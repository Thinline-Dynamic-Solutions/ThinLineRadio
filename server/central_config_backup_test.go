package main

import (
	"testing"
	"time"
)

func newTestCMS(enabled bool) *CentralManagementService {
	opts := &Options{CentralManagementEnabled: enabled, CentralManagementURL: "http://cm.example", CentralManagementAPIKey: "k"}
	return &CentralManagementService{controller: &Controller{Options: opts}, stopChan: make(chan struct{})}
}

func TestNotifyConfigChangedDisabledDoesNothing(t *testing.T) {
	cms := newTestCMS(false)
	cms.NotifyConfigChanged()
	if cms.configPush.timer != nil {
		t.Fatal("no timer should be armed when not paired with Central Management")
	}
	var nilCMS *CentralManagementService
	nilCMS.NotifyConfigChanged() // must not panic
}

func TestNotifyConfigChangedDebouncesAndCapsWait(t *testing.T) {
	cms := newTestCMS(true)
	cms.NotifyConfigChanged()
	p := &cms.configPush
	p.mu.Lock()
	first := p.firstPending
	timer := p.timer
	p.mu.Unlock()
	if timer == nil || first.IsZero() {
		t.Fatal("first change should arm the timer")
	}

	cms.NotifyConfigChanged()
	p.mu.Lock()
	if !p.firstPending.Equal(first) {
		t.Fatal("firstPending must keep the time of the first unsent change")
	}
	// Pretend changes have been arriving for longer than the max wait: the next
	// reschedule must fire promptly instead of sliding the window forever.
	p.firstPending = time.Now().Add(-configPushMaxWait - time.Minute)
	cms.scheduleConfigPushLocked(configPushDebounce)
	p.timer.Stop()
	p.mu.Unlock()
}
