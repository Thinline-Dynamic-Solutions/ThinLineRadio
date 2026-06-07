// Copyright (C) 2025 Thinline Dynamic Solutions
//
// Auto-learn tone sets: observe paging patterns and auto-add confident matches.

package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AutoLearnToneSetConfig holds duration windows and thresholds for tone auto-learn.
type AutoLearnToneSetConfig struct {
	AToneMinDuration     float64 `json:"aToneMinDuration"`
	AToneMaxDuration     float64 `json:"aToneMaxDuration"`
	BToneMinDuration     float64 `json:"bToneMinDuration"`
	BToneMaxDuration     float64 `json:"bToneMaxDuration"`
	LongToneMinDuration  float64 `json:"longToneMinDuration"`
	LongToneMaxDuration  float64 `json:"longToneMaxDuration"`
	CallsRequired        int     `json:"callsRequired"`
	FrequencyToleranceHz float64 `json:"frequencyToleranceHz"`
}

func DefaultAutoLearnToneSetConfig() AutoLearnToneSetConfig {
	return AutoLearnToneSetConfig{
		AToneMinDuration:     0.5,
		AToneMaxDuration:     1.2,
		BToneMinDuration:     1.5,
		BToneMaxDuration:     4.0,
		LongToneMinDuration:  6.0,
		LongToneMaxDuration:  0,
		CallsRequired:        3,
		FrequencyToleranceHz: 10,
	}
}

func (c *AutoLearnToneSetConfig) normalize() {
	def := DefaultAutoLearnToneSetConfig()
	if c.AToneMinDuration <= 0 {
		c.AToneMinDuration = def.AToneMinDuration
	}
	if c.AToneMaxDuration <= 0 {
		c.AToneMaxDuration = def.AToneMaxDuration
	}
	if c.BToneMinDuration <= 0 {
		c.BToneMinDuration = def.BToneMinDuration
	}
	if c.BToneMaxDuration <= 0 {
		c.BToneMaxDuration = def.BToneMaxDuration
	}
	if c.LongToneMinDuration <= 0 {
		c.LongToneMinDuration = def.LongToneMinDuration
	}
	if c.CallsRequired < 2 {
		c.CallsRequired = def.CallsRequired
	}
	if c.FrequencyToleranceHz <= 0 {
		c.FrequencyToleranceHz = def.FrequencyToleranceHz
	}
}

type toneLearnPatternType string

const (
	toneLearnPatternABPair toneLearnPatternType = "ab_pair"
	toneLearnPatternLong   toneLearnPatternType = "long"
)

type toneLearnCandidate struct {
	SignatureHash string
	PatternType   toneLearnPatternType
	ToneSetDraft  ToneSet
	AFrequency    float64
	BFrequency    float64
	LongFrequency float64
	ADuration     float64
	BDuration     float64
	LongDuration  float64
}

type toneLearnCallRecord struct {
	CallId      uint64 `json:"callId"`
	Transcript  string `json:"transcript"`
	Timestamp   int64  `json:"timestamp"`
	StackedCall bool   `json:"stackedCall"` // true when multiple tone patterns shared this voice call
}

func roundToneFrequency(freq float64) float64 {
	return math.Round(freq)
}

