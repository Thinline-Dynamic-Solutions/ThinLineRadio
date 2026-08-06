// Copyright (C) 2024 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (api *Api) cmAuthEnabled() bool {
	return api.Controller.Options.CentralManagementEnabled &&
		strings.TrimSpace(api.Controller.Options.CentralManagementURL) != ""
}

func (api *Api) cmPortalBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(api.Controller.Options.CentralManagementURL), "/")
}

// CMAuthStatusHandler reports whether this scanner uses Central Management auth.
// GET /api/cm-auth/status
func (api *Api) CMAuthStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	enabled := api.cmAuthEnabled()
	resp := map[string]interface{}{
		"centralManagementEnabled": enabled,
	}
	if enabled {
		resp["portalUrl"] = api.cmPortalBaseURL()
		if name := strings.TrimSpace(api.Controller.Options.CentralManagementServerName); name != "" {
			resp["serverName"] = name
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// CMAuthLoginHandler proxies AlertPage login to CM POST /api/auth/login.
func (api *Api) CMAuthLoginHandler(w http.ResponseWriter, r *http.Request) {
	api.proxyCMAuth(w, r, "/api/auth/login", http.MethodPost)
}

// CMAuthRegisterHandler proxies AlertPage signup to CM POST /api/auth/register.
func (api *Api) CMAuthRegisterHandler(w http.ResponseWriter, r *http.Request) {
	api.proxyCMAuth(w, r, "/api/auth/register", http.MethodPost)
}

// CMAuthForgotPasswordHandler proxies password-reset request to CM.
// Reset email links stay on the AlertPage / CM portal (not this scanner).
func (api *Api) CMAuthForgotPasswordHandler(w http.ResponseWriter, r *http.Request) {
	api.proxyCMAuth(w, r, "/api/auth/request-password-reset", http.MethodPost)
}

// CMAuthResetPasswordHandler proxies password-reset confirmation to CM.
func (api *Api) CMAuthResetPasswordHandler(w http.ResponseWriter, r *http.Request) {
	api.proxyCMAuth(w, r, "/api/auth/reset-password", http.MethodPost)
}

func (api *Api) proxyCMAuth(w http.ResponseWriter, r *http.Request, path, method string) {
	if r.Method != method {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if !api.cmAuthEnabled() {
		api.exitWithError(w, http.StatusForbidden, "Central management auth is not enabled")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Normalize client field names to CM's snake_case when present.
	var payload map[string]interface{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
			return
		}
		if v, ok := payload["firstName"]; ok {
			if _, exists := payload["first_name"]; !exists {
				payload["first_name"] = v
			}
			delete(payload, "firstName")
		}
		if v, ok := payload["lastName"]; ok {
			if _, exists := payload["last_name"]; !exists {
				payload["last_name"] = v
			}
			delete(payload, "lastName")
		}
		if email, ok := payload["email"].(string); ok {
			payload["email"] = strings.TrimSpace(strings.ToLower(email))
		}
		body, err = json.Marshal(payload)
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, "Failed to prepare request")
			return
		}
	}

	status, respBody, contentType, err := api.cmHTTP(method, path, body, "")
	if err != nil {
		log.Printf("cm-auth proxy %s: %v", path, err)
		api.exitWithError(w, http.StatusBadGateway, "Unable to reach AlertPage authentication service")
		return
	}

	if contentType == "" {
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write(respBody)
}

// CMAuthSessionHandler resolves access for this scanner using a CM JWT.
// POST /api/cm-auth/session  { "token": "..." }
// Returns { pin } or { needsSubscription, portalUrl, message }.
func (api *Api) CMAuthSessionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if !api.cmAuthEnabled() {
		api.exitWithError(w, http.StatusForbidden, "Central management auth is not enabled")
		return
	}

	var req struct {
		Token     string `json:"token"`
		ReturnURL string `json:"returnUrl"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		api.exitWithError(w, http.StatusBadRequest, "token is required")
		return
	}

	returnURL := strings.TrimSpace(req.ReturnURL)
	if returnURL == "" {
		if base := strings.TrimSpace(api.Controller.Options.BaseUrl); base != "" {
			returnURL = strings.TrimRight(base, "/")
		} else {
			scheme := "http"
			if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
				scheme = "https"
			}
			host := r.Host
			if fwd := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); fwd != "" {
				host = fwd
			}
			returnURL = scheme + "://" + host
		}
	}

	status, body, _, err := api.cmHTTP(http.MethodGet, "/api/user/servers", nil, token)
	if err != nil {
		log.Printf("cm-auth session servers: %v", err)
		api.exitWithError(w, http.StatusBadGateway, "Unable to reach AlertPage")
		return
	}
	if status == http.StatusUnauthorized {
		api.exitWithError(w, http.StatusUnauthorized, "Session expired. Please sign in again.")
		return
	}
	if status < 200 || status >= 300 {
		api.exitWithError(w, http.StatusBadGateway, "Failed to resolve scanner access")
		return
	}

	var serversResp struct {
		Servers []struct {
			ID   json.Number `json:"id"`
			Name string      `json:"name"`
			URL  string      `json:"url"`
			PIN  string      `json:"pin"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(body, &serversResp); err != nil {
		api.exitWithError(w, http.StatusBadGateway, "Invalid response from AlertPage")
		return
	}

	match := api.findAssignedCMServer(serversResp.Servers)
	if match != nil && strings.TrimSpace(match.PIN) != "" {
		email, firstName, lastName := api.fetchCMUserProfile(token)
		if email == "" {
			api.exitWithError(w, http.StatusBadGateway, "Unable to resolve AlertPage user profile")
			return
		}

		_, _, grantErr := api.applyCentralUserGrant(CentralUserGrantRequest{
			Email:           email,
			FirstName:       firstName,
			LastName:        lastName,
			PIN:             strings.TrimSpace(match.PIN),
			Systems:         "*",
			Talkgroups:      "*",
			ConnectionLimit: 0,
		})
		if grantErr != nil {
			log.Printf("cm-auth session grant: %v", grantErr)
			api.exitWithError(w, http.StatusInternalServerError, "Failed to provision local access")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"pin":        strings.TrimSpace(match.PIN),
			"email":      email,
			"serverName": match.Name,
		})
		return
	}

	hasSub, subMessage := api.fetchCMSubscriptionHint(token)
	portalPath := "/scanners"
	message := "This scanner is not assigned to your AlertPage account. Open AlertPage to select scanners."
	if !hasSub {
		portalPath = "/billing"
		if subMessage != "" {
			message = subMessage
		} else {
			message = "An AlertPage subscription is required to listen to this scanner."
		}
	}

	portalURL := api.buildCMPortalURL(portalPath, returnURL)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"needsSubscription": true,
		"portalUrl":         portalURL,
		"message":           message,
		"hasSubscription":   hasSub,
	})
}

