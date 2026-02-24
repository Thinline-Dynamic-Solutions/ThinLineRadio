// Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
// Modified by Thinline Dynamic Solutions
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
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"math"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/stripe/stripe-go/v74"
	"github.com/stripe/stripe-go/v74/customer"
	"golang.org/x/crypto/bcrypt"
)

// IsLocalhostIP checks if an IP address is localhost
func IsLocalhostIP(ip string) bool {
	ip = strings.TrimSpace(ip)

	if ip == "127.0.0.1" || ip == "::1" || ip == "localhost" || ip == "" {
		return true
	}

	if strings.HasPrefix(ip, "127.") {
		return true
	}

	if strings.HasPrefix(ip, "::ffff:127.") {
		return true
	}

	if ip == "[::1]" || ip == "[127.0.0.1]" {
		return true
	}

	return false
}

// GetClientIP extracts the client IP from the request
func GetClientIP(r *http.Request) string {
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		ips := strings.Split(forwarded, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}

	realIP := r.Header.Get("X-Real-IP")
	if realIP != "" {
		return realIP
	}

	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}

type Admin struct {
	Attempts         AdminLoginAttempts
	AttemptsMax      uint
	AttemptsMaxDelay time.Duration
	Broadcast        chan *[]byte
	Conns            map[*websocket.Conn]bool
	Controller       *Controller
	Register         chan *websocket.Conn
	Tokens           []string
	Unregister       chan *websocket.Conn
	mutex            sync.Mutex
	running          bool
}

// requireLocalhost middleware for admin routes
func (admin *Admin) requireLocalhost(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clientIP := GetClientIP(r)
		isLocalhost := IsLocalhostIP(clientIP)

		if admin.Controller.Options.AdminLocalhostOnly {
			if !isLocalhost {
				log.Printf("Admin access denied from non-localhost IP: %s for route: %s", clientIP, r.URL.Path)
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{
					"error": "Admin access restricted to localhost only",
				})
				return
			}
		}
		next(w, r)
	}
}

type AdminLoginAttempt struct {
	Count uint
	Date  time.Time
}

type AdminLoginAttempts map[string]*AdminLoginAttempt

func NewAdmin(controller *Controller) *Admin {
	return &Admin{
		Attempts:         AdminLoginAttempts{},
		AttemptsMax:      uint(3),
		AttemptsMaxDelay: time.Duration(time.Duration.Minutes(10)),
		Broadcast:        make(chan *[]byte),
		Conns:            make(map[*websocket.Conn]bool),
		Controller:       controller,
		Register:         make(chan *websocket.Conn),
		Tokens:           []string{},
		Unregister:       make(chan *websocket.Conn),
		mutex:            sync.Mutex{},
	}
}

