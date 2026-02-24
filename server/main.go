// Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
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
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/crypto/bcrypt"
)

// loadDotEnv loads environment variables from the first existing file in candidates.
// Supports lines in the format KEY=VALUE, optional quotes, ignores comments and blank lines.
func loadDotEnv(candidates ...string) {
	for _, filePath := range candidates {
		f, err := os.Open(filePath)
		if err != nil {
			continue
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, "export ") {
				line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
			}
			idx := strings.Index(line, "=")
			if idx <= 0 {
				continue
			}
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			if len(val) >= 2 {
				if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
					val = val[1 : len(val)-1]
				}
			}
			_ = os.Setenv(key, val)
		}
		return // only load the first found file
	}
}

func main() {
	// Enable multi-core processing
	runtime.GOMAXPROCS(runtime.NumCPU())
	log.Printf("Starting ThinLine Radio with %d CPU cores", runtime.NumCPU())

	// Load environment variables from a .env file if present
	loadDotEnv(".env", "../.env")

	const defaultAddr = "0.0.0.0"

	var (
		addr     string
		port     string
		hostname string
		sslAddr  string
		sslPort  string
	)

	config := NewConfig()

	// Check if we should run interactive setup wizard
	if shouldRunInteractiveSetup(config) {
		if config.DbName == "" || config.DbUsername == "" {
			fmt.Println("\n⚠️  Database configuration is incomplete or missing.")
		} else {
			fmt.Println("\n⚠️  No configuration file found.")
		}
		fmt.Println("Running interactive setup wizard...")
		if err := runInteractiveSetup(config.ConfigFile); err != nil {
			log.Fatalf("Setup failed: %v\n", err)
		}
		fmt.Println("\n✓ Setup complete! Please restart the server.")
		os.Exit(0)
	}

	if config.newAdminPassword == "" {
		fmt.Printf("\nThinLine Radio v%s\n", Version)
		fmt.Printf("----------------------------------\n")
	}

	controller := NewController(config)

	// Handle opus_migration flag from INI file
	if config.OpusMigration {
		fmt.Printf("\nThinLine Radio v%s - Opus Migration (Background Mode)\n", Version)
		fmt.Printf("--------------------------------------------------\n\n")
		fmt.Println("⚠️  opus_migration = true detected in configuration")
		fmt.Println("Starting background migration while server runs...")
		fmt.Println("Migration will use reduced resources to avoid impacting server.")
		fmt.Println("")

		// Run migration in background goroutine
		go func() {
			// Use smaller batch (100 instead of 5000) and fewer workers (10 instead of 200)
			if err := controller.Database.MigrateToOpus(100, false, true); err != nil {
				log.Printf("❌ Background migration error: %v", err)
				log.Printf("Migration will continue running. Check logs for details.")
			} else {
				fmt.Println("")
				fmt.Println("✅ Background migration complete! Setting opus_migration = false in INI file...")

				// Automatically set opus_migration = false in the INI file
				if err := config.SetOpusMigration(false); err != nil {
					log.Printf("⚠️  Warning: Could not update INI file: %v", err)
					log.Printf("Please manually set opus_migration = false in %s", config.ConfigFile)
				} else {
					fmt.Println("✅ Configuration updated successfully")
					fmt.Println("Migration is complete. Restart server to apply final changes.")
				}
			}
		}()

		// Give migration a moment to start
		time.Sleep(1 * time.Second)
		fmt.Println("🚀 Server starting while migration runs in background...")
		fmt.Println("")

		// Continue to start the server normally
		config.OpusMigration = false // Ensure we don't try to migrate again on next iteration
	}

	// Handle migrate-to-opus command line flag
	if config.migrateToOpus {
		fmt.Printf("\nThinLine Radio v%s - Opus Migration\n", Version)
		fmt.Printf("----------------------------------\n\n")

		if err := controller.Database.MigrateToOpus(config.migrateOpusBatch, config.migrateOpusDryRun, false); err != nil {
			log.Fatalf("Migration failed: %v", err)
		}

		// Command-line migration still exits (user explicitly ran migration tool)
		os.Exit(0)
	}

	if config.newAdminPassword != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(config.newAdminPassword), bcrypt.DefaultCost)
		if err != nil {
			log.Printf("ERROR: Failed to hash admin password: %v", err)
			os.Exit(1)
		}

		if err := controller.Options.Read(controller.Database); err != nil {
			log.Printf("ERROR: Failed to read options from database: %v", err)
			os.Exit(1)
		}

		controller.Options.adminPassword = string(hash)
		controller.Options.adminPasswordNeedChange = config.newAdminPassword == defaults.adminPassword

		if err := controller.Options.Write(controller.Database); err != nil {
			log.Printf("ERROR: Failed to write options to database: %v", err)
			os.Exit(1)
		}

		controller.Logs.LogEvent(LogLevelInfo, "admin password changed.")

		os.Exit(0)
	}

	if err := controller.Start(); err != nil {
		log.Printf("FATAL: Failed to start controller: %v", err)
		log.Printf("Server cannot continue without a running controller. Exiting.")
		os.Exit(1)
	}

	// Create a panic recovery middleware
	recoveryMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if panicValue := recover(); panicValue != nil {
					log.Printf("PANIC RECOVERED in %s %s: %v", r.Method, r.URL.Path, panicValue)

					// Try to send a JSON error response
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]string{
						"error":   "Internal server error - panic recovered",
						"details": fmt.Sprintf("%v", panicValue),
					})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}

	// Apply general rate limiting to all routes
	rateLimitWrapper := func(handler http.Handler) http.Handler {
		return RateLimitMiddleware(controller.RateLimiter)(handler)
	}

	// Apply security headers to all routes
	securityHeadersWrapper := func(handler http.Handler) http.Handler {
		return SecurityHeadersMiddleware(handler)
	}

	// Helper to wrap handlers with recovery, rate limiting, and security headers
	wrapHandler := func(handler http.Handler) http.Handler {
		return securityHeadersWrapper(rateLimitWrapper(recoveryMiddleware(handler)))
	}

	// corsMiddleware adds CORS headers so the Central Management frontend (a different
	// origin) can call user-facing API endpoints.  Authentication is still enforced by
	// each handler via PIN, so opening these endpoints to any origin is safe.
	corsMiddleware := func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			handler.ServeHTTP(w, r)
		})
	}

	if h, err := os.Hostname(); err == nil {
		hostname = h
	} else {
		hostname = defaultAddr
	}

	if s := strings.Split(config.Listen, ":"); len(s) > 1 {
		addr = s[0]
		port = s[1]
	} else {
		addr = s[0]
		port = "3000"
	}
	if len(addr) == 0 {
		addr = defaultAddr
	}

	if s := strings.Split(config.SslListen, ":"); len(s) > 1 {
		sslAddr = s[0]
		sslPort = s[1]
	} else {
		sslAddr = s[0]
		sslPort = "3000"
	}
	if len(sslAddr) == 0 {
		sslAddr = defaultAddr
	}

	http.HandleFunc("/api/admin/alerts", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.AlertsHandler)).ServeHTTP)

	http.HandleFunc("/api/admin/systemhealth", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.SystemHealthHandler)).ServeHTTP)

	http.HandleFunc("/api/admin/system-no-audio-settings", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.SystemNoAudioSettingsHandler)).ServeHTTP)

	http.HandleFunc("/api/admin/transcription-failures", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.TranscriptionFailuresHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/transcription-failure-threshold", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.TranscriptionFailureThresholdHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/tone-detection-issue-threshold", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.ToneDetectionIssueThresholdHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/alert-retention-days", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.AlertRetentionDaysHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/no-audio-threshold-minutes", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.NoAudioThresholdMinutesHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/no-audio-multiplier", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.NoAudioMultiplierHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/system-health-alerts-enabled", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.SystemHealthAlertsEnabledHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/system-health-alert-settings", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.SystemHealthAlertSettingsHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/call-audio/", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.CallAudioHandler)).ServeHTTP)

	http.HandleFunc("/api/admin/tone-import", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.ToneImportHandler)).ServeHTTP)

	http.HandleFunc("/api/admin/config", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.ConfigHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/email-logo", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.EmailLogoUploadHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/email-logo/delete", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.EmailLogoDeleteHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/favicon", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.FaviconUploadHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/favicon/delete", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.FaviconDeleteHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/email-test", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.EmailTestHandler)).ServeHTTP)

	http.HandleFunc("/api/admin/stripe-sync", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.StripeSyncHandler)).ServeHTTP)

	// Serve email logo file - register before root handler to ensure it's handled
	http.HandleFunc("/email-logo", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if controller.Options.EmailLogoFilename != "" {
			logoPath := filepath.Join(controller.Config.BaseDir, controller.Options.EmailLogoFilename)
			if b, err := os.ReadFile(logoPath); err == nil {
				w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(controller.Options.EmailLogoFilename)))
				w.Header().Set("Cache-Control", "public, max-age=31536000")
				w.Write(b)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	// Serve favicon file - register before root handler to ensure it's handled
	http.HandleFunc("/favicon", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if controller.Options.FaviconFilename != "" {
			faviconPath := filepath.Join(controller.Config.BaseDir, controller.Options.FaviconFilename)
			if b, err := os.ReadFile(faviconPath); err == nil {
				w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(controller.Options.FaviconFilename)))
				w.Header().Set("Cache-Control", "public, max-age=31536000")
				w.Write(b)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	// Admin login with rate limiting and login attempt tracking
	adminLoginHandler := LoginAttemptMiddleware(controller.LoginAttemptTracker)(
		recoveryMiddleware(controller.Admin.requireLocalhost(controller.Admin.LoginHandler)),
	)
	http.HandleFunc("/api/admin/login", securityHeadersWrapper(rateLimitWrapper(adminLoginHandler)).ServeHTTP)

	http.HandleFunc("/api/admin/logout", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.LogoutHandler)).ServeHTTP)

	http.HandleFunc("/api/admin/logs", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.LogsHandler)).ServeHTTP)

	http.HandleFunc("/api/admin/calls", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.CallsHandler)).ServeHTTP)

	http.HandleFunc("/api/admin/purge", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.PurgeHandler)).ServeHTTP)

	http.HandleFunc("/api/admin/password", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.PasswordHandler)).ServeHTTP)

	http.HandleFunc("/api/admin/users", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.UsersListHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/users/create", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.UserCreateHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/users/", wrapHandler(controller.Admin.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
		// Check if it's a device-tokens endpoint: /api/admin/users/{userId}/device-tokens/{tokenId}
		pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(pathParts) >= 6 && pathParts[4] == "device-tokens" && r.Method == http.MethodDelete {
			controller.Admin.DeviceTokenDeleteHandler(w, r)
			// Check if it's a test-push endpoint
		} else if strings.HasSuffix(r.URL.Path, "/test-push") && r.Method == http.MethodPost {
			controller.Admin.UserTestPushHandler(w, r)
			// Check if it's a reset-password endpoint
		} else if strings.HasSuffix(r.URL.Path, "/reset-password") && r.Method == http.MethodPost {
			controller.Admin.UserResetPasswordHandler(w, r)
		} else if r.Method == http.MethodDelete {
			controller.Admin.UserDeleteHandler(w, r)
		} else if r.Method == http.MethodPut {
			controller.Admin.UserUpdateHandler(w, r)
		} else {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})).ServeHTTP)

	http.HandleFunc("/api/admin/radioreference/test", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.RadioReferenceTestHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/radioreference/search", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.RadioReferenceSearchHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/radioreference/import", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.RadioReferenceImportHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/radioreference/count", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.RadioReferenceTalkgroupCountHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/radioreference/all-talkgroups", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.RadioReferenceAllTalkgroupsHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/radioreference/test-streaming", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.RadioReferenceTestStreamingHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/radioreference/streaming-talkgroups", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.RadioReferenceStreamingTalkgroupsHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/radioreference/progress-talkgroups", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.RadioReferenceProgressTalkgroupsHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/radioreference/countries", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.RadioReferenceCountriesHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/radioreference/states", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.RadioReferenceStatesHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/radioreference/counties", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.RadioReferenceCountiesHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/radioreference/systems", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.RadioReferenceSystemsHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/radioreference/talkgroups", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.RadioReferenceTalkgroupsHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/radioreference/talkgroup-categories", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.RadioReferenceTalkgroupCategoriesHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/radioreference/talkgroups-by-category", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.RadioReferenceTalkgroupsByCategoryHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/radioreference/sites", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.RadioReferenceSitesHandler)).ServeHTTP)

	http.HandleFunc("/api/admin/config/reload", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.ConfigReloadHandler)).ServeHTTP)

	// Hallucination detection endpoints
	http.HandleFunc("/api/admin/hallucinations/suggestions", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.HallucinationSuggestionsHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/hallucinations/approve", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.HallucinationApproveHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/hallucinations/reject", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.HallucinationRejectHandler)).ServeHTTP)

	// User registration and authentication routes
	http.HandleFunc("/api/user/register", wrapHandler(http.HandlerFunc(controller.Api.UserRegisterHandler)).ServeHTTP)
	http.HandleFunc("/api/user/validate-invitation", wrapHandler(http.HandlerFunc(controller.Api.ValidateInvitationHandler)).ServeHTTP)
	// User login with rate limiting and login attempt tracking
	userLoginHandler := LoginAttemptMiddleware(controller.LoginAttemptTracker)(
		recoveryMiddleware(http.HandlerFunc(controller.Api.UserLoginHandler)),
	)
	http.HandleFunc("/api/user/login", securityHeadersWrapper(rateLimitWrapper(userLoginHandler)).ServeHTTP)
	http.HandleFunc("/api/public-registration-info", wrapHandler(http.HandlerFunc(controller.Api.PublicRegistrationInfoHandler)).ServeHTTP)
	http.HandleFunc("/api/public-registration-channels", wrapHandler(http.HandlerFunc(controller.Api.PublicRegistrationChannelsHandler)).ServeHTTP)
	http.HandleFunc("/api/registration-settings", wrapHandler(http.HandlerFunc(controller.Api.RegistrationSettingsHandler)).ServeHTTP)
	http.HandleFunc("/api/user/validate-access-code", wrapHandler(http.HandlerFunc(controller.Api.ValidateAccessCodeHandler)).ServeHTTP)
	http.HandleFunc("/api/user/verify", wrapHandler(http.HandlerFunc(controller.Api.UserVerifyHandler)).ServeHTTP)
	http.HandleFunc("/api/user/resend-verification", wrapHandler(http.HandlerFunc(controller.Api.UserResendVerificationHandler)).ServeHTTP)
	http.HandleFunc("/api/user/transfer-to-public", wrapHandler(http.HandlerFunc(controller.Api.UserTransferToPublicHandler)).ServeHTTP)
	http.HandleFunc("/api/user/forgot-password", wrapHandler(http.HandlerFunc(controller.Api.RequestPasswordResetHandler)).ServeHTTP)
	http.HandleFunc("/api/user/reset-password", wrapHandler(http.HandlerFunc(controller.Api.ResetPasswordHandler)).ServeHTTP)
	http.HandleFunc("/api/user/device-token", wrapHandler(http.HandlerFunc(controller.Api.UserDeviceTokenHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/relay-server-auth-key", wrapHandler(http.HandlerFunc(controller.Api.RelayServerAuthKeyHandler)).ServeHTTP)

	// Group admin routes
	// Group admin login with rate limiting and login attempt tracking
	groupAdminLoginHandler := LoginAttemptMiddleware(controller.LoginAttemptTracker)(
		recoveryMiddleware(http.HandlerFunc(controller.Api.GroupAdminLoginHandler)),
	)
	http.HandleFunc("/api/group-admin/login", securityHeadersWrapper(rateLimitWrapper(groupAdminLoginHandler)).ServeHTTP)
	http.HandleFunc("/api/group-admin/users", wrapHandler(http.HandlerFunc(controller.Api.GroupAdminUsersHandler)).ServeHTTP)
	http.HandleFunc("/api/group-admin/add-user", wrapHandler(http.HandlerFunc(controller.Api.GroupAdminAddUserHandler)).ServeHTTP)
	http.HandleFunc("/api/group-admin/add-existing-user", wrapHandler(http.HandlerFunc(controller.Api.GroupAdminAddExistingUserHandler)).ServeHTTP)
	http.HandleFunc("/api/group-admin/remove-user", wrapHandler(http.HandlerFunc(controller.Api.GroupAdminRemoveUserHandler)).ServeHTTP)
	http.HandleFunc("/api/group-admin/toggle-admin", wrapHandler(http.HandlerFunc(controller.Api.GroupAdminToggleAdminHandler)).ServeHTTP)
	http.HandleFunc("/api/group-admin/generate-code", wrapHandler(http.HandlerFunc(controller.Api.GroupAdminGenerateCodeHandler)).ServeHTTP)
	http.HandleFunc("/api/group-admin/invite-user", wrapHandler(http.HandlerFunc(controller.Api.GroupAdminInviteUserHandler)).ServeHTTP)
	// Handle DELETE requests to /api/group-admin/codes/{codeId} - must come before /api/group-admin/codes
	http.HandleFunc("/api/group-admin/codes/", wrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			controller.Api.GroupAdminDeleteCodeHandler(w, r)
		} else {
			http.NotFound(w, r)
		}
	})).ServeHTTP)
	http.HandleFunc("/api/group-admin/codes", wrapHandler(http.HandlerFunc(controller.Api.GroupAdminCodesHandler)).ServeHTTP)
	http.HandleFunc("/api/group-admin/available-groups", wrapHandler(http.HandlerFunc(controller.Api.GroupAdminAvailableGroupsHandler)).ServeHTTP)
	http.HandleFunc("/api/group-admin/request-transfer", wrapHandler(http.HandlerFunc(controller.Api.GroupAdminRequestTransferHandler)).ServeHTTP)
	http.HandleFunc("/api/group-admin/approve-transfer", wrapHandler(http.HandlerFunc(controller.Api.GroupAdminApproveTransferHandler)).ServeHTTP)
	// Register approve-transfer-link at root level first to avoid Angular service worker
	http.HandleFunc("/approve-transfer", wrapHandler(http.HandlerFunc(controller.Api.GroupAdminApproveTransferLinkHandler)).ServeHTTP)
	http.HandleFunc("/api/group-admin/approve-transfer-link", wrapHandler(http.HandlerFunc(controller.Api.GroupAdminApproveTransferLinkHandler)).ServeHTTP)
	http.HandleFunc("/api/group-admin/transfer-requests", wrapHandler(http.HandlerFunc(controller.Api.GroupAdminTransferRequestsHandler)).ServeHTTP)

	// System admin group management routes
	http.HandleFunc("/api/admin/groups", wrapHandler(controller.Admin.requireLocalhost(controller.Api.AdminGroupsHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/groups/create", wrapHandler(controller.Admin.requireLocalhost(controller.Api.AdminCreateGroupHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/groups/update", wrapHandler(controller.Admin.requireLocalhost(controller.Api.AdminUpdateGroupHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/groups/assign-admin", wrapHandler(controller.Admin.requireLocalhost(controller.Api.AdminAssignGroupAdminHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/groups/remove-admin", wrapHandler(controller.Admin.requireLocalhost(controller.Api.AdminRemoveGroupAdminHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/groups/admins", wrapHandler(controller.Admin.requireLocalhost(controller.Api.AdminGroupAdminsHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/groups/delete/", wrapHandler(controller.Admin.requireLocalhost(controller.Api.AdminDeleteGroupHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/users/transfer", wrapHandler(http.HandlerFunc(controller.Api.AdminTransferUserHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/invitations", wrapHandler(http.HandlerFunc(controller.Api.AdminInviteUserHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/groups/", wrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasSuffix(path, "/codes/generate") {
			controller.Api.AdminGroupGenerateCodeHandler(w, r)
		} else if strings.Contains(path, "/codes/") && !strings.HasSuffix(path, "/codes/generate") && r.Method == http.MethodDelete {
			controller.Api.AdminGroupDeleteCodeHandler(w, r)
		} else if strings.HasSuffix(path, "/codes") && r.Method == http.MethodGet {
			controller.Api.AdminGroupCodesHandler(w, r)
		} else {
			http.NotFound(w, r)
		}
	})).ServeHTTP)

	// Alert routes
	http.HandleFunc("/api/alerts", wrapHandler(corsMiddleware(http.HandlerFunc(controller.Api.AlertsHandler))).ServeHTTP)
	http.HandleFunc("/api/alerts/preferences", wrapHandler(corsMiddleware(http.HandlerFunc(controller.Api.AlertPreferencesHandler))).ServeHTTP)
	http.HandleFunc("/api/transcripts", wrapHandler(corsMiddleware(http.HandlerFunc(controller.Api.TranscriptsHandler))).ServeHTTP)
	http.HandleFunc("/api/keyword-lists", wrapHandler(http.HandlerFunc(controller.Api.KeywordListsHandler)).ServeHTTP)

	// System alert routes (system admins only)
	http.HandleFunc("/api/system-alerts", wrapHandler(http.HandlerFunc(controller.Api.SystemAlertsHandler)).ServeHTTP)
	http.HandleFunc("/api/system-alerts/", wrapHandler(http.HandlerFunc(controller.Api.SystemAlertDismissHandler)).ServeHTTP)
	http.HandleFunc("/api/keyword-lists/", wrapHandler(http.HandlerFunc(controller.Api.KeywordListHandler)).ServeHTTP)

	// User settings routes — wrapped with CORS so Central Management can call across origins
	http.HandleFunc("/api/settings", wrapHandler(corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			controller.Api.SettingsGetHandler(w, r)
		} else if r.Method == http.MethodPost {
			controller.Api.SettingsSaveHandler(w, r)
		} else {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))).ServeHTTP)

	// Stripe webhook route - keep recoveryMiddleware only (webhooks need special handling)
	http.HandleFunc("/api/stripe/webhook", securityHeadersWrapper(recoveryMiddleware(http.HandlerFunc(controller.Api.StripeWebhookHandler))).ServeHTTP)

	// Central Management webhook routes (for receiving user grant/revoke from central system)
	http.HandleFunc("/api/webhook/central-user-grant", securityHeadersWrapper(recoveryMiddleware(http.HandlerFunc(controller.Api.CentralWebhookUserGrantHandler))).ServeHTTP)
	http.HandleFunc("/api/webhook/central-user-revoke", securityHeadersWrapper(recoveryMiddleware(http.HandlerFunc(controller.Api.CentralWebhookUserRevokeHandler))).ServeHTTP)
	http.HandleFunc("/api/webhook/central-test", securityHeadersWrapper(recoveryMiddleware(http.HandlerFunc(controller.Api.CentralWebhookTestConnectionHandler))).ServeHTTP)
	http.HandleFunc("/api/webhook/central-users", securityHeadersWrapper(recoveryMiddleware(http.HandlerFunc(controller.Api.CentralWebhookUsersListHandler))).ServeHTTP)
	http.HandleFunc("/api/webhook/central-users-batch-update", securityHeadersWrapper(recoveryMiddleware(http.HandlerFunc(controller.Api.CentralWebhookUsersBatchUpdateHandler))).ServeHTTP)
	http.HandleFunc("/api/webhook/central-systems-talkgroups-groups", securityHeadersWrapper(recoveryMiddleware(http.HandlerFunc(controller.Api.CentralWebhookSystemsTalkgroupsGroupsHandler))).ServeHTTP)

	// Admin endpoint to test connection TO central management system
	http.HandleFunc("/api/admin/test-central-connection", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.TestCentralConnectionHandler)).ServeHTTP)

	// Auto-update endpoints
	http.HandleFunc("/api/admin/update/check", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.UpdateCheckHandler)).ServeHTTP)
	http.HandleFunc("/api/admin/update/apply", wrapHandler(controller.Admin.requireLocalhost(controller.Admin.UpdateApplyHandler)).ServeHTTP)

	// Stripe checkout session route
	http.HandleFunc("/api/stripe/create-checkout-session", wrapHandler(http.HandlerFunc(controller.Api.CreateCheckoutSessionHandler)).ServeHTTP)

	// Account management routes
	http.HandleFunc("/api/account", wrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			controller.Api.AccountGetHandler(w, r)
		} else {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})).ServeHTTP)
	http.HandleFunc("/api/account/email/request-verification", wrapHandler(http.HandlerFunc(controller.Api.AccountRequestEmailChangeVerificationHandler)).ServeHTTP)
	http.HandleFunc("/api/account/email/verify-code", wrapHandler(http.HandlerFunc(controller.Api.AccountVerifyEmailChangeCodeHandler)).ServeHTTP)
	http.HandleFunc("/api/account/email", wrapHandler(http.HandlerFunc(controller.Api.AccountUpdateEmailHandler)).ServeHTTP)
	http.HandleFunc("/api/account/email/verify-new", wrapHandler(http.HandlerFunc(controller.Api.AccountVerifyNewEmailHandler)).ServeHTTP)
	http.HandleFunc("/api/account/password/request-verification", wrapHandler(http.HandlerFunc(controller.Api.AccountRequestPasswordChangeVerificationHandler)).ServeHTTP)
	http.HandleFunc("/api/account/password/verify-code", wrapHandler(http.HandlerFunc(controller.Api.AccountVerifyPasswordChangeCodeHandler)).ServeHTTP)
	http.HandleFunc("/api/account/password", wrapHandler(http.HandlerFunc(controller.Api.AccountUpdatePasswordHandler)).ServeHTTP)
	http.HandleFunc("/api/billing/portal", wrapHandler(http.HandlerFunc(controller.Api.BillingPortalSessionHandler)).ServeHTTP)

	// Log that routes have been registered
	log.Printf("All HTTP routes registered successfully")

	// Call upload endpoints - exclude from security headers and rate limiting (machine-to-machine APIs)
	// These endpoints handle their own validation and need to accept frequent uploads
	// Match v6 registration pattern exactly - pass handler directly without wrapping
	http.HandleFunc("/api/call-upload", controller.Api.CallUploadHandler)

	http.HandleFunc("/api/trunk-recorder-call-upload", controller.Api.TrunkRecorderCallUploadHandler)

	// Performance monitoring endpoint
	http.HandleFunc("/api/status/performance", wrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		controller.workerStats.Lock()
		activeWorkers := controller.workerStats.activeWorkers
		totalCalls := controller.workerStats.totalCalls
		avgProcessTime := controller.workerStats.avgProcessTime
		controller.workerStats.Unlock()

		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)

		response := map[string]interface{}{
			"cpu_cores":        runtime.NumCPU(),
			"active_workers":   activeWorkers,
			"total_calls":      totalCalls,
			"avg_process_time": avgProcessTime.String(),
			"goroutines":       runtime.NumGoroutine(),
			"memory_stats": map[string]interface{}{
				"alloc_mb":       memStats.Alloc / 1024 / 1024,
				"total_alloc_mb": memStats.TotalAlloc / 1024 / 1024,
				"sys_mb":         memStats.Sys / 1024 / 1024,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})).ServeHTTP)

	// Login blocked countdown page
	http.HandleFunc("/login-blocked", wrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondsParam := r.URL.Query().Get("seconds")
		seconds := 900 // Default to 15 minutes if not provided
		if s, err := strconv.Atoi(secondsParam); err == nil && s > 0 {
			seconds = s
		}

		html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>Login Blocked - Too Many Failed Attempts</title>
	<style>
		* {
			margin: 0;
			padding: 0;
			box-sizing: border-box;
		}
		body {
			font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, sans-serif;
			background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%);
			min-height: 100vh;
			display: flex;
			align-items: center;
			justify-content: center;
			padding: 20px;
		}
		.container {
			background: white;
			border-radius: 16px;
			box-shadow: 0 20px 60px rgba(0, 0, 0, 0.3);
			padding: 40px;
			max-width: 500px;
			width: 100%%;
			text-align: center;
		}
		.icon {
			font-size: 64px;
			margin-bottom: 20px;
		}
		h1 {
			color: #333;
			margin-bottom: 16px;
			font-size: 28px;
		}
		p {
			color: #666;
			margin-bottom: 32px;
			line-height: 1.6;
			font-size: 16px;
		}
		.countdown {
			font-size: 48px;
			font-weight: bold;
			color: #667eea;
			margin: 20px 0;
			font-family: 'Courier New', monospace;
		}
		.countdown-label {
			color: #999;
			font-size: 14px;
			text-transform: uppercase;
			letter-spacing: 1px;
			margin-top: 8px;
		}
		.message {
			background: #fff3cd;
			border: 1px solid #ffc107;
			border-radius: 8px;
			padding: 16px;
			margin-top: 24px;
			color: #856404;
			font-size: 14px;
		}
		.retry-button {
			margin-top: 32px;
			display: inline-block;
			padding: 12px 32px;
			background: #667eea;
			color: white;
			text-decoration: none;
			border-radius: 8px;
			font-weight: 500;
			transition: background 0.3s;
			pointer-events: none;
			opacity: 0.5;
		}
		.retry-button.active {
			pointer-events: auto;
			opacity: 1;
			cursor: pointer;
		}
		.retry-button.active:hover {
			background: #5568d3;
		}
	</style>
