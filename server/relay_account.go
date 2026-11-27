// Copyright (C) 2025 Thinline Dynamic Solutions
//
// relay_account.go — server-owner sign-in against the relay server, and the
// Nominatim geocoding add-on subscription it gates. Mirrors relay_suspension.go's
// poll-and-cache pattern. The relay itself is only used for billing/sign-in now —
// once subscribed, this server geocodes DIRECTLY against the nominatim-gateway
// service (see mapping/geocode_relay_nominatim.go), whose URL is learned from
// the same /api/geocode/status poll that fetches the subscription status below
// (see pollNominatimStatusOnce's "gateway_url" field / NominatimGatewayURLSnapshot).
// The gateway enforces its own allow-list (kept in sync from relay via Stripe
// webhooks) — this cache only decides whether it's worth attempting a direct
// geocode call at all.
//
// Password is never persisted: login/migrate exchange it once for a refresh
// token (persisted, revocable) and a short-lived access token (memory only).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

const relayAccountHTTPTimeout = 15 * time.Second

func relayAccountRequest(controller *Controller, method, path string, body map[string]any) (map[string]any, int, error) {
	relayURL := getRelayServerURL()
	var reqBody []byte
	if body != nil {
		var err error
		reqBody, err = json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
	}
	req, err := http.NewRequest(method, relayURL+path, bytes.NewReader(reqBody))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Rdio-Auth", getRelayServerAuthKey())
	client := &http.Client{Timeout: relayAccountHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var data map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&data)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := "relay request failed"
		if data != nil {
			if e, ok := data["error"].(string); ok && e != "" {
				msg = e
			}
		}
		return data, resp.StatusCode, fmt.Errorf("%s", msg)
	}
	return data, resp.StatusCode, nil
}

func (controller *Controller) setRelayAccountSession(accessToken string, accessExpiresAt time.Time) {
	controller.RelayAccountMu.Lock()
	controller.RelayAccountAccessToken = accessToken
	controller.RelayAccountAccessExpiresAt = accessExpiresAt
	controller.RelayAccountLastError = ""
	controller.relayRefreshRejectedAt = time.Time{}
	controller.RelayAccountMu.Unlock()
}

func (controller *Controller) setRelayAccountError(err error) {
	controller.RelayAccountMu.Lock()
	if err != nil {
		controller.RelayAccountLastError = err.Error()
	}
	controller.RelayAccountMu.Unlock()
}

// RelayAccountAccessTokenSnapshot returns the current access token, or "" if
// signed out / expired. Safe to call from the geocode fallback chain on
// every call — just a mutex read, no network.
func (controller *Controller) RelayAccountAccessTokenSnapshot() string {
	controller.RelayAccountMu.RLock()
	defer controller.RelayAccountMu.RUnlock()
	if controller.RelayAccountAccessToken == "" || time.Now().After(controller.RelayAccountAccessExpiresAt) {
		return ""
	}
	return controller.RelayAccountAccessToken
}

// RelayAccountSignedIn reports whether this server holds a relay session. A
// relay outage or a failed refresh leaves it signed in; only
// RelayAccountLogout signs it out.
func (controller *Controller) RelayAccountSignedIn() bool {
	controller.Options.mutex.Lock()
	defer controller.Options.mutex.Unlock()
	return controller.Options.RelayAccountRefreshToken != ""
}

// RelayAccountStatus summarizes sign-in + subscription state for the admin UI.
type RelayAccountStatus struct {
	Username                    string `json:"username"`
	SignedIn                    bool   `json:"signedIn"`
	LastError                   string `json:"lastError"`
	NominatimSubscriptionStatus string `json:"nominatimSubscriptionStatus"`
}

func (controller *Controller) GetRelayAccountStatus() RelayAccountStatus {
	controller.Options.mutex.Lock()
	username := controller.Options.RelayAccountUsername
	controller.Options.mutex.Unlock()
	controller.RelayAccountMu.RLock()
	lastErr := controller.RelayAccountLastError
	controller.RelayAccountMu.RUnlock()
	return RelayAccountStatus{
		Username:                    username,
		SignedIn:                    controller.RelayAccountSignedIn(),
		LastError:                   lastErr,
		NominatimSubscriptionStatus: controller.NominatimStatusSnapshot(),
	}
}

// NominatimAccessAllowed mirrors relay-side storage.NominatimAccessAllowed —
// only "active"/"trialing" are worth attempting a geocode call for.
func (controller *Controller) NominatimAccessAllowed() bool {
	switch controller.NominatimStatusSnapshot() {
	case "active", "trialing":
		return true
	default:
		return false
	}
}

