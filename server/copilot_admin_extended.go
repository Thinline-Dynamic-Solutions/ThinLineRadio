// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (admin *Admin) copilotGetTranscriptionFailures() (map[string]any, error) {
	twentyFourHoursAgo := time.Now().Add(-24 * time.Hour).UnixMilli()
	query := fmt.Sprintf(`SELECT c."callId", c."systemId", c."talkgroupId", c."timestamp", c."transcriptionFailureReason", s."label", t."label", t."name" FROM "calls" c LEFT JOIN "systems" s ON s."systemId" = c."systemId" LEFT JOIN "talkgroups" t ON t."talkgroupId" = c."talkgroupId" WHERE c."transcriptionStatus" = 'failed' AND c."timestamp" >= %d ORDER BY c."timestamp" DESC LIMIT 100`, twentyFourHoursAgo)
	rows, err := admin.Controller.Database.Sql.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	calls := []map[string]any{}
	for rows.Next() {
		var callId, systemId, talkgroupId uint64
		var timestamp int64
		var failureReason, systemLabel, talkgroupLabel, talkgroupName *string
		if err := rows.Scan(&callId, &systemId, &talkgroupId, &timestamp, &failureReason, &systemLabel, &talkgroupLabel, &talkgroupName); err != nil {
			continue
		}
		row := map[string]any{"callId": callId, "systemId": systemId, "talkgroupId": talkgroupId, "timestamp": timestamp}
		if failureReason != nil {
			row["failureReason"] = *failureReason
		}
		if systemLabel != nil {
			row["systemLabel"] = *systemLabel
		}
		if talkgroupLabel != nil {
			row["talkgroupLabel"] = *talkgroupLabel
		}
		if talkgroupName != nil {
			row["talkgroupName"] = *talkgroupName
		}
		calls = append(calls, row)
	}
	return map[string]any{"count": len(calls), "calls": calls}, nil
}

func (admin *Admin) copilotResetTranscriptionFailures(payloadJSON []byte) (map[string]any, error) {
	var req struct {
		CallIDs []uint64 `json:"callIds"`
	}
	_ = json.Unmarshal(payloadJSON, &req)
	if len(req.CallIDs) == 0 {
		return nil, fmt.Errorf("callIds array is required")
	}
	placeholders := make([]string, len(req.CallIDs))
	args := make([]any, len(req.CallIDs))
	for i, id := range req.CallIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	query := fmt.Sprintf(`UPDATE "calls" SET "transcriptionStatus" = 'pending', "transcriptionFailureReason" = '' WHERE "callId" IN (%s)`, strings.Join(placeholders, ","))
	if _, err := admin.Controller.Database.Sql.Exec(query, args...); err != nil {
		return nil, err
	}
	return map[string]any{"reset": len(req.CallIDs)}, nil
}

func (admin *Admin) copilotPatchTranscriptParser(payloadJSON []byte) error {
	var cfg TranscriptConfig
	if err := json.Unmarshal(payloadJSON, &cfg); err != nil {
		var wrap struct {
			Config TranscriptConfig `json:"config"`
		}
		if err2 := json.Unmarshal(payloadJSON, &wrap); err2 != nil {
			return err
		}
		cfg = wrap.Config
	}
	admin.Controller.Options.TranscriptParserConfig = cfg
	if err := admin.Controller.Options.Write(admin.Controller.Database); err != nil {
		return err
	}
	admin.Controller.rebuildTranscriptParser()
	admin.copilotFinishWrite("patch_transcript_parser")
	return nil
}

func (admin *Admin) copilotGetRelayStatus() map[string]any {
	snap := admin.Controller.getRelaySuspensionSnapshot()
	admin.Controller.Options.mutex.Lock()
	ownerUnlocked := admin.Controller.Options.RelayOwnerUnlockedPublicClient
	admin.Controller.Options.mutex.Unlock()
	return map[string]any{
		"fully_suspended":             snap.suspended,
		"suspend_message":             snap.message,
		"relay_owner_unlocked_public": ownerUnlocked,
		"public_listener_blocked":     admin.Controller.IsPublicWebListenerBlocked(),
		"push_notifications_blocked":  admin.Controller.RelayPushSuspended(),
	}
}