func buildABPairSignature(systemId, talkgroupId uint64, aHz, bHz, tol float64) string {
	aBin := int(math.Round(aHz / tol))
	bBin := int(math.Round(bHz / tol))
	raw := fmt.Sprintf("%d:%d:ab:%d:%d", systemId, talkgroupId, aBin, bBin)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func buildLongToneSignature(systemId, talkgroupId uint64, freq, tol float64) string {
	bin := int(math.Round(freq / tol))
	raw := fmt.Sprintf("%d:%d:long:%d", systemId, talkgroupId, bin)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func toneSetExistsOnTalkgroup(existing []ToneSet, cand toneLearnCandidate, tol float64) bool {
	for _, ts := range existing {
		if cand.PatternType == toneLearnPatternABPair && ts.ATone != nil && ts.BTone != nil {
			if freqWithinTol(cand.ToneSetDraft.ATone.Frequency, ts.ATone.Frequency, tol) &&
				freqWithinTol(cand.ToneSetDraft.BTone.Frequency, ts.BTone.Frequency, tol) {
				return true
			}
		}
		if cand.PatternType == toneLearnPatternLong && ts.LongTone != nil && ts.ATone == nil && ts.BTone == nil {
			if freqWithinTol(cand.ToneSetDraft.LongTone.Frequency, ts.LongTone.Frequency, tol) {
				return true
			}
		}
	}
	return false
}

func freqWithinTol(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func extractToneLearnCandidates(tones []Tone, cfg AutoLearnToneSetConfig, systemId, talkgroupId uint64) []toneLearnCandidate {
	sort.Slice(tones, func(i, j int) bool {
		return tones[i].StartTime < tones[j].StartTime
	})

	var out []toneLearnCandidate
	used := make(map[int]bool)

	// One A+B pair per call: earliest valid A, then the longest valid B that follows it.
	for i, a := range tones {
		if a.Duration < cfg.AToneMinDuration || a.Duration > cfg.AToneMaxDuration {
			continue
		}
		bestJ := -1
		var bestBDur float64
		for j, b := range tones {
			if j <= i {
				continue
			}
			if b.StartTime < a.EndTime-0.1 {
				continue
			}
			if b.Duration < cfg.BToneMinDuration || b.Duration > cfg.BToneMaxDuration {
				continue
			}
			if b.Duration > bestBDur {
				bestBDur = b.Duration
				bestJ = j
			}
		}
		if bestJ < 0 {
			continue
		}
		b := tones[bestJ]
		used[i] = true
		used[bestJ] = true
		aFreq := roundToneFrequency(a.Frequency)
		bFreq := roundToneFrequency(b.Frequency)
		draft := ToneSet{
			Id:          uuid.New().String(),
			Tolerance:   cfg.FrequencyToleranceHz,
			MinDuration: cfg.AToneMinDuration,
			ATone: &ToneSpec{
				Frequency:   aFreq,
				MinDuration: cfg.AToneMinDuration,
				MaxDuration: cfg.AToneMaxDuration,
			},
			BTone: &ToneSpec{
				Frequency:   bFreq,
				MinDuration: cfg.BToneMinDuration,
				MaxDuration: cfg.BToneMaxDuration,
			},
		}
		out = append(out, toneLearnCandidate{
			SignatureHash: buildABPairSignature(systemId, talkgroupId, aFreq, bFreq, cfg.FrequencyToleranceHz),
			PatternType:   toneLearnPatternABPair,
			ToneSetDraft:  draft,
			AFrequency:    a.Frequency,
			BFrequency:    b.Frequency,
			ADuration:     a.Duration,
			BDuration:     b.Duration,
		})
		break
	}

	for i := range tones {
		if used[i] {
			continue
		}
		t := tones[i]
		if t.Duration < cfg.LongToneMinDuration {
			continue
		}
		if cfg.LongToneMaxDuration > 0 && t.Duration > cfg.LongToneMaxDuration {
			continue
		}
		freq := roundToneFrequency(t.Frequency)
		draft := ToneSet{
			Id:        uuid.New().String(),
			Tolerance: cfg.FrequencyToleranceHz,
			LongTone: &ToneSpec{
				Frequency:   freq,
				MinDuration: cfg.LongToneMinDuration,
				MaxDuration: cfg.LongToneMaxDuration,
			},
		}
		out = append(out, toneLearnCandidate{
			SignatureHash: buildLongToneSignature(systemId, talkgroupId, freq, cfg.FrequencyToleranceHz),
			PatternType:   toneLearnPatternLong,
			ToneSetDraft:  draft,
			LongFrequency: t.Frequency,
			LongDuration:  t.Duration,
		})
	}

	return out
}

func toneAutoLearnEnabled(call *Call) bool {
	return call != nil && call.System != nil && call.Talkgroup != nil &&
		call.Talkgroup.AutoLearnToneSets &&
		call.System.AlertsEnabled && call.Talkgroup.AlertsEnabled
}

// processToneAutoLearnAsync runs tone auto-learn after WriteCall (raw ingest audio).
func (controller *Controller) processToneAutoLearnAsync(learnCall *Call, originalCall *Call, transcript string) {
	if learnCall.Id == 0 && originalCall != nil && originalCall.Id > 0 {
		learnCall.Id = originalCall.Id
	}
	if learnCall.Id == 0 && originalCall != nil {
		for i := 0; i < 100 && originalCall.Id == 0; i++ {
			time.Sleep(2 * time.Millisecond)
		}
		if originalCall.Id > 0 {
			learnCall.Id = originalCall.Id
		}
	}
	if learnCall.Id == 0 {
		talkgroupRef := uint(0)
		if learnCall.Talkgroup != nil {
			talkgroupRef = learnCall.Talkgroup.TalkgroupRef
		}
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("tone auto-learn skipped: call id not assigned (talkgroup=%d)", talkgroupRef))
		return
	}
	controller.processToneAutoLearn(learnCall, transcript)
}

// processToneAutoLearn observes paging tones via FFT Discover on call audio.
// Runs on ingest (tone pages) and after transcription (adds voice transcript to records).
func (controller *Controller) processToneAutoLearn(call *Call, transcript string) {
	if controller == nil || call == nil || call.System == nil || call.Talkgroup == nil {
		return
	}

	if !toneAutoLearnEnabled(call) {
		return
	}
	transcript = strings.TrimSpace(transcript)
	if transcript != "" && !controller.isVoiceForToneAlerts(transcript) {
		return
	}

	cfg := controller.Options.AutoLearnToneSetConfig
	cfg.normalize()

	audio := call.Audio
	mime := call.AudioMime
	if len(call.OriginalAudio) > 0 && call.OriginalAudioMime != "" {
		audio = call.OriginalAudio
		mime = call.OriginalAudioMime
	}
	if len(audio) == 0 {
		return
	}

	detector := NewToneDetector()
	tones, err := detector.Discover(audio, mime)
	if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("tone auto-learn: discover failed for call %d: %v", call.Id, err))
		return
	}
	if len(tones) == 0 {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
			"tone auto-learn: no tones discovered in call %d on talkgroup %d (%d bytes audio)",
			call.Id, call.Talkgroup.TalkgroupRef, len(audio),
		))
		return
	}

	candidates := extractToneLearnCandidates(tones, cfg, call.System.Id, call.Talkgroup.Id)
	stackedCall := len(candidates) > 1
	for _, cand := range candidates {
		if toneSetExistsOnTalkgroup(call.Talkgroup.ToneSets, cand, cfg.FrequencyToleranceHz) {
			continue
		}
		controller.upsertToneLearnCandidate(call, transcript, cand, cfg, stackedCall)
	}
}

