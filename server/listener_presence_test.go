package main

import "testing"

func TestListenerClientKind(t *testing.T) {
	cases := map[string]string{
		"":                                "unknown",
		"Mozilla/5.0 (Windows NT 10.0)":   "web",
		"Dart/3.4 (dart:io)":              "mobile",
		"ThinLineRadio/1.2":               "mobile",
		"okhttp/4.12.0":                   "mobile",
		"CountyPulse/2 CFNetwork Darwin":  "mobile",
	}
	for ua, want := range cases {
		if got := listenerClientKind(ua); got != want {
			t.Errorf("listenerClientKind(%q) = %q, want %q", ua, got, want)
		}
	}
}

func TestClipListenerText(t *testing.T) {
	if got := clipListenerText("  abc  ", 10); got != "abc" {
		t.Fatalf("trim: %q", got)
	}
	if got := clipListenerText("abcdef", 3); got != "abc" {
		t.Fatalf("clip: %q", got)
	}
}
