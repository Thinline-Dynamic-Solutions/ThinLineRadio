// Copyright (C) 2025 Thinline Dynamic Solutions
//
// llm_guards.go — precision gates for the rules-based local (OSM) mapping
// engine. Strips invented or chatter-derived addresses while preserving call
// nature when appropriate.

package mapping

import (
	"regexp"
	"strings"
)

// radioChatterMarkers are phrasings that indicate the transcript is unit
// traffic, a test, or a sign-off — not a dispatched incident with a location.
var radioChatterMarkers = []string{
	"YOU GOT ", "GOOD TEST", "GOOD AFTERNOON", "GOOD MORNING", "GOOD NIGHT",
	"DISREGARD", "10-4", "10 4", "COPY THAT", "THANK YOU", "YOU CAN DISREGARD",
	"POLLING NEWS", "BREAK FOR A SECOND", "BREAK ROOM",
	"ARRIVING ", "CHECK YOUR STRETCH OF ", "STRETCH OF ",
	"SCENE EASE", "CLEAR RETURN", "CLEAR WITH ", "MILEAGE ",
	"RUN THIS SOCIAL", "SECRET INDICTMENT", "TIED UP WITH ",
	"SAME TRAFFIC", "I'LL GET THAT", "EN ROUTE TO OFFICIAL", "ON SCENE REQUESTING",
	"SHOW YOU NOW", "HOLD ON", "THAT ONE", "WE'RE AT ST", "WE'RE AT ST.",
	"OPEN MIC", "CODE 6", "NO NEED FOR CHECK", "PATIENTS LOADING", "SERVICE RETURNING",
	"WALK-OFF", "WALK OFF", "GENTLEMAN IN THE LOBBY", "CAN'T FIND A METER",
	"CAN YOU SEND ME A PICTURE", "WHEELS LT OUT OF",
}

// adminLocationReferenceMarkers are phrasings that mention street-like words in
// administrative context (printouts, criminal-history checks) — not a dispatch
// location to geocode.
var adminLocationReferenceMarkers = []string{
	"PRINTOUT ", "PRINT OUT ",
	" IT WAS CCH ", " CCH ON ",
	" RUNNING ORI ", " ORI ON ",
	" WARRANT CHECK ", " BOLO ON ",
}

// TranscriptIsAdministrativeLocationReference reports when the transcript is
// discussing paperwork/history about a person or place, not toning units to an
// address ("PRINTOUT SCOTT AND LAUREN, IT WAS CCH ON LAUREN FOR DV").
func TranscriptIsAdministrativeLocationReference(transcript string) bool {
	if strings.TrimSpace(transcript) == "" {
		return false
	}
	u := " " + strings.ToUpper(transcript) + " "
	for _, m := range adminLocationReferenceMarkers {
		if strings.Contains(u, m) {
			return true
		}
	}
	return false
}

// addressByRouteSuffixRE strips junk like "1701 FAIRVIEW BY 33" where the
// trailing route fragment is not part of the street address.
var addressByRouteSuffixRE = regexp.MustCompile(`(?i)\s+BY\s+(?:SR|US|CR|RT|RTE|ROUTE|INTERSTATE|I[\s-]?)\s*\d{1,3}\s*$`)

// stationDispatchRE matches toned dispatch openers ("STATION 43 FOR A SQUAD CALL").
var stationDispatchRE = regexp.MustCompile(`(?i)\bSTATION\s+\d{1,3}\b.*\b(FOR|RESPOND|SQUAD|FIRE|MEDIC|ALARM)\b`)

// TranscriptIsTonedDispatch reports whether the transcript is a unit toned or
// dispatched to a new incident with a describable situation (replaces LLM
// is_dispatch).
func TranscriptIsTonedDispatch(transcript string) bool {
	if strings.TrimSpace(transcript) == "" {
		return false
	}
	if TranscriptIsRadioChatter(transcript) {
		return false
	}
	if TranscriptIsDispatchResourceStatusChatter(transcript) {
		return false
	}
	u := " " + strings.ToUpper(transcript) + " "
	// "I'M GOING TO MAKE SURE …" must not cancel a restated house-number dispatch.
	if TranscriptIsOfficerNarrative(transcript) && !containsRealIncidentMarker(u) &&
		!transcriptHasCleanNumberedDispatchAddress(transcript) {
		return false
	}
	if containsRealIncidentMarker(u) {
		return true
	}
	if stationDispatchRE.MatchString(transcript) {
		return true
	}
	return shouldInferNatureFromTranscript(transcript, false)
}

// ApplyExtractGuards clears extracted location fields when the transcript is
// not a toned dispatch or the address fails anchor/alignment checks.
func ApplyExtractGuards(curated *CuratedAlert, transcript string, scope *ScopeData) *CuratedAlert {
	if curated == nil {
		return curated
	}
	if !TranscriptIsTonedDispatch(transcript) || TranscriptIsPhoneticAlphabetRollCall(transcript) {
		clearExtractAddressFields(curated)
		return curated
	}
	if strings.TrimSpace(curated.Address) != "" {
		maybePreferTranscriptStreetVariant(curated, transcript, scope)
		anchored := AddressHasGeocodableAnchor(curated.Address, scope) ||
			addressConfirmedByRepeatedDispatchFraming(curated.Address, transcript) ||
			addressIsStrongSpokenDispatch(curated.Address, transcript, scope)
		if !anchored || !AddressAlignsWithTranscript(curated.Address, transcript, scope) {
			clearExtractAddressFields(curated)
		}
	}
	return curated
}

// addressConfirmedByRepeatedDispatchFraming reports whether the transcript
// itself supplies enough repetition/framing confidence to accept a short
// house number + suffixless single-word street ("604 FENTON" from "604
// FENTON, 604 FENTON" or "12 SCOTT" from "THE NUMBER 12, 12 SCOTT") that
// AddressHasGeocodableAnchor would otherwise reject as too ambiguous without
// gazetteer confirmation. A dispatcher restating the same house+street pair
// back to back — with or without an explicit "NUMBER" preamble — is itself
// strong location confidence, as strong as a spelled thoroughfare suffix, so
// the bare form is let through and left for the external geocoder to resolve
// the missing suffix (e.g. "Fenton Rd") rather than guessing it locally.
func addressConfirmedByRepeatedDispatchFraming(addr, transcript string) bool {
	house, street := splitHouseAndStreet(strings.ToUpper(strings.TrimSpace(addr)))
	if house == "" || street == "" || len(strings.Fields(street)) != 1 {
		return false
	}
	up := strings.ToUpper(transcript)
	for _, m := range transcriptNumberFramedRepeatedHouseRE.FindAllStringSubmatch(up, -1) {
		if len(m) < 4 || m[1] != m[2] {
			continue
		}
		if m[1] == house && strings.EqualFold(strings.TrimSpace(m[3]), street) {
			return true
		}
	}
	// Plain restatement ("604 FENTON, 604 FENTON, NEED FOR A..."): normalize
	// away punctuation so a comma between the two mentions doesn't block the
	// match, then check the exact house+street pair occurs twice.
	norm := strings.Join(strings.FieldsFunc(up, func(r rune) bool {
		return r == ',' || r == '.' || r == ';'
	}), " ")
	norm = strings.Join(strings.Fields(norm), " ")
	if strings.Count(norm, house+" "+street) >= 2 {
		return true
	}
	return false
}

// SanitizeExtractedAddress normalizes LLM/STT address fields before geocoding.
func SanitizeExtractedAddress(curated *CuratedAlert) {
	if curated == nil {
		return
	}
	curated.Address = sanitizeAddressField(curated.Address)
	curated.CrossStreet1 = sanitizeAddressField(curated.CrossStreet1)
	curated.CrossStreet2 = sanitizeAddressField(curated.CrossStreet2)
}

