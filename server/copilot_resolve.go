// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"fmt"
	"strings"
	"unicode"
)

// copilotNormalizeSearch folds labels for fuzzy matching.
func copilotNormalizeSearch(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevSpace = false
			continue
		}
		if !prevSpace {
			b.WriteByte(' ')
			prevSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func copilotTokens(s string) []string {
	parts := strings.Fields(copilotNormalizeSearch(s))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) >= 2 {
			out = append(out, p)
		}
	}
	return out
}

func copilotScoreLabel(query, label string) int {
	q := copilotNormalizeSearch(query)
	l := copilotNormalizeSearch(label)
	if q == "" || l == "" {
		return 0
	}
	if q == l {
		return 1000
	}
	if strings.Contains(l, q) || strings.Contains(q, l) {
		return 800
	}
	qTokens := copilotTokens(query)
	lTokens := copilotTokens(label)
	if len(qTokens) == 0 {
		return 0
	}
	matched := 0
	for _, qt := range qTokens {
		for _, lt := range lTokens {
			if qt == lt || strings.HasPrefix(lt, qt) || strings.HasPrefix(qt, lt) {
				matched++
				break
			}
		}
	}
	if matched == 0 {
		return 0
	}
	score := matched * 100
	if matched == len(qTokens) {
		score += 200
	}
	score += max(0, 50-len(lTokens))
	return score
}

type scoredSystem struct {
	sys   *System
	score int
}

type scoredTalkgroup struct {
	sys   *System
	tg    *Talkgroup
	score int
}

func (admin *Admin) copilotFindSystems(query string, systemID uint64, systemRef uint, limit int) []map[string]any {
	if limit <= 0 {
		limit = 10
	}
	var hits []scoredSystem
	for _, sys := range admin.Controller.Systems.List {
		if sys == nil {
			continue
		}
		score := 0
		if systemID > 0 && sys.Id == systemID {
			score = 2000
		} else if systemRef > 0 && sys.SystemRef == systemRef {
			score = 1500
		} else if query != "" {
			score = copilotScoreLabel(query, sys.Label)
			if n := parseUintLoose(query); n > 0 {
				if n == sys.Id {
					score = max(score, 1900)
				}
				if uint(n) == sys.SystemRef {
					score = max(score, 1400)
				}
			}
		}
		if score > 0 {
			hits = append(hits, scoredSystem{sys, score})
		}
	}
	sortScoredSystems(hits)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		out = append(out, map[string]any{
			"id":             h.sys.Id,
			"label":          h.sys.Label,
			"systemRef":      h.sys.SystemRef,
			"talkgroupCount": len(h.sys.Talkgroups.List),
			"score":          h.score,
		})
	}
	return out
}

func (admin *Admin) copilotFindTalkgroups(system *System, query string, talkgroupID uint64, talkgroupRef uint, limit int) []map[string]any {
	if limit <= 0 {
		limit = 15
	}
	var hits []scoredTalkgroup
	systems := []*System{}
	if system != nil {
		systems = append(systems, system)
	} else {
		systems = admin.Controller.Systems.List
	}
	for _, sys := range systems {
		if sys == nil {
			continue
		}
		for _, tg := range sys.Talkgroups.List {
			if tg == nil {
				continue
			}
			score := 0
			if talkgroupID > 0 && tg.Id == talkgroupID {
				score = 2000
			} else if talkgroupRef > 0 && tg.TalkgroupRef == talkgroupRef {
				score = 1800
			} else if query != "" {
				score = copilotScoreLabel(query, tg.Label)
				if n := parseUintLoose(query); n > 0 {
					if tg.TalkgroupRef == uint(n) {
						score = max(score, 1700)
					}
					if tg.Id == n {
						score = max(score, 1600)
					}
				}
				if score == 0 {
					score = copilotScoreLabel(query, tg.Label+" "+sys.Label)
				}
			}
			if score > 0 {
				hits = append(hits, scoredTalkgroup{sys, tg, score})
			}
		}
	}
	sortScoredTalkgroups(hits)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		out = append(out, map[string]any{
			"systemId":             h.sys.Id,
			"systemLabel":          h.sys.Label,
			"talkgroupId":          h.tg.Id,
			"talkgroupRef":         h.tg.TalkgroupRef,
			"label":                h.tg.Label,
			"name":                 h.tg.Name,
			"toneDetectionEnabled": h.tg.ToneDetectionEnabled,
			"toneSetCount":         len(h.tg.ToneSets),
			"score":                h.score,
		})
	}
	return out
}

