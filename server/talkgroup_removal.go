// Copyright (C) 2025 Thinline Dynamic Solutions
//
// Removing talkgroups from a system. Deleting a talkgroup row cascades to its
// calls (audio included), which on a busy channel is millions of rows in one
// transaction while the systems lock is held, stalling ingest and listeners.
// Talkgroups that have calls are therefore kept until their calls have been
// deleted in small batches, then removed.

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

const (
	talkgroupPurgeBatch = 500
	talkgroupPurgePause = 100 * time.Millisecond
)

var pendingTalkgroupRemovals = struct {
	sync.Mutex
	ids        map[uint64]bool
	finalizing map[uint64]bool
}{ids: map[uint64]bool{}, finalizing: map[uint64]bool{}}

// onDeferredTalkgroupRemovals receives talkgroups a systems write kept because
// they still had calls, after the write commits.
var onDeferredTalkgroupRemovals func(systemId uint64, ids []uint64)

func talkgroupRemovalPending(id uint64) bool {
	pendingTalkgroupRemovals.Lock()
	defer pendingTalkgroupRemovals.Unlock()
	return pendingTalkgroupRemovals.ids[id]
}

func talkgroupRemovalFinalizing(id uint64) bool {
	pendingTalkgroupRemovals.Lock()
	defer pendingTalkgroupRemovals.Unlock()
	return pendingTalkgroupRemovals.finalizing[id]
}

// adoptRemovedTalkgroupRefs gives new talkgroups the id of a removed row with
// the same talkgroupRef, so re-adding a talkgroup (for example from an import)
// updates the stored row and keeps its calls. It returns the rows still removed.
func adoptRemovedTalkgroupRefs(list []*Talkgroup, removed []uint64, refs map[uint64]uint, stored map[uint64]bool) []uint64 {
	byRef := map[uint][]uint64{}
	for _, id := range removed {
		byRef[refs[id]] = append(byRef[refs[id]], id)
	}
	adopted := map[uint64]bool{}
	for _, tg := range list {
		if tg == nil || (tg.Id > 0 && stored[tg.Id]) {
			continue
		}
		if ids := byRef[tg.TalkgroupRef]; len(ids) > 0 {
			tg.Id = ids[0]
			adopted[ids[0]] = true
			byRef[tg.TalkgroupRef] = ids[1:]
		}
	}
	out := make([]uint64, 0, len(removed))
	for _, id := range removed {
		if !adopted[id] {
			out = append(out, id)
		}
	}
	return out
}

// splitTalkgroupsWithCalls separates ids that can be deleted now from those that
// still have calls and must be purged first. Talkgroups whose purge is finishing
// are deleted even if a few calls arrived meanwhile.
func splitTalkgroupsWithCalls(tx *sql.Tx, ids []uint64) (deleteNow []uint64, deferred []uint64, err error) {
	if len(ids) == 0 {
		return ids, nil, nil
	}
	arr := make(pq.Int64Array, len(ids))
	for i, id := range ids {
		arr[i] = int64(id)
	}
	rows, err := tx.Query(`SELECT t FROM unnest($1::bigint[]) AS t WHERE EXISTS (SELECT 1 FROM "calls" WHERE "talkgroupId" = t)`, arr)
	if err != nil {
		return nil, nil, err
	}
	hasCalls := map[uint64]bool{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		hasCalls[uint64(id)] = true
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	for _, id := range ids {
		if hasCalls[id] && !talkgroupRemovalFinalizing(id) {
			deferred = append(deferred, id)
		} else {
			deleteNow = append(deleteNow, id)
		}
	}
	return deleteNow, deferred, nil
}

// removedTalkgroups lists talkgroups of existing that the incoming system no
// longer contains, matched by id and then by talkgroupRef.
func removedTalkgroups(existing *System, incoming map[string]any) []*Talkgroup {
	if existing == nil || existing.Talkgroups == nil {
		return nil
	}
	ids := map[uint64]bool{}
	refs := map[uint]bool{}
	if tgs, ok := incoming["talkgroups"].([]any); ok {
		for _, raw := range tgs {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if v, ok := m["id"].(float64); ok && v > 0 {
				ids[uint64(v)] = true
			}
			if v, ok := m["talkgroupRef"].(float64); ok {
				refs[uint(v)] = true
			}
		}
	}
	removed := []*Talkgroup{}
	for _, tg := range existing.Talkgroups.List {
		if tg == nil || tg.Id == 0 || ids[tg.Id] || refs[tg.TalkgroupRef] {
			continue
		}
		removed = append(removed, tg)
	}
	return removed
}

// talkgroupIdsWithCalls returns which of ids still have at least one call.
func (controller *Controller) talkgroupIdsWithCalls(systemId uint64, ids []uint64) (map[uint64]bool, error) {
	out := map[uint64]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	arr := make(pq.Int64Array, len(ids))
	for i, id := range ids {
		arr[i] = int64(id)
	}
	rows, err := controller.Database.Sql.Query(
		`SELECT t FROM unnest($2::bigint[]) AS t WHERE EXISTS (SELECT 1 FROM "calls" WHERE "systemId" = $1 AND "talkgroupId" = t)`,
		systemId, arr,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[uint64(id)] = true
	}
	return out, rows.Err()
}

func talkgroupRemovalSummary(removed []*Talkgroup) string {
	names := []string{}
	for i, tg := range removed {
		if i == 5 {
			names = append(names, fmt.Sprintf("and %d more", len(removed)-5))
			break
		}
		label := tg.Label
		if label == "" {
			label = fmt.Sprintf("%d", tg.TalkgroupRef)
		}
		names = append(names, label)
	}
	return strings.Join(names, ", ")
}

// keepTalkgroupsInPayload adds the stored copies of keep back into the incoming
// system so this save does not delete them.
func keepTalkgroupsInPayload(incoming map[string]any, stored map[string]any, keep map[uint64]bool) {
	if len(keep) == 0 {
		return
	}
	storedTgs, _ := stored["talkgroups"].([]any)
	tgs, _ := incoming["talkgroups"].([]any)
	for _, raw := range storedTgs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if v, ok := m["id"].(float64); ok && keep[uint64(v)] {
			tgs = append(tgs, m)
		}
	}
	incoming["talkgroups"] = tgs
}

