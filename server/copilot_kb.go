// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed copilot_kb.json
var copilotKBEmbedded []byte

type copilotKBArticle struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Keywords string `json:"keywords"`
	Body     string `json:"body"`
}

type copilotKB struct {
	Version  string             `json:"version"`
	Articles []copilotKBArticle `json:"articles"`
}

var copilotKBData copilotKB

func init() {
	_ = json.Unmarshal(copilotKBEmbedded, &copilotKBData)
}

func searchCopilotKB(query string, limit int) []copilotKBArticle {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" || limit <= 0 {
		return nil
	}
	terms := strings.Fields(query)
	type scored struct {
		article copilotKBArticle
		score   int
	}
	var matches []scored
	for _, a := range copilotKBData.Articles {
		hay := strings.ToLower(a.Title + " " + a.Keywords + " " + a.Body)
		score := 0
		for _, t := range terms {
			if strings.Contains(hay, t) {
				score++
			}
		}
		if score > 0 {
			matches = append(matches, scored{article: a, score: score})
		}
	}
	for i := 0; i < len(matches); i++ {
		for j := i + 1; j < len(matches); j++ {
			if matches[j].score > matches[i].score {
				matches[i], matches[j] = matches[j], matches[i]
			}
		}
	}
	if len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]copilotKBArticle, len(matches))
	for i, m := range matches {
		out[i] = m.article
	}
	return out
}
