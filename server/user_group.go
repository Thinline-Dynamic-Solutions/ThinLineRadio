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
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type PricingOption struct {
	PriceId   string `json:"priceId"`   // Stripe Price ID
	Label     string `json:"label"`     // Display label (e.g., "Monthly", "Yearly")
	Amount    string `json:"amount"`    // Display amount (e.g., "$10/month", "$100/year")
	TrialDays int    `json:"trialDays"` // Trial period in days (0 = no trial, 1-30 = trial days)
}

type UserGroup struct {
	Id                    uint64
	Name                  string
	Description           string
	SystemAccess          string // JSON array of system IDs (legacy) or array of objects with id and talkgroups (new format)
	Delay                 int
	SystemDelays          string // JSON map
	TalkgroupDelays       string // JSON map
	ConnectionLimit       uint
	MaxUsers              uint // Maximum number of users allowed in this group (0 = unlimited)
	BillingEnabled        bool
	StripePriceId         string // DEPRECATED: Legacy single price ID (kept for backward compatibility)
	PricingOptions        string // JSON array of PricingOption objects (up to 3)
	BillingMode           string // "all_users" (each user has own customer ID) or "group_admin" (all admins share one customer ID)
	CollectSalesTax       bool   // DEPRECATED: kept for migration only — use TaxMode instead
	TaxMode               string // "none", "automatic", or "fixed"
	StripeTaxRateId       string // Stripe Tax Rate ID (e.g. txr_xxx) used when TaxMode = "fixed"
	IsPublicRegistration  bool
	AllowAddExistingUsers bool // Allow group admins to add existing users from any group
	// AutoEnableNewTalkgroups controls whether newly created systems/talkgroups are
	// automatically granted to this group (when access uses wildcards / All) and
	// whether clients should default those channels on in the livefeed map.
	// Default false: freeze wildcards to an explicit snapshot on save; clients default off.
	AutoEnableNewTalkgroups bool
	// IsFreeGroup lets anonymous visitors use this group's systemAccess + features
	// without signing in (overrides requireListenerSignIn for that scoped feed).
	IsFreeGroup bool
	// AdFreeExcluded leaves the group's members out of server-wide ad-free:
	// they are not billed as seats and the mobile app shows them ads unless
	// they have their own app store subscription.
	AdFreeExcluded bool
	// Features is JSON UserGroupFeatures; empty means all-on (legacy groups).
	Features            string
	CreatedAt           int64
	systemAccessData    []uint64 // Legacy format: simple array of system IDs
	systemAccessDataNew any      // New format: array of objects with id and talkgroups (same format as user systemsData)
	systemDelaysMap     map[uint64]uint
	talkgroupDelaysMap  map[string]uint
	pricingOptionsData  []PricingOption
	featuresData        UserGroupFeatures
}

type UserGroups struct {
	mutex  sync.RWMutex
	groups map[uint64]*UserGroup
}

func NewUserGroups() *UserGroups {
	return &UserGroups{
		groups: make(map[uint64]*UserGroup),
	}
}

func (ug *UserGroup) loadSystemAccess() {
	trimmed := strings.TrimSpace(ug.SystemAccess)
	if trimmed == "" {
		// Empty string means All systems (legacy wildcard).
		ug.systemAccessData = []uint64{}
		ug.systemAccessDataNew = nil
		return
	}

	// Explicit empty JSON array means no systems (deny-all), not All.
	if trimmed == "[]" {
		ug.systemAccessData = []uint64{}
		ug.systemAccessDataNew = []map[string]interface{}{}
		return
	}

	// Try to parse as new format first (array of objects)
	var newFormat []map[string]interface{}
	if err := json.Unmarshal([]byte(ug.SystemAccess), &newFormat); err == nil && len(newFormat) > 0 {
		// Check if it's the new format (objects with id and talkgroups)
		if _, ok := newFormat[0]["id"]; ok {
			ug.systemAccessDataNew = newFormat
			ug.systemAccessData = []uint64{} // Clear legacy format
			return
		}
	}

	// Fall back to legacy format (simple array of system IDs)
	var systems []uint64
	if err := json.Unmarshal([]byte(ug.SystemAccess), &systems); err != nil {
		log.Printf("Error parsing system access for group %d: %v", ug.Id, err)
		ug.systemAccessData = []uint64{}
		ug.systemAccessDataNew = nil
	} else {
		ug.systemAccessData = systems
		ug.systemAccessDataNew = nil
	}
}

