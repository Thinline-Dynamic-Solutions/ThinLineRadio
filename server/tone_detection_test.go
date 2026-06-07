// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"strings"
	"testing"
)

func TestOrphanLoadCallIdPrefersPending(t *testing.T) {
	pending := &PendingToneSequence{CallId: 39395685}
	callId := uint64(0)

	loadCallId := pending.CallId
	if loadCallId == 0 {
		loadCallId = callId
	}
	if loadCallId != 39395685 {
		t.Fatalf("expected pending call id 39395685, got %d", loadCallId)
	}
}

func TestOrphanLoadCallIdFallsBackToArgument(t *testing.T) {
	pending := &PendingToneSequence{CallId: 0}
	callId := uint64(39395718)

	loadCallId := pending.CallId
	if loadCallId == 0 {
		loadCallId = callId
	}
	if loadCallId != 39395718 {
		t.Fatalf("expected fallback call id 39395718, got %d", loadCallId)
	}
}

func TestVoiceForToneAlertsShortDispatch(t *testing.T) {
	c := &Controller{}
	dispatch := "STATION 21RBD, STATION TRANSFER, STATION 21-0-3-2-6."

	if len(strings.Fields(strings.TrimSpace(dispatch))) >= 8 {
		t.Fatal("test transcript should be fewer than 8 words (keyword threshold)")
	}
	if !c.isVoiceForToneAlerts(dispatch) {
		t.Fatal("isVoiceForToneAlerts should accept short dispatch for tone attach")
	}
	if c.transcriptLooksLikeTonesOnly(dispatch) {
		t.Fatal("short dispatch should not be classified as tone-only")
	}
}

func TestVoiceForToneAlertsRejectsToneLike(t *testing.T) {
	c := &Controller{}

	if !c.transcriptLooksLikeTonesOnly("BEEP.") {
		t.Fatal("BEEP should be tone-like")
	}
	if c.isVoiceForToneAlerts("BEEP.") {
		t.Fatal("expected tone-like transcript to be rejected for tone alerts")
	}
}

func TestMatchToneSetsPrefersYoungstownOverMcDonaldOnStacked566(t *testing.T) {
	detector := NewToneDetector()
	ys := ToneSet{
		Label:     "Youngstown Air Base",
		Tolerance: 0.03,
		ATone:     &ToneSpec{Frequency: 566, MinDuration: 0.6},
		BTone:     &ToneSpec{Frequency: 1155.75, MinDuration: 0.6},
	}
	mcd := ToneSet{
		Label:     "McDonald",
		Tolerance: 0.04,
		ATone:     &ToneSpec{Frequency: 566, MinDuration: 0.6},
		BTone:     &ToneSpec{Frequency: 598, MinDuration: 0.6},
	}
	seq := &ToneSequence{
		HasTones: true,
		Tones: []Tone{
			{Frequency: 566.35, StartTime: 1.536, EndTime: 2.656, Duration: 1.12, Magnitude: 0.02},
			{Frequency: 598.37, StartTime: 2.528, EndTime: 5.664, Duration: 3.14, Magnitude: 0.02},
			{Frequency: 566.35, StartTime: 12.224, EndTime: 13.344, Duration: 1.12, Magnitude: 0.02},
			{Frequency: 1156.53, StartTime: 13.248, EndTime: 16.032, Duration: 2.78, Magnitude: 0.02},
		},
	}

	matched := detector.MatchToneSets(seq, []ToneSet{ys, mcd})
	if len(matched) < 2 {
		t.Fatalf("expected both tone sets to match, got %d", len(matched))
	}
	if matched[0].Label != "Youngstown Air Base" {
		t.Fatalf("expected first configured match Youngstown Air Base, got %s", matched[0].Label)
	}
}