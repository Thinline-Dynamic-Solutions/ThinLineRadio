// Copyright (C) 2025 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>

package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AdminRoles holds the named permission sets a system admin can hand to
// ordinary users, letting them into parts of the admin console.
//
// Sessions opened by role holders never enter Admin.Tokens, so every handler
// that checks ValidateToken rejects them unless adminRoleGate approved the
// request against adminRouteRules first.
type AdminRoles struct {
	controller *Controller
	mutex      sync.RWMutex
	roles      map[uint64]*AdminRole
	members    map[uint64]uint64
}

type AdminRole struct {
	Id          uint64   `json:"id"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

type AdminPermission struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Group string `json:"group"`
}

var AdminPermissionCatalog = []AdminPermission{
	{"users.view", "View users", "Users"},
	{"users.edit", "Edit users (profile, access, devices, setup links)", "Users"},
	{"users.password", "Reset user passwords", "Users"},
	{"users.suspend", "Suspend and unsuspend users", "Users"},
	{"users.delete", "Delete users", "Users"},
	{"users.create", "Create and invite users", "Users"},
	{"users.delayModeration", "Assign delay moderators", "Users"},
	{"groups.view", "View user groups", "User Groups"},
	{"groups.edit", "Edit user groups, group admins and registration codes", "User Groups"},
	{"systems.view", "View systems and talkgroups", "Systems"},
	{"systems.edit", "Edit systems, talkgroups and unit aliases", "Systems"},
	{"systems.import", "Import from Radio Reference", "Systems"},
	{"tags.edit", "Edit tags and talkgroup groups", "Systems"},
	{"listeners.view", "View connected listeners", "Operations"},
	{"shareLinks.manage", "Manage share links", "Operations"},
	{"logs.view", "View logs", "Operations"},
	{"health.view", "View system health", "Operations"},
	{"sdr.command", "Send SDRTrunk commands", "Operations"},
	{"dispatch.send", "Send dispatch alerts to chosen users and groups", "Operations"},
	{"content.transcripts", "Review transcripts and hallucinations", "Content"},
	{"content.incidentPins", "Correct incident pins on the map", "Content"},
	{"content.callNatures", "Edit call natures", "Content"},
}

var adminPermissionKeys = func() map[string]bool {
	keys := map[string]bool{}
	for _, p := range AdminPermissionCatalog {
		keys[p.Key] = true
	}
	return keys
}()

func NewAdminRoles(controller *Controller) *AdminRoles {
	return &AdminRoles{
		controller: controller,
		roles:      map[uint64]*AdminRole{},
		members:    map[uint64]uint64{},
	}
}

func migrateAdminRoles(db *Database) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS "adminRoles" (
    "adminRoleId" bigserial NOT NULL PRIMARY KEY,
    "name" text NOT NULL UNIQUE,
    "permissions" text NOT NULL DEFAULT '[]',
    "createdAt" bigint NOT NULL DEFAULT 0
  )`,
		`CREATE TABLE IF NOT EXISTS "userAdminRoles" (
    "userId" bigint NOT NULL PRIMARY KEY,
    "adminRoleId" bigint NOT NULL,
    CONSTRAINT "userAdminRoles_userId" FOREIGN KEY ("userId") REFERENCES "users" ("userId") ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT "userAdminRoles_adminRoleId" FOREIGN KEY ("adminRoleId") REFERENCES "adminRoles" ("adminRoleId") ON DELETE CASCADE ON UPDATE CASCADE
  )`,
	}
	for _, query := range queries {
		if _, err := db.Sql.Exec(query); err != nil {
			return fmt.Errorf("migrateAdminRoles: %w", err)
		}
	}
	return nil
}

