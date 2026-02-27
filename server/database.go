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
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Database struct {
	Config *Config
	Sql    *sql.DB
}

func NewDatabase(config *Config) *Database {
	var err error

	database := &Database{Config: config}

	dsn := fmt.Sprintf("postgresql://%s:%s@%s:%d/%s", config.DbUsername, config.DbPassword, config.DbHost, config.DbPort, config.DbName)

	if database.Sql, err = sql.Open("pgx", dsn); err != nil {
		log.Printf("FATAL: Failed to open PostgreSQL connection: %v", err)
		log.Printf("Please check your database configuration and ensure the database server is running.")
		os.Exit(1)
	}

	// Optimize connection pool for multi-core processing
	// Increased to prevent connection pool exhaustion under load
	maxConns := runtime.NumCPU() * 10 // 10 connections per CPU core (increased from 4)
	if maxConns < 50 {
		maxConns = 50 // Minimum of 50 connections (increased from 25)
	}
	if maxConns > 200 {
		maxConns = 200 // Cap at 200 connections (increased from 100)
	}

	database.Sql.SetConnMaxLifetime(30 * time.Minute)
	database.Sql.SetMaxIdleConns(maxConns)
	database.Sql.SetMaxOpenConns(maxConns)

	log.Printf("Database connection pool configured: %d max connections for %d CPU cores", maxConns, runtime.NumCPU())

	if err = database.migrate(); err != nil {
		log.Printf("FATAL: Database migration failed: %v", err)
		log.Printf("The database schema must be up to date for the server to run. Please fix the migration error and try again.")
		os.Exit(1)
	}

	// Seeding disabled to avoid conflicts during config imports
	// Auto-seeding default tags and groups can cause unique constraint violations
	// when importing configurations that define their own tags and groups
	// if err = database.seed(); err != nil {
	// 	log.Printf("WARNING: Database seeding failed: %v", err)
	// 	log.Printf("The server will continue, but default groups and tags may not be available.")
	// 	// Continue execution - seeding is not critical for server operation
	// }

	return database
}

