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

// linkOf is the share link a signed-out client connected with, if any.
func linkOf(client *Client) *ListenLink {
	if client == nil || client.User != nil {
		return nil
	}
	return client.ListenLink
}

var listenLinkDenyAll = func() *UserGroup {
	g := &UserGroup{SystemAccess: "[]", Features: UserGroupFeatures{}.ToJSON()}
	g.loadSystemAccess()
	g.loadFeatures()
	return g
}()

// anonymousScope is what a signed-out listener may hear: the share link they
// opened, otherwise the server's free groups. A link that was turned off or
// expired while connected grants nothing.
func (controller *Controller) anonymousScope(link *ListenLink) []*UserGroup {
	if link != nil {
		if link.Active() && link.scope != nil {
			return []*UserGroup{link.scope}
		}
		return []*UserGroup{listenLinkDenyAll}
	}
	if controller == nil || controller.UserGroups == nil {
		return nil
	}
	return controller.UserGroups.GetFreeGroups()
}

// anonymousAllowed reports whether a signed-out listener may listen at all.
func (controller *Controller) anonymousAllowed(link *ListenLink) bool {
	if link != nil {
		return link.Active()
	}
	return controller.allowAnonymousFreeListening()
}

// scopeRestricted reports whether a client's calls must be checked against its
// scope. Share-link visitors are limited to the link even on open servers.
func (controller *Controller) scopeRestricted(client *Client) bool {
	return controller.requiresUserAuth() || linkOf(client) != nil
}

// featuresForAnonymous is the union of the anonymous scope's features. Alerts,
// transcripts, stats and the map are served only to signed-in users, so they
// stay off here and the client shows them as locked.
func (controller *Controller) featuresForAnonymous(link *ListenLink) UserGroupFeatures {
	merged := UserGroupFeatures{}
	for _, g := range controller.anonymousScope(link) {
		merged = MergeFeatures(merged, g.GetFeatures())
	}
	merged.Alerts = false
	merged.Transcripts = false
	merged.Stats = false
	merged.Map = false
	return merged
}

// anonymousEffectiveDelay mirrors userEffectiveDelay for signed-out listeners:
// the smallest delay configured on a scope group that grants the call.
func (controller *Controller) anonymousEffectiveDelay(link *ListenLink, call *Call, defaultDelay uint) uint {
	if call == nil || call.System == nil || call.Talkgroup == nil {
		return defaultDelay
	}
	best := defaultDelay
	found := false
	sysRef := uint64(call.System.SystemRef)
	for _, g := range controller.anonymousScope(link) {
		if !g.HasSystemAccess(sysRef) || !g.HasTalkgroupAccess(sysRef, call.Talkgroup.TalkgroupRef) {
			continue
		}
		if g.Delay <= 0 && len(g.systemDelaysMap) == 0 && len(g.talkgroupDelaysMap) == 0 {
			continue
		}
		d := g.EffectiveDelay(call, defaultDelay)
		if !found || d < best {
			best = d
			found = true
		}
	}
	return best
}

// anonymousMinDelay is a lower bound on the scope's delays, used as the
// archive search cutoff before the exact per-row check.
func (controller *Controller) anonymousMinDelay(link *ListenLink, defaultDelay uint) uint {
	scope := controller.anonymousScope(link)
	if len(scope) == 0 {
		return defaultDelay
	}
	best := ^uint(0)
	consider := func(d uint) {
		if d < best {
			best = d
		}
	}
	for _, g := range scope {
		if g.Delay > 0 {
			consider(uint(g.Delay))
		} else {
			consider(defaultDelay)
		}
		for _, d := range g.systemDelaysMap {
			if d > 0 {
				consider(d)
			}
		}
		for _, d := range g.talkgroupDelaysMap {
			if d > 0 {
				consider(d)
			}
		}
	}
	return best
}

// clientEffectiveDelay is the live and playback delay for a connected client.
func (controller *Controller) clientEffectiveDelay(client *Client, call *Call, defaultDelay uint) uint {
	if client == nil {
		return defaultDelay
	}
	if client.User != nil {
		return controller.userEffectiveDelay(client.User, call, defaultDelay)
	}
	if !client.IsAdmin && !client.BypassPlaybackSearchACL && controller.anonymousAllowed(client.ListenLink) {
		return controller.anonymousEffectiveDelay(client.ListenLink, call, defaultDelay)
	}
	return defaultDelay
}

func (controller *Controller) featuresForUser(user *User) UserGroupFeatures {
	if controller == nil {
		return AllFeaturesOn()
	}
	if user == nil {
		return controller.featuresForSignedOut(nil)
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

func (controller *Controller) featuresForSignedOut(link *ListenLink) UserGroupFeatures {
	if controller.anonymousAllowed(link) || link != nil {
		return controller.featuresForAnonymous(link)
	}
	if controller.requiresUserAuth() {
		return UserGroupFeatures{}
	}
	return AllFeaturesOn()
}

func (controller *Controller) featuresForClient(client *Client) UserGroupFeatures {
	if client == nil {
		return UserGroupFeatures{}
	}
	if client.IsAdmin || client.BypassPlaybackSearchACL {
		return AllFeaturesOn()
	}
	if client.User == nil {
		return controller.featuresForSignedOut(client.ListenLink)
	}
	return controller.featuresForUser(client.User)
}

func (controller *Controller) clientHasFeature(client *Client, name string) bool {
	return controller.featuresForClient(client).Has(name)
}

func (controller *Controller) anonymousHasCallAccess(link *ListenLink, call *Call) bool {
	if call == nil || call.System == nil {
		return false
	}
	sysRef := uint64(call.System.SystemRef)
	for _, g := range controller.anonymousScope(link) {
		if g == nil || !g.HasSystemAccess(sysRef) {
			continue
		}
		if call.Talkgroup == nil || g.HasTalkgroupAccess(sysRef, call.Talkgroup.TalkgroupRef) {
			return true
		}
	}
	return false
}
