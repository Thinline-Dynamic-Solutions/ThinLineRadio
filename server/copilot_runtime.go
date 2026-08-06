// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

const (
	copilotMaxToolRounds     = 15
	copilotMaxHistoryMessages = 24 // user+assistant turns kept (plus system)
)

type copilotStreamEvent struct {
	Type    string `json:"type"` // status | tool_start | tool_end | message | done | error
	Message string `json:"message,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Summary string `json:"summary,omitempty"`
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
	Tools   []string `json:"toolsUsed,omitempty"`
	Error   string `json:"error,omitempty"`
	NeedsConfirm bool `json:"needsConfirm,omitempty"`
}

type copilotEmitFunc func(copilotStreamEvent)

type copilotRunRequest struct {
	Messages  []OpenAIChatMessage
	AuthToken string
	Emit      copilotEmitFunc
}

func (admin *Admin) runCopilotChat(req copilotRunRequest) (assistantContent string, toolsUsed []string, err error) {
	emit := req.Emit
	if emit == nil {
		emit = func(copilotStreamEvent) {}
	}

	system := copilotSystemPrompt + "\n\n" + copilotCompactActionIndex()
	messages := []OpenAIChatMessage{{Role: "system", Content: system}}
	messages = append(messages, copilotTruncateHistory(req.Messages)...)
	if len(messages) < 2 {
		return "", nil, fmt.Errorf("at least one user message required")
	}

	tools := copilotToolDefinitions()
	toolsUsed = []string{}

	for round := 0; round < copilotMaxToolRounds; round++ {
		emit(copilotStreamEvent{Type: "status", Message: "Calling model…"})
		reply, chatErr := admin.Controller.openAIChatCompletion(messages, tools)
		if chatErr != nil {
			return "", toolsUsed, chatErr
		}

		if len(reply.ToolCalls) == 0 {
			content := strings.TrimSpace(reply.Content)
			emit(copilotStreamEvent{Type: "message", Role: "assistant", Content: content})
			emit(copilotStreamEvent{Type: "done", Content: content, Tools: toolsUsed})
			return content, toolsUsed, nil
		}

		messages = append(messages, *reply)

		type toolOutcome struct {
			ID      string
			Name    string
			Result  string
			Summary string
		}
		outcomes := make([]toolOutcome, len(reply.ToolCalls))

		var eg errgroup.Group
		var mu sync.Mutex
		for i, tc := range reply.ToolCalls {
			i, tc := i, tc
			name := tc.Function.Name
			mu.Lock()
			toolsUsed = append(toolsUsed, name)
			mu.Unlock()
			emit(copilotStreamEvent{Type: "tool_start", Tool: name, Message: "Running " + name + "…"})
			eg.Go(func() error {
				result, toolErr := admin.executeCopilotToolCtx(name, tc.Function.Arguments, req.AuthToken)
				if toolErr != nil {
					result = fmt.Sprintf(`{"error":%q}`, toolErr.Error())
				}
				result = copilotSanitizeToolResult(result)
				summary := copilotToolResultSummary(name, result)
				needsConfirm := strings.Contains(summary, "needsConfirm")
				outcomes[i] = toolOutcome{ID: tc.ID, Name: name, Result: result, Summary: summary}
				emit(copilotStreamEvent{Type: "tool_end", Tool: name, Summary: summary, NeedsConfirm: needsConfirm})
				return nil
			})
		}
		_ = eg.Wait()

		for _, o := range outcomes {
			messages = append(messages, OpenAIChatMessage{
				Role:       "tool",
				ToolCallID: o.ID,
				Name:       o.Name,
				Content:    o.Result,
			})
		}

		// Soft signal if any tool needs confirmation (UI can show Confirm chips early).
		for _, o := range outcomes {
			if strings.Contains(o.Summary, "needsConfirm") {
				emit(copilotStreamEvent{Type: "status", Message: "Waiting for confirmation…", NeedsConfirm: true})
				break
			}
		}
	}

	return "", toolsUsed, fmt.Errorf("assistant exceeded maximum tool rounds; try a simpler question")
}

func copilotTruncateHistory(msgs []OpenAIChatMessage) []OpenAIChatMessage {
	filtered := make([]OpenAIChatMessage, 0, len(msgs))
	for _, m := range msgs {
		role := strings.TrimSpace(m.Role)
		if role != "user" && role != "assistant" {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if content == "" && len(m.ToolCalls) == 0 {
			continue
		}
		filtered = append(filtered, OpenAIChatMessage{Role: role, Content: content})
	}
	if len(filtered) <= copilotMaxHistoryMessages {
		return filtered
	}
	return filtered[len(filtered)-copilotMaxHistoryMessages:]
}

func copilotToolResultSummary(name, result string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		if len(result) > 120 {
			return result[:120] + "…"
		}
		return result
	}
	parts := []string{name}
	if v, ok := m["error"]; ok {
		parts = append(parts, fmt.Sprintf("error=%v", v))
	}
	if v, ok := m["needsConfirm"]; ok && v == true {
		parts = append(parts, "needsConfirm")
	}
	if v, ok := m["applied"]; ok {
		parts = append(parts, fmt.Sprintf("applied=%v", v))
	}
	if v, ok := m["ok"]; ok {
		parts = append(parts, fmt.Sprintf("ok=%v", v))
	}
	if v, ok := m["count"]; ok {
		parts = append(parts, fmt.Sprintf("count=%v", v))
	}
	if v, ok := m["truncated"]; ok && v == true {
		parts = append(parts, "truncated")
	}
	if v, ok := m["denied"]; ok && v == true {
		parts = append(parts, "denied")
	}
	return strings.Join(parts, " ")
}
