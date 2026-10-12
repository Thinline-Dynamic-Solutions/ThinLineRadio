// Copyright (C) 2026 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PublicApiCapabilities are per-user endpoint grants set by the server owner.
type PublicApiCapabilities struct {
	Health        bool `json:"health"`
	Transcripts   bool `json:"transcripts"`
	AlertWebhooks bool `json:"alertWebhooks"`
}

func parsePublicApiCapabilities(raw string) PublicApiCapabilities {
	var caps PublicApiCapabilities
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return caps
	}
	_ = json.Unmarshal([]byte(raw), &caps)
	return caps
}

func (user *User) PublicApiCaps() PublicApiCapabilities {
	if user == nil {
		return PublicApiCapabilities{}
	}
	return parsePublicApiCapabilities(user.PublicApiCapabilities)
}

func (user *User) HasPublicApiCapability(name string) bool {
	if user == nil || !user.PublicApiEnabled || user.Suspended {
		return false
	}
	caps := user.PublicApiCaps()
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "health":
		return caps.Health
	case "transcripts":
		return caps.Transcripts
	case "alertwebhooks", "alert_webhooks", "alertWebhooks":
		return caps.AlertWebhooks
	default:
		return false
	}
}

// PublicApiKey is a hashed user-owned REST credential (not an uploader apikey).
type PublicApiKey struct {
	Id           uint64 `json:"id"`
	UserId       uint64 `json:"userId"`
	Name         string `json:"name"`
	KeyPrefix    string `json:"keyPrefix"`
	KeyHash      string `json:"-"`
	CreatedAt    int64  `json:"createdAt"`
	LastUsedAt   int64  `json:"lastUsedAt"`
	Disabled     bool   `json:"disabled"`
	RequestCount int64  `json:"requestCount"`
}

type PublicApiWebhook struct {
	Id              uint64   `json:"id"`
	UserId          uint64   `json:"userId"`
	DestinationType string   `json:"destinationType"`
	Url             string   `json:"url"`
	Email           string   `json:"email"`
	Secret          string   `json:"secret,omitempty"`
	Enabled         bool     `json:"enabled"`
	AlertTypes      []string `json:"alertTypes"`
	CreatedAt       int64    `json:"createdAt"`
}

type publicApiAuthContext struct {
	User *User
	Key  *PublicApiKey
}

type publicApiRateBucket struct {
	windowStart time.Time
	count       int
}

// PublicApiService owns key lookup, rate limits, and /api/public/v1 handlers.
type PublicApiService struct {
	controller *Controller

	rateMu sync.Mutex
	rates  map[uint64]*publicApiRateBucket
}

func NewPublicApiService(c *Controller) *PublicApiService {
	return &PublicApiService{
		controller: c,
		rates:      make(map[uint64]*publicApiRateBucket),
	}
}

func hashPublicApiKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

func generatePublicApiKeyPlaintext() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "tlr_" + hex.EncodeToString(buf), nil
}

func extractPublicApiKeyFromRequest(r *http.Request) string {
	if h := strings.TrimSpace(r.Header.Get("Authorization")); h != "" {
		if strings.HasPrefix(strings.ToLower(h), "bearer ") {
			return strings.TrimSpace(h[7:])
		}
	}
	if k := strings.TrimSpace(r.Header.Get("X-API-Key")); k != "" {
		return k
	}
	return strings.TrimSpace(r.URL.Query().Get("api_key"))
}

