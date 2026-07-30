// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"rdio-scanner/server/mapping"
)

// TestCallNaturePhraseSave_EndToEndRegression documents the two failure modes
// that caused "phrases won't always save" and asserts the fixed pipeline
// (client pending-commit + server admin sanitize) preserves them.
func TestCallNaturePhraseSave_EndToEndRegression(t *testing.T) {
	t.Parallel()

	type step struct {
		label         string
		existing      []string
		pendingInput  string // typed in "Add phrase" but Save clicked without Add
		alreadyAdded  []string
		wantPersisted []string
	}

	cases := []step{
		{
			label:         "SHOTS FIRED",
			existing:      []string{"SHOTS FIRED"},
			pendingInput:  "gunshots heard",
			wantPersisted: []string{"SHOTS FIRED", "GUNSHOTS HEARD"},
		},
		{
			label:         "STRUCTURE FIRE",
			existing:      []string{"STRUCTURE FIRE"},
			alreadyAdded:  []string{"UNIT ON SCENE"}, // mining heuristic used to drop this
			wantPersisted: []string{"STRUCTURE FIRE", "UNIT ON SCENE"},
		},
		{
			label:         "PERSON WITH GUN",
			existing:      []string{"PERSON WITH GUN"},
			alreadyAdded:  []string{"PERSON WITH A GUN IN THE PARKING LOT"},
			wantPersisted: []string{"PERSON WITH GUN", "PERSON WITH A GUN IN THE PARKING LOT"},
		},
		{
			label:         "ALARM DROP",
			existing:      []string{"ALARM DROP"},
			pendingInput:  "alarm drop.",
			wantPersisted: []string{"ALARM DROP", "ALARM DROP."},
		},
		{
			label:         "MEDICAL",
			existing:      []string{"MEDICAL"},
			alreadyAdded:  []string{"FIRE STATION RESPONSE", "PT COMPLAINING OF CHEST PAIN"},
			wantPersisted: []string{"MEDICAL", "FIRE STATION RESPONSE", "PT COMPLAINING OF CHEST PAIN"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			// --- Client: mirror addPhraseFromInput + saveEdit payload build ---
			phrases := append([]string{}, tc.existing...)
			phrases = append(phrases, tc.alreadyAdded...)
			if pending := strings.ToUpper(strings.TrimSpace(tc.pendingInput)); pending != "" {
				dup := false
				for _, p := range phrases {
					if p == pending {
						dup = true
						break
					}
				}
				if !dup {
					phrases = append(phrases, pending)
				}
			}
			for i, p := range phrases {
				phrases[i] = strings.ToUpper(strings.TrimSpace(p))
			}

			// Prove old mining filter would have dropped at least one of the
			// non-label phrases in these regression cases (except the first,
			// which is about pending-input commit).
			if len(tc.alreadyAdded) > 0 || strings.Contains(tc.pendingInput, ".") {
				var droppedByOld []string
				for _, p := range phrases {
					if p == tc.label {
						continue
					}
					if !mapping.IsAcceptableCallNaturePhrase(p) {
						droppedByOld = append(droppedByOld, p)
					}
				}
				if len(droppedByOld) == 0 && (len(tc.alreadyAdded) > 0 || strings.Contains(tc.pendingInput, ".")) {
					// pending "gunshots heard" is acceptable to mining; skip assert
					if tc.pendingInput != "gunshots heard" {
						t.Fatalf("expected mining heuristic to reject some phrases in this case; got none from %v", phrases)
					}
				}
			}

			// --- Server: admin sanitize (fixed) ---
			persisted := sanitizeCallNaturePhrases(phrases)

			if len(persisted) != len(tc.wantPersisted) {
				t.Fatalf("persisted len=%d want %d\n got=%v\nwant=%v", len(persisted), len(tc.wantPersisted), persisted, tc.wantPersisted)
			}
			for i := range tc.wantPersisted {
				if persisted[i] != tc.wantPersisted[i] {
					t.Fatalf("idx %d: got %q want %q\nfull got=%v", i, persisted[i], tc.wantPersisted[i], persisted)
				}
			}

			// --- JSON round-trip like POST/PUT body ---
			body, err := json.Marshal(map[string]any{
				"label":   tc.label,
				"phrases": persisted,
			})
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatal(err)
			}
			again := sanitizeCallNaturePhrases(stringsFromAnySlice(decoded["phrases"]))
			if len(again) != len(persisted) {
				t.Fatalf("json round-trip changed phrase count: %v -> %v", persisted, again)
			}
		})
	}
}
