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
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
)

//! Important Note: This parser is optimized for Fire Department dispatches

// FuzzyWord represents a word to match with optional fuzzy tolerance.
// Fields are exported so configs can be JSON-serialized for database / web UI storage.
type FuzzyWord struct {
	Word        string   `json:"word"`
	MaxDistance uint8    `json:"maxDistance"`
	Aliases     []string `json:"aliases,omitempty"`
	// Reject lists preceding words that invalidate a match.
	// ex: match "RESCUE" but not when preceded by "ELEVATOR" so it doesn't
	// match "RESCUE 123" in "RESPOND TO ELEVATOR RESCUE, 123 MAIN STREET"
	Reject []string `json:"reject,omitempty"`
}

// ChannelShorthand maps an abbreviated label to its canonical dispatch name and
// optional separator. Each entry generates regexp variants for space, dash, and
// no separator (e.g. "SF 3", "SF-3", "SF3").
type ChannelShorthand struct {
	Label     string `json:"label"`
	Dispatch  string `json:"dispatch"`
	Separator string `json:"separator,omitempty"` // e.g. "FIRE", "OPS"; empty if none
}

// TranscriptConfig holds all configurable word lists for the transcript parser.
// Store this as JSON in the database and expose it through the admin UI.
type TranscriptConfig struct {
	UnitTypes     []FuzzyWord `json:"unitTypes"`
	UnitPrefixes  []FuzzyWord `json:"unitPrefixes"`
	DispatchNames []FuzzyWord `json:"dispatchNames"`
	// ChannelSeparators is the word between a dispatch name and channel number
	// (e.g. "FIRE", "POLICE", "EMS", "OPS"). When empty the parser matches
	// {Dispatch} {Number} directly with no separator required.
	ChannelSeparators []FuzzyWord        `json:"channelSeparators,omitempty"`
	ChannelShorthands []ChannelShorthand `json:"channelShorthands"`
	Corrections       []FuzzyWord        `json:"corrections"`
}

// ParsedUnit is the result of recognizing an apparatus unit in a transcript.
type ParsedUnit struct {
	Prefix    string   `json:"prefix"`
	Apparatus string   `json:"apparatus"`
	Number    string   `json:"number"`
	Raw       []string `json:"raw"`
	Fuzzy     bool     `json:"fuzzy"`
}

// ParsedChannel is the result of recognizing a dispatch channel in a transcript.
// Example Channels: "City Fire 2" "VECC Fire 5" "OPS 2"
type ParsedChannel struct {
	Dispatch  string   `json:"dispatch"`
	Separator string   `json:"separator"` // the word between dispatch and number, e.g. "FIRE"
	Channel   string   `json:"channel"`
	Raw       []string `json:"raw"`
	Fuzzy     bool     `json:"fuzzy"`
}

// TranscriptAnnotation describes a recognized unit or channel at a specific
// byte range within the corrected transcript string returned by AnnotateTranscript.
type TranscriptAnnotation struct {
	Type      string `json:"type"`                // "unit" or "channel"
	Text      string `json:"text"`                // raw text as found in transcript
	Start     int    `json:"start"`               // byte offset in corrected transcript (inclusive)
	End       int    `json:"end"`                 // byte offset in corrected transcript (exclusive)
	Prefix    string `json:"prefix,omitempty"`    // unit prefix, e.g. "MEDIC"
	Apparatus string `json:"apparatus,omitempty"` // unit type, e.g. "ENGINE"
	Number    string `json:"number,omitempty"`    // unit or channel number
	Dispatch  string `json:"dispatch,omitempty"`  // channel dispatch name
	Separator string `json:"separator,omitempty"` // channel separator word
	Channel   string `json:"channel,omitempty"`   // channel number
	Fuzzy     bool   `json:"fuzzy"`               // true if fuzzy-matched
}

// activeTranscriptParser is the live parser instance, updated whenever the
// admin saves a new TranscriptParserConfig. Package-level so MarshalJSON
// methods can access it without holding a controller reference.
var activeTranscriptParser atomic.Pointer[TranscriptParser]

// Candidate represents a possible fuzzy or exact match at a token position.
type Candidate struct {
	match    string
	distance uint8
	startIdx int
	endIdx   int // inclusive
}

type token struct {
	text  string
	index int
}

type compiledShorthand struct {
	pattern   *regexp.Regexp
	dispatch  string
	separator string
}

