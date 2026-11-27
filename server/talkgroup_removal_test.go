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

func TestAdoptRemovedTalkgroupRefs(t *testing.T) {
	refs := map[uint64]uint{10: 100, 11: 200, 12: 300}
	stored := map[uint64]bool{10: true, 11: true, 12: true}
	readded := &Talkgroup{TalkgroupRef: 200}
	imported := &Talkgroup{Id: 99, TalkgroupRef: 300}
	kept := &Talkgroup{Id: 10, TalkgroupRef: 100}
	fresh := &Talkgroup{TalkgroupRef: 400}

	left := adoptRemovedTalkgroupRefs([]*Talkgroup{kept, readded, imported, fresh}, []uint64{11, 12}, refs, stored)
	if len(left) != 0 {
		t.Fatalf("still removed = %v, want none", left)
	}
	if readded.Id != 11 || imported.Id != 12 || kept.Id != 10 || fresh.Id != 0 {
		t.Fatalf("ids = %d %d %d %d, want 11 12 10 0", readded.Id, imported.Id, kept.Id, fresh.Id)
	}
}

func TestAdoptRemovedTalkgroupRefsLeavesUnmatched(t *testing.T) {
	left := adoptRemovedTalkgroupRefs([]*Talkgroup{{TalkgroupRef: 5}}, []uint64{7}, map[uint64]uint{7: 6}, map[uint64]bool{7: true})
	if len(left) != 1 || left[0] != 7 {
		t.Fatalf("still removed = %v, want [7]", left)
	}
}
