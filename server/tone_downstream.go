// Copyright (C) 2025 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT EVEN THE IMPLIED WARRANTY OF MERCHANTABILITY or FITNESS
// FOR A PARTICULAR PURPOSE.  See the GNU General Public License for
// more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// ToneAlertMetadata is the JSON payload sent in the "metadata" form field
// alongside the audio file when forwarding a tone alert downstream.
type ToneAlertMetadata struct {
	CallId         uint64 `json:"callId"`
	System         uint   `json:"system"`
	SystemLabel    string `json:"systemLabel"`
	Talkgroup      uint   `json:"talkgroup"`
	TalkgroupLabel string `json:"talkgroupLabel"`
	Timestamp      int64  `json:"timestamp"` // Unix milliseconds
	ToneSetId      string `json:"toneSetId"`
	ToneSetLabel   string `json:"toneSetLabel"`
	Transcript     string `json:"transcript"`
}

// sendToneAlertDownstream forwards a tone alert to an external TonesToActive server.
// It sends a multipart/form-data POST containing the audio file and a JSON metadata blob.
// The API key is sent in the X-API-Key request header.
//
// destination — the full URL of the receiving endpoint (e.g. https://example.com/api/tone-alert)
// apiKey      — secret sent in X-API-Key header for authentication
// call        — the triggering call (supplies audio, system/talkgroup info, transcript)
// toneSet     — the matched tone set
func sendToneAlertDownstream(controller *Controller, destination string, apiKey string, call *Call, toneSet *ToneSet) error {
	if destination == "" {
		return fmt.Errorf("tone_downstream: destination URL is empty")
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// ── audio file ──────────────────────────────────────────────────────────
	if len(call.Audio) > 0 {
		filename := call.AudioFilename
		if filename == "" {
			filename = "audio.m4a"
		}
		w, err := mw.CreateFormFile("audio", filename)
		if err != nil {
			return fmt.Errorf("tone_downstream: create audio field: %w", err)
		}
		if _, err = w.Write(call.Audio); err != nil {
			return fmt.Errorf("tone_downstream: write audio: %w", err)
		}

		// audio name / type helpers for receivers that prefer separate fields
		if w2, err := mw.CreateFormField("audioName"); err == nil {
			_, _ = w2.Write([]byte(filename))
		}
		if w2, err := mw.CreateFormField("audioType"); err == nil {
			mime := call.AudioMime
			if mime == "" {
				mime = "audio/mp4"
			}
			_, _ = w2.Write([]byte(mime))
		}
	}

	// ── JSON metadata ────────────────────────────────────────────────────────
	systemRef := uint(0)
	systemLabel := ""
	if call.System != nil {
		systemRef = call.System.SystemRef
		systemLabel = call.System.Label
	}

	talkgroupRef := uint(0)
	talkgroupLabel := ""
	if call.Talkgroup != nil {
		talkgroupRef = call.Talkgroup.TalkgroupRef
		talkgroupLabel = call.Talkgroup.Label
	}

	meta := ToneAlertMetadata{
		CallId:         call.Id,
		System:         systemRef,
		SystemLabel:    systemLabel,
		Talkgroup:      talkgroupRef,
		TalkgroupLabel: talkgroupLabel,
		Timestamp:      call.Timestamp.UnixMilli(),
		ToneSetId:      toneSet.Id,
		ToneSetLabel:   toneSet.Label,
		Transcript:     call.Transcript,
	}

	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("tone_downstream: marshal metadata: %w", err)
	}

	if w, err := mw.CreateFormField("metadata"); err == nil {
		if _, err = w.Write(metaBytes); err != nil {
			return fmt.Errorf("tone_downstream: write metadata: %w", err)
		}
	} else {
		return fmt.Errorf("tone_downstream: create metadata field: %w", err)
	}

	if err := mw.Close(); err != nil {
		return fmt.Errorf("tone_downstream: close multipart writer: %w", err)
	}

	// ── HTTP POST ────────────────────────────────────────────────────────────
	req, err := http.NewRequest(http.MethodPost, destination, &buf)
	if err != nil {
		return fmt.Errorf("tone_downstream: build request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("tone_downstream: POST to %s: %w", destination, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("tone_downstream: server %s returned status %s", destination, resp.Status)
	}

	return nil
}

// syncToneSetsToDownstreams collects every tone set that has a downstream URL
// configured (per-tone-set or per-talkgroup channel) and pushes the full list
// to each unique TonesToActive server via POST /api/sync-tone-sets.
// Called once on startup after all system/talkgroup data is loaded.
func syncToneSetsToDownstreams(controller *Controller) {
	// Map: baseURL → { apiKey, []toneSetEntry }
	type toneSetEntry struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	type destInfo struct {
		apiKey   string
		toneSets []toneSetEntry
	}
	dests := map[string]*destInfo{}

	for _, sys := range controller.Systems.List {
		for _, tg := range sys.Talkgroups.List {
			// Per-tone-set downstreams
			for _, ts := range tg.ToneSets {
				if ts.DownstreamEnabled && ts.DownstreamURL != "" {
					base := strings.TrimSuffix(ts.DownstreamURL, "/api/tone-alert")
					base = strings.TrimRight(base, "/")
					if _, ok := dests[base]; !ok {
						dests[base] = &destInfo{apiKey: ts.DownstreamAPIKey}
					}
					dests[base].toneSets = append(dests[base].toneSets, toneSetEntry{
						ID:    ts.Id,
						Label: ts.Label,
					})
				}
			}
			// Per-channel (talkgroup-level) downstream
			if tg.ToneDownstreamEnabled && tg.ToneDownstreamURL != "" {
				base := strings.TrimSuffix(tg.ToneDownstreamURL, "/api/tone-alert")
				base = strings.TrimRight(base, "/")
				if _, ok := dests[base]; !ok {
					dests[base] = &destInfo{apiKey: tg.ToneDownstreamAPIKey}
				}
				for _, ts := range tg.ToneSets {
					dests[base].toneSets = append(dests[base].toneSets, toneSetEntry{
						ID:    ts.Id,
						Label: ts.Label,
					})
				}
			}
		}
	}

	for base, info := range dests {
		url := base + "/api/sync-tone-sets"
		payload, err := json.Marshal(map[string]any{"toneSets": info.toneSets})
		if err != nil {
			controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("sync-tone-sets: marshal: %v", err))
			continue
		}
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("sync-tone-sets: build request to %s: %v", url, err))
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if info.apiKey != "" {
			req.Header.Set("X-API-Key", info.apiKey)
		}
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("sync-tone-sets: POST to %s: %v", url, err))
			continue
		}
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("sync-tone-sets: %s returned %s", url, resp.Status))
		} else {
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("sync-tone-sets: pushed %d tone sets to %s", len(info.toneSets), url))
		}
	}
}

