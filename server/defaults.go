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

type Defaults struct {
	adminPassword           string
	adminPasswordNeedChange bool
	apikey                  DefaultApikey
	dirwatch                DefaultDirwatch
	downstream              DefaultDownstream
	groups                  []string
	keypadBeeps             string
	options                 DefaultOptions
	systems                 []System
	tags                    []string
}

type DefaultApikey struct {
	ident   string
	systems string
}

type DefaultDirwatch struct {
	delay       uint
	deleteAfter bool
	disabled    bool
	kind        string
}

type DefaultDownstream struct {
	systems string
}

type DefaultOptions struct {
	autoPopulate                bool
	audioConversion             uint
	audioCodec                  string
	audioBitrate                uint
	branding                    string
	defaultSystemDelay          uint
	dimmerDelay                 uint
	disableDuplicateDetection   bool
	duplicateDetectionMode      string
	duplicateDetectionTimeFrame uint
	advancedDetectionTimeFrame  uint
	email                       string
	keypadBeeps                 string
	maxClients                  uint
	playbackGoesLive            bool
	pruneDays                   uint
	showListenersCount          bool
	sortTalkgroups              bool
	time12hFormat               bool
	radioReferenceEnabled       bool
	radioReferenceUsername      string
	radioReferencePassword      string
	userRegistrationEnabled     bool
	publicRegistrationEnabled   bool
	publicRegistrationMode      string
	stripePaywallEnabled        bool
	emailServiceEnabled         bool
	emailServiceApiKey          string
	emailServiceDomain          string
	emailServiceTemplateId      string
	emailProvider               string
	emailSendGridAPIKey         string
	emailMailgunAPIKey          string
	emailMailgunDomain          string
	emailMailgunAPIBase         string
	emailSmtpHost               string
	emailSmtpPort               int
	emailSmtpUsername           string
	emailSmtpPassword           string
	emailSmtpUseTLS             bool
	emailSmtpSkipVerify         bool
	emailSmtpFromEmail          string
	emailSmtpFromName           string
	emailLogoFilename           string
	emailLogoBorderRadius       string
	faviconFilename             string
	stripePublishableKey        string
	stripeSecretKey             string
	stripeWebhookSecret         string
	stripeGracePeriodDays        uint
	stripePriceId               string
	baseUrl                     string
	transcriptionConfig         DefaultTranscriptionConfig
	transcriptionFailureThreshold uint
	toneDetectionIssueThreshold uint
	alertRetentionDays          uint
	noAudioThresholdMinutes     uint
	noAudioMultiplier            float64
	systemHealthAlertsEnabled   bool
	transcriptionFailureAlertsEnabled bool
	toneDetectionAlertsEnabled  bool
	noAudioAlertsEnabled        bool
	transcriptionFailureTimeWindow uint
	toneDetectionTimeWindow     uint
	noAudioTimeWindow           uint
	noAudioHistoricalDataDays   uint
	transcriptionFailureRepeatMinutes uint
	toneDetectionRepeatMinutes        uint
	noAudioRepeatMinutes              uint
	adminLocalhostOnly          bool
	configSyncEnabled           bool
	configSyncPath              string
	reconnectionEnabled         bool
	reconnectionGracePeriod     uint
	reconnectionMaxBufferSize   uint
}

type DefaultTranscriptionConfig struct {
	enabled          bool
	provider         string
	whisperAPIURL    string
	whisperAPIKey    string
	azureKey         string
	azureRegion      string
	googleAPIKey     string
	googleCredentials string
	assemblyAIKey    string
	language         string
	prompt           string
	workerPoolSize   int
}

