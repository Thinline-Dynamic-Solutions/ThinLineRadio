// Copyright (C) 2025 Thinline Dynamic Solutions
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
	"sort"
	"strconv"
)

// RadioReferenceLink is the Radio Reference system an admin picked for a system.
// The state and county are kept so the picker can reopen where it left off.
type RadioReferenceLink struct {
	SystemId   int    `json:"systemId"`
	SystemName string `json:"systemName"`
	StateId    int    `json:"stateId"`
	StateName  string `json:"stateName"`
	CountyId   int    `json:"countyId"`
	CountyName string `json:"countyName"`
}

func migrateSystemRouting(db *Database) error {
	_, err := db.Sql.Exec(`CREATE TABLE IF NOT EXISTS "systemRouting" (
		"systemId" bigint NOT NULL PRIMARY KEY,
		"isMain" boolean NOT NULL DEFAULT false,
		"parentIds" text NOT NULL DEFAULT '[]',
		"talkgroupNetworks" text NOT NULL DEFAULT '{}',
		"radioReference" text NOT NULL DEFAULT '{}'
	)`)
	if err != nil {
		return fmt.Errorf("migrateSystemRouting: %w", err)
	}
	return nil
}

func routingFromMap(system *System, m map[string]any) {
	if v, ok := m["isMain"].(bool); ok {
		system.IsMain = v
	}

	system.ParentIds = nil
	if v, ok := m["parentIds"].([]any); ok {
		seen := map[uint64]bool{}
		for _, raw := range v {
			if f, ok := raw.(float64); ok && f > 0 && !seen[uint64(f)] {
				seen[uint64(f)] = true
				system.ParentIds = append(system.ParentIds, uint64(f))
			}
		}
	}

	system.TalkgroupNetworks = nil
	if v, ok := m["talkgroupNetworks"].(map[string]any); ok {
		for key, raw := range v {
			ref, err := strconv.ParseUint(key, 10, 64)
			f, ok := raw.(float64)
			if err != nil || !ok || f <= 0 {
				continue
			}
			if system.TalkgroupNetworks == nil {
				system.TalkgroupNetworks = map[uint]uint64{}
			}
			system.TalkgroupNetworks[uint(ref)] = uint64(f)
		}
	}

	system.RadioReference = RadioReferenceLink{}
	if v, ok := m["radioReference"].(map[string]any); ok {
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &system.RadioReference)
	}

	if system.IsMain {
		system.ParentIds = nil
		system.TalkgroupNetworks = nil
		// A main system hands its calls to subsystems, so it would always look silent.
		system.NoAudioAlertsEnabled = false
	}
}

func routingToMap(system *System, m map[string]any) {
	m["isMain"] = system.IsMain
	parentIds := system.ParentIds
	if parentIds == nil {
		parentIds = []uint64{}
	}
	m["parentIds"] = parentIds
	networks := map[string]uint64{}
	for ref, mainId := range system.TalkgroupNetworks {
		networks[strconv.FormatUint(uint64(ref), 10)] = mainId
	}
	m["talkgroupNetworks"] = networks
	m["radioReference"] = system.RadioReference
}

// preserveRoutingFields keeps an existing system's routing and Radio Reference link
// when a save payload leaves those fields out.
func preserveRoutingFields(m map[string]any, existing *System) {
	if existing == nil {
		return
	}
	current := map[string]any{}
	routingToMap(existing, current)
	b, _ := json.Marshal(current)
	_ = json.Unmarshal(b, &current)
	for key, value := range current {
		if _, has := m[key]; !has {
			m[key] = value
		}
	}
}

// routesFrom reports whether a subsystem's talkgroup takes calls uploaded to mainId.
// With one main system every talkgroup does; with several, a talkgroup set to one
// network only routes from that main system, and an unset one routes from all.
func (system *System) routesFrom(mainId uint64, talkgroupRef uint) bool {
	if len(system.ParentIds) < 2 {
		return true
	}
	if network, ok := system.TalkgroupNetworks[talkgroupRef]; ok {
		return network == mainId
	}
	return true
}

func (system *System) isUnder(mainId uint64) bool {
	for _, id := range system.ParentIds {
		if id == mainId {
			return true
		}
	}
	return false
}

