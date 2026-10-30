package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

const coreLongerByMs int64 = 500

// A recording reported as started with no ready or cancel after this long is
// treated as cancelled, so a lost message cannot hold its talkgroup open.
const coreOpenLimit = 2 * time.Minute

// The longest a finished call waits for an earlier call on its talkgroup to
// finish uploading, so playback keeps air order without a backlog.
const coreOrderCap = 4 * time.Second

// A best copy shorter than coreShortCopyMs is often a fragment from a site that
// caught only the key-up; other sites' full copies report a few hundred ms later.
const coreShortCopyMs int64 = 1000
const coreShortSettle = 1 * time.Second

// coreHandoffSlop allows for SDRTrunk's call ends being up to a voice frame
// block off: a copy starting this close to a finished call's end is the next key-up.
const coreHandoffSlop int64 = 100

type coreMember struct {
	keyId       uint64
	recordingId string
	system      int
	talkgroup   int
	radioId     int
	patches     map[int]bool
	frequency   uint
	startMs     int64
	durationMs  int64
	quality     int
	frames      int
	opened      time.Time
	ready       bool
	cancelled   bool
	failed      bool
	asked       bool
}

type coreGroup struct {
	members    []*coreMember
	ordinary   *Call
	ordinaryMs int64
	updated    time.Time
	readySince time.Time
	closed     bool
	deciding   bool
	piece      bool
}

// coreSpan remembers a transmission that already chose a winner, so a late
// copy is discarded instead of uploaded again.
type coreSpan struct {
	at      time.Time
	members []coreMember
}

type coreCoordinator struct {
	controller *Controller
	mu         sync.Mutex
	systems    map[uint64]map[int]bool
	groups     []*coreGroup
	reserved   map[string]uint64
	recent     []coreSpan
}

func (controller *Controller) core() *coreCoordinator {
	controller.coreOnce.Do(func() {
		controller.coreCoord = &coreCoordinator{
			controller: controller,
			systems:    map[uint64]map[int]bool{},
			reserved:   map[string]uint64{},
		}
		go controller.coreCoord.sweep()
	})
	return controller.coreCoord
}

func (core *coreCoordinator) sweep() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		core.mu.Lock()
		pending := core.expireOpenLocked(time.Now())
		core.mu.Unlock()
		core.wake(pending)
	}
}

func (core *coreCoordinator) expireOpenLocked(now time.Time) []*coreGroup {
	var pending []*coreGroup
	for _, group := range core.groups {
		if group == nil || group.closed {
			continue
		}
		changed := false
		for _, member := range group.members {
			if member.ready || member.cancelled || member.failed || member.opened.IsZero() {
				continue
			}
			if now.Sub(member.opened) > coreOpenLimit {
				log.Printf("core: recording %s never finished, releasing talkgroup %d", member.recordingId, member.talkgroup)
				member.cancelled = true
				changed = true
			}
		}
		if changed {
			pending = append(pending, group)
		}
	}
	return pending
}

// allowSiteUpload reports whether a site key may upload the recording, and
// whether it is a fallback upload the server never asked for.
func (core *coreCoordinator) allowSiteUpload(keyId uint64, recordingId string) (allowed bool, fallback bool) {
	if recordingId == "" {
		return false, false
	}
	core.mu.Lock()
	reserved := core.reserved[recordingId]
	core.mu.Unlock()
	if reserved == keyId {
		return true, false
	}
	if reserved == 0 && !core.controller.sdrtrunkHub().connected(keyId) {
		return true, true
	}
	return false, false
}

func (core *coreCoordinator) offerOrdinary(call *Call) bool {
	if call == nil || call.CoreRecordingId != "" || call.SystemId == 0 || call.TalkgroupId == 0 || len(call.Audio) == 0 {
		return false
	}
	core.mu.Lock()
	group := core.matchingOpen(int(call.SystemId), int(call.TalkgroupId), int(callPrimarySource(call)), uintPatches(call.Patches), 0, 0)
	core.mu.Unlock()
	if group == nil {
		return false
	}

	ms := int64(call.Duration * 1000)
	if ms <= 0 {
		if duration, err := core.controller.getAudioDuration(call.Audio, call.AudioMime); err == nil && duration > 0 {
			call.Duration = duration
			ms = int64(duration * 1000)
		}
	}

	core.mu.Lock()
	group.ordinary = call
	group.ordinaryMs = ms
	group.updated = time.Now()
	core.mu.Unlock()
	core.schedule(group)
	return true
}

