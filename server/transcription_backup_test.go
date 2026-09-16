// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"fmt"
	"strings"
	"testing"
)

type mockSTT struct {
	name      string
	available bool
	err       error
	result    *TranscriptionResult
	calls     int
}

func (m *mockSTT) Transcribe(audio []byte, options TranscriptionOptions) (*TranscriptionResult, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

func (m *mockSTT) IsAvailable() bool { return m.available }
func (m *mockSTT) GetName() string   { return m.name }
func (m *mockSTT) GetSupportedLanguages() []string {
	return []string{"en"}
}

func TestResolveBackupProvider(t *testing.T) {
	if got := resolveBackupProvider("cloudflare", "gemini"); got != "gemini" {
		t.Fatalf("got %q", got)
	}
	if got := resolveBackupProvider("cloudflare", "cloudflare"); got != "" {
		t.Fatalf("same provider should be ignored, got %q", got)
	}
	if got := resolveBackupProvider("cloudflare", ""); got != "" {
		t.Fatalf("empty backup should be ignored, got %q", got)
	}
	if got := resolveBackupProvider("cloudflare", "hydra"); got != "" {
		t.Fatalf("hydra backup should be ignored, got %q", got)
	}
	if got := resolveBackupProvider("", "whisper-api"); got != "" {
		t.Fatalf("default primary is whisper-api, backup whisper-api should be ignored, got %q", got)
	}
	if got := resolveBackupProvider("", "gemini"); got != "gemini" {
		t.Fatalf("got %q", got)
	}
}

func TestTranscribeWithBackupPrimarySucceeds(t *testing.T) {
	primary := &mockSTT{name: "primary", available: true, result: &TranscriptionResult{Transcript: "PRIMARY"}}
	backup := &mockSTT{name: "backup", available: true, result: &TranscriptionResult{Transcript: "BACKUP"}}
	got, usedBackup, err := transcribeWithBackup(primary, backup, []byte("a"), TranscriptionOptions{}, TranscriptionOptions{})
	if err != nil || usedBackup || got.Transcript != "PRIMARY" {
		t.Fatalf("transcript=%v usedBackup=%v err=%v", got, usedBackup, err)
	}
	if backup.calls != 0 {
		t.Fatalf("backup should not run, calls=%d", backup.calls)
	}
}

func TestTranscribeWithBackupUsesBackupOnPrimaryError(t *testing.T) {
	primary := &mockSTT{name: "primary", available: true, err: fmt.Errorf("CUDA OOM")}
	backup := &mockSTT{name: "backup", available: true, result: &TranscriptionResult{Transcript: "BACKUP"}}
	got, usedBackup, err := transcribeWithBackup(primary, backup, []byte("a"), TranscriptionOptions{}, TranscriptionOptions{})
	if err != nil || !usedBackup || got.Transcript != "BACKUP" {
		t.Fatalf("transcript=%v usedBackup=%v err=%v", got, usedBackup, err)
	}
	if primary.calls != 1 || backup.calls != 1 {
		t.Fatalf("calls primary=%d backup=%d", primary.calls, backup.calls)
	}
}

func TestTranscribeWithBackupSkipsUnavailablePrimary(t *testing.T) {
	primary := &mockSTT{name: "Cloudflare", available: false, result: &TranscriptionResult{Transcript: "PRIMARY"}}
	backup := &mockSTT{name: "backup", available: true, result: &TranscriptionResult{Transcript: "BACKUP"}}
	got, usedBackup, err := transcribeWithBackup(primary, backup, []byte("a"), TranscriptionOptions{}, TranscriptionOptions{})
	if err != nil || !usedBackup || got.Transcript != "BACKUP" {
		t.Fatalf("transcript=%v usedBackup=%v err=%v", got, usedBackup, err)
	}
	if primary.calls != 0 {
		t.Fatalf("unavailable primary should not be called, calls=%d", primary.calls)
	}
}

func TestTranscribeWithBackupBothFail(t *testing.T) {
	primary := &mockSTT{name: "primary", available: true, err: fmt.Errorf("primary boom")}
	backup := &mockSTT{name: "backup", available: true, err: fmt.Errorf("backup boom")}
	_, usedBackup, err := transcribeWithBackup(primary, backup, []byte("a"), TranscriptionOptions{}, TranscriptionOptions{})
	if usedBackup || err == nil {
		t.Fatalf("usedBackup=%v err=%v", usedBackup, err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "primary boom") || !strings.Contains(msg, "backup boom") {
		t.Fatalf("combined error missing both causes: %s", msg)
	}
}

func TestTranscribeWithBackupNoBackup(t *testing.T) {
	primary := &mockSTT{name: "primary", available: true, err: fmt.Errorf("only primary")}
	_, usedBackup, err := transcribeWithBackup(primary, nil, []byte("a"), TranscriptionOptions{}, TranscriptionOptions{})
	if usedBackup || err == nil || err.Error() != "only primary" {
		t.Fatalf("usedBackup=%v err=%v", usedBackup, err)
	}
}
