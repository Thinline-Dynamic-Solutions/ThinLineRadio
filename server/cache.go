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
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ============================================================================
// 1. USER ALERT PREFERENCES CACHE
// ============================================================================

type UserAlertPreference struct {
	UserId              uint64
	SystemId            uint64
	TalkgroupId         uint64
	AlertEnabled        bool
	ToneAlerts          bool
	KeywordAlerts       bool
	Keywords            []string
	KeywordListIds      []uint64
	ToneSetIds          []string
	NotificationSound   string
	ToneSetSounds       map[string]string
	ToneDetectionEnabled bool // From talkgroup config
}

type PreferencesCache struct {
	// Map: userId -> composite key -> preference
	byUser map[uint64]map[uint64]*UserAlertPreference
	// Map: composite key -> []userId (for reverse lookups)
	byTalkgroup map[uint64][]uint64
	mutex       sync.RWMutex
	controller  *Controller
}

// makePreferenceKey creates a composite key from systemId and talkgroupId
// Uses bit-shifting to combine two uint64s into one (assumes IDs < 2^32)
func makePreferenceKey(systemId, talkgroupId uint64) uint64 {
	return (systemId << 32) | talkgroupId
}

func NewPreferencesCache(controller *Controller) *PreferencesCache {
	return &PreferencesCache{
		byUser:      make(map[uint64]map[uint64]*UserAlertPreference),
		byTalkgroup: make(map[uint64][]uint64),
		controller:  controller,
	}
}

func (cache *PreferencesCache) Read(db *Database) error {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	// Clear existing cache
	cache.byUser = make(map[uint64]map[uint64]*UserAlertPreference)
	cache.byTalkgroup = make(map[uint64][]uint64)

	// Query all preferences with talkgroup tone detection status
	query := `SELECT p."userId", p."systemId", p."talkgroupId", p."alertEnabled", 
	          p."toneAlerts", p."keywordAlerts", p."keywords", p."keywordListIds", 
	          p."toneSetIds", p."notificationSound", p."toneSetSounds",
	          COALESCE(t."toneDetectionEnabled", false) as "toneDetectionEnabled"
	          FROM "userAlertPreferences" p
	          LEFT JOIN "talkgroups" t ON t."talkgroupId" = p."talkgroupId"
	          WHERE COALESCE(t."alertsEnabled", true) = true 
	          AND COALESCE((SELECT "alertsEnabled" FROM "systems" WHERE "systemId" = p."systemId"), true) = true`

	rows, err := db.Sql.Query(query)
	if err != nil {
		return fmt.Errorf("failed to load preferences cache: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		pref := &UserAlertPreference{}
		var keywordsJson, keywordListIdsJson, toneSetIdsJson string
		var notificationSound, toneSetSoundsJson string

		if err := rows.Scan(
			&pref.UserId,
			&pref.SystemId,
			&pref.TalkgroupId,
			&pref.AlertEnabled,
			&pref.ToneAlerts,
			&pref.KeywordAlerts,
			&keywordsJson,
			&keywordListIdsJson,
			&toneSetIdsJson,
			&notificationSound,
			&toneSetSoundsJson,
			&pref.ToneDetectionEnabled,
		); err != nil {
			continue
		}

		// Parse JSON fields with error handling
		if keywordsJson != "" && keywordsJson != "[]" {
			if err := json.Unmarshal([]byte(keywordsJson), &pref.Keywords); err != nil {
				pref.Keywords = []string{}
			}
		}
		if keywordListIdsJson != "" && keywordListIdsJson != "[]" {
			if err := json.Unmarshal([]byte(keywordListIdsJson), &pref.KeywordListIds); err != nil {
				pref.KeywordListIds = []uint64{}
			}
		}
		if toneSetIdsJson != "" && toneSetIdsJson != "[]" {
			if err := json.Unmarshal([]byte(toneSetIdsJson), &pref.ToneSetIds); err != nil {
				pref.ToneSetIds = []string{}
			}
		}
		if toneSetSoundsJson != "" && toneSetSoundsJson != "{}" {
			if err := json.Unmarshal([]byte(toneSetSoundsJson), &pref.ToneSetSounds); err != nil {
				pref.ToneSetSounds = nil
			}
		}
		pref.NotificationSound = notificationSound

		// Store in byUser map using numeric key
		key := makePreferenceKey(pref.SystemId, pref.TalkgroupId)
		if cache.byUser[pref.UserId] == nil {
			cache.byUser[pref.UserId] = make(map[uint64]*UserAlertPreference)
		}
		cache.byUser[pref.UserId][key] = pref

		// Store in byTalkgroup reverse index
		cache.byTalkgroup[key] = append(cache.byTalkgroup[key], pref.UserId)

		count++
	}

	if cache.controller != nil && cache.controller.Logs != nil {
		if count == 0 {
			cache.controller.Logs.LogEvent(LogLevelInfo, "✅ Loaded 0 user alert preferences into cache (no preferences configured yet)")
		} else {
			cache.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("✅ Loaded %d user alert preferences into cache", count))
		}
	}

	return nil
}