var defaults = Defaults{
	adminPassword:           "admin",
	adminPasswordNeedChange: true,
	apikey: DefaultApikey{
		ident:   "admin",
		systems: "*",
	},
	dirwatch: DefaultDirwatch{
		delay:       2000,
		deleteAfter: false,
		disabled:    false,
		kind:        "default",
	},
	downstream: DefaultDownstream{
		systems: "*",
	},
	groups: []string{
		"Police",
		"Fire",
		"EMS",
		"Public Works",
		"Schools",
		"Business",
		"Other",
	},
	keypadBeeps: "uniden",
	options: DefaultOptions{
		autoPopulate:                true,
		audioConversion:             0,
		audioCodec:                  "opus",
		audioBitrate:                24,
		branding:                    "",
		defaultSystemDelay:          0,
		dimmerDelay:                 30000,
		disableDuplicateDetection:   false,
		duplicateDetectionMode:      "legacy",
		duplicateDetectionTimeFrame: 1000,
		advancedDetectionTimeFrame:  1000,
		email:                       "",
		keypadBeeps:                 "uniden",
		maxClients:                  100,
		playbackGoesLive:            false,
		pruneDays:                   0,
		showListenersCount:          true,
		sortTalkgroups:              false,
		time12hFormat:               false,
		radioReferenceEnabled:       false,
		radioReferenceUsername:      "",
		radioReferencePassword:      "",
		userRegistrationEnabled:     true,
		publicRegistrationEnabled:   false, // Default to invite-only
		publicRegistrationMode:      "both",
		stripePaywallEnabled:        false,
		emailServiceEnabled:         false,
		emailServiceApiKey:          "",
		emailServiceDomain:          "",
		emailServiceTemplateId:      "",
		emailProvider:               "sendgrid",
		emailSendGridAPIKey:         "",
		emailMailgunAPIKey:          "",
		emailMailgunDomain:          "",
		emailMailgunAPIBase:         "https://api.mailgun.net",
		emailSmtpHost:               "",
		emailSmtpPort:               587,
		emailSmtpUsername:           "",
		emailSmtpPassword:           "",
		emailSmtpUseTLS:             true,
		emailSmtpSkipVerify:         false,
		emailSmtpFromEmail:          "",
		emailSmtpFromName:           "",
		emailLogoFilename:           "",
		emailLogoBorderRadius:       "0px",
		faviconFilename:             "",
		stripePublishableKey:        "",
		stripeSecretKey:             "",
		stripeWebhookSecret:         "",
		stripeGracePeriodDays:       0,
		stripePriceId:               "",
		baseUrl:                     "",
		transcriptionConfig: DefaultTranscriptionConfig{
			enabled:        false,
			provider:       "whisper-api", // Default to external Whisper API server
			whisperAPIURL:  "http://localhost:8000",
			whisperAPIKey:  "",
			azureKey:       "",
			azureRegion:    "eastus",
			googleAPIKey:   "",
			googleCredentials: "",
			assemblyAIKey:  "",
			language:       "en",       // English by default
			prompt:         "",         // No default prompt
			workerPoolSize: 3,          // Conservative default
		},
		transcriptionFailureThreshold: 10,
		toneDetectionIssueThreshold: 5,
		alertRetentionDays: 5,
	noAudioThresholdMinutes: 30,
	noAudioMultiplier: 1.5,
	systemHealthAlertsEnabled: true,
	transcriptionFailureAlertsEnabled: true,
	toneDetectionAlertsEnabled: true,
	noAudioAlertsEnabled: true,
	transcriptionFailureTimeWindow: 24,
	toneDetectionTimeWindow: 24,
		noAudioTimeWindow: 24,
		noAudioHistoricalDataDays: 7,
		transcriptionFailureRepeatMinutes: 60,
		toneDetectionRepeatMinutes: 60,
		noAudioRepeatMinutes: 30,
		adminLocalhostOnly: false, // Default to false for backwards compatibility
		configSyncEnabled:  false,
		configSyncPath:     "",
		reconnectionEnabled: true,       // Enable by default
		reconnectionGracePeriod: 60,     // 60 seconds
		reconnectionMaxBufferSize: 100,  // 100 calls max
	},
	systems: []System{
		{
			Id:           1,
			Label:        "Default System",
		SystemRef:    1,
		AutoPopulate: true,
		Blacklists:   "",
		Delay:        0,
		Order:        1,
		Kind:         "",
	},
	},
	tags: []string{
		"Emergency",
		"Non-Emergency",
		"Administrative",
		"Training",
		"Maintenance",
		"Other",
	},
}
