// Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
// Modified by Thinline Dynamic Solutions
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
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Controller struct {
	Admin                  *Admin
	Api                    *Api
	Apikeys                *Apikeys
	Calls                  *Calls
	CallQueue              *CallQueue
	Clients                *Clients
	Config                 *Config
	Database               *Database
	Delayer                *Delayer
	Dirwatches             *Dirwatches
	Downstreams            *Downstreams
	FFMpeg                 *FFMpeg
	Groups                 *Groups
	Logs                   *Logs
	Options                *Options
	ReconnectionMgr        *ReconnectionManager
	Scheduler              *Scheduler
	Systems                *Systems
	Tags                   *Tags
	Users                  *Users
	UserGroups             *UserGroups
	RegistrationCodes      *RegistrationCodes
	TransferRequests       *TransferRequests
	DeviceTokens           *DeviceTokens
	EmailService           *EmailService
	ToneDetector           *ToneDetector
	TranscriptionQueue     *TranscriptionQueue
	KeywordMatcher         *KeywordMatcher
	AlertEngine            *AlertEngine
	HallucinationDetector  *HallucinationDetector
	CentralManagement      *CentralManagementService
	Register              chan *Client
	Unregister            chan *Client
	Ingest                chan *Call
	running               bool
	workerCancel          context.CancelFunc // Function to cancel worker context
	workersWg             sync.WaitGroup     // WaitGroup to track worker goroutines
	workerStats           struct {
		sync.Mutex
		activeWorkers  int
		totalCalls     int64
		avgProcessTime time.Duration
	}
	// Pending tone sequences per talkgroup (for associating tones with subsequent voice calls)
	// Tones detected on tone-only calls are stored here and attached to the first subsequent voice call
	pendingTones      map[string]*PendingToneSequence // Key: "systemId:talkgroupId"
	pendingTonesMutex sync.Mutex

	// Waiting short calls per talkgroup (for waiting 15 seconds to see if a longer voice call arrives)
	// Short transcripts that don't meet minimum requirements are stored here with a timer
	// If a longer call arrives within 15 seconds, attach to that. Otherwise, attach to the short call.
	waitingShortCalls      map[string]*WaitingShortCall // Key: "systemId:talkgroupId"
	waitingShortCallsMutex sync.Mutex

	// Per-user mutexes to serialize authentication and prevent race conditions
	authMutexes      map[uint64]*sync.Mutex // Key: user ID
	authMutexesMutex sync.Mutex

	// Rate limiting
	RateLimiter         *RateLimiter
	LoginAttemptTracker *LoginAttemptTracker

	// Debug logging for tones/keywords
	DebugLogger              *DebugLogger
	TranscriptionDebugLogger *TranscriptionDebugLogger
}

// WaitingShortCall represents a short voice call that is waiting for a longer one to arrive
type WaitingShortCall struct {
	Call         *Call
	PendingTones *PendingToneSequence
	Timer        *time.Timer
	CancelChan   chan bool
}

const (
	// pendingToneTimeoutMinutes is how long to keep pending tones before expiring
	// If tones don't get attached to a voice call within this time, they're considered orphaned
	// Set to 2 minutes to prevent unrelated incidents from merging together
	pendingToneTimeoutMinutes = 2
	// shortCallWaitSeconds is how long to wait for a longer voice call before attaching to a short one
	shortCallWaitSeconds = 15
)

func NewController(config *Config) *Controller {
	controller := &Controller{
		Clients:           NewClients(),
		Config:            config,
		Apikeys:           NewApikeys(),
		Dirwatches:        NewDirwatches(),
		FFMpeg:            NewFFMpeg(),
		Groups:            NewGroups(),
		Logs:              NewLogs(),
		Options:           NewOptions(),
		Systems:           NewSystems(),
		Tags:              NewTags(),
		Register:          make(chan *Client, 8192),
		Unregister:        make(chan *Client, 8192),
		Ingest:            make(chan *Call, 8192),
		pendingTones:      make(map[string]*PendingToneSequence),
		waitingShortCalls: make(map[string]*WaitingShortCall),
		authMutexes:       make(map[uint64]*sync.Mutex),
	}

	controller.Admin = NewAdmin(controller)
	controller.Api = NewApi(controller)
	controller.Calls = NewCalls(controller)
	controller.CallQueue = NewCallQueue()
	controller.Database = NewDatabase(config)
	controller.Users = NewUsers()
	controller.UserGroups = NewUserGroups()
	controller.RegistrationCodes = NewRegistrationCodes()
	controller.TransferRequests = NewTransferRequests()
	controller.DeviceTokens = NewDeviceTokens()
	controller.EmailService = NewEmailService(controller)
	controller.CentralManagement = NewCentralManagementService(controller)
	controller.Delayer = NewDelayer(controller)
	controller.Downstreams = NewDownstreams(controller)
	controller.Scheduler = NewScheduler(controller)

	controller.Logs.setDaemon(config.daemon)
	controller.Logs.setDatabase(controller.Database)

	// Initialize debug logger for tones/keywords if enabled in config
	if config.EnableDebugLog {
		debugLogger, err := NewDebugLogger("tone-keyword-debug.log")
		if err != nil {
			log.Printf("Warning: Failed to create debug logger: %v", err)
		} else {
			controller.DebugLogger = debugLogger
			log.Println("Tone & Keyword debug logging enabled - writing to tone-keyword-debug.log")
		}

		// Also initialize transcription debug logger
		transcriptionDebugLogger, err := NewTranscriptionDebugLogger("transcription-tone-debug.log")
		if err != nil {
			log.Printf("Warning: Failed to create transcription debug logger: %v", err)
		} else {
			controller.TranscriptionDebugLogger = transcriptionDebugLogger
			log.Println("Transcription tone removal debug logging enabled - writing to transcription-tone-debug.log")
		}
	}

	// Initialize tone detection and transcription components
	controller.ToneDetector = NewToneDetector()
	controller.KeywordMatcher = NewKeywordMatcher()
	controller.AlertEngine = NewAlertEngine(controller)
	controller.HallucinationDetector = NewHallucinationDetector(controller)

	// Initialize rate limiting
	// General rate limiter: 1000 requests per minute per IP
	controller.RateLimiter = NewRateLimiter(1000, 1*time.Minute)
	// Login attempt tracker: 6 failed attempts = 15 minute block
	controller.LoginAttemptTracker = NewLoginAttemptTracker(6, 15*time.Minute)

	// Initialize reconnection manager with default settings
	// Will be reconfigured with actual settings from Options after Options.Read()
	controller.ReconnectionMgr = NewReconnectionManager(controller, 60*time.Second, 100, true)

	// Initialize transcription queue (if transcription is enabled in options)
	// This will be initialized after Options.Read() in Start()

	return controller
}

func (controller *Controller) EmitCall(call *Call) {
	// If call is already marked as delayed (system-wide delay),
	// it's already been processed - just emit it
	if call.Delayed {
		go controller.Downstreams.Send(controller, call)
		go controller.Clients.EmitCall(controller, call)
		return
	}

	// Always send to downstreams immediately (downstreams should never be delayed)
	go controller.Downstreams.Send(controller, call)

	// Send to clients - Clients.EmitCall will handle per-client delays
	go controller.Clients.EmitCall(controller, call)
}

// EmitCallToClient sends a call to a specific client with their individual delay settings
func (controller *Controller) EmitCallToClient(call *Call, client *Client) {
	msg := &Message{Command: MessageCommandCall, Payload: call}

	// Prevent infinite recursion - don't check delay for already delayed calls
	if call.Delayed {
		// Non-blocking send
		select {
		case client.Send <- msg:
		default:
			// Skip if channel full
		}
		return
	}

	// Check if this specific client should delay this call
	if controller.Delayer.CanDelayForClient(call, client) {
		controller.Delayer.DelayForClient(call, client)
	} else {
		// Non-blocking send
		select {
		case client.Send <- msg:
		default:
			// Skip if channel full
		}
	}
}

func (controller *Controller) EmitConfig() {
	go controller.Clients.EmitConfig(controller)
	go controller.Admin.BroadcastConfig()
}

