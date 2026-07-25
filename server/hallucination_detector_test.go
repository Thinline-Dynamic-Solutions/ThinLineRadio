package main

import "testing"

func TestHallucinationDetectionEnabled(t *testing.T) {
	cases := []struct {
		mode string
		want bool
	}{
		{"", false},
		{"off", false},
		{"OFF", false},
		{"learning", true},
		{"manual", true}, // legacy
		{"auto-remove", true},
		{"auto", true}, // legacy
	}
	for _, tc := range cases {
		if got := hallucinationDetectionEnabled(tc.mode); got != tc.want {
			t.Errorf("hallucinationDetectionEnabled(%q) = %v, want %v", tc.mode, got, tc.want)
		}
	}
}

func TestHallucinationAutoRemovalEnabled(t *testing.T) {
	cases := []struct {
		mode string
		want bool
	}{
		{"", false},
		{"off", false},
		{"learning", false},
		{"manual", false},
		{"auto", true},
		{"auto-remove", true}, // admin UI value — was previously ignored (#231)
		{"AUTO-REMOVE", true},
		{"autoremove", true},
		{"auto_remove", true},
	}
	for _, tc := range cases {
		if got := hallucinationAutoRemovalEnabled(tc.mode); got != tc.want {
			t.Errorf("hallucinationAutoRemovalEnabled(%q) = %v, want %v", tc.mode, got, tc.want)
		}
	}
}

func TestShouldAutoAddRespectsModeThresholds(t *testing.T) {
	ctrl := &Controller{
		Options: &Options{
			TranscriptionConfig: TranscriptionConfig{
				HallucinationMinOccurrences:      5,
				HallucinationConfidenceThreshold: 0.6,
			},
		},
		Systems: &Systems{List: []*System{{Id: 1}}}, // single-system install
	}
	hd := NewHallucinationDetector(ctrl)

	// Enough rejections + score for threshold 0.6 on a single system
	sh := &SuspectedHallucination{
		Phrase:        "THE BELL IS INVITED TO SOUND THREE TIMES",
		RejectedCount: 10,
		AcceptedCount: 0,
		SystemIds:     []uint64{1},
		Status:        "pending",
		FirstSeenAt:   0, // maximizes age score
	}
	sh.ConfidenceScore = hd.calculateConfidenceScore(sh)

	if !hd.shouldAutoAdd(sh) {
		t.Fatalf("shouldAutoAdd = false for single-system high-confidence phrase (score=%.1f)", sh.ConfidenceScore)
	}

	// Multi-system controller still requires 2 system IDs
	ctrl.Systems.List = []*System{{Id: 1}, {Id: 2}}
	if hd.shouldAutoAdd(sh) {
		t.Fatal("shouldAutoAdd = true with only 1 systemId on a multi-system server")
	}
	sh.SystemIds = []uint64{1, 2}
	sh.ConfidenceScore = hd.calculateConfidenceScore(sh)
	if !hd.shouldAutoAdd(sh) {
		t.Fatalf("shouldAutoAdd = false with 2 systems (score=%.1f)", sh.ConfidenceScore)
	}

	// Accepted once → never auto-add
	sh.AcceptedCount = 1
	sh.ConfidenceScore = hd.calculateConfidenceScore(sh)
	if hd.shouldAutoAdd(sh) {
		t.Fatal("shouldAutoAdd = true despite AcceptedCount > 0")
	}
}
