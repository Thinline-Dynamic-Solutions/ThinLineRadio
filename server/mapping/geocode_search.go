// Copyright (C) 2025 Thinline Dynamic Solutions
//
// geocode_search.go — TLR client for nominatim-gateway GET /search.
// Used for place/city/township lookups (tone-set jurisdiction centers),
// not for dispatch street pins (those use POST /transcript).

package mapping

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var searchGeocodeClient = &http.Client{Timeout: 15 * time.Second}

// SearchGeocodeHit is one pin from gateway GET /search.
type SearchGeocodeHit struct {
	Lat         float64
	Lon         float64
	DisplayName string
	Class       string
	Type        string
	OK          bool
	Detail      string
}

type nominatimSearchResult struct {
	Lat         json.Number `json:"lat"`
	Lon         json.Number `json:"lon"`
	DisplayName string      `json:"display_name"`
	Class       string      `json:"class"`
	Type        string      `json:"type"`
}

// GeocodeNominatimSearch asks the gateway GET /search for a short place query.
func GeocodeNominatimSearch(gatewayURL, apiKey, query string, geo *GeoOptions) SearchGeocodeHit {
	gatewayURL = strings.TrimRight(strings.TrimSpace(gatewayURL), "/")
	apiKey = strings.TrimSpace(apiKey)
	query = strings.TrimSpace(query)
	if gatewayURL == "" || apiKey == "" || query == "" {
		return SearchGeocodeHit{Detail: "not_configured"}
	}
	if geo != nil && geo.SkipExternalGeocode {
		return SearchGeocodeHit{Detail: "skip_external"}
	}

	u, err := url.Parse(gatewayURL + "/search")
	if err != nil {
		return SearchGeocodeHit{Detail: "bad_url"}
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("limit", "5")
	if vb := geocodeViewboxParam(geo); vb != "" {
		q.Set("viewbox", vb)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return SearchGeocodeHit{Detail: "request_error"}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := searchGeocodeClient.Do(req)
	if err != nil {
		return SearchGeocodeHit{Detail: "transport_error"}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusPaymentRequired {
		return SearchGeocodeHit{Detail: "no_subscription"}
	}
	if resp.StatusCode != http.StatusOK {
		return SearchGeocodeHit{Detail: fmt.Sprintf("gateway_status_%d", resp.StatusCode)}
	}

	var results []nominatimSearchResult
	if err := json.Unmarshal(body, &results); err != nil {
		return SearchGeocodeHit{Detail: "decode_error"}
	}
	for _, r := range results {
		lat, lon := parseNominatimCoord(r.Lat), parseNominatimCoord(r.Lon)
		if lat == 0 && lon == 0 {
			continue
		}
		if PinOutsideCoverage(lat, lon, geo) {
			continue
		}
		return SearchGeocodeHit{
			Lat:         lat,
			Lon:         lon,
			DisplayName: r.DisplayName,
			Class:       r.Class,
			Type:        r.Type,
			OK:          true,
		}
	}
	if len(results) == 0 {
		return SearchGeocodeHit{Detail: "no_geocode_hit"}
	}
	return SearchGeocodeHit{Detail: "outside_coverage"}
}

// GeocodeNominatimSearchAll returns up to limit matches for an admin address
// lookup. Coverage viewbox is tried first. An empty viewbox search follows
// only when that box had no hit, so a full address outside the channel can
// still be chosen.
func GeocodeNominatimSearchAll(gatewayURL, apiKey, query string, geo *GeoOptions, limit int) ([]SearchGeocodeHit, string) {
	if limit <= 0 || limit > 8 {
		limit = 8
	}
	hits, detail := fetchNominatimSearch(gatewayURL, apiKey, query, geo, limit)
	if detail != "" {
		return nil, detail
	}
	if len(hits) == 0 && geocodeViewboxParam(geo) != "" {
		hits, detail = fetchNominatimSearch(gatewayURL, apiKey, query, nil, limit)
		if detail != "" {
			return nil, detail
		}
	}
	return hits, ""
}

func fetchNominatimSearch(gatewayURL, apiKey, query string, geo *GeoOptions, limit int) ([]SearchGeocodeHit, string) {
	gatewayURL = strings.TrimRight(strings.TrimSpace(gatewayURL), "/")
	apiKey = strings.TrimSpace(apiKey)
	query = strings.TrimSpace(query)
	if gatewayURL == "" || apiKey == "" || query == "" {
		return nil, "not_configured"
	}
	if geo != nil && geo.SkipExternalGeocode {
		return nil, "skip_external"
	}
	u, err := url.Parse(gatewayURL + "/search")
	if err != nil {
		return nil, "bad_url"
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("limit", strconv.Itoa(limit))
	if vb := geocodeViewboxParam(geo); vb != "" {
		q.Set("viewbox", vb)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "request_error"
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := searchGeocodeClient.Do(req)
	if err != nil {
		return nil, "transport_error"
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusPaymentRequired {
		return nil, "no_subscription"
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Sprintf("gateway_status_%d", resp.StatusCode)
	}
	var results []nominatimSearchResult
	if err := json.Unmarshal(body, &results); err != nil {
		return nil, "decode_error"
	}
	hits := make([]SearchGeocodeHit, 0, len(results))
	for _, r := range results {
		lat, lon := parseNominatimCoord(r.Lat), parseNominatimCoord(r.Lon)
		if lat == 0 && lon == 0 {
			continue
		}
		label := shortGeocodeLabel(r.DisplayName)
		if label == "" {
			label = fmt.Sprintf("%.5f, %.5f", lat, lon)
		}
		hits = append(hits, SearchGeocodeHit{
			Lat:         lat,
			Lon:         lon,
			DisplayName: label,
			Class:       r.Class,
			Type:        r.Type,
			OK:          true,
		})
	}
	return dedupeSearchHits(hits, limit), ""
}

func dedupeSearchHits(hits []SearchGeocodeHit, limit int) []SearchGeocodeHit {
	out := make([]SearchGeocodeHit, 0, len(hits))
	for _, h := range hits {
		dup := false
		for _, prev := range out {
			if math.Abs(prev.Lat-h.Lat) < 0.0004 && math.Abs(prev.Lon-h.Lon) < 0.0004 {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		out = append(out, h)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

var geocodeHouseNumberRE = regexp.MustCompile(`^\d+[A-Za-z]?$`)

// shortGeocodeLabel keeps the house, street, and place from a Nominatim
// display name ("954, Collins Street, Austintown Township, Mahoning County, Ohio").
func shortGeocodeLabel(display string) string {
	raw := strings.Split(display, ",")
	parts := make([]string, 0, len(raw))
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return strings.TrimSpace(display)
	}
	if len(parts) >= 2 && geocodeHouseNumberRE.MatchString(parts[0]) {
		parts = append([]string{parts[0] + " " + parts[1]}, parts[2:]...)
	}
	if len(parts) > 3 {
		parts = parts[:3]
	}
	return strings.Join(parts, ", ")
}

func parseNominatimCoord(n json.Number) float64 {
	if n == "" {
		return 0
	}
	f, err := n.Float64()
	if err == nil {
		return f
	}
	f, err = strconv.ParseFloat(string(n), 64)
	if err != nil {
		return 0
	}
	return f
}
