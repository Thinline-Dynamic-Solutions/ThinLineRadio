// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"strings"
	"testing"
)

func TestCopilotPrepareSQL_SelectAllowed(t *testing.T) {
	sql, err := copilotPrepareSQL("SELECT id, system FROM calls WHERE id > 1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.ToUpper(sql), "SELECT") {
		t.Fatalf("unexpected sql: %s", sql)
	}
}

func TestCopilotPrepareSQL_BlocksDDL(t *testing.T) {
	_, err := copilotPrepareSQL("DROP TABLE calls")
	if err == nil {
		t.Fatal("expected DROP to be denied")
	}
}

func TestCopilotPrepareSQL_BlocksMultiStatement(t *testing.T) {
	_, err := copilotPrepareSQL("SELECT 1; DELETE FROM calls")
	if err == nil {
		t.Fatal("expected multi-statement to be denied")
	}
}

func TestCopilotPrepareSQL_UnknownTableDenied(t *testing.T) {
	_, err := copilotPrepareSQL("DELETE FROM options WHERE key='x'")
	if err == nil {
		t.Fatal("expected unknown/non-allowlisted table to be denied")
	}
}

func TestCopilotSanitizeRedactsSecrets(t *testing.T) {
	raw := `{"ok":true,"openaiApiKey":"sk-secret","nested":{"password":"hunter2"}}`
	out := copilotSanitizeToolResult(raw)
	if strings.Contains(out, "sk-secret") || strings.Contains(out, "hunter2") {
		t.Fatalf("secrets leaked: %s", out)
	}
	if !strings.Contains(out, "[redacted]") {
		t.Fatalf("expected redaction markers: %s", out)
	}
}

func TestCopilotTruncateHistory(t *testing.T) {
	msgs := make([]OpenAIChatMessage, 0, 40)
	for i := 0; i < 40; i++ {
		msgs = append(msgs, OpenAIChatMessage{Role: "user", Content: "x"})
	}
	out := copilotTruncateHistory(msgs)
	if len(out) != copilotMaxHistoryMessages {
		t.Fatalf("got %d want %d", len(out), copilotMaxHistoryMessages)
	}
}
