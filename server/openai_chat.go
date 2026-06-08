// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultOpenAIChatModel = "gpt-5.4-mini"

// OpenAI chat model pricing per 1M tokens (input, output) for admin UI estimates.
var openAIChatModelPricing = map[string][2]float64{
	"gpt-4o-mini":    {0.15, 0.60},
	"gpt-4o":         {2.50, 10.00},
	"gpt-5.4-mini":   {0.75, 4.50},
}

// SupportedOpenAIChatModels lists models selectable in admin (order preserved for UI).
var SupportedOpenAIChatModels = []string{
	"gpt-5.4-mini",
	"gpt-4o-mini",
	"gpt-4o",
}

func (oai OpenAIIntegration) resolvedChatModel() string {
	m := strings.TrimSpace(oai.Model)
	if m == "" {
		return defaultOpenAIChatModel
	}
	for _, supported := range SupportedOpenAIChatModels {
		if m == supported {
			return m
		}
	}
	return defaultOpenAIChatModel
}

// EstimateOpenAINamingCostUSD returns an approximate cost for one unit/tone naming request.
func EstimateOpenAINamingCostUSD(model string) float64 {
	pricing, ok := openAIChatModelPricing[model]
	if !ok {
		pricing = openAIChatModelPricing[defaultOpenAIChatModel]
	}
	const estInputTokens = 2500.0
	const estOutputTokens = 20.0
	return (estInputTokens/1_000_000)*pricing[0] + (estOutputTokens/1_000_000)*pricing[1]
}

func (controller *Controller) openAIChatJSON(systemPrompt, userPrompt string) (string, error) {
	oai := controller.Options.OpenAIIntegration
	apiKey := strings.TrimSpace(oai.APIKey)
	if apiKey == "" {
		return "", fmt.Errorf("openai api key not configured")
	}

	model := oai.resolvedChatModel()
	baseURL := resolveOpenAIBaseURL(oai.BaseURL)

	payload := map[string]interface{}{
		"model":           model,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"temperature": 0.2,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", err
	}
	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("openai returned no choices")
	}
	return chatResp.Choices[0].Message.Content, nil
}

// OpenAIChatMessage is a chat completion message (user, assistant, system, or tool).
type OpenAIChatMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []OpenAIToolCall   `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

// OpenAIToolCall is a function invocation requested by the model.
type OpenAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAIToolDef struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

func (controller *Controller) openAIChatCompletion(messages []OpenAIChatMessage, tools []openAIToolDef) (*OpenAIChatMessage, error) {
	oai := controller.Options.OpenAIIntegration
	apiKey := strings.TrimSpace(oai.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("openai api key not configured")
	}

	model := oai.resolvedChatModel()
	baseURL := resolveOpenAIBaseURL(oai.BaseURL)

	payload := map[string]any{
		"model":       model,
		"messages":    messages,
		"temperature": 0.3,
	}
	if len(tools) > 0 {
		payload["tools"] = tools
		payload["tool_choice"] = "auto"
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp struct {
		Choices []struct {
			Message OpenAIChatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, err
	}
	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("openai returned no choices")
	}
	msg := chatResp.Choices[0].Message
	return &msg, nil
}
