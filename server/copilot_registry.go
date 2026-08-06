// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
)

// copilotAdminRoute is an allowlisted admin/API path the assistant may invoke.
type copilotAdminRoute struct {
	ID          string
	Method      string
	Path        string
	Write       bool
	Description string
}

// Deny-list substrings — never proxy these (binaries, auth session, secrets dump).
var copilotAdminPathDeny = []string{
	"/api/admin/login",
	"/api/admin/logout",
	"/api/admin/sso",
	"/api/admin/email-logo",
	"/api/admin/favicon",
	"/api/admin/call-audio/",
}

func copilotAdminRouteRegistry() []copilotAdminRoute {
	return []copilotAdminRoute{
		// Config / options
		{ID: "get_config", Method: "GET", Path: "/api/admin/config", Description: "Full admin config export (prefer narrower tools)"},
		{ID: "patch_options", Method: "PATCH", Path: "/api/admin/options", Write: true, Description: "Partial options update"},
		{ID: "get_apikeys", Method: "GET", Path: "/api/admin/apikeys", Description: "List API keys"},
		{ID: "save_apikeys", Method: "PUT", Path: "/api/admin/apikeys", Write: true, Description: "Replace API keys list"},
		{ID: "get_tags", Method: "GET", Path: "/api/admin/tags", Description: "List tags"},
		{ID: "save_tags", Method: "PUT", Path: "/api/admin/tags", Write: true, Description: "Replace tags"},
		{ID: "get_talkgroup_groups", Method: "GET", Path: "/api/admin/talkgroup-groups", Description: "List talkgroup groups"},
		{ID: "save_talkgroup_groups", Method: "PUT", Path: "/api/admin/talkgroup-groups", Write: true, Description: "Replace talkgroup groups"},
		{ID: "get_downstreams", Method: "GET", Path: "/api/admin/downstreams", Description: "List downstreams"},
		{ID: "save_downstreams", Method: "PUT", Path: "/api/admin/downstreams", Write: true, Description: "Replace downstreams"},
		{ID: "get_dirwatch", Method: "GET", Path: "/api/admin/dirwatch", Description: "List dirwatch entries"},
		{ID: "save_dirwatch", Method: "PUT", Path: "/api/admin/dirwatch", Write: true, Description: "Replace dirwatch"},
		{ID: "save_system", Method: "PUT", Path: "/api/admin/systems/save", Write: true, Description: "Save one system"},
		{ID: "systems_order", Method: "PUT", Path: "/api/admin/systems/order", Write: true, Description: "Reorder systems"},
		{ID: "fs_browse", Method: "GET", Path: "/api/admin/fs/browse", Description: "Browse server filesystem folders"},
		{ID: "search_logs", Method: "POST", Path: "/api/admin/logs", Description: "Search logs"},
		{ID: "get_system_health", Method: "GET", Path: "/api/admin/systemhealth", Description: "System health alerts"},
		{ID: "purge", Method: "POST", Path: "/api/admin/purge", Write: true, Description: "Purge data (destructive)"},
		{ID: "stripe_sync", Method: "POST", Path: "/api/admin/stripe-sync", Write: true, Description: "Stripe sync"},
		// Users / groups
		{ID: "list_users", Method: "GET", Path: "/api/admin/users", Description: "List users"},
		{ID: "create_user", Method: "POST", Path: "/api/admin/users/create", Write: true, Description: "Create user"},
		{ID: "bulk_delete_users", Method: "POST", Path: "/api/admin/users/bulk-delete", Write: true, Description: "Bulk delete users"},
		// Mapping
		{ID: "mapping_config", Method: "GET", Path: "/api/admin/mapping/config", Description: "Incident mapping config"},
		{ID: "mapping_data", Method: "GET", Path: "/api/admin/mapping/data", Description: "Mapping system data"},
		{ID: "mapping_boundaries_stats", Method: "GET", Path: "/api/admin/mapping/boundaries/stats", Description: "Boundary stats"},
		{ID: "mapping_tone_set_locations", Method: "GET", Path: "/api/admin/mapping/tone-set-locations", Description: "Tone-set locations"},
		{ID: "mapping_apply_tone_set_locations", Method: "POST", Path: "/api/admin/mapping/apply-tone-set-locations", Write: true, Description: "Apply tone-set locations"},
		{ID: "mapping_suggest_tone_set_locations", Method: "POST", Path: "/api/admin/mapping/suggest-tone-set-locations", Description: "Suggest tone-set locations"},
		{ID: "mapping_talkgroup_locations", Method: "GET", Path: "/api/admin/mapping/talkgroup-locations", Description: "Talkgroup locations"},
		{ID: "mapping_apply_talkgroup_locations", Method: "POST", Path: "/api/admin/mapping/apply-talkgroup-locations", Write: true, Description: "Apply talkgroup locations"},
		{ID: "mapping_suggest_talkgroup_locations", Method: "POST", Path: "/api/admin/mapping/suggest-talkgroup-locations", Description: "Suggest talkgroup locations"},
		{ID: "mapping_regeocode", Method: "POST", Path: "/api/admin/mapping/regeocode/", Write: true, Description: "Regeocode a mapped call"},
		// Unit aliases
		{ID: "unit_alias_suggestions", Method: "GET", Path: "/api/admin/unit-alias-suggestions", Description: "Unit alias suggestions"},
		{ID: "unit_alias_apply", Method: "POST", Path: "/api/admin/unit-alias-suggestions/", Write: true, Description: "Apply/reject unit alias suggestion"},
		{ID: "unit_alias_history_scan", Method: "POST", Path: "/api/admin/unit-alias-history-scan", Write: true, Description: "Scan history for unit aliases"},
		// Transcript review
		{ID: "transcript_review", Method: "GET", Path: "/api/admin/transcript-review", Description: "Transcript review queue"},
		{ID: "transcript_review_call", Method: "GET", Path: "/api/admin/transcript-review/", Description: "Transcript review call detail/actions"},
		{ID: "transcript_parser", Method: "GET", Path: "/api/admin/transcript-parser", Description: "Transcript parser config"},
		// System settings
		{ID: "system_retention", Method: "GET", Path: "/api/admin/system-retention-settings", Description: "Per-system retention"},
		{ID: "system_retention_save", Method: "PUT", Path: "/api/admin/system-retention-settings", Write: true, Description: "Save retention settings"},
		{ID: "system_dupe_detection", Method: "GET", Path: "/api/admin/system-duplicate-detection-settings", Description: "Duplicate detection settings"},
		{ID: "system_dupe_detection_save", Method: "PUT", Path: "/api/admin/system-duplicate-detection-settings", Write: true, Description: "Save duplicate detection"},
		{ID: "system_no_audio", Method: "PUT", Path: "/api/admin/system-no-audio-settings", Write: true, Description: "Per-system no-audio settings"},
		{ID: "system_health_alert_settings", Method: "GET", Path: "/api/admin/system-health-alert-settings", Description: "Health alert settings"},
		{ID: "system_health_alert_settings_save", Method: "PUT", Path: "/api/admin/system-health-alert-settings", Write: true, Description: "Save health alert settings"},
		// Relay
		{ID: "relay_account_status", Method: "GET", Path: "/api/admin/relay-account/status", Description: "Relay account status"},
		{ID: "relay_suspension", Method: "GET", Path: "/api/admin/relay-suspension", Description: "Relay suspension status"},
		{ID: "relay_unlock_public", Method: "POST", Path: "/api/admin/relay-unlock-public-client", Write: true, Description: "Unlock public client"},
		{ID: "relay_billing_catalog", Method: "GET", Path: "/api/admin/relay-billing/catalog", Description: "Relay billing catalog"},
		// Call natures
		{ID: "list_call_natures", Method: "GET", Path: "/api/call-natures", Description: "List call natures"},
		{ID: "create_call_nature", Method: "POST", Path: "/api/call-natures", Write: true, Description: "Create call nature"},
		{ID: "call_nature", Method: "GET", Path: "/api/call-natures/", Description: "Get/update/delete one call nature"},
		{ID: "call_nature_phrase_suggestions", Method: "GET", Path: "/api/call-nature-phrase-suggestions", Description: "Call nature phrase suggestions"},
		{ID: "call_nature_phrase_apply", Method: "POST", Path: "/api/call-nature-phrase-suggestions/", Write: true, Description: "Apply/reject phrase suggestion"},
		{ID: "call_nature_phrase_scan", Method: "POST", Path: "/api/call-nature-phrase-scan", Write: true, Description: "Scan transcripts for phrase suggestions"},
		// Transcription
		{ID: "transcription_failures", Method: "GET", Path: "/api/admin/transcription-failures", Description: "Transcription failures"},
		{ID: "reset_transcription_failures", Method: "POST", Path: "/api/admin/transcription-failures", Write: true, Description: "Reset transcription failures"},
	}
}

