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
	"time"
)

// SystemAlert represents a system-level alert for administrators
type SystemAlert struct {
	Id        uint64 `json:"id"`
	AlertType string `json:"alertType"` // "transcription_failure", "tone_detection_issue", "service_health", "manual"
	Severity  string `json:"severity"`  // "info", "warning", "error", "critical"
	Title     string `json:"title"`
	Message   string `json:"message"`
	Data      string `json:"data"` // JSON data for additional context
	CreatedAt int64  `json:"createdAt"`
	CreatedBy uint64 `json:"createdBy"` // User ID who created it (0 for system-generated)
	Dismissed bool   `json:"dismissed"`
}

// SystemAlertData represents the parsed Data field
type SystemAlertData struct {
	CallId           uint64 `json:"callId,omitempty"`
	SystemId         uint64 `json:"systemId,omitempty"`
	SystemLabel      string `json:"systemLabel,omitempty"`
	TalkgroupId      uint64 `json:"talkgroupId,omitempty"`
	Error            string `json:"error,omitempty"`
	Count            int    `json:"count,omitempty"`
	Service          string `json:"service,omitempty"`
	Threshold        int    `json:"threshold,omitempty"`
	LastCallTime     int64  `json:"lastCallTime,omitempty"`
	MinutesSinceLast int    `json:"minutesSinceLast,omitempty"`
}

// CreateSystemAlert creates a new system alert
func (controller *Controller) CreateSystemAlert(alertType, severity, title, message string, data *SystemAlertData, createdBy uint64) error {
	var dataJSON string
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			dataJSON = "{}"
		} else {
			dataJSON = string(b)
		}
	} else {
		dataJSON = "{}"
	}

	createdAt := time.Now().UnixMilli()

	var query string
	if createdBy > 0 {
		query = fmt.Sprintf(`INSERT INTO "systemAlerts" ("alertType", "severity", "title", "message", "data", "createdAt", "createdBy") VALUES ('%s', '%s', '%s', '%s', '%s', %d, %d)`,
			escapeQuotes(alertType), escapeQuotes(severity), escapeQuotes(title), escapeQuotes(message), escapeQuotes(dataJSON), createdAt, createdBy)
	} else {
		query = fmt.Sprintf(`INSERT INTO "systemAlerts" ("alertType", "severity", "title", "message", "data", "createdAt") VALUES ('%s', '%s', '%s', '%s', '%s', %d)`,
			escapeQuotes(alertType), escapeQuotes(severity), escapeQuotes(title), escapeQuotes(message), escapeQuotes(dataJSON), createdAt)
	}

	if _, err := controller.Database.Sql.Exec(query); err != nil {
		return fmt.Errorf("failed to create system alert: %v", err)
	}

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("System alert created: [%s] %s - %s", severity, title, message))

	// Send push notification to all system admins
	go controller.SendSystemAlertNotification(title, message, alertType, severity, dataJSON)

	return nil
}

