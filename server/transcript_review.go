// Copyright (C) 2025 Thinline Dynamic Solutions
//
// Admin transcript review for Whisper training — list, edit, approve, export.

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rdio-scanner/server/mapping"
)

const defaultTranscriptCollectorURL = "https://transcripts.thinlineds.com"
const collectorAPIKeyPrefix = "tlr_tc_"
const transcriptCollectorTrainingGoalHours = 5000

func validCollectorAPIKey(key string) bool {
	key = strings.TrimSpace(key)
	return strings.HasPrefix(key, collectorAPIKeyPrefix) && len(key) >= len(collectorAPIKeyPrefix)+8
}

func (admin *Admin) TranscriptReviewHandler(w http.ResponseWriter, r *http.Request) {
	if !admin.requireAdminToken(w, r) {
		return
	}

	switch r.Method {
	case http.MethodGet:
		admin.listTranscriptReviewQueue(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) TranscriptReviewCallHandler(w http.ResponseWriter, r *http.Request) {
	if !admin.requireAdminToken(w, r) {
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	callId, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid call id"})
		return
	}

	// /api/admin/transcript-review/{id}/audio
	if len(parts) >= 5 && parts[4] == "audio" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		admin.serveTranscriptReviewAudio(w, callId)
		return
	}

	// /api/admin/transcript-review/{id}/approve
	if len(parts) >= 5 && parts[4] == "approve" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		admin.approveTranscriptReview(w, r, callId)
		return
	}

	switch r.Method {
	case http.MethodPut:
		admin.saveTranscriptReview(w, r, callId)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) requireAdminToken(w http.ResponseWriter, r *http.Request) bool {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return false
	}
	return true
}

func (admin *Admin) listTranscriptReviewQueue(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 200 {
			limit = v
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	db := admin.Controller.Database
	// Same base filters as GET /api/transcripts (Alerts tab).
	where := []string{
		`(c."transcript" IS NOT NULL AND c."transcript" <> '')`,
		`d."callId" IS NULL`,
	}
	if search := strings.TrimSpace(r.URL.Query().Get("search")); search != "" {
		if admin.Controller.Database.Config.DbType == DbTypePostgresql {
			where = append(where, fmt.Sprintf(`c."transcript" ILIKE '%%%s%%'`, escapeQuotes(search)))
		} else {
			where = append(where, fmt.Sprintf(`c."transcript" LIKE '%%%s%%'`, escapeQuotes(search)))
		}
	}
	whereClause := strings.Join(where, " AND ")

	collectorConfigured := admin.collectorConfigured()
	// Prefer query without review columns — works before migration runs.
	out, qerr := admin.queryTranscriptReviewQueue(ctx, db, whereClause, limit, offset, false)
	if qerr != nil {
		log.Printf("transcript review list: base query failed, trying extended: %v", qerr)
		out, qerr = admin.queryTranscriptReviewQueue(ctx, db, whereClause, limit, offset, true)
	}
	if qerr != nil {
		log.Printf("transcript review list: query failed: %v", qerr)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("query failed: %v", qerr)})
		return
	}

	if err := ctx.Err(); err != nil {
		log.Printf("transcript review list: timeout after %d items: %v", len(out), err)
		w.WriteHeader(http.StatusGatewayTimeout)
		json.NewEncoder(w).Encode(map[string]string{"error": "transcript list timed out"})
		return
	}

	if len(out) == 0 {
		log.Printf("transcript review list: empty queue (offset=%d limit=%d)", offset, limit)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items":               out,
		"offset":              offset,
		"limit":               limit,
		"collectorConfigured": collectorConfigured,
	})
}

