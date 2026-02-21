// Copyright (C) 2024 Thinline Dynamic Solutions
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
	"log"
	"net/http"
	"time"
)

// CentralUserGrantRequest represents a request to grant user access from central system
type CentralUserGrantRequest struct {
	Email           string      `json:"email"`
	FirstName       string      `json:"firstName"`
	LastName        string      `json:"lastName"`
	PIN             string      `json:"pin"`
	Systems         interface{} `json:"systems"`         // can be "*" or array of system IDs
	Talkgroups      interface{} `json:"talkgroups"`       // can be "*" or array of talkgroup IDs
	GroupID         *uint64     `json:"group_id"`        // optional user group ID
	ConnectionLimit uint        `json:"connectionLimit"` // 0 = unlimited
}

// CentralUserRevokeRequest represents a request to revoke user access from central system
type CentralUserRevokeRequest struct {
	Email string `json:"email"`
	PIN   string `json:"pin"`
}

// CentralWebhookUserGrantHandler handles user access grants from central management system
func (api *Api) CentralWebhookUserGrantHandler(w http.ResponseWriter, r *http.Request) {
	// Verify central management is enabled
	if !api.Controller.Options.CentralManagementEnabled {
		api.exitWithError(w, http.StatusForbidden, "Central management not enabled")
		return
	}

	// Verify API key
	apiKey := r.Header.Get("X-API-Key")
	if apiKey != api.Controller.Options.CentralManagementAPIKey {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid API key")
		return
	}

	// Parse request
	var req CentralUserGrantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate required fields
	if req.Email == "" || req.PIN == "" {
		api.exitWithError(w, http.StatusBadRequest, "Email and PIN are required")
		return
	}

	// Check if user already exists
	existingUser := api.Controller.Users.GetUserByEmail(req.Email)
	if existingUser != nil {
		// Update existing user
		existingUser.Pin = req.PIN
		existingUser.PinExpiresAt = 0 // No expiration for centrally managed users
		existingUser.FirstName = req.FirstName
		existingUser.LastName = req.LastName
		existingUser.Verified = true // Central users are pre-verified
		existingUser.ConnectionLimit = req.ConnectionLimit

		// Update systems access
		if req.Systems == "*" {
			existingUser.Systems = "*"
		} else if systemIDs, ok := req.Systems.([]interface{}); ok {
			systemsJSON, _ := json.Marshal(systemIDs)
			existingUser.Systems = string(systemsJSON)
		}

		// Update talkgroups access
		if req.Talkgroups != nil {
			if req.Talkgroups == "*" {
				existingUser.Talkgroups = "*"
			} else if talkgroupIDs, ok := req.Talkgroups.([]interface{}); ok {
				talkgroupsJSON, _ := json.Marshal(talkgroupIDs)
				existingUser.Talkgroups = string(talkgroupsJSON)
			}
		}

		// Update user group
		if req.GroupID != nil {
			existingUser.UserGroupId = *req.GroupID
		}

		// Update in-memory map first.
		api.Controller.Users.Update(existingUser)

		// Write directly to the DB for this specific user — targeted and reliable.
		_, dbErr := api.Controller.Database.Sql.Exec(
			`UPDATE "users" SET "pin"=$1, "pinExpiresAt"=$2, "connectionLimit"=$3, "firstName"=$4, "lastName"=$5, "systems"=$6, "talkgroups"=$7, "userGroupId"=$8, "verified"=$9 WHERE "userId"=$10`,
			existingUser.Pin,
			int64(existingUser.PinExpiresAt),
			int64(existingUser.ConnectionLimit),
			existingUser.FirstName,
			existingUser.LastName,
			existingUser.Systems,
			existingUser.Talkgroups,
			existingUser.UserGroupId,
			existingUser.Verified,
			existingUser.Id,
		)
		if dbErr != nil {
			log.Printf("Central Management: WARNING - failed to persist updated user %s to DB: %v", req.Email, dbErr)
		}

		log.Printf("Central Management: Updated user %s (PIN: %s, ConnectionLimit: %d)", req.Email, req.PIN, req.ConnectionLimit)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "updated",
			"user_id": existingUser.Id,
			"message": "User access updated successfully",
		})
		return
	}

	// Create new user
	user := NewUser(req.Email, "") // No password for centrally managed users
	user.FirstName = req.FirstName
	user.LastName = req.LastName
	user.Pin = req.PIN
	user.PinExpiresAt = 0 // No expiration
	user.Verified = true
	user.ConnectionLimit = req.ConnectionLimit
	user.CreatedAt = time.Now().Format(time.RFC3339)

	// Set systems access
	if req.Systems == "*" {
		user.Systems = "*"
	} else if systemIDs, ok := req.Systems.([]interface{}); ok {
		systemsJSON, _ := json.Marshal(systemIDs)
		user.Systems = string(systemsJSON)
	} else {
		user.Systems = "*" // Default to all systems
	}

	// Set talkgroups access
	if req.Talkgroups != nil {
		if req.Talkgroups == "*" {
			user.Talkgroups = "*"
		} else if talkgroupIDs, ok := req.Talkgroups.([]interface{}); ok {
			talkgroupsJSON, _ := json.Marshal(talkgroupIDs)
			user.Talkgroups = string(talkgroupsJSON)
		} else {
			user.Talkgroups = "*" // Default to all talkgroups
		}
	} else {
		user.Talkgroups = "*" // Default to all talkgroups
	}

	// Set user group
	if req.GroupID != nil {
		user.UserGroupId = *req.GroupID
	}

	// Add user to database
	if err := api.Controller.Users.SaveNewUser(user, api.Controller.Database); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to save user")
		return
	}

	log.Printf("Central Management: Created user %s (PIN: %s)", req.Email, req.PIN)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "created",
		"user_id": user.Id,
		"message": "User access granted successfully",
	})
}

