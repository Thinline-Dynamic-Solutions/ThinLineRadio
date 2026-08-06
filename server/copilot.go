// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const copilotSystemPrompt = `You are the ThinLine Radio (TLR) Admin Assistant — a full admin/server copilot with access to config, admin APIs, and guarded SQL.

IDs (critical — do not confuse):
- systemId = database PK (e.g. OH Trumbull is often systemId 44)
- systemRef = radio system number (OH Trumbull systemRef may be 78 — that is NOT a talkgroup)
- talkgroupId = database PK
- talkgroupRef = radio TGID (e.g. "78 FD DISP" may be talkgroupRef 46043)
When a user says "talkgroup 78 FD dispatch on Trumbull", use section=find or section=tone_sets with query/systemLabel/talkgroupLabel — never guess IDs.

Workflow:
1. Discover with get_admin_config section=find query="…" or section=tone_sets / section=talkgroup with systemLabel+talkgroupLabel.
2. Use run_admin_action for admin APIs (mapping, call natures, unit aliases, transcript review, systems order, FS browse, bulk users). actionId=list if unsure.
3. Use apply_admin_change for legacy writes (update_talkgroup_tone_sets, patch_options, tags, users, etc.) with confirmed=true after summarizing.
4. Use db_query freely for diagnostics (SELECT on allowlisted tables). Writes need confirmed=true. Prefer SQL when a hand-written section is missing — explore, don't stop.
5. search_logs / get_system_health_alerts for ops diagnostics.

Rules:
- Never invent IDs or config — resolve first (find / SQL).
- On tool errors, retry with find/SQL instead of telling the admin you cannot do it.
- Writes require confirmation (Confirm chip or confirmed=true).
- Secrets are redacted. No DDL, no binary uploads, no server binary updates.
- Be concise; return the actual data the admin asked for.`

// CopilotChatHandler handles POST /api/admin/copilot/chat (non-streaming JSON).
func (admin *Admin) CopilotChatHandler(w http.ResponseWriter, r *http.Request) {
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

	msgs := make([]OpenAIChatMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, OpenAIChatMessage{Role: m.Role, Content: m.Content})
	}

	content, toolsUsed, err := admin.runCopilotChat(copilotRunRequest{
		Messages:  msgs,
		AuthToken: auth,
	})
	if err != nil {
		admin.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("copilot chat: %s", err.Error()))
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "maximum tool rounds") {
			status = http.StatusInternalServerError
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"message": map[string]string{
			"role":    "assistant",
			"content": content,
		},
		"toolsUsed": toolsUsed,
	})
}
