// Copyright (C) 2025 Thinline Dynamic Solutions
//
// address_transcript_align.go — reject extracted addresses whose street tokens
// do not appear in the source transcript. Universal pattern guards against
// regex/LLM fabrications like "SR 411 & AROUND" from dispatch chatter.

package mapping

import (
	"strconv"
	"strings"
)

// intersectionLeadNonStreets are prepositions/adverbs that STT often leaves
// before a real street name ("AROUND MEDLEY", "ACROSS FROM AKRON"). They are
// never valid as an intersection side or street name on their own.
var intersectionLeadNonStreets = map[string]bool{
	"AROUND": true, "ACROSS": true, "BETWEEN": true, "FROM": true, "INTO": true,
	"THROUGH": true, "TOWARD": true, "TOWARDS": true, "NEAR": true, "BY": true,
	"VIA": true, "BEHIND": true, "BEYOND": true, "PAST": true, "UNTIL": true,
	"INSIDE": true, "OUTSIDE": true, "BEFORE": true, "AFTER": true, "OVER": true,
	"UNDER": true, "ABOUT": true, "ALONG": true, "BESIDE": true, "BELOW": true,
	"ABOVE": true, "WITHIN": true, "WITHOUT": true,
}

// IntersectionSideIsNonStreet reports whether a parsed intersection side is
// obviously not a street name (preposition lead-in or standalone noise token).
func IntersectionSideIsNonStreet(side string) bool {
	u := strings.ToUpper(strings.TrimSpace(side))
	if u == "" {
		return true
	}
	fields := strings.Fields(u)
	if len(fields) == 0 {
		return true
	}
	if len(fields) == 1 && intersectionLeadNonStreets[fields[0]] {
		return true
	}
	if TokenIsRadioCommsNoise(fields[0]) {
		return true
	}
	if intersectionLeadNonStreets[fields[0]] {
		return true
	}
	return false
}

// AddressAlignsWithTranscript reports whether each meaningful token in the
// extracted address actually appears in the dispatch transcript. Intersections
// require both sides to be present; house+street requires the house number
// and street words. Fails closed when alignment cannot be verified.
//
// Both the address and transcript are passed through CanonicalStreetName first
// so gateway/OSM expansions never reject a real hit: SE≡SOUTHEAST, RD≡ROAD,
// AVE≡AVENUE, N≡NORTH, NORTH EAST≡NE, etc.
func AddressAlignsWithTranscript(addr, transcript string, scope *ScopeData) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" || strings.TrimSpace(transcript) == "" {
		return false
	}
	rawTranscript := transcript
	addr = CanonicalStreetName(normalizeHyphenCompoundsInTranscript(addr))
	padded := " " + CanonicalStreetName(normalizeHyphenCompoundsInTranscript(transcript)) + " "

	if strings.Contains(addr, "&") {
		a, b := splitIntersectionQuery(addr)
		a = CanonicalStreetName(cleanIntersectionSide(a))
		b = CanonicalStreetName(cleanIntersectionSide(b))
		if a == "" || b == "" {
			return false
		}
		if IntersectionSideIsNonStreet(a) || IntersectionSideIsNonStreet(b) {
			return false
		}
		if intersectionCaptureIsMedicalFacilityPhrase(a, b, strings.Trim(padded, " ")) {
			return false
		}
		return intersectionSideStrictlyAlignsWithTranscript(a, padded, scope) &&
			intersectionSideStrictlyAlignsWithTranscript(b, padded, scope)
	}

	house, street := splitHouseAndStreet(addr)
	if house != "" && street != "" {
		if !wordInPaddedTranscript(padded, house) && !houseInHyphenPairTranscript(house, rawTranscript) {
			return false
		}
		return streetAlignsWithTranscript(street, padded, scope, house)
	}

	target := street
	if target == "" {
		target = addr
	}
	if streetHasGeocodableSuffix(target) || hasStreetSuffix(target) || routeSideLooksValid(target) {
		return streetAlignsWithTranscript(target, padded, scope, "")
	}
	return false
}

