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
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"rdio-scanner/server/mapping"
)

// TranscriptionJob represents a job in the transcription queue
type TranscriptionJob struct {
	CallId        uint64
	Audio         []byte // Converted audio (AAC) - kept for backward compatibility
	AudioMime     string // Converted audio mime type
	OriginalAudio []byte // Original raw audio before AAC conversion (preferred for transcription)
	OriginalMime  string // Original audio mime type
	SystemId      uint64
	TalkgroupId   uint64
	Priority      int // Higher priority processed first
	Reasons       []string
}

// TranscriptionQueue manages transcription jobs with a worker pool
type TranscriptionQueue struct {
	jobs            chan TranscriptionJob
	workers         int
	provider        TranscriptionProvider
	backupProvider  TranscriptionProvider
	backupName      string
	controller      *Controller
	mutex           sync.Mutex
	running         bool
	processedCount  atomic.Uint64 // total transcriptions completed since startup
}

// NewTranscriptionQueue creates a new transcription queue with worker pool
func NewTranscriptionQueue(controller *Controller, config TranscriptionConfig) *TranscriptionQueue {
	// Use configured worker pool size for all providers
	// WARNING: For Whisper API (local Whisper), using more than 1 worker may cause
	// transcription failures if insufficient VRAM is available. Users should start
	// with 1 worker and increase only if they have adequate resources (8GB+ VRAM).
	workerCount := config.WorkerPoolSize
	if workerCount == 0 {
		// Default to 1 for safety (can be increased by users with adequate resources)
		workerCount = 1
	}

	queue := &TranscriptionQueue{
		jobs:       make(chan TranscriptionJob, 100), // Buffer 100 jobs
		workers:    workerCount,
		controller: controller,
		running:    true,
	}

	// Initialize providers from config
	primaryName := resolvePrimaryProvider(config.Provider)
	if primaryName == "hydra" {
		// Hydra transcription uses a separate retrieval queue, not the transcription queue.
		queue.provider = NewWhisperAPITranscription(&WhisperAPIConfig{})
	} else {
		queue.provider = newTranscriptionProvider(config, primaryName)
		if queue.provider == nil {
			queue.provider = newTranscriptionProvider(config, "whisper-api")
		}
	}

	if backupName := resolveBackupProvider(config.Provider, config.BackupProvider); backupName != "" {
		if bp := newTranscriptionProvider(config, backupName); bp != nil && bp.IsAvailable() {
			queue.backupProvider = bp
			queue.backupName = backupName
		} else {
			name := backupName
			if bp != nil {
				name = bp.GetName()
			}
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("transcription backup provider '%s' is not available", name))
		}
	}

	// Start worker pool when primary or backup can run jobs
	primaryOK := queue.provider != nil && queue.provider.IsAvailable()
	backupOK := queue.backupProvider != nil && queue.backupProvider.IsAvailable()
	if primaryOK || backupOK {
		for i := 0; i < queue.workers; i++ {
			go queue.worker(i)
		}
		msg := fmt.Sprintf("transcription queue started with %d workers using provider: %s", queue.workers, queue.provider.GetName())
		if queue.backupProvider != nil {
			msg += fmt.Sprintf(" (backup: %s)", queue.backupProvider.GetName())
		}
		controller.Logs.LogEvent(LogLevelInfo, msg)
	} else {
		providerName := "unknown"
		if queue.provider != nil {
			providerName = queue.provider.GetName()
		}
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

func (queue *TranscriptionQueue) optionsForProvider(provider string, base TranscriptionOptions, resolvedPrompt string, promptSystem *System, promptTalkgroup *Talkgroup) TranscriptionOptions {
	opts := base
	switch provider {
	case "gemini":
		// Gemini has its own short base prompt + JSON schema. Do not also send
		// the Whisper-style "Transcribe…" custom prompt (duplicate / conflicting).
		opts.InitialPrompt = geminiExtraContext(resolvedPrompt)
		opts.ExtractAddress = false
		if promptSystem != nil {
			mapCfg := resolveIncidentMappingConfig(promptSystem, promptTalkgroup)
			opts.ExtractAddress = mapCfg.Enabled && mapCfg.ExtractAddressWithGemini
		}
	case "assemblyai":
		opts.SpeechModel = queue.controller.Options.TranscriptionConfig.AssemblyAISpeechModel
		wordBoost := append([]string{}, queue.controller.Options.TranscriptionConfig.AssemblyAIWordBoost...)
		if resolvedPrompt != queue.controller.Options.TranscriptionConfig.Prompt && resolvedPrompt != "" {
			for _, term := range strings.Fields(resolvedPrompt) {
				wordBoost = append(wordBoost, term)
			}
		}
		opts.WordBoost = wordBoost
	}
	return opts
}

// worker processes transcription jobs
func (queue *TranscriptionQueue) worker(workerId int) {
	for job := range queue.jobs {
		if !queue.running {
			return
		}

		startTime := time.Now()

		// Resolve system and talkgroup labels for richer log lines
		systemLabel := fmt.Sprintf("sys:%d", job.SystemId)
		talkgroupLabel := fmt.Sprintf("tg:%d", job.TalkgroupId)
		if sys, ok := queue.controller.Systems.GetSystemById(job.SystemId); ok {
			systemLabel = sys.Label
			if tg, ok := sys.Talkgroups.GetTalkgroupById(job.TalkgroupId); ok {
				talkgroupLabel = tg.Label
			}
		}

		queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
			"[transcription] worker %d | call %d | %s / %s | queued",
			workerId, job.CallId, systemLabel, talkgroupLabel,
		))

		// Get the call to check if it has detected tones
		call, err := queue.controller.Calls.GetCall(job.CallId)

		// Re-check MinCallDuration here — tone detection may have finished after
		// the job was queued, so remaining-after-tones can now fall below the floor.
		if call != nil {
			if len(job.OriginalAudio) > 0 {
				call.OriginalAudio = job.OriginalAudio
				call.OriginalAudioMime = job.OriginalMime
			} else if len(job.Audio) > 0 {
				call.Audio = job.Audio
				call.AudioMime = job.AudioMime
			}
			minDuration := queue.controller.Options.TranscriptionConfig.MinCallDuration
			if skip, reason, _, derr := queue.controller.transcriptionDurationDecision(call, minDuration); derr == nil && skip {
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
					"[transcription] worker %d | call %d | skipped before STT: %s",
					workerId, job.CallId, reason,
				))
				if strings.HasPrefix(reason, "tone_only_remaining") {
					queue.updateCallTranscriptionStatus(job.CallId, "completed")
				} else {
					queue.controller.markTranscriptionSkipped(job.CallId, reason)
					queue.updateCallTranscriptionStatus(job.CallId, "skipped")
				}
				continue
			}
		}

		// Update call status to processing
		queue.updateCallTranscriptionStatus(job.CallId, "processing")

		// Use original audio for transcription (avoids double lossy conversion MP3->AAC->WAV)
		// Falls back to converted audio if original is not available
		audioToTranscribe := job.OriginalAudio
		audioMimeType := job.OriginalMime
		if len(audioToTranscribe) == 0 {
			// Fallback to converted audio if original not available
			audioToTranscribe = job.Audio
			audioMimeType = job.AudioMime
		}
		usedFilteredAudio := false

		// LOCK PENDING TONES: Prevent new tones from merging while this call transcribes
		// This prevents unrelated tones (from a different incident) from being attached to this voice call
		unlockPendingTones := func() {
			if call == nil || call.System == nil || call.Talkgroup == nil {
				return
			}
			key := fmt.Sprintf("%d:%d", call.System.Id, call.Talkgroup.Id)
			queue.controller.pendingTonesMutex.Lock()
			if pending, exists := queue.controller.pendingTones[key]; exists && pending != nil && pending.Locked {
				pending.Locked = false
			}
			queue.controller.pendingTonesMutex.Unlock()
		}
		if call != nil && call.System != nil && call.Talkgroup != nil {
			key := fmt.Sprintf("%d:%d", call.System.Id, call.Talkgroup.Id)
			queue.controller.pendingTonesMutex.Lock()
			if pending, exists := queue.controller.pendingTones[key]; exists && pending != nil && !pending.Locked {
				pending.Locked = true
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d: locked pending tones for talkgroup %d (call %d transcribing)", workerId, call.Talkgroup.TalkgroupRef, job.CallId))
			}
			queue.controller.pendingTonesMutex.Unlock()
		}

		// UNIVERSAL TONE REMOVAL: detect and cut ALL sustained dispatch tones before STT
		// so Whisper/Gemini do not hallucinate on beeps. Skip STT when almost nothing remains.
		queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d: scanning call %d for dispatch tones to remove", workerId, job.CallId))
		detectedTones, detectErr := queue.controller.ToneDetector.DetectAllTonesForTranscription(audioToTranscribe, audioMimeType)
		if detectErr != nil {
			queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("transcription worker %d: tone detection failed for call %d: %v, proceeding with original audio", workerId, job.CallId, detectErr))
		} else if len(detectedTones) > 0 {
			queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d: call %d has %d dispatch tones, filtering before transcription", workerId, job.CallId, len(detectedTones)))

			totalAudioDuration, durationErr := queue.controller.getAudioDuration(audioToTranscribe, audioMimeType)
			var toneEnd float64
			totalToneDuration := 0.0
			for _, tone := range detectedTones {
				totalToneDuration += tone.Duration
				if tone.EndTime > toneEnd {
					toneEnd = tone.EndTime
				}
			}
			remainingDuration := totalAudioDuration - totalToneDuration
			if toneEnd > 0 {
				remainingDuration = totalAudioDuration - toneEnd
			}

			const minRemainingDuration = 2.0
			if durationErr == nil && remainingDuration < minRemainingDuration {
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d: call %d is mostly tones (%.1fs tones, %.1fs remaining < %.1fs minimum), skipping transcription",
					workerId, job.CallId, totalToneDuration, remainingDuration, minRemainingDuration))
				queue.updateCallTranscriptionStatus(job.CallId, "completed")
				emptyResult := &TranscriptionResult{
					Transcript: "",
					Confidence: 0.0,
					Language:   queue.controller.Options.TranscriptionConfig.Language,
				}
				go queue.storeTranscription(job.CallId, emptyResult)
				unlockPendingTones()
				duration := time.Since(startTime)
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
					"[transcription] worker %d | call %d | %s / %s | skipped tone-only in %.2fs | total #%d",
					workerId, job.CallId, systemLabel, talkgroupLabel, duration.Seconds(), queue.processedCount.Add(1),
				))
				continue
			}

			filteredAudio, filterErr := queue.controller.ToneDetector.RemoveTonesFromAudio(audioToTranscribe, audioMimeType, detectedTones)
			if filterErr != nil {
				queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("transcription worker %d: audio filtering failed for call %d: %v, using original audio", workerId, job.CallId, filterErr))
			} else if len(filteredAudio) >= 1000 {
				audioToTranscribe = filteredAudio
				audioMimeType = "audio/wav"
				usedFilteredAudio = true
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d: using filtered audio for call %d (removed %.1fs of tones, %.1fs voice remaining)",
					workerId, job.CallId, totalToneDuration, remainingDuration))
			} else {
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d: filtered audio too small for call %d (tone-only), skipping transcription", workerId, job.CallId))
				queue.updateCallTranscriptionStatus(job.CallId, "completed")
				emptyResult := &TranscriptionResult{
					Transcript: "",
					Confidence: 0.0,
					Language:   queue.controller.Options.TranscriptionConfig.Language,
				}
				go queue.storeTranscription(job.CallId, emptyResult)
				unlockPendingTones()
				duration := time.Since(startTime)
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
					"[transcription] worker %d | call %d | %s / %s | skipped tone-only filter in %.2fs | total #%d",
					workerId, job.CallId, systemLabel, talkgroupLabel, duration.Seconds(), queue.processedCount.Add(1),
				))
				continue
			}
		} else {
			queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d: no dispatch tones detected in call %d, using original audio", workerId, job.CallId))
		}

		// Resolve transcription prompt: talkgroup overrides system which overrides global.
		// An empty string at any level means "fall through to the next level".
		resolvedPrompt := queue.controller.Options.TranscriptionConfig.Prompt
		var promptSystem *System
		var promptTalkgroup *Talkgroup
		if system, ok := queue.controller.Systems.GetSystemById(job.SystemId); ok {
			promptSystem = system
			if system.TranscriptionPrompt != "" {
				resolvedPrompt = system.TranscriptionPrompt
			}
			if talkgroup, ok := system.Talkgroups.GetTalkgroupById(job.TalkgroupId); ok {
				promptTalkgroup = talkgroup
				if talkgroup.TranscriptionPrompt != "" {
					resolvedPrompt = talkgroup.TranscriptionPrompt
				}
			}
		}
		if queue.controller.Options.MappingIntegration.SendLocationContext && promptSystem != nil {
			var toneSeq *ToneSequence
			if call != nil {
				toneSeq = call.ToneSequence
			}
			resolvedPrompt = appendTranscriptionLocationContext(resolvedPrompt, promptSystem, promptTalkgroup, toneSeq)
		}

		// Transcribe audio (filtered if tones were present, original otherwise)
		baseOpts := TranscriptionOptions{
			Language:       queue.controller.Options.TranscriptionConfig.Language,
			InitialPrompt:  resolvedPrompt,
			AudioMime:      audioMimeType,
			SystemLabel:    systemLabel,
			TalkgroupLabel: talkgroupLabel,
			CallID:         job.CallId,
		}
		primaryName := resolvePrimaryProvider(queue.controller.Options.TranscriptionConfig.Provider)
		primaryOpts := queue.optionsForProvider(primaryName, baseOpts, resolvedPrompt, promptSystem, promptTalkgroup)
		backupOpts := baseOpts
		if queue.backupName != "" {
			backupOpts = queue.optionsForProvider(queue.backupName, baseOpts, resolvedPrompt, promptSystem, promptTalkgroup)
		}

		result, usedBackup, err := transcribeWithBackup(queue.provider, queue.backupProvider, audioToTranscribe, primaryOpts, backupOpts)
		if usedBackup {
			queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
				"transcription worker %d used backup provider %s for call %d after primary failure",
				workerId, queue.backupName, job.CallId,
			))
		}

		if err != nil {
			errorMsg := err.Error()
			queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("transcription worker %d failed for call %d after retries: %v", workerId, job.CallId, err))
			provider := queue.controller.Options.TranscriptionConfig.Provider
			if provider == "whisper-api" || provider == "" {
				queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("transcription debug: provider=%s, apiURL=%s, usedFilteredAudio=%v, error=%s", provider, queue.controller.Options.TranscriptionConfig.WhisperAPIURL, usedFilteredAudio, errorMsg))
			} else if queue.backupName != "" {
				queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("transcription debug: provider=%s, backup=%s, usedFilteredAudio=%v, error=%s", provider, queue.backupName, usedFilteredAudio, errorMsg))
			} else {
				queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("transcription debug: provider=%s, usedFilteredAudio=%v, error=%s", provider, usedFilteredAudio, errorMsg))
			}

			// Check if this is a connection-related error that might indicate server issues
			if strings.Contains(strings.ToLower(errorMsg), "connection") ||
				strings.Contains(strings.ToLower(errorMsg), "eof") {
				if provider == "whisper-api" || provider == "" {
					queue.controller.Logs.LogEvent(LogLevelWarn, "Connection error detected. Check if Whisper API server is overloaded or network is unstable")
				} else {
					queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("Connection error detected with %s provider. Check network connectivity or API service status", provider))
				}
			}

			queue.updateCallTranscriptionStatus(job.CallId, "failed", errorMsg)

			// Release the pending-tones lock so future voice calls can still attach tones.
			// Without this, a transcription failure would permanently lock the talkgroup's
			// pending tones until the server restarts.
			if call != nil && call.System != nil && call.Talkgroup != nil {
				key := fmt.Sprintf("%d:%d", call.System.Id, call.Talkgroup.Id)
				queue.controller.pendingTonesMutex.Lock()
				if pending, exists := queue.controller.pendingTones[key]; exists && pending != nil && pending.Locked {
					pending.Locked = false
					queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d: unlocked pending tones for talkgroup %d after transcription failure (call %d)", workerId, call.Talkgroup.TalkgroupRef, job.CallId))
				}
				queue.controller.pendingTonesMutex.Unlock()
			}

			continue
		}

		// Clean hallucinations, then plain-text normalize before store/alerts/
		// keywords/geocoding so every consumer sees the same shape.
		cleanedTranscript, hadHallucinations := queue.controller.cleanTranscript(result.Transcript, job.CallId)
		cleanedTranscript = mapping.NormalizeTranscriptPlainText(cleanedTranscript)
		filteredTranscript := queue.controller.applyTranscriptProfanityFilter(cleanedTranscript)
		if filteredTranscript != cleanedTranscript {
			queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("profanity filter applied to call %d transcript", job.CallId))
			cleanedTranscript = filteredTranscript
		}
		alertSummary := queue.controller.applyTranscriptProfanityFilter(strings.TrimSpace(result.AlertSummary))

		// Store cleaned transcription result (include optional summary from Whisper server when present)
		extractedAddr := mapping.NormalizeTranscriptPlainText(strings.TrimSpace(result.ExtractedAddress))
		cleanedResult := &TranscriptionResult{
			Transcript:       cleanedTranscript,
			Confidence:       result.Confidence,
			Language:         result.Language,
			AlertSummary:     alertSummary,
			ExtractedAddress: extractedAddr,
		}
		go queue.storeTranscription(job.CallId, cleanedResult)

		// Capture the pre-transcription call for the post-transcription goroutine.
		// Tone detection has almost certainly completed by the time transcription finishes,
		// so we re-fetch HasTones from DB only once (at the HasTones check below) rather
		// than fetching the entire call a second time here.
		postCall := call

		// After transcription completes, check if we should attach pending tones to this call
		// or if this call has its own tones with voice (trigger alert)
		go func() {
			// CRITICAL: Always unlock pending tones, even if we can't load the call from DB
			// Store job data to ensure unlock happens regardless of DB load success
			jobSystemId := job.SystemId
			jobTalkgroupId := job.TalkgroupId

			if postCall != nil {
				call := postCall
				// Update call with cleaned transcript
				call.Transcript = cleanedTranscript
				call.TranscriptionStatus = "completed"

				// Tone attach uses a lenient check (short dispatch); keywords keep isActualVoice.
				hasVoiceForTones := queue.controller.isVoiceForToneAlerts(cleanedTranscript)
				hasVoiceForKeywords := queue.controller.isActualVoice(cleanedTranscript)

				if hasVoiceForTones && !hasVoiceForKeywords {
					queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
						"call %d: short dispatch transcript accepted for tone alerts (%d words, %d chars)",
						job.CallId, len(strings.Fields(strings.TrimSpace(cleanedTranscript))), len(strings.TrimSpace(cleanedTranscript)),
					))
				}

				// Track this phrase for hallucination detection (if enabled)
				// Track with the original transcript before cleaning to catch hallucinations
				if call.System != nil && queue.controller.HallucinationDetector != nil {
					queue.controller.HallucinationDetector.TrackPhrase(result.Transcript, hasVoiceForKeywords, call.System.Id)
				}

				// Debug log voice check result with call ID - ONLY for tone-enabled talkgroups
				if queue.controller.DebugLogger != nil && call.Talkgroup != nil && call.Talkgroup.ToneDetectionEnabled {
					logMsg := "Transcription completed - voice detected for tone alerts"
					if hadHallucinations {
						logMsg += " (after cleaning hallucinations)"
					}

					if hasVoiceForTones {
						queue.controller.DebugLogger.LogVoiceDetection(job.CallId, cleanedTranscript, true, logMsg)
						// Save audio file labeled as voice
						go queue.controller.DebugLogger.SaveAudioFile(job.CallId, job.Audio, job.AudioMime, "voice")
					} else {
						queue.controller.DebugLogger.LogVoiceDetection(job.CallId, cleanedTranscript, false, "Transcription completed - rejected as not voice for tone alerts")
					}
				}

				if hasVoiceForTones {
					// Reload call from DB to get latest HasTones state
					// (may have been updated by tone detection earlier)
					dbCall, err := queue.controller.Calls.GetCall(job.CallId)
					if err == nil && dbCall != nil {
						call = dbCall
						call.Transcript = cleanedTranscript
						call.TranscriptionStatus = "completed"
					}

					if call.Talkgroup != nil && call.Talkgroup.AlertingTalkgroup {
						go queue.controller.AlertEngine.TriggerTranscriptAlerts(call)
					} else {
						// Check for pending tones from previous tone-only calls (from other calls)
						attachedPending := queue.controller.checkAndAttachPendingTones(call)

						if attachedPending {
							go queue.controller.AlertEngine.TriggerToneAlerts(call)
						} else if call.HasTones {
							go queue.controller.AlertEngine.TriggerToneAlerts(call)
						}
					}
				} else {
					// No voice on this clip — matched tones are stored as pending; DB alerts fire when a
					// later voice call attaches them or when the orphan timer fires (~60s).
					queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription completed for call %d: no voice detected (tone-only), no alert created", job.CallId))

					if dbCall, err := queue.controller.Calls.GetCall(job.CallId); err == nil && dbCall != nil && dbCall.HasTones {
						matched := 0
						if dbCall.ToneSequence != nil {
							matched = len(dbCall.ToneSequence.MatchedToneSets)
							if matched == 0 && dbCall.ToneSequence.MatchedToneSet != nil {
								matched = 1
							}
						}
						if matched > 0 {
							queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("call %d tone-only with %d matched tone set(s) on talkgroup %d — pending until voice or orphan alert", job.CallId, matched, call.Talkgroup.TalkgroupRef))
						}
					}
				}

				// UNLOCK PENDING TONES: Transcription is complete, allow new tones to merge
				// This is critical - if we don't unlock, the next voice call won't be able to attach pending tones
				if call.System != nil && call.Talkgroup != nil {
					key := fmt.Sprintf("%d:%d", call.System.Id, call.Talkgroup.Id)
					queue.controller.pendingTonesMutex.Lock()
					if pending, exists := queue.controller.pendingTones[key]; exists && pending != nil && pending.Locked {
						pending.Locked = false
						queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d: unlocked pending tones for talkgroup %d (call %d transcription complete, hasVoiceForTones=%t)", workerId, call.Talkgroup.TalkgroupRef, job.CallId, hasVoiceForTones))

						// If there are "next pending" tones that arrived during the lock, merge them now
						nextKey := key + ":next"
						if nextPending, nextExists := queue.controller.pendingTones[nextKey]; nextExists && nextPending != nil {
							queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("merging next pending tones into current pending for talkgroup %d (lock cleared after call %d)", call.Talkgroup.TalkgroupRef, job.CallId))

							queue.controller.mergeNextPendingIntoCurrent(key, pending, nextPending)

							// Clear next pending slot
							delete(queue.controller.pendingTones, nextKey)
						}
					}
					queue.controller.pendingTonesMutex.Unlock()
				}
			} else {
				// FALLBACK: If we couldn't load the call from DB, still unlock using job data
				// This prevents pending tones from being permanently locked
				if jobSystemId > 0 && jobTalkgroupId > 0 {
					key := fmt.Sprintf("%d:%d", jobSystemId, jobTalkgroupId)
					queue.controller.pendingTonesMutex.Lock()
					if pending, exists := queue.controller.pendingTones[key]; exists && pending != nil && pending.Locked {
						pending.Locked = false
						queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcription worker %d: unlocked pending tones for system:talkgroup %d:%d (call %d transcription complete, fallback unlock)", workerId, jobSystemId, jobTalkgroupId, job.CallId))

						// If there are "next pending" tones that arrived during the lock, merge them now
						nextKey := key + ":next"
						if nextPending, nextExists := queue.controller.pendingTones[nextKey]; nextExists && nextPending != nil {
							queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("merging next pending tones into current pending for system:talkgroup %d:%d (lock cleared after call %d, fallback)", jobSystemId, jobTalkgroupId, job.CallId))

							queue.controller.mergeNextPendingIntoCurrent(key, pending, nextPending)

							// Clear next pending slot
							delete(queue.controller.pendingTones, nextKey)
						}
					}
					queue.controller.pendingTonesMutex.Unlock()
				}
			}
		}()

		// Process keywords if needed - use cleaned transcript
		go queue.processKeywords(job.CallId, job.SystemId, job.TalkgroupId, cleanedResult)

		go func() {
			if queue.controller.IncidentMappingQueue == nil {
				return
			}
			toneWaitStart := time.Now()
			mappingCall := queue.controller.callForIncidentMapping(job.CallId, cleanedTranscript)
			if mappingCall == nil {
				return
			}
			if extractedAddr != "" {
				mappingCall.ExtractedAddress = extractedAddr
			}
			if toneWait := time.Since(toneWaitStart); toneWait > 2*time.Second {
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
					"incident mapping call %d: waited %.1fs for tone match before mapping started", job.CallId, toneWait.Seconds()))
			}
			queue.controller.IncidentMappingQueue.ProcessCall(mappingCall, cleanedTranscript)
		}()

		// Auto-learn tone sets (observe patterns, auto-add or log after N voiced calls)
		go func() {
			if postCall != nil {
				call := postCall
				call.Transcript = cleanedTranscript
				queue.controller.processToneAutoLearn(call, cleanedTranscript)
				return
			}
			if dbCall, err := queue.controller.Calls.GetCall(job.CallId); err == nil && dbCall != nil {
				dbCall.Transcript = cleanedTranscript
				queue.controller.processToneAutoLearn(dbCall, cleanedTranscript)
			}
		}()

		// Auto-learn unit aliases (radio unitRef → human label)
		go func() {
			if postCall != nil {
				call := postCall
				call.Transcript = cleanedTranscript
				queue.controller.processUnitAutoLearn(call, cleanedTranscript)
				return
			}
			if dbCall, err := queue.controller.Calls.GetCall(job.CallId); err == nil && dbCall != nil {
				dbCall.Transcript = cleanedTranscript
				queue.controller.processUnitAutoLearn(dbCall, cleanedTranscript)
			}
		}()

		duration := time.Since(startTime)
		count := queue.processedCount.Add(1)
		queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
			"[transcription] worker %d | call %d | %s / %s | done in %.2fs | confidence %.2f | total #%d",
			workerId, job.CallId, systemLabel, talkgroupLabel,
			duration.Seconds(), result.Confidence, count,
		))
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

	// Update call table (and optional alert summary when provided by Whisper server)
	transcript := strings.ToUpper(result.Transcript) // Ensure ALL CAPS
	extracted := strings.ToUpper(strings.TrimSpace(result.ExtractedAddress))
	if queue.controller.Database.Config.DbType == DbTypePostgresql {
		query := `UPDATE "calls" SET "transcript" = $1, "transcriptConfidence" = $2, "transcriptionStatus" = 'completed', "alertSummary" = $4, "extractedAddress" = $5 WHERE "callId" = $3`
		if _, err := queue.controller.Database.Sql.Exec(query, transcript, result.Confidence, callId, result.AlertSummary, extracted); err != nil {
			queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to update call transcript: %v", err))
		}
	}

	// Store detailed transcription (optional, for history)
	insertQuery := `INSERT INTO "transcriptions" ("callId", "transcript", "confidence", "language", "createdAt") VALUES ($1, $2, $3, $4, $5)`
	if _, err := queue.controller.Database.Sql.Exec(insertQuery, callId, transcript, result.Confidence, result.Language, time.Now().UnixMilli()); err != nil {
		queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to insert transcription record: %v", err))
	}
}

