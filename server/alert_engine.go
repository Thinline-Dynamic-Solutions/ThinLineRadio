// Copyright (C) 2025 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT EVEN THE IMPLIED WARRANTY OF MERCHANTABILITY or FITNESS
// FOR A PARTICULAR PURPOSE.  See the GNU General Public License for
// more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>

package main

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// AlertEngine handles alert creation and triggering
type AlertEngine struct {
	controller *Controller

	// cooldownMu protects lastAlertFiredAt.
	cooldownMu sync.Mutex
	// lastAlertFiredAt tracks when the most recent tone alert notification was sent
	// for each talkgroup (keyed by talkgroupId).  Used to enforce per-talkgroup
	// alert cooldowns so that departments who double-page don't generate duplicate
	// push notifications within the configured window.
	lastAlertFiredAt map[uint64]time.Time

	// lastCleanupUnix is the Unix timestamp (seconds) of the most recent
	// cleanupOldAlerts run.  Compared atomically so that concurrent createAlert
	// calls don't launch redundant cleanup goroutines.
	lastCleanupUnix atomic.Int64
}

// NewAlertEngine creates a new alert engine
func NewAlertEngine(controller *Controller) *AlertEngine {
	return &AlertEngine{
		controller:       controller,
		lastAlertFiredAt: make(map[uint64]time.Time),
	}
}

