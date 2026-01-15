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
	"bytes"
	"fmt"
	"os/exec"
	"path"
	"strings"
	"time"
)

// MigrateToOpus converts all existing M4A/AAC audio in the database to Opus format
// This provides ~50% storage savings and better voice quality at lower bitrates
func (db *Database) MigrateToOpus(batchSize int, dryRun bool, autoConfirm bool) error {
	if db.Sql == nil {
		return fmt.Errorf("database connection is nil")
	}

	// Check if FFmpeg is available and supports Opus
	if err := checkOpusSupport(); err != nil {
		return fmt.Errorf("FFmpeg Opus support check failed: %v", err)
	}

	fmt.Println("=================================================================")
	fmt.Println("                    OPUS MIGRATION TOOL")
	fmt.Println("=================================================================")
	fmt.Println("")

	if dryRun {
		fmt.Println("🔍 DRY RUN MODE - No changes will be made")
	} else {
		fmt.Println("⚠️  LIVE MODE - Database will be modified")
	}
	fmt.Println("")

	// Count total calls to migrate
	var totalCalls int
	var m4aCalls int
	var aacCalls int
	var mp4Calls int
	var mp3Calls int

	if db.Config.DbType == DbTypePostgresql {
		db.Sql.QueryRow(`SELECT COUNT(*) FROM "calls" WHERE "audioMime" IN ('audio/mp4', 'audio/m4a', 'audio/aac', 'audio/x-m4a', 'audio/mpeg', 'audio/mp3')`).Scan(&totalCalls)
		db.Sql.QueryRow(`SELECT COUNT(*) FROM "calls" WHERE "audioMime" = 'audio/m4a'`).Scan(&m4aCalls)
		db.Sql.QueryRow(`SELECT COUNT(*) FROM "calls" WHERE "audioMime" = 'audio/aac'`).Scan(&aacCalls)
		db.Sql.QueryRow(`SELECT COUNT(*) FROM "calls" WHERE "audioMime" IN ('audio/mp4', 'audio/x-m4a')`).Scan(&mp4Calls)
		db.Sql.QueryRow(`SELECT COUNT(*) FROM "calls" WHERE "audioMime" IN ('audio/mpeg', 'audio/mp3')`).Scan(&mp3Calls)
	} else {
		db.Sql.QueryRow(`SELECT COUNT(*) FROM "calls" WHERE "audioMime" IN ('audio/mp4', 'audio/m4a', 'audio/aac', 'audio/x-m4a', 'audio/mpeg', 'audio/mp3')`).Scan(&totalCalls)
		db.Sql.QueryRow(`SELECT COUNT(*) FROM "calls" WHERE "audioMime" = 'audio/m4a'`).Scan(&m4aCalls)
		db.Sql.QueryRow(`SELECT COUNT(*) FROM "calls" WHERE "audioMime" = 'audio/aac'`).Scan(&aacCalls)
		db.Sql.QueryRow(`SELECT COUNT(*) FROM "calls" WHERE "audioMime" IN ('audio/mp4', 'audio/x-m4a')`).Scan(&mp4Calls)
		db.Sql.QueryRow(`SELECT COUNT(*) FROM "calls" WHERE "audioMime" IN ('audio/mpeg', 'audio/mp3')`).Scan(&mp3Calls)
	}

	fmt.Printf("📊 Found %d calls to migrate:\n", totalCalls)
	fmt.Printf("   - audio/m4a:  %d calls\n", m4aCalls)
	fmt.Printf("   - audio/mp4:  %d calls\n", mp4Calls)
	fmt.Printf("   - audio/aac:  %d calls\n", aacCalls)
	fmt.Printf("   - audio/mp3:  %d calls\n", mp3Calls)
	fmt.Println("")

	if totalCalls == 0 {
		fmt.Println("✅ No calls need migration - all done!")
		return nil
	}

	// Calculate estimated storage savings
	var totalSize int64
	if db.Config.DbType == DbTypePostgresql {
		db.Sql.QueryRow(`SELECT SUM(length("audio")) FROM "calls" WHERE "audioMime" IN ('audio/mp4', 'audio/m4a', 'audio/aac', 'audio/x-m4a', 'audio/mpeg', 'audio/mp3')`).Scan(&totalSize)
	} else {
		db.Sql.QueryRow(`SELECT SUM(length("audio")) FROM "calls" WHERE "audioMime" IN ('audio/mp4', 'audio/m4a', 'audio/aac', 'audio/x-m4a', 'audio/mpeg', 'audio/mp3')`).Scan(&totalSize)
	}

	estimatedSavings := float64(totalSize) * 0.5 // 50% savings expected
	fmt.Printf("💾 Current storage: %.2f MB\n", float64(totalSize)/(1024*1024))
	fmt.Printf("💰 Estimated savings: %.2f MB (50%%)\n", estimatedSavings/(1024*1024))
	fmt.Printf("📦 Final size: %.2f MB\n", float64(totalSize-int64(estimatedSavings))/(1024*1024))
	fmt.Println("")

	if dryRun {
		fmt.Println("✅ Dry run complete - no changes made")
		return nil
	}

	// Confirm migration
	fmt.Println("⏱️  Estimated time: ~" + estimateTime(totalCalls))
	fmt.Println("")
	fmt.Println("⚠️  WARNING: This operation will modify your database!")
	fmt.Println("⚠️  Please ensure you have a backup before proceeding.")
	fmt.Println("")

	if !autoConfirm {
		fmt.Print("Continue with migration? (yes/no): ")

		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "yes" {
			fmt.Println("❌ Migration cancelled")
			return nil
		}
	} else {
		fmt.Println("✅ Auto-confirmed (opus_migration from INI file)")
	}

	fmt.Println("")
	fmt.Println("🚀 Starting migration...")
	fmt.Println("")

	// Process in batches
	// NOTE: We use LIMIT without OFFSET because the WHERE clause changes as we convert
	// Always select the first batch of unconverted files
	migrated := 0
	failed := 0
	skipped := 0
	totalSaved := int64(0)
	startTime := time.Now()

	for migrated+failed+skipped < totalCalls {
		var query string
		// Always get first N unconverted files (no OFFSET needed since they're converted as we go)
		if db.Config.DbType == DbTypePostgresql {
			query = fmt.Sprintf(`SELECT "callId", "audio", "audioFilename", "audioMime" FROM "calls" WHERE "audioMime" IN ('audio/mp4', 'audio/m4a', 'audio/aac', 'audio/x-m4a', 'audio/mpeg', 'audio/mp3') ORDER BY "callId" LIMIT %d`, batchSize)
		} else {
			query = fmt.Sprintf(`SELECT "callId", "audio", "audioFilename", "audioMime" FROM "calls" WHERE "audioMime" IN ('audio/mp4', 'audio/m4a', 'audio/aac', 'audio/x-m4a', 'audio/mpeg', 'audio/mp3') ORDER BY "callId" LIMIT %d`, batchSize)
		}

		rows, err := db.Sql.Query(query)
		if err != nil {
			fmt.Printf("❌ Error querying batch: %v\n", err)
			continue
		}

		batchCount := 0
		for rows.Next() {
			var callId uint64
			var audio []byte
			var filename string
			var mimeType string

			if err := rows.Scan(&callId, &audio, &filename, &mimeType); err != nil {
				fmt.Printf("❌ Error scanning row: %v\n", err)
				failed++
				continue
			}

			batchCount++

			// Skip if already Opus (shouldn't happen, but safe)
			if mimeType == "audio/opus" {
				skipped++
				continue
			}

			// Convert to Opus
			opusAudio, err := convertToOpus(audio)
			if err != nil {
				fmt.Printf("❌ Call %d: Conversion failed: %v\n", callId, err)
				failed++
				continue
			}

			// Update filename
			newFilename := strings.TrimSuffix(filename, path.Ext(filename)) + ".opus"

			// Update database
			var updateQuery string
			if db.Config.DbType == DbTypePostgresql {
				updateQuery = fmt.Sprintf(`UPDATE "calls" SET "audio" = $1, "audioFilename" = '%s', "audioMime" = 'audio/opus' WHERE "callId" = %d`, newFilename, callId)
				_, err = db.Sql.Exec(updateQuery, opusAudio)
			} else {
				updateQuery = fmt.Sprintf(`UPDATE "calls" SET "audio" = ?, "audioFilename" = '%s', "audioMime" = 'audio/opus' WHERE "callId" = %d`, newFilename, callId)
				_, err = db.Sql.Exec(updateQuery, opusAudio)
			}

			if err != nil {
				fmt.Printf("❌ Call %d: Database update failed: %v\n", callId, err)
				failed++
				continue
			}

			// Track savings
			saved := len(audio) - len(opusAudio)
			totalSaved += int64(saved)
			migrated++

			// Progress update every 10 calls
			if migrated%10 == 0 {
				elapsed := time.Since(startTime)
				rate := float64(migrated) / elapsed.Seconds()
				remaining := int(float64(totalCalls-migrated) / rate)
				fmt.Printf("✅ Progress: %d/%d (%.1f%%) | Saved: %.2f MB | ETA: %s\n",
					migrated, totalCalls,
					float64(migrated)/float64(totalCalls)*100,
					float64(totalSaved)/(1024*1024),
					time.Duration(remaining)*time.Second)
			}
		}
		rows.Close()

		// If no rows were returned, we're done (all calls converted)
		if batchCount == 0 {
			break
		}
	}

	fmt.Println("")
	fmt.Println("=================================================================")
	fmt.Println("                    MIGRATION COMPLETE")
	fmt.Println("=================================================================")
	fmt.Printf("✅ Migrated: %d calls\n", migrated)
	fmt.Printf("❌ Failed: %d calls\n", failed)
	fmt.Printf("⏭️  Skipped: %d calls\n", skipped)
	fmt.Printf("💾 Space saved: %.2f MB (%.1f%%)\n",
		float64(totalSaved)/(1024*1024),
		float64(totalSaved)/float64(totalSize)*100)
	fmt.Printf("⏱️  Total time: %s\n", time.Since(startTime).Round(time.Second))
	fmt.Println("")

	// Automatically run VACUUM FULL for PostgreSQL
	if db.Config.DbType == DbTypePostgresql {
		fmt.Println("🔧 Running VACUUM FULL to reclaim disk space...")
		fmt.Println("   (This may take several minutes depending on database size)")
		fmt.Println("")

		vacuumStart := time.Now()
		if _, err := db.Sql.Exec(`VACUUM FULL "calls"`); err != nil {
			fmt.Printf("⚠️  Warning: VACUUM FULL failed: %v\n", err)
			fmt.Println("💡 You can manually run: psql -d yourdb -c 'VACUUM FULL calls;'")
		} else {
			fmt.Printf("✅ VACUUM FULL completed in %s\n", time.Since(vacuumStart).Round(time.Second))
			fmt.Println("💾 Disk space has been reclaimed")
		}
		fmt.Println("")
	}

	return nil
}

