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
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimiter provides general rate limiting to prevent DDoS attacks
type RateLimiter struct {
	requests map[string]*rateLimitEntry
	mutex    sync.RWMutex
	// Maximum requests per IP per window
	maxRequests int
	// Time window for rate limiting
	windowDuration time.Duration
	// Cleanup interval for old entries
	cleanupInterval time.Duration
}

type rateLimitEntry struct {
	count     int
	firstSeen time.Time
	lastSeen  time.Time
}

// LoginAttemptTracker tracks failed login attempts and blocks IPs after threshold
type LoginAttemptTracker struct {
	attempts map[string]*loginAttemptEntry
	mutex    sync.RWMutex
	// Maximum failed attempts before blocking
	maxAttempts int
	// Block duration after max attempts reached
	blockDuration time.Duration
	// Cleanup interval for old entries
	cleanupInterval time.Duration
}

type loginAttemptEntry struct {
	failedAttempts int
	blockedUntil   *time.Time
	lastAttempt    time.Time
}

// NewRateLimiter creates a new rate limiter
// maxRequests: maximum requests per IP per window (e.g., 100)
// windowDuration: time window for rate limiting (e.g., 1 minute)
func NewRateLimiter(maxRequests int, windowDuration time.Duration) *RateLimiter {
	rl := &RateLimiter{
		requests:        make(map[string]*rateLimitEntry),
		maxRequests:     maxRequests,
		windowDuration:  windowDuration,
		cleanupInterval: windowDuration * 2, // Clean up entries older than 2 windows
	}

	// Start cleanup goroutine
	go rl.cleanup()

	return rl
}

// Allow checks if a request from the given IP should be allowed
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	now := time.Now()
	entry, exists := rl.requests[ip]

	if !exists {
		// First request from this IP
		rl.requests[ip] = &rateLimitEntry{
			count:     1,
			firstSeen: now,
			lastSeen:  now,
		}
		return true
	}

	// Check if window has expired
	if now.Sub(entry.firstSeen) > rl.windowDuration {
		// Reset the window
		entry.count = 1
		entry.firstSeen = now
		entry.lastSeen = now
		return true
	}

	// Check if limit exceeded
	if entry.count >= rl.maxRequests {
		return false
	}

	// Increment count
	entry.count++
	entry.lastSeen = now
	return true
}

// cleanup removes old entries to prevent memory leaks
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(rl.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		rl.mutex.Lock()
		now := time.Now()
		for ip, entry := range rl.requests {
			if now.Sub(entry.lastSeen) > rl.cleanupInterval {
				delete(rl.requests, ip)
			}
		}
		rl.mutex.Unlock()
	}
}

// NewLoginAttemptTracker creates a new login attempt tracker
// maxAttempts: maximum failed attempts before blocking (e.g., 6)
// blockDuration: duration to block IP after max attempts (e.g., 15 minutes)
func NewLoginAttemptTracker(maxAttempts int, blockDuration time.Duration) *LoginAttemptTracker {
	lat := &LoginAttemptTracker{
		attempts:        make(map[string]*loginAttemptEntry),
		maxAttempts:     maxAttempts,
		blockDuration:   blockDuration,
		cleanupInterval: blockDuration * 2, // Clean up entries older than 2 block durations
	}

	// Start cleanup goroutine
	go lat.cleanup()

	return lat
}

// RecordFailedAttempt records a failed login attempt for the given IP
func (lat *LoginAttemptTracker) RecordFailedAttempt(ip string) {
	lat.mutex.Lock()
	defer lat.mutex.Unlock()

	now := time.Now()
	entry, exists := lat.attempts[ip]

	if !exists {
		entry = &loginAttemptEntry{
			failedAttempts: 0,
			lastAttempt:    now,
		}
		lat.attempts[ip] = entry
	}

	entry.failedAttempts++
	entry.lastAttempt = now

	// If threshold reached, block the IP
	if entry.failedAttempts >= lat.maxAttempts {
		blockedUntil := now.Add(lat.blockDuration)
		entry.blockedUntil = &blockedUntil
	}
}

// RecordSuccess resets failed attempts for a successful login
func (lat *LoginAttemptTracker) RecordSuccess(ip string) {
	lat.mutex.Lock()
	defer lat.mutex.Unlock()

	// Reset attempts on successful login
	delete(lat.attempts, ip)
}

// IsBlocked checks if the IP is currently blocked
func (lat *LoginAttemptTracker) IsBlocked(ip string) bool {
	lat.mutex.RLock()
	defer lat.mutex.RUnlock()

	entry, exists := lat.attempts[ip]
	if !exists {
		return false
	}

	if entry.blockedUntil == nil {
		return false
	}

	// Check if block has expired
	if time.Now().After(*entry.blockedUntil) {
		// Block expired, but keep entry for tracking
		entry.blockedUntil = nil
		entry.failedAttempts = 0
		return false
	}

	return true
}

// GetRemainingBlockTime returns the remaining block time for an IP, or 0 if not blocked
func (lat *LoginAttemptTracker) GetRemainingBlockTime(ip string) time.Duration {
	lat.mutex.RLock()
	defer lat.mutex.RUnlock()

	entry, exists := lat.attempts[ip]
	if !exists || entry.blockedUntil == nil {
		return 0
	}

	remaining := time.Until(*entry.blockedUntil)
	if remaining < 0 {
		return 0
	}

	return remaining
}

// cleanup removes old entries to prevent memory leaks
func (lat *LoginAttemptTracker) cleanup() {
	ticker := time.NewTicker(lat.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		lat.mutex.Lock()
		now := time.Now()
		for ip, entry := range lat.attempts {
			// Remove if block expired and no recent activity
			if entry.blockedUntil != nil && now.After(*entry.blockedUntil) {
				if now.Sub(entry.lastAttempt) > lat.cleanupInterval {
					delete(lat.attempts, ip)
				}
			}
		}
		lat.mutex.Unlock()
	}
}

// getRemoteAddr extracts the remote IP address from the request
func getRemoteAddr(r *http.Request) string {
	// Check X-Forwarded-For header first (for reverse proxies)
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		// X-Forwarded-For can contain multiple IPs, take the first one
		if idx := strings.Index(forwarded, ","); idx != -1 {
			return strings.TrimSpace(forwarded[:idx])
		}
		return strings.TrimSpace(forwarded)
	}

	// Check X-Real-IP header (another common reverse proxy header)
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		return strings.TrimSpace(realIP)
	}

	// Fall back to RemoteAddr
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}

// RateLimitMiddleware provides general rate limiting for all requests
func RateLimitMiddleware(limiter *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := getRemoteAddr(r)

			if !limiter.Allow(ip) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", fmt.Sprintf("%.0f", limiter.windowDuration.Seconds()))
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]string{
					"error": "Too many requests. Please try again later.",
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// LoginAttemptMiddleware checks if IP is blocked from login attempts
// Returns JSON error with redirect URL for API calls
func LoginAttemptMiddleware(tracker *LoginAttemptTracker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := getRemoteAddr(r)

			if tracker.IsBlocked(ip) {
				remaining := tracker.GetRemainingBlockTime(ip)
				remainingSeconds := int(remaining.Seconds())

				// Return JSON error with redirect URL for client to handle
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", fmt.Sprintf("%.0f", remaining.Seconds()))
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":        "Too many failed login attempts. IP address temporarily blocked.",
					"blocked":      true,
					"redirectTo":   fmt.Sprintf("/login-blocked?seconds=%d", remainingSeconds),
					"retryAfter":   remainingSeconds,
					"blockedUntil": remaining.String(),
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