type cmAssignedServer struct {
	ID   string
	Name string
	URL  string
	PIN  string
}

func (api *Api) findAssignedCMServer(servers []struct {
	ID   json.Number `json:"id"`
	Name string      `json:"name"`
	URL  string      `json:"url"`
	PIN  string      `json:"pin"`
}) *cmAssignedServer {
	wantID := strings.TrimSpace(api.Controller.Options.CentralManagementServerID)
	wantURL := normalizeCMURL(api.Controller.Options.BaseUrl)

	for _, s := range servers {
		idStr := strings.TrimSpace(s.ID.String())
		if wantID != "" && idStr == wantID {
			return &cmAssignedServer{ID: idStr, Name: s.Name, URL: s.URL, PIN: s.PIN}
		}
		if wantURL != "" && normalizeCMURL(s.URL) == wantURL {
			return &cmAssignedServer{ID: idStr, Name: s.Name, URL: s.URL, PIN: s.PIN}
		}
	}
	return nil
}

func normalizeCMURL(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return strings.TrimRight(raw, "/")
	}
	host := u.Host
	if host == "" {
		return strings.TrimRight(raw, "/")
	}
	return host + strings.TrimRight(u.Path, "/")
}

func (api *Api) fetchCMUserProfile(token string) (email, firstName, lastName string) {
	status, body, _, err := api.cmHTTP(http.MethodGet, "/api/user/me", nil, token)
	if err != nil || status < 200 || status >= 300 {
		return "", "", ""
	}
	var me map[string]interface{}
	if err := json.Unmarshal(body, &me); err != nil {
		return "", "", ""
	}
	// CM may nest under "user" or return the user object directly.
	if nested, ok := me["user"].(map[string]interface{}); ok {
		me = nested
	}
	email, _ = me["email"].(string)
	firstName, _ = me["first_name"].(string)
	lastName, _ = me["last_name"].(string)
	return strings.TrimSpace(strings.ToLower(email)), strings.TrimSpace(firstName), strings.TrimSpace(lastName)
}

