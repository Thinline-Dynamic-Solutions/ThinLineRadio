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
	"os/exec"
	"strings"
	"time"
)

// AssemblyAITranscription implements TranscriptionProvider for AssemblyAI
type AssemblyAITranscription struct {
	available  bool
	apiKey     string
	httpClient *http.Client
	warned     bool
}

// AssemblyAIConfig contains configuration for AssemblyAI
type AssemblyAIConfig struct {
	APIKey string // AssemblyAI API key
}

// NewAssemblyAITranscription creates a new AssemblyAI transcription provider
func NewAssemblyAITranscription(config *AssemblyAIConfig) *AssemblyAITranscription {
	assemblyai := &AssemblyAITranscription{
		apiKey: config.APIKey,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}

	// Check availability (basic validation)
	assemblyai.available = assemblyai.apiKey != ""

	return assemblyai
}

// Transcribe transcribes audio using AssemblyAI
func (assemblyai *AssemblyAITranscription) Transcribe(audio []byte, options TranscriptionOptions) (*TranscriptionResult, error) {
	if !assemblyai.available {
		if !assemblyai.warned {
			assemblyai.warned = true
			return nil, fmt.Errorf("AssemblyAI not configured. Please provide API key")
		}
		return nil, errors.New("AssemblyAI is not available")
	}

	// Determine language
	language := options.Language
	if language == "" || language == "auto" {
		language = "en"
	}
	// Convert language code format if needed (e.g., "en-US" -> "en")
	if strings.Contains(language, "-") {
		language = strings.Split(language, "-")[0]
	}

	// Step 1: Convert audio to WAV format using ffmpeg
	// This ensures AssemblyAI can recognize and process the audio correctly
	fmt.Printf("DEBUG: Converting audio to WAV - original size: %d bytes, mime: %s\n", len(audio), options.AudioMime)

	wavAudio, err := convertToWAV(audio)
	if err != nil {
		return nil, fmt.Errorf("failed to convert audio to WAV: %v", err)
	}

	fmt.Printf("DEBUG: Converted to WAV - new size: %d bytes\n", len(wavAudio))

	// Validate WAV audio data
	if len(wavAudio) == 0 {
		return nil, fmt.Errorf("WAV audio data is empty after conversion")
	}

	// Check WAV header
	if len(wavAudio) >= 4 {
		header := wavAudio[:4]
		fmt.Printf("DEBUG: WAV header bytes: %x (should be 52494646 for 'RIFF')\n", header)
	}

	// Step 2: Upload WAV audio as raw bytes
	uploadURL := "https://api.assemblyai.com/v2/upload"
	uploadReq, err := http.NewRequest("POST", uploadURL, bytes.NewReader(wavAudio))
	if err != nil {
		return nil, fmt.Errorf("failed to create upload request: %v", err)
	}

	uploadReq.Header.Set("authorization", assemblyai.apiKey)
	uploadReq.Header.Set("content-type", "application/octet-stream")

	uploadResp, err := assemblyai.httpClient.Do(uploadReq)
	if err != nil {
		return nil, fmt.Errorf("failed to upload audio: %v", err)
	}
	defer uploadResp.Body.Close()

	if uploadResp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(uploadResp.Body)
		return nil, fmt.Errorf("AssemblyAI upload failed with status %d: %s", uploadResp.StatusCode, string(bodyBytes))
	}

	var uploadResponse struct {
		UploadURL string `json:"upload_url"`
	}

	// Read the full response body first (before JSON decode consumes it)
	uploadRespBody, err := io.ReadAll(uploadResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read upload response: %v", err)
	}

	if err := json.Unmarshal(uploadRespBody, &uploadResponse); err != nil {
		return nil, fmt.Errorf("failed to parse upload response: %v. Response body: %s", err, string(uploadRespBody))
	}

	// Validate upload URL
	if uploadResponse.UploadURL == "" {
		return nil, fmt.Errorf("AssemblyAI upload returned empty URL. Response body: %s", string(uploadRespBody))
	}

	// Validate URL format (should be a valid URL)
	if !strings.HasPrefix(uploadResponse.UploadURL, "http://") && !strings.HasPrefix(uploadResponse.UploadURL, "https://") {
		return nil, fmt.Errorf("AssemblyAI upload returned invalid URL format: %s", uploadResponse.UploadURL)
	}

	// Step 2: Submit transcription job
	// Determine speech model — AssemblyAI requires speech_models to be a non-empty array.
	// Valid values: "universal-2" (default, cheaper) or "universal-3-pro" (higher accuracy).
	speechModel := options.SpeechModel
	if speechModel == "" {
		speechModel = "universal-2"
	}

	transcriptBody := map[string]interface{}{
		"audio_url":     uploadResponse.UploadURL,
		"speech_models": []string{speechModel},
	}

	// Keyterms for under-represented vocabulary (AssemblyAI; replaces deprecated word_boost for all models).
	if len(options.WordBoost) > 0 {
		validKeyterms := []string{}
		for _, term := range options.WordBoost {
			trimmed := strings.TrimSpace(term)
			if trimmed != "" && len(trimmed) <= 50 {
				validKeyterms = append(validKeyterms, trimmed)
			}
			if len(validKeyterms) >= 100 {
				break
			}
		}
		if len(validKeyterms) > 0 {
			transcriptBody["keyterms_prompt"] = validKeyterms
		}
	}

	// Only add optional fields if needed
	// Try minimal request first - just audio_url

	transcriptJSON, err := json.Marshal(transcriptBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal transcript request: %v", err)
	}

	transcriptURL := "https://api.assemblyai.com/v2/transcript"
	transcriptReq, err := http.NewRequest("POST", transcriptURL, bytes.NewReader(transcriptJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create transcript request: %v", err)
	}

	transcriptReq.Header.Set("authorization", assemblyai.apiKey)
	transcriptReq.Header.Set("content-type", "application/json")

	transcriptResp, err := assemblyai.httpClient.Do(transcriptReq)
	if err != nil {
		return nil, fmt.Errorf("failed to submit transcript request: %v", err)
	}
	defer transcriptResp.Body.Close()

	if transcriptResp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(transcriptResp.Body)
		return nil, fmt.Errorf("AssemblyAI transcript submission failed with status %d: %s", transcriptResp.StatusCode, string(bodyBytes))
	}

	var transcriptResponse struct {
		Id     string `json:"id"`
		Status string `json:"status"`
	}

	if err := json.NewDecoder(transcriptResp.Body).Decode(&transcriptResponse); err != nil {
		return nil, fmt.Errorf("failed to parse transcript response: %v", err)
	}

	// Step 3: Poll for results
	transcriptId := transcriptResponse.Id
	maxAttempts := 60 // 5 minutes max (5 second intervals)
	attempt := 0

	for attempt < maxAttempts {
		time.Sleep(5 * time.Second) // Wait 5 seconds between polls
		attempt++

		// Get transcript status
		getURL := fmt.Sprintf("https://api.assemblyai.com/v2/transcript/%s", transcriptId)
		getReq, err := http.NewRequest("GET", getURL, nil)
		if err != nil {
			continue
		}

		getReq.Header.Set("authorization", assemblyai.apiKey)

		getResp, err := assemblyai.httpClient.Do(getReq)
		if err != nil {
			continue
		}

		if getResp.StatusCode != http.StatusOK {
			getResp.Body.Close()
			continue
		}

		var result struct {
			Status string `json:"status"`
			Text   string `json:"text"`
			Words  []struct {
				Start int64  `json:"start"`
				End   int64  `json:"end"`
				Text  string `json:"text"`
			} `json:"words"`
			Confidence   float64 `json:"confidence"`
			LanguageCode string  `json:"language_code"`
		}

		if err := json.NewDecoder(getResp.Body).Decode(&result); err != nil {
			getResp.Body.Close()
			continue
		}
		getResp.Body.Close()

		if result.Status == "completed" {
			transcript := strings.ToUpper(strings.TrimSpace(result.Text))

			// Build segments from words
			segments := []TranscriptSegment{}
			if len(result.Words) > 0 {
				// Group words into segments (simplified: one segment per result)
				startTime := float64(result.Words[0].Start) / 1000.0 // Convert from milliseconds to seconds
				endTime := float64(result.Words[len(result.Words)-1].End) / 1000.0

				segments = append(segments, TranscriptSegment{
					Text:       transcript,
					StartTime:  startTime,
					EndTime:    endTime,
					Confidence: result.Confidence,
				})
			} else if transcript != "" {
				// Fallback if no word timestamps
				segments = append(segments, TranscriptSegment{
					Text:       transcript,
					StartTime:  0,
					EndTime:    0,
					Confidence: result.Confidence,
				})
			}

			return &TranscriptionResult{
				Transcript: transcript,
				Confidence: result.Confidence,
				Language:   result.LanguageCode,
				Segments:   segments,
			}, nil
		} else if result.Status == "error" {
			return nil, fmt.Errorf("AssemblyAI transcription failed")
		}
		// Status is "queued" or "processing", continue polling
	}

	return nil, fmt.Errorf("AssemblyAI transcription timed out after %d attempts", maxAttempts)
}

