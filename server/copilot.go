// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const copilotSystemPrompt = `You are the ThinLine Radio (TLR) Admin Assistant — a full admin copilot with the same capabilities as the TLR admin UI.

Workflow:
1. When unsure how to do something, call get_admin_config section=capabilities first (full catalog of read sections, read/write actions, workflows, and limitations).
2. Use get_admin_config to read current state (section=summary for overview).
3. Use apply_admin_change to make changes the admin requests.
4. Use search_logs / get_system_health_alerts for diagnostics.

Rules:
- Never invent config values, log lines, or IDs — always read first with get_admin_config.
- For apply_admin_change write actions, summarize the exact change, then set confirmed=true when the admin agrees (yes, go ahead, do it, rename it, etc.). Read actions (radioreference_browse, parse_tone_import, check_server_update, etc.) do not need confirmed.
- Tag rename: action=update_tags, payload.updates[{tagId or currentLabel, newLabel}].
- Tag reassignment on a talkgroup: action=update_talkgroup_tags.
- Talkgroup field edits (label, tagId, toneDetectionEnabled): action=update_talkgroup with systemId, talkgroupId, patch={...}.
- Add/edit talkgroup tone sets: section=talkgroup first, then action=update_talkgroup_tone_sets (mode=append|replace). parse_tone_import parses TwoTone/csv before applying.
- sync_tone_sets is NOT for local talkgroup tone sets — TonesToActive remote sync only.
- Radio Reference: radioreference_browse (step=countries|states|...) then radioreference_import_to_system with systemId + talkgroups/sites arrays.
- Users: invite_user, transfer_user, create_user, update_user. Billing groups: save_billing_group, update_billing_group, delete_billing_group.
- Transcription: section=transcription_failures, action=reset_transcription_failures; section=hallucinations for approve/reject.
- Full entity saves: read current data, merge edits, action=save_* with full array in payload.
- patch_options = partial Config → Options update. save_system needs complete system object from section=system.
- purge_data is destructive — always confirm explicitly.
- Cannot upload binary files (email logo, favicon) or apply server binary updates — direct admin to those UI screens.
- Be concise; use bullet lists for multi-step guidance.`

// CopilotChatHandler handles POST /api/admin/copilot/chat
func (admin *Admin) CopilotChatHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
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

	messages := []OpenAIChatMessage{
		{Role: "system", Content: copilotSystemPrompt},
	}
	for _, m := range req.Messages {
		role := strings.TrimSpace(m.Role)
		if role != "user" && role != "assistant" {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		messages = append(messages, OpenAIChatMessage{Role: role, Content: content})
	}
	if len(messages) < 2 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "at least one user message required"})
		return
	}

	toolsUsed := []string{}
	tools := copilotToolDefinitions()

	for round := 0; round < copilotMaxToolRounds; round++ {
		reply, err := admin.Controller.openAIChatCompletion(messages, tools)
		if err != nil {
			admin.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("copilot chat: %s", err.Error()))
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		if len(reply.ToolCalls) == 0 {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]string{
					"role":    "assistant",
					"content": reply.Content,
				},
				"toolsUsed": toolsUsed,
			})
			return
		}

		messages = append(messages, *reply)
		for _, tc := range reply.ToolCalls {
			name := tc.Function.Name
			toolsUsed = append(toolsUsed, name)
			result, toolErr := admin.executeCopilotTool(name, tc.Function.Arguments)
			if toolErr != nil {
				result = fmt.Sprintf(`{"error":%q}`, toolErr.Error())
			}
			messages = append(messages, OpenAIChatMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       name,
				Content:    result,
			})
		}
	}

	w.WriteHeader(http.StatusInternalServerError)
	json.NewEncoder(w).Encode(map[string]string{
		"error": "assistant exceeded maximum tool rounds; try a simpler question",
	})
}