func (core *coreCoordinator) onMessage(keyId uint64, kind string, payload []byte) {
	var body struct {
		System      int    `json:"system"`
		Talkgroup   int    `json:"talkgroup"`
		RadioId     int    `json:"radioId"`
		Frequency   uint   `json:"frequency"`
		Start       int64  `json:"start"`
		Duration    int64  `json:"duration"`
		RecordingId string `json:"recordingId"`
		Patches     []int  `json:"patches"`
		Quality     *int   `json:"quality"`
		Frames      int    `json:"frames"`
	}
	if json.Unmarshal(payload, &body) != nil {
		return
	}

	core.mu.Lock()
	defer core.mu.Unlock()

	switch kind {
	case "core.hello":
		core.addSystemLocked(keyId, body.System)
		return
	case "core.bye":
		pending := core.removeSystemLocked(keyId, body.System)
		go func() {
			for _, group := range pending {
				core.schedule(group)
			}
		}()
		return
	}

	if body.RecordingId == "" || body.System == 0 {
		return
	}
	// SDRTrunk's cancel carries only the recording id, so it is matched before the talkgroup check.
	if kind == "call.cancelled" {
		if group, member := core.cancelLocked(keyId, body.RecordingId); group != nil {
			log.Printf("core: call.cancelled from %s tg %d start %s %.4fMHz, call has %d copies",
				core.siteName(keyId), member.talkgroup, coreClock(member.startMs),
				float64(member.frequency)/1e6, len(group.members))
			go core.schedule(group)
		}
		return
	}
	if body.Talkgroup == 0 {
		if kind != "call.started" {
			recordingId := body.RecordingId
			go core.discard([]coreMember{{keyId: keyId, recordingId: recordingId}})
		}
		return
	}

	if core.groupByRecordingLocked(body.RecordingId) == nil &&
		core.suppressedLocked(keyId, body.System, body.Talkgroup, body.RadioId, patchSet(body.Talkgroup, body.Patches), body.Start, body.Duration) {
		log.Printf("core: sys %d tg %d late %s from %s (start %s), call already chosen, discarding",
			body.System, body.Talkgroup, kind, core.siteName(keyId), coreClock(body.Start))
		go core.discard([]coreMember{{keyId: keyId, recordingId: body.RecordingId}})
		return
	}

	group := core.groupForLocked(body.RecordingId, body.System, body.Talkgroup, body.RadioId, body.Patches, body.Start, body.Duration)
	member := core.memberLocked(group, body.RecordingId)
	if member == nil {
		member = &coreMember{
			keyId:       keyId,
			recordingId: body.RecordingId,
			system:      body.System,
			talkgroup:   body.Talkgroup,
			radioId:     body.RadioId,
			patches:     patchSet(body.Talkgroup, body.Patches),
			frequency:   body.Frequency,
			startMs:     body.Start,
			durationMs:  body.Duration,
			quality:     -1,
			opened:      time.Now(),
		}
		group.members = append(group.members, member)
	}
	if body.Quality != nil {
		member.quality = *body.Quality
		member.frames = body.Frames
	}
	member.radioId = body.RadioId
	member.patches = patchSet(body.Talkgroup, body.Patches)
	member.frequency = body.Frequency
	member.startMs = body.Start
	if body.Duration > 0 {
		member.durationMs = body.Duration
	}

	switch kind {
	case "call.ready":
		member.ready = true
		member.cancelled = false
	case "call.cancelled":
		member.cancelled = true
		member.ready = false
	case "call.started":
		if !member.ready {
			member.cancelled = false
		}
	}
	group.updated = time.Now()
	group = core.absorbLocked(group)
	log.Printf("core: %s from %s tg %d start %s +%dms %.4fMHz radio %d%s, call has %d copies",
		kind, core.siteName(keyId), body.Talkgroup, coreClock(body.Start), body.Duration,
		float64(body.Frequency)/1e6, body.RadioId, coreQuality(member), len(group.members))
	go core.schedule(group)
}

func (core *coreCoordinator) stored(call *Call) {
	if call == nil || call.CoreRecordingId == "" {
		return
	}
	core.mu.Lock()
	group := core.groupByRecordingLocked(call.CoreRecordingId)
	if group == nil || group.closed {
		delete(core.reserved, call.CoreRecordingId)
		core.mu.Unlock()
		return
	}
	if next := core.splitContinuationLocked(group, call.CoreRecordingId); next != nil {
		log.Printf("core: %s keeping %s's later piece as its own call %s", core.labelLocked(group), core.siteName(next.members[0].keyId), core.labelLocked(next))
	}
	discards := core.discardListLocked(group, call.CoreRecordingId)
	delete(core.reserved, call.CoreRecordingId)
	if member := core.memberByIdLocked(group, call.CoreRecordingId); member != nil {
		log.Printf("core: %s stored from %s, discarding %d other copies", core.labelLocked(group), core.siteName(member.keyId), len(discards))
	}
	pending := core.finishLocked(group, true)
	core.mu.Unlock()
	core.discard(discards)
	core.wake(pending)
}

func (core *coreCoordinator) rejected(call *Call) {
	if call == nil || call.CoreRecordingId == "" {
		return
	}
	core.mu.Lock()
	group := core.groupByRecordingLocked(call.CoreRecordingId)
	member := core.memberByIdLocked(group, call.CoreRecordingId)
	if group == nil || member == nil || group.closed {
		delete(core.reserved, call.CoreRecordingId)
		core.mu.Unlock()
		return
	}
	member.failed = true
	member.asked = false
	delete(core.reserved, call.CoreRecordingId)
	reason := call.CoreRejectReason
	if reason == "" {
		reason = "not stored"
	}
	if call.IsDuplicate || coreRejectsEverySite(reason) {
		discards := core.discardListLocked(group, call.CoreRecordingId)
		log.Printf("core: %s copy from %s rejected by TLR (%s), every copy would be, discarding %d others",
			core.labelLocked(group), core.siteName(member.keyId), reason, len(discards))
		pending := core.finishLocked(group, true)
		core.mu.Unlock()
		core.discard(discards)
		core.wake(pending)
		return
	}
	group.deciding = false
	log.Printf("core: %s copy from %s rejected by TLR (%s), trying next site", core.labelLocked(group), core.siteName(member.keyId), reason)
	core.mu.Unlock()
	core.schedule(group)
}

