package main

import (
	"encoding/json"
	"testing"
)

func TestTalkgroupHiddenRoundTrip(t *testing.T) {
	tg := NewTalkgroup().FromMap(map[string]any{"id": float64(1), "label": "X", "hidden": true})
	if !tg.Hidden {
		t.Fatal("hidden flag not parsed from map")
	}

	b, err := tg.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if v, _ := out["hidden"].(bool); !v {
		t.Fatalf("hidden flag not marshalled: %s", b)
	}

	if NewTalkgroup().FromMap(map[string]any{"id": float64(2)}).Hidden {
		t.Fatal("hidden must default to false")
	}
}