// GetUserPreferences returns all preferences for a user
func (cache *PreferencesCache) GetUserPreferences(userId uint64) map[uint64]*UserAlertPreference {
	cache.mutex.RLock()
	defer cache.mutex.RUnlock()
	return cache.byUser[userId]
}

// GetPreference returns a specific preference for user/system/talkgroup
func (cache *PreferencesCache) GetPreference(userId, systemId, talkgroupId uint64) *UserAlertPreference {
	cache.mutex.RLock()
	defer cache.mutex.RUnlock()

	key := makePreferenceKey(systemId, talkgroupId)
	if userPrefs, ok := cache.byUser[userId]; ok {
		return userPrefs[key]
	}
	return nil
}

// GetUsersForTalkgroup returns all userIds with preferences for a system/talkgroup
func (cache *PreferencesCache) GetUsersForTalkgroup(systemId, talkgroupId uint64) []uint64 {
	cache.mutex.RLock()
	defer cache.mutex.RUnlock()

	key := makePreferenceKey(systemId, talkgroupId)
	return cache.byTalkgroup[key]
}

// ============================================================================
// 2. KEYWORD LISTS CACHE
// ============================================================================

type KeywordList struct {
	Id          uint64
	Label       string
	Description string
	Keywords    []string
	Order       uint
	CreatedAt   int64
}

type KeywordListsCache struct {
	lists      map[uint64]*KeywordList // listId -> KeywordList
	mutex      sync.RWMutex
	controller *Controller
}

func NewKeywordListsCache(controller *Controller) *KeywordListsCache {
	return &KeywordListsCache{
		lists:      make(map[uint64]*KeywordList),
		controller: controller,
	}
}

func (cache *KeywordListsCache) Read(db *Database) error {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	// Clear existing cache
	cache.lists = make(map[uint64]*KeywordList)

	query := `SELECT "keywordListId", "label", "description", "keywords", "order", "createdAt" 
	          FROM "keywordLists" 
	          ORDER BY "order" ASC, "createdAt" DESC`

	rows, err := db.Sql.Query(query)
	if err != nil {
		return fmt.Errorf("failed to load keyword lists cache: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		list := &KeywordList{}
		var keywordsJson string

		if err := rows.Scan(
			&list.Id,
			&list.Label,
			&list.Description,
			&keywordsJson,
			&list.Order,
			&list.CreatedAt,
		); err != nil {
			continue
		}

		// Parse keywords JSON
		if keywordsJson != "" && keywordsJson != "[]" {
			json.Unmarshal([]byte(keywordsJson), &list.Keywords)
		}
		if list.Keywords == nil {
			list.Keywords = []string{}
		}

		cache.lists[list.Id] = list
		count++
	}

	if cache.controller != nil && cache.controller.Logs != nil {
		cache.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("✅ Loaded %d keyword lists into cache", count))
	}

	return nil
}

// GetList returns a keyword list by ID
func (cache *KeywordListsCache) GetList(listId uint64) *KeywordList {
	cache.mutex.RLock()
	defer cache.mutex.RUnlock()
	return cache.lists[listId]
}