func (api *Api) fetchCMSubscriptionHint(token string) (hasSubscription bool, message string) {
	status, body, _, err := api.cmHTTP(http.MethodGet, "/api/user/subscription", nil, token)
	if err != nil || status < 200 || status >= 300 {
		return false, ""
	}
	var sub map[string]interface{}
	if err := json.Unmarshal(body, &sub); err != nil {
		return false, ""
	}
	if v, ok := sub["has_subscription"].(bool); ok {
		hasSubscription = v
	}
	if m, ok := sub["message"].(string); ok {
		message = m
	}
	return hasSubscription, message
}

func (api *Api) buildCMPortalURL(path, returnURL string) string {
	base := api.cmPortalBaseURL()
	u := base + path
	if returnURL != "" {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += sep + "returnUrl=" + url.QueryEscape(returnURL)
	}
	return u
}

// CMAuthManageAccountHandler returns a short-lived AlertPage SSO URL for the
// authenticated listener so they can manage email/password/billing on CM.
// POST /api/cm-auth/manage-account  Authorization: Bearer <pin>
func (api *Api) CMAuthManageAccountHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if !api.cmAuthEnabled() {
		api.exitWithError(w, http.StatusForbidden, "Central management auth is not enabled")
		return
	}

	pin := r.URL.Query().Get("pin")
	if pin == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			pin = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}
	pin = strings.TrimSpace(pin)
	if pin == "" {
		api.exitWithError(w, http.StatusUnauthorized, "PIN required")
		return
	}

	user := api.Controller.Users.GetUserByPin(pin)
	if user == nil {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid PIN")
		return
	}
	email := strings.TrimSpace(strings.ToLower(user.Email))
	if email == "" {
		api.exitWithError(w, http.StatusBadRequest, "Account has no email address")
		return
	}

	apiKey := strings.TrimSpace(api.Controller.Options.CentralManagementAPIKey)
	if apiKey == "" {
		api.exitWithError(w, http.StatusServiceUnavailable, "Central management API key is not configured")
		return
	}

	payload, err := json.Marshal(map[string]string{"email": email})
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to prepare request")
		return
	}

	status, respBody, _, err := api.cmHTTPAPIKey(http.MethodPost, "/api/tlr/user-sso", payload, apiKey)
	if err != nil {
		log.Printf("cm-auth manage-account: %v", err)
		api.exitWithError(w, http.StatusBadGateway, "Unable to reach AlertPage")
		return
	}
	if status == http.StatusNotFound {
		api.exitWithError(w, http.StatusNotFound, "Your AlertPage account is not linked to this scanner")
		return
	}
	if status < 200 || status >= 300 {
		log.Printf("cm-auth manage-account: CM status %d: %s", status, string(respBody))
		api.exitWithError(w, http.StatusBadGateway, "AlertPage rejected the manage-account request")
		return
	}

	var sso struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(respBody, &sso); err != nil || strings.TrimSpace(sso.Token) == "" {
		api.exitWithError(w, http.StatusBadGateway, "Invalid response from AlertPage")
		return
	}

	manageURL := api.cmPortalBaseURL() + "/sso?token=" + url.QueryEscape(sso.Token) +
		"&expires_at=" + url.QueryEscape(sso.ExpiresAt.UTC().Format(time.RFC3339)) +
		"&returnUrl=" + url.QueryEscape("/account-settings")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"url":       manageURL,
		"portalUrl": api.cmPortalBaseURL(),
		"expiresAt": sso.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (api *Api) cmHTTP(method, path string, body []byte, bearerToken string) (status int, respBody []byte, contentType string, err error) {
	return api.cmHTTPRequest(method, path, body, bearerToken, "")
}

func (api *Api) cmHTTPAPIKey(method, path string, body []byte, apiKey string) (status int, respBody []byte, contentType string, err error) {
	return api.cmHTTPRequest(method, path, body, "", apiKey)
}

func (api *Api) cmHTTPRequest(method, path string, body []byte, bearerToken, apiKey string) (status int, respBody []byte, contentType string, err error) {
	base := api.cmPortalBaseURL()
	if base == "" {
		return 0, nil, "", fmt.Errorf("central management URL not configured")
	}
	endpoint := base + path

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		return 0, nil, "", err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()

	respBody, err = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, nil, "", err
	}
	return resp.StatusCode, respBody, resp.Header.Get("Content-Type"), nil
}
