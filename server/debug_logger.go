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
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DebugLogger handles writing debug logs to a dedicated file
type DebugLogger struct {
	file     *os.File
	mutex    sync.Mutex
	audioDir string // Directory to save debug audio files
	closed   bool   // Flag to prevent writes after close
}

// NewDebugLogger creates a new debug logger that writes to tone-keyword-debug.log
func NewDebugLogger(filename string) (*DebugLogger, error) {
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open debug log file: %v", err)
	}

	// Create audio debug directory
	audioDir := "debug-audio"
	if err := os.MkdirAll(audioDir, 0755); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to create debug audio directory: %v", err)
	}

	logger := &DebugLogger{
		file:     file,
		mutex:    sync.Mutex{},
		audioDir: audioDir,
	}

	// Write header on startup
	logger.WriteLog("=================================================")
	logger.WriteLog("Tone & Keyword Debug Log - Server Started")
	logger.WriteLog(fmt.Sprintf("Audio files will be saved to: %s/", audioDir))
	logger.WriteLog("=================================================")

	return logger, nil
}

// WriteLog writes a message to the debug log with timestamp
func (d *DebugLogger) WriteLog(message string) {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	// Check if logger is closed
	if d.closed || d.file == nil {
		return
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	logLine := fmt.Sprintf("[%s] %s\n", timestamp, message)

	d.file.WriteString(logLine)
	d.file.Sync() // Flush to disk immediately
}

// LogToneDetection logs tone detection events
func (d *DebugLogger) LogToneDetection(callId uint64, systemId uint64, talkgroupRef uint, message string) {
	d.WriteLog(fmt.Sprintf("[TONE] Call=%d System=%d Talkgroup=%d | %s", callId, systemId, talkgroupRef, message))
}

// LogToneFrequency logs detected tone frequencies
func (d *DebugLogger) LogToneFrequency(callId uint64, frequency float64, duration float64, matched bool, toneSetLabel string) {
	status := "NO_MATCH"
	if matched {
		status = fmt.Sprintf("MATCHED: %s", toneSetLabel)
	}
	d.WriteLog(fmt.Sprintf("[TONE_FREQ] Call=%d | %.1f Hz for %.2fs - %s", callId, frequency, duration, status))
}

// LogPendingTones logs pending tone operations
func (d *DebugLogger) LogPendingTones(operation string, callId uint64, talkgroupRef uint, details string) {
	d.WriteLog(fmt.Sprintf("[PENDING_TONES] %s | Call=%d Talkgroup=%d | %s", operation, callId, talkgroupRef, details))
}

// LogVoiceDetection logs voice detection decisions
func (d *DebugLogger) LogVoiceDetection(callId uint64, transcript string, isVoice bool, reason string) {
	status := "VOICE"
	if !isVoice {
		status = "NOT_VOICE"
	}
	transcriptPreview := transcript
	if len(transcriptPreview) > 100 {
		transcriptPreview = transcriptPreview[:100] + "..."
	}
	d.WriteLog(fmt.Sprintf("[VOICE_CHECK] Call=%d | Status=%s | Reason: %s | Transcript: %q", callId, status, reason, transcriptPreview))
}

// LogKeywordMatch logs keyword detection
func (d *DebugLogger) LogKeywordMatch(callId uint64, keyword string, transcript string) {
	transcriptPreview := transcript
	if len(transcriptPreview) > 100 {
		transcriptPreview = transcriptPreview[:100] + "..."
	}
	d.WriteLog(fmt.Sprintf("[KEYWORD] Call=%d | Matched: %q | Transcript: %q", callId, keyword, transcriptPreview))
}

// LogAlert logs alert creation
func (d *DebugLogger) LogAlert(alertType string, callId uint64, systemId uint64, talkgroupId uint64, details string) {
	d.WriteLog(fmt.Sprintf("[ALERT] Type=%s Call=%d System=%d Talkgroup=%d | %s", alertType, callId, systemId, talkgroupId, details))
}

// LogToneAttachment logs when pending tones are attached to a voice call
func (d *DebugLogger) LogToneAttachment(voiceCallId uint64, toneCallId uint64, talkgroupRef uint, ageMinutes float64, toneSetLabels []string) {
	d.WriteLog(fmt.Sprintf("[ATTACH] Voice Call=%d got pending tones from Tone Call=%d | Talkgroup=%d Age=%.2f min | ToneSets: %v", voiceCallId, toneCallId, talkgroupRef, ageMinutes, toneSetLabels))
}

// SaveAudioFile saves call audio to the debug directory
func (d *DebugLogger) SaveAudioFile(callId uint64, audioData []byte, mimeType string, callType string) error {
	if len(audioData) == 0 {
		return fmt.Errorf("no audio data to save")
	}

	// Determine file extension from MIME type
	ext := ".bin"
	switch mimeType {
	case "audio/mpeg", "audio/mp3":
		ext = ".mp3"
	case "audio/wav", "audio/x-wav":
		ext = ".wav"
	case "audio/ogg":
		ext = ".ogg"
	case "audio/opus":
		ext = ".opus"
	case "audio/aac":
		ext = ".aac"
	case "audio/m4a", "audio/mp4":
		ext = ".m4a"
	}

	// Create filename with call ID, type, and timestamp
	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("call-%d-%s-%s%s", callId, callType, timestamp, ext)
	filepath := filepath.Join(d.audioDir, filename)

	// Write audio file
	if err := os.WriteFile(filepath, audioData, 0644); err != nil {
		d.WriteLog(fmt.Sprintf("[AUDIO_SAVE_ERROR] Failed to save audio for call %d: %v", callId, err))
		return err
	}

	// Log success
	d.WriteLog(fmt.Sprintf("[AUDIO_SAVED] Call=%d Type=%s | Saved to: %s (%d bytes)", callId, callType, filename, len(audioData)))
	return nil
}

// Close closes the debug log file
func (d *DebugLogger) Close() {
	// Mark as closed first (this will cause WriteLog calls to return early)
	d.mutex.Lock()
	d.closed = true
	d.mutex.Unlock()
	
	// Small delay to let any in-flight writes complete
	time.Sleep(100 * time.Millisecond)
	
	// Write final message and close file (no mutex needed, writes will be rejected now)
	if d.file != nil {
		timestamp := time.Now().Format("2006-01-02 15:04:05.000")
		d.file.WriteString(fmt.Sprintf("[%s] =================================================\n", timestamp))
		d.file.WriteString(fmt.Sprintf("[%s] Server Stopping - Debug Log Closed\n", timestamp))
		d.file.WriteString(fmt.Sprintf("[%s] =================================================\n", timestamp))
		d.file.Sync()
		d.file.Close()
	}
}