// coreRejectsEverySite is true when TLR turned a copy away for something about
// the call itself, so another site's copy would be turned away too.
func coreRejectsEverySite(reason string) bool {
	return strings.HasPrefix(reason, "blacklisted") ||
		strings.HasPrefix(reason, "duplicate") ||
		reason == "hidden channel" ||
		reason == "no matching system/talkgroup"
}

func (core *coreCoordinator) sessionClosed(keyId uint64) {
	core.mu.Lock()
	delete(core.systems, keyId)
	var pending []*coreGroup
	for _, group := range core.groups {
		if group.closed {
			continue
		}
		changed := false
		for _, member := range group.members {
			if member.keyId == keyId && !member.ready && !member.cancelled {
				member.cancelled = true
				changed = true
			}
		}
		if changed {
			pending = append(pending, group)
		}
	}
	core.mu.Unlock()
	for _, group := range pending {
		core.schedule(group)
	}
}

func (core *coreCoordinator) schedule(group *coreGroup) {
	core.mu.Lock()
	if group == nil || group.closed {
		core.mu.Unlock()
		return
	}
	group = core.absorbLocked(group)
	if group == nil || group.closed || group.deciding {
		core.mu.Unlock()
		return
	}
	if !core.readyLocked(group) {
		if next := core.splitLaterKeyupLocked(group); next != nil {
			log.Printf("core: %s split off next key-up %s", core.labelLocked(group), core.labelLocked(next))
			core.mu.Unlock()
			go core.schedule(next)
			core.schedule(group)
			return
		}
		group.readySince = time.Time{}
		var pending []*coreGroup
		if core.abandonedLocked(group) {
			pending = core.finishLocked(group, false)
		}
		core.mu.Unlock()
		core.wake(pending)
		return
	}
	if group.readySince.IsZero() {
		group.readySince = time.Now()
	}
	if next := core.splitBackToBackLocked(group); next != nil {
		log.Printf("core: %s split off back-to-back call %s", core.labelLocked(group), core.labelLocked(next))
		go core.schedule(next)
	}
	if next := core.splitPiecesLocked(group); next != nil {
		log.Printf("core: %s split off separate piece %s", core.labelLocked(group), core.labelLocked(next))
		go core.schedule(next)
	}
	if group.closed || len(group.members) == 0 {
		core.mu.Unlock()
		return
	}
	if core.earlierUploadPendingLocked(group) {
		if wait := coreOrderCap - time.Since(group.readySince); wait > 0 {
			log.Printf("core: %s waiting up to %dms for an earlier call to upload", core.labelLocked(group), wait.Milliseconds())
			core.mu.Unlock()
			time.AfterFunc(wait, func() { core.schedule(group) })
			return
		}
	}
	if best := core.winnerLocked(group); best != nil && best.durationMs < coreShortCopyMs {
		if wait := coreShortSettle - time.Since(group.readySince); wait > 0 {
			log.Printf("core: %s best copy is %dms, waiting up to %dms for other sites", core.labelLocked(group), best.durationMs, wait.Milliseconds())
			core.mu.Unlock()
			time.AfterFunc(wait, func() { core.schedule(group) })
			return
		}
	}
	group.deciding = true
	ordinary := group.ordinary
	winner := core.winnerLocked(group)
	var discards []coreMember
	if winner == nil && ordinary != nil {
		log.Printf("core: %s [%s] keeping ordinary upload (%dms)", core.labelLocked(group), core.membersLocked(group), group.ordinaryMs)
		discards = core.discardListLocked(group, "")
		pending := core.finishLocked(group, true)
		core.mu.Unlock()
		core.discard(discards)
		core.wake(pending)
		select {
		case core.controller.Ingest <- ordinary:
		default:
			log.Printf("core: server busy, ordinary recording was not stored")
		}
		return
	}
	if winner == nil {
		if core.someoneAskedLocked(group) {
			group.deciding = true
			core.mu.Unlock()
			return
		}
		log.Printf("core: %s [%s] no copy left to upload", core.labelLocked(group), core.membersLocked(group))
		pending := core.finishLocked(group, false)
		core.mu.Unlock()
		core.wake(pending)
		return
	}
	log.Printf("core: %s [%s] asking %s (%dms)", core.labelLocked(group), core.membersLocked(group), core.siteName(winner.keyId), winner.durationMs)
	winner.asked = true
	core.reserved[winner.recordingId] = winner.keyId
	keyId := winner.keyId
	recordingId := winner.recordingId
	core.mu.Unlock()

	_, err := core.controller.sdrtrunkHub().command(keyId, map[string]any{
		"command":     "uploadCall",
		"recordingId": recordingId,
	})
	if err != nil {
		log.Printf("core: upload request failed for recording %s: %v", recordingId, err)
		core.failAsk(group, winner, recordingId)
		return
	}
	time.AfterFunc(45*time.Second, func() {
		core.mu.Lock()
		if group.closed || !winner.asked || winner.failed {
			core.mu.Unlock()
			return
		}
		core.mu.Unlock()
		log.Printf("core: timed out waiting for recording %s", recordingId)
		core.failAsk(group, winner, recordingId)
	})
}

