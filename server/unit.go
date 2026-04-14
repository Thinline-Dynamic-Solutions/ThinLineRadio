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
	"sort"
	"strings"
	"sync"
)

type Unit struct {
	Id       uint64
	Label    string
	Order    uint
	SystemId uint64
	UnitRef  uint
	UnitFrom uint
	UnitTo   uint
}

func NewUnit() *Unit {
	return &Unit{}
}

func (unit *Unit) FromMap(m map[string]any) *Unit {
	refFromMap := false
	if uv, ok := m["unitRef"]; ok && uv != nil {
		if v, ok := uv.(float64); ok {
			unit.UnitRef = uint(v)
			refFromMap = true
		}
	}
	_, unitRefKeyPresent := m["unitRef"]

	// Primary key: JSON "id" is the database unitId. Legacy MarshalJSON used radio unitRef as "id".
	if v, ok := m["id"].(float64); ok {
		idVal := uint64(v)
		if refFromMap && unit.UnitRef > 0 && idVal == uint64(unit.UnitRef) {
			unit.Id = 0
		} else if refFromMap {
			unit.Id = idVal
		} else if !unitRefKeyPresent {
			unit.UnitRef = uint(v)
			unit.Id = 0
		} else {
			unit.Id = idVal
		}
	} else if v, ok := m["_id"].(float64); ok {
		idVal := uint64(v)
		if refFromMap && unit.UnitRef > 0 && idVal == uint64(unit.UnitRef) {
			unit.Id = 0
		} else if refFromMap {
			unit.Id = idVal
		} else if !unitRefKeyPresent {
			unit.UnitRef = uint(v)
			unit.Id = 0
		} else {
			unit.Id = idVal
		}
	}

	switch v := m["label"].(type) {
	case string:
		unit.Label = v
	}

	switch v := m["order"].(type) {
	case float64:
		unit.Order = uint(v)
	}

	switch v := m["systemId"].(type) {
	case float64:
		unit.SystemId = uint64(v)
	}

	switch v := m["unitFrom"].(type) {
	case float64:
		unit.UnitFrom = uint(v)
	}

	switch v := m["unitTo"].(type) {
	case float64:
		unit.UnitTo = uint(v)
	}

	return unit
}

func (unit *Unit) MarshalJSON() ([]byte, error) {
	m := map[string]any{
		"label": unit.Label,
	}
	// "id" is the database primary key (unitId), not the radio unitRef — see issue #172.
	if unit.Id > 0 {
		m["id"] = unit.Id
	}

	if unit.Order > 0 {
		m["order"] = unit.Order
	}

	if unit.UnitRef > 0 {
		m["unitRef"] = unit.UnitRef
	}

	if unit.UnitFrom > 0 {
		m["unitFrom"] = unit.UnitFrom
	}

	if unit.UnitTo > 0 {
		m["unitTo"] = unit.UnitTo
	}

	return json.Marshal(m)
}

type Units struct {
	List     []*Unit
	SystemId uint64
	mutex    sync.Mutex
}

func NewUnits() *Units {
	return &Units{
		List:  []*Unit{},
		mutex: sync.Mutex{},
	}
}

func (units *Units) Add(unitRef uint, label string) (*Units, bool) {
	added := true

	for _, u := range units.List {
		if u.UnitRef == unitRef {
			added = false
			break
		}
	}

	if added {
		units.List = append(units.List, &Unit{Label: label, UnitRef: unitRef})
	}

	return units, added
}

func (units *Units) FromMap(f []any) *Units {
	units.mutex.Lock()
	defer units.mutex.Unlock()

	units.List = []*Unit{}

	for _, r := range f {
		switch m := r.(type) {
		case map[string]any:
			unit := NewUnit().FromMap(m)
			units.List = append(units.List, unit)
		}
	}

	return units
}

func (u *Units) Merge(units *Units) bool {
	merged := false

	if units != nil {
		u.mutex.Lock()
		defer u.mutex.Unlock()

		for _, unit := range units.List {
			if _, added := u.Add(unit.UnitRef, unit.Label); added {
				merged = added
			}
		}
	}

	return merged
}

