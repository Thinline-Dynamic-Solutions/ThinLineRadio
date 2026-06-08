// Copyright (C) 2025 Thinline Dynamic Solutions
//
// Log category taxonomy for TLR admin logs (aligned with Central Management patterns).

package main

import (
	"regexp"
	"strings"
)

const (
	LogCategoryAuth           = "auth"
	LogCategoryUsers          = "users"
	LogCategoryBilling        = "billing"
	LogCategoryEmail          = "email"
	LogCategoryCalls          = "calls"
	LogCategoryDirwatch       = "dirwatch"
	LogCategoryDownstream     = "downstream"
	LogCategoryTranscription  = "transcription"
	LogCategoryTones          = "tones"
	LogCategoryAlerts         = "alerts"
	LogCategoryPush           = "push"
	LogCategoryRelay          = "relay"
	LogCategoryHealth         = "health"
	LogCategoryCentral        = "central"
	LogCategoryAdmin          = "admin"
	LogCategoryWebsocket      = "websocket"
	LogCategoryAutoLearn      = "auto_learn"
	LogCategoryRadioReference = "radioreference"
	LogCategorySystem         = "system"
	LogCategoryUncategorized  = "uncategorized"
)

// AllLogCategories is the canonical list of log categories for admin filtering.
var AllLogCategories = []string{
	LogCategoryAuth,
	LogCategoryUsers,
	LogCategoryBilling,
	LogCategoryEmail,
	LogCategoryCalls,
	LogCategoryDirwatch,
	LogCategoryDownstream,
	LogCategoryTranscription,
	LogCategoryTones,
	LogCategoryAlerts,
	LogCategoryPush,
	LogCategoryRelay,
	LogCategoryHealth,
	LogCategoryCentral,
	LogCategoryAdmin,
	LogCategoryWebsocket,
	LogCategoryAutoLearn,
	LogCategoryRadioReference,
	LogCategorySystem,
	LogCategoryUncategorized,
}

// LogCategoryLabels maps category keys to human-readable labels.
var LogCategoryLabels = map[string]string{
	LogCategoryAuth:           "Authentication",
	LogCategoryUsers:          "Users & Accounts",
	LogCategoryBilling:        "Billing & Stripe",
	LogCategoryEmail:          "Email",
	LogCategoryCalls:          "Call Ingestion",
	LogCategoryDirwatch:       "Dirwatch",
	LogCategoryDownstream:     "Downstreams",
	LogCategoryTranscription:  "Transcription",
	LogCategoryTones:          "Tone Detection",
	LogCategoryAlerts:         "User Alerts",
	LogCategoryPush:           "Push Notifications",
	LogCategoryRelay:          "Relay & Encryption",
	LogCategoryHealth:         "System Health",
	LogCategoryCentral:        "Central Management",
	LogCategoryAdmin:          "Admin & Config",
	LogCategoryWebsocket:      "WebSocket",
	LogCategoryAutoLearn:      "Auto-Learn",
	LogCategoryRadioReference: "Radio Reference",
	LogCategorySystem:         "System",
	LogCategoryUncategorized:  "Other",
}

var logCategoryAllowSet map[string]bool

func init() {
	logCategoryAllowSet = make(map[string]bool, len(AllLogCategories))
	for _, c := range AllLogCategories {
		logCategoryAllowSet[c] = true
	}
}

// FilterLogCategories returns only allowed category keys from a request list.
func FilterLogCategories(requested []string) []string {
	if len(requested) == 0 {
		return nil
	}
	out := make([]string, 0, len(requested))
	for _, c := range requested {
		if logCategoryAllowSet[c] {
			out = append(out, c)
		}
	}
	return out
}

