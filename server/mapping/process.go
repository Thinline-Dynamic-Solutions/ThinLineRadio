// Copyright (C) 2025 Thinline Dynamic Solutions

package mapping

import (
	"fmt"
	"regexp"
	"strings"
)

// ProcessInput holds everything needed to extract and geocode one dispatch transcript.
type ProcessInput struct {
	Transcript    string
	ToneSetLabel  string
	OpenAIKey     string
	OpenAIModel   string
	Scope         *ScopeData
	Geo           *GeoOptions
	CoverageAddress string
	CoverageNature  string
	NatureCodes     []string
	// NatureMatchTerms includes configured phrases plus labels for transcript
	// matching. When empty, NatureCodes is used for matching.
	NatureMatchTerms         []string
	NaturePhraseToLabel      map[string]string
	CallNatureOpenAIClassify bool
	// SuppressUnknownNaturePins drops map pins for catch-all unknown natures
	// (UNKNOWN PROBLEM, etc.) after classification.
	SuppressUnknownNaturePins bool
	ForceCoverage             bool
	// Engine is retained for interface stability with existing callers but is
	// no longer branched on — extraction is always the local rule-based
	// identifier (digit address candidate); street pins are placed by
	// Nominatim in incident_mapping.go. Legacy OpenAI-extraction + Google /
	// Census geocode paths were removed.
	Engine string
}

// effectiveMatchTerms returns phrase-aware match terms when configured.
func effectiveMatchTerms(labels, matchTerms []string) []string {
	if len(matchTerms) > 0 {
		return matchTerms
	}
	return labels
}

func extractForLocalEngine(in ProcessInput, cleaned string) (*CuratedAlert, []*CuratedAlert) {
	c, e := ExtractLocal(cleaned, in.ToneSetLabel, in.Scope, effectiveMatchTerms(in.NatureCodes, in.NatureMatchTerms))
	return sanitizeExtractBatch(c, e, cleaned, in.Scope)
}

func sanitizeExtractBatch(curated *CuratedAlert, extras []*CuratedAlert, cleaned string, scope *ScopeData) (*CuratedAlert, []*CuratedAlert) {
	if curated != nil {
		curated.Address = SanitizeEnginePrefixedAddress(curated.Address, cleaned)
		curated.Address = stripGluedQuadrantWhenCommaSpoken(curated.Address, cleaned)
		SanitizeExtractedAddress(curated)
		ApplyExtractGuards(curated, cleaned, scope)
	}
	for _, ex := range extras {
		if ex != nil {
			ex.Address = SanitizeEnginePrefixedAddress(ex.Address, cleaned)
			ex.Address = stripGluedQuadrantWhenCommaSpoken(ex.Address, cleaned)
			SanitizeExtractedAddress(ex)
			ApplyExtractGuards(ex, cleaned, scope)
		}
	}
	return curated, extras
}

// ProcessOutput is the geocoding pipeline result.
type ProcessOutput struct {
	Primary  *CuratedAlert
	Extras   []*CuratedAlert
	Status   string // geocoded|local|cache|failed|skipped|coverage|approximate
	Source   string
	Query    string
	Cleaned  string
	// Approximate is set for limited-access highway calls that could not be
	// anchored to a cross street — the pin (if any) is a rough highway point,
	// not a precise location.
	Approximate bool
}

// dispatchToCommaSplitHouseRE matches STT that splits one house number across a
// comma after apparatus routing ("ENGINE 5 TO 26, 29 SOUTH STREET" → 2629).
var dispatchToCommaSplitHouseRE = regexp.MustCompile(`(?i)\bTO\s+(\d{1,3}),\s*(\d{2,4})\s+((?:SOUTH|NORTH|EAST|WEST)\s+)?([A-Z][A-Z\s'\-]{2,40}?\s+(?:ST|STREET|RD|ROAD|AVE|AVENUE|DR|DRIVE|LN|LANE|BLVD|BOULEVARD|WAY|CIR|CIRCLE|TRL|TRAIL))\b`)

// mergeDispatchToCommaSplitHouse collapses "TO 26, 29 SOUTH STREET" → "2629 SOUTH STREET".
func mergeDispatchToCommaSplitHouse(transcript string) string {
	return dispatchToCommaSplitHouseRE.ReplaceAllStringFunc(transcript, func(m string) string {
		sub := dispatchToCommaSplitHouseRE.FindStringSubmatch(m)
		if len(sub) != 5 {
			return m
		}
		g1, g2 := strings.TrimSpace(sub[1]), strings.TrimSpace(sub[2])
		if g1 == "" || g2 == "" || len(g1)+len(g2) < 3 || len(g1)+len(g2) > 6 {
			return m
		}
		dir := strings.TrimSpace(sub[3])
		street := strings.TrimSpace(sub[4])
		house := g1 + g2
		if dir != "" {
			return house + " " + dir + street
		}
		return house + " " + street
	})
}

