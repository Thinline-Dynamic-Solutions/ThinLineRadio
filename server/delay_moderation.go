// Copyright (C) 2025 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>

package main

import (
	"encoding/json"
	"fmt"
	mimepkg "mime"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DelayModeration lets listeners an admin trusts review calls on delayed
// talkgroups and remove them before anyone hears them.
//
// A scope entry is "*" (every delayed talkgroup the user can hear),
// "<systemRef>:*" (a whole system) or "<systemRef>:<talkgroupRef>".
type DelayModeration struct {
	controller *Controller
	mutex      sync.Mutex
	scopes     map[uint64][]string
	held       map[uint64]*heldSends
	removed    map[uint64]time.Time
}

type heldSends struct {
	timers []*time.Timer
	until  time.Time
}

type DelayQueueCall struct {
	Id             uint64 `json:"id"`
	SystemId       uint   `json:"systemId"`
	SystemLabel    string `json:"systemLabel"`
	TalkgroupId    uint   `json:"talkgroupId"`
	TalkgroupLabel string `json:"talkgroupLabel"`
	TalkgroupName  string `json:"talkgroupName"`
	Timestamp      int64  `json:"timestamp"`
	ReleaseAt      int64  `json:"releaseAt"`
	Transcript     string `json:"transcript,omitempty"`
}

const delayModerationRemovedTTL = 24 * time.Hour

func NewDelayModeration(controller *Controller) *DelayModeration {
	return &DelayModeration{
		controller: controller,
		scopes:     map[uint64][]string{},
		held:       map[uint64]*heldSends{},
		removed:    map[uint64]time.Time{},
	}
}

func migrateDelayModerators(db *Database) error {
	query := `CREATE TABLE IF NOT EXISTS "delayModerators" (
    "userId" bigint NOT NULL PRIMARY KEY,
    "scope" text NOT NULL DEFAULT '[]',
    CONSTRAINT "delayModerators_userId" FOREIGN KEY ("userId") REFERENCES "users" ("userId") ON DELETE CASCADE ON UPDATE CASCADE
  )`
	if _, err := db.Sql.Exec(query); err != nil {
		return fmt.Errorf("migrateDelayModerators: %w", err)
	}
	return nil
}

func (dm *DelayModeration) Read(db *Database) error {
	rows, err := db.Sql.Query(`SELECT "userId", "scope" FROM "delayModerators"`)
	if err != nil {
		return fmt.Errorf("delaymoderation.read: %w", err)
	}
	defer rows.Close()

	scopes := map[uint64][]string{}
	for rows.Next() {
		var (
			userId uint64
			raw    string
		)
		if err := rows.Scan(&userId, &raw); err != nil {
			return fmt.Errorf("delaymoderation.read: %w", err)
		}
		var scope []string
		if json.Unmarshal([]byte(raw), &scope) == nil {
			if scope = normalizeModerationScope(scope); len(scope) > 0 {
				scopes[userId] = scope
			}
		}
	}

	dm.mutex.Lock()
	dm.scopes = scopes
	dm.mutex.Unlock()
	return rows.Err()
}

func normalizeModerationScope(scope []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, entry := range scope {
		entry = strings.ReplaceAll(strings.TrimSpace(entry), " ", "")
		if entry == "" || seen[entry] {
			continue
		}
		if entry != "*" {
			sys, tg, ok := strings.Cut(entry, ":")
			if !ok {
				continue
			}
			if _, err := strconv.ParseUint(sys, 10, 32); err != nil {
				continue
			}
			if tg != "*" {
				if _, err := strconv.ParseUint(tg, 10, 32); err != nil {
					continue
				}
			}
		}
		seen[entry] = true
		out = append(out, entry)
	}
	sort.Strings(out)
	return out
}

func moderationScopeAllows(scope []string, systemRef uint, talkgroupRef uint) bool {
	system := strconv.FormatUint(uint64(systemRef), 10)
	talkgroup := system + ":" + strconv.FormatUint(uint64(talkgroupRef), 10)
	for _, entry := range scope {
		if entry == "*" || entry == system+":*" || entry == talkgroup {
			return true
		}
	}
	return false
}

func (dm *DelayModeration) Scope(userId uint64) []string {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()
	return append([]string{}, dm.scopes[userId]...)
}

func (dm *DelayModeration) SetScope(db *Database, userId uint64, scope []string) ([]string, error) {
	scope = normalizeModerationScope(scope)
	if len(scope) == 0 {
		if _, err := db.Sql.Exec(`DELETE FROM "delayModerators" WHERE "userId" = $1`, userId); err != nil {
			return nil, err
		}
	} else {
		raw, _ := json.Marshal(scope)
		if _, err := db.Sql.Exec(`INSERT INTO "delayModerators" ("userId", "scope") VALUES ($1, $2) ON CONFLICT ("userId") DO UPDATE SET "scope" = EXCLUDED."scope"`, userId, string(raw)); err != nil {
			return nil, err
		}
	}

	dm.mutex.Lock()
	if len(scope) == 0 {
		delete(dm.scopes, userId)
	} else {
		dm.scopes[userId] = scope
	}
	dm.mutex.Unlock()
	return scope, nil
}

func (dm *DelayModeration) IsModerator(user *User) bool {
	if dm == nil || user == nil {
		return false
	}
	dm.mutex.Lock()
	defer dm.mutex.Unlock()
	return len(dm.scopes[user.Id]) > 0
}

// CanModerate reports whether user may review and remove this call. The call
// must be on a delayed talkgroup the user can hear and that their scope covers.
func (dm *DelayModeration) CanModerate(user *User, call *Call) bool {
	if dm == nil || user == nil || call == nil || call.System == nil || call.Talkgroup == nil {
		return false
	}
	if user.IsSuspended() || dm.controller.Delayer.getSystemDelay(call) == 0 {
		return false
	}
	dm.mutex.Lock()
	scope := dm.scopes[user.Id]
	dm.mutex.Unlock()
	if !moderationScopeAllows(scope, call.System.SystemRef, call.Talkgroup.TalkgroupRef) {
		return false
	}
	return dm.controller.userHasAccess(user, call)
}

// IsModerated reports whether any moderator covers this call's talkgroup.
func (dm *DelayModeration) IsModerated(call *Call) bool {
	if dm == nil || call == nil || call.System == nil || call.Talkgroup == nil {
		return false
	}
	if dm.controller.Delayer.getSystemDelay(call) == 0 {
		return false
	}
	dm.mutex.Lock()
	defer dm.mutex.Unlock()
	for _, scope := range dm.scopes {
		if moderationScopeAllows(scope, call.System.SystemRef, call.Talkgroup.TalkgroupRef) {
			return true
		}
	}
	return false
}

// Hold records a delayed send so removing the call can cancel it.
func (dm *DelayModeration) Hold(callId uint64, timer *time.Timer, until time.Time) {
	if dm == nil || timer == nil || callId == 0 {
		return
	}
	dm.mutex.Lock()
	defer dm.mutex.Unlock()
	now := time.Now()
	for id, h := range dm.held {
		if now.After(h.until.Add(time.Minute)) {
			delete(dm.held, id)
		}
	}
	h := dm.held[callId]
	if h == nil {
		h = &heldSends{}
		dm.held[callId] = h
	}
	h.timers = append(h.timers, timer)
	if until.After(h.until) {
		h.until = until
	}
}

func (dm *DelayModeration) IsRemoved(callId uint64) bool {
	if dm == nil || callId == 0 {
		return false
	}
	dm.mutex.Lock()
	defer dm.mutex.Unlock()
	_, ok := dm.removed[callId]
	return ok
}

func (dm *DelayModeration) markRemoved(callId uint64) {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()
	now := time.Now()
	for id, at := range dm.removed {
		if now.Sub(at) > delayModerationRemovedTTL {
			delete(dm.removed, id)
		}
	}
	dm.removed[callId] = now
	if h := dm.held[callId]; h != nil {
		for _, t := range h.timers {
			t.Stop()
		}
		delete(dm.held, callId)
	}
}

// callReleaseFor is when user may first learn of call: hear it, see its
// transcript or be alerted about it.
func (controller *Controller) callReleaseFor(user *User, call *Call) time.Time {
	if user == nil || call == nil || call.System == nil || call.Talkgroup == nil || call.Timestamp.IsZero() {
		return time.Time{}
	}
	delay := controller.userEffectiveDelay(user, call, controller.Delayer.getSystemDelay(call))
	return call.Timestamp.Add(time.Duration(delay) * time.Minute)
}

// callReleasedForClient reports whether client may have call yet by any path:
// playback by id, search, downloads. call must carry its real System and
// Talkgroup, or their delays are missed.
func (controller *Controller) callReleasedForClient(client *Client, call *Call) bool {
	if client == nil || call == nil {
		return false
	}
	if controller.DelayModeration.IsRemoved(call.Id) {
		return false
	}
	if client.BypassPlaybackSearchACL || call.System == nil || call.Talkgroup == nil || call.Timestamp.IsZero() {
		return true
	}
	delay := controller.clientEffectiveDelay(client, call, controller.Delayer.getSystemDelay(call))
	return !time.Now().Before(call.Timestamp.Add(time.Duration(delay) * time.Minute))
}

// clientMayHaveCall applies the same access and delay gates as live playback.
func (api *Api) clientMayHaveCall(client *Client, call *Call) bool {
	controller := api.Controller
	if controller.scopeRestricted(client) {
		if client.User != nil {
			if !controller.userHasAccess(client.User, call) {
				return false
			}
		} else if !controller.anonymousAllowed(client.ListenLink) || !controller.anonymousHasCallAccess(client.ListenLink, call) {
			return false
		}
	}
	return controller.callReleasedForClient(client, call)
}

// heldCallsFilter is a SQL condition (leading " AND ") that drops rows of
// alias whose tsColumn is still inside the talkgroup's delay. For shared
// results that are not computed per listener.
func (controller *Controller) heldCallsFilter(now time.Time, alias, tsColumn string) string {
	byDelay := map[uint][]string{}
	for _, system := range controller.Systems.List {
		if system == nil {
			continue
		}
		for _, talkgroup := range system.Talkgroups.List {
			if talkgroup == nil || talkgroup.Id == 0 {
				continue
			}
			if d := controller.Delayer.getSystemDelay(&Call{System: system, Talkgroup: talkgroup}); d > 0 {
				byDelay[d] = append(byDelay[d], strconv.FormatUint(talkgroup.Id, 10))
			}
		}
	}
	if len(byDelay) == 0 {
		return ""
	}
	parts := make([]string, 0, len(byDelay))
	for d, ids := range byDelay {
		cutoff := now.Add(-time.Duration(d) * time.Minute).UnixMilli()
		parts = append(parts, fmt.Sprintf(`(%s."talkgroupId" IN (%s) AND %s."%s" > %d)`, alias, strings.Join(ids, ","), alias, tsColumn, cutoff))
	}
	return " AND NOT (" + strings.Join(parts, " OR ") + ")"
}

// maxReleaseDelay is the longest delay user has on any talkgroup they can hear.
func (controller *Controller) maxReleaseDelay(user *User) time.Duration {
	var longest uint
	for _, system := range controller.Systems.List {
		if system == nil {
			continue
		}
		for _, talkgroup := range system.Talkgroups.List {
			if talkgroup == nil {
				continue
			}
			probe := &Call{System: system, Talkgroup: talkgroup}
			if !controller.userHasAccess(user, probe) {
				continue
			}
			if d := controller.userEffectiveDelay(user, probe, controller.Delayer.getSystemDelay(probe)); d > longest {
				longest = d
			}
		}
	}
	return time.Duration(longest) * time.Minute
}

// alertRecipientsReleased returns the users who may be alerted about call now.
// The rest are passed to resend once their delay ends; a call a moderator
// removes in the meantime is never alerted.
func (controller *Controller) alertRecipientsReleased(userIds []uint64, call *Call, resend func([]uint64)) []uint64 {
	if call == nil {
		return userIds
	}
	dm := controller.DelayModeration
	if dm.IsRemoved(call.Id) {
		return nil
	}
	now := time.Now()
	ready := make([]uint64, 0, len(userIds))
	held := map[int64][]uint64{}
	for _, id := range userIds {
		release := controller.callReleaseFor(controller.Users.GetUserById(id), call)
		if release.After(now) {
			held[release.Unix()] = append(held[release.Unix()], id)
		} else {
			ready = append(ready, id)
		}
	}
	for at, ids := range held {
		until := time.Unix(at+1, 0)
		ids := ids
		timer := time.AfterFunc(time.Until(until), func() {
			if !dm.IsRemoved(call.Id) {
				resend(ids)
			}
		})
		dm.Hold(call.Id, timer, until)
	}
	return ready
}

func (dm *DelayModeration) releaseAt(call *Call) time.Time {
	return call.Timestamp.Add(time.Duration(dm.controller.Delayer.getSystemDelay(call)) * time.Minute)
}

// Pending lists the calls user can still remove, oldest first.
func (dm *DelayModeration) Pending(user *User) ([]DelayQueueCall, error) {
	if !dm.IsModerator(user) {
		return []DelayQueueCall{}, nil
	}

	var (
		maxDelay uint
		tgIds    []string
	)
	for _, system := range dm.controller.Systems.List {
		if system == nil {
			continue
		}
		for _, talkgroup := range system.Talkgroups.List {
			if talkgroup == nil || talkgroup.Id == 0 {
				continue
			}
			probe := &Call{System: system, Talkgroup: talkgroup}
			if !dm.CanModerate(user, probe) {
				continue
			}
			if d := dm.controller.Delayer.getSystemDelay(probe); d > maxDelay {
				maxDelay = d
			}
			tgIds = append(tgIds, strconv.FormatUint(talkgroup.Id, 10))
		}
	}
	if len(tgIds) == 0 {
		return []DelayQueueCall{}, nil
	}

	since := time.Now().Add(-time.Duration(maxDelay) * time.Minute).UnixMilli()
	query := fmt.Sprintf(`SELECT "callId", "systemId", "talkgroupId", "timestamp", "transcript" FROM "calls" WHERE "timestamp" >= $1 AND "talkgroupId" IN (%s) ORDER BY "timestamp" ASC LIMIT 500`, strings.Join(tgIds, ","))
	rows, err := dm.controller.Database.Sql.Query(query, since)
	if err != nil {
		return nil, fmt.Errorf("delaymoderation.pending: %w", err)
	}
	defer rows.Close()

	now := time.Now()
	out := []DelayQueueCall{}
	for rows.Next() {
		var (
			callId, systemId, talkgroupId uint64
			timestamp                     int64
			transcript                    string
		)
		if err := rows.Scan(&callId, &systemId, &talkgroupId, &timestamp, &transcript); err != nil {
			return nil, fmt.Errorf("delaymoderation.pending: %w", err)
		}
		call, ok := dm.callFromRow(callId, systemId, talkgroupId, timestamp)
		if !ok || dm.IsRemoved(callId) {
			continue
		}
		release := dm.releaseAt(call)
		if !now.Before(release) {
			continue
		}
		out = append(out, DelayQueueCall{
			Id:             callId,
			SystemId:       call.System.SystemRef,
			SystemLabel:    call.System.Label,
			TalkgroupId:    call.Talkgroup.TalkgroupRef,
			TalkgroupLabel: call.Talkgroup.Label,
			TalkgroupName:  call.Talkgroup.Name,
			Timestamp:      timestamp,
			ReleaseAt:      release.UnixMilli(),
			Transcript:     strings.TrimSpace(transcript),
		})
	}
	return out, rows.Err()
}

func (dm *DelayModeration) callFromRow(callId, systemId, talkgroupId uint64, timestamp int64) (*Call, bool) {
	system, ok := dm.controller.Systems.GetSystemById(systemId)
	if !ok {
		return nil, false
	}
	talkgroup, ok := system.Talkgroups.GetTalkgroupById(talkgroupId)
	if !ok {
		return nil, false
	}
	return &Call{Id: callId, System: system, Talkgroup: talkgroup, Timestamp: time.UnixMilli(timestamp)}, true
}

// pendingCallFor loads a call that user may still remove.
func (dm *DelayModeration) pendingCallFor(user *User, callId uint64) (*Call, bool) {
	call := dm.controller.callRefById(callId)
	if call == nil || dm.IsRemoved(callId) || !dm.CanModerate(user, call) {
		return nil, false
	}
	if !time.Now().Before(dm.releaseAt(call)) {
		return nil, false
	}
	return call, true
}

// Remove deletes pending calls user may moderate and cancels every held send.
// Calls already released, or outside the user's scope, are left alone.
func (dm *DelayModeration) Remove(user *User, callIds []uint64) ([]uint64, error) {
	removed := []uint64{}
	labels := []string{}
	for _, id := range callIds {
		call, ok := dm.pendingCallFor(user, id)
		if !ok {
			continue
		}
		dm.markRemoved(id)
		removed = append(removed, id)
		labels = append(labels, fmt.Sprintf("%d (%s / %s)", id, call.System.Label, call.Talkgroup.Label))
	}
	if len(removed) == 0 {
		return removed, nil
	}
	if err := dm.controller.Calls.DeleteByIDs(dm.controller.Database, removed); err != nil {
		return removed, err
	}
	dm.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("delay moderation: %s removed %d call(s) before release: %s", user.Email, len(removed), strings.Join(labels, ", ")))
	return removed, nil
}

