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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

type Options struct {
	AudioConversion             uint   `json:"audioConversion"`
	AudioCodec                  string `json:"audioCodec"`  // "opus" or "aac" (m4a)
	AudioBitrate                uint   `json:"audioBitrate"` // Bitrate in kbps (16, 24, 32, 48, 64)
	AutoPopulate                bool   `json:"autoPopulate"`
	Branding                    string `json:"branding"`
	DefaultSystemDelay          uint   `json:"defaultSystemDelay"`
	DimmerDelay                 uint   `json:"dimmerDelay"`
	DisableDuplicateDetection      bool   `json:"disableDuplicateDetection"`
	DuplicateDetectionMode         string `json:"duplicateDetectionMode"`         // "legacy" or "advanced"
	DuplicateDetectionTimeFrame    uint   `json:"duplicateDetectionTimeFrame"`    // Legacy mode timeframe
	AdvancedDetectionTimeFrame     uint   `json:"advancedDetectionTimeFrame"`     // Advanced mode timeframe
	Email                          string `json:"email"`
	KeypadBeeps                 string `json:"keypadBeeps"`
	MaxClients                  uint   `json:"maxClients"`
	PlaybackGoesLive            bool   `json:"playbackGoesLive"`
	PruneDays                   uint   `json:"pruneDays"`
	ShowListenersCount          bool   `json:"showListenersCount"`
	SortTalkgroups              bool   `json:"sortTalkgroups"`
	Time12hFormat               bool   `json:"time12hFormat"`
	RadioReferenceEnabled       bool   `json:"radioReferenceEnabled"`
	RadioReferenceUsername      string `json:"radioReferenceUsername"`
	RadioReferencePassword      string `json:"radioReferencePassword"`
	UserRegistrationEnabled     bool   `json:"userRegistrationEnabled"`
	PublicRegistrationEnabled   bool   `json:"publicRegistrationEnabled"`
	PublicRegistrationMode      string `json:"publicRegistrationMode"` // "codes", "email", "both"
	StripePaywallEnabled        bool   `json:"stripePaywallEnabled"`
	EmailServiceEnabled         bool   `json:"emailServiceEnabled"`
	EmailServiceType            string `json:"emailServiceType"` // "emailjs" or "smtp"
	EmailServiceApiKey          string `json:"emailServiceApiKey"`
	EmailServiceDomain          string `json:"emailServiceDomain"`
	EmailServiceTemplateId      string `json:"emailServiceTemplateId"`
	// Email provider selection
	EmailProvider               string `json:"emailProvider"` // "sendgrid", "mailgun", or "smtp"
	// SendGrid settings
	EmailSendGridAPIKey         string `json:"emailSendGridApiKey"`
	// Mailgun settings
	EmailMailgunAPIKey          string `json:"emailMailgunApiKey"`
	EmailMailgunDomain          string `json:"emailMailgunDomain"`
	EmailMailgunAPIBase         string `json:"emailMailgunApiBase"` // "https://api.mailgun.net" (US) or "https://api.eu.mailgun.net" (EU)
	// SMTP settings
	EmailSmtpHost               string `json:"emailSmtpHost"`     // SMTP server hostname
	EmailSmtpPort               int    `json:"emailSmtpPort"`     // SMTP server port (25, 465, 587, etc.)
	EmailSmtpUsername           string `json:"emailSmtpUsername"` // SMTP authentication username
	EmailSmtpPassword           string `json:"emailSmtpPassword"` // SMTP authentication password
	EmailSmtpUseTLS             bool   `json:"emailSmtpUseTLS"`   // Use TLS/SSL connection
	EmailSmtpSkipVerify         bool   `json:"emailSmtpSkipVerify"` // Skip certificate verification (for self-signed certs)
	// Email settings (common to all providers)
	EmailSmtpFromEmail          string `json:"emailSmtpFromEmail"`
	EmailSmtpFromName            string `json:"emailSmtpFromName"`
	// Email logo settings
	EmailLogoFilename            string `json:"emailLogoFilename"` // Filename of logo file (stored in base directory)
	EmailLogoBorderRadius       string `json:"emailLogoBorderRadius"` // Border radius for email logo (e.g., "0px", "8px", "50%")
	// Favicon settings
	FaviconFilename             string `json:"faviconFilename"` // Filename of favicon file (stored in base directory)
	StripePublishableKey        string `json:"stripePublishableKey"`
	StripeSecretKey             string `json:"stripeSecretKey"`
	StripeWebhookSecret         string `json:"stripeWebhookSecret"`
	StripeGracePeriodDays        uint   `json:"stripeGracePeriodDays"`
	StripePriceId               string            `json:"stripePriceId"`
	BaseUrl                     string            `json:"baseUrl"`
	TranscriptionConfig         TranscriptionConfig `json:"transcriptionConfig"`
	TranscriptionFailureThreshold uint            `json:"transcriptionFailureThreshold"`
	ToneDetectionIssueThreshold uint            `json:"toneDetectionIssueThreshold"`
	AlertRetentionDays          uint              `json:"alertRetentionDays"`
	NoAudioThresholdMinutes     uint            `json:"noAudioThresholdMinutes"`
	NoAudioMultiplier            float64         `json:"noAudioMultiplier"`
	SystemHealthAlertsEnabled   bool            `json:"systemHealthAlertsEnabled"`
	// Individual alert type toggles
	TranscriptionFailureAlertsEnabled bool   `json:"transcriptionFailureAlertsEnabled"`
	ToneDetectionAlertsEnabled        bool   `json:"toneDetectionAlertsEnabled"`
	NoAudioAlertsEnabled              bool   `json:"noAudioAlertsEnabled"`
	// Time windows (in hours)
	TranscriptionFailureTimeWindow    uint   `json:"transcriptionFailureTimeWindow"`
	ToneDetectionTimeWindow           uint   `json:"toneDetectionTimeWindow"`
	NoAudioTimeWindow                 uint   `json:"noAudioTimeWindow"`
	// Historical data window for no audio (in days)
	NoAudioHistoricalDataDays         uint   `json:"noAudioHistoricalDataDays"`
	// Repeat alert intervals (in minutes)
	TranscriptionFailureRepeatMinutes uint   `json:"transcriptionFailureRepeatMinutes"`
	ToneDetectionRepeatMinutes        uint   `json:"toneDetectionRepeatMinutes"`
	NoAudioRepeatMinutes              uint   `json:"noAudioRepeatMinutes"`
	RelayServerURL              string            `json:"relayServerURL"`
	RelayServerAPIKey           string            `json:"relayServerAPIKey"`
	RadioReferenceAPIKey        string            `json:"radioReferenceAPIKey"`
	AdminLocalhostOnly          bool              `json:"adminLocalhostOnly"`
	ConfigSyncEnabled           bool              `json:"configSyncEnabled"`
	ConfigSyncPath              string            `json:"configSyncPath"`
	// Cloudflare Turnstile configuration (for user registration/login and group admin login)
	TurnstileEnabled            bool              `json:"turnstileEnabled"`
	TurnstileSiteKey            string            `json:"turnstileSiteKey"`
	TurnstileSecretKey          string            `json:"turnstileSecretKey"`
	// Reconnection manager configuration
	ReconnectionEnabled         bool              `json:"reconnectionEnabled"`
	ReconnectionGracePeriod     uint              `json:"reconnectionGracePeriod"` // In seconds
	ReconnectionMaxBufferSize   uint              `json:"reconnectionMaxBufferSize"` // Maximum calls to buffer per user
	adminPassword               string
	adminPasswordNeedChange     bool
	mutex                       sync.Mutex
	secret                      string
}