// PreCleanTranscript normalizes STT artifacts before extraction.
// Plain-text normalization (no punctuation; letter↔digit spaces) runs first so
// every downstream rule sees the same shape as keyword alerts and geocoding.
func PreCleanTranscript(transcript string) string {
	transcript = NormalizeTranscriptPlainText(transcript)
	return normalizeSpokenOrdinalTokens(
		expandSpokenStateRoutesInDispatchTranscript(
			mergeDispatchToCommaSplitHouse(
				expandBareStreetBeforeFacility(
					expandSpelledDirectionalStreetInDispatchTranscript(
						joinSttCommaPausedCompoundStreets(
							expandHyphenatedCompoundStreetNames(
								expandBareNumberedStreetOrdinal(
									expandHouseLeadingSaintAbbreviation(
										expandHyphenHouseNumbersInDispatchTranscript(
											collapseSpokenDigitChainsInDispatchTranscript(
												unglueStationPrefixedHouseNumber(
													deduplicateConcatenatedNumber(transcript)))))))))))))
}

func applyCallNatureAssignment(curated *CuratedAlert, in ProcessInput, cleaned string, allowOpenAI bool) {
	if curated == nil {
		return
	}
	matchTerms := effectiveMatchTerms(in.NatureCodes, in.NatureMatchTerms)
	isDispatch := strings.TrimSpace(curated.NatureDesc) != ""
	infer := shouldInferNatureFromTranscript(cleaned, isDispatch)
	useOpenAI := allowOpenAI && shouldUseOpenAINatureClassify(in)

	// Pass 1: phrase / rule inference without assigning UNKNOWN PROBLEM yet.
	curated.NatureDesc = FinalizeIncidentNatureWithMap(
		curated.NatureDesc, cleaned, in.NatureCodes, matchTerms, infer, in.NaturePhraseToLabel, true,
	)
	// OpenAI judges the whole transcript and may override a phrase hit
	// (e.g. SHOOT→SHOTS FIRED on a threat-only dispatch). Intentional empty
	// clears a bad phrase hit; transport/parse failure keeps phrase/rules.
	openAIAnswered := false
	if useOpenAI {
		pick, answered := ClassifyCallNatureWithOpenAIResult(in.OpenAIKey, in.OpenAIModel, cleaned, in.NatureCodes)
		if answered {
			openAIAnswered = true
			// Trust the OpenAI pick: already validated against the configured
			// label list. Do NOT re-run through the phrase-anchor gate —
			// that requires a configured phrase literally in the transcript
			// and discards paraphrases ("double hernia" → ABDOMINAL PAIN).
			if pick == "" {
				curated.NatureDesc = ""
			} else {
				curated.NatureDesc = normalizeOpenAINaturePick(pick, in.NatureCodes, in.NaturePhraseToLabel)
			}
		}
	}
	// Skip phrase UNKNOWN fallback when OpenAI intentionally left nature blank
	// (chatter / non-incident). Re-running Finalize would restore bad phrase hits.
	if !openAIAnswered && strings.TrimSpace(curated.NatureDesc) == "" {
		curated.NatureDesc = FinalizeIncidentNatureWithMap(
			"", cleaned, in.NatureCodes, matchTerms, infer, in.NaturePhraseToLabel, false,
		)
	}
	curated.NatureDesc = SanitizeCallNatureAssignment(cleaned, curated.NatureDesc, in.NatureCodes)
}

func shouldUseOpenAINatureClassify(in ProcessInput) bool {
	return in.CallNatureOpenAIClassify &&
		strings.TrimSpace(in.OpenAIKey) != "" &&
		len(in.NatureCodes) > 0
}

// normalizeOpenAINaturePick maps an OpenAI-selected label to its canonical
// configured form without applying the transcript phrase-anchor gate. The pick
// was already constrained to the configured label set by the classifier, so it
// only needs canonicalization and clamping — never evidence re-checking.
func normalizeOpenAINaturePick(pick string, natureCodes []string, phraseToLabel map[string]string) string {
	n := CanonicalizeNatureLabel(strings.TrimSpace(pick), phraseToLabel)
	n = NormalizeNatureToKeywords(n, natureCodes)
	n = clampNatureLabel(n, natureCodes)
	if strings.TrimSpace(n) == "" || IsDefaultUnknownNatureLabel(n) {
		return ""
	}
	return n
}

