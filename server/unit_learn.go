// Copyright (C) 2025 Thinline Dynamic Solutions
//
// Auto-learn unit aliases: map radio unitRef to human labels using metadata and OpenAI.
// Consistent P25 radio aliases auto-add; OpenAI / conflicts queue for admin Accept/Dismiss.

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const unitLearnMaxCallRecords = 25

type unitLearnCallRecord struct {
	CallId     uint64 `json:"callId"`
	Transcript string `json:"transcript"`
	RadioLabel string `json:"radioLabel"`
	Timestamp  int64  `json:"timestamp"`
}

type unitObservation struct {
	UnitRef    uint
	RadioLabel string
}

func callHasUnitRefs(call *Call) bool {
	if call == nil {
		return false
	}
	if len(call.Meta.UnitRefs) > 0 {
		return true
	}
	for _, u := range call.Units {
		if u.UnitRef > 0 {
			return true
		}
	}
	return false
}

func extractUnitObservations(call *Call) []unitObservation {
	if call == nil {
		return nil
	}

	seen := map[uint]bool{}
	var out []unitObservation

	add := func(unitRef uint, label string) {
		if unitRef == 0 || seen[unitRef] {
			return
		}
		seen[unitRef] = true
		out = append(out, unitObservation{UnitRef: unitRef, RadioLabel: strings.TrimSpace(label)})
	}

	for i, unitRef := range call.Meta.UnitRefs {
		label := ""
		if i < len(call.Meta.UnitLabels) {
			label = call.Meta.UnitLabels[i]
		}
		add(unitRef, label)
	}

	for _, u := range call.Units {
		label := u.Label
		if label == "" && u.UnitRef > 0 {
			for i, ref := range call.Meta.UnitRefs {
				if ref == u.UnitRef && i < len(call.Meta.UnitLabels) {
					label = call.Meta.UnitLabels[i]
					break
				}
			}
		}
		add(u.UnitRef, label)
	}

	return out
}

func unitLearnEnabled(call *Call) bool {
	return call != nil && call.System != nil && call.Talkgroup != nil &&
		call.Talkgroup.AutoLearnUnitAliases &&
		call.System.AlertsEnabled && call.Talkgroup.AlertsEnabled
}

func unitExistsWithLabel(units *Units, unitRef uint) bool {
	if units == nil || unitRef == 0 {
		return false
	}
	units.mutex.Lock()
	defer units.mutex.Unlock()
	for _, u := range units.List {
		if u.UnitRef == unitRef && strings.TrimSpace(u.Label) != "" {
			return true
		}
	}
	return false
}

func truncateUnitLearnTranscript(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 240 {
		return s[:240]
	}
	return s
}

// processUnitAutoLearn records unitRef observations and auto-adds confident P25 aliases
// after N calls; OpenAI / ambiguous cases become reviewable suggestions.
func (controller *Controller) processUnitAutoLearn(call *Call, transcript string) {
	if controller == nil || call == nil || !unitLearnEnabled(call) {
		return
	}
	if !callHasUnitRefs(call) {
		return
	}

	cfg := controller.Options.AutoLearnToneSetConfig
	cfg.normalize()
	callsRequired := cfg.CallsRequired

	transcript = strings.ToUpper(strings.TrimSpace(transcript))
	observations := extractUnitObservations(call)
	for _, obs := range observations {
		if unitExistsWithLabel(call.System.Units, obs.UnitRef) {
			continue
		}
		controller.upsertUnitLearnCandidate(call, obs, transcript, callsRequired)
	}
}

