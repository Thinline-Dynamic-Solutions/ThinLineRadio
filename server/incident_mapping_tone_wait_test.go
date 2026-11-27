package main

import "testing"

func TestUnmatchedDetectedTonesDoNotDeferMapping(t *testing.T) {
	talkgroup := &Talkgroup{ToneDetectionEnabled: true, ToneSets: []ToneSet{{Label: "Station 1"}}}
	call := &Call{Talkgroup: talkgroup, HasTones: true, ToneSequence: &ToneSequence{}}

	if incidentMappingAwaitingPendingTones(nil, call) {
		t.Fatal("a call whose detected tones matched no set was held back from mapping")
	}
}

func TestToneDeferralNeedsToneDetection(t *testing.T) {
	call := &Call{Talkgroup: &Talkgroup{}, HasTones: true}

	if incidentMappingAwaitingPendingTones(nil, call) {
		t.Fatal("a talkgroup without tone detection deferred mapping")
	}
}