func (controller *Controller) IngestCall(call *Call) {
	var (
		err         error
		group       *Group
		groupId     uint64
		groupLabel  string
		ok          bool
		populated   bool
		system      *System
		systemId    uint
		tag         *Tag
		tagId       uint64
		tagLabel    string
		talkgroup   *Talkgroup
		talkgroupId uint
	)

	logCall := func(call *Call, level string, message string) {
		var systemRef interface{} = "nil"
		var talkgroupIdForLog uint = 0
		if system != nil {
			systemRef = system.SystemRef
		}

		// Use the talkgroup REF (the radio system's TGID) - NOT the database ID
		if call.Talkgroup != nil {
			talkgroupIdForLog = call.Talkgroup.TalkgroupRef
		} else if talkgroup != nil {
			talkgroupIdForLog = talkgroup.TalkgroupRef
		}

		// Log basic info (keep existing format for compatibility)
		controller.Logs.LogEvent(level, fmt.Sprintf("newcall: system=%v talkgroup=%v file=%v %v", systemRef, talkgroupIdForLog, call.AudioFilename, message))
	}

	logError := func(err error) {
		controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("controller.ingestcall: %v", err.Error()))
	}

	// Get system ID from call (v6 style - simple uint)
	if call.SystemId > 0 {
		systemId = call.SystemId
	} else if call.Meta.SystemRef > 0 {
		systemId = call.Meta.SystemRef
	} else if call.System != nil {
		systemId = uint(call.System.SystemRef)
	}

	// Get talkgroup ID from call (v6 style - simple uint)
	if call.TalkgroupId > 0 {
		talkgroupId = call.TalkgroupId
	} else if call.Meta.TalkgroupRef > 0 {
		talkgroupId = call.Meta.TalkgroupRef
	} else if call.Talkgroup != nil {
		talkgroupId = call.Talkgroup.TalkgroupRef
	}

	// Lookup system (v6 style - by ID/Ref)
	if systemId > 0 {
		// Try by Ref first (v6 used Id which maps to Ref in v7)
		if system, ok = controller.Systems.GetSystemByRef(systemId); !ok {
			// Fallback to by Id
			system, _ = controller.Systems.GetSystemById(uint64(systemId))
		}
	}

	if system != nil {
		if system.Blacklists.IsBlacklisted(talkgroupId) {
			logCall(call, LogLevelInfo, "blacklisted")
			return
		}
		if talkgroupId > 0 {
			talkgroup, _ = system.Talkgroups.GetTalkgroupByRef(talkgroupId)
		}

		// P25 Patch Handling (Early Check): If the main talkgroup doesn't exist but we have patches,
		// check if any patched talkgroup exists. This helps with auto-populate and blacklist checks.
		if talkgroup == nil && len(call.Patches) > 0 {
			for _, patchedTgId := range call.Patches {
				if patchedTgId == 0 {
					continue
				}

				// Check blacklist for patched talkgroups too
				if system.Blacklists.IsBlacklisted(patchedTgId) {
					logCall(call, LogLevelInfo, "blacklisted (patched talkgroup)")
					return
				}

				if patchedTalkgroup, ok := system.Talkgroups.GetTalkgroupByRef(patchedTgId); ok {
					// Found a valid patched talkgroup - use it
					originalTalkgroupId := talkgroupId
					talkgroup = patchedTalkgroup
					talkgroupId = patchedTgId

					// Add original patch TGID to patches if not already there
					if originalTalkgroupId > 0 && originalTalkgroupId != patchedTgId {
						alreadyInPatches := false
						for _, existingPatch := range call.Patches {
							if existingPatch == originalTalkgroupId {
								alreadyInPatches = true
								break
							}
						}
						if !alreadyInPatches {
							call.Patches = append(call.Patches, originalTalkgroupId)
						}
					}

					// Update call references
					call.TalkgroupId = talkgroupId
					call.Meta.TalkgroupRef = talkgroupId

					break // Use first valid patched talkgroup
				}
			}
		}
	}

	if controller.Options.AutoPopulate && system == nil && systemId > 0 {
		populated = true

		system = NewSystem()
		// V6 style: directly assign the ID
		system.Id = uint64(systemId)
		system.SystemRef = systemId
		system.AutoPopulate = true

		if len(call.Meta.SystemLabel) > 0 {
			system.Label = call.Meta.SystemLabel
		} else {
			system.Label = fmt.Sprintf("System %v", systemId)
		}

		controller.Systems.List = append(controller.Systems.List, system)
	}

	if controller.Options.AutoPopulate || (system != nil && system.AutoPopulate) {
		if system != nil && talkgroup == nil && talkgroupId > 0 {
			populated = true

			groupLabels := []string{"Unknown"}
			if len(call.Meta.TalkgroupGroups) > 0 {
				groupLabels = call.Meta.TalkgroupGroups
			}
			groupLabel = groupLabels[0]

			tagLabel = "Untagged"
			if len(call.Meta.TalkgroupTag) > 0 {
				tagLabel = call.Meta.TalkgroupTag
			}

			if group, ok = controller.Groups.GetGroupByLabel(groupLabel); !ok {
				group = &Group{Label: groupLabel}

				controller.Groups.List = append(controller.Groups.List, group)

				if err = controller.Groups.Write(controller.Database); err != nil {
					logError(err)
					return
				}

				if err = controller.Groups.Read(controller.Database); err != nil {
					logError(err)
					return
				}

				// Sync config to file if enabled
				controller.SyncConfigToFile()

				if group, ok = controller.Groups.GetGroupByLabel(groupLabel); !ok {
					logError(fmt.Errorf("unable to get group %s", groupLabel))
					return
				}
			}

			groupId = group.Id

			if tag, ok = controller.Tags.GetTagByLabel(tagLabel); !ok {
				tag = &Tag{Label: tagLabel}

				controller.Tags.List = append(controller.Tags.List, tag)

				if err = controller.Tags.Write(controller.Database); err != nil {
					logError(err)
					return
				}

				if err = controller.Tags.Read(controller.Database); err != nil {
					logError(err)
					return
				}

				// Sync config to file if enabled
				controller.SyncConfigToFile()

				if tag, ok = controller.Tags.GetTagByLabel(tagLabel); !ok {
					logError(fmt.Errorf("unable to get tag %s", tagLabel))
					return
				}
			}

			tagId = tag.Id

			// Find the max Order value among existing talkgroups to assign new talkgroup at the end
			maxOrder := uint(0)
			for _, existingTg := range system.Talkgroups.List {
				if existingTg.Order > maxOrder {
					maxOrder = existingTg.Order
				}
			}

			talkgroup = &Talkgroup{
				GroupIds:     []uint64{groupId},
				Label:        fmt.Sprintf("%d", talkgroupId),
				Name:         fmt.Sprintf("%d", talkgroupId),
				TalkgroupRef: talkgroupId,
				TagId:        tagId,
				Order:        maxOrder + 1, // Assign order at the end of the list
			}

			// Update label and name if provided (v6 style)
			if len(call.Meta.TalkgroupLabel) > 0 && talkgroup.Label != call.Meta.TalkgroupLabel {
				populated = true
				talkgroup.Label = call.Meta.TalkgroupLabel
			}

			// Set Name: use TalkgroupName if available, otherwise fallback to Label
			// This fixes SDR Trunk uploads that only send label/tgid but no name
			if len(call.Meta.TalkgroupName) > 0 {
				populated = true
				talkgroup.Name = call.Meta.TalkgroupName
			} else {
				// When TalkgroupName is not available, use Label as fallback
				// instead of leaving it as the talkgroup ID number
				populated = true
				talkgroup.Name = talkgroup.Label
			}

			system.Talkgroups.List = append(system.Talkgroups.List, talkgroup)
		}

		// Handle units (v6 style)
		// Always populate Units from Meta.UnitRefs if Units is empty
		// This ensures source information is available when emitting calls
		// Include all unitRefs, even 0, to match v6 behavior
		if len(call.Units) == 0 && len(call.Meta.UnitRefs) > 0 {
			for _, unitRef := range call.Meta.UnitRefs {
				call.Units = append(call.Units, CallUnit{
					UnitRef: unitRef,
					Offset:  0,
				})
			}
		}

		if system != nil {
			units := NewUnits()
			if len(call.Meta.UnitRefs) > 0 {
				for i, unitRef := range call.Meta.UnitRefs {
					if len(call.Meta.UnitLabels)-1 >= i {
						if len(call.Meta.UnitLabels[i]) > 0 {
							units.Add(unitRef, call.Meta.UnitLabels[i])
						}
					}
				}
			}
			if ok := system.Units.Merge(units); ok {
				populated = true
			}
		}
	}

	if populated {
		if err = controller.Systems.Write(controller.Database); err != nil {
			logError(err)
			return
		}

		if err = controller.Systems.Read(controller.Database); err != nil {
			logError(err)
			return
		}

		// Sync config to file if enabled
		controller.SyncConfigToFile()

		// Re-lookup system and talkgroup after read (v6 style - simple lookup)
		if systemId > 0 {
			if system, ok = controller.Systems.GetSystemByRef(systemId); !ok {
				if system, ok = controller.Systems.GetSystemById(uint64(systemId)); !ok {
					system = nil
				}
			}
		}

		if system != nil && talkgroupId > 0 {
			talkgroup, _ = system.Talkgroups.GetTalkgroupByRef(talkgroupId)
		}

		// P25 Patch Handling (After Re-lookup): Check patches again after auto-populate
		if system != nil && talkgroup == nil && len(call.Patches) > 0 {
			originalTalkgroupId := talkgroupId
			for _, patchedTgId := range call.Patches {
				if patchedTgId == 0 {
					continue
				}
				if patchedTalkgroup, ok := system.Talkgroups.GetTalkgroupByRef(patchedTgId); ok {
					talkgroup = patchedTalkgroup
					talkgroupId = patchedTgId

					if originalTalkgroupId > 0 && originalTalkgroupId != patchedTgId {
						alreadyInPatches := false
						for _, existingPatch := range call.Patches {
							if existingPatch == originalTalkgroupId {
								alreadyInPatches = true
								break
							}
						}
						if !alreadyInPatches {
							call.Patches = append(call.Patches, originalTalkgroupId)
						}
					}

					call.TalkgroupId = talkgroupId
					call.Meta.TalkgroupRef = talkgroupId

					break
				}
			}
		}

		// Emit config asynchronously to avoid blocking worker
		go controller.EmitConfig()
	}

	// Set call.System and call.Talkgroup for compatibility
	call.System = system
	call.Talkgroup = talkgroup

	// P25 Patch Handling: If the main talkgroup doesn't exist but we have patches,
	// check if any patched talkgroup exists and use it as the primary talkgroup.
	// This handles Harris P25 Phase II simulcast patches (64501-64599) where the
	// patch TGID is temporary but the patched talkgroups are the actual configured TGs.
	if system != nil && talkgroup == nil && len(call.Patches) > 0 {
		originalTalkgroupId := talkgroupId

		// Try each patched talkgroup to find one that exists in the system
		for _, patchedTgId := range call.Patches {
			if patchedTgId == 0 {
				continue // Skip zero/invalid TGIDs
			}

			if patchedTalkgroup, ok := system.Talkgroups.GetTalkgroupByRef(patchedTgId); ok {
				// Found a valid patched talkgroup - use it as the primary
				talkgroup = patchedTalkgroup
				talkgroupId = patchedTgId

				// Add the original patch TGID to the patches array if it's not already there
				// This preserves it for display and search purposes
				if originalTalkgroupId > 0 && originalTalkgroupId != patchedTgId {
					// Check if it's not already in the patches array
					alreadyInPatches := false
					for _, existingPatch := range call.Patches {
						if existingPatch == originalTalkgroupId {
							alreadyInPatches = true
							break
						}
					}
					if !alreadyInPatches {
						call.Patches = append(call.Patches, originalTalkgroupId)
					}
				}

				// Update the call's talkgroup references
				call.Talkgroup = talkgroup
				call.TalkgroupId = talkgroupId
				call.Meta.TalkgroupRef = talkgroupId

				controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("newcall: system=%v patch=%v resolved to talkgroup=%v file=%v",
					system.SystemRef, originalTalkgroupId, talkgroupId, call.AudioFilename))

				break // Use the first valid patched talkgroup found
			}
		}
	}

	if system == nil || talkgroup == nil {
		logCall(call, LogLevelWarn, "no matching system/talkgroup")
		return
	}

	// Verify system.Id is set (required for talkgroup lookup)
	if system.Id == 0 {
		logCall(call, LogLevelInfo, "dropped - incomplete data (system ID is 0)")
		return
	}

	// CRITICAL: Ensure talkgroup.Id is set before WriteCall (WriteCall uses it on line 641)
	// If Id is still 0, query the database to get it
	if call.Talkgroup != nil && call.Talkgroup.Id == 0 && call.Talkgroup.TalkgroupRef > 0 && system != nil && system.Id > 0 {
		var dbTalkgroupId uint64
		query := fmt.Sprintf(`SELECT "talkgroupId" FROM "talkgroups" WHERE "systemId" = %d AND "talkgroupRef" = %d LIMIT 1`, system.Id, call.Talkgroup.TalkgroupRef)
		if err := controller.Database.Sql.QueryRow(query).Scan(&dbTalkgroupId); err == nil && dbTalkgroupId > 0 {
			call.Talkgroup.Id = dbTalkgroupId
			talkgroup.Id = dbTalkgroupId
		}
	}

	// Final safety check - if talkgroup.Id is still 0, drop the call as incomplete data
	if call.Talkgroup != nil && call.Talkgroup.Id == 0 {
		logCall(call, LogLevelInfo, "dropped - incomplete data (talkgroup ID is 0)")
		return
	}

	// Determine site by frequency if not already set
	if call.SiteRef == "" && system != nil && system.Sites != nil && call.Frequency > 0 {
		if site, ok := system.Sites.GetSiteByFrequency(call.Frequency); ok {
			call.SiteRef = site.SiteRef
		}
	}

	if !controller.Options.DisableDuplicateDetection {
		var isDuplicate bool
		var err error
		var useAdvanced bool

		// Determine if we can use Advanced mode
		// Requires: Advanced mode enabled AND system has proper site configuration
		if controller.Options.DuplicateDetectionMode == "advanced" {
			// Check if system has required configuration for advanced mode
			if call.System != nil && call.System.Sites != nil && len(call.System.Sites.List) > 0 {
				// Check if at least one site is marked as preferred
				hasPreferredSite := false
				for _, site := range call.System.Sites.List {
					if site.Preferred {
						hasPreferredSite = true
						break
					}
				}

				// Only use advanced mode if system has sites with at least one preferred
				// Otherwise fall back to legacy mode for this call
				if hasPreferredSite {
					useAdvanced = true
				}
			}
		}

		if useAdvanced {
			// Check if talkgroup excludes preferred site detection
			// This is useful for interop/patched talkgroups that may receive calls from multiple physical systems
			if call.Talkgroup != nil && call.Talkgroup.ExcludeFromPreferredSite {
				useAdvanced = false
				logCall(call, LogLevelInfo, "talkgroup excluded from preferred site detection, using legacy mode")
			}
		}

		if useAdvanced {
			// Advanced mode with queue-based waiting for preferred sites and/or API keys

			// Determine if incoming call is from preferred site
			isPreferredSite := false
			if call.System.Sites != nil && call.SiteRef != "" {
				if site, ok := call.System.Sites.GetSiteByRef(call.SiteRef); ok {
					isPreferredSite = site.Preferred
				}
			}

			// Determine if incoming call is from preferred API key
			// Talkgroup-level preferred API key takes priority over system-level
			isPreferredApiKey := false
			if call.ApiKeyId != nil {
				if call.Talkgroup != nil && call.Talkgroup.PreferredApiKeyId != nil {
					isPreferredApiKey = (*call.ApiKeyId == *call.Talkgroup.PreferredApiKeyId)
				} else if call.System != nil && call.System.PreferredApiKeyId != nil {
					isPreferredApiKey = (*call.ApiKeyId == *call.System.PreferredApiKeyId)
				}
			}

			// Call is "preferred" if it matches EITHER preferred site OR preferred API key
			isPreferred := isPreferredSite || isPreferredApiKey

			// Check for existing calls in database
			var reason string
			isDuplicate, reason, err = controller.Calls.CheckDuplicateBySiteAndFrequency(
				call,
				controller.Options.AdvancedDetectionTimeFrame,
				controller.Database,
			)

			if err != nil {
				logError(fmt.Errorf("advanced duplicate detection failed, using legacy: %w", err))
				useAdvanced = false // Fall back to legacy
			} else if isDuplicate {
				logCall(call, LogLevelWarn, fmt.Sprintf("duplicate (advanced): %s", reason))
				return
			} else if isPreferred {
				// Preferred call (site or API key) - accept immediately and cancel any queued non-preferred calls
				timeWindow := time.Duration(controller.Options.AdvancedDetectionTimeFrame) * time.Millisecond
				cancelled := controller.CallQueue.CancelPending(
					call.System.Id,
					call.Talkgroup.Id,
					call.Timestamp,
					timeWindow,
				)
				if cancelled > 0 {
					preferredType := "site"
					if isPreferredApiKey && !isPreferredSite {
						preferredType = "API key"
					} else if isPreferredApiKey && isPreferredSite {
						preferredType = "site+API key"
					}
					logCall(call, LogLevelInfo, fmt.Sprintf("preferred %s call accepted, cancelled %d queued call(s)", preferredType, cancelled))
				}
				// Continue processing this preferred call immediately
			} else {
				// Non-preferred call - queue it and wait for preferred
				logCall(call, LogLevelInfo, fmt.Sprintf("non-preferred call queued, waiting %dms for preferred site/API key", controller.Options.AdvancedDetectionTimeFrame))

				waitDuration := time.Duration(controller.Options.AdvancedDetectionTimeFrame) * time.Millisecond
				controller.CallQueue.Add(call, waitDuration, func(queuedCall *Call) {
					// Timer expired - no preferred call arrived, process the non-preferred call
					logCall(queuedCall, LogLevelInfo, "non-preferred call processing after wait period")
					controller.processCallAfterDuplicateCheck(queuedCall)
				})
				return // Don't process now, will process after timer
			}
		}

		// Legacy mode (explicit legacy setting, or fallback when advanced not configured/available)
		// Uses legacy timeframe
		if !useAdvanced || err != nil {
			isDuplicate, err = controller.Calls.CheckDuplicate(
				call,
				controller.Options.DuplicateDetectionTimeFrame,
				controller.Database,
			)
			if err == nil && isDuplicate {
				logCall(call, LogLevelWarn, "duplicate (legacy)")
				return
			}
		}

		if err != nil {
			logError(err)
			return
		}
	}

	// Continue processing after duplicate detection
	controller.processCallAfterDuplicateCheck(call)
}

// processCallAfterDuplicateCheck processes a call after duplicate detection has passed
// This is used both for immediate processing and for queued secondary calls
func (controller *Controller) processCallAfterDuplicateCheck(call *Call) {
	var system *System

	logCall := func(call *Call, level string, msg string) {
		if call.System != nil && call.Talkgroup != nil {
			controller.Logs.LogEvent(level, fmt.Sprintf("[%s] [%s] %s", call.System.Label, call.Talkgroup.Label, msg))
		}
	}

	logError := func(err error) {
		logCall(call, "error", err.Error())
	}

	if call.System != nil {
		system = call.System
	}

	// Store original audio before encoding - tone detection and transcription need raw/uncompressed audio
	// This avoids double lossy conversion (e.g., MP3 -> Opus -> WAV) which degrades quality
	originalAudio := make([]byte, len(call.Audio))
	copy(originalAudio, call.Audio)
	originalAudioMime := call.AudioMime

	// Store original audio in call struct for transcription to use later
	call.OriginalAudio = originalAudio
	call.OriginalAudioMime = originalAudioMime

	// Run tone detection BEFORE encoding to avoid Opus compression affecting tone detection
	shouldDetectTones := call.Talkgroup != nil && call.Talkgroup.ToneDetectionEnabled && len(call.Talkgroup.ToneSets) > 0
	if shouldDetectTones {
		// Create temporary call with original audio for tone detection
		toneDetectionCall := *call
		toneDetectionCall.Audio = originalAudio
		toneDetectionCall.AudioMime = originalAudioMime
		// Run tone detection asynchronously so it doesn't block call processing if it hangs
		// Pass pointer to original call so results can be updated in memory after detection completes
		go controller.processToneDetectionAsync(&toneDetectionCall, call)
	}

	// Now encode audio to Opus/AAC for storage
	if convertErr := controller.FFMpeg.Convert(call, controller.Systems, controller.Tags, controller.Options.AudioConversion, controller.Config, controller.Options); convertErr != nil {
		controller.Logs.LogEvent(LogLevelWarn, convertErr.Error())
	}

	if id, err := controller.Calls.WriteCall(call, controller.Database); err == nil {
		call.Id = id
		// After writing, query the database to get the talkgroup ID that was actually written
		// This ensures we have the correct database ID for logging (like v6 did)
		var dbTalkgroupId uint64
		query := fmt.Sprintf(`SELECT "talkgroupId" FROM "calls" WHERE "callId" = %d`, call.Id)
		if err := controller.Database.Sql.QueryRow(query).Scan(&dbTalkgroupId); err == nil && dbTalkgroupId > 0 {
			if call.Talkgroup != nil {
				call.Talkgroup.Id = dbTalkgroupId
			}
		} else {
			// If query failed, try querying talkgroups table directly
			if call.Talkgroup != nil && system != nil && call.Talkgroup.TalkgroupRef > 0 && system.Id > 0 {
				query2 := fmt.Sprintf(`SELECT "talkgroupId" FROM "talkgroups" WHERE "systemId" = %d AND "talkgroupRef" = %d LIMIT 1`, system.Id, call.Talkgroup.TalkgroupRef)
				if err2 := controller.Database.Sql.QueryRow(query2).Scan(&dbTalkgroupId); err2 == nil && dbTalkgroupId > 0 {
					call.Talkgroup.Id = dbTalkgroupId
				}
			}
		}
		logCall(call, "info", "success")

		// Ensure Units are populated from Meta.UnitRefs before emitting
		// This ensures source information is available when calls are sent
		if len(call.Units) == 0 && len(call.Meta.UnitRefs) > 0 {
			for _, unitRef := range call.Meta.UnitRefs {
				call.Units = append(call.Units, CallUnit{
					UnitRef: unitRef,
					Offset:  0,
				})
			}
		}

		// IMMEDIATE: Emit call to clients (users can play NOW - zero delay)
		controller.EmitCall(call)

		// Note: Tone detection already completed above (before encoding)
		// Queue transcription with tone-aware decision
		go controller.queueTranscriptionIfNeeded(call)

		// Note: Pending tones are checked and attached AFTER transcription completes
		// This ensures we only attach pending tones to calls that actually have voice (not tone-only)
		// See transcription_queue.go where checkAndAttachPendingTones is called after transcription confirms voice
	} else {
		logError(err)
	}
}