func (admin *Admin) AlertsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		t := admin.GetAuthorization(r)
		if !admin.ValidateToken(t) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		if b, err := json.Marshal(Alerts); err == nil {
			w.Write(b)
		} else {
			w.WriteHeader(http.StatusExpectationFailed)
		}

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) SystemHealthHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		t := admin.GetAuthorization(r)
		if !admin.ValidateToken(t) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// Parse query parameters
		limitStr := r.URL.Query().Get("limit")
		includeDismissed := r.URL.Query().Get("includeDismissed") == "true"

		limit := 100 // Default limit for admin dashboard
		if limitStr != "" {
			if parsedLimit, err := strconv.Atoi(limitStr); err == nil {
				limit = parsedLimit
			}
		}

		// Get system alerts
		alerts, err := admin.Controller.GetSystemAlerts(limit, includeDismissed)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("failed to get system alerts: %v", err),
			})
			return
		}

		// Return JSON response
		w.Header().Set("Content-Type", "application/json")
		if b, err := json.Marshal(map[string]interface{}{
			"alerts": alerts,
			"count":  len(alerts),
		}); err == nil {
			w.Write(b)
		} else {
			w.WriteHeader(http.StatusExpectationFailed)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "failed to marshal system alerts",
			})
		}

	case http.MethodPost, http.MethodPut:
		// Handle dismissing alerts via admin API
		t := admin.GetAuthorization(r)
		if !admin.ValidateToken(t) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		var request struct {
			AlertId uint64 `json:"alertId"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "invalid request body",
			})
			return
		}

		if err := admin.Controller.DismissSystemAlert(request.AlertId); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("failed to dismiss alert: %v", err),
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "alert dismissed successfully",
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) TranscriptionFailuresHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Get failed transcription calls with details
		twentyFourHoursAgo := time.Now().Add(-24 * time.Hour).UnixMilli()

		query := fmt.Sprintf(`SELECT c."callId", c."systemId", c."talkgroupId", c."timestamp", c."transcriptionFailureReason", s."label" as "systemLabel", t."label" as "talkgroupLabel", t."name" as "talkgroupName" FROM "calls" c LEFT JOIN "systems" s ON s."systemId" = c."systemId" LEFT JOIN "talkgroups" t ON t."talkgroupId" = c."talkgroupId" WHERE c."transcriptionStatus" = 'failed' AND c."timestamp" >= %d ORDER BY c."timestamp" DESC LIMIT 100`, twentyFourHoursAgo)

		rows, err := admin.Controller.Database.Sql.Query(query)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("failed to query failed calls: %v", err),
			})
			return
		}
		defer rows.Close()

		var failedCalls []map[string]interface{}
		for rows.Next() {
			var callId, systemId, talkgroupId uint64
			var timestamp int64
			var systemLabel, talkgroupLabel, talkgroupName, failureReason sql.NullString

			if err := rows.Scan(&callId, &systemId, &talkgroupId, &timestamp, &failureReason, &systemLabel, &talkgroupLabel, &talkgroupName); err != nil {
				continue
			}

			callData := map[string]interface{}{
				"callId":         callId,
				"systemId":       systemId,
				"talkgroupId":    talkgroupId,
				"timestamp":      timestamp,
				"systemLabel":    "",
				"talkgroupLabel": "",
				"talkgroupName":  "",
				"failureReason":  "",
			}

			if systemLabel.Valid {
				callData["systemLabel"] = systemLabel.String
			}
			if talkgroupLabel.Valid {
				callData["talkgroupLabel"] = talkgroupLabel.String
			}
			if talkgroupName.Valid {
				callData["talkgroupName"] = talkgroupName.String
			}
			if failureReason.Valid {
				callData["failureReason"] = failureReason.String
			}

			failedCalls = append(failedCalls, callData)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"calls": failedCalls,
			"count": len(failedCalls),
		})

	case http.MethodPost:
		// Reset transcription failures - clear failed status
		var request struct {
			CallIds []uint64 `json:"callIds"` // If empty, reset all
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "invalid request body",
			})
			return
		}

		var query string
		var rowsAffected int64
		var err error

		if len(request.CallIds) > 0 {
			// Reset specific calls
			callIdStrs := make([]string, len(request.CallIds))
			for i, id := range request.CallIds {
				callIdStrs[i] = fmt.Sprintf("%d", id)
			}
			query = fmt.Sprintf(`UPDATE "calls" SET "transcriptionStatus" = 'pending', "transcriptionFailureReason" = '' WHERE "callId" IN (%s)`, strings.Join(callIdStrs, ","))
		} else {
			// Reset all failed calls from last 24 hours
			twentyFourHoursAgo := time.Now().Add(-24 * time.Hour).UnixMilli()
			query = fmt.Sprintf(`UPDATE "calls" SET "transcriptionStatus" = 'pending', "transcriptionFailureReason" = '' WHERE "transcriptionStatus" = 'failed' AND "timestamp" >= %d`, twentyFourHoursAgo)
		}

		// Log the query for debugging
		admin.Controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("Resetting transcription failures with query: %s", query))

		var result sql.Result
		result, err = admin.Controller.Database.Sql.Exec(query)
		if err != nil {
			errorMsg := fmt.Sprintf("failed to reset transcription failures: %v (query: %s)", err, query)
			admin.Controller.Logs.LogEvent(LogLevelError, errorMsg)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": errorMsg,
			})
			return
		}

		rowsAffected, err = result.RowsAffected()
		if err != nil {
			admin.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to get rows affected: %v", err))
		}

		admin.Controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("Successfully reset transcription failures: %d rows affected", rowsAffected))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":      true,
			"rowsAffected": rowsAffected,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) AlertRetentionDaysHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Get current retention days
		retentionDays := admin.Controller.Options.AlertRetentionDays
		if retentionDays == 0 {
			retentionDays = 5 // Default
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"retentionDays": retentionDays,
		})

	case http.MethodPost:
		// Set retention days
		var request struct {
			RetentionDays uint `json:"retentionDays"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "invalid request body",
			})
			return
		}

		if request.RetentionDays == 0 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "retention days must be greater than 0",
			})
			return
		}

		admin.Controller.Options.AlertRetentionDays = request.RetentionDays

		if err := admin.Controller.Options.Write(admin.Controller.Database); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("failed to save retention days: %v", err),
			})
			return
		}

		// Reload options to ensure consistency
		if err := admin.Controller.Options.Read(admin.Controller.Database); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("failed to reload options: %v", err),
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":       true,
			"retentionDays": request.RetentionDays,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) ToneDetectionIssueThresholdHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Get current threshold
		threshold := admin.Controller.Options.ToneDetectionIssueThreshold
		if threshold == 0 {
			threshold = 5 // Default
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"threshold": threshold,
		})

	case http.MethodPost:
		// Set threshold
		var request struct {
			Threshold uint `json:"threshold"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "invalid request body",
			})
			return
		}

		if request.Threshold == 0 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "threshold must be greater than 0",
			})
			return
		}

		admin.Controller.Options.ToneDetectionIssueThreshold = request.Threshold

		if err := admin.Controller.Options.Write(admin.Controller.Database); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("failed to save threshold: %v", err),
			})
			return
		}

		// Reload options to ensure consistency
		if err := admin.Controller.Options.Read(admin.Controller.Database); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("failed to reload options: %v", err),
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"threshold": request.Threshold,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) TranscriptionFailureThresholdHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Get current threshold
		threshold := admin.Controller.Options.TranscriptionFailureThreshold
		if threshold == 0 {
			threshold = 10 // Default
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"threshold": threshold,
		})

	case http.MethodPost:
		// Set threshold
		var request struct {
			Threshold uint `json:"threshold"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "invalid request body",
			})
			return
		}

		if request.Threshold == 0 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "threshold must be greater than 0",
			})
			return
		}

		admin.Controller.Options.TranscriptionFailureThreshold = request.Threshold

		if err := admin.Controller.Options.Write(admin.Controller.Database); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("failed to save threshold: %v", err),
			})
			return
		}

		// Reload options to ensure consistency
		if err := admin.Controller.Options.Read(admin.Controller.Database); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("failed to reload options: %v", err),
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"threshold": request.Threshold,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) NoAudioThresholdMinutesHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Get current threshold
		threshold := admin.Controller.Options.NoAudioThresholdMinutes
		if threshold == 0 {
			threshold = 30 // Default
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"threshold": threshold,
		})

	case http.MethodPost:
		// Set threshold
		var request struct {
			Threshold uint `json:"threshold"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "invalid request body",
			})
			return
		}

		if request.Threshold == 0 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "threshold must be greater than 0",
			})
			return
		}

		admin.Controller.Options.NoAudioThresholdMinutes = request.Threshold

		if err := admin.Controller.Options.Write(admin.Controller.Database); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("failed to save threshold: %v", err),
			})
			return
		}

		// Reload options to ensure consistency
		if err := admin.Controller.Options.Read(admin.Controller.Database); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("failed to reload options: %v", err),
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"threshold": request.Threshold,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) NoAudioMultiplierHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Get current multiplier
		multiplier := admin.Controller.Options.NoAudioMultiplier
		if multiplier == 0 {
			multiplier = 1.5 // Default
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"multiplier": multiplier,
		})

	case http.MethodPost:
		// Set multiplier
		var request struct {
			Multiplier float64 `json:"multiplier"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "invalid request body",
			})
			return
		}

		if request.Multiplier <= 0 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "multiplier must be greater than 0",
			})
			return
		}

		admin.Controller.Options.NoAudioMultiplier = request.Multiplier

		if err := admin.Controller.Options.Write(admin.Controller.Database); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("failed to save multiplier: %v", err),
			})
			return
		}

		// Reload options to ensure consistency
		if err := admin.Controller.Options.Read(admin.Controller.Database); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("failed to reload options: %v", err),
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    true,
			"multiplier": request.Multiplier,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) SystemHealthAlertsEnabledHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Get current enabled status
		enabled := admin.Controller.Options.SystemHealthAlertsEnabled

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"enabled": enabled,
		})

	case http.MethodPost:
		// Set enabled status
		var request struct {
			Enabled bool `json:"enabled"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "invalid request body",
			})
			return
		}

		admin.Controller.Options.SystemHealthAlertsEnabled = request.Enabled

		if err := admin.Controller.Options.Write(admin.Controller.Database); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("failed to save setting: %v", err),
			})
			return
		}

		// If enabling, restart no-audio monitoring for all systems
		if request.Enabled {
			go admin.Controller.StartNoAudioMonitoringForAllSystems()
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"enabled": request.Enabled,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) SystemHealthAlertSettingsHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Get all system health alert settings
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"transcriptionFailureAlertsEnabled": admin.Controller.Options.TranscriptionFailureAlertsEnabled,
			"toneDetectionAlertsEnabled":        admin.Controller.Options.ToneDetectionAlertsEnabled,
			"noAudioAlertsEnabled":              admin.Controller.Options.NoAudioAlertsEnabled,
			"transcriptionFailureThreshold":     admin.Controller.Options.TranscriptionFailureThreshold,
			"transcriptionFailureTimeWindow":    admin.Controller.Options.TranscriptionFailureTimeWindow,
			"toneDetectionIssueThreshold":       admin.Controller.Options.ToneDetectionIssueThreshold,
			"toneDetectionTimeWindow":           admin.Controller.Options.ToneDetectionTimeWindow,
			"noAudioThresholdMinutes":           admin.Controller.Options.NoAudioThresholdMinutes,
			"noAudioMultiplier":                 admin.Controller.Options.NoAudioMultiplier,
			"noAudioTimeWindow":                 admin.Controller.Options.NoAudioTimeWindow,
			"noAudioHistoricalDataDays":         admin.Controller.Options.NoAudioHistoricalDataDays,
			"transcriptionFailureRepeatMinutes": admin.Controller.Options.TranscriptionFailureRepeatMinutes,
			"toneDetectionRepeatMinutes":        admin.Controller.Options.ToneDetectionRepeatMinutes,
			"noAudioRepeatMinutes":              admin.Controller.Options.NoAudioRepeatMinutes,
			"alertRetentionDays":                admin.Controller.Options.AlertRetentionDays,
		})

	case http.MethodPost:
		// Update settings
		var request struct {
			TranscriptionFailureAlertsEnabled *bool    `json:"transcriptionFailureAlertsEnabled"`
			ToneDetectionAlertsEnabled        *bool    `json:"toneDetectionAlertsEnabled"`
			NoAudioAlertsEnabled              *bool    `json:"noAudioAlertsEnabled"`
			TranscriptionFailureThreshold     *uint    `json:"transcriptionFailureThreshold"`
			TranscriptionFailureTimeWindow    *uint    `json:"transcriptionFailureTimeWindow"`
			ToneDetectionIssueThreshold       *uint    `json:"toneDetectionIssueThreshold"`
			ToneDetectionTimeWindow           *uint    `json:"toneDetectionTimeWindow"`
			NoAudioThresholdMinutes           *uint    `json:"noAudioThresholdMinutes"`
			NoAudioMultiplier                 *float64 `json:"noAudioMultiplier"`
			NoAudioTimeWindow                 *uint    `json:"noAudioTimeWindow"`
			NoAudioHistoricalDataDays         *uint    `json:"noAudioHistoricalDataDays"`
			TranscriptionFailureRepeatMinutes *uint    `json:"transcriptionFailureRepeatMinutes"`
			ToneDetectionRepeatMinutes        *uint    `json:"toneDetectionRepeatMinutes"`
			NoAudioRepeatMinutes              *uint    `json:"noAudioRepeatMinutes"`
			AlertRetentionDays                *uint    `json:"alertRetentionDays"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "invalid request body",
			})
			return
		}

		// Update only provided fields
		if request.TranscriptionFailureAlertsEnabled != nil {
			admin.Controller.Options.TranscriptionFailureAlertsEnabled = *request.TranscriptionFailureAlertsEnabled
		}
		if request.ToneDetectionAlertsEnabled != nil {
			admin.Controller.Options.ToneDetectionAlertsEnabled = *request.ToneDetectionAlertsEnabled
		}
		if request.NoAudioAlertsEnabled != nil {
			admin.Controller.Options.NoAudioAlertsEnabled = *request.NoAudioAlertsEnabled
		}
		if request.TranscriptionFailureThreshold != nil && *request.TranscriptionFailureThreshold > 0 {
			admin.Controller.Options.TranscriptionFailureThreshold = *request.TranscriptionFailureThreshold
		}
		if request.TranscriptionFailureTimeWindow != nil && *request.TranscriptionFailureTimeWindow > 0 {
			admin.Controller.Options.TranscriptionFailureTimeWindow = *request.TranscriptionFailureTimeWindow
		}
		if request.ToneDetectionIssueThreshold != nil && *request.ToneDetectionIssueThreshold > 0 {
			admin.Controller.Options.ToneDetectionIssueThreshold = *request.ToneDetectionIssueThreshold
		}
		if request.ToneDetectionTimeWindow != nil && *request.ToneDetectionTimeWindow > 0 {
			admin.Controller.Options.ToneDetectionTimeWindow = *request.ToneDetectionTimeWindow
		}
		if request.NoAudioThresholdMinutes != nil && *request.NoAudioThresholdMinutes > 0 {
			admin.Controller.Options.NoAudioThresholdMinutes = *request.NoAudioThresholdMinutes
		}
		if request.NoAudioMultiplier != nil && *request.NoAudioMultiplier > 0 {
			admin.Controller.Options.NoAudioMultiplier = *request.NoAudioMultiplier
		}
		if request.NoAudioTimeWindow != nil && *request.NoAudioTimeWindow > 0 {
			admin.Controller.Options.NoAudioTimeWindow = *request.NoAudioTimeWindow
		}
		if request.NoAudioHistoricalDataDays != nil && *request.NoAudioHistoricalDataDays > 0 {
			admin.Controller.Options.NoAudioHistoricalDataDays = *request.NoAudioHistoricalDataDays
		}
		if request.TranscriptionFailureRepeatMinutes != nil && *request.TranscriptionFailureRepeatMinutes > 0 {
			admin.Controller.Options.TranscriptionFailureRepeatMinutes = *request.TranscriptionFailureRepeatMinutes
		}
		if request.ToneDetectionRepeatMinutes != nil && *request.ToneDetectionRepeatMinutes > 0 {
			admin.Controller.Options.ToneDetectionRepeatMinutes = *request.ToneDetectionRepeatMinutes
		}
		if request.NoAudioRepeatMinutes != nil && *request.NoAudioRepeatMinutes > 0 {
			admin.Controller.Options.NoAudioRepeatMinutes = *request.NoAudioRepeatMinutes
		}
		if request.AlertRetentionDays != nil && *request.AlertRetentionDays > 0 {
			admin.Controller.Options.AlertRetentionDays = *request.AlertRetentionDays
		}

		if err := admin.Controller.Options.Write(admin.Controller.Database); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("failed to save settings: %v", err),
			})
			return
		}

		// If no-audio alerts setting was changed, restart monitoring for all systems
		if request.NoAudioAlertsEnabled != nil {
			go admin.Controller.StartNoAudioMonitoringForAllSystems()
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// SystemNoAudioSettingsHandler handles updating per-system no-audio alert settings
func (admin *Admin) SystemNoAudioSettingsHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		SystemId                uint `json:"systemId"`
		NoAudioAlertsEnabled    bool `json:"noAudioAlertsEnabled"`
		NoAudioThresholdMinutes uint `json:"noAudioThresholdMinutes"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid request body",
		})
		return
	}

	// Find the system in the database
	system, ok := admin.Controller.Systems.GetSystemById(uint64(request.SystemId))
	if !ok || system == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "system not found",
		})
		return
	}

	// Update the system settings
	system.NoAudioAlertsEnabled = request.NoAudioAlertsEnabled
	system.NoAudioThresholdMinutes = request.NoAudioThresholdMinutes

	// Save to database
	if err := admin.Controller.Systems.Write(admin.Controller.Database); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("failed to save system settings: %v", err),
		})
		return
	}

	// Restart no-audio monitoring for this system with new settings
	go admin.Controller.RestartNoAudioMonitoringForSystem(uint64(request.SystemId))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "system no-audio settings updated successfully",
	})
}

// CallAudioHandler serves call audio for admin playback
func (admin *Admin) CallAudioHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Extract call ID from URL path (e.g., /api/admin/call-audio/12345)
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 4 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid call ID"})
		return
	}

	callIdStr := pathParts[3]
	callId, err := strconv.ParseUint(callIdStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid call ID format"})
		return
	}

	// Get call from database
	call, err := admin.Controller.Calls.GetCall(callId)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("call not found: %v", err)})
		return
	}

	// Check if call has audio
	if len(call.Audio) == 0 {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "call has no audio"})
		return
	}

	// Set appropriate headers for audio playback
	mimeType := call.AudioMime
	if mimeType == "" {
		mimeType = "audio/wav" // Default to WAV if not specified
	}

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", strconv.Itoa(len(call.Audio)))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"call-%d.%s\"", callId, getAudioExtension(mimeType)))

	// Write audio data
	w.Write(call.Audio)
}

// getAudioExtension returns file extension based on MIME type
func getAudioExtension(mimeType string) string {
	switch mimeType {
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	case "audio/wav", "audio/x-wav":
		return "wav"
	case "audio/ogg":
		return "ogg"
	case "audio/aac":
		return "aac"
	case "audio/m4a", "audio/mp4":
		return "m4a"
	default:
		return "wav"
	}
}

func (admin *Admin) ToneImportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	token := admin.GetAuthorization(r)
	if !admin.ValidateToken(token) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var req ToneImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	result, err := ParseToneImport(req.Format, req.Content)
	if err != nil {
		admin.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("tone import failed: %s", err.Error()))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(fmt.Sprintf(`{"error":"%s"}`, escapeQuotes(err.Error()))))
		return
	}

	response := ToneImportResponse{
		Format:   strings.ToLower(strings.TrimSpace(req.Format)),
		Count:    len(result.toneSets),
		ToneSets: result.toneSets,
		Warnings: result.warnings,
	}

	if b, err := json.Marshal(response); err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (admin *Admin) BroadcastConfig() {
	if b, err := json.Marshal(admin.GetConfig()); err == nil {
		for conn := range admin.Conns {
			conn.WriteMessage(websocket.TextMessage, b)
		}
	}
}

func (admin *Admin) ChangePassword(currentPassword any, newPassword string) error {
	var (
		err  error
		hash []byte
	)

	if len(newPassword) == 0 {
		return errors.New("newPassword is empty")
	}

	switch v := currentPassword.(type) {
	case string:
		if err = bcrypt.CompareHashAndPassword([]byte(admin.Controller.Options.adminPassword), []byte(v)); err != nil {
			return err
		}
	}

	if hash, err = bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost); err != nil {
		return err
	}

	admin.Controller.Options.adminPassword = string(hash)
	admin.Controller.Options.adminPasswordNeedChange = newPassword == defaults.adminPassword

	if err := admin.Controller.Options.Write(admin.Controller.Database); err != nil {
		return err
	}

	if err := admin.Controller.Options.Read(admin.Controller.Database); err != nil {
		return err
	}

	admin.Controller.Logs.LogEvent(LogLevelWarn, "admin password changed.")

	return nil
}

func (admin *Admin) ConfigHandler(w http.ResponseWriter, r *http.Request) {
	if strings.EqualFold(r.Header.Get("upgrade"), "websocket") {
		upgrader := websocket.Upgrader{}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		admin.Register <- conn

		go func() {
			conn.SetReadDeadline(time.Time{})

			for {
				_, b, err := conn.ReadMessage()
				if err != nil {
					break
				}

				if !admin.ValidateToken(string(b)) {
					break
				}
			}

			admin.Unregister <- conn

			conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1000, ""))
		}()

	} else {
		logError := func(err error) {
			admin.Controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("admin.confighandler.put: %s", err.Error()))
		}

		t := admin.GetAuthorization(r)
		if !admin.ValidateToken(t) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch r.Method {
		case http.MethodGet:
			admin.SendConfig(w)

		case http.MethodPut:
			// IMPORTANT: This import performs a COMPLETE OVERWRITE of all configuration data.
			// For each entity type present in the import file, ALL existing data of that type
			// in the database is replaced with the imported data. Entities not in the import
			// file are left unchanged (except for users and userGroups which are fully replaced).

			// Check if this is a full import (destructive) or just a save (non-destructive)
			isFullImport := r.Header.Get("X-Full-Import") == "true"

			m := map[string]any{}
			err := json.NewDecoder(r.Body).Decode(&m)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			admin.mutex.Lock()
			defer admin.mutex.Unlock()

			admin.Controller.Dirwatches.Stop()

			switch v := m["apikeys"].(type) {
			case []any:
				admin.Controller.Apikeys.FromMap(v)
				err = admin.Controller.Apikeys.Write(admin.Controller.Database)
				if err != nil {
					logError(err)
				} else {
					err = admin.Controller.Apikeys.Read(admin.Controller.Database)
					if err != nil {
						logError(err)
					}
				}
			}

			switch v := m["dirwatch"].(type) {
			case []any:
				admin.Controller.Dirwatches.FromMap(v)
				err = admin.Controller.Dirwatches.Write(admin.Controller.Database)
				if err != nil {
					logError(err)
				} else {
					err = admin.Controller.Dirwatches.Read(admin.Controller.Database)
					if err != nil {
						logError(err)
					}
				}
			}

			switch v := m["downstreams"].(type) {
			case []any:
				admin.Controller.Downstreams.FromMap(v)
				err = admin.Controller.Downstreams.Write(admin.Controller.Database)
				if err != nil {
					logError(err)
				} else {
					err = admin.Controller.Downstreams.Read(admin.Controller.Database)
					if err != nil {
						logError(err)
					}
				}
			}

			switch v := m["groups"].(type) {
			case []any:
				admin.Controller.Groups.FromMap(v)
				err = admin.Controller.Groups.Write(admin.Controller.Database)
				if err != nil {
					logError(err)
				} else {
					err = admin.Controller.Groups.Read(admin.Controller.Database)
					if err != nil {
						logError(err)
					}
				}
			}

			switch v := m["options"].(type) {
			case map[string]any:
				admin.Controller.Options.FromMap(v)
				err = admin.Controller.Options.Write(admin.Controller.Database)
				if err != nil {
					logError(err)
				} else {
					// Reload options from database to update in-memory state
					err = admin.Controller.Options.Read(admin.Controller.Database)
					if err != nil {
						logError(err)
					} else {
						// Restart transcription queue with updated settings
						admin.Controller.RestartTranscriptionQueue()
					}
				}
			}

			// Handle Radio Reference configuration
			switch v := m["radioReference"].(type) {
			case map[string]any:
				// Update the options with Radio Reference settings
				if enabled, ok := v["enabled"].(bool); ok {
					admin.Controller.Options.RadioReferenceEnabled = enabled
				}
				if username, ok := v["username"].(string); ok {
					admin.Controller.Options.RadioReferenceUsername = username
				}
				if password, ok := v["password"].(string); ok {
					admin.Controller.Options.RadioReferencePassword = password
				}

				// Save the updated options to database
				err = admin.Controller.Options.Write(admin.Controller.Database)
				if err != nil {
					logError(err)
				} else {
					// Reload options from database to update in-memory state
					err = admin.Controller.Options.Read(admin.Controller.Database)
					if err != nil {
						logError(err)
					}
				}
			}

			// Write tags BEFORE systems to ensure foreign key constraints are satisfied
			// Talkgroups reference tags via foreign key, so tags must exist before talkgroups are inserted
			switch v := m["tags"].(type) {
			case []any:
				admin.Controller.Tags.FromMap(v)
				err = admin.Controller.Tags.Write(admin.Controller.Database)
				if err != nil {
					logError(err)
				} else {
					err = admin.Controller.Tags.Read(admin.Controller.Database)
					if err != nil {
						logError(err)
					}
				}
			}

			switch v := m["systems"].(type) {
			case []any:
				admin.Controller.Systems.FromMap(v)
				err = admin.Controller.Systems.Write(admin.Controller.Database)
				if err != nil {
					logError(err)
				} else {
					err = admin.Controller.Systems.Read(admin.Controller.Database)
					if err != nil {
						logError(err)
					}
				}
			}

			// Helper functions for imports
			getStringFromMap := func(m map[string]any, key string) string {
				if v, ok := m[key].(string); ok {
					return v
				}
				return ""
			}
			getBoolFromMap := func(m map[string]any, key string, def bool) bool {
				if v, ok := m[key].(bool); ok {
					return v
				}
				return def
			}
			getUint64FromMap := func(m map[string]any, key string) uint64 {
				if v, ok := m[key].(float64); ok {
					return uint64(v)
				}
				return 0
			}
			getFloat64FromMap := func(m map[string]any, key string) float64 {
				if v, ok := m[key].(float64); ok {
					return v
				}
				return 0
			}

			// Handle user groups import
			// Map from imported group ID to actual group ID (for user assignment)
			groupIdMap := make(map[uint64]uint64)
			switch v := m["userGroups"].(type) {
			case []any:
				// Track imported group IDs to determine which groups to delete
				// Track actual IDs of successfully imported groups (updated or created)
				importedGroupIds := make(map[uint64]bool)

				for _, groupData := range v {
					groupMap, ok := groupData.(map[string]any)
					if !ok {
						continue
					}

					// Extract group data
					id, _ := groupMap["id"].(float64)
					name, _ := groupMap["name"].(string)
					if name == "" {
						continue
					}

					importedGroupId := uint64(id)

					// Check if group exists by ID first, then by name
					existingGroup := admin.Controller.UserGroups.Get(importedGroupId)
					if existingGroup == nil {
						// If not found by ID, try to find by name
						existingGroup = admin.Controller.UserGroups.GetByName(name)
					}
					if existingGroup != nil {
						// Fully overwrite existing group with imported data
						existingGroup.Name = name
						existingGroup.Description = getStringFromMap(groupMap, "description")
						existingGroup.SystemAccess = getStringFromMap(groupMap, "systemAccess")
						existingGroup.Delay = int(getFloat64FromMap(groupMap, "delay"))
						existingGroup.SystemDelays = getStringFromMap(groupMap, "systemDelays")
						existingGroup.TalkgroupDelays = getStringFromMap(groupMap, "talkgroupDelays")
						existingGroup.ConnectionLimit = uint(getFloat64FromMap(groupMap, "connectionLimit"))
						existingGroup.MaxUsers = uint(getFloat64FromMap(groupMap, "maxUsers"))
						existingGroup.BillingEnabled = getBoolFromMap(groupMap, "billingEnabled", false)
						existingGroup.StripePriceId = getStringFromMap(groupMap, "stripePriceId")
						existingGroup.PricingOptions = getStringFromMap(groupMap, "pricingOptions")
						existingGroup.BillingMode = getStringFromMap(groupMap, "billingMode")
						existingGroup.CollectSalesTax = getBoolFromMap(groupMap, "collectSalesTax", false)
						existingGroup.IsPublicRegistration = getBoolFromMap(groupMap, "isPublicRegistration", false)
						existingGroup.AllowAddExistingUsers = getBoolFromMap(groupMap, "allowAddExistingUsers", false)
						if createdAt, ok := groupMap["createdAt"].(float64); ok {
							existingGroup.CreatedAt = int64(createdAt)
						}

						if err := admin.Controller.UserGroups.Update(existingGroup, admin.Controller.Database); err != nil {
							logError(fmt.Errorf("failed to update imported user group %s: %v", name, err))
						} else {
							// Track the actual ID of the successfully updated group
							importedGroupIds[existingGroup.Id] = true
							// Map imported ID to actual ID (may be the same)
							groupIdMap[importedGroupId] = existingGroup.Id
						}
					} else {
						// Create new group
						group := &UserGroup{
							Name:                  name,
							Description:           getStringFromMap(groupMap, "description"),
							SystemAccess:          getStringFromMap(groupMap, "systemAccess"),
							Delay:                 int(getFloat64FromMap(groupMap, "delay")),
							SystemDelays:          getStringFromMap(groupMap, "systemDelays"),
							TalkgroupDelays:       getStringFromMap(groupMap, "talkgroupDelays"),
							ConnectionLimit:       uint(getFloat64FromMap(groupMap, "connectionLimit")),
							MaxUsers:              uint(getFloat64FromMap(groupMap, "maxUsers")),
							BillingEnabled:        getBoolFromMap(groupMap, "billingEnabled", false),
							StripePriceId:         getStringFromMap(groupMap, "stripePriceId"),
							PricingOptions:        getStringFromMap(groupMap, "pricingOptions"),
							BillingMode:           getStringFromMap(groupMap, "billingMode"),
							CollectSalesTax:       getBoolFromMap(groupMap, "collectSalesTax", false),
							IsPublicRegistration:  getBoolFromMap(groupMap, "isPublicRegistration", false),
							AllowAddExistingUsers: getBoolFromMap(groupMap, "allowAddExistingUsers", false),
						}
						if createdAt, ok := groupMap["createdAt"].(float64); ok {
							group.CreatedAt = int64(createdAt)
						} else {
							group.CreatedAt = time.Now().Unix()
						}

						if err := admin.Controller.UserGroups.Add(group, admin.Controller.Database); err != nil {
							logError(fmt.Errorf("failed to import user group %s: %v", name, err))
						} else {
							// Track the actual ID of the successfully created group (may differ from imported ID)
							importedGroupIds[group.Id] = true
							// Map imported ID to actual ID (will be different for new groups)
							groupIdMap[importedGroupId] = group.Id
						}
					}
				}

				// Only delete groups not in import if this is a full import
				// For regular saves, we preserve groups that aren't in the form data
				if isFullImport {
					allGroups := admin.Controller.UserGroups.GetAll()
					for _, existingGroup := range allGroups {
						if !importedGroupIds[existingGroup.Id] {
							if err := admin.Controller.UserGroups.Delete(existingGroup.Id, admin.Controller.Database); err != nil {
								logError(fmt.Errorf("failed to remove user group %d during import: %v", existingGroup.Id, err))
							}
						}
					}
				}

				// Reload user groups after import
				if err := admin.Controller.UserGroups.Load(admin.Controller.Database); err != nil {
					logError(err)
				}

				// Rebuild groupIdMap after reload by matching imported names to actual groups
				// This ensures the mapping is correct even if IDs don't match
				if v, ok := m["userGroups"].([]any); ok {
					for _, groupData := range v {
						groupMap, ok := groupData.(map[string]any)
						if !ok {
							continue
						}

						importedId, _ := groupMap["id"].(float64)
						importedName, _ := groupMap["name"].(string)
						if importedName == "" {
							continue
						}

						importedGroupId := uint64(importedId)
						// Find the actual group by name (since IDs might not match)
						if actualGroup := admin.Controller.UserGroups.GetByName(importedName); actualGroup != nil {
							// Update the mapping with the actual ID
							groupIdMap[importedGroupId] = actualGroup.Id
						}
					}
				}
			}

			// Handle users import
			// Map imported userId -> actual userId (based on email matching)
			userIdMap := make(map[uint64]uint64)
			switch v := m["users"].(type) {
			case []any:
				// Only delete ALL existing users for full imports, not regular saves
				if isFullImport {
					allUsers := admin.Controller.Users.GetAllUsers()
					for _, existingUser := range allUsers {
						// Delete from database first
						_, err := admin.Controller.Database.Sql.Exec(`DELETE FROM "users" WHERE "userId" = $1`, existingUser.Id)
						if err != nil {
							logError(fmt.Errorf("failed to delete user %s from database during import: %v", existingUser.Email, err))
						} else {
							// Remove from in-memory map
							if err := admin.Controller.Users.Remove(existingUser.Id); err != nil {
								logError(fmt.Errorf("failed to remove user %s from memory during import: %v", existingUser.Email, err))
							}
						}
					}
				}

				// Now create/update users from import
				for _, userData := range v {
					userMap, ok := userData.(map[string]any)
					if !ok {
						continue
					}

					email, _ := userMap["email"].(string)
					if email == "" {
						continue
					}

					// Create new user with imported password hash
					password, _ := userMap["password"].(string)
					if password == "" {
						logError(fmt.Errorf("cannot import user %s without password hash", email))
						continue
					}

					// Map imported userGroupId to actual group ID
					importedUserGroupId := getUint64FromMap(userMap, "userGroupId")
					actualUserGroupId := uint64(0)
					if importedUserGroupId > 0 {
						if actualId, ok := groupIdMap[importedUserGroupId]; ok {
							actualUserGroupId = actualId
						} else {
							// Group ID not found in mapping - try to find by ID in database
							if existingGroup := admin.Controller.UserGroups.Get(importedUserGroupId); existingGroup != nil {
								actualUserGroupId = importedUserGroupId
							} else {
								// Group doesn't exist - set to 0
								actualUserGroupId = 0
								logError(fmt.Errorf("user %s references non-existent group ID %d, setting to 0", email, importedUserGroupId))
							}
						}
					}

					// Track imported userId for ID mapping
					importedUserId := getUint64FromMap(userMap, "id")
					// Check if user already exists
					existingUser := admin.Controller.Users.GetUserByEmail(email)

					if existingUser != nil {
						if importedUserId != 0 {
							userIdMap[importedUserId] = existingUser.Id
						}
						// Update existing user with imported data
						existingUser.Password = password // Use imported password hash directly
						existingUser.FirstName = getStringFromMap(userMap, "firstName")
						existingUser.LastName = getStringFromMap(userMap, "lastName")
						existingUser.ZipCode = getStringFromMap(userMap, "zipCode")
						existingUser.Verified = getBoolFromMap(userMap, "verified", false)
						existingUser.UserGroupId = actualUserGroupId
						existingUser.IsGroupAdmin = getBoolFromMap(userMap, "isGroupAdmin", false)
						existingUser.SystemAdmin = getBoolFromMap(userMap, "systemAdmin", false)
						existingUser.PinExpiresAt = getUint64FromMap(userMap, "pinExpiresAt")
						existingUser.ConnectionLimit = uint(getFloat64FromMap(userMap, "connectionLimit"))
						existingUser.Systems = getStringFromMap(userMap, "systems")
						existingUser.Delay = int(getFloat64FromMap(userMap, "delay"))
						existingUser.SystemDelays = getStringFromMap(userMap, "systemDelays")
						existingUser.TalkgroupDelays = getStringFromMap(userMap, "talkgroupDelays")
						existingUser.Settings = getStringFromMap(userMap, "settings")
						existingUser.StripeCustomerId = getStringFromMap(userMap, "stripeCustomerId")
						existingUser.StripeSubscriptionId = getStringFromMap(userMap, "stripeSubscriptionId")
						existingUser.SubscriptionStatus = getStringFromMap(userMap, "subscriptionStatus")
						existingUser.AccountExpiresAt = getUint64FromMap(userMap, "accountExpiresAt")

						// Update PIN if provided in import (don't regenerate if already exists)
						if importedPin := getStringFromMap(userMap, "pin"); importedPin != "" {
							existingUser.Pin = importedPin
						}

						// Update timestamps if provided
						if createdAt := getStringFromMap(userMap, "createdAt"); createdAt != "" {
							existingUser.CreatedAt = createdAt
						}
						if lastLogin := getStringFromMap(userMap, "lastLogin"); lastLogin != "" {
							existingUser.LastLogin = lastLogin
						}

						// Update user in database
						if err := admin.Controller.Users.Update(existingUser); err != nil {
							logError(fmt.Errorf("failed to update existing user %s: %v", email, err))
							continue
						}
						if err := admin.Controller.Users.Write(admin.Controller.Database); err != nil {
							logError(fmt.Errorf("failed to write updated user %s to database: %v", email, err))
						}
					} else {
						// Create new user
						user := &User{
							Email:                email,
							Password:             password, // Use imported password hash directly
							FirstName:            getStringFromMap(userMap, "firstName"),
							LastName:             getStringFromMap(userMap, "lastName"),
							ZipCode:              getStringFromMap(userMap, "zipCode"),
							Verified:             getBoolFromMap(userMap, "verified", false),
							UserGroupId:          actualUserGroupId,
							IsGroupAdmin:         getBoolFromMap(userMap, "isGroupAdmin", false),
							SystemAdmin:          getBoolFromMap(userMap, "systemAdmin", false),
							Pin:                  getStringFromMap(userMap, "pin"),
							PinExpiresAt:         getUint64FromMap(userMap, "pinExpiresAt"),
							ConnectionLimit:      uint(getFloat64FromMap(userMap, "connectionLimit")),
							Systems:              getStringFromMap(userMap, "systems"),
							Delay:                int(getFloat64FromMap(userMap, "delay")),
							SystemDelays:         getStringFromMap(userMap, "systemDelays"),
							TalkgroupDelays:      getStringFromMap(userMap, "talkgroupDelays"),
							Settings:             getStringFromMap(userMap, "settings"),
							StripeCustomerId:     getStringFromMap(userMap, "stripeCustomerId"),
							StripeSubscriptionId: getStringFromMap(userMap, "stripeSubscriptionId"),
							SubscriptionStatus:   getStringFromMap(userMap, "subscriptionStatus"),
							AccountExpiresAt:     getUint64FromMap(userMap, "accountExpiresAt"),
							CreatedAt:            getStringFromMap(userMap, "createdAt"),
							LastLogin:            getStringFromMap(userMap, "lastLogin"),
						}

						// Generate PIN if not provided
						if user.Pin == "" {
							pin, err := admin.Controller.Users.GenerateUniquePin(0)
							if err != nil {
								logError(fmt.Errorf("failed to generate PIN for imported user %s: %v", email, err))
								continue
							}
							user.Pin = pin
						}

						// Set createdAt if not provided
						if user.CreatedAt == "" {
							user.CreatedAt = fmt.Sprintf("%d", time.Now().Unix())
						}

						if err := admin.Controller.Users.SaveNewUser(user, admin.Controller.Database); err != nil {
							logError(fmt.Errorf("failed to import new user %s: %v", email, err))
						} else if importedUserId != 0 {
							if createdUser := admin.Controller.Users.GetUserByEmail(email); createdUser != nil {
								userIdMap[importedUserId] = createdUser.Id
							}
						}
					}
				}

				// Reload users after import
				if err := admin.Controller.Users.Read(admin.Controller.Database); err != nil {
					logError(err)
				}
			}

			// Handle keyword lists import
			// NOTE: Keyword lists are user-defined and NOT part of Radio Reference data
			// DO NOT delete/reimport keyword lists during Radio Reference imports - this preserves user's keyword lists and their IDs
			switch v := m["keywordLists"].(type) {
			case []any:
				// Only import keyword lists during explicit full configuration backup/restore (not Radio Reference imports)
				// Skip keyword list import entirely if this appears to be a Radio Reference import
				if isFullImport && len(v) > 0 {
					// Detect if this is a backup restore vs Radio Reference import
					// Backup restores will have keywordListId field to preserve IDs
					isBackupRestore := false
					if firstList, ok := v[0].(map[string]any); ok {
						if _, hasId := firstList["keywordListId"]; hasId {
							isBackupRestore = true
						}
					}

					// Only process keyword lists for full backup restores, NOT Radio Reference imports
					if isBackupRestore {
						// Delete existing keyword lists for backup restore
						_, err := admin.Controller.Database.Sql.Exec(`DELETE FROM "keywordLists"`)
						if err != nil {
							logError(fmt.Errorf("failed to delete existing keyword lists during backup restore: %v", err))
						}

						// Import keyword lists with preserved IDs
						for _, listData := range v {
							listMap, ok := listData.(map[string]any)
							if !ok {
								continue
							}

							label, _ := listMap["label"].(string)
							if label == "" {
								continue
							}

							keywordListId := uint64(getFloat64FromMap(listMap, "keywordListId"))
							description := getStringFromMap(listMap, "description")
							order := uint(getFloat64FromMap(listMap, "order"))
							createdAt := int64(getFloat64FromMap(listMap, "createdAt"))
							if createdAt == 0 {
								createdAt = time.Now().UnixMilli()
							}

							// Get keywords array
							var keywords []string
							if keywordsData, ok := listMap["keywords"].([]any); ok {
								for _, kw := range keywordsData {
									if k, ok := kw.(string); ok {
										keywords = append(keywords, k)
									}
								}
							}

							keywordsJson, _ := json.Marshal(keywords)

							// Insert keyword list with preserved ID
							if admin.Controller.Database.Config.DbType == DbTypePostgresql {
								query := `INSERT INTO "keywordLists" ("keywordListId", "label", "description", "keywords", "order", "createdAt") VALUES ($1, $2, $3, $4, $5, $6)`
								if _, err := admin.Controller.Database.Sql.Exec(query, keywordListId, label, description, string(keywordsJson), order, createdAt); err != nil {
									logError(fmt.Errorf("failed to import keyword list %s with ID %d: %v", label, keywordListId, err))
								}
							} else {
								query := `INSERT INTO "keywordLists" ("keywordListId", "label", "description", "keywords", "order", "createdAt") VALUES (?, ?, ?, ?, ?, ?)`
								if _, err := admin.Controller.Database.Sql.Exec(query, keywordListId, label, description, string(keywordsJson), order, createdAt); err != nil {
									logError(fmt.Errorf("failed to import keyword list %s with ID %d: %v", label, keywordListId, err))
								}
							}
						}
					}
					// Radio Reference imports (without keywordListId) will skip keyword list processing entirely
				}
			}

			// Handle user alert preferences import (map imported userId -> actual userId)
			// ONLY delete and re-import if this is a full import, not a regular config save
			if isFullImport {
				switch v := m["userAlertPreferences"].(type) {
				case []any:
					// Delete ALL existing user alert preferences first (only during full import)
					_, err := admin.Controller.Database.Sql.Exec(`DELETE FROM "userAlertPreferences"`)
					if err != nil {
						logError(fmt.Errorf("failed to delete existing user alert preferences during import: %v", err))
					}

					// Import all user alert preferences
					for _, prefData := range v {
						prefMap, ok := prefData.(map[string]any)
						if !ok {
							continue
						}

						importedUserId := uint64(getFloat64FromMap(prefMap, "userId"))
						systemId := uint64(getFloat64FromMap(prefMap, "systemId"))
						talkgroupId := uint64(getFloat64FromMap(prefMap, "talkgroupId"))

						// Skip if essential fields are missing
						if importedUserId == 0 || systemId == 0 || talkgroupId == 0 {
							continue
						}

						actualUserId := importedUserId
						if mappedId, ok := userIdMap[importedUserId]; ok {
							actualUserId = mappedId
						}

						if admin.Controller.Users.GetUserById(actualUserId) == nil {
							continue
						}

						alertEnabled := getBoolFromMap(prefMap, "alertEnabled", false)
						toneAlerts := getBoolFromMap(prefMap, "toneAlerts", true)
						keywordAlerts := getBoolFromMap(prefMap, "keywordAlerts", true)

						// Get keywords array
						var keywords []string
						if keywordsData, ok := prefMap["keywords"].([]any); ok {
							for _, kw := range keywordsData {
								if k, ok := kw.(string); ok {
									keywords = append(keywords, k)
								}
							}
						}

						// Get keywordListIds array
						var keywordListIds []int
						if keywordListIdsData, ok := prefMap["keywordListIds"].([]any); ok {
							for _, kid := range keywordListIdsData {
								if k, ok := kid.(float64); ok {
									keywordListIds = append(keywordListIds, int(k))
								}
							}
						}

						// Get toneSetIds array
						var toneSetIds []string
						if toneSetIdsData, ok := prefMap["toneSetIds"].([]any); ok {
							for _, tid := range toneSetIdsData {
								if t, ok := tid.(string); ok {
									toneSetIds = append(toneSetIds, t)
								}
							}
						}

						keywordsJson, _ := json.Marshal(keywords)
						keywordListIdsJson, _ := json.Marshal(keywordListIds)
						toneSetIdsJson, _ := json.Marshal(toneSetIds)

						// Insert user alert preference using parameterized queries
						if admin.Controller.Database.Config.DbType == DbTypePostgresql {
							query := `INSERT INTO "userAlertPreferences" ("userId", "systemId", "talkgroupId", "alertEnabled", "toneAlerts", "keywordAlerts", "keywords", "keywordListIds", "toneSetIds") VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
							if _, err := admin.Controller.Database.Sql.Exec(query, actualUserId, systemId, talkgroupId, alertEnabled, toneAlerts, keywordAlerts, string(keywordsJson), string(keywordListIdsJson), string(toneSetIdsJson)); err != nil {
								logError(fmt.Errorf("failed to import user alert preference for userId=%d (mapped from %d), systemId=%d, talkgroupId=%d: %v", actualUserId, importedUserId, systemId, talkgroupId, err))
							}
						} else {
							query := `INSERT INTO "userAlertPreferences" ("userId", "systemId", "talkgroupId", "alertEnabled", "toneAlerts", "keywordAlerts", "keywords", "keywordListIds", "toneSetIds") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
							if _, err := admin.Controller.Database.Sql.Exec(query, actualUserId, systemId, talkgroupId, alertEnabled, toneAlerts, keywordAlerts, string(keywordsJson), string(keywordListIdsJson), string(toneSetIdsJson)); err != nil {
								logError(fmt.Errorf("failed to import user alert preference for userId=%d (mapped from %d), systemId=%d, talkgroupId=%d: %v", actualUserId, importedUserId, systemId, talkgroupId, err))
							}
						}
					}
				}

				// Handle device tokens import (map imported userId -> actual userId)
				switch v := m["deviceTokens"].(type) {
				case []any:
					// Delete ALL existing device tokens first (only during full import)
					_, err := admin.Controller.Database.Sql.Exec(`DELETE FROM "deviceTokens"`)
					if err != nil {
						logError(fmt.Errorf("failed to delete existing device tokens during import: %v", err))
					}

					for _, tokenData := range v {
						tokenMap, ok := tokenData.(map[string]any)
						if !ok {
							continue
						}

						importedUserId := uint64(getFloat64FromMap(tokenMap, "userId"))
						token := getStringFromMap(tokenMap, "token")
						if importedUserId == 0 || token == "" {
							continue
						}

						actualUserId := importedUserId
						if mappedId, ok := userIdMap[importedUserId]; ok {
							actualUserId = mappedId
						}
						if admin.Controller.Users.GetUserById(actualUserId) == nil {
							continue
						}

						platform := getStringFromMap(tokenMap, "platform")
						sound := getStringFromMap(tokenMap, "sound")
						createdAt := int64(getFloat64FromMap(tokenMap, "createdAt"))
						lastUsed := int64(getFloat64FromMap(tokenMap, "lastUsed"))
						if createdAt == 0 {
							createdAt = time.Now().Unix()
						}
						if lastUsed == 0 {
							lastUsed = createdAt
						}

						if admin.Controller.Database.Config.DbType == DbTypePostgresql {
							query := `INSERT INTO "deviceTokens" ("userId", "token", "platform", "sound", "createdAt", "lastUsed") VALUES ($1, $2, $3, $4, $5, $6)`
							if _, err := admin.Controller.Database.Sql.Exec(query, actualUserId, token, platform, sound, createdAt, lastUsed); err != nil {
								logError(fmt.Errorf("failed to import device token for userId=%d (mapped from %d): %v", actualUserId, importedUserId, err))
							}
						} else {
							query := `INSERT INTO "deviceTokens" ("userId", "token", "platform", "sound", "createdAt", "lastUsed") VALUES (?, ?, ?, ?, ?, ?)`
							if _, err := admin.Controller.Database.Sql.Exec(query, actualUserId, token, platform, sound, createdAt, lastUsed); err != nil {
								logError(fmt.Errorf("failed to import device token for userId=%d (mapped from %d): %v", actualUserId, importedUserId, err))
							}
						}
					}

					// Reload device tokens into memory
					admin.Controller.DeviceTokens.mutex.Lock()
					admin.Controller.DeviceTokens.tokens = make(map[uint64]*DeviceToken)
					query := `SELECT "deviceTokenId", "userId", "token", "platform", "sound", "createdAt", "lastUsed" FROM "deviceTokens"`
					rows, err := admin.Controller.Database.Sql.Query(query)
					if err == nil {
						defer rows.Close()
						for rows.Next() {
							var dt DeviceToken
							if err := rows.Scan(&dt.Id, &dt.UserId, &dt.Token, &dt.Platform, &dt.Sound, &dt.CreatedAt, &dt.LastUsed); err == nil {
								admin.Controller.DeviceTokens.tokens[dt.Id] = &dt
							}
						}
					}
					admin.Controller.DeviceTokens.mutex.Unlock()
				}
			} // End isFullImport check for userAlertPreferences and deviceTokens

			// Emit config asynchronously to avoid blocking
			go admin.Controller.EmitConfig()
			admin.Controller.Dirwatches.Start(admin.Controller)

			// Sync config to file if enabled
			admin.Controller.SyncConfigToFile()

			admin.SendConfig(w)

			admin.Controller.Logs.LogEvent(LogLevelWarn, "configuration changed")

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (admin *Admin) StripeSyncHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Check if Stripe is enabled
	if !admin.Controller.Options.StripePaywallEnabled || admin.Controller.Options.StripeSecretKey == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Stripe is not enabled or secret key is missing",
		})
		return
	}

	// Import Stripe package
	stripe.Key = admin.Controller.Options.StripeSecretKey

	// Fetch all customers from Stripe
	params := &stripe.CustomerListParams{}
	params.Limit = stripe.Int64(100) // Fetch in batches of 100

	customersByEmail := make(map[string]*stripe.Customer)
	iter := customer.List(params)

	for iter.Next() {
		c := iter.Customer()
		if c.Email != "" {
			customersByEmail[strings.ToLower(c.Email)] = c
		}
	}

	if err := iter.Err(); err != nil {
		admin.Controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("Failed to fetch Stripe customers: %v", err))
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("Failed to fetch Stripe customers: %v", err),
		})
		return
	}

	// Get all users
	users := admin.Controller.Users.GetAllUsers()
	matched := 0
	updatedUsers := []map[string]interface{}{}
	unmatchedCustomers := []map[string]interface{}{}

	// Match users to Stripe customers and update
	for _, user := range users {
		if stripeCustomer, ok := customersByEmail[strings.ToLower(user.Email)]; ok {
			// Found matching customer
			user.StripeCustomerId = stripeCustomer.ID

			// Check for active subscription
			if stripeCustomer.Subscriptions != nil && len(stripeCustomer.Subscriptions.Data) > 0 {
				// Get the first subscription (most recent)
				sub := stripeCustomer.Subscriptions.Data[0]
				user.StripeSubscriptionId = sub.ID
				user.SubscriptionStatus = string(sub.Status)
			}

			// Update user
			if err := admin.Controller.Users.Update(user); err != nil {
				admin.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("Failed to update user %s during Stripe sync: %v", user.Email, err))
				continue
			}

			matched++
			updatedUsers = append(updatedUsers, map[string]interface{}{
				"email":              user.Email,
				"stripeCustomerId":   user.StripeCustomerId,
				"subscriptionId":     user.StripeSubscriptionId,
				"subscriptionStatus": user.SubscriptionStatus,
			})
		}
	}

	// Write all updated users to database
	if err := admin.Controller.Users.Write(admin.Controller.Database); err != nil {
		admin.Controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("Failed to write users after Stripe sync: %v", err))
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Failed to save user updates",
		})
		return
	}

	// Find unmatched Stripe customers
	userEmails := make(map[string]bool)
	for _, user := range users {
		userEmails[strings.ToLower(user.Email)] = true
	}

	for email, customer := range customersByEmail {
		if !userEmails[email] {
			unmatchedCustomers = append(unmatchedCustomers, map[string]interface{}{
				"email": customer.Email,
				"id":    customer.ID,
			})
		}
	}

	// Sync config to file if enabled
	admin.Controller.SyncConfigToFile()

	// Return results
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":            true,
		"totalUsers":         len(users),
		"stripeCustomers":    len(customersByEmail),
		"matched":            matched,
		"unmatched":          len(users) - matched,
		"updatedUsers":       updatedUsers,
		"unmatchedCustomers": unmatchedCustomers,
	})

	admin.Controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("Stripe sync completed: %d users matched", matched))
}

func (admin *Admin) GetAuthorization(r *http.Request) string {
	return r.Header.Get("Authorization")
}

func (admin *Admin) GetConfig() map[string]any {
	// Get all users for export
	users := admin.Controller.Users.GetAllUsers()
	userList := make([]map[string]any, 0, len(users))
	for _, user := range users {
		userList = append(userList, map[string]any{
			"id":                   user.Id,
			"email":                user.Email,
			"password":             user.Password, // Export password hash for import/restore
			"firstName":            user.FirstName,
			"lastName":             user.LastName,
			"zipCode":              user.ZipCode,
			"verified":             user.Verified,
			"createdAt":            user.CreatedAt,
			"lastLogin":            user.LastLogin,
			"systems":              user.Systems,
			"delay":                user.Delay,
			"systemDelays":         user.SystemDelays,
			"talkgroupDelays":      user.TalkgroupDelays,
			"settings":             user.Settings,
			"pin":                  user.Pin,
			"pinExpiresAt":         user.PinExpiresAt,
			"connectionLimit":      user.ConnectionLimit,
			"userGroupId":          user.UserGroupId,
			"isGroupAdmin":         user.IsGroupAdmin,
			"systemAdmin":          user.SystemAdmin,
			"stripeCustomerId":     user.StripeCustomerId,
			"stripeSubscriptionId": user.StripeSubscriptionId,
			"subscriptionStatus":   user.SubscriptionStatus,
			"accountExpiresAt":     user.AccountExpiresAt,
		})
	}

	// Get all user groups for export
	userGroups := admin.Controller.UserGroups.GetAll()
	userGroupList := make([]map[string]any, 0, len(userGroups))
	for _, group := range userGroups {
		userGroupList = append(userGroupList, map[string]any{
			"id":                    group.Id,
			"name":                  group.Name,
			"description":           group.Description,
			"systemAccess":          group.SystemAccess,
			"delay":                 group.Delay,
			"systemDelays":          group.SystemDelays,
			"talkgroupDelays":       group.TalkgroupDelays,
			"connectionLimit":       group.ConnectionLimit,
			"maxUsers":              group.MaxUsers,
			"billingEnabled":        group.BillingEnabled,
			"stripePriceId":         group.StripePriceId,
			"pricingOptions":        group.PricingOptions,
			"billingMode":           group.BillingMode,
			"collectSalesTax":       group.CollectSalesTax,
			"isPublicRegistration":  group.IsPublicRegistration,
			"allowAddExistingUsers": group.AllowAddExistingUsers,
			"createdAt":             group.CreatedAt,
		})
	}

	// Get all device tokens for export
	deviceTokenList := make([]map[string]any, 0)
	admin.Controller.DeviceTokens.mutex.RLock()
	for _, token := range admin.Controller.DeviceTokens.tokens {
		deviceTokenList = append(deviceTokenList, map[string]any{
			"id":        token.Id,
			"userId":    token.UserId,
			"token":     token.Token, // OneSignal player ID
			"platform":  token.Platform,
			"sound":     token.Sound,
			"createdAt": token.CreatedAt,
			"lastUsed":  token.LastUsed,
		})
	}
	admin.Controller.DeviceTokens.mutex.RUnlock()

	// Get all keyword lists for export
	keywordListList := make([]map[string]any, 0)
	query := `SELECT "keywordListId", "label", "description", "keywords", "order", "createdAt" FROM "keywordLists" ORDER BY "order" ASC, "createdAt" DESC`
	rows, err := admin.Controller.Database.Sql.Query(query)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var (
				listId       uint64
				label        string
				description  string
				keywordsJson string
				order        uint
				createdAt    int64
			)

			if err := rows.Scan(&listId, &label, &description, &keywordsJson, &order, &createdAt); err != nil {
				continue
			}

			var keywords []string
			if keywordsJson != "" && keywordsJson != "[]" {
				json.Unmarshal([]byte(keywordsJson), &keywords)
			}

			keywordListList = append(keywordListList, map[string]any{
				"id":          listId,
				"label":       label,
				"description": description,
				"keywords":    keywords,
				"order":       order,
				"createdAt":   createdAt,
			})
		}
	}

	// Get all user alert preferences for export
	userAlertPrefList := make([]map[string]any, 0)
	alertQuery := `SELECT "userAlertPreferenceId", "userId", "systemId", "talkgroupId", "alertEnabled", "toneAlerts", "keywordAlerts", "keywords", "keywordListIds", "toneSetIds" FROM "userAlertPreferences" ORDER BY "userId" ASC`
	alertRows, alertErr := admin.Controller.Database.Sql.Query(alertQuery)
	if alertErr == nil {
		defer alertRows.Close()
		for alertRows.Next() {
			var (
				prefId         uint64
				userId         uint64
				systemId       uint64
				talkgroupId    uint64
				alertEnabled   bool
				toneAlerts     bool
				keywordAlerts  bool
				keywordsJson   string
				keywordListIds string
				toneSetIds     string
			)

			if err := alertRows.Scan(&prefId, &userId, &systemId, &talkgroupId, &alertEnabled, &toneAlerts, &keywordAlerts, &keywordsJson, &keywordListIds, &toneSetIds); err != nil {
				continue
			}

			var keywords []string
			if keywordsJson != "" && keywordsJson != "[]" {
				json.Unmarshal([]byte(keywordsJson), &keywords)
			}

			var keywordListIdsParsed []int
			if keywordListIds != "" && keywordListIds != "[]" {
				json.Unmarshal([]byte(keywordListIds), &keywordListIdsParsed)
			}

			var toneSetIdsParsed []string
			if toneSetIds != "" && toneSetIds != "[]" {
				json.Unmarshal([]byte(toneSetIds), &toneSetIdsParsed)
			}

			userAlertPrefList = append(userAlertPrefList, map[string]any{
				"id":             prefId,
				"userId":         userId,
				"systemId":       systemId,
				"talkgroupId":    talkgroupId,
				"alertEnabled":   alertEnabled,
				"toneAlerts":     toneAlerts,
				"keywordAlerts":  keywordAlerts,
				"keywords":       keywords,
				"keywordListIds": keywordListIdsParsed,
				"toneSetIds":     toneSetIdsParsed,
			})
		}
	}

	return map[string]any{
		"apikeys":              admin.Controller.Apikeys.List,
		"branding":             admin.Controller.Options.Branding,
		"dirwatch":             admin.Controller.Dirwatches.List,
		"downstreams":          admin.Controller.Downstreams.List,
		"groups":               admin.Controller.Groups.List,
		"options":              admin.Controller.Options,
		"systems":              admin.Controller.Systems.List,
		"tags":                 admin.Controller.Tags.List,
		"users":                userList,
		"userGroups":           userGroupList,
		"deviceTokens":         deviceTokenList,
		"keywordLists":         keywordListList,
		"userAlertPreferences": userAlertPrefList,
		"version":              Version,
	}
}

func (admin *Admin) LogsHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodPost:
		m := map[string]any{}
		err := json.NewDecoder(r.Body).Decode(&m)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		logOptions := NewLogSearchOptions().FromMap(m)

		r, err := admin.Controller.Logs.Search(logOptions, admin.Controller.Database)
		if err != nil {
			admin.Controller.Logs.LogEvent(LogLevelError, err.Error())
			w.WriteHeader(http.StatusExpectationFailed)
			return
		}

		b, err := json.Marshal(r)
		if err != nil {
			admin.Controller.Logs.LogEvent(LogLevelError, err.Error())
			w.WriteHeader(http.StatusExpectationFailed)
			return
		}

		w.Write(b)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) CallsHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodPost:
		m := map[string]any{}
		err := json.NewDecoder(r.Body).Decode(&m)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		callOptions := NewCallSearchOptions().fromMap(m)

		// Create a minimal client for admin search (bypasses user restrictions)
		adminClient := &Client{
			Controller: admin.Controller,
			User:       nil, // Admin has no user restrictions
		}

		results, err := admin.Controller.Calls.Search(callOptions, adminClient)
		if err != nil {
			admin.Controller.Logs.LogEvent(LogLevelError, err.Error())
			w.WriteHeader(http.StatusExpectationFailed)
			return
		}

		b, err := json.Marshal(results)
		if err != nil {
			admin.Controller.Logs.LogEvent(LogLevelError, err.Error())
			w.WriteHeader(http.StatusExpectationFailed)
			return
		}

		w.Write(b)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) PurgeHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodPost:
		m := map[string]any{}
		err := json.NewDecoder(r.Body).Decode(&m)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var purgeType string
		if v, ok := m["type"].(string); ok {
			purgeType = v
		} else {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "type parameter is required",
			})
			return
		}

		// Check if IDs are provided for selective deletion
		var ids []uint64
		if idsInterface, ok := m["ids"].([]interface{}); ok {
			for _, idInterface := range idsInterface {
				switch v := idInterface.(type) {
				case float64:
					ids = append(ids, uint64(v))
				case uint64:
					ids = append(ids, v)
				case int:
					ids = append(ids, uint64(v))
				}
			}
		}

		switch purgeType {
		case "calls":
			if len(ids) > 0 {
				// Delete specific calls by IDs
				err = admin.Controller.Calls.DeleteByIDs(admin.Controller.Database, ids)
				if err != nil {
					admin.Controller.Logs.LogEvent(LogLevelError, err.Error())
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]string{
						"error": err.Error(),
					})
					return
				}
				admin.Controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("%d calls deleted by admin", len(ids)))
				json.NewEncoder(w).Encode(map[string]interface{}{
					"message": fmt.Sprintf("%d calls deleted successfully", len(ids)),
					"deleted": len(ids),
				})
			} else {
				// Delete all calls
				err = admin.Controller.Calls.PurgeAll(admin.Controller.Database)
				if err != nil {
					admin.Controller.Logs.LogEvent(LogLevelError, err.Error())
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]string{
						"error": err.Error(),
					})
					return
				}
				admin.Controller.Logs.LogEvent(LogLevelInfo, "All calls purged by admin")
				json.NewEncoder(w).Encode(map[string]string{
					"message": "All calls purged successfully",
				})
			}

		case "logs":
			if len(ids) > 0 {
				// Delete specific logs by IDs
				err = admin.Controller.Logs.DeleteByIDs(admin.Controller.Database, ids)
				if err != nil {
					admin.Controller.Logs.LogEvent(LogLevelError, err.Error())
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]string{
						"error": err.Error(),
					})
					return
				}
				// Note: Can't log to logs table after deleting logs, so we just write response
				json.NewEncoder(w).Encode(map[string]interface{}{
					"message": fmt.Sprintf("%d logs deleted successfully", len(ids)),
					"deleted": len(ids),
				})
			} else {
				// Delete all logs
				err = admin.Controller.Logs.PurgeAll(admin.Controller.Database)
				if err != nil {
					admin.Controller.Logs.LogEvent(LogLevelError, err.Error())
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]string{
						"error": err.Error(),
					})
					return
				}
				// Note: Can't log to logs table after purging logs, so we just write response
				json.NewEncoder(w).Encode(map[string]string{
					"message": "All logs purged successfully",
				})
			}

		default:
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "Invalid type. Must be 'calls' or 'logs'",
			})
			return
		}

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) LoginHandler(w http.ResponseWriter, r *http.Request) {
	// Check localhost restriction if enabled
	clientIP := GetClientIP(r)
	isLocalhost := IsLocalhostIP(clientIP)

	if admin.Controller.Options.AdminLocalhostOnly {
		if !isLocalhost {
			// Enhanced logging with request context
			userAgent := r.Header.Get("User-Agent")
			if userAgent == "" {
				userAgent = "none"
			}
			admin.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("admin: Admin login attempt denied from non-localhost IP | IP=%s | Endpoint=%s %s | UserAgent=%s",
				clientIP, r.Method, r.URL.Path, userAgent))
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "Admin access restricted to localhost only",
			})
			return
		}
	}

	switch r.Method {
	case http.MethodPost:
		m := map[string]any{}

		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		remoteAddr := GetRemoteAddr(r)

		attempt := admin.Attempts[remoteAddr]

		if attempt == nil {
			admin.Attempts[remoteAddr] = &AdminLoginAttempt{
				Count: 1,
				Date:  time.Now(),
			}
			attempt = admin.Attempts[remoteAddr]
		} else {
			attempt.Count++
			attempt.Date = time.Now()
		}

		if attempt.Count > admin.AttemptsMax || time.Since(attempt.Date) < admin.AttemptsMaxDelay {
			if attempt.Count == admin.AttemptsMax+1 {
				// Enhanced logging with request context
				userAgent := r.Header.Get("User-Agent")
				if userAgent == "" {
					userAgent = "none"
				}
				admin.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("admin: Too many login attempts | IP=%s | Endpoint=%s %s | UserAgent=%s | AttemptCount=%d",
					remoteAddr, r.Method, r.URL.Path, userAgent, attempt.Count))
			}

			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		ok := false

		switch v := m["password"].(type) {
		case string:
			if len(v) > 0 {
				if err := bcrypt.CompareHashAndPassword([]byte(admin.Controller.Options.adminPassword), []byte(v)); err == nil {
					ok = true
				}
			}
		}

		if !ok {
			// Record failed attempt
			admin.Controller.LoginAttemptTracker.RecordFailedAttempt(remoteAddr)
			// Enhanced logging with request context
			userAgent := r.Header.Get("User-Agent")
			if userAgent == "" {
				userAgent = "none"
			}
			admin.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("admin: Invalid login attempt | IP=%s | Endpoint=%s %s | UserAgent=%s",
				remoteAddr, r.Method, r.URL.Path, userAgent))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// Login successful - reset failed attempts
		admin.Controller.LoginAttemptTracker.RecordSuccess(remoteAddr)

		id, err := uuid.NewRandom()

		if err != nil {
			w.WriteHeader(http.StatusExpectationFailed)
			return
		}

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{ID: id.String()})
		sToken, err := token.SignedString([]byte(admin.Controller.Options.secret))

		if err != nil {
			w.WriteHeader(http.StatusExpectationFailed)
			return
		}

		if len(admin.Tokens) < 5 {
			admin.Tokens = append(admin.Tokens, sToken)
		} else {
			admin.Tokens = append(admin.Tokens[1:], sToken)
		}

		b, err := json.Marshal(map[string]any{
			"passwordNeedChange": true,
			"token":              sToken,
		})
		if err != nil {
			w.WriteHeader(http.StatusExpectationFailed)
			return
		}

		for k, v := range admin.Attempts {
			if time.Since(v.Date) > admin.AttemptsMaxDelay {
				delete(admin.Attempts, k)
			}
		}

		w.Write(b)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		t := admin.GetAuthorization(r)
		if !admin.ValidateToken(t) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		for k, v := range admin.Tokens {
			if v == t {
				admin.Tokens = append(admin.Tokens[:k], admin.Tokens[k+1:]...)
			}
		}
		w.WriteHeader(http.StatusOK)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) PasswordHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var (
			b               []byte
			currentPassword any
			newPassword     string
		)

		logError := func(err error) {
			admin.Controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("admin.passwordhandler.post: %s", err.Error()))
		}

		t := admin.GetAuthorization(r)
		if !admin.ValidateToken(t) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		m := map[string]any{}
		err := json.NewDecoder(r.Body).Decode(&m)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		switch v := m["currentPassword"].(type) {
		case string:
			currentPassword = v
		}

		switch v := m["newPassword"].(type) {
		case string:
			newPassword = v
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if err = admin.ChangePassword(currentPassword, newPassword); err != nil {
			logError(errors.New("unable to change admin password, current password is invalid"))
			w.WriteHeader(http.StatusExpectationFailed)
			return
		}

		if b, err = json.Marshal(map[string]any{"passwordNeedChange": admin.Controller.Options.adminPasswordNeedChange}); err == nil {
			w.Write(b)
		} else {
			w.WriteHeader(http.StatusExpectationFailed)
		}

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) SendConfig(w http.ResponseWriter) {
	var m map[string]any
	_, docker := os.LookupEnv("DOCKER")
	if docker {
		m = map[string]any{
			"config":             admin.GetConfig(),
			"docker":             docker,
			"passwordNeedChange": admin.Controller.Options.adminPasswordNeedChange,
		}
	} else {
		m = map[string]any{
			"config":             admin.GetConfig(),
			"passwordNeedChange": admin.Controller.Options.adminPasswordNeedChange,
		}
	}
	if b, err := json.Marshal(m); err == nil {
		w.Write(b)
	} else {
		w.WriteHeader(http.StatusExpectationFailed)
	}
}

func (admin *Admin) Start() error {
	if admin.running {
		return errors.New("admin already running")
	} else {
		admin.running = true
	}

	go func() {
		for {
			select {
			case data, ok := <-admin.Broadcast:
				if !ok {
					return
				}

				for conn := range admin.Conns {
					err := conn.WriteMessage(websocket.TextMessage, *data)
					if err != nil {
						admin.Unregister <- conn
					}
				}

			case conn := <-admin.Register:
				admin.Conns[conn] = true

			case conn := <-admin.Unregister:
				if _, ok := admin.Conns[conn]; ok {
					delete(admin.Conns, conn)
					conn.Close()
				}
			}
		}
	}()

	return nil
}

func (admin *Admin) UserAddHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (admin *Admin) UserRemoveHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (admin *Admin) UserEditHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (admin *Admin) ValidateToken(sToken string) bool {
	found := false
	for _, t := range admin.Tokens {
		if t == sToken {
			found = true
			break
		}
	}
	if !found {
		return false
	}

	token, err := jwt.Parse(sToken, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		return []byte(admin.Controller.Options.secret), nil
	})
	if err != nil {
		return false
	}

	return token.Valid
}

func (admin *Admin) RadioReferenceTestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		Username string `json:"username"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if request.Username == "" {
		http.Error(w, "Username is required", http.StatusBadRequest)
		return
	}

	// Check if RadioReference is enabled and credentials are configured
	if !admin.Controller.Options.RadioReferenceEnabled {
		http.Error(w, "Radio Reference is not enabled", http.StatusExpectationFailed)
		return
	}

	if admin.Controller.Options.RadioReferenceUsername == "" || admin.Controller.Options.RadioReferencePassword == "" {
		http.Error(w, "Radio Reference credentials are not configured", http.StatusExpectationFailed)
		return
	}

	// Verify the username matches the stored username
	if request.Username != admin.Controller.Options.RadioReferenceUsername {
		http.Error(w, "Username does not match configured credentials", http.StatusExpectationFailed)
		return
	}

	rr := NewRadioReferenceService(admin.Controller.Options.RadioReferenceUsername, admin.Controller.Options.RadioReferencePassword, admin.Controller.Options.RadioReferenceAPIKey)
	userInfo, err := rr.TestConnection()

	if err != nil {
		log.Printf("Radio Reference connection test failed: %v", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"userInfo": userInfo,
	})
}

