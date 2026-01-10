// Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
// Modified by Thinline Dynamic Solutions
//lint:file-ignore SA5009 Template strings contain literal percent signs used in CSS.
//lint:file-ignore SA5009 Template strings contain literal percent signs used in CSS/HTML.
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
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v76"
	billingportalsession "github.com/stripe/stripe-go/v76/billingportal/session"
	checkoutsession "github.com/stripe/stripe-go/v76/checkout/session"
	"github.com/stripe/stripe-go/v76/customer"
	"github.com/stripe/stripe-go/v76/subscription"
	"github.com/stripe/stripe-go/v76/webhook"
)

type Api struct {
	Controller *Controller
}

func NewApi(controller *Controller) *Api {
	return &Api{Controller: controller}
}

// isMobileAppRequest checks if the request is from a mobile app by examining the User-Agent header
func (api *Api) isMobileAppRequest(r *http.Request) bool {
	userAgent := r.Header.Get("User-Agent")
	if userAgent == "" {
		return false
	}

	// Check for common mobile app identifiers
	// Flutter/Dart HTTP client typically includes "Dart" in User-Agent
	// Also check for common mobile app patterns
	mobilePatterns := []string{
		"Dart/",     // Flutter/Dart HTTP client
		"Flutter",   // Flutter apps
		"ThinLine",  // Your app name
		"Thinline",  // Your app name (case variant)
		"ohiorsn",   // Your app identifier
		"Android",   // Android apps (though browsers also have this)
		"CFNetwork", // iOS networking library
	}

	userAgentLower := strings.ToLower(userAgent)
	for _, pattern := range mobilePatterns {
		if strings.Contains(userAgentLower, strings.ToLower(pattern)) {
			return true
		}
	}

	return false
}

// verifyTurnstile verifies a Cloudflare Turnstile token using the scanner server's configured keys
// Mobile apps are exempt from Turnstile verification
func (api *Api) verifyTurnstile(token, clientIP string, r *http.Request) (bool, error) {
	if !api.Controller.Options.TurnstileEnabled {
		return true, nil // Turnstile is disabled, so always succeed
	}

	// Exempt mobile apps from Turnstile verification
	if api.isMobileAppRequest(r) {
		return true, nil // Mobile apps are exempt
	}

	if api.Controller.Options.TurnstileSecretKey == "" {
		return false, fmt.Errorf("Turnstile secret key not configured on scanner server")
	}

	data := url.Values{}
	data.Set("secret", api.Controller.Options.TurnstileSecretKey)
	data.Set("response", token)
	if clientIP != "" {
		data.Set("remoteip", clientIP)
	}

	resp, err := http.PostForm("https://challenges.cloudflare.com/turnstile/v0/siteverify", data)
	if err != nil {
		return false, fmt.Errorf("failed to verify Turnstile token: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Success bool     `json:"success"`
		Errors  []string `json:"error-codes"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("failed to decode Turnstile response: %w", err)
	}

	if !result.Success {
		log.Printf("Turnstile verification failed: %v", result.Errors)
		return false, nil
	}

	return true, nil
}

// getSchemeAndHost returns the scheme (http/https) and host from the request,
// taking into account reverse proxy headers (X-Forwarded-Proto, X-Forwarded-Host)
func getSchemeAndHost(r *http.Request) (scheme string, host string) {
	// Check X-Forwarded-Host header (for reverse proxies)
	if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
		host = forwardedHost
	} else {
		host = r.Host
	}

	// Check X-Forwarded-Proto header first (for reverse proxies)
	if forwardedProto := r.Header.Get("X-Forwarded-Proto"); forwardedProto != "" {
		scheme = forwardedProto
	} else if r.TLS != nil {
		// If TLS connection, use https
		scheme = "https"
	} else {
		// Default to http (for localhost development)
		scheme = "http"
	}

	return scheme, host
}

func (api *Api) CallUploadHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var (
			call = NewCall()
			key  string
		)

		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			api.exitWithError(w, http.StatusBadRequest, "Invalid content-type")
			return
		}

		if !strings.HasPrefix(mediaType, "multipart/") {
			api.exitWithError(w, http.StatusBadRequest, "Not a multipart content")
			return
		}

		mr := multipart.NewReader(r.Body, params["boundary"])

		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			} else if err != nil {
				api.exitWithError(w, http.StatusExpectationFailed, fmt.Sprintf("multipart: %s\n", err.Error()))
				return
			}

			b, err := io.ReadAll(p)
			if err != nil {
				api.exitWithError(w, http.StatusExpectationFailed, fmt.Sprintf("ioread: %s\n", err.Error()))
				return
			}

			switch p.FormName() {
			case "key":
				key = string(b)
			default:
				ParseMultipartContent(call, p, b)
			}
		}

		// Check if this is a test connection (SDRTrunk sends key, system, test fields)
		if len(call.Audio) == 0 && call.SystemId > 0 && call.TalkgroupId == 0 && call.Timestamp.IsZero() {
			// This is likely a test connection from SDRTrunk
			// SDRTrunk expects this to fail with "Incomplete call data: no talkgroup" to consider it successful
			// Log test connection details for debugging
			log.Printf("api: Test connection detected - SystemId=%d TalkgroupId=%d AudioLen=%d Timestamp=%v",
				call.SystemId, call.TalkgroupId, len(call.Audio), call.Timestamp)
			api.exitWithError(w, http.StatusExpectationFailed, "Incomplete call data: no talkgroup")
			return
		}

		if ok, err := call.IsValid(); ok {
			api.HandleCall(key, call, w)
		} else {
			// Log full call data for debugging incomplete uploads
			log.Printf("api: INCOMPLETE CALL DATA RECEIVED:")
			log.Printf("  Error: %s", err.Error())
			log.Printf("  SystemId: %d", call.SystemId)
			log.Printf("  TalkgroupId: %d", call.TalkgroupId)
			log.Printf("  Audio Length: %d bytes", len(call.Audio))
			log.Printf("  Timestamp: %v", call.Timestamp)
			log.Printf("  SiteRef: %d", call.SiteRef)
			log.Printf("  Frequency: %d", call.Frequency)
			log.Printf("  Units: %v", call.Units)
			log.Printf("  Patches: %v", call.Patches)
			log.Printf("  Meta.UnitRefs: %v", call.Meta.UnitRefs)
			log.Printf("  Meta.UnitLabels: %v", call.Meta.UnitLabels)
			log.Printf("  Remote Address: %s", r.RemoteAddr)
			log.Printf("  User-Agent: %s", r.Header.Get("User-Agent"))

			// Also log to event system
			api.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("api: Incomplete call data: %s | SystemId=%d TalkgroupId=%d AudioLen=%d Timestamp=%v SiteRef=%d Frequency=%d",
				err.Error(), call.SystemId, call.TalkgroupId, len(call.Audio), call.Timestamp, call.SiteRef, call.Frequency))
			api.exitWithError(w, http.StatusExpectationFailed, fmt.Sprintf("Incomplete call data: %s\n", err.Error()))
		}

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("Unsupported method\n"))
	}
}

func (api *Api) HandleCall(key string, call *Call, w http.ResponseWriter) {
	defer func() {
		if r := recover(); r != nil {
			// Enhanced panic logging with call details
			var systemInfo, talkgroupInfo, metaInfo string
			if call != nil {
				systemInfo = fmt.Sprintf("System: %v (SystemId: %d, Meta.SystemRef: %d)", call.System, call.SystemId, call.Meta.SystemRef)
				talkgroupInfo = fmt.Sprintf("Talkgroup: %v (TalkgroupId: %d, Meta.TalkgroupRef: %d)", call.Talkgroup, call.TalkgroupId, call.Meta.TalkgroupRef)
				metaInfo = fmt.Sprintf("SiteRef: %d, Meta.SiteRef: %d", call.SiteRef, call.Meta.SiteRef)
			} else {
				systemInfo = "call is nil"
			}
			api.Controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("PANIC in HandleCall: %v | %s | %s | %s", r, systemInfo, talkgroupInfo, metaInfo))
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Internal server error\n"))
		}
	}()

	// Populate System and Talkgroup objects for v6-style calls (SystemId/TalkgroupId populated but objects are nil)
	// This must happen BEFORE HasAccess check
	if call != nil && call.System == nil && call.SystemId > 0 {
		if system, ok := api.Controller.Systems.GetSystemByRef(call.SystemId); ok {
			call.System = system
		} else if system, ok := api.Controller.Systems.GetSystemById(uint64(call.SystemId)); ok {
			call.System = system
		}
	}

	if call != nil && call.Talkgroup == nil && call.TalkgroupId > 0 && call.System != nil {
		if talkgroup, ok := call.System.Talkgroups.GetTalkgroupByRef(call.TalkgroupId); ok {
			call.Talkgroup = talkgroup
		}
	}

	var systemRef, talkgroupRef interface{} = "nil", "nil"
	if call != nil {
		if call.System != nil {
			systemRef = call.System.SystemRef
		}
		if call.Talkgroup != nil {
			talkgroupRef = call.Talkgroup.TalkgroupRef
		}
	}
	msg := []byte(fmt.Sprintf("Invalid API key for system %v talkgroup %v.\n", systemRef, talkgroupRef))

	if apikey, ok := api.Controller.Apikeys.GetApikey(key); ok {
		if apikey.HasAccess(call) {
			// Ensure site information is properly resolved before ingestion
			if call != nil && call.SiteRef == 0 && call.Meta.SiteRef > 0 {
				// Try to resolve by siteRef first
				if call.System != nil && call.System.Sites != nil {
					if site, ok := call.System.Sites.GetSiteByRef(call.Meta.SiteRef); ok {
						call.SiteRef = site.SiteRef
						call.Meta.SiteId = site.Id
						call.Meta.SiteLabel = site.Label
					}
				}
			}

			// Use a non-blocking send to avoid deadlocks
			select {
			case api.Controller.Ingest <- call:
			default:
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("Server busy, please try again\n"))
				return
			}

		} else {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write(msg)
			return
		}

	} else {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write(msg)
		return
	}

	w.Write([]byte("Call imported successfully.\n"))
}

func (api *Api) TrunkRecorderCallUploadHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var (
			call = NewCall()
			key  string
		)

		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			api.exitWithError(w, http.StatusBadRequest, "Invalid content-type")
			return
		}

		if !strings.HasPrefix(mediaType, "multipart/") {
			api.exitWithError(w, http.StatusBadRequest, "Not a multipart content")
			return
		}

		mr := multipart.NewReader(r.Body, params["boundary"])

		parts := map[*multipart.Part][]byte{}

		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			} else if err != nil {
				api.exitWithError(w, http.StatusExpectationFailed, fmt.Sprintf("multipart: %s", err.Error()))
				return
			}

			b, err := io.ReadAll(p)
			if err != nil {
				api.exitWithError(w, http.StatusExpectationFailed, fmt.Sprintf("ioread: %s", err.Error()))
				return
			}

			switch p.FormName() {
			case "key":
				key = string(b)
			case "meta":
				if err := ParseTrunkRecorderMeta(call, b); err != nil {
					api.exitWithError(w, http.StatusExpectationFailed, "Invalid call data")
					return
				}
			default:
				parts[p] = b
			}
		}

		for p, b := range parts {
			ParseMultipartContent(call, p, b)
		}

		if ok, err := call.IsValid(); ok {
			api.HandleCall(key, call, w)

		} else {
			api.exitWithError(w, http.StatusExpectationFailed, fmt.Sprintf("Incomplete call data: %s\n", err.Error()))
		}

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("Unsupported method\n"))
	}
}

func (api *Api) exitWithError(w http.ResponseWriter, status int, message string) {
	api.Controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("api: %s", message))

	w.WriteHeader(status)
	w.Write([]byte(fmt.Sprintf("%s\n", message)))
}

// Helper function to generate support button HTML
func getSupportButton(supportEmail string) string {
	if supportEmail != "" {
		return `<a href="mailto:` + supportEmail + `" class="button secondary">Contact Support</a>`
	}
	return `<p class="no-support">The owner of this server has not provided email support.</p>`
}

// User registration handler
func (api *Api) UserRegisterHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if !api.Controller.Options.UserRegistrationEnabled {
		api.exitWithError(w, http.StatusForbidden, "User registration is disabled")
		return
	}

	var request struct {
		Email            string `json:"email"`
		Password         string `json:"password"`
		FirstName        string `json:"firstName"`
		LastName         string `json:"lastName"`
		ZipCode          string `json:"zipCode"`
		RegistrationCode string `json:"registrationCode"` // Deprecated: use accessCode
		InvitationCode   string `json:"invitationCode"`   // Deprecated: use accessCode
		AccessCode       string `json:"accessCode"`       // Unified field for both invitation and registration codes
		TurnstileToken   string `json:"turnstile_token"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Backward compatibility: if accessCode is provided, it takes precedence
	// Otherwise, use the old separate fields
	if request.AccessCode != "" {
		// Try to determine if it's an invitation code or registration code
		// First check if it's an invitation code (they take precedence)
		var invitationExists bool
		err := api.Controller.Database.Sql.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM "userInvitations" WHERE "code" = $1)`,
			request.AccessCode,
		).Scan(&invitationExists)

		if err == nil && invitationExists {
			request.InvitationCode = request.AccessCode
		} else {
			// Otherwise, treat it as a registration code
			request.RegistrationCode = request.AccessCode
		}
	}

	// Validate input
	if request.Email == "" || request.Password == "" || request.FirstName == "" || request.LastName == "" || request.ZipCode == "" {
		api.exitWithError(w, http.StatusBadRequest, "All fields are required")
		return
	}

	// Turnstile verification (mobile apps and invitation-based registrations are exempt)
	// Invitation codes are already validated via email, so CAPTCHA is redundant
	if api.Controller.Options.TurnstileEnabled && request.InvitationCode == "" {
		clientIP := GetRemoteAddr(r)
		valid, err := api.verifyTurnstile(request.TurnstileToken, clientIP, r)
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("CAPTCHA verification error: %v", err))
			return
		}
		if !valid {
			api.exitWithError(w, http.StatusForbidden, "CAPTCHA verification failed. Please try again.")
			return
		}
	}

	// Validate email format
	if err := ValidateEmail(request.Email); err != nil {
		api.exitWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Normalize email to lowercase
	request.Email = NormalizeEmail(request.Email)

	// Validate password strength
	if err := ValidatePassword(request.Password); err != nil {
		api.exitWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Check if user already exists (case-insensitive)
	if existingUser := api.Controller.Users.GetUserByEmail(request.Email); existingUser != nil {
		api.exitWithError(w, http.StatusConflict, "User already exists")
		return
	}

	// Determine which group to assign user to
	var targetGroup *UserGroup
	var regCode *RegistrationCode
	var invitationId int64

	// Check if invitation code is provided (takes precedence over registration code)
	if request.InvitationCode != "" {
		// Validate invitation code
		var invitation struct {
			Id          int64
			Email       string
			UserGroupId uint64
			Status      string
			ExpiresAt   int64
			UsedAt      sql.NullInt64
		}

		err := api.Controller.Database.Sql.QueryRow(
			`SELECT "userInvitationId", "email", "userGroupId", "status", "expiresAt", "usedAt" 
			 FROM "userInvitations" WHERE "code" = $1`,
			request.InvitationCode,
		).Scan(&invitation.Id, &invitation.Email, &invitation.UserGroupId, &invitation.Status, &invitation.ExpiresAt, &invitation.UsedAt)

		if err == sql.ErrNoRows {
			api.exitWithError(w, http.StatusBadRequest, "Invalid invitation code")
			return
		}
		if err != nil {
			log.Printf("Error validating invitation: %v", err)
			api.exitWithError(w, http.StatusInternalServerError, "Failed to validate invitation")
			return
		}

		// Check if invitation is already used
		if invitation.UsedAt.Valid && invitation.UsedAt.Int64 > 0 {
			api.exitWithError(w, http.StatusBadRequest, "Invitation has already been used")
			return
		}

		// Check if invitation is expired
		if invitation.ExpiresAt > 0 && time.Now().Unix() > invitation.ExpiresAt {
			api.exitWithError(w, http.StatusBadRequest, "Invitation has expired")
			return
		}

		// Check if invitation status is valid
		if invitation.Status != "pending" {
			api.exitWithError(w, http.StatusBadRequest, "Invitation is not valid")
			return
		}

		// Verify email matches (if invitation has email)
		if invitation.Email != "" && invitation.Email != request.Email {
			api.exitWithError(w, http.StatusBadRequest, "Email does not match invitation")
			return
		}

		// Get group from invitation
		targetGroup = api.Controller.UserGroups.Get(invitation.UserGroupId)
		if targetGroup == nil {
			api.exitWithError(w, http.StatusBadRequest, "Invalid invitation - group not found")
			return
		}

		invitationId = invitation.Id
	} else if api.Controller.Options.PublicRegistrationEnabled {
		// Check if public registration is enabled and has a public group
		publicGroup := api.Controller.UserGroups.GetPublicRegistrationGroup()
		if publicGroup != nil {
			// Public registration is enabled and group exists
			// Check if code is required based on mode
			if api.Controller.Options.PublicRegistrationMode == "codes" ||
				(api.Controller.Options.PublicRegistrationMode == "both" && request.RegistrationCode != "") {
				// Code is required or provided
				if request.RegistrationCode == "" {
					api.exitWithError(w, http.StatusBadRequest, "Registration code is required")
					return
				}
				// Validate code
				var err error
				regCode, err = api.Controller.RegistrationCodes.Validate(request.RegistrationCode)
				if err != nil {
					api.exitWithError(w, http.StatusBadRequest, err.Error())
					return
				}
				targetGroup = api.Controller.UserGroups.Get(regCode.UserGroupId)
				if targetGroup == nil {
					api.exitWithError(w, http.StatusBadRequest, "Invalid registration code")
					return
				}
			} else {
				// No code required, use public group
				targetGroup = publicGroup
			}
		} else {
			// Public registration enabled but no public group - code is required
			if request.RegistrationCode == "" {
				api.exitWithError(w, http.StatusBadRequest, "Registration code is required")
				return
			}
			// Validate code
			var err error
			regCode, err = api.Controller.RegistrationCodes.Validate(request.RegistrationCode)
			if err != nil {
				api.exitWithError(w, http.StatusBadRequest, err.Error())
				return
			}
			targetGroup = api.Controller.UserGroups.Get(regCode.UserGroupId)
			if targetGroup == nil {
				api.exitWithError(w, http.StatusBadRequest, "Invalid registration code")
				return
			}
		}
	} else {
		// Public registration disabled - code is required
		if request.RegistrationCode == "" {
			api.exitWithError(w, http.StatusBadRequest, "Registration code is required")
			return
		}
		// Validate code
		var err error
		regCode, err = api.Controller.RegistrationCodes.Validate(request.RegistrationCode)
		if err != nil {
			api.exitWithError(w, http.StatusBadRequest, err.Error())
			return
		}
		targetGroup = api.Controller.UserGroups.Get(regCode.UserGroupId)
		if targetGroup == nil {
			api.exitWithError(w, http.StatusBadRequest, "Invalid registration code")
			return
		}
	}

	// Check max users limit for the group
	// This check is enforced regardless of registration code maxUses setting
	// Even if a code has unlimited uses (maxUses = 0), the group's maxUsers limit still applies
	if targetGroup.MaxUsers > 0 {
		currentUserCount := api.Controller.UserGroups.GetUserCount(targetGroup.Id, api.Controller.Users)
		if currentUserCount >= targetGroup.MaxUsers {
			api.exitWithError(w, http.StatusForbidden, fmt.Sprintf("Group has reached maximum user limit of %d", targetGroup.MaxUsers))
			return
		}
	}

	// Create new user
	user := &User{
		Email:           request.Email,
		FirstName:       request.FirstName,
		LastName:        request.LastName,
		ZipCode:         request.ZipCode,
		UserGroupId:     targetGroup.Id,
		ConnectionLimit: targetGroup.ConnectionLimit, // Inherit group's connection limit
		CreatedAt:       fmt.Sprintf("%d", time.Now().Unix()),
	}

	if err := user.HashPassword(request.Password); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to hash password")
		return
	}

	if err := user.GenerateVerificationToken(); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to generate verification token")
		return
	}

	// Create Stripe customer if Stripe is enabled AND group billing is enabled
	if api.Controller.Options.StripePaywallEnabled && api.Controller.Options.StripeSecretKey != "" && targetGroup.BillingEnabled {
		stripe.Key = api.Controller.Options.StripeSecretKey

		// Check if group uses shared customer ID for admins
		if targetGroup.BillingMode == "group_admin" && user.IsGroupAdmin {
			// Use shared customer ID for group admins
			sharedCustomerId, err := api.getOrCreateGroupSharedCustomerId(targetGroup)
			if err != nil {
				log.Printf("Failed to get/create shared customer ID for group %d: %v", targetGroup.Id, err)
				// Continue with user creation even if Stripe customer creation fails
			} else {
				user.StripeCustomerId = sharedCustomerId
				log.Printf("Assigned shared customer ID %s to new group admin %s", sharedCustomerId, request.Email)
			}
		} else {
			// Individual customer ID for non-admin users or when billing mode is "all_users"
			params := &stripe.CustomerParams{
				Email: stripe.String(request.Email),
				Name:  stripe.String(request.FirstName + " " + request.LastName),
			}

			customer, err := customer.New(params)
			if err != nil {
				log.Printf("Failed to create Stripe customer: %v", err)
				// Continue with user creation even if Stripe customer creation fails
			} else {
				user.StripeCustomerId = customer.ID
				log.Printf("Created Stripe customer %s for user %s", customer.ID, request.Email)
			}
		}
	}

	// Save new user directly to database
	if err := api.Controller.Users.SaveNewUser(user, api.Controller.Database); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to save user")
		return
	}

	// Sync config to file if enabled
	api.Controller.SyncConfigToFile()

	// Handle billing setup for new users in billing-enabled groups
	if targetGroup.BillingEnabled {
		if targetGroup.BillingMode == "group_admin" && !user.IsGroupAdmin {
			// For non-admin users in admin-managed billing groups, sync subscription status from admin
			syncedFromAdmin := false
			allUsers := api.Controller.Users.GetAllUsers()
			for _, admin := range allUsers {
				if admin.UserGroupId == targetGroup.Id && admin.IsGroupAdmin && admin.SubscriptionStatus == "active" {
					// Sync from this admin
					user.SubscriptionStatus = admin.SubscriptionStatus
					user.PinExpiresAt = admin.PinExpiresAt
					api.Controller.Users.Update(user)
					api.Controller.Users.Write(api.Controller.Database)
					// Sync config to file if enabled
					api.Controller.SyncConfigToFile()
					log.Printf("Synced subscription status from admin %s to new user %s", admin.Email, user.Email)
					syncedFromAdmin = true
					break
				}
			}

			// If no active admin found, expire PIN immediately - user needs admin to subscribe
			if !syncedFromAdmin {
				user.SubscriptionStatus = "incomplete"
				user.PinExpiresAt = uint64(time.Now().Unix() - 86400) // Set to 1 day ago to ensure it's expired
				api.Controller.Users.Update(user)
				api.Controller.Users.Write(api.Controller.Database)
				// Sync config to file if enabled
				api.Controller.SyncConfigToFile()
				log.Printf("No active admin found - set PIN to expire (1 day ago) for new user %s in admin-managed billing group", user.Email)
			}
		} else if targetGroup.BillingMode == "all_users" || (targetGroup.BillingMode == "group_admin" && user.IsGroupAdmin) {
			// For all_users mode OR group admins in admin-managed mode, they need to subscribe
			// Expire PIN immediately - no access until they subscribe
			user.SubscriptionStatus = "incomplete"
			user.PinExpiresAt = uint64(time.Now().Unix() - 86400) // Set to 1 day ago to ensure it's expired
			api.Controller.Users.Update(user)
			api.Controller.Users.Write(api.Controller.Database)
			// Sync config to file if enabled
			api.Controller.SyncConfigToFile()
			log.Printf("Set PIN to expire (1 day ago) for new user %s in billing-enabled group (mode: %s, isAdmin: %v) - must subscribe to gain access", user.Email, targetGroup.BillingMode, user.IsGroupAdmin)
		}
	}

	// Mark registration code as used if one was provided
	if regCode != nil {
		if err := api.Controller.RegistrationCodes.Use(request.RegistrationCode, api.Controller.Database); err != nil {
			log.Printf("Warning: Failed to mark registration code as used: %v", err)
		}
	}

	// Mark invitation as used if one was provided
	if invitationId > 0 {
		usedAt := time.Now().Unix()
		_, err := api.Controller.Database.Sql.Exec(
			`UPDATE "userInvitations" SET "usedAt" = $1, "status" = 'used' WHERE "userInvitationId" = $2`,
			usedAt, invitationId,
		)
		if err != nil {
			log.Printf("Warning: Failed to mark invitation as used: %v", err)
		}
	}

	// Send verification email
	if api.Controller.Options.EmailServiceEnabled {
		if err := api.Controller.EmailService.SendVerificationEmail(user); err != nil {
			api.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("Failed to send verification email: %v", err))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":           "User registered successfully. Please check your email for verification.",
		"verificationToken": user.VerificationToken,
		"pin":               user.Pin,
	})
}

// User login handler
func (api *Api) UserLoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var request struct {
		Email          string `json:"email"`
		Password       string `json:"password"`
		TurnstileToken string `json:"turnstile_token"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Validate input
	if request.Email == "" || request.Password == "" {
		api.exitWithError(w, http.StatusBadRequest, "Email and password are required")
		return
	}

	// Validate email format
	if err := ValidateEmail(request.Email); err != nil {
		api.exitWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Normalize email to lowercase for case-insensitive login
	request.Email = NormalizeEmail(request.Email)

	// Get client IP for login attempt tracking
	clientIP := GetRemoteAddr(r)

	// Turnstile verification (mobile apps are exempt)
	if api.Controller.Options.TurnstileEnabled {
		valid, err := api.verifyTurnstile(request.TurnstileToken, clientIP, r)
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("CAPTCHA verification error: %v", err))
			return
		}
		if !valid {
			api.exitWithError(w, http.StatusForbidden, "CAPTCHA verification failed. Please try again.")
			return
		}
	}

	// Find user
	user := api.Controller.Users.GetUserByEmail(request.Email)
	if user == nil {
		// Record failed attempt
		api.Controller.LoginAttemptTracker.RecordFailedAttempt(clientIP)
		api.exitWithError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	// Verify password
	if !user.VerifyPassword(request.Password) {
		// Record failed attempt
		api.Controller.LoginAttemptTracker.RecordFailedAttempt(clientIP)
		api.exitWithError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	// Login successful - reset failed attempts
	api.Controller.LoginAttemptTracker.RecordSuccess(clientIP)

	// Allow login even if not verified - email verification is optional
	if !user.Verified {
		log.Printf("User %s logged in without email verification", user.Email)
	}

	// Note: We don't check subscription status here - users should be able to log in
	// and see the checkout screen if they need to subscribe. Subscription checks happen
	// when they try to access the service content (calls, etc.)

	// Update last login timestamp
	user.UpdateLastLogin()
	api.Controller.Users.Update(user)
	api.Controller.Users.Write(api.Controller.Database)

	// Check if user needs subscription
	needsSubscription := false
	if api.Controller.Options.StripePaywallEnabled {
		// Check if user's group requires billing
		userGroup := api.Controller.UserGroups.Get(user.UserGroupId)
		if userGroup != nil && userGroup.BillingEnabled {
			// Check subscription status based on billing mode
			if userGroup.BillingMode == "group_admin" && user.IsGroupAdmin {
				// For group admin mode, check if any admin has active subscription
				// For now, check this user's subscription status
				// TODO: Could check if any admin in group has active subscription
				needsSubscription = user.SubscriptionStatus != "active"
			} else {
				// For all_users mode, each user needs their own subscription
				needsSubscription = user.SubscriptionStatus != "active"
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Login successful",
		"user": map[string]interface{}{
			"id":                 user.Id,
			"email":              user.Email,
			"pin":                user.Pin,
			"subscriptionStatus": user.SubscriptionStatus,
			"needsSubscription":  needsSubscription,
		},
	})
}

// RequestPasswordResetHandler handles password reset requests
func (api *Api) RequestPasswordResetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var request struct {
		Email string `json:"email"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Validate input
	if request.Email == "" {
		api.exitWithError(w, http.StatusBadRequest, "Email is required")
		return
	}

	// Validate email format
	if err := ValidateEmail(request.Email); err != nil {
		api.exitWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Find user
	user := api.Controller.Users.GetUserByEmail(request.Email)
	if user == nil {
		// Don't reveal if user exists or not for security
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "If an account with that email exists, a password reset code has been sent.",
		})
		return
	}

	// Check if user is verified
	if !user.Verified {
		api.exitWithError(w, http.StatusForbidden, "Account not verified. Please verify your email first.")
		return
	}

	// Generate reset code
	resetCode, err := user.GenerateResetCode()
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to generate reset code")
		return
	}

	// Update user with reset code
	api.Controller.Users.Update(user)
	api.Controller.Users.Write(api.Controller.Database)

	// Send reset code via email
	if api.Controller.Options.EmailServiceEnabled {
		if err := api.Controller.EmailService.SendPasswordResetEmail(user, resetCode); err != nil {
			log.Printf("Failed to send password reset email: %v", err)
			// Don't fail the request, but log the error
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "If an account with that email exists, a password reset code has been sent.",
	})
}

// ResetPasswordHandler handles password reset with verification code
func (api *Api) ResetPasswordHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var request struct {
		Email       string `json:"email"`
		Code        string `json:"code"`
		NewPassword string `json:"newPassword"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Validate input
	if request.Email == "" || request.Code == "" || request.NewPassword == "" {
		api.exitWithError(w, http.StatusBadRequest, "Email, code, and new password are required")
		return
	}

	// Validate password strength
	if err := ValidatePassword(request.NewPassword); err != nil {
		api.exitWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Find user
	user := api.Controller.Users.GetUserByEmail(request.Email)
	if user == nil {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid email or code")
		return
	}

	// Verify reset code
	if !user.VerifyResetCode(request.Code) {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid or expired code")
		return
	}

	// Set new password
	user.SetPassword(request.NewPassword)

	// Clear reset code
	user.ResetCode = ""
	user.ResetCodeExpires = 0

	// Update user
	api.Controller.Users.Update(user)
	api.Controller.Users.Write(api.Controller.Database)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Password reset successful",
	})
}

// User verification handler
func (api *Api) UserVerifyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	token := r.URL.Query().Get("token")

	// For POST requests, also check the request body for the token
	if r.Method == http.MethodPost && token == "" {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if bodyToken, exists := body["token"]; exists {
				token = bodyToken
			}
		}
	}

	// For GET requests, redirect to the main app with the token
	if r.Method == http.MethodGet {
		// Set headers to prevent caching and ensure clean redirect
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		if token == "" {
			// Redirect to home page if no token
			scheme, host := getSchemeAndHost(r)
			baseURL := fmt.Sprintf("%s://%s", scheme, host)
			redirectURL := baseURL + "/"
			http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
			return
		}
		// Redirect to main app with token as query parameter
		// Use absolute URL to ensure proper base path context
		// Check reverse proxy headers for correct scheme/host
		scheme, host := getSchemeAndHost(r)
		baseURL := fmt.Sprintf("%s://%s", scheme, host)
		redirectURL := baseURL + "/?verify=" + token
		// Use StatusTemporaryRedirect (307) to preserve method and ensure clean redirect
		http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
		return
	}

	// For POST requests, handle verification logic
	if token == "" {
		// Return a nice HTML error page for missing tokens
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadRequest)
		html := `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Invalid Link - ThinLine Radio</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: linear-gradient(135deg, #ffa726 0%, #ff7043 100%);
            margin: 0;
            padding: 0;
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
        }
        .container {
            background: white;
            border-radius: 12px;
            padding: 40px;
            box-shadow: 0 20px 40px rgba(0,0,0,0.1);
            text-align: center;
            max-width: 500px;
            width: 90%;
        }
        .warning-icon {
            font-size: 64px;
            color: #ff9800;
            margin-bottom: 20px;
        }
        h1 {
            color: #333;
            margin-bottom: 16px;
            font-size: 28px;
            font-weight: 600;
        }
        p {
            color: #666;
            font-size: 16px;
            line-height: 1.6;
            margin-bottom: 30px;
        }
        .button {
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            padding: 12px 24px;
            border: none;
            border-radius: 6px;
            font-size: 16px;
            font-weight: 500;
            text-decoration: none;
            display: inline-block;
            transition: transform 0.2s;
        }
        .button:hover {
            transform: translateY(-2px);
        }
        .footer {
            margin-top: 30px;
            padding-top: 20px;
            border-top: 1px solid #eee;
            color: #999;
            font-size: 14px;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="warning-icon">⚠️</div>
        <h1>Invalid Verification Link</h1>
        <p>This verification link is missing required information. Please check your email for the complete verification link.</p>
        <a href="/" class="button">Go to ThinLine Radio</a>
        <div class="footer">
            <p>📻 ThinLine Radio - Radio Scanner Application</p>
        </div>
    </div>
</body>
</html>`
		w.Write([]byte(html))
		return
	}

	// Find user by verification token
	var user *User
	for _, u := range api.Controller.Users.users {
		if u.VerificationToken == token {
			user = u
			break
		}
	}

	if user == nil {
		// Return a nice HTML error page for invalid tokens
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)

		// Get branding from options
		branding := "ThinLine Radio"
		if api.Controller.Options.Branding != "" {
			branding = api.Controller.Options.Branding
		}

		// Get support button HTML
		supportButton := getSupportButton(api.Controller.Options.Email)

		html := fmt.Sprintf(`
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Verification Failed - %s</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: #f5f5f5;
            margin: 0;
            padding: 0;
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
        }
        .container {
            background: white;
            border-radius: 8px;
            padding: 40px;
            box-shadow: 0 2px 8px rgba(0,0,0,0.1);
            text-align: center;
            max-width: 500px;
            width: 90%%;
            border: 1px solid #e0e0e0;
        }
        .error-icon {
            font-size: 64px;
            color: #f44336;
            margin-bottom: 20px;
        }
        h1 {
            color: #333;
            margin-bottom: 16px;
            font-size: 28px;
            font-weight: 400;
        }
        p {
            color: #666;
            font-size: 16px;
            line-height: 1.6;
            margin-bottom: 30px;
        }
        .button {
            background: #424242;
            color: white;
            padding: 12px 24px;
            border: none;
            border-radius: 4px;
            font-size: 16px;
            font-weight: 500;
            text-decoration: none;
            display: inline-block;
            transition: background-color 0.2s;
            margin: 0 8px;
        }
        .button:hover {
            background: #616161;
        }
        .button.secondary {
            background: #666;
        }
        .button.secondary:hover {
            background: #757575;
        }
        .footer {
            margin-top: 30px;
            padding-top: 20px;
            border-top: 1px solid #e0e0e0;
            color: #666;
            font-size: 14px;
        }
        .help-text {
            background: #f8f9fa;
            border: 1px solid #e0e0e0;
            border-radius: 4px;
            padding: 16px;
            margin: 20px 0;
            font-size: 14px;
            color: #666;
        }
        .no-support {
            color: #999;
            font-size: 14px;
            font-style: italic;
            margin: 16px 0;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="error-icon">❌</div>
        <h1>Verification Failed</h1>
        <p>The verification link you clicked is invalid or has expired. This could happen if:</p>
        <div class="help-text">
            • The link has already been used<br>
            • The link has expired (links expire after 24 hours)<br>
            • The link was copied incorrectly
        </div>
        <p>Don't worry! You can request a new verification email.</p>
        <a href="/" class="button">Go to %s</a>
        %s
        <div class="footer">
            <p>📻 %s - Radio Scanner Application</p>
        </div>
    </div>
</body>
</html>`, branding, branding, supportButton, branding)
		w.Write([]byte(html))
		return
	}

	// Check if this is an email change verification (user has pending email in settings)
	var settings map[string]interface{}
	if user.Settings != "" {
		if err := json.Unmarshal([]byte(user.Settings), &settings); err == nil {
			if pendingEmail, ok := settings["pendingEmailChange"].(string); ok && pendingEmail != "" {
				// This is an email change verification - complete the email change
				oldEmail := user.Email
				user.Email = pendingEmail

				// Remove pending email from settings
				delete(settings, "pendingEmailChange")
				settingsJson, err := json.Marshal(settings)
				if err == nil {
					user.Settings = string(settingsJson)
				}

				// Update Stripe customer email if they have a Stripe customer ID
				if user.StripeCustomerId != "" && api.Controller.Options.StripeSecretKey != "" {
					stripe.Key = api.Controller.Options.StripeSecretKey
					_, err := customer.Update(user.StripeCustomerId, &stripe.CustomerParams{
						Email: stripe.String(pendingEmail),
					})
					if err != nil {
						log.Printf("Failed to update Stripe customer email: %v", err)
					} else {
						log.Printf("Updated Stripe customer email from %s to %s", oldEmail, pendingEmail)
					}
				}

				log.Printf("Email change completed: %s -> %s", oldEmail, pendingEmail)
			}
		}
	}

	// Verify user
	user.Verified = true
	user.VerificationToken = ""
	api.Controller.Users.Update(user)
	api.Controller.Users.Write(api.Controller.Database)

	// Sync config to file if enabled
	api.Controller.SyncConfigToFile()

	// Return JSON response for API calls
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":  "Email verified successfully",
		"verified": true,
		"email":    user.Email,
	})
}

