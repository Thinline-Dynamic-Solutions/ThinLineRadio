// Copyright (C) 2025 Thinline Dynamic Solutions
//
// Rules that block a transcript-review approval from being sent to the
// transcript collector. The client mirrors these checks for live feedback.

package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"rdio-scanner/server/mapping"
)

const trainingMaxAudioSeconds = 30.0

var (
	reTrainingStreetType = regexp.MustCompile(`\b(AVE|BLVD|RD|LN)\b`)
	reTrainingStDr       = regexp.MustCompile(`\b(\d{1,6}\s+)((?:[A-Z']+\s+){1,3}?)(ST|DR)\b`)
	reTrainingRoute      = regexp.MustCompile(`\b(RTE|RT)\s+\d`)
	reTrainingDirAfter   = regexp.MustCompile(`\b(?:STREET|AVENUE|ROAD|DRIVE|LANE|BOULEVARD|AVE|BLVD|RD|LN|ST|DR)\s+(NE|NW|SE|SW)\b`)
	reTrainingDirNumber  = regexp.MustCompile(`(\d{1,6})\s+(NE|NW|SE|SW|N|E|W|S)\s+(\d+(?:ST|ND|RD|TH)|[A-Z]{3,})\b`)
	reTrainingOrdinal    = regexp.MustCompile(`^\d+(?:ST|ND|RD|TH)$`)
)

var trainingStreetFull = map[string]string{
	"AVE": "avenue", "BLVD": "boulevard", "RD": "road", "LN": "lane", "ST": "street", "DR": "drive",
	"RT": "route", "RTE": "route",
	"N": "north", "S": "south", "E": "east", "W": "west",
	"NE": "northeast", "NW": "northwest", "SE": "southeast", "SW": "southwest",
}

// Words that sit in front of "st"/"dr" when it is not a street type.
var trainingNotStreetPrev = map[string]bool{
	"TO": true, "AT": true, "IN": true, "FROM": true, "OF": true, "BY": true,
	"THE": true, "AND": true, "FOR": true, "WITH": true, "A": true, "AN": true,
	"IS": true, "ON": true, "MARY": true, "LUKE": true, "JAMES": true,
}

// A direction after one of these is a highway, not a street direction.
var trainingHwyPrev = map[string]bool{
	"I": true, "IH": true, "US": true, "SR": true, "ROUTE": true, "RT": true, "RTE": true,
	"HWY": true, "HIGHWAY": true, "HW": true, "STATE": true, "COUNTY": true, "CR": true,
	"FM": true, "MILE": true,
}

var trainingStreetWords = map[string]bool{
	"STREET": true, "AVENUE": true, "ROAD": true, "DRIVE": true, "LANE": true, "BOULEVARD": true,
	"ST": true, "AVE": true, "RD": true, "BLVD": true, "LN": true, "DR": true,
}

// Cardinals only. "oh" stays, because reviewers use it for the letter O.
var trainingNumberWords = map[string]bool{
	"ZERO": true, "ONE": true, "TWO": true, "THREE": true, "FOUR": true, "FIVE": true,
	"SIX": true, "SEVEN": true, "EIGHT": true, "NINE": true, "TEN": true, "ELEVEN": true,
	"TWELVE": true, "THIRTEEN": true, "FOURTEEN": true, "FIFTEEN": true, "SIXTEEN": true,
	"SEVENTEEN": true, "EIGHTEEN": true, "NINETEEN": true, "TWENTY": true, "THIRTY": true,
	"FORTY": true, "FIFTY": true, "SIXTY": true, "SEVENTY": true, "EIGHTY": true,
	"NINETY": true, "HUNDRED": true, "THOUSAND": true,
}

// A transcript made only of these words is a label, not speech.
var trainingNonSpeechWords = map[string]bool{
	"NO": true, "NOT": true, "VOICE": true, "VOICES": true, "AUDIO": true, "SPEECH": true,
	"WORD": true, "WORDS": true, "SPOKEN": true, "TONE": true, "TONES": true, "ONLY": true,
	"JUST": true, "SILENCE": true, "SILENT": true, "STATIC": true, "SOUND": true, "SOUNDS": true,
	"NONE": true, "NOTHING": true, "EMPTY": true, "BEEP": true, "BEEPS": true, "OR": true, "AND": true,
}

var trainingNonSpeechMarkers = map[string]bool{
	"VOICE": true, "VOICES": true, "AUDIO": true, "SPEECH": true, "WORD": true, "WORDS": true,
	"TONE": true, "TONES": true, "SILENCE": true, "SILENT": true, "STATIC": true,
	"NONE": true, "NOTHING": true, "EMPTY": true, "BEEP": true, "BEEPS": true,
}

type trainingAbbrevHit struct {
	pos  int
	from string
	to   string
}

