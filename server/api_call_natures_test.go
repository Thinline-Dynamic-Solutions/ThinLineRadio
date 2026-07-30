// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"strings"
	"testing"
)

func TestSanitizeCallNaturePhrases_PreservesAdminInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "basic uppercase trim dedup",
			in:   []string{"  shots fired ", "SHOTS FIRED", "structure fire"},
			want: []string{"SHOTS FIRED", "STRUCTURE FIRE"},
		},
		{
			name: "period previously rejected by mining heuristic",
			in:   []string{"ALARM DROP."},
			want: []string{"ALARM DROP."},
		},
		{
			name: "on scene substring previously rejected",
			in:   []string{"UNIT ON SCENE"},
			want: []string{"UNIT ON SCENE"},
		},
		{
			name: "more than six words previously rejected",
			in:   []string{"PERSON WITH A GUN IN THE PARKING LOT"},
			want: []string{"PERSON WITH A GUN IN THE PARKING LOT"},
		},
		{
			name: "longer than mining max length previously rejected",
			in:   []string{strings.Repeat("A", 60)},
			want: []string{strings.Repeat("A", 60)},
		},
		{
			name: "station substring previously rejected",
			in:   []string{"FIRE STATION RESPONSE"},
			want: []string{"FIRE STATION RESPONSE"},
		},
		{
			name: "drops empty only",
			in:   []string{"", "  ", "OK"},
			want: []string{"OK"},
		},
		{
			name: "caps pathological paste",
			in:   []string{strings.Repeat("X", 200)},
			want: []string{strings.Repeat("X", maxManualCallNaturePhraseLen)},
		},
		{
			name: "short phrases still kept",
			in:   []string{"AB", "ABC"},
			want: []string{"AB", "ABC"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sanitizeCallNaturePhrases(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len=%d want %d; got=%v want=%v", len(got), len(tc.want), got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("idx %d: got %q want %q (full got=%v)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

func TestSanitizeCallNaturePhrases_DoesNotApplyMiningHeuristic(t *testing.T) {
	t.Parallel()

	// These are exactly the patterns IsAcceptableCallNaturePhrase rejects.
	rejectedByMining := []string{
		"ON SCENE CHECK",
		"COMPLAINING OF CHEST PAIN",
		"SHE'S ARMED",
		"HE'S DOWN",
		"THEY'RE FIGHTING",
		"TIME OUT STATUS",
		"CUSTOMER DISPUTE",
		"WIFE IS INJURED",
		"HUSBAND IS DOWN",
		"HIT HIS HEAD",
		"HIT HER ARM",
		"C/O PAIN",
		"UNKNOWN PROBLEM",
		"45 YEAR OLD MALE",
	}

	got := sanitizeCallNaturePhrases(rejectedByMining)
	if len(got) != len(rejectedByMining) {
		t.Fatalf("admin sanitize dropped phrases; got %d want %d: %v", len(got), len(rejectedByMining), got)
	}
	for i, p := range rejectedByMining {
		want := strings.ToUpper(strings.TrimSpace(p))
		if got[i] != want {
			t.Fatalf("idx %d: got %q want %q", i, got[i], want)
		}
	}
}
