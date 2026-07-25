// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"fmt"
	"time"
)

// TriggerTranscriptAlerts fires transcript alerts for alerting talkgroups (no tone/keyword gate).
func (engine *AlertEngine) TriggerTranscriptAlerts(call *Call) {
	if call == nil || call.System == nil || call.Talkgroup == nil {
		return
	}
	if !call.Talkgroup.AlertingTalkgroup {
		return
	}
	if call.System != nil && !call.System.AlertsEnabled {
		return
	}
	if !call.Talkgroup.AlertsEnabled {
		return
	}
	if !engine.controller.isVoiceForToneAlerts(call.Transcript) {
		return
	}

	systemId := call.System.Id
	talkgroupId := call.Talkgroup.Id
	// Resolve refs → DB ids (same as pre-alerts) so preference keys match AlertsHandler.
	if systemId == 0 && call.System.SystemRef > 0 {
		if id, ok := engine.controller.IdLookupsCache.GetSystemId(call.System.SystemRef); ok {
			systemId = id
		}
	}
	if talkgroupId == 0 && call.Talkgroup.TalkgroupRef > 0 && systemId > 0 {
		if id, ok := engine.controller.IdLookupsCache.GetTalkgroupId(systemId, call.Talkgroup.TalkgroupRef); ok {
			talkgroupId = id
		}
	}
	if systemId == 0 || talkgroupId == 0 {
		engine.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf(
			"transcript alert skipped for call %d: systemId=%d talkgroupId=%d unresolved",
			call.Id, systemId, talkgroupId,
		))
		return
	}

	_, alertExists := engine.controller.RecentAlertsCache.AlertExists(
		call.Id, systemId, talkgroupId, "transcript", "", "",
	)
	if alertExists {
		return
	}

	transcriptSnippet := call.Transcript
	if len(transcriptSnippet) > 200 {
		transcriptSnippet = transcriptSnippet[:200] + "..."
	}

	engine.createAlert(&AlertRecord{
		CallId:            call.Id,
		SystemId:          systemId,
		TalkgroupId:       talkgroupId,
		AlertType:         "transcript",
		ToneDetected:      false,
		TranscriptSnippet: transcriptSnippet,
		CreatedAt:         time.Now().UnixMilli(),
	})

	systemLabel := call.System.Label
	talkgroupLabel := call.Talkgroup.Label

	userIds := engine.controller.PreferencesCache.GetUsersForTalkgroup(systemId, talkgroupId)
	var eligibleUsers []uint64
	for _, userId := range userIds {
		pref := engine.controller.PreferencesCache.GetPreference(userId, systemId, talkgroupId)
		if pref != nil && pref.AlertEnabled {
			if !engine.controller.userEligibleForTalkgroupAlert(userId, call) {
				continue
			}
			eligibleUsers = append(eligibleUsers, userId)
			go engine.sendAlertNotification(userId, call.Id, "transcript")
		}
	}

	if len(eligibleUsers) == 0 {
		engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("transcript alert: no users with alerts enabled for call %d on alerting talkgroup %d", call.Id, call.Talkgroup.TalkgroupRef))
		return
	}

	cooldownTgId := engine.cooldownTalkgroupId(call)
	if engine.isToneAlertCooldownActive(cooldownTgId) {
		secs := engine.getAlertCooldownSeconds(cooldownTgId)
		engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
			"transcript alert cooldown active for talkgroup %d (cooldown=%ds) — skipping push for call %d (DB record kept)",
			cooldownTgId, secs, call.Id,
		))
		return
	}

	go engine.controller.sendBatchedPushNotification(eligibleUsers, "transcript", call, systemLabel, talkgroupLabel, "", nil)
	engine.recordToneAlertCooldown(cooldownTgId)

	engine.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
		"transcript alert sent for call %d on alerting talkgroup %d to %d user(s)",
		call.Id, call.Talkgroup.TalkgroupRef, len(eligibleUsers),
	))
}
