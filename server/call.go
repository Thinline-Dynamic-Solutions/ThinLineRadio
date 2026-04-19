// Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
// Modified by Thinline Dynamic Solutions
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
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type CallFrequency struct {
	Id        uint64
	CallId    uint64
	Dbm       int
	Errors    uint
	Frequency uint
	Offset    float32
	Spikes    uint
}

type CallMeta struct {
	SiteId          uint64
	SiteLabel       string
	SiteRef         string // Site ID as string to preserve leading zeros
	SystemId        uint64
	SystemLabel     string
	SystemRef       uint
	TalkgroupGroups []string
	TalkgroupId     uint64
	TalkgroupLabel  string
	TalkgroupName   string
	TalkgroupRef    uint
	TalkgroupTag    string
	UnitLabels      []string
	UnitRefs        []uint
}

type CallUnit struct {
	Id      uint64
	CallId  uint64
	Label   string // P25 talker alias or other unit label (dynamic, from call upload)
	Offset  float32
	UnitRef uint
}

type Call struct {
	Id                   uint64
	Audio                []byte
	AudioFilename        string
	AudioMime            string
	OriginalAudio        []byte // Original audio before AAC conversion (used for transcription)
	OriginalAudioMime    string // Original audio MIME type
	Delayed              bool
	Frequencies          []CallFrequency
	Frequency            uint
	Meta                 CallMeta
	Patches              []uint
	SiteRef              string // Site ID as string to preserve leading zeros
	System               *System
	Talkgroup            *Talkgroup
	Timestamp            time.Time
	Units                []CallUnit
	ToneSequence         *ToneSequence
	HasTones             bool
	Transcript           string
	TranscriptConfidence float64
	TranscriptionStatus  string
	AlertSummary         string  // Optional short LLM summary for alerts (when summarized alerts enabled)
	ApiKeyId             *uint64 // API key used for upload (for preferred API key logic)

	// Add back simple fields for compatibility with v6 uploads
	SystemId    uint `json:"system"`
	TalkgroupId uint `json:"talkgroup"`

	// RDIO/upstream transcription passthrough fields
	// Set by the uploader when transcription was already processed externally.
	// Empty when transcription is disabled or audio is outside 2s-250s range.
	TransmissionId string    // upstream transmission ID (e.g. from RDIO/AlertPage)
	RequestId      string    // upstream request correlation ID
	SignalJobId    string    // upstream signal job ID (e.g. 1772856910589-fd88c97f)
	ReceivedAt     time.Time // when TLR received this call

	// Cached audio duration in seconds. Computed once on first call to getCallDuration
	// and reused for all subsequent checks (duration check, tone-only check, etc.).
	// Not persisted to DB or included in JSON output.
	Duration float64

	IsDuplicate bool `json:"isDuplicate,omitempty"`
	AudioHash   string `json:"audioHash,omitempty"`

	// IsForwarded is set when this call was received from another TLR server via
	// downstream forwarding. It is runtime-only (never stored in DB) and prevents
	// the receiving server from re-forwarding the call, breaking circular loops.
	IsForwarded bool `json:"-"`
}

func NewCall() *Call {
	return &Call{
		Frequencies: []CallFrequency{},
		Frequency:   0,
		Meta: CallMeta{
			TalkgroupGroups: []string{},
			UnitLabels:      []string{},
			UnitRefs:        []uint{},
		},
		Patches:     []uint{},
		Units:       []CallUnit{},
		SystemId:    0,
		TalkgroupId: 0,
	}
}

func (call *Call) IsValid() (ok bool, err error) {
	ok = true

	if len(call.Audio) <= 44 {
		ok = false
		err = errors.New("no audio")
	}

	if call.Timestamp.UnixMilli() == 0 {
		ok = false
		err = errors.New("no timestamp")
	}

	if call.SystemId < 1 {
		ok = false
		err = errors.New("no system")
	}

	if call.TalkgroupId < 1 {
		ok = false
		err = errors.New("no talkgroup")
	}

	return ok, err
}

func (call *Call) MarshalJSON() ([]byte, error) {
	audio := strings.ReplaceAll(fmt.Sprintf("%v", call.Audio), " ", ",")

	callMap := map[string]any{
		"id": call.Id,
		"audio": map[string]any{
			"data": json.RawMessage(audio),
			"type": "Buffer",
		},
		"audioName": call.AudioFilename,
		"audioType": call.AudioMime,
		"dateTime":  call.Timestamp.Format(time.RFC3339),
		"delayed":   call.Delayed,
		"patches":   call.Patches,
		"hasTones":  call.HasTones,
	}

	if call.ToneSequence != nil {
		callMap["toneSequence"] = call.ToneSequence
	}

	if call.Transcript != "" {
		transcript := call.Transcript
		callMap["transcriptConfidence"] = call.TranscriptConfidence
		callMap["transcriptionStatus"] = call.TranscriptionStatus
		if p := activeTranscriptParser.Load(); p != nil {
			corrected, annotations := p.AnnotateTranscript(call.Transcript)
			transcript = corrected
			if len(annotations) > 0 {
				callMap["transcriptAnnotations"] = annotations
			}
		}
		callMap["transcript"] = transcript
	}
	if call.AlertSummary != "" {
		callMap["alertSummary"] = call.AlertSummary
	}

	if len(call.Frequencies) > 0 {
		freqs := []map[string]any{}
		for _, f := range call.Frequencies {
			freq := map[string]any{
				"errorCount": f.Errors,
				"freq":       f.Frequency,
				"pos":        f.Offset,
				"spikeCount": f.Spikes,
			}

			if f.Dbm > 0 {
				freq["dbm"] = f.Dbm
			}

			freqs = append(freqs, freq)
		}

		callMap["frequencies"] = freqs
	}

	if call.SiteRef != "" {
		callMap["site"] = call.SiteRef
	}

	if call.System != nil {
		callMap["system"] = call.System.SystemRef
	} else if call.SystemId > 0 {
		callMap["system"] = call.SystemId
	}

	if call.Talkgroup != nil {
		callMap["talkgroup"] = call.Talkgroup.TalkgroupRef
	} else if call.TalkgroupId > 0 {
		callMap["talkgroup"] = call.TalkgroupId
	}

	// Populate Units from Meta.UnitRefs if Units is empty (for JSON marshaling only)
	unitsToUse := call.Units
	if len(unitsToUse) == 0 && len(call.Meta.UnitRefs) > 0 {
		// Create temporary units from Meta.UnitRefs for marshaling
		unitsToUse = make([]CallUnit, 0, len(call.Meta.UnitRefs))
		for _, unitRef := range call.Meta.UnitRefs {
			// Include all unitRefs, even 0, to match v6 behavior
			unitsToUse = append(unitsToUse, CallUnit{
				UnitRef: unitRef,
				Offset:  0,
			})
		}
	}

	if len(unitsToUse) > 0 {
		sources := []map[string]any{}
		for _, unit := range unitsToUse {
			entry := map[string]any{
				"pos": unit.Offset,
				"src": unit.UnitRef,
			}
			if unit.Label != "" {
				entry["tag"] = unit.Label
			}
			sources = append(sources, entry)
		}
		callMap["sources"] = sources
		// Also set source field as fallback for frontend compatibility (v6 style)
		// Always set source from first unit, even if 0, to match v6 behavior
		callMap["source"] = unitsToUse[0].UnitRef
	} else {
		// If no units at all, set source to 0 to match v6 behavior where source is always present
		callMap["source"] = 0
	}

	if call.Frequency > 0 {
		callMap["frequency"] = call.Frequency
	}

	return json.Marshal(callMap)
}

