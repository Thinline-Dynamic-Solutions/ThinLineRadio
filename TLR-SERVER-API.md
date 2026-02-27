# ThinLine Radio Server — API Reference

This document describes every HTTP and WebSocket endpoint exposed by the TLR server.
It is intended for developers building alternative front-ends, billing integrations, or
multi-server management tools.

---

## General Notes

| Topic | Detail |
|---|---|
| Default port | `3000` (HTTP). Configurable via `thinline-radio.ini`. |
| Content type | All endpoints accept and return `application/json` unless noted. |
| Authentication | Varies by endpoint family — see individual sections. |
| Rate limiting | Applied globally. Repeated failed auth attempts trigger a 15-minute IP block. |
| CORS | `*` is allowed on user-facing and alert endpoints. Admin and webhook endpoints do **not** emit CORS headers. |

---

## Authentication Schemes

### 1. Admin JWT
Obtained via `POST /api/admin/login`. Pass in the `Authorization` header:
```
Authorization: <token>
```
All `/api/admin/*` endpoints require this token. Admin endpoints are **localhost-only** by default (configurable via `admin_localhost_only` in the INI file).

### 2. User Bearer Token
Obtained via `POST /api/user/login`. Pass in the `Authorization` header:
```
Authorization: Bearer <token>
```

### 3. Management API Key (`X-API-Key`)
A shared secret configured in the server's options. Required by all `/api/webhook/central-*` and `/api/central-management/*` endpoints:
```
X-API-Key: <api_key>
```

### 4. API Key (call upload)
A per-system API key configured in the admin panel. Passed as a query parameter or in the request body depending on the upload format.

---

## WebSocket — Real-time Audio Playback

```
ws://<server>/
wss://<server>/
```

The root path doubles as the WebSocket upgrade endpoint. Connect with a standard WebSocket handshake (set `Upgrade: websocket`). Once connected the server sends audio call events in real time. Authentication is handled through the WebSocket message protocol after connection.

---

## User Registration & Authentication

All endpoints in this section are public (no prior auth required) and CORS-enabled.

### `GET /api/public-registration-info`
Returns the server's public-facing display name, registration mode, and whether a Stripe paywall is active.

**Response**
```json
{
  "branding": "My Scanner Server",
  "userRegistrationEnabled": true,
  "stripePaywallEnabled": false
}
```

---

### `GET /api/public-registration-channels`
Returns the list of systems and talkgroups available for a prospective user to browse before registering.

---

### `GET /api/registration-settings`
Returns whether registration requires an invitation code or access code, and any other sign-up restrictions.

---

### `POST /api/user/register`
Create a new user account.

**Body**
```json
{
  "email": "user@example.com",
  "password": "...",
  "firstName": "Jane",
  "lastName": "Doe",
  "turnstileToken": "..."   // required if Turnstile is enabled on the server
}
```

**Responses**
- `201` — account created, verification email sent
- `400` — validation error
- `409` — email already registered

---

### `POST /api/user/login`
Authenticate an existing user.

**Body**
```json
{
  "email": "user@example.com",
  "password": "..."
}
```

**Response**
```json
{
  "token": "<jwt>",
  "user": { "id": 1, "email": "...", "firstName": "...", "lastName": "..." }
}
```

---

### `POST /api/user/verify`
Verify a user's email address using the code sent after registration.

**Body**
```json
{ "token": "<verification_token>" }
```

---

### `POST /api/user/resend-verification`
Re-send the verification email.

**Body**
```json
{ "email": "user@example.com" }
```

---

### `POST /api/user/validate-invitation`
Check whether an invitation code is valid before showing the registration form.

**Body**
```json
{ "code": "INVITE-CODE" }
```

---

### `POST /api/user/validate-access-code`
Validate an access / registration code.

**Body**
```json
{ "code": "ACCESS-CODE" }
```

---

### `POST /api/user/forgot-password`
Request a password-reset email.

**Body**
```json
{ "email": "user@example.com" }
```

---

### `POST /api/user/reset-password`
Complete a password reset using the token from the email.

**Body**
```json
{ "token": "<reset_token>", "password": "newpassword" }
```

---

### `POST /api/user/transfer-to-public`
Transfer a group-managed user account to a standalone account.

**Headers:** `Authorization: Bearer <token>`

---

### `POST /api/user/device-token`
Register a push notification device token (mobile apps).

**Headers:** `Authorization: Bearer <token>`

**Body**
```json
{ "token": "<fcm_or_apns_token>", "platform": "ios" }
```

---

## Account Management

All endpoints in this section require `Authorization: Bearer <token>`.

