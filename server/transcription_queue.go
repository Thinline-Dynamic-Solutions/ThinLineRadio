// Copyright (C) 2025 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT EVEN THE IMPLIED WARRANTY OF MERCHANTABILITY or FITNESS
// FOR A PARTICULAR PURPOSE.  See the GNU General Public License for
// more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// TranscriptionJob represents a job in the transcription queue
type TranscriptionJob struct {
	CallId      uint64
	Audio       []byte
	AudioMime   string
	SystemId    uint64
	TalkgroupId uint64
	Priority    int // Higher priority processed first
	Reasons     []string
}

// TranscriptionQueue manages transcription jobs with a worker pool
type TranscriptionQueue struct {
	jobs       chan TranscriptionJob
	workers    int
	provider   TranscriptionProvider
	controller *Controller
	mutex      sync.Mutex
	running    bool
}

// NewTranscriptionQueue creates a new transcription queue with worker pool
func NewTranscriptionQueue(controller *Controller, config TranscriptionConfig) *TranscriptionQueue {
	queue := &TranscriptionQueue{
		jobs:       make(chan TranscriptionJob, 100), // Buffer 100 jobs
		workers:    config.WorkerPoolSize,
		controller: controller,
		running:    true,
	}
	
	if queue.workers == 0 {
		queue.workers = 5 // Default worker pool size
	}
	
	// Initialize provider based on config
	switch config.Provider {
	case "whisper-api":
		// External OpenAI-compatible Whisper API server
		queue.provider = NewWhisperAPITranscription(&WhisperAPIConfig{
			BaseURL: config.WhisperAPIURL,
			APIKey:  config.WhisperAPIKey,
		})
	case "azure":
		// Azure Speech Services
		queue.provider = NewAzureTranscription(&AzureConfig{
			APIKey: config.AzureKey,
			Region: config.AzureRegion,
		})
	case "google":
		// Google Cloud Speech-to-Text
		queue.provider = NewGoogleTranscription(&GoogleConfig{
			APIKey:      config.GoogleAPIKey,
			Credentials: config.GoogleCredentials,
		})
	case "assemblyai":
		// AssemblyAI
		queue.provider = NewAssemblyAITranscription(&AssemblyAIConfig{
			APIKey: config.AssemblyAIKey,
		})
	default:
		// Default to whisper-api
		if config.WhisperAPIURL == "" {
			config.WhisperAPIURL = "http://localhost:8000"
		}
		queue.provider = NewWhisperAPITranscription(&WhisperAPIConfig{
			BaseURL: config.WhisperAPIURL,
			APIKey:  config.WhisperAPIKey,
		})
	}
	
	// Start worker pool
	if queue.provider.IsAvailable() {
		for i := 0; i < queue.workers; i++ {
			go queue.worker(i)
		}
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription queue started with %d workers using provider: %s", queue.workers, queue.provider.GetName()))
	} else {
		providerName := queue.provider.GetName()
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("transcription provider '%s' not available, queue will not process jobs", providerName))
		controller.Logs.LogEvent(LogLevelWarn, "Make sure your transcription provider is properly configured and accessible")
	}
	
	return queue
}

// QueueJob adds a job to the transcription queue
func (queue *TranscriptionQueue) QueueJob(job TranscriptionJob) {
	if !queue.running {
		return
	}
	
	select {
	case queue.jobs <- job:
		// Job queued successfully
		queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription job queued for call %d (priority: %d)", job.CallId, job.Priority))
	default:
		// Queue is full, log warning
		queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("transcription queue full, dropping call %d", job.CallId))
	}
}

