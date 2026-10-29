package main

import (
	"testing"
	"time"
)

func TestCoreRadiosSplit(t *testing.T) {
	if coreRadiosSplit(0, 5) || coreRadiosSplit(5, 0) || coreRadiosSplit(5, 5) {
		t.Fatal("missing or equal radio ids stay in one call")
	}
	if !coreRadiosSplit(5, 6) {
		t.Fatal("different radio ids are separate calls")
	}
}

func TestCoreLonger(t *testing.T) {
	if coreLonger(7000, 12000) {
		t.Fatal("shorter recording must not win")
	}
	if coreLonger(12300, 12000) {
		t.Fatal("a few hundred milliseconds is the same length")
	}
	if !coreLonger(12000, 7000) {
		t.Fatal("the 12 second recording wins")
	}
}

func TestCoreIntervalsOverlap(t *testing.T) {
	if !coreIntervalsOverlap(0, 0, 5, 5) {
		t.Fatal("unknown start still matches an open call")
	}
	if !coreIntervalsOverlap(1_000, 7_000, 1_200, 12_000) {
		t.Fatal("overlapping spans match")
	}
	if coreIntervalsOverlap(1_000, 2_000, 20_000, 2_000) {
		t.Fatal("a later key-up is a new call")
	}
}

func TestCoreTalkgroupsOverlap(t *testing.T) {
	left := patchSet(100, []int{200})
	right := patchSet(200, nil)
	if !coreTalkgroupsOverlap(left, right) {
		t.Fatal("a patched talkgroup matches the patch")
	}
	if coreTalkgroupsOverlap(patchSet(1, nil), patchSet(2, nil)) {
		t.Fatal("different talkgroups do not match")
	}
}

func member(radio int, start int64, duration int64) *coreMember {
	return &coreMember{
		system:     78,
		talkgroup:  46024,
		radioId:    radio,
		patches:    patchSet(46024, nil),
		startMs:    start,
		durationMs: duration,
		ready:      duration > 0,
	}
}

func TestOverlappingRadiosAreOneTransmission(t *testing.T) {
	// 78 FD SE: two sites, 158ms apart, different radio ids, same length.
	first := member(7890442, 36_428, 720)
	if !transmissionMatch(first, 78, 46024, 7891310, patchSet(46024, nil), 36_586, 720) {
		t.Fatal("overlapping audio is one transmission even when the radio ids differ")
	}
}

func TestBackToBackRadiosStaySeparate(t *testing.T) {
	// Earlier 2.3s call ended about 0.8s before the next unit keyed up.
	first := member(7891310, 29_060, 2340)
	if transmissionMatch(first, 78, 46024, 7890442, patchSet(46024, nil), 32_211, 720) {
		t.Fatal("a later unit is a new call")
	}
}

func TestOpenCallDoesNotSwallowTheNextKeyup(t *testing.T) {
	opening := member(7891310, 29_060, 0)
	opening.ready = false
	if transmissionMatch(opening, 78, 46024, 7890442, patchSet(46024, nil), 32_211, 0) {
		t.Fatal("a key-up a few seconds later must not join a call that is still opening")
	}
	if !transmissionMatch(opening, 78, 46024, 7890442, patchSet(46024, nil), 29_218, 0) {
		t.Fatal("the other site's copy of the same key-up still joins")
	}
}

func TestAbsorbKeepsOneGroupForOverlappingSites(t *testing.T) {
	core := &coreCoordinator{}
	first := &coreGroup{updated: time.Now(), members: []*coreMember{member(7891310, 34_478, 1260)}}
	second := &coreGroup{updated: time.Now(), members: []*coreMember{member(7891310, 34_586, 1260)}}
	core.groups = []*coreGroup{first, second}
	kept := core.absorbLocked(second)
	if len(core.groups) != 1 {
		t.Fatalf("sites of one transmission should be one group, got %d", len(core.groups))
	}
	if len(kept.members) != 2 {
		t.Fatalf("both recordings should remain candidates, got %d", len(kept.members))
	}
}