func (controller *Controller) upsertUnitLearnCandidate(call *Call, obs unitObservation, transcript string, callsRequired int) {
	now := time.Now().UnixMilli()

	var candidateId uint64
	var callRecordsJson string
	var finalizedAt, acceptedAt, dismissedAt sql.NullInt64

	selectQuery := `SELECT "candidateId", "callRecords", "finalizedAt", "acceptedAt", "dismissedAt" FROM "unitAliasLearnCandidates" WHERE "systemId" = $1 AND "talkgroupId" = $2 AND "unitRef" = $3`
	if controller.Database.Config.DbType != DbTypePostgresql {
		selectQuery = `SELECT "candidateId", "callRecords", "finalizedAt", "acceptedAt", "dismissedAt" FROM "unitAliasLearnCandidates" WHERE "systemId" = ? AND "talkgroupId" = ? AND "unitRef" = ?`
	}

	err := controller.Database.Sql.QueryRow(selectQuery, call.System.Id, call.Talkgroup.Id, obs.UnitRef).
		Scan(&candidateId, &callRecordsJson, &finalizedAt, &acceptedAt, &dismissedAt)

	records := []unitLearnCallRecord{}
	if err == nil && callRecordsJson != "" {
		_ = json.Unmarshal([]byte(callRecordsJson), &records)
	}

	if (finalizedAt.Valid && finalizedAt.Int64 > 0) ||
		(acceptedAt.Valid && acceptedAt.Int64 > 0) ||
		(dismissedAt.Valid && dismissedAt.Int64 > 0) {
		return
	}

	snippet := truncateUnitLearnTranscript(transcript)
	updatedExisting := false
	for i, r := range records {
		if r.CallId != call.Id {
			continue
		}
		if snippet != "" {
			records[i].Transcript = snippet
		}
		if strings.TrimSpace(obs.RadioLabel) != "" {
			records[i].RadioLabel = strings.TrimSpace(obs.RadioLabel)
		}
		updatedExisting = true
		break
	}

	if !updatedExisting {
		records = append(records, unitLearnCallRecord{
			CallId:     call.Id,
			Transcript: snippet,
			RadioLabel: strings.TrimSpace(obs.RadioLabel),
			Timestamp:  call.Timestamp.UnixMilli(),
		})
	}
	if len(records) > unitLearnMaxCallRecords {
		records = records[len(records)-unitLearnMaxCallRecords:]
	}
	recordsJson, _ := json.Marshal(records)

	if err == sql.ErrNoRows {
		insertQuery := `INSERT INTO "unitAliasLearnCandidates" ("systemId", "talkgroupId", "unitRef", "callRecords", "firstSeenAt", "lastSeenAt") VALUES ($1, $2, $3, $4, $5, $6)`
		if controller.Database.Config.DbType != DbTypePostgresql {
			insertQuery = `INSERT INTO "unitAliasLearnCandidates" ("systemId", "talkgroupId", "unitRef", "callRecords", "firstSeenAt", "lastSeenAt") VALUES (?, ?, ?, ?, ?, ?)`
		}
		if _, err := controller.Database.Sql.Exec(insertQuery, call.System.Id, call.Talkgroup.Id, obs.UnitRef, string(recordsJson), now, now); err != nil {
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("unit auto-learn: insert candidate failed: %v", err))
		}
	} else if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("unit auto-learn: load candidate failed: %v", err))
		return
	} else {
		updateQuery := `UPDATE "unitAliasLearnCandidates" SET "callRecords" = $1, "lastSeenAt" = $2 WHERE "candidateId" = $3`
		if controller.Database.Config.DbType != DbTypePostgresql {
			updateQuery = `UPDATE "unitAliasLearnCandidates" SET "callRecords" = ?, "lastSeenAt" = ? WHERE "candidateId" = ?`
		}
		if _, err := controller.Database.Sql.Exec(updateQuery, string(recordsJson), now, candidateId); err != nil {
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("unit auto-learn: update candidate failed: %v", err))
			return
		}
	}

	if len(records) < callsRequired {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
			"unit auto-learn: unitRef %d on talkgroup %d at %d/%d calls",
			obs.UnitRef, call.Talkgroup.TalkgroupRef, len(records), callsRequired,
		))
		return
	}

	label, consistent, conflict := consistentRadioLabel(records)
	if conflict {
		controller.queueUnitLearnForReview(call.System, call.Talkgroup, obs.UnitRef, "",
			"conflicting radio aliases across calls", false)
		return
	}
	if consistent && label != "" {
		go controller.autoAddLearnedUnitAlias(call.System, call.Talkgroup, obs.UnitRef, label, records, false)
		return
	}

	if !unitLearnHasTranscriptVoice(controller, records) {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
			"unit auto-learn: unitRef %d has %d calls but no consistent radio label or voice transcript yet",
			obs.UnitRef, len(records),
		))
		return
	}

	suggested := controller.suggestUnitLearnLabel(call.System, call.Talkgroup, obs.UnitRef, records)
	if suggested == "" || suggested == "UNKNOWN" {
		controller.queueUnitLearnForReview(call.System, call.Talkgroup, obs.UnitRef, "",
			"could not determine a consistent unit label", true)
		return
	}
	controller.queueUnitLearnForReview(call.System, call.Talkgroup, obs.UnitRef, suggested,
		"suggested from voice transcripts", true)
}

// queueUnitLearnForReview stores a suggested label for admin Accept/Dismiss (does not write units).
func (controller *Controller) queueUnitLearnForReview(system *System, talkgroup *Talkgroup, unitRef uint, suggestedLabel, reason string, usedOpenAI bool) {
	if controller == nil || system == nil || talkgroup == nil || unitRef == 0 {
		return
	}

	suggestedLabel = strings.TrimSpace(suggestedLabel)
	reason = strings.TrimSpace(reason)

	updateQuery := `UPDATE "unitAliasLearnCandidates" SET "suggestedLabel" = $1, "reason" = $2, "usedOpenAI" = $3 WHERE "systemId" = $4 AND "talkgroupId" = $5 AND "unitRef" = $6 AND ("finalizedAt" IS NULL OR "finalizedAt" = 0) AND ("acceptedAt" IS NULL OR "acceptedAt" = 0) AND ("dismissedAt" IS NULL OR "dismissedAt" = 0)`
	if controller.Database.Config.DbType != DbTypePostgresql {
		updateQuery = `UPDATE "unitAliasLearnCandidates" SET "suggestedLabel" = ?, "reason" = ?, "usedOpenAI" = ? WHERE "systemId" = ? AND "talkgroupId" = ? AND "unitRef" = ? AND ("finalizedAt" IS NULL OR "finalizedAt" = 0) AND ("acceptedAt" IS NULL OR "acceptedAt" = 0) AND ("dismissedAt" IS NULL OR "dismissedAt" = 0)`
	}
	res, err := controller.Database.Sql.Exec(updateQuery, suggestedLabel, reason, usedOpenAI, system.Id, talkgroup.Id, unitRef)
	if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("unit auto-learn: queue for review failed: %v", err))
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return
	}

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
		"unit auto-learn: queued unitRef %d on talkgroup %d for review — %s (label=%q)",
		unitRef, talkgroup.TalkgroupRef, reason, suggestedLabel,
	))
}

