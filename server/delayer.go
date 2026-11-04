// Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
// Modified by Thinline Dynamic Solutions
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
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"
)

type Delayer struct {
	controller *Controller
	mutex      sync.Mutex
	timers     map[uint64]time.Timer
}

func NewDelayer(controller *Controller) *Delayer {
	return &Delayer{
		controller: controller,
		mutex:      sync.Mutex{},
		timers:     make(map[uint64]time.Timer),
	}
}

func (delayer *Delayer) CanDelay(call *Call) bool {
	// Prevent infinite recursion - already delayed calls can't be delayed again
	if call.Delayed {
		return false
	}
	return delayer.getTimestamp(call).After(time.Now())
}

func (delayer *Delayer) CanDelayForClient(call *Call, client *Client) bool {
	// Prevent infinite recursion - already delayed calls can't be delayed again
	if call.Delayed {
		return false
	}
	return delayer.getTimestampForClient(call, client).After(time.Now())
}

func (delayer *Delayer) Delay(call *Call) {
	if call.Delayed {
		return
	}

	delay := delayer.getSystemDelay(call)
	if delayer.controller.requiresUserAuth() {
		delayer.controller.Clients.mutex.Lock()
		for client := range delayer.controller.Clients.Map {
			if client.User == nil {
				if !delayer.controller.anonymousAllowed(client.ListenLink) || !delayer.controller.anonymousHasCallAccess(client.ListenLink, call) {
					continue
				}
			} else if !delayer.controller.userHasAccess(client.User, call) {
				continue
			}
			clientDelay := delayer.controller.clientEffectiveDelay(client, call, delay)
			if clientDelay > 0 && (delay == 0 || clientDelay < delay) {
				delay = clientDelay
			}
		}
		delayer.controller.Clients.mutex.Unlock()
	}

	logError := func(err error) {
		delayer.controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("delayer.delay: %s", err.Error()))
	}

	if delay > 0 {
		call.Delayed = true

		timestamp := call.Timestamp.Add(time.Duration(delay) * time.Minute)
		remaining := time.Until(timestamp)

		if err := delayer.push(call, timestamp); err == nil {
			delayer.timers[call.Id] = *time.AfterFunc(remaining, func() {
				if err := delayer.pop(call); err != nil {
					logError(err)
				}

				delete(delayer.timers, call.Id)

				if delayer.controller.DelayModeration.IsRemoved(call.Id) {
					return
				}

				// Clear the global delayed flag so individual client delays can be checked
				call.Delayed = false

				// Use a direct call to avoid circular reference
				go delayer.controller.Downstreams.Send(delayer.controller, call)
				go delayer.controller.Clients.EmitCall(delayer.controller, call)
			})

		} else {
			logError(err)
		}

	} else {
		// Use a direct call to avoid circular reference
		go delayer.controller.Downstreams.Send(delayer.controller, call)
		go delayer.controller.Clients.EmitCall(delayer.controller, call)
	}
}

func (delayer *Delayer) DelayForClient(call *Call, client *Client) {
	// Note: Don't check call.Delayed here - this is for per-client delays
	// The global Delayed flag is for system-wide delays only

	delay := delayer.getEffectiveDelayForClient(call, client)

	if delay > 0 {
		// For client-specific delays, schedule a timer to send later
		// DO NOT set call.Delayed = true (that's for global delays only)
		timestamp := delayer.getTimestampForClient(call, client)
		remaining := time.Until(timestamp)

		// Only schedule if delay hasn't already passed
		if remaining > 0 {
			// Schedule delayed send for this specific client only
			timer := time.AfterFunc(remaining, func() {
				// Check if client still exists before sending
				if client.Send == nil || delayer.controller.DelayModeration.IsRemoved(call.Id) {
					return
				}
				// Non-blocking send to prevent deadlock
				msg := &Message{Command: MessageCommandCall, Payload: call}
				select {
				case client.Send <- msg:
					// Message sent successfully
				default:
					// Channel full, skip to avoid blocking
				}
			})
			delayer.controller.DelayModeration.Hold(call.Id, timer, timestamp)
		} else {
			// Delay already passed, send immediately
			msg := &Message{Command: MessageCommandCall, Payload: call}
			select {
			case client.Send <- msg:
			default:
			}
		}

	} else {
		// Send immediately to this client with non-blocking send
		msg := &Message{Command: MessageCommandCall, Payload: call}
		select {
		case client.Send <- msg:
			// Message sent successfully
		default:
			// Channel full, skip to avoid blocking
		}
	}
}

func (delayer *Delayer) Start() error {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("delayer restore panic: %v", r)
			}
		}()
		if err := delayer.restore(); err != nil {
			log.Printf("delayer restore: %v", err)
		}
	}()
	return nil
}