// SendSystemAlertNotification sends a push notification for system alerts
// Manual alerts (sent by admins) go to all verified users
// Health monitoring alerts only go to system admins
func (controller *Controller) SendSystemAlertNotification(title, message, alertType, severity, dataJSON string) {
	var query string
	var targetDescription string

	if alertType == "manual" {
		// Manual alerts: send to ALL verified users
		query = `SELECT "userId" FROM "users" WHERE "verified" = true`
		targetDescription = "verified users"
	} else {
		// Health/monitoring alerts: only send to system admins
		query = `SELECT "userId" FROM "users" WHERE "systemAdmin" = true`
		targetDescription = "system admins"
	}

	rows, err := controller.Database.Sql.Query(query)
	if err != nil {
		controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("failed to get %s: %v", targetDescription, err))
		return
	}
	defer rows.Close()

	var targetUserIds []uint64
	for rows.Next() {
		var userId uint64
		if err := rows.Scan(&userId); err != nil {
			continue
		}
		targetUserIds = append(targetUserIds, userId)
	}

	if len(targetUserIds) == 0 {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("no %s found to send system alert notification", targetDescription))
		return
	}

	// Get device tokens for target users
	var playerIds []string
	for _, userId := range targetUserIds {
		tokens := controller.DeviceTokens.GetByUser(userId)
		for _, token := range tokens {
			if token.Token != "" {
				playerIds = append(playerIds, token.Token)
			}
		}
	}

	if len(playerIds) == 0 {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("no device tokens found for %s", targetDescription))
		return
	}

	// Determine icon and sound based on severity
	icon := "🔔"
	sound := "startup.wav"
	switch severity {
	case "critical":
		icon = "🚨"
		sound = "startup.wav" // Could be customized per severity
	case "error":
		icon = "❌"
		sound = "startup.wav"
	case "warning":
		icon = "⚠️"
		sound = "startup.wav"
	case "info":
		icon = "ℹ️"
		sound = "startup.wav"
	}

	// Group player IDs by platform (if we had platform info, but we don't store it in device tokens lookup)
	// For now, send to all devices using the batch system
	notificationTitle := fmt.Sprintf("%s System Alert", icon)

	// Send to all player IDs at once
	// The relay server will handle the actual platform-specific formatting
	if len(playerIds) > 0 {
		go controller.sendNotificationBatch(playerIds, notificationTitle, title, message, "android", sound, nil, "", "")
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("[%s] system alert notification sent to %d device(s) (%s)", alertType, len(playerIds), targetDescription))
	}
}

// GetSystemAlerts retrieves system alerts (optionally filtered by dismissed status)
func (controller *Controller) GetSystemAlerts(limit int, includeDismissed bool) ([]*SystemAlert, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	var query string
	if includeDismissed {
		query = fmt.Sprintf(`SELECT "alertId", "alertType", "severity", "title", "message", "data", "createdAt", COALESCE("createdBy", 0), "dismissed" FROM "systemAlerts" ORDER BY "createdAt" DESC LIMIT %d`, limit)
	} else {
		query = fmt.Sprintf(`SELECT "alertId", "alertType", "severity", "title", "message", "data", "createdAt", COALESCE("createdBy", 0), "dismissed" FROM "systemAlerts" WHERE "dismissed" = false ORDER BY "createdAt" DESC LIMIT %d`, limit)
	}

	rows, err := controller.Database.Sql.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query system alerts: %v", err)
	}
	defer rows.Close()

	var alerts []*SystemAlert
	for rows.Next() {
		alert := &SystemAlert{}
		if err := rows.Scan(&alert.Id, &alert.AlertType, &alert.Severity, &alert.Title, &alert.Message, &alert.Data, &alert.CreatedAt, &alert.CreatedBy, &alert.Dismissed); err != nil {
			continue
		}
		alerts = append(alerts, alert)
	}

	return alerts, nil
}

// DismissSystemAlert marks a system alert as dismissed
func (controller *Controller) DismissSystemAlert(alertId uint64) error {
	query := fmt.Sprintf(`UPDATE "systemAlerts" SET "dismissed" = true WHERE "alertId" = %d`, alertId)
	if _, err := controller.Database.Sql.Exec(query); err != nil {
		return fmt.Errorf("failed to dismiss system alert: %v", err)
	}
	return nil
}

// CleanupOldSystemAlerts removes system alerts older than retention days
func (controller *Controller) CleanupOldSystemAlerts() {
	retentionDays := controller.Options.AlertRetentionDays
	if retentionDays == 0 {
		retentionDays = 5 // Default: 5 days
	}

	cutoffTime := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour).UnixMilli()
	var query string
	if controller.Database.Config.DbType == DbTypePostgresql {
		query = `DELETE FROM "systemAlerts" WHERE "createdAt" < $1`
	} else {
		query = `DELETE FROM "systemAlerts" WHERE "createdAt" < ?`
	}

	result, err := controller.Database.Sql.Exec(query, cutoffTime)
	if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to cleanup old system alerts: %v", err))
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected > 0 {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("cleaned up %d old system alerts (older than %d days)", rowsAffected, retentionDays))
	}
}

