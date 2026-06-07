// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type ToneHistoryAnalyzeRequest struct {
	SystemId    uint64 `json:"systemId"`
	TalkgroupId uint64 `json:"talkgroupId"`
	Limit       int    `json:"limit"`
	Hours       int    `json:"hours"`
}

type ToneHistorySuggestion struct {
	PatternType string   `json:"patternType"`
	PatternDesc string   `json:"patternDesc"`
	CallCount   int      `json:"callCount"`
	CallIds     []uint64 `json:"callIds"`
	Label       string   `json:"label"`
	ToneSet     ToneSet  `json:"toneSet"`
}

type ToneHistoryPartialPattern struct {
	PatternDesc string `json:"patternDesc"`
	CallCount   int    `json:"callCount"`
}

type ToneHistoryAnalyzeResponse struct {
	CallsScanned            int                         `json:"callsScanned"`
	CallsWithTones          int                         `json:"callsWithTones"`
	CallsWithCandidates     int                         `json:"callsWithCandidates"`
	DiscoverErrors          int                         `json:"discoverErrors"`
	PatternsBelowThreshold  int                         `json:"patternsBelowThreshold"`
	PartialPatterns         []ToneHistoryPartialPattern `json:"partialPatterns,omitempty"`
	CallsRequired           int                         `json:"callsRequired"`
	Suggestions             []ToneHistorySuggestion     `json:"suggestions"`
	Message                 string                      `json:"message,omitempty"`
}

type toneHistoryAgg struct {
	cand    toneLearnCandidate
	records []toneLearnCallRecord
}

func toneLearnPatternDescription(cand toneLearnCandidate) string {
	switch cand.PatternType {
	case toneLearnPatternABPair:
		return fmt.Sprintf("Two-tone pair: A=%.1f Hz (%.2fs), B=%.1f Hz (%.2fs)",
			cand.AFrequency, cand.ADuration, cand.BFrequency, cand.BDuration)
	case toneLearnPatternLong:
		return fmt.Sprintf("Long tone: %.1f Hz for %.2fs", cand.LongFrequency, cand.LongDuration)
	default:
		return string(cand.PatternType)
	}
}

func toneHistoryAudioMime(audioMime, audioFilename string) string {
	mime := strings.TrimSpace(audioMime)
	if mime != "" {
		return mime
	}
	lower := strings.ToLower(audioFilename)
	switch {
	case strings.HasSuffix(lower, ".m4a"), strings.HasSuffix(lower, ".mp4"):
		return "audio/mp4"
	case strings.HasSuffix(lower, ".wav"):
		return "audio/wav"
	case strings.HasSuffix(lower, ".mp3"):
		return "audio/mpeg"
	default:
		return "audio/mp4"
	}
}