// TranscriptionConfig contains configuration for transcription
type TranscriptionConfig struct {
	Enabled                      bool     `json:"enabled"`
	Provider                     string   `json:"provider"`                     // "whisper-api", "azure", "google", "assemblyai"
	Language                     string   `json:"language"`                     // "en", "auto"
	Prompt                       string   `json:"prompt"`                       // Custom prompt for Whisper to guide transcription (e.g., terminology, formatting)
	WorkerPoolSize               int      `json:"workerPoolSize"`
	MinCallDuration              float64  `json:"minCallDuration"`              // Minimum call duration in seconds to transcribe (default: 0 = transcribe all)
	WhisperAPIURL                string   `json:"whisperAPIURL"`                // Base URL for external Whisper API server (e.g., "http://localhost:8000") or OpenAI API URL
	WhisperAPIKey                string   `json:"whisperAPIKey"`                // Optional API key for external Whisper API server or OpenAI API key
	AzureKey                     string   `json:"azureKey"`                     // Azure Speech Services subscription key
	AzureRegion                  string   `json:"azureRegion"`                  // Azure Speech Services region (e.g., "eastus", "westus2")
	GoogleAPIKey                 string   `json:"googleAPIKey"`                 // Google Cloud Speech-to-Text API key
	GoogleCredentials            string   `json:"googleCredentials"`            // Google Cloud service account JSON credentials (alternative to API key)
	AssemblyAIKey                string   `json:"assemblyAIKey"`                // AssemblyAI API key
	AssemblyAIWordBoost          []string `json:"assemblyAIWordBoost"`          // Word boost/keyterms for AssemblyAI (max 100 terms, 50 chars each)
	HallucinationPatterns        []string `json:"hallucinationPatterns"`        // Patterns to remove from transcripts (Whisper hallucinations)
	HallucinationDetectionMode   string   `json:"hallucinationDetectionMode"`   // "off", "manual", "auto"
	HallucinationMinOccurrences  int      `json:"hallucinationMinOccurrences"`  // Minimum times a phrase must appear in rejected calls before flagging (default: 5)
}

const (
	AUDIO_CONVERSION_DISABLED              = 0
	AUDIO_CONVERSION_ENABLED               = 1
	AUDIO_CONVERSION_CONSERVATIVE_NORM     = 2 // -16 LUFS (Broadcast standard)
	AUDIO_CONVERSION_STANDARD_NORM         = 3 // -12 LUFS (Modern streaming, recommended)
	AUDIO_CONVERSION_AGGRESSIVE_NORM       = 4 // -10 LUFS (Dispatcher/public safety optimized)
	AUDIO_CONVERSION_MAXIMUM_NORM          = 5 // -8 LUFS (Very loud, heavily compressed)
)

// getRelayServerAuthKey returns the authorization key for relay server API requests
// This is derived from a hash to avoid exposing the key directly in source code
// The key is consistent across all installations (same seed = same hash)
func getRelayServerAuthKey() string {
	// Hash of a constant string - same result every time, but not obvious from code
	// This seed must match the one in relay-server/config/config.go
	const seed = "thinline-radio-relay-auth-2026"
	hash := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(hash[:])
}

func NewOptions() *Options {
	return &Options{
		mutex: sync.Mutex{},
	}
}

