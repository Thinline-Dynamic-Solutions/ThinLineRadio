// Copyright (C) 2025 Thinline Dynamic Solutions
//
// geocode.go — shared geocoding utilities: the outbound-geocode viewbox
// builder, STT digit-doubling cleanup (house numbers glued by a repeated
// spoken readback), and dispatch-station-number un-gluing. The Google Maps
// Geocoding API wrapper that used to live here was removed.

package mapping

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// geoBoundsBufferMiles is the maximum cushion beyond BoundsRadiusMi for accepting
// or retaining any geocoded pin (mutual-aid edge cases). Pins farther than
// BoundsRadiusMi + this value from the configured center are rejected.
const geoBoundsBufferMiles = 2.0

// milesPerDegreeLat is the constant used everywhere in this package to
// convert a radius in miles to a latitude delta in degrees.
const milesPerDegreeLat = 69.0

// geocodeViewboxParam builds a Nominatim "minLon,maxLat,maxLon,minLat"
// viewbox string (paired with bounded=1 by the gateway) for the current
// request disc: home/tone-set bounds (+buffer) and, when dispatch actually
// named another community, that spoken-locality disc. Mutual-aid destination
// catalogs are NOT unioned in — they exist so ApplyMutualAidGeoOverride can
// rewrite BoundsLat/Lon when a peer city is spoken. Unioning every talkgroup
// on the system (Niles FD + Newton Falls + Warren + …) made a 10-mile Niles
// search ~20 miles wide and let "36 WEST CHURCH STREET" pin in Newton Falls.
// Returns "" when no bounds are configured at all, so callers with no
// coverage area behave exactly as before (no viewbox sent, unbounded
// nationwide search).
func geocodeViewboxParam(geo *GeoOptions) string {
	if geo == nil {
		return ""
	}
	var minLat, maxLat, minLon, maxLon float64
	have := false
	grow := func(lat, lon, radiusMi float64) {
		if radiusMi <= 0 || (lat == 0 && lon == 0) {
			return
		}
		dLat := radiusMi / milesPerDegreeLat
		cosLat := math.Cos(lat * math.Pi / 180)
		if cosLat < 0.1 {
			cosLat = 0.1
		}
		dLon := radiusMi / (milesPerDegreeLat * cosLat)
		lo, hi := lat-dLat, lat+dLat
		lLon, rLon := lon-dLon, lon+dLon
		if !have {
			minLat, maxLat, minLon, maxLon = lo, hi, lLon, rLon
			have = true
			return
		}
		minLat, maxLat = math.Min(minLat, lo), math.Max(maxLat, hi)
		minLon, maxLon = math.Min(minLon, lLon), math.Max(maxLon, rLon)
	}
	// Search disc = current request bounds (+buffer), plus a spoken-locality
	// disc when dispatch named that community. Do NOT expand to
	// HomeMaxRadiusMi or the full MutualAidDestinations catalog: those are
	// pin-accept escape hatches, not search boxes. Using them as the viewbox
	// (Niles 10mi → county union, or Niles 5mi → 30mi HomeMax) lets far
	// homonyms win addr-search (36 WEST CHURCH → Newton Falls; 1500 MCKINLEY
	// → Poland's West McKinley Way via digit-repair).
	if geo.BoundsRadiusMi > 0 && (geo.BoundsLat != 0 || geo.BoundsLon != 0) {
		grow(geo.BoundsLat, geo.BoundsLon, geo.BoundsRadiusMi+geoBoundsBufferMiles)
	} else if geo.HomeMaxRadiusMi > 0 {
		// Bounds cleared (mutual-aid unrecognized-city fallback): fall back
		// to the absolute home ceiling so the gateway still has a box.
		grow(geo.HomeLat, geo.HomeLon, geo.HomeMaxRadiusMi)
	}
	grow(geo.SpokenLocalityLat, geo.SpokenLocalityLon, geo.SpokenLocalityRadiusMi)
	if !have {
		return ""
	}
	return fmt.Sprintf("%.5f,%.5f,%.5f,%.5f", minLon, maxLat, maxLon, minLat)
}

// concatenatedDigitTokenRE matches digit-only tokens long enough to hold a
// repeated house number (e.g. "563563", "14391439") anywhere in a transcript.
var concatenatedDigitTokenRE = regexp.MustCompile(`\b\d{4,}\b`)

