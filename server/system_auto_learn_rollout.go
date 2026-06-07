// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"fmt"
	"time"
)

// applyAutoLearnToneSetsRollout enables auto-learn tone sets on talkgroups matching selected tags.
func (system *System) applyAutoLearnToneSetsRollout() {
	if system == nil || system.Talkgroups == nil {
		return
	}

	tagSet := bulkToneTagSet(system.AutoLearnToneSetsTagIds)
	if len(tagSet) == 0 {
		return
	}

	now := time.Now().UnixMilli()
	if !system.AutoLearnToneSets {
		system.AutoLearnToneSetsExpiresAt = 0
		return
	}

	if system.AutoLearnToneSetsAutoOffDays > 0 && system.AutoLearnToneSetsExpiresAt == 0 {
		system.AutoLearnToneSetsExpiresAt = now + int64(system.AutoLearnToneSetsAutoOffDays)*24*60*60*1000
	}
	for _, tg := range system.Talkgroups.List {
		if tagSet[tg.TagId] {
			tg.AutoLearnToneSets = true
		}
	}
}

// applyAutoLearnUnitAliasesRollout enables unit alias auto-learn on talkgroups matching selected tags.
func (system *System) applyAutoLearnUnitAliasesRollout() {
	if system == nil {
		return
	}

	if system.Talkgroups == nil {
		return
	}

	tagSet := bulkToneTagSet(system.AutoLearnUnitAliasesTagIds)
	if len(tagSet) == 0 {
		return
	}

	if !system.AutoLearnUnitAliases {
		system.AutoLearnUnitAliasesExpiresAt = 0
		return
	}

	now := time.Now().UnixMilli()
	if system.AutoLearnUnitAliasesAutoOffDays > 0 && system.AutoLearnUnitAliasesExpiresAt == 0 {
		system.AutoLearnUnitAliasesExpiresAt = now + int64(system.AutoLearnUnitAliasesAutoOffDays)*24*60*60*1000
	}
	for _, tg := range system.Talkgroups.List {
		if tagSet[tg.TagId] {
			tg.AutoLearnUnitAliases = true
		}
	}
}

func (controller *Controller) expireAutoLearnToneSets() {
	if controller == nil {
		return
	}

	now := time.Now().UnixMilli()
	controller.Systems.mutex.RLock()
	systems := make([]*System, len(controller.Systems.List))
	copy(systems, controller.Systems.List)
	controller.Systems.mutex.RUnlock()

	for _, system := range systems {
		if system == nil || !system.AutoLearnToneSets {
			continue
		}
		if system.AutoLearnToneSetsExpiresAt == 0 || now < system.AutoLearnToneSetsExpiresAt {
			continue
		}
		controller.finalizeAutoLearnToneSetsExpiry(system)
	}
}

func (controller *Controller) finalizeAutoLearnToneSetsExpiry(system *System) {
	tagSet := bulkToneTagSet(system.AutoLearnToneSetsTagIds)
	if len(tagSet) == 0 {
		return
	}

	disabled := 0
	for tagId := range tagSet {
		q := `UPDATE "talkgroups" SET "autoLearnToneSets" = false WHERE "systemId" = ? AND "tagId" = ?`
		if controller.Database.Config.DbType == DbTypePostgresql {
			q = `UPDATE "talkgroups" SET "autoLearnToneSets" = false WHERE "systemId" = $1 AND "tagId" = $2`
		}
		res, err := controller.Database.Sql.Exec(q, system.Id, tagId)
		if err != nil {
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("tone auto-learn expiry: disable tag %d on system %d failed: %v", tagId, system.Id, err))
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			disabled += int(n)
		}
	}

	sysQuery := `UPDATE "systems" SET "autoLearnToneSets" = false, "autoLearnToneSetsExpiresAt" = 0 WHERE "systemId" = ?`
	if controller.Database.Config.DbType == DbTypePostgresql {
		sysQuery = `UPDATE "systems" SET "autoLearnToneSets" = false, "autoLearnToneSetsExpiresAt" = 0 WHERE "systemId" = $1`
	}
	if _, err := controller.Database.Sql.Exec(sysQuery, system.Id); err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("tone auto-learn expiry: clear system %d failed: %v", system.Id, err))
	}

	controller.Systems.mutex.Lock()
	for _, sys := range controller.Systems.List {
		if sys.Id != system.Id {
			continue
		}
		sys.AutoLearnToneSets = false
		sys.AutoLearnToneSetsExpiresAt = 0
		if sys.Talkgroups != nil {
			for _, tg := range sys.Talkgroups.List {
				if tagSet[tg.TagId] {
					tg.AutoLearnToneSets = false
				}
			}
		}
		break
	}
	controller.Systems.mutex.Unlock()

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
		"tone auto-learn expired for system %s (%d): disabled on %d talkgroup(s)",
		system.Label, system.SystemRef, disabled,
	))
}