// IsAvailable checks if AssemblyAI is available
func (assemblyai *AssemblyAITranscription) IsAvailable() bool {
	return assemblyai.available
}

// GetName returns the name of this transcription provider
func (assemblyai *AssemblyAITranscription) GetName() string {
	return "AssemblyAI"
}

// GetSupportedLanguages returns supported languages
func (assemblyai *AssemblyAITranscription) GetSupportedLanguages() []string {
	return []string{
		"auto", "en", "es", "fr", "de", "it", "pt", "ru", "ja", "ko", "zh",
		"nl", "tr", "pl", "ca", "fa", "ar", "cs", "el", "fi", "he", "hi",
		"hu", "id", "ms", "no", "ro", "sk", "sv", "uk", "vi", "da", "th",
		"hi", "hi-Latn", "hi-Latn-romanian",
	}
}

// convertToWAV converts audio to WAV format using ffmpeg
func convertToWAV(audio []byte) ([]byte, error) {
	// Use ffmpeg to convert to WAV 16kHz mono
	// This format is universally recognized and reduces upload size
	ffArgs := []string{
		"-y", "-loglevel", "error",
		"-i", "pipe:0", // Read from stdin
		"-ar", "16000", // 16kHz sample rate
		"-ac", "1", // Mono
		"-f", "wav", // WAV format
		"pipe:1", // Write to stdout
	}

	cmd := exec.Command("ffmpeg", ffArgs...)
	cmd.Stdin = bytes.NewReader(audio)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg conversion failed: %v, stderr: %s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}