// ResolveIncidentNature classifies a dispatch transcript into a call-nature label
// using phrase rules and optional OpenAI fallback (same path as Process).
func ResolveIncidentNature(transcript string, in ProcessInput) string {
	curated := &CuratedAlert{}
	applyCallNatureAssignment(curated, in, PreCleanTranscript(transcript), true)
	return strings.TrimSpace(curated.NatureDesc)
}

// Process runs extract → corrections → known places → forward geocode → cross-street sanity.
func Process(in ProcessInput) ProcessOutput {
	out := ProcessOutput{Status: "skipped"}
	if strings.TrimSpace(in.Transcript) == "" {
		return out
	}
	if in.Scope == nil {
		in.Scope = &ScopeData{}
	}
	if in.Geo == nil {
		in.Geo = &GeoOptions{}
	}

	cleaned := PreCleanTranscript(in.Transcript)
	if maGeo := ApplyMutualAidGeoOverride(in.Geo, cleaned); maGeo != nil {
		in.Geo = maGeo
	}
	out.Cleaned = cleaned

	coverageEligible := in.ForceCoverage ||
		(strings.TrimSpace(in.CoverageAddress) != "" && TranscriptIsPureStationCoverage(cleaned))

	// Cost pre-screen: skip extraction when the transcript has no detectable
	// location signal. Such a call can only resolve to "skipped" downstream.
	// Coverage calls are exempt — their address comes from configuration.
	if !coverageEligible && !TranscriptLikelyHasLocation(cleaned, in.Scope) &&
		!TranscriptIsPeerAgencySquadRequest(cleaned) {
		curated := RawFallback(cleaned, in.ToneSetLabel)
		// No location signal at all means this call can only ever end up
		// "skipped" — never geocoded, never pinned. Phrase/rule nature
		// matching still runs (cheap, local), but the OpenAI fallback is
		// skipped: it's pure cost for what is almost always non-dispatch
		// chatter (status checks, acknowledgements, unit traffic), and was
		// a major contributor to per-call OpenAI load during high-volume
		// bursts even though most of these calls have zero mapping value.
		applyCallNatureAssignment(curated, in, cleaned, false)
		out.Primary = curated
		out.Status = "skipped"
		return out
	}

	curated, extras := extractForLocalEngine(in, cleaned)
	if curated == nil {
		curated = RawFallback(cleaned, in.ToneSetLabel)
	}

	applyCallNatureAssignment(curated, in, cleaned, true)

	curated.Address = normalizeHyphenatedHousePrefix(curated.Address)
	curated.Address = SanitizeEnginePrefixedAddress(curated.Address, cleaned)
	curated.Address = ApplyCorrections(in.Scope, curated.Address)
	curated.Address = ApplyFuzzyGazetteerStreetCorrection(curated.Address, in.Scope)
	curated.Address = CorrectAddressPhraseASR(curated.Address)
	curated.CrossStreet1 = ApplyCorrections(in.Scope, curated.CrossStreet1)
	curated.CrossStreet1 = ApplyFuzzyGazetteerStreetCorrection(curated.CrossStreet1, in.Scope)
	curated.CrossStreet2 = ApplyCorrections(in.Scope, curated.CrossStreet2)
	curated.CrossStreet2 = ApplyFuzzyGazetteerStreetCorrection(curated.CrossStreet2, in.Scope)
	curated.Notes = ApplyCorrections(in.Scope, curated.Notes)
	SanitizeExtractedAddress(curated)

	// Limited-access highway references (turnpike/interstate/freeway) are not
	// addressable points. Strip the travel direction, normalize the route, and
	// qualify a bare "TURNPIKE" with the agency's state so it resolves to the
	// real toll road. The actual pin comes from the cross-street intersection
	// below when one was named.
	highwayRef := false
	if norm, isHwy := normalizeHighwayAddress(curated.Address, in.Geo.State); isHwy {
		curated.Address = norm
		highwayRef = true
	}

	// Radio chatter mis-extracted as an address (e.g. "YOU CAN SHOW MYSELF &
	// SR 32" from "you can show myself and 32 en route…"). Drop it so we don't
	// geocode and pin a non-location; the call falls through to "skipped".
	if AddressIsConversationalNoise(curated.Address) {
		curated.Address = ""
		curated.CrossStreet1 = ""
		curated.CrossStreet2 = ""
		highwayRef = false
	}

	// Officer self-narrative (e.g. "Bradley out, check's okay… I'll head over to
	// Porter Road and check on this deer"). The street is real but it's only
	// mentioned as where the unit is going, not a dispatched incident. Require a
	// missing house number so we never suppress a real "123 Main St" dispatch.
	ApplyExtractedAddressGuards(curated, cleaned, in.Scope)
	curated.Address = appendSpokenTrailingDirectional(curated.Address, cleaned)
	curated.Address = alignAddressLeadingQualifiersFromTranscript(curated.Address, cleaned)
	curated.Address = alignAddressSuffixFromTranscript(curated.Address, cleaned)
	curated.Address = alignAddressStreetFromTranscript(curated.Address, cleaned, in.Scope)
	if strings.TrimSpace(curated.Address) == "" {
		highwayRef = false
	}
	DropUnimportAnchoredAddress(curated, cleaned, in.Scope)
	if strings.TrimSpace(curated.Address) == "" {
		highwayRef = false
	}

	forceCoverage := in.ForceCoverage
	if !forceCoverage && strings.TrimSpace(in.CoverageAddress) != "" &&
		TranscriptIsPureStationCoverage(cleaned) {
		forceCoverage = true
	}

	if forceCoverage && strings.TrimSpace(in.CoverageAddress) != "" {
		curated.Address = strings.ToUpper(strings.TrimSpace(in.CoverageAddress))
		nature := strings.TrimSpace(in.CoverageNature)
		if nature == "" {
			nature = "OFF DUTY COVERAGE"
		}
		curated.NatureDesc = strings.ToUpper(nature)
		curated.CommonName = ""
		curated.CrossStreet1 = ""
		curated.CrossStreet2 = ""
		curated.AptUnit = ""
		curated.Lat = ""
		curated.Lon = ""
		out.Primary = curated
		out.Extras = extras
		out.Status = "coverage"
		return out
	}

	// No validated card address yet. Use "failed" (not "skipped") so
	// incident_mapping can still POST the full transcript to nominatim-gateway —
	// the gateway recovers house+street from dispatch text when local extract
	// or guards wiped the card. Reserve "skipped" for the no-location pre-screen
	// above (and coverage / empty-transcript paths elsewhere).
	if strings.TrimSpace(curated.Address) == "" {
		curated.Lat = ""
		curated.Lon = ""
		curated.CommonName = ""
		out.Primary = curated
		out.Extras = extras
		out.Status = "failed"
		return out
	}

	// Rule-based cross streets fill gaps left by OpenAI extraction or RawFallback.
	if curated.CrossStreet1 == "" || curated.CrossStreet2 == "" {
		ruleCS1, ruleCS2 := localCrossStreets(strings.ToUpper(cleaned), in.Scope)
		if curated.CrossStreet1 == "" && ruleCS1 != "" {
			curated.CrossStreet1 = ruleCS1
		}
		if curated.CrossStreet2 == "" && ruleCS2 != "" {
			curated.CrossStreet2 = ruleCS2
		}
	}

	// Identify-only here: extract+guards produced a digit address candidate.
	// Nominatim (incident_mapping.go) places the street pin. Known-place POI
	// coords are the only non-Nominatim pin applied inside Process.
	source, status, approximate := resolveLocalWithFallbacks(in, curated, cleaned)
	out.Primary = curated
	out.Extras = extras
	switch {
	case status != "":
		out.Source = source
		out.Status = status
		out.Approximate = approximate
	case curated.Lat != "" && curated.Lon != "":
		out.Status = "geocoded"
	default:
		out.Status = "failed"
	}
	// A highway reference we could not anchor to a cross-street intersection
	// is, at best, a rough point on a long route — flag it.
	if highwayRef && source != "local_intersection" && curated.Lat != "" && curated.Lon != "" {
		out.Approximate = true
		out.Status = "approximate"
	}
	clearLocalPinOutsideCoverage(curated, in.Geo)
	// Street identity after Nominatim is owned by nominatim-gateway — do not
	// re-veto pins here with TLR STT contradiction heuristics.
	ClearPinWhenAddressNotImportAnchored(curated, in.Scope)
	if in.SuppressUnknownNaturePins {
		suppressUnclassifiedPin(&out)
	}
	if curated.Lat == "" && curated.Lon == "" && out.Status != "skipped" && strings.TrimSpace(curated.Address) != "" {
		out.Status = "failed"
	}
	return out
}

