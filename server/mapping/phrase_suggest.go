// Copyright (C) 2025 Thinline Dynamic Solutions

package mapping

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	// Radio / CAD style codes: 10-50, 10-50 PI, 10-50PD, 11-80, etc.
	radioCodePhraseRE = regexp.MustCompile(`(?i)\b\d{1,2}-\d{1,3}(?:\s*[A-Z]{1,4})?\b`)
	// Compact ten-codes without hyphen (after transcript normalize): 1050, 1080.
	tenCodeCompactRE = regexp.MustCompile(`\b10\d{2}(?:\s+[A-Z]{1,4})?\b`)
)

// SuggestNewPhrasesForNature finds short local jargon phrases in a transcript
// that are not already known match terms, for admin review under the given
// canonical call-nature label. Used by live phrase learning and history scan.
//
// Unlike DiscoverLearnedPhrasesForLabel (which mostly re-discovers existing
// catalog terms), this extracts radio codes — e.g. "10-50 PI" on an MVA call.
func SuggestNewPhrasesForNature(transcript, label string, knownTerms map[string]bool) []string {
	label = strings.ToUpper(strings.TrimSpace(label))
	if label == "" || IsDefaultUnknownNatureLabel(label) {
		return nil
	}
	rawUpper := strings.ToUpper(strings.TrimSpace(transcript))
	cleaned := PreCleanTranscript(transcript)
	if rawUpper == "" && cleaned == "" {
		return nil
	}
	seen := map[string]bool{label: true}
	var out []string
	add := func(p string) {
		p = strings.ToUpper(strings.TrimSpace(p))
		p = strings.Join(strings.Fields(p), " ")
		if p == "" || seen[p] || knownTerms[p] {
			return
		}
		if !IsAcceptableCallNaturePhrase(p) && !isAcceptableRadioCodePhrase(p) {
			return
		}
		seen[p] = true
		out = append(out, p)
	}

	// Prefer hyphenated codes from the raw transcript (PreClean may strip hyphens).
	for _, src := range []string{rawUpper, strings.ToUpper(cleaned)} {
		for _, m := range radioCodePhraseRE.FindAllString(src, -1) {
			add(normalizeRadioCodePhrase(m))
		}
		for _, m := range tenCodeCompactRE.FindAllString(src, -1) {
			add(strings.Join(strings.Fields(m), " "))
		}
	}

	// Also keep DiscoverLearnedPhrases hits that are still new.
	if cleaned != "" {
		for _, p := range DiscoverLearnedPhrasesForLabel(cleaned, label, []string{label}, nil, map[string]string{label: label}) {
			add(p)
		}
	}

	return out
}

func normalizeRadioCodePhrase(p string) string {
	p = strings.ToUpper(strings.TrimSpace(p))
	p = strings.Join(strings.Fields(p), " ")
	// "10-50PI" -> "10-50 PI"
	if i := strings.Index(p, "-"); i >= 0 && i+1 < len(p) {
		rest := p[i+1:]
		digitEnd := 0
		for digitEnd < len(rest) && unicode.IsDigit(rune(rest[digitEnd])) {
			digitEnd++
		}
		if digitEnd > 0 && digitEnd < len(rest) {
			suffix := strings.TrimSpace(rest[digitEnd:])
			if suffix != "" {
				p = p[:i+1] + rest[:digitEnd] + " " + suffix
			}
		}
	}
	return p
}

// isAcceptableRadioCodePhrase allows short radio/CAD codes that the general
// mining heuristic might reject for being too short (e.g. "10-50").
func isAcceptableRadioCodePhrase(phrase string) bool {
	phrase = strings.ToUpper(strings.TrimSpace(phrase))
	if phrase == "" || len(phrase) > maxLearnedPhraseLen {
		return false
	}
	if radioCodePhraseRE.MatchString(phrase) {
		return len(strings.Fields(phrase)) <= 3
	}
	if tenCodeCompactRE.MatchString(phrase) {
		return len(strings.Fields(phrase)) <= 3
	}
	return false
}