// getProviderDisplayName converts a provider string to a user-friendly display name
func getProviderDisplayName(provider string) string {
	switch provider {
	case "whisper-api":
		return "Whisper API Server"
	case "azure":
		return "Azure Speech Services"
	case "google":
		return "Google Cloud Speech-to-Text"
	case "assemblyai":
		return "AssemblyAI"
	default:
		// Default fallback if provider is unknown or empty
		if provider == "" {
			return "transcription service"
		}
		return provider
	}
}

// MonitorTranscriptionFailures monitors for transcription failures and creates system alerts
func (controller *Controller) MonitorTranscriptionFailures() {
	// Check if transcription failure alerts are enabled
	if !controller.Options.TranscriptionFailureAlertsEnabled || !controller.Options.SystemHealthAlertsEnabled {
		return
	}

	// Get configurable time window (default 24 hours)
	timeWindowHours := int(controller.Options.TranscriptionFailureTimeWindow)
	if timeWindowHours <= 0 {
		timeWindowHours = 24
	}
	timeWindowAgo := time.Now().Add(-time.Duration(timeWindowHours) * time.Hour).UnixMilli()

	query := fmt.Sprintf(`SELECT COUNT(*) FROM "calls" WHERE "transcriptionStatus" = 'failed' AND "timestamp" >= %d`, timeWindowAgo)

	var failureCount int
	if err := controller.Database.Sql.QueryRow(query).Scan(&failureCount); err != nil {
		controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("failed to check transcription failures: %v", err))
		return
	}

	// Use configurable threshold, default to 10
	threshold := int(controller.Options.TranscriptionFailureThreshold)
	if threshold <= 0 {
		threshold = 10
	}

	// If we have more than threshold failures in last 24 hours, create an alert
	if failureCount >= threshold && controller.Options.SystemHealthAlertsEnabled {
		// Check if there's already an active alert for transcription failures
		// Only create a new alert if the last one is older than the repeat interval
		repeatMinutes := int(controller.Options.TranscriptionFailureRepeatMinutes)
		if repeatMinutes <= 0 {
			repeatMinutes = 60 // Default: 60 minutes
		}

		checkAlertQuery := `SELECT MAX("createdAt") FROM "systemAlerts" 
			WHERE "alertType" = 'transcription_failure' 
				AND "dismissedAt" IS NULL`

		var lastAlertTime sql.NullInt64
		shouldCreateAlert := true
		if err := controller.Database.Sql.QueryRow(checkAlertQuery).Scan(&lastAlertTime); err == nil && lastAlertTime.Valid {
			lastAlertTimeObj := time.UnixMilli(lastAlertTime.Int64)
			minutesSinceLastAlert := int(time.Since(lastAlertTimeObj).Minutes())
			// Only create new alert if last one is older than repeat interval
			if minutesSinceLastAlert < repeatMinutes {
				shouldCreateAlert = false
			}
		}

		if shouldCreateAlert {
			// Get provider name for the alert message
			providerName := getProviderDisplayName(controller.Options.TranscriptionConfig.Provider)

			data := &SystemAlertData{
				Count:   failureCount,
				Service: "transcription",
			}

			timeWindowStr := fmt.Sprintf("%d hour(s)", timeWindowHours)
			if timeWindowHours == 24 {
				timeWindowStr = "24 hours"
			}
			controller.CreateSystemAlert(
				"transcription_failure",
				"warning",
				"Transcription Service Issues",
				fmt.Sprintf("%d transcription failures detected in the last %s. Check %s service status.", failureCount, timeWindowStr, providerName),
				data,
				0, // System-generated
			)
		}
	}
}

