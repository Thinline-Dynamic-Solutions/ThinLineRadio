// Copyright (C) 2026 Thinline Dynamic Solutions

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"rdio-scanner/server/mapping"
)

// canManageIncidentPins reports whether the request comes from the admin
// console (admin token) or a system-admin user session.
func (api *Api) canManageIncidentPins(client *Client) bool {
	if api.isAdmin(client) {
		return true
	}
	return client != nil && client.User != nil && client.User.SystemAdmin
}

// IncidentPinHandler handles PUT/DELETE /api/incidents/pin/{callId} — system
// admins correcting or removing incident pins on the live map.
func (api *Api) IncidentPinHandler(w http.ResponseWriter, r *http.Request) {
	client := api.getClient(r)
	if client == nil || (client.User == nil && !client.IsAdmin) {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !api.canManageIncidentPins(client) {
		api.exitWithError(w, http.StatusForbidden, "system admin access required")
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/api/incidents/pin/")
	callId, err := strconv.ParseUint(strings.TrimSpace(idStr), 10, 64)
	if err != nil || callId == 0 {
		api.exitWithError(w, http.StatusBadRequest, "invalid call id")
		return
	}
	call, err := api.Controller.Calls.GetCall(callId)
	if err != nil || call == nil {
		api.exitWithError(w, http.StatusNotFound, "call not found")
		return
	}
	store := NewMappingStore(api.Controller.Database)

	switch r.Method {
	case http.MethodDelete:
		if err := store.ClearCallIncident(callId); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("clear failed: %v", err))
			return
		}
		api.Controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("incident pin for call %d removed by %s", callId, pinAdminActor(client)))
		api.Controller.IncidentMappingQueue.broadcastIncidentUpdate(call, nil, "skipped")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "removed"})

	case http.MethodPut:
		var body struct {
			Address *string  `json:"address"`
			Nature  *string  `json:"nature"`
			Lat     *float64 `json:"lat"`
			Lon     *float64 `json:"lon"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			api.exitWithError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if body.Address == nil && body.Nature == nil && body.Lat == nil && body.Lon == nil {
			api.exitWithError(w, http.StatusBadRequest, "nothing to update")
			return
		}
		if (body.Lat == nil) != (body.Lon == nil) {
			api.exitWithError(w, http.StatusBadRequest, "lat and lon must be provided together")
			return
		}
		if body.Lat != nil {
			if *body.Lat < -90 || *body.Lat > 90 || *body.Lon < -180 || *body.Lon > 180 {
				api.exitWithError(w, http.StatusBadRequest, "coordinates out of range")
				return
			}
			if *body.Lat == 0 && *body.Lon == 0 {
				api.exitWithError(w, http.StatusBadRequest, "coordinates cannot both be zero")
				return
			}
		}
		if body.Address != nil {
			trimmed := strings.TrimSpace(*body.Address)
			body.Address = &trimmed
		}
		if body.Nature != nil {
			upper := strings.ToUpper(strings.TrimSpace(*body.Nature))
			body.Nature = &upper
		}
		if err := store.CorrectCallIncident(callId, body.Address, body.Nature, body.Lat, body.Lon); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("update failed: %v", err))
			return
		}
		api.Controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("incident pin for call %d corrected by %s", callId, pinAdminActor(client)))

		// Broadcast the persisted values so open maps and alert cards refresh.
		var (
			addr, nature string
			lat, lon     float64
		)
		err := api.Controller.Database.Sql.QueryRow(
			`SELECT "incidentAddress", "incidentNature", "incidentLat", "incidentLon" FROM "calls" WHERE "callId" = $1`,
			callId,
		).Scan(&addr, &nature, &lat, &lon)
		if err == nil {
			primary := &mapping.CuratedAlert{
				Address:    addr,
				NatureDesc: nature,
				Lat:        fmt.Sprintf("%.6f", lat),
				Lon:        fmt.Sprintf("%.6f", lon),
			}
			api.Controller.IncidentMappingQueue.broadcastIncidentUpdate(call, primary, "manual")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "corrected",
			"address": addr,
			"nature":  nature,
			"lat":     lat,
			"lon":     lon,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func pinAdminActor(client *Client) string {
	if client != nil && client.User != nil {
		return fmt.Sprintf("system admin %s", client.User.Email)
	}
	return "admin console"
}
