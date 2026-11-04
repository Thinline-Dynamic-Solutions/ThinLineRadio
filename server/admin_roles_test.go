package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func adminRolesTestSetup(perms ...string) (*Admin, string, *http.ServeMux, *[]string) {
	controller := &Controller{Options: &Options{}, Logs: NewLogs()}
	controller.Users = NewUsers()
	for _, u := range []*User{
		{Id: 1, Email: "mod@example.com"},
		{Id: 2, Email: "listener@example.com", Pin: "LISTENPIN"},
		{Id: 3, Email: "root@example.com", SystemAdmin: true, Pin: "ROOTPIN"},
		{Id: 4, Email: "othermod@example.com"},
	} {
		controller.Users.users[u.Id] = u
	}
	controller.AdminRoles = NewAdminRoles(controller)
	controller.AdminRoles.roles[10] = &AdminRole{Id: 10, Name: "Moderator", Permissions: normalizeAdminPermissions(perms)}
	controller.AdminRoles.members[1] = 10
	controller.AdminRoles.members[4] = 10
	admin := NewAdmin(controller)
	controller.Admin = admin

	seen := &[]string{}
	mux := http.NewServeMux()
	protected := func(w http.ResponseWriter, r *http.Request) {
		token := admin.GetAuthorization(r)
		*seen = append(*seen, token)
		if !admin.ValidateToken(token) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"users": []map[string]any{{"id": 2, "email": "listener@example.com", "pin": "LISTENPIN"}},
			"body":  string(body),
		})
	}
	for _, p := range []string{"/api/admin/users", "/api/admin/users/", "/api/admin/options", "/api/admin/apikeys", "/api/admin/transcript-review/", "/api/admin/roles"} {
		mux.HandleFunc(p, protected)
	}
	return admin, admin.sessions.openLimited(1), mux, seen
}

func gateRequest(admin *Admin, mux http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	rec := httptest.NewRecorder()
	admin.RoleGate(mux).ServeHTTP(rec, req)
	return rec
}

func TestMatchAdminPath(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"/api/admin/users", "/api/admin/users", true},
		{"/api/admin/users", "/api/admin/users/2", false},
		{"/api/admin/users/*", "/api/admin/users/2", true},
		{"/api/admin/users/*", "/api/admin/users/2/suspend", false},
		{"/api/admin/users/*/suspend", "/api/admin/users/2/suspend", true},
		{"/api/admin/systems/**", "/api/admin/systems/1/talkgroups/2", true},
		{"/api/admin/systems/**", "/api/admin/systems", false},
		{"/api/admin/users/*", "/api/admin/users//", false},
	}
	for _, c := range cases {
		if got := matchAdminPath(c.pattern, c.path); got != c.want {
			t.Errorf("match(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestAdminRouteRulesNeverGrantSystemAdminAreas(t *testing.T) {
	for _, path := range []string{
		"/api/admin/options", "/api/admin/apikeys", "/api/admin/dirwatch", "/api/admin/downstreams",
		"/api/admin/purge", "/api/admin/password", "/api/admin/config/reload", "/api/admin/roles",
		"/api/admin/roles/1", "/api/admin/users/2/admin-role", "/api/admin/fs/browse",
		"/api/admin/copilot/chat", "/api/admin/relay-account/login", "/api/admin/groups/apply-tax-rate",
		"/api/admin/transcript-review/collector", "/api/admin/transcript-review/collector/request-key",
		"/api/admin/public-api/keys", "/api/admin/email-test", "/api/admin/stripe-sync",
	} {
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
			if rule := findAdminRouteRule(method, path); rule != nil {
				t.Errorf("%s %s is grantable via %q", method, path, rule.pattern)
			}
		}
	}
	for _, rule := range adminRouteRules {
		for _, p := range rule.perms {
			if !adminPermissionKeys[p] {
				t.Errorf("rule %q uses unknown permission %q", rule.pattern, p)
			}
		}
	}
}

func TestRoleGateAllowsGrantedRouteWithScrubbedSecrets(t *testing.T) {
	admin, token, mux, seen := adminRolesTestSetup("users.view")

	rec := gateRequest(admin, mux, "GET", "/api/admin/users", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "LISTENPIN") || !strings.Contains(rec.Body.String(), "listener@example.com") {
		t.Fatalf("pin not scrubbed: %s", rec.Body.String())
	}
	pass := (*seen)[0]
	if pass == token || !strings.HasPrefix(pass, adminPassPrefix) {
		t.Fatalf("handler saw %q, want a pass", pass)
	}
	if admin.ValidateToken(pass) {
		t.Fatal("pass must expire with the request")
	}
	if admin.ValidateToken(token) {
		t.Fatal("limited token must never satisfy ValidateToken")
	}
}

func TestRoleGateDeniesUngrantedRoutes(t *testing.T) {
	admin, token, mux, seen := adminRolesTestSetup("users.view", "content.transcripts")
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/admin/options"},
		{"GET", "/api/admin/apikeys"},
		{"GET", "/api/admin/roles"},
		{"POST", "/api/admin/users/2/suspend"},
		{"DELETE", "/api/admin/users/2"},
		{"GET", "/api/admin/transcript-review/collector"},
	} {
		if rec := gateRequest(admin, mux, c.method, c.path, token, "{}"); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", c.method, c.path, rec.Code)
		}
	}
	if len(*seen) != 0 {
		t.Fatalf("denied requests reached handlers: %v", *seen)
	}
}

