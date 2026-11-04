// Copyright (C) 2026 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
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

// Server alerts send system alerts (no audio, transcription failures, disk
// space, alerts posted by outside monitors) to admin-managed destinations, and
// let outside monitors post alerts with an alert key.

const (
	serverAlertKeyPrefix      = "tla_"
	serverAlertRatePerMinute  = 30
	serverAlertMaxTitle       = 200
	serverAlertMaxMessage     = 4000
	serverAlertMaxMetaKeys    = 30
	defaultDiskSpaceAlertPct  = 10
	diskSpaceCheckInterval    = 10 * time.Minute
	diskSpaceAlertRepeatAfter = 24 * time.Hour
)

var serverAlertSeverityRank = map[string]int{"info": 0, "warning": 1, "error": 2, "critical": 3}

type ServerAlertDestination struct {
	Id              uint64 `json:"id"`
	Label           string `json:"label"`
	DestinationType string `json:"destinationType"`
	Url             string `json:"url"`
	Email           string `json:"email"`
	Secret          string `json:"secret,omitempty"`
	HasSecret       bool   `json:"hasSecret"`
	MinSeverity     string `json:"minSeverity"`
	Enabled         bool   `json:"enabled"`
	CreatedAt       int64  `json:"createdAt"`
}

type ServerAlertKey struct {
	Id         uint64 `json:"id"`
	Label      string `json:"label"`
	Prefix     string `json:"prefix"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  int64  `json:"createdAt"`
	LastUsedAt int64  `json:"lastUsedAt"`
}

type serverAlertPayload struct {
	Source    string         `json:"source"`
	Severity  string         `json:"severity"`
	Title     string         `json:"title"`
	Message   string         `json:"message"`
	Timestamp int64          `json:"timestamp"`
	Metadata  map[string]any `json:"metadata"`
}

var (
	serverAlertRateMu sync.Mutex
	serverAlertRate   = map[uint64]*publicApiRateBucket{}

	diskAlertMu     sync.Mutex
	diskAlertActive bool
	diskAlertLastAt time.Time
)

func migrateServerAlerts(db *Database) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS "serverAlertDestinations" (
			"destinationId" bigserial NOT NULL PRIMARY KEY,
			"label" text NOT NULL DEFAULT '',
			"destinationType" text NOT NULL DEFAULT 'custom',
			"url" text NOT NULL DEFAULT '',
			"email" text NOT NULL DEFAULT '',
			"secret" text NOT NULL DEFAULT '',
			"minSeverity" text NOT NULL DEFAULT 'warning',
			"enabled" boolean NOT NULL DEFAULT true,
			"createdAt" bigint NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS "serverAlertKeys" (
			"keyId" bigserial NOT NULL PRIMARY KEY,
			"label" text NOT NULL DEFAULT '',
			"prefix" text NOT NULL DEFAULT '',
			"keyHash" text NOT NULL UNIQUE,
			"enabled" boolean NOT NULL DEFAULT true,
			"createdAt" bigint NOT NULL DEFAULT 0,
			"lastUsedAt" bigint NOT NULL DEFAULT 0
		)`,
	} {
		if _, err := db.Sql.Exec(stmt); err != nil {
			return fmt.Errorf("migrateServerAlerts: %w", err)
		}
	}
	return nil
}

func normalizeServerAlertSeverity(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "warning", true
	}
	if s == "warn" {
		s = "warning"
	}
	_, ok := serverAlertSeverityRank[s]
	return s, ok
}

func serverAlertSeverityAtLeast(severity, min string) bool {
	return serverAlertSeverityRank[severity] >= serverAlertSeverityRank[min]
}

func hashServerAlertKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func (controller *Controller) listServerAlertDestinations() ([]*ServerAlertDestination, error) {
	rows, err := controller.Database.Sql.Query(`SELECT "destinationId", "label", "destinationType", "url", "email", "secret", "minSeverity", "enabled", "createdAt" FROM "serverAlertDestinations" ORDER BY "destinationId"`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ServerAlertDestination{}
	for rows.Next() {
		d := &ServerAlertDestination{}
		if err := rows.Scan(&d.Id, &d.Label, &d.DestinationType, &d.Url, &d.Email, &d.Secret, &d.MinSeverity, &d.Enabled, &d.CreatedAt); err != nil {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func (controller *Controller) getServerAlertDestination(id uint64) *ServerAlertDestination {
	all, err := controller.listServerAlertDestinations()
	if err != nil {
		return nil
	}
	for _, d := range all {
		if d.Id == id {
			return d
		}
	}
	return nil
}

// serverAlertFromSystemAlert builds the outbound payload for a stored system alert.
func (controller *Controller) serverAlertFromSystemAlert(alertType, severity, title, message, dataJSON string, createdAt int64) serverAlertPayload {
	meta := map[string]any{}
	_ = json.Unmarshal([]byte(dataJSON), &meta)
	source := "thinline-radio"
	if s, ok := meta["source"].(string); ok && strings.TrimSpace(s) != "" {
		source = s
		delete(meta, "source")
	}
	if extra, ok := meta["metadata"].(map[string]any); ok {
		delete(meta, "metadata")
		for k, v := range extra {
			if _, taken := meta[k]; !taken {
				meta[k] = v
			}
		}
	}
	if reported, ok := meta["reportedAt"].(float64); ok && reported > 0 {
		createdAt = int64(reported)
		delete(meta, "reportedAt")
	}
	meta["alertType"] = alertType
	if controller.Options != nil {
		if host := strings.TrimSpace(controller.Options.BaseUrl); host != "" {
			meta["server"] = host
		}
	}
	return serverAlertPayload{
		Source:    source,
		Severity:  severity,
		Title:     title,
		Message:   message,
		Timestamp: createdAt,
		Metadata:  meta,
	}
}

// DispatchServerAlert sends one system alert to every enabled destination whose
// minimum severity it meets. Failures are logged and never block the caller.
func (controller *Controller) DispatchServerAlert(payload serverAlertPayload) {
	dests, err := controller.listServerAlertDestinations()
	if err != nil {
		return
	}
	for _, d := range dests {
		if !d.Enabled || !serverAlertSeverityAtLeast(payload.Severity, d.MinSeverity) {
			continue
		}
		dest := d
		go func() {
			if err := controller.deliverServerAlert(dest, payload); err != nil {
				controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("server alert to %q failed: %v", dest.Label, err))
			}
		}()
	}
}

func (controller *Controller) deliverServerAlert(d *ServerAlertDestination, p serverAlertPayload) error {
	if d.DestinationType == "email" {
		if controller.EmailService == nil {
			return fmt.Errorf("email is not set up on this server")
		}
		fromName, fromEmail := "ThinLine Radio", ""
		if o := controller.Options; o != nil {
			if o.EmailSmtpFromName != "" {
				fromName = o.EmailSmtpFromName
			}
			fromEmail = o.EmailSmtpFromEmail
			if fromEmail == "" {
				fromEmail = o.Email
			}
		}
		body := fmt.Sprintf("<p><strong>%s</strong></p><p>%s</p><p>Source: %s</p>", htmlEscape(serverAlertHeading(p)), htmlEscape(p.Message), htmlEscape(p.Source))
		return controller.EmailService.sendEmail(fromName, fromEmail, d.Email, "[Server Alert] "+p.Title, body)
	}
	if d.DestinationType != "pagerduty" {
		if err := validatePublicWebhookURL(d.Url, d.DestinationType); err != nil {
			return err
		}
	}
	url, body, headers, err := buildServerAlertRequest(d, p)
	if err != nil {
		return err
	}
	return postServerAlert(url, body, headers)
}

func serverAlertHeading(p serverAlertPayload) string {
	return fmt.Sprintf("[%s] %s", strings.ToUpper(p.Severity), p.Title)
}

// buildServerAlertRequest shapes the alert for a webhook destination.
func buildServerAlertRequest(d *ServerAlertDestination, p serverAlertPayload) (string, any, map[string]string, error) {
	heading := serverAlertHeading(p)
	switch d.DestinationType {
	case "slack":
		return d.Url, map[string]any{"text": heading + "\n" + p.Message}, nil, nil
	case "discord":
		content := []rune(fmt.Sprintf("**%s**\n%s", heading, p.Message))
		if len(content) > 1900 {
			content = append(content[:1900], '…')
		}
		return d.Url, map[string]any{"content": string(content)}, nil, nil
	case "pagerduty":
		if strings.TrimSpace(d.Secret) == "" {
			return "", nil, nil, fmt.Errorf("missing PagerDuty routing key")
		}
		return "https://events.pagerduty.com/v2/enqueue", map[string]any{
			"routing_key":  d.Secret,
			"event_action": "trigger",
			"payload": map[string]any{
				"summary":        strings.TrimSpace(p.Title + ": " + p.Message),
				"severity":       p.Severity,
				"source":         p.Source,
				"timestamp":      time.UnixMilli(p.Timestamp).UTC().Format(time.RFC3339),
				"custom_details": p,
			},
		}, nil, nil
	case "custom":
		headers := map[string]string{}
		if s := strings.TrimSpace(d.Secret); s != "" {
			headers["X-Webhook-Secret"] = s
		}
		return d.Url, p, headers, nil
	}
	return "", nil, nil, fmt.Errorf("unknown destination type %q", d.DestinationType)
}

func postServerAlert(url string, body any, headers map[string]string) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ThinLineRadio-ServerAlerts/1.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := publicApiWebhookHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("destination answered %d", resp.StatusCode)
	}
	return nil
}

type serverAlertDestinationRequest struct {
	Label           string  `json:"label"`
	DestinationType string  `json:"destinationType"`
	Url             string  `json:"url"`
	Email           string  `json:"email"`
	Secret          *string `json:"secret"`
	MinSeverity     string  `json:"minSeverity"`
	Enabled         *bool   `json:"enabled"`
}

func (req *serverAlertDestinationRequest) apply(d *ServerAlertDestination) error {
	d.Label = strings.TrimSpace(req.Label)
	d.DestinationType = strings.ToLower(strings.TrimSpace(req.DestinationType))
	d.Url = strings.TrimSpace(req.Url)
	d.Email = strings.TrimSpace(req.Email)
	if req.Secret != nil {
		d.Secret = strings.TrimSpace(*req.Secret)
	}
	if req.Enabled != nil {
		d.Enabled = *req.Enabled
	}
	sev, ok := normalizeServerAlertSeverity(req.MinSeverity)
	if !ok {
		return fmt.Errorf("Minimum severity must be info, warning, error or critical")
	}
	d.MinSeverity = sev
	if d.Label == "" {
		return fmt.Errorf("Name is required")
	}
	switch d.DestinationType {
	case "slack", "discord", "custom":
		d.Email = ""
		if err := validatePublicWebhookURL(d.Url, d.DestinationType); err != nil {
			return err
		}
	case "pagerduty":
		d.Url, d.Email = "", ""
		if d.Secret == "" {
			return fmt.Errorf("PagerDuty needs an Events API v2 routing key")
		}
	case "email":
		d.Url, d.Secret = "", ""
		if !strings.Contains(d.Email, "@") || strings.ContainsAny(d.Email, " \r\n,;") {
			return fmt.Errorf("Enter one email address")
		}
	default:
		return fmt.Errorf("Unknown destination type")
	}
	return nil
}

func serverAlertDestinationJSON(d *ServerAlertDestination) *ServerAlertDestination {
	out := *d
	out.HasSecret = d.Secret != ""
	out.Secret = ""
	return &out
}

// ServerAlertsHandler serves the admin API under /api/admin/server-alerts:
//
//	GET    /api/admin/server-alerts                         destinations, keys and settings
//	PUT    /api/admin/server-alerts/settings                {"diskSpaceAlertPercent": n}
//	POST   /api/admin/server-alerts/destinations            add a destination
//	PUT    /api/admin/server-alerts/destinations/{id}       update it
//	DELETE /api/admin/server-alerts/destinations/{id}       delete it
//	POST   /api/admin/server-alerts/destinations/{id}/test  send a test alert
//	POST   /api/admin/server-alerts/keys                    create an alert key (shown once)
//	PUT    /api/admin/server-alerts/keys/{id}               {"enabled": bool}
//	DELETE /api/admin/server-alerts/keys/{id}               delete it
func (admin *Admin) ServerAlertsHandler(w http.ResponseWriter, r *http.Request) {
	if !admin.ValidateToken(admin.GetAuthorization(r)) {
		writeAdminJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	controller := admin.Controller
	db := controller.Database.Sql
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/server-alerts"), "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		parts = nil
	}
	fail := func(status int, msg string) {
		writeAdminJSON(w, status, map[string]string{"error": msg})
	}
	var id uint64
	if len(parts) >= 2 {
		parsed, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil || parsed == 0 {
			fail(http.StatusNotFound, "not found")
			return
		}
		id = parsed
	}
	decode := func(v any) bool {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
			fail(http.StatusBadRequest, "invalid request")
			return false
		}
		return true
	}

	switch {
	case len(parts) == 0 && r.Method == http.MethodGet:
		dests, err := controller.listServerAlertDestinations()
		if err != nil {
			fail(http.StatusInternalServerError, err.Error())
			return
		}
		outDests := make([]*ServerAlertDestination, 0, len(dests))
		for _, d := range dests {
			outDests = append(outDests, serverAlertDestinationJSON(d))
		}
		keys := []*ServerAlertKey{}
		rows, err := db.Query(`SELECT "keyId", "label", "prefix", "enabled", "createdAt", "lastUsedAt" FROM "serverAlertKeys" ORDER BY "keyId"`)
		if err == nil {
			for rows.Next() {
				k := &ServerAlertKey{}
				if rows.Scan(&k.Id, &k.Label, &k.Prefix, &k.Enabled, &k.CreatedAt, &k.LastUsedAt) == nil {
					keys = append(keys, k)
				}
			}
			rows.Close()
		}
		writeAdminJSON(w, http.StatusOK, map[string]any{
			"destinations":          outDests,
			"keys":                  keys,
			"diskSpaceAlertPercent": controller.Options.DiskSpaceAlertPercent,
			"healthAlertsEnabled":   controller.Options.SystemHealthAlertsEnabled,
			"emailAvailable":        controller.EmailService != nil && controller.Options.EmailServiceEnabled,
		})

	case len(parts) == 1 && parts[0] == "settings" && r.Method == http.MethodPut:
		var req struct {
			DiskSpaceAlertPercent *int `json:"diskSpaceAlertPercent"`
		}
		if !decode(&req) {
			return
		}
		if req.DiskSpaceAlertPercent == nil || *req.DiskSpaceAlertPercent < 0 || *req.DiskSpaceAlertPercent > 90 {
			fail(http.StatusBadRequest, "Disk space threshold must be between 0 and 90 percent")
			return
		}
		pct := uint(*req.DiskSpaceAlertPercent)
		if err := controller.Options.WriteKey(controller.Database, "diskSpaceAlertPercent", pct, func() {
			controller.Options.DiskSpaceAlertPercent = pct
		}); err != nil {
			fail(http.StatusInternalServerError, err.Error())
			return
		}
		go controller.checkDiskSpace()
		writeAdminJSON(w, http.StatusOK, map[string]any{"diskSpaceAlertPercent": pct})

	case len(parts) == 1 && parts[0] == "destinations" && r.Method == http.MethodPost:
		var req serverAlertDestinationRequest
		if !decode(&req) {
			return
		}
		d := &ServerAlertDestination{Enabled: true, CreatedAt: time.Now().UnixMilli()}
		if err := req.apply(d); err != nil {
			fail(http.StatusBadRequest, err.Error())
			return
		}
		if err := db.QueryRow(`INSERT INTO "serverAlertDestinations" ("label", "destinationType", "url", "email", "secret", "minSeverity", "enabled", "createdAt") VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING "destinationId"`,
			d.Label, d.DestinationType, d.Url, d.Email, d.Secret, d.MinSeverity, d.Enabled, d.CreatedAt).Scan(&d.Id); err != nil {
			fail(http.StatusInternalServerError, err.Error())
			return
		}
		writeAdminJSON(w, http.StatusOK, serverAlertDestinationJSON(d))

	case len(parts) == 2 && parts[0] == "destinations" && r.Method == http.MethodPut:
		d := controller.getServerAlertDestination(id)
		if d == nil {
			fail(http.StatusNotFound, "not found")
			return
		}
		var req serverAlertDestinationRequest
		if !decode(&req) {
			return
		}
		if req.DestinationType != "" && !strings.EqualFold(req.DestinationType, d.DestinationType) && req.Secret == nil {
			d.Secret = ""
		}
		if err := req.apply(d); err != nil {
			fail(http.StatusBadRequest, err.Error())
			return
		}
		if _, err := db.Exec(`UPDATE "serverAlertDestinations" SET "label"=$1, "destinationType"=$2, "url"=$3, "email"=$4, "secret"=$5, "minSeverity"=$6, "enabled"=$7 WHERE "destinationId"=$8`,
			d.Label, d.DestinationType, d.Url, d.Email, d.Secret, d.MinSeverity, d.Enabled, d.Id); err != nil {
			fail(http.StatusInternalServerError, err.Error())
			return
		}
		writeAdminJSON(w, http.StatusOK, serverAlertDestinationJSON(d))

	case len(parts) == 2 && parts[0] == "destinations" && r.Method == http.MethodDelete:
		if _, err := db.Exec(`DELETE FROM "serverAlertDestinations" WHERE "destinationId"=$1`, id); err != nil {
			fail(http.StatusInternalServerError, err.Error())
			return
		}
		writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true})

	case len(parts) == 3 && parts[0] == "destinations" && parts[2] == "test" && r.Method == http.MethodPost:
		d := controller.getServerAlertDestination(id)
		if d == nil {
			fail(http.StatusNotFound, "not found")
			return
		}
		p := controller.serverAlertFromSystemAlert("test", "info", "Test alert", "This is a test from ThinLine Radio server alerts.", "{}", time.Now().UnixMilli())
		if err := controller.deliverServerAlert(d, p); err != nil {
			fail(http.StatusBadGateway, err.Error())
			return
		}
		writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true})

	case len(parts) == 1 && parts[0] == "keys" && r.Method == http.MethodPost:
		var req struct {
			Label string `json:"label"`
		}
		if !decode(&req) {
			return
		}
		req.Label = strings.TrimSpace(req.Label)
		if req.Label == "" {
			fail(http.StatusBadRequest, "Name is required")
			return
		}
		raw := make([]byte, 24)
		if _, err := rand.Read(raw); err != nil {
			fail(http.StatusInternalServerError, "could not create key")
			return
		}
		plain := serverAlertKeyPrefix + hex.EncodeToString(raw)
		k := &ServerAlertKey{Label: req.Label, Prefix: plain[:len(serverAlertKeyPrefix)+6], Enabled: true, CreatedAt: time.Now().UnixMilli()}
		if err := db.QueryRow(`INSERT INTO "serverAlertKeys" ("label", "prefix", "keyHash", "enabled", "createdAt") VALUES ($1, $2, $3, true, $4) RETURNING "keyId"`,
			k.Label, k.Prefix, hashServerAlertKey(plain), k.CreatedAt).Scan(&k.Id); err != nil {
			fail(http.StatusInternalServerError, err.Error())
			return
		}
		writeAdminJSON(w, http.StatusOK, map[string]any{"key": k, "plaintext": plain})

	case len(parts) == 2 && parts[0] == "keys" && r.Method == http.MethodPut:
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if !decode(&req) {
			return
		}
		if _, err := db.Exec(`UPDATE "serverAlertKeys" SET "enabled"=$1 WHERE "keyId"=$2`, req.Enabled, id); err != nil {
			fail(http.StatusInternalServerError, err.Error())
			return
		}
		writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true})

	case len(parts) == 2 && parts[0] == "keys" && r.Method == http.MethodDelete:
		if _, err := db.Exec(`DELETE FROM "serverAlertKeys" WHERE "keyId"=$1`, id); err != nil {
			fail(http.StatusInternalServerError, err.Error())
			return
		}
		writeAdminJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		fail(http.StatusNotFound, "not found")
	}
}

