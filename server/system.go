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
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type System struct {
	Id           uint64
	AutoPopulate bool
	Blacklists   Blacklists
	Delay        uint
	Kind         string
	Label        string
	Order        uint
	Sites        *Sites
	SystemRef    uint
	Talkgroups   *Talkgroups
	Units        *Units
}

func NewSystem() *System {
	return &System{
		Sites:      NewSites(),
		Talkgroups: NewTalkgroups(),
		Units:      NewUnits(),
	}
}

func (system *System) FromMap(m map[string]any) *System {
	// Handle both "id" and "_id" fields for backward compatibility
	if v, ok := m["id"].(float64); ok {
		system.Id = uint64(v)
	} else if v, ok := m["_id"].(float64); ok {
		system.Id = uint64(v)
	}

	switch v := m["autoPopulate"].(type) {
	case bool:
		system.AutoPopulate = v
	}

	switch v := m["blacklists"].(type) {
	case string:
		system.Blacklists = Blacklists(v)
	}

	switch v := m["delay"].(type) {
	case float64:
		system.Delay = uint(v)
	}

	switch v := m["type"].(type) {
	case string:
		system.Kind = v
	}

	switch v := m["label"].(type) {
	case string:
		system.Label = v
	}

	switch v := m["order"].(type) {
	case float64:
		system.Order = uint(v)
	}

	switch v := m["sites"].(type) {
	case []any:
		system.Sites.FromMap(v)
	}

	switch v := m["systemRef"].(type) {
	case float64:
		system.SystemRef = uint(v)
	}

	switch v := m["talkgroups"].(type) {
	case []any:
		system.Talkgroups.FromMap(v)
	}

	switch v := m["units"].(type) {
	case []any:
		system.Units.FromMap(v)
	}

	return system
}

func (system *System) MarshalJSON() ([]byte, error) {
	m := map[string]any{
		"id":           system.Id,
		"autoPopulate": system.AutoPopulate,
		"label":        system.Label,
		"sites":        system.Sites.List,
		"systemRef":    system.SystemRef,
		"talkgroups":   system.Talkgroups.List,
		"units":        system.Units.List,
	}

	if len(system.Blacklists) > 0 {
		m["blacklists"] = system.Blacklists
	}

	if system.Delay > 0 {
		m["delay"] = system.Delay
	}

	if len(system.Kind) > 0 {
		m["type"] = system.Kind
	}

	if system.Order > 0 {
		m["order"] = system.Order
	}

	return json.Marshal(m)
}

type SystemMap map[string]any

type Systems struct {
	List  []*System
	mutex sync.RWMutex
}

func NewSystems() *Systems {
	return &Systems{
		List:  []*System{},
		mutex: sync.RWMutex{},
	}
}

func (systems *Systems) FromMap(f []any) *Systems {
	systems.mutex.Lock()
	defer systems.mutex.Unlock()

	systems.List = []*System{}

	for _, r := range f {
		switch m := r.(type) {
		case map[string]any:
			system := NewSystem()
			system.FromMap(m)
			systems.List = append(systems.List, system)
		}
	}

	return systems
}

func (systems *Systems) GetNewSystemRef() uint {
	systems.mutex.Lock()
	defer systems.mutex.Unlock()

NextRef:
	for i := uint(1); i < 2e16; i++ {
		for _, s := range systems.List {
			if s.SystemRef == i {
				continue NextRef
			}
		}
		return i
	}
	return 0
}

func (systems *Systems) GetSystemById(id uint64) (system *System, ok bool) {
	systems.mutex.RLock()
	defer systems.mutex.RUnlock()

	for _, system := range systems.List {
		if system.Id == id {
			return system, true
		}
	}

	return nil, false
}

// getSystemByIdInternal is an internal helper that doesn't use mutex (caller must hold lock)
func (systems *Systems) getSystemByIdInternal(id uint64) (system *System, ok bool) {
	for _, system := range systems.List {
		if system.Id == id {
			return system, true
		}
	}

	return nil, false
}