func (call *Call) ToJson() (string, error) {
	if b, err := json.Marshal(call); err == nil {
		return string(b), nil
	} else {
		return "", fmt.Errorf("call.tojson: %v", err)
	}
}

// MarshalJSONWithEncryption serialises the call like MarshalJSON but replaces
// the raw audio bytes with AES-256-GCM ciphertext (nonce prepended, base64-
// encoded) and sets audioType to AudioEncryptedType so clients know to decrypt.
// If encryption fails the call is marshalled without encryption as a fallback
// so playback is never silently broken.
func (call *Call) MarshalJSONWithEncryption(key []byte) ([]byte, error) {
	if len(key) != 32 {
		return json.Marshal(call)
	}

	encryptedAudio, err := EncryptAudio(key, call.Audio)
	if err != nil {
		// Fallback to unencrypted on error — callers log this upstream.
		return json.Marshal(call)
	}

	callMap := map[string]any{
		"id": call.Id,
		"audio": map[string]any{
			"data": encryptedAudio,
			"type": "EncryptedBuffer",
		},
		"audioName": call.AudioFilename,
		"audioType": AudioEncryptedType,
		"dateTime":  call.Timestamp.Format(time.RFC3339),
		"delayed":   call.Delayed,
		"patches":   call.Patches,
		"hasTones":  call.HasTones,
	}

	if call.ToneSequence != nil {
		callMap["toneSequence"] = call.ToneSequence
	}
	if call.Transcript != "" {
		transcript := call.Transcript
		callMap["transcriptConfidence"] = call.TranscriptConfidence
		callMap["transcriptionStatus"] = call.TranscriptionStatus
		if p := activeTranscriptParser.Load(); p != nil {
			corrected, annotations := p.AnnotateTranscript(call.Transcript)
			transcript = corrected
			if len(annotations) > 0 {
				callMap["transcriptAnnotations"] = annotations
			}
		}
		callMap["transcript"] = transcript
	}
	if call.AlertSummary != "" {
		callMap["alertSummary"] = call.AlertSummary
	}
	if len(call.Frequencies) > 0 {
		freqs := []map[string]any{}
		for _, f := range call.Frequencies {
			freq := map[string]any{
				"errorCount": f.Errors,
				"freq":       f.Frequency,
				"pos":        f.Offset,
				"spikeCount": f.Spikes,
			}
			if f.Dbm > 0 {
				freq["dbm"] = f.Dbm
			}
			freqs = append(freqs, freq)
		}
		callMap["frequencies"] = freqs
	}
	if call.SiteRef != "" {
		callMap["site"] = call.SiteRef
	}
	if call.System != nil {
		callMap["system"] = call.System.SystemRef
	} else if call.SystemId > 0 {
		callMap["system"] = call.SystemId
	}
	if call.Talkgroup != nil {
		callMap["talkgroup"] = call.Talkgroup.TalkgroupRef
	} else if call.TalkgroupId > 0 {
		callMap["talkgroup"] = call.TalkgroupId
	}

	unitsToUse := call.Units
	if len(unitsToUse) == 0 && len(call.Meta.UnitRefs) > 0 {
		unitsToUse = make([]CallUnit, 0, len(call.Meta.UnitRefs))
		for _, unitRef := range call.Meta.UnitRefs {
			unitsToUse = append(unitsToUse, CallUnit{UnitRef: unitRef})
		}
	}
	if len(unitsToUse) > 0 {
		sources := []map[string]any{}
		for _, unit := range unitsToUse {
			entry := map[string]any{"pos": unit.Offset, "src": unit.UnitRef}
			if unit.Label != "" {
				entry["tag"] = unit.Label
			}
			sources = append(sources, entry)
		}
		callMap["sources"] = sources
		callMap["source"] = unitsToUse[0].UnitRef
	} else {
		callMap["source"] = 0
	}
	if call.Frequency > 0 {
		callMap["frequency"] = call.Frequency
	}

	return json.Marshal(callMap)
}

type Calls struct {
	controller *Controller
}

func NewCalls(controller *Controller) *Calls {
	return &Calls{
		controller: controller,
	}
}

// timestampDurationRatioMin is the minimum ratio (shorter/longer) between the
// incoming call's duration and a candidate's stored duration for the timestamp
// fallback check. More lenient than the former energy ratio (0.85) because an
// identical P25 timestamp is already strong evidence of the same grant — the
// guard exists only to reject wildly different calls (e.g. 0.26s vs 2.6s) that
// share a wall-clock second due to SDR Trunk not embedding true P25 timestamps.
const timestampDurationRatioMin = 0.50

// audioFingerprintWindow is the ±time window used when searching the DB for
// duplicate candidates via audio fingerprinting (energy profiles and Chromaprint).
// ±120s covers the worst observed delayed-upload scenario: an uploader whose
// reported P25 timestamp is up to ~90s behind the actual wall clock. False
// positives are prevented by the energy similarity (≥80%) and duration ratio
// (≥85%) guards — a genuinely different call will not score above those
// thresholds regardless of how close in time it is.
const audioFingerprintWindow = 120 * time.Second