func TestRoleGateProtectsAdminsAndStripsPrivilegedFields(t *testing.T) {
	admin, token, mux, _ := adminRolesTestSetup("users.edit", "users.delete")

	for _, id := range []string{"3", "4", "1"} {
		if rec := gateRequest(admin, mux, "PUT", "/api/admin/users/"+id, token, `{"email":"x@example.com"}`); rec.Code != http.StatusForbidden {
			t.Errorf("edit of admin user %s = %d, want 403", id, rec.Code)
		}
	}
	if rec := gateRequest(admin, mux, "POST", "/api/admin/users/bulk-delete", token, `{"userIds":[2,3]}`); rec.Code != http.StatusForbidden {
		t.Errorf("bulk delete including a system admin = %d, want 403", rec.Code)
	}

	rec := gateRequest(admin, mux, "PUT", "/api/admin/users/2", token, `{"email":"l@example.com","systemAdmin":true,"pin":"1234"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit of listener = %d, want 200", rec.Code)
	}
	var out struct{ Body string }
	json.Unmarshal(rec.Body.Bytes(), &out)
	if strings.Contains(out.Body, "systemAdmin") || strings.Contains(out.Body, "1234") || !strings.Contains(out.Body, "l@example.com") {
		t.Fatalf("handler body = %s", out.Body)
	}
}

func TestRoleGateEndsSessionWhenRoleRemoved(t *testing.T) {
	admin, token, mux, _ := adminRolesTestSetup("users.view")
	delete(admin.Controller.AdminRoles.members, 1)
	if rec := gateRequest(admin, mux, "GET", "/api/admin/users", token, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if admin.sessions.lookupLimited(token) != nil {
		t.Fatal("session should be closed")
	}
}

func TestRoleGateRejectsForgedPass(t *testing.T) {
	admin, _, mux, seen := adminRolesTestSetup("users.view")
	if rec := gateRequest(admin, mux, "GET", "/api/admin/users", adminPassPrefix+"deadbeef", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(*seen) != 0 {
		t.Fatal("forged pass reached a handler")
	}
}

func TestRoleGateSessionEndpoint(t *testing.T) {
	admin, token, mux, _ := adminRolesTestSetup("logs.view", "users.view")
	rec := gateRequest(admin, mux, "GET", "/api/admin/session", token, "")
	var out struct {
		SystemAdmin bool     `json:"systemAdmin"`
		Role        string   `json:"role"`
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.SystemAdmin || out.Role != "Moderator" || len(out.Permissions) != 2 {
		t.Fatalf("session = %s", rec.Body.String())
	}
}

func TestRedactPinText(t *testing.T) {
	cases := map[string]string{
		"[ReconnectionManager] User id:1604 (PIN: SIMPIN4242) reconnected": "[ReconnectionManager] User id:1604 (PIN: [redacted]) reconnected",
		"invalid user pin ABCD1234 for ip 1.2.3.4":                         "invalid user pin [redacted] for ip 1.2.3.4",
		"saved state for user pin:XYZ98765":                                "saved state for user pin:[redacted]",
		"invalid user pin for ip 1.2.3.4":                                  "invalid user pin for ip 1.2.3.4",
		"user connected with expired pin":                                  "user connected with expired pin",
		"incident pin moved":                                               "incident pin moved",
	}
	for in, want := range cases {
		if got := redactPinText(in); got != want {
			t.Errorf("redactPinText(%q) = %q, want %q", in, got, want)
		}
	}
}
