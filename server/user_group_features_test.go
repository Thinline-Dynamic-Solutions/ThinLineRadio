package main

import "testing"

func TestMergeFeatures(t *testing.T) {
	a := FreeGroupDefaultFeatures()
	b := UserGroupFeatures{Replay: true, Alerts: true}
	m := MergeFeatures(a, b)
	if !m.LiveFeed || !m.Replay || !m.Alerts {
		t.Fatalf("merge failed: %+v", m)
	}
	if m.Map || m.Stats {
		t.Fatalf("unexpected features on: %+v", m)
	}
}

func TestParseUserGroupFeaturesEmptyIsAllOn(t *testing.T) {
	f := parseUserGroupFeatures("")
	if !f.LiveFeed || !f.Replay || !f.Settings {
		t.Fatalf("empty should be all-on: %+v", f)
	}
}

func TestFeaturesHas(t *testing.T) {
	f := AllFeaturesOn()
	if !f.Has("liveFeed") || !f.Has("alerts_side_rail") {
		t.Fatal("Has failed for all-on")
	}
	f.AlertsSideRail = false
	if f.Has("alertsSideRail") {
		t.Fatal("alertsSideRail should be off")
	}
}