// MonitorToneDetectionIssues monitors for tone detection problems
func (controller *Controller) MonitorToneDetectionIssues() {
	// Check if tone detection alerts are enabled
	if !controller.Options.ToneDetectionAlertsEnabled || !controller.Options.SystemHealthAlertsEnabled {
		return
	}

	// Get configurable time window (default 24 hours)
	timeWindowHours := int(controller.Options.ToneDetectionTimeWindow)
	if timeWindowHours <= 0 {
		timeWindowHours = 24
	}
	timeWindowAgo := time.Now().Add(-time.Duration(timeWindowHours) * time.Hour).UnixMilli()

	// Get talkgroups with tone detection enabled
	query := `SELECT "talkgroupId", "label", "systemId" FROM "talkgroups" WHERE "toneDetectionEnabled" = true`
	rows, err := controller.Database.Sql.Query(query)
	if err != nil {
		controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("failed to check tone detection: %v", err))
		return
	}
	defer rows.Close()

	for rows.Next() {
		var talkgroupId, systemId uint64
		var label string
		if err := rows.Scan(&talkgroupId, &label, &systemId); err != nil {
			continue
		}

		// Check if this talkgroup has had any calls with tones in the time window
		checkQuery := fmt.Sprintf(`SELECT COUNT(*) FROM "calls" WHERE "talkgroupId" = %d AND "hasTones" = true AND "timestamp" >= %d`, talkgroupId, timeWindowAgo)

		var toneCount int
		if err := controller.Database.Sql.QueryRow(checkQuery).Scan(&toneCount); err != nil {
			continue
		}

		// Also check if there have been ANY calls on this talkgroup
		callCountQuery := fmt.Sprintf(`SELECT COUNT(*) FROM "calls" WHERE "talkgroupId" = %d AND "timestamp" >= %d`, talkgroupId, timeWindowAgo)

		var callCount int
		if err := controller.Database.Sql.QueryRow(callCountQuery).Scan(&callCount); err != nil {
			continue
		}

		// Only alert if there have been calls but no tones (might indicate tone detection issue)
		threshold := int(controller.Options.ToneDetectionIssueThreshold)
		if threshold <= 0 {
			threshold = 5 // Default: 5 calls
		}
		if callCount >= threshold && toneCount == 0 {
			// Check if there's already an active alert for this talkgroup
			// Only create a new alert if the last one is older than the repeat interval
			repeatMinutes := int(controller.Options.ToneDetectionRepeatMinutes)
			if repeatMinutes <= 0 {
				repeatMinutes = 60 // Default: 60 minutes
			}

			checkAlertQuery := fmt.Sprintf(`
				SELECT MAX("createdAt") FROM "systemAlerts" 
				WHERE "alertType" = 'tone_detection_issue' 
					AND "data" LIKE '%%"talkgroupId":%d%%'
					AND "dismissedAt" IS NULL
			`, talkgroupId)

			var lastAlertTime sql.NullInt64
			shouldCreateAlert := true
			if err := controller.Database.Sql.QueryRow(checkAlertQuery).Scan(&lastAlertTime); err == nil && lastAlertTime.Valid {
				lastAlertTimeObj := time.UnixMilli(lastAlertTime.Int64)
				minutesSinceLastAlert := int(time.Since(lastAlertTimeObj).Minutes())
				// Only create new alert if last one is older than repeat interval
				if minutesSinceLastAlert < repeatMinutes {
					shouldCreateAlert = false
				}
			}

			if shouldCreateAlert {
				data := &SystemAlertData{
					TalkgroupId: talkgroupId,
					SystemId:    systemId,
					Count:       callCount,
				}

				timeWindowStr := fmt.Sprintf("%d hour(s)", timeWindowHours)
				if timeWindowHours == 24 {
					timeWindowStr = "24 hours"
				}
				controller.CreateSystemAlert(
					"tone_detection_issue",
					"info",
					"No Tones Detected",
					fmt.Sprintf("Talkgroup '%s' has tone detection enabled but no tones detected in %d calls over %s.", label, callCount, timeWindowStr),
					data,
					0, // System-generated
				)
			}
		}
	}
}

