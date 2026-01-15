# Opus Migration Guide

This guide explains how to migrate your existing Thinline Radio database from M4A/AAC audio format to Opus format.

## Why Migrate to Opus?

- **50% storage reduction** (16k Opus vs 32k AAC)
- **Better voice quality** at lower bitrates
- **~120 KB per minute** vs 240 KB currently
- **Modern codec** optimized specifically for speech
- **Native support** on all modern devices (Android 5+, iOS 11+)

## Prerequisites

✅ **Backup your database first!**

```bash
# PostgreSQL backup
pg_dump -U postgres thinline > backup_before_opus_$(date +%Y%m%d).sql

# Or use your normal backup process
```

✅ **Ensure FFmpeg has Opus support:**

```bash
ffmpeg -encoders | grep libopus
```

You should see output like: `libopus                 libopus Opus`

If not, install FFmpeg with Opus:
```bash
# Ubuntu/Debian
sudo apt install ffmpeg

# macOS
brew install ffmpeg

# Should already include libopus
```

✅ **Stop the Thinline Radio server:**

```bash
# If running as service
sudo systemctl stop thinline-radio

# Or if running directly
# Press Ctrl+C to stop the server
```

## Migration Options

### Option 1: Dry Run (Recommended First Step)

Preview what will happen without making any changes:

```bash
./thinline-radio -migrate_to_opus -migrate_dry_run
```

This shows:
- Number of calls to migrate
- Current storage size
- Estimated savings
- Estimated time

### Option 2: Full Migration

Migrate all audio at once:

```bash
./thinline-radio -migrate_to_opus
```

### Option 3: Custom Batch Size

Process in smaller or larger batches:

```bash
# Process 50 calls at a time (slower, less memory)
./thinline-radio -migrate_to_opus -migrate_batch_size=50

# Process 500 calls at a time (faster, more memory)
./thinline-radio -migrate_to_opus -migrate_batch_size=500
```

## Migration Process

### Step 1: Dry Run

```bash
$ ./thinline-radio -migrate_to_opus -migrate_dry_run

=================================================================
                    OPUS MIGRATION TOOL
=================================================================

🔍 DRY RUN MODE - No changes will be made

📊 Found 5420 calls to migrate:
   - audio/m4a:  3200 calls
   - audio/mp4:  2100 calls
   - audio/aac:  120 calls

💾 Current storage: 1250.45 MB
💰 Estimated savings: 625.23 MB (50%)
📦 Final size: 625.22 MB

✅ Dry run complete - no changes made
```

### Step 2: Actual Migration

```bash
$ ./thinline-radio -migrate_to_opus

=================================================================
                    OPUS MIGRATION TOOL
=================================================================

⚠️  LIVE MODE - Database will be modified

📊 Found 5420 calls to migrate:
   - audio/m4a:  3200 calls
   - audio/mp4:  2100 calls
   - audio/aac:  120 calls

💾 Current storage: 1250.45 MB
💰 Estimated savings: 625.23 MB (50%)
📦 Final size: 625.22 MB

⏱️  Estimated time: ~45 minutes

⚠️  WARNING: This operation will modify your database!
⚠️  Please ensure you have a backup before proceeding.

Continue with migration? (yes/no): yes

🚀 Starting migration...

✅ Progress: 100/5420 (1.8%) | Saved: 12.5 MB | ETA: 42m
✅ Progress: 200/5420 (3.7%) | Saved: 24.8 MB | ETA: 40m
...
✅ Progress: 5400/5420 (99.6%) | Saved: 620.1 MB | ETA: 30s

=================================================================
                    MIGRATION COMPLETE
=================================================================
✅ Migrated: 5420 calls
❌ Failed: 0 calls
⏭️  Skipped: 0 calls
💾 Space saved: 625.23 MB (50.0%)
⏱️  Total time: 43m 12s

💡 Recommendation: Run 'VACUUM FULL' on your database to reclaim space:
   psql -d thinline -c 'VACUUM FULL calls;'
```

### Step 3: Reclaim Space (PostgreSQL)

The migration updates records, but PostgreSQL doesn't automatically free the space. Run VACUUM:

```bash
# Connect to database and vacuum
psql -U postgres -d thinline -c "VACUUM FULL calls;"

# This may take 10-30 minutes depending on database size
```

### Step 4: Verify Migration

Check that calls are now Opus:

```sql
SELECT 
    "audioMime",
    COUNT(*) as count,
    pg_size_pretty(SUM(length("audio"))::bigint) as total_size
FROM "calls"
GROUP BY "audioMime"
ORDER BY count DESC;
```

Expected output:
```
 audioMime  | count |  total_size
------------+-------+-------------
 audio/opus |  5420 | 625 MB
```

### Step 5: Update Mobile App (Important!)

Before users can play Opus audio, they need the updated mobile app:

1. **Build and deploy mobile app** with Opus support (already done in code)
2. **Release to app stores** 
3. **Wait for 90%+ adoption** (check analytics)
4. **Then restart server** with new Opus encoding

## Troubleshooting

### Migration Fails on Some Calls

