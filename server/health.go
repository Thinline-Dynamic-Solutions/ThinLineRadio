// Copyright (C) 2026 Thinline Dynamic Solutions
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
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/process"
)

// processStartTime records when the binary booted. Set in main() before any
// initialization so /api/health/* can report accurate uptime.
var processStartTime time.Time

// HealthService renders the read-only /api/health/* payload. The whole struct
// is safe for concurrent use because cache reads/writes are mutex-guarded and
// the only mutable inputs (controller stats) are read under their own locks.
//
// Reusing a single *process.Process means the gopsutil "since last call" CPU
// reading reflects the time between health hits, not a fresh-from-zero sample
// every request. We cache the rendered JSON for ~3s so a hostile scraper can't
// burn CPU by polling tightly.
//
// Authentication is NOT done here — these handlers are wired behind
// requireAdminBasicAuth in main.go, so by the time the handler runs the caller
// has already proven they hold the admin password.
type HealthService struct {
	controller *Controller

	procSampler *process.Process

	mu          sync.Mutex
	cachedAt    time.Time
	cachedBody  []byte
	cachedReady bool
	cachedCode  int
}

const healthCacheTTL = 3 * time.Second

// NewHealthService builds a HealthService bound to the given controller.
// gopsutil's process sampler is best-effort — if it fails we just skip the
// cpu_proc_pct field rather than failing the whole endpoint.
func NewHealthService(c *Controller) *HealthService {
	hs := &HealthService{controller: c}
	if p, err := process.NewProcess(int32(os.Getpid())); err == nil {
		hs.procSampler = p
	}
	return hs
}

// LiveHandler is a deliberately tiny "is the process alive" probe. The
// requireAdminBasicAuth wrapper in main.go has already validated the admin
// password by the time this runs.
func (hs *HealthService) LiveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprint(w, `{"status":"ok"}`)
}