// TriggerPreAlerts triggers immediate pre-alerts when tones are detected (before transcription)
// This allows users to be notified immediately without waiting for voice confirmation
func (engine *AlertEngine) TriggerPreAlerts(call *Call) {
	if call == nil || !call.HasTones {
		return
	}

	// Get all matched tone sets from this call
	matchedToneSets := call.ToneSequence.MatchedToneSets
	if len(matchedToneSets) == 0 {
		// Fallback to singular MatchedToneSet for backward compatibility
		if call.ToneSequence.MatchedToneSet == nil {
			return
		}
		matchedToneSets = []*ToneSet{call.ToneSequence.MatchedToneSet}
	}

	// Ensure System and Talkgroup database IDs are set (required for query)
	// For tone-only calls (Call=0), these might not be set yet, so resolve them
	if call.System == nil || call.Talkgroup == nil {
		engine.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("pre-alert skipped for call %d: System or Talkgroup is nil", call.Id))
		return
	}

	systemId := call.System.Id
	talkgroupId := call.Talkgroup.Id

	// If database IDs are not set, try to resolve them from SystemRef and TalkgroupRef using cache
	if systemId == 0 && call.System.SystemRef > 0 {
		if id, ok := engine.controller.IdLookupsCache.GetSystemId(call.System.SystemRef); ok {
			systemId = id
		} else {
			engine.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("pre-alert skipped for call %d: failed to resolve systemId from systemRef %d", call.Id, call.System.SystemRef))
			return
		}
	}

	if talkgroupId == 0 && call.Talkgroup.TalkgroupRef > 0 && systemId > 0 {
		if id, ok := engine.controller.IdLookupsCache.GetTalkgroupId(systemId, call.Talkgroup.TalkgroupRef); ok {
			talkgroupId = id
		} else {
			engine.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("pre-alert skipped for call %d: failed to resolve talkgroupId from talkgroupRef %d", call.Id, call.Talkgroup.TalkgroupRef))
			return
		}
	}

	if systemId == 0 || talkgroupId == 0 {
		engine.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("pre-alert skipped for call %d: systemId=%d or talkgroupId=%d is 0", call.Id, systemId, talkgroupId))
		return
	}

	// Get all users with tone alerts enabled for this talkgroup from cache
	userIds := engine.controller.PreferencesCache.GetUsersForTalkgroup(systemId, talkgroupId)

	// Collect user preferences from cache
	type userPref struct {
		userId             uint64
		selectedToneSetIds map[string]bool // If empty, user wants all tone sets
	}
	var users []userPref

	for _, userId := range userIds {
		pref := engine.controller.PreferencesCache.GetPreference(userId, systemId, talkgroupId)
		if pref == nil || !pref.AlertEnabled || !pref.ToneAlerts {
			continue
		}

		userPreference := userPref{
			userId:             userId,
			selectedToneSetIds: make(map[string]bool),
		}

		// Use user's selected tone set IDs from cache
		if len(pref.ToneSetIds) > 0 {
			for _, id := range pref.ToneSetIds {
				userPreference.selectedToneSetIds[id] = true
			}
			// DEBUG: Log what user selected
			engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("pre-alert: user %d has selected specific tone sets: %v", userId, pref.ToneSetIds))
		} else {
			// DEBUG: Log that user wants all tone sets
			engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("pre-alert: user %d wants ALL tone sets (none selected)", userId))
		}

		users = append(users, userPreference)
	}

	if len(users) == 0 {
		engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("no users with tone alerts enabled for pre-alert on call %d", call.Id))
		return
	}

	// Get system and talkgroup labels once
	systemLabel := ""
	talkgroupLabel := ""
	if call.System != nil {
		systemLabel = call.System.Label
	}
	if call.Talkgroup != nil {
		talkgroupLabel = call.Talkgroup.Label
	}

	// Create one pre-alert per matched tone set
	for _, matchedToneSet := range matchedToneSets {
		if matchedToneSet == nil || matchedToneSet.Id == "" {
			continue
		}

		// DEBUG: Log detected tone set details
		engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("🔔 [TONE SET DEBUG] Pre-alert processing: Detected tone set ID='%s', Label='%s' on call %d", matchedToneSet.Id, matchedToneSet.Label, call.Id))

		// NOTE: Pre-alerts are NOT saved to database - they're instant notifications only
		// No need to check for existing alerts or create database records
		engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("preparing pre-alert notifications for call %d, tone set '%s' (not saving to database)", call.Id, matchedToneSet.Label))

		// Collect users who should get notifications for this tone set
		var eligibleUsers []uint64
		for _, user := range users {
			// Check if user wants this specific tone set
			// If user has no selected tone sets (empty map), they want all tone sets
			if len(user.selectedToneSetIds) == 0 {
				// User wants all tone sets
				eligibleUsers = append(eligibleUsers, user.userId)
				engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("pre-alert: user %d gets alert for '%s' (wants all tone sets)", user.userId, matchedToneSet.Label))
			} else if user.selectedToneSetIds[matchedToneSet.Id] {
				// User selected this specific tone set
				eligibleUsers = append(eligibleUsers, user.userId)
				engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("pre-alert: user %d gets alert for '%s' (selected this tone set)", user.userId, matchedToneSet.Label))
			} else {
				// User did NOT select this tone set - skip
				engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("pre-alert: user %d SKIPPED for '%s' (not in selected tone sets)", user.userId, matchedToneSet.Label))
			}
		}

		// Send batched push notification for all eligible users
		if len(eligibleUsers) > 0 {
			toneSetName := matchedToneSet.Label
			engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("sending pre-alert notifications to %d users for tone set '%s'", len(eligibleUsers), toneSetName))
			go engine.controller.sendBatchedPushNotificationWithToneSet(eligibleUsers, "pre-alert", call, systemLabel, talkgroupLabel, toneSetName, matchedToneSet.Id, nil)
		}
	}
}

