// Copyright (C) 2025 Thinline Dynamic Solutions

package mapping

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
)

const (
	maxLearnedPhraseLen   = 48
	maxLearnedPhraseWords = 6
)

var learnedPhraseAgeRE = regexp.MustCompile(`(?i)\b\d{1,3}[- ]?YEAR[- ]?OLD\b`)

// CanonicalizeNatureLabel maps a matched phrase to its parent call-nature label.
func CanonicalizeNatureLabel(nature string, phraseToLabel map[string]string) string {
	nature = strings.ToUpper(strings.TrimSpace(nature))
	if nature == "" || len(phraseToLabel) == 0 {
		return nature
	}
	if label, ok := phraseToLabel[nature]; ok && label != "" {
		return label
	}
	return nature
}

// IsAcceptableCallNaturePhrase reports whether a phrase is short enough to use
// as a transcript match hint — not a whole dispatch narrative.
func IsAcceptableCallNaturePhrase(phrase string) bool {
	phrase = strings.ToUpper(strings.TrimSpace(phrase))
	if len(phrase) < 3 || len(phrase) > maxLearnedPhraseLen {
		return false
	}
	if strings.Contains(phrase, " UNKNOWN") || strings.HasPrefix(phrase, "UNKNOWN ") || phrase == "UNKNOWN" {
		return false
	}
	words := strings.Fields(phrase)
	if len(words) == 0 || len(words) > maxLearnedPhraseWords {
		return false
	}
	if learnedPhraseAgeRE.MatchString(phrase) || strings.Contains(phrase, " YEAR OLD ") {
		return false
	}
	if strings.Contains(phrase, ".") {
		return false
	}
	for _, bad := range []string{
		" ON SCENE", " COMPLAINING OF", " C/O ", " HIT HIS ", " HIT HER ",
		" SHE'S ", " HE'S ", " THEY'RE ", " STATION ", " TIME OUT",
		" CUSTOMER", " WIFE IS ", " HUSBAND IS ",
	} {
		if strings.Contains(phrase, bad) {
			return false
		}
	}
	return true
}

// callNatureOpenAISystemPrompt is the single-transcript classifier prompt.
// Tuned on a gold set of gun/shots/threat/chatter calls (v2 + gpt-4o-mini).
const callNatureOpenAISystemPrompt = `You classify radio dispatch transcripts into exactly one incident category.
Reply with JSON only: {"nature":"CATEGORY LABEL"}
Use only a label from the provided list. If nothing fits, reply {"nature":""}.

Judge the WHOLE message, not isolated words.
- SHOTS FIRED / SHOTS BEING HEARD: only when shots were heard, fired, shot-spotter/shot-spot alert, drive-by, rounds detected, or someone was shot. Do NOT use these for threats to shoot later, "going to get a gun and shoot", sports/radio slang "shot", CAD chatter, or explicit negatives ("no one shot", "no calls on shots").
- THREATS: verbal threats of future violence (including threaten to shoot / get a gun and shoot) when no shots have occurred.
- PERSON WITH GUN: subject currently has/with a firearm — not "hospital gun" medical jargon and not a future threat to obtain a gun.
- Follow-ups, tows, and status chatter about an earlier shooting are not a new SHOTS FIRED incident unless new shots are reported.
- ALARM DROP MEDICAL: only medical/pendant/lifeline/personal medical alarms. Never for burglar, intrusion, hold-up, ATM/ETM, tamper, or a bare "an alarm" with no medical wording.
- FIRE ALARM DROP: fire/smoke/automatic fire or fire-system trouble alarms only.
- BURGLARY: burglar/intrusion/hold-up/ATM-ETM/tamper alarms and break-ins.
Never use UNKNOWN PROBLEM or other catch-all unknown labels.`

const callNatureOpenAISystemPromptBatch = `You classify radio dispatch transcripts into incident categories.
Reply with JSON only: {"results":[{"index":0,"nature":"CATEGORY"},...]}
Use one label from the list per transcript index. Use {"nature":""} when nothing fits.

Judge the WHOLE message, not isolated words.
- SHOTS FIRED / SHOTS BEING HEARD: only when shots were heard, fired, shot-spotter/shot-spot alert, drive-by, rounds detected, or someone was shot. Do NOT use for threats to shoot later, sports/radio slang "shot", CAD chatter, or explicit negatives ("no one shot", "no calls on shots").
- THREATS: verbal threats of future violence when no shots have occurred.
- PERSON WITH GUN: subject currently has a firearm — not medical "hospital gun" jargon.
- Follow-ups/tows about an earlier shooting are not a new SHOTS FIRED unless new shots are reported.
- ALARM DROP MEDICAL: only medical/pendant/lifeline/personal medical alarms — not burglar, ATM/ETM, tamper, hold-up, or bare "an alarm".
- FIRE ALARM DROP: fire/smoke/automatic fire or fire-system trouble alarms only.
- BURGLARY: burglar/intrusion/hold-up/ATM-ETM/tamper alarms and break-ins.
Never use UNKNOWN PROBLEM or other catch-all unknown labels.`