// defaultTimestampFallbackWindow is the default ±window for timestamp-based
// duplicate detection when the admin has not configured DuplicateTimestampWindow.
const defaultTimestampFallbackWindow = 800 * time.Millisecond

// CheckDuplicateByHash queries the DB for any call on the same system+talkgroup
// whose PCM content hash matches this call's hash. A hash match means the decoded
// audio samples are bit-identical — a guaranteed duplicate regardless of how far
// apart the timestamps are. No time window is needed or applied.
func (calls *Calls) CheckDuplicateByHash(call *Call, db *Database) (bool, error) {
	if call.AudioHash == "" || call.System == nil || call.Talkgroup == nil {
		return false, nil
	}

	formatError := errorFormatter("calls", "checkduplicatebyhash")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	query := fmt.Sprintf(
		`SELECT COUNT(*) FROM "calls" WHERE "audioHash" = $1 AND "systemId" = %d AND "talkgroupId" = %d`,
		call.System.Id, call.Talkgroup.Id,
	)

	var count int
	if err := db.Sql.QueryRowContext(ctx, query, call.AudioHash).Scan(&count); err != nil {
		return false, formatError(err, query)
	}
	return count > 0, nil
}

// CheckDuplicateByTimestamp is the last-resort fallback after audio fingerprinting
// has already cleared the call. It queries the DB for any call on the same
// system+talkgroup whose P25 timestamp is within ±timestampFallbackWindow.
// A duration ratio guard (same as the energy path) is applied: if the candidate's
// stored duration differs by more than 15% from this call's duration, it is skipped.
// This prevents false positives when two genuinely different calls land at the same
// wall-clock second (e.g. SDR Trunk uploaders that don't embed true P25 timestamps).
func (calls *Calls) CheckDuplicateByTimestamp(call *Call, db *Database, windowMs int64) (bool, error) {
	if call.System == nil || call.Talkgroup == nil {
		return false, nil
	}

	formatError := errorFormatter("calls", "checkduplicatebytimestamp")

	from := call.Timestamp.UnixMilli() - windowMs
	to := call.Timestamp.UnixMilli() + windowMs

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	query := fmt.Sprintf(
		`SELECT "audioDuration" FROM "calls" WHERE "timestamp" BETWEEN %d AND %d AND "systemId" = %d AND "talkgroupId" = %d`,
		from, to, call.System.Id, call.Talkgroup.Id,
	)

	rows, err := db.Sql.QueryContext(ctx, query)
	if err != nil {
		return false, formatError(err, query)
	}
	defer rows.Close()

	for rows.Next() {
		var candidateDuration float64
		if err := rows.Scan(&candidateDuration); err != nil {
			continue
		}
		// If either duration is unknown, trust the timestamp match.
		if call.Duration <= 0 || candidateDuration <= 0 {
			return true, nil
		}
		lo, hi := call.Duration, candidateDuration
		if hi < lo {
			lo, hi = hi, lo
		}
		if lo/hi >= timestampDurationRatioMin {
			return true, nil
		}
	}

	return false, nil
}

// receivedAtDuplicateWindow is the maximum gap between this call's server-arrival
// time and a prior call's server-arrival time on the same system+talkgroup for the
// receivedAt duplicate pass to fire. 1 second covers the common multi-recorder case
// where two recorders upload the same transmission within a fraction of a second of
// each other even when their P25 clocks are skewed.
const receivedAtDuplicateWindow = 1000 * time.Millisecond