func routeSideLooksValid(side string) bool {
	if _, ok := ohioStateRouteNumberInText(normalizeRouteTokens(strings.ToUpper(strings.TrimSpace(side)))); ok {
		return true
	}
	fields := strings.Fields(strings.ToUpper(strings.TrimSpace(side)))
	return len(fields) >= 2 && localRouteKeywords[fields[0]] && isShortRouteNumber(fields[1])
}

func intersectionSideAlignsWithTranscript(side, padded string, scope *ScopeData) bool {
	side = strings.ToUpper(strings.TrimSpace(side))
	fields := strings.Fields(side)
	if len(fields) >= 2 && localRouteKeywords[fields[0]] && isShortRouteNumber(fields[1]) {
		return wordInPaddedTranscript(padded, fields[1])
	}
	return streetAlignsWithTranscript(side, padded, scope, "")
}

// intersectionSideStrictlyAlignsWithTranscript requires each stem word of an
// intersection side to appear verbatim in the transcript. Fuzzy gazetteer snaps
// (PAIN→PANIN, SPINE→SPIKE) are rejected for intersection pins.
func intersectionSideStrictlyAlignsWithTranscript(side, padded string, scope *ScopeData) bool {
	side = strings.ToUpper(strings.TrimSpace(side))
	if side == "" {
		return false
	}
	fields := strings.Fields(side)
	if len(fields) >= 2 && localRouteKeywords[fields[0]] && isShortRouteNumber(fields[1]) {
		return wordInPaddedTranscript(padded, fields[1])
	}
	if collapsedStreetAlignsWithTranscript(side, padded) {
		return true
	}
	stem, _ := streetNameAndSuffix(side)
	words := significantStreetWords(stem)
	if len(words) == 0 {
		words = strings.Fields(stem)
	}
	if len(words) == 0 {
		return false
	}
	matched := 0
	for _, w := range words {
		if len(w) < 3 || localStreetSuffixes[w] || streetDirTokens[w] {
			continue
		}
		if wordInPaddedTranscript(padded, w) {
			matched++
			continue
		}
		// The side may have already been fuzzy/STT-corrected against the
		// gazetteer (e.g. spoken "LAMONT" snapped to the real "LUMONT
		// DRIVE"), so the literal spoken word no longer appears verbatim.
		// Fall back to a per-word STT match against the transcript's own
		// tokens before rejecting outright.
		if wordFuzzyMatchesTranscript(padded, w) {
			matched++
			continue
		}
		return false
	}
	return matched > 0
}

// wordFuzzyMatchesTranscript reports whether any single token in the
// (padded, uppercase) transcript is a plausible STT mis-hearing of w —
// covers gazetteer-corrected intersection sides whose original spoken form
// differs slightly from the snapped name.
func wordFuzzyMatchesTranscript(padded, w string) bool {
	w = strings.ToUpper(strings.TrimSpace(w))
	if len(w) < 4 {
		return false
	}
	for _, tok := range strings.Fields(padded) {
		tok = strings.Trim(tok, ",.;:!?")
		if len(tok) < 4 {
			continue
		}
		if StreetNamesSTTMatch(w, tok) {
			return true
		}
	}
	return false
}

// intersectionCaptureIsMedicalFacilityPhrase reports clinical facility names
// misread as cross streets ("ON THE MEDICAL PAIN AND SPINE").
func intersectionCaptureIsMedicalFacilityPhrase(a, b, transcript string) bool {
	u := " " + strings.ToUpper(strings.TrimSpace(transcript)) + " "
	a = strings.ToUpper(strings.TrimSpace(a))
	b = strings.ToUpper(strings.TrimSpace(b))
	if strings.Contains(u, " PAIN AND SPINE") || (a == "PAIN" && b == "SPINE") {
		return strings.Contains(u, " MEDICAL") || strings.Contains(u, " ON THE MEDICAL")
	}
	return false
}

