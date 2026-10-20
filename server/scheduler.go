// Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
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
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type Scheduler struct {
	Controller *Controller

	// lifecycleMu guards started, done and the tickers. Start/Stop may be called from
	// different goroutines, and the loop goroutine never touches these fields: it works
	// from its own copies and exits when done is closed.
	lifecycleMu sync.Mutex
	started     bool
	done        chan struct{}
	Ticker      *time.Ticker
	pruneTicker *time.Ticker

	// Daily prune at Options.PruneTime.
	pruneMu      sync.Mutex
	lastPruneDay string // yyyy-mm-dd (in the instance zone) of the last scheduled prune
	scheduleKey  string // "HH:MM|zone" the state above belongs to
	pruning      atomic.Bool
}

func NewScheduler(controller *Controller) *Scheduler {
	return &Scheduler{
		Controller: controller,
	}
}

func (scheduler *Scheduler) pruneDatabase() error {
	scheduler.Controller.Logs.LogEvent(LogLevelInfo, "database pruning (audio and logs)")

	// Prune calls using hierarchical retention: talkgroup > system > global pruneDays.
	if err := scheduler.Controller.Calls.Prune(scheduler.Controller.Database, scheduler.Controller.Options.PruneDays); err != nil {
		return fmt.Errorf("prune calls failed: %v", err)
	}

	if scheduler.Controller.Options.PruneDays == 0 {
		return nil
	}

	if err := scheduler.Controller.Logs.Prune(scheduler.Controller.Database, scheduler.Controller.Options.PruneDays); err != nil {
		return fmt.Errorf("prune logs failed: %v", err)
	}

	return nil
}

// pruneTime returns the configured daily prune time ("" when unset = legacy behavior).
func (scheduler *Scheduler) pruneTime() string {
	options := scheduler.Controller.Options
	options.mutex.Lock()
	defer options.mutex.Unlock()
	return options.PruneTime
}

// runPrune prunes in the background, never running two prunes at once.
func (scheduler *Scheduler) runPrune() {
	if !scheduler.pruning.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer scheduler.pruning.Store(false)
		if err := scheduler.pruneDatabase(); err != nil {
			scheduler.Controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("scheduler.pruneDatabase: %s", err.Error()))
		}
	}()
}

// duePrune decides whether the daily prune should start now. It is pure state logic
// (no controller access) so it can be tested directly.
//
// Whenever the schedule itself changes (first check after start, or an edit to the prune
// time or time zone) a slot that has already passed today counts as done: saving a new
// time, or restarting, must never cause an immediate prune outside the chosen slot.
func (scheduler *Scheduler) duePrune(now time.Time, hhmm string) bool {
	slot, ok := pruneSlot(now, hhmm)
	if !ok {
		return false
	}
	day := now.Format("2006-01-02")
	key := hhmm + "|" + now.Location().String()

	scheduler.pruneMu.Lock()
	defer scheduler.pruneMu.Unlock()

	if key != scheduler.scheduleKey {
		scheduler.scheduleKey = key
		if now.Before(slot) {
			scheduler.lastPruneDay = ""
		} else {
			scheduler.lastPruneDay = day
		}
	}
	if now.Before(slot) || scheduler.lastPruneDay == day {
		return false
	}
	scheduler.lastPruneDay = day
	return true
}

// checkScheduledPrune runs the prune once per day when the instance's clock passes its
// configured prune time. It is a no-op when no prune time is set.
func (scheduler *Scheduler) checkScheduledPrune(now time.Time) {
	hhmm := scheduler.pruneTime()
	if hhmm == "" {
		return
	}
	now = now.In(scheduler.Controller.Options.Location())
	if !scheduler.duePrune(now, hhmm) {
		return
	}
	scheduler.Controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("scheduled prune starting (daily at %s %s)", hhmm, now.Location()))
	scheduler.runPrune()
}

func (scheduler *Scheduler) run() {
	// Run cleanup operations in background goroutines to avoid blocking the scheduler ticker
	// This ensures the scheduler continues to run on schedule even if cleanup takes a long time

	// Prune database (audio and logs) - runs in background. With a configured prune time
	// the daily check in checkScheduledPrune does this instead, so instances do not all
	// prune at startup and on the hour together.
	if scheduler.pruneTime() == "" {
		scheduler.runPrune()
	}

	// Cleanup old alerts (runs periodically, not just when alerts are created) - runs in background
	if scheduler.Controller.AlertEngine != nil {
		go func() {
			scheduler.Controller.AlertEngine.cleanupOldAlerts()
		}()
	}

	// Cleanup old system alerts (runs periodically) - runs in background
	go func() {
		scheduler.Controller.CleanupOldSystemAlerts()
	}()

	// Prune authMutexes entries for users that no longer exist
	go scheduler.Controller.pruneAuthMutexes()

	// Disable unit alias auto-learn when the rollout timer elapses
	go scheduler.Controller.expireAutoLearnUnitAliases()

	// Disable tone auto-learn when the rollout timer elapses
	go scheduler.Controller.expireAutoLearnToneSets()
}

func (scheduler *Scheduler) Start() error {
	scheduler.lifecycleMu.Lock()
	defer scheduler.lifecycleMu.Unlock()

	if scheduler.started {
		return errors.New("scheduler already started")
	}
	scheduler.started = true

	// Run cleanup immediately on startup
	scheduler.run()

	// Then run every hour, and check the daily prune slot every minute.
	ticker := time.NewTicker(time.Hour)
	pruneTicker := time.NewTicker(time.Minute)
	done := make(chan struct{})
	scheduler.Ticker, scheduler.pruneTicker, scheduler.done = ticker, pruneTicker, done

	// The goroutine uses only its own copies, so Stop() clearing the fields can never
	// leave it dereferencing nil, and closing done always ends it (even mid-run).
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				scheduler.run()
			case now := <-pruneTicker.C:
				scheduler.checkScheduledPrune(now)
			}
		}
	}()

	return nil
}

func (scheduler *Scheduler) Stop() error {
	scheduler.lifecycleMu.Lock()
	defer scheduler.lifecycleMu.Unlock()

	if !scheduler.started {
		return errors.New("scheduler not started")
	}

	if scheduler.Ticker != nil {
		scheduler.Ticker.Stop()
		scheduler.Ticker = nil
	}
	if scheduler.pruneTicker != nil {
		scheduler.pruneTicker.Stop()
		scheduler.pruneTicker = nil
	}
	if scheduler.done != nil {
		close(scheduler.done)
		scheduler.done = nil
	}
	scheduler.started = false

	return nil
}