func (delayer *Delayer) restore() error {
	var (
		err   error
		query string
		rows  *sql.Rows
	)

	delayer.mutex.Lock()

	callIds := map[uint64]int64{}

	formatError := errorFormatter("delayer", "restore")

	query = `SELECT "callId", "timestamp" from "delayed"`
	if rows, err = delayer.controller.Database.Sql.Query(query); err != nil {
		return formatError(err, query)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			callId    uint64
			timestamp int64
		)

		if err = rows.Scan(&callId, &timestamp); err != nil {
			break
		}

		callIds[callId] = timestamp
	}

	if err != nil {
		return formatError(err, "")
	}

	if len(callIds) > 0 {
		query = `DELETE FROM "delayed"`
		if _, err = delayer.controller.Database.Sql.Exec(query); err != nil {
			return formatError(err, query)
		}
	}

	delayer.mutex.Unlock()

	for callId, timestamp := range callIds {
		if call, err := delayer.controller.Calls.GetCall(callId); err == nil {
			// Don't re-delay calls that are already marked as delayed
			if call.Delayed {
				// Skip already delayed calls to prevent circular reference
				continue
			}

			call.Delayed = true

			if time.UnixMilli(timestamp).Before(time.Now()) {
				// Delay has already expired, clear the delayed flag
				call.Delayed = false
				// Use direct calls to avoid circular reference
				go delayer.controller.Downstreams.Send(delayer.controller, call)
				go delayer.controller.Clients.EmitCall(delayer.controller, call)

			} else {
				// Only delay if the call isn't already delayed and has a valid delay time
				// Use system delay for restoration since we don't have client context
				if delayer.getSystemDelay(call) > 0 {
					delayer.Delay(call)
				} else {
					// Use direct calls to avoid circular reference
					go delayer.controller.Downstreams.Send(delayer.controller, call)
					go delayer.controller.Clients.EmitCall(delayer.controller, call)
				}
			}
		}
	}

	return nil
}

func (delayer *Delayer) getEffectiveDelayForClient(call *Call, client *Client) uint {
	if call == nil || call.System == nil || call.Talkgroup == nil {
		return 0
	}

	return delayer.controller.clientEffectiveDelay(client, call, delayer.getSystemDelay(call))
}

func (delayer *Delayer) getSystemDelay(call *Call) uint {
	// Check talkgroup delay first (highest priority)
	// Note: All delays are in MINUTES and affect live audio streaming to clients
	if call.Talkgroup.Delay > 0 {
		return call.Talkgroup.Delay
	}

	// Check system delay second (medium priority)
	if call.System.Delay > 0 {
		return call.System.Delay
	}

	// Use default system delay as fallback (lowest priority)
	return delayer.controller.Options.DefaultSystemDelay
}

func (delayer *Delayer) getTimestamp(call *Call) time.Time {
	// Safety check to prevent nil pointer dereference
	if call == nil || call.Timestamp.IsZero() {
		return time.Now()
	}

	// This function is deprecated - use getTimestampForClient instead
	// For backward compatibility, use system delay
	delay := delayer.getSystemDelay(call)

	return call.Timestamp.Add(time.Duration(delay) * time.Minute)
}

func (delayer *Delayer) getTimestampForClient(call *Call, client *Client) time.Time {
	// Safety check to prevent nil pointer dereference
	if call == nil || call.Timestamp.IsZero() {
		return time.Now()
	}

	delay := delayer.getEffectiveDelayForClient(call, client)
	if delay == 0 {
		return time.Now()
	}

	return call.Timestamp.Add(time.Duration(delay) * time.Minute)
}

func (delayer *Delayer) pop(call *Call) error {
	delayer.mutex.Lock()
	defer delayer.mutex.Unlock()

	formatError := errorFormatter("delayer", "pop")

	query := fmt.Sprintf(`DELETE FROM "delayed" WHERE "callId" = %d`, call.Id)
	if _, err := delayer.controller.Database.Sql.Exec(query); err != nil {
		return formatError(err, query)
	}

	return nil
}

func (delayer *Delayer) push(call *Call, timestamp time.Time) error {
	delayer.mutex.Lock()
	defer delayer.mutex.Unlock()

	formatError := errorFormatter("delayer", "push")

	query := fmt.Sprintf(`INSERT INTO "delayed" ("callId", "timestamp") VALUES (%d, %d)`, call.Id, timestamp.UnixMilli())
	if _, err := delayer.controller.Database.Sql.Exec(query); err != nil {
		return formatError(err, query)
	}

	return nil
}

// IsCallDelayed checks if a call is currently delayed and not yet available for playback
func (delayer *Delayer) IsCallDelayed(callId uint64) bool {
	delayer.mutex.Lock()
	defer delayer.mutex.Unlock()

	// Check if the call exists in the delayed table
	var timestamp int64
	query := fmt.Sprintf(`SELECT "timestamp" FROM "delayed" WHERE "callId" = %d`, callId)

	if err := delayer.controller.Database.Sql.QueryRow(query).Scan(&timestamp); err != nil {
		// If there's an error or no rows, the call is not delayed
		return false
	}

	// Check if the delay period has expired
	delayTime := time.UnixMilli(timestamp)
	return time.Now().Before(delayTime)
}