// TriggerToneAlerts triggers alerts for tone-detected calls
// Creates one alert per matched tone set (for stacked tones from same or different calls)
func (engine *AlertEngine) TriggerToneAlerts(call *Call) {
	if call == nil || !call.HasTones {
		return
	}

	// Get all matched tone sets from this call
	matchedToneSets := call.ToneSequence.MatchedToneSets
	if len(matchedToneSets) == 0 {
		// Fallback to singular MatchedToneSet for backward compatibility
		if call.ToneSequence.MatchedToneSet == nil {
			return
		}
		matchedToneSets = []*ToneSet{call.ToneSequence.MatchedToneSet}
	}

	// Get all users with tone alerts enabled for this talkgroup from cache
	userIds := engine.controller.PreferencesCache.GetUsersForTalkgroup(call.System.Id, call.Talkgroup.Id)

	// Collect user preferences from cache
	type userPref struct {
		userId             uint64
		selectedToneSetIds map[string]bool // If empty, user wants all tone sets
	}
	var users []userPref

	for _, userId := range userIds {
		pref := engine.controller.PreferencesCache.GetPreference(userId, call.System.Id, call.Talkgroup.Id)
		if pref == nil || !pref.AlertEnabled || !pref.ToneAlerts {
			continue
		}

		userPreference := userPref{
			userId:             userId,
			selectedToneSetIds: make(map[string]bool),
		}

		// Use user's selected tone set IDs from cache
		if len(pref.ToneSetIds) > 0 {
			for _, id := range pref.ToneSetIds {
				userPreference.selectedToneSetIds[id] = true
			}
			// DEBUG: Log what user selected
			engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("tone alert: user %d has selected specific tone sets: %v", userId, pref.ToneSetIds))
		} else {
			// DEBUG: Log that user wants all tone sets
			engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("tone alert: user %d wants ALL tone sets (none selected)", userId))
		}

		users = append(users, userPreference)
	}

	// Get system and talkgroup labels once
	systemLabel := ""
	talkgroupLabel := ""
	if call.System != nil {
		systemLabel = call.System.Label
	}
	if call.Talkgroup != nil {
		talkgroupLabel = call.Talkgroup.Label
	}

	// Create one alert per matched tone set (not per user)
	for _, matchedToneSet := range matchedToneSets {
		if matchedToneSet == nil || matchedToneSet.Id == "" {
			continue
		}

		// DEBUG: Log detected tone set details
		engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("🔔 [TONE SET DEBUG] Tone alert processing: Detected tone set ID='%s', Label='%s' on call %d", matchedToneSet.Id, matchedToneSet.Label, call.Id))

		// Check if alert already exists for this call + tone set combination using cache
		// This prevents duplicate alerts if the function is called multiple times
		_, alertExists := engine.controller.RecentAlertsCache.AlertExists(
			call.Id, call.System.Id, call.Talkgroup.Id, "tone", matchedToneSet.Id, "")
		
		if !alertExists {
			// Create alert once for this tone set
			engine.createAlert(&AlertRecord{
				CallId:       call.Id,
				SystemId:     call.System.Id,
				TalkgroupId:  call.Talkgroup.Id,
				AlertType:    "tone",
				ToneDetected: true,
				ToneSetId:    matchedToneSet.Id,
				CreatedAt:    time.Now().UnixMilli(),
			})
		}

		// Forward to TonesToActive downstream (per-tone-set and/or global)
		dispatchToneDownstreams(engine.controller, call, matchedToneSet)

		// --- Per-talkgroup alert cooldown ---
		// If the talkgroup has a cooldown configured and an alert was already sent
		// within that window, skip push notifications (but keep the DB alert record
		// above so history is preserved).  cooldown == 0 means disabled.
		if call.Talkgroup != nil && call.Talkgroup.AlertCooldownSeconds > 0 {
			cooldown := time.Duration(call.Talkgroup.AlertCooldownSeconds) * time.Second
			engine.cooldownMu.Lock()
			last, hasPrior := engine.lastAlertFiredAt[call.Talkgroup.Id]
			engine.cooldownMu.Unlock()

			if hasPrior && time.Since(last) < cooldown {
				engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
					"tone alert cooldown active for talkgroup %d: last alert was %.0fs ago (cooldown=%.0fs) — skipping notifications for call %d",
					call.Talkgroup.Id, time.Since(last).Seconds(), cooldown.Seconds(), call.Id,
				))
				continue // next matched tone set; notifications suppressed
			}

			// Record this alert fire time
			engine.cooldownMu.Lock()
			engine.lastAlertFiredAt[call.Talkgroup.Id] = time.Now()
			engine.cooldownMu.Unlock()
		}

		// Collect users who should get notifications for this tone set
		var eligibleUsers []uint64
		for _, user := range users {
			// Check if user wants alerts for this tone set
			// If user has no specific tone set selection, they want all tone sets
			if len(user.selectedToneSetIds) == 0 {
				// User wants all tone sets
				engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("tone alert: user %d gets alert for '%s' (wants all tone sets)", user.userId, matchedToneSet.Label))
			} else if !user.selectedToneSetIds[matchedToneSet.Id] {
				// User did NOT select this tone set - skip
				engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("tone alert: user %d SKIPPED for '%s' (not in selected tone sets)", user.userId, matchedToneSet.Label))
				continue // User doesn't want alerts for this specific tone set
			} else {
				// User selected this specific tone set
				engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("tone alert: user %d gets alert for '%s' (selected this tone set)", user.userId, matchedToneSet.Label))
			}

			// Get user object to check delays
			userObj := engine.controller.Users.GetUserById(user.userId)
			if userObj == nil {
				continue
			}

			// Check if alert is delayed for this user
			defaultDelay := engine.controller.Options.DefaultSystemDelay
			effectiveDelay := engine.controller.userEffectiveDelay(userObj, call, defaultDelay)

			if effectiveDelay > 0 {
				delayCompletionTime := call.Timestamp.Add(time.Duration(effectiveDelay) * time.Minute)
				if time.Now().Before(delayCompletionTime) {
					// Alert is delayed for this user - schedule notification for when delay expires
					remainingDelay := time.Until(delayCompletionTime)
					go func(userId uint64, callId uint64, delay time.Duration) {
						time.Sleep(delay)
						engine.sendAlertNotification(userId, callId, "tone")
					}(user.userId, call.Id, remainingDelay)
				} else {
					// Delay has already expired - send notification immediately
					go engine.sendAlertNotification(user.userId, call.Id, "tone")
				}
			} else {
				// No delay - send notification immediately
				go engine.sendAlertNotification(user.userId, call.Id, "tone")
			}

			// Collect user for batched push notification (push notifications handle delays internally)
			eligibleUsers = append(eligibleUsers, user.userId)
		}

		// Check if keyword alerts exist for this call (to include keyword info in tone alerts)
		var keywordsMatched []string
		var keywordQuery string
		if engine.controller.Database.Config.DbType == DbTypePostgresql {
			keywordQuery = `SELECT "keywordsMatched" FROM "alerts" WHERE "callId" = $1 AND "systemId" = $2 AND "talkgroupId" = $3 AND "alertType" = 'keyword' LIMIT 1`
		} else {
			keywordQuery = `SELECT "keywordsMatched" FROM "alerts" WHERE "callId" = ? AND "systemId" = ? AND "talkgroupId" = ? AND "alertType" = 'keyword' LIMIT 1`
		}
		var keywordsJsonStr string
		if err := engine.controller.Database.Sql.QueryRow(keywordQuery, call.Id, call.System.Id, call.Talkgroup.Id).Scan(&keywordsJsonStr); err == nil {
			// Keyword alert exists - parse keywords to include in tone alert
			if keywordsJsonStr != "" && keywordsJsonStr != "[]" {
				json.Unmarshal([]byte(keywordsJsonStr), &keywordsMatched)
				engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("found keyword alert for call %d, will include keywords in tone alert: %v", call.Id, keywordsMatched))
			}
		}

		// Send batched push notification for all eligible users
		if len(eligibleUsers) > 0 {
			toneSetName := ""
			toneSetIdStr := ""
			if matchedToneSet != nil {
				toneSetName = matchedToneSet.Label
				toneSetIdStr = matchedToneSet.Id
			}
			// Include keywords if they were matched (tone alert with keyword info)
			go engine.controller.sendBatchedPushNotificationWithToneSet(eligibleUsers, "tone", call, systemLabel, talkgroupLabel, toneSetName, toneSetIdStr, keywordsMatched)
		}
	}
}

