package main

import "testing"

func TestOptionsMaskedMapHidesCredentials(t *testing.T) {
	o := NewOptions()
	o.EmailSmtpPassword = "hunter2"
	o.StripeSecretKey = ""
	o.MaxClients = 42
	o.TranscriptionConfig.GeminiAPIKey = "gem-secret"
	o.OpenAIIntegration.APIKey = "sk-secret"
	o.OpenAIIntegration.Model = "gpt"

	m, err := o.MaskedMap()
	if err != nil {
		t.Fatal(err)
	}

	if got := m["maxClients"]; got != float64(42) {
		t.Fatalf("maxClients = %v, want 42", got)
	}
	pw, ok := m["emailSmtpPassword"].(map[string]any)
	if !ok || pw["set"] != true {
		t.Fatalf("emailSmtpPassword not masked as set: %#v", m["emailSmtpPassword"])
	}
	sk, ok := m["stripeSecretKey"].(map[string]any)
	if !ok || sk["set"] != false {
		t.Fatalf("empty stripeSecretKey should be {set:false}: %#v", m["stripeSecretKey"])
	}
	tc := m["transcriptionConfig"].(map[string]any)
	if g, ok := tc["geminiAPIKey"].(map[string]any); !ok || g["set"] != true {
		t.Fatalf("nested geminiAPIKey not masked: %#v", tc["geminiAPIKey"])
	}
	oa := m["openAIIntegration"].(map[string]any)
	if a, ok := oa["apiKey"].(map[string]any); !ok || a["set"] != true {
		t.Fatalf("openAI apiKey not masked: %#v", oa["apiKey"])
	}
	if oa["model"] != "gpt" {
		t.Fatalf("openAI model should pass through, got %v", oa["model"])
	}
}
