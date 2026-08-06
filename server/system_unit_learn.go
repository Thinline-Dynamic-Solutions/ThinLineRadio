// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"fmt"
	"time"
)

// expireAutoLearnUnitAliases disables unit alias learning when the timer elapses.
func (controller *Controller) expireAutoLearnUnitAliases() {
	if controller == nil {
		return
	}

	now := time.Now().UnixMilli()
	controller.Systems.mutex.RLock()
	systems := make([]*System, len(controller.Systems.List))
	copy(systems, controller.Systems.List)
	controller.Systems.mutex.RUnlock()

	for _, system := range systems {
		if system == nil || !system.AutoLearnUnitAliases {
			continue
		}
		if system.AutoLearnUnitAliasesExpiresAt == 0 || now < system.AutoLearnUnitAliasesExpiresAt {
			continue
		}
		controller.finalizeAutoLearnUnitAliasesExpiry(system)
	}
}

func (controller *Controller) finalizeAutoLearnUnitAliasesExpiry(system *System) {
	tagSet := bulkToneTagSet(system.AutoLearnUnitAliasesTagIds)
	disabled := 0

	if len(tagSet) == 0 {
		q := `UPDATE "talkgroups" SET "autoLearnUnitAliases" = false WHERE "systemId" = ?`
		if controller.Database.Config.DbType == DbTypePostgresql {
			q = `UPDATE "talkgroups" SET "autoLearnUnitAliases" = false WHERE "systemId" = $1`
		}
		res, err := controller.Database.Sql.Exec(q, system.Id)
		if err != nil {
			controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("unit auto-learn expiry: disable all talkgroups on system %d failed: %v", system.Id, err))
		} else if n, _ := res.RowsAffected(); n > 0 {
			disabled = int(n)
		}
	} else {
		for tagId := range tagSet {
			q := `UPDATE "talkgroups" SET "autoLearnUnitAliases" = false WHERE "systemId" = ? AND "tagId" = ?`
			if controller.Database.Config.DbType == DbTypePostgresql {
				q = `UPDATE "talkgroups" SET "autoLearnUnitAliases" = false WHERE "systemId" = $1 AND "tagId" = $2`
			}
			res, err := controller.Database.Sql.Exec(q, system.Id, tagId)
			if err != nil {
				controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("unit auto-learn expiry: disable tag %d on system %d failed: %v", tagId, system.Id, err))
				continue
			}
			if n, _ := res.RowsAffected(); n > 0 {
				disabled += int(n)
			}
		}
	}

	sysQuery := `UPDATE "systems" SET "autoLearnUnitAliases" = false, "autoLearnUnitAliasesExpiresAt" = 0 WHERE "systemId" = ?`
	if controller.Database.Config.DbType == DbTypePostgresql {
		sysQuery = `UPDATE "systems" SET "autoLearnUnitAliases" = false, "autoLearnUnitAliasesExpiresAt" = 0 WHERE "systemId" = $1`
	}
	if _, err := controller.Database.Sql.Exec(sysQuery, system.Id); err != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("unit auto-learn expiry: clear system %d failed: %v", system.Id, err))
	}

	controller.Systems.mutex.Lock()
	for _, sys := range controller.Systems.List {
		if sys.Id != system.Id {
			continue
		}
		sys.AutoLearnUnitAliases = false
		sys.AutoLearnUnitAliasesExpiresAt = 0
		if sys.Talkgroups != nil {
			for _, tg := range sys.Talkgroups.List {
				if len(tagSet) == 0 || tagSet[tg.TagId] {
					tg.AutoLearnUnitAliases = false
				}
			}
		}
		break
	}
	controller.Systems.mutex.Unlock()

	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
		"unit alias auto-learn expired for system %s (%d): disabled on %d talkgroup(s)",
		system.Label, system.SystemRef, disabled,
	))
}
