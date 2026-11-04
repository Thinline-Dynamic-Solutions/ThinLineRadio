package main

import (
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func delayModerationTestController(tgDelay uint) (*Controller, *User, *Call) {
	controller := freeGroupController(&UserGroup{SystemAccess: "[1]"})
	tg := &Talkgroup{Id: 100, TalkgroupRef: 10, Delay: tgDelay}
	sys := &System{Id: 7, SystemRef: 1, Talkgroups: &Talkgroups{List: []*Talkgroup{tg}}}
	controller.Systems = &Systems{List: []*System{sys}}
	controller.Delayer = &Delayer{controller: controller}
	controller.DelayModeration = NewDelayModeration(controller)
	controller.Users = NewUsers()
	user := &User{Id: 5, UserGroupId: 1}
	controller.Users.users[user.Id] = user
	call := &Call{Id: 42, System: sys, Talkgroup: tg, Timestamp: time.Now()}
	return controller, user, call
}

func TestNormalizeModerationScope(t *testing.T) {
	got := normalizeModerationScope([]string{" 1:* ", "1:10", "bad", "1:x", "x:1", "1:10", "*", ""})
	want := []string{"*", "1:*", "1:10"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scope = %v, want %v", got, want)
	}
}

func TestModerationScopeAllows(t *testing.T) {
	cases := []struct {
		scope []string
		want  bool
	}{
		{[]string{"*"}, true},
		{[]string{"1:*"}, true},
		{[]string{"1:10"}, true},
		{[]string{"1:11"}, false},
		{[]string{"2:*"}, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := moderationScopeAllows(c.scope, 1, 10); got != c.want {
			t.Errorf("scope %v: got %v, want %v", c.scope, got, c.want)
		}
	}
}

func TestCanModerateNeedsDelayScopeAndAccess(t *testing.T) {
	controller, user, call := delayModerationTestController(10)
	dm := controller.DelayModeration

	if dm.CanModerate(user, call) {
		t.Fatal("user without a scope must not moderate")
	}
	dm.scopes[user.Id] = []string{"1:10"}
	if !dm.CanModerate(user, call) || !dm.IsModerated(call) {
		t.Fatal("scoped user should moderate a delayed talkgroup")
	}

	call.Talkgroup.Delay = 0
	if dm.CanModerate(user, call) || dm.IsModerated(call) {
		t.Fatal("an undelayed talkgroup has nothing to moderate")
	}
	call.Talkgroup.Delay = 10

	controller.UserGroups.groups[1].SystemAccess = "[2]"
	controller.UserGroups.groups[1].loadSystemAccess()
	if dm.CanModerate(user, call) {
		t.Fatal("moderator must also be able to hear the talkgroup")
	}
}

func TestRemovedCallCancelsHeldSends(t *testing.T) {
	controller, _, call := delayModerationTestController(10)
	dm := controller.DelayModeration

	var fired atomic.Bool
	timer := time.AfterFunc(50*time.Millisecond, func() { fired.Store(true) })
	dm.Hold(call.Id, timer, time.Now().Add(50*time.Millisecond))
	dm.markRemoved(call.Id)

	time.Sleep(100 * time.Millisecond)
	if fired.Load() {
		t.Fatal("removing a call must stop its held sends")
	}
	if !dm.IsRemoved(call.Id) {
		t.Fatal("call should be marked removed")
	}
}

func TestAlertRecipientsWaitForTalkgroupDelay(t *testing.T) {
	controller, user, call := delayModerationTestController(10)

	var resent atomic.Int32
	ready := controller.alertRecipientsReleased([]uint64{user.Id}, call, func([]uint64) { resent.Add(1) })
	if len(ready) != 0 {
		t.Fatalf("talkgroup delay must hold the alert, got ready %v", ready)
	}

	call.Timestamp = time.Now().Add(-11 * time.Minute)
	if ready := controller.alertRecipientsReleased([]uint64{user.Id}, call, func([]uint64) {}); len(ready) != 1 {
		t.Fatalf("released call should alert now, got %v", ready)
	}

	controller.DelayModeration.markRemoved(call.Id)
	if ready := controller.alertRecipientsReleased([]uint64{user.Id}, call, func([]uint64) {}); len(ready) != 0 {
		t.Fatalf("removed call must never alert, got %v", ready)
	}
	if resent.Load() != 0 {
		t.Fatal("held alert must not resend after removal")
	}
}

func TestAlertResendsWhenDelayEnds(t *testing.T) {
	controller, user, call := delayModerationTestController(1)
	call.Timestamp = time.Now().Add(-time.Minute + 200*time.Millisecond)

	done := make(chan []uint64, 1)
	if ready := controller.alertRecipientsReleased([]uint64{user.Id}, call, func(ids []uint64) { done <- ids }); len(ready) != 0 {
		t.Fatalf("alert should be held, got %v", ready)
	}
	select {
	case ids := <-done:
		if len(ids) != 1 || ids[0] != user.Id {
			t.Fatalf("resent to %v", ids)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("held alert was never resent")
	}
}

func TestTalkgroupDelayGatesEveryClientPath(t *testing.T) {
	controller, user, call := delayModerationTestController(10)
	listener := &Client{User: user}

	if controller.callReleasedForClient(listener, call) {
		t.Fatal("talkgroup delay must block playback by id")
	}
	if !controller.callReleasedForClient(&Client{BypassPlaybackSearchACL: true}, call) {
		t.Fatal("bypass clients are never delayed")
	}
	if got := controller.maxReleaseDelay(user); got != 10*time.Minute {
		t.Fatalf("max delay = %v, want 10m", got)
	}

	call.Timestamp = time.Now().Add(-11 * time.Minute)
	if !controller.callReleasedForClient(listener, call) {
		t.Fatal("call should be playable once its delay ends")
	}
	controller.DelayModeration.markRemoved(call.Id)
	if controller.callReleasedForClient(listener, call) {
		t.Fatal("a removed call is never released")
	}
}

func TestHeldCallsFilter(t *testing.T) {
	controller, _, _ := delayModerationTestController(10)
	now := time.UnixMilli(1_000_000_000)

	want := ` AND NOT ((c."talkgroupId" IN (100) AND c."timestamp" > 999400000))`
	if got := controller.heldCallsFilter(now, "c", "timestamp"); got != want {
		t.Fatalf("filter = %s, want %s", got, want)
	}

	controller.Systems.List[0].Talkgroups.List[0].Delay = 0
	if got := controller.heldCallsFilter(now, "c", "timestamp"); got != "" {
		t.Fatalf("no delays should add no filter, got %s", got)
	}
}