func sanitizeAddressField(addr string) string {
	addr = strings.TrimSpace(strings.ToUpper(addr))
	if addr == "" {
		return ""
	}
	if idx := strings.Index(addr, " BY "); idx > 0 {
		addr = strings.TrimSpace(addr[:idx])
	}
	addr = addressByRouteSuffixRE.ReplaceAllString(addr, "")
	addr = dispatchStreetCutAtConnector(addr)
	if strings.Contains(addr, "&") {
		a, b := splitIntersectionQuery(addr)
		a = stripCrossStreetCaptureNoise(a)
		b = stripCrossStreetCaptureNoise(b)
		if a != "" && b != "" {
			addr = a + " & " + b
		}
	} else {
		addr = stripCrossStreetCaptureNoise(addr)
	}
	addr = cutAddressAtNarrativeBreak(addr)
	addr = stripOrdinalStreetFiller(addr)
	addr = dedupeTrailingStreetSuffix(addr)
	addr = stripFacilityPhraseTail(addr)
	addr = stripAddressNarrativeTail(addr)
	return strings.TrimSpace(addr)
}

// stripFacilityPhraseTail removes glued facility / CAD clauses after a street
// stem so geocode anchors stay "1501 TIBBETTS WICK" not "… ASSISTED LIVING".
func stripFacilityPhraseTail(addr string) string {
	u := strings.TrimSpace(strings.ToUpper(addr))
	if u == "" {
		return addr
	}
	for _, phrase := range facilityPhraseTails {
		if idx := strings.Index(u, phrase); idx > 0 {
			u = strings.TrimSpace(u[:idx])
		}
	}
	return u
}

// cutAddressAtNarrativeBreak truncates an extract at the first sentence break —
// a token carrying question/exclamation punctuation or a narrative contraction
// ("19 SPRING RUN? I'M EN BLVD" → "19 SPRING RUN").
func cutAddressAtNarrativeBreak(addr string) string {
	fields := strings.Fields(strings.TrimSpace(addr))
	for i, f := range fields {
		if tokenIsNarrativeContraction(f) {
			return strings.Join(fields[:i], " ")
		}
		if strings.ContainsAny(f, "?!") {
			kept := append([]string{}, fields[:i]...)
			if tok := strings.Trim(f, "?!.,;:"); tok != "" {
				kept = append(kept, tok)
			}
			return strings.Join(kept, " ")
		}
	}
	return addr
}

// stripAddressNarrativeTail removes sentence-start words glued onto a street
// extract ("12018 BROOKLAWN THERE'S" → "12018 BROOKLAWN").
func stripAddressNarrativeTail(addr string) string {
	fields := strings.Fields(strings.TrimSpace(addr))
	if len(fields) < 2 {
		return addr
	}
	// First field is often the house number — only strip narrative from street tail.
	start := 0
	if len(fields) > 0 && isAllDigits(fields[0]) {
		start = 1
	}
	for len(fields) > start+1 && addressNarrativeTailTokens[fields[len(fields)-1]] {
		fields = fields[:len(fields)-1]
	}
	return strings.Join(fields, " ")
}

var addressNarrativeTailTokens = map[string]bool{
	"THERE'S": true, "THERES": true, "THERE": true, "THEY'RE": true, "THEYRE": true,
	"SHE'S": true, "SHES": true, "HE'S": true, "HES": true, "IT'S": true, "ITS": true,
	"WE'RE": true, "WERE": true, "WHO'S": true, "WHOS": true, "WHAT'S": true, "WHATS": true,
	"WITH": true, "FOR": true, "AND": true, "BUT": true, "WHO": true, "SHE": true,
	"POSSIBLY": true, "PROBABLY": true, "SICK": true, "INJURED": true,
	"IN": true,
	// Facility / CAD clause glue left after STT address harvest.
	"ASSISTED": true, "LIVING": true, "SKILLED": true, "NURSING": true,
	"CONTACT": true, "ATTEMPT": true, "NUMBER": true, "HEALTH": true, "CARE": true,
	"BETWEEN": true, "WILL": true,
}

// facilityPhraseTails are multi-word clauses glued onto a harvested street
// ("1501 TIBBETTS WICK ASSISTED LIVING", "65 WEST WALNUT NO CONTACT").
var facilityPhraseTails = []string{
	" ASSISTED LIVING", " SKILLED NURSING", " NURSING HOME", " SENIOR LIVING",
	" NO CONTACT", " ATTEMPT TO", " HEALTH CARE", " HEALTHCARE", " HEALTH CENTER",
}

// dispatchStreetCutAtConnector strips cross-street / location tails glued onto a
// house+street extract ("1898 WICK CAMPBELL BETWEEN CATAWPA LANE" → "1898 WICK CAMPBELL").
// dispatchStreetLeadingSelfLocationMarkers are phrases a unit uses to report
// its own location ("41, 42 IN THE AREA OF ..."), not a house-number lead-in.
// When one of these opens the captured "street" text, the number that
// preceded it in the transcript is a unit/station designator, not a house
// number, and the real location (if any) follows the marker.
var dispatchStreetLeadingSelfLocationMarkers = []string{
	"IN THE AREA OF ", "IN THE VICINITY OF ", "IN THE AREA ", "NEAR THE AREA OF ",
}

func dispatchStreetHasLeadingSelfLocationMarker(street string) bool {
	u := strings.ToUpper(strings.TrimSpace(street))
	for _, m := range dispatchStreetLeadingSelfLocationMarkers {
		if strings.HasPrefix(u, m) {
			return true
		}
	}
	return false
}

func dispatchStreetCutAtConnector(street string) string {
	st := strings.TrimSpace(strings.ToUpper(street))
	if st == "" {
		return ""
	}
	for _, sep := range []string{
		" IN THE ", " BETWEEN ", " NEAR ", " BY THE ", " AT THE ", " JUST ", " RIGHT ",
		" CROSS STREET", " CROSSROADS OF ", " CROSSROADS ", " CROSSES OF ", " CROSSES ",
		" CROSS OF ", " CROSS ",
		" LOT ", " SPACE ", " APT ", " APARTMENT ", " UNIT ", " TRAILER ",
		" THERE'S ", " THERE IS ", " THERE ARE ", " THERE WAS ", " THERE WERE ",
		" WITH A ", " WITH THIS ", " WITH THE ", " WITH ", " FOR A ", " WHEN YOU ",
		// Unit radio procedure ("3847 AND 50, CAN YOU START HEADING…") — never street.
		" CAN YOU ", " START HEADING ", " HEADING OVER ",
		// "9715 CLINTON OVER AT TRIAD ENGINEERING" — OVER AT is location phrasing.
		" OVER AT ", " OVER BY ", " OVER NEAR ",
		" POSSIBLY ", " PROBABLY ",
	} {
		if idx := strings.Index(st, sep); idx > 0 {
			st = strings.TrimSpace(st[:idx])
		}
	}
	return st
}

