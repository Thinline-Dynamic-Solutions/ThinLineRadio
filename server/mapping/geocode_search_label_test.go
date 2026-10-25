// Copyright (C) 2026 Thinline Dynamic Solutions

package mapping

import "testing"

func TestShortGeocodeLabelKeepsHouseStreetPlace(t *testing.T) {
	got := shortGeocodeLabel("954, Collins Street, Austintown Township, Mahoning County, Ohio, 44515, United States")
	want := "954 Collins Street, Austintown Township, Mahoning County"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDedupeSearchHitsDropsSameSpot(t *testing.T) {
	hits := []SearchGeocodeHit{
		{Lat: 41.10001, Lon: -80.70001, DisplayName: "A", OK: true},
		{Lat: 41.10002, Lon: -80.70002, DisplayName: "A again", OK: true},
		{Lat: 41.20000, Lon: -80.80000, DisplayName: "B", OK: true},
	}
	out := dedupeSearchHits(hits, 8)
	if len(out) != 2 || out[0].DisplayName != "A" || out[1].DisplayName != "B" {
		t.Fatalf("got %#v", out)
	}
}
