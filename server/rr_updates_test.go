package main

import "testing"

func TestBuildRRReview(t *testing.T) {
	groups := NewGroups()
	groups.List = []*Group{{Id: 1, Label: "Unknown"}, {Id: 2, Label: "Mahoning County"}, {Id: 3, Label: "Carroll County"}}
	tags := NewTags()
	tags.List = []*Tag{{Id: 1, Label: "Untagged"}, {Id: 2, Label: "Law Dispatch "}}

	systems, marcs, _, mahoning, carroll := routingFixture()
	ctrl := &Controller{Systems: systems, Groups: groups, Tags: tags}

	named := func(tg *Talkgroup, label, name string, group, tag uint64) {
		tg.Label, tg.Name, tg.GroupIds, tg.TagId = label, name, []uint64{group}, tag
	}
	byRef := func(s *System, ref uint) *Talkgroup {
		tg, _ := s.Talkgroups.GetTalkgroupByRef(ref)
		return tg
	}
	// Mahoning: 3100 named by hand, 3200 auto-populated, 5087 on the other network.
	named(byRef(mahoning, 3100), "MAH SO Disp", "Mahoning Sheriff Dispatch", 2, 2)
	named(byRef(mahoning, 3200), "3200", "3200", 1, 1)
	named(byRef(mahoning, 5087), "AB 5087", "AB", 1, 1)
	// Carroll: 4100 named by hand, 5087 named differently from Radio Reference.
	named(byRef(carroll, 4100), "CAR SO", "Carroll Sheriff", 3, 2)
	named(byRef(carroll, 5087), "CAR FD", "Carroll Fire", 3, 2)

	rr := []RadioReferenceTalkgroup{
		{ID: 3100, AlphaTag: "MAH SO Disp", Description: "Mahoning Sheriff Dispatch", Group: "Mahoning County Law", Tag: "Law Dispatch"},
		{ID: 3200, AlphaTag: "MAH SO 2", Description: "Mahoning Sheriff Tac 2", Group: "Mahoning County Law", Tag: "Law Tac"},
		{ID: 3300, AlphaTag: "MAH SO 3", Description: "Mahoning Sheriff Tac 3", Group: "Mahoning County Law", Tag: "Law Tac"},
		{ID: 4100, AlphaTag: "CAR SO", Description: "Carroll Sheriff", Group: "Carroll County", Tag: "Law Dispatch"},
		{ID: 4200, AlphaTag: "CAR EMS", Description: "Carroll EMS", Group: "Carroll County", Tag: "EMS Dispatch"},
		{ID: 5087, AlphaTag: "CAR FD Disp", Description: "Carroll Fire Dispatch", Group: "Carroll County", Tag: "Fire Dispatch"},
		{ID: 9000, AlphaTag: "HAMILTON", Description: "Hamilton County", Group: "Hamilton County", Tag: "Law Dispatch"},
	}
	review := ctrl.buildRRReview(marcs, rr)

	if review.Talkgroups != 4 {
		t.Fatalf("MARCS review should cover 4 talkgroups (Mahoning's AB 5087 is excluded), got %d", review.Talkgroups)
	}
	if len(review.Fill) != 1 || review.Fill[0].Ref != 3200 || review.Fill[0].Proposed.Group != "Mahoning County" || review.Fill[0].Proposed.Label != "MAH SO 2" {
		t.Fatalf("fill: %+v", review.Fill)
	}
	if len(review.Differ) != 1 || review.Differ[0].Ref != 5087 || review.Differ[0].SystemId != carroll.Id {
		t.Fatalf("differ: %+v", review.Differ)
	}
	if len(review.Missing) != 2 {
		t.Fatalf("missing should be 3300 and 4200 (not Hamilton), got %+v", review.Missing)
	}
	for _, m := range review.Missing {
		want := map[uint]uint64{3300: mahoning.Id, 4200: carroll.Id}[m.Ref]
		if m.SystemId != want {
			t.Errorf("missing %d should go to system %d, got %d", m.Ref, want, m.SystemId)
		}
	}
	if review.GroupMap["Mahoning County Law"] != "Mahoning County" {
		t.Errorf("group map: %+v", review.GroupMap)
	}
	if len(review.Targets) != 3 {
		t.Errorf("targets should be MARCS, Mahoning and Carroll: %+v", review.Targets)
	}
}