// dedupeRepeatedDigitToken returns the first half when token is STT that glued a
// repeated house number with no separator ("563563" → "563"). Tokens of four
// digits or fewer are never deduped — "5555" and "1212" are real addresses, not
// doubled readbacks.
func dedupeRepeatedDigitToken(token string) string {
	if len(token) <= 4 || len(token)%2 != 0 {
		return ""
	}
	for _, c := range token {
		if c < '0' || c > '9' {
			return ""
		}
	}
	half := len(token) / 2
	if token[:half] == token[half:] {
		return token[:half]
	}
	return ""
}

// deduplicateConcatenatedNumber detects when STT has concatenated a repeated
// house number with no separator (e.g. "14391439 SALT SPRING" → "1439 SALT SPRING",
// "563563 BROOKFIELD AVENUE" → "563 BROOKFIELD AVENUE") and returns a cleaned
// transcript. Applies to any digit-only token whose first half equals its second.
func deduplicateConcatenatedNumber(transcript string) string {
	return concatenatedDigitTokenRE.ReplaceAllStringFunc(transcript, func(token string) string {
		if deduped := dedupeRepeatedDigitToken(token); deduped != "" {
			return deduped
		}
		return token
	})
}

// DispatchHouseFromTranscriptForStreet returns the house number dispatch spoke
// before street when extraction dropped it. PreCleanTranscript must run first so
// glued readbacks are collapsed (108108 → 108, 33793379 → 3379).
func DispatchHouseFromTranscriptForStreet(transcript, street string) string {
	street = strings.ToUpper(strings.TrimSpace(street))
	if street == "" {
		return ""
	}
	stem, _ := streetNameAndSuffix(street)
	if stem == "" {
		stem = street
	}
	cleaned := PreCleanTranscript(transcript)
	for _, a := range extractDispatchAddressesFromTranscript(cleaned) {
		h, st := splitHouseAndStreet(strings.ToUpper(strings.TrimSpace(a)))
		if h == "" || st == "" {
			continue
		}
		if dispatchStreetMatchesSalvageStem(st, stem, street) {
			return h
		}
	}
	for _, frag := range []string{street, stem} {
		if frag == "" {
			continue
		}
		if h := dispatchHouseBeforeStreetStem(cleaned, frag); h != "" {
			return h
		}
	}
	return ""
}

func dispatchStreetMatchesSalvageStem(st, stem, fullStreet string) bool {
	st = strings.ToUpper(strings.TrimSpace(st))
	if st == stem || st == fullStreet || strings.HasPrefix(st, stem+" ") {
		return true
	}
	stName, _ := streetNameAndSuffix(st)
	return stName == stem || StreetNamesSTTMatch(stName, stem)
}

func dispatchHouseBeforeStreetStem(transcript, streetStem string) string {
	u := strings.ToUpper(strings.TrimSpace(transcript))
	streetStem = strings.ToUpper(strings.TrimSpace(streetStem))
	if streetStem == "" {
		return ""
	}
	for _, sep := range []string{" ", ", "} {
		needle := sep + streetStem
		idx := 0
		for idx < len(u) {
			rel := strings.Index(u[idx:], needle)
			if rel < 0 {
				break
			}
			pos := idx + rel
			if pos > 0 {
				start := pos - 12
				if start < 0 {
					start = 0
				}
				before := strings.TrimSpace(u[start:pos])
				fields := strings.Fields(before)
				if len(fields) > 0 {
					cand := strings.TrimRight(fields[len(fields)-1], ".,;:")
					if isAllDigits(cand) && dispatchHouseStreetPhraseInTranscript(u, cand, streetStem) {
						return cand
					}
				}
			}
			idx = pos + len(needle)
		}
	}
	return ""
}

// hyphenDispatchHouseTokenRe matches a pair of digit groups separated by a hyphen
// and followed by whitespace (e.g. "10-20 " before a street name).
var hyphenDispatchHouseTokenRe = regexp.MustCompile(`(?i)\b(\d{1,4})-(\d{1,4})(\s+)`)

// dispatchSpokenDigitChainRe matches a chain of 3+ single-digit groups
// separated by hyphens followed by whitespace (e.g. "1-8-0-5 NORTH" from
// the spelled-out STT of "one eight oh five") and ending before a non-
// digit. Used to collapse the chain into a single house number so the
// downstream LLM doesn't have to guess which digits actually belong
// together (alert 6620 regression: LLM read "1-8-0-5" as "1825" and
// matched the wrong known place).
var dispatchSpokenDigitChainRe = regexp.MustCompile(`(?i)\b(\d-\d(?:-\d){1,5})(\s+)`)

