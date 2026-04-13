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
	"net/http"
	"strings"
	"time"
)

// AzureTranscription implements TranscriptionProvider for Azure Speech Services
type AzureTranscription struct {
	available  bool
	apiKey     string
	region     string
	httpClient *http.Client
	warned     bool
}

// AzureConfig contains configuration for Azure Speech Services
type AzureConfig struct {
	APIKey string // Azure Speech Services subscription key
	Region string // Azure region (e.g., "eastus", "westus2")
}

// NewAzureTranscription creates a new Azure Speech Services transcription provider
func NewAzureTranscription(config *AzureConfig) *AzureTranscription {
	azure := &AzureTranscription{
		apiKey: config.APIKey,
		region: config.Region,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}

	// Default region if not specified
	if azure.region == "" {
		azure.region = "eastus"
	}

	// Check availability (basic validation)
	azure.available = azure.apiKey != "" && azure.region != ""

	return azure
}

// Transcribe transcribes audio using Azure Speech Services
func (azure *AzureTranscription) Transcribe(audio []byte, options TranscriptionOptions) (*TranscriptionResult, error) {
	if !azure.available {
		if !azure.warned {
			azure.warned = true
			return nil, fmt.Errorf("Azure Speech Services not configured. Please provide API key and region")
		}
		return nil, errors.New("Azure Speech Services is not available")
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

	// Convert audio to WAV format using ffmpeg
	// Azure Speech Services works best with WAV format (16kHz mono recommended)
	wavAudio, err := convertToWAV(audio)
	if err != nil {
		return nil, fmt.Errorf("failed to convert audio to WAV: %v", err)
	}

	// Validate WAV audio data
	if len(wavAudio) == 0 {
		return nil, fmt.Errorf("WAV audio data is empty after conversion")
	}

	// Azure Speech Services endpoint
	endpoint := fmt.Sprintf("https://%s.stt.speech.microsoft.com/speech/recognition/conversation/cognitiveservices/v1?language=%s&format=detailed", azure.region, language)

	// Create request
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(wavAudio))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	// Set headers
	req.Header.Set("Ocp-Apim-Subscription-Key", azure.apiKey)
	req.Header.Set("Content-Type", "audio/wav")

	// Send request
	resp, err := azure.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Azure API request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var azureResponse struct {
		RecognitionStatus string `json:"RecognitionStatus"`
		DisplayText       string `json:"DisplayText"`
		Offset            int64  `json:"Offset"`
		Duration          int64  `json:"Duration"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&azureResponse); err != nil {
		return nil, fmt.Errorf("failed to parse Azure response: %v", err)
	}

	if azureResponse.RecognitionStatus != "Success" {
		return nil, fmt.Errorf("Azure recognition failed: %s", azureResponse.RecognitionStatus)
	}

	transcript := strings.ToUpper(strings.TrimSpace(azureResponse.DisplayText))

	// Build segments (Azure provides single result, so create one segment)
	segments := []TranscriptSegment{}
	if transcript != "" {
		segments = append(segments, TranscriptSegment{
			Text:       transcript,
			StartTime:  float64(azureResponse.Offset) / 10000000.0, // Convert from 100-nanosecond units to seconds
			EndTime:    float64(azureResponse.Offset+azureResponse.Duration) / 10000000.0,
			Confidence: 0.95, // Azure doesn't provide confidence in this endpoint
		})
	}

	return &TranscriptionResult{
		Transcript: transcript,
		Confidence: 0.95,
		Language:   language,
		Segments:   segments,
	}, nil
}

// IsAvailable checks if Azure Speech Services is available
func (azure *AzureTranscription) IsAvailable() bool {
	return azure.available
}

// GetName returns the name of this transcription provider
func (azure *AzureTranscription) GetName() string {
	return fmt.Sprintf("Azure Speech Services (%s)", azure.region)
}

// GetSupportedLanguages returns supported languages
func (azure *AzureTranscription) GetSupportedLanguages() []string {
	return []string{
		"auto", "en-US", "es-ES", "fr-FR", "de-DE", "it-IT", "pt-BR", "ru-RU", "ja-JP", "ko-KR", "zh-CN",
		"nl-NL", "tr-TR", "pl-PL", "ca-ES", "fa-IR", "ar-EG", "cs-CZ", "el-GR", "fi-FI", "he-IL", "hi-IN",
		"hu-HU", "id-ID", "ms-MY", "no-NO", "ro-RO", "sk-SK", "sv-SE", "uk-UA", "vi-VN",
	}
}

// Note: convertToWAV function is shared with AssemblyAI transcription provider
// It's defined in transcription_assemblyai.go since both are in the same package
