// Copyright (C) 2026 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const sdrtrunkHealthStale = 90 * time.Second

// sdrtrunkOfflineStartupGrace keeps a restarted server from calling every site
// down before they have reconnected and reported.
const sdrtrunkOfflineStartupGrace = 3 * time.Minute

// sdrtrunkAlertsOff in AlertsMutedUntil turns a site's alerts off until an admin
// turns them back on. Any other value is a mute end time in Unix milliseconds.
const sdrtrunkAlertsOff int64 = -1

const defaultSdrtrunkOfflineRepeatMinutes = 30

const (
	sdrtrunkAlertControlLost    = "sdrtrunk_control_lost"
	sdrtrunkAlertTunerError     = "sdrtrunk_tuner_error"
	sdrtrunkAlertUploaderError  = "sdrtrunk_uploader_error"
	sdrtrunkAlertOffline        = "sdrtrunk_offline"
	sdrtrunkAlertSampleOverflow = "sdrtrunk_sample_overflow"
)

// SdrtrunkHealthRecord is the latest one-way health report from an SDRTrunk uploader.
type SdrtrunkHealthRecord struct {
	ApiKeyId      uint64
	ApiKeyIdent   string
	SystemRef     uint
	SystemLabel   string
	ReceivedAt    int64
	Reporting     bool
	Version       string
	UploaderState string
	ControlLost   bool
	TunerError    bool
	UploaderError bool
	Report        string

	AlertsMutedUntil int64
}

func (record *SdrtrunkHealthRecord) alertsMuted(now time.Time) bool {
	return record.AlertsMutedUntil == sdrtrunkAlertsOff || record.AlertsMutedUntil > now.UnixMilli()
}

type sdrtrunkHealthRequest struct {
	Key           string   `json:"key"`
	System        uint     `json:"system"`
	Reporting     bool     `json:"reporting"`
	Role          string   `json:"role"`
	Alerts        []string `json:"alerts"`
	Version       string   `json:"version"`
	UptimeSeconds int64    `json:"uptimeSeconds"`
	Uploader      struct {
		Name       string `json:"name"`
		State      string `json:"state"`
		StateLabel string `json:"stateLabel"`
		Error      bool   `json:"error"`
	} `json:"uploader"`
	Tuners []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Status      string `json:"status"`
		StatusLabel string `json:"statusLabel"`
		Error       string `json:"error"`
	} `json:"tuners"`
	Channels []struct {
		Name                string `json:"name"`
		System              string `json:"system"`
		Site                string `json:"site"`
		Decoder             string `json:"decoder"`
		Trunking            bool   `json:"trunking"`
		State               string `json:"state"`
		SecondsSinceControl int64  `json:"secondsSinceControl"`
		ControlLost         bool   `json:"controlLost"`
		NoTuner             bool   `json:"noTuner"`
		Overflow            bool   `json:"overflow"`
	} `json:"channels"`
}

// SdrtrunkHealthHandler accepts a one-way health report from SDRTrunk.
func (api *Api) SdrtrunkHealthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("Unsupported method\n"))
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		api.exitWithError(w, http.StatusBadRequest, "Invalid health report")
		return
	}

	code, message := api.ingestSdrtrunkBody(body)
	w.WriteHeader(code)
	if code == http.StatusOK {
		w.Write([]byte("ok\n"))
		return
	}
	w.Write([]byte(message + "\n"))
}

func apikeyAllowsSystem(apikey *Apikey, systemRef uint) bool {
	if apikey == nil || systemRef == 0 {
		return false
	}

	switch systems := apikey.Systems.(type) {
	case string:
		return systems == "*"
	case []any:
		for _, entry := range systems {
			record, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			switch id := record["id"].(type) {
			case float64:
				if uint(id) == systemRef {
					return true
				}
			case int:
				if uint(id) == systemRef {
					return true
				}
			case uint:
				if id == systemRef {
					return true
				}
			}
		}
	}

	return false
}

