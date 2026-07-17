# Protection: antivirus, trash retention & version retention

The v0.4 "Koru" wave groups filex's data-protection knobs behind one admin
surface: optional **ClamAV upload scanning**, the existing **trash retention**
window, and a new **version retention** count. Trash and versioning themselves
are documented in [TRASH-VERSIONING.md](TRASH-VERSIONING.md).

- [Protection settings API](#protection-settings-api)
- [Antivirus (ClamAV)](#antivirus-clamav)
- [Version retention (`versions.keep_n`)](#version-retention-versionskeep_n)
- [The `file.infected` event](#the-fileinfected-event)

---

## Protection settings API

Admin-only, session or admin-scoped token:

| Method & path | Body | Notes |
|---|---|---|
| `GET /api/admin/protection` | — | Returns `{"trash_retention_days":30,"versions_keep_n":0,"antivirus":{"enabled":false,"binary":""}}`. `antivirus` is a **live probe** of the resolved ClamAV binary (`binary` is its base name — `clamscan` / `clamdscan` — or `""`). |
| `PATCH /api/admin/protection` | `{"trash_retention_days"?: n, "versions_keep_n"?: n}` | Partial update; echoes the fresh GET shape. Validation: retention **1–3650** days, keep_n **0–1000** (`0` = unlimited, retention job off). Out-of-range → **400**. |

Both values live in the `settings` table (`trash.retention_days`,
`versions.keep_n`) — no migration, and the generic
`/api/admin/settings` endpoints see the same rows. Antivirus availability is
**not** a DB setting; it is an operator/environment concern (below).

## Antivirus (ClamAV)

Optional and fully self-configuring — filex scans uploads **only when a ClamAV
binary is present**. No binary → the feature is silently off, `capabilities`
reports `"antivirus": false`, and nothing else changes.

**Install** (Debian/Ubuntu):

```bash
apt install clamav clamav-daemon   # clamd + clamdscan (recommended: fast, daemon-backed)
# or minimal: apt install clamav   # clamscan only (slow cold start per scan)
```

**Binary resolution** (highest first):

1. `FILEX_CLAMAV=0` — kill-switch, scanning off even if a binary exists.
2. `FILEX_CLAMAV_BIN=/path/to/clamscan` — authoritative; an invalid path
   disables scanning (no silent fallback).
3. `$PATH`: `clamdscan` first, then `clamscan`.

`FILEX_CLAMAV_MAX` (bytes, default 100 MiB) caps the file size eligible for
scanning; larger uploads are skipped, not failed.

**How it works:** after every successful write on the upload surfaces
(chunked upload finalize, explorer multipart upload, public file-drop) a scan
job is enqueued on the persistent queue — scanning is **fully async** and never
blocks or fails the upload response. The worker reads the file back from its
storage backend, spools it to a temp file and execs ClamAV (`--no-summary`,
60s timeout; `clamdscan` also gets `--fdpass`).

- **Clean** (exit 0): no side effects at all.
- **Infected** (exit 1): the file is **quarantined into trash** — the object is
  renamed under `.filex-trash/` and the DB row soft-deleted exactly like a user
  delete, so it disappears from listings but stays restorable/purgeable through
  the normal trash tooling — and a [`file.infected`](#the-fileinfected-event)
  event fires. The server also logs a `WARN`.
- **Scan error** (exit ≥2, timeout): the op fails and uses the queue's normal
  retry budget; the file stays in place.

The capability endpoint (`GET /api/capabilities`) advertises the current state
as a top-level `"antivirus": true|false` flag (same pattern as `"ocr"`).

> Files written over **WebDAV** or the AI/MCP surface are not yet enqueued for
> scanning — the wired surfaces are the three upload paths above.

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
  "title": "Virüs tespit edildi",
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
