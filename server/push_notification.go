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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// isLegacyOneSignalToken returns true for device tokens that were registered via
// OneSignal and can no longer receive notifications through the FCM-only pipeline.
func isLegacyOneSignalToken(dt *DeviceToken) bool {
	return dt.FCMToken == "" || dt.PushType == "onesignal"
}

// handleLegacyOneSignalToken deletes the stale OneSignal token from the database and,
// if the owning user has an email address and hasn't been notified yet this call,
// sends them an app-update email. The notifiedUsers set prevents sending multiple
// emails when a user has several legacy devices.
func (controller *Controller) handleLegacyOneSignalToken(dt *DeviceToken, notifiedUsers map[uint64]struct{}) {
	// Delete from DB + memory
	if err := controller.DeviceTokens.Delete(dt.Id, controller.Database); err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf(
			"push notification: failed to delete legacy token %d for user %d: %v", dt.Id, dt.UserId, err))
	} else {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
			"push notification: deleted legacy token %d for user %d (no FCM token)", dt.Id, dt.UserId))
	}

	// Send one email per user, regardless of how many legacy devices they have.
	if _, alreadySent := notifiedUsers[dt.UserId]; alreadySent {
		return
	}
	notifiedUsers[dt.UserId] = struct{}{}

	user := controller.Users.GetUserById(dt.UserId)
	if user == nil || user.Email == "" {
		return
	}

	go func() {
		if err := controller.EmailService.SendAppUpdateRequiredEmail(user); err != nil {
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf(
				"push notification: failed to send app update email to user %d (%s): %v",
				user.Id, user.Email, err))
		}
	}()
}