func (controller *Controller) upsertToneLearnCandidate(call *Call, transcript string, cand toneLearnCandidate, cfg AutoLearnToneSetConfig, stackedCall bool) {
	now := time.Now().UnixMilli()
	draftJson, _ := json.Marshal(cand.ToneSetDraft)

	var candidateId uint64
	var callRecordsJson string
	var reviewEmailedAt sql.NullInt64

	selectQuery := `SELECT "candidateId", "callRecords", "reviewEmailedAt" FROM "toneSetLearnCandidates" WHERE "systemId" = $1 AND "talkgroupId" = $2 AND "signatureHash" = $3`
	if controller.Database.Config.DbType != DbTypePostgresql {
		selectQuery = `SELECT "candidateId", "callRecords", "reviewEmailedAt" FROM "toneSetLearnCandidates" WHERE "systemId" = ? AND "talkgroupId" = ? AND "signatureHash" = ?`
	}

	err := controller.Database.Sql.QueryRow(selectQuery, call.System.Id, call.Talkgroup.Id, cand.SignatureHash).
		Scan(&candidateId, &callRecordsJson, &reviewEmailedAt)

	records := []toneLearnCallRecord{}
	if err == nil && callRecordsJson != "" {
		_ = json.Unmarshal([]byte(callRecordsJson), &records)
	}

	// Already finalized (emailed for review or auto-added)
	if reviewEmailedAt.Valid && reviewEmailedAt.Int64 > 0 {
		return
	}

	updatedExisting := false
	for i, r := range records {
		if r.CallId != call.Id {
			continue
		}
		if strings.TrimSpace(transcript) != "" {
			records[i].Transcript = strings.ToUpper(strings.TrimSpace(transcript))
		}
		updatedExisting = true
		break
	}
	if updatedExisting {
		recordsJson, _ := json.Marshal(records)
		updateQuery := `UPDATE "toneSetLearnCandidates" SET "callRecords" = $1, "lastSeenAt" = $2 WHERE "candidateId" = $3`
		if controller.Database.Config.DbType != DbTypePostgresql {
			updateQuery = `UPDATE "toneSetLearnCandidates" SET "callRecords" = ?, "lastSeenAt" = ? WHERE "candidateId" = ?`
		}
		_, _ = controller.Database.Sql.Exec(updateQuery, string(recordsJson), now, candidateId)
		return
	}

	records = append(records, toneLearnCallRecord{
		CallId:      call.Id,
		Transcript:  strings.ToUpper(strings.TrimSpace(transcript)),
		Timestamp:   call.Timestamp.UnixMilli(),
		StackedCall: stackedCall,
	})
	recordsJson, _ := json.Marshal(records)

	if err == sql.ErrNoRows {
		insertQuery := `INSERT INTO "toneSetLearnCandidates" ("systemId", "talkgroupId", "signatureHash", "patternType", "toneSetDraft", "callRecords", "firstSeenAt", "lastSeenAt") VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
		if controller.Database.Config.DbType != DbTypePostgresql {
			insertQuery = `INSERT INTO "toneSetLearnCandidates" ("systemId", "talkgroupId", "signatureHash", "patternType", "toneSetDraft", "callRecords", "firstSeenAt", "lastSeenAt") VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
		}
		if _, err := controller.Database.Sql.Exec(insertQuery, call.System.Id, call.Talkgroup.Id, cand.SignatureHash, string(cand.PatternType), string(draftJson), string(recordsJson), now, now); err != nil {
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("tone auto-learn: insert candidate failed: %v", err))
		}
	} else if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("tone auto-learn: load candidate failed: %v", err))
		return
	} else {
		updateQuery := `UPDATE "toneSetLearnCandidates" SET "callRecords" = $1, "toneSetDraft" = $2, "lastSeenAt" = $3 WHERE "candidateId" = $4`
		if controller.Database.Config.DbType != DbTypePostgresql {
			updateQuery = `UPDATE "toneSetLearnCandidates" SET "callRecords" = ?, "toneSetDraft" = ?, "lastSeenAt" = ? WHERE "candidateId" = ?`
		}
		if _, err := controller.Database.Sql.Exec(updateQuery, string(recordsJson), string(draftJson), now, candidateId); err != nil {
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("tone auto-learn: update candidate failed: %v", err))
			return
		}
	}

	if len(records) < cfg.CallsRequired {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("tone auto-learn: candidate %s on talkgroup %d at %d/%d calls", cand.SignatureHash[:8], call.Talkgroup.TalkgroupRef, len(records), cfg.CallsRequired))
		return
	}

	if toneLearnCandidateNeedsReview(records) {
		controller.skipToneLearnCandidate(call.System, call.Talkgroup, cand,
			"stacked tones on same voice call (add tone sets manually in admin if needed)")
		return
	}

	go controller.autoAddLearnedToneSet(call.System, call.Talkgroup, cand, records, cfg)
}

