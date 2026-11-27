package main

import "testing"

func TestConfigSaveKeepsRelaySession(t *testing.T) {
	o := NewOptions()
	o.RelayAccountUsername = "owner@example.com"
	o.RelayAccountRefreshToken = "refresh-token"

	for _, m := range []map[string]any{
		{},
		{"relayAccountRefreshToken": map[string]any{"set": true}, "relayAccountUsername": ""},
		{"relayAccountRefreshToken": "  "},
	} {
		o.FromMap(m)
		if o.RelayAccountRefreshToken != "refresh-token" || o.RelayAccountUsername != "owner@example.com" {
			t.Fatalf("config save %v signed the server out of the relay", m)
		}
	}

	o.FromMap(map[string]any{"relayAccountRefreshToken": "restored-token"})
	if o.RelayAccountRefreshToken != "restored-token" {
		t.Fatal("a real token from a full import should still be adopted")
	}
}

func TestRelaySignedInSurvivesFailedRefresh(t *testing.T) {
	c := &Controller{Options: NewOptions()}
	c.Options.RelayAccountRefreshToken = "refresh-token"
	if !c.RelayAccountSignedIn() {
		t.Fatal("a stored session must count as signed in without a live access token")
	}
}