// suppressUnclassifiedPin drops the map pin for catch-all unknown-problem calls.
// A call we could not classify to a real nature is low-confidence: keeping the
// pin risks force-fitting garbled or non-dispatch audio onto the nearest known
// street. The call, transcript, and nature label are preserved — only the map
// coordinates are cleared. Reclassifying such a call to a real nature (via
// phrase rules or OpenAI) restores its pin on the next mapping pass.
func suppressUnclassifiedPin(out *ProcessOutput) {
	if out == nil || out.Primary == nil {
		return
	}
	SuppressUnclassifiedPinOnAlert(out.Primary, &out.Status)
}

// ClearIncidentLocationUnlessGeocoded drops address and coordinates when no pin
// could be resolved. Ungeocoded calls must not persist as mapped incidents.
func ClearIncidentLocationUnlessGeocoded(curated *CuratedAlert, status *string) {
	if curated == nil {
		return
	}
	if status != nil && *status == "coverage" {
		return
	}
	var lat, lon float64
	fmt.Sscanf(curated.Lat, "%f", &lat)
	fmt.Sscanf(curated.Lon, "%f", &lon)
	if strings.TrimSpace(curated.Lat) != "" && strings.TrimSpace(curated.Lon) != "" && lat != 0 && lon != 0 {
		return
	}
	curated.Address = ""
	curated.CrossStreet1 = ""
	curated.CrossStreet2 = ""
	curated.CommonName = ""
	curated.AptUnit = ""
	curated.Lat = ""
	curated.Lon = ""
	if status != nil && (*status == "geocoded" || *status == "local" || *status == "approximate") {
		*status = "failed"
	}
}