func TestSplitBackToBackUnits(t *testing.T) {
	core := &coreCoordinator{}
	group := &coreGroup{updated: time.Now(), members: []*coreMember{
		member(7891310, 29_060, 2340),
		member(7890442, 32_211, 720),
	}}
	core.groups = []*coreGroup{group}
	next := core.splitBackToBackLocked(group)
	if next == nil || len(group.members) != 1 || len(next.members) != 1 {
		t.Fatal("back-to-back units should be split before a winner is chosen")
	}
}

func TestClosedCallDiscardsALateCopy(t *testing.T) {
	core := &coreCoordinator{}
	group := &coreGroup{members: []*coreMember{member(7891310, 34_478, 1260)}}
	core.rememberLocked(group)
	if !core.suppressedLocked(0, 78, 46024, 7891310, patchSet(46024, nil), 34_586, 1260) {
		t.Fatal("a late copy of a call that already uploaded should be discarded")
	}
	if core.suppressedLocked(0, 78, 46024, 7890442, patchSet(46024, nil), 38_000, 720) {
		t.Fatal("the next key-up is not a duplicate of the closed call")
	}
}

func TestSameSiteTailAfterStoredCopyPlays(t *testing.T) {
	// Call 77191137: Portage alone, 1.8s stored, the rest started 250ms after it ended.
	core := &coreCoordinator{}
	core.rememberLocked(&coreGroup{members: []*coreMember{siteMember(2, 7891192, 52_471, 1800)}})
	if core.suppressedLocked(2, 78, 46024, 0, patchSet(46024, nil), 54_521, 0) {
		t.Fatal("the same site's piece after its stored copy is the rest of the call")
	}
	if core.suppressedLocked(1, 78, 46024, 0, patchSet(46024, nil), 54_521, 0) {
		t.Fatal("another site's start after the stored copy ended is not a copy of it")
	}
	if !core.suppressedLocked(1, 78, 46024, 0, patchSet(46024, nil), 54_200, 0) {
		t.Fatal("another site's copy that starts before the end is still a duplicate")
	}
	if !core.suppressedLocked(2, 78, 46024, 7891192, patchSet(46024, nil), 53_000, 0) {
		t.Fatal("the same site's overlapping copy is a duplicate")
	}
}

func TestStoredCopyKeepsItsSitesLaterPiece(t *testing.T) {
	core := &coreCoordinator{}
	stored := siteMember(2, 7891192, 52_471, 1800)
	stored.recordingId = "stored"
	tail := siteMember(2, 0, 54_521, 0)
	tail.ready = false
	other := siteMember(1, 7891192, 52_500, 1700)
	group := &coreGroup{deciding: true, members: []*coreMember{stored, tail, other}}
	core.groups = []*coreGroup{group}
	next := core.splitContinuationLocked(group, "stored")
	if next == nil || len(next.members) != 1 || next.members[0] != tail {
		t.Fatal("the site's later piece becomes its own call")
	}
	if len(group.members) != 2 {
		t.Fatal("other copies of the stored call stay to be discarded")
	}
}

func siteMember(key uint64, radio int, start int64, duration int64) *coreMember {
	m := member(radio, start, duration)
	m.keyId = key
	return m
}

func TestLateSiteJoinsWhileAnotherIsReceiving(t *testing.T) {
	core := &coreCoordinator{}
	open := siteMember(1, 7891310, 10_000, 0)
	group := &coreGroup{members: []*coreMember{open}}
	core.groups = []*coreGroup{group}
	// The second site's repeater came up 2.2 seconds later.
	if got := core.groupForLocked("site2", 78, 46024, 7890442, nil, 12_200, 0); got != group {
		t.Fatal("a site that starts while another is still receiving joins that call")
	}
}

