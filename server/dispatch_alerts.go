// Copyright (C) 2026 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Dispatch alerts are admin-sent notices to chosen users and user groups. They
// ignore the recipients' channel and alert settings, play the dispatch tone,
// and are stored as "dispatch" system alerts that only recipients can see.

const (
	dispatchAlertType         = "dispatch"
	defaultDispatchAlertSound = "five_beep.wav"
	defaultDispatchLabel      = "Dispatch Alert"
	dispatchMaxTitle          = 120
	dispatchMaxMessage        = 2000
	dispatchMaxLabel          = 40
)

// dispatchAlertSounds are bundled in the web client and in both mobile apps.
var dispatchAlertSounds = []string{
	"five_beep.wav", "smoke_alarm.wav", "alert.wav", "chirp_long.wav", "classic.wav", "ding.wav",
	"door_bell.wav", "double_pulse.wav", "fast_beep_long.wav", "fast_beep_short.wav", "modern.wav",
	"pluck.wav", "pop.wav", "quick_beep.wav", "relaxed.wav", "settle_alert.wav", "simple.wav",
	"startup.wav", "tone.wav",
}

func isDispatchAlertSound(s string) bool {
	for _, known := range dispatchAlertSounds {
		if s == known {
			return true
		}
	}
	return false
}

func (controller *Controller) dispatchAlertSound() string {
	if s := controller.Options.DispatchAlertSound; isDispatchAlertSound(s) {
		return s
	}
	return defaultDispatchAlertSound
}

type dispatchAlertRequest struct {
	Title    string   `json:"title"`
	Message  string   `json:"message"`
	Label    string   `json:"label"`
	Severity string   `json:"severity"`
	UserIds  []uint64 `json:"userIds"`
	GroupIds []uint64 `json:"groupIds"`
}

// resolveDispatchRecipients returns the verified, active users chosen directly
// or through one of the chosen user groups.
func (controller *Controller) resolveDispatchRecipients(userIds, groupIds []uint64) []*User {
	wantUser := map[uint64]bool{}
	for _, id := range userIds {
		wantUser[id] = true
	}
	wantGroup := map[uint64]bool{}
	for _, id := range groupIds {
		wantGroup[id] = true
	}
	out := []*User{}
	for _, u := range controller.Users.GetAllUsers() {
		if u == nil || !u.Verified || u.IsSuspended() {
			continue
		}
		if wantUser[u.Id] {
			out = append(out, u)
			continue
		}
		for _, g := range controller.userGroups(u) {
			if g != nil && wantGroup[g.Id] {
				out = append(out, u)
				break
			}
		}
	}
	return out
}

func dispatchUserName(u *User) string {
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if name == "" {
		return u.Email
	}
	return name
}

// sendDispatchAlert stores the alert and pushes it to every recipient device.
// It returns the alert id and how many devices were notified.
func (controller *Controller) sendDispatchAlert(req dispatchAlertRequest, recipients []*User, sentBy string, createdBy uint64) (uint64, int, error) {
	ids := make([]uint64, 0, len(recipients))
	for _, u := range recipients {
		ids = append(ids, u.Id)
	}
	sound := controller.dispatchAlertSound()
	data := &SystemAlertData{
		Label:          req.Label,
		RecipientIds:   ids,
		GroupIds:       req.GroupIds,
		SentBy:         sentBy,
		RecipientCount: len(ids),
		Sound:          sound,
	}

	devices := map[string][]string{}
	total := 0
	for _, u := range recipients {
		for _, device := range controller.DeviceTokens.GetByUser(u.Id) {
			if isLegacyOneSignalToken(device) || device.FCMToken == "" {
				continue
			}
			platform := device.Platform
			if platform == "" {
				platform = "android"
			}
			devices[platform] = append(devices[platform], device.FCMToken)
			total++
		}
	}
	data.DeviceCount = total

	raw, err := json.Marshal(data)
	if err != nil {
		return 0, 0, err
	}
	var createdByArg any
	if createdBy > 0 {
		createdByArg = createdBy
	}
	var alertId uint64
	if err := controller.Database.Sql.QueryRow(
		`INSERT INTO "systemAlerts" ("alertType", "severity", "title", "message", "data", "createdAt", "createdBy") VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING "alertId"`,
		dispatchAlertType, req.Severity, req.Title, req.Message, string(raw), time.Now().UnixMilli(), createdByArg,
	).Scan(&alertId); err != nil {
		return 0, 0, fmt.Errorf("failed to save dispatch alert: %w", err)
	}

	heading := "🚨 " + req.Label
	extra := map[string]interface{}{"dispatch_alert": "true", "alertId": fmt.Sprintf("%d", alertId), "notification_sound": sound}
	for platform, tokens := range devices {
		platformSound := sound
		if platform == "ios" {
			platformSound = strings.TrimSuffix(platformSound, ".wav")
		}
		go controller.sendNotificationBatch(tokens, heading, req.Title, req.Message, platform, platformSound, nil, "", "", extra)
	}
	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("dispatch alert %d %q sent by %s to %d user(s), %d device(s)", alertId, req.Title, sentBy, len(ids), total))
	return alertId, total, nil
}