// processToneDetectionAsync runs tone detection asynchronously and updates the original call object
func (controller *Controller) processToneDetectionAsync(toneDetectionCall *Call, originalCall *Call) {
	startTime := time.Now()
	systemId := uint64(0)
	if toneDetectionCall.System != nil {
		systemId = toneDetectionCall.System.Id
	}
	talkgroupRef := uint(0)
	if toneDetectionCall.Talkgroup != nil {
		talkgroupRef = toneDetectionCall.Talkgroup.TalkgroupRef
	}

	// Run tone detection on the temporary call
	controller.processToneDetection(toneDetectionCall)

	duration := time.Since(startTime)

	// Log completion time for monitoring
	if controller.DebugLogger != nil {
		controller.DebugLogger.LogToneDetection(toneDetectionCall.Id, systemId, talkgroupRef,
			fmt.Sprintf("Detection completed in %v (audio: %d bytes)", duration, len(toneDetectionCall.Audio)))
	}

	// Log warning if detection took longer than expected (>5 seconds)
	if duration > 5*time.Second {
		controller.Logs.LogEvent(LogLevelWarn,
			fmt.Sprintf("tone detection took %v for call %d (system=%d, talkgroup=%d, audio=%d bytes) - may indicate performance issue",
				duration, toneDetectionCall.Id, systemId, talkgroupRef, len(toneDetectionCall.Audio)))
	}

	// Update the original call object with the results
	if toneDetectionCall.ToneSequence != nil {
		originalCall.ToneSequence = toneDetectionCall.ToneSequence
		originalCall.HasTones = toneDetectionCall.HasTones
	}
}

// processToneDetection processes tone detection for a call (async, doesn't block)
func (controller *Controller) processToneDetection(call *Call) {
	// Debug logging to diagnose why tone detection isn't running
	systemId := uint64(0)
	if call.System != nil {
		systemId = call.System.Id
	}

	if call.Talkgroup == nil {
		return
	}

	if !call.Talkgroup.ToneDetectionEnabled {
		return
	}

	if len(call.Talkgroup.ToneSets) == 0 {
		return
	}

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("tone detection starting for call %d (system=%d, talkgroup=%d, toneSets=%d, audioSize=%d bytes)", call.Id, systemId, call.Talkgroup.TalkgroupRef, len(call.Talkgroup.ToneSets), len(call.Audio)))

	// Debug log
	if controller.DebugLogger != nil {
		controller.DebugLogger.LogToneDetection(call.Id, systemId, call.Talkgroup.TalkgroupRef, fmt.Sprintf("Starting detection - %d tone sets configured, audio size: %d bytes", len(call.Talkgroup.ToneSets), len(call.Audio)))
	}

	// Fast tone detection (100-500ms typically)
	toneSequence, err := controller.ToneDetector.Detect(call.Audio, call.AudioMime, call.Talkgroup.ToneSets)
	if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("tone detection failed for call %d: %v", call.Id, err))
		if controller.DebugLogger != nil {
			controller.DebugLogger.LogToneDetection(call.Id, systemId, call.Talkgroup.TalkgroupRef,
				fmt.Sprintf("FAILED: %v", err))
		}
		return
	}

	if toneSequence == nil {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("tone detection completed for call %d: no sequence returned", call.Id))
		return
	}

	call.ToneSequence = toneSequence
	call.HasTones = len(toneSequence.Tones) > 0

	if call.HasTones {
		// Log detected tone frequencies
		toneFreqs := make([]string, len(toneSequence.Tones))
		for i, tone := range toneSequence.Tones {
			toneFreqs[i] = fmt.Sprintf("%.1f Hz (%.2fs)", tone.Frequency, tone.Duration)
		}
		audioDuration, err := controller.getAudioDuration(call.Audio, call.AudioMime)
		if err != nil {
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to get audio duration for call %d: %v", call.Id, err))
			audioDuration = 0.0
		}
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("tones detected for call %d: %d tones found - %s (audio: %d bytes, duration: %.2fs)", call.Id, len(toneSequence.Tones), strings.Join(toneFreqs, ", "), len(call.Audio), audioDuration))

		// Save audio file labeled as tone-only (will be updated if voice is found later)
		if controller.DebugLogger != nil {
			go controller.DebugLogger.SaveAudioFile(call.Id, call.Audio, call.AudioMime, "tone-only")
		}

		// Match against configured tone sets - find ALL matches for stacked tones
		matchedToneSets := controller.ToneDetector.MatchToneSets(toneSequence, call.Talkgroup.ToneSets)
		toneSequence.MatchedToneSets = matchedToneSets

		// Debug log each detected tone (after matching, so we can show which tone set matched)
		if controller.DebugLogger != nil {
			for _, tone := range toneSequence.Tones {
				// Find which tone set(s) matched this tone
				matchedLabels := []string{}
				for _, ts := range matchedToneSets {
					// Check if this tone matches this tone set
					baseTol := ts.Tolerance
					actualTol := baseTol
					if baseTol < 1.0 {
						actualTol = baseTol * 500.0
					}

					matched := false
					if tone.ToneType == "A" && ts.ATone != nil {
						if math.Abs(tone.Frequency-ts.ATone.Frequency) <= actualTol {
							matched = true
						}
					} else if tone.ToneType == "B" && ts.BTone != nil {
						if math.Abs(tone.Frequency-ts.BTone.Frequency) <= actualTol {
							matched = true
						}
					} else if tone.ToneType == "Long" && ts.LongTone != nil {
						if math.Abs(tone.Frequency-ts.LongTone.Frequency) <= actualTol {
							matched = true
						}
					}

					if matched {
						matchedLabels = append(matchedLabels, ts.Label)
					}
				}

				matchedStr := "NO_MATCH"
				if len(matchedLabels) > 0 {
					matchedStr = fmt.Sprintf("MATCHED: %s", strings.Join(matchedLabels, ", "))
				}
				controller.DebugLogger.LogToneFrequency(call.Id, tone.Frequency, tone.Duration, len(matchedLabels) > 0, matchedStr)
			}
		}

		// Debug log matched tone sets
		if controller.DebugLogger != nil {
			if len(matchedToneSets) > 0 {
				toneSetLabels := make([]string, len(matchedToneSets))
				for i, ts := range matchedToneSets {
					toneSetLabels[i] = ts.Label
				}
				controller.DebugLogger.LogToneDetection(call.Id, systemId, call.Talkgroup.TalkgroupRef, fmt.Sprintf("MATCHED %d tone sets: %s", len(matchedToneSets), strings.Join(toneSetLabels, ", ")))
			} else {
				controller.DebugLogger.LogToneDetection(call.Id, systemId, call.Talkgroup.TalkgroupRef, "No tone sets matched")
			}
		}

		if len(matchedToneSets) > 0 {
			toneSequence.MatchedToneSet = matchedToneSets[0] // Keep first for backward compatibility
			toneSetLabels := make([]string, len(matchedToneSets))
			for i, ts := range matchedToneSets {
				toneSetLabels[i] = ts.Label
			}
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("tone set(s) matched for call %d: %s", call.Id, strings.Join(toneSetLabels, ", ")))
		} else {
			// Log why no match - show what was configured vs what was detected
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("tones detected for call %d but no tone set matched", call.Id))
			if len(call.Talkgroup.ToneSets) > 0 && len(toneSequence.Tones) > 0 {
				// Show first few configured tone frequencies for comparison
				sampleToneSets := call.Talkgroup.ToneSets
				if len(sampleToneSets) > 3 {
					sampleToneSets = sampleToneSets[:3]
				}
				for _, ts := range sampleToneSets {
					expectedFreqs := []string{}
					if ts.ATone != nil {
						expectedFreqs = append(expectedFreqs, fmt.Sprintf("A:%.1f", ts.ATone.Frequency))
					}
					if ts.BTone != nil {
						expectedFreqs = append(expectedFreqs, fmt.Sprintf("B:%.1f", ts.BTone.Frequency))
					}
					if ts.LongTone != nil {
						expectedFreqs = append(expectedFreqs, fmt.Sprintf("Long:%.1f", ts.LongTone.Frequency))
					}
					if len(expectedFreqs) > 0 {
						controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("  configured tone set '%s': %s (tolerance: %.1f Hz)", ts.Label, strings.Join(expectedFreqs, ", "), ts.Tolerance))
					}
				}
			}
		}

		// Update call in database (synchronous to ensure HasTones is set before transcription completes)
		// This prevents a race condition where transcription checks for tones before the DB is updated
		controller.updateCallToneSequence(call.Id, toneSequence)

		// IMMEDIATE PRE-ALERT: Send notification as soon as tones are detected
		// This allows users to tune in right away without waiting for transcription
		// Pre-alerts are not saved to database - they're instant notifications only
		if len(matchedToneSets) > 0 {
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("sending pre-alert notifications for call with %d matched tone sets", len(matchedToneSets)))
			go controller.AlertEngine.TriggerPreAlerts(call)
		}

		// If transcription is still pending, we don't know yet if this is tone-only or has voice
		// Store as pending and wait for transcription to complete
		// Only create alerts immediately if we KNOW there's voice (transcript exists)
		if call.TranscriptionStatus != "completed" || call.Transcript == "" {
			// Transcription not done yet, or no transcript - store as pending
			// After transcription completes, we'll check if it's voice or tone-only
			controller.storePendingTones(call, toneSequence)
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("tones detected on call %d (transcription pending), storing as pending for talkgroup %d (audio: %d bytes, duration: %.2fs, alert will be created after transcription if voice found)", call.Id, call.Talkgroup.TalkgroupRef, len(call.Audio), audioDuration))
		} else if controller.isToneOnlyCall(call) {
			// Transcription completed but no voice - store as pending for next voice call
			controller.storePendingTones(call, toneSequence)
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("tones detected on tone-only call %d, storing as pending for talkgroup %d (audio: %d bytes, duration: %.2fs, no alert created)", call.Id, call.Talkgroup.TalkgroupRef, len(call.Audio), audioDuration))
		} else {
			// Transcription completed and has voice - trigger alert immediately
			go controller.AlertEngine.TriggerToneAlerts(call)
		}
	} else {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("tone detection completed for call %d: no tones detected", call.Id))
	}
}

// getAudioDuration gets the actual audio duration using ffprobe
// Returns duration in seconds and an error if ffprobe fails
// This function requires ffprobe to be installed and working - no fallback estimation
func (controller *Controller) getAudioDuration(audio []byte, audioMime string) (float64, error) {
	if len(audio) == 0 {
		return 0.0, fmt.Errorf("audio data is empty")
	}

	// Write audio to temp file
	tempDir := os.TempDir()
	ext := ".m4a" // default extension
	if strings.Contains(audioMime, "mp3") || strings.Contains(audioMime, "mpeg") {
		ext = ".mp3"
	} else if strings.Contains(audioMime, "wav") || strings.Contains(audioMime, "wave") {
		ext = ".wav"
	} else if strings.Contains(audioMime, "ogg") {
		ext = ".ogg"
	}

	tempFile := filepath.Join(tempDir, fmt.Sprintf("duration_%d%s", time.Now().UnixNano(), ext))

	if err := os.WriteFile(tempFile, audio, 0644); err != nil {
		return 0.0, fmt.Errorf("failed to write temp file for duration check: %v", err)
	}
	defer os.Remove(tempFile)

	// Use ffprobe to get duration with timeout to prevent hanging
	// ffprobe -v error -show_entries format=duration -of json tempFile
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "json", tempFile)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return 0.0, fmt.Errorf("ffprobe timed out after 5 seconds")
		}
		return 0.0, fmt.Errorf("ffprobe failed to get duration: %v, stderr: %s (make sure ffprobe is installed and in PATH)", err, stderr.String())
	}

	// Parse JSON output
	var result struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}

	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return 0.0, fmt.Errorf("failed to parse ffprobe JSON output: %v, stdout: %s", err, stdout.String())
	}

	// Parse duration string to float64
	if result.Format.Duration == "" {
		return 0.0, fmt.Errorf("ffprobe returned empty duration field, stdout: %s", stdout.String())
	}

	duration, err := strconv.ParseFloat(result.Format.Duration, 64)
	if err != nil {
		return 0.0, fmt.Errorf("failed to parse duration '%s' from ffprobe: %v", result.Format.Duration, err)
	}

	return duration, nil
}

