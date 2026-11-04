package main

import (
	"strings"
	"testing"
)

func routingTestSystem(id uint64, ref uint, label string, talkgroupRefs ...uint) *System {
	s := NewSystem()
	s.Id = id
	s.SystemRef = ref
	s.Label = label
	for _, tg := range talkgroupRefs {
		s.Talkgroups.List = append(s.Talkgroups.List, &Talkgroup{TalkgroupRef: tg, Label: label})
	}
	return s
}

// MARCS and Austintown/Boardman both carry 5087: Carroll's under MARCS, Mahoning's
// under Austintown/Boardman. Mahoning is under both main systems.
func routingFixture() (*Systems, *System, *System, *System, *System) {
	marcs := routingTestSystem(1, 7000, "Ohio MARCS")
	marcs.IsMain = true
	ab := routingTestSystem(2, 7512, "Austintown/Boardman")
	ab.IsMain = true
	mahoning := routingTestSystem(3, 50, "OH Mahoning", 5087, 3100, 3200)
	mahoning.ParentIds = []uint64{1, 2}
	mahoning.TalkgroupNetworks = map[uint]uint64{5087: 2, 3100: 1}
	carroll := routingTestSystem(4, 10, "OH Carroll", 5087, 4100)
	carroll.ParentIds = []uint64{1}

	systems := NewSystems()
	systems.List = []*System{marcs, ab, mahoning, carroll}
	systems.buildRoutesLocked()
	return systems, marcs, ab, mahoning, carroll
}

func TestRouteFromMainScopesByMainSystem(t *testing.T) {
	systems, marcs, ab, mahoning, carroll := routingFixture()

	cases := []struct {
		main *System
		tg   uint
		want *System
	}{
		{ab, 5087, mahoning},
		{marcs, 5087, carroll},
		{marcs, 3100, mahoning},
		{ab, 3100, nil},
		{marcs, 3200, mahoning},
		{ab, 3200, mahoning},
		{marcs, 4100, carroll},
		{ab, 4100, nil},
		{marcs, 9999, nil},
	}
	for _, c := range cases {
		got, ok := systems.RouteFromMain(c.main, c.tg, nil)
		if c.want == nil {
			if ok {
				t.Errorf("%s tg %d: routed to %s, want no route", c.main.Label, c.tg, got.Label)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("%s tg %d: got %v, want %s", c.main.Label, c.tg, got, c.want.Label)
		}
	}

	if got, ok := systems.RouteFromMain(marcs, 9999, []uint{4100}); !ok || got != carroll {
		t.Errorf("patched talkgroup should route to Carroll, got %v", got)
	}
	if _, ok := systems.RouteFromMain(carroll, 5087, nil); ok {
		t.Error("a subsystem upload must not be rerouted")
	}
}

func TestValidateSystemRouting(t *testing.T) {
	_, marcs, ab, mahoning, carroll := routingFixture()
	if problems := ValidateSystemRouting([]*System{marcs, ab, mahoning, carroll}); len(problems) != 0 {
		t.Fatalf("valid setup reported problems: %v", problems)
	}

	// 5087 on Mahoning routing from MARCS too would clash with Carroll.
	delete(mahoning.TalkgroupNetworks, 5087)
	problems := ValidateSystemRouting([]*System{marcs, ab, mahoning, carroll})
	if len(problems) != 1 || !strings.Contains(problems[0], "Talkgroup 5087") || !strings.Contains(problems[0], "Ohio MARCS") {
		t.Fatalf("expected one 5087 clash under MARCS, got %v", problems)
	}
	mahoning.TalkgroupNetworks[5087] = 2

	carroll.SystemRef = 7512
	problems = ValidateSystemRouting([]*System{marcs, ab, mahoning, carroll})
	if len(problems) != 1 || !strings.Contains(problems[0], "System ID 7512") {
		t.Fatalf("expected shared ID problem, got %v", problems)
	}
	carroll.SystemRef = 10

	carroll.ParentIds = []uint64{3}
	problems = ValidateSystemRouting([]*System{marcs, ab, mahoning, carroll})
	if len(problems) != 1 || !strings.Contains(problems[0], "isn't marked as a main system") {
		t.Fatalf("expected non-main parent problem, got %v", problems)
	}
	carroll.ParentIds = []uint64{1}

	mahoning.TalkgroupNetworks[3200] = 9
	problems = ValidateSystemRouting([]*System{marcs, ab, mahoning, carroll})
	if len(problems) != 1 || !strings.Contains(problems[0], "Talkgroup 3200") {
		t.Fatalf("expected bad network problem, got %v", problems)
	}
}

func TestScopedSystemsHideMainSystems(t *testing.T) {
	systems, _, _, _, _ := routingFixture()
	scoped := systems.GetScopedSystems(&Client{}, NewGroups(), NewTags(), false)
	labels := []string{}
	for _, s := range scoped {
		labels = append(labels, s["label"].(string))
	}
	if len(scoped) != 2 || strings.Contains(strings.Join(labels, ","), "MARCS") || strings.Contains(strings.Join(labels, ","), "Austintown") {
		t.Fatalf("listeners should only see the two subsystems, got %v", labels)
	}
}

func TestCoreMatchesMainAndSubsystemReports(t *testing.T) {
	systems, _, _, _, _ := routingFixture()
	core := &coreCoordinator{controller: &Controller{Systems: systems}}
	cases := []struct{ system, tg, want int }{
		{7512, 5087, 50},   // Austintown/Boardman main, Mahoning's 5087
		{7000, 5087, 10},   // MARCS main, Carroll's 5087
		{50, 5087, 50},     // Mahoning's own ID stays
		{7000, 9999, 7000}, // no subsystem has it
		{7000, 0, 7000},    // no talkgroup yet
	}
	for _, c := range cases {
		if got := core.matchSystem(c.system, c.tg, nil); got != c.want {
			t.Errorf("system %d tg %d: got %d, want %d", c.system, c.tg, got, c.want)
		}
	}
	if got := core.matchSystem(7000, 9999, []int{4100}); got != 10 {
		t.Errorf("patched talkgroup should match Carroll, got %d", got)
	}
}

func TestRoutingRoundTrip(t *testing.T) {
	_, _, _, mahoning, _ := routingFixture()
	mahoning.RadioReference = RadioReferenceLink{SystemId: 8115, SystemName: "Test", StateId: 39, CountyId: 2050}
	m := map[string]any{}
	routingToMap(mahoning, m)
	preserved := map[string]any{}
	preserveRoutingFields(preserved, mahoning)

	back := NewSystem()
	routingFromMap(back, preserved)
	if len(back.ParentIds) != 2 || back.TalkgroupNetworks[5087] != 2 || back.TalkgroupNetworks[3100] != 1 || back.RadioReference.SystemId != 8115 || back.RadioReference.CountyId != 2050 {
		t.Fatalf("round trip lost data: %+v %+v %+v", back.ParentIds, back.TalkgroupNetworks, back.RadioReference)
	}

	main := NewSystem()
	main.NoAudioAlertsEnabled = true
	routingFromMap(main, map[string]any{"isMain": true, "parentIds": []any{float64(3)}})
	if main.NoAudioAlertsEnabled || len(main.ParentIds) != 0 {
		t.Fatal("main systems drop parents and no-audio alerts")
	}
}
