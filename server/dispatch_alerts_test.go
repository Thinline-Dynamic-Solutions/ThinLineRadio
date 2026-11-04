// Copyright (C) 2026 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
)

func dispatchTestController() *Controller {
	controller := freeGroupController(&UserGroup{Name: "Freelancers"}, &UserGroup{Name: "Paid"})
	controller.Users = NewUsers()
	for _, u := range []*User{
		{Id: 1, Email: "a@x", Verified: true, UserGroupId: 1},
		{Id: 2, Email: "b@x", Verified: true, UserGroupId: 2},
		{Id: 3, Email: "c@x", Verified: false, UserGroupId: 1},
		{Id: 4, Email: "d@x", Verified: true, UserGroupId: 1, Suspended: true},
		{Id: 5, Email: "e@x", Verified: true, UserGroupId: 2},
	} {
		controller.Users.users[u.Id] = u
	}
	return controller
}

func TestResolveDispatchRecipients(t *testing.T) {
	controller := dispatchTestController()
	ids := func(users []*User) []uint64 {
		out := []uint64{}
		for _, u := range users {
			out = append(out, u.Id)
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
	if got := ids(controller.resolveDispatchRecipients(nil, []uint64{1})); len(got) != 1 || got[0] != 1 {
		t.Errorf("group 1 = %v; want [1] (unverified and suspended members skipped)", got)
	}
	if got := ids(controller.resolveDispatchRecipients([]uint64{5, 2}, []uint64{2})); len(got) != 2 || got[0] != 2 || got[1] != 5 {
		t.Errorf("user plus overlapping group = %v; want [2 5] with no duplicates", got)
	}
	if got := controller.resolveDispatchRecipients([]uint64{3, 4}, nil); len(got) != 0 {
		t.Errorf("unverified/suspended users picked directly = %v; want none", ids(got))
	}
}

func TestDispatchAlertOnlyVisibleToRecipients(t *testing.T) {
	controller := dispatchTestController()
	data, _ := json.Marshal(&SystemAlertData{Label: "Fire Call", RecipientIds: []uint64{1}, SentBy: "chief@x"})
	alert := &SystemAlert{Id: 9, AlertType: dispatchAlertType, Data: string(data)}

	if !controller.userHasSystemAlertAccess(controller.Users.users[1], alert) {
		t.Error("recipient can't see the alert")
	}
	if controller.userHasSystemAlertAccess(controller.Users.users[2], alert) {
		t.Error("non-recipient can see the alert")
	}
	if !controller.userHasSystemAlertAccess(&User{Id: 99, SystemAdmin: true}, alert) {
		t.Error("system admin can't see the alert")
	}

	clean := listenerDispatchAlert(alert)
	if strings.Contains(clean.Data, "recipientIds") || strings.Contains(clean.Data, "chief@x") || !strings.Contains(clean.Data, "Fire Call") {
		t.Errorf("listener copy = %s", clean.Data)
	}
	if !strings.Contains(alert.Data, "recipientIds") {
		t.Error("original alert was modified")
	}
}

func TestDispatchAlertSound(t *testing.T) {
	controller := &Controller{Options: &Options{DispatchAlertSound: "smoke_alarm.wav"}}
	if got := controller.dispatchAlertSound(); got != "smoke_alarm.wav" {
		t.Errorf("sound = %s", got)
	}
	controller.Options.DispatchAlertSound = "../../etc/passwd"
	if got := controller.dispatchAlertSound(); got != defaultDispatchAlertSound {
		t.Errorf("unknown sound not replaced: %s", got)
	}
}

func TestDispatchRoutesForAdminRoles(t *testing.T) {
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/admin/dispatch-alerts"},
		{http.MethodPost, "/api/admin/dispatch-alerts"},
		{http.MethodGet, "/api/admin/dispatch-alerts/recipients"},
	} {
		rule := findAdminRouteRule(c.method, c.path)
		if rule == nil || len(rule.perms) != 1 || rule.perms[0] != "dispatch.send" {
			t.Errorf("%s %s should need dispatch.send", c.method, c.path)
		}
	}
	if findAdminRouteRule(http.MethodPut, "/api/admin/dispatch-alerts/settings") != nil {
		t.Error("changing the dispatch tone must stay system admin only")
	}
	if !adminPermissionKeys["dispatch.send"] {
		t.Error("dispatch.send missing from the permission catalog")
	}
}
