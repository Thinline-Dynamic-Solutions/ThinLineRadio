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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GoogleTranscription implements TranscriptionProvider for Google Cloud Speech-to-Text
type GoogleTranscription struct {
	available   bool
	apiKey      string
	credentials string // Service account JSON (alternative to API key)
	httpClient  *http.Client
	warned      bool
}

// GoogleConfig contains configuration for Google Cloud Speech-to-Text
type GoogleConfig struct {
	APIKey      string // Google Cloud API key
	Credentials string // Service account JSON credentials (alternative to API key)
}

// NewGoogleTranscription creates a new Google Cloud Speech-to-Text transcription provider
func NewGoogleTranscription(config *GoogleConfig) *GoogleTranscription {
	google := &GoogleTranscription{
		apiKey:      config.APIKey,
		credentials: config.Credentials,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}

	// Check availability (basic validation)
	google.available = google.apiKey != "" || google.credentials != ""

	return google
}

// Transcribe transcribes audio using Google Cloud Speech-to-Text
func (google *GoogleTranscription) Transcribe(audio []byte, options TranscriptionOptions) (*TranscriptionResult, error) {
	if !google.available {
		if !google.warned {
			google.warned = true
			return nil, fmt.Errorf("Google Cloud Speech-to-Text not configured. Please provide API key or service account credentials")
		}
		return nil, errors.New("Google Cloud Speech-to-Text is not available")
	}

	// Determine language
	language := options.Language
	if language == "" || language == "auto" {
		language = "en-US"
	}
	// Convert language code format if needed (e.g., "en" -> "en-US")
	if len(language) == 2 {
		language = language + "-US"
	}

	// Base64 encode audio
	audioBase64 := base64.StdEncoding.EncodeToString(audio)

	// Build request body
	requestBody := map[string]interface{}{
		"config": map[string]interface{}{
			"encoding":                   google.getAudioEncoding(options.AudioMime),
			"sampleRateHertz":            16000, // Default, may need adjustment based on actual audio
			"languageCode":               language,
			"enableAutomaticPunctuation": true,
			"enableWordTimeOffsets":      true,
		},
		"audio": map[string]interface{}{
			"content": audioBase64,
		},
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	// Google Cloud Speech-to-Text endpoint
	endpoint := "https://speech.googleapis.com/v1/speech:recognize"
	if google.apiKey != "" {
		endpoint += "?key=" + google.apiKey
	}

	// Create request
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// If using service account credentials, we'd need OAuth2 token
	// For now, API key is simpler
	if google.credentials != "" && google.apiKey == "" {
		return nil, fmt.Errorf("service account credentials require OAuth2 token generation (not yet implemented). Please use API key instead")
	}

	// Send request
	resp, err := google.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Google API request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var googleResponse struct {
		Results []struct {
			Alternatives []struct {
				Transcript string  `json:"transcript"`
				Confidence float64 `json:"confidence"`
				Words      []struct {
					StartTime string `json:"startTime"`
					EndTime   string `json:"endTime"`
					Word      string `json:"word"`
				} `json:"words"`
			} `json:"alternatives"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&googleResponse); err != nil {
		return nil, fmt.Errorf("failed to parse Google response: %v", err)
	}

	if len(googleResponse.Results) == 0 || len(googleResponse.Results[0].Alternatives) == 0 {
		return &TranscriptionResult{
			Transcript: "",
			Confidence: 0.0,
			Language:   language,
			Segments:   []TranscriptSegment{},
		}, nil
	}

	bestAlternative := googleResponse.Results[0].Alternatives[0]
	transcript := strings.ToUpper(strings.TrimSpace(bestAlternative.Transcript))

	// Build segments from words
	segments := []TranscriptSegment{}
	if len(bestAlternative.Words) > 0 {
		// Group words into segments (simplified: one segment per result)
		// In a more sophisticated implementation, you could group by time gaps
		startTime := google.parseTime(bestAlternative.Words[0].StartTime)
		endTime := google.parseTime(bestAlternative.Words[len(bestAlternative.Words)-1].EndTime)

		segments = append(segments, TranscriptSegment{
			Text:       transcript,
			StartTime:  startTime,
			EndTime:    endTime,
			Confidence: bestAlternative.Confidence,
		})
	} else if transcript != "" {
		// Fallback if no word timestamps
		segments = append(segments, TranscriptSegment{
			Text:       transcript,
			StartTime:  0,
			EndTime:    0,
			Confidence: bestAlternative.Confidence,
		})
	}

	return &TranscriptionResult{
		Transcript: transcript,
		Confidence: bestAlternative.Confidence,
		Language:   language,
		Segments:   segments,
	}, nil
}

// parseTime parses Google's time format (e.g., "1.234s" or "1234.567890123s")
func (google *GoogleTranscription) parseTime(timeStr string) float64 {
	if timeStr == "" {
		return 0
	}
	// Remove "s" suffix and parse
	timeStr = strings.TrimSuffix(timeStr, "s")
	var seconds float64
	fmt.Sscanf(timeStr, "%f", &seconds)
	return seconds
}

// getAudioEncoding determines audio encoding from MIME type
func (google *GoogleTranscription) getAudioEncoding(mimeType string) string {
	switch mimeType {
	case "audio/wav", "audio/wave":
		return "LINEAR16"
	case "audio/mp4", "audio/m4a":
		return "MP3" // Google may accept m4a as MP3
	case "audio/mpeg", "audio/mp3":
		return "MP3"
	case "audio/ogg":
		return "OGG_OPUS"
	case "audio/webm":
		return "WEBM_OPUS"
	default:
		return "LINEAR16" // Default to WAV
	}
}

// IsAvailable checks if Google Cloud Speech-to-Text is available
func (google *GoogleTranscription) IsAvailable() bool {
	return google.available
}

// GetName returns the name of this transcription provider
func (google *GoogleTranscription) GetName() string {
	return "Google Cloud Speech-to-Text"
}

// GetSupportedLanguages returns supported languages
func (google *GoogleTranscription) GetSupportedLanguages() []string {
	return []string{
		"auto", "en-US", "es-ES", "fr-FR", "de-DE", "it-IT", "pt-BR", "ru-RU", "ja-JP", "ko-KR", "zh-CN",
		"nl-NL", "tr-TR", "pl-PL", "ca-ES", "fa-IR", "ar-EG", "cs-CZ", "el-GR", "fi-FI", "he-IL", "hi-IN",
		"hu-HU", "id-ID", "ms-MY", "no-NO", "ro-RO", "sk-SK", "sv-SE", "uk-UA", "vi-VN",
	}
}