// ApplyExtractedAddressGuards clears bogus addresses from any extractor when
// the transcript context shows the location is not a dispatched incident pin.
func ApplyExtractedAddressGuards(curated *CuratedAlert, transcript string, scope *ScopeData) {
	if curated == nil || strings.TrimSpace(curated.Address) == "" {
		return
	}
	// Cut CROSSROADS OF / CROSSES OF / narrative connectors before anchor and
	// conflict checks — otherwise "3807 CUMBERLAND CROSSROADS OF … CIRCLE"
	// fails geocode and can look like a different street than the spoken stem.
	curated.Address = sanitizeAddressField(curated.Address)
	if strings.TrimSpace(curated.Address) == "" {
		return
	}
	sanitizeFacilityCommonName(curated, transcript)
	maybeSalvageInTheFacility(curated, transcript)
	maybeAppendSpokenStreetSuffix(curated, transcript)
	maybeEnrichIntersectionSidesFromTranscript(curated, transcript)
	maybePreferTranscriptStreetVariant(curated, transcript, scope)
	maybeExpandTruncatedStreetFromTranscript(curated, transcript)
	maybeSalvageMissingDispatchHouse(curated, transcript)
	maybeSalvageMalformedAddress(curated, transcript, scope)
	maybeStripSquadCallReferenceHouse(curated, transcript)
	maybeStripBareStationHouse(curated, transcript)
	maybeSalvagePatientNameStreet(curated, transcript, scope)
	maybeSalvageApparatusUnitStreet(curated, transcript, scope)
	if strings.TrimSpace(curated.Address) == "" {
		return
	}
	// Spoken house+thoroughfare is a toned location. Do not wipe it for
	// clinical/chatter transcript heuristics — those still apply when the
	// extract is ambiguous or house-less.
	strongSpoken := addressIsStrongSpokenDispatch(curated.Address, transcript, scope)
	if AddressIsUnitPersonIdentifier(curated.Address, transcript) ||
		AddressIsPhoneticUnitCrimeMisextract(curated.Address, transcript) ||
		TranscriptIsPhoneticUnitCrimeCode(transcript) ||
		AddressIsStandByStreetMisSnap(curated.Address, transcript) ||
		AddressIsLicensePlate(curated.Address, transcript) ||
		AddressHouseIsLicenseNumberFragment(curated.Address, transcript) ||
		AddressIsFacilityGateReference(curated.Address, transcript) ||
		AddressIsUnitNavigationChoice(curated.Address, transcript) ||
		AddressIsUnitStandDownOrCancel(curated.Address, transcript) ||
		AddressIsMisreadDecimalOrUnitPair(curated.Address, transcript) ||
		AddressIsConversationalNoise(curated.Address) ||
		AddressStructurallyImplausible(curated.Address) ||
		AddressIsHighwayStretchOnly(curated.Address, transcript) ||
		AddressIsUnitMileageReference(curated.Address, transcript) ||
		AddressIsUnitWillBeStatusMisextract(curated.Address, transcript) ||
		AddressIsUnitRadioSignOffMisextract(curated.Address, transcript) ||
		AddressIsVehicleYearMisextract(curated.Address, transcript) ||
		AddressIsVehicleDescription(curated.Address, transcript) ||
		AddressHasSttJunkStreetTail(curated.Address) ||
		AddressHouseNumberNotSpokenInTranscript(curated.Address, transcript) ||
		(!strongSpoken && AddressIsPlaceNarrativeNotDispatch(curated.Address, transcript)) ||
		(!strongSpoken && AddressConflictsWithDispatchTimestamp(curated.Address, transcript)) ||
		AddressHouseNumberIsRouteReference(curated.Address, transcript) ||
		AddressHouseNumberFollowsEngineBlob(curated.Address, transcript) ||
		AddressHouseNumberIsEngineUnitConcatenation(curated.Address, transcript) ||
		AddressHouseNumberIsBareStationToken(curated.Address, transcript) ||
		AddressHouseNumberIsStationCorridorPOI(curated.Address, transcript) ||
		AddressHouseNumberIsStationNumberConcatenation(curated.Address, transcript) ||
		AddressHouseNumberIsRouteFragment(curated.Address, transcript) ||
		AddressIsBareRouteStemMisextract(curated.Address, transcript) ||
		(!strongSpoken && AddressHouseNumberIsDispatchUnitPrefix(curated.Address, transcript)) ||
		AddressHouseNumberIsBatchRunNumber(curated.Address, transcript) ||
		AddressHouseNumberIsCallsignIdentifier(curated.Address, transcript) ||
		AddressIsDutyOffApparatusLabel(curated.Address, transcript) ||
		AddressIsMunicipalityAgencyName(curated.Address, transcript) ||
		AddressHouseNumberFromPriorityCode(curated.Address, transcript) ||
		AddressHouseNumberFromAge(curated.Address, transcript) ||
		AddressIsJurisdictionAvailabilityMisextract(curated.Address, transcript) ||
		AddressIsPainScaleFragment(curated.Address, transcript) ||
		AddressIsVitalSignFragment(curated.Address, transcript) ||
		(!strongSpoken && AddressMissingSpokenStreetSuffix(curated.Address, transcript)) ||
		(!strongSpoken && TranscriptHasConflictingDispatchAddresses(transcript, scope)) ||
		AddressIsPreambleBeforeSquadCall(curated.Address, transcript) ||
		dispatchAddressIsRequestAdminFragment(curated.Address, transcript) ||
		AddressIsTruncatedStreetVersusTranscript(curated.Address, transcript) ||
		(!strongSpoken && (
			(TranscriptIsAdministrativeLocationReference(transcript) && !containsRealIncidentMarker(" "+strings.ToUpper(transcript)+" ")) ||
				TranscriptIsOfficerLocationStatusUpdate(transcript) ||
				TranscriptIsOfficerSelfReportVisit(transcript) ||
				TranscriptIsDispatchClearSignOff(transcript) ||
				TranscriptIsRoutineUnitLog(transcript) ||
				TranscriptIsTransportNarrative(transcript) ||
				TranscriptIsUnitRouteNavigation(transcript) ||
				TranscriptIsUnitEnRouteStatus(transcript) ||
				TranscriptIsUnitRouteDirective(transcript) ||
				TranscriptIsUnitCoordinationChatter(transcript) ||
				TranscriptIsNonDispatchUnitStatus(transcript) ||
				TranscriptIsWelfareMeetRequest(transcript) ||
				TranscriptIsWelfareCheckInfoUpdate(transcript) ||
				TranscriptIsUtilityTurnOn(transcript) ||
				TranscriptIsPlateOrLookupChatter(transcript) ||
				TranscriptIsLicensePlateReadout(transcript) ||
				TranscriptIsAdvisoryTraffic(transcript) ||
				TranscriptIsPatchInService(transcript) ||
				TranscriptIsUnitOnSiteStatus(transcript) ||
				TranscriptIsMedicEnRouteUpdate(transcript) ||
				TranscriptIsUnitEnRouteBriefing(transcript) ||
				TranscriptIsOnScenePatientUpdate(transcript) ||
				TranscriptBlocksIncidentPin(transcript) ||
				TranscriptIsDispatchResourceStatusChatter(transcript) ||
				TranscriptIsRadioTestAnnouncement(transcript))) {
		clearExtractAddressFields(curated)
		clearNatureIfPlateReadout(curated, transcript)
		return
	}
	if !strongSpoken && TranscriptIsLicensePlateReadout(transcript) {
		clearExtractAddressFields(curated)
		clearNatureIfPlateReadout(curated, transcript)
		return
	}
	if !AddressHasGeocodableAnchor(curated.Address, scope) &&
		!addressConfirmedByRepeatedDispatchFraming(curated.Address, transcript) &&
		!strongSpoken {
		clearExtractAddressFields(curated)
		return
	}
	if !AddressAlignsWithTranscript(curated.Address, transcript, scope) {
		clearExtractAddressFields(curated)
		return
	}
	if house, _ := splitHouseAndStreet(curated.Address); house == "" &&
		!strings.Contains(curated.Address, "&") &&
		TranscriptIsOfficerNarrative(transcript) {
		clearExtractAddressFields(curated)
		return
	}
	_ = scope // reserved for future scope-aware guards
}

