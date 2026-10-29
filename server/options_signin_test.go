package main

import "testing"

func TestOptionsWithoutSignInSettingKeepScannerLocked(t *testing.T) {
	o := NewOptions()
	o.RequireListenerSignIn = true

	o.FromMap(map[string]any{"userRegistrationEnabled": true})
	if !o.RequireListenerSignIn {
		t.Fatal("options saved without requireListenerSignIn opened a locked scanner")
	}

	o.FromMap(map[string]any{"requireListenerSignIn": false})
	if o.RequireListenerSignIn {
		t.Fatal("an explicit false should still turn sign-in off")
	}
}