// TranscriptParser holds compiled patterns derived from a TranscriptConfig.
// Build it once with NewTranscriptParser and rebuild whenever the config changes.
type TranscriptParser struct {
	cfg               TranscriptConfig
	unitPattern       *regexp.Regexp
	unitHasPrefix     bool // true when unitPattern includes the optional prefix capture group
	channelPattern    *regexp.Regexp
	channelHasSep     bool // true when channelPattern includes a separator capture group
	channelShorthands []compiledShorthand
}

var tokenPattern = regexp.MustCompile(`[A-Z]+|\d+`)

// NewTranscriptParser compiles all regex patterns from cfg and returns a
// ready-to-use parser. Call again whenever the config changes.
func NewTranscriptParser(cfg TranscriptConfig) *TranscriptParser {
	p := &TranscriptParser{cfg: cfg}

	if len(cfg.UnitTypes) > 0 {
		types := make([]string, len(cfg.UnitTypes))
		for i, uw := range cfg.UnitTypes {
			types[i] = regexp.QuoteMeta(uw.Word)
		}

		if len(cfg.UnitPrefixes) > 0 {
			prefixes := make([]string, len(cfg.UnitPrefixes))
			for i, pw := range cfg.UnitPrefixes {
				prefixes[i] = regexp.QuoteMeta(pw.Word)
			}
			p.unitPattern = regexp.MustCompile(
				`\b(?:(` + strings.Join(prefixes, "|") + `)\s+)?` +
					`(` + strings.Join(types, "|") + `)` +
					`\s+(\d{1,3})\b`,
			)
			p.unitHasPrefix = true
		} else {
			p.unitPattern = regexp.MustCompile(
				`\b(` + strings.Join(types, "|") + `)` +
					`\s+(\d{1,3})\b`,
			)
		}
	}

	if len(cfg.DispatchNames) > 0 {
		dispatches := make([]string, len(cfg.DispatchNames))
		for i, d := range cfg.DispatchNames {
			dispatches[i] = regexp.QuoteMeta(d.Word)
		}

		if len(cfg.ChannelSeparators) > 0 {
			seps := make([]string, len(cfg.ChannelSeparators))
			for i, s := range cfg.ChannelSeparators {
				seps[i] = regexp.QuoteMeta(s.Word)
			}
			// Groups: (dispatch) (separator) (number)
			p.channelPattern = regexp.MustCompile(
				`\b(` + strings.Join(dispatches, "|") + `)` +
					`\s+(` + strings.Join(seps, "|") + `)` +
					`\s+(\d{1,2})\b`,
			)
			p.channelHasSep = true
		} else {
			// No separator configured — match {Dispatch} {Number} directly.
			// Groups: (dispatch) (number)
			p.channelPattern = regexp.MustCompile(
				`\b(` + strings.Join(dispatches, "|") + `)` +
					`\s+(\d{1,2})\b`,
			)
		}
	}

	for _, def := range cfg.ChannelShorthands {
		escaped := regexp.QuoteMeta(def.Label)
		p.channelShorthands = append(p.channelShorthands,
			compiledShorthand{pattern: regexp.MustCompile(`\b` + escaped + `\s+(\d{1,2})\b`), dispatch: def.Dispatch, separator: def.Separator},
			compiledShorthand{pattern: regexp.MustCompile(`\b` + escaped + `-(\d{1,2})\b`), dispatch: def.Dispatch, separator: def.Separator},
			compiledShorthand{pattern: regexp.MustCompile(`\b` + escaped + `(\d{1,2})\b`), dispatch: def.Dispatch, separator: def.Separator},
		)
	}

	return p
}

// ParseTranscript extracts all units and channels from transcript.
func (p *TranscriptParser) ParseTranscript(transcript string) ([]ParsedUnit, []ParsedChannel) {
	return p.ParseUnits(transcript), p.ParseChannels(transcript)
}

// levenshtein computes the edit distance between two ASCII strings.
func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	if a == b {
		return 0
	}

	// Ensure a is the shorter string for O(min(m,n)) space.
	if len(a) > len(b) {
		a, b = b, a
	}

	prev := make([]int, len(a)+1)
	curr := make([]int, len(a)+1)

	for i := range prev {
		prev[i] = i
	}

	for j := 1; j <= len(b); j++ {
		curr[0] = j
		for i := 1; i <= len(a); i++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[i] + 1
			ins := curr[i-1] + 1
			sub := prev[i-1] + cost
			curr[i] = min(del, min(ins, sub))
		}
		prev, curr = curr, prev
	}

	return prev[len(a)]
}

