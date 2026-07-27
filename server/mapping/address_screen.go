// Copyright (C) 2025 Thinline Dynamic Solutions
//
// address_screen.go — a cheap, local pre-screen that decides whether a
// transcript could possibly contain a dispatch location BEFORE we spend an
// OpenAI extraction call on it. Most radio traffic (status checks, unit
// chatter, acknowledgements) has no address at all and can only ever resolve
// to "skipped" downstream, so the LLM round-trip is pure cost. The screen is
// intentionally generous: when in doubt it returns true so a real address is
// never dropped here — it only filters transcripts with NO location signal of
// any kind. Universal: pattern + per-group known streets/places, no
// per-department hard-coding.

package mapping

import (
	"regexp"
	"strings"
)

// screenStreetSuffixes are thoroughfare words that, when present as a whole
// token, indicate a street name is in the transcript.
// Ambiguous English words that are also street types (RUN, PASS, ROW, BEND,
// COVE, LOOP, PATH, ROUTE, SPUR) are deliberately excluded — as bare tokens
// they false-match chatter ("en route", "dry run"). A real address on such a
// street is still caught by the house-number shape or a known-street match.
var screenStreetSuffixes = []string{
	"STREET", "ST", "ROAD", "RD", "AVENUE", "AVE", "DRIVE", "DR",
	"LANE", "LN", "COURT", "CT", "BOULEVARD", "BLVD", "PLACE", "PL",
	"WAY", "CIRCLE", "CIR", "TRAIL", "TRL", "TERRACE", "TERR", "TER",
	"PARKWAY", "PKWY", "HIGHWAY", "HWY", "PIKE", "TURNPIKE", "FREEWAY",
	"EXPRESSWAY", "BYPASS", "CROSSING", "PLAZA", "ALLEY", "EXTENSION",
	"CONNECTOR", "POINT", "PT", "RUN",
}

// screenSuffixlessAddressRE matches "<house> <word> <word>" dispatch shorthands
// that omit the thoroughfare type ("82 HOWLAND WILSON", "7525 WARREN SHARON").
var screenSuffixlessAddressRE = regexp.MustCompile(`\b(\d{1,6})\s+(?:AT\s+)?([A-Z][A-Z'\-]{2,}\s+[A-Z][A-Z'\-]{2,})\b`)

// screenSingleSuffixlessAddressRE matches "<house> <street>" without a suffix
// ("21 FAIRVIEW") when dispatch drops the thoroughfare type.
var screenSingleSuffixlessAddressRE = regexp.MustCompile(`\b(\d{1,6})\s+([A-Z][A-Z'\-]{3,})\b`)

// screenRouteMarkers are location-strong words/phrases that imply a highway or
// numbered route. Bare " ROUTE " is intentionally excluded — it false-matches
// the ubiquitous "en route" chatter; numbered routes are caught by
// screenRouteNumberRE instead.
var screenRouteMarkers = []string{
	" STATE ROUTE", " STATE ROAD", " US ROUTE", " US RTE",
	" COUNTY ROAD", " COUNTY ROUTE", " CO RD ", " INTERSTATE",
	" TURNPIKE", " FREEWAY", " OH-",
	" MILE MARKER", " MILEPOST", " MILE POST",
}

// screenRouteNumberRE matches a numbered route reference ("SR 32", "US 422",
// "I-80", "ROUTE 14", "HWY 88"). Requiring the trailing number keeps "en
// route", a bare "us", or the pronoun "I" from triggering.
var screenRouteNumberRE = regexp.MustCompile(`\b(?:ROUTE|RT|RTE|SR|US|CR|HWY|HIGHWAY|I|IH|LOOP)\s*-?\s*\d{1,4}\b`)

// screenTypedIntersectionRE requires thoroughfare types on both sides of AND/&.
var screenTypedIntersectionRE = regexp.MustCompile(
	`\b(?:[A-Z0-9][A-Z0-9']+\s+){0,3}[A-Z][A-Z0-9']+\s+(?:ROAD|RD|STREET|ST|AVENUE|AVE|DRIVE|DR|LANE|LN|COURT|CT|BOULEVARD|BLVD|PLACE|PL|WAY|CIRCLE|CIR|HIGHWAY|HWY|PIKE|PARKWAY|PKWY)\s+(?:AND|&)\s+(?:[A-Z0-9][A-Z0-9']+\s+){0,3}[A-Z][A-Z0-9']+\s+(?:ROAD|RD|STREET|ST|AVENUE|AVE|DRIVE|DR|LANE|LN|COURT|CT|BOULEVARD|BLVD|PLACE|PL|WAY|CIRCLE|CIR|HIGHWAY|HWY|PIKE|PARKWAY|PKWY)\b`,
)

