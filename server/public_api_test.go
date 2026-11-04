package main

import (
	"strings"
	"testing"
)

func publicApiTestController(group *UserGroup) (*PublicApiService, *User, *System) {
	controller := freeGroupController(group)
	tg := &Talkgroup{Id: 100, TalkgroupRef: 10}
	sys := &System{Id: 7, SystemRef: 1, Talkgroups: &Talkgroups{List: []*Talkgroup{tg}}}
	controller.Systems = &Systems{List: []*System{sys}}
	controller.Delayer = &Delayer{controller: controller}
	user := &User{Id: 5, UserGroupId: 1, PublicApiEnabled: true}
	return NewPublicApiService(controller), user, sys
}

func TestPublicApiTranscriptsFollowGroupDelay(t *testing.T) {
	svc, user, sys := publicApiTestController(&UserGroup{SystemAccess: "[1]", Delay: 30})

	if got := svc.transcriptDelay(user, sys, sys.Talkgroups.List[0]); got.Minutes() != 30 {
		t.Fatalf("delay = %v, want 30m", got)
	}

	clause, args, ok := svc.transcriptAccessFilter(user)
	if !ok {
		t.Fatal("user should see the group's talkgroup")
	}
	if !strings.Contains(clause, `c."timestamp" <=`) || len(args) != 2 {
		t.Fatalf("delayed talkgroup must carry a timestamp cutoff: %s %v", clause, args)
	}
	if ids, _ := args[0].([]int64); len(ids) != 1 || ids[0] != 100 {
		t.Fatalf("talkgroup ids = %v, want [100]", args[0])
	}
}

func TestPublicApiTranscriptsNoDelayHasNoCutoff(t *testing.T) {
	svc, user, _ := publicApiTestController(&UserGroup{SystemAccess: "[1]"})
	clause, args, ok := svc.transcriptAccessFilter(user)
	if !ok || strings.Contains(clause, "timestamp") || len(args) != 1 {
		t.Fatalf("undelayed talkgroup should only filter by id: %s %v", clause, args)
	}
}

func TestPublicApiSystemAdminSeesEverythingUndelayed(t *testing.T) {
	svc, user, sys := publicApiTestController(&UserGroup{SystemAccess: "[1]", Delay: 30})
	user.SystemAdmin = true
	if clause, _, ok := svc.transcriptAccessFilter(user); !ok || clause != "" {
		t.Fatalf("admin should be unfiltered, got %q", clause)
	}
	if d := svc.transcriptDelay(user, sys, sys.Talkgroups.List[0]); d != 0 {
		t.Fatalf("admin delay = %v, want 0", d)
	}
}