func TestQuickRekeyIsItsOwnCall(t *testing.T) {
	// 78 FD DISP 14:49: every site finished by 25.770, the dispatcher keyed
	// again at 26.285 and Trumbull held that one 1.5 seconds longer.
	core := &coreCoordinator{}
	finished := &coreGroup{deciding: true, members: []*coreMember{
		siteMember(1, 7890442, 16_196, 9360),
		siteMember(2, 7890442, 16_241, 6480),
		siteMember(2, 7890442, 22_890, 2880),
	}}
	core.groups = []*coreGroup{finished}
	if got := core.groupForLocked("trumbull-next", 78, 46024, 7890442, nil, 26_285, 0); got == finished {
		t.Fatal("a re-key after every site finished is a new call")
	}
	if got := core.groupForLocked("portage-tail", 78, 46024, 7890442, nil, 25_400, 0); got != finished {
		t.Fatal("a copy that starts inside the finished call still joins it")
	}
}

func TestRejectsEverySite(t *testing.T) {
	for _, reason := range []string{"no matching system/talkgroup", "hidden channel", "blacklisted (patched talkgroup)", "duplicate (radio timestamp)"} {
		if !coreRejectsEverySite(reason) {
			t.Fatalf("%q would reject every site's copy", reason)
		}
	}
	if coreRejectsEverySite("not stored") {
		t.Fatal("a failed write is retried with another site")
	}
}

func TestLoneSitePiecesBothPlay(t *testing.T) {
	core := &coreCoordinator{}
	first := siteMember(1, 7891310, 20_000, 1500)
	second := siteMember(1, 7891310, 21_600, 1400)
	group := &coreGroup{members: []*coreMember{first, second}}
	core.groups = []*coreGroup{group}
	next := core.splitPiecesLocked(group)
	if next == nil || len(group.members) != 1 || len(next.members) != 1 {
		t.Fatal("each piece of a dropped call with no other copy uploads")
	}
	if core.mergeCandidateLocked(group) != nil {
		t.Fatal("the pieces must not merge back together")
	}
}

func TestOtherSiteCoversTheDrop(t *testing.T) {
	core := &coreCoordinator{}
	group := &coreGroup{members: []*coreMember{
		siteMember(2, 7891310, 20_000, 3000),
		siteMember(1, 7891310, 20_050, 1500),
		siteMember(1, 7891310, 21_700, 1300),
	}}
	core.groups = []*coreGroup{group}
	if core.splitPiecesLocked(group) != nil {
		t.Fatal("the unbroken copy covers both pieces, so the pieces are discarded")
	}
	if winner := core.winnerLocked(group); winner == nil || winner.keyId != 2 {
		t.Fatal("the unbroken copy wins")
	}
}

func TestStaleOpenRecordingIsReleased(t *testing.T) {
	core := &coreCoordinator{}
	stuck := siteMember(1, 7891310, 10_000, 0)
	stuck.ready = false
	stuck.opened = time.Now().Add(-3 * time.Minute)
	fresh := siteMember(2, 7891310, 10_100, 0)
	fresh.ready = false
	fresh.opened = time.Now()
	group := &coreGroup{members: []*coreMember{stuck, fresh}}
	core.groups = []*coreGroup{group}
	pending := core.expireOpenLocked(time.Now())
	if len(pending) != 1 || !stuck.cancelled || fresh.cancelled {
		t.Fatal("only a recording open past the limit is released")
	}
}

func TestNextTransmissionAfterStoredCopyPlays(t *testing.T) {
	// 78 FD CENT 17:31: Portage's stored span ended at 03.829 and Trumbull's
	// next transmission started at 04.040.
	core := &coreCoordinator{}
	stored := *siteMember(2, 7891145, 3_109, 720)
	stored.talkgroup, stored.patches = 46028, patchSet(46028, nil)
	core.recent = []coreSpan{{at: time.Now(), members: []coreMember{stored}}}
	if core.suppressedLocked(1, 78, 46028, 7890442, patchSet(46028, nil), 4_040, 0) {
		t.Fatal("a start after the stored copy ended is a new call")
	}
	if core.suppressedLocked(1, 78, 46028, 7890442, patchSet(46028, nil), 4_040, 4680) {
		t.Fatal("a ready after the stored copy ended is a new call")
	}
	if !core.suppressedLocked(1, 78, 46028, 7891145, patchSet(46028, nil), 3_200, 0) {
		t.Fatal("another site's copy inside the stored call is still discarded")
	}
}

