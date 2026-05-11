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
	"sync"
	"time"
)

// RecentCallsRing is a tiny per-second ring buffer that tracks how many calls
// the server processed in the last 60 seconds. It is updated from the call
// ingestion worker pool and read once per minute by the central management
// heartbeat to publish a "calls/min" stat. Cost per Bump is one mod, one
// compare-and-swap of the bucket index, and one increment under a short lock.
type RecentCallsRing struct {
	mu       sync.Mutex
	buckets  [60]int
	lastSec  int64 // unix second of the last write/read; used to age out stale buckets
}

// NewRecentCallsRing creates a fresh ring buffer ready to count call activity.
func NewRecentCallsRing() *RecentCallsRing {
	return &RecentCallsRing{lastSec: time.Now().Unix()}
}

// Bump records one call against the current second's bucket, zeroing any
// buckets we slid past since the last write so they cannot leak old counts
// into the next 60-second window.
func (r *RecentCallsRing) Bump() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.advance(time.Now().Unix())
	r.buckets[r.lastSec%int64(len(r.buckets))]++
}

// CountLastMinute returns the total number of calls recorded in the last 60
// seconds, ageing out stale buckets first so a long-idle scanner reads zero.
func (r *RecentCallsRing) CountLastMinute() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.advance(time.Now().Unix())
	total := 0
	for _, v := range r.buckets {
		total += v
	}
	return total
}

// advance moves the cursor forward to `now`, zeroing any buckets that would
// otherwise carry counts older than 60 seconds. Must be called with mu held.
func (r *RecentCallsRing) advance(now int64) {
	if now <= r.lastSec {
		return
	}
	gap := now - r.lastSec
	if gap >= int64(len(r.buckets)) {
		// Idle for a full minute or more — wipe every bucket.
		for i := range r.buckets {
			r.buckets[i] = 0
		}
	} else {
		for i := int64(1); i <= gap; i++ {
			r.buckets[(r.lastSec+i)%int64(len(r.buckets))] = 0
		}
	}
	r.lastSec = now
}