func copilotFindAdminRoute(actionID, method, path string) (*copilotAdminRoute, error) {
	actionID = strings.TrimSpace(actionID)
	method = strings.ToUpper(strings.TrimSpace(method))
	path = strings.TrimSpace(path)

	for _, deny := range copilotAdminPathDeny {
		if path != "" && strings.Contains(path, deny) {
			return nil, fmt.Errorf("path is denied for assistant use")
		}
	}

	routes := copilotAdminRouteRegistry()
	if actionID != "" {
		for i := range routes {
			if routes[i].ID == actionID {
				return &routes[i], nil
			}
		}
		return nil, fmt.Errorf("unknown actionId %q — call run_admin_action with actionId=list to see options", actionID)
	}
	if method == "" || path == "" {
		return nil, fmt.Errorf("provide actionId or method+path")
	}
	for i := range routes {
		r := &routes[i]
		if r.Method == method && (r.Path == path || strings.HasPrefix(path, r.Path)) {
			return r, nil
		}
	}
	// Allow exact allowlisted path match even if not in curated list, as long as under /api/admin and not denied.
	if strings.HasPrefix(path, "/api/admin/") || strings.HasPrefix(path, "/api/call-natures") ||
		strings.HasPrefix(path, "/api/call-nature-phrase") {
		write := method != "GET" && method != "HEAD"
		return &copilotAdminRoute{
			ID:          "custom",
			Method:      method,
			Path:        path,
			Write:       write,
			Description: "Allowlisted admin path",
		}, nil
	}
	return nil, fmt.Errorf("path not allowlisted")
}