func (admin *Admin) RadioReferenceSearchHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodPost:
		m := map[string]any{}
		err := json.NewDecoder(r.Body).Decode(&m)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		query, ok := m["query"].(string)
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if !admin.Controller.Options.RadioReferenceEnabled {
			w.WriteHeader(http.StatusExpectationFailed)
			json.NewEncoder(w).Encode(map[string]string{"error": "Radio Reference is not enabled"})
			return
		}

		rr := NewRadioReferenceService(
			admin.Controller.Options.RadioReferenceUsername,
			admin.Controller.Options.RadioReferencePassword,
			admin.Controller.Options.RadioReferenceAPIKey,
		)

		systems, err := rr.SearchSystems(query)
		if err != nil {
			w.WriteHeader(http.StatusExpectationFailed)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"systems": systems,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (admin *Admin) RadioReferenceImportHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodPost:
		m := map[string]any{}
		err := json.NewDecoder(r.Body).Decode(&m)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		systemID, ok1 := m["systemID"].(float64)
		importType, ok2 := m["importType"].(string)
		destinationType, ok3 := m["destinationType"].(string) // Destination for import
		if !ok1 || !ok2 || !ok3 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Missing required parameters"})
			return
		}

		if destinationType != "system" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Library imports are no longer supported"})
			return
		}

		// Extract optional filter parameters
		groupFilter, _ := m["groupFilter"].(string)
		tagFilter, _ := m["tagFilter"].(string)
		var encryptedFilter *bool
		if v, ok := m["encryptedFilter"].(bool); ok {
			encryptedFilter = &v
		} else if vPtr, ok := m["encryptedFilter"].(*bool); ok && vPtr != nil {
			encryptedFilter = vPtr
		}

		// Extract pagination parameters - only set defaults if explicitly provided
		page, hasPage := m["page"].(float64)
		pageSize, hasPageSize := m["pageSize"].(float64)

		rr := NewRadioReferenceService(
			admin.Controller.Options.RadioReferenceUsername,
			admin.Controller.Options.RadioReferencePassword,
			admin.Controller.Options.RadioReferenceAPIKey,
		)

		var result map[string]any
		switch importType {
		case "talkgroups":
			// Check if this is a request for total count only
			if countOnly, ok := m["countOnly"].(bool); ok && countOnly {
				totalCount, err := admin.getTotalTalkgroupCount(rr, int(systemID), groupFilter, tagFilter, encryptedFilter)
				if err != nil {
					w.WriteHeader(http.StatusExpectationFailed)
					json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
					return
				}

				result = map[string]any{
					"success":         true,
					"importType":      "talkgroups",
					"destinationType": destinationType,
					"totalCount":      totalCount,
					"pageSize":        pageSize,
					"totalPages":      int(math.Ceil(float64(totalCount) / pageSize)),
				}
			} else if loadAll, ok := m["loadAll"].(bool); ok && loadAll {
				// Load all talkgroups at once (streaming approach)
				allTalkgroups, err := admin.getAllTalkgroupsStreaming(rr, int(systemID), groupFilter, tagFilter, encryptedFilter)
				if err != nil {
					w.WriteHeader(http.StatusExpectationFailed)
					json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
					return
				}

				// Import directly to the system (v6 style behaviour)
				result = map[string]any{
					"success":         true,
					"importType":      "talkgroups",
					"destinationType": destinationType,
					"data":            allTalkgroups,
					"totalCount":      len(allTalkgroups),
					"mode":            "all",
				}
			} else if hasPage && hasPageSize {
				// Get paginated talkgroups (explicit pagination request)
				talkgroups, totalCount, err := admin.getPaginatedTalkgroups(rr, int(systemID), int(page), int(pageSize), groupFilter, tagFilter, encryptedFilter)
				if err != nil {
					w.WriteHeader(http.StatusExpectationFailed)
					json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
					return
				}

				result = map[string]any{
					"success":         true,
					"importType":      "talkgroups",
					"destinationType": destinationType,
					"data":            talkgroups,
					"pagination": map[string]any{
						"page":       page,
						"pageSize":   pageSize,
						"totalCount": totalCount,
						"totalPages": int(math.Ceil(float64(totalCount) / pageSize)),
						"hasNext":    int(page)*int(pageSize) < totalCount,
						"hasPrev":    int(page) > 1,
					},
					"mode": "pagination",
				}
			} else {
				// Default behaviour: stream all talkgroups directly to the system
				err := admin.getAllTalkgroupsWithProgress(w, rr, int(systemID), groupFilter, tagFilter, encryptedFilter)
				if err != nil {
					w.WriteHeader(http.StatusExpectationFailed)
					json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
					return
				}
				// Response already sent by getAllTalkgroupsWithProgress
				return
			}
		case "sites":
			sites, err := rr.GetSites(int(systemID))
			if err != nil {
				w.WriteHeader(http.StatusExpectationFailed)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			result = map[string]any{
				"success":         true,
				"importType":      "sites",
				"destinationType": destinationType,
				"data":            sites,
			}
		default:
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid import type"})
			return
		}

		json.NewEncoder(w).Encode(result)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// getTotalTalkgroupCount gets the total count of talkgroups without loading them all into memory
func (admin *Admin) getTotalTalkgroupCount(rr *RadioReferenceService, systemID int, groupFilter, tagFilter string, encryptedFilter *bool) (int, error) {
	// Create context with timeout to prevent endless cycling
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Get talkgroup categories first
	categories, err := rr.GetTalkgroupCategories(systemID)
	if err != nil {
		return 0, err
	}

	totalCount := 0
	for _, category := range categories {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return totalCount, fmt.Errorf("timeout reached while counting talkgroups")
		default:
		}

		// Get talkgroups for this category
		talkgroups, err := rr.GetTalkgroupsByCategory(systemID, category.ID, category.Name)
		if err != nil {
			continue
		}

		// Apply filters and count
		for _, tg := range talkgroups {
			if admin.talkgroupMatchesFilter(tg, groupFilter, tagFilter, encryptedFilter) {
				totalCount++
			}
		}
	}

	return totalCount, nil
}