// trainingSubmitIssues returns reviewer-facing reasons a clip must not be sent.
// durationKnown is false when the audio length could not be measured.
func trainingSubmitIssues(transcript string, durationSeconds float64, durationKnown bool) []string {
	var issues []string

	switch {
	case !durationKnown || durationSeconds <= 0:
		issues = append(issues, "The audio length could not be measured, so this clip cannot be sent for training.")
	case durationSeconds > trainingMaxAudioSeconds:
		issues = append(issues, fmt.Sprintf(
			"This clip is %s seconds long. Only clips of 30 seconds or less can be sent for training.",
			formatTrainingSeconds(durationSeconds),
		))
	}

	normalized := mapping.NormalizeTranscriptPlainText(transcript)
	if normalized == "" {
		return issues
	}
	if isTrainingNonSpeechLabel(normalized) {
		issues = append(issues, fmt.Sprintf(
			"This transcript is a label (%q), not spoken words. Clips with no voice or only tones cannot be sent.",
			strings.ToLower(normalized),
		))
	}
	if abbrevs := trainingAbbreviationParts(normalized); len(abbrevs) > 0 {
		issues = append(issues, "Spell out abbreviations: "+strings.Join(abbrevs, ", ")+".")
	}
	if words := trainingNumberWordHits(normalized); len(words) > 0 {
		issues = append(issues, "Write numbers as digits, not words: "+strings.Join(words, ", ")+".")
	}
	return issues
}

func formatTrainingSeconds(seconds float64) string {
	s := fmt.Sprintf("%.1f", seconds)
	return strings.TrimSuffix(s, ".0")
}

func isTrainingNonSpeechLabel(normalized string) bool {
	fields := strings.Fields(normalized)
	if len(fields) == 0 {
		return false
	}
	marker := false
	for _, w := range fields {
		if !trainingNonSpeechWords[w] {
			return false
		}
		if trainingNonSpeechMarkers[w] {
			marker = true
		}
	}
	return marker
}

func trainingAbbreviationParts(normalized string) []string {
	var hits []trainingAbbrevHit

	for _, m := range reTrainingStreetType.FindAllStringSubmatchIndex(normalized, -1) {
		word := normalized[m[2]:m[3]]
		hits = append(hits, trainingAbbrevHit{m[2], strings.ToLower(word), trainingStreetFull[word]})
	}
	for _, m := range reTrainingStDr.FindAllStringSubmatchIndex(normalized, -1) {
		words := strings.Fields(normalized[m[4]:m[5]])
		if len(words) == 0 || trainingNotStreetPrev[words[len(words)-1]] {
			continue
		}
		suffix := normalized[m[6]:m[7]]
		hits = append(hits, trainingAbbrevHit{m[6], strings.ToLower(suffix), trainingStreetFull[suffix]})
	}
	for _, m := range reTrainingRoute.FindAllStringSubmatchIndex(normalized, -1) {
		word := normalized[m[2]:m[3]]
		hits = append(hits, trainingAbbrevHit{m[2], strings.ToLower(word), "route"})
	}
	for _, m := range reTrainingDirAfter.FindAllStringSubmatchIndex(normalized, -1) {
		word := normalized[m[2]:m[3]]
		hits = append(hits, trainingAbbrevHit{m[2], strings.ToLower(word), trainingStreetFull[word]})
	}
	for _, m := range reTrainingDirNumber.FindAllStringSubmatchIndex(normalized, -1) {
		dir := normalized[m[4]:m[5]]
		next := normalized[m[6]:m[7]]
		if trainingHwyPrev[wordBefore(normalized, m[2])] {
			continue
		}
		if trainingStreetWords[next] {
			continue
		}
		if dir == "S" && !reTrainingOrdinal.MatchString(next) {
			continue
		}
		hits = append(hits, trainingAbbrevHit{m[4], strings.ToLower(dir), trainingStreetFull[dir]})
	}

	sort.SliceStable(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	seen := map[string]bool{}
	parts := make([]string, 0, len(hits))
	for _, h := range hits {
		if h.to == "" || seen[h.from] {
			continue
		}
		seen[h.from] = true
		parts = append(parts, h.from+" → "+h.to)
	}
	return parts
}

func wordBefore(s string, idx int) string {
	if idx < 0 || idx > len(s) {
		return ""
	}
	prev := strings.TrimRight(s[:idx], " ")
	if prev == "" {
		return ""
	}
	if i := strings.LastIndex(prev, " "); i >= 0 {
		return prev[i+1:]
	}
	return prev
}

func trainingNumberWordHits(normalized string) []string {
	seen := map[string]bool{}
	var words []string
	for _, tok := range strings.Fields(normalized) {
		if !trainingNumberWords[tok] {
			continue
		}
		low := strings.ToLower(tok)
		if seen[low] {
			continue
		}
		seen[low] = true
		words = append(words, low)
	}
	return words
}