func (core *coreCoordinator) failAsk(group *coreGroup, winner *coreMember, recordingId string) {
	core.mu.Lock()
	if group.closed {
		core.mu.Unlock()
		return
	}
	winner.failed = true
	winner.asked = false
	group.deciding = false
	// Keep the reservation briefly. The recorder may already be uploading
	// this copy, and rejecting it is how a call disappears.
	core.mu.Unlock()
	core.schedule(group)
	time.AfterFunc(2*time.Minute, func() {
		core.mu.Lock()
		if core.reserved[recordingId] == winner.keyId {
			delete(core.reserved, recordingId)
		}
		core.mu.Unlock()
	})
}

func (core *coreCoordinator) siteName(keyId uint64) string {
	if core.controller != nil && core.controller.Apikeys != nil {
		if apikey, ok := core.controller.Apikeys.GetById(keyId); ok && apikey.Ident != "" {
			return fmt.Sprintf("'%s'", apikey.Ident)
		}
	}
	return fmt.Sprintf("site %d", keyId)
}

func coreClock(ms int64) string {
	if ms <= 0 {
		return "?"
	}
	return time.UnixMilli(ms).Format("15:04:05.000")
}

// labelLocked names a call for the log: system, talkgroup, and air start time.
func (core *coreCoordinator) labelLocked(group *coreGroup) string {
	if group == nil || len(group.members) == 0 {
		return "call"
	}
	first := group.members[0]
	return fmt.Sprintf("sys %d tg %d start %s", first.system, first.talkgroup, coreClock(groupStart(group)))
}

// membersLocked lists every copy of a call for the log.
func (core *coreCoordinator) membersLocked(group *coreGroup) string {
	parts := make([]string, 0, len(group.members))
	for _, member := range group.members {
		state := "open"
		switch {
		case member.failed:
			state = "failed"
		case member.cancelled:
			state = "cancelled"
		case member.ready:
			state = "ready"
		}
		parts = append(parts, fmt.Sprintf("%s tg %d %s +%dms %.4fMHz radio %d%s %s",
			core.siteName(member.keyId), member.talkgroup, coreClock(member.startMs), member.durationMs,
			float64(member.frequency)/1e6, member.radioId, coreQuality(member), state))
	}
	return strings.Join(parts, "; ")
}

// coreQuality is the decode score SDRTrunk reported: the share of digital
// voice frames that arrived clean.
func coreQuality(member *coreMember) string {
	if member == nil || member.quality < 0 || member.frames <= 0 {
		return ""
	}
	return fmt.Sprintf(" quality %d%% of %d frames", member.quality, member.frames)
}

func (core *coreCoordinator) discard(members []coreMember) {
	for _, member := range members {
		if _, err := core.controller.sdrtrunkHub().command(member.keyId, map[string]any{
			"command":     "discardCall",
			"recordingId": member.recordingId,
		}); err != nil {
			log.Printf("core: discard failed for recording %s: %v", member.recordingId, err)
		}
	}
}

func (core *coreCoordinator) addSystemLocked(keyId uint64, system int) {
	if system == 0 {
		return
	}
	set := core.systems[keyId]
	if set == nil {
		set = map[int]bool{}
		core.systems[keyId] = set
	}
	set[system] = true
}

func (core *coreCoordinator) removeSystemLocked(keyId uint64, system int) []*coreGroup {
	set := core.systems[keyId]
	if set == nil {
		return nil
	}
	delete(set, system)
	if len(set) == 0 {
		delete(core.systems, keyId)
	}
	var pending []*coreGroup
	for _, group := range core.groups {
		if group.closed {
			continue
		}
		changed := false
		for _, member := range group.members {
			if member.keyId == keyId && member.system == system && !member.ready && !member.cancelled {
				member.cancelled = true
				changed = true
			}
		}
		if changed {
			pending = append(pending, group)
		}
	}
	return pending
}

func (core *coreCoordinator) groupForLocked(recordingId string, system int, talkgroup int, radioId int, patches []int, start int64, duration int64) *coreGroup {
	if group := core.groupByRecordingLocked(recordingId); group != nil {
		return group
	}
	if group := core.matchingOpen(system, talkgroup, radioId, patches, start, duration); group != nil {
		return group
	}
	group := &coreGroup{}
	core.groups = append(core.groups, group)
	return group
}

