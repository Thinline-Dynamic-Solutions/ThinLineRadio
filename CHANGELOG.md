# Change log

## Version 7.0 Beta 7 - Released TBD

### New Features

- **Opus audio codec support for 50% storage savings**
  - Implemented Opus audio encoding as an alternative to M4A/AAC format
  - Provides 50% storage reduction (16 kbps Opus vs 32 kbps AAC) with same or better voice quality
  - Opus is specifically optimized for voice/dispatch audio with superior low-bitrate performance
  - Storage savings: ~240 KB per minute (M4A) → ~120 KB per minute (Opus)
  - Bitrate: 32 kbps AAC → 16 kbps Opus (50% reduction)
  - Format: M4A container → OGG container with Opus codec
  - Voice-optimized encoding settings (`-application voip`, variable bitrate, max compression)
  - Configurable via `thinline-radio.ini`: `opus = true/false` (default: false for backward compatibility in Beta 7)
  - **Note:** Opus will become the default codec in Beta 8 (M4A/AAC will be deprecated)
  - Only affects NEW calls when enabled - existing calls remain unchanged until migration
  - Browser compatibility: Chrome/Edge/Firefox/Safari 14+ all support Opus natively
  - Mobile app compatibility: Android 5.0+ (99% of devices), iOS 11+ (99% of devices)
  - Web client: No changes needed - browsers automatically decode Opus via AudioContext
  - Mobile app: Added Opus/OGG format detection with magic byte checking (`OggS` header, `OpusHead` marker)
  - Files modified: server/ffmpeg.go, server/tone_detector.go, server/debug_logger.go, server/config.go, server/main.go, server/command.go, ThinlineRadio-Mobile/lib/services/audio_service.dart
  - Files added: server/migrate_to_opus.go, OPUS_IMPLEMENTATION.md, OPUS_CONFIGURATION.md, OPUS_MIGRATION.md

- **Opus migration tool for converting existing audio**
  - Database migration tool to convert all existing M4A/AAC/MP3 calls to Opus format
  - Command-line tool: `./thinline-radio -migrate_to_opus`
  - Batch processing with configurable batch size (default: 100 calls per batch)
  - Dry run mode: `-migrate_dry_run` flag to preview migration without making changes
  - Progress tracking with ETA estimates (~0.5 seconds per call processing time)
  - Error handling and retry logic for failed conversions
  - Statistics and savings reporting (shows total calls, converted count, size reduction)
  - FFmpeg Opus support verification before migration starts
  - Safe to restart - already-converted calls are skipped on re-run
  - Requires server to be stopped (migration runs on startup, then exits)
  - Configuration: `opus_migration = true` in INI file (set back to false after migration)
  - Files added: server/migrate_to_opus.go
  - Documentation: Comprehensive migration guide in OPUS_MIGRATION.md

**Migration Process:**
  - **Prerequisites:** Backup database, ensure FFmpeg has libopus support, stop server
  - **Step 1 - Dry Run:** `./thinline-radio -migrate_to_opus -migrate_dry_run` to preview changes without modifying database
  - **Step 2 - Migration:** `./thinline-radio -migrate_to_opus` to convert all existing audio files
  - **Custom Batch Size:** Use `-migrate_batch_size=50` for smaller batches (less memory) or `-migrate_batch_size=500` for larger batches (faster)
  - **Step 3 - Reclaim Space:** After migration, run `psql -d thinline -c "VACUUM FULL calls;"` to reclaim PostgreSQL disk space
  - **Step 4 - Verify:** Check migration with SQL query: `SELECT "audioMime", COUNT(*) FROM "calls" GROUP BY "audioMime";`
  - **Timeline:** ~15 min for 2,000 calls, ~45 min for 5,000 calls, ~3.5 hours for 25,000 calls
  - **Rollback:** Restore from database backup if needed (migration is one-way conversion)
  - **Important:** Update mobile app first and wait for 90%+ user adoption before migrating existing calls

### Bug Fixes

- **Fixed keyword alerts not respecting alertEnabled preference**
  - Fixed critical bug where keyword alerts were sent to users even when they had disabled alerts for a system/talkgroup
  - Root cause: Keyword alert query was only checking `keywordAlerts = true` but missing the `alertEnabled = true` check
  - Tone alerts correctly checked both `alertEnabled = true AND toneAlerts = true`, but keyword alerts were only checking `keywordAlerts = true`
  - Now keyword alerts properly check `alertEnabled = true AND keywordAlerts = true` to match tone alert behavior
  - Users who disable alerts for a specific system/talkgroup will no longer receive keyword alerts for that combination
  - Alert preferences remain per-system/talkgroup - users can have different alert settings for different systems/talkgroups
  - Files modified: server/transcription_queue.go
  - Addresses issue where users reported receiving keyword alerts despite having all alerts disabled

## Version 7.0 Beta 6 - Released January 10, 2026

### Bug Fixes

- **Fixed invitation codes not working for user registration**
  - Fixed `ValidateAccessCodeHandler` checking for incorrect status: was checking for "active" but invitations are created with "pending" status
  - Fixed `ValidateAccessCodeHandler` incorrectly treating unused invitations as used: database stores usedAt as 0 (not NULL), so added check for > 0
  - Fixed registration form not appearing when clicking email invitation link: auth-screen component wasn't setting `codeValidated = true` after successful validation
  - Users can now successfully register using invitation codes sent via email
  - Added comprehensive logging throughout invitation validation flow for easier debugging
  - Files modified: server/api.go, client/src/app/components/rdio-scanner/auth-screen/auth-screen.component.ts, client/src/app/components/rdio-scanner/user-registration/user-registration.component.ts
  - Addresses issue where fresh invitation codes were incorrectly reported as "already used"

## Version 7.0 Beta 5 - Released January 10, 2026

### New Features

- **Custom prompt support for Whisper transcription**
  - Added prompt field in Admin UI transcription settings (visible for Whisper API provider)
  - Administrators can now provide custom prompts to guide transcription with domain-specific terminology
  - Supports radio codes, phonetic alphabet, unit designations, medical terminology, and formatting preferences
  - Full backend support passing prompts through transcription queue to Whisper service
  - Switched Whisper implementation to OpenAI's official whisper library for native prompt support
  - Added hallucination prevention settings (condition_on_previous_text=False, compression_ratio_threshold, etc.)
  - Tested radio dispatch prompt achieving ~95% accuracy included in Whisper repository documentation
  - Compatible with both local Whisper installations and OpenAI API endpoints
  - Files modified: server/options.go, server/defaults.go, server/transcription_queue.go, server/transcription_whisper_api.go, client admin UI components, whisper service

### Changes

- **Major tone detection improvements for analog conventional channels**
  - Implemented dynamic noise floor estimation using 20th percentile method for adaptive thresholding
  - Added parabolic peak interpolation for sub-bin frequency accuracy (improved from ±3.9 Hz to ±0.5 Hz)
  - Implemented force-split detection with lookahead confirmation to prevent false merges of distinct tones
  - Added bandpass filtering (200-3000 Hz) and dynamic audio normalization in ffmpeg preprocessing
  - Increased frequency merging tolerance from ±15 Hz to ±20 Hz to handle analog channel drift and Doppler effects
  - Dual gating system: frames must pass both global threshold (-28 dB) and SNR above noise floor (+6 dB)
  - Lowered base magnitude threshold from 0.05 to 0.02 (safe due to improved noise gating)
  - Frequency history tracking for better handling of slowly-drifting tones on analog channels
  - Significantly improves detection reliability on analog conventional channels with varying noise levels
  - **Note**: Tone detection feature is still in BETA - these improvements are based on user reports but have not been fully tested on analog channels by the development team (our systems are all digital)
  - Techniques and algorithms inspired by icad_tone_detection project by thegreatcodeholio (Apache 2.0 License)
  - GitHub: https://github.com/thegreatcodeholio/icad_tone_detection
  - Special thanks to thegreatcodeholio for developing icad_tone_detection and providing guidance
  - Files modified: server/tone_detector.go
  - Addresses community reports of poor tone detection performance on analog conventional channels
  - Community testing and feedback welcome to further refine analog channel detection

- **Whisper service improvements**
  - Renamed `whisper.py` to `whisper_server.py` to avoid Python import conflicts
  - Updated dependencies from `transformers` to `openai-whisper` for better prompt support and stability
  - Added proper hallucination detection and prevention mechanisms
  - Improved transcription quality for short audio clips and radio traffic

### Bug Fixes

- **Fixed template compilation error in main component**
  - Removed reference to non-existent `talkgroupId` property in previousCall display template
  - Fixed TypeScript compilation error that prevented client build
  - Files modified: client/src/app/components/rdio-scanner/main/main.component.html

- **Fixed talker alias ingestion not working**
  - Both ParseMultipartContent and ParseTrunkRecorderMeta now properly ingest talker aliases from uploaded calls
  - Added parsing of "tag" field from "sources" and "srcList" arrays in call metadata
  - Tag/alias information is now extracted and stored in call.Meta.UnitLabels
  - Existing controller infrastructure automatically adds/updates unit aliases in the database
  - Fixes issue where trunk-recorder and other upload agents could not provide unit alias information
  - Unit aliases now properly populate and persist in the units database table
  - Files modified: server/parsers.go
  - Thanks to community report for identifying the missing alias ingestion functionality

- **Fixed talkgroup sorting not persisting after save**
  - Fixed bug where manually sorted talkgroups would randomly revert to alphabetical order
  - Root cause: When SortTalkgroups option was enabled, code was modifying the actual talkgroup Order field in the database during config retrieval
  - Talkgroup Order values were being overwritten every time config was sent to clients (on connect, refresh, etc.)
  - Changed behavior: SortTalkgroups option now only affects display order without modifying database values
  - When SortTalkgroups is disabled (default): Custom sort order from admin panel is respected and persisted
  - When SortTalkgroups is enabled: Displays alphabetically by label without changing stored Order values
  - Manual talkgroup sorting in admin panel now properly persists across server restarts and client connections
  - Files modified: server/system.go