func (svc *PublicApiService) lookupKey(plaintext string) (*PublicApiKey, *User, error) {
	if plaintext == "" || svc.controller == nil || svc.controller.Database == nil {
		return nil, nil, fmt.Errorf("unauthorized")
	}
	hash := hashPublicApiKey(plaintext)
	row := &PublicApiKey{}
	err := svc.controller.Database.Sql.QueryRow(
		`SELECT "keyId", "userId", "name", "keyPrefix", "keyHash", "createdAt", "lastUsedAt", "disabled", "requestCount" FROM "publicApiKeys" WHERE "keyHash" = $1`,
		hash,
	).Scan(&row.Id, &row.UserId, &row.Name, &row.KeyPrefix, &row.KeyHash, &row.CreatedAt, &row.LastUsedAt, &row.Disabled, &row.RequestCount)
	if err != nil {
		return nil, nil, fmt.Errorf("unauthorized")
	}
	if row.Disabled {
		return nil, nil, fmt.Errorf("key disabled")
	}
	user := svc.controller.Users.GetUserById(row.UserId)
	if user == nil || !user.PublicApiEnabled || user.Suspended {
		return nil, nil, fmt.Errorf("unauthorized")
	}
	return row, user, nil
}

func (svc *PublicApiService) allowRate(keyId uint64) bool {
	const limit = 120
	const window = time.Minute
	now := time.Now()
	svc.rateMu.Lock()
	defer svc.rateMu.Unlock()
	b := svc.rates[keyId]
	if b == nil || now.Sub(b.windowStart) >= window {
		svc.rates[keyId] = &publicApiRateBucket{windowStart: now, count: 1}
		return true
	}
	if b.count >= limit {
		return false
	}
	b.count++
	return true
}

func (svc *PublicApiService) recordUsage(key *PublicApiKey, path string, status int) {
	if key == nil || svc.controller.Database == nil {
		return
	}
	now := time.Now().UnixMilli()
	_, _ = svc.controller.Database.Sql.Exec(
		`UPDATE "publicApiKeys" SET "lastUsedAt" = $1, "requestCount" = "requestCount" + 1 WHERE "keyId" = $2`,
		now, key.Id,
	)
	_, _ = svc.controller.Database.Sql.Exec(
		`INSERT INTO "publicApiKeyUsage" ("keyId", "path", "status", "at") VALUES ($1, $2, $3, $4)`,
		key.Id, path, status, now,
	)
	// Best-effort prune older than 30 days
	cutoff := now - int64(30*24*time.Hour/time.Millisecond)
	_, _ = svc.controller.Database.Sql.Exec(`DELETE FROM "publicApiKeyUsage" WHERE "at" < $1`, cutoff)
}

func (svc *PublicApiService) requireAuth(w http.ResponseWriter, r *http.Request, capability string) *publicApiAuthContext {
	plaintext := extractPublicApiKeyFromRequest(r)
	key, user, err := svc.lookupKey(plaintext)
	if err != nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return nil
	}
	if !svc.allowRate(key.Id) {
		svc.recordUsage(key, r.URL.Path, http.StatusTooManyRequests)
		http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
		return nil
	}
	if capability != "" && !user.HasPublicApiCapability(capability) {
		svc.recordUsage(key, r.URL.Path, http.StatusForbidden)
		http.Error(w, `{"error":"capability not granted"}`, http.StatusForbidden)
		return nil
	}
	return &publicApiAuthContext{User: user, Key: key}
}

func (svc *PublicApiService) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// Router dispatches /api/public/v1/*
func (svc *PublicApiService) Router(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/public/v1")
	path = strings.TrimPrefix(path, "/")

	switch {
	case path == "health" && r.Method == http.MethodGet:
		svc.handleHealth(w, r)
	case path == "health/live" && r.Method == http.MethodGet:
		svc.handleHealthLive(w, r)
	case path == "health/systems" && r.Method == http.MethodGet:
		svc.handleHealthSystems(w, r, 0)
	case strings.HasPrefix(path, "health/systems/") && r.Method == http.MethodGet:
		refStr := strings.TrimPrefix(path, "health/systems/")
		ref, _ := strconv.ParseUint(refStr, 10, 64)
		svc.handleHealthSystems(w, r, ref)
	case path == "transcripts" && r.Method == http.MethodGet:
		svc.handleTranscripts(w, r, 0)
	case strings.HasPrefix(path, "transcripts/") && r.Method == http.MethodGet:
		idStr := strings.TrimPrefix(path, "transcripts/")
		id, _ := strconv.ParseUint(idStr, 10, 64)
		svc.handleTranscripts(w, r, id)
	case path == "keys":
		svc.handleKeys(w, r)
	case strings.HasPrefix(path, "keys/") && (r.Method == http.MethodDelete || r.Method == http.MethodPost):
		svc.handleKeyAction(w, r, strings.TrimPrefix(path, "keys/"))
	case path == "webhooks":
		svc.handleWebhooks(w, r)
	case strings.HasPrefix(path, "webhooks/") && (r.Method == http.MethodDelete || r.Method == http.MethodPut || r.Method == http.MethodPost):
		svc.handleWebhookAction(w, r, strings.TrimPrefix(path, "webhooks/"))
	default:
		http.NotFound(w, r)
	}
}

