package main

import "testing"

func TestNormalizeUIAccentColor(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", defaultUIAccentColor},
		{"#FF5B2E", "#ff5b2e"},
		{"ff5b2e", "#ff5b2e"},
		{"#5fd", "#55ffdd"},
		{"#5FD0FF", "#5fd0ff"},
		{"not-a-color", defaultUIAccentColor},
		{"#gg0000", defaultUIAccentColor},
		{"#fff", "#ffffff"},
		{"  #38E0A4  ", "#38e0a4"},
	}
	for _, c := range cases {
		if got := normalizeUIAccentColor(c.in); got != c.want {
			t.Errorf("normalizeUIAccentColor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