func (controller *Controller) NominatimStatusSnapshot() string {
	controller.NominatimMu.RLock()
	defer controller.NominatimMu.RUnlock()
	if controller.NominatimSubscriptionStatus == "" {
		return "none"
	}
	return controller.NominatimSubscriptionStatus
}

func (controller *Controller) setNominatimStatus(status string, periodEnd time.Time) {
	controller.NominatimMu.Lock()
	controller.NominatimSubscriptionStatus = status
	controller.NominatimCurrentPeriodEnd = periodEnd
	controller.NominatimMu.Unlock()
	// Cache the last-known-good status so a restart while the relay is
	// unreachable doesn't disable geocoding until the next poll succeeds. Only
	// access-granting statuses are cached; the live 2-minute poll (and the
	// gateway's own per-key allow-list) still correct a genuinely lapsed sub.
	if NominatimStatusGrantsAccess(status) {
		controller.Options.mutex.Lock()
		changed := controller.Options.NominatimSubscriptionStatus != status
		controller.Options.NominatimSubscriptionStatus = status
		controller.Options.mutex.Unlock()
		if changed {
			if err := controller.Options.Write(controller.Database); err != nil {
				log.Printf("relay account: failed to persist nominatim status: %v", err)
			}
		}
	}
}

// NominatimStatusGrantsAccess mirrors relay-side storage.NominatimAccessAllowed —
// only "active"/"trialing" grant geocoding access.
func NominatimStatusGrantsAccess(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "active", "trialing":
		return true
	default:
		return false
	}
}

// NominatimGatewayURLSnapshot returns the last gateway_url reported by the
// relay's /api/geocode/status poll, or "" if never polled successfully (in
// which case direct-geocode lookups stay disabled — see incident_mapping.go).
func (controller *Controller) NominatimGatewayURLSnapshot() string {
	controller.NominatimMu.RLock()
	defer controller.NominatimMu.RUnlock()
	return controller.NominatimGatewayURL
}

func (controller *Controller) setNominatimGatewayURL(url string) {
	url = strings.TrimSpace(url)
	if url == "" {
		return
	}
	controller.NominatimMu.Lock()
	changed := controller.NominatimGatewayURL != url
	controller.NominatimGatewayURL = url
	controller.NominatimMu.Unlock()
	// Cache the last-known-good URL so a restart while the relay is briefly
	// misconfigured/unreachable doesn't leave geocoding disabled until the
	// next successful poll (see loadPersistedNominatimGatewayURL).
	if changed {
		controller.Options.mutex.Lock()
		controller.Options.NominatimGatewayURL = url
		controller.Options.mutex.Unlock()
		if err := controller.Options.Write(controller.Database); err != nil {
			log.Printf("relay account: failed to persist nominatim gateway url: %v", err)
		}
	}
}

// loadPersistedNominatimGatewayURL seeds the in-memory gateway URL and
// subscription status from the last-known-good values saved to Options, so
// geocoding can resume immediately on restart even before the first
// /api/geocode/status poll succeeds.
func (controller *Controller) loadPersistedNominatimGatewayURL() {
	controller.Options.mutex.Lock()
	url := strings.TrimSpace(controller.Options.NominatimGatewayURL)
	status := strings.TrimSpace(controller.Options.NominatimSubscriptionStatus)
	controller.Options.mutex.Unlock()
	controller.NominatimMu.Lock()
	if url != "" && controller.NominatimGatewayURL == "" {
		controller.NominatimGatewayURL = url
	}
	if NominatimStatusGrantsAccess(status) && controller.NominatimSubscriptionStatus == "" {
		controller.NominatimSubscriptionStatus = status
	}
	controller.NominatimMu.Unlock()
}

// RelayAccountLogin signs in with a username/password already linked to this
// server's relay API key (post-migration). On success persists the refresh
// token (never the password) and starts the refresh loop if not already running.
func (controller *Controller) RelayAccountLogin(username, password string) error {
	apiKey := strings.TrimSpace(controller.Options.RelayServerAPIKey)
	if apiKey == "" {
		return fmt.Errorf("relay server API key is not configured")
	}
	data, _, err := relayAccountRequest(controller, http.MethodPost, "/api/account/login", map[string]any{
		"api_key":  apiKey,
		"username": username,
		"password": password,
	})
	if err != nil {
		controller.setRelayAccountError(err)
		return err
	}
	return controller.applyRelayAccountLoginResponse(username, data)
}

// RelayAccountMigrate completes a one-time migration invite (see relay
// internal/storage/accounts.go) for a server that already has an API key but
// no account yet, creating the account and signing in immediately.
func (controller *Controller) RelayAccountMigrate(token, username, password string) error {
	data, _, err := relayAccountRequest(controller, http.MethodPost, "/api/account/migrate/complete", map[string]any{
		"token":    token,
		"username": username,
		"password": password,
	})
	if err != nil {
		controller.setRelayAccountError(err)
		return err
	}
	return controller.applyRelayAccountLoginResponse(username, data)
}