// NormalizeSystemAccess freezes wildcards when AutoEnableNewTalkgroups is false.
// Empty SystemAccess (All) and talkgroups:"*" become explicit ID lists of systems/TGs
// that exist at save time, so later additions are not granted until an admin updates the group.
// When freezeAll is false, empty All is left unchanged (only talkgroups:"*" are expanded);
// use freezeAll=true on admin save so choosing All with the toggle off becomes a snapshot.
func (ug *UserGroup) NormalizeSystemAccess(systems *Systems) {
	ug.normalizeSystemAccess(systems, true)
}

// ExpandStarTalkgroups expands talkgroups:"*" to current TG IDs when auto-enable is
// off, without converting empty All into a full snapshot. Safe for startup.
func (ug *UserGroup) ExpandStarTalkgroups(systems *Systems) {
	ug.normalizeSystemAccess(systems, false)
}

func (ug *UserGroup) normalizeSystemAccess(systems *Systems, freezeAll bool) {
	if ug == nil || ug.AutoEnableNewTalkgroups {
		return
	}
	if systems == nil {
		systems = &Systems{}
	}

	trimmed := strings.TrimSpace(ug.SystemAccess)

	buildAllSnapshot := func() []map[string]interface{} {
		out := make([]map[string]interface{}, 0, len(systems.List))
		for _, system := range systems.List {
			tgRefs := make([]interface{}, 0, len(system.Talkgroups.List))
			for _, tg := range system.Talkgroups.List {
				tgRefs = append(tgRefs, tg.TalkgroupRef)
			}
			out = append(out, map[string]interface{}{
				"id":         system.SystemRef,
				"talkgroups": tgRefs,
			})
		}
		return out
	}

	expandTalkgroups := func(systemRef uint64, talkgroups any) []interface{} {
		system, ok := systems.GetSystemByRef(uint(systemRef))
		if !ok || system == nil || system.Talkgroups == nil {
			if list, ok := talkgroups.([]interface{}); ok {
				return list
			}
			return []interface{}{}
		}
		switch v := talkgroups.(type) {
		case string:
			if v == "*" {
				refs := make([]interface{}, 0, len(system.Talkgroups.List))
				for _, tg := range system.Talkgroups.List {
					refs = append(refs, tg.TalkgroupRef)
				}
				return refs
			}
		case []interface{}:
			return v
		case nil:
			refs := make([]interface{}, 0, len(system.Talkgroups.List))
			for _, tg := range system.Talkgroups.List {
				refs = append(refs, tg.TalkgroupRef)
			}
			return refs
		}
		refs := make([]interface{}, 0, len(system.Talkgroups.List))
		for _, tg := range system.Talkgroups.List {
			refs = append(refs, tg.TalkgroupRef)
		}
		return refs
	}

	var frozen []map[string]interface{}

	if trimmed == "" {
		if !freezeAll {
			return
		}
		frozen = buildAllSnapshot()
	} else {
		var newFormat []map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &newFormat); err == nil && len(newFormat) > 0 {
			if _, ok := newFormat[0]["id"]; ok {
				frozen = make([]map[string]interface{}, 0, len(newFormat))
				changed := false
				for _, scope := range newFormat {
					var systemRef uint64
					switch id := scope["id"].(type) {
					case float64:
						systemRef = uint64(id)
					case string:
						if parsed, err := strconv.ParseUint(id, 10, 64); err == nil {
							systemRef = parsed
						}
					}
					if systemRef == 0 {
						continue
					}
					tgVal := scope["talkgroups"]
					if s, ok := tgVal.(string); ok && s == "*" {
						changed = true
					} else if tgVal == nil {
						changed = true
					}
					frozen = append(frozen, map[string]interface{}{
						"id":         systemRef,
						"talkgroups": expandTalkgroups(systemRef, tgVal),
					})
				}
				if !freezeAll && !changed {
					return
				}
			}
		}
		if frozen == nil {
			var legacy []uint64
			if err := json.Unmarshal([]byte(trimmed), &legacy); err == nil {
				// Legacy system-ID lists imply all talkgroups; expand when freezing.
				frozen = make([]map[string]interface{}, 0, len(legacy))
				for _, systemRef := range legacy {
					frozen = append(frozen, map[string]interface{}{
						"id":         systemRef,
						"talkgroups": expandTalkgroups(systemRef, "*"),
					})
				}
			} else if freezeAll {
				frozen = buildAllSnapshot()
			} else {
				return
			}
		}
	}

	encoded, err := json.Marshal(frozen)
	if err != nil {
		log.Printf("Error freezing system access for group %d: %v", ug.Id, err)
		return
	}
	ug.SystemAccess = string(encoded)
	ug.loadSystemAccess()
}

