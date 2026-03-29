// Copyright (C) 2025 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// SuspectedHallucination represents a phrase that might be a hallucination
type SuspectedHallucination struct {
	Id              uint64   `json:"id"`
	Phrase          string   `json:"phrase"`
	RejectedCount   int      `json:"rejectedCount"`
	AcceptedCount   int      `json:"acceptedCount"`
	FirstSeenAt     int64    `json:"firstSeenAt"`
	LastSeenAt      int64    `json:"lastSeenAt"`
	SystemIds       []uint64 `json:"systemIds"`
	Status          string   `json:"status"` // "pending", "approved", "rejected", "auto_added"
	AutoAdded       bool     `json:"autoAdded"`
	ConfidenceScore float64  `json:"confidenceScore"`
	CreatedAt       int64    `json:"createdAt"`
	UpdatedAt       int64    `json:"updatedAt"`
}

// HallucinationDetector tracks and identifies potential hallucinations
type HallucinationDetector struct {
	controller *Controller
	mutex      sync.Mutex
}

// NewHallucinationDetector creates a new hallucination detector
func NewHallucinationDetector(controller *Controller) *HallucinationDetector {
	return &HallucinationDetector{
		controller: controller,
	}
}

// Emergency vocabulary that should never be flagged
var emergencyVocabulary = []string{
	"station", "engine", "truck", "unit", "medic", "ambulance",
	"fire", "ems", "rescue", "squad", "chief", "captain",
	"dispatch", "county", "city", "township", "village",
	"respond", "responding", "enroute", "on scene", "available", "clear",
	"copy", "10-4", "received", "roger", "acknowledged",
	"emergency", "code", "signal", "alarm", "alert",
	"street", "road", "avenue", "boulevard", "highway", "route",
	"north", "south", "east", "west",
	"911", "e911",
}

// TrackPhrase tracks a phrase from a transcript based on whether it was accepted or rejected
func (hd *HallucinationDetector) TrackPhrase(transcript string, wasAccepted bool, systemId uint64) {
	// Check if detection is enabled
	mode := hd.controller.Options.TranscriptionConfig.HallucinationDetectionMode
	if mode == "" || mode == "off" {
		return
	}

	// Don't track empty or very short transcripts
	if len(strings.TrimSpace(transcript)) < 10 {
		return
	}

	// Normalize the transcript
	phrase := strings.TrimSpace(strings.ToUpper(transcript))

	// Don't track phrases containing emergency vocabulary (these are legitimate)
	if hd.containsEmergencyVocabulary(phrase) {
		return
	}

	// Track this phrase in the database
	hd.mutex.Lock()
	defer hd.mutex.Unlock()

	existing, err := hd.getOrCreatePhrase(phrase, systemId)
	if err != nil {
		hd.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to track phrase: %v", err))
		return
	}

	// Update counts
	if wasAccepted {
		existing.AcceptedCount++
	} else {
		existing.RejectedCount++
	}
	existing.LastSeenAt = time.Now().UnixMilli()
	existing.UpdatedAt = time.Now().UnixMilli()

	// Add system ID if not already present
	if !hd.containsSystemId(existing.SystemIds, systemId) {
		existing.SystemIds = append(existing.SystemIds, systemId)
	}

	// Calculate confidence score
	existing.ConfidenceScore = hd.calculateConfidenceScore(existing)

	// Save updated phrase
	if err := hd.savePhrase(existing); err != nil {
		hd.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to save tracked phrase: %v", err))
		return
	}

	// Check if we should auto-add this pattern
	if mode == "auto" && existing.Status == "pending" {
		if hd.shouldAutoAdd(existing) {
			hd.autoAddPattern(existing)
		}
	}
}

// containsEmergencyVocabulary checks if phrase contains any emergency vocabulary
func (hd *HallucinationDetector) containsEmergencyVocabulary(phrase string) bool {
	phraseUpper := strings.ToUpper(phrase)
	for _, word := range emergencyVocabulary {
		if strings.Contains(phraseUpper, strings.ToUpper(word)) {
			return true
		}
	}
	return false
}

// containsSystemId checks if system ID is in the list
func (hd *HallucinationDetector) containsSystemId(systemIds []uint64, systemId uint64) bool {
	for _, id := range systemIds {
		if id == systemId {
			return true
		}
	}
	return false
}

// calculateConfidenceScore calculates how confident we are that this is a hallucination
func (hd *HallucinationDetector) calculateConfidenceScore(sh *SuspectedHallucination) float64 {
	score := 0.0

	// If it ever appeared in accepted calls, confidence drops dramatically
	if sh.AcceptedCount > 0 {
		return 0.0
	}

	// Rejected count score (up to 5 points)
	if sh.RejectedCount >= 10 {
		score += 5.0
	} else if sh.RejectedCount >= 5 {
		score += 3.0
	} else if sh.RejectedCount >= 3 {
		score += 1.0
	}

	// Multiple systems score (up to 3 points)
	if len(sh.SystemIds) >= 3 {
		score += 3.0
	} else if len(sh.SystemIds) >= 2 {
		score += 2.0
	} else {
		score += 1.0
	}

	// Time window score (up to 2 points)
	// Phrases that appear over longer time periods are more suspicious
	daysSinceFirst := float64(time.Now().UnixMilli()-sh.FirstSeenAt) / (1000.0 * 60 * 60 * 24)
	if daysSinceFirst >= 7 {
		score += 2.0
	} else if daysSinceFirst >= 3 {
		score += 1.0
	}

	return score
}