var goLogTimestampRE = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}\s+`)

func stripGoLogTimestamp(msg string) string {
	return goLogTimestampRE.ReplaceAllString(msg, "")
}

// CategorizeLogMessage assigns a category based on message content and optional prefix.
func CategorizeLogMessage(message string) string {
	msg := strings.TrimSpace(message)
	msg = stripGoLogTimestamp(msg)
	if msg == "" {
		return LogCategoryUncategorized
	}

	if strings.HasPrefix(msg, "PANIC") || strings.Contains(strings.ToLower(msg), "panic in ") {
		return LogCategorySystem
	}

	// Structured "prefix: body" messages (dirwatch.ingest:, admin.confighandler.put:, etc.)
	if idx := strings.Index(msg, ": "); idx > 0 && idx < 80 {
		prefix := strings.ToLower(msg[:idx])
		body := msg[idx+2:]
		if cat := categorizeLogPrefix(prefix, body, msg); cat != "" {
			return cat
		}
	}

	return categorizeLogBody(strings.ToLower(msg), msg)
}

func categorizeLogPrefix(prefix, body, full string) string {
	switch {
	case strings.HasPrefix(prefix, "dirwatch"):
		return LogCategoryDirwatch
	case prefix == "downstream" || strings.HasPrefix(prefix, "sync-tone-sets") || strings.HasPrefix(prefix, "tone_downstream"):
		return LogCategoryDownstream
	case strings.HasPrefix(prefix, "push notification"):
		return LogCategoryPush
	case prefix == "api":
		return categorizeAPIPrefix(body, full)
	case strings.HasPrefix(prefix, "controller.ingestcall"):
		return LogCategoryCalls
	case strings.HasPrefix(prefix, "newcall"):
		return LogCategoryCalls
	case strings.HasPrefix(prefix, "admin"):
		return categorizeAdminPrefix(body, full)
	case strings.HasPrefix(prefix, "client."):
		return LogCategoryWebsocket
	case strings.HasPrefix(prefix, "scheduler."):
		return LogCategorySystem
	case strings.HasPrefix(prefix, "delayer."):
		return LogCategorySystem
	case strings.HasPrefix(prefix, "radioreference"):
		return LogCategoryRadioReference
	}
	return ""
}

func categorizeAPIPrefix(body, full string) string {
	lower := strings.ToLower(body)
	fullLower := strings.ToLower(full)

	// Call upload / ingest pipeline only
	if strings.Contains(lower, "[upload") || strings.Contains(lower, "[tr-upload") ||
		strings.Contains(lower, "incomplete call data") || strings.Contains(lower, "handlecall") ||
		strings.Contains(lower, "passing to handlecall") ||
		strings.Contains(fullLower, "/api/call-upload") || strings.Contains(fullLower, "/api/trunk-recorder") {
		return LogCategoryCalls
	}

	// Authentication & access errors (exitWithError / exitWithErrorContext)
	if strings.Contains(lower, "invalid pin") || strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "invalid credentials") || strings.Contains(lower, "forbidden") ||
		strings.Contains(lower, "access denied") || strings.Contains(lower, "invalid api key") ||
		strings.Contains(lower, "invalid token") || strings.Contains(lower, "not authenticated") ||
		strings.Contains(lower, "invalid password") || strings.Contains(lower, "turnstile") ||
		strings.Contains(fullLower, "/api/user/login") || strings.Contains(fullLower, "/api/admin/login") ||
		strings.Contains(fullLower, "/api/user/logout") {
		return LogCategoryAuth
	}

	// Registration, accounts, preferences
	if strings.Contains(lower, "registration") || strings.Contains(lower, "invitation") ||
		strings.Contains(lower, "user not found") || strings.Contains(lower, "group not found") ||
		strings.Contains(lower, "preferences") || strings.Contains(lower, "keyword list") ||
		strings.Contains(fullLower, "/api/user/register") {
		return LogCategoryUsers
	}

	if strings.Contains(lower, "stripe") || strings.Contains(lower, "subscription") ||
		strings.Contains(lower, "billing") || strings.Contains(lower, "checkout") {
		return LogCategoryBilling
	}

	if strings.Contains(lower, "verification email") || strings.Contains(lower, "failed to send") && strings.Contains(lower, "email") {
		return LogCategoryEmail
	}

	return LogCategoryUncategorized
}

func categorizeAdminPrefix(body, full string) string {
	lower := strings.ToLower(body)
	fullLower := strings.ToLower(full)
	switch {
	case strings.Contains(fullLower, "login") || strings.Contains(fullLower, "sso"):
		return LogCategoryAuth
	case strings.Contains(fullLower, "stripe"):
		return LogCategoryBilling
	case strings.Contains(lower, "transcription failures"):
		return LogCategoryTranscription
	case strings.Contains(lower, "audio encryption"):
		return LogCategoryRelay
	case strings.Contains(lower, "sync-tone-sets"):
		return LogCategoryDownstream
	case strings.Contains(lower, "tone import"):
		return LogCategoryAdmin
	case strings.Contains(lower, "calls deleted") || strings.Contains(lower, "calls purged"):
		return LogCategoryAdmin
	case strings.Contains(lower, "password changed"):
		return LogCategoryAuth
	}
	return LogCategoryAdmin
}

func categorizeLogBody(lower, original string) string {
	switch {
	// Central Management
	case strings.Contains(lower, "central management") || strings.HasPrefix(lower, "centralwebhook"):
		return LogCategoryCentral

	// Dirwatch (non-prefix)
	case strings.Contains(lower, "dirwatch"):
		return LogCategoryDirwatch

	// Push
	case strings.Contains(lower, "push notification"):
		return LogCategoryPush

	// Email
	case strings.Contains(lower, "verification email") || strings.Contains(lower, "password reset email") ||
		strings.Contains(lower, "signup verification email") || strings.Contains(lower, "mobile setup email") ||
		strings.Contains(lower, "mobile welcome email") || strings.Contains(lower, "relay listener emails") ||
		strings.Contains(lower, "sendgrid") || strings.Contains(lower, "mailgun") ||
		(strings.Contains(lower, "email sent") && !strings.Contains(lower, "stripe customer email")) ||
		strings.Contains(lower, "email service is disabled") || strings.Contains(lower, "email provider"):
		return LogCategoryEmail

	// Billing / Stripe
	case strings.Contains(lower, "stripe") || strings.Contains(lower, "checkout.session") ||
		strings.Contains(lower, "invoice payment") || strings.Contains(lower, "billing-enabled group"):
		return LogCategoryBilling

	// Hydra / transcript review / hallucination
	case strings.Contains(lower, "hydra retrieval") || strings.Contains(lower, "transcript review") ||
		strings.Contains(lower, "hallucination") || strings.Contains(lower, "transcription queue") ||
		strings.Contains(lower, "transcription worker") || strings.Contains(lower, "transcription completed") ||
		strings.Contains(lower, "transcription debug") || strings.Contains(lower, "keyword processing") ||
		strings.Contains(lower, "keyword match") || strings.Contains(lower, "resetting transcription") ||
		strings.Contains(lower, "reset ") && strings.Contains(lower, "transcription") ||
		strings.Contains(lower, "queued call") && strings.Contains(lower, "hydra transcription"):
		return LogCategoryTranscription

	// Auto-learn
	case strings.Contains(lower, "auto-learn") || strings.Contains(lower, "auto learn") ||
		strings.Contains(lower, "tone history analyze"):
		return LogCategoryAutoLearn

	// Tone detection pipeline
	case strings.Contains(lower, "tone detection") || strings.Contains(lower, "tones detected") ||
		strings.Contains(lower, "pending tone") || strings.Contains(lower, "orphaned tone") ||
		strings.Contains(lower, "waiting short call") || strings.Contains(lower, "tone set(s) matched") ||
		strings.Contains(lower, "tone-only"):
		return LogCategoryTones

	// User alerts
	case strings.Contains(lower, "pre-alert") || strings.Contains(lower, "tone alert") ||
		strings.Contains(lower, "transcript alert") || strings.Contains(lower, "alert created") ||
		strings.Contains(lower, "tone set debug") || strings.Contains(lower, "[tone set debug]"):
		return LogCategoryAlerts

	// System health
	case strings.Contains(lower, "no-audio") || strings.Contains(lower, "system alert") ||
		strings.Contains(lower, "system health") || strings.Contains(lower, "failed to check transcription failures") ||
		strings.Contains(lower, "failed to check tone detection"):
		return LogCategoryHealth

	// Relay / encryption / suspension
	case strings.Contains(lower, "audio encryption") || strings.Contains(lower, "relay suspension") ||
		strings.Contains(lower, "relay api key") || strings.Contains(lower, "relay server") ||
		strings.Contains(lower, "relay listener pin") || strings.HasPrefix(lower, "fcm command"):
		return LogCategoryRelay

	// WebSocket / listeners / reconnection
	case strings.Contains(lower, "websocket") || strings.Contains(lower, "new listener from") ||
		strings.Contains(lower, "listener disconnected") || strings.Contains(lower, "listeners count") ||
		strings.Contains(lower, "[reconnectionmanager]"):
		return LogCategoryWebsocket

	// Users & accounts
	case strings.Contains(lower, "admin created new user") || strings.Contains(lower, "admin reset password") ||
		strings.Contains(lower, "devicetokens.") || strings.Contains(lower, "device token") ||
		strings.Contains(lower, "preferences cache") || strings.Contains(lower, "keyword lists cache") ||
		strings.Contains(lower, "registration -") || strings.Contains(lower, "registration code") ||
		strings.Contains(lower, "invitation") || strings.Contains(lower, "mobile setup") ||
		strings.Contains(lower, "invalid user pin") || strings.Contains(lower, "expired pin") ||
		strings.Contains(lower, "too many concurrent connections") ||
		strings.Contains(lower, "user connected with expired pin"):
		return LogCategoryUsers

	// Auth (admin login)
	case strings.Contains(lower, "admin login") || strings.Contains(lower, "login attempt") ||
		strings.Contains(lower, "too many login attempts") || strings.Contains(lower, "admin password changed") ||
		strings.Contains(lower, "turnstile verification"):
		return LogCategoryAuth

	// Radio Reference
	case strings.Contains(lower, "radio reference") || strings.Contains(lower, "radioreference"):
		return LogCategoryRadioReference

	// Downstream
	case strings.HasPrefix(lower, "downstream:") || strings.Contains(lower, "sync-tone-sets"):
		return LogCategoryDownstream

	// Call ingestion
	case strings.Contains(lower, "newcall:") || strings.Contains(lower, "ingestcall") ||
		strings.Contains(lower, "[upload parsed]") || strings.Contains(lower, "purgelegacyduplicates") ||
		strings.Contains(lower, "search results query") || strings.Contains(lower, "client access evaluation"):
		return LogCategoryCalls

	// Admin & config
	case strings.Contains(lower, "configuration changed") || strings.Contains(lower, "options changed") ||
		strings.Contains(lower, "keyword list id repair") || strings.Contains(lower, "operator unlocked") ||
		strings.Contains(lower, "admin access denied"):
		return LogCategoryAdmin

	// System / startup / maintenance
	case strings.Contains(lower, "server started") || strings.HasPrefix(lower, "startup:") ||
		strings.Contains(lower, "worker pool") || strings.Contains(lower, "database pruning") ||
		strings.Contains(lower, "auto-update") || strings.Contains(lower, "migration") ||
		strings.Contains(lower, "loaded ") && strings.Contains(lower, "cache") ||
		strings.Contains(lower, "config synced") || strings.Contains(lower, "base folder is"):
		return LogCategorySystem
	}

	_ = original
	return LogCategoryUncategorized
}

// InferLogLevelFromMessage guesses severity when capturing unstructured stdout lines.
func InferLogLevelFromMessage(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "panic"):
		return LogLevelError
	case strings.Contains(lower, "failed") || strings.Contains(lower, "error") ||
		strings.HasPrefix(lower, "warning:") || strings.Contains(lower, " unreachable"):
		if strings.HasPrefix(lower, "warning:") || strings.Contains(lower, " warning") {
			return LogLevelWarn
		}
		return LogLevelError
	default:
		return LogLevelInfo
	}
}
