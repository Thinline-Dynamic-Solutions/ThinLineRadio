// Copyright (C) 2025 Thinline Dynamic Solutions
//
// Admin review APIs for unit alias auto-learn suggestions (Accept / Dismiss / history scan).

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	unitAliasScanDefaultHours = 168
	unitAliasScanMaxHours     = 720
	unitAliasScanDefaultLimit = 200
	unitAliasScanMaxLimit     = 500
)

// UnitAliasSuggestion is a rolled-up review row for one system unitRef.
type UnitAliasSuggestion struct {
	CandidateId    uint64   `json:"candidateId"`
	SystemId       uint64   `json:"systemId"`
	UnitRef        uint     `json:"unitRef"`
	SuggestedLabel string   `json:"suggestedLabel"`
	Reason         string   `json:"reason,omitempty"`
	UsedOpenAI     bool     `json:"usedOpenAI"`
	Sightings      int      `json:"sightings"`
	SampleCallIds  []uint64 `json:"sampleCallIds,omitempty"`
	SampleTranscript string `json:"sampleTranscript,omitempty"`
	FirstSeenAt    int64    `json:"firstSeenAt"`
	LastSeenAt     int64    `json:"lastSeenAt"`
	Ready          bool     `json:"ready"`
	TalkgroupIds   []uint64 `json:"talkgroupIds,omitempty"`
}

type UnitAliasLearnStatus struct {
	Enabled       bool `json:"enabled"`
	CallsRequired int  `json:"callsRequired"`
	PendingReady  int  `json:"pendingReady"`
	PendingAll    int  `json:"pendingAll"`
}

type UnitAliasSuggestionsResponse struct {
	Status      UnitAliasLearnStatus   `json:"status"`
	Suggestions []UnitAliasSuggestion  `json:"suggestions"`
}

type UnitAliasScanRequest struct {
	SystemId uint64 `json:"systemId"`
	Hours    int    `json:"hours"`
	Limit    int    `json:"limit"`
}

type UnitAliasScanResponse struct {
	CallsScanned      int                   `json:"callsScanned"`
	CallsWithUnits    int                   `json:"callsWithUnits"`
	CandidatesTouched int                   `json:"candidatesTouched"`
	ReadyCount        int                   `json:"readyCount"`
	LookbackHours     int                   `json:"lookbackHours"`
	CallsRequired     int                   `json:"callsRequired"`
	Suggestions       []UnitAliasSuggestion `json:"suggestions"`
	Message           string                `json:"message,omitempty"`
}

type UnitAliasAcceptRequest struct {
	Label string `json:"label"`
}

func (controller *Controller) unitLearnCallsRequired() int {
	cfg := controller.Options.AutoLearnToneSetConfig
	cfg.normalize()
	return cfg.CallsRequired
}

func (controller *Controller) systemHasUnitLearnEnabled(systemId uint64) bool {
	sys, ok := controller.Systems.GetSystemById(systemId)
	if !ok || sys == nil {
		return false
	}
	if sys.AutoLearnUnitAliases {
		return true
	}
	if sys.Talkgroups == nil {
		return false
	}
	for _, tg := range sys.Talkgroups.List {
		if tg != nil && tg.AutoLearnUnitAliases {
			return true
		}
	}
	return false
}

