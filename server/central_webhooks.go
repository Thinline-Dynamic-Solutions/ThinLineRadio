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
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
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

// CentralManagementPairRequest is the payload sent by Central Management to pair this server.
type CentralManagementPairRequest struct {
	AdminPassword         string `json:"admin_password"`
	CentralManagementURL  string `json:"central_management_url"`
	APIKey                string `json:"api_key"`
	ServerName            string `json:"server_name"`
	ServerID              string `json:"server_id"`
}

// PairWithCentralManagementHandler is called by the Central Management backend to authenticate
// and push the API key + CM URL directly to this server, enabling centralized management mode
// without any manual copy-paste on the TLR server side.
//
// This endpoint is intentionally NOT localhost-restricted so that the CM backend can reach it,
// but it is protected by admin password verification (bcrypt).
func (api *Api) PairWithCentralManagementHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CentralManagementPairRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.AdminPassword == "" || req.CentralManagementURL == "" || req.APIKey == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "admin_password, central_management_url and api_key are required"})
		return
	}

	// Verify the admin password via bcrypt — same check as the normal admin login.
	if err := bcrypt.CompareHashAndPassword(
		[]byte(api.Controller.Options.adminPassword),
		[]byte(req.AdminPassword),
	); err != nil {
		log.Printf("Central Management pairing: invalid admin password from %s", r.RemoteAddr)
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid admin password"})
		return
	}

	// Apply the centralized management configuration.
	api.Controller.Options.mutex.Lock()
	api.Controller.Options.CentralManagementEnabled = true
	api.Controller.Options.CentralManagementURL = req.CentralManagementURL
	api.Controller.Options.CentralManagementAPIKey = req.APIKey
	if req.ServerName != "" {
		api.Controller.Options.CentralManagementServerName = req.ServerName
	}
	if req.ServerID != "" {
		api.Controller.Options.CentralManagementServerID = req.ServerID
	}
	api.Controller.Options.mutex.Unlock()

	// Persist to database.
	if err := api.Controller.Options.Write(api.Controller.Database); err != nil {
		log.Printf("Central Management pairing: failed to persist options: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to save configuration"})
		return
	}

	// Start (or restart) the central management service so it registers immediately.
	if api.Controller.CentralManagement != nil {
		api.Controller.CentralManagement.Stop()
	}
	cms := NewCentralManagementService(api.Controller)
	api.Controller.CentralManagement = cms
	go cms.Start()

	log.Printf("Central Management pairing: server successfully paired with %s", req.CentralManagementURL)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": "Server paired with Central Management successfully",
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

// CMAdminTokenHandler issues a short-lived admin JWT so that Central Management can open
// this server's admin UI in a new browser tab without requiring the admin password.
// The caller must supply the correct X-API-Key header matching this server's stored CM API key.
// POST /api/central-management/admin-token
func (api *Api) CMAdminTokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Central management must be enabled on this server
	if !api.Controller.Options.CentralManagementEnabled {
		api.exitWithError(w, http.StatusForbidden, "Central management is not enabled on this server")
		return
	}

	// Verify the API key
	apiKey := r.Header.Get("X-API-Key")
	if apiKey == "" || apiKey != api.Controller.Options.CentralManagementAPIKey {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid or missing API key")
		return
	}

	// Generate a UUID claim ID
	id, err := uuid.NewRandom()
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	// Sign a JWT the same way LoginHandler does so it is accepted by ValidateToken
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{ID: id.String()})
	sToken, err := token.SignedString([]byte(api.Controller.Options.secret))
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to sign token")
		return
	}

	// Register the token in the Admin token list so it will be accepted
	admin := api.Controller.Admin
	admin.mutex.Lock()
	if len(admin.Tokens) < 5 {
		admin.Tokens = append(admin.Tokens, sToken)
	} else {
		admin.Tokens = append(admin.Tokens[1:], sToken)
	}
	admin.mutex.Unlock()

	log.Printf("Central Management: issued temporary admin token for CM access")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"token": sToken,
	})
}

// SetRemovalCodeHandler receives a one-time removal code from Central Management.
// CM calls this when an admin clicks "Generate Removal Code" in the CM UI.
// The code is stored temporarily (15 min) and validated when the local admin
// clicks "Leave Central Management" in the TLR admin panel.
// POST /api/central-management/set-removal-code
func (api *Api) SetRemovalCodeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if !api.Controller.Options.CentralManagementEnabled {
		api.exitWithError(w, http.StatusForbidden, "Central management is not enabled")
		return
	}

	// Authenticate via the CM API key
	apiKey := r.Header.Get("X-API-Key")
	if apiKey == "" || apiKey != api.Controller.Options.CentralManagementAPIKey {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid or missing API key")
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		api.exitWithError(w, http.StatusBadRequest, "code is required")
		return
	}

	cms := api.Controller.CentralManagement
	if cms == nil {
		api.exitWithError(w, http.StatusInternalServerError, "Central management service not running")
		return
	}

	cms.removalCodeMu.Lock()
	cms.removalCode = strings.ToUpper(strings.TrimSpace(req.Code))
	cms.removalCodeExpiry = time.Now().Add(15 * time.Minute)
	cms.removalCodeMu.Unlock()

	log.Printf("Central Management: removal code set (expires in 15 min)")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// LeaveCentralManagementHandler lets a local TLR admin remove this server from Central Management.