// DelayQueueHandler serves GET /api/delay-queue and POST /api/delay-queue/remove.
func (api *Api) DelayQueueHandler(w http.ResponseWriter, r *http.Request) {
	dm := api.Controller.DelayModeration
	user := api.userFromRequestPin(r)
	if user == nil || user.IsSuspended() {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !dm.IsModerator(user) {
		api.exitWithError(w, http.StatusForbidden, "delay moderation is not enabled for this account")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")

	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/delay-queue"):
		calls, err := dm.Pending(user)
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, "failed to load the delay queue")
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"calls":      calls,
			"serverTime": time.Now().UnixMilli(),
		})

	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/delay-queue/remove"):
		var request struct {
			Ids []uint64 `json:"ids"`
			All bool     `json:"all"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&request); err != nil {
			api.exitWithError(w, http.StatusBadRequest, "invalid request")
			return
		}
		ids := request.Ids
		if request.All {
			calls, err := dm.Pending(user)
			if err != nil {
				api.exitWithError(w, http.StatusInternalServerError, "failed to load the delay queue")
				return
			}
			ids = ids[:0]
			for _, c := range calls {
				ids = append(ids, c.Id)
			}
		}
		removed, err := dm.Remove(user, ids)
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, "failed to remove calls")
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"removed": removed})

	default:
		api.exitWithError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// DelayQueueAudioHandler serves GET /api/delay-queue/audio?id= so a moderator
// can preview a call that is still held.
func (api *Api) DelayQueueAudioHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	dm := api.Controller.DelayModeration
	user := api.userFromRequestPin(r)
	if user == nil || !dm.IsModerator(user) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if _, ok := dm.pendingCallFor(user, id); !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var (
		audio          []byte
		mime, filename string
	)
	if err := api.Controller.Database.Sql.QueryRow(`SELECT "audio", "audioMime", "audioFilename" FROM "calls" WHERE "callId" = $1`, id).Scan(&audio, &mime, &filename); err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if mime == "" {
		mime = mimepkg.TypeByExtension(filepath.Ext(filename))
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(audio)))
	w.Write(audio)
}

// UserDelayModerationHandler serves GET/PUT /api/admin/users/{id}/delay-moderation.
func (admin *Admin) UserDelayModerationHandler(w http.ResponseWriter, r *http.Request) {
	if !admin.ValidateToken(admin.GetAuthorization(r)) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 5 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid user ID"})
		return
	}
	userID, err := strconv.ParseUint(pathParts[3], 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid user ID format"})
		return
	}
	if admin.Controller.Users.GetUserById(userID) == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "User not found"})
		return
	}

	dm := admin.Controller.DelayModeration
	switch r.Method {
	case http.MethodGet:
		json.NewEncoder(w).Encode(map[string]any{"scope": dm.Scope(userID)})
	case http.MethodPut:
		var request struct {
			Scope []string `json:"scope"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request"})
			return
		}
		scope, err := dm.SetScope(admin.Controller.Database, userID, request.Scope)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save"})
			return
		}
		admin.Controller.EmitConfig()
		json.NewEncoder(w).Encode(map[string]any{"scope": scope})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (controller *Controller) sendDownstreamAfterModeration(call *Call) {
	dm := controller.DelayModeration
	until := dm.releaseAt(call)
	timer := time.AfterFunc(time.Until(until), func() {
		if !dm.IsRemoved(call.Id) {
			controller.Downstreams.Send(controller, call)
		}
	})
	dm.Hold(call.Id, timer, until)
}

// callRefById loads the system, talkgroup and time of a stored call.
func (controller *Controller) callRefById(callId uint64) *Call {
	if callId == 0 || controller.Database == nil || controller.Database.Sql == nil {
		return nil
	}
	var (
		systemId, talkgroupId uint64
		timestamp             int64
	)
	if err := controller.Database.Sql.QueryRow(`SELECT "systemId", "talkgroupId", "timestamp" FROM "calls" WHERE "callId" = $1`, callId).Scan(&systemId, &talkgroupId, &timestamp); err != nil {
		return nil
	}
	call, _ := controller.DelayModeration.callFromRow(callId, systemId, talkgroupId, timestamp)
	return call
}