func (options *Options) FromMap(m map[string]any) *Options {
	options.mutex.Lock()
	defer options.mutex.Unlock()

	switch v := m["audioConversion"].(type) {
	case float64:
		options.AudioConversion = uint(v)
	default:
		options.AudioConversion = defaults.options.audioConversion
	}

	switch v := m["autoPopulate"].(type) {
	case bool:
		options.AutoPopulate = v
	default:
		options.AutoPopulate = defaults.options.autoPopulate
	}

	switch v := m["defaultSystemDelay"].(type) {
	case float64:
		options.DefaultSystemDelay = uint(v)
	case int:
		options.DefaultSystemDelay = uint(v)
	case int64:
		options.DefaultSystemDelay = uint(v)
	default:
		options.DefaultSystemDelay = defaults.options.defaultSystemDelay
	}

	switch v := m["branding"].(type) {
	case string:
		options.Branding = v
	}

	switch v := m["dimmerDelay"].(type) {
	case float64:
		options.DimmerDelay = uint(v)
	default:
		options.DimmerDelay = defaults.options.dimmerDelay
	}

	switch v := m["disableDuplicateDetection"].(type) {
	case bool:
		options.DisableDuplicateDetection = v
	default:
		options.DisableDuplicateDetection = defaults.options.disableDuplicateDetection
	}

	switch v := m["duplicateDetectionMode"].(type) {
	case string:
		// Validate mode is "legacy" or "advanced"
		if v == "legacy" || v == "advanced" {
			options.DuplicateDetectionMode = v
		} else {
			options.DuplicateDetectionMode = defaults.options.duplicateDetectionMode
		}
	default:
		options.DuplicateDetectionMode = defaults.options.duplicateDetectionMode
	}

	switch v := m["duplicateDetectionTimeFrame"].(type) {
	case float64:
		options.DuplicateDetectionTimeFrame = uint(v)
	default:
		options.DuplicateDetectionTimeFrame = defaults.options.duplicateDetectionTimeFrame
	}

	switch v := m["advancedDetectionTimeFrame"].(type) {
	case float64:
		options.AdvancedDetectionTimeFrame = uint(v)
	default:
		options.AdvancedDetectionTimeFrame = defaults.options.advancedDetectionTimeFrame
	}

	switch v := m["email"].(type) {
	case string:
		options.Email = v
	}

	switch v := m["keypadBeeps"].(type) {
	case string:
		options.KeypadBeeps = v
	default:
		options.KeypadBeeps = defaults.options.keypadBeeps
	}

	switch v := m["maxClients"].(type) {
	case float64:
		options.MaxClients = uint(v)
	default:
		options.MaxClients = defaults.options.maxClients
	}

	switch v := m["playbackGoesLive"].(type) {
	case bool:
		options.PlaybackGoesLive = v
	}

	switch v := m["pruneDays"].(type) {
	case float64:
		options.PruneDays = uint(v)
	default:
		options.PruneDays = defaults.options.pruneDays
	}

	switch v := m["showListenersCount"].(type) {
	case bool:
		options.ShowListenersCount = v
	default:
		options.ShowListenersCount = defaults.options.showListenersCount
	}

	switch v := m["sortTalkgroups"].(type) {
	case bool:
		options.SortTalkgroups = v
	default:
		options.SortTalkgroups = defaults.options.sortTalkgroups
	}

	switch v := m["time12hFormat"].(type) {
	case bool:
		options.Time12hFormat = v
	default:
		options.Time12hFormat = defaults.options.time12hFormat
	}

	switch v := m["radioReferenceEnabled"].(type) {
	case bool:
		options.RadioReferenceEnabled = v
	default:
		options.RadioReferenceEnabled = defaults.options.radioReferenceEnabled
	}

	switch v := m["radioReferenceUsername"].(type) {
	case string:
		options.RadioReferenceUsername = v
	default:
		options.RadioReferenceUsername = defaults.options.radioReferenceUsername
	}

	switch v := m["radioReferencePassword"].(type) {
	case string:
		options.RadioReferencePassword = v
	default:
		options.RadioReferencePassword = defaults.options.radioReferencePassword
	}

	switch v := m["userRegistrationEnabled"].(type) {
	case bool:
		options.UserRegistrationEnabled = v
	default:
		options.UserRegistrationEnabled = defaults.options.userRegistrationEnabled
	}

	switch v := m["publicRegistrationEnabled"].(type) {
	case bool:
		options.PublicRegistrationEnabled = v
	default:
		options.PublicRegistrationEnabled = defaults.options.publicRegistrationEnabled
	}

	switch v := m["publicRegistrationMode"].(type) {
	case string:
		options.PublicRegistrationMode = v
	default:
		options.PublicRegistrationMode = defaults.options.publicRegistrationMode
	}

	switch v := m["stripePaywallEnabled"].(type) {
	case bool:
		options.StripePaywallEnabled = v
	default:
		options.StripePaywallEnabled = defaults.options.stripePaywallEnabled
	}

	switch v := m["emailServiceEnabled"].(type) {
	case bool:
		options.EmailServiceEnabled = v
	default:
		options.EmailServiceEnabled = defaults.options.emailServiceEnabled
	}

	switch v := m["emailServiceApiKey"].(type) {
	case string:
		options.EmailServiceApiKey = v
	default:
		options.EmailServiceApiKey = defaults.options.emailServiceApiKey
	}

	switch v := m["emailServiceDomain"].(type) {
	case string:
		options.EmailServiceDomain = v
	default:
		options.EmailServiceDomain = defaults.options.emailServiceDomain
	}

	switch v := m["emailServiceTemplateId"].(type) {
	case string:
		options.EmailServiceTemplateId = v
	default:
		options.EmailServiceTemplateId = defaults.options.emailServiceTemplateId
	}

	switch v := m["emailProvider"].(type) {
	case string:
		options.EmailProvider = v
	default:
		options.EmailProvider = defaults.options.emailProvider
	}

	switch v := m["emailSendGridApiKey"].(type) {
	case string:
		options.EmailSendGridAPIKey = v
	default:
		options.EmailSendGridAPIKey = defaults.options.emailSendGridAPIKey
	}

	switch v := m["emailMailgunApiKey"].(type) {
	case string:
		options.EmailMailgunAPIKey = v
	default:
		options.EmailMailgunAPIKey = defaults.options.emailMailgunAPIKey
	}

	switch v := m["emailMailgunDomain"].(type) {
	case string:
		options.EmailMailgunDomain = v
	default:
		options.EmailMailgunDomain = defaults.options.emailMailgunDomain
	}

	switch v := m["emailMailgunApiBase"].(type) {
	case string:
		options.EmailMailgunAPIBase = v
	default:
		options.EmailMailgunAPIBase = defaults.options.emailMailgunAPIBase
	}

	switch v := m["emailSmtpHost"].(type) {
	case string:
		options.EmailSmtpHost = v
	default:
		options.EmailSmtpHost = defaults.options.emailSmtpHost
	}

	switch v := m["emailSmtpPort"].(type) {
	case float64:
		options.EmailSmtpPort = int(v)
	case int:
		options.EmailSmtpPort = v
	default:
		options.EmailSmtpPort = defaults.options.emailSmtpPort
	}

	switch v := m["emailSmtpUsername"].(type) {
	case string:
		options.EmailSmtpUsername = v
	default:
		options.EmailSmtpUsername = defaults.options.emailSmtpUsername
	}

	switch v := m["emailSmtpPassword"].(type) {
	case string:
		options.EmailSmtpPassword = v
	default:
		options.EmailSmtpPassword = defaults.options.emailSmtpPassword
	}

	switch v := m["emailSmtpUseTLS"].(type) {
	case bool:
		options.EmailSmtpUseTLS = v
	default:
		options.EmailSmtpUseTLS = defaults.options.emailSmtpUseTLS
	}

	switch v := m["emailSmtpSkipVerify"].(type) {
	case bool:
		options.EmailSmtpSkipVerify = v
	default:
		options.EmailSmtpSkipVerify = defaults.options.emailSmtpSkipVerify
	}

	switch v := m["emailSmtpFromEmail"].(type) {
	case string:
		options.EmailSmtpFromEmail = v
	default:
		options.EmailSmtpFromEmail = defaults.options.emailSmtpFromEmail
	}

	switch v := m["emailSmtpFromName"].(type) {
	case string:
		options.EmailSmtpFromName = v
	default:
		options.EmailSmtpFromName = defaults.options.emailSmtpFromName
	}

	switch v := m["emailLogoFilename"].(type) {
	case string:
		options.EmailLogoFilename = v
	default:
		options.EmailLogoFilename = defaults.options.emailLogoFilename
	}

	switch v := m["emailLogoBorderRadius"].(type) {
	case string:
		options.EmailLogoBorderRadius = v
	default:
		options.EmailLogoBorderRadius = defaults.options.emailLogoBorderRadius
	}

	switch v := m["faviconFilename"].(type) {
	case string:
		options.FaviconFilename = v
	default:
		options.FaviconFilename = defaults.options.faviconFilename
	}

	switch v := m["stripePublishableKey"].(type) {
	case string:
		options.StripePublishableKey = v
	default:
		options.StripePublishableKey = defaults.options.stripePublishableKey
	}

	switch v := m["stripeSecretKey"].(type) {
	case string:
		options.StripeSecretKey = v
	default:
		options.StripeSecretKey = defaults.options.stripeSecretKey
	}

	switch v := m["stripeWebhookSecret"].(type) {
	case string:
		options.StripeWebhookSecret = v
	default:
		options.StripeWebhookSecret = defaults.options.stripeWebhookSecret
	}

	switch v := m["stripeGracePeriodDays"].(type) {
	case float64:
		options.StripeGracePeriodDays = uint(v)
	case int:
		options.StripeGracePeriodDays = uint(v)
	default:
		options.StripeGracePeriodDays = defaults.options.stripeGracePeriodDays
	}

	switch v := m["stripePriceId"].(type) {
	case string:
		options.StripePriceId = v
	default:
		options.StripePriceId = defaults.options.stripePriceId
	}

	switch v := m["baseUrl"].(type) {
	case string:
		options.BaseUrl = v
	default:
		options.BaseUrl = defaults.options.baseUrl
	}

	switch v := m["alertRetentionDays"].(type) {
	case float64:
		options.AlertRetentionDays = uint(v)
	case int:
		options.AlertRetentionDays = uint(v)
	case int64:
		options.AlertRetentionDays = uint(v)
	default:
		options.AlertRetentionDays = defaults.options.alertRetentionDays
	}

	switch v := m["transcriptionFailureThreshold"].(type) {
	case float64:
		options.TranscriptionFailureThreshold = uint(v)
	case int:
		options.TranscriptionFailureThreshold = uint(v)
	case int64:
		options.TranscriptionFailureThreshold = uint(v)
	default:
		options.TranscriptionFailureThreshold = defaults.options.transcriptionFailureThreshold
	}

	switch v := m["toneDetectionIssueThreshold"].(type) {
	case float64:
		options.ToneDetectionIssueThreshold = uint(v)
	case int:
		options.ToneDetectionIssueThreshold = uint(v)
	case int64:
		options.ToneDetectionIssueThreshold = uint(v)
	default:
		options.ToneDetectionIssueThreshold = defaults.options.toneDetectionIssueThreshold
	}

	switch v := m["relayServerURL"].(type) {
	case string:
		options.RelayServerURL = v
	default:
		options.RelayServerURL = ""
	}

	switch v := m["relayServerAPIKey"].(type) {
	case string:
		options.RelayServerAPIKey = v
	default:
		options.RelayServerAPIKey = ""
	}

	switch v := m["radioReferenceAPIKey"].(type) {
	case string:
		options.RadioReferenceAPIKey = v
	default:
		options.RadioReferenceAPIKey = ""
	}

	switch v := m["adminLocalhostOnly"].(type) {
	case bool:
		options.AdminLocalhostOnly = v
	default:
		options.AdminLocalhostOnly = defaults.options.adminLocalhostOnly
	}

	switch v := m["transcriptionFailureAlertsEnabled"].(type) {
	case bool:
		options.TranscriptionFailureAlertsEnabled = v
	default:
		options.TranscriptionFailureAlertsEnabled = defaults.options.transcriptionFailureAlertsEnabled
	}

	switch v := m["toneDetectionAlertsEnabled"].(type) {
	case bool:
		options.ToneDetectionAlertsEnabled = v
	default:
		options.ToneDetectionAlertsEnabled = defaults.options.toneDetectionAlertsEnabled
	}

	switch v := m["noAudioAlertsEnabled"].(type) {
	case bool:
		options.NoAudioAlertsEnabled = v
	default:
		options.NoAudioAlertsEnabled = defaults.options.noAudioAlertsEnabled
	}

	switch v := m["transcriptionFailureTimeWindow"].(type) {
	case float64:
		options.TranscriptionFailureTimeWindow = uint(v)
	case int:
		options.TranscriptionFailureTimeWindow = uint(v)
	case int64:
		options.TranscriptionFailureTimeWindow = uint(v)
	default:
		options.TranscriptionFailureTimeWindow = defaults.options.transcriptionFailureTimeWindow
	}

	switch v := m["toneDetectionTimeWindow"].(type) {
	case float64:
		options.ToneDetectionTimeWindow = uint(v)
	case int:
		options.ToneDetectionTimeWindow = uint(v)
	case int64:
		options.ToneDetectionTimeWindow = uint(v)
	default:
		options.ToneDetectionTimeWindow = defaults.options.toneDetectionTimeWindow
	}

	switch v := m["noAudioTimeWindow"].(type) {
	case float64:
		options.NoAudioTimeWindow = uint(v)
	case int:
		options.NoAudioTimeWindow = uint(v)
	case int64:
		options.NoAudioTimeWindow = uint(v)
	default:
		options.NoAudioTimeWindow = defaults.options.noAudioTimeWindow
	}

	switch v := m["noAudioHistoricalDataDays"].(type) {
	case float64:
		options.NoAudioHistoricalDataDays = uint(v)
	case int:
		options.NoAudioHistoricalDataDays = uint(v)
	case int64:
		options.NoAudioHistoricalDataDays = uint(v)
	default:
		options.NoAudioHistoricalDataDays = defaults.options.noAudioHistoricalDataDays
	}

	switch v := m["configSyncEnabled"].(type) {
	case bool:
		options.ConfigSyncEnabled = v
	default:
		options.ConfigSyncEnabled = defaults.options.configSyncEnabled
	}

	switch v := m["configSyncPath"].(type) {
	case string:
		options.ConfigSyncPath = v
	default:
		options.ConfigSyncPath = defaults.options.configSyncPath
	}

	switch v := m["turnstileEnabled"].(type) {
	case bool:
		options.TurnstileEnabled = v
	default:
		options.TurnstileEnabled = false
	}

	switch v := m["turnstileSiteKey"].(type) {
	case string:
		options.TurnstileSiteKey = v
	default:
		options.TurnstileSiteKey = ""
	}

	switch v := m["turnstileSecretKey"].(type) {
	case string:
		options.TurnstileSecretKey = v
	default:
		options.TurnstileSecretKey = ""
	}

	switch v := m["reconnectionEnabled"].(type) {
	case bool:
		options.ReconnectionEnabled = v
	default:
		options.ReconnectionEnabled = defaults.options.reconnectionEnabled
	}

	switch v := m["reconnectionGracePeriod"].(type) {
	case float64:
		options.ReconnectionGracePeriod = uint(v)
	case int:
		options.ReconnectionGracePeriod = uint(v)
	default:
		options.ReconnectionGracePeriod = defaults.options.reconnectionGracePeriod
	}

	switch v := m["reconnectionMaxBufferSize"].(type) {
	case float64:
		options.ReconnectionMaxBufferSize = uint(v)
	case int:
		options.ReconnectionMaxBufferSize = uint(v)
	default:
		options.ReconnectionMaxBufferSize = defaults.options.reconnectionMaxBufferSize
	}

	// Transcription: allow flat toggle and nested config from admin UI
	if v, ok := m["transcriptionEnabled"].(bool); ok {
		options.TranscriptionConfig.Enabled = v
	}
	if tc, ok := m["transcriptionConfig"].(map[string]any); ok {
		if v, ok := tc["enabled"].(bool); ok {
			options.TranscriptionConfig.Enabled = v
		}
		if v, ok := tc["provider"].(string); ok && v != "" {
			options.TranscriptionConfig.Provider = v
		}
		if v, ok := tc["language"].(string); ok && v != "" {
			options.TranscriptionConfig.Language = v
		}
		if v, ok := tc["prompt"].(string); ok {
			options.TranscriptionConfig.Prompt = v
		}
		if v, ok := tc["workerPoolSize"].(float64); ok && v > 0 {
			options.TranscriptionConfig.WorkerPoolSize = int(v)
		}
		if v, ok := tc["minCallDuration"].(float64); ok {
			options.TranscriptionConfig.MinCallDuration = v
		}
		if v, ok := tc["whisperAPIURL"].(string); ok {
			options.TranscriptionConfig.WhisperAPIURL = v
		}
		if v, ok := tc["whisperAPIKey"].(string); ok {
			options.TranscriptionConfig.WhisperAPIKey = v
		}
		if v, ok := tc["azureKey"].(string); ok {
			options.TranscriptionConfig.AzureKey = v
		}
		if v, ok := tc["azureRegion"].(string); ok {
			options.TranscriptionConfig.AzureRegion = v
		}
		if v, ok := tc["googleAPIKey"].(string); ok {
			options.TranscriptionConfig.GoogleAPIKey = v
		}
		if v, ok := tc["googleCredentials"].(string); ok {
			options.TranscriptionConfig.GoogleCredentials = v
		}
		if v, ok := tc["assemblyAIKey"].(string); ok {
			options.TranscriptionConfig.AssemblyAIKey = v
		}
		if v, ok := tc["assemblyAIWordBoost"].([]interface{}); ok {
			wordBoost := make([]string, 0, len(v))
			for _, wb := range v {
				if str, ok := wb.(string); ok && str != "" {
					wordBoost = append(wordBoost, str)
				}
			}
			options.TranscriptionConfig.AssemblyAIWordBoost = wordBoost
		}
		if v, ok := tc["hallucinationPatterns"].([]interface{}); ok {
			patterns := make([]string, 0, len(v))
			for _, p := range v {
				if str, ok := p.(string); ok && str != "" {
					patterns = append(patterns, str)
				}
			}
			options.TranscriptionConfig.HallucinationPatterns = patterns
		}
		if v, ok := tc["hallucinationDetectionMode"].(string); ok {
			options.TranscriptionConfig.HallucinationDetectionMode = v
		}
		if v, ok := tc["hallucinationMinOccurrences"].(float64); ok {
			options.TranscriptionConfig.HallucinationMinOccurrences = int(v)
		}
	}

	return options
}

