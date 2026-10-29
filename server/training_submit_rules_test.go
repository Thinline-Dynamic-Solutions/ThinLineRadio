// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"strings"
	"testing"
)

func TestTrainingSubmitIssuesClean(t *testing.T) {
	text := "engine 12 on scene at 1234 main street crosses of elm avenue and oak road"
	if issues := trainingSubmitIssues(text, 12.5, true); len(issues) != 0 {
		t.Fatalf("clean clip flagged: %v", issues)
	}
	if issues := trainingSubmitIssues(text, 30, true); len(issues) != 0 {
		t.Fatalf("30 second clip flagged: %v", issues)
	}
}

func TestTrainingSubmitIssuesDuration(t *testing.T) {
	issues := trainingSubmitIssues("engine 12 on scene", 45, true)
	if len(issues) != 1 || !strings.Contains(issues[0], "45 seconds") || !strings.Contains(issues[0], "30 seconds or less") {
		t.Fatalf("duration issue = %v", issues)
	}
	unknown := trainingSubmitIssues("engine 12 on scene", 0, false)
	if len(unknown) != 1 || !strings.Contains(unknown[0], "could not be measured") {
		t.Fatalf("unknown duration = %v", unknown)
	}
}

func TestTrainingSubmitIssuesNonSpeech(t *testing.T) {
	for _, text := range []string{"no voice", "TONES ONLY", "tone only", "no voice, tones only", "static", "silence"} {
		issues := trainingSubmitIssues(text, 8, true)
		if len(issues) != 1 || !strings.Contains(issues[0], "not spoken words") {
			t.Fatalf("%q issues = %v", text, issues)
		}
	}
	spoken := trainingSubmitIssues("engine 12 no delay on scene", 8, true)
	if len(spoken) != 0 {
		t.Fatalf("spoken clip flagged: %v", spoken)
	}
}

func TestTrainingSubmitIssuesAbbreviations(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"1234 main ave", "ave → avenue"},
		{"squad at 10 elm blvd", "blvd → boulevard"},
		{"400 oak rd", "rd → road"},
		{"12 pine ln", "ln → lane"},
		{"squad 41 on scene at 123 main st", "st → street"},
		{"616 n state st", "n → north"},
		{"410 n 8th ave", "n → north"},
		{"123 main st nw", "nw → northwest"},
		{"responding rt 315", "rt → route"},
	}
	for _, tc := range cases {
		issues := trainingSubmitIssues(tc.text, 10, true)
		if len(issues) != 1 || !strings.Contains(issues[0], tc.want) {
			t.Fatalf("%q issues = %v, want %q", tc.text, issues, tc.want)
		}
	}
}

func TestTrainingSubmitIssuesAbbreviationExceptions(t *testing.T) {
	for _, text := range []string{
		"responding to st mary hospital",
		"dr smith is on scene",
		"engine 12 at 3rd street",
		"198 e street",
		"us 20 e main",
		"123 s main street",
	} {
		issues := trainingSubmitIssues(text, 10, true)
		for _, issue := range issues {
			if strings.Contains(issue, "Spell out abbreviations") {
				t.Fatalf("%q unexpectedly flagged: %v", text, issues)
			}
		}
	}
}

func TestTrainingSubmitIssuesNumberWords(t *testing.T) {
	issues := trainingSubmitIssues("engine twelve at forty two main street, 4 four", 10, true)
	if len(issues) != 1 || !strings.Contains(issues[0], "twelve") || !strings.Contains(issues[0], "forty") || !strings.Contains(issues[0], "four") {
		t.Fatalf("number issues = %v", issues)
	}
	plate := trainingSubmitIssues("plate oh 047 kn", 10, true)
	if len(plate) != 0 {
		t.Fatalf("letter oh flagged: %v", plate)
	}
}

func TestTrainingSubmitIssuesCombined(t *testing.T) {
	issues := trainingSubmitIssues("no voice", 45, true)
	if len(issues) != 2 {
		t.Fatalf("want duration and label, got %v", issues)
	}
}
