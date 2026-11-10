package main

import "testing"

func TestBillingRosterOpaqueIDStable(t *testing.T) {
	a := billingRosterOpaqueID(42)
	b := billingRosterOpaqueID(42)
	if a == "" || a != b {
		t.Fatalf("opaque id should be stable, got %q vs %q", a, b)
	}
	if billingRosterOpaqueID(43) == a {
		t.Fatal("different users must not share an opaque id")
	}
}

func TestUserBillingRequired(t *testing.T) {
	groups := NewUserGroups()
	paid := &UserGroup{Id: 1, BillingEnabled: true, BillingMode: "all_users"}
	free := &UserGroup{Id: 2, BillingEnabled: false}
	adminBilled := &UserGroup{Id: 3, BillingEnabled: true, BillingMode: "group_admin"}
	groups.mutex.Lock()
	groups.groups[1] = paid
	groups.groups[2] = free
	groups.groups[3] = adminBilled
	groups.mutex.Unlock()

	member := &User{Id: 1, UserGroupId: 1}
	if !userBillingRequired(member, groups, true) {
		t.Fatal("paid all_users member should require billing")
	}
	if userBillingRequired(member, groups, false) {
		t.Fatal("paywall off should not require billing")
	}
	freeUser := &User{Id: 2, UserGroupId: 2}
	if userBillingRequired(freeUser, groups, true) {
		t.Fatal("free group should not require billing")
	}
	staff := &User{Id: 3, UserGroupId: 3, IsGroupAdmin: false}
	if userBillingRequired(staff, groups, true) {
		t.Fatal("group_admin members are not billed")
	}
	admin := &User{Id: 4, UserGroupId: 3, IsGroupAdmin: true}
	if !userBillingRequired(admin, groups, true) {
		t.Fatal("group admin should require billing")
	}
}

func TestBuildBillingRosterFlags(t *testing.T) {
	groups := NewUserGroups()
	users := []*User{
		{Id: 1, SubscriptionStatus: "active"},
		{Id: 2, SubscriptionStatus: "not_billed", Suspended: true},
		{Id: 3, AccountExpiresAt: 1},
	}
	roster := buildBillingRoster(users, groups, false)
	if len(roster) != 3 {
		t.Fatalf("len=%d want 3", len(roster))
	}
	if roster[0].Suspended || roster[0].AccountExpired || roster[0].BillingRequired {
		t.Fatalf("user 1 flags: %+v", roster[0])
	}
	if !roster[1].Suspended {
		t.Fatal("user 2 should be suspended")
	}
	if !roster[2].AccountExpired {
		t.Fatal("user 3 should be expired")
	}
}

func TestBuildBillingRosterSkipsAdFreeExcluded(t *testing.T) {
	groups := NewUserGroups()
	groups.mutex.Lock()
	groups.groups[1] = &UserGroup{Id: 1, AdFreeExcluded: true}
	groups.groups[2] = &UserGroup{Id: 2, BillingEnabled: true, BillingMode: "all_users"}
	groups.mutex.Unlock()

	excluded := &User{Id: 1, UserGroupId: 1}
	alsoPaid := &User{Id: 2, UserGroupId: 1}
	alsoPaid.SetGroupIds([]uint64{1, 2})
	ungrouped := &User{Id: 3}

	if !userAdFreeExcluded(excluded, groups) {
		t.Fatal("user only in an excluded group should be excluded")
	}
	if userAdFreeExcluded(alsoPaid, groups) {
		t.Fatal("user also in a covered group should not be excluded")
	}
	if userAdFreeExcluded(ungrouped, groups) {
		t.Fatal("ungrouped user should not be excluded")
	}

	roster := buildBillingRoster([]*User{excluded, alsoPaid, ungrouped}, groups, true)
	if len(roster) != 2 {
		t.Fatalf("len=%d want 2", len(roster))
	}
	if roster[0].ID != billingRosterOpaqueID(2) || roster[1].ID != billingRosterOpaqueID(3) {
		t.Fatalf("unexpected roster: %+v", roster)
	}
}
