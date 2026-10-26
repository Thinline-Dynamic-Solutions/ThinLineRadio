// Copyright (C) 2025 Thinline Dynamic Solutions
//
// geocode_transcript.go — TLR client for nominatim-gateway POST /transcript.
// After TranscriptLikelyHasLocation, TLR sends the FULL cleaned dispatch +
// coverage viewbox; the gateway finds in-area streets, pairs house numbers,
// and returns a Nominatim pin.

package mapping

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var transcriptGeocodeClient = &http.Client{Timeout: 20 * time.Second}

// TranscriptGeocodeHit is a successful pin from gateway transcript geocode.
type TranscriptGeocodeHit struct {
	Lat         float64
	Lon         float64
	DisplayName string
	Query       string
	House       string
	StreetName  string
	OK          bool
	Detail      string
}

type transcriptGeocodeRequestBody struct {
	Transcript string `json:"transcript"`
	Viewbox    string `json:"viewbox"`
	City       string `json:"city,omitempty"`
	County     string `json:"county,omitempty"`
	State      string `json:"state,omitempty"`
}

type transcriptGeocodeResponseBody struct {
	OK          bool    `json:"ok"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	DisplayName string  `json:"display_name"`
	Query       string  `json:"query"`
	House       string  `json:"house"`
	StreetName  string  `json:"street_name"`
	Detail      string  `json:"detail"`
}

// GeocodeRelayNominatimFromTranscript asks the gateway to geocode from the
// full dispatch transcript. Returns OK=false (never panics) when the gateway
// isn't configured, viewbox is missing, or no pin is found.
func GeocodeRelayNominatimFromTranscript(gatewayURL, apiKey, transcript string, geo *GeoOptions) TranscriptGeocodeHit {
	gatewayURL = strings.TrimRight(strings.TrimSpace(gatewayURL), "/")
	apiKey = strings.TrimSpace(apiKey)
	transcript = strings.TrimSpace(transcript)
	if gatewayURL == "" || apiKey == "" || transcript == "" {
		return TranscriptGeocodeHit{Detail: "not_configured"}
	}
	if geo != nil && geo.SkipExternalGeocode {
		return TranscriptGeocodeHit{Detail: "skip_external"}
	}
	vb := geocodeViewboxParam(geo)
	if vb == "" {
		return TranscriptGeocodeHit{Detail: "viewbox_required"}
	}
	reqBody := transcriptGeocodeRequestBody{Transcript: transcript, Viewbox: vb}
	if geo != nil {
		reqBody.City = strings.TrimSpace(geo.CityHint)
		if ctx := strings.TrimSpace(geo.LocationContext); ctx != "" {
			// "Trumbull County, Ohio" → county
			parts := strings.Split(ctx, ",")
			if len(parts) > 0 {
				reqBody.County = strings.TrimSpace(parts[0])
			}
		}
		if st := strings.TrimSpace(geo.State); st != "" {
			reqBody.State = StateAbbrev(st)
			if reqBody.State == "" {
				reqBody.State = st
			}
		}
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return TranscriptGeocodeHit{Detail: "encode_error"}
	}

	// Canonical path is /transcript. /geocode/transcript is kept as a fallback
	// alias for older gateway builds during rolling deploys.
	var out transcriptGeocodeResponseBody
	var lastDetail string
	gotHTTP := false
	for _, path := range []string{"/transcript", "/geocode/transcript"} {
		status, detail, parsed := postTranscriptGeocode(gatewayURL+path, apiKey, payload)
		if status == http.StatusOK {
			out = parsed
			gotHTTP = true
			lastDetail = ""
			break
		}
		lastDetail = detail
		// Only fall through to the alias when the canonical path is missing.
		if status != http.StatusNotFound {
			break
		}
	}
	if !gotHTTP {
		return TranscriptGeocodeHit{Detail: lastDetail}
	}
	if !out.OK || out.Lat == 0 || out.Lon == 0 {
		detail := strings.TrimSpace(out.Detail)
		if detail == "" {
			detail = "no_geocode_hit"
		}
		return TranscriptGeocodeHit{Detail: detail, Query: out.Query}
	}
	if PinOutsideCoverage(out.Lat, out.Lon, geo) {
		return TranscriptGeocodeHit{Detail: "outside_coverage", Query: out.Query}
	}
	return TranscriptGeocodeHit{
		Lat:         out.Lat,
		Lon:         out.Lon,
		DisplayName: out.DisplayName,
		Query:       out.Query,
		House:       out.House,
		StreetName:  out.StreetName,
		Detail:      out.Detail,
		OK:          true,
	}
}

// postTranscriptGeocode POSTs once, then retries once on transient 502/503
// (common for a second or two while nominatim-gateway restarts under the tunnel).
func postTranscriptGeocode(url, apiKey string, payload []byte) (status int, detail string, out transcriptGeocodeResponseBody) {
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return 0, "request_error", out
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := transcriptGeocodeClient.Do(req)
		if err != nil {
			if attempt == 0 {
				time.Sleep(400 * time.Millisecond)
				continue
			}
			return 0, "transport_error", out
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		status = resp.StatusCode
		if status == http.StatusPaymentRequired {
			return status, "no_subscription", out
		}
		if status == http.StatusBadGateway || status == http.StatusServiceUnavailable {
			if attempt == 0 {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return status, fmt.Sprintf("gateway_status_%d", status), out
		}
		if status != http.StatusOK {
			return status, fmt.Sprintf("gateway_status_%d", status), out
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return status, "decode_error", out
		}
		return status, "", out
	}
	return 0, "transport_error", out
}

func transcriptGeocodeHitIsIntersection(hit TranscriptGeocodeHit) bool {
	if strings.TrimSpace(hit.House) != "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(hit.Detail), "intersection") {
		return true
	}
	return strings.Contains(strings.ToLower(hit.StreetName), " and ")
}

// ApplyTranscriptGeocodeHit writes gateway transcript-geocode coordinates onto
// the curated alert and fills a missing card address from the hit.
func ApplyTranscriptGeocodeHit(curated *CuratedAlert, hit TranscriptGeocodeHit) {
	if curated == nil || !hit.OK {
		return
	}
	curated.Lat = fmt.Sprintf("%.6f", hit.Lat)
	curated.Lon = fmt.Sprintf("%.6f", hit.Lon)
	// "ENGINE 6 … AREA OF UNION STREET AND PARKMAN ROAD" is an intersection.
	// The 6 is the engine, and the local extract must not stay on the card.
	if transcriptGeocodeHitIsIntersection(hit) {
		curated.Address = strings.ToUpper(strings.TrimSpace(hit.StreetName))
		return
	}
	if strings.TrimSpace(curated.Address) == "" {
		addr := strings.TrimSpace(strings.TrimSpace(hit.House) + " " + strings.TrimSpace(hit.StreetName))
		if addr == "" && hit.DisplayName != "" {
			addr = StreetNameFromGeocodedFormatted(hit.DisplayName)
			if hit.House != "" {
				addr = strings.TrimSpace(hit.House + " " + addr)
			}
		}
		if addr != "" {
			curated.Address = addr
		}
	} else if hit.StreetName != "" {
		ApplyGeocodedStreetToAddress(curated, hit.StreetName)
		if hit.House != "" {
			ApplyGeocodedHouseToAddress(curated, hit.House)
		}
	}
}
