// Copyright (C) 2026 Carter Carling <carter@cartercarling.com>
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>

package main

import (
	"strings"
	"testing"
)

// testConfig is the word list used across all tests
// The word lists were created from errors found in real dispatch transcripts
var testConfig = TranscriptConfig{
	UnitTypes: []FuzzyWord{
		{Word: "ENGINE", MaxDistance: 2},
		{Word: "TRUCK", MaxDistance: 1, Aliases: []string{"TREK", "TRUK", "CHUG", "TUG", "CHOP"}},
		{Word: "TOWER", MaxDistance: 1},
		{Word: "LADDER", MaxDistance: 2},
		{Word: "CHAT", MaxDistance: 0, Aliases: []string{"CAT", "CHAD", "CHATT"}},
		{Word: "SQUAD", MaxDistance: 1},
		{Word: "QUINT", MaxDistance: 1, Aliases: []string{"QUINN"}},
		{Word: "HAZMAT", MaxDistance: 2},
		{Word: "BATTALION", MaxDistance: 2},
		{Word: "RED", MaxDistance: 0, Aliases: []string{"WRED", "REDD", "RAD"}},
		{Word: "AMBULANCE", MaxDistance: 2},
		{Word: "RESCUE", MaxDistance: 2, Aliases: []string{"RESQ", "RESQUE", "RESQEU"}, Reject: []string{"ELEVATOR"}},
		{Word: "UTILITY", MaxDistance: 2},
		{Word: "TENDER", MaxDistance: 2},
		{Word: "REHAB", MaxDistance: 1},
		{Word: "COMPANY", MaxDistance: 2},
		{Word: "LU", MaxDistance: 0, Aliases: []string{"LOU", "LUA", "LUR"}},
		{Word: "MEDIC", MaxDistance: 0},
	},
	UnitPrefixes: []FuzzyWord{
		{Word: "MEDIC", MaxDistance: 1},
		{Word: "HEAVY", MaxDistance: 1},
		{Word: "HEAVY MEDIC", MaxDistance: 2},
		{Word: "BRUSH", MaxDistance: 1},
		{Word: "TACTICAL", MaxDistance: 2},
	},
	DispatchNames: []FuzzyWord{
		{Word: "CITY", MaxDistance: 1},
		{Word: "VECC", MaxDistance: 1, Aliases: []string{"VEC", "DECK", "TECH", "VAC", "VEX", "VIC"}},
		{Word: "SANDY", MaxDistance: 1},
	},
	ChannelSeparators: []FuzzyWord{
		{Word: "FIRE", MaxDistance: 1},
	},
	ChannelShorthands: []ChannelShorthand{
		{Label: "SF", Dispatch: "SANDY", Separator: "FIRE"},
		{Label: "BACKFIRE", Dispatch: "VECC", Separator: "FIRE"},
		{Label: "CITY AIR", Dispatch: "CITY", Separator: "FIRE"},
		{Label: "X-FIRE", Dispatch: "VECC", Separator: "FIRE"},
	},
	Corrections: []FuzzyWord{
		{Word: "SHORT FALL", MaxDistance: 2, Aliases: []string{"SHEFFIELD", "CHILWORTH FALL", "CHILLICOTHE FALL", "SHORTFALL", "SHILLER'S FALL"}},
		{Word: "UNKNOWN MEDICAL", MaxDistance: 1, Aliases: []string{"A KNOWN MEDICAL"}},
		{Word: "STROKE OR CVA", MaxDistance: 1},
	},
}

var testParser = NewTranscriptParser(testConfig)