### `GET /api/account`
Return the authenticated user's profile and current subscription/access details.

---

### `GET /api/settings` · `POST /api/settings`
Get or save user preferences (theme, playback settings, etc.).

**POST body** — arbitrary JSON object; the server stores and returns it verbatim.

---

### Email change flow
| Step | Endpoint |
|---|---|
| 1. Request OTP | `POST /api/account/email/request-verification` |
| 2. Validate OTP | `POST /api/account/email/verify-code` — body: `{ "code": "123456" }` |
| 3. Apply new email | `POST /api/account/email` — body: `{ "email": "new@example.com" }` |
| 4. Verify new address | `POST /api/account/email/verify-new` — body: `{ "token": "..." }` |

### Password change flow
| Step | Endpoint |
|---|---|
| 1. Request OTP | `POST /api/account/password/request-verification` |
| 2. Validate OTP | `POST /api/account/password/verify-code` — body: `{ "code": "123456" }` |
| 3. Apply new password | `POST /api/account/password` — body: `{ "password": "..." }` |

---

## Alerts & Transcripts

These endpoints are CORS-enabled and require `Authorization: Bearer <token>`.

### `GET /api/alerts`
Return alert notifications for the authenticated user.

---

### `GET /api/alerts/preferences` · `POST /api/alerts/preferences`
Get or save the user's per-talkgroup/system alert preferences (tone alerts, keyword alerts, etc.).

---

### `GET /api/transcripts`
Return transcripts for calls the authenticated user has access to.

Query params:
- `callId` — filter by specific call
- `systemRef`, `talkgroupRef` — filter by system / talkgroup

---

### `GET /api/system-alerts`
Return system health alerts visible to the authenticated user (requires system-admin role).

---

### `DELETE /api/system-alerts/{alertId}`
Dismiss a system health alert.

---

## Keyword Lists

Require `Authorization: Bearer <token>`.

### `GET /api/keyword-lists`
Return all keyword lists the user has access to.

### `GET /api/keyword-lists/{id}` · `POST /api/keyword-lists/{id}` · `DELETE /api/keyword-lists/{id}`
Get, update, or delete a single keyword list by ID.

---

## Call Upload (Recorder → Server)

These endpoints are **not** rate-limited or wrapped with security headers so recorders can post calls at high frequency. Authentication is via API key embedded in the payload.

### `POST /api/call-upload`
Standard TLR call upload. Multipart form or JSON depending on recorder.

**Key fields**
| Field | Type | Description |
|---|---|---|
| `key` | string | API key configured in the admin panel |
| `system` | integer | System reference (decimal radio system ID) |
| `talkgroup` | integer | Talkgroup reference |
| `dateTime` | string | ISO-8601 UTC timestamp |
| `audio` | file | Audio file (MP3, M4A, WAV, or Opus) |
| `frequencies` | JSON array | List of frequencies used |
| `sources` | JSON array | List of source unit IDs |

---

### `POST /api/trunk-recorder-call-upload`
Trunk Recorder native format upload. Accepts the JSON metadata file and audio file produced directly by Trunk Recorder.

---

## Billing (Stripe Integration)

### `POST /api/stripe/create-checkout-session`
Create a Stripe Checkout session. Returns a `{ url }` to redirect the user to for payment.

**Headers:** `Authorization: Bearer <token>`

---

### `POST /api/stripe/webhook`
Stripe sends signed events here (subscription created/cancelled, payment succeeded/failed, etc.). The server verifies the Stripe signature and applies the relevant access changes automatically. **Do not call this endpoint directly.**

---

### `POST /api/billing/portal`
Create a Stripe Customer Portal session so the user can manage their subscription.

**Headers:** `Authorization: Bearer <token>`

**Response**
```json
{ "url": "https://billing.stripe.com/session/..." }
```

---

## Group Administration

Group admins are regular users promoted to manage a subset of users within a group. These endpoints do **not** require the main admin password.

### `POST /api/group-admin/login`
Authenticate as a group admin.

**Body**
```json
{ "email": "admin@example.com", "password": "..." }
```

**Response** — returns a Bearer token scoped to group-admin operations.

---

### `GET /api/group-admin/users`
List all users in the authenticated admin's groups.

**Headers:** `Authorization: Bearer <token>`

---

### `POST /api/group-admin/add-user`
Create and add a new user to the group.

### `POST /api/group-admin/add-existing-user`
Add an already-registered user to the group by email.

### `POST /api/group-admin/remove-user`
Remove a user from the group.

### `POST /api/group-admin/toggle-admin`
Promote or demote a group member to/from group-admin status.

