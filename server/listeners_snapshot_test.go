package main

import (
	"net/http"
	"testing"
	"time"
)

func TestSnapshotListenersGroupsDevices(t *testing.T) {
	clients := NewClients()

	userA := &User{Id: 10, Email: "a@example.com", FirstName: "Ann", LastName: "Alpha"}
	userB := &User{Id: 20, Email: "b@example.com", FirstName: "Bob", LastName: "Beta"}

	reqA := &http.Request{RemoteAddr: "10.0.0.1:1234", Header: http.Header{}}
	reqB := &http.Request{RemoteAddr: "10.0.0.2:1234", Header: http.Header{}}
	reqAnon := &http.Request{RemoteAddr: "10.0.0.3:1234", Header: http.Header{}}

	c1 := &Client{User: userA, request: reqA, Livefeed: NewLivefeed(), ConnectedAt: time.Now().UTC().Add(-2 * time.Minute)}
	c2 := &Client{User: userA, request: reqB, FCMToken: "token-abc", Livefeed: NewLivefeed(), ConnectedAt: time.Now().UTC().Add(-1 * time.Minute)}
	c3 := &Client{User: userB, request: reqB, Livefeed: NewLivefeed(), ConnectedAt: time.Now().UTC()}
	c4 := &Client{request: reqAnon, Livefeed: NewLivefeed(), ConnectedAt: time.Now().UTC()}

	clients.Add(c1)
	clients.Add(c2)
	clients.Add(c3)
	clients.Add(c4)

	snap := clients.SnapshotListeners()
	if snap.TotalConnections != 4 {
		t.Fatalf("totalConnections=%d want 4", snap.TotalConnections)
	}
	if snap.UniqueUsers != 2 {
		t.Fatalf("uniqueUsers=%d want 2", snap.UniqueUsers)
	}
	if snap.AnonymousConnections != 1 {
		t.Fatalf("anonymousConnections=%d want 1", snap.AnonymousConnections)
	}
	if len(snap.Listeners) != 3 {
		t.Fatalf("listeners=%d want 3 (2 users + anonymous)", len(snap.Listeners))
	}

	var foundA, foundB, foundAnon bool
	for _, u := range snap.Listeners {
		if u.Anonymous {
			foundAnon = true
			if u.DeviceCount != 1 {
				t.Fatalf("anonymous deviceCount=%d want 1", u.DeviceCount)
			}
			continue
		}
		if u.UserId == 10 {
			foundA = true
			if u.DeviceCount != 2 {
				t.Fatalf("user A deviceCount=%d want 2", u.DeviceCount)
			}
			if u.Email != "a@example.com" {
				t.Fatalf("user A email=%q", u.Email)
			}
			kinds := map[string]bool{}
			for _, d := range u.Devices {
				kinds[d.ClientKind] = true
				if d.IP == "" {
					t.Fatal("expected IP on device")
				}
			}
			if !kinds["web"] || !kinds["mobile"] {
				t.Fatalf("user A kinds=%v want web+mobile", kinds)
			}
		}
		if u.UserId == 20 {
			foundB = true
			if u.DeviceCount != 1 {
				t.Fatalf("user B deviceCount=%d want 1", u.DeviceCount)
			}
		}
		// Ensure secrets are not accidentally present via JSON field names on structs
		// (PIN/FCM are simply not on the snapshot types).
	}
	if !foundA || !foundB || !foundAnon {
		t.Fatalf("missing groups: A=%v B=%v anon=%v", foundA, foundB, foundAnon)
	}
}