func tokenize(s string) []token {
	indices := tokenPattern.FindAllStringIndex(s, -1)
	tokens := make([]token, len(indices))
	for i, idx := range indices {
		tokens[i] = token{text: s[idx[0]:idx[1]], index: idx[0]}
	}
	return tokens
}

func isDigitToken(s string, maxLen int) bool {
	if len(s) == 0 || len(s) > maxLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

func stripSpaces(s string) string {
	return strings.ReplaceAll(s, " ", "")
}

func findCandidates(tokens []token, wordList []FuzzyWord, skipIndices map[int]bool, maxMerge int) []Candidate {
	var candidates []Candidate

	for i := 0; i < len(tokens); i++ {
		if skipIndices[i] {
			continue
		}
		if isDigits(tokens[i].text) {
			continue
		}

		for m := 0; m < maxMerge && i+m < len(tokens); m++ {
			endIdx := i + m
			if isDigits(tokens[endIdx].text) {
				break
			}
			if m > 0 && skipIndices[endIdx] {
				break
			}

			// Merge token texts. Spaces stripped so multi-word entries like
			// "MEDIC ENGINE" match merged tokens "MEDICENGINE".
			var merged string
			if m == 0 {
				merged = tokens[i].text
			} else {
				var sb strings.Builder
				for k := i; k <= endIdx; k++ {
					sb.WriteString(tokens[k].text)
				}
				merged = sb.String()
			}

			// Exact match
			found := false
			for _, w := range wordList {
				if stripSpaces(w.Word) == merged {
					candidates = append(candidates, Candidate{match: w.Word, distance: 0, startIdx: i, endIdx: endIdx})
					found = true
					break
				}
			}
			if found {
				break
			}

			// Alias match
			for _, w := range wordList {
				for _, alias := range w.Aliases {
					if stripSpaces(alias) == merged {
						candidates = append(candidates, Candidate{match: w.Word, distance: 0, startIdx: i, endIdx: endIdx})
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if found {
				break
			}

			// Fuzzy match
			bestDist := -1
			var bestMatch string
			for _, w := range wordList {
				if w.MaxDistance == 0 {
					continue
				}
				dist := levenshtein(merged, stripSpaces(w.Word))
				if dist > 0 && dist <= int(w.MaxDistance) {
					if bestDist == -1 || dist < bestDist {
						bestMatch = w.Word
						bestDist = dist
					}
				}
			}
			if bestDist >= 0 {
				candidates = append(candidates, Candidate{match: bestMatch, distance: uint8(bestDist), startIdx: i, endIdx: endIdx})
				break
			}
		}
	}

	return candidates
}

func overlaps(a, b Candidate) bool {
	return a.startIdx <= b.endIdx && b.startIdx <= a.endIdx
}

func rejectListFor(wordList []FuzzyWord, word string) []string {
	for _, w := range wordList {
		if w.Word == word {
			return w.Reject
		}
	}
	return nil
}

// precedesToken checks if any reject word appears as the token immediately
// before startIdx in the token list.
func precedesToken(tokens []token, startIdx int, rejectWords []string) bool {
	if startIdx <= 0 || len(rejectWords) == 0 {
		return false
	}
	prev := tokens[startIdx-1].text
	for _, r := range rejectWords {
		if prev == r {
			return true
		}
	}
	return false
}

// precedesByte checks if any reject word appears immediately before bytePos
// in the normalized string.
func precedesByte(normalized string, bytePos int, rejectWords []string) bool {
	if bytePos <= 0 || len(rejectWords) == 0 {
		return false
	}
	before := strings.TrimRight(normalized[:bytePos], " ,.")
	for _, r := range rejectWords {
		if strings.HasSuffix(before, r) {
			return true
		}
	}
	return false
}

func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// matchSeparator checks tokenText against the configured separator list
// (exact -> alias -> fuzzy) and returns the canonical separator word.
func matchSeparator(tokenText string, separators []FuzzyWord) (string, bool) {
	for _, s := range separators {
		if tokenText == s.Word {
			return s.Word, true
		}
		for _, alias := range s.Aliases {
			if tokenText == alias {
				return s.Word, true
			}
		}
	}
	for _, s := range separators {
		if s.MaxDistance == 0 {
			continue
		}
		if levenshtein(tokenText, s.Word) <= int(s.MaxDistance) {
			return s.Word, true
		}
	}
	return "", false
}

// channelKey builds the deduplication key for a ParsedChannel.
func channelKey(dispatch, separator, channel string) string {
	if separator == "" {
		return dispatch + " " + channel
	}
	return dispatch + " " + separator + " " + channel
}

// CorrectTranscript replaces common mis-transcriptions with their correct
// forms using the configured fuzzy corrections list.
func (p *TranscriptParser) CorrectTranscript(transcript string) string {
	if transcript == "" || len(p.cfg.Corrections) == 0 {
		return transcript
	}

	normalized := strings.ToUpper(transcript)
	tokens := tokenize(normalized)
	if len(tokens) == 0 {
		return transcript
	}

	candidates := findCandidates(tokens, p.cfg.Corrections, make(map[int]bool), 3)
	if len(candidates) == 0 {
		return transcript
	}

	// Sort by distance (best first), position as tiebreaker.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].distance != candidates[j].distance {
			return candidates[i].distance < candidates[j].distance
		}
		return candidates[i].startIdx < candidates[j].startIdx
	})

	// Greedily select non-overlapping candidates.
	var selected []Candidate
	for _, c := range candidates {
		conflict := false
		for _, s := range selected {
			if overlaps(c, s) {
				conflict = true
				break
			}
		}
		if !conflict {
			selected = append(selected, c)
		}
	}

	// Re-sort by position for right-to-left replacement.
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].startIdx < selected[j].startIdx
	})

	result := normalized
	for i := len(selected) - 1; i >= 0; i-- {
		c := selected[i]
		byteStart := tokens[c.startIdx].index
		endTok := tokens[c.endIdx]
		byteEnd := endTok.index + len(endTok.text)
		result = result[:byteStart] + c.match + result[byteEnd:]
	}

	return result
}