func (ug *UserGroup) loadSystemDelays() {
	if strings.TrimSpace(ug.SystemDelays) == "" {
		ug.systemDelaysMap = make(map[uint64]uint)
		return
	}

	var delays map[string]uint
	if err := json.Unmarshal([]byte(ug.SystemDelays), &delays); err != nil {
		log.Printf("Error parsing system delays for group %d: %v", ug.Id, err)
		ug.systemDelaysMap = make(map[uint64]uint)
	} else {
		ug.systemDelaysMap = make(map[uint64]uint)
		for k, v := range delays {
			if id, err := strconv.ParseUint(k, 10, 64); err == nil {
				ug.systemDelaysMap[id] = v
			}
		}
	}
}

func (ug *UserGroup) loadTalkgroupDelays() {
	if strings.TrimSpace(ug.TalkgroupDelays) == "" {
		ug.talkgroupDelaysMap = make(map[string]uint)
		return
	}

	if err := json.Unmarshal([]byte(ug.TalkgroupDelays), &ug.talkgroupDelaysMap); err != nil {
		log.Printf("Error parsing talkgroup delays for group %d: %v", ug.Id, err)
		ug.talkgroupDelaysMap = make(map[string]uint)
	}
}

func (ug *UserGroup) loadPricingOptions() {
	if strings.TrimSpace(ug.PricingOptions) == "" {
		ug.pricingOptionsData = []PricingOption{}
		return
	}

	if err := json.Unmarshal([]byte(ug.PricingOptions), &ug.pricingOptionsData); err != nil {
		log.Printf("Error parsing pricing options for group %d: %v", ug.Id, err)
		ug.pricingOptionsData = []PricingOption{}
	}
}

func (ug *UserGroup) GetPricingOptions() []PricingOption {
	return ug.pricingOptionsData
}

// HasAnySystemAccess reports whether the group grants access to at least one system.
func (ug *UserGroup) HasAnySystemAccess() bool {
	if ug == nil {
		return false
	}
	if ug.systemAccessDataNew != nil {
		switch v := ug.systemAccessDataNew.(type) {
		case []map[string]interface{}:
			return len(v) > 0
		}
	}
	// Legacy: empty list means all systems.
	if len(ug.systemAccessData) == 0 {
		return true
	}
	return len(ug.systemAccessData) > 0
}

func (ug *UserGroup) HasSystemAccess(systemId uint64) bool {
	// If using new format, check it
	if ug.systemAccessDataNew != nil {
		switch v := ug.systemAccessDataNew.(type) {
		case []map[string]interface{}:
			for _, scope := range v {
				idVal, ok := scope["id"]
				if !ok {
					continue
				}
				var systemRef uint64
				switch id := idVal.(type) {
				case float64:
					systemRef = uint64(id)
				case string:
					if parsed, err := strconv.ParseUint(id, 10, 64); err == nil {
						systemRef = parsed
					}
				}
				if systemRef == systemId {
					return true
				}
			}
			return false
		}
	}

	// Legacy format: simple array of system IDs
	if len(ug.systemAccessData) == 0 {
		return true // Empty means all systems
	}
	for _, id := range ug.systemAccessData {
		if id == systemId {
			return true
		}
	}
	return false
}

// HasTalkgroupAccess checks if the group has access to a specific talkgroup in a system
func (ug *UserGroup) HasTalkgroupAccess(systemId uint64, talkgroupId uint) bool {
	// If no system access, deny
	if !ug.HasSystemAccess(systemId) {
		return false
	}

	// If using new format, check talkgroup restrictions
	if ug.systemAccessDataNew != nil {
		switch v := ug.systemAccessDataNew.(type) {
		case []map[string]interface{}:
			for _, scope := range v {
				idVal, ok := scope["id"]
				if !ok {
					continue
				}
				var systemRef uint64
				switch id := idVal.(type) {
				case float64:
					systemRef = uint64(id)
				case string:
					if parsed, err := strconv.ParseUint(id, 10, 64); err == nil {
						systemRef = parsed
					}
				}
				if systemRef != systemId {
					continue
				}

				// Check talkgroups restriction
				if tg, ok := scope["talkgroups"]; ok {
					switch talkgroups := tg.(type) {
					case string:
						if talkgroups == "*" {
							return true // All talkgroups allowed
						}
					case []interface{}:
						for _, entry := range talkgroups {
							switch talkgroupRef := entry.(type) {
							case float64:
								if uint(talkgroupRef) == talkgroupId {
									return true
								}
							case string:
								if parsed, err := strconv.ParseUint(talkgroupRef, 10, 32); err == nil && uint(parsed) == talkgroupId {
									return true
								}
							}
						}
						return false // System matched but talkgroup not in list
					}
				} else {
					// No talkgroups restriction means whole system allowed
					return true
				}
			}
			return false
		}
	}

	// Legacy format: if system is accessible, all talkgroups are accessible
	return true
}