// getPaginatedTalkgroups gets talkgroups for a specific page without loading all into memory
func (admin *Admin) getPaginatedTalkgroups(rr *RadioReferenceService, systemID, page, pageSize int, groupFilter, tagFilter string, encryptedFilter *bool) ([]RadioReferenceTalkgroup, int, error) {
	// Create context with timeout to prevent endless cycling
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Get talkgroup categories first
	categories, err := rr.GetTalkgroupCategories(systemID)
	if err != nil {
		return nil, 0, err
	}

	var allTalkgroups []RadioReferenceTalkgroup
	totalCount := 0
	startIndex := (page - 1) * pageSize
	endIndex := startIndex + pageSize
	currentIndex := 0

	// Stream through categories and collect talkgroups for the requested page
	for _, category := range categories {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return allTalkgroups, totalCount, fmt.Errorf("timeout reached while fetching talkgroups")
		default:
		}

		if currentIndex >= endIndex {
			break // We have enough talkgroups for this page
		}

		talkgroups, err := rr.GetTalkgroupsByCategory(systemID, category.ID, category.Name)
		if err != nil {
			continue
		}

		// Process talkgroups for this category
		for _, tg := range talkgroups {
			if admin.talkgroupMatchesFilter(tg, groupFilter, tagFilter, encryptedFilter) {
				totalCount++

				// Only add talkgroups for the current page
				if currentIndex >= startIndex && currentIndex < endIndex {
					allTalkgroups = append(allTalkgroups, tg)
				}

				currentIndex++
			}
		}

	}

	return allTalkgroups, totalCount, nil
}