func (systems *Systems) loadSystemRouting(db *Database) error {
	rows, err := db.Sql.Query(`SELECT "systemId", "isMain", "parentIds", "talkgroupNetworks", "radioReference" FROM "systemRouting"`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type row struct {
		isMain                    bool
		parents, networks, rrLink sql.NullString
	}
	byId := map[uint64]row{}
	for rows.Next() {
		var id uint64
		var r row
		if err := rows.Scan(&id, &r.isMain, &r.parents, &r.networks, &r.rrLink); err != nil {
			return err
		}
		byId[id] = r
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, sys := range systems.List {
		r, ok := byId[sys.Id]
		if !ok {
			continue
		}
		m := map[string]any{"isMain": r.isMain}
		var parents []any
		if json.Unmarshal([]byte(r.parents.String), &parents) == nil {
			m["parentIds"] = parents
		}
		var networks map[string]any
		if json.Unmarshal([]byte(r.networks.String), &networks) == nil {
			m["talkgroupNetworks"] = networks
		}
		var rrLink map[string]any
		if json.Unmarshal([]byte(r.rrLink.String), &rrLink) == nil {
			m["radioReference"] = rrLink
		}
		routingFromMap(sys, m)
	}

	// Links to deleted systems, or to systems that are no longer main, are dropped.
	mains := map[uint64]bool{}
	for _, sys := range systems.List {
		if sys.IsMain {
			mains[sys.Id] = true
		}
	}
	for _, sys := range systems.List {
		kept := sys.ParentIds[:0]
		for _, id := range sys.ParentIds {
			if mains[id] && id != sys.Id {
				kept = append(kept, id)
			}
		}
		sys.ParentIds = kept
		for ref, id := range sys.TalkgroupNetworks {
			if !sys.isUnder(id) {
				delete(sys.TalkgroupNetworks, ref)
			}
		}
	}

	systems.buildRoutesLocked()
	return nil
}

func (systems *Systems) saveSystemRouting(db *Database) error {
	for _, sys := range systems.List {
		if sys == nil || sys.Id == 0 {
			continue
		}
		parents, _ := json.Marshal(nonNilIds(sys.ParentIds))
		networks := map[string]uint64{}
		for ref, id := range sys.TalkgroupNetworks {
			networks[strconv.FormatUint(uint64(ref), 10)] = id
		}
		networksJSON, _ := json.Marshal(networks)
		rrLink, _ := json.Marshal(sys.RadioReference)
		if _, err := db.Sql.Exec(`INSERT INTO "systemRouting" ("systemId", "isMain", "parentIds", "talkgroupNetworks", "radioReference")
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT ("systemId") DO UPDATE SET "isMain" = EXCLUDED."isMain", "parentIds" = EXCLUDED."parentIds",
				"talkgroupNetworks" = EXCLUDED."talkgroupNetworks", "radioReference" = EXCLUDED."radioReference"`,
			sys.Id, sys.IsMain, string(parents), string(networksJSON), string(rrLink)); err != nil {
			return err
		}
	}
	for _, table := range []string{"systemRouting", "radioReferenceReviews"} {
		if _, err := db.Sql.Exec(fmt.Sprintf(`DELETE FROM "%s" WHERE "systemId" NOT IN (SELECT "systemId" FROM "systems")`, table)); err != nil {
			return err
		}
	}
	return nil
}

func nonNilIds(ids []uint64) []uint64 {
	if ids == nil {
		return []uint64{}
	}
	return ids
}

// buildRoutesLocked maps each main system's talkgroups to the subsystem that owns them.
// The caller holds systems.mutex.
func (systems *Systems) buildRoutesLocked() {
	routes := map[uint64]map[uint]*System{}
	for _, sys := range systems.List {
		if sys.IsMain {
			routes[sys.Id] = map[uint]*System{}
		}
	}
	for _, sub := range systems.List {
		for _, mainId := range sub.ParentIds {
			table, ok := routes[mainId]
			if !ok {
				continue
			}
			for _, tg := range sub.Talkgroups.List {
				if tg == nil || !sub.routesFrom(mainId, tg.TalkgroupRef) {
					continue
				}
				if _, taken := table[tg.TalkgroupRef]; !taken {
					table[tg.TalkgroupRef] = sub
				}
			}
		}
	}
	systems.routes = routes
}

// RouteFromMain finds the subsystem that takes a call uploaded to a main system.
// Patched talkgroups are tried when the call's own talkgroup isn't in any subsystem.
func (systems *Systems) RouteFromMain(main *System, talkgroupRef uint, patches []uint) (*System, bool) {
	if main == nil || !main.IsMain {
		return nil, false
	}
	systems.mutex.RLock()
	defer systems.mutex.RUnlock()

	table := systems.routes[main.Id]
	if sub, ok := table[talkgroupRef]; ok {
		return sub, true
	}
	for _, ref := range patches {
		if sub, ok := table[ref]; ok {
			return sub, true
		}
	}
	return nil, false
}

// RouteUpload finds the subsystem for a call uploaded to systemRef, when systemRef is
// a main system and one of its subsystems has the talkgroup.
func (systems *Systems) RouteUpload(systemRef uint, talkgroupRef uint, patches []uint) (*System, bool) {
	main, ok := systems.GetSystemByRef(systemRef)
	if !ok || !main.IsMain {
		return nil, false
	}
	return systems.RouteFromMain(main, talkgroupRef, patches)
}

// ValidateSystemRouting lists what stops a set of systems from being saved:
// shared system IDs, bad main system links, and talkgroups that more than one
// subsystem would claim under the same main system.
func ValidateSystemRouting(list []*System) []string {
	problems := []string{}
	name := func(s *System) string {
		if s.Label != "" {
			return fmt.Sprintf("%s (ID %d)", s.Label, s.SystemRef)
		}
		return fmt.Sprintf("system ID %d", s.SystemRef)
	}

	byRef := map[uint]*System{}
	byId := map[uint64]*System{}
	for _, s := range list {
		if s == nil {
			continue
		}
		if s.SystemRef > 0 {
			if other, ok := byRef[s.SystemRef]; ok {
				problems = append(problems, fmt.Sprintf("System ID %d is used by both %s and %s. Every main system and subsystem needs its own ID.", s.SystemRef, other.Label, s.Label))
			} else {
				byRef[s.SystemRef] = s
			}
		}
		if s.Id > 0 {
			byId[s.Id] = s
		}
	}

	for _, s := range list {
		if s == nil {
			continue
		}
		if s.IsMain && len(s.ParentIds) > 0 {
			problems = append(problems, fmt.Sprintf("%s is a main system, so it can't also be under another main system.", name(s)))
		}
		for _, id := range s.ParentIds {
			parent, ok := byId[id]
			switch {
			case id == s.Id:
				problems = append(problems, fmt.Sprintf("%s can't be under itself.", name(s)))
			case !ok:
				problems = append(problems, fmt.Sprintf("%s is under a main system that no longer exists.", name(s)))
			case !parent.IsMain:
				problems = append(problems, fmt.Sprintf("%s is under %s, which isn't marked as a main system.", name(s), name(parent)))
			}
		}
		refs := make([]int, 0, len(s.TalkgroupNetworks))
		for ref := range s.TalkgroupNetworks {
			refs = append(refs, int(ref))
		}
		sort.Ints(refs)
		for _, ref := range refs {
			if !s.isUnder(s.TalkgroupNetworks[uint(ref)]) {
				problems = append(problems, fmt.Sprintf("Talkgroup %d on %s is set to a network this system isn't under.", ref, name(s)))
			}
		}
	}

	for _, main := range list {
		if main == nil || !main.IsMain || main.Id == 0 {
			continue
		}
		owner := map[uint]*System{}
		clashes := map[string]bool{}
		for _, sub := range list {
			if sub == nil || !sub.isUnder(main.Id) {
				continue
			}
			for _, tg := range sub.Talkgroups.List {
				if tg == nil || !sub.routesFrom(main.Id, tg.TalkgroupRef) {
					continue
				}
				if other, ok := owner[tg.TalkgroupRef]; ok && other != sub {
					msg := fmt.Sprintf("Talkgroup %d is in both %s and %s, which are both under %s, so a call uploaded to %s couldn't tell where it goes.",
						tg.TalkgroupRef, other.Label, sub.Label, main.Label, main.Label)
					if !clashes[msg] {
						clashes[msg] = true
						problems = append(problems, msg)
					}
					continue
				}
				owner[tg.TalkgroupRef] = sub
			}
		}
	}

	return problems
}