// copilotResolveTalkgroup flexibly resolves system + talkgroup from IDs, refs, and/or labels.
func (admin *Admin) copilotResolveTalkgroup(systemID uint64, systemRef uint, systemQuery string, talkgroupID uint64, talkgroupRef uint, talkgroupQuery string) (*System, *Talkgroup, map[string]any, error) {
	hint := map[string]any{}

	var system *System
	if systemID > 0 {
		if sys, ok := admin.Controller.Systems.GetSystemById(systemID); ok {
			system = sys
		} else if sys, ok := admin.Controller.Systems.GetSystemByRef(uint(systemID)); ok {
			// Common mistake: passing systemRef as systemId (e.g. Trumbull systemRef=78).
			system = sys
			hint["systemIdNote"] = fmt.Sprintf("systemId %d not found; matched systemRef %d (%s)", systemID, sys.SystemRef, sys.Label)
		}
	}
	if system == nil && systemRef > 0 {
		if sys, ok := admin.Controller.Systems.GetSystemByRef(systemRef); ok {
			system = sys
		}
	}
	if system == nil && systemQuery != "" {
		cands := admin.copilotFindSystems(systemQuery, 0, 0, 5)
		hint["systemCandidates"] = cands
		if len(cands) > 0 {
			switch id := cands[0]["id"].(type) {
			case uint64:
				system, _ = admin.Controller.Systems.GetSystemById(id)
			case float64:
				system, _ = admin.Controller.Systems.GetSystemById(uint64(id))
			}
		}
	}
	if system == nil {
		return nil, nil, hint, fmt.Errorf("system not found — use section=find with query, or pass systemLabel")
	}

	var tg *Talkgroup
	if talkgroupID > 0 {
		if t, ok := system.Talkgroups.GetTalkgroupById(talkgroupID); ok {
			tg = t
		} else if t, ok := system.Talkgroups.GetTalkgroupByRef(uint(talkgroupID)); ok {
			tg = t
			hint["talkgroupIdNote"] = fmt.Sprintf("talkgroupId %d not found; matched talkgroupRef %d (%s)", talkgroupID, t.TalkgroupRef, t.Label)
		}
	}
	if tg == nil && talkgroupRef > 0 {
		if t, ok := system.Talkgroups.GetTalkgroupByRef(talkgroupRef); ok {
			tg = t
		}
	}
	if tg == nil && talkgroupQuery != "" {
		cands := admin.copilotFindTalkgroups(system, talkgroupQuery, 0, 0, 8)
		hint["talkgroupCandidates"] = cands
		if len(cands) > 0 {
			switch id := cands[0]["talkgroupId"].(type) {
			case uint64:
				tg, _ = system.Talkgroups.GetTalkgroupById(id)
			case float64:
				tg, _ = system.Talkgroups.GetTalkgroupById(uint64(id))
			}
		}
	}
	if tg == nil {
		q := talkgroupQuery
		if q == "" && talkgroupID > 0 {
			q = fmt.Sprintf("%d", talkgroupID)
		}
		if q != "" {
			cands := admin.copilotFindTalkgroups(nil, q, talkgroupID, talkgroupRef, 8)
			hint["globalTalkgroupCandidates"] = cands
		}
		return system, nil, hint, fmt.Errorf("talkgroup not found in system %d (%s)", system.Id, system.Label)
	}

	hint["resolved"] = map[string]any{
		"systemId": system.Id, "systemLabel": system.Label, "systemRef": system.SystemRef,
		"talkgroupId": tg.Id, "talkgroupRef": tg.TalkgroupRef, "talkgroupLabel": tg.Label,
	}
	return system, tg, hint, nil
}

func parseUintLoose(s string) uint64 {
	s = copilotNormalizeSearch(s)
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return 0
	}
	// Prefer a pure-numeric string; otherwise first pure-numeric token.
	candidates := []string{s}
	if len(parts) > 1 {
		candidates = parts
	}
	for _, c := range candidates {
		allDigit := true
		var m uint64
		for _, r := range c {
			if r < '0' || r > '9' {
				allDigit = false
				break
			}
			m = m*10 + uint64(r-'0')
		}
		if allDigit && len(c) > 0 {
			return m
		}
	}
	return 0
}

func sortScoredSystems(hits []scoredSystem) {
	for i := 0; i < len(hits); i++ {
		for j := i + 1; j < len(hits); j++ {
			if hits[j].score > hits[i].score {
				hits[i], hits[j] = hits[j], hits[i]
			}
		}
	}
}

func sortScoredTalkgroups(hits []scoredTalkgroup) {
	for i := 0; i < len(hits); i++ {
		for j := i + 1; j < len(hits); j++ {
			if hits[j].score > hits[i].score {
				hits[i], hits[j] = hits[j], hits[i]
			}
		}
	}
}
