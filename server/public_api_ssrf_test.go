// Copyright (C) 2026 Thinline Dynamic Solutions

package main

import (
	"net"
	"testing"
)

func TestIsBlockedWebhookIP(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.0.0.1", true},
		{"192.168.1.1", true},
		{"172.16.0.1", true},
		{"169.254.1.1", true},
		{"100.64.0.1", true},
		{"100.127.255.255", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if got := isBlockedWebhookIP(ip); got != c.blocked {
			t.Fatalf("%s: blocked=%v want %v", c.ip, got, c.blocked)
		}
	}
}

func TestValidatePublicWebhookURL(t *testing.T) {
	if err := validatePublicWebhookURL("http://hooks.slack.com/services/x", "slack"); err == nil {
		t.Fatal("expected http reject")
	}
	if err := validatePublicWebhookURL("https://example.com/hook", "slack"); err == nil {
		t.Fatal("expected slack host reject")
	}
	if err := validatePublicWebhookURL("https://hooks.slack.com/services/T/B/x", "slack"); err != nil {
		t.Fatalf("slack url: %v", err)
	}
	if err := validatePublicWebhookURL("https://discord.com/api/webhooks/1/x", "discord"); err != nil {
		t.Fatalf("discord url: %v", err)
	}
	if err := validatePublicWebhookURL("https://evil.example/x", "discord"); err == nil {
		t.Fatal("expected discord host reject")
	}
	if err := validatePublicWebhookURL("https://127.0.0.1/admin", "custom"); err == nil {
		t.Fatal("expected loopback reject")
	}
}
