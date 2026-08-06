// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"encoding/json"
	"strings"
)

const copilotMaxToolResultBytes = 48_000

var copilotSecretKeyNames = map[string]bool{
	"apikey": true, "apiKey": true, "api_key": true, "key": true,
	"password": true, "adminPassword": true, "smtpPassword": true, "emailSmtpPassword": true,
	"radioReferencePassword": true, "secret": true, "token": true, "fcmToken": true,
	"stripeSecretKey": true, "stripeWebhookSecret": true, "assemblyAIKey": true,
	"openaiApiKey": true, "openAIApiKey": true, "apiKeyValue": true,
	"deviceToken": true, "refreshToken": true, "accessToken": true,
	"pin": true, // user PINs
}

// copilotRedactSecrets walks JSON-compatible values and redacts secret-looking keys.
func copilotRedactSecrets(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if copilotSecretKeyNames[k] || strings.Contains(strings.ToLower(k), "password") ||
				strings.HasSuffix(strings.ToLower(k), "apikey") ||
				strings.HasSuffix(strings.ToLower(k), "secret") {
				if s, ok := val.(string); ok && s != "" {
					out[k] = "[redacted]"
				} else if val != nil && val != "" {
					out[k] = "[redacted]"
				} else {
					out[k] = val
				}
				continue
			}
			out[k] = copilotRedactSecrets(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = copilotRedactSecrets(item)
		}
		return out
	default:
		return v
	}
}

// copilotSanitizeToolResult redacts secrets and caps payload size for the LLM.
func copilotSanitizeToolResult(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return `{"ok":true}`
	}
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		if len(raw) > copilotMaxToolResultBytes {
			return raw[:copilotMaxToolResultBytes] + "…[truncated]"
		}
		return raw
	}
	parsed = copilotRedactSecrets(parsed)
	b, err := json.Marshal(parsed)
	if err != nil {
		return `{"error":"failed to marshal sanitized result"}`
	}
	if len(b) <= copilotMaxToolResultBytes {
		return string(b)
	}
	// Prefer structured truncation for objects.
	if m, ok := parsed.(map[string]any); ok {
		summary := map[string]any{
			"truncated": true,
			"totalBytes": len(b),
			"keys":       mapKeys(m),
			"hint":       "Result was too large; re-query a narrower section or use filters.",
		}
		if msg, ok := m["error"]; ok {
			summary["error"] = msg
		}
		if okVal, ok := m["ok"]; ok {
			summary["ok"] = okVal
		}
		if applied, ok := m["applied"]; ok {
			summary["applied"] = applied
		}
		if action, ok := m["action"]; ok {
			summary["action"] = action
		}
		sb, _ := json.Marshal(summary)
		return string(sb)
	}
	return string(b[:copilotMaxToolResultBytes]) + "…[truncated]"
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// copilotOptionsPublicView returns Options without secret fields.
func copilotOptionsPublicView(opts *Options) map[string]any {
	if opts == nil {
		return map[string]any{}
	}
	b, err := json.Marshal(opts)
	if err != nil {
		return map[string]any{"error": "failed to marshal options"}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{"error": "failed to parse options"}
	}
	redacted, _ := copilotRedactSecrets(m).(map[string]any)
	return redacted
}

// copilotApikeysPublicView strips raw key material.
func copilotApikeysPublicView(list []*Apikey) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, a := range list {
		if a == nil {
			continue
		}
		keyPreview := ""
		if a.Key != "" {
			if len(a.Key) > 8 {
				keyPreview = a.Key[:4] + "…" + a.Key[len(a.Key)-4:]
			} else {
				keyPreview = "[set]"
			}
		}
		out = append(out, map[string]any{
			"id":                       a.Id,
			"ident":                    a.Ident,
			"disabled":                 a.Disabled,
			"order":                    a.Order,
			"keyPreview":               keyPreview,
			"systems":                  a.Systems,
			"noAudioAlertsEnabled":     a.NoAudioAlertsEnabled,
			"noAudioThresholdMinutes":  a.NoAudioThresholdMinutes,
			"lastCallAt":               a.LastCallAt,
		})
	}
	return out
}