// SuppressUnclassifiedPinOnAlert clears coordinates for catch-all unknown-problem
// calls. Also invoked from incident_mapping after late geocode fallbacks when
// MappingIntegration.SuppressUnknownNaturePins is enabled. Transcript, address
// text, and nature label are preserved — only the map pin is dropped.
//
// Blank natures are treated the same as UNKNOWN PROBLEM: the incident map UI
// labels empty nature as "UNKNOWN PROBLEM", so leaving those pins would still
// show as unclassified on the map.
func SuppressUnclassifiedPinOnAlert(curated *CuratedAlert, status *string) {
	if curated == nil {
		return
	}
	nature := strings.TrimSpace(curated.NatureDesc)
	if nature != "" && !IsDefaultUnknownNatureLabel(nature) {
		return
	}
	curated.Lat = ""
	curated.Lon = ""
	if status != nil && (*status == "geocoded" || *status == "local" || *status == "approximate" || *status == "cache") {
		*status = "failed"
	}
}

// resolveLocalWithFallbacks no longer pins from local OSM/TIGER geometry.
// Street addresses are identified upstream and geocoded by Nominatim in
// incident_mapping.go. This step only lightly aligns the candidate from the
// transcript and applies known-place POI pins when there is no street address.
func resolveLocalWithFallbacks(in ProcessInput, curated *CuratedAlert, cleaned string) (source, status string, approximate bool) {
	if curated == nil {
		return "", "", false
	}
	if strings.TrimSpace(curated.Address) != "" {
		curated.Address = applyHouseStateRouteFromTranscript(curated.Address, cleaned)
		curated.Address = alignAddressStreetFromTranscript(curated.Address, cleaned, in.Scope)
		curated.Address = alignAddressSuffixFromTranscript(curated.Address, cleaned)
		// Digit address candidates go to Nominatim — do not invent a local pin.
		if AddressCandidateHasDigit(curated.Address) {
			curated.Lat = ""
			curated.Lon = ""
			return "", "", false
		}
	}

	if applied, _ := applyKnownPlaceCoords(in.Scope, curated, false, in.Geo); applied {
		if !finalizeLocalCoords(curated, in.Geo) {
			curated.Lat = ""
			curated.Lon = ""
			return "", "", false
		}
		return "known_place", "local", false
	}
	// Do not snap to a transcript word/POI when the extracted address was rejected
	// or never aligned with what dispatch actually said.
	addrOK := strings.TrimSpace(curated.Address) == "" ||
		AddressAlignsWithTranscript(curated.Address, cleaned, in.Scope)
	if TranscriptQualifiesForKnownPlacePin(cleaned) && addrOK {
		house, street := splitHouseAndStreet(strings.TrimSpace(curated.Address))
		allowTranscriptPlace := house == "" || street == ""
		addrU := strings.ToUpper(strings.TrimSpace(curated.Address))
		if strings.Contains(addrU, "&") || strings.Contains(addrU, " AND ") || strings.HasPrefix(addrU, "AREA OF ") {
			allowTranscriptPlace = false
		}
		if allowTranscriptPlace {
			if applied, _ := applyKnownPlaceFromTranscript(in.Scope, curated, cleaned); applied &&
				curated.Lat != "" && curated.Lon != "" {
				if !finalizeLocalCoords(curated, in.Geo) {
					curated.Lat = ""
					curated.Lon = ""
					return "", "", false
				}
				return "known_place", "local", false
			}
		}
	}
	return "", "", false
}
