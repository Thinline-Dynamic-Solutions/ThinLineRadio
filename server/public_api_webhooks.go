// Copyright (C) 2026 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type publicApiAlertPayload struct {
	Source         string                 `json:"source"`
	Severity       string                 `json:"severity"`
	Message        string                 `json:"message"`
	Timestamp      int64                  `json:"timestamp"`
	Metadata       map[string]interface{} `json:"metadata"`
	Transcription  string                 `json:"transcription,omitempty"`
}

// DispatchAlertWebhooks fans out outbound alert webhooks for users who have the
// alertWebhooks capability. Failures are logged and never block the alert path.
func (svc *PublicApiService) DispatchAlertWebhooks(userIds []uint64, alertType string, call *Call, systemLabel, talkgroupLabel, toneSetName string, keywords []string) {
	if svc == nil || svc.controller == nil || len(userIds) == 0 || call == nil {
		return
	}
	seen := map[uint64]bool{}
	for _, userId := range userIds {
		if seen[userId] {
			continue
		}
		seen[userId] = true
		user := svc.controller.Users.GetUserById(userId)
		if user == nil || !user.HasPublicApiCapability("alertWebhooks") {
			continue
		}
		if !svc.controller.userHasAccess(user, call) {
			continue
		}
		hooks, err := svc.listWebhooksForUser(userId)
		if err != nil || len(hooks) == 0 {
			continue
		}
		payload := svc.buildAlertPayload(alertType, call, systemLabel, talkgroupLabel, toneSetName, keywords)
		for _, hook := range hooks {
			if !hook.Enabled {
				continue
			}
			if !webhookAlertTypeMatches(hook.AlertTypes, alertType) {
				continue
			}
			h := hook
			go svc.deliverWebhook(h, payload)
		}
	}
}

func webhookAlertTypeMatches(types []string, alertType string) bool {
	if len(types) == 0 {
		return true
	}
	for _, t := range types {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "*" || t == "all" || t == strings.ToLower(alertType) {
			return true
		}
	}
	return false
}

func (svc *PublicApiService) buildAlertPayload(alertType string, call *Call, systemLabel, talkgroupLabel, toneSetName string, keywords []string) publicApiAlertPayload {
	severity := "info"
	switch strings.ToLower(alertType) {
	case "pre-alert", "tone", "tone+keyword":
		severity = "critical"
	case "keyword":
		severity = "warning"
	case "transcript":
		severity = "info"
	}

	msg := strings.TrimSpace(systemLabel)
	if talkgroupLabel != "" {
		if msg != "" {
			msg += " / "
		}
		msg += talkgroupLabel
	}
	if toneSetName != "" {
		msg += " — " + toneSetName
	}
	if msg == "" {
		msg = "ThinLine Radio alert"
	}

	host := ""
	if svc.controller.Options != nil {
		host = strings.TrimSpace(svc.controller.Options.BaseUrl)
	}

	sysRef := uint(0)
	tgRef := uint(0)
	callId := uint64(0)
	ts := time.Now().UnixMilli()
	transcript := ""
	if call != nil {
		callId = call.Id
		if !call.Timestamp.IsZero() {
			ts = call.Timestamp.UnixMilli()
		}
		transcript = call.Transcript
		if call.System != nil {
			sysRef = call.System.SystemRef
			if systemLabel == "" {
				systemLabel = call.System.Label
			}
		}
		if call.Talkgroup != nil {
			tgRef = call.Talkgroup.TalkgroupRef
			if talkgroupLabel == "" {
				talkgroupLabel = call.Talkgroup.Label
			}
		}
	}

	meta := map[string]interface{}{
		"host":           host,
		"systemRef":      sysRef,
		"systemLabel":    systemLabel,
		"talkgroupRef":   tgRef,
		"talkgroupLabel": talkgroupLabel,
		"alertType":      alertType,
		"callId":         callId,
	}
	if toneSetName != "" {
		meta["toneSetName"] = toneSetName
	}
	if len(keywords) > 0 {
		meta["keywords"] = keywords
	}

	return publicApiAlertPayload{
		Source:        "thinline-radio",
		Severity:      severity,
		Message:       msg,
		Timestamp:     ts,
		Metadata:      meta,
		Transcription: transcript,
	}
}