// --- Levenshtein ---

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"ENGINE", "ENGINE", 0},
		{"", "ABC", 3},
		{"ABC", "", 3},
		{"ENGIN", "ENGINE", 1},
		{"ENGNE", "ENGINE", 1},
		{"TRUCK", "TREK", 2},
		{"RESQ", "RESCUE", 3},
		{"KITTEN", "SITTING", 3},
	}
	for _, tt := range tests {
		got := levenshtein(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestLevenshteinSymmetric(t *testing.T) {
	pairs := [][2]string{
		{"ABC", "DEF"},
		{"ENGINE", "ENGIN"},
		{"TRUCK", "TREK"},
	}
	for _, p := range pairs {
		ab := levenshtein(p[0], p[1])
		ba := levenshtein(p[1], p[0])
		if ab != ba {
			t.Errorf("levenshtein(%q,%q)=%d but levenshtein(%q,%q)=%d", p[0], p[1], ab, p[1], p[0], ba)
		}
	}
}

// --- Helper functions ---

func TestTokenize(t *testing.T) {
	tokens := tokenize("ENGINE 5 RESPONDING ON SCENE")
	texts := make([]string, len(tokens))
	for i, tk := range tokens {
		texts[i] = tk.text
	}
	want := []string{"ENGINE", "5", "RESPONDING", "ON", "SCENE"}
	if len(texts) != len(want) {
		t.Fatalf("tokenize got %v, want %v", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Errorf("token[%d] = %q, want %q", i, texts[i], want[i])
		}
	}
}

func TestTokenizePreservesIndex(t *testing.T) {
	input := "AB 12 CD"
	tokens := tokenize(input)
	for _, tk := range tokens {
		got := input[tk.index : tk.index+len(tk.text)]
		if got != tk.text {
			t.Errorf("token %q at index %d does not match source %q", tk.text, tk.index, got)
		}
	}
}

func TestIsDigitToken(t *testing.T) {
	tests := []struct {
		s      string
		maxLen int
		want   bool
	}{
		{"123", 3, true},
		{"1234", 3, false},
		{"0", 3, true},
		{"", 3, false},
		{"12A", 3, false},
		{"99", 2, true},
	}
	for _, tt := range tests {
		got := isDigitToken(tt.s, tt.maxLen)
		if got != tt.want {
			t.Errorf("isDigitToken(%q, %d) = %v, want %v", tt.s, tt.maxLen, got, tt.want)
		}
	}
}

// --- Unit parsing ---

func TestParseUnitsExact(t *testing.T) {
	results := testParser.ParseUnits("ENGINE 5 TRUCK 12")
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Apparatus != "ENGINE" || results[0].Number != "5" || results[0].Fuzzy {
		t.Errorf("result[0] = %+v, want ENGINE 5 non-fuzzy", results[0])
	}
	if results[1].Apparatus != "TRUCK" || results[1].Number != "12" || results[1].Fuzzy {
		t.Errorf("result[1] = %+v, want TRUCK 12 non-fuzzy", results[1])
	}
}

func TestParseUnitsWithPrefix(t *testing.T) {
	results := testParser.ParseUnits("MEDIC ENGINE 5")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Prefix != "MEDIC" || r.Apparatus != "ENGINE" || r.Number != "5" {
		t.Errorf("got %+v, want MEDIC ENGINE 5", r)
	}
}

func TestParseUnitsFuzzy(t *testing.T) {
	results := testParser.ParseUnits("ENGNE 5")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Apparatus != "ENGINE" || r.Number != "5" || !r.Fuzzy {
		t.Errorf("got %+v, want fuzzy ENGINE 5", r)
	}
}

func TestParseUnitsAlias(t *testing.T) {
	results := testParser.ParseUnits("TREK 3")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Apparatus != "TRUCK" || r.Number != "3" || !r.Fuzzy {
		t.Errorf("got %+v, want fuzzy TRUCK 3", r)
	}
}

func TestParseUnitsDedup(t *testing.T) {
	results := testParser.ParseUnits("ENGINE 5 AND ENGINE 5")
	if len(results) != 1 {
		t.Fatalf("expected 1 result (deduped), got %d", len(results))
	}
	if results[0].Apparatus != "ENGINE" || results[0].Number != "5" {
		t.Errorf("got %+v, want ENGINE 5", results[0])
	}
}

func TestParseUnitsReject(t *testing.T) {
	// "ELEVATOR RESCUE 160" should NOT parse RESCUE 160 as a unit
	results := testParser.ParseUnits("TRUCK 2, ELEVATOR RESCUE 160 SOUTH 300 WEST")
	if len(results) != 1 {
		t.Fatalf("expected 1 result (TRUCK 2 only), got %d: %+v", len(results), results)
	}
	if results[0].Apparatus != "TRUCK" || results[0].Number != "2" {
		t.Errorf("got %+v, want TRUCK 2", results[0])
	}
}

func TestParseUnitsRejectDoesNotAffectNormal(t *testing.T) {
	// "RESCUE 3" without a reject word before it should still match
	results := testParser.ParseUnits("RESCUE 3 RESPONDING")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Apparatus != "RESCUE" || results[0].Number != "3" {
		t.Errorf("got %+v, want RESCUE 3", results[0])
	}
}

func TestParseUnitsMedicAsUnit(t *testing.T) {
	results := testParser.ParseUnits("MEDIC 6 RESPONDING")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %+v", len(results), results)
	}
	if results[0].Apparatus != "MEDIC" || results[0].Number != "6" {
		t.Errorf("got %+v, want MEDIC 6", results[0])
	}
}

func TestParseUnitsMedicAsPrefix(t *testing.T) {
	results := testParser.ParseUnits("MEDIC ENGINE 5 RESPONDING")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %+v", len(results), results)
	}
	if results[0].Prefix != "MEDIC" || results[0].Apparatus != "ENGINE" || results[0].Number != "5" {
		t.Errorf("got %+v, want MEDIC ENGINE 5", results[0])
	}
}

func TestParseUnitsMedicBoth(t *testing.T) {
	results := testParser.ParseUnits("MEDIC 6 AND MEDIC ENGINE 5")
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(results), results)
	}
}