func consistentRadioLabel(records []unitLearnCallRecord) (label string, consistent bool, conflict bool) {
	seen := map[string]string{}
	for _, r := range records {
		l := strings.TrimSpace(r.RadioLabel)
		if l == "" {
			continue
		}
		key := strings.ToUpper(l)
		if _, ok := seen[key]; !ok {
			seen[key] = l
		}
	}
	if len(seen) == 0 {
		return "", false, false
	}
	if len(seen) > 1 {
		return "", false, true
	}
	for _, v := range seen {
		return v, true, false
	}
	return "", false, false
}

func unitLearnHasTranscriptVoice(controller *Controller, records []unitLearnCallRecord) bool {
	for _, r := range records {
		if strings.TrimSpace(r.Transcript) == "" {
			continue
		}
		if controller.isVoiceForToneAlerts(r.Transcript) {
			return true
		}
	}
	return false
}

func (controller *Controller) autoAddLearnedUnitAlias(system *System, talkgroup *Talkgroup, unitRef uint, label string, records []unitLearnCallRecord, usedOpenAI bool) {
	if controller == nil || system == nil || talkgroup == nil || unitRef == 0 {
		return
	}

	label = strings.TrimSpace(label)
	if label == "" {
		return
	}

	now := time.Now().UnixMilli()
	updateQuery := `UPDATE "unitAliasLearnCandidates" SET "finalizedAt" = $1, "acceptedAt" = $1, "suggestedLabel" = $2, "usedOpenAI" = $3 WHERE "systemId" = $4 AND "talkgroupId" = $5 AND "unitRef" = $6 AND ("finalizedAt" IS NULL OR "finalizedAt" = 0) AND ("acceptedAt" IS NULL OR "acceptedAt" = 0) AND ("dismissedAt" IS NULL OR "dismissedAt" = 0)`
	if controller.Database.Config.DbType != DbTypePostgresql {
		updateQuery = `UPDATE "unitAliasLearnCandidates" SET "finalizedAt" = ?, "acceptedAt" = ?, "suggestedLabel" = ?, "usedOpenAI" = ? WHERE "systemId" = ? AND "talkgroupId" = ? AND "unitRef" = ? AND ("finalizedAt" IS NULL OR "finalizedAt" = 0) AND ("acceptedAt" IS NULL OR "acceptedAt" = 0) AND ("dismissedAt" IS NULL OR "dismissedAt" = 0)`
	}
	var res sql.Result
	var err error
	if controller.Database.Config.DbType == DbTypePostgresql {
		res, err = controller.Database.Sql.Exec(updateQuery, now, label, usedOpenAI, system.Id, talkgroup.Id, unitRef)
	} else {
		res, err = controller.Database.Sql.Exec(updateQuery, now, now, label, usedOpenAI, system.Id, talkgroup.Id, unitRef)
	}
	if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("unit auto-learn: mark finalized failed: %v", err))
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return
	}

	sys, ok := controller.Systems.GetSystemById(system.Id)
	if !ok {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("unit auto-learn: system %d not in cache", system.Id))
		return
	}
	if unitExistsWithLabel(sys.Units, unitRef) {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("unit auto-learn: unitRef %d already exists on system %s", unitRef, system.Label))
		return
	}

	if err := controller.persistLearnedUnit(sys.Id, unitRef, label); err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("unit auto-learn: persist unit %d failed: %v", unitRef, err))
		return
	}

	sys.Units.Add(unitRef, label)

	source := "radio metadata"
	if usedOpenAI {
		source = "OpenAI"
	}
	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
		"unit auto-learn: added unit %q (ref %d) on system %s via %s (%d calls, talkgroup %d)",
		label, unitRef, system.Label, source, len(records), talkgroup.TalkgroupRef,
	))
}

func (controller *Controller) persistLearnedUnit(systemId uint64, unitRef uint, label string) error {
	var query string
	if controller.Database.Config.DbType == DbTypePostgresql {
		query = `INSERT INTO "units" ("label", "order", "systemId", "unitRef", "unitFrom", "unitTo") VALUES ($1, 0, $2, $3, 0, 0)`
		_, err := controller.Database.Sql.Exec(query, label, systemId, unitRef)
		return err
	}
	query = fmt.Sprintf(`INSERT INTO "units" ("label", "order", "systemId", "unitRef", "unitFrom", "unitTo") VALUES ('%s', 0, %d, %d, 0, 0)`, escapeQuotes(label), systemId, unitRef)
	_, err := controller.Database.Sql.Exec(query)
	return err
}