// audioDurationsSimilarForReceivedAtDup returns true when two audio durations are
// close enough to be considered the same transmission. We use a generous tolerance
// because the same transmission captured by two different SDRs can differ by a
// fraction of a second in decode length.
func audioDurationsSimilarForReceivedAtDup(a, b float64) bool {
	if a <= 0 || b <= 0 {
		return false
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	// Absolute tolerance: within 0.4 s
	if diff <= 0.4 {
		return true
	}
	// Relative tolerance: within 15% of the longer duration
	longer := a
	if b > a {
		longer = b
	}
	return diff/longer <= 0.15
}

// CheckDuplicateByReceivedAt looks for a prior call on the same system+talkgroup
// whose server-arrival time (receivedAt) is within receivedAtDuplicateWindow of
// this call's arrival time AND whose audio duration is similar. This catches
// multi-recorder near-duplicates where the audio bytes differ (different noise
// floor or encoding) but the same transmission was received by the server within
// ~1 second from two different uploaders.
//
// Forwarded calls are excluded from this check — a call that arrived via
// downstream forwarding will always have a different receivedAt than the original,
// and we must not flag it as a duplicate of itself.
func (calls *Calls) CheckDuplicateByReceivedAt(call *Call, db *Database) (bool, error) {
	if call.System == nil || call.Talkgroup == nil {
		return false, nil
	}

	// We need the duration to guard against false positives.
	if _, err := calls.controller.getCallDuration(call); err != nil || call.Duration <= 0 {
		return false, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	windowStart := time.Now().Add(-receivedAtDuplicateWindow)

	// Look for the most recent call on this system+talkgroup that arrived within
	// the window. The duration guard prevents false positives from genuinely
	// different calls that happen to land in the same second.
	query := fmt.Sprintf(
		`SELECT "audioDuration" FROM "calls" WHERE "systemId" = %d AND "talkgroupId" = %d AND "receivedAt" >= $1 ORDER BY "receivedAt" DESC LIMIT 1`,
		call.System.Id, call.Talkgroup.Id,
	)

	var priorDur sql.NullFloat64
	_ = db.Sql.QueryRowContext(ctx, query, windowStart).Scan(&priorDur)

	if !priorDur.Valid {
		return false, nil
	}

	if !audioDurationsSimilarForReceivedAtDup(call.Duration, priorDur.Float64) {
		return false, nil
	}

	return true, nil
}

func (calls *Calls) GetCall(id uint64) (*Call, error) {
	var (
		err   error
		query string
		rows  *sql.Rows
		tx    *sql.Tx

		patch       string
		systemId    uint64
		talkgroupId uint64
		timestamp   int64
		frequency   sql.NullInt64
	)

	formatError := errorFormatter("calls", "getcall")

	// Check if this call is currently delayed
	if calls.controller.Delayer.IsCallDelayed(id) {
		return nil, formatError(fmt.Errorf("call %d is currently delayed and not available for playback", id), "")
	}

	// Add timeout context to prevent indefinite blocking
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if tx, err = calls.controller.Database.Sql.BeginTx(ctx, nil); err != nil {
		return nil, formatError(err, "")
	}

	call := Call{Id: id}

	if calls.controller.Database.Config.DbType == DbTypePostgresql {
		query = fmt.Sprintf(`SELECT c."audio", c."audioFilename", c."audioMime", c."siteRef", c."timestamp", STRING_AGG(CAST(COALESCE(cpt."talkgroupRef", 0) AS text), ','), sy."systemId", t."talkgroupId", c."frequency", c."toneSequence", c."hasTones", c."transcript", c."transcriptConfidence", c."transcriptionStatus", c."alertSummary" FROM "calls" AS c LEFT JOIN "callPatches" AS cp on cp."callId" = c."callId" LEFT JOIN "talkgroups" AS cpt ON cpt."talkgroupId" = cp."talkgroupId" LEFT JOIN "systems" AS sy ON sy."systemId" = c."systemId" LEFT JOIN "talkgroups" AS t ON t."talkgroupId" = c."talkgroupId" WHERE c."callId" = %d GROUP BY c."callId", c."audio", c."audioFilename", c."audioMime", c."siteRef", c."timestamp", sy."systemId", t."talkgroupId", c."frequency", c."toneSequence", c."hasTones", c."transcript", c."transcriptConfidence", c."transcriptionStatus", c."alertSummary"`, id)

	} else {
		query = fmt.Sprintf(`SELECT c."audio", c."audioFilename", c."audioMime", c."siteRef", c."timestamp", GROUP_CONCAT(COALESCE(cpt."talkgroupRef", 0)), sy."systemId", t."talkgroupId", c."frequency", c."toneSequence", c."hasTones", c."transcript", c."transcriptConfidence", c."transcriptionStatus", c."alertSummary" FROM "calls" AS c LEFT JOIN "callPatches" AS cp on cp."callId" = c."callId" LEFT JOIN "talkgroups" AS cpt ON cpt."talkgroupId" = cp."talkgroupId" LEFT JOIN "systems" AS sy ON sy."systemId" = c."systemId" LEFT JOIN "talkgroups" AS t ON t."talkgroupId" = c."talkgroupId" WHERE c."callId" = %d GROUP BY c."callId", c."audio", c."audioFilename", c."audioMime", c."siteRef", c."timestamp", sy."systemId", t."talkgroupId", c."frequency", c."toneSequence", c."hasTones", c."transcript", c."transcriptConfidence", c."transcriptionStatus", c."alertSummary"`, id)
	}

	var toneSequenceJson sql.NullString
	var transcript sql.NullString
	var transcriptConfidence sql.NullFloat64
	var transcriptionStatus sql.NullString
	var alertSummary sql.NullString

	if err = tx.QueryRow(query).Scan(&call.Audio, &call.AudioFilename, &call.AudioMime, &call.SiteRef, &timestamp, &patch, &systemId, &talkgroupId, &frequency, &toneSequenceJson, &call.HasTones, &transcript, &transcriptConfidence, &transcriptionStatus, &alertSummary); err != nil && err != sql.ErrNoRows {
		tx.Rollback()
		return nil, formatError(err, query)
	}

	call.Timestamp = time.UnixMilli(timestamp)

	if frequency.Valid && frequency.Int64 > 0 {
		call.Frequency = uint(frequency.Int64)
		call.Frequencies = []CallFrequency{
			{
				Frequency: call.Frequency,
			},
		}
	}

	// Parse tone sequence
	if toneSequenceJson.Valid && toneSequenceJson.String != "" && toneSequenceJson.String != "[]" {
		var toneSequence ToneSequence
		if err := json.Unmarshal([]byte(toneSequenceJson.String), &toneSequence); err == nil {
			call.ToneSequence = &toneSequence
			call.HasTones = len(toneSequence.Tones) > 0
		}
	}

	// Load transcript
	if transcript.Valid {
		call.Transcript = transcript.String
	}
	if transcriptConfidence.Valid {
		call.TranscriptConfidence = transcriptConfidence.Float64
	}
	if transcriptionStatus.Valid {
		call.TranscriptionStatus = transcriptionStatus.String
	}
	if alertSummary.Valid {
		call.AlertSummary = alertSummary.String
	}

	if len(patch) > 0 {
		for _, s := range strings.Split(patch, ",") {
			if i, err := strconv.Atoi(s); err == nil && i > 0 {
				call.Patches = append(call.Patches, uint(i))
			}
		}
	}

	if system, ok := calls.controller.Systems.GetSystemById(systemId); ok {
		call.System = system

	} else {
		tx.Rollback()
		return nil, formatError(fmt.Errorf("cannot retrieve system id %d for call id %d", systemId, call.Id), "")
	}

	if talkgroup, ok := call.System.Talkgroups.GetTalkgroupById(talkgroupId); ok {
		call.Talkgroup = talkgroup

	} else {
		tx.Rollback()
		return nil, formatError(fmt.Errorf("cannot retrieve talkgroup id %d for call id %d", talkgroupId, call.Id), "")
	}

	query = fmt.Sprintf(`SELECT "offset", "unitRef", COALESCE("label", '') FROM "callUnits" WHERE "callId" = %d ORDER BY "offset" ASC`, id)
	if rows, err = tx.Query(query); err != nil {
		tx.Rollback()
		return nil, formatError(err, query)
	}

	for rows.Next() {
		unit := CallUnit{}

		if err = rows.Scan(&unit.Offset, &unit.UnitRef, &unit.Label); err != nil {
			break
		}

		call.Units = append(call.Units, unit)
	}

	rows.Close()

	if err != nil {
		tx.Rollback()
		return nil, formatError(err, query)
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return nil, formatError(err, "")
	}

	return &call, nil
}

// GetCallsBulk fetches multiple calls in 3 queries instead of N×2 round-trips.
//
//	Query 1 — metadata + patches (no audio blob; avoids GROUP BY on blobs):
//	          callId, timestamp, patches, systemId, talkgroupId, frequency,
//	          toneSequence, hasTones, transcript, transcriptConfidence,
//	          transcriptionStatus, alertSummary
//	Query 2 — audio bytes + filenames: callId, audio, audioFilename, audioMime, siteRef
//	Query 3 — units:                   callId, offset, unitRef, label
//
// Any IDs currently in the delay queue are silently skipped.
func (calls *Calls) GetCallsBulk(ids []uint64) []*Call {
	if len(ids) == 0 {
		return nil
	}

	// Filter out calls currently being held in the delay queue.
	var active []uint64
	for _, id := range ids {
		if !calls.controller.Delayer.IsCallDelayed(id) {
			active = append(active, id)
		}
	}
	if len(active) == 0 {
		return nil
	}

	// Build the IN clause (safe: IDs are uint64, no user input).
	inParts := make([]string, len(active))
	for i, id := range active {
		inParts[i] = strconv.FormatUint(id, 10)
	}
	inClause := strings.Join(inParts, ",")

	// --- Query 1: metadata ---
	// Audio blobs are intentionally excluded from this query so they don't
	// participate in the GROUP BY (which would force a full blob comparison
	// for every row in the aggregation).
	var metaQuery string
	if calls.controller.Database.Config.DbType == DbTypePostgresql {
		metaQuery = `SELECT c."callId", c."timestamp", STRING_AGG(CAST(COALESCE(cpt."talkgroupRef", 0) AS text), ','), sy."systemId", t."talkgroupId", c."frequency", c."toneSequence", c."hasTones", c."transcript", c."transcriptConfidence", c."transcriptionStatus", c."alertSummary" FROM "calls" AS c LEFT JOIN "callPatches" AS cp ON cp."callId" = c."callId" LEFT JOIN "talkgroups" AS cpt ON cpt."talkgroupId" = cp."talkgroupId" LEFT JOIN "systems" AS sy ON sy."systemId" = c."systemId" LEFT JOIN "talkgroups" AS t ON t."talkgroupId" = c."talkgroupId" WHERE c."callId" IN (` + inClause + `) GROUP BY c."callId", c."timestamp", sy."systemId", t."talkgroupId", c."frequency", c."toneSequence", c."hasTones", c."transcript", c."transcriptConfidence", c."transcriptionStatus", c."alertSummary" ORDER BY c."timestamp" ASC`
	} else {
		metaQuery = `SELECT c."callId", c."timestamp", GROUP_CONCAT(COALESCE(cpt."talkgroupRef", 0)), sy."systemId", t."talkgroupId", c."frequency", c."toneSequence", c."hasTones", c."transcript", c."transcriptConfidence", c."transcriptionStatus", c."alertSummary" FROM "calls" AS c LEFT JOIN "callPatches" AS cp ON cp."callId" = c."callId" LEFT JOIN "talkgroups" AS cpt ON cpt."talkgroupId" = cp."talkgroupId" LEFT JOIN "systems" AS sy ON sy."systemId" = c."systemId" LEFT JOIN "talkgroups" AS t ON t."talkgroupId" = c."talkgroupId" WHERE c."callId" IN (` + inClause + `) GROUP BY c."callId" ORDER BY c."timestamp" ASC`
	}

	metaRows, err := calls.controller.Database.Sql.Query(metaQuery)
	if err != nil {
		return nil
	}
	defer metaRows.Close()

	byId := make(map[uint64]*Call, len(active))
	var ordered []*Call

	for metaRows.Next() {
		var id uint64
		var patch string
		var systemId, talkgroupId uint64
		var timestamp int64
		var frequency sql.NullInt64
		var toneSeqJson, transcript, transcriptionStatus, alertSummary sql.NullString
		var transcriptConfidence sql.NullFloat64
		var hasTones bool

		if err = metaRows.Scan(&id, &timestamp, &patch, &systemId, &talkgroupId, &frequency, &toneSeqJson, &hasTones, &transcript, &transcriptConfidence, &transcriptionStatus, &alertSummary); err != nil {
			continue
		}

		call := &Call{Id: id}
		call.Timestamp = time.UnixMilli(timestamp)

		if frequency.Valid && frequency.Int64 > 0 {
			call.Frequency = uint(frequency.Int64)
			call.Frequencies = []CallFrequency{{Frequency: call.Frequency}}
		}
		if toneSeqJson.Valid && toneSeqJson.String != "" && toneSeqJson.String != "[]" {
			var ts ToneSequence
			if json.Unmarshal([]byte(toneSeqJson.String), &ts) == nil {
				call.ToneSequence = &ts
				call.HasTones = len(ts.Tones) > 0
			}
		} else {
			call.HasTones = hasTones
		}
		if transcript.Valid {
			call.Transcript = transcript.String
		}
		if transcriptConfidence.Valid {
			call.TranscriptConfidence = transcriptConfidence.Float64
		}
		if transcriptionStatus.Valid {
			call.TranscriptionStatus = transcriptionStatus.String
		}
		if alertSummary.Valid {
			call.AlertSummary = alertSummary.String
		}
		if len(patch) > 0 {
			for _, s := range strings.Split(patch, ",") {
				if i, err2 := strconv.Atoi(s); err2 == nil && i > 0 {
					call.Patches = append(call.Patches, uint(i))
				}
			}
		}
		if system, ok := calls.controller.Systems.GetSystemById(systemId); ok {
			call.System = system
		} else {
			continue
		}
		if tg, ok := call.System.Talkgroups.GetTalkgroupById(talkgroupId); ok {
			call.Talkgroup = tg
		} else {
			continue
		}

		byId[id] = call
		ordered = append(ordered, call)
	}
	metaRows.Close()

	if len(byId) == 0 {
		return nil
	}

	// --- Query 2: audio blobs ---
	audioRows, err := calls.controller.Database.Sql.Query(
		`SELECT "callId", "audio", "audioFilename", "audioMime", "siteRef" FROM "calls" WHERE "callId" IN (` + inClause + `)`)
	if err == nil {
		defer audioRows.Close()
		for audioRows.Next() {
			var cid uint64
			var audio []byte
			var filename, mime, siteRef string
			if audioRows.Scan(&cid, &audio, &filename, &mime, &siteRef) == nil {
				if c, ok := byId[cid]; ok {
					c.Audio = audio
					c.AudioFilename = filename
					c.AudioMime = mime
					c.SiteRef = siteRef
				}
			}
		}
	}

	// --- Query 3: units ---
	unitRows, err := calls.controller.Database.Sql.Query(
		`SELECT "callId", "offset", "unitRef", COALESCE("label", '') FROM "callUnits" WHERE "callId" IN (` + inClause + `) ORDER BY "callId", "offset" ASC`)
	if err == nil {
		defer unitRows.Close()
		for unitRows.Next() {
			var cid uint64
			unit := CallUnit{}
			if unitRows.Scan(&cid, &unit.Offset, &unit.UnitRef, &unit.Label) == nil {
				if c, ok := byId[cid]; ok {
					c.Units = append(c.Units, unit)
				}
			}
		}
	}

	return ordered
}

func (calls *Calls) Prune(db *Database, pruneDays uint) error {
	timestamp := time.Now().Add(-24 * time.Hour * time.Duration(pruneDays)).UnixMilli()
	query := fmt.Sprintf(`DELETE FROM "calls" WHERE "timestamp" < %d`, timestamp)

	if _, err := db.Sql.Exec(query); err != nil {
		return fmt.Errorf("%s in %s", err, query)
	}

	return nil
}

func (calls *Calls) PurgeAll(db *Database) error {
	query := `DELETE FROM "calls"`

	if _, err := db.Sql.Exec(query); err != nil {
		return fmt.Errorf("%s in %s", err, query)
	}

	return nil
}

func (calls *Calls) DeleteByIDs(db *Database, ids []uint64) error {
	if len(ids) == 0 {
		return nil
	}

	var placeholders []string
	var args []interface{}
	for i, id := range ids {
		if db.Config.DbType == DbTypePostgresql {
			placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
		} else {
			placeholders = append(placeholders, "?")
		}
		args = append(args, id)
	}

	query := fmt.Sprintf(`DELETE FROM "calls" WHERE "callId" IN (%s)`, strings.Join(placeholders, ", "))

	if _, err := db.Sql.Exec(query, args...); err != nil {
		return fmt.Errorf("%s in %s", err, query)
	}

	return nil
}

func (calls *Calls) Search(searchOptions *CallsSearchOptions, client *Client) (*CallsSearchResults, error) {
	const (
		ascOrder  = "ASC"
		descOrder = "DESC"
	)

	var (
		err  error
		rows *sql.Rows

		limit  uint
		offset uint
		order  string
		query  string

		timestamp int64
	)

	db := client.Controller.Database

	formatError := errorFormatter("calls", "search")

	searchResults := &CallsSearchResults{
		Options: searchOptions,
		Results: []CallsSearchResult{},
	}

	calls.controller.Logs.LogEvent(LogLevelInfo, "Client access evaluation complete")

	// Determine sort order early so we can use it for date filtering
	switch v := searchOptions.Sort.(type) {
	case int:
		if v < 0 {
			order = descOrder
		} else {
			order = ascOrder
		}
	default:
		order = ascOrder
	}

	// Build WHERE conditions using slice-based approach (like v6)
	where := []string{
		`c."systemId" > 0`,
		`c."talkgroupId" > 0`,
		`c."systemRef" > 0`,
		`c."talkgroupRef" > 0`,
		`d."callId" IS NULL`,
	}

	// System/Talkgroup filters (added first for optimal index usage)
	switch v := searchOptions.System.(type) {
	case uint:
		conditions := []string{
			fmt.Sprintf(`c."systemRef" = %d`, v),
		}
		switch v := searchOptions.Talkgroup.(type) {
		case uint:
			conditions = append(conditions, fmt.Sprintf(`c."talkgroupRef" = %d`, v))
		}
		if len(conditions) > 0 {
			where = append(where, fmt.Sprintf("(%s)", strings.Join(conditions, " AND ")))
		}
	}

	switch v := searchOptions.Group.(type) {
	case string:
		groupConditions := []string{}
		for id, m := range client.GroupsMap[v] {
			in := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", m), " ", ", "), "[", "("), "]", ")")
			groupConditions = append(groupConditions, fmt.Sprintf(`(c."systemRef" = %d AND c."talkgroupRef" IN %s)`, id, in))
		}
		if len(groupConditions) > 0 {
			where = append(where, fmt.Sprintf("(%s)", strings.Join(groupConditions, " OR ")))
		}
	}

	switch v := searchOptions.Tag.(type) {
	case string:
		tagConditions := []string{}
		for id, m := range client.TagsMap[v] {
			in := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", m), " ", ", "), "[", "("), "]", ")")
			tagConditions = append(tagConditions, fmt.Sprintf(`(c."systemRef" = %d AND c."talkgroupRef" IN %s)`, id, in))
		}
		if len(tagConditions) > 0 {
			where = append(where, fmt.Sprintf("(%s)", strings.Join(tagConditions, " OR ")))
		}
	}

	// Calculate the effective delay for this specific client
	var effectiveDelay uint = 0

	if calls.controller.requiresUserAuth() && client.User != nil {
		for _, delay := range client.User.talkgroupDelaysMap {
			if delay > 0 && (effectiveDelay == 0 || delay < effectiveDelay) {
				effectiveDelay = delay
			}
		}

		if effectiveDelay == 0 {
			for _, delay := range client.User.systemDelaysMap {
				if delay > 0 && (effectiveDelay == 0 || delay < effectiveDelay) {
					effectiveDelay = delay
				}
			}
		}

		if effectiveDelay == 0 && client.User.Delay > 0 {
			effectiveDelay = uint(client.User.Delay)
		}

		if effectiveDelay == 0 {
			effectiveDelay = calls.controller.Options.DefaultSystemDelay
		}
	} else {
		effectiveDelay = calls.controller.Options.DefaultSystemDelay
	}

	// Apply the delay filtering
	if effectiveDelay > 0 {
		now := time.Now()
		delayDuration := time.Duration(effectiveDelay) * time.Minute
		cutoffTime := now.Add(-delayDuration)
		cutoffTimeMs := cutoffTime.UnixMilli()

		// Add delay condition: only include calls older than the delay period
		where = append(where, fmt.Sprintf(`c."timestamp" <= %d`, cutoffTimeMs))
	}

	// Date filter - use simple comparisons instead of BETWEEN (like v6/Python)
	switch v := searchOptions.Date.(type) {
	case time.Time:
		selectedDateMs := v.UnixMilli()
		// When a date is selected, always show calls from that date forward (>=)
		// The sort order (ASC/DESC) controls whether oldest or newest are shown first
		where = append(where, fmt.Sprintf(`c."timestamp" >= %d`, selectedDateMs))
	default:
		// No date selected - for large databases, limit scan range when sorting DESC (newest first)
		// This prevents full table scans on 50M+ record databases
		// For DESC order (newest first), default to last 24 hours to optimize index usage
		// For ASC order (oldest first), no default filter - let user see oldest calls
		if order == descOrder {
			now := time.Now()
			// Default to 24 hours back for newest-first searches without a date
			defaultLookback := now.Add(-24 * time.Hour)
			defaultLookbackMs := defaultLookback.UnixMilli()
			where = append(where, fmt.Sprintf(`c."timestamp" >= %d`, defaultLookbackMs))
			calls.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("Search: No date selected, applying default 24-hour lookback for DESC order (from %s)", defaultLookback.Format("2006-01-02 15:04:05")))
		}
	}

	// Build final WHERE clause
	whereClause := "1=1"
	for _, condition := range where {
		whereClause += " AND " + condition
	}

	// Use the same WHERE clause for delay filtering
	delayWhere := "WHERE " + whereClause

	// Skip expensive MIN/MAX queries for DateStart/DateStop
	// Set defaults - these are informational only and not critical for functionality
	searchResults.DateStart = time.Time{}
	searchResults.DateStop = time.Time{}

	switch v := searchOptions.Limit.(type) {
	case uint:
		limit = uint(math.Min(float64(500), float64(v)))
	default:
		limit = 200
	}

	switch v := searchOptions.Offset.(type) {
	case uint:
		offset = v
	}

	// Skip COUNT(*) query to avoid querying entire database
	// We'll use hasMore flag based on whether we got exactly 'limit' results
	searchResults.Count = 0
	searchResults.HasMore = false

	// Use subquery approach for PostgreSQL
	// Query for limit+1 to determine if there are more results
	queryLimit := limit + 1
	query = fmt.Sprintf(`SELECT c."callId", c."timestamp", c."systemRef", c."talkgroupRef", c."frequency", c."siteRef", (SELECT cu."unitRef" FROM "callUnits" cu WHERE cu."callId" = c."callId" ORDER BY cu."offset" LIMIT 1) as "source" FROM "calls" AS c LEFT JOIN "delayed" AS d ON d."callId" = c."callId" %s ORDER BY c."timestamp" %s LIMIT %d OFFSET %d`, delayWhere, order, queryLimit, offset)

	calls.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("Search RESULTS query: %s", query))

	// Add timeout context to prevent indefinite blocking
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if rows, err = db.Sql.QueryContext(ctx, query); err != nil && err != sql.ErrNoRows {
		return nil, formatError(err, query)
	}

	var totalCalls, includedCalls int

	for rows.Next() {
		searchResult := CallsSearchResult{}
		var frequency sql.NullInt64
		var siteRef sql.NullInt64
		var source sql.NullInt64
		if err = rows.Scan(&searchResult.Id, &timestamp, &searchResult.System, &searchResult.Talkgroup, &frequency, &siteRef, &source); err != nil {
			break
		}

		// Convert timestamp - validate to prevent JSON marshaling errors
		// JSON only supports years 0-9999, so skip calls with invalid timestamps
		searchResult.Timestamp = time.UnixMilli(timestamp)
		if searchResult.Timestamp.Year() < 1 || searchResult.Timestamp.Year() > 9999 {
			// Skip this call - invalid timestamp that will cause JSON marshaling to fail
			calls.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("Skipping call %d with invalid timestamp: %v (year %d out of range)", searchResult.Id, searchResult.Timestamp, searchResult.Timestamp.Year()))
			continue
		}

		if frequency.Valid && frequency.Int64 > 0 {
			searchResult.Frequency = uint(frequency.Int64)
		}
		if siteRef.Valid && siteRef.Int64 > 0 {
			searchResult.Site = uint(siteRef.Int64)
		}
		if source.Valid && source.Int64 > 0 {
			searchResult.Source = uint(source.Int64)
		}
		totalCalls++

		// Only include up to 'limit' results (drop the extra one we fetched)
		if includedCalls < int(limit) {
			searchResults.Results = append(searchResults.Results, searchResult)
			includedCalls++
		}
	}

	rows.Close()

	if err != nil {
		return nil, formatError(err, "")
	}

	// Set count to actual number of results returned (should be limit)
	searchResults.Count = uint(len(searchResults.Results))

	// If we fetched more than 'limit' rows, there are more results available
	searchResults.HasMore = totalCalls > int(limit)

	return searchResults, err
}