// getAllTalkgroupsStreaming loads all talkgroups for a system into memory using streaming
func (admin *Admin) getAllTalkgroupsStreaming(rr *RadioReferenceService, systemID int, groupFilter, tagFilter string, encryptedFilter *bool) ([]RadioReferenceTalkgroup, error) {
	// Create context with much longer timeout for large systems (312 categories)
	// 10 minutes should be enough for even the largest systems
	ctx, cancel := context.WithTimeout(context.Background(), 10*60*time.Second)
	defer cancel()

	var allTalkgroups []RadioReferenceTalkgroup

	// Get talkgroup categories first
	categories, err := rr.GetTalkgroupCategories(systemID)
	if err != nil {
		return nil, fmt.Errorf("failed to get categories: %v", err)
	}

	// Stream through categories and collect talkgroups
	successfulCategories := 0
	failedCategories := 0
	emptyCategories := 0

	for i, category := range categories {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return allTalkgroups, fmt.Errorf("timeout reached while streaming talkgroups at category %d/%d", i+1, len(categories))
		default:
		}

		// Add retry logic for failed categories
		var talkgroups []RadioReferenceTalkgroup
		var err error
		maxRetries := 3

		for retry := 0; retry < maxRetries; retry++ {
			talkgroups, err = rr.GetTalkgroupsByCategory(systemID, category.ID, category.Name)
			if err == nil {
				break
			}
			if retry < maxRetries-1 {
				time.Sleep(2 * time.Second) // Wait 2 seconds before retry
			}
		}

		if err != nil {
			failedCategories++
			continue // Continue with other categories instead of failing completely
		}

		if len(talkgroups) == 0 {
			emptyCategories++
			continue
		}

		// Process talkgroups for this category
		filteredCount := 0

		for _, tg := range talkgroups {
			if admin.talkgroupMatchesFilter(tg, groupFilter, tagFilter, encryptedFilter) {
				allTalkgroups = append(allTalkgroups, tg)
				filteredCount++
			}
		}

		successfulCategories++

		// Add a small delay between categories to avoid overwhelming the API
		if i < len(categories)-1 { // Don't delay after the last category
			time.Sleep(100 * time.Millisecond)
		}
	}

	if successfulCategories == 0 {
		return nil, fmt.Errorf("no categories were processed successfully")
	}

	return allTalkgroups, nil
}