func (units *Units) ReadTx(tx *sql.Tx, systemId uint64) error {
	var (
		err   error
		query string
		rows  *sql.Rows
	)

	units.mutex.Lock()
	defer units.mutex.Unlock()

	units.List = []*Unit{}

	formatError := errorFormatter("units", "read")

	query = fmt.Sprintf(`SELECT "unitId", "label", "order", "unitRef", "unitFrom", "unitTo" FROM "units" WHERE "systemId" = %d`, systemId)
	if rows, err = tx.Query(query); err != nil {
		return formatError(err, query)
	}

	for rows.Next() {
		unit := NewUnit()

		if err = rows.Scan(&unit.Id, &unit.Label, &unit.Order, &unit.UnitRef, &unit.UnitFrom, &unit.UnitTo); err != nil {
			continue
		}

		units.List = append(units.List, unit)
	}

	rows.Close()

	if err != nil {
		return formatError(err, "")
	}

	sort.Slice(units.List, func(i int, j int) bool {
		return units.List[i].Order < units.List[j].Order
	})

	return nil
}

func (units *Units) WriteTx(tx *sql.Tx, systemId uint64) error {
	var (
		err     error
		query   string
		rows    *sql.Rows
		unitIds = []uint64{}
	)

	units.mutex.Lock()
	defer units.mutex.Unlock()

	formatError := errorFormatter("units", "writetx")

	incomingIDs := map[uint64]struct{}{}
	incomingRefNoPK := map[uint]struct{}{}
	for _, u := range units.List {
		if u.Id > 0 {
			incomingIDs[u.Id] = struct{}{}
		}
		if u.Id == 0 && u.UnitRef > 0 {
			incomingRefNoPK[u.UnitRef] = struct{}{}
		}
	}

	query = fmt.Sprintf(`SELECT "unitId", "unitRef" FROM "units" WHERE "systemId" = %d`, systemId)
	if rows, err = tx.Query(query); err != nil {
		return formatError(err, query)
	}

	for rows.Next() {
		var unitId uint64
		var unitRef uint
		if err = rows.Scan(&unitId, &unitRef); err != nil {
			break
		}
		_, keepByPK := incomingIDs[unitId]
		_, keepByRef := incomingRefNoPK[unitRef]
		if !keepByPK && !keepByRef {
			unitIds = append(unitIds, unitId)
		}
	}

	rows.Close()

	if err != nil {
		return formatError(err, "")
	}

	if len(unitIds) > 0 {
		if b, err := json.Marshal(unitIds); err == nil {
			in := strings.ReplaceAll(strings.ReplaceAll(string(b), "[", "("), "]", ")")
			query = fmt.Sprintf(`DELETE FROM "units" WHERE "unitId" IN %s`, in)
			if _, err = tx.Exec(query); err != nil {
				return formatError(err, query)
			}
		}
	}

	for _, unit := range units.List {
		if unit.Id > 0 {
			var count uint
			query = fmt.Sprintf(`SELECT COUNT(*) FROM "units" WHERE "unitId" = %d AND "systemId" = %d`, unit.Id, systemId)
			if err = tx.QueryRow(query).Scan(&count); err != nil {
				break
			}
			if count > 0 {
				query = fmt.Sprintf(`UPDATE "units" SET "label" = '%s', "order" = %d, "unitRef" = %d, "unitFrom" = %d, "unitTo" = %d WHERE "unitId" = %d AND "systemId" = %d`, escapeQuotes(unit.Label), unit.Order, unit.UnitRef, unit.UnitFrom, unit.UnitTo, unit.Id, systemId)
				if _, err = tx.Exec(query); err != nil {
					break
				}
				continue
			}
		}

		if unit.UnitRef > 0 {
			var existingId uint64
			q2 := fmt.Sprintf(`SELECT "unitId" FROM "units" WHERE "systemId" = %d AND "unitRef" = %d LIMIT 1`, systemId, unit.UnitRef)
			scanErr := tx.QueryRow(q2).Scan(&existingId)
			if scanErr == sql.ErrNoRows {
				// fall through to INSERT
			} else if scanErr != nil {
				err = scanErr
				break
			} else if existingId > 0 {
				query = fmt.Sprintf(`UPDATE "units" SET "label" = '%s', "order" = %d, "unitRef" = %d, "unitFrom" = %d, "unitTo" = %d WHERE "unitId" = %d AND "systemId" = %d`, escapeQuotes(unit.Label), unit.Order, unit.UnitRef, unit.UnitFrom, unit.UnitTo, existingId, systemId)
				if _, err = tx.Exec(query); err != nil {
					break
				}
				continue
			}
		}

		query = fmt.Sprintf(`INSERT INTO "units" ("label", "order", "systemId", "unitRef", "unitFrom", "unitTo") VALUES ('%s', %d, %d, %d, %d, %d)`, escapeQuotes(unit.Label), unit.Order, systemId, unit.UnitRef, unit.UnitFrom, unit.UnitTo)
		if _, err = tx.Exec(query); err != nil {
			break
		}
	}

	if err != nil {
		return formatError(err, query)
	}

	return nil
}