func (controller *Controller) applySdrtrunkHealth(apikey *Apikey, report sdrtrunkHealthRequest, raw []byte) {
	record := &SdrtrunkHealthRecord{
		ApiKeyId:      apikey.Id,
		ApiKeyIdent:   apikey.Ident,
		SystemRef:     report.System,
		ReceivedAt:    time.Now().UnixMilli(),
		Reporting:     report.Reporting,
		Version:       report.Version,
		UploaderState: report.Uploader.State,
		UploaderError: report.Reporting && (report.Uploader.Error || sdrtrunkUploaderStateIsError(report.Uploader.State)),
		Report:        string(raw),
	}

	if system, ok := controller.Systems.GetSystemByRef(report.System); ok {
		record.SystemLabel = system.Label
	}

	var controlDetails []string
	var tunerDetails []string
	var overflowDetails []string

	if report.Reporting && report.Role != "upload" {
		for _, channel := range report.Channels {
			label := sdrtrunkChannelLabel(channel.Name, channel.System, channel.Site)
			if channel.Trunking && channel.ControlLost {
				record.ControlLost = true
				controlDetails = append(controlDetails, fmt.Sprintf("%s (%s) for %d seconds", label, channel.Decoder, channel.SecondsSinceControl))
			}
			if channel.NoTuner {
				record.TunerError = true
				tunerDetails = append(tunerDetails, fmt.Sprintf("%s has no tuner", label))
			}
			if channel.Overflow {
				detail := label
				if strings.TrimSpace(channel.Decoder) != "" {
					detail = fmt.Sprintf("%s (%s)", label, channel.Decoder)
				}
				overflowDetails = append(overflowDetails, detail)
			}
		}

		for _, tuner := range report.Tuners {
			if strings.EqualFold(tuner.Status, "ERROR") {
				record.TunerError = true
				name := tuner.Name
				if name == "" {
					name = tuner.ID
				}
				detail := name + " is in error"
				if strings.TrimSpace(tuner.Error) != "" {
					detail += ": " + strings.TrimSpace(tuner.Error)
				}
				tunerDetails = append(tunerDetails, detail)
			}
		}
	}

	controller.storeSdrtrunkHealth(record)
	controller.syncSdrtrunkAlerts(record, controlDetails, tunerDetails, overflowDetails)
}

func sdrtrunkUploaderStateIsError(state string) bool {
	switch state {
	case "ERROR", "CONFIGURATION_ERROR", "INVALID_CREDENTIALS", "INVALID_MOUNT_POINT", "INVALID_SETTINGS",
		"UNSUPPORTED_AUDIO_FORMAT", "NO_SERVER", "NETWORK_UNAVAILABLE", "DISCONNECTED":
		return true
	default:
		return false
	}
}

func sdrtrunkChannelLabel(name, system, site string) string {
	parts := make([]string, 0, 3)
	for _, part := range []string{system, site, name} {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return "channel"
	}
	return strings.Join(parts, " / ")
}

func (controller *Controller) storeSdrtrunkHealth(record *SdrtrunkHealthRecord) {
	controller.sdrtrunkHealthMu.Lock()
	if controller.sdrtrunkHealth == nil {
		controller.sdrtrunkHealth = make(map[uint64]*SdrtrunkHealthRecord)
	}
	previous := controller.sdrtrunkHealth[record.ApiKeyId]
	if previous != nil {
		record.AlertsMutedUntil = previous.AlertsMutedUntil
	}
	controller.sdrtrunkHealth[record.ApiKeyId] = record
	controller.sdrtrunkHealthMu.Unlock()

	if previous == nil || previous.Reporting != record.Reporting || previous.ControlLost != record.ControlLost ||
		previous.TunerError != record.TunerError || previous.UploaderError != record.UploaderError ||
		previous.UploaderState != record.UploaderState {
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("sdrtrunk health from '%s' system %d: reporting=%t controlLost=%t tunerError=%t uploader=%s",
			record.ApiKeyIdent, record.SystemRef, record.Reporting, record.ControlLost, record.TunerError, record.UploaderState))
	}

	if controller.Database == nil || controller.Database.Sql == nil {
		return
	}

	_, err := controller.Database.Sql.Exec(`
		INSERT INTO "sdrtrunkHealth" ("apikeyId", "systemRef", "receivedAt", "reporting", "version", "uploaderState", "controlLost", "tunerError", "uploaderError", "report")
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT ("apikeyId") DO UPDATE SET
			"systemRef" = EXCLUDED."systemRef",
			"receivedAt" = EXCLUDED."receivedAt",
			"reporting" = EXCLUDED."reporting",
			"version" = EXCLUDED."version",
			"uploaderState" = EXCLUDED."uploaderState",
			"controlLost" = EXCLUDED."controlLost",
			"tunerError" = EXCLUDED."tunerError",
			"uploaderError" = EXCLUDED."uploaderError",
			"report" = EXCLUDED."report"
	`, record.ApiKeyId, record.SystemRef, record.ReceivedAt, record.Reporting, record.Version, record.UploaderState,
		record.ControlLost, record.TunerError, record.UploaderError, record.Report)
	if err != nil {
		controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("failed to store sdrtrunk health for API key %d: %v", record.ApiKeyId, err))
	}
}