func (controller *Controller) listUnitAliasSuggestions(systemId uint64, readyOnly bool) ([]UnitAliasSuggestion, error) {
	if controller == nil || controller.Database == nil || controller.Database.Sql == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	if systemId == 0 {
		return nil, fmt.Errorf("systemId is required")
	}

	callsRequired := controller.unitLearnCallsRequired()

	query := `SELECT "candidateId", "systemId", "talkgroupId", "unitRef", "callRecords", "suggestedLabel", "reason", "usedOpenAI", "firstSeenAt", "lastSeenAt"
		FROM "unitAliasLearnCandidates"
		WHERE "systemId" = $1
		  AND ("finalizedAt" IS NULL OR "finalizedAt" = 0)
		  AND ("acceptedAt" IS NULL OR "acceptedAt" = 0)
		  AND ("dismissedAt" IS NULL OR "dismissedAt" = 0)
		ORDER BY "lastSeenAt" DESC
		LIMIT 500`
	if controller.Database.Config.DbType != DbTypePostgresql {
		query = `SELECT "candidateId", "systemId", "talkgroupId", "unitRef", "callRecords", "suggestedLabel", "reason", "usedOpenAI", "firstSeenAt", "lastSeenAt"
			FROM "unitAliasLearnCandidates"
			WHERE "systemId" = ?
			  AND ("finalizedAt" IS NULL OR "finalizedAt" = 0)
			  AND ("acceptedAt" IS NULL OR "acceptedAt" = 0)
			  AND ("dismissedAt" IS NULL OR "dismissedAt" = 0)
			ORDER BY "lastSeenAt" DESC
			LIMIT 500`
	}

	rows, err := controller.Database.Sql.Query(query, systemId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type rowData struct {
		candidateId    uint64
		talkgroupId    uint64
		unitRef        uint
		records        []unitLearnCallRecord
		suggestedLabel string
		reason         string
		usedOpenAI     bool
		firstSeenAt    int64
		lastSeenAt     int64
	}

	byUnit := map[uint]*UnitAliasSuggestion{}
	callSeen := map[uint]map[uint64]bool{}

	for rows.Next() {
		var (
			r              rowData
			recordsJSON    string
			systemIdScan   uint64
			usedOpenAIScan bool
		)
		if err := rows.Scan(&r.candidateId, &systemIdScan, &r.talkgroupId, &r.unitRef, &recordsJSON,
			&r.suggestedLabel, &r.reason, &usedOpenAIScan, &r.firstSeenAt, &r.lastSeenAt); err != nil {
			return nil, err
		}
		r.usedOpenAI = usedOpenAIScan
		if recordsJSON != "" {
			_ = json.Unmarshal([]byte(recordsJSON), &r.records)
		}

		s, ok := byUnit[r.unitRef]
		if !ok {
			s = &UnitAliasSuggestion{
				CandidateId:    r.candidateId,
				SystemId:       systemId,
				UnitRef:        r.unitRef,
				SuggestedLabel: strings.TrimSpace(r.suggestedLabel),
				Reason:         strings.TrimSpace(r.reason),
				UsedOpenAI:     r.usedOpenAI,
				FirstSeenAt:    r.firstSeenAt,
				LastSeenAt:     r.lastSeenAt,
				TalkgroupIds:   []uint64{},
				SampleCallIds:  []uint64{},
			}
			byUnit[r.unitRef] = s
			callSeen[r.unitRef] = map[uint64]bool{}
		}

		s.TalkgroupIds = appendUniqueUint64(s.TalkgroupIds, r.talkgroupId)
		if r.lastSeenAt > s.LastSeenAt {
			s.LastSeenAt = r.lastSeenAt
			s.CandidateId = r.candidateId
		}
		if r.firstSeenAt > 0 && (s.FirstSeenAt == 0 || r.firstSeenAt < s.FirstSeenAt) {
			s.FirstSeenAt = r.firstSeenAt
		}
		if strings.TrimSpace(r.suggestedLabel) != "" && (s.SuggestedLabel == "" || r.usedOpenAI) {
			s.SuggestedLabel = strings.TrimSpace(r.suggestedLabel)
			s.UsedOpenAI = r.usedOpenAI
		}
		if strings.TrimSpace(r.reason) != "" && s.Reason == "" {
			s.Reason = strings.TrimSpace(r.reason)
		}

		for _, rec := range r.records {
			if rec.CallId == 0 || callSeen[r.unitRef][rec.CallId] {
				continue
			}
			callSeen[r.unitRef][rec.CallId] = true
			s.Sightings++
			if len(s.SampleCallIds) < 8 {
				s.SampleCallIds = append(s.SampleCallIds, rec.CallId)
			}
			if s.SampleTranscript == "" && strings.TrimSpace(rec.Transcript) != "" {
				s.SampleTranscript = truncateUnitLearnTranscript(rec.Transcript)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]UnitAliasSuggestion, 0, len(byUnit))
	for _, s := range byUnit {
		s.Ready = s.Sightings >= callsRequired && (strings.TrimSpace(s.SuggestedLabel) != "" || strings.TrimSpace(s.Reason) != "")
		if readyOnly && !s.Ready {
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Ready != out[j].Ready {
			return out[i].Ready
		}
		return out[i].LastSeenAt > out[j].LastSeenAt
	})
	return out, nil
}

func appendUniqueUint64(list []uint64, v uint64) []uint64 {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func (controller *Controller) acceptUnitAliasSuggestion(systemId, candidateId uint64, labelOverride string) error {
	if controller == nil || controller.Database == nil {
		return fmt.Errorf("database unavailable")
	}

	var (
		unitRef        uint
		suggestedLabel string
		sysId          uint64
		usedOpenAI     bool
		finalizedAt    int64
		acceptedAt     int64
		dismissedAt    int64
	)

	sel := `SELECT "systemId", "unitRef", "suggestedLabel", "usedOpenAI", "finalizedAt", "acceptedAt", "dismissedAt"
		FROM "unitAliasLearnCandidates" WHERE "candidateId" = $1`
	if controller.Database.Config.DbType != DbTypePostgresql {
		sel = `SELECT "systemId", "unitRef", "suggestedLabel", "usedOpenAI", "finalizedAt", "acceptedAt", "dismissedAt"
			FROM "unitAliasLearnCandidates" WHERE "candidateId" = ?`
	}
	err := controller.Database.Sql.QueryRow(sel, candidateId).
		Scan(&sysId, &unitRef, &suggestedLabel, &usedOpenAI, &finalizedAt, &acceptedAt, &dismissedAt)
	if err == sql.ErrNoRows {
		return fmt.Errorf("suggestion not found")
	}
	if err != nil {
		return err
	}
	if systemId != 0 && sysId != systemId {
		return fmt.Errorf("suggestion does not belong to this system")
	}
	if dismissedAt > 0 {
		return fmt.Errorf("suggestion was dismissed")
	}
	if acceptedAt > 0 || finalizedAt > 0 {
		return nil
	}

	label := strings.TrimSpace(labelOverride)
	if label == "" {
		label = strings.TrimSpace(suggestedLabel)
	}
	if label == "" {
		return fmt.Errorf("label is required — enter a unit label to accept")
	}

	sys, ok := controller.Systems.GetSystemById(sysId)
	if !ok {
		return fmt.Errorf("system %d not found", sysId)
	}

	if !unitExistsWithLabel(sys.Units, unitRef) {
		if err := controller.persistLearnedUnit(sys.Id, unitRef, label); err != nil {
			return fmt.Errorf("persist unit: %w", err)
		}
		sys.Units.Add(unitRef, label)
	}

	now := time.Now().UnixMilli()
	upd := `UPDATE "unitAliasLearnCandidates"
		SET "acceptedAt" = $1, "finalizedAt" = $1, "suggestedLabel" = $2
		WHERE "systemId" = $3 AND "unitRef" = $4
		  AND ("acceptedAt" IS NULL OR "acceptedAt" = 0)
		  AND ("dismissedAt" IS NULL OR "dismissedAt" = 0)`
	if controller.Database.Config.DbType != DbTypePostgresql {
		upd = `UPDATE "unitAliasLearnCandidates"
			SET "acceptedAt" = ?, "finalizedAt" = ?, "suggestedLabel" = ?
			WHERE "systemId" = ? AND "unitRef" = ?
			  AND ("acceptedAt" IS NULL OR "acceptedAt" = 0)
			  AND ("dismissedAt" IS NULL OR "dismissedAt" = 0)`
		_, err = controller.Database.Sql.Exec(upd, now, now, label, sysId, unitRef)
	} else {
		_, err = controller.Database.Sql.Exec(upd, now, label, sysId, unitRef)
	}
	if err != nil {
		return err
	}

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
		"unit auto-learn: accepted unit %q (ref %d) on system %s (openai=%v)",
		label, unitRef, sys.Label, usedOpenAI,
	))
	return nil
}

func (controller *Controller) dismissUnitAliasSuggestion(systemId, candidateId uint64) error {
	if controller == nil || controller.Database == nil {
		return fmt.Errorf("database unavailable")
	}

	var (
		unitRef     uint
		sysId       uint64
		acceptedAt  int64
		dismissedAt int64
	)
	sel := `SELECT "systemId", "unitRef", "acceptedAt", "dismissedAt" FROM "unitAliasLearnCandidates" WHERE "candidateId" = $1`
	if controller.Database.Config.DbType != DbTypePostgresql {
		sel = `SELECT "systemId", "unitRef", "acceptedAt", "dismissedAt" FROM "unitAliasLearnCandidates" WHERE "candidateId" = ?`
	}
	err := controller.Database.Sql.QueryRow(sel, candidateId).Scan(&sysId, &unitRef, &acceptedAt, &dismissedAt)
	if err == sql.ErrNoRows {
		return fmt.Errorf("suggestion not found")
	}
	if err != nil {
		return err
	}
	if systemId != 0 && sysId != systemId {
		return fmt.Errorf("suggestion does not belong to this system")
	}
	if acceptedAt > 0 {
		return fmt.Errorf("suggestion already accepted")
	}
	if dismissedAt > 0 {
		return nil
	}

	now := time.Now().UnixMilli()
	upd := `UPDATE "unitAliasLearnCandidates" SET "dismissedAt" = $1, "finalizedAt" = $1
		WHERE "systemId" = $2 AND "unitRef" = $3
		  AND ("acceptedAt" IS NULL OR "acceptedAt" = 0)
		  AND ("dismissedAt" IS NULL OR "dismissedAt" = 0)`
	if controller.Database.Config.DbType != DbTypePostgresql {
		upd = `UPDATE "unitAliasLearnCandidates" SET "dismissedAt" = ?, "finalizedAt" = ?
			WHERE "systemId" = ? AND "unitRef" = ?
			  AND ("acceptedAt" IS NULL OR "acceptedAt" = 0)
			  AND ("dismissedAt" IS NULL OR "dismissedAt" = 0)`
		_, err = controller.Database.Sql.Exec(upd, now, now, sysId, unitRef)
	} else {
		_, err = controller.Database.Sql.Exec(upd, now, sysId, unitRef)
	}
	if err != nil {
		return err
	}

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
		"unit auto-learn: dismissed unitRef %d on system %d", unitRef, sysId,
	))
	return nil
}

