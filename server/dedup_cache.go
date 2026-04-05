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
	"sync"
	"time"
)

// DedupEntry tracks a recently seen call for duplicate detection.
type DedupEntry struct {
	Timestamp int64 // call timestamp in ms (from the recorder, not server clock)
	SeenAt    time.Time
}

// DedupCache is a mutex-protected in-memory cache that catches duplicate calls
// before they reach the database. This closes the race window where two identical
// calls arrive simultaneously and both pass the DB check because neither has been
// written yet.
//
// Keyed by systemId+talkgroupId, each entry stores the most recent call timestamp.
// An incoming call is a duplicate if an entry exists for the same key and the call
// timestamps are within the configured window.
//
// A background goroutine evicts stale entries every 30 seconds to prevent unbounded
// memory growth. Entries expire after 2x the detection timeframe.
type DedupCache struct {
	entries map[uint64]*DedupEntry // key: (systemId << 32) | talkgroupId
	mutex   sync.Mutex
	ttl     time.Duration
	stopCh  chan struct{}
}

func NewDedupCache(timeframeMs uint) *DedupCache {
	ttl := time.Duration(timeframeMs*2) * time.Millisecond
	if ttl < 5*time.Second {
		ttl = 5 * time.Second
	}

	dc := &DedupCache{
		entries: make(map[uint64]*DedupEntry),
		ttl:     ttl,
		stopCh:  make(chan struct{}),
	}

	go dc.evictionLoop()

	return dc
}

func dedupKey(systemId, talkgroupId uint64) uint64 {
	return (systemId << 32) | talkgroupId
}

// CheckAndMark atomically checks whether a call is a duplicate and, if not,
// marks it as seen. Returns true if the call should be rejected as a duplicate.
func (dc *DedupCache) CheckAndMark(systemId, talkgroupId uint64, callTimestampMs int64, windowMs uint) bool {
	dc.mutex.Lock()
	defer dc.mutex.Unlock()

	key := dedupKey(systemId, talkgroupId)
	window := int64(windowMs)

	if entry, exists := dc.entries[key]; exists {
		delta := callTimestampMs - entry.Timestamp
		if delta < 0 {
			delta = -delta
		}
		if delta <= window {
			return true
		}
	}

	dc.entries[key] = &DedupEntry{
		Timestamp: callTimestampMs,
		SeenAt:    time.Now(),
	}
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

// UpdateTTL allows reconfiguring the cache when the timeframe option changes.
func (dc *DedupCache) UpdateTTL(timeframeMs uint) {
	dc.mutex.Lock()
	defer dc.mutex.Unlock()

	ttl := time.Duration(timeframeMs*2) * time.Millisecond
	if ttl < 5*time.Second {
		ttl = 5 * time.Second
	}
	dc.ttl = ttl
}

// Size returns the current number of entries (for diagnostics).
func (dc *DedupCache) Size() int {
	dc.mutex.Lock()
	defer dc.mutex.Unlock()
	return len(dc.entries)
}