func (svc *PublicApiService) deliverWebhook(hook PublicApiWebhook, payload publicApiAlertPayload) {
	defer func() {
		if r := recover(); r != nil {
			svc.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("public api webhook panic: %v", r))
		}
	}()

	switch strings.ToLower(hook.DestinationType) {
	case "slack":
		svc.postJSON(hook.Url, map[string]any{
			"text": fmt.Sprintf("[%s] %s", strings.ToUpper(payload.Severity), payload.Message),
			"blocks": []map[string]any{
				{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": fmt.Sprintf("*%s*\n%s", payload.Message, payload.Transcription)}},
			},
		})
	case "discord":
		content := fmt.Sprintf("**[%s]** %s", strings.ToUpper(payload.Severity), payload.Message)
		if payload.Transcription != "" {
			content += "\n" + payload.Transcription
		}
		if len(content) > 1900 {
			content = content[:1900] + "…"
		}
		svc.postJSON(hook.Url, map[string]any{"content": content})
	case "pagerduty":
		routingKey := strings.TrimSpace(hook.Secret)
		if routingKey == "" {
			svc.controller.Logs.LogEvent(LogLevelWarn, "public api pagerduty webhook missing secret/routing key")
			return
		}
		svc.postJSON("https://events.pagerduty.com/v2/enqueue", map[string]any{
			"routing_key": routingKey,
			"event_action": "trigger",
			"payload": map[string]any{
				"summary":   payload.Message,
				"severity":  payload.Severity,
				"source":    "thinline-radio",
				"timestamp": time.UnixMilli(payload.Timestamp).UTC().Format(time.RFC3339),
				"custom_details": payload,
			},
		})
	case "email":
		if svc.controller.EmailService == nil || strings.TrimSpace(hook.Email) == "" {
			return
		}
		body := fmt.Sprintf("<p><strong>%s</strong></p><p>%s</p>", htmlEscape(payload.Message), htmlEscape(payload.Transcription))
		fromName := "ThinLine Radio"
		fromEmail := ""
		if svc.controller.Options != nil {
			if svc.controller.Options.EmailSmtpFromName != "" {
				fromName = svc.controller.Options.EmailSmtpFromName
			}
			fromEmail = svc.controller.Options.EmailSmtpFromEmail
			if fromEmail == "" {
				fromEmail = svc.controller.Options.Email
			}
		}
		if err := svc.controller.EmailService.sendEmail(fromName, fromEmail, hook.Email, "[Alert] "+payload.Message, body); err != nil {
			svc.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("public api email webhook failed: %v", err))
		}
	default: // custom
		headers := map[string]string{}
		if s := strings.TrimSpace(hook.Secret); s != "" {
			headers["X-Webhook-Secret"] = s
		}
		svc.postJSONWithHeaders(hook.Url, payload, headers)
	}
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func (svc *PublicApiService) postJSON(url string, body any) {
	svc.postJSONWithHeaders(url, body, nil)
}

func (svc *PublicApiService) postJSONWithHeaders(url string, body any, headers map[string]string) {
	url = strings.TrimSpace(url)
	if url == "" {
		return
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ThinLineRadio-PublicAPI/1.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		svc.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("public api webhook POST failed: %v", err))
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		svc.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("public api webhook POST status %d for %s", resp.StatusCode, url))
	}
}

// PublicApiKeysListHandler lists all public API keys for admin tracking.
func (admin *Admin) PublicApiKeysListHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	rows, err := admin.Controller.Database.Sql.Query(
		`SELECT k."keyId", k."userId", k."name", k."keyPrefix", k."createdAt", k."lastUsedAt", k."disabled", k."requestCount",
		        COALESCE(u."email", ''), COALESCE(u."firstName", ''), COALESCE(u."lastName", '')
		 FROM "publicApiKeys" k
		 LEFT JOIN "users" u ON u."userId" = k."userId"
		 ORDER BY k."lastUsedAt" DESC, k."createdAt" DESC`,
	)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id, userId, createdAt, lastUsedAt, requestCount int64
			name, prefix, email, firstName, lastName        string
			disabled                                        bool
		)
		if err := rows.Scan(&id, &userId, &name, &prefix, &createdAt, &lastUsedAt, &disabled, &requestCount, &email, &firstName, &lastName); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"id": id, "userId": userId, "name": name, "keyPrefix": prefix,
			"createdAt": createdAt, "lastUsedAt": lastUsedAt, "disabled": disabled,
			"requestCount": requestCount, "email": email, "firstName": firstName, "lastName": lastName,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": out})
}

// PublicApiKeyActionHandler disables a public API key: POST .../keys/{id}/disable
func (admin *Admin) PublicApiKeyActionHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/public-api/keys/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid id"})
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	if r.Method == http.MethodPost && (action == "disable" || action == "revoke") {
		res, err := admin.Controller.Database.Sql.Exec(
			`UPDATE "publicApiKeys" SET "disabled" = true WHERE "keyId" = $1`, id,
		)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed"})
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}
