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
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Config backup to Central Management.
//
// Mirrors config sync: every change that already calls SyncConfigToFile also calls
// NotifyConfigChanged, and a debounced worker sends the full export (the same
// document config sync writes to disk and the admin UI exports) to Central
// Management, which keeps versioned snapshots and can restore one.
//
// The export is large on busy servers and changes in bursts (an import touches many
// tables), so pushes are coalesced: wait for a quiet period, but never longer than
// the max wait, and skip the send when the document is identical to the last one
// CM accepted.
const (
	configPushDebounce     = 30 * time.Second
	configPushMaxWait      = 5 * time.Minute
	configPushTimeout      = 90 * time.Second
	configPushBaselineWait = 2 * time.Minute
	configPushRetryMin     = time.Minute
	configPushRetryMax     = 15 * time.Minute
	configPushUnsupported  = time.Hour // CM without the endpoint: check again rarely
)

type configPushState struct {
	mu           sync.Mutex
	timer        *time.Timer
	firstPending time.Time
	pushing      bool
	lastSentHash string
	retryDelay   time.Duration
	lastErrLog   time.Time
}

// configBackupEnabled reports whether this server is paired with Central Management.
func (cms *CentralManagementService) configBackupEnabled() bool {
	o := cms.controller.Options
	return o.CentralManagementEnabled &&
		strings.TrimSpace(o.CentralManagementURL) != "" &&
		strings.TrimSpace(o.CentralManagementAPIKey) != ""
}

// NotifyConfigChanged schedules a debounced config push. Cheap and non-blocking, so
// it is safe to call from every configuration write path.
func (cms *CentralManagementService) NotifyConfigChanged() {
	if cms == nil || !cms.configBackupEnabled() {
		return
	}
	p := &cms.configPush
	p.mu.Lock()
	defer p.mu.Unlock()
	cms.scheduleConfigPushLocked(configPushDebounce)
}

// scheduleConfigPushLocked (re)arms the timer: wait the debounce, but not past the
// max wait counted from the first unsent change. Caller holds p.mu.
func (cms *CentralManagementService) scheduleConfigPushLocked(delay time.Duration) {
	p := &cms.configPush
	now := time.Now()
	if p.firstPending.IsZero() {
		p.firstPending = now
	}
	if remaining := configPushMaxWait - now.Sub(p.firstPending); remaining < delay {
		delay = remaining
		if delay < time.Second {
			delay = time.Second
		}
	}
	if p.timer != nil {
		p.timer.Stop()
	}
	p.timer = time.AfterFunc(delay, cms.flushConfigPush)
}

// scheduleBaselineConfigPush sends one snapshot shortly after startup so CM has a
// current copy even if nothing changes. CM ignores it when it already has that content.
func (cms *CentralManagementService) scheduleBaselineConfigPush() {
	go func() {
		select {
		case <-time.After(configPushBaselineWait):
			cms.NotifyConfigChanged()
		case <-cms.stopChan:
		}
	}()
}

func (cms *CentralManagementService) flushConfigPush() {
	p := &cms.configPush
	p.mu.Lock()
	if p.pushing {
		cms.scheduleConfigPushLocked(5 * time.Second)
		p.mu.Unlock()
		return
	}
	p.pushing = true
	p.firstPending = time.Time{}
	p.mu.Unlock()

	retryIn, err := cms.pushConfigSnapshot()

	p.mu.Lock()
	defer p.mu.Unlock()
	p.pushing = false
	if err == nil {
		p.retryDelay = 0
		return
	}
	if retryIn == 0 {
		if p.retryDelay == 0 {
			p.retryDelay = configPushRetryMin
		} else if p.retryDelay *= 2; p.retryDelay > configPushRetryMax {
			p.retryDelay = configPushRetryMax
		}
		retryIn = p.retryDelay
	}
	// Log at most every 15 minutes so a CM outage does not flood the server log.
	if time.Since(p.lastErrLog) > 15*time.Minute {
		log.Printf("Central Management: config backup push failed (retrying in %s): %v", retryIn, err)
		p.lastErrLog = time.Now()
	}
	cms.scheduleConfigPushLocked(retryIn)
}

// pushConfigSnapshot sends the full export. The returned duration, when non-zero,
// overrides the normal retry backoff.
func (cms *CentralManagementService) pushConfigSnapshot() (time.Duration, error) {
	ctrl := cms.controller
	if ctrl == nil || ctrl.Admin == nil || !cms.configBackupEnabled() {
		return 0, nil
	}

	raw, err := json.Marshal(ctrl.Admin.GetConfig())
	if err != nil {
		return 0, fmt.Errorf("failed to encode config: %w", err)
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])

	p := &cms.configPush
	p.mu.Lock()
	same := hash == p.lastSentHash
	p.mu.Unlock()
	if same {
		return 0, nil
	}

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(raw); err != nil {
		return 0, fmt.Errorf("failed to compress config: %w", err)
	}
	if err := zw.Close(); err != nil {
		return 0, fmt.Errorf("failed to compress config: %w", err)
	}

	url := strings.TrimRight(strings.TrimSpace(ctrl.Options.CentralManagementURL), "/") + "/api/tlr/config-snapshot"
	req, err := http.NewRequest(http.MethodPost, url, &gz)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("X-API-Key", ctrl.Options.CentralManagementAPIKey)
	req.Header.Set("X-Config-Sha256", hash)
	req.Header.Set("X-TLR-Version", Version)

	resp, err := (&http.Client{Timeout: configPushTimeout}).Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to send config: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated:
		p.mu.Lock()
		p.lastSentHash = hash
		p.mu.Unlock()
		return 0, nil
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed:
		return configPushUnsupported, fmt.Errorf("central management does not support config backups yet (HTTP %d)", resp.StatusCode)
	default:
		return 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
}
