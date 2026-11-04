// Copyright (C) 2026 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServerAlertSeverity(t *testing.T) {
	for in, want := range map[string]string{"": "warning", "WARN": "warning", "Critical": "critical", " info ": "info"} {
		got, ok := normalizeServerAlertSeverity(in)
		if !ok || got != want {
			t.Errorf("normalize(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if _, ok := normalizeServerAlertSeverity("panic"); ok {
		t.Error("unknown severity accepted")
	}
	if !serverAlertSeverityAtLeast("critical", "warning") || serverAlertSeverityAtLeast("info", "warning") || !serverAlertSeverityAtLeast("error", "error") {
		t.Error("severity ordering wrong")
	}
}

func TestParseServerAlertTimestamp(t *testing.T) {
	cases := map[string]int64{
		`1762214400`:             1762214400000,
		`1762214400123`:          1762214400123,
		`"2025-11-04T00:00:00Z"`: 1762214400000,
	}
	for raw, want := range cases {
		got, ok := parseServerAlertTimestamp(json.RawMessage(raw))
		if !ok || got != want {
			t.Errorf("parse(%s) = %d, %v; want %d", raw, got, ok, want)
		}
	}
	if got, ok := parseServerAlertTimestamp(nil); !ok || time.Since(time.UnixMilli(got)) > time.Minute {
		t.Error("missing timestamp should default to now")
	}
	for _, raw := range []string{`"yesterday"`, `-5`, `true`} {
		if _, ok := parseServerAlertTimestamp(json.RawMessage(raw)); ok {
			t.Errorf("parse(%s) accepted", raw)
		}
	}
}

func TestServerAlertDestinationValidation(t *testing.T) {
	secret := "routing-key"
	ok := []serverAlertDestinationRequest{
		{Label: "Ops", DestinationType: "slack", Url: "https://hooks.slack.com/services/T/B/x"},
		{Label: "Ops", DestinationType: "discord", Url: "https://discord.com/api/webhooks/1/x"},
		{Label: "PD", DestinationType: "pagerduty", Secret: &secret, MinSeverity: "critical"},
		{Label: "Me", DestinationType: "email", Email: "owner@example.com"},
	}
	for _, req := range ok {
		if err := req.apply(&ServerAlertDestination{}); err != nil {
			t.Errorf("%s rejected: %v", req.DestinationType, err)
		}
	}
	bad := []serverAlertDestinationRequest{
		{Label: "", DestinationType: "slack", Url: "https://hooks.slack.com/services/T/B/x"},
		{Label: "x", DestinationType: "slack", Url: "https://example.com/hook"},
		{Label: "x", DestinationType: "custom", Url: "http://example.com/hook"},
		{Label: "x", DestinationType: "custom", Url: "https://127.0.0.1/hook"},
		{Label: "x", DestinationType: "custom", Url: "https://10.0.0.5/hook"},
		{Label: "x", DestinationType: "pagerduty"},
		{Label: "x", DestinationType: "email", Email: "a@b.com, c@d.com"},
		{Label: "x", DestinationType: "sms"},
		{Label: "x", DestinationType: "slack", Url: "https://hooks.slack.com/x", MinSeverity: "loud"},
	}
	for _, req := range bad {
		if err := req.apply(&ServerAlertDestination{}); err == nil {
			t.Errorf("accepted bad destination %+v", req)
		}
	}
}

func TestServerAlertDestinationJSONHidesSecret(t *testing.T) {
	out := serverAlertDestinationJSON(&ServerAlertDestination{Secret: "s3cret"})
	if out.Secret != "" || !out.HasSecret {
		t.Fatalf("secret leaked or flag missing: %+v", out)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "s3cret") {
		t.Fatal("secret in JSON")
	}
}

func TestServerAlertPayloadFromExternalAlert(t *testing.T) {
	controller := &Controller{Options: &Options{BaseUrl: "https://radio.example.com"}}
	data, _ := json.Marshal(&SystemAlertData{Source: "watchdog", ReportedAt: 1762214400000, Metadata: map[string]any{"host": "tr-1", "alertType": "spoof"}})
	p := controller.serverAlertFromSystemAlert("external", "error", "Recorder down", "trunk-recorder stopped", string(data), 42)
	if p.Source != "watchdog" || p.Severity != "error" || p.Title != "Recorder down" || p.Timestamp != 1762214400000 {
		t.Fatalf("payload = %+v", p)
	}
	if p.Metadata["host"] != "tr-1" || p.Metadata["alertType"] != "external" || p.Metadata["server"] != "https://radio.example.com" {
		t.Fatalf("metadata = %v", p.Metadata)
	}
	if _, nested := p.Metadata["metadata"]; nested {
		t.Fatal("metadata should be flattened")
	}
}

func TestBuildServerAlertRequest(t *testing.T) {
	p := serverAlertPayload{Source: "watchdog", Severity: "critical", Title: "Recorder down", Message: "stopped", Timestamp: 1762214400000}

	_, body, _, _ := buildServerAlertRequest(&ServerAlertDestination{DestinationType: "slack", Url: "https://hooks.slack.com/x"}, p)
	if body.(map[string]any)["text"] != "[CRITICAL] Recorder down\nstopped" {
		t.Errorf("slack body = %v", body)
	}

	long := p
	long.Message = strings.Repeat("é", 3000)
	_, body, _, _ = buildServerAlertRequest(&ServerAlertDestination{DestinationType: "discord", Url: "https://discord.com/x"}, long)
	if n := len([]rune(body.(map[string]any)["content"].(string))); n != 1901 {
		t.Errorf("discord content has %d runes", n)
	}

	url, body, _, err := buildServerAlertRequest(&ServerAlertDestination{DestinationType: "pagerduty", Secret: "rk"}, p)
	pd := body.(map[string]any)
	if err != nil || url != "https://events.pagerduty.com/v2/enqueue" || pd["routing_key"] != "rk" ||
		pd["payload"].(map[string]any)["severity"] != "critical" || pd["payload"].(map[string]any)["timestamp"] != "2025-11-04T00:00:00Z" {
		t.Errorf("pagerduty = %s %v %v", url, body, err)
	}

	_, body, headers, _ := buildServerAlertRequest(&ServerAlertDestination{DestinationType: "custom", Url: "https://x.example", Secret: "s"}, p)
	if body.(serverAlertPayload).Source != "watchdog" || headers["X-Webhook-Secret"] != "s" {
		t.Errorf("custom = %v %v", body, headers)
	}
}

func TestServerAlertIngestRejectsMissingKey(t *testing.T) {
	api := &Api{Controller: &Controller{}}
	for _, auth := range []string{"", "Bearer tlr_userkey", "Bearer somepin"} {
		r := httptest.NewRequest(http.MethodPost, "/api/server-alerts", strings.NewReader(`{"message":"x"}`))
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		api.ServerAlertIngestHandler(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("auth %q: status %d", auth, w.Code)
		}
	}
	w := httptest.NewRecorder()
	api.ServerAlertIngestHandler(w, httptest.NewRequest(http.MethodGet, "/api/server-alerts", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status %d", w.Code)
	}
}

func TestServerAlertRateLimit(t *testing.T) {
	const key = 999999
	defer func() {
		serverAlertRateMu.Lock()
		delete(serverAlertRate, key)
		serverAlertRateMu.Unlock()
	}()
	for i := 0; i < serverAlertRatePerMinute; i++ {
		if !allowServerAlertRequest(key) {
			t.Fatalf("request %d refused", i+1)
		}
	}
	if allowServerAlertRequest(key) {
		t.Fatal("limit not enforced")
	}
}

func TestServerAlertRoutesAreSystemAdminOnly(t *testing.T) {
	for _, path := range []string{"/api/admin/server-alerts", "/api/admin/server-alerts/keys", "/api/admin/server-alerts/destinations/1/test"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
			if rule := findAdminRouteRule(method, path); rule != nil {
				t.Errorf("%s %s is grantable to admin roles", method, path)
			}
		}
	}
}

func TestDiskUsageReadsBaseDir(t *testing.T) {
	free, total, err := diskUsage(t.TempDir())
	if err != nil || total == 0 || free > total {
		t.Fatalf("diskUsage = %d, %d, %v", free, total, err)
	}
}