func (core *coreCoordinator) matchingOpen(system int, talkgroup int, radioId int, callPatches []int, start int64, duration int64) *coreGroup {
	incoming := patchSet(talkgroup, callPatches)
	for _, group := range core.groups {
		if group.closed || group.piece {
			continue
		}
		if core.groupActivelyReceivingLocked(group, system, talkgroup, incoming) {
			return group
		}
		if end := finishedEnd(group); end > 0 && start > 0 && start >= end-coreHandoffSlop {
			continue
		}
		for _, member := range group.members {
			if transmissionMatch(member, system, talkgroup, radioId, incoming, start, duration) {
				return group
			}
		}
	}
	return nil
}

func (core *coreCoordinator) groupActivelyReceivingLocked(group *coreGroup, system int, talkgroup int, incoming map[int]bool) bool {
	if group == nil || group.closed {
		return false
	}
	for _, member := range group.members {
		if member == nil || member.cancelled || member.failed || member.ready || member.system != system {
			continue
		}
		if coreTalkgroupsOverlap(member.patches, incoming) || member.talkgroup == talkgroup {
			return true
		}
	}
	return false
}

func (core *coreCoordinator) absorbLocked(group *coreGroup) *coreGroup {
	if group == nil {
		return nil
	}
	for {
		other := core.mergeCandidateLocked(group)
		if other == nil {
			return group
		}
		dest, src := group, other
		if start := groupStart(other); start > 0 && (groupStart(group) == 0 || start < groupStart(group)) {
			dest, src = other, group
		}
		dest.members = append(dest.members, src.members...)
		if src.updated.After(dest.updated) {
			dest.updated = src.updated
		}
		dest.readySince = time.Time{}
		src.members = nil
		src.closed = true
		core.dropGroupLocked(src)
		group = dest
	}
}

func (core *coreCoordinator) mergeCandidateLocked(group *coreGroup) *coreGroup {
	if group.piece {
		return nil
	}
	for _, other := range core.groups {
		if other == nil || other == group || other.closed || other.piece {
			continue
		}
		if groupsMatch(group, other) {
			return other
		}
	}
	return nil
}

func (core *coreCoordinator) splitBackToBackLocked(group *coreGroup) *coreGroup {
	if group == nil || len(group.members) < 2 || !core.readyLocked(group) {
		return nil
	}
	// Keep every copy linked to the first one through any chain of matches.
	// groupsMatch merges groups when any pair matches, so a copy that matches
	// any kept copy would be merged straight back in after the split.
	kept := []*coreMember{group.members[0]}
	moved := append([]*coreMember(nil), group.members[1:]...)
	for changed := true; changed; {
		changed = false
		for i := 0; i < len(moved); i++ {
			if membersMatch(kept, moved[i]) {
				kept = append(kept, moved[i])
				moved = append(moved[:i], moved[i+1:]...)
				i--
				changed = true
			}
		}
	}
	if len(moved) == 0 {
		return nil
	}
	group.members = kept
	next := &coreGroup{members: moved, updated: time.Now(), readySince: group.readySince}
	core.groups = append(core.groups, next)
	return next
}

// splitLaterKeyupLocked moves recordings still in progress that started after
// every finished copy ended into their own call. Without a radio id, a late
// receiver's copy can end a few milliseconds into the next key-up.
func (core *coreCoordinator) splitLaterKeyupLocked(group *coreGroup) *coreGroup {
	if group == nil || len(group.members) < 2 {
		return nil
	}
	var end int64
	for _, member := range group.members {
		if member.ready && !member.cancelled && !member.failed && member.startMs > 0 && member.durationMs > 0 {
			if e := member.startMs + member.durationMs; e > end {
				end = e
			}
		}
	}
	if end == 0 {
		return nil
	}
	var kept []*coreMember
	var moved []*coreMember
	for _, member := range group.members {
		if member.ready || member.cancelled || member.failed {
			kept = append(kept, member)
			continue
		}
		if member.startMs <= 0 || member.startMs < end-coreHandoffSlop {
			return nil
		}
		moved = append(moved, member)
	}
	if len(moved) == 0 {
		return nil
	}
	group.members = kept
	next := &coreGroup{members: moved, updated: time.Now()}
	core.groups = append(core.groups, next)
	return next
}

// finishedEnd is when the last copy of a call ended, or 0 while any copy is
// still recording.
func finishedEnd(group *coreGroup) int64 {
	var end int64
	for _, member := range group.members {
		if member == nil || member.cancelled || member.failed {
			continue
		}
		if !member.ready || member.startMs <= 0 || member.durationMs <= 0 {
			return 0
		}
		if e := member.startMs + member.durationMs; e > end {
			end = e
		}
	}
	return end
}

func followsFinished(earlier *coreGroup, later *coreGroup) bool {
	end := finishedEnd(earlier)
	start := groupStart(later)
	return end > 0 && start > 0 && start >= end-coreHandoffSlop
}