### `POST /api/group-admin/invite-user`
Send an invitation email to a new user with a pre-filled sign-up link.

### `GET /api/group-admin/codes`
List active access codes for the group.

### `POST /api/group-admin/generate-code`
Generate a new single-use or multi-use access code.

### `DELETE /api/group-admin/codes/{codeId}`
Delete an access code.

### `GET /api/group-admin/available-groups`
List the groups the authenticated admin manages.

### `POST /api/group-admin/request-transfer`
Request a user be transferred from one group to another.

### `POST /api/group-admin/approve-transfer`
Approve a pending transfer request.

### `GET /api/group-admin/transfer-requests`
List pending transfer requests.

---

## Server Status

### `GET /api/status/performance`
Returns real-time server performance metrics. No authentication required.

**Response**
```json
{
  "cpu_cores": 8,
  "active_workers": 3,
  "total_calls": 145820,
  "avg_process_time": "42ms",
  "goroutines": 112,
  "memory_stats": {
    "alloc_mb": 128,
    "total_alloc_mb": 4096,
    "sys_mb": 256
  }
}
```

---

## Management Integration — Inbound Webhooks

The following endpoints allow an external billing or management system to control users on this TLR server. All require the `X-API-Key` header matching the server's configured management API key.

Central management must be enabled on the server (`central_management_enabled = true` in the options or set via the pairing endpoint).

---

### `POST /api/webhook/central-user-grant`
Create a new user or update an existing user's access. This is the primary endpoint for granting access after a successful billing event.

**Headers:** `X-API-Key: <api_key>`

**Body**
```json
{
  "email": "user@example.com",
  "firstName": "Jane",
  "lastName": "Doe",
  "pin": "123456",
  "systems": "*",
  "talkgroups": "*",
  "group_id": null,
  "connectionLimit": 2
}
```

| Field | Type | Description |
|---|---|---|
| `email` | string | **Required.** User's email address. |
| `pin` | string | **Required.** Numeric PIN the user enters to authenticate on the scanner client. |
| `firstName` / `lastName` | string | Optional display name. |
| `systems` | `"*"` or `[id, ...]` | Which systems the user can access. `"*"` = all. |
| `talkgroups` | `"*"` or `[id, ...]` | Which talkgroups the user can access. `"*"` = all. |
| `group_id` | integer \| null | Optional user group ID. |
| `connectionLimit` | integer | Maximum simultaneous WebSocket connections. `0` = unlimited. |

**Responses**
- `201 Created` — new user created
```json
{ "status": "created", "user_id": 42, "message": "User access granted successfully" }
```
- `200 OK` — existing user updated
```json
{ "status": "updated", "user_id": 42, "message": "User access updated successfully" }
```

---

### `POST /api/webhook/central-user-revoke`
Revoke a user's access immediately. Active WebSocket connections are disconnected in real time.

**Headers:** `X-API-Key: <api_key>`

**Body** — supply at least one of `email` or `pin`:
```json
{
  "email": "user@example.com",
  "pin": "123456"
}
```

**Response**
```json
{ "status": "revoked", "user_id": 42, "message": "User access revoked successfully" }
```

---

### `POST /api/webhook/central-users-batch-update`
Update the `connectionLimit` for multiple users in a single request. Useful when a billing plan's tier changes and affects many users simultaneously.

**Headers:** `X-API-Key: <api_key>`

**Body**
```json
{
  "updates": [
    { "email": "alice@example.com", "connectionLimit": 3 },
    { "email": "bob@example.com",   "connectionLimit": 3 }
  ]
}
```

**Response**
```json
{ "status": "ok", "updated": 2, "total": 2 }
```

---

### `GET /api/webhook/central-users`
Return all users currently registered on this server. Useful for syncing your management system's user list on initial connection or after a gap.

**Headers:** `X-API-Key: <api_key>`

**Response**
```json
{
  "status": "ok",
  "count": 3,
  "users": [
    {
      "id": 1,
      "email": "alice@example.com",
      "first_name": "Alice",
      "last_name": "Smith",
      "verified": true,
      "systems": "*",
      "talkgroups": "*",
      "user_group_id": null,
      "pin": "123456",
      "pin_active": true,
      "password_hash": "<sha256-hex>"
    }
  ]
}
```

> **Note:** `password_hash` is the SHA-256 hex of the user's password as stored on the TLR server. It is included solely for import/migration purposes. Handle with care.

---

### `GET /api/webhook/central-systems-talkgroups-groups`
Return a snapshot of all systems, their talkgroups, and user groups configured on this server. Use this to populate system/talkgroup selectors in your management UI when editing a user's access.

**Headers:** `X-API-Key: <api_key>`