func TestParseUnitsEmpty(t *testing.T) {
	results := testParser.ParseUnits("")
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestParseUnitsAllTypes(t *testing.T) {
	// Every unit type (except MEDIC, which has its own prefix/unit duality tests)
	// should be recognised in an exact match
	types := []string{
		"ENGINE", "TRUCK", "TOWER", "LADDER", "CHAT", "SQUAD",
		"QUINT", "HAZMAT", "BATTALION", "RED", "AMBULANCE",
		"RESCUE", "UTILITY", "TENDER", "REHAB", "COMPANY", "LU",
	}
	for _, typ := range types {
		results := testParser.ParseUnits(typ + " 1")
		if len(results) != 1 {
			t.Errorf("%s 1: expected 1 result, got %d", typ, len(results))
			continue
		}
		if results[0].Apparatus != typ {
			t.Errorf("%s 1: apparatus = %q, want %q", typ, results[0].Apparatus, typ)
		}
		if results[0].Fuzzy {
			t.Errorf("%s 1: should not be fuzzy", typ)
		}
	}
}

func TestParseUnitsAllAliases(t *testing.T) {
	tests := []struct {
		alias string
		want  string
	}{
		{"TREK", "TRUCK"},
		{"TRUK", "TRUCK"},
		{"CHUG", "TRUCK"},
		{"TUG", "TRUCK"},
		{"CHOP", "TRUCK"},
		{"CAT", "CHAT"},
		{"CHAD", "CHAT"},
		{"CHATT", "CHAT"},
		{"QUINN", "QUINT"},
		{"WRED", "RED"},
		{"REDD", "RED"},
		{"RAD", "RED"},
		{"RESQ", "RESCUE"},
		{"RESQUE", "RESCUE"},
		{"RESQEU", "RESCUE"},
		{"LOU", "LU"},
		{"LUA", "LU"},
		{"LUR", "LU"},
	}
	for _, tt := range tests {
		results := testParser.ParseUnits(tt.alias + " 7")
		if len(results) != 1 {
			t.Errorf("%s 7: expected 1 result, got %d", tt.alias, len(results))
			continue
		}
		if results[0].Apparatus != tt.want {
			t.Errorf("%s 7: apparatus = %q, want %q", tt.alias, results[0].Apparatus, tt.want)
		}
		if !results[0].Fuzzy {
			t.Errorf("%s 7: should be fuzzy", tt.alias)
		}
	}
}

func TestParseUnitsFuzzyDistance(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// ENGINE maxDistance=2: "ENGIN" is distance 1, "ENGNE" is distance 1
		{"ENGIN 3", "ENGINE"},
		{"ENGNE 3", "ENGINE"},
		// LADDER maxDistance=2: "LADDR" is distance 2
		{"LADDR 3", "LADDER"},
		// SQUAD maxDistance=1: "SQAD" is distance 1
		{"SQAD 3", "SQUAD"},
	}
	for _, tt := range tests {
		results := testParser.ParseUnits(tt.input)
		if len(results) != 1 {
			t.Errorf("%q: expected 1 result, got %d", tt.input, len(results))
			continue
		}
		if results[0].Apparatus != tt.want {
			t.Errorf("%q: apparatus = %q, want %q", tt.input, results[0].Apparatus, tt.want)
		}
		if !results[0].Fuzzy {
			t.Errorf("%q: should be fuzzy", tt.input)
		}
	}
}

func TestParseUnitsFuzzyTooFar(t *testing.T) {
	// TRUCK maxDistance=1; "TRAK" is distance 2 — should not match
	results := testParser.ParseUnits("TRAK 5")
	for _, r := range results {
		if r.Apparatus == "TRUCK" {
			t.Errorf("TRAK should not fuzzy-match TRUCK (distance > maxDistance)")
		}
	}
}

func TestParseUnitsAllPrefixes(t *testing.T) {
	prefixes := []string{"MEDIC", "HEAVY", "BRUSH", "TACTICAL"}
	for _, p := range prefixes {
		results := testParser.ParseUnits(p + " ENGINE 1")
		if len(results) != 1 {
			t.Errorf("%s ENGINE 1: expected 1 result, got %d", p, len(results))
			continue
		}
		if results[0].Prefix != p {
			t.Errorf("%s ENGINE 1: prefix = %q, want %q", p, results[0].Prefix, p)
		}
	}
}

func TestParseUnitsMultiWordPrefix(t *testing.T) {
	results := testParser.ParseUnits("HEAVY MEDIC ENGINE 1")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Prefix != "HEAVY MEDIC" {
		t.Errorf("prefix = %q, want HEAVY MEDIC", results[0].Prefix)
	}
}

func TestParseUnitsThreeDigitNumber(t *testing.T) {
	results := testParser.ParseUnits("ENGINE 123")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Number != "123" {
		t.Errorf("number = %q, want 123", results[0].Number)
	}
}

func TestParseUnitsFourDigitNumberIgnored(t *testing.T) {
	results := testParser.ParseUnits("ENGINE 1234")
	if len(results) != 0 {
		t.Errorf("expected 0 results for 4-digit number, got %d: %+v", len(results), results)
	}
}

