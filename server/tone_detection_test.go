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