// Resend verification email handler
func (api *Api) UserResendVerificationHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var request struct {
		Email string `json:"email"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if request.Email == "" {
		api.exitWithError(w, http.StatusBadRequest, "Email is required")
		return
	}

	// Find user
	user := api.Controller.Users.GetUserByEmail(request.Email)
	if user == nil {
		api.exitWithError(w, http.StatusNotFound, "User not found")
		return
	}

	if user.Verified {
		api.exitWithError(w, http.StatusBadRequest, "Account already verified")
		return
	}

	// Generate new verification token
	if err := user.GenerateVerificationToken(); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to generate verification token")
		return
	}

	// Update user
	api.Controller.Users.Update(user)
	api.Controller.Users.Write(api.Controller.Database)

	// Send verification email
	if api.Controller.Options.EmailServiceEnabled {
		if err := api.Controller.EmailService.SendVerificationEmail(user); err != nil {
			api.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("Failed to send verification email: %v", err))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Verification email sent",
	})
}

// Stripe webhook handler
func (api *Api) StripeWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	// Get the Stripe signature from headers
	stripeSignature := r.Header.Get("Stripe-Signature")
	if stripeSignature == "" {
		api.exitWithError(w, http.StatusBadRequest, "Missing Stripe signature")
		return
	}

	// Verify the webhook signature
	webhookSecret := api.Controller.Options.StripeWebhookSecret
	if webhookSecret == "" {
		api.exitWithError(w, http.StatusInternalServerError, "Webhook secret not configured")
		return
	}

	// Verify the webhook signature (ignore API version mismatch)
	stripeEvent, err := webhook.ConstructEventWithOptions(body, stripeSignature, webhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
	if err != nil {
		log.Printf("Webhook signature verification failed: %v", err)
		api.exitWithError(w, http.StatusBadRequest, "Invalid signature")
		return
	}

	log.Printf("Stripe webhook received: %s", stripeEvent.Type)
	log.Printf("Webhook event ID: %s", stripeEvent.ID)

	// Handle different event types
	switch stripeEvent.Type {
	case "checkout.session.completed":
		log.Printf("Processing checkout.session.completed event")
		api.handleCheckoutSessionCompleted(stripeEvent)
	case "customer.subscription.created", "customer.subscription.updated":
		log.Printf("Processing subscription event: %s", stripeEvent.Type)
		api.handleSubscriptionEvent(stripeEvent.Data.Raw, "active")
	case "customer.subscription.deleted":
		log.Printf("Processing subscription deleted event")
		api.handleSubscriptionEvent(stripeEvent.Data.Raw, "canceled")
	case "invoice.payment_succeeded":
		log.Printf("Processing invoice payment succeeded event")
		api.handleInvoicePaymentSucceeded(stripeEvent.Data.Raw)
	case "invoice.payment_failed":
		log.Printf("Processing invoice payment failed event")
		api.handleInvoicePaymentFailed(stripeEvent.Data.Raw)
	default:
		log.Printf("Unhandled Stripe event type: %s", stripeEvent.Type)
	}

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "success",
	})
}

// calculatePinExpiration calculates the PIN expiration date based on subscription period end,
// adding a 2-day buffer if the subscription is active (has had at least one successful payment),
// and optionally adding grace period for failed payments.
// Returns 0 if no expiration should be set.
func (api *Api) calculatePinExpiration(sub *stripe.Subscription, isFailedPayment bool) uint64 {
	if sub == nil {
		return 0
	}

	// Get CurrentPeriodEnd - check top level first, then items array
	var periodEnd int64 = sub.CurrentPeriodEnd
	if periodEnd == 0 && len(sub.Items.Data) > 0 {
		// CurrentPeriodEnd might be in the subscription items
		// Note: SubscriptionItem doesn't have CurrentPeriodEnd field in Go SDK,
		// but we can try to get it from the raw JSON if needed
		// For now, we'll rely on fetching from Stripe if top-level is missing
		log.Printf("CurrentPeriodEnd not at top level, checking if we need to fetch from Stripe")
	}

	if periodEnd == 0 {
		return 0
	}

	// Check if subscription is active/trialing (meaning they've had at least one successful payment)
	// This includes both first payment and renewals
	hasSuccessfulPayment := sub.Status == "active" || sub.Status == "trialing"

	// If subscription is not active (no successful payment yet), don't add buffer or grace period
	if !hasSuccessfulPayment {
		return uint64(sub.CurrentPeriodEnd)
	}

	// Add 2-day buffer (hard-coded) - applies to all active subscriptions
	expiration := sub.CurrentPeriodEnd + (2 * 24 * 60 * 60) // 2 days in seconds

	// Add grace period if payment failed (only for users who have had successful payment)
	if isFailedPayment {
		gracePeriodDays := api.Controller.Options.StripeGracePeriodDays
		if gracePeriodDays > 0 {
			expiration += int64(gracePeriodDays) * 24 * 60 * 60 // Convert days to seconds
		}
	}

	return uint64(expiration)
}

// Handle subscription events (created, updated, deleted)
func (api *Api) handleSubscriptionEvent(rawData []byte, status string) {
	var subData stripe.Subscription
	err := json.Unmarshal(rawData, &subData)
	if err != nil {
		log.Printf("Error parsing subscription event: %v", err)
		return
	}

	log.Printf("Processing subscription event for customer: %s", subData.Customer.ID)
	log.Printf("Subscription ID: %s, Status: %s", subData.ID, subData.Status)
	log.Printf("Customer email: '%s'", subData.Customer.Email)
	log.Printf("Customer name: '%s'", subData.Customer.Name)

	// Find user by Stripe customer ID
	user := api.Controller.Users.GetUserByStripeCustomerId(subData.Customer.ID)
	if user == nil {
		log.Printf("User not found for Stripe customer ID: %s", subData.Customer.ID)

		// Try to find user by email from customer object
		if subData.Customer.Email != "" {
			log.Printf("Trying to find user by email: %s", subData.Customer.Email)
			user = api.Controller.Users.GetUserByEmail(subData.Customer.Email)
			if user != nil {
				log.Printf("Found user by email, updating with Stripe customer ID")
				user.StripeCustomerId = subData.Customer.ID
			}
		}

		if user == nil {
			log.Printf("User not found for email: %s", subData.Customer.Email)
			log.Printf("Cannot process subscription event - user not found for customer ID: %s", subData.Customer.ID)
			return
		}
	}

	log.Printf("Found user: %s (ID: %d)", user.Email, user.Id)

	// Update user subscription status
	user.StripeSubscriptionId = subData.ID
	user.SubscriptionStatus = status

	// Always update PIN expiration based on subscription period end
	var periodEnd int64 = subData.CurrentPeriodEnd
	var sub *stripe.Subscription = &subData

	// If CurrentPeriodEnd is not at top level, try to get it from items array
	if periodEnd == 0 && len(subData.Items.Data) > 0 {
		// The Go SDK doesn't expose CurrentPeriodEnd in SubscriptionItem,
		// but we can try to extract it from the raw JSON if available
		// For now, we'll fetch from Stripe which will have the complete data
		log.Printf("CurrentPeriodEnd not at top level, will fetch from Stripe if needed")
	}

	if periodEnd == 0 {
		// CurrentPeriodEnd might not be in webhook data, fetch it from Stripe
		log.Printf("CurrentPeriodEnd not in webhook data, fetching subscription %s from Stripe", subData.ID)
		stripe.Key = api.Controller.Options.StripeSecretKey
		if stripe.Key != "" {
			fetchedSub, err := subscription.Get(subData.ID, nil)
			if err == nil && fetchedSub.CurrentPeriodEnd > 0 {
				sub = fetchedSub
				periodEnd = fetchedSub.CurrentPeriodEnd
				log.Printf("Fetched CurrentPeriodEnd from Stripe: %d", periodEnd)
			} else if err != nil {
				log.Printf("Failed to fetch subscription %s: %v", subData.ID, err)
			} else {
				log.Printf("Warning: Subscription %s has no CurrentPeriodEnd set", subData.ID)
			}
		}
	}

	// Update PIN expiration based on subscription status
	if status == "active" || status == "trialing" {
		// For active subscriptions, always apply the 2-day buffer
		// Even if scheduled to cancel at period end, the subscription is still active until then
		user.PinExpiresAt = api.calculatePinExpiration(sub, false)
		if user.PinExpiresAt > 0 {
			if sub.CancelAtPeriodEnd {
				log.Printf("Set PIN expiration to %d (with 2-day buffer) for active subscription scheduled to cancel at period end", user.PinExpiresAt)
			} else {
				log.Printf("Set PIN expiration to %d (Unix timestamp) for status: %s", user.PinExpiresAt, status)
			}
		} else {
			log.Printf("Warning: Subscription status is %s but no period end date found", status)
		}
	} else if status == "canceled" {
		// For canceled subscriptions, check if it was canceled immediately or at period end
		// If canceled immediately (ended_at is set), expire immediately
		// If canceled at period end (ended_at is null), set to CurrentPeriodEnd
		if sub.EndedAt > 0 {
			// Canceled immediately - expire right now
			user.PinExpiresAt = uint64(time.Now().Unix())
			log.Printf("Set PIN expiration to current time (immediate expiration) for immediately canceled subscription")
		} else if periodEnd > 0 {
			// Canceled at period end - set to period end with NO buffer and NO grace period
			user.PinExpiresAt = uint64(periodEnd)
			log.Printf("Set PIN expiration to CurrentPeriodEnd %d (no buffer, no grace period) for canceled-at-period-end subscription", user.PinExpiresAt)
		} else {
			user.PinExpiresAt = 0
			log.Printf("Cleared PIN expiration - canceled subscription has no period end date")
		}
	} else if status == "unpaid" {
		// For unpaid status, check if they've had successful payments before
		// If yes, give them buffer + grace period. If no, expire immediately.
		hasHadSuccessfulPayment := sub.CurrentPeriodStart > 0 && sub.Created > 0 && sub.CurrentPeriodStart != sub.Created

		if hasHadSuccessfulPayment {
			// They've had successful payments before (renewal failed) - give buffer + grace period
			user.PinExpiresAt = api.calculatePinExpiration(sub, true) // true = isFailedPayment
			if user.PinExpiresAt > 0 {
				log.Printf("Set PIN expiration to %d (with buffer + grace period) for unpaid subscription (had successful payments before)", user.PinExpiresAt)
			}
		} else {
			// No successful payments before - expire immediately (set to current time)
			user.PinExpiresAt = uint64(time.Now().Unix())
			log.Printf("Set PIN expiration to current time (immediate expiration) for unpaid subscription (no successful payments before)")
		}
	} else if status == "incomplete_expired" {
		// Incomplete expired means initial payment never completed - expire immediately
		user.PinExpiresAt = uint64(time.Now().Unix())
		log.Printf("Set PIN expiration to current time (immediate expiration) for incomplete_expired subscription")
	} else {
		// For other statuses (past_due, incomplete, etc.), still set expiration if we have it
		user.PinExpiresAt = api.calculatePinExpiration(sub, false)
		if user.PinExpiresAt > 0 {
			log.Printf("Set PIN expiration to %d (Unix timestamp) for status: %s", user.PinExpiresAt, status)
		}
	}

	// Save changes to database
	api.Controller.Users.Update(user)
	if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
		log.Printf("Failed to update user subscription status: %v", err)
		return
	}

	// Sync config to file if enabled
	api.Controller.SyncConfigToFile()

	// If this is an admin in an admin-managed billing group, sync subscription status to all group users
	// This should happen for ALL webhook events (active, canceled, past_due, unpaid, etc.)
	if user.IsGroupAdmin {
		api.syncGroupAdminSubscriptionToAllUsers(user)
	}

	log.Printf("Updated user %s subscription status to: %s", user.Email, status)
}

// Handle successful invoice payment
func (api *Api) handleInvoicePaymentSucceeded(rawData []byte) {
	var invoice stripe.Invoice
	err := json.Unmarshal(rawData, &invoice)
	if err != nil {
		log.Printf("Error parsing invoice event: %v", err)
		return
	}

	log.Printf("Processing invoice payment succeeded for customer: %s", invoice.Customer.ID)

	// Find user by Stripe customer ID
	user := api.Controller.Users.GetUserByStripeCustomerId(invoice.Customer.ID)
	if user == nil {
		log.Printf("User not found for Stripe customer ID: %s", invoice.Customer.ID)

		// Try to find user by email from customer object
		if invoice.Customer.Email != "" {
			log.Printf("Trying to find user by email: %s", invoice.Customer.Email)
			user = api.Controller.Users.GetUserByEmail(invoice.Customer.Email)
			if user != nil {
				log.Printf("Found user by email, updating with Stripe customer ID")
				user.StripeCustomerId = invoice.Customer.ID
			}
		}

		if user == nil {
			log.Printf("User not found for email: %s", invoice.Customer.Email)
			return
		}
	}

	log.Printf("Found user: %s (ID: %d)", user.Email, user.Id)

	// Update subscription status to active
	user.SubscriptionStatus = "active"

	// Always update PIN expiration - fetch subscription to get period end date
	if invoice.Subscription != nil && invoice.Subscription.ID != "" {
		// Set Stripe API key
		stripe.Key = api.Controller.Options.StripeSecretKey
		if stripe.Key != "" {
			sub, err := subscription.Get(invoice.Subscription.ID, nil)
			if err == nil {
				user.PinExpiresAt = api.calculatePinExpiration(sub, false)
				if user.PinExpiresAt > 0 {
					log.Printf("Set PIN expiration to %d (Unix timestamp) from successful invoice payment", user.PinExpiresAt)
				} else {
					log.Printf("Warning: Subscription %s has no CurrentPeriodEnd set", invoice.Subscription.ID)
				}
			} else {
				log.Printf("Failed to fetch subscription %s: %v", invoice.Subscription.ID, err)
			}
		}
	}

	// Save changes to database
	api.Controller.Users.Update(user)
	if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
		log.Printf("Failed to update user subscription status: %v", err)
		return
	}

	// If this is an admin in an admin-managed billing group, sync subscription status to all group users
	if user.IsGroupAdmin {
		api.syncGroupAdminSubscriptionToAllUsers(user)
	}

	log.Printf("Updated user %s subscription status to active after successful payment", user.Email)
}

// Handle failed invoice payment
func (api *Api) handleInvoicePaymentFailed(rawData []byte) {
	var invoice stripe.Invoice
	err := json.Unmarshal(rawData, &invoice)
	if err != nil {
		log.Printf("Error parsing invoice event: %v", err)
		return
	}

	log.Printf("Processing invoice payment failed for customer: %s", invoice.Customer.ID)

	// Find user by Stripe customer ID
	user := api.Controller.Users.GetUserByStripeCustomerId(invoice.Customer.ID)
	if user == nil {
		log.Printf("User not found for Stripe customer ID: %s", invoice.Customer.ID)

		// Try to find user by email from customer object
		if invoice.Customer.Email != "" {
			log.Printf("Trying to find user by email: %s", invoice.Customer.Email)
			user = api.Controller.Users.GetUserByEmail(invoice.Customer.Email)
			if user != nil {
				log.Printf("Found user by email, updating with Stripe customer ID")
				user.StripeCustomerId = invoice.Customer.ID
			}
		}

		if user == nil {
			log.Printf("User not found for email: %s", invoice.Customer.Email)
			log.Printf("Cannot process invoice payment event - user not found for customer ID: %s", invoice.Customer.ID)
			return
		}
	}

	log.Printf("Found user: %s (ID: %d)", user.Email, user.Id)

	// Update subscription status to past_due
	user.SubscriptionStatus = "past_due"

	// Still update PIN expiration if subscription exists (subscription might still be active but past_due)
	// This is a failed payment, so we'll add grace period if user has had successful payment or trial
	if invoice.Subscription != nil && invoice.Subscription.ID != "" {
		// Set Stripe API key
		stripe.Key = api.Controller.Options.StripeSecretKey
		if stripe.Key != "" {
			sub, err := subscription.Get(invoice.Subscription.ID, nil)
			if err == nil {
				log.Printf("Fetched subscription %s with status: %s for failed payment", sub.ID, sub.Status)

				// Check if user had actual successful payments (not just trial)
				// Grace period is only for users who have actually paid before
				// If they only had a trial and payment fails after trial, no grace period
				hadSuccessfulPayment := false

				// Note: When payment fails, status is typically "past_due" or "incomplete_expired", not "active"
				// But check anyway in case of timing edge cases
				if sub.Status == "active" {
					// Status is active means they've had successful payment (shouldn't happen on payment failure, but check anyway)
					hadSuccessfulPayment = true
					log.Printf("Subscription %s is active - user had successful payment", sub.ID)
				} else if sub.TrialEnd > 0 && sub.CurrentPeriodStart > 0 {
					// If they had a trial, check if they've had successful payments after trial ended
					// When trial ends and payment succeeds, CurrentPeriodStart is set to start of paid period
					// When payment fails after trial, CurrentPeriodStart might be close to TrialEnd
					// If CurrentPeriodStart is significantly after TrialEnd (more than billing period length),
					// they've completed at least one full paid billing period
					// Note: This is conservative - requires at least 1 day difference to account for edge cases
					// where CurrentPeriodStart might be set slightly after TrialEnd even on payment failure
					daysSinceTrialEnd := float64(sub.CurrentPeriodStart-sub.TrialEnd) / 86400.0
					if daysSinceTrialEnd > 1.0 {
						// They've been in paid period for more than 1 day - had successful payment
						// This indicates they completed at least one billing period after trial
						hadSuccessfulPayment = true
						log.Printf("Subscription %s has completed paid periods (period start: %d, trial ended: %d, %.2f days after trial) - user had successful payment", sub.ID, sub.CurrentPeriodStart, sub.TrialEnd, daysSinceTrialEnd)
					} else {
						// CurrentPeriodStart is close to or before TrialEnd - no successful payment yet
						// This includes trial users whose first payment failed immediately after trial
						log.Printf("Subscription %s had trial (ended: %d) but no successful payment (period start: %d, %.2f days after trial) - no grace period", sub.ID, sub.TrialEnd, sub.CurrentPeriodStart, daysSinceTrialEnd)
					}
				} else if sub.CurrentPeriodStart > 0 && sub.Created > 0 && sub.CurrentPeriodStart > sub.Created {
					// No trial period, but they've completed at least one billing period (had successful payment)
					// Check if period start is significantly after creation (more than 1 day)
					daysSinceCreation := float64(sub.CurrentPeriodStart-sub.Created) / 86400.0
					if daysSinceCreation > 1.0 {
						hadSuccessfulPayment = true
						log.Printf("Subscription %s has completed billing periods (period start: %d, created: %d, %d days after creation) - user had successful payment", sub.ID, sub.CurrentPeriodStart, sub.Created, int(daysSinceCreation))
					} else {
						log.Printf("Subscription %s period start (%d) is close to creation (%d) - likely no successful payment yet - no grace period", sub.ID, sub.CurrentPeriodStart, sub.Created)
					}
				} else {
					log.Printf("Subscription %s has no indicators of successful payment - no grace period (status: %s)", sub.ID, sub.Status)
				}

				if hadSuccessfulPayment {
					// User had successful payment(s) - give them grace period
					// Note: calculatePinExpiration checks if status is active/trialing, but after payment fails
					// status becomes past_due, so we need to manually calculate with grace period
					if sub.CurrentPeriodEnd > 0 {
						// Add 2-day buffer (same as calculatePinExpiration)
						expiration := int64(sub.CurrentPeriodEnd) + (2 * 24 * 60 * 60) // 2 days in seconds

						// Add grace period for failed payment
						gracePeriodDays := api.Controller.Options.StripeGracePeriodDays
						if gracePeriodDays > 0 {
							expiration += int64(gracePeriodDays) * 24 * 60 * 60 // Convert days to seconds
						}

						user.PinExpiresAt = uint64(expiration)
						log.Printf("Set PIN expiration to %d (Unix timestamp) from failed payment (status: %s, includes 2-day buffer + %d-day grace period) - user had successful payment", user.PinExpiresAt, sub.Status, gracePeriodDays)
					} else {
						// No CurrentPeriodEnd - use grace period from now
						gracePeriodDays := api.Controller.Options.StripeGracePeriodDays
						if gracePeriodDays > 0 {
							user.PinExpiresAt = uint64(time.Now().Unix() + int64(gracePeriodDays)*24*60*60)
							log.Printf("Set PIN expiration to %d (current time + %d-day grace period) from failed payment - user had successful payment but no CurrentPeriodEnd", user.PinExpiresAt, gracePeriodDays)
						} else {
							user.PinExpiresAt = uint64(time.Now().Unix())
							log.Printf("Set PIN expiration to current time (no grace period configured) from failed payment")
						}
					}
				} else {
					// No successful payment - expire immediately (includes trial users who fail first payment)
					user.PinExpiresAt = uint64(time.Now().Unix())
					log.Printf("Set PIN expiration to current time (immediate expiration) for failed payment - user had no successful payment (status: %s)", sub.Status)
				}
			} else {
				log.Printf("Failed to fetch subscription %s: %v", invoice.Subscription.ID, err)
				// If we can't fetch subscription, expire immediately to be safe
				user.PinExpiresAt = uint64(time.Now().Unix())
				log.Printf("Cannot verify subscription status - setting PIN expiration to current time (immediate expiration)")
			}
		} else {
			// No Stripe API key - expire immediately to be safe
			user.PinExpiresAt = uint64(time.Now().Unix())
			log.Printf("Stripe API key not configured - setting PIN expiration to current time (immediate expiration)")
		}
	} else {
		// No subscription - expire immediately
		user.PinExpiresAt = uint64(time.Now().Unix())
		log.Printf("No subscription in invoice - setting PIN expiration to current time (immediate expiration)")
	}

	// Save changes to database
	api.Controller.Users.Update(user)
	if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
		log.Printf("Failed to update user subscription status: %v", err)
		return
	}

	// If this is an admin in an admin-managed billing group, sync subscription status to all group users
	if user.IsGroupAdmin {
		api.syncGroupAdminSubscriptionToAllUsers(user)
	}

	log.Printf("Updated user %s subscription status to past_due after failed payment", user.Email)
}

// Handle checkout session completed
func (api *Api) handleCheckoutSessionCompleted(event stripe.Event) {
	var session stripe.CheckoutSession
	err := json.Unmarshal(event.Data.Raw, &session)
	if err != nil {
		log.Printf("Error parsing checkout session: %v", err)
		return
	}

	log.Printf("Checkout session completed for email: %s", session.CustomerEmail)
	log.Printf("Session ID: %s", session.ID)
	log.Printf("Customer ID: %s", session.Customer.ID)
	log.Printf("Subscription ID: %s", session.Subscription.ID)
	log.Printf("Payment Status: %s", session.PaymentStatus)

	// Find user by email
	user := api.Controller.Users.GetUserByEmail(session.CustomerEmail)
	if user == nil {
		log.Printf("User not found for email: %s", session.CustomerEmail)
		return
	}

	log.Printf("Found user: %s (ID: %d)", user.Email, user.Id)

	// Verify payment and subscription status before activating account
	// checkout.session.completed can fire even when payment fails
	// We need to check both payment_status and subscription status
	var sub *stripe.Subscription

	// Check payment status: "paid" or "no_payment_required" are valid
	// "unpaid" is acceptable only if subscription has a trial period (will be verified below)
	if session.PaymentStatus != "paid" && session.PaymentStatus != "no_payment_required" && session.PaymentStatus != "unpaid" {
		log.Printf("Payment not successful for checkout session %s (PaymentStatus: %s). User account will not be activated.", session.ID, session.PaymentStatus)
		// Update Stripe customer ID if available, but do not activate account
		if session.Customer.ID != "" {
			user.StripeCustomerId = session.Customer.ID
		}
		user.SubscriptionStatus = "incomplete"
		// Expire PIN immediately if payment failed
		user.PinExpiresAt = uint64(time.Now().Unix() - 86400) // Set to 1 day ago to ensure it's expired
		api.Controller.Users.Update(user)
		if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
			log.Printf("Failed to update user after failed payment: %v", err)
		} else {
			log.Printf("Updated user %s: payment failed, account not activated", user.Email)
		}
		return
	}

	// Verify subscription status - this is the critical check
	if session.Subscription.ID != "" {
		// Set Stripe API key
		stripe.Key = api.Controller.Options.StripeSecretKey
		if stripe.Key != "" {
			fetchedSub, err := subscription.Get(session.Subscription.ID, nil)
			if err != nil {
				log.Printf("Failed to fetch subscription %s: %v", session.Subscription.ID, err)
				log.Printf("Cannot verify subscription status. User account will not be activated.")
				// Update Stripe customer ID if available, but do not activate account
				if session.Customer.ID != "" {
					user.StripeCustomerId = session.Customer.ID
				}
				user.SubscriptionStatus = "incomplete"
				user.PinExpiresAt = uint64(time.Now().Unix() - 86400) // Set to 1 day ago to ensure it's expired
				api.Controller.Users.Update(user)
				if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
					log.Printf("Failed to update user: %v", err)
				}
				return
			}
			sub = fetchedSub
			log.Printf("Fetched subscription %s with status: %s", sub.ID, sub.Status)

			// Only activate if subscription is active or trialing (not incomplete, incomplete_expired, past_due, etc.)
			if sub.Status != "active" && sub.Status != "trialing" {
				log.Printf("Subscription %s status is %s (not active or trialing). Payment status: %s. User account will not be activated.", sub.ID, sub.Status, session.PaymentStatus)
				// Update Stripe customer ID and subscription ID, but do not activate account
				user.StripeCustomerId = session.Customer.ID
				user.StripeSubscriptionId = session.Subscription.ID
				user.SubscriptionStatus = string(sub.Status)
				user.PinExpiresAt = uint64(time.Now().Unix() - 86400) // Set to 1 day ago to ensure it's expired
				api.Controller.Users.Update(user)
				if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
					log.Printf("Failed to update user: %v", err)
				} else {
					log.Printf("Updated user %s: subscription status %s, account not activated", user.Email, sub.Status)
				}
				return
			}

			// If payment status is "unpaid" but subscription is trialing, that's valid (trial period)
			if session.PaymentStatus == "unpaid" && sub.Status == "trialing" {
				log.Printf("Subscription %s is in trial period (trialing status). Account will be activated.", sub.ID)
			}
		} else {
			log.Printf("Stripe API key not configured. Cannot verify subscription status. User account will not be activated.")
			if session.Customer.ID != "" {
				user.StripeCustomerId = session.Customer.ID
			}
			user.SubscriptionStatus = "incomplete"
			user.PinExpiresAt = uint64(time.Now().Unix() - 86400)
			api.Controller.Users.Update(user)
			if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
				log.Printf("Failed to update user: %v", err)
			}
			return
		}
	} else {
		// No subscription ID means this shouldn't happen for subscription checkouts
		log.Printf("No subscription ID in checkout session %s. User account will not be activated.", session.ID)
		if session.Customer.ID != "" {
			user.StripeCustomerId = session.Customer.ID
		}
		user.SubscriptionStatus = "incomplete"
		user.PinExpiresAt = uint64(time.Now().Unix() - 86400)
		api.Controller.Users.Update(user)
		if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
			log.Printf("Failed to update user: %v", err)
		}
		return
	}

	// If user already has a subscription, cancel it before setting the new one
	oldSubscriptionId := user.StripeSubscriptionId
	if oldSubscriptionId != "" && oldSubscriptionId != session.Subscription.ID {
		log.Printf("User has existing subscription %s, canceling it before setting new subscription %s", oldSubscriptionId, session.Subscription.ID)
		stripe.Key = api.Controller.Options.StripeSecretKey
		if stripe.Key != "" {
			// Cancel the old subscription immediately (they're switching plans)
			_, err := subscription.Cancel(oldSubscriptionId, nil)
			if err != nil {
				log.Printf("Warning: Failed to cancel old subscription %s for user %s: %v", oldSubscriptionId, user.Email, err)
				// Continue anyway - we'll still set the new subscription
			} else {
				log.Printf("Successfully canceled old subscription %s for user %s", oldSubscriptionId, user.Email)
			}
		}
	}

	// Payment was successful and subscription is active/trialing - activate account
	user.StripeCustomerId = session.Customer.ID
	user.StripeSubscriptionId = session.Subscription.ID
	user.SubscriptionStatus = "active"

	// Update PIN expiration - fetch subscription to get period end date
	if sub != nil {
		user.PinExpiresAt = api.calculatePinExpiration(sub, false)
		if user.PinExpiresAt > 0 {
			log.Printf("Set PIN expiration to %d (Unix timestamp) from checkout", user.PinExpiresAt)
		} else {
			log.Printf("Warning: Subscription %s has no CurrentPeriodEnd set", session.Subscription.ID)
		}
	}

	log.Printf("Updated user with Stripe info - Customer: %s, Subscription: %s, Status: %s",
		user.StripeCustomerId, user.StripeSubscriptionId, user.SubscriptionStatus)

	// Save to database
	if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
		log.Printf("Failed to update user after checkout completion: %v", err)
		return
	}

	// Sync config to file if enabled
	api.Controller.SyncConfigToFile()

	// If this is an admin in an admin-managed billing group, sync subscription status to all group users
	if user.IsGroupAdmin {
		api.syncGroupAdminSubscriptionToAllUsers(user)
	}

	log.Printf("User %s subscription activated after checkout completion", user.Email)
}

// Create Stripe checkout session
func (api *Api) CreateCheckoutSessionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var request struct {
		PriceId    string `json:"priceId"`
		Email      string `json:"email"`
		SuccessUrl string `json:"successUrl"`
		CancelUrl  string `json:"cancelUrl"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Validate input
	if request.Email == "" {
		api.exitWithError(w, http.StatusBadRequest, "Email is required")
		return
	}

	// Set Stripe API key
	stripe.Key = api.Controller.Options.StripeSecretKey

	// Find user by email to get their Stripe customer ID and group
	user := api.Controller.Users.GetUserByEmail(request.Email)
	if user == nil {
		api.exitWithError(w, http.StatusNotFound, "User not found")
		return
	}

	// Get group and determine which price ID to use: group price ID is required
	group := api.Controller.UserGroups.Get(user.UserGroupId)
	if group == nil || !group.BillingEnabled {
		api.exitWithError(w, http.StatusBadRequest, "User is not in a billing-enabled group")
		return
	}

	priceId := request.PriceId
	if priceId == "" {
		api.exitWithError(w, http.StatusBadRequest, "Price ID is required")
		return
	}

	// Validate that the requested price ID is one of the valid pricing options for this group
	pricingOptions := group.GetPricingOptions()
	validPriceId := false
	for _, option := range pricingOptions {
		if option.PriceId == priceId {
			validPriceId = true
			break
		}
	}

	if !validPriceId {
		api.exitWithError(w, http.StatusBadRequest, "Invalid price ID for this group")
		return
	}

	log.Printf("Using price ID %s for user %s (group: %s)", priceId, request.Email, group.Name)

	// Create Stripe Checkout Session
	params := &stripe.CheckoutSessionParams{
		PaymentMethodTypes: stripe.StringSlice([]string{
			"card",
		}),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Price:    stripe.String(priceId),
				Quantity: stripe.Int64(1),
			},
		},
		Mode:       stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		SuccessURL: stripe.String(request.SuccessUrl),
		CancelURL:  stripe.String(request.CancelUrl),
		Locale:     stripe.String("en"),
		AutomaticTax: &stripe.CheckoutSessionAutomaticTaxParams{
			Enabled: stripe.Bool(group.CollectSalesTax),
		},
	}

	// If automatic tax is enabled, configure customer update to save billing address
	if group.CollectSalesTax {
		params.CustomerUpdate = &stripe.CheckoutSessionCustomerUpdateParams{
			Address: stripe.String("auto"),
		}
	}

	// Add trial period if configured for this pricing option
	for _, option := range pricingOptions {
		if option.PriceId == priceId && option.TrialDays > 0 {
			params.SubscriptionData = &stripe.CheckoutSessionSubscriptionDataParams{
				TrialPeriodDays: stripe.Int64(int64(option.TrialDays)),
			}
			log.Printf("Adding %d day trial period for price %s", option.TrialDays, priceId)
			break
		}
	}

	// Use existing Stripe customer ID if available, otherwise use email
	if user.StripeCustomerId != "" {
		params.Customer = stripe.String(user.StripeCustomerId)
		log.Printf("Using existing Stripe customer ID: %s for user: %s", user.StripeCustomerId, request.Email)
	} else {
		params.CustomerEmail = stripe.String(request.Email)
		log.Printf("Using customer email for checkout session: %s", request.Email)
	}

	checkoutSession, err := checkoutsession.New(params)
	if err != nil {
		log.Printf("Error creating Stripe checkout session: %v", err)

		// Check if this is a Stripe error about origin address or customer address for automatic tax
		errorMessage := "Failed to create checkout session. Please contact support."
		errStr := err.Error()

		// Check for specific error patterns in the error string
		if strings.Contains(errStr, "origin address") || (strings.Contains(errStr, "automatic tax") && strings.Contains(errStr, "origin")) {
			errorMessage = "Automatic tax requires a valid origin address to be configured in your Stripe dashboard. Please visit https://dashboard.stripe.com/test/settings/tax (test mode) or https://dashboard.stripe.com/settings/tax (live mode) to configure your business address."
		} else if strings.Contains(errStr, "customer_tax_location_invalid") || strings.Contains(errStr, "valid address on the Customer") {
			errorMessage = "Automatic tax requires a customer address. The billing address entered during checkout will be saved automatically."
		} else if _, ok := err.(*stripe.Error); ok {
			// For other Stripe errors, provide the error message
			if errStr != "" {
				errorMessage = fmt.Sprintf("Stripe error: %s", errStr)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": errorMessage,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"checkoutUrl": checkoutSession.URL,
	})
}