// intersectionGeocodeAllowed reports whether every street used for an
// intersection pin was spoken verbatim in the dispatch transcript.
func intersectionGeocodeAllowed(transcript, streetA string, crosses []string, scope *ScopeData) bool {
	if strings.TrimSpace(transcript) == "" {
		return false
	}
	padded := " " + normalizeHyphenCompoundsInTranscript(strings.ToUpper(transcript)) + " "
	if streetA != "" && !intersectionSideStrictlyAlignsWithTranscript(streetA, padded, scope) {
		return false
	}
	for _, c := range crosses {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !intersectionSideStrictlyAlignsWithTranscript(c, padded, scope) {
			return false
		}
	}
	return true
}

func streetAlignsWithTranscript(street, padded string, scope *ScopeData, house string) bool {
	street = CanonicalStreetName(street)
	if street == "" {
		return false
	}
	// Drop trailing SE/SOUTHEAST (already canonicalized to SE) when unspoken.
	street = stripUnspokenQuadrantSuffix(street, padded)
	street = stripUnspokenTrailingCardinal(street, padded)
	if routeN, ok := ohioStateRouteNumberInText(normalizeRouteTokens(street)); ok {
		return wordInPaddedTranscript(padded, strconv.Itoa(routeN))
	}
	if streetStemSTTMatchesTranscriptWord(street, padded) {
		return true
	}
	if collapsedStreetAlignsWithTranscript(street, padded) {
		return true
	}
	if house != "" {
		spokenNS := collapsedStreetWordsAfterHouse(strings.Trim(padded, " "), house)
		if spokenNS != "" {
			core, _ := StreetCoreTypeKey(CanonicalStreetName(street))
			if core != "" && gluedSpokenStemAlignsWithGazetteer(spokenNS, core) {
				return true
			}
		}
	}
	if scope != nil {
		for _, ks := range scope.KnownStreets {
			ksU := normalizeRouteTokens(strings.ToUpper(strings.TrimSpace(ks)))
			if ksU == "" {
				continue
			}
			if street == ksU || StreetNamesSTTMatch(street, ksU) {
				if gazetteerStreetAlignsWithTranscript(ksU, padded) {
					return true
				}
				if collapsedKnownStreetAlignsWithTranscript(ksU, strings.Trim(padded, " ")) {
					return true
				}
				if fuzzySnappedKnownStreetAligns(street, ksU, strings.Trim(padded, " "), house) {
					return true
				}
			}
		}
	}
	words := significantStreetWords(street)
	if len(words) == 0 && hasStreetSuffix(street) {
		// Directional + suffix thoroughfare names ("NORTH ROAD", "SOUTH MAIN")
		// have no remaining stem after suffix stripping; require the spoken
		// pre-suffix token in the transcript.
		fields := strings.Fields(street)
		if len(fields) >= 2 && localStreetSuffixes[fields[len(fields)-1]] {
			for _, w := range fields[:len(fields)-1] {
				if len(w) >= 3 && !localStreetSuffixes[w] && wordOrHomophoneInPaddedTranscript(padded, w) {
					return true
				}
			}
		}
		return false
	}
	if len(words) == 0 {
		return false
	}
	matched := 0
	for _, w := range words {
		if wordOrHomophoneInPaddedTranscript(padded, w) {
			matched++
		}
	}
	if len(words) == 1 {
		return matched == 1
	}
	return matched >= len(words)
}

// gazetteerStreetAlignsWithTranscript verifies a known-street snap using only
// stem words dispatch actually spoke. Trailing quadrant suffixes (SOUTHEAST)
// added from the gazetteer are not required to appear verbatim in STT.
func gazetteerStreetAlignsWithTranscript(ksU, padded string) bool {
	stem := stripUnspokenQuadrantSuffix(ksU, padded)
	words := significantStreetWords(stem)
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if !wordOrHomophoneInPaddedTranscript(padded, w) {
			return false
		}
	}
	return true
}

func stripUnspokenQuadrantSuffix(street, padded string) string {
	fields := strings.Fields(CanonicalStreetName(street))
	if len(fields) < 2 {
		return street
	}
	last := fields[len(fields)-1]
	if !isStreetQuadrantSuffix(last) {
		return street
	}
	// padded is expected already CanonicalStreetName'd (SE not SOUTHEAST).
	if wordInPaddedTranscript(padded, last) {
		return strings.Join(fields, " ")
	}
	return strings.Join(fields[:len(fields)-1], " ")
}

