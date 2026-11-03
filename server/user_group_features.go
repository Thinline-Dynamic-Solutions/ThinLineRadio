// Copyright (C) 2026 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"encoding/json"
	"strings"
)

// UserGroupFeatures gates listener UI surfaces and matching APIs per group.
// Existing groups default to all-on; free groups typically enable liveFeed only.
type UserGroupFeatures struct {
	LiveFeed       bool `json:"liveFeed"`
	Replay         bool `json:"replay"`
	Channels       bool `json:"channels"`
	Alerts         bool `json:"alerts"`
	Transcripts    bool `json:"transcripts"`
	Stats          bool `json:"stats"`
	Map            bool `json:"map"`
	Settings       bool `json:"settings"`
	AlertsSideRail bool `json:"alertsSideRail"`
}

func AllFeaturesOn() UserGroupFeatures {
	return UserGroupFeatures{
		LiveFeed:       true,
		Replay:         true,
		Channels:       true,
		Alerts:         true,
		Transcripts:    true,
		Stats:          true,
		Map:            true,
		Settings:       true,
		AlertsSideRail: true,
	}
}

func FreeGroupDefaultFeatures() UserGroupFeatures {
	return UserGroupFeatures{LiveFeed: true}
}

func MergeFeatures(a, b UserGroupFeatures) UserGroupFeatures {
	return UserGroupFeatures{
		LiveFeed:       a.LiveFeed || b.LiveFeed,
		Replay:         a.Replay || b.Replay,
		Channels:       a.Channels || b.Channels,
		Alerts:         a.Alerts || b.Alerts,
		Transcripts:    a.Transcripts || b.Transcripts,
		Stats:          a.Stats || b.Stats,
		Map:            a.Map || b.Map,
		Settings:       a.Settings || b.Settings,
		AlertsSideRail: a.AlertsSideRail || b.AlertsSideRail,
	}
}

func (f UserGroupFeatures) Has(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "livefeed", "live_feed", "live":
		return f.LiveFeed
	case "replay", "archive", "history":
		return f.Replay
	case "channels", "channel":
		return f.Channels
	case "alerts", "alert":
		return f.Alerts
	case "transcripts", "transcript":
		return f.Transcripts
	case "stats", "statistics":
		return f.Stats
	case "map", "incidentmap", "incidents":
		return f.Map
	case "settings", "setting":
		return f.Settings
	case "alertssiderail", "alerts_side_rail", "siderail", "side_rail":
		return f.AlertsSideRail
	default:
		return false
	}
}

func (f UserGroupFeatures) ToJSON() string {
	raw, err := json.Marshal(f)
	if err != nil {
		return ""
	}
	return string(raw)
}

func parseUserGroupFeatures(raw string) UserGroupFeatures {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		// Empty = legacy group: preserve full access.
		return AllFeaturesOn()
	}
	var f UserGroupFeatures
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		return AllFeaturesOn()
	}
	return f
}

func (ug *UserGroup) loadFeatures() {
	if ug == nil {
		return
	}
	ug.featuresData = parseUserGroupFeatures(ug.Features)
}

func (ug *UserGroup) GetFeatures() UserGroupFeatures {
	if ug == nil {
		return AllFeaturesOn()
	}
	if strings.TrimSpace(ug.Features) == "" {
		return AllFeaturesOn()
	}
	return ug.featuresData
}

func (ugs *UserGroups) GetFreeGroups() []*UserGroup {
	if ugs == nil {
		return nil
	}
	ugs.mutex.RLock()
	defer ugs.mutex.RUnlock()
	out := []*UserGroup{}
	for _, g := range ugs.groups {
		if g != nil && g.IsFreeGroup {
			out = append(out, g)
		}
	}
	return out
}

func (ugs *UserGroups) HasFreeGroup() bool {
	return len(ugs.GetFreeGroups()) > 0
}

func (ugs *UserGroups) HasPublicPaidRegistrationTier() bool {
	if ugs == nil {
		return false
	}
	for _, g := range ugs.GetPublicRegistrationGroups() {
		if g != nil && g.BillingEnabled && len(g.GetPricingOptions()) > 0 {
			return true
		}
	}
	return false
}

func (controller *Controller) allowAnonymousFreeListening() bool {
	return controller != nil && controller.UserGroups != nil && controller.UserGroups.HasFreeGroup()
}

func (controller *Controller) featuresForAnonymous() UserGroupFeatures {
	if controller == nil || controller.UserGroups == nil {
		return UserGroupFeatures{}
	}
	free := controller.UserGroups.GetFreeGroups()
	if len(free) == 0 {
		return UserGroupFeatures{}
	}
	merged := UserGroupFeatures{}
	for _, g := range free {
		merged = MergeFeatures(merged, g.GetFeatures())
	}
	return merged
}

func (controller *Controller) featuresForUser(user *User) UserGroupFeatures {
	if controller == nil {
		return AllFeaturesOn()
	}
	if user == nil {
		if controller.allowAnonymousFreeListening() {
			return controller.featuresForAnonymous()
		}
		if controller.requiresUserAuth() {
			return UserGroupFeatures{}
		}
		return AllFeaturesOn()
	}
	groups := controller.userGroups(user)
	if len(groups) == 0 {
		return AllFeaturesOn()
	}
	merged := UserGroupFeatures{}
	for _, g := range groups {
		merged = MergeFeatures(merged, g.GetFeatures())
	}
	return merged
}

func (controller *Controller) featuresForClient(client *Client) UserGroupFeatures {
	if client == nil {
		return UserGroupFeatures{}
	}
	return controller.featuresForUser(client.User)
}

func (controller *Controller) clientHasFeature(client *Client, name string) bool {
	return controller.featuresForClient(client).Has(name)
}

func (controller *Controller) anonymousHasCallAccess(call *Call) bool {
	if controller == nil || call == nil || call.System == nil {
		return false
	}
	free := controller.UserGroups.GetFreeGroups()
	if len(free) == 0 {
		return false
	}
	sysRef := uint64(call.System.SystemRef)
	for _, g := range free {
		if g == nil || !g.HasSystemAccess(sysRef) {
			continue
		}
		if call.Talkgroup == nil || g.HasTalkgroupAccess(sysRef, call.Talkgroup.TalkgroupRef) {
			return true
		}
	}
	return false
}
