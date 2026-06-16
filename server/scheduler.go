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
	"time"
)

type Scheduler struct {
	Controller *Controller
	Ticker     *time.Ticker
	cancel     chan any
	started    bool
}

func NewScheduler(controller *Controller) *Scheduler {
	return &Scheduler{
		Controller: controller,
		cancel:     make(chan any),
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

func (scheduler *Scheduler) run() {
	// Run cleanup operations in background goroutines to avoid blocking the scheduler ticker
	// This ensures the scheduler continues to run on schedule even if cleanup takes a long time

	// Prune database (audio and logs) - runs in background
	go func() {
		if err := scheduler.pruneDatabase(); err != nil {
			scheduler.Controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("scheduler.pruneDatabase: %s", err.Error()))
		}
	}()

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
	if scheduler.started {
		return errors.New("scheduler already started")
	} else {
		scheduler.started = true
	}

	// Run cleanup immediately on startup
	scheduler.run()

	// Then run every hour
	scheduler.Ticker = time.NewTicker(time.Hour)

	go func() {
		for {
			select {
			case <-scheduler.cancel:
				scheduler.Stop()
				return
			case <-scheduler.Ticker.C:
				scheduler.run()
			}
		}
	}()

	return nil
}

func (scheduler *Scheduler) Stop() error {
	if !scheduler.started {
		return errors.New("scheduler not started")
	}

	scheduler.Ticker.Stop()
	scheduler.Ticker = nil
	scheduler.started = false

	// Signal the goroutine to exit.
	select {
	case scheduler.cancel <- struct{}{}:
	default:
	}

	return nil
}