// worker processes transcription jobs
func (queue *TranscriptionQueue) worker(workerId int) {
	for job := range queue.jobs {
		if !queue.running {
			return
		}
		
		startTime := time.Now()
		queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d starting call %d", workerId, job.CallId))
		
		// Update call status to processing
		queue.updateCallTranscriptionStatus(job.CallId, "processing")
		
		// Get the call to check if it has detected tones
		call, err := queue.controller.Calls.GetCall(job.CallId)
		audioToTranscribe := job.Audio
		usedFilteredAudio := false
		
		// LOCK PENDING TONES: Prevent new tones from merging while this call transcribes
		// This prevents unrelated tones (from a different incident) from being attached to this voice call
		if call != nil && call.System != nil && call.Talkgroup != nil {
			key := fmt.Sprintf("%d:%d", call.System.Id, call.Talkgroup.Id)
			queue.controller.pendingTonesMutex.Lock()
			if pending, exists := queue.controller.pendingTones[key]; exists && pending != nil && !pending.Locked {
				pending.Locked = true
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d: locked pending tones for talkgroup %d (call %d transcribing)", workerId, call.Talkgroup.TalkgroupRef, job.CallId))
			}
			queue.controller.pendingTonesMutex.Unlock()
		}
		
		if err == nil && call != nil && call.ToneSequence != nil && len(call.ToneSequence.Tones) > 0 {
			// Call has detected tones - filter them out before transcription
			queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d: call %d has %d detected tones, filtering audio before transcription", workerId, job.CallId, len(call.ToneSequence.Tones)))
			
			// Calculate how much audio will remain after filtering
			totalAudioDuration, durationErr := queue.controller.getAudioDuration(job.Audio, job.AudioMime)
			totalToneDuration := 0.0
			for _, tone := range call.ToneSequence.Tones {
				totalToneDuration += tone.Duration
			}
			remainingDuration := totalAudioDuration - totalToneDuration
			
			// Only filter if we'll have meaningful audio left (at least 2 seconds)
			const minRemainingDuration = 2.0
			if durationErr == nil && remainingDuration < minRemainingDuration {
				// Not enough audio left after removing tones - skip transcription entirely
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d: call %d is mostly tones (%.1fs tones, %.1fs remaining < %.1fs minimum), skipping transcription", 
					workerId, job.CallId, totalToneDuration, remainingDuration, minRemainingDuration))
				
				// Mark as completed with empty transcript (tone-only call)
				queue.updateCallTranscriptionStatus(job.CallId, "completed")
				emptyResult := &TranscriptionResult{
					Transcript: "",
					Confidence: 0.0,
					Language:   queue.controller.Options.TranscriptionConfig.Language,
				}
				go queue.storeTranscription(job.CallId, emptyResult)
				
				duration := time.Since(startTime)
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d skipped call %d in %v (tone-only)", workerId, job.CallId, duration))
				continue
			}
			
			filteredAudio, filterErr := queue.controller.ToneDetector.RemoveTonesFromAudio(job.Audio, job.AudioMime, call.ToneSequence.Tones)
			if filterErr != nil {
				// Filtering failed, use original audio
				queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("transcription worker %d: audio filtering failed for call %d: %v, using original audio", workerId, job.CallId, filterErr))
			} else if len(filteredAudio) >= 1000 {
				// Filtering succeeded, use filtered audio
				audioToTranscribe = filteredAudio
				usedFilteredAudio = true
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d: using filtered audio for call %d (removed %.1fs of tones, %.1fs remaining)", 
					workerId, job.CallId, totalToneDuration, remainingDuration))
			} else {
				// Filtered audio too small (probably all tones, no voice)
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d: filtered audio too small for call %d, using original", workerId, job.CallId))
			}
		}
		
		// Transcribe audio (filtered if tones were present, original otherwise)
		result, err := queue.provider.Transcribe(audioToTranscribe, TranscriptionOptions{
			Language:      queue.controller.Options.TranscriptionConfig.Language,
			InitialPrompt: queue.controller.Options.TranscriptionConfig.Prompt,
			AudioMime:     job.AudioMime,
		})
		
		if err != nil {
			errorMsg := err.Error()
			queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("transcription worker %d failed for call %d after retries: %v", workerId, job.CallId, err))
			queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("transcription debug: apiURL=%s, usedFilteredAudio=%v, error=%s", queue.controller.Options.TranscriptionConfig.WhisperAPIURL, usedFilteredAudio, errorMsg))
			
			// Check if this is a connection-related error that might indicate server issues
			if strings.Contains(strings.ToLower(errorMsg), "connection") || 
			   strings.Contains(strings.ToLower(errorMsg), "eof") {
				queue.controller.Logs.LogEvent(LogLevelWarn, "Connection error detected. Check if Whisper API server is overloaded or network is unstable")
			}
			
			queue.updateCallTranscriptionStatus(job.CallId, "failed", errorMsg)
			continue
		}
		
		// Clean the transcript of hallucinations before storing and processing
		cleanedTranscript, hadHallucinations := queue.controller.cleanTranscript(result.Transcript, job.CallId)
		
		// Store cleaned transcription result
		cleanedResult := &TranscriptionResult{
			Transcript: cleanedTranscript,
			Confidence: result.Confidence,
			Language:   result.Language,
		}
		go queue.storeTranscription(job.CallId, cleanedResult)
		
		// After transcription completes, check if we should attach pending tones to this call
		// or if this call has its own tones with voice (trigger alert)
		go func() {
			// Load the call to check for pending tones
			call, err := queue.controller.Calls.GetCall(job.CallId)
			if err == nil && call != nil {
				// Update call with cleaned transcript
				call.Transcript = cleanedTranscript
				call.TranscriptionStatus = "completed"
				
				// Check if this call has actual voice (not just tones being transcribed)
				hasVoice := queue.controller.isActualVoice(cleanedTranscript)
				
			// Track this phrase for hallucination detection (if enabled)
			// Track with the original transcript before cleaning to catch hallucinations
			if call.System != nil && queue.controller.HallucinationDetector != nil {
				queue.controller.HallucinationDetector.TrackPhrase(result.Transcript, hasVoice, call.System.Id)
			}

			// Debug log voice check result with call ID - ONLY for tone-enabled talkgroups
			if queue.controller.DebugLogger != nil && call.Talkgroup != nil && call.Talkgroup.ToneDetectionEnabled {
				logMsg := "Transcription completed - voice detected"
				if hadHallucinations {
					logMsg += " (after cleaning hallucinations)"
				}
				
				if hasVoice {
					queue.controller.DebugLogger.LogVoiceDetection(job.CallId, cleanedTranscript, true, logMsg)
					// Save audio file labeled as voice
					go queue.controller.DebugLogger.SaveAudioFile(job.CallId, job.Audio, job.AudioMime, "voice")
				} else {
					queue.controller.DebugLogger.LogVoiceDetection(job.CallId, cleanedTranscript, false, "Transcription completed - rejected as not voice")
				}
			}
				
				if hasVoice {
					// Reload call from DB to get latest HasTones state
					// (may have been updated by tone detection earlier)
					dbCall, err := queue.controller.Calls.GetCall(job.CallId)
					if err == nil && dbCall != nil {
						call = dbCall
						call.Transcript = cleanedTranscript
						call.TranscriptionStatus = "completed"
					}
					
					// Check for pending tones from previous tone-only calls (from other calls)
					attachedPending := queue.controller.checkAndAttachPendingTones(call)
					
					if attachedPending {
						// Pending tones from a different call were attached - trigger tone alerts
						go queue.controller.AlertEngine.TriggerToneAlerts(call)
					} else if call.HasTones {
						// This call has its own tones (from this same call or already attached)
						// Trigger alert for this voice call with tones
						go queue.controller.AlertEngine.TriggerToneAlerts(call)
					}
				} else {
					// No voice - if this call has tones, they should have been stored as pending earlier
					// No alert needed for tone-only calls
					queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription completed for call %d: no voice detected (tone-only), no alert created", job.CallId))
				}
			}
		}()
		
		// Process keywords if needed - use cleaned transcript
		go queue.processKeywords(job.CallId, job.SystemId, job.TalkgroupId, cleanedResult)
		
		duration := time.Since(startTime)
		queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d completed call %d in %v (confidence: %.2f)", workerId, job.CallId, duration, result.Confidence))
	}
}