// sendPushNotification sends a push notification to the relay server
func (controller *Controller) sendPushNotification(userId uint64, alertType string, call *Call, systemLabel, talkgroupLabel string, toneSetName string, keywords []string) {
	// Check if relay server API key is configured (URL is hardcoded)
	if controller.Options.RelayServerAPIKey == "" {
		return // Push notifications not configured
	}

	// Get user
	user := controller.Users.GetUserById(userId)
	if user == nil {
		return
	}

	// Note: Group suspension check removed as Suspended field was not added to UserGroup
	// If needed, can be added later

	// Check billing/subscription status if billing is enabled on user's group
	if user.UserGroupId > 0 {
		group := controller.UserGroups.Get(user.UserGroupId)
		if group != nil && group.BillingEnabled {
			var subscriptionStatus string

			if group.BillingMode == "group_admin" {
				// O(1) lookup via pre-built index instead of scanning all users.
				if admin := controller.Users.GetGroupAdmin(group.Id); admin != nil {
					subscriptionStatus = admin.SubscriptionStatus
				}
				// If no admin found, leave empty → grace period (allow notification)
			} else {
				subscriptionStatus = user.SubscriptionStatus
			}

			if subscriptionStatus != "" && subscriptionStatus != "not_billed" {
				if subscriptionStatus != "active" && subscriptionStatus != "trialing" {
					return
				}
			}
		}
	}

	// Check if call is still delayed for this user (respects group delays)
	if call != nil && call.System != nil && call.Talkgroup != nil {
		defaultDelay := controller.Options.DefaultSystemDelay
		effectiveDelay := controller.userEffectiveDelay(user, call, defaultDelay)

		// Check if call is still delayed
		if effectiveDelay > 0 {
			delayCompletionTime := call.Timestamp.Add(time.Duration(effectiveDelay) * time.Minute)
			if time.Now().Before(delayCompletionTime) {
				// Call is still delayed for this user, don't send push notification
				return
			}
		}
	}

	// Get user's device tokens
	deviceTokens := controller.DeviceTokens.GetByUser(userId)
	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: retrieved %d device token(s) for user %d", len(deviceTokens), userId))
	if len(deviceTokens) == 0 {
		return // No devices registered
	}

	// Log all tokens being processed
	for i, device := range deviceTokens {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: device %d for user %d - token: %s, platform: %s", i+1, userId, device.Token, device.Platform))
	}

	// Build notification title and message
	// Title: System name / Channel name (+ Tone Set name for tone alerts)
	title := ""
	baseTitle := ""
	if systemLabel != "" && talkgroupLabel != "" {
		baseTitle = fmt.Sprintf("%s / %s", strings.ToUpper(systemLabel), strings.ToUpper(talkgroupLabel))
	} else if systemLabel != "" {
		baseTitle = strings.ToUpper(systemLabel)
	} else if talkgroupLabel != "" {
		baseTitle = strings.ToUpper(talkgroupLabel)
	} else {
		baseTitle = "RADIO ALERT"
	}
	if toneSetName != "" && (alertType == "pre-alert" || alertType == "tone" || alertType == "tone+keyword") {
		title = fmt.Sprintf("%s - %s", baseTitle, strings.ToUpper(toneSetName))
	} else {
		title = baseTitle
	}

	// Message: use summary if available and not generic "RADIO TRAFFIC", otherwise use transcript
	message := ""
	if call != nil && call.Transcript != "" {
		message = strings.ToUpper(call.Transcript)
	} else {
		// Fallback to alert type info if no transcript
		if alertType == "pre-alert" {
			// Pre-alert: Tones detected, waiting for voice
			currentTime := time.Now().Format("3:04 PM")
			if toneSetName != "" {
				message = fmt.Sprintf("%s Tones Detected @ %s", strings.ToUpper(toneSetName), currentTime)
			} else {
				message = fmt.Sprintf("Tones Detected @ %s", currentTime)
			}
		} else if alertType == "tone" {
			if len(keywords) > 0 {
				// Tone alert with keywords - include keyword info
				keywordText := strings.ToUpper(keywords[0])
				if toneSetName != "" {
					message = fmt.Sprintf("%s + KEYWORD: %s", strings.ToUpper(toneSetName), keywordText)
				} else {
					message = fmt.Sprintf("TONE + KEYWORD: %s", keywordText)
				}
			} else {
				// Tone alert without keywords
				if toneSetName != "" {
					message = fmt.Sprintf("%s DETECTED", strings.ToUpper(toneSetName))
				} else {
					message = "TONE ALERT"
				}
			}
		} else if alertType == "keyword" {
			if len(keywords) > 0 {
				message = fmt.Sprintf("KEYWORD MATCH: %s", strings.ToUpper(keywords[0]))
			} else {
				message = "KEYWORD ALERT"
			}
		} else if alertType == "tone+keyword" {
			keywordText := ""
			if len(keywords) > 0 {
				keywordText = strings.ToUpper(keywords[0])
			}
			if toneSetName != "" {
				message = fmt.Sprintf("%s + KEYWORD: %s", strings.ToUpper(toneSetName), keywordText)
			} else {
				message = fmt.Sprintf("TONE + KEYWORD: %s", keywordText)
			}
		}
	}

	// Resolve per-channel notification sound for this user+talkgroup.
	var systemId, talkgroupId uint64
	if call != nil {
		if call.System != nil {
			systemId = call.System.Id
		}
		if call.Talkgroup != nil {
			talkgroupId = call.Talkgroup.Id
		}
	}
	channelSound := controller.resolveUserAlertSound(userId, systemId, talkgroupId, "")

	// Group devices by platform and sound preference.
	// Legacy OneSignal tokens are deleted on the spot and the user is emailed once.
	androidDevices := []string{}
	iosDevices := []string{}
	androidSound := "startup.wav"
	iosSound := "startup.wav"
	notifiedUsers := make(map[uint64]struct{})

	for _, device := range deviceTokens {
		if isLegacyOneSignalToken(device) {
			controller.handleLegacyOneSignalToken(device, notifiedUsers)
			continue
		}

		// Sound priority: per-channel override → device default → fallback
		effectiveSound := channelSound
		if effectiveSound == "" {
			effectiveSound = device.Sound
		}
		if effectiveSound == "" {
			effectiveSound = "startup.wav"
		}

		if device.Platform == "ios" {
			iosDevices = append(iosDevices, device.FCMToken)
			iosSound = effectiveSound
		} else {
			androidDevices = append(androidDevices, device.FCMToken)
			androidSound = effectiveSound
		}
	}

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: grouped devices for user %d - Android: %d, iOS: %d", userId, len(androidDevices), len(iosDevices)))

	// Send to Android devices
	if len(androidDevices) > 0 {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: sending to %d Android device(s) for user %d with sound: %s", len(androidDevices), userId, androidSound))
		// Send in goroutine to ensure independent execution - failures don't affect other batches
		go func(ids []string, sound string) {
			controller.sendNotificationBatch(ids, title, "", message, "android", sound, call, systemLabel, talkgroupLabel, nil)
		}(androidDevices, androidSound)
	}

	// Send to iOS devices
	if len(iosDevices) > 0 {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: sending to %d iOS device(s) for user %d", len(iosDevices), userId))
		// iOS requires sound name without extension (e.g., "startup" not "startup.wav")
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: iOS original sound: %s", iosSound))
		iosSoundStripped := strings.TrimSuffix(iosSound, ".wav")
		iosSoundStripped = strings.TrimSuffix(iosSoundStripped, ".mp3")
		iosSoundStripped = strings.TrimSuffix(iosSoundStripped, ".m4a")
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: iOS final sound (stripped extension): %s", iosSoundStripped))
		// Send in goroutine to ensure independent execution - failures don't affect other batches
		go func(ids []string, sound string) {
			controller.sendNotificationBatch(ids, title, "", message, "ios", sound, call, systemLabel, talkgroupLabel, nil)
		}(iosDevices, iosSoundStripped)
	}
}

