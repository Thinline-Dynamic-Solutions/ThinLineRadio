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

	// Fixed conservative pool: works for typical all-in-one installs and avoids
	// each process reserving dozens of PostgreSQL backends (important on shared DBs).
	const maxOpenConns = 25
	const maxIdleConns = 8

	database.Sql.SetConnMaxLifetime(30 * time.Minute)
	database.Sql.SetConnMaxIdleTime(5 * time.Minute)
	database.Sql.SetMaxIdleConns(maxIdleConns)
	database.Sql.SetMaxOpenConns(maxOpenConns)

	log.Printf("Database connection pool configured: max_open=%d max_idle=%d", maxOpenConns, maxIdleConns)

	if err = database.migrate(); err != nil {
		log.Printf("FATAL: Database migration failed: %v", err)
		if strings.Contains(err.Error(), "57P01") || strings.Contains(err.Error(), "administrator command") {
			log.Printf("The database connection was terminated during migration — usually because the server was stopped mid-startup or PostgreSQL was restarted. Start again and let migration finish without closing the process.")
		} else {
			log.Printf("The database schema must be up to date for the server to run. Please fix the migration error and try again.")
		}
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

func isRetryableMigrationErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unexpected eof") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "conn closed") ||
		strings.Contains(s, "driver: bad connection") ||
		strings.Contains(s, "sqlstate 57p01") ||
		strings.Contains(s, "sqlstate 08006") ||
		strings.Contains(s, "sqlstate 08003") ||
		strings.Contains(s, "sqlstate 08001") ||
		strings.Contains(s, "server closed the connection")
}

// runMigrationStep pings the DB, runs fn, and retries on transient connection drops
// (common on Windows/Docker Postgres when a prior heavy ALTER kills the session).
func (db *Database) runMigrationStep(name string, fn func(*Database) error) error {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := db.Sql.Ping(); err != nil {
			log.Printf("migration %s: reconnecting (attempt %d/%d): %v", name, attempt, maxAttempts, err)
			time.Sleep(time.Duration(attempt) * time.Second)
			if err := db.Sql.Ping(); err != nil {
				lastErr = err
				if attempt == maxAttempts || !isRetryableMigrationErr(err) {
					return fmt.Errorf("%s: ping: %w", name, err)
				}
				continue
			}
		}
		if err := fn(db); err != nil {
			lastErr = err
			if attempt < maxAttempts && isRetryableMigrationErr(err) {
				log.Printf("migration %s: retryable error (attempt %d/%d): %v", name, attempt, maxAttempts, err)
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}
	return fmt.Errorf("%s: %w", name, lastErr)
}

func (db *Database) migrate() error {
	formatError := errorFormatter("database", "migrate")

	// Prepare migration table first (v6 style)
	if _, err := prepareMigration(db); err != nil {
		return formatError(err, "")
	}

	if !postgresqlBootstrapComplete(db) {
		if err := runPostgresqlBootstrap(db); err != nil {
			return formatError(err, "")
		}
	} else {
		log.Println("postgresql bootstrap already complete, skipping")
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

	// Migrate userGroups taxMode and stripeTaxRateId columns
	if err := migrateUserGroupsTaxMode(db); err != nil {
		return formatError(err, "")
	}

	// Migrate users accountExpiresAt column
	if err := migrateUserAccountExpiresAt(db); err != nil {
		return formatError(err, "")
	}

	// Migrate users forcePasswordReset column
	if err := migrateUserForcePasswordReset(db); err != nil {
		return formatError(err, "")
	}

	// Migrate users userGroupIds column (multi-group membership)
	if err := migrateUserGroupIds(db); err != nil {
		return formatError(err, "")
	}

	// Migrate users mobile setup token (one-time app sign-in link)
	if err := migrateUserMobileSetupToken(db); err != nil {
		return formatError(err, "")
	}

	// Migrate users mobile welcome email sent flag
	if err := migrateUserMobileWelcomeEmailSent(db); err != nil {
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

	// One-time: allow full relay listener-email sync again after upgrade
	if err := migrateRelayListenerEmailsResync(db); err != nil {
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

	// Remaining steps use ping+retry so a dropped Postgres session (unexpected EOF)
	// after a heavy ALTER does not hard-fail startup on the next catalog query.
	lateSteps := []struct {
		name string
		fn   func(*Database) error
	}{
		{"migrateLogsCategory", migrateLogsCategory},
		{"migrateCallUnitsLabel", migrateCallUnitsLabel},
		{"migrateAlertCooldown", migrateAlertCooldown},
		{"migrateLinkedVoiceTalkgroup", migrateLinkedVoiceTalkgroup},
		{"migrateAudioConversionModes", migrateAudioConversionModes},
		{"migrateRemoveAudioCodecBitrate", migrateRemoveAudioCodecBitrate},
		{"migrateRegistrationCodesLabel", migrateRegistrationCodesLabel},
		{"migrateAlertsEnabled", migrateAlertsEnabled},
		{"migrateChannelNotificationSounds", migrateChannelNotificationSounds},
		{"migrateTranscriptionPrompt", migrateTranscriptionPrompt},
		{"migrateAutoPopulateAlertsEnabled", migrateAutoPopulateAlertsEnabled},
		{"migrateAutoPopulateUnits", migrateAutoPopulateUnits},
		{"migratePagerAlert", migratePagerAlert},
		{"migrateToneSetPagerAlerts", migrateToneSetPagerAlerts},
		{"migrateCallsDuplicateAudioOf", migrateCallsDuplicateAudioOf},
		{"migrateCallsAudioDuration", migrateCallsAudioDuration},
		{"migrateCallsIsDuplicate", migrateCallsIsDuplicate},
		{"migrateCallsAudioHash", migrateCallsAudioHash},
		{"migrateCallsTrainingReview", migrateCallsTrainingReview},
		{"migrateToneSetAutoLearn", migrateToneSetAutoLearn},
		{"migrateBulkToneDetection", migrateBulkToneDetection},
		{"migrateUnitAliasAutoLearn", migrateUnitAliasAutoLearn},
		{"migrateAutoLearnTagRollout", migrateAutoLearnTagRollout},
		{"migrateCallsVerifiedDuplicate", migrateCallsVerifiedDuplicate},
		{"migratePostgresSiteRefToText", migratePostgresSiteRefToText},
		{"migrateApikeyNoAudioMonitoring", migrateApikeyNoAudioMonitoring},
		{"migrateRetentionDays", migrateRetentionDays},
		{"migrateSystemDuplicateDetection", migrateSystemDuplicateDetection},
		{"migrateUserAlertPushPreferences", migrateUserAlertPushPreferences},
		{"migrateIncidentMapping", migrateIncidentMapping},
		{"migrateCallNatures", migrateCallNatures},
		{"migrateKeywordAlertUnique", migrateKeywordAlertUnique},
	}
	for _, step := range lateSteps {
		if err := db.runMigrationStep(step.name, step.fn); err != nil {
			return formatError(err, "")
		}
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