func openAIClassifyLabels(labels []string) []string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		l = strings.ToUpper(strings.TrimSpace(l))
		if l == "" || IsDefaultUnknownNatureLabel(l) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// ClassifyCallNatureWithOpenAIBatch classifies dispatches in one OpenAI request.
// The returned slice matches the transcripts order.
func ClassifyCallNatureWithOpenAIBatch(apiKey, model string, transcripts []string, labels []string) []string {
	out := make([]string, len(transcripts))
	labels = openAIClassifyLabels(labels)
	if len(labels) == 0 || len(transcripts) == 0 || strings.TrimSpace(apiKey) == "" {
		return out
	}
	if model == "" {
		model = DefaultOpenAIModel
	}
	var user strings.Builder
	user.WriteString("Categories:\n")
	for _, l := range labels {
		user.WriteString(l)
		user.WriteByte('\n')
	}
	user.WriteString("\nTranscripts:\n")
	for i, tr := range transcripts {
		fmt.Fprintf(&user, "%d. %s\n", i, strings.TrimSpace(tr))
	}
	system := callNatureOpenAISystemPromptBatch
	body := map[string]any{
		"model":           model,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user.String()},
		},
		"temperature": 0,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return out
	}
	oaiResp, err := PostOpenAIWithRetry(OpenAIChatCompletionsURL, apiKey, bodyBytes)
	if err != nil {
		log.Printf("[WARN] call nature openai batch classify: %v", err)
		return out
	}
	if len(oaiResp.Choices) == 0 {
		return out
	}
	var parsed struct {
		Results []struct {
			Index  int    `json:"index"`
			Nature string `json:"nature"`
		} `json:"results"`
	}
	content := strings.TrimSpace(oaiResp.Choices[0].Message.Content)
	if json.Unmarshal([]byte(content), &parsed) != nil {
		return out
	}
	labelSet := map[string]bool{}
	for _, l := range labels {
		labelSet[strings.ToUpper(strings.TrimSpace(l))] = true
	}
	for _, r := range parsed.Results {
		if r.Index < 0 || r.Index >= len(out) {
			continue
		}
		pick := strings.ToUpper(strings.TrimSpace(r.Nature))
		if pick == "" || !labelSet[pick] || IsDefaultUnknownNatureLabel(pick) {
			continue
		}
		out[r.Index] = pick
	}
	return out
}

// ClassifyCallNatureWithOpenAI asks OpenAI to pick the best matching category
// from the configured call-nature labels.
func ClassifyCallNatureWithOpenAI(apiKey, model, transcript string, labels []string) string {
	pick, _ := ClassifyCallNatureWithOpenAIResult(apiKey, model, transcript, labels)
	return pick
}

// ClassifyCallNatureWithOpenAIResult is like ClassifyCallNatureWithOpenAI but
// reports whether the model answered (including an intentional empty nature).
// answered=false means transport/parse failure — callers should keep prior nature.
func ClassifyCallNatureWithOpenAIResult(apiKey, model, transcript string, labels []string) (pick string, answered bool) {
	apiKey = strings.TrimSpace(apiKey)
	transcript = strings.TrimSpace(transcript)
	if apiKey == "" || transcript == "" || len(labels) == 0 {
		return "", false
	}
	labels = openAIClassifyLabels(labels)
	if len(labels) == 0 {
		return "", false
	}
	if model == "" {
		model = DefaultOpenAIModel
	}
	system := callNatureOpenAISystemPrompt
	user := fmt.Sprintf("Categories:\n%s\n\nTranscript:\n%s", strings.Join(labels, "\n"), transcript)
	body := map[string]any{
		"model": model,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", false
	}
	oaiResp, err := PostOpenAIWithRetry(OpenAIChatCompletionsURL, apiKey, bodyBytes)
	if err != nil {
		log.Printf("[WARN] call nature openai classify: %v", err)
		return "", false
	}
	if len(oaiResp.Choices) == 0 {
		return "", false
	}
	var parsed struct {
		Nature string `json:"nature"`
	}
	content := strings.TrimSpace(oaiResp.Choices[0].Message.Content)
	if json.Unmarshal([]byte(content), &parsed) != nil {
		return "", false
	}
	pick = strings.ToUpper(strings.TrimSpace(parsed.Nature))
	if pick == "" {
		return "", true
	}
	labelSet := map[string]bool{}
	for _, l := range labels {
		labelSet[strings.ToUpper(strings.TrimSpace(l))] = true
	}
	if !labelSet[pick] || IsDefaultUnknownNatureLabel(pick) {
		// Model answered but pick is unusable — treat as intentional empty.
		return "", true
	}
	return pick, true
}