// updateCallTranscriptionStatus updates the transcription status for a call
func (queue *TranscriptionQueue) updateCallTranscriptionStatus(callId uint64, status string, failureReason ...string) {
	var query string
	if status == "failed" && len(failureReason) > 0 && failureReason[0] != "" {
		// Store failure reason when status is failed
		reason := escapeQuotes(failureReason[0])
		// Truncate to reasonable length (500 chars)
		if len(reason) > 500 {
			reason = reason[:500]
		}
		query = fmt.Sprintf(`UPDATE "calls" SET "transcriptionStatus" = '%s', "transcriptionFailureReason" = '%s' WHERE "callId" = %d`, escapeQuotes(status), reason, callId)
	} else {
		// Clear failure reason when status is not failed
		query = fmt.Sprintf(`UPDATE "calls" SET "transcriptionStatus" = '%s', "transcriptionFailureReason" = '' WHERE "callId" = %d`, escapeQuotes(status), callId)
	}
	if _, err := queue.controller.Database.Sql.Exec(query); err != nil {
		queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to update transcription status for call %d: %v", callId, err))
	}
}

// storeTranscription stores the transcription result in the database
func (queue *TranscriptionQueue) storeTranscription(callId uint64, result *TranscriptionResult) {
	if result == nil {
		return
	}
	
	// Update call table
	transcript := strings.ToUpper(result.Transcript) // Ensure ALL CAPS
	query := fmt.Sprintf(`UPDATE "calls" SET "transcript" = $1, "transcriptConfidence" = %.2f, "transcriptionStatus" = 'completed' WHERE "callId" = %d`, result.Confidence, callId)
	if queue.controller.Database.Config.DbType == DbTypePostgresql {
		_, err := queue.controller.Database.Sql.Exec(query, transcript)
		if err != nil {
			queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to update call transcript: %v", err))
		}
	}
	
	// Store detailed transcription (optional, for history)
	insertQuery := fmt.Sprintf(`INSERT INTO "transcriptions" ("callId", "transcript", "confidence", "language", "createdAt") VALUES (%d, $1, %.2f, '%s', %d)`, callId, result.Confidence, result.Language, time.Now().UnixMilli())
	if queue.controller.Database.Config.DbType == DbTypePostgresql {
		_, err := queue.controller.Database.Sql.Exec(insertQuery, transcript)
		if err != nil {
			queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to insert transcription record: %v", err))
		}
	} else {
		insertQuery = fmt.Sprintf(`INSERT INTO "transcriptions" ("callId", "transcript", "confidence", "language", "createdAt") VALUES (%d, ?, %.2f, '%s', %d)`, callId, result.Confidence, result.Language, time.Now().UnixMilli())
		_, err := queue.controller.Database.Sql.Exec(insertQuery, transcript)
		if err != nil {
			queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to insert transcription record: %v", err))
		}
	}
}