// SettingsGetHandler handles GET requests to get user settings (authenticated via PIN in WebSocket)
// This is called via HTTP, but we'll authenticate via the PIN that's stored after WebSocket login
func (api *Api) SettingsGetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Get PIN from query parameter or Authorization header
	pin := r.URL.Query().Get("pin")
	if pin == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			pin = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if pin == "" {
		api.exitWithError(w, http.StatusUnauthorized, "PIN required")
		return
	}

	// Find user by PIN
	user := api.Controller.Users.GetUserByPin(pin)
	if user == nil {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid PIN")
		return
	}

	// Parse settings JSON or return empty object
	var settings map[string]interface{}
	if user.Settings != "" {
		if err := json.Unmarshal([]byte(user.Settings), &settings); err != nil {
			settings = make(map[string]interface{})
		}
	} else {
		settings = make(map[string]interface{})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}

// SettingsSaveHandler handles POST requests to save user settings (authenticated via PIN in WebSocket)
// AlertsHandler handles GET /api/alerts - Get user's alerts
func (api *Api) AlertsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		client := api.getClient(r)
		if client == nil || client.User == nil {
			api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		// Parse since parameter (timestamp in milliseconds)
		var sinceTimestamp int64 = 0
		if since := r.URL.Query().Get("since"); since != "" {
			if v, err := strconv.ParseInt(since, 10, 64); err == nil {
				sinceTimestamp = v
			}
		}

		// Get user's alert preferences first (get all preferences, not just enabled ones)
		// We'll filter by alertEnabled when checking individual alerts
		prefsQuery := fmt.Sprintf(`SELECT "systemId", "talkgroupId", "alertEnabled", "toneAlerts", "keywordAlerts", "toneSetIds", "keywords", "keywordListIds" FROM "userAlertPreferences" WHERE "userId" = %d`, client.User.Id)
		prefsRows, err := api.Controller.Database.Sql.Query(prefsQuery)
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("failed to query preferences: %v", err))
			return
		}
		defer prefsRows.Close()

		// Build preference map for efficient lookup
		type userPref struct {
			systemId       uint64
			talkgroupId    uint64
			alertEnabled   bool
			toneAlerts     bool
			keywordAlerts  bool
			toneSetIds     []string
			keywords       []string
			keywordListIds []uint64
		}
		preferences := make(map[string]*userPref) // Key: "systemId-talkgroupId"

		for prefsRows.Next() {
			var (
				systemId          uint64
				talkgroupId       uint64
				alertEnabled      bool
				toneAlerts        bool
				keywordAlerts     bool
				toneSetIdsRaw     string
				keywordsRaw       string
				keywordListIdsRaw string
			)
			if err := prefsRows.Scan(&systemId, &talkgroupId, &alertEnabled, &toneAlerts, &keywordAlerts, &toneSetIdsRaw, &keywordsRaw, &keywordListIdsRaw); err != nil {
				continue
			}

			key := fmt.Sprintf("%d-%d", systemId, talkgroupId)
			pref := &userPref{
				systemId:       systemId,
				talkgroupId:    talkgroupId,
				alertEnabled:   alertEnabled,
				toneAlerts:     toneAlerts,
				keywordAlerts:  keywordAlerts,
				toneSetIds:     []string{},
				keywords:       []string{},
				keywordListIds: []uint64{},
			}

			// Parse toneSetIds
			if toneSetIdsRaw != "" && toneSetIdsRaw != "[]" {
				json.Unmarshal([]byte(toneSetIdsRaw), &pref.toneSetIds)
			}

			// Parse keywords
			if keywordsRaw != "" && keywordsRaw != "[]" {
				json.Unmarshal([]byte(keywordsRaw), &pref.keywords)
			}

			// Parse keywordListIds
			if keywordListIdsRaw != "" && keywordListIdsRaw != "[]" {
				var listIds []uint64
				json.Unmarshal([]byte(keywordListIdsRaw), &listIds)
				pref.keywordListIds = listIds
			}

			preferences[key] = pref
		}

		// Fetch all alerts (no pagination - client will display in scrollable view)
		// Use a reasonable maximum (10000) to avoid memory issues
		maxAlerts := uint(10000)

		// Query alerts with optional since filter
		// If sinceTimestamp > 0, only fetch alerts created at or after that timestamp
		// Use >= to ensure we don't miss alerts created at the exact timestamp
		whereClause := ""
		if sinceTimestamp > 0 {
			whereClause = fmt.Sprintf(`WHERE a."createdAt" >= %d`, sinceTimestamp)
		}

		// Query all alerts (no userId filter) with system, talkgroup labels, call transcripts, and tone sequence
		query := fmt.Sprintf(`SELECT a."alertId", a."callId", a."systemId", a."talkgroupId", a."alertType", a."toneDetected", a."toneSetId", a."keywordsMatched", a."transcriptSnippet", a."createdAt", s."label" as "systemLabel", s."systemRef" as "systemRef", t."label" as "talkgroupLabel", t."name" as "talkgroupName", c."transcript" as "callTranscript", c."transcriptionStatus" as "callTranscriptionStatus", c."toneSequence" as "callToneSequence", c."timestamp" as "callTimestamp" FROM "alerts" a LEFT JOIN "systems" s ON s."systemId" = a."systemId" LEFT JOIN "talkgroups" t ON t."talkgroupId" = a."talkgroupId" LEFT JOIN "calls" c ON c."callId" = a."callId" %s ORDER BY a."createdAt" DESC LIMIT %d`, whereClause, maxAlerts)
		rows, err := api.Controller.Database.Sql.Query(query)
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("failed to query alerts: %v", err))
			return
		}
		defer rows.Close()

		// Collect all alerts first
		allAlerts := []map[string]any{}
		for rows.Next() {
			var (
				alertId                 uint64
				callId                  uint64
				systemId                uint64
				talkgroupId             uint64
				alertType               string
				toneDetected            bool
				toneSetId               string
				keywordsMatched         string
				transcriptSnippet       string
				createdAt               int64
				systemLabel             sql.NullString
				systemRef               sql.NullInt64
				talkgroupLabel          sql.NullString
				talkgroupName           sql.NullString
				callTranscript          sql.NullString
				callTranscriptionStatus sql.NullString
				callToneSequence        sql.NullString
				callTimestamp           sql.NullInt64
			)

			if err := rows.Scan(&alertId, &callId, &systemId, &talkgroupId, &alertType, &toneDetected, &toneSetId, &keywordsMatched, &transcriptSnippet, &createdAt, &systemLabel, &systemRef, &talkgroupLabel, &talkgroupName, &callTranscript, &callTranscriptionStatus, &callToneSequence, &callTimestamp); err != nil {
				continue
			}

			// Fallback snippet if alert was created before we had one
			snippet := transcriptSnippet
			if snippet == "" && callTranscript.Valid {
				snippet = strings.TrimSpace(callTranscript.String)
				if len(snippet) > 200 {
					snippet = snippet[:200] + "..."
				}
			}

			// Extract matched tone set name(s) from tone sequence JSON
			// If toneSetId is set, ONLY include that specific tone set (one alert per tone set)
			// Otherwise, extract all matched tone sets (for backward compatibility)
			matchedToneSetNames := []string{}
			matchedToneSetName := "" // The specific tone set for this alert, or first one for backward compatibility
			if callToneSequence.Valid && callToneSequence.String != "" && callToneSequence.String != "{}" {
				var toneSeq map[string]any
				if err := json.Unmarshal([]byte(callToneSequence.String), &toneSeq); err == nil {
					// Check for matchedToneSets array (plural - preferred)
					if matchedToneSets, ok := toneSeq["matchedToneSets"].([]any); ok && len(matchedToneSets) > 0 {
						if toneSetId != "" {
							// This alert is for a specific tone set - only find and return that one
							for _, ts := range matchedToneSets {
								if matchedToneSet, ok := ts.(map[string]any); ok {
									if id, ok := matchedToneSet["id"].(string); ok && id == toneSetId {
										if label, ok := matchedToneSet["label"].(string); ok && label != "" {
											matchedToneSetName = label
											matchedToneSetNames = []string{label} // Only this specific tone set
											break
										}
									}
								}
							}
						} else {
							// No specific toneSetId - extract all matched tone sets (backward compatibility)
							for _, ts := range matchedToneSets {
								if matchedToneSet, ok := ts.(map[string]any); ok {
									if label, ok := matchedToneSet["label"].(string); ok && label != "" {
										matchedToneSetNames = append(matchedToneSetNames, label)
									}
								}
							}
							if len(matchedToneSetNames) > 0 {
								matchedToneSetName = matchedToneSetNames[0]
							}
						}
					}
					// Fallback to singular matchedToneSet for backward compatibility
					if len(matchedToneSetNames) == 0 || matchedToneSetName == "" {
						if matchedToneSet, ok := toneSeq["matchedToneSet"].(map[string]any); ok {
							if label, ok := matchedToneSet["label"].(string); ok && label != "" {
								if toneSetId != "" {
									// Check if this is the right tone set
									if id, ok := matchedToneSet["id"].(string); ok && id == toneSetId {
										matchedToneSetName = label
										matchedToneSetNames = []string{label}
									}
								} else {
									matchedToneSetNames = append(matchedToneSetNames, label)
									matchedToneSetName = label
								}
							}
						}
					}
				}
			}

			alertMap := map[string]any{
				"alertId":           alertId,
				"callId":            callId,
				"systemId":          systemId,
				"talkgroupId":       talkgroupId,
				"alertType":         alertType,
				"toneDetected":      toneDetected,
				"keywordsMatched":   keywordsMatched,
				"transcriptSnippet": snippet,
				"createdAt":         createdAt,
			}

			if systemLabel.Valid {
				alertMap["systemLabel"] = systemLabel.String
			}
			if talkgroupLabel.Valid {
				alertMap["talkgroupLabel"] = talkgroupLabel.String
			}
			if talkgroupName.Valid {
				alertMap["talkgroupName"] = talkgroupName.String
			}
			if callTranscript.Valid {
				alertMap["transcript"] = callTranscript.String
			}
			if callTranscriptionStatus.Valid {
				alertMap["transcriptionStatus"] = callTranscriptionStatus.String
			}
			if toneSetId != "" {
				alertMap["toneSetId"] = toneSetId
			}
			if matchedToneSetName != "" {
				alertMap["matchedToneSetName"] = matchedToneSetName // The specific tone set for this alert
			}
			if len(matchedToneSetNames) > 0 {
				alertMap["matchedToneSetNames"] = matchedToneSetNames // All matched tone sets (for backward compatibility)
			}

			// Filter alert based on user preferences only (no access restrictions for alerts)
			prefKey := fmt.Sprintf("%d-%d", systemId, talkgroupId)
			pref, hasPreference := preferences[prefKey]

			if !hasPreference {
				// User has no preference for this system/talkgroup, skip this alert
				continue
			}

			// Check if alerts are enabled for this preference
			if !pref.alertEnabled {
				// User has disabled alerts for this system/talkgroup, skip this alert
				continue
			}

			// Check if alert matches user's preferences
			matchesPreference := false

			if alertType == "tone" {
				// For tone alerts: user must have toneAlerts enabled
				if pref.toneAlerts {
					// If user has specific toneSetIds, check if this alert's toneSetId matches
					if len(pref.toneSetIds) > 0 {
						// User wants specific tone sets - check if this alert's toneSetId is in the list
						for _, userToneSetId := range pref.toneSetIds {
							if userToneSetId == toneSetId {
								matchesPreference = true
								break
							}
						}
					} else {
						// User wants all tone sets
						matchesPreference = true
					}
				}
			} else if alertType == "keyword" {
				// For keyword alerts: user must have keywordAlerts enabled
				if pref.keywordAlerts {
					// Parse alert's keywords
					var alertKeywords []string
					if keywordsMatched != "" && keywordsMatched != "[]" {
						json.Unmarshal([]byte(keywordsMatched), &alertKeywords)
					}

					// Check if any alert keyword matches user's keywords
					matchesKeywords := false
					for _, alertKw := range alertKeywords {
						for _, userKw := range pref.keywords {
							if strings.EqualFold(alertKw, userKw) {
								matchesKeywords = true
								break
							}
						}
						if matchesKeywords {
							break
						}
					}

					// If no direct keyword match, check keyword lists
					if !matchesKeywords && len(pref.keywordListIds) > 0 {
						// Load keyword lists and check if alert keywords match
						for _, listId := range pref.keywordListIds {
							listQuery := fmt.Sprintf(`SELECT "keywords" FROM "keywordLists" WHERE "keywordListId" = %d`, listId)
							var listKeywordsJson string
							if err := api.Controller.Database.Sql.QueryRow(listQuery).Scan(&listKeywordsJson); err == nil {
								var listKeywords []string
								if listKeywordsJson != "" && listKeywordsJson != "[]" {
									json.Unmarshal([]byte(listKeywordsJson), &listKeywords)
								}
								// Check if any alert keyword matches list keywords
								for _, alertKw := range alertKeywords {
									for _, listKw := range listKeywords {
										if strings.EqualFold(alertKw, listKw) {
											matchesKeywords = true
											break
										}
									}
									if matchesKeywords {
										break
									}
								}
							}
							if matchesKeywords {
								break
							}
						}
					}

					matchesPreference = matchesKeywords
				}
			} else if alertType == "tone+keyword" {
				// For tone+keyword alerts: must match both tone and keyword preferences
				toneMatches := false
				keywordMatches := false

				// Check tone match
				if pref.toneAlerts {
					if len(pref.toneSetIds) > 0 {
						for _, userToneSetId := range pref.toneSetIds {
							if userToneSetId == toneSetId {
								toneMatches = true
								break
							}
						}
					} else {
						toneMatches = true
					}
				}

				// Check keyword match
				if pref.keywordAlerts {
					var alertKeywords []string
					if keywordsMatched != "" && keywordsMatched != "[]" {
						json.Unmarshal([]byte(keywordsMatched), &alertKeywords)
					}

					// Check direct keywords
					for _, alertKw := range alertKeywords {
						for _, userKw := range pref.keywords {
							if strings.EqualFold(alertKw, userKw) {
								keywordMatches = true
								break
							}
						}
						if keywordMatches {
							break
						}
					}

					// Check keyword lists
					if !keywordMatches && len(pref.keywordListIds) > 0 {
						for _, listId := range pref.keywordListIds {
							listQuery := fmt.Sprintf(`SELECT "keywords" FROM "keywordLists" WHERE "keywordListId" = %d`, listId)
							var listKeywordsJson string
							if err := api.Controller.Database.Sql.QueryRow(listQuery).Scan(&listKeywordsJson); err == nil {
								var listKeywords []string
								if listKeywordsJson != "" && listKeywordsJson != "[]" {
									json.Unmarshal([]byte(listKeywordsJson), &listKeywords)
								}
								for _, alertKw := range alertKeywords {
									for _, listKw := range listKeywords {
										if strings.EqualFold(alertKw, listKw) {
											keywordMatches = true
											break
										}
									}
									if keywordMatches {
										break
									}
								}
							}
							if keywordMatches {
								break
							}
						}
					}
				}

				matchesPreference = toneMatches && keywordMatches
			}

			// Only add alert if it matches user's preferences
			if !matchesPreference {
				continue
			}

			// Check if alert is still delayed for this user (respects group delays)
			if callTimestamp.Valid && client.User != nil {
				// Get system and talkgroup to build call object for delay check
				system, _ := api.Controller.Systems.GetSystemById(systemId)
				var talkgroup *Talkgroup
				if system != nil {
					talkgroup, _ = system.Talkgroups.GetTalkgroupById(talkgroupId)
				}

				if system != nil && talkgroup != nil {
					// Create minimal call object for delay check
					callTimestampTime := time.UnixMilli(callTimestamp.Int64)
					minimalCall := &Call{
						Id:        callId,
						System:    system,
						Talkgroup: talkgroup,
						Timestamp: callTimestampTime,
					}

					// Get user's effective delay (includes group delays)
					defaultDelay := api.Controller.Options.DefaultSystemDelay
					effectiveDelay := api.Controller.userEffectiveDelay(client.User, minimalCall, defaultDelay)

					// Check if call is still delayed
					if effectiveDelay > 0 {
						delayCompletionTime := callTimestampTime.Add(time.Duration(effectiveDelay) * time.Minute)
						if time.Now().Before(delayCompletionTime) {
							// Alert is still delayed for this user, skip it
							continue
						}
					}
				}
			}

			// Alert passed all filters, add it
			allAlerts = append(allAlerts, alertMap)
		}

		// Group alerts by tone set (for tone alerts) or channel (for keyword alerts)
		type alertGroup struct {
			key    string
			alerts []map[string]any
		}
		toneGroups := []alertGroup{}
		channelGroups := []alertGroup{}

		// Group tone alerts by tone set name
		toneGroupMap := make(map[string][]map[string]any)
		channelGroupMap := make(map[string][]map[string]any)
		for _, alert := range allAlerts {
			alertType, _ := alert["alertType"].(string)
			if alertType == "tone" || alertType == "tone+keyword" {
				// Get tone set name from alert
				toneSetKey := "Unknown Tone Set"
				if matchedToneSetName, ok := alert["matchedToneSetName"].(string); ok && matchedToneSetName != "" {
					toneSetKey = matchedToneSetName
				} else if matchedToneSetNamesRaw, ok := alert["matchedToneSetNames"]; ok {
					// Handle both []string and []interface{} (from JSON unmarshaling)
					if matchedToneSetNames, ok := matchedToneSetNamesRaw.([]string); ok && len(matchedToneSetNames) > 0 {
						toneSetKey = matchedToneSetNames[0]
					} else if matchedToneSetNamesAny, ok := matchedToneSetNamesRaw.([]any); ok && len(matchedToneSetNamesAny) > 0 {
						if first, ok := matchedToneSetNamesAny[0].(string); ok && first != "" {
							toneSetKey = first
						}
					}
				}
				toneGroupMap[toneSetKey] = append(toneGroupMap[toneSetKey], alert)
			} else if alertType == "keyword" {
				// Group keyword alerts by channel (system + talkgroup)
				systemLabel, _ := alert["systemLabel"].(string)
				if systemLabel == "" {
					systemId, _ := alert["systemId"].(uint64)
					systemLabel = fmt.Sprintf("System %d", systemId)
				}
				talkgroupLabel, _ := alert["talkgroupLabel"].(string)
				if talkgroupLabel == "" {
					talkgroupName, _ := alert["talkgroupName"].(string)
					if talkgroupName == "" {
						talkgroupId, _ := alert["talkgroupId"].(uint64)
						talkgroupLabel = fmt.Sprintf("Talkgroup %d", talkgroupId)
					} else {
						talkgroupLabel = talkgroupName
					}
				}
				channelKey := fmt.Sprintf("%s / %s", systemLabel, talkgroupLabel)
				channelGroupMap[channelKey] = append(channelGroupMap[channelKey], alert)
			}
		}

		// Convert tone group map to slice
		for key, alerts := range toneGroupMap {
			toneGroups = append(toneGroups, alertGroup{
				key:    key,
				alerts: alerts,
			})
		}

		// Convert channel group map to slice
		for key, alerts := range channelGroupMap {
			channelGroups = append(channelGroups, alertGroup{
				key:    key,
				alerts: alerts,
			})
		}

		// Sort groups by most recent alert (within each group, alerts are already sorted by createdAt DESC)
		sort.Slice(toneGroups, func(i, j int) bool {
			if len(toneGroups[i].alerts) == 0 || len(toneGroups[j].alerts) == 0 {
				return false
			}
			createdAtI, _ := toneGroups[i].alerts[0]["createdAt"].(int64)
			createdAtJ, _ := toneGroups[j].alerts[0]["createdAt"].(int64)
			return createdAtI > createdAtJ
		})
		sort.Slice(channelGroups, func(i, j int) bool {
			if len(channelGroups[i].alerts) == 0 || len(channelGroups[j].alerts) == 0 {
				return false
			}
			createdAtI, _ := channelGroups[i].alerts[0]["createdAt"].(int64)
			createdAtJ, _ := channelGroups[j].alerts[0]["createdAt"].(int64)
			return createdAtI > createdAtJ
		})

		// Combine tone and channel groups, interleaved by most recent alert
		allGroups := []alertGroup{}
		allGroups = append(allGroups, toneGroups...)
		allGroups = append(allGroups, channelGroups...)

		// Sort all groups by most recent alert
		sort.Slice(allGroups, func(i, j int) bool {
			if len(allGroups[i].alerts) == 0 || len(allGroups[j].alerts) == 0 {
				return false
			}
			createdAtI, _ := allGroups[i].alerts[0]["createdAt"].(int64)
			createdAtJ, _ := allGroups[j].alerts[0]["createdAt"].(int64)
			return createdAtI > createdAtJ
		})

		// Return all alerts (no pagination - client will display in scrollable view)
		alerts := []map[string]any{}
		for _, group := range allGroups {
			// Add all alerts from this group
			alerts = append(alerts, group.alerts...)
		}

		if b, err := json.Marshal(alerts); err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Write(b)
		} else {
			api.exitWithError(w, http.StatusInternalServerError, "failed to marshal alerts")
		}

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// TranscriptsHandler handles GET /api/transcripts - list recent call transcripts for user
func (api *Api) TranscriptsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	client := api.getClient(r)
	if client == nil || client.User == nil {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var (
		limit       uint = 50
		offset      uint = 0
		systemId    uint64
		talkgroupId uint64
		status      string
	)

	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.ParseUint(l, 10, 32); err == nil {
			limit = uint(v)
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.ParseUint(o, 10, 32); err == nil {
			offset = uint(v)
		}
	}
	if s := r.URL.Query().Get("systemId"); s != "" {
		if v, err := strconv.ParseUint(s, 10, 64); err == nil {
			systemId = v
		}
	}
	if tg := r.URL.Query().Get("talkgroupId"); tg != "" {
		if v, err := strconv.ParseUint(tg, 10, 64); err == nil {
			talkgroupId = v
		}
	}
	status = strings.TrimSpace(r.URL.Query().Get("status"))

	where := []string{`(c."transcript" IS NOT NULL AND c."transcript" <> '')`}
	if systemId > 0 {
		where = append(where, fmt.Sprintf(`c."systemId" = %d`, systemId))
	}
	if talkgroupId > 0 {
		where = append(where, fmt.Sprintf(`c."talkgroupId" = %d`, talkgroupId))
	}
	if status != "" {
		where = append(where, fmt.Sprintf(`c."transcriptionStatus" = '%s'`, escapeQuotes(status)))
	}
	whereClause := strings.Join(where, " AND ")

	query := fmt.Sprintf(`SELECT c."callId", c."systemId", c."talkgroupId", c."transcriptionStatus", c."transcript", c."timestamp", s."label" as "systemLabel", t."label" as "talkgroupLabel", t."name" as "talkgroupName" FROM "calls" c LEFT JOIN "systems" s ON s."systemId" = c."systemId" LEFT JOIN "talkgroups" t ON t."talkgroupId" = c."talkgroupId" WHERE %s ORDER BY c."callId" DESC LIMIT %d OFFSET %d`, whereClause, limit, offset)

	rows, err := api.Controller.Database.Sql.Query(query)
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("failed to query transcripts: %v", err))
		return
	}
	defer rows.Close()

	results := []map[string]any{}
	for rows.Next() {
		var (
			callId              uint64
			sysId               uint64
			tgId                uint64
			transcriptionStatus sql.NullString
			transcript          sql.NullString
			callTimestamp       sql.NullInt64
			systemLabel         sql.NullString
			talkgroupLabel      sql.NullString
			talkgroupName       sql.NullString
		)

		if err := rows.Scan(&callId, &sysId, &tgId, &transcriptionStatus, &transcript, &callTimestamp, &systemLabel, &talkgroupLabel, &talkgroupName); err != nil {
			continue
		}

		entry := map[string]any{
			"callId":              callId,
			"systemId":            sysId,
			"talkgroupId":         tgId,
			"transcript":          transcript.String,
			"transcriptionStatus": transcriptionStatus.String,
			"timestamp":           callTimestamp.Int64,
		}
		if systemLabel.Valid {
			entry["systemLabel"] = systemLabel.String
		}
		if talkgroupLabel.Valid {
			entry["talkgroupLabel"] = talkgroupLabel.String
		}
		if talkgroupName.Valid {
			entry["talkgroupName"] = talkgroupName.String
		}
		results = append(results, entry)
	}

	if b, err := json.Marshal(results); err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	} else {
		api.exitWithError(w, http.StatusInternalServerError, "failed to marshal transcripts")
	}
}