func (controller *Controller) sendNotificationBatch(playerIDs []string, title, subtitle, message, platform, sound string, call *Call, systemLabel, talkgroupLabel string, extraData map[string]interface{}) {
	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: sendNotificationBatch called with %d player ID(s) for %s platform", len(playerIDs), platform))
	for i, playerID := range playerIDs {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: player ID %d: %s", i+1, playerID))
	}

	// Build payload data (keep existing structure)
	data := map[string]interface{}{}

	// Merge any extra data provided by the caller first (callers can override defaults).
	for k, v := range extraData {
		data[k] = v
	}

	if call != nil {
		data["callId"] = call.Id
		if call.System != nil {
			data["systemId"] = call.System.Id
			if systemLabel == "" {
				systemLabel = call.System.Label
			}
		}
		if call.Talkgroup != nil {
			data["talkgroupId"] = call.Talkgroup.Id
			if talkgroupLabel == "" {
				talkgroupLabel = call.Talkgroup.Label
			}
		}
	}

	if systemLabel != "" {
		data["systemLabel"] = systemLabel
	}
	if talkgroupLabel != "" {
		data["talkgroupLabel"] = talkgroupLabel
	}

	// Build request payload
	payload := map[string]interface{}{
		"player_ids": playerIDs,
		"title":      title,
		"message":    message,
		"data":       data,
		"platform":   platform,
		"sound":      sound,
	}

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: sending batch with %d FCM token(s) to relay server", len(playerIDs)))

	// Add subtitle if provided
	if subtitle != "" {
		payload["subtitle"] = subtitle
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: adding subtitle '%s' to payload for %s platform", subtitle, platform))
	} else {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: no subtitle for %s platform", platform))
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to marshal push notification: %v", err))
		return
	}

	// Send to relay server (hardcoded URL)
	relayServerURL := "https://tlradioserver.thinlineds.com"
	url := fmt.Sprintf("%s/api/notify", relayServerURL)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to create push notification request: %v", err))
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", controller.Options.RelayServerAPIKey))

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: sending HTTP request to relay server: %s", url))
	resp, err := client.Do(req)
	if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to send push notification: %v", err))
		return
	}
	defer resp.Body.Close()

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: received response from relay server (status %d)", resp.StatusCode))
	body, _ := io.ReadAll(resp.Body)
	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: response body: %s", string(body)))

	// Parse response to check for invalid player IDs and failures
	var response struct {
		Success          bool     `json:"success"`
		Recipients       int      `json:"recipients"`
		Failed           int      `json:"failed"`
		Errors           []string `json:"errors"`
		InvalidPlayerIDs []string `json:"invalid_player_ids"` // Player IDs that don't exist in relay server
		Error            string   `json:"error"`              // Error message for non-200 responses
	}

	if err := json.Unmarshal(body, &response); err != nil {
		// Fallback if response parsing fails
		if resp.StatusCode != http.StatusOK {
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("push notification failed (status %d): %s - this failure does not affect other batches", resp.StatusCode, string(body)))
		} else {
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification sent to %d %s devices", len(playerIDs), platform))
		}
		return
	}

	// Handle invalid FCM tokens — relay server reports tokens it could not deliver to.
	// O(1) per token via tokenIndex; no need to scan all users.
	if len(response.InvalidPlayerIDs) > 0 {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("push notification: removing %d invalid FCM token(s) from user accounts", len(response.InvalidPlayerIDs)))
		notifiedUsers := make(map[uint64]struct{})
		for _, invalidToken := range response.InvalidPlayerIDs {
			dt := controller.DeviceTokens.GetByToken(invalidToken)
			if dt == nil {
				controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: invalid FCM token not found in index (already removed?)"))
				continue
			}
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: removing invalid FCM token for user %d", dt.UserId))
			if err := controller.DeviceTokens.Delete(dt.Id, controller.Database); err != nil {
				controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("push notification: failed to remove invalid FCM token for user %d: %v", dt.UserId, err))
			}
			// Email the user once so they know to re-register their device.
			controller.handleLegacyOneSignalToken(dt, notifiedUsers)
		}
	}

	if resp.StatusCode != http.StatusOK {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("push notification failed (status %d): %s - this failure does not affect other batches", resp.StatusCode, response.Error))
		return
	}

	// Handle successful response
	if response.Failed > 0 {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("push notification partially failed: %d sent, %d failed to %s devices. Errors: %v", response.Recipients, response.Failed, platform, response.Errors))
	} else {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification sent to %d %s devices", response.Recipients, platform))
	}
}

