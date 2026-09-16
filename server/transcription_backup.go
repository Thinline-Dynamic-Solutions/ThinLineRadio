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
	"fmt"
	"strings"
)

func supportedSTTProvider(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "whisper-api", "azure", "google", "gemini", "assemblyai", "cloudflare":
		return strings.ToLower(strings.TrimSpace(name))
	default:
		return ""
	}
}

func resolvePrimaryProvider(name string) string {
	if strings.EqualFold(strings.TrimSpace(name), "hydra") {
		return "hydra"
	}
	if p := supportedSTTProvider(name); p != "" {
		return p
	}
	return "whisper-api"
}

func resolveBackupProvider(primary, backup string) string {
	b := supportedSTTProvider(backup)
	if b == "" {
		return ""
	}
	p := resolvePrimaryProvider(primary)
	if b == p {
		return ""
	}
	return b
}

func newTranscriptionProvider(config TranscriptionConfig, provider string) TranscriptionProvider {
	switch supportedSTTProvider(provider) {
	case "azure":
		return NewAzureTranscription(&AzureConfig{
			APIKey: config.AzureKey,
			Region: config.AzureRegion,
		})
	case "google":
		return NewGoogleTranscription(&GoogleConfig{
			APIKey:      config.GoogleAPIKey,
			Credentials: config.GoogleCredentials,
		})
	case "gemini":
		apiKey := strings.TrimSpace(config.GeminiAPIKey)
		if apiKey == "" {
			apiKey = strings.TrimSpace(config.GoogleAPIKey)
		}
		return NewGeminiTranscription(&GeminiConfig{
			APIKey:         apiKey,
			Model:          config.GeminiModel,
			TimeoutSeconds: config.TimeoutSeconds,
		})
	case "assemblyai":
		return NewAssemblyAITranscription(&AssemblyAIConfig{
			APIKey: config.AssemblyAIKey,
		})
	case "cloudflare":
		return NewCloudflareTranscription(&CloudflareConfig{
			AccountID:      config.CloudflareAccountID,
			APIToken:       config.CloudflareAPIToken,
			Model:          config.CloudflareModel,
			TimeoutSeconds: config.TimeoutSeconds,
		})
	case "whisper-api":
		url := strings.TrimSpace(config.WhisperAPIURL)
		if url == "" {
			url = "http://localhost:8000"
		}
		return NewWhisperAPITranscription(&WhisperAPIConfig{
			BaseURL:        url,
			APIKey:         config.WhisperAPIKey,
			Model:          config.WhisperAPIModel,
			TimeoutSeconds: config.TimeoutSeconds,
		})
	default:
		return nil
	}
}

// transcribeWithBackup runs primary STT, then backup only if primary fails or is unavailable.
func transcribeWithBackup(primary, backup TranscriptionProvider, audio []byte, primaryOpts, backupOpts TranscriptionOptions) (*TranscriptionResult, bool, error) {
	var primaryErr error
	if primary != nil && primary.IsAvailable() {
		result, err := primary.Transcribe(audio, primaryOpts)
		if err == nil {
			return result, false, nil
		}
		primaryErr = err
	} else if primary != nil {
		primaryErr = fmt.Errorf("%s not available", primary.GetName())
	} else {
		primaryErr = fmt.Errorf("transcription provider not configured")
	}

	if backup == nil {
		return nil, false, primaryErr
	}
	if !backup.IsAvailable() {
		return nil, false, fmt.Errorf("%w; backup not available", primaryErr)
	}

	result, err := backup.Transcribe(audio, backupOpts)
	if err != nil {
		return nil, false, fmt.Errorf("%w; backup: %v", primaryErr, err)
	}
	return result, true, nil
}
