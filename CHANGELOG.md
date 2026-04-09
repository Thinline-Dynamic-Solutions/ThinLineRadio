# Change log

## Version 26.04.025 - Released Apr 8, 2026

### Security

- **Server — Removed open test-pager-alert endpoint**
  - `POST /api/admin/test-pager-alert` was unauthenticated and publicly accessible — any caller could trigger push notifications to any user
  - The route and handler have been removed entirely

---

## Version 26.04.024 - Released Apr 8, 2026

### Fixed

- **Server — Pager alert audio fetch failing with 400 (scientific-notation `callId` in URL)**
  - `callId`, `systemId`, and `talkgroupId` were placed into the FCM data payload as raw integers
  - Go's JSON decoder parses all JSON numbers into `float64` when unmarshalling into `interface{}`, so the relay server converted `29874435` to `float64(29874435)` and then formatted it with `%v`, producing `"2.9874435e+07"`
  - The mobile app built the audio URL as `/api/calls/2.9874435e+07/audio`, which the server rejected with HTTP 400
  - All three IDs are now serialised to strings with `fmt.Sprintf("%d", ...)` before being placed in the payload — the relay no longer touches them as numbers

---

## Version 26.04.023 - Released Apr 9, 2026

### Changed

- **Server — Duplicate timestamp match window is now configurable via the admin UI**
  - The ±millisecond window used to compare call timestamps when detecting duplicates was previously hardcoded at 800 ms
  - Admins can now adjust this under Options → **Timestamp Match Window** (default: 800 ms, range: 100–30,000 ms)
  - Increasing this value (e.g. to 1200 ms) catches simulcast uploads from recorders whose clocks differ by up to 1 second
  - The existing "Detection Timeframe" field has been renamed to **Cache Retention** with a corrected description — it controls how long call fingerprints are held in memory, not the comparison window

---

## Version 26.04.022 - Released Apr 9, 2026

> ⚠️ **DUPLICATE DETECTION CHANGE — READ BEFORE DEPLOYING**
>
> Timestamp-based duplicate detection has been **replaced with PCM audio content hashing**.
> If you experience missed or incorrectly flagged duplicate calls after upgrading, **roll back to 26.04.021** — rollback is safe; the database schema is forward-compatible and no data will be lost.
> Timestamp-based matching will be re-introduced if issues are reported. This version was validated against 12+ hours of continuous live traffic with zero false positives or false negatives observed.

### Changed

- **Server — Duplicate call detection overhauled (timestamp matching replaced with audio content hashing)**
  - Duplicate detection now uses PCM content hashing (SHA-256 of raw decoded audio) as the primary method — two calls are only flagged as duplicates when their actual audio is byte-for-byte identical after decoding, regardless of container format or codec
  - Timestamp-based matching has been removed as the primary detection mechanism; it was producing false positives on systems that assign coarse wall-clock timestamps (e.g. SDR Trunk) where two distinct calls could share the same second-level timestamp
  - All calls — including detected duplicates — are now stored in the database (`isDuplicate = true`) rather than dropped, so all audio is retained for review and debugging
  - Validated against 12+ hours of continuous manual review with zero false positives and zero false negatives observed

- **Server — `/calls` debug page restricted to admin authentication**
  - The call debug page (`/calls`, `/calls/audio/`, `/calls/verify`) was previously unauthenticated and publicly accessible
  - All three routes are now protected by HTTP Basic Auth verified against the admin password — the browser will prompt for credentials on access

- **Server — Audio duration stored in the database now matches browser playback length**
  - Duration is now always derived from the final converted audio rather than the original upload, preventing mismatches caused by pre-allocated duration headers in SDR Trunk M4A files

---

## Version 26.04.021 - Released Apr 9, 2026

### Fixed

- **Server — Batched live alerts had no pager audio (CallKit only on iOS)**
  - `sendBatchedPushNotificationWithToneSet` sent all FCM batches with `pager_alert` omitted; only the separate VoIP batch included it, so PushKit/CallKit fired but the Flutter FCM background handler never ran (it requires `pager_alert` in the FCM data)
  - For users with pager-style playback enabled for the talkgroup, iOS and Android FCM tokens are now bucketed under keys `ios+pager:…` / `android+pager:…` and those batches include `pager_alert: "true"`, matching behaviour of the single-user `sendPushNotification` path

- **Server — Admin `POST /api/admin/test-pager-alert` did not trigger pager playback**
  - The handler called `sendNotificationBatch` with `extraData` always `nil` and grouped devices with a naive `platform == "ios"` check, so `pager_alert` was never set and VoIP tokens were mishandled
  - The test path now mirrors `sendPushNotification`: `resolveUserPagerAlert`, `resolveUserAlertSound`, legacy OneSignal handling, VoIP-only-when-enabled, and `pager_alert` in extras when appropriate

---

## Version 26.04.020 - Released Apr 8, 2026

### Fixed

- **Server — Pager alert VoIP push sent for all calls regardless of user preference**
  - `pager_alert: "true"` was unconditionally added to every call notification payload and VoIP tokens were included for all calls where `call != nil`, meaning every iOS device with a VoIP token received a PushKit/CallKit wake for every call — even talkgroups where the user never enabled pager-style playback
  - `resolveUserPagerAlert()` added: reads from the in-memory `PreferencesCache` (O(1), no DB round-trip) and returns whether the user has pager-style audio enabled for the specific talkgroup, with tone-set-level override support
  - VoIP tokens and the `pager_alert` flag are now only included in the notification payload when `resolveUserPagerAlert()` returns true for that user+talkgroup combination
  - Fix applied to both `sendPushNotification` (single-user path) and `sendBatchedPushNotificationWithToneSet` (multi-user path); VoIP tokens in the batched path are now collected per-user into a separate slice and sent as their own batch with `pager_alert: "true"` only when warranted

- **Server — `resolveUserAlertSound` hitting the database on every notification**
  - `resolveUserAlertSound` was issuing a `SELECT` query per user per call instead of using the in-memory `PreferencesCache`
  - Both `resolveUserAlertSound` and the new `resolveUserPagerAlert` now read from `PreferencesCache.GetPreference()`, consistent with the alert engine, keyword matcher, and tone detector

- **Relay Server — `content-available: 1` set on every iOS FCM message**
  - `content-available: 1` instructs iOS to wake the app silently in the background for every incoming push, including disconnect notifications
  - The background FCM handler was receiving disconnect notifications, triggering a reconnect, and re-enabling the live feed — causing the scanner to start playing audio even when the user had closed the app
  - `content-available: 1` is now only set on iOS FCM messages when `pager_alert == "true"` is present in the data payload; regular alert and disconnect notifications arrive as plain banner notifications only

---

## Version 26.04.019 - Released Apr 7, 2026

### New

- **Mobile — Pager-style alert playback**
  - Per-talkgroup and per-tone-set toggle to enable pager-style audio playback when the app is backgrounded
  - When enabled, the app fetches and plays the call audio natively in the background after the push notification arrives — no app interaction needed
  - Uses a native serial audio queue on both Android (`MediaPlayer` via `SingleThreadExecutor`) and iOS (`AVAudioPlayer` via a serial `DispatchQueue`): multiple simultaneous alerts are queued and played one after another rather than overlapping or cutting each other off
  - Duplicate callId deduplication in the native queue: if tone-alert and keyword-alert fire for the same call at the same time, only one playback is queued

- **Server — Pager-alert preference persistence**
  - `pagerAlert` and `toneSetPagerAlerts` columns added to `userAlertPreferences` (auto-migrated on startup)
  - `GET /api/alerts/preferences` now returns both fields; `PUT /api/alerts/preferences` saves them — toggles now survive app restarts and preference reloads from the server

- **Server — VoIP/PushKit push gating**
  - APNs VoIP pushes are now only sent when the notification is a real pager-alert call (`pager_alert: true` in the payload)
  - Regular test pushes, disconnect alerts, and keyword-only alerts no longer trigger the CallKit UI on iOS
  - Fix applied at both the TLR Scanner Server (excludes VoIP tokens when `call == nil`) and the Relay Server (checks `pager_alert` flag before sending APNs VoIP)

- **Server — Pager-alert test endpoint respects user toggles**
  - Removed the `pager_force` bypass from `TestPagerAlertHandler` — the admin test endpoint now respects each device's per-talkgroup pager-alert setting, consistent with real alert behaviour

---

## Version 26.04.018 - Released Apr 6, 2026

### Fixed

- **Admin — API key / downstream "Choose Systems" dialog shows 0 talkgroups**
  - Systems in the root config form are built with talkgroups skipped for performance (lazy-loaded only when a system is opened)
  - The systems-selection dialog now receives `rawSystems` (the original config data with full talkgroup lists) via the `apikeys` and `downstreams` components
  - When the form's talkgroup array is empty the dialog falls back to the raw data, so all talkgroup counts and checkboxes display correctly without any change to load-time performance
  - `downstreams` component receives the same fix; users and user-groups were already unaffected (they build their own forms or use a separate UI)

- **Server — Auto-populate race condition causes FK violation on `talkgroupGroups`**
  - When two calls arrived simultaneously for the same new talkgroup, a second goroutine could read a group or tag from the in-memory list before its DB-assigned ID was written back, resulting in `groupId = 0` being persisted
  - `talkgroup.WriteTx()` now skips any `groupId == 0` entry in the `GroupIds` slice instead of attempting the `talkgroupGroups` INSERT, eliminating the `SQLSTATE 23503` foreign-key violations
  - `group.Write()` now captures the database-assigned ID immediately after INSERT using `RETURNING "groupId"` (PostgreSQL) or `LastInsertId` (SQLite/other), so the in-memory group pointer carries its real ID before the write lock is released
  - `tag.Write()` receives the same fix (`RETURNING "tagId"` / `LastInsertId`), closing the equivalent race for tags

---

## Version 26.04.017 - Released Apr 5, 2026

### New

- **Admin — Master settings search bar**
  - Added a global search bar above the tab group on the admin panel
  - Searches across all Options panels, all Config sections (Systems, Users, API Keys, Groups, Tags, etc.), and all Tools by label, keywords, and breadcrumb path
  - Clicking a result switches to the correct tab, navigates to the matching section, and auto-opens and scrolls to the relevant Options expansion panel
  - Results capped at 10, shown in a floating dropdown with icon, label, and breadcrumb (e.g. "Config → Options → Email")
  - Press `Escape` or click away to dismiss; `×` button clears the query

- **Admin — Unsaved changes indicator and header save/reset buttons**
  - "Unsaved changes" badge with pulsing red dot appears in the top header when the config form is dirty
  - Save and Reset buttons added to the top header bar, visible only on the Config tab
  - Reset now performs a full `window.location.reload()` to guarantee a clean state without any flicker
  - Buttons and badge are hidden on smaller screens (icon-only mode on mobile)

- **Admin — Options page collapsible sections**
  - All settings panels are collapsed by default and sorted A–Z: Alert & Health Monitoring, Audio Settings, Branding, Email, External Integrations, General Settings, Stripe Payments, Transcription Settings, User Registration
  - `panelsReady` guard with forced `cdr.detectChanges()` prevents the expanded-then-collapsed flicker on initial load and after reset
  - All panel expanded flags are explicitly reset to `false` before hiding so panels never flash open on re-render

- **Admin — Per-system no-audio alert settings in Options**
  - Moved all System Health Alert settings (master toggle, transcription failure alerts, tone detection alerts, no-audio alerts, alert retention) from the System Health page into Options → Alert & Health Monitoring
  - Added per-system no-audio threshold table under Alert & Health Monitoring, allowing per-system enable/disable and custom threshold minutes
  - System Health page now shows only live stats with a banner directing users to Options for configuration

- **Admin — Email verification option for user registration**
  - Added `EmailVerificationRequired` option under User Registration in Options
  - When enabled, signup uses a two-step flow: user enters email → receives 6-digit code → completes registration form
  - Users registering via invitation or access code are automatically marked as verified; no verification email is sent
  - Password reset now works regardless of email verification status
  - New `RequestSignupVerificationHandler` API endpoint sends a 6-digit code email using a dedicated `SendSignupVerificationEmail` method

- **Admin — Reconnection Manager always enabled**
  - Reconnection Manager is now always on server-side
  - Grace period and max buffer size settings remain configurable

- **Server — No-audio monitoring improvements**
  - Per-system no-audio monitoring goroutines now use dedicated stop channels for clean restart/shutdown without goroutine leaks
  - `StartNoAudioMonitoringForAllSystems` stops existing goroutines before spawning new ones
  - Monitoring automatically restarts when options are saved from the admin panel
  - `StartSystemHealthMonitoring` now performs immediate startup checks for transcription failures and tone detection issues in addition to the hourly ticker

### Changed

- **Admin — Options UI reorganisation**
  - Renamed "Security & Access Control" panel to "Audio Settings"; moved audio conversion and duplicate detection settings into it
  - Renamed "Push Notifications & Email" panel to "Email"; updated icon from bell to envelope
  - Moved Stripe Payments and User Registration into their own top-level expansion panels
  - Added Branding as a top-level expansion panel (Branding Label, Base URL, Server Logo, Favicon)
  - Renamed "Email Logo" to "Server Logo"

- **Admin — Search index coverage**
  - Added dedicated entries for Relay Server, Relay Server API Key, and Push Notifications
  - "Push notifications" keyword now maps to the Email panel (where relay server toggle lives)

### Fixed

- **Admin — Options page loading expanded (flicker bug)**
  - Root cause: `panelsReady = false` was set but change detection was not forced immediately, so the `setTimeout(0)` re-showed panels before they had actually hidden
  - Fixed by calling `cdr.detectChanges()` immediately after setting `panelsReady = false`, explicitly collapsing all nine panel flags first, and increasing the reveal delay to 80 ms

- **Admin — Tags and Groups showing "Unused" until talkgroups loaded**
  - Fixed by checking usage against the full `originalConfig` instead of the lazy-loaded FormArray

- **Admin — Chrome/Edge "Save password?" prompt on settings fields**
  - All non-login `type="password"` inputs converted to `type="text" class="masked-pw"` so Chrome's password detector never triggers outside the login form
  - `.masked-pw` class uses `-webkit-text-security: disc` to visually render as bullet characters, identical in appearance to a password field
  - `autocomplete="off"` added to all admin `<form>` elements and all text inputs
  - `autocomplete="new-password"` applied to remaining password-typed inputs (login form excluded)
  - Global `MutationObserver` in `index.html` stamps autocomplete attributes on any inputs Angular renders dynamically after boot
  - `navigator.credentials.preventSilentAccess()` called on page load to opt the admin panel out of the browser credential manager

- **Web Client — Live feed: Now Playing and call history out of sync**
  - Natural end of a transmission used `stop({ emit: false })` during the inter-call delay, so the UI never learned the call had finished until the next call was decoded — finished calls appeared late in “Call history (last hour)” and Now Playing could still show the previous transmission
  - Natural end now emits `stop({ emit: true })` so the finished call moves into history and Now Playing clears as soon as audio ends
  - Inter-call delay before dequeuing the next call reduced from 1000 ms to 350 ms
  - Now Playing “Source” only uses live `callUnit` when it matches a source/tag on the current call; otherwise it falls back to call metadata so a leftover unit ID from the prior transmission cannot display on the new row

### Performance

- **Server — In-memory duplicate call detection cache**
  - Added a mutex-protected in-memory cache that catches duplicate calls before they reach the database, closing the race window where two identical calls arrive simultaneously and both pass the DB check because neither has been written yet
  - Keyed by `systemId + talkgroupId` (bit-shifted composite key), each entry stores the most recent call timestamp; an incoming call is rejected if an entry exists within the configured detection timeframe
  - Background eviction goroutine runs every 30 seconds and removes entries older than 2× the detection timeframe, preventing unbounded memory growth — the map holds at most one entry per active talkgroup
  - Cache is checked before the legacy database query; rejected calls are logged as `"duplicate (legacy/cache)"` to distinguish from DB-level catches (`"duplicate (legacy)"`)
  - Graceful shutdown stops the eviction goroutine; cache is initialized at startup with the configured `duplicateDetectionTimeFrame`

- **Server — RAM caching for high-impact database queries**
  - Implemented in-memory caching for user alert preferences, keyword lists, system/talkgroup ID lookups, and recent alerts (1-hour window)
  - All caches use `sync.RWMutex` for thread-safe concurrent access
  - Composite keys (systemId, talkgroupId) now use bit-shifting for efficient numeric map keys instead of string concatenation
  - Database queries replaced with cache lookups in `alert_engine.go`, `api.go`, `controller.go`, and `transcription_queue.go`
  - Caches automatically reload after database writes to maintain consistency

- **Admin — Lazy loading for talkgroups in Systems configuration**
  - Systems overview page now loads instantly without instantiating thousands of FormControls
  - Talkgroups load progressively in the background when a system is opened, keeping initial paint low
  - Implemented per-system tracking to ensure only loaded systems have talkgroups included in saves
  - Systems that weren't opened retain their original talkgroup data from the backend
  - Optimized FormArray getters to return sorted cached views instead of clearing/repopulating on every access
  - Fixed critical bug where FormArray getters were destroying and recreating thousands of FormControls on every interaction

## Version 26.04.016 - Released Apr 1, 2026

### Fixed

- **Web Client — Now Playing row not always showing active transmission**
  - `event.emit({ call })` was only fired after `decodeAudioData` completed (an async operation), meaning the Now Playing row stayed blank for the entire 1-second inter-call gap plus however long browser audio decoding took
  - Moved the call emit to immediately after the call is dequeued, before decryption and decode begin — Now Playing metadata now appears as soon as the next call is selected, with no visible blank period between transmissions
  - Removed the now-redundant emit inside the `decodeAudioData` error callback

- **Duplicate alerts in web app for the same call**
  - Keyword alert deduplication was checking `callId + keywordsMatched` (exact match), so `["FIRE"]` and `["FIRE","MEDICAL","BREATHING"]` would both insert as separate rows for the same call
  - Changed the server-side check to match by `callId` only; if an alert already exists for that call it now `UPDATE`s the row with a merged, deduplicated keyword set instead of inserting a new row
  - Added `mergeKeywordsJson` helper that unions two JSON keyword arrays with deduplication
  - Client-side: `recentAlertsFlat` now deduplicates by `callId`, keeping the alert with the most keywords, as a safety net for existing duplicate rows in the database

## Version 26.04.015 - Released Mar 31, 2026

### Performance

- **Web Client — Core Web Vitals improvements (LCP, CLS, INP)**
  - **LCP (Largest Contentful Paint):** Reduced from 4.52 s → ~1.40 s
    - `alertsService` now persists the last 50 alerts to `sessionStorage` after every fetch and restores them on construction, so the Recent Alerts embed paints from cached data on the very first frame instead of waiting for an HTTP round-trip
    - The Recent Alerts embed component seeds from the service cache synchronously at init so `p.transcript-text` is visible immediately on page load
    - Replaced `white-space: pre-wrap` with `pre-line` on transcript text to reduce text-layout cost for long radio transcripts
    - Switched monospace font stack to `ui-monospace` (system alias, no download) as the leading option
    - Added `contain: layout style` to `.transcript-text` in the embed rail to isolate its layout from the rest of the page
  - **CLS (Cumulative Layout Shift):** Reduced from 0.44 → ~0.02
    - Added `content-visibility: auto` and `contain-intrinsic-size` to `.transcript-item` so off-screen paginated items reserve stable height before being painted
  - **INP (Interaction to Next Paint):** Reduced from 528 ms → ~200 ms range
    - All data-loading calls in `ngOnInit` (HTTP fetches, service subscriptions) are now deferred with `setTimeout(0)`, yielding the main thread so the browser can paint the newly activated tab before any work begins
    - The `alertsService.alerts$` BehaviorSubject subscription — which was firing synchronously with cached data and triggering expensive `updateGroupedAlerts()` on every tab click — is now also deferred into the same yielded task
    - Transcript search input debounced at 300 ms with `distinctUntilChanged` via RxJS `Subject`; previously fired an HTTP request on every keystroke
    - Added `trackBy: trackByTranscriptId` to the transcript `*ngFor` loop to prevent full list re-creation on pagination/refresh
    - Added `content-visibility: auto` to `.stats-section` and `.stats-counters` to skip layout/paint of off-screen chart sections during Stats tab initial render
    - Removed `console.log` from `loadTranscripts` hot path

### New

- **Admin — Download Rate Limiting: enable/disable toggle**
  - Replaced the "set to 0 to disable" pattern with an explicit enable/disable slide toggle in Admin → Options → Audio Security
  - When the toggle is off, Max Downloads and Window Duration fields are hidden and their validators are cleared — the form is always valid by default
  - When the toggle is on, both fields appear with their configured defaults (100 downloads / 60-minute window)
  - On save, if the toggle is off, `maxDownloadsPerWindow` is written as `0` to the server so existing backend logic is unchanged

- **Web Client — Scan Lists: add to list from Channels and Favorites**
  - Every talkgroup row in the Channels and Favorites tabs now has a `playlist_add` icon button
  - Clicking it opens an anchored dropdown listing all scan lists with a green checkbox for lists the talkgroup is already in — click to toggle in or out
  - A "New list…" option at the bottom creates a new scan list and immediately adds the talkgroup to it
  - Clicking anywhere outside the dropdown closes it
  - The dropdown appears in both the Channels tab and the Favorites tab (shared row template)

- **Web Client — Scan Lists: drag-to-reorder**
  - Scan list cards can now be reordered by dragging the `⠿` handle on the left of each card header
  - Uses Angular CDK drag-drop with vertical-axis lock; a dashed green placeholder marks the drop target
  - The favorites-derived list cannot be dragged
  - New order is auto-saved to the server immediately on drop

- **Mobile — Scan Lists: drag-to-reorder**
  - Scan list cards in the Scan Lists tab now support drag-to-reorder via a `drag_indicator` handle on the left of each card header
  - Uses Flutter's `ReorderableListView` with `ReorderableDragStartListener`; custom handles replace the default built-in handles
  - The favorites-derived list cannot be dragged
  - New order is persisted to both local storage and the server on drop

### Fixed