// ReadyHandler reports overall readiness. Returns 200 + "ok" or 503 + a list
// of reasons (e.g. "db: ping failed"). Used by orchestrators that want to take
// the scanner out of a load balancer pool when it's degraded.
func (hs *HealthService) ReadyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	ok, reasons := hs.evaluateReadiness()
	body := map[string]interface{}{
		"status":  "ok",
		"reasons": reasons,
	}
	code := http.StatusOK
	if !ok {
		body["status"] = "degraded"
		code = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// FullHandler returns the full health payload, served from a 3s in-memory
// cache so a cron-like scraper can't force a runtime.ReadMemStats / db.Ping
// on every hit. The cache also remembers the last evaluated readiness code,
// so /api/health and /api/health/ready agree about the current state.
func (hs *HealthService) FullHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	body, code := hs.renderCached()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

// renderCached returns the cached JSON body if it's still fresh, otherwise
// rebuilds it. The lock is held for the entire rebuild so concurrent first
// hits collapse onto a single render rather than each running an independent
// gather.
func (hs *HealthService) renderCached() ([]byte, int) {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	if hs.cachedBody != nil && time.Since(hs.cachedAt) < healthCacheTTL {
		return hs.cachedBody, hs.cachedCode
	}

	payload, ready := hs.gather()
	code := http.StatusOK
	if !ready {
		code = http.StatusServiceUnavailable
	}

	body, err := json.Marshal(payload)
	if err != nil {
		body = []byte(`{"status":"error","reasons":["failed to marshal payload"]}`)
		code = http.StatusInternalServerError
	}

	hs.cachedBody = body
	hs.cachedCode = code
	hs.cachedReady = ready
	hs.cachedAt = time.Now()
	return body, code
}

// evaluateReadiness is a thin wrapper for /ready that just reuses the gather
// path (and its cache) so /health and /ready can never disagree.
func (hs *HealthService) evaluateReadiness() (bool, []string) {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	if hs.cachedBody != nil && time.Since(hs.cachedAt) < healthCacheTTL {
		var parsed struct {
			Reasons []string `json:"reasons"`
		}
		_ = json.Unmarshal(hs.cachedBody, &parsed)
		return hs.cachedReady, parsed.Reasons
	}

	payload, ready := hs.gather()
	body, _ := json.Marshal(payload)
	hs.cachedBody = body
	hs.cachedReady = ready
	if ready {
		hs.cachedCode = http.StatusOK
	} else {
		hs.cachedCode = http.StatusServiceUnavailable
	}
	hs.cachedAt = time.Now()

	reasons, _ := payload["reasons"].([]string)
	return ready, reasons
}

// gather assembles the full payload. All sources are cheap (channel length,
// already-locked workerStats, runtime.MemStats, gopsutil samplers, syscall.Statfs)
// — no DB-side aggregation queries are run here.
func (hs *HealthService) gather() (map[string]interface{}, bool) {
	ctrl := hs.controller
	opts := ctrl.Options
	now := time.Now().UTC()

	var reasons []string
	ready := true

	payload := map[string]interface{}{
		"service":    "thinline-radio",
		"version":    Version,
		"go_version": runtime.Version(),
		"now":        now.Format(time.RFC3339),
	}

	if id := opts.CentralManagementServerID; id != "" {
		payload["server_id"] = id
	}
	if name := opts.CentralManagementServerName; name != "" {
		payload["server_name"] = name
	}
	if h, err := os.Hostname(); err == nil {
		payload["hostname"] = h
	}

	if !processStartTime.IsZero() {
		payload["started_at"] = processStartTime.UTC().Format(time.RFC3339)
		payload["uptime_seconds"] = int64(time.Since(processStartTime).Seconds())
	}

	if ctrl.Clients != nil {
		payload["listener_count"] = ctrl.Clients.Count()
	}
	if ctrl.RecentCalls != nil {
		payload["calls_last_minute"] = ctrl.RecentCalls.CountLastMinute()
	}

	ctrl.workerStats.Lock()
	payload["active_workers"] = ctrl.workerStats.activeWorkers
	payload["total_calls_processed"] = ctrl.workerStats.totalCalls
	payload["avg_process_time_ms"] = ctrl.workerStats.avgProcessTime.Milliseconds()
	ctrl.workerStats.Unlock()

	payload["transcription_enabled"] = opts.TranscriptionConfig.Enabled
	payload["transcription_provider"] = opts.TranscriptionConfig.Provider
	payload["transcription_backup_provider"] = opts.TranscriptionConfig.BackupProvider
	if ctrl.TranscriptionQueue != nil {
		payload["transcription_queue_depth"] = ctrl.TranscriptionQueue.QueueDepth()
	}

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	payload["goroutines"] = runtime.NumGoroutine()
	payload["cpu_cores"] = runtime.NumCPU()
	payload["mem_alloc_mb"] = int(memStats.Alloc / 1024 / 1024)
	payload["mem_sys_mb"] = int(memStats.Sys / 1024 / 1024)

	if pcts, err := cpu.Percent(0, false); err == nil && len(pcts) > 0 {
		payload["cpu_pct"] = round1(pcts[0])
	}
	if hs.procSampler != nil {
		if pp, err := hs.procSampler.Percent(0); err == nil {
			cores := runtime.NumCPU()
			if cores > 0 {
				payload["cpu_proc_pct"] = round1(pp / float64(cores))
			}
		}
	}

	dbOK := true
	if ctrl.Database != nil && ctrl.Database.Sql != nil {
		stats := ctrl.Database.Sql.Stats()
		payload["db_open_connections"] = stats.OpenConnections
		payload["db_in_use"] = stats.InUse
		payload["db_wait_count"] = stats.WaitCount
		if err := ctrl.Database.Sql.Ping(); err != nil {
			dbOK = false
			ready = false
			reasons = append(reasons, fmt.Sprintf("db: ping failed: %v", err))
		}
	}
	payload["db_ok"] = dbOK

	// gopsutil/disk handles per-OS Statfs/GetDiskFreeSpaceExW differences so
	// the cross-platform builds (linux/darwin/freebsd/openbsd/netbsd/solaris/
	// windows) all compile from the same source.
	if ctrl.Config != nil && ctrl.Config.BaseDir != "" {
		if usage, err := disk.Usage(ctrl.Config.BaseDir); err == nil && usage != nil {
			payload["disk_free_bytes"] = usage.Free
			payload["disk_total_bytes"] = usage.Total
			if usage.Total > 0 {
				payload["disk_used_pct"] = round1(usage.UsedPercent)
				if usage.Free*20 < usage.Total {
					reasons = append(reasons, "disk: <5% free on data directory")
				}
			}
		}
	}

	cmPaired := opts.CentralManagementEnabled &&
		strings.TrimSpace(opts.CentralManagementURL) != "" &&
		strings.TrimSpace(opts.CentralManagementAPIKey) != ""
	payload["central_management_enabled"] = opts.CentralManagementEnabled
	payload["central_management_paired"] = cmPaired
	payload["relay_configured"] = opts.RelayServerAPIKey != ""
	payload["hydra_transcription_enabled"] = opts.HydraTranscriptionEnabled
	payload["hydra_api_key_present"] = strings.TrimSpace(opts.HydraAPIKey) != ""

	payload["reasons"] = reasons
	if ready {
		payload["status"] = "ok"
	} else {
		payload["status"] = "degraded"
	}
	return payload, ready
}