// dispatchLooksLikeStreetAddressSoon returns true if s (typically the text
// after a hyphenated number) contains a thoroughfare token soon — used to
// avoid expanding "10-20 minutes" or similar.
// dispatchLooksLikeNumberedRouteSoon reports when text after a hyphenated digit
// pair is a numbered highway ("876-8 ROUTE 7") rather than a house on a named
// street ("10-20 CENTER STREET").
func dispatchLooksLikeNumberedRouteSoon(s string) bool {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return false
	}
	return dispatchNumberedRouteLeadRE.MatchString(s)
}

var dispatchNumberedRouteLeadRE = regexp.MustCompile(`^(?:ROUTE|RTE|RT|SR|STATE\s+ROUTE|STATE\s+RD|ST\s+ROUTE|ST\s+RTE|US(?:\s+ROUTE|\s+RTE)?|HWY|HIGHWAY)\s+\d`)

func dispatchLooksLikeStreetAddressSoon(s string) bool {
	if len(s) > 180 {
		s = s[:180]
	}
	// Replace punctuation with spaces so a suffix immediately followed by a
	// period/comma ("EAST ROYALTY ROAD.") still satisfies the " ROAD " needles.
	u := " " + strings.Map(func(r rune) rune {
		switch r {
		case '.', ',', ';', ':', '!', '?', '(', ')', '"', '\'', '/':
			return ' '
		}
		return r
	}, strings.ToUpper(s)) + " "
	needles := []string{
		" STREET ", " ST ", " ROAD ", " RD ", " AVENUE ", " AVE ",
		" DRIVE ", " DR ", " LANE ", " LN ", " COURT ", " CT ",
		" BOULEVARD ", " BLVD ", " HIGHWAY ", " HWY ", " ROUTE ", " RT ",
		" PLACE ", " PL ", " WAY ", " CIRCLE ", " CIR ", " TRAIL ", " TERR ",
		" INTERSTATE ", " STATE ROUTE ", " COUNTY ROAD ", " CR ",
	}
	for _, n := range needles {
		if strings.Contains(u, n) {
			return true
		}
	}
	return false
}

// dispatchLooksLikeSuffixlessStreetSoon reports when the text after a short
// hyphenated digit pair (e.g. "2-1 FAIRVIEW") begins with a plausible
// single-word street name and no thoroughfare suffix is spoken.
func dispatchLooksLikeSuffixlessStreetSoon(s string) bool {
	if len(s) > 180 {
		s = s[:180]
	}
	u := strings.ToUpper(strings.TrimSpace(s))
	u = strings.TrimRight(u, ".,;:!?")
	if u == "" {
		return false
	}
	if strings.Contains(" "+u+" ", " OFF OF ") {
		return false
	}
	first := strings.Fields(u)[0]
	first = strings.TrimRight(first, ".,;:")
	if len(first) < 3 {
		return false
	}
	return suffixlessSingleStreetPlausible(first)
}

// expandHyphenHouseNumbersInDispatchTranscript merges dispatch-style hyphenated
// house numbers (e.g. "10-20 CENTER STREET" → "1020 CENTER STREET") when a
// street-type word follows soon after. STT often leaves the hyphen in.
// dispatchAddressContextWords are tokens that commonly follow a spelled-out
// house number in dispatch transcripts: directionals (NORTH/SOUTH/...),
// route phrases, and street prefixes. Used by
// collapseSpokenDigitChainsInDispatchTranscript when the canonical
// thoroughfare check would otherwise leave a real house number
// uncollapsed (alert 6620: "1-8-0-5 NORTH LEAVITT" — no "RD" or "ST"
// follows immediately because LEAVITT is the street name itself).
var dispatchAddressContextWords = []string{
	" NORTH ", " SOUTH ", " EAST ", " WEST ",
	" N ", " S ", " E ", " W ",
	" NE ", " NW ", " SE ", " SW ",
	" STATE ROUTE ", " STATE ROAD ", " ST RTE ", " SR ",
	" US ", " I-", " INTERSTATE ",
	" UPPER ", " LOWER ", " OLD ", " NEW ", " EAST OF ", " WEST OF ",
	" OFF OF ",
}

// dispatchLooksLikeAddressContext returns true when the text following
// a multi-segment digit chain plausibly continues into a street address.
// More permissive than dispatchLooksLikeStreetAddressSoon — accepts
// directional words and route prefixes that typically precede the
// street name even when no explicit thoroughfare suffix is nearby.
func dispatchLooksLikeAddressContext(s string) bool {
	if dispatchLooksLikeStreetAddressSoon(s) {
		return true
	}
	if len(s) > 180 {
		s = s[:180]
	}
	u := " " + strings.ToUpper(s) + " "
	for _, w := range dispatchAddressContextWords {
		if strings.Contains(u, w) {
			return true
		}
	}
	return false
}