// CentralWebhookUserRevokeHandler handles user access revocations from central management system
func (api *Api) CentralWebhookUserRevokeHandler(w http.ResponseWriter, r *http.Request) {
	// Verify central management is enabled
	if !api.Controller.Options.CentralManagementEnabled {
		api.exitWithError(w, http.StatusForbidden, "Central management not enabled")
		return
	}

	// Verify API key
	apiKey := r.Header.Get("X-API-Key")
	if apiKey != api.Controller.Options.CentralManagementAPIKey {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid API key")
		return
	}

	// Parse request
	var req CentralUserRevokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Find user by email or PIN
	var user *User
	if req.Email != "" {
		user = api.Controller.Users.GetUserByEmail(req.Email)
	} else if req.PIN != "" {
		user = api.Controller.Users.GetUserByPin(req.PIN)
	}

	if user == nil {
		api.exitWithError(w, http.StatusNotFound, "User not found")
		return
	}

	// Expire the PIN to revoke access
	user.PinExpiresAt = uint64(time.Now().Unix())
	api.Controller.Users.Update(user)
	api.Controller.Users.Write(api.Controller.Database)

	// Disconnect any active connections for this user
	api.Controller.Clients.mutex.Lock()
	for client := range api.Controller.Clients.Map {
		if client.User != nil && client.User.Id == user.Id {
			// Send disconnect message
			msg := &Message{Command: MessageCommandError, Payload: "Access revoked by central management"}
			select {
			case client.Send <- msg:
			default:
			}
			// Disconnect the client
			api.Controller.Unregister <- client
		}
	}
	api.Controller.Clients.mutex.Unlock()

	log.Printf("Central Management: Revoked access for user %s", req.Email)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "revoked",
		"user_id": user.Id,
		"message": "User access revoked successfully",
	})
}