</head>
<body>
	<div class="container">
		<div class="icon">🔒</div>
		<h1>Login Temporarily Blocked</h1>
		<p>Too many failed login attempts have been detected from your IP address. For security reasons, login access has been temporarily restricted.</p>
		
		<div class="countdown" id="countdown">--:--</div>
		<div class="countdown-label">Time Remaining</div>
		
		<div class="message">
			<strong>What happened?</strong><br>
			After 6 failed login attempts, your IP address has been blocked for 15 minutes to prevent unauthorized access attempts.
		</div>
		
		<a href="/admin" class="retry-button" id="retryButton">Try Again</a>
	</div>

	<script>
		let remainingSeconds = %d;
		const countdownEl = document.getElementById('countdown');
		const retryButton = document.getElementById('retryButton');
		
		function formatTime(seconds) {
			const mins = Math.floor(seconds / 60);
			const secs = seconds %% 60;
			return String(mins).padStart(2, '0') + ':' + String(secs).padStart(2, '0');
		}
		
		function updateCountdown() {
			if (remainingSeconds <= 0) {
				countdownEl.textContent = '00:00';
				retryButton.classList.add('active');
				retryButton.textContent = 'Try Again Now';
				return;
			}
			
			countdownEl.textContent = formatTime(remainingSeconds);
			remainingSeconds--;
			
			setTimeout(updateCountdown, 1000);
		}
		
		updateCountdown();
	</script>
