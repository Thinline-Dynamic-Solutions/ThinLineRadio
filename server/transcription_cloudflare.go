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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// CloudflareTranscription implements TranscriptionProvider for Cloudflare Workers AI
type CloudflareTranscription struct {
	accountID  string
	apiToken   string
	model      string
	httpClient *http.Client
}

// CloudflareConfig contains configuration for Cloudflare Workers AI transcription
type CloudflareConfig struct {
	AccountID      string
	APIToken       string
	Model          string // e.g. "@cf/openai/whisper-large-v3-turbo"
	TimeoutSeconds int
}

// NewCloudflareTranscription creates a new Cloudflare Workers AI transcription provider
func NewCloudflareTranscription(config *CloudflareConfig) *CloudflareTranscription {
	const defaultTimeoutSeconds = 300
	timeoutSecs := config.TimeoutSeconds
	if timeoutSecs <= 0 {
		timeoutSecs = defaultTimeoutSeconds
	}
	timeout := time.Duration(timeoutSecs) * time.Second

	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		MaxConnsPerHost:     20,
		IdleConnTimeout:     90 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     false,
		DisableKeepAlives:     false,
	}

	model := config.Model
	if model == "" {
		model = "@cf/openai/whisper-large-v3-turbo"
	}

	return &CloudflareTranscription{
		accountID: config.AccountID,
		apiToken:  config.APIToken,
		model:     model,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
	}
}

// Transcribe transcribes audio using Cloudflare Workers AI
func (cf *CloudflareTranscription) Transcribe(audio []byte, options TranscriptionOptions) (*TranscriptionResult, error) {
	maxRetries := 3
	baseDelay := 1 * time.Second

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := baseDelay * time.Duration(1<<uint(attempt-1))
			time.Sleep(delay)
		}

		result, err := cf.attemptTranscribe(audio, options)
		if err == nil {
			return result, nil
		}

		lastErr = err

		if isRetryableError(err) && attempt < maxRetries {
			continue
		}

		break
	}

	return nil, lastErr
}

func (cf *CloudflareTranscription) attemptTranscribe(audio []byte, options TranscriptionOptions) (*TranscriptionResult, error) {
	// Cloudflare Workers AI accepts base64-encoded audio as JSON
	audioB64 := base64.StdEncoding.EncodeToString(audio)

	// Build request body — omit language field when set to "auto" so CF auto-detects
	type requestBody struct {
		Audio    string `json:"audio"`
		Language string `json:"language,omitempty"`
	}

	body := requestBody{Audio: audioB64}
	lang := options.Language
	if lang != "" && lang != "auto" {
		body.Language = lang
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %v", err)
	}

	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/ai/run/%s", cf.accountID, cf.model)
	req, err := http.NewRequest("POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cf.apiToken)

	resp, err := cf.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(respBytes))
	}

	var cfResponse struct {
		Result struct {
			Text      string `json:"text"`
			WordCount int    `json:"word_count"`
			Words     []struct {
				Word  string  `json:"word"`
				Start float64 `json:"start"`
				End   float64 `json:"end"`
			} `json:"words"`
		} `json:"result"`
		Success bool  `json:"success"`
		Errors  []any `json:"errors"`
	}

	if err := json.Unmarshal(respBytes, &cfResponse); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %v", err)
	}

	if !cfResponse.Success {
		return nil, fmt.Errorf("Cloudflare API returned success=false: %v", cfResponse.Errors)
	}

	transcript := strings.ToUpper(strings.TrimSpace(cfResponse.Result.Text))

	var segments []TranscriptSegment
	for _, w := range cfResponse.Result.Words {
		word := strings.TrimSpace(w.Word)
		if word == "" {
			continue
		}
		segments = append(segments, TranscriptSegment{
			Text:       strings.ToUpper(word),
			StartTime:  w.Start,
			EndTime:    w.End,
			Confidence: 0.95,
		})
	}
	// Fallback: no word-level data but we have text
	if len(segments) == 0 && transcript != "" {
		segments = []TranscriptSegment{{
			Text:       transcript,
			StartTime:  0,
			EndTime:    0,
			Confidence: 0.95,
		}}
	}

	return &TranscriptionResult{
		Transcript: transcript,
		Confidence: 0.95,
		Language:   options.Language,
		Segments:   segments,
	}, nil
}

// IsAvailable always returns true; connectivity errors surface at transcription time
func (cf *CloudflareTranscription) IsAvailable() bool {
	return cf.accountID != "" && cf.apiToken != ""
}

// GetName returns the name of this transcription provider
func (cf *CloudflareTranscription) GetName() string {
	return fmt.Sprintf("Cloudflare Workers AI (%s)", cf.model)
}

// GetSupportedLanguages returns supported languages
func (cf *CloudflareTranscription) GetSupportedLanguages() []string {
	return []string{
		"auto", "en", "es", "fr", "de", "it", "pt", "ru", "ja", "ko", "zh",
		"nl", "tr", "pl", "ca", "fa", "ar", "cs", "el", "fi", "he", "hi",
		"hu", "id", "ms", "no", "ro", "sk", "sv", "uk", "vi",
	}
}