// sendDisconnectPushNotification sends a push notification to a user's devices
// when their WebSocket connection to the TLR server is dropped.
func (controller *Controller) sendDisconnectPushNotification(user *User) {
	if controller.Options.RelayServerAPIKey == "" {
		return
	}

	deviceTokens := controller.DeviceTokens.GetByUser(user.Id)
	if len(deviceTokens) == 0 {
		return
	}

	serverName := controller.Options.Branding
	if serverName == "" {
		serverName = "TLR Server"
	}

	title := "DISCONNECTED"
	message := fmt.Sprintf("You have been disconnected from %s", strings.ToUpper(serverName))

	// Read the user's chosen disconnect alert sound (set via the mobile app's
	// Notification Sounds screen). Falls back to the device default then "startup.wav".
	disconnectSound := ""
	if user.Settings != "" {
		var userSettings map[string]interface{}
		if err := json.Unmarshal([]byte(user.Settings), &userSettings); err == nil {
			if s, ok := userSettings["disconnectAlertSound"].(string); ok && s != "" {
				disconnectSound = s
			}
		}
	}

	androidDevices := []string{}
	iosDevices := []string{}
	androidSound := disconnectSound
	iosSound := disconnectSound
	notifiedUsers := make(map[uint64]struct{})

	for _, device := range deviceTokens {
		if isLegacyOneSignalToken(device) {
			controller.handleLegacyOneSignalToken(device, notifiedUsers)
			continue
		}
		// Fall back to device default sound if no disconnect-specific sound is set.
		effectiveSound := disconnectSound
		if effectiveSound == "" {
			effectiveSound = device.Sound
		}
		if effectiveSound == "" {
			effectiveSound = "startup.wav"
		}
		if device.Platform == "ios" {
			iosDevices = append(iosDevices, device.FCMToken)
			iosSound = effectiveSound
		} else {
			androidDevices = append(androidDevices, device.FCMToken)
			androidSound = effectiveSound
		}
	}

	if androidSound == "" {
		androidSound = "startup.wav"
	}
	if iosSound == "" {
		iosSound = "startup.wav"
	}

	disconnectExtra := map[string]interface{}{
		"type":                 "disconnect",
		"notification_message": "false",
	}

	if len(androidDevices) > 0 {
		go func(ids []string, sound string) {
			controller.sendNotificationBatch(ids, title, "", message, "android", sound, nil, "", "", disconnectExtra)
		}(androidDevices, androidSound)
	}

	if len(iosDevices) > 0 {
		iosSoundStripped := strings.TrimSuffix(iosSound, ".wav")
		iosSoundStripped = strings.TrimSuffix(iosSoundStripped, ".mp3")
		iosSoundStripped = strings.TrimSuffix(iosSoundStripped, ".m4a")
		go func(ids []string, sound string) {
			controller.sendNotificationBatch(ids, title, "", message, "ios", sound, nil, "", "", disconnectExtra)
		}(iosDevices, iosSoundStripped)
	}
}