func (options *Options) Read(db *Database) error {
	var (
		defaultPassword []byte
		err             error
		f               any
		query           string
		rows            *sql.Rows

		key   sql.NullString
		value sql.NullString
	)

	options.mutex.Lock()
	defer options.mutex.Unlock()

	defaultPassword, _ = bcrypt.GenerateFromPassword([]byte(defaults.adminPassword), bcrypt.DefaultCost)

	options.adminPassword = string(defaultPassword)
	options.adminPasswordNeedChange = defaults.adminPasswordNeedChange
	options.AudioConversion = defaults.options.audioConversion
	options.AudioCodec = defaults.options.audioCodec
	options.AudioBitrate = defaults.options.audioBitrate
	options.AutoPopulate = defaults.options.autoPopulate
	options.Branding = defaults.options.branding
	options.DefaultSystemDelay = defaults.options.defaultSystemDelay
	options.DimmerDelay = defaults.options.dimmerDelay
	options.DisableDuplicateDetection = defaults.options.disableDuplicateDetection
	options.DuplicateDetectionMode = defaults.options.duplicateDetectionMode
	options.DuplicateDetectionTimeFrame = defaults.options.duplicateDetectionTimeFrame
	options.AdvancedDetectionTimeFrame = defaults.options.advancedDetectionTimeFrame
	options.Email = defaults.options.email
	options.KeypadBeeps = defaults.options.keypadBeeps
	options.MaxClients = defaults.options.maxClients
	options.PlaybackGoesLive = defaults.options.playbackGoesLive
	options.PruneDays = defaults.options.pruneDays
	options.ShowListenersCount = defaults.options.showListenersCount
	options.SortTalkgroups = defaults.options.sortTalkgroups
	options.Time12hFormat = defaults.options.time12hFormat
	options.AlertRetentionDays = defaults.options.alertRetentionDays
	options.TranscriptionFailureThreshold = defaults.options.transcriptionFailureThreshold
	options.ToneDetectionIssueThreshold = defaults.options.toneDetectionIssueThreshold
	options.NoAudioThresholdMinutes = defaults.options.noAudioThresholdMinutes
	options.NoAudioMultiplier = defaults.options.noAudioMultiplier
	options.SystemHealthAlertsEnabled = defaults.options.systemHealthAlertsEnabled
	options.TranscriptionFailureAlertsEnabled = defaults.options.transcriptionFailureAlertsEnabled
	options.ToneDetectionAlertsEnabled = defaults.options.toneDetectionAlertsEnabled
	options.NoAudioAlertsEnabled = defaults.options.noAudioAlertsEnabled
	options.TranscriptionFailureTimeWindow = defaults.options.transcriptionFailureTimeWindow
	options.ToneDetectionTimeWindow = defaults.options.toneDetectionTimeWindow
	options.NoAudioTimeWindow = defaults.options.noAudioTimeWindow
	options.NoAudioHistoricalDataDays = defaults.options.noAudioHistoricalDataDays
	options.TranscriptionFailureRepeatMinutes = defaults.options.transcriptionFailureRepeatMinutes
	options.ToneDetectionRepeatMinutes = defaults.options.toneDetectionRepeatMinutes
	options.NoAudioRepeatMinutes = defaults.options.noAudioRepeatMinutes
	options.AdminLocalhostOnly = defaults.options.adminLocalhostOnly
	options.ConfigSyncEnabled = defaults.options.configSyncEnabled
	options.ConfigSyncPath = defaults.options.configSyncPath
	options.ReconnectionEnabled = defaults.options.reconnectionEnabled
	options.ReconnectionGracePeriod = defaults.options.reconnectionGracePeriod
	options.ReconnectionMaxBufferSize = defaults.options.reconnectionMaxBufferSize
	
	// Initialize Radio Reference credentials with defaults, but they will be overridden by database values
	options.RadioReferenceEnabled = defaults.options.radioReferenceEnabled
	options.RadioReferenceUsername = defaults.options.radioReferenceUsername
	options.RadioReferencePassword = defaults.options.radioReferencePassword

	formatError := errorFormatter("options", "read")

	query = `SELECT "key", "value" FROM "options"`
	if rows, err = db.Sql.Query(query); err != nil {
		return formatError(err, query)
	}

	for rows.Next() {
		if err = rows.Scan(&key, &value); err != nil {
			continue
		}

		if !key.Valid || !value.Valid {
			continue
		}

		switch key.String {
		case "adminPassword":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.adminPassword = v
				}
			}
		case "adminPasswordNeedChange":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.adminPasswordNeedChange = v
				}
			}
		case "audioConversion":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.AudioConversion = uint(v)
				}
			}
		case "audioCodec":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.AudioCodec = v
				}
			}
		case "audioBitrate":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.AudioBitrate = uint(v)
				}
			}
		case "autoPopulate":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.AutoPopulate = v
				}
			}
		case "branding":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.Branding = v
				}
			}
		case "defaultSystemDelay":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.DefaultSystemDelay = uint(v)
				}
			}
		case "dimmerDelay":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.DimmerDelay = uint(v)
				}
			}
		case "disableDuplicateDetection":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.DisableDuplicateDetection = v
				}
			}
		case "duplicateDetectionMode":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					// Validate mode is "legacy" or "advanced"
					if v == "legacy" || v == "advanced" {
						options.DuplicateDetectionMode = v
					} else {
						options.DuplicateDetectionMode = defaults.options.duplicateDetectionMode
					}
				}
			}
		case "duplicateDetectionTimeFrame":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.DuplicateDetectionTimeFrame = uint(v)
				}
			}
		case "advancedDetectionTimeFrame":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.AdvancedDetectionTimeFrame = uint(v)
				}
			}
		case "email":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.Email = v
				}
			}
		case "keypadBeeps":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.KeypadBeeps = v
				}
			}
		case "maxClients":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.MaxClients = uint(v)
				}
			}
		case "playbackGoesLive":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.PlaybackGoesLive = v
				}
			}
		case "pruneDays":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.PruneDays = uint(v)
				}
			}
		case "showListenersCount":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.ShowListenersCount = v
				}
			}
		case "sortTalkgroups":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.SortTalkgroups = v
				}
			}
		case "time12hFormat":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.Time12hFormat = v
				}
			}
		case "radioReferenceEnabled":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.RadioReferenceEnabled = v
				}
			}
		case "radioReferenceUsername":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.RadioReferenceUsername = v
				}
			}
		case "radioReferencePassword":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.RadioReferencePassword = v
				}
			}
		case "userRegistrationEnabled":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.UserRegistrationEnabled = v
				}
			}
		case "publicRegistrationEnabled":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.PublicRegistrationEnabled = v
				}
			}
		case "publicRegistrationMode":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.PublicRegistrationMode = v
				}
			}
		case "stripePaywallEnabled":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.StripePaywallEnabled = v
				}
			}
		case "emailServiceEnabled":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.EmailServiceEnabled = v
				}
			}
		case "emailServiceApiKey":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.EmailServiceApiKey = v
				}
			}
		case "emailServiceDomain":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.EmailServiceDomain = v
				}
			}
		case "emailServiceTemplateId":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.EmailServiceTemplateId = v
				}
			}
		case "emailSmtpFromEmail":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.EmailSmtpFromEmail = v
				}
			}
		case "emailSmtpFromName":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.EmailSmtpFromName = v
				}
			}
		case "emailSendGridApiKey":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.EmailSendGridAPIKey = v
				}
			}
		case "emailMailgunApiKey":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.EmailMailgunAPIKey = v
				}
			}
		case "emailMailgunDomain":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.EmailMailgunDomain = v
				}
			}
		case "emailMailgunApiBase":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.EmailMailgunAPIBase = v
				}
			}
		case "emailProvider":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.EmailProvider = v
				}
			}
		case "emailSmtpHost":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.EmailSmtpHost = v
				}
			}
		case "emailSmtpPort":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.EmailSmtpPort = int(v)
				case int:
					options.EmailSmtpPort = v
				}
			}
		case "emailSmtpUsername":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.EmailSmtpUsername = v
				}
			}
		case "emailSmtpPassword":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.EmailSmtpPassword = v
				}
			}
		case "emailSmtpUseTLS":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.EmailSmtpUseTLS = v
				}
			}
		case "emailSmtpSkipVerify":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.EmailSmtpSkipVerify = v
				}
			}
		case "emailLogoFilename":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.EmailLogoFilename = v
				}
			}
		case "emailLogoBorderRadius":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.EmailLogoBorderRadius = v
				}
			}
		case "faviconFilename":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.FaviconFilename = v
				}
			}
		case "stripePublishableKey":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.StripePublishableKey = v
				}
			}
		case "stripeSecretKey":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.StripeSecretKey = v
				}
			}
		case "stripeWebhookSecret":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.StripeWebhookSecret = v
				}
			}
		case "stripePriceId":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.StripePriceId = v
				}
			}
		case "baseUrl":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.BaseUrl = v
				}
			}
		case "transcriptionConfig":
			var cfg TranscriptionConfig
			if err := json.Unmarshal([]byte(value.String), &cfg); err == nil {
				options.TranscriptionConfig = cfg
			}
		case "alertRetentionDays":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.AlertRetentionDays = uint(v)
				}
			}
		case "transcriptionFailureThreshold":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.TranscriptionFailureThreshold = uint(v)
				}
			}
		case "toneDetectionIssueThreshold":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.ToneDetectionIssueThreshold = uint(v)
				}
			}
		case "noAudioThresholdMinutes":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.NoAudioThresholdMinutes = uint(v)
				}
			}
		case "noAudioMultiplier":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.NoAudioMultiplier = v
				}
			}
		case "systemHealthAlertsEnabled":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.SystemHealthAlertsEnabled = v
				}
			}
		case "transcriptionFailureAlertsEnabled":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.TranscriptionFailureAlertsEnabled = v
				}
			}
		case "toneDetectionAlertsEnabled":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.ToneDetectionAlertsEnabled = v
				}
			}
		case "noAudioAlertsEnabled":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.NoAudioAlertsEnabled = v
				}
			}
		case "transcriptionFailureTimeWindow":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.TranscriptionFailureTimeWindow = uint(v)
				}
			}
		case "toneDetectionTimeWindow":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.ToneDetectionTimeWindow = uint(v)
				}
			}
		case "noAudioTimeWindow":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.NoAudioTimeWindow = uint(v)
				}
			}
		case "noAudioHistoricalDataDays":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.NoAudioHistoricalDataDays = uint(v)
				}
			}
		case "transcriptionFailureRepeatMinutes":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.TranscriptionFailureRepeatMinutes = uint(v)
				}
			}
		case "toneDetectionRepeatMinutes":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.ToneDetectionRepeatMinutes = uint(v)
				}
			}
		case "noAudioRepeatMinutes":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.NoAudioRepeatMinutes = uint(v)
				}
			}
		case "relayServerURL":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.RelayServerURL = v
				}
			}
		case "relayServerAPIKey":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.RelayServerAPIKey = v
				}
			}
		case "radioReferenceAPIKey":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.RadioReferenceAPIKey = v
				}
			}
		case "adminLocalhostOnly":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.AdminLocalhostOnly = v
				}
			}
		case "configSyncEnabled":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.ConfigSyncEnabled = v
				}
			}
		case "configSyncPath":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.ConfigSyncPath = v
				}
			}
		case "turnstileEnabled":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.TurnstileEnabled = v
				}
			}
		case "turnstileSiteKey":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.TurnstileSiteKey = v
				}
			}
		case "turnstileSecretKey":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case string:
					options.TurnstileSecretKey = v
				}
			}
		case "reconnectionEnabled":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case bool:
					options.ReconnectionEnabled = v
				}
			}
		case "reconnectionGracePeriod":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.ReconnectionGracePeriod = uint(v)
				}
			}
		case "reconnectionMaxBufferSize":
			if err = json.Unmarshal([]byte(value.String), &f); err == nil {
				switch v := f.(type) {
				case float64:
					options.ReconnectionMaxBufferSize = uint(v)
				}
			}
		}
	}

	// Close the rows to prevent resource leaks
	rows.Close()

	return nil
}

