# Notifications

filex tells you when something needs attention — a replica fell behind, a
storage is nearly full, a queue is stuck, someone dropped files into a shared
folder. Every such event fans out to **two channels at once** from a single
call: a persistent **in-app bell** and one **outbound webhook** POST.

The whole subsystem is optional and safe to leave on — with no webhook
configured it simply records to the bell and skips the outbound call.

- [How it works](#how-it-works)
- [Configuration](#configuration)
- [The webhook](#the-webhook) — [payload](#payload) · [headers](#headers) · [delivery--retry](#delivery--retry)
- [Event types & severities](#event-types--severities)
- [In-app bell (endpoints)](#in-app-bell-endpoints)
- [Admin endpoints](#admin-endpoints)
- [Per-user settings](#per-user-settings)
- [Failure modes & troubleshooting](#failure-modes--troubleshooting)
- [See also](#see-also)

---

## How it works

A subsystem inside filex hands an **event** to one `Service.Send` call, and
that call fans out to two independent channels:

```
                          ┌─► in-app bell   (row in notifications table — survives restart)
  subsystem ── Send() ──► │
                          └─► webhook POST  (one request, async, retried)
```

- **In-app bell** — the event is written to the `notifications` table
  **synchronously**, so it is durable the moment `Send` returns and **survives a
  server restart**. Users read it back through the `/api/notifications/…`
  endpoints (bell icon, history, unread badge, mark-read).
- **Webhook** — a single `POST` to `FILEX_WEBHOOK_URL`, dispatched in a
  **background goroutine** so the originating request never blocks on it. The
  body is a generic JSON document (see [The webhook](#the-webhook)).

The two channels are independent: a webhook failure never affects the bell row,
and no webhook URL simply means the outbound call is skipped while the bell keeps
recording. Webhook errors are recorded **against the notification row**, never
bubbled up to break the action that triggered the event.

> **Master switch.** `FILEX_NOTIFY_ENABLED` (default **true**) toggles the whole
> subsystem. When **false**, `Service.Send` is a no-op and every
> `/api/notifications/…` endpoint returns **503 `{"error":"notifications
> offline"}`**. Leave it on unless you have a reason not to.

---

## Configuration

Set via environment variables (or the equivalent `notify.*` YAML keys). All are
optional — the defaults give you a working in-app bell with no outbound webhook.

| Env var | YAML | Default | Meaning |
|---|---|---|---|
| `FILEX_NOTIFY_ENABLED` | `notify.enabled` | `true` | Master switch. `1`/`true` enables; any other value disables (503 + no-op). |
| `FILEX_WEBHOOK_URL` | `notify.webhook_url` | `""` | Where each event is POSTed. **Empty = webhook skipped** (the bell still records). |
| `FILEX_WEBHOOK_TOKEN` | `notify.webhook_token` | `""` | Optional secret. Sent as `Authorization: Bearer <token>` on every webhook POST. |

```bash
# In-app bell only (no outbound webhook) — this is the default.
FILEX_NOTIFY_ENABLED=true

# Add a webhook (e.g. an incoming-webhook relay or your own receiver).
FILEX_WEBHOOK_URL=https://hooks.example.com/services/T000/B000/xxxx
FILEX_WEBHOOK_TOKEN=s3cr3t-shared-token
```

The webhook URL and token read from the environment at boot, but an admin can
**change them at runtime** through the admin API without a restart — see
[Admin endpoints](#admin-endpoints).

---

## The webhook

When a webhook URL is set, every event produces exactly one outbound `POST`.

### Payload

The body is the JSON encoding of the event — a small, stable, provider-neutral
document:

```json
{
  "event": "quota_near_full",
  "severity": "warning",
  "title": "Storage almost full",
  "body": "team-bucket is at 92% of its 100 GB quota.",
  "meta": { "storage": "team-bucket", "used_pct": 92 },
  "ts": "2026-07-04T09:15:00Z"
}
```

| Field | Type | Notes |
|---|---|---|
| `event` | string | Event type id (see [Event types](#event-types--severities)). |
| `severity` | string | `info` · `warning` · `error` · `critical`. |
| `title` | string | Short headline. Defaults to the event id if the sender left it empty. |
| `body` | string | Human-readable detail. |
| `meta` | object | Optional, event-specific key/values. **Omitted** when empty. |
| `ts` | string (RFC 3339) | Event timestamp (UTC). |

> The per-user routing field (`UserID`) is **internal only** — it scopes the
> in-app bell row and is **never** included in the webhook payload.

The payload is deliberately generic JSON, so it works as-is with a self-hosted
receiver, or behind a relay that adapts it for Slack / Discord / Microsoft
Teams / PagerDuty / etc.

### Headers

Every webhook request carries:

| Header | Value |
|---|---|
| `Content-Type` | `application/json` |
| `User-Agent` | `filex-webhook/1.0` |
| `Authorization` | `Bearer <token>` — **only when** `FILEX_WEBHOOK_TOKEN` is set |

### Delivery & retry

Delivery is **asynchronous** — the POST runs in a background goroutine, so the
user action that produced the event returns immediately.

- **Attempts:** an initial attempt plus **3 retries** — up to **4** total.
- **Backoff between attempts:** `1s → 3s → 9s`.
- **Per-attempt timeout:** 10 s.
- **Success:** any **2xx** response marks the row `sent` and stops retrying.
- **Retry:** a transport error or any non-2xx (e.g. `HTTP 500`) triggers the
  next attempt; the last error is recorded.
- **Exhausted:** after the final attempt the row is marked `failed` with the
  last error message — investigate the receiver.
- **No URL configured:** the row is marked `skipped` (the in-app bell row still
  exists). A malformed URL fails immediately without retrying.

Each notification row tracks this lifecycle in `webhook_status`
(`pending → sent | failed | skipped`) and `webhook_error`, both visible in the
[admin view](#admin-endpoints).

---

## Event types & severities

**Canonical events** filex emits itself:

| Event | Typical severity | When |
|---|---|---|
| `replica_fail` | error | A replica write/op failed. |
| `replica_fail_spike` | critical | Replica failures crossed a rate threshold. |
| `replica_reconcile_done` | info | A reconcile pass finished. |
| `replica_status_report` | info | Periodic replica health summary. |
| `primary_read_fail` | error | A read from the primary backend failed. |
| `quota_near_full` | warning | A storage is approaching its quota. |
| `quota_full` | critical | A storage hit its quota. |
| `queue_stuck` | warning | The op queue stopped making progress. |
| `auth_fail_spike` | warning | A burst of failed logins. |
| `disk_full` | critical | The host disk is out of space. |

Subsystems may also emit **non-canonical** event ids — e.g. `file_dropped`
(someone uploaded into a shared drop folder) and `admin_test` (the manual test
button). The webhook echoes **whatever event id is given**; receivers should
treat the list as open-ended and match on the strings they care about.

**Severities:** `info` · `warning` · `error` · `critical`. The store accepts any
string, but the bell UI only colour-codes these four — stick to them.

---

## In-app bell (endpoints)

Authenticated user endpoints, scoped to the **current user** (they see their own
notifications plus any broadcast notifications). All return **503** when the
subsystem is disabled.

| Method & path | Purpose |
|---|---|
| `GET /api/notifications?unread=&limit=&offset=` | Paginated history → `{items, total, limit, offset}`. `unread=true` returns only unread rows. |
| `GET /api/notifications/unread-count` | Bell badge number → `{count}`. |
| `POST /api/notifications/{id}/read` | Mark one notification read → `204`. |
| `POST /api/notifications/read-all` | Mark all of the user's notifications read → `204`. |
| `GET /api/notifications/settings` | Read [per-user settings](#per-user-settings). |
| `PATCH /api/notifications/settings` | Update per-user settings. |

Each item in `items` looks like:

```json
{
  "id": 42,
  "event": "file_dropped",
  "severity": "info",
  "title": "New upload",
  "body": "alice dropped 3 files into \"Inbox\".",
  "meta": { "folder": "Inbox", "count": 3 },
  "webhook_status": "sent",
  "created_at": "2026-07-04T09:15:00Z"
}
```

`read_at` is **absent** until the row is marked read (then it holds the
timestamp); `user_id` is present only on user-scoped rows (absent on
broadcasts); and `webhook_error` appears only when the webhook for that row
failed.

---

## Admin endpoints

Admin-session endpoints under `/api/admin`. These give the **global** view (all
users' notifications plus broadcasts) and manage the webhook at runtime.

| Method & path | Purpose |
|---|---|
| `GET /api/admin/notifications?unread=&limit=&offset=` | Global history across every user + broadcasts. |
| `POST /api/admin/notifications/test` | Emit an `admin_test` event through **both** channels → `{id}`. Use it to verify the webhook is wired. |
| `GET /api/admin/notifications/webhook-config` | Current config → `{url, token_set}`. |
| `PATCH /api/admin/notifications/webhook-config` | Set the webhook URL/token at runtime → `{ok:true}`. |

**Changing the webhook at runtime** — `PATCH …/webhook-config` with
`{"url": "...", "token": "..."}`:

- An **empty `url`** disables webhook delivery **without** taking the in-app bell
  down.
- An **empty `token`** clears the token; a **non-empty** one replaces it.
- The body is the **full new state**. To keep an existing token you must resend
  it verbatim — there is no "keep current" shortcut (the literal `"__keep__"` is
  explicitly rejected with 400).

> **The token is never echoed back.** `GET …/webhook-config` returns only a
> boolean `token_set`, never the secret itself — secrets don't round-trip
> through the admin UI.

---

## Per-user settings

`GET`/`PATCH /api/notifications/settings` stores one preference row per user:

```json
{
  "user_id": 7,
  "in_app_enabled": true,
  "muted_events": ["replica_status_report", "file_dropped"]
}
```

| Field | Type | Meaning |
|---|---|---|
| `in_app_enabled` | bool | Whether this user wants the in-app bell at all. |
| `muted_events` | array of event ids | Event types this user doesn't want to see. |

A user with **no settings row** is treated as the default: `in_app_enabled=true`
with **no** muted events. `PATCH` replaces the whole preference (send the full
`muted_events` list each time; omitting it clears the mutes).

> These are **per-user display preferences** for the bell — they do not change
> what gets recorded, and they have **no effect on the webhook**. The webhook is
> a single global channel governed only by `FILEX_WEBHOOK_URL`.

---

## Failure modes & troubleshooting

### The webhook never fires
Check, in order:
1. **URL set?** With `FILEX_WEBHOOK_URL` empty the outbound call is skipped by
   design — rows show `webhook_status: skipped`. Set the URL (env, or
   `PATCH …/webhook-config`).
2. **Subsystem enabled?** If the `/api/notifications/…` endpoints return **503
   `notifications offline`**, `FILEX_NOTIFY_ENABLED` is off — nothing is sent at
   all. Turn it back on.
3. **Receiver reachable?** Rows marked `failed` carry the last error in
   `webhook_error` (e.g. `HTTP 500`, a connection error, or a DNS failure).
   Confirm the receiver is up and reachable from filex's network, then re-test
   with `POST /api/admin/notifications/test`.

### Webhook returns 401/403 at the receiver
The receiver expects auth filex isn't sending, or a mismatched secret. Set
`FILEX_WEBHOOK_TOKEN` (or update it via `PATCH …/webhook-config`) to the value
your receiver validates — filex sends it as `Authorization: Bearer <token>`.

### Too many notifications
This is a per-user preference, not a global one: have the user add the noisy
event ids to `muted_events` via `PATCH /api/notifications/settings`, or set
`in_app_enabled: false` to silence their bell entirely. To reduce **webhook**
volume instead, filter on `event`/`severity` at your receiver (the payload
carries both) — there is no server-side per-event webhook filter.

### Test button says the subsystem is offline
`POST /api/admin/notifications/test` returning 503 means
`FILEX_NOTIFY_ENABLED` is false. Enable it and restart, then re-test.

### Nothing survives a restart
The in-app bell is durable (a DB row written before `Send` returns) — if the
bell is empty after a restart, the events were never sent, or the subsystem was
disabled when they fired. The webhook, by contrast, is fire-and-forget: an event
that failed all retries is **not** re-queued across a restart (its row is left
`failed`).

---

## See also

- [CONFIGURATION.md](CONFIGURATION.md) — full config/env reference
- [STORAGE.md](STORAGE.md) — storages, sync, and the replica events that feed notifications
- [RBAC.md](RBAC.md) — who can reach the admin endpoints
- [API.md](API.md) — the complete HTTP API
