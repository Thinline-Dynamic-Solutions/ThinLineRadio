// Copyright (C) 2025 Thinline Dynamic Solutions
//
// One-time mobile app sign-in: email link → landing page → app deep link → password + consume API.

package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"strings"
)

func hashMobileSetupToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// issueMobileSetupToken generates a new single-use token (valid until consumed on the server),
// persists hash, returns plaintext for links. No calendar expiry — possession of the link plus
// the account password is the gate; the token is cleared after a successful consume.
func (api *Api) issueMobileSetupToken(user *User) (plaintext string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", err
	}
	plaintext = hex.EncodeToString(buf)
	user.MobileSetupTokenHash = hashMobileSetupToken(plaintext)
	user.MobileSetupTokenExpires = 0
	if err = api.Controller.Users.Update(user); err != nil {
		return "", err
	}
	if err = api.Controller.Users.Write(api.Controller.Database); err != nil {
		return "", err
	}
	api.Controller.SyncConfigToFile()
	return plaintext, nil
}

// sendMobileWelcomeEmailOnce issues a mobile setup token and sends the welcome email at most once per account.
func (api *Api) sendMobileWelcomeEmailOnce(user *User) {
	if user == nil || !api.Controller.Options.EmailServiceEnabled || user.MobileWelcomeEmailSent {
		return
	}
	plain, err := api.issueMobileSetupToken(user)
	if err != nil {
		log.Printf("sendMobileWelcomeEmailOnce: issue token: %v", err)
		return
	}
	if err := api.Controller.EmailService.SendMobileSetupEmail(user, plain); err != nil {
		log.Printf("sendMobileWelcomeEmailOnce: send email: %v", err)
		return
	}
	user.MobileWelcomeEmailSent = true
	if err := api.Controller.Users.Update(user); err != nil {
		log.Printf("sendMobileWelcomeEmailOnce: update user: %v", err)
		return
	}
	if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
		log.Printf("sendMobileWelcomeEmailOnce: write: %v", err)
		return
	}
	api.Controller.SyncConfigToFile()
}

func (api *Api) findUserByMobileSetupToken(plaintext string) *User {
	if plaintext == "" {
		return nil
	}
	want := hashMobileSetupToken(plaintext)
	for _, u := range api.Controller.Users.GetAllUsers() {
		if u.MobileSetupTokenHash != "" && u.MobileSetupTokenHash == want {
			return u
		}
	}
	return nil
}

func normalizePublicBaseURL(raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		base = "https://localhost:8080"
	}
	if strings.HasPrefix(base, "http://") {
		base = strings.Replace(base, "http://", "https://", 1)
	} else if !strings.HasPrefix(base, "https://") {
		base = "https://" + base
	}
	return strings.TrimRight(base, "/")
}

// MobileSetupLandingHandler serves a tiny HTML page that opens the mobile app via custom URL scheme.
func (api *Api) MobileSetupLandingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		http.Error(w, "Missing token", http.StatusBadRequest)
		return
	}
	base := normalizePublicBaseURL(api.Controller.Options.BaseUrl)
	encBase := url.QueryEscape(base)
	deep := fmt.Sprintf("thinlineradio://scanner-setup?token=%s&baseUrl=%s", url.QueryEscape(token), encBase)
	branding := api.Controller.Options.Branding
	if branding == "" {
		branding = "ThinLine Radio"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// Auto-redirect; if the app is not installed, user can use the manual link.
	fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title></head><body style="font-family:system-ui,sans-serif;padding:24px;text-align:center">
<p><strong>%s</strong></p>
<p>Opening the app…</p>
<p><a href="%s">Tap here if nothing happens</a></p>
<script>setTimeout(function(){ location.href = %q; }, 400);</script>
</body></html>`,
		html.EscapeString(branding),
		html.EscapeString(branding),
		html.EscapeString(deep),
		deep,
	)
}

// UserMobileSetupConsumeHandler exchanges a valid one-time token + password for the listener PIN (mobile app only).
func (api *Api) UserMobileSetupConsumeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	body.Token = strings.TrimSpace(body.Token)
	if body.Token == "" || body.Password == "" {
		api.exitWithError(w, http.StatusBadRequest, "Token and password are required")
		return
	}
	user := api.findUserByMobileSetupToken(body.Token)
	if user == nil {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid link")
		return
	}
	if user.IsSuspended() {
		api.exitWithError(w, http.StatusForbidden, "Your account has been suspended. Contact support for assistance.")
		return
	}
	if !user.CheckPassword(body.Password) {
		api.exitWithError(w, http.StatusUnauthorized, "Incorrect password")
		return
	}
	user.MobileSetupTokenHash = ""
	user.MobileSetupTokenExpires = 0
	if err := api.Controller.Users.Update(user); err != nil {
		log.Printf("mobile setup consume: update user: %v", err)
		api.exitWithError(w, http.StatusInternalServerError, "Failed to update account")
		return
	}
	if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
		log.Printf("mobile setup consume: write: %v", err)
		api.exitWithError(w, http.StatusInternalServerError, "Failed to save account")
		return
	}
	api.Controller.SyncConfigToFile()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"pin":   user.Pin,
		"email": user.Email,
	})
}

// RelayListenerPinWebhookHandler is called by the ThinLine relay with email + password so the app
// can add scanners without an email link. Authenticated with X-API-Key matching RelayServerAPIKey.
// POST /api/webhook/relay-listener-pin  Body: {"email":"...","password":"..."}
func (api *Api) RelayListenerPinWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	key := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if key == "" || key != api.Controller.Options.RelayServerAPIKey {
		api.exitWithError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	email := strings.TrimSpace(body.Email)
	fail := map[string]interface{}{"ok": false}
	w.Header().Set("Content-Type", "application/json")
	if email == "" || strings.TrimSpace(body.Password) == "" {
		json.NewEncoder(w).Encode(fail)
		return
	}
	if err := ValidateEmail(email); err != nil {
		json.NewEncoder(w).Encode(fail)
		return
	}
	user := api.Controller.Users.GetUserByEmail(email)
	if user == nil {
		json.NewEncoder(w).Encode(fail)
		return
	}
	if api.Controller.Options.EmailVerificationRequired && !user.Verified {
		json.NewEncoder(w).Encode(fail)
		return
	}
	if user.IsSuspended() {
		json.NewEncoder(w).Encode(fail)
		return
	}
	if user.ForcePasswordReset {
		json.NewEncoder(w).Encode(fail)
		return
	}
	if !user.CheckPassword(body.Password) {
		json.NewEncoder(w).Encode(fail)
		return
	}
	pinBefore := user.Pin
	user.ensurePinsLoaded()
	if user.Pin != pinBefore {
		if err := api.Controller.Users.Update(user); err != nil {
			log.Printf("relay listener pin: persist new pin: %v", err)
			json.NewEncoder(w).Encode(fail)
			return
		}
		if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
			log.Printf("relay listener pin: write users: %v", err)
			json.NewEncoder(w).Encode(fail)
			return
		}
		api.Controller.SyncConfigToFile()
	}
	if user.Pin == "" {
		json.NewEncoder(w).Encode(fail)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "pin": user.Pin})
}

// PublicAppLinksHandler returns App Store / Play URLs (from server options or built-in defaults).
func (api *Api) PublicAppLinksHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"iosAppStoreUrl":      api.Controller.Options.EffectiveIOSAppStoreURL(),
		"androidPlayStoreUrl": api.Controller.Options.EffectiveAndroidPlayStoreURL(),
	})
}
