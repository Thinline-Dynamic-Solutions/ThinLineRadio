// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	_ "embed"
	"encoding/json"
)

//go:embed copilot_capabilities.json
var copilotCapabilitiesEmbedded []byte

var copilotCapabilitiesData map[string]any

func init() {
	_ = json.Unmarshal(copilotCapabilitiesEmbedded, &copilotCapabilitiesData)
}

func getCopilotCapabilities() map[string]any {
	if copilotCapabilitiesData == nil {
		return map[string]any{}
	}
	return copilotCapabilitiesData
}