// resolveUserAlertSound returns the notification sound for a specific user+talkgroup alert.
// Priority: per-tone-set sound → per-channel sound → "" (use device default).
func (controller *Controller) resolveUserAlertSound(userId, systemId, talkgroupId uint64, toneSetId string) string {
	var notificationSound, toneSetSoundsJson string
	query := `SELECT COALESCE("notificationSound",''), COALESCE("toneSetSounds",'{}') FROM "userAlertPreferences" WHERE "userId" = $1 AND "systemId" = $2 AND "talkgroupId" = $3 LIMIT 1`
	if err := controller.Database.Sql.QueryRow(query, userId, systemId, talkgroupId).Scan(&notificationSound, &toneSetSoundsJson); err != nil {
		return ""
	}
	// Check per-tone-set sound first
	if toneSetId != "" && toneSetSoundsJson != "" && toneSetSoundsJson != "{}" {
		var toneSetSounds map[string]string
		if err := json.Unmarshal([]byte(toneSetSoundsJson), &toneSetSounds); err == nil {
			if s, ok := toneSetSounds[toneSetId]; ok && s != "" {
				return s
			}
		}
	}
	return notificationSound
}

// sendBatchedPushNotification sends push notifications to multiple users in a single batch
// Groups device tokens by platform and sound preference, then sends batched notifications
func (controller *Controller) sendBatchedPushNotification(userIds []uint64, alertType string, call *Call, systemLabel, talkgroupLabel string, toneSetName string, keywords []string) {
	controller.sendBatchedPushNotificationWithToneSet(userIds, alertType, call, systemLabel, talkgroupLabel, toneSetName, "", keywords)
}

