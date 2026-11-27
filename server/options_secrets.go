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
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// transcriptionSecretKeys are the speech-to-text credentials stored inside the
// transcriptionConfig options blob. They are all sent back by the admin UI on
// every save, so a stale form could silently replace a good key.
var transcriptionSecretKeys = []string{
	"whisperAPIKey",
	"azureKey",
	"googleAPIKey",
	"googleCredentials",
	"geminiAPIKey",
	"assemblyAIKey",
	"cloudflareAPIToken",
}

func (options *Options) transcriptionSecretValue(key string) string {
	tc := &options.TranscriptionConfig
	switch key {
	case "whisperAPIKey":
		return tc.WhisperAPIKey
	case "azureKey":
		return tc.AzureKey
	case "googleAPIKey":
		return tc.GoogleAPIKey
	case "googleCredentials":
		return tc.GoogleCredentials
	case "geminiAPIKey":
		return tc.GeminiAPIKey
	case "assemblyAIKey":
		return tc.AssemblyAIKey
	case "cloudflareAPIToken":
		return tc.CloudflareAPIToken
	}
	return ""
}

// transcriptionSecretSnapshot returns the current transcription credentials plus
// the OpenAI integration key, keyed by a stable field name.
func (options *Options) transcriptionSecretSnapshot() map[string]string {
	options.mutex.Lock()
	defer options.mutex.Unlock()

	snap := make(map[string]string, len(transcriptionSecretKeys)+1)
	for _, k := range transcriptionSecretKeys {
		snap["transcriptionConfig."+k] = options.transcriptionSecretValue(k)
	}
	snap["openAIIntegration.apiKey"] = options.OpenAIIntegration.APIKey
	return snap
}

// secretFingerprint is a short, non-reversible tag so logs can show which key
// replaced which without revealing either value.
func secretFingerprint(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "empty"
	}
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])[:8]
}

// describeSecretChanges lists the secret fields whose value differs between two
// snapshots, using fingerprints only.
func describeSecretChanges(before, after map[string]string) []string {
	var changes []string
	for k, b := range before {
		if a := after[k]; a != b {
			changes = append(changes, k+" "+secretFingerprint(b)+"->"+secretFingerprint(a))
		}
	}
	sort.Strings(changes)
	return changes
}

// protectOptionSecrets keeps stored credentials safe when a whole-options map
// is applied with FromMap, which would otherwise accept any string (including a
// blank or outdated one) as the new key.
//
//   - preserveAll=true: every incoming credential is replaced by the stored one.
//     Used for tools that load the config earlier and save it back whole
//     (import talkgroups/units, password settings) and never meant to edit keys.
//   - preserveAll=false: only blank incoming credentials are dropped, so a
//     config import never wipes a working key with an empty one.
func (options *Options) protectOptionSecrets(incoming map[string]any, preserveAll bool) {
	if incoming == nil {
		return
	}

	options.mutex.Lock()
	currentTC := make(map[string]string, len(transcriptionSecretKeys))
	for _, k := range transcriptionSecretKeys {
		currentTC[k] = options.transcriptionSecretValue(k)
	}
	currentOpenAI := options.OpenAIIntegration.APIKey
	options.mutex.Unlock()

	if tc, ok := incoming["transcriptionConfig"].(map[string]any); ok {
		for _, k := range transcriptionSecretKeys {
			v, present := tc[k]
			if !present {
				continue
			}
			if preserveAll {
				tc[k] = currentTC[k]
				continue
			}
			if s, isString := v.(string); isString && strings.TrimSpace(s) == "" {
				delete(tc, k)
			}
		}
	}

	if oai, ok := incoming["openAIIntegration"].(map[string]any); ok {
		if v, present := oai["apiKey"]; present {
			if preserveAll {
				oai["apiKey"] = currentOpenAI
			} else if s, isString := v.(string); isString && strings.TrimSpace(s) == "" {
				delete(oai, "apiKey")
			}
		}
	}
}
