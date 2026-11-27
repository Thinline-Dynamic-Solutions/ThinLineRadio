package main

import "testing"

func TestToneSetAdditionalPatternMatches(t *testing.T) {
	detector := NewToneDetector()
	dept := ToneSet{
		Id:        "dept",
		Label:     "Station 1",
		ATone:     &ToneSpec{Frequency: 1000, MinDuration: 0.8},
		BTone:     &ToneSpec{Frequency: 1500, MinDuration: 2.5},
		Tolerance: 10,
		Patterns:  []TonePattern{{LongTone: &ToneSpec{Frequency: 800, MinDuration: 4}}},
	}
	other := ToneSet{Id: "other", Label: "Station 2", LongTone: &ToneSpec{Frequency: 600, MinDuration: 4}, Tolerance: 10}

	allCall := &ToneSequence{HasTones: true, Tones: []Tone{{Frequency: 801, StartTime: 0, EndTime: 5, Duration: 5}}}
	got := detector.MatchToneSets(allCall, []ToneSet{dept, other})
	if len(got) != 1 || got[0].Id != "dept" {
		t.Fatalf("all-call matched %v, want only dept", got)
	}

	both := &ToneSequence{HasTones: true, Tones: []Tone{
		{Frequency: 1000, StartTime: 0, EndTime: 1, Duration: 1, ToneType: "A"},
		{Frequency: 1500, StartTime: 1, EndTime: 4, Duration: 3, ToneType: "B"},
		{Frequency: 800, StartTime: 4, EndTime: 9, Duration: 5},
	}}
	if got := detector.MatchToneSets(both, []ToneSet{dept}); len(got) != 1 {
		t.Fatalf("matching two patterns of one tone set returned %d alerts, want 1", len(got))
	}

	none := &ToneSequence{HasTones: true, Tones: []Tone{{Frequency: 900, StartTime: 0, EndTime: 5, Duration: 5}}}
	if got := detector.MatchToneSets(none, []ToneSet{dept}); len(got) != 0 {
		t.Fatalf("unrelated tone matched %v", got)
	}
}

func TestToneSetPatternsRoundTrip(t *testing.T) {
	in := []ToneSet{{Id: "a", Label: "A", LongTone: &ToneSpec{Frequency: 700}, Patterns: []TonePattern{{ATone: &ToneSpec{Frequency: 300}, BTone: &ToneSpec{Frequency: 400}}}}}
	raw, err := SerializeToneSets(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ParseToneSets(raw)
	if err != nil || len(out) != 1 || len(out[0].Patterns) != 1 || out[0].Patterns[0].BTone.Frequency != 400 {
		t.Fatalf("round trip = %+v, %v", out, err)
	}
	if v := out[0].patternVariants(); len(v) != 2 || v[1].ATone.Frequency != 300 || v[1].LongTone != nil || v[1].Id != "a" {
		t.Fatalf("variants = %+v", v)
	}
}
