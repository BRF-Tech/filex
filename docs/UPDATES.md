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
| `FILEX_INSTALL_MODE` | auto-detected | `binary`, `docker` or `package` — or the package manager by name: `homebrew`, `winget`, `snap` — when the detection is wrong for your setup. See [Package-manager installs](#package-manager-installs). |

The periodic-check settings also exist in `config.yaml` under `update:`
(`enabled`, `policy`, `channel`, `manifest_url`, `window`, `interval`).
`AUTO_UPGRADE`, `FILEX_UPDATE_PRE_COMMAND` and `FILEX_INSTALL_MODE` are read from
the environment only.

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

In a container, `FILEX_UPDATE_POLICY=patch` (or `minor`, or `AUTO_UPGRADE=true`)
therefore changes nothing: every release is announced. **Ops → Updates** says
so — its policy badge reads **Announces only**, and a line under it names the
saved policy as having no effect on this install. See
[What the policy badge says](#what-the-policy-badge-says).

---

## Package-manager installs

When a package manager installed filex — Homebrew, winget or Snap — the package
manager owns the binary, and filex leaves it alone: `filex self-update` refuses,
and no update policy ever replaces it. Replacing it anyway would split the two
records: the package manager would keep reporting the old version, and its next
upgrade would write over whatever filex had put there (a snap's files are
read-only to begin with). Upgrade with the package manager instead — filex
tells you the command:

| Installed with | How filex recognizes it | Upgrade command |
|---|---|---|
| Homebrew (cask) | the binary lives under `…/Caskroom/<token>/<version>/` | `brew upgrade --cask filex` |
| Homebrew (formula) | the binary lives under `…/Cellar/<formula>/<version>/<dir>/` | `brew upgrade filex` |
| winget | the binary lives under `…\WinGet\Packages\<id>_<source>\` — per user under `%LOCALAPPDATA%\Microsoft`, machine-wide under `%ProgramFiles%` | `winget upgrade BRFTech.filex` |
| Snap | `SNAP` and `SNAP_NAME` are set **and** the binary lives under `$SNAP` | `snap refresh filex` |
| a distribution package (`.deb`, `.rpm`, AUR …), Linux | the binary lives directly in `/usr/bin`, `/usr/sbin`, `/bin` or `/sbin` — only a package manager puts files there; a hand install goes to `/usr/local/bin` | none named: upgrade with the package manager that installed it |

The name in the command is read from where the binary lives — the cask token,
the formula, the winget package id, the snap instance — so a renamed or forked
package is told the command for itself. What keeps the detection honest:

- The path is judged after following links: `/opt/homebrew/bin/filex` and
  `WinGet\Links\filex.exe` point into the layouts above.
- Only the whole layout counts, never a similar name: `~/Cellar-backup/filex` is
  a plain binary, and so is a `Cellar` folder without a version inside it.
- `SNAP` alone is not enough. A terminal opened from another snap (the VS Code
  snap's, for one) passes its own `SNAP` on to everything started in it, and a
  plain filex started there must not be told to `snap refresh code`.
- Inside a container the container wins: the image is what gets upgraded.

What still works on such an install:

```bash
filex self-update --check    # reports the newest release, and:
# upgrade: brew upgrade --cask filex
```

The admin page (**Ops → Updates**) shows the release, why filex is not taking it
and the command, with no **Upgrade now** button. Its policy badge reads
**Announces only** whatever `FILEX_UPDATE_POLICY` says, and the line under it
names the saved policy as having no effect here, with the package manager and
its command (`brew upgrade --cask filex`) — or "your package manager" when
filex cannot tell which one it was. Two things filex would have
done itself are yours now, and the instructions say so: a package manager
replaces the file, not the running process, so **restart filex** afterwards (on
Windows, stop it first — a running `filex.exe` cannot be replaced); and the
database snapshot a self-upgrade takes before a schema change does not happen,
so when a release changes the schema, **back up first**.

**Packagers:** a package that installs filex into `/usr/bin` (or `/usr/sbin`,
`/bin`, `/sbin`) is recognized without help. One that puts it elsewhere (under
`/opt`, say) can declare itself with
`FILEX_INSTALL_MODE=package` in the environment filex runs in — the service
unit's `Environment=` covers the server's automatic updates; a `filex
self-update` typed in a shell reads the shell's. filex then refuses to replace
itself and tells the operator to upgrade with the package manager that installed
it. `FILEX_INSTALL_MODE=homebrew|winget|snap` names the manager outright, and
`FILEX_INSTALL_MODE=binary` turns all of this off — on a snap the replacement
then fails anyway, because the files are read-only.

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
4. The install can replace its own binary (not a container, not a package
   manager's).
5. The current time is inside `FILEX_UPDATE_WINDOW`, if one is set.

Minor releases have one extra rule: while filex is on a `0.x` version, semver
gives minor releases no compatibility promise, so they are **never** automatic —
even under `policy: minor`. That relaxes once the project reaches `1.0`.

### What the policy badge says

The badge on **Ops → Updates** says what this install **does** by itself, which
is not always what the saved policy asks for. The server works it out from the
policy, the checking switch, the install mode and the running version — the
same rules as the list above, in one place — and the page only words it:

| Saved policy | Install | Badge | Why |
|---|---|---|---|
| any | every case not listed below | **Policy: …** (the saved policy) | the policy is in force |
| `manual`, `patch`, `minor` | any, with `FILEX_UPDATE_CHECK=0` | **Checking off** | nothing is checked, so nothing is announced or installed |
| `patch`, `minor` | container | **Announces only** | the image owns the binary |
| `patch`, `minor` | package manager (Homebrew, winget, Snap, a distribution package) | **Announces only** | the package manager owns the binary; its name and command are shown |
| `minor` | plain binary on `0.x` | **Installs patches** | a `0.x` minor release is never automatic |

When the badge is not the saved policy, a line under it names the saved policy
and says it has no effect here, and why. The policy itself is **kept**, not
reset: it takes effect again when the reason goes away — checking is switched
back on, the data directory is served by a plain binary, filex reaches `1.0`.

## What an upgrade does, in order

1. Refuse immediately if this install cannot replace itself (a container, or a
   binary a package manager owns).
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

> **Why the snapshot matters:** putting the old binary back does not put the old
> schema back. A self-upgrade never rolls a migration down, and the down steps
> that do exist (`filex migrate down`, one at a time) are destructive by
> definition — `00038`'s drops the columns it added, with whatever was written
> into them. For anything that already migrated, the backup *is* the rollback.

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

Two fields are filex's own rather than GitHub's:

- **`auto_ok`** — the kill switch described above. It cannot be derived from
  anything, which is why this document exists at all.
- **`migrations`** — makes "patches carry no schema changes" checkable instead
  of a promise, and makes an install confirm (so a backup is taken) before it
  takes a release that changes the schema. `scripts/gen-update-manifest.py`
  **derives** it from the git tags — a tag whose tree holds a migration file no
  earlier tag held — so a mirror built from a checkout with its tags fetched
  gets it right without a list to maintain (`--print-migrations` shows the
  result, `--repo-dir` names the checkout). Every published release is listed,
  not just the newest ones.

`min_version` is available for releases that must not be jumped to directly;
installs below it are told to upgrade in steps.

Digests live **in the manifest** rather than a separate checksums file, so there
is exactly one authenticated document to trust: it arrives over TLS from a host
you chose, and every downloaded byte is verified against it.

---

## Notifications

Two events are emitted through the normal notification pipeline (in-app history
plus any configured webhooks). ⚠ Since v0.43.0 both are **operator alarms**:
only an administrator's bell and badge carry them. The stored row, the admin
**Notifications** list and the webhook delivery are unchanged.

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

All three are admin-only, and in **multi-tenant mode supertenant-only**:
applying a release replaces the binary every tenant is served by and needs a
restart of the whole instance, so it belongs to the platform operator. A tenant
admin gets `403 supertenant_only`, on the status read as well — an admin who
cannot apply an update has nothing to do with the answer. Single-tenant
installs are unaffected. See
[MULTI-TENANCY.md](MULTI-TENANCY.md#instance-wide-admin-surfaces).

```http
GET  /api/admin/update        → cached status; never touches the network
POST /api/admin/update/check  → force a fetch, then return the status
POST /api/admin/update/apply  → install the pending release
```

`apply` answers `409` on a container or package-manager install, with the
instructions in the body. That is a permanent condition, not a transient
failure — which is exactly why it is not a `5xx`.

The status names the install: `mode` is `binary`, `docker` or `package`, and a
`package` install also carries `package_manager` (`homebrew`, `winget`,
`snap`), `package_manager_name` (the product name to show, e.g. `Homebrew`) and
`upgrade_command` (e.g. `brew upgrade --cask filex`). The manager fields are
absent when `FILEX_INSTALL_MODE=package` was set and nothing more could be
detected.

`policy` is the saved policy, always as saved. `behavior` is what the install
does with it by itself — `off`, `announce`, `patch` or `minor` — and
`policy_limit` says why that is less than `policy` asks for: `disabled`
(checking is switched off), `container`, `package`, or `zero_major` (`minor` on
a `0.x` version). `policy_limit` is absent when the policy is in force. A
client shows these; it does not work them out from `mode` and `policy` (see
[What the policy badge says](#what-the-policy-badge-says)).
