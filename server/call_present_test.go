package main

import (
	"testing"
)

func TestEnabledMatchingTalkgroupRefsPrefersPrimary(t *testing.T) {
	lf := NewLivefeed()
	lf.Matrix[78] = map[uint]bool{
		46012: true, // SW
		46024: true, // SE
		46028: true, // CENT primary
	}

	sys := &System{SystemRef: 78, Talkgroups: NewTalkgroups()}
	primary := &Talkgroup{TalkgroupRef: 46028, Label: "78 FD CENT"}
	sys.Talkgroups.List = []*Talkgroup{
		primary,
		{TalkgroupRef: 46012, Label: "78 FD SW"},
		{TalkgroupRef: 46024, Label: "78 FD SE"},
	}

	call := NewCall()
	call.System = sys
	call.Talkgroup = primary
	call.Patches = []uint{46028, 46012, 46024}

	got := lf.EnabledMatchingTalkgroupRefs(call)
	if len(got) != 3 {
		t.Fatalf("expected 3 matches, got %v", got)
	}
	if got[0] != 46028 {
		t.Fatalf("primary should be first, got %v", got)
	}
}

func TestCallWithPresentedTalkgroupClearsPatches(t *testing.T) {
	sys := &System{SystemRef: 78, Talkgroups: NewTalkgroups()}
	primary := &Talkgroup{TalkgroupRef: 46028, Label: "78 FD CENT"}
	sw := &Talkgroup{TalkgroupRef: 46012, Label: "78 FD SW"}
	sys.Talkgroups.List = []*Talkgroup{primary, sw}

	call := NewCall()
	call.Id = 42
	call.System = sys
	call.Talkgroup = primary
	call.TalkgroupId = 46028
	call.Patches = []uint{46028, 46012}

	presented := callWithPresentedTalkgroup(call, 46012)
	if presented == nil {
		t.Fatal("expected presented call")
	}
	if presented.Talkgroup.TalkgroupRef != 46012 {
		t.Fatalf("expected SW, got %d", presented.Talkgroup.TalkgroupRef)
	}
	if len(presented.Patches) != 0 {
		t.Fatalf("patches should be cleared, got %v", presented.Patches)
	}
	if len(call.Patches) != 2 {
		t.Fatal("original call patches must stay intact")
	}
	if call.Talkgroup.TalkgroupRef != 46028 {
		t.Fatal("original primary must stay intact")
	}
}

func TestPresentCallForClientSingleWhenMultipleOn(t *testing.T) {
	controller := &Controller{Options: NewOptions()}
	// Auth off so ACL is not a factor
	controller.Options.UserRegistrationEnabled = false

	sys := &System{SystemRef: 181, Talkgroups: NewTalkgroups()}
	primary := &Talkgroup{TalkgroupRef: 56380, Label: "SW ALERT"}
	other := &Talkgroup{TalkgroupRef: 56348, Label: "SW Fire 1"}
	sys.Talkgroups.List = []*Talkgroup{primary, other}

	call := NewCall()
	call.System = sys
	call.Talkgroup = primary
	call.TalkgroupId = 56380
	call.Patches = []uint{56380, 56348}

	lf := NewLivefeed()
	lf.Matrix[181] = map[uint]bool{56380: true, 56348: true}

	presented := controller.presentCallForClient(call, lf, nil, nil)
	if presented == nil {
		t.Fatal("expected a presented call")
	}
	if presented.Talkgroup.TalkgroupRef != 56380 {
		t.Fatalf("prefer primary when both on, got %d", presented.Talkgroup.TalkgroupRef)
	}
	if len(presented.Patches) != 0 {
		t.Fatalf("should not stream as patch, got %v", presented.Patches)
	}

	// Avoid primary — should present as the other enabled patch member once
	lf.Matrix[181][56380] = false
	presented = controller.presentCallForClient(call, lf, nil, nil)
	if presented == nil {
		t.Fatal("expected present-as patch member")
	}
	if presented.Talkgroup.TalkgroupRef != 56348 {
		t.Fatalf("expected SW Fire 1, got %d", presented.Talkgroup.TalkgroupRef)
	}
	if len(presented.Patches) != 0 {
		t.Fatal("patches must be cleared")
	}
}
