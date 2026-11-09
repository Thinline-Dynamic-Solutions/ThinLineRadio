// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"rdio-scanner/server/mapping"
)

const (
	boundaryBBoxCacheTTL = 45 * time.Second
	boundaryBBoxCacheMax = 48
)

type boundaryBBoxCacheEntry struct {
	at       time.Time
	response []byte
}

var boundaryBBoxCache = struct {
	sync.Mutex
	items map[string]boundaryBBoxCacheEntry
	order []string
}{
	items: make(map[string]boundaryBBoxCacheEntry),
}

func boundaryBBoxCacheKey(west, south, east, north float64, layers []string) string {
	return fmt.Sprintf("%.4f,%.4f,%.4f,%.4f,%s", west, south, east, north, strings.Join(layers, "|"))
}

func getBoundaryBBoxCached(key string) ([]byte, bool) {
	boundaryBBoxCache.Lock()
	defer boundaryBBoxCache.Unlock()
	entry, ok := boundaryBBoxCache.items[key]
	if !ok || time.Since(entry.at) > boundaryBBoxCacheTTL {
		return nil, false
	}
	return append([]byte(nil), entry.response...), true
}

func putBoundaryBBoxCache(key string, response []byte) {
	boundaryBBoxCache.Lock()
	defer boundaryBBoxCache.Unlock()
	if _, exists := boundaryBBoxCache.items[key]; !exists {
		boundaryBBoxCache.order = append(boundaryBBoxCache.order, key)
	}
	boundaryBBoxCache.items[key] = boundaryBBoxCacheEntry{
		at:       time.Now(),
		response: append([]byte(nil), response...),
	}
	for len(boundaryBBoxCache.order) > boundaryBBoxCacheMax {
		oldest := boundaryBBoxCache.order[0]
		boundaryBBoxCache.order = boundaryBBoxCache.order[1:]
		delete(boundaryBBoxCache.items, oldest)
	}
}