// shouldAutoAdd determines if a phrase should be automatically added to the filter
func (hd *HallucinationDetector) shouldAutoAdd(sh *SuspectedHallucination) bool {
	config := hd.controller.Options.TranscriptionConfig

	// Get thresholds (with defaults)
	minOccurrences := config.HallucinationMinOccurrences
	if minOccurrences == 0 {
		minOccurrences = 5
	}

	// Must have minimum rejected count
	if sh.RejectedCount < minOccurrences {
		return false
	}

	// Must NEVER appear in accepted calls (zero-tolerance rule)
	if sh.AcceptedCount > 0 {
		return false
	}

	// Must have high confidence score (at least 6/10)
	if sh.ConfidenceScore < 6.0 {
		return false
	}

	// Must appear on at least 2 systems (to avoid system-specific language)
	if len(sh.SystemIds) < 2 {
		return false
	}

	return true
}

// autoAddPattern automatically adds a pattern to the hallucination filter
func (hd *HallucinationDetector) autoAddPattern(sh *SuspectedHallucination) {
	// Add to hallucination patterns
	patterns := hd.controller.Options.TranscriptionConfig.HallucinationPatterns
	patterns = append(patterns, sh.Phrase)
	hd.controller.Options.TranscriptionConfig.HallucinationPatterns = patterns

	// Update status
	sh.Status = "auto_added"
	sh.AutoAdded = true
	sh.UpdatedAt = time.Now().UnixMilli()

	// Save to database
	if err := hd.savePhrase(sh); err != nil {
		hd.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to save auto-added pattern: %v", err))
		return
	}

	// Save options (to persist the new pattern)
	if err := hd.controller.Options.Write(hd.controller.Database); err != nil {
		hd.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to save options after auto-add: %v", err))
		return
	}

	hd.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("auto-added hallucination pattern: %q (rejected: %d, systems: %d, score: %.1f)",
		sh.Phrase, sh.RejectedCount, len(sh.SystemIds), sh.ConfidenceScore))
}

