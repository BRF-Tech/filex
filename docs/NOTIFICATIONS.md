# Notifications

filex tells you when something needs attention — a replica fell behind, a
storage is nearly full, a queue is stuck, someone dropped files into a shared
folder. Every such event fans out to **two channels at once** from a single
call: a persistent **in-app bell** and an **outbound webhook** POST to every
configured destination.

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
                          └─► webhook POST  (one per destination, async, retried)
```

- **In-app bell** — the event is written to the `notifications` table
  **synchronously**, so it is durable the moment `Send` returns and **survives a
  server restart**. Users read it back through the `/api/notifications/…`
  endpoints (bell icon, history, unread badge, mark-read).
- **Webhook** — a `POST` to `FILEX_WEBHOOK_URL` (the legacy global webhook)
  **and** to every enabled webhook target whose event allow-list matches the
  event, each dispatched in its own **background goroutine** so the originating
  request never blocks on any of them. The body is a generic JSON document (see
  [The webhook](#the-webhook)).

The two channels are independent: a webhook failure never affects the bell row,
and having no destination at all simply means the outbound call is skipped while
the bell keeps recording. Webhook errors are recorded **against the notification row**, never
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

Every event produces one outbound `POST` **per destination**. There are two
kinds of destination and they are independent:

- the **legacy global webhook** — `FILEX_WEBHOOK_URL`, one for the whole
  install, receives every event;
- any number of **webhook v2 targets** — rows managed in **Admin → Webhooks**,
  each with its own URL, its own signing secret and its own **per-event
  allow-list**. A target with an empty allow-list receives everything.

The deliveries run in parallel and retry independently; one failing receiver
does not delay or fail the others.

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
  "ts": "2026-07-04T09:15:00Z",
  "at": "2026-07-04T09:15:00Z"
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
| `at` | string (RFC 3339) | The same timestamp under the webhook v2 field name. **Always present** — it is filled from `ts` on every send. |
| `node` | object | The file/folder the event is about: `storage_id`, `path`, `name`, `size` (`size` omitted when zero). Present on the file events. |
| `share` | object | The public link the event is about: `token`, `path`. Present on `share.created`. |
| `actor` | object | Who triggered it, best-effort: `id`, `email`. Omitted on anonymous surfaces such as a public drop. |

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
| `X-Filex-Event` | The event id, so a receiver can route without parsing the body |
| `X-Filex-Delivery` | A UUID minted once per delivery and **reused across that delivery's retries** — deduplicate on it |
| `Authorization` | `Bearer <token>` — **only on the legacy global webhook**, and only when `FILEX_WEBHOOK_TOKEN` is set |
| `X-Filex-Signature` | `sha256=<hex HMAC-SHA256 of the raw body>` — **only when the target has a secret** |

A webhook v2 target is authenticated by its **signature**, never by the bearer
token: verify `X-Filex-Signature` against the raw request body using the secret
you set on the target. The secret is write-only — the admin API returns a
`secret_set` boolean and never the value.

### Delivery & retry

Delivery is **asynchronous** — the POST runs in a background goroutine, so the
user action that produced the event returns immediately.

- **Attempts:** an initial attempt plus **3 retries** — up to **4** total.
- **Backoff between attempts:** `1s → 3s → 9s`.
- **Per-attempt timeout:** 10 s.
- **Success:** a **2xx** stops that destination's retry chain. The notification
  row is marked `sent` only when **every** destination succeeded; otherwise
  `failed`, with the per-destination errors joined (each prefixed with the
  target's name).
- **Retry:** a transport error or any non-2xx (e.g. `HTTP 500`) triggers the
  next attempt; the last error is recorded.
- **Exhausted:** after the final attempt the row is marked `failed` with the
  last error message — investigate the receiver.
- **No destination:** when there is neither a `FILEX_WEBHOOK_URL` nor any
  enabled target matching the event, the row is marked `skipped` (the in-app
  bell row still exists). A malformed URL fails immediately without retrying.

Each notification row tracks this lifecycle in `webhook_status`
(`pending → sent | failed | skipped`) and `webhook_error`, both visible in the
[admin view](#admin-endpoints). That is the **aggregate** across destinations;
each target additionally persists its own last delivery — final HTTP status
(`0` when there was no response), last error and timestamp — which is what
**Admin → Webhooks** shows per row.

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
| `update_available` | info | A newer release was published. Fires **once** per release — the announcement is persisted, so a restart loop cannot turn it into a stream. |
| `update_applied` | info | A self-upgrade replaced the binary. |

**File and share events** (webhook v2) — the subscribable catalogue, every one
of them tickable on a target in **Admin → Webhooks**:

| Event | When |
|---|---|
| `file.uploaded` | A write **created** a file that was not there before. |
| `file.updated` | A write **replaced the bytes of a file that already existed** — an editor save, a re-upload over the same name, a WebDAV `PUT`/S3 `PutObject` over an existing key. |
| `file.upload_failed` | Bytes filex had already acknowledged could not be written to the storage driver. |
| `file.deleted` | Permanent removal (trash purge, or a hard delete on a driver without move support). |
| `file.trashed` | Soft delete — the file was moved into `.filex-trash/` and is restorable. |
| `file.moved` | A file was moved or renamed. `meta.from` / `meta.to` carry both paths. |
| `file.infected` | The async antivirus scan flagged a file; `meta.signature` names it. Quarantine into the trash is best-effort: `meta.quarantined` says whether it worked and `meta.trash_path` appears only when it did. |
| `share.created` | A public share link was created. |
| `drop.received` | A file arrived through a public "request files" link. |
| `comment.added` | Somebody commented on a file or folder. `meta` carries `comment_id` and the first 200 characters of the body. |
| `e2e.escrow_used` | An encrypted folder was opened with the **recovery (escrow) key** instead of its passphrase. `meta` carries `escrow_kid`, `storage`, `folder` and, when the caller was signed in, `actor_email`. |

The six **write** events (`file.uploaded`, `file.updated`, `file.upload_failed`,
`file.deleted`, `file.moved`, `file.trashed`) come from one shared post-write
gate, so every one of them carries `meta.origin` — which surface wrote it:
`manager`, `ai`, `sharex`, `dav`, `ops`, `s3`, `sftp`, `ftp`, `nfs`,
`onlyoffice`. The other events are emitted by their own subsystems and carry
their own `meta` instead, as listed above.

⚠ `onlyoffice` is its own origin rather than `manager`, and the distinction is
load-bearing for a subscriber: the bytes are assembled and posted by the
document server, not by the browser that opened the file, so the event arrives
**after** the last editor closed the document rather than while somebody is
still working in it. A pipeline that reacts to an office document wants that
one and not every intermediate upload.

> ⚠ **`file.updated` narrows `file.uploaded`.** Before it existed, both a
> created file and an edited one arrived as `file.uploaded`, and nothing could
> tell them apart. A subscriber that watched `file.uploaded` to see edits will
> stop hearing them and has to subscribe to `file.updated` as well; a
> subscriber that only ever wanted new files needs no change and gets a quieter
> feed. A target with an **empty** event list still receives everything.

Subsystems may also emit **non-canonical** event ids — `admin_test` (the global
test button, `POST /api/admin/notifications/test`) and `webhook_test` (the
per-target **Test** button, which fires **one attempt with no retries**). The
webhook echoes **whatever event id is given**;
receivers should treat the list as open-ended and match on the strings they
care about.

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
  "event": "drop.received",
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
| `GET /api/admin/webhooks` | List the webhook v2 targets (secrets masked to a `secret_set` flag) plus each one's last delivery. |
| `POST /api/admin/webhooks` | Create a target: `name`, `url`, optional `secret`, optional `events` allow-list, `enabled`. |
| `PATCH /api/admin/webhooks/{id}` | Update one target. |
| `DELETE /api/admin/webhooks/{id}` | Remove one target. |
| `POST /api/admin/webhooks/{id}/test` | Fire a synthetic `webhook_test` delivery at that one target and return the outcome synchronously. |

The two families are separate on purpose: `…/notifications/webhook-config`
governs the single legacy global webhook, `…/webhooks` governs the v2 targets.
Both are reachable from the admin UI — **Notifications** for the global webhook
and its history, **Webhooks** for the targets.

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
  "muted_events": ["replica_status_report", "drop.received"]
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
> what gets recorded, and they have **no effect on the webhook**. Webhook
> delivery is global: `FILEX_WEBHOOK_URL` plus the targets in
> **Admin → Webhooks**.
>
> ⚠ Both fields are stored and returned faithfully, but **nothing applies them
> yet**: the history endpoints filter on the user and the read flag only, and
> `Send` does not consult the settings row. Treat them as a preference the bell
> UI will honour, not as a filter that is in force today.

---

## Failure modes & troubleshooting

### The webhook never fires
Check, in order:
1. **Any destination at all?** A row shows `webhook_status: skipped` only when
   `FILEX_WEBHOOK_URL` is empty **and** no enabled target matched the event —
   so check the target's `enabled` flag and its event allow-list too, not just
   the env var. Set the URL (env, or `PATCH …/webhook-config`) or add a target
   in **Admin → Webhooks**.
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
volume instead, give the target a **per-event allow-list**: tick only the events
you want in **Admin → Webhooks** (`events` on `POST`/`PATCH /api/admin/webhooks`),
and filex stops sending the rest to that target. An empty list means "every
event". The legacy global webhook has no filter of its own — for that one,
filter on `event`/`severity` at your receiver (the payload carries both).

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