// CentralWebhookTestConnectionHandler tests the connection to central management (INCOMING test from central system)
func (api *Api) CentralWebhookTestConnectionHandler(w http.ResponseWriter, r *http.Request) {
	// Verify API key
	apiKey := r.Header.Get("X-API-Key")
	expectedKey := r.URL.Query().Get("api_key")

	if apiKey != expectedKey && expectedKey != "" {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid API key")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "Connection test successful",
		"server":  "Thinline Radio Server",
		"version": Version,
	})
}

// CentralBatchUpdateRequest holds a list of connection-limit updates from central management.
type CentralBatchUpdateRequest struct {
	Updates []CentralUserUpdateEntry `json:"updates"`
}

// CentralUserUpdateEntry is a single entry in a batch update.
type CentralUserUpdateEntry struct {
	Email           string `json:"email"`
	ConnectionLimit uint   `json:"connectionLimit"` // 0 = unlimited
}

// CentralWebhookUsersBatchUpdateHandler updates connection limits for multiple users in one call.
// Central Management uses this when a billing plan's connection limit changes, so it only needs
// to make one HTTP request per TLR server regardless of how many users are affected.
func (api *Api) CentralWebhookUsersBatchUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if !api.Controller.Options.CentralManagementEnabled {
		api.exitWithError(w, http.StatusForbidden, "Central management not enabled")
		return
	}

	apiKey := r.Header.Get("X-API-Key")
	if apiKey != api.Controller.Options.CentralManagementAPIKey {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid API key")
		return
	}

	var req CentralBatchUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	updated := 0
	for _, entry := range req.Updates {
		user := api.Controller.Users.GetUserByEmail(entry.Email)
		if user == nil {
			continue
		}
		user.ConnectionLimit = entry.ConnectionLimit
		api.Controller.Users.Update(user)

		_, dbErr := api.Controller.Database.Sql.Exec(
			`UPDATE "users" SET "connectionLimit"=$1 WHERE "userId"=$2`,
			int64(entry.ConnectionLimit),
			user.Id,
		)
		if dbErr != nil {
			log.Printf("Central Management: batch update failed for %s: %v", entry.Email, dbErr)
		} else {
			updated++
		}
	}

	log.Printf("Central Management: Batch updated connectionLimit to %d for %d/%d users",
		func() uint {
			if len(req.Updates) > 0 {
				return req.Updates[0].ConnectionLimit
			}
			return 0
		}(),
		updated, len(req.Updates))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"updated": updated,
		"total":   len(req.Updates),
	})
}

