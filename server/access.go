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
	"strings"
	"sync"
	"time"
)

type Access struct {
	Id         any    `json:"_id"`
	Code       string `json:"code"`
	Expiration any    `json:"expiration"`
	Ident      string `json:"ident"`
	Limit      any    `json:"limit"`
	Order      any    `json:"order"`
	Systems    any    `json:"systems"`
}

func NewAccess() *Access {
	return &Access{Systems: "*"}
}

func (access *Access) FromMap(m map[string]any) *Access {
	switch v := m["_id"].(type) {
	case float64:
		access.Id = uint(v)
	}

	switch v := m["code"].(type) {
	case string:
		access.Code = v
	}

	switch v := m["expiration"].(type) {
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			access.Expiration = t.UTC()
		}
	}

	switch v := m["ident"].(type) {
	case string:
		access.Ident = v
	}

	switch v := m["limit"].(type) {
	case float64:
		access.Limit = uint(v)
	}

	switch v := m["order"].(type) {
	case float64:
		access.Order = uint(v)
	}

	switch v := m["systems"].(type) {
	case []any:
		// Keep as array for HasAccess() to work correctly
		access.Systems = v
	case string:
		// Could be "*" or a JSON string - try to unmarshal if it looks like JSON
		if v == "*" {
			access.Systems = "*"
		} else {
			// Try to unmarshal as JSON array
			var unmarshaled any
			if err := json.Unmarshal([]byte(v), &unmarshaled); err == nil {
				access.Systems = unmarshaled
			} else {
				// If unmarshal fails, keep as string (might be "*" or invalid)
				access.Systems = v
			}
		}
	}

	return access
}

// HasAccess checks if access code has permission for a call (v7 version - uses systemId/talkgroupId)
func (access *Access) HasAccess(call *Call) bool {
	if access.Systems != nil {
		switch v := access.Systems.(type) {
		case []any:
			for _, f := range v {
				switch v := f.(type) {
				case map[string]any:
					switch id := v["id"].(type) {
					case float64:
						systemRef := uint(id)

						// Get system ref from call (check multiple sources)
						var callSystemRef uint
						if call.System != nil {
							callSystemRef = call.System.SystemRef
						} else if call.Meta.SystemRef > 0 {
							callSystemRef = call.Meta.SystemRef
						} else if call.SystemId > 0 {
							// SystemId might be the ref in v6 compatibility mode
							callSystemRef = call.SystemId
						}

						// Check if systemRef matches
						if callSystemRef > 0 && callSystemRef == systemRef {
							switch tg := v["talkgroups"].(type) {
							case string:
								if tg == "*" {
									return true
								}
							case []any:
								// Get talkgroup ref from call (check multiple sources)
								var callTalkgroupRef uint
								if call.Talkgroup != nil {
									callTalkgroupRef = call.Talkgroup.TalkgroupRef
								} else if call.Meta.TalkgroupRef > 0 {
									callTalkgroupRef = call.Meta.TalkgroupRef
								} else if call.TalkgroupId > 0 {
									// TalkgroupId might be the ref in v6 compatibility mode
									callTalkgroupRef = call.TalkgroupId
								}

								for _, f := range tg {
									switch tg := f.(type) {
									case float64:
										if callTalkgroupRef > 0 && uint(tg) == callTalkgroupRef {
											return true
										}
									}
								}
							}
						}
					}
				}
			}

		case string:
			if v == "*" {
				return true
			}
		}
	}

	return false
}

func (access *Access) HasExpired() bool {
	switch v := access.Expiration.(type) {
	case time.Time:
		return v.Before(time.Now())
	}
	return false
}

type Accesses struct {
	List  []*Access
	mutex sync.Mutex
}

func NewAccesses() *Accesses {
	return &Accesses{
		List:  []*Access{},
		mutex: sync.Mutex{},
	}
}

func (accesses *Accesses) Add(access *Access) (*Accesses, bool) {
	accesses.mutex.Lock()
	defer accesses.mutex.Unlock()

	added := true

	for _, a := range accesses.List {
		if a.Code == access.Code {
			a.Expiration = access.Expiration
			a.Ident = access.Ident
			a.Limit = access.Limit
			a.Systems = access.Systems
			added = false
		}
	}

	if added {
		accesses.List = append(accesses.List, access)
	}

	return accesses, added
}

func (accesses *Accesses) FromMap(f []any) *Accesses {
	accesses.mutex.Lock()
	defer accesses.mutex.Unlock()

	accesses.List = []*Access{}

	for _, r := range f {
		switch m := r.(type) {
		case map[string]any:
			access := &Access{}
			access.FromMap(m)
			accesses.List = append(accesses.List, access)
		}
	}

	return accesses
}