// RelayAccountCreate creates a portal account for this server's existing
// relay API key (email + password; email must match the API key contact) and
// signs in immediately.
func (controller *Controller) RelayAccountCreate(email, password string) error {
	apiKey := strings.TrimSpace(controller.Options.RelayServerAPIKey)
	if apiKey == "" {
		return fmt.Errorf("relay server API key is not configured — request an API key first")
	}
	email = strings.TrimSpace(strings.ToLower(email))
	data, _, err := relayAccountRequest(controller, http.MethodPost, "/api/account/create", map[string]any{
		"api_key":  apiKey,
		"email":    email,
		"password": password,
	})
	if err != nil {
		controller.setRelayAccountError(err)
		return err
	}
	return controller.applyRelayAccountLoginResponse(email, data)
}

// NominatimSubscribe starts a Stripe Checkout session for this server's
// $20/mo Nominatim add-on and returns the hosted checkout URL for the admin
// UI to open in a new tab. Mirrors pollNominatimStatusOnce's auth (bearer
// API key, no account-token requirement yet — see requireAPIKey on the
// relay side).
func (controller *Controller) NominatimSubscribe() (string, error) {
	return controller.RelayPlanSubscribe("nominatim")
}

// RelayAccountRequestMigrationInvite asks the relay to (re)send a migration
// invite email to this key's contact address — the in-admin fallback when
// the original proactive campaign email was missed or expired.
func (controller *Controller) RelayAccountRequestMigrationInvite() error {
	apiKey := strings.TrimSpace(controller.Options.RelayServerAPIKey)
	if apiKey == "" {
		return fmt.Errorf("relay server API key is not configured")
	}
	relayURL := getRelayServerURL()
	req, err := http.NewRequest(http.MethodPost, relayURL+"/api/account/migrate/request", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Rdio-Auth", getRelayServerAuthKey())
	req.Header.Set("X-API-Key", apiKey)
	client := &http.Client{Timeout: relayAccountHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var data map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&data)
		if e, ok := data["error"].(string); ok && e != "" {
			return fmt.Errorf("%s", e)
		}
		return fmt.Errorf("relay returned status %d", resp.StatusCode)
	}
	return nil
}

func (controller *Controller) applyRelayAccountLoginResponse(username string, data map[string]any) error {
	refreshToken, _ := data["refresh_token"].(string)
	if refreshToken == "" {
		return fmt.Errorf("relay did not return a session")
	}
	accessToken, _ := data["access_token"].(string)
	accessExpiresAt := parseRelayTimestamp(data["access_expires_at"])

	controller.Options.mutex.Lock()
	controller.Options.RelayAccountUsername = username
	controller.Options.RelayAccountRefreshToken = refreshToken
	controller.Options.mutex.Unlock()
	if err := controller.Options.Write(controller.Database); err != nil {
		log.Printf("relay account: failed to persist session: %v", err)
	}
	controller.setRelayAccountSession(accessToken, accessExpiresAt)
	go controller.pollNominatimStatusOnce()
	return nil
}

// RelayAccountLogout revokes the session on the relay and clears local state.
func (controller *Controller) RelayAccountLogout() error {
	controller.Options.mutex.Lock()
	refreshToken := controller.Options.RelayAccountRefreshToken
	controller.Options.mutex.Unlock()
	if refreshToken != "" {
		_, _, _ = relayAccountRequest(controller, http.MethodPost, "/api/account/logout", map[string]any{
			"refresh_token": refreshToken,
		})
	}
	controller.Options.mutex.Lock()
	controller.Options.RelayAccountUsername = ""
	controller.Options.RelayAccountRefreshToken = ""
	controller.Options.mutex.Unlock()
	if err := controller.Options.Write(controller.Database); err != nil {
		log.Printf("relay account: failed to persist logout: %v", err)
	}
	controller.RelayAccountMu.Lock()
	controller.RelayAccountAccessToken = ""
	controller.RelayAccountAccessExpiresAt = time.Time{}
	controller.RelayAccountLastError = ""
	controller.RelayAccountMu.Unlock()
	controller.setNominatimStatus("none", time.Time{})
	return nil
}