// isToneOnlyCall determines if a call contains only tones (no voice/audio content)
// This is a heuristic: if the call is very short (< 3 seconds) or will likely not have voice
func (controller *Controller) isToneOnlyCall(call *Call) bool {
	// Heuristic: very short calls (< 3 seconds) are likely tone-only
	// We can refine this later with actual audio analysis
	// For now, if call has tones and no transcript yet, we'll check again after transcription
	// But if it's very short, assume tone-only

	// Check audio duration using ffprobe
	audioDuration, err := controller.getAudioDuration(call.Audio, call.AudioMime)
	if err != nil {
		// If we can't get duration, we can't determine if it's tone-only
		// Log the error but don't assume it's tone-only
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to get audio duration for call %d in isToneOnlyCall: %v", call.Id, err))
		return false
	}

	// If call is very short (< 3 seconds), likely tone-only
	if audioDuration < 3.0 {
		return true
	}

	// If call has transcript already, it's not tone-only
	if call.Transcript != "" {
		return false
	}

	// If transcription is already completed with no transcript, it's tone-only
	if call.TranscriptionStatus == "completed" && call.Transcript == "" {
		return true
	}

	// Otherwise, we'll check again after transcription completes
	return false
}

// storePendingTones stores tones as pending to attach to subsequent voice call
func (controller *Controller) storePendingTones(call *Call, toneSequence *ToneSequence) {
	if call.System == nil || call.Talkgroup == nil {
		return
	}

	key := fmt.Sprintf("%d:%d", call.System.Id, call.Talkgroup.Id)

	controller.pendingTonesMutex.Lock()
	defer controller.pendingTonesMutex.Unlock()

	// Check if pending tones are "locked" (claimed by an ongoing transcription)
	// If locked, store in "nextPending" slot to be promoted after lock clears
	existing, exists := controller.pendingTones[key]
	if exists && existing != nil && existing.Locked {
		// Pending tones are locked by an ongoing transcription
		// Store these new tones in the "next pending" slot
		// They'll become the new pending tones after the current transcription completes
		nextKey := key + ":next"

		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("pending tones for talkgroup %d are locked, storing call %d tones in next slot", call.Talkgroup.TalkgroupRef, call.Id))

		// Check if there's already a "next pending" - merge with it (stacked tones for next incident)
		nextPending, nextExists := controller.pendingTones[nextKey]
		if !nextExists || nextPending == nil {
			// Create new "next pending"
			controller.pendingTones[nextKey] = &PendingToneSequence{
				ToneSequence: toneSequence,
				CallId:       call.Id,
				Timestamp:    call.Timestamp.UnixMilli(), // Use call timestamp, not processing time
				SystemId:     call.System.Id,
				TalkgroupId:  call.Talkgroup.Id,
				Locked:       false, // Next pending is not locked yet
			}
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("stored call %d tones in next pending slot for talkgroup %d", call.Id, call.Talkgroup.TalkgroupRef))
		} else {
			// Check if existing next pending tones are too old (expired)
			existingAge := time.Now().UnixMilli() - nextPending.Timestamp
			maxAge := int64(pendingToneTimeoutMinutes) * 60 * 1000 // Convert minutes to milliseconds

			if existingAge > maxAge {
				// Existing next pending tones are too old - replace instead of merge
				ageMinutes := float64(existingAge) / 60000.0
				controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("existing next pending tones for call %d are too old (%.1f minutes), replacing with new tones from call %d", nextPending.CallId, ageMinutes, call.Id))

				controller.pendingTones[nextKey] = &PendingToneSequence{
					ToneSequence: toneSequence,
					CallId:       call.Id,
					Timestamp:    call.Timestamp.UnixMilli(), // Use call timestamp, not processing time
					SystemId:     call.System.Id,
					TalkgroupId:  call.Talkgroup.Id,
					Locked:       false,
				}
			} else {
				// Merge with existing "next pending" (multiple tone-only calls for next incident)
				controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("merging next pending: call %d (existing) + call %d (new)", nextPending.CallId, call.Id))
				mergedSequence := controller.mergePendingTones(nextPending.ToneSequence, toneSequence)
				nextPending.ToneSequence = mergedSequence
				nextPending.CallId = call.Id // Update to most recent call
				// IMPORTANT: Keep the original timestamp! Don't update to now, or it may be AFTER the voice call that's already transcribing
				// nextPending.Timestamp = time.Now().UnixMilli() // DON'T DO THIS - it breaks timestamp comparison
				controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("merged next pending tones for talkgroup %d", call.Talkgroup.TalkgroupRef))
			}
		}

		if controller.DebugLogger != nil {
			controller.DebugLogger.LogPendingTones("NEXT_SLOT", call.Id, call.Talkgroup.TalkgroupRef, "Stored in next pending slot (current pending locked)")
		}
		return
	}

	// Store or merge with existing pending tones (for stacked tones across multiple calls)
	if !exists || existing == nil {
		// Create new pending tone sequence
		controller.pendingTones[key] = &PendingToneSequence{
			ToneSequence: toneSequence,
			CallId:       call.Id,
			Timestamp:    call.Timestamp.UnixMilli(), // Use call timestamp, not processing time
			SystemId:     call.System.Id,
			TalkgroupId:  call.Talkgroup.Id,
		}

		// Debug log
		if controller.DebugLogger != nil {
			toneSetLabels := []string{}
			if toneSequence.MatchedToneSets != nil {
				for _, ts := range toneSequence.MatchedToneSets {
					if ts != nil {
						toneSetLabels = append(toneSetLabels, ts.Label)
					}
				}
			}
			controller.DebugLogger.LogPendingTones("STORED", call.Id, call.Talkgroup.TalkgroupRef, fmt.Sprintf("New pending tones stored | ToneSets: %v", toneSetLabels))
		}

		// Start a timer to check if tones are orphaned (no new tones within 60 seconds)
		// If they're still pending after 60 seconds, send an alert for "tones detected but no voice call"
		go controller.checkOrphanedTones(key, call.Id, call.Timestamp.UnixMilli())
	} else {
		// Check if existing pending tones are too old (expired)
		existingAge := time.Now().UnixMilli() - existing.Timestamp
		maxAge := int64(pendingToneTimeoutMinutes) * 60 * 1000 // Convert minutes to milliseconds

		if existingAge > maxAge {
			// Existing pending tones are too old - replace instead of merge
			ageMinutes := float64(existingAge) / 60000.0
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("existing pending tones for call %d are too old (%.1f minutes), replacing with new tones from call %d", existing.CallId, ageMinutes, call.Id))

			controller.pendingTones[key] = &PendingToneSequence{
				ToneSequence: toneSequence,
				CallId:       call.Id,
				Timestamp:    call.Timestamp.UnixMilli(), // Use call timestamp, not processing time
				SystemId:     call.System.Id,
				TalkgroupId:  call.Talkgroup.Id,
			}

			if controller.DebugLogger != nil {
				controller.DebugLogger.LogPendingTones("REPLACED", call.Id, call.Talkgroup.TalkgroupRef, fmt.Sprintf("Replaced expired pending tones from call %d (age: %.1f min)", existing.CallId, ageMinutes))
			}
			return
		}

		// Existing pending tones are fresh enough to merge
		// Log that we're merging with existing pending tones
		existingToneSetLabels := []string{}
		if existing.ToneSequence.MatchedToneSets != nil {
			for _, ts := range existing.ToneSequence.MatchedToneSets {
				if ts != nil {
					existingToneSetLabels = append(existingToneSetLabels, ts.Label)
				}
			}
		}
		newToneSetLabels := []string{}
		if toneSequence.MatchedToneSets != nil {
			for _, ts := range toneSequence.MatchedToneSets {
				if ts != nil {
					newToneSetLabels = append(newToneSetLabels, ts.Label)
				}
			}
		}
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("merging pending tones: call %d (existing: %s) + call %d (new: %s)", existing.CallId, strings.Join(existingToneSetLabels, ", "), call.Id, strings.Join(newToneSetLabels, ", ")))

		// Debug log
		if controller.DebugLogger != nil {
			controller.DebugLogger.LogPendingTones("MERGED", call.Id, call.Talkgroup.TalkgroupRef, fmt.Sprintf("Merging with existing call %d | Existing: %v | New: %v", existing.CallId, existingToneSetLabels, newToneSetLabels))
		}

		// Merge tones from multiple calls (stacked tones)
		// Combine all tones from both sequences
		combinedTones := append(existing.ToneSequence.Tones, toneSequence.Tones...)

		// Update to use the most recent tone sequence structure but with combined tones
		existing.ToneSequence.Tones = combinedTones
		existing.CallId = call.Id // Use the most recent call ID
		// IMPORTANT: Keep the original timestamp! Don't update to now, or it may be AFTER the voice call that's already transcribing
		// existing.Timestamp = time.Now().UnixMilli() // DON'T DO THIS - it breaks timestamp comparison

		// Accumulate ALL matched tone sets across calls (don't overwrite, merge)
		// Create a map to track unique tone set IDs
		matchedToneSetMap := make(map[string]*ToneSet)

		// Add existing matched tone sets
		if existing.ToneSequence.MatchedToneSets != nil {
			for _, ts := range existing.ToneSequence.MatchedToneSets {
				if ts != nil && ts.Id != "" {
					matchedToneSetMap[ts.Id] = ts
				}
			}
		}
		// Also check the singular MatchedToneSet for backward compatibility
		if existing.ToneSequence.MatchedToneSet != nil && existing.ToneSequence.MatchedToneSet.Id != "" {
			matchedToneSetMap[existing.ToneSequence.MatchedToneSet.Id] = existing.ToneSequence.MatchedToneSet
		}

		// Add new matched tone sets
		if toneSequence.MatchedToneSets != nil {
			for _, ts := range toneSequence.MatchedToneSets {
				if ts != nil && ts.Id != "" {
					matchedToneSetMap[ts.Id] = ts
				}
			}
		}
		// Also check the singular MatchedToneSet for backward compatibility
		if toneSequence.MatchedToneSet != nil && toneSequence.MatchedToneSet.Id != "" {
			matchedToneSetMap[toneSequence.MatchedToneSet.Id] = toneSequence.MatchedToneSet
		}

		// Convert map back to slice
		if len(matchedToneSetMap) > 0 {
			existing.ToneSequence.MatchedToneSets = make([]*ToneSet, 0, len(matchedToneSetMap))
			for _, ts := range matchedToneSetMap {
				existing.ToneSequence.MatchedToneSets = append(existing.ToneSequence.MatchedToneSets, ts)
			}
			// Set first one for backward compatibility
			existing.ToneSequence.MatchedToneSet = existing.ToneSequence.MatchedToneSets[0]

			// Log final merged result
			mergedToneSetLabels := make([]string, len(existing.ToneSequence.MatchedToneSets))
			for i, ts := range existing.ToneSequence.MatchedToneSets {
				mergedToneSetLabels[i] = ts.Label
			}
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("merged pending tones result: %d tone set(s) - %s", len(existing.ToneSequence.MatchedToneSets), strings.Join(mergedToneSetLabels, ", ")))
		}
	}
}

// mergePendingTones merges two tone sequences together (for stacked tones across multiple calls)
func (controller *Controller) mergePendingTones(existing *ToneSequence, new *ToneSequence) *ToneSequence {
	if existing == nil {
		return new
	}
	if new == nil {
		return existing
	}

	// Combine all tones from both sequences
	combinedTones := append(existing.Tones, new.Tones...)

	// Create merged sequence with combined tones
	merged := &ToneSequence{
		Tones:           combinedTones,
		ATone:           existing.ATone,          // Keep first detected A-tone
		BTone:           existing.BTone,          // Keep first detected B-tone
		LongTone:        existing.LongTone,       // Keep first detected long tone
		MatchedToneSet:  existing.MatchedToneSet, // Will be updated below
		MatchedToneSets: []*ToneSet{},
	}

	// Accumulate ALL matched tone sets across calls (don't overwrite, merge)
	matchedToneSetMap := make(map[string]*ToneSet)

	// Add existing matched tone sets
	if existing.MatchedToneSets != nil {
		for _, ts := range existing.MatchedToneSets {
			if ts != nil && ts.Id != "" {
				matchedToneSetMap[ts.Id] = ts
			}
		}
	}
	if existing.MatchedToneSet != nil && existing.MatchedToneSet.Id != "" {
		matchedToneSetMap[existing.MatchedToneSet.Id] = existing.MatchedToneSet
	}

	// Add new matched tone sets
	if new.MatchedToneSets != nil {
		for _, ts := range new.MatchedToneSets {
			if ts != nil && ts.Id != "" {
				matchedToneSetMap[ts.Id] = ts
			}
		}
	}
	if new.MatchedToneSet != nil && new.MatchedToneSet.Id != "" {
		matchedToneSetMap[new.MatchedToneSet.Id] = new.MatchedToneSet
	}

	// Convert map back to slice
	if len(matchedToneSetMap) > 0 {
		merged.MatchedToneSets = make([]*ToneSet, 0, len(matchedToneSetMap))
		for _, ts := range matchedToneSetMap {
			merged.MatchedToneSets = append(merged.MatchedToneSets, ts)
		}
		// Set first one for backward compatibility
		merged.MatchedToneSet = merged.MatchedToneSets[0]
	}

	return merged
}

// checkOrphanedTones waits 60 seconds and checks if pending tones are still there without being attached
// If so, triggers an alert for "tones detected but no voice call available"
func (controller *Controller) checkOrphanedTones(key string, callId uint64, timestamp int64) {
	// Wait 60 seconds
	time.Sleep(60 * time.Second)

	controller.pendingTonesMutex.Lock()
	pending, exists := controller.pendingTones[key]
	controller.pendingTonesMutex.Unlock()

	if !exists || pending == nil {
		// Tones were already attached or expired - nothing to do
		return
	}

	// Check if this is still the same pending tone sequence (same timestamp)
	// If timestamp changed, it means new tones were added (merged), so don't trigger alert yet
	if pending.Timestamp != timestamp {
		// Timestamp changed - new tones were merged, so not orphaned
		return
	}

	// Tones have been sitting for 60 seconds without new tones or voice call
	// Trigger an alert for the tone-only call
	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("orphaned tones detected after 60 seconds for call %d - triggering alert without voice", callId))

	if controller.DebugLogger != nil {
		controller.DebugLogger.LogPendingTones("ORPHANED", callId, 0, "Tones pending for 60+ seconds without voice - triggering alert")
	}

	// Load the original call that had the tones
	call, err := controller.Calls.GetCall(callId)
	if err != nil || call == nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to load orphaned tone call %d: %v", callId, err))
		return
	}

	// Ensure the call has the tone sequence attached
	if call.ToneSequence == nil && pending.ToneSequence != nil {
		call.ToneSequence = pending.ToneSequence
		call.HasTones = true
	}

	// Set a special transcript to indicate no voice was available
	if call.Transcript == "" {
		call.Transcript = "TONES DETECTED - NO VOICE CALL AVAILABLE"

		// Update the call in the database with this transcript
		query := fmt.Sprintf(`UPDATE "calls" SET "transcript" = '%s', "transcriptionStatus" = 'completed' WHERE "callId" = %d`,
			escapeQuotes(call.Transcript), callId)
		if _, err := controller.Database.Sql.Exec(query); err != nil {
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to update orphaned call transcript: %v", err))
		}
	}

	// Don't remove from pending yet - let the normal timeout (5 minutes) handle that
	// This allows a late voice call to still attach if it comes in

	// Trigger tone alerts for this orphaned call
	if controller.AlertEngine != nil {
		go controller.AlertEngine.TriggerToneAlerts(call)
	}
}

