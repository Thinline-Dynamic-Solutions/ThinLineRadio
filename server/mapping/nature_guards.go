// Copyright (C) 2025 Thinline Dynamic Solutions
//
// nature_guards.go — incident nature validation, generic-label rejection, and
// optional normalization to configured keyword-list terms.

package mapping

import (
	"regexp"
	"sort"
	"strings"
)

var (
	fallingObjectRE           = regexp.MustCompile(`(?i)\bFALLING\s+(HER|HIS|THE|THEIR|A|AN|THIS|MY|YOUR|OUR)\b`)
	fallIncidentContextRE     = regexp.MustCompile(`(?i)\b(FALL\s+PATIENT|FALL\s+VICTIM|FOR\s+A\s+FALL|FOR\s+FALL|FELL\s+DOWN|HAS\s+FALLEN|HAVE\s+FALLEN|FROM\s+A\s+FALL|LIFT\s+ASSIST|ON\s+THE\s+FLOOR|UNABLE\s+TO\s+GET\s+UP|CAN'?T\s+GET\s+UP|STILL\s+DOWN|INJURED\s+IN\s+FALL|SLIPPED\s+AND\s+FELL)\b`)
	dispatchDemographicLeadRE = regexp.MustCompile(`(?i)^(?:\d{1,3}[- ]?YEAR[- ]?OLD\s+)?(?:MALE|FEMALE)\s+`)
	// House number + street name ending in COURT ("150 CHARLES COURT").
	streetAddressCourtRE = regexp.MustCompile(`(?i)\b\d{1,6}(?:[\s\-]+\d{1,4})?(?:[\s,\-]+[A-Z0-9][A-Z0-9'\-]*){0,6}\s+COURT\b`)
)

// genericNatureLabels are useless catch-alls the LLM must not emit.
var genericNatureLabels = map[string]bool{
	"ALERT": true, "UNKNOWN": true, "EMERGENCY": true, "GENERAL": true,
	"INCIDENT": true, "SITUATION": true, "N/A": true, "NA": true, "NONE": true,
	"UNCLASSIFIED": true, "OTHER": true, "MISC": true, "RADIO": true,
	"MULTI-INCIDENT": true, "MULTI INCIDENT": true,
	"ON-SITE": true, "ON SITE": true,
	"ALL CLEAR": true, "DISPATCH CLEAR": true,
}

// apparatusOnlyNatureLabels are unit/apparatus tokens that sometimes appear in
// keyword lists but must never be the incident nature on a map pin.
var apparatusOnlyNatureLabels = map[string]bool{
	"LADDER": true, "ENGINE": true, "MEDIC": true, "SQUAD": true, "TRUCK": true,
	"BATTALION": true, "RESCUE": true, "UNIT": true, "CHANNEL": true, "CAR": true,
	"EMS": true, "ALS": true, "BLS": true, "PATIENT": true, "MEDFOR": true,
	"CHIEF": true, "CAPTAIN": true, "TANKER": true, "BRUSH": true, "QUINT": true,
}

var (
	ilpersonNatureRE          = regexp.MustCompile(`(?i)\bIL\s*PERSON\b`)
	weightLimitNatureRE       = regexp.MustCompile(`(?i)^OVER\s*\d+\s*LIMIT$`)
	apparatusDispatchPhraseRE = regexp.MustCompile(`(?i)^(MEDIC|ENGINE|SQUAD|RESCUE|LADDER|TRUCK|UNIT|BATTALION|CHIEF|CAPTAIN)\s+(RUN|CALL|DISPATCH)$`)
	policeStatusNatureRE      = regexp.MustCompile(`(?i)^(10[- ]?\d{1,2}|CODE\s+(CLEAR|BLUE|VIOLET|4)|CLEAR\s+CODE|TROUBLE\s+UNKNOWN|TRAFFIC\s+STOP|STAND\s+BY|NOTICE|ADVICE|TOW|READ|RELEASE|MOTION|HANGOUT|HANG\s+UP)$`)
)

func isApparatusOnlyNatureLabel(nature string) bool {
	n := strings.ToUpper(strings.TrimSpace(nature))
	if apparatusOnlyNatureLabels[n] {
		return true
	}
	return apparatusDispatchPhraseRE.MatchString(n)
}

const maxNatureLabelLen = 22

func isExactNatureCode(nature string, natureCodes []string) bool {
	n := strings.ToUpper(strings.TrimSpace(nature))
	if n == "" {
		return false
	}
	for _, code := range natureCodes {
		if strings.ToUpper(strings.TrimSpace(code)) == n {
			return true
		}
	}
	return false
}

// IsGenericNature reports catch-all nature labels that carry no dispatch meaning.
func IsGenericNature(nature string) bool {
	n := strings.ToUpper(strings.TrimSpace(nature))
	if n == "" {
		return false
	}
	if genericNatureLabels[n] {
		return true
	}
	if isApparatusOnlyNatureLabel(n) {
		return true
	}
	if weightLimitNatureRE.MatchString(n) {
		return true
	}
	if policeStatusNatureRE.MatchString(n) {
		return true
	}
	// "GENERAL ALERT", "MEDICAL EMERGENCY" with no specifics — keep MEDICAL EMERGENCY?
	if n == "MEDICAL EMERGENCY" || n == "POLICE EMERGENCY" {
		return true
	}
	return false
}

// ScrubIncidentNature corrects false-positive nature labels inferred from STT
// homophones and verb forms that are not medical fall dispatches.
func ScrubIncidentNature(nature, transcript string) string {
	n := strings.ToUpper(strings.TrimSpace(nature))
	if n == "" || IsGenericNature(n) {
		return ""
	}
	u := strings.ToUpper(transcript)
	padded := " " + u + " "
	switch n {
	case "FALL", "FELL", "FALLEN":
		if IsFallNatureFalsePositive(u) {
			return ""
		}
	case "LAKE EMERGENCY":
		if natureKeywordIsLakeDepartmentFalsePositive(padded, n) {
			return ""
		}
		if inferWiresTreesDownNature(transcript, nil) != "" {
			return ""
		}
	case "ALLERGIC REACTION":
		if !allergicEvidenceInTranscript(padded) {
			return ""
		}
	case "PERSONAL":
		if !personalNatureEvidenceInTranscript(padded) {
			return ""
		}
	case "BREAK":
		if natureKeywordIsRadioBreakFalsePositive(padded, "BREAK") {
			return ""
		}
	case "COURT", "CT":
		if !courtNatureExplicitlySpoken(padded) {
			return ""
		}
	case "ENTRAPMENT/RESCUE":
		if natureKeywordIsDepartmentRescueFalsePositive(padded, "RESCUE") &&
			!strings.Contains(padded, " ENTRAPMENT ") && !strings.Contains(padded, " EXTRICATION ") &&
			!strings.Contains(padded, " PINNED ") && !strings.Contains(padded, " TRAPPED ") {
			return ""
		}
	}
	return n
}

// clampNatureLabel shortens LLM-invented phrases to configured keywords or the
// first few words that fit the display limit. Exact configured call-nature
// labels are preserved at full length for call-card display.
func clampNatureLabel(nature string, natureCodes []string) string {
	n := strings.ToUpper(strings.TrimSpace(nature))
	if n == "" {
		return n
	}
	if isExactNatureCode(n, natureCodes) {
		return n
	}
	if len(n) <= maxNatureLabelLen {
		return n
	}
	flat := strings.Join(strings.Fields(strings.NewReplacer(",", " ", ".", " ", ";", " ").Replace(n)), " ")
	if norm := NormalizeNatureToKeywords(flat, natureCodes); norm != "" {
		if len(norm) <= maxNatureLabelLen || isExactNatureCode(norm, natureCodes) {
			return norm
		}
	}
	fields := strings.Fields(n)
	for len(fields) > 1 && len(strings.Join(fields, " ")) > maxNatureLabelLen {
		fields = fields[:len(fields)-1]
	}
	out := strings.Join(fields, " ")
	if len(out) > maxNatureLabelLen && len(fields) > 0 {
		return fields[0]
	}
	return out
}

var natureAnchorStopwords = map[string]bool{
	"POSSIBLE": true, "PROBABLE": true, "SUSPECTED": true, "REPORTED": true,
	"WITH": true, "THE": true, "AND": true, "FOR": true, "A": true, "AN": true,
	"OF": true, "TO": true, "ON": true, "AT": true, "IN": true, "OR": true,
}

// natureAnchoredInTranscript rejects invented or over-broad nature labels that
// do not appear in what dispatch actually said.
func natureAnchoredInTranscript(nature, transcript string) bool {
	n := strings.ToUpper(strings.TrimSpace(nature))
	if n == "" {
		return true
	}
	u := " " + strings.ToUpper(transcript) + " "
	switch n {
	case "CRASH":
		return strings.Contains(u, " CRASH ") || strings.Contains(u, " MVA ") ||
			strings.Contains(u, " ACCIDENT ") || strings.Contains(u, " COLLISION ") ||
			strings.Contains(u, " WRECK ") || strings.Contains(u, " CRASH WITH ")
	case "PATIENT":
		return strings.Contains(u, " PATIENT ") && !strings.Contains(u, " FALL PATIENT ")
	case "MEDICAL":
		return strings.Contains(u, " MEDICAL ") || strings.Contains(u, " EMS ") ||
			strings.Contains(u, " MEDIC ") || strings.Contains(u, " AMBULANCE ")
	case "CRITICAL":
		return strings.Contains(u, " CRITICAL ")
	case "FIRE":
		return strings.Contains(u, " FIRE ") && !strings.Contains(u, " FIREARM ") &&
			!natureKeywordIsNegated(u, "FIRE")
	case "COURT", "CT":
		if courtNatureExplicitlySpoken(u) {
			return true
		}
		return false
	case "VICTIM":
		return strings.Contains(u, " VICTIM ")
	case "INJURY":
		return strings.Contains(u, " INJURY ") || strings.Contains(u, " INJURED ")
	case "ACCIDENT":
		return strings.Contains(u, " ACCIDENT ") || strings.Contains(u, " AUTO ACCIDENT") ||
			strings.Contains(u, " MVA ") || strings.Contains(u, " CRASH ")
	case "SMOKE":
		return (strings.Contains(u, " SMOKE SHOWING ") || strings.Contains(u, " SMOKE ")) &&
			!strings.Contains(u, " FIREARM ") && !natureKeywordIsNegated(u, "SMOKE")
	case "CHASE":
		if strings.Contains(u, " CHASE TRAIL") || strings.Contains(u, " TO VIEW") ||
			strings.Contains(u, " MARK ME IN ROUTE ") {
			return false
		}
		return strings.Contains(u, " CHASE ") || strings.Contains(u, " PURSUIT ")
	case "WIRES/TREES DOWN":
		return transcriptDescribesWiresTreesDown(transcript)
	}
	if strings.Contains(n, "SUICID") || strings.Contains(n, "MENTAL") {
		return transcriptDescribesSuicideRisk(u)
	}
	if wholeWordContains(u, n) || transcriptContainsNatureKeyword(u, n) {
		return true
	}
	matched, required := 0, 0
	for _, w := range strings.Fields(n) {
		w = strings.TrimRight(w, ".,;/")
		if len(w) < 3 || natureAnchorStopwords[w] {
			continue
		}
		required++
		if strings.Contains(u, " "+w+" ") || strings.Contains(u, " "+w+"'") {
			matched++
		}
	}
	if required == 0 {
		return false
	}
	return matched >= 1
}

// IsFallNatureFalsePositive reports whether "fall" nature was inferred from
// conversational "falling [pronoun] …" (usually STT for "following") rather than
// a patient-down dispatch.
func IsFallNatureFalsePositive(transcriptUpper string) bool {
	u := strings.ToUpper(transcriptUpper)
	if !strings.Contains(u, "FALLING") && !strings.Contains(u, " FALL ") {
		return false
	}
	if fallIncidentContextRE.MatchString(u) {
		return false
	}
	if fallingObjectRE.MatchString(u) {
		return true
	}
	if strings.Contains(u, "FALLING") {
		return true
	}
	return false
}

// wholeWordContains reports whether term appears in paddedUpper bounded by
// spaces (paddedUpper must already include leading/trailing spaces).
func wholeWordContains(paddedUpper, term string) bool {
	term = strings.ToUpper(strings.TrimSpace(term))
	if term == "" {
		return false
	}
	if strings.Contains(paddedUpper, " "+term+" ") {
		return true
	}
	needle := " " + term
	for _, end := range []string{",", ".", ";", ":"} {
		if strings.Contains(paddedUpper, needle+end) {
			return true
		}
	}
	return false
}

// fdRequestingPDNature labels inter-agency requests where fire/EMS tones police
// on the radio ("THE FD IS REQUESTING PD ON THE RAD") rather than a PD squad tone.
func fdRequestingPDNature(paddedUpper string) string {
	if strings.Contains(paddedUpper, " REQUESTING PD ") ||
		strings.Contains(paddedUpper, " REQUESTING POLICE ") ||
		strings.Contains(paddedUpper, " IS REQUESTING PD ") ||
		strings.Contains(paddedUpper, " IS REQUESTING POLICE ") {
		return "FD ASSIST"
	}
	return ""
}

// inferCardiacSymptomNature maps tingling/pressure cardiac symptoms to chest pain.
func inferCardiacSymptomNature(transcript string, natureCodes []string) string {
	u := " " + strings.ToUpper(transcript) + " "
	if strings.Contains(u, " CHEST TINGLING") || strings.Contains(u, " CHEST PAIN") ||
		strings.Contains(u, " CHEST PAINS") || strings.Contains(u, " CHEST PRESSURE") ||
		(strings.Contains(u, " CHEST ") && strings.Contains(u, " TINGLING")) ||
		(strings.Contains(u, " LEFT ARM ") && strings.Contains(u, " TINGLING")) {
		if norm := NormalizeNatureToKeywords("CHEST PAIN", natureCodes); norm != "" {
			return norm
		}
		return "CHEST PAIN"
	}
	return ""
}

// inferSeizureActivityNature prefers seizure wording over trailing consciousness vitals.
func inferSeizureActivityNature(transcript string, natureCodes []string) string {
	u := strings.ToUpper(transcript)
	if !strings.Contains(u, "SEIZURE") {
		return ""
	}
	if norm := NormalizeNatureToKeywords("SEIZURES", natureCodes); norm != "" {
		return norm
	}
	for _, pref := range []string{"SEIZURE-LIKE ACTIVITY", "SEIZURE LIKE ACTIVITY", "SEIZURE"} {
		if strings.Contains(u, pref) {
			if norm := NormalizeNatureToKeywords(pref, natureCodes); norm != "" {
				return norm
			}
		}
	}
	return "SEIZURES"
}