// addressIsStrongSpokenDispatch reports a house number plus street stem that
// appear together in the transcript — typically a toned EMS/FD location that
// must not be cleared by clinical-copy / chatter heuristics.
func addressIsStrongSpokenDispatch(addr, transcript string, scope *ScopeData) bool {
	house, street := splitHouseAndStreet(strings.ToUpper(strings.TrimSpace(addr)))
	if house == "" || street == "" {
		return false
	}
	stHead := strings.Fields(street)
	if len(stHead) == 0 {
		return false
	}
	// Reject unit/radio chatter harvested as a street ("432 BE CLEAR ST",
	// "33 RESPOND MENTOR").
	if strongDispatchStreetNoise[stHead[0]] {
		return false
	}
	u := strings.ToUpper(transcript)
	if !strings.Contains(u, house) {
		return false
	}
	if !strings.Contains(u, house+" "+stHead[0]) {
		// Allow hyphenated STT house forms that normalize differently.
		if !strings.Contains(u, stHead[0]) || AddressHouseNumberNotSpokenInTranscript(addr, transcript) {
			return false
		}
	}
	if hasStreetSuffix(street) || streetHasGeocodableSuffix(street) {
		return true
	}
	if len(stHead) >= 2 && !strongDispatchStreetNoise[stHead[1]] {
		return true
	}
	return AddressHasGeocodableAnchor(addr, scope) ||
		addressConfirmedByRepeatedDispatchFraming(addr, transcript)
}

var strongDispatchStreetNoise = map[string]bool{
	"BE": true, "CLEAR": true, "RESPOND": true, "RESPONDING": true, "RESPONSE": true,
	"WILL": true, "SHOW": true, "SHOWING": true, "COPY": true, "ADVISE": true,
	"ADVISING": true, "TIMEOUT": true, "TIME": true, "OUT": true, "MEDIC": true,
	"SQUAD": true, "ENGINE": true, "LADDER": true, "UNIT": true, "CHANNEL": true,
}

func clearExtractAddressFields(c *CuratedAlert) {
	c.Address = ""
	c.CrossStreet1 = ""
	c.CrossStreet2 = ""
	c.AptUnit = ""
	c.CommonName = ""
	c.Lat = ""
	c.Lon = ""
}

func clearNatureIfPlateReadout(c *CuratedAlert, transcript string) {
	if c == nil || !TranscriptIsLicensePlateReadout(transcript) {
		return
	}
	if !containsRealIncidentMarker(" " + strings.ToUpper(transcript) + " ") {
		c.NatureDesc = ""
	}
}

// TranscriptIsDispatchClearSignOff reports unit sign-off traffic that may repeat
// a prior address but is not a new toned dispatch.
func TranscriptIsDispatchClearSignOff(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	clearMarkers := []string{
		" GOOD TO CLEAR", " CLEAR YOU OFF", " YOU CAN CLEAR",
		" YOU'RE GOOD FOR ME TO CLEAR", " CLEAR WITH ",
		" I'LL SHOW YOU CLEAR", " SHOW YOU CLEAR",
		" WE'RE GOOD TO CLEAR", " WE'RE CLEAR", " WE ARE CLEAR",
	}
	hasClear := false
	for _, m := range clearMarkers {
		if strings.Contains(u, m) {
			hasClear = true
			break
		}
	}
	if !hasClear {
		if strings.Contains(u, " YOU CAN CLEAR ") && strings.Contains(u, " I COPY ") &&
			!strings.Contains(u, " RESPOND ") && !containsRealIncidentMarker(u) {
			return true
		}
		return false
	}
	if strings.Contains(u, " RESPOND ") || strings.Contains(u, " RESPONDING ") ||
		strings.Contains(u, " SQUAD CALL ") || strings.Contains(u, " DISPATCHING ") ||
		strings.Contains(u, " FOR A ") || strings.Contains(u, " FOR AN ") {
		return false
	}
	return !containsRealIncidentMarker(u)
}

// TranscriptIsRoutineUnitLog reports non-dispatch unit activity (inspections,
// traffic posts, facility checks) that should not produce map pins.
func TranscriptIsRoutineUnitLog(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if containsRealIncidentMarker(u) {
		return false
	}
	if strings.Contains(u, " RESPOND ") || strings.Contains(u, " RESPONDING ") ||
		strings.Contains(u, " SQUAD CALL ") || strings.Contains(u, " DISPATCHING ") {
		return false
	}
	routine := []string{
		" VEHICLE INSPECTION", " TRAFFIC UNITS", " TRAFFIC UNIT ",
		" OPEN LINE", " JUST ADVISING",
	}
	for _, m := range routine {
		if strings.Contains(u, m) {
			return true
		}
	}
	return false
}

// TranscriptIsOfficerDirectedVisit reports when dispatch is tasking a unit to a
// facility/office for follow-up — not a toned incident with a map location.
func TranscriptIsOfficerDirectedVisit(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if containsRealIncidentMarker(u) || strings.Contains(u, " RESPOND ") ||
		strings.Contains(u, " SQUAD CALL ") || strings.Contains(u, " STRUCTURE FIRE ") {
		return false
	}
	if strings.Contains(u, " WHEN YOU'RE CLEAR ") || strings.Contains(u, " WHEN YOUR CLEAR ") {
		if strings.Contains(u, " GO TO THE OFFICE ") || strings.Contains(u, " CAN YOU GO TO ") ||
			strings.Contains(u, " HEAD TO THE OFFICE ") {
			return true
		}
	}
	if strings.Contains(u, " GO TO THE OFFICE AT ") && strings.Contains(u, " COMPLAINT ") {
		return true
	}
	return false
}

// TranscriptIsHospitalBedCoordination reports inter-facility bed/guard slot
// coordination that is not a new dispatched incident.
func TranscriptIsHospitalBedCoordination(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if strings.Contains(u, " GUARD SLOT") || strings.Contains(u, " HOLDING,") ||
		(strings.Contains(u, " RIVERSIDE HOSPITAL") && strings.Contains(u, " ROOM ")) ||
		(strings.Contains(u, " MOUNT CARMEL") && strings.Contains(u, " RACE PRIORITY")) {
		return !containsRealIncidentMarker(u) && !strings.Contains(u, " RESPOND ")
	}
	return false
}

// TranscriptIsRadioTestAnnouncement reports scheduled AM/FM radio test traffic
// ("WARREN CITY FIRE DEPARTMENT ON THE AIR … PERFORMING THE AM RADIO TEST").
func TranscriptIsRadioTestAnnouncement(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if containsRealIncidentMarker(u) ||
		strings.Contains(u, " DISPATCHING ") ||
		strings.Contains(u, " SQUAD CALL ") ||
		strings.Contains(u, " RESPOND ") ||
		strings.Contains(u, " RESPONDING ") {
		return false
	}
	if strings.Contains(u, " AM RADIO TEST") || strings.Contains(u, " RADIO TEST") {
		return true
	}
	if strings.Contains(u, " ON THE AIR") &&
		(strings.Contains(u, " FIRE DEPARTMENT") || strings.Contains(u, " POLICE DEPARTMENT")) {
		return true
	}
	// Unit sign-on / availability ("ENGINE 6 WILL BE ON THE AIR") — not a dispatch.
	if (strings.Contains(u, " WILL BE ON THE AIR") || strings.Contains(u, " WILL BE ON AIR")) &&
		!containsRealIncidentMarker(u) {
		return true
	}
	return false
}

