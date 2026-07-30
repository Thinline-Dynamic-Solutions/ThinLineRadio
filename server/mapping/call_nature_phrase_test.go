package mapping

import "testing"

func TestIsAcceptableCallNaturePhrase_StillRejectsMiningNoise(t *testing.T) {
	t.Parallel()

	reject := []string{
		"AB", // too short
		"ALARM DROP.",
		"UNIT ON SCENE", // contains " ON SCENE"
		"PERSON WITH A GUN IN THE PARKING LOT", // >6 words
		"UNKNOWN PROBLEM",
		"45 YEAR OLD MALE",
		"FIRE STATION RESPONSE", // contains " STATION "
		"PT COMPLAINING OF CHEST PAIN", // contains " COMPLAINING OF"
	}
	for _, p := range reject {
		if IsAcceptableCallNaturePhrase(p) {
			t.Fatalf("mining heuristic should reject %q", p)
		}
	}

	accept := []string{
		"SHOTS FIRED",
		"STRUCTURE FIRE",
		"PERSON WITH GUN",
		"ALARM DROP",
	}
	for _, p := range accept {
		if !IsAcceptableCallNaturePhrase(p) {
			t.Fatalf("mining heuristic should accept %q", p)
		}
	}
}