// AlertPreferencesHandler handles GET/PUT /api/alerts/preferences
func (api *Api) AlertPreferencesHandler(w http.ResponseWriter, r *http.Request) {
	client := api.getClient(r)
	if client == nil || client.User == nil {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Get preferences with talkgroupRef (for frontend matching)
		query := fmt.Sprintf(`SELECT p."userId", p."systemId", p."talkgroupId", p."alertEnabled", p."toneAlerts", p."keywordAlerts", p."keywords", p."keywordListIds", p."toneSetIds", t."talkgroupRef", s."systemRef" FROM "userAlertPreferences" p LEFT JOIN "talkgroups" t ON t."talkgroupId" = p."talkgroupId" LEFT JOIN "systems" s ON s."systemId" = p."systemId" WHERE p."userId" = %d`, client.User.Id)
		rows, err := api.Controller.Database.Sql.Query(query)
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("failed to query preferences: %v", err))
			return
		}
		defer rows.Close()

		preferences := []map[string]any{}
		for rows.Next() {
			var (
				userId         uint64
				systemId       uint64
				talkgroupId    uint64
				alertEnabled   bool
				toneAlerts     bool
				keywordAlerts  bool
				keywordsJson   string
				keywordListIds string
				toneSetIdsJson string
				talkgroupRef   sql.NullInt32
				systemRef      sql.NullInt32
			)

			if err := rows.Scan(&userId, &systemId, &talkgroupId, &alertEnabled, &toneAlerts, &keywordAlerts, &keywordsJson, &keywordListIds, &toneSetIdsJson, &talkgroupRef, &systemRef); err != nil {
				continue
			}

			var keywords []string
			if keywordsJson != "" && keywordsJson != "[]" {
				json.Unmarshal([]byte(keywordsJson), &keywords)
			}

			var keywordListIdsList []uint64
			if keywordListIds != "" && keywordListIds != "[]" {
				json.Unmarshal([]byte(keywordListIds), &keywordListIdsList)
			}

			var toneSetIdsList []string
			if toneSetIdsJson != "" && toneSetIdsJson != "[]" {
				json.Unmarshal([]byte(toneSetIdsJson), &toneSetIdsList)
			}

			prefMap := map[string]any{
				"userId":         userId,
				"systemId":       systemId,
				"talkgroupId":    talkgroupId,
				"alertEnabled":   alertEnabled,
				"toneAlerts":     toneAlerts,
				"keywordAlerts":  keywordAlerts,
				"keywords":       keywords,
				"keywordListIds": keywordListIdsList,
				"toneSetIds":     toneSetIdsList,
			}

			// Also include talkgroupRef and systemRef if available (for frontend matching)
			if talkgroupRef.Valid {
				prefMap["talkgroupRef"] = talkgroupRef.Int32
			}
			if systemRef.Valid {
				prefMap["systemRef"] = systemRef.Int32
			}

			preferences = append(preferences, prefMap)
		}

		if b, err := json.Marshal(preferences); err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Write(b)
		} else {
			api.exitWithError(w, http.StatusInternalServerError, "failed to marshal preferences")
		}

	case http.MethodPut:
		// Update alert preferences
		var preferences []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&preferences); err != nil {
			api.exitWithError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
			return
		}

		tx, err := api.Controller.Database.Sql.Begin()
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, "failed to begin transaction")
			return
		}
		defer tx.Rollback()

		for _, pref := range preferences {
			var (
				requestSystem  uint64
				requestTg      uint64
				systemId       uint64
				alertEnabled   bool
				toneAlerts     bool = true
				keywordAlerts  bool = true
				keywords       []string
				keywordListIds []uint64
				toneSetIds     []string
			)

			// Accept either systemId or systemRef field names
			if v, ok := pref["systemId"].(float64); ok {
				requestSystem = uint64(v)
			}
			if v, ok := pref["systemRef"].(float64); ok && requestSystem == 0 {
				requestSystem = uint64(v)
			}
			// Accept either talkgroupId or talkgroupRef field names
			if v, ok := pref["talkgroupId"].(float64); ok {
				requestTg = uint64(v)
			}
			if v, ok := pref["talkgroupRef"].(float64); ok && requestTg == 0 {
				requestTg = uint64(v)
			}
			if v, ok := pref["alertEnabled"].(bool); ok {
				alertEnabled = v
			}
			if v, ok := pref["toneAlerts"].(bool); ok {
				toneAlerts = v
			}
			if v, ok := pref["keywordAlerts"].(bool); ok {
				keywordAlerts = v
			}
			if v, ok := pref["keywords"].([]any); ok {
				for _, kw := range v {
					if k, ok := kw.(string); ok {
						keywords = append(keywords, k)
					}
				}
			}
			if v, ok := pref["keywordListIds"].([]any); ok {
				for _, id := range v {
					switch idVal := id.(type) {
					case float64:
						keywordListIds = append(keywordListIds, uint64(idVal))
					case string:
						if parsed, err := strconv.ParseUint(idVal, 10, 64); err == nil {
							keywordListIds = append(keywordListIds, parsed)
						}
					}
				}
			}
			if v, ok := pref["toneSetIds"].([]any); ok {
				for _, value := range v {
					switch idVal := value.(type) {
					case string:
						toneSetIds = append(toneSetIds, idVal)
					case float64:
						toneSetIds = append(toneSetIds, fmt.Sprintf("%.0f", idVal))
					}
				}
			}

			// Resolve systemId: prefer systemRef, fallback to systemId
			systemId = 0
			// Try systemRef first to avoid collision (e.g., OH Geauga systemRef=28 vs OH Statewide MA systemId=28)
			resolveSystemQuery := fmt.Sprintf(`SELECT "systemId" FROM "systems" WHERE "systemRef" = %d`, requestSystem)
			if err := api.Controller.Database.Sql.QueryRow(resolveSystemQuery).Scan(&systemId); err != nil {
				// Fallback: try as systemId
				resolveSystemQuery = fmt.Sprintf(`SELECT "systemId" FROM "systems" WHERE "systemId" = %d`, requestSystem)
				if err := api.Controller.Database.Sql.QueryRow(resolveSystemQuery).Scan(&systemId); err != nil {
					api.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("skipping preference: could not resolve systemId from value=%d", requestSystem))
					continue
				}
			}

			// Validate that talkgroup exists and get tone detection status (prefer talkgroupRef, fallback to talkgroupId)
			var dbTalkgroupId uint64 = 0
			var toneDetectionEnabled bool = false
			// Try talkgroupRef first
			verifyQuery := fmt.Sprintf(`SELECT "talkgroupId", "toneDetectionEnabled" FROM "talkgroups" WHERE "systemId" = %d AND "talkgroupRef" = %d`, systemId, requestTg)
			if err := api.Controller.Database.Sql.QueryRow(verifyQuery).Scan(&dbTalkgroupId, &toneDetectionEnabled); err != nil {
				// Fallback: try as talkgroupId
				verifyQuery = fmt.Sprintf(`SELECT "talkgroupId", "toneDetectionEnabled" FROM "talkgroups" WHERE "systemId" = %d AND "talkgroupId" = %d`, systemId, requestTg)
				if err := api.Controller.Database.Sql.QueryRow(verifyQuery).Scan(&dbTalkgroupId, &toneDetectionEnabled); err != nil {
					// Talkgroup doesn't exist, skip this preference
					api.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("skipping preference for non-existent talkgroup: systemId=%d, providedTalkgroup=%d", systemId, requestTg))
					continue
				}
			}

			// If tone detection is not enabled for this talkgroup, disable tone alerts
			if !toneDetectionEnabled && toneAlerts {
				toneAlerts = false
			}

			keywordsJson, _ := json.Marshal(keywords)
			keywordListIdsJson, _ := json.Marshal(keywordListIds)
			toneSetIdsJson, _ := json.Marshal(toneSetIds)

			// Ensure we never store "null" for arrays - always use "[]" for empty arrays
			if string(keywordsJson) == "null" {
				keywordsJson = []byte("[]")
			}
			if string(keywordListIdsJson) == "null" {
				keywordListIdsJson = []byte("[]")
			}
			if string(toneSetIdsJson) == "null" {
				toneSetIdsJson = []byte("[]")
			}

			// DEBUG: Log tone set preferences being saved
			if toneAlerts {
				meaning := "ALL TONE SETS"
				if len(toneSetIds) > 0 {
					meaning = fmt.Sprintf("SPECIFIC: %v", toneSetIds)
				}
				api.Controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("💾 [TONE SET DEBUG] Saving preference for user %d, system %d, talkgroup %d: %s (alertEnabled=%t)", client.User.Id, systemId, dbTalkgroupId, meaning, alertEnabled))
				api.Controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("💾 [TONE SET DEBUG] JSON being stored: %s", string(toneSetIdsJson)))
			} else if alertEnabled {
				// Alert is enabled but tone alerts are off (maybe just keyword alerts)
				api.Controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("💾 [TONE SET DEBUG] Saving preference for user %d, system %d, talkgroup %d: toneAlerts=false (only keyword alerts)", client.User.Id, systemId, dbTalkgroupId))
			}

			// Upsert preference using verified database talkgroupId
			query := fmt.Sprintf(`INSERT INTO "userAlertPreferences" ("userId", "systemId", "talkgroupId", "alertEnabled", "toneAlerts", "keywordAlerts", "keywords", "keywordListIds", "toneSetIds") VALUES (%d, %d, %d, %t, %t, %t, $1, $2, $3) ON CONFLICT ("userId", "systemId", "talkgroupId") DO UPDATE SET "alertEnabled" = %t, "toneAlerts" = %t, "keywordAlerts" = %t, "keywords" = $1, "keywordListIds" = $2, "toneSetIds" = $3`, client.User.Id, systemId, dbTalkgroupId, alertEnabled, toneAlerts, keywordAlerts, alertEnabled, toneAlerts, keywordAlerts)

			if _, err := tx.Exec(query, string(keywordsJson), string(keywordListIdsJson), string(toneSetIdsJson)); err != nil {
				api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("failed to update preference: %v", err))
				return
			}
		}

		if err := tx.Commit(); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, "failed to commit transaction")
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// KeywordListsHandler handles GET/POST /api/keyword-lists
func (api *Api) KeywordListsHandler(w http.ResponseWriter, r *http.Request) {
	client := api.getClient(r)
	if client == nil {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Allow admin token authentication (client.IsAdmin) or user authentication (client.User != nil)
	if !client.IsAdmin && client.User == nil {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Get all keyword lists (admin can see all, users see available lists)
		query := `SELECT "keywordListId", "label", "description", "keywords", "order", "createdAt" FROM "keywordLists" ORDER BY "order" ASC, "createdAt" DESC`
		rows, err := api.Controller.Database.Sql.Query(query)
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("failed to query keyword lists: %v", err))
			return
		}
		defer rows.Close()

		lists := []map[string]any{}
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

			lists = append(lists, map[string]any{
				"id":          listId,
				"label":       label,
				"description": description,
				"keywords":    keywords,
				"order":       order,
				"createdAt":   createdAt,
			})
		}

		if b, err := json.Marshal(lists); err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Write(b)
		} else {
			api.exitWithError(w, http.StatusInternalServerError, "failed to marshal keyword lists")
		}

	case http.MethodPost:
		// Create keyword list (admin only)
		if !api.isAdmin(client) {
			api.exitWithError(w, http.StatusForbidden, "admin only")
			return
		}

		var list map[string]any
		if err := json.NewDecoder(r.Body).Decode(&list); err != nil {
			api.exitWithError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
			return
		}

		var (
			label       string
			description string
			keywords    []string
			order       uint
		)

		if v, ok := list["label"].(string); ok {
			label = v
		}
		if v, ok := list["description"].(string); ok {
			description = v
		}
		if v, ok := list["keywords"].([]any); ok {
			for _, kw := range v {
				if k, ok := kw.(string); ok {
					keywords = append(keywords, k)
				}
			}
		}
		if v, ok := list["order"].(float64); ok {
			order = uint(v)
		}

		keywordsJson, _ := json.Marshal(keywords)

		query := fmt.Sprintf(`INSERT INTO "keywordLists" ("label", "description", "keywords", "order", "createdAt") VALUES ('%s', '%s', '%s', %d, %d) RETURNING "keywordListId"`, escapeQuotes(label), escapeQuotes(description), escapeQuotes(string(keywordsJson)), order, time.Now().UnixMilli())

		var listId uint64
		if err := api.Controller.Database.Sql.QueryRow(query).Scan(&listId); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create keyword list: %v", err))
			return
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(fmt.Sprintf(`{"id": %d, "success": true}`, listId)))

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// KeywordListHandler handles PUT/DELETE /api/keyword-lists/{id}
func (api *Api) KeywordListHandler(w http.ResponseWriter, r *http.Request) {
	client := api.getClient(r)
	if client == nil {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Allow admin token authentication (client.IsAdmin) or user authentication (client.User != nil)
	if !client.IsAdmin && client.User == nil {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if !api.isAdmin(client) {
		api.exitWithError(w, http.StatusForbidden, "admin only")
		return
	}

	// Extract list ID from path
	path := strings.TrimPrefix(r.URL.Path, "/api/keyword-lists/")
	listId, err := strconv.ParseUint(path, 10, 64)
	if err != nil {
		api.exitWithError(w, http.StatusBadRequest, "invalid keyword list id")
		return
	}

	switch r.Method {
	case http.MethodPut:
		var list map[string]any
		if err := json.NewDecoder(r.Body).Decode(&list); err != nil {
			api.exitWithError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
			return
		}

		var (
			label       string
			description string
			keywords    []string
			order       uint
		)

		if v, ok := list["label"].(string); ok {
			label = v
		}
		if v, ok := list["description"].(string); ok {
			description = v
		}
		if v, ok := list["keywords"].([]any); ok {
			for _, kw := range v {
				if k, ok := kw.(string); ok {
					keywords = append(keywords, k)
				}
			}
		}
		if v, ok := list["order"].(float64); ok {
			order = uint(v)
		}

		keywordsJson, _ := json.Marshal(keywords)

		query := fmt.Sprintf(`UPDATE "keywordLists" SET "label" = '%s', "description" = '%s', "keywords" = '%s', "order" = %d WHERE "keywordListId" = %d`, escapeQuotes(label), escapeQuotes(description), escapeQuotes(string(keywordsJson)), order, listId)

		if _, err := api.Controller.Database.Sql.Exec(query); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("failed to update keyword list: %v", err))
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))

	case http.MethodDelete:
		query := fmt.Sprintf(`DELETE FROM "keywordLists" WHERE "keywordListId" = %d`, listId)
		if _, err := api.Controller.Database.Sql.Exec(query); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("failed to delete keyword list: %v", err))
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// getClient extracts client from request (helper for API handlers)
func (api *Api) getClient(r *http.Request) *Client {
	// Get PIN/token from query parameter or Authorization header
	token := r.URL.Query().Get("pin")
	if token == "" {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			// Handle both "Bearer <token>" and direct token formats
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			} else {
				// Direct token (admin service format)
				token = authHeader
			}
		}
	}

	if token == "" {
		return nil
	}

	// First check if it's an admin token
	if api.Controller.Admin != nil && api.Controller.Admin.ValidateToken(token) {
		// Return a client marked as admin
		return &Client{
			User:    nil, // Admin token, not a user
			IsAdmin: true,
		}
	}

	// Then try to find user by PIN
	user := api.Controller.Users.GetUserByPin(token)
	if user != nil {
		// Create a client for this request
		return &Client{
			User:    user,
			IsAdmin: false,
		}
	}

	return nil
}

// isAdmin checks if client is admin
func (api *Api) isAdmin(client *Client) bool {
	if client == nil {
		return false
	}

	// Check if client was authenticated with admin token
	if client.IsAdmin {
		return true
	}

	// For now, regular users are not admins
	// This could be extended to check user roles in the future
	return false
}

func (api *Api) SettingsSaveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Get PIN from query parameter or Authorization header
	pin := r.URL.Query().Get("pin")
	if pin == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			pin = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if pin == "" {
		api.exitWithError(w, http.StatusUnauthorized, "PIN required")
		return
	}

	// Find user by PIN
	user := api.Controller.Users.GetUserByPin(pin)
	if user == nil {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid PIN")
		return
	}

	// Parse request body
	var settings map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Protect AccountExpiresAt - remove it from user-provided settings if present
	// This field is server-managed and cannot be modified by users
	delete(settings, "accountExpiresAt")

	// Convert settings to JSON string
	settingsJson, err := json.Marshal(settings)
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to marshal settings")
		return
	}

	// Save settings to user
	user.Settings = string(settingsJson)
	if err := api.Controller.Users.Update(user); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to update user")
		return
	}

	// Write to database
	if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
		log.Printf("ERROR: Failed to write user settings to database: %v", err)
		api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to save settings: %v", err))
		return
	}

	// Sync config to file if enabled
	api.Controller.SyncConfigToFile()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Settings saved successfully",
	})
}

// AccountGetHandler handles GET requests to get current user account information
func (api *Api) AccountGetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Get PIN from query parameter or Authorization header
	pin := r.URL.Query().Get("pin")
	if pin == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			pin = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if pin == "" {
		api.exitWithError(w, http.StatusUnauthorized, "PIN required")
		return
	}

	// Find user by PIN
	user := api.Controller.Users.GetUserByPin(pin)
	if user == nil {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid PIN")
		return
	}

	// Parse timestamps - return nil for invalid/empty values instead of 0
	var createdAtTimestamp interface{}
	var lastLoginTimestamp interface{}

	if user.CreatedAt != "" && user.CreatedAt != "0" {
		if parsed, err := strconv.ParseInt(user.CreatedAt, 10, 64); err == nil && parsed > 0 {
			createdAtTimestamp = parsed
		} else {
			createdAtTimestamp = nil
		}
	} else {
		createdAtTimestamp = nil
	}

	if user.LastLogin != "" && user.LastLogin != "0" {
		if parsed, err := strconv.ParseInt(user.LastLogin, 10, 64); err == nil && parsed > 0 {
			lastLoginTimestamp = parsed
		} else {
			lastLoginTimestamp = nil
		}
	} else {
		lastLoginTimestamp = nil
	}

	// Get user group information and determine if billing is required
	var groupName string
	var billingRequired bool
	var currentPriceId string = ""
	var subscriptionStatusDisplay string = user.SubscriptionStatus
	if user.UserGroupId > 0 {
		group := api.Controller.UserGroups.Get(user.UserGroupId)
		if group != nil {
			groupName = group.Name

			// Determine if billing is required for this user
			if api.Controller.Options.StripePaywallEnabled && group.BillingEnabled {
				if group.BillingMode == "group_admin" {
					// For group_admin mode, only admins need billing
					billingRequired = user.IsGroupAdmin
					// If not an admin, always show that billing is managed by group admin
					// This applies regardless of subscription status (active, canceled, etc.)
					if !user.IsGroupAdmin {
						subscriptionStatusDisplay = "group_admin_managed"
					}
				} else {
					// For all_users mode, all users need billing
					billingRequired = true
				}
			} else {
				// Billing not enabled for this group
				billingRequired = false
				// Show that billing is not required
				if user.SubscriptionStatus == "canceled" || user.SubscriptionStatus == "not_billed" || user.SubscriptionStatus == "" {
					subscriptionStatusDisplay = "not_billed"
				}
			}
		}
	}

	// Get current subscription price ID from Stripe if user has an active subscription
	if user.StripeSubscriptionId != "" && api.Controller.Options.StripeSecretKey != "" {
		stripe.Key = api.Controller.Options.StripeSecretKey
		sub, err := subscription.Get(user.StripeSubscriptionId, nil)
		if err == nil && len(sub.Items.Data) > 0 {
			// Get the price ID from the first subscription item
			currentPriceId = sub.Items.Data[0].Price.ID
		} else if err != nil {
			log.Printf("Failed to fetch subscription %s to get price ID: %v", user.StripeSubscriptionId, err)
		}
	}

	// Get pricing options from user's group if billing is enabled
	var pricingOptions []PricingOption
	if user.UserGroupId > 0 {
		group := api.Controller.UserGroups.Get(user.UserGroupId)
		if group != nil && group.BillingEnabled {
			pricingOptions = group.GetPricingOptions()
		}
	}

	// Return user account info (excluding sensitive data)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":                        user.Id,
		"email":                     user.Email,
		"firstName":                 user.FirstName,
		"lastName":                  user.LastName,
		"zipCode":                   user.ZipCode,
		"verified":                  user.Verified,
		"createdAt":                 createdAtTimestamp,
		"lastLogin":                 lastLoginTimestamp,
		"userGroupId":               user.UserGroupId,
		"userGroupName":             groupName,
		"isGroupAdmin":              user.IsGroupAdmin,
		"stripeCustomerId":          user.StripeCustomerId,
		"stripeSubscriptionId":      user.StripeSubscriptionId,
		"subscriptionStatus":        user.SubscriptionStatus,
		"subscriptionStatusDisplay": subscriptionStatusDisplay,
		"currentPriceId":            currentPriceId,
		"pricingOptions":            pricingOptions,
		"hasBilling":                user.StripeCustomerId != "",
		"billingRequired":           billingRequired,
		"pinExpired":                user.PinExpired(),
		"pinExpiresAt":              user.PinExpiresAt,
	})
}

// AccountRequestEmailChangeVerificationHandler handles POST requests to request email change verification code
func (api *Api) AccountRequestEmailChangeVerificationHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Get PIN from query parameter or Authorization header
	pin := r.URL.Query().Get("pin")
	if pin == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			pin = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if pin == "" {
		api.exitWithError(w, http.StatusUnauthorized, "PIN required")
		return
	}

	// Find user by PIN
	user := api.Controller.Users.GetUserByPin(pin)
	if user == nil {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid PIN")
		return
	}

	// Generate email change verification code
	verificationCode, err := user.GenerateEmailChangeCode()
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to generate verification code")
		return
	}

	// Update user with verification code
	if err := api.Controller.Users.Update(user); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to update user")
		return
	}

	if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to save user")
		return
	}

	// Send verification code email
	if api.Controller.Options.EmailServiceEnabled {
		if err := api.Controller.EmailService.SendEmailChangeVerificationEmail(user, verificationCode); err != nil {
			log.Printf("Failed to send email change verification email: %v", err)
			api.exitWithError(w, http.StatusInternalServerError, "Failed to send verification email")
			return
		}
	} else {
		api.exitWithError(w, http.StatusServiceUnavailable, "Email service is not enabled")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Verification code sent to your email",
	})
}

