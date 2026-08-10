# Updates

filex can tell you when a new release exists and — where it is able to — install
it for you. What happens is decided by **which part of the version moved**:

> Looking for **what changed** rather than how it is installed?
> **[Releases](RELEASES.md)** lists every published version with a short summary.

| Version part | Example | What filex does |
|---|---|---|
| **z** — patch | `0.7.5 → 0.7.6` | Applies it automatically (when the policy allows) |
| **y** — minor | `0.7.6 → 0.8.0` | Announces it; you upgrade with one click |
| **x** — major | `0.9.0 → 1.0.0` | Announces it and shows the upgrade instructions |

The asymmetry is the whole design. A patch is a fix on a shape that already
works. A minor may add a migration or change an embedded API. A major is a
decision you should read about before taking.

> **Nothing moves until you opt in.** Out of the box filex only *checks* and
> tells you (`policy: manual`). Automatic patching starts when you set
> `AUTO_UPGRADE=true`.

---

## Quick start

```bash
# Check and announce only (default)
FILEX_UPDATE_POLICY=manual

# Apply patch releases by themselves, announce the rest
AUTO_UPGRADE=true

# No outbound update requests at all
FILEX_UPDATE_CHECK=0
```

The admin UI shows the result under **Ops → Updates**: running version, what is
available, why filex is or is not taking it, and — when it cannot act itself —
the exact commands for your install.

---

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `FILEX_UPDATE_CHECK` | `1` | Master switch for the periodic check. `0` = no outbound request ever. |
| `FILEX_UPDATE_POLICY` | `manual` | `off` · `manual` · `patch` · `minor` — how far filex may go on its own. |
| `AUTO_UPGRADE` | – | Shorthand: `true` selects `patch`. An explicit `FILEX_UPDATE_POLICY` wins. |
| `FILEX_UPDATE_CHANNEL` | `stable` | Release channel (informational unless you host your own manifest). |
| `FILEX_UPDATE_MANIFEST_URL` | `https://filex.sh/updates/stable.json` | Where the release index is fetched from. Point it at your own mirror for air-gapped installs. |
| `FILEX_UPDATE_WINDOW` | – | Daily maintenance window for automatic upgrades, e.g. `03:00-05:00` (server local time). Empty = any time. |
| `FILEX_UPDATE_INTERVAL` | `24h` | Time between checks. Values under `1h` are raised to `1h`. |
| `FILEX_UPDATE_PRE_COMMAND` | – | Shell command run right before a self-upgrade (database dump for external engines). A non-zero exit **aborts** the upgrade. |
| `FILEX_INSTALL_MODE` | auto-detected | `binary` or `docker`, when the detection is wrong for your setup. |

The same keys exist in `config.yaml` under `update:`.

### What the check sends

A single `GET` for a static JSON document. The only thing that identifies you is
the `User-Agent`, which carries the running version:

```
User-Agent: filex-updater/0.7.6
```

No hostname, no license key, no instance id, no file statistics. If that is still
one request too many, `FILEX_UPDATE_CHECK=0` stops it completely and the feature
goes silent.

---

## Binary vs container

**Binary / systemd installs** own their executable, so filex can replace it:

```bash
filex self-update            # install the newest release
filex self-update --check    # look, change nothing
filex self-update --to v0.8.0  # a specific version — you are the confirmation
```

**Container installs cannot upgrade themselves, by design.** An image layer is
immutable: a binary written inside a running container disappears at the next
`docker compose up`, and the version silently reverts. That is a worse failure
than not upgrading — the UI would report success while the old code kept running.
So in a container filex refuses, and shows you this instead:

```bash
# docker-compose.yml: image: ghcr.io/brf-tech/filex:v0.7.6
docker compose pull filex
docker compose up -d
```

If you want containers to update themselves, use a dedicated updater
(watchtower, diun, Renovate on your compose repo). filex will not ask you for
`/var/run/docker.sock`: that socket is root on the host, and a file manager is
the last service that should hold it.

---

## What "automatic" actually checks

Before a patch is applied without asking, **all** of these must hold:

1. The policy allows it (`patch` or `minor`).
2. The release is marked `auto_ok` in the manifest — a **kill switch**: a bad
   release can be pulled back from automatic distribution without deleting the
   tag.