func (admin *Admin) copilotGetHallucinations() (map[string]any, error) {
	suggestions, err := admin.Controller.HallucinationDetector.GetPendingSuggestions()
	if err != nil {
		return nil, err
	}
	return map[string]any{"suggestions": suggestions}, nil
}

func (admin *Admin) copilotApproveHallucination(payloadJSON []byte) error {
	var req struct {
		ID uint64 `json:"id"`
	}
	if err := json.Unmarshal(payloadJSON, &req); err != nil {
		return err
	}
	if err := admin.Controller.HallucinationDetector.ApproveHallucination(req.ID); err != nil {
		return err
	}
	admin.Controller.SyncConfigToFile()
	return nil
}

func (admin *Admin) copilotRejectHallucination(payloadJSON []byte) error {
	var req struct {
		ID uint64 `json:"id"`
	}
	if err := json.Unmarshal(payloadJSON, &req); err != nil {
		return err
	}
	return admin.Controller.HallucinationDetector.RejectHallucination(req.ID)
}

func (admin *Admin) copilotInviteUser(payloadJSON []byte) (map[string]any, error) {
	var req struct {
		Email   string `json:"email"`
		GroupID uint64 `json:"groupId"`
	}
	if err := json.Unmarshal(payloadJSON, &req); err != nil {
		return nil, err
	}
	req.Email = NormalizeEmail(strings.TrimSpace(req.Email))
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		return nil, fmt.Errorf("valid email is required")
	}
	group := admin.Controller.UserGroups.Get(req.GroupID)
	if group == nil {
		return nil, fmt.Errorf("group not found")
	}
	if admin.Controller.Users.GetUserByEmail(req.Email) != nil {
		return nil, fmt.Errorf("user already exists")
	}
	code, err := generateInvitationCode()
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(7 * 24 * time.Hour).Unix()
	invitedAt := time.Now().Unix()
	var invitationId int64
	err = admin.Controller.Database.Sql.QueryRow(
		`INSERT INTO "userInvitations" ("email", "code", "userGroupId", "invitedBy", "invitedAt", "expiresAt", "status") VALUES ($1, $2, $3, NULL, $4, $5, $6) RETURNING "userInvitationId"`,
		req.Email, code, req.GroupID, invitedAt, expiresAt, "pending",
	).Scan(&invitationId)
	if err != nil {
		return nil, err
	}
	if admin.Controller.Options.EmailServiceEnabled {
		baseUrl := admin.Controller.Options.BaseUrl
		if baseUrl == "" {
			baseUrl = "https://localhost:8080"
		} else if strings.HasPrefix(baseUrl, "http://") {
			baseUrl = strings.Replace(baseUrl, "http://", "https://", 1)
		} else if !strings.HasPrefix(baseUrl, "https://") {
			baseUrl = "https://" + baseUrl
		}
		branding := admin.Controller.Options.Branding
		if branding == "" {
			branding = "ThinLine Radio"
		}
		_ = admin.Controller.EmailService.SendInvitationEmail(req.Email, code, baseUrl+"/?invite="+code, group.Name, branding)
	}
	return map[string]any{"invitationId": invitationId, "email": req.Email, "code": code}, nil
}

func (admin *Admin) copilotTransferUser(payloadJSON []byte) (map[string]any, error) {
	var req struct {
		UserID        uint64 `json:"userId"`
		TargetGroupID uint64 `json:"targetGroupId"`
	}
	if err := json.Unmarshal(payloadJSON, &req); err != nil {
		return nil, err
	}
	user := admin.Controller.Users.GetUserById(req.UserID)
	if user == nil {
		return nil, fmt.Errorf("user not found")
	}
	if req.TargetGroupID > 0 && admin.Controller.UserGroups.Get(req.TargetGroupID) == nil {
		return nil, fmt.Errorf("target group not found")
	}
	user.UserGroupId = req.TargetGroupID
	user.IsGroupAdmin = false
	if err := admin.Controller.Users.Update(user); err != nil {
		return nil, err
	}
	if err := admin.Controller.Users.Write(admin.Controller.Database); err != nil {
		return nil, err
	}
	admin.Controller.SyncConfigToFile()
	return map[string]any{"userId": user.Id, "userGroupId": user.UserGroupId}, nil
}