Some calls may fail to convert. The migration continues and reports:

```
❌ Call 1234: Conversion failed: ffmpeg failed: Invalid data
```

**Reasons:**
- Corrupted audio file
- Unsupported format
- Zero-length audio

**Solution:** These calls keep their original format. You can:
1. Investigate specific calls
2. Delete corrupted calls
3. Re-run migration (it will skip already-converted calls)

### "FFmpeg not found"

```
❌ FFmpeg not found or not executable
```

**Solution:**
```bash
# Install FFmpeg
sudo apt install ffmpeg  # Ubuntu/Debian
brew install ffmpeg      # macOS

# Verify
ffmpeg -version
```

### "libopus encoder not found"

```
❌ FFmpeg does not have libopus encoder support
```

**Solution:**
Your FFmpeg was compiled without Opus. Install a version with Opus:
```bash
# Ubuntu/Debian - official repos include it
sudo apt remove ffmpeg
sudo apt install ffmpeg

# Or build from source with --enable-libopus
```

### Out of Memory

If processing large batches causes memory issues:

```bash
# Use smaller batch size
./thinline-radio -migrate_to_opus -migrate_batch_size=25
```

### Database Connection Timeout

For very long migrations, you may need to increase timeout:

```bash
# In postgresql.conf
statement_timeout = 0  # Disable timeout for migration
```

### Disk Space Full

Migration needs temporary disk space for processing:

**Required space:** ~10-20% of current audio size

**Solution:**
```bash
# Check available space
df -h

# Free up space or use smaller batches
./thinline-radio -migrate_to_opus -migrate_batch_size=10
```

## Rollback

If you need to rollback the migration:

### If You Have a Backup

```bash
# Stop server
sudo systemctl stop thinline-radio

# Restore database
psql -U postgres -d thinline < backup_before_opus_20260111.sql

# Restart server
sudo systemctl start thinline-radio
```

### If No Backup (Not Recommended)

You would need to re-encode Opus back to M4A (lossy, not recommended):

```bash
# This is NOT included as it's a lossy conversion
# Always maintain backups!
```

## Performance Impact

- **CPU:** Moderate during migration (one core per FFmpeg process)
- **Memory:** ~50-100 MB per batch being processed
- **I/O:** High database reads/writes
- **Network:** None (all local processing)

**Recommendation:** Run migration during off-peak hours if possible.

## Timeline

For typical deployments:

| Database Size | Calls | Migration Time |
|--------------|-------|----------------|
| 500 MB | 2,000 | ~15 minutes |
| 1 GB | 5,000 | ~45 minutes |
| 5 GB | 25,000 | ~3.5 hours |
| 10 GB | 50,000 | ~7 hours |

Actual time varies based on:
- CPU speed
- Database I/O performance
- Batch size
- Audio file sizes

## Post-Migration

### Update Server Code

The server code has already been updated to encode new calls as Opus.

When you restart the server after migration, all NEW calls will be Opus automatically.

### Mobile App Compatibility

- ✅ Android 5.0+ (2014): Native Opus support
- ✅ iOS 11+ (2017): Native Opus support
- ❌ iOS <11, Android <5: Cannot play Opus

**Expected compatibility:** 99%+ of active users

### Web Client Compatibility

- ✅ Chrome/Edge: Full support
- ✅ Firefox: Full support
- ✅ Safari 14+: Full support
- ⚠️ Safari <14: No support (users should upgrade)

## FAQ

**Q: Can I run the migration while the server is running?**  
A: No. Stop the server first to avoid conflicts.

**Q: Will users lose audio during migration?**  
A: No. Migration updates existing records. No data is deleted.

**Q: What if migration is interrupted?**  
A: Safe to resume. Already-converted calls are skipped automatically.

**Q: Can I migrate only recent calls?**  
A: Current tool migrates all M4A/AAC. For selective migration, modify the SQL query in `migrate_to_opus.go`.

**Q: Does this affect transcriptions?**  
A: No. Transcriptions are text and stored separately.

**Q: Can I undo the migration?**  
A: Only by restoring from backup. The conversion is one-way.

**Q: Will old mobile apps still work?**  
A: Old apps cannot play Opus. Update mobile app first, wait for adoption, then migrate.

## Support

If you encounter issues:

1. Check this guide's troubleshooting section
2. Review migration logs
3. Restore from backup if needed
4. Report issues with:
   - Database size
   - Number of calls
   - Error messages
   - FFmpeg version: `ffmpeg -version`

## Recommended Migration Flow

For zero downtime:

1. ✅ **Week 1:** Update and release mobile app with Opus support
2. ✅ **Week 2-3:** Monitor app adoption (aim for 90%+)
3. ✅ **Week 4:** Schedule migration during maintenance window
   - Announce downtime to users
   - Backup database
   - Stop server
   - Run migration (dry run first)
   - Run actual migration
   - Vacuum database
   - Restart server
4. ✅ **Post-migration:** Monitor for issues
   - Check audio playback
   - Verify storage savings
   - Monitor error logs

---

**Last Updated:** January 2026  
**Thinline Radio Version:** 7.0.0+