3. Neither the target nor any release being skipped over carries a schema
   migration. A patch that changes the schema is a packaging mistake, and filex
   treats it as "not really a patch" and asks.
4. The install can replace its own binary (not a container).
5. The current time is inside `FILEX_UPDATE_WINDOW`, if one is set.

Minor releases have one extra rule: while filex is on a `0.x` version, semver
gives minor releases no compatibility promise, so they are **never** automatic —
even under `policy: minor`. That relaxes once the project reaches `1.0`.

## What an upgrade does, in order

1. Refuse immediately if this install cannot replace itself.
2. Download the build for your OS/arch and verify its **SHA-256** against the
   manifest (which arrived over TLS).
3. Unpack next to the current binary — same filesystem, so the final move is
   atomic rather than a cross-device copy.
4. **Smoke-test** the new binary by running `filex --version`. A truncated
   download, a wrong architecture or a corrupt archive dies here, before
   anything is replaced.
5. Take a **database snapshot** (`FILEX_UPDATE_PRE_COMMAND`, or `VACUUM INTO`
   for sqlite). If this fails, the upgrade aborts with nothing changed.
6. Keep the old binary as `filex.bak-<version>`, then move the new one into
   place.
7. Restart via systemd if that is what is supervising; otherwise report
   "restart required" and keep serving with the old process until you do.

Everything before step 6 is undone by doing nothing.

> **Why the snapshot matters:** filex has no down migrations. Once a release has
> migrated the schema there is no code path back — the backup *is* the rollback.

### Rolling back

```bash
systemctl stop filex
mv /usr/local/bin/filex.bak-0.7.5 /usr/local/bin/filex
# restore the snapshot only if the upgrade ran a migration:
#   cp instance.sqlite.pre-0.7.6-20260729T113000Z instance.sqlite
systemctl start filex
```

---

## The release manifest

The document filex polls is a plain static JSON file, so you can host your own
(internal mirror, air-gapped network, a fork):

```json
{
  "channel": "stable",
  "releases": [
    {
      "version": "v0.7.6",
      "date": "2026-07-29",
      "auto_ok": true,
      "migrations": false,
      "severity": "normal",
      "notes": "AI surface: denials answer 403 instead of 500",
      "notes_url": "https://github.com/BRF-Tech/filex/releases/tag/v0.7.6",
      "image": "ghcr.io/brf-tech/filex:v0.7.6",
      "assets": [
        {
          "os": "linux",
          "arch": "amd64",
          "url": "https://github.com/BRF-Tech/filex/releases/download/v0.7.6/filex_0.7.6_linux_amd64.tar.gz",
          "sha256": "…"
        }
      ]
    }
  ]
}
```

Two fields cannot be derived from a git tag, which is why this document exists
at all:

- **`auto_ok`** — the kill switch described above.
- **`migrations`** — makes "patches carry no schema changes" checkable instead
  of a promise.

`min_version` is available for releases that must not be jumped to directly;
installs below it are told to upgrade in steps.

Digests live **in the manifest** rather than a separate checksums file, so there
is exactly one authenticated document to trust: it arrives over TLS from a host
you chose, and every downloaded byte is verified against it.

---

## Notifications

Two events are emitted through the normal notification pipeline (in-app history
plus any configured webhooks):

| Event | When |
|---|---|
| `update_available` | A newly published release is seen. Fires **once** per version — the mark is persisted, so a restart loop cannot turn it into a stream. Severity rises to `warning` for security releases. |
| `update_applied` | A self-upgrade replaced the binary. |

---

## Embedded / vendored copies

If you embed the filex explorer web component in another application and vendor
its bundle, that copy has its own upgrade path — the server updating itself does
not move it. Treat it like any other dependency: bump on patch automatically,
review on minor (the component's config and event API may change), and verify
after the bump that the host page still loads the bundle it expects.

---

## API

All three are admin-only.

```http
GET  /api/admin/update        → cached status; never touches the network
POST /api/admin/update/check  → force a fetch, then return the status
POST /api/admin/update/apply  → install the pending release
```

`apply` answers `409` on a container install, with the instructions in the body.
That is a permanent condition, not a transient failure — which is exactly why it
is not a `5xx`.