func isLawEnforcementCrimeNature(n string) bool {
	ku := strings.ToUpper(strings.TrimSpace(n))
	for _, crime := range []string{
		"COUNTERFEIT", "FORGERY", "FRAUD", "ROBBERY", "BURGLARY", "THEFT", "ASSAULT",
		"DOMESTIC", "PURSUIT", "WARRANT", "SHOTS FIRED", "STABBING",
	} {
		if strings.Contains(ku, crime) {
			return true
		}
	}
	return false
}

// natureKeywordIsLakeDepartmentFalsePositive reports when a lake-emergency phrase
// matched only inside a fire-department name ("Geneva on the Lake Fire").
func natureKeywordIsLakeDepartmentFalsePositive(paddedUpper, keyword string) bool {
	ku := strings.ToUpper(strings.TrimSpace(keyword))
	if ku != "LAKE EMERGENCY" && ku != "LAKE RESCUE" && ku != "BOAT EMERGENCY" && ku != "ON THE LAKE" {
		return false
	}
	for _, cue := range []string{
		" ON THE LAKE FIRE", " TO YOU ON THE LAKE FIRE", " ATTENTION TO YOU ON THE LAKE FIRE",
		" LAKE FIRE,", " LAKE FIRE ", " LAKE FIRE DEPARTMENT", " LAKE FD ",
	} {
		if strings.Contains(paddedUpper, cue) {
			return true
		}
	}
	if strings.Contains(paddedUpper, " LAKE RESCUE ") && !lakeEmergencyEvidenceInTranscript(paddedUpper) {
		return true
	}
	return false
}

// keyword matched only inside "BREAK ROOM" facility phrasing.
func natureKeywordIsBreakRoomFalsePositive(paddedUpper, keyword string) bool {
	ku := strings.ToUpper(strings.TrimSpace(keyword))
	if ku != "BREAK" && ku != "WATER BREAK" && !strings.HasSuffix(ku, " BREAK") {
		return false
	}
	return strings.Contains(paddedUpper, " BREAK ROOM") ||
		strings.Contains(paddedUpper, " IN THE BREAK ROOM")
}

// natureKeywordIsRadioBreakFalsePositive rejects bare BREAK as radio
// procedure ("BREAK FOR THE NAME", trailing BREAK) unless the transcript
// is a unit meal/break or a glass/forced-entry break.
func natureKeywordIsRadioBreakFalsePositive(paddedUpper, keyword string) bool {
	ku := strings.ToUpper(strings.TrimSpace(keyword))
	if ku != "BREAK" {
		return false
	}
	if strings.Contains(paddedUpper, " BREAK TIME") || strings.Contains(paddedUpper, " COFFEE BREAK") ||
		strings.Contains(paddedUpper, " ON BREAK") || strings.Contains(paddedUpper, " LUNCH BREAK") ||
		strings.Contains(paddedUpper, " GLASS BREAK") || strings.Contains(paddedUpper, " WINDOW BREAK") {
		return false
	}
	return true
}

// natureKeywordIsDepartmentRescueFalsePositive rejects bare RESCUE when it is
// only the company name ("WESTFIELD RESCUE REQUEST FOR YOUR SQUAD") and the
// transcript has no entrapment/extrication evidence.
func natureKeywordIsDepartmentRescueFalsePositive(paddedUpper, keyword string) bool {
	ku := strings.ToUpper(strings.TrimSpace(keyword))
	if ku != "RESCUE" {
		return false
	}
	for _, ev := range []string{
		" ENTRAPMENT ", " EXTRICATION ", " PINNED ", " TRAPPED ",
		" PERSON TRAPPED ", " PEOPLE TRAPPED ", " VEHICLE EXTRICATION ",
	} {
		if strings.Contains(paddedUpper, ev) {
			return false
		}
	}
	return true
}

func natureKeywordBlockedInMedicalDispatch(paddedUpper, keyword string) bool {
	ku := strings.ToUpper(strings.TrimSpace(keyword))
	if ku == "" {
		return false
	}
	if !strings.Contains(paddedUpper, " CHEST ") && !strings.Contains(paddedUpper, " TINGLING") &&
		!strings.Contains(paddedUpper, " SHORTNESS OF BREATH") {
		return false
	}
	for _, crime := range []string{
		"COUNTERFEIT", "FORGERY", "FRAUD", "ROBBERY", "BURGLARY", "THEFT", "ASSAULT",
		"DOMESTIC", "PURSUIT", "WARRANT", "SHOTS FIRED", "STABBING",
	} {
		if strings.Contains(ku, crime) {
			return true
		}
	}
	return false
}

// transcriptContainsNatureKeyword reports whether a configured keyword appears
// in the transcript literally or as a morphological variant (DEHYDRATED ↔
// DEHYDRATION). Multi-word keywords still require an exact phrase match.
func transcriptContainsNatureKeyword(paddedUpper, keyword string) bool {
	kw := strings.ToUpper(strings.TrimSpace(keyword))
	if kw == "" {
		return false
	}
	if wholeWordContains(paddedUpper, kw) {
		return true
	}
	if strings.Contains(kw, " ") {
		return false
	}
	for _, tok := range strings.Fields(strings.Trim(paddedUpper, " ")) {
		tok = strings.Trim(tok, ".,;:!?'\"")
		if natureWordStemMatch(tok, kw) {
			return true
		}
	}
	return false
}

func transcriptNatureKeywordIndex(paddedUpper, keyword string) int {
	kw := strings.ToUpper(strings.TrimSpace(keyword))
	if kw == "" {
		return -1
	}
	if idx := strings.Index(paddedUpper, " "+kw+" "); idx >= 0 {
		return idx
	}
	if idx := strings.Index(paddedUpper, " "+kw+","); idx >= 0 {
		return idx
	}
	if strings.Contains(kw, " ") {
		return -1
	}
	best := -1
	for _, tok := range strings.Fields(strings.Trim(paddedUpper, " ")) {
		raw := tok
		tok = strings.Trim(tok, ".,;:!?'\"")
		if !natureWordStemMatch(tok, kw) {
			continue
		}
		if idx := strings.Index(paddedUpper, " "+raw); idx >= 0 {
			if best < 0 || idx < best {
				best = idx
			}
		}
	}
	return best
}

// natureWordStemMatch reports whether a transcript token and a configured nature
// keyword share the same medical word stem (DEHYDRATED/DEHYDRATION, INJURED/INJURY).
func natureWordStemMatch(transcriptTok, keyword string) bool {
	a := strings.ToUpper(strings.TrimSpace(transcriptTok))
	b := strings.ToUpper(strings.TrimSpace(keyword))
	if a == b {
		return true
	}
	if len(a) < 5 || len(b) < 5 {
		return false
	}
	longerTok, shorterTok := a, b
	if len(b) > len(a) {
		longerTok, shorterTok = b, a
	}
	if strings.HasPrefix(longerTok, shorterTok) && len(longerTok) > len(shorterTok) &&
		!natureMorphologicalSuffix(longerTok[len(shorterTok):]) {
		return false // COURTESY must not match COURT
	}
	stem := 0
	for stem < len(a) && stem < len(b) && a[stem] == b[stem] {
		stem++
	}
	if stem < 5 {
		return false
	}
	longer := len(a)
	if len(b) > longer {
		longer = len(b)
	}
	// Reject prefix-only overlap (BLACK in BLACK HAWK → BLACKMAIL).
	if longer-stem > 3 {
		return false
	}
	shorter := len(a)
	if len(b) < shorter {
		shorter = len(b)
	}
	// An "-LY" adverb is a different word class than a plural/base noun form:
	// DISORDERLY vs DISORDERS ("seizure disorders" is not "disorderly"),
	// COSTLY vs COSTS. Never treat them as the same stem.
	tailA := a[stem:]
	tailB := b[stem:]
	if (tailA == "LY") != (tailB == "LY") {
		return false
	}
	// Allow only inflectional suffix differences (-ED/-ION/-ING/-NESS/-S).
	// Derivational extras (PERSON+AL, PERSON+NEL) are different words —
	// "SQUAD PERSONNEL" and "INJURED PERSON" must not become PERSONAL.
	return natureInflectionalSuffix(a[stem:]) && natureInflectionalSuffix(b[stem:])
}

// natureInflectionalSuffix is a same-word grammatical ending, not a
// derivational suffix that builds a new lexical item (AL/IVE/OUS/MENT).
func natureInflectionalSuffix(suffix string) bool {
	suffix = strings.ToUpper(strings.TrimSpace(suffix))
	switch suffix {
	case "", "S", "ES", "ED", "ING", "ION", "TION", "SION", "NESS", "ER", "OR", "Y":
		return true
	}
	return false
}

func natureMorphologicalSuffix(suffix string) bool {
	suffix = strings.ToUpper(strings.TrimSpace(suffix))
	if suffix == "" {
		return true
	}
	for _, suf := range []string{
		"NESS", "ING", "ION", "TION", "SION", "ED", "S", "LY", "AL", "ITY",
		"IVE", "OUS", "MENT", "ABLE", "IBLE", "ER", "OR", "IST", "ISM",
	} {
		if suffix == suf {
			return true
		}
	}
	return false
}

// NormalizeNatureToKeywords maps an LLM phrase to the closest configured keyword
// when it clearly contains one (longest match wins). Otherwise the LLM phrase
// is kept verbatim.
func NormalizeNatureToKeywords(nature string, natureCodes []string) string {
	n := strings.ToUpper(strings.TrimSpace(nature))
	if n == "" {
		return ""
	}
	padded := " " + n + " "
	codes := sortedNatureCodes(natureCodes)
	for _, kw := range codes {
		if kw == n {
			return kw
		}
	}
	best := ""
	bestPos := -1
	bestLen := 0
	for _, kw := range codes {
		if kw == "" {
			continue
		}
		if !wholeWordContains(padded, kw) && !natureWordStemMatch(n, kw) {
			continue
		}
		idx := strings.Index(padded, " "+kw+" ")
		if idx < 0 {
			idx = 0
		}
		if bestPos < 0 || idx < bestPos || (idx == bestPos && len(kw) > bestLen) {
			bestPos = idx
			bestLen = len(kw)
			best = kw
		}
	}
	if best != "" {
		return best
	}
	for _, kw := range codes {
		if len(n) >= 4 && kw != "" && strings.Contains(" "+kw+" ", " "+n+" ") {
			return kw
		}
	}
	return n
}

// inferFireworksComplaintNature classifies caller-reported fireworks when the
// transcript names fireworks and reads like a complaint / area check, not fire
// suppression.
func inferFireworksComplaintNature(transcript string) string {
	u := " " + strings.Map(func(r rune) rune {
		switch r {
		case '.', ',', ';', ':', '!', '?', '\'', '"':
			return ' '
		}
		return r
	}, strings.ToUpper(transcript)) + " "
	if !strings.Contains(u, " FIREWORKS ") && !strings.Contains(u, " FIREWORK ") {
		return ""
	}
	for _, cue := range []string{
		" COMPLAINT", " REQUESTING AN OFFICER", " CHECK THE AREA",
		" CALLER ", " REPORTING ", " LIGHTING OFF ", " SHOOTING OFF ",
		" SETTING OFF ",
	} {
		if strings.Contains(u, cue) {
			return "FIREWORKS COMPLAINT"
		}
	}
	return ""
}

var animalDispatchTokens = []string{
	"RACCOON", "DEER", "COYOTE", "SKUNK", "OPOSSUM", "SNAKE", "FOX", "BEAR",
	"DOG", "CAT", "BIRD", "HORSE", "COW", "LIVESTOCK", "SQUIRREL", "GROUNDHOG",
}

// inferAnimalComplaintNature classifies sick/injured wildlife or animal-control
// calls so "INJURED RACCOON" does not become human INJURY.
func inferAnimalComplaintNature(transcript string, natureCodes []string) string {
	u := " " + strings.ToUpper(transcript) + " "
	hasAnimal := false
	for _, a := range animalDispatchTokens {
		if strings.Contains(u, " "+a+" ") {
			hasAnimal = true
			break
		}
	}
	if !hasAnimal {
		return ""
	}
	animalCue := false
	for _, cue := range []string{
		" SICK ", " INJURED ", " HURT ", " ANIMAL ", " WILD ", " BACKYARD ",
		" COMPLAINT ", " POSSIBLY ",
	} {
		if strings.Contains(u, cue) {
			animalCue = true
			break
		}
	}
	if !animalCue {
		return ""
	}
	prefs := []string{"ANIMAL COMPLAINT", "WILD ANIMAL", "ANIMAL", "SICK ANIMAL", "INJURED ANIMAL"}
	for _, p := range prefs {
		if norm := NormalizeNatureToKeywords(p, natureCodes); norm != "" {
			return norm
		}
	}
	for _, kw := range sortedNatureCodes(natureCodes) {
		ku := strings.ToUpper(strings.TrimSpace(kw))
		if strings.Contains(ku, "ANIMAL") {
			return ku
		}
	}
	return ""
}

// inferCourtesyTransportNature labels officer-initiated courtesy rides/transports.
func inferCourtesyTransportNature(transcript string, natureCodes []string) string {
	u := " " + strings.ToUpper(transcript) + " "
	for _, phrase := range []string{"COURTESY TRANSPORT", "COURTESY RIDE"} {
		if strings.Contains(u, " "+phrase+" ") || strings.Contains(u, " "+phrase+" TO ") {
			if norm := NormalizeNatureToKeywords("TRANSPORT PERSON OR PRISONER", natureCodes); norm != "" {
				return norm
			}
			return "TRANSPORT PERSON OR PRISONER"
		}
	}
	return ""
}

func isTransportPersonNature(nature string) bool {
	n := strings.ToUpper(strings.TrimSpace(nature))
	return n == "TRANSPORT PERSON OR PRISONER" || strings.Contains(n, "PRISONER TRANSPORT") ||
		strings.Contains(n, "COURT TRANSPORT")
}