**Response**
```json
{
  "status": "ok",
  "systems": [
    {
      "id": 1,
      "label": "County Fire",
      "talkgroups": [
        { "id": 100, "label": "Dispatch", "name": "Fire Dispatch", "tag": "Fire" }
      ]
    }
  ],
  "groups": [
    { "id": 1, "name": "Subscribers", "description": "Paid subscribers" }
  ]
}
```

---

### `GET /api/webhook/central-test`
Lightweight connectivity check. Returns `200 OK` if the server is reachable. Optionally pass `?api_key=<key>` to validate the key in the same request.

**Response**
```json
{
  "status": "ok",
  "message": "Connection test successful",
  "server": "Thinline Radio Server",
  "version": "7.0"
}
```

---

### `POST /api/webhook/central-set-relay-key`
Push a relay server API key to this TLR server so it can route push notifications through a shared relay. The key is persisted immediately.

**Headers:** `X-API-Key: <api_key>`

**Body**
```json
{ "relay_api_key": "<relay_key>" }
```

**Response**
```json
{ "status": "ok", "message": "Relay API key updated successfully" }
```

## Admin Endpoints (Localhost-Only)

These endpoints require a valid admin JWT and, by default, are only reachable from the same machine as the server. They are documented here for completeness but are not intended for use by external integrations.

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/admin/login` | Obtain an admin JWT |
| `POST` | `/api/admin/logout` | Invalidate the current token |
| `GET/PUT` | `/api/admin/config` | Get or replace the full server configuration |
| `POST` | `/api/admin/config/reload` | Reload config from database without restart |
| `POST` | `/api/admin/logs` | Search server log entries |
| `POST` | `/api/admin/calls` | Search recorded calls |
| `POST` | `/api/admin/purge` | Purge calls or logs |
| `POST` | `/api/admin/password` | Change the admin password |
| `GET` | `/api/admin/users` | List all users |
| `POST` | `/api/admin/users/create` | Create a user |
| `PUT` | `/api/admin/users/{id}` | Update a user |
| `DELETE` | `/api/admin/users/{id}` | Delete a user |
| `POST` | `/api/admin/users/{id}/reset-password` | Force-reset a user's password |
| `POST` | `/api/admin/users/{id}/test-push` | Send a test push notification |
| `DELETE` | `/api/admin/users/{id}/device-tokens/{tokenId}` | Remove a device token |
| `GET` | `/api/admin/alerts` | List system health alerts |
| `GET` | `/api/admin/systemhealth` | Get system health overview |
| `GET/POST` | `/api/admin/system-health-alert-settings` | Get or update health alert settings |
| `POST` | `/api/admin/system-no-audio-settings` | Update per-system no-audio alert settings |
| `GET` | `/api/admin/transcription-failures` | List transcription failures |
| `POST` | `/api/admin/email-test` | Send a test email |
| `POST` | `/api/admin/stripe-sync` | Sync users from Stripe |
| `POST` | `/api/admin/tone-import` | Import tone set definitions |
| `GET` | `/api/admin/call-audio/{callId}` | Stream raw audio for a specific call |
| `POST` | `/api/admin/email-logo` | Upload the email logo image |
| `POST` | `/api/admin/email-logo/delete` | Remove the email logo |
| `POST` | `/api/admin/favicon` | Upload a custom favicon |
| `POST` | `/api/admin/favicon/delete` | Remove the custom favicon |
| `GET` | `/api/admin/update/check` | Check for a server update |
| `POST` | `/api/admin/update/apply` | Download and apply a server update |
| `GET/POST` | `/api/admin/groups` | List or create user groups |
| `POST` | `/api/admin/groups/create` | Create a group |
| `POST` | `/api/admin/groups/update` | Update a group |
| `DELETE` | `/api/admin/groups/delete/{id}` | Delete a group |
| `GET` | `/api/admin/groups/{id}/codes` | List access codes for a group |
| `POST` | `/api/admin/groups/{id}/codes/generate` | Generate a group access code |
| `DELETE` | `/api/admin/groups/{id}/codes/{codeId}` | Delete a group access code |
| `POST` | `/api/admin/groups/assign-admin` | Assign a group admin |
| `POST` | `/api/admin/groups/remove-admin` | Remove a group admin |
| `GET` | `/api/admin/groups/admins` | List group admins |
| `POST` | `/api/admin/invitations` | Send an invitation email |
| `POST` | `/api/admin/users/transfer` | Transfer a user between groups |
| Various | `/api/admin/radioreference/*` | RadioReference.com data import tools |
| Various | `/api/admin/hallucinations/*` | AI hallucination detection review |