// 78 FD DISP 19:08: a 3.6s call with no radio id on every copy. Trumbull's
// late receiver ended at 52.416 and dispatch keyed up again at 52.405.
func dispatchHandoffGroup() *coreGroup {
	portage := siteMember(2, 0, 48_740, 3600)
	first := siteMember(1, 0, 48_654, 3600)
	late := siteMember(1, 0, 50_616, 1800)
	return &coreGroup{members: []*coreMember{portage, first, late}}
}

func TestNextKeyupAfterFinishedCallIsNewCall(t *testing.T) {
	core := &coreCoordinator{}
	earlier := dispatchHandoffGroup()
	core.groups = []*coreGroup{earlier}
	if got := core.groupForLocked("dispatch", 78, 46024, 7890442, nil, 52_405, 0); got == earlier {
		t.Fatal("a key-up as the finished call ends is a new call")
	}
	if got := core.groupForLocked("tail", 78, 46024, 0, nil, 51_000, 0); got != earlier {
		t.Fatal("a copy that starts inside the finished call still joins it")
	}
}

func TestFinishedCallDoesNotAbsorbNextKeyup(t *testing.T) {
	core := &coreCoordinator{}
	earlier := dispatchHandoffGroup()
	next := &coreGroup{members: []*coreMember{siteMember(1, 7890442, 52_405, 0)}}
	next.members[0].ready = false
	core.groups = []*coreGroup{earlier, next}
	if got := core.absorbLocked(next); got != next || len(earlier.members) != 3 {
		t.Fatal("the next key-up must not merge into the finished call")
	}
}

func TestNextKeyupSplitsOffBeforeTheEarlierCallDecides(t *testing.T) {
	core := &coreCoordinator{}
	group := dispatchHandoffGroup()
	open := siteMember(1, 7890442, 52_405, 0)
	open.ready = false
	group.members = append(group.members, open)
	core.groups = []*coreGroup{group}
	next := core.splitLaterKeyupLocked(group)
	if next == nil || len(next.members) != 1 || next.members[0] != open {
		t.Fatal("the next key-up becomes its own call")
	}
	if !core.readyLocked(group) {
		t.Fatal("the earlier call can decide right away")
	}
	stillRecording := siteMember(2, 0, 48_740, 0)
	stillRecording.ready = false
	group.members = append(group.members, stillRecording, open)
	if core.splitLaterKeyupLocked(group) != nil {
		t.Fatal("wait while a copy of the earlier call is still recording")
	}
}

func TestNextUnitStartingEarlyPlays(t *testing.T) {
	// 78 FD SE 18:25: 7891344 stored 08.526 +540ms, dispatch's 2.52s key-up
	// started on two receivers at 09.026, 40ms before that call ended.
	core := &coreCoordinator{}
	core.rememberLocked(&coreGroup{members: []*coreMember{siteMember(1, 7891344, 8_526, 540)}})
	if core.suppressedLocked(1, 78, 46024, 7890442, patchSet(46024, nil), 9_026, 2520) {
		t.Fatal("a longer recording from the next unit is a new call")
	}
	if !core.suppressedLocked(1, 78, 46024, 7891344, patchSet(46024, nil), 8_600, 540) {
		t.Fatal("the same unit's copy is still discarded")
	}
	if !core.suppressedLocked(1, 78, 46024, 7890442, patchSet(46024, nil), 8_600, 500) {
		t.Fatal("a copy that ends with the stored call is still discarded")
	}
}

func TestTimestampSpansOverlap(t *testing.T) {
	// 78 FD CENT 17:28: a 0.18s blip, then a 2.88s call 0.7s later.
	if timestampSpansOverlap(32_545, 180, 33_244, 2880) {
		t.Fatal("the next transmission is not a copy of the blip")
	}
	if !timestampSpansOverlap(33_244, 2880, 33_344, 2880) {
		t.Fatal("two copies of one transmission overlap")
	}
}

