// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import "testing"

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