// GetAllLists returns all keyword lists
func (cache *KeywordListsCache) GetAllLists() []*KeywordList {
	cache.mutex.RLock()
	defer cache.mutex.RUnlock()

	lists := make([]*KeywordList, 0, len(cache.lists))
	for _, list := range cache.lists {
		lists = append(lists, list)
	}
	return lists
}

// ============================================================================
// 3. SYSTEM/TALKGROUP ID LOOKUP MAPS
// ============================================================================

type IdLookupsCache struct {
	systemRefToId    map[uint]uint64    // systemRef -> systemId
	talkgroupRefToId map[uint64]uint64  // composite key -> talkgroupId
	mutex            sync.RWMutex
	controller       *Controller
}

// makeTalkgroupKey creates a composite key from systemId and talkgroupRef
func makeTalkgroupKey(systemId uint64, talkgroupRef uint) uint64 {
	return (systemId << 32) | uint64(talkgroupRef)
}

func NewIdLookupsCache(controller *Controller) *IdLookupsCache {
	return &IdLookupsCache{
		systemRefToId:    make(map[uint]uint64),
		talkgroupRefToId: make(map[uint64]uint64),
		controller:       controller,
	}
}

func (cache *IdLookupsCache) Read(db *Database) error {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	// Clear existing caches
	cache.systemRefToId = make(map[uint]uint64)
	cache.talkgroupRefToId = make(map[uint64]uint64)

	// Load system mappings
	systemQuery := `SELECT "systemId", "systemRef" FROM "systems"`
	rows, err := db.Sql.Query(systemQuery)
	if err != nil {
		return fmt.Errorf("failed to load system ID mappings: %v", err)
	}
	defer rows.Close()

	systemCount := 0
	for rows.Next() {
		var systemId uint64
		var systemRef uint
		if err := rows.Scan(&systemId, &systemRef); err != nil {
			continue
		}
		cache.systemRefToId[systemRef] = systemId
		systemCount++
	}
	rows.Close()

	// Load talkgroup mappings
	talkgroupQuery := `SELECT "talkgroupId", "systemId", "talkgroupRef" FROM "talkgroups"`
	rows, err = db.Sql.Query(talkgroupQuery)
	if err != nil {
		return fmt.Errorf("failed to load talkgroup ID mappings: %v", err)
	}
	defer rows.Close()

	talkgroupCount := 0
	for rows.Next() {
		var talkgroupId, systemId uint64
		var talkgroupRef uint
		if err := rows.Scan(&talkgroupId, &systemId, &talkgroupRef); err != nil {
			continue
		}
		key := makeTalkgroupKey(systemId, talkgroupRef)
		cache.talkgroupRefToId[key] = talkgroupId
		talkgroupCount++
	}

	if cache.controller != nil && cache.controller.Logs != nil {
		cache.controller.Logs.LogEvent(LogLevelInfo, 
			fmt.Sprintf("✅ Loaded %d system and %d talkgroup ID mappings into cache", systemCount, talkgroupCount))
	}

	return nil
}

// GetSystemId returns systemId from systemRef
func (cache *IdLookupsCache) GetSystemId(systemRef uint) (uint64, bool) {
	cache.mutex.RLock()
	defer cache.mutex.RUnlock()
	id, ok := cache.systemRefToId[systemRef]
	return id, ok
}

// GetTalkgroupId returns talkgroupId from systemId + talkgroupRef
func (cache *IdLookupsCache) GetTalkgroupId(systemId uint64, talkgroupRef uint) (uint64, bool) {
	cache.mutex.RLock()
	defer cache.mutex.RUnlock()
	key := makeTalkgroupKey(systemId, talkgroupRef)
	id, ok := cache.talkgroupRefToId[key]
	return id, ok
}

// ============================================================================
// 4. RECENT ALERTS CACHE (1 hour window to prevent duplicates)
// ============================================================================

type AlertCacheEntry struct {
	AlertId     uint64
	CallId      uint64
	SystemId    uint64
	TalkgroupId uint64
	AlertType   string
	ToneSetId   string
	Keywords    string
	CreatedAt   time.Time
}