// screenLocationKeywords are dispatch phrases that strongly imply a location
// is being given even when no thoroughfare suffix follows immediately.
var screenLocationKeywords = []string{
	" RESPOND TO ", " RESPONDING TO ", " EN ROUTE TO ", " ENROUTE TO ",
	" BLOCK OF ", " INTERSECTION", " CROSS STREET", " CORNER OF ",
	" AT THE CORNER", " ADDRESS ", " LOCATED AT ", " IN THE AREA OF ",
	" AREA OF ", " VICINITY OF ", " TRAILHEAD", " TRAIL HEAD",
	" METRO PARK", " MILE ON ",
	// Travel directions ("Detroit eastbound", "turnpike eastbound") are spoken
	// only about roadways, so they reliably mark a road reference.
	" EASTBOUND", " WESTBOUND", " NORTHBOUND", " SOUTHBOUND",
	" EAST BOUND", " WEST BOUND", " NORTH BOUND", " SOUTH BOUND",
	" BOUND ON ",
}

// screenAddressNumberRE matches a 3–5 digit number followed by a word — the
// "<house number> <street>" shape ("4201 LAMBERT", "399 BISHOP") that has no
// thoroughfare suffix and so escapes the suffix list.
var screenAddressNumberRE = regexp.MustCompile(`\b(\d{3,5})\s+([A-Z][A-Z'\-]{2,})`)

// screenNumberWordNoise are words that, when they follow a number, mean the
// number is NOT a house number (unit IDs, ages, durations, counts).
// Note: "OLD" is intentionally NOT here — it is a common street-name word
// ("8082 OLD FITCH RD"), and ages are spoken hyphenated ("87-YEAR-OLD") so the
// 3–5 digit shape never matches them.
var screenNumberWordNoise = map[string]bool{
	"UNIT": true, "UNITS": true, "STATION": true, "STATIONS": true,
	"ENGINE": true, "MEDIC": true, "SQUAD": true, "CAR": true, "MED": true,
	"TRUCK": true, "RESCUE": true, "BATTALION": true, "CHIEF": true,
	"YEAR": true, "YEARS": true, "MALE": true, "FEMALE": true,
	"HOUR": true, "HOURS": true, "MINUTE": true, "MINUTES": true,
	"AM": true, "PM": true, "TIMES": true, "TIME": true, "POUND": true,
	"POUNDS": true, "DEGREE": true, "DEGREES": true, "FOOT": true, "FEET": true,
}

// screenPunctReplacer flattens punctuation to spaces so token checks work on
// "ST. CLAIR", "RD,", "AVE." etc.
var screenPunctReplacer = strings.NewReplacer(
	".", " ", ",", " ", ";", " ", ":", " ", "!", " ", "?", " ",
	"(", " ", ")", " ", "\"", " ", "\n", " ", "\t", " ",
)

// TranscriptLikelyHasLocation reports whether a transcript plausibly contains a
// dispatch address/location. Primary signal is a digit-bearing house/route
// shape (not unit/age/duration noise); also true for street suffixes, routes,
// intersections, dispatch location phrases, or known street/place names.
// Callers use a false result to skip extraction entirely.
func TranscriptLikelyHasLocation(transcript string, scope *ScopeData) bool {
	if strings.TrimSpace(transcript) == "" {
		return false
	}
	// Soft chatter blockers must not hide a restated house-number address
	// ("… 6010 PRICE ROAD … I'M GOING TO MAKE SURE …").
	if !transcriptHasCleanNumberedDispatchAddress(transcript) {
		if TranscriptBlocksIncidentPin(transcript) || TranscriptIsOnSceneAdvisory(transcript) ||
			TranscriptIsLocationNarrative(transcript) || TranscriptIsOfficerNarrative(transcript) ||
			TranscriptIsDispatchResourceStatusChatter(transcript) ||
			TranscriptIsAdministrativeLocationReference(transcript) {
			return false
		}
	}
	clean := screenPunctReplacer.Replace(strings.ToUpper(transcript))
	clean = strings.Join(strings.Fields(clean), " ")
	padded := " " + clean + " "

	for _, suf := range screenStreetSuffixes {
		if strings.Contains(padded, " "+suf+" ") {
			return true
		}
	}
	for _, m := range screenRouteMarkers {
		if strings.Contains(padded, m) {
			return true
		}
	}
	if screenRouteNumberRE.MatchString(clean) {
		return true
	}
	for _, kw := range screenLocationKeywords {
		if strings.Contains(padded, kw) {
			return true
		}
	}
	if screenHasHouseNumberWord(clean) {
		return true
	}
	if scope != nil && len(scope.KnownStreets) > 0 {
		if screenHasGazetteerBackedSuffixlessAddress(clean, transcript, scope) {
			return true
		}
		if extractBareAndIntersectionAddress(transcript, scope) != "" ||
			extractCommaSeparatedIntersectionAddress(transcript, scope) != "" {
			return true
		}
	} else if screenHasSuffixlessAddress(clean, transcript) {
		return true
	}
	if len(extractDispatchIntersectionsFromTranscript(transcript, screenScopeStreets(scope))) > 0 {
		return true
	}
	if scope != nil && len(scope.KnownStreets) > 0 {
		if screenHasGazetteerBackedIntersection(clean, scope) {
			return true
		}
	} else if screenHasBareIntersection(clean) {
		return true
	}
	if scope != nil {
		for _, st := range scope.KnownStreets {
			stu := strings.ToUpper(strings.TrimSpace(st))
			if len(stu) >= 4 && strings.Contains(padded, " "+stu+" ") {
				return true
			}
		}
		for i := range scope.KnownPlaces {
			dn := strings.ToUpper(strings.TrimSpace(scope.KnownPlaces[i].DisplayName))
			if len(dn) >= 4 && strings.Contains(padded, dn) &&
				PlaceMentionIsDispatchContext(transcript, dn) {
				return true
			}
		}
	}
	return false
}

