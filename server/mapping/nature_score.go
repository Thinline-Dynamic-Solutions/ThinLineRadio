// Copyright (C) 2025 Thinline Dynamic Solutions
//
// nature_score.go — evidence-weighted call-nature classification. Every
// configured nature (label + phrases) is scored by how many distinct
// transcript words its configured terms explain; the label with the most
// word evidence wins. No label names are special-cased here: words mean
// classification, and more matched words mean stronger evidence.

package mapping

import (
	"sort"
	"strings"
)

// natureEvidence is one label's accumulated transcript evidence.
type natureEvidence struct {
	Label string
	// Score counts distinct transcript tokens covered by the label's matched
	// terms, plus one specificity bonus when a multi-word phrase matched.
	Score int
	// Pos is the earliest covered token index (earlier mention breaks ties).
	Pos int
	// MultiWord marks that at least one multi-word configured phrase matched.
	MultiWord bool
}

// natureTranscriptTokens splits text for term matching. Hyphens and slashes
// are token separators so "LOW-HANGING WIRES" exposes both words.
func natureTranscriptTokens(text string) []string {
	u := strings.NewReplacer("-", " ", "/", " ").Replace(strings.ToUpper(text))
	fields := strings.Fields(u)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, ".,;:!?'\"()")
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// natureTokenWordMatch reports whether a transcript token satisfies one word
// of a configured term: exact, simple plural, or shared morphological stem
// (POWER LINE ↔ POWER LINES, DEHYDRATED ↔ DEHYDRATION).
func natureTokenWordMatch(tok, word string) bool {
	if tok == word {
		return true
	}
	// Plural / +S / +ES only for 3+ letter tokens. "BE"+"ES"="BEES" was
	// classifying every "going to be" as ALLERGIC REACTION.
	if len(tok) >= 3 && len(word) >= 3 &&
		(tok == word+"S" || word == tok+"S" || tok == word+"ES" || word == tok+"ES") {
		return true
	}
	return natureWordStemMatch(tok, word)
}

// phrasalVerbParticles are particles that change a gerund's sense entirely
// ("HANGING OUT", "BREAKING UP") — a single-word "-ING" term followed by one
// of these is conversational, not an incident keyword.
var phrasalVerbParticles = map[string]bool{
	"OUT": true, "AROUND": true, "BACK": true, "UP": true,
}