// collapseSpokenDigitChainsInDispatchTranscript collapses multi-segment
// hyphenated digit chains like "1-8-0-5 NORTH" into a single number
// "1805 NORTH" before the LLM sees them. Without this, the LLM has to
// disambiguate which subset of "1-8-0-5" forms the house number and
// frequently picks the wrong digits (e.g. "1825" instead of "1805" on
// alert 6620). Only fires when all groups are single digits (the spoken
// pattern) and the next word looks like part of a street address.
func collapseSpokenDigitChainsInDispatchTranscript(transcript string) string {
	idxs := dispatchSpokenDigitChainRe.FindAllStringSubmatchIndex(transcript, -1)
	if len(idxs) == 0 {
		return transcript
	}
	var b strings.Builder
	last := 0
	for _, pair := range idxs {
		full0, full1 := pair[0], pair[1]
		chainStart, chainEnd := pair[2], pair[3]
		spStart, spEnd := pair[4], pair[5]
		chain := transcript[chainStart:chainEnd]
		segs := strings.Split(chain, "-")
		allSingle := true
		for _, s := range segs {
			if len(s) != 1 {
				allSingle = false
				break
			}
		}
		if !allSingle {
			b.WriteString(transcript[last:full1])
			last = full1
			continue
		}
		rest := transcript[full1:]
		if !dispatchLooksLikeAddressContext(rest) {
			b.WriteString(transcript[last:full1])
			last = full1
			continue
		}
		b.WriteString(transcript[last:full0])
		for _, s := range segs {
			b.WriteString(s)
		}
		b.WriteString(transcript[spStart:spEnd])
		last = full1
	}
	b.WriteString(transcript[last:])
	return b.String()
}

func expandHyphenHouseNumbersInDispatchTranscript(transcript string) string {
	idxs := hyphenDispatchHouseTokenRe.FindAllStringSubmatchIndex(transcript, -1)
	if len(idxs) == 0 {
		return transcript
	}
	var b strings.Builder
	last := 0
	for _, pair := range idxs {
		full0, full1 := pair[0], pair[1]
		g10, g11 := pair[2], pair[3]
		g20, g21 := pair[4], pair[5]
		sp0, sp1 := pair[6], pair[7]
		g1 := transcript[g10:g11]
		g2 := transcript[g20:g21]
		if !isAllDigits(g1) || !isAllDigits(g2) {
			b.WriteString(transcript[last:full1])
			last = full1
			continue
		}
		combinedLen := len(g1) + len(g2)
		allowShortPair := combinedLen == 2 && len(g1) == 1 && len(g2) == 1
		if combinedLen < 2 || combinedLen > 6 || (combinedLen < 3 && !allowShortPair) {
			b.WriteString(transcript[last:full1])
			last = full1
			continue
		}
		rest := transcript[full1:]
		trimRest := strings.TrimLeft(rest, " \t")
		if len(trimRest) > 0 && trimRest[0] >= '0' && trimRest[0] <= '9' {
			// e.g. "1-0-5 MAIN ST" — first match "1-0 " must not become "10"; the
			// remaining digit continues the hyphenated house number.
			b.WriteString(transcript[last:full1])
			last = full1
			continue
		}
		if dispatchLooksLikeNumberedRouteSoon(trimRest) {
			b.WriteString(transcript[last:full1])
			last = full1
			continue
		}
		if !dispatchLooksLikeStreetAddressSoon(rest) &&
			!(allowShortPair && dispatchLooksLikeSuffixlessStreetSoon(rest)) {
			b.WriteString(transcript[last:full1])
			last = full1
			continue
		}
		// "ON A 34-0635 SHARON MILL" — run/batch id, not a hyphenated house number.
		before := strings.ToUpper(transcript[max(0, full0-8):full0])
		if strings.HasSuffix(strings.TrimSpace(before), "ON A") {
			b.WriteString(transcript[last:full1])
			last = full1
			continue
		}
		b.WriteString(transcript[last:full0])
		// When both digit groups are identical (e.g. "726-726") it's almost
		// always STT stuttering, not a multi-syllable house number — dedupe to a
		// single group. Universal: applies to any duplicated digit pair.
		if g1 == g2 && len(g1) >= 2 {
			b.WriteString(g1)
		} else {
			b.WriteString(g1)
			b.WriteString(g2)
		}
		b.WriteString(transcript[sp0:sp1])
		last = full1
	}
	b.WriteString(transcript[last:])
	return b.String()
}