- **Admin — Audio Encryption toggle: reliable disable when no relay API key**
  - Removed the unreliable `[disabled]` template binding on the Audio Encryption slide toggle; the control is now properly enabled/disabled through Angular's reactive form API in the component
  - When the Relay Server API Key is blank or cleared after the toggle was already on, the toggle automatically turns off and disables — preventing an invalid saved state
  - The warning banner below the section heading is still shown when no API key is present

- **Admin — Options form invalid on fresh config**
  - `downloadWindowMinutes` was initialized with `?? 60` (nullish coalescing), but the Go server returns `0` for fields that have never been set; `0 ?? 60` resolves to `0`, which immediately failed `Validators.min(1)` and made the entire Options form invalid before the user touched anything
  - Changed to `|| 60` so that `0` (unset) correctly falls back to the default of `60`

- **Web Client — Favorites: tag favorite showed all system talkgroups**
  - When a tag was starred (favorited), the Favorites detail pane was still rendering all talkgroups in the system because the split-view detail pane always called `groupTalkgroupsByTag()` regardless of nav mode
  - Fixed by switching the detail pane's `*ngFor` source to `getFavoriteTagGroupsForSystem()` when in favorites mode
  - Also fixed a secondary bug in `getFavoriteTagGroupsForSystem`: a favorited tag's talkgroups were additionally filtered by `favoriteTalkgroups.has(tg.id)`, so favoriting a tag (with no individual talkgroup favorites) showed nothing; now all talkgroups under a favorited tag are shown, and individually favorited talkgroups whose tag is not starred are still included

## Version 26.04.014 - Released Mar 30, 2026

### New

- **Transcription queue depth endpoint**
  - New unauthenticated endpoint `GET /api/status/transcription-queue` returns `{"pending": N}` — the number of jobs currently waiting in the in-memory transcription queue
  - Queue depth also included in the existing `GET /api/status/performance` response as `transcription_queue_depth`
  - Useful for monitoring transcription backlog without logging into the admin panel

## Version 26.04.013 - Released Mar 30, 2026

### Fixed

- **Transcription: short calls on tone-detection-enabled talkgroups no longer bypass minimum duration**
  - Calls on tone-detection-enabled talkgroups that had no tones detected and were shorter than the configured minimum call duration were silently bypassing the minimum duration check and getting queued for transcription
  - These calls are now correctly skipped, consistent with non-tone-detection talkgroups
  - Calls that do have tones detected continue to use the existing remaining-audio logic unchanged

- **Transcription: duration check now uses original audio instead of converted audio**
  - `getCallDuration` was probing `call.Audio` (AAC-converted audio) to measure duration, which would fail or return incorrect results for servers not using audio conversion
  - Duration is now measured from `call.OriginalAudio` first, with `call.Audio` as a fallback — matching the same audio precedence used by the transcription workers themselves

## Version 7.0 Beta 9.7.26 - Released Mar 28, 2026

### New

- **Audio Encryption (AES-256-GCM)**
  - Available to registered servers only — prevents unauthorized scraping and rebroadcasting of audio streams
  - The encryption key is hosted exclusively on the Thinline App Server and is never visible on the client or server side
  - Authorized clients decrypt audio transparently with no audible difference
  - Downstream sends are now fired in parallel rather than sequentially

- **Download Rate Limiting**
  - Audio download requests can now be rate-limited per connection using a configurable sliding window (max downloads / window duration in minutes)
  - Configurable in Admin → Options → Audio Security
  - Set max downloads to 0 to disable

### New

- **Web Client — Classic / Legacy View toggle**
  - New fixed toggle button (top-right) lets users switch between the modern tab-based layout and the original classic scanner layout at any time without a page refresh
  - Both views remain mounted simultaneously using `[hidden]` instead of `*ngIf`, preserving WebSocket subscriptions, audio state, and all component state across view switches
  - View preference is saved to `localStorage` and restored on next visit
  - Classic view uses the exact original `mat-sidenav-container` architecture: Search slides in from the left, Channel Select / Settings / Alerts slide in from the right — identical to the pre-tab-era layout
  - Classic view source files (`main-legacy.component.*`, `select-legacy.component.*`) are restored verbatim from git history (commit `21a729b`) — only class names and selectors renamed to avoid conflicts

- **Web Client — Channel Select: Scan Lists tab**
  - New "Scan Lists" tab in the channel select panel alongside "Channels" and "Favorites"
  - Users can create, rename, and delete multiple named scan lists
  - Channels can be added or removed from any list; lists are persisted server-side under `users.settings`
  - Scan lists are collapsible by system and tag group, matching the Channels tab layout

- **Web Client — Channel Select: bubble/pill channel layout**
  - Talkgroups replaced the old CSS grid with full-width pill rows (`border-radius: 20px`)
  - Each pill shows the talkgroup label, name subtitle, and ID badge
  - Enabled channels highlighted with green border and background; disabled channels subdued
  - Sidebar + detail layout: systems listed on the left, talkgroup pills in the right detail panel, grouped by tag with collapsible tag headers

- **Web Client — Channel Select: compact toolbar redesign**
  - Search bar on its own row at the top
  - Single controls row below: enabled/total count on the left, icon-only action buttons (toggle all, systems filter, favorites bulk) on the right — replacing the previous cluttered layout

- **Web Client — Transcripts tab in Alerts screen**
  - New "Transcripts" tab alongside "Alerts" and "System Alerts" in the alerts panel
  - Displays all transcripts with system/talkgroup filters (sorted A–Z, grouped by tag), full-text search, and infinite scroll
  - Clicking anywhere on a transcript card plays the audio for that call
  - Pulls data via the same API path as the web app transcript list
  - Tab bar redesigned with a visible active indicator/border

- **Admin — Alerts enabled toggle per system and talkgroup**
  - New toggle on each system and talkgroup config card to enable or disable alerts (and transcription) for that entity
  - Toggle is surfaced directly in the system/talkgroup list rows — no need to open the edit card
  - When alerts are disabled for a system or talkgroup, all existing user alert preferences for that entity are automatically deleted from the database (migration runs on save)
  - Channels with alerts disabled are excluded from the alerts preferences UI for all users

- **Mobile — Per-channel notification sounds**
  - Users can select a distinct alert sound per channel and per tone set within that channel
  - Accessible from App Settings → Notification Sounds (renamed from "Notification Sound")
  - The global default sound appears at the top; systems and tag groups below it are collapsed by default, sorted the same as Alert Preferences
  - Tapping anywhere on a system or tag row expands or collapses it
  - The same bottom-sheet sound picker used for per-channel sounds is also used for the global default

- **Mobile — Transcripts tab in Alerts screen**
  - New "Transcripts" tab in the mobile alerts screen alongside "Alerts" and "System Alerts"
  - Displays transcripts with system/talkgroup filter chips (sorted A–Z, tag-separated), search bar, pull-to-refresh, and infinite scroll
  - Tapping anywhere on a transcript card plays the audio for that call
  - Tab bar updated with a visible bottom border/indicator on the active tab

- **Mobile — Scan Lists (replaces Favorites as multi-list system)**
  - Favorites are preserved in the backend; front-end now exposes them as one of potentially many user-defined "Scan Lists"
  - Users can create, rename, and delete multiple scan lists; each list stores a collection of channels
  - New "Scan Lists" tab in the channel select screen alongside "Channels"
  - Channel items in the Scan Lists tab shown as full-width round-bubble pills with name subtitle, ID badge, and tag-color header grouping; remove icon is red
  - Scan list data persisted server-side via `users.settings` and synced across devices
  - Key-press beep plays when toggling a channel in or out of a list

- **Mobile — Edit List mode in Channels tab**
  - New edit mode in the Channels tab: user selects a scan list, then taps channels to add or remove them from that list
  - Visual checkmark on each channel indicates membership in the selected list
  - Tag group headers show full / partial / no-check state; tapping the tag header bulk-adds or bulk-removes all channels in that tag from the list
  - Sticky banner at the top of the screen shows which list is being edited with a Done button

- **Mobile — Channel tab full-width bubble layout**
  - Replaced the grid layout in the Channels tab with full-width round-bubble list items matching the Scan Lists tab style
  - Each bubble shows the talkgroup label, name subtitle, ID badge, enabled/disabled state, and favorite star
  - Background uses the same black card style as the Scan Lists tab for visual consistency

- **Mobile — Channels tab top section redesign**
  - Compact two-row layout: search bar on top, then a single row with enabled/total count on the left and three icon-only action buttons (toggle all, systems filter, edit list) on the right
  - Replaced the previous cluttered arrangement of separate stat chips and labeled buttons

- **Web Client — Mobile browsers: scanner UI blocked; account hub**
  - Detects common phone/tablet user agents (Android, iPhone, iPad, iPadOS “desktop” Safari with touch, and similar mobile UAs)
  - After login—or whenever the scanner would normally appear—mobile users see a **Mobile Web Hub** instead of the new tabbed scanner or classic `mat-sidenav` layout: brief notice to use the **native app** or a **desktop browser**, Play Store / App Store badges (same store URLs as the existing native snackbar), and **Sign out**
  - When **user registration** and **Stripe paywall** are enabled: **Subscribe**, **Change plan**, and **Manage billing or cancel** (Stripe Customer Portal, including subscription cancellation) mirror the billing actions in App Settings
  - **`/register`** and **`/verify`** are unchanged and remain available on mobile for registration and email verification
  - **System admin** (`/admin`) is not available on mobile browsers (`canActivate` guard redirects to `/`); **group admin** routes are unchanged
  - The delayed “use native app” `MatSnackBar` is not shown on mobile, since the hub already links to the stores

### Changes

