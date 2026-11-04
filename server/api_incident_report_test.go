// Copyright (C) 2026 Thinline Dynamic Solutions

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIncidentReportLimiter(t *testing.T) {
	l := &incidentReportLimiter{hits: map[string][]time.Time{}}
	now := time.Now()
	for i := 0; i < incidentReportsPerHour; i++ {
		if !l.allow("7", now) {
			t.Fatalf("report %d refused under the cap", i+1)
		}
	}
	if l.allow("7", now) {
		t.Fatal("report over the hourly cap was allowed")
	}
	if !l.allow("8", now) {
		t.Fatal("another user was limited")
	}
	if !l.allow("7", now.Add(61*time.Minute)) {
		t.Fatal("cap did not reset after an hour")
	}
}

func TestPostIncidentCorrection(t *testing.T) {
	var got incidentCorrectionReport
	var auth string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/corrections" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer gw.Close()

	lat, lon := 40.6, -81.1
	report := incidentCorrectionReport{
		Kind:      "location",
		CallID:    42,
		Original:  incidentCorrectionPin{Address: "123 MAIN ST", Nature: "FIRE"},
		Corrected: incidentCorrectionPin{Address: "123 MAIN ST, MALVERN", Lat: &lat, Lon: &lon},
		Bounds:    incidentCorrectionBounds{Viewbox: "-81.3,40.8,-80.9,40.4", RadiusMi: 15},
	}
	if err := postIncidentCorrection(gw.URL+"/", "k1", report); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer k1" || got.CallID != 42 || got.Corrected.Lat == nil || *got.Corrected.Lat != lat || got.Original.Lat != nil {
		t.Fatalf("gateway got auth=%q report=%+v", auth, got)
	}

	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "No active geocoding subscription", http.StatusPaymentRequired)
	}))
	defer refuse.Close()
	if err := postIncidentCorrection(refuse.URL, "k1", report); err == nil {
		t.Fatal("a refused correction must return an error")
	}
}