</body>
</html>`, seconds)

		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	})).ServeHTTP)

	http.HandleFunc("/", wrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Redirect /admin to root if accessed from non-localhost and AdminLocalhostOnly is enabled
		requestPath := r.URL.Path
		if (requestPath == "/admin" || strings.HasPrefix(requestPath, "/admin/")) && controller.Options.AdminLocalhostOnly {
			clientIP := GetClientIP(r)
			isLocalhost := IsLocalhostIP(clientIP)

			if !isLocalhost {
				log.Printf("Redirecting %s to / for non-localhost IP: %s", requestPath, clientIP)
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
		}

		// Handle approval link requests that come through root path (URL normalization or old email links)
		if r.URL.Path == "/" {
			requestId := r.URL.Query().Get("requestId")
			token := r.URL.Query().Get("token")
			if requestId != "" && token != "" {
				controller.Api.GroupAdminApproveTransferLinkHandler(w, r)
				return
			}
		}

		// Skip API routes - they should be handled by specific handlers
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		// When this server is centrally managed, redirect normal web UI traffic to Central Management.
		// Keep admin, API, websocket, and static asset traffic local so the admin panel continues to work.
		isStaticAsset := func(p string) bool {
			ext := strings.ToLower(filepath.Ext(p))
			switch ext {
			case ".js", ".css", ".map", ".json", ".webmanifest",
				".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp",
				".woff", ".woff2", ".ttf", ".eot",
				".mp3", ".ogg", ".wav":
				return true
			}
			return false
		}
		if controller.Options.CentralManagementEnabled &&
			strings.TrimSpace(controller.Options.CentralManagementURL) != "" &&
			!strings.HasPrefix(requestPath, "/admin") &&
			!isStaticAsset(requestPath) &&
			!strings.EqualFold(r.Header.Get("upgrade"), "websocket") &&
			(r.Method == http.MethodGet || r.Method == http.MethodHead) {
			baseCentralURL := strings.TrimRight(strings.TrimSpace(controller.Options.CentralManagementURL), "/")
			if baseCentralURL != "" {
				target := baseCentralURL + requestPath
				if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
					target += "?" + rawQuery
				}
				http.Redirect(w, r, target, http.StatusFound)
				return
			}
		}

		url := r.URL.Path[1:]

		if strings.EqualFold(r.Header.Get("upgrade"), "websocket") {
			upgrader := websocket.Upgrader{
				CheckOrigin: func(r *http.Request) bool {
					return true
				},
				ReadBufferSize:  1024,
				WriteBufferSize: 1024,
			}

			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				log.Println(err)
			}

			client := &Client{}
			if err = client.Init(controller, r, conn); err != nil {
				log.Println(err)
			}

		} else {
			if url == "" {
				// Serve index.html with injected initial config
				if b, err := webapp.ReadFile("webapp/index.html"); err == nil {
					html := string(b)

					// Fix base href to be absolute with full domain
					// Check reverse proxy headers for correct scheme/host
					scheme, host := getSchemeAndHost(r)
					baseUrl := fmt.Sprintf("%s://%s/", scheme, host)
					html = strings.Replace(html, `<base href="./">`, fmt.Sprintf(`<base href="%s">`, baseUrl), 1)

					// Get initial config data
					branding := controller.Options.Branding
					if branding == "" {
						branding = "Thinline Radio"
					}
					email := controller.Options.Email

					// Inject config into HTML
					configScript := fmt.Sprintf(`