- **Added admin-configurable default tag colors**
  - Administrators can now set default colors for tags in the admin panel
  - Color priority hierarchy: User settings > Admin defaults > Hardcoded defaults > White
  - Users can still override admin-set colors in their personal settings
  - Admin colors are stored in the database and synced to all clients
  - Color picker in admin panel provides 9 predefined color options
  - Files modified: server/tag.go, server/migrations.go, server/database.go, client/src/app/components/rdio-scanner/admin/config/tags/, client/src/app/components/rdio-scanner/tag-color.service.ts, ThinlineRadio-Mobile/lib/services/tag_color_service.dart

- **Fixed P25 Phase II simulcast patch calls being dropped**
  - Fixed critical bug where Harris P25 Phase II patched dispatch calls were silently dropped
  - Issue: When dispatcher creates simulcast patch (TGID 64501-64599), system patches multiple talkgroups together (e.g., 1003, 6001)
  - Previous behavior: Call with patch TGID 64501 was dropped because 64501 doesn't exist in configured talkgroups
  - New behavior: System now checks patched talkgroups array and uses first valid configured talkgroup as primary
  - Call is now correctly associated with actual operational talkgroup (e.g., 1003) instead of temporary patch ID
  - Original patch TGID is preserved in patches array for search/display purposes
  - Eliminates need to manually add all 99 potential patch TGIDs (64501-64599) as workaround
  - All priority/emergency dispatch calls now properly recorded and displayed
  - Patched talkgroups are validated against blacklists to honor system restrictions
  - Three strategic checkpoints added throughout call ingestion process:
    1. Early check after initial talkgroup lookup
    2. Re-check after auto-populate creates new systems/talkgroups
    3. Final check before call write to prevent dropping valid patched calls
  - Compatible with Trunk Recorder's `patched_talkgroups` field format
  - Existing livefeed patch display logic now works correctly since calls no longer dropped
  - Files modified: server/controller.go
  - Thanks to user report for detailed analysis of Harris P25 patch behavior

## Version 7.0 Beta 4 - Released January 3, 2026

### Bug Fixes & Improvements

- **Fixed talker alias ingestion not working**
  - Both ParseMultipartContent and ParseTrunkRecorderMeta now properly ingest talker aliases from uploaded calls
  - Added parsing of "tag" field from "sources" and "srcList" arrays in call metadata
  - Tag/alias information is now extracted and stored in call.Meta.UnitLabels
  - Existing controller infrastructure automatically adds/updates unit aliases in the database
  - Fixes issue where trunk-recorder and other upload agents could not provide unit alias information
  - Unit aliases now properly populate and persist in the units database table
  - Files modified: server/parsers.go
  - Thanks to community report for identifying the missing alias ingestion functionality

- **Fixed talkgroup sorting not persisting after save**
  - Fixed bug where manually sorted talkgroups would randomly revert to alphabetical order
  - Root cause: When SortTalkgroups option was enabled, code was modifying the actual talkgroup Order field in the database during config retrieval
  - Talkgroup Order values were being overwritten every time config was sent to clients (on connect, refresh, etc.)
  - Changed behavior: SortTalkgroups option now only affects display order without modifying database values
  - When SortTalkgroups is disabled (default): Custom sort order from admin panel is respected and persisted
  - When SortTalkgroups is enabled: Displays alphabetically by label without changing stored Order values
  - Manual talkgroup sorting in admin panel now properly persists across server restarts and client connections
  - Files modified: server/system.go

- **Added admin-configurable default tag colors**
  - Administrators can now set default colors for tags in the admin panel
  - Color priority hierarchy: User settings > Admin defaults > Hardcoded defaults > White
  - Users can still override admin-set colors in their personal settings
  - Admin colors are stored in the database and synced to all clients
  - Color picker in admin panel provides 9 predefined color options
  - Files modified: server/tag.go, server/migrations.go, server/database.go, client/src/app/components/rdio-scanner/admin/config/tags/, client/src/app/components/rdio-scanner/tag-color.service.ts, ThinlineRadio-Mobile/lib/services/tag_color_service.dart

- **Fixed P25 Phase II simulcast patch calls being dropped**
  - Fixed critical bug where Harris P25 Phase II patched dispatch calls were silently dropped
  - Issue: When dispatcher creates simulcast patch (TGID 64501-64599), system patches multiple talkgroups together (e.g., 1003, 6001)
  - Previous behavior: Call with patch TGID 64501 was dropped because 64501 doesn't exist in configured talkgroups
  - New behavior: System now checks patched talkgroups array and uses first valid configured talkgroup as primary
  - Call is now correctly associated with actual operational talkgroup (e.g., 1003) instead of temporary patch ID
  - Original patch TGID is preserved in patches array for search/display purposes
  - Eliminates need to manually add all 99 potential patch TGIDs (64501-64599) as workaround
  - All priority/emergency dispatch calls now properly recorded and displayed
  - Patched talkgroups are validated against blacklists to honor system restrictions
  - Three strategic checkpoints added throughout call ingestion process:
    1. Early check after initial talkgroup lookup
    2. Re-check after auto-populate creates new systems/talkgroups
    3. Final check before call write to prevent dropping valid patched calls
  - Compatible with Trunk Recorder's `patched_talkgroups` field format
  - Existing livefeed patch display logic now works correctly since calls no longer dropped
  - Files modified: server/controller.go
  - Thanks to user report for detailed analysis of Harris P25 patch behavior

## Version 7.0 Beta 4 - Released January 3, 2026

### Bug Fixes & Improvements

- **Fixed email case sensitivity causing duplicate accounts and login issues**
  - Emails are now normalized to lowercase during registration and login
  - Users can log in with any capitalization (user@email.com, USER@email.com, User@Email.com all work)
  - Prevents duplicate accounts with same email but different capitalization
  - Added startup check that creates `duplicate_emails.log` if existing duplicates are found
  - Log file provides detailed information about duplicate accounts for manual resolution
  - Backwards compatible: existing accounts continue to work, duplicates logged for manual cleanup
  - Files modified: server/validation.go, server/user.go, server/api.go, server/admin.go, server/controller.go
  - Files added: server/duplicate_email_check.go

- **Fixed Docker image not reading environment variables for database configuration**
  - Added `docker-entrypoint.sh` script to properly convert environment variables to command-line flags
  - Docker image now correctly accepts DB_HOST, DB_PORT, DB_NAME, DB_USER, DB_PASS environment variables
  - Required environment variables are validated on startup with helpful error messages
  - Supports optional configuration: LISTEN, SSL_LISTEN, SSL_CERT_FILE, SSL_KEY_FILE, SSL_AUTO_CERT, BASE_DIR
  - Fixes "connection refused" and "database: failed to connect to host=localhost" errors when using Docker
  - Files added: docker-entrypoint.sh
  - Files modified: Dockerfile

- **Fixed Apache reverse proxy causing mixed content errors with HTTPS**
  - Updated example Apache configuration to properly pass X-Forwarded-Proto header
  - Server now correctly detects HTTPS when behind SSL-terminating proxy
  - Fixes blank page at root URL while /index.html worked
  - Fixes base href being set to http:// instead of https:// causing CSS/JS to fail loading
  - Files modified: docs/examples/apache/.htaccess

- **Fixed Whisper transcription connection failures**
  - Implemented automatic retry logic with exponential backoff (up to 3 retries)
  - Enhanced HTTP connection pooling with proper timeout and keep-alive settings
  - Added connection pool configuration: 100 max idle connections, 20 max per host
  - Configured proper timeouts: 30s connection, 30s response headers, 90s idle
  - Improved error detection and logging for connection-related failures
  - Retry delays: 1s, 2s, 4s with automatic retry on transient network errors
  - Added detailed troubleshooting documentation (docs/WHISPER-TROUBLESHOOTING.md)
  - Fixes errors: "connection forcibly closed", "EOF", "wsarecv" network errors
  - Files modified: server/transcription_whisper_api.go, server/transcription_queue.go

- **Fixed dirwatch validation failure for default and dsdplus types**
  - Fixed critical bug where dirwatch configurations with `systemId` and `talkgroupId` would fail validation with "no talkgroup" error
  - Root cause: `ingestDefault()` and `ingestDSDPlus()` only set `call.Meta.*` fields, but `call.IsValid()` checks top-level `call.SystemId` and `call.TalkgroupId` fields
  - Now correctly sets both Meta fields and top-level fields when dirwatch config has systemId/talkgroupId
  - Includes overflow protection for 32-bit systems
  - Affects: dirwatch type "default" and "dsdplus" (trunk-recorder and sdr-trunk were not affected)
  - Files modified: server/dirwatch.go
  - Thanks to Dustin Holbrook for detailed bug report and analysis

- **Fixed talkgroup sorting persistence issue**
  - Fixed bug where talkgroup order would randomly revert after saving
  - Root cause: Display used sorted getter but underlying FormArray order wasn't updated during drag-and-drop
  - Now properly reorders the FormArray itself when dragging talkgroups, ensuring sort persists on save
  - Files modified: client/src/app/components/rdio-scanner/admin/config/systems/system/system.component.ts

- **Fixed talkgroup access control not being enforced**
  - Fixed critical bug where group/user talkgroup restrictions were ignored - users could see all talkgroups in a system
  - Two separate issues fixed:
    1. Call filtering: `controller.userHasAccess()` now checks group talkgroup access, not just system access
    2. Config filtering: `systems.GetScopedSystems()` now filters talkgroups based on group restrictions
  - Group permissions establish baseline, user permissions can further restrict
  - Files modified: server/controller.go, server/system.go

