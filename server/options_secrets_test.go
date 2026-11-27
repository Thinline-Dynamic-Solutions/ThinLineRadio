package main

import "testing"

func TestProtectOptionSecretsPreserveAll(t *testing.T) {
	opts := &Options{}
	opts.TranscriptionConfig.WhisperAPIKey = "good-whisper"
	opts.TranscriptionConfig.AssemblyAIKey = "good-assembly"
	opts.OpenAIIntegration.APIKey = "good-openai"

	incoming := map[string]any{
		"transcriptionConfig": map[string]any{
			"whisperAPIKey": "stale-whisper",
			"assemblyAIKey": "",
			"language":      "en",
		},
		"openAIIntegration": map[string]any{"apiKey": ""},
	}
	opts.protectOptionSecrets(incoming, true)

	tc := incoming["transcriptionConfig"].(map[string]any)
	if tc["whisperAPIKey"] != "good-whisper" || tc["assemblyAIKey"] != "good-assembly" {
		t.Fatalf("stored keys were not restored: %#v", tc)
	}
	if tc["language"] != "en" {
		t.Fatalf("non-secret field changed: %#v", tc)
	}
	if incoming["openAIIntegration"].(map[string]any)["apiKey"] != "good-openai" {
		t.Fatalf("openai key not restored")
	}
}

func TestProtectOptionSecretsDropsBlanksOnImport(t *testing.T) {
	opts := &Options{}
	opts.TranscriptionConfig.WhisperAPIKey = "good-whisper"

	incoming := map[string]any{
		"transcriptionConfig": map[string]any{
			"whisperAPIKey": "  ",
			"azureKey":      "imported-azure",
		},
	}
	opts.protectOptionSecrets(incoming, false)

	tc := incoming["transcriptionConfig"].(map[string]any)
	if _, present := tc["whisperAPIKey"]; present {
		t.Fatalf("blank key should have been dropped: %#v", tc)
	}
	if tc["azureKey"] != "imported-azure" {
		t.Fatalf("non-blank imported key should be kept: %#v", tc)
	}
}

func TestProtectOptionSecretsMapboxToken(t *testing.T) {
	opts := &Options{}
	opts.MappingIntegration.MapboxAccessToken = "pk.good"

	stale := map[string]any{"mappingIntegration": map[string]any{"mapboxAccessToken": "", "newPinFlashSeconds": float64(3)}}
	opts.protectOptionSecrets(stale, true)
	if got := stale["mappingIntegration"].(map[string]any)["mapboxAccessToken"]; got != "pk.good" {
		t.Fatalf("stale save replaced the Mapbox token with %q", got)
	}

	imported := map[string]any{"mappingIntegration": map[string]any{"mapboxAccessToken": " "}}
	opts.protectOptionSecrets(imported, false)
	if _, present := imported["mappingIntegration"].(map[string]any)["mapboxAccessToken"]; present {
		t.Fatal("blank imported Mapbox token should have been dropped")
	}
}

func TestDescribeSecretChangesHidesValues(t *testing.T) {
	changes := describeSecretChanges(
		map[string]string{"transcriptionConfig.whisperAPIKey": "old-secret"},
		map[string]string{"transcriptionConfig.whisperAPIKey": ""},
	)
	if len(changes) != 1 {
		t.Fatalf("expected one change, got %v", changes)
	}
	if got := changes[0]; got == "" || len(got) > 80 {
		t.Fatalf("unexpected description %q", got)
	}
}