type serverAlertIngestRequest struct {
	Source    string          `json:"source"`
	Severity  string          `json:"severity"`
	Title     string          `json:"title"`
	Message   string          `json:"message"`
	Timestamp json.RawMessage `json:"timestamp"`
	Metadata  map[string]any  `json:"metadata"`
}

func serverAlertKeyFromRequest(r *http.Request) string {
	if k := strings.TrimSpace(r.Header.Get("X-API-Key")); k != "" {
		return k
	}
	if auth := r.Header.Get("Authorization"); len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

// parseServerAlertTimestamp accepts unix seconds, unix milliseconds or RFC 3339.
func parseServerAlertTimestamp(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Now().UnixMilli(), true
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		if n <= 0 {
			return 0, false
		}
		if n < 1e11 {
			return int64(n * 1000), true
		}
		return int64(n), true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(s)); err == nil {
			return t.UnixMilli(), true
		}
	}
	return 0, false
}

func allowServerAlertRequest(keyId uint64) bool {
	serverAlertRateMu.Lock()
	defer serverAlertRateMu.Unlock()
	now := time.Now()
	b := serverAlertRate[keyId]
	if b == nil || now.Sub(b.windowStart) >= time.Minute {
		serverAlertRate[keyId] = &publicApiRateBucket{windowStart: now, count: 1}
		return true
	}
	if b.count >= serverAlertRatePerMinute {
		return false
	}
	b.count++
	return true
}