func (options *Options) Write(db *Database) error {
	var (
		err error
		res sql.Result
		tx  *sql.Tx
	)
	options.mutex.Lock()
	defer options.mutex.Unlock()

	formatError := errorFormatter("options", "write")

	set := func(key string, val any) {
		if val, err = json.Marshal(val); err == nil {
			switch v := val.(type) {
			case string:
				val = escapeQuotes(v)
			}

			query := fmt.Sprintf(`UPDATE "options" SET "value" = '%s' WHERE "key" = '%s'`, val, key)
			if res, err = tx.Exec(query); err == nil {
				if i, err := res.RowsAffected(); err == nil && i == 0 {
					query = fmt.Sprintf(`INSERT INTO "options" ("key", "value") VALUES ('%s', '%s')`, key, val)
					if _, err = tx.Exec(query); err != nil {
						log.Println(formatError(err, query))
					}
				}
			}
		}
	}

	if tx, err = db.Sql.Begin(); err != nil {
		return formatError(err, "")
	}

	set("adminPassword", options.adminPassword)
	set("adminPasswordNeedChange", options.adminPasswordNeedChange)
	set("audioConversion", options.AudioConversion)
	set("audioCodec", options.AudioCodec)
	set("audioBitrate", options.AudioBitrate)
	set("autoPopulate", options.AutoPopulate)
	set("branding", options.Branding)
	set("defaultSystemDelay", options.DefaultSystemDelay)
	set("dimmerDelay", options.DimmerDelay)
	set("disableDuplicateDetection", options.DisableDuplicateDetection)
	set("duplicateDetectionMode", options.DuplicateDetectionMode)
	set("duplicateDetectionTimeFrame", options.DuplicateDetectionTimeFrame)
	set("advancedDetectionTimeFrame", options.AdvancedDetectionTimeFrame)
	set("email", options.Email)
	set("keypadBeeps", options.KeypadBeeps)
	set("maxClients", options.MaxClients)
	set("playbackGoesLive", options.PlaybackGoesLive)
	set("pruneDays", options.PruneDays)
	set("secret", options.secret)
	set("showListenersCount", options.ShowListenersCount)
	set("sortTalkgroups", options.SortTalkgroups)
	set("time12hFormat", options.Time12hFormat)
	set("radioReferenceEnabled", options.RadioReferenceEnabled)
	set("radioReferenceUsername", options.RadioReferenceUsername)
	set("radioReferencePassword", options.RadioReferencePassword)
	set("userRegistrationEnabled", options.UserRegistrationEnabled)
	set("publicRegistrationEnabled", options.PublicRegistrationEnabled)
	set("publicRegistrationMode", options.PublicRegistrationMode)
	set("stripePaywallEnabled", options.StripePaywallEnabled)
	set("emailServiceEnabled", options.EmailServiceEnabled)
	set("emailServiceApiKey", options.EmailServiceApiKey)
	set("emailServiceDomain", options.EmailServiceDomain)
	set("emailServiceTemplateId", options.EmailServiceTemplateId)
	set("emailProvider", options.EmailProvider)
	set("emailSmtpFromEmail", options.EmailSmtpFromEmail)
	set("emailSmtpFromName", options.EmailSmtpFromName)
	set("emailSendGridApiKey", options.EmailSendGridAPIKey)
	set("emailMailgunApiKey", options.EmailMailgunAPIKey)
	set("emailMailgunDomain", options.EmailMailgunDomain)
	set("emailMailgunApiBase", options.EmailMailgunAPIBase)
	set("emailSmtpHost", options.EmailSmtpHost)
	set("emailSmtpPort", options.EmailSmtpPort)
	set("emailSmtpUsername", options.EmailSmtpUsername)
	set("emailSmtpPassword", options.EmailSmtpPassword)
	set("emailSmtpUseTLS", options.EmailSmtpUseTLS)
	set("emailSmtpSkipVerify", options.EmailSmtpSkipVerify)
	set("emailLogoFilename", options.EmailLogoFilename)
	set("emailLogoBorderRadius", options.EmailLogoBorderRadius)
	set("faviconFilename", options.FaviconFilename)
	set("stripePublishableKey", options.StripePublishableKey)
	set("stripeSecretKey", options.StripeSecretKey)
	set("stripeWebhookSecret", options.StripeWebhookSecret)
	set("stripeGracePeriodDays", options.StripeGracePeriodDays)
	set("stripePriceId", options.StripePriceId)
	set("baseUrl", options.BaseUrl)
	set("alertRetentionDays", options.AlertRetentionDays)
	set("transcriptionFailureThreshold", options.TranscriptionFailureThreshold)
	set("toneDetectionIssueThreshold", options.ToneDetectionIssueThreshold)
	set("noAudioThresholdMinutes", options.NoAudioThresholdMinutes)
	set("noAudioMultiplier", options.NoAudioMultiplier)
	set("systemHealthAlertsEnabled", options.SystemHealthAlertsEnabled)
	set("transcriptionFailureAlertsEnabled", options.TranscriptionFailureAlertsEnabled)
	set("toneDetectionAlertsEnabled", options.ToneDetectionAlertsEnabled)
	set("noAudioAlertsEnabled", options.NoAudioAlertsEnabled)
	set("transcriptionFailureTimeWindow", options.TranscriptionFailureTimeWindow)
	set("toneDetectionTimeWindow", options.ToneDetectionTimeWindow)
	set("noAudioTimeWindow", options.NoAudioTimeWindow)
	set("noAudioHistoricalDataDays", options.NoAudioHistoricalDataDays)
	set("transcriptionFailureRepeatMinutes", options.TranscriptionFailureRepeatMinutes)
	set("toneDetectionRepeatMinutes", options.ToneDetectionRepeatMinutes)
	set("noAudioRepeatMinutes", options.NoAudioRepeatMinutes)
	set("relayServerURL", options.RelayServerURL)
	set("relayServerAPIKey", options.RelayServerAPIKey)
	set("radioReferenceAPIKey", options.RadioReferenceAPIKey)
	set("adminLocalhostOnly", options.AdminLocalhostOnly)
	set("configSyncEnabled", options.ConfigSyncEnabled)
	set("configSyncPath", options.ConfigSyncPath)
	set("turnstileEnabled", options.TurnstileEnabled)
	set("turnstileSiteKey", options.TurnstileSiteKey)
	set("turnstileSecretKey", options.TurnstileSecretKey)
	set("reconnectionEnabled", options.ReconnectionEnabled)
	set("reconnectionGracePeriod", options.ReconnectionGracePeriod)
	set("reconnectionMaxBufferSize", options.ReconnectionMaxBufferSize)
	// Persist entire transcription config as a single JSON blob
	set("transcriptionConfig", options.TranscriptionConfig)

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	return nil
}