func (admin *Admin) copilotSaveBillingGroup(payloadJSON []byte) (map[string]any, error) {
	var req map[string]any
	if err := json.Unmarshal(payloadJSON, &req); err != nil {
		return nil, err
	}
	name, _ := req["name"].(string)
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("name is required")
	}
	group := &UserGroup{
		Name:        name,
		Description: stringFromAny(req["description"]),
		SystemAccess: func() string {
			if v, ok := req["systemAccess"].(string); ok {
				return v
			}
			return "*"
		}(),
		CreatedAt: time.Now().Unix(),
	}
	if v, ok := req["billingEnabled"].(bool); ok {
		group.BillingEnabled = v
	}
	if err := admin.Controller.UserGroups.Add(group, admin.Controller.Database); err != nil {
		return nil, err
	}
	return map[string]any{"id": group.Id, "name": group.Name}, nil
}

func (admin *Admin) copilotUpdateBillingGroup(payloadJSON []byte) (map[string]any, error) {
	var req map[string]any
	if err := json.Unmarshal(payloadJSON, &req); err != nil {
		return nil, err
	}
	var id uint64
	if v, ok := req["id"].(float64); ok {
		id = uint64(v)
	}
	if id == 0 {
		return nil, fmt.Errorf("id is required")
	}
	group := admin.Controller.UserGroups.Get(id)
	if group == nil {
		return nil, fmt.Errorf("group not found")
	}
	if v, ok := req["name"].(string); ok && v != "" {
		group.Name = v
	}
	if v, ok := req["description"].(string); ok {
		group.Description = v
	}
	if v, ok := req["systemAccess"].(string); ok {
		group.SystemAccess = v
	}
	if v, ok := req["billingEnabled"].(bool); ok {
		group.BillingEnabled = v
	}
	if err := admin.Controller.UserGroups.Update(group, admin.Controller.Database); err != nil {
		return nil, err
	}
	return map[string]any{"id": group.Id, "name": group.Name}, nil
}

func (admin *Admin) copilotDeleteBillingGroup(payloadJSON []byte) (map[string]any, error) {
	var req struct {
		GroupID uint64 `json:"groupId"`
	}
	if err := json.Unmarshal(payloadJSON, &req); err != nil {
		return nil, err
	}
	if err := admin.Controller.UserGroups.Delete(req.GroupID, admin.Controller.Database); err != nil {
		return nil, err
	}
	return map[string]any{"deletedGroupId": req.GroupID}, nil
}

func (admin *Admin) copilotRadioReferenceBrowse(payload map[string]any) (map[string]any, error) {
	if !admin.Controller.Options.RadioReferenceEnabled {
		return nil, fmt.Errorf("radio reference is not enabled")
	}
	rr := NewRadioReferenceService(
		admin.Controller.Options.RadioReferenceUsername,
		admin.Controller.Options.RadioReferencePassword,
		admin.Controller.Options.RadioReferenceAPIKey,
	)
	step := strings.ToLower(stringFromAny(payload["step"]))
	switch step {
	case "countries":
		items, err := rr.GetCountries()
		return map[string]any{"items": items}, err
	case "states":
		items, err := rr.GetStates(int(floatFromAny(payload["countryId"])))
		return map[string]any{"items": items}, err
	case "counties":
		items, err := rr.GetCounties(int(floatFromAny(payload["stateId"])))
		return map[string]any{"items": items}, err
	case "systems":
		items, err := rr.GetSystemsByCounty(int(floatFromAny(payload["countyId"])))
		return map[string]any{"items": items}, err
	case "talkgroups":
		items, err := rr.GetTalkgroups(int(floatFromAny(payload["systemId"])))
		return map[string]any{"items": items}, err
	case "talkgroup_categories":
		items, err := rr.GetTalkgroupCategories(int(floatFromAny(payload["systemId"])))
		return map[string]any{"items": items}, err
	case "talkgroups_by_category":
		items, err := rr.GetTalkgroupsByCategory(int(floatFromAny(payload["systemId"])), int(floatFromAny(payload["categoryId"])), stringFromAny(payload["categoryName"]))
		return map[string]any{"items": items}, err
	case "sites":
		items, err := rr.GetSites(int(floatFromAny(payload["systemId"])))
		return map[string]any{"items": items}, err
	default:
		return nil, fmt.Errorf("step must be countries, states, counties, systems, talkgroups, talkgroup_categories, talkgroups_by_category, or sites")
	}
}