// stripUnspokenTrailingCardinal drops a trailing N/S/E/W that OSM/gateway
// added when dispatch never spoke that cardinal.
func stripUnspokenTrailingCardinal(street, padded string) string {
	fields := strings.Fields(CanonicalStreetName(street))
	if len(fields) < 2 {
		return street
	}
	last := fields[len(fields)-1]
	if !isStreetTrailingCardinal(last) {
		return street
	}
	if wordInPaddedTranscript(padded, last) {
		return strings.Join(fields, " ")
	}
	return strings.Join(fields[:len(fields)-1], " ")
}

func isStreetQuadrantSuffix(tok string) bool {
	switch strings.ToUpper(strings.TrimSpace(tok)) {
	case "SE", "SW", "NE", "NW", "SOUTHEAST", "SOUTHWEST", "NORTHEAST", "NORTHWEST":
		return true
	}
	return false
}

// isStreetTrailingCardinal reports N/S/E/W (and full forms) used after a
// thoroughfare type ("LAKE ROAD WEST", "MAIN STREET NORTH").
func isStreetTrailingCardinal(tok string) bool {
	switch strings.ToUpper(strings.TrimSpace(tok)) {
	case "N", "S", "E", "W", "NORTH", "SOUTH", "EAST", "WEST":
		return true
	}
	return false
}

func spokenStreetQuadrant(street string) string {
	for _, f := range strings.Fields(strings.ToUpper(strings.TrimSpace(street))) {
		if isStreetQuadrantSuffix(f) {
			return f
		}
	}
	return ""
}

// collapsedStreetAlignsWithTranscript reports when dispatch spoke a compound
// street with STT whitespace splits that match the gazetteer stem ("AUSTIN TOWN
// WARREN" vs "AUSTINTOWN WARREN ROAD").
func collapsedStreetAlignsWithTranscript(street, padded string) bool {
	stem, _ := streetNameAndSuffix(street)
	if stem == "" {
		stem = street
	}
	stemNS := stripStreetSpaces(stem)
	if len(stemNS) < 10 {
		return false
	}
	transcript := strings.Trim(padded, " ")
	for _, m := range transcriptHouseStreetLeadRE.FindAllStringSubmatch(transcript, -1) {
		if len(m) < 2 {
			continue
		}
		if collapsedStreetWordsAfterHouse(transcript, strings.TrimSpace(m[1])) == stemNS {
			return true
		}
	}
	return strings.Contains(stripStreetSpaces(transcript), stemNS)
}

// streetThoroughfareSuffixSpokenInTranscript reports when dispatch actually said
// the thoroughfare type on this street (STREET, ROAD, etc.), not just the name stem.
func streetThoroughfareSuffixSpokenInTranscript(street, transcript string) bool {
	_, suffix := streetNameAndSuffix(strings.ToUpper(strings.TrimSpace(street)))
	if suffix == "" {
		return false
	}
	u := " " + strings.ToUpper(transcript) + " "
	switch suffix {
	case "ST", "STREET":
		return strings.Contains(u, " STREET ") || strings.Contains(u, " ST ") || strings.HasSuffix(strings.TrimSpace(u), " STREET")
	case "RD", "ROAD":
		return strings.Contains(u, " ROAD ") || strings.Contains(u, " RD ")
	case "AVE", "AVENUE":
		return strings.Contains(u, " AVENUE ") || strings.Contains(u, " AVE ")
	case "DR", "DRIVE":
		return strings.Contains(u, " DRIVE ") || strings.Contains(u, " DR ")
	case "LN", "LANE":
		return strings.Contains(u, " LANE ") || strings.Contains(u, " LN ")
	case "BLVD", "BOULEVARD":
		return strings.Contains(u, " BOULEVARD ") || strings.Contains(u, " BLVD ")
	case "CT", "COURT":
		return strings.Contains(u, " COURT ") || strings.Contains(u, " CT ")
	case "PL", "PLACE":
		return strings.Contains(u, " PLACE ") || strings.Contains(u, " PL ")
	case "WAY":
		return strings.Contains(u, " WAY ")
	case "TRL", "TRAIL":
		return strings.Contains(u, " TRAIL ") || strings.Contains(u, " TRL ")
	case "CIR", "CIRCLE":
		return strings.Contains(u, " CIRCLE ") || strings.Contains(u, " CIR ")
	case "PT", "POINT":
		return strings.Contains(u, " POINT ") || strings.Contains(u, " PT ")
	case "PKWY", "PARKWAY":
		return strings.Contains(u, " PARKWAY ") || strings.Contains(u, " PKWY ")
	case "HWY", "HIGHWAY":
		return strings.Contains(u, " HIGHWAY ") || strings.Contains(u, " HWY ")
	}
	return wordInPaddedTranscript(u, suffix)
}