// sendBatchedPushNotificationWithToneSet is the full implementation that accepts a toneSetId
// so per-tone-set notification sounds can be resolved from each user's alert preferences.
func (controller *Controller) sendBatchedPushNotificationWithToneSet(userIds []uint64, alertType string, call *Call, systemLabel, talkgroupLabel string, toneSetName string, toneSetId string, keywords []string) {
	// Check if relay server API key is configured (URL is hardcoded)
	if controller.Options.RelayServerAPIKey == "" {
		return // Push notifications not configured
	}

	// Build notification title and message (same for all users)
	// Title: System name / Channel name (+ Tone Set name for tone alerts)
	title := ""
	baseTitle := ""
	if systemLabel != "" && talkgroupLabel != "" {
		baseTitle = fmt.Sprintf("%s / %s", strings.ToUpper(systemLabel), strings.ToUpper(talkgroupLabel))
	} else if systemLabel != "" {
		baseTitle = strings.ToUpper(systemLabel)
	} else if talkgroupLabel != "" {
		baseTitle = strings.ToUpper(talkgroupLabel)
	} else {
		baseTitle = "RADIO ALERT"
	}
	if toneSetName != "" && (alertType == "pre-alert" || alertType == "tone" || alertType == "tone+keyword") {
		title = fmt.Sprintf("%s - %s", baseTitle, strings.ToUpper(toneSetName))
	} else {
		title = baseTitle
	}

	// Message: use summary if available and not generic "RADIO TRAFFIC", otherwise use transcript
	message := ""
	if call != nil && call.Transcript != "" {
		message = strings.ToUpper(call.Transcript)
	} else {
		// Fallback to alert type info if no transcript
		if alertType == "pre-alert" {
			// Pre-alert: Tones detected, waiting for voice
			currentTime := time.Now().Format("3:04 PM")
			if toneSetName != "" {
				message = fmt.Sprintf("%s Tones Detected @ %s", strings.ToUpper(toneSetName), currentTime)
			} else {
				message = fmt.Sprintf("Tones Detected @ %s", currentTime)
			}
		} else if alertType == "tone" {
			if len(keywords) > 0 {
				// Tone alert with keywords - include keyword info
				keywordText := strings.ToUpper(keywords[0])
				if toneSetName != "" {
					message = fmt.Sprintf("%s + KEYWORD: %s", strings.ToUpper(toneSetName), keywordText)
				} else {
					message = fmt.Sprintf("TONE + KEYWORD: %s", keywordText)
				}
			} else {
				// Tone alert without keywords
				if toneSetName != "" {
					message = fmt.Sprintf("%s DETECTED", strings.ToUpper(toneSetName))
				} else {
					message = "TONE ALERT"
				}
			}
		} else if alertType == "keyword" {
			if len(keywords) > 0 {
				message = fmt.Sprintf("KEYWORD MATCH: %s", strings.ToUpper(keywords[0]))
			} else {
				message = "KEYWORD ALERT"
			}
		} else if alertType == "tone+keyword" {
			keywordText := ""
			if len(keywords) > 0 {
				keywordText = strings.ToUpper(keywords[0])
			}
			if toneSetName != "" {
				message = fmt.Sprintf("%s + KEYWORD: %s", strings.ToUpper(toneSetName), keywordText)
			} else {
				message = fmt.Sprintf("TONE + KEYWORD: %s", keywordText)
			}
		}
	}

	// Collect all device tokens from all users, grouped by platform and sound.
	// Key: "platform:sound" -> []FCM tokens
	// Legacy OneSignal tokens are deleted on the spot and the user is emailed once.
	deviceGroups := make(map[string][]string)
	notifiedUsers := make(map[uint64]struct{})

	for _, userId := range userIds {
		user := controller.Users.GetUserById(userId)
		if user == nil {
			continue
		}

		// Check billing/subscription status if billing is enabled on user's group
		if user.UserGroupId > 0 {
			group := controller.UserGroups.Get(user.UserGroupId)
			if group != nil && group.BillingEnabled {
				var subscriptionStatus string

			if group.BillingMode == "group_admin" {
				// O(1) lookup via pre-built index instead of scanning all users.
				if admin := controller.Users.GetGroupAdmin(group.Id); admin != nil {
					subscriptionStatus = admin.SubscriptionStatus
				}
				// If no admin found, leave empty → grace period (allow notification)
				} else {
					// For all_users mode, check the user's own subscription status
					subscriptionStatus = user.SubscriptionStatus
				}

				// Block push notification if subscription status exists and is not active or trialing
				// Allow if status is empty/not_billed (grace period or no billing set up yet)
				if subscriptionStatus != "" && subscriptionStatus != "not_billed" {
					if subscriptionStatus != "active" && subscriptionStatus != "trialing" {
						continue // Block push notification - subscription not active
					}
				}
			}
		}

		// Check if call is still delayed for this user (respects group delays)
		if call != nil && call.System != nil && call.Talkgroup != nil {
			defaultDelay := controller.Options.DefaultSystemDelay
			effectiveDelay := controller.userEffectiveDelay(user, call, defaultDelay)

			// Check if call is still delayed
			if effectiveDelay > 0 {
				delayCompletionTime := call.Timestamp.Add(time.Duration(effectiveDelay) * time.Minute)
				if time.Now().Before(delayCompletionTime) {
					// Call is still delayed for this user, skip push notification
					continue
				}
			}
		}

		// Get user's device tokens
		deviceTokens := controller.DeviceTokens.GetByUser(userId)
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification (batched): retrieved %d device token(s) for user %d", len(deviceTokens), userId))
		if len(deviceTokens) == 0 {
			continue // No devices registered
		}

		// Log all tokens being processed
		for i, device := range deviceTokens {
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification (batched): device %d for user %d - token: %s, platform: %s", i+1, userId, device.Token, device.Platform))
		}

		// Resolve per-channel / per-tone-set sound for this user.
		// Falls back to "" which means "use device default" below.
		var systemId, talkgroupId uint64
		if call != nil {
			if call.System != nil {
				systemId = call.System.Id
			}
			if call.Talkgroup != nil {
				talkgroupId = call.Talkgroup.Id
			}
		}
		channelSound := controller.resolveUserAlertSound(userId, systemId, talkgroupId, toneSetId)

		// Group devices by platform and sound; delete any legacy OneSignal tokens.
		for _, device := range deviceTokens {
			if isLegacyOneSignalToken(device) {
				controller.handleLegacyOneSignalToken(device, notifiedUsers)
				continue
			}

			// Sound priority: per-tone-set/per-channel → device default → fallback
			sound := channelSound
			if sound == "" {
				sound = device.Sound
			}
			if sound == "" {
				sound = "startup.wav"
			}
			key := fmt.Sprintf("%s:%s", device.Platform, sound)
			deviceGroups[key] = append(deviceGroups[key], device.FCMToken)
		}
	}

	// Send batched notifications for each platform/sound combination
	batchIndex := 0
	for key, playerIDs := range deviceGroups {
		if len(playerIDs) == 0 {
			continue
		}

		// Parse platform and sound from key
		parts := strings.Split(key, ":")
		if len(parts) != 2 {
			continue
		}
		platform := parts[0]
		sound := parts[1]

		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification (batched): sending batch with %d player ID(s) for %s platform, sound: %s", len(playerIDs), platform, sound))

		// iOS requires sound name without extension (e.g., "startup" not "startup.wav")
		finalSound := sound
		if platform == "ios" {
			finalSound = strings.TrimSuffix(sound, ".wav")
			finalSound = strings.TrimSuffix(finalSound, ".mp3")
			finalSound = strings.TrimSuffix(finalSound, ".m4a")
		}

		// Send each platform/sound batch independently so failures don't affect others.
		// Stagger batches slightly to avoid relay-server rate limiting.
		delay := time.Duration(batchIndex) * 200 * time.Millisecond
		go func(ids []string, plat string, snd string, d time.Duration) {
			if d > 0 {
				time.Sleep(d)
			}
			controller.sendNotificationBatch(ids, title, "", message, plat, snd, call, systemLabel, talkgroupLabel, nil)
		}(playerIDs, platform, finalSound, delay)
		batchIndex++
	}
}
