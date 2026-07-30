// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rdio-scanner/server/mapping"
)

const (
	defaultCallNaturePhraseLearnMinSightings = 3
	callNaturePhraseLearnScanMaxCalls        = 500
	callNaturePhraseLearnScanDefaultHours    = 168
	callNaturePhraseLearnScanMaxHours        = 720
)

type callNaturePhraseCallRecord struct {
	CallId     uint64 `json:"callId"`
	Transcript string `json:"transcript,omitempty"`
	Timestamp  int64  `json:"timestamp"`
}

// CallNaturePhraseSuggestion is a ready-to-review learned phrase for one category.
type CallNaturePhraseSuggestion struct {
	CandidateId uint64   `json:"candidateId"`
	Label       string   `json:"label"`
	Phrase      string   `json:"phrase"`
	Sightings   int      `json:"sightings"`
	SampleCallIds []uint64 `json:"sampleCallIds,omitempty"`
	FirstSeenAt int64    `json:"firstSeenAt"`
	LastSeenAt  int64    `json:"lastSeenAt"`
	Ready       bool     `json:"ready"`
}

type CallNaturePhraseLearnStatus struct {
	Enabled      bool `json:"enabled"`
	MinSightings int  `json:"minSightings"`
	PendingReady int  `json:"pendingReady"`
	PendingAll   int  `json:"pendingAll"`
}

type CallNaturePhraseScanRequest struct {
	Hours int `json:"hours"`
	Limit int `json:"limit"`
}

type CallNaturePhraseScanResponse struct {
	CallsScanned   int                          `json:"callsScanned"`
	CallsWithNature int                         `json:"callsWithNature"`
	CandidatesTouched int                       `json:"candidatesTouched"`
	ReadyCount     int                          `json:"readyCount"`
	LookbackHours  int                          `json:"lookbackHours"`
	MinSightings   int                          `json:"minSightings"`
	Suggestions    []CallNaturePhraseSuggestion `json:"suggestions"`
	Message        string                       `json:"message,omitempty"`
}