// refreshRelayAccessTokenOnce exchanges the persisted refresh token for a new
// short-lived access token. Called proactively before expiry and once at
// startup so a restart doesn't require the operator to sign in again.
func (controller *Controller) refreshRelayAccessTokenOnce() {
	controller.Options.mutex.Lock()
	refreshToken := controller.Options.RelayAccountRefreshToken
	controller.Options.mutex.Unlock()
	if refreshToken == "" {
		return
	}
	data, status, err := relayAccountRequest(controller, http.MethodPost, "/api/account/refresh", map[string]any{
		"refresh_token": refreshToken,
	})
	if err != nil {
		controller.setRelayAccountError(err)
		if status == http.StatusUnauthorized {
			controller.RelayAccountMu.Lock()
			controller.relayRefreshRejectedAt = time.Now()
			controller.RelayAccountMu.Unlock()
		}
		log.Printf("relay account: refresh failed: %v", err)
		return
	}
	accessToken, _ := data["access_token"].(string)
	accessExpiresAt := parseRelayTimestamp(data["access_expires_at"])
	if accessToken == "" {
		return
	}
	controller.setRelayAccountSession(accessToken, accessExpiresAt)
}

// pollNominatimStatusOnce checks the relay for this key's current Nominatim
// subscription status. Requires a signed-in account once the relay enables
// require_account_session; until then the API key alone is enough, same as
// every other relay-authenticated call this server already makes.
func (controller *Controller) pollNominatimStatusOnce() {
	relayURL := getRelayServerURL()
	apiKey := strings.TrimSpace(controller.Options.RelayServerAPIKey)
	if apiKey == "" {
		return
	}
	req, err := http.NewRequest(http.MethodGet, relayURL+"/api/geocode/status", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if token := controller.RelayAccountAccessTokenSnapshot(); token != "" {
		req.Header.Set("X-Account-Token", token)
	}
	client := &http.Client{Timeout: relayAccountHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return
	}
	status, _ := data["status"].(string)
	if status == "" {
		status = "none"
	}
	periodEnd := parseRelayTimestamp(data["current_period_end"])
	controller.setNominatimStatus(status, periodEnd)
	if gatewayURL, _ := data["gateway_url"].(string); gatewayURL != "" {
		controller.setNominatimGatewayURL(gatewayURL)
	}
}

func (controller *Controller) relayAccessTokenNeedsRefresh() bool {
	controller.RelayAccountMu.RLock()
	defer controller.RelayAccountMu.RUnlock()
	if time.Since(controller.relayRefreshRejectedAt) < 10*time.Minute {
		return false
	}
	return controller.RelayAccountAccessToken == "" || time.Until(controller.RelayAccountAccessExpiresAt) < 5*time.Minute
}

// startRelayAccountRefreshLoop keeps the access token fresh (15-minute TTL,
// renewed when under 5 minutes remain, retried every minute after a failure)
// and periodically re-checks the Nominatim subscription so a
// cancellation/renewal on Stripe is picked up without a TLR restart.
func (controller *Controller) startRelayAccountRefreshLoop() {
	controller.loadPersistedNominatimGatewayURL()
	controller.refreshRelayAccessTokenOnce()
	controller.pollNominatimStatusOnce()
	accessTicker := time.NewTicker(time.Minute)
	statusTicker := time.NewTicker(2 * time.Minute)
	defer accessTicker.Stop()
	defer statusTicker.Stop()
	for {
		select {
		case <-accessTicker.C:
			if controller.relayAccessTokenNeedsRefresh() {
				controller.refreshRelayAccessTokenOnce()
			}
		case <-statusTicker.C:
			controller.pollNominatimStatusOnce()
		}
	}
}

func parseRelayTimestamp(v any) time.Time {
	s, ok := v.(string)
	if !ok || s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// RelayBillingWebhookHandler receives plan entitlement updates from the relay
// after Stripe checkout/subscription webhooks. Authenticated with X-API-Key.
// POST /api/webhook/relay-billing
func (api *Api) RelayBillingWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	key := r.Header.Get("X-API-Key")
	if key == "" || key != api.Controller.Options.RelayServerAPIKey {
		api.exitWithError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var body struct {
		PlanSlug         string `json:"plan_slug"`
		Status           string `json:"status"`
		CurrentPeriodEnd string `json:"current_period_end"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	status := strings.TrimSpace(strings.ToLower(body.Status))
	if status == "" {
		status = "none"
	}
	periodEnd := parseRelayTimestamp(body.CurrentPeriodEnd)
	planSlug := strings.TrimSpace(strings.ToLower(body.PlanSlug))
	// Geocoding access is gated by the nominatim entitlement cache.
	if planSlug == "" || planSlug == "nominatim" {
		api.Controller.setNominatimStatus(status, periodEnd)
		log.Printf("relay billing webhook: nominatim status=%s", status)
	} else {
		log.Printf("relay billing webhook: plan=%s status=%s", planSlug, status)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