// getAllTalkgroupsWithTempFile loads all talkgroups using a temp file to prevent memory issues
func (admin *Admin) getAllTalkgroupsWithTempFile(w http.ResponseWriter, rr *RadioReferenceService, systemID int, groupFilter, tagFilter string, encryptedFilter *bool) error {
	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 15*60*time.Second) // 15 minutes
	defer cancel()

	// Create temp file
	tempFile, err := os.CreateTemp("", "talkgroups_*.json")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %v", err)
	}
	defer os.Remove(tempFile.Name()) // Clean up temp file
	defer tempFile.Close()

	// Write JSON array start
	tempFile.WriteString("[\n")

	// Get talkgroup categories first
	categories, err := rr.GetTalkgroupCategories(systemID)
	if err != nil {
		return fmt.Errorf("failed to get categories: %v", err)
	}

	// Set response headers for streaming
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)

	// Write response start
	w.Write([]byte(`{"success":true,"importType":"talkgroups","mode":"streaming","data":[`))

	// Stream through categories and write talkgroups directly to response
	successfulCategories := 0
	failedCategories := 0
	emptyCategories := 0
	totalTalkgroups := 0
	isFirst := true

	for i, category := range categories {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout reached while streaming talkgroups")
		default:
		}

		// Get talkgroups for this category with retry logic
		var talkgroups []RadioReferenceTalkgroup
		var err error
		maxRetries := 3

		for retry := 0; retry < maxRetries; retry++ {
			talkgroups, err = rr.GetTalkgroupsByCategory(systemID, category.ID, category.Name)
			if err == nil {
				break
			}
			if retry < maxRetries-1 {
				time.Sleep(2 * time.Second)
			}
		}

		if err != nil {
			failedCategories++
			continue
		}

		if len(talkgroups) == 0 {
			emptyCategories++
			continue
		}

		// Process and stream talkgroups for this category
		filteredCount := 0

		for _, tg := range talkgroups {
			if admin.talkgroupMatchesFilter(tg, groupFilter, tagFilter, encryptedFilter) {
				// Convert to JSON
				tgJSON, err := json.Marshal(tg)
				if err != nil {
					continue
				}

				// Write to temp file
				if !isFirst {
					tempFile.WriteString(",\n")
				}
				tempFile.Write(tgJSON)

				// Stream to client
				if !isFirst {
					w.Write([]byte(","))
				}
				w.Write(tgJSON)

				// Flush to ensure data is sent immediately
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}

				isFirst = false
				filteredCount++
				totalTalkgroups++
			}
		}

		successfulCategories++

		// Small delay between categories
		if i < len(categories)-1 {
			time.Sleep(100 * time.Millisecond)
		}
	}

	// Write response end
	w.Write([]byte(`],"totalCount":` + strconv.Itoa(totalTalkgroups) + `}`))

	// Flush final data
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	if successfulCategories == 0 {
		return fmt.Errorf("no categories were processed successfully")
	}

	return nil
}

// getAllTalkgroupsWithProgress streams talkgroups with real-time progress updates
func (admin *Admin) getAllTalkgroupsWithProgress(w http.ResponseWriter, rr *RadioReferenceService, systemID int, groupFilter, tagFilter string, encryptedFilter *bool) error {
	// Set up HTTP headers for streaming
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Keep-Alive", "timeout=900, max=1000")

	// Get flusher for streaming
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported")
	}

	// Create context with timeout (20 minutes - longer than HTTP server timeout)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// Get talkgroup categories
	categories, err := rr.GetTalkgroupCategories(systemID)
	if err != nil {
		return fmt.Errorf("failed to get talkgroup categories: %v", err)
	}

	totalCategories := len(categories)

	// Send initial progress
	initialProgress := map[string]any{
		"type":    "progress",
		"current": 0,
		"total":   totalCategories,
		"message": fmt.Sprintf("Starting processing of %d categories...", totalCategories),
		"status":  "starting",
	}

	// Check if context is cancelled before sending initial progress
	select {
	case <-ctx.Done():
		return fmt.Errorf("client disconnected: %v", ctx.Err())
	default:
		// Continue processing
	}

	initialProgressJSON, _ := json.Marshal(initialProgress)
	if _, err := w.Write(initialProgressJSON); err != nil {
		return fmt.Errorf("failed to write initial progress: %v", err)
	}
	if _, err := w.Write([]byte("\n")); err != nil {
		return fmt.Errorf("failed to write initial progress newline: %v", err)
	}
	flusher.Flush()

	// Initialize variables
	var allTalkgroups []RadioReferenceTalkgroup
	var successfulCategories, failedCategories, emptyCategories int

	// Process categories in chunks with checkpointing
	chunkSize := 10 // Reduced from 25 to 10 for more frequent progress updates

	for i := 0; i < totalCategories; i += chunkSize {
		endIndex := i + chunkSize
		if endIndex > totalCategories {
			endIndex = totalCategories
		}

		// Check if context is cancelled (client disconnected)
		select {
		case <-ctx.Done():
			return fmt.Errorf("client disconnected: %v", ctx.Err())
		default:
			// Continue processing
		}

		// Process current chunk
		chunkTalkgroups, chunkSuccessful, chunkFailed, chunkEmpty, err := admin.processCategoryChunk(
			ctx, rr, categories[i:endIndex], i, totalCategories, systemID, groupFilter, tagFilter, encryptedFilter,
		)

		if err != nil {
			// Send error progress and continue with next chunk
			errorProgress := map[string]any{
				"type":    "progress",
				"current": i,
				"total":   totalCategories,
				"message": fmt.Sprintf("Chunk failed, continuing... Error: %v", err),
				"status":  "processing",
			}

			// Check if context is cancelled before sending error progress
			select {
			case <-ctx.Done():
				return fmt.Errorf("client disconnected: %v", ctx.Err())
			default:
				// Continue processing
			}

			errorProgressJSON, _ := json.Marshal(errorProgress)
			if _, err := w.Write(errorProgressJSON); err != nil {
				return fmt.Errorf("failed to write error progress: %v", err)
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				return fmt.Errorf("failed to write error progress newline: %v", err)
			}
			flusher.Flush()
			continue
		}

		// Send progress for each category in the chunk
		for j, category := range categories[i:endIndex] {
			// Check if context is cancelled before sending each progress update
			select {
			case <-ctx.Done():
				return fmt.Errorf("client disconnected: %v", ctx.Err())
			default:
				// Continue processing
			}

			progress := map[string]any{
				"type":    "progress",
				"current": i + j + 1,
				"total":   totalCategories,
				"message": fmt.Sprintf("Processing category %d/%d: %s", i+j+1, totalCategories, category.Name),
				"status":  "processing",
			}
			progressJSON, _ := json.Marshal(progress)

			if _, err := w.Write(progressJSON); err != nil {
				return fmt.Errorf("failed to write progress: %v", err)
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				return fmt.Errorf("failed to write newline: %v", err)
			}

			// Log after writing to confirm success

			flusher.Flush()
		}

		// Send heartbeat to keep connection alive
		heartbeat := map[string]any{
			"type":    "heartbeat",
			"current": i + chunkSize,
			"total":   totalCategories,
			"message": "Processing in progress...",
			"status":  "processing",
		}
		heartbeatJSON, _ := json.Marshal(heartbeat)

		// Log before sending heartbeat

		if _, err := w.Write(heartbeatJSON); err != nil {
			return fmt.Errorf("failed to write heartbeat: %v", err)
		}
		if _, err := w.Write([]byte("\n")); err != nil {
			return fmt.Errorf("failed to write heartbeat newline: %v", err)
		}

		flusher.Flush()

		// Send checkpoint message
		checkpoint := map[string]any{
			"type":    "checkpoint",
			"current": i + chunkSize,
			"total":   totalCategories,
			"message": fmt.Sprintf("Completed %d/%d categories, collected %d talkgroups", i+chunkSize, totalCategories, len(allTalkgroups)),
			"status":  "checkpoint",
		}
		checkpointJSON, _ := json.Marshal(checkpoint)

		// Log before sending checkpoint

		if _, err := w.Write(checkpointJSON); err != nil {
			return fmt.Errorf("failed to write checkpoint: %v", err)
		}
		if _, err := w.Write([]byte("\n")); err != nil {
			return fmt.Errorf("failed to write checkpoint newline: %v", err)
		}

		flusher.Flush()

		// Accumulate results
		allTalkgroups = append(allTalkgroups, chunkTalkgroups...)
		successfulCategories += chunkSuccessful
		failedCategories += chunkFailed
		emptyCategories += chunkEmpty
	}

	// Send completion message
	completion := map[string]any{
		"type":       "complete",
		"data":       allTalkgroups,
		"totalCount": len(allTalkgroups),
		"stats": map[string]any{
			"totalCategories":      totalCategories,
			"successfulCategories": successfulCategories,
			"failedCategories":     failedCategories,
			"emptyCategories":      emptyCategories,
		},
	}

	// Check if context is cancelled before sending completion
	select {
	case <-ctx.Done():
		return fmt.Errorf("client disconnected: %v", ctx.Err())
	default:
		// Continue processing
	}

	completionJSON, _ := json.Marshal(completion)
	if _, err := w.Write(completionJSON); err != nil {
		return fmt.Errorf("failed to write completion message: %v", err)
	}
	if _, err := w.Write([]byte("\n")); err != nil {
		return fmt.Errorf("failed to write completion newline: %v", err)
	}
	flusher.Flush()

	return nil
}

// processCategoryChunk processes a chunk of categories and returns the results
func (admin *Admin) processCategoryChunk(ctx context.Context, rr *RadioReferenceService, categories []RadioReferenceTalkgroupCategory, startIndex int, totalCategories int, systemID int, groupFilter, tagFilter string, encryptedFilter *bool) ([]RadioReferenceTalkgroup, int, int, int, error) {
	var chunkTalkgroups []RadioReferenceTalkgroup
	var successfulCategories, failedCategories, emptyCategories int

	for _, category := range categories {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return chunkTalkgroups, successfulCategories, failedCategories, emptyCategories, ctx.Err()
		default:
			// Continue processing
		}

		// Process category with retry logic
		var talkgroups []RadioReferenceTalkgroup
		var err error
		for retry := 0; retry < 3; retry++ {
			talkgroups, err = rr.GetTalkgroupsByCategory(systemID, category.ID, category.Name)
			if err == nil {
				break
			}
			if retry < 2 {
				time.Sleep(1 * time.Second) // Reduced retry delay
			}
		}

		if err != nil {
			failedCategories++
			continue
		}

		if len(talkgroups) == 0 {
			emptyCategories++
		} else {
			successfulCategories++
		}

		// Apply filters
		filteredTalkgroups := admin.filterTalkgroups(talkgroups, groupFilter, tagFilter, encryptedFilter)
		chunkTalkgroups = append(chunkTalkgroups, filteredTalkgroups...)

		// Reduced delay to speed up processing significantly
		time.Sleep(10 * time.Millisecond) // Reduced from 25ms to 10ms
	}

	return chunkTalkgroups, successfulCategories, failedCategories, emptyCategories, nil
}

// talkgroupMatchesFilter checks if a talkgroup matches the given filters
func (admin *Admin) talkgroupMatchesFilter(tg RadioReferenceTalkgroup, groupFilter, tagFilter string, encryptedFilter *bool) bool {
	// Group filter
	if groupFilter != "" && !strings.Contains(strings.ToLower(tg.Group), strings.ToLower(groupFilter)) {
		return false
	}
	// Tag filter
	if tagFilter != "" && !strings.Contains(strings.ToLower(tg.Tag), strings.ToLower(tagFilter)) {
		return false
	}
	// Encrypted filter
	if encryptedFilter != nil {
		if *encryptedFilter && tg.Enc == 0 {
			return false // Want encrypted, but this is not encrypted
		}
		if !*encryptedFilter && tg.Enc != 0 {
			return false // Don't want encrypted, but this is encrypted
		}
	}
	return true
}

// filterTalkgroups filters a slice of talkgroups based on the given filters
func (admin *Admin) filterTalkgroups(talkgroups []RadioReferenceTalkgroup, groupFilter, tagFilter string, encryptedFilter *bool) []RadioReferenceTalkgroup {
	var filtered []RadioReferenceTalkgroup
	for _, tg := range talkgroups {
		if admin.talkgroupMatchesFilter(tg, groupFilter, tagFilter, encryptedFilter) {
			filtered = append(filtered, tg)
		}
	}
	return filtered
}

// New endpoints to support dropdown-based selection like SDRTrunk
func (admin *Admin) RadioReferenceCountriesHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !admin.Controller.Options.RadioReferenceEnabled {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Radio Reference is not enabled"})
		return
	}
	rr := NewRadioReferenceService(admin.Controller.Options.RadioReferenceUsername, admin.Controller.Options.RadioReferencePassword, admin.Controller.Options.RadioReferenceAPIKey)
	items, err := rr.GetCountries()
	if err != nil {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "items": items})
}

func (admin *Admin) RadioReferenceStatesHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !admin.Controller.Options.RadioReferenceEnabled {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Radio Reference is not enabled"})
		return
	}
	q := r.URL.Query().Get("countryId")
	id, err := strconv.Atoi(q)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid countryId parameter"})
		return
	}

	// Check if credentials are empty or default
	if admin.Controller.Options.RadioReferenceUsername == "" {
	}
	if admin.Controller.Options.RadioReferencePassword == "" {
	}

	rr := NewRadioReferenceService(admin.Controller.Options.RadioReferenceUsername, admin.Controller.Options.RadioReferencePassword, admin.Controller.Options.RadioReferenceAPIKey)
	items, err := rr.GetStates(id)
	if err != nil {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "items": items})
}

func (admin *Admin) RadioReferenceCountiesHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !admin.Controller.Options.RadioReferenceEnabled {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Radio Reference is not enabled"})
		return
	}
	q := r.URL.Query().Get("stateId")
	id, _ := strconv.Atoi(q)
	rr := NewRadioReferenceService(admin.Controller.Options.RadioReferenceUsername, admin.Controller.Options.RadioReferencePassword, admin.Controller.Options.RadioReferenceAPIKey)
	items, err := rr.GetCounties(id)
	if err != nil {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "items": items})
}

func (admin *Admin) RadioReferenceSystemsHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !admin.Controller.Options.RadioReferenceEnabled {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Radio Reference is not enabled"})
		return
	}
	q := r.URL.Query().Get("countyId")
	id, _ := strconv.Atoi(q)
	rr := NewRadioReferenceService(admin.Controller.Options.RadioReferenceUsername, admin.Controller.Options.RadioReferencePassword, admin.Controller.Options.RadioReferenceAPIKey)
	items, err := rr.GetSystemsByCounty(id)
	if err != nil {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "items": items})
}

func (admin *Admin) RadioReferenceTalkgroupsHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !admin.Controller.Options.RadioReferenceEnabled {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Radio Reference is not enabled"})
		return
	}

	systemID := r.URL.Query().Get("systemId")
	if systemID == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "systemId parameter is required"})
		return
	}

	id, err := strconv.Atoi(systemID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid systemId parameter"})
		return
	}

	rr := NewRadioReferenceService(admin.Controller.Options.RadioReferenceUsername, admin.Controller.Options.RadioReferencePassword, admin.Controller.Options.RadioReferenceAPIKey)

	// Try the main method first
	talkgroups, err := rr.GetTalkgroups(id)
	if err != nil {

		// Try alternative method
		talkgroups, err = rr.GetTalkgroupsAlternative(id)
		if err != nil {
			w.WriteHeader(http.StatusExpectationFailed)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
	}

	json.NewEncoder(w).Encode(map[string]any{"success": true, "talkgroups": talkgroups})
}

// New endpoint to get talkgroup categories for a system
func (admin *Admin) RadioReferenceTalkgroupCategoriesHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !admin.Controller.Options.RadioReferenceEnabled {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Radio Reference is not enabled"})
		return
	}

	systemIDStr := r.URL.Query().Get("systemId")
	if systemIDStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "systemId parameter is required"})
		return
	}

	systemID, err := strconv.ParseInt(systemIDStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid systemId parameter"})
		return
	}

	rr := NewRadioReferenceService(
		admin.Controller.Options.RadioReferenceUsername,
		admin.Controller.Options.RadioReferencePassword,
		admin.Controller.Options.RadioReferenceAPIKey,
	)

	categories, err := rr.GetTalkgroupCategories(int(systemID))
	if err != nil {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	result := map[string]any{
		"success":    true,
		"systemId":   systemID,
		"categories": categories,
	}

	json.NewEncoder(w).Encode(result)
}

// Modified endpoint to get talkgroups by category instead of all talkgroups
func (admin *Admin) RadioReferenceTalkgroupsByCategoryHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !admin.Controller.Options.RadioReferenceEnabled {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Radio Reference is not enabled"})
		return
	}

	systemIDStr := r.URL.Query().Get("systemId")
	categoryIDStr := r.URL.Query().Get("categoryId")
	categoryName := r.URL.Query().Get("categoryName")

	if systemIDStr == "" || categoryIDStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "systemId and categoryId parameters are required"})
		return
	}

	systemID, err := strconv.ParseInt(systemIDStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid systemId parameter"})
		return
	}

	categoryID, err := strconv.ParseInt(categoryIDStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid categoryId parameter"})
		return
	}

	rr := NewRadioReferenceService(
		admin.Controller.Options.RadioReferenceUsername,
		admin.Controller.Options.RadioReferencePassword,
		admin.Controller.Options.RadioReferenceAPIKey,
	)

	talkgroups, err := rr.GetTalkgroupsByCategory(int(systemID), int(categoryID), categoryName)
	if err != nil {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	result := map[string]any{
		"success":      true,
		"systemId":     systemID,
		"categoryId":   categoryID,
		"categoryName": categoryName,
		"data":         talkgroups,
	}

	json.NewEncoder(w).Encode(result)
}

// Radio Reference Sites Handler
func (admin *Admin) RadioReferenceSitesHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if !admin.Controller.Options.RadioReferenceEnabled {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Radio Reference is not enabled"})
		return
	}

	systemIDStr := r.URL.Query().Get("systemId")
	if systemIDStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "systemId parameter is required"})
		return
	}

	systemID, err := strconv.ParseInt(systemIDStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid systemId parameter"})
		return
	}

	rr := NewRadioReferenceService(
		admin.Controller.Options.RadioReferenceUsername,
		admin.Controller.Options.RadioReferencePassword,
		admin.Controller.Options.RadioReferenceAPIKey,
	)

	sites, err := rr.GetSites(int(systemID))
	if err != nil {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	result := map[string]any{
		"success":  true,
		"systemId": systemID,
		"data":     sites,
	}

	json.NewEncoder(w).Encode(result)
}