// TranscriptHasGeocodeAnchor is stricter than TranscriptLikelyHasLocation: it
// requires a house/route/typed-intersection/gazetteer-place cue worth calling
// the Nominatim gateway. Bare STREET mentions, "EN ROUTE TO", and eastbound
// chatter alone return false so MISRN-style status traffic does not burn quota.
func TranscriptHasGeocodeAnchor(transcript string, scope *ScopeData) bool {
	if strings.TrimSpace(transcript) == "" {
		return false
	}
	if transcriptHasCleanNumberedDispatchAddress(transcript) {
		return true
	}
	clean := screenPunctReplacer.Replace(strings.ToUpper(transcript))
	clean = strings.Join(strings.Fields(clean), " ")
	padded := " " + clean + " "

	if screenRouteNumberRE.MatchString(clean) {
		return true
	}
	for _, m := range screenRouteMarkers {
		if strings.Contains(padded, m) {
			return true
		}
	}
	if screenTypedIntersectionRE.MatchString(clean) {
		return true
	}
	if scope != nil && len(scope.KnownStreets) > 0 {
		if screenHasGazetteerBackedSuffixlessAddress(clean, transcript, scope) {
			return true
		}
		if extractBareAndIntersectionAddress(transcript, scope) != "" ||
			extractCommaSeparatedIntersectionAddress(transcript, scope) != "" {
			return true
		}
		if screenHasGazetteerBackedIntersection(clean, scope) {
			return true
		}
	}
	if len(extractDispatchIntersectionsFromTranscript(transcript, screenScopeStreets(scope))) > 0 {
		return true
	}
	if scope != nil {
		for i := range scope.KnownPlaces {
			dn := strings.ToUpper(strings.TrimSpace(scope.KnownPlaces[i].DisplayName))
			if len(dn) >= 4 && strings.Contains(padded, dn) &&
				PlaceMentionIsDispatchContext(transcript, dn) {
				return true
			}
		}
	}
	return false
}

// screenHasHouseNumberWord reports whether clean contains a "<3-5 digit number>
// <word>" shape where the trailing word is not a known non-address noun
// (unit/age/duration/count).
func screenHasSuffixlessAddress(clean, transcript string) bool {
	for _, m := range screenSuffixlessAddressRE.FindAllStringSubmatch(clean, -1) {
		if len(m) >= 3 && suffixlessStreetNamePlausible(m[2]) &&
			!suffixlessMatchFollowedByNarrativeVerb(m[1], m[2], transcript) {
			return true
		}
	}
	for _, pair := range screenSingleSuffixlessAddressRE.FindAllStringSubmatchIndex(clean, -1) {
		if len(pair) < 4 {
			continue
		}
		if dispatchHouseMatchPrecededByHyphen(clean, pair[2]) {
			continue
		}
		house := strings.TrimSpace(clean[pair[2]:pair[3]])
		st := strings.TrimSpace(clean[pair[4]:pair[5]])
		if suffixlessSingleStreetPlausible(st) &&
			!suffixlessMatchFollowedByNarrativeVerb(house, st, transcript) {
			return true
		}
	}
	return false
}

func screenHasHouseNumberWord(clean string) bool {
	for _, m := range screenAddressNumberRE.FindAllStringSubmatch(clean, -1) {
		if len(m) < 3 {
			continue
		}
		if !screenNumberWordNoise[m[2]] {
			return true
		}
	}
	return false
}

func screenScopeStreets(scope *ScopeData) []string {
	if scope == nil {
		return nil
	}
	return scope.KnownStreets
}

