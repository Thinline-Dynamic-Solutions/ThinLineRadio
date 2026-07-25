// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rdio-scanner/server/mapping"
)

// CallNaturesHandler handles GET/POST /api/call-natures
func (api *Api) CallNaturesHandler(w http.ResponseWriter, r *http.Request) {
	client := api.getClient(r)
	if client == nil || (!client.IsAdmin && client.User == nil) {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	switch r.Method {
	case http.MethodGet:
		natures := api.Controller.CallNaturesCache.GetAll()
		out := make([]map[string]any, 0, len(natures))
		for _, n := range natures {
			out = append(out, callNatureToJSON(n))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)

	case http.MethodPost:
		if !api.isAdmin(client) {
			api.exitWithError(w, http.StatusForbidden, "admin only")
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			api.exitWithError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		label := strings.ToUpper(strings.TrimSpace(stringFromAny(body["label"])))
		if label == "" {
			api.exitWithError(w, http.StatusBadRequest, "label required")
			return
		}
		phrases := sanitizeCallNaturePhrases(stringsFromAnySlice(body["phrases"]))
		if len(phrases) == 0 {
			phrases = []string{label}
		}
		order := uintFromAny(body["order"])
		expireMinutes := clampCallNatureExpireMinutes(uintFromAny(body["expireMinutes"]))
		enabled := true
		if v, ok := body["enabled"].(bool); ok {
			enabled = v
		}
		phrasesJSON, _ := json.Marshal(phrases)
		var id int64
		err := api.Controller.Database.Sql.QueryRow(
			`INSERT INTO "callNatures" ("label", "phrases", "enabled", "order", "expireMinutes", "createdAt")
			 VALUES ($1, $2, $3, $4, $5, $6) RETURNING "callNatureId"`,
			label, string(phrasesJSON), enabled, order, expireMinutes, time.Now().UnixMilli(),
		).Scan(&id)
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("insert failed: %v", err))
			return
		}
		_ = api.Controller.CallNaturesCache.Read(api.Controller.Database)
		row := api.Controller.Database.Sql.QueryRow(`SELECT "callNatureId", "label", "phrases", "enabled", "order", "expireMinutes", "createdAt"
			FROM "callNatures" WHERE "callNatureId" = $1`, id)
		n, _ := callNatureFromRow(row)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(callNatureToJSON(n))

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// CallNatureHandler handles PUT/DELETE /api/call-natures/{id}
func (api *Api) CallNatureHandler(w http.ResponseWriter, r *http.Request) {
	client := api.getClient(r)
	if client == nil || !api.isAdmin(client) {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/api/call-natures/")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil || id <= 0 {
		api.exitWithError(w, http.StatusBadRequest, "invalid id")
		return
	}

	switch r.Method {
	case http.MethodPut:
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			api.exitWithError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		label := strings.ToUpper(strings.TrimSpace(stringFromAny(body["label"])))
		if label == "" {
			api.exitWithError(w, http.StatusBadRequest, "label required")
			return
		}
		phrases := sanitizeCallNaturePhrases(stringsFromAnySlice(body["phrases"]))
		order := uintFromAny(body["order"])
		expireMinutes := clampCallNatureExpireMinutes(uintFromAny(body["expireMinutes"]))
		enabled := true
		if v, ok := body["enabled"].(bool); ok {
			enabled = v
		}
		phrasesJSON, _ := json.Marshal(phrases)
		_, err := api.Controller.Database.Sql.Exec(
			`UPDATE "callNatures" SET "label" = $1, "phrases" = $2, "enabled" = $3, "order" = $4, "expireMinutes" = $5 WHERE "callNatureId" = $6`,
			label, string(phrasesJSON), enabled, order, expireMinutes, id,
		)
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("update failed: %v", err))
			return
		}
		_ = api.Controller.CallNaturesCache.Read(api.Controller.Database)
		row := api.Controller.Database.Sql.QueryRow(`SELECT "callNatureId", "label", "phrases", "enabled", "order", "expireMinutes", "createdAt"
			FROM "callNatures" WHERE "callNatureId" = $1`, id)
		n, _ := callNatureFromRow(row)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(callNatureToJSON(n))

	case http.MethodDelete:
		if _, err := api.Controller.Database.Sql.Exec(`DELETE FROM "callNatures" WHERE "callNatureId" = $1`, id); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("delete failed: %v", err))
			return
		}
		_ = api.Controller.CallNaturesCache.Read(api.Controller.Database)
		w.WriteHeader(http.StatusNoContent)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func stringsFromAnySlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range arr {
		if s, ok := item.(string); ok {
			s = strings.ToUpper(strings.TrimSpace(s))
			if s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func sanitizeCallNaturePhrases(phrases []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range phrases {
		p = strings.ToUpper(strings.TrimSpace(p))
		if p == "" || seen[p] || !mapping.IsAcceptableCallNaturePhrase(p) {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func uintFromAny(v any) uint {
	// Cap at int4 max: every column these values land in is a Postgres
	// integer, and float64→uint conversion above that range is undefined.
	const maxInt4 = 2147483647
	switch n := v.(type) {
	case float64:
		if n >= maxInt4 {
			return maxInt4
		}
		if n >= 0 {
			return uint(n)
		}
	case int:
		if n >= maxInt4 {
			return maxInt4
		}
		if n >= 0 {
			return uint(n)
		}
	}
	return 0
}

// clampCallNatureExpireMinutes mirrors the admin UI bound (7 days).
func clampCallNatureExpireMinutes(v uint) uint {
	const max = 10080
	if v > max {
		return max
	}
	return v
}