// dispatchToneDownstreams checks per-tone-set and per-channel downstream config
// and fires any enabled downstream in a goroutine per destination.
func dispatchToneDownstreams(controller *Controller, call *Call, toneSet *ToneSet) {
	// 1. Per-tone-set downstream
	if toneSet.DownstreamEnabled && toneSet.DownstreamURL != "" {
		go func() {
			logPrefix := fmt.Sprintf("tone_downstream[tone-set]: call=%d toneSet=%q", call.Id, toneSet.Label)
			if err := sendToneAlertDownstream(controller, toneSet.DownstreamURL, toneSet.DownstreamAPIKey, call, toneSet); err != nil {
				controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("%s → %s ERROR: %v", logPrefix, toneSet.DownstreamURL, err))
			} else {
				controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("%s → %s OK", logPrefix, toneSet.DownstreamURL))
			}
		}()
	}

	// 2. Per-channel downstream (all tone sets for this talkgroup)
	if call.Talkgroup != nil && call.Talkgroup.ToneDownstreamEnabled && call.Talkgroup.ToneDownstreamURL != "" {
		go func() {
			logPrefix := fmt.Sprintf("tone_downstream[channel]: call=%d talkgroup=%q toneSet=%q", call.Id, call.Talkgroup.Label, toneSet.Label)
			if err := sendToneAlertDownstream(controller, call.Talkgroup.ToneDownstreamURL, call.Talkgroup.ToneDownstreamAPIKey, call, toneSet); err != nil {
				controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("%s → %s ERROR: %v", logPrefix, call.Talkgroup.ToneDownstreamURL, err))
			} else {
				controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("%s → %s OK", logPrefix, call.Talkgroup.ToneDownstreamURL))
			}
		}()
	}
}