// TranscriptIsRadioChatter reports sign-offs, tests, cancellations, and similar
// traffic that should never produce a map pin even when an address-like token
// appears in the STT output.
func TranscriptIsRadioChatter(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	clean := strings.Join(strings.Fields(u), " ")
	if TranscriptBlocksIncidentPin(transcript) ||
		TranscriptIsDispatchClearSignOff(transcript) || TranscriptIsRoutineUnitLog(transcript) ||
		TranscriptIsOfficerDirectedVisit(transcript) || TranscriptIsHospitalBedCoordination(transcript) ||
		TranscriptIsRadioTestAnnouncement(transcript) || TranscriptIsPhoneticAlphabetRollCall(transcript) {
		return true
	}
	// Highway stretch advisories are never dispatches.
	if (strings.Contains(u, "CHECK YOUR STRETCH OF ") || strings.Contains(u, " STRETCH OF ")) &&
		!containsRealIncidentMarker(u) {
		return true
	}
	// Chatter markers before address-like tokens (e.g. "tied up with 174").
	// Skipped when a real house-number-shaped token is present elsewhere in
	// the transcript ("COPY, BOTH EN ROUTE TO 5214 MAHONING... DO YOU WANT
	// THEM TO CONTINUE OR DISREGARD?") — a genuine dispatch readback with a
	// real address must not be discarded just because a later, unrelated
	// radio-chatter phrase ("DISREGARD", "HOLD ON", ...) also appears in the
	// same transmission.
	if !containsRealIncidentMarker(u) && !screenHasHouseNumberWord(clean) {
		for _, m := range radioChatterMarkers {
			if strings.Contains(u, m) {
				return true
			}
		}
	}
	// End-of-transmission sign-offs ("dispatch clear", "timeout 1641") appear on
	// real dispatches too — don't treat those as chatter when location was given.
	if containsRealIncidentMarker(u) || strings.Contains(u, " YOU ARE AT ") ||
		screenHasHouseNumberWord(clean) || strings.Contains(u, " RESPOND ") ||
		strings.Contains(u, " STRUCTURE FIRE ") || strings.Contains(u, " SQUAD CALL ") {
		return false
	}
	for _, m := range radioChatterMarkers {
		if strings.Contains(u, m) {
			return true
		}
	}
	// Pure cancellation with no real incident markers.
	if strings.Contains(u, "CANCELLATION") && !containsRealIncidentMarker(u) {
		return true
	}
	if strings.Contains(u, " CANCEL AT ") && !containsRealIncidentMarker(u) {
		return true
	}
	return false
}

func containsRealIncidentMarker(paddedUpper string) bool {
	for _, m := range RealIncidentMarkers {
		if strings.Contains(paddedUpper, m) {
			return true
		}
	}
	for _, m := range suicideIncidentMarkers {
		if strings.Contains(paddedUpper, m) {
			return true
		}
	}
	return false
}

// TranscriptIsOfficerLocationStatusUpdate reports when a unit is describing
// where someone was last seen or is currently located ("I'M SEEING THEM AT…
// THEY WERE AT 7432 OTTAWA AS OF 9-24") — not a toned dispatch to that address.
func TranscriptIsOfficerLocationStatusUpdate(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if containsRealIncidentMarker(u) || stationDispatchRE.MatchString(transcript) {
		return false
	}
	if strings.Contains(u, " I'M SEEING THEM AT") || strings.Contains(u, " IM SEEING THEM AT") ||
		strings.Contains(u, " I AM SEEING THEM AT") {
		return true
	}
	if strings.Contains(u, " THEY WERE AT ") && strings.Contains(u, " AS OF ") {
		return true
	}
	if strings.Contains(u, " LAST SEEN AT ") || strings.Contains(u, " LAST KNOWN AT ") {
		return true
	}
	return false
}

// TranscriptIsOfficerSelfReportVisit reports when an officer is logging their
// own follow-up/check-in visit ("under this call log I went to…") rather than
// a CAD dispatch with a new incident location.
func TranscriptIsOfficerSelfReportVisit(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if containsRealIncidentMarker(u) {
		return false
	}
	if strings.Contains(u, " UNDER THIS CALL LOG ") {
		return true
	}
	if strings.Contains(u, " I WENT TO ") &&
		(strings.Contains(u, " FOLLOW-UP") || strings.Contains(u, " FOLLOW UP") ||
			strings.Contains(u, " CHECK ON ") || strings.Contains(u, " CHECK IN ")) {
		return true
	}
	if strings.Contains(u, " I'LL BE OUT AT") &&
		(strings.Contains(u, " FOLLOW UP") || strings.Contains(u, " FOLLOW-UP")) {
		return true
	}
	return false
}

// AddressIsHighwayStretchOnly catches "check your stretch of 480 westbound"
// style traffic advisories where the LLM extracts a bare route fragment.
func AddressIsHighwayStretchOnly(addr, transcript string) bool {
	addr = strings.TrimSpace(strings.ToUpper(addr))
	if addr == "" {
		return false
	}
	u := " " + strings.ToUpper(transcript) + " "
	if !strings.Contains(u, "STRETCH OF ") && !strings.Contains(u, "CHECK YOUR STRETCH") {
		return false
	}
	if strings.Contains(addr, "&") {
		return false
	}
	house, street := splitHouseAndStreet(addr)
	// "480 W" parses as house=480 street=W — still a bare highway fragment.
	if house != "" && street != "" {
		if len(street) <= 2 && streetDirTokens[street] {
			return true
		}
		if localRouteKeywords[street] || isShortRouteNumber(house) && len(strings.Fields(street)) <= 1 {
			return true
		}
		return false
	}
	fields := strings.Fields(street)
	if len(fields) == 0 {
		fields = strings.Fields(addr)
	}
	if len(fields) >= 1 && len(fields) <= 3 {
		if localRouteKeywords[fields[0]] {
			return true
		}
		if len(fields) == 1 && isShortRouteNumber(fields[0]) {
			return true
		}
	}
	return false
}

// AddressHasGeocodableAnchor reports whether an extracted address is specific
// enough to geocode: house number + street, a two-sided intersection, or a
// numbered route paired with a cross street in the transcript.
func AddressHasGeocodableAnchor(addr string, scope *ScopeData) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return false
	}
	if strings.Contains(addr, "&") {
		a, b := splitIntersectionQuery(addr)
		a, b = cleanIntersectionSide(a), cleanIntersectionSide(b)
		return a != "" && b != "" &&
			bareIntersectionSidePlausible(a, scope) && bareIntersectionSidePlausible(b, scope)
	}
	house, street := splitHouseAndStreet(addr)
	if house != "" && street != "" {
		// Reject room numbers misread as house numbers when the trailing token is
		// not a real thoroughfare ("219 SIXTY" from APARTMENT 219).
		if len(house) <= 3 && !streetHasGeocodableSuffix(street) && !hasStreetSuffix(street) &&
			!isPlausibleLocalStreet(street, scope) && !suffixlessKnownGazetteerStem(street, scope) {
			// Distinctive single-word stems ("CENTRAL") with a short house can still
			// be real ("AREA 490 CENTRAL" / "490 CENTRAL") — only block when the
			// street token itself is too short/ambiguous to geocode safely.
			if !(len(strings.Fields(street)) == 1 && len(street) >= 5 && suffixlessSingleStreetPlausible(street)) {
				return false
			}
		}
		// Single-word street without a suffix ("13117 CEDAR") is too ambiguous —
		// it snaps to unrelated OSM features named "CEDAR".
		if len(strings.Fields(street)) == 1 && !hasStreetSuffix(street) {
			// Distinctive suffixless names ("GARDEN", "CENTRAL", "MILES") may
			// geocode with city bias; very short tokens ("ELM", "OAK") stay
			// blocked unless gazetteered.
			if len(street) >= 5 && suffixlessSingleStreetPlausible(street) {
				return true
			}
			if suffixlessKnownGazetteerStem(street, scope) {
				return true
			}
			return false
		}
		// "SOUTH GREEN" / "EAST MARKET" / "NORTH HIGH" — compass + stem without a
		// type is still a real street form after a same-house correction drops
		// the suffix (HIGH is only 4 letters).
		if streetFields := strings.Fields(street); len(streetFields) == 2 &&
			!hasStreetSuffix(street) &&
			(homonymStreetDirToken(streetFields[0]) || streetDirTokens[canonicalStreetTokens(streetFields[0])]) &&
			len(streetFields[1]) >= 4 && suffixlessSingleStreetPlausible(streetFields[1]) {
			return true
		}
		return isPlausibleLocalStreet(street, scope) || streetHasGeocodableSuffix(street)
	}
	target := strings.TrimSpace(street)
	if target == "" {
		target = addr
	}
	// Street-only numbered routes ("STATE ROUTE 7", "SR 88") between cross streets.
	if house == "" && addressUsesNumberedRoute(target) {
		return isPlausibleLocalStreet(target, scope)
	}
	// Street-only mentions ("STOKES BOULEVARD", "MAIN ST") without a house number.
	if house == "" && hasStreetSuffix(target) {
		return isPlausibleLocalStreet(target, scope) || len(strings.Fields(target)) >= 2
	}
	return false
}