// Configuration reload handler
func (admin *Admin) ConfigReloadHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Check what's in the database before reload
	query := `SELECT "key", "value" FROM "options" WHERE "key" IN ('radioReferenceUsername', 'radioReferencePassword', 'radioReferenceEnabled')`
	rows, err := admin.Controller.Database.Sql.Query(query)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var key, value sql.NullString
			if err := rows.Scan(&key, &value); err == nil {
				if key.Valid && value.Valid {
				}
			}
		}
	}

	// Reload options from database (this will include the updated Radio Reference settings)
	if err := admin.Controller.Options.Read(admin.Controller.Database); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "Configuration reloaded successfully"})
}

// EmailLogoUploadHandler handles logo file upload for emails
func (admin *Admin) EmailLogoUploadHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form (max 10MB)
	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		log.Printf("Failed to parse multipart form: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to parse form"})
		return
	}

	file, handler, err := r.FormFile("logo")
	if err != nil {
		log.Printf("Failed to get file from form: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "No file provided"})
		return
	}
	defer file.Close()

	// Validate file type
	contentType := handler.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "File must be an image"})
		return
	}

	// Determine file extension
	ext := filepath.Ext(handler.Filename)
	if ext == "" {
		// Try to determine from content type
		switch contentType {
		case "image/png":
			ext = ".png"
		case "image/jpeg", "image/jpg":
			ext = ".jpg"
		case "image/svg+xml":
			ext = ".svg"
		default:
			ext = ".png"
		}
	}

	// Generate filename
	filename := "email-logo" + ext
	logoPath := filepath.Join(admin.Controller.Config.BaseDir, filename)

	// Delete old logo if exists
	if admin.Controller.Options.EmailLogoFilename != "" {
		oldPath := filepath.Join(admin.Controller.Config.BaseDir, admin.Controller.Options.EmailLogoFilename)
		os.Remove(oldPath)
	}

	// Create file
	dst, err := os.Create(logoPath)
	if err != nil {
		log.Printf("Failed to create logo file: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save file"})
		return
	}
	defer dst.Close()

	// Copy file content
	_, err = io.Copy(dst, file)
	if err != nil {
		log.Printf("Failed to copy file: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save file"})
		return
	}

	// Update options with filename
	admin.Controller.Options.EmailLogoFilename = filename
	err = admin.Controller.Options.Write(admin.Controller.Database)
	if err != nil {
		log.Printf("Failed to save logo filename to database: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save configuration"})
		return
	}

	// Reload options
	err = admin.Controller.Options.Read(admin.Controller.Database)
	if err != nil {
		log.Printf("Failed to reload options: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"filename": filename,
		"message":  "Logo uploaded successfully",
	})
}

// EmailTestHandler sends a test email
func (admin *Admin) EmailTestHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		ToEmail string `json:"toEmail"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}

	// Send test email
	err := admin.Controller.EmailService.SendTestEmail(request.ToEmail)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Test email sent successfully",
	})
}

// EmailLogoDeleteHandler deletes the email logo
func (admin *Admin) EmailLogoDeleteHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Delete logo file if exists
	if admin.Controller.Options.EmailLogoFilename != "" {
		logoPath := filepath.Join(admin.Controller.Config.BaseDir, admin.Controller.Options.EmailLogoFilename)
		os.Remove(logoPath)
	}

	// Clear filename from options
	admin.Controller.Options.EmailLogoFilename = ""
	err := admin.Controller.Options.Write(admin.Controller.Database)
	if err != nil {
		log.Printf("Failed to save logo filename to database: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save configuration"})
		return
	}

	// Reload options
	err = admin.Controller.Options.Read(admin.Controller.Database)
	if err != nil {
		log.Printf("Failed to reload options: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Logo deleted successfully",
	})
}

// FaviconUploadHandler handles favicon file upload
func (admin *Admin) FaviconUploadHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form (max 10MB)
	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		log.Printf("Failed to parse multipart form: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to parse form"})
		return
	}

	file, handler, err := r.FormFile("favicon")
	if err != nil {
		log.Printf("Failed to get file from form: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "No file provided"})
		return
	}
	defer file.Close()

	// Validate file type
	contentType := handler.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "File must be an image"})
		return
	}

	// Determine file extension
	ext := filepath.Ext(handler.Filename)
	if ext == "" {
		// Try to determine from content type
		switch contentType {
		case "image/png":
			ext = ".png"
		case "image/jpeg", "image/jpg":
			ext = ".jpg"
		case "image/svg+xml":
			ext = ".svg"
		case "image/x-icon", "image/vnd.microsoft.icon":
			ext = ".ico"
		default:
			ext = ".png"
		}
	}

	// Generate filename
	filename := "favicon" + ext
	faviconPath := filepath.Join(admin.Controller.Config.BaseDir, filename)

	// Delete old favicon if exists
	if admin.Controller.Options.FaviconFilename != "" {
		oldPath := filepath.Join(admin.Controller.Config.BaseDir, admin.Controller.Options.FaviconFilename)
		os.Remove(oldPath)
	}

	// Create file
	dst, err := os.Create(faviconPath)
	if err != nil {
		log.Printf("Failed to create favicon file: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save file"})
		return
	}
	defer dst.Close()

	// Copy file content
	_, err = io.Copy(dst, file)
	if err != nil {
		log.Printf("Failed to copy file: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save file"})
		return
	}

	// Update options with filename
	admin.Controller.Options.FaviconFilename = filename
	err = admin.Controller.Options.Write(admin.Controller.Database)
	if err != nil {
		log.Printf("Failed to save favicon filename to database: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save configuration"})
		return
	}

	// Reload options
	err = admin.Controller.Options.Read(admin.Controller.Database)
	if err != nil {
		log.Printf("Failed to reload options: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"filename": filename,
		"message":  "Favicon uploaded successfully",
	})
}

// FaviconDeleteHandler deletes the favicon
func (admin *Admin) FaviconDeleteHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Delete favicon file if exists
	if admin.Controller.Options.FaviconFilename != "" {
		faviconPath := filepath.Join(admin.Controller.Config.BaseDir, admin.Controller.Options.FaviconFilename)
		os.Remove(faviconPath)
	}

	// Clear filename from options
	admin.Controller.Options.FaviconFilename = ""
	err := admin.Controller.Options.Write(admin.Controller.Database)
	if err != nil {
		log.Printf("Failed to save favicon filename to database: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to save configuration"})
		return
	}

	// Reload options
	err = admin.Controller.Options.Read(admin.Controller.Database)
	if err != nil {
		log.Printf("Failed to reload options: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Favicon deleted successfully",
	})
}

// RadioReferenceTalkgroupCountHandler gets the total count of talkgroups for pagination
func (admin *Admin) RadioReferenceTalkgroupCountHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if !admin.Controller.Options.RadioReferenceEnabled {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Radio Reference is not enabled"})
		return
	}

	systemIDStr := r.URL.Query().Get("systemId")
	if systemIDStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "systemId parameter is required"})
		return
	}

	systemID, err := strconv.ParseInt(systemIDStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid systemId parameter"})
		return
	}

	// Extract filter parameters from query string
	groupFilter := r.URL.Query().Get("groupFilter")
	tagFilter := r.URL.Query().Get("tagFilter")
	encryptedFilterStr := r.URL.Query().Get("encryptedFilter")

	var encryptedFilter *bool
	if encryptedFilterStr != "" {
		if encryptedFilterStr == "true" {
			val := true
			encryptedFilter = &val
		} else if encryptedFilterStr == "false" {
			val := false
			encryptedFilter = &val
		}
	}

	rr := NewRadioReferenceService(
		admin.Controller.Options.RadioReferenceUsername,
		admin.Controller.Options.RadioReferencePassword,
		admin.Controller.Options.RadioReferenceAPIKey,
	)

	totalCount, err := admin.getTotalTalkgroupCount(rr, int(systemID), groupFilter, tagFilter, encryptedFilter)
	if err != nil {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Get page size from query or use default
	pageSizeStr := r.URL.Query().Get("pageSize")
	pageSize := 100 // Default page size
	if pageSizeStr != "" {
		if size, err := strconv.Atoi(pageSizeStr); err == nil && size > 0 {
			pageSize = size
		}
	}

	result := map[string]any{
		"success":    true,
		"systemId":   systemID,
		"totalCount": totalCount,
		"pageSize":   pageSize,
		"totalPages": int(math.Ceil(float64(totalCount) / float64(pageSize))),
		"filters": map[string]any{
			"groupFilter":     groupFilter,
			"tagFilter":       tagFilter,
			"encryptedFilter": encryptedFilter,
		},
	}

	json.NewEncoder(w).Encode(result)
}

// RadioReferenceAllTalkgroupsHandler loads all talkgroups for a system efficiently
func (admin *Admin) RadioReferenceAllTalkgroupsHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if !admin.Controller.Options.RadioReferenceEnabled {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Radio Reference is not enabled"})
		return
	}

	systemIDStr := r.URL.Query().Get("systemId")
	if systemIDStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "systemId parameter is required"})
		return
	}

	systemID, err := strconv.ParseInt(systemIDStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid systemId parameter"})
		return
	}

	// Extract filter parameters from query string
	groupFilter := r.URL.Query().Get("groupFilter")
	tagFilter := r.URL.Query().Get("tagFilter")
	encryptedFilterStr := r.URL.Query().Get("encryptedFilter")

	var encryptedFilter *bool
	if encryptedFilterStr != "" {
		if encryptedFilterStr == "true" {
			val := true
			encryptedFilter = &val
		} else if encryptedFilterStr == "false" {
			val := false
			encryptedFilter = &val
		}
	}

	rr := NewRadioReferenceService(
		admin.Controller.Options.RadioReferenceUsername,
		admin.Controller.Options.RadioReferencePassword,
		admin.Controller.Options.RadioReferenceAPIKey,
	)

	// Load all talkgroups with streaming
	allTalkgroups, err := admin.getAllTalkgroupsStreaming(rr, int(systemID), groupFilter, tagFilter, encryptedFilter)
	if err != nil {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	result := map[string]any{
		"success":    true,
		"systemId":   systemID,
		"data":       allTalkgroups,
		"totalCount": len(allTalkgroups),
		"mode":       "all",
		"filters": map[string]any{
			"groupFilter":     groupFilter,
			"tagFilter":       tagFilter,
			"encryptedFilter": encryptedFilter,
		},
	}

	json.NewEncoder(w).Encode(result)
}

// RadioReferenceTestStreamingHandler tests the streaming function directly
func (admin *Admin) RadioReferenceTestStreamingHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if !admin.Controller.Options.RadioReferenceEnabled {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Radio Reference is not enabled"})
		return
	}

	systemIDStr := r.URL.Query().Get("systemId")
	if systemIDStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "systemId parameter is required"})
		return
	}

	systemID, err := strconv.ParseInt(systemIDStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid systemId parameter"})
		return
	}

	rr := NewRadioReferenceService(
		admin.Controller.Options.RadioReferenceUsername,
		admin.Controller.Options.RadioReferencePassword,
		admin.Controller.Options.RadioReferenceAPIKey,
	)

	// Test the streaming function with minimal filters
	allTalkgroups, err := admin.getAllTalkgroupsStreaming(rr, int(systemID), "", "", nil)
	if err != nil {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	result := map[string]any{
		"success":    true,
		"systemId":   systemID,
		"totalCount": len(allTalkgroups),
		"message":    "Streaming test completed successfully",
		"sampleData": allTalkgroups[:min(5, len(allTalkgroups))], // Show first 5 talkgroups as sample
	}

	json.NewEncoder(w).Encode(result)
}

// RadioReferenceStreamingTalkgroupsHandler loads all talkgroups using streaming with temp file
func (admin *Admin) RadioReferenceStreamingTalkgroupsHandler(w http.ResponseWriter, r *http.Request) {
	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if !admin.Controller.Options.RadioReferenceEnabled {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Radio Reference is not enabled"})
		return
	}

	systemIDStr := r.URL.Query().Get("systemId")
	if systemIDStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "systemId parameter is required"})
		return
	}

	systemID, err := strconv.ParseInt(systemIDStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid systemId parameter"})
		return
	}

	// Extract filter parameters from query string
	groupFilter := r.URL.Query().Get("groupFilter")
	tagFilter := r.URL.Query().Get("tagFilter")
	encryptedFilterStr := r.URL.Query().Get("encryptedFilter")

	var encryptedFilter *bool
	if encryptedFilterStr != "" {
		if encryptedFilterStr == "true" {
			val := true
			encryptedFilter = &val
		} else if encryptedFilterStr == "false" {
			val := false
			encryptedFilter = &val
		}
	}

	rr := NewRadioReferenceService(
		admin.Controller.Options.RadioReferenceUsername,
		admin.Controller.Options.RadioReferencePassword,
		admin.Controller.Options.RadioReferenceAPIKey,
	)

	// Use the temp file streaming approach
	err = admin.getAllTalkgroupsWithTempFile(w, rr, int(systemID), groupFilter, tagFilter, encryptedFilter)
	if err != nil {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Response is already written by the streaming function
}

// RadioReferenceProgressTalkgroupsHandler handles progress-based talkgroup loading
func (admin *Admin) RadioReferenceProgressTalkgroupsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Check if Radio Reference is enabled
	if !admin.Controller.Options.RadioReferenceEnabled {
		log.Printf("Radio Reference is not enabled")
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Radio Reference is not enabled"})
		return
	}

	var request struct {
		SystemID        int    `json:"systemID"`
		GroupFilter     string `json:"groupFilter"`
		TagFilter       string `json:"tagFilter"`
		EncryptedFilter *bool  `json:"encryptedFilter"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		log.Printf("Failed to decode request body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}

	log.Printf("RadioReferenceProgressTalkgroupsHandler: systemID=%d, groupFilter=%s, tagFilter=%s",
		request.SystemID, request.GroupFilter, request.TagFilter)

	rr := NewRadioReferenceService(
		admin.Controller.Options.RadioReferenceUsername,
		admin.Controller.Options.RadioReferencePassword,
		admin.Controller.Options.RadioReferenceAPIKey,
	)

	// Use progress-based loading
	err := admin.getAllTalkgroupsWithProgress(w, rr, request.SystemID, request.GroupFilter, request.TagFilter, request.EncryptedFilter)
	if err != nil {
		log.Printf("getAllTalkgroupsWithProgress failed: %v", err)
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
}

// UsersListHandler handles GET requests to fetch all registered users
func (admin *Admin) UsersListHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Read users from database to ensure we have the latest data
	if err := admin.Controller.Users.Read(admin.Controller.Database); err != nil {
		log.Printf("Failed to read users: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to fetch users"})
		return
	}

	// Get all users from memory
	users := admin.Controller.Users.GetAllUsers()

	// Convert users to JSON format
	var userList []map[string]interface{}
	for _, user := range users {
		// Format timestamps to readable dates
		var createdAtFormatted, lastLoginFormatted string

		// Format createdAt
		if user.CreatedAt != "" {
			if timestamp, err := strconv.ParseInt(user.CreatedAt, 10, 64); err == nil {
				createdAtFormatted = time.Unix(timestamp, 0).Format("2006-01-02 15:04:05 MST")
			} else {
				createdAtFormatted = user.CreatedAt // fallback to raw value
			}
		} else {
			createdAtFormatted = "Never"
		}

		// Format lastLogin
		if user.LastLogin == "" || user.LastLogin == "0" {
			lastLoginFormatted = "User has not logged in"
		} else {
			if timestamp, err := strconv.ParseInt(user.LastLogin, 10, 64); err == nil {
				// Check if timestamp is 0 (Unix epoch) which means never logged in
				if timestamp == 0 {
					lastLoginFormatted = "User has not logged in"
				} else {
					lastLoginFormatted = time.Unix(timestamp, 0).Format("2006-01-02 15:04:05 MST")
				}
			} else {
				lastLoginFormatted = "User has not logged in" // fallback for invalid timestamps
			}
		}

		// Get effective connection limit (group limit if user is in a group, otherwise user limit)
		effectiveConnectionLimit := user.ConnectionLimit
		if user.UserGroupId > 0 {
			group := admin.Controller.UserGroups.Get(user.UserGroupId)
			if group != nil && group.ConnectionLimit > 0 {
				effectiveConnectionLimit = group.ConnectionLimit
			}
		}

		// Get device tokens (FCM) for this user
		deviceTokens := admin.Controller.DeviceTokens.GetByUser(user.Id)
		var fcmTokens []map[string]interface{}
		for _, dt := range deviceTokens {
			// Only include tokens that have FCM tokens
			if dt.FCMToken != "" {
				fcmTokens = append(fcmTokens, map[string]interface{}{
					"id":        dt.Id,
					"fcmToken":  dt.FCMToken,
					"pushType":  dt.PushType,
					"platform":  dt.Platform,
					"sound":     dt.Sound,
					"createdAt": time.Unix(dt.CreatedAt, 0).Format("2006-01-02 15:04:05 MST"),
					"lastUsed":  time.Unix(dt.LastUsed, 0).Format("2006-01-02 15:04:05 MST"),
				})
			}
		}

		userList = append(userList, map[string]interface{}{
			"id":                       user.Id,
			"email":                    user.Email,
			"firstName":                user.FirstName,
			"lastName":                 user.LastName,
			"zipCode":                  user.ZipCode,
			"verified":                 user.Verified,
			"createdAt":                createdAtFormatted,
			"lastLogin":                lastLoginFormatted,
			"systems":                  user.Systems,
			"delay":                    user.Delay,
			"systemDelays":             user.SystemDelays,
			"talkgroupDelays":          user.TalkgroupDelays,
			"pin":                      user.Pin,
			"pinExpiresAt":             user.PinExpiresAt,
			"pinExpired":               user.PinExpired(),
			"connectionLimit":          user.ConnectionLimit,
			"effectiveConnectionLimit": effectiveConnectionLimit,
			"userGroupId":              user.UserGroupId,
			"isGroupAdmin":             user.IsGroupAdmin,
			"systemAdmin":              user.SystemAdmin,
			"stripeCustomerId":         user.StripeCustomerId,
			"stripeSubscriptionId":     user.StripeSubscriptionId,
			"subscriptionStatus":       user.SubscriptionStatus,
			"fcmTokens":                fcmTokens,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(userList)
}

// UserTestPushHandler sends a test push notification to a user
func (admin *Admin) UserTestPushHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Extract user ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 5 { // /api/admin/users/{id}/test-push
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid user ID"})
		return
	}

	userIDStr := pathParts[4]
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid user ID format"})
		return
	}

	// Get user
	user := admin.Controller.Users.GetUserById(userID)
	if user == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "User not found"})
		return
	}

	// Send test push notification
	// Use keyword alert type with a test message
	admin.Controller.sendPushNotification(
		userID,
		"keyword",
		nil,
		"TEST NOTIFICATION",
		"Push Notification Test",
		"",
		[]string{"This is a test push notification from the admin panel"},
	)

	log.Printf("Test push notification sent to user %d (%s)", userID, user.Email)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Test push notification sent successfully",
		"userId":  fmt.Sprintf("%d", userID),
		"email":   user.Email,
	})
}