// homonymSuffixSwapUnspokenInTranscript blocks ROAD↔STREET relabeling when dispatch
// only spoke the shared stem ("520 FENTON") and neither suffix was in STT.
func homonymSuffixSwapUnspokenInTranscript(fromStreet, toStreet, transcript string) bool {
	if !streetsShareLeadingNameToken(fromStreet, toStreet) {
		return false
	}
	fromName, fromSfx := streetNameAndSuffix(strings.ToUpper(strings.TrimSpace(fromStreet)))
	toName, toSfx := streetNameAndSuffix(strings.ToUpper(strings.TrimSpace(toStreet)))
	if fromName == "" || toName == "" || fromName != toName || fromSfx == toSfx {
		return false
	}
	if streetThoroughfareSuffixSpokenInTranscript(fromStreet, transcript) ||
		streetThoroughfareSuffixSpokenInTranscript(toStreet, transcript) {
		return false
	}
	return true
}

func significantStreetWords(street string) []string {
	skip := map[string]bool{
		"SR": true, "CR": true, "US": true, "HWY": true, "I": true, "RT": true, "RTE": true,
		"ST": true, "STREET": true, "RD": true, "ROAD": true, "AVE": true, "AVENUE": true,
		"DR": true, "DRIVE": true, "BLVD": true, "LN": true, "LANE": true, "CT": true,
		"COURT": true, "PL": true, "PLACE": true, "WAY": true, "TRL": true, "TRAIL": true,
		"N": true, "S": true, "E": true, "W": true, "NE": true, "NW": true, "SE": true, "SW": true,
		"NORTH": true, "SOUTH": true, "EAST": true, "WEST": true,
		// Gateway/OSM expand spoken SE→SOUTHEAST; do not require the long form
		// in STT (6246 MINES ROAD SE vs … SOUTHEAST).
		"NORTHEAST": true, "NORTHWEST": true, "SOUTHEAST": true, "SOUTHWEST": true,
	}
	var out []string
	for _, w := range strings.Fields(strings.ToUpper(street)) {
		if len(w) < 3 || skip[w] {
			continue
		}
		out = append(out, w)
	}
	return out
}

func houseInHyphenPairTranscript(house, transcript string) bool {
	h := strings.ToUpper(strings.TrimSpace(house))
	if h == "" {
		return false
	}
	u := strings.ToUpper(transcript)
	return strings.Contains(u, "-"+h) || strings.Contains(u, h+"-")
}

// normalizeHyphenCompoundsInTranscript rewrites letter-hyphen-letter tokens so
// alignment can match compound dispatch streets ("MCCLARY-YACOBY").
func normalizeHyphenCompoundsInTranscript(transcript string) string {
	return hyphenatedCompoundStreetRE.ReplaceAllString(strings.ToUpper(transcript), "$1 $2")
}