func hasStreetSuffix(street string) bool {
	fields := strings.Fields(strings.ToUpper(street))
	if len(fields) == 0 {
		return false
	}
	return localStreetSuffixes[fields[len(fields)-1]]
}

// streetHasGeocodableSuffix accepts thoroughfare + trailing quadrant
// ("194 FULLER DRIVE NE") as well as normal suffix-only forms.
func streetHasGeocodableSuffix(street string) bool {
	if hasStreetSuffix(street) {
		return true
	}
	fields := strings.Fields(strings.ToUpper(strings.TrimSpace(street)))
	if len(fields) >= 2 && localStreetSuffixes[fields[len(fields)-2]] &&
		(isStreetQuadrantSuffix(fields[len(fields)-1]) || isStreetTrailingCardinal(fields[len(fields)-1])) {
		return true
	}
	return false
}

// RuleAddressPassesGuards is the same anchor check used when merging a
// rule-based address fallback after LLM extraction.
func RuleAddressPassesGuards(addr, transcript string, scope *ScopeData) bool {
	addr = sanitizeAddressField(addr)
	if addr == "" || AddressIsConversationalNoise(addr) ||
		AddressIsUnitPersonIdentifier(addr, transcript) || AddressIsLicensePlate(addr, transcript) ||
		AddressHouseIsLicenseNumberFragment(addr, transcript) ||
		AddressIsMalformedPlateOrNarrative(addr, transcript) || AddressIsUnitNavigationChoice(addr, transcript) ||
		AddressIsUnitStandDownOrCancel(addr, transcript) {
		return false
	}
	if AddressIsMisreadDecimalOrUnitPair(addr, transcript) {
		return false
	}
	if TranscriptIsRadioChatter(transcript) ||
		TranscriptBlocksIncidentPin(transcript) ||
		TranscriptIsRadioTestAnnouncement(transcript) ||
		TranscriptIsTransportNarrative(transcript) ||
		TranscriptIsUnitRouteNavigation(transcript) ||
		TranscriptIsUnitEnRouteStatus(transcript) ||
		TranscriptIsUnitRouteDirective(transcript) ||
		TranscriptIsUnitCoordinationChatter(transcript) ||
		TranscriptIsNonDispatchUnitStatus(transcript) ||
		TranscriptIsWelfareMeetRequest(transcript) ||
		TranscriptIsWelfareCheckInfoUpdate(transcript) ||
		TranscriptIsUtilityTurnOn(transcript) ||
		TranscriptIsPlateOrLookupChatter(transcript) ||
		TranscriptIsLicensePlateReadout(transcript) ||
		TranscriptIsAdvisoryTraffic(transcript) ||
		AddressConflictsWithDispatchTimestamp(addr, transcript) ||
		AddressHouseNumberIsRouteReference(addr, transcript) ||
		AddressHouseNumberFollowsEngineBlob(addr, transcript) ||
		AddressHouseNumberIsEngineUnitConcatenation(addr, transcript) ||
		AddressHouseNumberIsBareStationToken(addr, transcript) ||
		AddressHouseNumberIsStationCorridorPOI(addr, transcript) ||
		AddressHouseNumberIsRouteFragment(addr, transcript) ||
		AddressHouseNumberIsDispatchUnitPrefix(addr, transcript) ||
		AddressHouseNumberIsCallsignIdentifier(addr, transcript) ||
		AddressIsDutyOffApparatusLabel(addr, transcript) ||
		AddressIsMunicipalityAgencyName(addr, transcript) ||
		AddressIsPlaceNarrativeNotDispatch(addr, transcript) ||
		AddressHouseNumberNotSpokenInTranscript(addr, transcript) {
		return false
	}
	if TranscriptIsLicensePlateReadout(transcript) &&
		(AddressIsLicensePlate(addr, transcript) || AddressIsMalformedPlateOrNarrative(addr, transcript)) {
		return false
	}
	return AddressHasGeocodableAnchor(addr, scope) &&
		AddressAlignsWithTranscript(addr, transcript, scope)
}

// TranscriptIsTransportNarrative reports courtesy rides and officer-initiated
// transports that mention a destination but are not toned dispatches.
func TranscriptIsTransportNarrative(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if containsRealIncidentMarker(u) {
		return false
	}
	for _, m := range []string{
		"COURTESY RIDE", "COURTESY TRANSPORT", "GIVING A RIDE", "GIVE A RIDE", "GIVING A LIFT",
		"GIVE A LIFT", "TRANSPORTING A", "TRANSPORTING THE", "BREAK FOR MILEAGE",
		"FROM DENNINGS PARK TO", "FROM THE PARK TO",
		"GOING FOR CRITICAL CARE", "THIS IS CRITICAL CARE",
		"WE'LL BE AT YOU IN ABOUT", "WE WILL BE AT YOU IN ABOUT",
		"CURRENTLY IN ROUTE", "CURRENTLY EN ROUTE",
		" WE ARE EN ROUTE WITH ", " WE RE EN ROUTE WITH ", " WE'RE EN ROUTE WITH ",
		" EN ROUTE WITH A ", " WE WILL BE THERE IN ABOUT ", " WE'LL BE THERE IN ABOUT ",
		" WE LL BE THERE IN ABOUT ", " WORKING ON AN IV",
		" ON BOARD", " ONBOARD",
	} {
		if strings.Contains(u, m) {
			return true
		}
	}
	return false
}

// TranscriptIsPatchInService reports inter-unit patch traffic ("351 OVER IN
// SERVICE, PATCH 21-30") that relays another run — not a new incident.
func TranscriptIsPatchInService(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	return strings.Contains(u, " OVER IN SERVICE") &&
		(strings.Contains(u, " PATCH ") || strings.Contains(u, " BATCH "))
}

// TranscriptIsOnScenePatientUpdate reports units already on scene describing a
// patient ("FIRE IS ON SCENE WITH … SHORTNESS OF BREATH") — not a new fire dispatch.
func TranscriptIsOnScenePatientUpdate(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	// Toned dispatches often include "… is on scene with her" as scene status —
	// keep the pin when this is clearly a station/squad callout.
	if containsRealIncidentMarker(u) || stationDispatchRE.MatchString(transcript) ||
		strings.Contains(u, " SQUAD CALL ") || strings.Contains(u, " SQUAD CALL,") {
		return false
	}
	if !(strings.Contains(u, " ON SCENE WITH ") || strings.Contains(u, " IS ON SCENE WITH ")) {
		return false
	}
	for _, cue := range []string{
		" SHORTNESS OF BREATH", " CHEST PAIN", " DIFFICULTY BREATHING",
		" BREATHING PROBLEMS", " UNCONSCIOUS", " SEIZURE", " STROKE",
		" OVERDOSE", " DIABETIC", " ABDOMINAL", " FALL ", " INJURED ",
		" YEAR OLD", " YEAR-OLD", " FEMALE", " MALE", " PATIENT",
	} {
		if strings.Contains(u, cue) {
			return true
		}
	}
	return false
}

// TranscriptIsUnitOnSiteStatus reports on-scene unit updates with no dispatch.
func TranscriptIsUnitOnSiteStatus(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if containsRealIncidentMarker(u) {
		return false
	}
	return strings.Contains(u, " ON-SITE") || strings.Contains(u, " ON SITE")
}

// TranscriptIsUnitRouteNavigation reports units describing their current highway
// position and a cross street for navigation — not an incident location.
func TranscriptIsUnitRouteNavigation(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if !(strings.Contains(u, " YOU'RE ON ROUTE ") || strings.Contains(u, " ON ROUTE ")) {
		return false
	}
	if strings.Contains(u, " CROSS STREET") || strings.Contains(u, " AT THAT INTERSECTION") {
		return true
	}
	return false
}