func (systems *Systems) GetSystemByLabel(label string) (system *System, ok bool) {
	systems.mutex.RLock()
	defer systems.mutex.RUnlock()

	for _, system := range systems.List {
		if system.Label == label {
			return system, true
		}
	}

	return nil, false
}

func (systems *Systems) GetSystemByRef(ref uint) (system *System, ok bool) {
	systems.mutex.RLock()
	defer systems.mutex.RUnlock()

	for _, system := range systems.List {
		if system.SystemRef == ref {
			return system, true
		}
	}

	return nil, false
}

func (systems *Systems) GetScopedSystems(client *Client, groups *Groups, tags *Tags, sortTalkgroups bool) SystemsMap {
	var (
		rawSystems = []System{}
		systemsMap = SystemsMap{}
	)

	user := client.User

	// Get user's group if they belong to one
	var userGroup *UserGroup
	if user != nil && user.UserGroupId > 0 && client.Controller != nil {
		userGroup = client.Controller.UserGroups.Get(user.UserGroupId)
	}

	// Helper function to check if a system is allowed
	isSystemAllowed := func(systemRef uint) bool {
		// If user belongs to a group, check group access first
		if userGroup != nil {
			return userGroup.HasSystemAccess(uint64(systemRef))
		}
		// No group restrictions
		return true
	}

	// Helper function to filter talkgroups based on group restrictions
	filterTalkgroupsByGroup := func(system *System) *System {
		// If no group restrictions, return system as-is
		if userGroup == nil {
			return system
		}

		// Filter talkgroups based on group access
		filteredSystem := *system
		filteredSystem.Talkgroups = NewTalkgroups()
		
		for _, tg := range system.Talkgroups.List {
			if userGroup.HasTalkgroupAccess(uint64(system.SystemRef), tg.TalkgroupRef) {
				filteredSystem.Talkgroups.List = append(filteredSystem.Talkgroups.List, tg)
			}
		}
		
		return &filteredSystem
	}

	if user == nil || user.systemsData == nil {
		// No user-level restrictions, but still need to check group restrictions
		for _, system := range systems.List {
			if isSystemAllowed(system.SystemRef) {
				filteredSystem := filterTalkgroupsByGroup(system)
				rawSystems = append(rawSystems, *filteredSystem)
			}
		}

	} else {
		switch v := user.systemsData.(type) {
		case nil:
			// No user-level restrictions, but still need to check group restrictions
			for _, system := range systems.List {
				if isSystemAllowed(system.SystemRef) {
					filteredSystem := filterTalkgroupsByGroup(system)
					rawSystems = append(rawSystems, *filteredSystem)
				}
			}

		case string:
			if strings.TrimSpace(v) == "" || v == "*" {
				// User allows all systems, but still need to check group restrictions
				for _, system := range systems.List {
					if isSystemAllowed(system.SystemRef) {
						filteredSystem := filterTalkgroupsByGroup(system)
						rawSystems = append(rawSystems, *filteredSystem)
					}
				}
			}

		case []any:
			for _, fSystem := range v {
				switch v := fSystem.(type) {
				case map[string]any:
					var (
						mSystemId   = v["id"]
						mTalkgroups = v["talkgroups"]
						systemId    uint
					)

					switch v := mSystemId.(type) {
					case float64:
						systemId = uint(v)
					default:
						continue
					}

					system, ok := systems.GetSystemByRef(systemId)
					if !ok {
						continue
					}

					// Check group access first - if group doesn't allow this system, skip it
					if !isSystemAllowed(system.SystemRef) {
						continue
					}

					switch v := mTalkgroups.(type) {
					case string:
						if mTalkgroups == "*" {
							// User allows all talkgroups, but filter by group restrictions
							filteredSystem := filterTalkgroupsByGroup(system)
							rawSystems = append(rawSystems, *filteredSystem)
							continue
						}

					case []any:
						rawSystem := *system
						rawSystem.Talkgroups = NewTalkgroups()
						for _, fTalkgroupId := range v {
							switch v := fTalkgroupId.(type) {
							case float64:
								rawTalkgroup, ok := system.Talkgroups.GetTalkgroupByRef(uint(v))
								if !ok {
									continue
								}
								// Also check group access for this talkgroup
								if userGroup != nil && !userGroup.HasTalkgroupAccess(uint64(system.SystemRef), rawTalkgroup.TalkgroupRef) {
									continue
								}
								rawSystem.Talkgroups.List = append(rawSystem.Talkgroups.List, rawTalkgroup)
							default:
								continue
							}
						}
						rawSystems = append(rawSystems, rawSystem)
					}
				}
			}
		}
	}

	for _, rawSystem := range rawSystems {
		talkgroupsMap := TalkgroupsMap{}

		for _, rawTalkgroup := range rawSystem.Talkgroups.List {
			var (
				groupLabel  string
				groupLabels = []string{}
			)

			for _, id := range rawTalkgroup.GroupIds {
				if group, ok := groups.GetGroupById(id); ok {
					groupLabels = append(groupLabels, group.Label)
				}
			}

			if len(groupLabels) > 0 {
				groupLabel = groupLabels[0]
			}

			tag, ok := tags.GetTagById(rawTalkgroup.TagId)
			if !ok {
				continue
			}

	talkgroupMap := TalkgroupMap{
		"id":                   rawTalkgroup.TalkgroupRef,
		"talkgroupId":          rawTalkgroup.Id,            // Database ID for admin/backend use
		"talkgroupRef":         rawTalkgroup.TalkgroupRef,  // Radio reference ID
		"frequency":            rawTalkgroup.Frequency,
		"group":                groupLabel,
		"groups":               groupLabels,
		"label":                rawTalkgroup.Label,
		"name":                 rawTalkgroup.Name,
		"order":                rawTalkgroup.Order,
		"tag":                  tag.Label,
		"type":                 rawTalkgroup.Kind,
		"toneDetectionEnabled": rawTalkgroup.ToneDetectionEnabled,
	}

			if len(rawTalkgroup.ToneSets) > 0 {
				if toneSetsJson, err := SerializeToneSets(rawTalkgroup.ToneSets); err == nil {
					var toneSets []map[string]any
					if err := json.Unmarshal([]byte(toneSetsJson), &toneSets); err == nil {
						talkgroupMap["toneSets"] = toneSets
					}
				}
			}

		talkgroupsMap = append(talkgroupsMap, talkgroupMap)
	}

	// Sort talkgroups: either by custom order (from database) or alphabetically by label
	if sortTalkgroups {
		// Sort alphabetically by label
		sort.Slice(talkgroupsMap, func(i int, j int) bool {
			labelA := fmt.Sprintf("%v", talkgroupsMap[i]["label"])
			labelB := fmt.Sprintf("%v", talkgroupsMap[j]["label"])
			return labelA < labelB
		})
	} else {
		// Sort by custom order field
		sort.Slice(talkgroupsMap, func(i int, j int) bool {
			if a, err := strconv.Atoi(fmt.Sprintf("%v", talkgroupsMap[i]["order"])); err == nil {
				if b, err := strconv.Atoi(fmt.Sprintf("%v", talkgroupsMap[j]["order"])); err == nil {
					return a < b
				}
			}
			return false
		})
	}

	systemMap := SystemMap{
		"id":         rawSystem.SystemRef,
		"systemId":   rawSystem.Id,          // Database ID for admin/backend use
		"systemRef":  rawSystem.SystemRef,   // Radio reference ID
		"label":      rawSystem.Label,
		"order":      rawSystem.Order,
		"talkgroups": talkgroupsMap,
		"units":      rawSystem.Units.List,
		"type":       rawSystem.Kind,
	}

		systemsMap = append(systemsMap, systemMap)
	}

	sort.Slice(systemsMap, func(i int, j int) bool {
		if a, err := strconv.Atoi(fmt.Sprintf("%v", systemsMap[i]["order"])); err == nil {
			if b, err := strconv.Atoi(fmt.Sprintf("%v", systemsMap[j]["order"])); err == nil {
				return a < b
			}
		}
		return false
	})

	return systemsMap
}

