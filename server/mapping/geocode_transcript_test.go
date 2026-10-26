package mapping

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGeocodeRelayNominatimFromTranscriptSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transcript" && r.URL.Path != "/geocode/transcript" {
			t.Fatalf("path %s", r.URL.Path)
		}
		var req transcriptGeocodeRequestBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if req.Transcript == "" || req.Viewbox == "" {
			t.Fatalf("missing fields: %+v", req)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(transcriptGeocodeResponseBody{
			OK: true, Lat: 41.48, Lon: -80.72,
			DisplayName: "728, Maro Circle, Warren, Trumbull County, Ohio",
			Query:       "728 MARO CIRCLE",
			House:       "728",
			StreetName:  "MARO CIRCLE",
		})
	}))
	defer srv.Close()

	hit := GeocodeRelayNominatimFromTranscript(srv.URL, "key",
		"STATION 7 LIFT ASSIST 728 MARO CIRCLE", testGeoWithBounds())
	if !hit.OK || hit.House != "728" || hit.Lat == 0 {
		t.Fatalf("unexpected hit: %+v", hit)
	}
}

func TestGeocodeRelayNominatimFromTranscriptRequiresViewbox(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()
	hit := GeocodeRelayNominatimFromTranscript(srv.URL, "key", "728 MARO CIRCLE", nil)
	if hit.OK || hit.Detail != "viewbox_required" || called {
		t.Fatalf("got %+v called=%v", hit, called)
	}
}

func TestApplyTranscriptGeocodeHitDropsEngineNumberOnIntersection(t *testing.T) {
	c := &CuratedAlert{Address: "6 UNION STREET AND PARKMAN ROAD"}
	ApplyTranscriptGeocodeHit(c, TranscriptGeocodeHit{
		OK: true, Lat: 41.231969, Lon: -80.833072,
		StreetName: "UNION STREET and PARKMAN ROAD",
		Detail:     "intersection",
	})
	if c.Address != "UNION STREET AND PARKMAN ROAD" {
		t.Fatalf("engine number stayed on the intersection label: %q", c.Address)
	}
	if c.Lat == "" || c.Lon == "" {
		t.Fatalf("pin missing: %+v", c)
	}
}

func TestApplyTranscriptGeocodeHitFillsAddress(t *testing.T) {
	c := &CuratedAlert{}
	ApplyTranscriptGeocodeHit(c, TranscriptGeocodeHit{
		OK: true, Lat: 41.2, Lon: -80.7, House: "728", StreetName: "MARO CIRCLE",
	})
	if c.Lat == "" || c.Lon == "" || c.Address == "" {
		t.Fatalf("got %+v", c)
	}
}