func (api *Api) MappingConfigHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		api.Controller.Options.mutex.Lock()
		cfg := api.Controller.Options.MappingIntegration
		api.Controller.Options.mutex.Unlock()
		json.NewEncoder(w).Encode(cfg)
	case http.MethodPut:
		var raw map[string]any
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			api.exitWithError(w, http.StatusBadRequest, "invalid json")
			return
		}
		api.Controller.Options.mutex.Lock()
		applyMappingIntegrationFromMap(&api.Controller.Options.MappingIntegration, raw)
		api.Controller.Options.mutex.Unlock()
		if err := api.Controller.Options.Write(api.Controller.Database); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, err.Error())
			return
		}
		go api.Controller.EmitConfig()
		w.WriteHeader(http.StatusOK)
	default:
		api.exitWithError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (api *Api) MappingSystemDataHandler(w http.ResponseWriter, r *http.Request) {
	systemId, _ := strconv.ParseUint(r.URL.Query().Get("systemId"), 10, 64)
	if systemId == 0 {
		api.exitWithError(w, http.StatusBadRequest, "systemId required")
		return
	}
	talkgroupId, _ := strconv.ParseUint(r.URL.Query().Get("talkgroupId"), 10, 64)
	store := NewMappingStore(api.Controller.Database)

	switch r.Method {
	case http.MethodGet:
		kind := r.URL.Query().Get("kind")
		switch kind {
		case "streets":
			json.NewEncoder(w).Encode(map[string]any{"items": store.ListKnownStreets(systemId, talkgroupId)})
		case "corrections":
			json.NewEncoder(w).Encode(map[string]any{"items": store.ListCorrections(systemId, talkgroupId)})
		case "places":
			json.NewEncoder(w).Encode(map[string]any{"items": store.ListKnownPlacesRows(systemId, talkgroupId)})
		case "stats":
			json.NewEncoder(w).Encode(store.MappingStatsScoped(systemId, talkgroupId))
		default:
			json.NewEncoder(w).Encode(map[string]any{
				"streets":     store.ListKnownStreets(systemId, talkgroupId),
				"corrections": store.ListCorrections(systemId, talkgroupId),
				"places":      store.ListKnownPlacesRows(systemId, talkgroupId),
				"stats":       store.MappingStatsScoped(systemId, talkgroupId),
			})
		}
	case http.MethodPost:
		var body struct {
			Kind        string  `json:"kind"`
			StreetName  string  `json:"streetName"`
			BadName     string  `json:"badName"`
			CorrectName string  `json:"correctName"`
			DisplayName string  `json:"displayName"`
			Lat         float64 `json:"lat"`
			Lon         float64 `json:"lon"`
			AddressHint string  `json:"addressHint"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			api.exitWithError(w, http.StatusBadRequest, "invalid json")
			return
		}
		switch body.Kind {
		case "street":
			if err := store.AddKnownStreet(systemId, talkgroupId, body.StreetName); err != nil {
				api.exitWithError(w, http.StatusInternalServerError, err.Error())
				return
			}
		case "correction":
			if err := store.AddCorrection(systemId, talkgroupId, body.BadName, body.CorrectName); err != nil {
				api.exitWithError(w, http.StatusInternalServerError, err.Error())
				return
			}
		case "place":
			var tg *uint64
			if talkgroupId > 0 {
				tg = &talkgroupId
			}
			if err := store.UpsertKnownPlace(systemId, tg, body.DisplayName, body.Lat, body.Lon, body.AddressHint, "manual"); err != nil {
				api.exitWithError(w, http.StatusInternalServerError, err.Error())
				return
			}
		default:
			api.exitWithError(w, http.StatusBadRequest, "kind must be street, correction, or place")
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		kind := r.URL.Query().Get("kind")
		id, _ := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
		if id == 0 {
			api.exitWithError(w, http.StatusBadRequest, "id required")
			return
		}
		if err := store.DeleteMappingRow(kind, id); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		api.exitWithError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (api *Api) MappingToneSetLocationsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	systemId, _ := strconv.ParseUint(r.URL.Query().Get("systemId"), 10, 64)
	if systemId == 0 {
		api.exitWithError(w, http.StatusBadRequest, "systemId required")
		return
	}
	rows, err := api.Controller.ListToneSetLocations(systemId)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusBadRequest
		}
		api.exitWithError(w, status, err.Error())
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"toneSets": rows, "total": len(rows)})
}

func (api *Api) MappingApplyToneSetLocationsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		SystemId uint64                 `json:"systemId"`
		ToneSets []ToneSetLocationApply `json:"toneSets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.SystemId == 0 {
		api.exitWithError(w, http.StatusBadRequest, "systemId required")
		return
	}
	applied, cleared, err := api.Controller.ApplyToneSetLocations(body.SystemId, body.ToneSets)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusBadRequest
		}
		api.exitWithError(w, status, err.Error())
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"applied": applied,
		"cleared": cleared,
	})
}

func (api *Api) MappingSuggestToneSetLocationsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		SystemId  uint64 `json:"systemId"`
		OnlyEmpty *bool  `json:"onlyEmpty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.SystemId == 0 {
		api.exitWithError(w, http.StatusBadRequest, "systemId required")
		return
	}
	onlyEmpty := true
	if body.OnlyEmpty != nil {
		onlyEmpty = *body.OnlyEmpty
	}
	rows, err := api.Controller.SuggestToneSetLocations(body.SystemId, onlyEmpty)
	if err != nil {
		status := http.StatusInternalServerError
		msg := err.Error()
		if strings.Contains(msg, "not found") || strings.Contains(msg, "Gemini API key") {
			status = http.StatusBadRequest
		}
		api.exitWithError(w, status, msg)
		return
	}
	filled, skipped, failed := countLocationSuggestStats(len(rows), func(i int) (source, city string, lat, lon float64) {
		r := rows[i]
		return r.Source, r.GeoCity, r.GeoLat, r.GeoLon
	})
	json.NewEncoder(w).Encode(map[string]any{
		"toneSets": rows,
		"filled":   filled,
		"skipped":  skipped,
		"failed":   failed,
		"total":    len(rows),
	})
}

func (api *Api) MappingTalkgroupLocationsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	systemId, _ := strconv.ParseUint(r.URL.Query().Get("systemId"), 10, 64)
	if systemId == 0 {
		api.exitWithError(w, http.StatusBadRequest, "systemId required")
		return
	}
	rows, err := api.Controller.ListTalkgroupLocations(systemId)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusBadRequest
		}
		api.exitWithError(w, status, err.Error())
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"talkgroups": rows, "total": len(rows)})
}

func (api *Api) MappingApplyTalkgroupLocationsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		SystemId   uint64                   `json:"systemId"`
		Talkgroups []TalkgroupLocationApply `json:"talkgroups"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.SystemId == 0 {
		api.exitWithError(w, http.StatusBadRequest, "systemId required")
		return
	}
	applied, cleared, err := api.Controller.ApplyTalkgroupLocations(body.SystemId, body.Talkgroups)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusBadRequest
		}
		api.exitWithError(w, status, err.Error())
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"applied": applied,
		"cleared": cleared,
	})
}