func normalizeAdminPermissions(perms []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, p := range perms {
		p = strings.TrimSpace(p)
		if adminPermissionKeys[p] && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func (ar *AdminRoles) Read(db *Database) error {
	roles := map[uint64]*AdminRole{}
	rows, err := db.Sql.Query(`SELECT "adminRoleId", "name", "permissions" FROM "adminRoles"`)
	if err != nil {
		return fmt.Errorf("adminroles.read: %w", err)
	}
	for rows.Next() {
		var (
			role AdminRole
			raw  string
		)
		if err := rows.Scan(&role.Id, &role.Name, &raw); err != nil {
			rows.Close()
			return fmt.Errorf("adminroles.read: %w", err)
		}
		var perms []string
		_ = json.Unmarshal([]byte(raw), &perms)
		role.Permissions = normalizeAdminPermissions(perms)
		roles[role.Id] = &role
	}
	rows.Close()

	members := map[uint64]uint64{}
	rows, err = db.Sql.Query(`SELECT "userId", "adminRoleId" FROM "userAdminRoles"`)
	if err != nil {
		return fmt.Errorf("adminroles.read: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var userId, roleId uint64
		if err := rows.Scan(&userId, &roleId); err != nil {
			return fmt.Errorf("adminroles.read: %w", err)
		}
		members[userId] = roleId
	}

	ar.mutex.Lock()
	ar.roles = roles
	ar.members = members
	ar.mutex.Unlock()
	return rows.Err()
}

type adminRoleListing struct {
	AdminRole
	Members int `json:"members"`
}

func (ar *AdminRoles) List() []adminRoleListing {
	ar.mutex.RLock()
	defer ar.mutex.RUnlock()
	counts := map[uint64]int{}
	for _, roleId := range ar.members {
		counts[roleId]++
	}
	out := make([]adminRoleListing, 0, len(ar.roles))
	for _, role := range ar.roles {
		out = append(out, adminRoleListing{
			AdminRole: AdminRole{Id: role.Id, Name: role.Name, Permissions: append([]string{}, role.Permissions...)},
			Members:   counts[role.Id],
		})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func (ar *AdminRoles) Save(db *Database, role AdminRole) (*AdminRole, error) {
	role.Name = strings.TrimSpace(role.Name)
	if role.Name == "" {
		return nil, errors.New("role name is required")
	}
	role.Permissions = normalizeAdminPermissions(role.Permissions)
	raw, _ := json.Marshal(role.Permissions)

	ar.mutex.RLock()
	for _, existing := range ar.roles {
		if existing.Id != role.Id && strings.EqualFold(existing.Name, role.Name) {
			ar.mutex.RUnlock()
			return nil, fmt.Errorf("a role named %q already exists", role.Name)
		}
	}
	_, known := ar.roles[role.Id]
	ar.mutex.RUnlock()

	if role.Id == 0 {
		if err := db.Sql.QueryRow(`INSERT INTO "adminRoles" ("name", "permissions", "createdAt") VALUES ($1, $2, $3) RETURNING "adminRoleId"`,
			role.Name, string(raw), time.Now().UnixMilli()).Scan(&role.Id); err != nil {
			return nil, err
		}
	} else {
		if !known {
			return nil, errors.New("role not found")
		}
		if _, err := db.Sql.Exec(`UPDATE "adminRoles" SET "name" = $1, "permissions" = $2 WHERE "adminRoleId" = $3`, role.Name, string(raw), role.Id); err != nil {
			return nil, err
		}
	}

	saved := &AdminRole{Id: role.Id, Name: role.Name, Permissions: role.Permissions}
	ar.mutex.Lock()
	ar.roles[role.Id] = saved
	ar.mutex.Unlock()
	return saved, nil
}

func (ar *AdminRoles) Delete(db *Database, roleId uint64) error {
	if _, err := db.Sql.Exec(`DELETE FROM "adminRoles" WHERE "adminRoleId" = $1`, roleId); err != nil {
		return err
	}
	ar.mutex.Lock()
	delete(ar.roles, roleId)
	for userId, id := range ar.members {
		if id == roleId {
			delete(ar.members, userId)
		}
	}
	ar.mutex.Unlock()
	return nil
}

// SetUserRole assigns a role to a user; roleId 0 removes it.
func (ar *AdminRoles) SetUserRole(db *Database, userId uint64, roleId uint64) error {
	if roleId == 0 {
		if _, err := db.Sql.Exec(`DELETE FROM "userAdminRoles" WHERE "userId" = $1`, userId); err != nil {
			return err
		}
		ar.mutex.Lock()
		delete(ar.members, userId)
		ar.mutex.Unlock()
		return nil
	}

	ar.mutex.RLock()
	_, known := ar.roles[roleId]
	ar.mutex.RUnlock()
	if !known {
		return errors.New("role not found")
	}
	if _, err := db.Sql.Exec(`INSERT INTO "userAdminRoles" ("userId", "adminRoleId") VALUES ($1, $2) ON CONFLICT ("userId") DO UPDATE SET "adminRoleId" = EXCLUDED."adminRoleId"`, userId, roleId); err != nil {
		return err
	}
	ar.mutex.Lock()
	ar.members[userId] = roleId
	ar.mutex.Unlock()
	return nil
}

func (ar *AdminRoles) RoleOf(userId uint64) *AdminRole {
	if ar == nil {
		return nil
	}
	ar.mutex.RLock()
	defer ar.mutex.RUnlock()
	role := ar.roles[ar.members[userId]]
	if role == nil {
		return nil
	}
	return &AdminRole{Id: role.Id, Name: role.Name, Permissions: append([]string{}, role.Permissions...)}
}

func (ar *AdminRoles) Permissions(userId uint64) []string {
	if role := ar.RoleOf(userId); role != nil {
		return role.Permissions
	}
	return []string{}
}

func (ar *AdminRoles) UserCan(userId uint64, perm string) bool {
	for _, p := range ar.Permissions(userId) {
		if p == perm {
			return true
		}
	}
	return false
}

// HoldsAdminPower reports whether a user is a system admin or holds a role.
// Limited sessions may never act on such users.
func (ar *AdminRoles) HoldsAdminPower(user *User) bool {
	return user != nil && (user.SystemAdmin || ar.RoleOf(user.Id) != nil)
}

// ----- sessions -----

type limitedAdminSession struct {
	userId  uint64
	expires time.Time
}

const (
	limitedAdminSessionTTL = 12 * time.Hour
	limitedAdminSessionMax = 50
	adminPassPrefix        = "tlr-pass-"
)

type adminSessions struct {
	mutex   sync.Mutex
	limited map[string]*limitedAdminSession
	passes  map[string]bool
	actors  map[string]string
}

func newAdminSessions() *adminSessions {
	return &adminSessions{
		limited: map[string]*limitedAdminSession{},
		passes:  map[string]bool{},
		actors:  map[string]string{},
	}
}

func randomAdminToken(prefix string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}

func (s *adminSessions) openLimited(userId uint64) string {
	token := randomAdminToken("tlr-role-")
	now := time.Now()
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for t, sess := range s.limited {
		if now.After(sess.expires) {
			delete(s.limited, t)
		}
	}
	for len(s.limited) >= limitedAdminSessionMax {
		var oldest string
		for t, sess := range s.limited {
			if oldest == "" || sess.expires.Before(s.limited[oldest].expires) {
				oldest = t
			}
		}
		delete(s.limited, oldest)
	}
	s.limited[token] = &limitedAdminSession{userId: userId, expires: now.Add(limitedAdminSessionTTL)}
	return token
}

func (s *adminSessions) lookupLimited(token string) *limitedAdminSession {
	if token == "" {
		return nil
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	sess := s.limited[token]
	if sess == nil {
		return nil
	}
	if time.Now().After(sess.expires) {
		delete(s.limited, token)
		return nil
	}
	return sess
}

func (s *adminSessions) closeLimited(token string) {
	s.mutex.Lock()
	delete(s.limited, token)
	s.mutex.Unlock()
}

func (s *adminSessions) openPass() string {
	pass := randomAdminToken(adminPassPrefix)
	s.mutex.Lock()
	s.passes[pass] = true
	s.mutex.Unlock()
	return pass
}

func (s *adminSessions) closePass(pass string) {
	s.mutex.Lock()
	delete(s.passes, pass)
	delete(s.actors, pass)
	s.mutex.Unlock()
}

func (s *adminSessions) isPass(token string) bool {
	if !strings.HasPrefix(token, adminPassPrefix) {
		return false
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.passes[token]
}

func (s *adminSessions) setActor(token, actor string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.actors[token] = actor
	if len(s.actors) > 50 {
		for t := range s.actors {
			if t != token {
				delete(s.actors, t)
				break
			}
		}
	}
}

func (s *adminSessions) actor(token string) string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if a := s.actors[token]; a != "" {
		return a
	}
	return "admin"
}

// ----- route permissions -----

type adminRouteRule struct {
	pattern string
	methods string
	perms   []string
	guard   func(admin *Admin, r *http.Request) string
}

func perms(p ...string) []string { return p }

var adminRouteRules = []adminRouteRule{
	{"/api/admin/users", "GET", perms("users.view", "groups.view"), nil},
	{"/api/admin/users/create", "POST", perms("users.create"), guardStripPrivileged},
	{"/api/admin/users/bulk-delete", "POST", perms("users.delete"), guardBulkDelete},
	{"/api/admin/users/transfer", "POST", perms("users.edit", "groups.edit"), guardTransfer},
	{"/api/admin/users/*/device-tokens/*", "DELETE", perms("users.edit"), guardUserTarget},
	{"/api/admin/users/*/test-push", "POST", perms("users.edit"), guardUserTarget},
	{"/api/admin/users/*/mobile-setup-link", "POST", perms("users.edit"), guardUserTarget},
	{"/api/admin/users/*/reset-password", "POST", perms("users.password"), guardUserTarget},
	{"/api/admin/users/*/suspend", "POST", perms("users.suspend"), guardUserTarget},
	{"/api/admin/users/*/delay-moderation", "GET", perms("users.delayModeration"), nil},
	{"/api/admin/users/*/delay-moderation", "PUT", perms("users.delayModeration"), guardUserTarget},
	{"/api/admin/users/*", "PUT,PATCH", perms("users.edit"), guardUserEdit},
	{"/api/admin/users/*", "DELETE", perms("users.delete"), guardUserTarget},
	{"/api/admin/invitations", "GET", perms("users.view"), nil},
	{"/api/admin/invitations", "*", perms("users.create"), guardStripPrivileged},

	{"/api/admin/groups", "GET", perms("groups.view", "users.view"), nil},
	{"/api/admin/groups/admins", "GET", perms("groups.view"), nil},
	{"/api/admin/groups/*/codes", "GET", perms("groups.view"), nil},
	{"/api/admin/groups/*/codes/generate", "POST", perms("groups.edit"), nil},
	{"/api/admin/groups/*/codes/*", "DELETE", perms("groups.edit"), nil},
	{"/api/admin/groups/create", "POST", perms("groups.edit"), nil},
	{"/api/admin/groups/update", "POST,PUT", perms("groups.edit"), nil},
	{"/api/admin/groups/delete/*", "DELETE,POST", perms("groups.edit"), nil},
	{"/api/admin/groups/assign-admin", "POST", perms("groups.edit"), guardBodyUserTarget},
	{"/api/admin/groups/remove-admin", "POST", perms("groups.edit"), guardBodyUserTarget},
	{"/api/admin/user-groups/*", "PATCH,PUT", perms("groups.edit"), nil},

	{"/api/admin/server-management", "GET", perms("systems.view"), nil},
	{"/api/admin/systems/**", "GET", perms("systems.view"), nil},
	{"/api/admin/systems/save", "POST,PUT", perms("systems.edit"), nil},
	{"/api/admin/systems/order", "POST,PUT", perms("systems.edit"), nil},
	{"/api/admin/systems/delete/*", "DELETE,POST", perms("systems.edit"), nil},
	{"/api/admin/systems/**", "PUT,PATCH,POST", perms("systems.edit"), nil},
	{"/api/admin/unit-alias-suggestions", "*", perms("systems.edit"), nil},
	{"/api/admin/unit-alias-suggestions/**", "*", perms("systems.edit"), nil},
	{"/api/admin/unit-alias-history-scan", "*", perms("systems.edit"), nil},
	{"/api/admin/radioreference/**", "*", perms("systems.import"), nil},
	{"/api/admin/tags", "GET", perms("systems.view", "tags.edit"), nil},
	{"/api/admin/tags", "*", perms("tags.edit"), nil},
	{"/api/admin/talkgroup-groups", "GET", perms("systems.view", "tags.edit"), nil},
	{"/api/admin/talkgroup-groups", "*", perms("tags.edit"), nil},

	{"/api/admin/listeners", "GET", perms("listeners.view", "users.view"), nil},
	{"/api/admin/listen-links", "*", perms("shareLinks.manage"), nil},
	{"/api/admin/listen-links/**", "*", perms("shareLinks.manage"), nil},
	{"/api/admin/logs", "GET,POST", perms("logs.view"), nil},
	{"/api/admin/logs/categories", "GET", perms("logs.view"), nil},

	{"/api/admin/systemhealth", "*", perms("health.view"), nil},
	{"/api/admin/sdrtrunk-health", "GET", perms("health.view"), nil},
	{"/api/admin/sdrtrunk-live", "GET", perms("health.view"), nil},
	{"/api/admin/transcription-failures", "GET", perms("health.view"), nil},
	{"/api/admin/system-health-alerts-enabled", "GET", perms("health.view"), nil},
	{"/api/admin/system-health-alert-settings", "GET", perms("health.view"), nil},
	{"/api/admin/call-audio/**", "GET", perms("health.view", "content.transcripts"), nil},
	{"/api/admin/sdrtrunk-command", "POST", perms("sdr.command"), nil},
	{"/api/admin/dispatch-alerts", "GET,POST", perms("dispatch.send"), nil},
	{"/api/admin/dispatch-alerts/recipients", "GET", perms("dispatch.send"), nil},

	{"/api/admin/transcript-review", "*", perms("content.transcripts"), nil},
	{"/api/admin/transcript-review/collector/**", "", nil, nil},
	{"/api/admin/transcript-review/collector", "", nil, nil},
	{"/api/admin/transcript-review/**", "*", perms("content.transcripts"), nil},
	{"/api/admin/hallucinations/**", "*", perms("content.transcripts"), nil},

	{"/api/call-natures", "*", perms("content.callNatures"), nil},
	{"/api/call-natures/**", "*", perms("content.callNatures"), nil},
	{"/api/call-nature-phrase-suggestions", "*", perms("content.callNatures"), nil},
	{"/api/call-nature-phrase-suggestions/**", "*", perms("content.callNatures"), nil},
	{"/api/call-nature-phrase-scan", "*", perms("content.callNatures"), nil},
}

// matchAdminPath matches "/a/*/b" (one segment per *) and a trailing "/**"
// (one or more segments).
func matchAdminPath(pattern, path string) bool {
	path = strings.TrimSuffix(path, "/")
	ps := strings.Split(strings.Trim(pattern, "/"), "/")
	xs := strings.Split(strings.Trim(path, "/"), "/")
	for i, p := range ps {
		if p == "**" {
			return len(xs) > i
		}
		if i >= len(xs) || (p != "*" && p != xs[i]) || xs[i] == "" {
			return false
		}
	}
	return len(xs) == len(ps)
}

func methodAllowed(methods, method string) bool {
	if methods == "*" {
		return true
	}
	for _, m := range strings.Split(methods, ",") {
		if m == method {
			return true
		}
	}
	return false
}

// findAdminRouteRule returns the first rule matching path and method. A rule
// with no methods matches the path for every method and grants nothing.
func findAdminRouteRule(method, path string) *adminRouteRule {
	for i := range adminRouteRules {
		rule := &adminRouteRules[i]
		if !matchAdminPath(rule.pattern, path) {
			continue
		}
		if rule.methods == "" {
			return nil
		}
		if methodAllowed(rule.methods, method) {
			return rule
		}
	}
	return nil
}

// ----- guards -----

const guardAdminTargetMessage = "your admin role can't change a system admin or another admin role holder"

func pathUserId(path string) uint64 {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 {
		return 0
	}
	id, _ := strconv.ParseUint(parts[3], 10, 64)
	return id
}

func (admin *Admin) userIsProtected(userId uint64) bool {
	if userId == 0 {
		return false
	}
	return admin.Controller.AdminRoles.HoldsAdminPower(admin.Controller.Users.GetUserById(userId))
}

func guardUserTarget(admin *Admin, r *http.Request) string {
	if admin.userIsProtected(pathUserId(r.URL.Path)) {
		return guardAdminTargetMessage
	}
	return ""
}

func guardUserEdit(admin *Admin, r *http.Request) string {
	if msg := guardUserTarget(admin, r); msg != "" {
		return msg
	}
	return guardStripPrivileged(admin, r)
}

func readJSONBody(r *http.Request) ([]byte, map[string]any) {
	if r.Body == nil {
		return nil, nil
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	return raw, body
}

func setJSONBody(r *http.Request, body map[string]any) {
	raw, _ := json.Marshal(body)
	r.Body = io.NopCloser(bytes.NewReader(raw))
	r.ContentLength = int64(len(raw))
	r.Header.Set("Content-Length", strconv.Itoa(len(raw)))
}

// guardStripPrivileged drops fields a limited session may never set.
func guardStripPrivileged(admin *Admin, r *http.Request) string {
	_, body := readJSONBody(r)
	if body == nil {
		return ""
	}
	for _, key := range []string{"systemAdmin", "pin", "regeneratePin"} {
		delete(body, key)
	}
	setJSONBody(r, body)
	return ""
}

func jsonUint(v any) uint64 {
	switch n := v.(type) {
	case float64:
		if n > 0 {
			return uint64(n)
		}
	case string:
		id, _ := strconv.ParseUint(n, 10, 64)
		return id
	}
	return 0
}

func guardBodyUserTarget(admin *Admin, r *http.Request) string {
	_, body := readJSONBody(r)
	if body != nil && admin.userIsProtected(jsonUint(body["userId"])) {
		return guardAdminTargetMessage
	}
	return ""
}

func guardTransfer(admin *Admin, r *http.Request) string {
	return guardBodyUserTarget(admin, r)
}

func guardBulkDelete(admin *Admin, r *http.Request) string {
	_, body := readJSONBody(r)
	if body == nil {
		return ""
	}
	ids, _ := body["userIds"].([]any)
	for _, id := range ids {
		if admin.userIsProtected(jsonUint(id)) {
			return guardAdminTargetMessage
		}
	}
	return ""
}

// ----- gate -----

type adminStatusRecorder struct {
	http.ResponseWriter
	status int
}

func (rec *adminStatusRecorder) WriteHeader(status int) {
	if rec.status == 0 {
		rec.status = status
	}
	rec.ResponseWriter.WriteHeader(status)
}

func (rec *adminStatusRecorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return rec.ResponseWriter.Write(b)
}

func (rec *adminStatusRecorder) Flush() {
	if f, ok := rec.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rec *adminStatusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := rec.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijack not supported")
}

// adminScrubRecorder buffers a limited session's response so PINs and
// password hashes can be removed before it leaves the server.
type adminScrubRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (rec *adminScrubRecorder) Header() http.Header { return rec.header }
func (rec *adminScrubRecorder) WriteHeader(status int) {
	if rec.status == 0 {
		rec.status = status
	}
}
func (rec *adminScrubRecorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return rec.body.Write(b)
}

var adminScrubbedKeys = map[string]bool{"pin": true, "password": true, "pinHash": true}

// adminPinTextRE matches PINs written into free text such as old log lines
// ("(PIN: ABC123)", "pin:ABC123", "invalid user pin ABC123 for ip").
var adminPinTextRE = regexp.MustCompile(`(?i)(\bpin\b[\s:=]*)([A-Za-z0-9]{4,})`)

func redactPinText(s string) string {
	return adminPinTextRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := adminPinTextRE.FindStringSubmatch(m)
		if !strings.ContainsAny(sub[2], "0123456789") && sub[2] != strings.ToUpper(sub[2]) {
			return m
		}
		return sub[1] + "[redacted]"
	})
}

func scrubAdminSecrets(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if adminScrubbedKeys[k] {
				delete(t, k)
				continue
			}
			t[k] = scrubAdminSecrets(val)
		}
	case []any:
		for i := range t {
			t[i] = scrubAdminSecrets(t[i])
		}
	case string:
		return redactPinText(t)
	}
	return v
}

func (rec *adminScrubRecorder) flushTo(w http.ResponseWriter) {
	body := rec.body.Bytes()
	if strings.Contains(rec.header.Get("Content-Type"), "json") || json.Valid(body) {
		var parsed any
		if json.Unmarshal(body, &parsed) == nil {
			if out, err := json.Marshal(scrubAdminSecrets(parsed)); err == nil {
				body = out
			}
		}
	}
	for k, v := range rec.header {
		w.Header()[k] = v
	}
	w.Header().Del("Content-Length")
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	w.WriteHeader(rec.status)
	w.Write(body)
}

func requestAdminToken(r *http.Request) string {
	if t := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); t != "" {
		return t
	}
	q := r.URL.Query()
	if t := q.Get("token"); t != "" {
		return t
	}
	return q.Get("pin")
}

func isReadOnlyMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func (admin *Admin) auditLog(actor string, r *http.Request, outcome string) {
	admin.Controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("admin audit: %s %s %s from %s -> %s", actor, r.Method, r.URL.Path, GetClientIP(r), outcome))
}

func writeAdminJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// RoleGate wraps the whole HTTP mux. Requests from limited sessions are
// checked against adminRouteRules and run with a short-lived pass in place of
// their token; changes made by any admin session are written to the audit log.
func (admin *Admin) RoleGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := requestAdminToken(r)
		if token == "" || strings.HasPrefix(token, adminPassPrefix) {
			if token != "" {
				writeAdminJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		if sess := admin.sessions.lookupLimited(token); sess != nil {
			admin.serveLimited(w, r, token, sess, next)
			return
		}

		isAdminPath := strings.HasPrefix(r.URL.Path, "/api/admin/")
		if r.URL.Path == "/api/admin/session" {
			if !admin.ValidateToken(token) {
				writeAdminJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			writeAdminJSON(w, http.StatusOK, map[string]any{
				"systemAdmin": true,
				"actor":       admin.sessions.actor(token),
				"permissions": allAdminPermissionKeys(),
			})
			return
		}

		if isAdminPath && !isReadOnlyMethod(r.Method) && r.URL.Path != "/api/admin/login" && r.URL.Path != "/api/admin/sso" && admin.ValidateToken(token) {
			rec := &adminStatusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			admin.auditLog(admin.sessions.actor(token), r, strconv.Itoa(rec.status))
			return
		}

		next.ServeHTTP(w, r)
	})
}

func allAdminPermissionKeys() []string {
	out := make([]string, 0, len(AdminPermissionCatalog))
	for _, p := range AdminPermissionCatalog {
		out = append(out, p.Key)
	}
	return out
}

func (admin *Admin) serveLimited(w http.ResponseWriter, r *http.Request, token string, sess *limitedAdminSession, next http.Handler) {
	user := admin.Controller.Users.GetUserById(sess.userId)
	role := admin.Controller.AdminRoles.RoleOf(sess.userId)
	if user == nil || user.IsSuspended() || user.SystemAdmin || role == nil || len(role.Permissions) == 0 {
		admin.sessions.closeLimited(token)
		writeAdminJSON(w, http.StatusUnauthorized, map[string]string{"error": "admin session ended"})
		return
	}
	actor := fmt.Sprintf("%s (%s)", user.Email, role.Name)

	switch r.URL.Path {
	case "/api/admin/session":
		writeAdminJSON(w, http.StatusOK, map[string]any{
			"systemAdmin": false,
			"actor":       actor,
			"role":        role.Name,
			"permissions": role.Permissions,
		})
		return
	case "/api/admin/logout":
		admin.sessions.closeLimited(token)
		admin.auditLog(actor, r, "signed out")
		w.WriteHeader(http.StatusOK)
		return
	case "/api/admin/config":
		if r.Method == http.MethodGet && !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			admin.writeLimitedConfig(w, role)
			return
		}
	}

	rule := findAdminRouteRule(r.Method, r.URL.Path)
	allowed := false
	if rule != nil {
		for _, p := range rule.perms {
			for _, have := range role.Permissions {
				if p == have {
					allowed = true
				}
			}
		}
	}
	if !allowed {
		admin.auditLog(actor, r, "denied")
		writeAdminJSON(w, http.StatusForbidden, map[string]string{"error": "your admin role doesn't allow this"})
		return
	}
	if rule.guard != nil {
		if msg := rule.guard(admin, r); msg != "" {
			admin.auditLog(actor, r, "denied: "+msg)
			writeAdminJSON(w, http.StatusForbidden, map[string]string{"error": msg})
			return
		}
	}

	pass := admin.sessions.openPass()
	admin.sessions.setActor(pass, actor)
	defer admin.sessions.closePass(pass)

	r2 := r.Clone(r.Context())
	r2.Body = r.Body
	r2.Header.Set("Authorization", pass)
	q := r2.URL.Query()
	for _, key := range []string{"token", "pin"} {
		if q.Get(key) == token {
			q.Set(key, pass)
		}
	}
	r2.URL.RawQuery = q.Encode()

	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		next.ServeHTTP(w, r2)
		return
	}

	rec := &adminScrubRecorder{header: http.Header{}}
	next.ServeHTTP(rec, r2)
	rec.flushTo(w)
	if !isReadOnlyMethod(r.Method) {
		admin.auditLog(actor, r, strconv.Itoa(rec.status))
	}
}

