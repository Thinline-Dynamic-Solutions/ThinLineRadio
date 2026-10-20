package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

type serverManagementTalkgroup struct {
	Id            uint64 `json:"id"`
	Label         string `json:"label"`
	Name          string `json:"name"`
	TalkgroupRef  uint   `json:"talkgroupRef"`
	AlertsEnabled bool   `json:"alertsEnabled"`
}

type serverManagementSystem struct {
	Id            uint64                      `json:"id"`
	Label         string                      `json:"label"`
	SystemRef     uint                        `json:"systemRef"`
	AlertsEnabled bool                        `json:"alertsEnabled"`
	Talkgroups    []serverManagementTalkgroup `json:"talkgroups"`
}

type serverManagementUserGroup struct {
	Id                      uint64           `json:"id"`
	Name                    string           `json:"name"`
	Description             string           `json:"description"`
	AllSystems              bool             `json:"allSystems"`
	SystemAccess            []map[string]any `json:"systemAccess"`
	Delay                   int              `json:"delay"`
	SystemDelays            map[string]uint  `json:"systemDelays"`
	TalkgroupDelays         map[string]uint  `json:"talkgroupDelays"`
	AutoEnableNewTalkgroups bool             `json:"autoEnableNewTalkgroups"`
	ConnectionLimit         uint             `json:"connectionLimit"`
}

func encodeUserGroupForMobile(g *UserGroup) serverManagementUserGroup {
	out := serverManagementUserGroup{
		Id:                      g.Id,
		Name:                    g.Name,
		Description:             g.Description,
		AllSystems:              false,
		SystemAccess:            []map[string]any{},
		Delay:                   g.Delay,
		SystemDelays:            map[string]uint{},
		TalkgroupDelays:         map[string]uint{},
		AutoEnableNewTalkgroups: g.AutoEnableNewTalkgroups,
		ConnectionLimit:         g.ConnectionLimit,
	}

	trimmed := strings.TrimSpace(g.SystemAccess)
	switch {
	case trimmed == "":
		out.AllSystems = true
	case trimmed == "[]":
		// deny-all: keep empty access list
	case g.systemAccessDataNew != nil:
		if scopes, ok := g.systemAccessDataNew.([]map[string]interface{}); ok {
			for _, scope := range scopes {
				out.SystemAccess = append(out.SystemAccess, scope)
			}
		}
	default:
		for _, id := range g.systemAccessData {
			out.SystemAccess = append(out.SystemAccess, map[string]any{
				"id":         id,
				"talkgroups": "*",
			})
		}
	}

	if g.systemDelaysMap != nil {
		for id, delay := range g.systemDelaysMap {
			if delay > 0 {
				out.SystemDelays[strconv.FormatUint(id, 10)] = delay
			}
		}
	}
	if g.talkgroupDelaysMap != nil {
		for key, delay := range g.talkgroupDelaysMap {
			if delay > 0 {
				out.TalkgroupDelays[key] = delay
			}
		}
	}
	return out
}