// AccountVerifyEmailChangeCodeHandler handles POST requests to verify email change code
func (api *Api) AccountVerifyEmailChangeCodeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Get PIN from query parameter or Authorization header
	pin := r.URL.Query().Get("pin")
	if pin == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			pin = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if pin == "" {
		api.exitWithError(w, http.StatusUnauthorized, "PIN required")
		return
	}

	// Find user by PIN
	user := api.Controller.Users.GetUserByPin(pin)
	if user == nil {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid PIN")
		return
	}

	var request struct {
		Code string `json:"code"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Validate input
	if request.Code == "" {
		api.exitWithError(w, http.StatusBadRequest, "Verification code is required")
		return
	}

	// Verify code
	if !user.VerifyEmailChangeCode(request.Code) {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid or expired verification code")
		return
	}

	// Code is valid - clear it and mark as verified (we'll store this in a temporary field or session)
	// For now, we'll just return success and the client will proceed with email change
	// The code will be cleared when the email is actually changed

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":  "Verification code verified successfully",
		"verified": true,
	})
}

// AccountUpdateEmailHandler handles POST requests to update user email
// Requires: email change code verification first, then sends verification to new email
func (api *Api) AccountUpdateEmailHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Get PIN from query parameter or Authorization header
	pin := r.URL.Query().Get("pin")
	if pin == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			pin = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if pin == "" {
		api.exitWithError(w, http.StatusUnauthorized, "PIN required")
		return
	}

	// Find user by PIN
	user := api.Controller.Users.GetUserByPin(pin)
	if user == nil {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid PIN")
		return
	}

	var request struct {
		NewEmail string `json:"newEmail"`
		Password string `json:"password"`
		Code     string `json:"code"` // Email change verification code
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Validate input
	if request.NewEmail == "" {
		api.exitWithError(w, http.StatusBadRequest, "New email is required")
		return
	}

	if request.Password == "" {
		api.exitWithError(w, http.StatusBadRequest, "Password is required to change email")
		return
	}

	if request.Code == "" {
		api.exitWithError(w, http.StatusBadRequest, "Verification code is required")
		return
	}

	// Verify password
	if !user.VerifyPassword(request.Password) {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid password")
		return
	}

	// Verify email change code
	if !user.VerifyEmailChangeCode(request.Code) {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid or expired verification code")
		return
	}

	// Check if email is already in use
	existingUser := api.Controller.Users.GetUserByEmail(request.NewEmail)
	if existingUser != nil && existingUser.Id != user.Id {
		api.exitWithError(w, http.StatusConflict, "Email already in use")
		return
	}

	// Store new email temporarily in Settings JSON
	var settings map[string]interface{}
	if user.Settings != "" {
		if err := json.Unmarshal([]byte(user.Settings), &settings); err != nil {
			settings = make(map[string]interface{})
		}
	} else {
		settings = make(map[string]interface{})
	}
	settings["pendingEmailChange"] = request.NewEmail // Store new email temporarily

	settingsJson, err := json.Marshal(settings)
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to marshal settings")
		return
	}
	user.Settings = string(settingsJson)
	user.Verified = false       // Mark as unverified until new email is verified
	user.ClearEmailChangeCode() // Clear the email change code

	// Generate verification token for new email
	if err := user.GenerateVerificationToken(); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to generate verification token")
		return
	}

	// Update user in database (but don't change email yet)
	if err := api.Controller.Users.Update(user); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to update user")
		return
	}

	if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to save user")
		return
	}

	// Sync config to file if enabled
	api.Controller.SyncConfigToFile()

	// Send verification email to new address using the email change verification template
	if api.Controller.Options.EmailServiceEnabled {
		if err := api.Controller.EmailService.SendNewEmailVerificationEmail(request.NewEmail, user.VerificationToken); err != nil {
			log.Printf("Failed to send verification email to new address: %v", err)
			// Don't fail the request, but log the error
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":              "Email change initiated. Please check your new email for verification.",
		"newEmail":             request.NewEmail,
		"requiresVerification": true,
	})
}

// AccountVerifyNewEmailHandler handles POST requests to verify new email and complete the change
func (api *Api) AccountVerifyNewEmailHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var request struct {
		Token string `json:"token"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if request.Token == "" {
		api.exitWithError(w, http.StatusBadRequest, "Verification token is required")
		return
	}

	// Find user by verification token
	allUsers := api.Controller.Users.GetAllUsers()
	var user *User
	for _, u := range allUsers {
		if u.VerificationToken == request.Token && !u.Verified {
			user = u
			break
		}
	}

	if user == nil {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid or expired verification token")
		return
	}

	// Get new email from VerificationToken (we stored it there temporarily)
	newEmail := user.VerificationToken
	if newEmail == "" || !strings.Contains(newEmail, "@") {
		// If VerificationToken doesn't contain new email, it means the flow is different
		api.exitWithError(w, http.StatusBadRequest, "Invalid verification state")
		return
	}

	// Update email
	user.Email = newEmail
	user.Verified = true
	user.VerificationToken = ""

	// Update Stripe customer email if they have a Stripe customer ID
	if user.StripeCustomerId != "" && api.Controller.Options.StripeSecretKey != "" {
		stripe.Key = api.Controller.Options.StripeSecretKey
		_, err := customer.Update(user.StripeCustomerId, &stripe.CustomerParams{
			Email: stripe.String(newEmail),
		})
		if err != nil {
			log.Printf("Failed to update Stripe customer email: %v", err)
			// Don't fail the request, but log the error
		} else {
			log.Printf("Updated Stripe customer email to %s", newEmail)
		}
	}

	// Update user in database
	if err := api.Controller.Users.Update(user); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to update user")
		return
	}

	if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to save user")
		return
	}

	// Sync config to file if enabled
	api.Controller.SyncConfigToFile()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Email verified and updated successfully",
		"email":   user.Email,
	})
}

// AccountRequestPasswordChangeVerificationHandler handles POST requests to request password change verification code
func (api *Api) AccountRequestPasswordChangeVerificationHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Get PIN from query parameter or Authorization header
	pin := r.URL.Query().Get("pin")
	if pin == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			pin = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if pin == "" {
		api.exitWithError(w, http.StatusUnauthorized, "PIN required")
		return
	}

	// Find user by PIN
	user := api.Controller.Users.GetUserByPin(pin)
	if user == nil {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid PIN")
		return
	}

	// Generate password change verification code
	verificationCode, err := user.GeneratePasswordChangeCode()
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to generate verification code")
		return
	}

	// Update user with verification code
	if err := api.Controller.Users.Update(user); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to update user")
		return
	}

	if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to save user")
		return
	}

	// Send verification code email
	if api.Controller.Options.EmailServiceEnabled {
		if err := api.Controller.EmailService.SendPasswordChangeVerificationEmail(user, verificationCode); err != nil {
			log.Printf("Failed to send password change verification email: %v", err)
			api.exitWithError(w, http.StatusInternalServerError, "Failed to send verification email")
			return
		}
	} else {
		api.exitWithError(w, http.StatusServiceUnavailable, "Email service is not enabled")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Verification code sent to your email",
	})
}

// AccountVerifyPasswordChangeCodeHandler handles POST requests to verify password change code
func (api *Api) AccountVerifyPasswordChangeCodeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Get PIN from query parameter or Authorization header
	pin := r.URL.Query().Get("pin")
	if pin == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			pin = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if pin == "" {
		api.exitWithError(w, http.StatusUnauthorized, "PIN required")
		return
	}

	// Find user by PIN
	user := api.Controller.Users.GetUserByPin(pin)
	if user == nil {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid PIN")
		return
	}

	var request struct {
		Code string `json:"code"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Validate input
	if request.Code == "" {
		api.exitWithError(w, http.StatusBadRequest, "Verification code is required")
		return
	}

	// Verify code
	if !user.VerifyPasswordChangeCode(request.Code) {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid or expired verification code")
		return
	}

	// Code is valid - return success
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":  "Verification code verified successfully",
		"verified": true,
	})
}

// AccountUpdatePasswordHandler handles POST requests to update user password
// Requires: password change code verification first
func (api *Api) AccountUpdatePasswordHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Get PIN from query parameter or Authorization header
	pin := r.URL.Query().Get("pin")
	if pin == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			pin = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if pin == "" {
		api.exitWithError(w, http.StatusUnauthorized, "PIN required")
		return
	}

	// Find user by PIN
	user := api.Controller.Users.GetUserByPin(pin)
	if user == nil {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid PIN")
		return
	}

	var request struct {
		NewPassword string `json:"newPassword"`
		Code        string `json:"code"` // Password change verification code
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Validate input
	if request.NewPassword == "" {
		api.exitWithError(w, http.StatusBadRequest, "New password is required")
		return
	}

	if request.Code == "" {
		api.exitWithError(w, http.StatusBadRequest, "Verification code is required")
		return
	}

	// Verify password change code
	if !user.VerifyPasswordChangeCode(request.Code) {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid or expired verification code")
		return
	}

	// Validate password strength
	if err := ValidatePassword(request.NewPassword); err != nil {
		api.exitWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Set new password
	user.SetPassword(request.NewPassword)
	user.ClearPasswordChangeCode() // Clear the verification code

	// Update user in database
	if err := api.Controller.Users.Update(user); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to update user")
		return
	}

	if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to save user")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Password updated successfully",
	})
}

// BillingPortalSessionHandler handles POST requests to create a Stripe billing portal session
func (api *Api) BillingPortalSessionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Get PIN from query parameter or Authorization header
	pin := r.URL.Query().Get("pin")
	if pin == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			pin = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if pin == "" {
		api.exitWithError(w, http.StatusUnauthorized, "PIN required")
		return
	}

	// Find user by PIN
	user := api.Controller.Users.GetUserByPin(pin)
	if user == nil {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid PIN")
		return
	}

	// Check if user has a Stripe customer ID
	if user.StripeCustomerId == "" {
		api.exitWithError(w, http.StatusBadRequest, "No billing account found")
		return
	}

	// Set Stripe API key
	stripe.Key = api.Controller.Options.StripeSecretKey
	if stripe.Key == "" {
		api.exitWithError(w, http.StatusInternalServerError, "Stripe not configured")
		return
	}

	var request struct {
		ReturnURL string `json:"returnUrl"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		// Use default return URL if decode fails
		request.ReturnURL = r.Header.Get("Referer")
		if request.ReturnURL == "" {
			request.ReturnURL = "/"
		}
	}

	// If ReturnURL is still empty after decoding, use default
	if request.ReturnURL == "" {
		request.ReturnURL = r.Header.Get("Referer")
		if request.ReturnURL == "" {
			// Build a proper return URL from the request using the helper function
			// which handles reverse proxy headers
			scheme, host := getSchemeAndHost(r)
			if host != "" {
				request.ReturnURL = fmt.Sprintf("%s://%s/", scheme, host)
			} else {
				// Fallback to base URL from options if available
				baseURL := api.Controller.Options.BaseUrl
				if baseURL != "" {
					if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
						baseURL = "https://" + baseURL
					}
					request.ReturnURL = baseURL + "/"
				} else {
					request.ReturnURL = "https://localhost/"
				}
			}
		}
	}

	// Create billing portal session
	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(user.StripeCustomerId),
		ReturnURL: stripe.String(request.ReturnURL),
	}

	portalSession, err := billingportalsession.New(params)
	if err != nil {
		log.Printf("Error creating billing portal session: %v", err)
		api.exitWithError(w, http.StatusInternalServerError, "failed to create billing portal session")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"url": portalSession.URL,
	})
}

// Group Admin Login Handler
func (api *Api) GroupAdminLoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var request struct {
		Email          string `json:"email"`
		Password       string `json:"password"`
		TurnstileToken string `json:"turnstile_token"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if request.Email == "" || request.Password == "" {
		api.exitWithError(w, http.StatusBadRequest, "Email and password are required")
		return
	}

	// Normalize email to lowercase for case-insensitive login
	request.Email = NormalizeEmail(request.Email)

	// Get client IP for login attempt tracking
	clientIP := GetRemoteAddr(r)

	// Turnstile verification (mobile apps are exempt)
	if api.Controller.Options.TurnstileEnabled {
		valid, err := api.verifyTurnstile(request.TurnstileToken, clientIP, r)
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("CAPTCHA verification error: %v", err))
			return
		}
		if !valid {
			api.exitWithError(w, http.StatusForbidden, "CAPTCHA verification failed. Please try again.")
			return
		}
	}

	user := api.Controller.Users.GetUserByEmail(request.Email)
	if user == nil || !user.VerifyPassword(request.Password) {
		// Record failed attempt
		api.Controller.LoginAttemptTracker.RecordFailedAttempt(clientIP)
		api.exitWithError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	// Login successful - reset failed attempts
	api.Controller.LoginAttemptTracker.RecordSuccess(clientIP)

	// Allow login even if not verified - email verification is optional
	if !user.Verified {
		log.Printf("Group admin %s logged in without email verification", user.Email)
	}

	if !user.IsGroupAdmin {
		api.exitWithError(w, http.StatusForbidden, "User is not a group admin")
		return
	}

	group := api.Controller.UserGroups.Get(user.UserGroupId)
	if group == nil {
		api.exitWithError(w, http.StatusForbidden, "User group not found")
		return
	}

	user.UpdateLastLogin()
	api.Controller.Users.Update(user)
	api.Controller.Users.Write(api.Controller.Database)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Login successful",
		"user": map[string]interface{}{
			"id":    user.Id,
			"email": user.Email,
			"pin":   user.Pin,
		},
		"group": map[string]interface{}{
			"id":                    group.Id,
			"name":                  group.Name,
			"allowAddExistingUsers": group.AllowAddExistingUsers,
		},
	})
}

// Helper function to get authenticated group admin user
func (api *Api) getGroupAdminUser(r *http.Request) (*User, *UserGroup, error) {
	pin := r.URL.Query().Get("pin")
	if pin == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			pin = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if pin == "" {
		return nil, nil, fmt.Errorf("PIN required")
	}

	user := api.Controller.Users.GetUserByPin(pin)
	if user == nil {
		return nil, nil, fmt.Errorf("Invalid PIN")
	}

	if !user.IsGroupAdmin {
		return nil, nil, fmt.Errorf("User is not a group admin")
	}

	group := api.Controller.UserGroups.Get(user.UserGroupId)
	if group == nil {
		return nil, nil, fmt.Errorf("User group not found")
	}

	return user, group, nil
}

// Group Admin - Get Users in Group
func (api *Api) GroupAdminUsersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	_, group, err := api.getGroupAdminUser(r)
	if err != nil {
		api.exitWithError(w, http.StatusUnauthorized, err.Error())
		return
	}

	// Get all users in the group
	allUsers := api.Controller.Users.GetAllUsers()
	groupUsers := []map[string]interface{}{}
	currentUserCount := 0
	for _, u := range allUsers {
		if u.UserGroupId == group.Id {
			currentUserCount++
			groupUsers = append(groupUsers, map[string]interface{}{
				"id":           u.Id,
				"email":        u.Email,
				"firstName":    u.FirstName,
				"lastName":     u.LastName,
				"verified":     u.Verified,
				"isGroupAdmin": u.IsGroupAdmin,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"users": groupUsers,
		"group": map[string]interface{}{
			"id":                    group.Id,
			"name":                  group.Name,
			"maxUsers":              group.MaxUsers,
			"userCount":             currentUserCount,
			"allowAddExistingUsers": group.AllowAddExistingUsers,
		},
	})
}

// Group Admin - Remove User from Group
func (api *Api) GroupAdminRemoveUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	user, group, err := api.getGroupAdminUser(r)
	if err != nil {
		api.exitWithError(w, http.StatusUnauthorized, err.Error())
		return
	}

	var request struct {
		UserId uint64 `json:"userId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	targetUser := api.Controller.Users.GetUserById(request.UserId)
	if targetUser == nil {
		api.exitWithError(w, http.StatusNotFound, "User not found")
		return
	}

	if targetUser.UserGroupId != group.Id {
		api.exitWithError(w, http.StatusForbidden, "User is not in your group")
		return
	}

	// Don't allow removing yourself
	if targetUser.Id == user.Id {
		api.exitWithError(w, http.StatusBadRequest, "Cannot remove yourself")
		return
	}

	// Remove group admin status if user was a group admin (security: admin status should not persist when removed from group)
	if targetUser.IsGroupAdmin {
		targetUser.IsGroupAdmin = false
	}

	// Move user to public registration group or remove group assignment
	publicGroup := api.Controller.UserGroups.GetPublicRegistrationGroup()
	if publicGroup != nil {
		targetUser.UserGroupId = publicGroup.Id
		// Clear user's individual delay settings - they will use the group's delay settings
		api.clearUserDelayValues(targetUser)
		// Sync user's connection limit with the group's connection limit
		api.syncUserConnectionLimit(targetUser)
	} else {
		targetUser.UserGroupId = 0
		// Clear user's individual delay settings when removing from group
		api.clearUserDelayValues(targetUser)
		// User has no group, keep their individual connection limit
	}

	api.Controller.Users.Update(targetUser)
	api.Controller.Users.Write(api.Controller.Database)

	// Sync config to file if enabled
	api.Controller.SyncConfigToFile()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "User removed from group",
	})
}

// Group Admin - Toggle Group Admin Status
func (api *Api) GroupAdminToggleAdminHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	currentAdmin, group, err := api.getGroupAdminUser(r)
	if err != nil {
		api.exitWithError(w, http.StatusUnauthorized, err.Error())
		return
	}

	var request struct {
		UserId       uint64 `json:"userId"`
		IsGroupAdmin bool   `json:"isGroupAdmin"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	targetUser := api.Controller.Users.GetUserById(request.UserId)
	if targetUser == nil {
		api.exitWithError(w, http.StatusNotFound, "User not found")
		return
	}

	if targetUser.UserGroupId != group.Id {
		api.exitWithError(w, http.StatusForbidden, "User is not in your group")
		return
	}

	// Don't allow changing your own admin status
	if targetUser.Id == currentAdmin.Id {
		api.exitWithError(w, http.StatusBadRequest, "Cannot change your own admin status")
		return
	}

	// Update the user's group admin status
	targetUser.IsGroupAdmin = request.IsGroupAdmin
	api.Controller.Users.Update(targetUser)
	api.Controller.Users.Write(api.Controller.Database)

	// Sync config to file if enabled
	api.Controller.SyncConfigToFile()

	action := "removed from"
	if request.IsGroupAdmin {
		action = "promoted to"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": fmt.Sprintf("User %s group admin", action),
	})
}

// Group Admin - Add User by Email
func (api *Api) GroupAdminAddUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	_, group, err := api.getGroupAdminUser(r)
	if err != nil {
		api.exitWithError(w, http.StatusUnauthorized, err.Error())
		return
	}

	var request struct {
		Email     string `json:"email"`
		Password  string `json:"password"`
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
		ZipCode   string `json:"zipCode"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Validate required fields
	if request.Email == "" || request.FirstName == "" || request.LastName == "" || request.ZipCode == "" {
		api.exitWithError(w, http.StatusBadRequest, "Email, first name, last name, and ZIP code are required")
		return
	}

	// Check if group has reached max users limit
	if group.MaxUsers > 0 {
		allUsers := api.Controller.Users.GetAllUsers()
		currentUserCount := 0
		for _, u := range allUsers {
			if u.UserGroupId == group.Id {
				currentUserCount++
			}
		}
		if currentUserCount >= int(group.MaxUsers) {
			api.exitWithError(w, http.StatusForbidden, fmt.Sprintf("Group has reached maximum user limit of %d", group.MaxUsers))
			return
		}
	}

	// Find user by email
	user := api.Controller.Users.GetUserByEmail(request.Email)

	if user == nil {
		// User doesn't exist - create new user
		if request.Password == "" {
			api.exitWithError(w, http.StatusBadRequest, "Password is required for new users")
			return
		}

		// Validate password strength
		if err := ValidatePassword(request.Password); err != nil {
			api.exitWithError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Create new user
		user = &User{
			Email:           request.Email,
			FirstName:       request.FirstName,
			LastName:        request.LastName,
			ZipCode:         request.ZipCode,
			UserGroupId:     group.Id,
			ConnectionLimit: group.ConnectionLimit, // Inherit group's connection limit
			Verified:        false,                 // Require email verification
			CreatedAt:       fmt.Sprintf("%d", time.Now().Unix()),
		}

		if err := user.HashPassword(request.Password); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, "Failed to hash password")
			return
		}

		// Generate verification token
		if err := user.GenerateVerificationToken(); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, "Failed to generate verification token")
			return
		}

		// Generate PIN for the user
		pin, err := api.Controller.Users.GenerateUniquePin(0)
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, "Failed to generate PIN")
			return
		}
		user.Pin = pin

		// Save new user
		if err := api.Controller.Users.SaveNewUser(user, api.Controller.Database); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, "Failed to create user")
			return
		}

		// Sync config to file if enabled
		api.Controller.SyncConfigToFile()

		// Handle billing setup for new users in billing-enabled groups
		if group.BillingEnabled {
			if group.BillingMode == "group_admin" && !user.IsGroupAdmin {
				// For non-admin users in admin-managed billing groups, sync subscription status from admin
				syncedFromAdmin := false
				allUsers := api.Controller.Users.GetAllUsers()
				for _, admin := range allUsers {
					if admin.UserGroupId == group.Id && admin.IsGroupAdmin && admin.SubscriptionStatus == "active" {
						// Sync from this admin
						user.SubscriptionStatus = admin.SubscriptionStatus
						user.PinExpiresAt = admin.PinExpiresAt
						api.Controller.Users.Update(user)
						api.Controller.Users.Write(api.Controller.Database)
						log.Printf("Synced subscription status from admin %s to new user %s", admin.Email, user.Email)
						syncedFromAdmin = true
						break
					}
				}

				// If no active admin found, expire PIN immediately - user needs admin to subscribe
				if !syncedFromAdmin {
					user.SubscriptionStatus = "incomplete"
					user.PinExpiresAt = uint64(time.Now().Unix() - 86400) // Set to 1 day ago to ensure it's expired
					api.Controller.Users.Update(user)
					api.Controller.Users.Write(api.Controller.Database)
					log.Printf("No active admin found - set PIN to expire (1 day ago) for new user %s in admin-managed billing group", user.Email)
				}
			} else if group.BillingMode == "all_users" || (group.BillingMode == "group_admin" && user.IsGroupAdmin) {
				// For all_users mode OR group admins in admin-managed mode, they need to subscribe
				// Expire PIN immediately - no access until they subscribe
				user.SubscriptionStatus = "incomplete"
				user.PinExpiresAt = uint64(time.Now().Unix() - 86400) // Set to 1 day ago to ensure it's expired
				api.Controller.Users.Update(user)
				api.Controller.Users.Write(api.Controller.Database)
				log.Printf("Set PIN to expire (1 day ago) for new user %s in billing-enabled group (mode: %s, isAdmin: %v) - must subscribe to gain access", user.Email, group.BillingMode, user.IsGroupAdmin)
			}
		}

		// Send verification email
		if api.Controller.Options.EmailServiceEnabled {
			if err := api.Controller.EmailService.SendVerificationEmail(user); err != nil {
				api.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("Failed to send verification email: %v", err))
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "User created and added to group successfully. Verification email sent.",
			"user": map[string]interface{}{
				"id":    user.Id,
				"email": user.Email,
			},
		})
		return
	}

	// User exists - add to group
	// Check if user is already in this group
	if user.UserGroupId == group.Id {
		api.exitWithError(w, http.StatusConflict, "User is already in this group")
		return
	}

	// Check if user is in another group
	if user.UserGroupId > 0 {
		api.exitWithError(w, http.StatusConflict, "User is already in another group")
		return
	}

	// Update user info if provided
	if request.FirstName != "" {
		user.FirstName = request.FirstName
	}
	if request.LastName != "" {
		user.LastName = request.LastName
	}
	if request.ZipCode != "" {
		user.ZipCode = request.ZipCode
	}
	if request.Password != "" {
		// Validate password strength
		if err := ValidatePassword(request.Password); err != nil {
			api.exitWithError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := user.HashPassword(request.Password); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, "Failed to hash password")
			return
		}
	}

	// Add user to group
	user.UserGroupId = group.Id
	// Clear user's individual delay settings - they will use the group's delay settings
	api.clearUserDelayValues(user)
	// Sync user's connection limit with the group's connection limit
	api.syncUserConnectionLimit(user)
	api.Controller.Users.Update(user)
	api.Controller.Users.Write(api.Controller.Database)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "User added to group successfully",
		"user": map[string]interface{}{
			"id":    user.Id,
			"email": user.Email,
		},
	})
}