func migrateCallNaturePhraseLearn(db *Database) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS "callNaturePhraseCandidates" (
			"candidateId" bigserial NOT NULL PRIMARY KEY,
			"label" text NOT NULL,
			"phrase" text NOT NULL,
			"phraseHash" text NOT NULL,
			"callRecords" text NOT NULL DEFAULT '[]',
			"firstSeenAt" bigint NOT NULL DEFAULT 0,
			"lastSeenAt" bigint NOT NULL DEFAULT 0,
			"dismissedAt" bigint NOT NULL DEFAULT 0,
			"acceptedAt" bigint NOT NULL DEFAULT 0
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "callNaturePhraseCandidates_label_phrase_uidx"
			ON "callNaturePhraseCandidates" ("label", "phraseHash")`,
		`CREATE INDEX IF NOT EXISTS "callNaturePhraseCandidates_ready_idx"
			ON "callNaturePhraseCandidates" ("dismissedAt", "acceptedAt", "lastSeenAt" DESC)`,
	}
	for _, q := range queries {
		if _, err := db.Sql.Exec(q); err != nil {
			return fmt.Errorf("migrateCallNaturePhraseLearn: %w", err)
		}
	}
	return nil
}

func callNaturePhraseHash(phrase string) string {
	sum := sha1.Sum([]byte(strings.ToUpper(strings.TrimSpace(phrase))))
	return hex.EncodeToString(sum[:])
}

func callNaturePhraseLearnMinSightings(mi MappingIntegration) int {
	n := int(mi.CallNaturePhraseLearnMinSightings)
	if n <= 0 {
		return defaultCallNaturePhraseLearnMinSightings
	}
	if n > 50 {
		return 50
	}
	return n
}

func (controller *Controller) processCallNaturePhraseLearn(callId uint64, transcript, natureLabel, cleaned string, natureData CallNatureMatchData) {
	if controller == nil || controller.Database == nil || controller.Database.Sql == nil {
		return
	}
	controller.Options.mutex.Lock()
	mi := controller.Options.MappingIntegration
	controller.Options.mutex.Unlock()
	if !mi.CallNaturePhraseLearn {
		return
	}
	label := strings.ToUpper(strings.TrimSpace(natureLabel))
	if label == "" || mapping.IsDefaultUnknownNatureLabel(label) {
		return
	}
	text := strings.TrimSpace(cleaned)
	if text == "" {
		text = strings.TrimSpace(transcript)
	}
	if text == "" {
		return
	}

	known := map[string]bool{}
	for term, mapped := range natureData.PhraseToLabel {
		known[strings.ToUpper(strings.TrimSpace(term))] = true
		_ = mapped
	}
	for _, t := range natureData.MatchTerms {
		known[strings.ToUpper(strings.TrimSpace(t))] = true
	}
	known[label] = true

	phrases := mapping.SuggestNewPhrasesForNature(text, label, known)
	if len(phrases) == 0 {
		return
	}
	now := time.Now().UnixMilli()
	for _, phrase := range phrases {
		if err := controller.upsertCallNaturePhraseCandidate(label, phrase, callId, text, now); err != nil {
			log.Printf("[WARN] call nature phrase learn upsert: %v", err)
		}
	}
}

func (controller *Controller) upsertCallNaturePhraseCandidate(label, phrase string, callId uint64, transcript string, now int64) error {
	db := controller.Database.Sql
	label = strings.ToUpper(strings.TrimSpace(label))
	phrase = strings.ToUpper(strings.TrimSpace(phrase))
	if label == "" || phrase == "" {
		return nil
	}
	hash := callNaturePhraseHash(phrase)

	var (
		id           int64
		recordsJSON  string
		dismissedAt  int64
		acceptedAt   int64
	)
	err := db.QueryRow(
		`SELECT "candidateId", "callRecords", "dismissedAt", "acceptedAt"
		 FROM "callNaturePhraseCandidates" WHERE "label" = $1 AND "phraseHash" = $2`,
		label, hash,
	).Scan(&id, &recordsJSON, &dismissedAt, &acceptedAt)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if acceptedAt > 0 || dismissedAt > 0 {
		return nil
	}

	var records []callNaturePhraseCallRecord
	if recordsJSON != "" && recordsJSON != "[]" {
		_ = json.Unmarshal([]byte(recordsJSON), &records)
	}
	snippet := transcript
	if len(snippet) > 240 {
		snippet = snippet[:240]
	}
	updated := false
	for i := range records {
		if records[i].CallId == callId {
			records[i].Transcript = snippet
			records[i].Timestamp = now
			updated = true
			break
		}
	}
	if !updated {
		records = append(records, callNaturePhraseCallRecord{
			CallId: callId, Transcript: snippet, Timestamp: now,
		})
	}
	if len(records) > 25 {
		records = records[len(records)-25:]
	}
	raw, _ := json.Marshal(records)

	if err == sql.ErrNoRows {
		_, err = db.Exec(
			`INSERT INTO "callNaturePhraseCandidates"
			 ("label", "phrase", "phraseHash", "callRecords", "firstSeenAt", "lastSeenAt")
			 VALUES ($1, $2, $3, $4, $5, $5)`,
			label, phrase, hash, string(raw), now,
		)
		return err
	}
	_, err = db.Exec(
		`UPDATE "callNaturePhraseCandidates"
		 SET "phrase" = $1, "callRecords" = $2, "lastSeenAt" = $3
		 WHERE "candidateId" = $4`,
		phrase, string(raw), now, id,
	)
	return err
}

func (controller *Controller) listCallNaturePhraseSuggestions(readyOnly bool) ([]CallNaturePhraseSuggestion, error) {
	if controller == nil || controller.Database == nil || controller.Database.Sql == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	controller.Options.mutex.Lock()
	minSight := callNaturePhraseLearnMinSightings(controller.Options.MappingIntegration)
	controller.Options.mutex.Unlock()

	rows, err := controller.Database.Sql.Query(
		`SELECT "candidateId", "label", "phrase", "callRecords", "firstSeenAt", "lastSeenAt"
		 FROM "callNaturePhraseCandidates"
		 WHERE "dismissedAt" = 0 AND "acceptedAt" = 0
		 ORDER BY "lastSeenAt" DESC
		 LIMIT 200`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CallNaturePhraseSuggestion
	for rows.Next() {
		var (
			s           CallNaturePhraseSuggestion
			recordsJSON string
		)
		if err := rows.Scan(&s.CandidateId, &s.Label, &s.Phrase, &recordsJSON, &s.FirstSeenAt, &s.LastSeenAt); err != nil {
			continue
		}
		var records []callNaturePhraseCallRecord
		_ = json.Unmarshal([]byte(recordsJSON), &records)
		s.Sightings = len(records)
		for i, r := range records {
			if i >= 5 {
				break
			}
			if r.CallId > 0 {
				s.SampleCallIds = append(s.SampleCallIds, r.CallId)
			}
		}
		s.Ready = s.Sightings >= minSight
		if readyOnly && !s.Ready {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

func (api *Api) CallNaturePhraseSuggestionsHandler(w http.ResponseWriter, r *http.Request) {
	client := api.getClient(r)
	if client == nil || !api.isAdmin(client) {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	readyOnly := r.URL.Query().Get("ready") == "1"
	list, err := api.Controller.listCallNaturePhraseSuggestions(readyOnly)
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, err.Error())
		return
	}
	api.Controller.Options.mutex.Lock()
	mi := api.Controller.Options.MappingIntegration
	api.Controller.Options.mutex.Unlock()
	ready := 0
	for _, s := range list {
		if s.Ready {
			ready++
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": CallNaturePhraseLearnStatus{
			Enabled:      mi.CallNaturePhraseLearn,
			MinSightings: callNaturePhraseLearnMinSightings(mi),
			PendingReady: ready,
			PendingAll:   len(list),
		},
		"suggestions": list,
	})
}

// CallNaturePhraseSuggestionActionHandler handles
// POST /api/call-nature-phrase-suggestions/{id}/accept|dismiss
func (api *Api) CallNaturePhraseSuggestionActionHandler(w http.ResponseWriter, r *http.Request) {
	client := api.getClient(r)
	if client == nil || !api.isAdmin(client) {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// call-nature-phrase-suggestions / {id} / accept|dismiss
	if len(parts) < 3 {
		api.exitWithError(w, http.StatusBadRequest, "invalid path")
		return
	}
	action := parts[len(parts)-1]
	id, err := strconv.ParseUint(parts[len(parts)-2], 10, 64)
	if err != nil || id == 0 {
		api.exitWithError(w, http.StatusBadRequest, "invalid id")
		return
	}
	switch action {
	case "accept":
		if err := api.Controller.acceptCallNaturePhraseCandidate(id); err != nil {
			api.exitWithError(w, http.StatusBadRequest, err.Error())
			return
		}
	case "dismiss":
		_, err = api.Controller.Database.Sql.Exec(
			`UPDATE "callNaturePhraseCandidates" SET "dismissedAt" = $1
			 WHERE "candidateId" = $2 AND "acceptedAt" = 0 AND "dismissedAt" = 0`,
			time.Now().UnixMilli(), id,
		)
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, err.Error())
			return
		}
	default:
		api.exitWithError(w, http.StatusBadRequest, "unknown action")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (controller *Controller) acceptCallNaturePhraseCandidate(id uint64) error {
	var (
		label       string
		phrase      string
		acceptedAt  int64
		dismissedAt int64
	)
	err := controller.Database.Sql.QueryRow(
		`SELECT "label", "phrase", "acceptedAt", "dismissedAt"
		 FROM "callNaturePhraseCandidates" WHERE "candidateId" = $1`, id,
	).Scan(&label, &phrase, &acceptedAt, &dismissedAt)
	if err != nil {
		return fmt.Errorf("candidate not found")
	}
	if acceptedAt > 0 {
		return nil
	}
	if dismissedAt > 0 {
		return fmt.Errorf("candidate was dismissed")
	}
	label = strings.ToUpper(strings.TrimSpace(label))
	phrase = strings.ToUpper(strings.TrimSpace(phrase))
	if label == "" || phrase == "" {
		return fmt.Errorf("invalid candidate")
	}

	var (
		natureID    int64
		phrasesJSON string
	)
	err = controller.Database.Sql.QueryRow(
		`SELECT "callNatureId", "phrases" FROM "callNatures" WHERE "label" = $1`, label,
	).Scan(&natureID, &phrasesJSON)
	if err != nil {
		return fmt.Errorf("call nature %q not found — create the category first", label)
	}
	var phrases []string
	_ = json.Unmarshal([]byte(phrasesJSON), &phrases)
	exists := false
	for _, p := range phrases {
		if strings.ToUpper(strings.TrimSpace(p)) == phrase {
			exists = true
			break
		}
	}
	if !exists {
		phrases = append(phrases, phrase)
	}
	raw, _ := json.Marshal(phrases)
	_, err = controller.Database.Sql.Exec(
		`UPDATE "callNatures" SET "phrases" = $1 WHERE "callNatureId" = $2`,
		string(raw), natureID,
	)
	if err != nil {
		return err
	}
	_, _ = controller.Database.Sql.Exec(
		`UPDATE "callNaturePhraseCandidates" SET "acceptedAt" = $1 WHERE "candidateId" = $2`,
		time.Now().UnixMilli(), id,
	)
	_ = controller.CallNaturesCache.Read(controller.Database)
	return nil
}

func (api *Api) CallNaturePhraseScanHandler(w http.ResponseWriter, r *http.Request) {
	client := api.getClient(r)
	if client == nil || !api.isAdmin(client) {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	api.Controller.Options.mutex.Lock()
	mi := api.Controller.Options.MappingIntegration
	api.Controller.Options.mutex.Unlock()
	if !mi.CallNaturePhraseLearn {
		api.exitWithError(w, http.StatusBadRequest, "enable Suggest phrases from transcripts first")
		return
	}

	req := CallNaturePhraseScanRequest{}
	_ = json.NewDecoder(r.Body).Decode(&req)
	hours := req.Hours
	if hours <= 0 {
		hours = callNaturePhraseLearnScanDefaultHours
	}
	if hours > callNaturePhraseLearnScanMaxHours {
		hours = callNaturePhraseLearnScanMaxHours
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > callNaturePhraseLearnScanMaxCalls {
		limit = callNaturePhraseLearnScanMaxCalls
	}

	since := time.Now().Add(-time.Duration(hours) * time.Hour).UnixMilli()
	rows, err := api.Controller.Database.Sql.Query(
		`SELECT "callId", COALESCE("transcript", ''), COALESCE("incidentNature", ''), "timestamp"
		 FROM "calls"
		 WHERE "timestamp" >= $1
		   AND length(trim(COALESCE("transcript", ''))) > 0
		 ORDER BY "timestamp" DESC
		 LIMIT $2`,
		since, limit,
	)
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	natureData := CallNatureMatchData{}
	if api.Controller.CallNaturesCache != nil {
		natureData = api.Controller.CallNaturesCache.MatchData(false)
	}

	scanned := 0
	withNature := 0
	touched := 0
	for rows.Next() {
		var (
			callId   uint64
			transcript string
			nature   string
			ts       int64
		)
		if err := rows.Scan(&callId, &transcript, &nature, &ts); err != nil {
			continue
		}
		scanned++
		nature = strings.ToUpper(strings.TrimSpace(nature))
		if nature == "" || mapping.IsDefaultUnknownNatureLabel(nature) {
			continue
		}
		withNature++
		before := touched
		api.Controller.processCallNaturePhraseLearn(callId, transcript, nature, transcript, natureData)
		// Count candidates for this nature that mention this call is hard; approximate by running learn.
		_ = before
		touched++
	}

	suggestions, _ := api.Controller.listCallNaturePhraseSuggestions(false)
	ready := 0
	for _, s := range suggestions {
		if s.Ready {
			ready++
		}
	}
	msg := fmt.Sprintf(
		"Scanned %d calls (%d with a classified nature). Suggestions appear after %d sightings — review and Accept to add phrases.",
		scanned, withNature, callNaturePhraseLearnMinSightings(mi),
	)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(CallNaturePhraseScanResponse{
		CallsScanned:      scanned,
		CallsWithNature:   withNature,
		CandidatesTouched: touched,
		ReadyCount:        ready,
		LookbackHours:     hours,
		MinSightings:      callNaturePhraseLearnMinSightings(mi),
		Suggestions:       suggestions,
		Message:           msg,
	})
}