type serverManagementUser struct {
	Id                 uint64 `json:"id"`
	Email              string `json:"email"`
	FirstName          string `json:"firstName"`
	LastName           string `json:"lastName"`
	UserGroupId        uint64 `json:"userGroupId"`
	UserGroupName      string `json:"userGroupName"`
	SystemAdmin        bool   `json:"systemAdmin"`
	IsGroupAdmin       bool   `json:"isGroupAdmin"`
	Verified           bool   `json:"verified"`
	Suspended          bool   `json:"suspended"`
	SubscriptionStatus string `json:"subscriptionStatus"`
	PinExpiresAt       uint64 `json:"pinExpiresAt"`
	PinExpired         bool   `json:"pinExpired"`
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (admin *Admin) persistSystemsAfterPatch() error {
	admin.mutex.Lock()
	defer admin.mutex.Unlock()

	if err := admin.Controller.Systems.Write(admin.Controller.Database); err != nil {
		if readErr := admin.Controller.Systems.Read(admin.Controller.Database); readErr != nil {
			admin.Controller.Logs.LogEvent(LogLevelError, "admin.server-management.recover: "+readErr.Error())
		}
		return err
	}
	if err := admin.Controller.Systems.Read(admin.Controller.Database); err != nil {
		return err
	}
	if e := admin.Controller.IdLookupsCache.Read(admin.Controller.Database); e != nil {
		admin.Controller.Logs.LogEvent(LogLevelWarn, "failed to reload ID lookups cache: "+e.Error())
	}
	go admin.Controller.EmitConfig()
	admin.Controller.SyncConfigToFile()
	return nil
}

// ServerManagementCatalogHandler returns a PIN-free snapshot for the mobile
// Server admin screens: systems, talkgroups, users, and user groups.
//
//	GET /api/admin/server-management
func (admin *Admin) ServerManagementCatalogHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !admin.requireAdminToken(w, r) {
		return
	}

	admin.Controller.Systems.mutex.RLock()
	systemsCopy := append([]*System{}, admin.Controller.Systems.List...)
	admin.Controller.Systems.mutex.RUnlock()

	sort.Slice(systemsCopy, func(i, j int) bool {
		if systemsCopy[i].Order != systemsCopy[j].Order {
			return systemsCopy[i].Order < systemsCopy[j].Order
		}
		return strings.ToLower(systemsCopy[i].Label) < strings.ToLower(systemsCopy[j].Label)
	})

	outSystems := make([]serverManagementSystem, 0, len(systemsCopy))
	for _, sys := range systemsCopy {
		item := serverManagementSystem{
			Id:            sys.Id,
			Label:         sys.Label,
			SystemRef:     sys.SystemRef,
			AlertsEnabled: sys.AlertsEnabled,
			Talkgroups:    []serverManagementTalkgroup{},
		}
		if sys.Talkgroups != nil {
			sys.Talkgroups.mutex.Lock()
			tgs := append([]*Talkgroup{}, sys.Talkgroups.List...)
			sys.Talkgroups.mutex.Unlock()
			sort.Slice(tgs, func(i, j int) bool {
				if tgs[i].Order != tgs[j].Order {
					return tgs[i].Order < tgs[j].Order
				}
				return strings.ToLower(tgs[i].Label) < strings.ToLower(tgs[j].Label)
			})
			for _, tg := range tgs {
				item.Talkgroups = append(item.Talkgroups, serverManagementTalkgroup{
					Id:            tg.Id,
					Label:         tg.Label,
					Name:          tg.Name,
					TalkgroupRef:  tg.TalkgroupRef,
					AlertsEnabled: tg.AlertsEnabled,
				})
			}
		}
		outSystems = append(outSystems, item)
	}

	groups := admin.Controller.UserGroups.GetAll()
	sort.Slice(groups, func(i, j int) bool {
		return strings.ToLower(groups[i].Name) < strings.ToLower(groups[j].Name)
	})
	outGroups := make([]serverManagementUserGroup, 0, len(groups))
	for _, g := range groups {
		outGroups = append(outGroups, encodeUserGroupForMobile(g))
	}

	users := admin.Controller.Users.GetAllUsers()
	sort.Slice(users, func(i, j int) bool {
		return strings.ToLower(users[i].Email) < strings.ToLower(users[j].Email)
	})
	outUsers := make([]serverManagementUser, 0, len(users))
	for _, user := range users {
		groupName := ""
		if user.UserGroupId > 0 {
			if g := admin.Controller.UserGroups.Get(user.UserGroupId); g != nil {
				groupName = g.Name
			}
		}
		outUsers = append(outUsers, serverManagementUser{
			Id:                 user.Id,
			Email:              user.Email,
			FirstName:          user.FirstName,
			LastName:           user.LastName,
			UserGroupId:        user.UserGroupId,
			UserGroupName:      groupName,
			SystemAdmin:        user.SystemAdmin,
			IsGroupAdmin:       user.IsGroupAdmin,
			Verified:           user.Verified,
			Suspended:          user.Suspended,
			SubscriptionStatus: user.SubscriptionStatus,
			PinExpiresAt:       user.PinExpiresAt,
			PinExpired:         user.PinExpired(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"systems":    outSystems,
		"userGroups": outGroups,
		"users":      outUsers,
	})
}

// ServerManagementSystemHandler merges label / alertsEnabled onto one system
// or talkgroup without replacing the rest of the system.
//
//	PATCH /api/admin/systems/{id}
//	PATCH /api/admin/systems/{id}/talkgroups/{tgId}
func (admin *Admin) ServerManagementSystemHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !admin.requireAdminToken(w, r) {
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// api / admin / systems / {id} [/ talkgroups / {tgId}]
	if len(parts) < 4 {
		writeJSONError(w, http.StatusBadRequest, "invalid path")
		return
	}
	systemID, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid system id")
		return
	}

	if len(parts) >= 6 && parts[4] == "talkgroups" {
		tgID, err := strconv.ParseUint(parts[5], 10, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid talkgroup id")
			return
		}
		admin.patchTalkgroup(w, r, systemID, tgID)
		return
	}
	if len(parts) != 4 {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	admin.patchSystem(w, r, systemID)
}

func (admin *Admin) patchSystem(w http.ResponseWriter, r *http.Request, systemID uint64) {
	var body struct {
		Label         *string `json:"label"`
		AlertsEnabled *bool   `json:"alertsEnabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Label == nil && body.AlertsEnabled == nil {
		writeJSONError(w, http.StatusBadRequest, "no fields to update")
		return
	}

	system, ok := admin.Controller.Systems.GetSystemById(systemID)
	if !ok || system == nil {
		writeJSONError(w, http.StatusNotFound, "system not found")
		return
	}
	if body.Label != nil {
		label := strings.TrimSpace(*body.Label)
		if label == "" {
			writeJSONError(w, http.StatusBadRequest, "label is required")
			return
		}
		system.Label = label
	}
	if body.AlertsEnabled != nil {
		system.AlertsEnabled = *body.AlertsEnabled
	}

	if err := admin.persistSystemsAfterPatch(); err != nil {
		admin.Controller.Logs.LogEvent(LogLevelError, "admin.server-management.system: "+err.Error())
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	updated, _ := admin.Controller.Systems.GetSystemById(systemID)
	if updated == nil {
		updated = system
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":            updated.Id,
		"label":         updated.Label,
		"systemRef":     updated.SystemRef,
		"alertsEnabled": updated.AlertsEnabled,
	})
}

func (admin *Admin) patchTalkgroup(w http.ResponseWriter, r *http.Request, systemID, talkgroupID uint64) {
	var body struct {
		Label         *string `json:"label"`
		Name          *string `json:"name"`
		AlertsEnabled *bool   `json:"alertsEnabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Label == nil && body.Name == nil && body.AlertsEnabled == nil {
		writeJSONError(w, http.StatusBadRequest, "no fields to update")
		return
	}

	system, ok := admin.Controller.Systems.GetSystemById(systemID)
	if !ok || system == nil || system.Talkgroups == nil {
		writeJSONError(w, http.StatusNotFound, "system not found")
		return
	}
	tg, ok := system.Talkgroups.GetTalkgroupById(talkgroupID)
	if !ok || tg == nil {
		writeJSONError(w, http.StatusNotFound, "talkgroup not found")
		return
	}
	if body.Label != nil {
		label := strings.TrimSpace(*body.Label)
		if label == "" {
			writeJSONError(w, http.StatusBadRequest, "label is required")
			return
		}
		tg.Label = label
	}
	if body.Name != nil {
		tg.Name = strings.TrimSpace(*body.Name)
	}
	if body.AlertsEnabled != nil {
		tg.AlertsEnabled = *body.AlertsEnabled
	}

	if err := admin.persistSystemsAfterPatch(); err != nil {
		admin.Controller.Logs.LogEvent(LogLevelError, "admin.server-management.talkgroup: "+err.Error())
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":            tg.Id,
		"label":         tg.Label,
		"name":          tg.Name,
		"talkgroupRef":  tg.TalkgroupRef,
		"alertsEnabled": tg.AlertsEnabled,
	})
}

// UserMergePatchHandler updates selected user fields without replacing systems or billing.
//
//	PATCH /api/admin/users/{id}
//	PATCH /api/admin/users/{id}/group
func (admin *Admin) UserMergePatchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !admin.requireAdminToken(w, r) {
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		writeJSONError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	userID, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid user id format")
		return
	}

	var body struct {
		Email              *string `json:"email"`
		FirstName          *string `json:"firstName"`
		LastName           *string `json:"lastName"`
		UserGroupId        *uint64 `json:"userGroupId"`
		IsGroupAdmin       *bool   `json:"isGroupAdmin"`
		SystemAdmin        *bool   `json:"systemAdmin"`
		Verified           *bool   `json:"verified"`
		Suspended          *bool   `json:"suspended"`
		SubscriptionStatus *string `json:"subscriptionStatus"`
		PinExpiresAt       *uint64 `json:"pinExpiresAt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Email == nil && body.FirstName == nil && body.LastName == nil &&
		body.UserGroupId == nil && body.IsGroupAdmin == nil && body.SystemAdmin == nil &&
		body.Verified == nil && body.Suspended == nil &&
		body.SubscriptionStatus == nil && body.PinExpiresAt == nil {
		writeJSONError(w, http.StatusBadRequest, "no fields to update")
		return
	}

	user := admin.Controller.Users.GetUserById(userID)
	if user == nil {
		writeJSONError(w, http.StatusNotFound, "user not found")
		return
	}

	if body.Email != nil {
		if err := ValidateEmail(*body.Email); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		email := NormalizeEmail(*body.Email)
		if existing := admin.Controller.Users.GetUserByEmail(email); existing != nil && existing.Id != user.Id {
			writeJSONError(w, http.StatusConflict, "email already in use")
			return
		}
		user.Email = email
	}
	if body.FirstName != nil {
		user.FirstName = strings.TrimSpace(*body.FirstName)
	}
	if body.LastName != nil {
		user.LastName = strings.TrimSpace(*body.LastName)
	}

	oldGroupID := user.UserGroupId
	if body.UserGroupId != nil {
		newGroupID := *body.UserGroupId
		if newGroupID != 0 && admin.Controller.UserGroups.Get(newGroupID) == nil {
			writeJSONError(w, http.StatusBadRequest, "invalid user group id")
			return
		}
		user.UserGroupId = newGroupID
		if user.IsGroupAdmin && oldGroupID != newGroupID {
			user.IsGroupAdmin = false
		}
	}

	effectiveGroupID := user.UserGroupId
	if body.IsGroupAdmin != nil {
		if *body.IsGroupAdmin && effectiveGroupID == 0 {
			writeJSONError(w, http.StatusBadRequest, "user must be in a group to be a group admin")
			return
		}
		user.IsGroupAdmin = *body.IsGroupAdmin
	}

	if body.SystemAdmin != nil {
		if user.SystemAdmin && !*body.SystemAdmin && admin.wouldLeaveNoSystemAdmin(user.Id) {
			writeJSONError(w, http.StatusBadRequest, "cannot remove the last system administrator")
			return
		}
		user.SystemAdmin = *body.SystemAdmin
	}
	if body.Verified != nil {
		user.Verified = *body.Verified
	}
	wasSuspended := user.Suspended
	if body.Suspended != nil {
		user.Suspended = *body.Suspended
	}
	if body.SubscriptionStatus != nil {
		status := strings.ToLower(strings.TrimSpace(*body.SubscriptionStatus))
		switch status {
		case "", "active", "trialing", "incomplete", "incomplete_expired", "past_due", "canceled", "cancelled", "unpaid":
			if status == "cancelled" {
				status = "canceled"
			}
			user.SubscriptionStatus = status
		default:
			writeJSONError(w, http.StatusBadRequest, "invalid subscription status")
			return
		}
	}
	if body.PinExpiresAt != nil {
		user.PinExpiresAt = *body.PinExpiresAt
	}

	if err := admin.Controller.Users.Update(user); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to update user")
		return
	}
	if err := admin.Controller.Users.Write(admin.Controller.Database); err != nil {
		admin.Controller.Logs.LogEvent(LogLevelError, "admin.server-management.user: "+err.Error())
		writeJSONError(w, http.StatusInternalServerError, "failed to update user")
		return
	}
	if !wasSuspended && user.Suspended {
		admin.Controller.DisconnectUserClients(user.Id, "Account suspended")
	}
	admin.Controller.SyncConfigToFile()

	groupName := ""
	if user.UserGroupId > 0 {
		if g := admin.Controller.UserGroups.Get(user.UserGroupId); g != nil {
			groupName = g.Name
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":                 user.Id,
		"email":              user.Email,
		"firstName":          user.FirstName,
		"lastName":           user.LastName,
		"userGroupId":        user.UserGroupId,
		"userGroupName":      groupName,
		"isGroupAdmin":       user.IsGroupAdmin,
		"systemAdmin":        user.SystemAdmin,
		"verified":           user.Verified,
		"suspended":          user.Suspended,
		"subscriptionStatus": user.SubscriptionStatus,
		"pinExpiresAt":       user.PinExpiresAt,
		"pinExpired":         user.PinExpired(),
	})
}

func marshalDelayMap(delays map[string]uint) string {
	if len(delays) == 0 {
		return ""
	}
	clean := make(map[string]uint, len(delays))
	for key, delay := range delays {
		key = strings.TrimSpace(key)
		if key == "" || delay == 0 {
			continue
		}
		clean[key] = delay
	}
	if len(clean) == 0 {
		return ""
	}
	encoded, err := json.Marshal(clean)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func validateSystemAccessEntries(entries []map[string]any) error {
	for i, entry := range entries {
		idVal, ok := entry["id"]
		if !ok {
			return fmt.Errorf("systemAccess[%d] missing id", i)
		}
		switch idVal.(type) {
		case float64, json.Number, int, int64, uint64, uint:
		case string:
			if _, err := strconv.ParseUint(idVal.(string), 10, 64); err != nil {
				return fmt.Errorf("systemAccess[%d] has invalid id", i)
			}
		default:
			return fmt.Errorf("systemAccess[%d] has invalid id", i)
		}
	}
	return nil
}

// UserGroupMergePatchHandler updates group name, talkgroup access, delays and
// the per-user connection limit without replacing billing or other fields.
//
//	PATCH /api/admin/user-groups/{id}
func (admin *Admin) UserGroupMergePatchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !admin.requireAdminToken(w, r) {
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 {
		writeJSONError(w, http.StatusBadRequest, "invalid group id")
		return
	}
	groupID, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid group id format")
		return
	}

	var body struct {
		Name                    *string           `json:"name"`
		Description             *string           `json:"description"`
		AllSystems              *bool             `json:"allSystems"`
		SystemAccess            *[]map[string]any `json:"systemAccess"`
		Delay                   *int              `json:"delay"`
		SystemDelays            *map[string]uint  `json:"systemDelays"`
		TalkgroupDelays         *map[string]uint  `json:"talkgroupDelays"`
		AutoEnableNewTalkgroups *bool             `json:"autoEnableNewTalkgroups"`
		ConnectionLimit         *int              `json:"connectionLimit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == nil && body.Description == nil && body.AllSystems == nil &&
		body.SystemAccess == nil && body.Delay == nil && body.SystemDelays == nil &&
		body.TalkgroupDelays == nil && body.AutoEnableNewTalkgroups == nil &&
		body.ConnectionLimit == nil {
		writeJSONError(w, http.StatusBadRequest, "no fields to update")
		return
	}

	group := admin.Controller.UserGroups.Get(groupID)
	if group == nil {
		writeJSONError(w, http.StatusNotFound, "group not found")
		return
	}

	if body.Name != nil {
		name := strings.TrimSpace(*body.Name)
		if name == "" {
			writeJSONError(w, http.StatusBadRequest, "name is required")
			return
		}
		group.Name = name
	}
	if body.Description != nil {
		group.Description = strings.TrimSpace(*body.Description)
	}
	if body.Delay != nil {
		if *body.Delay < 0 {
			writeJSONError(w, http.StatusBadRequest, "delay cannot be negative")
			return
		}
		group.Delay = *body.Delay
	}
	if body.SystemDelays != nil {
		group.SystemDelays = marshalDelayMap(*body.SystemDelays)
	}
	if body.TalkgroupDelays != nil {
		group.TalkgroupDelays = marshalDelayMap(*body.TalkgroupDelays)
	}
	if body.AutoEnableNewTalkgroups != nil {
		group.AutoEnableNewTalkgroups = *body.AutoEnableNewTalkgroups
	}
	if body.ConnectionLimit != nil {
		if *body.ConnectionLimit < 0 {
			writeJSONError(w, http.StatusBadRequest, "connectionLimit cannot be negative")
			return
		}
		group.ConnectionLimit = uint(*body.ConnectionLimit)
	}

	accessChanged := false
	if body.AllSystems != nil && *body.AllSystems {
		group.SystemAccess = ""
		accessChanged = true
	} else if body.SystemAccess != nil {
		entries := *body.SystemAccess
		if err := validateSystemAccessEntries(entries); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if len(entries) == 0 {
			group.SystemAccess = "[]"
		} else {
			encoded, err := json.Marshal(entries)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid systemAccess")
				return
			}
			group.SystemAccess = string(encoded)
		}
		accessChanged = true
	} else if body.AllSystems != nil && !*body.AllSystems && strings.TrimSpace(group.SystemAccess) == "" {
		group.SystemAccess = "[]"
		accessChanged = true
	}

	if accessChanged || body.AutoEnableNewTalkgroups != nil {
		group.NormalizeSystemAccess(admin.Controller.Systems)
	}

	if err := admin.Controller.UserGroups.Update(group, admin.Controller.Database); err != nil {
		admin.Controller.Logs.LogEvent(LogLevelError, "admin.server-management.group: "+err.Error())
		writeJSONError(w, http.StatusInternalServerError, "failed to update group")
		return
	}
	if err := admin.Controller.UserGroups.Load(admin.Controller.Database); err != nil {
		admin.Controller.Logs.LogEvent(LogLevelWarn, "admin.server-management.group.reload: "+err.Error())
	}
	if updated := admin.Controller.UserGroups.Get(groupID); updated != nil {
		group = updated
	}

	admin.Controller.Clients.RefreshConfigForGroup(admin.Controller, group.Id)
	admin.Controller.SyncConfigToFile()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(encodeUserGroupForMobile(group))
}
