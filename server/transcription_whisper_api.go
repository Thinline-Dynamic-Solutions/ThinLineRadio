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
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"strings"
	"time"
)

// WhisperAPITranscription implements TranscriptionProvider for external OpenAI-compatible Whisper API server
type WhisperAPITranscription struct {
	available  bool
	baseURL    string // Base URL of the Whisper API server (e.g., "http://localhost:8000")
	apiKey     string // Optional API key (if required)
	model      string // Model name (e.g., "whisper-1", "gpt-4o-transcribe")
	httpClient *http.Client
	warned     bool
}

// WhisperAPIConfig contains configuration for external Whisper API
type WhisperAPIConfig struct {
	BaseURL string // Base URL of the API server
	APIKey  string // Optional API key
	Model   string // Model name (e.g., "whisper-1", "gpt-4o-transcribe"); defaults to "whisper-1"
}

// NewWhisperAPITranscription creates a new external Whisper API transcription service
func NewWhisperAPITranscription(config *WhisperAPIConfig) *WhisperAPITranscription {
	// Configure custom transport with proper connection pooling and timeouts
	transport := &http.Transport{
		// Connection pool settings
		MaxIdleConns:        100,              // Maximum total idle connections
		MaxIdleConnsPerHost: 10,               // Maximum idle connections per host
		MaxConnsPerHost:     20,               // Maximum total connections per host
		IdleConnTimeout:     90 * time.Second, // How long idle connections stay open

		// Timeouts for establishing connections
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second, // Connection timeout
			KeepAlive: 30 * time.Second, // Keep-alive probe interval
		}).DialContext,

		// Other important timeouts
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second, // Timeout waiting for response headers
		ExpectContinueTimeout: 1 * time.Second,

		// Disable HTTP/2 to avoid potential issues with some Whisper servers
		ForceAttemptHTTP2: false,

		// Don't reuse connections that have been idle too long
		DisableKeepAlives: false, // Keep connections alive for reuse
	}

	model := config.Model
	if model == "" {
		model = "whisper-1"
	}

	api := &WhisperAPITranscription{
		baseURL: config.BaseURL,
		apiKey:  config.APIKey,
		model:   model,
		httpClient: &http.Client{
			Timeout:   5 * time.Minute, // Allow up to 5 minutes for transcription
			Transport: transport,
		},
	}

	// Default to https://api.openai.com if not specified
	if api.baseURL == "" {
		api.baseURL = "https://api.openai.com"
	}

	// Remove trailing slash
	api.baseURL = strings.TrimSuffix(api.baseURL, "/")

	if api.baseURL == "https://api.openai.com" {
	    api.available = true
	} else {
	    // Test availability by checking health endpoint if no OpenAI Official
    	api.available = api.checkAvailability()
	}


	return api
}

// checkAvailability checks if the API server is available
// Uses a short timeout (5 seconds) to avoid blocking server startup
func (api *WhisperAPITranscription) checkAvailability() bool {
	healthURL := api.baseURL + "/health"

	// Use a short timeout for health checks to avoid blocking server startup
	// if the API server is busy processing a transcription
	healthClient := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := healthClient.Get(healthURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Transcribe transcribes audio using the external Whisper API server
func (api *WhisperAPITranscription) Transcribe(audio []byte, options TranscriptionOptions) (*TranscriptionResult, error) {
	if !api.available {
		if !api.warned {
			api.warned = true
			return nil, fmt.Errorf("whisper API server not available at %s. Make sure the server is running", api.baseURL)
		}
		return nil, errors.New("whisper API is not available")
	}

	// Retry logic with exponential backoff for transient network errors
	maxRetries := 3
	baseDelay := 1 * time.Second

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s
			delay := baseDelay * time.Duration(1<<uint(attempt-1))
			time.Sleep(delay)
		}

		result, err := api.attemptTranscribe(audio, options)
		if err == nil {
			return result, nil
		}

		lastErr = err

		// Check if error is retryable (network/connection errors)
		if isRetryableError(err) && attempt < maxRetries {
			// Retry on connection errors, EOF, etc.
			continue
		}

		// Non-retryable error or max retries exceeded
		break
	}

	return nil, lastErr
}

// isRetryableError determines if an error should trigger a retry
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := err.Error()

	// Check for common retryable errors
	retryableErrors := []string{
		"connection refused",
		"connection reset",
		"connection forcibly closed",
		"EOF",
		"broken pipe",
		"i/o timeout",
		"no such host",
		"temporary failure",
		"TLS handshake timeout",
	}

	for _, retryable := range retryableErrors {
		if strings.Contains(strings.ToLower(errMsg), strings.ToLower(retryable)) {
			return true
		}
	}

	return false
}

