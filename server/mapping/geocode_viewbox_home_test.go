package mapping

import (
	"strings"
	"testing"
)

func TestGeocodeViewboxUsesBoundsNotHomeMax(t *testing.T) {
	geo := &GeoOptions{
		BoundsLat: 41.183, BoundsLon: -80.765, BoundsRadiusMi: 5,
		HomeLat: 41.183, HomeLon: -80.765, HomeMaxRadiusMi: 30,
	}
	vb := geocodeViewboxParam(geo)
	// 5+2=7mi box, not 30mi. 30mi west edge is past -81.3.
	if strings.HasPrefix(vb, "-81.3") || strings.HasPrefix(vb, "-81.2") || strings.HasPrefix(vb, "-81.1") {
		t.Fatalf("viewbox used HomeMax (too wide): %s", vb)
	}
	// Tight Niles box should start near -80.90
	if !strings.HasPrefix(vb, "-80.8") && !strings.HasPrefix(vb, "-80.9") {
		t.Fatalf("unexpected viewbox: %s", vb)
	}
}

func TestGeocodeViewboxDoesNotUnionMutualAidDests(t *testing.T) {
	geo := &GeoOptions{
		BoundsLat: 41.188351, BoundsLon: -80.7484585, BoundsRadiusMi: 10,
		HomeLat: 41.188351, HomeLon: -80.7484585, HomeMaxRadiusMi: 35,
		MutualAidDestinations: []MutualAidDestination{
			{CityLabel: "Newton Falls, OH", Lat: 41.19601, Lon: -80.970188, RadiusMi: 15},
			{CityLabel: "Warren, OH", Lat: 41.241457, Lon: -80.817239, RadiusMi: 15},
		},
	}
	vb := geocodeViewboxParam(geo)
	// Niles 12mi west is ~-80.979. Dest-union west (Newton Falls 15mi) is -81.259.
	if strings.HasPrefix(vb, "-81.") {
		t.Fatalf("viewbox unioned mutual-aid dests: %s", vb)
	}
	if !strings.HasPrefix(vb, "-80.9") {
		t.Fatalf("expected tight Niles box, got %s", vb)
	}

	geo.SpokenLocalityLat = 41.19601
	geo.SpokenLocalityLon = -80.970188
	geo.SpokenLocalityRadiusMi = 15
	vb2 := geocodeViewboxParam(geo)
	if !strings.HasPrefix(vb2, "-81.2") && !strings.HasPrefix(vb2, "-81.1") {
		t.Fatalf("spoken Newton Falls locality should widen the box: %s", vb2)
	}
}

func TestClearPinContradictionDisabled(t *testing.T) {
	c := &CuratedAlert{
		Address: "966 EVERETT CORTLAND HULL ROAD",
		Lat:     "41.32998", Lon: "-80.78683",
	}
	tr := "966 EVERETT HALL IN FRONT OF THE FAIRGROUNDS"
	if !TranscriptContradictsAddressStreet(c.Address, tr) {
		t.Fatal("expected contradict diagnostic still true for HALL vs CORTLAND HULL")
	}
	ClearPinWhenStreetContradictsTranscript(c, tr)
	if c.Lat == "" || c.Lon == "" {
		t.Fatal("ClearPin must not wipe gateway pin")
	}
}