func (admin *Admin) queryTranscriptReviewQueue(ctx context.Context, db *Database, whereClause string, limit, offset int, withReviewCols bool) ([]map[string]any, error) {
	selectCols := `c."callId", c."timestamp", c."transcript", COALESCE(c."transcriptionStatus", ''),
			COALESCE(s."label", ''), COALESCE(t."label", ''), COALESCE(t."name", '')`
	if withReviewCols {
		selectCols = `c."callId", c."timestamp", c."transcript", COALESCE(c."reviewedTranscript", ''), COALESCE(c."trainingReviewStatus", ''), COALESCE(c."transcriptionStatus", ''),
			COALESCE(s."label", ''), COALESCE(t."label", ''), COALESCE(t."name", '')`
	}

	query := fmt.Sprintf(`SELECT %s
		FROM "calls" c
		LEFT JOIN "delayed" AS d ON d."callId" = c."callId"
		LEFT JOIN "systems" s ON s."systemId" = c."systemId"
		LEFT JOIN "talkgroups" t ON t."talkgroupId" = c."talkgroupId"
		WHERE %s
		ORDER BY c."callId" DESC
		LIMIT %d OFFSET %d`, selectCols, whereClause, limit, offset)

	rows, err := db.Sql.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]map[string]any, 0, limit)
	for rows.Next() {
		var (
			callId              uint64
			ts                  sql.NullInt64
			transcript          string
			reviewed            string
			status              string
			transcriptionStatus string
			systemLabel         string
			talkgroupLabel      string
			talkgroupName       string
		)

		var scanErr error
		if withReviewCols {
			scanErr = rows.Scan(&callId, &ts, &transcript, &reviewed, &status, &transcriptionStatus, &systemLabel, &talkgroupLabel, &talkgroupName)
		} else {
			scanErr = rows.Scan(&callId, &ts, &transcript, &transcriptionStatus, &systemLabel, &talkgroupLabel, &talkgroupName)
		}
		if scanErr != nil {
			log.Printf("transcript review list: scan row failed: %v", scanErr)
			continue
		}
		if !ts.Valid {
			continue
		}

		text := reviewed
		if strings.TrimSpace(text) == "" {
			text = transcript
		}
		tg := talkgroupLabel
		if talkgroupName != "" {
			if tg != "" {
				tg += " — "
			}
			tg += talkgroupName
		}
		out = append(out, map[string]any{
			"callId":               callId,
			"timestamp":            ts.Int64,
			"transcript":           transcript,
			"reviewedTranscript":   text,
			"trainingReviewStatus": status,
			"transcriptionStatus":  transcriptionStatus,
			"systemLabel":          systemLabel,
			"talkgroupLabel":       tg,
			"talkgroupName":        talkgroupName,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, ctx.Err()
}

func (admin *Admin) saveTranscriptReview(w http.ResponseWriter, r *http.Request, callId uint64) {
	var body struct {
		ReviewedTranscript string `json:"reviewedTranscript"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.ReviewedTranscript) == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "reviewedTranscript required"})
		return
	}

	call, err := admin.Controller.Calls.GetCall(callId)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "call not found"})
		return
	}
	if call.TrainingReviewStatus == "submitted" {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "this call was already submitted for training"})
		return
	}

	esc := escapeQuotes(body.ReviewedTranscript)
	query := fmt.Sprintf(`UPDATE "calls" SET "reviewedTranscript" = '%s', "trainingReviewStatus" = 'pending' WHERE "callId" = %d`, esc, callId)
	if _, err := admin.Controller.Database.Sql.Exec(query); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "save failed"})
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"message": "saved"})
}

func (admin *Admin) approveTranscriptReview(w http.ResponseWriter, r *http.Request, callId uint64) {
	var body struct {
		ReviewedTranscript string `json:"reviewedTranscript"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	call, err := admin.Controller.Calls.GetCall(callId)
	if err != nil || len(call.Audio) == 0 {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "call not found or has no audio"})
		return
	}

	reviewed := strings.TrimSpace(body.ReviewedTranscript)
	if reviewed == "" {
		reviewed = strings.TrimSpace(call.ReviewedTranscript)
	}
	if reviewed == "" {
		reviewed = strings.TrimSpace(call.Transcript)
	}
	if reviewed == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "reviewed transcript is empty"})
		return
	}
	reviewedUpper := mapping.NormalizeTranscriptPlainText(reviewed)
	if reviewedUpper == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "reviewed transcript is empty"})
		return
	}

	if call.TrainingReviewStatus == "submitted" {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "this call was already submitted for training"})
		return
	}

	durationSeconds := 0.0
	durationKnown := false
	if d, err := admin.Controller.getCallDuration(call); err != nil {
		log.Printf("transcript review: duration check failed for call %d: %v", callId, err)
	} else if d > 0 {
		durationSeconds = d
		durationKnown = true
	}
	if issues := trainingSubmitIssues(reviewed, durationSeconds, durationKnown); len(issues) > 0 {
		log.Printf("transcript review: blocked call %d: %s", callId, strings.Join(issues, " | "))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]any{
			"error":  strings.Join(issues, " "),
			"issues": issues,
		})
		return
	}

	admin.refreshCollectorFromDB()
	collectorURL, collectorKey := admin.collectorSettings()
	if collectorURL == "" {
		collectorURL = defaultTranscriptCollectorURL
	}
	if collectorKey == "" || !validCollectorAPIKey(collectorKey) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "transcript collector API key not configured"})
		return
	}

	systemLabel := ""
	talkgroupLabel := ""
	if call.System != nil {
		systemLabel = call.System.Label
	}
	if call.Talkgroup != nil {
		talkgroupLabel = call.Talkgroup.Label
		if call.Talkgroup.Name != "" {
			if talkgroupLabel != "" {
				talkgroupLabel += " — "
			}
			talkgroupLabel += call.Talkgroup.Name
		}
	}

	reviewer := "tlr-admin"
	meta := map[string]any{
		"tlrCallId":          fmt.Sprintf("%d", callId),
		"systemLabel":        systemLabel,
		"talkgroupLabel":     talkgroupLabel,
		"originalTranscript": call.Transcript,
		"reviewedTranscript": reviewedUpper,
		"reviewer":           reviewer,
		"reviewedAt":         time.Now().UTC().Format(time.RFC3339),
	}
	metaJSON, _ := json.Marshal(meta)

	if err := submitToTranscriptCollector(collectorURL, collectorKey, call.Audio, call.AudioFilename, call.AudioMime, metaJSON); err != nil {
		log.Printf("transcript review: collector submit failed for call %d: %v", callId, err)
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	escTranscript := escapeQuotes(reviewedUpper)
	escReviewed := escapeQuotes(reviewedUpper)
	query := fmt.Sprintf(`UPDATE "calls" SET "transcript" = '%s', "reviewedTranscript" = '%s', "trainingReviewStatus" = 'submitted' WHERE "callId" = %d`, escTranscript, escReviewed, callId)
	if _, err := admin.Controller.Database.Sql.Exec(query); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "submitted but failed to update local status"})
		return
	}

	if updated, err := admin.Controller.Calls.GetCall(callId); err == nil && updated != nil {
		updated.Transcript = reviewedUpper
		updated.ReviewedTranscript = reviewedUpper
		updated.TrainingReviewStatus = "submitted"
	}

	json.NewEncoder(w).Encode(map[string]string{"message": "approved and sent to transcript collector"})
}