- **Added "Sort A-Z" button for talkgroups**
  - New button to alphabetically sort all talkgroups in a system with one click
  - Properly updates both order values and FormArray ordering for persistence
  - Button disabled when no talkgroups exist
  - Files modified: client/src/app/components/rdio-scanner/admin/config/systems/system/system.component.html, system.component.ts

- **Added toggle Select/Unselect All button for talkgroups in user groups**
  - Button dynamically changes between "Select All Talkgroups" and "Unselect All Talkgroups" based on current state
  - Icon updates accordingly (select_all vs deselect)
  - Provides one-click selection/deselection of all talkgroups when configuring group access
  - Files modified: client/src/app/components/rdio-scanner/admin/config/user-groups/user-groups.component.html, user-groups.component.ts

- **Added descriptive validation error messages in systems config**
  - Error messages now display next to red (!) icons showing exactly what's wrong
  - System-level errors: "Label required", "System ID required", "Duplicate system ID", "X invalid talkgroups", etc.
  - Talkgroup errors: "ID required", "Duplicate ID", "Label required", "Name required", "Group required", "Tag required"
  - Only shows specific validation errors, no generic messages
  - Files modified: client/src/app/components/rdio-scanner/admin/config/systems/systems.component.html, systems.component.ts, system/system.component.html, system/system.component.ts

- **Enhanced API call upload logging and diagnostics**
  - Added detailed stack trace logging for incomplete call data uploads
  - Now logs all call metadata when SDRTrunk or other sources send incomplete data
  - Includes: SystemId, TalkgroupId, Audio length, Timestamp, SiteRef, Frequency, Units, Patches
  - Also logs remote address and User-Agent for troubleshooting connection issues
  - Test connections now explicitly logged with test connection indicator
  - Example incomplete data log:
    ```
    api: INCOMPLETE CALL DATA RECEIVED:
      Error: no talkgroup
      SystemId: 12345
      TalkgroupId: 0
      Audio Length: 45632 bytes
      Timestamp: 2025-01-03 12:34:56
      SiteRef: 1
      Frequency: 851025000
      Remote Address: 192.168.1.100:54321
      User-Agent: SDRTrunk/0.6.0
    ```

