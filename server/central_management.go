// Copyright (C) 2024 Thinline Dynamic Solutions
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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/process"
)

// CentralManagementService handles communication with the centralized management system
type CentralManagementService struct {
	controller *Controller
	stopChan   chan struct{}
	registered bool

	// Pending removal code issued by the CM system (cleared after use or expiry)
	removalCodeMu     sync.Mutex
	removalCode       string
	removalCodeExpiry time.Time

	// CPU sampling state. gopsutil reports % since the previous call to
	// cpu.Percent / process.Percent, so we keep one *process.Process around for
	// the whole CMS lifetime and rely on the heartbeat cadence (~1/min) to space
	// the samples. The first call after init returns 0 which is fine — it just
	// means the very first heartbeat reports 0% CPU.
	procSampler *process.Process

	// Debounced full-config push to CM (see central_config_backup.go).
	configPush configPushState
}

// NewCentralManagementService creates a new central management service
func NewCentralManagementService(controller *Controller) *CentralManagementService {
	cms := &CentralManagementService{
		controller: controller,
		stopChan:   make(chan struct{}),
	}
	// Best-effort; if we can't open /proc for ourselves the CPU% just stays nil
	// in the heartbeat payload and the admin UI shows "—".
	if proc, err := process.NewProcess(int32(os.Getpid())); err == nil {
		cms.procSampler = proc
		// Prime both samplers so the first real heartbeat returns a non-zero
		// reading instead of the gopsutil "no prior sample" 0%.
		_, _ = cpu.Percent(0, false)
		_, _ = proc.Percent(0)
	}
	return cms
}

// Start begins the central management service
func (cms *CentralManagementService) Start() {
	if !cms.controller.Options.CentralManagementEnabled {
		return
	}

	log.Println("Central Management: Service enabled, attempting registration...")

	// Attempt initial registration
	if err := cms.register(); err != nil {
		log.Printf("Central Management: Initial registration failed: %v", err)
	} else {
		cms.registered = true
		log.Println("Central Management: Successfully registered")
	}

	// Start heartbeat loop (first heartbeat fires immediately, then every minute)
	go cms.heartbeatLoop()

	// Give CM a baseline config snapshot shortly after startup.
	cms.scheduleBaselineConfigPush()
}

// Stop stops the central management service
func (cms *CentralManagementService) Stop() {
	close(cms.stopChan)
}

// register sends registration information to the central system
func (cms *CentralManagementService) register() error {
	if cms.controller.Options.CentralManagementURL == "" ||
		cms.controller.Options.CentralManagementAPIKey == "" {
		return fmt.Errorf("central management URL or API key not configured")
	}

	// Gather server information
	serverName := cms.controller.Options.CentralManagementServerName
	if serverName == "" {
		serverName = "TLR Server"
	}

	// Get public URL from BaseUrl option or construct from listen address
	serverURL := cms.controller.Options.BaseUrl
	if serverURL == "" {
		serverURL = "http://localhost:3000"
	}

	// Get systems information from database
	systems := []map[string]interface{}{}
	for _, system := range cms.controller.Systems.List {
		systems = append(systems, map[string]interface{}{
			"id":    system.Id,
			"label": system.Label,
			"kind":  system.Kind,
		})
	}

	// Prepare registration payload
	payload := map[string]interface{}{
		"name":    serverName,
		"url":     serverURL,
		"systems": systems,
		"version": Version,
	}

	// Add the admin-configured Server ID if set
	if cms.controller.Options.CentralManagementServerID != "" {
		payload["server_id"] = cms.controller.Options.CentralManagementServerID
	}

	// Send registration request
	return cms.sendRequest("POST", "/api/tlr/register", payload)
}

// heartbeatLoop sends periodic heartbeats to the central system forever.
//
// IMPORTANT — this loop NEVER auto-unpairs the scanner. A previous version
// did: after N consecutive heartbeat failures it would wipe the CM URL /
// API key from options and exit the goroutine, requiring an operator to
// manually re-pair every affected scanner from CM admin. That conflated
// transient outages (CM redeploy, DNS hiccup, network blip > N minutes,
// cert renewal, BGP rerouting) with "the credentials are bad" and meant
// every brief CM downtime cascaded into a fleet-wide manual repair job.
//
// Behaviour now:
//   - Tick every minute, send a heartbeat.
//   - On failure, log + try to re-register (CM's /api/tlr/register is
//     idempotent: it'll UPDATE an existing row by api_key, claim a pending
//     row, or INSERT a new one — so this also self-heals when CM has
//     dropped the servers row, e.g. a DB rebuild or admin re-add).
//   - On success, mark registered and move on.
//   - The only way the goroutine exits is via Stop() (close(stopChan)).
//
// If the credentials genuinely are wrong, the scanner will keep failing
// and the operator will see persistent failure logs + an offline state in
// CM admin (last_heartbeat stale). They can then explicitly remove the
// pairing from the scanner admin UI rather than have the scanner silently
// commit suicide on its own.
func (cms *CentralManagementService) heartbeatLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	consecutiveFailures := 0

	for {
		select {
		case <-ticker.C:
			if err := cms.sendHeartbeat(); err != nil {
				consecutiveFailures++
				log.Printf("Central Management: Heartbeat failed (%d consecutive failures, will keep retrying): %v",
					consecutiveFailures, err)

				// Always attempt a re-register on heartbeat failure. CM
				// might have lost our row (DB restore, admin re-add) or we
				// might never have registered cleanly in the first place;
				// register() is idempotent on the CM side, so it's cheap
				// and safe to retry every minute.
				if regErr := cms.register(); regErr != nil {
					// Don't log this as a hard error — the heartbeat error
					// above already explains why we're here, and a follow-
					// up "re-register also failed" line just adds noise.
					_ = regErr
				} else {
					cms.registered = true
					log.Println("Central Management: Re-registration successful")
				}
			} else {
				if consecutiveFailures > 0 {
					log.Printf("Central Management: Heartbeat recovered after %d consecutive failures", consecutiveFailures)
				}
				cms.registered = true
				consecutiveFailures = 0
			}
		case <-cms.stopChan:
			return
		}
	}
}

