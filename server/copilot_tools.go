// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const copilotMaxToolRounds = 15

func copilotToolDefinitions() []openAIToolDef {
	stringProp := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}
	intProp := func(desc string) map[string]any {
		return map[string]any{"type": "integer", "description": desc}
	}
	boolProp := func(desc string) map[string]any {
		return map[string]any{"type": "boolean", "description": desc}
	}

	def := func(name, description string, props map[string]any, required []string) openAIToolDef {
		var d openAIToolDef
		d.Type = "function"
		d.Function.Name = name
		d.Function.Description = description
		d.Function.Parameters = map[string]any{
			"type":       "object",
			"properties": props,
		}
		if len(required) > 0 {
			d.Function.Parameters["required"] = required
		}
		return d
	}

	objectProp := func(desc string) map[string]any {
		return map[string]any{"type": "object", "description": desc}
	}

	return []openAIToolDef{
		def("get_admin_config",
			"Read TLR admin configuration and data. Use section=capabilities when unsure (full action catalog + workflows). section=summary for overview. section=talkgroup needs systemId + talkgroupId and returns toneSets schema.",
			map[string]any{
				"section":      stringProp("capabilities, summary, tags, systems, system, talkgroup, options, users, transcription_failures, hallucinations, relay_status, ..."),
				"systemId":     intProp("Required for section=system or talkgroup"),
				"talkgroupId":  intProp("Required for section=talkgroup (or talkgroupRef)"),
				"talkgroupRef": intProp("Alternative to talkgroupId for section=talkgroup"),
				"params":       objectProp("Optional filters, e.g. for section=calls"),
			},
			nil,
		),
		def("apply_admin_change",
			"Apply admin UI changes. confirmed=true required for writes only. Read actions (no confirm): radioreference_browse, radioreference_search, parse_tone_import, tone_history_analyze, check_server_update, test_central_connection. Writes include update_talkgroup, update_talkgroup_tone_sets, invite_user, radioreference_import_to_system, patch_transcript_parser, etc. Call get_admin_config section=capabilities for full list and payload shapes.",
			map[string]any{
				"action":    stringProp("Action name — see get_admin_config section=capabilities or summary availableWriteActions/availableReadActions"),
				"confirmed": boolProp("Must be true for write actions"),
				"payload":   objectProp("Action-specific JSON body matching the admin API"),
			},
			[]string{"action"},
		),
		def("search_logs",
			"Search server logs by message text, level, and category. Returns recent matching log lines.",
			map[string]any{
				"search":     stringProp("Substring to match in log messages (optional)"),
				"level":      stringProp("Filter by level: info, warn, or error (optional)"),
				"categories": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Log category IDs, e.g. auth, billing, email, calls, transcription"},
				"limit":      intProp("Max rows to return (default 25, max 50)"),
				"hours":      intProp("Look back this many hours (default 24)"),
			},
			nil,
		),
		def("get_server_status",
			"Snapshot of server version, connected listeners, and enabled integrations (transcription, Stripe, email, OpenAI, Central Management).",
			map[string]any{},
			nil,
		),
		def("get_system_health_alerts",
			"List active (non-dismissed) system health alerts such as no-audio, transcription failures, and tone detection issues.",
			map[string]any{
				"limit": intProp("Max alerts to return (default 20)"),
			},
			nil,
		),
		def("list_systems_and_tags",
			"List systems, tags, and talkgroup counts. Prefer get_admin_config section=systems or section=tags.",
			map[string]any{},
			nil,
		),
		def("audit_talkgroup_tags",
			"Find talkgroups whose tag may be wrong based on label/name keywords (fire, law, ems, etc.). Optional systemId filter.",
			map[string]any{
				"systemId": intProp("Limit audit to one system ID (optional)"),
			},
			nil,
		),
		def("search_admin_help",
			"Search embedded TLR admin documentation for how to configure or troubleshoot features.",
			map[string]any{
				"query": stringProp("Search terms, e.g. stripe webhook, transcription, tags"),
				"limit": intProp("Max articles (default 5)"),
			},
			[]string{"query"},
		),
	}
}

