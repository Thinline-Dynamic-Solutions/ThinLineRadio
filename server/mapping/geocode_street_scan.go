// Copyright (C) 2025 Thinline Dynamic Solutions
//
// geocode_street_scan.go — client for the nominatim-gateway's /streets/scan
// endpoint (see nominatim-gateway/streetscan.go). Used by research tools
// (e.g. streetscan_prototype). Live incident mapping geocodes via a single
// POST /transcript; it does not narrow multi-query /search fan-out here.

package mapping

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

var streetScanClient = &http.Client{Timeout: 30 * time.Second}

// StreetScanHit mirrors the gateway's StreetHit response shape.
type StreetScanHit struct {
	MatchedText string `json:"matched_text"`
	StreetName  string `json:"street_name"`
	Exact       bool   `json:"exact"`
	Fuzzy       bool   `json:"fuzzy"`
}

type streetScanRequestBody struct {
	Transcript string `json:"transcript"`
	Viewbox    string `json:"viewbox"`
}

type streetScanResponseBody struct {
	Hits []StreetScanHit `json:"hits"`
}

// ScanTranscriptForStreets asks the nominatim-gateway which real, in-area
// street names appear anywhere in transcript. Returns nil (never an error)
// whenever the gateway isn't configured, there is no viewbox to scope the
// search to, or the request itself fails for any reason.
//
// A viewbox is a hard requirement — see the handler-side comment in
// nominatim-gateway/server.go: scanning with no geographic bound would
// confirm every same-named street in the country.
func ScanTranscriptForStreets(gatewayURL, apiKey, transcript string, geo *GeoOptions) []StreetScanHit {
	gatewayURL = strings.TrimRight(strings.TrimSpace(gatewayURL), "/")
	apiKey = strings.TrimSpace(apiKey)
	transcript = strings.TrimSpace(transcript)
	if gatewayURL == "" || apiKey == "" || transcript == "" {
		return nil
	}
	vb := geocodeViewboxParam(geo)
	if vb == "" {
		return nil
	}
	payload, err := json.Marshal(streetScanRequestBody{Transcript: transcript, Viewbox: vb})
	if err != nil {
		return nil
	}
	req, err := http.NewRequest(http.MethodPost, gatewayURL+"/streets/scan", bytes.NewReader(payload))
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := streetScanClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var out streetScanResponseBody
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil
	}
	if out.Hits == nil {
		out.Hits = []StreetScanHit{}
	}
	return out.Hits
}
