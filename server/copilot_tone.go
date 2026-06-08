// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// copilotToneSetSchemaDoc is returned with talkgroup reads so the model knows the expected shape.
const copilotToneSetSchemaDoc = `Each tone set object: { id, label, tolerance (0.02 default = ±10Hz), aTone?: {frequency, minDuration, maxDuration}, bTone?: {...}, longTone?: {...}, downstreamEnabled?: bool }. Enable talkgroup toneDetectionEnabled when adding tone sets.`

func copilotNormalizeToneSets(raw []any) ([]ToneSet, error) {
	out := make([]ToneSet, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			b, err := json.Marshal(item)
			if err != nil {
				return nil, err
			}
			var ts ToneSet
			if err := json.Unmarshal(b, &ts); err != nil {
				return nil, fmt.Errorf("invalid tone set: %v", err)
			}
			out = append(out, copilotFinalizeToneSet(ts))
			continue
		}
		out = append(out, copilotNormalizeToneSetMap(m))
	}
	return out, nil
}

func copilotNormalizeToneSetMap(m map[string]any) ToneSet {
	var ts ToneSet
	if v, ok := m["id"].(string); ok {
		ts.Id = v
	}
	if v, ok := m["label"].(string); ok {
		ts.Label = v
	}
	if v, ok := m["tolerance"].(float64); ok {
		ts.Tolerance = v
	}
	if v, ok := m["minDuration"].(float64); ok {
		ts.MinDuration = v
	}
	if v, ok := m["downstreamEnabled"].(bool); ok {
		ts.DownstreamEnabled = v
	}
	if v, ok := m["downstreamURL"].(string); ok {
		ts.DownstreamURL = v
	}
	if v, ok := m["downstreamAPIKey"].(string); ok {
		ts.DownstreamAPIKey = v
	}

	ts.ATone = copilotParseToneSpec(m, "aTone", "aToneFrequency", "aToneMinDuration", "aToneMaxDuration")
	ts.BTone = copilotParseToneSpec(m, "bTone", "bToneFrequency", "bToneMinDuration", "bToneMaxDuration")
	ts.LongTone = copilotParseToneSpec(m, "longTone", "longToneFrequency", "longToneMinDuration", "longToneMaxDuration")

	return copilotFinalizeToneSet(ts)
}

func copilotParseToneSpec(m map[string]any, nestedKey, freqKey, minKey, maxKey string) *ToneSpec {
	if nested, ok := m[nestedKey].(map[string]any); ok {
		spec := &ToneSpec{}
		if v, ok := nested["frequency"].(float64); ok {
			spec.Frequency = v
		}
		if v, ok := nested["minDuration"].(float64); ok {
			spec.MinDuration = v
		}
		if v, ok := nested["maxDuration"].(float64); ok {
			spec.MaxDuration = v
		}
		if spec.Frequency > 0 || spec.MinDuration > 0 {
			return spec
		}
	}
	freq, hasFreq := m[freqKey].(float64)
	minDur, hasMin := m[minKey].(float64)
	maxDur, _ := m[maxKey].(float64)
	if !hasFreq && !hasMin {
		return nil
	}
	spec := &ToneSpec{Frequency: freq, MinDuration: minDur, MaxDuration: maxDur}
	return spec
}

func copilotFinalizeToneSet(ts ToneSet) ToneSet {
	if strings.TrimSpace(ts.Id) == "" {
		ts.Id = fmt.Sprintf("tone-set-%d-%s", time.Now().UnixMilli(), uuid.New().String()[:8])
	}
	if ts.Tolerance == 0 {
		ts.Tolerance = 0.02
	}
	return ts
}

func (admin *Admin) copilotFindTalkgroup(systemID, talkgroupID uint64, talkgroupRef uint) (*System, *Talkgroup, error) {
	system, ok := admin.Controller.Systems.GetSystemById(systemID)
	if !ok {
		return nil, nil, fmt.Errorf("system %d not found", systemID)
	}
	for _, tg := range system.Talkgroups.List {
		if talkgroupID > 0 && tg.Id == talkgroupID {
			return system, tg, nil
		}
		if talkgroupRef > 0 && tg.TalkgroupRef == talkgroupRef {
			return system, tg, nil
		}
	}
	return nil, nil, fmt.Errorf("talkgroup not found in system %d", systemID)
}