// ServerAlertIngestHandler lets outside monitors post an alert:
// POST /api/server-alerts with "Authorization: Bearer tla_…" (or X-API-Key).
func (api *Api) ServerAlertIngestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}
	controller := api.Controller
	key := serverAlertKeyFromRequest(r)
	if !strings.HasPrefix(key, serverAlertKeyPrefix) {
		writeAdminJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid alert key"})
		return
	}
	var keyId uint64
	var label string
	var enabled bool
	if err := controller.Database.Sql.QueryRow(`SELECT "keyId", "label", "enabled" FROM "serverAlertKeys" WHERE "keyHash"=$1`, hashServerAlertKey(key)).Scan(&keyId, &label, &enabled); err != nil || !enabled {
		writeAdminJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid alert key"})
		return
	}
	if !allowServerAlertRequest(keyId) {
		w.Header().Set("Retry-After", "60")
		writeAdminJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many alerts; limit is 30 a minute per key"})
		return
	}

	var req serverAlertIngestRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "body must be JSON"})
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		writeAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "message is required"})
		return
	}
	severity, ok := normalizeServerAlertSeverity(req.Severity)
	if !ok {
		writeAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "severity must be info, warning, error or critical"})
		return
	}
	ts, ok := parseServerAlertTimestamp(req.Timestamp)
	if !ok {
		writeAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "timestamp must be unix seconds, unix milliseconds or RFC 3339"})
		return
	}
	if len(req.Metadata) > serverAlertMaxMetaKeys {
		writeAdminJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("metadata can have at most %d keys", serverAlertMaxMetaKeys)})
		return
	}
	source := truncateRunes(strings.TrimSpace(req.Source), 100)
	if source == "" {
		source = label
	}
	title := truncateRunes(strings.TrimSpace(req.Title), serverAlertMaxTitle)
	if title == "" {
		title = source
	}
	data := &SystemAlertData{
		Source:     source,
		Metadata:   req.Metadata,
		AlertKey:   label,
		ReportedAt: ts,
	}
	if err := controller.CreateSystemAlert("external", severity, title, truncateRunes(req.Message, serverAlertMaxMessage), data, 0); err != nil {
		writeAdminJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save alert"})
		return
	}
	_, _ = controller.Database.Sql.Exec(`UPDATE "serverAlertKeys" SET "lastUsedAt"=$1 WHERE "keyId"=$2`, time.Now().UnixMilli(), keyId)
	writeAdminJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// checkDiskSpace raises a disk_space_low alert when free space on the base
