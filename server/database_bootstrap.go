// Copyright (C) 2025 Thinline Dynamic Solutions
//
// PostgreSQL initial schema bootstrap runs once per database. Established
// instances skip table DDL on every boot; missing indexes are built in the
// background after the server is ready.

package main

import (
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/lib/pq"
)

const postgresqlBootstrapMarker = "postgresql-bootstrap-complete"

var createIndexNameRe = regexp.MustCompile(`CREATE INDEX IF NOT EXISTS "([^"]+)"`)

func splitPostgresqlSchema(schema []string) (ddl, indexes []string) {
	for _, query := range schema {
		trimmed := strings.ToUpper(strings.TrimSpace(query))
		if strings.HasPrefix(trimmed, "CREATE INDEX") {
			indexes = append(indexes, query)
		} else {
			ddl = append(ddl, query)
		}
	}
	return ddl, indexes
}

func indexNameFromDDL(query string) string {
	if m := createIndexNameRe.FindStringSubmatch(query); len(m) > 1 {
		return m[1]
	}
	return ""
}

func postgresqlBootstrapComplete(db *Database) bool {
	var count int
	if err := db.Sql.QueryRow(`SELECT COUNT(*) FROM "rdioScannerMeta" WHERE "name" = $1`, postgresqlBootstrapMarker).Scan(&count); err == nil && count > 0 {
		return true
	}

	var exists bool
	if err := db.Sql.QueryRow(`SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = 'options'
	)`).Scan(&exists); err == nil && exists {
		markPostgresqlBootstrapComplete(db)
		return true
	}

	return false
}

func markPostgresqlBootstrapComplete(db *Database) {
	if _, err := db.Sql.Exec(`INSERT INTO "rdioScannerMeta" ("name") VALUES ($1) ON CONFLICT ("name") DO NOTHING`, postgresqlBootstrapMarker); err != nil {
		log.Printf("migration note (postgresql bootstrap marker): %v", err)
	}
}

func runPostgresqlBootstrap(db *Database) error {
	ddl, indexes := splitPostgresqlSchema(PostgresqlSchema)

	log.Println("postgresql bootstrap: creating tables and columns...")

	tx, err := db.Sql.Begin()
	if err != nil {
		return err
	}

	for i, query := range ddl {
		if _, err = tx.Exec(query); err != nil {
			log.Printf("ERROR: Failed to execute schema statement %d: %v", i+1, err)
			tx.Rollback()
			return fmt.Errorf("%w in %s", err, query)
		}
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return err
	}

	log.Printf("postgresql bootstrap: applying %d indexes...", len(indexes))
	if err := applyPostgresqlIndexes(db, indexes); err != nil {
		return err
	}

	markPostgresqlBootstrapComplete(db)
	log.Println("postgresql bootstrap completed")
	return nil
}

func applyPostgresqlIndexes(db *Database, indexes []string) error {
	for i, query := range indexes {
		if _, err := db.Sql.Exec(query); err != nil {
			log.Printf("ERROR: Failed to execute schema index %d: %v", i+1, err)
			return fmt.Errorf("%w in %s", err, query)
		}
	}
	return nil
}

// pgIndexState reports whether an index exists and whether it is valid. A
// CREATE INDEX CONCURRENTLY interrupted by a restart leaves an invalid index
// under the same name, which the planner never uses.
func pgIndexState(db *Database, indexName string) (exists bool, valid bool, err error) {
	err = db.Sql.QueryRow(`SELECT i.indisvalid
		FROM pg_class c
		JOIN pg_index i ON i.indexrelid = c.oid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema() AND c.relname = $1`, indexName).Scan(&valid)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, valid, nil
}

func ensureBootstrapIndexesBackground(db *Database) {
	_, indexes := splitPostgresqlSchema(PostgresqlSchema)

	for _, query := range indexes {
		indexName := indexNameFromDDL(query)
		if indexName == "" || !validSQLIdentifier(indexName) {
			continue
		}

		exists, valid, err := pgIndexState(db, indexName)
		if err != nil {
			writeLogStdout(fmt.Sprintf("bootstrap index check %q: %v", indexName, err))
			continue
		}
		if exists && valid {
			continue
		}
		if exists {
			writeLogStdout(fmt.Sprintf("bootstrap index %q is invalid (interrupted build) — dropping and rebuilding...", indexName))
			if _, err := db.Sql.Exec(fmt.Sprintf(`DROP INDEX CONCURRENTLY IF EXISTS %s`, pq.QuoteIdentifier(indexName))); err != nil {
				writeLogStdout(fmt.Sprintf("bootstrap index %q drop failed: %v", indexName, err))
				continue
			}
		}

		concurrent := strings.Replace(query, "CREATE INDEX IF NOT EXISTS", "CREATE INDEX CONCURRENTLY", 1)
		writeLogStdout(fmt.Sprintf("bootstrap index %q missing — building concurrently in background...", indexName))
		if _, err := db.Sql.Exec(concurrent); err != nil {
			writeLogStdout(fmt.Sprintf("bootstrap index %q concurrent build failed (%v), trying blocking create", indexName, err))
			if _, err2 := db.Sql.Exec(query); err2 != nil {
				writeLogStdout(fmt.Sprintf("bootstrap index %q build failed: %v", indexName, err2))
				continue
			}
		}
		writeLogStdout(fmt.Sprintf("bootstrap index %q build completed", indexName))

		// Yield between large index builds so call ingest keeps pool slots.
		time.Sleep(500 * time.Millisecond)
	}
}
