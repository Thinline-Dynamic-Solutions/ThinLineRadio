package main

import "testing"

func removalFixture() *System {
	tgs := NewTalkgroups()
	tgs.List = []*Talkgroup{
		{Id: 1, TalkgroupRef: 100, Label: "Fire"},
		{Id: 2, TalkgroupRef: 200, Label: "EMS"},
		{Id: 3, TalkgroupRef: 300, Label: "Police"},
	}
	return &System{Id: 9, Label: "County", Talkgroups: tgs}
}

func TestRemovedTalkgroupsMatchesByIdThenRef(t *testing.T) {
	incoming := map[string]any{"talkgroups": []any{
		map[string]any{"id": float64(1), "talkgroupRef": float64(100)},
		map[string]any{"talkgroupRef": float64(300)},
	}}
	removed := removedTalkgroups(removalFixture(), incoming)
	if len(removed) != 1 || removed[0].Id != 2 {
		t.Fatalf("removed = %v, want only talkgroup 2", removed)
	}
}

func TestRemovedTalkgroupsMissingListRemovesAll(t *testing.T) {
	if got := len(removedTalkgroups(removalFixture(), map[string]any{})); got != 3 {
		t.Fatalf("removed %d, want 3", got)
	}
}

func TestKeepTalkgroupsInPayload(t *testing.T) {
	incoming := map[string]any{"talkgroups": []any{map[string]any{"id": float64(1)}}}
	stored := map[string]any{"talkgroups": []any{
		map[string]any{"id": float64(1)},
		map[string]any{"id": float64(2), "label": "EMS"},
		map[string]any{"id": float64(3)},
	}}
	keepTalkgroupsInPayload(incoming, stored, map[uint64]bool{2: true})
	tgs := incoming["talkgroups"].([]any)
	if len(tgs) != 2 || tgs[1].(map[string]any)["label"] != "EMS" {
		t.Fatalf("talkgroups = %v, want 1 plus stored EMS", tgs)
	}
}

func TestTalkgroupRemovalSummaryTruncates(t *testing.T) {
	list := []*Talkgroup{}
	for i := 0; i < 7; i++ {
		list = append(list, &Talkgroup{Label: "TG"})
	}
	if got, want := talkgroupRemovalSummary(list), "TG, TG, TG, TG, TG, and 2 more"; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}