// processKeywords processes keywords after transcription completes
// OPTIMIZED: Loads users once, caches keyword lists, runs matching once per unique keyword set
func (queue *TranscriptionQueue) processKeywords(callId uint64, systemId uint64, talkgroupId uint64, result *TranscriptionResult) {
	if result == nil || result.Transcript == "" {
		queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("keyword processing skipped for call %d: no transcript", callId))
		return
	}

	if system, ok := queue.controller.Systems.GetSystemById(systemId); ok {
		if talkgroup, _ := system.Talkgroups.GetTalkgroupById(talkgroupId); talkgroup != nil && talkgroup.AlertingTalkgroup {
			queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("keyword processing skipped for call %d: alerting talkgroup", callId))
			return
		}
	}

	// Skip keyword processing if transcript is tone-only (no actual voice)
	// This saves processing on tone-only calls while still allowing transcription to complete
	if !queue.controller.isActualVoice(result.Transcript) {
		queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("keyword processing skipped for call %d: tone-only transcript", callId))
		return
	}

	queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("processing keywords for call %d (system=%d, talkgroup=%d)", callId, systemId, talkgroupId))

	system, sysOk := queue.controller.Systems.GetSystemById(systemId)
	if !sysOk {
		return
	}
	talkgroup, tgOk := system.Talkgroups.GetTalkgroupById(talkgroupId)
	if !tgOk {
		return
	}
	minimalCall := &Call{
		Id:        callId,
		System:    system,
		Talkgroup: talkgroup,
	}

	// Get all users with keyword alerts enabled for this talkgroup from cache
	userIds := queue.controller.PreferencesCache.GetUsersForTalkgroup(systemId, talkgroupId)

	transcript := strings.ToUpper(result.Transcript) // Ensure ALL CAPS

	// Step 1: Collect all users and their keyword preferences from cache
	type userKeywords struct {
		userId         uint64
		keywords       []string
		keywordListIds []uint64
	}
	var users []userKeywords

	for _, userId := range userIds {
		pref := queue.controller.PreferencesCache.GetPreference(userId, systemId, talkgroupId)
		if pref == nil || !pref.AlertEnabled || !pref.KeywordAlerts {
			continue
		}
		if !queue.controller.userEligibleForTalkgroupAlert(userId, minimalCall) {
			continue
		}

		user := userKeywords{
			userId:         userId,
			keywords:       pref.Keywords,
			keywordListIds: pref.KeywordListIds,
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

			// Batch-store all keyword matches for all users in one INSERT instead of
			// one INSERT per match per user (reduces N×M round-trips to 1).
			queue.storeKeywordMatchesBatch(callId, group.userIds, matches)

			// One alert row per call — must run synchronously before per-user goroutines.
			queue.controller.AlertEngine.UpsertKeywordAlert(callId, systemId, talkgroupId, matches, result)

			for _, userId := range group.userIds {
				queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("found %d keyword matches for user %d on call %d", len(matches), userId, callId))

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
				cooldownTgId := talkgroupId
				if call != nil && queue.controller.AlertEngine != nil {
					cooldownTgId = queue.controller.AlertEngine.cooldownTalkgroupId(call)
				}
				if queue.controller.AlertEngine != nil && queue.controller.AlertEngine.isToneAlertCooldownActive(cooldownTgId) {
					secs := queue.controller.AlertEngine.getAlertCooldownSeconds(cooldownTgId)
					queue.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
						"keyword alert cooldown active for talkgroup %d (cooldown=%ds) — skipping keyword push for call %d",
						cooldownTgId, secs, callId,
					))
				} else {
					go queue.controller.sendBatchedPushNotification(eligibleUserIds, "keyword", call, systemLabel, talkgroupLabel, "", keywordsMatched)
					if queue.controller.AlertEngine != nil {
						queue.controller.AlertEngine.recordToneAlertCooldown(cooldownTgId)
					}
				}
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
	// Get from cache instead of database
	list := queue.controller.KeywordListsCache.GetList(listId)
	if list == nil {
		return []string{}
	}
	return list.Keywords
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