// splitPiecesLocked moves recordings that share no audio with the longest copy
// into their own call. A lone site that dropped and regained a transmission
// has two pieces, and both have to play.
func (core *coreCoordinator) splitPiecesLocked(group *coreGroup) *coreGroup {
	if group == nil || len(group.members) < 2 || !core.readyLocked(group) {
		return nil
	}
	anchor := core.longestLocked(group)
	if anchor == nil {
		return nil
	}
	var kept []*coreMember
	var moved []*coreMember
	for _, member := range group.members {
		if member == anchor || member.cancelled || member.failed || coreSharesAudio(anchor, member) {
			kept = append(kept, member)
		} else {
			moved = append(moved, member)
		}
	}
	if len(moved) == 0 {
		return nil
	}
	group.members = kept
	next := &coreGroup{members: moved, updated: time.Now(), readySince: group.readySince, piece: true}
	core.groups = append(core.groups, next)
	return next
}

// splitContinuationLocked moves recordings from the stored copy's site that
// started after it ended into a new call. They joined while the stored copy was
// uploading, and are the rest of the transmission after that site dropped.
func (core *coreCoordinator) splitContinuationLocked(group *coreGroup, storedId string) *coreGroup {
	stored := core.memberByIdLocked(group, storedId)
	if stored == nil || stored.startMs <= 0 || stored.durationMs <= 0 {
		return nil
	}
	end := stored.startMs + stored.durationMs
	var kept []*coreMember
	var moved []*coreMember
	for _, member := range group.members {
		if member != stored && member.keyId == stored.keyId && !member.cancelled && member.startMs >= end {
			moved = append(moved, member)
		} else {
			kept = append(kept, member)
		}
	}
	if len(moved) == 0 {
		return nil
	}
	group.members = kept
	next := &coreGroup{members: moved, updated: time.Now()}
	core.groups = append(core.groups, next)
	return next
}

func (core *coreCoordinator) longestLocked(group *coreGroup) *coreMember {
	var best *coreMember
	for _, member := range group.members {
		if !member.ready || member.cancelled || member.failed {
			continue
		}
		if best == nil || member.durationMs > best.durationMs {
			best = member
		}
	}
	return best
}

// coreSharesAudio is true when two finished recordings hold the same audio.
// Recordings from one computer share a clock, so they must truly overlap.
// Other computers get a little slack for clock drift.
func coreSharesAudio(a *coreMember, b *coreMember) bool {
	if a.startMs <= 0 || b.startMs <= 0 || a.durationMs <= 0 || b.durationMs <= 0 {
		return true
	}
	if a.keyId != b.keyId {
		return coreAudioOverlaps(a.startMs, a.durationMs, b.startMs, b.durationMs)
	}
	aEnd := a.startMs + a.durationMs
	bEnd := b.startMs + b.durationMs
	return a.startMs < bEnd && b.startMs < aEnd
}

func (core *coreCoordinator) earlierUploadPendingLocked(group *coreGroup) bool {
	start := groupStart(group)
	if start == 0 {
		return false
	}
	for _, other := range core.groups {
		if other == nil || other == group || other.closed || !other.deciding {
			continue
		}
		if s := groupStart(other); s > 0 && s < start && groupsShareTalkgroup(group, other) {
			return true
		}
	}
	return false
}

func groupsShareTalkgroup(a *coreGroup, b *coreGroup) bool {
	for _, left := range a.members {
		for _, right := range b.members {
			if left.system == right.system && (left.talkgroup == right.talkgroup || coreTalkgroupsOverlap(left.patches, right.patches)) {
				return true
			}
		}
	}
	return false
}

// suppressedLocked is true when a report is another copy of a call that already
// uploaded. A site's own recording that starts after its stored copy ended is
// the rest of the transmission after a drop, not a copy, so it still plays.
func (core *coreCoordinator) suppressedLocked(keyId uint64, system int, talkgroup int, radioId int, patches map[int]bool, start int64, duration int64) bool {
	cutoff := time.Now().Add(-5 * time.Second)
	fresh := make([]coreSpan, 0, len(core.recent))
	for _, span := range core.recent {
		if !span.at.Before(cutoff) {
			fresh = append(fresh, span)
		}
	}
	core.recent = fresh
	for _, span := range core.recent {
		for i := range span.members {
			member := &span.members[i]
			if nextUnitOutlasts(member, radioId, start, duration) {
				continue
			}
			if member.durationMs < coreShortCopyMs && (coreLonger(duration, member.durationMs) ||
				(duration <= 0 && (member.radioId == 0 || member.radioId != radioId))) {
				continue
			}
			if keyId != 0 && member.keyId == keyId {
				if closedOverlapSameSite(member, system, talkgroup, patches, start, duration) {
					return true
				}
				continue
			}
			if closedOverlap(member, system, talkgroup, patches, start, duration) {
				return true
			}
		}
	}
	return false
}

func (core *coreCoordinator) finishLocked(group *coreGroup, remember bool) []*coreGroup {
	if group == nil || group.closed {
		return nil
	}
	group.closed = true
	if remember {
		core.rememberLocked(group)
	}
	core.dropGroupLocked(group)
	return core.openGroupsLocked()
}

func (core *coreCoordinator) rememberLocked(group *coreGroup) {
	var members []coreMember
	for _, member := range group.members {
		if member == nil || member.cancelled || !member.ready {
			continue
		}
		members = append(members, *member)
	}
	if len(members) == 0 && group.ordinary == nil {
		return
	}
	core.recent = append(core.recent, coreSpan{at: time.Now(), members: members})
}