// checkAndAttachPendingTones checks if there are pending tones for this call's talkgroup and attaches them if this is a voice call
func (controller *Controller) checkAndAttachPendingTones(call *Call) bool {
	if call.System == nil || call.Talkgroup == nil {
		return false
	}

	// Don't attach pending tones if this call already has its own tones detected AND they matched a tone set
	// A call with matched tones means it's either a tone-only call (handled separately)
	// or a call that starts with tones (should keep its own tones, not overwrite with pending)
	// However, if the call has tones that didn't match any tone set, we should still attach pending tones that do match
	if call.HasTones && call.ToneSequence != nil && call.ToneSequence.MatchedToneSets != nil && len(call.ToneSequence.MatchedToneSets) > 0 {
		return false
	}

	// Resolve System.Id and Talkgroup.Id if they're 0 (needed for key lookup)
	systemId := call.System.Id
	talkgroupId := call.Talkgroup.Id

	if systemId == 0 && call.System.SystemRef > 0 {
		query := fmt.Sprintf(`SELECT "systemId" FROM "systems" WHERE "systemRef" = %d LIMIT 1`, call.System.SystemRef)
		if err := controller.Database.Sql.QueryRow(query).Scan(&systemId); err != nil {
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("checkAndAttachPendingTones: failed to resolve systemId from systemRef %d: %v", call.System.SystemRef, err))
			return false
		}
	}

	if talkgroupId == 0 && call.Talkgroup.TalkgroupRef > 0 && systemId > 0 {
		query := fmt.Sprintf(`SELECT "talkgroupId" FROM "talkgroups" WHERE "systemId" = %d AND "talkgroupRef" = %d LIMIT 1`, systemId, call.Talkgroup.TalkgroupRef)
		if err := controller.Database.Sql.QueryRow(query).Scan(&talkgroupId); err != nil {
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("checkAndAttachPendingTones: failed to resolve talkgroupId from talkgroupRef %d: %v", call.Talkgroup.TalkgroupRef, err))
			return false
		}
	}

	if systemId == 0 || talkgroupId == 0 {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("checkAndAttachPendingTones: systemId=%d or talkgroupId=%d is 0, cannot lookup pending tones", systemId, talkgroupId))
		return false
	}

	key := fmt.Sprintf("%d:%d", systemId, talkgroupId)

	controller.pendingTonesMutex.Lock()
	pending, exists := controller.pendingTones[key]
	controller.pendingTonesMutex.Unlock()

	if !exists || pending == nil {
		return false
	}

	// Check if pending tones are still valid (within time window)
	now := time.Now().UnixMilli()
	ageMinutes := float64(now-pending.Timestamp) / (1000.0 * 60.0)
	if ageMinutes > float64(pendingToneTimeoutMinutes) {
		// Pending tones expired, remove them
		controller.pendingTonesMutex.Lock()
		delete(controller.pendingTones, key)
		controller.pendingTonesMutex.Unlock()
		return false
	}

	// CRITICAL: Only attach pending tones to calls that came AFTER the tone call
	// Pending tones should never attach to pre-announcements or calls with earlier timestamps
	// Use timestamps instead of IDs because tone calls may have CallId=0 (not yet saved to database)
	callTimestamp := call.Timestamp.UnixMilli()
	if callTimestamp < pending.Timestamp {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("skipping pending tone attachment: call %d (timestamp %d) came before pending tones (timestamp %d)", call.Id, callTimestamp, pending.Timestamp))
		return false
	}

	// Check if there's a waiting short call - if so, cancel it since we have a longer call now
	controller.cancelWaitingShortCall(key)

	// Check if this call has voice (transcript exists or will exist)
	hasVoice := controller.callHasVoice(call)

	if !hasVoice {
		return false
	}

	// This is a voice call without its own tones - attach pending tones
	call.ToneSequence = pending.ToneSequence
	call.HasTones = pending.ToneSequence != nil && len(pending.ToneSequence.Tones) > 0

	// Use the matched tone sets that were already accumulated during merging
	// Do NOT re-match here (neither A-B pairs nor long tones), as merging tones from multiple calls can cause false matches
	// where A-tones from one call incorrectly pair with B-tones from another call
	// The mergePendingTones function already preserves all valid matches from individual calls
	if call.ToneSequence != nil && call.ToneSequence.MatchedToneSets != nil && len(call.ToneSequence.MatchedToneSets) > 0 {
		// Ensure backward compatibility - set first matched tone set
		if call.ToneSequence.MatchedToneSet == nil && len(call.ToneSequence.MatchedToneSets) > 0 {
			call.ToneSequence.MatchedToneSet = call.ToneSequence.MatchedToneSets[0]
		}

		toneSetLabels := make([]string, len(call.ToneSequence.MatchedToneSets))
		for i, ts := range call.ToneSequence.MatchedToneSets {
			toneSetLabels[i] = ts.Label
		}
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("attached matched tone set(s) from merged pending tones: %s", strings.Join(toneSetLabels, ", ")))

		// Debug log
		if controller.DebugLogger != nil {
			controller.DebugLogger.LogToneAttachment(call.Id, pending.CallId, call.Talkgroup.TalkgroupRef, ageMinutes, toneSetLabels)
			// Save audio file labeled as tone+voice
			go controller.DebugLogger.SaveAudioFile(call.Id, call.Audio, call.AudioMime, "tone+voice")
		}
	}

	// Update call in database with attached tones
	go controller.updateCallToneSequence(call.Id, pending.ToneSequence)

	// Clear pending tones (only attach to FIRST voice call)
	controller.pendingTonesMutex.Lock()
	delete(controller.pendingTones, key)

	// Check if there are "next pending" tones waiting (arrived during lock)
	// Promote them to current pending for the next voice call
	nextKey := key + ":next"
	if nextPending, nextExists := controller.pendingTones[nextKey]; nextExists && nextPending != nil {
		// Promote next pending to current pending
		controller.pendingTones[key] = nextPending
		delete(controller.pendingTones, nextKey)
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("promoted next pending tones to current pending for talkgroup %d (from call %d)", call.Talkgroup.Id, nextPending.CallId))

		if controller.DebugLogger != nil {
			toneSetLabels := []string{}
			if nextPending.ToneSequence != nil && nextPending.ToneSequence.MatchedToneSets != nil {
				for _, ts := range nextPending.ToneSequence.MatchedToneSets {
					if ts != nil {
						toneSetLabels = append(toneSetLabels, ts.Label)
					}
				}
			}
			controller.DebugLogger.LogPendingTones("PROMOTED", nextPending.CallId, call.Talkgroup.TalkgroupRef, fmt.Sprintf("Next pending promoted to current | ToneSets: %v", toneSetLabels))
		}
	}
	controller.pendingTonesMutex.Unlock()

	audioDuration, err := controller.getAudioDuration(call.Audio, call.AudioMime)
	if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to get audio duration for call %d: %v", call.Id, err))
		audioDuration = 0.0
	}
	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("attached pending tones from call %d to voice call %d (talkgroup %d, age: %.2f minutes, audio: %d bytes, duration: %.2fs)", pending.CallId, call.Id, call.Talkgroup.Id, ageMinutes, len(call.Audio), audioDuration))

	// Note: Do NOT trigger alerts here - alerts will be triggered after transcription completes
	// This function may be called before transcription completes, so we wait to ensure voice exists

	return true
}

const (
	minVoiceDurationSeconds = 5.0
	minVoiceWordCount       = 8
	minVoiceCharCount       = 30
)

// callHasVoice determines if a call contains voice/audio content (not just tones)
func (controller *Controller) callHasVoice(call *Call) bool {
	// If call already has a transcript, check if it's actual voice (not tones being transcribed)
	if call.Transcript != "" {
		if !controller.isActualVoice(call.Transcript) {
			return false
		}

		if !controller.hasMeaningfulVoiceContent(call.Transcript) {
			words := len(strings.Fields(call.Transcript))
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("call %d transcript too short (%d words) - waiting 15 seconds for longer voice call", call.Id, words))

			// Check if there are pending tones to attach
			if call.System != nil && call.Talkgroup != nil {
				key := fmt.Sprintf("%d:%d", call.System.Id, call.Talkgroup.Id)
				controller.pendingTonesMutex.Lock()
				pending, hasPending := controller.pendingTones[key]
				controller.pendingTonesMutex.Unlock()

				if hasPending && pending != nil {
					// Store this short call and start a 15-second timer
					controller.storeWaitingShortCall(call, pending)
				}
			}

			return false
		}

		return true
	}

	// If transcription is completed with no transcript, it's tone-only
	if call.TranscriptionStatus == "completed" && (call.Transcript == "" || len(call.Transcript) <= 10) {
		return false
	}

	// Get audio duration using ffprobe
	audioDuration, err := controller.getAudioDuration(call.Audio, call.AudioMime)
	if err != nil {
		// If we can't get duration, we can't determine if it has voice
		// Log the error but assume it might have voice if transcription is in progress
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to get audio duration for call %d in callHasVoice: %v", call.Id, err))
		// If transcription is pending or in progress, assume it might have voice
		if call.TranscriptionStatus == "pending" || call.TranscriptionStatus == "processing" {
			return true
		}
		return false
	}

	// If transcription is pending or in progress, assume it will have voice
	// (tone-only calls usually don't get transcribed unless they have tones)
	if call.TranscriptionStatus == "pending" || call.TranscriptionStatus == "processing" {
		// Longer calls likely have voice
		return audioDuration >= minVoiceDurationSeconds
	}

	// Default: assume call might have voice if it's longer than tone-only threshold
	return audioDuration >= minVoiceDurationSeconds
}

// cleanTranscript removes known hallucination patterns from a transcript
// Returns the cleaned transcript and a flag indicating if any patterns were removed
func (controller *Controller) cleanTranscript(transcript string, callId uint64) (cleanedTranscript string, hadHallucinations bool) {
	if transcript == "" {
		return transcript, false
	}

	patterns := controller.Options.TranscriptionConfig.HallucinationPatterns
	if len(patterns) == 0 {
		return transcript, false
	}

	cleanedTranscript = transcript
	removedPatterns := []string{}

	// Case-insensitive removal of each pattern
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}

		patternUpper := strings.ToUpper(strings.TrimSpace(pattern))
		transcriptUpper := strings.ToUpper(cleanedTranscript)

		if strings.Contains(transcriptUpper, patternUpper) {
			// Remove the pattern (case-insensitive)
			// Find all occurrences and remove them while preserving case of surrounding text
			result := strings.Builder{}
			searchIn := cleanedTranscript

			// Simple case-insensitive replacement
			for {
				idx := strings.Index(strings.ToUpper(searchIn), patternUpper)
				if idx == -1 {
					result.WriteString(searchIn)
					break
				}

				// Write everything before the pattern
				result.WriteString(searchIn[:idx])
				// Skip the pattern
				searchIn = searchIn[idx+len(pattern):]
			}

			cleanedTranscript = result.String()
			removedPatterns = append(removedPatterns, pattern)
		}
	}

	// Clean up extra whitespace
	if len(removedPatterns) > 0 {
		cleanedTranscript = strings.Join(strings.Fields(cleanedTranscript), " ")
		cleanedTranscript = strings.TrimSpace(cleanedTranscript)

		if controller.DebugLogger != nil {
			controller.DebugLogger.WriteLog(fmt.Sprintf("[HALLUCINATION_CLEAN] Call=%d | Original: %q | Cleaned: %q | Removed: %v",
				callId, transcript, cleanedTranscript, removedPatterns))
		}

		return cleanedTranscript, true
	}

	return transcript, false
}

// isActualVoice determines if a transcript contains actual voice content (not just tones being transcribed)
func (controller *Controller) isActualVoice(transcript string) bool {
	if transcript == "" {
		if controller.DebugLogger != nil {
			controller.DebugLogger.LogVoiceDetection(0, "", false, "Empty transcript")
		}
		return false
	}

	transcript = strings.TrimSpace(transcript)

	// Too short to be voice
	if len(transcript) < 10 {
		if controller.DebugLogger != nil {
			controller.DebugLogger.LogVoiceDetection(0, transcript, false, fmt.Sprintf("Too short (%d chars)", len(transcript)))
		}
		return false
	}

	// Check if transcript is mostly repeating the same character (e.g., "BEEEEEE..." from tone transcription)
	// If a single character makes up more than 70% of the transcript, it's likely just tones
	transcriptUpper := strings.ToUpper(transcript)
	runes := []rune(transcriptUpper)
	if len(runes) > 0 {
		charCounts := make(map[rune]int)
		for _, r := range runes {
			if r != ' ' && r != '\n' && r != '\r' && r != '\t' {
				charCounts[r]++
			}
		}

		totalChars := 0
		maxCount := 0
		for _, count := range charCounts {
			totalChars += count
			if count > maxCount {
				maxCount = count
			}
		}

		// If most characters are the same (e.g., "BEEEEEE..."), it's likely tones
		if totalChars > 0 && float64(maxCount)/float64(totalChars) > 0.7 {
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcript appears to be tones (repeating characters: %.1f%%), not voice", float64(maxCount)/float64(totalChars)*100))
			if controller.DebugLogger != nil {
				controller.DebugLogger.LogVoiceDetection(0, transcript, false, fmt.Sprintf("Repeating characters: %.1f%%", float64(maxCount)/float64(totalChars)*100))
			}
			return false
		}
	}

	// Check if transcript has actual words (contains spaces or multiple distinct words)
	words := strings.Fields(transcriptUpper)
	if len(words) < 8 {
		// Very few words - likely not actual voice
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcript has too few words (%d), likely not voice", len(words)))
		if controller.DebugLogger != nil {
			controller.DebugLogger.LogVoiceDetection(0, transcript, false, fmt.Sprintf("Too few words (%d)", len(words)))
		}
		return false
	}

	// Check if transcript has meaningful diversity (different words)
	uniqueWords := make(map[string]bool)
	for _, word := range words {
		if len(word) > 1 {
			uniqueWords[word] = true
		}
	}

	// If all words are the same (e.g., "BEE BEE BEE..."), it's likely tones
	if len(uniqueWords) < 2 && len(words) >= 3 {
		controller.Logs.LogEvent(LogLevelInfo, "transcript has no word diversity (all same), likely not voice")
		if controller.DebugLogger != nil {
			controller.DebugLogger.LogVoiceDetection(0, transcript, false, "No word diversity")
		}
		return false
	}

	// Voice detected - log success
	if controller.DebugLogger != nil {
		controller.DebugLogger.LogVoiceDetection(0, transcript, true, fmt.Sprintf("Valid voice: %d words, %d unique", len(words), len(uniqueWords)))
	}

	return true
}