// collapsedKnownStreetAlignsWithTranscript reports when the spoken street phrase
// after a house number fuzzy-matches a gazetteer stem (MCCLARY YACOBY vs
// MC CLEARY JACOBY ROAD).
func collapsedKnownStreetAlignsWithTranscript(ksU, transcript string) bool {
	transcript = normalizeHyphenCompoundsInTranscript(transcript)
	for _, house := range transcriptHouseNumbers(transcript) {
		if collapsedSpokenCompletesKnownStem(transcript, house, []string{ksU}) {
			return true
		}
		spokenNS := collapsedStreetWordsAfterHouse(transcript, house)
		core, sfx := StreetCoreTypeKey(CanonicalStreetName(ksU))
		if sfx == "" || core == "" || spokenNS == "" {
			continue
		}
		stemNS := core
		if spokenNS == stemNS {
			return true
		}
		if len(spokenNS) >= 10 && len(stemNS) >= 10 && StreetNamesSTTMatch(spokenNS, stemNS) {
			return true
		}
		if gluedSpokenStemAlignsWithGazetteer(spokenNS, stemNS) {
			return true
		}
		if len(spokenNS) >= 12 && len(stemNS) >= 12 && levenshtein(spokenNS, stemNS) <= 3 {
			return true
		}
	}
	return false
}

