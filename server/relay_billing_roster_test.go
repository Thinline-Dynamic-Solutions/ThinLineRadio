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