func (accesses *Accesses) GetAccess(code string) (access *Access, ok bool) {
	accesses.mutex.Lock()
	defer accesses.mutex.Unlock()

	for _, access := range accesses.List {
		if access.Code == code {
			return access, true
		}
	}

	return nil, false
}

func (accesses *Accesses) IsRestricted() bool {
	accesses.mutex.Lock()
	defer accesses.mutex.Unlock()

	return len(accesses.List) > 0
}

func (accesses *Accesses) Read(db *Database) error {
	var (
		err        error
		expiration sql.NullTime
		id         sql.NullFloat64
		limit      sql.NullFloat64
		order      sql.NullFloat64
		rows       *sql.Rows
		systems    string
	)

	accesses.mutex.Lock()
	defer accesses.mutex.Unlock()

	accesses.List = []*Access{}

	formatError := func(err error) error {
		return fmt.Errorf("accesses.read: %v", err)
	}

	log.Printf("DEBUG: Accesses.Read() starting - reading from database")

	// Explicitly use public schema to avoid search_path issues
	query := `SELECT "accessId", "code", "expiration", "ident", "limit", "order", "systems" FROM "public"."accesses"`

	if rows, err = db.Sql.Query(query); err != nil {
		// Table should exist from schema creation - if it doesn't, try to create it
		errStr := err.Error()
		if strings.Contains(errStr, "does not exist") || strings.Contains(errStr, "relation") || strings.Contains(errStr, "Unknown table") {
			log.Printf("WARNING: accesses table does not exist in Read(), attempting to create it...")
			log.Printf("WARNING: Database: %s, Host: %s, Port: %d", db.Config.DbName, db.Config.DbHost, db.Config.DbPort)

			// Try to create the table - explicitly in public schema
			var createQuery string
			if db.Config.DbType == DbTypePostgresql {
				createQuery = `CREATE TABLE IF NOT EXISTS "public"."accesses" (
    "accessId" bigserial NOT NULL PRIMARY KEY,
    "code" text NOT NULL UNIQUE,
    "expiration" timestamp,
    "ident" text NOT NULL DEFAULT '',
    "limit" integer,
    "order" integer,
    "systems" text NOT NULL DEFAULT ''
  )`
			} else {
				createQuery = `CREATE TABLE IF NOT EXISTS "accesses" (
    "accessId" bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
    "code" text NOT NULL UNIQUE,
    "expiration" datetime,
    "ident" text NOT NULL DEFAULT '',
    "limit" integer,
    "order" integer,
    "systems" text NOT NULL DEFAULT ''
  )`
			}
			if _, createErr := db.Sql.Exec(createQuery); createErr != nil {
				log.Printf("ERROR: Failed to create accesses table in Read(): %v", createErr)
				return formatError(err) // Return original error
			}
			log.Printf("WARNING: accesses table created in Read() fallback - this should not be necessary")
			// Retry the query after creating the table
			if rows, err = db.Sql.Query(query); err != nil {
				return formatError(err)
			}
		} else {
			return formatError(err)
		}
	}

	for rows.Next() {
		access := &Access{}

		if err = rows.Scan(&id, &access.Code, &expiration, &access.Ident, &limit, &order, &systems); err != nil {
			break
		}

		if id.Valid && id.Float64 > 0 {
			access.Id = uint(id.Float64)
		}

		if len(access.Code) == 0 {
			continue
		}

		if expiration.Valid {
			access.Expiration = expiration.Time
		}

		if len(access.Ident) == 0 {
			access.Ident = "Anonymous"
		}

		if limit.Valid && limit.Float64 > 0 {
			access.Limit = uint(limit.Float64)
		}

		if order.Valid && order.Float64 > 0 {
			access.Order = uint(order.Float64)
		}

		// Handle systems field - can be "*" or JSON array
		if systems == "*" {
			access.Systems = "*"
		} else if err = json.Unmarshal([]byte(systems), &access.Systems); err != nil {
			access.Systems = []any{}
		}

		accesses.List = append(accesses.List, access)
	}

	rows.Close()

	if err != nil {
		return formatError(err)
	}

	log.Printf("DEBUG: Accesses.Read() completed - loaded %d access codes", len(accesses.List))
	return nil
}

func (accesses *Accesses) Remove(access *Access) (*Accesses, bool) {
	accesses.mutex.Lock()
	defer accesses.mutex.Unlock()

	removed := false

	for i, a := range accesses.List {
		if a.Code == access.Code {
			accesses.List = append(accesses.List[:i], accesses.List[i+1:]...)
			removed = true
		}
	}

	return accesses, removed
}