// skipToneLearnCandidate marks a candidate finalized without auto-adding or emailing.
func (controller *Controller) skipToneLearnCandidate(system *System, talkgroup *Talkgroup, cand toneLearnCandidate, reason string) {
	if controller == nil || system == nil || talkgroup == nil {
		return
	}

	now := time.Now().UnixMilli()
	updateQuery := `UPDATE "toneSetLearnCandidates" SET "reviewEmailedAt" = $1 WHERE "systemId" = $2 AND "talkgroupId" = $3 AND "signatureHash" = $4 AND ("reviewEmailedAt" IS NULL OR "reviewEmailedAt" = 0)`
	if controller.Database.Config.DbType != DbTypePostgresql {
		updateQuery = `UPDATE "toneSetLearnCandidates" SET "reviewEmailedAt" = ? WHERE "systemId" = ? AND "talkgroupId" = ? AND "signatureHash" = ? AND ("reviewEmailedAt" IS NULL OR "reviewEmailedAt" = 0)`
	}
	res, err := controller.Database.Sql.Exec(updateQuery, now, system.Id, talkgroup.Id, cand.SignatureHash)
	if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("tone auto-learn: mark skipped failed: %v", err))
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return
	}

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
		"tone auto-learn: skipped signature %s on talkgroup %d — %s",
		cand.SignatureHash[:8], talkgroup.TalkgroupRef, reason,
	))
}