// Requires a valid admin JWT token + the one-time removal code previously pushed by CM.
// POST /api/central-management/leave
func (api *Api) LeaveCentralManagementHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Require a valid admin session token
	token := r.Header.Get("Authorization")
	if !api.Controller.Admin.ValidateToken(token) {
		api.exitWithError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		api.exitWithError(w, http.StatusBadRequest, "code is required")
		return
	}
	enteredCode := strings.ToUpper(strings.TrimSpace(req.Code))

	cms := api.Controller.CentralManagement
	if cms == nil {
		api.exitWithError(w, http.StatusBadRequest, "No removal code has been generated. Ask a Central Management admin to generate one first.")
		return
	}

	// Validate code
	cms.removalCodeMu.Lock()
	validCode := cms.removalCode
	expiry := cms.removalCodeExpiry
	cms.removalCodeMu.Unlock()

	if validCode == "" {
		api.exitWithError(w, http.StatusBadRequest, "No removal code has been generated. Ask a Central Management admin to generate one first.")
		return
	}
	if time.Now().After(expiry) {
		cms.removalCodeMu.Lock()
		cms.removalCode = ""
		cms.removalCodeMu.Unlock()
		api.exitWithError(w, http.StatusBadRequest, "Removal code has expired. Please generate a new one from Central Management.")
		return
	}
	if enteredCode != validCode {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid removal code.")
		return
	}

	// Code is valid — clear it immediately (one-time use)
	cms.removalCodeMu.Lock()
	cms.removalCode = ""
	cms.removalCodeMu.Unlock()

	// Snapshot the CM credentials before we wipe them so we can notify CM
	api.Controller.Options.mutex.Lock()
	cmURL := api.Controller.Options.CentralManagementURL
	cmAPIKey := api.Controller.Options.CentralManagementAPIKey
	api.Controller.Options.mutex.Unlock()

	// Notify the CM system to remove this server from its list.
	// This is best-effort — we proceed with unlinking even if CM is unreachable.
	if cmURL != "" && cmAPIKey != "" {
		selfRemoveURL := strings.TrimRight(cmURL, "/") + "/api/tlr/server"
		req, err := http.NewRequest(http.MethodDelete, selfRemoveURL, nil)
		if err == nil {
			req.Header.Set("X-API-Key", cmAPIKey)
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				log.Printf("Central Management: could not notify CM to remove server (continuing anyway): %v", err)
			} else {
				resp.Body.Close()
				log.Printf("Central Management: notified CM to remove server (HTTP %d)", resp.StatusCode)
			}
		}
	}

	// Stop the CM service
	cms.Stop()
	api.Controller.CentralManagement = nil

	// Clear all CM settings
	api.Controller.Options.mutex.Lock()
	api.Controller.Options.CentralManagementEnabled = false
	api.Controller.Options.CentralManagementURL = ""
	api.Controller.Options.CentralManagementAPIKey = ""
	api.Controller.Options.CentralManagementServerName = ""
	api.Controller.Options.CentralManagementServerID = ""
	api.Controller.Options.mutex.Unlock()

	// Persist to database
	if err := api.Controller.Options.Write(api.Controller.Database); err != nil {
		log.Printf("Central Management: warning — failed to persist options after leaving CM: %v", err)
	}

	log.Printf("Central Management: server successfully removed from Central Management")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": "Server successfully removed from Central Management",
	})
}

// CentralWebhookSetRelayAPIKeyHandler receives a relay server API key from
// Central Management and saves it to this server's options so push
// notifications can be sent via the relay.
// POST /api/webhook/central-set-relay-key
func (api *Api) CentralWebhookSetRelayAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	if !api.Controller.Options.CentralManagementEnabled {
		api.exitWithError(w, http.StatusForbidden, "Central management not enabled")
		return
	}

	apiKey := r.Header.Get("X-API-Key")
	if apiKey == "" || apiKey != api.Controller.Options.CentralManagementAPIKey {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid API key")
		return
	}

	var req struct {
		RelayAPIKey string `json:"relay_api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.RelayAPIKey == "" {
		api.exitWithError(w, http.StatusBadRequest, "relay_api_key is required")
		return
	}

	// Save to options
	api.Controller.Options.mutex.Lock()
	api.Controller.Options.RelayServerAPIKey = req.RelayAPIKey
	api.Controller.Options.mutex.Unlock()

	// Persist to database
	if err := api.Controller.Options.Write(api.Controller.Database); err != nil {
		log.Printf("CentralWebhookSetRelayAPIKey: failed to persist relay API key: %v", err)
		api.exitWithError(w, http.StatusInternalServerError, "failed to save relay API key")
		return
	}

	log.Printf("CentralWebhookSetRelayAPIKey: relay API key updated via Central Management")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": "Relay API key updated successfully",
	})
}
