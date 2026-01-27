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
				// For group_admin mode, check the group admin's subscription status
				// Find an admin in the group
				allUsers := controller.Users.GetAllUsers()
				foundAdmin := false
				for _, admin := range allUsers {
					if admin.UserGroupId == group.Id && admin.IsGroupAdmin {
						subscriptionStatus = admin.SubscriptionStatus
						foundAdmin = true
						break
					}
				}
				// If no admin found, allow (grace period)
				if !foundAdmin {
					subscriptionStatus = ""
				}
			} else {
				// For all_users mode, check the user's own subscription status
				subscriptionStatus = user.SubscriptionStatus
			}

			// Block push notification if subscription status exists and is not active or trialing
			// Allow if status is empty/not_billed (grace period or no billing set up yet)
			if subscriptionStatus != "" && subscriptionStatus != "not_billed" {
				if subscriptionStatus != "active" && subscriptionStatus != "trialing" {
					return // Block push notification - subscription not active
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
	// Title: System name / Channel name
	title := ""
	if systemLabel != "" && talkgroupLabel != "" {
		title = fmt.Sprintf("%s / %s", strings.ToUpper(systemLabel), strings.ToUpper(talkgroupLabel))
	} else if systemLabel != "" {
		title = strings.ToUpper(systemLabel)
	} else if talkgroupLabel != "" {
		title = strings.ToUpper(talkgroupLabel)
	} else {
		title = "RADIO ALERT"
	}

	// Message: Full transcript if available, otherwise fallback to alert type info
	message := ""
	if call != nil && call.Transcript != "" && len(call.Transcript) > 0 {
		// Use full transcript
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

	// Group devices by platform and sound preference
	androidDevices := []string{}
	iosDevices := []string{}
	androidSound := "startup.wav"
	iosSound := "startup.wav"

	for _, device := range deviceTokens {
		// Use FCM token if available (new system), otherwise fall back to OneSignal token (legacy)
		token := device.FCMToken
		if token == "" {
			token = device.Token
		}

		if device.Platform == "ios" {
			iosDevices = append(iosDevices, token)
			// Use this device's sound preference for iOS
			if device.Sound != "" {
				iosSound = device.Sound
			}
		} else {
			androidDevices = append(androidDevices, token)
			// Use this device's sound preference for Android
			if device.Sound != "" {
				androidSound = device.Sound
			}
		}
	}

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: grouped devices for user %d - Android: %d, iOS: %d", userId, len(androidDevices), len(iosDevices)))

	// Build subtitle for tone alerts
	subtitle := ""
	if alertType == "pre-alert" || alertType == "tone" || alertType == "tone+keyword" {
		if toneSetName != "" {
			subtitle = strings.ToUpper(toneSetName)
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: setting subtitle '%s' for %s alert", subtitle, alertType))
		} else {
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: toneSetName is empty for %s alert, no subtitle", alertType))
		}
	} else {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: alertType is '%s', no subtitle needed", alertType))
	}

	// Send to Android devices
	if len(androidDevices) > 0 {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: sending to %d Android device(s) for user %d with sound: %s", len(androidDevices), userId, androidSound))
		// Send in goroutine to ensure independent execution - failures don't affect other batches
		go func(ids []string, sound string) {
			controller.sendNotificationBatch(ids, title, subtitle, message, "android", sound, call, systemLabel, talkgroupLabel)
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
			controller.sendNotificationBatch(ids, title, subtitle, message, "ios", sound, call, systemLabel, talkgroupLabel)
		}(iosDevices, iosSoundStripped)
	}
}

func (controller *Controller) sendNotificationBatch(playerIDs []string, title, subtitle, message, platform, sound string, call *Call, systemLabel, talkgroupLabel string) {
	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: sendNotificationBatch called with %d player ID(s) for %s platform", len(playerIDs), platform))
	for i, playerID := range playerIDs {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: player ID %d: %s", i+1, playerID))
	}

	// Build payload data (keep existing structure)
	data := map[string]interface{}{}

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

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: sending batch with %d player_ids to relay server", len(playerIDs)))

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

	// Handle invalid player IDs - remove them from user accounts
	if len(response.InvalidPlayerIDs) > 0 {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("push notification: removing %d invalid OneSignal ID(s) from user accounts: %v", len(response.InvalidPlayerIDs), response.InvalidPlayerIDs))
		for _, invalidPlayerID := range response.InvalidPlayerIDs {
			// Find and remove the device token with this OneSignal ID
			allUsers := controller.Users.GetAllUsers()
			for _, user := range allUsers {
				deviceTokens := controller.DeviceTokens.GetByUser(user.Id)
				for _, deviceToken := range deviceTokens {
					if deviceToken.Token == invalidPlayerID {
						controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: removing invalid OneSignal ID %s from user %d", invalidPlayerID, user.Id))
						if err := controller.DeviceTokens.Delete(deviceToken.Id, controller.Database); err != nil {
							controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("push notification: failed to remove invalid OneSignal ID %s from user %d: %v", invalidPlayerID, user.Id, err))
						} else {
							controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification: successfully removed invalid OneSignal ID %s from user %d", invalidPlayerID, user.Id))
						}
						break // Found and removed, move to next invalid ID
					}
				}
			}
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

// sendBatchedPushNotification sends push notifications to multiple users in a single batch
// Groups device tokens by platform and sound preference, then sends batched notifications
func (controller *Controller) sendBatchedPushNotification(userIds []uint64, alertType string, call *Call, systemLabel, talkgroupLabel string, toneSetName string, keywords []string) {
	// Check if relay server API key is configured (URL is hardcoded)
	if controller.Options.RelayServerAPIKey == "" {
		return // Push notifications not configured
	}

	// Build notification title and message (same for all users)
	// Title: System name / Channel name
	title := ""
	if systemLabel != "" && talkgroupLabel != "" {
		title = fmt.Sprintf("%s / %s", strings.ToUpper(systemLabel), strings.ToUpper(talkgroupLabel))
	} else if systemLabel != "" {
		title = strings.ToUpper(systemLabel)
	} else if talkgroupLabel != "" {
		title = strings.ToUpper(talkgroupLabel)
	} else {
		title = "RADIO ALERT"
	}

	// Message: Full transcript if available, otherwise fallback to alert type info
	message := ""
	if call != nil && call.Transcript != "" && len(call.Transcript) > 0 {
		// Use full transcript
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

	// Collect all device tokens from all users, grouped by platform and sound
	// Key: "platform:sound" -> []playerIDs
	deviceGroups := make(map[string][]string)

	for _, userId := range userIds {
		// Get user
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
					// For group_admin mode, check the group admin's subscription status
					// Find an admin in the group
					allUsers := controller.Users.GetAllUsers()
					foundAdmin := false
					for _, admin := range allUsers {
						if admin.UserGroupId == group.Id && admin.IsGroupAdmin {
							subscriptionStatus = admin.SubscriptionStatus
							foundAdmin = true
							break
						}
					}
					// If no admin found, allow (grace period)
					if !foundAdmin {
						subscriptionStatus = ""
					}
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

		// Group devices by platform and sound
		for _, device := range deviceTokens {
			// Use FCM token if available (new system), otherwise fall back to OneSignal token (legacy)
			token := device.FCMToken
			if token == "" {
				token = device.Token
			}

			sound := device.Sound
			if sound == "" {
				sound = "startup.wav"
			}
			key := fmt.Sprintf("%s:%s", device.Platform, sound)
			deviceGroups[key] = append(deviceGroups[key], token)
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

		// Build subtitle for tone alerts
		subtitle := ""
		if alertType == "tone" || alertType == "tone+keyword" {
			if toneSetName != "" {
				subtitle = strings.ToUpper(toneSetName)
			}
		}

		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("push notification (batched): sending batch with %d player ID(s) for %s platform, sound: %s", len(playerIDs), platform, sound))

		// iOS requires sound name without extension (e.g., "startup" not "startup.wav")
		finalSound := sound
		if platform == "ios" {
			finalSound = strings.TrimSuffix(sound, ".wav")
			finalSound = strings.TrimSuffix(finalSound, ".mp3")
			finalSound = strings.TrimSuffix(finalSound, ".m4a")
		}

		// Send batch notification in goroutine to ensure independent execution
		// Each batch is sent independently, so failures in one don't affect others
		// Add small delay between batches to avoid OneSignal rate limiting (especially on free plan)
		delay := time.Duration(batchIndex) * 200 * time.Millisecond
		go func(ids []string, plat string, snd string, d time.Duration) {
			if d > 0 {
				time.Sleep(d)
			}
			controller.sendNotificationBatch(ids, title, subtitle, message, plat, snd, call, systemLabel, talkgroupLabel)
		}(playerIDs, platform, finalSound, delay)
		batchIndex++
	}
}
