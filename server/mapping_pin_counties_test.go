package main

import "testing"

func TestCountyShapeContains(t *testing.T) {
	// A 2x2 square with a 1x1 hole in the middle, plus a separate island.
	s := &countyShape{minLat: 0, minLon: 0, maxLat: 2, maxLon: 6}
	s.polygons = parseCountyGeometry(`{"type":"MultiPolygon","coordinates":[
		[[[0,0],[2,0],[2,2],[0,2],[0,0]],[[0.5,0.5],[1.5,0.5],[1.5,1.5],[0.5,1.5],[0.5,0.5]]],
		[[[5,0],[6,0],[6,1],[5,1],[5,0]]]]}`)
	cases := []struct {
		lat, lon float64
		want     bool
	}{
		{0.25, 0.25, true},
		{1, 1, false}, // in the hole
		{0.5, 5.5, true},
		{1.5, 5.5, false},
		{3, 1, false},
	}
	for _, c := range cases {
		if got := s.contains(c.lat, c.lon); got != c.want {
			t.Errorf("contains(%v, %v) = %v, want %v", c.lat, c.lon, got, c.want)
		}
	}
}

func TestNormalizePinCounties(t *testing.T) {
	got := normalizePinCounties([]any{" 39155 ", "39155", "", 7, "39099"})
	if len(got) != 2 || got[0] != "39155" || got[1] != "39099" {
		t.Fatalf("normalizePinCounties = %v", got)
	}
}