- **Enhanced API key logging and diagnostics**
  - Added detailed logging when API keys are loaded on server startup
  - Now shows total count, enabled count, and disabled count
  - Added logging when API keys are saved: `Apikeys.Write: successfully saved X API keys to database`
  - Displays warning message when no API keys are found (upload sources won't be able to connect)
  - Example log: `Apikeys.Read: loaded 3 total API keys (2 enabled, 1 disabled)`
  - **Note**: API keys do NOT have foreign key cascade constraints, so they will NOT be automatically deleted when other data changes
  - **API keys ARE persisted to database** - they are saved via INSERT/UPDATE statements and loaded on every server restart
  
- **Enhanced device token logging and diagnostics**
  - Added detailed logging when device tokens are loaded on server startup
  - Now shows total token count and number of users with registered devices
  - Displays warning message when no tokens are found (helpful for troubleshooting)
  - Added deletion logging to track when and why device tokens are removed
  - Example logs: `DeviceTokens.Load: loaded 15 total device tokens for 8 users`
  
- **Device Token Cascade Delete Documentation**
  - **IMPORTANT**: Device tokens are automatically deleted when a user account is deleted
  - This is enforced by a foreign key constraint: `ON DELETE CASCADE`
  - If users report losing device tokens after server restart/update, possible causes:
    1. User accounts were deleted or modified during maintenance
    2. Database was restored from a backup that didn't include device tokens
    3. Connected to wrong database instance (test vs production)
  - Device tokens must be re-registered by users in the mobile app after such events
  - Server logs will now clearly show: `DeviceTokens.Load: WARNING - No device tokens found in database`

## Version 7.0 Beta 3 - January 2, 2025

### User Registration Improvements
- **Simplified registration mode settings**
  - Removed confusing "Enable User Registration" toggle (now always enabled)
  - Replaced "Enable Public Registration" with clear "Registration Mode" dropdown
  - Two modes: "Invite Only" (requires code) or "Public Registration" (anyone can sign up)
  - "Public Registration" option automatically disabled until a Public Registration Group is created
  - Context-sensitive hints explain what each mode does

- **Enhanced invite-only security**
  - Users must validate their invitation/registration code BEFORE seeing the form
  - Code validation gateway prevents unauthorized form access
  - Yellow notice displays: "Registration is by invitation only"
  - After successful validation, green success message: "Invite Code Validated, Please Fill Out the Form"
  - All public group information (pricing, channels) hidden in invite-only mode
  - No API calls made for public data when in invite-only mode

- **New backend endpoints**
  - `/api/registration-settings` - Returns current registration mode (public/invite-only)
  - `/api/user/validate-access-code` - Validates invitation or registration codes before form access
  - Validates both invitation codes and registration codes
  - Checks expiration, usage status, and activation status
  - Returns group information upon successful validation

- **Improved user experience**
  - Default registration mode is now "Invite Only" (more secure)
  - Clear error messages for invalid or expired codes
  - Pre-fills email if provided in invitation
  - Seamless flow from code validation to form completion
  - Works on both main registration page and auth screen

- **Files modified**
  - Frontend: auth-screen component, user-registration component
  - Backend: api.go, main.go, options.go, defaults.go

### Docker Support (UNTESTED)
- **Complete Docker deployment solution added**
  - Multi-stage Dockerfile for optimized builds (Node.js → Go → Alpine)
  - docker-compose.yml with PostgreSQL 16 orchestration
  - Automatic FFmpeg installation for audio processing
  - Non-root user (UID 1000) for security
  - Health checks and automatic restarts
  - Volume persistence for data (postgres, audio files, logs)
  - Environment variable configuration via .env file
  - Support for all ThinLine Radio features (transcription, email, SSL, billing)

- **Docker Compose variants**
  - docker-compose.prod.yml: Production-optimized configuration
  - docker-compose.dev.yml: Development configuration with Adminer

- **Helper scripts**
  - docker-deploy.sh: Interactive deployment wizard
  - docker-test.sh: Automated test suite (15 tests)

- **Comprehensive documentation**
  - DOCKER.md: Quick start guide (5-minute setup)
  - docker/README.md: Complete deployment guide (~50 KB)
  - docker/TROUBLESHOOTING.md: Troubleshooting guide with 10+ scenarios
  - docker/config/README.md: SSL, transcription, and secrets configuration
  - docker/init-db/README.md: Database initialization guide
  - DOCKER-IMPLEMENTATION.md: Technical implementation details
  - DOCKER-CHECKLIST.md: Step-by-step deployment checklist

- **CI/CD integration**
  - GitHub Actions workflow for automated Docker Hub publishing
  - Multi-platform builds (linux/amd64, linux/arm64)
  - Security scanning with Trivy

- **Database initialization**
  - Example custom indexes for performance optimization
  - Support for custom SQL scripts on first startup

- **Updated files**
  - .gitignore: Added Docker-specific exclusions
  - README.md: Added Docker quick start section

⚠️ **IMPORTANT NOTE**: This Docker implementation is **UNTESTED** and provided as-is. While comprehensive documentation and automated tests are included, the solution has not been tested in a live environment. Users should test thoroughly in development before deploying to production.

### Scanner Customization Mode
- **New full-screen customization interface for scanner layout**
  - Accessible via floating "Customize Layout" button (dashboard_customize icon)
  - Modern blue color scheme (#64b5f6) with high contrast for better visibility
  - Full-screen modal overlay with organized control sections
  - All preferences automatically saved to localStorage

- **Layout mode toggle**
  - **Horizontal (Side-by-Side)**: Scanner and alerts panel displayed side-by-side
  - **Vertical (Stacked)**: Scanner on top, alerts panel below, centered on screen
  - Perfect for different screen sizes and user preferences

- **Panel positioning controls**
  - Swap panels button to change which side scanner/alerts appear on (horizontal mode)
  - Improved button styling with accent color for better visibility

- **Dynamic panel width adjustment**
  - Scanner width: Adjustable from 400px to 800px with live slider
  - Alerts width: Adjustable from 300px to 600px with live slider
  - Fixed alerts width slider to actually apply changes
  - In vertical mode, both panels use full width (up to 800px max)

- **Button visibility customization**
  - Click any control button in edit mode to show/hide it
  - Hidden buttons display with dashed border and "eye-off" icon
  - Visible buttons show with solid styling and "eye" icon
  - All 12 control buttons customizable: Live Feed, Pause, Replay Last, Skip Next, Avoid, Favorite, Hold System, Hold Talkgroup, Playback, Alerts, Settings, Channel Select

- **Live preview mode**
  - Preview button in edit header to see changes in real-time
  - Dark overlay completely disappears in preview mode
  - Control panel hides, showing only the top bar with controls
  - Smooth transitions between edit and preview states

- **Persistent preferences**
  - Layout mode (horizontal/vertical)
  - Panel positions and widths
  - Button visibility states
  - All saved to localStorage for consistent experience
  - Reset button to restore default settings

### Alerts Panel Enhancements
- **Conditional alerts display based on transcription settings**
  - Alerts button and Recent Alerts panel automatically hidden when transcription is disabled in admin settings
  - Server now sends `transcriptionEnabled` flag via WebSocket configuration
  - Scanner layout automatically centers when alerts panel is hidden

- **User-controlled alerts panel visibility**
  - New hide/show button in alerts panel header (eyeball icon instead of X)
  - Floating "Show Alerts" button appears when panel is hidden
  - Preference saved to localStorage

### User Registration & Authentication
- **Unified invite/registration code experience**
  - Merged separate "Invitation Code" and "Registration Code" fields into single "Invite Code" field
  - Backend intelligently determines code type (invitation vs registration)
  - Maintains backward compatibility with existing invitation and registration systems
  - Clearer user experience with single field instead of confusing dual fields
  - Icon changed from key to mail icon for better representation

- **Fixed sign-up page scroll issue**
  - Changed auth screen alignment from center to flex-start
  - Added padding-top to allow scrolling when content exceeds viewport
  - Improves accessibility on smaller screens

### Branding & UI Enhancements
- **Browser tab titles now show branding**
  - Main scanner page shows: `TLR-{Branding}` (or `TLR-ThinLine Radio` if no branding configured)
  - Admin page shows: `Admin-{Branding}` (or `Admin-TLR` if no branding configured)
  - Dynamically updates based on configured branding in options
  - Makes it easier to identify multiple instances in browser tabs

- **Favicon now uses email logo**
  - Favicon automatically uses uploaded email logo if available
  - Falls back to default ThinLine Radio icon if no logo uploaded
  - Applies to all icon sizes (16x16, 32x32, 192x192)
  - Provides consistent branding across browser tab and bookmarks

### User Groups - System Access & Delays UI Improvements
- **Simplified system/talkgroup selection interface**
  - Removed confusing "Enable talkgroup-level selection" checkbox toggle
  - System access now always shows talkgroup options when a system is selected
  - Cleaner, more intuitive UI with single workflow instead of two modes
  - Talkgroups populate immediately upon system selection (no more double-clicking required)

- **Fixed talkgroup selection not populating**
  - Added `(ngModelChange)` event handlers to trigger Angular change detection
  - System selection now immediately displays available talkgroups
  - Fixed same issue in talkgroup delay configuration section
  - Corrected property names (`systemRef` instead of `system.id`, `talkgroupRef` instead of `talkgroup.id`)


### Downstreams - Name Field
- **Added optional name field to downstreams**
  - Give friendly names to downstream instances (e.g., "Backup Server", "Secondary Instance")
  - Expansion panel header now shows: "Name - URL" or just "URL" if no name provided
  - Backend: Added `name` column to `downstreams` table with automatic migration
  - Frontend: New text input field in downstream configuration form
  - Makes it easier to identify and manage multiple downstream connections

### System Health Dashboard - Configurable Thresholds
- **Added configurable tone detection issue threshold**
  - Tone detection monitoring threshold is now configurable (default: 5 calls)
  - Previously hardcoded to 5 calls; now adjustable in system health dashboard settings
  - Alerts trigger when a talkgroup with tone detection enabled has threshold number of calls with no tones detected in 24 hours
  - Backend: Added `ToneDetectionIssueThreshold` field to `Options` struct
  - Backend: New API endpoint `/api/admin/tone-detection-issue-threshold` for getting/setting threshold
  - Frontend: New setting in system health dashboard with inline editing capability
  - Consistent with existing transcription failure threshold configuration

- **Enhanced system health settings section**
  - System health dashboard now includes three configurable settings:
    - Transcription Failure Threshold (alerts when failures exceed count in 24 hours)
    - Tone Detection Issue Threshold (alerts when talkgroups have calls with no tones)
    - Alert Retention Days (how long system alerts are kept before deletion)
  - All settings support inline editing with save/cancel buttons
  - Settings automatically reload after changes to reflect new values

### Tone Detection & Transcription Optimization
- **Tone detection now runs BEFORE transcription decision** - Major optimization to prevent wasting API calls
  - Tone detection completes first (typically 100-500ms), then decides whether to queue transcription
  - Calculates remaining audio duration after tone removal before sending to transcription API
  - Skips transcription if remaining audio < 2 seconds (likely tone-only, no voice content)
  - Saves significant API costs by avoiding transcription of calls that are 85%+ tones
  - Example: 8.1s of tones in 9.5s audio = 1.4s remaining → transcription skipped

- **Fixed tone duration logic** - Now respects user-configured min/max durations
  - Removed hardcoded tone duration thresholds (e.g., "Long tones > 3 seconds")
  - Now properly uses `MinDuration` and `MaxDuration` from tone set configuration for A-tones, B-tones, and Long-tones
  - Added `ToneType` field to track which type was matched ("A", "B", "Long")
  - More accurate tone detection based on user's actual pager settings

- **Enhanced tone removal before transcription** - Prevents Whisper hallucinations
  - Tones are removed from audio file before sending to transcription API
  - Eliminates transcribed artifacts like "BEEP", "BOOP", "doot doot", etc.
  - Uses ffmpeg `atrim` and `concat` filters to surgically remove tone segments
  - Preserves voice content while eliminating tone interference

### Pre-Alert System
- **Immediate pre-alert notifications** - Users notified as soon as tones are detected
  - Pre-alerts sent instantly when tones match a tone set (before transcription starts)
  - Allows users to tune in faster without waiting for transcription to complete
  - Pre-alert notification format: `TONE SET Tones Detected @ 3:04 PM` (includes timestamp in 12-hour format)
  - Separate alert type (`pre-alert`) created in database for tracking
  - Full tone alert sent later after transcription confirms voice content

### Pending Tone Management
- **Fixed unrelated tone sequences merging** - Prevents incorrect tone attachments
  - Added `Locked` field to pending tone sequences
  - Pending tones are locked when voice call starts transcription
  - New tone-only calls cannot merge with locked pending tones (stored in "next" slot instead)
  - Prevents race condition where unrelated tones merge during slow transcription

- **Added age check for pending tone merging** - Prevents stale tone combinations
  - Reduced pending tone timeout from 5 minutes to 2 minutes
  - New tone-only calls check age of existing pending tones before merging
  - If existing pending tones are older than timeout (2 min), they're replaced instead of merged
  - Prevents unrelated incidents from merging together (e.g., tones 3+ minutes apart)
  - Logs: `existing pending tones for call X are too old (3.5 minutes), replacing with new tones`

### Alert System Improvements
- **Fixed tone set filtering** - Users now properly receive only selected tone sets
  - Added extensive debug logging throughout alert preference chain (frontend → API → backend)
  - Fixed API to always store empty array `[]` instead of `null` when no tone sets selected
  - Backend treats `null`, `""`, and `[]` as "alert for all tone sets"
  - Mobile app (Flutter) and web client (Angular) now properly send tone set selections
  - Debug logs show: `user X has selected specific tone sets: [id1, id2, ...]` or `user X wants ALL tone sets (none selected)`

- **Enhanced alert filtering logic**
  - Pre-alerts and tone alerts both respect user's tone set selections
  - Clear logging when user is skipped: `user X SKIPPED for 'Brookfield' (not in selected tone sets)`
  - Clear logging when user gets alert: `user X gets alert for 'Liberty Duty' (selected this tone set)`

### Performance & Reliability
- **Optimized transcription worker logic**
  - Transcription workers now check remaining audio duration after tone removal
  - Mark calls as transcription completed if mostly tones (prevents pending tones from waiting forever)
  - Better handling of tone-heavy audio files
  - Reduced unnecessary transcription queue entries

- **Improved logging**
  - Added detailed logs for tone detection: `tone detection: analyzed X samples at 16000 Hz, found X potential tone detections`
  - Added logs for remaining audio calculation: `call X has sufficient remaining audio after tone removal (8.0s of 11.0s total, 3.0s tones)`
  - Added logs for pending tone lifecycle: stored, merged, locked, replaced, attached
  - Added debug emoji indicators: 🔔 for tone set matching, 💾 for preference saves

### Bug Fixes
- Fixed compilation errors in tone detection and alert engine
- Fixed pending tone timeout not being respected during merge operations
- Fixed transcription status updates for tone-only calls
- Fixed race condition in pending tone management during concurrent transcriptions

---

## Version 7.0 Beta 2 - December 28, 2024

### Build System Fixes
- **Fixed missing Angular component files**: Added `config-sync` component files that were accidentally excluded from Git repository
  - Fixed `.gitignore` rule that was too broad (`config-sync/` → `/config-sync/`)
  - Users building from source no longer get "Module not found: Error: Can't resolve './tools/config-sync/config-sync.component'" error
  - Added `config-sync.component.ts`, `config-sync.component.html`, and `config-sync.component.scss` to repository

### Email & Configuration
- **Re-added SMTP email support** alongside existing Mailgun and SendGrid providers
  - Full TLS/SSL encryption support
  - Admin UI configuration for SMTP host, port, username, password
  - Option to skip certificate verification for self-signed certificates
  - Fixed SMTP configuration not saving properly in admin panel

### Radio Reference Import
- Fixed duplicate key errors during talkgroup imports
- Fixed groups and tags not being created/saved during imports
- Added support for updating existing talkgroups while preserving custom settings (tones, alerts, delays)
- Implemented upsert logic for sites (update existing or create new based on Radio Reference ID)
- Added automatic config reload after import to sync database-assigned IDs
- Added support for selecting multiple talkgroup categories simultaneously
- Sorted talkgroup categories alphabetically for easier navigation
- Added visual separators between dropdown options for improved readability
- Improved import success messaging with created/updated counts

### SDRTrunk Compatibility
- Fixed SDRTrunk 0.6.0 test connection compatibility issue
- Removed noisy test connection logs (now handled silently)
- Added detailed diagnostic logging for incomplete call data uploads
- Fixed talkgroup parsing to allow `talkgroup=0` for test connections

### Database & Performance
- **Removed MySQL/MariaDB support** - PostgreSQL is now the only supported database
  - Deleted `mysql.go` and all MySQL/MariaDB-specific code
  - PostgreSQL provides better concurrency, performance, and reliability for real-time operations
  - See migration guide in documentation for upgrading from MySQL/MariaDB
- **Dramatically improved call search performance** - Added composite index for 420x speed improvement
  - Added composite index `callUnits_callId_idx` on `callUnits` table for `(callId, offset)`
  - Reduced search query execution time from 23+ seconds to ~55ms (420x faster)
  - Fixed N+1 query problem in call search where correlated subquery was performing 201 sequential scans
  - Especially beneficial for mobile app call history searches
- Added automatic PostgreSQL sequence reset to prevent duplicate key errors
- Fixed sequence detection for case-sensitive table names (userGroups, registrationCodes)
- Sequences now automatically reset to MAX(id) + 1 on server startup
- Prevents duplicate key violations when creating new API keys, talkgroups, groups, tags, etc.

---

## Latest Updates

### December 28, 2024

**Email & Configuration:**
- Re-added SMTP email support alongside existing Mailgun and SendGrid providers
- Fixed SMTP configuration not saving properly in admin panel

**Radio Reference Import:**
- Fixed duplicate key errors during talkgroup imports
- Fixed groups and tags not being created/saved during imports
- Added support for updating existing talkgroups while preserving custom settings (tones, alerts, delays)
- Implemented upsert logic for sites (update existing or create new based on Radio Reference ID)
- Added automatic config reload after import to sync database-assigned IDs
- Added support for selecting multiple talkgroup categories simultaneously
- Sorted talkgroup categories alphabetically for easier navigation
- Added visual separators between dropdown options for improved readability
- Improved import success messaging with created/updated counts

**SDRTrunk Compatibility:**
- Fixed SDRTrunk 0.6.0 test connection compatibility issue
- Removed noisy test connection logs (now handled silently)
- Added detailed diagnostic logging for incomplete call data uploads
- Fixed talkgroup parsing to allow `talkgroup=0` for test connections

**Database & Performance:**
- **Removed MySQL/MariaDB support** - PostgreSQL is now the only supported database
- **Dramatically improved call search performance** - 420x faster with new composite index
  - Reduced search query execution time from 23+ seconds to ~55ms
  - Added composite index on `callUnits` table for `(callId, offset)`
  - Fixed N+1 query problem causing 201 sequential scans
- Added automatic PostgreSQL sequence reset to prevent duplicate key errors
- Fixed sequence detection for case-sensitive table names (userGroups, registrationCodes)
- Sequences now automatically reset to MAX(id) + 1 on server startup
- Prevents duplicate key violations when creating new API keys, talkgroups, groups, tags, etc.

## Version 7.0

**Make sure to backup your config and your database before updating to Version 7.0.**

### Core Version 7.0 Features (Original Rdio Scanner Project)

- New database schema now compatible with PostgreSQL. 

**SQLite Support Removed:**
SQLite support has been removed in v7 due to fundamental architectural limitations that make it unsuitable for Rdio Scanner's production workloads, even in v6. SQLite suffers from:
- **Database locking issues**: SQLite uses file-level locking which causes frequent "database is locked" errors when multiple processes or concurrent operations attempt to access the database simultaneously. This is particularly problematic with Rdio Scanner's high-concurrency architecture where multiple clients, call ingestion, transcription processing, alert engines, and admin operations all need simultaneous database access.
- **SQL_BUSY errors**: Under load, SQLite frequently returns SQL_BUSY errors when write operations conflict with reads, causing call ingestion failures, search timeouts, and client connection issues. This was a persistent problem even in v6 with the simpler architecture.
- **Performance limitations**: SQLite is designed for single-user or low-concurrency applications. Rdio Scanner's real-time nature requires high-throughput database operations (hundreds of calls per minute, simultaneous client queries, alert processing, transcription storage) which SQLite cannot handle efficiently. The lack of proper connection pooling and concurrent write support creates severe bottlenecks.
- **No true concurrent writes**: SQLite only allows one writer at a time, which creates contention when multiple systems are ingesting calls, storing transcriptions, updating user preferences, and processing alerts simultaneously.
- **File-based architecture**: The file-based nature of SQLite makes it unsuitable for distributed or high-availability deployments, and creates I/O bottlenecks that proper database servers avoid through optimized memory management and connection pooling.

For production deployments, PostgreSQL is required and provides proper concurrent access, connection pooling, and performance characteristics needed for Rdio Scanner's real-time, multi-user architecture.
- New Delayed feature which allows to delay ingested audio broadcasting for a specified amount of minutes.
- New alert sounds that can be assigned to groups, tags, systems and talkgroups.
- New system and talkgroup types to help identify the radio system.
- Talkgroups can now be assigned to more than one group.
- LED colors can now be assigned to groups, tags, systems and talkgroups.
- Better call duplicates detection, thanks to the new database schema.
- Tags toggle removed in favor of multi groups assignment for talkgroups.
- AFS systems option remove and replace by system/talkgroup type provoice.
- Newer API while retaining backward compatility.
- Integrated web app migrated to Angular 15.
- Simplified talkgroup importation to a specific system.
- New /reset url path that allow reseting the user access code and talkgroups selection.
- New #UNITLBL metatag for dirwatch.

---

## THINLINE DYNAMIC SOLUTIONS ENHANCEMENTS & ADDITIONS

The following features and fixes were added by Thinline Dynamic Solutions to the base Rdio Scanner v7.0:

### Latest Updates (December 28, 2024)

**New Features:**
- **SMTP Email Support Re-added**: Direct SMTP email provider support restored alongside SendGrid and Mailgun with full TLS/SSL encryption support and admin UI configuration
- **Interactive Setup Wizard**: Added comprehensive interactive setup wizard for first-time installation
  - Automatically detects if PostgreSQL is installed locally
  - Guides users through PostgreSQL installation with platform-specific instructions
  - Supports both local PostgreSQL setup (auto-creates database and user) and remote PostgreSQL server configuration
  - Generates configuration file automatically based on user inputs
  - Beautiful ASCII art radio scanner display with Ohio MARCS-IP branding
  - Handles incomplete or missing configuration files gracefully
  
- **Database Performance Optimization**: Dramatically improved mobile app search performance
  - Added composite index `callUnits_callId_idx` on `callUnits` table for `(callId, offset)`
  - Reduced search query execution time from 23+ seconds to ~55ms (420x faster)
  - Fixed N+1 query problem in call search where correlated subquery was performing 201 sequential scans
  - Migration system ensures index is applied to both new installations and existing databases

**Documentation Updates:**
- Updated all platform build scripts (Linux, Windows, macOS) to include Interactive Setup Wizard documentation
- Enhanced README.md with clear setup options (Interactive Wizard vs Manual)
- Updated `docs/setup-and-administration.md` with comprehensive wizard documentation
- All distribution packages now include updated setup instructions with both local and remote PostgreSQL options

**Bug Fixes:**
- Fixed invalid user account creation and last login dates showing "invalid date" or "1/1/2000"
  - Added migration to fix existing users with empty or invalid timestamps
  - CreatedAt and LastLogin fields now properly initialized with Unix timestamps
  - API returns `null` instead of `0` for never-logged-in users
  - Fixed NewUser() constructor to set proper default timestamps
- Fixed SDR Trunk auto-import issue where talkgroup name field was incorrectly set to talkgroup ID instead of label
  - Talkgroup name now properly uses label as fallback when name field is not provided
- Fixed Trunk Recorder radio ID -1 (unknown radio) causing database errors
  - Added validation to skip invalid unitRef values that exceed PostgreSQL bigint limits
  - -1 values from Trunk Recorder no longer cause "bigint out of range" errors
- Removed Cloudflare Turnstile CAPTCHA requirement from relay server registration (API key request)
  - Removed Turnstile widget and validation from frontend API key request dialog
  - Removed backend CAPTCHA verification check (controlled by turnstile_secret_key config)
  - Simplified registration flow for relay server connections

### Major Feature Additions

**User Account & Authentication System**
- Complete user registration and authentication system (email/password)
- Email verification system with branded HTML email templates
- PIN-based quick authentication for easy mobile access
- Password reset and account recovery system
- User profiles with first name, last name, ZIP code
- User-specific system access and talkgroup permissions
- Per-user delay settings (global, per-system, per-talkgroup)
- Account expiration dates for time-limited access

**User Groups & Multi-Tenant System**
- User Groups with hierarchical permission levels
- Group administrators who can manage their group's users
- Group-based system access control with granular permissions
- Group-based delay settings that cascade to users
- Max users per group limits for capacity management
- Public registration option per group
- Group-to-group user transfer system with approval workflow
- Registration codes with expiration dates and usage limits
- Email invitation system for adding users
- One-time or multi-use registration codes

**Stripe Integration & Billing System**
- Full Stripe payment processing integration
- Multiple pricing tiers (up to 3 pricing options per group)
- Subscription management (active/failed/expired status tracking)
- Two billing modes: per-user billing or group-admin billing
- Automatic subscription status tracking
- Payment failure handling and notifications
- Stripe customer and subscription ID management
- Integration with user account expiration

**Audio Transcription System**
- Multiple transcription provider support:
  - Google Speech-to-Text
  - Azure Speech-to-Text  
  - Whisper API (OpenAI compatible)
  - AssemblyAI
- Transcription queue processing system
- Confidence score tracking
- Multi-language detection and support
- Timestamped transcript segments
- Transcript storage in database
- Transcription status tracking (pending/processing/completed/failed)

**Alert & Notification Engine**
- **Tone Detection System:**
  - FFT-based frequency analysis for precise tone detection
  - Configurable tone sets per talkgroup
  - Two-tone sequential detection (A-tone + B-tone)
  - Long-tone detection support
  - Complex multi-tone pattern matching
  - Frequency tolerance configuration
  - Duration-based tone validation
  - Tone set library management
- **Keyword Matching System:**
  - Whole-word keyword matching in transcripts
  - Case-insensitive matching
  - Context extraction around matches
  - Multiple keyword lists per user
  - Shared keyword lists across users
  - Keyword match history and tracking
- **Push Notification System:**
  - OneSignal integration for mobile push
  - iOS and Android platform support
  - Custom notification sounds per device
  - Alert filtering based on user delay settings
  - Notification deduplication
  - Device token management
- **Per-User Alert Preferences:**
  - Enable/disable alerts per system/talkgroup
  - Select specific tone sets to monitor
  - Custom keyword lists per user per talkgroup
  - Email notification preferences
  - Alert history tracking

**Enhanced Site Management**
- Complete site database table with foreign key relationships
- Site ordering and organization
- Site resolution during call ingestion from both siteId and siteRef
- Site display on main screen with label and reference ID
- Site-based call filtering and search

**Email System**
- SMTP integration (Gmail, Office 365, custom SMTP servers)
- TLS and StartTLS security support
- Branded HTML email templates with custom logo
- Email verification messages
- Password reset emails with secure tokens
- User invitation emails
- Alert notification emails
- Transfer request approval emails
- Customizable email branding per instance

**Mobile Device Management**
- Device token registration for mobile apps
- Platform detection and management (Android/iOS)
- Custom notification sounds per device
- Multi-device support per user
- Device removal and cleanup

**Enhanced Access Control**
- Migration from simple "access codes" to full user account system
- Connection limits per user and per group
- Account expiration enforcement
- System/talkgroup granular permission controls
- Group-based permission inheritance
- Dynamic permission updates without server restart

**New Database Architecture**
New tables added in v7:
- `users` - Full user account system
- `userGroups` - User group management
- `userAlertPreferences` - Per-user alert configuration
- `keywordLists` - Shared keyword list management
- `alerts` - Alert history and tracking
- `transcriptions` - Audio transcription storage
- `keywordMatches` - Keyword match tracking
- `registrationCodes` - Registration code system
- `userInvitations` - Email invitation workflow
- `transferRequests` - Inter-group transfer system
- `deviceTokens` - Mobile device management
- `talkgroupGroups` - Multi-group talkgroup assignments
- `sites` - Enhanced site management

**Removed Features**
- Simple "Access Codes" system replaced by full user authentication
- Direct access code configuration replaced by user/group management

### Core Enhancements & Fixes

**Critical Bug Fixes**
- Fixed SQL GROUP BY clause error in call retrieval that prevented playing calls from search results
- Fixed stack overflow error in Delayer component caused by infinite recursion when restoring delayed calls
- Fixed critical infinite recursion in Delayer component by eliminating circular calls to EmitCall from within Delay methods
- Fixed security issue where delayed calls could be bypassed through direct call access (search calls, direct ID access, etc.)
- Fixed persistence issue where Default System Delay option was not being saved/loaded properly across server restarts
- Fixed search bypass issue - Search results now properly exclude calls that should be delayed based on current time and system delay settings
- Fixed SQL syntax errors - Resolved PostgreSQL compatibility issues with ORDER BY clauses and parameter placeholders
- Fixed database compatibility - SQL query construction now works correctly with PostgreSQL

**Default System Delay Enhancement**
- Added new "Default System Delay" configuration option that applies to all systems and talkgroups unless they have specific delay values set
- Stream delay in minutes that delays live audio streaming to clients (not recording delay)
- Global fallback with individual system/talkgroup override capability

**Delayed Call Security & Access Control**
- Delayed calls are now properly blocked from playback until their delay period expires
- Enhanced client error handling to display user-friendly error messages when delayed calls are accessed
- New "ERR" websocket message type for error notifications
- Comprehensive error display system using Material Design snackbars for immediate user feedback

**User-Specific Delay System**
- Comprehensive user access delay support throughout the entire call processing pipeline
- Enhanced delay logic - User access codes now properly respect individual delay settings (talkgroup, system, and global delays)
- Live call processing - Modified `EmitCall` function to use user delays instead of system defaults for all incoming calls
- Search functionality - Updated call search system to respect user-specific delays, ensuring consistent behavior between live and archived calls
- Delayer system - Fixed delayer component to properly handle user delays when processing calls
- Admin interface - New `/api/admin/user-edit` endpoint for updating existing access codes without requiring server restarts
- Real-time updates - Access code changes take effect immediately without server restarts
- Priority system - Implemented proper delay priority: talkgroup-specific → system-specific → user global → system default
- Enhanced UI indicators - Visual distinction between system-delayed calls, historical audio, and live audio with color-coded flags and tooltips

**Client-Side Persistence**
- Hold TG SYS persistent in local storage - Hold system and talkgroup preferences now persist across page refreshes and browser sessions
- User preferences saved to browser local storage for consistent experience

**Enhanced Site ID Support**
- Implemented comprehensive site resolution system supporting both database siteId and user-defined siteRef during call ingestion, API uploads, and main screen display
- Server-side: Enhanced call ingestion to resolve site information from both siteId and siteRef metadata
- API: Added site resolution logic to API call upload handler for proper site identification
- Client: Added site information display row on main screen showing site label and reference ID
- Parsers: Added support for siteId and siteRef metadata tags in multipart content parsing
- Database: Leverages existing site table structure with siteId (auto-increment) and siteRef (user-defined) fields

**Additional Enhancements**
- Added safety checks to prevent circular references and nil pointer dereferences in delay processing
- API Support for Radio Reference DB Direct Import

**Completed Features**
- ✅ Hold TG SYS persistent in local storage
- ✅ Ingest Site ID and API Site ID - Comprehensive site resolution for both database siteId and user-defined siteRef
- ✅ Display site # or label on main screen - Site information display with label and reference ID

**Known Outstanding Items**
- TODO: Ingest call DBFS and API call DBFS
- TODO: Search by UID

---

**END OF THINLINE DYNAMIC SOLUTIONS ENHANCEMENTS**

## Version 6.6

- From now on precompiled versions of macOS will be named as such instead of darwin.
- Better example for rtlsdr-airband that leverage the new #TGHZ, #TGKHZ and #TGMHZ meta tags for dirwatch.
- Fixed dirwatch definition not always showing mask field when type is default.
- Fixed truncated source ids with SDRTrunk parser (issue #265).
- Fixed admin logs not updating if no results are found.
- New parameter http://host:port/?id=xyz added to URL that allows multiple client instances with different talkgroup selections to be retained accross sessions.

_v6.6.1_

- Fixed search issue (issue #267).

_v6.6.2_

- Fixed authentication endless loop if wrong access code is entered.

_v6.6.3_

- Fixed dirwatch validation for type trunk-recorder (issue #280).

## Version 6.5

- Fixed API looping on malformed or invalid multipart content (issue #181, #212).
- Source code updated to GO 1.18 with `interface{}` replaced by `any`.
- Removed ingest mutex for performance reasons.
- Replaced all `path.Base()` by `filepath.Base()` to fix an issue with audio filenames on Windows.
- New `Branding Label` and `Email Support` options to show on main screen (issue #220).
- New temporary avoid feature (discussion #218).
- Fixed remote address regexp (issue #225).
- Added the `ident` to `new listener` log message (discussion #226).
- New populated talkgroups won't be activated on the client if its group (or tag) is turned off (issue #227).
- Removed the duplicated webapp section from the PDF document.

_v6.5.1_

- Fixed broken functionality for `HOLD SYS` and `HOLD TG` (issue #228).

_v6.5.2_

- Fixed erratic listeners count.
- Show call date on main screen when call is older than one day (issue #229).
- Fixed dirwatch #DATE, #TIME and #ZTIME regexp to accomodate filenames like 20220711082833 (issue #235).

_v6.5.3_

- Return of the -admin_password option to reset the administrator password in case of forgetting.
- New `<iframe>` wrapper in `docs/examples/iframe` for those who want to give more information to their users.
- Fixed systems.write constraint failed (issue #241).
- Add filename to dirwatch error messages (issue #248).
- Dirwatch.type=default now defaults to the current date and time if none are provided by the metatags (discussion #250).

_v6.5.4_

- Fixed some warnings when linting server code.
- New dirwatch type `DSDPlus Fast Lane` (discussion #244).
- Added new error catches on dirwatch (issue 254).
- Fixed search by inaccurate time (issue #258).
- Reverted sync.Map to regular map with sync.Mutex.

_v6.5.5_

- Fixed concurrent map read and map write on dirwatch.

_v6.5.6_

- Fixed Clients lockup by removing mutex on some unecessary Clients methods.
- Better `DSDPlus Fast Lane` parser. Tested with `ConP(BS)`, `DMR(BS)`, `NEXEDGE48(CS)`, `NEXEDGE48(CB)`, `NEXEDGE48(TB)`, `NEXEDGE96(CB)`, `NEXEDGE96(CS)`, `NEXEDGE96(TB)`, `P25(BS)` and `P25`.
- Fixed unit aliases not displaying on the main screen under certain circumstances.
- New incremental debouncer for emitting listeners count.

## Version 6.4

- New `-cmd` command line options to allow advanced administrative tasks.
- New playback mode goes live options which is not enabled by default (issue #175).
- Fixed logs retrieval from administrative dashboard (issue #193).
- Improved field conversions when retrieving calls from a mysql/mariadb database (issue #194, #198).
- Highlight replayed call on the history list (issue #196).

_v6.4.1_

- New 12-Hour time format option (issue #205).
- New audio conversion options which replace the disable audio conversion option.
- Keep database connections open and don't close them when idle.
- Log the origin of listeners.
- Fixed timestamp format when checking for call duplicates.
- Fixed http timeouts on call ingestions or admin config save when dowstream takes too long (issue #197).

_v6.4.2_

- Revert the last changes to the SDR Trunk parser (issue #206).

_v6.4.3_

- Add a note on the dirwatch admin screen about sdr-trunk.
- Starts client read/write pumps before registering with the controller (issue #181, #212).

_v6.4.4_

- Don't emit calls to listeners in separate GO routine to stay in sync with call ingestion.

_v6.4.5_

- SQL idle connections now expiring after 1 minute.
- Reverted defer rows.Close() to simple rows.Close().

## Version 6.3

- Changed scroll speed when drag droping talkgroups or units in a system (discussion #170).
- System Ids listed in the `Config / Options / AFS Systems` will have their talkgroup Ids displayed in AFS format (issue #163).
- New dirwatch meta tags #GROUP #SYSLBL #TAG #TGAFS and #UNIT for better ProScan compatibility (issue #164).
- Playback mode will now catch up to live (issue #175).
- Dirwatch code rewrite (issue #177).

_v6.3.1_

- Playback mode catch up to live, then swith to livefeed mode.
- Removed the mutex lock on Clients.Count which led to a deadlock and froze call ingestion.

_v6.3.2_

- New #TGLBL metatag for dirwatch for ProScan (%C) or alike.
- Fixed `semacquire` lockup in Clients (issue #177, #181, #182).
- Replay button now replays from history if pressed multiple times quickly (issue #186).

_v6.3.3_

- Fixed concurrent map writes fatal error in dirwatch (issue #187).
- Brighter LED colors and new orange color.
- Fixed call id when retrieved from a MySQL database.
- Add loudnorm audio filter to the ffmpeg audio conversion.
- Show the real IP address in the logs taking into account if behind a proxy.
- Fixed panic when emitting a call to clients.

_v6.3.4_

- Fixed ffmpeg audio filter not available on older version (issue #189).
- Improved logging when run as a service, Windows users can now see these logs in the events viewer.
- Dirwatch now catches panic errors and logs them.

_v6.3.5_

- Replace standard map with sync.map in dirwatch.
- Fixed the ffmpeg version test.
- Fixed led color type, orage -> orange.
- Fixed incorrect options when reading from a mysql database (issue #190).

_v6.3.6_

- Fixed systems order properties not sent to clients.
- Fixed side panels not scrolling to top when opened.

## Version 6.2

- New max clients options which is 200 by default.
- New show listeners count options which is disabled by default (issue #125).
- Fixed panic: concurrent write to websocket connection on goroutine.
- Fixed units import from SDR Trunk (issue #150).

_v6.2.1_

- Fixed SIGSEGV error in Units.Merge (issue #151).

_v6.2.2_

- Fixed another SIGSEGV error in Units.Merge (issue #151).

_v6.2.3_

- New random UUID in the JSON-Web Token payload.
- Fixed dirwatch not properly shutting down when a new configuration is applied.
- Fixed dashboard logout not sending HTTP 200 OK status.
- Clear the active dirwatch list when stopped.
- Pauses calls ingestion before database pruning.
- Fixed regex for units in driwatch type SDRTrunk (discussion #155).
- Update SQLite driver.

_v6.2.4_

- Fixed call frequencies table not being transmitted to downstream.
- Avoid using setInterval and setTimeout in the webapp.
- Fixed talkgroup search filter upon new configuration (issue #158).

_v6.2.5_

- Fixed unnecessary auto populate of unit id/label (issue #160).

## Version 6.1

- Calls now support patched talkgroups.
- New search patched talkgroups option which is disabled by default.
- Talkgroups and units are now stored in their own database table.
- New units CSV importer.
- Fixed blacklisted talkgroups being created anyway when autopopulate is enabled.
- Fixed compatibility with mysql/mariadb (default sqlite is still recommended).

_v6.1.1_

- Fixed `unknown datetime format sql.NullString` error.

_v6.1.2_

- Fixed image links in webapp.md (issue #76).
- Fixed SIGSEGV when trying to autopopulate (issue #77).
- Fixed parsing SDRTrunk meta data.
- Dirwatch type trunk-recorder now deletes json files without audio (when deleteAfter is set).
- Add a new `docs/update-from-v5.md` document.

_v6.1.3_

- Fixed concurrent config write when autopopulate is enabled (issue #77).
- Fixed API in regards to audio filename and audio type (issue #78).
- Fixed migration error on mysql database (issue #86).
- Fixed some calls not playing on the native app (issue #87).
- Fixed admin password not read from mysql.

_v6.1.4_

- Talkgroup label now syncs with the talkgroup_tag from the API or dirwatch (issue #80).
- Fixed more migration errors on mysql database (issue #86).
- Fixed config export not working with non latin-1 characters (issue #89).
- Fixed talkgroup label from dirwatch type sdrtrunk (discussion #98).
- Fixed SIGSEGV (issue #100).
- New `patch` indicator for patched talkgroups.

_v6.1.5_

- Fixed trunk-recorder API (issue #104).
- Fixed for avoid/patch flags on main display not beaving as expected.
- Fixed downstream not sending sources data.
- Fixed dirwatch crashing when config is updated.

_v6.1.6_

- Fixed webapp not reporting the correct version.

_v6.1.7_

- More concurrency mutexes to resolve SQL_BUSY errors.
- Better internal management of dirwatches.
- Fixed SDRTrunk files not being ingested (discussion #108).
- Fixed Trunk Recorder talkgroup_tag assign to the wrong property (issue #115).
- Improved the way the talkgroup label and name are autopopulated. If Trunk Recorder sends a talkgroup_tag with an empty value or with a single `-`, it will not overwrite the talkgroup label.

_v6.1.8_

- New dirwatch masks #TGHZ, #TGKHZ and #TGMHZ which allow to set talkgroup id based on frequency.

_v6.1.9_

- Fixed talkgroup sorting issue when importing from a CSV file (issue #119).
- Fixed SIGSEGV (issue #120).

_v6.1.10_

- Backport dirwatch delay value from v5.1.

_v6.1.11_

- Fixed connection errors when behind a reverse-proxy.
- Fixed disappearing talkgroups (issue #127).

_v6.1.12_

- Fixed too many open files (issue #129).
- Cosmetic: AVOID and PATCH flags now only appear when needed.

_v6.1.13_

- Better handling of dead client connections.
- Fixed too many open files (issue #129).
- Remove net.http error messages from the output (issue #131).

_v6.1.14_

- Fixed FAQ section not being added to the PDF documents.
- Bump delay before killing unauthenticated clients from 10 seconds to 60 seconds.
- Remove the gitter.im support forum from the documentation and prefer github discussions.

_v6.1.15_

- Fixed access and downstreams order not retained.
- Remove the self-signed certificate generator (-ssl create) as it was causing more problems than solutions.
- Client handling and call ingestion now run on 2 different threads (issue #135).
- Fixed downstream talkgroup select keeps reverting to all talkgroups (issue #136).

_v6.1.16_

- Fixed concurrent map access for clients.
- Some tweaks to websocket management.

## Version 6.0

- Backend server rewritten in Go for better performance and ease of installation.
- New toggle by tags option to toggle talkgroups by their tag in addition to their group.
- Buttons on the select panel now sound differently depending on their state.
- You can now filter calls by date and time on the search panel.
- Installable as a service from the command line.
- Let's Encrypt automatic generation of certificates from the command line.
- A bunch of minor fixes and improvements.

### BREAKING CHANGES SINCE V5

[Rdio Scanner](https://github.com/chuot/rdio-scanner) is now distributed as a precompiled executable in a zip file, which also contains documentation on how it works.

The backend server has been completely rewritten in GO language. Therefore, all the subpackages used in v5 had to be replaced with new ones. These new subpackages do not necessarily have the same functionality as those of v5.

- No more polling mode for _dirwatch_, which in a way is a good thing as polling was disastrous for CPU consumption. The alternative is to install a local instance and use the downstream feature to feed your main instance.
- Due to the polling situation, the Docker version of Rdio Scanner doesn't have the dirwatch feature.
- Default database name changed from _database.sqlite_ to _rdio-scanner.db_. You will need to rename your database file with the new name if you want to convert it. Otherwise, a new database will be created.

_v6.0.1_

- Fixed button sound on select panel for TG (beep state inverted)
- Auto populate system units (issue #66)

_v6.0.2_

- Try to fix the SQL_BUSY error (issue #67).
- Fixed `-service stop` timing out before exiting.
- Drop the ApiKey uniqueness of the downstreams database table.
- Fixed auto-populating the database with empty units tag.

_v6.0.3_

- Fixed strconv.Atoi: invalid syntax for dirwatch type sdrtrunk.
- Fixed the new version available dialog opening more than once.

_v6.0.4_

- Fixed wrong time calculation in prune scheduler.
- More fix on the SQL_BUSY error (issue #67).
- Support files (certs, db, ini) are now created in the same folder as the executable, if the folder is writable, or under a `Rdio Scanner` folder in the user's home folder.
- Some code refactoring.

_v6.0.5_

- Force mime type to `application/javascript` for `.js` extensions. (see https://github.com/golang/go/issues/32350).
- New `-base_dir` option to specify the directory where all data will be written.
- New Docker container with disabled dirwatch.

_v6.0.6_

- Fixed an issue with not closing the database when restarting the host platform (issue #71).
- Fixed SDRTunk parser when artist tag contains CTCSS tones.
- Platforms linux/amd64, linux/arm and linux/arm64 are now available for the Docker container.

_v6.0.7_

- Fixed dropped connections when going through a proxy.

## Version 5.2

- Change to how the server reports version.
- Fixed cmd.js exiting on inexistant session token keystore.
- Fixed issue with iframe.
- Node modules updated for security fixes.

_v5.2.1_

- Fixed talkgroup header on the search panel (issue #47).
- Update dirwatch meta tags #DATE, #TIME and #ZTIME for SDRSharp compatibility (issue #48).
- Fixed dirwath date and time parsing bug.
- Configurable call duplicate detection time frame.

_v5.2.2_

- Little changes to the main screen history layout, more room for the second and third columns.
- Node modules updates.

_v5.2.3_

- Change history columns padding from 1px to 6px on the main screen.
- Fixed a bug in the admin api where the server crash when saving new config from the admin dashboard.

_v5.2.4_

- Updated to Angular 12.2.
- New update prompt for clients when server is updated.
- Fixed unaligned back arrow on the search panel.

_v5.2.5_

- STS command removed from the server.
- Minor fixes here and there.
- README.md updated.
- Documentation images resized.

_v5.2.6_

- Fixed crash when when options.pruneDays = 0.

_v5.2.7_

- Fixed handling of JSON datatypes on MySQL/MariaDB database backend.
- Fixed listeners count.

_V5.2.8_

- Fixed SQLite does not support TEXT with options.

_V5.2.9_

- Fixed bad code for server options parsing.
- Increase dirwatch polling interval from 1000ms to 2500ms.

## Version 5.1

This one is a big one... **Be sure to backup your config.json and your database.sqlite before updating.**

- With the exception of some parameters like the SSL certificates, all configurations have been moved to an administrative dashboard for easier configuration. No more config.json editing!
- Access codes can now be set with a limit of simultaneous connections. It is also possible to configure an expiration date for each access codes.
- Auto populate option can now be set per system in addition to globally.
- Duplicate call detection is now optional and can be disabled from the options section of the administrative dashboard.
- On a per system basis, it is now possible to blacklist certain talkgroup IDs against ingestion.
- Groups and tags are now defined in their own section, then linked to talkgroup definitions.
- Server logs are now stored in the database and accessed through the administrative dashboard, in addition to the standard output.
- Talkgroups CSV files can now be loaded in from the administrative dashboard.
- Server configuration can be exported/imported to/from a JSON file.
- The downstream id_as property is gone due to its complexity of implementation with the new systems/talkgroups selection dialog for access codes, downstreams and apikeys.
- The keyboard shortcuts are a thing of the past. They caused conflicts with other features.
- Minor changes to the webapp look, less rounded.
- Talkgroup buttons label now wraps on 2 lines.

_v5.1.1_

- Fixed database migration script to version 5.1 to filter out duplicate property values on unique fields.
- Fixed payload too large error message when saving configuration from the administrative dashboard.
- Bring back the load-rrdb, load-tr and random uuid command line tools.

_v5.1.2_

- Fixed config class not returning proper id properties when new records are added.
- Fixed database migration script to version 5.1 when on mysql.
- Fixed bad logic in apiKey validation.
- Remove the autoJsonMap from the sequelize dialectOptions.
- Client updated to angular 12.

## Version 5.0

- Add rdioScanner.options.autoPopulate which by default is true. The configuration file will now be automatically populated from new received calls with unknown system/talkgroup.
- Add rdioScanner.options.sortTalkgroupswhich by default is false. Sort talkgroups based on their ID.
- Remove default rdioScanner.systems for new installation, since now we have autoPopulate.
- Node modules update.

_v5.0.1_

- Remove the EBU R128 loudness normalization as it's not working as intended.
- Fixed the API key validation when using the complex syntax.

_v5.0.2_

- Fixed rdioScanner.options.disableAudioConversion which was ignored when true.

_v5.0.3_

- Fixed error with docker builds where sequelize can't find the sqlite database.

_v5.0.4_

- Improvement to load-rrdb and load-rr functions.
- Sort groups on the selection panel.
- Allow downstream to other instances running with self-signed certificates.
- Node modules update.

_v5.0.5_

- Node modules security update.
- Improve documentation in regards to minimal Node.js LTS version.
- Add python to build requirements (to be able to build SQLite node module).

## Version 4.9

- Add basic duplicate call detection and rejection.
- Add keyboard shortcuts for the main buttons.
- Add an avoid indicator when the talkgroup is avoided.
- Add an no link indicator when websocket connection is down.
- Node modules update.

_v4.9.1_

- Add EBU R128 loudness normalization.
- dirWatch.type="trunk-recorder" now deletes the JSON file in case the audio file is missing.
- Fixed downstream sending wrong talkgroup id.

_v4.9.2_

- Add Config.options.disableKeyboardShortcuts to make everyone a happy camper.

## Version 4.8

- Add downstream.system.id_as property to allow export system with a different id.
- Add system.order for system list ordering on the client side.
- Fixed client main screen unscrollable overflow while in landscape.
- Fixed issue 26 - date in documentation for mask isn't clear.
- The skip button now also allows you to skip the one second delay between calls.
- Node modules update.

_v4.8.1_

- Refactor panels' back button and make them fixed at the viewport top.
- Node modules update.

_v4.8.2_

- Fixed dirWatch.type='sdr-trunk' metatag artist as source is now optional.
- Fixed dirWatch.type='sdr-trunk' metatag title as talkgroup.id.
- Web app now running with Angular 11.
- Node modules update.

_v4.8.3_

- Add the ability to overwrite the default dirWatch extension for type sdr-trunk and trunk-recorder.
- Fixed dirWatch.disabled being ignored.
- Node modules update.

_v4.8.4_

- Fixed the timezone issue when on mariadb.
- Fixed downstream sending wrong talkgroup id.
- Node modules security update.

_v4.8.5_

- Fixed broken dirwatch.delay.
- Node modules update.

## Version 4.7

- New dirWatch.type='sdr-trunk'.
- New search panel layout with new group and tag filters.
- Add load-tr to load Trunk Recorder talkgroups csv.
- Remove Config.options.allowDownloads, but the feature remains.
- Remove Config.options.useGroup, but the feature remains.
- Bug fixes.

_v4.7.1_

- Fixed crash on client when access to talkgroups is restricted with a password.

_v4.7.2_

- Fixed Keypad beeps not working on iOS.
- Fixed pause not going off due to the above bug.

_v4.7.3_

- Fixed websocket not connection on ssl.

_v4.7.4_

- Fixed display width too wide when long talkgroup name.

_v4.7.5_

- Fixed playback mode getting mixed up if clicking too fast on play.
- Fixed side panels background color inheritance.
- Node modules update.

_v4.7.6_

- Fixed search results not going back to page 1 when search filters are modified.
- Skip next button no longer emit a denied beeps sequence when pushed while there's no audio playing.
- Node modules update.

## Version 4.6

- Fixed documentation in regards to load-rrd in install-github.md.
- Fixed database absolute path in config.json.
- Remove config.options.useLed.
- Rename Config.options.keyBeep to Config.options.keypadBeeps.
- Config.options.keypadBeeps now with presets instead of full pattern declaration.
- Bug fixes.

## Version 4.5

- Config.options.keyBeep which by default is true.
- Bug fixes.

## Version 4.4

- Config.systems.talkgroups.patches to group many talkgroups (patches) into one talkgroup.id.
- Config.options now groups allowDownloads, disableAudioConversion, pruneDays, useDimmer, useGroup and useLed options instead of having them spread all over the config file.
- Client will always display talkgroup id on the right side instead of 0 when call is analog.
- Fixed annoying bug when next call queued to play is still played even though offline continuous play mode is turned off.
- Talkgroup ID is displayed no matter what and unit ID is displayed only if known.

## Version 4.3

- Add metatags to converted audio files.
- Automatic database migration on startup.
- Client now on Angular 10 in strict mode.
- Dockerized.
- Fixed downstream not being triggered when a new call imported.
- Fixed dirWatch mask parser and new mask metatags.
- Fixed stop button on the search panel when in offline play mode.
- Fixed SSL certificate handling.
- Rewritten documentation.

## Version 4.2

- Fixed possible race conditions....
- Added websocket keepalive which helps mobile clients when switching from/to wifi/wan.
- Better playback offline mode animations and queue count.
- New dirWatch.mask option to simplify meta data import.

## Version 4.1

- New playback mode.

## Version 4.0

- GraphQL replaced by a pure websocket command and control system.
- `server/.env` replaced by a `server/config.json`.
- Systems are now configured through `server/config.json`, which also invalidate the script `upload-system`.
- Indexes which result in much faster access to archived audio files.
- Add SSL mode.
- Restrict systems/talkgroups access with passwords.
- Directory watch and automatic audio files ingestion.
- Automatic m4a/aac file conversion for better compatibility/performance.
- Selectively share systems/talkgroups to other instances via downstreams.
- Customizable LED colors by systems/talkgroups.
- Dimmable display based on active call.

### Upgrading from version 3

- Your `server/.env` file will be used to create the new `server/config.json` file. Then the `server/.env` will be deleted.
- The `rdioScannerSystems` table will be used to create the _rdioScanner.systems_ within `server/config.json`. Then the `rdioScannerSystems` table will be purged.
- The `rdioScannerCalls` table will be rebuilt, which can be pretty long on some systems.
- It is no longer possible to upload neither your TALKGROUP.CSV nor you ALIAS.CSV files to _Rdio Scanner_. Instead, you have to define them in the `server/config.json` file.

> YOU SHOULD BACKUP YOUR `SERVER/.ENV` FILE AND YOUR DATABASE PRIOR TO UPGRADING, JUST IN CASE. WE'VE TESTED THE UPGRADE PROCESS MANY TIMES, BUT WE CAN'T KNOW FOR SURE IF IT'S GOING TO WORK WELL ON YOUR SIDE.

## Version 3.1

- Client now on Angular 9.
- Display listeners count on the server's end.

## Version 3.0

- Unit aliases support, display names instead of unit ID.
- Download calls from the search panel.
- New configuration options: _allowDownload_ and _useGroup_.

> Note that you can only update from version 2.0 and above. You have to do a fresh install if your actual version is prior to version 2.0.

## Version 2.5

- New group toggle on the select panel.

## Version 2.1

- Various speed improvements for searching stored calls.

## Version 2.0

- Ditched meteor in favour of GraphQL.

## Version 1.0

- First public version.
