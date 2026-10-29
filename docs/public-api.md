# Public API (user-scoped)

ThinLine Radio exposes a **user-scoped** REST API under `/api/public/v1/*`. It is separate from uploader **API keys** used for call ingest (`/api/call-upload`, etc.).

Access is granted by a server owner in **Admin → Users → Public API**. Users with access mint their own keys from the console **API** button (next to Admin when enabled).

## Auth

Send the key on every request:

```http
Authorization: Bearer tlr_…
```

or:

```http
X-API-Key: tlr_…
```

Keys are stored as SHA-256 hashes. The plaintext secret is shown **once** at creation.

Data responses are filtered with the same system/talkgroup access as the user’s account (`userHasAccess`).

## Capabilities

| Capability | Endpoints |
|------------|-----------|
| **Health** | `GET /health`, `/health/live`, `/health/systems`, `/health/systems/{systemRef}` |
| **Transcripts** | `GET /transcripts`, `/transcripts/{callId}` |
| **Alert webhooks** | Outbound destinations + `CRUD /webhooks` |

Key management (`/keys`) requires public API enabled (any capability). Session PIN (`Authorization: Bearer <pin>`) or an owned `tlr_…` key may be used to mint/revoke keys.

## Health (Uptime Kuma)

### Monitor setup

1. Create a public API key with the **Health** capability.
2. In Uptime Kuma, add an **HTTP(s)** monitor:
   - URL: `https://your-host/api/public/v1/health`
   - Method: `GET`
   - Headers: `Authorization: Bearer tlr_your_key_here`
3. Expect **200** when ready, **503** when degraded (JSON body includes `status` and `reasons`).

Liveness-only probe:

```bash
curl -fsS -H "Authorization: Bearer tlr_…" \
  https://your-host/api/public/v1/health/live
```

Systems last-call / volume summary (scoped to systems you can hear):

```bash
curl -fsS -H "Authorization: Bearer tlr_…" \
  https://your-host/api/public/v1/health/systems
```

### No-auth alternative

The existing open probe `GET /api/ready` remains available for hosts that prefer uptime checks without issuing keys.

## Transcripts

```bash
curl -fsS -H "Authorization: Bearer tlr_…" \
  "https://your-host/api/public/v1/transcripts?limit=50"

curl -fsS -H "Authorization: Bearer tlr_…" \
  https://your-host/api/public/v1/transcripts/12345
```

Optional query filters (when listing): `systemId`, `talkgroupId`, `limit` (max 200).

## Alert webhooks (outbound)

When **Alert webhooks** is granted, configure destinations in the console API dialog (`CRUD /api/public/v1/webhooks`). On alert push (same path as FCM), ThinLine posts a normalized payload asynchronously.

### Payload shape

```json
{
  "source": "thinline-radio",
  "severity": "info|warning|critical",
  "message": "SYSTEM / TG — Station 1",
  "timestamp": 1710000000000,
  "metadata": {
    "host": "https://scanner.example.com",
    "systemRef": 1,
    "systemLabel": "...",
    "talkgroupRef": 2,
    "talkgroupLabel": "...",
    "alertType": "tone",
    "callId": 123
  },
  "transcription": "..."
}
```

### Destination types

| Type | Notes |
|------|--------|
| `slack` | Incoming webhook URL; Slack message JSON |
| `discord` | Discord webhook URL; `content` field |
| `email` | Uses server email settings; set `email` on the destination |
| `pagerduty` | Events API v2; put routing key in `secret` |
| `custom` | Raw JSON POST; optional `secret` sent as `X-Webhook-Secret` |

### Slack example

1. Create an Incoming Webhook in Slack.
2. Add destination: `destinationType=slack`, `url=<webhook url>`.
3. Trigger a tone/keyword/transcript alert on a talkgroup you monitor.

### Discord example

Same as Slack with `destinationType=discord` and a Discord channel webhook URL.

## Rate limits

About **120 requests per minute per key**. Excess returns `429`.

## Admin tracking

Server owners can list and disable keys under each user’s Public API section (prefix, last used, request count).