// ParseUnits extracts apparatus units (e.g. "ENGINE 5", "MEDIC LADDER 12") from
// transcript using the configured unit types and prefixes.
func (p *TranscriptParser) ParseUnits(transcript string) []ParsedUnit {
	if transcript == "" || p.unitPattern == nil {
		return []ParsedUnit{}
	}

	normalized := strings.ToUpper(transcript)
	seen := make(map[string]int) // key -> index in results
	var results []ParsedUnit

	// Track character ranges consumed by exact matches.
	var consumed [][2]int

	// --- Pass 1: exact regex ---
	matches := p.unitPattern.FindAllStringSubmatchIndex(normalized, -1)
	for _, m := range matches {
		fullMatch := normalized[m[0]:m[1]]

		var prefix, apparatus, number string
		var checkPos int

		if p.unitHasPrefix {
			// Groups: (prefix)? (apparatus) (number)
			if m[2] >= 0 {
				prefix = normalized[m[2]:m[3]]
			}
			apparatus = normalized[m[4]:m[5]]
			number = normalized[m[6]:m[7]]
			checkPos = m[4]
			if prefix != "" {
				checkPos = m[2]
			}
		} else {
			// Groups: (apparatus) (number)
			apparatus = normalized[m[2]:m[3]]
			number = normalized[m[4]:m[5]]
			checkPos = m[2]
		}

		if rejectWords := rejectListFor(p.cfg.UnitTypes, apparatus); precedesByte(normalized, checkPos, rejectWords) {
			continue
		}

		key := strings.TrimSpace(prefix + " " + apparatus + " " + number)

		if idx, ok := seen[key]; ok {
			if !containsString(results[idx].Raw, fullMatch) {
				results[idx].Raw = append(results[idx].Raw, fullMatch)
			}
		} else {
			seen[key] = len(results)
			results = append(results, ParsedUnit{
				Prefix:    prefix,
				Apparatus: apparatus,
				Number:    number,
				Raw:       []string{fullMatch},
				Fuzzy:     false,
			})
		}
		consumed = append(consumed, [2]int{m[0], m[1]})
	}

	// --- Pass 2: fuzzy label + assemble ---
	tokens := tokenize(normalized)

	exactConsumed := make(map[int]bool)
	for i, tk := range tokens {
		for _, c := range consumed {
			if tk.index >= c[0] && tk.index < c[1] {
				exactConsumed[i] = true
				break
			}
		}
	}

	prefixCandidates := findCandidates(tokens, p.cfg.UnitPrefixes, exactConsumed, 3)
	unitCandidates := findCandidates(tokens, p.cfg.UnitTypes, exactConsumed, 3)

	assemblyConsumed := make(map[int]bool)

	for _, unit := range unitCandidates {
		skip := false
		for idx := unit.startIdx; idx <= unit.endIdx; idx++ {
			if assemblyConsumed[idx] {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		if rejectWords := rejectListFor(p.cfg.UnitTypes, unit.match); precedesToken(tokens, unit.startIdx, rejectWords) {
			continue
		}

		// The token immediately after the unit must be a 1-3 digit number.
		numIdx := unit.endIdx + 1
		if numIdx >= len(tokens) {
			continue
		}
		numToken := tokens[numIdx]
		if !isDigitToken(numToken.text, 3) {
			continue
		}
		if assemblyConsumed[numIdx] {
			continue
		}

		number := numToken.text

		// Look for the best prefix candidate ending right before this unit.
		var bestPrefix *Candidate
		for ci := range prefixCandidates {
			pc := &prefixCandidates[ci]
			if pc.endIdx != unit.startIdx-1 {
				continue
			}
			if overlaps(*pc, unit) {
				continue
			}

			prefixUsed := false
			for idx := pc.startIdx; idx <= pc.endIdx; idx++ {
				if assemblyConsumed[idx] {
					prefixUsed = true
					break
				}
			}
			if prefixUsed {
				continue
			}

			if bestPrefix == nil || pc.distance < bestPrefix.distance {
				bestPrefix = pc
			}
		}

		var prefix string
		if bestPrefix != nil {
			prefix = bestPrefix.match
		}
		apparatus := unit.match
		key := strings.TrimSpace(prefix + " " + apparatus + " " + number)

		rawStart := tokens[unit.startIdx].index
		if bestPrefix != nil {
			rawStart = tokens[bestPrefix.startIdx].index
		}
		rawEnd := numToken.index + len(numToken.text)
		raw := normalized[rawStart:rawEnd]

		if idx, ok := seen[key]; ok {
			if !containsString(results[idx].Raw, raw) {
				results[idx].Raw = append(results[idx].Raw, raw)
			}
		} else {
			seen[key] = len(results)
			results = append(results, ParsedUnit{
				Prefix:    prefix,
				Apparatus: apparatus,
				Number:    number,
				Raw:       []string{raw},
				Fuzzy:     true,
			})
		}

		if bestPrefix != nil {
			for idx := bestPrefix.startIdx; idx <= bestPrefix.endIdx; idx++ {
				assemblyConsumed[idx] = true
			}
		}
		for idx := unit.startIdx; idx <= unit.endIdx; idx++ {
			assemblyConsumed[idx] = true
		}
		assemblyConsumed[numIdx] = true
	}

	// --- Pass 3: prefix upgrade for exact matches with no prefix ---
	for ri := range results {
		result := &results[ri]
		if result.Prefix != "" || result.Fuzzy {
			continue
		}

		matchCharStart := strings.Index(normalized, result.Raw[0])
		if matchCharStart == -1 {
			continue
		}

		unitTokenIdx := -1
		for i, tk := range tokens {
			if tk.index == matchCharStart {
				unitTokenIdx = i
				break
			}
		}
		if unitTokenIdx <= 0 {
			continue
		}

		var bestPrefix *Candidate
		for ci := range prefixCandidates {
			pc := &prefixCandidates[ci]
			if pc.endIdx != unitTokenIdx-1 {
				continue
			}

			prefixUsed := false
			for idx := pc.startIdx; idx <= pc.endIdx; idx++ {
				if assemblyConsumed[idx] {
					prefixUsed = true
					break
				}
			}
			if prefixUsed {
				continue
			}

			if bestPrefix == nil || pc.distance < bestPrefix.distance {
				bestPrefix = pc
			}
		}

		if bestPrefix == nil {
			continue
		}

		oldKey := strings.TrimSpace(result.Apparatus + " " + result.Number)
		newKey := bestPrefix.match + " " + result.Apparatus + " " + result.Number
		if _, exists := seen[newKey]; exists {
			continue
		}
		delete(seen, oldKey)
		seen[newKey] = ri

		result.Prefix = bestPrefix.match
		rawStart := tokens[bestPrefix.startIdx].index
		newRaw := make([]string, len(result.Raw))
		for i, r := range result.Raw {
			rStart := strings.Index(normalized, r)
			if rStart == -1 {
				newRaw[i] = r
			} else {
				newRaw[i] = normalized[rawStart : rStart+len(r)]
			}
		}
		result.Raw = newRaw

		for idx := bestPrefix.startIdx; idx <= bestPrefix.endIdx; idx++ {
			assemblyConsumed[idx] = true
		}
	}

	return results
}

// AnnotateTranscript applies corrections to transcript, parses units and channels,
// and returns the corrected string along with position-annotated results.
// The corrected string is always uppercase (normalized by CorrectTranscript).
// Start/End offsets reference byte positions in the returned corrected string.
// Returns ("", nil) for empty input. Safe to call on a nil parser.
func (p *TranscriptParser) AnnotateTranscript(transcript string) (string, []TranscriptAnnotation) {
	if p == nil || transcript == "" {
		return transcript, nil
	}

	corrected := p.CorrectTranscript(transcript)
	units := p.ParseUnits(corrected)
	channels := p.ParseChannels(corrected)

	if len(units) == 0 && len(channels) == 0 {
		return corrected, nil
	}

	var annotations []TranscriptAnnotation

	for _, unit := range units {
		for _, raw := range unit.Raw {
			offset := 0
			for {
				idx := strings.Index(corrected[offset:], raw)
				if idx == -1 {
					break
				}
				start := offset + idx
				end := start + len(raw)
				annotations = append(annotations, TranscriptAnnotation{
					Type:      "unit",
					Text:      raw,
					Start:     start,
					End:       end,
					Prefix:    unit.Prefix,
					Apparatus: unit.Apparatus,
					Number:    unit.Number,
					Fuzzy:     unit.Fuzzy,
				})
				offset = end
			}
		}
	}

	for _, ch := range channels {
		for _, raw := range ch.Raw {
			offset := 0
			for {
				idx := strings.Index(corrected[offset:], raw)
				if idx == -1 {
					break
				}
				start := offset + idx
				end := start + len(raw)
				annotations = append(annotations, TranscriptAnnotation{
					Type:      "channel",
					Text:      raw,
					Start:     start,
					End:       end,
					Dispatch:  ch.Dispatch,
					Separator: ch.Separator,
					Channel:   ch.Channel,
					Fuzzy:     ch.Fuzzy,
				})
				offset = end
			}
		}
	}

	if len(annotations) == 0 {
		return corrected, nil
	}

	sort.Slice(annotations, func(i, j int) bool {
		return annotations[i].Start < annotations[j].Start
	})

	// Substitute canonical forms into the corrected transcript string so that
	// the returned text reflects what was recognized (e.g. alias "DECK, FIRE 3"
	// becomes "VECC FIRE 3", fuzzy "ENGNE 5" becomes "ENGINE 5").
	// Process left-to-right with a cumulative delta so that each annotation's
	// Start/End is adjusted for all prior substitutions before use.
	delta := 0
	for i := range annotations {
		ann := &annotations[i]
		ann.Start += delta
		ann.End += delta

		var canonical string
		if ann.Type == "unit" {
			parts := make([]string, 0, 3)
			if ann.Prefix != "" {
				parts = append(parts, ann.Prefix)
			}
			parts = append(parts, ann.Apparatus, ann.Number)
			canonical = strings.Join(parts, " ")
		} else {
			parts := make([]string, 0, 3)
			parts = append(parts, ann.Dispatch)
			if ann.Separator != "" {
				parts = append(parts, ann.Separator)
			}
			parts = append(parts, ann.Channel)
			canonical = strings.Join(parts, " ")
		}
		if canonical == ann.Text {
			continue
		}
		origLen := ann.End - ann.Start
		corrected = corrected[:ann.Start] + canonical + corrected[ann.End:]
		ann.End = ann.Start + len(canonical)
		ann.Text = canonical
		delta += len(canonical) - origLen
	}

	// Convert byte offsets to Unicode code-point (rune) offsets.
	// Go strings are UTF-8 (byte-indexed); JavaScript strings are UTF-16
	// (character-indexed). They differ when the transcript contains multi-byte
	// characters such as an em-dash "—" (3 bytes in UTF-8, 1 char in JS).
	// Build a byte→rune index map so annotation positions are valid in JS.
	byteToRune := make([]int, len(corrected)+1)
	ri := 0
	for bi := range corrected {
		byteToRune[bi] = ri
		ri++
	}
	byteToRune[len(corrected)] = ri
	for i := range annotations {
		annotations[i].Start = byteToRune[annotations[i].Start]
		annotations[i].End = byteToRune[annotations[i].End]
	}

	return corrected, annotations
}

// ParseChannels extracts dispatch channels from transcript using the configured
// dispatch names, channel separators, and shorthands.
//
// The channel format depends on how ChannelSeparators is configured:
//   - With separators: "{Dispatch} {Separator} {Number}"  (e.g. "CITY FIRE 3", "METRO POLICE 2")
//   - Without separators: "{Dispatch} {Number}"           (e.g. "CITY 3")
//
// Shorthands are always matched regardless of the separator config.
func (p *TranscriptParser) ParseChannels(transcript string) []ParsedChannel {
	if transcript == "" || (p.channelPattern == nil && len(p.channelShorthands) == 0) {
		return []ParsedChannel{}
	}

	normalized := strings.ToUpper(transcript)
	seen := make(map[string]int)
	var results []ParsedChannel
	var consumed [][2]int

	addOrMerge := func(dispatch, separator, channel, raw string, fuzzy bool) {
		key := channelKey(dispatch, separator, channel)
		if idx, ok := seen[key]; ok {
			if !containsString(results[idx].Raw, raw) {
				results[idx].Raw = append(results[idx].Raw, raw)
			}
		} else {
			seen[key] = len(results)
			results = append(results, ParsedChannel{
				Dispatch:  dispatch,
				Separator: separator,
				Channel:   channel,
				Raw:       []string{raw},
				Fuzzy:     fuzzy,
			})
		}
	}

	// --- Pass 1: exact regex ---
	if p.channelPattern != nil {
		matches := p.channelPattern.FindAllStringSubmatchIndex(normalized, -1)
		for _, m := range matches {
			fullMatch := normalized[m[0]:m[1]]
			var dispatch, separator, channel string
			if p.channelHasSep {
				// Groups: (dispatch) (separator) (number)
				dispatch = normalized[m[2]:m[3]]
				separator = normalized[m[4]:m[5]]
				channel = normalized[m[6]:m[7]]
			} else {
				// Groups: (dispatch) (number)
				dispatch = normalized[m[2]:m[3]]
				channel = normalized[m[4]:m[5]]
			}
			addOrMerge(dispatch, separator, channel, fullMatch, false)
			consumed = append(consumed, [2]int{m[0], m[1]})
		}
	}

	// --- Pass 1b: shorthand patterns ---
	for _, sh := range p.channelShorthands {
		shMatches := sh.pattern.FindAllStringSubmatchIndex(normalized, -1)
		for _, m := range shMatches {
			fullMatch := normalized[m[0]:m[1]]
			channel := normalized[m[2]:m[3]]
			addOrMerge(sh.dispatch, sh.separator, channel, fullMatch, false)
			consumed = append(consumed, [2]int{m[0], m[1]})
		}
	}

	// --- Pass 2: fuzzy dispatch (+ optional separator) + number ---
	tokens := tokenize(normalized)

	exactConsumed := make(map[int]bool)
	for i, tk := range tokens {
		for _, c := range consumed {
			if tk.index >= c[0] && tk.index < c[1] {
				exactConsumed[i] = true
				break
			}
		}
	}

	dispatchCandidates := findCandidates(tokens, p.cfg.DispatchNames, exactConsumed, 2)

	for _, dc := range dispatchCandidates {
		nextIdx := dc.endIdx + 1
		if nextIdx >= len(tokens) {
			continue
		}

		var separator, channel string
		var numToken token

		if len(p.cfg.ChannelSeparators) > 0 {
			// Expect a separator token then a number.
			sep, ok := matchSeparator(tokens[nextIdx].text, p.cfg.ChannelSeparators)
			if !ok {
				continue
			}
			separator = sep

			numIdx := nextIdx + 1
			if numIdx >= len(tokens) {
				continue
			}
			numToken = tokens[numIdx]
			if !isDigitToken(numToken.text, 2) {
				continue
			}
			channel = numToken.text
		} else {
			// No separator — next token must be the number directly.
			if !isDigitToken(tokens[nextIdx].text, 2) {
				continue
			}
			numToken = tokens[nextIdx]
			channel = numToken.text
		}

		rawStart := tokens[dc.startIdx].index
		rawEnd := numToken.index + len(numToken.text)
		raw := normalized[rawStart:rawEnd]

		addOrMerge(dc.match, separator, channel, raw, true)
	}

	return results
}