// CentralWebhookSystemsTalkgroupsGroupsHandler returns systems, talkgroups, and user groups
// for Central Management to use when editing users.
func (api *Api) CentralWebhookSystemsTalkgroupsGroupsHandler(w http.ResponseWriter, r *http.Request) {
	if !api.Controller.Options.CentralManagementEnabled {
		api.exitWithError(w, http.StatusForbidden, "Central management not enabled")
		return
	}

	apiKey := r.Header.Get("X-API-Key")
	if apiKey != api.Controller.Options.CentralManagementAPIKey {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid API key")
		return
	}

	// Get all systems
	allSystems := api.Controller.Systems.List
	systemsList := []map[string]interface{}{}

	for _, system := range allSystems {
		// Get talkgroups for this system
		talkgroups := []map[string]interface{}{}
		for _, tg := range system.Talkgroups.List {
			tagLabel := ""
			if tg.TagId > 0 {
				if tag, ok := api.Controller.Tags.GetTagById(tg.TagId); ok {
					tagLabel = tag.Label
				}
			}
			talkgroups = append(talkgroups, map[string]interface{}{
				"id":          tg.TalkgroupRef,
				"label":       tg.Label,
				"name":        tg.Name,
				"tag":         tagLabel,
			})
		}

		systemsList = append(systemsList, map[string]interface{}{
			"id":         system.SystemRef,
			"label":      system.Label,
			"talkgroups": talkgroups,
		})
	}

	// Get all user groups
	groups := api.Controller.UserGroups.GetAll()
	groupsList := []map[string]interface{}{}
	for _, group := range groups {
		groupsList = append(groupsList, map[string]interface{}{
			"id":          group.Id,
			"name":        group.Name,
			"description": group.Description,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "ok",
		"systems":    systemsList,
		"groups":     groupsList,
	})
}

// CentralWebhookUsersListHandler returns current users on this TLR server to central management.
func (api *Api) CentralWebhookUsersListHandler(w http.ResponseWriter, r *http.Request) {
	if !api.Controller.Options.CentralManagementEnabled {
		api.exitWithError(w, http.StatusForbidden, "Central management not enabled")
		return
	}

	apiKey := r.Header.Get("X-API-Key")
	if apiKey == "" || apiKey != api.Controller.Options.CentralManagementAPIKey {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid API key")
		return
	}

	now := uint64(time.Now().Unix())
	users := api.Controller.Users.GetAllUsers()

	type ServerUser struct {
		ID           uint64  `json:"id"`
		Email        string  `json:"email"`
		FirstName    string  `json:"first_name"`
		LastName     string  `json:"last_name"`
		Verified     bool    `json:"verified"`
		Systems      string  `json:"systems"`
		Talkgroups   string  `json:"talkgroups"`
		UserGroupID  *uint64 `json:"user_group_id,omitempty"`
		PIN          string  `json:"pin,omitempty"`
		PINActive    bool    `json:"pin_active"`
		PasswordHash string  `json:"password_hash,omitempty"` // SHA-256 hex — for Central Management import only
	}

	respUsers := make([]ServerUser, 0, len(users))
	for _, u := range users {
		pinActive := u.Pin != "" && (u.PinExpiresAt == 0 || u.PinExpiresAt > now)
		var groupID *uint64
		if u.UserGroupId > 0 {
			gid := u.UserGroupId
			groupID = &gid
		}
		respUsers = append(respUsers, ServerUser{
			ID:           u.Id,
			Email:        u.Email,
			FirstName:    u.FirstName,
			LastName:     u.LastName,
			Verified:     u.Verified,
			Systems:      u.Systems,
			Talkgroups:   u.Talkgroups,
			UserGroupID:  groupID,
			PIN:          u.Pin,
			PINActive:    pinActive,
			PasswordHash: u.Password, // SHA-256 hex stored on TLR
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"users":  respUsers,
		"count":  len(respUsers),
	})
}

// TestCentralConnectionHandler tests the connection FROM this server TO the central management system
func (admin *Admin) TestCentralConnectionHandler(w http.ResponseWriter, r *http.Request) {
	// Read test parameters from request body (settings may not be saved yet)
	var testReq struct {
		CentralManagementURL string `json:"central_management_url"`
		APIKey               string `json:"api_key"`
		ServerName           string `json:"server_name"`
		ServerURL            string `json:"server_url"`
	}

	if err := json.NewDecoder(r.Body).Decode(&testReq); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Invalid request body",
		})
		return
	}

	// Validate required fields
	if testReq.CentralManagementURL == "" || testReq.APIKey == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Central Management URL and API Key are required",
		})
		return
	}

	// Use the CentralManagementService to test the connection with provided credentials
	cms := admin.Controller.CentralManagement
	if cms == nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Central Management service not initialized",
		})
		return
	}

	// Test the connection using the provided URL and API key (not saved options)
	statusCode, responseBody, err := cms.TestConnection(
		testReq.CentralManagementURL,
		testReq.APIKey,
		testReq.ServerName,
		testReq.ServerURL,
	)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("Failed to test connection: %v", err),
		})
		return
	}

	if len(responseBody) == 0 {
		responseBody = []byte(`{"status":"error","error":"central management returned an empty response"}`)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_, _ = w.Write(responseBody)
}