// storeKeywordMatchesBatch stores all keyword matches for all users in a single
// multi-row INSERT, replacing the previous N×M individual INSERT approach.
func (queue *TranscriptionQueue) storeKeywordMatchesBatch(callId uint64, userIds []uint64, matches []KeywordMatch) {
	if len(matches) == 0 || len(userIds) == 0 {
		return
	}

	// Build parameter slices for a single VALUES (...),(...) INSERT.
	// Total rows = len(userIds) × len(matches).
	var args []any
	var placeholders []string

	if queue.controller.Database.Config.DbType == DbTypePostgresql {
		idx := 1
		for _, userId := range userIds {
			for _, match := range matches {
				placeholders = append(placeholders, fmt.Sprintf("(%d, %d, $%d, $%d, %d, false)", callId, userId, idx, idx+1, match.Position))
				args = append(args, match.Keyword, match.Context)
				idx += 2
			}
		}
		query := `INSERT INTO "keywordMatches" ("callId", "userId", "keyword", "context", "position", "alerted") VALUES ` + strings.Join(placeholders, ", ")
		if _, err := queue.controller.Database.Sql.Exec(query, args...); err != nil {
			queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to batch-store keyword matches: %v", err))
		}
	} else {
		for _, userId := range userIds {
			for _, match := range matches {
				placeholders = append(placeholders, fmt.Sprintf("(%d, %d, ?, ?, %d, false)", callId, userId, match.Position))
				args = append(args, match.Keyword, match.Context)
			}
		}
		query := `INSERT INTO "keywordMatches" ("callId", "userId", "keyword", "context", "position", "alerted") VALUES ` + strings.Join(placeholders, ", ")
		if _, err := queue.controller.Database.Sql.Exec(query, args...); err != nil {
			queue.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to batch-store keyword matches: %v", err))
		}
	}
}

// QueueDepth returns the number of jobs currently waiting in the queue channel
func (queue *TranscriptionQueue) QueueDepth() int {
	return len(queue.jobs)
}

// Stop stops the transcription queue
func (queue *TranscriptionQueue) Stop() {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()

	queue.running = false
	close(queue.jobs)
}