// screenIntersectionStopwords are tokens that commonly flank "and" in chatter
// but never name a street, so an "X and Y" with one of these on either side is
// not treated as a possible intersection.
var screenIntersectionStopwords = map[string]bool{
	"HIM": true, "HER": true, "THEM": true, "THEY": true, "THIS": true,
	"THAT": true, "THESE": true, "THOSE": true, "MINE": true, "OURS": true,
	"YOURS": true, "HERS": true, "FIRE": true, "POLICE": true, "EMS": true,
	"RESCUE": true, "MEDIC": true, "ENGINE": true, "SQUAD": true, "UNIT": true,
	"UNITS": true, "CHIEF": true, "DISPATCH": true, "CALLER": true,
	"MALE": true, "FEMALE": true, "BACK": true, "FORTH": true, "OVER": true,
	"OUT": true, "CLEAR": true, "COPY": true, "AGAIN": true, "HERE": true,
	"THERE": true, "EVERYTHING": true, "ANYTHING": true, "NOTHING": true,
	"SOMETHING": true, "ONCE": true, "TWICE": true, "BOTH": true, "THEN": true,
	"NOW": true, "WAIT": true, "HOLD": true, "DONE": true, "GOOD": true,
	"OKAY": true, "READY": true, "MOTHER": true, "FATHER": true, "SON": true,
	"DAUGHTER": true, "WIFE": true, "HUSBAND": true, "BROTHER": true,
	"SISTER": true, "FRIEND": true, "NEIGHBOR": true,
	"STATIC": true, "BROKEN": true, "GARBLED": true, "SCRATCHY": true,
	"WEAK": true, "BUSY": true, "NAUSEOUS": true, "NAUSEA": true, "DIZZY": true,
}

// screenConnectorTokens are the intersection connectors recognized between two
// street names.
var screenConnectorTokens = map[string]bool{"AND": true, "&": true, "/": true}

// screenHasBareIntersection reports whether clean contains an "X and Y" phrase
// where the tokens immediately flanking the connector both look like proper
// street names (alphabetic, ≥4 chars, not a chatter stopword). Catches
// suffixless intersections like "WEST KNIFE AND SUPERIOR" that the routeish
// intersection extractor misses, while avoiding pronoun/role pairings.
func screenHasBareIntersection(clean string) bool {
	fields := strings.Fields(clean)
	for i := 1; i < len(fields)-1; i++ {
		if !screenConnectorTokens[fields[i]] {
			continue
		}
		left := fields[i-1]
		right := fields[i+1]
		if tokenIsPatientSymptom(left) || tokenIsPatientSymptom(right) {
			continue
		}
		if screenTokenIsStreetNameLike(left) && screenTokenIsStreetNameLike(right) {
			return true
		}
	}
	return false
}

func screenTokenIsStreetNameLike(tok string) bool {
	if len(tok) < 4 || screenIntersectionStopwords[tok] {
		return false
	}
	for _, c := range tok {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// screenHasGazetteerBackedSuffixlessAddress is like screenHasSuffixlessAddress
// but requires the captured street phrase to resolve to an imported thoroughfare.
func screenHasGazetteerBackedSuffixlessAddress(clean, transcript string, scope *ScopeData) bool {
	for _, m := range screenSuffixlessAddressRE.FindAllStringSubmatch(clean, -1) {
		if len(m) >= 3 && suffixlessPhraseInGazetteer(m[2], scope) &&
			!suffixlessMatchFollowedByNarrativeVerb(m[1], m[2], transcript) {
			return true
		}
	}
	for _, pair := range screenSingleSuffixlessAddressRE.FindAllStringSubmatchIndex(clean, -1) {
		if len(pair) < 6 {
			continue
		}
		if dispatchHouseMatchPrecededByHyphen(clean, pair[2]) {
			continue
		}
		house := strings.TrimSpace(clean[pair[2]:pair[3]])
		st := strings.TrimSpace(clean[pair[4]:pair[5]])
		if suffixlessPhraseInGazetteer(st, scope) &&
			!suffixlessMatchFollowedByNarrativeVerb(house, st, transcript) {
			return true
		}
	}
	return false
}

// screenHasGazetteerBackedIntersection requires both sides of a bare "X and Y"
// connector phrase to resolve to imported streets when gazetteer data exists.
func screenHasGazetteerBackedIntersection(clean string, scope *ScopeData) bool {
	fields := strings.Fields(clean)
	for i := 1; i < len(fields)-1; i++ {
		if !screenConnectorTokens[fields[i]] {
			continue
		}
		left := fields[i-1]
		right := fields[i+1]
		if tokenIsPatientSymptom(left) || tokenIsPatientSymptom(right) {
			continue
		}
		if !screenTokenIsStreetNameLike(left) || !screenTokenIsStreetNameLike(right) {
			continue
		}
		if bareIntersectionSidePlausible(left, scope) && bareIntersectionSidePlausible(right, scope) {
			return true
		}
	}
	return false
}