func TestParseUnitsMultipleDistinct(t *testing.T) {
	results := testParser.ParseUnits("ENGINE 5 LADDER 12 RESCUE 3")
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	want := []struct{ app, num string }{
		{"ENGINE", "5"},
		{"LADDER", "12"},
		{"RESCUE", "3"},
	}
	for i, w := range want {
		if results[i].Apparatus != w.app || results[i].Number != w.num {
			t.Errorf("result[%d] = %s %s, want %s %s", i, results[i].Apparatus, results[i].Number, w.app, w.num)
		}
	}
}

func TestParseUnitsCaseInsensitive(t *testing.T) {
	results := testParser.ParseUnits("engine 5")
	if len(results) != 1 {
		t.Fatalf("expected 1 result for lowercase, got %d", len(results))
	}
	if results[0].Apparatus != "ENGINE" {
		t.Errorf("apparatus = %q, want ENGINE", results[0].Apparatus)
	}
}

func TestParseUnitsNoNumber(t *testing.T) {
	results := testParser.ParseUnits("ENGINE RESPONDING")
	if len(results) != 0 {
		t.Errorf("expected 0 results for apparatus without number, got %d: %+v", len(results), results)
	}
}

func TestParseUnitsDedupCollectsRaw(t *testing.T) {
	// Exact match and alias for the same unit should dedup but collect both raw strings
	results := testParser.ParseUnits("TRUCK 5 IS ON SCENE TREK 5 CLEAR")
	if len(results) != 1 {
		t.Fatalf("expected 1 deduped result, got %d", len(results))
	}
	if len(results[0].Raw) < 2 {
		t.Errorf("expected at least 2 raw strings, got %d: %v", len(results[0].Raw), results[0].Raw)
	}
}

func TestParseUnitsFuzzyPrefix(t *testing.T) {
	// MEDIC maxDistance=1: "MDIC" is distance 1
	results := testParser.ParseUnits("MDIC ENGINE 5")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Prefix != "MEDIC" {
		t.Errorf("prefix = %q, want MEDIC", results[0].Prefix)
	}
}

func TestParseUnitsPrefixUpgrade(t *testing.T) {
	// Pass 3: exact match without prefix should get upgraded when a prefix precedes it
	results := testParser.ParseUnits("BRUSH ENGINE 5")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Prefix != "BRUSH" || results[0].Apparatus != "ENGINE" || results[0].Number != "5" {
		t.Errorf("got %+v, want BRUSH ENGINE 5", results[0])
	}
}

func TestParseUnits_NumberBeforeApparatus(t *testing.T) {
	// "5 ENGINE" has the number before the apparatus — should not panic
	results := testParser.ParseUnits("5 ENGINE")
	for _, r := range results {
		if r.Apparatus != "ENGINE" {
			t.Errorf("expected apparatus ENGINE, got %q", r.Apparatus)
		}
	}
}

func TestParseUnits_MultiplePrefix(t *testing.T) {
	results := testParser.ParseUnits("AIR MEDIC ENGINE 5")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Apparatus != "ENGINE" || r.Number != "5" {
		t.Errorf("got %+v, want ENGINE 5", r)
	}
}

// --- Channel parsing ---

func TestParseChannelsExact(t *testing.T) {
	results := testParser.ParseChannels("CITY FIRE 3")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Dispatch != "CITY" || r.Separator != "FIRE" || r.Channel != "3" || r.Fuzzy {
		t.Errorf("got %+v, want CITY FIRE 3 non-fuzzy", r)
	}
}

func TestParseChannelsShorthand(t *testing.T) {
	results := testParser.ParseChannels("SF 3")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Dispatch != "SANDY" || r.Separator != "FIRE" || r.Channel != "3" || r.Fuzzy {
		t.Errorf("got %+v, want SANDY/FIRE/3 non-fuzzy", r)
	}
}

func TestParseChannelsBackfire(t *testing.T) {
	results := testParser.ParseChannels("BACKFIRE 2")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Dispatch != "VECC" || r.Separator != "FIRE" || r.Channel != "2" || r.Fuzzy {
		t.Errorf("got %+v, want VECC/FIRE/2 non-fuzzy", r)
	}
}