func (api *Api) MappingSuggestTalkgroupLocationsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		SystemId     uint64   `json:"systemId"`
		OnlyEmpty    *bool    `json:"onlyEmpty"`
		TalkgroupIds []uint64 `json:"talkgroupIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.SystemId == 0 {
		api.exitWithError(w, http.StatusBadRequest, "systemId required")
		return
	}
	onlyEmpty := true
	if body.OnlyEmpty != nil {
		onlyEmpty = *body.OnlyEmpty
	}
	rows, err := api.Controller.SuggestTalkgroupLocations(body.SystemId, onlyEmpty, body.TalkgroupIds)
	if err != nil {
		status := http.StatusInternalServerError
		msg := err.Error()
		if strings.Contains(msg, "not found") || strings.Contains(msg, "Gemini API key") {
			status = http.StatusBadRequest
		}
		api.exitWithError(w, status, msg)
		return
	}
	filled, skipped, failed := countLocationSuggestStats(len(rows), func(i int) (source, city string, lat, lon float64) {
		r := rows[i]
		return r.Source, r.GeoCity, r.GeoLat, r.GeoLon
	})
	json.NewEncoder(w).Encode(map[string]any{
		"talkgroups": rows,
		"filled":     filled,
		"skipped":    skipped,
		"failed":     failed,
		"total":      len(rows),
	})
}

func countLocationSuggestStats(n int, at func(i int) (source, city string, lat, lon float64)) (filled, skipped, failed int) {
	for i := 0; i < n; i++ {
		source, city, lat, lon := at(i)
		switch {
		case source == "skipped":
			skipped++
		case lat != 0 && lon != 0:
			filled++
		case strings.TrimSpace(city) != "":
			filled++
		default:
			failed++
		}
	}
	return filled, skipped, failed
}

func (api *Api) MappingRegeocodeCallHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	rawPath := r.URL.Path
	path := strings.TrimPrefix(rawPath, "/api/admin/mapping/regeocode")
	path = strings.TrimPrefix(path, "/api/mapping/regeocode")
	path = strings.Trim(path, "/")
	// Batch: POST /api/admin/mapping/regeocode with JSON body {callIds:[...]}.
	if path == "" {
		api.mappingBatchRegeocode(w, r)
		return
	}
	callId, _ := strconv.ParseUint(path, 10, 64)
	if callId == 0 {
		api.exitWithError(w, http.StatusBadRequest, "callId required")
		return
	}
	if err := api.queueIncidentRegeocode(callId); err != nil {
		api.exitWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
}

const mappingBatchRegeocodeMax = 100

func (api *Api) mappingBatchRegeocode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CallIds []uint64 `json:"callIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(body.CallIds) == 0 {
		api.exitWithError(w, http.StatusBadRequest, "callIds required")
		return
	}
	if len(body.CallIds) > mappingBatchRegeocodeMax {
		api.exitWithError(w, http.StatusBadRequest, fmt.Sprintf("at most %d callIds", mappingBatchRegeocodeMax))
		return
	}
	queued := make([]uint64, 0, len(body.CallIds))
	failed := make([]map[string]any, 0)
	for _, id := range body.CallIds {
		if err := api.queueIncidentRegeocode(id); err != nil {
			failed = append(failed, map[string]any{"callId": id, "error": err.Error()})
			continue
		}
		queued = append(queued, id)
	}
	json.NewEncoder(w).Encode(map[string]any{
		"status": "queued",
		"queued": queued,
		"failed": failed,
	})
}

func (api *Api) queueIncidentRegeocode(callId uint64) error {
	if api == nil || api.Controller == nil || api.Controller.IncidentMappingQueue == nil {
		return fmt.Errorf("mapping unavailable")
	}
	if callId == 0 {
		return fmt.Errorf("callId required")
	}
	call, err := api.Controller.Calls.GetCall(callId)
	if err != nil || call == nil {
		return fmt.Errorf("call not found")
	}
	if strings.TrimSpace(call.Transcript) == "" {
		return fmt.Errorf("call has no transcript")
	}
	go api.Controller.IncidentMappingQueue.ProcessCall(call, call.Transcript)
	return nil
}

