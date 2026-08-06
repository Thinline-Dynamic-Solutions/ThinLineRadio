// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type nopFlusher struct{}

func (nopFlusher) Flush() {}

// CopilotChatStreamHandler handles POST /api/admin/copilot/chat/stream (NDJSON events).
func (admin *Admin) CopilotChatStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	auth := admin.GetAuthorization(r)
	if !admin.ValidateToken(auth) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}
	if len(req.Messages) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "messages required"})
		return
	}
	if strings.TrimSpace(admin.Controller.Options.OpenAIIntegration.APIKey) == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "OpenAI API key is not configured. Set it under Config → Options → Integrations → OpenAI Integration.",
		})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		// Fallback: unwrap nested ResponseWriters (middleware) until we find a Flusher.
		if uw, ok := w.(interface{ Unwrap() http.ResponseWriter }); ok {
			if f, ok := uw.Unwrap().(http.Flusher); ok {
				flusher = f
				ok = true
			}
		}
	}
	if !ok {
		// Last resort: continue without flush (response still works, just less progressive).
		flusher = nopFlusher{}
	}

	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	writeEvent := func(ev copilotStreamEvent) {
		b, err := json.Marshal(ev)
		if err != nil {
			return
		}
		b = append(b, '\n')
		_, _ = w.Write(b)
		flusher.Flush()
	}

	msgs := make([]OpenAIChatMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, OpenAIChatMessage{Role: m.Role, Content: m.Content})
	}

	_, _, err := admin.runCopilotChat(copilotRunRequest{
		Messages:  msgs,
		AuthToken: auth,
		Emit:      writeEvent,
	})
	if err != nil {
		admin.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("copilot stream: %s", err.Error()))
		writeEvent(copilotStreamEvent{Type: "error", Error: err.Error()})
	}
}