// folder's disk drops below the threshold, repeats it daily while low, and
// clears it once space recovers.
func (controller *Controller) checkDiskSpace() {
	pct := controller.Options.DiskSpaceAlertPercent
	if pct == 0 || !controller.Options.SystemHealthAlertsEnabled || controller.Config == nil {
		return
	}
	path := controller.Config.BaseDir
	if path == "" {
		path = "."
	}
	free, total, err := diskUsage(path)
	if err != nil || total == 0 {
		return
	}
	freePct := float64(free) * 100 / float64(total)

	diskAlertMu.Lock()
	defer diskAlertMu.Unlock()
	if freePct >= float64(pct) {
		if diskAlertActive {
			diskAlertActive = false
			controller.DismissAlertsByType("disk_space_low")
		}
		return
	}
	if diskAlertActive && time.Since(diskAlertLastAt) < diskSpaceAlertRepeatAfter {
		return
	}
	severity := "warning"
	if freePct < float64(pct)/2 {
		severity = "critical"
	}
	msg := fmt.Sprintf("Only %.1f%% free (%s of %s) on the disk holding %s. Alerts start below %d%%.", freePct, formatDiskBytes(free), formatDiskBytes(total), path, pct)
	if err := controller.CreateSystemAlert("disk_space_low", severity, "Disk space low", msg, &SystemAlertData{Service: "disk", Threshold: int(pct)}, 0); err == nil {
		diskAlertActive = true
		diskAlertLastAt = time.Now()
	}
}

func (controller *Controller) startDiskSpaceMonitor(stop <-chan struct{}) {
	go func() {
		controller.checkDiskSpace()
		ticker := time.NewTicker(diskSpaceCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				controller.checkDiskSpace()
			case <-stop:
				return
			}
		}
	}()
}

func formatDiskBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