func (controller *Controller) loadSdrtrunkHealth() {
	if controller.Database == nil || controller.Database.Sql == nil {
		return
	}

	rows, err := controller.Database.Sql.Query(`
		SELECT "apikeyId", "systemRef", "receivedAt", "reporting", "version", "uploaderState", "controlLost", "tunerError", "uploaderError", "report", "alertsMutedUntil"
		FROM "sdrtrunkHealth"
	`)
	if err != nil {
		controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("failed to load sdrtrunk health: %v", err))
		return
	}
	defer rows.Close()

	loaded := make(map[uint64]*SdrtrunkHealthRecord)
	for rows.Next() {
		record := &SdrtrunkHealthRecord{}
		if err := rows.Scan(&record.ApiKeyId, &record.SystemRef, &record.ReceivedAt, &record.Reporting, &record.Version,
			&record.UploaderState, &record.ControlLost, &record.TunerError, &record.UploaderError, &record.Report, &record.AlertsMutedUntil); err != nil {
			continue
		}
		if apikey, ok := controller.Apikeys.GetById(record.ApiKeyId); ok {
			record.ApiKeyIdent = apikey.Ident
		}
		if system, ok := controller.Systems.GetSystemByRef(record.SystemRef); ok {
			record.SystemLabel = system.Label
		}
		loaded[record.ApiKeyId] = record
	}

	controller.sdrtrunkHealthMu.Lock()
	controller.sdrtrunkHealth = loaded
	controller.sdrtrunkHealthMu.Unlock()
}