func (accesses *Accesses) Write(db *Database) error {
	var (
		count   uint
		err     error
		rows    *sql.Rows
		rowIds  = []uint{}
		systems any
	)

	accesses.mutex.Lock()
	defer accesses.mutex.Unlock()

	log.Printf("DEBUG: Accesses.Write() starting - writing %d access codes to database", len(accesses.List))

	formatError := func(err error) error {
		return fmt.Errorf("accesses.write: %v", err)
	}

	var query string
	if db.Config.DbType == DbTypePostgresql {
		query = `SELECT "accessId" FROM "public"."accesses"`
	} else {
		query = "SELECT `accessId` FROM `accesses`"
	}

	if rows, err = db.Sql.Query(query); err != nil {
		return formatError(err)
	}

	for rows.Next() {
		var id uint
		if err = rows.Scan(&id); err != nil {
			break
		}
		remove := true
		for _, access := range accesses.List {
			switch v := access.Id.(type) {
			case uint:
				if v == id {
					remove = false
					break
				}
			}
		}
		if remove {
			rowIds = append(rowIds, id)
		}
	}

	rows.Close()

	if err != nil {
		return formatError(err)
	}

	if len(rowIds) > 0 {
		if b, err := json.Marshal(rowIds); err == nil {
			s := string(b)
			s = strings.ReplaceAll(s, "[", "(")
			s = strings.ReplaceAll(s, "]", ")")
			if db.Config.DbType == DbTypePostgresql {
				query = fmt.Sprintf(`DELETE FROM "public"."accesses" WHERE "accessId" IN %s`, s)
			} else {
				query = fmt.Sprintf("DELETE FROM `accesses` WHERE `accessId` IN %s", s)
			}
			if _, err = db.Sql.Exec(query); err != nil {
				return formatError(err)
			}
		}
	}

	for _, access := range accesses.List {
		// Marshal systems to JSON
		if access.Systems == "*" {
			systems = `"*"`
		} else {
			systemsBytes, marshalErr := json.Marshal(access.Systems)
			if marshalErr != nil {
				log.Printf("ERROR: Failed to marshal systems for access code %s: %v", access.Code, marshalErr)
				systems = `"*"` // Default to all systems on error
			} else {
				systems = string(systemsBytes)
			}
		}

		var id uint = 0
		switch v := access.Id.(type) {
		case uint:
			id = v
		case float64:
			id = uint(v)
		}

		// Check if this is an existing record
		isNew := id == 0

		if !isNew {
			if db.Config.DbType == DbTypePostgresql {
				query = `SELECT COUNT(*) FROM "public"."accesses" WHERE "accessId" = $1`
			} else {
				query = "SELECT COUNT(*) FROM `accesses` WHERE `accessId` = ?"
			}

			if err = db.Sql.QueryRow(query, id).Scan(&count); err != nil {
				break
			}
			isNew = count == 0
		}

		if isNew {
			// New record - let database auto-generate the ID
			if db.Config.DbType == DbTypePostgresql {
				query = `INSERT INTO "public"."accesses" ("code", "expiration", "ident", "limit", "order", "systems") VALUES ($1, $2, $3, $4, $5, $6) RETURNING "accessId"`
				var newId uint
				if err = db.Sql.QueryRow(query, access.Code, access.Expiration, access.Ident, access.Limit, access.Order, systems).Scan(&newId); err != nil {
					break
				}
				access.Id = newId
			} else {
				query = "INSERT INTO `accesses` (`code`, `expiration`, `ident`, `limit`, `order`, `systems`) VALUES (?, ?, ?, ?, ?, ?)"
				result, execErr := db.Sql.Exec(query, access.Code, access.Expiration, access.Ident, access.Limit, access.Order, systems)
				if execErr != nil {
					err = execErr
					break
				}
				lastId, lastIdErr := result.LastInsertId()
				if lastIdErr != nil {
					err = lastIdErr
					break
				}
				access.Id = uint(lastId)
			}

		} else {
			// Update existing record
			if db.Config.DbType == DbTypePostgresql {
				query = `UPDATE "public"."accesses" SET "code" = $1, "expiration" = $2, "ident" = $3, "limit" = $4, "order" = $5, "systems" = $6 WHERE "accessId" = $7`
			} else {
				query = "UPDATE `accesses` SET `code` = ?, `expiration` = ?, `ident` = ?, `limit` = ?, `order` = ?, `systems` = ? WHERE `accessId` = ?"
			}
			if _, err = db.Sql.Exec(query, access.Code, access.Expiration, access.Ident, access.Limit, access.Order, systems, id); err != nil {
				break
			}
		}
	}

	if err != nil {
		return formatError(err)
	}

	log.Printf("DEBUG: Accesses.Write() completed - wrote %d access codes", len(accesses.List))
	return nil
}