func (controller *Controller) analyzeTalkgroupToneHistory(systemId, talkgroupId uint64, limit, hours int) (*ToneHistoryAnalyzeResponse, error) {
	if controller == nil || controller.Database == nil {
		return nil, fmt.Errorf("server not ready")
	}
	if systemId == 0 || talkgroupId == 0 {
		return nil, fmt.Errorf("systemId and talkgroupId are required")
	}

	sys, ok := controller.Systems.GetSystemById(systemId)
	if !ok {
		return nil, fmt.Errorf("system %d not found", systemId)
	}
	talkgroup, ok := sys.Talkgroups.GetTalkgroupById(talkgroupId)
	if !ok {
		return nil, fmt.Errorf("talkgroup %d not found on system %d", talkgroupId, systemId)
	}

	if limit <= 0 {
		limit = 200
	}
	if limit > 500 {
		limit = 500
	}
	if hours <= 0 {
		hours = 168
	}
	if hours > 720 {
		hours = 720
	}

	cfg := controller.Options.AutoLearnToneSetConfig
	cfg.normalize()

	since := time.Now().Add(-time.Duration(hours) * time.Hour).UnixMilli()

	var query string
	if controller.Database.Config.DbType == DbTypePostgresql {
		query = `SELECT "callId", "audio", "audioMime", "audioFilename", "transcript", "reviewedTranscript", "timestamp" FROM "calls" WHERE "systemId" = $1 AND "talkgroupId" = $2 AND "timestamp" >= $3 AND length("audio") > 0 ORDER BY "timestamp" DESC LIMIT $4`
	} else {
		query = `SELECT "callId", "audio", "audioMime", "audioFilename", "transcript", "reviewedTranscript", "timestamp" FROM "calls" WHERE "systemId" = ? AND "talkgroupId" = ? AND "timestamp" >= ? AND length("audio") > 0 ORDER BY "timestamp" DESC LIMIT ?`
	}

	rows, err := controller.Database.Sql.Query(query, systemId, talkgroupId, since, limit)
	if err != nil {
		return nil, fmt.Errorf("query calls: %w", err)
	}
	defer rows.Close()

	resp := &ToneHistoryAnalyzeResponse{
		CallsRequired: cfg.CallsRequired,
		Suggestions:   []ToneHistorySuggestion{},
	}

	aggregates := make(map[string]*toneHistoryAgg)
	detector := NewToneDetector()
	var firstDiscoverErr string

	for rows.Next() {
		var (
			callId             uint64
			audio              []byte
			audioMime          string
			audioFilename      string
			transcript         sql.NullString
			reviewedTranscript sql.NullString
			timestamp          int64
		)
		if err := rows.Scan(&callId, &audio, &audioMime, &audioFilename, &transcript, &reviewedTranscript, &timestamp); err != nil {
			return nil, fmt.Errorf("scan call: %w", err)
		}
		resp.CallsScanned++

		mime := toneHistoryAudioMime(audioMime, audioFilename)
		tones, err := detector.Discover(audio, mime)
		if err != nil {
			resp.DiscoverErrors++
			if firstDiscoverErr == "" {
				firstDiscoverErr = fmt.Sprintf("call %d: %v", callId, err)
			}
			continue
		}
		if len(tones) == 0 {
			continue
		}
		resp.CallsWithTones++

		candidates := extractToneLearnCandidates(tones, cfg, systemId, talkgroupId)
		if len(candidates) == 0 {
			continue
		}
		resp.CallsWithCandidates++

		stackedCall := len(candidates) > 1

		transcriptText := ""
		if reviewedTranscript.Valid && strings.TrimSpace(reviewedTranscript.String) != "" {
			transcriptText = strings.ToUpper(strings.TrimSpace(reviewedTranscript.String))
		} else if transcript.Valid {
			transcriptText = strings.ToUpper(strings.TrimSpace(transcript.String))
		}

		for _, cand := range candidates {
			if toneSetExistsOnTalkgroup(talkgroup.ToneSets, cand, cfg.FrequencyToleranceHz) {
				continue
			}
			agg, exists := aggregates[cand.SignatureHash]
			if !exists {
				aggregates[cand.SignatureHash] = &toneHistoryAgg{cand: cand}
				agg = aggregates[cand.SignatureHash]
			}
			dup := false
			for _, r := range agg.records {
				if r.CallId == callId {
					dup = true
					break
				}
			}
			if dup {
				continue
			}
			agg.records = append(agg.records, toneLearnCallRecord{
				CallId:      callId,
				Transcript:  transcriptText,
				Timestamp:   timestamp,
				StackedCall: stackedCall,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate calls: %w", err)
	}

	for _, agg := range aggregates {
		if len(agg.records) < cfg.CallsRequired {
			resp.PatternsBelowThreshold++
			if len(resp.PartialPatterns) < 10 {
				resp.PartialPatterns = append(resp.PartialPatterns, ToneHistoryPartialPattern{
					PatternDesc: toneLearnPatternDescription(agg.cand),
					CallCount:   len(agg.records),
				})
			}
			continue
		}
		if toneLearnCandidateNeedsReview(agg.records) {
			continue
		}
		if toneSetExistsOnTalkgroup(talkgroup.ToneSets, agg.cand, cfg.FrequencyToleranceHz) {
			continue
		}

		label := controller.suggestToneLearnLabel(sys, talkgroup, agg.cand, agg.records)
		if label == "" || label == "UNKNOWN" {
			label = fmt.Sprintf("Learned %s", strings.ToUpper(string(agg.cand.PatternType)))
		}

		toneSet := agg.cand.ToneSetDraft
		toneSet.Label = label

		callIds := make([]uint64, len(agg.records))
		for i, r := range agg.records {
			callIds[i] = r.CallId
		}

		resp.Suggestions = append(resp.Suggestions, ToneHistorySuggestion{
			PatternType: string(agg.cand.PatternType),
			PatternDesc: toneLearnPatternDescription(agg.cand),
			CallCount:   len(agg.records),
			CallIds:     callIds,
			Label:       label,
			ToneSet:     toneSet,
		})
	}

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
		"tone history analyze: system=%d talkgroup=%d (TGID %d) scanned=%d withTones=%d withCandidates=%d discoverErrors=%d belowThreshold=%d suggestions=%d aDur=%.1f-%.1f bDur=%.1f-%.1f callsRequired=%d%s",
		systemId, talkgroupId, talkgroup.TalkgroupRef,
		resp.CallsScanned, resp.CallsWithTones, resp.CallsWithCandidates, resp.DiscoverErrors,
		resp.PatternsBelowThreshold, len(resp.Suggestions),
		cfg.AToneMinDuration, cfg.AToneMaxDuration, cfg.BToneMinDuration, cfg.BToneMaxDuration, cfg.CallsRequired,
		func() string {
			if firstDiscoverErr != "" {
				return " firstDiscoverErr=" + firstDiscoverErr
			}
			return ""
		}(),
	))

	if len(resp.Suggestions) == 0 {
		switch {
		case resp.CallsScanned == 0:
			resp.Message = fmt.Sprintf("No calls with audio found in the last %d hours for this talkgroup.", hours)
		case resp.CallsWithTones == 0:
			resp.Message = fmt.Sprintf(
				"Scanned %d calls but FFT found no sustained tones (discover errors: %d). Stored audio may be voice-only or ffmpeg could not decode calls.",
				resp.CallsScanned, resp.DiscoverErrors,
			)
		case resp.CallsWithCandidates == 0:
			resp.Message = fmt.Sprintf(
				"Scanned %d calls, %d had raw tones, but none matched auto-learn duration windows (A %.1f–%.1fs, B %.1f–%.1fs, long ≥%.1fs). Check Admin → Options → auto-learn tone durations.",
				resp.CallsScanned, resp.CallsWithTones, cfg.AToneMinDuration, cfg.AToneMaxDuration, cfg.BToneMinDuration, cfg.BToneMaxDuration, cfg.LongToneMinDuration,
			)
		case resp.PatternsBelowThreshold > 0:
			resp.Message = fmt.Sprintf(
				"Found %d pattern(s) but none reached %d calls yet (scanned %d calls, %d with matching A+B/long patterns). See partial patterns below.",
				resp.PatternsBelowThreshold, cfg.CallsRequired, resp.CallsScanned, resp.CallsWithCandidates,
			)
		default:
			resp.Message = fmt.Sprintf(
				"No new tone patterns with at least %d matching calls in the last %d hours (scanned %d calls, %d with tones).",
				cfg.CallsRequired, hours, resp.CallsScanned, resp.CallsWithTones,
			)
		}
	}

	return resp, nil
}

func (admin *Admin) ToneHistoryAnalyzeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	token := admin.GetAuthorization(r)
	if !admin.ValidateToken(token) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var req ToneHistoryAnalyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	result, err := admin.Controller.analyzeTalkgroupToneHistory(req.SystemId, req.TalkgroupId, req.Limit, req.Hours)
	if err != nil {
		admin.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("tone history analyze failed: %s", err.Error()))
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
