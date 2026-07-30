package mapping

import "testing"

func TestSuggestNewPhrasesForNature_RadioCodes(t *testing.T) {
	t.Parallel()
	known := map[string]bool{"MOTOR VEHICLE ACCIDENT": true}
	got := SuggestNewPhrasesForNature(
		"Engine 1 respond for a 10-50 PI at Main and Oak, one subject complaining",
		"MOTOR VEHICLE ACCIDENT",
		known,
	)
	wantAny := []string{"10-50 PI", "10-50", "1050 PI", "1050"}
	found := map[string]bool{}
	for _, p := range got {
		found[p] = true
	}
	ok := false
	for _, w := range wantAny {
		if found[w] {
			ok = true
			break
		}
	}
	if !ok {
		t.Fatalf("expected one of %v in suggestions, got %v", wantAny, got)
	}
	// Should not dump unrelated dispatch filler.
	for _, bad := range []string{"ENGINE 1", "1 RESPOND", "RESPOND FOR"} {
		if found[bad] {
			t.Fatalf("unexpected noisy suggestion %q in %v", bad, got)
		}
	}
}

func TestSuggestNewPhrasesForNature_SkipsKnownAndUnknown(t *testing.T) {
	t.Parallel()
	known := map[string]bool{"10-50": true, "SHOTS FIRED": true}
	got := SuggestNewPhrasesForNature("10-50 at the park", "SHOTS FIRED", known)
	for _, p := range got {
		if p == "10-50" || p == "SHOTS FIRED" {
			t.Fatalf("should skip known/label phrases, got %v", got)
		}
	}
	if SuggestNewPhrasesForNature("something", "UNKNOWN PROBLEM", nil) != nil {
		t.Fatal("unknown labels should not suggest")
	}
}
