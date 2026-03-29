// Copyright (C) 2025 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT EVEN THE IMPLIED WARRANTY of MERCHANTABILITY or FITNESS
// FOR A PARTICULAR PURPOSE.  See the GNU General Public License for
// more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	hydraBaseURL = "https://hydra.alertpage.us"
)

// HydraTranscriptionRetrievalJob represents a job to retrieve transcription from Hydra
type HydraTranscriptionRetrievalJob struct {
	CallId         uint64
	TransmissionId string
	RequestId      string
	QueuedAt       time.Time
	RetryCount     int // Number of times this job has been retried
}

// HydraTranscriptionRetrievalQueue manages retrieval of transcriptions from Hydra API
type HydraTranscriptionRetrievalQueue struct {
	jobs              chan HydraTranscriptionRetrievalJob
	controller        *Controller
	mutex             sync.Mutex
	running           bool
	stopChan          chan struct{}
	apiSecret         string    // Node secret for authentication
	jwtToken          string    // JWT token obtained from /auth/node
	tokenExpiry       time.Time // When the JWT expires
	lastNoJobsLogTime time.Time // Last time we logged "no jobs ready" message
	pollCount         int       // Counter for debugging polling
}

// NewHydraTranscriptionRetrievalQueue creates a new Hydra transcription retrieval queue
func NewHydraTranscriptionRetrievalQueue(controller *Controller) *HydraTranscriptionRetrievalQueue {
	queue := &HydraTranscriptionRetrievalQueue{
		jobs:       make(chan HydraTranscriptionRetrievalJob, 100), // Buffer 100 jobs
		controller: controller,
		running:    true,
		stopChan:   make(chan struct{}),
		apiSecret:  controller.Options.HydraAPIKey, // Stored as "apiSecret" but comes from HydraAPIKey option
	}

	// Authenticate immediately to get JWT
	if err := queue.authenticate(); err != nil {
		log.Printf("Hydra retrieval: WARNING - initial authentication failed: %v", err)
	} else {
		log.Printf("Hydra retrieval: queue initialized and authenticated, starting poll worker")
	}

	// Start the polling worker
	go queue.pollWorker()

	return queue
}

// QueueJob adds a transmission ID to the retrieval queue
func (queue *HydraTranscriptionRetrievalQueue) QueueJob(job HydraTranscriptionRetrievalJob) {
	if !queue.running {
		return
	}

	// Set QueuedAt if not already set
	if job.QueuedAt.IsZero() {
		job.QueuedAt = time.Now()
	}

	select {
	case queue.jobs <- job:
		if job.RetryCount == 0 {
			log.Printf("Hydra retrieval: queued call %d with transmission_id=%s", job.CallId, job.TransmissionId)
		} else {
			log.Printf("Hydra retrieval: requeued call %d with transmission_id=%s (retry %d)", job.CallId, job.TransmissionId, job.RetryCount)
		}
	default:
		log.Printf("Hydra retrieval: queue full, dropping call %d", job.CallId)
	}
}

// pollWorker polls Hydra API every 6 seconds to retrieve transcriptions for queued jobs
func (queue *HydraTranscriptionRetrievalQueue) pollWorker() {
	log.Printf("Hydra retrieval: poll worker started")
	ticker := time.NewTicker(6 * time.Second)
	defer ticker.Stop()

	// Process immediately on start, then every 6 seconds
	queue.processBatch()

	for {
		select {
		case <-ticker.C:
			queue.processBatch()
		case <-queue.stopChan:
			log.Printf("Hydra retrieval: poll worker stopped")
			return
		}
	}
}

