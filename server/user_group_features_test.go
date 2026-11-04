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

func freeGroupController(groups ...*UserGroup) *Controller {
	ugs := NewUserGroups()
	for i, g := range groups {
		g.Id = uint64(i + 1)
		g.loadSystemAccess()
		g.loadSystemDelays()
		g.loadTalkgroupDelays()
		g.loadFeatures()
		ugs.groups[g.Id] = g
	}
	return &Controller{UserGroups: ugs, Options: &Options{}}
}

func testCall(system, talkgroup uint) *Call {
	return &Call{System: &System{SystemRef: system}, Talkgroup: &Talkgroup{TalkgroupRef: talkgroup}}
}

func TestAnonymousGetsFreeGroupDelay(t *testing.T) {
	controller := freeGroupController(&UserGroup{
		IsFreeGroup:     true,
		SystemAccess:    "[1]",
		Delay:           30,
		TalkgroupDelays: `{"1:20":45}`,
	})
	anon := &Client{Controller: controller}

	if got := controller.clientEffectiveDelay(anon, testCall(1, 10), 0); got != 30 {
		t.Fatalf("group delay: got %d, want 30", got)
	}
	if got := controller.clientEffectiveDelay(anon, testCall(1, 20), 0); got != 45 {
		t.Fatalf("talkgroup delay: got %d, want 45", got)
	}
	if got := controller.anonymousMinDelay(nil, 0); got != 30 {
		t.Fatalf("search cutoff: got %d, want 30", got)
	}
	admin := &Client{Controller: controller, BypassPlaybackSearchACL: true}
	if got := controller.clientEffectiveDelay(admin, testCall(1, 10), 0); got != 0 {
		t.Fatalf("admin search must not get the free delay, got %d", got)
	}
}

func TestAnonymousMinDelayIsLowerBound(t *testing.T) {
	controller := freeGroupController(&UserGroup{
		IsFreeGroup:     true,
		TalkgroupDelays: `{"1:20":45}`,
	})
	if got := controller.anonymousMinDelay(nil, 0); got != 0 {
		t.Fatalf("talkgroups without a delay must keep the cutoff at 0, got %d", got)
	}
}

func TestAnonymousFeaturesNeverIncludeSignInOnlySurfaces(t *testing.T) {
	controller := freeGroupController(&UserGroup{IsFreeGroup: true, Features: AllFeaturesOn().ToJSON()})
	f := controller.featuresForAnonymous(nil)
	if !f.LiveFeed || !f.Replay || !f.Channels || !f.Settings {
		t.Fatalf("free features lost: %+v", f)
	}
	if f.Alerts || f.Transcripts || f.Stats || f.Map {
		t.Fatalf("sign-in-only features must stay off for anonymous: %+v", f)
	}
}

func TestAdminSearchClientHasAllFeatures(t *testing.T) {
	controller := freeGroupController(&UserGroup{IsFreeGroup: true, Features: FreeGroupDefaultFeatures().ToJSON()})
	admin := &Client{Controller: controller, BypassPlaybackSearchACL: true}
	if !controller.clientHasFeature(admin, "replay") {
		t.Fatal("admin search client must keep replay")
	}
	anon := &Client{Controller: controller}
	if controller.clientHasFeature(anon, "replay") {
		t.Fatal("free default must not include replay")
	}
}

func TestListenLinkScopesSignedOutClient(t *testing.T) {
	controller := freeGroupController(&UserGroup{IsFreeGroup: true, SystemAccess: "[2]", Delay: 30})
	link := &ListenLink{
		Enabled:      true,
		SystemAccess: `[{"id":1,"talkgroups":[10]}]`,
		Delay:        5,
		Features:     UserGroupFeatures{LiveFeed: true, Replay: true}.ToJSON(),
	}
	link.buildScope()
	client := &Client{Controller: controller, ListenLink: link}

	if !controller.anonymousHasCallAccess(link, testCall(1, 10)) {
		t.Fatal("link channel must be allowed")
	}
	if controller.anonymousHasCallAccess(link, testCall(1, 11)) {
		t.Fatal("talkgroup outside the link must be denied")
	}
	if controller.anonymousHasCallAccess(link, testCall(2, 1)) {
		t.Fatal("free group channels must not leak into a link")
	}
	if got := controller.clientEffectiveDelay(client, testCall(1, 10), 0); got != 5 {
		t.Fatalf("link delay: got %d, want 5", got)
	}
	if !controller.clientHasFeature(client, "replay") || controller.clientHasFeature(client, "channels") {
		t.Fatalf("link features wrong: %+v", controller.featuresForClient(client))
	}
	if !controller.scopeRestricted(client) {
		t.Fatal("link clients must be scope-restricted even on open servers")
	}

	link.Enabled = false
	if controller.anonymousHasCallAccess(link, testCall(1, 10)) {
		t.Fatal("a turned-off link must grant nothing")
	}
	if controller.anonymousAllowed(link) {
		t.Fatal("a turned-off link must not allow listening")
	}

	link.Enabled = true
	link.ExpiresAt = 1
	if controller.anonymousAllowed(link) {
		t.Fatal("an expired link must not allow listening")
	}
}

func TestListenLinkTokens(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		token, err := newListenLinkToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(token) != 10 || seen[token] {
			t.Fatalf("bad or repeated token %q", token)
		}
		seen[token] = true
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