func (calls *Calls) WriteCall(call *Call, db *Database) (uint64, error) {
	var (
		err   error
		query string
		res   sql.Result
		tx    *sql.Tx
	)

	formatError := errorFormatter("calls", "writecall")

	// Add timeout context to prevent indefinite blocking
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if tx, err = db.Sql.BeginTx(ctx, nil); err != nil {
		return 0, formatError(err, "")
	}

	frequencyValue := call.Frequency
	if frequencyValue == 0 && len(call.Frequencies) > 0 {
		if call.Frequencies[0].Frequency > 0 {
			frequencyValue = call.Frequencies[0].Frequency
		}
	}

	if frequencyValue > 0 && call.Frequency == 0 {
		call.Frequency = frequencyValue
	}
	if frequencyValue > 0 && len(call.Frequencies) == 0 {
		call.Frequencies = []CallFrequency{
			{
				Frequency: frequencyValue,
			},
		}
	}

	// Serialize tone sequence
	toneSequenceJson := ""
	if call.ToneSequence != nil {
		if json, err := SerializeToneSequence(call.ToneSequence); err == nil {
			toneSequenceJson = json
		}
	}
	if toneSequenceJson == "" {
		toneSequenceJson = "{}"
	}

	// Default transcription status
	if call.TranscriptionStatus == "" {
		call.TranscriptionStatus = "pending"
	}

	// Determine site by frequency if not already set
	if call.SiteRef == "" && call.System != nil && call.System.Sites != nil && frequencyValue > 0 {
		if site, ok := call.System.Sites.GetSiteByFrequency(frequencyValue); ok {
			call.SiteRef = site.SiteRef
		}
	}

	// Convert SiteRef string to integer for database
	siteRefInt := 0
	if call.SiteRef != "" {
		if val, err := strconv.Atoi(call.SiteRef); err == nil {
			siteRefInt = val
		}
	}

	if db.Config.DbType == DbTypePostgresql {
		query = fmt.Sprintf(`INSERT INTO "calls" ("audio", "audioFilename", "audioMime", "siteRef", "systemId", "talkgroupId", "systemRef", "talkgroupRef", "timestamp", "frequency", "toneSequence", "hasTones", "transcript", "transcriptConfidence", "transcriptionStatus", "transmissionId", "requestId", "signalJobId", "receivedAt", "audioDuration", "isDuplicate", "audioHash") VALUES ($1, $2, $3, %d, %d, %d, %d, %d, %d, %d, $4, %t, $5, %.2f, $6, $7, $8, $9, NOW(), %.4f, %t, $10) RETURNING "callId"`, siteRefInt, call.System.Id, call.Talkgroup.Id, call.System.SystemRef, call.Talkgroup.TalkgroupRef, call.Timestamp.UnixMilli(), frequencyValue, call.HasTones, call.TranscriptConfidence, call.Duration, call.IsDuplicate)

		err = tx.QueryRow(query, call.Audio, call.AudioFilename, call.AudioMime, toneSequenceJson, call.Transcript, call.TranscriptionStatus, call.TransmissionId, call.RequestId, call.SignalJobId, call.AudioHash).Scan(&call.Id)

	} else {
		query = fmt.Sprintf(`INSERT INTO "calls" ("audio", "audioFilename", "audioMime", "siteRef", "systemId", "talkgroupId", "systemRef", "talkgroupRef", "timestamp", "frequency", "toneSequence", "hasTones", "transcript", "transcriptConfidence", "transcriptionStatus", "transmissionId", "requestId", "signalJobId", "receivedAt", "audioDuration", "isDuplicate", "audioHash") VALUES (?, ?, ?, %d, %d, %d, %d, %d, %d, %d, ?, %t, ?, %.2f, ?, ?, ?, ?, CURRENT_TIMESTAMP, %.4f, %t, ?)`, siteRefInt, call.System.Id, call.Talkgroup.Id, call.System.SystemRef, call.Talkgroup.TalkgroupRef, call.Timestamp.UnixMilli(), frequencyValue, call.HasTones, call.TranscriptConfidence, call.Duration, call.IsDuplicate)

		if res, err = tx.Exec(query, call.Audio, call.AudioFilename, call.AudioMime, toneSequenceJson, call.Transcript, call.TranscriptionStatus, call.TransmissionId, call.RequestId, call.SignalJobId, call.AudioHash); err == nil {
			if id, err := res.LastInsertId(); err == nil {
				call.Id = uint64(id)
			}
		}
	}

	if err != nil {
		tx.Rollback()
		return 0, formatError(err, query)
	}

	for _, ref := range call.Patches {
		var talkgroupId sql.NullInt64
		query = fmt.Sprintf(`SELECT "talkgroupId" FROM "talkgroups" WHERE "systemId" = %d and "talkgroupRef" = %d`, call.System.Id, ref)
		if err = tx.QueryRow(query).Scan(&talkgroupId); err != nil && err != sql.ErrNoRows {
			tx.Rollback()
			return 0, formatError(err, query)
		}
		if !talkgroupId.Valid {
			continue
		}
		query = fmt.Sprintf(`INSERT INTO "callPatches" ("callId", "talkgroupId") VALUES (%d, %d)`, call.Id, talkgroupId.Int64)
		if _, err = tx.Exec(query); err != nil {
			tx.Rollback()
			return 0, formatError(err, query)
		}
	}

	for _, unit := range call.Units {
		// Skip invalid unitRef values from Trunk Recorder (e.g., -1 which wraps to 18446744073709551615)
		// Trunk Recorder sends -1 when radio ID is unknown or not determined
		// PostgreSQL bigint max is 9223372036854775807, so wrapped values exceed this
		if unit.UnitRef > 9223372036854775807 {
			continue
		}
		query = fmt.Sprintf(`INSERT INTO "callUnits" ("callId", "offset", "unitRef", "label") VALUES (%d, %f, %d, $1)`, call.Id, unit.Offset, unit.UnitRef)
		if _, err = tx.Exec(query, unit.Label); err != nil {
			tx.Rollback()
			return 0, formatError(err, query)
		}
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return 0, formatError(err, "")
	}

	return uint64(call.Id), nil
}