// sendHeartbeat sends a heartbeat to the central system, including a small
// snapshot of in-process counters so Central Management can render scanner
// stats without scanners having to expose any extra HTTP endpoints.
//
// Every value here is read from already-tracked memory (Clients map, runtime
// MemStats, transcription queue depth, workerStats, the database/sql pool,
// and the per-second RecentCalls ring buffer). One extra runtime.ReadMemStats
// per minute is the only added cost on the scanner.
func (cms *CentralManagementService) sendHeartbeat() error {
	payload := cms.gatherStatsPayload()
	return cms.sendRequest("POST", "/api/tlr/heartbeat", payload)
}

// round1 rounds a float to one decimal place (e.g. 12.345 → 12.3) so the JSON
// payload stays compact and the admin UI doesn't try to render meaningless
// micro-precision over a 60-second sampling window.
func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}

// gatherStatsPayload assembles the heartbeat body with the current scanner
// stats. Kept separate from sendHeartbeat so it is easy to unit-test and so
// the hot path stays small.
func (cms *CentralManagementService) gatherStatsPayload() map[string]interface{} {
	ctrl := cms.controller
	payload := map[string]interface{}{}

	if id := ctrl.Options.CentralManagementServerID; id != "" {
		payload["server_id"] = id
	}

	// Listener count — already O(1) on Clients.Map.
	if ctrl.Clients != nil {
		payload["listener_count"] = ctrl.Clients.Count()
	}

	// Calls in the last 60s — sourced from the ring buffer the worker pool bumps.
	if ctrl.RecentCalls != nil {
		payload["calls_last_minute"] = ctrl.RecentCalls.CountLastMinute()
	}

	// Go runtime — heap, total OS-allocated memory, goroutines, NumCPU.
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	payload["mem_alloc_mb"] = int(memStats.Alloc / 1024 / 1024)
	payload["mem_sys_mb"] = int(memStats.Sys / 1024 / 1024)
	payload["goroutines"] = runtime.NumGoroutine()
	payload["cpu_cores"] = runtime.NumCPU()

	// CPU usage. cpu.Percent / process.Percent report the average since the last
	// call to that same function — the heartbeat cadence (~1/min) defines the
	// window, so this is "average CPU% over the last minute". Rounded to one
	// decimal because anything more precise is meaningless on a 60s window.
	if pcts, err := cpu.Percent(0, false); err == nil && len(pcts) > 0 {
		payload["cpu_pct"] = round1(pcts[0])
	}
	if cms.procSampler != nil {
		if pp, err := cms.procSampler.Percent(0); err == nil {
			// process.Percent returns a value relative to a single CPU core
			// (e.g. 200% on a 4-core box = 2 cores fully pinned). Convert to a
			// system-wide percent so it lines up with cpu_pct above.
			cores := runtime.NumCPU()
			if cores > 0 {
				payload["cpu_proc_pct"] = round1(pp / float64(cores))
			}
		}
	}

	// Transcription queue depth — channel length, O(1).
	if ctrl.TranscriptionQueue != nil {
		payload["transcription_queue_depth"] = ctrl.TranscriptionQueue.QueueDepth()
	}

	// Active call-processing workers — read under the workerStats lock so we
	// match what /api/status/performance reports.
	ctrl.workerStats.Lock()
	payload["active_workers"] = ctrl.workerStats.activeWorkers
	ctrl.workerStats.Unlock()

	// DB connection pool — Stats() is cheap and doesn't touch the pool.
	if ctrl.Database != nil && ctrl.Database.Sql != nil {
		dbStats := ctrl.Database.Sql.Stats()
		payload["db_open_connections"] = dbStats.OpenConnections
		payload["db_in_use"] = dbStats.InUse
		payload["db_wait_count"] = dbStats.WaitCount // cumulative; CM converts to a per-minute delta
	}

	return payload
}

// sendRequest sends an HTTP request to the central management system
func (cms *CentralManagementService) sendRequest(method, path string, payload interface{}) error {
	url := cms.controller.Options.CentralManagementURL + path

	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal payload: %w", err)
		}
	}

	req, err := http.NewRequest(method, url, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", cms.controller.Options.CentralManagementAPIKey)

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

// TestConnection tests the connection to the central management system with provided credentials.
// It returns the exact upstream HTTP status and response body for easier troubleshooting in the UI.
func (cms *CentralManagementService) TestConnection(centralURL, apiKey, serverName, serverURL string) (int, []byte, error) {
	baseURL := strings.TrimRight(centralURL, "/")
	testURL := fmt.Sprintf("%s/api/tlr/register", baseURL)

	// Build a lightweight payload so upstream logs clearly show this is a test request.
	payload := map[string]interface{}{
		"name":    serverName,
		"url":     serverURL,
		"systems": []interface{}{},
		"version": Version,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to marshal test payload: %w", err)
	}

	req, err := http.NewRequest("POST", testURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return 0, nil, fmt.Errorf("failed to create test request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to reach central management: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return resp.StatusCode, nil, fmt.Errorf("failed to read central response body: %w", readErr)
	}

	return resp.StatusCode, body, nil
}
