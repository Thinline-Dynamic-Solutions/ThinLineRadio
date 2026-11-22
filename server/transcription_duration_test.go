// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import "testing"

func TestBypassMinCallDurationForCall(t *testing.T) {
	if bypassMinCallDurationForCall(nil) {
		t.Fatal("nil call")
	}
	if bypassMinCallDurationForCall(&Call{}) {
		t.Fatal("nil talkgroup")
	}
	if bypassMinCallDurationForCall(&Call{Talkgroup: &Talkgroup{}}) {
		t.Fatal("plain talkgroup must not bypass")
	}
	if !bypassMinCallDurationForCall(&Call{Talkgroup: &Talkgroup{AlertingTalkgroup: true}}) {
		t.Fatal("alerting talkgroup must bypass")
	}
	if !bypassMinCallDurationForCall(&Call{Talkgroup: &Talkgroup{ToneDetectionEnabled: true}}) {
		t.Fatal("tone-detection talkgroup must bypass")
	}
	if !bypassMinCallDurationForCall(&Call{Talkgroup: &Talkgroup{AlertingTalkgroup: true, AlertEveryCall: false}}) {
		t.Fatal("keyword-only alerting talkgroup must still bypass")
	}
}

func TestAlertsOnEveryCall(t *testing.T) {
	if (&Talkgroup{AlertEveryCall: true}).AlertsOnEveryCall() {
		t.Fatal("plain talkgroup must not alert on every call")
	}
	if !(&Talkgroup{AlertingTalkgroup: true, AlertEveryCall: true}).AlertsOnEveryCall() {
		t.Fatal("alerting talkgroup with every-call on must alert on every call")
	}
	if (&Talkgroup{AlertingTalkgroup: true, AlertEveryCall: false}).AlertsOnEveryCall() {
		t.Fatal("keyword-only alerting talkgroup must not alert on every call")
	}
	if !NewTalkgroup().FromMap(map[string]any{"alertingTalkgroup": true}).AlertsOnEveryCall() {
		t.Fatal("alertEveryCall must default to true when absent")
	}
	if NewTalkgroup().FromMap(map[string]any{"alertingTalkgroup": true, "alertEveryCall": false}).AlertsOnEveryCall() {
		t.Fatal("alertEveryCall false must be kept")
	}
}

func TestEffectiveTranscriptionAudioSeconds(t *testing.T) {
	call := &Call{HasTones: true, ToneSequence: &ToneSequence{
		Tones: []Tone{{Duration: 3.5}, {Duration: 1.5}},
	}}
	if got := effectiveTranscriptionAudioSeconds(10, call); got != 5 {
		t.Fatalf("got %v want 5", got)
	}
	if got := effectiveTranscriptionAudioSeconds(4, call); got != 0 {
		t.Fatalf("clamped remaining: got %v", got)
	}
	plain := &Call{}
	if got := effectiveTranscriptionAudioSeconds(2.5, plain); got != 2.5 {
		t.Fatalf("plain: got %v", got)
	}
}

func TestAnyToFloat64(t *testing.T) {
	cases := []struct {
		in   any
		want float64
		ok   bool
	}{
		{float64(3.5), 3.5, true},
		{5, 5, true},
		{int64(7), 7, true},
		{"2.25", 2.25, true},
		{nil, 0, false},
	}
	for _, tc := range cases {
		got, ok := anyToFloat64(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Fatalf("in=%v got=(%v,%v) want=(%v,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