func (controller *Controller) scanUnitAliasHistory(systemId uint64, hours, limit int) (*UnitAliasScanResponse, error) {
	if controller == nil || controller.Database == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	if systemId == 0 {
		return nil, fmt.Errorf("systemId is required")
	}
	sys, ok := controller.Systems.GetSystemById(systemId)
	if !ok {
		return nil, fmt.Errorf("system %d not found", systemId)
	}

	lookback := unitAliasScanDefaultHours
	if hours > 0 {
		lookback = hours
	}
	if lookback > unitAliasScanMaxHours {
		lookback = unitAliasScanMaxHours
	}
	batchLimit := unitAliasScanDefaultLimit
	if limit > 0 {
		batchLimit = limit
	}
	if batchLimit > unitAliasScanMaxLimit {
		batchLimit = unitAliasScanMaxLimit
	}

	callsRequired := controller.unitLearnCallsRequired()
	resp := &UnitAliasScanResponse{
		LookbackHours: lookback,
		CallsRequired: callsRequired,
		Suggestions:   []UnitAliasSuggestion{},
	}

	cutoff := time.Now().Add(-time.Duration(lookback) * time.Hour).UnixMilli()

	query := `SELECT DISTINCT c."callId", c."talkgroupId", c."timestamp", COALESCE(c."transcript", '')
		FROM "calls" c
		INNER JOIN "callUnits" cu ON cu."callId" = c."callId"
		WHERE c."systemId" = $1 AND c."timestamp" >= $2 AND cu."unitRef" > 0
		ORDER BY c."timestamp" DESC
		LIMIT $3`
	if controller.Database.Config.DbType != DbTypePostgresql {
		query = `SELECT DISTINCT c."callId", c."talkgroupId", c."timestamp", COALESCE(c."transcript", '')
			FROM "calls" c
			INNER JOIN "callUnits" cu ON cu."callId" = c."callId"
			WHERE c."systemId" = ? AND c."timestamp" >= ? AND cu."unitRef" > 0
			ORDER BY c."timestamp" DESC
			LIMIT ?`
	}

	rows, err := controller.Database.Sql.Query(query, systemId, cutoff, batchLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scanCall struct {
		callId      uint64
		talkgroupId uint64
		timestamp   int64
		transcript  string
	}
	var batch []scanCall
	for rows.Next() {
		var c scanCall
		if err := rows.Scan(&c.callId, &c.talkgroupId, &c.timestamp, &c.transcript); err != nil {
			return nil, err
		}
		batch = append(batch, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	beforeReady, _ := controller.listUnitAliasSuggestions(systemId, false)
	beforeCount := len(beforeReady)

	for _, item := range batch {
		resp.CallsScanned++
		tg, tgOk := sys.Talkgroups.GetTalkgroupById(item.talkgroupId)
		if !tgOk || tg == nil || !tg.AutoLearnUnitAliases {
			continue
		}
		if !sys.AlertsEnabled || !tg.AlertsEnabled {
			continue
		}

		units, err := controller.loadCallUnitsForLearn(item.callId)
		if err != nil || len(units) == 0 {
			continue
		}
		resp.CallsWithUnits++

		call := NewCall()
		call.Id = item.callId
		call.System = sys
		call.Talkgroup = tg
		call.Timestamp = time.UnixMilli(item.timestamp)
		call.Transcript = item.transcript
		call.Units = units
		for _, u := range units {
			call.Meta.UnitRefs = append(call.Meta.UnitRefs, u.UnitRef)
			call.Meta.UnitLabels = append(call.Meta.UnitLabels, u.Label)
		}
		controller.processUnitAutoLearn(call, item.transcript)
	}

	suggestions, err := controller.listUnitAliasSuggestions(systemId, false)
	if err != nil {
		return nil, err
	}
	resp.Suggestions = suggestions
	resp.CandidatesTouched = len(suggestions)
	if len(suggestions) > beforeCount {
		resp.CandidatesTouched = len(suggestions) // still useful as open count
	}
	ready := 0
	for _, s := range suggestions {
		if s.Ready {
			ready++
		}
	}
	resp.ReadyCount = ready
	resp.Message = fmt.Sprintf(
		"Scanned %d calls (%d with units) over %dh. %d suggestion(s) ready to review.",
		resp.CallsScanned, resp.CallsWithUnits, lookback, ready,
	)
	return resp, nil
}

func (controller *Controller) loadCallUnitsForLearn(callId uint64) ([]CallUnit, error) {
	q := `SELECT "unitRef", COALESCE("label", ''), "offset" FROM "callUnits" WHERE "callId" = $1 ORDER BY "offset"`
	if controller.Database.Config.DbType != DbTypePostgresql {
		q = `SELECT "unitRef", COALESCE("label", ''), "offset" FROM "callUnits" WHERE "callId" = ? ORDER BY "offset"`
	}
	rows, err := controller.Database.Sql.Query(q, callId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CallUnit
	for rows.Next() {
		var u CallUnit
		var offset float64
		if err := rows.Scan(&u.UnitRef, &u.Label, &offset); err != nil {
			return nil, err
		}
		u.CallId = callId
		u.Offset = float32(offset)
		if u.UnitRef > 0 {
			out = append(out, u)
		}
	}
	return out, rows.Err()
}

func (admin *Admin) UnitAliasSuggestionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	token := admin.GetAuthorization(r)
	if !admin.ValidateToken(token) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	systemId, _ := strconv.ParseUint(r.URL.Query().Get("systemId"), 10, 64)
	if systemId == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"systemId is required"}`))
		return
	}
	readyOnly := r.URL.Query().Get("ready") == "1"

	list, err := admin.Controller.listUnitAliasSuggestions(systemId, readyOnly)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(fmt.Sprintf(`{"error":"%s"}`, escapeQuotes(err.Error()))))
		return
	}

	ready := 0
	for _, s := range list {
		if s.Ready {
			ready++
		}
	}
	resp := UnitAliasSuggestionsResponse{
		Status: UnitAliasLearnStatus{
			Enabled:       admin.Controller.systemHasUnitLearnEnabled(systemId),
			CallsRequired: admin.Controller.unitLearnCallsRequired(),
			PendingReady:  ready,
			PendingAll:    len(list),
		},
		Suggestions: list,
	}
	b, err := json.Marshal(resp)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

func (admin *Admin) UnitAliasSuggestionActionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	token := admin.GetAuthorization(r)
	if !admin.ValidateToken(token) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// /api/admin/unit-alias-suggestions/{id}/accept|dismiss
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	action := parts[len(parts)-1]
	id, err := strconv.ParseUint(parts[len(parts)-2], 10, 64)
	if err != nil || id == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	systemId, _ := strconv.ParseUint(r.URL.Query().Get("systemId"), 10, 64)

	switch action {
	case "accept":
		var req UnitAliasAcceptRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := admin.Controller.acceptUnitAliasSuggestion(systemId, id, req.Label); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(fmt.Sprintf(`{"error":"%s"}`, escapeQuotes(err.Error()))))
			return
		}
	case "dismiss":
		if err := admin.Controller.dismissUnitAliasSuggestion(systemId, id); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(fmt.Sprintf(`{"error":"%s"}`, escapeQuotes(err.Error()))))
			return
		}
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (admin *Admin) UnitAliasHistoryScanHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	token := admin.GetAuthorization(r)
	if !admin.ValidateToken(token) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var req UnitAliasScanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	result, err := admin.Controller.scanUnitAliasHistory(req.SystemId, req.Hours, req.Limit)
	if err != nil {
		admin.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("unit alias history scan failed: %s", err.Error()))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(fmt.Sprintf(`{"error":"%s"}`, escapeQuotes(err.Error()))))
		return
	}

	if b, err := json.Marshal(result); err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
	}
}