// processKeywords processes keywords after transcription completes
// OPTIMIZED: Loads users once, caches keyword lists, runs matching once per unique keyword set
func (queue *TranscriptionQueue) processKeywords(callId uint64, systemId uint64, talkgroupId uint64, result *TranscriptionResult) {
	if result == nil || result.Transcript == "" {
		queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("keyword processing skipped for call %d: no transcript", callId))
		return
	}
	
	// Skip keyword processing if transcript is tone-only (no actual voice)
	// This saves processing on tone-only calls while still allowing transcription to complete
	if !queue.controller.isActualVoice(result.Transcript) {
		queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("keyword processing skipped for call %d: tone-only transcript", callId))
		return
	}
	
	queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("processing keywords for call %d (system=%d, talkgroup=%d)", callId, systemId, talkgroupId))
	
	// Get all users with keyword alerts enabled for this talkgroup
	query := fmt.Sprintf(`SELECT "userId", "keywords", "keywordListIds" FROM "userAlertPreferences" WHERE "systemId" = %d AND "talkgroupId" = %d AND "alertEnabled" = true AND "keywordAlerts" = true`, systemId, talkgroupId)
	rows, err := queue.controller.Database.Sql.Query(query)
	if err != nil {
		queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to query user alert preferences: %v", err))
		return
	}
	defer rows.Close()
	
	transcript := strings.ToUpper(result.Transcript) // Ensure ALL CAPS
	
	// Step 1: Collect all users and their keyword preferences
	type userKeywords struct {
		userId         uint64
		keywords       []string
		keywordListIds []uint64
	}
	var users []userKeywords
	
	for rows.Next() {
		var (
			userId         uint64
			keywordsJson   string
			keywordListIds string
		)
		
		if err := rows.Scan(&userId, &keywordsJson, &keywordListIds); err != nil {
			continue
		}
		
		user := userKeywords{userId: userId}
		
		// Parse user's personal keywords
		if keywordsJson != "" && keywordsJson != "[]" {
			json.Unmarshal([]byte(keywordsJson), &user.keywords)
		}
		
		// Parse user's keyword list IDs
		if keywordListIds != "" && keywordListIds != "[]" {
			json.Unmarshal([]byte(keywordListIds), &user.keywordListIds)
		}
		
		users = append(users, user)
	}
	
	if len(users) == 0 {
		queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("no users with keyword alerts enabled for call %d (system=%d, talkgroup=%d)", callId, systemId, talkgroupId))
		return
	}
	
	// Step 2: Cache keyword lists (load each list only once)
	keywordListCache := make(map[uint64][]string)
	for _, user := range users {
		for _, listId := range user.keywordListIds {
			if _, exists := keywordListCache[listId]; !exists {
				listKeywords := queue.getKeywordsFromList(listId)
				keywordListCache[listId] = listKeywords
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("cached %d keywords from list %d", len(listKeywords), listId))
			}
		}
	}
	
	// Step 3: Group users by their unique keyword sets (to avoid duplicate matching)
	// Create a signature for each user's complete keyword set
	type keywordSetSignature string
	type keywordGroup struct {
		keywords []string
		userIds  []uint64
	}
	keywordGroups := make(map[keywordSetSignature]*keywordGroup)
	
	for _, user := range users {
		// Build complete keyword list for this user
		allKeywords := make([]string, 0, len(user.keywords))
		allKeywords = append(allKeywords, user.keywords...)
		
		// Add keywords from lists
		for _, listId := range user.keywordListIds {
			if listKeywords, exists := keywordListCache[listId]; exists {
				allKeywords = append(allKeywords, listKeywords...)
			}
		}
		
		// Create signature (sorted list IDs + personal keywords for grouping)
		signature := keywordSetSignature(fmt.Sprintf("%v:%v", user.keywordListIds, user.keywords))
		
		if group, exists := keywordGroups[signature]; exists {
			// Same keyword set - add user to existing group
			group.userIds = append(group.userIds, user.userId)
		} else {
			// New keyword set - create new group
			keywordGroups[signature] = &keywordGroup{
				keywords: allKeywords,
				userIds:  []uint64{user.userId},
			}
		}
	}
	
	queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("optimized keyword matching: %d users grouped into %d unique keyword sets", len(users), len(keywordGroups)))
	
	// Step 4: Run matching once per unique keyword set, distribute to all users in group
	for _, group := range keywordGroups {
		queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("checking %d keywords for %d users against transcript", len(group.keywords), len(group.userIds)))
		
		// Match keywords ONCE for this group
		matches := queue.controller.KeywordMatcher.MatchKeywords(transcript, group.keywords)

		// Debug log keyword matches
		if queue.controller.DebugLogger != nil {
			for _, match := range matches {
				queue.controller.DebugLogger.LogKeywordMatch(callId, match.Keyword, result.Transcript)
			}
		}
		
		if len(matches) > 0 {
			// Get system and talkgroup labels once for the batch
			var systemLabel, talkgroupLabel string
			if system, ok := queue.controller.Systems.GetSystemById(systemId); ok {
				systemLabel = system.Label
				if talkgroup, ok := system.Talkgroups.GetTalkgroupById(talkgroupId); ok {
					talkgroupLabel = talkgroup.Label
				}
			}

			// Build keywords matched list
			keywordsMatched := make([]string, len(matches))
			for i, match := range matches {
				keywordsMatched[i] = match.Keyword
			}

			// Check if there are pending tones for this talkgroup
			key := fmt.Sprintf("%d:%d", systemId, talkgroupId)
			queue.controller.pendingTonesMutex.Lock()
			hasPendingTones := false
			if pending, exists := queue.controller.pendingTones[key]; exists && pending != nil {
				// Check if pending tones are still valid (within time window)
				now := time.Now().UnixMilli()
				ageMinutes := float64(now-pending.Timestamp) / (1000.0 * 60.0)
				if ageMinutes <= float64(pendingToneTimeoutMinutes) {
					hasPendingTones = true
					queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("found pending tones for key %s (age: %.2f minutes) when processing keywords for call %d", key, ageMinutes, callId))
				} else {
					queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("pending tones for key %s expired (age: %.2f minutes > %d minutes)", key, ageMinutes, pendingToneTimeoutMinutes))
				}
			} else {
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("no pending tones found for key %s when processing keywords for call %d", key, callId))
			}
			queue.controller.pendingTonesMutex.Unlock()

			// Distribute matches to ALL users in this group
			var eligibleUserIds []uint64
			var usersWithToneAlerts []uint64 // Users who have both keyword and tone alerts enabled
			
			for _, userId := range group.userIds {
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("found %d keyword matches for user %d on call %d", len(matches), userId, callId))
				
				// Create keyword matches in database
				for _, match := range matches {
					match.UserId = userId
					match.CallId = callId
					queue.storeKeywordMatch(&match)
				}
				
				// Trigger alerts (creates DB records and WebSocket notifications)
				go queue.controller.AlertEngine.TriggerKeywordAlerts(callId, systemId, talkgroupId, userId, matches, result)
				
				// Check if user has tone alerts enabled for this talkgroup
				// If pending tones exist and user has tone alerts, skip keyword push notification
				// (tone alert will be sent when tones are attached)
				shouldSendKeywordAlert := true
				if hasPendingTones {
					var toneAlertsQuery string
					if queue.controller.Database.Config.DbType == DbTypePostgresql {
						toneAlertsQuery = `SELECT COUNT(*) FROM "userAlertPreferences" WHERE "userId" = $1 AND "systemId" = $2 AND "talkgroupId" = $3 AND "alertEnabled" = true AND "toneAlerts" = true`
					} else {
						toneAlertsQuery = `SELECT COUNT(*) FROM "userAlertPreferences" WHERE "userId" = ? AND "systemId" = ? AND "talkgroupId" = ? AND "alertEnabled" = true AND "toneAlerts" = true`
					}
					var count uint64
					if err := queue.controller.Database.Sql.QueryRow(toneAlertsQuery, userId, systemId, talkgroupId).Scan(&count); err == nil && count > 0 {
						// User has both keyword and tone alerts enabled, and pending tones exist
						// Skip keyword push notification - tone alert will be sent instead
						shouldSendKeywordAlert = false
						usersWithToneAlerts = append(usersWithToneAlerts, userId)
						queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("skipping keyword push notification for user %d on call %d (pending tones exist, tone alert will be sent instead)", userId, callId))
					}
				}
				
				// Collect user for batched push notification (only if not skipping)
				if shouldSendKeywordAlert {
					eligibleUserIds = append(eligibleUserIds, userId)
				}
			}

			// Send batched push notification for users who should get keyword alerts
			if len(eligibleUserIds) > 0 {
				// Fetch call to get transcript
				call, err := queue.controller.Calls.GetCall(callId)
				if err != nil {
					queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to get call %d for push notification: %v", callId, err))
					call = nil // Continue without call object
				}
				go queue.controller.sendBatchedPushNotification(eligibleUserIds, "keyword", call, systemLabel, talkgroupLabel, "", keywordsMatched)
			}
			
			// Log users who will get tone alerts instead
			if len(usersWithToneAlerts) > 0 {
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("deferred keyword alerts for %d user(s) on call %d (will receive tone alerts with keyword info instead)", len(usersWithToneAlerts), callId))
			}
		} else {
			for _, userId := range group.userIds {
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("no keyword matches for user %d on call %d", userId, callId))
			}
		}
	}
}