- **User Accounts — Force password reset on next login (Issue #23)**
  - Added a per-user `Require Password Reset on Next Login` flag in Admin → Users
  - Admin-triggered password resets now automatically mark the user to change their password at next sign-in
  - User login now returns a password-reset-required state and blocks normal app access until the user sets a new password

- **Web Client — Channel Select: hide system disables its talkgroups**
  - When a system is hidden via the Systems Visibility dialog, all of its talkgroups are immediately forced off in the livefeed map
  - New talkgroups added to a hidden system via config updates are also forced off on merge

- **Mobile — Hide system disables its talkgroups**
  - Hiding a system in the channel select now forces all of its talkgroups off immediately
  - Config update merges also check hidden system state and force new talkgroups in hidden systems off automatically

- **Web Client — Classic view: Channel Select uses new bubble UI**
  - The classic view's channel select sidenav now uses the new `rdio-scanner-select` component (bubbles, Scan Lists, tag grouping) instead of the legacy grid-based select

- **Web Client — Classic view: Recent Alerts beside the scanner (stylesheet)**
  - `RdioScannerMainLegacyComponent` only listed `common.scss` and `main.component.scss` in `styleUrls`, so **`main-legacy.component.scss` was never applied** — the `.scanner-layout` flex row rules lived only in that file, which caused the Recent Alerts column to stack **below** the scanner on all viewports
  - **`./main-legacy.component.scss`** is now included in `styleUrls` so the intended side-by-side scanner + alerts layout is active again

- **Web Client — Config timing fix: buttons and tabs appear without page refresh**
  - Both `main.component` and `main-legacy.component` now seed `this.config` from `rdioScannerService.getConfig()` at `ngOnInit`, immediately after mounting
  - Fixes Alerts, Transcripts, Stats tabs and the legacy Alerts button not appearing until a page refresh when the WebSocket config event fires before the component subscribes

- **Mobile — App Settings: Notification Sounds box colour**
  - The Notification Sounds settings box is now grey (matching all other menu items) instead of black

- **Web Client — Per-channel sounds section styling**
  - Per-channel sounds section redesigned to match the mobile look: collapsible systems, white sound text in dropdowns, cleaner layout consistent with Alert Preferences

### Fixes

- **Admin — Radio Reference import: all talkgroups now imported correctly**
  - "All Categories" checkbox now visually selects all categories in the list immediately on first click
  - Backend now writes talkgroups directly to the database on import rather than staging them, fixing a bug where only 3 of an expected 50+ talkgroups were saved
  - System and site imports use the same direct DB write path
  - UI refreshes the system form data after import so the imported talkgroups are immediately visible without navigating away

- **Web Client — Alert preferences: systems with alerts disabled no longer shown**
  - Systems and talkgroups with `alertsEnabled = false` are now excluded from the user-facing alert preferences list on both web and mobile
  - Does not affect the channel select for live listening — only the alerts/transcription preference UI

- **Web Client — Classic view: Channel Select, Playback, Alerts, Settings buttons now functional**
  - Previously, clicking these buttons emitted `@Output` events that the parent component never handled, so the panels never opened
  - Fixed by wiring the `mat-sidenav` open/close calls in the root `rdio-scanner.component` to handle `(openSelectPanel)`, `(openSearchPanel)`, `(openSettingsPanel)`, and `(openAlertsPanel)` events emitted by the legacy main component — exactly matching the original pre-tab architecture

---

## Version 7.0 Beta 9.7.25 - Released Mar 27, 2026

### Performance

- **Server — Startup time (large systems)**
  - `Systems.Read` now loads all sites, talkgroups, and units in **4 total queries** regardless of system count, down from 3N+1 sequential queries inside a single transaction (e.g. 10 systems: 31 queries → 4 queries)
  - `fetchRadioReferenceAPIKey` moved to a background goroutine — no longer blocks server startup waiting on a network call (up to 10 s saved on cold start)
  - Startup timing now logged: database load time per slow reader (>500 ms) and total server-ready time appear in the event log

- **Server — CPU efficiency**
  - Keyword regex patterns are compiled once per pattern for the lifetime of the process and cached on the `KeywordMatcher` singleton; previously compiled fresh for every keyword on every transcribed call
  - `cleanupOldAlerts` is now rate-limited to at most once per hour via an atomic timestamp; previously a goroutine was launched on every single alert insert, potentially firing dozens of times per minute during tone storms
  - `sendAlertNotification` now snapshots matching clients under the shortest possible lock window and releases the mutex before sending on channels; previously the global clients map lock was held for the entire fan-out
  - ffprobe audio duration computed during tone detection is now propagated back to the original call struct, preventing a redundant ffprobe invocation during transcription duration checks

- **Server — Database hot paths**
  - Group admin subscription status lookup is now O(1) via a `groupAdmins map[uint64]*User` index maintained alongside the users map; previously `GetAllUsers()` was called and scanned linearly on every push notification sent to a billing group
  - Invalid FCM token cleanup is now O(1) via a `tokenIndex map[string]*DeviceToken` keyed by FCM token value; previously a nested scan over all users and all their tokens was performed per invalid token reported by the relay server
  - Keyword match database writes are now batched into a single multi-row `INSERT` per transcription; previously one `INSERT` per match per user
  - Client backlog send (`sendAvailableCallsToClient`) now fetches up to 1000 calls in 3 bulk queries instead of N individual `GetCall` round-trips (each of which opened a transaction and ran 2 queries)

### New

- **TLR Time Sync — standalone clock synchronisation client**
  - New separate tool (`tlr-time-sync`) for keeping SDR-Trunk machine clocks aligned with the TLR server, improving duplicate call detection accuracy
  - Queries the new `GET /api/time` endpoint, applies NTP-style round-trip compensation (offset = serverTime − (t1+t3)/2), and sets the OS system clock
  - Takes multiple samples per sync cycle (configurable, default 4) and uses the lowest-RTT sample for the most accurate measurement
  - Dead-zone filter: offsets within half the RTT are treated as measurement noise and skipped
  - Exponential back-off after repeated failures (5 s → 10 s → 20 s … capped at 10 minutes)
  - Installs as a native system service on all platforms: Windows Service (LocalSystem), Linux systemd, macOS LaunchDaemon — auto-starts at boot, no recurring privilege prompts
  - Configured via `tlr-time-sync.ini` (server URL, sync interval, sample count, failure threshold)
  - Permission-denied errors logged with a clear actionable message
  - Lives in its own repository; excluded from the TLR repo via `.gitignore`

- **Server — `GET /api/time` endpoint**
  - New lightweight endpoint returning the server's current UTC time as a nanosecond Unix timestamp (`{"unix_ns": ...}`)
  - No authentication, no database access — intentionally minimal so it remains accurate under heavy server load
  - Used by the `tlr-time-sync` client; registered outside the rate-limiting and security-header middleware stack

### Changes

- **Server — Push notifications: OneSignal removed, FCM only**
  - OneSignal is no longer supported as a push provider
  - The device registration endpoint now requires `fcm_token` and rejects any registration without one; all remaining legacy OneSignal tokens for the user are deleted on registration
  - At push-send time, any device token without an FCM token (`PushType == "onesignal"` or `FCMToken == ""`) is detected as legacy, deleted from the database immediately, and the user is emailed once per event to update their app — regardless of how many legacy devices they have
  - New email: **"Action Required: Update the App"** — informs the user their push notifications have stopped and instructs them to update from the App Store or Google Play; sent automatically, no admin action required
  - `RemoveAllOneSignalTokensForUser` renamed to `RemoveAllLegacyTokensForUser` with updated detection logic
  - `tokenIndex` on `DeviceTokens` now keys by `FCMToken` so invalid-token responses from the relay server are matched correctly

---

## Version 7.0 Beta 9.7.24 - Released Mar 22, 2026

### Improvements

- **Audio conversion — processing chain overhaul**
  - Audio conversion modes now use a full broadcast-quality processing chain: high-pass filter (120 Hz), 4:1 compressor (8 ms attack / 80 ms release), FFT denoiser (`afftdn`), EQ cut at 250 Hz (−3 dB), presence boost at 3 kHz (+5 dB), low-pass at 3.2 kHz, loudnorm, and a hard limiter at −1 dBFS
  - **Mode 2 (normalization):** loudnorm target −14 LUFS, LRA 11 (natural dynamics)
  - **Mode 3 (loud normalization):** loudnorm target −14 LUFS, LRA 3 (tight broadcast leveling)
  - Output bitrate changed to 48 k AAC (was 32 k)
  - ffmpeg version detection regex aligned with upstream rdio-scanner for consistency
  - Admin UI description simplified to match upstream wording

- **Audio conversion — transcription enhancement option**
  - New admin toggle: **Transcription Audio Enhancement** — pre-processes audio before transcription using noise reduction (`afftdn`) and dynamic compression
  - Outputs 16 kHz mono WAV to the transcription pipeline (optimal format for Whisper, Google, Azure, and AssemblyAI)
  - Processing runs after the transcription snapshot (Stage 3.5) and before AAC conversion (Stage 4), so stored audio and transcription audio are processed independently
  - Has no effect if ffmpeg is not installed

- **Client — Transport & status UI**
  - ThinLine Radio logo moved from toolbar center to the top-right header alongside the Admin and Sign Out buttons
  - Volume slider moved into the toolbar right zone where the logo previously sat
  - Volume control redesigned: flat single-row layout (no box/border) — `Volume` label → speaker icon → slider → percentage — matching the visual style of the status strip meta items

- **Auth screen — dark theme**
  - Full dark theme applied across the entire login / registration screen: `#0f0f0f` screen background, `#1a1a1a` card, `#cc0000` red accents on tabs, form field outlines, and links
  - Angular Material form field outlines, floating labels, and hint text overridden for dark backgrounds via `::ng-deep`
  - ThinLine Radio `logo-banner.png` added as a faint watermark at the bottom of the auth card

- **Auth screen — registration success flow (issue #132)**
  - "Check Your Email" verification notice now appears whenever `emailServiceEnabled` is not explicitly `false` (previously was hidden when the config flag was `undefined` / not yet loaded, leaving users on a blank screen)
  - "No email service" fallback only shown when the flag is explicitly `false`
  - "Continue to Sign In" button always visible after successful registration

- **Admin — Push Notification API key dialog (issue #130)**
  - "Update API Key" button no longer hidden off-screen: dialog panel is now bounded to `90vh` with Angular Material keeping the title and actions bar always in view; content area constrained to `calc(90vh - 130px)` and scrolls independently
  - Removed the "Localhost Access Required" warning notice — the dialog is accessible from any admin session
  - Update flow now judges success on the HTTP status code (`2xx`) rather than a `success` flag in the response body, preventing false "failed to update" errors when the relay server returns `204 No Content`
  - Dialog closes immediately on a successful update (no intermediate "API Key Created" screen); the parent snackbar confirms the result
  - Configuration is automatically saved after a key is generated, updated, or recovered — no manual save step required

- **Admin — Registration codes (issue #138)**
  - Admins can now assign a human-readable **Label** to each registration code for easier tracking
  - Custom registration codes can be entered manually; leave blank to auto-generate
  - New codes appear in the list immediately after creation without closing and reopening the dialog
  - "Add Registration Code" form layout refactored to a two-row grid — eliminates overlap between the "One-time use" checkbox and the "Add Code" button

- **Server — Transcription health checks (issue #139)**
  - Removed `/health` endpoint checks for all OpenAI-compatible transcription providers
  - `IsAvailable()` always returns `true`; errors are handled per-job with retries instead of permanently disabling the transcription queue at startup
  - Allows third-party providers (Groq, Together AI, etc.) that do not expose a `/health` endpoint to work without modification

---

## Version 7.0 Beta 9.7.22 - Released Mar 19, 2026

### Improvements

- **Client — Channels tab**
  - Master–detail layout: searchable system sidebar, single-system detail with tags and talkgroups, full-width dark board styling aligned with other tabs
  - **All systems** / **Favorites** segment for the sidebar list
  - Replaced separate **Fav on** / **Fav off** with one compact **Favs on** / **Favs off** control (toggles all favorited talkgroups; respects partial state)

- **Client — Alert preferences**
  - Same master–detail pattern (sidebar + detail) with search
  - Tag rows: stable `trackBy`, immutable expand-state map updates, and header clicks that ignore All/Some/None and Enable/Disable so expand/collapse works on first try

- **Client — Board tabs & alerts**
  - Main tabs: **Alerts**, **Transcripts**, and **Stats** are top-level tabs after **Channels** (before **Settings**); inner Alerts panel is **Alerts** + **Preferences** only
  - Realtime alert fetch / notification / sound limited to embed and main Alerts+Preferences instances (avoids duplicate toasts when Transcripts/Stats panels are mounted)

- **Client — Transport & status UI**
  - **ThinLine** `logo-banner.png` centered in the flexible area of the transport bar (opaque PNG on dark toolbar)
  - **Replay last** transport button removed
  - **Full screen** remains next to **Hold talkgroup**; **output volume** control moved into the status row (LINK / time / listeners / queue)

---

## Version 7.0 Beta 9.7.21 - Released Mar 14, 2026

### Bug Fixes

- **PostgreSQL: connection pool exhaustion causing database to appear unreachable**
  - Fixed transaction leak in `GetCall()` — when a system or talkgroup lookup failed, the open transaction was never rolled back, holding a DB connection with locks until PostgreSQL timed it out. Under load these would stack up and exhaust the pool
  - Fixed `sendAvailableCallsToClient()` — previously opened up to 1,000 simultaneous transactions (one per call) while holding an outer cursor open, capable of draining the entire connection pool in a single client connect event. Now collects all call IDs first, closes the cursor, then processes sequentially
  - Added `SetConnMaxIdleTime(5 minutes)` to the connection pool — idle connections were previously held open indefinitely (up to 30 minutes), keeping PostgreSQL backend processes alive unnecessarily. Idle connections are now returned after 5 minutes
  - Halved max idle connections from `maxConns` to `maxConns/2` to reduce steady-state PostgreSQL backend load
  - Files modified: `server/call.go`, `server/controller.go`, `server/database.go`

- **PostgreSQL: SQL syntax errors on log inserts with quoted system/talkgroup names**
  - Log messages containing single quotes (e.g. system names like `'OH Cuyahoga GCRN'`, tone set names like `'Brookfield'`, `'Weathersfield 41'`) were breaking the SQL string and causing every log insert to silently fail
  - Root cause: `migrations.go` log migration used `fmt.Sprintf` with `%s` string interpolation instead of parameterized queries
  - Fixed: switched to `$1, $2, $3, $4` parameterized query
  - Files modified: `server/migrations.go`

- **PostgreSQL: partial options commit with no error returned**
  - The `Options.Write()` `set()` closure silently swallowed write errors — if any option key failed to write, the outer `err` variable was overwritten by the next `json.Marshal` call (which almost always succeeds), making the failure invisible. The transaction would then commit successfully with some keys missing
  - Fixed: rewrote `set()` to use a dedicated `setErr` sentinel that stops further writes on first failure, rolls back the transaction, and propagates the error to the caller
  - Also switched all `UPDATE`/`INSERT` queries in `set()` from `fmt.Sprintf` string interpolation to `$1/$2` parameterized queries
  - Files modified: `server/options.go`

- **PostgreSQL: external API data interpolated directly into SQL**
  - `transcription_queue.go`: `result.Language` (from external transcription API) and `result.Confidence` were string-interpolated into INSERT/UPDATE queries via `fmt.Sprintf`. Switched to fully parameterized queries for both PostgreSQL and SQLite paths
  - `call.go`: `AudioFilename`, `AudioMime`, and `TranscriptionStatus` were interpolated into the calls INSERT via `fmt.Sprintf`. Moved to parameterized arguments (`$2`, `$3`, `$6` for PostgreSQL; `?` for SQLite)
  - Files modified: `server/transcription_queue.go`, `server/call.go`

- **Transaction cleanup: missing rollbacks on error paths**
  - `admin.go`: missing `tx.Rollback()` after a failed `tx.Commit()` on user deletion
  - `seeds.go`: missing `tx.Rollback()` after failed `tx.Commit()` in both `seedGroups()` and `seedTags()`
  - Files modified: `server/admin.go`, `server/seeds.go`

---

## Version 7.0 Beta 9.7.20 - Released Mar 11, 2026

### Bug Fixes

- **Docker build: fix `selfsigned@2.4.1` 400 Bad Request from npm registry**
  - The `selfsigned@2.4.1` tarball returns HTTP 400 from the npm registry, breaking `npm install` during Docker builds
  - Added `overrides` in `client/package.json` to pin `selfsigned` to `2.4.0`
  - Updated Dockerfile from `node:16-alpine` (EOL) to `node:18-alpine`
  - Committed `client/package-lock.json` (was previously gitignored) for reproducible Docker builds
  - Files modified: `Dockerfile`, `client/package.json`, `client/package-lock.json`, `.gitignore`

---

## Version 7.0 Beta 9.7.19 - Released Mar 11, 2026

### Improvements

- **Admin: audio conversion quality warning added to Options page**
  - A warning is now displayed on the Audio Conversion setting informing users that enabling conversion on already-compressed source audio (MP3/M4A) will re-encode and degrade quality
  - Recommends only enabling conversion when the source sends raw/WAV audio or normalization is needed
  - Audio conversion settings reverted to rdio-scanner-master defaults (AAC 32kbps, M4A output)
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/options/options.component.html`, `server/version.go`

---

## Version 7.0 Beta 9.7.18 - Released Mar 10, 2026

### New Features

- **Admin: audio fingerprint deduplication controls added to the Options page**
  - The fingerprint dedup toggle, threshold, and time window are now configurable directly from the admin UI under the duplicate detection section
  - Fingerprint deduplication defaults to **off**. Enable it only when multiple feeders upload the same transmission (e.g. two SDRs covering the same system). Single-feeder systems should leave it disabled
  - When enabled, the threshold (Hamming distance 0.0–1.0, default 0.25) and time window (ms, default 5000) are shown
  - Files modified: `client/src/app/components/rdio-scanner/admin/admin.service.ts`, `client/src/app/components/rdio-scanner/admin/config/options/options.component.html`

---

## Version 7.0 Beta 9.7.17 - Released Mar 10, 2026

### Bug Fixes

- **Duplicate detection: fingerprint cache causing false-positive call drops on single-feeder systems**
  - The in-memory fingerprint cache TTL was hardcoded at 30 seconds in `NewController`, meaning it was always 30 seconds regardless of what the admin configured for `audioFingerprintTimeFrame`. The cache was initialized before `Options.Read()` ran, so the admin setting was never applied
  - The 30-second window caused consecutive legitimate calls on the same talkgroup to be compared against each other. On busy channels with short transmissions (1–4 fingerprint integers), the adaptive threshold (+0.15 for <5 integers) was permissive enough to match different calls as duplicates, silently dropping real traffic
  - Fixed: the fingerprint cache is now re-initialized in `Start()` after options load, using `AudioFingerprintTimeFrame` from the admin settings
  - Fixed: `audioFingerprintTimeFrame` default reduced from 30,000ms to 5,000ms
  - Fixed: fingerprint similarity comparison is now skipped when either fingerprint has fewer than 3 integers (< 96 bits) — not enough data for a reliable comparison. Previously these short fingerprints received a +0.15 threshold boost that made false positives near-certain on short radio transmissions
  - Fixed: adaptive threshold boosts reduced — +0.05 for 3–4 integers (was +0.15), +0.03 for 5–9 integers (was +0.08)
  - Fixed: fingerprint comparison is now reliable enough that `audioFingerprintEnabled` remains `true` by default — the root cause was the bugs above, not the feature itself
  - Files modified: `server/controller.go`, `server/defaults.go`, `server/audio_fingerprint.go`

---

## Version 7.0 Beta 9.7.16 - Released Mar 10, 2026

### New Features

- **Duplicate Detection: audio fingerprinting for content-based duplicate suppression**
  - The existing duplicate detection relied purely on metadata (system, talkgroup, and timestamp window). Calls arriving outside the timestamp window, from recorders with slightly mismatched clocks, or with different audio durations could slip through and play for listeners twice
  - Added a spectral audio fingerprinting engine (`server/audio_fingerprint.go`) that generates a compact `[]int32` fingerprint from each call's audio content. The algorithm normalises audio to 11025 Hz mono via FFmpeg (`dynaudnorm`), divides it into overlapping ~0.37 s frames, computes FFT band energies across 8 logarithmically-spaced frequency bands (200–3500 Hz), and encodes temporal spectral changes as bits packed into int32 values. Two recordings of the same transmission — regardless of noise floor, signal strength, or codec differences — produce fingerprints with low Hamming distance; unrelated audio produces ~50% bit difference
  - Duplicate detection now runs in two layers after the existing metadata checks pass:
    - **In-memory cache (race condition layer):** catches simultaneous uploads that arrive before either call is committed to the database. The cache check is atomic under a mutex. Each entry stores both the radio call timestamp and the fingerprint. Two checks are applied: (1) if the incoming call's radio timestamp is within the metadata window of any cached entry on the same system/talkgroup → duplicate, regardless of fingerprint (handles P25 digital audio where different decoders produce spectrally different but identical-content audio); (2) if Hamming distance is below threshold → fingerprint duplicate
    - **Database query (delayed upload layer):** queries stored fingerprints over a wider 30-second window to catch out-of-order or delayed uploads that arrived after the cache TTL expired
  - Fingerprint comparison uses a **bidirectional sliding window** (±5 integer positions ≈ ±4 seconds) so recordings of the same transmission where one recorder started a few seconds earlier or later are correctly identified as duplicates even when both fingerprints are the same length
  - **Adaptive threshold** for short clips: calls under 5 seconds produce only 3–4 fingerprint integers, making raw Hamming distance unreliable. The threshold is automatically relaxed (+0.15 for <5 integers, +0.08 for 5–9 integers) so very short transmissions from different receivers are still correctly matched
  - **Patch talkgroup emit suppression:** when the same transmission arrives on two patched talkgroups (e.g. FINDLAY and POST41), both calls are saved to the database (history preserved on both talkgroups) but only the first is streamed to connected clients. A secondary `EmitFingerprintCache` keyed by system ID compares fingerprints cross-talkgroup at emit time and suppresses the duplicate stream. Logged as `patch duplicate suppressed at emit`
  - Three new admin options (all on by default): **Audio Fingerprint Enabled** (toggle), **Fingerprint Threshold** (Hamming distance 0.0–1.0, default 0.25), **Fingerprint Time Frame** (ms window for DB query, default 30000)
  - The `audioFingerprint` column is added to the `calls` table automatically on startup via the standard migration path
  - Log messages distinguish detection layer: `duplicate (fingerprint-cache)` for the in-memory hit, `duplicate (fingerprint)` for the DB hit, `patch duplicate suppressed at emit` for cross-talkgroup patch suppression
  - Files modified: `server/audio_fingerprint.go` (new), `server/call.go`, `server/controller.go`, `server/options.go`, `server/defaults.go`, `server/migrations.go`, `server/database.go`

---

## Version 7.0 Beta 9.7.15 - Released Mar 8, 2026

### Bug Fixes

- **Upload: `signal_jobid` field not parsed — always stored as empty**
  - The upload parser matched `signal_jobID` (capital ID) but uploaders send `signal_jobid` (all lowercase). The field was visible in raw logs but always showed `SignalJobId=""` in parsed logs and was never stored in the database
  - Added `signal_jobid` as an additional case alongside `signal_jobID` so both capitalizations are accepted
  - Files modified: `server/parsers.go`

- **Upload logging: consolidated from ~20 lines per upload to 2**
  - Each call upload logged one line per multipart header and one per field, producing 20–30 log lines per upload that made logs difficult to read
  - The RAW log now collects all fields into a single pipe-separated line. The PARSED log is also a single line with all metadata. Both `UPLOAD` and `TR-UPLOAD` paths updated
  - Files modified: `server/api.go`

---

## Version 7.0 Beta 9.7.14 - Released Mar 8, 2026

### New Features

- **Talkgroup: cross-channel voice association for agencies that page on one TGID and dispatch on another**
  - Some agencies transmit paging tones on a dedicated signalling channel (TGID A) and then conduct the voice dispatch on a separate tactical or dispatch channel (TGID B). Because the existing pending-tones system keys on the same talkgroup, tones detected on TGID A were never attached to voice calls that arrived on TGID B, resulting in alerts without audio or transcription
  - Added three optional per-talkgroup fields: **Linked Voice Channel** (the radio ID of the voice talkgroup to watch), **Voice Watch Window** (seconds, default 30), and **Minimum Voice Duration** (seconds, to filter mic clicks and squelch tails on the linked channel). When tones fire on a talkgroup that has a Linked Voice Channel configured, the server registers a second pending-tones watch entry keyed to the voice talkgroup. The first voice call arriving on that talkgroup within the watch window — and meeting the minimum duration — automatically receives the tones and triggers the full tone alert with audio and transcription. When claimed, the source talkgroup's own pending entry is also cleared to prevent a duplicate alert. All fields default to `0` which disables the feature; talkgroups without a linked voice channel behave exactly as before
  - Configured per-talkgroup in the admin UI under **Systems → [System] → [Talkgroup] → Linked Voice Channel**. The Voice Watch Window and Minimum Voice Duration fields are only shown when a linked channel is configured
  - Closes [#103](https://github.com/Thinline-Dynamic-Solutions/ThinLineRadio/issues/103)
  - Files modified: `server/tone_detector.go`, `server/talkgroup.go`, `server/controller.go`, `server/migrations.go`, `server/database.go`, `client/src/app/components/rdio-scanner/admin/admin.service.ts`, `client/src/app/components/rdio-scanner/admin/config/systems/talkgroup/talkgroup.component.html`

- **Talkgroup: per-talkgroup alert cooldown to suppress double-page notifications**
  - Departments that always double-page (tone out, message, then repeat) generated two push notifications per incident. There was no way to suppress the second without disabling alerts entirely
  - Added an `alertCooldownSeconds` field to talkgroups. When set to a value greater than zero, tone alert push notifications on that talkgroup are suppressed for the configured number of seconds after the first alert fires. The alert DB record is still written on both pages so history is preserved — only the push notification is held back. Setting the value to `0` (the default) disables the cooldown entirely, preserving existing behaviour for all talkgroups
  - Cooldown state is tracked in memory on the `AlertEngine` and resets on container restart, which is intentional — cooldown windows are short (typically 30–300 seconds) and do not need to survive restarts
  - Configured per-talkgroup in the admin UI under **Systems → [System] → [Talkgroup] → Alert Cooldown**
  - Closes [#113](https://github.com/Thinline-Dynamic-Solutions/ThinLineRadio/issues/113)
  - Files modified: `server/talkgroup.go`, `server/alert_engine.go`, `server/migrations.go`, `server/database.go`, `client/src/app/components/rdio-scanner/admin/admin.service.ts`, `client/src/app/components/rdio-scanner/admin/config/systems/talkgroup/talkgroup.component.html`

### Bug Fixes

- **Admin: system admin SSO — access admin panel directly from the main TLR UI**
  - System admin users (`systemAdmin = true`) previously had to navigate to `/admin` separately and enter the admin password. There was no connection between their TLR user account and the admin panel
  - Added a one-click **Admin** button in the main TLR UI (shown only to system admin users, next to Sign Out). Clicking it exchanges the user's PIN for a short-lived admin JWT via the new `POST /api/admin/sso` endpoint, then opens `/admin?sso_token=...` in a new tab — which logs in automatically the same way Central Management's one-click login does. The Central Management one-click path (`CMAdminTokenHandler`) is completely unaffected
  - Added a **Disable Admin Password Login** toggle in Admin → Options → Admin Access. When enabled, the password form on `/admin` is hidden and all password-based login attempts return 403. Admin access remains available via system admin SSO and Central Management. The toggle includes a lockout warning — it should only be enabled when at least one System Admin user exists
  - `isSystemAdmin` is now included in the `/api/user/login` response so the main UI knows immediately after login whether to show the Admin button, without a separate API call
  - The `isSystemAdmin` flag is persisted in `localStorage` so the Admin button persists across page reloads without re-fetching
  - Closes [#15](https://github.com/Thinline-Dynamic-Solutions/ThinLineRadio/issues/15) (partial — IP allowlist is a separate feature; this closes the SSO access request)
  - Files modified: `server/api.go`, `server/admin.go`, `server/options.go`, `server/main.go`, `client/src/app/pages/rdio-scanner/admin/admin.component.ts`, `client/src/app/components/rdio-scanner/admin/login/login.component.ts`, `client/src/app/components/rdio-scanner/admin/login/login.component.html`, `client/src/app/components/rdio-scanner/admin/admin.service.ts`, `client/src/app/components/rdio-scanner/rdio-scanner.service.ts`, `client/src/app/components/rdio-scanner/auth-screen/auth-screen.component.ts`, `client/src/app/components/rdio-scanner/user-login/user-login.component.ts`, `client/src/app/components/rdio-scanner/main/main.component.ts`, `client/src/app/components/rdio-scanner/main/main.component.html`, `client/src/app/components/rdio-scanner/admin/config/options/options.component.html`

- **Transcription: configurable timeout for slow local Whisper servers**
  - Transcription requests to local Whisper servers timed out after 30 seconds because the HTTP transport's `ResponseHeaderTimeout` was hardcoded to 30 s. Whisper does not send any response headers until transcription is complete, so any call that took longer than 30 seconds to process on a slow CPU or older GPU was silently dropped as a timeout. The overall 5-minute `http.Client.Timeout` was never reached because the transport-level header timeout fired first
  - Added a **Transcription Timeout** setting (seconds) to the Transcription Settings section of the admin UI. This value is applied to both the overall HTTP client timeout and the response-header timeout so they stay consistent. Default remains 300 seconds (5 minutes). Setting to 0 uses the default. Users on slow machines can increase this to 600 seconds or more
  - Closes [#101](https://github.com/Thinline-Dynamic-Solutions/ThinLineRadio/issues/101)
  - Files modified: `server/options.go`, `server/transcription_whisper_api.go`, `server/transcription_queue.go`, `client/src/app/components/rdio-scanner/admin/admin.service.ts`, `client/src/app/components/rdio-scanner/admin/config/options/options.component.html`

- **PostgreSQL: stuck transaction after admin config save silently drops all incoming calls**
  - When a config save in the admin UI triggered a mid-transaction DB error (e.g. a talkgroup referencing a group that was deleted in the same save), a missing `break` in the outer talkgroup loop allowed execution to continue against a transaction that PostgreSQL had already marked aborted (SQLSTATE 25P02). Every subsequent query in that transaction failed with "current transaction is aborted, commands ignored until end of transaction block", masking the real error. The transaction was eventually rolled back, but any `INSERT … RETURNING "talkgroupId"` that ran before the failure had already written a phantom sequence value into the in-memory talkgroup struct. Because neither the admin handler nor the controller called `Systems.Read()` after a failed write, the phantom ID persisted in memory. All subsequent calls for affected talkgroups failed with a foreign key constraint violation (`calls_talkgroupId` SQLSTATE 23503) — audio was received but never stored. A container restart was required to recover
  - Added the missing `if err != nil { break }` after the inner `groupId` loop in `Talkgroups.WriteTx` so a failed `talkgroupGroups` INSERT immediately exits the outer talkgroup loop, preventing further queries against the aborted transaction
  - Added `defer tx.Rollback()` to `Systems.Write()` using the standard Go pattern (calling `Rollback` after a successful `Commit` is a harmless no-op) to ensure the transaction is always cleaned up even in unexpected code paths
  - Both the admin config handler and the controller auto-populate path now call `Systems.Read()` after a failed `Systems.Write()` to restore a consistent in-memory state and clear any phantom IDs before the next call arrives
  - Files modified: `server/talkgroup.go`, `server/system.go`, `server/admin.go`, `server/controller.go`

- **Auto-update: update checker reports "up to date" for versions 7.0.0-beta9.7.10 and above**
  - The pre-release version comparison used a plain string comparison (`cParts[1] > rParts[1]`). Lexicographic ordering treats `"beta9.7.8"` as greater than `"beta9.7.10"` because `"8"` > `"1"` at the differing character position, so any release from `.10` onward was seen as older than `.8` or `.9` and the updater always reported the server as up to date
  - Replaced the string comparison with `comparePreRelease()`, which strips the alphabetic prefix (`beta`) then splits and compares each dot-separated segment as an integer — correctly ranking `.10` > `.8`
  - Files modified: `server/updater.go`

- **System Alerts: `column "dismissedAt" does not exist` error in no-audio, transcription, and tone-detection monitoring**
  - The no-audio, transcription-failure, and tone-detection alert queries referenced a `"dismissedAt"` timestamp column that was never added to the `systemAlerts` table. The table only has a boolean `"dismissed"` column. Every monitoring cycle logged `ERROR: column "dismissedAt" does not exist (SQLSTATE 42703)` and failed to suppress duplicate alerts or dismiss stale ones
  - Replaced all four `"dismissedAt"` references with the correct `"dismissed" = false` / `SET "dismissed" = true` expressions that match the actual schema
  - Files modified: `server/system_alert.go`

- **User Groups: Group Admin assignment UI — dialog CSS not applied, controls rendered unstyled and narrow**
  - All four dialog overlays (`codes-dialog-overlay`) were placed outside the closing `</div>` of `.user-groups-admin` in the template, making them siblings rather than descendants of that element. Because all dialog CSS rules were nested inside `.user-groups-admin` in the component SCSS, Angular's emulated view encapsulation generated selectors requiring a descendant relationship that was never satisfied. As a result no dialog styles applied at all: `position: fixed` was ignored (dialogs rendered inline), `width: 90%` had no effect, and `full-width` on `mat-form-field` elements did nothing — leaving every control squashed to its minimum intrinsic width
  - Moved the closing `</div>` to the end of the template so all dialogs are descendants of `.user-groups-admin`, restoring the correct CSS selector match for all dialog layout rules
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/user-groups/user-groups.component.html`

- **User Groups: Group Admin assignment required two dialog layers and a dropdown click to reach the user list**
  - Clicking "Assign New Group Admin" opened a second `codes-dialog-overlay` on top of the first, doubling the dark backdrop. Inside that second dialog, a `mat-select` required an additional click to open, and the search field was hidden inside the dropdown panel — three interactions before a user was visible
  - Merged the assign flow into the existing Group Admins dialog as a toggled view (`showAssignView` flag). Replaced the `mat-select` + embedded search with an always-visible search bar and a scrollable, filterable user list. Clicking a user row highlights it; a single "Assign Admin" button confirms. The dialog closes automatically on successful assignment or removal
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/user-groups/user-groups.component.html`, `user-groups.component.ts`, `user-groups.component.scss`

- **Admin: version header showed only "v" on initial page load**
  - The admin page header displayed the server version fetched via the config API, but the component initialised `version` as an empty string, so the header read "v" until the first API response arrived
  - Initialised `version` from the bundled `package.json` so a meaningful value is shown immediately, then replaced it with the authoritative server version once the config loads
  - Files modified: `client/src/app/pages/rdio-scanner/admin/admin.component.ts`, `client/package.json`

- **Interactive Setup: `role "thinline_user" does not exist` when creating database**
  - The setup wizard created the database with `OWNER thinline_user` before creating the user, causing `pq: role "thinline_user" does not exist` on first-time setup
  - Reordered operations: create the database user first, then create the database (PostgreSQL requires the owner role to exist)
  - Added password escaping for single quotes to prevent SQL syntax errors when passwords contain `'`
  - Files modified: `server/setup.go`

- **Logs: SQL syntax error when system name contains a single quote**
  - `LogEvent` built its INSERT using `fmt.Sprintf` with `'%s'` placeholders, so any log message containing a single quote (e.g. `no-audio monitoring not started for system 'Waverly Public Schools'`) produced malformed SQL and the log entry was dropped with a PostgreSQL syntax error
  - Replaced the raw string-interpolated query with a parameterized `$1/$2/$3` query so the driver handles escaping safely
  - Closes [#119](https://github.com/Thinline-Dynamic-Solutions/ThinLineRadio/issues/119)
  - Files modified: `server/log.go`

- **AssemblyAI: transcription fails with status 400 when using `universal-3-pro` model**
  - The transcription provider always sent `word_boost` regardless of model, but AssemblyAI's `universal-3-pro` does not support `word_boost` and returns HTTP 400. The correct parameter for that model is `keyterms_prompt`
  - The provider now checks the selected speech model and sends `keyterms_prompt` for `universal-3-pro` and `word_boost` for all other models (e.g. `universal-2`). The same terms list is used for both — no configuration change required
  - Closes [#118](https://github.com/Thinline-Dynamic-Solutions/ThinLineRadio/issues/118)
  - Files modified: `server/transcription_assemblyai.go`

- **AssemblyAI: word boost terms repopulate after being cleared and saved**
  - When the word boost textarea was emptied and the config was saved, the terms reappeared on next page load. The save handler only converted the textarea string to an array when the value was truthy, so an empty string was left unconverted. The backend received a string instead of an empty array, failed the type assertion, and kept the previous list unchanged
  - Removed the truthy guard so an empty string is always split and filtered, producing an empty array `[]` that correctly clears the stored list
  - Closes [#118](https://github.com/Thinline-Dynamic-Solutions/ThinLineRadio/issues/118)
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/config.component.ts`

- **Tools > Radio Reference Import: State/Province dropdown always disabled after UI overhaul**
  - The State/Province dropdown used `[disabled]="!selectedCountry"` (the full country object), while every other cascading dropdown in the same form correctly used the ID primitive: County uses `[disabled]="!selectedStateId"`, System uses `[disabled]="!selectedCountyId"`. The object-based condition is fragile — object identity can be lost on saved-state restore, and a UI refactor can easily leave the reference `null` — causing the State dropdown to remain permanently disabled even after a country was selected
  - Changed to `[disabled]="!selectedCountryId"` to match the consistent pattern used by the County and System dropdowns
  - Also added `selectedCountry` to `saveState()` / `restoreState()` so the Country `mat-select` itself reflects the correct saved selection on return visits
  - Files modified: `client/src/app/components/rdio-scanner/admin/tools/radio-reference-import/radio-reference-import.component.html`, `radio-reference-import.component.ts`

- **Admin: dragging a sortable row when clicking inside a text field moves the row instead of selecting text**
  - In the API Keys, Groups, and Tags tables each row has `cdkDrag` applied directly to the `mat-row`. CDK drag registers a global mousedown listener on the entire row, so clicking inside a text field and dragging to select text was interpreted as a row drag — the row moved and text selection was impossible
  - Added `(mousedown)="$event.stopPropagation()"` to every `td` that contains an editable `mat-form-field`, so the CDK drag listener never sees mousedown events originating inside those cells. The drag grip icon is unaffected — dragging from the `drag_indicator` icon still reorders rows normally
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/apikeys/apikeys.component.html`, `config/groups/groups.component.html`, `config/tags/tags.component.html`

- **Admin: select/dropdown option text unreadable on hover (dark text on dark background)**
  - The CDK overlay container is rendered in `<body>` outside the `.admin-dark-theme` host element, so Material's dark theme colours never reach overlay panels. The `separated-options` hover style set a dark `#2a2a2a` background without overriding text colour, producing dark text on a dark background
  - Added a global `.admin-select-panel` CSS class in `styles.scss` (dark background, light text, correct hover and selected states). Provided `MAT_SELECT_CONFIG` in `admin.module.ts` so every `mat-select` in the admin module automatically receives this panel class without requiring per-template changes. The RR import location dropdowns carry both `admin-select-panel` and `separated-options` for their additional border decoration
  - Files modified: `client/src/styles.scss`, `client/src/app/components/rdio-scanner/admin/admin.module.ts`, `admin/tools/radio-reference-import/radio-reference-import.component.html`, `radio-reference-import.component.scss`

- **Tools > Import Units / Import Talkgroups: saving after import attempts to delete all users**
  - Every config event emitted from the Tools panel was passed with `{ isImport: true }`, which marked the config form as a full destructive import. When the user clicked Save after importing units or talkgroups, the backend entered the full-import code path and attempted to delete every user account — failing with PostgreSQL foreign key violations on `deviceTokens` and `userAlertPreferences`
  - The full-import flag is only appropriate for the Import/Export Config full restore, which already calls `saveConfig(config, true)` directly and does not use this event. Changed the Tools panel config event to `isImport: false` so Import Units and Import Talkgroups saves only update system/unit/talkgroup data and never touch users
  - Files modified: `client/src/app/components/rdio-scanner/admin/admin.component.html`

---

## Version 7.0 Beta 9.7.12 - Released Mar 4, 2026

### Bug Fixes

- **Billing: Tax Collection Mode not showing saved value after page refresh**
  - `taxMode` and `stripeTaxRateId` were missing from `newUserGroupForm()` in `admin.service.ts`, so when the admin page reloaded and rebuilt the form from the API response those fields were never populated — the dropdown always fell back to "None" regardless of what was saved in the database
  - Added `collectSalesTax`, `taxMode`, and `stripeTaxRateId` to `newUserGroupForm()` so saved values are correctly restored on reload
  - Files modified: `client/src/app/components/rdio-scanner/admin/admin.service.ts`

- **Billing: Apply Tax Rate to Existing Subscribers fails with Stripe error `invalid_request_error`**
  - Stripe rejects adding manual tax rates (`DefaultTaxRates`) to a subscription that already has `automatic_tax[enabled]=true` — the two modes are mutually exclusive
  - The apply-tax-rate handler now disables `AutomaticTax` on the subscription at the same time as setting `DefaultTaxRates`, resolving the conflict in a single Stripe API call
  - Files modified: `server/api.go`

---

## Version 7.0 Beta 9.7.11 - Released Mar 4, 2026

### New Features

- **Billing: Flexible tax collection — Automatic or Fixed Rate per User Group**
  - The previous `Collect Sales Tax` checkbox (which enabled Stripe Automatic Tax and required customers to enter their billing address at checkout) has been replaced with a **Tax Collection Mode** dropdown per User Group
  - Three modes available:
    - **None** — No tax collected (default)
    - **Automatic** — Stripe Automatic Tax; calculates tax based on customer billing address entered at checkout. Requires origin address configured in Stripe Tax dashboard settings
    - **Fixed Rate** — Apply a specific Stripe Tax Rate ID (e.g. `txr_xxx`) at a fixed percentage. No customer address required at checkout
  - Existing groups that had `Collect Sales Tax` enabled are automatically migrated to **Automatic** mode — no data loss, changeable at any time
  - For Fixed Rate mode: a **"Apply Tax Rate to Existing Subscribers"** button appears when editing a group, allowing admins to push the tax rate to all current active Stripe subscriptions in one click. Tax is applied on the next invoice cycle, not retroactively
  - New API endpoint: `POST /api/admin/groups/apply-tax-rate`
  - Files modified: `server/user_group.go`, `server/migrations.go`, `server/database.go`, `server/postgresql.go`, `server/admin.go`, `server/api.go`, `server/main.go`, `client/src/app/components/rdio-scanner/admin/admin.service.ts`, `client/src/app/components/rdio-scanner/admin/config/user-groups/user-groups.component.ts`, `client/src/app/components/rdio-scanner/admin/config/user-groups/user-groups.component.html`

---

## Version 7.0 Beta 9.7.7 - Released Mar 3, 2026

### Bug Fixes

- **Web: P25 talker aliases not showing — radio ID displayed instead of alias name**
  - The web client resolved unit labels exclusively from the admin-configured static unit table (`systemData.units`), which contains manually entered radio IDs. P25 talker aliases are *dynamic* — they are broadcast by the radio at transmission time and arrive embedded in the call metadata (`sources[].tag`). The static table never has them, so the web always fell back to the raw radio ID
  - Server-side: added `Label` field to `CallUnit` struct; all three call-upload parsers (`sources`, `units`, `srcList`) now populate `unit.Label` directly when a `tag`/`label` field is present
  - Server-side: `call.MarshalJSON` now includes `"tag"` in each source entry when a label is set, so the alias travels with the call to the web client
  - Database: added `label` column to the `callUnits` table (migration `20260303000000-callunits-label`); labels are now persisted on write and loaded on read so historical/search calls also display the correct alias
  - Web client: `updateDisplay()` in `main.component.ts` now checks `source.tag` first; falls back to the static unit table lookup only when no inline alias exists
  - Also removed a leftover `console.log('here', ...)` debug statement from the same function
  - Closes [#111](https://github.com/Thinline-Dynamic-Solutions/ThinLineRadio/issues/111)
  - Files modified: `server/call.go`, `server/parsers.go`, `server/migrations.go`, `server/database.go`, `client/src/app/components/rdio-scanner/main/main.component.ts`

- **Whisper API: GPT transcribe models fail with `response_format 'verbose_json' is not compatible`**
  - `gpt-4o-transcribe` and `gpt-4o-mini-transcribe` only accept `response_format: "json"` or `"text"` — sending `"verbose_json"` (used by `whisper-1` and local Whisper servers for segment timestamps) causes HTTP 400
  - Added `isGPTTranscribeModel()` helper that detects GPT transcribe model names; those requests now use `response_format: "json"` and omit `timestamp_granularities[]` (also unsupported by these models)
  - Response parsing is now branched: GPT transcribe models parse the simple `{"text":"..."}` response; all other models continue parsing the full `verbose_json` structure with segments, language, and duration
  - Closes [#114](https://github.com/Thinline-Dynamic-Solutions/ThinLineRadio/issues/114)
  - Files modified: `server/transcription_whisper_api.go`

- **Admin: API key cell is truncated and read-only**
  - The API key column displayed keys in a `<code>` element capped at `max-width: 220px` with `text-overflow: ellipsis`, cutting off the key visually with no way to see the full value or edit it
  - Replaced the static `<code>` display with a proper `mat-form-field` text input bound to `formControlName="key"` — the key is now fully visible, editable, selectable, and copyable
  - Input uses `type="password"` when masked and `type="text"` when revealed (show/hide button unchanged); the clipboard copy button remains as a convenience
  - Closes [#115](https://github.com/Thinline-Dynamic-Solutions/ThinLineRadio/issues/115)
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/apikeys/apikeys.component.html`, `client/src/app/components/rdio-scanner/admin/config/apikeys/apikeys.component.scss`

- **AssemblyAI: `speech_models` is now required — re-added with `universal-2` default**
  - AssemblyAI's API now returns HTTP 400 with *"speech_models must be a non-empty list"* when the field is omitted entirely (the behavior introduced in 9.7.4 of not sending it at all)
  - Re-added `speech_models` as a required field sent on every transcription request, defaulting to `"universal-2"` when no model is configured
  - Restored the configurable **Speech Model** field in Admin → Options → Transcription (AssemblyAI section) with autocomplete for `universal-2` and `universal-3-pro`
  - Files modified: `server/transcription_assemblyai.go`, `server/transcription_provider.go`, `server/options.go`, `server/transcription_queue.go`, `client/src/app/components/rdio-scanner/admin/admin.service.ts`, `client/src/app/components/rdio-scanner/admin/config/options/options.component.html`

- **System Health Alerts: Master toggle not persisting across restarts/saves**
  - `systemHealthAlertsEnabled` (the master on/off toggle) was missing from `Options.FromMap`, so every time any admin settings were saved the full options write would silently reset it back to `true` (the default), ignoring whatever was stored in the database
  - The three individual toggles (`transcriptionFailureAlertsEnabled`, `toneDetectionAlertsEnabled`, `noAudioAlertsEnabled`) were already correctly handled and were not affected
  - Files modified: `server/options.go`

- **Transcription debug log: provider-aware logging**
  - The error debug log always showed `apiURL=<WhisperAPIURL>` regardless of which transcription provider was in use, causing misleading log lines like `apiURL=http://localhost:8000` when the actual failing provider was AssemblyAI
  - The connection error hint also always said "Check if Whisper API server is overloaded" even for AssemblyAI/Azure/Google failures
  - Log lines now include `provider=<name>` and only show `apiURL` for Whisper API; connection error hints are provider-aware
  - Files modified: `server/transcription_queue.go`

---

## Version 7.0 Beta 9.7.5 - Released Mar 2, 2026

### New Features

- **TonesToActive Per-Channel & Per-Tone-Set Downstream Forwarding**
  - Added per-channel TonesToActive forwarding to the talkgroup configuration: each talkgroup can now independently enable forwarding of tone alerts to a TonesToActive server with its own destination URL and API key
  - Added per-tone-set forwarding controls inside each tone set definition, allowing fine-grained control over which specific tone sets trigger a downstream forward
  - When a tone alert fires, `alert_engine.go` now calls `dispatchToneDownstreams` which checks both channel-level and tone-set-level forwarding settings and sends the tone set name, transcript, and raw audio to the configured TonesToActive endpoint
  - New `tone_downstream.go` handles all dispatch logic including HTTP multipart POST to the TonesToActive `/api/tone-alert` endpoint with the `X-API-Key` header
  - Database migration adds `toneDownstreamEnabled`, `toneDownstreamURL`, and `toneDownstreamAPIKey` columns to the `talkgroups` table (PostgreSQL and SQLite, backward compatible with `DEFAULT` values)
  - Admin UI: talkgroup editor now shows a "Forward to TonesToActive" section (channel-level) when tone detection is enabled, and a per-tone-set forwarding panel inside each tone set row
  - Files modified: `server/talkgroup.go`, `server/tone_detector.go`, `server/alert_engine.go`, `server/migrations.go`, `server/postgresql.go`, `server/system.go`, `server/tone_downstream.go` (new), `server/options.go`, `client/src/app/components/rdio-scanner/admin/admin.service.ts`, `client/src/app/components/rdio-scanner/admin/config/config.component.ts`, `client/src/app/components/rdio-scanner/admin/config/systems/talkgroup/talkgroup.component.html`, `client/src/app/components/rdio-scanner/admin/config/systems/talkgroup/talkgroup.component.ts`

### Other Changes

- **`.gitignore`**: Added `TonesToActive/` to exclude the TonesToActive sidecar project from the main repo (managed independently)

---

## Version 7.0 Beta 9.7.4 - Released Mar 1, 2026

### Bug Fixes

- **AssemblyAI: Remove speech model field entirely — revert to API default**
  - Removed the configurable speech model feature introduced in 9.7.0 due to ongoing AssemblyAI API instability around the `speech_models` parameter
  - The server no longer sends any `speech_models` field in transcription requests; AssemblyAI will use its own default model automatically (the same behavior as before 9.7.0)
  - Removed the Speech Model input from the admin options page and all supporting backend code
  - Files modified: `server/transcription_assemblyai.go`, `server/transcription_queue.go`, `server/options.go`, `client/src/app/components/rdio-scanner/admin/admin.service.ts`, `client/src/app/components/rdio-scanner/admin/config/options/options.component.html`

---

## Version 7.0 Beta 9.7.3 - Released Mar 1, 2026

### Bug Fixes

- **AssemblyAI: Gracefully handle stale/unrecognized speech model names**
  - If a user had an old model value (e.g. `best`, `nano`, `slam-1-5`) saved in their config from before the AssemblyAI API change, the server would send it as `["best"]` which the API rejects with HTTP 400
  - Added a whitelist check: only `universal-3-pro` and `universal-2` are sent; any other stored value is silently ignored and the API default is used instead, with a warning logged
  - **Action required for affected users:** Go to Admin → Options → Transcription → AssemblyAI Speech Model and set it to `universal-2` or `universal-3-pro` (or leave it blank)
  - Files modified: `server/transcription_assemblyai.go`

---

## Version 7.0 Beta 9.7.2 - Released Mar 1, 2026

### Bug Fixes

- **AssemblyAI: `speech_models` must be sent as an array**
  - AssemblyAI's API requires `speech_models` to be a JSON array (e.g. `["universal-3-pro"]`), not a plain string — sending a string caused HTTP 400: *"speech_models must be a non-empty list containing one or more of: universal-3-pro, universal-2"*
  - Updated the Go request body to wrap the configured model in a `[]string{}` slice
  - Updated the admin UI autocomplete to show the correct current model names: `universal-3-pro` and `universal-2`
  - Files modified: `server/transcription_assemblyai.go`, `client/src/app/components/rdio-scanner/admin/config/options/options.component.html`

---

## Version 7.0 Beta 9.7.1 - Released Mar 1, 2026

### Bug Fixes

- **AssemblyAI: Fix deprecated `speech_model` API field**
  - AssemblyAI renamed the transcript request field from `speech_model` to `speech_models` (plural) in their current API, causing all transcription jobs to fail with HTTP 400
  - Updated the request body to use `speech_models` per AssemblyAI's current documentation
  - Files modified: `server/transcription_assemblyai.go`

---

## Version 7.0 Beta 9.7.0 - Released Mar 1, 2026

### New Features

- **AssemblyAI: Configurable Speech Model**
  - Added a **Speech Model** input field in the admin options page under the AssemblyAI transcription provider settings
  - Allows selecting or typing any AssemblyAI model name (e.g. `best`, `nano`, `slam-1-5`) without needing a server update when AssemblyAI changes their available models
  - Ships with an autocomplete dropdown listing the three most common models for quick selection; free-text entry supports any future model names
  - Leaving the field blank continues to use AssemblyAI's API default
  - Files modified: `server/options.go`, `server/transcription_assemblyai.go`, `server/transcription_queue.go`, `client/src/app/components/rdio-scanner/admin/admin.service.ts`, `client/src/app/components/rdio-scanner/admin/config/options/options.component.html`

---

> ⚠️ **Beta / Work in Progress** — Features in this release are actively being developed and refined. Some functionality may be incomplete or subject to change.

### New Features

- **Stats Dashboard: Today's Incident Summary panel** *(WIP)*
  - New collapsible incident summary panel on the Stats tab, powered by real-time keyword analysis of call transcripts
  - Six parent categories, each color-coded with emoji identifiers: 🔥 Fire, ☣️ Hazmat, 🚑 Medical / EMS, 🚔 Crime, 🚗 Traffic, ⚠️ Disturbance
  - Click any category row to expand a sub-breakdown (e.g. Fire → Structure Fire, Brush/Wildland, Vehicle Fire, Fire Alarm; Traffic → MVA/Crash, Traffic Stop/Plate, Road Hazard)
  - Inline proportional bar charts for both parent categories and sub-categories
  - Fully respects the system filter dropdown — all counts reflect the selected system (or all systems)
  - Panel is positioned at the top of the Stats tab, directly under the system filter, for immediate visibility
  - Only categories with at least one matching call today are displayed
  - Files modified: `server/api.go`, `client/src/app/components/rdio-scanner/alerts/alerts.component.ts`, `client/src/app/components/rdio-scanner/alerts/alerts.component.html`, `client/src/app/components/rdio-scanner/alerts/alerts.component.scss`

- **Stats Dashboard: Calls per Minute and Calls per Hour counters**
  - Replaced the previous "Calls / 5 min" counter with two more meaningful live counters: **Calls / Min** (calls in the last 60 seconds) and **Calls / Hour** (calls in the last 60 minutes)
  - Backend `StatsHandler` now returns `callsLastMinute` and `callsLastHour` fields
  - Files modified: `server/api.go`, `client/src/app/components/rdio-scanner/alerts/alerts.component.ts`, `client/src/app/components/rdio-scanner/alerts/alerts.component.html`, `client/src/app/components/rdio-scanner/alerts/alerts.component.scss`

### Improvements

- **Stats Dashboard: Incident Summary repositioned to top**
  - Moved the 📋 Today's Incident Summary panel to appear immediately below the system filter bar, making high-level incident categories the first thing visible when opening the Stats tab
  - Files modified: `client/src/app/components/rdio-scanner/alerts/alerts.component.html`

- **Stats Dashboard: Emoji icons throughout Incident Summary**
  - Replaced Material icons with native emojis for category icons and expand/collapse chevrons (▲/▼), improving visual clarity and reducing dependency on icon font rendering
  - Files modified: `client/src/app/components/rdio-scanner/alerts/alerts.component.ts`, `client/src/app/components/rdio-scanner/alerts/alerts.component.html`, `client/src/app/components/rdio-scanner/alerts/alerts.component.scss`

---

## Version 7.0 Beta 9.6.8 - Released Feb 27, 2026

### Bug Fixes

- **System Health Alerts: No-audio and health alerts not delivered to all devices**
  - `SendSystemAlertNotification` was collecting all registered device tokens but then sending every token — regardless of platform — in a single batch hardcoded as `"android"`. iOS devices received a payload formatted for Android, which the relay server / FCM silently dropped, so only Android devices ever received system health alerts
  - Fixed by mirroring the same per-platform grouping logic used by the regular tone/talkgroup alert path: device tokens are now grouped by their stored `platform` field (`"ios"` / `"android"`), a separate batch is dispatched for each platform, iOS sound names have the file extension stripped as required by APNs, and each batch fires in its own goroutine with a 200 ms stagger to avoid relay-server rate limiting
  - Admins signed into the same account on multiple devices (mixed iOS and Android) will now receive system health and no-audio alerts on all devices, consistent with tone/talkgroup alerts
  - Files modified: `server/system_alert.go`

## Version 7.0 Beta 9.6.7 - Released Feb 27, 2026

### Bug Fixes

- **Admin — Logs: HTTP 417 caused by corrupt timestamp values in the logs table**
  - The real cause of the persistent 417 error was identified as corrupt log rows where the `timestamp` column was stored in the wrong unit (microseconds or nanoseconds instead of milliseconds). When those rows were converted via `time.UnixMilli()` they produced years far beyond 9999 (e.g. year ~58,000), which caused `time.Time.MarshalJSON` to return an error. That error propagated from `json.Marshal` in the handler, which responded with HTTP 417
  - Fixed with two layers of protection:
    1. **SQL filter** — a `AND "timestamp" > 0 AND "timestamp" < 253402300800000` clause is now always added to the search query, preventing out-of-range rows from being fetched at all
    2. **Go guard** — after `time.UnixMilli` conversion, a year-range check (`year < 1 || year > 9999`) skips any row that still somehow slips through rather than aborting the entire response
  - Corrupt rows are left in place but silently excluded; they can be permanently removed with `DELETE FROM logs WHERE timestamp <= 0 OR timestamp >= 253402300800000`
  - Files modified: `server/log.go`

## Version 7.0 Beta 9.6.6 - Released Feb 27, 2026

### Improvements

- **Transcription: Configurable OpenAI-compatible model**
  - The `whisper-api` transcription provider previously hardcoded the model name `whisper-1` in every request, making it impossible to use newer or alternative models without rebuilding the server
  - Added a `whisperAPIModel` field to `TranscriptionConfig`. The model defaults to `whisper-1` when left blank so existing configurations are unaffected
  - The admin UI **Transcription → Model** field is now a free-text input with autocomplete suggestions grouped by provider:
    - **OpenAI Cloud** (`api.openai.com`): `whisper-1`, `gpt-4o-transcribe`, `gpt-4o-mini-transcribe`
    - **Groq Cloud** (`api.groq.com/openai`): `whisper-large-v3`, `whisper-large-v3-turbo`, `distil-whisper-large-v3-en`
    - Any other model name can be typed freely for self-hosted or third-party OpenAI-compatible endpoints
  - Files modified: `server/options.go`, `server/transcription_whisper_api.go`, `server/transcription_queue.go`, `client/src/app/components/rdio-scanner/admin/admin.service.ts`, `client/src/app/components/rdio-scanner/admin/config/options/options.component.html`, `client/src/app/shared/material/material.module.ts`

- **Admin — Options: External Management API section**
  - Added a new **External Management API** section at the bottom of the Options tab allowing server owners to enable/disable the inbound webhook API, manage the API key, and test connectivity without exposing internal Central Management pairing details
  - The API key field supports visibility toggle and one-click cryptographic key generation (`window.crypto.getRandomValues`, 256-bit hex)
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/options/options.component.html`, `client/src/app/components/rdio-scanner/admin/config/options/options.component.ts`

### Bug Fixes

- **Admin — Options: Generated External Management API key now visible immediately**
  - After clicking "Generate", the key field stayed in `type="password"` mode so the new value was invisible
  - The field now switches to visible mode automatically on generation; the eye-icon toggle lets the user re-hide it afterwards
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/options/options.component.html`, `client/src/app/components/rdio-scanner/admin/config/options/options.component.ts`

- **Admin — API Keys / Options: Icon button hover circle now correctly centred**
  - Material MDC icon buttons have a `--mdc-icon-button-state-layer-size` CSS variable (default 40 px) that controls the hover ripple circle independently of the element's `width`/`height`. When only `width`/`height` were overridden the visible button shrank but the hover target stayed 40 px, causing the hover highlight to appear off-centre
  - Fixed by setting `--mdc-icon-button-state-layer-size` to match the actual button size so the ripple is centred correctly
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/apikeys/apikeys.component.scss`

- **Admin — System Health: Removed obsolete "Multiplier" help text**
  - The settings description panel still referenced the adaptive-threshold multiplier formula (`threshold = max(base threshold, historical average × multiplier)`) which is no longer used
  - Removed the entire multiplier help block from the UI
  - Files modified: `client/src/app/components/rdio-scanner/admin/system-health/system-health.component.html`

- **Admin — Logs: Log viewer no longer returns 417 on large log tables (Issue #112)**
  - The logs search handler was performing three sequential full-table scans before returning any data: `SELECT MIN(timestamp)`, `SELECT MAX(timestamp)`, and `SELECT COUNT(*)`. On tables with millions of rows and no index these queries timed out, causing `Logs.Search` to return an error and the handler to respond with HTTP 417
  - Added a `CREATE INDEX CONCURRENTLY "logs_timestamp_idx" ON "logs" ("timestamp" DESC)` migration that runs on startup. The index is built without holding a write lock on the logs table so the server stays fully responsive during the build. A `pg_indexes` existence check makes the migration a no-op on subsequent startups
  - Rewrote `Logs.Search` to mirror the calls search pattern: the three expensive pre-queries are eliminated entirely, a 30-second `context.WithTimeout` is applied to the main query, and `limit+1` row fetching is used to detect `HasMore` without a `COUNT(*)`
  - When no date filter is selected and sort order is descending (newest-first), a 24-hour default lookback is applied automatically so the query uses the new index instead of scanning the entire table — exactly the same optimisation already applied to the calls search
  - `LogsSearchResults.Count` is now computed as `offset + returned rows (+1 if HasMore)` so the admin UI paginator continues to show a next-page button correctly without the real total
  - Files modified: `server/log.go`, `server/migrations.go`, `server/database.go`, `server/postgresql.go`

- **Admin — System Health: `noAudioAlertsEnabled` reset to `true` after any config save**
  - When the admin saved any configuration change the backend called `Systems.FromMap(v)` which defaults `NoAudioAlertsEnabled` to `true` when the key is absent from the payload. Standard config-editor payloads (talkgroup edits, etc.) do not include the System Health tab fields, so every save silently overwrote a user's "disabled" setting back to enabled
  - Fixed by preserving the existing per-system `noAudioAlertsEnabled` and `noAudioThresholdMinutes` values from the database before calling `FromMap`, and writing them back into the parsed map for any system whose payload omits those keys
  - Files modified: `server/admin.go`

### Improvements

- **Admin — Logs: Keyword search and improved time display**
  - Added a "Search messages" text field to the logs filter bar; typing a keyword (e.g. `failed`, `tone detection`, a system name) filters results server-side using a case-insensitive substring match (`ILIKE`) on the message column. SQL wildcard characters in the search term are escaped so they are treated as literals
  - Keyword search is applied after the timestamp index narrows the scan to the active date window, so performance is not impacted
  - Time column now shows seconds (`HH:mm:ss`) instead of just hours and minutes, making it practical to correlate rapid sequences of events
  - Search field submits on `Enter` or field blur and is cleared by the existing Reset button
  - Files modified: `server/log.go`, `central-management/frontend/src/app/components/rdio-scanner/admin/admin.service.ts`, `central-management/frontend/src/app/components/rdio-scanner/admin/logs/logs.component.ts`, `central-management/frontend/src/app/components/rdio-scanner/admin/logs/logs.component.html`

## Version 7.0 Beta 9.6.5 - Released Feb 24, 2026

### Bug Fixes

- **Mobile Login Screen: Header/logo no longer cut off on iOS mobile browsers (Issue #109)**
  - The `<meta name="viewport">` tag was missing `viewport-fit=cover`, preventing `env(safe-area-inset-top)` from working
  - `mat-sidenav-content` uses `align-items: center` for the main scanner UI; the auth-screen component was treated as a centered flex child, so when the visible viewport was smaller than `100vh` (browser chrome eating into it), the top of the card was clipped above the scroll origin and unreachable
  - Fixed by adding `align-self: flex-start` to the auth-screen `:host` so it anchors to the top of the container instead of being vertically centered
  - Added `padding-top: calc(40px + env(safe-area-inset-top, 0px))` to clear the iOS notch and status bar on both desktop and the `@media (max-width: 480px)` breakpoint
  - Added `min-height: 100dvh` alongside `100vh` to use the dynamic viewport height on modern browsers
  - Files modified: `client/src/index.html`, `client/src/app/components/rdio-scanner/auth-screen/auth-screen.component.scss`

- **Mobile Login Screen: Auth card is now correctly centered horizontally**
  - The card was visually shifted slightly to the right due to flex centering being susceptible to scrollbar gutters and Angular Material's dynamic margin injection on `mat-sidenav-content`
  - Replaced flex-based centering with `margin: 0 auto` directly on `.auth-container` (the classic block-level auto-margin technique), which is immune to parent flex container behavior
  - Changed `.auth-screen` from `display: flex` to `display: block` since centering is now handled by the container's own margin
  - Files modified: `client/src/app/components/rdio-scanner/auth-screen/auth-screen.component.scss`

- **Sign-Up: "View Available Channels" count now shows total talkgroups, not number of systems**
  - The button label showed `availableChannels.length` which counted the number of *systems* in the response, not the individual talkgroups within them
  - Added `getTotalChannelCount()` method that sums `system.talkgroups.length` across all systems
  - Files modified: `client/src/app/components/rdio-scanner/auth-screen/auth-screen.component.ts`, `client/src/app/components/rdio-scanner/auth-screen/auth-screen.component.html`

- **Admin — Systems: Manual drag-and-drop sorting restored (Issue #110)**
  - The systems overview table lost `cdkDropList`/`cdkDrag` support during the Beta 9.6 rewrite; the `order` field still existed on each system's `FormGroup` but there was no UI to change it
  - Restored a `drag_indicator` handle column, `cdkDropList` on the table, `cdkDrag` on each row, and a `dropSystem()` method that mirrors the existing `dropTalkgroup`/`dropSite` pattern: moves the item in the displayed array, rewrites each system's `order` field (1-based), and marks the form dirty
  - Drag is automatically disabled when a search filter is active (`[cdkDropListDisabled]="!!systemsSearchTerm"`) since reordering a filtered subset would produce incorrect order values for hidden rows; a tooltip "Clear search to reorder" is shown instead
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/systems/systems.component.ts`, `client/src/app/components/rdio-scanner/admin/config/systems/systems.component.html`, `client/src/app/components/rdio-scanner/admin/config/systems/systems.component.scss`

- **Admin — Talkgroups: Delete now removes the correct talkgroup; Select All checkboxes now render correctly**
  - **Delete bug**: The inline delete button used `let i = index` (positional index in `filteredTalkgroups`) and passed it to `arr.removeAt(i)` on the full `FormArray`. When a search filter is active, `filteredTalkgroups[i]` and `talkgroups[i]` are different items — the delete always acted on the first item in the *full* sorted list (the "top one"), not the clicked row
  - **Select All visual bug**: `isTalkgroupSelected(i)` and `toggleTalkgroupSelection(i)` also relied on the same potentially wrong positional index `i`, causing individual row checkboxes to appear unchecked even though `allTalkgroupsSelected` reported true
  - Fixed by eliminating all positional-index usage in the select and actions columns; the HTML now passes the `FormGroup` object reference (`tg`) directly; TypeScript methods (`isTalkgroupSelected`, `toggleTalkgroupSelection`, `removeTalkgroup`, `blacklistTalkgroup`) perform reference-based lookups using `talkgroups.indexOf(tg)` and `arr.controls.indexOf(tg)`, which are immune to filtered-index drift
  - `allTalkgroupsSelected` now checks whether all *currently visible* (filtered) talkgroups are selected, making the header checkbox reflect the filtered view correctly
  - `selectAllTalkgroups()` now selects only the currently visible (filtered) talkgroups
  - Removed the now-unnecessary `_fullTalkgroupIdx` private helper
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/systems/system/system.component.ts`, `client/src/app/components/rdio-scanner/admin/config/systems/system/system.component.html`

## Version 7.0 Beta 9.6.4 - Released Feb 23, 2026

### Bug Fixes

- **Auto-Update Linux: Server now restarts automatically after update**
  - Previously the server would download, replace the binary, and shut down gracefully but never come back up — requiring a manual restart
  - Root cause: the new binary was spawned immediately while the old process was still holding the port; the new process tried to bind port 3000, failed, and exited silently before the old process finished shutting down
  - Fixed by spawning the new binary via `sh -c "sleep 5 && exec <path>"` — the 5-second shell delay gives the current process time to complete graceful shutdown and release the port before the new binary attempts to bind it
  - Works whether running directly in a terminal, under `nohup`, or managed by systemd

- **Auto-Update Windows: Switched from PowerShell script to cmd.exe batch script**
  - PowerShell execution policy (`Restricted`) at the machine/Group Policy level blocked the `.ps1` script even with `-ExecutionPolicy Bypass`, causing the update to silently do nothing after backing up the binary
  - Replaced with a `.cmd` batch script executed via `cmd.exe` — batch scripts have no execution policy restrictions and run on any Windows machine regardless of PowerShell configuration
  - Full update log written to `thinline-update.log` in the install directory for diagnosing any future issues

- **Auto-Update Unix: New binary now has correct executable permissions after update**
  - On systems where `/tmp` is a separate mount (tmpfs), permissions could be lost during the binary move
  - Fixed by re-applying `chmod 0755` to the binary after it is moved to its final location

- **Auto-Update: Check interval changed from 12 hours / 5 min delay to 30 min / 30 sec delay**
  - First update check now runs 30 seconds after startup (was 5 minutes)
  - Subsequent checks run every 30 minutes (was every 12 hours)

### Bug Fixes

- **Auto-Update Windows: Critical fix — binary swap now fully handled by PowerShell script**
  - Previous behaviour: the Go process renamed the current `.exe` to `.bak` *before* launching the PowerShell script. If the script failed to start for any reason (permissions, PowerShell policy, AV, etc.) the server was left with no executable and could not restart
  - Root cause 1: all file operations (backup + swap) were done inside the Go process before `os.Exit(0)`, meaning a script-launch failure left the install directory in a broken state
  - Root cause 2: `Write-Host` on a detached/hidden process with no console could silently crash PowerShell before it did anything, and no log file meant zero visibility into what went wrong
  - Fix: the Go process now only downloads, extracts, writes the script, and exits. **All file operations** (rename old exe → `.bak`, move new binary → `.exe`, start new server) are performed by the PowerShell script after the process has exited and released the file lock
  - If the PowerShell script fails to launch, the old binary is completely untouched — the server keeps running normally
  - Added `thinline-update.log` written to the install directory with timestamped entries for every step, so failures are fully diagnosable
  - Script now uses `Add-Content` to log file instead of `Write-Host` (detached processes have no console)
  - Added `-WindowStyle Hidden` to PowerShell invocation to suppress any console window flash

## Version 7.0 Beta 9.6.3 - Released TBD

### Bug Fixes

- **Admin — API Keys: New key row renders immediately on click**
  - Clicking "New API Key" now instantly shows the blank input row without requiring the user to navigate away and back first
  - Root cause was three compounding issues: the parent `config.component` uses `ChangeDetectionStrategy.OnPush`; the `apikeys` getter was returning the same mutated array reference so `mat-table` saw no change; and no `trackBy` function meant the table reused existing DOM rows with stale data
  - Fixed by spreading into a new array in the getter (`[...controls].sort(...)`), switching from `detectChanges()` to `markForCheck()` to propagate up through the OnPush ancestor, and adding `[trackBy]="trackByKey"` on the `mat-table` so rows are identified by their unique API key UUID
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/apikeys/apikeys.component.ts`, `client/src/app/components/rdio-scanner/admin/config/apikeys/apikeys.component.html`

### Testing Notes

- **Auto-Update — Beta 9.6.3 is the first release that can self-update**
  - Servers running Beta 9.6.2 with `auto_update = true` in `thinline-radio.ini` will automatically detect and download this release within 12 hours of it being published
  - To test immediately without waiting: `POST /api/admin/update/apply` from the admin API (requires admin token)
  - After update: server restarts gracefully — on Linux/macOS systemd picks it back up automatically; on Windows the PowerShell script relaunches the new `.exe`
  - Old binary is preserved as `thinline-radio.bak` in the install directory as a rollback option

## Version 7.0 Beta 9.6.2 - Released TBD

### New Features

- **Auto-Update Support**
  - Server can now automatically check GitHub Releases for new versions and apply updates without manual intervention
  - On Unix platforms (Linux, macOS, BSD, Solaris): new binary is atomically swapped in place via `os.Rename()` and the server restarts gracefully via `SIGTERM` — systemd/process managers pick it up automatically
  - On Windows: a detached PowerShell script handles the binary swap after the process exits, then relaunches the server
  - Background check runs 5 minutes after startup, then every 12 hours thereafter
  - Controlled by a new ini setting: `auto_update = true/false` (default: `false`)
  - Even with `auto_update = false`, manual update check and apply are available via the admin API:
    - `GET  /api/admin/update/check` — returns current version, latest version, and whether an update is available
    - `POST /api/admin/update/apply` — downloads and applies the update immediately, server restarts after responding
  - Current binary is backed up as `thinline-radio.bak` before replacement for safety
  - Files added: `server/updater.go`, `server/updater_unix.go`, `server/updater_windows.go`
  - Files modified: `server/config.go`, `server/controller.go`, `server/admin.go`, `server/main.go`, `server/thinline-radio.ini.template`

### Enhancements

- **Complete Admin Panel UI/UX Overhaul**
  - New sticky dark-themed header with ThinLine Radio logo, brand name, server version badge, and admin console indicator
  - Tab-based top navigation replacing the previous single-page accordion layout (Config, Logs, System Health, Tools)
  - Logout button relocated to the header for persistent, accessible logout regardless of active tab
  - Applied `admin-dark-theme` class globally to the admin wrapper for consistent dark styling
  - Redesigned login page with modern card layout, animated blocked state with countdown timer for failed attempts
  - Files modified: `client/src/app/pages/rdio-scanner/admin/admin.component.html`, `client/src/app/pages/rdio-scanner/admin/admin.component.scss`, `client/src/app/components/rdio-scanner/admin/admin.component.html`, `client/src/app/components/rdio-scanner/admin/admin.component.ts`, `client/src/app/components/rdio-scanner/admin/admin.component.scss`, `client/src/app/components/rdio-scanner/admin/login/login.component.html`, `client/src/app/components/rdio-scanner/admin/login/login.component.scss`

- **Config Section — Sidebar Navigation**
  - Replaced `mat-accordion` expansion panels with a persistent left sidebar navigation listing all configuration sections
  - Active section highlighted with red left-border indicator matching the brand accent colour
  - Error indicators (⚠) shown inline on sidebar items when a section has invalid form fields
  - Save and Reset buttons anchored to the bottom of the sidebar, always accessible without scrolling
  - Loading spinner shown while configuration is fetched and form is built, preventing blank-state flash
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/config.component.ts`, `client/src/app/components/rdio-scanner/admin/config/config.component.html`, `client/src/app/components/rdio-scanner/admin/config/config.component.scss`

- **Config Performance Improvements**
  - Parallelized `getConfig` and `loadAlerts` API calls using `Promise.all`
  - Deferred form initialization with `setTimeout(0)` so the loading spinner renders before the heavy form-build blocks the UI thread
  - Prevented redundant form rebuilds triggered by WebSocket updates on a pristine form within 500ms of last reset
  - Deduplicated concurrent `getConfig` calls using a shared `_configPromise` — multiple callers share a single in-flight request
  - Cached static `Alerts` data in `localStorage` to avoid repeated network requests on every load
  - Removed unused `loadAlerts()` calls from `ngOnInit` and the service constructor
  - Files modified: `client/src/app/components/rdio-scanner/admin/admin.service.ts`, `client/src/app/components/rdio-scanner/admin/config/config.component.ts`

- **Systems Section — New Sidebar Architecture**
  - Systems expand in the Config sidebar to list every configured system individually
  - Clicking a system in the sidebar navigates to a full-page detail view for that system
  - Systems overview page displays all systems in a sortable, drag-and-drop `mat-table` with columns for ID, label, type, talkgroup count, site count, and an edit button
  - System detail view uses a settings grid for core system properties, with separate `mat-table`s for Talkgroups, Sites, and Units — all visible without expansion
  - Talkgroups table supports inline editing, bulk group/tag assignment, drag-and-drop reordering, group chips, and tag labels
  - Sites and Units tables support inline editing and drag-and-drop reordering
  - Removed all pagination from systems, talkgroups, sites, and units — all records load at once
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/systems/systems.component.ts`, `client/src/app/components/rdio-scanner/admin/config/systems/systems.component.html`, `client/src/app/components/rdio-scanner/admin/config/systems/systems.component.scss`, `client/src/app/components/rdio-scanner/admin/config/systems/system/system.component.ts`, `client/src/app/components/rdio-scanner/admin/config/systems/system/system.component.html`, `client/src/app/components/rdio-scanner/admin/config/systems/system/system.component.scss`

- **Users Section — Table Layout**
  - Replaced `mat-accordion` expansion panels with a `mat-table` showing all user details inline
  - Columns: avatar (initials), username/email, group, status, PIN, last login, and actions menu
  - Clicking a row expands an inline edit form for that user below the row; all other rows remain visible
  - Actions menu (⋮) provides Transfer, Reset Password, Test Push, Resend Verification, and Delete
  - Sortable columns via `mat-sort-header`
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/users/users.component.ts`, `client/src/app/components/rdio-scanner/admin/config/users/users.component.html`, `client/src/app/components/rdio-scanner/admin/config/users/users.component.scss`

- **API Keys Section — Table Layout**
  - Replaced `mat-accordion` expansion panels with a `mat-table` showing all key data inline
  - Columns: drag handle, status chip (toggleable active/disabled), name (inline editable), masked API key with show/hide toggle and copy button, systems access badge, and delete
  - Drag-and-drop reordering preserves key visibility state
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/apikeys/apikeys.component.ts`, `client/src/app/components/rdio-scanner/admin/config/apikeys/apikeys.component.html`, `client/src/app/components/rdio-scanner/admin/config/apikeys/apikeys.component.scss`

- **Groups Section — Table Layout**
  - Replaced `mat-accordion` expansion panels with a `mat-table` showing all groups inline
  - Columns: drag handle, group label (inline editable), usage chip (unused/in-use), ID, and delete
  - Drag-and-drop reordering
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/groups/groups.component.ts`, `client/src/app/components/rdio-scanner/admin/config/groups/groups.component.html`, `client/src/app/components/rdio-scanner/admin/config/groups/groups.component.scss`

- **Tags Section — Table Layout**
  - Replaced `mat-accordion` expansion panels with a `mat-table` showing all tags inline
  - Columns: drag handle, colour swatch (with live preview in select trigger), label (inline editable), usage chip, and delete
  - Inline `⚠ Required` placeholder replaces separate error message to prevent row height shift
  - Drag-and-drop reordering
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/tags/tags.component.ts`, `client/src/app/components/rdio-scanner/admin/config/tags/tags.component.html`, `client/src/app/components/rdio-scanner/admin/config/tags/tags.component.scss`

- **Keyword Lists Section — Card Layout**
  - Replaced `mat-accordion` expansion panels with a card-based layout
  - Each card shows the list name, keyword count badge, optional description, and a preview of the first 12 keywords as chips with a "+N more" indicator
  - Clicking Edit expands the card in-place with an inline keyword input field — press Enter or click Add to add keywords without a browser `prompt()` dialog
  - Import from file (`.txt` or `.json`) remains available in edit mode
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/keyword-lists/keyword-lists.component.ts`, `client/src/app/components/rdio-scanner/admin/config/keyword-lists/keyword-lists.component.html`, `client/src/app/components/rdio-scanner/admin/config/keyword-lists/keyword-lists.component.scss`

- **Tools Section — Sidebar Navigation**
  - Replaced `mat-accordion` expansion panels with a left sidebar navigation identical in layout to the Config section
  - 8 tools listed: Import Talkgroups, Import Units, Radio Reference, Admin Password, Import/Export Config, Config Sync, Stripe Customer Sync, Purge Data
  - Section header shows icon, tool name, and a short description on tool selection
  - Responsive: collapses to a compact horizontal icon row on screens ≤ 600 px
  - Files modified: `client/src/app/components/rdio-scanner/admin/tools/tools.component.ts`, `client/src/app/components/rdio-scanner/admin/tools/tools.component.html`, `client/src/app/components/rdio-scanner/admin/tools/tools.component.scss`

- **Docker environment awareness**
  - `dirwatch` configuration section is automatically hidden when the application is running inside a Docker container
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/config.component.ts`

- **`central-management/` added to `.gitignore`**
  - Central management folder excluded from the main repository tracking
  - Files modified: `.gitignore`

### Bug Fixes

- **"Invalid Date" shown for Registered and Last Login in Users table**
  - Dates were being read from a parent `FormArray` which stored them in a non-parseable format rather than RFC 3339 strings from the API
  - Fixed by always calling `loadUsers(true)` on `ngOnInit` to force a fresh API fetch with properly formatted date strings
  - Hardened `formatDate()` to check `isNaN(date.getTime())` and years before 1970 (Go zero-time), returning `'Never'` instead of `'Invalid Date'`
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/users/users.component.ts`

- **Dark theme — hardcoded light colours overriding admin dark theme**
  - Systematically identified and replaced hardcoded light-theme `background-color`, `color`, and `border` values across 13+ files
  - Affected components: User Registration, Password, Config Sync, Options, Users, System, System Health, Radio Reference Import, Stripe Sync, Purge Data, User Groups, Keyword Lists
  - Inline styles updated to dark-theme compatible values; SCSS files rewritten with dark backgrounds and lighter text colours
  - Files modified: `user-registration.component.html`, `user-registration.component.scss`, `password.component.html`, `config-sync.component.html`, `options.component.html`, `users.component.scss`, `system.component.html`, `system-health.component.html`, `system-health.component.scss`, `radio-reference-import.component.html`, `radio-reference-import.component.scss`, `stripe-sync.component.scss`, `purge-data.component.scss`, `user-groups.component.scss`

- **`login.component.scss` — `color: #555` invisible on dark background**
  - `.wait-text` colour changed from `#555` to `#999` for readability on the dark login card
  - Files modified: `client/src/app/components/rdio-scanner/admin/login/login.component.scss`

- **Tags table — text not vertically centred, required error shifting row height**
  - Wrapped `mat-cell` content in `<div class="cell-content">` with `display: flex; align-items: center` for consistent vertical alignment
  - Removed `<mat-error>` from the label field; replaced with a dynamic `placeholder="⚠ Required"` shown only when the field is touched and invalid, eliminating the subscript space reservation that caused the row shift
  - Added `::ng-deep .mat-mdc-form-field-subscript-wrapper { display: none; }` to suppress reserved subscript space
  - Files modified: `client/src/app/components/rdio-scanner/admin/config/tags/tags.component.html`, `client/src/app/components/rdio-scanner/admin/config/tags/tags.component.scss`

- **Tools sidebar rendering as horizontal bar instead of vertical sidebar**
  - Root cause: `ViewEncapsulation.None` was missing from the `@Component` decorator; without it Angular's emulated view encapsulation scoped and broke the flex-direction styles
  - Added `encapsulation: ViewEncapsulation.None` to match the pattern used by `config.component.ts`
  - Files modified: `client/src/app/components/rdio-scanner/admin/tools/tools.component.ts`

### Copyright

- Added Thinline Dynamic Solutions copyright headers to all files created or significantly modified during this release cycle (33 files)
- Chrystian Huot's original copyright is retained at the top of all files derived from the upstream rdio-scanner codebase
- Files modified: all `.ts`, `.html`, and `.scss` files listed in the sections above

## Version 7.0 Beta 9.5 - Released TBD

### Bug Fixes

- **Audio conversion improvements**
  - Re-implemented Opus codec support alongside AAC/M4A
  - Admin page now allows codec selection (AAC recommended, Opus for lower bandwidth)
  - Updated FFmpeg filters for clearer audio quality with reduced background noise
  - Conservative normalization mode: Gentle highpass/lowpass (80Hz-8kHz), moderate loudnorm (I=-16, TP=-2.0)
  - Standard normalization mode: Balanced filtering (100Hz-7kHz), higher loudness (I=-12, TP=-1.5)
  - Aggressive normalization mode: Tighter bandwidth (120Hz-6kHz), FFT denoise, loudest output (I=-10, TP=-1.5)
  - Maximum normalization mode: Narrowest bandwidth (150Hz-5kHz), stronger denoise, maximum loudness (I=-8, TP=-1.0)
  - Bitrate range extended from 16-512 kbps (admin configurable)
  - Opus encoding: 48kHz stereo, optimized for voice (VOIP mode)
  - AAC encoding: 44.1kHz stereo, optimized for universal compatibility
  - Fixed Opus sample rate error (Opus only supports 8/12/16/24/48 kHz, not 44.1 kHz)
  - Removed hardcoded bitrate limits - now fully controlled by admin settings
  - Files modified: `server/ffmpeg.go`, `server/options.go`, `server/defaults.go`, `client/src/app/components/rdio-scanner/admin/config/options/options.component.html`, `client/src/app/components/rdio-scanner/admin/admin.service.ts`

## Version 7.0 Beta 9.4 - Released TBD

### Enhancements

- **Per-system no-audio monitoring with independent timers**
  - Complete refactor of no-audio monitoring from fixed 5-minute global checks to per-system dynamic timers
  - Each system now monitors at its own threshold interval (1 min threshold = checks every 1 min, 2 hours = checks every 2 hours)
  - Monitoring automatically detects setting changes and restarts with new interval
  - Stops monitoring when alerts are disabled globally or per-system
  - Added functions: `MonitorNoAudioForSystem()`, `StartNoAudioMonitoringForAllSystems()`, `StartNoAudioMonitoringForSystem()`, `RestartNoAudioMonitoringForSystem()`
  - Files modified: server/system_alert.go, server/admin.go

- **Comprehensive logging for no-audio monitoring**
  - Added detailed logging at every step of no-audio monitoring
  - Logs show: system checks, threshold comparisons, alert creation, skip reasons
  - Makes troubleshooting no-audio alerts much easier
  - Files modified: server/system_alert.go

- **Simplified to AAC/M4A audio encoding only**
  - Removed Opus codec option - all new calls now encode as AAC/M4A for universal compatibility
  - Legacy Opus files can still be played, but new encoding is AAC only
  - Fixes audio quality issues on iPhone 16/17 Pro Max (Opus resampling caused muffled audio)
  - Admin UI simplified - only bitrate selection (32-128 kbps), codec is always AAC
  - Default bitrate: 48 kbps (good quality, reasonable file size)
  - Recommended: 48 kbps (better) or 64 kbps (excellent)
  - Files modified: `server/options.go`, `server/ffmpeg.go`, `server/thinline-radio.ini`, `client/src/app/components/rdio-scanner/admin/config/options/options.component.html`

### Bug Fixes

- **System health alerts toggle reverting bug**
  - Fixed issue where the system health alerts master toggle would revert to the opposite state after clicking
  - The toggle was double-inverting the value (once by ngModel, once by the handler)
  - Files modified: client/src/app/components/rdio-scanner/admin/system-health/system-health.component.ts

- **Slow response when changing system health alert settings**
  - Removed unnecessary `Options.Read()` calls that were reloading all options from database after every save
  - Settings now respond instantly instead of taking several seconds
  - Files modified: server/admin.go (SystemHealthAlertsEnabledHandler, SystemHealthAlertSettingsHandler)

- **No audio alerts not sending push notifications**
  - Changed `MonitorNoAudio()` to use `CreateSystemAlert()` method (same as transcription failures)
  - Now properly sends push notifications to all system admins via `SendSystemAlertNotification()`
  - Fixed TODO comment that said push notifications weren't implemented
  - Files modified: server/system_alert.go

- **No audio alerts not triggering for systems with no calls**
  - Fixed issue where systems with no calls in database were being skipped
  - Now creates alerts for systems with no calls (shows "has no calls in database" message)
  - Files modified: server/system_alert.go

- **No audio alerts now only keep latest per system**
  - Automatically dismisses previous no-audio alerts for a system when creating a new one
  - Prevents accumulation of duplicate "No Audio Received" alerts for the same system
  - Users only see the most recent status update per system
  - Files modified: server/system_alert.go

- **Auto-restart monitoring when settings change**
  - Monitoring automatically restarts when system health alert settings are updated via admin interface
  - Ensures new thresholds take effect immediately without manual server restart
  - Files modified: server/admin.go (SystemHealthAlertsEnabledHandler, SystemHealthAlertSettingsHandler, SystemNoAudioSettingsHandler)

- **Extended SystemAlertData struct**
  - Added fields: `SystemLabel`, `Threshold`, `LastCallTime`, `MinutesSinceLast`
  - Better support for no-audio alert data
  - Files modified: server/system_alert.go

- **REMOVED: Disabled tone removal/trimming in both Go server and Whisper server - focusing on Whisper configuration instead**
  - Root cause analysis: Both the Go server (FFT tone detection) and Whisper server (Silero VAD) were trying to remove/skip tones before transcription
  - The tone removal logic added significant complexity and wasn't reliably working
  - Detection was either too sensitive (removing voice) or too lenient (missing tones)
  - **New approach:** Let Whisper handle full audio and configure it to ignore tones
  - **Changes to Go server (`server/transcription_queue.go`):**
    - Removed all tone detection and removal logic from transcription pipeline
    - Transcription now always uses original audio (no filtering)
    - Simplified codebase significantly
  - **Changes to Whisper server (`whisper/whisper_server.py`):**
    - Removed Silero VAD tone-skipping logic that was trimming audio before speech detection
    - Now transcribes full audio from start to finish
    - **Improved Whisper parameters to handle tones:**
      - `no_speech_threshold`: Raised from **0.3 → 0.6** to better skip tone-only segments
      - `logprob_threshold`: Lowered from **-0.8 → -1.0** to be more strict (skip low-confidence segments)
      - `initial_prompt`: Added default prompt: *"This is radio dispatch audio. Ignore alert tones and beeps. Transcribe only spoken words."*
      - This guides Whisper to focus on speech and ignore tones
    - Removed Silero VAD dependencies and functions (no longer needed)
  - **Result:** Simpler codebase, Whisper handles tones naturally through better configuration
  - Files modified: `server/transcription_queue.go`, `whisper/whisper_server.py`

- **CRITICAL: Fixed tone-only calls being transcribed with original audio causing hallucinations and incorrect pending tone attachments**
  - Root cause: When tone removal failed or produced very small filtered audio (<1000 bytes), the system fell back to transcribing the **original audio with tones still present**
  - This caused Whisper to hallucinate on tone-only calls (e.g., "THE FOLLOWING IS A WORK OF FICTION...")
  - The hallucinated transcript was then treated as a "voice call," causing the system to incorrectly attach pending tones to it
  - **Problem scenarios:**
    1. Tone filtering failed (ffmpeg error) → used original audio → hallucination
    2. Filtered audio too small (< 1000 bytes, mostly/all tones) → used original audio → hallucination
  - **Fixed:**
    - When tone filtering **fails**, skip transcription entirely (mark as completed with empty transcript)
    - When filtered audio is **too small** (< 1000 bytes), skip transcription entirely (don't fall back to original)
    - These calls are now correctly marked as "tone-only" with empty transcripts
    - Prevents hallucinated transcripts from being attached to pending tones
    - Prevents wasted Whisper API calls on unusable audio
  - Log messages now clearly indicate: "skipping transcription (tone-only call)" or "skipping transcription (tone filtering failed)"
  - Debug logs show: "SKIPPING TRANSCRIPTION - Filtered audio too small" or "FILTERING FAILED ... Skipping transcription to prevent hallucinations"
  - Files modified: `server/transcription_queue.go`

- **Fixed Opus audio sounding overly amplified, distorted, and muffled compared to raw audio**
  - Root cause: Opus bitrate was set too low (16 kbps) for normalized audio, causing audible distortion
  - When audio normalization is applied (loudnorm + limiter), the 16 kbps bitrate couldn't handle the processed signal
  - Result: Opus audio sounded "over-amplified," "distorted," and "muffled" compared to the original
  - Fixed: Increased Opus bitrate from 16 kbps to 24 kbps
  - 24 kbps provides better quality for normalized/processed voice audio while still maintaining small file sizes
  - Opus at 24 kbps is still significantly smaller than AAC at 32 kbps (previous default)
  - Files modified: `server/ffmpeg.go`

### Enhancements

- **Implemented Systems visibility toggle dialog (GitHub issue - stale button)**
  - Root cause: "Systems" button in Channel Select was present but non-functional (stale button)
  - The button existed but `showSystemsModal()` was just a TODO placeholder
  - Infrastructure was already in place (`hiddenSystems` Set and `getVisibleSystems()` filtering) but no UI to manage it
  - Fixed: Created new `SystemsVisibilityDialogComponent` dialog that allows users to show/hide systems
  - Dialog displays all systems with checkboxes to toggle visibility
  - Hidden systems are persisted to localStorage and restored on page load
  - Hidden systems are filtered out from the Channel Select list (only visible systems shown)
  - Matches functionality available in mobile app
  - Users can now customize which systems appear in their Channel Select view
  - Files modified: client/src/app/components/rdio-scanner/select/select.component.ts, client/src/app/components/rdio-scanner/rdio-scanner.module.ts
  - Files created: client/src/app/components/rdio-scanner/select/systems-visibility-dialog.component.ts, systems-visibility-dialog.component.html, systems-visibility-dialog.component.scss

### Bug Fixes

- **Fixed hidden systems still being included in livefeed and affected by Enable All/Disable All**
  - Root cause: When systems were hidden via the Systems visibility dialog, they were still included in the livefeed map sent to the server
  - Hidden systems were also being toggled when clicking "Enable All" or "Disable All" buttons
  - This caused users to hear audio from hidden systems even though they were hidden in the UI
  - Fixed #1: Updated `toggleAllTalkgroups()` to only affect visible systems (iterates through `getVisibleSystems()`)
  - Fixed #2: Updated `isAllEnabled()`, `isPartiallyEnabled()`, `getEnabledCount()`, and `getTotalCount()` to only count visible systems
  - Fixed #3: Updated `startLivefeed()` in service to filter out hidden systems from livefeed map before sending to server
  - Hidden systems are now completely excluded from livefeed - they won't receive audio even if their talkgroups are enabled
  - Enable All/Disable All buttons now only affect visible systems, matching user expectations
  - Files modified: client/src/app/components/rdio-scanner/select/select.component.ts, client/src/app/components/rdio-scanner/rdio-scanner.service.ts

- **Fixed favorites list system Enable/Disable buttons enabling all talkgroups instead of only favorited ones**
  - Root cause: When clicking star on a tag to add favorites, it correctly added only that tag's talkgroups to favorites
  - However, clicking "Enable" button on the system in favorites list would enable ALL talkgroups in that system
  - This happened because `setSystemTalkgroupsStatus()` method always called `avoid({ system, status })` which affects all talkgroups
  - The same method was used in both the favorites view and the all-systems view, causing incorrect behavior in favorites context
  - Additionally, the system stats showed total talkgroup count (e.g., "3 / 110 total") instead of favorited count
  - Fixed #1: Created new `setFavoriteSystemTalkgroupsStatus()` method that only enables/disables favorited talkgroups
  - New method filters `favoriteItems` to get only talkgroups marked as favorites for that system
  - Fixed #2: Created new `getFavoriteTalkgroupsCount()` method to return count of favorited talkgroups in a system
  - System Enable/Disable buttons in favorites view now use the new method to respect favorites selection
  - System stats in favorites view now show "3 / 20 total" where 20 is the favorited count, not total system talkgroups
  - System enable count in favorites list now correctly reflects only the favorited talkgroups that are enabled
  - Files modified: client/src/app/components/rdio-scanner/select/select.component.ts, client/src/app/components/rdio-scanner/select/select.component.html

- **Fixed web browser Channel Select clearing sporadically when auto-populate adds new talkgroups (GitHub issue #104)**
  - Root cause: The `rebuildLivefeedMap()` function used a loose falsy check (`&&`) to determine if talkgroups existed in saved selections
  - When `this.livefeedMap[sys.id][tg.id]` existed but had falsy properties, it was incorrectly treated as non-existent
  - This caused existing talkgroup selections to be reset to `active: false` instead of being preserved
  - Happened sporadically when CFG messages were sent after auto-populate added new talkgroups to hidden or public systems
  - Fixed: Changed existence check from `this.livefeedMap[sys.id] && this.livefeedMap[sys.id][tg.id]` to explicit `!== undefined` checks
  - Now uses: `this.livefeedMap[sys.id] !== undefined && this.livefeedMap[sys.id][tg.id] !== undefined`
  - This ensures existing talkgroup selections are always preserved regardless of their property values
  - New talkgroups from auto-populate are correctly set to `active: false` without affecting existing selections
  - Web browser now behaves consistently with mobile app (which already handled this correctly)
  - Files modified: client/src/app/components/rdio-scanner/rdio-scanner.service.ts

- **CRITICAL: Fixed transcription tone removal silently failing with "parsing N: invalid syntax" error**
  - Root cause: `RemoveTonesFromAudio()` was trying to get audio duration using `ffprobe`, which returns "N/A" for piped/streamed audio
  - Error: `FILTERING FAILED: failed to parse duration: strconv.ParseFloat: parsing "N": invalid syntax`
  - When duration detection fails, the function returns original audio with tones still present, causing Whisper to hallucinate text like "THE FOLLOWING IS A WORK OF FICTION..."
  - **Problem:** Both `ffprobe` and `ffmpeg` return "N/A" for duration when audio is piped through stdin (not written to disk), regardless of format (MP3, M4A, WAV, Opus)
  - **Solution:** Parse WAV header directly to calculate duration from PCM sample data (no external tools needed)
  - **Architecture:** Raw audio → WAV (tone removal) → WAV (transcription) | Raw audio → Opus (streaming/storage only)
  - **Critical:** Opus should NEVER be transcribed - it's only for streaming and storage; WAV is used for all transcription and tone removal
  - Fixed #1: `RemoveTonesFromAudio()` now converts input audio to WAV before processing (consistent with tone detection)
  - Fixed #2: Duration is calculated directly from WAV header: `duration = (sample_count / channels) / sample_rate`
  - Fixed #3: Eliminates dependency on `ffprobe` for duration detection (which fails with piped audio)
  - Fixed #4: Tone removal outputs WAV (not Opus) for Whisper transcription
  - Fixed #5: Simplified and robust - single conversion step, direct header parsing, no external tool failures
  - Fixed #6: Consistent processing - both tone detection and removal now use the same WAV-based approach
  - Enhancement: Added comprehensive transcription tone removal debug logging system
  - New debug logger: `TranscriptionDebugLogger` writes to `transcription-tone-debug.log` (separate from tone detection logs)
  - Debug system logs: detected tones (frequency, duration, timing), filtering success/failure, ffmpeg errors, and audio processing details
  - Controlled by existing `EnableDebugLog` config setting (same as tone detection debug logs)
  - Admins can now review detailed logs to diagnose tone removal issues and verify the process is working correctly
  - **All ffmpeg errors are now visible in logs** (previously silent failures with "N" parsing errors)
  - Files modified: server/tone_detector.go, server/debug_logger.go, server/controller.go, server/transcription_queue.go

## Version 7.0 Beta 9.3 - Released TBD

### Bug Fixes

- **CRITICAL: Fixed bug where user alert preferences and FCM tokens were being cleared on config save**
  - Root cause #1: Database CASCADE DELETE constraints on `userAlertPreferences.userId` and `deviceTokens.userId` automatically deleted data when users were updated
  - Root cause #2: Migration in beta 9.2 only dropped constraints but didn't recreate them without CASCADE DELETE
  - When saving config, user records are updated, triggering CASCADE DELETE of all related alert preferences and FCM tokens
  - Database fix: Migration now properly recreates foreign key constraints WITHOUT CASCADE DELETE (defaults to NO ACTION)
  - Updated migration function `migrateRemoveUserAlertPreferencesCascadeDelete` to drop AND recreate constraints
  - Database constraints now prevent automatic deletion when parent records are updated
  - User alert preferences and FCM tokens now persist across all config save operations
  - Files modified: server/migrations.go

- **Fixed keyword list IDs becoming orphaned when deleting keyword lists or importing from Radio Reference**
  - Root cause #1: When deleting keyword lists via API, references in `userAlertPreferences.keywordListIds` JSON arrays weren't being cleaned up
  - Root cause #2: Radio Reference imports were deleting ALL keyword lists and recreating them with new auto-incremented IDs
  - Keyword lists are user-defined and have nothing to do with Radio Reference data, yet they were being wiped on every RR import
  - Migration `migrateFixKeywordListIds` would fix orphaned IDs on startup, but problems would recur after deletions or RR imports
  - Fixed #1: DELETE endpoint now cleans up references in all user alert preferences before deleting keyword list
  - Fixed #2: Radio Reference imports now skip keyword list processing entirely, preserving user's keyword lists and their IDs
  - Only full backup/restore operations (which include `keywordListId` field) will replace keyword lists with preserved IDs
  - Orphaned keyword list IDs no longer occur from deletions or Radio Reference imports
  - Files modified: server/api.go, server/admin.go

## Version 7.0 Beta 9.2 - Released TBD

### Enhancements

- **Enhanced API error logging with additional context (GitHub issue #88)**
  - Root cause: API errors like "Invalid credentials" and "Incomplete call data: no talkgroup" provided no details about source IP, endpoint, or user agent
  - Made troubleshooting difficult as admins couldn't identify where unauthorized access attempts or invalid API calls were coming from
  - Fixed: Added new `exitWithErrorContext()` function that logs comprehensive request details (source IP, HTTP method, endpoint path, user agent)
  - Handles proxy headers (X-Forwarded-For, X-Real-IP) to capture real client IP behind proxies/load balancers
  - Updated critical API error paths: invalid credentials (login endpoints), incomplete call data (call upload)
  - Also enhanced admin login logging for failed attempts, rate limiting, and localhost-only violations
  - Logs now show format: "api: [error message] | IP=[client_ip] | Endpoint=[method path] | UserAgent=[agent]"
  - Example: "api: Invalid credentials | IP=192.168.1.100 | Endpoint=POST /api/user/login | UserAgent=Mozilla/5.0..."
  - Admin logs: "admin: Invalid login attempt | IP=127.0.0.1 | Endpoint=POST /api/admin/login | UserAgent=Chrome/120.0..."
  - Admins can now identify problematic API clients, unauthorized access attempts, and misconfigured upload systems
  - Files modified: server/api.go, server/admin.go

### Bug Fixes

- **CRITICAL: Fixed bug where importing sites/talkgroups from Radio Reference would wipe out all user alert preferences (INCOMPLETE FIX - see beta 9.3)**
  - Root cause: Database CASCADE DELETE constraints on `userAlertPreferences` table automatically deleted preferences when talkgroups were updated/deleted
  - When talkgroups are written to database, old ones are deleted and recreated, triggering CASCADE DELETE of all related user preferences
  - Database fix: Attempted to remove CASCADE DELETE foreign key constraints from `userAlertPreferences.systemId` and `userAlertPreferences.talkgroupId`
  - Client-side fix: Excluded `userAlertPreferences` and `deviceTokens` from regular config saves (only included in full imports)
  - Server-side fix: User alert preferences and device tokens are now only deleted/reimported during explicit full configuration imports
  - **NOTE: Migration was incomplete - only dropped constraints but didn't recreate them. Fixed properly in beta 9.3**
  - Files modified: server/postgresql.go, server/migrations.go, server/database.go, server/admin.go, client/src/app/components/rdio-scanner/admin/config/config.component.ts

- **Fixed system alerts not clearing on first click (GitHub issue #96)**
  - Root cause: Frontend was using POST method but backend expected PUT for RESTful compliance
  - Alert dismissal required multiple clicks to work, especially on busy servers
  - Fixed: Updated backend to accept both POST and PUT methods for dismissing alerts
  - Updated frontend to use PUT method as originally intended
  - Files modified: server/admin.go, client/src/app/components/rdio-scanner/admin/admin.service.ts

- **Fixed per-system no-audio alert settings not saving reliably**
  - Root cause: Frontend was saving entire config JSON which caused race conditions on high-traffic servers (100+ calls/min)
  - Saving per-system settings would fail intermittently due to concurrent config modifications
  - Fixed: Added dedicated API endpoint `/api/admin/system-no-audio-settings` for atomic updates
  - Now updates only the specific system's settings without loading/saving entire config
  - Files modified: server/admin.go, server/main.go, client/src/app/components/rdio-scanner/admin/admin.service.ts, client/src/app/components/rdio-scanner/admin/system-health/system-health.component.ts

- **Fixed double lossy audio conversion degrading transcription quality (GitHub issue #91)**
  - Root cause: Audio was being converted twice through lossy codecs before transcription
  - Original flow: SDRTrunk MP3 (16kbps) → Opus/AAC conversion → WAV conversion for transcription
  - Each lossy conversion degrades audio quality, making transcription less accurate
  - Tone detection was already using original audio (before Opus conversion), but transcription was not
  - Fixed: Transcription now uses the original raw audio (MP3 from SDRTrunk) before Opus/AAC conversion
  - This avoids double lossy conversion and provides same quality audio to transcription as tone detection gets
  - New flow: SDRTrunk MP3 (16kbps) → WAV conversion for transcription (single conversion)
  - Added `OriginalAudio` and `OriginalAudioMime` fields to Call struct and TranscriptionJob struct
  - Controller stores original audio before encoding and passes it to transcription queue
  - Transcription worker now uses original audio, falling back to converted audio if original unavailable
  - Files modified: server/call.go, server/controller.go, server/transcription_queue.go

- **Fixed talkgroup CSV import inserting talkgroups in reverse order**
  - Root cause: CSV import component was using `unshift()` method which adds items to the beginning of the array
  - When importing a CSV file, each talkgroup was prepended to the list, resulting in reverse order
  - This made the talkgroups appear in opposite order from the CSV file when not sorting by ID or name
  - Fixed: Changed `unshift()` to `push()` to append talkgroups in correct order
  - Talkgroups now import in the same order as they appear in the CSV file
  - Files modified: client/src/app/components/rdio-scanner/admin/tools/import-talkgroups/import-talkgroups.component.ts

- **Fixed audio playback duplication when toggling channels during livefeed (GitHub issue #93)**
  - Root cause: When user toggled systems/talkgroups in Channel Select while livefeed was running with backlog enabled, the client sent a new LivefeedMap to server
  - Server's `ProcessMessageCommandLivefeedMap()` always called `sendAvailableCallsToClient()` on every LivefeedMap update
  - This re-sent all backlog audio (e.g., 1 minute of prior calls) every time a channel was toggled
  - With hundreds of calls in the backlog, the queue would fill with duplicate transmissions
  - Fixed: Added `BacklogSent` flag to Client struct to track whether initial backlog has been sent for current livefeed session
  - Server now only sends backlog on initial livefeed start (when transitioning from all-off to any-on state)
  - Channel toggles during active livefeed no longer re-send backlog audio
  - Flag resets when livefeed is fully stopped (all channels off), allowing backlog to be sent again on next livefeed start
  - Users can now toggle channels without experiencing audio queue duplication
  - Files modified: server/client.go, server/controller.go

- **Fixed tag validation error when assigning manually created tags to talkgroups (GitHub issue #95)**
  - Root cause: After saving config, Angular's change detection wasn't properly updating child components with fresh data containing database-assigned IDs
  - When creating a new tag and saving, the server assigned an ID and returned the updated config, but form rebuilding with OnPush change detection didn't propagate to child components
  - Tag dropdown would show newly created tags but without IDs, causing "Tag required" error when selected
  - This only worked after manual browser refresh which fully reinitialized all components
  - Fixed: Page now automatically reloads after save to ensure all components get fresh data with database-assigned IDs (same as manual refresh)
  - Users can now create tags and immediately assign them after save completes
  - Files modified: client/src/app/components/rdio-scanner/admin/config/config.component.ts, client/src/app/components/rdio-scanner/admin/config/config.component.html, client/src/app/components/rdio-scanner/admin/admin.service.ts, client/src/app/components/rdio-scanner/admin/config/systems/talkgroup/talkgroup.component.html

- **Fixed FFmpeg version detection for FFmpeg 8.0+ (GitHub issue #92)**
  - Root cause: Version detection regex only matched single-digit version numbers (e.g., 4.3)
  - FFmpeg 8.0.1+ versions failed regex pattern `([0-9])` which only captures 0-9, not multi-digit numbers
  - This caused server to incorrectly fall back to `dynaudnorm` filter instead of using `loudnorm` filter
  - Users saw warning "FFmpeg 4.3+ required for loudnorm filter" despite having FFmpeg 8.0.1 installed
  - Fixed: Updated regex pattern from `([0-9])\.([0-9])` to `([0-9]+)\.([0-9]+)` to match multi-digit versions
  - Server now correctly detects FFmpeg 8.0.1, 10.2.1, and other multi-digit versions
  - Audio normalization now uses proper `loudnorm` filter (EBU R128 standard) instead of fallback `dynaudnorm`
  - Files modified: server/ffmpeg.go

- **Fixed talkgroup blacklist not working properly**
  - Root cause: Admin panel was using wrong field (`id` - database primary key) instead of `talkgroupRef` (radio reference ID) when adding to blacklist
  - Server blacklist checking uses `talkgroupRef` to match incoming calls, but admin was storing database IDs in blacklist string
  - This caused blacklisted talkgroups to persist and continue receiving calls after being blacklisted
  - Fixed: Changed `blacklistTalkgroup()` method to use `talkgroup.value.talkgroupRef` instead of `talkgroup.value.id`
  - Blacklisted talkgroups are now properly rejected when calls arrive
  - Files modified: client/src/app/components/rdio-scanner/admin/config/systems/system/system.component.ts, rdio-scanner-master/client/src/app/components/rdio-scanner/admin/config/systems/system/system.component.ts

- **Fixed talkgroup pagination breaking bulk actions (GitHub issue #97)**
  - Root cause: Bulk action methods were using paginated indices (0-49 for each page) directly on the full talkgroups array
  - When selecting talkgroups on page 2+, bulk actions (Assign Tag/Group) were applied to wrong talkgroups at those positions on page 1
  - Example: Selecting item 5 on page 2 (actual index 55) would apply action to item 5 on page 1 (actual index 5)
  - Fixed: Added `getFullTalkgroupIndex()` helper method to map paginated index to full array index
  - Updated `toggleTalkgroupSelection()` and `isTalkgroupSelected()` to use full array indices for selection tracking
  - Bulk actions now correctly apply to the talkgroups selected on any page
  - Files modified: client/src/app/components/rdio-scanner/admin/config/systems/system/system.component.ts

## Version 7.0 Beta 9.1 - Released TBD

### Breaking Changes

- **Push notifications migrated from OneSignal to Firebase Cloud Messaging (FCM)**
  - Scanner server now uses FCM tokens for push notifications instead of OneSignal player IDs
  - Device registration endpoint updated to accept `fcm_token` and `push_type` fields
  - Legacy OneSignal tokens (`token` field) automatically fallback for backward compatibility
  - **Test push notifications**: Now use per-device sound preferences from database
  - **Platform-specific sound handling**: iOS devices receive sound names without extensions; Android devices receive sound with `.wav` extensions
  - **Bug fix**: Scanner server no longer overwrites platform-specific sounds - each platform (iOS/Android) now uses its own device's sound preference
  - Files modified: server/push_notification.go

## Version 7.0 Beta 9 - Released TBD

### Bug Fixes

- **Relay Server: Temporarily bypassed push notification subscription validation**
  - Temporary fix to allow all push notifications through without any validation
  - Bypasses both database checks and subscription validation due to app sync account sign-out issues
  - All player IDs are now passed directly to OneSignal without validation
  - OneSignal will handle filtering of invalid player IDs on their end
  - Users can't get a player ID until they subscribe in the mobile app anyway, so validation is redundant
  - Original validation logic (database checks and subscription verification) preserved in comments for easy restoration
  - TODO: Re-enable validation once app sync account issues are resolved
  - Files modified: relay-server/internal/api/api.go

- **Fixed talkgroup sorting not persisting properly**
  - Root cause: Admin panel FormArray was not being reordered to match display order
  - When saving config after modifying other sections, talkgroups were saved in database ID order instead of custom sort order
  - Client-side fix: FormArray is now reordered to match display order (sorted by Order field) on load
  - Server-side fix: Changed all talkgroup sorts to stable sorts with secondary sort key (talkgroup ID) to prevent random shuffling
  - AutoPopulate fix: New talkgroups now get Order = max(existing orders) + 1 instead of 0, preventing them from jumping to the top
  - Fixed typo in talkgroup.ToMap() where Order field was incorrectly mapped as "talkgroup" instead of "order"
  - Talkgroup custom sort order now persists correctly across saves, page refreshes, and server restarts
  - Files modified: client/src/app/components/rdio-scanner/admin/config/systems/system/system.component.ts, server/controller.go, server/talkgroup.go, server/system.go

- **Fixed new talkgroups/systems auto-enabling for all users (GitHub issue #85)**
  - Root cause: Web client's `rebuildLivefeedMap()` was defaulting new talkgroups to enabled if their group/tag wasn't explicitly Off
  - This caused newly created or auto-populated talkgroups to automatically appear in all users' live feeds
  - **Web client fix**: New talkgroups now default to `active: false` in livefeed map, requiring users to manually enable them in Channel Select
  - **Mobile app**: Already correctly defaulted new talkgroups to disabled state (`false`) in `_mergeTalkgroupStates()`
  - Users must now explicitly enable new talkgroups/systems before they appear in live feed
  - Prevents unexpected audio from new sources users haven't selected
  - Files modified: client/src/app/components/rdio-scanner/rdio-scanner.service.ts

- **Fixed system no-audio alerts foreign key constraint error**
  - Root cause: System-generated no-audio alerts were passing 0 for `createdBy` field instead of NULL
  - This violated the foreign key constraint requiring `createdBy` to reference a valid user or be NULL
  - Fix: Changed `createdBy` value from 0 to NULL for system-generated alerts
  - Error message: `ERROR: insert or update on table "systemAlerts" violates foreign key constraint "systemAlerts_createdBy_fkey" (SQLSTATE 23503)`
  - Files modified: server/system_alert.goR

### New Features

- **Per-system no-audio alert configuration with simplified monitoring**
  - Replaced complex adaptive monitoring with simple per-system threshold configuration
  - Each system now has individual "No Audio Alerts" toggle and threshold (minutes) setting
  - Configured in Admin → System Health → Per-System No Audio Settings
  - Removed: Adaptive threshold calculation, historical data analysis, multipliers, time-of-day learning
  - New simple logic: Alert if system hasn't received audio in X minutes (configurable per-system)
  - Defaults: Enabled with 30-minute threshold for all systems
  - Global "No Audio Alerts Enabled" toggle still acts as master switch
  - Monitoring runs every 5 minutes and checks each enabled system
  - Files modified: server/system.go, server/postgresql.go, server/migrations.go, server/system_alert.go, client/src/app/components/rdio-scanner/admin/admin.service.ts, client/src/app/components/rdio-scanner/admin/system-health/system-health.component.ts, client/src/app/components/rdio-scanner/admin/system-health/system-health.component.html

- **Per-talkgroup exclusion from preferred site detection**
  - New "Exclude from Preferred Site Detection" option for individual talkgroups
  - Useful for interop/patched talkgroups that receive calls from multiple physical P25 systems
  - When enabled, talkgroup bypasses advanced duplicate detection and uses legacy time-based detection
  - Prevents unnecessary delays for talkgroups that can originate from sites outside preferred site configuration
  - Configured in Admin → Config → Systems → Talkgroups
  - Files modified: server/talkgroup.go, server/postgresql.go, server/migrations.go, server/controller.go, client/src/app/components/rdio-scanner/admin/admin.service.ts, client/src/app/components/rdio-scanner/admin/config/systems/talkgroup/talkgroup.component.html

- **Automatic site identification by frequency**
  - System automatically determines which site a call originated from by matching call frequency against configured site frequencies
  - New `GetSiteByFrequency()` method searches within a system's sites for frequency matches
  - Uses 10 kHz tolerance (0.01 MHz) for frequency matching to account for variations
  - Applies during call ingestion and before database write
  - Only searches within the specific system the call belongs to
  - Populates `siteRef` field automatically when not provided by upload source
  - Files modified: server/site.go, server/call.go, server/controller.go

- **Advanced duplicate call detection system with intelligent site and API key prioritization**
  - Introduces two duplicate detection modes: Legacy (time-based only) and Advanced (site + frequency + API key aware)
  - **Legacy Mode**: Original behavior - rejects duplicate calls within configurable time window
  - **Advanced Mode**: Enhanced detection using preferred sites, frequency validation, and API key enforcement
  - **Preferred Sites**: Mark sites as preferred; calls from preferred sites are accepted immediately and cancel queued secondary site calls
  - **Secondary Site Queueing**: Calls from non-preferred sites are held for configurable time (default 2 seconds); automatically processed if no preferred site call arrives
  - **Preferred API Keys**: Assign preferred upload API keys to systems or talkgroups; preferred API key calls are prioritized over others
  - **Frequency Validation**: Sites can have configured frequencies for validation in advanced mode
  - **Smart Fallback**: Automatically falls back to legacy time-based detection if advanced configuration (sites, API keys) is not configured
  - **Separate Time Windows**: Independent configurable time frames for legacy and advanced modes
  - **Call Queue System**: New intelligent queueing system for delayed call ingestion with automatic cancellation
  - Configuration: Admin → Config → Options → Duplicate Call Detection
  - Files added: server/call_queue.go
  - Files modified: server/controller.go, server/call.go, server/api.go, server/options.go, server/defaults.go, server/migrations.go, server/site.go, server/system.go, server/talkgroup.go, client/src/app/components/rdio-scanner/admin/admin.service.ts, client/src/app/components/rdio-scanner/admin/config/options/options.component.html, client/src/app/components/rdio-scanner/admin/config/systems/system/system.component.html, client/src/app/components/rdio-scanner/admin/config/systems/talkgroup/talkgroup.component.html

- **Site configuration enhancements with P25 system support**
  - **Site ID as string**: Changed from numeric to string format to preserve leading zeros (e.g., "001", "021", "050")
  - **RFSS field**: Added Radio Frequency Sub-System ID field for P25 Phase 2 systems
  - **Frequencies array**: Sites can now store multiple frequencies for frequency validation in advanced duplicate detection
  - **Preferred site flag**: Mark one site per system as preferred for advanced duplicate detection
  - Backward compatibility: Existing numeric site IDs automatically converted to strings during migration
  - Database migration handles type conversion from INTEGER to TEXT for siteRef column
  - Files modified: server/site.go, server/migrations.go, server/call.go, server/api.go, server/dirwatch.go, server/parsers.go, client/src/app/components/rdio-scanner/admin/admin.service.ts, client/src/app/components/rdio-scanner/admin/config/systems/site/site.component.html

- **Radio Reference import improvements with comprehensive state persistence**
  - **Full state persistence**: All selections, loaded data, and filters automatically saved and restored across page reloads
  - **Persistent data**: Import type, target system, country, state, county, selected system, categories, talkgroups, and sites
  - **Site import enhancements**: Added site selection with checkboxes, pagination (25/50/100/250 per page), and bulk selection options
  - **Frequency import**: Sites imported from Radio Reference now include all frequencies from the siteFreqs data
  - **RFSS import**: RFSS (Radio Frequency Sub-System) values are now imported and assigned to sites
  - **Improved site review**: Separate review table for sites showing RFSS, Site ID, Name, County, Latitude, Longitude, and Frequencies
  - **Site filtering**: Search sites by ID, name, or county with real-time filtering
  - **Clear saved state**: Added button to manually clear saved state if needed
  - **Better UX**: No need to re-query dropdowns or reselect options when returning to the import page
  - Files modified: client/src/app/components/rdio-scanner/admin/tools/radio-reference-import/radio-reference-import.component.ts, client/src/app/components/rdio-scanner/admin/tools/radio-reference-import/radio-reference-import.component.html, server/radioreference.go

### New Features

- **Simplified user registration settings for better UX**
  - Removed confusing "Public Registration Mode" sub-option dropdown (Codes Only / Email Invites Only / Both)
  - Public registration now defaults to supporting both codes and email invites by default
  - Cleaner, more intuitive admin interface with fewer configuration steps
  - Files modified: client/src/app/components/rdio-scanner/admin/config/user-registration/user-registration.component.html, client/src/app/components/rdio-scanner/admin/config/user-registration/user-registration.component.ts

### Bug Fixes

- **Fixed tone detection for overlapping two-tone paging sequences**
  - Relaxed sequencing requirements to support overlapping tones (common in two-tone paging)
  - Changed from requiring "B-tone must end after A-tone ends" to "B-tone must start after A-tone starts"
  - Now properly detects both sequential tones (A then B) and overlapping tones (A+B simultaneously)
  - Allows B-tone to start anytime during A-tone's duration (full overlap support)
  - Files modified: server/tone_detector.go

- **Improved tone detection for closely-spaced frequencies**
  - Added local maximum (peak) detection to FFT analysis
  - Only processes local maxima instead of all bins above threshold
  - Better separates closely-spaced tones (e.g., 556 Hz and 598 Hz with 42 Hz separation)
  - Prevents false merging of distinct simultaneous tones
  - Files modified: server/tone_detector.go

- **Fixed tone tolerance calculation in debug output**
  - Corrected tolerance calculation from `frequency * tolerance` to `tolerance * 500 Hz`
  - Debug output now shows accurate tolerance values matching actual detection logic
  - Example: 0.04 tolerance now correctly displays as ±20 Hz instead of ±22.24 Hz (for 556 Hz tone)
  - Files modified: server/tone_detector.go

- **Fixed SQL error when inserting calls with empty siteRef**
  - Converts string `siteRef` to integer before database insertion
  - Defaults to 0 when siteRef is empty or invalid
  - Resolves "syntax error at or near )" error in PostgreSQL
  - Files modified: server/call.go

- **Fixed admin purge functionality display issues**
  - "Purge All Logs" button now correctly shows it only deletes logs (not logs and calls)
  - "Purge All Calls" button now correctly shows it only deletes calls (not calls and logs)  
  - Clarified warning text to state each button only affects its specific data type
  - Reduced confirmation steps from 3 to 1 (single typed confirmation with full warning text)
  - Split purging state into separate `purgingCalls` and `purgingLogs` flags
  - Prevents both buttons showing "Purging..." when only one operation is active
  - Files modified: client/src/app/components/rdio-scanner/admin/tools/purge-data/purge-data.component.ts, client/src/app/components/rdio-scanner/admin/tools/purge-data/purge-data.component.html

- **Fixed web app display after transmission ends**
  - Talkgroup description and call info now properly clear when transmission finishes
  - "SCANNING" animation displays correctly when live feed is active and no call is playing
  - Finished call immediately moves to "Last 10 Transmissions" history
  - All call display variables reset to defaults (system, tag, talkgroup, unit, etc.)
  - Prevents stale call information from remaining on screen
  - Files modified: client/src/app/components/rdio-scanner/main/main.component.ts

- **Enhanced user registration and login experience with improved password security**
  - Email addresses are now automatically converted to lowercase in all forms (registration, login, forgot password) to prevent case-sensitivity issues
  - Lowercase conversion happens both as users type and when forms are submitted
  - Added prominent email verification notice after successful registration with visual emphasis
  - Users are now clearly directed to check their email inbox (and spam folder) for the verification link
  - Email verification notice appears in both standalone registration page and main auth screen
  - **Added special character requirement to passwords for improved security**
  - Password requirements now include: 8+ characters, uppercase, lowercase, number, and special character
  - Fixed password validation error overflow by displaying all missing requirements in a single compact line
  - Error format: "Missing: 8+ chars, uppercase, lowercase, number, special char" (only shows what's missing)
  - Prevents text overflow into confirm password field with comma-separated inline format
  - "Passwords do not match" error now correctly appears only on the confirm password field
  - Applied to all authentication forms: user registration, user login, group admin login, and password reset
  - Files modified: client/src/app/components/rdio-scanner/user-registration/user-registration.component.ts, client/src/app/components/rdio-scanner/user-registration/user-registration.component.html, client/src/app/components/rdio-scanner/user-login/user-login.component.ts, client/src/app/components/rdio-scanner/user-login/user-login.component.html, client/src/app/components/rdio-scanner/auth-screen/auth-screen.component.ts, client/src/app/components/rdio-scanner/auth-screen/auth-screen.component.html, client/src/app/components/rdio-scanner/group-admin/group-admin-login.component.ts

- **Restored configurable Whisper transcription worker pool size with safety warnings**
  - Re-added worker pool size configuration for Whisper API transcription (previously removed in Beta 8)
  - Users with sufficient VRAM (8GB+) can now run multiple concurrent Whisper workers for faster transcription processing
  - Default remains at 1 worker for safety and stability
  - **Prominent warnings added in admin UI** about potential transcription failures when using multiple workers with insufficient resources
  - Recommended approach: Start with 1 worker, monitor for failures, and increase only if system has adequate VRAM
  - Cloud providers (Azure, Google, AssemblyAI) can typically handle 3-5+ workers without issues
  - Worker pool size configurable from 1-10 workers with provider-specific hints in UI
  - Configuration: Admin → Config → Options → Transcription Settings → Worker Pool Size
  - Files modified: client/src/app/components/rdio-scanner/admin/config/options/options.component.html, client/src/app/components/rdio-scanner/admin/admin.service.ts, server/transcription_queue.go

- **Configurable loudness normalization presets with multiple loudness targets**
  - Replaced basic/loud normalization with four industry-standard loudness presets
  - **Conservative (-16 LUFS)**: Broadcast TV/radio standard (EBU R128), preserves high dynamic range, safest for all content
  - **Standard (-12 LUFS)**: Modern streaming standard (YouTube, Spotify), 4 dB louder than conservative, recommended default
  - **Aggressive (-10 LUFS)**: Dispatcher/public safety optimized, 6 dB louder with compressed dynamics for consistent volume
  - **Maximum (-8 LUFS)**: Maximum loudness, 8 dB louder than conservative, heavily compressed with minimal dynamics
  - **Bidirectional normalization**: Automatically boosts quiet channels AND reduces loud channels to target level for consistent listening experience
  - **EBU R128 compliant**: Uses FFmpeg's `loudnorm` filter based on broadcast industry standards (LUFS = Loudness Units relative to Full Scale)
  - **Dynamic range control**: Each preset balances loudness target with appropriate dynamic range preservation (LRA values from 11 to 5)
  - **True peak limiting**: Prevents clipping and distortion with appropriate headroom for each loudness level
  - **Enhanced over-modulated signal handling**: Added pre-limiter (`alimiter`) before loudnorm to catch extreme peaks, significantly improves handling of hot/distorted audio
  - **Linear mode processing**: Uses linear mode (`linear=true`) for better quality and more accurate normalization
  - **Dual mono optimization**: Optimized for mono scanner audio sources (`dual_mono=true`)
  - **Fallback for older FFmpeg**: Automatically falls back to `dynaudnorm` filter if FFmpeg < 4.3 (with user warning to upgrade)
  - Solves common issue where some channels are naturally quieter and hard to hear even with normalization
  - Fixes reported issue where over-modulated signals weren't being properly reduced to target levels
  - Provides flexibility for different use cases: monitoring (conservative), general listening (standard), dispatch operations (aggressive/maximum)
  - Admin UI includes helpful descriptions and hints explaining how normalization affects both quiet and loud channels
  - Configuration: Admin → Config → Options → Audio Conversion (select from dropdown)
  - Files modified: server/options.go, server/ffmpeg.go, client/src/app/components/rdio-scanner/admin/config/options/options.component.html

### Performance Improvements

- **Admin systems and units page performance enhancements**
  - **Pagination**: Systems, talkgroups, units, and sites now display in pages of 50 items each
  - **Search functionality**: Added search bars to filter by label, name, or ID for instant results
  - **Cached sorting**: Optimized sorting algorithms to prevent redundant array operations on every change detection
  - **Reduced DOM nodes**: Only renders visible items per page, dramatically improving load times and responsiveness
  - **Real-time filtering**: Search results update instantly with item counts displayed
  - **Backward navigation**: Pagination controls include first/previous/next/last page buttons
  - Significantly improves admin interface performance for systems with hundreds or thousands of talkgroups/units
  - Files modified: client/src/app/components/rdio-scanner/admin/config/systems/systems.component.ts, client/src/app/components/rdio-scanner/admin/config/systems/systems.component.html, client/src/app/components/rdio-scanner/admin/config/systems/system/system.component.ts, client/src/app/components/rdio-scanner/admin/config/systems/system/system.component.html

### Bug Fixes

- **Fixed System Health Alert settings save failure (Issue #82)**
  - Added missing database migration for system health alert option keys
  - Users upgrading from older versions were missing required options table entries, causing 500 errors when saving settings
  - Migration now initializes all 16 system health alert options with default values if they don't exist
  - Fixes: systemHealthAlertsEnabled, transcriptionFailureAlertsEnabled, toneDetectionAlertsEnabled, noAudioAlertsEnabled, and related threshold/window/repeat settings
  - Migration runs automatically on server startup for existing installations
  - Issue reported: https://github.com/Thinline-Dynamic-Solutions/ThinLineRadio/issues/82
  - Files modified: server/migrations.go, server/database.go

- **Fixed Radio Reference site frequency parsing**
  - Corrected XML parsing to extract frequencies from `<siteFreqs><item><freq>` nodes
  - Frequencies are now properly parsed and included in site data from Radio Reference API
  - Added comprehensive logging for debugging frequency extraction
  - Files modified: server/radioreference.go

- **Fixed frequency data type in site imports**
  - Corrected frequency storage format from strings to numbers for proper database handling
  - Frequencies now correctly saved as JSON array of float64 values
  - Resolves issue where frequencies appeared in import preview but disappeared after save
  - Files modified: client/src/app/components/rdio-scanner/admin/tools/radio-reference-import/radio-reference-import.component.ts

### Database Changes

- Added `rfss` column to sites table (INTEGER, default 0)
- Changed `siteRef` column type from INTEGER to TEXT to preserve leading zeros
- Added `frequencies` column to sites table (TEXT, JSON array of floats)
- Added `preferred` column to sites table (BOOLEAN, default false)
- Added `preferredApiKeyId` column to systems table (INTEGER, nullable)
- Added `preferredApiKeyId` column to talkgroups table (INTEGER, nullable)
- Added `advancedDetectionTimeFrame` column to options table (INTEGER, default 1000)
- Migration automatically converts existing numeric siteRef values to strings

### Technical Notes

- Call queue system uses in-memory storage with timer-based expiration and cancellation
- Advanced duplicate detection checks database for existing calls before queueing
- Preferred site/API key calls bypass queue and immediately cancel any pending secondary calls
- Secondary calls are automatically processed after timeout if no preferred call arrives
- Site ID string format supports any text format (recommended: zero-padded decimals like "001")
- Frequency validation in advanced mode requires both site frequencies to be configured

## Version 7.0 Beta 8 - Released TBD

### New Features

- **Universal dispatch tone removal for transcription**
  - Added automatic detection and removal of ALL dispatch tones before transcription (200-5000Hz range)
  - Prevents Whisper hallucinations caused by two-tone sequential, quick call, and long tone paging systems
  - Works on all audio regardless of whether tone detection is enabled for the talkgroup
  - Detects sustained tones using FFT analysis with dynamic noise floor estimation
  - Removes detected tone segments using ffmpeg while preserving voice audio
  - Minimum tone duration: 500ms (catches all typical dispatch tones)
  - Skips transcription if less than 2 seconds of voice audio remains after tone removal
  - Provides detailed logging: detected tones with frequencies, durations, and removal status
  - Significantly improves transcription quality by eliminating tone-induced hallucination phrases
  - Files modified: server/tone_detector.go, server/transcription_queue.go

- **AssemblyAI word boost support for improved transcription accuracy**
  - Added word boost/keyterms feature for AssemblyAI transcription provider
  - Allows administrators to provide a list of words or phrases to improve recognition accuracy
  - Particularly useful for: unit names, technical terms, proper names, local terminology, call signs
  - Configuration: Enter words/phrases in Admin UI (one per line) under Options → Transcription → AssemblyAI Word Boost
  - Maximum 100 terms, each up to 50 characters
  - Terms are automatically validated and filtered before being sent to AssemblyAI
  - Only visible when AssemblyAI is selected as the transcription provider
  - Files modified: server/options.go, server/transcription_provider.go, server/transcription_assemblyai.go, server/transcription_queue.go, client/src/app/components/rdio-scanner/admin/admin.service.ts, client/src/app/components/rdio-scanner/admin/config/config.component.ts, client/src/app/components/rdio-scanner/admin/config/options/options.component.html

- **Enhanced transcripts tab with filtering and search capabilities**
  - Added comprehensive filtering controls to the Transcripts tab in the Alerts UI
  - Filter by system: Dropdown to filter transcripts by specific radio system
  - Filter by talkgroup: Dropdown to filter transcripts by specific talkgroup (filtered by selected system)
  - Filter by date range: Date inputs to filter transcripts by "From" and "To" dates
  - Search functionality: Text search bar to find specific words or phrases within transcript text
  - Search highlighting: Matching search terms are highlighted in yellow within displayed transcripts
  - Clear filters button: Quick reset of all filter criteria
  - Real-time filtering: Filters apply immediately as selections change
  - Backend API enhancements: Added support for systemId, talkgroupId, dateFrom, dateTo, and search query parameters
  - Proper systemRef/talkgroupRef resolution: Backend correctly resolves radio reference IDs to database IDs for filtering
  - Files modified: server/api.go (TranscriptsHandler), client/src/app/components/rdio-scanner/alerts/alerts.component.ts, client/src/app/components/rdio-scanner/alerts/alerts.component.html, client/src/app/components/rdio-scanner/alerts/alerts.component.scss, client/src/app/components/rdio-scanner/alerts/alerts.service.ts

- **Whisper transcription worker optimization - single worker configuration**
  - Removed configurable worker pool size option for Whisper transcription
  - Whisper API provider (local Whisper) now always uses exactly 1 worker
  - Testing showed that using 1 worker eliminated all transcription failures
  - Multiple workers were causing race conditions and failures with local Whisper
  - Other transcription providers (Azure, Google, AssemblyAI) continue to use configurable workers
  - Worker pool size UI field removed from Admin → Options → Transcription settings
  - Files modified: server/transcription_queue.go, client/src/app/components/rdio-scanner/admin/config/options/options.component.html, client/src/app/components/rdio-scanner/admin/admin.service.ts

- **Configurable repeat alert timing for system health monitoring**
  - Added individual repeat interval settings for each alert type (Transcription Failures, Tone Detection Issues, No Audio Received)
  - Administrators can now configure how often alerts repeat when issues persist
  - Default values: 60 minutes for transcription and tone detection alerts, 30 minutes for no audio alerts
  - Configuration available in Admin → System Health → Additional Settings column for each alert type
  - Prevents alert spam by allowing customization of repeat frequency per alert category
  - Files modified: server/options.go, server/defaults.go, server/admin.go, server/system_alert.go, client/src/app/components/rdio-scanner/admin/system-health/system-health.component.ts, client/src/app/components/rdio-scanner/admin/system-health/system-health.component.html, client/src/app/components/rdio-scanner/admin/admin.service.ts

- **Enhanced system alerts display with intelligent grouping and management**
  - Redesigned system alerts interface with professional category-based grouping
  - Alerts are now organized by type: "No Audio Received", "Tone Detection Issues", "Transcription Failures", and "Other Alerts"
  - Each group displays alert count badge for quick overview
  - Removed technical clutter: System IDs, raw JSON data, and technical metadata hidden from display
  - Individual alert dismissal: Each alert has a dismiss button (X icon) in the header
  - Bulk dismissal: "Clear All" button for each alert group to dismiss all alerts in a category at once
  - Confirmation dialogs: Prevent accidental dismissals with clear confirmations showing alert counts
  - Success notifications: Snackbar feedback when alerts are dismissed (individual or bulk)
  - Improved visual hierarchy: Group headers with clear categorization, better spacing and organization
  - Active alerts only: Statistics and displays only show non-dismissed alerts
  - Enhanced description for No Audio monitoring: Updated to "Intelligent adaptive monitoring: Continuously analyzes historical audio patterns by time of day, learns normal activity baselines, and dynamically adjusts alert thresholds to reduce false positives while maintaining sensitivity to genuine issues"
  - Files modified: client/src/app/components/rdio-scanner/admin/system-health/system-health.component.ts, client/src/app/components/rdio-scanner/admin/system-health/system-health.component.html, client/src/app/components/rdio-scanner/admin/system-health/system-health.component.scss, client/src/app/components/rdio-scanner/admin/admin.service.ts

- **Purge logs and calls from admin UI with selective deletion support**
  - Added purge functionality to Admin → Tools → Purge Data section
  - **Purge All**: Delete all logs or all calls with triple confirmation (warning dialog, final confirmation, typed confirmation)
  - **Selective Delete**: Search and filter logs/calls, then select specific items for deletion
  - **Logs Management**: Search by date, level (info/warn/error), sort order; select individual items or batches
  - **Calls Management**: Search by date, system, talkgroup, sort order; select individual items or batches
  - **Selection Options**: "Select All on Page" (current 10 items), "Select All in Batch" (current 200 items), "Deselect All"
  - Pagination support: Navigate through results while maintaining selections
  - Confirmation dialogs: Requires confirmation before deleting selected items
  - Visual feedback: Selected count displayed, success/error notifications
  - Backend API: `/api/admin/purge` endpoint accepts `{type: 'calls'|'logs', ids?: number[]}` for selective or bulk deletion
  - Admin authentication required with localhost restriction (same as other admin endpoints)
  - Files modified: server/call.go, server/log.go, server/admin.go, server/main.go, client/src/app/components/rdio-scanner/admin/admin.service.ts, client/src/app/components/rdio-scanner/admin/admin.module.ts, client/src/app/components/rdio-scanner/admin/tools/purge-data/*
  - Files added: client/src/app/components/rdio-scanner/admin/tools/purge-data/purge-data.component.ts, purge-data.component.html, purge-data.component.scss

### Bug Fixes

- **Fixed handling of unknown radio IDs from Trunk Recorder**
  - Trunk Recorder sends -1 for transmissions where the radio ID could not be determined (no value over the air or control channel)
  - Previously, -1 was being converted to an unsigned integer (18446744073709551615), causing database insertion errors
  - Error message: "bigint out of range (SQLSTATE 22003)" when attempting to insert call units
  - Solution: Added validation to skip negative source IDs before converting to unsigned integers
  - Unknown transmissions (src: -1) are now gracefully ignored instead of causing database errors
  - Affects all parsing methods: Trunk Recorder srcList, generic sources/units, and unit field
  - Files modified: server/parsers.go

### Changes

- **Talkgroups with tone detection now transcribe short audio clips after tone removal**
  - Previously, calls with less than 2 seconds of audio remaining after tone removal were skipped
  - Now, if a talkgroup has tone detection enabled, short clips are transcribed regardless of remaining duration
  - This ensures important dispatch messages aren't missed even if they're brief after tones are removed
  - Applies to both the pre-queue duration check and the transcription worker check
  - Example: "RESPOND CODE 3" after tone removal might only be 1.5 seconds but is now transcribed
  - Files modified: server/controller.go, server/transcription_queue.go

- **Removed emojis from all email subjects and body content to reduce spam marking**
  - Emojis are automatically stripped from all email subjects and body text (both HTML and plain text)
  - Improves email deliverability by avoiding spam filters that flag emoji-heavy emails
  - Applies to all email types: verification, password reset, invitations, transfers, and test emails
  - Works with all email providers: SendGrid, Mailgun, and SMTP
  - Emoji removal uses comprehensive regex patterns covering all major emoji ranges
  - Files modified: server/email.go

- **Opus codec is now the default for new audio recordings**
  - Changed default from M4A/AAC to Opus codec for 50% storage savings
  - Provides superior voice quality at lower bitrates (16 kbps Opus vs 32 kbps AAC)
  - Can be disabled in `thinline-radio.ini` by setting `opus = false` to revert to M4A/AAC
  - Only affects NEW calls - existing calls remain unchanged
  - Migration tool remains optional (set `opus_migration = true` in INI to convert existing audio)
  - Browser and mobile app compatibility: Chrome/Edge/Firefox/Safari 14+, Android 5.0+, iOS 11+
  - Files modified: server/config.go

### Bug Fixes

- **Fixed playback sort order and date filtering behavior**
  - Fixed inconsistent behavior when selecting a specific date in playback mode
  - When a date is selected, calls now always start from that date forward (>= selected date)
  - Sort order now correctly controls display order: "Newest First" shows most recent calls first (DESC), "Oldest First" shows oldest calls first (ASC)
  - Previously, "Newest First" with a selected date would show calls before the selected date (backwards in time)
  - Now both sort orders show calls from the selected date forward, just in different order
  - Mobile app: Fixed reversed sort order labels that displayed "Newest First" when actually showing oldest first
  - Improves intuitive behavior: selecting a date means "show me calls from this point forward"
  - Files modified: server/call.go, ThinlineRadio-Mobile/lib/screens/playback/playback_screen.dart

- **Fixed foreign key constraint violation when creating pre-alerts for tone detection**
  - Fixed "ERROR: insert or update on table 'alerts' violates foreign key constraint" error
  - Pre-alerts are now instant notifications only and are not saved to the database
  - Removed unnecessary database lookups and insert operations for pre-alerts
  - Pre-alerts now send immediately when tones are detected without waiting for call to be saved
  - Improves pre-alert delivery speed and eliminates race condition errors
  - Files modified: server/alert_engine.go, server/controller.go

- **Fixed authorization error when dismissing system alerts from admin UI**
  - Fixed "API unauthorized" error when clicking "Clear All" or individual dismiss buttons in System Health
  - Updated alert dismissal to use admin token authentication instead of WebSocket client authentication
  - Added POST handler to `/admin/systemhealth` endpoint for dismissing alerts

- **Fixed pending tones never attaching to voice calls due to timestamp mismatch**
  - Critical fix: Pending tone timestamps were using processing time instead of actual call transmission time
  - This caused voice calls to be incorrectly rejected as "came before pending tones" even when they came after
  - Example: Tone call at 12:00:00.000, processed and stored at 12:00:01.200, voice call at 12:00:00.500 would be rejected
  - Now uses `call.Timestamp` (radio transmission time) instead of `time.Now()` (processing time) for pending tones
  - Ensures timestamp comparisons accurately reflect the actual sequence of radio transmissions
  - Fixes issue where pending tones would lock/unlock properly but never attach to any voice calls
  - Files modified: server/controller.go (storePendingTones function)
  - Allows administrators to dismiss individual alerts or bulk dismiss alert groups
  - Files modified: server/admin.go, client/src/app/components/rdio-scanner/admin/admin.service.ts

- **Fixed UI text cutoff issues in system health settings**
  - Fixed "Repeat Interval" label text being cut off on Transcription Failures and Tone Detection Issues rows
  - Increased field width from 140px to 160px for repeat interval dropdowns
  - Improved label wrapper CSS to allow text to wrap and display fully
  - Files modified: client/src/app/components/rdio-scanner/admin/system-health/system-health.component.html, client/src/app/components/rdio-scanner/admin/system-health/system-health.component.scss

- **Fixed critical data integrity bug with keyword lists and user alert preferences**
  - Fixed destructive bug where keyword lists and user alert preferences were deleted and recreated on ANY admin config save
  - Root cause: Admin config handler was ignoring the `isFullImport` flag and processing keyword lists/preferences deletions on all saves
  - Previously, editing unrelated settings (systems, talkgroups, etc.) would delete ALL keyword lists and recreate them with new IDs
  - This caused user alert preferences to reference non-existent keyword list IDs, breaking keyword-based alerts
  - PostgreSQL auto-increment sequences caused IDs to jump (e.g., 41-44 → 53-56), orphaning existing user references
  - Now keyword lists and user alert preferences are ONLY processed during explicit full config imports (with `X-Full-Import: true` header)
  - Normal admin saves no longer touch keyword lists or user alert preferences, maintaining data integrity
  - Added automatic migration on server startup to repair existing orphaned keyword list ID references
  - Migration detects orphaned IDs and maps them to current keyword lists by position (maintains user selections)
  - Migration runs once automatically and tracks completion to avoid re-running
  - This matches the protection pattern already correctly implemented for users (which properly checked `isFullImport`)
  - Files modified: server/admin.go (lines 1246-1299 and 1302-1377), server/fix_keyword_list_ids.go, server/database.go
  - Prevents hundreds of users from losing their alert preferences when administrators make routine config changes

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