// MonitorNoAudioForSystem monitors a specific system for lack of audio activity
func (controller *Controller) MonitorNoAudioForSystem(systemId uint64, systemLabel string, thresholdMinutes uint) {
	// Check if no-audio alerts are enabled globally
	if !controller.Options.NoAudioAlertsEnabled || !controller.Options.SystemHealthAlertsEnabled {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("no-audio monitoring skipped for system '%s' (ID: %d) - globally disabled", systemLabel, systemId))
		return
	}

	currentTime := time.Now()

	// Query for the most recent call for this system
	var lastCallTime sql.NullInt64
	callQuery := `SELECT MAX("timestamp") FROM "calls" WHERE "systemId" = $1`
	if err := controller.Database.Sql.QueryRow(callQuery, systemId).Scan(&lastCallTime); err != nil {
		controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("failed to query last call time for system %d: %v", systemId, err))
		return
	}

	var timeSinceLastCall time.Duration
	var lastCallTimeMs int64
	
	// If no calls found, treat as infinite time since last call
	if !lastCallTime.Valid {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("no-audio monitoring: system '%s' (ID: %d) has no calls in database - will create alert", systemLabel, systemId))
		// Set to a very old time to ensure alert is triggered
		timeSinceLastCall = time.Duration(365*24) * time.Hour // 1 year
		lastCallTimeMs = 0
	} else {
		// Convert timestamp to time
		lastCall := time.Unix(lastCallTime.Int64/1000, 0)
		timeSinceLastCall = currentTime.Sub(lastCall)
		lastCallTimeMs = lastCallTime.Int64
		
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("no-audio check: system '%s' (ID: %d) last call was %d minutes ago (threshold: %d minutes)", 
			systemLabel, systemId, int(timeSinceLastCall.Minutes()), thresholdMinutes))
	}

	// Check if time since last call exceeds threshold
	thresholdDuration := time.Duration(thresholdMinutes) * time.Minute
	if timeSinceLastCall > thresholdDuration {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("no-audio threshold exceeded for system '%s' (ID: %d): %d minutes since last call (threshold: %d minutes)", 
			systemLabel, systemId, int(timeSinceLastCall.Minutes()), thresholdMinutes))
		
		// Check for existing alert
		repeatMinutes := int(controller.Options.NoAudioRepeatMinutes)
		if repeatMinutes <= 0 {
			repeatMinutes = 30 // Default: 30 minutes
		}

		// Check if we already have a recent alert for this system
		checkAlertQuery := fmt.Sprintf(`
			SELECT MAX("createdAt") FROM "systemAlerts" 
			WHERE "alertType" = 'no_audio' 
				AND "data" LIKE '%%"systemId":%d%%'
				AND "dismissedAt" IS NULL
		`, systemId)

		var lastAlertTime sql.NullInt64
		shouldCreateAlert := true
		repeatThreshold := currentTime.Add(-time.Duration(repeatMinutes) * time.Minute).UnixMilli()
		if err := controller.Database.Sql.QueryRow(checkAlertQuery).Scan(&lastAlertTime); err == nil && lastAlertTime.Valid {
			// Only create new alert if last one is older than repeat interval
			if lastAlertTime.Int64 > repeatThreshold {
				shouldCreateAlert = false
				minutesSinceLastAlert := int(currentTime.Sub(time.UnixMilli(lastAlertTime.Int64)).Minutes())
				controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("skipping no-audio alert for system '%s' (ID: %d) - alert created %d minutes ago (repeat interval: %d minutes)", 
					systemLabel, systemId, minutesSinceLastAlert, repeatMinutes))
			}
		}

		if shouldCreateAlert {
			// Dismiss any existing no-audio alerts for this system before creating new one
			// This keeps only the latest alert instead of accumulating them
			dismissQuery := fmt.Sprintf(`
				UPDATE "systemAlerts" 
				SET "dismissedAt" = %d, "dismissed" = true 
				WHERE "alertType" = 'no_audio' 
					AND "data" LIKE '%%"systemId":%d%%'
					AND "dismissedAt" IS NULL
			`, currentTime.UnixMilli(), systemId)
			if _, err := controller.Database.Sql.Exec(dismissQuery); err != nil {
				controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("failed to dismiss old no-audio alerts for system %d: %v", systemId, err))
			}

			data := &SystemAlertData{
				SystemId:         systemId,
				SystemLabel:      systemLabel,
				Threshold:        int(thresholdMinutes),
				LastCallTime:     lastCallTimeMs,
				MinutesSinceLast: int(timeSinceLastCall.Minutes()),
			}

			title := "No Audio Received"
			var message string
			if lastCallTimeMs == 0 {
				message = fmt.Sprintf("System '%s' has no calls in database (threshold: %d minutes)",
					systemLabel,
					thresholdMinutes)
			} else {
				message = fmt.Sprintf("System '%s' has not received audio for %d minutes (threshold: %d minutes)",
					systemLabel,
					int(timeSinceLastCall.Minutes()),
					thresholdMinutes)
			}

			if err := controller.CreateSystemAlert(
				"no_audio",
				"warning",
				title,
				message,
				data,
				0, // System-generated
			); err != nil {
				controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("failed to create no-audio alert for system '%s' (ID: %d): %v", systemLabel, systemId, err))
			} else {
				controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("created no-audio alert for system '%s' (ID: %d) - %d minutes since last call", systemLabel, systemId, int(timeSinceLastCall.Minutes())))
			}
		}
	} else {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("no-audio check OK: system '%s' (ID: %d) within threshold - %d minutes since last call (threshold: %d minutes)", 
			systemLabel, systemId, int(timeSinceLastCall.Minutes()), thresholdMinutes))
	}
}