func (systems *Systems) Read(db *Database) error {
	var (
		err   error
		query string
		rows  *sql.Rows
		tx    *sql.Tx
	)

	systems.mutex.Lock()
	defer systems.mutex.Unlock()

	systems.List = []*System{}

	formatError := errorFormatter("systems", "read")

	if tx, err = db.Sql.Begin(); err != nil {
		return formatError(err, "")
	}

	query = `SELECT "systemId", "autoPopulate", "blacklists", "delay", "label", "order", "systemRef", "type" FROM "systems"`
	if rows, err = tx.Query(query); err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	for rows.Next() {
		system := NewSystem()

		if err = rows.Scan(&system.Id, &system.AutoPopulate, &system.Blacklists, &system.Delay, &system.Label, &system.Order, &system.SystemRef, &system.Kind); err != nil {
			break
		}

		systems.List = append(systems.List, system)
	}

	rows.Close()

	if err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	for _, system := range systems.List {
		if err = system.Sites.ReadTx(tx, system.Id); err != nil {
			break
		}

		if err = system.Talkgroups.ReadTx(tx, system.Id, db.Config.DbType); err != nil {
			break
		}

		if err = system.Units.ReadTx(tx, system.Id); err != nil {
			break
		}
	}

	if err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	sort.Slice(systems.List, func(i int, j int) bool {
		return systems.List[i].Order < systems.List[j].Order
	})

	return nil
}

