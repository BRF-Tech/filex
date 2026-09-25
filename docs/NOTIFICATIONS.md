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
- [Click target](#click-target) — [the field](#the-field) · [which events carry one](#which-events-carry-one) · [where a click goes](#where-a-click-goes)
- [In-app bell (endpoints)](#in-app-bell-endpoints)
- [Reaching someone who is not looking at the bell](#reaching-someone-who-is-not-looking-at-the-bell)
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
the bell keeps recording.

The bell row is then **read** by three surfaces — the bell itself, a browser
notification while a tab is open, and the desktop app's native OS notification
— all of which take a person to the event's
[click target](#click-target). They are readers of the one feed, not channels
of their own: nothing extra is sent and nothing extra is polled (see
[Reaching someone who is not looking at the bell](#reaching-someone-who-is-not-looking-at-the-bell)). Webhook errors are recorded **against the notification row**, never
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
  "at": "2026-07-04T09:15:00Z",
  "target": { "kind": "none" }
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
| `target` | object | **Where a click on this notification goes** — `kind` (`file`/`dir`/`share`/`none`) plus `storage`, `path`, `id`. **Always present**; see [Click target](#click-target). |

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

⚠ Read the **Emitted** column before you build an alert on one of these. Seven
of the twelve operational alert ids below are declared in
`internal/notify/event.go` and **no code emits them** — the id is accepted by a
webhook target's allow-list, the target saves, and the event never arrives. A
subscription that can never fire looks exactly like a subsystem that never has
a problem, which is the worst way to learn your monitoring was never wired.

| Event | Typical severity | Emitted | When |
|---|---|---|---|
| `replica_fail` | error | yes | A replica write/op failed. |
| `replica_fail_spike` | critical | **no** ⚠ | Replica failures crossed a rate threshold. |
| `replica_reconcile_done` | info | yes | A reconcile pass finished. |
| `replica_status_report` | info | yes | Periodic replica health summary. |
| `primary_read_fail` | error | yes | A read from the primary backend failed. |
| `quota_near_full` | warning | **no** ⚠ | A storage is approaching its quota. |
| `quota_full` | critical | **no** ⚠ | A storage hit its quota. |
| `queue_stuck` | warning | **no** ⚠ | The op queue stopped making progress. |
| `auth_fail_spike` | warning | **no** ⚠ | A burst of failed logins. |
| `disk_full` | critical | **no** ⚠ | The host disk is out of space. |
| `update_available` | info | yes | A newer release was published. Fires **once** per release — the announcement is persisted, so a restart loop cannot turn it into a stream. |
| `update_applied` | info | **no** ⚠ | A self-upgrade replaced the binary. |

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
| `archive.created` | Background archive creation completed. The component file write does not also emit `file.uploaded`. |
| `archive.extracted` | Archive extraction completed. One event describes the batch rather than emitting `file.uploaded` for every extracted member; `meta.count` carries the number of files. |
| `share.created` | A public share link was created. |
| `drop.received` | A file arrived through a public "request files" link. |
| `comment.added` | Somebody commented on a file or folder. `meta` carries `comment_id` and the first 200 characters of the body. |
| `e2e.escrow_used` | An encrypted folder was opened with the operator's **escrow key** instead of its owner's passphrase — not the recovery key, which the owner holds. `meta` carries `escrow_kid`, `storage`, `folder` and, when the caller was signed in, `actor_email`. |
| `plugin.notice` | An installed app plugin (see `APP-PLUGINS.md`) sent a message through its `notify_send` host function — a signature request, a finished job. Title/body are the plugin's English wording; `meta` carries `plugin` (the app's install id), `plugin_label_<lang>` (its name as people know it — what a reader prints in front of the message, never the id), `title_<lang>`/`body_<lang>` — one of each per language the app wrote it in (`_en`/`_tr` always, at most 16 more; the reader's own language is used, then its base language, then English), `job` for a queued action, and up to eight small facts the plugin added. The app may address one person instead of the instance feed, and may attach a target: the file plus, optionally, the app screen to open on it (`target.open = {plugin, action|view}`), so a click lands in the signing screen rather than on the notifications page. |

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
test button, `POST /api/admin/notifications/test`) and `webhook_test` (**Test**
in a target's **Actions** menu, which fires **one attempt with no retries**). The
webhook echoes **whatever event id is given**;
receivers should treat the list as open-ended and match on the strings they
care about.

**Severities:** `info` · `warning` · `error` · `critical`. The store accepts any
string, but the bell UI only colour-codes these four — stick to them.

---

## Click target

A notification that tells you a file arrived and then cannot show you the file
is half a notification. Every event therefore carries a **typed target**: what
it is about, in a form a client can act on.

### The field

`target` is present on **every** notification — in the webhook body, in the
bell item, and in the row the admin list returns.

```json
"target": { "kind": "file", "storage": "team-bucket", "path": "Documents/report.pdf" }
```

| Field | Type | Notes |
|---|---|---|
| `kind` | string | `file` · `dir` · `share` · `trash` · `app` · `none`. A **closed set** — clients switch on these six and nothing else; an older client reads an unknown kind as `none`. |
| `storage` | string | The storage **NAME**, not its numeric id. Present on `file`/`dir`. |
| `path` | string | Path **inside that storage**, relative, never carrying a `<storage>://` prefix. The file itself for `file`, the folder for `dir`, the path the item was deleted **from** for `trash`. |
| `id` | string | The share **token**, for `kind: "share"`. |
| `open` | object | **App plugins only** (`plugin.notice`): `{plugin, action?, view?}` — one of that app's own screens to open ON the file; for `kind: "app"`, `{plugin, view, section?}` — one of the app's **home** pages, no file. The server validates the pair before storing it, so a client acts on it without re-checking. See [APP-PLUGINS-API.md](APP-PLUGINS-API.md) → *Frontend needs (v2)* §7. |

Three rules the field is built on, each of which is a bug somebody would
otherwise hit:

- **`none` is an answer, not a gap.** An event about the whole instance — a
  replica failure, an available update — has nothing to open, and says so.
  On the **webhook** the field is always there (`{"kind":"none"}`); on a **bell
  item** it is simply **absent**, because it is not stored for those rows. Treat
  an absent `target` and `{"kind":"none"}` as the same thing.
- **The storage is a NAME because a client cannot turn an id into one.**
  `/api/admin/storages` is admin-only, and the explorer addresses storages by
  name. It is resolved once, centrally, when the event is sent — no emitter
  looks it up and no two emitters can resolve it differently.
- **Half an address is refused.** If the storage cannot be resolved (the row is
  gone, the store errored) the target is downgraded to `none`. A path with no
  storage would otherwise be resolved by the client against whatever storage
  the user happened to have open — a click that lands somewhere plausible and
  wrong.

> **Not the same thing as `node` / `share`.** Those stay what they always were:
> descriptive context for a receiver. `target` is the **address**, and the two
> genuinely differ — `file.trashed` describes the file at its original path and
> has to open the Trash view; `share.created` carries both a node and a
> share while only one of them is the thing to open.

> **Rows written before this field existed** have no target and read as `none`.
> Nothing is backfilled: the target is derived from what the emitter knew at
> the time, and there is no way to recover that afterwards.

### Which events carry one

| Event | Target | Why that one |
|---|---|---|
| `file.uploaded` · `file.updated` | `file` — the file | Opens its folder with it selected. |
| `file.moved` | `file` — the **new** path | "Where is it now" is the only useful answer to a move. |
| `file.trashed` | `trash` — the **original** path | The Trash view, with the item selected: that view lists items by where they came from. ⚠ Never `.filex-trash/<key>` — it used to be, and a click opened the bin's raw folder with nothing in it to restore (fixed 2026-09-21). |
| `file.deleted` | `dir` — the parent folder | A permanent removal leaves no row to select. |
| `file.upload_failed` | `dir` — the parent folder | The bytes never landed; the folder they were headed for is where the user retries. |
| `file.infected` | `trash` — the **original** path when it was quarantined; `file` — the original path when the driver had no move | Where the file actually is. |
| `archive.created` | `file` — the new archive | Opens its folder with the archive selected. |
| `archive.extracted` | `dir` — the extraction destination | Opens the folder containing the extracted members. |
| `drop.received` | `dir` — the drop folder | A drop can carry several files, so there is no single row to select. |
| `comment.added` | `file` **or** `dir` | Read from the node row's type — a comment can hang on a folder. |
| `e2e.escrow_used` | `dir` — the encrypted folder | |
| `share.created` | `share` — the token | The event is "a link now exists"; the link is the thing. |
| `admin_test` · `webhook_test` | `none` | |
| `update_available` · `update_applied` | `none` | Not about a file. |
| `replica_fail` · `replica_fail_spike` · `replica_reconcile_done` · `replica_status_report` · `primary_read_fail` | **`none` — honestly cannot** | These carry a path and nothing else (`internal/replica/`): a bare path does not name a storage, and guessing which storage it belongs to would send a click into another tenant's folder whenever two storages share a folder name. |
| `quota_near_full` · `quota_full` · `queue_stuck` · `auth_fail_spike` · `disk_full` | `none` | Declared but **not emitted** by any code — see the Emitted column above. |

### filex's own directories

filex keeps machinery inside every storage — the bin (`.filex-trash`), version
history (`.versions`), legacy thumbnails (`.thumbs`) and the desktop app's
"open with filex" working copies (`.filex-open`), one list in
`backend/internal/syspath`. Every write, move and delete there goes through the
same post-write gate as a person's own files, so one rule decides what reaches
a person, applied in `notify.Service.Send` (the door every event goes through)
and again when rows are read:

| Event about… | Bell row / webhook |
|---|---|
| anything inside the bin, version history, thumbnails, or a keep marker | **none** — it is bookkeeping |
| a desktop working copy being placed or swept | **none** |
| a desktop working copy being **saved** (`file.updated`) or found **infected** | announced under the **original document's name**, with an empty `node.path`, `target: none` and `meta.open_with: true` — the original lives on the person's own computer, so no storage path names it |
| a person's file whose address is in the bin (`file.trashed`, a quarantine) | the file's own path, `target: trash` |

Rows recorded before this rule existed are filtered on **read**, never
rewritten: a row whose body is a path inside one of these directories is left
out of the list and of the unread count (in SQL, so the badge and the list
agree), and an old `file.trashed` row that targeted the bin comes back
addressed to the Trash view. The bell's copy of a row also drops
`meta.trash_path`; the webhook body keeps it.

### Where a click goes

One resolver, three surfaces. The bell row, the browser notification and the
desktop app's native notification all route through
`web/src/lib/notificationTarget.ts` — the desktop **main process imports that
same file** — so the three cannot disagree about where a click lands.

| `kind` | Web (admin/drive SPA) | Desktop app |
|---|---|---|
| `file` | `/{base}explore?select=<storage>://<path>#<storage>/<folder>` — the folder opens and the row is **selected**; with `open`, `&app=…&appAction=`/`&appView=…` as well, and the app's screen opens on that row | remounts the explorer at the folder and selects the row |
| `dir` | `/{base}explore#<storage>/<folder>` | remounts the explorer at the folder |
| `trash` | `/{base}explore?select=<storage>://<path>#.trash` — the Trash view, the item selected | remounts the explorer on the Trash view and selects the item |
| `app` | `/{base}app/<plugin>/<view>?section=<section>` — the app's home page, in the same tab | brings the app window to the front (it has no app home pages) |
| `share` | the public `/s/<token>` page | opened in the **system browser** — a public page is not something to load into a window holding a bearer token |
| `none` | **nowhere — the row is not clickable** | brings the app window to the front |

⚠⚠ `none` used to go to the notifications page. That page is the one the
reader was most likely already looking at, and it is admin-gated — so for
everybody else the same click was a guard bounce that threw away the folder
they were standing in, and the row was STILL not about anything. A row that
cannot go anywhere now takes no cursor, no hover and no click: reading it is
the whole interaction (`isNotificationClickable`). Which is also why an event
that CAN carry an address should carry one.

⚠ A `file` target resolves to its **folder plus a selection**, never to the file
as a destination of its own. Opening "the file" would mean choosing between
preview, edit and download — three answers for three file types — where showing
it in its folder is right for every type, and is what "reveal" means in every
file manager.

⚠ The two path shapes are **not** interchangeable and both are load-bearing:
the explorer's address bar carries `#<storage>/<folder>`, while its rows carry
`data-fe-path="<storage>://<path>"`. Building one from the other by hand is how
a hash ends up naming a folder called `qldemo:`.

---

## Reaching someone who is not looking at the bell

The bell only notifies somebody who is looking at it. Two channels carry the
same event further, and they are mutually exclusive on any one machine.

### Browser notifications

While a filex tab is open and the browser has granted permission, each new bell
row also raises a `Notification`. Clicking it focuses the tab and goes to the
event's [target](#click-target).

- **Permission is asked from a click** — the button in the **Notifications**
  pane of the user-settings dialog (the account menu, on every front door), or
  the same pane of the admin panel's **Notifications → Your notifications** —
  and never on page load. An origin that asks without a
  gesture is answered by Chrome with a muted chip instead of a prompt — asking
  at the wrong moment can cost you the permission permanently.
- **A per-user switch** beside it turns it off. It is stored in
  `localStorage` (`filex.notify.browser.<user id>`), **not** in the per-user
  settings row, because the permission it acts on is granted per browser
  profile and per device: a server-side flag would travel to a machine where
  the permission was never granted, and say "on" while nothing ever appeared.
- **It says WHO is notifying, and shows a logo the browser can decode.** The
  title is the instance's name — the operator's own when they set one on the
  Branding page — and the event's sentence is the body, because a
  notification's second line is the app's identity and the browser fills it in
  only for an *installed* app; everywhere else it prints the bare origin. The
  `icon` and the `badge` are **PNG** (`/admin/icons/icon-192.png`,
  `badge-96.png`, rasterised from `icons/icon.svg` by
  `scripts/make-icon-pngs.mjs`).
  ⚠⚠ Not the SVG: Chromium decodes a notification's icon through its image
  decoders and SVG is not among them, so an SVG there is not a small logo or a
  blurry one — it is **no logo**, and the toast falls back to a generic bell.
  Firefox draws it, which is exactly why that lasted. The badge is a
  **monochrome alpha mask** on Android, which is why it is white on
  transparent rather than the full-bleed square.
- **It degrades silently.** No API, an insecure origin, permission denied —
  all no-ops, never an error. A constructor that throws (Android Chrome, where
  only a service worker may notify) falls back to
  `registration.showNotification`, whose click is handled by
  `web/public/notify-sw.js` (focus an open tab and send it to the target,
  else open one). ⚠ The in-page path is tried FIRST, because a toast
  constructed by the page keeps its callback — which is what marks the row
  read and navigates inside the running SPA; the worker can only open an
  address.
- One toast per notification id (`tag: filex-notification-<id>`), so a
  re-render cannot produce two. `renotify` rides with the tag, because Android
  replaces a same-tag notification silently otherwise.

### Desktop app

The desktop window is the explorer and has no bell in it, so the app polls the
same endpoint and raises a **native OS notification** instead. A click brings
the window to the front and opens the target; a share opens in the system
browser. **App settings → Notifications** turns it off.

The unread count goes on the app's own icon instead of on a bell: the dock /
taskbar badge where the platform draws one (macOS and Linux desktops that
support it; Windows has no such badge), and the tray icon's tooltip
(`filex — 12`). See rule 3 below for why the badge and the tooltip round
differently past 99.

It never double-notifies: the browser channel refuses to fire inside the
Electron shell, so one event produces one notification on that machine.

### Cost

Both channels ride the bell's existing **15 s** unread-count poll — the one the
bell has always run. The head of the unread list is fetched **only when that
count goes up**, so a quiet instance costs exactly what it cost before. On the
web the loop lives at the root of the SPA rather than in the bell component, so
the screens that draw no bell (an app's full page, the standalone editor,
**My shares**) are covered by the same loop rather than by a second one.

⚠ **A baseline is taken before anything is announced.** A reload, or an app
start, must not replay every unread row the user already had as toasts.

⚠ A notification carries **a name, a count and a target** — never file content
and never a credential. The title and body are the same strings the bell shows.

---

## The bell, and who can reach it (product rule)

⚠⚠ Three rules, written down because the product broke all three at once and
each break is invisible from the code: the panel looked fine to the
administrator who built it.

**1. A notification is clickable exactly when it has somewhere to go.**
Clickability is not a style, it is a fact about the row: a row whose `target`
resolves to a place opens that place, and a row with `{"kind":"none"}` (or no
target at all) must not look clickable — no pointer cursor, no hover lift, no
link role. Every event that CAN name a place must carry one: a file that
arrived opens the file, a share that was created opens the share, an app
plugin's notice opens that app's screen on that file. An event that genuinely
has nowhere to go says `none` and says it honestly; an event that quietly
forgets its target is a bug, not a `none`.

**2. Every person reaches ALL of their notifications without leaving the
explorer.** The bell shows the last few; "see all" must open the full list
**inside the explorer** — its own screen or a panel over it — and it must work
for somebody who is not an administrator. (Today: **View all** at the foot of
the bell opens it as a panel over the page, read from the user-scoped
endpoints below.) ⚠ The admin notification page is
for MANAGING the subsystem (the webhook, everybody's rows, the settings); it
is not where an ordinary person reads their own notifications, and pointing
"see all" at it hides a person's own mail behind a permission they do not
have. An administrator can still walk to the admin page by themselves.

**3. The count lives ON the icon.** An unread count is drawn as a badge on the
bell icon itself — in the explorer, in the desktop app, and on mobile when it
comes. It reads the exact number up to 99 and **`99+`** above that, never a
raw 3-digit number and never a bare dot. It clears as rows are read, and it is
the same component on every surface: a counter written twice is a counter that
disagrees with itself. (One deliberate exception: the desktop app's dock /
taskbar badge is drawn by the operating system, which has room for the real
number, so it is handed the exact count; the same module, `unreadBadge.ts`,
clamps the tray tooltip to `99+`.)

## In-app bell (endpoints)

Authenticated user endpoints, scoped to the **current user**. All return **503**
when the subsystem is disabled.

What a bell holds:

- **Rows addressed to the user.** Routine file activity (`file.uploaded`,
  `file.updated`, `file.moved`, `file.trashed`, `file.deleted`) is always
  addressed to the person who did it, including when the queue finished the
  work (a queued copy/move/delete, the commit of a staged upload): the queue
  row names who asked, and the event is theirs.
- **Broadcasts** (rows addressed to nobody: antivirus alerts, replica
  reports, update notices, a drop or escrow notice with no owner on record)
  go to **admins**. A member receives four kinds: an antivirus hit
  (`file.infected`), an upload that never landed (`file.upload_failed`), the
  admin page's test (`admin_test`) and an app's instance-wide notice
  (`plugin.notice`). Whenever one of them names a file, the member gets it only
  when that file is one they could see in the explorer: the same grants and
  the same "ancestor folders of a grant" rule the listing uses. A row that
  names a file by name but gives no path — an "open with filex" working copy,
  whose path names nothing anybody can open — reaches no member (the
  antivirus scanner addresses its own to the copy's owner). Everything else is
  for admins: an operator alarm names no storage, a drop or share notice
  carries the link's bearer token.
- In **multi-tenant** mode the tenant boundary applies first: nobody receives
  a row about another tenant's storage, a tenant admin gets the broadcasts
  that name a file in their tenant, and a row that names no storage reaches
  only the supertenant's readers (its admins, and — for the admin test and an
  app's notice that names nothing — its members).
- A broadcast of **routine** file activity (a surface that could not say who
  asked — every queued operation before this rule existed) is in no bell at
  all; it stays in the table and in the admin list below.
- A **folder-confined token** (`root:<storage>://<folder>`, narrowed by an
  `X-Filex-Root` header — the files routes' own confinement) reads only the
  notices about files inside its folder, its owner's own notices included; a
  notice naming a file outside it, or naming paths it cannot place (a replica
  alarm's bare paths), is invisible to that token, and `read` / `read-all`
  through it touch nothing else. A notice that names no file stays readable.

Which broadcasts a bell takes is decided in SQL, so rows a reader may not see
never fill their page. The badge (`unread-count`) counts exactly what the list
would show, and so does the list's `total`: the reader's own rows are counted
in SQL and only the broadcasts their bell admits are walked, so a page is cut
where the reader sees it and every row they may see is on some page.

**Read state is per reader** (migration 00056). A row addressed to a user is
read when that user marks it. A broadcast is read separately for each reader,
and marking it changes the caller's bell and nobody else's:

- `read-all` reads every notification up to that moment, for the caller: one
  write, however many there are. Broadcasts the caller's bell does not show are
  read for them too, which changes nothing anybody sees. What arrives afterwards
  is unread again.
- `read` on a single broadcast marks it for the caller when their bell shows it
  (an admin nobody confines — single-tenant, or the supertenant — may mark any
  broadcast: they read them all in the admin history). On any other id, or an id
  that does not exist, it answers `204` and changes nothing, so the endpoint
  does not tell anyone which ids exist.
- A broadcast marked read before 00056, when read state was one shared column,
  stays read for everyone; nothing is backfilled.

⚠ **Operator alarms reach administrators only.** `update_available`,
`update_applied` and the replica, quota, queue, auth and disk alarms are
recorded as broadcasts, but a non-administrator's list and unread count leave
them out — they are about a server that person cannot touch (a plain user's
bell used to read "filex v0.42.2 is available — this server runs 0.1.0-dev").
Other broadcasts — the admin test and an app's instance-wide notice — still
reach everybody. Nothing is dropped: the admin list and the webhook carry them
as before.

| Method & path | Purpose |
|---|---|
| `GET /api/notifications?unread=&limit=&offset=` | Paginated history → `{items, total, limit, offset}`. `unread=true` returns only unread rows. |
| `GET /api/notifications/unread-count` | Bell badge number → `{count}`. |
| `POST /api/notifications/{id}/read` | Mark one notification read for the caller → `204` (also for an id the caller's bell does not show; nothing changes then). |
| `POST /api/notifications/read-all` | Mark everything up to now read, for the caller → `204`. |
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
  "target": { "kind": "dir", "storage": "team-bucket", "path": "Inbox" },
  "webhook_status": "sent",
  "created_at": "2026-07-04T09:15:00Z"
}
```

`read_at` is **absent** until the row is marked read — for a broadcast, until
the CALLER marked it — and then holds the timestamp; `user_id` is present only on user-scoped rows (absent on
broadcasts); `webhook_error` appears only when the webhook for that row
failed; and `target` is **absent** when there is nothing to open — see
[Click target](#click-target), where an absent target and `{"kind":"none"}`
mean the same thing.

---

## Admin endpoints

Admin-session endpoints under `/api/admin`. These give the **global** view (all
users' notifications plus broadcasts) and manage the webhook at runtime.

⚠ In multi-tenant mode the global history, the test event and the legacy
webhook config are **supertenant-only** (`403 supertenant_only` for a tenant
admin): all three span every tenant — a history row names its tenant only
inside `meta`, and the test event goes to the instance's webhook receivers. A
tenant admin reads the tenant's own events in their bell, which is scoped.

| Method & path | Purpose |
|---|---|
| `GET /api/admin/notifications?unread=&limit=&offset=` | Global history across every user + broadcasts. A user-scoped row also carries `user_name`, the person's display name. A broadcast carries `audience` — who it reaches, by the rule above: `everyone`, `viewers` (admins and the members who can see the file it names), `admins`, or `nobody` (routine file activity recorded without the person who did it) — and `admins_only` (`audience` = `admins`). A broadcast's `read_at` (and `unread=`) is the caller's own; a user's row carries that user's. |
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
Both are set up on ONE screen in the admin UI, **Webhooks** — the default
(global) webhook above the targets — so every place an event is delivered is
visible in one place; **Notifications** keeps the history and points there.

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
| `in_app_enabled` | bool | `false` empties this user's bell: the list returns nothing and the unread badge is 0. |
| `muted_events` | array of event ids | Event types dropped from this user's list **and** unread count. |

A user with **no settings row** is treated as the default: `in_app_enabled=true`
with **no** muted events. `PATCH` replaces the whole preference (send the full
`muted_events` list each time; omitting it clears the mutes).

> These are **per-user display preferences** for the bell, and they gate the
> **read**, not the write:
>
> - **Nothing stops being recorded.** `Send` still inserts every event, so a
>   muted one stays in the audit and reappears in full the moment the mute is
>   lifted. Muting changes what a user sees, not what filex keeps.
> - **The webhook is untouched.** Delivery is global — `FILEX_WEBHOOK_URL` plus
>   the targets in **Admin → Webhooks** — and no per-user preference reaches
>   it. To cut webhook volume, use a target's per-event allow-list instead.
> - **The admin/global view is never filtered.** A listing with no user scope
>   returns everything the system recorded; one admin's personal mute list
>   cannot hide rows from the audit.
>
> ⚠ A settings row that cannot be read — none written yet, a DB error, a
> hand-corrupted `muted_events` — **fails open**: the user sees everything. A
> display preference must not be able to hide an antivirus hit behind a
> transient error.
>
> ⚠ `in_app_enabled` has a screen now, and **everybody can reach it**: the
> **Notifications** pane of the user-settings dialog, opened from the account
> menu on every front door — the admin chrome, Home and the standalone
> explorer, which between them are every screen a non-admin can be on. The
> admin panel's own **Notifications → Your notifications** section is the same
> two switches for an operator who is already there. `muted_events` has its
> switches in the dialog's **What to tell me about** list, one per event — and
> only for events that can happen on this instance: no virus switch while
> scanning is off, no escrow switch without an escrow key, no app switch while
> apps are off. Both screens resend the user's existing list verbatim so
> opening one cannot clear their mutes. The filtering itself is in force
> regardless of how the row got written.
>
> Why that matters more than it sounds: this endpoint is open to every account,
> and until the dialog existed its only screen sat behind the admin gate — so
> the people who receive notifications were the exact people who could not turn
> them off.

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