// StartNoAudioMonitoringForAllSystems starts per-system no-audio monitoring with individual timers
func (controller *Controller) StartNoAudioMonitoringForAllSystems() {
	// Check if no-audio alerts are enabled globally
	if !controller.Options.NoAudioAlertsEnabled || !controller.Options.SystemHealthAlertsEnabled {
		controller.Logs.LogEvent(LogLevelInfo, "no-audio monitoring is disabled")
		return
	}

	// Get all systems with their no-audio alert settings
	query := `SELECT "systemId", "label", "noAudioAlertsEnabled", "noAudioThresholdMinutes" FROM "systems"`
	rows, err := controller.Database.Sql.Query(query)
	if err != nil {
		controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("failed to query systems for no-audio monitoring: %v", err))
		return
	}
	defer rows.Close()

	for rows.Next() {
		var systemId uint64
		var systemLabel string
		var noAudioAlertsEnabled bool
		var thresholdMinutes uint
		if err := rows.Scan(&systemId, &systemLabel, &noAudioAlertsEnabled, &thresholdMinutes); err != nil {
			continue
		}

		// Skip systems with no-audio alerts disabled
		if !noAudioAlertsEnabled {
			continue
		}

		// Use system-specific threshold (defaults to 30 minutes if not set)
		if thresholdMinutes == 0 {
			thresholdMinutes = 30
		}

		// Start monitoring for this system with its own interval
		go controller.StartNoAudioMonitoringForSystem(systemId, systemLabel, thresholdMinutes)
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("started no-audio monitoring for system '%s' (ID: %d) with %d minute threshold", systemLabel, systemId, thresholdMinutes))
	}
}