// ClassifyCallNatureForLearning is phrase/rule matching without assigning the
// UNKNOWN fallback — used when mining phrases so unmatched traffic is not
// treated as a classified unknown incident.
func ClassifyCallNatureForLearning(transcript string, labels, matchTerms []string, phraseToLabel map[string]string) string {
	cleaned := PreCleanTranscript(transcript)
	return FinalizeIncidentNatureWithMap(
		"", cleaned, labels, effectiveMatchTerms(labels, matchTerms),
		shouldInferNatureFromTranscript(cleaned, false), phraseToLabel, true,
	)
}

// ClassifyCallNature assigns a call-nature label from transcript phrase matching
// and rule-based inference. OpenAI is never used.
func ClassifyCallNature(transcript string, labels, matchTerms []string, phraseToLabel map[string]string) string {
	cleaned := PreCleanTranscript(transcript)
	nature := FinalizeIncidentNatureWithMap(
		"", cleaned, labels, effectiveMatchTerms(labels, matchTerms),
		shouldInferNatureFromTranscript(cleaned, false), phraseToLabel, false,
	)
	return SanitizeCallNatureAssignment(cleaned, nature, labels)
}

// extractLearnedPhraseForLabel returns the shortest identifying phrase from a
// dispatch segment — never the full narrative tail.
func extractLearnedPhraseForLabel(segment, label string, labels, matchTerms []string, phraseToLabel map[string]string) string {
	segment = stripDispatchDemographicLead(strings.TrimSpace(segment))
	if segment == "" {
		return ""
	}
	padded := " " + strings.ToUpper(segment) + " "
	best := ""
	bestLen := 0
	for _, term := range effectiveMatchTerms(labels, matchTerms) {
		t := strings.ToUpper(strings.TrimSpace(term))
		if t == "" || (phraseToLabel[t] != label && t != label) {
			continue
		}
		if !transcriptContainsNatureKeyword(padded, t) || !IsAcceptableCallNaturePhrase(t) {
			continue
		}
		if len(t) > bestLen {
			bestLen = len(t)
			best = t
		}
	}
	for _, mk := range RealIncidentMarkers {
		mk = strings.ToUpper(strings.TrimSpace(mk))
		if mk == "" || !strings.Contains(padded, mk) {
			continue
		}
		if NormalizeNatureToKeywords(mk, labels) != label {
			continue
		}
		if !IsAcceptableCallNaturePhrase(mk) {
			continue
		}
		if len(mk) > bestLen {
			bestLen = len(mk)
			best = mk
		}
	}
	return best
}

// DiscoverLearnedPhrasesForLabel returns transcript phrases that matched or
// support the given canonical label (for backfill phrase learning).
func DiscoverLearnedPhrasesForLabel(transcript, label string, labels, matchTerms []string, phraseToLabel map[string]string) []string {
	label = strings.ToUpper(strings.TrimSpace(label))
	if label == "" || IsDefaultUnknownNatureLabel(label) {
		return nil
	}
	cleaned := PreCleanTranscript(transcript)
	padded := " " + strings.ToUpper(cleaned) + " "
	seen := map[string]bool{label: true}
	var out []string
	add := func(p string) {
		p = strings.ToUpper(strings.TrimSpace(p))
		if p == "" || seen[p] || !IsAcceptableCallNaturePhrase(p) {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	terms := effectiveMatchTerms(labels, matchTerms)
	for _, term := range terms {
		t := strings.ToUpper(strings.TrimSpace(term))
		if phraseToLabel[t] != label && t != label {
			continue
		}
		if transcriptContainsNatureKeyword(padded, t) {
			add(t)
		}
	}
	for _, marker := range RealIncidentMarkers {
		mk := strings.ToUpper(strings.TrimSpace(marker))
		if mk == "" || !strings.Contains(padded, mk) {
			continue
		}
		if norm := NormalizeNatureToKeywords(strings.TrimSpace(mk), labels); norm == label {
			add(strings.TrimSpace(mk))
		}
	}
	for _, cue := range []string{
		"FOR A ", "FOR AN ", "COMPLAINT", "INVESTIGATION", "ASSIST", "UNKNOWN PROBLEM",
	} {
		idx := strings.Index(padded, cue)
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(padded[idx+len(cue):])
		for _, sep := range []string{" TIMEOUT", " TIME OUT", " DISPATCH CLEAR", " YOUR TIME", ","} {
			if i := strings.Index(rest, sep); i > 0 {
				rest = strings.TrimSpace(rest[:i])
			}
		}
		rest = strings.TrimRight(rest, ".,;")
		if rest == "" {
			continue
		}
		for _, seg := range strings.Split(rest, ",") {
			if phrase := extractLearnedPhraseForLabel(seg, label, labels, matchTerms, phraseToLabel); phrase != "" {
				add(phrase)
			}
		}
	}
	return out
}