func (core *coreCoordinator) openGroupsLocked() []*coreGroup {
	pending := make([]*coreGroup, 0, len(core.groups))
	for _, group := range core.groups {
		if group != nil && !group.closed {
			pending = append(pending, group)
		}
	}
	return pending
}

func (core *coreCoordinator) wake(groups []*coreGroup) {
	for _, group := range groups {
		go core.schedule(group)
	}
}

func (core *coreCoordinator) someoneAskedLocked(group *coreGroup) bool {
	for _, member := range group.members {
		if member.asked && !member.failed && !member.cancelled {
			return true
		}
	}
	return false
}

func (core *coreCoordinator) abandonedLocked(group *coreGroup) bool {
	if group.ordinary != nil {
		return false
	}
	for _, member := range group.members {
		if !member.cancelled && !member.failed {
			return false
		}
	}
	return true
}

func (core *coreCoordinator) readyLocked(group *coreGroup) bool {
	if group == nil {
		return false
	}
	sawReady := group.ordinary != nil
	for _, member := range group.members {
		if member.cancelled || member.failed {
			continue
		}
		if !member.ready {
			return false
		}
		sawReady = true
	}
	return sawReady
}

func (core *coreCoordinator) winnerLocked(group *coreGroup) *coreMember {
	var best *coreMember
	for _, member := range group.members {
		if !member.ready || member.failed || member.cancelled || member.asked {
			continue
		}
		if best == nil || coreBetter(member, best) {
			best = member
		}
	}
	if group.ordinary != nil && (best == nil || coreLonger(group.ordinaryMs, best.durationMs)) {
		return nil
	}
	return best
}

func (core *coreCoordinator) discardListLocked(group *coreGroup, keep string) []coreMember {
	var list []coreMember
	for _, member := range group.members {
		if member.recordingId == "" || member.recordingId == keep || member.cancelled {
			continue
		}
		copied := *member
		list = append(list, copied)
	}
	return list
}

func (core *coreCoordinator) groupByRecordingLocked(recordingId string) *coreGroup {
	if recordingId == "" {
		return nil
	}
	for _, group := range core.groups {
		if core.memberByIdLocked(group, recordingId) != nil {
			return group
		}
	}
	return nil
}

func (core *coreCoordinator) memberByIdLocked(group *coreGroup, recordingId string) *coreMember {
	if group == nil {
		return nil
	}
	for _, member := range group.members {
		if member.recordingId == recordingId {
			return member
		}
	}
	return nil
}

func (core *coreCoordinator) memberLocked(group *coreGroup, recordingId string) *coreMember {
	return core.memberByIdLocked(group, recordingId)
}

func (core *coreCoordinator) cancelLocked(keyId uint64, recordingId string) (*coreGroup, *coreMember) {
	group := core.groupByRecordingLocked(recordingId)
	member := core.memberByIdLocked(group, recordingId)
	if member == nil || member.keyId != keyId || member.ready {
		return nil, nil
	}
	member.cancelled = true
	group.updated = time.Now()
	return group, member
}

func (core *coreCoordinator) dropGroupLocked(group *coreGroup) {
	next := core.groups[:0]
	for _, candidate := range core.groups {
		if candidate != group {
			next = append(next, candidate)
		}
	}
	core.groups = next
}

func uintPatches(values []uint) []int {
	patches := make([]int, 0, len(values))
	for _, value := range values {
		patches = append(patches, int(value))
	}
	return patches
}

func patchSet(talkgroup int, patches []int) map[int]bool {
	set := map[int]bool{}
	if talkgroup > 0 {
		set[talkgroup] = true
	}
	for _, patch := range patches {
		if patch > 0 {
			set[patch] = true
		}
	}
	return set
}

func coreTalkgroupsOverlap(a map[int]bool, b map[int]bool) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for value := range a {
		if b[value] {
			return true
		}
	}
	return false
}

func coreRadiosSplit(a int, b int) bool {
	return a > 0 && b > 0 && a != b
}

// transmissionMatch is true when two reports are one transmission.
// Different radio IDs still match when the audio overlaps or both copies are
// still opening within a second of each other. Back-to-back units stay apart.
// A copy still on the air only joins a finished copy it starts inside, so a
// quick re-key is its own transmission and every site receiving it is waited on.
func transmissionMatch(member *coreMember, system int, talkgroup int, radioId int, patches map[int]bool, start int64, duration int64) bool {
	if member == nil || member.cancelled || member.system != system {
		return false
	}
	incoming := patches
	if incoming == nil {
		incoming = patchSet(talkgroup, nil)
	}
	if !coreTalkgroupsOverlap(member.patches, incoming) && member.talkgroup != talkgroup {
		return false
	}
	if start == 0 || member.startMs == 0 {
		return !coreRadiosSplit(member.radioId, radioId)
	}
	if member.durationMs <= 0 && duration <= 0 {
		return abs64(member.startMs-start) <= 1000
	}
	if member.durationMs <= 0 {
		if abs64(member.startMs-start) <= 1000 {
			return true
		}
		return member.startMs >= start-250 && member.startMs <= start+duration+250
	}
	if duration <= 0 {
		if abs64(member.startMs-start) <= 1000 {
			return true
		}
		return start >= member.startMs-250 && start <= member.startMs+member.durationMs+250
	}
	if coreAudioOverlaps(member.startMs, member.durationMs, start, duration) {
		return true
	}
	if coreRadiosSplit(member.radioId, radioId) {
		return false
	}
	return coreIntervalsOverlap(member.startMs, member.durationMs, start, duration)
}