// Group Admin - Add Existing User to Group (by email)
func (api *Api) GroupAdminAddExistingUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	_, group, err := api.getGroupAdminUser(r)
	if err != nil {
		api.exitWithError(w, http.StatusUnauthorized, err.Error())
		return
	}

	// Check if group allows adding existing users
	if !group.AllowAddExistingUsers {
		api.exitWithError(w, http.StatusForbidden, "This group does not allow adding existing users")
		return
	}

	var request struct {
		Email string `json:"email"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if request.Email == "" {
		api.exitWithError(w, http.StatusBadRequest, "Email is required")
		return
	}

	// Validate email format
	if !strings.Contains(request.Email, "@") {
		api.exitWithError(w, http.StatusBadRequest, "Invalid email format")
		return
	}

	// Check if group has reached max users limit
	if group.MaxUsers > 0 {
		currentUserCount := api.Controller.UserGroups.GetUserCount(group.Id, api.Controller.Users)
		if currentUserCount >= group.MaxUsers {
			api.exitWithError(w, http.StatusForbidden, fmt.Sprintf("Group has reached maximum user limit of %d", group.MaxUsers))
			return
		}
	}

	// Find user by email
	user := api.Controller.Users.GetUserByEmail(request.Email)
	if user == nil {
		api.exitWithError(w, http.StatusNotFound, "User not found")
		return
	}

	// Check if user is already in this group
	if user.UserGroupId == group.Id {
		api.exitWithError(w, http.StatusConflict, "User is already in this group")
		return
	}

	// Get old group info for notifications
	oldGroupId := user.UserGroupId
	var oldGroup *UserGroup
	if oldGroupId > 0 {
		oldGroup = api.Controller.UserGroups.Get(oldGroupId)
	}

	// Remove group admin status if user was a group admin
	if user.IsGroupAdmin && oldGroupId > 0 {
		user.IsGroupAdmin = false
	}

	// Handle billing transitions
	err = api.handleUserGroupBillingTransition(user, oldGroup, group)
	if err != nil {
		log.Printf("Error handling billing transition: %v", err)
		// Continue anyway - billing errors shouldn't block the move
	}

	// Move user to new group
	user.UserGroupId = group.Id
	// Clear user's individual delay settings - they will use the group's delay settings
	api.clearUserDelayValues(user)
	api.Controller.Users.Update(user)
	api.Controller.Users.Write(api.Controller.Database)

	// If user was added to an admin-managed billing group, sync subscription status from admin
	if group.BillingEnabled && group.BillingMode == "group_admin" && !user.IsGroupAdmin {
		// Find an admin in the group to sync from
		syncedFromAdmin := false
		allUsers := api.Controller.Users.GetAllUsers()
		for _, admin := range allUsers {
			if admin.UserGroupId == group.Id && admin.IsGroupAdmin && admin.SubscriptionStatus == "active" {
				// Sync from this admin
				user.SubscriptionStatus = admin.SubscriptionStatus
				user.PinExpiresAt = admin.PinExpiresAt
				api.Controller.Users.Update(user)
				api.Controller.Users.Write(api.Controller.Database)
				log.Printf("Synced subscription status from admin %s to user %s after adding to group", admin.Email, user.Email)
				syncedFromAdmin = true
				break
			}
		}

		// If no active admin found, expire PIN immediately - user needs admin to subscribe
		if !syncedFromAdmin {
			user.SubscriptionStatus = "incomplete"
			user.PinExpiresAt = uint64(time.Now().Unix() - 86400) // Set to 1 day ago to ensure it's expired
			api.Controller.Users.Update(user)
			api.Controller.Users.Write(api.Controller.Database)
			log.Printf("No active admin found - set PIN to expire (1 day ago) for user %s added to admin-managed billing group", user.Email)
		}
	}

	// Sync config to file if enabled
	api.Controller.SyncConfigToFile()

	// Send email notifications
	go func() {
		// Email the user about the group change
		if api.Controller.Options.EmailServiceEnabled {
			api.Controller.EmailService.SendUserGroupChangeEmail(user, group, oldGroup)
		}

		// Email original group admin(s) if they exist
		if oldGroupId > 0 {
			api.sendGroupAdminNotification(oldGroupId, user, group)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "User added to group successfully",
		"user": map[string]interface{}{
			"id":    user.Id,
			"email": user.Email,
		},
	})
}

// syncGroupAdminSubscriptionToAllUsers syncs the admin's subscription status and PIN expiration to all non-admin users in the group
// This is used for admin-managed billing groups where all users share the same subscription status
func (api *Api) syncGroupAdminSubscriptionToAllUsers(adminUser *User) {
	if adminUser == nil || adminUser.UserGroupId == 0 {
		log.Printf("syncGroupAdminSubscriptionToAllUsers: adminUser is nil or has no group ID")
		return
	}

	group := api.Controller.UserGroups.Get(adminUser.UserGroupId)
	if group == nil {
		log.Printf("syncGroupAdminSubscriptionToAllUsers: group %d not found", adminUser.UserGroupId)
		return
	}

	if !group.BillingEnabled || group.BillingMode != "group_admin" {
		log.Printf("syncGroupAdminSubscriptionToAllUsers: group %d is not admin-managed billing (billingEnabled=%v, billingMode=%s)",
			group.Id, group.BillingEnabled, group.BillingMode)
		return
	}

	if !adminUser.IsGroupAdmin {
		log.Printf("syncGroupAdminSubscriptionToAllUsers: user %s is not a group admin, skipping sync", adminUser.Email)
		return
	}

	log.Printf("syncGroupAdminSubscriptionToAllUsers: Starting sync for admin %s (status: %s, pinExpiresAt: %d) in group %d",
		adminUser.Email, adminUser.SubscriptionStatus, adminUser.PinExpiresAt, group.Id)

	// Get all users in the group
	allUsers := api.Controller.Users.GetAllUsers()
	updatedCount := 0

	for _, u := range allUsers {
		if u.UserGroupId == group.Id && !u.IsGroupAdmin && u.Id != adminUser.Id {
			oldStatus := u.SubscriptionStatus
			oldPinExpiresAt := u.PinExpiresAt
			// Sync subscription status and PIN expiration from admin to this user
			u.SubscriptionStatus = adminUser.SubscriptionStatus
			u.PinExpiresAt = adminUser.PinExpiresAt
			// Don't sync StripeSubscriptionId - only admins have that
			api.Controller.Users.Update(u)
			updatedCount++
			log.Printf("Synced subscription status from admin %s to user %s (ID: %d) - status: %s -> %s, pinExpiresAt: %d -> %d",
				adminUser.Email, u.Email, u.Id, oldStatus, u.SubscriptionStatus, oldPinExpiresAt, u.PinExpiresAt)
		}
	}

	if updatedCount > 0 {
		if err := api.Controller.Users.Write(api.Controller.Database); err != nil {
			log.Printf("Failed to save synced subscription statuses: %v", err)
		} else {
			log.Printf("Successfully synced subscription status from admin %s to %d users in group %d (group: %s)",
				adminUser.Email, updatedCount, group.Id, group.Name)
			// Sync config to file if enabled
			api.Controller.SyncConfigToFile()
		}
	} else {
		log.Printf("syncGroupAdminSubscriptionToAllUsers: No users to sync in group %d", group.Id)
	}
}

// getOrCreateGroupSharedCustomerId gets or creates a shared Stripe customer ID for a group when billing mode is "group_admin"
// Returns the customer ID that all group admins should use
func (api *Api) getOrCreateGroupSharedCustomerId(group *UserGroup) (string, error) {
	if group == nil || !group.BillingEnabled || group.BillingMode != "group_admin" {
		return "", fmt.Errorf("group does not support shared customer ID")
	}

	// Get all group admins
	allUsers := api.Controller.Users.GetAllUsers()
	var groupAdmins []*User
	for _, u := range allUsers {
		if u.UserGroupId == group.Id && u.IsGroupAdmin && u.StripeCustomerId != "" {
			groupAdmins = append(groupAdmins, u)
		}
	}

	// If any admin already has a customer ID, use that one (they all should share it)
	if len(groupAdmins) > 0 {
		sharedCustomerId := groupAdmins[0].StripeCustomerId
		log.Printf("Using existing shared customer ID %s for group %d", sharedCustomerId, group.Id)
		return sharedCustomerId, nil
	}

	// No existing customer ID found, create a new one using the first admin's info
	// Find the first group admin to use their info for the customer
	var firstAdmin *User
	for _, u := range allUsers {
		if u.UserGroupId == group.Id && u.IsGroupAdmin {
			firstAdmin = u
			break
		}
	}

	if firstAdmin == nil {
		return "", fmt.Errorf("no group admin found to create shared customer ID")
	}

	stripe.Key = api.Controller.Options.StripeSecretKey
	params := &stripe.CustomerParams{
		Email: stripe.String(firstAdmin.Email),
		Name:  stripe.String(firstAdmin.FirstName + " " + firstAdmin.LastName),
		Metadata: map[string]string{
			"group_id":     fmt.Sprintf("%d", group.Id),
			"group_name":   group.Name,
			"billing_mode": "group_admin",
		},
	}

	customer, err := customer.New(params)
	if err != nil {
		return "", fmt.Errorf("failed to create shared Stripe customer for group %d: %w", group.Id, err)
	}

	// Assign this customer ID to the first admin (and all admins will use it)
	firstAdmin.StripeCustomerId = customer.ID
	api.Controller.Users.Update(firstAdmin)
	api.Controller.Users.Write(api.Controller.Database)

	log.Printf("Created shared Stripe customer %s for group %d (first admin: %s)", customer.ID, group.Id, firstAdmin.Email)
	return customer.ID, nil
}

// clearUserDelayValues clears a user's individual delay settings when transferring between groups
// This ensures users only use the new group's delay settings
func (api *Api) clearUserDelayValues(user *User) {
	user.Delay = 0
	user.SystemDelays = ""
	user.TalkgroupDelays = ""
	log.Printf("Cleared delay values for user %s after group transfer", user.Email)
}

// syncUserConnectionLimit syncs a user's connection limit with their group's connection limit
// This ensures the user's individual limit matches the group's limit
func (api *Api) syncUserConnectionLimit(user *User) {
	if user.UserGroupId > 0 {
		group := api.Controller.UserGroups.Get(user.UserGroupId)
		if group != nil {
			user.ConnectionLimit = group.ConnectionLimit
			log.Printf("Synced connection limit %d from group %s to user %s", group.ConnectionLimit, group.Name, user.Email)
		}
	} else {
		// User not in a group, keep their individual limit
		log.Printf("User %s not in a group, keeping individual connection limit %d", user.Email, user.ConnectionLimit)
	}
}

// handleUserGroupBillingTransition handles Stripe subscription changes when moving users between groups
func (api *Api) handleUserGroupBillingTransition(user *User, oldGroup *UserGroup, newGroup *UserGroup) error {
	// Only handle if Stripe is enabled
	if !api.Controller.Options.StripePaywallEnabled || api.Controller.Options.StripeSecretKey == "" {
		return nil
	}

	stripe.Key = api.Controller.Options.StripeSecretKey

	oldHasBilling := oldGroup != nil && oldGroup.BillingEnabled
	newHasBilling := newGroup != nil && newGroup.BillingEnabled

	log.Printf("Billing transition: user=%s, oldHasBilling=%v, newHasBilling=%v, oldBillingMode=%s, newBillingMode=%s, userCustomerId=%s",
		user.Email, oldHasBilling, newHasBilling,
		func() string {
			if oldGroup != nil {
				return oldGroup.BillingMode
			} else {
				return "none"
			}
		}(),
		func() string {
			if newGroup != nil {
				return newGroup.BillingMode
			} else {
				return "none"
			}
		}(),
		user.StripeCustomerId)

	// Case 1: Moving from billing group to non-billing group - cancel subscription
	if oldHasBilling && !newHasBilling {
		// Only cancel if this user has their own subscription (not group-level)
		if user.StripeSubscriptionId != "" && (oldGroup == nil || oldGroup.BillingMode != "group_admin" || !user.IsGroupAdmin) {
			_, err := subscription.Cancel(user.StripeSubscriptionId, nil)
			if err != nil {
				log.Printf("Failed to cancel Stripe subscription %s for user %s: %v", user.StripeSubscriptionId, user.Email, err)
				return fmt.Errorf("failed to cancel subscription: %w", err)
			}
			log.Printf("Canceled Stripe subscription %s for user %s", user.StripeSubscriptionId, user.Email)

			// Update user subscription status - set to "not_billed" to indicate billing is not required
			user.StripeSubscriptionId = ""
			user.SubscriptionStatus = "not_billed"
			user.PinExpiresAt = 0 // Set PIN to never expire for non-billing groups
			log.Printf("Set PIN expiration to unlimited (0) for user %s moved to non-billing group", user.Email)
			api.Controller.Users.Update(user)
			api.Controller.Users.Write(api.Controller.Database)
			api.Controller.SyncConfigToFile()
		} else {
			// User doesn't have their own subscription, just clear status
			user.SubscriptionStatus = "not_billed"
			user.PinExpiresAt = 0 // Set PIN to never expire for non-billing groups
			log.Printf("Set PIN expiration to unlimited (0) for user %s moved to non-billing group", user.Email)
			api.Controller.Users.Update(user)
			api.Controller.Users.Write(api.Controller.Database)
			api.Controller.SyncConfigToFile()
		}
		return nil
	}

	// Case 2: Moving from non-billing group to billing group - create/reattach customer
	if !oldHasBilling && newHasBilling {
		// Check if group uses shared customer ID for admins
		if newGroup.BillingMode == "group_admin" && user.IsGroupAdmin {
			// Use shared customer ID for group admins
			sharedCustomerId, err := api.getOrCreateGroupSharedCustomerId(newGroup)
			if err != nil {
				log.Printf("Failed to get/create shared customer ID for group %d: %v", newGroup.Id, err)
				return err
			}
			user.StripeCustomerId = sharedCustomerId
			log.Printf("Assigned shared customer ID %s to group admin %s", sharedCustomerId, user.Email)
		} else {
			// Individual customer ID for non-admin users or when billing mode is "all_users"
			if user.StripeCustomerId == "" {
				// Create new Stripe customer
				params := &stripe.CustomerParams{
					Email: stripe.String(user.Email),
					Name:  stripe.String(user.FirstName + " " + user.LastName),
				}

				customer, err := customer.New(params)
				if err != nil {
					log.Printf("Failed to create Stripe customer for user %s: %v", user.Email, err)
					return fmt.Errorf("failed to create Stripe customer: %w", err)
				}
				user.StripeCustomerId = customer.ID
				log.Printf("Created Stripe customer %s for user %s", customer.ID, user.Email)
			} else {
				// Reattach existing customer - update customer email/name if needed
				params := &stripe.CustomerParams{
					Email: stripe.String(user.Email),
					Name:  stripe.String(user.FirstName + " " + user.LastName),
				}
				_, err := customer.Update(user.StripeCustomerId, params)
				if err != nil {
					log.Printf("Failed to update Stripe customer %s for user %s: %v", user.StripeCustomerId, user.Email, err)
					// Continue anyway - customer exists
				}
				log.Printf("Reattached Stripe customer %s for user %s", user.StripeCustomerId, user.Email)
			}

			// For individual users (BillingMode == "all_users"), check if this is a user-initiated transfer
			if newGroup.BillingMode == "all_users" {
				// Check if this transfer is from a group_admin group (user-initiated via "transfer to personal subscription")
				// If so, expire PIN immediately - no grace period
				if oldGroup != nil && oldGroup.BillingMode == "group_admin" {
					// User-initiated transfer - expire immediately, no grace period
					user.PinExpiresAt = uint64(time.Now().Unix())
					user.SubscriptionStatus = "incomplete" // Set explicit status - they need to subscribe
					log.Printf("Set user %s PIN to expire immediately after user-initiated transfer to all_users billing, customer ID: %s", user.Email, user.StripeCustomerId)
				} else {
					// Admin-initiated transfer - give grace period
					gracePeriodDays := 15
					if api.Controller.Options.StripeGracePeriodDays > 0 {
						gracePeriodDays = int(api.Controller.Options.StripeGracePeriodDays)
					}
					user.PinExpiresAt = uint64(time.Now().Unix() + int64(gracePeriodDays*24*60*60))
					user.SubscriptionStatus = "incomplete" // Set explicit status - they need to subscribe
					log.Printf("Set user %s to grace period (%d days) after admin-initiated transfer to billing group (all_users mode), customer ID: %s", user.Email, gracePeriodDays, user.StripeCustomerId)
				}
				// Save user with updated customer ID and PIN expiration
				api.Controller.Users.Update(user)
				api.Controller.Users.Write(api.Controller.Database)
				api.Controller.SyncConfigToFile()
			}
		}

		// For group_admin mode, set status based on whether user is admin
		if newGroup.BillingMode == "group_admin" {
			if user.IsGroupAdmin {
				// Admin - they need to set up subscription
				user.SubscriptionStatus = ""
				user.StripeSubscriptionId = ""
			} else {
				// Non-admin - check if group already has an active subscription from an admin
				allUsers := api.Controller.Users.GetAllUsers()
				hasActiveAdmin := false
				for _, admin := range allUsers {
					if admin.UserGroupId == newGroup.Id && admin.IsGroupAdmin && admin.SubscriptionStatus == "active" {
						// Sync from this admin
						user.SubscriptionStatus = admin.SubscriptionStatus
						user.PinExpiresAt = admin.PinExpiresAt
						hasActiveAdmin = true
						log.Printf("Synced subscription status from admin %s to user %s after transfer to group", admin.Email, user.Email)
						break
					}
				}
				if !hasActiveAdmin {
					// No active admin subscription yet - billing is managed by group admin
					user.SubscriptionStatus = "group_admin_managed"
					user.StripeSubscriptionId = ""
				}
			}
		}

		api.Controller.Users.Update(user)
		api.Controller.Users.Write(api.Controller.Database)
		api.Controller.SyncConfigToFile()

		return nil
	}

	// Case 3: Moving from billing group to billing group - handle customer ID and subscription status
	if oldHasBilling && newHasBilling {
		log.Printf("Moving user %s between billing groups (old: %d mode=%s, new: %d mode=%s)",
			user.Email, oldGroup.Id, oldGroup.BillingMode, newGroup.Id, newGroup.BillingMode)

		// Handle subscription status based on new group's billing mode
		if newGroup.BillingMode == "all_users" {
			// Cancel old subscription if it exists and was individual
			if user.StripeSubscriptionId != "" && (oldGroup == nil || oldGroup.BillingMode != "group_admin" || !user.IsGroupAdmin) {
				_, err := subscription.Cancel(user.StripeSubscriptionId, nil)
				if err != nil {
					log.Printf("Warning: Failed to cancel old subscription %s for user %s: %v", user.StripeSubscriptionId, user.Email, err)
					// Continue anyway
				} else {
					log.Printf("Canceled old subscription %s for user %s", user.StripeSubscriptionId, user.Email)
				}
				user.StripeSubscriptionId = ""
			}

			// Ensure customer ID exists for all_users mode
			if user.StripeCustomerId == "" {
				params := &stripe.CustomerParams{
					Email: stripe.String(user.Email),
					Name:  stripe.String(user.FirstName + " " + user.LastName),
				}

				customer, err := customer.New(params)
				if err != nil {
					log.Printf("Failed to create Stripe customer for user %s: %v", user.Email, err)
					return fmt.Errorf("failed to create Stripe customer: %w", err)
				}
				user.StripeCustomerId = customer.ID
				log.Printf("Created Stripe customer %s for user %s (moving to all_users billing)", customer.ID, user.Email)
			}

			// For user-initiated transfers (from group_admin to all_users), expire PIN immediately
			// They need to subscribe right away - no grace period
			if oldGroup != nil && oldGroup.BillingMode == "group_admin" {
				// User-initiated transfer - expire immediately, no grace period
				user.PinExpiresAt = uint64(time.Now().Unix())
				user.SubscriptionStatus = "incomplete" // Set explicit status - they need to subscribe
				log.Printf("Set user %s PIN to expire immediately after user-initiated transfer to all_users billing, customer ID: %s", user.Email, user.StripeCustomerId)
			} else {
				// Admin-initiated transfer - give grace period
				gracePeriodDays := 15
				if api.Controller.Options.StripeGracePeriodDays > 0 {
					gracePeriodDays = int(api.Controller.Options.StripeGracePeriodDays)
				}
				user.PinExpiresAt = uint64(time.Now().Unix() + int64(gracePeriodDays*24*60*60))
				user.SubscriptionStatus = "incomplete" // Set explicit status - they need to subscribe
				log.Printf("Set user %s to grace period (%d days) after admin-initiated transfer between billing groups (all_users mode), customer ID: %s", user.Email, gracePeriodDays, user.StripeCustomerId)
			}
		} else if newGroup.BillingMode == "group_admin" {
			// Moving to group_admin mode
			if user.IsGroupAdmin {
				// Admin - get or create shared customer ID
				sharedCustomerId, err := api.getOrCreateGroupSharedCustomerId(newGroup)
				if err != nil {
					log.Printf("Failed to get/create shared customer ID for group %d: %v", newGroup.Id, err)
					return err
				}
				user.StripeCustomerId = sharedCustomerId
				user.SubscriptionStatus = ""
				user.StripeSubscriptionId = ""
				log.Printf("Assigned shared customer ID %s to group admin %s (moving to group_admin billing)", sharedCustomerId, user.Email)
			} else {
				// Non-admin - check if group already has an active subscription from an admin
				allUsers := api.Controller.Users.GetAllUsers()
				hasActiveAdmin := false
				for _, admin := range allUsers {
					if admin.UserGroupId == newGroup.Id && admin.IsGroupAdmin && admin.SubscriptionStatus == "active" {
						// Sync from this admin
						user.SubscriptionStatus = admin.SubscriptionStatus
						user.PinExpiresAt = admin.PinExpiresAt
						hasActiveAdmin = true
						log.Printf("Synced subscription status from admin %s to user %s after transfer to group_admin group", admin.Email, user.Email)
						break
					}
				}
				if !hasActiveAdmin {
					// No active admin subscription yet - billing is managed by group admin
					user.SubscriptionStatus = "group_admin_managed"
					user.StripeSubscriptionId = ""
				}
			}
		}

		// Save user after updating customer ID and subscription status
		api.Controller.Users.Update(user)
		api.Controller.Users.Write(api.Controller.Database)
		api.Controller.SyncConfigToFile()

		return nil
	}

	return nil
}

// sendGroupAdminNotification sends email to all group admins in a group
func (api *Api) sendGroupAdminNotification(groupId uint64, movedUser *User, newGroup *UserGroup) {
	if !api.Controller.Options.EmailServiceEnabled {
		return
	}

	// Get the old group (the group the user was moved from)
	oldGroup := api.Controller.UserGroups.Get(groupId)
	if oldGroup == nil {
		return
	}

	// Get all users in the group
	allUsers := api.Controller.Users.GetAllUsers()
	groupAdmins := []*User{}

	for _, u := range allUsers {
		if u.UserGroupId == groupId && u.IsGroupAdmin && u.Email != "" {
			groupAdmins = append(groupAdmins, u)
		}
	}

	// Send email to each group admin
	for _, admin := range groupAdmins {
		err := api.Controller.EmailService.SendUserMovedFromGroupEmail(admin, movedUser, oldGroup, newGroup)
		if err != nil {
			log.Printf("Failed to send email to group admin %s: %v", admin.Email, err)
		}
	}
}

// sendTransferRequestNotification sends email to group admins when a transfer request is created
func (api *Api) sendTransferRequestNotification(transferReq *TransferRequest, targetUser *User, fromGroup *UserGroup, toGroup *UserGroup) {
	if !api.Controller.Options.EmailServiceEnabled {
		return
	}

	// Get all group admins in the target group
	allUsers := api.Controller.Users.GetAllUsers()
	groupAdmins := []*User{}

	for _, u := range allUsers {
		if u.UserGroupId == toGroup.Id && u.IsGroupAdmin && u.Email != "" {
			groupAdmins = append(groupAdmins, u)
		}
	}

	// Generate approval token and store in database
	approvalToken, err := api.generateTransferApprovalToken(transferReq)
	if err != nil {
		log.Printf("Failed to generate approval token for transfer request %d: %v", transferReq.Id, err)
		// Continue without token - email will be sent but approval link won't work
		approvalToken = ""
	}

	// Send email to each group admin
	for _, admin := range groupAdmins {
		err := api.Controller.EmailService.SendTransferRequestEmail(admin, transferReq, targetUser, fromGroup, toGroup, approvalToken)
		if err != nil {
			log.Printf("Failed to send transfer request email to group admin %s: %v", admin.Email, err)
		}
	}
}

// generateTransferApprovalToken generates a secure token for transfer approval and stores it in the database
func (api *Api) generateTransferApprovalToken(transferReq *TransferRequest) (string, error) {
	// Generate a secure random token using crypto/rand
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate random token: %w", err)
	}

	// Encode token as hexadecimal string
	token := fmt.Sprintf("%x", buf)

	// Set expiration to 7 days from now
	expiresAt := time.Now().Add(7 * 24 * time.Hour).Unix()

	// Store token in transfer request
	transferReq.ApprovalToken = token
	transferReq.ApprovalTokenExpiresAt = expiresAt
	transferReq.ApprovalTokenUsed = false

	// Update in database
	if err := api.Controller.TransferRequests.Update(transferReq, api.Controller.Database); err != nil {
		return "", fmt.Errorf("failed to store approval token: %w", err)
	}

	return token, nil
}

// Group Admin - Generate Registration Code
func (api *Api) GroupAdminGenerateCodeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	user, group, err := api.getGroupAdminUser(r)
	if err != nil {
		api.exitWithError(w, http.StatusUnauthorized, err.Error())
		return
	}

	var request struct {
		ExpiresAt int64 `json:"expiresAt"` // Unix timestamp, 0 for no expiration
		MaxUses   int   `json:"maxUses"`   // 0 for unlimited
		IsOneTime bool  `json:"isOneTime"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	regCode, err := api.Controller.RegistrationCodes.GenerateCode(group.Id, user.Id, request.ExpiresAt, request.MaxUses, request.IsOneTime)
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to generate code")
		return
	}

	if err := api.Controller.RegistrationCodes.Add(regCode, api.Controller.Database); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to save code")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"code":    regCode.Code,
		"message": "Registration code generated",
	})
}

// Group Admin - Get Registration Codes
func (api *Api) GroupAdminCodesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	_, group, err := api.getGroupAdminUser(r)
	if err != nil {
		api.exitWithError(w, http.StatusUnauthorized, err.Error())
		return
	}

	allCodes := api.Controller.RegistrationCodes.GetAll()
	groupCodes := []map[string]interface{}{}
	for _, code := range allCodes {
		if code.UserGroupId == group.Id {
			groupCodes = append(groupCodes, map[string]interface{}{
				"id":          code.Id,
				"code":        code.Code,
				"expiresAt":   code.ExpiresAt,
				"maxUses":     code.MaxUses,
				"currentUses": code.CurrentUses,
				"isOneTime":   code.IsOneTime,
				"isActive":    code.IsActive,
				"createdAt":   code.CreatedAt,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"codes": groupCodes,
	})
}

// Group Admin - Delete Registration Code
func (api *Api) GroupAdminDeleteCodeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	_, group, err := api.getGroupAdminUser(r)
	if err != nil {
		api.exitWithError(w, http.StatusUnauthorized, err.Error())
		return
	}

	// Extract code ID from URL path
	// Path format: /api/group-admin/codes/{codeId}
	path := strings.TrimPrefix(r.URL.Path, "/api/group-admin/codes/")
	if path == r.URL.Path {
		api.exitWithError(w, http.StatusBadRequest, "Invalid path format")
		return
	}

	codeID, err := strconv.ParseUint(path, 10, 64)
	if err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid code ID format")
		return
	}

	// Verify code exists and belongs to the group admin's group
	allCodes := api.Controller.RegistrationCodes.GetAll()
	var codeToDelete *RegistrationCode
	for _, code := range allCodes {
		if code.Id == codeID && code.UserGroupId == group.Id {
			codeToDelete = code
			break
		}
	}

	if codeToDelete == nil {
		api.exitWithError(w, http.StatusNotFound, "Registration code not found or access denied")
		return
	}

	// Delete the code
	if err := api.Controller.RegistrationCodes.Delete(codeID, api.Controller.Database); err != nil {
		log.Printf("Error deleting registration code: %v", err)
		api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete code: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Registration code deleted successfully",
	})
}

// System Admin - Get All Groups
func (api *Api) AdminGroupsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check admin authentication
	client := api.getClient(r)
	if client == nil || !api.isAdmin(client) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Unauthorized",
		})
		return
	}

	groups := api.Controller.UserGroups.GetAll()
	groupList := []map[string]interface{}{}
	for _, group := range groups {
		groupList = append(groupList, map[string]interface{}{
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
			"pricingOptions":        group.GetPricingOptions(),
			"billingMode":           group.BillingMode,
			"collectSalesTax":       group.CollectSalesTax,
			"isPublicRegistration":  group.IsPublicRegistration,
			"allowAddExistingUsers": group.AllowAddExistingUsers,
			"createdAt":             group.CreatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"groups": groupList,
	})
}

// System Admin - Create Group
func (api *Api) AdminCreateGroupHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check admin authentication
	client := api.getClient(r)
	if client == nil || !api.isAdmin(client) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Unauthorized",
		})
		return
	}

	var request struct {
		Name                  string          `json:"name"`
		Description           string          `json:"description"`
		SystemAccess          string          `json:"systemAccess"`
		Delay                 int             `json:"delay"`
		SystemDelays          string          `json:"systemDelays"`
		TalkgroupDelays       string          `json:"talkgroupDelays"`
		ConnectionLimit       uint            `json:"connectionLimit"`
		MaxUsers              uint            `json:"maxUsers"`
		BillingEnabled        bool            `json:"billingEnabled"`
		StripePriceId         string          `json:"stripePriceId"`
		PricingOptions        []PricingOption `json:"pricingOptions"`
		BillingMode           string          `json:"billingMode"`
		CollectSalesTax       bool            `json:"collectSalesTax"`
		IsPublicRegistration  bool            `json:"isPublicRegistration"`
		AllowAddExistingUsers bool            `json:"allowAddExistingUsers"`
		// Group admin assignment
		AssignExistingUserAsAdmin bool   `json:"assignExistingUserAsAdmin"`
		GroupAdminUserId          uint64 `json:"groupAdminUserId"`
		CreateNewUserAsAdmin      bool   `json:"createNewUserAsAdmin"`
		NewGroupAdminEmail        string `json:"newGroupAdminEmail"`
		NewGroupAdminPassword     string `json:"newGroupAdminPassword"`
		NewGroupAdminFirstName    string `json:"newGroupAdminFirstName"`
		NewGroupAdminLastName     string `json:"newGroupAdminLastName"`
		NewGroupAdminZipCode      string `json:"newGroupAdminZipCode"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if request.Name == "" {
		api.exitWithError(w, http.StatusBadRequest, "Group name is required")
		return
	}

	// Validate: If billing is enabled, at least one pricing option is required
	if request.BillingEnabled && len(request.PricingOptions) == 0 {
		api.exitWithError(w, http.StatusBadRequest, "At least one pricing option is required when billing is enabled")
		return
	}

	// Validate pricing options (max 3, all fields required)
	if len(request.PricingOptions) > 3 {
		api.exitWithError(w, http.StatusBadRequest, "Maximum 3 pricing options allowed")
		return
	}
	for i, opt := range request.PricingOptions {
		if opt.PriceId == "" || opt.Label == "" || opt.Amount == "" {
			api.exitWithError(w, http.StatusBadRequest, fmt.Sprintf("Pricing option %d is missing required fields", i+1))
			return
		}
	}

	// Convert pricing options to JSON string
	pricingOptionsJSON := ""
	if len(request.PricingOptions) > 0 {
		if jsonBytes, err := json.Marshal(request.PricingOptions); err == nil {
			pricingOptionsJSON = string(jsonBytes)
		}
	}

	// If setting as public registration, unset any existing public registration group
	if request.IsPublicRegistration {
		existingPublic := api.Controller.UserGroups.GetPublicRegistrationGroup()
		if existingPublic != nil {
			existingPublic.IsPublicRegistration = false
			api.Controller.UserGroups.Update(existingPublic, api.Controller.Database)
			// Sync config to file if enabled
			api.Controller.SyncConfigToFile()
		}
	}

	// Set default billing mode if not provided
	billingMode := request.BillingMode
	if billingMode == "" {
		billingMode = "all_users"
	}

	group := &UserGroup{
		Name:                  request.Name,
		Description:           request.Description,
		SystemAccess:          request.SystemAccess,
		Delay:                 request.Delay,
		SystemDelays:          request.SystemDelays,
		TalkgroupDelays:       request.TalkgroupDelays,
		ConnectionLimit:       request.ConnectionLimit,
		MaxUsers:              request.MaxUsers,
		BillingEnabled:        request.BillingEnabled,
		StripePriceId:         request.StripePriceId,
		PricingOptions:        pricingOptionsJSON,
		BillingMode:           billingMode,
		CollectSalesTax:       request.CollectSalesTax,
		IsPublicRegistration:  request.IsPublicRegistration,
		AllowAddExistingUsers: request.AllowAddExistingUsers,
		CreatedAt:             time.Now().Unix(),
	}

	if err := api.Controller.UserGroups.Add(group, api.Controller.Database); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to create group")
		return
	}

	// Sync config to file if enabled (delayed to ensure database visibility across connections)
	go func() {
		time.Sleep(2 * time.Second)
		api.Controller.SyncConfigToFile()
	}()

	// Handle group admin assignment
	if request.AssignExistingUserAsAdmin && request.GroupAdminUserId > 0 {
		user := api.Controller.Users.GetUserById(request.GroupAdminUserId)
		if user == nil {
			api.exitWithError(w, http.StatusNotFound, "User not found")
			return
		}

		// Ensure user is in the group
		if user.UserGroupId != group.Id {
			user.UserGroupId = group.Id
			// Clear user's individual delay settings - they will use the group's delay settings
			api.clearUserDelayValues(user)
		}

		user.IsGroupAdmin = true
		api.Controller.Users.Update(user)
		api.Controller.Users.Write(api.Controller.Database)
	} else if request.CreateNewUserAsAdmin {
		// Validate new user fields
		if request.NewGroupAdminEmail == "" || request.NewGroupAdminPassword == "" ||
			request.NewGroupAdminFirstName == "" || request.NewGroupAdminLastName == "" ||
			request.NewGroupAdminZipCode == "" {
			api.exitWithError(w, http.StatusBadRequest, "All user fields are required when creating a new group admin")
			return
		}

		// Validate password strength
		if err := ValidatePassword(request.NewGroupAdminPassword); err != nil {
			api.exitWithError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Check if user already exists
		if existingUser := api.Controller.Users.GetUserByEmail(request.NewGroupAdminEmail); existingUser != nil {
			api.exitWithError(w, http.StatusConflict, "User with this email already exists")
			return
		}

		// Create new user
		user := &User{
			Email:           request.NewGroupAdminEmail,
			FirstName:       request.NewGroupAdminFirstName,
			LastName:        request.NewGroupAdminLastName,
			ZipCode:         request.NewGroupAdminZipCode,
			UserGroupId:     group.Id,
			ConnectionLimit: group.ConnectionLimit, // Inherit group's connection limit
			IsGroupAdmin:    true,
			Verified:        false, // Require email verification
			CreatedAt:       fmt.Sprintf("%d", time.Now().Unix()),
		}

		if err := user.HashPassword(request.NewGroupAdminPassword); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, "Failed to hash password")
			return
		}

		// Generate verification token
		if err := user.GenerateVerificationToken(); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, "Failed to generate verification token")
			return
		}

		// Generate PIN for the user
		pin, err := api.Controller.Users.GenerateUniquePin(0)
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, "Failed to generate PIN")
			return
		}
		user.Pin = pin

		// Save new user
		if err := api.Controller.Users.SaveNewUser(user, api.Controller.Database); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, "Failed to create group admin user")
			return
		}

		// Sync config to file if enabled
		api.Controller.SyncConfigToFile()

		// Send verification email
		if api.Controller.Options.EmailServiceEnabled {
			if err := api.Controller.EmailService.SendVerificationEmail(user); err != nil {
				api.Controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("Failed to send verification email to group admin: %v", err))
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Group created successfully",
		"group": map[string]interface{}{
			"id":   group.Id,
			"name": group.Name,
		},
	})
}

// System Admin - Delete Group
func (api *Api) AdminDeleteGroupHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check admin authentication
	client := api.getClient(r)
	if client == nil || !api.isAdmin(client) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Unauthorized",
		})
		return
	}

	// Extract group ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 4 {
		api.exitWithError(w, http.StatusBadRequest, "Invalid group ID")
		return
	}

	groupIDStr := pathParts[len(pathParts)-1]
	groupID, err := strconv.ParseUint(groupIDStr, 10, 64)
	if err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid group ID format")
		return
	}

	// Get group to check if exists
	group := api.Controller.UserGroups.Get(groupID)
	if group == nil {
		api.exitWithError(w, http.StatusNotFound, "Group not found")
		return
	}

	// Get all users in this group and unassign them
	allUsers := api.Controller.Users.GetAllUsers()
	usersInGroup := []*User{}
	for _, user := range allUsers {
		if user.UserGroupId == groupID {
			usersInGroup = append(usersInGroup, user)
		}
	}

	// Unassign all users from this group (set to 0 = no group)
	for _, user := range usersInGroup {
		user.UserGroupId = 0
		user.IsGroupAdmin = false // Remove group admin status
		api.Controller.Users.Update(user)
	}

	// Write user updates to database
	if len(usersInGroup) > 0 {
		api.Controller.Users.Write(api.Controller.Database)
	}

	// Delete the group
	if err := api.Controller.UserGroups.Delete(groupID, api.Controller.Database); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to delete group")
		return
	}

	// Wait 1 second to ensure database transaction visibility across connection pool
	time.Sleep(1 * time.Second)

	// Reload groups from database to ensure in-memory state matches DB after deletion
	if err := api.Controller.UserGroups.Load(api.Controller.Database); err != nil {
		log.Printf("WARNING: Failed to reload groups after delete: %v", err)
	}

	// Sync config to file if enabled (delayed)
	go func() {
		time.Sleep(1 * time.Second)
		api.Controller.SyncConfigToFile()
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Group deleted successfully",
	})
}

// System Admin - Update Group
func (api *Api) AdminUpdateGroupHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check admin authentication
	client := api.getClient(r)
	if client == nil || !api.isAdmin(client) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Unauthorized",
		})
		return
	}

	var request struct {
		Id                    uint64          `json:"id"`
		Name                  string          `json:"name"`
		Description           string          `json:"description"`
		SystemAccess          string          `json:"systemAccess"`
		Delay                 int             `json:"delay"`
		SystemDelays          string          `json:"systemDelays"`
		TalkgroupDelays       string          `json:"talkgroupDelays"`
		ConnectionLimit       uint            `json:"connectionLimit"`
		MaxUsers              uint            `json:"maxUsers"`
		BillingEnabled        bool            `json:"billingEnabled"`
		StripePriceId         string          `json:"stripePriceId"`
		PricingOptions        []PricingOption `json:"pricingOptions"`
		BillingMode           string          `json:"billingMode"`
		CollectSalesTax       bool            `json:"collectSalesTax"`
		IsPublicRegistration  bool            `json:"isPublicRegistration"`
		AllowAddExistingUsers bool            `json:"allowAddExistingUsers"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	group := api.Controller.UserGroups.Get(request.Id)
	if group == nil {
		api.exitWithError(w, http.StatusNotFound, "Group not found")
		return
	}

	// Validate: If billing is enabled, at least one pricing option is required
	if request.BillingEnabled && len(request.PricingOptions) == 0 {
		api.exitWithError(w, http.StatusBadRequest, "At least one pricing option is required when billing is enabled")
		return
	}

	// Validate pricing options (max 3, all fields required)
	if len(request.PricingOptions) > 3 {
		api.exitWithError(w, http.StatusBadRequest, "Maximum 3 pricing options allowed")
		return
	}
	for i, opt := range request.PricingOptions {
		if opt.PriceId == "" || opt.Label == "" || opt.Amount == "" {
			api.exitWithError(w, http.StatusBadRequest, fmt.Sprintf("Pricing option %d is missing required fields", i+1))
			return
		}
	}

	// Convert pricing options to JSON string
	pricingOptionsJSON := ""
	if len(request.PricingOptions) > 0 {
		if jsonBytes, err := json.Marshal(request.PricingOptions); err == nil {
			pricingOptionsJSON = string(jsonBytes)
		}
	}

	// If setting as public registration, unset any existing public registration group
	if request.IsPublicRegistration && !group.IsPublicRegistration {
		existingPublic := api.Controller.UserGroups.GetPublicRegistrationGroup()
		if existingPublic != nil && existingPublic.Id != group.Id {
			existingPublic.IsPublicRegistration = false
			api.Controller.UserGroups.Update(existingPublic, api.Controller.Database)
			// Sync config to file if enabled
			api.Controller.SyncConfigToFile()
		}
	}

	oldBillingEnabled := group.BillingEnabled

	group.Name = request.Name
	group.Description = request.Description
	group.SystemAccess = request.SystemAccess
	group.Delay = request.Delay
	group.SystemDelays = request.SystemDelays
	group.TalkgroupDelays = request.TalkgroupDelays
	group.ConnectionLimit = request.ConnectionLimit
	group.MaxUsers = request.MaxUsers
	group.BillingEnabled = request.BillingEnabled
	group.StripePriceId = request.StripePriceId
	group.PricingOptions = pricingOptionsJSON
	if request.BillingMode != "" {
		group.BillingMode = request.BillingMode
	}
	group.CollectSalesTax = request.CollectSalesTax
	group.IsPublicRegistration = request.IsPublicRegistration
	group.AllowAddExistingUsers = request.AllowAddExistingUsers

	if err := api.Controller.UserGroups.Update(group, api.Controller.Database); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to update group")
		return
	}

	// Reload groups from database to ensure consistency
	api.Controller.UserGroups.Load(api.Controller.Database)

	// Sync connection limit to all users in this group
	allUsers := api.Controller.Users.GetAllUsers()
	usersUpdated := 0
	for _, user := range allUsers {
		if user.UserGroupId == group.Id {
			if user.ConnectionLimit != group.ConnectionLimit {
				user.ConnectionLimit = group.ConnectionLimit
				api.Controller.Users.Update(user)
				usersUpdated++
			}
		}
	}
	if usersUpdated > 0 {
		api.Controller.Users.Write(api.Controller.Database)
		log.Printf("Synced connection limit %d from group %s to %d users", group.ConnectionLimit, group.Name, usersUpdated)
	}

	// Sync config to file if enabled (delayed to ensure database visibility)
	go func() {
		time.Sleep(1 * time.Second)
		api.Controller.SyncConfigToFile()
	}()

	// If billing was just enabled for group_admin mode, assign shared customer ID to all existing admins
	if !oldBillingEnabled && request.BillingEnabled && request.BillingMode == "group_admin" && api.Controller.Options.StripePaywallEnabled && api.Controller.Options.StripeSecretKey != "" {
		sharedCustomerId, err := api.getOrCreateGroupSharedCustomerId(group)
		if err != nil {
			log.Printf("Failed to get/create shared customer ID for group %d when enabling billing: %v", group.Id, err)
			// Continue anyway - billing errors shouldn't block group update
		} else {
			// Assign shared customer ID to all existing group admins
			allUsers := api.Controller.Users.GetAllUsers()
			for _, u := range allUsers {
				if u.UserGroupId == group.Id && u.IsGroupAdmin && u.StripeCustomerId == "" {
					u.StripeCustomerId = sharedCustomerId
					api.Controller.Users.Update(u)
					log.Printf("Assigned shared customer ID %s to existing group admin %s", sharedCustomerId, u.Email)
				}
			}
			api.Controller.Users.Write(api.Controller.Database)
		}
	}

	// Refresh configuration for all active clients belonging to users in this group
	// This ensures they get updated system access and delay settings immediately
	api.Controller.Clients.RefreshConfigForGroup(api.Controller, group.Id)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Group updated successfully",
	})
}