// normalizeHyphenatedHousePrefix merges a leading hyphenated house token on an
// address string (e.g. "10-20 CENTER ST W" → "1020 CENTER ST W"). Multi-segment
// forms like "1-0-5" (all single digits) collapse to "105"; three-or-more
// segments with any multi-digit group are left unchanged (avoids "10-17-20"
// time phrases as addresses).
func normalizeHyphenatedHousePrefix(addr string) string {
	addr = strings.TrimSpace(addr)
	fields := strings.Fields(addr)
	if len(fields) < 2 {
		return addr
	}
	tok := fields[0]
	segs := strings.Split(tok, "-")
	if len(segs) < 2 {
		return addr
	}
	for _, s := range segs {
		if !isAllDigits(s) || len(s) > 4 {
			return addr
		}
	}
	if len(segs) >= 3 {
		for _, s := range segs {
			if len(s) != 1 {
				return addr
			}
		}
	}
	var merged strings.Builder
	for _, s := range segs {
		merged.WriteString(s)
	}
	if merged.Len() < 2 || merged.Len() > 6 {
		return addr
	}
	return merged.String() + " " + strings.Join(fields[1:], " ")
}

// stationHouseGlueRE matches STT glue of a station id onto the house number
// ("STATION 12369 NORTH HIGH" ← station 12 + 369; "STATION 312796 ORCHARD" ←
// station 31 + 2796). Confirmed later "STATION 31" mentions prefer that split.
var (
	stationHouseGlueRE       = regexp.MustCompile(`(?i)\b(STATION)\s+(\d{4,6})\s+([A-Za-z][A-Za-z']*)`)
	stationIdMentionRE       = regexp.MustCompile(`(?i)\bSTATION\s+(\d{1,2})\b`)
)

// unglueStationPrefixedHouseNumber splits STT-glued "STATION <station+house>"
// into "STATION <station> <house>" before extract. Universal: prefer a later
// spoken station id when the glued digits start with it; otherwise prefer a
// 2-digit station with a 3–4 digit house remainder (then 1-digit station).
func unglueStationPrefixedHouseNumber(transcript string) string {
	if transcript == "" || !stationHouseGlueRE.MatchString(transcript) {
		return transcript
	}
	confirmed := map[string]bool{}
	for _, m := range stationIdMentionRE.FindAllStringSubmatch(transcript, -1) {
		if len(m) == 2 {
			confirmed[m[1]] = true
		}
	}
	return stationHouseGlueRE.ReplaceAllStringFunc(transcript, func(match string) string {
		sub := stationHouseGlueRE.FindStringSubmatch(match)
		if len(sub) != 4 {
			return match
		}
		label, digits, street := sub[1], sub[2], sub[3]
		stU := strings.ToUpper(street)
		if localStreetSuffixes[stU] || suffixlessStreetStopwords[stU] || screenNumberWordNoise[stU] {
			return match
		}
		station, house := pickStationHouseSplit(digits, confirmed)
		if station == "" || house == "" {
			return match
		}
		return label + " " + station + " " + house + " " + street
	})
}

func pickStationHouseSplit(digits string, confirmed map[string]bool) (station, house string) {
	if len(digits) < 4 || len(digits) > 6 || !isAllDigits(digits) {
		return "", ""
	}
	// Longest confirmed station prefix wins (31 before 3).
	bestLen := 0
	for n := 1; n <= 2 && n <= len(digits)-3; n++ {
		st, h := digits[:n], digits[n:]
		if !confirmed[st] || !plausibleFireStationNumber(st) || len(h) < 3 || len(h) > 5 {
			continue
		}
		if n > bestLen {
			bestLen = n
			station, house = st, h
		}
	}
	if station != "" {
		return station, house
	}
	// Default: 2-digit station + 3–4 digit house, else 1-digit + 3–5 digit house.
	for _, n := range []int{2, 1} {
		if n > len(digits)-3 {
			continue
		}
		st, h := digits[:n], digits[n:]
		if !plausibleFireStationNumber(st) {
			continue
		}
		if n == 2 && (len(h) < 3 || len(h) > 4) {
			continue
		}
		if n == 1 && (len(h) < 3 || len(h) > 5) {
			continue
		}
		return st, h
	}
	return "", ""
}

// haversineMeters returns the distance in meters between two WGS-84 coordinates.
func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000
	p1 := lat1 * math.Pi / 180
	p2 := lat2 * math.Pi / 180
	dp := (lat2 - lat1) * math.Pi / 180
	dl := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