func sortedNatureCodes(natureCodes []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, kw := range natureCodes {
		k := strings.ToUpper(strings.TrimSpace(kw))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// dispatchHarnessMarkers are RealIncidentMarkers that prove a toned dispatch but
// must not become the incident nature label (SQUAD CALL ≠ nature of call).
var dispatchHarnessMarkers = map[string]bool{
	"SQUAD CALL": true, "SQUAW CALL": true, "SWAB CALL": true, "SQUARE CALL": true,
	"2ND SQUAD": true, "SECOND SQUAD": true, "2ND CALL": true, "SECOND CALL": true,
	"MUTUALLY": true,
}

func isDispatchHarnessMarker(marker string) bool {
	return dispatchHarnessMarkers[strings.ToUpper(strings.TrimSpace(marker))]
}

// victimQualifierNature returns the incident type when dispatch qualified a
// victim descriptor ("FALL VICTIM", "FIRE VICTIM") instead of bare "VICTIM".
func victimQualifierNature(paddedUpper string, natureCodes []string) string {
	for _, code := range sortedNatureCodes(natureCodes) {
		c := strings.ToUpper(strings.TrimSpace(code))
		if c == "" || c == "VICTIM" || isDispatchHarnessMarker(c) || isApparatusOnlyNatureLabel(c) || IsGenericNature(c) {
			continue
		}
		if strings.Contains(paddedUpper, " "+c+" VICTIM ") {
			return c
		}
	}
	for _, q := range []string{"FALL", "FIRE", "CRASH", "MVA", "ASSAULT", "SHOOTING", "STABBING", "DROWNING"} {
		if strings.Contains(paddedUpper, " "+q+" VICTIM ") {
			if norm := NormalizeNatureToKeywords(q, natureCodes); norm != "" {
				return norm
			}
			return q
		}
	}
	return ""
}

// inferForADispatchNature recovers the incident type from toned-dispatch phrasing
// ("… FOR A BURNING SMELL") when the opener word is an STT mishear (WARREN→WARRANT).
func inferForADispatchNature(transcript string, natureCodes []string) string {
	padded := " " + strings.ToUpper(transcript) + " "
	for _, lead := range []string{" FOR A ", " FOR AN "} {
		idx := strings.Index(padded, lead)
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(padded[idx+len(lead):])
		for _, sep := range []string{" TIMEOUT", " TIME OUT", " YOUR TIME", " DISPATCH CLEAR", " TIME TO"} {
			if i := strings.Index(rest, sep); i > 0 {
				rest = strings.TrimSpace(rest[:i])
			}
		}
		rest = strings.TrimRight(rest, ".,;")
		if rest == "" {
			continue
		}
		if n := natureFromForAPhrase(rest, natureCodes); n != "" {
			return n
		}
	}
	return ""
}

func stripDispatchDemographicLead(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	for i := 0; i < 3; i++ {
		next := strings.TrimSpace(dispatchDemographicLeadRE.ReplaceAllString(s, ""))
		if next == s {
			break
		}
		s = next
	}
	for _, lead := range []string{"HAVING ", "WITH ", "COMPLAINING OF ", "C/O ", "REPORTING "} {
		if strings.HasPrefix(s, lead) {
			s = strings.TrimSpace(s[len(lead):])
		}
	}
	return s
}

func natureFromForAPhrase(rest string, natureCodes []string) string {
	rest = stripDispatchDemographicLead(rest)
	if rest == "" {
		return ""
	}
	best := ""
	bestLen := 0
	segments := []string{rest}
	for _, seg := range strings.Split(rest, ",") {
		seg = stripDispatchDemographicLead(strings.TrimSpace(seg))
		if seg != "" {
			segments = append(segments, seg)
		}
	}
	for _, seg := range segments {
		paddedSeg := " " + seg + " "
		for _, code := range sortedNatureCodes(natureCodes) {
			ku := strings.ToUpper(strings.TrimSpace(code))
			if ku == "" || isApparatusOnlyNatureLabel(ku) || isDispatchHarnessMarker(ku) {
				continue
			}
			if strings.HasPrefix(seg+" ", ku+" ") || seg == ku || transcriptContainsNatureKeyword(paddedSeg, ku) {
				if len(ku) > bestLen {
					bestLen = len(ku)
					best = ku
				}
			}
		}
	}
	if best != "" {
		return best
	}
	if w := inferWiresTreesDownNature(" "+rest, natureCodes); w != "" {
		return w
	}
	if n := localNature(" "+rest+" ", natureCodes); n != "" {
		return n
	}
	return ""
}

func natureAtTranscriptOpener(nature, transcript string) bool {
	fields := strings.Fields(strings.ToUpper(strings.TrimSpace(transcript)))
	n := strings.ToUpper(strings.TrimSpace(nature))
	if len(fields) == 0 || n == "" {
		return false
	}
	return fields[0] == n
}

// courtNatureExplicitlySpoken reports real court/legal dispatch context — not
// a thoroughfare suffix on an address ("150 CHARLES COURT").
func courtNatureExplicitlySpoken(paddedUpper string) bool {
	for _, cue := range []string{
		" COURT ORDER", " COURT HEARING", " COURT PAPER", " COURT TRANSPORT",
		" GO TO COURT", " AT COURT ", " IN COURT ", " COURTHOUSE",
	} {
		if strings.Contains(paddedUpper, cue) {
			return true
		}
	}
	return false
}

// natureLabelIsStreetSuffixFalsePositive reports when a configured nature label
// only appears as a street suffix in a spoken address (CHARLES COURT ≠ COURT).
func natureLabelIsStreetSuffixFalsePositive(label, paddedUpper string) bool {
	n := strings.ToUpper(strings.TrimSpace(label))
	switch n {
	case "COURT", "CT":
		// Bare COURT is a street suffix (CORKWOOD COURT, MAPLE LEAF COURT)
		// unless dispatch actually said a legal-court cue.
		return !courtNatureExplicitlySpoken(paddedUpper)
	default:
		return false
	}
}

func scrubNatureStreetSuffixFalsePositive(nature, transcript string) string {
	n := strings.ToUpper(strings.TrimSpace(nature))
	if n == "" {
		return ""
	}
	if natureLabelIsStreetSuffixFalsePositive(n, " "+strings.ToUpper(transcript)+" ") {
		return ""
	}
	return nature
}

func defaultUnknownProblemNature(natureCodes []string) string {
	for _, candidate := range []string{
		"UNKNOWN PROBLEM", "EMS UNKNOWN PROBLEM/UNCLASSIFIED", "UNKNOWN PROBLEM/UNCLASSIFIED",
	} {
		if isExactNatureCode(candidate, natureCodes) {
			return strings.ToUpper(strings.TrimSpace(candidate))
		}
		if norm := NormalizeNatureToKeywords(candidate, natureCodes); norm != "" {
			return norm
		}
	}
	return ""
}

// natureKeywordIsNegated reports when dispatch explicitly ruled out a keyword
// ("NO SMOKE OR FIRE") rather than describing an active incident.
func natureKeywordIsNegated(paddedUpper, keyword string) bool {
	kw := strings.ToUpper(strings.TrimSpace(keyword))
	if kw != "FIRE" && kw != "SMOKE" {
		return false
	}
	if strings.Contains(paddedUpper, " NO SMOKE OR FIRE") {
		return true
	}
	for _, prefix := range []string{" NO ", " NOT "} {
		if strings.Contains(paddedUpper, prefix+kw+" ") ||
			strings.Contains(paddedUpper, prefix+kw+".") ||
			strings.Contains(paddedUpper, prefix+kw+",") {
			return true
		}
	}
	return false
}

// transcriptDescribesWiresTreesDown reports utility line or tree-on-roadway hazards.
func transcriptDescribesWiresTreesDown(transcript string) bool {
	u := " " + strings.ToUpper(transcript) + " "
	for _, cue := range []string{
		" WIRES DOWN", " WIRE DOWN", " TREE DOWN", " TREES DOWN", " TREEDOWN",
		" TORE DOWN A WIRE", " TORE DOWN THE WIRE", " KNOCKED DOWN A WIRE", " PULLED DOWN A WIRE",
		" POWER LINE DOWN", " STRUCK A POWER LINE", " HIT A POWER LINE",
		" WIRES ARE ON", " LINES DOWN", " LINE DOWN",
		" POWER LINES ARE ON", " POWER LINES ON THE GROUND",
		" POWER LINE ON THE GROUND", " LINES ARE ON THE GROUND", " LINES ON THE GROUND",
		" TREE ACROSS THE ROADWAY", " TREE ACROSS THE ROAD", " TREE ACROSS ROAD",
	} {
		if strings.Contains(u, cue) {
			return true
		}
	}
	if strings.Contains(u, " POWER LINE") &&
		(strings.Contains(u, " CUT DOWN A TREE") || strings.Contains(u, " CUT DOWN THE TREE") ||
			strings.Contains(u, " TREE HAS FALLEN") || strings.Contains(u, " TREE FELL")) {
		return true
	}
	return false
}

// inferWiresTreesDownNature recovers utility line / tree-on-wire dispatches.
func inferWiresTreesDownNature(transcript string, natureCodes []string) string {
	if !transcriptDescribesWiresTreesDown(transcript) {
		return ""
	}
	if norm := NormalizeNatureToKeywords("WIRES/TREES DOWN", natureCodes); norm != "" {
		return norm
	}
	return "WIRES/TREES DOWN"
}

// inferSpecificDispatchNature recovers a real incident type from dispatch
// phrasing when keyword lists only matched an apparatus token (LADDER 12, etc.).
func inferSpecificDispatchNature(transcript string, natureCodes []string) string {
	if wires := inferWiresTreesDownNature(transcript, natureCodes); wires != "" {
		return wires
	}
	u := strings.ToUpper(transcript)
	padded := " " + u + " "
	if ilpersonNatureRE.MatchString(u) || strings.Contains(padded, " ILL PERSON ") ||
		strings.Contains(u, "ILPERSON") || strings.Contains(u, "ILLPERSON") {
		return "ILL PERSON"
	}
	if strings.Contains(padded, " COMPLAINT ABOUT ") || strings.Contains(padded, " COMPLAINT REGARDING ") ||
		strings.Contains(padded, " COMPLAINT FOR ") {
		for _, kw := range sortedNatureCodes(natureCodes) {
			ku := strings.ToUpper(strings.TrimSpace(kw))
			if strings.Contains(ku, "COMPLAINT") && wholeWordContains(padded, ku) {
				return ku
			}
		}
		if norm := NormalizeNatureToKeywords("NOISE COMPLAINT", natureCodes); norm != "" {
			return norm
		}
		if wholeWordContains(padded, "COMPLAINT") {
			return "COMPLAINT"
		}
	}
	if strings.Contains(padded, " PARKING COMPLAINT") || strings.Contains(padded, " HAVE A PARKING COMPLAINT") {
		if norm := NormalizeNatureToKeywords("PARKING COMPLAINT", natureCodes); norm != "" {
			return norm
		}
		for _, kw := range sortedNatureCodes(natureCodes) {
			ku := strings.ToUpper(strings.TrimSpace(kw))
			if strings.Contains(ku, "PARKING") && strings.Contains(ku, "COMPLAINT") && wholeWordContains(padded, ku) {
				return ku
			}
		}
	}
	if fallIncidentContextRE.MatchString(u) {
		if wholeWordContains(padded, "FALL PATIENT") {
			return "FALL PATIENT"
		}
		return "FALL"
	}
	for _, phrase := range []string{"EXTREME WEAKNESS", "GENERALIZED WEAKNESS", "SUDDEN WEAKNESS"} {
		if strings.Contains(padded, " "+phrase) {
			if norm := NormalizeNatureToKeywords("WEAKNESS", natureCodes); norm != "" {
				return norm
			}
			return "WEAKNESS"
		}
	}
	if strings.Contains(padded, " WEAKNESS") {
		if norm := NormalizeNatureToKeywords("WEAKNESS", natureCodes); norm != "" {
			return norm
		}
		return "WEAKNESS"
	}
	if strings.Contains(padded, " AUTO ACCIDENT") || strings.Contains(padded, " MOTOR VEHICLE ACCIDENT") ||
		strings.Contains(padded, " MVA ") {
		for _, label := range []string{
			"CRASH WITH REPORTED INJURIES", "CRASH PROPERTY DAMAGE", "MVA", "ACCIDENT",
		} {
			if norm := NormalizeNatureToKeywords(label, natureCodes); norm != "" {
				return norm
			}
		}
	}
	if strings.Contains(padded, " TROUBLE ALARM") {
		if norm := NormalizeNatureToKeywords("TROUBLE ALARM", natureCodes); norm != "" {
			return norm
		}
		return "TROUBLE ALARM"
	}
	if strings.Contains(padded, " CONCERNED WITH HIS WELFARE") ||
		strings.Contains(padded, " CONCERNED WITH HER WELFARE") ||
		strings.Contains(padded, " CONCERNED WITH THEIR WELFARE") {
		if norm := NormalizeNatureToKeywords("WELFARE CHECK", natureCodes); norm != "" {
			return norm
		}
		return "WELFARE CHECK"
	}
	if strings.Contains(padded, " REFUSING TO LEAVE") ||
		strings.Contains(padded, " REFUSING, AND NOW") ||
		strings.Contains(padded, " ASKED TO LEAVE") {
		if norm := NormalizeNatureToKeywords("TRESPASS", natureCodes); norm != "" {
			return norm
		}
		if norm := NormalizeNatureToKeywords("DISTURBANCE", natureCodes); norm != "" {
			return norm
		}
		return "DISTURBANCE"
	}
	// Re-run keyword inference without apparatus-only tokens.
	filtered := make([]string, 0, len(natureCodes))
	for _, kw := range natureCodes {
		k := strings.ToUpper(strings.TrimSpace(kw))
		if k == "" || isApparatusOnlyNatureLabel(k) || isDispatchHarnessMarker(k) {
			continue
		}
		filtered = append(filtered, kw)
	}
	if n := localNature(u, filtered); n != "" && !isApparatusOnlyNatureLabel(n) && !isDispatchHarnessMarker(n) {
		return n
	}
	best := ""
	bestLen := 0
	for _, m := range RealIncidentMarkers {
		mk := strings.TrimSpace(strings.ToUpper(m))
		if mk == "" || isDispatchHarnessMarker(mk) {
			continue
		}
		if strings.Contains(padded, mk) {
			if best == "" || len(mk) > bestLen {
				best = mk
				bestLen = len(mk)
			}
		}
	}
	if best != "" {
		if best == "FELL" {
			return "FALL"
		}
		return best
	}
	return ""
}

var chiefComplaintNatureRE = regexp.MustCompile(`(?i)(?:CHIEF COMPLAINT|COMPLAINT TODAY)(?:\s+IS)?(?:\s+GOING TO BE)?\s+([A-Z][A-Z\s/\-]{2,40})`)

// inferChiefComplaintNature recovers the medic-stated chief complaint over vitals noise.
func inferChiefComplaintNature(transcript string, natureCodes []string) string {
	m := chiefComplaintNatureRE.FindStringSubmatch(strings.ToUpper(transcript))
	if len(m) < 2 {
		return ""
	}
	phrase := strings.TrimSpace(m[1])
	phrase = strings.TrimRight(phrase, ".,;")
	for _, tail := range []string{" HE SAID", " SHE SAID", " HE DID", " SHE DID", " RIGHT NOW"} {
		if idx := strings.Index(phrase, tail); idx > 0 {
			phrase = strings.TrimSpace(phrase[:idx])
		}
	}
	if phrase == "" {
		return ""
	}
	if norm := NormalizeNatureToKeywords(phrase, natureCodes); norm != "" {
		return norm
	}
	return phrase
}

// FinalizeIncidentNature validates, scrubs, and optionally normalizes nature.
// When the LLM/rule output is empty, keyword-list inference runs only when the
// transcript looks like real incident or relay traffic (not pure sign-off).
func FinalizeIncidentNature(nature, transcript string, natureCodes []string, isDispatch bool) string {
	return FinalizeIncidentNatureWithMap(nature, transcript, natureCodes, nil, isDispatch, nil, false)
}

// squadCallDispatchRE matches a "squad call" EMS tone-out marker, tolerating the
// common STT manglings of "squad" ("squaw", "squawk", "squall") and of "call"
// ("crawl", "hall"). It deliberately requires the trailing call word so an
// apparatus reference ("SQUAD 23", "SQUAD RESPONDING") is never mistaken for a
// tone-out. A squad tone-out is an EMS response even when the dispatcher speaks
// only a location and no ailment.
var squadCallDispatchRE = regexp.MustCompile(`(?i)\bSQUA(?:D|WK?|LL)\s+(?:CALL|CRAWL|HALL)\b`)

// forThatMedicalRE matches Trumbull-style EMS framing without the words
// "squad call" ("STATION 7 FOR THAT MEDICAL AT 605 WEST 3RD").
var forThatMedicalRE = regexp.MustCompile(`(?i)\bFOR\s+(?:THAT|THE|A)\s+MEDICAL\b`)

// inferSquadCallEMSNature returns the configured generic EMS label when the
// transcript is a squad (EMS) tone-out but nothing more specific classified.
// Used only as a last resort, just before the UNKNOWN PROBLEM catch-all, so a
// location-only squad dispatch ("STATION 28 SQUAD CALL, HILLVIEW DRIVE") lands
// on a real EMS nature instead of unknown.
func inferSquadCallEMSNature(transcript string, natureCodes []string) string {
	if !squadCallDispatchRE.MatchString(transcript) && !forThatMedicalRE.MatchString(transcript) {
		return ""
	}
	const ems = "EMERGENCY MEDICAL ASSISTANCE"
	if isExactNatureCode(ems, natureCodes) {
		return ems
	}
	return ""
}

func FinalizeIncidentNatureWithMap(nature, transcript string, natureCodes, matchTerms []string, isDispatch bool, phraseToLabel map[string]string, deferDefaultUnknown bool) string {
	match := effectiveMatchTerms(natureCodes, matchTerms)
	u := strings.ToUpper(transcript)
	if strings.Contains(u, "INJURED IN FALL") {
		nature = "FALL PATIENT"
	}
	n := ScrubIncidentNature(nature, transcript)
	n = CanonicalizeNatureLabel(n, phraseToLabel)
	n = NormalizeNatureToKeywords(n, natureCodes)
	n = clampNatureLabel(n, natureCodes)
	if forA := inferForADispatchNature(transcript, natureCodes); forA != "" {
		if n == "" || natureAtTranscriptOpener(n, transcript) ||
			strings.EqualFold(n, "LAKE EMERGENCY") ||
			natureKeywordIsLakeDepartmentFalsePositive(" "+u+" ", n) {
			n = forA
		}
	}
	if wires := inferWiresTreesDownNature(transcript, natureCodes); wires != "" {
		if n == "" || !isExactNatureCode(n, natureCodes) ||
			strings.EqualFold(n, "LAKE EMERGENCY") ||
			natureKeywordIsLakeDepartmentFalsePositive(" "+u+" ", n) {
			n = wires
		}
	}
	// Evidence-weighted correction: when another configured nature explains
	// strictly more transcript words than the working label, the words win
	// ("POWER LINES HANGING VERY LOW" is utility-line evidence, not a lone
	// gerund match). A scored pick is anchored by construction — its
	// configured phrases were found in the transcript.
	scoredAnchored := false
	if scored := scoredNatureOverride(n, transcript, natureCodes, match, phraseToLabel); scored != "" {
		n = scored
		scoredAnchored = true
	} else if natureScoredEvidenceConfirms(n, transcript, natureCodes, match, phraseToLabel) {
		scoredAnchored = true
	}
	if !scoredAnchored && !natureAnchoredInTranscript(n, transcript) {
		n = ""
	}
	if IsGenericNature(n) {
		n = ""
	}
	if n == "" || isApparatusOnlyNatureLabel(n) {
		if specific := inferSpecificDispatchNature(transcript, natureCodes); specific != "" {
			n = specific
			n = ScrubIncidentNature(n, transcript)
			n = NormalizeNatureToKeywords(n, natureCodes)
			n = clampNatureLabel(n, natureCodes)
		} else if cardiac := inferCardiacSymptomNature(transcript, natureCodes); cardiac != "" {
			n = cardiac
		} else if isApparatusOnlyNatureLabel(n) {
			n = ""
		}
	}
	if cardiac := inferCardiacSymptomNature(transcript, natureCodes); cardiac != "" &&
		(n == "" || isApparatusOnlyNatureLabel(n) || isLawEnforcementCrimeNature(n)) {
		n = cardiac
	}
	if seizure := inferSeizureActivityNature(transcript, natureCodes); seizure != "" {
		if n == "" || isApparatusOnlyNatureLabel(n) || strings.EqualFold(n, "CONSCIOUSNESS") ||
			strings.Contains(strings.ToUpper(n), "CONSCIOUS") {
			n = seizure
		}
	}
	if suicide := inferSuicideNature(transcript, natureCodes); suicide != "" {
		if n == "" || isApparatusOnlyNatureLabel(n) {
			n = suicide
		}
	}
	if n == "" && shouldInferNatureFromTranscript(transcript, isDispatch) {
		inferred := localNature(strings.ToUpper(transcript), match)
		n = ScrubIncidentNature(inferred, transcript)
		// A matched phrase belongs to its configured parent label; map it
		// before keyword containment can pick a shorter homonym code
		// ("POWER LINES HANGING" is a utility phrase, not the HANGING code).
		n = CanonicalizeNatureLabel(n, phraseToLabel)
		n = NormalizeNatureToKeywords(n, natureCodes)
		n = clampNatureLabel(n, natureCodes)
		// Spoken "UNKNOWN PROBLEM" is a catch-all, not a classified complaint —
		// clear it so squad EMS / scored evidence can still assign a real label.
		if isBareCatchAllUnknownNature(n) {
			n = ""
		}
		if scored := scoredNatureOverride(n, transcript, natureCodes, match, phraseToLabel); scored != "" {
			n = scored
			scoredAnchored = true
		} else if natureScoredEvidenceConfirms(n, transcript, natureCodes, match, phraseToLabel) {
			scoredAnchored = true
		}
		if !scoredAnchored && !natureAnchoredInTranscript(n, transcript) {
			n = ""
		}
		if isApparatusOnlyNatureLabel(n) {
			if specific := inferSpecificDispatchNature(transcript, natureCodes); specific != "" {
				n = ScrubIncidentNature(specific, transcript)
				n = NormalizeNatureToKeywords(n, natureCodes)
				n = clampNatureLabel(n, natureCodes)
			} else {
				n = ""
			}
		}
	}
	if fw := inferFireworksComplaintNature(transcript); fw != "" {
		n = fw
	}
	if animal := inferAnimalComplaintNature(transcript, natureCodes); animal != "" {
		n = animal
	}
	// Late evidence-weighted pass: inference helpers above can settle on a
	// label matched from a single word; when another configured nature
	// explains strictly more transcript words, the words win.
	if scored := scoredNatureOverride(n, transcript, natureCodes, match, phraseToLabel); scored != "" {
		n = scored
	}
	// Nothing settled yet — take the best complaint evidence (response-mode
	// demoted) before falling through to squad EMS / UNKNOWN catch-alls.
	if strings.TrimSpace(n) == "" {
		if best := bestScoredNature(transcript, natureCodes, match, phraseToLabel, 1); best != "" {
			n = best
		}
	}
	if natureLoneGerundEvidence(n, transcript, natureCodes, match, phraseToLabel) {
		n = ""
	}
	if TranscriptIsRadioChatter(transcript) && !containsRealIncidentMarker(" "+strings.ToUpper(transcript)+" ") {
		n = ""
	}
	if TranscriptIsPatchInService(transcript) || TranscriptIsUnitOnSiteStatus(transcript) ||
		TranscriptIsMedicEnRouteUpdate(transcript) || TranscriptIsUnitEnRouteBriefing(transcript) ||
		TranscriptIsOfficerDirectedVisit(transcript) || TranscriptIsHospitalBedCoordination(transcript) {
		n = ""
	}
	if TranscriptIsTransportNarrative(transcript) {
		if tp := inferCourtesyTransportNature(transcript, natureCodes); tp != "" {
			n = tp
		} else if n != "" && !isTransportPersonNature(n) {
			n = ""
		}
	}
	if strings.Contains(strings.ToUpper(n), "BLOOD PRESSURE") {
		if chief := inferChiefComplaintNature(transcript, natureCodes); chief != "" {
			n = chief
		} else if strings.Contains(u, "ON THE MONITOR") || strings.Contains(u, "HEART RATE") {
			n = ""
		}
	}
	if IsGenericNature(n) {
		return ""
	}
	if strings.EqualFold(n, "INJURED") && strings.Contains(u, "INJURED IN FALL") {
		n = "FALL PATIENT"
	}
	if strings.EqualFold(n, "VICTIM") {
		if q := victimQualifierNature(" "+u+" ", natureCodes); q != "" {
			n = q
		}
	}
	n = harmonizeNatureSynonyms(n, transcript, natureCodes)
	n = scrubNatureStreetSuffixFalsePositive(n, transcript)
	if !deferDefaultUnknown && strings.TrimSpace(n) == "" && shouldInferNatureFromTranscript(transcript, isDispatch) {
		if ems := inferSquadCallEMSNature(transcript, natureCodes); ems != "" {
			n = ems
		} else {
			n = defaultUnknownProblemNature(natureCodes)
		}
	}
	n = CanonicalizeNatureLabel(n, phraseToLabel)
	// Every classified call must land on a configured nature code. Map stray
	// labels through phrase evidence and keyword containment; when nothing in
	// the catalog fits, fall back to the configured catch-all instead of
	// publishing an invented label.
	if strings.TrimSpace(n) != "" && len(natureCodes) > 0 && !isExactNatureCode(n, natureCodes) {
		if norm := NormalizeNatureToKeywords(n, natureCodes); isExactNatureCode(norm, natureCodes) {
			n = norm
		} else if scored := bestScoredNature(transcript, natureCodes, match, phraseToLabel, 2); scored != "" &&
			isExactNatureCode(scored, natureCodes) {
			n = scored
		} else if !deferDefaultUnknown && shouldInferNatureFromTranscript(transcript, isDispatch) {
			n = defaultUnknownProblemNature(natureCodes)
		} else {
			n = ""
		}
	}
	return n
}

// DefaultUnknownProblemLabel returns the configured catch-all nature label.
func DefaultUnknownProblemLabel(natureCodes []string) string {
	return defaultUnknownProblemNature(natureCodes)
}

// ShouldApplyUnknownNatureFallback reports whether a transcript should receive
// the catch-all unknown label when no specific category matched.
func ShouldApplyUnknownNatureFallback(transcript string) bool {
	if TranscriptIsLicensePlateReadout(transcript) {
		return false
	}
	if transcriptIsIncompleteSquadOpener(transcript) || transcriptIsBareUnitStatus(transcript) {
		return false
	}
	if TranscriptIsPhoneticAlphabetRollCall(transcript) {
		return false
	}
	return shouldInferNatureFromTranscript(transcript, false)
}

// ShouldEnqueueCallNatureBackfill reports whether a call should enter the
// nature backfill queue (UNKNOWN relabel, or empty nature with incident signal).
func ShouldEnqueueCallNatureBackfill(oldNature, transcript string) bool {
	old := strings.TrimSpace(oldNature)
	if IsDefaultUnknownNatureLabel(old) {
		return true
	}
	if old != "" {
		return false
	}
	if TranscriptIsLicensePlateReadout(transcript) {
		return false
	}
	if TranscriptIsRadioChatter(transcript) && !containsRealIncidentMarker(" "+strings.ToUpper(transcript)+" ") {
		return false
	}
	words := strings.Fields(strings.TrimSpace(transcript))
	if len(words) <= 2 && !containsRealIncidentMarker(" "+strings.ToUpper(transcript)+" ") {
		return false
	}
	return shouldInferNatureFromTranscript(transcript, false)
}

// SanitizeCallNatureAssignment clears catch-all, chatter, and incomplete-opener
// labels before persisting call nature from rules or OpenAI.
func SanitizeCallNatureAssignment(transcript, nature string, natureCodes []string) string {
	nature = strings.ToUpper(strings.TrimSpace(nature))
	if nature == "" {
		return ""
	}
	// Bare UNKNOWN PROBLEM is a last-resort catch-all. EMS UNKNOWN is a real
	// classified label ("UNKNOWN MEDICAL") and must not be rewritten to the
	// generic UNKNOWN PROBLEM default.
	if isBareCatchAllUnknownNature(nature) {
		if ShouldApplyUnknownNatureFallback(transcript) {
			return DefaultUnknownProblemLabel(natureCodes)
		}
		return ""
	}
	u := " " + strings.ToUpper(transcript) + " "
	if TranscriptIsLicensePlateReadout(transcript) {
		return ""
	}
	if TranscriptIsRadioChatter(transcript) && !containsRealIncidentMarker(u) {
		return ""
	}
	if transcriptIsIncompleteSquadOpener(transcript) {
		return ""
	}
	if transcriptIsBareUnitStatus(transcript) {
		return ""
	}
	if corrected := correctFalseMedicalAlarmNature(u, nature, natureCodes); corrected != nature {
		return corrected
	}
	if strings.EqualFold(nature, "ALLERGIC REACTION") && !allergicEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "BREAK") && natureKeywordIsRadioBreakFalsePositive(u, "BREAK") {
		return ""
	}
	if strings.EqualFold(nature, "PERSONAL") && !personalNatureEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "CHECK WELFARE") && !welfareEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "PERSON WITH GUN") && natureKeywordIsBareGunFalsePositive(u, "GUN") {
		return ""
	}
	if strings.EqualFold(nature, "PERSON WITH KNIFE") && knifeMentionIsNotAThreat(u) {
		if knifeBodyCutSpoken(u) {
			if label := clampNatureLabel("BLEEDING", natureCodes); label != "" {
				return label
			}
		}
		if knifeSelfHarmSpoken(u) {
			if label := clampNatureLabel("SUICIDE", natureCodes); label != "" {
				return label
			}
		}
		return ""
	}
	if knifeSelfHarmSpoken(u) && !knifeThreatFrame(u) && strings.EqualFold(nature, "THREATS") {
		if label := clampNatureLabel("SUICIDE", natureCodes); label != "" {
			return label
		}
	}
	if strings.EqualFold(nature, "PURSUIT") && natureKeywordIsChaseUnitOrStreet(u, "CHASE") &&
		!strings.Contains(u, " PURSUIT ") {
		return ""
	}
	if strings.EqualFold(nature, "LUNCH") && natureKeywordIsUnitLunch(u, "LUNCH") {
		return ""
	}
	if strings.EqualFold(nature, "CELL PHONE CALLS") && natureKeywordIsOfficerCellOrProperty(u, "CELL PHONE") {
		return ""
	}
	if strings.EqualFold(nature, "HANGING") && natureKeywordIsHangingFalsePositive(u, "HANGING") {
		return ""
	}
	if strings.EqualFold(nature, "LIFT ASSIST") && !liftAssistEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "FALL") &&
		(natureKeywordIsFallsPlaceName(u, "FALL") || natureKeywordIsFallenStreetName(u, "FALLEN")) {
		return ""
	}
	if strings.EqualFold(nature, "SHORTNESS OF BREATH") && !breathingDifficultyEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "STANDBY") && natureKeywordIsRadioStandbyFalsePositive(u, "STANDBY") {
		return ""
	}
	if strings.EqualFold(nature, "MENTAL") && mentalStatusOnlyInTranscript(u) {
		if label := clampNatureLabel("ALTERED LEVEL OF CONSCIOUSNESS", natureCodes); label != "" {
			return label
		}
		return ""
	}
	if strings.EqualFold(nature, "ELECTRICAL FIRE") && !electricalFireEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "STRUCTURE FIRE") && !structureFireEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "FIRE ALARM DROP") && natureKeywordIsCarAlarmFalsePositive(u, "FIRE ALARM") {
		return ""
	}
	if strings.EqualFold(nature, "HEART PROBLEM") && !heartProblemEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "FRACTURE") && natureKeywordIsBrokenPropertyFalsePositive(u, "BROKEN") {
		return ""
	}
	if strings.EqualFold(nature, "CRASH WITH REPORTED INJURIES") && natureKeywordIsCrashUnitOrGasLineFalsePositive(u, "CRASH") {
		return ""
	}
	if strings.EqualFold(nature, "BURNS") {
		if strings.Contains(u, " OPEN BURN ") || strings.Contains(u, " BURNING COMPLAINT ") {
			if label := clampNatureLabel("BURNING COMPLAINT", natureCodes); label != "" {
				return label
			}
			return ""
		}
		if !burnInjuryEvidenceInTranscript(u) {
			return ""
		}
	}
	if strings.EqualFold(nature, "DOA") && !doaEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "DUMPSTER FIRE") && !dumpsterFireEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "ATV/DIRTBIKE") && !atvDirtbikeEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "DRUG ACTIVITY") && natureKeywordIsDrugMartFalsePositive(u, "DRUGS") {
		return ""
	}
	if strings.EqualFold(nature, "OB/GYN") {
		if strings.Contains(u, " LABORED BREATHING ") &&
			!strings.Contains(u, " PREGNANT ") && !strings.Contains(u, " IN LABOR ") {
			if label := clampNatureLabel("SHORTNESS OF BREATH", natureCodes); label != "" {
				return label
			}
			return ""
		}
		if natureKeywordIsNonObstetricLaborFalsePositive(u, "LABOR") {
			return ""
		}
	}
	if strings.EqualFold(nature, "STROKE") &&
		(strings.Contains(u, " HEAT STROKE ") || strings.Contains(u, " HEATSTROKE ") || strings.Contains(u, " SUN STROKE ")) &&
		!strings.Contains(u, " STROKE SYMPTOMS ") && !strings.Contains(u, " CVA ") {
		if label := clampNatureLabel("GENERAL ILLNESS", natureCodes); label != "" {
			return label
		}
		return ""
	}
	if strings.EqualFold(nature, "UNCONSCIOUS/FAINTED") && !unconsciousFaintedEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "FIGHT") &&
		(strings.Contains(u, " TWO DOGS ") || strings.Contains(u, " DOGS ARE ") || strings.Contains(u, " DOG FIGHT ")) {
		if label := clampNatureLabel("ANIMAL COMPLAINT", natureCodes); label != "" {
			return label
		}
		return ""
	}
	if strings.EqualFold(nature, "ASSAULT") && natureKeywordIsElectricBatteryFalsePositive(u, "BATTERY") &&
		!strings.Contains(u, " ASSAULT ") {
		return ""
	}
	if strings.EqualFold(nature, "DRILL") && strings.Contains(u, " ROTC DRILL ") {
		return ""
	}
	if (strings.EqualFold(nature, "ACTIVE SHOOTER") || strings.EqualFold(nature, "ACTIVE SHOOTER INCIDENT")) &&
		strings.Contains(u, " THIS IS A DRILL ") {
		if label := clampNatureLabel("DRILL", natureCodes); label != "" {
			return label
		}
		return ""
	}
	if strings.EqualFold(nature, "CHIMNEY FIRE") && !chimneyFireEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "BOMB THREAT") && !bombThreatEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "HOMICIDE OR POSSIBLE HOMICIDE") {
		if homicidalIdeationInTranscript(u) {
			if label := clampNatureLabel("MENTAL", natureCodes); label != "" {
				return label
			}
			return ""
		}
		if !homicideEvidenceInTranscript(u) {
			return ""
		}
	}
	if strings.EqualFold(nature, "SEX OFFENSE") && natureKeywordIsSexOffenderRegistryFalsePositive(u, "SEX OFFENSE") {
		return ""
	}
	if strings.EqualFold(nature, "ABDUCTION OR KIDNAPPING") && natureKeywordIsKidnapMetaphorFalsePositive(u, "KIDNAP") {
		return ""
	}
	if strings.EqualFold(nature, "DROWNING") && strings.Contains(u, " DOG FELL IN THE POOL ") {
		if label := clampNatureLabel("ANIMAL COMPLAINT", natureCodes); label != "" {
			return label
		}
		return ""
	}
	if strings.EqualFold(nature, "SHOTS FIRED") && natureKeywordIsLongShot(u, "SHOTS") {
		return ""
	}
	if strings.EqualFold(nature, "FIRE ALARM DROP") && !fireAlarmEvidenceInTranscript(u) &&
		securityAlarmEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "CARDIAC ARREST") && !cardiacArrestEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "LAKE EMERGENCY") && !lakeEmergencyEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "PUMPS") && !pumpsIncidentEvidenceInTranscript(u) {
		return ""
	}
	if strings.EqualFold(nature, "BREAK") &&
		(strings.Contains(u, " BREAK IN") || strings.Contains(u, " BREAK-IN") ||
			strings.Contains(u, " BREAK INTO ") || strings.Contains(u, " BREAKING AND ENTERING")) {
		if label := clampNatureLabel("BREAKING AND ENTERING", natureCodes); label != "" {
			return label
		}
		if label := clampNatureLabel("BURGLARY", natureCodes); label != "" {
			return label
		}
		return ""
	}
	return nature
}

func allergicEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" ALLERGIC ", " ALLERGY ", " ALLERGIES ", " ANAPHYLAXIS ", " ANAPHYLACTIC ",
		" BEE STING ", " BEE STINGS ", " STUNG ", " EPINEPHRINE ", " EPI PEN ", " EPIPEN ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func personalNatureEvidenceInTranscript(paddedUpper string) bool {
	return strings.Contains(paddedUpper, " PERSONAL ") &&
		!strings.Contains(paddedUpper, " PERSONNEL ") &&
		!strings.Contains(paddedUpper, " PERSONAL ALARM ") &&
		!strings.Contains(paddedUpper, " PERSONAL INJURY ") &&
		!strings.Contains(paddedUpper, " ACTING PERSONAL ") &&
		!strings.Contains(paddedUpper, " PERSONAL ITEMS ") &&
		!strings.Contains(paddedUpper, " PERSONAL SERVICE ") &&
		!strings.Contains(paddedUpper, " QUICK PERSONAL ") &&
		!strings.Contains(paddedUpper, " ON A PERSONAL ")
}

func welfareEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" WELFARE ", " WELL BEING ", " WELL-BEING ", " WELLBEING ",
		" WELLNESS CHECK ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

// natureKeywordIsAreaCheckFalsePositive rejects "CHECK THE AREA" / bare
// "CHECK ON" when dispatch never said welfare or well-being.
func natureKeywordIsAreaCheckFalsePositive(paddedUpper, keyword string) bool {
	switch strings.ToUpper(strings.TrimSpace(keyword)) {
	case "CHECK ON", "CHECK THE AREA", "CHECK FOR HER", "CHECK FOR HIM",
		"CHECK ON THE CALLER", "CHECK WELFARE":
		return !welfareEvidenceInTranscript(paddedUpper)
	default:
		return false
	}
}