// convertToOpus converts audio bytes to Opus format using FFmpeg
func convertToOpus(audio []byte) ([]byte, error) {
	args := []string{
		"-y", "-loglevel", "error",
		"-i", "pipe:0", // Read from stdin
		"-ar", "16000", // 16kHz sample rate
		"-ac", "1", // Mono
		"-c:a", "libopus",
		"-b:a", "16k", // 16 kbps
		"-vbr", "on", // Variable bitrate
		"-application", "voip", // Voice optimization
		"-compression_level", "10", // Max compression
		"-f", "opus", // Opus format
		"pipe:1", // Write to stdout
	}

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdin = bytes.NewReader(audio)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %v, stderr: %s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}

// checkOpusSupport verifies FFmpeg can encode Opus
func checkOpusSupport() error {
	cmd := exec.Command("ffmpeg", "-encoders")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg not found or not executable")
	}

	output := stdout.String()
	if !strings.Contains(output, "libopus") {
		return fmt.Errorf("FFmpeg does not have libopus encoder support. Please install ffmpeg with libopus.")
	}

	return nil
}

// estimateTime estimates how long the migration will take
func estimateTime(totalCalls int) string {
	// Estimate ~0.5 seconds per call (conservative)
	seconds := totalCalls / 2
	if seconds < 60 {
		return fmt.Sprintf("%d seconds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%d minutes", seconds/60)
	}
	return fmt.Sprintf("%.1f hours", float64(seconds)/3600)
}
