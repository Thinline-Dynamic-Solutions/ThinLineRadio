# Troubleshooting Bad Timestamps in Database

## Problem Description

Bad timestamps in the database can prevent playback from loading. The most common issue is timestamps stored in **microseconds** instead of **milliseconds**, causing year values to exceed the valid range (1-9999).

### Symptoms
- Playback fails to load
- Calls appear to have timestamps far in the future (year 58086+)
- JSON marshaling errors due to invalid year ranges

## Valid Timestamp Range

The system stores timestamps as Unix milliseconds (bigint). Valid range:
- **Minimum**: -62135596800000 ms (year 1)
- **Maximum**: 253402300799999 ms (year 9999)

## Diagnosis Queries

### 1. Find All Bad Timestamps (Comprehensive Check)

```sql
-- Combined query to find all potentially problematic timestamps
SELECT 
    "callId", 
    "timestamp",
    "systemId",
    "talkgroupId",
    CASE 
        WHEN "timestamp" <= 0 THEN 'Zero or Negative'
        WHEN "timestamp" < -62135596800000 THEN 'Year < 1'
        WHEN "timestamp" > 253402300799999 THEN 'Year > 9999 (likely microseconds)'
        WHEN "timestamp" < 946684800000 THEN 'Possibly in seconds (pre-2000)'
        WHEN "timestamp" > (EXTRACT(EPOCH FROM NOW()) * 1000 + 86400000) THEN 'Future timestamp'
        ELSE 'Unknown Issue'
    END as "issue_type",
    to_timestamp("timestamp" / 1000.0) as "converted_time"
FROM "calls"
WHERE 
    "timestamp" <= 0
    OR "timestamp" < -62135596800000
    OR "timestamp" > 253402300799999
    OR "timestamp" < 946684800000
    OR "timestamp" > (EXTRACT(EPOCH FROM NOW()) * 1000 + 86400000)
ORDER BY "timestamp" DESC
LIMIT 1000;
```

### 2. Count Bad Timestamps by Type

```sql
SELECT 
    CASE 
        WHEN "timestamp" <= 0 THEN 'Zero or Negative'
        WHEN "timestamp" < -62135596800000 THEN 'Year < 1'
        WHEN "timestamp" > 253402300799999 THEN 'Year > 9999 (likely microseconds)'
        WHEN "timestamp" < 946684800000 THEN 'Possibly in seconds (pre-2000)'
        WHEN "timestamp" > (EXTRACT(EPOCH FROM NOW()) * 1000 + 86400000) THEN 'Future timestamp'
    END as "issue_type",
    COUNT(*) as "count"
FROM "calls"
WHERE 
    "timestamp" <= 0
    OR "timestamp" < -62135596800000
    OR "timestamp" > 253402300799999
    OR "timestamp" < 946684800000
    OR "timestamp" > (EXTRACT(EPOCH FROM NOW()) * 1000 + 86400000)
GROUP BY "issue_type"
ORDER BY "count" DESC;
```

### 3. Find Timestamps in Microseconds (Year > 9999)

```sql
-- Find calls with timestamps that result in year > 9999
SELECT 
    "callId", 
    "timestamp",
    "systemId",
    "talkgroupId",
    to_timestamp("timestamp" / 1000.0) as "converted_time",
    EXTRACT(YEAR FROM to_timestamp("timestamp" / 1000.0)) as "year"
FROM "calls"
WHERE "timestamp" > 253402300799999
ORDER BY "timestamp" DESC
LIMIT 100;
```

### 4. Check Delayed Table for Bad Timestamps

```sql
-- Check delayed table for bad timestamps
SELECT 
    "delayedId",
    "callId",
    "timestamp" as "bad_timestamp",
    "timestamp" / 1000 as "corrected_timestamp",
    to_timestamp("timestamp" / 1000.0 / 1000.0) as "corrected_time"
FROM "delayed"
WHERE "timestamp" > 253402300799999;
```

## Fix Procedures

### Fix 1: Timestamps in Microseconds (Most Common Issue)