func dispatchAlertHistoryJSON(alert *SystemAlert, groupNames map[uint64]string) map[string]any {
	out := map[string]any{
		"id":        alert.Id,
		"title":     alert.Title,
		"message":   alert.Message,
		"severity":  alert.Severity,
		"createdAt": alert.CreatedAt,
	}
	if data := parseSystemAlertData(alert); data != nil {
		out["label"] = data.Label
		out["sentBy"] = data.SentBy
		out["recipientCount"] = data.RecipientCount
		out["deviceCount"] = data.DeviceCount
		groups := []string{}
		for _, id := range data.GroupIds {
			if name := groupNames[id]; name != "" {
				groups = append(groups, name)
			}
		}
		out["groups"] = groups
	}
	return out
}

// DispatchAlertsHandler serves:
//
//	GET  /api/admin/dispatch-alerts             recent dispatch alerts and the dispatch tone
//	POST /api/admin/dispatch-alerts             send one
//	GET  /api/admin/dispatch-alerts/recipients  users and groups that can be picked
//	PUT  /api/admin/dispatch-alerts/settings    {"sound": "five_beep.wav"} (system admins only)
func (admin *Admin) DispatchAlertsHandler(w http.ResponseWriter, r *http.Request) {
	token := admin.GetAuthorization(r)
	if !admin.ValidateToken(token) {
		writeAdminJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	controller := admin.Controller
	sub := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/dispatch-alerts"), "/")
	fail := func(status int, msg string) {
		writeAdminJSON(w, status, map[string]string{"error": msg})
	}

	switch {
	case sub == "" && r.Method == http.MethodGet:
		rows, err := controller.Database.Sql.Query(`SELECT "alertId", "alertType", "severity", "title", "message", "data", "createdAt", COALESCE("createdBy", 0), "dismissed" FROM "systemAlerts" WHERE "alertType" = $1 ORDER BY "createdAt" DESC LIMIT 50`, dispatchAlertType)
		if err != nil {
			fail(http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		groupNames := map[uint64]string{}
		for _, g := range controller.UserGroups.GetAll() {
			groupNames[g.Id] = g.Name
		}
		history := []map[string]any{}
		for rows.Next() {
			a := &SystemAlert{}
			if rows.Scan(&a.Id, &a.AlertType, &a.Severity, &a.Title, &a.Message, &a.Data, &a.CreatedAt, &a.CreatedBy, &a.Dismissed) == nil {
				history = append(history, dispatchAlertHistoryJSON(a, groupNames))
			}
		}
		writeAdminJSON(w, http.StatusOK, map[string]any{
			"history":      history,
			"sound":        controller.dispatchAlertSound(),
			"sounds":       dispatchAlertSounds,
			"canEditSound": !admin.sessions.isPass(token),
		})

	case sub == "recipients" && r.Method == http.MethodGet:
		type groupOut struct {
			Id      uint64 `json:"id"`
			Name    string `json:"name"`
			Members int    `json:"members"`
		}
		type userOut struct {
			Id       uint64   `json:"id"`
			Name     string   `json:"name"`
			Email    string   `json:"email"`
			GroupIds []uint64 `json:"groupIds"`
			Devices  int      `json:"devices"`
		}
		counts := map[uint64]int{}
		users := []userOut{}
		for _, u := range controller.Users.GetAllUsers() {
			if u == nil || !u.Verified || u.IsSuspended() {
				continue
			}
			gids := []uint64{}
			for _, g := range controller.userGroups(u) {
				if g != nil {
					gids = append(gids, g.Id)
					counts[g.Id]++
				}
			}
			users = append(users, userOut{Id: u.Id, Name: dispatchUserName(u), Email: u.Email, GroupIds: gids, Devices: len(controller.DeviceTokens.GetByUser(u.Id))})
		}
		groups := []groupOut{}
		for _, g := range controller.UserGroups.GetAll() {
			groups = append(groups, groupOut{Id: g.Id, Name: g.Name, Members: counts[g.Id]})
		}
		writeAdminJSON(w, http.StatusOK, map[string]any{"users": users, "groups": groups})

	case sub == "" && r.Method == http.MethodPost:
		var req dispatchAlertRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			fail(http.StatusBadRequest, "invalid request")
			return
		}
		req.Message = truncateRunes(strings.TrimSpace(req.Message), dispatchMaxMessage)
		req.Label = truncateRunes(strings.TrimSpace(req.Label), dispatchMaxLabel)
		req.Title = truncateRunes(strings.TrimSpace(req.Title), dispatchMaxTitle)
		if req.Message == "" {
			fail(http.StatusBadRequest, "Write a message")
			return
		}
		if req.Label == "" {
			req.Label = defaultDispatchLabel
		}
		if req.Title == "" {
			req.Title = req.Label
		}
		severity, ok := normalizeServerAlertSeverity(req.Severity)
		if !ok || req.Severity == "" {
			severity = "critical"
		}
		req.Severity = severity
		if len(req.UserIds) == 0 && len(req.GroupIds) == 0 {
			fail(http.StatusBadRequest, "Pick at least one user or group")
			return
		}
		recipients := controller.resolveDispatchRecipients(req.UserIds, req.GroupIds)
		if len(recipients) == 0 {
			fail(http.StatusBadRequest, "None of the chosen users can receive alerts: they're unverified, suspended, or the groups are empty")
			return
		}
		createdBy := uint64(0)
		if session := admin.sessions.lookupLimited(token); session != nil {
			createdBy = session.userId
		}
		id, devices, err := controller.sendDispatchAlert(req, recipients, admin.sessions.actor(token), createdBy)
		if err != nil {
			fail(http.StatusInternalServerError, err.Error())
			return
		}
		writeAdminJSON(w, http.StatusOK, map[string]any{"id": id, "recipients": len(recipients), "devices": devices})

	case sub == "settings" && r.Method == http.MethodPut:
		if admin.sessions.isPass(token) {
			fail(http.StatusForbidden, "only system admins can change the dispatch tone")
			return
		}
		var req struct {
			Sound string `json:"sound"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || !isDispatchAlertSound(req.Sound) {
			fail(http.StatusBadRequest, "Pick one of the listed tones")
			return
		}
		if err := controller.Options.WriteKey(controller.Database, "dispatchAlertSound", req.Sound, func() {
			controller.Options.DispatchAlertSound = req.Sound
		}); err != nil {
			fail(http.StatusInternalServerError, err.Error())
			return
		}
		writeAdminJSON(w, http.StatusOK, map[string]any{"sound": req.Sound})

	default:
		fail(http.StatusNotFound, "not found")
	}
}

// userIsDispatchRecipient reports whether a dispatch alert was sent to the user.
func userIsDispatchRecipient(user *User, data *SystemAlertData) bool {
	if user == nil || data == nil {
		return false
	}
	for _, id := range data.RecipientIds {
		if id == user.Id {
			return true
		}
	}
	return false
}

// listenerDispatchAlert strips recipient lists and the sender from a dispatch
// alert before it goes to a listener.
func listenerDispatchAlert(alert *SystemAlert) *SystemAlert {
	out := *alert
	label := defaultDispatchLabel
	if data := parseSystemAlertData(alert); data != nil && data.Label != "" {
		label = data.Label
	}
	raw, _ := json.Marshal(map[string]string{"label": label})
	out.Data = string(raw)
	return &out
}
