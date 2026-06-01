package main

import (
	"testing"
	"time"
)

func TestAlertCooldown_PreAlertDoesNotBlockToneAlert(t *testing.T) {
	engine := &AlertEngine{
		lastPreAlertFiredAt:  make(map[uint64]time.Time),
		lastToneAlertFiredAt: make(map[uint64]time.Time),
		toneAlertDispatched:  make(map[uint64]struct{}),
		controller: &Controller{
			Systems: &Systems{
				List: []*System{{
					Talkgroups: &Talkgroups{
						List: []*Talkgroup{{Id: 10, AlertCooldownSeconds: 120}},
					},
				}},
			},
		},
	}

	tgId := uint64(10)

	engine.recordPreAlertCooldown(tgId)
	if !engine.isPreAlertCooldownActive(tgId) {
		t.Fatal("pre-alert cooldown should be active after recording")
	}
	if engine.isToneAlertCooldownActive(tgId) {
		t.Fatal("tone alert cooldown should not be active after pre-alert only")
	}

	engine.recordToneAlertCooldown(tgId)
	if !engine.isToneAlertCooldownActive(tgId) {
		t.Fatal("tone alert cooldown should be active after recording")
	}
}

func TestAlertCooldown_SeparateWindows(t *testing.T) {
	engine := &AlertEngine{
		lastPreAlertFiredAt:  make(map[uint64]time.Time),
		lastToneAlertFiredAt: make(map[uint64]time.Time),
		controller: &Controller{
			Systems: &Systems{
				List: []*System{{
					Talkgroups: &Talkgroups{
						List: []*Talkgroup{{Id: 5, AlertCooldownSeconds: 120}},
					},
				}},
			},
		},
	}

	engine.cooldownMu.Lock()
	engine.lastPreAlertFiredAt[5] = time.Now().Add(-130 * time.Second)
	engine.lastToneAlertFiredAt[5] = time.Now()
	engine.cooldownMu.Unlock()

	if engine.isPreAlertCooldownActive(5) {
		t.Fatal("expired pre-alert cooldown should not block")
	}
	if !engine.isToneAlertCooldownActive(5) {
		t.Fatal("recent tone alert cooldown should block")
	}
}

func TestAlertCooldown_ToneSourceTalkgroupId(t *testing.T) {
	engine := &AlertEngine{
		controller: &Controller{
			Systems: &Systems{
				List: []*System{{
					Talkgroups: &Talkgroups{
						List: []*Talkgroup{
							{Id: 1, AlertCooldownSeconds: 60},
							{Id: 2, AlertCooldownSeconds: 0},
						},
					},
				}},
			},
		},
	}

	call := &Call{
		ToneSourceTalkgroupId: 1,
		Talkgroup:             &Talkgroup{Id: 2},
	}
	if engine.cooldownTalkgroupId(call) != 1 {
		t.Fatalf("expected tone source talkgroup 1, got %d", engine.cooldownTalkgroupId(call))
	}
	if engine.getAlertCooldownSeconds(engine.cooldownTalkgroupId(call)) != 60 {
		t.Fatal("expected 60s cooldown from tone source talkgroup")
	}
}

func TestClaimToneAlertDispatch(t *testing.T) {
	engine := &AlertEngine{toneAlertDispatched: make(map[uint64]struct{})}
	if !engine.claimToneAlertDispatch(42) {
		t.Fatal("first claim should succeed")
	}
	if engine.claimToneAlertDispatch(42) {
		t.Fatal("second claim for same call should fail")
	}
}
