// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import "testing"

func TestCopilotScoreLabelToneDispatch(t *testing.T) {
	q := "trumbull county talkgroup 78 fd dispatch"
	if s := copilotScoreLabel(q, "78 FD DISP"); s < 200 {
		t.Fatalf("expected strong score for 78 FD DISP, got %d", s)
	}
	if s := copilotScoreLabel(q, "OH Trumbull"); s < 100 {
		t.Fatalf("expected match for OH Trumbull, got %d", s)
	}
}

func TestParseUintLoose(t *testing.T) {
	if parseUintLoose("78") != 78 {
		t.Fatal("78")
	}
	if parseUintLoose("78 fd") != 78 {
		t.Fatal("78 fd")
	}
}