// SdrtrunkHealthAdminHandler lists SDR Health sources (GET) and changes site
// alert settings (POST):
//
//	{"apiKeyId": n, "muteMinutes": m}   m > 0 mutes for m minutes, 0 turns alerts on, -1 turns them off
//	{"offlineRepeatMinutes": m}         repeat a site-down alert every m minutes; 0 alerts once
func (admin *Admin) SdrtrunkHealthAdminHandler(w http.ResponseWriter, r *http.Request) {
	token := admin.GetAuthorization(r)
	if !admin.ValidateToken(token) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"sources":              admin.Controller.sdrtrunkHealthAdminSources(),
			"offlineRepeatMinutes": admin.Controller.Options.SdrtrunkOfflineRepeatMinutes,
		})

	case http.MethodPost:
		var request struct {
			ApiKeyId             uint64 `json:"apiKeyId"`
			MuteMinutes          *int64 `json:"muteMinutes"`
			OfflineRepeatMinutes *int64 `json:"offlineRepeatMinutes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if request.OfflineRepeatMinutes != nil {
			minutes := *request.OfflineRepeatMinutes
			if minutes < 0 || minutes > 24*60 {
				writeAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "Repeat must be 0 to 1440 minutes"})
				return
			}
			value := uint(minutes)
			if err := admin.Controller.Options.WriteKey(admin.Controller.Database, "sdrtrunkOfflineRepeatMinutes", value, func() {
				admin.Controller.Options.SdrtrunkOfflineRepeatMinutes = value
			}); err != nil {
				writeAdminJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}

		if request.MuteMinutes != nil {
			minutes := *request.MuteMinutes
			if minutes < -1 || minutes > 7*24*60 {
				writeAdminJSON(w, http.StatusBadRequest, map[string]string{"error": "Mute must be up to 7 days"})
				return
			}
			until := int64(0)
			switch {
			case minutes == -1:
				until = sdrtrunkAlertsOff
			case minutes > 0:
				until = time.Now().Add(time.Duration(minutes) * time.Minute).UnixMilli()
			}
			if err := admin.Controller.setSdrtrunkAlertsMuted(request.ApiKeyId, until); err != nil {
				writeAdminJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
		}

		writeAdminJSON(w, http.StatusOK, map[string]any{
			"sources":              admin.Controller.sdrtrunkHealthAdminSources(),
			"offlineRepeatMinutes": admin.Controller.Options.SdrtrunkOfflineRepeatMinutes,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// setSdrtrunkAlertsMuted mutes or turns off a site's SDRTrunk alerts, closing the
// ones already open, or turns them back on (until 0).
func (controller *Controller) setSdrtrunkAlertsMuted(apiKeyId uint64, until int64) error {
	controller.sdrtrunkHealthMu.Lock()
	record, ok := controller.sdrtrunkHealth[apiKeyId]
	if ok && record != nil {
		record.AlertsMutedUntil = until
	}
	controller.sdrtrunkHealthMu.Unlock()
	if !ok || record == nil {
		return fmt.Errorf("no SDR Health source for API key %d", apiKeyId)
	}

	if controller.Database != nil && controller.Database.Sql != nil {
		if _, err := controller.Database.Sql.Exec(`UPDATE "sdrtrunkHealth" SET "alertsMutedUntil" = $1 WHERE "apikeyId" = $2`, until, apiKeyId); err != nil {
			return fmt.Errorf("failed to save alert setting: %v", err)
		}
	}

	if until != 0 {
		for _, alertType := range []string{sdrtrunkAlertControlLost, sdrtrunkAlertTunerError, sdrtrunkAlertUploaderError,
			sdrtrunkAlertOffline, sdrtrunkAlertSampleOverflow} {
			controller.dismissOpenSystemAlerts(alertType, apiKeyId)
		}
	}

	state := "on"
	switch {
	case until == sdrtrunkAlertsOff:
		state = "off"
	case until > 0:
		state = "muted until " + time.UnixMilli(until).Format(time.RFC3339)
	}
	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("sdrtrunk alerts for API key %d: %s", apiKeyId, state))
	return nil
}

func (controller *Controller) sdrtrunkAlertsMuted(apiKeyId uint64) bool {
	record, ok := controller.sdrtrunkHealthSnapshot(apiKeyId)
	return ok && record.alertsMuted(time.Now())
}

func (controller *Controller) sdrtrunkHealthAdminSources() []map[string]any {
	controller.sdrtrunkHealthMu.Lock()
	records := make([]*SdrtrunkHealthRecord, 0, len(controller.sdrtrunkHealth))
	for _, record := range controller.sdrtrunkHealth {
		if record != nil {
			copy := *record
			records = append(records, &copy)
		}
	}
	controller.sdrtrunkHealthMu.Unlock()

	sort.Slice(records, func(i, j int) bool {
		return records[i].ReceivedAt > records[j].ReceivedAt
	})

	sources := make([]map[string]any, 0, len(records))
	now := time.Now()
	for _, record := range records {
		age := now.Sub(time.UnixMilli(record.ReceivedAt))
		report := map[string]any{}
		if strings.TrimSpace(record.Report) != "" {
			_ = json.Unmarshal([]byte(record.Report), &report)
			delete(report, "key")
		}

		role, _ := report["role"].(string)
		if role == "upload" {
			apikey, ok := controller.Apikeys.GetById(record.ApiKeyId)
			if !ok || apikey.Disabled || !apikey.NoAudioAlertsEnabled {
				continue
			}
		}
		status := "healthy"
		switch {
		case role == "upload" && !record.Reporting:
			status = "not_reporting"
		case role == "upload" && age > sdrtrunkHealthStale:
			status = "not_checking_in"
		case role == "upload" && record.UploaderError:
			status = "uploader_error"
		case role == "upload":
			status = "ok"
		case !record.Reporting:
			status = "not_reporting"
		case age > sdrtrunkHealthStale:
			status = "offline"
		case record.ControlLost:
			status = "control_lost"
		case record.TunerError:
			status = "tuner_error"
		case record.UploaderError:
			status = "uploader_error"
		case sdrtrunkReportHasOverflow(report):
			status = "sample_overflow"
		}

		sources = append(sources, map[string]any{
			"apiKeyId":      record.ApiKeyId,
			"apiKeyIdent":   record.ApiKeyIdent,
			"systemRef":     record.SystemRef,
			"systemLabel":   record.SystemLabel,
			"receivedAt":    record.ReceivedAt,
			"ageSeconds":    int64(age.Seconds()),
			"stale":         age > sdrtrunkHealthStale,
			"reporting":     record.Reporting,
			"role":          role,
			"version":       record.Version,
			"uploaderState": record.UploaderState,
			"controlLost":   record.ControlLost,
			"tunerError":    record.TunerError,
			"uploaderError": record.UploaderError,
			"status":        status,
			"connected":     role != "upload" && controller.sdrtrunkHub().connected(record.ApiKeyId),
			"report":        report,

			"alertsMutedUntil": record.AlertsMutedUntil,
		})
	}

	return sources
}

func (controller *Controller) sdrtrunkHealthSnapshot(apiKeyId uint64) (*SdrtrunkHealthRecord, bool) {
	controller.sdrtrunkHealthMu.Lock()
	defer controller.sdrtrunkHealthMu.Unlock()
	record, ok := controller.sdrtrunkHealth[apiKeyId]
	if !ok || record == nil {
		return nil, false
	}
	copy := *record
	return &copy, true
}

func sdrtrunkReportHasOverflow(report map[string]any) bool {
	channels, ok := report["channels"].([]any)
	if !ok {
		return false
	}
	for _, entry := range channels {
		channel, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		overflow, _ := channel["overflow"].(bool)
		if overflow {
			return true
		}
	}
	return false
}

func sdrtrunkReportRole(raw string) string {
	var body struct {
		Role string `json:"role"`
	}
	_ = json.Unmarshal([]byte(raw), &body)
	return body.Role
}

// sdrtrunkAlertSelected is true when the report asks for this alert.
// Reports from older SDRTrunk builds omit the list and keep every alert on.
func sdrtrunkAlertSelected(raw, name string) bool {
	var body map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &body) != nil {
		return true
	}
	alerts, ok := body["alerts"]
	if !ok {
		return true
	}
	var names []string
	if json.Unmarshal(alerts, &names) != nil {
		return true
	}
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

func (controller *Controller) syncSdrtrunkAlerts(record *SdrtrunkHealthRecord, controlDetails, tunerDetails, overflowDetails []string) {
	data := &SystemAlertData{
		ApiKeyId:    record.ApiKeyId,
		ApiKeyIdent: record.ApiKeyIdent,
		SystemLabel: record.SystemLabel,
	}

	source := record.ApiKeyIdent
	if record.SystemLabel != "" {
		source = fmt.Sprintf("%s (%s)", record.ApiKeyIdent, record.SystemLabel)
	}

	role := sdrtrunkReportRole(record.Report)
	if role == "upload" {
		controller.syncUploadCheckIn(record, data, source)
		return
	}
	if role == "health" {
		controller.syncSiteHealthAlerts(record, data, source, controlDetails, tunerDetails, overflowDetails)
		return
	}

	if !record.Reporting {
		controller.dismissOpenSystemAlerts(sdrtrunkAlertControlLost, record.ApiKeyId)
		controller.dismissOpenSystemAlerts(sdrtrunkAlertTunerError, record.ApiKeyId)
		controller.dismissOpenSystemAlerts(sdrtrunkAlertUploaderError, record.ApiKeyId)
		controller.dismissOpenSystemAlerts(sdrtrunkAlertSampleOverflow, record.ApiKeyId)
		return
	}

	// Older builds sent the full site report on each upload key. A fresh report
	// from that key still means the uploader is connected.
	controller.dismissOpenSystemAlerts("api_key_no_audio", record.ApiKeyId)

	controller.syncSdrtrunkAlert(record.ControlLost, sdrtrunkAlertControlLost, "warning", "SDRTrunk control channel lost",
		fmt.Sprintf("SDRTrunk for '%s' lost a control channel: %s", source, strings.Join(controlDetails, "; ")), data)
	controller.syncSdrtrunkAlert(record.TunerError, sdrtrunkAlertTunerError, "error", "SDRTrunk tuner error",
		fmt.Sprintf("SDRTrunk for '%s' has a tuner problem: %s", source, strings.Join(tunerDetails, "; ")), data)

	uploaderMessage := fmt.Sprintf("SDRTrunk uploader for '%s' is %s", source, record.UploaderState)
	controller.syncSdrtrunkAlert(record.UploaderError, sdrtrunkAlertUploaderError, "error", "SDRTrunk uploader error", uploaderMessage, data)
	controller.syncSdrtrunkAlert(len(overflowDetails) > 0, sdrtrunkAlertSampleOverflow, "error", "SDRTrunk sample overflow",
		fmt.Sprintf("SDRTrunk for '%s' has sample overflow: %s", source, strings.Join(overflowDetails, "; ")), data)
	controller.dismissOpenSystemAlerts(sdrtrunkAlertOffline, record.ApiKeyId)
}

func (controller *Controller) syncUploadCheckIn(record *SdrtrunkHealthRecord, data *SystemAlertData, source string) {
	controller.dismissOpenSystemAlerts(sdrtrunkAlertControlLost, record.ApiKeyId)
	controller.dismissOpenSystemAlerts(sdrtrunkAlertTunerError, record.ApiKeyId)
	controller.dismissOpenSystemAlerts(sdrtrunkAlertSampleOverflow, record.ApiKeyId)
	controller.dismissOpenSystemAlerts(sdrtrunkAlertOffline, record.ApiKeyId)

	if !record.Reporting {
		controller.dismissOpenSystemAlerts(sdrtrunkAlertUploaderError, record.ApiKeyId)
		return
	}

	selected := sdrtrunkAlertSelected(record.Report, "uploader_error")
	controller.syncSdrtrunkAlert(record.UploaderError && selected, sdrtrunkAlertUploaderError, "error", "SDRTrunk uploader error",
		fmt.Sprintf("SDRTrunk uploader for '%s' is %s", source, record.UploaderState), data)
	if !record.UploaderError || selected {
		controller.dismissOpenSystemAlerts("api_key_no_audio", record.ApiKeyId)
	}
}

func (controller *Controller) syncSiteHealthAlerts(record *SdrtrunkHealthRecord, data *SystemAlertData, source string, controlDetails, tunerDetails, overflowDetails []string) {
	controller.dismissOpenSystemAlerts(sdrtrunkAlertUploaderError, record.ApiKeyId)

	if !record.Reporting {
		controller.dismissOpenSystemAlerts(sdrtrunkAlertControlLost, record.ApiKeyId)
		controller.dismissOpenSystemAlerts(sdrtrunkAlertTunerError, record.ApiKeyId)
		controller.dismissOpenSystemAlerts(sdrtrunkAlertSampleOverflow, record.ApiKeyId)
		return
	}

	controller.syncSdrtrunkAlert(record.ControlLost && sdrtrunkAlertSelected(record.Report, "control_lost"), sdrtrunkAlertControlLost, "warning", "SDRTrunk control channel lost",
		fmt.Sprintf("SDRTrunk for '%s' lost a control channel: %s", source, strings.Join(controlDetails, "; ")), data)
	controller.syncSdrtrunkAlert(record.TunerError && sdrtrunkAlertSelected(record.Report, "tuner_error"), sdrtrunkAlertTunerError, "error", "SDRTrunk tuner error",
		fmt.Sprintf("SDRTrunk for '%s' has a tuner problem: %s", source, strings.Join(tunerDetails, "; ")), data)
	controller.syncSdrtrunkAlert(len(overflowDetails) > 0 && sdrtrunkAlertSelected(record.Report, "sample_overflow"), sdrtrunkAlertSampleOverflow, "error", "SDRTrunk sample overflow",
		fmt.Sprintf("SDRTrunk for '%s' has sample overflow: %s", source, strings.Join(overflowDetails, "; ")), data)
	controller.dismissOpenSystemAlerts(sdrtrunkAlertOffline, record.ApiKeyId)
}

func (controller *Controller) syncSdrtrunkAlert(active bool, alertType, severity, title, message string, data *SystemAlertData) {
	if !controller.Options.SystemHealthAlertsEnabled {
		return
	}

	if active && controller.sdrtrunkAlertsMuted(data.ApiKeyId) {
		active = false
	}
	if active {
		if !controller.hasOpenSystemAlert(alertType, data.ApiKeyId) {
			if err := controller.CreateSystemAlert(alertType, severity, title, message, data, 0); err != nil {
				controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("failed to create %s alert for API key %d: %v", alertType, data.ApiKeyId, err))
			}
		}
		return
	}

	controller.dismissOpenSystemAlerts(alertType, data.ApiKeyId)
}

func (controller *Controller) hasOpenSystemAlert(alertType string, apiKeyId uint64) bool {
	if controller.Database == nil || controller.Database.Sql == nil {
		return false
	}

	var count int
	err := controller.Database.Sql.QueryRow(`
		SELECT COUNT(*) FROM "systemAlerts"
		WHERE "alertType" = $1 AND "dismissed" = false AND ("data" LIKE $2 OR "data" LIKE $3)
	`, alertType, fmt.Sprintf(`%%"apiKeyId":%d,%%`, apiKeyId), fmt.Sprintf(`%%"apiKeyId":%d}%%`, apiKeyId)).Scan(&count)
	return err == nil && count > 0
}

func (controller *Controller) dismissOpenSystemAlerts(alertType string, apiKeyId uint64) {
	if controller.Database == nil || controller.Database.Sql == nil {
		return
	}

	_, err := controller.Database.Sql.Exec(`
		UPDATE "systemAlerts" SET "dismissed" = true
		WHERE "alertType" = $1 AND "dismissed" = false AND ("data" LIKE $2 OR "data" LIKE $3)
	`, alertType, fmt.Sprintf(`%%"apiKeyId":%d,%%`, apiKeyId), fmt.Sprintf(`%%"apiKeyId":%d}%%`, apiKeyId))
	if err != nil {
		controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("failed to dismiss %s alerts for API key %d: %v", alertType, apiKeyId, err))
	}
}

// forgetDeletedSdrtrunkKeys removes the SDR Health record, open SDRTrunk alerts
// and connection of every API key that no longer exists. A connected SDRTrunk
// is only checked against its key when it connects, so it is disconnected too;
// otherwise its next report would bring the record back.
func (controller *Controller) forgetDeletedSdrtrunkKeys() {
	if controller.Apikeys == nil {
		return
	}
	controller.Apikeys.mutex.Lock()
	loaded := len(controller.Apikeys.List) > 0
	controller.Apikeys.mutex.Unlock()
	if !loaded {
		return
	}

	hub := controller.sdrtrunkHub()
	ids := map[uint64]bool{}
	controller.sdrtrunkHealthMu.Lock()
	for apiKeyId := range controller.sdrtrunkHealth {
		ids[apiKeyId] = true
	}
	controller.sdrtrunkHealthMu.Unlock()
	for _, apiKeyId := range hub.sessionKeys() {
		ids[apiKeyId] = true
	}

	for apiKeyId := range ids {
		if _, ok := controller.Apikeys.GetById(apiKeyId); ok {
			continue
		}
		controller.sdrtrunkHealthMu.Lock()
		delete(controller.sdrtrunkHealth, apiKeyId)
		controller.sdrtrunkHealthMu.Unlock()
		hub.disconnect(apiKeyId)

		if controller.Database != nil && controller.Database.Sql != nil {
			if _, err := controller.Database.Sql.Exec(`DELETE FROM "sdrtrunkHealth" WHERE "apikeyId" = $1`, apiKeyId); err != nil {
				controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("failed to remove sdrtrunk health for deleted API key %d: %v", apiKeyId, err))
			}
		}
		for _, alertType := range []string{sdrtrunkAlertControlLost, sdrtrunkAlertTunerError, sdrtrunkAlertUploaderError,
			sdrtrunkAlertOffline, sdrtrunkAlertSampleOverflow} {
			controller.dismissOpenSystemAlerts(alertType, apiKeyId)
		}
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("removed sdrtrunk health and alerts for deleted API key %d", apiKeyId))
	}
}

func (controller *Controller) StartSdrtrunkHealthMonitor() {
	controller.loadSdrtrunkHealth()
	controller.forgetDeletedSdrtrunkKeys()
	controller.sdrtrunkHealthStop = make(chan struct{})
	controller.sdrtrunkHealthFrom = time.Now()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		controller.checkSdrtrunkOffline()
		for {
			select {
			case <-controller.sdrtrunkHealthStop:
				return
			case <-ticker.C:
				controller.checkSdrtrunkOffline()
			}
		}
	}()
}

func (controller *Controller) checkSdrtrunkOffline() {
	controller.sdrtrunkHealthMu.Lock()
	records := make([]*SdrtrunkHealthRecord, 0, len(controller.sdrtrunkHealth))
	for _, record := range controller.sdrtrunkHealth {
		if record == nil {
			continue
		}
		copy := *record
		records = append(records, &copy)
	}
	controller.sdrtrunkHealthMu.Unlock()

	now := time.Now()
	starting := now.Sub(controller.sdrtrunkHealthFrom) < sdrtrunkOfflineStartupGrace
	for _, record := range records {
		if sdrtrunkReportRole(record.Report) == "upload" {
			continue
		}
		age := now.Sub(time.UnixMilli(record.ReceivedAt))
		// A site that closed SDRTrunk sent one last report saying so, then went quiet;
		// a crashed one just went quiet. Either way it is down once the report is stale.
		down := age > sdrtrunkHealthStale
		if !down || !sdrtrunkAlertSelected(record.Report, "offline") || record.alertsMuted(now) ||
			!controller.Options.SystemHealthAlertsEnabled {
			controller.dismissOpenSystemAlerts(sdrtrunkAlertOffline, record.ApiKeyId)
			continue
		}
		if starting {
			continue
		}

		source := record.ApiKeyIdent
		if record.SystemLabel != "" {
			source = fmt.Sprintf("%s (%s)", record.ApiKeyIdent, record.SystemLabel)
		}
		message := fmt.Sprintf("SDRTrunk for '%s' has not reported for %s", source, sdrtrunkDownFor(age))
		if !record.Reporting {
			message = fmt.Sprintf("SDRTrunk for '%s' was closed or stopped reporting %s ago", source, sdrtrunkDownFor(age))
		}
		controller.raiseSdrtrunkOffline(record.ApiKeyId, now, message, &SystemAlertData{
			ApiKeyId:         record.ApiKeyId,
			ApiKeyIdent:      record.ApiKeyIdent,
			SystemLabel:      record.SystemLabel,
			MinutesSinceLast: int(age.Minutes()),
		})
	}
}

// raiseSdrtrunkOffline opens a site-down alert, and while the site stays down
// replaces it with a fresh one every SdrtrunkOfflineRepeatMinutes so admins are
// reminded. 0 alerts once.
func (controller *Controller) raiseSdrtrunkOffline(apiKeyId uint64, now time.Time, message string, data *SystemAlertData) {
	if controller.Database == nil || controller.Database.Sql == nil {
		return
	}
	var last sql.NullInt64
	err := controller.Database.Sql.QueryRow(`
		SELECT MAX("createdAt") FROM "systemAlerts"
		WHERE "alertType" = $1 AND "dismissed" = false AND ("data" LIKE $2 OR "data" LIKE $3)
	`, sdrtrunkAlertOffline, fmt.Sprintf(`%%"apiKeyId":%d,%%`, apiKeyId), fmt.Sprintf(`%%"apiKeyId":%d}%%`, apiKeyId)).Scan(&last)
	if err != nil {
		controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("failed to read open sdrtrunk offline alert for API key %d: %v", apiKeyId, err))
		return
	}
	if last.Valid {
		repeat := time.Duration(controller.Options.SdrtrunkOfflineRepeatMinutes) * time.Minute
		if repeat == 0 || now.Sub(time.UnixMilli(last.Int64)) < repeat {
			return
		}
		controller.dismissOpenSystemAlerts(sdrtrunkAlertOffline, apiKeyId)
	}
	if err := controller.CreateSystemAlert(sdrtrunkAlertOffline, "error", "SDRTrunk offline", message, data, 0); err != nil {
		controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("failed to create %s alert for API key %d: %v", sdrtrunkAlertOffline, apiKeyId, err))
	}
}

func sdrtrunkDownFor(age time.Duration) string {
	minutes := int(age.Minutes())
	switch {
	case minutes < 2:
		return fmt.Sprintf("%d seconds", int(age.Seconds()))
	case minutes < 120:
		return fmt.Sprintf("%d minutes", minutes)
	case minutes < 48*60:
		return fmt.Sprintf("%d hours %d minutes", minutes/60, minutes%60)
	default:
		return fmt.Sprintf("%d days", minutes/(24*60))
	}
}

// sdrtrunkHealthExplainsQuietUpload reports whether a missing-upload alert should be skipped
// because SDRTrunk has already explained the source.
func (controller *Controller) sdrtrunkHealthExplainsQuietUpload(apiKeyId uint64) (bool, string) {
	record, ok := controller.sdrtrunkHealthSnapshot(apiKeyId)
	if !ok || !record.Reporting {
		return false, ""
	}

	age := time.Since(time.UnixMilli(record.ReceivedAt))
	fresh := age <= sdrtrunkHealthStale
	switch sdrtrunkReportRole(record.Report) {
	case "health":
		return false, ""
	case "upload":
		if !fresh {
			return false, ""
		}
		if !record.UploaderError {
			return true, "SDRTrunk uploader is connected"
		}
		if sdrtrunkAlertSelected(record.Report, "uploader_error") {
			return true, "SDRTrunk uploader error covers this source"
		}
		return false, ""
	}
	if fresh && !record.ControlLost && !record.TunerError && !record.UploaderError {
		return true, "SDRTrunk uploader is connected"
	}
	if record.ControlLost || record.TunerError || record.UploaderError || age > sdrtrunkHealthStale {
		return true, "SDRTrunk health alert covers this source"
	}
	return false, ""
}
