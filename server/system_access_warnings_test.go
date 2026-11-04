// Copyright (C) 2026 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"reflect"
	"testing"
)

func TestSystemAccessWarnings(t *testing.T) {
	newGroup := func(name, access string) *UserGroup {
		g := &UserGroup{Name: name, SystemAccess: access}
		g.loadSystemAccess()
		return g
	}

	system := &System{SystemRef: 20, Talkgroups: NewTalkgroups()}
	system.Talkgroups.List = []*Talkgroup{
		{TalkgroupRef: 1},
		{TalkgroupRef: 2},
		{TalkgroupRef: 3, Hidden: true},
	}

	groups := []*UserGroup{
		newGroup("All", ""),
		newGroup("Frozen", `[{"id":10,"talkgroups":"*"}]`),
		newGroup("Partial", `[{"id":20,"talkgroups":[1]}]`),
		newGroup("Whole", `[{"id":20,"talkgroups":"*"}]`),
	}

	got := systemAccessWarnings(system, groups)
	want := []string{
		"Frozen: system not included",
		"Partial: 1 talkgroup not included",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("warnings = %v, want %v", got, want)
	}
}