// natureKeywordIsKnifeCutNotThreat rejects "WITH A KNIFE" when the knife is
// how someone cut themselves or a piece of property. PERSON WITH KNIFE is a
// person threatening someone else (pulled a knife, armed, chasing, domestic).
func natureKeywordIsKnifeCutNotThreat(paddedUpper, keyword string) bool {
	switch strings.ToUpper(strings.TrimSpace(keyword)) {
	case "WITH A KNIFE", "WITH THE KNIFE":
	default:
		return false
	}
	return knifeMentionIsNotAThreat(paddedUpper)
}

// knifeMentionIsNotAThreat is true when a knife is a cut, self-harm, or
// property damage, and nobody is threatening another person with it.
func knifeMentionIsNotAThreat(paddedUpper string) bool {
	if knifeThreatFrame(paddedUpper) {
		return false
	}
	return knifeBodyCutSpoken(paddedUpper) || knifeSelfHarmSpoken(paddedUpper) || knifeUsedOnProperty(paddedUpper)
}

func knifeThreatFrame(padded string) bool {
	for _, cue := range []string{
		" ARMED WITH ",
		" HAS A KNIFE ", " HAD A KNIFE ", " HAVE A KNIFE ",
		" PULLED A KNIFE ", " PULLED THE KNIFE ", " PULL A KNIFE ", " PULLING A KNIFE ",
		" KNIFE OUT ", " KNIFE IN HIS HAND ", " KNIFE IN HER HAND ", " KNIFE IN HAND ",
		" CHASING ", " CHASED ",
		" WIELDING ", " BRANDISH",
		" DOMESTIC ",
		" ATTACKED ", " ATTACKING ", " ATTACK ",
		" STABBED HIM ", " STABBED HER ", " STABBED THE ", " STABBED A ",
		" STAB HIM ", " STAB HER ", " STAB THE ", " STAB THEM ",
		" TO STAB ", " STABBING ",
		" FIGHT ", " FIGHTING ",
		" PERSON WITH A KNIFE ", " PERSON WITH THE KNIFE ",
		" MAN WITH A KNIFE ", " MAN WITH THE KNIFE ",
		" WOMAN WITH A KNIFE ", " WOMAN WITH THE KNIFE ",
		" MALE WITH A KNIFE ", " MALE WITH THE KNIFE ",
		" FEMALE WITH A KNIFE ", " FEMALE WITH THE KNIFE ",
		" SUBJECT WITH A KNIFE ", " SUBJECT WITH THE KNIFE ",
		" SOMEONE WITH A KNIFE ", " SOMEONE WITH THE KNIFE ",
		" GUY WITH A KNIFE ", " KID WITH A KNIFE ",
		" THREATENING WITH ", " THREATENED WITH ",
		" THREATENING TO STAB ", " THREATENED TO STAB ",
		" THREATENING HIM ", " THREATENING HER ", " THREATENING THEM ",
		" POINTING A KNIFE ", " POINTED A KNIFE ",
	} {
		if strings.Contains(padded, cue) {
			return true
		}
	}
	return false
}

func knifeBodyCutSpoken(padded string) bool {
	cut := strings.Contains(padded, " CUT ") || strings.Contains(padded, " CUTS ") ||
		strings.Contains(padded, " CUTTING ") || strings.Contains(padded, " SLICED ") ||
		strings.Contains(padded, " SLICE ") || strings.Contains(padded, " SLICING ") ||
		strings.Contains(padded, " LACERAT")
	if !cut {
		return false
	}
	for _, part := range []string{
		"FINGER", "FINGERS", "THUMB", "THUMBS", "HAND", "HANDS", "PALM", "KNUCKLE",
		"WRIST", "ARM", "FOREARM", "LEG", "TOE", "TOES", "FOOT", "FEET",
	} {
		if strings.Contains(padded, " "+part+" ") {
			return true
		}
	}
	return false
}

func knifeSelfHarmSpoken(padded string) bool {
	for _, cue := range []string{
		" KNIFE TO HER NECK ", " KNIFE TO HIS NECK ", " KNIFE TO THEIR NECK ",
		" KNIFE TO HER THROAT ", " KNIFE TO HIS THROAT ", " KNIFE TO THEIR THROAT ",
		" CUTTING HIMSELF ", " CUTTING HERSELF ", " CUTTING THEMSELVES ",
		" CUT HERSELF ", " CUT HIMSELF ",
		" HARMING HERSELF ", " HARMING HIMSELF ", " HARMING THEMSELVES ",
	} {
		if strings.Contains(padded, cue) {
			return true
		}
	}
	return false
}

