# Opus Audio Configuration Guide

## Quick Start

Add these two lines to your `thinline-radio.ini`:

```ini
# Enable Opus for new calls (default: false)
opus = false

# Migrate existing calls (default: false)
opus_migration = false
```

## Configuration Options

### `opus = true/false`

Controls audio encoding for **NEW calls** coming in.

- **`false`** (default): Encodes as M4A/AAC at 32 kbps
- **`true`**: Encodes as Opus at 16 kbps (50% smaller)

**When to enable:**
1. After updating and releasing mobile app
2. After 90%+ of users have updated their apps
3. When you want to start saving storage on new calls

**Effect:** Only affects NEW calls. Existing calls remain unchanged.

### `opus_migration = true/false`

Converts **ALL EXISTING** M4A/AAC/MP3 calls to Opus format.

- **`false`** (default): No migration
- **`true`**: Runs migration on startup, then exits

**When to enable:**
1. **Only after** updating mobile app
2. **Only after** backing up database
3. **Only once** to migrate existing calls

**Effect:** 
- Server starts migration on launch
- Converts all old calls to Opus
- Server exits after completion
- You must set back to `false` and restart

## Usage Scenarios

### Scenario 1: Enable Opus for New Calls Only

```ini
# Start encoding new calls as Opus
opus = true
opus_migration = false
```

**Result:**
- New calls: Opus (50% smaller)
- Old calls: Still M4A (mixed database)
- Safe, gradual transition

### Scenario 2: Full Opus Migration

```ini
# Step 1: Enable Opus
opus = true
opus_migration = false
```

Restart server, wait a few days, then:

```ini
# Step 2: Migrate old calls (requires stop/start)
opus = true
opus_migration = true
```

Server will migrate and exit. Then:

```ini
# Step 3: Set migration back to false
opus = true
opus_migration = false
```

**Result:**
- All calls: Opus format
- Maximum storage savings
- Consistent database

### Scenario 3: Keep Using M4A (Default)

```ini
# Keep everything as M4A/AAC
opus = false
opus_migration = false
```

**Result:**
- Everything stays M4A
- Backward compatible
- No changes needed

## Migration Process

### Before Migration

1. **Update mobile app** with Opus support
2. **Release to app stores**
3. **Wait for adoption** (check analytics for 90%+ updated)
4. **Backup database:**
   ```bash
   pg_dump -U postgres rdio_scanner > backup_$(date +%Y%m%d).sql
   ```

### During Migration

1. **Stop the server:**
   ```bash
   sudo systemctl stop thinline-radio
   ```

2. **Edit INI file:**
   ```ini
   opus_migration = true
   ```

3. **Start server** (migration runs automatically):
   ```bash
   ./thinline-radio
   ```

4. **Wait for completion** (server exits when done)

5. **Edit INI file again:**
   ```ini
   opus_migration = false
   ```

6. **Vacuum database** (reclaim space):
   ```bash
   psql -d rdio_scanner -c "VACUUM FULL calls;"
   ```

7. **Restart server normally:**
   ```bash
   sudo systemctl start thinline-radio
   ```

### After Migration

Check results:

```sql
SELECT 
    "audioMime",
    COUNT(*) as count,
    pg_size_pretty(SUM(length("audio"))::bigint) as size
FROM "calls"
GROUP BY "audioMime";
```

Expected output:
```
 audioMime  | count | size
------------+-------+------
 audio/opus |  XXXX | XX MB
```

## Compatibility

### Server
- ✅ Requires FFmpeg with libopus support
- ✅ All major Linux distributions
- ✅ macOS (via Homebrew)
- ✅ Windows (FFmpeg builds include Opus)

### Web Client
- ✅ Chrome/Edge/Firefox: Full support
- ✅ Safari 14+: Full support
- ❌ Safari <14: No Opus support (users should upgrade)

### Mobile App
- ✅ Android 5.0+ (99% of devices)
- ✅ iOS 11+ (99% of devices)
- ❌ Older devices cannot play Opus

**Overall:** 99%+ user compatibility

## Storage Savings

Example for 7,053 calls at 98 MB:

| Setting | Format | Size | Savings |
|---------|--------|------|---------|
| `opus = false` | M4A | 98 MB | 0% |
| `opus = true` (new only) | Mixed | 90 MB | 8% (gradual) |
| `opus = true` + migration | Opus | 49 MB | 50% |

## Troubleshooting

### Migration Won't Start

**Check INI file:**
```bash
grep opus /path/to/thinline-radio.ini
```

Should show:
```
opus_migration = true
```

### Server Keeps Running (Doesn't Exit)

Migration only runs if `opus_migration = true`. Check your INI file.

### "FFmpeg does not have libopus encoder"

Install FFmpeg with Opus support:
```bash
sudo apt install ffmpeg  # Ubuntu/Debian
brew install ffmpeg      # macOS
```

### Mobile App Can't Play Audio

Users need the updated app with Opus support. Check:
1. App was rebuilt with Opus detection
2. App was released to stores
3. Users have updated their apps

### Mixed Audio Formats After Migration

This is normal if:
- `opus = true` (new calls are Opus)
- `opus_migration = false` (old calls still M4A)

To fix: Run migration with `opus_migration = true`

## Best Practices

### Recommended Approach

1. **Week 1-2:** Update and test mobile app locally
2. **Week 3:** Release mobile app to app stores
3. **Week 4-5:** Monitor app adoption (aim for 90%+)
4. **Week 6:** Enable `opus = true` (new calls only)
5. **Week 7:** Run migration if desired (`opus_migration = true`)

### Don't Forget

- ✅ Always backup before migration
- ✅ Update mobile app first
- ✅ Wait for user adoption
- ✅ Set `opus_migration = false` after migration
- ✅ Run VACUUM FULL after migration
- ✅ Monitor for playback issues

### Optional: Gradual Rollout

```ini
# Phase 1: New calls only (reversible)
opus = true
opus_migration = false
```

Wait 2-3 weeks, monitor for issues:

```ini
# Phase 2: Migrate old calls (irreversible without backup)
opus = true
opus_migration = true
```

## FAQ

**Q: Can I switch back to M4A after enabling Opus?**  
A: Yes for new calls (set `opus = false`). No for migrated calls (need restore from backup).

**Q: Will this affect transcriptions?**  
A: No. Whisper transcription works with any audio format.

**Q: Do I have to migrate old calls?**  
A: No. You can keep old calls as M4A and only use Opus for new calls.

**Q: How long does migration take?**  
A: ~0.5 seconds per call. For 7,000 calls: ~1 hour.

**Q: What if migration fails halfway?**  
A: Migration is safe to restart. Already-converted calls are skipped.

**Q: Can I run migration on a live server?**  
A: No. Stop the server first to avoid conflicts.

---

**Support:** See `OPUS_MIGRATION.md` for detailed troubleshooting.

