// Copyright (C) 2026 Thinline Dynamic Solutions

package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"rdio-scanner/server/mapping"
)

const (
	incidentReportsPerHour = 30
	incidentReportNoteMax  = 1000
)

var incidentCorrectionClient = &http.Client{Timeout: 15 * time.Second}

type incidentCorrectionPin struct {
	Address string   `json:"address"`
	Nature  string   `json:"nature"`
	Lat     *float64 `json:"lat"`
	Lon     *float64 `json:"lon"`
}

type incidentCorrectionBounds struct {
	Viewbox  string  `json:"viewbox"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	RadiusMi float64 `json:"radius_mi"`
	City     string  `json:"city"`
	County   string  `json:"county"`
	State    string  `json:"state"`
}

// incidentCorrectionReport is the nominatim-gateway POST /corrections body.
type incidentCorrectionReport struct {
	Kind          string                   `json:"kind"`
	Reporter      string                   `json:"reporter"`
	Note          string                   `json:"note"`
	CallID        uint64                   `json:"call_id"`
	CallTime      int64                    `json:"call_time"`
	System        string                   `json:"system"`
	Talkgroup     string                   `json:"talkgroup"`
	Transcript    string                   `json:"transcript"`
	GeocodeSource string                   `json:"geocode_source"`
	GeocodeQuery  string                   `json:"geocode_query"`
	Original      incidentCorrectionPin    `json:"original"`
	Corrected     incidentCorrectionPin    `json:"corrected"`
	Bounds        incidentCorrectionBounds `json:"bounds"`
	ServerVersion string                   `json:"server_version"`
}

type incidentReportLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

var incidentReports = &incidentReportLimiter{hits: map[string][]time.Time{}}

// allow records one report for who and reports whether it fits the hourly cap.
func (l *incidentReportLimiter) allow(who string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-time.Hour)
	kept := l.hits[who][:0]
	for _, t := range l.hits[who] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= incidentReportsPerHour {
		l.hits[who] = kept
		return false
	}
	l.hits[who] = append(kept, now)
	return true
}

// loadIncidentOriginal reads the pin currently stored for a call.
func (api *Api) loadIncidentOriginal(callId uint64) (pin incidentCorrectionPin, source, query string) {
	var addr, nature, src, q sql.NullString
	var lat, lon sql.NullFloat64
	err := api.Controller.Database.Sql.QueryRow(
		`SELECT "incidentAddress", "incidentNature", "incidentLat", "incidentLon", "incidentGeocodeSource", "incidentGeocodeQuery"
		FROM "calls" WHERE "callId" = $1`, callId,
	).Scan(&addr, &nature, &lat, &lon, &src, &q)
	if err != nil {
		return pin, "", ""
	}
	pin.Address = strings.TrimSpace(addr.String)
	pin.Nature = strings.TrimSpace(nature.String)
	if lat.Valid && lon.Valid && (lat.Float64 != 0 || lon.Float64 != 0) {
		la, lo := lat.Float64, lon.Float64
		pin.Lat, pin.Lon = &la, &lo
	}
	return pin, strings.TrimSpace(src.String), strings.TrimSpace(q.String)
}

// buildIncidentCorrection fills everything the reviewer needs: the stored pin,
// transcript, channel, and the coverage area geocoding searched in.
func (api *Api) buildIncidentCorrection(call *Call, original incidentCorrectionPin, source, query string) (incidentCorrectionReport, *mapping.GeoOptions) {
	geo := api.geoOptionsForPinSearch(call)
	report := incidentCorrectionReport{
		CallID:        call.Id,
		CallTime:      call.Timestamp.UnixMilli(),
		Transcript:    strings.TrimSpace(call.ReviewedTranscript),
		GeocodeSource: source,
		GeocodeQuery:  query,
		Original:      original,
		ServerVersion: Version,
	}
	if report.Transcript == "" {
		report.Transcript = strings.TrimSpace(call.Transcript)
	}
	if call.System != nil {
		report.System = call.System.Label
	}
	if call.Talkgroup != nil {
		report.Talkgroup = call.Talkgroup.Label
		if name := strings.TrimSpace(call.Talkgroup.Name); name != "" && name != call.Talkgroup.Label {
			report.Talkgroup += " — " + name
		}
	}
	report.Bounds = incidentCorrectionBounds{
		Viewbox:  mapping.GeocodeViewbox(geo),
		Lat:      geo.BoundsLat,
		Lon:      geo.BoundsLon,
		RadiusMi: geo.BoundsRadiusMi,
		City:     strings.TrimSpace(geo.CityHint),
	}
	if ctx := strings.TrimSpace(geo.LocationContext); ctx != "" {
		report.Bounds.County = strings.TrimSpace(strings.Split(ctx, ",")[0])
	}
	if st := strings.TrimSpace(geo.State); st != "" {
		report.Bounds.State = mapping.StateAbbrev(st)
		if report.Bounds.State == "" {
			report.Bounds.State = st
		}
	}
	return report, geo
}

func postIncidentCorrection(gatewayURL, apiKey string, report incidentCorrectionReport) error {
	payload, err := json.Marshal(report)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(gatewayURL, "/")+"/corrections", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := incidentCorrectionClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("gateway status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// reportAdminPinChange forwards a system admin's Correct/Remove Pin to the
// gateway as a confirmed-by-admin correction. Best effort, off the request path.
func (api *Api) reportAdminPinChange(call *Call, original incidentCorrectionPin, source, query string, corrected incidentCorrectionPin, removed bool) {
	kind := "not_incident"
	if !removed {
		moved := corrected.Address != original.Address ||
			(corrected.Lat != nil && (original.Lat == nil || !sameCoord(*corrected.Lat, *original.Lat) || !sameCoord(*corrected.Lon, *original.Lon)))
		renatured := corrected.Nature != original.Nature
		switch {
		case moved && renatured:
			kind = "both"
		case moved:
			kind = "location"
		case renatured:
			kind = "nature"
		default:
			return
		}
	}
	report, geo := api.buildIncidentCorrection(call, original, source, query)
	if geo.NominatimDirectURL == "" || geo.NominatimAPIKey == "" {
		return
	}
	report.Kind = kind
	report.Reporter = "system_admin"
	report.Corrected = corrected
	go func() {
		if err := postIncidentCorrection(geo.NominatimDirectURL, geo.NominatimAPIKey, report); err != nil {
			api.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("incident correction for call %d not sent to geocode server: %v", call.Id, err))
		}
	}()
}

func sameCoord(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}

// IncidentReportHandler handles POST /api/incidents/report/{callId} (any
// signed-in listener suggesting a pin or nature fix) and
// GET /api/incidents/report/{callId}/search (address lookup for the form).
func (api *Api) IncidentReportHandler(w http.ResponseWriter, r *http.Request) {
	client := api.getClient(r)
	if client == nil || (client.User == nil && !client.IsAdmin) {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !api.Controller.clientHasFeature(client, "map") {
		api.exitWithError(w, http.StatusForbidden, "map not available for your group")
		return
	}
	idStr := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/incidents/report/"), "/")
	searching := false
	if strings.HasSuffix(idStr, "/search") {
		searching = true
		idStr = strings.Trim(strings.TrimSuffix(idStr, "/search"), "/")
	}
	callId, err := strconv.ParseUint(strings.TrimSpace(idStr), 10, 64)
	if err != nil || callId == 0 {
		api.exitWithError(w, http.StatusBadRequest, "invalid call id")
		return
	}
	call, err := api.Controller.Calls.GetCall(callId)
	if err != nil || call == nil || !api.clientMayHaveCall(client, call) {
		api.exitWithError(w, http.StatusNotFound, "call not found")
		return
	}
	if searching {
		api.searchIncidentPinAddress(w, r, call)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Kind    string   `json:"kind"`
		Address string   `json:"address"`
		Nature  string   `json:"nature"`
		Lat     *float64 `json:"lat"`
		Lon     *float64 `json:"lon"`
		Note    string   `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body.Kind = strings.ToLower(strings.TrimSpace(body.Kind))
	body.Address = strings.TrimSpace(body.Address)
	body.Nature = strings.ToUpper(strings.TrimSpace(body.Nature))
	body.Note = strings.TrimSpace(body.Note)
	if len(body.Note) > incidentReportNoteMax {
		body.Note = body.Note[:incidentReportNoteMax]
	}
	if (body.Lat == nil) != (body.Lon == nil) {
		api.exitWithError(w, http.StatusBadRequest, "lat and lon must be provided together")
		return
	}
	if body.Lat != nil && (*body.Lat < -90 || *body.Lat > 90 || *body.Lon < -180 || *body.Lon > 180 || (*body.Lat == 0 && *body.Lon == 0)) {
		api.exitWithError(w, http.StatusBadRequest, "coordinates out of range")
		return
	}
	switch body.Kind {
	case "location", "both":
		if body.Address == "" && body.Lat == nil {
			api.exitWithError(w, http.StatusBadRequest, "enter the correct address or move the pin")
			return
		}
		if body.Kind == "both" && body.Nature == "" {
			api.exitWithError(w, http.StatusBadRequest, "enter the correct incident type")
			return
		}
	case "nature":
		if body.Nature == "" {
			api.exitWithError(w, http.StatusBadRequest, "enter the correct incident type")
			return
		}
	case "not_incident":
	default:
		api.exitWithError(w, http.StatusBadRequest, "unknown correction kind")
		return
	}

	who := "admin"
	reporter := "system_admin"
	if client.User != nil {
		who = strconv.FormatUint(client.User.Id, 10)
		if !client.User.SystemAdmin {
			reporter = "user"
		}
	}
	if !incidentReports.allow(who, time.Now()) {
		api.exitWithError(w, http.StatusTooManyRequests, "too many corrections sent this hour; try again later")
		return
	}

	original, source, query := api.loadIncidentOriginal(callId)
	report, geo := api.buildIncidentCorrection(call, original, source, query)
	if geo.NominatimDirectURL == "" || geo.NominatimAPIKey == "" {
		api.exitWithError(w, http.StatusServiceUnavailable, "corrections need the Thinline geocode service, which this server is not using")
		return
	}
	report.Kind = body.Kind
	report.Reporter = reporter
	report.Note = body.Note
	report.Corrected = incidentCorrectionPin{Address: body.Address, Nature: body.Nature, Lat: body.Lat, Lon: body.Lon}

	if err := postIncidentCorrection(geo.NominatimDirectURL, geo.NominatimAPIKey, report); err != nil {
		api.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("incident correction for call %d failed: %v", callId, err))
		api.exitWithError(w, http.StatusBadGateway, "the geocode server did not accept the correction")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "sent"})
}