func (svc *PublicApiService) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := svc.requireAuth(w, r, "health")
	if ctx == nil {
		return
	}
	ok, reasons := svc.controller.Health.evaluateReadiness()
	status := "ok"
	code := http.StatusOK
	if !ok {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}
	svc.recordUsage(ctx.Key, r.URL.Path, code)
	svc.writeJSON(w, code, map[string]any{
		"source":         "thinline-radio",
		"status":         status,
		"reasons":        reasons,
		"uptime_seconds": int64(time.Since(processStartTime).Seconds()),
		"version":        Version,
	})
}

func (svc *PublicApiService) handleHealthLive(w http.ResponseWriter, r *http.Request) {
	ctx := svc.requireAuth(w, r, "health")
	if ctx == nil {
		return
	}
	svc.recordUsage(ctx.Key, r.URL.Path, http.StatusOK)
	svc.writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (svc *PublicApiService) handleHealthSystems(w http.ResponseWriter, r *http.Request, systemRef uint64) {
	ctx := svc.requireAuth(w, r, "health")
	if ctx == nil {
		return
	}

	type sysHealth struct {
		SystemRef    uint   `json:"systemRef"`
		SystemId     uint64 `json:"systemId"`
		Label        string `json:"label"`
		LastCallAt   int64  `json:"lastCallAt"`
		CallsLastHour int64 `json:"callsLastHour"`
	}

	out := []sysHealth{}
	for _, sys := range svc.controller.Systems.List {
		if sys == nil {
			continue
		}
		if systemRef > 0 && uint64(sys.SystemRef) != systemRef {
			continue
		}
		if !ctx.User.SystemAdmin && !svc.controller.userHasSystemScopeAccess(ctx.User, sys.SystemRef) {
			continue
		}

		var lastCallAt int64
		_ = svc.controller.Database.Sql.QueryRow(
			`SELECT COALESCE(MAX("timestamp"), 0) FROM "calls" WHERE "systemId" = $1`,
			sys.Id,
		).Scan(&lastCallAt)
		hourAgo := time.Now().Add(-time.Hour).UnixMilli()
		var callsLastHour int64
		_ = svc.controller.Database.Sql.QueryRow(
			`SELECT COUNT(*) FROM "calls" WHERE "systemId" = $1 AND "timestamp" >= $2`,
			sys.Id, hourAgo,
		).Scan(&callsLastHour)

		out = append(out, sysHealth{
			SystemRef:     sys.SystemRef,
			SystemId:      sys.Id,
			Label:         sys.Label,
			LastCallAt:    lastCallAt,
			CallsLastHour: callsLastHour,
		})
	}

	if systemRef > 0 && len(out) == 0 {
		svc.recordUsage(ctx.Key, r.URL.Path, http.StatusNotFound)
		svc.writeJSON(w, http.StatusNotFound, map[string]string{"error": "system not found or not accessible"})
		return
	}
	svc.recordUsage(ctx.Key, r.URL.Path, http.StatusOK)
	if systemRef > 0 {
		svc.writeJSON(w, http.StatusOK, out[0])
		return
	}
	svc.writeJSON(w, http.StatusOK, map[string]any{"systems": out})
}

func (svc *PublicApiService) handleTranscripts(w http.ResponseWriter, r *http.Request, callId uint64) {
	ctx := svc.requireAuth(w, r, "transcripts")
	if ctx == nil {
		return
	}

	if callId > 0 {
		svc.handleSingleTranscript(w, r, ctx, callId)
		return
	}

	limit := uint(50)
	offset := uint(0)
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.ParseUint(l, 10, 32); err == nil {
			limit = uint(v)
		}
	}
	if limit > 200 {
		limit = 200
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.ParseUint(o, 10, 32); err == nil {
			offset = uint(v)
		}
	}

	where := []string{`(c."transcript" IS NOT NULL AND c."transcript" <> '')`}
	if s := r.URL.Query().Get("systemId"); s != "" {
		if v, err := strconv.ParseUint(s, 10, 64); err == nil {
			var resolved uint64
			if err := svc.controller.Database.Sql.QueryRow(`SELECT "systemId" FROM "systems" WHERE "systemRef" = $1`, v).Scan(&resolved); err == nil {
				where = append(where, fmt.Sprintf(`c."systemId" = %d`, resolved))
			} else {
				where = append(where, fmt.Sprintf(`c."systemId" = %d`, v))
			}
		}
	}
	if df := r.URL.Query().Get("dateFrom"); df != "" {
		if v, err := strconv.ParseInt(df, 10, 64); err == nil {
			where = append(where, fmt.Sprintf(`c."timestamp" >= %d`, v))
		}
	}
	if dt := r.URL.Query().Get("dateTo"); dt != "" {
		if v, err := strconv.ParseInt(dt, 10, 64); err == nil {
			where = append(where, fmt.Sprintf(`c."timestamp" <= %d`, v))
		}
	}
	if search := strings.TrimSpace(r.URL.Query().Get("search")); search != "" {
		where = append(where, fmt.Sprintf(`c."transcript" ILIKE '%%%s%%'`, escapeQuotes(search)))
	}

	query := fmt.Sprintf(
		`SELECT c."callId", c."systemId", c."talkgroupId", c."timestamp", c."transcript", c."transcriptionStatus", c."alertSummary",
			sy."systemRef", sy."label", t."talkgroupRef", t."label", t."name"
		 FROM "calls" AS c
		 LEFT JOIN "systems" AS sy ON sy."systemId" = c."systemId"
		 LEFT JOIN "talkgroups" AS t ON t."talkgroupId" = c."talkgroupId"
		 WHERE %s
		 ORDER BY c."timestamp" DESC
		 LIMIT %d OFFSET %d`,
		strings.Join(where, " AND "), limit*3, offset, // over-fetch then filter by access
	)

	rows, err := svc.controller.Database.Sql.Query(query)
	if err != nil {
		svc.recordUsage(ctx.Key, r.URL.Path, http.StatusInternalServerError)
		svc.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	results := []map[string]any{}
	for rows.Next() {
		var (
			id, sysId, tgId             uint64
			ts                          int64
			transcript, status, summary string
			sysRefN                     sql.NullInt64
			sysLabelN, tgLabelN, tgNameN sql.NullString
			tgRefN                      sql.NullInt64
		)
		if err := rows.Scan(&id, &sysId, &tgId, &ts, &transcript, &status, &summary, &sysRefN, &sysLabelN, &tgRefN, &tgLabelN, &tgNameN); err != nil {
			continue
		}
		sysRef := uint(0)
		if sysRefN.Valid {
			sysRef = uint(sysRefN.Int64)
		}
		tgRef := uint(0)
		if tgRefN.Valid {
			tgRef = uint(tgRefN.Int64)
		}
		sys, _ := svc.controller.Systems.GetSystemById(sysId)
		var tg *Talkgroup
		if sys != nil {
			tg, _ = sys.Talkgroups.GetTalkgroupById(tgId)
			if sysRef == 0 {
				sysRef = sys.SystemRef
			}
		}
		call := &Call{System: sys, Talkgroup: tg, SystemId: sysRef, Id: id}
		if !svc.controller.userHasAccess(ctx.User, call) {
			continue
		}
		results = append(results, map[string]any{
			"callId":              id,
			"systemId":            sysId,
			"talkgroupId":         tgId,
			"systemRef":           sysRef,
			"talkgroupRef":        tgRef,
			"timestamp":           ts,
			"transcript":          transcript,
			"transcriptionStatus": status,
			"alertSummary":        summary,
			"systemLabel":         sysLabelN.String,
			"talkgroupLabel":      tgLabelN.String,
			"talkgroupName":       tgNameN.String,
		})
		if uint(len(results)) >= limit {
			break
		}
	}

	svc.recordUsage(ctx.Key, r.URL.Path, http.StatusOK)
	svc.writeJSON(w, http.StatusOK, map[string]any{"results": results, "count": len(results)})
}

func (svc *PublicApiService) handleSingleTranscript(w http.ResponseWriter, r *http.Request, ctx *publicApiAuthContext, callId uint64) {
	var (
		sysId, tgId             uint64
		ts                      int64
		transcript, status, sum string
	)
	err := svc.controller.Database.Sql.QueryRow(
		`SELECT "systemId", "talkgroupId", "timestamp", COALESCE("transcript",''), COALESCE("transcriptionStatus",''), COALESCE("alertSummary",'') FROM "calls" WHERE "callId" = $1`,
		callId,
	).Scan(&sysId, &tgId, &ts, &transcript, &status, &sum)
	if err != nil || transcript == "" {
		svc.recordUsage(ctx.Key, r.URL.Path, http.StatusNotFound)
		svc.writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	sys, _ := svc.controller.Systems.GetSystemById(sysId)
	var tg *Talkgroup
	if sys != nil {
		tg, _ = sys.Talkgroups.GetTalkgroupById(tgId)
	}
	if !svc.controller.userHasAccess(ctx.User, &Call{System: sys, Talkgroup: tg, Id: callId}) {
		svc.recordUsage(ctx.Key, r.URL.Path, http.StatusForbidden)
		svc.writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	svc.recordUsage(ctx.Key, r.URL.Path, http.StatusOK)
	svc.writeJSON(w, http.StatusOK, map[string]any{
		"callId":              callId,
		"systemId":            sysId,
		"talkgroupId":         tgId,
		"timestamp":           ts,
		"transcript":          transcript,
		"transcriptionStatus": status,
		"alertSummary":        sum,
	})
}

// resolveUserForKeyMgmt accepts either a public API key (owner) or PIN session.
func (svc *PublicApiService) resolveUserForKeyMgmt(r *http.Request) *User {
	if plaintext := extractPublicApiKeyFromRequest(r); strings.HasPrefix(plaintext, "tlr_") {
		_, user, err := svc.lookupKey(plaintext)
		if err == nil && user != nil && user.PublicApiEnabled {
			return user
		}
	}
	client := svc.controller.Api.getClient(r)
	if client != nil && client.User != nil && client.User.PublicApiEnabled {
		return client.User
	}
	return nil
}

func (svc *PublicApiService) handleKeys(w http.ResponseWriter, r *http.Request) {
	user := svc.resolveUserForKeyMgmt(r)
	if user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		keys, err := svc.listKeysForUser(user.Id)
		if err != nil {
			svc.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		svc.writeJSON(w, http.StatusOK, map[string]any{
			"keys":         keys,
			"capabilities": user.PublicApiCaps(),
			"enabled":      user.PublicApiEnabled,
		})
	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body)
		name := strings.TrimSpace(body.Name)
		if name == "" {
			name = "API key"
		}
		plain, err := generatePublicApiKeyPlaintext()
		if err != nil {
			svc.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "key generation failed"})
			return
		}
		prefix := plain
		if len(prefix) > 12 {
			prefix = prefix[:12]
		}
		now := time.Now().UnixMilli()
		hash := hashPublicApiKey(plain)
		var id uint64
		err = svc.controller.Database.Sql.QueryRow(
			`INSERT INTO "publicApiKeys" ("userId", "name", "keyPrefix", "keyHash", "createdAt", "lastUsedAt", "disabled", "requestCount")
			 VALUES ($1, $2, $3, $4, $5, 0, false, 0) RETURNING "keyId"`,
			user.Id, name, prefix, hash, now,
		).Scan(&id)
		if err != nil {
			svc.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save key"})
			return
		}
		svc.writeJSON(w, http.StatusCreated, map[string]any{
			"id":        id,
			"name":      name,
			"keyPrefix": prefix,
			"key":       plain,
			"createdAt": now,
			"message":   "Store this key now; it will not be shown again.",
		})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (svc *PublicApiService) handleKeyAction(w http.ResponseWriter, r *http.Request, rest string) {
	user := svc.resolveUserForKeyMgmt(r)
	if user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	id, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		svc.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid key id"})
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	if r.Method == http.MethodDelete || action == "revoke" || action == "disable" {
		res, err := svc.controller.Database.Sql.Exec(
			`UPDATE "publicApiKeys" SET "disabled" = true WHERE "keyId" = $1 AND "userId" = $2`,
			id, user.Id,
		)
		if err != nil {
			svc.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed"})
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			svc.writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		svc.writeJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (svc *PublicApiService) listKeysForUser(userId uint64) ([]PublicApiKey, error) {
	rows, err := svc.controller.Database.Sql.Query(
		`SELECT "keyId", "userId", "name", "keyPrefix", "createdAt", "lastUsedAt", "disabled", "requestCount"
		 FROM "publicApiKeys" WHERE "userId" = $1 ORDER BY "createdAt" DESC`,
		userId,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PublicApiKey{}
	for rows.Next() {
		k := PublicApiKey{}
		if err := rows.Scan(&k.Id, &k.UserId, &k.Name, &k.KeyPrefix, &k.CreatedAt, &k.LastUsedAt, &k.Disabled, &k.RequestCount); err != nil {
			continue
		}
		out = append(out, k)
	}
	return out, nil
}

func (svc *PublicApiService) handleWebhooks(w http.ResponseWriter, r *http.Request) {
	user := svc.resolveUserForKeyMgmt(r)
	if user == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if !user.HasPublicApiCapability("alertWebhooks") {
		svc.writeJSON(w, http.StatusForbidden, map[string]string{"error": "alert webhooks not granted"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		hooks, err := svc.listWebhooksForUser(user.Id)
		if err != nil {
			svc.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		svc.writeJSON(w, http.StatusOK, map[string]any{"webhooks": hooks})
	case http.MethodPost:
		var body struct {
			DestinationType string   `json:"destinationType"`
			Url             string   `json:"url"`
			Email           string   `json:"email"`
			Secret          string   `json:"secret"`
			Enabled         *bool    `json:"enabled"`
			AlertTypes      []string `json:"alertTypes"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			svc.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		dest := strings.ToLower(strings.TrimSpace(body.DestinationType))
		switch dest {
		case "slack", "discord", "email", "pagerduty", "custom":
		default:
			svc.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid destinationType"})
			return
		}
		if dest == "email" {
			if strings.TrimSpace(body.Email) == "" {
				svc.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email required"})
				return
			}
		} else if strings.TrimSpace(body.Url) == "" {
			svc.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url required"})
			return
		}
		alertTypes := body.AlertTypes
		if len(alertTypes) == 0 {
			alertTypes = []string{"*"}
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		typesJson, _ := json.Marshal(alertTypes)
		now := time.Now().UnixMilli()
		var id uint64
		err := svc.controller.Database.Sql.QueryRow(
			`INSERT INTO "publicApiWebhooks" ("userId", "destinationType", "url", "email", "secret", "enabled", "alertTypes", "createdAt")
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING "webhookId"`,
			user.Id, dest, strings.TrimSpace(body.Url), strings.TrimSpace(body.Email),
			strings.TrimSpace(body.Secret), enabled, string(typesJson), now,
		).Scan(&id)
		if err != nil {
			svc.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save webhook"})
			return
		}
		svc.writeJSON(w, http.StatusCreated, PublicApiWebhook{
			Id: id, UserId: user.Id, DestinationType: dest,
			Url: strings.TrimSpace(body.Url), Email: strings.TrimSpace(body.Email),
			Secret: strings.TrimSpace(body.Secret), Enabled: enabled,
			AlertTypes: alertTypes, CreatedAt: now,
		})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (svc *PublicApiService) handleWebhookAction(w http.ResponseWriter, r *http.Request, rest string) {
	user := svc.resolveUserForKeyMgmt(r)
	if user == nil || !user.HasPublicApiCapability("alertWebhooks") {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	id, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		svc.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	if r.Method == http.MethodDelete {
		res, err := svc.controller.Database.Sql.Exec(
			`DELETE FROM "publicApiWebhooks" WHERE "webhookId" = $1 AND "userId" = $2`, id, user.Id,
		)
		if err != nil {
			svc.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed"})
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			svc.writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		svc.writeJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}
	if r.Method == http.MethodPut || r.Method == http.MethodPost {
		var body struct {
			Enabled *bool    `json:"enabled"`
			Url     *string  `json:"url"`
			Email   *string  `json:"email"`
			Secret  *string  `json:"secret"`
			AlertTypes []string `json:"alertTypes"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			svc.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		hooks, _ := svc.listWebhooksForUser(user.Id)
		var existing *PublicApiWebhook
		for i := range hooks {
			if hooks[i].Id == id {
				existing = &hooks[i]
				break
			}
		}
		if existing == nil {
			svc.writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		if body.Enabled != nil {
			existing.Enabled = *body.Enabled
		}
		if body.Url != nil {
			existing.Url = strings.TrimSpace(*body.Url)
		}
		if body.Email != nil {
			existing.Email = strings.TrimSpace(*body.Email)
		}
		if body.Secret != nil {
			existing.Secret = strings.TrimSpace(*body.Secret)
		}
		if body.AlertTypes != nil {
			existing.AlertTypes = body.AlertTypes
		}
		typesJson, _ := json.Marshal(existing.AlertTypes)
		_, err := svc.controller.Database.Sql.Exec(
			`UPDATE "publicApiWebhooks" SET "url"=$1, "email"=$2, "secret"=$3, "enabled"=$4, "alertTypes"=$5 WHERE "webhookId"=$6 AND "userId"=$7`,
			existing.Url, existing.Email, existing.Secret, existing.Enabled, string(typesJson), id, user.Id,
		)
		if err != nil {
			svc.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed"})
			return
		}
		svc.writeJSON(w, http.StatusOK, existing)
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (svc *PublicApiService) listWebhooksForUser(userId uint64) ([]PublicApiWebhook, error) {
	rows, err := svc.controller.Database.Sql.Query(
		`SELECT "webhookId", "userId", "destinationType", "url", "email", "secret", "enabled", "alertTypes", "createdAt"
		 FROM "publicApiWebhooks" WHERE "userId" = $1 ORDER BY "createdAt" DESC`,
		userId,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PublicApiWebhook{}
	for rows.Next() {
		h := PublicApiWebhook{}
		var typesRaw string
		if err := rows.Scan(&h.Id, &h.UserId, &h.DestinationType, &h.Url, &h.Email, &h.Secret, &h.Enabled, &typesRaw, &h.CreatedAt); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(typesRaw), &h.AlertTypes)
		if len(h.AlertTypes) == 0 {
			h.AlertTypes = []string{"*"}
		}
		out = append(out, h)
	}
	return out, nil
}
