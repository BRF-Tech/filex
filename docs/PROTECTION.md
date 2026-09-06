# Protection: antivirus, trash retention, version retention & share-link life

The v0.4 "Koru" wave groups filex's data-protection knobs behind one admin
surface: optional **ClamAV scanning of every write**, the existing **trash retention**
window, a **version retention** count and — since v0.25 — the **maximum life of
a share link**. Trash and versioning themselves are documented in
[TRASH-VERSIONING.md](TRASH-VERSIONING.md); share links in [SHARING.md](SHARING.md).

- [Protection settings API](#protection-settings-api)
- [Share-link life (`share.max_ttl_days`)](#share-link-life-sharemax_ttl_days)
- [Antivirus (ClamAV)](#antivirus-clamav)
- [Version retention (`versions.keep_n`)](#version-retention-versionskeep_n)
- [The `file.infected` event](#the-fileinfected-event)

---

## Protection settings API

Admin-only, session or admin-scoped token:

| Method & path | Body | Notes |
|---|---|---|
| `GET /api/admin/protection` | — | Returns the four retention/share values plus an `antivirus` block (below). `shares_over_max_ttl` is a read-only count (below). |
| `PATCH /api/admin/protection` | `{"trash_retention_days"?: n, "versions_keep_n"?: n, "share_max_ttl_days"?: n, "av_enabled"?: bool, "av_mode"?: "binary"\|"daemon", "av_clamd_addr"?: s, "av_max_scan_mb"?: n, "av_save_scan_window_minutes"?: n}` | Partial update; echoes the fresh GET shape. Validation: retention **1–3650** days, keep_n **0–1000** (`0` = unlimited, retention job off), share TTL **0–3650** days (`0` = no ceiling). Out-of-range → **400**. |

The values live in the `settings` table (`trash.retention_days`,
`versions.keep_n`, `share.max_ttl_days`, `antivirus.*`) — no migration, and the
generic `/api/admin/settings` endpoints see the same rows.

### The `antivirus` block

```json
{
  "enabled": true,          "binary": "clamd",
  "mode": "daemon",         "address": "clamav:3310",
  "reachable": true,        "health": "",
  "version": "ClamAV 1.5.4/28108/Sun Aug 30 06:27:10 2026",
  "restart_pending": false,
  "scan_enabled": true,     "scan_mode": "daemon",
  "clamd_addr": "clamav:3310",
  "modes": ["binary", "daemon"],
  "max_scan_mb": 100,       "max_scan_mb_min": 1,  "max_scan_mb_max": 10240,
  "save_scan_window_minutes": 30,
  "save_scan_window_min": 2, "save_scan_window_max": 60
}
```

It has two halves, and they can legitimately **disagree**:

| Half | Fields | Means |
|---|---|---|
| **Status** — what this process is doing | `enabled`, `binary`, `mode`, `address`, `reachable`, `health`, `version`, `restart_pending` | `enabled` = switched on **and** something is configured to reach. `binary` names what would answer: `clamscan`, `clamdscan`, or `clamd` in daemon mode |
| **Settings** — what an admin last saved | `scan_enabled`, `scan_mode`, `clamd_addr`, `modes`, `max_scan_mb*`, `save_scan_window*` | Writable through `PATCH` |

⚠ `scan_enabled` can be `true` while `enabled` is `false` (switched on, nothing
to reach), and both can be `true` while `reachable` is `false` (clamd is
configured and clamd is down). `reachable` is a **live probe** — a clamd
`PING`/`PONG`, or an existence check on the executable — and `health` carries
the reason when it fails. A configured-but-dead daemon is the case that would
otherwise sit behind a green light while nothing at all was being scanned.

## Share-link life (`share.max_ttl_days`)

The longest life a **new** share link or file request may be given. Default
**7 days**; `0` removes the ceiling; `FILEX_SHARE_MAX_TTL` seeds it once on a
fresh install. A link created with no expiry gets `now + max`, a longer request
is shortened, and the create response reports the stored `expires_at` plus
`expiry_clamped: true` when the server changed the request. The share dialogs
read the ceiling from `GET /api/capabilities` (`share_max_ttl_days`) and only
offer expiries under it. Cached folder archives are swept on the same clock
(`FILEX_SHAREZIP_MAX_AGE`), so a week-old share has neither a working link nor a
ZIP on disk.

**Existing links are never modified.** Changing the ceiling is a rule for links
created from then on. `shares_over_max_ttl` tells the operator how many live
links currently outlive the ceiling (no expiry, or an expiry later than
`now + max`); the Protection page shows the number and the boot log prints it.
Revoking any of them is a manual decision under **Shares** — the intended case
is a hosting operator whose tenants handed customers long-lived links before the
limit existed.

## Antivirus (ClamAV)

Optional. Everything is edited under **Settings → Protection**; nothing has to
be edited in compose after the first boot.

### Two ways to reach ClamAV

filex can reach ClamAV either by **running a binary** or by **talking to a clamd
daemon over the network or a unix socket**. Which one is used is an explicit
setting — filex does not guess, so "daemon mode with a broken address" reads as
broken rather than quietly falling back to some other scanner.

| Mode | What it needs | Use it when |
|---|---|---|
| `binary` | `clamdscan` or `clamscan` on filex's own `$PATH`, and the signature database beside it | ClamAV is installed on the same host as filex |
| `daemon` | A running clamd reachable at `host:port` or a unix socket | **ClamAV is its own container** (`clamav/clamav`, port 3310), or a distro clamd that only listens on a socket |

⚠ Daemon mode is the one you want under Docker, podman and Kubernetes. The
official filex images do **not** contain a scanner — a ClamAV install plus its
signature database is close to a gigabyte — so binary mode there would mean
building your own image. Daemon mode needs no shared filesystem at all: the
bytes are streamed to clamd over the same connection the command goes out on
(clamd's `INSTREAM`), so filex and clamd only need a network route between
them. This is also why the daemon path never writes a temp file, while the
binary path has to (both ClamAV CLIs take a path argument).

**Docker Compose:**

```yaml
services:
  filex:
    image: ghcr.io/brf-tech/filex:latest
    environment:
      FILEX_CLAMAV_ADDR: "clamav:3310"   # seeds daemon mode on first boot
  clamav:
    image: clamav/clamav:latest
    volumes:
      - clamav-db:/var/lib/clamav        # keep signatures across restarts
volumes:
  clamav-db:
```

The repository ships that pair ready to run: `deploy/compose/docker-compose.full.yml`
carries a `clamav` service under the **`clamav` profile**, with the volume for
the signature database already wired. Add `clamav` to `COMPOSE_PROFILES` and
uncomment `FILEX_CLAMAV_ADDR`.

**Same host, over a unix socket** — share the socket directory between the two
and set the address to the socket path:

```yaml
      FILEX_CLAMAV_ADDR: "/var/run/clamav/clamd.ctl"
```

**Install on the host instead** (Debian/Ubuntu, for `binary` mode):

```bash
apt install clamav clamav-daemon   # clamd + clamdscan (fast, daemon-backed)
# or minimal: apt install clamav   # clamscan only (slow cold start per scan)
```

### The settings

All five live in the `settings` table and are edited on the Protection page.

| Setting | Default | Range / form | In force |
|---|---|---|---|
| Scanning on/off (`antivirus.enabled`) | on | switch | ⚠ **next restart** |
| How ClamAV is reached (`antivirus.mode`) | `binary` | `binary` \| `daemon` | ⚠ **next restart** |
| clamd address (`antivirus.clamd_addr`) | — | `host:port`, `tcp://host:port`, or a unix socket path | ⚠ **next restart** |
| Largest file scanned (`antivirus.max_scan_mb`) | 100 MB | 1–10240 MB | immediately |
| Editor save-scan window (`antivirus.save_scan_window_minutes`) | 30 min | 2–60 min | immediately |

⚠⚠ **The first three take effect when the server restarts, in both
directions.** Turning the switch on does not start scanning and turning it off
does not stop it until filex is restarted, because the scan pipeline is wired
once at boot — the queue handler is registered and the upload paths are handed
an enqueue function only when scanning resolved as available. The admin page
says so at the moment you change it and keeps a "restart required" band up
until the restart has actually happened (`restart_pending` in the API). The
other two are read per file, so they apply to the next file scanned.

An address is validated when you **save** it, not when a file is scanned:
`clamav 3310` is refused by the form. Choosing `daemon` with no address on file
is refused as well — each half is legal alone and the pair is a scanner that is
switched on and can reach nothing.

⚠ The scanner **binary** is still not editable in the UI, and for the original
reason: it is a path this server *executes*, so an admin-writable field for it
would turn an admin account into arbitrary command execution. Set
`FILEX_CLAMAV_BIN` in the environment if `$PATH` resolution is not what you
want. A clamd **address** is a different thing — a dial target, never executed
— and is editable.

⚠⚠ Every antivirus environment variable is now a **seed**, consumed on a boot
where no stored value exists yet, and inert afterwards. That includes
`FILEX_CLAMAV`, which used to be an env-only kill switch: an install that had
`FILEX_CLAMAV=0` keeps scanning off across the upgrade (the value is seeded
into the row), and from then on the switch lives on the Protection page and
editing the variable in compose does nothing. See
[CONFIGURATION.md](CONFIGURATION.md#antivirus-clamav).

The whole set, so you can check your own compose against it:

| Variable | Seeds | |
|---|---|---|
| `FILEX_CLAMAV` | `antivirus.enabled` | `0` / `false` = scanning off |
| `FILEX_CLAMAV_MODE` | `antivirus.mode` | `binary` \| `daemon` |
| `FILEX_CLAMAV_ADDR` | `antivirus.clamd_addr` | with no `_MODE` beside it, also seeds `mode=daemon` |
| `FILEX_CLAMAV_MAX` | `antivirus.max_scan_mb` | ⚠ **bytes** here, megabytes in the setting; the conversion rounds **up**, so an upgrade can only leave the ceiling the same or very slightly larger |
| `FILEX_CLAMAV_SAVE_WINDOW_MINUTES` | `antivirus.save_scan_window_minutes` | 2–60 |
| `FILEX_CLAMAV_BIN` | nothing — **stays environment-only** | a path this server executes; see the warning above |

⚠ The seed is applied **per setting, not per family**: a setting whose row does
not exist yet is seeded from its variable even if its siblings already have
rows. Nobody loses configuration by upgrading in two steps.

### How it works

After a successful write, a scan job is enqueued on the persistent queue —
scanning is **fully async** and never blocks or fails the write response. The
worker reads the file back from its storage backend and then, depending on the
mode, either streams it to clamd (`INSTREAM`, nothing touches disk) or spools
it to a temp file and execs ClamAV (`--no-summary`, 60s timeout; `clamdscan`
also gets `--fdpass`). Both produce the same three outcomes:

- **Clean**: no side effects at all.
- **Infected**: the file is **quarantined into trash** — the object is
  renamed under `.filex-trash/` and the DB row soft-deleted exactly like a user
  delete, so it disappears from listings but stays restorable/purgeable through
  the normal trash tooling — and a [`file.infected`](#the-fileinfected-event)
  event fires. The server also logs a `WARN`.
- **Scan error** (a timeout, a daemon that cannot be reached, a reply filex
  does not recognise, or clamd refusing a stream over its own
  `StreamMaxLength`): the op fails and uses the queue's normal retry budget;
  the file stays in place.

⚠⚠ **An unreachable scanner is never a clean file.** Every failure path returns
an error, and the queue turns that into a failed op that retries and shows up
in the ops list. It is not turned into a pass, because a pass looks exactly
like a real scan and the failure would then be invisible in the one place it
matters.

The capability endpoint (`GET /api/capabilities`) advertises the current state
as `"antivirus": true|false` plus `"antivirus_mode": "binary"|"daemon"` (the
flag follows the same pattern as `"ocr"`; the mode is there because two very
different deployments produce the same green light and the person looking at it
has to know which one answered). ⚠ Like `enabled` above, it means *configured*,
not *answering* — reachability costs a network round trip and is probed on the
Protection page, where an admin is waiting for the answer, rather than on every
capabilities fetch.

**Which writes are scanned.** Every surface that writes a file through the
shared post-write gate (`writehook.OnFileWritten`) enqueues a scan: the chunked
and multipart upload paths, the AI/MCP surface, the archive extractor, the
async copy/move worker, and the five protocol endpoints (WebDAV, SFTP, FTPS,
NFS and the S3 gateway) through `protocolsync`. The public **file-drop** is
scanned too, but reaches the scanner directly rather than through the gate —
which is why a drop emits `drop.received` and not `file.uploaded`.

**OnlyOffice save-back** goes through the gate too, as of v0.34.0 — an office
document edited in the browser is scanned like any other write. Which of the
two scan schedules it takes depends on what the Document Server says the save
is: the final revision of a closed editing session is scanned immediately, an
interim force-save takes the debounced window below. See
[ONLYOFFICE.md](ONLYOFFICE.md#what-a-save-does).

Three surfaces do not go through the gate. The editor and the two **restore**
paths are covered separately below, and files the **sync discovers** are the
section immediately after this one.

### Files the sync discovers

A file does not have to be written *through* filex to end up in it. Dropped
into a bucket with `aws s3 cp`, written on a mounted disk by another process,
or simply already there when the storage was pointed at the folder — it reaches
the catalogue through the periodic **sync walk**, which creates the row, feeds
the search index and queues content extraction.

Until this was fixed, that was the end of it: the walk never handed the bytes
to ClamAV. It is the one place a reader most expects a scan, because an
operator who turned scanning on believes the files in filex are scanned.

The walk now enqueues a scan in exactly two cases, and no others:

- a file it **newly catalogues** — including a file catalogued as new because
  an object turned up where a trashed row still sat;
- a file whose **content drifted** — the etag where the backend reports one,
  otherwise its size and modification time.

⚠ *Not* "every file the walk sees". The walk sees the same objects on every
pass, forever; scanning on sight would re-scan the entire storage every sync
interval — 96 times a day on the 15-minute default — for content nothing had
touched.

Drift is the backend's etag where there is one, and **size + modification time
where there is not** — `local`, `sftp`, `smb` and `ftp` always, plus any WebDAV
server that omits the header and any storage plugin that reports no etag. See
[STORAGE.md → Drift detection](STORAGE.md#drift-detection-what-a-replaced-file-looks-like)
for what that catches and what it does not; the one case it misses, a
replacement that preserves both the size and the mtime, is also a file that is
never re-scanned.

Eligibility is the ordinary one, so directories, empty files, files over
`antivirus.max_scan_mb`, and anything under `.filex-trash/` or `.versions/` are
refused exactly as they are on the upload path. An infected file the sync found
is quarantined exactly like an uploaded one: renamed into `.filex-trash/`, the
row soft-deleted and retagged, `file.infected` emitted.

**The first import of an existing storage.** Point filex at a folder that
already holds 20 000 files and every one of them is newly discovered, so one
pass queues 20 000 scans. That backlog is intended — those files genuinely have
never been scanned — but it must not push a person to the back of the queue.
The queue orders `priority DESC, enqueued_at ASC`, so at equal priority those
20 000 rows sit ahead of everything that arrives next. Measured, with
`clamdscan`, 4 workers: an upload's scan enqueued ten seconds into the import
waited **41 s** to be picked up.

So a scan the sync asks for is enqueued one step **below** every other op filex
queues (`Priority = -1`). The same probe then waited **1 ms**: an interactive
scan only ever waits for a worker to finish the one scan it is holding. The
import itself is unaffected — 20 000 files, 465 MiB, drained in **53 s** — and
there is no cap, because a per-pass cap would silently drop the scans it
deferred: a file is "newly catalogued" exactly once, so anything skipped on the
pass that catalogued it would never be enqueued again.

⚠ **`clamdscan`, not `clamscan`.** With the daemon a scan costs ~10 ms and the
import above finishes in under a minute. Without it, `clamscan` re-loads the
~112 MB signature database on **every** invocation: measured over the same
code path, 40 files took **2 m 40 s** on 4 workers — 0.25 scans/s, which puts
that 20 000-file import at roughly **22 hours** with all four workers held the
whole time. The priority rule still holds (the probe was picked up after
**7.3 s**, one in-flight scan, rather than after the backlog), but that is the
floor a 16-second-per-file scanner leaves. Installing `clamav-daemon` is not
cosmetic.

All three queue drivers order by `priority`. Redis does it with a sorted set
scored on `priority DESC, arrival ASC`; measured on a real Redis with the same
20 000-op backlog, an interactive op that had waited **20.0 s** behind 18 000
sweep ops is now served **first, in 1 ms**. (Until v0.34.0 the Redis pending set
was a list consumed positionally, so `Priority` was stored and displayed but
had no effect on the order — on Redis the first import was FIFO and an upload's
scan waited behind all of it.)

### Restoring is a write

Two operations put bytes back in front of users without going through an upload
surface, and both now enqueue a scan of the restored file.

- **Restoring a version** (`POST /api/files/versions/restore`). Snapshots in
  `.versions/` are deliberately **not** scanned when they are taken: every
  destructive write takes one, so scanning each would multiply the scan load by
  the edit rate, for bytes no one can reach. Restoring is rare and is the
  moment they become live again, so the scan happens there. Without it,
  overwriting an infected file with a clean one and rolling back was a way to
  put an infected file live on an install where every upload is scanned.
- **Restoring from trash** (`POST /api/files/manager/restore`). The trash is
  where quarantine puts an infected file — an antivirus quarantine and a user
  deletion produce the identical row — so a restore can release a file ClamAV
  condemned. It is also where old bytes go live again: they were scanned when
  they arrived, but the signature database has moved on since. Restoring a
  **folder** scans every file in the subtree it brings back, not just the row
  the user clicked.

⚠ Both are **asynchronous**, exactly like an upload: the file is live and
unscanned until the verdict lands, and an infected verdict quarantines it
straight back (`file.infected` fires as usual). That window is not a compromise
specific to restore — it is the same window every uploaded file has, and it is
what keeps a slow scanner from stalling a write. Making restore block on ClamAV
would make it the only write surface in filex that does.

### Files written in the editor

filex's built-in text editor (`POST /api/files/save-text`, the Monaco code view
and markdown "edit" mode) is the other surface that does **not** go through
that gate, and until this was fixed it enqueued no scan at all — which made the
editor a way to put a file on an install where every uploaded file is scanned
and have it never be.

It now splits the two cases, because they are genuinely different:

- **Creating a file** — the editor writes to a path with no catalogue row —
  scans **immediately**, exactly like an upload. A create happens once.
- **Saving over a file that already exists** schedules **one** scan, the
  save-scan window from now. Every further save to that file inside the window
  is dropped rather than rescheduled, so the window starts at the first save
  and the scan is guaranteed to happen rather than being pushed out
  indefinitely by someone who keeps typing. When it runs it reads the file as
  it stands **then** — the final state, not the content of the save that
  scheduled it.

So a burst of Ctrl+S costs exactly one scan, and the file is unscanned for at
most one window after its last save. That trade, and the reasoning behind the
2–60 minute bounds, is in
[CONFIGURATION.md](CONFIGURATION.md#the-editor-save-scan-window).

⚠ The delay is `ops_queue.not_before`, not a timer inside the process, and the
"one pending scan per file" rule is a unique coalescing key on the pending
queue row. Both survive a restart: a deploy in the middle of someone's editing
session does not drop the scan, and two saves landing at the same instant
produce one scan rather than none.

An infected verdict from a delayed scan behaves identically to one from an
upload — quarantine into `.filex-trash/` plus a `file.infected` event.


## Version retention (`versions.keep_n`)

By default filex trims each file's history to a compile-time 20 snapshots at
snapshot time. `versions.keep_n` adds an operator-tunable **daily retention
sweep**:

- `0` (default): sweep disabled — behavior unchanged.
- `N > 0`: once a day, every node that has version rows is trimmed to its
  newest `N` versions (rows + backing `.versions/…` objects, deletion of the
  storage object being best-effort per object).

The sweep shares its schedule with the trash purge loop: daily tick, first
tick one interval after boot, summary log line
(`version retention complete keep_n=… nodes=… deleted=…`).

> Note: because the snapshot path still trims to 20 inline, values **above 20**
> currently have no additional effect — `keep_n` is practically a way to keep
> *fewer* than 20 versions.

## The `file.infected` event

Emitted on the notification bus (in-app bell + [webhook v2](NOTIFICATIONS.md)
targets — add `file.infected` to a target's event allow-list, or leave the
list empty for all events):

```json
{
  "event": "file.infected",
  "severity": "warning",
  "title": "Infected file detected",
  "body": "/inbox/malware.exe: Eicar-Test-Signature",
  "node": { "storage_id": 1, "path": "/inbox/malware.exe", "name": "malware.exe", "size": 68 },
  "meta": { "signature": "Eicar-Test-Signature", "quarantined": true, "trash_path": "/.filex-trash/…" }
}
```

`meta.signature` is the ClamAV signature name; `meta.quarantined` is `false`
only in the rare case of a storage driver without rename support (the file
then stays in place and the WARN log is the operator's cue).

---

See also: [TRASH-VERSIONING.md](TRASH-VERSIONING.md) ·
[NOTIFICATIONS.md](NOTIFICATIONS.md) · [CONFIGURATION.md](CONFIGURATION.md)
