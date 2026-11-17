// Copyright (C) 2025 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>

package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ThinLine Transcription: hosted Whisper billed against the prepaid credit of
// this server's relay API key. Balance, top-ups and auto-recharge live on the
// relay; the transcription host rejects calls with 402 once credit runs out.
const (
	thinlineTranscriptionURL = "https://transcribe.thinlineradio.com"
	thinlineNoCreditPause    = 2 * time.Minute
	thinlineBadKeyPause      = 10 * time.Minute
)

type ThinLineTranscription struct {
	*WhisperAPITranscription
	apiKey string

	mu          sync.Mutex
	pausedUntil time.Time
	pauseReason string
}

func NewThinLineTranscription(apiKey string, timeoutSeconds int) *ThinLineTranscription {
	apiKey = strings.TrimSpace(apiKey)
	return &ThinLineTranscription{
		WhisperAPITranscription: NewWhisperAPITranscription(&WhisperAPIConfig{
			BaseURL:        thinlineTranscriptionURL,
			APIKey:         apiKey,
			Model:          "whisper-1",
			TimeoutSeconds: timeoutSeconds,
		}),
		apiKey: apiKey,
	}
}

func (t *ThinLineTranscription) Transcribe(audio []byte, options TranscriptionOptions) (*TranscriptionResult, error) {
	t.mu.Lock()
	if time.Now().Before(t.pausedUntil) {
		reason := t.pauseReason
		t.mu.Unlock()
		return nil, fmt.Errorf("ThinLine Transcription paused: %s", reason)
	}
	t.mu.Unlock()

	result, err := t.WhisperAPITranscription.Transcribe(audio, options)
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "status 402"):
			t.pause(thinlineNoCreditPause, "prepaid balance is empty; add credit under Options > Transcription")
		case strings.Contains(msg, "status 401"):
			t.pause(thinlineBadKeyPause, "relay API key was rejected")
		}
	}
	return result, err
}

func (t *ThinLineTranscription) pause(d time.Duration, reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if time.Now().After(t.pausedUntil) {
		log.Printf("ThinLine Transcription: %s; pausing for %s", reason, d)
	}
	t.pausedUntil = time.Now().Add(d)
	t.pauseReason = reason
}

func (t *ThinLineTranscription) IsAvailable() bool {
	return t.apiKey != ""
}

func (t *ThinLineTranscription) GetName() string {
	return "ThinLine Transcription"
}

// RelayTranscriptionHandler proxies /api/admin/relay-transcription/{account,topup,auto-recharge}
// to the relay with this server's API key, so the key never reaches the browser.
func (admin *Admin) RelayTranscriptionHandler(w http.ResponseWriter, r *http.Request) {
	if !admin.ValidateToken(admin.GetAuthorization(r)) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/admin/relay-transcription/")
	allowed := map[string]string{
		"account":       http.MethodGet,
		"topup":         http.MethodPost,
		"auto-recharge": http.MethodPost,
	}
	method, ok := allowed[action]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method != method {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	apiKey := strings.TrimSpace(admin.Controller.Options.RelayServerAPIKey)
	if apiKey == "" {
		respondRelayAccountError(w, http.StatusBadRequest, "Relay server API key is not configured")
		return
	}

	var body io.Reader
	if method == http.MethodPost {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err != nil {
			respondRelayAccountError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, getRelayServerURL()+"/api/transcription/"+action, body)
	if err != nil {
		respondRelayAccountError(w, http.StatusInternalServerError, err.Error())
		return
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := admin.Controller.RelayAccountAccessTokenSnapshot(); token != "" {
		req.Header.Set("X-Account-Token", token)
	}
	resp, err := (&http.Client{Timeout: relayAccountHTTPTimeout}).Do(req)
	if err != nil {
		respondRelayAccountError(w, http.StatusBadGateway, "Could not reach the relay server")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, io.LimitReader(resp.Body, 1<<20))
}