func TestParseChannelsFuzzy(t *testing.T) {
	// "CITI" is distance 1 from "CITY"
	results := testParser.ParseChannels("CITI FIRE 1")
	if len(results) < 1 {
		t.Fatalf("expected at least 1 result, got %d", len(results))
	}
	found := false
	for _, r := range results {
		if r.Dispatch == "CITY" && r.Channel == "1" && r.Fuzzy {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected fuzzy CITY FIRE 1 in results: %+v", results)
	}
}

func TestParseChannelsFuzzyFire(t *testing.T) {
	// "FIRA" is distance 1 from "FIRE" — fuzzy pass should pick it up
	results := testParser.ParseChannels("CITY FIRA 3")
	if len(results) < 1 {
		t.Fatalf("expected at least 1 result for fuzzy separator, got %d", len(results))
	}
	found := false
	for _, r := range results {
		if r.Dispatch == "CITY" && r.Channel == "3" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected CITY/*/3 in results: %+v", results)
	}
}

func TestParseChannelsAllDispatches(t *testing.T) {
	dispatches := []string{"CITY", "VECC", "SANDY"}
	for _, d := range dispatches {
		results := testParser.ParseChannels(d + " FIRE 1")
		if len(results) != 1 {
			t.Errorf("%s FIRE 1: expected 1 result, got %d", d, len(results))
			continue
		}
		if results[0].Dispatch != d || results[0].Channel != "1" || results[0].Fuzzy {
			t.Errorf("got %+v, want %s FIRE 1 non-fuzzy", results[0], d)
		}
	}
}

func TestParseChannelsDispatchAliases(t *testing.T) {
	aliases := []struct {
		alias string
		want  string
	}{
		{"VEC", "VECC"},
		{"DECK", "VECC"},
		{"TECH", "VECC"},
		{"VAC", "VECC"},
		{"VEX", "VECC"},
	}
	for _, tt := range aliases {
		results := testParser.ParseChannels(tt.alias + " FIRE 2")
		if len(results) < 1 {
			t.Errorf("%s FIRE 2: expected at least 1 result, got 0", tt.alias)
			continue
		}
		found := false
		for _, r := range results {
			if r.Dispatch == tt.want && r.Channel == "2" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s FIRE 2: expected dispatch %s in results: %+v", tt.alias, tt.want, results)
		}
	}
}

func TestParseChannelsShorthandDash(t *testing.T) {
	results := testParser.ParseChannels("SF-3")
	if len(results) != 1 {
		t.Fatalf("expected 1 result for SF-3, got %d", len(results))
	}
	if results[0].Dispatch != "SANDY" || results[0].Channel != "3" {
		t.Errorf("got %+v, want SANDY/FIRE/3", results[0])
	}
}

func TestParseChannelsShorthandNoSeparator(t *testing.T) {
	results := testParser.ParseChannels("SF3")
	if len(results) != 1 {
		t.Fatalf("expected 1 result for SF3, got %d", len(results))
	}
	if results[0].Dispatch != "SANDY" || results[0].Channel != "3" {
		t.Errorf("got %+v, want SANDY/FIRE/3", results[0])
	}
}

func TestParseChannelsXFire(t *testing.T) {
	results := testParser.ParseChannels("X-FIRE 4")
	if len(results) != 1 {
		t.Fatalf("expected 1 result for X-FIRE 4, got %d", len(results))
	}
	if results[0].Dispatch != "VECC" || results[0].Channel != "4" {
		t.Errorf("got %+v, want VECC/FIRE/4", results[0])
	}
}

func TestParseChannelsCityAir(t *testing.T) {
	results := testParser.ParseChannels("CITY AIR 2")
	if len(results) != 1 {
		t.Fatalf("expected 1 result for CITY AIR 2, got %d", len(results))
	}
	if results[0].Dispatch != "CITY" || results[0].Channel != "2" {
		t.Errorf("got %+v, want CITY/FIRE/2", results[0])
	}
}

func TestParseChannelsTwoDigit(t *testing.T) {
	results := testParser.ParseChannels("VECC FIRE 12")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Channel != "12" {
		t.Errorf("channel = %q, want 12", results[0].Channel)
	}
}

func TestParseChannelsThreeDigitIgnored(t *testing.T) {
	results := testParser.ParseChannels("CITY FIRE 123")
	if len(results) != 0 {
		t.Errorf("expected 0 results for 3-digit channel, got %d: %+v", len(results), results)
	}
}

func TestParseChannelsCaseInsensitive(t *testing.T) {
	results := testParser.ParseChannels("city fire 3")
	if len(results) != 1 {
		t.Fatalf("expected 1 result for lowercase, got %d", len(results))
	}
	if results[0].Dispatch != "CITY" || results[0].Channel != "3" {
		t.Errorf("got %+v, want CITY FIRE 3", results[0])
	}
}

func TestParseChannelsDedup(t *testing.T) {
	results := testParser.ParseChannels("CITY FIRE 3 AND CITY FIRE 3")
	if len(results) != 1 {
		t.Errorf("expected 1 deduped result, got %d", len(results))
	}
}

func TestParseChannelsMultiple(t *testing.T) {
	results := testParser.ParseChannels("CITY FIRE 1 VECC FIRE 2")
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestParseChannels_NoNumber(t *testing.T) {
	results := testParser.ParseChannels("CITY FIRE")
	if len(results) != 0 {
		t.Errorf("expected 0 results for channel with no number, got %d: %+v", len(results), results)
	}
}

// TestParseChannelsOtherSeparator verifies that non-FIRE separators work when configured.
func TestParseChannelsOtherSeparator(t *testing.T) {
	p := NewTranscriptParser(TranscriptConfig{
		DispatchNames: []FuzzyWord{
			{Word: "METRO", MaxDistance: 1},
			{Word: "COUNTY", MaxDistance: 1},
		},
		ChannelSeparators: []FuzzyWord{
			{Word: "POLICE", MaxDistance: 1},
			{Word: "EMS", MaxDistance: 0},
			{Word: "OPS", MaxDistance: 0},
		},
	})

	tests := []struct {
		input    string
		dispatch string
		sep      string
		channel  string
	}{
		{"METRO POLICE 2", "METRO", "POLICE", "2"},
		{"COUNTY EMS 1", "COUNTY", "EMS", "1"},
		{"METRO OPS 5", "METRO", "OPS", "5"},
	}
	for _, tt := range tests {
		results := p.ParseChannels(tt.input)
		if len(results) != 1 {
			t.Errorf("%q: expected 1 result, got %d", tt.input, len(results))
			continue
		}
		r := results[0]
		if r.Dispatch != tt.dispatch || r.Separator != tt.sep || r.Channel != tt.channel {
			t.Errorf("%q: got %+v, want dispatch=%s sep=%s channel=%s", tt.input, r, tt.dispatch, tt.sep, tt.channel)
		}
	}
}

// TestParseChannelsNoSeparator verifies that {Dispatch} {Number} matching works
// when no ChannelSeparators are configured.
func TestParseChannelsNoSeparator(t *testing.T) {
	p := NewTranscriptParser(TranscriptConfig{
		DispatchNames: []FuzzyWord{
			{Word: "ALPHA", MaxDistance: 0},
			{Word: "BRAVO", MaxDistance: 0},
		},
	})

	results := p.ParseChannels("ALPHA 3 AND BRAVO 7")
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(results), results)
	}
	if results[0].Dispatch != "ALPHA" || results[0].Separator != "" || results[0].Channel != "3" {
		t.Errorf("result[0] = %+v, want ALPHA/3", results[0])
	}
	if results[1].Dispatch != "BRAVO" || results[1].Separator != "" || results[1].Channel != "7" {
		t.Errorf("result[1] = %+v, want BRAVO/7", results[1])
	}
}

// TestParseChannelsSeparatorPopulatedOnFuzzy verifies that the Separator field is
// set correctly on fuzzy (Pass 2) matches, not just exact (Pass 1) matches.
func TestParseChannelsSeparatorPopulatedOnFuzzy(t *testing.T) {
	// "CITI" is a fuzzy match for "CITY" — result should still carry Separator "FIRE"
	results := testParser.ParseChannels("CITI FIRE 2")
	if len(results) < 1 {
		t.Fatalf("expected at least 1 result, got 0")
	}
	for _, r := range results {
		if r.Dispatch == "CITY" && r.Channel == "2" {
			if r.Separator != "FIRE" {
				t.Errorf("fuzzy match: separator = %q, want FIRE", r.Separator)
			}
			return
		}
	}
	t.Errorf("expected CITY/FIRE/2 in results: %+v", results)
}

// --- CorrectTranscript ---

func TestCorrectTranscriptEmpty(t *testing.T) {
	result := testParser.CorrectTranscript("")
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestCorrectTranscriptNoMatch(t *testing.T) {
	// Input that contains none of the configured corrections should pass through unchanged.
	input := "SOME RANDOM TRANSCRIPT"
	result := testParser.CorrectTranscript(input)
	if result != input {
		t.Errorf("expected %q, got %q", input, result)
	}
}

func TestCorrectTranscriptWithCorrections(t *testing.T) {
	p := NewTranscriptParser(TranscriptConfig{
		Corrections: []FuzzyWord{
			{Word: "DISPATCH", MaxDistance: 2, Aliases: []string{"DISPACH"}},
			{Word: "RESPOND", MaxDistance: 0, Aliases: []string{"RSPOND", "RESPND"}},
			{Word: "STROKE OR CVA", MaxDistance: 1},
		},
	})

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"exact alias", "DISPACH TO SCENE", "DISPATCH TO SCENE"},
		{"fuzzy match", "DISPATC TO SCENE", "DISPATCH TO SCENE"},
		{"multiple corrections", "DISPACH AND RESPND", "DISPATCH AND RESPOND"},
		{"no match passthrough", "ENGINE 5 RESPONDING", "ENGINE 5 RESPONDING"},
		{"alias case insensitive", "rspond to call", "RESPOND TO CALL"},
		{"stroke or cva 1", "RESPOND TO STROKE OR CPA", "RESPOND TO STROKE OR CVA"},
		{"stroke or cva 2", "RESPOND TO STROKE OR CDA", "RESPOND TO STROKE OR CVA"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.CorrectTranscript(tt.input)
			if got != tt.want {
				t.Errorf("CorrectTranscript(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCorrectTranscriptMultiWord(t *testing.T) {
	p := NewTranscriptParser(TranscriptConfig{
		Corrections: []FuzzyWord{
			{Word: "SHORT FALL", MaxDistance: 2, Aliases: []string{"SHEFFIELD", "SHORTFALL"}},
			{Word: "UNKNOWN MEDICAL", MaxDistance: 1, Aliases: []string{"A KNOWN MEDICAL"}},
		},
	})

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"multi-word alias", "SHEFFIELD IS ON SCENE", "SHORT FALL IS ON SCENE"},
		{"single word alias", "RESPOND TO SHORTFALL", "RESPOND TO SHORT FALL"},
		{"multi-word fuzzy", "SHORT FAL RESPONDING", "SHORT FALL RESPONDING"},
		{"multi-word alias with spaces", "A KNOWN MEDICAL EMERGENCY", "UNKNOWN MEDICAL EMERGENCY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.CorrectTranscript(tt.input)
			if got != tt.want {
				t.Errorf("CorrectTranscript(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- ParseTranscript (combined) ---

func TestParseTranscript_Empty(t *testing.T) {
	units, channels := testParser.ParseTranscript("")
	if units == nil {
		t.Error("ParseTranscript units = nil, want empty slice")
	}
	if channels == nil {
		t.Error("ParseTranscript channels = nil, want empty slice")
	}
	if len(units) != 0 {
		t.Errorf("ParseTranscript units len = %d, want 0", len(units))
	}
	if len(channels) != 0 {
		t.Errorf("ParseTranscript channels len = %d, want 0", len(channels))
	}
}

func TestParseTranscriptIntegration(t *testing.T) {
	units, channels := testParser.ParseTranscript("ENGINE 5 RESPONDING ON CITY FIRE 3")

	if len(units) != 1 {
		t.Fatalf("expected 1 unit, got %d", len(units))
	}
	if units[0].Apparatus != "ENGINE" || units[0].Number != "5" {
		t.Errorf("unit = %+v, want ENGINE 5", units[0])
	}

	if len(channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(channels))
	}
	if channels[0].Dispatch != "CITY" || channels[0].Channel != "3" {
		t.Errorf("channel = %+v, want CITY FIRE 3", channels[0])
	}
}

func TestParseTranscriptMultipleUnitsAndChannels(t *testing.T) {
	units, channels := testParser.ParseTranscript("ENGINE 5 TRUCK 3 RESPONDING ON VECC FIRE 2 AND CITY FIRE 1")
	if len(units) != 2 {
		t.Fatalf("expected 2 units, got %d", len(units))
	}
	if len(channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(channels))
	}
}

func TestParseTranscriptFuzzyUnitsAndChannels(t *testing.T) {
	units, channels := testParser.ParseTranscript("ENGNE 5 RESPONDING ON CITI FIRE 3")

	if len(units) != 1 {
		t.Fatalf("expected 1 unit, got %d", len(units))
	}
	if units[0].Apparatus != "ENGINE" || !units[0].Fuzzy {
		t.Errorf("unit = %+v, want fuzzy ENGINE 5", units[0])
	}

	if len(channels) < 1 {
		t.Fatalf("expected at least 1 channel, got %d", len(channels))
	}
	found := false
	for _, c := range channels {
		if c.Dispatch == "CITY" && c.Channel == "3" && c.Fuzzy {
			found = true
		}
	}
	if !found {
		t.Errorf("expected fuzzy CITY FIRE 3 in channels: %+v", channels)
	}
}

func TestParseTranscriptShorthandWithUnits(t *testing.T) {
	units, channels := testParser.ParseTranscript("ENGINE 1 RESPONDING SF 3")
	if len(units) != 1 {
		t.Fatalf("expected 1 unit, got %d", len(units))
	}
	if len(channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(channels))
	}
	if channels[0].Dispatch != "SANDY" || channels[0].Channel != "3" {
		t.Errorf("channel = %+v, want SANDY/FIRE/3", channels[0])
	}
}

// --- AnnotateTranscript ---

func TestAnnotateTranscriptBasic(t *testing.T) {
	corrected, annotations := testParser.AnnotateTranscript("ENGINE 5 RESPONDING ON CITY FIRE 3")
	if corrected == "" {
		t.Fatal("expected non-empty corrected string")
	}
	if len(annotations) != 2 {
		t.Fatalf("expected 2 annotations (1 unit + 1 channel), got %d: %+v", len(annotations), annotations)
	}

	unitAnn := annotations[0]
	if unitAnn.Type != "unit" || unitAnn.Apparatus != "ENGINE" || unitAnn.Number != "5" {
		t.Errorf("annotation[0] = %+v, want unit ENGINE 5", unitAnn)
	}
	if unitAnn.Start < 0 || unitAnn.End <= unitAnn.Start {
		t.Errorf("unit annotation has invalid offsets: start=%d end=%d", unitAnn.Start, unitAnn.End)
	}
	if corrected[unitAnn.Start:unitAnn.End] != unitAnn.Text {
		t.Errorf("unit annotation text %q does not match corrected[%d:%d] = %q",
			unitAnn.Text, unitAnn.Start, unitAnn.End, corrected[unitAnn.Start:unitAnn.End])
	}

	chanAnn := annotations[1]
	if chanAnn.Type != "channel" || chanAnn.Dispatch != "CITY" || chanAnn.Channel != "3" {
		t.Errorf("annotation[1] = %+v, want channel CITY/FIRE/3", chanAnn)
	}
	if corrected[chanAnn.Start:chanAnn.End] != chanAnn.Text {
		t.Errorf("channel annotation text %q does not match corrected[%d:%d] = %q",
			chanAnn.Text, chanAnn.Start, chanAnn.End, corrected[chanAnn.Start:chanAnn.End])
	}
}

func TestAnnotateTranscriptFuzzy(t *testing.T) {
	// "ENGNE" is fuzzy ENGINE; "CITI" is fuzzy CITY
	_, annotations := testParser.AnnotateTranscript("ENGNE 5 ON CITI FIRE 3")
	if len(annotations) < 2 {
		t.Fatalf("expected at least 2 annotations, got %d: %+v", len(annotations), annotations)
	}

	var unitAnn, chanAnn *TranscriptAnnotation
	for i := range annotations {
		switch annotations[i].Type {
		case "unit":
			unitAnn = &annotations[i]
		case "channel":
			chanAnn = &annotations[i]
		}
	}

	if unitAnn == nil {
		t.Fatal("no unit annotation found")
	}
	if !unitAnn.Fuzzy || unitAnn.Apparatus != "ENGINE" {
		t.Errorf("unit annotation = %+v, want fuzzy ENGINE", unitAnn)
	}

	if chanAnn == nil {
		t.Fatal("no channel annotation found")
	}
	if !chanAnn.Fuzzy || chanAnn.Dispatch != "CITY" {
		t.Errorf("channel annotation = %+v, want fuzzy CITY", chanAnn)
	}
}

func TestAnnotateTranscriptCorrections(t *testing.T) {
	// "A KNOWN MEDICAL" should be corrected to "UNKNOWN MEDICAL" before parsing
	p := NewTranscriptParser(TranscriptConfig{
		Corrections: []FuzzyWord{
			{Word: "UNKNOWN MEDICAL", MaxDistance: 1, Aliases: []string{"A KNOWN MEDICAL"}},
		},
		UnitTypes: []FuzzyWord{
			{Word: "ENGINE", MaxDistance: 1},
		},
	})

	corrected, _ := p.AnnotateTranscript("a known medical ENGINE 5")
	if !strings.Contains(corrected, "UNKNOWN MEDICAL") {
		t.Errorf("expected correction in output, got %q", corrected)
	}
	if strings.Contains(corrected, "A KNOWN MEDICAL") {
		t.Errorf("expected original mis-transcription to be corrected, got %q", corrected)
	}

	// Annotation offsets must reference the corrected string, not the original
	_, annotations := p.AnnotateTranscript("a known medical ENGINE 5")
	for _, ann := range annotations {
		if ann.Type == "unit" {
			if corrected[ann.Start:ann.End] != ann.Text {
				t.Errorf("annotation offset mismatch: corrected[%d:%d]=%q but Text=%q",
					ann.Start, ann.End, corrected[ann.Start:ann.End], ann.Text)
			}
		}
	}
}

func TestAnnotateTranscriptEmpty(t *testing.T) {
	corrected, annotations := testParser.AnnotateTranscript("")
	if corrected != "" {
		t.Errorf("expected empty corrected string, got %q", corrected)
	}
	if annotations != nil {
		t.Errorf("expected nil annotations for empty input, got %v", annotations)
	}
}

func TestAnnotateTranscriptNoParser(t *testing.T) {
	var p *TranscriptParser
	corrected, annotations := p.AnnotateTranscript("ENGINE 5 RESPONDING")
	if corrected != "ENGINE 5 RESPONDING" {
		t.Errorf("nil parser should pass transcript through, got %q", corrected)
	}
	if annotations != nil {
		t.Errorf("nil parser should return nil annotations, got %v", annotations)
	}
}

func TestAnnotateTranscriptSortedByStart(t *testing.T) {
	// Two units: ENGINE 5 at start, LADDER 3 later — annotations must be in order
	corrected, annotations := testParser.AnnotateTranscript("ENGINE 5 AND LADDER 3")
	if len(annotations) != 2 {
		t.Fatalf("expected 2 annotations, got %d: %+v", len(annotations), annotations)
	}
	if annotations[0].Start > annotations[1].Start {
		t.Errorf("annotations not sorted: [0].Start=%d [1].Start=%d", annotations[0].Start, annotations[1].Start)
	}
	// Verify offsets are correct
	for i, ann := range annotations {
		if corrected[ann.Start:ann.End] != ann.Text {
			t.Errorf("annotation[%d] offset mismatch: corrected[%d:%d]=%q Text=%q",
				i, ann.Start, ann.End, corrected[ann.Start:ann.End], ann.Text)
		}
	}
}

func TestAnnotateTranscriptNoMatches(t *testing.T) {
	// A transcript with no recognizable units or channels returns nil annotations
	corrected, annotations := testParser.AnnotateTranscript("RESPOND TO THE SCENE IMMEDIATELY")
	if corrected == "" {
		t.Error("corrected string should not be empty")
	}
	if annotations != nil {
		t.Errorf("expected nil annotations when nothing recognized, got %v", annotations)
	}
}