// TranscriptIsUnitRouteDirective reports dispatch-ack routing like "COPY 241 AND
// ROUTE 13117 CEDAR ROAD" — the address is a destination being assigned to a
// unit, not a new toned incident to pin on the map.
func TranscriptIsUnitRouteDirective(transcript string) bool {
	u := " " + strings.ToUpper(strings.TrimSpace(transcript)) + " "
	if !strings.Contains(u, " AND ROUTE ") {
		return false
	}
	if containsRealIncidentMarker(u) {
		return false
	}
	return strings.Contains(u, " COPY ") || strings.HasPrefix(strings.TrimSpace(u), "COPY ")
}

// ClearPinWhenAddressNotImportAnchored drops coordinates when a suffixless
// extracted address does not resolve to an imported thoroughfare for this
// agency. Fail-closed backstop after geocoding when fuzzy matchers pin noise.
func ClearPinWhenAddressNotImportAnchored(curated *CuratedAlert, scope *ScopeData) {
	if curated == nil || scope == nil || len(scope.KnownStreets) == 0 {
		return
	}
	if strings.TrimSpace(curated.Lat) == "" {
		return
	}
	addr := strings.ToUpper(strings.TrimSpace(curated.Address))
	if addr == "" {
		curated.Lat = ""
		curated.Lon = ""
		return
	}
	if strings.Contains(addr, "&") {
		a, b := splitIntersectionQuery(addr)
		a, b = cleanIntersectionSide(a), cleanIntersectionSide(b)
		if !addressSideImportAnchored(a, scope) || !addressSideImportAnchored(b, scope) {
			curated.Lat = ""
			curated.Lon = ""
		}
		return
	}
	_, street := splitHouseAndStreet(addr)
	if street == "" {
		street = addr
	}
	if hasStreetSuffix(street) || streetHasGeocodableSuffix(street) || addressUsesNumberedRoute(street) {
		return
	}
	if !suffixlessPhraseInGazetteer(street, scope) {
		curated.Lat = ""
		curated.Lon = ""
	}
}

