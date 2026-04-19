// Copyright (C) 2025 Thinline Dynamic Solutions
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
	"fmt"
	"sync"
	"time"
)

// DedupEntry caches metadata for a recently seen call to catch simultaneous
// duplicate arrivals before either has been written to the DB.
type DedupEntry struct {
	Duration      float64   // Audio duration in seconds (for ratio guard)
	CallTimestamp int64     // P25 call timestamp in milliseconds
	SeenAt        time.Time
}

// DedupCache is a mutex-protected in-memory cache that closes the race window
// where two identical calls arrive simultaneously and both pass the DB check
// before either has been written.
//
// Key prefixes:
//   "ep:systemId:talkgroupId" — energy profile entry
//   "ah:systemId:talkgroupId:hash" — PCM content hash entry
//   "ts:systemId:talkgroupId" — timestamp fallback entry
//
// A background goroutine evicts stale entries every 30 seconds.
type DedupCache struct {
	entries map[string]*DedupEntry
	mutex   sync.Mutex
	ttl     time.Duration
	stopCh  chan struct{}
}

func NewDedupCache(timeframeMs uint) *DedupCache {
	ttl := time.Duration(timeframeMs*2) * time.Millisecond
	if ttl < 60*time.Second {
		ttl = 60 * time.Second
	}
	dc := &DedupCache{
		entries: make(map[string]*DedupEntry),
		ttl:     ttl,
		stopCh:  make(chan struct{}),
	}
	go dc.evictionLoop()
	return dc
}


// CheckAndMarkReceivedAt checks whether a call for the given system+talkgroup
// was already seen within receivedAtDuplicateWindow (1 second) of now.
// Returns true (duplicate) if so. Always records the current arrival time so
// back-to-back simultaneous uploads are caught before either hits the database.
func (dc *DedupCache) CheckAndMarkReceivedAt(systemId, talkgroupId uint64) bool {
	key := fmt.Sprintf("ra:%d:%d", systemId, talkgroupId)
	now := time.Now()
	dc.mutex.Lock()
	defer dc.mutex.Unlock()

	if entry, ok := dc.entries[key]; ok {
		if now.Sub(entry.SeenAt) <= receivedAtDuplicateWindow {
			// Update SeenAt so we keep blocking additional arrivals within the window.
			entry.SeenAt = now
			return true
		}
	}
	dc.entries[key] = &DedupEntry{SeenAt: now}
	return false
}

func (dc *DedupCache) evictionLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			dc.evict()
		case <-dc.stopCh:
			return
		}
	}
}

func (dc *DedupCache) evict() {
	dc.mutex.Lock()
	defer dc.mutex.Unlock()
	cutoff := time.Now().Add(-dc.ttl)
	for key, entry := range dc.entries {
		if entry.SeenAt.Before(cutoff) {
			delete(dc.entries, key)
		}
	}
}

// Stop shuts down the background eviction goroutine.
func (dc *DedupCache) Stop() {
	close(dc.stopCh)
}

// UpdateTTL reconfigures the cache TTL when the timeframe option changes.
func (dc *DedupCache) UpdateTTL(timeframeMs uint) {
	dc.mutex.Lock()
	defer dc.mutex.Unlock()
	ttl := time.Duration(timeframeMs*2) * time.Millisecond
	if ttl < 60*time.Second {
		ttl = 60 * time.Second
	}
	dc.ttl = ttl
}

// Size returns the current number of entries (for diagnostics).
func (dc *DedupCache) Size() int {
	dc.mutex.Lock()
	defer dc.mutex.Unlock()
	return len(dc.entries)
}