func (controller *Controller) hasMeaningfulVoiceContent(transcript string) bool {
	trimmed := strings.TrimSpace(transcript)
	if len(trimmed) >= minVoiceCharCount {
		return true
	}

	words := strings.Fields(trimmed)
	return len(words) >= minVoiceWordCount
}

// updateCallToneSequence updates tone sequence in database (async)
func (controller *Controller) updateCallToneSequence(callId uint64, toneSequence *ToneSequence) {
	toneSequenceJson, err := SerializeToneSequence(toneSequence)
	if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to serialize tone sequence: %v", err))
		return
	}

	if toneSequenceJson == "" {
		toneSequenceJson = "{}"
	}

	hasTones := toneSequence != nil && len(toneSequence.Tones) > 0

	query := fmt.Sprintf(`UPDATE "calls" SET "toneSequence" = $1, "hasTones" = %t WHERE "callId" = %d`, hasTones, callId)
	if controller.Database.Config.DbType == DbTypePostgresql {
		_, err = controller.Database.Sql.Exec(query, toneSequenceJson)
	} else {
		query = fmt.Sprintf(`UPDATE "calls" SET "toneSequence" = ?, "hasTones" = %t WHERE "callId" = %d`, hasTones, callId)
		_, err = controller.Database.Sql.Exec(query, toneSequenceJson)
	}

	if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to update tone sequence for call %d: %v", callId, err))
	}
}

// storeWaitingShortCall stores a short voice call with pending tones and starts a 15-second timer
// If no longer call arrives within 15 seconds, pending tones will be attached to this short call
func (controller *Controller) storeWaitingShortCall(call *Call, pending *PendingToneSequence) {
	if call.System == nil || call.Talkgroup == nil {
		return
	}

	key := fmt.Sprintf("%d:%d", call.System.Id, call.Talkgroup.Id)

	controller.waitingShortCallsMutex.Lock()
	defer controller.waitingShortCallsMutex.Unlock()

	// Cancel any existing waiting short call for this talkgroup
	if existing, exists := controller.waitingShortCalls[key]; exists && existing != nil {
		if existing.Timer != nil {
			existing.Timer.Stop()
		}
		if existing.CancelChan != nil {
			select {
			case existing.CancelChan <- true:
			default:
			}
		}
	}

	// Create cancel channel for this waiting call
	cancelChan := make(chan bool, 1)

	// Create a timer that will attach to the short call after 15 seconds
	timer := time.AfterFunc(15*time.Second, func() {
		// Check if we were cancelled
		select {
		case <-cancelChan:
			return // Was cancelled, do nothing
		default:
		}

		controller.waitingShortCallsMutex.Lock()
		waiting, stillExists := controller.waitingShortCalls[key]
		controller.waitingShortCallsMutex.Unlock()

		if !stillExists || waiting == nil || waiting.Call == nil {
			return
		}

		// Reload call from database to get latest state
		shortCall, err := controller.Calls.GetCall(waiting.Call.Id)
		if err != nil || shortCall == nil {
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to reload short call %d for tone attachment", waiting.Call.Id))
			return
		}

		// 15 seconds passed - attach pending tones to the short call
		shortCall.ToneSequence = waiting.PendingTones.ToneSequence
		shortCall.HasTones = waiting.PendingTones.ToneSequence != nil && len(waiting.PendingTones.ToneSequence.Tones) > 0

		if shortCall.ToneSequence != nil && shortCall.ToneSequence.MatchedToneSets != nil && len(shortCall.ToneSequence.MatchedToneSets) > 0 {
			if shortCall.ToneSequence.MatchedToneSet == nil {
				shortCall.ToneSequence.MatchedToneSet = shortCall.ToneSequence.MatchedToneSets[0]
			}

			toneSetLabels := make([]string, len(shortCall.ToneSequence.MatchedToneSets))
			for i, ts := range shortCall.ToneSequence.MatchedToneSets {
				toneSetLabels[i] = ts.Label
			}
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("15 seconds elapsed - attaching pending tones to short call %d: %s", shortCall.Id, strings.Join(toneSetLabels, ", ")))
		}

		// Update call in database with attached tones
		go controller.updateCallToneSequence(shortCall.Id, waiting.PendingTones.ToneSequence)

		// Clear pending tones
		controller.pendingTonesMutex.Lock()
		delete(controller.pendingTones, key)
		controller.pendingTonesMutex.Unlock()

		// Clear waiting short call
		controller.waitingShortCallsMutex.Lock()
		delete(controller.waitingShortCalls, key)
		controller.waitingShortCallsMutex.Unlock()

		// Trigger tone alerts for the short call
		go controller.AlertEngine.TriggerToneAlerts(shortCall)
	})

	// Store the waiting short call
	controller.waitingShortCalls[key] = &WaitingShortCall{
		Call:         call,
		PendingTones: pending,
		Timer:        timer,
		CancelChan:   cancelChan,
	}

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("stored short call %d (transcript: %d words) - waiting 15 seconds for longer voice call", call.Id, len(strings.Fields(call.Transcript))))
}

// cancelWaitingShortCall cancels any waiting short call for the given talkgroup
func (controller *Controller) cancelWaitingShortCall(key string) {
	controller.waitingShortCallsMutex.Lock()
	defer controller.waitingShortCallsMutex.Unlock()

	if waiting, exists := controller.waitingShortCalls[key]; exists && waiting != nil {
		if waiting.Timer != nil {
			waiting.Timer.Stop()
		}
		if waiting.CancelChan != nil {
			select {
			case waiting.CancelChan <- true:
			default:
			}
		}
		delete(controller.waitingShortCalls, key)
		controller.Logs.LogEvent(LogLevelInfo, "cancelled waiting short call - longer voice call arrived")
	}
}

// clearPendingState clears all pending tones and waiting short calls on server startup
// This ensures a clean state after an unexpected shutdown
func (controller *Controller) clearPendingState() {
	// Clear pending tones
	controller.pendingTonesMutex.Lock()
	pendingCount := len(controller.pendingTones)
	controller.pendingTones = make(map[string]*PendingToneSequence)
	controller.pendingTonesMutex.Unlock()

	// Clear waiting short calls and cancel any timers
	controller.waitingShortCallsMutex.Lock()
	waitingCount := len(controller.waitingShortCalls)
	for _, waiting := range controller.waitingShortCalls {
		if waiting != nil {
			if waiting.Timer != nil {
				waiting.Timer.Stop()
			}
			if waiting.CancelChan != nil {
				select {
				case waiting.CancelChan <- true:
				default:
				}
			}
		}
	}
	controller.waitingShortCalls = make(map[string]*WaitingShortCall)
	controller.waitingShortCallsMutex.Unlock()

	if pendingCount > 0 || waitingCount > 0 {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("cleared %d pending tone sequences and %d waiting short calls on startup", pendingCount, waitingCount))
	}
}

// resetStuckTranscriptions resets any calls stuck in "processing" status back to "pending"
// This handles cases where the server was shut down while transcription was in progress
func (controller *Controller) resetStuckTranscriptions() {
	var query string
	if controller.Database.Config.DbType == DbTypePostgresql {
		query = `UPDATE "calls" SET "transcriptionStatus" = 'pending' WHERE "transcriptionStatus" = 'processing'`
	} else {
		query = `UPDATE "calls" SET "transcriptionStatus" = 'pending' WHERE "transcriptionStatus" = 'processing'`
	}

	result, err := controller.Database.Sql.Exec(query)
	if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to reset stuck transcriptions: %v", err))
		return
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to get rows affected for stuck transcription reset: %v", err))
		return
	}

	if rowsAffected > 0 {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("reset %d calls from 'processing' to 'pending' status on startup", rowsAffected))
	}
}

// queueTranscriptionIfNeeded queues transcription if at least one user has alerts enabled for this talkgroup
func (controller *Controller) queueTranscriptionIfNeeded(call *Call) {
	// Lazily initialize the transcription queue if enabled but not started yet
	if controller.TranscriptionQueue == nil && controller.Options.TranscriptionConfig.Enabled {
		controller.Logs.LogEvent(LogLevelInfo, "transcription queue not initialized starting now due to incoming call")
		controller.TranscriptionQueue = NewTranscriptionQueue(controller, controller.Options.TranscriptionConfig)
	}
	if controller.TranscriptionQueue == nil || !controller.Options.TranscriptionConfig.Enabled {
		return
	}

	// Check if transcription is needed
	needsTranscription := false
	priority := 50
	reasons := []string{}

	// Check minimum call duration if configured
	// EXCEPTION: Tone-enabled talkgroups bypass this check if they have > 2s after tone removal
	// Do this check asynchronously to avoid blocking the worker
	minDuration := controller.Options.TranscriptionConfig.MinCallDuration
	toneDetectionEnabled := call.Talkgroup != nil && call.Talkgroup.ToneDetectionEnabled

	if minDuration > 0 {
		// Check duration in a separate goroutine to avoid blocking
		go func() {
			audioDuration, err := controller.getAudioDuration(call.Audio, call.AudioMime)
			if err != nil {
				// ffprobe failed - log but don't block
				controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("ffprobe failed for call %d (will skip transcription): %v", call.Id, err))
				return
			}

			// For tone-enabled talkgroups with detected tones, check remaining audio
			if toneDetectionEnabled && call.HasTones && call.ToneSequence != nil && len(call.ToneSequence.Tones) > 0 {
				// Calculate total tone duration
				toneDuration := 0.0
				for _, tone := range call.ToneSequence.Tones {
					toneDuration += tone.Duration
				}

				// Calculate remaining audio duration after tones would be removed
				remainingDuration := audioDuration - toneDuration
				const minRemainingDuration = 2.0

				if remainingDuration < minRemainingDuration {
					// Tone-only call - don't queue for transcription (avoids locking pending tones)
					controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("skipping transcription for call %d on tone-enabled talkgroup: remaining audio after tone removal (%.1fs) is less than minimum (%.1fs) - tone-only", call.Id, remainingDuration, minRemainingDuration))
					updateQuery := fmt.Sprintf(`UPDATE "calls" SET "transcriptionStatus" = 'completed' WHERE "callId" = %d`, call.Id)
					controller.Database.Sql.Exec(updateQuery)
					return
				}

				// Has > 2s remaining after tones - bypass global minimum and transcribe
				controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("call %d on tone-enabled talkgroup: bypassing global minimum (%.1fs < %.1fs), %.1fs remaining after %.1fs tones", call.Id, audioDuration, minDuration, remainingDuration, toneDuration))
				// Continue to alert checks (bypassed global minimum)
			} else if toneDetectionEnabled && audioDuration >= minDuration {
				// Tone-enabled talkgroup without tones but meets global minimum - allow
				controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("call %d on tone-enabled talkgroup: meets global minimum (%.1fs >= %.1fs)", call.Id, audioDuration, minDuration))
				// Continue to alert checks
			} else if toneDetectionEnabled && audioDuration < minDuration {
				// Tone-enabled talkgroup without tones, shorter than minimum - still allow (might be voice after tone call)
				controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("call %d on tone-enabled talkgroup: bypassing global minimum (%.1fs < %.1fs), no tones in this call", call.Id, audioDuration, minDuration))
				// Continue to alert checks
			} else if audioDuration < minDuration {
				// Normal check for non-tone-enabled talkgroups or calls without tones
				controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("skipping transcription for call %d: duration %.1fs is less than minimum %.1fs", call.Id, audioDuration, minDuration))
				return
			}

			// NEW: Check if audio is mostly tones (would result in very short remaining audio after tone removal)
			// This applies to calls with tones but NOT on tone-enabled talkgroups (already handled above)
			if !toneDetectionEnabled && call.HasTones && call.ToneSequence != nil && len(call.ToneSequence.Tones) > 0 {
				// Calculate total tone duration
				toneDuration := 0.0
				for _, tone := range call.ToneSequence.Tones {
					toneDuration += tone.Duration
				}

				// Calculate remaining audio duration after tones would be removed
				remainingDuration := audioDuration - toneDuration
				const minRemainingDuration = 2.0

				if remainingDuration < minRemainingDuration {
					controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("skipping transcription for call %d: remaining audio after tone removal (%.1fs) is less than minimum (%.1fs) - likely tone-only", call.Id, remainingDuration, minRemainingDuration))
					// Mark as completed so pending tones don't wait forever
					updateQuery := fmt.Sprintf(`UPDATE "calls" SET "transcriptionStatus" = 'completed' WHERE "callId" = %d`, call.Id)
					controller.Database.Sql.Exec(updateQuery)
					return
				}
				controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("call %d has sufficient remaining audio after tone removal (%.1fs of %.1fs total, %.1fs tones)", call.Id, remainingDuration, audioDuration, toneDuration))
			}

			// Duration check passed, now check if any user has alerts enabled
			hasToneAlerts := controller.hasUsersWithToneAlerts(call.System.Id, call.Talkgroup.Id)
			hasKeywordAlerts := controller.hasUsersWithKeywordAlerts(call.System.Id, call.Talkgroup.Id)

			localReasons := []string{}
			if hasToneAlerts {
				localReasons = append(localReasons, "tone_alerts")
			}
			if hasKeywordAlerts {
				localReasons = append(localReasons, "keyword_alerts")
			}

			if len(localReasons) == 0 {
				controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("no users with alerts enabled for call %d (system=%d, talkgroup=%d)", call.Id, call.System.Id, call.Talkgroup.Id))
				return
			}

			// Both duration and alert checks passed, queue transcription
			controller.queueTranscriptionJobIfNeeded(call, priority, localReasons)
		}()
		return // Exit early, goroutine will handle queueing
	}

	// Reason 2: Any user has tone alerts OR keyword alerts enabled for this talkgroup
	if !needsTranscription {
		hasToneAlerts := controller.hasUsersWithToneAlerts(call.System.Id, call.Talkgroup.Id)
		hasKeywordAlerts := controller.hasUsersWithKeywordAlerts(call.System.Id, call.Talkgroup.Id)

		if hasToneAlerts {
			needsTranscription = true
			reasons = append(reasons, "tone_alerts")
		}

		if hasKeywordAlerts {
			needsTranscription = true
			reasons = append(reasons, "keyword_alerts")
		}

		if !needsTranscription {
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("no users with alerts enabled for call %d (system=%d, talkgroup=%d)", call.Id, call.System.Id, call.Talkgroup.Id))
		}
	}

	if needsTranscription {
		controller.queueTranscriptionJobIfNeeded(call, priority, reasons)
	}
}