func (admin *Admin) writeLimitedConfig(w http.ResponseWriter, role *AdminRole) {
	full := admin.GetConfig()
	has := func(p string) bool {
		for _, have := range role.Permissions {
			if have == p {
				return true
			}
		}
		return false
	}

	config := map[string]any{
		"apikeys":              []any{},
		"branding":             full["branding"],
		"dirwatch":             []any{},
		"downstreams":          []any{},
		"groups":               full["groups"],
		"systems":              full["systems"],
		"tags":                 full["tags"],
		"users":                []any{},
		"userGroups":           []any{},
		"deviceTokens":         []any{},
		"keywordLists":         []any{},
		"userAlertPreferences": []any{},
		"version":              full["version"],
	}
	if has("users.view") || has("groups.view") {
		config["users"] = full["users"]
		config["userGroups"] = full["userGroups"]
	}
	if masked, err := admin.Controller.Options.MaskedMap(); err == nil {
		config["options"] = masked
	} else {
		config["options"] = map[string]any{}
	}

	raw, err := json.Marshal(config)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	var parsed any
	_ = json.Unmarshal(raw, &parsed)
	writeAdminJSON(w, http.StatusOK, map[string]any{
		"config":             scrubAdminSecrets(parsed),
		"passwordNeedChange": false,
		"limited":            true,
	})
}

