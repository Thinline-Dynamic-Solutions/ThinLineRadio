// Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
// Modified by Thinline Dynamic Solutions
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
	"strings"
	"time"
)

// migrateAccesses - REMOVED: Access codes functionality has been removed
func migrateAccesses(db *Database) error {
	// Access codes are no longer supported - this migration does nothing
	return nil
}

func migrateApikeys(db *Database) error {
	var (
		err   error
		query string
		rows  *sql.Rows
		tx    *sql.Tx

		apikeyId sql.NullInt64
		disabled sql.NullBool
		ident    sql.NullString
		key      sql.NullString
		order    sql.NullInt32
		systems  sql.NullString
	)

	formatError := errorFormatter("migration", "migrateApikeys")

	if _, err = db.Sql.Exec(`SELECT COUNT(*) FROM "rdioScannerApiKeys"`); err != nil {
		return nil
	}

	log.Println("migrating apikeys...")

	if tx, err = db.Sql.Begin(); err != nil {
		return formatError(err, "")
	}

	query = `SELECT "_id", "disabled", "ident", "key", "order", "systems" FROM "rdioScannerApiKeys"`
	if rows, err = tx.Query(query); err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	for rows.Next() {
		apikey := NewApikey()

		if err = rows.Scan(&apikeyId, &disabled, &ident, &key, &order, &systems); err != nil {
			continue
		}

		if apikeyId.Valid {
			apikey.Id = uint64(apikeyId.Int64)
		} else {
			continue
		}

		if disabled.Valid {
			apikey.Disabled = disabled.Bool
		}

		if ident.Valid {
			apikey.Ident = escapeQuotes(ident.String)
		}

		if key.Valid {
			apikey.Key = escapeQuotes(key.String)
		}

		if order.Valid {
			apikey.Order = uint(order.Int32)
		}

		if systems.Valid {
			apikey.Systems = systems.String
		}

		query = fmt.Sprintf(`INSERT INTO "apikeys" ("apikeyId", "disabled", "ident", "key", "order", "systems") VALUES (%d, %t, '%s', '%s', %d, '%s')`, apikey.Id, apikey.Disabled, apikey.Ident, apikey.Key, apikey.Order, apikey.Systems)
		if _, err = tx.Exec(query); err != nil {
			log.Println(formatError(err, query))
		}
	}

	rows.Close()

	query = `DROP TABLE "rdioScannerApiKeys"`
	if _, err = tx.Exec(query); err != nil {
		log.Println(formatError(err, query))
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	return nil
}

func migrateCalls(db *Database) error {
	var (
		err   error
		query string
		rows  *sql.Rows
		tx    *sql.Tx

		systems    = map[int32]int32{}
		talkgroups = map[int32]map[int32]int32{}

		timestamp int64

		callId        sql.NullInt32
		audio         sql.NullString
		audioFilename sql.NullString
		audioMime     sql.NullString
		dateTime      sql.NullTime
		frequencies   sql.NullString
		frequency     sql.NullInt32
		patches       sql.NullString
		source        sql.NullInt32
		sources       sql.NullString
		systemId      sql.NullInt32
		systemRef     sql.NullInt32
		talkgroupId   sql.NullInt32
		talkgroupRef  sql.NullInt32
	)

	formatError := errorFormatter("migration", "migrateCalls")

	if _, err = db.Sql.Exec(`SELECT COUNT(*) FROM "rdioScannerCalls"`); err != nil {
		return nil
	}

	log.Println("migrating calls...")

	query = `SELECT s."systemId", s."systemRef", t."talkgroupId", t."talkgroupRef" FROM "systems" AS s LEFT JOIN "talkgroups" AS t`
	if rows, err = db.Sql.Query(query); err != nil {
		return formatError(err, query)
	}

	for rows.Next() {
		if err = rows.Scan(&systemId, &systemRef, &talkgroupId, &talkgroupRef); err != nil {
			continue
		}

		if systemId.Valid && systemRef.Valid && talkgroupId.Valid && talkgroupRef.Valid {
			if systems[systemRef.Int32] == 0 {
				systems[systemRef.Int32] = systemId.Int32
				talkgroups[systemRef.Int32] = map[int32]int32{}
			}

			talkgroups[systemRef.Int32][talkgroupRef.Int32] = talkgroupId.Int32
		}
	}

	rows.Close()

	if tx, err = db.Sql.Begin(); err != nil {
		return formatError(err, "")
	}

	query = `SELECT "id", "audio", "audioName", "audioType", "dateTime", "frequencies", "frequency", "patches", "source", "sources", "system", "talkgroup" FROM "rdioScannerCalls"`
	if rows, err = tx.Query(query); err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	for rows.Next() {
		call := NewCall()

		if err = rows.Scan(&callId, &audio, &audioFilename, &audioMime, &dateTime, &frequencies, &frequency, &patches, &source, &sources, &systemRef, &talkgroupRef); err != nil {
			continue
		}

		if callId.Valid {
			call.Id = uint64(callId.Int32)
		} else {
			continue
		}

		if audio.Valid && len(audio.String) > 0 {
			call.Audio = []byte(audio.String)
		} else {
			continue
		}

		if audioFilename.Valid {
			call.AudioFilename = escapeQuotes(audioFilename.String)
		}

		if audioMime.Valid {
			call.AudioMime = audioMime.String
		}

		if dateTime.Valid {
			timestamp = dateTime.Time.UnixMilli()
		} else {
			continue
		}

		if !systemRef.Valid || systems[systemRef.Int32] == 0 {
			continue
		}

		if !talkgroupRef.Valid || talkgroups[systemRef.Int32][talkgroupRef.Int32] == 0 {
			continue
		}

		var frequencyValue int64 = 0

		if frequencies.Valid && len(frequencies.String) > 0 {
			var f any
			if err = json.Unmarshal([]byte(frequencies.String), &f); err == nil {
				switch v := f.(type) {
				case []any:
					for _, v := range v {
						switch m := v.(type) {
						case map[string]any:
							switch val := m["freq"].(type) {
							case float64:
								if val > 0 {
									frequencyValue = int64(val)
								}
							}
						}
						if frequencyValue > 0 {
							break
						}
					}
				}
			}
		} else if frequency.Valid && frequency.Int32 > 0 {
			frequencyValue = int64(frequency.Int32)
		}

		query = fmt.Sprintf(`INSERT INTO "calls" ("callId", "audio", "audioFilename", "audioMime", "siteRef", "systemId", "talkgroupId", "timestamp", "frequency") VALUES (%d, $1, '%s', '%s', 0, %d, %d, %d, %d)`, call.Id, call.AudioFilename, call.AudioMime, systems[systemRef.Int32], talkgroups[systemRef.Int32][talkgroupRef.Int32], timestamp, frequencyValue)

		if _, err = tx.Exec(query, call.Audio); err == nil {
			if patches.Valid && len(patches.String) > 0 {
				var f any
				if err = json.Unmarshal([]byte(patches.String), &f); err == nil {
					switch v := f.(type) {
					case []any:
						for _, v := range v {
							switch i := v.(type) {
							case float64:
								if i := talkgroups[systemRef.Int32][int32(i)]; i > 0 {
									query = fmt.Sprintf(`INSERT INTO "callPatches" ("callId", "talkgroupId") VALUES (%d, %d)`, call.Id, i)
									if _, err = tx.Exec(query); err != nil {
										log.Println(formatError(err, query))
									}
								}
							}
						}
					}
				}
			}

			if sources.Valid && len(sources.String) > 0 && sources.String != "[]" {
				var f any
				if err = json.Unmarshal([]byte(sources.String), &f); err == nil {
					switch v := f.(type) {
					case []any:
						for _, v := range v {
							switch m := v.(type) {
							case map[string]any:
								switch src := (m["src"]).(type) {
								case float64:
									if src > 0 {
										query = fmt.Sprintf(`INSERT INTO "callUnits" ("callId", "offset", "unitRef") VALUES (%d, %f, %f)`, call.Id, m["pos"], src)
										if _, err = tx.Exec(query); err != nil {
											log.Println(formatError(err, query))
										}
									}
								}
							}
						}
					}
				}

			} else if source.Valid && source.Int32 > 0 {
				var c int
				query = fmt.Sprintf(`SELECT COUNT(*) FROM "units" WHERE "systemId" = %d AND "unitRef" = %d`, systems[systemRef.Int32], source.Int32)
				if err = tx.QueryRow(query).Scan(&c); err == nil && c == 0 {
					query = fmt.Sprintf(`INSERT INTO "units" ("label", "systemId", "unitRef") VALUES(%d, %d, %d)`, source.Int32, systems[systemRef.Int32], source.Int32)
					if _, err = tx.Exec(query); err != nil {
						log.Println(formatError(err, query))
					} else {
						query = fmt.Sprintf(`INSERT INTO "callUnits" ("callId", "offset", "unitRef") VALUES (%d, %d, %d)`, call.Id, 0, source.Int32)
						if _, err = tx.Exec(query); err != nil {
							log.Println(formatError(err, query))
						}
					}

				} else if err != nil {
					log.Println(formatError(err, query))
				}
			}

		} else {
			log.Println(formatError(err, query))
		}
	}

	rows.Close()

	query = `DROP TABLE "rdioScannerCalls"`
	if _, err = tx.Exec(query); err != nil {
		log.Println(formatError(err, query))
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	return nil
}

func migrateCallsRefs(db *Database) error {
	var (
		err   error
		query string
		tx    *sql.Tx
		count int
	)

	formatError := errorFormatter("migration", "migrateCallsRefs")

	// Check if columns already exist and have data
	query = `SELECT COUNT(*) FROM "calls" WHERE "systemRef" > 0 AND "talkgroupRef" > 0`
	if err = db.Sql.QueryRow(query).Scan(&count); err == nil && count > 0 {
		// Already migrated
		return nil
	}

	log.Println("migrating calls refs (backfilling systemRef and talkgroupRef)...")

	if tx, err = db.Sql.Begin(); err != nil {
		return formatError(err, "")
	}

	// Update all calls with their systemRef and talkgroupRef
	query = `UPDATE "calls" AS c SET "systemRef" = s."systemRef", "talkgroupRef" = t."talkgroupRef" FROM "systems" AS s, "talkgroups" AS t WHERE c."systemId" = s."systemId" AND c."talkgroupId" = t."talkgroupId" AND (c."systemRef" = 0 OR c."talkgroupRef" = 0)`

	if _, err = tx.Exec(query); err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	// Verify migration by checking a few rows
	query = `SELECT COUNT(*) FROM "calls" WHERE "systemRef" = 0 OR "talkgroupRef" = 0`
	if err = tx.QueryRow(query).Scan(&count); err == nil && count > 0 {
		log.Printf("WARNING: %d calls still have missing refs after migration", count)
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	log.Println("calls refs migration completed")
	return nil
}

func migrateDirwatches(db *Database) error {
	var (
		err   error
		query string
		rows  *sql.Rows
		tx    *sql.Tx

		systems    = map[int32]int32{}
		talkgroups = map[int32]map[int32]int32{}

		refSystem    any
		refTalkgroup any

		delay        sql.NullInt32
		deleteAfter  sql.NullBool
		directory    sql.NullString
		dirwatchId   sql.NullInt64
		disabled     sql.NullBool
		extension    sql.NullString
		frequency    sql.NullInt32
		kind         sql.NullString
		mask         sql.NullString
		order        sql.NullInt32
		systemId     sql.NullInt32
		systemRef    sql.NullInt32
		talkgroupId  sql.NullInt32
		talkgroupRef sql.NullInt32
	)

	formatError := errorFormatter("migration", "migrateDirwatches")

	if _, err = db.Sql.Exec(`SELECT COUNT(*) FROM "rdioScannerDirwatches"`); err != nil {
		return nil
	}

	log.Println("migrating dirwatches...")

	query = `SELECT s."systemId", s."systemRef", t."talkgroupId", t."talkgroupRef" FROM "systems" AS s LEFT JOIN "talkgroups" AS t`
	if rows, err = db.Sql.Query(query); err != nil {
		return formatError(err, query)
	}

	for rows.Next() {
		if err = rows.Scan(&systemId, &systemRef, &talkgroupId, &talkgroupRef); err != nil {
			continue
		}

		if systemId.Valid && systemRef.Valid && talkgroupId.Valid && talkgroupRef.Valid {
			if systems[systemRef.Int32] == 0 {
				systems[systemRef.Int32] = systemId.Int32
				talkgroups[systemRef.Int32] = map[int32]int32{}
			}

			talkgroups[systemRef.Int32][talkgroupRef.Int32] = talkgroupId.Int32
		}
	}

	rows.Close()

	if tx, err = db.Sql.Begin(); err != nil {
		return formatError(err, "")
	}

	query = `SELECT "_id", "delay", "deleteAfter", "directory", "disabled", "extension", "frequency", "mask", "order", "systemId", "talkgroupId", "type" FROM "rdioScannerDirwatches"`
	if rows, err = tx.Query(query); err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	for rows.Next() {
		dirwatch := NewDirwatch()

		if err = rows.Scan(&dirwatchId, &delay, &deleteAfter, &directory, &disabled, &extension, &frequency, &mask, &order, &systemRef, &talkgroupRef, &kind); err != nil {
			continue
		}

		if dirwatchId.Valid {
			dirwatch.Id = uint64(dirwatchId.Int64)
		} else {
			continue
		}

		if delay.Valid {
			dirwatch.Delay = uint(delay.Int32)
		}

		if deleteAfter.Valid {
			dirwatch.DeleteAfter = deleteAfter.Bool
		}

		if directory.Valid && len(directory.String) > 0 {
			dirwatch.Directory = escapeQuotes(directory.String)
		} else {
			continue
		}

		if disabled.Valid {
			dirwatch.Disabled = disabled.Bool
		}

		if extension.Valid {
			dirwatch.Extension = escapeQuotes(extension.String)
		}

		if frequency.Valid {
			dirwatch.Frequency = uint(frequency.Int32)
		}

		if mask.Valid && len(mask.String) > 0 {
			dirwatch.Mask = escapeQuotes(mask.String)
		}

		if kind.Valid && len(kind.String) > 0 {
			dirwatch.Kind = kind.String
		}

		if order.Valid {
			dirwatch.Order = uint(order.Int32)
		}

		if systemRef.Valid && systems[systemRef.Int32] > 0 {
			refSystem = systems[systemRef.Int32]
		} else {
			refSystem = nil
		}

		if talkgroupId.Valid {
			refTalkgroup = talkgroups[systemRef.Int32][talkgroupRef.Int32]
		} else {
			refTalkgroup = nil
		}

		query = fmt.Sprintf(`INSERT INTO "dirwatches" ("dirwatchId", "delay", "deleteAfter", "directory", "disabled", "extension", "frequency", "mask", "order", "systemId", "talkgroupId", "type") VALUES (%d, %d, %t, '%s', %t, '%s', %d, '%s', %d, %d, %d, '%s')`, dirwatch.Id, dirwatch.Delay, dirwatch.DeleteAfter, dirwatch.Directory, dirwatch.Disabled, dirwatch.Extension, dirwatch.Frequency, dirwatch.Mask, dirwatch.Order, refSystem, refTalkgroup, dirwatch.Kind)
		if _, err = tx.Exec(query); err != nil {
			log.Println(formatError(err, query))
		}
	}

	rows.Close()

	query = `DROP TABLE "RdioScannerDirWatches"`
	if _, err = tx.Exec(`DROP TABLE "RdioScannerDirWatches"`); err != nil {
		log.Println(formatError(err, query))
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	return nil
}

func migrateDownstreams(db *Database) error {
	var (
		err   error
		query string
		rows  *sql.Rows
		tx    *sql.Tx

		apikey       sql.NullString
		disabled     sql.NullBool
		downstreamId sql.NullInt64
		order        sql.NullInt32
		systems      sql.NullString
		url          sql.NullString
	)

	formatError := errorFormatter("migration", "migrateDownstreams")

	if _, err = db.Sql.Exec(`SELECT COUNT(*) FROM "rdioScannerDownstreams"`); err != nil {
		return nil
	}

	log.Println("migrating downstreams...")

	if tx, err = db.Sql.Begin(); err != nil {
		return formatError(err, "")
	}

	query = `SELECT "_id", "apiKey", "disabled", "order", "systems", "url" FROM "rdioScannerDownstreams"`
	if rows, err = tx.Query(query); err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	for rows.Next() {
		downstream := NewDownstream(nil)

		if err = rows.Scan(&downstreamId, &apikey, &disabled, &order, &systems, &url); err != nil {
			continue
		}

		if downstreamId.Valid {
			downstream.Id = uint64(downstreamId.Int64)
		} else {
			continue
		}

		if apikey.Valid && len(apikey.String) > 0 {
			downstream.Apikey = escapeQuotes(apikey.String)
		} else {
			continue
		}

		if disabled.Valid {
			downstream.Disabled = disabled.Bool
		}

		if order.Valid {
			downstream.Order = uint(order.Int32)
		}

		if systems.Valid && len(systems.String) > 0 {
			downstream.Systems = systems.String
		} else {
			continue
		}

		if url.Valid && len(url.String) > 0 {
			downstream.Url = escapeQuotes(url.String)
		} else {
			continue
		}

		query = fmt.Sprintf(`INSERT INTO "downstreams" ("downstreamId", "apikey", "disabled", "order", "systems", "url") VALUES (%d, '%s', %t, %d, '%s', '%s')`, downstream.Id, downstream.Apikey, downstream.Disabled, downstream.Order, downstream.Systems, downstream.Url)
		if _, err = tx.Exec(query); err != nil {
			log.Println(formatError(err, query))
		}
	}

	rows.Close()

	query = `DROP TABLE "rdioScannerDownstreams"`
	if _, err = tx.Exec(query); err != nil {
		log.Println(formatError(err, query))
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	return nil
}

func migrateGroups(db *Database) error {
	var (
		err   error
		query string
		rows  *sql.Rows
		tx    *sql.Tx

		groups  = []*Group{}
		groupId sql.NullInt32
		label   sql.NullString
	)

	formatError := errorFormatter("migration", "migrateGroups")

	if _, err = db.Sql.Exec(`SELECT COUNT(*) FROM "rdioScannerGroups"`); err != nil {
		return nil
	}

	log.Println("migrating groups...")

	if tx, err = db.Sql.Begin(); err != nil {
		return formatError(err, "")
	}

	query = `SELECT "_id", "label" FROM "rdioScannerGroups"`
	if rows, err = tx.Query(query); err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	for rows.Next() {
		group := NewGroup()

		if err = rows.Scan(&groupId, &label); err != nil {
			continue
		}

		if groupId.Valid {
			group.Id = uint64(groupId.Int32)
		} else {
			continue
		}

		if label.Valid {
			group.Label = escapeQuotes(label.String)
		}

		groups = append(groups, group)
	}

	rows.Close()

	sort.Slice(groups, func(i int, j int) bool {
		return groups[i].Label < groups[j].Label
	})

	for i, group := range groups {
		group.Order = uint(i + 1)

		query = fmt.Sprintf(`INSERT INTO "groups" ("groupId", "label", "order") VALUES (%d, '%s', %d)`, group.Id, group.Label, group.Order)
		if _, err = tx.Exec(query); err != nil {
			log.Println(formatError(err, query))
		}
	}

	query = `DROP TABLE "rdioScannerGroups"`
	if _, err = tx.Exec(query); err != nil {
		log.Println(formatError(err, query))
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	return nil
}

func migrateLogs(db *Database) error {
	var (
		err   error
		query string
		rows  *sql.Rows
		tx    *sql.Tx

		timestamp int64

		dateTime sql.NullTime
		level    sql.NullString
		logId    sql.NullInt32
		message  sql.NullString
	)

	formatError := errorFormatter("migration", "migrateLogs")

	if _, err = db.Sql.Exec(`SELECT COUNT(*) FROM "rdioScannerLogs"`); err != nil {
		return nil
	}

	log.Println("migrating logs...")

	if tx, err = db.Sql.Begin(); err != nil {
		return formatError(err, "")
	}

	query = `SELECT "_id", "dateTime", "level", "message" FROM "rdioScannerLogs"`
	if rows, err = tx.Query(query); err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	for rows.Next() {
		l := NewLog()

		if err = rows.Scan(&logId, &dateTime, &level, &message); err != nil {
			continue
		}

		if logId.Valid {
			l.Id = uint(logId.Int32)
		} else {
			continue
		}

		if dateTime.Valid {
			timestamp = dateTime.Time.UnixMilli()
		} else {
			continue
		}

		if level.Valid && len(level.String) > 0 {
			l.Level = level.String
		} else {
			continue
		}

		if message.Valid && len(message.String) > 0 {
			l.Message = escapeQuotes(message.String)
		} else {
			continue
		}

		query = fmt.Sprintf(`INSERT INTO "logs" ("logId", "level", "message", "timestamp") VALUES (%d, '%s', '%s', %d)`, l.Id, l.Level, l.Message, timestamp)
		if _, err = tx.Exec(query); err != nil {
			log.Println(formatError(err, query))
		}
	}

	rows.Close()

	query = `DROP TABLE "rdioScannerLogs"`
	if _, err = tx.Exec(query); err != nil {
		log.Println(formatError(err, query))
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	return nil
}

func migrateMeta(db *Database) error {
	// Prepare migration table (v6 style) - don't drop it, we need it for tracking migrations
	_, err := prepareMigration(db)
	return err
}

// prepareMigration prepares the rdioScannerMeta table for tracking migrations (v6 style)
func prepareMigration(db *Database) (bool, error) {
	var (
		err     error
		verbose bool = true
		query   string
	)

	query = `SELECT COUNT(*) FROM "rdioScannerMeta"`

	if _, err = db.Sql.Exec(query); err != nil {
		// Table doesn't exist, check for SequelizeMeta (legacy)
		query = `SELECT COUNT(*) FROM "SequelizeMeta"`
		if _, err = db.Sql.Exec(query); err == nil {
			log.Println("Preparing for database migration")
			query = `ALTER TABLE "SequelizeMeta" RENAME TO "rdioScannerMeta"`
			_, err = db.Sql.Exec(query)
		} else {
			verbose = false
			// Create the table
			query = `CREATE TABLE IF NOT EXISTS "rdioScannerMeta" ("name" text NOT NULL UNIQUE PRIMARY KEY)`
			_, err = db.Sql.Exec(query)
		}
	}

	return verbose, err
}

// migrateWithSchema runs a migration with schema changes, tracking it in rdioScannerMeta (v6 style)
func (db *Database) migrateWithSchema(name string, schemas []string, verbose bool) error {
	var (
		count int = 0
		err   error
		query string
		tx    *sql.Tx
	)

	formatError := func(err error, query string) error {
		return fmt.Errorf("%s while doing %s", err.Error(), query)
	}

	query = fmt.Sprintf(`SELECT COUNT(*) FROM "rdioScannerMeta" WHERE "name" = '%s'`, escapeQuotes(name))

	if err = db.Sql.QueryRow(query).Scan(&count); err != nil {
		return formatError(err, query)
	}

	if count == 0 {
		if verbose {
			log.Printf("running database migration %s", name)
		}

		if tx, err = db.Sql.Begin(); err == nil {
			for _, query = range schemas {
				if _, err = tx.Exec(query); err != nil {
					tx.Rollback()
					return formatError(err, query)
				}
			}

			query = fmt.Sprintf(`INSERT INTO "rdioScannerMeta" ("name") VALUES ('%s')`, escapeQuotes(name))

			if _, err = tx.Exec(query); err != nil {
				tx.Rollback()
				return formatError(err, query)
			}

			if err = tx.Commit(); err != nil {
				tx.Rollback()
				return err
			}
		}
	}

	return nil
}

func migrateOptions(db *Database) error {
	var (
		err   error
		query string
		rows  *sql.Rows
		tx    *sql.Tx

		key   sql.NullString
		value sql.NullString
	)

	formatError := errorFormatter("migration", "migrateOptions")

	if _, err = db.Sql.Exec(`SELECT COUNT(*) FROM "rdioScannerConfigs"`); err != nil {
		return nil
	}

	log.Println("migrating options...")

	if tx, err = db.Sql.Begin(); err != nil {
		return formatError(err, "")
	}

	query = `SELECT "key", "val" FROM "rdioScannerConfigs"`
	if rows, err = tx.Query(query); err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	for rows.Next() {
		if err = rows.Scan(&key, &value); err != nil {
			continue
		}

		if !key.Valid || !value.Valid {
			continue
		}

		if key.String == "options" {
			var m map[string]any

			if err = json.Unmarshal([]byte(value.String), &m); err == nil {
				switch v := m["audioConversion"].(type) {
				case bool:
					if b, err := json.Marshal(v); err == nil {
						query = fmt.Sprintf(`INSERT INTO "options" ("key", "value") VALUES ('%s', '%s')`, "audioConversion", string(b))
						if _, err = tx.Exec(query); err != nil {
							log.Println(formatError(err, query))
						}
					}
				}
				switch v := m["autoPopulate"].(type) {
				case bool:
					if b, err := json.Marshal(v); err == nil {
						query = fmt.Sprintf(`INSERT INTO "options" ("key", "value") VALUES ('%s', '%s')`, "autoPopulate", string(b))
						if _, err = tx.Exec(query); err != nil {
							log.Println(formatError(err, query))
						}
					}
				}
				switch v := m["branding"].(type) {
				case string:
					if b, err := json.Marshal(v); err == nil {
						query = fmt.Sprintf(`INSERT INTO "options" ("key", "value") VALUES ('%s', '%s')`, "branding", escapeQuotes(string(b)))
						if _, err = tx.Exec(query); err != nil {
							log.Println(formatError(err, query))
						}
					}
				}
				switch v := m["dimmerDelay"].(type) {
				case float64:
					if b, err := json.Marshal(v); err == nil {
						query = fmt.Sprintf(`INSERT INTO "options" ("key", "value") VALUES ('%s', '%s')`, "dimmerDelay", string(b))
						if _, err = tx.Exec(query); err != nil {
							log.Println(formatError(err, query))
						}
					}
				}
				switch v := m["disableDuplicateDetection"].(type) {
				case bool:
					if b, err := json.Marshal(v); err == nil {
						query = fmt.Sprintf(`INSERT INTO "options" ("key", "value") VALUES ('%s', '%s')`, "disableDuplicateDetection", string(b))
						if _, err = tx.Exec(query); err != nil {
							log.Println(formatError(err, query))
						}
					}
				}
				switch v := m["duplicateDetectionTimeFrame"].(type) {
				case float64:
					if b, err := json.Marshal(v); err == nil {
						query = fmt.Sprintf(`INSERT INTO "options" ("key", "value") VALUES ('%s', '%s')`, "duplicateDetectionTimeFrame", string(b))
						if _, err = tx.Exec(query); err != nil {
							log.Println(formatError(err, query))
						}
					}
				}
				switch v := m["email"].(type) {
				case string:
					if b, err := json.Marshal(v); err == nil {
						query = fmt.Sprintf(`INSERT INTO "options" ("key", "value") VALUES ('%s', '%s')`, "email", escapeQuotes(string(b)))
						if _, err = tx.Exec(query); err != nil {
							log.Println(formatError(err, query))
						}
					}
				}
				switch v := m["keypadBeeps"].(type) {
				case string:
					if b, err := json.Marshal(v); err == nil {
						query = fmt.Sprintf(`INSERT INTO "options" ("key", "value") VALUES ('%s', '%s')`, "keypadBeeps", string(b))
						if _, err = tx.Exec(query); err != nil {
							log.Println(formatError(err, query))
						}
					}
				}
				switch v := m["maxClients"].(type) {
				case float64:
					if b, err := json.Marshal(v); err == nil {
						query = fmt.Sprintf(`INSERT INTO "options" ("key", "value") VALUES ('%s', '%s')`, "maxClients", string(b))
						if _, err = tx.Exec(query); err != nil {
							log.Println(formatError(err, query))
						}
					}
				}
				switch v := m["playbackGoesLive"].(type) {
				case bool:
					if b, err := json.Marshal(v); err == nil {
						query = fmt.Sprintf(`INSERT INTO "options" ("key", "value") VALUES ('%s', '%s')`, "playbackGoesLive", string(b))
						if _, err = tx.Exec(query); err != nil {
							log.Println(formatError(err, query))
						}
					}
				}
				switch v := m["pruneDays"].(type) {
				case float64:
					if b, err := json.Marshal(v); err == nil {
						query = fmt.Sprintf(`INSERT INTO "options" ("key", "value") VALUES ('%s', '%s')`, "pruneDays", string(b))
						if _, err = tx.Exec(query); err != nil {
							log.Println(formatError(err, query))
						}
					}
				}
				switch v := m["showListenersCount"].(type) {
				case bool:
					if b, err := json.Marshal(v); err == nil {
						query = fmt.Sprintf(`INSERT INTO "options" ("key", "value") VALUES ('%s', '%s')`, "showListenersCount", string(b))
						if _, err = tx.Exec(query); err != nil {
							log.Println(formatError(err, query))
						}
					}
				}
				switch v := m["sortTalkgroups"].(type) {
				case bool:
					if b, err := json.Marshal(v); err == nil {
						query = fmt.Sprintf(`INSERT INTO "options" ("key", "value") VALUES ('%s', '%s')`, "sortTalkgroups", string(b))
						if _, err = tx.Exec(query); err != nil {
							log.Println(formatError(err, query))
						}
					}
				}
				switch v := m["time12hFormat"].(type) {
				case bool:
					if b, err := json.Marshal(v); err == nil {
						query = fmt.Sprintf(`INSERT INTO "options" ("key", "value") VALUES ('%s', '%s')`, "time12hFormat", string(b))
						if _, err = tx.Exec(query); err != nil {
							log.Println(formatError(err, query))
						}
					}
				}
			}

		} else {
			query = fmt.Sprintf(`INSERT INTO "options" ("key", "value") VALUES ('%s', '%s')`, escapeQuotes(key.String), escapeQuotes(value.String))
			if _, err = tx.Exec(query); err != nil {
				log.Println(formatError(err, query))
			}
		}
	}

	rows.Close()

	query = `DROP TABLE "rdioScannerConfigs"`
	if _, err = tx.Exec(query); err != nil {
		log.Println(formatError(err, query))
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	return nil
}

func migrateSystems(db *Database) error {
	var (
		err   error
		query string
		rows  *sql.Rows
		tx    *sql.Tx

		autoPopulate sql.NullBool
		blacklists   sql.NullString
		label        sql.NullString
		order        sql.NullInt32
		systemId     sql.NullInt64
		systemRef    sql.NullInt32
	)

	formatError := errorFormatter("migration", "migrateSystems")

	if _, err = db.Sql.Exec(`SELECT COUNT(*) FROM "rdioScannerSystems"`); err != nil {
		return nil
	}

	log.Println("migrating systems...")

	if tx, err = db.Sql.Begin(); err != nil {
		return formatError(err, "")
	}

	query = `SELECT "_id", "autoPopulate", "blacklists", "id", "label", "order" FROM "rdioScannerSystems"`
	if rows, err = tx.Query(query); err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	for rows.Next() {
		system := NewSystem()

		if err = rows.Scan(&systemId, &autoPopulate, &blacklists, &systemRef, &label, &order); err != nil {
			continue
		}

		if systemId.Valid {
			system.Id = uint64(systemId.Int64)
		} else {
			continue
		}

		if autoPopulate.Valid {
			system.AutoPopulate = autoPopulate.Bool
		}

		if blacklists.Valid {
			system.Blacklists = Blacklists(strings.ReplaceAll(strings.ReplaceAll(blacklists.String, "[", ""), "]", ""))
		}

		if label.Valid {
			system.Label = escapeQuotes(label.String)
		}

		if order.Valid {
			system.Order = uint(order.Int32)
		}

		if systemRef.Valid {
			system.SystemRef = uint(systemRef.Int32)
		}

		query = fmt.Sprintf(`INSERT INTO "systems" ("systemId", "autoPopulate", "blacklists", "label", "order", "systemRef") VALUES (%d, %t, '%s', '%s', %d, %d)`, system.Id, system.AutoPopulate, system.Blacklists, system.Label, system.Order, system.SystemRef)
		if _, err = tx.Exec(query); err != nil {
			log.Println(formatError(err, query))
		}
	}

	rows.Close()

	query = `DROP TABLE "rdioScannerSystems"`
	if _, err = tx.Exec(query); err != nil {
		log.Println(formatError(err, query))
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	return nil
}

func migrateTags(db *Database) error {
	var (
		err   error
		query string
		rows  *sql.Rows
		tx    *sql.Tx

		label sql.NullString
		tags  = []*Tag{}
		tagId sql.NullInt32
	)

	formatError := errorFormatter("migration", "migrateTags")

	if _, err = db.Sql.Exec(`SELECT COUNT(*) FROM "rdioScannerTags"`); err != nil {
		return nil
	}

	log.Println("migrating tags...")

	if tx, err = db.Sql.Begin(); err != nil {
		return formatError(err, "")
	}

	query = `SELECT "_id", "label" FROM "rdioScannerTags"`
	if rows, err = tx.Query(query); err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	for rows.Next() {
		tag := NewTag()

		if err = rows.Scan(&tagId, &label); err != nil {
			continue
		}

		if tagId.Valid {
			tag.Id = uint64(tagId.Int32)
		} else {
			continue
		}

		if label.Valid {
			tag.Label = escapeQuotes(label.String)
		}

		tags = append(tags, tag)
	}

	rows.Close()

	sort.Slice(tags, func(i int, j int) bool {
		return tags[i].Label < tags[j].Label
	})

	for i, tag := range tags {
		tag.Order = uint(i + 1)

		query = fmt.Sprintf(`INSERT INTO "tags" ("tagId", "label", "order") VALUES (%d, '%s', %d)`, tag.Id, tag.Label, tag.Order)
		if _, err = tx.Exec(query); err != nil {
			log.Println(formatError(err, query))
		}
	}

	query = `DROP TABLE "rdioScannerTags"`
	if _, err = tx.Exec(query); err != nil {
		log.Println(formatError(err, query))
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	return nil
}

func migrateTalkgroups(db *Database) error {
	var (
		err   error
		query string
		rows  *sql.Rows
		tx    *sql.Tx

		systems = map[int64]int64{}

		frequency    sql.NullInt32
		groupId      sql.NullInt64
		label        sql.NullString
		name         sql.NullString
		order        sql.NullInt32
		systemId     sql.NullInt64
		tagId        sql.NullInt64
		talkgroupId  sql.NullInt64
		talkgroupRef sql.NullInt32
	)

	formatError := errorFormatter("migration", "migrateTalkgroups")

	if _, err = db.Sql.Exec(`SELECT COUNT(*) FROM "rdioScannerTalkgroups"`); err != nil {
		return nil
	}

	log.Println("migrating talkgroups...")

	query = `SELECT "systemId", "systemRef" FROM "systems"`
	if rows, err = db.Sql.Query(query); err != nil {
		return formatError(err, query)
	}

	for rows.Next() {
		if err = rows.Scan(&systemId, &talkgroupRef); err != nil {
			continue
		}

		if systemId.Valid && talkgroupRef.Valid {
			systems[int64(talkgroupRef.Int32)] = systemId.Int64
		}
	}

	rows.Close()

	if tx, err = db.Sql.Begin(); err != nil {
		return formatError(err, "")
	}

	query = `SELECT "_id", "frequency", "groupId", "id", "label", "name", "order", "systemId", "tagId" FROM "rdioScannerTalkgroups"`
	if rows, err = tx.Query(query); err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	for rows.Next() {
		talkgroup := NewTalkgroup()

		if err = rows.Scan(&talkgroupId, &frequency, &groupId, &talkgroupRef, &label, &name, &order, &systemId, &tagId); err != nil {
			continue
		}

		if talkgroupId.Valid {
			talkgroup.Id = uint64(talkgroupId.Int64)
		} else {
			continue
		}

		if frequency.Valid {
			talkgroup.Frequency = uint(frequency.Int32)
		}

		if groupId.Valid {
			talkgroup.GroupIds = []uint64{uint64(groupId.Int64)}
		}

		if label.Valid {
			talkgroup.Label = escapeQuotes(label.String)
		}

		if name.Valid {
			talkgroup.Name = escapeQuotes(name.String)
		}

		if order.Valid {
			talkgroup.Order = uint(order.Int32)
		}

		if !systemId.Valid || systems[systemId.Int64] == 0 {
			continue
		}

		if talkgroupRef.Valid {
			talkgroup.TalkgroupRef = uint(talkgroupRef.Int32)
		}

		if tagId.Valid {
			talkgroup.TagId = uint64(tagId.Int64)
		}

		query = fmt.Sprintf(`INSERT INTO "talkgroups" ("talkgroupId", "frequency", "label", "name", "order", "systemId", "tagId", "talkgroupRef") VALUES (%d, %d, '%s', '%s', %d, %d, %d, %d)`, talkgroup.Id, talkgroup.Frequency, talkgroup.Label, talkgroup.Name, talkgroup.Order, systems[systemId.Int64], talkgroup.TagId, talkgroup.TalkgroupRef)
		if _, err = tx.Exec(query); err == nil {
			query = fmt.Sprintf(`INSERT INTO "talkgroupGroups" ("groupId", "talkgroupId") VALUES (%d, %d)`, talkgroup.GroupIds[0], talkgroup.Id)
			if _, err = tx.Exec(query); err != nil {
				log.Println(formatError(err, query))
			}

		} else {
			log.Println(formatError(err, query))
		}
	}

	rows.Close()

	query = `DROP TABLE "rdioScannerTalkgroups"`
	if _, err = tx.Exec(query); err != nil {
		log.Println(formatError(err, query))
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	return nil
}

func migrateUnits(db *Database) error {
	var (
		err   error
		query string
		rows  *sql.Rows
		tx    *sql.Tx

		systems = map[int32]int32{}

		label    sql.NullString
		order    sql.NullInt32
		systemId sql.NullInt32
		unitId   sql.NullInt64
		unitRef  sql.NullInt32
	)

	formatError := errorFormatter("migration", "migrateUnits")

	if _, err = db.Sql.Exec(`SELECT COUNT(*) FROM "rdioScannerUnits"`); err != nil {
		return nil
	}

	log.Println("migrating units...")

	query = `SELECT "systemId", "systemRef" FROM "systems"`
	if rows, err = db.Sql.Query(query); err != nil {
		return formatError(err, query)
	}

	for rows.Next() {
		if err = rows.Scan(&systemId, &unitRef); err != nil {
			continue
		}

		if systemId.Valid && unitRef.Valid {
			systems[unitRef.Int32] = systemId.Int32
		}
	}

	rows.Close()

	if tx, err = db.Sql.Begin(); err != nil {
		return formatError(err, "")
	}

	query = `SELECT "_id", "id", "label", "order", "systemId" FROM "rdioScannerUnits"`
	if rows, err = tx.Query(query); err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	for rows.Next() {
		unit := NewUnit()

		if err = rows.Scan(&unitId, &unitRef, &label, &order, &systemId); err != nil {
			continue
		}

		if !unitId.Valid {
			continue
		}

		if !systemId.Valid || systems[systemId.Int32] == 0 {
			continue
		}

		if label.Valid {
			unit.Label = escapeQuotes(label.String)
		}

		if order.Valid {
			unit.Order = uint(order.Int32)
		}

		if unitRef.Valid {
			unit.UnitRef = uint(unitRef.Int32)
		}

		query = fmt.Sprintf(`INSERT INTO "units" ("unitId", "label", "order", "systemId", "unitRef") VALUES (%d, '%s', %d, %d, %d)`, unitId.Int64, unit.Label, unit.Order, systems[systemId.Int32], unit.Id)
		if _, err = tx.Exec(query); err != nil {
			log.Println(formatError(err, query))
		}
	}

	rows.Close()

	query = `DROP TABLE "rdioScannerUnits"`
	if _, err = tx.Exec(query); err != nil {
		log.Println(formatError(err, query))
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	return nil
}

func migrateUsers(db *Database) error {
	// Check if users table already exists
	var count int
	err := db.Sql.QueryRow(`SELECT COUNT(*) FROM "users"`).Scan(&count)
	if err != nil {
		// Table doesn't exist, will be created by schema migration
		return nil
	}

	// Check if new columns exist and add them if they don't
	columns := []struct {
		name       string
		definition string
	}{
		{"firstName", `text NOT NULL DEFAULT ''`},
		{"lastName", `text NOT NULL DEFAULT ''`},
		{"zipCode", `text NOT NULL DEFAULT ''`},
		{"settings", `text NOT NULL DEFAULT ''`},
		{"userGroupId", `bigint NOT NULL DEFAULT 0`},
		{"isGroupAdmin", `boolean NOT NULL DEFAULT false`},
		{"resetCode", `text DEFAULT ''`},
		{"resetCodeExpires", `bigint DEFAULT 0`},
	}

	changesMade := false
	for _, col := range columns {
		// Check if column exists using information_schema
		var exists bool
		var err error

		err = db.Sql.QueryRow(`SELECT EXISTS (
			SELECT 1 FROM information_schema.columns 
			WHERE table_name = 'users' 
				AND table_schema = current_schema()
				AND column_name = $1
		)`, col.name).Scan(&exists)

		if err != nil {
			log.Printf("Warning: Could not check if column %s exists: %v", col.name, err)
			continue
		}

		if !exists {
			if !changesMade {
				log.Println("migrating users table...")
				changesMade = true
			}
			log.Printf("Adding column %s to users table...", col.name)
			alterQuery := fmt.Sprintf(`ALTER TABLE "users" ADD COLUMN "%s" %s`, col.name, col.definition)
			_, err := db.Sql.Exec(alterQuery)
			if err != nil {
				// Check if error is "column already exists" - this is OK
				if strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "duplicate column") {
					// Column was added between check and ALTER, which is fine
				} else {
					log.Printf("Error adding column %s: %v", col.name, err)
					return fmt.Errorf("failed to add column %s: %v", col.name, err)
				}
			} else {
				log.Printf("Successfully added column %s", col.name)
			}
		}
	}

	return nil
}

func migrateUserPins(db *Database) error {
	// Ensure users table exists
	if _, err := db.Sql.Exec(`SELECT 1 FROM "users" LIMIT 1`); err != nil {
		return nil
	}

	addColumnQueries := []string{
		`ALTER TABLE "users" ADD COLUMN IF NOT EXISTS "pin" TEXT DEFAULT ''`,
		`ALTER TABLE "users" ADD COLUMN IF NOT EXISTS "pinExpiresAt" BIGINT DEFAULT 0`,
		`ALTER TABLE "users" ADD COLUMN IF NOT EXISTS "connectionLimit" INTEGER DEFAULT 0`,
	}

	for _, query := range addColumnQueries {
		if _, err := db.Sql.Exec(query); err != nil {
			errStr := strings.ToLower(err.Error())
			if !strings.Contains(errStr, "duplicate") && !strings.Contains(errStr, "exists") {
				log.Printf("DEBUG: Unable to execute migration query (%s): %v", query, err)
			}
		}
	}

	// Ensure connectionLimit defaults to 0 where NULL
	if _, err := db.Sql.Exec(`UPDATE "users" SET "connectionLimit" = 0 WHERE "connectionLimit" IS NULL`); err != nil {
		log.Printf("DEBUG: Unable to normalise connectionLimit column: %v", err)
	}
	if _, err := db.Sql.Exec(`UPDATE "users" SET "pinExpiresAt" = 0 WHERE "pinExpiresAt" IS NULL`); err != nil {
		log.Printf("DEBUG: Unable to normalise pinExpiresAt column: %v", err)
	}

	// Attempt to create unique index on pin column
	indexQuery := `CREATE UNIQUE INDEX IF NOT EXISTS "users_pin_idx" ON "users" ("pin")`
	if _, err := db.Sql.Exec(indexQuery); err != nil {
		errStr := strings.ToLower(err.Error())
		if !strings.Contains(errStr, "exists") && !strings.Contains(errStr, "duplicate") {
			log.Printf("DEBUG: Unable to create users_pin_idx: %v", err)
		}
	}

	// Drop legacy accesses table if it still exists
	if _, err := db.Sql.Exec(`DROP TABLE IF EXISTS "accesses"`); err != nil {
		log.Printf("DEBUG: Unable to drop legacy accesses table: %v", err)
	}

	// Load existing pins to make sure we don't duplicate values
	existingPins := map[string]struct{}{}
	rows, err := db.Sql.Query(`SELECT "pin" FROM "users" WHERE "pin" IS NOT NULL AND "pin" <> ''`)
	if err == nil {
		for rows.Next() {
			var pin sql.NullString
			if err := rows.Scan(&pin); err == nil && pin.Valid {
				existingPins[pin.String] = struct{}{}
			}
		}
		rows.Close()
	}

	rows, err = db.Sql.Query(`SELECT "userId", "pin" FROM "users"`)
	if err != nil {
		return fmt.Errorf("migrateUserPins select users: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			userId uint64
			pin    sql.NullString
		)
		if err := rows.Scan(&userId, &pin); err != nil {
			continue
		}

		if pin.Valid && pin.String != "" {
			existingPins[pin.String] = struct{}{}
			continue
		}

		newPin, genErr := generateUserPin()
		if genErr != nil {
			return fmt.Errorf("unable to generate user pin: %w", genErr)
		}
		for {
			if _, ok := existingPins[newPin]; !ok {
				break
			}
			newPin, genErr = generateUserPin()
			if genErr != nil {
				return fmt.Errorf("unable to generate unique user pin: %w", genErr)
			}
		}

		existingPins[newPin] = struct{}{}
		updateQuery := fmt.Sprintf(`UPDATE "users" SET "pin" = '%s' WHERE "userId" = %d`, escapeQuotes(newPin), userId)
		if _, err := db.Sql.Exec(updateQuery); err != nil {
			log.Printf("DEBUG: Unable to update user %d with generated pin: %v", userId, err)
		}
	}

	return nil
}

// migrateAlerts adds alerts table and related columns
func migrateAlerts(db *Database) error {
	// Add toneSetId column if it doesn't exist
	query := `ALTER TABLE "alerts" ADD COLUMN IF NOT EXISTS "toneSetId" text NOT NULL DEFAULT ''`
	if _, err := db.Sql.Exec(query); err != nil {
		log.Printf("migration note: %v", err)
	}

	// Create index on createdAt if it doesn't exist
	query = `CREATE INDEX IF NOT EXISTS "alerts_created_idx" ON "alerts" ("createdAt")`
	if _, err := db.Sql.Exec(query); err != nil {
		log.Printf("migration note: %v", err)
	}
	return nil
}

// migrateAlertPreferences adds userAlertPreferences table and related columns
func migrateAlertPreferences(db *Database) error {

	query := `ALTER TABLE "userAlertPreferences" ADD COLUMN IF NOT EXISTS "toneSetIds" text NOT NULL DEFAULT '[]'`
	if _, err := db.Sql.Exec(query); err != nil {
		log.Printf("migration note: %v", err)
	}
	return nil
}

// migrateToneDetection adds tone detection columns to talkgroups and calls tables
func migrateToneDetection(db *Database) error {

	// Add columns to talkgroups if they don't exist
	queries := []string{
		`ALTER TABLE "talkgroups" ADD COLUMN IF NOT EXISTS "toneDetectionEnabled" boolean NOT NULL DEFAULT false`,
		`ALTER TABLE "talkgroups" ADD COLUMN IF NOT EXISTS "toneSets" text NOT NULL DEFAULT '[]'`,
	}
	for _, query := range queries {
		if _, err := db.Sql.Exec(query); err != nil {
			// Column might already exist, that's okay
			log.Printf("migration note: %v", err)
		}
	}

	// Add columns to calls if they don't exist
	queries = []string{
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "toneSequence" text NOT NULL DEFAULT '{}'`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "hasTones" boolean NOT NULL DEFAULT false`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "transcript" text NOT NULL DEFAULT ''`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "transcriptConfidence" real NOT NULL DEFAULT 0.0`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "transcriptionStatus" text NOT NULL DEFAULT 'pending'`,
		`ALTER TABLE "calls" ADD COLUMN IF NOT EXISTS "transcriptionFailureReason" text NOT NULL DEFAULT ''`,
	}
	for _, query := range queries {
		if _, err := db.Sql.Exec(query); err != nil {
			// Column might already exist, that's okay
			log.Printf("migration note: %v", err)
		}
	}

	return nil
}

// migrateUserGroupsMaxUsers adds maxUsers column to userGroups table
func migrateUserGroupsMaxUsers(db *Database) error {
	query := `ALTER TABLE "userGroups" ADD COLUMN IF NOT EXISTS "maxUsers" integer NOT NULL DEFAULT 0`
	if _, err := db.Sql.Exec(query); err != nil {
		log.Printf("migration note: %v", err)
	}
	return nil
}

// migrateSystemAdmins adds systemAdmin column to users table and creates systemAlerts table
func migrateSystemAdmins(db *Database) error {
	// Add systemAdmin column to users table
	query := `ALTER TABLE "users" ADD COLUMN IF NOT EXISTS "systemAdmin" boolean NOT NULL DEFAULT false`
	if _, err := db.Sql.Exec(query); err != nil {
		log.Printf("migration note (add systemAdmin): %v", err)
	}

	// Create systemAlerts table
	query = `CREATE TABLE IF NOT EXISTS "systemAlerts" (
		"alertId" bigserial NOT NULL PRIMARY KEY,
		"alertType" text NOT NULL,
		"severity" text NOT NULL DEFAULT 'info',
		"title" text NOT NULL,
		"message" text NOT NULL,
		"data" text NOT NULL DEFAULT '{}',
		"createdAt" bigint NOT NULL,
		"createdBy" bigint,
		"dismissed" boolean NOT NULL DEFAULT false,
		CONSTRAINT "systemAlerts_createdBy_fkey" FOREIGN KEY ("createdBy") REFERENCES "users" ("userId") ON DELETE SET NULL
	)`
	if _, err := db.Sql.Exec(query); err != nil {
		log.Printf("migration note (create systemAlerts): %v", err)
	}

	// Create index for quick lookups
	query = `CREATE INDEX IF NOT EXISTS "systemAlerts_createdAt_idx" ON "systemAlerts" ("createdAt" DESC)`
	if _, err := db.Sql.Exec(query); err != nil {
		log.Printf("migration note (create index): %v", err)
	}

	return nil
}

// migrateRegistrationCodesCreatedBy makes createdBy nullable to allow system admin-created codes
func migrateRegistrationCodesCreatedBy(db *Database) error {
	// First drop the foreign key constraint
	query := `ALTER TABLE "registrationCodes" DROP CONSTRAINT IF EXISTS "registrationCodes_createdBy_fkey"`
	if _, err := db.Sql.Exec(query); err != nil {
		log.Printf("migration note (dropping constraint): %v", err)
	}

	// Make column nullable
	query = `ALTER TABLE "registrationCodes" ALTER COLUMN "createdBy" DROP NOT NULL`
	if _, err := db.Sql.Exec(query); err != nil {
		log.Printf("migration note (making nullable): %v", err)
	}

	// Re-add foreign key constraint with ON DELETE SET NULL
	query = `ALTER TABLE "registrationCodes" ADD CONSTRAINT "registrationCodes_createdBy_fkey" 
	         FOREIGN KEY ("createdBy") REFERENCES "users" ("userId") ON DELETE SET NULL ON UPDATE CASCADE`
	if _, err := db.Sql.Exec(query); err != nil {
		// Constraint might already exist, that's okay
		if !strings.Contains(err.Error(), "already exists") {
			log.Printf("migration note (adding constraint): %v", err)
		}
	}
	return nil
}

// migrateUserInvitationsInvitedBy makes invitedBy nullable to allow system admin-created invitations
func migrateUserInvitationsInvitedBy(db *Database) error {
	// First drop the foreign key constraint
	query := `ALTER TABLE "userInvitations" DROP CONSTRAINT IF EXISTS "userInvitations_invitedBy_fkey"`
	if _, err := db.Sql.Exec(query); err != nil {
		log.Printf("migration note (dropping constraint): %v", err)
	}

	// Make column nullable
	query = `ALTER TABLE "userInvitations" ALTER COLUMN "invitedBy" DROP NOT NULL`
	if _, err := db.Sql.Exec(query); err != nil {
		log.Printf("migration note (making nullable): %v", err)
	}

	// Re-add foreign key constraint with ON DELETE SET NULL
	query = `ALTER TABLE "userInvitations" ADD CONSTRAINT "userInvitations_invitedBy_fkey" 
	         FOREIGN KEY ("invitedBy") REFERENCES "users" ("userId") ON DELETE SET NULL ON UPDATE CASCADE`
	if _, err := db.Sql.Exec(query); err != nil {
		// Constraint might already exist, that's okay
		if !strings.Contains(err.Error(), "already exists") {
			log.Printf("migration note (adding constraint): %v", err)
		}
	}

	return nil
}

// migrateUserGroupsAllowAddExistingUsers adds allowAddExistingUsers column to userGroups table
func migrateUserGroupsAllowAddExistingUsers(db *Database) error {
	query := `ALTER TABLE "userGroups" ADD COLUMN IF NOT EXISTS "allowAddExistingUsers" boolean NOT NULL DEFAULT false`
	if _, err := db.Sql.Exec(query); err != nil {
		log.Printf("migration note: %v", err)
	}
	return nil
}

// migrateUserGroupsBillingFields adds stripePriceId and billingMode columns to userGroups table
func migrateUserGroupsBillingFields(db *Database) error {
	queries := []string{
		`ALTER TABLE "userGroups" ADD COLUMN IF NOT EXISTS "stripePriceId" text NOT NULL DEFAULT ''`,
		`ALTER TABLE "userGroups" ADD COLUMN IF NOT EXISTS "billingMode" text NOT NULL DEFAULT 'all_users'`,
	}
	for _, query := range queries {
		if _, err := db.Sql.Exec(query); err != nil {
			log.Printf("migration note: %v", err)
		}
	}
	return nil
}

// migrateUserAccountExpiresAt adds accountExpiresAt column to users table
func migrateUserAccountExpiresAt(db *Database) error {
	query := `ALTER TABLE "users" ADD COLUMN IF NOT EXISTS "accountExpiresAt" bigint NOT NULL DEFAULT 0`
	if _, err := db.Sql.Exec(query); err != nil {
		log.Printf("migration note: %v", err)
	}
	return nil
}

// migrateUserGroupsPricingOptions adds pricingOptions column to userGroups table
func migrateUserGroupsPricingOptions(db *Database) error {
	query := `ALTER TABLE "userGroups" ADD COLUMN IF NOT EXISTS "pricingOptions" text NOT NULL DEFAULT ''`
	if _, err := db.Sql.Exec(query); err != nil {
		log.Printf("migration note: %v", err)
	}
	return nil
}

// migrateUserGroupsCollectSalesTax adds collectSalesTax column to userGroups table
func migrateUserGroupsCollectSalesTax(db *Database) error {
	query := `ALTER TABLE "userGroups" ADD COLUMN IF NOT EXISTS "collectSalesTax" boolean NOT NULL DEFAULT false`
	if _, err := db.Sql.Exec(query); err != nil {
		log.Printf("migration note: %v", err)
	}
	return nil
}

// migrateTransferRequestsApprovalTokens adds approval token columns to transferRequests table
func migrateTransferRequestsApprovalTokens(db *Database) error {
	// Check if transferRequests table exists
	if _, err := db.Sql.Exec(`SELECT 1 FROM "transferRequests" LIMIT 1`); err != nil {
		return nil // Table doesn't exist yet, schema will create it with columns
	}

	queries := []string{
		`ALTER TABLE "transferRequests" ADD COLUMN IF NOT EXISTS "approvalToken" text NOT NULL DEFAULT ''`,
		`ALTER TABLE "transferRequests" ADD COLUMN IF NOT EXISTS "approvalTokenExpiresAt" bigint NOT NULL DEFAULT 0`,
		`ALTER TABLE "transferRequests" ADD COLUMN IF NOT EXISTS "approvalTokenUsed" boolean NOT NULL DEFAULT false`,
	}
	for _, query := range queries {
		if _, err := db.Sql.Exec(query); err != nil {
			log.Printf("migration note: %v", err)
		}
	}
	return nil
}

// migrateCallsPerformanceIndexes adds performance indexes for system-only and system+talkgroup queries ordered by timestamp
// This matches the v6 migration20250101000000 optimization, using v6's migration system
func migrateCallsPerformanceIndexes(db *Database) error {
	var queries []string
	verbose := true // Migration table is already prepared in migrate()

	// Add indexes to optimize query performance for different filter patterns
	// This migration adds indexes to speed up queries when filtering by system and/or talkgroup
	//
	// Existing indexes:
	// - (systemId, siteRef, talkgroupId, timestamp) - good for queries with siteRef
	// - (systemRef, talkgroupRef, timestamp) - good for ref-based queries
	//
	// New indexes being added:
	// - (systemId, timestamp) - critical for system-only filters ordered by date
	// - (systemId, talkgroupId, timestamp) - critical for system+talkgroup filters ordered by date
	//
	// Note: Indexes automatically apply to ALL data in the table - both existing rows and all future inserts.
	// PostgreSQL will build the index from existing data when created, then automatically maintain
	// it for all new call data as it's inserted.

	queries = []string{
		// Index for system-only queries with date ordering
		`CREATE INDEX IF NOT EXISTS "calls_system_timestamp_idx" ON "calls" ("systemId", "timestamp")`,
		// Index for system+talkgroup queries with date ordering
		`CREATE INDEX IF NOT EXISTS "calls_system_talkgroup_timestamp_idx" ON "calls" ("systemId", "talkgroupId", "timestamp")`,
	}

	return db.migrateWithSchema("20250101000000-optimize-search-performance", queries, verbose)
}

// migrateCallUnitsIndex adds index on callUnits table for fast lookup by callId
// This dramatically speeds up search queries that fetch the source (unitRef) for each call
// Before: 23 seconds (full table scan of 4.5M rows, 201 times)
// After: 55ms (index lookup)
func migrateCallUnitsIndex(db *Database) error {
	queries := []string{
		`CREATE INDEX IF NOT EXISTS "callUnits_callId_idx" ON "callUnits" ("callId", "offset")`,
	}
	return db.migrateWithSchema("20250127000000-callunits-callid-index", queries, true)
}

// migrateTagsGroupsUniqueLabels adds unique constraints on the label column for tags and groups tables
// This prevents duplicate tag/group labels from being created during concurrent operations
func migrateTagsGroupsUniqueLabels(db *Database, verbose bool) error {
	var (
		count int
		err   error
		query string
	)

	formatError := errorFormatter("migration", "migrateTagsGroupsUniqueLabels")

	// Check if migration has already been applied
	query = `SELECT COUNT(*) FROM "rdioScannerMeta" WHERE "name" = '20251215000000-tags-groups-unique-labels'`

	if err = db.Sql.QueryRow(query).Scan(&count); err != nil {
		return formatError(err, query)
	}

	if count > 0 {
		return nil // Already migrated
	}

	if verbose {
		log.Printf("running database migration 20251215000000-tags-groups-unique-labels")
	}

	// Clean up duplicate tags - keep the first occurrence of each label
	query = `DELETE FROM "tags" WHERE "tagId" NOT IN (
		SELECT MIN("tagId") FROM "tags" GROUP BY "label"
	)`
	if _, err = db.Sql.Exec(query); err != nil {
		return formatError(err, "removing duplicate tags")
	}

	// Clean up duplicate groups - keep the first occurrence of each label
	query = `DELETE FROM "groups" WHERE "groupId" NOT IN (
		SELECT MIN("groupId") FROM "groups" GROUP BY "label"
	)`
	if _, err = db.Sql.Exec(query); err != nil {
		return formatError(err, "removing duplicate groups")
	}

	// Now create the unique indexes
	queries := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS "tags_label_unique" ON "tags" ("label")`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "groups_label_unique" ON "groups" ("label")`,
	}

	return db.migrateWithSchema("20251215000000-tags-groups-unique-labels", queries, verbose)
}

// Migration to remove alert tone columns from systems, talkgroups, tags, and groups
func migrateRemoveAlertTones(db *Database) error {
	formatError := errorFormatter("migration", "migrateRemoveAlertTones")

	// Check if migration already ran
	var count int
	if err := db.Sql.QueryRow(`SELECT COUNT(*) FROM "migrations" WHERE "id" = '20251219000000-remove-alert-tones'`).Scan(&count); err == nil && count > 0 {
		return nil
	}

	verbose := false
	if count == 0 {
		verbose = true
	}

	// Drop alert columns from systems, talkgroups, tags, and groups
	queries := []string{
		`ALTER TABLE "systems" DROP COLUMN IF EXISTS "alert"`,
		`ALTER TABLE "talkgroups" DROP COLUMN IF EXISTS "alert"`,
		`ALTER TABLE "tags" DROP COLUMN IF EXISTS "alert"`,
		`ALTER TABLE "groups" DROP COLUMN IF EXISTS "alert"`,
	}

	if len(queries) == 0 {
		// All columns already removed, just record migration
		if _, err := db.Sql.Exec(`INSERT INTO "migrations" ("id") VALUES ('20251219000000-remove-alert-tones')`); err != nil {
			return formatError(err, "recording migration")
		}
		return nil
	}

	return db.migrateWithSchema("20251219000000-remove-alert-tones", queries, verbose)
}

// Migration to remove led columns from systems, talkgroups, tags, and groups
func migrateRemoveLedColors(db *Database) error {
	formatError := errorFormatter("migration", "migrateRemoveLedColors")

	// Check if migration already ran
	var count int
	if err := db.Sql.QueryRow(`SELECT COUNT(*) FROM "migrations" WHERE "id" = '20251219000001-remove-led-colors'`).Scan(&count); err == nil && count > 0 {
		return nil
	}

	verbose := false
	if count == 0 {
		verbose = true
	}

	// Drop led columns from systems, talkgroups, tags, and groups
	queries := []string{
		`ALTER TABLE "systems" DROP COLUMN IF EXISTS "led"`,
		`ALTER TABLE "talkgroups" DROP COLUMN IF EXISTS "led"`,
		`ALTER TABLE "tags" DROP COLUMN IF EXISTS "led"`,
		`ALTER TABLE "groups" DROP COLUMN IF EXISTS "led"`,
	}

	if len(queries) == 0 {
		if _, err := db.Sql.Exec(`INSERT INTO "migrations" ("id") VALUES ('20251219000001-remove-led-colors')`); err != nil {
			return formatError(err, "recording migration")
		}
		return nil
	}

	return db.migrateWithSchema("20251219000001-remove-led-colors", queries, verbose)
}

// Migration to fix invalid user timestamps (empty strings or invalid values)
func migrateFixUserTimestamps(db *Database) error {
	formatError := errorFormatter("migration", "migrateFixUserTimestamps")

	// Check if migration already ran
	var count int
	if err := db.Sql.QueryRow(`SELECT COUNT(*) FROM "rdioScannerMeta" WHERE "name" = '20251228000000-fix-user-timestamps'`).Scan(&count); err != nil {
		return formatError(err, "checking migration status")
	}

	if count > 0 {
		return nil // Already migrated
	}

	log.Printf("running database migration 20251228000000-fix-user-timestamps")

	// Fix users with empty or invalid createdAt timestamps
	// Set to current time if empty, invalid, or 0
	currentTime := time.Now().Unix()

	// Fix createdAt: Set to current time if empty or invalid
	query := fmt.Sprintf(`UPDATE "users" SET "createdAt" = '%d' WHERE "createdAt" = '' OR "createdAt" = '0'`, currentTime)
	if _, err := db.Sql.Exec(query); err != nil {
		return formatError(err, "fixing createdAt timestamps")
	}

	// Fix lastLogin: Set to '0' if empty (0 means never logged in)
	query = `UPDATE "users" SET "lastLogin" = '0' WHERE "lastLogin" = ''`
	if _, err := db.Sql.Exec(query); err != nil {
		return formatError(err, "fixing lastLogin timestamps")
	}

	// Record migration as completed
	query = `INSERT INTO "rdioScannerMeta" ("name") VALUES ('20251228000000-fix-user-timestamps')`
	if _, err := db.Sql.Exec(query); err != nil {
		return formatError(err, "recording migration")
	}

	log.Printf("Fixed user timestamps - set createdAt to current time for %d users", currentTime)
	return nil
}

// fixAutoIncrementSequences - Resets auto-increment sequences to prevent duplicate key errors
// This ensures that all sequences are set to MAX(id) + 1 for their respective tables
func fixAutoIncrementSequences(db *Database) error {
	// Only applicable to PostgreSQL (SQLite handles this automatically)
	if db.Config.DbType != DbTypePostgresql {
		return nil
	}

	// List of tables and their ID columns/sequence names that use sequences
	// Use exact table names as they appear in the database (with proper casing)
	sequences := map[string]struct {
		table    string
		idColumn string
	}{
		"apikeys":           {table: "apikeys", idColumn: "apikeyId"},
		"calls":             {table: "calls", idColumn: "callId"},
		"groups":            {table: "groups", idColumn: "groupId"},
		"systems":           {table: "systems", idColumn: "systemId"},
		"tags":              {table: "tags", idColumn: "tagId"},
		"talkgroups":        {table: "talkgroups", idColumn: "talkgroupId"},
		"users":             {table: "users", idColumn: "userId"},
		"userGroups":        {table: "userGroups", idColumn: "userGroupId"},
		"registrationCodes": {table: "registrationCodes", idColumn: "registrationCodeId"},
		"downstreams":       {table: "downstreams", idColumn: "downstreamId"},
	}

	for _, seq := range sequences {
		// First, check if this is actually a sequence-based column
		// Query to find the actual sequence name for this column
		// Use double quotes to preserve case sensitivity
		var seqName sql.NullString
		query := fmt.Sprintf(`
			SELECT pg_get_serial_sequence('"%s"', '%s')
		`, seq.table, seq.idColumn)

		if err := db.Sql.QueryRow(query).Scan(&seqName); err != nil {
			// Silently skip if sequence not found
			continue
		}

		// If no sequence found, skip (might be a regular column)
		if !seqName.Valid || seqName.String == "" {
			continue
		}

		// Get the current max ID from the table
		var maxId sql.NullInt64
		query = fmt.Sprintf(`SELECT MAX("%s") FROM "%s"`, seq.idColumn, seq.table)
		if err := db.Sql.QueryRow(query).Scan(&maxId); err != nil {
			// Silently skip if error getting max ID
			continue
		}

		// Set the sequence to max + 1 (or 1 if table is empty)
		nextVal := int64(1)
		if maxId.Valid && maxId.Int64 > 0 {
			nextVal = maxId.Int64 + 1
		}

		// Use the actual sequence name returned by pg_get_serial_sequence
		query = fmt.Sprintf(`SELECT setval('%s', %d, false)`, seqName.String, nextVal)
		if _, err := db.Sql.Exec(query); err != nil {
			// Silently skip if error resetting sequence
			continue
		}
	}

	return nil
}

// migrateDownstreamsName adds name column to downstreams table
func migrateDownstreamsName(db *Database) error {
	query := `ALTER TABLE "downstreams" ADD COLUMN IF NOT EXISTS "name" text NOT NULL DEFAULT ''`
	if _, err := db.Sql.Exec(query); err != nil {
		log.Printf("migration note: %v", err)
	}
	return nil
}

func migrateTagsColor(db *Database) error {
	query := `ALTER TABLE "tags" ADD COLUMN IF NOT EXISTS "color" text NOT NULL DEFAULT ''`
	if _, err := db.Sql.Exec(query); err != nil {
		log.Printf("migration note: %v", err)
	}
	return nil
}