func (admin *Admin) executeCopilotTool(name string, argsJSON string) (string, error) {
	switch name {
	case "get_admin_config":
		return admin.copilotToolGetAdminConfig(argsJSON)
	case "apply_admin_change":
		return admin.copilotToolApplyAdminChange(argsJSON)
	case "search_logs":
		return admin.copilotToolSearchLogs(argsJSON)
	case "get_server_status":
		return admin.copilotToolServerStatus()
	case "get_system_health_alerts":
		return admin.copilotToolHealthAlerts(argsJSON)
	case "list_systems_and_tags":
		return admin.copilotToolListSystemsTags()
	case "audit_talkgroup_tags":
		return admin.copilotToolAuditTags(argsJSON)
	case "search_admin_help":
		return admin.copilotToolSearchHelp(argsJSON)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func (admin *Admin) copilotToolSearchLogs(argsJSON string) (string, error) {
	var args struct {
		Search     string   `json:"search"`
		Level      string   `json:"level"`
		Categories []string `json:"categories"`
		Limit      int      `json:"limit"`
		Hours      int      `json:"hours"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)

	hours := args.Hours
	if hours <= 0 {
		hours = 24
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 25
	}
	if limit > 50 {
		limit = 50
	}

	opts := NewLogSearchOptions()
	opts.Search = args.Search
	if args.Level != "" {
		opts.Level = args.Level
	}
	opts.Categories = args.Categories
	opts.Limit = uint(limit)
	opts.Sort = -1
	opts.Date = time.Now().Add(-time.Duration(hours) * time.Hour)

	results, err := admin.Controller.Logs.Search(opts, admin.Controller.Database)
	if err != nil {
		return "", err
	}

	type row struct {
		Time     string `json:"time"`
		Level    string `json:"level"`
		Category string `json:"category"`
		Message  string `json:"message"`
	}
	out := struct {
		Count int   `json:"count"`
		Logs  []row `json:"logs"`
	}{Logs: []row{}}

	for _, l := range results.Logs {
		out.Logs = append(out.Logs, row{
			Time:     l.DateTime.UTC().Format(time.RFC3339),
			Level:    l.Level,
			Category: l.Category,
			Message:  l.Message,
		})
	}
	out.Count = len(out.Logs)
	b, _ := json.Marshal(out)
	return string(b), nil
}

func (admin *Admin) copilotToolServerStatus() (string, error) {
	ctrl := admin.Controller
	opts := ctrl.Options

	tagByID := map[uint64]string{}
	for _, t := range ctrl.Tags.List {
		tagByID[t.Id] = t.Label
	}

	var talkgroupCount int
	for _, sys := range ctrl.Systems.List {
		talkgroupCount += len(sys.Talkgroups.List)
	}

	status := map[string]any{
		"version":            Version,
		"listenerCount":      ctrl.Clients.Count(),
		"systemCount":        len(ctrl.Systems.List),
		"talkgroupCount":     talkgroupCount,
		"tagCount":           len(ctrl.Tags.List),
		"openaiConfigured":   strings.TrimSpace(opts.OpenAIIntegration.APIKey) != "",
		"openaiModel":        opts.OpenAIIntegration.resolvedChatModel(),
		"transcriptionEnabled":  opts.TranscriptionConfig.Enabled,
		"transcriptionProvider": opts.TranscriptionConfig.Provider,
		"stripeEnabled":      opts.StripePaywallEnabled,
		"emailEnabled":       opts.EmailServiceEnabled,
		"centralManagement":  opts.CentralManagementEnabled,
		"branding":           opts.Branding,
	}
	b, _ := json.Marshal(status)
	return string(b), nil
}

func (admin *Admin) copilotToolHealthAlerts(argsJSON string) (string, error) {
	var args struct {
		Limit int `json:"limit"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)
	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}

	alerts, err := admin.Controller.GetSystemAlerts(limit, false)
	if err != nil {
		return "", err
	}

	type alertRow struct {
		ID       uint64 `json:"id"`
		Type     string `json:"type"`
		Severity string `json:"severity"`
		Title    string `json:"title"`
		Message  string `json:"message"`
		Created  string `json:"createdAt"`
	}
	out := struct {
		Count  int        `json:"count"`
		Alerts []alertRow `json:"alerts"`
	}{Alerts: []alertRow{}}

	for _, a := range alerts {
		out.Alerts = append(out.Alerts, alertRow{
			ID:       a.Id,
			Type:     a.AlertType,
			Severity: a.Severity,
			Title:    a.Title,
			Message:  a.Message,
			Created:  time.UnixMilli(a.CreatedAt).UTC().Format(time.RFC3339),
		})
	}
	out.Count = len(out.Alerts)
	b, _ := json.Marshal(out)
	return string(b), nil
}

func (admin *Admin) copilotToolListSystemsTags() (string, error) {
	ctrl := admin.Controller

	tagLabels := map[uint64]string{}
	tags := []map[string]any{}
	for _, t := range ctrl.Tags.List {
		tagLabels[t.Id] = t.Label
		tags = append(tags, map[string]any{"id": t.Id, "label": t.Label})
	}

	systems := []map[string]any{}
	for _, sys := range ctrl.Systems.List {
		perTag := map[string]int{}
		untagged := 0
		for _, tg := range sys.Talkgroups.List {
			if tg.TagId == 0 {
				untagged++
				continue
			}
			label := tagLabels[tg.TagId]
			if label == "" {
				label = fmt.Sprintf("tag:%d", tg.TagId)
			}
			perTag[label]++
		}
		systems = append(systems, map[string]any{
			"id":              sys.Id,
			"label":           sys.Label,
			"systemRef":       sys.SystemRef,
			"talkgroupCount":  len(sys.Talkgroups.List),
			"untaggedCount":   untagged,
			"talkgroupsByTag": perTag,
		})
	}

	out := map[string]any{"tags": tags, "systems": systems}
	b, _ := json.Marshal(out)
	return string(b), nil
}

var copilotTagKeywordRules = []struct {
	Tag      string
	Keywords []string
}{
	{"Fire", []string{"fire", "fd ", " fd", "rescue", "battalion", "engine", "ladder", "arson"}},
	{"Law", []string{"police", "sheriff", "law", " pd", "pd ", "deputy", "trooper", "patrol", "detective"}},
	{"EMS", []string{"ems", "medical", "ambulance", "medic", "hospital", "rescue squad"}},
	{"Dispatch", []string{"dispatch", "disp", "ops", "operations", "911"}},
}

func inferTalkgroupTagLabel(label, name string) string {
	hay := strings.ToLower(label + " " + name)
	best := ""
	bestScore := 0
	for _, rule := range copilotTagKeywordRules {
		score := 0
		for _, kw := range rule.Keywords {
			if strings.Contains(hay, kw) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			best = rule.Tag
		}
	}
	return best
}

func (admin *Admin) copilotToolAuditTags(argsJSON string) (string, error) {
	var args struct {
		SystemID uint64 `json:"systemId"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)

	tagLabels := map[uint64]string{}
	for _, t := range admin.Controller.Tags.List {
		tagLabels[t.Id] = t.Label
	}

	type issue struct {
		SystemID     uint64 `json:"systemId"`
		SystemLabel  string `json:"systemLabel"`
		TalkgroupID  uint64 `json:"talkgroupId"`
		TalkgroupRef uint   `json:"talkgroupRef"`
		Label        string `json:"label"`
		Name         string `json:"name"`
		CurrentTag   string `json:"currentTag"`
		SuggestedTag string `json:"suggestedTag"`
		Reason       string `json:"reason"`
	}
	issues := []issue{}

	for _, sys := range admin.Controller.Systems.List {
		if args.SystemID > 0 && sys.Id != args.SystemID {
			continue
		}
		for _, tg := range sys.Talkgroups.List {
			current := tagLabels[tg.TagId]
			if tg.TagId == 0 {
				suggested := inferTalkgroupTagLabel(tg.Label, tg.Name)
				if suggested != "" {
					issues = append(issues, issue{
						SystemID: sys.Id, SystemLabel: sys.Label,
						TalkgroupID: tg.Id, TalkgroupRef: tg.TalkgroupRef,
						Label: tg.Label, Name: tg.Name,
						CurrentTag: "(none)", SuggestedTag: suggested,
						Reason: "talkgroup has no tag assigned",
					})
				}
				continue
			}
			suggested := inferTalkgroupTagLabel(tg.Label, tg.Name)
			if suggested == "" {
				continue
			}
			if !strings.EqualFold(current, suggested) {
				issues = append(issues, issue{
					SystemID: sys.Id, SystemLabel: sys.Label,
					TalkgroupID: tg.Id, TalkgroupRef: tg.TalkgroupRef,
					Label: tg.Label, Name: tg.Name,
					CurrentTag: current, SuggestedTag: suggested,
					Reason: fmt.Sprintf("label/name suggests %s but tagged as %s", suggested, current),
				})
			}
		}
	}

	out := map[string]any{"issueCount": len(issues), "issues": issues}
	b, _ := json.Marshal(out)
	return string(b), nil
}

func (admin *Admin) copilotToolSearchHelp(argsJSON string) (string, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 5
	}
	articles := searchCopilotKB(args.Query, limit)
	type articleOut struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	out := struct {
		Version  string       `json:"kbVersion"`
		Articles []articleOut `json:"articles"`
	}{Version: copilotKBData.Version, Articles: []articleOut{}}
	for _, a := range articles {
		out.Articles = append(out.Articles, articleOut{ID: a.ID, Title: a.Title, Body: a.Body})
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

func (admin *Admin) copilotToolApplyTagDefinitionUpdates(argsJSON string) (string, error) {
	var args struct {
		Confirmed bool `json:"confirmed"`
		Updates   []struct {
			TagID        uint64 `json:"tagId"`
			CurrentLabel string `json:"currentLabel"`
			NewLabel     string `json:"newLabel"`
			Color        string `json:"color"`
		} `json:"updates"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	if !args.Confirmed {
		b, _ := json.Marshal(map[string]any{"applied": false, "error": "confirmed must be true"})
		return string(b), nil
	}
	if len(args.Updates) == 0 {
		b, _ := json.Marshal(map[string]any{"applied": false, "error": "no updates provided"})
		return string(b), nil
	}

	currentJSON, err := json.Marshal(admin.Controller.Tags.List)
	if err != nil {
		return "", err
	}
	var list []any
	if err = json.Unmarshal(currentJSON, &list); err != nil {
		return "", err
	}

	applied := []map[string]any{}
	errors := []string{}

	for _, upd := range args.Updates {
		newLabel := strings.TrimSpace(upd.NewLabel)
		if newLabel == "" {
			errors = append(errors, "each update needs newLabel")
			continue
		}

		found := false
		for i, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			var match bool
			if upd.TagID > 0 {
				if id, ok := m["id"].(float64); ok && uint64(id) == upd.TagID {
					match = true
				}
			} else if upd.CurrentLabel != "" {
				if label, ok := m["label"].(string); ok && strings.EqualFold(label, strings.TrimSpace(upd.CurrentLabel)) {
					match = true
				}
			}
			if !match {
				continue
			}

			oldLabel, _ := m["label"].(string)
			duplicate := false
			for j, other := range list {
				if j == i {
					continue
				}
				if om, ok := other.(map[string]any); ok {
					if ol, ok := om["label"].(string); ok && strings.EqualFold(ol, newLabel) {
						errors = append(errors, fmt.Sprintf("tag label %q already exists", newLabel))
						duplicate = true
						break
					}
				}
			}
			if duplicate {
				found = true
				break
			}

			m["label"] = newLabel
			if upd.Color != "" {
				m["color"] = strings.TrimSpace(upd.Color)
			}
			list[i] = m
			applied = append(applied, map[string]any{
				"tagId":     m["id"],
				"oldLabel":  oldLabel,
				"newLabel":  newLabel,
				"color":     m["color"],
			})
			found = true
			break
		}
		if !found && len(errors) == 0 {
			if upd.TagID > 0 {
				errors = append(errors, fmt.Sprintf("tag id %d not found", upd.TagID))
			} else {
				errors = append(errors, fmt.Sprintf("tag %q not found", upd.CurrentLabel))
			}
		}
	}

	if len(applied) == 0 {
		out := map[string]any{"applied": false, "changes": applied, "errors": errors}
		b, _ := json.Marshal(out)
		return string(b), nil
	}

	admin.mutex.Lock()
	admin.Controller.Tags.FromMap(list)
	err = admin.Controller.Tags.Write(admin.Controller.Database)
	if err == nil {
		err = admin.Controller.Tags.Read(admin.Controller.Database)
	}
	admin.mutex.Unlock()

	if err != nil {
		return "", err
	}

	go admin.Controller.EmitConfig()
	admin.Controller.SyncConfigToFile()
	admin.Controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("copilot: renamed/updated %d tag definition(s)", len(applied)))

	out := map[string]any{
		"applied": true,
		"changes": applied,
		"errors":  errors,
		"tags":    admin.Controller.Tags.List,
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

func (admin *Admin) copilotToolApplyTalkgroupTagUpdates(argsJSON string) (string, error) {
	var args struct {
		Confirmed bool `json:"confirmed"`
		Updates   []struct {
			SystemID     uint64 `json:"systemId"`
			TalkgroupID  uint64 `json:"talkgroupId"`
			TalkgroupRef uint   `json:"talkgroupRef"`
			TagID        uint64 `json:"tagId"`
			TagLabel     string `json:"tagLabel"`
		} `json:"updates"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	if !args.Confirmed {
		b, _ := json.Marshal(map[string]any{"applied": false, "error": "confirmed must be true"})
		return string(b), nil
	}
	if len(args.Updates) == 0 {
		b, _ := json.Marshal(map[string]any{"applied": false, "error": "no updates provided"})
		return string(b), nil
	}

	tagIDByLabel := map[string]uint64{}
	for _, t := range admin.Controller.Tags.List {
		tagIDByLabel[strings.ToLower(t.Label)] = t.Id
	}

	applied := []map[string]any{}
	errors := []string{}

	updatesBySystem := map[uint64][]struct {
		talkgroupID  uint64
		talkgroupRef uint
		tagID        uint64
	}{}

	for _, u := range args.Updates {
		tagID := u.TagID
		if tagID == 0 && u.TagLabel != "" {
			if id, ok := tagIDByLabel[strings.ToLower(strings.TrimSpace(u.TagLabel))]; ok {
				tagID = id
			} else {
				errors = append(errors, fmt.Sprintf("unknown tag label %q", u.TagLabel))
				continue
			}
		}
		if tagID == 0 {
			errors = append(errors, "each update needs tagId or tagLabel")
			continue
		}
		if u.TalkgroupID == 0 && u.TalkgroupRef == 0 {
			errors = append(errors, "each update needs talkgroupId or talkgroupRef")
			continue
		}
		updatesBySystem[u.SystemID] = append(updatesBySystem[u.SystemID], struct {
			talkgroupID  uint64
			talkgroupRef uint
			tagID        uint64
		}{u.TalkgroupID, u.TalkgroupRef, tagID})
	}

	admin.mutex.Lock()
	defer admin.mutex.Unlock()

	for systemID, sysUpdates := range updatesBySystem {
		system, ok := admin.Controller.Systems.GetSystemById(systemID)
		if !ok {
			errors = append(errors, fmt.Sprintf("system %d not found", systemID))
			continue
		}

		currentJSON, err := json.Marshal(system)
		if err != nil {
			errors = append(errors, fmt.Sprintf("system %d marshal: %v", systemID, err))
			continue
		}
		var incoming map[string]any
		if err = json.Unmarshal(currentJSON, &incoming); err != nil {
			errors = append(errors, fmt.Sprintf("system %d unmarshal: %v", systemID, err))
			continue
		}

		tgList, ok := incoming["talkgroups"].([]any)
		if !ok {
			errors = append(errors, fmt.Sprintf("system %d has no talkgroups array", systemID))
			continue
		}

		for _, upd := range sysUpdates {
			found := false
			for i, item := range tgList {
				m, ok := item.(map[string]any)
				if !ok {
					continue
				}
				var match bool
				if upd.talkgroupID > 0 {
					if id, ok := m["id"].(float64); ok && uint64(id) == upd.talkgroupID {
						match = true
					}
				} else if upd.talkgroupRef > 0 {
					if ref, ok := m["talkgroupRef"].(float64); ok && uint(ref) == upd.talkgroupRef {
						match = true
					}
				}
				if match {
					m["tagId"] = float64(upd.tagID)
					tgList[i] = m
					applied = append(applied, map[string]any{
						"systemId": systemID, "talkgroupId": m["id"], "talkgroupRef": m["talkgroupRef"], "tagId": upd.tagID,
					})
					found = true
					break
				}
			}
			if !found {
				errors = append(errors, fmt.Sprintf("talkgroup not found in system %d", systemID))
			}
		}
		incoming["talkgroups"] = tgList

		currentAllJSON, err := json.Marshal(admin.Controller.Systems.List)
		if err != nil {
			errors = append(errors, fmt.Sprintf("systems list marshal: %v", err))
			continue
		}
		var arr []any
		if err = json.Unmarshal(currentAllJSON, &arr); err != nil {
			errors = append(errors, fmt.Sprintf("systems list unmarshal: %v", err))
			continue
		}
		replaced := false
		for i, r := range arr {
			if m, ok := r.(map[string]any); ok {
				if mid, ok := m["id"].(float64); ok && uint64(mid) == systemID {
					arr[i] = incoming
					replaced = true
					break
				}
			}
		}
		if !replaced {
			errors = append(errors, fmt.Sprintf("system %d not in list", systemID))
			continue
		}

		admin.Controller.Systems.FromMap(arr)
		if err = admin.Controller.Systems.Write(admin.Controller.Database); err != nil {
			_ = admin.Controller.Systems.Read(admin.Controller.Database)
			errors = append(errors, fmt.Sprintf("system %d write: %v", systemID, err))
			continue
		}
		if err = admin.Controller.Systems.Read(admin.Controller.Database); err != nil {
			errors = append(errors, fmt.Sprintf("system %d reload: %v", systemID, err))
			continue
		}
		_ = admin.Controller.IdLookupsCache.Read(admin.Controller.Database)
	}

	if len(applied) > 0 {
		go admin.Controller.EmitConfig()
		admin.Controller.SyncConfigToFile()
		admin.Controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("copilot: applied %d talkgroup tag update(s)", len(applied)))
	}

	out := map[string]any{
		"applied": len(applied) > 0,
		"changes": applied,
		"errors":  errors,
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}