func transcriptHouseNumbers(transcript string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(h string) {
		h = strings.TrimSpace(h)
		if h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	for _, m := range transcriptDispatchAddressRE.FindAllStringSubmatch(transcript, -1) {
		if len(m) >= 2 {
			add(m[1])
		}
	}
	for _, m := range transcriptHouseStreetLeadRE.FindAllStringSubmatch(transcript, -1) {
		if len(m) >= 2 {
			add(m[1])
		}
	}
	return out
}

// fuzzySnappedKnownStreetAligns allows gazetteer snaps that fuzzy-match a
// known street when dispatch spoke a collapsed compound variant (MCCLARY YACOBY
// vs MC CLEARY JACOBY ROAD).
func fuzzySnappedKnownStreetAligns(snappedStreet, ksU, transcript, house string) bool {
	snappedStreet = strings.ToUpper(strings.TrimSpace(snappedStreet))
	if _, st := splitHouseAndStreet(snappedStreet); st != "" {
		snappedStreet = st
	}
	if !StreetNamesSTTMatch(snappedStreet, ksU) {
		return false
	}
	transcript = normalizeHyphenCompoundsInTranscript(transcript)
	spokenNS := ""
	houses := transcriptHouseNumbers(transcript)
	if house != "" {
		houses = append(houses, house)
	}
	seen := map[string]bool{}
	for _, h := range houses {
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		if ns := collapsedStreetWordsAfterHouse(transcript, h); len(ns) > len(spokenNS) {
			spokenNS = ns
		}
	}
	stem, sfx := StreetCoreTypeKey(CanonicalStreetName(ksU))
	if sfx == "" || stem == "" || spokenNS == "" {
		return false
	}
	stemNS := stem
	if spokenNS == stemNS {
		return true
	}
	if len(strings.Fields(spokenNS)) == 1 && len(strings.Fields(stemNS)) == 1 && spokenNS != stemNS {
		return false
	}
	if StreetNamesSTTMatch(spokenNS, stemNS) {
		return true
	}
	return gluedSpokenStemAlignsWithGazetteer(spokenNS, stemNS) ||
		(len(spokenNS) >= 12 && len(stemNS) >= 12 && levenshtein(spokenNS, stemNS) <= 3)
}

// gluedSpokenStemAlignsWithGazetteer accepts STT-glued street tokens ("KINGSGRAVE")
// against collapsed gazetteer stems ("KINGGRAVES") when edit distance is small.
func gluedSpokenStemAlignsWithGazetteer(spokenNS, stemNS string) bool {
	spokenNS = stripStreetSpaces(spokenNS)
	stemNS = stripStreetSpaces(stemNS)
	if spokenNS == "" || stemNS == "" {
		return false
	}
	if spokenNS == stemNS {
		return true
	}
	if len(spokenNS) < 8 || len(stemNS) < 8 {
		return false
	}
	if strings.Contains(spokenNS, "GRAV") && strings.Contains(stemNS, "STON") &&
		!strings.Contains(stemNS, "GRAV") {
		return false
	}
	if ScoreStreetSTTCoreMatch(spokenNS, stemNS, nil) >= sttMatchScoreThreshold {
		return true
	}
	return levenshtein(spokenNS, stemNS) <= 2
}

func wordInPaddedTranscript(padded, word string) bool {
	word = strings.ToUpper(strings.TrimSpace(word))
	if word == "" {
		return false
	}
	return strings.Contains(padded, " "+word+" ") ||
		strings.Contains(padded, " "+word+",") ||
		strings.Contains(padded, " "+word+".") ||
		strings.HasSuffix(strings.TrimSpace(padded), " "+word)
}

func wordOrHomophoneInPaddedTranscript(padded, word string) bool {
	return streetWordAlignsWithTranscript(word, padded)
}

// streetWordAlignsWithTranscript reports whether a street-name word in the
// extracted address is supported by the transcript. Homophone matching is
// allowed for near-miss spellings (BEACH/BEECH) but not when dispatch spoke a
// strictly longer form of the same token (SQUAWK vs a shortened SQUAW snap).
// Abbreviation expansions are handled upstream via CanonicalStreetName on both
// the address and the padded transcript (SE≡SOUTHEAST, RD≡ROAD, …).
func streetWordAlignsWithTranscript(word, padded string) bool {
	word = strings.TrimSpace(CanonicalStreetName(word))
	if word == "" {
		return false
	}
	if wordInPaddedTranscript(padded, word) {
		return true
	}
	if transcriptHasLongerFormOfWord(padded, word) {
		return false
	}
	for _, tok := range strings.Fields(strings.Trim(padded, " ")) {
		tok = strings.Trim(tok, ".,;")
		tok = strings.TrimSpace(CanonicalStreetName(tok))
		if tok == "" {
			continue
		}
		if StreetTokensSTTMatch(word, tok) {
			return true
		}
		if dispatchCompoundTokenAligns(word, tok) {
			return true
		}
	}
	return false
}

// dispatchCompoundTokenAligns allows near-miss compound street tokens where STT
// swaps a leading consonant on an otherwise identical suffix (YACOBY/JACOBY).
func dispatchCompoundTokenAligns(a, b string) bool {
	a = strings.ToUpper(strings.TrimSpace(a))
	b = strings.ToUpper(strings.TrimSpace(b))
	if a == "" || b == "" || a == b {
		return false
	}
	if len(a) < 5 || len(b) < 5 || absInt(len(a)-len(b)) > 1 {
		return false
	}
	return levenshtein(a, b) <= 1
}

// transcriptHasLongerFormOfWord reports when dispatch spoke a longer token
// that extends the address word as a prefix (STT glue or a bad gazetteer snap).
func transcriptHasLongerFormOfWord(padded, word string) bool {
	word = strings.ToUpper(strings.TrimSpace(word))
	if word == "" || wordInPaddedTranscript(padded, word) {
		return false
	}
	for _, tok := range strings.Fields(strings.Trim(padded, " ")) {
		tok = strings.Trim(tok, ".,;")
		if len(tok) <= len(word) {
			continue
		}
		if strings.HasPrefix(tok, word) && levenshtein(word, tok) <= 2 {
			return true
		}
	}
	return false
}

// streetStemSTTMatchesTranscriptWord accepts a street whose stem phonetically
// matches a spoken transcript word (STT swaps like MAHONEY/MAHONING) — the
// universal form of per-name alignment cases.
func streetStemSTTMatchesTranscriptWord(street, padded string) bool {
	stem, _ := streetNameAndSuffix(CanonicalStreetName(street))
	stem = strings.TrimSpace(stem)
	if len(stem) < 5 || strings.Contains(stem, " ") {
		return false
	}
	for _, tok := range strings.Fields(strings.ToUpper(padded)) {
		tok = strings.Trim(tok, ".,;:!?")
		if len(tok) < 5 {
			continue
		}
		if tok == stem || StreetNamesSTTMatch(stem, tok) {
			return true
		}
	}
	return false
}