func (admin *Admin) copilotToolRunAdminAction(authToken string, argsJSON string) (string, error) {
	var args struct {
		ActionID  string          `json:"actionId"`
		Method    string          `json:"method"`
		Path      string          `json:"path"`
		Query     map[string]any  `json:"query"`
		Body      json.RawMessage `json:"body"`
		Confirmed bool            `json:"confirmed"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}

	if strings.TrimSpace(args.ActionID) == "list" {
		routes := copilotAdminRouteRegistry()
		items := make([]map[string]any, 0, len(routes))
		for _, r := range routes {
			items = append(items, map[string]any{
				"actionId":    r.ID,
				"method":      r.Method,
				"path":        r.Path,
				"write":       r.Write,
				"description": r.Description,
			})
		}
		b, _ := json.Marshal(map[string]any{"actions": items, "count": len(items)})
		return string(b), nil
	}

	route, err := copilotFindAdminRoute(args.ActionID, args.Method, args.Path)
	if err != nil {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error()})
		return string(b), nil
	}

	if route.Write && !args.Confirmed {
		b, _ := json.Marshal(map[string]any{
			"ok":           false,
			"applied":      false,
			"needsConfirm": true,
			"actionId":     route.ID,
			"method":       route.Method,
			"path":         route.Path,
			"error":        "confirmed must be true for write actions",
		})
		return string(b), nil
	}

	path := route.Path
	if args.Path != "" && (route.ID == "custom" || strings.HasPrefix(args.Path, route.Path)) {
		path = args.Path
	}
	if len(args.Query) > 0 {
		q := make([]string, 0, len(args.Query))
		for k, v := range args.Query {
			q = append(q, fmt.Sprintf("%s=%v", k, v))
		}
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path = path + sep + strings.Join(q, "&")
	}

	var bodyReader io.Reader
	if len(args.Body) > 0 && string(args.Body) != "null" {
		bodyReader = bytes.NewReader(args.Body)
	}

	req := httptest.NewRequest(route.Method, path, bodyReader)
	if req == nil {
		return "", fmt.Errorf("failed to build request")
	}
	req.RequestURI = path
	req.RemoteAddr = "127.0.0.1:1"
	if authToken != "" {
		req.Header.Set("Authorization", authToken)
	}
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	rr := httptest.NewRecorder()
	http.DefaultServeMux.ServeHTTP(rr, req)

	respBody, _ := io.ReadAll(rr.Body)
	contentType := rr.Header().Get("Content-Type")
	var parsed any
	if strings.Contains(contentType, "json") || (len(respBody) > 0 && (respBody[0] == '{' || respBody[0] == '[')) {
		_ = json.Unmarshal(respBody, &parsed)
		parsed = copilotRedactSecrets(parsed)
	} else {
		parsed = string(respBody)
		if len(respBody) > 4000 {
			parsed = string(respBody[:4000]) + "…[truncated]"
		}
	}

	out := map[string]any{
		"ok":         rr.Code >= 200 && rr.Code < 300,
		"status":     rr.Code,
		"actionId":   route.ID,
		"method":     route.Method,
		"path":       path,
		"write":      route.Write,
		"applied":    route.Write && rr.Code >= 200 && rr.Code < 300,
		"response":   parsed,
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

func copilotCompactActionIndex() string {
	routes := copilotAdminRouteRegistry()
	var b strings.Builder
	b.WriteString("Admin action index (use run_admin_action actionId=…; writes need confirmed=true):\n")
	for _, r := range routes {
		w := "R"
		if r.Write {
			w = "W"
		}
		fmt.Fprintf(&b, "- [%s] %s %s %s — %s\n", w, r.ID, r.Method, r.Path, r.Description)
	}
	b.WriteString("Also: db_query for guarded SQL; apply_admin_change for legacy write actions; get_admin_config for reads.\n")
	return b.String()
}