// natureTermTokenMatch returns the transcript token positions covered by a
// configured term, or nil when the term does not appear. Every term word must
// match its aligned transcript token.
func natureTermTokenMatch(tokens []string, term string) []int {
	words := natureTranscriptTokens(term)
	if len(words) == 0 || len(words) > len(tokens) {
		return nil
	}
	for start := 0; start+len(words) <= len(tokens); start++ {
		ok := true
		for j, w := range words {
			if !natureTokenWordMatch(tokens[start+j], w) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if len(words) == 1 && strings.HasSuffix(words[0], "ING") &&
			start+1 < len(tokens) && phrasalVerbParticles[tokens[start+1]] {
			continue
		}
		out := make([]int, len(words))
		for j := range words {
			out[j] = start + j
		}
		return out
	}
	return nil
}

// scoreConfiguredNatures scores every configured nature against a transcript.
// Results are sorted best-first (score, then earliest mention).
func scoreConfiguredNatures(transcript string, natureCodes, matchTerms []string, phraseToLabel map[string]string) []natureEvidence {
	tokens := natureTranscriptTokens(transcript)
	if len(tokens) == 0 {
		return nil
	}
	padded := " " + strings.ToUpper(transcript) + " "
	type acc struct {
		covered   map[int]bool
		pos       int
		multiWord bool
	}
	byLabel := map[string]*acc{}
	seenTerm := map[string]bool{}
	for _, term := range effectiveMatchTerms(natureCodes, matchTerms) {
		t := strings.ToUpper(strings.TrimSpace(term))
		if t == "" || seenTerm[t] {
			continue
		}
		seenTerm[t] = true
		label := t
		if l, ok := phraseToLabel[t]; ok && strings.TrimSpace(l) != "" {
			label = strings.ToUpper(strings.TrimSpace(l))
		}
		if isApparatusOnlyNatureLabel(label) || IsGenericNature(label) ||
			isDispatchHarnessMarker(label) || isBareCatchAllUnknownNature(label) {
			continue
		}
		if natureKeywordBlockedInMedicalDispatch(padded, t) || natureKeywordIsNegated(padded, t) ||
			natureKeywordIsBreakRoomFalsePositive(padded, t) ||
			natureKeywordIsRadioBreakFalsePositive(padded, t) ||
			natureKeywordIsDepartmentRescueFalsePositive(padded, t) ||
			natureKeywordIsLakeDepartmentFalsePositive(padded, t) ||
			natureKeywordIsAreaCheckFalsePositive(padded, t) ||
			natureKeywordIsBareGunFalsePositive(padded, t) ||
			natureKeywordIsKnifeCutNotThreat(padded, t) ||
			natureKeywordIsChaseUnitOrStreet(padded, t) ||
			natureKeywordIsUnitLunch(padded, t) ||
			natureKeywordIsOfficerCellOrProperty(padded, t) ||
			natureKeywordIsHangingFalsePositive(padded, t) ||
			natureKeywordIsBareLiftFalsePositive(padded, t) ||
			natureKeywordIsTooShortAbbrev(t) ||
			natureKeywordIsFallsPlaceName(padded, t) ||
			natureKeywordIsFallenStreetName(padded, t) ||
			natureKeywordIsRadioStandbyFalsePositive(padded, t) ||
			natureKeywordIsLongShot(padded, t) ||
			natureKeywordIsCardinalPlaceName(padded, t) ||
			natureKeywordIsPumpFalsePositive(padded, t) ||
			natureKeywordIsCarAlarmFalsePositive(padded, t) ||
			natureKeywordIsElectricalPlaceFalsePositive(padded, t) ||
			natureKeywordIsBrokenPropertyFalsePositive(padded, t) ||
			natureKeywordIsCrashUnitOrGasLineFalsePositive(padded, t) ||
			natureKeywordIsBurnPlaceOrOpenBurnFalsePositive(padded, t) ||
			natureKeywordIsDumpsterWithoutFireFalsePositive(padded, t) ||
			natureKeywordIsBareBikeFalsePositive(padded, t) ||
			natureKeywordIsDrugMartFalsePositive(padded, t) ||
			natureKeywordIsNonObstetricLaborFalsePositive(padded, t) ||
			natureKeywordIsElectricBatteryFalsePositive(padded, t) ||
			natureKeywordIsDogFightFalsePositive(padded, t) ||
			natureKeywordIsChimneyStreetFalsePositive(padded, t) ||
			natureKeywordIsSexOffenderRegistryFalsePositive(padded, t) ||
			natureKeywordIsKidnapMetaphorFalsePositive(padded, t) ||
			natureLabelIsStreetSuffixFalsePositive(t, padded) {
			continue
		}
		positions := natureTermTokenMatch(tokens, t)
		if len(positions) == 0 {
			continue
		}
		a := byLabel[label]
		if a == nil {
			a = &acc{covered: map[int]bool{}, pos: positions[0]}
			byLabel[label] = a
		}
		for _, p := range positions {
			a.covered[p] = true
		}
		if positions[0] < a.pos {
			a.pos = positions[0]
		}
		if len(positions) > 1 {
			a.multiWord = true
		}
	}
	out := make([]natureEvidence, 0, len(byLabel))
	for label, a := range byLabel {
		score := len(a.covered)
		if a.multiWord {
			score++
		}
		out = append(out, natureEvidence{Label: label, Score: score, Pos: a.pos, MultiWord: a.multiWord})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Pos != out[j].Pos {
			return out[i].Pos < out[j].Pos
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// scoredNatureOverride returns a configured nature whose transcript word
// evidence strictly outweighs the working label's own evidence ("POWER LINES
// HANGING VERY LOW" explains two words for the utility category versus one
// for a lone gerund match). Empty when the working label should stand.
//
// Response-mode labels (MUTUAL AID, ASSIST FD/PD) describe how units are
// toned, not the patient/incident complaint. When both a response-mode label
// and a complaint nature have transcript evidence, the complaint wins even if
// its raw word score is lower ("MUTUAL AID REQUEST … FALLEN" → FALL).
func scoredNatureOverride(current, transcript string, natureCodes, matchTerms []string, phraseToLabel map[string]string) string {
	cur := strings.ToUpper(strings.TrimSpace(current))
	if cur == "" {
		return ""
	}
	scores := scoreConfiguredNatures(transcript, natureCodes, matchTerms, phraseToLabel)
	if len(scores) == 0 {
		return ""
	}
	curLabel := CanonicalizeNatureLabel(cur, phraseToLabel)
	best := preferComplaintOverResponseMode(scores)
	if best.Label == "" {
		return ""
	}
	if best.Label == cur || best.Label == curLabel {
		return ""
	}
	curScore := 0
	for _, s := range scores {
		if s.Label == curLabel || s.Label == cur {
			curScore = s.Score
			break
		}
	}
	if isResponseModeNature(curLabel) && !isResponseModeNature(best.Label) {
		return best.Label
	}
	if best.Score > curScore {
		return best.Label
	}
	return ""
}

// isResponseModeNature reports agency-to-agency / assist framing that is not
// the underlying complaint (mutual aid for a fall is still a FALL).
func isResponseModeNature(label string) bool {
	switch strings.ToUpper(strings.TrimSpace(label)) {
	case "MUTUAL AID", "ASSIST FIRE DEPARTMENT", "ASSIST POLICE DEPARTMENT", "ASSIST OFFICER":
		return true
	default:
		return false
	}
}

// preferComplaintOverResponseMode returns the best-scoring nature, skipping
// response-mode labels when any complaint nature also scored.
func preferComplaintOverResponseMode(scores []natureEvidence) natureEvidence {
	if len(scores) == 0 {
		return natureEvidence{}
	}
	var bestComplaint natureEvidence
	for _, s := range scores {
		if isResponseModeNature(s.Label) {
			continue
		}
		bestComplaint = s
		break
	}
	if bestComplaint.Label != "" {
		return bestComplaint
	}
	return scores[0]
}

// natureLoneGerundEvidence reports when a single-word "-ING" label's only
// transcript evidence is one bare verb token in narration ("HE'S BEEN HANGING
// THERE", "GUY'S JUST CHILLING"): no toned-dispatch marker, no "FOR A <label>"
// dispatch cue, and no multi-word phrase evidence. A gerund used as a verb in
// conversation is not a dispatched incident type.
func natureLoneGerundEvidence(label, transcript string, natureCodes, matchTerms []string, phraseToLabel map[string]string) bool {
	n := strings.ToUpper(strings.TrimSpace(label))
	if n == "" || strings.Contains(n, " ") || !strings.HasSuffix(n, "ING") {
		return false
	}
	// Medical / complaint -ING labels are symptom/offense names, not conversational gerunds.
	switch n {
	case "BLEEDING", "CHOKING", "NAUSEA/VOMITING", "VOMITING", "DROWNING",
		"POISONING", "SHOPLIFTING", "STALKING", "SOLICITING", "BREAKING AND ENTERING":
		return false
	}
	u := " " + strings.ToUpper(transcript) + " "
	if containsRealIncidentMarker(u) {
		return false
	}
	for _, cue := range []string{
		" FOR A " + n, " FOR AN " + n, " FOR " + n,
		" REPORT OF " + n, " POSSIBLE " + n, " ATTEMPTED " + n,
	} {
		if strings.Contains(u, cue) {
			return false
		}
	}
	for _, s := range scoreConfiguredNatures(transcript, natureCodes, matchTerms, phraseToLabel) {
		if s.Label == n {
			return s.Score <= 1 && !s.MultiWord
		}
	}
	return true
}

// natureScoredEvidenceConfirms reports whether the working label itself has
// configured-phrase evidence in the transcript. A label reached through its
// own catalog phrases ("DIZZY" → DIZZINESS) is anchored by construction even
// when the label word never appears verbatim.
func natureScoredEvidenceConfirms(current, transcript string, natureCodes, matchTerms []string, phraseToLabel map[string]string) bool {
	cur := CanonicalizeNatureLabel(strings.ToUpper(strings.TrimSpace(current)), phraseToLabel)
	if cur == "" {
		return false
	}
	for _, s := range scoreConfiguredNatures(transcript, natureCodes, matchTerms, phraseToLabel) {
		if s.Label == cur {
			return s.Score >= 1
		}
	}
	return false
}

// bestScoredNature returns the highest-evidence configured nature, requiring
// at least minScore covered words.
func bestScoredNature(transcript string, natureCodes, matchTerms []string, phraseToLabel map[string]string, minScore int) string {
	scores := scoreConfiguredNatures(transcript, natureCodes, matchTerms, phraseToLabel)
	best := preferComplaintOverResponseMode(scores)
	if best.Label == "" || best.Score < minScore {
		return ""
	}
	return best.Label
}