// attemptTranscribe performs a single transcription attempt
func (api *WhisperAPITranscription) attemptTranscribe(audio []byte, options TranscriptionOptions) (*TranscriptionResult, error) {
	// Determine file extension from MIME type
	filename := "audio.m4a" // Default
	if options.AudioMime != "" {
		switch options.AudioMime {
		case "audio/mp4", "audio/m4a":
			filename = "audio.m4a"
		case "audio/mpeg", "audio/mp3":
			filename = "audio.mp3"
		case "audio/wav", "audio/wave":
			filename = "audio.wav"
		case "audio/ogg":
			filename = "audio.ogg"
		case "audio/webm":
			filename = "audio.webm"
		default:
			filename = "audio.m4a" // Default to m4a
		}
	}

	// Create multipart form data
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// Add file field
	fileWriter, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %v", err)
	}
	if _, err := io.Copy(fileWriter, bytes.NewReader(audio)); err != nil {
		return nil, fmt.Errorf("failed to write audio data: %v", err)
	}

	// Add model field (required by OpenAI API format)
	if err := writer.WriteField("model", api.model); err != nil {
		return nil, fmt.Errorf("failed to write model field: %v", err)
	}

	// Add language if specified
	language := options.Language
	if language == "" || language == "auto" {
		language = "en"
	}
	if language != "" {
		if err := writer.WriteField("language", language); err != nil {
			return nil, fmt.Errorf("failed to write language field: %v", err)
		}
	}

	// Add response format (use verbose_json to get segments)
	if err := writer.WriteField("response_format", "verbose_json"); err != nil {
		return nil, fmt.Errorf("failed to write response_format field: %v", err)
	}

	// Add temperature if specified
	if options.Temperature > 0 {
		if err := writer.WriteField("temperature", fmt.Sprintf("%.2f", options.Temperature)); err != nil {
			return nil, fmt.Errorf("failed to write temperature field: %v", err)
		}
	}

	// Add timestamp_granularities for segments
	if err := writer.WriteField("timestamp_granularities[]", "segment"); err != nil {
		return nil, fmt.Errorf("failed to write timestamp_granularities field: %v", err)
	}

	// Add prompt if specified (for custom terminology, formatting, etc.)
	if options.InitialPrompt != "" {
		if err := writer.WriteField("prompt", options.InitialPrompt); err != nil {
			return nil, fmt.Errorf("failed to write prompt field: %v", err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %v", err)
	}

	// Create HTTP request
	url := api.baseURL + "/v1/audio/transcriptions"
	req, err := http.NewRequest("POST", url, &requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	// Add Connection: close header to avoid connection reuse issues
	req.Header.Set("Connection", "keep-alive")

	if api.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+api.apiKey)
	}

	// Send request
	resp, err := api.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var apiResponse struct {
		Text     string `json:"text"`
		Language string `json:"language"`
		Duration float64 `json:"duration"`
		Segments []struct {
			Id    int     `json:"id"`
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Text  string  `json:"text"`
		} `json:"segments"`
		Words []struct {
			Word  string  `json:"word"`
			Start float64 `json:"start"`
			End   float64 `json:"end"`
		} `json:"words"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %v", err)
	}

	// Convert to TranscriptionResult format
	transcript := strings.ToUpper(strings.TrimSpace(apiResponse.Text))

	// Build segments
	segments := make([]TranscriptSegment, 0, len(apiResponse.Segments))
	for _, seg := range apiResponse.Segments {
		segText := strings.TrimSpace(seg.Text)
		if segText == "" {
			continue
		}
		segments = append(segments, TranscriptSegment{
			Text:       strings.ToUpper(segText),
			StartTime:  seg.Start,
			EndTime:    seg.End,
			Confidence: 0.95, // API doesn't provide per-segment confidence
		})
	}

	// If no segments but we have text, create a single segment
	if len(segments) == 0 && transcript != "" {
		segments = append(segments, TranscriptSegment{
			Text:       transcript,
			StartTime:  0,
			EndTime:    apiResponse.Duration,
			Confidence: 0.95,
		})
	}

	return &TranscriptionResult{
		Transcript: transcript,
		Confidence: 0.95, // API doesn't provide overall confidence
		Language:   apiResponse.Language,
		Segments:   segments,
	}, nil
}

// IsAvailable checks if the API server is available
func (api *WhisperAPITranscription) IsAvailable() bool {
	return api.available
}

// GetName returns the name of this transcription provider
func (api *WhisperAPITranscription) GetName() string {
	return fmt.Sprintf("Whisper API Server (%s)", api.baseURL)
}

// GetSupportedLanguages returns supported languages
func (api *WhisperAPITranscription) GetSupportedLanguages() []string {
	// Whisper API supports all languages that Whisper supports
	return []string{
		"auto", "en", "es", "fr", "de", "it", "pt", "ru", "ja", "ko", "zh",
		"nl", "tr", "pl", "ca", "fa", "ar", "cs", "el", "fi", "he", "hi",
		"hu", "id", "ms", "no", "ro", "sk", "sv", "uk", "vi",
	}
}

