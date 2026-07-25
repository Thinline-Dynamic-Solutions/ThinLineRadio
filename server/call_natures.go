// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// CallNature is a dispatch incident category with optional transcript phrases
// that map transcripts to the canonical label shown on call cards.
type CallNature struct {
	Id      uint64
	Label   string
	Phrases []string
	Enabled bool
	Order   uint
	// ExpireMinutes force-expires this category's incidents off the live map
	// that many minutes after dispatch, overriding the viewer's selected time
	// range. Zero means incidents never force-expire.
	ExpireMinutes uint
	CreatedAt     int64
}

// CallNatureMatchData is the flattened catalog passed into the mapping pipeline.
type CallNatureMatchData struct {
	Labels            []string
	MatchTerms        []string
	PhraseToLabel     map[string]string
	OpenAIClassify    bool
}

type CallNaturesCache struct {
	controller *Controller
	mutex      sync.RWMutex
	natures    []*CallNature
}

func NewCallNaturesCache(controller *Controller) *CallNaturesCache {
	return &CallNaturesCache{controller: controller}
}

func (cache *CallNaturesCache) Read(db *Database) error {
	if db == nil || db.Sql == nil {
		return fmt.Errorf("database unavailable")
	}
	rows, err := db.Sql.Query(`SELECT "callNatureId", "label", "phrases", "enabled", "order", "expireMinutes", "createdAt"
		FROM "callNatures" ORDER BY "order" ASC, "label" ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var loaded []*CallNature
	for rows.Next() {
		var (
			n           CallNature
			phrasesJSON string
			enabled     bool
		)
		if err := rows.Scan(&n.Id, &n.Label, &phrasesJSON, &enabled, &n.Order, &n.ExpireMinutes, &n.CreatedAt); err != nil {
			continue
		}
		n.Enabled = enabled
		n.Label = strings.ToUpper(strings.TrimSpace(n.Label))
		if phrasesJSON != "" && phrasesJSON != "[]" {
			_ = json.Unmarshal([]byte(phrasesJSON), &n.Phrases)
		}
		loaded = append(loaded, &n)
	}

	cache.mutex.Lock()
	cache.natures = loaded
	cache.mutex.Unlock()
	return nil
}

func (cache *CallNaturesCache) GetAll() []*CallNature {
	cache.mutex.RLock()
	defer cache.mutex.RUnlock()
	out := make([]*CallNature, len(cache.natures))
	copy(out, cache.natures)
	return out
}

func (cache *CallNaturesCache) MatchData(openAIClassify bool) CallNatureMatchData {
	cache.mutex.RLock()
	defer cache.mutex.RUnlock()

	data := CallNatureMatchData{
		PhraseToLabel:  map[string]string{},
		OpenAIClassify: openAIClassify,
	}
	seenTerm := map[string]bool{}
	addTerm := func(term string) {
		term = strings.ToUpper(strings.TrimSpace(term))
		if term == "" || seenTerm[term] {
			return
		}
		seenTerm[term] = true
		data.MatchTerms = append(data.MatchTerms, term)
	}

	for _, n := range cache.natures {
		if n == nil || !n.Enabled || n.Label == "" {
			continue
		}
		data.Labels = append(data.Labels, n.Label)
		data.PhraseToLabel[n.Label] = n.Label
		addTerm(n.Label)
		for _, p := range n.Phrases {
			p = strings.ToUpper(strings.TrimSpace(p))
			if p == "" {
				continue
			}
			data.PhraseToLabel[p] = n.Label
			addTerm(p)
		}
	}
	sort.Strings(data.Labels)
	return data
}

func migrateCallNatures(db *Database) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS "callNatures" (
			"callNatureId" bigserial NOT NULL PRIMARY KEY,
			"label" text NOT NULL,
			"phrases" text NOT NULL DEFAULT '[]',
			"enabled" boolean NOT NULL DEFAULT true,
			"order" integer NOT NULL DEFAULT 0,
			"createdAt" bigint NOT NULL DEFAULT 0
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "callNatures_label_uidx" ON "callNatures" ("label")`,
		`ALTER TABLE "callNatures" ADD COLUMN IF NOT EXISTS "expireMinutes" integer NOT NULL DEFAULT 0`,
	}
	for _, q := range queries {
		if _, err := db.Sql.Exec(q); err != nil {
			return fmt.Errorf("migrateCallNatures: %w", err)
		}
	}
	return seedDefaultCallNatures(db)
}

func seedDefaultCallNatures(db *Database) error {
	var count int
	if err := db.Sql.QueryRow(`SELECT COUNT(*) FROM "callNatures"`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	for i, label := range defaultCallNatureLabels {
		label = strings.ToUpper(strings.TrimSpace(label))
		if label == "" {
			continue
		}
		phrases, _ := json.Marshal([]string{label})
		_, err := db.Sql.Exec(`INSERT INTO "callNatures" ("label", "phrases", "enabled", "order", "createdAt")
			VALUES ($1, $2, true, $3, $4)`,
			label, string(phrases), i, now)
		if err != nil {
			return fmt.Errorf("seed call nature %q: %w", label, err)
		}
	}
	return nil
}

func callNatureFromRow(row *sql.Row) (*CallNature, error) {
	var (
		n           CallNature
		phrasesJSON string
		enabled     bool
	)
	if err := row.Scan(&n.Id, &n.Label, &phrasesJSON, &enabled, &n.Order, &n.ExpireMinutes, &n.CreatedAt); err != nil {
		return nil, err
	}
	n.Enabled = enabled
	n.Label = strings.ToUpper(strings.TrimSpace(n.Label))
	if phrasesJSON != "" && phrasesJSON != "[]" {
		_ = json.Unmarshal([]byte(phrasesJSON), &n.Phrases)
	}
	return &n, nil
}

func callNatureToJSON(n *CallNature) map[string]any {
	if n == nil {
		return nil
	}
	return map[string]any{
		"id":            n.Id,
		"label":         n.Label,
		"phrases":       n.Phrases,
		"enabled":       n.Enabled,
		"order":         n.Order,
		"expireMinutes": n.ExpireMinutes,
		"createdAt":     n.CreatedAt,
	}
}