func TestCancelWithoutTalkgroupReleasesTheCall(t *testing.T) {
	// 78 FD SW 16:19: Portage's second receiver cancelled with only the recording id.
	core := &coreCoordinator{}
	done := siteMember(2, 7891498, 31_565, 2340)
	done.recordingId = "portage-855"
	extra := siteMember(2, 7891498, 31_609, 0)
	extra.recordingId = "portage-853"
	group := &coreGroup{members: []*coreMember{done, extra}}
	core.groups = []*coreGroup{group}
	if got, _ := core.cancelLocked(1, "portage-853"); got != nil || extra.cancelled {
		t.Fatal("another site cannot cancel this recording")
	}
	if got, _ := core.cancelLocked(2, "portage-853"); got != group || !extra.cancelled {
		t.Fatal("the cancel finds its call by recording id")
	}
	if !core.readyLocked(group) {
		t.Fatal("with the extra copy cancelled the call is ready to decide")
	}
}

func TestLaterCallWaitsBrieflyForEarlierUpload(t *testing.T) {
	core := &coreCoordinator{}
	earlier := &coreGroup{deciding: true, members: []*coreMember{member(7891310, 29_060, 2340)}}
	later := &coreGroup{members: []*coreMember{member(7890442, 32_211, 720)}}
	other := &coreGroup{members: []*coreMember{{system: 78, talkgroup: 46036, patches: patchSet(46036, nil), startMs: 33_000, durationMs: 900, ready: true}}}
	core.groups = []*coreGroup{earlier, later, other}
	if !core.earlierUploadPendingLocked(later) {
		t.Fatal("a reply waits for the earlier call on its talkgroup")
	}
	if core.earlierUploadPendingLocked(other) {
		t.Fatal("another talkgroup does not wait")
	}
}

func TestStoredFragmentDoesNotDiscardTheRealCall(t *testing.T) {
	// 77 tg 62958 22:34:19: Wadsworth's 180ms radio-0 fragment was stored first,
	// then Summit's 3.6s copy starting 1ms later was discarded as a duplicate.
	core := &coreCoordinator{}
	core.rememberLocked(&coreGroup{members: []*coreMember{siteMember(16, 0, 19_539, 180)}})
	if core.suppressedLocked(17, 78, 46024, 7760024, patchSet(46024, nil), 19_540, 0) {
		t.Fatal("a start after a stored fragment may be the real call")
	}
	if core.suppressedLocked(17, 78, 46024, 7760024, patchSet(46024, nil), 19_540, 3600) {
		t.Fatal("a much longer copy than the stored fragment plays")
	}
	if !core.suppressedLocked(17, 78, 46024, 0, patchSet(46024, nil), 19_545, 200) {
		t.Fatal("another copy of the same fragment is still a duplicate")
	}
}

func TestCleanerCopyWinsWhenEquallyLong(t *testing.T) {
	// 77 tg 62962 21:28:27: both sites held 2.88 seconds, Summit decoded 99%
	// and Wadsworth 94%, and Wadsworth was kept because it reported first.
	core := &coreCoordinator{}
	wadsworth := siteMember(16, 7728335, 27_310, 2880)
	wadsworth.quality, wadsworth.frames = 94, 144
	summit := siteMember(17, 7728335, 27_300, 2880)
	summit.quality, summit.frames = 99, 144
	group := &coreGroup{members: []*coreMember{wadsworth, summit}}
	if winner := core.winnerLocked(group); winner != summit {
		t.Fatal("the cleaner copy wins when both are the same length")
	}

	longer := siteMember(16, 7728335, 27_310, 3600)
	longer.quality, longer.frames = 80, 180
	group = &coreGroup{members: []*coreMember{summit, longer}}
	if winner := core.winnerLocked(group); winner != longer {
		t.Fatal("a clearly longer copy still wins over a cleaner shorter one")
	}

	unscored := siteMember(16, 7728335, 27_310, 2880)
	unscored.quality, unscored.frames = -1, 0
	group = &coreGroup{members: []*coreMember{unscored, summit}}
	if winner := core.winnerLocked(group); winner != unscored {
		t.Fatal("without a score on both copies the first copy is kept")
	}
}