// startTalkgroupPurge deletes the calls of ids in the background, then removes
// the talkgroups from the system. Ids already being purged are skipped.
func (controller *Controller) startTalkgroupPurge(systemId uint64, ids []uint64) {
	pendingTalkgroupRemovals.Lock()
	fresh := []uint64{}
	for _, id := range ids {
		if !pendingTalkgroupRemovals.ids[id] {
			pendingTalkgroupRemovals.ids[id] = true
			fresh = append(fresh, id)
		}
	}
	pendingTalkgroupRemovals.Unlock()
	if len(fresh) == 0 {
		return
	}
	go controller.purgeTalkgroups(systemId, fresh)
}

func (controller *Controller) purgeTalkgroups(systemId uint64, ids []uint64) {
	defer func() {
		if r := recover(); r != nil {
			controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("talkgroup removal: panic: %v", r))
		}
		pendingTalkgroupRemovals.Lock()
		for _, id := range ids {
			delete(pendingTalkgroupRemovals.ids, id)
			delete(pendingTalkgroupRemovals.finalizing, id)
		}
		pendingTalkgroupRemovals.Unlock()
	}()

	arr := make(pq.Int64Array, len(ids))
	for i, id := range ids {
		arr[i] = int64(id)
	}
	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("talkgroup removal: deleting calls for %d talkgroup(s) on system %d before removing them", len(ids), systemId))

	total := int64(0)
	lastLog := time.Now()
	for {
		res, err := controller.Database.Sql.Exec(
			fmt.Sprintf(`DELETE FROM "calls" WHERE "callId" IN (SELECT "callId" FROM "calls" WHERE "systemId" = $1 AND "talkgroupId" = ANY($2) LIMIT %d)`, talkgroupPurgeBatch),
			systemId, arr,
		)
		if err != nil {
			controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("talkgroup removal: deleting calls stopped after %d: %v. The talkgroups were kept; remove them again to retry.", total, err))
			return
		}
		n, _ := res.RowsAffected()
		total += n
		if n == 0 {
			break
		}
		if time.Since(lastLog) > time.Minute {
			controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("talkgroup removal: %d calls deleted so far on system %d", total, systemId))
			lastLog = time.Now()
		}
		time.Sleep(talkgroupPurgePause)
	}

	pendingTalkgroupRemovals.Lock()
	for _, id := range ids {
		pendingTalkgroupRemovals.finalizing[id] = true
	}
	pendingTalkgroupRemovals.Unlock()

	if err := controller.removeTalkgroupsFromSystem(systemId, ids); err != nil {
		controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("talkgroup removal: %d calls deleted but removing the talkgroups failed: %v", total, err))
		return
	}
	controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("talkgroup removal: removed %d talkgroup(s) and %d calls from system %d", len(ids), total, systemId))
	go controller.EmitConfig()
	controller.SyncConfigToFile()
}

func (controller *Controller) removeTalkgroupsFromSystem(systemId uint64, ids []uint64) error {
	drop := map[uint64]bool{}
	for _, id := range ids {
		drop[id] = true
	}

	controller.Admin.mutex.Lock()
	defer controller.Admin.mutex.Unlock()

	currentJSON, err := json.Marshal(controller.Systems.List)
	if err != nil {
		return err
	}
	var arr []any
	if err = json.Unmarshal(currentJSON, &arr); err != nil {
		return err
	}
	found := false
	for _, raw := range arr {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := m["id"].(float64); !ok || uint64(id) != systemId {
			continue
		}
		found = true
		tgs, _ := m["talkgroups"].([]any)
		kept := make([]any, 0, len(tgs))
		for _, t := range tgs {
			if tm, ok := t.(map[string]any); ok {
				if id, ok := tm["id"].(float64); ok && drop[uint64(id)] {
					continue
				}
			}
			kept = append(kept, t)
		}
		m["talkgroups"] = kept
	}
	if !found {
		return nil
	}

	controller.Systems.FromMap(arr)
	if err = controller.Systems.Write(controller.Database); err != nil {
		if readErr := controller.Systems.Read(controller.Database); readErr != nil {
			controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("talkgroup removal: reload after failed write: %v", readErr))
		}
		return err
	}
	if err = controller.Systems.Read(controller.Database); err != nil {
		return err
	}
	if e := controller.IdLookupsCache.Read(controller.Database); e != nil {
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("failed to reload ID lookups cache: %v", e))
	}
	return nil
}