type CallsSearchOptions struct {
	Date      any `json:"date,omitempty"`
	Group     any `json:"group,omitempty"`
	Limit     any `json:"limit,omitempty"`
	Offset    any `json:"offset,omitempty"`
	Sort      any `json:"sort,omitempty"`
	System    any `json:"system,omitempty"`
	Tag       any `json:"tag,omitempty"`
	Talkgroup any `json:"talkgroup,omitempty"`
}

func NewCallSearchOptions() *CallsSearchOptions {
	return &CallsSearchOptions{}
}

func (searchOptions *CallsSearchOptions) fromMap(m map[string]any) *CallsSearchOptions {
	switch v := m["date"].(type) {
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			searchOptions.Date = t
		}
	}

	switch v := m["group"].(type) {
	case string:
		searchOptions.Group = v
	}

	switch v := m["limit"].(type) {
	case float64:
		searchOptions.Limit = uint(v)
	}

	switch v := m["offset"].(type) {
	case float64:
		searchOptions.Offset = uint(v)
	}

	switch v := m["sort"].(type) {
	case float64:
		searchOptions.Sort = int(v)
	}

	switch v := m["system"].(type) {
	case float64:
		searchOptions.System = uint(v)
	}

	switch v := m["tag"].(type) {
	case string:
		searchOptions.Tag = v
	}

	switch v := m["talkgroup"].(type) {
	case float64:
		searchOptions.Talkgroup = uint(v)
	}

	return searchOptions
}

type CallsSearchResult struct {
	Id        uint64    `json:"id"`
	System    uint      `json:"system"`
	Talkgroup uint      `json:"talkgroup"`
	Timestamp time.Time `json:"dateTime"`
	Frequency uint      `json:"frequency,omitempty"`
	Source    uint      `json:"source,omitempty"`
	Site      uint      `json:"site,omitempty"`
}

type CallsSearchResults struct {
	Count     uint                `json:"count"`
	HasMore   bool                `json:"hasMore"`
	DateStart time.Time           `json:"dateStart"`
	DateStop  time.Time           `json:"dateStop"`
	Options   *CallsSearchOptions `json:"options"`
	Results   []CallsSearchResult `json:"results"`
}
