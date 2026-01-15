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
	"fmt"
	"log"
	"sync"
	"time"
)

// DisconnectedClientState holds the state of a recently disconnected client
type DisconnectedClientState struct {
	User          *User
	LastSeen      time.Time
	MissedCalls   []*Call
	Livefeed      *Livefeed
	MaxBufferSize int
}

// ReconnectionManager manages reconnection states for disconnected clients
type ReconnectionManager struct {
	States       map[string]*DisconnectedClientState // Key: User ID or PIN
	mutex        sync.RWMutex
	HoldDuration time.Duration // How long to hold buffers
	MaxBufferSize int          // Maximum calls to buffer per user
	Enabled      bool
	controller   *Controller
}

// NewReconnectionManager creates a new reconnection manager
func NewReconnectionManager(controller *Controller, holdDuration time.Duration, maxBufferSize int, enabled bool) *ReconnectionManager {
	return &ReconnectionManager{
		States:        make(map[string]*DisconnectedClientState),
		mutex:         sync.RWMutex{},
		HoldDuration:  holdDuration,
		MaxBufferSize: maxBufferSize,
		Enabled:       enabled,
		controller:    controller,
	}
}

// SaveDisconnectedState saves the state of a disconnected client for potential reconnection
func (rm *ReconnectionManager) SaveDisconnectedState(client *Client) {
	if !rm.Enabled || client.User == nil {
		return
	}

	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	userKey := rm.getUserKey(client.User)
	
	// Create a deep copy of the livefeed matrix to preserve filter state
	livefeedCopy := &Livefeed{
		Matrix: make(map[uint]map[uint]bool),
	}
	
	// Copy the matrix
	for sysId, talkgroups := range client.Livefeed.Matrix {
		livefeedCopy.Matrix[sysId] = make(map[uint]bool)
		for tgId, enabled := range talkgroups {
			livefeedCopy.Matrix[sysId][tgId] = enabled
		}
	}

	rm.States[userKey] = &DisconnectedClientState{
		User:          client.User,
		LastSeen:      time.Now(),
		MissedCalls:   make([]*Call, 0, rm.MaxBufferSize),
		Livefeed:      livefeedCopy,
		MaxBufferSize: rm.MaxBufferSize,
	}

	log.Printf("[ReconnectionManager] Saved state for user %s (PIN: %s)", userKey, client.User.Pin)
}

// BufferCallForDisconnected buffers a call for disconnected clients who should receive it
func (rm *ReconnectionManager) BufferCallForDisconnected(call *Call) {
	if !rm.Enabled || call == nil {
		return
	}

	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	now := time.Now()
	
	for _, state := range rm.States {
		// Skip if grace period expired
		if now.Sub(state.LastSeen) > rm.HoldDuration {
			continue
		}

		// Check if user's filters would allow this call
		if !state.Livefeed.IsEnabled(call) {
			continue
		}

		// Check access permissions
		if rm.controller.requiresUserAuth() {
			if !rm.controller.userHasAccess(state.User, call) {
				continue
			}
		}

		// Add to buffer if not full
		if len(state.MissedCalls) < state.MaxBufferSize {
			state.MissedCalls = append(state.MissedCalls, call)
		} else {
			// Buffer full - remove oldest call and add new one (FIFO)
			state.MissedCalls = append(state.MissedCalls[1:], call)
		}
	}
}

// RestoreClientState restores buffered calls to a reconnecting client
func (rm *ReconnectionManager) RestoreClientState(client *Client) bool {
	if !rm.Enabled || client.User == nil {
		return false
	}

	rm.mutex.Lock()
	
	userKey := rm.getUserKey(client.User)
	state, exists := rm.States[userKey]
	
	if !exists {
		rm.mutex.Unlock()
		return false
	}

	// Check if still within grace period
	if time.Since(state.LastSeen) > rm.HoldDuration {
		delete(rm.States, userKey)
		rm.mutex.Unlock()
		log.Printf("[ReconnectionManager] Grace period expired for user %s (PIN: %s)", userKey, client.User.Pin)
		return false
	}

	// Get buffered calls before unlocking
	missedCalls := state.MissedCalls
	missedCount := len(missedCalls)
	disconnectDuration := time.Since(state.LastSeen)
	
	// Restore livefeed state
	client.Livefeed = state.Livefeed

	// Clean up saved state
	delete(rm.States, userKey)
	rm.mutex.Unlock()

	if missedCount == 0 {
		log.Printf("[ReconnectionManager] User %s (PIN: %s) reconnected after %.1fs - no missed calls", 
			userKey, client.User.Pin, disconnectDuration.Seconds())
		return true
	}

	// Send buffered calls in a goroutine to avoid blocking
	go func() {
		successCount := 0
		for _, call := range missedCalls {
			msg := &Message{Command: MessageCommandCall, Payload: call}
			
			select {
			case client.Send <- msg:
				successCount++
				// Small delay to preserve order and avoid overwhelming the client
				time.Sleep(5 * time.Millisecond)
			default:
				// Channel full, stop trying to avoid blocking
				log.Printf("[ReconnectionManager] Channel full while sending buffered calls to user %s (sent %d/%d)", 
					userKey, successCount, missedCount)
				return
			}
		}
		
		log.Printf("[ReconnectionManager] Successfully sent %d buffered calls to user %s (PIN: %s) after %.1fs disconnect", 
			successCount, userKey, client.User.Pin, disconnectDuration.Seconds())
	}()

	return true
}

// StartCleanup starts a background goroutine to clean up expired states
func (rm *ReconnectionManager) StartCleanup() {
	if !rm.Enabled {
		return
	}

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		log.Printf("[ReconnectionManager] Cleanup routine started (grace period: %v, max buffer: %d)", 
			rm.HoldDuration, rm.MaxBufferSize)

		for range ticker.C {
			rm.mutex.Lock()
			now := time.Now()
			expiredCount := 0
			totalDroppedCalls := 0

			for userKey, state := range rm.States {
				if now.Sub(state.LastSeen) > rm.HoldDuration {
					totalDroppedCalls += len(state.MissedCalls)
					delete(rm.States, userKey)
					expiredCount++
				}
			}
			
			rm.mutex.Unlock()

			if expiredCount > 0 {
				log.Printf("[ReconnectionManager] Cleaned up %d expired states (%d calls dropped)", 
					expiredCount, totalDroppedCalls)
			}
		}
	}()
}

// GetStats returns current statistics about the reconnection manager
func (rm *ReconnectionManager) GetStats() map[string]interface{} {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	totalBufferedCalls := 0
	for _, state := range rm.States {
		totalBufferedCalls += len(state.MissedCalls)
	}

	return map[string]interface{}{
		"enabled":            rm.Enabled,
		"disconnectedUsers":  len(rm.States),
		"totalBufferedCalls": totalBufferedCalls,
		"gracePeriod":        rm.HoldDuration.String(),
		"maxBufferSize":      rm.MaxBufferSize,
	}
}

// getUserKey generates a unique key for a user (prefer ID over PIN)
func (rm *ReconnectionManager) getUserKey(user *User) string {
	if user.Id != 0 {
		return fmt.Sprintf("id:%d", user.Id)
	}
	return fmt.Sprintf("pin:%s", user.Pin)
}

