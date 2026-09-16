package main

import "testing"

func TestFilterProfanityMasksSwears(t *testing.T) {
	got := FilterProfanity("ENGINE 1 THAT FUCKING FIRE IS SHIT", nil)
	want := "ENGINE 1 THAT F****** FIRE IS S***"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFilterProfanityMasksCompoundsAndSlurs(t *testing.T) {
	got := FilterProfanity("THAT SHITHEAD CALLED HIM A SANDNIGGER", nil)
	want := "THAT S******* CALLED HIM A S*********"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFilterProfanityPreservesDispatchTerms(t *testing.T) {
	cases := []string{
		"ASSAULT IN PROGRESS AT 12 MAIN",
		"CLASS 1 MEDICAL ASSIGNMENT COMPLETE",
		"ASSIST THE ENGINE ON PASSENGER SIDE",
		"SHELL STATION ON MITCHELL ROAD",
		"HANCOCK STREET AND DICKSON AVENUE",
		"HELLO DISPATCH THIS IS ENGINE 2",
		"ANALYSIS COMPLETE ON THE CALL",
		"ASSOCIATE WITH THE OTHER UNIT",
		"VAN DYKE ROAD AND GYPSY LANE",
		"COON RAPIDS AND SQUAW CREEK",
		"CRACKER BARREL ON HOOKER AVENUE",
		"ORIENTAL BOULEVARD NEAR JAPAN ROAD",
		"GUINEA PIKE MEDICAL",
		"NIP IT AT THE SOURCE",
		"BLOODY NOSE WITH RECTAL BLEEDING",
	}
	for _, in := range cases {
		if got := FilterProfanity(in, nil); got != in {
			t.Errorf("false positive: %q became %q", in, got)
		}
	}
}

func TestFilterProfanityExtraWords(t *testing.T) {
	got := FilterProfanity("UNIT 12 ON SCENE AT THE DUMPSTER", []string{"dumpster"})
	want := "UNIT 12 ON SCENE AT THE D*******"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFilterProfanityPunctuationAndCase(t *testing.T) {
	got := FilterProfanity("what the fuck! Shit.", nil)
	want := "what the f***! S***."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestIsProfanityFilterEnabledDefaultOn(t *testing.T) {
	var cfg TranscriptionConfig
	if !cfg.IsProfanityFilterEnabled() {
		t.Fatal("expected default on when flag is unset")
	}
	off := false
	cfg.ProfanityFilterEnabled = &off
	if cfg.IsProfanityFilterEnabled() {
		t.Fatal("expected off when flag is false")
	}
	on := true
	cfg.ProfanityFilterEnabled = &on
	if !cfg.IsProfanityFilterEnabled() {
		t.Fatal("expected on when flag is true")
	}
}

func TestApplyTranscriptProfanityFilterHonorsOption(t *testing.T) {
	off := false
	ctrl := &Controller{
		Options: &Options{
			TranscriptionConfig: TranscriptionConfig{
				ProfanityFilterEnabled: &off,
			},
		},
	}
	in := "THAT FUCKING CALL"
	if got := ctrl.applyTranscriptProfanityFilter(in); got != in {
		t.Fatalf("disabled filter changed text: %q", got)
	}

	on := true
	ctrl.Options.TranscriptionConfig.ProfanityFilterEnabled = &on
	got := ctrl.applyTranscriptProfanityFilter(in)
	if got == in || !containsRune(got, '*') {
		t.Fatalf("enabled filter did not mask: %q", got)
	}
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}