// queueTranscriptionJobIfNeeded is a helper to queue a transcription job
// Extracted to allow async duration checking without duplicating queue logic
func (controller *Controller) queueTranscriptionJobIfNeeded(call *Call, priority int, reasons []string) {
	queue := controller.TranscriptionQueue
	if queue != nil {
		// Use original audio for transcription if available (avoids double lossy conversion)
		// Falls back to converted audio if original is not available
		audioToUse := call.Audio
		mimeToUse := call.AudioMime
		if len(call.OriginalAudio) > 0 && call.OriginalAudioMime != "" {
			audioToUse = call.OriginalAudio
			mimeToUse = call.OriginalAudioMime
		}

		queue.QueueJob(TranscriptionJob{
			CallId:        call.Id,
			Audio:         call.Audio, // Keep converted audio for backward compatibility
			AudioMime:     call.AudioMime,
			OriginalAudio: audioToUse, // Preferred audio for transcription
			OriginalMime:  mimeToUse,
			SystemId:      call.System.Id,
			TalkgroupId:   call.Talkgroup.Id,
			Priority:      priority,
			Reasons:       reasons,
		})
	} else {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("transcription queue became unavailable while processing call %d", call.Id))
	}
}

// hasUsersWithToneAlerts checks if any user has tone alerts enabled for this talkgroup
func (controller *Controller) hasUsersWithToneAlerts(systemId uint64, talkgroupId uint64) bool {
	query := fmt.Sprintf(`SELECT COUNT(*) FROM "userAlertPreferences" WHERE "systemId" = %d AND "talkgroupId" = %d AND "alertEnabled" = true AND "toneAlerts" = true`, systemId, talkgroupId)
	var count uint64
	if err := controller.Database.Sql.QueryRow(query).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

// hasUsersWithKeywordAlerts checks if any user has keyword alerts enabled for this talkgroup
func (controller *Controller) hasUsersWithKeywordAlerts(systemId uint64, talkgroupId uint64) bool {
	query := fmt.Sprintf(`SELECT COUNT(*) FROM "userAlertPreferences" WHERE "systemId" = %d AND "talkgroupId" = %d AND "alertEnabled" = true AND "keywordAlerts" = true`, systemId, talkgroupId)
	var count uint64
	if err := controller.Database.Sql.QueryRow(query).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

func (controller *Controller) LogClientsCount() {
	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("listeners count is %v", controller.Clients.Count()))
}

func (controller *Controller) ProcessMessage(client *Client, message *Message) error {
	restricted := controller.requiresUserAuth()

	if message.Command == MessageCommandVersion {
		controller.ProcessMessageCommandVersion(client)

	} else if restricted && client.User == nil && message.Command != MessageCommandPin {
		msg := &Message{Command: MessageCommandPin}
		select {
		case client.Send <- msg:
		default:
		}

	} else if client.PinExpired && message.Command != MessageCommandPin && message.Command != MessageCommandVersion {
		// PIN is expired - ignore all messages except PIN (for re-authentication) and Version
		// This prevents the client from interacting with the scanner when their subscription is expired
		return nil

	} else if message.Command == MessageCommandCall {
		if err := controller.ProcessMessageCommandCall(client, message); err != nil {
			return err
		}

	} else if message.Command == MessageCommandConfig {
		// Client is requesting config - only send if not already sent (avoid duplicate config messages)
		// Config is already sent after PIN authentication, so this is usually redundant
		// But send it anyway in case client needs it
		client.SendConfig(controller.Groups, controller.Options, controller.Systems, controller.Tags)

	} else if message.Command == MessageCommandListCall {
		if err := controller.ProcessMessageCommandListCall(client, message); err != nil {
			return err
		}

	} else if message.Command == MessageCommandLivefeedMap {
		controller.ProcessMessageCommandLivefeedMap(client, message)

	} else if message.Command == MessageCommandPin {
		if err := controller.ProcessMessageCommandPin(client, message); err != nil {
			return err
		}
	}

	return nil
}

func (controller *Controller) ProcessMessageCommandCall(client *Client, message *Message) error {
	var (
		call   *Call
		callId uint64
		err    error
	)

	switch v := message.Payload.(type) {
	case float64:
		callId = uint64(v)
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			callId = uint64(i)
		} else {
			return err
		}
	}

	// Check if call is still in global delay (blocks all playback until it clears)
	if controller.Delayer.IsCallDelayed(callId) {
		msg := &Message{Command: MessageCommandError, Payload: fmt.Sprintf("call %d is currently delayed and not available for playback", callId)}
		select {
		case client.Send <- msg:
		default:
		}
		return nil
	}

	if call, err = controller.Calls.GetCall(callId); err != nil {
		// Send error message to client instead of just returning error
		msg := &Message{Command: MessageCommandError, Payload: err.Error()}
		select {
		case client.Send <- msg:
		default:
		}
		return nil // Don't return error to prevent connection issues
	}

	// Check user access (includes group access restrictions)
	if controller.requiresUserAuth() {
		if client.User == nil || !controller.userHasAccess(client.User, call) {
			msg := &Message{Command: MessageCommandError, Payload: "access denied"}
			select {
			case client.Send <- msg:
			default:
			}
			return nil
		}

		// Check user/group-specific delay for playback
		// Even if the call passed the global delay check, this user might have a longer delay
		effectiveDelay := controller.Options.DefaultSystemDelay
		if client.User != nil {
			effectiveDelay = controller.userEffectiveDelay(client.User, call, controller.Options.DefaultSystemDelay)
		}

		if effectiveDelay > 0 {
			delayCompletionTime := call.Timestamp.Add(time.Duration(effectiveDelay) * time.Minute)
			if time.Now().Before(delayCompletionTime) {
				msg := &Message{Command: MessageCommandError, Payload: fmt.Sprintf("call %d is still delayed for your account and not available for playback", callId)}
				select {
				case client.Send <- msg:
				default:
				}
				return nil
			}
		}
	}

	msg := &Message{Command: MessageCommandCall, Payload: call, Flag: message.Flag}
	select {
	case client.Send <- msg:
	default:
	}
	return nil
}

func (controller *Controller) ProcessMessageCommandListCall(client *Client, message *Message) error {
	switch v := message.Payload.(type) {
	case map[string]any:
		searchOptions := NewCallSearchOptions().fromMap(v)
		if searchResults, err := controller.Calls.Search(searchOptions, client); err == nil {
			msg := &Message{Command: MessageCommandListCall, Payload: searchResults}
			select {
			case client.Send <- msg:
			default:
			}
		} else {
			return fmt.Errorf("controller.processmessage.commandlistcall: %v", err)
		}
	}
	return nil
}

func (controller *Controller) ProcessMessageCommandLivefeedMap(client *Client, message *Message) {
	// Check if this is a livefeed stop (null/empty map)
	wasAllOff := client.Livefeed.IsAllOff()

	client.Livefeed.FromMap(message.Payload)
	msg := &Message{Command: MessageCommandLivefeedMap, Payload: !client.Livefeed.IsAllOff()}
	select {
	case client.Send <- msg:
	default:
	}

	// Only send available calls on initial livefeed start (not on channel toggles)
	// GitHub issue #93: Prevents backlog duplication when toggling channels
	if !client.Livefeed.IsAllOff() {
		// If livefeed was previously all off (initial start or resuming from stopped state)
		if wasAllOff {
			// Mark that we're starting fresh - backlog should be sent
			client.BacklogSent = false
		}

		// Only send backlog if we haven't sent it yet for this livefeed session
		if !client.BacklogSent {
			client.BacklogSent = true
			go controller.sendAvailableCallsToClient(client)
		}
	} else {
		// Livefeed turned off - reset flag so backlog will be sent on next start
		client.BacklogSent = false
	}
}

// sendAvailableCallsToClient sends calls that are currently available to a newly connected client
func (controller *Controller) sendAvailableCallsToClient(client *Client) {
	if controller.requiresUserAuth() && client.User == nil {
		return
	}

	// Calculate cutoff time based on user's default delay
	// If user has 5 min delay, and current time is 11:00 PM, get calls from 10:55 PM onwards
	defaultDelay := controller.Options.DefaultSystemDelay
	if client.User != nil {
		// Get user's default delay (group or user level)
		if client.User.UserGroupId > 0 {
			group := controller.UserGroups.Get(client.User.UserGroupId)
			if group != nil && group.Delay > 0 {
				defaultDelay = uint(group.Delay)
			}
		}
		if defaultDelay == controller.Options.DefaultSystemDelay && client.User.Delay > 0 {
			defaultDelay = uint(client.User.Delay)
		}
	}

	// Get backlog setting from user preferences (available to all users)
	var backlogMinutes uint = 0
	if client.User != nil && client.User.Settings != "" {
		var settings map[string]any
		if err := json.Unmarshal([]byte(client.User.Settings), &settings); err == nil {
			if backlog, ok := settings["livefeedBacklogMinutes"].(float64); ok {
				backlogMinutes = uint(backlog)
			}
		}
	}

	var cutoffTime time.Time
	if defaultDelay == 0 {
		// No delay - if backlog is requested, use it; otherwise return early
		if backlogMinutes == 0 {
			// No backlog requested - return early, only send new calls going forward
			return
		}
		// User requested backlog - use it as cutoff time from current time
		cutoffTime = time.Now().Add(-time.Duration(backlogMinutes) * time.Minute)
	} else {
		// User has a delay - backlog is measured from the start of the delay period
		// Example: 5 min delay, 2 min backlog, current time 11:00 PM
		// Delay period starts at 10:55 PM, backlog goes back 2 min from there = 10:53 PM
		// System/talkgroup delays are always respected and can't be overridden
		delayStartTime := time.Now().Add(-time.Duration(defaultDelay) * time.Minute)

		if backlogMinutes > 0 {
			// Subtract backlog from the delay start time
			cutoffTime = delayStartTime.Add(-time.Duration(backlogMinutes) * time.Minute)
		} else {
			// No backlog setting - start from delay period start only
			cutoffTime = delayStartTime
		}
	}

	var (
		rows *sql.Rows
		err  error
	)

	query := `SELECT c."callId", c."timestamp" FROM "calls" AS c WHERE c."timestamp" >= $1 ORDER BY c."timestamp" ASC LIMIT 1000`

	if rows, err = controller.Database.Sql.Query(query, cutoffTime.UnixMilli()); err != nil {
		controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("sendAvailableCallsToClient query failed: %v", err))
		return
	}
	defer rows.Close()

	for rows.Next() {
		var (
			callId    uint64
			timestamp int64
		)

		if err = rows.Scan(&callId, &timestamp); err != nil {
			continue
		}

		call, err := controller.Calls.GetCall(callId)
		if err != nil || call == nil {
			continue
		}

		if controller.requiresUserAuth() {
			if client.User == nil || !controller.userHasAccess(client.User, call) {
				continue
			}
		}

		// For delayed feed catchup: send all calls from the delayed window
		// The cutoff time was already calculated based on user's delay
		// So all calls in the query result should be sent (no additional delay checks)
		if client.Livefeed.IsEnabled(call) {
			msg := &Message{Command: MessageCommandCall, Payload: call}
			// Use non-blocking send for safety, with small delay to preserve order
			select {
			case client.Send <- msg:
				// Small delay to ensure chronological order is preserved
				time.Sleep(1 * time.Millisecond)
			default:
				// Channel full or client disconnected, skip to avoid blocking
			}
		}
	}
}

func (controller *Controller) requiresUserAuth() bool {
	if controller.Options == nil {
		return false
	}

	if controller.Options.UserRegistrationEnabled {
		return true
	}

	if controller.Users != nil && controller.Users.HasPins() {
		return true
	}

	return false
}

func (controller *Controller) ProcessMessageCommandPin(client *Client, message *Message) error {
	const maxAuthCount = 5

	switch v := message.Payload.(type) {
	case string:
		b, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return fmt.Errorf("controller.processmessage.commandpin: %v", err)
		}

		client.AuthCount++
		if client.AuthCount > maxAuthCount {
			msg := &Message{Command: MessageCommandPin}
			select {
			case client.Send <- msg:
			default:
			}
			return nil
		}

		code := string(b)
		user := controller.Users.GetUserByPin(code)

		// If user auth is required and no user found, reject
		if controller.requiresUserAuth() && user == nil {
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("invalid user pin %s for ip %s", code, client.GetRemoteAddr()))
			msg := &Message{Command: MessageCommandPin}
			select {
			case client.Send <- msg:
			default:
			}
			return nil
		}

		// Check if PIN is expired - we still want to send config so user can see pricing options
		var pinExpired bool
		if user != nil {
			pinExpired = user.PinExpired()
			client.PinExpired = pinExpired
			if pinExpired {
				controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("expired pin for user %s", user.Email))
				msg := &Message{Command: MessageCommandExpired}
				select {
				case client.Send <- msg:
				default:
				}
				// Continue to set user and send config so they can see pricing options and subscribe
			}
		} else {
			client.PinExpired = false
		}

		// Serialize authentication per user to prevent race conditions
		if user != nil {
			// Check if this client is already authenticated
			if client.User != nil && client.User.Id == user.Id {
				log.Printf("DEBUG: Client from IP %s already authenticated as user %s, ignoring duplicate auth",
					client.GetRemoteAddr(), user.Email)
				return nil
			}

			// Get or create mutex for this user
			controller.authMutexesMutex.Lock()
			userMutex, exists := controller.authMutexes[user.Id]
			if !exists {
				userMutex = &sync.Mutex{}
				controller.authMutexes[user.Id] = userMutex
			}
			controller.authMutexesMutex.Unlock()

			// Lock this user's authentication mutex
			userMutex.Lock()
			defer userMutex.Unlock()

			effectiveLimit := controller.userEffectiveConnectionLimit(user)
			if effectiveLimit > 0 {
				currentCount := controller.Clients.UserConnectionCount(user)
				if currentCount >= effectiveLimit {
					controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("too many concurrent connections for user %s, limit is %d", user.Email, effectiveLimit))
					// Send the connection limit to the client so it can display a helpful message
					msg := &Message{Command: MessageCommandMax, Payload: effectiveLimit}
					select {
					case client.Send <- msg:
					default:
					}
					return nil
				}
			}

			// Set user and authenticate (still holding the lock)
			client.AuthCount = 0
			client.User = user
		} else {
			client.AuthCount = 0
			client.User = user
		}

		if user != nil {
			if !pinExpired {
			} else {
				controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("user connected with expired pin email=%s ip=%s (sending config for subscription)", user.Email, client.GetRemoteAddr()))
			}
			if strings.TrimSpace(user.Pin) != "" {
				msg := &Message{Command: MessageCommandPinSet, Payload: user.Pin}
				select {
				case client.Send <- msg:
				default:
				}
			}
		}

		client.SendConfig(controller.Groups, controller.Options, controller.Systems, controller.Tags)

		// Attempt to restore buffered calls from a previous disconnection
		if user != nil && !pinExpired && controller.ReconnectionMgr != nil {
			controller.ReconnectionMgr.RestoreClientState(client)
		}
	}

	return nil
}