func (admin *Admin) copilotConfigReload() error {
	if err := admin.Controller.Options.Read(admin.Controller.Database); err != nil {
		return err
	}
	admin.copilotFinishWrite("config_reload")
	return nil
}

func (admin *Admin) copilotRelayUnlockPublicClient() error {
	if !admin.Controller.RelayPushSuspended() {
		return fmt.Errorf("server is not suspended by the relay")
	}
	admin.Controller.Options.mutex.Lock()
	admin.Controller.Options.RelayOwnerUnlockedPublicClient = true
	admin.Controller.Options.mutex.Unlock()
	if err := admin.Controller.Options.Write(admin.Controller.Database); err != nil {
		return err
	}
	admin.Controller.Logs.LogEvent(LogLevelInfo, "copilot: operator unlocked public web listener")
	return nil
}

func (admin *Admin) copilotTestCentralConnection() (map[string]any, error) {
	if !admin.Controller.Options.CentralManagementEnabled {
		return nil, fmt.Errorf("central management is not enabled")
	}
	return map[string]any{
		"centralManagementEnabled": true,
		"message":                "use Config → Options → User Registration for CM status; connection test runs from that UI",
	}, nil
}

func (admin *Admin) copilotCheckServerUpdate() (map[string]any, error) {
	if admin.Controller.Updater == nil {
		return nil, fmt.Errorf("updater not initialised")
	}
	info, err := admin.Controller.Updater.CheckForUpdate()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"current_version":  info.CurrentVersion,
		"latest_version":   info.LatestVersion,
		"update_available": info.UpdateAvailable,
		"platform":         info.Platform,
	}, nil
}

func (admin *Admin) copilotRadioReferenceImportToSystem(payloadJSON []byte) (map[string]any, error) {
	created, updated, err := admin.radioReferenceImportToSystemFromJSON(payloadJSON)
	if err != nil {
		return nil, err
	}
	return map[string]any{"created": created, "updated": updated}, nil
}

func (admin *Admin) copilotUpdateTalkgroup(payloadJSON []byte) (map[string]any, error) {
	var req struct {
		SystemID     uint64         `json:"systemId"`
		TalkgroupID  uint64         `json:"talkgroupId"`
		TalkgroupRef uint           `json:"talkgroupRef"`
		Patch        map[string]any `json:"patch"`
	}
	if err := json.Unmarshal(payloadJSON, &req); err != nil {
		return nil, err
	}
	if req.SystemID == 0 || len(req.Patch) == 0 {
		return nil, fmt.Errorf("systemId and patch are required")
	}
	system, tg, err := admin.copilotFindTalkgroup(req.SystemID, req.TalkgroupID, req.TalkgroupRef)
	if err != nil {
		return nil, err
	}
	tgJSON, _ := json.Marshal(tg)
	var tgMap map[string]any
	_ = json.Unmarshal(tgJSON, &tgMap)
	for k, v := range req.Patch {
		tgMap[k] = v
	}
	patchedJSON, _ := json.Marshal(tgMap)
	_ = json.Unmarshal(patchedJSON, tg)

	systemJSON, err := json.Marshal(system)
	if err != nil {
		return nil, err
	}
	if err = admin.copilotSaveSystem(systemJSON); err != nil {
		return nil, err
	}
	return map[string]any{
		"systemId":    req.SystemID,
		"talkgroupId": tg.Id,
		"talkgroup":   tg,
	}, nil
}

func stringFromAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func floatFromAny(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}