func (engine *AlertEngine) userMatchesToneSetFilter(toneSetIdsRaw string, call *Call) bool {
	if toneSetIdsRaw == "" || toneSetIdsRaw == "[]" {
		return true
	}

	var toneSetIds []string
	if err := json.Unmarshal([]byte(toneSetIdsRaw), &toneSetIds); err != nil || len(toneSetIds) == 0 {
		return true
	}

	if call == nil || call.ToneSequence == nil {
		return false
	}

	// Check all matched tone sets (for stacked tones)
	matchedToneSets := call.ToneSequence.MatchedToneSets
	if len(matchedToneSets) == 0 {
		// Fallback to singular MatchedToneSet for backward compatibility
		if call.ToneSequence.MatchedToneSet == nil {
			return false
		}
		matchedToneSets = []*ToneSet{call.ToneSequence.MatchedToneSet}
	}

	// Create a set of matched IDs for fast lookup
	matchedIds := make(map[string]bool)
	for _, ts := range matchedToneSets {
		if ts != nil && ts.Id != "" {
			matchedIds[ts.Id] = true
		}
	}

	// Check if any user-selected tone set matches any detected tone set
	for _, id := range toneSetIds {
		if matchedIds[id] {
			return true
		}
	}

	return false
}

// TriggerKeywordAlerts triggers alerts for keyword matches
// This is called per-user but creates alerts once per keyword match group
func (engine *AlertEngine) TriggerKeywordAlerts(callId uint64, systemId uint64, talkgroupId uint64, userId uint64, matches []KeywordMatch, transcript *TranscriptionResult) {
	if len(matches) == 0 {
		return
	}

	// Build keywords matched list
	keywordsMatched := make([]string, len(matches))
	for i, match := range matches {
		keywordsMatched[i] = match.Keyword
	}
	keywordsJson, _ := json.Marshal(keywordsMatched)
	keywordsJsonStr := string(keywordsJson)

	// Get transcript snippet (first 200 chars)
	transcriptSnippet := ""
	if transcript != nil && transcript.Transcript != "" {
		transcriptSnippet = transcript.Transcript
		if len(transcriptSnippet) > 200 {
			transcriptSnippet = transcriptSnippet[:200] + "..."
		}
	}

	// Check if any keyword alert already exists for this call — match on callId only,
	// not on the keyword set, so we never create a second row for the same call.
	var existingAlertId uint64
	var existingKeywordsJson string
	var checkQuery string
	if engine.controller.Database.Config.DbType == DbTypePostgresql {
		checkQuery = `SELECT "alertId", "keywordsMatched" FROM "alerts" WHERE "callId" = $1 AND "systemId" = $2 AND "talkgroupId" = $3 AND "alertType" = 'keyword' LIMIT 1`
	} else {
		checkQuery = `SELECT "alertId", "keywordsMatched" FROM "alerts" WHERE "callId" = ? AND "systemId" = ? AND "talkgroupId" = ? AND "alertType" = 'keyword' LIMIT 1`
	}
	if err := engine.controller.Database.Sql.QueryRow(checkQuery, callId, systemId, talkgroupId).Scan(&existingAlertId, &existingKeywordsJson); err == nil {
		// Alert already exists — merge any new keywords into it.
		mergedJson := mergeKeywordsJson(existingKeywordsJson, keywordsJsonStr)
		if mergedJson != existingKeywordsJson {
			var updateQuery string
			if engine.controller.Database.Config.DbType == DbTypePostgresql {
				updateQuery = `UPDATE "alerts" SET "keywordsMatched" = $1 WHERE "alertId" = $2`
			} else {
				updateQuery = `UPDATE "alerts" SET "keywordsMatched" = ? WHERE "alertId" = ?`
			}
			engine.controller.Database.Sql.Exec(updateQuery, mergedJson, existingAlertId)
		}
	} else {
		// No alert yet — create one.
		engine.createAlert(&AlertRecord{
			CallId:            callId,
			SystemId:          systemId,
			TalkgroupId:       talkgroupId,
			AlertType:         "keyword",
			ToneDetected:      false,
			KeywordsMatched:   keywordsJsonStr,
			TranscriptSnippet: transcriptSnippet,
			CreatedAt:         time.Now().UnixMilli(),
		})
	}

	// Get user object to check delays
	user := engine.controller.Users.GetUserById(userId)
	if user == nil {
		// If we can't get the user, send notification immediately (fallback)
		go engine.sendAlertNotification(userId, callId, "keyword")
		return
	}

	// Get system and talkgroup to build call object for delay check
	system, _ := engine.controller.Systems.GetSystemById(systemId)
	var talkgroup *Talkgroup
	if system != nil {
		talkgroup, _ = system.Talkgroups.GetTalkgroupById(talkgroupId)
	}

	if system != nil && talkgroup != nil {
		// Get call timestamp from database (avoid GetCall which checks global delays)
		var callTimestamp int64
		var tsQuery string
		if engine.controller.Database.Config.DbType == DbTypePostgresql {
			tsQuery = `SELECT "timestamp" FROM "calls" WHERE "callId" = $1`
		} else {
			tsQuery = `SELECT "timestamp" FROM "calls" WHERE "callId" = ?`
		}
		if err := engine.controller.Database.Sql.QueryRow(tsQuery, callId).Scan(&callTimestamp); err == nil {
			// Create minimal call object for delay check
			callTimestampTime := time.UnixMilli(callTimestamp)
			minimalCall := &Call{
				Id:        callId,
				System:    system,
				Talkgroup: talkgroup,
				Timestamp: callTimestampTime,
			}

			// Check if alert is delayed for this user
			defaultDelay := engine.controller.Options.DefaultSystemDelay
			effectiveDelay := engine.controller.userEffectiveDelay(user, minimalCall, defaultDelay)

			if effectiveDelay > 0 {
				delayCompletionTime := callTimestampTime.Add(time.Duration(effectiveDelay) * time.Minute)
				if time.Now().Before(delayCompletionTime) {
					// Alert is delayed for this user - schedule notification for when delay expires
					remainingDelay := time.Until(delayCompletionTime)
					go func(userId uint64, callId uint64, delay time.Duration) {
						time.Sleep(delay)
						engine.sendAlertNotification(userId, callId, "keyword")
					}(userId, callId, remainingDelay)
					return
				}
			}
		}
	}

	// No delay or delay expired - send notification immediately
	go engine.sendAlertNotification(userId, callId, "keyword")

	// Note: Push notifications for keyword alerts are now batched in transcription_queue.go
}