// MappingDiagnosticsHandler lists recent failed/skipped/unattempted mapped calls
// plus reason counts for the admin Incident Mapping panel.
func (api *Api) MappingDiagnosticsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	nowMs := time.Now().UnixMilli()
	since := nowMs - int64(24)*60*60*1000
	until := nowMs
	if v := r.URL.Query().Get("since"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			since = n
		}
	}
	if v := r.URL.Query().Get("until"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			until = n
		}
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	systemId, _ := strconv.ParseUint(r.URL.Query().Get("systemId"), 10, 64)
	talkgroupId, _ := strconv.ParseUint(r.URL.Query().Get("talkgroupId"), 10, 64)
	reasonFilter := strings.TrimSpace(r.URL.Query().Get("reason"))
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))

	where := `WHERE c."timestamp" >= $1 AND c."timestamp" <= $2
		AND COALESCE(c."transcript", '') <> ''
		AND (
			COALESCE(c."incidentGeocodeStatus", '') IN ('failed', 'skipped', '')
			OR (c."incidentLat" = 0 AND c."incidentLon" = 0
				AND COALESCE(c."incidentGeocodeStatus", '') NOT IN ('geocoded', 'local', 'cache', 'approximate', 'coverage', 'manual'))
		)`
	args := []any{since, until}
	argN := 3
	if systemId > 0 {
		where += fmt.Sprintf(` AND c."systemId" = $%d`, argN)
		args = append(args, systemId)
		argN++
	}
	if talkgroupId > 0 {
		where += fmt.Sprintf(` AND c."talkgroupId" = $%d`, argN)
		args = append(args, talkgroupId)
		argN++
	}
	if reasonFilter != "" {
		where += fmt.Sprintf(` AND c."incidentMappingReason" = $%d`, argN)
		args = append(args, reasonFilter)
		argN++
	}
	switch statusFilter {
	case "failed", "skipped":
		where += fmt.Sprintf(` AND COALESCE(c."incidentGeocodeStatus", '') = $%d`, argN)
		args = append(args, statusFilter)
		argN++
	case "empty", "unattempted":
		where += ` AND COALESCE(c."incidentGeocodeStatus", '') = ''`
	}

	countRows, err := api.Controller.Database.Sql.Query(fmt.Sprintf(`
		SELECT COALESCE(NULLIF(c."incidentMappingReason", ''), '(none)') AS reason, COUNT(*)::int
		FROM "calls" c %s
		GROUP BY 1 ORDER BY COUNT(*) DESC LIMIT 40`, where), args...)
	reasonCounts := []map[string]any{}
	if err == nil {
		defer countRows.Close()
		for countRows.Next() {
			var reason string
			var n int
			if countRows.Scan(&reason, &n) == nil {
				reasonCounts = append(reasonCounts, map[string]any{"reason": reason, "count": n})
			}
		}
	}

	listArgs := append([]any{}, args...)
	listArgs = append(listArgs, limit)
	query := fmt.Sprintf(`SELECT c."callId", c."systemId", c."talkgroupId", c."timestamp",
		COALESCE(c."incidentGeocodeStatus", ''), COALESCE(c."incidentMappingReason", ''),
		COALESCE(c."incidentGeocodeSource", ''), COALESCE(c."incidentAddress", ''),
		COALESCE(c."incidentNature", ''), LEFT(COALESCE(c."transcript", ''), 240),
		COALESCE(s."label", ''), COALESCE(t."label", '')
		FROM "calls" c
		LEFT JOIN "systems" s ON s."systemId" = c."systemId"
		LEFT JOIN "talkgroups" t ON t."talkgroupId" = c."talkgroupId" AND t."systemId" = c."systemId"
		%s ORDER BY c."timestamp" DESC LIMIT $%d`, where, argN)
	rows, err := api.Controller.Database.Sql.Query(query, listArgs...)
	if err != nil {
		api.exitWithErrorContext(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var callId, sysId, tgId uint64
		var ts int64
		var status, reason, source, addr, nature, transcript, sysLabel, tgLabel string
		if rows.Scan(&callId, &sysId, &tgId, &ts, &status, &reason, &source, &addr, &nature, &transcript, &sysLabel, &tgLabel) != nil {
			continue
		}
		items = append(items, map[string]any{
			"callId": callId, "systemId": sysId, "talkgroupId": tgId, "timestamp": ts,
			"status": status, "reason": reason, "source": source,
			"address": addr, "nature": nature, "transcript": transcript,
			"systemLabel": sysLabel, "talkgroupLabel": tgLabel,
		})
	}
	json.NewEncoder(w).Encode(map[string]any{
		"items":        items,
		"reasonCounts": reasonCounts,
		"since":        since,
		"until":        until,
	})
}

