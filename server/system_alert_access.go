// Copyright (C) 2025 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"encoding/json"
)

func parseSystemAlertData(alert *SystemAlert) *SystemAlertData {
	if alert == nil || alert.Data == "" || alert.Data == "{}" {
		return nil
	}
	var data SystemAlertData
	if err := json.Unmarshal([]byte(alert.Data), &data); err != nil {
		return nil
	}
	return &data
}

func (controller *Controller) userHasSystemAlertAccess(user *User, alert *SystemAlert) bool {
	if user == nil || alert == nil {
		return false
	}
	if !isSystemAlertVisibleToUser(alert.AlertType, user.SystemAdmin) {
		return false
	}
	if user.SystemAdmin {
		return true
	}

	data := parseSystemAlertData(alert)

	switch alert.AlertType {
	case "manual":
		return true
	case "no_audio", "no_audio_received":
		if data == nil || data.SystemId == 0 {
			return false
		}
		return user.HasSystemNoAudioAlertSystemId(data.SystemId)
	case "api_key_no_audio", "sdrtrunk_control_lost", "sdrtrunk_tuner_error", "sdrtrunk_uploader_error", "sdrtrunk_offline", "sdrtrunk_sample_overflow":
		if data == nil || data.ApiKeyId == 0 {
			return false
		}
		return user.HasApiKeyNoAudioAlertApiKeyId(data.ApiKeyId)
	case "tone_detection_issue":
		if data == nil || data.SystemId == 0 || data.TalkgroupId == 0 {
			return false
		}
		sys, ok := controller.Systems.GetSystemById(data.SystemId)
		if !ok {
			return false
		}
		tg, ok := sys.Talkgroups.GetTalkgroupById(data.TalkgroupId)
		if !ok {
			return false
		}
		return controller.userHasTalkgroupScopeAccess(user, sys.SystemRef, tg.TalkgroupRef)
	default:
		return false
	}
}

func (controller *Controller) userShouldReceiveSystemAlertPush(user *User, alertType string, dataJSON string) bool {
	if user == nil || !user.Verified {
		return false
	}
	if user.SystemAdmin {
		return true
	}

	var data SystemAlertData
	if dataJSON != "" {
		_ = json.Unmarshal([]byte(dataJSON), &data)
	}

	switch alertType {
	case "no_audio", "no_audio_received":
		if !user.PushSystemNoAudioAlerts || data.SystemId == 0 {
			return false
		}
		return user.HasSystemNoAudioAlertSystemId(data.SystemId)
	case "api_key_no_audio", "sdrtrunk_control_lost", "sdrtrunk_tuner_error", "sdrtrunk_uploader_error", "sdrtrunk_offline", "sdrtrunk_sample_overflow":
		if !user.PushApiKeyNoAudioAlerts || data.ApiKeyId == 0 {
			return false
		}
		return user.HasApiKeyNoAudioAlertApiKeyId(data.ApiKeyId)
	default:
		return false
	}
}