func knifeUsedOnProperty(padded string) bool {
	return strings.Contains(padded, " TIRE ") || strings.Contains(padded, " TIRES ")
}

// natureKeywordIsBareGunFalsePositive rejects the word GUN unless a person is
// armed (NCIC "gun check clear" and "gunshot" traffic are not this nature).
func natureKeywordIsBareGunFalsePositive(paddedUpper, keyword string) bool {
	switch strings.ToUpper(strings.TrimSpace(keyword)) {
	case "GUN", "GUNS":
	default:
		return false
	}
	for _, ev := range []string{
		" WITH A GUN ", " WITH GUN ", " WITH GUNS ", " PULLED A GUN ",
		" WAVING A GUN ", " MAN WITH GUN ", " PERSON WITH GUN ",
		" SUBJECT WITH GUN ", " ARMED SUBJECT ", " HAS A GUN ",
		" HAS THE GUN ", " HAS HIS GUN ", " HAS HER GUN ", " GUN OUT ",
		" GUN BEING PULLED ", " GUN WAS PULLED ", " POINTING A GUN ", " BRANDISH ", " FIREARM ", " A FIREARM ",
	} {
		if strings.Contains(paddedUpper, ev) {
			return false
		}
	}
	return true
}

// natureKeywordIsChaseUnitOrStreet rejects CHASE when it is an apparatus name
// (GREEN CHASE 1) or a street (HICKORY CHASE WAY), not a vehicle pursuit.
func natureKeywordIsChaseUnitOrStreet(paddedUpper, keyword string) bool {
	if strings.ToUpper(strings.TrimSpace(keyword)) != "CHASE" {
		return false
	}
	for _, ev := range []string{
		" FOOT PURSUIT ", " VEHICLE PURSUIT ", " IN A PURSUIT ", " IN PURSUIT ",
		" HIGH SPEED PURSUIT ", " PURSUIT THE ", " A PURSUIT ",
	} {
		if strings.Contains(paddedUpper, ev) {
			return false
		}
	}
	return true
}

func natureKeywordIsUnitLunch(paddedUpper, keyword string) bool {
	return strings.ToUpper(strings.TrimSpace(keyword)) == "LUNCH"
}

// natureKeywordIsOfficerCellOrProperty rejects CELL PHONE when it is a unit's
// radio ID, a stolen/left phone, or property — not an incomplete 911.
func natureKeywordIsOfficerCellOrProperty(paddedUpper, keyword string) bool {
	ku := strings.ToUpper(strings.TrimSpace(keyword))
	if !strings.Contains(ku, "CELL") {
		return false
	}
	if strings.Contains(paddedUpper, " 911 ") &&
		(strings.Contains(paddedUpper, " INCOMPLETE ") ||
			strings.Contains(paddedUpper, " ABANDONED ") ||
			strings.Contains(paddedUpper, " DISCONNECTED ") ||
			strings.Contains(paddedUpper, " NOTHING HEARD ") ||
			strings.Contains(paddedUpper, " NO ANSWER ")) {
		return false
	}
	if strings.Contains(paddedUpper, " 911 FROM CELL ") ||
		strings.Contains(paddedUpper, " 911 CELL PHONE ") {
		return false
	}
	return true
}

func natureKeywordIsHangingFalsePositive(paddedUpper, keyword string) bool {
	switch strings.ToUpper(strings.TrimSpace(keyword)) {
	case "HANGING", "HANGED":
	default:
		return false
	}
	if strings.Contains(paddedUpper, " HANGING FIRE ") ||
		strings.Contains(paddedUpper, " HANGING ON THE ") ||
		strings.Contains(paddedUpper, " HANGING OUT ") {
		return true
	}
	return false
}

func liftAssistEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" LIFT ASSIST ", " LIFT ASSISTANCE ", " INVALID ASSIST ",
		" ASSIST UP ", " HELP UP ", " CANNOT GET UP ", " CAN T GET UP ",
		" UNABLE TO GET UP ", " STUCK IN A CHAIR ", " CANNOT STAND ",
		" UNABLE TO WALK ", " CANNOT WALK ", " CAN T WALK ", " NOT ABLE TO WALK ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func natureKeywordIsBareLiftFalsePositive(paddedUpper, keyword string) bool {
	switch strings.ToUpper(strings.TrimSpace(keyword)) {
	case "LIFT", "LIFT ASSIST", "INVALID ASSIST":
	default:
		return false
	}
	return !liftAssistEvidenceInTranscript(paddedUpper)
}

func natureKeywordIsTooShortAbbrev(keyword string) bool {
	return strings.ToUpper(strings.TrimSpace(keyword)) == "INV"
}

func cardiacArrestEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" CARDIAC ARREST ", " FULL ARREST ", " CODE BLUE ", " CPR ",
		" CPR IN PROGRESS ", " NOT BREATHING ", " NON BREATHER ",
		" V-FIB ", " V FIB ", " VFIB ", " ASYSTOLE ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	if strings.Contains(paddedUpper, " CARDIAC ") && !strings.Contains(paddedUpper, " CARDINAL ") {
		return true
	}
	return false
}

func natureKeywordIsCardinalPlaceName(paddedUpper, keyword string) bool {
	switch strings.ToUpper(strings.TrimSpace(keyword)) {
	case "CARDIAC", "CARDIAC ARREST":
	default:
		return false
	}
	return strings.Contains(paddedUpper, " CARDINAL ") && !cardiacArrestEvidenceInTranscript(paddedUpper)
}

func lakeEmergencyEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" IN THE LAKE ", " PERSON IN THE LAKE ", " SUBJECT IN THE LAKE ",
		" BOAT EMERGENCY ", " BOAT IN DISTRESS ", " DROWNING ",
		" PERSON IN THE WATER ", " IN THE WATER ", " JET SKI ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func pumpsIncidentEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" NEED PUMPS ", " REQUESTING PUMPS ", " PUMP FAILURE ",
		" BOOSTER PUMP ", " DRAFTING ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func natureKeywordIsPumpFalsePositive(paddedUpper, keyword string) bool {
	switch strings.ToUpper(strings.TrimSpace(keyword)) {
	case "PUMP", "PUMPS":
	default:
		return false
	}
	return !pumpsIncidentEvidenceInTranscript(paddedUpper)
}

// natureKeywordIsFallsPlaceName rejects FALL when the only hit is a place
// (FALLS AVENUE, FALLS EMS, CUYAHOGA FALLS) and nobody actually fell.
func natureKeywordIsFallsPlaceName(paddedUpper, keyword string) bool {
	switch strings.ToUpper(strings.TrimSpace(keyword)) {
	case "FALL", "FALLS":
	default:
		return false
	}
	for _, ev := range []string{
		" FELL ", " FALL VICTIM ", " FALL PATIENT ", " FALL FROM ",
		" A FALL ", " THE FALL ", " FOR A FALL ", " FOR FALL ", " FALL WITH ",
		" FALL BETWEEN ", " FALLS BETWEEN ", " MULTIPLE FALLS ",
	} {
		if strings.Contains(paddedUpper, ev) {
			return false
		}
	}
	tokens := natureTranscriptTokens(paddedUpper)
	if n := len(tokens); n >= 2 && tokens[n-1] == "FALLS" {
		prev := tokens[n-2]
		if prev != "CUYAHOGA" && prev != "MONROE" && prev != "NEWTON" &&
			prev != "CHAGRIN" && prev != "GRAND" && prev != "MEADOW" &&
			prev != "OLMSTED" && prev != "VALLEY" && prev != "SOUTH" &&
			prev != "NORTH" && prev != "EAST" && prev != "WEST" && prev != "AND" {
			return false
		}
	}
	if fallenPatientEvidenceInTranscript(paddedUpper) {
		return false
	}
	if strings.Contains(paddedUpper, " FALL ") && !strings.Contains(paddedUpper, " FALLS ") {
		return false
	}
	return strings.Contains(paddedUpper, " FALLS ")
}

// fallenPatientEvidenceInTranscript is a person who has fallen — not FALLEN ROAD.
func fallenPatientEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" HAS FALLEN ", " HAD FALLEN ", " HAVE FALLEN ", " FALLEN DOWN ",
		" FALLEN OFF ", " FALLEN FROM ", " FALLEN OUT ", " FALLEN ON ",
		" PATIENT FALLEN ", " PERSON FALLEN ", " FALLEN PATIENT ",
		" FALLEN PERSON ", " FALLEN VICTIM ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

// natureKeywordIsFallenStreetName rejects FALL/FALLEN when the only hit is a
// street (FALLEN ROAD, "...AND FALLEN") and nobody actually fell.
func natureKeywordIsFallenStreetName(paddedUpper, keyword string) bool {
	switch strings.ToUpper(strings.TrimSpace(keyword)) {
	case "FALL", "FALLS", "FALLEN":
	default:
		return false
	}
	if fallenPatientEvidenceInTranscript(paddedUpper) {
		return false
	}
	tokens := natureTranscriptTokens(paddedUpper)
	for i, tok := range tokens {
		if tok != "FALLEN" {
			continue
		}
		if i+1 >= len(tokens) {
			if i > 0 && tokens[i-1] == "AND" {
				return true
			}
			continue
		}
		switch tokens[i+1] {
		case "ROAD", "RD", "STREET", "ST", "AVENUE", "AVE", "DRIVE", "DR",
			"LANE", "LN", "BOULEVARD", "BLVD", "WAY", "COURT", "CT",
			"PLACE", "PL", "CIRCLE", "CIR", "HIGHWAY", "HWY",
			"SOUTHWEST", "SOUTHEAST", "NORTHWEST", "NORTHEAST", "SW", "SE", "NW", "NE":
			return true
		}
	}
	return false
}

// breathingDifficultyEvidenceInTranscript requires a spoken breathing
// complaint. Vital-status "conscious and breathing" and STT lookalikes
// (BREEDING DIFFICULTY) are not evidence.
func breathingDifficultyEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" BREATHING DIFFICULT", " DIFFICULTY BREATHING ", " TROUBLE BREATHING ",
		" HARD TIME BREATHING ", " SHORT OF BREATH ", " SHORT OF THE BREATH ",
		" SHORTNESS OF BREATH ", " OUT OF BREATH ", " CATCH HIS BREATH ",
		" CATCH HER BREATH ", " CATCH THEIR BREATH ", " SOB ", " RESPIRATORY ",
		" DYSPNEA ", " BREATHING PROBLEM ", " BREATHING PROBLEMS ",
		" BREATHING ISSUE ", " BREATHING ISSUES ", " LABORED BREATHING ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func mentalStatusOnlyInTranscript(paddedUpper string) bool {
	if strings.Contains(paddedUpper, " MENTAL HEALTH ") ||
		strings.Contains(paddedUpper, " MENTALLY ILL ") ||
		strings.Contains(paddedUpper, " MENTAL ILLNESS ") ||
		strings.Contains(paddedUpper, " MENTAL EVAL ") ||
		strings.Contains(paddedUpper, " MENTAL EVALUATION ") ||
		strings.Contains(paddedUpper, " MENTAL BREAKDOWN ") ||
		strings.Contains(paddedUpper, " PSYCH ") ||
		strings.Contains(paddedUpper, " PSYCHIATRIC ") ||
		strings.Contains(paddedUpper, " BEHAVIORAL ") {
		return false
	}
	for _, kw := range []string{
		" MENTAL STATUS ", " CHANGE IN MENTAL ", " ALTERED MENTAL ",
		" ALTERED LOC ", " A LOC ", " AMS ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func heartProblemEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" HEART ATTACK ", " HEART PROBLEM ", " HEART PROBLEMS ",
		" HEART RATE ", " CARDIAC EVENT ", " POSSIBLE HEART ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func electricalFireEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" ELECTRICAL FIRE ", " ELECTRIC FIRE ", " SMELL OF AN ELECTRICAL BURN ",
		" ELECTRICAL BURNING ", " SPARKING ", " SPARKING AND ",
		" ARCING ", " WIRES ON FIRE ", " WIRE ON FIRE ", " POLE ON FIRE ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func natureKeywordIsElectricalPlaceFalsePositive(paddedUpper, keyword string) bool {
	switch strings.ToUpper(strings.TrimSpace(keyword)) {
	case "ELECTRICAL", "ELECTRICAL FIRE", "ELECTRIC FIRE":
	default:
		return false
	}
	return !electricalFireEvidenceInTranscript(paddedUpper)
}

func structureFireEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" STRUCTURE FIRE ", " RESTRUCTURE FIRE ", " STRUCTURED FIRE ", " HOUSE FIRE ", " BUILDING FIRE ",
		" APARTMENT FIRE ", " GARAGE FIRE ", " KITCHEN FIRE ", " STOVE FIRE ",
		" OVEN FIRE ", " WORKING FIRE ", " FULLY INVOLVED ", " BOX ALARM ",
		" SECOND ALARM ", " 2ND ALARM ", " FIREBOX ", " FIRE BOX ",
		" FIRE IN THE ", " ON FIRE ", " POSSIBLE STRUCTURE FIRE ", " RESIDENTIAL FIRE ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func natureKeywordIsCarAlarmFalsePositive(paddedUpper, keyword string) bool {
	switch strings.ToUpper(strings.TrimSpace(keyword)) {
	case "ALARM", "FIRE ALARM", "FIRE ALARM DROP", "AUTOMATIC ALARM",
		"ALARM FIRE", "HIGH RISE ALARM":
	default:
		return false
	}
	if fireAlarmEvidenceInTranscript(paddedUpper) || structureFireEvidenceInTranscript(paddedUpper) {
		return false
	}
	return strings.Contains(paddedUpper, " CAR ALARM ")
}

func fracturePatientEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" FRACTURE ", " FRACTURED ", " BROKEN BONE ", " BROKEN LEG ",
		" BROKEN LEGS ", " BROKEN ARM ", " BROKEN ARMS ", " BROKEN HIP ",
		" BROKEN ANKLE ", " BROKEN WRIST ", " BROKEN FOOT ", " BROKEN SHOULDER ",
		" BROKEN NOSE ", " BROKEN JAW ", " BROKEN RIB ", " BROKEN RIBS ",
		" BROKEN COLLARBONE ", " BROKE HIS LEG ", " BROKE HER LEG ",
		" BROKE HIS ARM ", " BROKE HER ARM ", " BROKE HIS HIP ", " BROKE HER HIP ",
		" BROKE HIS ANKLE ", " BROKE HER ANKLE ", " BROKE HIS WRIST ",
		" BROKE HER WRIST ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func natureKeywordIsBrokenPropertyFalsePositive(paddedUpper, keyword string) bool {
	ku := strings.ToUpper(strings.TrimSpace(keyword))
	if !strings.Contains(ku, "BROKEN") && !strings.Contains(ku, "FRACTURE") &&
		!strings.Contains(ku, "BROKE") {
		return false
	}
	if fracturePatientEvidenceInTranscript(paddedUpper) {
		return false
	}
	for _, prop := range []string{
		" BROKEN INTO ", " BROKEN WINDOW ", " WINDOW IS BROKEN ",
		" WINDOW BROKEN ", " NOTHING APPEARS BROKEN ", " BROKEN DOWN ",
		" BROKEN PIPE ", " BROKE HER TV ", " BROKE HIS TV ", " BROKE A ",
	} {
		if strings.Contains(paddedUpper, prop) {
			return true
		}
	}
	return false
}