func (api *Api) IncidentsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	client := api.getClient(r)
	if client == nil || client.User == nil {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !api.Controller.clientHasFeature(client, "map") {
		api.exitWithError(w, http.StatusForbidden, "map not available for your group")
		return
	}
	since := int64(0)
	until := int64(0)
	if v := r.URL.Query().Get("since"); v != "" {
		since, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := r.URL.Query().Get("until"); v != "" {
		until, _ = strconv.ParseInt(v, 10, 64)
	}
	const maxIncidentLimit = 2000
	limit := 1500
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= maxIncidentLimit {
			limit = n
		}
	}
	// Busy statewide maps mint hundreds of pins per hour. Floor tiny legacy
	// caps (120–250) so a 24h window isn't truncated after a few hours.
	if since > 0 && limit < 800 {
		limit = 1500
	}
	nowMs := time.Now().UnixMilli()
	// The expiry predicate below makes rows older than a nature's window
	// permanently unmatchable, so a since-less request would walk the whole
	// timestamp index without ever filling LIMIT on large installs. Floor the
	// scan window when the caller omits "since" (the shipped map always sends
	// one).
	const maxIncidentScanWindowMs = int64(31) * 24 * 60 * 60 * 1000
	if since <= 0 {
		if until > 0 {
			since = until - maxIncidentScanWindowMs
		} else {
			since = nowMs - maxIncidentScanWindowMs
		}
	}
	where := `WHERE c."incidentLat" <> 0 AND c."incidentLon" <> 0
		AND COALESCE(c."incidentGeocodeStatus", '') NOT IN ('', 'failed', 'skipped')`
	api.Controller.Options.mutex.Lock()
	suppressUnknown := api.Controller.Options.MappingIntegration.SuppressUnknownNaturePins
	api.Controller.Options.mutex.Unlock()
	if suppressUnknown {
		where += ` AND COALESCE(c."incidentNature", '') <> '' AND UPPER(c."incidentNature") NOT IN ('UNKNOWN PROBLEM', 'UNKNOWN PROBLEM/UNCLASSIFIED', 'EMS UNKNOWN PROBLEM/UNCLASSIFIED')`
	}
	if since > 0 {
		where += fmt.Sprintf(` AND c."timestamp" >= %d`, since)
	}
	if until > 0 {
		where += fmt.Sprintf(` AND c."timestamp" <= %d`, until)
	}
	if v := r.URL.Query().Get("systemId"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > 0 {
			where += fmt.Sprintf(` AND c."systemId" = %d`, n)
		}
	}
	if v := r.URL.Query().Get("talkgroupId"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > 0 {
			where += fmt.Sprintf(` AND c."talkgroupId" = %d`, n)
		}
	}
	// Call-nature force expiry: an enabled category with expireMinutes > 0
	// removes its incidents from the map that many minutes after dispatch, no
	// matter what time range the viewer selected. Disabled categories are inert
	// here, matching MatchData. Blank natures render as UNKNOWN PROBLEM on the
	// map, so an expiry on that category covers them too (same equivalence as
	// SuppressUnknownNaturePins).
	where += fmt.Sprintf(` AND NOT EXISTS (
		SELECT 1 FROM "callNatures" n
		WHERE n."enabled" = true AND n."expireMinutes" > 0
			AND (UPPER(n."label") = UPPER(c."incidentNature")
				OR (c."incidentNature" = '' AND UPPER(n."label") = 'UNKNOWN PROBLEM'))
			AND c."timestamp" + n."expireMinutes"::bigint * 60000 <= %d
	)`, nowMs)
	query := fmt.Sprintf(`SELECT c."callId", c."systemId", c."talkgroupId", c."timestamp",
		c."incidentAddress", c."incidentCrossStreet1", c."incidentCrossStreet2",
		c."incidentNature", c."incidentCommonName", c."incidentLat", c."incidentLon",
		c."incidentGeocodeStatus", c."incidentGeocodeSource",
		LEFT(COALESCE(c."transcript", ''), 400),
		COALESCE(s."label", ''), COALESCE(t."label", ''), COALESCE(tg."label", ''), COALESCE(tg."color", '')
		FROM "calls" c
		LEFT JOIN "systems" s ON s."systemId" = c."systemId"
		LEFT JOIN "talkgroups" t ON t."talkgroupId" = c."talkgroupId" AND t."systemId" = c."systemId"
		LEFT JOIN "tags" tg ON tg."tagId" = t."tagId"
		%s ORDER BY c."timestamp" DESC LIMIT %d`, where, limit)
	rows, err := api.Controller.Database.Sql.Query(query)
    if err != nil {
		api.exitWithErrorContext(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var callId, systemId, talkgroupId uint64
		var ts int64
		var addr, cs1, cs2, nature, common, status, source, transcript, sysLabel, tgLabel, tagLabel, tagColor string
		var lat, lon float64
		if err := rows.Scan(&callId, &systemId, &talkgroupId, &ts, &addr, &cs1, &cs2, &nature, &common, &lat, &lon, &status, &source, &transcript, &sysLabel, &tgLabel, &tagLabel, &tagColor); err != nil {
			continue
		}
		system, sysOk := api.Controller.Systems.GetSystemById(systemId)
		if !sysOk {
			continue
		}
		talkgroup, tgOk := system.Talkgroups.GetTalkgroupById(talkgroupId)
		if !tgOk {
			continue
		}
		incidentCall := &Call{
			Id:        callId,
			System:    system,
			Talkgroup: talkgroup,
			Timestamp: time.UnixMilli(ts),
		}
		if !api.Controller.userHasAccess(client.User, incidentCall) || !api.transcriptReleasedForUser(client.User, incidentCall) {
			continue
		}
		row := map[string]any{
			"callId": callId, "systemId": systemId, "talkgroupId": talkgroupId,
			"timestamp": ts, "address": addr, "crossStreet1": cs1, "crossStreet2": cs2,
			"nature": nature, "commonName": common, "lat": lat, "lon": lon,
			"status": status, "source": source, "transcript": transcript,
			"systemLabel": sysLabel, "talkgroupLabel": tgLabel,
		}
		if tagLabel != "" {
			row["tagLabel"] = tagLabel
		}
		if tagColor != "" {
			row["tagColor"] = tagColor
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		api.exitWithErrorContext(w, r, http.StatusInternalServerError, err.Error())
		return
	}

	api.Controller.Options.mutex.Lock()
	mappingEnabled := api.Controller.Options.MappingIntegration.IncidentMappingEnabled
	suppressUnknownMeta := api.Controller.Options.MappingIntegration.SuppressUnknownNaturePins
	api.Controller.Options.mutex.Unlock()

	metaUntil := until
	if metaUntil <= 0 {
		metaUntil = nowMs
	}
	metaWhere := `WHERE c."timestamp" >= $1 AND c."timestamp" <= $2 AND COALESCE(c."transcript", '') <> ''`
	metaArgs := []any{since, metaUntil}
	metaN := 3
	if v := r.URL.Query().Get("systemId"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > 0 {
			metaWhere += fmt.Sprintf(` AND c."systemId" = $%d`, metaN)
			metaArgs = append(metaArgs, n)
			metaN++
		}
	}
	if v := r.URL.Query().Get("talkgroupId"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > 0 {
			metaWhere += fmt.Sprintf(` AND c."talkgroupId" = $%d`, metaN)
			metaArgs = append(metaArgs, n)
			metaN++
		}
	}
	var mappedCount, failedCount, skippedCount, unattemptedCount int
	_ = api.Controller.Database.Sql.QueryRow(fmt.Sprintf(`SELECT
		COUNT(*) FILTER (WHERE c."incidentLat" <> 0 AND c."incidentLon" <> 0
			AND COALESCE(c."incidentGeocodeStatus", '') NOT IN ('', 'failed', 'skipped'))::int,
		COUNT(*) FILTER (WHERE COALESCE(c."incidentGeocodeStatus", '') = 'failed')::int,
		COUNT(*) FILTER (WHERE COALESCE(c."incidentGeocodeStatus", '') = 'skipped')::int,
		COUNT(*) FILTER (WHERE COALESCE(c."incidentGeocodeStatus", '') = '')::int
		FROM "calls" c %s`, metaWhere), metaArgs...).Scan(
		&mappedCount, &failedCount, &skippedCount, &unattemptedCount)

	json.NewEncoder(w).Encode(map[string]any{
		"incidents": out,
		"meta": map[string]any{
			"mappingEnabled":            mappingEnabled,
			"suppressUnknownNaturePins": suppressUnknownMeta,
			"mapped":                    mappedCount,
			"failed":                    failedCount,
			"skipped":                   skippedCount,
			"unattempted":               unattemptedCount,
		},
	})
}

// MappingImportBoundariesHandler starts a background Census boundary download/import.
func (api *Api) MappingImportBoundariesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		StateFIPS       []string `json:"stateFips"`
		Layers          []string `json:"layers"`
		ReplaceExisting bool     `json:"replaceExisting"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "invalid json")
		return
	}
	var layers []mapping.BoundaryLayer
	for _, s := range body.Layers {
		l := mapping.BoundaryLayer(strings.ToLower(strings.TrimSpace(s)))
		if l.Valid() {
			layers = append(layers, l)
		}
	}
	opts := ImportBoundariesOptions{
		StateFIPS:       body.StateFIPS,
		Layers:          layers,
		ReplaceExisting: body.ReplaceExisting,
	}
	if err := globalBoundaryImport.start(NewMappingStore(api.Controller.Database), api.Controller, opts); err != nil {
		api.exitWithError(w, http.StatusConflict, err.Error())
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"status": "started"})
}

// MappingImportBoundariesStatusHandler reports boundary import progress.
func (api *Api) MappingImportBoundariesStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	json.NewEncoder(w).Encode(globalBoundaryImport.snapshot())
}

// MappingBoundariesStatsHandler returns stored boundary counts.
func (api *Api) MappingBoundariesStatsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	store := NewMappingStore(api.Controller.Database)
	json.NewEncoder(w).Encode(store.BoundaryStats())
}

// MappingDeleteBoundariesHandler removes all stored boundary overlays.
func (api *Api) MappingDeleteBoundariesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		api.exitWithError(w, http.StatusMethodNotAllowed, "DELETE only")
		return
	}
	store := NewMappingStore(api.Controller.Database)
	if err := store.DeleteAllBoundaries(); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// MapBoundariesHandler returns GeoJSON features for the incident map viewport.
func (api *Api) MapBoundariesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	client := api.getClient(r)
	if client == nil || client.User == nil {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	api.Controller.Options.mutex.Lock()
	cfg := api.Controller.Options.MappingIntegration
	api.Controller.Options.mutex.Unlock()
	if !cfg.MapBoundariesEnabled {
		json.NewEncoder(w).Encode(map[string]any{"enabled": false, "features": []any{}})
		return
	}
	west, _ := strconv.ParseFloat(r.URL.Query().Get("west"), 64)
	south, _ := strconv.ParseFloat(r.URL.Query().Get("south"), 64)
	east, _ := strconv.ParseFloat(r.URL.Query().Get("east"), 64)
	north, _ := strconv.ParseFloat(r.URL.Query().Get("north"), 64)
	if west == 0 && south == 0 && east == 0 && north == 0 {
		api.exitWithError(w, http.StatusBadRequest, "bbox required (west,south,east,north)")
		return
	}
	layers := cfg.MapBoundaryLayers
	if len(layers) == 0 {
		layers = []string{"county", "place", "cousub"}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("layers")); v != "" {
		layers = strings.Split(v, ",")
	}
	cacheKey := boundaryBBoxCacheKey(west, south, east, north, layers)
	if cached, ok := getBoundaryBBoxCached(cacheKey); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-TLR-Boundary-Cache", "HIT")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(cached)
		return
	}

	store := NewMappingStore(api.Controller.Database)
	features, err := store.QueryBoundariesBBox(west, south, east, north, layers, 0)
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if features == nil {
		features = []map[string]any{}
	}
	payload, err := json.Marshal(map[string]any{
		"enabled":  true,
		"layers":   layers,
		"colors":   mapping.BoundaryFillColors,
		"type":     "FeatureCollection",
		"features": features,
	})
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, err.Error())
		return
	}
	putBoundaryBBoxCache(cacheKey, payload)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-TLR-Boundary-Cache", "MISS")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}