// getKeywordsFromList retrieves keywords from a keyword list
func (queue *TranscriptionQueue) getKeywordsFromList(listId uint64) []string {
	query := fmt.Sprintf(`SELECT "keywords" FROM "keywordLists" WHERE "keywordListId" = %d`, listId)
	var keywordsJson string
	if err := queue.controller.Database.Sql.QueryRow(query).Scan(&keywordsJson); err != nil {
		return []string{}
	}
	
	var keywords []string
	if keywordsJson != "" && keywordsJson != "[]" {
		json.Unmarshal([]byte(keywordsJson), &keywords)
	}
	
	return keywords
}

// storeKeywordMatch stores a keyword match in the database
func (queue *TranscriptionQueue) storeKeywordMatch(match *KeywordMatch) {
	query := fmt.Sprintf(`INSERT INTO "keywordMatches" ("callId", "userId", "keyword", "context", "position", "alerted") VALUES (%d, %d, $1, $2, %d, false)`, match.CallId, match.UserId, match.Position)
	if queue.controller.Database.Config.DbType == DbTypePostgresql {
		_, err := queue.controller.Database.Sql.Exec(query, match.Keyword, match.Context)
		if err != nil {
			queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to store keyword match: %v", err))
		}
	} else {
		query = fmt.Sprintf(`INSERT INTO "keywordMatches" ("callId", "userId", "keyword", "context", "position", "alerted") VALUES (%d, %d, ?, ?, %d, false)`, match.CallId, match.UserId, match.Position)
		_, err := queue.controller.Database.Sql.Exec(query, match.Keyword, match.Context)
		if err != nil {
			queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to store keyword match: %v", err))
		}
	}
}

// Stop stops the transcription queue
func (queue *TranscriptionQueue) Stop() {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	
	queue.running = false
	close(queue.jobs)
}