func natureKeywordIsCrashUnitOrGasLineFalsePositive(paddedUpper, keyword string) bool {
	ku := strings.ToUpper(strings.TrimSpace(keyword))
	if !strings.Contains(ku, "CRASH") && !strings.Contains(ku, "ACCIDENT") &&
		!strings.Contains(ku, "COLLISION") && ku != "MVA" {
		return false
	}
	if strings.Contains(paddedUpper, " CRASH ONE ") ||
		strings.Contains(paddedUpper, " CRASH 1 ") ||
		strings.Contains(paddedUpper, " GAS LINE HIT ") ||
		strings.Contains(paddedUpper, " HIT A GAS LINE ") ||
		strings.Contains(paddedUpper, " ACCIDENTALLY CALLED ") ||
		strings.Contains(paddedUpper, " ACCIDENTALLY DIALED ") ||
		strings.Contains(paddedUpper, " ACCIDENTALLY HIT 911 ") ||
		strings.Contains(paddedUpper, " 911 ACCIDENTAL ") {
		return !strings.Contains(paddedUpper, " ACCIDENT WITH INJURY") &&
			!strings.Contains(paddedUpper, " VEHICLE ACCIDENT ") &&
			!strings.Contains(paddedUpper, " CAR ACCIDENT ") &&
			!strings.Contains(paddedUpper, " TRAFFIC COLLISION ") &&
			!strings.Contains(paddedUpper, " MVA ")
	}
	return false
}

func burnInjuryEvidenceInTranscript(paddedUpper string) bool {
	if strings.Contains(paddedUpper, " OPEN BURN ") ||
		strings.Contains(paddedUpper, " BURN RESCUE ") ||
		strings.Contains(paddedUpper, " BURNING SENSATION ") ||
		strings.Contains(paddedUpper, " BURNERS ") {
		return false
	}
	for _, kw := range []string{
		" DEGREE BURN", " BURN INJURY ", " BURN VICTIM ", " THERMAL BURN ",
		" SCALDED ", " BURNED HIS ", " BURNED HER ", " BURNED THEIR ",
		" BURNED BY ", " HOT COFFEE ", " HOT WATER ", " HOT OIL ", " HOT GREASE ",
		" BURNS ", " BURNED ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func natureKeywordIsBurnPlaceOrOpenBurnFalsePositive(paddedUpper, keyword string) bool {
	switch strings.ToUpper(strings.TrimSpace(keyword)) {
	case "BURN", "BURNS", "BURNED", "BURN INJURY", "THERMAL BURN":
	default:
		return false
	}
	return !burnInjuryEvidenceInTranscript(paddedUpper)
}

func doaEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" DOA ", " D O A ", " DEAD ON ARRIVAL ", " DECEASED ",
		" OBVIOUS DEATH ", " PASSED AWAY ", " TIME OF DEATH ",
		" COLD AND STIFF ", " RIGOR ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func dumpsterFireEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" DUMPSTER FIRE ", " TRASH FIRE ", " DUMPSTER ON FIRE ",
		" DUMPSTER IS ON FIRE ", " FIRE IN THE DUMPSTER ", " FIRE IN A DUMPSTER ",
		" DUMPSTER SMOKING ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func natureKeywordIsDumpsterWithoutFireFalsePositive(paddedUpper, keyword string) bool {
	switch strings.ToUpper(strings.TrimSpace(keyword)) {
	case "DUMPSTER", "DUMPSTER FIRE", "TRASH FIRE":
	default:
		return false
	}
	return !dumpsterFireEvidenceInTranscript(paddedUpper)
}

func atvDirtbikeEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" ATV ", " ATVS ", " DIRT BIKE ", " DIRT BIKES ", " DIRTBIKE ",
		" DIRTBIKES ", " FOUR WHEELER ", " FOUR WHEELERS ", " 4 WHEELER ",
		" QUAD ", " MINI BIKE ", " MINIBIKE ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func natureKeywordIsBareBikeFalsePositive(paddedUpper, keyword string) bool {
	ku := strings.ToUpper(strings.TrimSpace(keyword))
	if !strings.Contains(ku, "ATV") && !strings.Contains(ku, "BIKE") &&
		!strings.Contains(ku, "WHEELER") {
		return false
	}
	return !atvDirtbikeEvidenceInTranscript(paddedUpper)
}

func drugActivityEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" DRUG ACTIVITY ", " DRUG DEAL ", " DRUG USE ", " DRUG OFFENSE ",
		" SELLING DRUGS ", " SMOKING DRUGS ", " UNDER THE INFLUENCE OF DRUGS ",
		" ON DRUGS ", " NARCOTICS ", " MARIJUANA ", " HEROIN ", " FENTANYL ",
		" COCAINE ", " METH ", " METHAMPHETAMINE ", " DRUG PARAPHERNALIA ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	if strings.Contains(paddedUpper, " DRUGS ") &&
		!strings.Contains(paddedUpper, " NO DRINKING DRUGS ") &&
		!strings.Contains(paddedUpper, " OR ON DRUGS ") &&
		!strings.Contains(paddedUpper, " HISTORY OF DRUGS ") {
		return true
	}
	return false
}

func natureKeywordIsDrugMartFalsePositive(paddedUpper, keyword string) bool {
	ku := strings.ToUpper(strings.TrimSpace(keyword))
	if !strings.Contains(ku, "DRUG") && !strings.Contains(ku, "NARCOTIC") {
		return false
	}
	if drugActivityEvidenceInTranscript(paddedUpper) {
		return false
	}
	return strings.Contains(paddedUpper, " DRUG MART ") ||
		strings.Contains(paddedUpper, " DRUG STORE ") ||
		strings.Contains(paddedUpper, " DRUG HER ") ||
		strings.Contains(paddedUpper, " DRUG HIM ") ||
		strings.Contains(paddedUpper, " DRUG THE ") ||
		strings.Contains(paddedUpper, " DRUGGED THE ") ||
		strings.Contains(paddedUpper, " NO DRINKING DRUGS ") ||
		strings.Contains(paddedUpper, " OR ON DRUGS ") ||
		strings.Contains(paddedUpper, " HISTORY OF DRUGS ")
}

func natureKeywordIsNonObstetricLaborFalsePositive(paddedUpper, keyword string) bool {
	if strings.ToUpper(strings.TrimSpace(keyword)) != "LABOR" {
		return false
	}
	for _, ok := range []string{
		" IN LABOR ", " ACTIVE LABOR ", " INACTIVE LABOR ", " GOING INTO LABOR ",
		" PREGNANT ", " PREGNANCY ", " CHILDBIRTH ", " CONTRACTIONS ",
		" WATER BROKE ",
	} {
		if strings.Contains(paddedUpper, ok) {
			return false
		}
	}
	return strings.Contains(paddedUpper, " LABORED BREATHING ") ||
		strings.Contains(paddedUpper, " MUTUAL LABOR ") ||
		strings.Contains(paddedUpper, " LABOR DAY ")
}

func unconsciousFaintedEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" UNCONSCIOUS ", " UNRESPONSIVE ", " NOT RESPONSIVE ", " FAINTED ",
		" FAINTING ", " PASSED OUT ", " PASSING OUT ", " PASS OUT ",
		" SYNCOPAL ", " SYNCOPE ", " NOT BREATHING ", " SHALLOW BREATHING ",
		" BARELY BREATHING ", " BARELY CONSCIOUS ", " IN AND OUT OF CONSCIOUSNESS ",
		" LOST CONSCIOUSNESS ", " LAYING IN ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func standbyIncidentEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" FOR A STANDBY ", " FOR STANDBY ", " STANDBY AT ", " ON STANDBY ",
		" FIRE STANDBY ", " MEDICAL STANDBY ", " LZ STANDBY ", " LANDING ZONE ",
		" REQUEST ON STANDBY ", " REQUESTING STANDBY ", " TO STANDBY ",
		" STANDBY FOR FIRE ", " STANDBY FOR POLICE ", " STANDBY FOR PD ",
		" STANDBY FOR EMS ", " PROPERTY STANDBY ", " CIVIL STANDBY ",
		" STANDBY FOR BELONGINGS ", " ASKING FOR A STAND BY ",
		" ASKED FOR A STAND BY ", " REQUESTING A STAND BY ",
		" REQUESTED TO STAND BY ", " COULD STAND BY ", " STAND BY WHILE ",
		" STAND BY WITH ", " OFFICER S STANDBY ", " OFFICERS STANDBY ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

// natureKeywordIsRadioStandbyFalsePositive rejects radio "stand by" / a person
// standing somewhere when dispatch never requested a standby assignment.
func natureKeywordIsRadioStandbyFalsePositive(paddedUpper, keyword string) bool {
	switch strings.ToUpper(strings.TrimSpace(keyword)) {
	case "STANDBY", "STAND BY", "STANDING BY":
	default:
		return false
	}
	return !standbyIncidentEvidenceInTranscript(paddedUpper)
}

func natureKeywordIsLongShot(paddedUpper, keyword string) bool {
	switch strings.ToUpper(strings.TrimSpace(keyword)) {
	case "SHOT", "SHOTS", "SHOTS FIRED", "SHOOTING":
	default:
		return false
	}
	if strings.Contains(paddedUpper, " LONG SHOT ") ||
		strings.Contains(paddedUpper, " SHOT THROUGH ") ||
		strings.Contains(paddedUpper, " SHOT PAST ") ||
		strings.Contains(paddedUpper, " WATER SHOOTING ") ||
		strings.Contains(paddedUpper, " SHOOTING UP IN THE AIR ") ||
		strings.Contains(paddedUpper, " SHOOTING DOWN ") ||
		strings.Contains(paddedUpper, " TETANUS SHOT ") ||
		strings.Contains(paddedUpper, " FLU SHOT ") {
		return !strings.Contains(paddedUpper, " SHOTS FIRED ") &&
			!strings.Contains(paddedUpper, " SHOT FIRED ") &&
			!strings.Contains(paddedUpper, " GUNSHOT ") &&
			!strings.Contains(paddedUpper, " DRIVE BY SHOOTING ") &&
			!strings.Contains(paddedUpper, " GOT SHOT ")
	}
	return false
}

func natureKeywordIsElectricBatteryFalsePositive(paddedUpper, keyword string) bool {
	if strings.ToUpper(strings.TrimSpace(keyword)) != "BATTERY" {
		return false
	}
	return strings.Contains(paddedUpper, " DETECTOR ") ||
		strings.Contains(paddedUpper, " DETECTORS ") ||
		strings.Contains(paddedUpper, " SMOKE ALARM ") ||
		strings.Contains(paddedUpper, " CAR BATTERY ") ||
		strings.Contains(paddedUpper, " DEAD BATTERY ") ||
		strings.Contains(paddedUpper, " JUMP START ")
}

func natureKeywordIsDogFightFalsePositive(paddedUpper, keyword string) bool {
	switch strings.ToUpper(strings.TrimSpace(keyword)) {
	case "FIGHT", "FIGHTING":
	default:
		return false
	}
	return strings.Contains(paddedUpper, " TWO DOGS ") ||
		strings.Contains(paddedUpper, " DOGS ARE CURRENTLY ") ||
		strings.Contains(paddedUpper, " DOG FIGHT ")
}

func chimneyFireEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" CHIMNEY FIRE ", " FLUE FIRE ", " OUT OF THE CHIMNEY ",
		" FROM A CHIMNEY ", " FROM THE CHIMNEY ", " CHIMNEY IS ON FIRE ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func natureKeywordIsChimneyStreetFalsePositive(paddedUpper, keyword string) bool {
	switch strings.ToUpper(strings.TrimSpace(keyword)) {
	case "CHIMNEY", "CHIMNEY FIRE", "FLUE FIRE":
	default:
		return false
	}
	return !chimneyFireEvidenceInTranscript(paddedUpper)
}

func bombThreatEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" BOMB THREAT ", " BOMB SCARE ", " EXPLOSIVE THREAT ",
		" PLANTED A BOMB ", " IS A BOMB ", " HAS A BOMB ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func homicidalIdeationInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" HOMICIDAL THOUGHTS ", " HOMICIDAL IDEATION", " FEELING HOMICIDAL ",
		" HOMICIDAL AND SUICIDAL ", " HOMICIDAL MALE ", " HOMICIDAL FEMALE ",
		" HOMICIDAL TEXT ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func homicideEvidenceInTranscript(paddedUpper string) bool {
	if strings.Contains(paddedUpper, " HOMICIDE UNIT ") ||
		strings.Contains(paddedUpper, " HOMICIDE DETECTIVE ") ||
		strings.Contains(paddedUpper, " WASN T HOMICIDAL ") ||
		strings.Contains(paddedUpper, " NOT HOMICIDAL ") ||
		strings.Contains(paddedUpper, " FUNERAL ") ||
		homicidalIdeationInTranscript(paddedUpper) {
		return false
	}
	for _, kw := range []string{
		" POSSIBLE HOMICIDE ", " A HOMICIDE ", " THE HOMICIDE ",
		" HOMICIDE SUSPECT ", " DECEASED ", " DEAD BODY ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func natureKeywordIsSexOffenderRegistryFalsePositive(paddedUpper, keyword string) bool {
	ku := strings.ToUpper(strings.TrimSpace(keyword))
	if !strings.Contains(ku, "SEX") && ku != "RAPE" {
		return false
	}
	if strings.Contains(paddedUpper, " SEXUAL ASSAULT ") ||
		strings.Contains(paddedUpper, " SEX OFFENSE ") ||
		strings.Contains(paddedUpper, " SEX CRIME ") ||
		strings.Contains(paddedUpper, " RAPED ") ||
		strings.Contains(paddedUpper, " RAPE ") {
		return strings.Contains(paddedUpper, " ASSAULT KITS ") ||
			strings.Contains(paddedUpper, " ASSAULT KIT ")
	}
	return strings.Contains(paddedUpper, " SEX OFFENDER ")
}