func (ug *UserGroup) EffectiveDelay(call *Call, defaultDelay uint) uint {
	if ug == nil || call == nil || call.System == nil || call.Talkgroup == nil {
		return defaultDelay
	}

	if len(ug.talkgroupDelaysMap) > 0 {
		key := fmt.Sprintf("%d:%d", call.System.SystemRef, call.Talkgroup.TalkgroupRef)
		if delay, ok := ug.talkgroupDelaysMap[key]; ok && delay > 0 {
			return delay
		}
	}

	if len(ug.systemDelaysMap) > 0 {
		if delay, ok := ug.systemDelaysMap[uint64(call.System.SystemRef)]; ok && delay > 0 {
			return delay
		}
	}

	if ug.Delay > 0 {
		return uint(ug.Delay)
	}

	return defaultDelay
}

func (ugs *UserGroups) Load(db *Database) error {
	ugs.mutex.Lock()
	defer ugs.mutex.Unlock()

	rows, err := db.Sql.Query(`SELECT "userGroupId", "name", "description", "systemAccess", "delay", "systemDelays", "talkgroupDelays", "connectionLimit", "maxUsers", "billingEnabled", "stripePriceId", "pricingOptions", "billingMode", "collectSalesTax", "taxMode", "stripeTaxRateId", "isPublicRegistration", "allowAddExistingUsers", "autoEnableNewTalkgroups", COALESCE("isFreeGroup", false), COALESCE("adFreeExcluded", false), COALESCE("features", ''), "createdAt" FROM "userGroups"`)
	if err != nil {
		return err
	}
	defer rows.Close()

	// Don't create a new map - update the existing one to preserve recently added groups
	if ugs.groups == nil {
		ugs.groups = make(map[uint64]*UserGroup)
	}

	// Track groups loaded from DB to detect deletions
	loadedFromDb := make(map[uint64]bool)

	for rows.Next() {
		group := &UserGroup{}
		var createdAt sql.NullInt64
		var maxUsers sql.NullInt64
		var allowAddExistingUsers sql.NullBool
		var autoEnableNewTalkgroups sql.NullBool
		var isFreeGroup sql.NullBool
		var features sql.NullString
		var stripePriceId sql.NullString
		var pricingOptions sql.NullString
		var billingMode sql.NullString
		var collectSalesTax sql.NullBool
		var taxMode sql.NullString
		var stripeTaxRateId sql.NullString

		err := rows.Scan(
			&group.Id,
			&group.Name,
			&group.Description,
			&group.SystemAccess,
			&group.Delay,
			&group.SystemDelays,
			&group.TalkgroupDelays,
			&group.ConnectionLimit,
			&maxUsers,
			&group.BillingEnabled,
			&stripePriceId,
			&pricingOptions,
			&billingMode,
			&collectSalesTax,
			&taxMode,
			&stripeTaxRateId,
			&group.IsPublicRegistration,
			&allowAddExistingUsers,
			&autoEnableNewTalkgroups,
			&isFreeGroup,
			&group.AdFreeExcluded,
			&features,
			&createdAt,
		)
		if err != nil {
			log.Printf("Error loading user group: %v", err)
			continue
		}

		if maxUsers.Valid && maxUsers.Int64 >= 0 {
			group.MaxUsers = uint(maxUsers.Int64)
		}

		if allowAddExistingUsers.Valid {
			group.AllowAddExistingUsers = allowAddExistingUsers.Bool
		} else {
			group.AllowAddExistingUsers = false // Default to false for existing groups
		}

		if autoEnableNewTalkgroups.Valid {
			group.AutoEnableNewTalkgroups = autoEnableNewTalkgroups.Bool
		} else {
			group.AutoEnableNewTalkgroups = false
		}

		if isFreeGroup.Valid {
			group.IsFreeGroup = isFreeGroup.Bool
		}
		if features.Valid {
			group.Features = features.String
		}

		if stripePriceId.Valid {
			group.StripePriceId = stripePriceId.String
		} else {
			group.StripePriceId = ""
		}

		if pricingOptions.Valid {
			group.PricingOptions = pricingOptions.String
		} else {
			group.PricingOptions = ""
		}

		if billingMode.Valid && billingMode.String != "" {
			group.BillingMode = billingMode.String
		} else {
			group.BillingMode = "all_users" // Default to all_users for existing groups
		}

		if collectSalesTax.Valid {
			group.CollectSalesTax = collectSalesTax.Bool
		} else {
			group.CollectSalesTax = false // Default to false for existing groups
		}

		if taxMode.Valid && taxMode.String != "" {
			group.TaxMode = taxMode.String
		} else {
			group.TaxMode = "none"
		}

		if stripeTaxRateId.Valid {
			group.StripeTaxRateId = stripeTaxRateId.String
		} else {
			group.StripeTaxRateId = ""
		}

		if createdAt.Valid {
			group.CreatedAt = createdAt.Int64
		} else {
			group.CreatedAt = time.Now().Unix()
		}

		group.loadSystemAccess()
		group.loadSystemDelays()
		group.loadTalkgroupDelays()
		group.loadPricingOptions()
		group.loadFeatures()

		ugs.groups[group.Id] = group
		loadedFromDb[group.Id] = true
	}

	// Remove groups that no longer exist in the database
	for id := range ugs.groups {
		if !loadedFromDb[id] {
			delete(ugs.groups, id)
		}
	}

	return rows.Err()
}