// ----- role management (system admin only) -----

func (admin *Admin) requireFullAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !admin.ValidateToken(admin.GetAuthorization(r)) || admin.sessions.isPass(admin.GetAuthorization(r)) {
		writeAdminJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return false
	}
	return true
}

// AdminRolesHandler serves /api/admin/roles and /api/admin/roles/{id}.
func (admin *Admin) AdminRolesHandler(w http.ResponseWriter, r *http.Request) {
	if !admin.requireFullAdmin(w, r) {
		return
	}
	roles := admin.Controller.AdminRoles
	db := admin.Controller.Database

	idPart := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/roles"), "/")
	if idPart == "" {
		switch r.Method {
		case http.MethodGet:
			writeAdminJSON(w, http.StatusOK, map[string]any{"roles": roles.List(), "permissions": AdminPermissionCatalog})
		case http.MethodPost:
			var body AdminRole
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
				return
			}
			body.Id = 0
			saved, err := roles.Save(db, body)
			if err != nil {
				writeAdminJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			admin.Controller.EmitConfig()
			writeAdminJSON(w, http.StatusOK, saved)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	roleId, err := strconv.ParseUint(idPart, 10, 64)
	if err != nil || roleId == 0 {
		writeAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid role id"})
		return
	}
	switch r.Method {
	case http.MethodPut:
		var body AdminRole
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		body.Id = roleId
		saved, err := roles.Save(db, body)
		if err != nil {
			writeAdminJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		admin.Controller.EmitConfig()
		writeAdminJSON(w, http.StatusOK, saved)
	case http.MethodDelete:
		if err := roles.Delete(db, roleId); err != nil {
			writeAdminJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		admin.Controller.EmitConfig()
		writeAdminJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// UserAdminRoleHandler serves GET/PUT /api/admin/users/{id}/admin-role.
func (admin *Admin) UserAdminRoleHandler(w http.ResponseWriter, r *http.Request) {
	if !admin.requireFullAdmin(w, r) {
		return
	}
	userId := pathUserId(r.URL.Path)
	user := admin.Controller.Users.GetUserById(userId)
	if user == nil {
		writeAdminJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		var roleId uint64
		if role := admin.Controller.AdminRoles.RoleOf(userId); role != nil {
			roleId = role.Id
		}
		writeAdminJSON(w, http.StatusOK, map[string]any{"roleId": roleId, "systemAdmin": user.SystemAdmin})
	case http.MethodPut:
		var body struct {
			RoleId uint64 `json:"roleId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if err := admin.Controller.AdminRoles.SetUserRole(admin.Controller.Database, userId, body.RoleId); err != nil {
			writeAdminJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		admin.Controller.EmitConfig()
		writeAdminJSON(w, http.StatusOK, map[string]any{"roleId": body.RoleId})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