func natureKeywordIsKidnapMetaphorFalsePositive(paddedUpper, keyword string) bool {
	ku := strings.ToUpper(strings.TrimSpace(keyword))
	if !strings.Contains(ku, "KIDNAP") && !strings.Contains(ku, "ABDUCTION") &&
		!strings.Contains(ku, "HOSTAGE") {
		return false
	}
	return strings.Contains(paddedUpper, " KIDNAPPED HIS PHONE ") ||
		strings.Contains(paddedUpper, " KIDNAPPED HER PHONE ") ||
		strings.Contains(paddedUpper, " KIDNAPPED THE KITTEN ") ||
		strings.Contains(paddedUpper, " KIDNAPPED THE CAT ") ||
		strings.Contains(paddedUpper, " KIDNAPPED THE DOG ") ||
		strings.Contains(paddedUpper, " HOSTAGE DRIVE ") ||
		strings.Contains(paddedUpper, " PREVIOUS 48 REGISTRATION KIDNAPPING ")
}

// adjustUnspecifiedAlarmNature sends a generic commercial/residential/general
// alarm to the fire alarm label when a fire company is dispatched or the
// talkgroup tag is fire. Tamper, burglar, intrusion, and the other security
// cues stay burglary. serviceTag may be empty; the gateway has no tag.
func adjustUnspecifiedAlarmNature(nature, transcript, serviceTag string, natureCodes []string) string {
	if !strings.EqualFold(strings.TrimSpace(nature), "BURGLARY") {
		return nature
	}
	u := " " + strings.ToUpper(transcript) + " "
	if !genericSecurityAlarmPhrase(u) || specificSecurityAlarmCue(u) {
		return nature
	}
	if !fireCompanyDispatched(u) && !strings.Contains(strings.ToUpper(serviceTag), "FIRE") {
		return nature
	}
	if label := clampNatureLabel("FIRE ALARM DROP", natureCodes); label != "" {
		return label
	}
	return nature
}

func genericSecurityAlarmPhrase(padded string) bool {
	for _, cue := range []string{
		" COMMERCIAL ALARM ", " RESIDENTIAL ALARM ", " GENERAL ALARM ",
	} {
		if strings.Contains(padded, cue) {
			return true
		}
	}
	return false
}

func specificSecurityAlarmCue(padded string) bool {
	for _, cue := range []string{
		" TAMPER ", " BURGLAR ", " BURG ALARM ", " INTRUSION ", " HOLD UP ", " HOLDUP ",
		" MOTION SENSOR ", " PANIC ALARM ", " ENTRY EXIT ", " SILENT ALARM ", " AUDIBLE ALARM ",
		" ATM ALARM ", " ETM ALARM ",
	} {
		if strings.Contains(padded, cue) {
			return true
		}
	}
	return false
}

func fireCompanyDispatched(padded string) bool {
	for _, cue := range []string{
		" BATTALION ", " ENGINE ", " LADDER ", " QUINT ", " TOWER ",
	} {
		if strings.Contains(padded, cue) {
			return true
		}
	}
	return false
}

// correctFalseMedicalAlarmNature rewrites ALARM DROP MEDICAL when the dispatch
// is a burglar/ATM/tamper-style alarm (or otherwise has no medical/pendant cue).
func correctFalseMedicalAlarmNature(paddedUpper, nature string, natureCodes []string) string {
	if strings.ToUpper(strings.TrimSpace(nature)) != "ALARM DROP MEDICAL" {
		return nature
	}
	if medicalAlarmEvidenceInTranscript(paddedUpper) {
		return nature
	}
	if securityAlarmEvidenceInTranscript(paddedUpper) {
		if label := clampNatureLabel("BURGLARY", natureCodes); label != "" {
			return label
		}
		return ""
	}
	if fireAlarmEvidenceInTranscript(paddedUpper) {
		if label := clampNatureLabel("FIRE ALARM DROP", natureCodes); label != "" {
			return label
		}
	}
	// Model picked medical with no medical/pendant wording — drop it.
	return ""
}

func medicalAlarmEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" MEDICAL ALARM ", " MEDICAL ALERT ", " MEDICAL PANIC ", " MEDICAL DROP ",
		" PENDANT ", " PENNANT ", " LIFELINE ", " PERSONAL ALARM ", " HELP BUTTON ",
		" MEDICAL ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func securityAlarmEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" TAMPER ", " BURGLAR ", " BURG ALARM ", " INTRUSION ", " HOLD UP ", " HOLDUP ",
		" ATM ", " ETM ", " SILENT ALARM ", " AUDIBLE ALARM ", " COMMERCIAL ALARM ",
		" RESIDENTIAL ALARM ", " MOTION SENSOR ", " PANIC ALARM ", " GENERAL ALARM ",
		" ENTRY EXIT ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func fireAlarmEvidenceInTranscript(paddedUpper string) bool {
	for _, kw := range []string{
		" FIRE ALARM ", " SMOKE ALARM ", " SMOKE DETECTOR ", " AUTOMATIC ALARM ",
		" TROUBLE ALARM ", " PULL STATION ", " ALARM FIRE ", " FIRE DETECTOR ",
		" HIGH RISE ALARM ", " DETECTOR BEEPING ", " DETECTOR CHIRPING ",
		" ALARM CHIRPING ",
	} {
		if strings.Contains(paddedUpper, kw) {
			return true
		}
	}
	return false
}

func transcriptIsIncompleteSquadOpener(transcript string) bool {
	u := strings.TrimRight(strings.ToUpper(strings.TrimSpace(transcript)), ".")
	if !strings.Contains(u, "SQUAD CALL") {
		return false
	}
	words := strings.Fields(u)
	if len(words) <= 4 && strings.HasSuffix(u, "SQUAD CALL") {
		return true
	}
	if strings.Contains(u, "SQUAD CALL,") || strings.Contains(u, "SQUAD CALL ") {
		if len(words) <= 6 {
			for _, m := range []string{
				"FALL", "BREATHING", "PAIN", "BLEEDING", "UNCONSCIOUS", "CHEST",
				"OVERDOSE", "SEIZURE", "INJURY", "MEDICAL", "PATIENT", "VOMIT",
				"STROKE", "CARDIAC", "LAYING", "LYING", "DRIVEWAY", "MALE", "FEMALE",
				"YEAR-OLD", "YEAR OLD",
			} {
				if strings.Contains(u, m) {
					return false
				}
			}
			if !screenHasHouseNumberWord(u) {
				return true
			}
		}
	}
	if strings.Contains(u, "SQUAD CALL IS ") {
		for _, m := range []string{
			"FALL", "BREATHING", "PAIN", "BLEEDING", "UNCONSCIOUS", "CHEST",
			"OVERDOSE", "SEIZURE", "INJURY", "MEDICAL", "PATIENT", "VOMIT",
			"STROKE", "CARDIAC", "LAYING", "LYING", "DRIVEWAY",
		} {
			if strings.Contains(u, m) {
				return false
			}
		}
		return true
	}
	return false
}

func transcriptIsBareUnitStatus(transcript string) bool {
	u := strings.TrimRight(strings.ToUpper(strings.TrimSpace(transcript)), ".")
	if containsRealIncidentMarker(" " + u + " ") {
		return false
	}
	words := strings.Fields(strings.ReplaceAll(u, ",", " "))
	if len(words) == 0 || len(words) > 5 {
		return false
	}
	switch words[0] {
	case "MEDIC", "ENGINE", "RESCUE", "SQUAD", "LADDER", "CHIEF", "CAPTAIN", "BATTALION", "CRESCUE":
		return true
	default:
		return false
	}
}

// IsDefaultUnknownNatureLabel reports catch-all unknown labels assigned when no
// specific category matched — OpenAI classify should run before these are set.
func IsDefaultUnknownNatureLabel(nature string) bool {
	n := strings.ToUpper(strings.TrimSpace(nature))
	switch n {
	case "UNKNOWN PROBLEM", "EMS UNKNOWN PROBLEM/UNCLASSIFIED", "UNKNOWN PROBLEM/UNCLASSIFIED":
		return true
	default:
		return false
	}
}

// isBareCatchAllUnknownNature is the dispatcher catch-all ("UNKNOWN PROBLEM")
// without a medical qualifier. Spoken catch-alls must not lock classification —
// a squad tone-out still deserves EMERGENCY MEDICAL ASSISTANCE, and
// "UNKNOWN MEDICAL" still maps to EMS UNKNOWN PROBLEM/UNCLASSIFIED.
func isBareCatchAllUnknownNature(nature string) bool {
	switch strings.ToUpper(strings.TrimSpace(nature)) {
	case "UNKNOWN PROBLEM", "UNKNOWN PROBLEM/UNCLASSIFIED":
		return true
	default:
		return false
	}
}

func harmonizeNatureSynonyms(n, transcript string, natureCodes []string) string {
	u := " " + strings.ToUpper(transcript) + " "
	if len(n) > maxNatureLabelLen && !isExactNatureCode(n, natureCodes) {
		if specific := natureFromForAPhrase(n, natureCodes); specific != "" {
			n = specific
		} else if kw := localNature(u, natureCodes); kw != "" {
			n = kw
		}
	}
	if strings.Contains(u, " THROWING UP") || strings.Contains(u, " THROW UP") {
		if x := NormalizeNatureToKeywords("VOMITING", natureCodes); x != "" {
			return x
		}
		if strings.Contains(strings.ToUpper(n), "THROW") || strings.Contains(strings.ToUpper(n), "STOMACH") {
			return "VOMITING"
		}
	}
	if strings.Contains(u, " ACCIDENT ") || strings.Contains(u, " AUTO ACCIDENT") ||
		strings.Contains(u, " THREE-CAR ACCIDENT") {
		if strings.EqualFold(n, "CRASH") {
			if x := NormalizeNatureToKeywords("ACCIDENT", natureCodes); x != "" {
				return x
			}
			return "ACCIDENT"
		}
	}
	if strings.Contains(u, " SMOKE SHOWING ") {
		if strings.EqualFold(n, "SMOKE") {
			if x := NormalizeNatureToKeywords("SMOKE SHOWING", natureCodes); x != "" {
				return x
			}
			return "SMOKE SHOWING"
		}
	}
	if TranscriptIsOnScenePatientUpdate(transcript) {
		for _, cue := range []string{
			"SHORTNESS OF BREATH", "CHEST PAIN", "DIFFICULTY BREATHING",
			"BREATHING PROBLEMS", "UNCONSCIOUS", "SEIZURE", "STROKE", "OVERDOSE",
		} {
			if strings.Contains(u, " "+cue) {
				if x := NormalizeNatureToKeywords(cue, natureCodes); x != "" {
					return x
				}
				return cue
			}
		}
	}
	return n
}

func transcriptDescribesSuicideRisk(paddedUpper string) bool {
	for _, cue := range []string{
		" ATTEMPTED SUICIDE", " ATTEMPTED TO KILL", " INTENDS TO KILL", " INTENDS TO STRANGLE",
		" STRANGLING HIMSELF", " STRANGLE HIMSELF", " STRANGLE MYSELF", " STRANGLING MYSELF",
		" SUICIDAL", " SUICIDE ATTEMPT", " SUICIDE THREAT", " VIA HANGING", " HANGING HIMSELF",
		" HANG HIMSELF", " HANG HERSELF", " HANG THEMSELF", " TO HANG HIMSELF",
		" TO HANG HERSELF", " ATTEMPTING TO HANG", " ATTEMPTED TO HANG", " ATTEMPTED HANGING",
		" KILL HIMSELF", " KILL MYSELF", " KILL HERSELF", " KILL THEMSELF",
	} {
		if strings.Contains(paddedUpper, cue) {
			return true
		}
	}
	return transcriptContainsNatureKeyword(paddedUpper, "SUICIDE") ||
		transcriptContainsNatureKeyword(paddedUpper, "SUICIDAL")
}

func inferSuicideNature(transcript string, natureCodes []string) string {
	u := " " + strings.ToUpper(transcript) + " "
	if !transcriptDescribesSuicideRisk(u) {
		return ""
	}
	for _, pref := range []string{
		"SUICIDE ATTEMPT", "SUICIDAL", "SUICIDE THREAT", "SUICIDE", "MENTAL HEALTH", "MENTAL",
	} {
		if norm := NormalizeNatureToKeywords(pref, natureCodes); norm != "" {
			return norm
		}
	}
	return "SUICIDAL"
}

func shouldInferNatureFromTranscript(transcript string, isDispatch bool) bool {
	if isDispatch {
		return true
	}
	u := " " + strings.ToUpper(transcript) + " "
	if containsRealIncidentMarker(u) {
		return true
	}
	if TranscriptLikelyHasLocation(transcript, nil) &&
		!TranscriptIsRadioChatter(transcript) {
		return true
	}
	// Relay/callback with a described situation (custody, caller reporting, etc.)
	for _, m := range []string{
		" CALLING IN", " CALLER ", " REPORTING ", " COMPLAINT", " DISPUTE",
		" CUSTODY", " DOMESTIC", " SUBJECT ", " SUSPECT ",
	} {
		if strings.Contains(u, m) {
			return true
		}
	}
	return false
}

// NaturePromptHints builds compact category examples plus a sample of configured
// keywords for the LLM prompt (multi-word phrases prioritized).
func NaturePromptHints(natureCodes []string) string {
	const examples = `Examples by category (use when clearly applicable; otherwise summarize in 2-5 words):
- Fire: STRUCTURE FIRE, VEHICLE FIRE, FIRE ALARM, GAS LEAK, SMOKE INVESTIGATION
- Medical: CARDIAC ARREST, FALL PATIENT, UNCONSCIOUS, LIFT ASSIST, DIFFICULTY BREATHING
- Law: DOMESTIC, CHILD CUSTODY, CUSTODY DISPUTE, MVA, WELFARE CHECK, SHOTS FIRED, DISTURBANCE
- General: MUTUAL AID, WIRES DOWN, TREE DOWN, WATER RESCUE`
	codes := sortedNatureCodes(natureCodes)
	if len(codes) == 0 {
		return examples
	}
	limit := 50
	if len(codes) < limit {
		limit = len(codes)
	}
	sample := strings.Join(codes[:limit], ", ")
	return examples + "\nConfigured agency terms (prefer exact match when the situation fits): " + sample
}