// getOrCreatePhrase gets an existing phrase or creates a new one
func (hd *HallucinationDetector) getOrCreatePhrase(phrase string, systemId uint64) (*SuspectedHallucination, error) {
	// Try to get existing
	existing, err := hd.getPhrase(phrase)
	if err == nil && existing != nil {
		return existing, nil
	}

	// Create new
	now := time.Now().UnixMilli()
	sh := &SuspectedHallucination{
		Phrase:        phrase,
		RejectedCount: 0,
		AcceptedCount: 0,
		FirstSeenAt:   now,
		LastSeenAt:    now,
		SystemIds:     []uint64{systemId},
		Status:        "pending",
		AutoAdded:     false,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	// Insert into database
	systemIdsJson, _ := json.Marshal(sh.SystemIds)
	query := fmt.Sprintf(`INSERT INTO "suspectedHallucinations" ("phrase", "rejectedCount", "acceptedCount", "firstSeenAt", "lastSeenAt", "systemIds", "status", "autoAdded", "createdAt", "updatedAt") VALUES ($1, %d, %d, %d, %d, $2, '%s', %t, %d, %d) RETURNING "id"`,
		sh.RejectedCount, sh.AcceptedCount, sh.FirstSeenAt, sh.LastSeenAt, sh.Status, sh.AutoAdded, sh.CreatedAt, sh.UpdatedAt)

	err = hd.controller.Database.Sql.QueryRow(query, phrase, string(systemIdsJson)).Scan(&sh.Id)

	if err != nil {
		return nil, err
	}

	return sh, nil
}

// getPhrase retrieves a phrase from the database
func (hd *HallucinationDetector) getPhrase(phrase string) (*SuspectedHallucination, error) {
	query := `SELECT "id", "phrase", "rejectedCount", "acceptedCount", "firstSeenAt", "lastSeenAt", "systemIds", "status", "autoAdded", "createdAt", "updatedAt" FROM "suspectedHallucinations" WHERE "phrase" = $1`

	var sh SuspectedHallucination
	var systemIdsJson string

	err := hd.controller.Database.Sql.QueryRow(query, phrase).Scan(
		&sh.Id, &sh.Phrase, &sh.RejectedCount, &sh.AcceptedCount,
		&sh.FirstSeenAt, &sh.LastSeenAt, &systemIdsJson, &sh.Status,
		&sh.AutoAdded, &sh.CreatedAt, &sh.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Parse system IDs
	if systemIdsJson != "" {
		json.Unmarshal([]byte(systemIdsJson), &sh.SystemIds)
	}

	// Calculate confidence score
	sh.ConfidenceScore = hd.calculateConfidenceScore(&sh)

	return &sh, nil
}

// savePhrase saves a phrase to the database
func (hd *HallucinationDetector) savePhrase(sh *SuspectedHallucination) error {
	systemIdsJson, _ := json.Marshal(sh.SystemIds)

	query := fmt.Sprintf(`UPDATE "suspectedHallucinations" SET "rejectedCount" = %d, "acceptedCount" = %d, "lastSeenAt" = %d, "systemIds" = $1, "status" = '%s', "autoAdded" = %t, "updatedAt" = %d WHERE "id" = %d`,
		sh.RejectedCount, sh.AcceptedCount, sh.LastSeenAt, escapeQuotes(sh.Status), sh.AutoAdded, sh.UpdatedAt, sh.Id)

	_, err := hd.controller.Database.Sql.Exec(query, string(systemIdsJson))
	return err
}

// GetPendingSuggestions returns all pending hallucination suggestions
func (hd *HallucinationDetector) GetPendingSuggestions() ([]*SuspectedHallucination, error) {
	query := `SELECT "id", "phrase", "rejectedCount", "acceptedCount", "firstSeenAt", "lastSeenAt", "systemIds", "status", "autoAdded", "createdAt", "updatedAt" FROM "suspectedHallucinations" WHERE "status" = 'pending' ORDER BY "rejectedCount" DESC, "lastSeenAt" DESC`

	rows, err := hd.controller.Database.Sql.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var suggestions []*SuspectedHallucination
	for rows.Next() {
		var sh SuspectedHallucination
		var systemIdsJson string

		err := rows.Scan(&sh.Id, &sh.Phrase, &sh.RejectedCount, &sh.AcceptedCount,
			&sh.FirstSeenAt, &sh.LastSeenAt, &systemIdsJson, &sh.Status,
			&sh.AutoAdded, &sh.CreatedAt, &sh.UpdatedAt)

		if err != nil {
			continue
		}

		// Parse system IDs
		if systemIdsJson != "" {
			json.Unmarshal([]byte(systemIdsJson), &sh.SystemIds)
		}

		// Calculate confidence score
		sh.ConfidenceScore = hd.calculateConfidenceScore(&sh)

		suggestions = append(suggestions, &sh)
	}

	return suggestions, nil
}

// ApproveHallucination manually approves a hallucination and adds it to the filter
func (hd *HallucinationDetector) ApproveHallucination(id uint64) error {
	hd.mutex.Lock()
	defer hd.mutex.Unlock()

	// Get the phrase
	query := `SELECT "id", "phrase", "rejectedCount", "acceptedCount", "firstSeenAt", "lastSeenAt", "systemIds", "status", "autoAdded", "createdAt", "updatedAt" FROM "suspectedHallucinations" WHERE "id" = $1`
	if hd.controller.Database.Config.DbType != DbTypePostgresql {
		query = `SELECT "id", "phrase", "rejectedCount", "acceptedCount", "firstSeenAt", "lastSeenAt", "systemIds", "status", "autoAdded", "createdAt", "updatedAt" FROM "suspectedHallucinations" WHERE "id" = ?`
	}

	var sh SuspectedHallucination
	var systemIdsJson string

	err := hd.controller.Database.Sql.QueryRow(query, id).Scan(
		&sh.Id, &sh.Phrase, &sh.RejectedCount, &sh.AcceptedCount,
		&sh.FirstSeenAt, &sh.LastSeenAt, &systemIdsJson, &sh.Status,
		&sh.AutoAdded, &sh.CreatedAt, &sh.UpdatedAt,
	)

	if err != nil {
		return err
	}

	// Add to hallucination patterns
	patterns := hd.controller.Options.TranscriptionConfig.HallucinationPatterns

	// Check if already exists
	for _, p := range patterns {
		if strings.EqualFold(p, sh.Phrase) {
			return fmt.Errorf("pattern already exists")
		}
	}

	patterns = append(patterns, sh.Phrase)
	hd.controller.Options.TranscriptionConfig.HallucinationPatterns = patterns

	// Update status
	sh.Status = "approved"
	sh.UpdatedAt = time.Now().UnixMilli()

	// Save to database
	query = fmt.Sprintf(`UPDATE "suspectedHallucinations" SET "status" = 'approved', "updatedAt" = %d WHERE "id" = %d`, sh.UpdatedAt, sh.Id)
	if _, err := hd.controller.Database.Sql.Exec(query); err != nil {
		return err
	}

	// Save options (to persist the new pattern)
	if err := hd.controller.Options.Write(hd.controller.Database); err != nil {
		return err
	}

	hd.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("manually approved hallucination pattern: %q", sh.Phrase))

	return nil
}

// RejectHallucination marks a suspected hallucination as rejected (not a hallucination)
func (hd *HallucinationDetector) RejectHallucination(id uint64) error {
	hd.mutex.Lock()
	defer hd.mutex.Unlock()

	query := fmt.Sprintf(`UPDATE "suspectedHallucinations" SET "status" = 'rejected', "updatedAt" = %d WHERE "id" = %d`, time.Now().UnixMilli(), id)
	_, err := hd.controller.Database.Sql.Exec(query)
	return err
}