**Step 1: Preview the correction**
```sql
-- Preview what the corrected timestamps would look like
SELECT 
    "callId", 
    "timestamp" as "bad_timestamp",
    "timestamp" / 1000 as "corrected_timestamp",
    to_timestamp("timestamp" / 1000.0 / 1000.0) as "corrected_time",
    EXTRACT(YEAR FROM to_timestamp("timestamp" / 1000.0 / 1000.0)) as "corrected_year"
FROM "calls"
WHERE "timestamp" > 253402300799999
LIMIT 20;
```

**Step 2: Fix the calls table**
```sql
-- Fix timestamps that are in microseconds instead of milliseconds
-- This divides them by 1000 to convert from microseconds to milliseconds
UPDATE "calls"
SET "timestamp" = "timestamp" / 1000
WHERE "timestamp" > 253402300799999;
```

**Step 3: Fix the delayed table (if applicable)**
```sql
-- Fix delayed table timestamps
UPDATE "delayed"
SET "timestamp" = "timestamp" / 1000
WHERE "timestamp" > 253402300799999;
```

**Step 4: Verify the fix**
```sql
-- After running the UPDATE, verify no more bad timestamps exist
SELECT COUNT(*) as "remaining_bad_timestamps"
FROM "calls"
WHERE "timestamp" > 253402300799999;
```

### Fix 2: Delete Unfixable Records (Last Resort)

If timestamps cannot be corrected:
```sql
-- Delete calls with unfixable timestamps
DELETE FROM "calls"
WHERE "timestamp" > 253402300799999;
```

**Note:** This will cascade delete related records in `callPatches`, `callUnits`, and `delayed` tables due to foreign key constraints.

## Prevention

### Root Cause Analysis

If you find bad timestamps, investigate the data source:
1. Check which `systemId` and `talkgroupId` are affected
2. Review the data ingestion code for those sources
3. Verify the timestamp format being sent (should be Unix milliseconds, not microseconds)

### Example from February 2026 Fix

**Problem:** All bad timestamps were from systemId 13, talkgroupId 2130
- Timestamps were stored as `1770859124446000` (microseconds)
- Should have been `1770859124446` (milliseconds)

**Resolution:** 
1. Fixed existing timestamps by dividing by 1000
2. Investigated systemId 13's data source to prevent future occurrences

## Related Code References

- **Database Schema**: `server/postgresql.go` (lines 99-111)
- **Timestamp Validation**: `server/call.go` (lines 743-750)
- **Delay System**: `server/delayer.go`
- **Playback Query**: `server/controller.go` (lines 2569-2577)

## Additional Timestamp Issues

### Zero or Negative Timestamps
```sql
SELECT "callId", "timestamp", "systemId", "talkgroupId"
FROM "calls"
WHERE "timestamp" <= 0
LIMIT 100;
```

### Timestamps in Seconds (Pre-2000)
```sql
SELECT "callId", "timestamp", "systemId", "talkgroupId",
       to_timestamp("timestamp") as "if_seconds",
       to_timestamp("timestamp" / 1000.0) as "if_milliseconds"
FROM "calls"
WHERE "timestamp" > 0 AND "timestamp" < 946684800000
LIMIT 100;
```

### Future Timestamps (More than 1 day ahead)
```sql
SELECT "callId", "timestamp", "systemId", "talkgroupId",
       to_timestamp("timestamp" / 1000.0) as "converted_time",
       ("timestamp" - EXTRACT(EPOCH FROM NOW()) * 1000) / 1000 / 3600 as "hours_in_future"
FROM "calls"
WHERE "timestamp" > (EXTRACT(EPOCH FROM NOW()) * 1000 + 86400000)
ORDER BY "timestamp" DESC
LIMIT 100;
```

## System Behavior with Bad Timestamps

The system has built-in protection against bad timestamps:

```go
// From server/call.go (lines 743-750)
searchResult.Timestamp = time.UnixMilli(timestamp)
if searchResult.Timestamp.Year() < 1 || searchResult.Timestamp.Year() > 9999 {
    // Skip this call - invalid timestamp that will cause JSON marshaling to fail
    calls.controller.Logs.LogEvent(LogLevelWarn, 
        fmt.Sprintf("Skipping call %d with invalid timestamp: %v (year %d out of range)", 
        searchResult.Id, searchResult.Timestamp, searchResult.Timestamp.Year()))
    continue
}
```

This means bad timestamps are automatically skipped in search results, but they can still cause issues with direct playback requests and may accumulate in the database.
