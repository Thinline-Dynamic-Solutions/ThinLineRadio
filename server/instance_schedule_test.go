package main

import (
	"testing"
	"time"
)

func TestNormalizePruneTime(t *testing.T) {
	cases := map[string]struct {
		out string
		ok  bool
	}{
		"":       {"", true},
		"3:05":   {"03:05", true},
		"03:30":  {"03:30", true},
		" 23:59": {"23:59", true},
		"24:00":  {"", false},
		"12:60":  {"", false},
		"noon":   {"", false},
		"3":      {"", false},
		"3:05pm": {"", false},
	}
	for in, want := range cases {
		out, ok := normalizePruneTime(in)
		if out != want.out || ok != want.ok {
			t.Errorf("normalizePruneTime(%q) = %q,%v want %q,%v", in, out, ok, want.out, want.ok)
		}
	}
}

func TestValidTimeZone(t *testing.T) {
	for _, ok := range []string{"", "America/Chicago", "UTC", "Europe/London"} {
		if !validTimeZone(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"Mars/Base", "Local", "CST6CDT5", "chicago"} {
		if validTimeZone(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestOptionsFromMapTimeSettings(t *testing.T) {
	o := &Options{}
	o.FromMap(map[string]any{"timeZone": "America/Chicago", "pruneTime": "4:15"})
	if o.TimeZone != "America/Chicago" || o.PruneTime != "04:15" {
		t.Fatalf("got %q %q", o.TimeZone, o.PruneTime)
	}
	o.FromMap(map[string]any{"timeZone": "Nope/Nope", "pruneTime": "99:99"})
	if o.TimeZone != "" || o.PruneTime != "" {
		t.Fatalf("invalid values must be cleared, got %q %q", o.TimeZone, o.PruneTime)
	}
}

func TestPruneSlotUsesInstanceZone(t *testing.T) {
	loc := locationFor("America/Chicago")
	now := time.Date(2026, 10, 20, 2, 59, 0, 0, loc)
	slot, ok := pruneSlot(now, "03:00")
	if !ok || !slot.After(now) || slot.Sub(now) != time.Minute {
		t.Fatalf("slot %v now %v", slot, now)
	}
	if locationFor("") != time.Local || locationFor("garbage") != time.Local {
		t.Fatal("unset or unknown zone must fall back to local")
	}
}

func TestDuePruneFiresOncePerDayAtSlot(t *testing.T) {
	loc := locationFor("America/Chicago")
	s := &Scheduler{}
	at := func(h, m, day int) time.Time { return time.Date(2026, 10, day, h, m, 0, 0, loc) }

	if s.duePrune(at(2, 59, 20), "03:00") {
		t.Fatal("must not run before the slot")
	}
	if !s.duePrune(at(3, 0, 20), "03:00") {
		t.Fatal("must run when the slot arrives")
	}
	if s.duePrune(at(3, 1, 20), "03:00") || s.duePrune(at(23, 0, 20), "03:00") {
		t.Fatal("must not run twice on the same day")
	}
	if !s.duePrune(at(3, 0, 21), "03:00") {
		t.Fatal("must run again the next day")
	}
}

func TestDuePruneNoImmediateRunWhenScheduleIsSetLate(t *testing.T) {
	loc := locationFor("America/Chicago")

	// Server (re)starts at 10:00 with a 03:00 slot already behind it: no catch-up prune.
	s := &Scheduler{}
	if s.duePrune(time.Date(2026, 10, 20, 10, 0, 0, 0, loc), "03:00") {
		t.Fatal("a restart after the slot must not prune")
	}

	// Editing the time to one that already passed today must not prune now either...
	if s.duePrune(time.Date(2026, 10, 20, 10, 5, 0, 0, loc), "09:00") {
		t.Fatal("setting a time earlier than now must wait for tomorrow")
	}
	// ...but a time still ahead of us fires when it arrives.
	if s.duePrune(time.Date(2026, 10, 20, 10, 6, 0, 0, loc), "10:30") {
		t.Fatal("not yet")
	}
	if !s.duePrune(time.Date(2026, 10, 20, 10, 30, 0, 0, loc), "10:30") {
		t.Fatal("a slot set for later today must fire today")
	}

	// Changing the zone re-evaluates against the new local clock.
	tokyo := locationFor("Asia/Tokyo")
	if s.duePrune(time.Date(2026, 10, 21, 1, 0, 0, 0, tokyo), "10:30") {
		t.Fatal("zone change after the slot must wait for the next day")
	}
}