func (ugs *UserGroups) Get(id uint64) *UserGroup {
	ugs.mutex.RLock()
	defer ugs.mutex.RUnlock()
	return ugs.groups[id]
}

func (ugs *UserGroups) GetByName(name string) *UserGroup {
	ugs.mutex.RLock()
	defer ugs.mutex.RUnlock()
	for _, group := range ugs.groups {
		if group.Name == name {
			return group
		}
	}
	return nil
}

func (ugs *UserGroups) GetPublicRegistrationGroup() *UserGroup {
	ugs.mutex.RLock()
	defer ugs.mutex.RUnlock()
	for _, group := range ugs.groups {
		if group.IsPublicRegistration {
			return group
		}
	}
	return nil
}

// GetPublicRegistrationGroups returns every group flagged as a public
// registration tier, ordered by name for a stable signup listing.
func (ugs *UserGroups) GetPublicRegistrationGroups() []*UserGroup {
	ugs.mutex.RLock()
	defer ugs.mutex.RUnlock()
	out := []*UserGroup{}
	for _, group := range ugs.groups {
		if group.IsPublicRegistration {
			out = append(out, group)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// GetFreePlanGroup is the sign-up group without billing that a user whose access
// has expired can switch to instead of paying, or nil when there isn't one.
func (ugs *UserGroups) GetFreePlanGroup() *UserGroup {
	for _, group := range ugs.GetPublicRegistrationGroups() {
		if !group.BillingEnabled {
			return group
		}
	}
	return nil
}

// IsSelfBillable reports whether a user can self-serve join/leave this group:
// free groups, or paid groups billed per-user ("all_users"). Groups billed via
// "group_admin" (a shared customer the admin pays) are excluded.
func (ug *UserGroup) IsSelfBillable() bool {
	if ug == nil {
		return false
	}
	return !ug.BillingEnabled || ug.BillingMode == "all_users" || ug.BillingMode == ""
}

func (ugs *UserGroups) GetAll() []*UserGroup {
	ugs.mutex.RLock()
	defer ugs.mutex.RUnlock()
	groups := make([]*UserGroup, 0, len(ugs.groups))
	for _, group := range ugs.groups {
		groups = append(groups, group)
	}
	return groups
}

func (ugs *UserGroups) Add(group *UserGroup, db *Database) error {
	if group.CreatedAt == 0 {
		group.CreatedAt = time.Now().Unix()
	}

	if group.IsFreeGroup {
		group.BillingEnabled = false
	}
	if strings.TrimSpace(group.Features) == "" {
		if group.IsFreeGroup {
			group.Features = FreeGroupDefaultFeatures().ToJSON()
		} else {
			group.Features = AllFeaturesOn().ToJSON()
		}
	}
	group.loadSystemAccess()
	group.loadSystemDelays()
	group.loadTalkgroupDelays()
	group.loadPricingOptions()
	group.loadFeatures()

	var userId int64
	err := db.Sql.QueryRow(
		`INSERT INTO "userGroups" ("name", "description", "systemAccess", "delay", "systemDelays", "talkgroupDelays", "connectionLimit", "maxUsers", "billingEnabled", "stripePriceId", "pricingOptions", "billingMode", "collectSalesTax", "taxMode", "stripeTaxRateId", "isPublicRegistration", "allowAddExistingUsers", "autoEnableNewTalkgroups", "isFreeGroup", "adFreeExcluded", "features", "createdAt") 
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22) RETURNING "userGroupId"`,
		group.Name, group.Description, group.SystemAccess, group.Delay, group.SystemDelays, group.TalkgroupDelays, group.ConnectionLimit, group.MaxUsers, group.BillingEnabled, group.StripePriceId, group.PricingOptions, group.BillingMode, group.CollectSalesTax, group.TaxMode, group.StripeTaxRateId, group.IsPublicRegistration, group.AllowAddExistingUsers, group.AutoEnableNewTalkgroups, group.IsFreeGroup, group.AdFreeExcluded, group.Features, group.CreatedAt,
	).Scan(&userId)

	if err != nil {
		return err
	}

	group.Id = uint64(userId)

	ugs.mutex.Lock()
	ugs.groups[group.Id] = group
	ugs.mutex.Unlock()

	return nil
}

func (ugs *UserGroups) Update(group *UserGroup, db *Database) error {
	if group.IsFreeGroup {
		group.BillingEnabled = false
	}
	if strings.TrimSpace(group.Features) == "" {
		if group.IsFreeGroup {
			group.Features = FreeGroupDefaultFeatures().ToJSON()
		} else {
			group.Features = AllFeaturesOn().ToJSON()
		}
	}
	group.loadSystemAccess()
	group.loadSystemDelays()
	group.loadTalkgroupDelays()
	group.loadPricingOptions()
	group.loadFeatures()

	_, err := db.Sql.Exec(
		`UPDATE "userGroups" SET "name" = $1, "description" = $2, "systemAccess" = $3, "delay" = $4, "systemDelays" = $5, "talkgroupDelays" = $6, "connectionLimit" = $7, "maxUsers" = $8, "billingEnabled" = $9, "stripePriceId" = $10, "pricingOptions" = $11, "billingMode" = $12, "collectSalesTax" = $13, "taxMode" = $14, "stripeTaxRateId" = $15, "isPublicRegistration" = $16, "allowAddExistingUsers" = $17, "autoEnableNewTalkgroups" = $18, "isFreeGroup" = $19, "adFreeExcluded" = $20, "features" = $21 WHERE "userGroupId" = $22`,
		group.Name, group.Description, group.SystemAccess, group.Delay, group.SystemDelays, group.TalkgroupDelays, group.ConnectionLimit, group.MaxUsers, group.BillingEnabled, group.StripePriceId, group.PricingOptions, group.BillingMode, group.CollectSalesTax, group.TaxMode, group.StripeTaxRateId, group.IsPublicRegistration, group.AllowAddExistingUsers, group.AutoEnableNewTalkgroups, group.IsFreeGroup, group.AdFreeExcluded, group.Features, group.Id,
	)

	if err != nil {
		return err
	}

	ugs.mutex.Lock()
	ugs.groups[group.Id] = group
	ugs.mutex.Unlock()

	return nil
}

func (ugs *UserGroups) Delete(id uint64, db *Database) error {
	_, err := db.Sql.Exec(`DELETE FROM "userGroups" WHERE "userGroupId" = $1`, id)
	if err != nil {
		return err
	}

	ugs.mutex.Lock()
	delete(ugs.groups, id)
	ugs.mutex.Unlock()

	return nil
}

// GetUserCount returns the number of users currently in this group
func (ugs *UserGroups) GetUserCount(groupId uint64, users *Users) uint {
	if users == nil {
		return 0
	}

	ugs.mutex.RLock()
	defer ugs.mutex.RUnlock()

	group := ugs.groups[groupId]
	if group == nil {
		return 0
	}

	// Count users in this group
	count := uint(0)
	users.mutex.RLock()
	defer users.mutex.RUnlock()

	for _, user := range users.users {
		if user.IsMemberOf(groupId) {
			count++
		}
	}

	return count
}