// System Admin - Assign Group Admin
func (api *Api) AdminAssignGroupAdminHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check admin authentication
	client := api.getClient(r)
	if client == nil || !api.isAdmin(client) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Unauthorized",
		})
		return
	}

	var request struct {
		UserId  uint64 `json:"userId"`
		GroupId uint64 `json:"groupId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	user := api.Controller.Users.GetUserById(request.UserId)
	if user == nil {
		api.exitWithError(w, http.StatusNotFound, "User not found")
		return
	}

	group := api.Controller.UserGroups.Get(request.GroupId)
	if group == nil {
		api.exitWithError(w, http.StatusNotFound, "Group not found")
		return
	}

	// Ensure user is in the group
	if user.UserGroupId != group.Id {
		user.UserGroupId = group.Id
		// Clear user's individual delay settings - they will use the group's delay settings
		api.clearUserDelayValues(user)
	}

	user.IsGroupAdmin = true

	// If group has billing enabled with group_admin mode, assign shared customer ID
	if group.BillingEnabled && group.BillingMode == "group_admin" && api.Controller.Options.StripePaywallEnabled && api.Controller.Options.StripeSecretKey != "" {
		sharedCustomerId, err := api.getOrCreateGroupSharedCustomerId(group)
		if err != nil {
			log.Printf("Failed to get/create shared customer ID for group %d when assigning admin: %v", group.Id, err)
			// Continue anyway - billing errors shouldn't block admin assignment
		} else {
			user.StripeCustomerId = sharedCustomerId
			log.Printf("Assigned shared customer ID %s to new group admin %s", sharedCustomerId, user.Email)
		}
	}

	api.Controller.Users.Update(user)
	api.Controller.Users.Write(api.Controller.Database)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Group admin assigned successfully",
	})
}

// System Admin - Remove Group Admin
func (api *Api) AdminRemoveGroupAdminHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check admin authentication
	client := api.getClient(r)
	if client == nil || !api.isAdmin(client) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Unauthorized",
		})
		return
	}

	var request struct {
		UserId  uint64 `json:"userId"`
		GroupId uint64 `json:"groupId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	user := api.Controller.Users.GetUserById(request.UserId)
	if user == nil {
		api.exitWithError(w, http.StatusNotFound, "User not found")
		return
	}

	group := api.Controller.UserGroups.Get(request.GroupId)
	if group == nil {
		api.exitWithError(w, http.StatusNotFound, "Group not found")
		return
	}

	// Verify user is in the group and is a group admin
	if user.UserGroupId != group.Id {
		api.exitWithError(w, http.StatusBadRequest, "User is not in this group")
		return
	}

	if !user.IsGroupAdmin {
		api.exitWithError(w, http.StatusBadRequest, "User is not a group admin")
		return
	}

	user.IsGroupAdmin = false
	api.Controller.Users.Update(user)
	api.Controller.Users.Write(api.Controller.Database)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Group admin removed successfully",
	})
}

// System Admin - Get Group Admins for a Group
func (api *Api) AdminGroupAdminsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check admin authentication
	client := api.getClient(r)
	if client == nil || !api.isAdmin(client) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Unauthorized",
		})
		return
	}

	groupIdStr := r.URL.Query().Get("groupId")
	if groupIdStr == "" {
		api.exitWithError(w, http.StatusBadRequest, "groupId is required")
		return
	}

	groupId, err := strconv.ParseUint(groupIdStr, 10, 64)
	if err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid groupId")
		return
	}

	group := api.Controller.UserGroups.Get(groupId)
	if group == nil {
		api.exitWithError(w, http.StatusNotFound, "Group not found")
		return
	}

	// Get all users in the group who are group admins
	allUsers := api.Controller.Users.GetAllUsers()
	groupAdmins := []map[string]interface{}{}
	for _, u := range allUsers {
		if u.UserGroupId == group.Id && u.IsGroupAdmin {
			groupAdmins = append(groupAdmins, map[string]interface{}{
				"id":        u.Id,
				"email":     u.Email,
				"firstName": u.FirstName,
				"lastName":  u.LastName,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"groupAdmins": groupAdmins,
		"groupId":     groupId,
		"groupName":   group.Name,
	})
}

// System Admin - Get Registration Codes for a Group
func (api *Api) AdminGroupCodesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check admin authentication
	client := api.getClient(r)
	if client == nil || !api.isAdmin(client) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Unauthorized",
		})
		return
	}

	// Extract group ID from URL path
	// Path format: /api/admin/groups/{groupId}/codes
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/groups/")
	path = strings.TrimSuffix(path, "/codes")

	if path == "" || path == r.URL.Path {
		api.exitWithError(w, http.StatusBadRequest, "Invalid path format")
		return
	}

	groupID, err := strconv.ParseUint(path, 10, 64)
	if err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid group ID format")
		return
	}

	// Verify group exists
	group := api.Controller.UserGroups.Get(groupID)
	if group == nil {
		api.exitWithError(w, http.StatusNotFound, "Group not found")
		return
	}

	// Get all codes for this group
	allCodes := api.Controller.RegistrationCodes.GetAll()
	groupCodes := []map[string]interface{}{}
	for _, code := range allCodes {
		if code.UserGroupId == groupID {
			groupCodes = append(groupCodes, map[string]interface{}{
				"id":          code.Id,
				"code":        code.Code,
				"expiresAt":   code.ExpiresAt,
				"maxUses":     code.MaxUses,
				"currentUses": code.CurrentUses,
				"isOneTime":   code.IsOneTime,
				"isActive":    code.IsActive,
				"createdAt":   code.CreatedAt,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"codes": groupCodes,
	})
}

// System Admin - Generate Registration Code for a Group
func (api *Api) AdminGroupGenerateCodeHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("AdminGroupGenerateCodeHandler called: method=%s, path=%s", r.Method, r.URL.Path)

	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check admin authentication
	client := api.getClient(r)
	if client == nil {
		log.Printf("AdminGroupGenerateCodeHandler: client is nil")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Unauthorized",
		})
		return
	}

	if !api.isAdmin(client) {
		log.Printf("AdminGroupGenerateCodeHandler: client is not admin")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Unauthorized",
		})
		return
	}

	log.Printf("AdminGroupGenerateCodeHandler: admin authenticated successfully")

	// Extract group ID from URL path
	// Path format: /api/admin/groups/{groupId}/codes/generate
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/groups/")
	if path == r.URL.Path {
		log.Printf("Error: Path does not start with /api/admin/groups/: %s", r.URL.Path)
		api.exitWithError(w, http.StatusBadRequest, "Invalid path format")
		return
	}

	path = strings.TrimSuffix(path, "/codes/generate")
	if path == "" || strings.Contains(path, "/") {
		log.Printf("Error: Invalid path after trimming: original=%s, trimmed=%s", r.URL.Path, path)
		api.exitWithError(w, http.StatusBadRequest, "Invalid path format")
		return
	}

	groupID, err := strconv.ParseUint(path, 10, 64)
	if err != nil {
		log.Printf("Error parsing group ID: path=%s, error=%v", path, err)
		api.exitWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid group ID format: %s", path))
		return
	}

	log.Printf("Parsed group ID: %d from path: %s", groupID, r.URL.Path)

	// Verify group exists
	group := api.Controller.UserGroups.Get(groupID)
	if group == nil {
		api.exitWithError(w, http.StatusNotFound, "Group not found")
		return
	}

	var request struct {
		ExpiresAt int64 `json:"expiresAt"` // Unix timestamp, 0 for no expiration
		MaxUses   int   `json:"maxUses"`   // 0 for unlimited
		IsOneTime bool  `json:"isOneTime"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Use admin user ID as createdBy (0 if not available)
	createdBy := uint64(0)
	if client != nil && client.User != nil {
		createdBy = client.User.Id
	}

	regCode, err := api.Controller.RegistrationCodes.GenerateCode(groupID, createdBy, request.ExpiresAt, request.MaxUses, request.IsOneTime)
	if err != nil {
		log.Printf("Error generating registration code: %v", err)
		api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to generate code: %v", err))
		return
	}

	if err := api.Controller.RegistrationCodes.Add(regCode, api.Controller.Database); err != nil {
		log.Printf("Error adding registration code to database: %v", err)
		api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to save code: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"code":    regCode.Code,
		"message": "Registration code generated",
	})
}

// System Admin - Delete Registration Code for a Group
func (api *Api) AdminGroupDeleteCodeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check admin authentication
	client := api.getClient(r)
	if client == nil || !api.isAdmin(client) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Unauthorized",
		})
		return
	}

	// Extract code ID from URL path
	// Path format: /api/admin/groups/{groupId}/codes/{codeId}
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/groups/")
	if path == r.URL.Path {
		api.exitWithError(w, http.StatusBadRequest, "Invalid path format")
		return
	}

	// Path should be: {groupId}/codes/{codeId}
	pathParts := strings.Split(path, "/")
	if len(pathParts) != 3 || pathParts[1] != "codes" {
		api.exitWithError(w, http.StatusBadRequest, "Invalid path format")
		return
	}

	codeIDStr := pathParts[2] // Last part is the code ID
	codeID, err := strconv.ParseUint(codeIDStr, 10, 64)
	if err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid code ID format")
		return
	}

	// Verify code exists and belongs to a valid group
	allCodes := api.Controller.RegistrationCodes.GetAll()
	var codeToDelete *RegistrationCode
	for _, code := range allCodes {
		if code.Id == codeID {
			codeToDelete = code
			break
		}
	}

	if codeToDelete == nil {
		api.exitWithError(w, http.StatusNotFound, "Registration code not found")
		return
	}

	// Delete the code
	if err := api.Controller.RegistrationCodes.Delete(codeID, api.Controller.Database); err != nil {
		log.Printf("Error deleting registration code: %v", err)
		api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete code: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Registration code deleted successfully",
	})
}

// System Admin - Send User Invitation
func (api *Api) AdminInviteUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check admin authentication
	client := api.getClient(r)
	if client == nil || !api.isAdmin(client) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Unauthorized",
		})
		return
	}

	var request struct {
		Email   string `json:"email"`
		GroupId uint64 `json:"groupId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if request.Email == "" {
		api.exitWithError(w, http.StatusBadRequest, "Email is required")
		return
	}

	// Validate email format (basic check)
	if !strings.Contains(request.Email, "@") {
		api.exitWithError(w, http.StatusBadRequest, "Invalid email format")
		return
	}

	// Get the group
	group := api.Controller.UserGroups.Get(request.GroupId)
	if group == nil {
		api.exitWithError(w, http.StatusNotFound, "Group not found")
		return
	}

	// Check if user already exists
	existingUser := api.Controller.Users.GetUserByEmail(request.Email)
	if existingUser != nil {
		api.exitWithError(w, http.StatusConflict, "User with this email already exists")
		return
	}

	// Generate unique invitation code
	code, err := generateInvitationCode()
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to generate invitation code")
		return
	}

	// Get admin user ID (nil for system admin)
	var adminUserId interface{}
	if client.User != nil {
		adminUserId = client.User.Id
	} else {
		adminUserId = nil // System admin - no user record
	}

	// Set expiration to 7 days from now
	expiresAt := time.Now().Add(7 * 24 * time.Hour).Unix()
	invitedAt := time.Now().Unix()

	// Insert invitation into database
	var invitationId int64
	err = api.Controller.Database.Sql.QueryRow(
		`INSERT INTO "userInvitations" ("email", "code", "userGroupId", "invitedBy", "invitedAt", "expiresAt", "status") 
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING "userInvitationId"`,
		request.Email, code, request.GroupId, adminUserId, invitedAt, expiresAt, "pending",
	).Scan(&invitationId)

	if err != nil {
		log.Printf("Error creating invitation: %v", err)
		api.exitWithError(w, http.StatusInternalServerError, "Failed to create invitation")
		return
	}

	// Send invitation email if email service is enabled
	if api.Controller.Options.EmailServiceEnabled {
		baseUrl := api.Controller.Options.BaseUrl
		if baseUrl == "" {
			baseUrl = "https://localhost:8080"
		} else {
			if strings.HasPrefix(baseUrl, "http://") {
				baseUrl = strings.Replace(baseUrl, "http://", "https://", 1)
			} else if !strings.HasPrefix(baseUrl, "https://") {
				baseUrl = "https://" + baseUrl
			}
		}
		invitationLink := baseUrl + "/?invite=" + code

		// Get branding
		branding := api.Controller.Options.Branding
		if branding == "" {
			branding = "ThinLine Radio"
		}

		// Send invitation email
		if err := api.Controller.EmailService.SendInvitationEmail(request.Email, code, invitationLink, group.Name, branding); err != nil {
			log.Printf("Warning: Failed to send invitation email: %v", err)
			// Don't fail the request if email fails, just log it
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":      "Invitation sent successfully",
		"invitationId": invitationId,
		"code":         code,
	})
}

// Group Admin - Send User Invitation (to their group only)
func (api *Api) GroupAdminInviteUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check group admin authentication
	groupAdminUser, group, err := api.getGroupAdminUser(r)
	if err != nil {
		api.exitWithError(w, http.StatusUnauthorized, err.Error())
		return
	}

	var request struct {
		Email string `json:"email"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if request.Email == "" {
		api.exitWithError(w, http.StatusBadRequest, "Email is required")
		return
	}

	// Validate email format (basic check)
	if !strings.Contains(request.Email, "@") {
		api.exitWithError(w, http.StatusBadRequest, "Invalid email format")
		return
	}

	// Check if group has reached max users limit
	if group.MaxUsers > 0 {
		allUsers := api.Controller.Users.GetAllUsers()
		currentUserCount := 0
		for _, u := range allUsers {
			if u.UserGroupId == group.Id {
				currentUserCount++
			}
		}
		if currentUserCount >= int(group.MaxUsers) {
			api.exitWithError(w, http.StatusForbidden, fmt.Sprintf("Group has reached maximum user limit of %d", group.MaxUsers))
			return
		}
	}

	// Check if user already exists
	existingUser := api.Controller.Users.GetUserByEmail(request.Email)
	if existingUser != nil {
		api.exitWithError(w, http.StatusConflict, "User with this email already exists")
		return
	}

	// Generate unique invitation code
	code, err := generateInvitationCode()
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to generate invitation code")
		return
	}

	// Set expiration to 7 days from now
	expiresAt := time.Now().Add(7 * 24 * time.Hour).Unix()
	invitedAt := time.Now().Unix()

	// Insert invitation into database (invited by group admin)
	var invitationId int64
	err = api.Controller.Database.Sql.QueryRow(
		`INSERT INTO "userInvitations" ("email", "code", "userGroupId", "invitedBy", "invitedAt", "expiresAt", "status") 
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING "userInvitationId"`,
		request.Email, code, group.Id, groupAdminUser.Id, invitedAt, expiresAt, "pending",
	).Scan(&invitationId)

	if err != nil {
		log.Printf("Error creating invitation: %v", err)
		api.exitWithError(w, http.StatusInternalServerError, "Failed to create invitation")
		return
	}

	// Send invitation email if email service is enabled
	if api.Controller.Options.EmailServiceEnabled {
		baseUrl := api.Controller.Options.BaseUrl
		if baseUrl == "" {
			baseUrl = "https://localhost:8080"
		} else {
			if strings.HasPrefix(baseUrl, "http://") {
				baseUrl = strings.Replace(baseUrl, "http://", "https://", 1)
			} else if !strings.HasPrefix(baseUrl, "https://") {
				baseUrl = "https://" + baseUrl
			}
		}
		invitationLink := baseUrl + "/?invite=" + code

		// Get branding
		branding := api.Controller.Options.Branding
		if branding == "" {
			branding = "ThinLine Radio"
		}

		// Send invitation email
		if err := api.Controller.EmailService.SendInvitationEmail(request.Email, code, invitationLink, group.Name, branding); err != nil {
			log.Printf("Warning: Failed to send invitation email: %v", err)
			// Don't fail the request if email fails, just log it
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":      "Invitation sent successfully",
		"invitationId": invitationId,
		"code":         code,
	})
}

// Validate Invitation Code - Public endpoint
func (api *Api) ValidateInvitationHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		api.exitWithError(w, http.StatusBadRequest, "Invitation code is required")
		return
	}

	// Query invitation from database
	var invitation struct {
		Id          int64
		Email       string
		UserGroupId uint64
		Status      string
		ExpiresAt   int64
		UsedAt      sql.NullInt64
	}

	err := api.Controller.Database.Sql.QueryRow(
		`SELECT "userInvitationId", "email", "userGroupId", "status", "expiresAt", "usedAt" 
		 FROM "userInvitations" WHERE "code" = $1`,
		code,
	).Scan(&invitation.Id, &invitation.Email, &invitation.UserGroupId, &invitation.Status, &invitation.ExpiresAt, &invitation.UsedAt)

	if err == sql.ErrNoRows {
		api.exitWithError(w, http.StatusNotFound, "Invitation not found")
		return
	}
	if err != nil {
		log.Printf("Error validating invitation: %v", err)
		api.exitWithError(w, http.StatusInternalServerError, "Failed to validate invitation")
		return
	}

	// Check if invitation is already used
	if invitation.UsedAt.Valid && invitation.UsedAt.Int64 > 0 {
		api.exitWithError(w, http.StatusBadRequest, "Invitation has already been used")
		return
	}

	// Check if invitation is expired
	if invitation.ExpiresAt > 0 && time.Now().Unix() > invitation.ExpiresAt {
		api.exitWithError(w, http.StatusBadRequest, "Invitation has expired")
		return
	}

	// Check if invitation status is valid
	if invitation.Status != "pending" {
		api.exitWithError(w, http.StatusBadRequest, "Invitation is not valid")
		return
	}

	// Get group information
	group := api.Controller.UserGroups.Get(invitation.UserGroupId)
	if group == nil {
		api.exitWithError(w, http.StatusNotFound, "Group not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"valid":     true,
		"email":     invitation.Email,
		"groupId":   invitation.UserGroupId,
		"groupName": group.Name,
		"expiresAt": invitation.ExpiresAt,
	})
}

// Helper function to generate invitation code
func generateInvitationCode() (string, error) {
	const codeLength = 16
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	buf := make([]byte, codeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	code := make([]byte, codeLength)
	for i := 0; i < codeLength; i++ {
		code[i] = chars[int(buf[i])%len(chars)]
	}

	return string(code), nil
}

// System Admin - Transfer User to Another Group
func (api *Api) AdminTransferUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check admin authentication
	client := api.getClient(r)
	if client == nil || !api.isAdmin(client) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Unauthorized",
		})
		return
	}

	var request struct {
		UserId    uint64 `json:"userId"`
		ToGroupId uint64 `json:"toGroupId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	targetUser := api.Controller.Users.GetUserById(request.UserId)
	if targetUser == nil {
		api.exitWithError(w, http.StatusNotFound, "User not found")
		return
	}

	toGroup := api.Controller.UserGroups.Get(request.ToGroupId)
	if toGroup == nil {
		api.exitWithError(w, http.StatusNotFound, "Target group not found")
		return
	}

	// Check max users limit for the target group
	if toGroup.MaxUsers > 0 {
		currentUserCount := api.Controller.UserGroups.GetUserCount(toGroup.Id, api.Controller.Users)
		if currentUserCount >= toGroup.MaxUsers {
			api.exitWithError(w, http.StatusForbidden, fmt.Sprintf("Target group has reached maximum user limit of %d", toGroup.MaxUsers))
			return
		}
	}

	// Transfer user (system admin can transfer directly, no approval needed)
	// Remove group admin status if user was a group admin (security: admin status should not persist when moved to another group)
	if targetUser.IsGroupAdmin {
		targetUser.IsGroupAdmin = false
	}
	targetUser.UserGroupId = toGroup.Id
	// Clear user's individual delay settings - they will use the group's delay settings
	api.clearUserDelayValues(targetUser)
	// Sync user's connection limit with the group's connection limit
	api.syncUserConnectionLimit(targetUser)
	api.Controller.Users.Update(targetUser)
	api.Controller.Users.Write(api.Controller.Database)

	// Sync config to file if enabled
	api.Controller.SyncConfigToFile()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "User transferred successfully",
	})
}