func (admin *Admin) copilotGetTalkgroupConfig(systemID, talkgroupID uint64, talkgroupRef uint) (map[string]any, error) {
	_, tg, err := admin.copilotFindTalkgroup(systemID, talkgroupID, talkgroupRef)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"systemId":             systemID,
		"talkgroup":            tg,
		"toneSetCount":         len(tg.ToneSets),
		"toneDetectionEnabled": tg.ToneDetectionEnabled,
		"toneSetSchema":        copilotToneSetSchemaDoc,
		"exampleToneSet": map[string]any{
			"id":        "tone-set-example",
			"label":     "Fire Dept",
			"tolerance": 0.02,
			"aTone": map[string]any{
				"frequency":   1234.5,
				"minDuration": 1.0,
				"maxDuration": 3.0,
			},
		},
	}, nil
}

func (admin *Admin) copilotUpdateTalkgroupToneSets(payloadJSON []byte) (map[string]any, error) {
	var req struct {
		SystemID            uint64 `json:"systemId"`
		TalkgroupID         uint64 `json:"talkgroupId"`
		TalkgroupRef        uint   `json:"talkgroupRef"`
		Mode                string `json:"mode"`
		EnableToneDetection *bool  `json:"enableToneDetection"`
		ToneSets            []any  `json:"toneSets"`
	}
	if err := json.Unmarshal(payloadJSON, &req); err != nil {
		return nil, err
	}
	if req.SystemID == 0 {
		return nil, fmt.Errorf("systemId is required")
	}
	if req.TalkgroupID == 0 && req.TalkgroupRef == 0 {
		return nil, fmt.Errorf("talkgroupId or talkgroupRef is required")
	}
	if len(req.ToneSets) == 0 {
		return nil, fmt.Errorf("toneSets array is required")
	}

	newSets, err := copilotNormalizeToneSets(req.ToneSets)
	if err != nil {
		return nil, err
	}

	system, tg, err := admin.copilotFindTalkgroup(req.SystemID, req.TalkgroupID, req.TalkgroupRef)
	if err != nil {
		return nil, err
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "append"
	}

	var merged []ToneSet
	switch mode {
	case "replace":
		merged = newSets
	case "append":
		merged = append([]ToneSet{}, tg.ToneSets...)
		for _, ns := range newSets {
			replaced := false
			for i, existing := range merged {
				if (ns.Id != "" && existing.Id == ns.Id) ||
					(strings.EqualFold(existing.Label, ns.Label) && ns.Label != "") {
					merged[i] = ns
					replaced = true
					break
				}
			}
			if !replaced {
				merged = append(merged, ns)
			}
		}
	default:
		return nil, fmt.Errorf("mode must be append or replace")
	}

	tg.ToneSets = merged
	if req.EnableToneDetection != nil {
		tg.ToneDetectionEnabled = *req.EnableToneDetection
	} else if len(merged) > 0 {
		tg.ToneDetectionEnabled = true
	}

	systemJSON, err := json.Marshal(system)
	if err != nil {
		return nil, err
	}
	if err = admin.copilotSaveSystem(systemJSON); err != nil {
		return nil, err
	}

	return map[string]any{
		"systemId":             req.SystemID,
		"talkgroupId":          tg.Id,
		"talkgroupRef":         tg.TalkgroupRef,
		"label":                tg.Label,
		"toneDetectionEnabled": tg.ToneDetectionEnabled,
		"toneSetCount":         len(merged),
		"toneSets":             merged,
	}, nil
}

func (admin *Admin) copilotParseToneImport(payload map[string]any) (map[string]any, error) {
	format, _ := payload["format"].(string)
	content, _ := payload["content"].(string)
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("payload.content is required")
	}
	result, err := ParseToneImport(format, content)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"format":   format,
		"count":    len(result.toneSets),
		"toneSets": result.toneSets,
		"warnings": result.warnings,
		"hint":     "Use update_talkgroup_tone_sets with these toneSets to apply to a talkgroup",
	}, nil
}
