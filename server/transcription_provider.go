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

// TranscriptionProvider defines the interface for transcription services
type TranscriptionProvider interface {
	Transcribe(audio []byte, options TranscriptionOptions) (*TranscriptionResult, error)
	IsAvailable() bool
	GetName() string
	GetSupportedLanguages() []string
}

// TranscriptionOptions contains options for transcription
type TranscriptionOptions struct {
	Language       string   // "en", "auto", etc.
	Model          string   // "tiny", "base", "small", "medium", "large" (for Whisper)
	Device         string   // "cpu", "cuda", "metal" (for GPU)
	Temperature    float64  // Temperature for sampling (0.0-1.0)
	InitialPrompt  string   // Initial prompt/context
	AudioMime      string   // MIME type of audio (e.g., "audio/mp4", "audio/mpeg")
	WordBoost      []string // Word boost/keyterms for AssemblyAI (max 100 terms, 50 chars each)
	SpeechModel    string   // AssemblyAI speech_models id (e.g. "universal-2", "universal-3-5-pro")
	SystemLabel    string   // Human-readable system name (passed to Whisper server for logging)
	TalkgroupLabel string   // Human-readable talkgroup name (passed to Whisper server for logging)
	CallID         uint64   // Call ID (passed to Whisper server for log correlation)
	// ExtractAddress asks Gemini (and similar multimodal providers) to also
	// return a scene address / common place name in ExtractedAddress.
	ExtractAddress bool
}

// TranscriptionResult contains the transcription result
type TranscriptionResult struct {
	Transcript   string              `json:"transcript"`    // The transcribed text (in ALL CAPS)
	Confidence   float64             `json:"confidence"`    // Confidence score (0.0-1.0)
	Language     string              `json:"language"`      // Detected language code
	Segments     []TranscriptSegment `json:"segments"`      // Timestamped segments (optional)
	AlertSummary string              `json:"alert_summary"` // Optional short summary from Whisper server (e.g. from integrated Ollama); used for alerts when present
	// ExtractedAddress is an optional short scene address / place from Gemini
	// when incident-mapping address extraction is enabled.
	ExtractedAddress string `json:"extracted_address,omitempty"`
}

// TranscriptSegment represents a timestamped segment of the transcript
type TranscriptSegment struct {
	Text       string  `json:"text"`       // Segment text
	StartTime  float64 `json:"startTime"`  // Start time in seconds
	EndTime    float64 `json:"endTime"`    // End time in seconds
	Confidence float64 `json:"confidence"` // Confidence for this segment
}