// AddressCandidateHasDigit reports whether addr contains a 0-9 digit — the
// local identify signal for "possible address before Nominatim". Unit/station
// false positives are rejected by ApplyExtractedAddressGuards, not here.
func AddressCandidateHasDigit(addr string) bool {
	for _, r := range addr {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// DropUnimportAnchoredAddress used to clear extracts missing from the import
// gazetteer. Nominatim is now the street authority, so digit-bearing
// candidates always pass through. Non-digit extracts (rare intersection-only
// forms) still require a gazetteer/strong-spoken anchor when KnownStreets is
// loaded, so pure chatter without digits does not reach geocode.
func DropUnimportAnchoredAddress(curated *CuratedAlert, transcript string, scope *ScopeData) {
	if curated == nil {
		return
	}
	addr := strings.TrimSpace(curated.Address)
	if addr == "" {
		return
	}
	if AddressCandidateHasDigit(addr) {
		return
	}
	if scope == nil || len(scope.KnownStreets) == 0 {
		return
	}
	if AddressHasGeocodableAnchor(addr, scope) {
		return
	}
	if addressIsStrongSpokenDispatch(addr, transcript, scope) {
		return
	}
	curated.Address = ""
	curated.CrossStreet1 = ""
	curated.CrossStreet2 = ""
	curated.CommonName = ""
	curated.AptUnit = ""
}

func addressSideImportAnchored(side string, scope *ScopeData) bool {
	side = strings.ToUpper(strings.TrimSpace(side))
	if side == "" {
		return false
	}
	if hasStreetSuffix(side) || addressUsesNumberedRoute(side) {
		return isPlausibleLocalStreet(side, scope)
	}
	return suffixlessPhraseInGazetteer(side, scope)
}

// suffixlessKnownGazetteerStem reports when a suffixless dispatch stem matches
// at least one imported thoroughfare ("520 FENTON" vs FENTON STREET/ROAD).
func suffixlessKnownGazetteerStem(street string, scope *ScopeData) bool {
	street = strings.ToUpper(strings.TrimSpace(street))
	if street == "" || len(strings.Fields(street)) != 1 || scope == nil {
		return false
	}
	for _, ks := range scope.KnownStreets {
		name, suffix := streetNameAndSuffix(strings.ToUpper(strings.TrimSpace(ks)))
		if suffix == "" {
			continue
		}
		if name == street || streetLeadingNameToken(name) == street {
			return true
		}
	}
	return false
}

// AddressMissingSpokenStreetSuffix reports when the LLM dropped a thoroughfare
// type the dispatcher actually spoke ("13117 CEDAR" vs "13117 CEDAR ROAD").
func AddressMissingSpokenStreetSuffix(addr, transcript string) bool {
	house, street := splitHouseAndStreet(strings.ToUpper(strings.TrimSpace(addr)))
	if house == "" || street == "" || hasStreetSuffix(street) {
		return false
	}
	u := strings.ToUpper(transcript)
	stU := strings.ToUpper(street)
	for _, suf := range []string{" ROAD", " RD", " STREET", " ST", " AVENUE", " AVE", " DRIVE", " DR", " LANE", " LN", " BOULEVARD", " BLVD"} {
		if strings.Contains(u, house+" "+stU+suf) ||
			strings.Contains(u, house+", "+stU+suf) ||
			strings.Contains(u, house+", "+stU+","+suf) {
			return true
		}
	}
	return false
}

// TranscriptIsMedicEnRouteUpdate reports medic-to-hospital updates with vitals —
// not a new toned dispatch to pin on the map.
func TranscriptIsMedicEnRouteUpdate(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if !(strings.Contains(u, " EN ROUTE ") || strings.Contains(u, " IN ROUTE ")) {
		return false
	}
	if strings.Contains(u, " MEDIC ") || strings.Contains(u, " AMBULANCE ") ||
		strings.Contains(u, " WE ARE EN ROUTE") || strings.Contains(u, " WE'RE EN ROUTE") {
		if strings.Contains(u, " ON THE MONITOR") || strings.Contains(u, " CHIEF COMPLAINT") ||
			strings.Contains(u, " COMPLAINT TODAY") || strings.Contains(u, " HEART RATE") {
			return true
		}
	}
	return false
}

// TranscriptIsUnitEnRouteBriefing reports unit check-ins marking en route to a
// trail/post ("MARK ME IN ROUTE TO THE CAMP CHASE TRAIL") — not incident dispatch.
func TranscriptIsUnitEnRouteBriefing(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if containsRealIncidentMarker(u) {
		return false
	}
	if strings.Contains(u, " MARK ME IN ROUTE ") || strings.Contains(u, " MARK US IN ROUTE ") {
		return true
	}
	if strings.Contains(u, " TO DISPATCH") && strings.Contains(u, " IN ROUTE TO ") {
		return true
	}
	return false
}

// TranscriptIsUnitEnRouteStatus reports unit travel updates ("EN ROUTE TO 123
// MAIN ST, ETA FIVE MINUTES") that mention a street as a destination, not a
// toned incident location.
func TranscriptIsUnitEnRouteStatus(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if containsRealIncidentMarker(u) || stationDispatchRE.MatchString(transcript) {
		return false
	}
	if strings.Contains(u, " EN ROUTE TO ") || strings.Contains(u, " ENROUTE TO ") {
		return true
	}
	if strings.Contains(u, " ETA ") && strings.Contains(u, " EN ROUTE") {
		return true
	}
	return false
}

// TranscriptIsNonDispatchUnitStatus reports short unit check-ins ("it's 723 about…")
// that are not incident dispatches.
func TranscriptIsNonDispatchUnitStatus(transcript string) bool {
	u := strings.TrimSpace(strings.ToUpper(transcript))
	if containsRealIncidentMarker(" " + u + " ") {
		return false
	}
	if matched, _ := regexp.MatchString(`^(?:IT'S|ITS|HE'S|SHE'S|WE'RE|THEY'RE)\s+\d`, u); matched {
		return true
	}
	return false
}

// AddressConflictsWithDispatchTimestamp reports when the extracted house number
// is actually a trailing dispatch-time token (TIMEOUT 1704, DISPATCH CLEAR 1701).
func AddressConflictsWithDispatchTimestamp(addr, transcript string) bool {
	house, _ := splitHouseAndStreet(strings.ToUpper(strings.TrimSpace(addr)))
	if house == "" {
		return false
	}
	u := strings.ToUpper(transcript)
	for _, prefix := range []string{
		"TIME TO DISPATCH ", "YOUR TIME TO DISPATCH ",
		"DISPATCH CLEAR ", "TIMEOUT ", "TIME OUT ", "YOUR TIMEOUT ",
		"COMMON TONE IS ", "TIME OF TONE ", "TIME ATONE ",
		"RING ROUTE HAS ", "YOUR RING ROUTE HAS ", "RING ROUTE ",
		" YOUR TIME IS ", " TIME IS ",
	} {
		if idx := strings.Index(u, prefix); idx >= 0 {
			rest := strings.Fields(u[idx+len(prefix):])
			if len(rest) > 0 {
				tok := strings.TrimRight(rest[0], ".,;")
				if tok == house {
					return true
				}
			}
		}
	}
	// Trailing HHMM hand-off: "… 1706, YOUR TURN." / "1706 YOUR TURN"
	if regexp.MustCompile(`\b`+regexp.QuoteMeta(house)+`\b[,.]?\s+YOUR\s+TURN\b`).MatchString(u) {
		return true
	}
	if regexp.MustCompile(`\b`+regexp.QuoteMeta(house)+`\b[,.]?\s+YOUR\s+TIME(?:\s+OUT)?\b`).MatchString(u) {
		return true
	}
	if len(house) <= 2 && isAllDigits(house) {
		if regexp.MustCompile(`\b\d{1,2}\.` + regexp.QuoteMeta(house) + `\b`).MatchString(u) {
			return true
		}
	}
	return false
}

// AddressHouseNumberIsRouteReference reports when the house number only appears
// as a state/county route reference ("IT'S ON 1703", "ON ROUTE 1703") while a
// different house+street pair dominates the transcript.
func AddressHouseNumberIsRouteReference(addr, transcript string) bool {
	house, street := splitHouseAndStreet(strings.ToUpper(strings.TrimSpace(addr)))
	if house == "" || street == "" {
		return false
	}
	u := strings.ToUpper(transcript)
	routeRefs := []string{
		" ON ROUTE " + house,
		"IT'S ON " + house,
		" ON " + house + ".",
	}
	hasRouteRef := false
	for _, p := range routeRefs {
		if strings.Contains(u, p) {
			hasRouteRef = true
			break
		}
	}
	if !hasRouteRef {
		return false
	}
	streetHead := strings.Fields(street)
	if len(streetHead) == 0 {
		return false
	}
	dispatchKey := house + " " + streetHead[0]
	if strings.Contains(u, dispatchKey) {
		return false
	}
	// Another house number on the same street appears more often (1160 RANCH vs route 1703).
	for _, m := range regexp.MustCompile(`\b(\d{3,5})\s+`+regexp.QuoteMeta(streetHead[0])).FindAllStringSubmatch(u, -1) {
		if len(m) == 2 && m[1] != house {
			return true
		}
	}
	return hasRouteRef
}

// TranscriptIsWelfareCheckInfoUpdate reports officer-to-officer status on a
// welfare check ("I was able to find the information on Lawrence Crowe you're
// doing welfare check-on") — not a new dispatch with a location.
func TranscriptIsWelfareCheckInfoUpdate(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if !(strings.Contains(u, " WELFARE CHECK") || strings.Contains(u, " WELFARE CHECK-ON") ||
		strings.Contains(u, " WELLNESS CHECK")) {
		return false
	}
	if containsRealIncidentMarker(u) {
		return false
	}
	for _, m := range []string{
		" FIND THE INFORMATION ON ", " FOUND THE INFORMATION ON ",
		" I WAS ABLE TO FIND THE INFORMATION ON ", " I WAS ABLE TO FIND ",
		" I HAVE THE INFORMATION ON ", " GOT THE INFORMATION ON ",
		" YOU'RE DOING WELFARE CHECK", " YOU ARE DOING WELFARE CHECK",
	} {
		if strings.Contains(u, m) {
			return true
		}
	}
	return false
}

// TranscriptIsWelfareMeetRequest reports meet-the-officer requests that name a
// business address but are not emergency dispatches.
func TranscriptIsWelfareMeetRequest(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if containsRealIncidentMarker(u) {
		return false
	}
	for _, m := range []string{"YOU CAN MEET", "MEET ME AT", "MEET US AT", "MEET OUTSIDE"} {
		if strings.Contains(u, m) {
			return true
		}
	}
	return false
}

// TranscriptIsUtilityTurnOn reports gas/electric turn-on notifications.
func TranscriptIsUtilityTurnOn(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if containsRealIncidentMarker(u) {
		return false
	}
	return strings.Contains(u, " TURN ON") || strings.Contains(u, " TURNON") ||
		strings.Contains(u, " READY FOR THEIR TURN ON")
}

// TranscriptIsAdvisoryTraffic reports informational traffic that mentions a
// location but is not a toned dispatch (911 open-line advisories, etc.).
func TranscriptIsAdvisoryTraffic(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if containsRealIncidentMarker(u) {
		return false
	}
	return (strings.Contains(u, " JUST ADVISING") || strings.Contains(u, " ADVISING,")) &&
		(strings.Contains(u, " OPEN LINE") || strings.Contains(u, " 911 HANG"))
}

// TranscriptIsPlateOrLookupChatter reports BMV/plate lookup traffic.
func TranscriptIsPlateOrLookupChatter(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if strings.Contains(u, " COMING BACK ON A ") ||
		strings.Contains(u, " REGISTERED OWNER") ||
		strings.Contains(u, " COMES BACK TO AN ADDRESS IN ") ||
		strings.Contains(u, " COMES BACK TO ") ||
		strings.Contains(u, " COMES BACK ON ") ||
		strings.Contains(u, " WELCOME BACK ON ") ||
		strings.Contains(u, " IN COLOR, VALID") ||
		(strings.Contains(u, " OUT OF ") && strings.Contains(u, " IN COLOR")) {
		return true
	}
	// Person/license return: "51 OUT OF GENEVA, VALID" / "OUT OF WARREN, WHO'S VALID".
	// Do not treat OUT OF SERVICE / OUT OF STATE as BMV returns.
	if strings.Contains(u, " OUT OF ") &&
		!strings.Contains(u, " OUT OF SERVICE") &&
		!strings.Contains(u, " OUT OF STATE") &&
		(strings.Contains(u, " VALID") || strings.Contains(u, " WHO'S VALID") ||
			strings.Contains(u, " WHOS VALID") || strings.Contains(u, " REVOKE")) {
		return true
	}
	return false
}

// TranscriptQualifiesForKnownPlacePin reports whether transcript-sourced known
// place pins are appropriate (real dispatch / location signal present).
func TranscriptQualifiesForKnownPlacePin(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	if TranscriptIsPlateOrLookupChatter(transcript) ||
		TranscriptIsTransportNarrative(transcript) ||
		TranscriptIsUtilityTurnOn(transcript) ||
		TranscriptIsRadioChatter(transcript) ||
		TranscriptIsLocationNarrative(transcript) {
		return false
	}
	return containsRealIncidentMarker(u) || TranscriptLikelyHasLocation(transcript, nil)
}
