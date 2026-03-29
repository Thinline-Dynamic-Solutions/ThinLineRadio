package main

import (
	"testing"
)

func TestTalkgroupMatchesFilter(t *testing.T) {
	admin := &Admin{}

	// Test talkgroup
	tg := RadioReferenceTalkgroup{
		Group: "Police",
		Tag:   "Emergency",
		Enc:   0,
	}

	// Test with no filters
	if !admin.talkgroupMatchesFilter(tg, "", "", nil) {
		t.Error("Talkgroup should match when no filters are applied")
	}

	// Test with group filter
	if !admin.talkgroupMatchesFilter(tg, "Police", "", nil) {
		t.Error("Talkgroup should match group filter 'Police'")
	}

	if admin.talkgroupMatchesFilter(tg, "Fire", "", nil) {
		t.Error("Talkgroup should not match group filter 'Fire'")
	}

	// Test with tag filter
	if !admin.talkgroupMatchesFilter(tg, "", "Emergency", nil) {
		t.Error("Talkgroup should match tag filter 'Emergency'")
	}

	if admin.talkgroupMatchesFilter(tg, "", "Traffic", nil) {
		t.Error("Talkgroup should not match tag filter 'Traffic'")
	}

	// Test with encrypted filter
	encrypted := true
	if admin.talkgroupMatchesFilter(tg, "", "", &encrypted) {
		t.Error("Non-encrypted talkgroup should not match encrypted filter true")
	}

	notEncrypted := false
	if !admin.talkgroupMatchesFilter(tg, "", "", &notEncrypted) {
		t.Error("Non-encrypted talkgroup should match encrypted filter false")
	}
}

func TestPaginationCalculation(t *testing.T) {
	// Test pagination math
	page := 1
	pageSize := 100
	startIndex := (page - 1) * pageSize
	endIndex := startIndex + pageSize

	if startIndex != 0 {
		t.Errorf("Expected startIndex 0, got %d", startIndex)
	}

	if endIndex != 100 {
		t.Errorf("Expected endIndex 100, got %d", endIndex)
	}

	// Test second page
	page = 2
	startIndex = (page - 1) * pageSize
	endIndex = startIndex + pageSize

	if startIndex != 100 {
		t.Errorf("Expected startIndex 100, got %d", startIndex)
	}

	if endIndex != 200 {
		t.Errorf("Expected endIndex 200, got %d", endIndex)
	}
}
