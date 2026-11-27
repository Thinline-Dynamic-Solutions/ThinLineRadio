package main

import "testing"

func TestMutedSystemAdminSkipsHealthPush(t *testing.T) {
	controller := &Controller{}
	admin := &User{Verified: true, SystemAdmin: true}
	if !controller.userShouldReceiveSystemAlertPush(admin, "sdrtrunk_control_lost", "") {
		t.Fatal("system admin should receive health pushes by default")
	}
	admin.MuteSystemHealthAlerts = true
	if controller.userShouldReceiveSystemAlertPush(admin, "sdrtrunk_control_lost", "") {
		t.Fatal("muted system admin should not receive health pushes")
	}
}