func (db *Database) migrate() error {
	var schema []string

	formatError := errorFormatter("database", "migrate")

	// Prepare migration table first (v6 style)
	if _, err := prepareMigration(db); err != nil {
		return formatError(err, "")
	}

	schema = PostgresqlSchema

	if tx, err := db.Sql.Begin(); err == nil {
		for i, query := range schema {
			if _, err = tx.Exec(query); err != nil {
				log.Printf("ERROR: Failed to execute schema statement %d: %v", i+1, err)
				tx.Rollback()
				return formatError(err, query)
			}
		}

		if err = tx.Commit(); err != nil {
			log.Printf("ERROR: Failed to commit schema transaction: %v", err)
			tx.Rollback()
			return formatError(err, "")
		}
	} else {
		log.Printf("ERROR: Failed to begin schema transaction: %v", err)
		return formatError(err, "")
	}

	if err := migrateGroups(db); err != nil {
		return formatError(err, "")
	}

	if err := migrateTags(db); err != nil {
		return formatError(err, "")
	}

	if err := migrateSystems(db); err != nil {
		return formatError(err, "")
	}

	if err := migrateTalkgroups(db); err != nil {
		return formatError(err, "")
	}

	if err := migrateUnits(db); err != nil {
		return formatError(err, "")
	}

	if err := migrateOptions(db); err != nil {
		return formatError(err, "")
	}

	if err := migrateMeta(db); err != nil {
		return formatError(err, "")
	}

	if err := migrateLogs(db); err != nil {
		return formatError(err, "")
	}

	if err := migrateDownstreams(db); err != nil {
		return formatError(err, "")
	}

	if err := migrateDirwatches(db); err != nil {
		return formatError(err, "")
	}

	if err := migrateCalls(db); err != nil {
		return formatError(err, "")
	}

	if err := migrateCallsRefs(db); err != nil {
		return formatError(err, "")
	}

	if err := migrateApikeys(db); err != nil {
		return formatError(err, "")
	}

	// Migrate users table
	if err := migrateUsers(db); err != nil {
		return formatError(err, "")
	}

	if err := migrateUserPins(db); err != nil {
		return formatError(err, "")
	}

	// Migrate alert-related tables and columns
	if err := migrateToneDetection(db); err != nil {
		return formatError(err, "")
	}

	if err := migrateAlerts(db); err != nil {
		return formatError(err, "")
	}

	if err := migrateAlertPreferences(db); err != nil {
		return formatError(err, "")
	}

	// Migrate userGroups maxUsers column
	if err := migrateUserGroupsMaxUsers(db); err != nil {
		return formatError(err, "")
	}

	// Migrate system admins and system alerts
	if err := migrateSystemAdmins(db); err != nil {
		return formatError(err, "")
	}

	// Migrate registrationCodes createdBy to be nullable
	if err := migrateRegistrationCodesCreatedBy(db); err != nil {
		return formatError(err, "")
	}

	// Migrate userInvitations invitedBy to be nullable
	if err := migrateUserInvitationsInvitedBy(db); err != nil {
		return formatError(err, "")
	}

	// Migrate tags and groups to have unique labels
	if err := migrateTagsGroupsUniqueLabels(db, false); err != nil {
		return formatError(err, "")
	}

	// Migrate userGroups allowAddExistingUsers column
	if err := migrateUserGroupsAllowAddExistingUsers(db); err != nil {
		return formatError(err, "")
	}

	// Migrate userGroups billing fields (stripePriceId, billingMode)
	if err := migrateUserGroupsBillingFields(db); err != nil {
		return formatError(err, "")
	}

	// Migrate userGroups pricingOptions column
	if err := migrateUserGroupsPricingOptions(db); err != nil {
		return formatError(err, "")
	}

	// Migrate userGroups collectSalesTax column
	if err := migrateUserGroupsCollectSalesTax(db); err != nil {
		return formatError(err, "")
	}

	// Migrate users accountExpiresAt column
	if err := migrateUserAccountExpiresAt(db); err != nil {
		return formatError(err, "")
	}

	// Migrate transferRequests approval token columns
	if err := migrateTransferRequestsApprovalTokens(db); err != nil {
		return formatError(err, "")
	}

	// Migrate calls performance indexes (matching v6 migration20250101000000)
	if err := migrateCallsPerformanceIndexes(db); err != nil {
		return formatError(err, "")
	}

	// Migrate callUnits index for fast search performance
	if err := migrateCallUnitsIndex(db); err != nil {
		return formatError(err, "")
	}

	// Remove alert tone columns
	if err := migrateRemoveAlertTones(db); err != nil {
		return formatError(err, "")
	}

	// Remove LED color columns
	if err := migrateRemoveLedColors(db); err != nil {
		return formatError(err, "")
	}

	// Fix invalid user timestamps (empty strings or 0 values)
	if err := migrateFixUserTimestamps(db); err != nil {
		return formatError(err, "")
	}

	// Add name column to downstreams table
	if err := migrateDownstreamsName(db); err != nil {
		return formatError(err, "")
	}

	// Add color field to tags
	if err := migrateTagsColor(db); err != nil {
		return formatError(err, "")
	}

	// Fix auto-increment sequences to prevent duplicate key errors
	if err := fixAutoIncrementSequences(db); err != nil {
		return formatError(err, "")
	}

	// Fix orphaned keyword list IDs in user alert preferences
	if err := migrateFixKeywordListIds(db); err != nil {
		return formatError(err, "")
	}

	// Enhanced duplicate detection with site frequencies, preferred sites, and API key preferences
	if err := migrateEnhancedDuplicateDetection(db); err != nil {
		return formatError(err, "")
	}

	// System health alert options (Beta 8/9)
	if err := migrateSystemHealthAlertOptions(db); err != nil {
		return formatError(err, "")
	}

	// Remove CASCADE DELETE from userAlertPreferences (Beta 9.2)
	if err := migrateRemoveUserAlertPreferencesCascadeDelete(db); err != nil {
		return formatError(err, "")
	}

	// Index on logs.timestamp to prevent full table scans on large log tables
	if err := migrateLogsIndex(db); err != nil {
		return formatError(err, "")
	}

	return nil
}

func (db *Database) seed() error {
	formatError := func(err error) error {
		return fmt.Errorf("database.seed: %v", err)
	}
	if err := seedGroups(db); err != nil {
		return formatError(err)
	}

	if err := seedTags(db); err != nil {
		return formatError(err)
	}

	return nil
}

func escapeQuotes(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