func (admin *Admin) serveTranscriptReviewAudio(w http.ResponseWriter, callId uint64) {
	call, err := admin.Controller.Calls.GetCall(callId)
	if err != nil || len(call.Audio) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	mimeType := call.AudioMime
	if mimeType == "" {
		mimeType = "audio/wav"
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", strconv.Itoa(len(call.Audio)))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Write(call.Audio)
}

func (admin *Admin) TranscriptReviewRequestCollectorKeyHandler(w http.ResponseWriter, r *http.Request) {
	if !admin.requireAdminToken(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	serverURL, serverName := admin.deriveCollectorServerIdentity(r)

	// API keys are always issued by the central collector — not the local TLR host.
	collectorURL := defaultTranscriptCollectorURL

	apiKey, err := registerWithTranscriptCollector(collectorURL, serverName, serverURL)
	if err != nil {
		log.Printf("transcript review: collector register failed (collector=%s, server=%s): %v", collectorURL, serverURL, err)
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	admin.Controller.Options.mutex.Lock()
	cfg := admin.Controller.Options.TranscriptionConfig
	cfg.CollectorURL = collectorURL
	cfg.CollectorAPIKey = apiKey
	saved := cfg
	admin.Controller.Options.mutex.Unlock()

	if err := admin.Controller.Options.WriteKey(admin.Controller.Database, "transcriptionConfig", saved, func() {
		admin.Controller.Options.TranscriptionConfig = saved
	}); err != nil {
		log.Printf("transcript review: save collector key after register: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "received API key but failed to save locally"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"message":      "connected to transcript collector",
		"configured":   true,
		"collectorURL": collectorURL,
		"serverUrl":    serverURL,
		"serverName":   serverName,
	})
}

func (admin *Admin) deriveCollectorServerIdentity(r *http.Request) (serverURL, serverName string) {
	scheme, host := getSchemeAndHost(r)
	host = strings.TrimSpace(host)

	admin.Controller.Options.mutex.Lock()
	baseURL := strings.TrimSpace(admin.Controller.Options.BaseUrl)
	if n := strings.TrimSpace(admin.Controller.Options.CentralManagementServerName); n != "" {
		serverName = n
	} else if b := strings.TrimSpace(admin.Controller.Options.Branding); b != "" {
		serverName = b
	}
	admin.Controller.Options.mutex.Unlock()

	if baseURL != "" {
		serverURL = strings.TrimSuffix(baseURL, "/")
	} else if host != "" {
		serverURL = fmt.Sprintf("%s://%s", scheme, strings.TrimSuffix(host, "/"))
	}

	if serverName == "" && admin.Controller.Systems != nil {
		for _, sys := range admin.Controller.Systems.List {
			if sys == nil {
				continue
			}
			if label := strings.TrimSpace(sys.Label); label != "" {
				serverName = label
				break
			}
		}
	}
	if serverName == "" && host != "" {
		serverName = host
	}
	if serverName == "" {
		serverName = "ThinLine Radio"
	}
	return serverURL, serverName
}

func registerWithTranscriptCollector(baseURL, serverName, serverURL string) (string, error) {
	baseURL = strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return "", fmt.Errorf("collector URL is empty")
	}
	payload, err := json.Marshal(map[string]string{
		"serverName": serverName,
		"serverUrl":  serverURL,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/register", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("collector request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("collector returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var result struct {
		APIKey string `json:"apiKey"`
		Key    string `json:"key"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("invalid collector response: %w", err)
	}
	key := strings.TrimSpace(result.APIKey)
	if key == "" {
		key = strings.TrimSpace(result.Key)
	}
	if key == "" {
		return "", fmt.Errorf("collector returned no API key")
	}
	if !validCollectorAPIKey(key) {
		return "", fmt.Errorf("collector returned invalid API key format")
	}
	return key, nil
}

func (admin *Admin) TranscriptReviewCollectorHandler(w http.ResponseWriter, r *http.Request) {
	if !admin.requireAdminToken(w, r) {
		return
	}

	switch r.Method {
	case http.MethodGet:
		admin.refreshCollectorFromDB()
		_, key := admin.collectorSettings()
		hasKey := validCollectorAPIKey(key)
		connected := hasKey
		serverURL, serverName := admin.deriveCollectorServerIdentity(r)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"hasApiKey":           hasKey,
			"configured":          hasKey,
			"connected":           connected,
			"collectorURL":        defaultTranscriptCollectorURL,
			"serverName":          serverName,
			"serverUrl":           serverURL,
			"defaultCollectorURL": defaultTranscriptCollectorURL,
		})
	case http.MethodDelete:
		admin.clearCollectorAPIKey()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "collector API key cleared"})
	case http.MethodPut:
		var body struct {
			CollectorURL    string `json:"collectorURL"`
			CollectorAPIKey string `json:"collectorAPIKey"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid json"})
			return
		}
		key := strings.TrimSpace(body.CollectorAPIKey)
		url := strings.TrimSpace(body.CollectorURL)
		if url == "" {
			url = defaultTranscriptCollectorURL
		}
		if key != "" && !validCollectorAPIKey(key) {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid collector API key format (expected tlr_tc_…)"})
			return
		}

		admin.Controller.Options.mutex.Lock()
		cfg := admin.Controller.Options.TranscriptionConfig
		cfg.CollectorURL = url
		if key != "" {
			cfg.CollectorAPIKey = key
		} else if strings.TrimSpace(cfg.CollectorAPIKey) == "" {
			admin.Controller.Options.mutex.Unlock()
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "collectorAPIKey required"})
			return
		}
		saved := cfg
		admin.Controller.Options.mutex.Unlock()

		if err := admin.Controller.Options.WriteKey(admin.Controller.Database, "transcriptionConfig", saved, func() {
			admin.Controller.Options.TranscriptionConfig = saved
		}); err != nil {
			log.Printf("transcript review: save collector settings: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed to save settings"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"message": "collector settings saved"})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) TranscriptReviewCollectorStatsHandler(w http.ResponseWriter, r *http.Request) {
	if !admin.requireAdminToken(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	admin.refreshCollectorFromDB()
	collectorURL, apiKey := admin.collectorSettings()
	if collectorURL == "" {
		collectorURL = defaultTranscriptCollectorURL
	}
	if !validCollectorAPIKey(apiKey) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "transcript collector API key not configured"})
		return
	}

	stats, err := fetchCollectorStats(collectorURL, apiKey)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func fetchCollectorStats(baseURL, apiKey string) (map[string]any, error) {
	baseURL = strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/stats", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("collector request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("collector returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("invalid collector response: %w", err)
	}
	return out, nil
}

func fetchCollectorGlobalProgress(baseURL string) (map[string]any, error) {
	baseURL = strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = defaultTranscriptCollectorURL
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(baseURL + "/api/status")
	if err != nil {
		return nil, fmt.Errorf("collector request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("collector returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var status map[string]any
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("invalid collector response: %w", err)
	}
	progress, _ := status["trainingProgress"].(map[string]any)
	if progress == nil {
		return nil, fmt.Errorf("collector did not return training progress")
	}
	return progress, nil
}

func (admin *Admin) collectorSettings() (url, apiKey string) {
	admin.Controller.Options.mutex.Lock()
	defer admin.Controller.Options.mutex.Unlock()
	cfg := admin.Controller.Options.TranscriptionConfig
	return defaultTranscriptCollectorURL, strings.TrimSpace(cfg.CollectorAPIKey)
}

func (admin *Admin) refreshCollectorFromDB() {
	if admin.Controller.Database == nil {
		return
	}
	var value sql.NullString
	if err := admin.Controller.Database.Sql.QueryRow(`SELECT "value" FROM "options" WHERE "key" = 'transcriptionConfig'`).Scan(&value); err != nil || !value.Valid {
		return
	}
	var cfg TranscriptionConfig
	if err := json.Unmarshal([]byte(value.String), &cfg); err != nil {
		return
	}
	if strings.TrimSpace(cfg.CollectorAPIKey) != "" && !validCollectorAPIKey(cfg.CollectorAPIKey) {
		cfg.CollectorAPIKey = ""
		if b, err := json.Marshal(cfg); err == nil {
			_, _ = admin.Controller.Database.Sql.Exec(
				`UPDATE "options" SET "value" = $1 WHERE "key" = 'transcriptionConfig'`,
				string(b),
			)
		}
	}
	admin.Controller.Options.mutex.Lock()
	admin.Controller.Options.TranscriptionConfig.CollectorURL = cfg.CollectorURL
	admin.Controller.Options.TranscriptionConfig.CollectorAPIKey = cfg.CollectorAPIKey
	admin.Controller.Options.mutex.Unlock()
}

func (admin *Admin) clearCollectorAPIKey() {
	admin.refreshCollectorFromDB()
	admin.Controller.Options.mutex.Lock()
	cfg := admin.Controller.Options.TranscriptionConfig
	cfg.CollectorAPIKey = ""
	saved := cfg
	admin.Controller.Options.mutex.Unlock()
	_ = admin.Controller.Options.WriteKey(admin.Controller.Database, "transcriptionConfig", saved, func() {
		admin.Controller.Options.TranscriptionConfig = saved
	})
}

func (admin *Admin) collectorConfigured() bool {
	admin.refreshCollectorFromDB()
	_, key := admin.collectorSettings()
	return validCollectorAPIKey(key)
}

func pingTranscriptCollector(baseURL string) error {
	baseURL = strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return fmt.Errorf("collector URL is empty")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(baseURL + "/api/status")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("collector returned %d", resp.StatusCode)
	}
	return nil
}

func submitToTranscriptCollector(baseURL, apiKey string, audio []byte, filename, mime string, metaJSON []byte) error {
	baseURL = strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return fmt.Errorf("collector URL is empty")
	}
	submitURL := baseURL + "/api/v1/submissions"

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	metaPart, err := writer.CreateFormField("metadata")
	if err != nil {
		return err
	}
	if _, err := metaPart.Write(metaJSON); err != nil {
		return err
	}

	if filename == "" {
		filename = "call.wav"
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	audioPart, err := writer.CreateFormFile("audio", filename)
	if err != nil {
		return err
	}
	if _, err := audioPart.Write(audio); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, submitURL, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("collector request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("collector returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}
