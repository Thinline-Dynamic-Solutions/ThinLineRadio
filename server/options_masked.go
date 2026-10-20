// Copyright (C) 2024 Thinline Dynamic Solutions
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
	"encoding/json"
	"fmt"
)

// sensitiveOptionKeys lists option keys (flat or nested JSON keys) whose values are
// credentials. They are never returned in clear text by GET /api/admin/options;
// the response carries {"set": true|false} instead so a remote manager (Central
// Management) can show whether a secret is configured without ever holding it.
//
// Keep in sync with the "sensitive" flags in central-management's settings catalog.
var sensitiveOptionKeys = map[string]bool{
	"radioReferencePassword":   true,
	"radioReferenceAPIKey":     true,
	"emailServiceApiKey":       true,
	"emailSendGridApiKey":      true,
	"emailMailgunApiKey":       true,
	"emailSmtpPassword":        true,
	"stripeSecretKey":          true,
	"stripeWebhookSecret":      true,
	"relayServerAPIKey":        true,
	"relayAccountRefreshToken": true,
	"turnstileSecretKey":       true,
	"centralManagementAPIKey":  true,
	"hydraAPIKey":              true,
	// nested: transcriptionConfig
	"whisperAPIKey":      true,
	"azureKey":           true,
	"googleAPIKey":       true,
	"googleCredentials":  true,
	"geminiAPIKey":       true,
	"assemblyAIKey":      true,
	"cloudflareAPIToken": true,
	"collectorAPIKey":    true,
	// nested: openAIIntegration
	"apiKey": true,
}

// maskOptionsValue replaces credential values with a {"set": bool} marker, recursing
// into nested objects.
func maskOptionsValue(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if sensitiveOptionKeys[k] {
			set := false
			if s, ok := v.(string); ok {
				set = s != ""
			} else if v != nil {
				set = true
			}
			out[k] = map[string]any{"set": set}
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			out[k] = maskOptionsValue(nested)
			continue
		}
		out[k] = v
	}
	return out
}

// MaskedMap returns the current options as a generic map with all credentials masked.
func (options *Options) MaskedMap() (map[string]any, error) {
	options.mutex.Lock()
	raw, err := json.Marshal(options)
	options.mutex.Unlock()
	if err != nil {
		return nil, fmt.Errorf("options MaskedMap marshal: %w", err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("options MaskedMap unmarshal: %w", err)
	}
	return maskOptionsValue(m), nil
}