// authenticate obtains a JWT token from Hydra API using the node secret
func (queue *HydraTranscriptionRetrievalQueue) authenticate() error {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()

	if queue.apiSecret == "" {
		return fmt.Errorf("Hydra API secret not configured")
	}

	// Call /auth/node endpoint
	url := fmt.Sprintf("%s/auth/node", hydraBaseURL)
	payload := map[string]string{
		"api_secret": queue.apiSecret,
	}
	payloadBytes, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", url, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return fmt.Errorf("failed to create auth request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to authenticate with Hydra: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Hydra authentication failed: %d %s", resp.StatusCode, string(body))
	}

	var authResponse struct {
		Token     string `json:"token"`
		IPAddress string `json:"ip_address"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&authResponse); err != nil {
		return fmt.Errorf("failed to decode auth response: %w", err)
	}

	if authResponse.Token == "" {
		return fmt.Errorf("Hydra auth response missing token")
	}

	queue.jwtToken = authResponse.Token
	// JWT expires in 90 days according to docs, but set expiry to 85 days to be safe
	queue.tokenExpiry = time.Now().Add(85 * 24 * time.Hour)

	log.Printf("Hydra retrieval: authenticated successfully, JWT expires at %v", queue.tokenExpiry)
	return nil
}

// ensureAuthenticated checks if JWT is valid and re-authenticates if needed
func (queue *HydraTranscriptionRetrievalQueue) ensureAuthenticated() error {
	queue.mutex.Lock()
	needsAuth := queue.jwtToken == "" || time.Now().After(queue.tokenExpiry)
	queue.mutex.Unlock()

	if needsAuth {
		return queue.authenticate()
	}
	return nil
}

// processBatch processes up to 15 queued jobs by querying Hydra API
func (queue *HydraTranscriptionRetrievalQueue) processBatch() {
	if !queue.running {
		return
	}

	// Update API secret from options (in case it changed)
	queue.controller.Options.mutex.Lock()
	queue.apiSecret = queue.controller.Options.HydraAPIKey
	enabled := queue.controller.Options.HydraTranscriptionEnabled
	queue.controller.Options.mutex.Unlock()

	if !enabled || queue.apiSecret == "" {
		// Log immediately on first check to help debug
		queue.mutex.Lock()
		firstCheck := queue.lastNoJobsLogTime.IsZero()
		queue.mutex.Unlock()
		if firstCheck {
			log.Printf("Hydra retrieval: processBatch called but Hydra disabled or no API secret (enabled=%v, hasSecret=%v)", enabled, queue.apiSecret != "")
		}
		// Hydra not enabled or no API secret - skip processing but keep queue running
		// Log once every 60 seconds if disabled to help debug
		queue.mutex.Lock()
		shouldLog := time.Since(queue.lastNoJobsLogTime) > 60*time.Second
		if shouldLog {
			queue.lastNoJobsLogTime = time.Now()
		}
		queue.mutex.Unlock()
		if shouldLog {
			if !enabled {
				log.Printf("Hydra retrieval: queue running but Hydra transcription is disabled (enabled=%v, apiSecret empty=%v)", enabled, queue.apiSecret == "")
			} else {
				log.Printf("Hydra retrieval: queue running but API secret is empty (enabled=%v)", enabled)
			}
		}
		return
	}

	// Ensure we have a valid JWT token
	if err := queue.ensureAuthenticated(); err != nil {
		log.Printf("Hydra retrieval: authentication failed: %v", err)
		return
	}

	// Collect up to 15 jobs from the queue, but only process those that have waited at least 15 seconds
	now := time.Now()

	// Log when we start checking for jobs (first few times to confirm polling is working)
	queue.mutex.Lock()
	pollCount := queue.pollCount
	queue.pollCount++
	queue.mutex.Unlock()
	if pollCount < 5 {
		log.Printf("Hydra retrieval: processBatch checking for ready jobs (poll #%d)", pollCount+1)
	}
	readyJobs := make([]HydraTranscriptionRetrievalJob, 0, 15)
	pendingJobs := make([]HydraTranscriptionRetrievalJob, 0)

	// Drain all available jobs from the channel
	draining := true
	for draining && len(readyJobs) < 15 && len(pendingJobs) < 100 {
		select {
		case job := <-queue.jobs:
			// Check if job has waited at least 15 seconds since it was queued
			waitTime := now.Sub(job.QueuedAt)
			if waitTime >= 15*time.Second {
				readyJobs = append(readyJobs, job)
			} else {
				pendingJobs = append(pendingJobs, job)
			}
		default:
			draining = false
		}
	}

	// ALWAYS put pending jobs back in the queue before doing anything else
	for _, job := range pendingJobs {
		select {
		case queue.jobs <- job:
			// Successfully requeued
		default:
			log.Printf("Hydra retrieval: queue full, dropping pending call %d", job.CallId)
		}
	}

	if len(readyJobs) == 0 {
		if len(pendingJobs) > 0 {
			log.Printf("Hydra retrieval: %d job(s) still waiting for 15-second delay", len(pendingJobs))
		}
		return
	}

	log.Printf("Hydra retrieval: processing %d transmission(s) (%d still waiting)", len(readyJobs), len(pendingJobs))

	// Query Hydra API for each transmission ID
	for _, job := range readyJobs {
		queue.retrieveTranscription(job)
	}
}

// retrieveTranscription queries Hydra API for a single transmission ID
func (queue *HydraTranscriptionRetrievalQueue) retrieveTranscription(job HydraTranscriptionRetrievalJob) {
	if job.TransmissionId == "" {
		log.Printf("Hydra retrieval: skipping call %d - empty transmission_id", job.CallId)
		return
	}

	// Query Hydra API: GET /api/transmission/{transmission_id}
	url := fmt.Sprintf("%s/api/transmission/%s", hydraBaseURL, job.TransmissionId)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Printf("Hydra retrieval: failed to create request for call %d: %v", job.CallId, err)
		return
	}

	// Get JWT token (with mutex lock)
	queue.mutex.Lock()
	jwtToken := queue.jwtToken
	queue.mutex.Unlock()

	if jwtToken == "" {
		log.Printf("Hydra retrieval: no JWT token available for call %d", job.CallId)
		return
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", jwtToken))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Hydra retrieval: failed to query Hydra for call %d: %v", job.CallId, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		// JWT expired or invalid - re-authenticate and retry once
		log.Printf("Hydra retrieval: JWT expired/invalid for call %d, re-authenticating", job.CallId)
		queue.mutex.Lock()
		queue.jwtToken = "" // Clear invalid token
		queue.mutex.Unlock()

		if err := queue.authenticate(); err != nil {
			log.Printf("Hydra retrieval: re-authentication failed: %v", err)
			return
		}

		// Retry the request with new token
		queue.mutex.Lock()
		jwtToken = queue.jwtToken
		queue.mutex.Unlock()

		req2, _ := http.NewRequest("GET", url, nil)
		req2.Header.Set("Authorization", fmt.Sprintf("Bearer %s", jwtToken))
		req2.Header.Set("Content-Type", "application/json")

		resp2, err := client.Do(req2)
		if err != nil {
			log.Printf("Hydra retrieval: retry failed for call %d: %v", job.CallId, err)
			return
		}
		defer resp2.Body.Close()

		if resp2.StatusCode != http.StatusOK {
			log.Printf("Hydra retrieval: Hydra returned %d for call %d (transmission_id=%s) after re-auth", resp2.StatusCode, job.CallId, job.TransmissionId)
			return
		}

		// Use resp2 for decoding
		resp = resp2
	} else if resp.StatusCode != http.StatusOK {
		log.Printf("Hydra retrieval: Hydra returned %d for call %d (transmission_id=%s)", resp.StatusCode, job.CallId, job.TransmissionId)
		return
	}

	// Single-transmission endpoint returns result as an object, not an array
	var hydraResponse struct {
		Success bool `json:"success"`
		Result  struct {
			TranscriptionText string `json:"transcription_text"`
			IdTransmission    string `json:"id_transmission"`
		} `json:"result"`
	}

	// Read response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Hydra retrieval: failed to read Hydra response for call %d: %v", job.CallId, err)
		return
	}

	// Log raw Hydra response for debugging transcript mismatches
	bodyStr := string(bodyBytes)
	if len(bodyStr) > 500 {
		bodyStr = bodyStr[:500] + "..."
	}
	log.Printf("Hydra retrieval: response for call %d (transmission_id=%s): %s", job.CallId, job.TransmissionId, bodyStr)

	if err := json.Unmarshal(bodyBytes, &hydraResponse); err != nil {
		log.Printf("Hydra retrieval: failed to decode Hydra response for call %d: %v", job.CallId, err)
		return
	}

	if !hydraResponse.Success {
		log.Printf("Hydra retrieval: no transcription found for call %d (transmission_id=%s)", job.CallId, job.TransmissionId)
		return
	}

	// Verify the returned transmission matches what we requested
	if hydraResponse.Result.IdTransmission != "" && hydraResponse.Result.IdTransmission != job.TransmissionId {
		log.Printf("Hydra retrieval: WARNING - Hydra returned wrong transmission for call %d: requested=%s, got=%s", job.CallId, job.TransmissionId, hydraResponse.Result.IdTransmission)
		return
	}

	transcriptionText := hydraResponse.Result.TranscriptionText
	if transcriptionText == "" || strings.TrimSpace(transcriptionText) == "" {
		// Empty transcription - requeue once if this is the first attempt
		if job.RetryCount == 0 {
			log.Printf("Hydra retrieval: empty transcription for call %d (transmission_id=%s), requeuing for retry", job.CallId, job.TransmissionId)
			// Requeue with retry count incremented
			// For retry, wait only 5 seconds (will be checked in next polling cycle)
			retryJob := job
			retryJob.RetryCount = 1
			retryJob.QueuedAt = time.Now().Add(-10 * time.Second) // Set to 10 seconds ago so it's ready in ~5 seconds
			queue.QueueJob(retryJob)
		} else {
			// Already retried once, drop it
			log.Printf("Hydra retrieval: empty transcription for call %d (transmission_id=%s) after retry, dropping", job.CallId, job.TransmissionId)
		}
		return
	}

	// Verify that the callId still has the matching transmission_id before storing transcript
	// This prevents storing transcripts on the wrong call if transmission_id changed
	var dbTransmissionId string
	verifyQuery := `SELECT "transmissionId" FROM "calls" WHERE "callId" = $1`
	if queue.controller.Database.Config.DbType != DbTypePostgresql {
		verifyQuery = `SELECT "transmissionId" FROM "calls" WHERE "callId" = ?`
	}
	err = queue.controller.Database.Sql.QueryRow(verifyQuery, job.CallId).Scan(&dbTransmissionId)
	if err != nil {
		log.Printf("Hydra retrieval: failed to verify call %d exists: %v", job.CallId, err)
		return
	}

	// Verify transmission_id matches
	if dbTransmissionId != job.TransmissionId {
		log.Printf("Hydra retrieval: WARNING - transmission_id mismatch for call %d: stored=%q, expected=%q, skipping transcript storage", job.CallId, dbTransmissionId, job.TransmissionId)
		return
	}

	// Store transcription in the call
	transcript := strings.ToUpper(strings.TrimSpace(transcriptionText))
	var query string
	if queue.controller.Database.Config.DbType == DbTypePostgresql {
		query = `UPDATE "calls" SET "transcript" = $1, "transcriptConfidence" = $2, "transcriptionStatus" = $3 WHERE "callId" = $4 AND "transmissionId" = $5`
	} else {
		query = `UPDATE "calls" SET "transcript" = ?, "transcriptConfidence" = ?, "transcriptionStatus" = ? WHERE "callId" = ? AND "transmissionId" = ?`
	}
	result, err := queue.controller.Database.Sql.Exec(query, transcript, 1.0, "completed", job.CallId, job.TransmissionId)
	if err != nil {
		log.Printf("Hydra retrieval: failed to update call transcript for call %d: %v", job.CallId, err)
		return
	}

	// Verify the update actually affected a row
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("Hydra retrieval: failed to get rows affected for call %d: %v", job.CallId, err)
		return
	}
	if rowsAffected == 0 {
		log.Printf("Hydra retrieval: WARNING - no rows updated for call %d with transmission_id=%s (call may have been deleted or transmission_id changed)", job.CallId, job.TransmissionId)
		return
	}

	// Log with first 80 chars of transcript to help verify correct matching
	preview := transcript
	if len(preview) > 80 {
		preview = preview[:80] + "..."
	}
	log.Printf("Hydra retrieval: stored transcript for call %d (transmission_id=%s, len=%d): %q", job.CallId, job.TransmissionId, len(transcript), preview)
}

// Stop stops the retrieval queue
func (queue *HydraTranscriptionRetrievalQueue) Stop() {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	if queue.running {
		queue.running = false
		close(queue.stopChan)
	}
}

// UpdateAPIKey updates the API secret (called when config changes)
func (queue *HydraTranscriptionRetrievalQueue) UpdateAPIKey(apiSecret string) {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	oldSecret := queue.apiSecret
	queue.apiSecret = apiSecret

	// If secret changed, invalidate token to force re-auth
	if oldSecret != apiSecret {
		queue.jwtToken = ""
		queue.tokenExpiry = time.Time{}
		log.Printf("Hydra retrieval: API secret updated, will re-authenticate on next request")
	}
}