func (systems *Systems) Write(db *Database) error {
	var (
		err       error
		query     string
		res       sql.Result
		rows      *sql.Rows
		systemIds = []uint64{}
		tx        *sql.Tx
	)

	systems.mutex.Lock()
	defer systems.mutex.Unlock()

	formatError := errorFormatter("systems", "write")

	if tx, err = db.Sql.Begin(); err != nil {
		return formatError(err, "")
	}

	query = `SELECT "systemId" FROM "systems"`
	if rows, err = tx.Query(query); err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	for rows.Next() {
		var systemId uint64
		if err = rows.Scan(&systemId); err != nil {
			break
		}
		remove := true
		for _, system := range systems.List {
			if system.Id == 0 || system.Id == systemId {
				remove = false
				break
			}
		}
		if remove {
			systemIds = append(systemIds, systemId)
		}
	}

	rows.Close()

	if err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	if len(systemIds) > 0 {
		if b, err := json.Marshal(systemIds); err == nil {
			in := strings.ReplaceAll(strings.ReplaceAll(string(b), "[", "("), "]", ")")

			query = fmt.Sprintf(`DELETE FROM "systems" WHERE "systemId" IN %s`, in)
			if res, err = tx.Exec(query); err != nil {
				tx.Rollback()
				return formatError(err, query)
			}

			if count, err := res.RowsAffected(); err == nil && count > 0 {
				query = fmt.Sprintf(`DELETE FROM "sites" WHERE "systemId" IN %s`, in)
				if _, err = tx.Exec(query); err != nil {
					tx.Rollback()
					return formatError(err, query)
				}

				query = fmt.Sprintf(`DELETE FROM "talkgroups" WHERE "systemId" IN %s`, in)
				if _, err = tx.Exec(query); err != nil {
					tx.Rollback()
					return formatError(err, query)
				}

				query = fmt.Sprintf(`DELETE FROM "units" WHERE "systemId" IN %s`, in)
				if _, err = tx.Exec(query); err != nil {
					tx.Rollback()
					return formatError(err, query)
				}
			}
		}
	}

	for _, system := range systems.List {
		var count uint
		var existingId uint64

		// First check if a system with this ID already exists
		if system.Id > 0 {
			query = fmt.Sprintf(`SELECT COUNT(*) FROM "systems" WHERE "systemId" = %d`, system.Id)
			if err = tx.QueryRow(query).Scan(&count); err != nil {
				break
			}
		}

		// If not found by ID, check if a system with the same SystemRef exists
		// This prevents duplicates when auto-creating systems
		if count == 0 && system.SystemRef > 0 {
			query = fmt.Sprintf(`SELECT "systemId" FROM "systems" WHERE "systemRef" = %d LIMIT 1`, system.SystemRef)
			if err = tx.QueryRow(query).Scan(&existingId); err == nil && existingId > 0 {
				// Found existing system with same SystemRef, use its ID
				system.Id = existingId
				count = 1
			} else if err != nil && err != sql.ErrNoRows {
				// Real error occurred
				break
			}
		}

		if count == 0 {
			if system.Id > 0 {
				// Preserve the explicit ID when inserting
				query = fmt.Sprintf(`INSERT INTO "systems" ("systemId", "autoPopulate", "blacklists", "delay", "label", "order", "systemRef", "type") VALUES (%d, %t, '%s', %d, '%s', %d, %d, '%s')`, system.Id, system.AutoPopulate, system.Blacklists, system.Delay, escapeQuotes(system.Label), system.Order, system.SystemRef, system.Kind)
			} else {
				// Let database assign auto-increment ID
				query = fmt.Sprintf(`INSERT INTO "systems" ("autoPopulate", "blacklists", "delay", "label", "order", "systemRef", "type") VALUES (%t, '%s', %d, '%s', %d, %d, '%s')`, system.AutoPopulate, system.Blacklists, system.Delay, escapeQuotes(system.Label), system.Order, system.SystemRef, system.Kind)
			}

			if db.Config.DbType == DbTypePostgresql {
				if system.Id > 0 {
					// When inserting with explicit ID, don't use RETURNING as it's already set
					if _, err = tx.Exec(query); err != nil {
						break
					}
				} else {
					// Only use RETURNING when database assigns the ID
					query = query + ` RETURNING "systemId"`
					if err = tx.QueryRow(query).Scan(&system.Id); err != nil {
						break
					}
				}

			} else {
				if res, err = tx.Exec(query); err == nil {
					// Only get LastInsertId when we didn't specify an explicit ID
					if system.Id == 0 {
						if id, err := res.LastInsertId(); err == nil {
							system.Id = uint64(id)
						}
					}
					// If system.Id > 0, we already have the ID, so don't override it
				} else {
					break
				}
			}

		} else {
			query = fmt.Sprintf(`UPDATE "systems" SET "autoPopulate" = %t, "blacklists" = '%s', "delay" = %d, "label" = '%s', "order" = %d, "systemRef" = %d, "type" = '%s' WHERE "systemId" = %d`, system.AutoPopulate, system.Blacklists, system.Delay, escapeQuotes(system.Label), system.Order, system.SystemRef, system.Kind, system.Id)
			if _, err = tx.Exec(query); err != nil {
				break
			}
		}

		query = ""

		if err = system.Sites.WriteTx(tx, system.Id); err != nil {
			break
		}

		if err = system.Talkgroups.WriteTx(tx, system.Id, db.Config.DbType); err != nil {
			break
		}

		if err = system.Units.WriteTx(tx, system.Id); err != nil {
			break
		}
	}

	if err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	if err = tx.Commit(); err != nil {
		log.Printf("DEBUG: Systems.Write() - Transaction commit failed: %v", err)
		tx.Rollback()
		return formatError(err, "")
	}

	log.Printf("Systems.Write() - Write operation completed successfully")
	return nil
}

type SystemsMap []SystemMap
