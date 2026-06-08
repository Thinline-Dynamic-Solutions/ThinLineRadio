// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/customer"
)

// copilotAdminChangeAction lists every write the assistant may perform (mirrors admin UI capabilities).
var copilotAdminWriteActions = map[string]bool{
	"save_tags":                       true,
	"update_tags":                     true,
	"save_talkgroup_groups":           true,
	"save_apikeys":                    true,
	"save_dirwatch":                   true,
	"save_downstreams":                true,
	"save_system":                     true,
	"delete_system":                   true,
	"patch_options":                   true,
	"save_system_no_audio_settings":   true,
	"patch_health_settings":           true,
	"dismiss_health_alert":            true,
	"purge_data":                      true,
	"stripe_sync":                     true,
	"sync_tone_sets":                  true,
	"create_user":                     true,
	"update_user":                     true,
	"delete_user":                     true,
	"reset_user_password":             true,
	"create_keyword_list":             true,
	"update_keyword_list":             true,
	"delete_keyword_list":             true,
	"update_talkgroup_tags":           true,
	"send_test_email":                 true,
	"change_admin_password":           true,
	"update_talkgroup_tone_sets":      true,
	"update_talkgroup":                true,
	"reset_transcription_failures":    true,
	"patch_transcript_parser":         true,
	"relay_unlock_public_client":      true,
	"approve_hallucination":           true,
	"reject_hallucination":            true,
	"invite_user":                     true,
	"transfer_user":                   true,
	"save_billing_group":              true,
	"update_billing_group":            true,
	"delete_billing_group":            true,
	"radioreference_import_to_system": true,
	"config_reload":                   true,
}

var copilotAdminReadActions = map[string]bool{
	"radioreference_search":  true,
	"radioreference_browse":  true,
	"tone_history_analyze":   true,
	"parse_tone_import":      true,
	"check_server_update":    true,
	"test_central_connection": true,
}