func membersMatch(kept []*coreMember, member *coreMember) bool {
	for _, other := range kept {
		if transmissionMatch(other, member.system, member.talkgroup, member.radioId, member.patches, member.startMs, member.durationMs) ||
			transmissionMatch(member, other.system, other.talkgroup, other.radioId, other.patches, other.startMs, other.durationMs) {
			return true
		}
	}
	return false
}

func groupsMatch(a *coreGroup, b *coreGroup) bool {
	if a == nil || b == nil || followsFinished(a, b) || followsFinished(b, a) {
		return false
	}
	for _, left := range a.members {
		for _, right := range b.members {
			if transmissionMatch(left, right.system, right.talkgroup, right.radioId, right.patches, right.startMs, right.durationMs) {
				return true
			}
		}
	}
	return false
}

func groupStart(group *coreGroup) int64 {
	if group == nil {
		return 0
	}
	var start int64
	for _, member := range group.members {
		if member == nil || member.startMs <= 0 {
			continue
		}
		if start == 0 || member.startMs < start {
			start = member.startMs
		}
	}
	return start
}

func closedOverlap(member *coreMember, system int, talkgroup int, patches map[int]bool, start int64, duration int64) bool {
	if member == nil || member.system != system || start <= 0 || member.startMs <= 0 || member.durationMs <= 0 {
		return false
	}
	incoming := patches
	if incoming == nil {
		incoming = patchSet(talkgroup, nil)
	}
	if !coreTalkgroupsOverlap(member.patches, incoming) && member.talkgroup != talkgroup {
		return false
	}
	// Another site's copy of the same audio starts before this one ends; a start
	// after the end is the next transmission, even within the clock slop.
	end := member.startMs + member.durationMs
	if start >= end {
		return false
	}
	if duration > 0 {
		return coreAudioOverlaps(member.startMs, member.durationMs, start, duration)
	}
	return start >= member.startMs-250
}

// closedOverlapSameSite compares two recordings from one computer, which share a
// clock, so only audio that truly overlaps counts.
func closedOverlapSameSite(member *coreMember, system int, talkgroup int, patches map[int]bool, start int64, duration int64) bool {
	if member == nil || member.system != system || start <= 0 || member.startMs <= 0 || member.durationMs <= 0 {
		return false
	}
	incoming := patches
	if incoming == nil {
		incoming = patchSet(talkgroup, nil)
	}
	if !coreTalkgroupsOverlap(member.patches, incoming) && member.talkgroup != talkgroup {
		return false
	}
	end := member.startMs + member.durationMs
	if duration > 0 {
		return start < end && member.startMs < start+duration
	}
	return start >= member.startMs && start < end
}

// nextUnitOutlasts is true for a finished recording from a different radio that
// runs well past the stored copy: the next key-up starting a moment early.
func nextUnitOutlasts(member *coreMember, radioId int, start int64, duration int64) bool {
	if member == nil || duration <= 0 || !coreRadiosSplit(member.radioId, radioId) {
		return false
	}
	return start+duration > member.startMs+member.durationMs+500
}

func coreAudioOverlaps(aStart int64, aDuration int64, bStart int64, bDuration int64) bool {
	if aStart == 0 || bStart == 0 || aDuration <= 0 || bDuration <= 0 {
		return false
	}
	const slop int64 = 250
	aEnd := aStart + aDuration
	bEnd := bStart + bDuration
	return aStart <= bEnd+slop && bStart <= aEnd+slop
}

func abs64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func coreLonger(candidate int64, current int64) bool {
	return candidate > current+coreLongerByMs
}

// coreBetter prefers the clearly longer copy. Copies within coreLongerByMs of
// each other are the same transmission heard equally long, so the cleaner
// decode wins when both sites reported a score.
func coreBetter(candidate *coreMember, current *coreMember) bool {
	if coreLonger(candidate.durationMs, current.durationMs) {
		return true
	}
	if coreLonger(current.durationMs, candidate.durationMs) {
		return false
	}
	if candidate.quality < 0 || candidate.frames <= 0 || current.quality < 0 || current.frames <= 0 {
		return false
	}
	return candidate.quality > current.quality
}

func coreIntervalsOverlap(aStart int64, aDuration int64, bStart int64, bDuration int64) bool {
	if aStart == 0 || bStart == 0 {
		return true
	}
	if aDuration < 0 {
		aDuration = 0
	}
	if bDuration < 0 {
		bDuration = 0
	}
	aEnd := aStart + aDuration
	bEnd := bStart + bDuration
	const hang int64 = 1000
	return aStart <= bEnd+hang && bStart <= aEnd+hang
}

func (core *coreCoordinator) String() string {
	return fmt.Sprintf("core groups=%d", len(core.groups))
}