<script>
window.initialConfig = {
	"branding": %q,
	"email": %q,
	"options": {
		"userRegistrationEnabled": %t,
		"stripePaywallEnabled": %t,
		"stripePublishableKey": %q,
		"stripePriceId": %q,
		"baseUrl": %q,
		"emailLogoFilename": %q,
		"emailLogoBorderRadius": %q,
		"turnstileEnabled": %t,
		"turnstileSiteKey": %q
	}
};
</script>`, branding, email, controller.Options.UserRegistrationEnabled, controller.Options.StripePaywallEnabled, controller.Options.StripePublishableKey, controller.Options.StripePriceId, controller.Options.BaseUrl, controller.Options.EmailLogoFilename, controller.Options.EmailLogoBorderRadius, controller.Options.TurnstileEnabled, controller.Options.TurnstileSiteKey)

					// Try multiple insertion points for the config script
					injected := false
					// Try to insert config script before the closing </head> tag
					if strings.Contains(html, "</head>") {
						html = strings.Replace(html, "</head>", configScript+"</head>", 1)
						injected = true
					} else if strings.Contains(html, "</HEAD>") { // Case-insensitive check
						html = strings.Replace(html, "</HEAD>", configScript+"</HEAD>", 1)
						injected = true
					} else if strings.Contains(html, "<head>") {
						// Insert after <head> tag
						html = strings.Replace(html, "<head>", "<head>"+configScript, 1)
						injected = true
					} else if strings.Contains(html, "<HEAD>") {
						html = strings.Replace(html, "<HEAD>", "<HEAD>"+configScript, 1)
						injected = true
					} else if strings.Contains(html, "</body>") {
						html = strings.Replace(html, "</body>", configScript+"</body>", 1)
						injected = true
					} else if strings.Contains(html, "</BODY>") { // Case-insensitive check
						html = strings.Replace(html, "</BODY>", configScript+"</BODY>", 1)
						injected = true
					}

					if !injected {
						// Last resort: prepend to HTML
						html = configScript + html
					}

					w.Header().Set("Content-Type", "text/html")
					w.Write([]byte(html))
					return
				}
				url = "index.html"
			}

			if b, err := webapp.ReadFile(path.Join("webapp", url)); err == nil {
				var t string
				switch path.Ext(url) {
				case ".js":
					t = "text/javascript" // see https://github.com/golang/go/issues/32350
				default:
					t = mime.TypeByExtension(path.Ext(url))
				}
				w.Header().Set("Content-Type", t)
				w.Write(b)

			} else if url[:len(url)-1] != "/" {
				if b, err := webapp.ReadFile("webapp/index.html"); err == nil {
					w.Write(b)

				} else {
					w.WriteHeader(http.StatusNotFound)
				}

			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		}
	})).ServeHTTP)

	if port == "80" {
		log.Printf("main interface at http://%s", hostname)
	} else {
		log.Printf("main interface at http://%s:%s", hostname, port)
	}

	sslPrintInfo := func() {
		if sslPort == "443" {
			log.Printf("main interface at https://%s", hostname)
			log.Printf("admin interface at https://%s/admin", hostname)

		} else {
			log.Printf("main interface at https://%s:%s", hostname, sslPort)
			log.Printf("admin interface at https://%s:%s/admin", hostname, sslPort)
		}
	}

	newServer := func(addr string, tlsConfig *tls.Config) *http.Server {
		s := &http.Server{
			Addr:         addr,
			TLSConfig:    tlsConfig,
			ReadTimeout:  10 * time.Minute,                                         // Increased from 30s to 10 minutes for long imports
			WriteTimeout: 10 * time.Minute,                                         // Increased from 30s to 10 minutes for long imports
			ErrorLog:     log.New(os.Stderr, "HTTP_SERVER_ERROR: ", log.LstdFlags), // Enable error logging
		}

		s.SetKeepAlivesEnabled(true)

		return s
	}

	// Store server references for graceful shutdown
	var httpServer *http.Server
	var httpsServer *http.Server

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	// Start HTTPS server if configured
	if len(config.SslCertFile) > 0 && len(config.SslKeyFile) > 0 {
		go func() {
			sslPrintInfo()

			sslCert := config.GetSslCertFilePath()
			sslKey := config.GetSslKeyFilePath()

			httpsServer = newServer(fmt.Sprintf("%s:%s", sslAddr, sslPort), nil)

			if err := httpsServer.ListenAndServeTLS(sslCert, sslKey); err != nil && err != http.ErrServerClosed {
				log.Printf("HTTPS server error: %v", err)
			}
		}()

	} else if config.SslAutoCert != "" {
		go func() {
			sslPrintInfo()

			manager := &autocert.Manager{
				Cache:      autocert.DirCache("autocert"),
				Prompt:     autocert.AcceptTOS,
				HostPolicy: autocert.HostWhitelist(config.SslAutoCert),
			}

			httpsServer = newServer(fmt.Sprintf("%s:%s", sslAddr, sslPort), manager.TLSConfig())

			if err := httpsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				log.Printf("HTTPS server error: %v", err)
			}
		}()

	} else if port == "80" {
		log.Printf("admin interface at http://%s/admin", hostname)

	} else {
		log.Printf("admin interface at http://%s:%s/admin", hostname, port)
	}

	// Start HTTP server in a goroutine
	httpServer = newServer(fmt.Sprintf("%s:%s", addr, port), nil)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	<-sigChan
	log.Println("Shutdown signal received, starting graceful shutdown...")

	// Create shutdown context with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Shutdown HTTP server
	if httpServer != nil {
		log.Println("Shutting down HTTP server...")
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("Error shutting down HTTP server: %v", err)
		} else {
			log.Println("HTTP server shut down gracefully")
		}
	}

	// Shutdown HTTPS server if it exists
	if httpsServer != nil {
		log.Println("Shutting down HTTPS server...")
		if err := httpsServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("Error shutting down HTTPS server: %v", err)
		} else {
			log.Println("HTTPS server shut down gracefully")
		}
	}

	// Terminate controller (shuts down workers, closes database, etc.)
	log.Println("Terminating controller...")
	controller.Terminate()
}

func GetRemoteAddr(r *http.Request) string {
	re := regexp.MustCompile(`(.+):.*$`)

	for _, addr := range strings.Split(r.Header.Get("X-Forwarded-For"), ",") {
		if ip := re.ReplaceAllString(addr, "$1"); len(ip) > 0 {
			return ip
		}
	}

	if ip := re.ReplaceAllString(r.RemoteAddr, "$1"); len(ip) > 0 {
		return ip
	}

	return r.RemoteAddr
}