// StartNoAudioMonitoringForSystem starts monitoring a specific system with its own timer
func (controller *Controller) StartNoAudioMonitoringForSystem(systemId uint64, systemLabel string, thresholdMinutes uint) {
	ticker := time.NewTicker(time.Duration(thresholdMinutes) * time.Minute)
	defer ticker.Stop()

	// Run initial check immediately
	controller.MonitorNoAudioForSystem(systemId, systemLabel, thresholdMinutes)

	// Then check at the threshold interval
	for range ticker.C {
		// Re-check if monitoring is still enabled
		if !controller.Options.NoAudioAlertsEnabled || !controller.Options.SystemHealthAlertsEnabled {
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("stopping no-audio monitoring for system '%s' (ID: %d) - globally disabled", systemLabel, systemId))
			return
		}

		// Re-check system settings from database in case they changed
		var noAudioAlertsEnabled bool
		var currentThresholdMinutes uint
		checkQuery := `SELECT "noAudioAlertsEnabled", "noAudioThresholdMinutes" FROM "systems" WHERE "systemId" = $1`
		if err := controller.Database.Sql.QueryRow(checkQuery, systemId).Scan(&noAudioAlertsEnabled, &currentThresholdMinutes); err != nil {
			controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("failed to check system settings for system %d: %v", systemId, err))
			continue
		}

		// If alerts disabled for this system, stop monitoring
		if !noAudioAlertsEnabled {
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("stopping no-audio monitoring for system '%s' (ID: %d) - disabled for this system", systemLabel, systemId))
			return
		}

		// If threshold changed, restart with new interval
		if currentThresholdMinutes != thresholdMinutes && currentThresholdMinutes > 0 {
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("restarting no-audio monitoring for system '%s' (ID: %d) - threshold changed from %d to %d minutes", systemLabel, systemId, thresholdMinutes, currentThresholdMinutes))
			// Stop this ticker and start a new one
			ticker.Stop()
			go controller.StartNoAudioMonitoringForSystem(systemId, systemLabel, currentThresholdMinutes)
			return
		}

		// Run the check
		controller.MonitorNoAudioForSystem(systemId, systemLabel, thresholdMinutes)
	}
}

// RestartNoAudioMonitoringForSystem restarts monitoring for a specific system
// This should be called when system no-audio settings are updated via admin interface
func (controller *Controller) RestartNoAudioMonitoringForSystem(systemId uint64) {
	// Get current system settings
	var systemLabel string
	var noAudioAlertsEnabled bool
	var thresholdMinutes uint
	
	query := `SELECT "label", "noAudioAlertsEnabled", "noAudioThresholdMinutes" FROM "systems" WHERE "systemId" = $1`
	if err := controller.Database.Sql.QueryRow(query, systemId).Scan(&systemLabel, &noAudioAlertsEnabled, &thresholdMinutes); err != nil {
		controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("failed to query system for no-audio monitoring restart: %v", err))
		return
	}

	// If alerts are disabled or threshold is 0, nothing to start
	if !noAudioAlertsEnabled || thresholdMinutes == 0 {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("no-audio monitoring not started for system '%s' (ID: %d) - disabled or invalid threshold", systemLabel, systemId))
		return
	}

	// Start monitoring with new settings (the goroutine will detect setting changes and adapt)
	go controller.StartNoAudioMonitoringForSystem(systemId, systemLabel, thresholdMinutes)
	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("restarted no-audio monitoring for system '%s' (ID: %d) with %d minute threshold", systemLabel, systemId, thresholdMinutes))
}

// StartSystemHealthMonitoring starts periodic system health checks
func (controller *Controller) StartSystemHealthMonitoring() {
	// Start hourly checks for transcription failures and tone detection issues
	ticker := time.NewTicker(1 * time.Hour) // Check every hour
	go func() {
		for range ticker.C {
			controller.MonitorTranscriptionFailures()
			controller.MonitorToneDetectionIssues()
		}
	}()

	// Start per-system no-audio monitoring with individual timers
	go controller.StartNoAudioMonitoringForAllSystems()

	controller.Logs.LogEvent(LogLevelInfo, "system health monitoring started")
}