// UserDeleteHandler handles DELETE requests to delete a user
func (admin *Admin) UserDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Extract user ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 4 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid user ID"})
		return
	}

	userIDStr := pathParts[len(pathParts)-1]
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid user ID format"})
		return
	}

	// Get user to check if exists
	user := admin.Controller.Users.GetUserById(userID)
	if user == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "User not found"})
		return
	}

	// Delete user directly from database
	_, err = admin.Controller.Database.Sql.Exec(`DELETE FROM "users" WHERE "userId" = $1`, userID)
	if err != nil {
		log.Printf("Failed to delete user %d from database: %v", userID, err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete user from database"})
		return
	}

	// Remove user from in-memory map
	if err := admin.Controller.Users.Remove(userID); err != nil {
		log.Printf("Failed to remove user %d from memory: %v", userID, err)
		// Don't fail the request since database deletion succeeded
	}

	// Sync config to file if enabled
	admin.Controller.SyncConfigToFile()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "User deleted successfully"})
}

// UserUpdateHandler handles PUT requests to update a user
func (admin *Admin) UserUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Extract user ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 4 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid user ID"})
		return
	}

	userIDStr := pathParts[len(pathParts)-1]
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid user ID format"})
		return
	}

	// Get user to check if exists
	user := admin.Controller.Users.GetUserById(userID)
	if user == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "User not found"})
		return
	}

	// Parse request body
	var request struct {
		Email                string  `json:"email"`
		FirstName            string  `json:"firstName"`
		LastName             string  `json:"lastName"`
		ZipCode              string  `json:"zipCode"`
		Verified             bool    `json:"verified"`
		Systems              string  `json:"systems"`
		Delay                int     `json:"delay"`
		Pin                  *string `json:"pin"`
		PinExpiresAt         *uint64 `json:"pinExpiresAt"`
		ConnectionLimit      *uint   `json:"connectionLimit"`
		SystemDelays         *string `json:"systemDelays"`
		TalkgroupDelays      *string `json:"talkgroupDelays"`
		RegeneratePin        bool    `json:"regeneratePin"`
		UserGroupId          *uint64 `json:"userGroupId"`
		IsGroupAdmin         *bool   `json:"isGroupAdmin"`
		SystemAdmin          *bool   `json:"systemAdmin"`
		StripeCustomerId     string  `json:"stripeCustomerId"`
		StripeSubscriptionId string  `json:"stripeSubscriptionId"`
		SubscriptionStatus   string  `json:"subscriptionStatus"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON"})
		return
	}

	// Validate input
	if request.Email == "" || request.FirstName == "" || request.LastName == "" || request.ZipCode == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "All fields are required"})
		return
	}

	// Update user fields
	user.Email = request.Email
	user.FirstName = request.FirstName
	user.LastName = request.LastName
	user.ZipCode = request.ZipCode
	user.Verified = request.Verified
	user.Systems = strings.TrimSpace(request.Systems)
	if request.Delay < 0 {
		user.Delay = 0
	} else {
		user.Delay = request.Delay
	}

	if request.SystemDelays != nil {
		user.SystemDelays = strings.TrimSpace(*request.SystemDelays)
	}
	if request.TalkgroupDelays != nil {
		user.TalkgroupDelays = strings.TrimSpace(*request.TalkgroupDelays)
	}
	if request.PinExpiresAt != nil {
		user.PinExpiresAt = *request.PinExpiresAt
	}
	if request.ConnectionLimit != nil {
		user.ConnectionLimit = *request.ConnectionLimit
	}

	// Update Stripe billing information
	user.StripeCustomerId = strings.TrimSpace(request.StripeCustomerId)
	user.StripeSubscriptionId = strings.TrimSpace(request.StripeSubscriptionId)
	user.SubscriptionStatus = strings.TrimSpace(request.SubscriptionStatus)

	// Determine the effective user group ID (use new value if provided, otherwise current)
	effectiveUserGroupId := user.UserGroupId
	oldGroupId := user.UserGroupId
	if request.UserGroupId != nil {
		// Validate that the group exists
		group := admin.Controller.UserGroups.Get(*request.UserGroupId)
		if group == nil && *request.UserGroupId != 0 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid user group ID"})
			return
		}
		user.UserGroupId = *request.UserGroupId
		effectiveUserGroupId = *request.UserGroupId

		// Remove group admin status if user is being moved to a different group (security: admin status should not persist across groups)
		if user.IsGroupAdmin && oldGroupId != *request.UserGroupId {
			user.IsGroupAdmin = false
		}
	}

	if request.IsGroupAdmin != nil {
		// Only allow setting group admin if user is in a group (check effective group ID)
		if *request.IsGroupAdmin && effectiveUserGroupId == 0 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "User must be assigned to a group to be a group admin"})
			return
		}
		user.IsGroupAdmin = *request.IsGroupAdmin
	}

	if request.SystemAdmin != nil {
		user.SystemAdmin = *request.SystemAdmin
	}

	if request.RegeneratePin {
		newPin, err := admin.Controller.Users.GenerateUniquePin(user.Id)
		if err != nil {
			log.Printf("Failed to regenerate pin for user %d: %v", userID, err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to regenerate pin"})
			return
		}
		user.Pin = newPin
		user.PinExpiresAt = 0
	} else if request.Pin != nil {
		pinValue := strings.TrimSpace(*request.Pin)
		if pinValue == "" {
			newPin, err := admin.Controller.Users.GenerateUniquePin(user.Id)
			if err != nil {
				log.Printf("Failed to regenerate pin for user %d: %v", userID, err)
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "Failed to regenerate pin"})
				return
			}
			user.Pin = newPin
			user.PinExpiresAt = 0
		} else {
			if !admin.Controller.Users.IsPinAvailable(pinValue, user.Id) {
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]string{"error": "PIN already in use"})
				return
			}
			user.Pin = pinValue
		}
	}

	// Update user in memory and database
	admin.Controller.Users.Update(user)
	if err := admin.Controller.Users.Write(admin.Controller.Database); err != nil {
		log.Printf("Failed to update user %d: %v", userID, err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to update user"})
		return
	}

	// Sync config to file if enabled
	admin.Controller.SyncConfigToFile()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "User updated successfully"})
}

// UserCreateHandler handles POST requests to create a new user
func (admin *Admin) UserCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Parse request body
	var request struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		FirstName   string `json:"firstName"`
		LastName    string `json:"lastName"`
		ZipCode     string `json:"zipCode"`
		UserGroupId uint64 `json:"userGroupId"`
		Verified    *bool  `json:"verified"` // Optional, defaults to true for admin-created users
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}

	// Validate required fields
	if request.Email == "" || request.Password == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Email and password are required"})
		return
	}

	// Normalize email to lowercase
	request.Email = NormalizeEmail(request.Email)

	// Check if email is already registered (case-insensitive)
	if admin.Controller.Users.GetUserByEmail(request.Email) != nil {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "Email is already registered"})
		return
	}

	// Validate password length
	if len(request.Password) < 6 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Password must be at least 6 characters long"})
		return
	}

	// Generate a unique PIN
	pin, err := admin.Controller.Users.GenerateUniquePin(0)
	if err != nil {
		log.Printf("Failed to generate PIN: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to generate PIN"})
		return
	}

	// Determine if user should be verified (default to true for admin-created users)
	verified := true
	if request.Verified != nil {
		verified = *request.Verified
	}

	// Create new user
	user := NewUser(request.Email, request.Password)

	// Hash the password
	if err := user.HashPassword(request.Password); err != nil {
		log.Printf("Failed to hash password: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create user"})
		return
	}
	user.FirstName = request.FirstName
	user.LastName = request.LastName
	user.ZipCode = request.ZipCode
	user.Pin = pin
	user.PinExpiresAt = 0 // No expiration for non-billing groups by default
	user.Verified = verified
	user.VerificationToken = "" // No token needed since admin creates it
	user.CreatedAt = fmt.Sprintf("%d", time.Now().Unix())
	user.LastLogin = "0"
	user.UserGroupId = request.UserGroupId
	user.SystemAdmin = false
	user.IsGroupAdmin = false
	user.ConnectionLimit = 0
	user.Delay = 0
	user.Systems = "*" // Default to all systems
	user.SubscriptionStatus = ""

	// If assigned to a user group, handle group-specific setup
	if request.UserGroupId > 0 {
		group := admin.Controller.UserGroups.Get(request.UserGroupId)
		if group == nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid user group ID"})
			return
		}

		// Check max users limit for the group
		if group.MaxUsers > 0 {
			currentUserCount := admin.Controller.UserGroups.GetUserCount(group.Id, admin.Controller.Users)
			if currentUserCount >= group.MaxUsers {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Group has reached maximum user limit of %d", group.MaxUsers)})
				return
			}
		}

		// Handle billing setup for billing-enabled groups
		if group.BillingEnabled {
			if group.BillingMode == "group_admin" {
				// For admin-managed billing, sync from admin if available
				syncedFromAdmin := false
				allUsers := admin.Controller.Users.GetAllUsers()
				for _, groupAdmin := range allUsers {
					if groupAdmin.UserGroupId == group.Id && groupAdmin.IsGroupAdmin && groupAdmin.SubscriptionStatus == "active" {
						user.SubscriptionStatus = groupAdmin.SubscriptionStatus
						user.PinExpiresAt = groupAdmin.PinExpiresAt
						syncedFromAdmin = true
						log.Printf("Synced subscription status from admin %s to new user %s", groupAdmin.Email, user.Email)
						break
					}
				}

				if !syncedFromAdmin {
					// No active admin found - expire PIN immediately
					user.SubscriptionStatus = "incomplete"
					user.PinExpiresAt = uint64(time.Now().Unix() - 86400)
					log.Printf("No active admin found - set PIN to expire for new user %s in admin-managed billing group", user.Email)
				}
			} else if group.BillingMode == "all_users" {
				// For all_users mode, they need to subscribe - expire PIN immediately
				user.SubscriptionStatus = "incomplete"
				user.PinExpiresAt = uint64(time.Now().Unix() - 86400)
				log.Printf("Set PIN to expire for new user %s in all_users billing group - must subscribe", user.Email)
			}
		}
	}

	// Add user to database
	if err := admin.Controller.Users.SaveNewUser(user, admin.Controller.Database); err != nil {
		log.Printf("Failed to create user: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create user"})
		return
	}

	// Sync config to file if enabled
	admin.Controller.SyncConfigToFile()

	log.Printf("Admin created new user: %s (ID: %d, Group: %d)", user.Email, user.Id, user.UserGroupId)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "User created successfully",
		"userId":  user.Id,
		"pin":     user.Pin,
	})
}

// UserResetPasswordHandler handles POST requests to reset a user's password (admin only, no current password required)
func (admin *Admin) UserResetPasswordHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Extract user ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 5 { // /api/admin/users/{id}/reset-password
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid user ID"})
		return
	}

	userIDStr := pathParts[len(pathParts)-2] // ID is second to last
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid user ID format"})
		return
	}

	// Get user to check if exists
	user := admin.Controller.Users.GetUserById(userID)
	if user == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "User not found"})
		return
	}

	// Parse request body
	var request struct {
		NewPassword string `json:"newPassword"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}

	// Validate new password
	if len(request.NewPassword) < 6 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Password must be at least 6 characters long"})
		return
	}

	// Hash the new password
	if err := user.HashPassword(request.NewPassword); err != nil {
		log.Printf("Failed to hash password: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to reset password"})
		return
	}

	// Update user in memory and database
	admin.Controller.Users.Update(user)
	if err := admin.Controller.Users.Write(admin.Controller.Database); err != nil {
		log.Printf("Failed to update user %d password: %v", userID, err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to reset password"})
		return
	}

	// Sync config to file if enabled
	admin.Controller.SyncConfigToFile()

	log.Printf("Admin reset password for user: %s (ID: %d)", user.Email, user.Id)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Password reset successfully"})
}

// HallucinationSuggestionsHandler returns pending hallucination suggestions
func (admin *Admin) HallucinationSuggestionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	suggestions, err := admin.Controller.HallucinationDetector.GetPendingSuggestions()
	if err != nil {
		log.Printf("Failed to get hallucination suggestions: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to get suggestions"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(suggestions)
}

// HallucinationApproveHandler approves a suggested hallucination
func (admin *Admin) HallucinationApproveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Id uint64 `json:"id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request"})
		return
	}

	if err := admin.Controller.HallucinationDetector.ApproveHallucination(req.Id); err != nil {
		log.Printf("Failed to approve hallucination %d: %v", req.Id, err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Sync config to file if enabled (since we updated hallucination patterns)
	admin.Controller.SyncConfigToFile()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Hallucination pattern approved and added to filter"})
}

// HallucinationRejectHandler rejects a suggested hallucination
func (admin *Admin) HallucinationRejectHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Id uint64 `json:"id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request"})
		return
	}

	if err := admin.Controller.HallucinationDetector.RejectHallucination(req.Id); err != nil {
		log.Printf("Failed to reject hallucination %d: %v", req.Id, err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to reject suggestion"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Suggestion rejected"})
}

// DeviceTokenDeleteHandler handles DELETE requests to delete a user's device token
func (admin *Admin) DeviceTokenDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Extract user ID and token ID from URL path
	// Expected path: /api/admin/users/{userId}/device-tokens/{tokenId}
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 6 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid URL format"})
		return
	}

	userIDStr := pathParts[3]
	tokenIDStr := pathParts[5]

	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid user ID format"})
		return
	}

	tokenID, err := strconv.ParseUint(tokenIDStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid token ID format"})
		return
	}

	// Verify the user exists
	user := admin.Controller.Users.GetUserById(userID)
	if user == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "User not found"})
		return
	}

	// Delete the device token
	if err := admin.Controller.DeviceTokens.Delete(tokenID, admin.Controller.Database); err != nil {
		log.Printf("Failed to delete device token %d for user %d: %v", tokenID, userID, err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete device token"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Device token deleted successfully"})
}

// UpdateCheckHandler handles GET /api/admin/update/check
// Returns the current and latest version along with whether an update is available.
func (admin *Admin) UpdateCheckHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if admin.Controller.Updater == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "updater not initialised"})
		return
	}

	info, err := admin.Controller.Updater.CheckForUpdate()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// UpdateApplyHandler handles POST /api/admin/update/apply
// Downloads and applies the latest release then triggers a graceful restart.
// The HTTP response is sent BEFORE the restart begins so the client receives it.
func (admin *Admin) UpdateApplyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	t := admin.GetAuthorization(r)
	if !admin.ValidateToken(t) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if admin.Controller.Updater == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "updater not initialised"})
		return
	}

	info, err := admin.Controller.Updater.CheckForUpdate()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if !info.UpdateAvailable {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Already up to date",
			"version": info.CurrentVersion,
		})
		return
	}

	// Send the response first — the server will restart shortly after.
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": fmt.Sprintf("Applying update to %s — server will restart momentarily", info.LatestVersion),
		"from":    info.CurrentVersion,
		"to":      info.LatestVersion,
	})

	// Flush so the client receives the response before we restart.
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Apply the update in a goroutine so the handler returns cleanly.
	go func() {
		if err := admin.Controller.Updater.ApplyUpdate(info.DownloadURL); err != nil {
			log.Printf("Auto-update apply failed: %v", err)
		}
	}()
}