// TriggerToneAndKeywordAlerts triggers combined alerts
// Creates one alert per matched tone set that also has keywords matched
func (engine *AlertEngine) TriggerToneAndKeywordAlerts(call *Call, userId uint64, matches []KeywordMatch, transcript *TranscriptionResult) {
	if call == nil || !call.HasTones || len(matches) == 0 {
		return
	}

	// Get all matched tone sets from this call
	if call.ToneSequence == nil {
		return
	}
	matchedToneSets := call.ToneSequence.MatchedToneSets
	if len(matchedToneSets) == 0 {
		// Fallback to singular MatchedToneSet for backward compatibility
		if call.ToneSequence.MatchedToneSet == nil {
			return
		}
		matchedToneSets = []*ToneSet{call.ToneSequence.MatchedToneSet}
	}

	// Build keywords matched list
	keywordsMatched := make([]string, len(matches))
	for i, match := range matches {
		keywordsMatched[i] = match.Keyword
	}
	keywordsJson, _ := json.Marshal(keywordsMatched)

	// Get transcript snippet
	transcriptSnippet := ""
	if transcript != nil && transcript.Transcript != "" {
		transcriptSnippet = transcript.Transcript
		if len(transcriptSnippet) > 200 {
			transcriptSnippet = transcriptSnippet[:200] + "..."
		}
	}

	// Create one alert per matched tone set (for stacked tones)
	// Check if alerts already exist to prevent duplicates
	keywordsJsonStr := string(keywordsJson)
	for _, matchedToneSet := range matchedToneSets {
		if matchedToneSet == nil || matchedToneSet.Id == "" {
			continue
		}

		// Check if alert already exists for this call + tone set + keyword combination using cache
		_, alertExists := engine.controller.RecentAlertsCache.AlertExists(
			call.Id, call.System.Id, call.Talkgroup.Id, "tone+keyword", matchedToneSet.Id, keywordsJsonStr)
		
		if !alertExists {
			// Create alert once for this tone set + keywords combination
			engine.createAlert(&AlertRecord{
				CallId:            call.Id,
				SystemId:          call.System.Id,
				TalkgroupId:       call.Talkgroup.Id,
				AlertType:         "tone+keyword",
				ToneDetected:      true,
				ToneSetId:         matchedToneSet.Id,
				KeywordsMatched:   keywordsJsonStr,
				TranscriptSnippet: transcriptSnippet,
				CreatedAt:         time.Now().UnixMilli(),
			})
		}

		// Get user object to check delays
		user := engine.controller.Users.GetUserById(userId)
		if user != nil {
			// Check if alert is delayed for this user
			defaultDelay := engine.controller.Options.DefaultSystemDelay
			effectiveDelay := engine.controller.userEffectiveDelay(user, call, defaultDelay)

			if effectiveDelay > 0 {
				delayCompletionTime := call.Timestamp.Add(time.Duration(effectiveDelay) * time.Minute)
				if time.Now().Before(delayCompletionTime) {
					// Alert is delayed for this user - schedule notification for when delay expires
					remainingDelay := time.Until(delayCompletionTime)
					go func(userId uint64, callId uint64, delay time.Duration) {
						time.Sleep(delay)
						engine.sendAlertNotification(userId, callId, "tone+keyword")
					}(userId, call.Id, remainingDelay)
				} else {
					// Delay has already expired - send notification immediately
					go engine.sendAlertNotification(userId, call.Id, "tone+keyword")
				}
			} else {
				// No delay - send notification immediately
				go engine.sendAlertNotification(userId, call.Id, "tone+keyword")
			}
		} else {
			// If we can't get the user, send notification immediately (fallback)
			go engine.sendAlertNotification(userId, call.Id, "tone+keyword")
		}

		// Send push notification (push notifications handle delays internally)
		systemLabel := ""
		talkgroupLabel := ""
		if call.System != nil {
			systemLabel = call.System.Label
		}
		if call.Talkgroup != nil {
			talkgroupLabel = call.Talkgroup.Label
		}
		toneSetName := ""
		if matchedToneSet != nil {
			toneSetName = matchedToneSet.Label
		}
		go engine.controller.sendPushNotification(userId, "tone+keyword", call, systemLabel, talkgroupLabel, toneSetName, keywordsMatched)
	}
}