type RecentAlertsCache struct {
	// Map: "callId-systemId-talkgroupId-alertType-toneSetId-keywords" -> entry
	alerts     map[string]*AlertCacheEntry
	mutex      sync.RWMutex
	controller *Controller
}

func NewRecentAlertsCache(controller *Controller) *RecentAlertsCache {
	cache := &RecentAlertsCache{
		alerts:     make(map[string]*AlertCacheEntry),
		controller: controller,
	}
	
	// Start cleanup goroutine to remove old entries
	go cache.cleanup()
	
	return cache
}

func (cache *RecentAlertsCache) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		cache.mutex.Lock()
		cutoff := time.Now().Add(-1 * time.Hour)
		for key, entry := range cache.alerts {
			if entry.CreatedAt.Before(cutoff) {
				delete(cache.alerts, key)
			}
		}
		cache.mutex.Unlock()
	}
}

// AlertExists checks if an alert already exists (within last hour)
func (cache *RecentAlertsCache) AlertExists(callId, systemId, talkgroupId uint64, alertType, toneSetId, keywords string) (uint64, bool) {
	cache.mutex.RLock()
	defer cache.mutex.RUnlock()

	key := cache.makeKey(callId, systemId, talkgroupId, alertType, toneSetId, keywords)
	entry, exists := cache.alerts[key]
	if exists {
		return entry.AlertId, true
	}
	return 0, false
}

// AddAlert adds an alert to the cache
func (cache *RecentAlertsCache) AddAlert(alertId, callId, systemId, talkgroupId uint64, alertType, toneSetId, keywords string) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	key := cache.makeKey(callId, systemId, talkgroupId, alertType, toneSetId, keywords)
	cache.alerts[key] = &AlertCacheEntry{
		AlertId:     alertId,
		CallId:      callId,
		SystemId:    systemId,
		TalkgroupId: talkgroupId,
		AlertType:   alertType,
		ToneSetId:   toneSetId,
		Keywords:    keywords,
		CreatedAt:   time.Now(),
	}
}

func (cache *RecentAlertsCache) makeKey(callId, systemId, talkgroupId uint64, alertType, toneSetId, keywords string) string {
	return fmt.Sprintf("%d-%d-%d-%s-%s-%s", callId, systemId, talkgroupId, alertType, toneSetId, keywords)
}

// Read loads recent alerts from database (last hour)
func (cache *RecentAlertsCache) Read(db *Database) error {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()

	// Clear existing cache
	cache.alerts = make(map[string]*AlertCacheEntry)

	// Load alerts from last hour using parameterized query
	cutoff := time.Now().Add(-1 * time.Hour).UnixMilli()
	query := `SELECT "alertId", "callId", "systemId", "talkgroupId", "alertType", 
	                      "toneSetId", "keywordsMatched", "createdAt" 
	                      FROM "alerts" 
	                      WHERE "createdAt" >= $1`

	rows, err := db.Sql.Query(query, cutoff)
	if err != nil {
		return fmt.Errorf("failed to load recent alerts cache: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var alertId, callId, systemId, talkgroupId uint64
		var alertType, toneSetId, keywords string
		var createdAt int64

		if err := rows.Scan(&alertId, &callId, &systemId, &talkgroupId, &alertType, &toneSetId, &keywords, &createdAt); err != nil {
			continue
		}

		key := cache.makeKey(callId, systemId, talkgroupId, alertType, toneSetId, keywords)
		cache.alerts[key] = &AlertCacheEntry{
			AlertId:     alertId,
			CallId:      callId,
			SystemId:    systemId,
			TalkgroupId: talkgroupId,
			AlertType:   alertType,
			ToneSetId:   toneSetId,
			Keywords:    keywords,
			CreatedAt:   time.UnixMilli(createdAt),
		}
		count++
	}

	if cache.controller != nil && cache.controller.Logs != nil {
		cache.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("✅ Loaded %d recent alerts into cache (last 1 hour)", count))
	}

	return nil
}