func (controller *Controller) ProcessMessageCommandVersion(client *Client) {
	p := map[string]string{"version": Version}

	if len(controller.Options.Branding) > 0 {
		p["branding"] = controller.Options.Branding
	}

	if len(controller.Options.Email) > 0 {
		p["email"] = controller.Options.Email
	}

	msg := &Message{Command: MessageCommandVersion, Payload: p}
	select {
	case client.Send <- msg:
	default:
	}
}

func (controller *Controller) Start() error {
	var err error

	if controller.running {
		return errors.New("controller already running")
	} else {
		controller.running = true
	}

	controller.Logs.LogEvent(LogLevelWarn, "server started")

	if len(controller.Config.BaseDir) > 0 {
		log.Printf("base folder is %s\n", controller.Config.BaseDir)
	}

	// Clear any pending tones and waiting short calls from previous session
	controller.clearPendingState()

	// Reset any calls stuck in "processing" status from previous session
	controller.resetStuckTranscriptions()

	// Batch database reads for better performance
	if err = controller.readAllData(); err != nil {
		return err
	}

	// Fetch Radio Reference API key from relay server if not already stored
	if controller.Options.RadioReferenceAPIKey == "" {
		controller.fetchRadioReferenceAPIKey()
	}

	// Initialize transcription queue after options are loaded
	if controller.Options.TranscriptionConfig.Enabled {
		controller.TranscriptionQueue = NewTranscriptionQueue(controller, controller.Options.TranscriptionConfig)
	} else {
		controller.Logs.LogEvent(LogLevelInfo, "transcription is disabled in config")
	}

	// Start system health monitoring for system admins
	controller.StartSystemHealthMonitoring()

	// Start reconnection manager cleanup routine
	if controller.ReconnectionMgr != nil {
		controller.ReconnectionMgr.StartCleanup()
		controller.Logs.LogEvent(LogLevelInfo, "Reconnection manager started")
	}

	// Start central management service if enabled
	if controller.Options.CentralManagementEnabled {
		controller.CentralManagement.Start()
		controller.Logs.LogEvent(LogLevelInfo, "Central Management service started")
	}

	if err = controller.Admin.Start(); err != nil {
		return err
	}
	if err := controller.Delayer.Start(); err != nil {
		return err
	}
	if err := controller.Scheduler.Start(); err != nil {
		return err
	}

	// Create a context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	controller.workerCancel = cancel

	// Create worker pool to process calls from Ingest channel
	// Use 2x CPU cores to prevent worker pool exhaustion during blocking operations
	workerCount := runtime.NumCPU() * 2
	controller.workerStats.activeWorkers = workerCount
	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("Starting %d call processing workers", workerCount))

	for i := 0; i < workerCount; i++ {
		controller.workersWg.Add(1)
		go func(workerId int) {
			defer controller.workersWg.Done()
			defer func() {
				if r := recover(); r != nil {
					controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("PANIC in Worker %d: %v", workerId, r))
				}
			}()

			for {
				select {
				case call := <-controller.Ingest:
					if call != nil {
						startTime := time.Now()
						controller.IngestCall(call)
						processTime := time.Since(startTime)

						controller.workerStats.Lock()
						controller.workerStats.totalCalls++
						if controller.workerStats.totalCalls == 1 {
							controller.workerStats.avgProcessTime = processTime
						} else {
							controller.workerStats.avgProcessTime = (controller.workerStats.avgProcessTime + processTime) / 2
						}
						controller.workerStats.Unlock()
					}
				case <-ctx.Done():
					return
				}
			}
		}(i)
	}

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("Worker pool started - %d workers ready", workerCount))

	// Start client management goroutine
	go func() {
		var timer *time.Timer

		emitClientsCount := func() {
			if timer == nil {
				timer = time.AfterFunc(time.Duration(5)*time.Second, func() {
					controller.LogClientsCount()
					if controller.Options.ShowListenersCount {
						controller.Clients.EmitListenersCount()
					}
					timer = nil
				})
			}
		}

		for {
			select {
			case client := <-controller.Register:
				controller.Clients.Add(client)
				emitClientsCount()

			case client := <-controller.Unregister:
				controller.Clients.Remove(client)
				emitClientsCount()

			case <-ctx.Done():
				return
			}
		}
	}()

	controller.Dirwatches.Start(controller)

	return nil
}

// RestartTranscriptionQueue restarts the transcription queue with updated settings
func (controller *Controller) RestartTranscriptionQueue() {
	// Stop existing queue if it exists
	if controller.TranscriptionQueue != nil {
		controller.Logs.LogEvent(LogLevelInfo, "stopping existing transcription queue for configuration update")
		controller.TranscriptionQueue.Stop()
		controller.TranscriptionQueue = nil
	}

	// Start new queue if transcription is enabled
	if controller.Options.TranscriptionConfig.Enabled {
		controller.Logs.LogEvent(LogLevelInfo, "starting transcription queue with updated configuration")
		controller.TranscriptionQueue = NewTranscriptionQueue(controller, controller.Options.TranscriptionConfig)
	} else {
		controller.Logs.LogEvent(LogLevelInfo, "transcription is disabled in config")
	}
}

// readAllData reads all data from the database in a single function for better organization
func (controller *Controller) readAllData() error {
	// Read all data in parallel for better performance
	var wg sync.WaitGroup
	errChan := make(chan error, 10)

	readFunc := func(fn func() error, name string) {
		defer wg.Done()
		if err := fn(); err != nil {
			errChan <- fmt.Errorf("failed to read %s: %v", name, err)
		}
	}

	wg.Add(12)
	go readFunc(func() error { return controller.Apikeys.Read(controller.Database) }, "apikeys")
	go readFunc(func() error { return controller.Dirwatches.Read(controller.Database) }, "dirwatches")
	go readFunc(func() error { return controller.Downstreams.Read(controller.Database) }, "downstreams")
	go readFunc(func() error { return controller.Groups.Read(controller.Database) }, "groups")
	go readFunc(func() error { return controller.Options.Read(controller.Database) }, "options")
	go readFunc(func() error {
		return controller.Systems.Read(controller.Database)
	}, "systems")
	go readFunc(func() error { return controller.Tags.Read(controller.Database) }, "tags")
	go readFunc(func() error { return controller.Users.Read(controller.Database) }, "users")
	go readFunc(func() error { return controller.UserGroups.Load(controller.Database) }, "userGroups")
	go readFunc(func() error { return controller.RegistrationCodes.Load(controller.Database) }, "registrationCodes")
	go readFunc(func() error { return controller.TransferRequests.Load(controller.Database) }, "transferRequests")
	go readFunc(func() error { return controller.DeviceTokens.Load(controller.Database) }, "deviceTokens")

	// Wait for all reads to complete
	wg.Wait()
	close(errChan)

	// Check for any errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	// Check for duplicate emails and log them
	controller.checkDuplicateEmails()

	// Update reconnection manager settings from options
	if controller.ReconnectionMgr != nil {
		controller.ReconnectionMgr.HoldDuration = time.Duration(controller.Options.ReconnectionGracePeriod) * time.Second
		controller.ReconnectionMgr.MaxBufferSize = int(controller.Options.ReconnectionMaxBufferSize)
		controller.ReconnectionMgr.Enabled = controller.Options.ReconnectionEnabled
		log.Printf("[ReconnectionManager] Configured - Enabled: %v, Grace Period: %ds, Max Buffer: %d",
			controller.ReconnectionMgr.Enabled,
			controller.Options.ReconnectionGracePeriod,
			controller.Options.ReconnectionMaxBufferSize)
	}

	return nil
}

// Helper method to check if user has access to a call (uses group settings if available)
func (controller *Controller) userHasAccess(user *User, call *Call) bool {
	if user == nil || call == nil || call.System == nil {
		return true
	}

	// Check group access first if user has a group
	if user.UserGroupId > 0 {
		group := controller.UserGroups.Get(user.UserGroupId)
		if group != nil {
			// Check if group has access to the system
			if !group.HasSystemAccess(uint64(call.System.SystemRef)) {
				return false
			}

			// CRITICAL: Also check talkgroup-level access if talkgroup exists
			if call.Talkgroup != nil {
				if !group.HasTalkgroupAccess(uint64(call.System.SystemRef), call.Talkgroup.TalkgroupRef) {
					return false
				}
			}

			// Group allows this system and talkgroup
			// Still check user-level restrictions (user can be more restrictive than group)
		}
	}

	// Check user-level access (can further restrict access beyond group)
	return user.HasAccess(call)
}

// Helper method to get effective delay for a user (uses group settings if available)
func (controller *Controller) userEffectiveDelay(user *User, call *Call, defaultDelay uint) uint {
	if user == nil || call == nil || call.System == nil || call.Talkgroup == nil {
		return defaultDelay
	}

	// Check group delays first if user has a group
	if user.UserGroupId > 0 {
		group := controller.UserGroups.Get(user.UserGroupId)
		if group != nil {
			groupDelay := group.EffectiveDelay(call, defaultDelay)
			// If group has a delay, use it (group settings override user settings)
			if groupDelay != defaultDelay || group.Delay > 0 || len(group.systemDelaysMap) > 0 || len(group.talkgroupDelaysMap) > 0 {
				return groupDelay
			}
		}
	}

	// Fall back to user-level delays
	return user.EffectiveDelay(call, defaultDelay)
}

// Helper method to get effective connection limit for a user (uses group settings if available)
func (controller *Controller) userEffectiveConnectionLimit(user *User) uint {
	if user == nil {
		return 0
	}

	// Check group connection limit first if user has a group
	if user.UserGroupId > 0 {
		group := controller.UserGroups.Get(user.UserGroupId)
		if group != nil && group.ConnectionLimit > 0 {
			// Group connection limit overrides user limit
			return group.ConnectionLimit
		}
	}

	// Fall back to user-level connection limit
	return user.ConnectionLimit
}

func (controller *Controller) fetchRadioReferenceAPIKey() {
	// Hardcoded relay server URL
	relayServerURL := "https://tlradioserver.thinlineds.com"

	// Get auth key using the same method as relay server
	authKey := getRelayServerAuthKey()

	// Fetch API key from relay server
	url := fmt.Sprintf("%s/api/radio-reference-api-key", relayServerURL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to create request for Radio Reference API key: %v", err))
		return
	}

	req.Header.Set("X-Rdio-Auth", authKey)

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to fetch Radio Reference API key from relay server: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("relay server returned error %d when fetching Radio Reference API key: %s", resp.StatusCode, string(body)))
		return
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to decode Radio Reference API key response: %v", err))
		return
	}

	if apiKey, ok := result["api_key"].(string); ok && apiKey != "" {
		controller.Options.RadioReferenceAPIKey = apiKey
		if err := controller.Options.Write(controller.Database); err != nil {
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to save Radio Reference API key to database: %v", err))
			return
		}
	} else {
		controller.Logs.LogEvent(LogLevelWarn, "Radio Reference API key not found in relay server response")
	}
}

// SyncConfigToFile syncs the current configuration to a file if config sync is enabled
// This is used for Google Drive sync and other file-based sync solutions
func (controller *Controller) SyncConfigToFile() {
	if !controller.Options.ConfigSyncEnabled {
		return
	}
	if controller.Options.ConfigSyncPath == "" {
		return
	}

	// Get current config (this matches the format expected by import)
	config := controller.Admin.GetConfig()

	// Marshal to JSON (same format as manual export)
	configJSON, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal config for sync: %v", err)
		return
	}

	// Ensure directory exists
	syncPath := controller.Options.ConfigSyncPath
	if err := os.MkdirAll(syncPath, 0755); err != nil {
		log.Printf("Failed to create sync directory %s: %v", syncPath, err)
		return
	}

	// Write to temp file first (atomic write)
	fileName := filepath.Join(syncPath, "ThinLineRadioV7-config.json")
	tempFileName := fileName + ".tmp"

	// Write to temp file
	if err := os.WriteFile(tempFileName, configJSON, 0644); err != nil {
		log.Printf("Failed to write config to temp file %s: %v", tempFileName, err)
		return
	}

	// Atomic rename (overwrites existing file)
	if err := os.Rename(tempFileName, fileName); err != nil {
		log.Printf("Failed to rename temp file to %s: %v", fileName, err)
		// Clean up temp file on error
		os.Remove(tempFileName)
		return
	}

	log.Printf("Config synced to %s", fileName)
}

func (controller *Controller) Terminate() {
	// Cancel worker context to signal workers to stop
	if controller.workerCancel != nil {
		controller.workerCancel()
		log.Println("Worker context cancelled, waiting for workers to finish...")

		// Wait for workers to finish with a timeout
		done := make(chan struct{})
		go func() {
			controller.workersWg.Wait()
			close(done)
		}()

		select {
		case <-done:
			log.Println("All workers finished gracefully")
		case <-time.After(10 * time.Second):
			log.Println("Worker shutdown timeout reached (10s), proceeding with shutdown")
		}
	}

	controller.Dirwatches.Stop()

	// Stop transcription queue
	if controller.TranscriptionQueue != nil {
		log.Println("Stopping transcription queue...")
		controller.TranscriptionQueue.Stop()
		log.Println("Transcription queue stopped")
	}

	// Close debug logger (give async audio saves a moment to finish)
	if controller.DebugLogger != nil {
		time.Sleep(500 * time.Millisecond) // Brief pause for pending audio writes
		controller.DebugLogger.Close()
		log.Println("Debug logger closed")
	}

	// Close transcription debug logger
	if controller.TranscriptionDebugLogger != nil {
		controller.TranscriptionDebugLogger.Close()
		log.Println("Transcription debug logger closed")
	}

	if err := controller.Database.Sql.Close(); err != nil {
		log.Println(err)
	}

	log.Println("Controller terminated gracefully")
}