func (admin *Admin) copilotToolGetAdminConfig(argsJSON string) (string, error) {
	var args struct {
		Section       string         `json:"section"`
		SystemID      uint64         `json:"systemId"`
		TalkgroupID   uint64         `json:"talkgroupId"`
		TalkgroupRef  uint           `json:"talkgroupRef"`
		Params        map[string]any `json:"params"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	section := strings.TrimSpace(strings.ToLower(args.Section))
	if section == "" {
		section = "summary"
	}

	var out map[string]any

	switch section {
	case "summary":
		out = admin.copilotConfigSummary()
	case "all":
		out = map[string]any{"config": admin.GetConfig()}
	case "tags":
		out = map[string]any{"tags": admin.Controller.Tags.List}
	case "talkgroup_groups":
		out = map[string]any{"groups": admin.Controller.Groups.List}
	case "apikeys":
		out = map[string]any{"apikeys": admin.Controller.Apikeys.List}
	case "dirwatch":
		out = map[string]any{"dirwatch": admin.Controller.Dirwatches.List}
	case "downstreams":
		out = map[string]any{"downstreams": admin.Controller.Downstreams.List}
	case "options":
		out = map[string]any{"options": admin.Controller.Options}
	case "systems":
		out = map[string]any{"systems": admin.copilotSystemsSummary()}
	case "system":
		if args.SystemID == 0 {
			return "", fmt.Errorf("systemId is required when section=system")
		}
		sys, ok := admin.Controller.Systems.GetSystemById(args.SystemID)
		if !ok {
			return "", fmt.Errorf("system %d not found", args.SystemID)
		}
		out = map[string]any{"system": sys}
	case "talkgroup":
		systemID := args.SystemID
		tgID := args.TalkgroupID
		tgRef := args.TalkgroupRef
		if args.Params != nil {
			if systemID == 0 {
				if v, ok := args.Params["systemId"].(float64); ok {
					systemID = uint64(v)
				}
			}
			if tgID == 0 {
				if v, ok := args.Params["talkgroupId"].(float64); ok {
					tgID = uint64(v)
				}
			}
			if tgRef == 0 {
				if v, ok := args.Params["talkgroupRef"].(float64); ok {
					tgRef = uint(v)
				}
			}
		}
		if systemID == 0 {
			return "", fmt.Errorf("systemId is required when section=talkgroup")
		}
		result, err := admin.copilotGetTalkgroupConfig(systemID, tgID, tgRef)
		if err != nil {
			return "", err
		}
		out = result
	case "users":
		out = map[string]any{"users": admin.copilotUserList()}
	case "user_groups":
		cfg := admin.GetConfig()
		out = map[string]any{"userGroups": cfg["userGroups"]}
	case "keyword_lists":
		lists := []map[string]any{}
		for _, list := range admin.Controller.KeywordListsCache.GetAllLists() {
			lists = append(lists, map[string]any{
				"id": list.Id, "label": list.Label, "description": list.Description,
				"keywords": list.Keywords, "order": list.Order,
			})
		}
		out = map[string]any{"keywordLists": lists}
	case "health_settings":
		o := admin.Controller.Options
		out = map[string]any{
			"systemHealthAlertsEnabled":         o.SystemHealthAlertsEnabled,
			"transcriptionFailureAlertsEnabled": o.TranscriptionFailureAlertsEnabled,
			"toneDetectionAlertsEnabled":          o.ToneDetectionAlertsEnabled,
			"noAudioAlertsEnabled":              o.NoAudioAlertsEnabled,
			"transcriptionFailureThreshold":     o.TranscriptionFailureThreshold,
			"transcriptionFailureTimeWindow":    o.TranscriptionFailureTimeWindow,
			"toneDetectionIssueThreshold":       o.ToneDetectionIssueThreshold,
			"alertRetentionDays":                o.AlertRetentionDays,
			"noAudioThresholdMinutes":           o.NoAudioThresholdMinutes,
			"noAudioMultiplier":                 o.NoAudioMultiplier,
		}
	case "log_categories":
		out = map[string]any{"categories": LogCategoryInfoForAdmin(admin.Controller.Options.CentralManagementEnabled)}
	case "calls":
		result, err := admin.copilotSearchCalls(args.Params)
		if err != nil {
			return "", err
		}
		out = result
	case "capabilities":
		out = getCopilotCapabilities()
	case "transcription_failures":
		result, err := admin.copilotGetTranscriptionFailures()
		if err != nil {
			return "", err
		}
		out = result
	case "transcript_parser":
		out = map[string]any{"transcriptParserConfig": admin.Controller.Options.TranscriptParserConfig}
	case "relay_status":
		out = admin.copilotGetRelayStatus()
	case "hallucinations":
		result, err := admin.copilotGetHallucinations()
		if err != nil {
			return "", err
		}
		out = result
	default:
		return "", fmt.Errorf("unknown section %q — call get_admin_config section=capabilities for the full catalog", section)
	}

	b, _ := json.Marshal(out)
	return string(b), nil
}

func (admin *Admin) copilotConfigSummary() map[string]any {
	ctrl := admin.Controller
	opts := ctrl.Options
	tagLabels := make([]string, 0, len(ctrl.Tags.List))
	for _, t := range ctrl.Tags.List {
		tagLabels = append(tagLabels, t.Label)
	}
	return map[string]any{
		"version":               Version,
		"listenerCount":         ctrl.Clients.Count(),
		"systemCount":           len(ctrl.Systems.List),
		"tagCount":              len(ctrl.Tags.List),
		"tagLabels":             tagLabels,
		"userCount":             len(ctrl.Users.GetAllUsers()),
		"openaiConfigured":      strings.TrimSpace(opts.OpenAIIntegration.APIKey) != "",
		"transcriptionEnabled":  opts.TranscriptionConfig.Enabled,
		"stripeEnabled":         opts.StripePaywallEnabled,
		"emailEnabled":          opts.EmailServiceEnabled,
		"centralManagement":     opts.CentralManagementEnabled,
		"availableSections": []string{
			"summary", "capabilities", "tags", "talkgroup_groups", "apikeys", "dirwatch", "downstreams",
			"options", "systems", "system", "talkgroup", "users", "user_groups", "keyword_lists",
			"health_settings", "log_categories", "calls", "transcription_failures", "transcript_parser",
			"relay_status", "hallucinations", "all",
		},
		"availableWriteActions": copilotAdminWriteActionNames(),
		"availableReadActions":  copilotAdminReadActionNames(),
		"hint":                  "Call get_admin_config section=capabilities for workflows, payload shapes, and limitations",
	}
}

func copilotAdminWriteActionNames() []string {
	names := make([]string, 0, len(copilotAdminWriteActions))
	for k := range copilotAdminWriteActions {
		names = append(names, k)
	}
	return names
}

func copilotAdminReadActionNames() []string {
	names := make([]string, 0, len(copilotAdminReadActions))
	for k := range copilotAdminReadActions {
		names = append(names, k)
	}
	return names
}

func (admin *Admin) copilotSystemsSummary() []map[string]any {
	out := make([]map[string]any, 0, len(admin.Controller.Systems.List))
	for _, sys := range admin.Controller.Systems.List {
		out = append(out, map[string]any{
			"id":              sys.Id,
			"label":           sys.Label,
			"systemRef":       sys.SystemRef,
			"talkgroupCount":  len(sys.Talkgroups.List),
			"unitCount":       len(sys.Units.List),
			"siteCount":       len(sys.Sites.List),
		})
	}
	return out
}

func (admin *Admin) copilotUserList() []map[string]any {
	users := admin.Controller.Users.GetAllUsers()
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]any{
			"id": u.Id, "email": u.Email, "firstName": u.FirstName, "lastName": u.LastName,
			"verified": u.Verified, "systemAdmin": u.SystemAdmin, "userGroupId": u.UserGroupId,
			"pinExpiresAt": u.PinExpiresAt, "subscriptionStatus": u.SubscriptionStatus,
		})
	}
	return out
}

func (admin *Admin) copilotSearchCalls(params map[string]any) (map[string]any, error) {
	if params == nil {
		params = map[string]any{}
	}
	callOptions := NewCallSearchOptions().fromMap(params)
	adminClient := &Client{
		Controller:              admin.Controller,
		BypassPlaybackSearchACL: true,
	}
	results, err := admin.Controller.Calls.Search(callOptions, adminClient)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"count":   results.Count,
		"hasMore": results.HasMore,
		"calls":   results.Results,
	}, nil
}

func (admin *Admin) copilotToolApplyAdminChange(argsJSON string) (string, error) {
	var args struct {
		Action    string         `json:"action"`
		Confirmed bool           `json:"confirmed"`
		Payload   map[string]any `json:"payload"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	action := strings.TrimSpace(args.Action)
	if action == "" {
		return "", fmt.Errorf("action is required")
	}
	if args.Payload == nil {
		args.Payload = map[string]any{}
	}

	if copilotAdminReadActions[action] {
		return admin.copilotAdminReadAction(action, args.Payload)
	}
	if !copilotAdminWriteActions[action] {
		return "", fmt.Errorf("unknown action %q — call get_admin_config section=summary for availableWriteActions", action)
	}
	if !args.Confirmed {
		b, _ := json.Marshal(map[string]any{"applied": false, "error": "confirmed must be true to apply admin changes"})
		return string(b), nil
	}

	payloadJSON, _ := json.Marshal(args.Payload)
	var result map[string]any
	var err error

	switch action {
	case "save_tags":
		err = admin.copilotSaveTagsList(payloadJSON)
		result = map[string]any{"tags": admin.Controller.Tags.List}
	case "update_tags":
		wrapped := map[string]any{"confirmed": true}
		for k, v := range args.Payload {
			wrapped[k] = v
		}
		wb, _ := json.Marshal(wrapped)
		return admin.copilotToolApplyTagDefinitionUpdates(string(wb))
	case "save_talkgroup_groups":
		err = admin.copilotSaveTalkgroupGroups(payloadJSON)
		result = map[string]any{"groups": admin.Controller.Groups.List}
	case "save_apikeys":
		err = admin.copilotSaveApikeys(payloadJSON)
		result = map[string]any{"apikeys": admin.Controller.Apikeys.List}
	case "save_dirwatch":
		err = admin.copilotSaveDirwatch(payloadJSON)
		result = map[string]any{"dirwatch": admin.Controller.Dirwatches.List}
	case "save_downstreams":
		err = admin.copilotSaveDownstreams(payloadJSON)
		result = map[string]any{"downstreams": admin.Controller.Downstreams.List}
	case "save_system":
		err = admin.copilotSaveSystem(payloadJSON)
		result = map[string]any{"systems": admin.Controller.Systems.List}
	case "delete_system":
		result, err = admin.copilotDeleteSystem(payloadJSON)
	case "patch_options":
		err = admin.copilotPatchOptions(payloadJSON)
		result = map[string]any{"options": admin.Controller.Options}
	case "save_system_no_audio_settings":
		err = admin.copilotSaveSystemNoAudio(payloadJSON)
	case "patch_health_settings":
		err = admin.copilotPatchHealthSettings(payloadJSON)
	case "dismiss_health_alert":
		result, err = admin.copilotDismissAlert(payloadJSON)
	case "purge_data":
		result, err = admin.copilotPurgeData(payloadJSON)
	case "stripe_sync":
		result, err = admin.copilotStripeSync()
	case "sync_tone_sets":
		result, err = admin.copilotSyncToneSets(payloadJSON)
	case "create_user":
		result, err = admin.copilotCreateUser(payloadJSON)
	case "update_user":
		result, err = admin.copilotUpdateUser(payloadJSON)
	case "delete_user":
		result, err = admin.copilotDeleteUser(payloadJSON)
	case "reset_user_password":
		result, err = admin.copilotResetUserPassword(payloadJSON)
	case "create_keyword_list":
		result, err = admin.copilotCreateKeywordList(payloadJSON)
	case "update_keyword_list":
		result, err = admin.copilotUpdateKeywordList(payloadJSON)
	case "delete_keyword_list":
		result, err = admin.copilotDeleteKeywordList(payloadJSON)
	case "update_talkgroup_tags":
		wrapped := map[string]any{"confirmed": true}
		for k, v := range args.Payload {
			wrapped[k] = v
		}
		wb, _ := json.Marshal(wrapped)
		return admin.copilotToolApplyTalkgroupTagUpdates(string(wb))
	case "send_test_email":
		result, err = admin.copilotSendTestEmail(payloadJSON)
	case "change_admin_password":
		result, err = admin.copilotChangeAdminPassword(payloadJSON)
	case "update_talkgroup_tone_sets":
		result, err = admin.copilotUpdateTalkgroupToneSets(payloadJSON)
	case "update_talkgroup":
		result, err = admin.copilotUpdateTalkgroup(payloadJSON)
	case "reset_transcription_failures":
		result, err = admin.copilotResetTranscriptionFailures(payloadJSON)
	case "patch_transcript_parser":
		err = admin.copilotPatchTranscriptParser(payloadJSON)
	case "relay_unlock_public_client":
		err = admin.copilotRelayUnlockPublicClient()
	case "approve_hallucination":
		err = admin.copilotApproveHallucination(payloadJSON)
	case "reject_hallucination":
		err = admin.copilotRejectHallucination(payloadJSON)
	case "invite_user":
		result, err = admin.copilotInviteUser(payloadJSON)
	case "transfer_user":
		result, err = admin.copilotTransferUser(payloadJSON)
	case "save_billing_group":
		result, err = admin.copilotSaveBillingGroup(payloadJSON)
	case "update_billing_group":
		result, err = admin.copilotUpdateBillingGroup(payloadJSON)
	case "delete_billing_group":
		result, err = admin.copilotDeleteBillingGroup(payloadJSON)
	case "radioreference_import_to_system":
		result, err = admin.copilotRadioReferenceImportToSystem(payloadJSON)
	case "config_reload":
		err = admin.copilotConfigReload()
	default:
		return "", fmt.Errorf("action %q not implemented", action)
	}

	if err != nil {
		return "", err
	}
	if result == nil {
		result = map[string]any{"applied": true}
	} else {
		result["applied"] = true
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

func (admin *Admin) copilotAdminReadAction(action string, payload map[string]any) (string, error) {
	switch action {
	case "radioreference_search":
		query, _ := payload["query"].(string)
		if strings.TrimSpace(query) == "" {
			return "", fmt.Errorf("payload.query is required")
		}
		if !admin.Controller.Options.RadioReferenceEnabled {
			return "", fmt.Errorf("radio reference is not enabled")
		}
		rr := NewRadioReferenceService(
			admin.Controller.Options.RadioReferenceUsername,
			admin.Controller.Options.RadioReferencePassword,
			admin.Controller.Options.RadioReferenceAPIKey,
		)
		systems, err := rr.SearchSystems(query)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{"systems": systems})
		return string(b), nil
	case "tone_history_analyze":
		payloadJSON, _ := json.Marshal(payload)
		var req ToneHistoryAnalyzeRequest
		if err := json.Unmarshal(payloadJSON, &req); err != nil {
			return "", err
		}
		result, err := admin.Controller.analyzeTalkgroupToneHistory(req.SystemId, req.TalkgroupId, req.Limit, req.Hours)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	case "parse_tone_import":
		result, err := admin.copilotParseToneImport(payload)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	case "radioreference_browse":
		result, err := admin.copilotRadioReferenceBrowse(payload)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	case "check_server_update":
		result, err := admin.copilotCheckServerUpdate()
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	case "test_central_connection":
		result, err := admin.copilotTestCentralConnection()
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	default:
		return "", fmt.Errorf("unknown read action %q", action)
	}
}

func (admin *Admin) copilotFinishWrite(action string) {
	go admin.Controller.EmitConfig()
	admin.Controller.SyncConfigToFile()
	admin.Controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("copilot: %s", action))
}

func decodeList(payloadJSON []byte, key string) ([]any, error) {
	var raw any
	if err := json.Unmarshal(payloadJSON, &raw); err != nil {
		return nil, err
	}
	switch v := raw.(type) {
	case []any:
		return v, nil
	case map[string]any:
		if key != "" {
			if list, ok := v[key].([]any); ok {
				return list, nil
			}
		}
	}
	return nil, fmt.Errorf("expected array or payload.%s array", key)
}

func (admin *Admin) copilotSaveTagsList(payloadJSON []byte) error {
	list, err := decodeList(payloadJSON, "tags")
	if err != nil {
		return err
	}
	admin.mutex.Lock()
	defer admin.mutex.Unlock()
	admin.Controller.Tags.FromMap(list)
	if err = admin.Controller.Tags.Write(admin.Controller.Database); err != nil {
		return err
	}
	err = admin.Controller.Tags.Read(admin.Controller.Database)
	if err == nil {
		admin.copilotFinishWrite("save_tags")
	}
	return err
}

func (admin *Admin) copilotSaveTalkgroupGroups(payloadJSON []byte) error {
	list, err := decodeList(payloadJSON, "groups")
	if err != nil {
		return err
	}
	admin.mutex.Lock()
	defer admin.mutex.Unlock()
	admin.Controller.Groups.FromMap(list)
	if err = admin.Controller.Groups.Write(admin.Controller.Database); err != nil {
		return err
	}
	err = admin.Controller.Groups.Read(admin.Controller.Database)
	if err == nil {
		admin.copilotFinishWrite("save_talkgroup_groups")
	}
	return err
}

func (admin *Admin) copilotSaveApikeys(payloadJSON []byte) error {
	list, err := decodeList(payloadJSON, "apikeys")
	if err != nil {
		return err
	}
	admin.mutex.Lock()
	defer admin.mutex.Unlock()
	admin.Controller.Apikeys.FromMap(list)
	if err = admin.Controller.Apikeys.Write(admin.Controller.Database); err != nil {
		return err
	}
	err = admin.Controller.Apikeys.Read(admin.Controller.Database)
	if err == nil {
		admin.copilotFinishWrite("save_apikeys")
	}
	return err
}

func (admin *Admin) copilotSaveDirwatch(payloadJSON []byte) error {
	list, err := decodeList(payloadJSON, "dirwatch")
	if err != nil {
		return err
	}
	admin.mutex.Lock()
	defer admin.mutex.Unlock()
	admin.Controller.Dirwatches.Stop()
	admin.Controller.Dirwatches.FromMap(list)
	if err = admin.Controller.Dirwatches.Write(admin.Controller.Database); err != nil {
		return err
	}
	err = admin.Controller.Dirwatches.Read(admin.Controller.Database)
	admin.Controller.Dirwatches.Start(admin.Controller)
	if err == nil {
		admin.copilotFinishWrite("save_dirwatch")
	}
	return err
}

func (admin *Admin) copilotSaveDownstreams(payloadJSON []byte) error {
	list, err := decodeList(payloadJSON, "downstreams")
	if err != nil {
		return err
	}
	admin.mutex.Lock()
	defer admin.mutex.Unlock()
	admin.Controller.Downstreams.FromMap(list)
	if err = admin.Controller.Downstreams.Write(admin.Controller.Database); err != nil {
		return err
	}
	err = admin.Controller.Downstreams.Read(admin.Controller.Database)
	if err == nil {
		admin.copilotFinishWrite("save_downstreams")
	}
	return err
}

func (admin *Admin) copilotSaveSystem(payloadJSON []byte) error {
	var incoming map[string]any
	if err := json.Unmarshal(payloadJSON, &incoming); err != nil {
		return err
	}
	if sys, ok := incoming["system"].(map[string]any); ok {
		incoming = sys
	}

	var existing *System
	if idVal, ok := incoming["id"].(float64); ok && idVal > 0 {
		existing, _ = admin.Controller.Systems.GetSystemById(uint64(idVal))
	}
	if existing != nil {
		if _, has := incoming["noAudioAlertsEnabled"]; !has {
			incoming["noAudioAlertsEnabled"] = existing.NoAudioAlertsEnabled
		}
		if _, has := incoming["noAudioThresholdMinutes"]; !has {
			incoming["noAudioThresholdMinutes"] = existing.NoAudioThresholdMinutes
		}
	}

	admin.mutex.Lock()
	defer admin.mutex.Unlock()

	currentJSON, err := json.Marshal(admin.Controller.Systems.List)
	if err != nil {
		return err
	}
	var arr []any
	if err = json.Unmarshal(currentJSON, &arr); err != nil {
		return err
	}
	replaced := false
	if idVal, ok := incoming["id"].(float64); ok && idVal > 0 {
		for i, r := range arr {
			if m, ok := r.(map[string]any); ok {
				if mid, ok := m["id"].(float64); ok && mid == idVal {
					arr[i] = incoming
					replaced = true
					break
				}
			}
		}
	}
	if !replaced {
		arr = append(arr, incoming)
	}

	admin.Controller.Systems.FromMap(arr)
	if err = admin.Controller.Systems.Write(admin.Controller.Database); err != nil {
		_ = admin.Controller.Systems.Read(admin.Controller.Database)
		return err
	}
	if err = admin.Controller.Systems.Read(admin.Controller.Database); err != nil {
		return err
	}
	_ = admin.Controller.IdLookupsCache.Read(admin.Controller.Database)
	admin.copilotFinishWrite("save_system")
	return nil
}

func (admin *Admin) copilotDeleteSystem(payloadJSON []byte) (map[string]any, error) {
	var req struct {
		SystemID uint64 `json:"systemId"`
	}
	if err := json.Unmarshal(payloadJSON, &req); err != nil {
		return nil, err
	}
	if req.SystemID == 0 {
		return nil, fmt.Errorf("systemId is required")
	}

	admin.mutex.Lock()
	defer admin.mutex.Unlock()

	currentJSON, err := json.Marshal(admin.Controller.Systems.List)
	if err != nil {
		return nil, err
	}
	var arr []any
	if err = json.Unmarshal(currentJSON, &arr); err != nil {
		return nil, err
	}
	found := false
	filtered := make([]any, 0, len(arr))
	for _, r := range arr {
		if m, ok := r.(map[string]any); ok {
			if mid, ok := m["id"].(float64); ok && uint64(mid) == req.SystemID {
				found = true
				continue
			}
		}
		filtered = append(filtered, r)
	}
	if !found {
		return nil, fmt.Errorf("system %d not found", req.SystemID)
	}
	admin.Controller.Systems.FromMap(filtered)
	if err = admin.Controller.Systems.Write(admin.Controller.Database); err != nil {
		_ = admin.Controller.Systems.Read(admin.Controller.Database)
		return nil, err
	}
	if err = admin.Controller.Systems.Read(admin.Controller.Database); err != nil {
		return nil, err
	}
	_ = admin.Controller.IdLookupsCache.Read(admin.Controller.Database)
	admin.copilotFinishWrite("delete_system")
	return map[string]any{"deletedSystemId": req.SystemID}, nil
}

func (admin *Admin) copilotPatchOptions(payloadJSON []byte) error {
	var partial map[string]any
	if err := json.Unmarshal(payloadJSON, &partial); err != nil {
		return err
	}
	if opts, ok := partial["options"].(map[string]any); ok {
		partial = opts
	}
	if len(partial) == 0 {
		return fmt.Errorf("no option keys in payload")
	}
	if v, ok := partial["adminPasswordLoginDisabled"].(bool); ok && v {
		if !admin.Controller.Users.HasSystemAdmin() {
			return fmt.Errorf("cannot disable password login: no system administrator user exists")
		}
	}
	admin.mutex.Lock()
	err := admin.Controller.Options.ApplyPartial(admin.Controller.Database, partial)
	admin.mutex.Unlock()
	if err != nil {
		return err
	}
	admin.copilotFinishWrite("patch_options")
	return nil
}

func (admin *Admin) copilotSaveSystemNoAudio(payloadJSON []byte) error {
	var request struct {
		SystemID                uint `json:"systemId"`
		NoAudioAlertsEnabled    bool `json:"noAudioAlertsEnabled"`
		NoAudioThresholdMinutes uint `json:"noAudioThresholdMinutes"`
	}
	if err := json.Unmarshal(payloadJSON, &request); err != nil {
		return err
	}
	system, ok := admin.Controller.Systems.GetSystemById(uint64(request.SystemID))
	if !ok {
		return fmt.Errorf("system %d not found", request.SystemID)
	}
	system.NoAudioAlertsEnabled = request.NoAudioAlertsEnabled
	system.NoAudioThresholdMinutes = request.NoAudioThresholdMinutes
	if err := admin.Controller.Systems.Write(admin.Controller.Database); err != nil {
		return err
	}
	if err := admin.Controller.Systems.Read(admin.Controller.Database); err != nil {
		return err
	}
	admin.copilotFinishWrite("save_system_no_audio_settings")
	return nil
}

func (admin *Admin) copilotPatchHealthSettings(payloadJSON []byte) error {
	var request map[string]any
	if err := json.Unmarshal(payloadJSON, &request); err != nil {
		return err
	}
	if inner, ok := request["settings"].(map[string]any); ok {
		request = inner
	}

	db := admin.Controller.Database
	opts := admin.Controller.Options

	setBool := func(key string, set func(bool)) error {
		if v, ok := request[key].(bool); ok {
			if err := opts.WriteKey(db, key, v, func() { set(v) }); err != nil {
				return err
			}
		}
		return nil
	}
	setUint := func(key string, set func(uint)) error {
		if v, ok := request[key].(float64); ok {
			u := uint(v)
			if err := opts.WriteKey(db, key, u, func() { set(u) }); err != nil {
				return err
			}
		}
		return nil
	}

	if err := setBool("transcriptionFailureAlertsEnabled", func(v bool) { opts.TranscriptionFailureAlertsEnabled = v }); err != nil {
		return err
	}
	if err := setBool("toneDetectionAlertsEnabled", func(v bool) { opts.ToneDetectionAlertsEnabled = v }); err != nil {
		return err
	}
	if err := setBool("noAudioAlertsEnabled", func(v bool) { opts.NoAudioAlertsEnabled = v }); err != nil {
		return err
	}
	if err := setUint("transcriptionFailureThreshold", func(v uint) { opts.TranscriptionFailureThreshold = v }); err != nil {
		return err
	}
	if err := setUint("transcriptionFailureTimeWindow", func(v uint) { opts.TranscriptionFailureTimeWindow = v }); err != nil {
		return err
	}
	if err := setUint("toneDetectionIssueThreshold", func(v uint) { opts.ToneDetectionIssueThreshold = v }); err != nil {
		return err
	}
	if err := setUint("alertRetentionDays", func(v uint) { opts.AlertRetentionDays = v }); err != nil {
		return err
	}
	if err := setUint("noAudioThresholdMinutes", func(v uint) { opts.NoAudioThresholdMinutes = v }); err != nil {
		return err
	}
	if v, ok := request["noAudioMultiplier"].(float64); ok {
		if err := opts.WriteKey(db, "noAudioMultiplier", v, func() { opts.NoAudioMultiplier = v }); err != nil {
			return err
		}
	}
	admin.copilotFinishWrite("patch_health_settings")
	return nil
}

func (admin *Admin) copilotDismissAlert(payloadJSON []byte) (map[string]any, error) {
	var req struct {
		AlertID uint64 `json:"alertId"`
	}
	if err := json.Unmarshal(payloadJSON, &req); err != nil {
		return nil, err
	}
	if err := admin.Controller.DismissSystemAlert(req.AlertID); err != nil {
		return nil, err
	}
	return map[string]any{"dismissedAlertId": req.AlertID}, nil
}

func (admin *Admin) copilotPurgeData(payloadJSON []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(payloadJSON, &m); err != nil {
		return nil, err
	}
	purgeType, _ := m["type"].(string)
	if purgeType != "calls" && purgeType != "logs" {
		return nil, fmt.Errorf("payload.type must be calls or logs")
	}

	var ids []uint64
	if idsInterface, ok := m["ids"].([]any); ok {
		for _, idInterface := range idsInterface {
			if v, ok := idInterface.(float64); ok {
				ids = append(ids, uint64(v))
			}
		}
	}

	switch purgeType {
	case "calls":
		if len(ids) > 0 {
			if err := admin.Controller.Calls.DeleteByIDs(admin.Controller.Database, ids); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": len(ids), "type": "calls"}, nil
		}
		if err := admin.Controller.Calls.PurgeAll(admin.Controller.Database); err != nil {
			return nil, err
		}
		return map[string]any{"purged": "all_calls"}, nil
	case "logs":
		if len(ids) > 0 {
			if err := admin.Controller.Logs.DeleteByIDs(admin.Controller.Database, ids); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": len(ids), "type": "logs"}, nil
		}
		if err := admin.Controller.Logs.PurgeAll(admin.Controller.Database); err != nil {
			return nil, err
		}
		return map[string]any{"purged": "all_logs"}, nil
	}
	return nil, fmt.Errorf("invalid purge type")
}

func (admin *Admin) copilotStripeSync() (map[string]any, error) {
	if !admin.Controller.Options.StripePaywallEnabled || admin.Controller.Options.StripeSecretKey == "" {
		return nil, fmt.Errorf("stripe is not enabled or secret key is missing")
	}
	stripe.Key = admin.Controller.Options.StripeSecretKey
	params := &stripe.CustomerListParams{}
	params.Limit = stripe.Int64(100)
	customersByEmail := make(map[string]*stripe.Customer)
	iter := customer.List(params)
	for iter.Next() {
		c := iter.Customer()
		if c.Email != "" {
			customersByEmail[strings.ToLower(c.Email)] = c
		}
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	users := admin.Controller.Users.GetAllUsers()
	matched := 0
	for _, user := range users {
		if stripeCustomer, ok := customersByEmail[strings.ToLower(user.Email)]; ok {
			user.StripeCustomerId = stripeCustomer.ID
			if stripeCustomer.Subscriptions != nil && len(stripeCustomer.Subscriptions.Data) > 0 {
				sub := stripeCustomer.Subscriptions.Data[0]
				user.StripeSubscriptionId = sub.ID
				user.SubscriptionStatus = string(sub.Status)
			}
			if err := admin.Controller.Users.Update(user); err != nil {
				continue
			}
			matched++
		}
	}
	if err := admin.Controller.Users.Write(admin.Controller.Database); err != nil {
		return nil, err
	}
	admin.Controller.SyncConfigToFile()
	return map[string]any{
		"matched":         matched,
		"totalUsers":      len(users),
		"stripeCustomers": len(customersByEmail),
	}, nil
}

func (admin *Admin) copilotSyncToneSets(payloadJSON []byte) (map[string]any, error) {
	var req struct {
		URL      string `json:"url"`
		APIKey   string `json:"apiKey"`
		ToneSets []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"toneSets"`
	}
	if err := json.Unmarshal(payloadJSON, &req); err != nil || req.URL == "" {
		return nil, fmt.Errorf("payload.url and payload.toneSets are required")
	}
	base := strings.TrimSuffix(req.URL, "/api/tone-alert")
	base = strings.TrimRight(base, "/")
	syncURL := base + "/api/sync-tone-sets"
	payload, _ := json.Marshal(map[string]any{"toneSets": req.ToneSets})
	proxyReq, err := http.NewRequest(http.MethodPost, syncURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	proxyReq.Header.Set("Content-Type", "application/json")
	if req.APIKey != "" {
		proxyReq.Header.Set("X-API-Key", req.APIKey)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(proxyReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("sync-tone-sets status %d: %s", resp.StatusCode, string(body))
	}
	return map[string]any{"status": resp.StatusCode, "body": string(body)}, nil
}

func (admin *Admin) copilotCreateUser(payloadJSON []byte) (map[string]any, error) {
	var request struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		FirstName   string `json:"firstName"`
		LastName    string `json:"lastName"`
		ZipCode     string `json:"zipCode"`
		UserGroupId uint64 `json:"userGroupId"`
	}
	if err := json.Unmarshal(payloadJSON, &request); err != nil {
		return nil, err
	}
	if request.Email == "" || request.Password == "" {
		return nil, fmt.Errorf("email and password are required")
	}
	request.Email = NormalizeEmail(request.Email)
	if admin.Controller.Users.GetUserByEmail(request.Email) != nil {
		return nil, fmt.Errorf("email is already registered")
	}
	if len(request.Password) < 6 {
		return nil, fmt.Errorf("password must be at least 6 characters")
	}
	pin, err := admin.Controller.Users.GenerateUniquePin(0)
	if err != nil {
		return nil, err
	}
	user := NewUser(request.Email, request.Password)
	if err := user.HashPassword(request.Password); err != nil {
		return nil, err
	}
	user.FirstName = request.FirstName
	user.LastName = request.LastName
	user.ZipCode = request.ZipCode
	user.Pin = pin
	user.PinExpiresAt = 0
	user.Verified = true
	user.VerificationToken = ""
	user.CreatedAt = fmt.Sprintf("%d", time.Now().Unix())
	user.LastLogin = "0"
	user.UserGroupId = request.UserGroupId
	user.Systems = "*"
	if err := admin.Controller.Users.SaveNewUser(user, admin.Controller.Database); err != nil {
		return nil, err
	}
	admin.Controller.SyncConfigToFile()
	return map[string]any{"userId": user.Id, "email": user.Email, "pin": user.Pin}, nil
}

func (admin *Admin) copilotUpdateUser(payloadJSON []byte) (map[string]any, error) {
	var request struct {
		UserID    uint64 `json:"userId"`
		Email     string `json:"email"`
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
		ZipCode   string `json:"zipCode"`
		Verified  *bool  `json:"verified"`
		Pin       *string `json:"pin"`
	}
	if err := json.Unmarshal(payloadJSON, &request); err != nil {
		return nil, err
	}
	if request.UserID == 0 {
		return nil, fmt.Errorf("userId is required")
	}
	user := admin.Controller.Users.GetUserById(request.UserID)
	if user == nil {
		return nil, fmt.Errorf("user not found")
	}
	if request.Email != "" {
		if err := ValidateEmail(request.Email); err != nil {
			return nil, err
		}
		user.Email = NormalizeEmail(request.Email)
	}
	if request.FirstName != "" {
		user.FirstName = strings.TrimSpace(request.FirstName)
	}
	if request.LastName != "" {
		user.LastName = strings.TrimSpace(request.LastName)
	}
	if request.ZipCode != "" {
		zipCode := strings.TrimSpace(request.ZipCode)
		zipOk, _ := regexp.MatchString(`^\d{5}(-\d{4})?$`, zipCode)
		if !zipOk {
			return nil, fmt.Errorf("invalid zip code format")
		}
		user.ZipCode = zipCode
	}
	if request.Verified != nil {
		user.Verified = *request.Verified
	}
	if request.Pin != nil {
		pinValue := strings.TrimSpace(*request.Pin)
		if pinValue != "" && !admin.Controller.Users.IsPinAvailable(pinValue, user.Id) {
			return nil, fmt.Errorf("PIN already in use")
		}
		if pinValue != "" {
			user.Pin = pinValue
		}
	}
	if err := admin.Controller.Users.Update(user); err != nil {
		return nil, err
	}
	if err := admin.Controller.Users.Write(admin.Controller.Database); err != nil {
		return nil, err
	}
	admin.Controller.SyncConfigToFile()
	return map[string]any{"userId": user.Id, "email": user.Email}, nil
}

func (admin *Admin) copilotDeleteUser(payloadJSON []byte) (map[string]any, error) {
	var req struct {
		UserID uint64 `json:"userId"`
	}
	if err := json.Unmarshal(payloadJSON, &req); err != nil {
		return nil, err
	}
	user := admin.Controller.Users.GetUserById(req.UserID)
	if user == nil {
		return nil, fmt.Errorf("user not found")
	}
	tx, err := admin.Controller.Database.Sql.Begin()
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`DELETE FROM "userAlertPreferences" WHERE "userId" = $1`, req.UserID); err != nil {
		tx.Rollback()
		return nil, err
	}
	if _, err = tx.Exec(`DELETE FROM "deviceTokens" WHERE "userId" = $1`, req.UserID); err != nil {
		tx.Rollback()
		return nil, err
	}
	if _, err = tx.Exec(`DELETE FROM "users" WHERE "userId" = $1`, req.UserID); err != nil {
		tx.Rollback()
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	_ = admin.Controller.Users.Remove(req.UserID)
	admin.Controller.SyncConfigToFile()
	return map[string]any{"deletedUserId": req.UserID}, nil
}

func (admin *Admin) copilotResetUserPassword(payloadJSON []byte) (map[string]any, error) {
	var req struct {
		UserID      uint64 `json:"userId"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.Unmarshal(payloadJSON, &req); err != nil {
		return nil, err
	}
	user := admin.Controller.Users.GetUserById(req.UserID)
	if user == nil {
		return nil, fmt.Errorf("user not found")
	}
	if len(req.NewPassword) < 6 {
		return nil, fmt.Errorf("password must be at least 6 characters")
	}
	if err := user.HashPassword(req.NewPassword); err != nil {
		return nil, err
	}
	if err := admin.Controller.Users.Update(user); err != nil {
		return nil, err
	}
	if err := admin.Controller.Users.Write(admin.Controller.Database); err != nil {
		return nil, err
	}
	return map[string]any{"userId": user.Id}, nil
}

func (admin *Admin) copilotCreateKeywordList(payloadJSON []byte) (map[string]any, error) {
	var list map[string]any
	if err := json.Unmarshal(payloadJSON, &list); err != nil {
		return nil, err
	}
	if inner, ok := list["keywordList"].(map[string]any); ok {
		list = inner
	}
	label, _ := list["label"].(string)
	var keywords []string
	if v, ok := list["keywords"].([]any); ok {
		for _, kw := range v {
			if k, ok := kw.(string); ok {
				keywords = append(keywords, k)
			}
		}
	}
	description, _ := list["description"].(string)
	order := uint(0)
	if v, ok := list["order"].(float64); ok {
		order = uint(v)
	}
	keywordsJSON, _ := json.Marshal(keywords)
	query := fmt.Sprintf(`INSERT INTO "keywordLists" ("label", "description", "keywords", "order", "createdAt") VALUES ('%s', '%s', '%s', %d, %d) RETURNING "keywordListId"`,
		escapeQuotes(label), escapeQuotes(description), escapeQuotes(string(keywordsJSON)), order, time.Now().UnixMilli())
	var listId uint64
	if err := admin.Controller.Database.Sql.QueryRow(query).Scan(&listId); err != nil {
		return nil, err
	}
	_ = admin.Controller.KeywordListsCache.Read(admin.Controller.Database)
	return map[string]any{"id": listId}, nil
}

func (admin *Admin) copilotUpdateKeywordList(payloadJSON []byte) (map[string]any, error) {
	var req struct {
		ID   uint64         `json:"id"`
		List map[string]any `json:"keywordList"`
	}
	if err := json.Unmarshal(payloadJSON, &req); err != nil {
		return nil, err
	}
	list := req.List
	if list == nil {
		_ = json.Unmarshal(payloadJSON, &list)
	}
	if req.ID == 0 {
		if v, ok := list["id"].(float64); ok {
			req.ID = uint64(v)
		}
	}
	if req.ID == 0 {
		return nil, fmt.Errorf("id is required")
	}
	label, _ := list["label"].(string)
	description, _ := list["description"].(string)
	var keywords []string
	if v, ok := list["keywords"].([]any); ok {
		for _, kw := range v {
			if k, ok := kw.(string); ok {
				keywords = append(keywords, k)
			}
		}
	}
	order := uint(0)
	if v, ok := list["order"].(float64); ok {
		order = uint(v)
	}
	keywordsJSON, _ := json.Marshal(keywords)
	query := fmt.Sprintf(`UPDATE "keywordLists" SET "label" = '%s', "description" = '%s', "keywords" = '%s', "order" = %d WHERE "keywordListId" = %d`,
		escapeQuotes(label), escapeQuotes(description), escapeQuotes(string(keywordsJSON)), order, req.ID)
	if _, err := admin.Controller.Database.Sql.Exec(query); err != nil {
		return nil, err
	}
	_ = admin.Controller.KeywordListsCache.Read(admin.Controller.Database)
	return map[string]any{"id": req.ID}, nil
}

func (admin *Admin) copilotDeleteKeywordList(payloadJSON []byte) (map[string]any, error) {
	var req struct {
		ID uint64 `json:"id"`
	}
	if err := json.Unmarshal(payloadJSON, &req); err != nil {
		return nil, err
	}
	if req.ID == 0 {
		return nil, fmt.Errorf("id is required")
	}
	if _, err := admin.Controller.Database.Sql.Exec(`DELETE FROM "keywordLists" WHERE "keywordListId" = $1`, req.ID); err != nil {
		return nil, err
	}
	_ = admin.Controller.KeywordListsCache.Read(admin.Controller.Database)
	return map[string]any{"deletedId": req.ID}, nil
}

func (admin *Admin) copilotSendTestEmail(payloadJSON []byte) (map[string]any, error) {
	var req struct {
		To      string `json:"to"`
		ToEmail string `json:"toEmail"`
	}
	if err := json.Unmarshal(payloadJSON, &req); err != nil {
		return nil, err
	}
	to := strings.TrimSpace(req.ToEmail)
	if to == "" {
		to = strings.TrimSpace(req.To)
	}
	if to == "" {
		return nil, fmt.Errorf("payload.toEmail is required")
	}
	if !admin.Controller.Options.EmailServiceEnabled {
		return nil, fmt.Errorf("email service is not enabled")
	}
	if err := admin.Controller.EmailService.SendTestEmail(to); err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "to": to}, nil
}

func (admin *Admin) copilotChangeAdminPassword(payloadJSON []byte) (map[string]any, error) {
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.Unmarshal(payloadJSON, &req); err != nil {
		return nil, err
	}
	if err := admin.ChangePassword(req.CurrentPassword, req.NewPassword); err != nil {
		return nil, err
	}
	return map[string]any{"passwordNeedChange": admin.Controller.Options.adminPasswordNeedChange}, nil
}
