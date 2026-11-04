package main

import "testing"

func TestSiteClockOffsetIgnoresLateReports(t *testing.T) {
	clock := &coreSiteClock{}
	for i := 0; i < 60; i++ {
		delay := int64(15 + (i*7)%50)
		if i%6 == 0 {
			delay = 4000 // re-sent on a later pass, or held through a reconnect
		}
		clock.add(-1200 - delay)
	}
	if !clock.ready() {
		t.Fatal("clock should be measured after enough reports")
	}
	if clock.offsetMs > -1200 || clock.offsetMs < -1270 {
		t.Fatalf("offset %dms, want about -1215ms", clock.offsetMs)
	}
}

func TestSiteClockWaitsForEnoughReports(t *testing.T) {
	core := &coreCoordinator{}
	if got := core.serverStartLocked(1, 50_000, 51_000, true); got != 50_000 {
		t.Fatalf("one report must not move start times, got %d", got)
	}
}

func TestSkewedSitesMatchAfterClockCorrection(t *testing.T) {
	// Paintsville-style site 2.4 s behind another site on the same call.
	core := &coreCoordinator{}
	base := int64(1_800_000_000_000)
	for i := int64(0); i < 20; i++ {
		at := base + i*5000
		core.serverStartLocked(1, at, at+30, true)
		core.serverStartLocked(2, at-2400, at+45, true)
	}

	start := base + 200_000
	first := siteMember(1, 7891310, start, 900)
	second := siteMember(2, 7891310, start-2400, 900)
	if transmissionMatch(first, second.system, second.talkgroup, second.radioId, second.patches, second.startMs, second.durationMs) {
		t.Fatal("test setup: uncorrected copies should look like separate calls")
	}

	first.startMs = core.serverStartLocked(1, start, start+30, true)
	second.startMs = core.serverStartLocked(2, start-2400, start+45, true)
	if !transmissionMatch(first, second.system, second.talkgroup, second.radioId, second.patches, second.startMs, second.durationMs) {
		t.Fatalf("corrected copies should be one call (starts %d and %d)", first.startMs, second.startMs)
	}
	if d := abs64(first.startMs - second.startMs); d > 100 {
		t.Fatalf("corrected starts should be within network delay, %dms apart", d)
	}
}