// Group Admin - Request User Transfer
func (api *Api) GroupAdminRequestTransferHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	user, group, err := api.getGroupAdminUser(r)
	if err != nil {
		api.exitWithError(w, http.StatusUnauthorized, err.Error())
		return
	}

	var request struct {
		UserId    uint64 `json:"userId"`
		ToGroupId uint64 `json:"toGroupId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	targetUser := api.Controller.Users.GetUserById(request.UserId)
	if targetUser == nil {
		api.exitWithError(w, http.StatusNotFound, "User not found")
		return
	}

	if targetUser.UserGroupId != group.Id {
		api.exitWithError(w, http.StatusForbidden, "User is not in your group")
		return
	}

	toGroup := api.Controller.UserGroups.Get(request.ToGroupId)
	if toGroup == nil {
		api.exitWithError(w, http.StatusNotFound, "Target group not found")
		return
	}

	// Check if it's a public registration group - auto-approve
	if toGroup.IsPublicRegistration {
		// Get the old group for billing transition and notifications
		oldGroup := api.Controller.UserGroups.Get(targetUser.UserGroupId)

		// Handle billing transitions (cancel/create subscriptions as needed)
		if err := api.handleUserGroupBillingTransition(targetUser, oldGroup, toGroup); err != nil {
			log.Printf("Warning: Failed to handle billing transition for user %s: %v", targetUser.Email, err)
			// Continue with transfer anyway
		}

		// Update user group
		oldGroupId := targetUser.UserGroupId
		targetUser.UserGroupId = toGroup.Id
		// Remove group admin status if user was a group admin in the old group
		if targetUser.IsGroupAdmin && oldGroupId > 0 {
			targetUser.IsGroupAdmin = false
		}
		// Clear user's individual delay settings - they will use the group's delay settings
		api.clearUserDelayValues(targetUser)
		// Sync user's connection limit with the group's connection limit
		api.syncUserConnectionLimit(targetUser)
		api.Controller.Users.Update(targetUser)
		api.Controller.Users.Write(api.Controller.Database)

		// Sync config to file if enabled
		api.Controller.SyncConfigToFile()

		// Send email notifications asynchronously
		go func() {
			// Email the user about the group change
			if api.Controller.Options.EmailServiceEnabled {
				api.Controller.EmailService.SendUserGroupChangeEmail(targetUser, toGroup, oldGroup)
			}

			// Email original group admin(s) if they exist
			if oldGroupId > 0 {
				api.sendGroupAdminNotification(oldGroupId, targetUser, toGroup)
			}
		}()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "User transferred to public registration group",
		})
		return
	}

	// Check if target group has any group admins - if not, auto-approve
	allUsers := api.Controller.Users.GetAllUsers()
	hasGroupAdmin := false
	for _, u := range allUsers {
		if u.UserGroupId == toGroup.Id && u.IsGroupAdmin {
			hasGroupAdmin = true
			break
		}
	}

	// If no group admin exists, auto-approve the transfer
	if !hasGroupAdmin {
		// Check max users limit for the target group
		if toGroup.MaxUsers > 0 {
			currentUserCount := api.Controller.UserGroups.GetUserCount(toGroup.Id, api.Controller.Users)
			if currentUserCount >= toGroup.MaxUsers {
				api.exitWithError(w, http.StatusForbidden, fmt.Sprintf("Target group has reached maximum user limit of %d", toGroup.MaxUsers))
				return
			}
		}

		// Get the old group for billing transition and notifications
		oldGroup := api.Controller.UserGroups.Get(targetUser.UserGroupId)

		// Handle billing transitions (cancel/create subscriptions as needed)
		if err := api.handleUserGroupBillingTransition(targetUser, oldGroup, toGroup); err != nil {
			log.Printf("Warning: Failed to handle billing transition for user %s: %v", targetUser.Email, err)
			// Continue with transfer anyway
		}

		// Update user group
		oldGroupId := targetUser.UserGroupId
		targetUser.UserGroupId = toGroup.Id
		// Remove group admin status if user was a group admin in the old group
		if targetUser.IsGroupAdmin && oldGroupId > 0 {
			targetUser.IsGroupAdmin = false
		}
		// Clear user's individual delay settings - they will use the group's delay settings
		api.clearUserDelayValues(targetUser)
		// Sync user's connection limit with the group's connection limit
		api.syncUserConnectionLimit(targetUser)
		api.Controller.Users.Update(targetUser)
		api.Controller.Users.Write(api.Controller.Database)

		// Sync config to file if enabled
		api.Controller.SyncConfigToFile()

		// Send email notifications asynchronously
		go func() {
			// Email the user about the group change
			if api.Controller.Options.EmailServiceEnabled {
				api.Controller.EmailService.SendUserGroupChangeEmail(targetUser, toGroup, oldGroup)
			}

			// Email original group admin(s) if they exist
			if oldGroupId > 0 {
				api.sendGroupAdminNotification(oldGroupId, targetUser, toGroup)
			}
		}()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "User transferred (auto-approved - no group admin in target group)",
		})
		return
	}

	// Create transfer request (requires approval from group admin)
	transferReq := &TransferRequest{
		UserId:      request.UserId,
		FromGroupId: group.Id,
		ToGroupId:   request.ToGroupId,
		RequestedBy: user.Id,
		Status:      "pending",
		RequestedAt: time.Now().Unix(),
	}

	if err := api.Controller.TransferRequests.Add(transferReq, api.Controller.Database); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to create transfer request")
		return
	}

	// Send email notifications to target group admins
	go func() {
		if api.Controller.Options.EmailServiceEnabled {
			api.sendTransferRequestNotification(transferReq, targetUser, group, toGroup)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":   "Transfer request created",
		"requestId": transferReq.Id,
	})
}

// Group Admin - Approve/Reject Transfer Request
func (api *Api) GroupAdminApproveTransferHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	user, group, err := api.getGroupAdminUser(r)
	if err != nil {
		api.exitWithError(w, http.StatusUnauthorized, err.Error())
		return
	}

	var request struct {
		RequestId uint64 `json:"requestId"`
		Approve   bool   `json:"approve"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	transferReq := api.Controller.TransferRequests.Get(request.RequestId)
	if transferReq == nil {
		api.exitWithError(w, http.StatusNotFound, "Transfer request not found")
		return
	}

	// Only approve if request is to this group
	if transferReq.ToGroupId != group.Id {
		api.exitWithError(w, http.StatusForbidden, "Transfer request is not for your group")
		return
	}

	if transferReq.Status != "pending" {
		api.exitWithError(w, http.StatusBadRequest, "Transfer request is not pending")
		return
	}

	if request.Approve {
		// Approve transfer
		targetUser := api.Controller.Users.GetUserById(transferReq.UserId)
		if targetUser == nil {
			api.exitWithError(w, http.StatusNotFound, "User not found")
			return
		}

		// Get the old group for billing transition handling
		oldGroup := api.Controller.UserGroups.Get(transferReq.FromGroupId)

		// Handle billing transitions (cancel/create subscriptions as needed)
		if err := api.handleUserGroupBillingTransition(targetUser, oldGroup, group); err != nil {
			log.Printf("Warning: Failed to handle billing transition for user %s: %v", targetUser.Email, err)
			// Continue with transfer anyway
		}

		// Update user group
		oldGroupId := targetUser.UserGroupId
		targetUser.UserGroupId = group.Id
		// Remove group admin status if user was a group admin in the old group
		if targetUser.IsGroupAdmin && oldGroupId > 0 {
			targetUser.IsGroupAdmin = false
		}
		// Clear user's individual delay settings - they will use the group's delay settings
		api.clearUserDelayValues(targetUser)
		// Sync user's connection limit with the group's connection limit
		api.syncUserConnectionLimit(targetUser)
		api.Controller.Users.Update(targetUser)
		api.Controller.Users.Write(api.Controller.Database)

		// Sync config to file if enabled
		api.Controller.SyncConfigToFile()

		// Send email notifications asynchronously
		go func() {
			// Email the user about the group change
			if api.Controller.Options.EmailServiceEnabled {
				api.Controller.EmailService.SendUserGroupChangeEmail(targetUser, group, oldGroup)
			}

			// Email original group admin(s) if they exist
			if oldGroupId > 0 {
				api.sendGroupAdminNotification(oldGroupId, targetUser, group)
			}
		}()

		transferReq.Status = "approved"
		transferReq.ApprovedBy = user.Id
		transferReq.ApprovedAt = time.Now().Unix()
	} else {
		transferReq.Status = "rejected"
		transferReq.ApprovedBy = user.Id
		transferReq.ApprovedAt = time.Now().Unix()
	}

	if err := api.Controller.TransferRequests.Update(transferReq, api.Controller.Database); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, "Failed to update transfer request")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Transfer request updated",
	})
}

// Group Admin - Approve Transfer Request via Email Link (no login required)
func (api *Api) GroupAdminApproveTransferLinkHandler(w http.ResponseWriter, r *http.Request) {
	// Handle root path requests (for old email links or URL normalization issues)
	if r.URL.Path == "/" {
		requestId := r.URL.Query().Get("requestId")
		token := r.URL.Query().Get("token")
		if requestId != "" && token != "" {
			newURL := fmt.Sprintf("/approve-transfer?requestId=%s&token=%s", requestId, token)
			http.Redirect(w, r, newURL, http.StatusTemporaryRedirect)
			return
		}
	}

	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	requestIdStr := r.URL.Query().Get("requestId")
	token := r.URL.Query().Get("token")

	if requestIdStr == "" || token == "" {
		api.sendTransferApprovalPage(w, false, "Missing request ID or token")
		return
	}

	requestId, err := strconv.ParseUint(requestIdStr, 10, 64)
	if err != nil {
		api.sendTransferApprovalPage(w, false, "Invalid request ID")
		return
	}

	// Get transfer request from database
	transferReq := api.Controller.TransferRequests.Get(requestId)
	if transferReq == nil {
		api.sendTransferApprovalPage(w, false, "Transfer request not found")
		return
	}

	if transferReq.Status != "pending" {
		api.sendTransferApprovalPage(w, false, fmt.Sprintf("Transfer request is already %s", transferReq.Status))
		return
	}

	// Validate token from database
	if transferReq.ApprovalToken == "" {
		api.sendTransferApprovalPage(w, false, "No approval token found for this transfer request")
		return
	}

	if transferReq.ApprovalTokenUsed {
		api.sendTransferApprovalPage(w, false, "Approval token has already been used")
		return
	}

	if transferReq.ApprovalTokenExpiresAt > 0 && transferReq.ApprovalTokenExpiresAt < time.Now().Unix() {
		api.sendTransferApprovalPage(w, false, "Approval token has expired")
		return
	}

	// Verify token matches
	if transferReq.ApprovalToken != token {
		api.sendTransferApprovalPage(w, false, "Invalid approval token")
		return
	}

	// Get the target group
	toGroup := api.Controller.UserGroups.Get(transferReq.ToGroupId)
	if toGroup == nil {
		api.sendTransferApprovalPage(w, false, "Target group not found")
		return
	}

	// Check max users limit for the target group
	if toGroup.MaxUsers > 0 {
		currentUserCount := api.Controller.UserGroups.GetUserCount(toGroup.Id, api.Controller.Users)
		if currentUserCount >= toGroup.MaxUsers {
			api.sendTransferApprovalPage(w, false, fmt.Sprintf("Target group has reached maximum user limit of %d", toGroup.MaxUsers))
			return
		}
	}

	// Get the user to transfer
	targetUser := api.Controller.Users.GetUserById(transferReq.UserId)
	if targetUser == nil {
		api.sendTransferApprovalPage(w, false, "User not found")
		return
	}

	// Get the old group for billing transition handling
	oldGroup := api.Controller.UserGroups.Get(transferReq.FromGroupId)

	// Handle billing transitions (cancel/create subscriptions as needed)
	if err := api.handleUserGroupBillingTransition(targetUser, oldGroup, toGroup); err != nil {
		log.Printf("Warning: Failed to handle billing transition for user %s: %v", targetUser.Email, err)
		// Continue with transfer anyway
	}

	// Update user group
	oldGroupId := targetUser.UserGroupId
	targetUser.UserGroupId = toGroup.Id
	// Remove group admin status if user was a group admin in the old group
	if targetUser.IsGroupAdmin && oldGroupId > 0 {
		targetUser.IsGroupAdmin = false
	}
	// Clear user's individual delay settings - they will use the group's delay settings
	api.clearUserDelayValues(targetUser)
	api.Controller.Users.Update(targetUser)
	api.Controller.Users.Write(api.Controller.Database)

	// Sync config to file if enabled
	api.Controller.SyncConfigToFile()

	// Send email notifications asynchronously
	go func() {
		// Email the user about the group change
		if api.Controller.Options.EmailServiceEnabled {
			api.Controller.EmailService.SendUserGroupChangeEmail(targetUser, toGroup, oldGroup)
		}

		// Email original group admin(s) if they exist
		if oldGroupId > 0 {
			api.sendGroupAdminNotification(oldGroupId, targetUser, toGroup)
		}
	}()

	transferReq.Status = "approved"
	transferReq.ApprovedBy = 0 // System approved via email link
	transferReq.ApprovedAt = time.Now().Unix()
	transferReq.ApprovalTokenUsed = true // Mark token as used
	api.Controller.TransferRequests.Update(transferReq, api.Controller.Database)

	api.sendTransferApprovalPage(w, true, "Transfer request approved successfully")
}

// sendTransferApprovalPage sends an HTML page indicating the result of transfer approval
func (api *Api) sendTransferApprovalPage(w http.ResponseWriter, success bool, message string) {
	branding := api.Controller.Options.Branding
	if branding == "" {
		branding = "ThinLine Radio"
	}

	statusColor := "#f44336"
	statusIcon := "❌"
	if success {
		statusColor = "#4CAF50"
		statusIcon = "✅"
	}

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <meta http-equiv="X-UA-Compatible" content="IE=edge">
    <meta name="robots" content="noindex, nofollow">
    <meta http-equiv="Content-Security-Policy" content="default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self' data:;">
    <title>Transfer Request - %s</title>
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }
        html, body {
            height: 100%%;
            width: 100%%;
            overflow: hidden;
        }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            display: flex;
            justify-content: center;
            align-items: center;
            min-height: 100vh;
            margin: 0;
            background-color: #f5f5f5;
        }
        .container {
            background: white;
            border-radius: 12px;
            padding: 40px;
            box-shadow: 0 4px 12px rgba(0,0,0,0.1);
            text-align: center;
            max-width: 500px;
            margin: 20px;
        }
        .icon {
            font-size: 64px;
            margin-bottom: 20px;
            line-height: 1;
        }
        h1 {
            color: %s;
            margin: 0 0 20px 0;
            font-size: 24px;
            font-weight: 600;
        }
        p {
            color: #555;
            font-size: 16px;
            margin: 10px 0;
            line-height: 1.5;
        }
    </style>
    <script>
        (function() {
            // Immediately unregister service workers to prevent Angular app from loading
            if ('serviceWorker' in navigator) {
                navigator.serviceWorker.getRegistrations().then(function(registrations) {
                    for(let i = 0; i < registrations.length; i++) {
                        registrations[i].unregister();
                    }
                });
            }
            // Prevent any further script loading
            window.stop = function() {};
        })();
    </script>
</head>
<body>
    <div class="container">
        <div class="icon">%s</div>
        <h1>%s</h1>
        <p>%s</p>
        <p style="margin-top: 20px; font-size: 14px; color: #999;">You can close this window.</p>
    </div>
</body>
</html>`, branding, statusColor, statusIcon, message, message)

	// Set headers to prevent Angular app from loading and ensure standalone page
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Service-Worker-Allowed", "/")
	// Write response immediately to prevent any middleware from interfering
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Error writing approval page response: %v", err)
	}
}

// Group Admin - Get Available Groups for Transfer
func (api *Api) GroupAdminAvailableGroupsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	_, group, err := api.getGroupAdminUser(r)
	if err != nil {
		api.exitWithError(w, http.StatusUnauthorized, err.Error())
		return
	}

	// Get all groups except the current group
	allGroups := api.Controller.UserGroups.GetAll()
	availableGroups := []map[string]interface{}{}
	for _, g := range allGroups {
		if g.Id != group.Id {
			availableGroups = append(availableGroups, map[string]interface{}{
				"id":   g.Id,
				"name": g.Name,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"groups": availableGroups,
	})
}

// Group Admin - Get Transfer Requests
func (api *Api) GroupAdminTransferRequestsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	_, group, err := api.getGroupAdminUser(r)
	if err != nil {
		api.exitWithError(w, http.StatusUnauthorized, err.Error())
		return
	}

	requests := api.Controller.TransferRequests.GetByGroup(group.Id)
	requestList := []map[string]interface{}{}
	for _, req := range requests {
		requestUser := api.Controller.Users.GetUserById(req.UserId)
		fromGroup := api.Controller.UserGroups.Get(req.FromGroupId)
		toGroup := api.Controller.UserGroups.Get(req.ToGroupId)

		requestList = append(requestList, map[string]interface{}{
			"id":     req.Id,
			"userId": req.UserId,
			"userEmail": func() string {
				if requestUser != nil {
					return requestUser.Email
				}
				return ""
			}(),
			"fromGroupId": req.FromGroupId,
			"fromGroupName": func() string {
				if fromGroup != nil {
					return fromGroup.Name
				}
				return ""
			}(),
			"toGroupId": req.ToGroupId,
			"toGroupName": func() string {
				if toGroup != nil {
					return toGroup.Name
				}
				return ""
			}(),
			"status":      req.Status,
			"requestedAt": req.RequestedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"requests": requestList,
	})
}

// User device token registration handler
func (api *Api) RelayServerAuthKeyHandler(w http.ResponseWriter, r *http.Request) {
	// Return the authorization key for relay server API requests
	// This is computed from a hash, not stored in plain text
	key := getRelayServerAuthKey()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"authKey": key,
	})
}

func (api *Api) UserDeviceTokenHandler(w http.ResponseWriter, r *http.Request) {
	client := api.getClient(r)
	if client == nil || client.User == nil {
		api.exitWithError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	switch r.Method {
	case http.MethodPost:
		// Register or update device token
		var request struct {
			Token    string `json:"token"`    // OneSignal player ID
			Platform string `json:"platform"` // "ios" or "android"
			Sound    string `json:"sound"`    // Notification sound preference
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
			return
		}

		if request.Token == "" {
			api.exitWithError(w, http.StatusBadRequest, "Token is required")
			return
		}

		if request.Platform != "ios" && request.Platform != "android" {
			request.Platform = "android" // Default
		}

		if request.Sound == "" {
			request.Sound = "startup.wav" // Default
		}

		// Check if device token already exists for this user
		existingToken := api.Controller.DeviceTokens.FindByUserAndToken(client.User.Id, request.Token)
		if existingToken != nil {
			// Update existing token
			existingToken.Platform = request.Platform
			existingToken.Sound = request.Sound
			if err := api.Controller.DeviceTokens.Update(existingToken, api.Controller.Database); err != nil {
				api.exitWithError(w, http.StatusInternalServerError, "Failed to update device token")
				return
			}
		} else {
			// Create new device token
			deviceToken := &DeviceToken{
				UserId:    client.User.Id,
				Token:     request.Token,
				Platform:  request.Platform,
				Sound:     request.Sound,
				CreatedAt: time.Now().Unix(),
				LastUsed:  time.Now().Unix(),
			}

			if err := api.Controller.DeviceTokens.Add(deviceToken, api.Controller.Database); err != nil {
				api.exitWithError(w, http.StatusInternalServerError, "Failed to register device token")
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Device token registered successfully",
		})

	case http.MethodDelete:
		// Unregister device token
		var request struct {
			Token string `json:"token"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
			return
		}

		deviceToken := api.Controller.DeviceTokens.FindByUserAndToken(client.User.Id, request.Token)
		if deviceToken == nil {
			api.exitWithError(w, http.StatusNotFound, "Device token not found")
			return
		}

		if err := api.Controller.DeviceTokens.Delete(deviceToken.Id, api.Controller.Database); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, "Failed to delete device token")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Device token unregistered successfully",
		})

	default:
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// UserTransferToPublicHandler allows users to transfer themselves to the public registration group
func (api *Api) UserTransferToPublicHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Authenticate user via PIN
	var request struct {
		Pin string `json:"pin"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if request.Pin == "" {
		api.exitWithError(w, http.StatusBadRequest, "PIN is required")
		return
	}

	// Find user by PIN
	user := api.Controller.Users.GetUserByPin(request.Pin)
	if user == nil {
		api.exitWithError(w, http.StatusUnauthorized, "Invalid PIN")
		return
	}

	// Find public registration group
	publicGroup := api.Controller.UserGroups.GetPublicRegistrationGroup()
	if publicGroup == nil {
		api.exitWithError(w, http.StatusNotFound, "Public registration group not found")
		return
	}

	// Check if user is already in the public registration group
	if user.UserGroupId == publicGroup.Id {
		api.exitWithError(w, http.StatusBadRequest, "User is already in the public registration group")
		return
	}

	// Get the old group for billing transition and notifications
	oldGroup := api.Controller.UserGroups.Get(user.UserGroupId)

	// Log the transfer details for debugging
	log.Printf("Transfer to public group: user=%s, oldGroupId=%d, newGroupId=%d, oldBillingEnabled=%v, newBillingEnabled=%v, oldBillingMode=%s, newBillingMode=%s",
		user.Email, user.UserGroupId, publicGroup.Id,
		oldGroup != nil && oldGroup.BillingEnabled, publicGroup.BillingEnabled,
		func() string {
			if oldGroup != nil {
				return oldGroup.BillingMode
			} else {
				return "none"
			}
		}(),
		publicGroup.BillingMode)

	// Handle billing transitions (cancel/create subscriptions as needed)
	if err := api.handleUserGroupBillingTransition(user, oldGroup, publicGroup); err != nil {
		log.Printf("ERROR: Failed to handle billing transition for user %s: %v", user.Email, err)
		// Continue with transfer anyway, but log the error
	} else {
		log.Printf("Successfully handled billing transition for user %s, customer ID: %s", user.Email, user.StripeCustomerId)
	}

	// Update user group
	oldGroupId := user.UserGroupId
	user.UserGroupId = publicGroup.Id
	// Remove group admin status if user was a group admin in the old group
	if user.IsGroupAdmin && oldGroupId > 0 {
		user.IsGroupAdmin = false
	}
	// Clear user's individual delay settings - they will use the group's delay settings
	api.clearUserDelayValues(user)
	api.Controller.Users.Update(user)
	api.Controller.Users.Write(api.Controller.Database)

	// Send email notifications asynchronously
	go func() {
		// Email the user about the group change
		if api.Controller.Options.EmailServiceEnabled {
			api.Controller.EmailService.SendUserGroupChangeEmail(user, publicGroup, oldGroup)
		}

		// Email original group admin(s) if they exist
		if oldGroupId > 0 {
			api.sendGroupAdminNotification(oldGroupId, user, publicGroup)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Successfully transferred to public registration group",
		"groupId": publicGroup.Id,
	})
}

// PublicRegistrationInfoHandler returns public registration group information including pricing
func (api *Api) PublicRegistrationInfoHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	publicGroup := api.Controller.UserGroups.GetPublicRegistrationGroup()
	if publicGroup == nil {
		api.exitWithError(w, http.StatusNotFound, "Public registration group not found")
		return
	}

	// Get pricing options
	pricingOptions := publicGroup.GetPricingOptions()

	response := map[string]interface{}{
		"name":           publicGroup.Name,
		"description":    publicGroup.Description,
		"billingEnabled": publicGroup.BillingEnabled,
		"pricingOptions": pricingOptions,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// PublicRegistrationChannelsHandler returns available systems/channels for the public registration group
func (api *Api) PublicRegistrationChannelsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	publicGroup := api.Controller.UserGroups.GetPublicRegistrationGroup()
	if publicGroup == nil {
		api.exitWithError(w, http.StatusNotFound, "Public registration group not found")
		return
	}

	// Get all systems
	allSystems := api.Controller.Systems.List
	availableSystems := []map[string]interface{}{}

	for _, system := range allSystems {
		// Check if this system is accessible to the public group
		if publicGroup.HasSystemAccess(uint64(system.SystemRef)) {
			// Get talkgroups for this system, filtered by group access
			talkgroups := []map[string]interface{}{}
			for _, tg := range system.Talkgroups.List {
				// Check if this talkgroup is accessible
				if publicGroup.HasTalkgroupAccess(uint64(system.SystemRef), tg.TalkgroupRef) {
					// Get tag for sorting
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
						"description": tg.Name,  // Use name as description
						"tag":         tagLabel, // Alpha tag
					})
				}
			}

			// Only add system if it has accessible talkgroups
			if len(talkgroups) > 0 {
				availableSystems = append(availableSystems, map[string]interface{}{
					"id":         system.SystemRef,
					"label":      system.Label,
					"talkgroups": talkgroups,
				})
			}
		}
	}

	// Sort systems by label, then sort talkgroups by tag then label
	for _, sys := range availableSystems {
		if talkgroups, ok := sys["talkgroups"].([]map[string]interface{}); ok {
			sort.Slice(talkgroups, func(i, j int) bool {
				tagI := talkgroups[i]["tag"].(string)
				tagJ := talkgroups[j]["tag"].(string)
				if tagI != tagJ {
					return tagI < tagJ
				}
				labelI := talkgroups[i]["label"].(string)
				labelJ := talkgroups[j]["label"].(string)
				return labelI < labelJ
			})
		}
	}

	// Sort systems by label
	sort.Slice(availableSystems, func(i, j int) bool {
		labelI := availableSystems[i]["label"].(string)
		labelJ := availableSystems[j]["label"].(string)
		return labelI < labelJ
	})

	response := map[string]interface{}{
		"systems": availableSystems,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// RegistrationSettingsHandler returns registration settings (public/invite-only mode)
func (api *Api) RegistrationSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	response := map[string]interface{}{
		"publicRegistrationEnabled": api.Controller.Options.PublicRegistrationEnabled,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// ValidateAccessCodeHandler validates a registration or invitation code before showing the form
func (api *Api) ValidateAccessCodeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.exitWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var request struct {
		Code string `json:"code"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if request.Code == "" {
		api.exitWithError(w, http.StatusBadRequest, "Code is required")
		return
	}

	// First try as invitation code
	var invitation struct {
		Id          int64
		Email       string
		UserGroupId uint64
		Status      string
		ExpiresAt   int64
		UsedAt      sql.NullInt64
	}

	err := api.Controller.Database.Sql.QueryRow(
		`SELECT "userInvitationId", "email", "userGroupId", "status", "expiresAt", "usedAt" 
		 FROM "userInvitations" WHERE "code" = $1`,
		request.Code,
	).Scan(&invitation.Id, &invitation.Email, &invitation.UserGroupId, &invitation.Status, &invitation.ExpiresAt, &invitation.UsedAt)

	if err == nil {
		// Check if invitation is valid
		if invitation.Status != "active" {
			api.exitWithError(w, http.StatusBadRequest, "Invitation has been revoked or is inactive")
			return
		}

		if invitation.UsedAt.Valid {
			api.exitWithError(w, http.StatusBadRequest, "Invitation code has already been used")
			return
		}

		if invitation.ExpiresAt > 0 && time.Now().Unix() > invitation.ExpiresAt {
			api.exitWithError(w, http.StatusBadRequest, "Invitation code has expired")
			return
		}

		group := api.Controller.UserGroups.Get(invitation.UserGroupId)
		if group != nil {
			response := map[string]interface{}{
				"valid": true,
				"type":  "invitation",
				"groupInfo": map[string]interface{}{
					"name":        group.Name,
					"description": group.Description,
				},
			}
			if invitation.Email != "" {
				response["email"] = invitation.Email
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
	}

	// Try as registration code
	regCode, err := api.Controller.RegistrationCodes.Validate(request.Code)
	if err == nil && regCode != nil {
		group := api.Controller.UserGroups.Get(regCode.UserGroupId)
		if group != nil {
			response := map[string]interface{}{
				"valid": true,
				"type":  "registration",
				"groupInfo": map[string]interface{}{
					"name":        group.Name,
					"description": group.Description,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
	}

	// Code not found or invalid
	api.exitWithError(w, http.StatusBadRequest, "Invalid or expired code")
}

// SystemAlertsHandler handles GET/POST for system alerts (system admins only)
func (api *Api) SystemAlertsHandler(w http.ResponseWriter, r *http.Request) {
	client := api.getClient(r)
	if client == nil || client.User == nil {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Get system alerts - all authenticated users can view
		limitStr := r.URL.Query().Get("limit")
		includeDismissed := r.URL.Query().Get("includeDismissed") == "true"

		limit := 50
		if limitStr != "" {
			if parsedLimit, err := strconv.Atoi(limitStr); err == nil {
				limit = parsedLimit
			}
		}

		alerts, err := api.Controller.GetSystemAlerts(limit, includeDismissed)
		if err != nil {
			api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get system alerts: %v", err))
			return
		}

		// Filter alerts based on user role
		// Regular users only see "manual" alerts (sent by system admins)
		// System admins see all alerts (including health monitoring)
		filteredAlerts := []*SystemAlert{}
		for _, alert := range alerts {
			if client.User.SystemAdmin {
				// System admins see all alerts
				filteredAlerts = append(filteredAlerts, alert)
			} else {
				// Regular users only see manual alerts
				if alert.AlertType == "manual" {
					filteredAlerts = append(filteredAlerts, alert)
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"alerts":        filteredAlerts,
			"isSystemAdmin": client.User.SystemAdmin,
		})

	case http.MethodPost:
		// Only system admins can create alerts
		if !client.User.SystemAdmin {
			api.exitWithError(w, http.StatusForbidden, "system admin access required")
			return
		}
		// Create a manual system alert
		var request struct {
			Title    string                 `json:"title"`
			Message  string                 `json:"message"`
			Severity string                 `json:"severity"` // "info", "warning", "error", "critical"
			Data     map[string]interface{} `json:"data"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			api.exitWithError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		if request.Title == "" || request.Message == "" {
			api.exitWithError(w, http.StatusBadRequest, "title and message are required")
			return
		}

		// Default severity to info if not specified or invalid
		if request.Severity == "" {
			request.Severity = "info"
		}
		validSeverities := map[string]bool{"info": true, "warning": true, "error": true, "critical": true}
		if !validSeverities[request.Severity] {
			request.Severity = "info"
		}

		// Create system alert data
		var data *SystemAlertData
		if request.Data != nil {
			data = &SystemAlertData{}
			// Try to parse the data map into SystemAlertData
			if callId, ok := request.Data["callId"].(float64); ok {
				data.CallId = uint64(callId)
			}
			if systemId, ok := request.Data["systemId"].(float64); ok {
				data.SystemId = uint64(systemId)
			}
			if talkgroupId, ok := request.Data["talkgroupId"].(float64); ok {
				data.TalkgroupId = uint64(talkgroupId)
			}
			if errorMsg, ok := request.Data["error"].(string); ok {
				data.Error = errorMsg
			}
			if count, ok := request.Data["count"].(float64); ok {
				data.Count = int(count)
			}
			if service, ok := request.Data["service"].(string); ok {
				data.Service = service
			}
		}

		if err := api.Controller.CreateSystemAlert("manual", request.Severity, request.Title, request.Message, data, client.User.Id); err != nil {
			api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create system alert: %v", err))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "system alert created successfully",
		})

	default:
		api.exitWithError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// SystemAlertDismissHandler handles PUT /api/system-alerts/:id/dismiss (system admins only)
func (api *Api) SystemAlertDismissHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		api.exitWithError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	client := api.getClient(r)
	if client == nil || client.User == nil {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Check if user is a system admin
	if !client.User.SystemAdmin {
		api.exitWithError(w, http.StatusForbidden, "system admin access required")
		return
	}

	// Get alert ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 4 {
		api.exitWithError(w, http.StatusBadRequest, "invalid request path")
		return
	}

	alertIdStr := pathParts[len(pathParts)-2] // Get ID before /dismiss
	alertId, err := strconv.ParseUint(alertIdStr, 10, 64)
	if err != nil {
		api.exitWithError(w, http.StatusBadRequest, "invalid alert ID")
		return
	}

	if err := api.Controller.DismissSystemAlert(alertId); err != nil {
		api.exitWithError(w, http.StatusInternalServerError, fmt.Sprintf("failed to dismiss alert: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "alert dismissed successfully",
	})
}