// AlertRecord represents an alert record in the database
type AlertRecord struct {
	AlertId           uint64 `json:"alertId"`
	CallId            uint64 `json:"callId"`
	SystemId          uint64 `json:"systemId"`
	TalkgroupId       uint64 `json:"talkgroupId"`
	AlertType         string `json:"alertType"` // "tone", "keyword", "tone+keyword"
	ToneDetected      bool   `json:"toneDetected"`
	ToneSetId         string `json:"toneSetId"`       // ID of the tone set that triggered this alert (empty for keyword-only alerts)
	KeywordsMatched   string `json:"keywordsMatched"` // JSON array
	TranscriptSnippet string `json:"transcriptSnippet"`
	CreatedAt         int64  `json:"createdAt"`
}

// createAlert creates an alert in the database
func (engine *AlertEngine) createAlert(alert *AlertRecord) {
	var query string
	if engine.controller.Database.Config.DbType == DbTypePostgresql {
		query = `INSERT INTO "alerts" ("callId", "systemId", "talkgroupId", "alertType", "toneDetected", "toneSetId", "keywordsMatched", "transcriptSnippet", "createdAt") VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING "alertId"`
		var alertId uint64
		if err := engine.controller.Database.Sql.QueryRow(query, alert.CallId, alert.SystemId, alert.TalkgroupId, alert.AlertType, alert.ToneDetected, alert.ToneSetId, alert.KeywordsMatched, alert.TranscriptSnippet, alert.CreatedAt).Scan(&alertId); err != nil {
			engine.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to create alert: %v", err))
			return
		}
		alert.AlertId = alertId
	} else {
		query = `INSERT INTO "alerts" ("callId", "systemId", "talkgroupId", "alertType", "toneDetected", "toneSetId", "keywordsMatched", "transcriptSnippet", "createdAt") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
		result, err := engine.controller.Database.Sql.Exec(query, alert.CallId, alert.SystemId, alert.TalkgroupId, alert.AlertType, alert.ToneDetected, alert.ToneSetId, alert.KeywordsMatched, alert.TranscriptSnippet, alert.CreatedAt)
		if err != nil {
			engine.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to create alert: %v", err))
			return
		}
		if id, err := result.LastInsertId(); err == nil {
			alert.AlertId = uint64(id)
		}
	}

	engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("alert created: id=%d, call=%d, type=%s", alert.AlertId, alert.CallId, alert.AlertType))

	// Add alert to cache for duplicate prevention
	engine.controller.RecentAlertsCache.AddAlert(
		alert.AlertId, alert.CallId, alert.SystemId, alert.TalkgroupId, 
		alert.AlertType, alert.ToneSetId, alert.KeywordsMatched)

	// Debug log
	if engine.controller.DebugLogger != nil {
		details := fmt.Sprintf("AlertID=%d", alert.AlertId)
		if alert.ToneSetId != "" {
			details += fmt.Sprintf(" ToneSet=%s", alert.ToneSetId)
		}
		if alert.KeywordsMatched != "" {
			details += fmt.Sprintf(" Keywords=%s", alert.KeywordsMatched)
		}
		engine.controller.DebugLogger.LogAlert(alert.AlertType, alert.CallId, alert.SystemId, alert.TalkgroupId, details)
	}

	// Cleanup old alerts at most once per hour. The scheduler also runs this
	// hourly, so this guard prevents a goroutine storm when many alerts fire
	// in a short window (e.g. mass keyword hits or tone storms).
	now := time.Now().Unix()
	last := engine.lastCleanupUnix.Load()
	if now-last >= 3600 && engine.lastCleanupUnix.CompareAndSwap(last, now) {
		go engine.cleanupOldAlerts()
	}
}

// sendAlertNotification sends a WebSocket notification to the user.
// The clients map lock is held only long enough to snapshot matching clients;
// the actual channel sends happen after the lock is released so that slow or
// full client queues cannot block other goroutines from acquiring the lock.
func (engine *AlertEngine) sendAlertNotification(userId uint64, callId uint64, alertType string) {
	// Snapshot matching clients under the shortest possible lock window.
	engine.controller.Clients.mutex.Lock()
	var targets []*Client
	for client := range engine.controller.Clients.Map {
		if client.User != nil && client.User.Id == userId {
			targets = append(targets, client)
		}
	}
	engine.controller.Clients.mutex.Unlock()

	if len(targets) == 0 {
		return
	}

	notification := map[string]any{
		"type":      "alert",
		"callId":    callId,
		"alertType": alertType,
	}
	msg := &Message{Command: MessageCommandAlert, Payload: notification}
	for _, client := range targets {
		select {
		case client.Send <- msg:
		default:
			// Channel full, skip
		}
	}
}

// cleanupOldAlerts removes alerts older than retention days
func (engine *AlertEngine) cleanupOldAlerts() {
	retentionDays := engine.controller.Options.AlertRetentionDays
	if retentionDays == 0 {
		retentionDays = 5 // Default: 5 days
	}

	cutoffTime := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour).UnixMilli()
	var query string
	if engine.controller.Database.Config.DbType == DbTypePostgresql {
		query = `DELETE FROM "alerts" WHERE "createdAt" < $1`
	} else {
		query = `DELETE FROM "alerts" WHERE "createdAt" < ?`
	}

	if _, err := engine.controller.Database.Sql.Exec(query, cutoffTime); err != nil {
		engine.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to cleanup old alerts: %v", err))
	}

	// Prune lastAlertFiredAt entries that are older than the longest possible cooldown
	// (capped at 24 hours) so the map doesn't grow unboundedly over time.
	pruneBefore := time.Now().Add(-24 * time.Hour)
	engine.cooldownMu.Lock()
	for talkgroupId, firedAt := range engine.lastAlertFiredAt {
		if firedAt.Before(pruneBefore) {
			delete(engine.lastAlertFiredAt, talkgroupId)
		}
	}
	engine.cooldownMu.Unlock()
}

// mergeKeywordsJson merges two JSON keyword arrays, deduplicating entries.
// Returns the merged JSON string. Falls back to existingJson on any parse error.
func mergeKeywordsJson(existingJson, newJson string) string {
	var existing, incoming []string
	json.Unmarshal([]byte(existingJson), &existing)
	json.Unmarshal([]byte(newJson), &incoming)

	seen := make(map[string]struct{}, len(existing)+len(incoming))
	merged := make([]string, 0, len(existing)+len(incoming))
	for _, kw := range append(existing, incoming...) {
		if _, ok := seen[kw]; !ok {
			seen[kw] = struct{}{}
			merged = append(merged, kw)
		}
	}
	if result, err := json.Marshal(merged); err == nil {
		return string(result)
	}
	return existingJson
}