func toneLearnCandidateNeedsReview(records []toneLearnCallRecord) bool {
	for _, r := range records {
		if r.StackedCall {
			return true
		}
	}
	return false
}

func (controller *Controller) autoAddLearnedToneSet(system *System, talkgroup *Talkgroup, cand toneLearnCandidate, records []toneLearnCallRecord, cfg AutoLearnToneSetConfig) {
	if controller == nil || system == nil || talkgroup == nil {
		return
	}

	now := time.Now().UnixMilli()
	updateQuery := `UPDATE "toneSetLearnCandidates" SET "reviewEmailedAt" = $1 WHERE "systemId" = $2 AND "talkgroupId" = $3 AND "signatureHash" = $4 AND ("reviewEmailedAt" IS NULL OR "reviewEmailedAt" = 0)`
	if controller.Database.Config.DbType != DbTypePostgresql {
		updateQuery = `UPDATE "toneSetLearnCandidates" SET "reviewEmailedAt" = ? WHERE "systemId" = ? AND "talkgroupId" = ? AND "signatureHash" = ? AND ("reviewEmailedAt" IS NULL OR "reviewEmailedAt" = 0)`
	}
	res, err := controller.Database.Sql.Exec(updateQuery, now, system.Id, talkgroup.Id, cand.SignatureHash)
	if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("tone auto-learn: mark auto-added failed: %v", err))
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return
	}

	label := controller.suggestToneLearnLabel(system, talkgroup, cand, records)
	if label == "" || label == "UNKNOWN" {
		label = fmt.Sprintf("Learned %s", strings.ToUpper(string(cand.PatternType)))
	}

	toneSet := cand.ToneSetDraft
	toneSet.Label = label

	sys, ok := controller.Systems.GetSystemById(system.Id)
	if !ok {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("tone auto-learn: system %d not in cache for auto-add", system.Id))
		return
	}
	tgMem, ok := sys.Talkgroups.GetTalkgroupById(talkgroup.Id)
	if !ok {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("tone auto-learn: talkgroup %d not in cache for auto-add", talkgroup.Id))
		return
	}
	if toneSetExistsOnTalkgroup(tgMem.ToneSets, cand, cfg.FrequencyToleranceHz) {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("tone auto-learn: tone set already exists on talkgroup %d, skipping auto-add", tgMem.TalkgroupRef))
		return
	}
	tgMem.ToneSets = append(tgMem.ToneSets, toneSet)
	if err := controller.persistTalkgroupToneSets(tgMem.Id, tgMem.ToneSets); err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("tone auto-learn: persist tone set failed for talkgroup %d: %v", tgMem.TalkgroupRef, err))
		tgMem.ToneSets = tgMem.ToneSets[:len(tgMem.ToneSets)-1]
		return
	}

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
		"tone auto-learn: auto-added tone set %q on talkgroup %d (signature %s, %d calls)",
		label, talkgroup.TalkgroupRef, cand.SignatureHash[:8], len(records),
	))
}

func (controller *Controller) persistTalkgroupToneSets(talkgroupId uint64, toneSets []ToneSet) error {
	toneSetsJson := "[]"
	if len(toneSets) > 0 {
		json, err := SerializeToneSets(toneSets)
		if err != nil {
			return err
		}
		toneSetsJson = json
	}

	var query string
	if controller.Database.Config.DbType == DbTypePostgresql {
		query = `UPDATE "talkgroups" SET "toneSets" = $1 WHERE "talkgroupId" = $2`
		_, err := controller.Database.Sql.Exec(query, toneSetsJson, talkgroupId)
		return err
	}
	query = `UPDATE "talkgroups" SET "toneSets" = ? WHERE "talkgroupId" = ?`
	_, err := controller.Database.Sql.Exec(query, toneSetsJson, talkgroupId)
	return err
}
