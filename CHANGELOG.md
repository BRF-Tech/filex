# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.34.2] - 2026-09-06

### Fixed

- **An instance that was never told its public URL sent every browser to
  `localhost:5212`.** The realtime ticket built `ws_url` from `PublicURL`,
  whose default is a guess — so filex on any other port advertised a socket
  address nothing was listening on, the connection was refused, and the client
  dropped to 12-second polling **with nothing logged**. It reads as "live
  updates do not work" while the server is perfectly healthy, and it is the
  default state of any install that has not set `FILEX_PUBLIC_URL`.

  With no public URL configured the socket address now comes from the request,
  which is safe *here and only here*: the ticket goes back to the client that
  asked for it, so a forged `Host` can mislead nobody but its sender. Share
  links are mailed to third parties and still use the configured origin — a
  test pins that difference.

  The startup banner had the same fault, printing the guess under "Listening
  on". It prints the real listen address, and says so, when no public URL was
  chosen.

- Two browser tests asserted **translated strings** and so failed once English
  became the fallback language in v0.33.0, with nothing about the features
  having changed: `82-capability-gating` looked for the Turkish OnlyOffice
  message (now a stable selector), and `17-theme-locale` was fixed in 0.34.1.
  A test that fails when the UI is translated is testing the translation.

## [0.34.1] - 2026-09-06

### Fixed

- **A user who had chosen Turkish saw an English admin panel on any second
  device.** The language on the account (`users.locale`) was written by
  Profile → Save and then **never read**. Saving also set it in this browser's
  `localStorage`, so it looked right on the machine you saved it on; sign in
  from another browser, another device or a private window and the account's
  choice was ignored and the browser's language used instead.

  It was invisible while `tr` was the fallback for everybody — a Turkish user
  got Turkish either way, from the default rather than from their preference.
  v0.33.0 made the fallback `en`, which was the right change, and this
  never-read preference surfaced behind it.

  The account's language is applied wherever the user is loaded, ranked below a
  choice made on this device (the language switcher writes only locally, so it
  is the newer decision) and above the instance default.

- **A red CI suite could not stop a release, and had not.** `release.yml` now
  calls `ci.yml` as its first job and nothing publishes until it is green.

  Until now CI ran on the branch push and the release workflow on the tag
  pushed two seconds later — in parallel, unaware of each other, with no
  required status check anywhere in the repository. Measured 2026-09-06: **CI
  had been red since v0.31.0 and four tags shipped over it**, carrying the
  locale bug above. None of the release steps would have caught it: they check
  the README, the screenshots, the links, the anchors and the version
  manifests, and never run a test.

- `17-theme-locale.cy.ts` asserted the account's language while measuring the
  **browser's**: with no stored choice the app fell back to browser detection,
  so it passed on a Turkish workstation and failed on an English CI runner. It
  now pins the browser to English, so only the saved preference can satisfy it.

- `siteAssets.test.ts` failed on every public CI run: `site/` is withheld from
  the published tree, so the directory it compares does not exist there. It
  skips where the directory is absent and still gates where it is present. The
  failure also cascaded — the image-build job `needs` it, so v0.34.0's images
  were never verified on main.

## [0.34.0] - 2026-09-06

### Upgrade notes

- ⚠ **Webhook subscribers: a write that REPLACES a file now emits
  `file.updated`, not `file.uploaded`.** Until this release every write
  announced `file.uploaded` whether it had created a file or overwritten one,
  because the post-write gate hard-coded the id — no filter could tell the two
  apart. A target subscribed to `file.uploaded` in order to see edits will go
  quiet; tick `file.updated` beside it on Settings → Webhooks. A target with an
  empty event list still receives everything, and a target that only ever
  wanted new files needs no change and now gets a quieter feed.

- ⚠ **`FILEX_CLAMAV`, `FILEX_CLAMAV_BIN` and `FILEX_CLAMAV_MAX` are now
  first-boot seeds, not live switches.** The antivirus on/off state, its mode
  and the clamd address live in the settings table and are edited on
  Settings → Protection. An existing `FILEX_CLAMAV=0` survives the upgrade —
  it seeds the row — but changing it in `compose.yml` afterwards will have no
  effect, which is a change in what that variable means.

- **Redis queue users:** the pending queue converts from a list to an ordered
  set on first boot so that priority is honoured. Every queued op is kept, in
  the order the old build was about to serve them. Downgrading afterwards is
  not supported.

### Added

- **Three events the server has been emitting all along are finally
  subscribable, and a fourth that had no name at all now has one.** The
  webhook subscription checkboxes were driven by a hand-written copy of the
  backend's event list, and it had drifted: `file.upload_failed`,
  `file.infected` and `comment.added` were being delivered to targets with an
  empty allow-list but could not be ticked by anyone who wanted only those.
  `file.infected` is the one that matters — filex scans uploads for viruses and
  there was no way to ask it to tell you when it found one.

  A fifth event was worse off. The escrow announcement — *an encrypted folder
  was opened with the recovery key rather than its owner's passphrase*, about
  as security-relevant as this product gets — was written as an inline
  `notify.EventType("e2e.escrow_used")` inside a handler. It is emitted on
  every escrow unlock, and because it was not a constant, nothing that reads
  the event catalogue could see it. It is `EventE2EEscrowUsed` now and appears
  in the list with the rest.

  Every event in the list also gained a label an operator can read, in English
  and Turkish, with the wire name kept underneath the checkbox — the checkbox
  used to be labelled `file.trashed` and nothing else.

- **`file.updated` — a write that replaced an existing file no longer claims a
  new one arrived.** ⚠ **This is a behaviour change for existing webhook
  subscribers.** Until now every write on every surface emitted
  `file.uploaded`, whether the bytes created a file or overwrote one that was
  already there, so nothing downstream could tell "a document arrived in this
  folder" from "somebody edited that document" — and no filter could separate
  them, because they were literally the same event id.

  From this release the post-write gate splits them: **created →
  `file.uploaded`, replaced → `file.updated`.** A target subscribed to
  `file.uploaded` in order to see edits will go quiet and has to tick
  `file.updated` as well (the admin UI lists it next to `file.uploaded`); a
  target with an empty event list still receives everything, and one that only
  ever wanted new files needs no change and gets a quieter feed.

  It is one decision in one place — `writehook.OnFileWritten` now takes the
  kind, so every surface answers the same question from the fact it already
  had: the cache-row lookup it does anyway. That covers the browser upload
  form, WebDAV `PUT`, S3 `PutObject`/`CopyObject`, SFTP, FTPS, NFS, the AI/MCP
  write, the archive extractor and the ops-worker copy. Two paths are called
  out because they answer it differently:

  - The **staged (large-file) upload** asks the storage driver rather than the
    catalogue, one statement before the overwrite. It has to: its node row is
    published at commit time, before the bytes move, so by transfer time the DB
    has a row either way and has forgotten whether it minted it — and unlike a
    flag carried from commit, the driver's answer survives a restart in between.
  - Where a surface genuinely **cannot tell** (the DB mirror was unreachable,
    so there is no row to compare against) it reports `file.uploaded` — the
    value the event carried before, rather than a wrong claim that a file was
    edited.

- **The text editor announced nothing at all.** `/api/files/save-text` wrote
  the bytes, updated the row, re-indexed the file and scheduled the antivirus
  scan — and never emitted an event, so a file created or rewritten in the
  browser reached no webhook and no notification bell. It now goes through the
  shared gate like every other write surface (`file.uploaded` on create,
  `file.updated` on save), using the same `existing` lookup that already chose
  between an immediate and a debounced scan. It keeps its own debounced scan:
  the divergence is in the scan, never in the event.

### Fixed

- **The Office editor was the one write nothing looked at afterwards.** Every
  other write surface in filex fans out through the shared post-write gate —
  announce the change to open explorers, re-index for search, fire the webhook,
  queue an antivirus scan — and this release *extended* that gate to two more
  places. The OnlyOffice save-back was not one of them: it took its version
  snapshot, wrote the revision, refreshed the node row, and stopped. So a
  `.docx` edited in the browser kept its **pre-edit text in content search**,
  never reached a `file.updated` subscriber, left another browser with the
  folder open showing a stale listing, and — on an install where every upload
  is scanned — **was never scanned**. Office documents are exactly the file
  type macro-borne malware travels in, which is what made this the wrong gap to
  ship a release about scan coverage with.

  The callback now goes through the gate like everything else, as a
  **replacement** (`file.updated`, never `file.uploaded` — a save-back
  overwrites a document that was already there) stamped with a new
  `meta.origin: "onlyoffice"`, because the bytes are assembled and posted by
  the document server rather than by the browser that opened the file.

  ⚠ **Whether the scan runs now or on the save window is decided by the
  callback status, not by a rule of thumb.** The reason the browser's text
  editor got a debounced window is that it cannot tell a mid-session Ctrl+S
  from the last one. The document server does not make filex guess:

  - **status 2** (ready for saving) arrives once per editing session, after
    every editor has *closed* the document and the server has assembled the
    final revision — roughly ten seconds after the last one disconnects. The
    bytes are final and nobody is still typing, so it is scanned **immediately**,
    like an upload. Deferring it would coalesce nothing (there is one save) and
    would leave a finished document unscanned for up to a full window.
  - **status 6** (force save) is an *interim* save with the document still
    open. filex never asks for one and the document server does not send them
    by default, but an operator can switch them on, and then they repeat for as
    long as somebody keeps the document open — the shape of a Ctrl+S burst. It
    takes the **debounced** window, the same one the text editor uses.

  A session with force-save on therefore costs one scan per window while it is
  open plus one immediate scan when it closes. That last one is deliberate: a
  document server that dies mid-session never sends status 2, and then the
  debounced scan of the interim bytes is the only one there will ever be.

  ⚠ The driver `Stat` still lands on the row **before** the index runs, and
  that ordering is load-bearing: content re-extraction is triggered by the
  content fingerprint drifting, the fingerprint prefers the etag, and indexing
  against the pre-edit etag would refresh the metadata while leaving the **old
  text** searchable — most of the symptom being fixed.

- **`/api/admin/protection` let any tenant admin turn antivirus off for every
  tenant** — and three other instance-wide surfaces were open the same way.
  Everything under `/api/admin` has passed `RequireAdmin`, and in multi-tenant
  mode that means an admin of *some* tenant: the tenant resolver labels the
  request without denying anything, and the scoped store filters three list
  queries and nothing else. `/providers` and `/plugins` asked the extra
  question. These did not:

  - **`/protection`** — the antivirus switch, its mode and the clamd address
    moved into the global settings table in this release, so one customer's
    admin could disable scanning platform-wide or point the scanner at a host
    they control. Trash and version retention sat beside it.
  - **`/external`** — one document server, one converter, one shared JWT
    secret: repointing it is enough to read and rewrite every tenant's office
    documents in transit, and the Test button dials whatever it is given.
  - **`/auth-providers`** — the global `auth.*` rows that decide who can sign
    in to the instance at all (OIDC issuer and client secret, LDAP bind).
  - **`/update`** — replaces the binary every tenant is served by.

  All four now ask `requireSupertenant`, one predicate shared with `/providers`
  and `/plugins` rather than a second mechanism. ⚠ The check lives in the
  **handler**, not on the chi route, because the route is not the only door:
  `/api/ai/admin` mounts the same handler instances behind an `admin`-scoped
  API token, and the MCP admin tools drive those same methods in-process — a
  route middleware would have closed one of three.

  ⚠ **Reads are gated too, not only writes.** `/protection` returns the clamd
  address in force and a live reachability probe; `/external` returns where the
  document server lives. That is a map of the operator's internal
  infrastructure, handed to somebody with no standing to act on it.

  ⚠ **Single-tenant installs are unaffected, by construction.** The tenant
  resolver attaches no scope when multi-tenant mode is off, absence means
  "unscoped", and unscoped passes — there is no flag to set. The gate fails
  *closed* the other way: an authenticated user whose provider cannot be
  resolved already gets a deny-all scope, and it is refused rather than read as
  "no scope, therefore single-tenant".

  The surfaces that are still instance-wide and still ungated are now
  **named** rather than half-closed — `/settings` (the same global table, but
  `branding.*` keys are legitimately per-tenant, so it needs a per-key
  classification and not a route gate), webhooks, replication targets and
  rules, the search rebuild, the queue, the trash sweep, and a set of
  cross-tenant metadata reads. See
  [MULTI-TENANCY.md](docs/MULTI-TENANCY.md#instance-wide-admin-surfaces).

- **A 403 that had a sentence to say showed a machine code instead.** The admin
  SPA's error extractor preferred `data.error` over `data.message`, so a
  response carrying both — the two `supertenant_only` and `plugins_disabled`
  shapes — put `supertenant_only` on screen and threw away the explanation
  written for the person reading it. `message` now wins when both are present;
  the handlers that return only `error` are the majority and put the sentence
  there, so they are unchanged.

- **The plugins gate answered "plugins are disabled" before "not yours".** A
  tenant admin who may not touch the surface at all could still learn whether
  the operator had the subsystem switched on. The tenancy check runs first now.
  Single-tenant installs see no difference: no scope is attached, the gate
  passes, and a disabled subsystem still answers `503`.

- **The docs-site build no longer hands you somebody else's diff.**
  `cd docs-site && npm run build` is a mandatory release gate, so everybody
  runs it — and it was `npm run releases && vitepress build`, which refetched
  the GitHub releases and rewrote `docs/RELEASES.md` and
  `docs-site/data/releases.json` every single time, restamping today's date
  even when nothing had changed. Three people hit it in one day, each reverted
  it by hand, and one release nearly committed the churn under an unrelated
  message.

  The build now runs an offline check instead (`check-releases.mjs`: the page
  exists and lists at least one release, no network, no writes), and
  regenerating is an explicit `npm run releases` at the release step that means
  to do it. `docs/RELEASES.md` stays tracked and published — ignoring it was
  never an option, readers see that page.

  The generator is idempotent as well, which is the other half: `generatedAt`
  only moves when the release list actually moved, and neither file is written
  unless its bytes changed. So a curious `npm run releases` cannot dirty the
  tree either — only a real new release can.

- **Two gates now catch the drift that caused the above, instead of a user
  reporting it.** The UI's event list stays a hand-maintained mirror on purpose
  — the admin SPA is compiled into the server binary, so an endpoint listing
  the events could never disagree with the bundled UI and would only add a way
  for the checkbox list to render empty. What a mirror needs is a build-time
  check, so it has one: `web/tests/webhooks/eventCatalog.test.ts` parses the Go
  constants and fails when the two sets differ or an event is missing its
  English or Turkish label, and `backend/internal/notify/catalog_test.go`
  refuses an inline `EventType("x.y")` anywhere in the backend, so an event
  cannot be born somewhere the catalogue does not look.

- **The Redis queue driver ignored `Priority`.** Its pending set was a LIST
  consumed with `BLMOVE`, and a list is positional: an op's priority was
  persisted, returned by `Get` and rendered in the admin UI, and had no effect
  whatsoever on the order ops came out. So the rule the SQLite and Postgres
  drivers enforce — a scan the storage sync discovered sits one step below a
  person's upload, `ORDER BY priority DESC, enqueued_at ASC` — simply did not
  apply on Redis: a first import of 20 000 files was still FIFO and an upload's
  scan still waited behind all of it. Measured on a real Redis with that
  backlog, an interactive op waited **20.0 s** behind 18 000 sweep ops; it is
  now served **first, in 1 ms**.

  The pending set is a **sorted set** whose score encodes `priority DESC,
  arrival ASC` exactly, and claiming is **one Lua script**: the removal from
  pending, the move to running, the status write, the attempt bump and the
  release of the coalescing key happen as one indivisible step. That is
  stricter than what it replaces — `BLMOVE` moved the id and a *second*
  transaction flipped the hash, and a process that died in the gap left an op
  in no list at all, permanently `pending` in its own hash, which
  `RecoverOrphans` then dropped. Type filtering no longer mutates anything
  either: the script walks candidates in priority order and steps over the ones
  this worker cannot handle, instead of popping them and pushing them back.
  `Enqueue` is a script too, so it is one round trip rather than two and can no
  longer leave a coalescing claim behind a write that failed.

  ⚠ **What it costs:** the claim is no longer a blocking Redis command, because
  a script cannot block. A worker with nothing to do now waits on a capped
  doorbell list that every push into pending writes a token to, so an arriving
  op still wakes a worker in about a millisecond. Two honest differences: a
  token can be taken by a worker whose type filter does not match the op that
  produced it, and a drained burst can leave a few stale tokens behind. Each
  costs one extra script call, never a missed op.

  ⚠ **Upgrading:** an install already running the Redis driver has a LIST at
  the pending key, and `ZADD` against a list is a `WRONGTYPE` error. Startup
  converts it, keeping every queued op and the order the old build was about to
  serve them in — and improving it, since those ops now carry their priority.
  Downgrading afterwards is not supported.

- **A file replaced under a local storage was never noticed.** The storage sync
  exists to find changes filex did not make; on the drivers that report no
  etag — **local, SFTP, SMB, FTP**, and any WebDAV server that omits the header
  — it found none, ever. Drift was an etag comparison, and comparing two empty
  strings is never a difference, so a file swapped out underneath filex looked
  unchanged on every pass: its size stayed stale in the catalogue, its extracted
  text stayed stale in the search index (you found the old words, not the new
  ones), and since the sync began queueing antivirus scans, a clean file could
  be replaced with an infected one and nothing would ever read it again.

  Where the backend reports no etag, drift is now the file's **size and
  modification time** — the two fields every one of those drivers does report,
  both already in the listing the walk just made, so a full walk costs what it
  always did (20 000 files on local disk: 3.0–4.0 s per pass before, 3.0–4.0 s
  after, with zero drift reported on three consecutive unchanged passes).

  It catches an ordinary edit, a rewrite that keeps the same size, a file that
  grew or shrank with its mtime preserved, and a restore whose mtime is *older*
  than the row's. It does **not** catch a replacement that preserves both the
  size and the modification time, or a rewrite landing in the same clock second
  as the recorded mtime with the size unchanged — those need the content, and
  hashing every file on every pass is not a walk anyone can run. See
  [STORAGE.md → Drift detection](docs/STORAGE.md#drift-detection-what-a-replaced-file-looks-like).

  Times are compared **to the second** deliberately: Postgres stores
  microseconds, FTP's `MDTM` has no sub-second field and FAT keeps two-second
  steps, so a finer comparison would report drift on every file on every pass —
  which on an install with antivirus enabled is the whole storage re-scanned
  every sync interval, forever. Directories are exempt for the same reason:
  their row holds the cached *recursive* size, which a listing never reports.

- **`Store.MoveNode` did not update `storage_key`, so a renamed or moved file
  kept the key it had at its old path.** `storage_key` is not decoration:
  versioning (`Snapshot` *and* `Restore`), the antivirus quarantine, the
  id-addressed download in `Manager.Read` and the sync tombstone pass all hand
  it to the storage driver **in preference to** `path`. Every one of them fails
  *silently* on a miss, which is why this survived so long:

  - `Snapshot` stats the stale key, gets `ErrNotFound`, and returns "nothing to
    snapshot" — so the pre-overwrite guard passes with **zero versions written**
    and the destructive write goes ahead. A moved file lost its history.
  - The antivirus quarantine moves the stale key into `.filex-trash/`, tolerates
    the `ErrNotFound`, marks the file quarantined and drops it from the search
    index — **while the infected bytes stay live** at the real path, now
    invisible to any future rescan.
  - `Restore` writes the restored version to the stale path, leaving the real
    file untouched, then stamps the node with the restored size and etag.
  - A download by id 404s on a file the listing shows.
  - `confirmGone` stats the stale key, gets a miss and tombstones a file that
    is perfectly fine.

  The column now follows the path on a **live** row. It deliberately does *not*
  on a **trashed** one, where `storage_key` holds the original path: that is
  the only record of where `trash.Service.Restore` puts the file back, and what
  `sync.reconcileTrash` reads to tell a restorable deletion from a row that has
  to be hard-deleted.

  Existing rows are repaired by migration `00033`, because nothing else would:
  the periodic storage walk only touches `seen_at` and `UpdateNodeMeta`, and
  neither statement writes this column, so a wrong value survives every sync
  run indefinitely. The repair is live rows only, and leaves an empty
  `storage_key` alone (every reader already falls back to `path`).

- **A single write announced itself three to six times, and a 5 000-file
  extraction announced itself 5 000 times.** Both reached every open explorer
  over the WebSocket. Measured on 2026-09-06 against a real NFSv3 mount and a
  real browser.

  The NFS half is not a bug in filex's counting — NFSv3 has no "close", so
  go-nfs opens, writes and **closes** the handle on every write RPC, and filex
  commits and announces on each close. `cp -p` of a 5 MB file: 1 `CREATE` +
  5 × 1 MiB `WRITE` = **6 frames** for one file.

  What the browser actually did with 5 000 frames turned out to be the more
  interesting half, and it was not "re-render 5 000 times". The explorer already
  coalesced — with a plain trailing debounce, which **starves**: while frames
  arrive closer together than the 200 ms window, every one of them cancels the
  pending reload. Measured in a real browser watching the folder being filled:
  the first re-listing came **114 seconds** into the job, with a 40-second gap
  in the middle. The open folder sat empty while five thousand files landed in
  it.

  Two changes, both in shared code, so the web app, the desktop app and every
  `@brftech/filex` embed get them together:

  - The realtime hub coalesces per folder on the **leading edge**: the first
    change in a quiet room goes out immediately with nothing added to it, and
    everything after it is merged into one trailing frame per window (200 ms,
    doubling to 1.5 s while a burst continues, reset when the folder goes
    quiet). A merged frame carries `count`, and keeps the file's name only when
    every merged change was the same change — naming one of five thousand would
    be worse than naming none. **Nothing is dropped, only merged**: a burst
    always ends with a frame reflecting its final state.
  - The explorer's reload debounce grew a ceiling (`burstDebounce`), so a
    sustained stream from any source can no longer postpone the re-listing
    indefinitely.

  Measured, same steps, same instance, before → after:

  | | before | after |
  |---|---|---|
  | frames, one 5 MB `cp -p` over NFS | 6 | **2** |
  | frames, 5 000-file zip extraction | 5 000 | **134** (all 5 000 changes accounted for by `count`) |
  | first re-listing of the open folder | 114.1 s | **0.46 s** |
  | longest gap between re-listings | 40.5 s | **2.0 s** |
  | longest single main-thread block | 1 087 ms | **360 ms** |
  | single WebDAV PUT → frame | 24 ms | **21 ms** |
  | single NFS write → frame reflecting final state | 171 ms | **218 ms** |

  ⚠ The last two rows are the ones that decided the design. A window that
  batches nicely but delays one ordinary upload would be a regression however
  good the frame count looked, which is why the first change in a quiet room is
  never delayed at all.

  New page: [Realtime updates & presence](docs/REALTIME.md) — the socket, the
  frames, the coalescing contract, and the debounce-with-a-ceiling advice for
  anyone integrating against it.

- **The README showed a screenshot advertising the private GitLab repo.** The
  plugins picture had a `github.com/brf-tech/filex` footer in it, on a
  page whose whole job is to be read by strangers on GitHub. It had also
  outlived several releases while every other screenshot was retaken.

  The cause was not the picture, it was the gate. `admin-plugins.png` needs the
  example plugin built and really running, so the capture script builds it with
  `go build` — and on a Windows workstation the Go toolchain lives in WSL, so
  that is `ENOENT` every single time. The script logged one line, skipped the
  shot, and **exited 0**. A release step that reports success while quietly
  leaving the old file in place is not a gate.

  Two changes: the script now falls back to cross-building through WSL, and a
  shot it was asked for and could not take **fails the run** (`SHOTS_ALLOW_SKIP=1`
  for a deliberate partial run). A wrong `SHOTS_PLUGIN_BIN` is an error rather
  than a shrug, for the same reason.

- **Files that arrive *on* a storage were catalogued, indexed and never
  scanned.** Every write *through* filex is handed to ClamAV — uploads, the
  AI/MCP surface, ShareX, drop links, the editor, both restore paths. A file
  that turns up on the backend instead (`aws s3 cp` into the bucket, another
  process writing on a mounted disk, everything that was already there when the
  storage was pointed at the folder) reaches the catalogue only through the
  periodic sync walk, which created the row, fed the search index and queued
  content extraction — and never once handed the bytes to the scanner. That is
  the one place a reader most expects a scan, because an operator who turned
  scanning on believes the files in filex are scanned.

  Measured end to end on a real instance with a real ClamAV: an EICAR sample
  written straight into a storage folder, one sync pass — catalogued at the
  root, trash empty, nothing in the log. The same run now quarantines it into
  `.filex-trash/` and logs `antivirus: infected file detected … quarantined=true`.

  The walk enqueues a scan in exactly two cases: a file it **newly catalogues**,
  and a file whose **content drifted**. ⚠ Not "every file the walk sees" — the
  walk sees the same objects forever, so scanning on sight would re-scan the
  entire storage every sync interval, 96 times a day on the 15-minute default.
  Eligibility is the ordinary one, so directories, empty files, oversized files
  and anything under `.filex-trash/` or `.versions/` are refused exactly as on
  the upload path, and an infected file the sync found is quarantined exactly
  like an uploaded one.

  ⚠ **The first import of an existing storage was the case that decided the
  shape of this.** Point filex at a folder holding 20 000 files and one pass
  queues 20 000 scans. The backlog itself is right — those files genuinely have
  never been scanned — and it is cheap: 465 MiB drained in **53 s** on 4
  workers with `clamdscan`, the walk itself about a second slower (0.8 s and
  1.4 s over two runs), 1.95 MiB of queue rows. What was *not* right is where
  it left everybody else: the queue
  orders `priority DESC, enqueued_at ASC`, so at equal priority those 20 000
  rows sit ahead of whatever arrives next, and a probe standing in for an
  upload's scan, enqueued ten seconds into the import, waited **41 s** to be
  picked up. Scans the sync asks for are therefore enqueued one step **below**
  every other op filex queues; the same probe then waited **1 ms**, because an
  interactive scan only ever waits for a worker to finish the one scan it is
  holding.

  There is deliberately **no cap** on how many a pass may enqueue: a file is
  "newly catalogued" exactly once, so a per-pass cap would silently drop the
  scans it deferred and leave them unscanned forever — a bound that reads like
  safety and is really data loss. ⚠ Two honest limits go with it: without
  `clamav-daemon`, `clamscan` re-loads its ~112 MB signature database on every
  invocation — measured over the same code path, 0.25 scans/s on 4 workers,
  which puts that same import at roughly **22 hours** — and the **Redis** queue
  driver's pending list is positional and ignores `priority`, so on Redis the
  first import is FIFO.

- **External services tested green and then did not work: the admin UI and the
  running process were reading different configurations** (GitHub issue #17).
  An operator whose OnlyOffice / drawio / converter lived in a separate compose
  file configured them the only way open to them — the admin UI — watched the
  **Test** button answer 200 for each, saw `PATCH /api/admin/external/<name>`
  save and `GET /api/admin/external` reflect it, and then got **"OnlyOffice is
  not configured"** the moment they opened a `.ods`.

  Both halves were true at once because they came from different places. The
  admin API, the Test probe and `/api/files/capabilities` all read the
  `external_services` **table**. Everything that *used* the configuration read
  a snapshot of env/YAML taken once at boot: `server.New` built the OnlyOffice
  service only `if cfg.ExternalServices.OnlyOffice.URL != "" && …JWTSecret != ""`,
  so with nothing in the environment the service was **nil** and every editor
  endpoint answered `503 {"error":"onlyoffice not configured"}` forever; the
  converter URL was baked into the AI/MCP handlers the same way. Nothing an
  operator did in the UI could reach the process.

  ⚠ The reporter's "thumbnails still work" was the clue that named it. The
  thumbnailer shells out to local binaries and consults external services *not
  at all* — so the split was exactly "reads the DB" vs "reads the boot
  snapshot", and thumbnails were in neither camp.

  Reproduced against their stack (podman-shaped: a real Garage S3 over a plain
  `http://` endpoint, SQLite, admin-API configuration only). Before: Test →
  `{"reachable":true,"state":"ok"}`, capabilities → `onlyoffice_url` set,
  `POST /api/files/onlyoffice/config` → **HTTP 503**. After, same steps, same
  process: **HTTP 200** with a signed editor descriptor whose fetch URL serves
  the document (285 bytes, ZIP magic). `file_root`'s converter URL went from
  `null` to the configured URL the same way.

  **The DB row is now the single runtime source of truth**, read on every use
  (`internal/external`, 1-second cache, invalidated by the admin PATCH). Env
  and `config.yaml` are declarative configuration *for* that row: a service they
  name is re-asserted onto it at every boot, so editing `FILEX_ONLYOFFICE_URL`
  and restarting still takes effect. An admin-UI edit to such a service applies
  immediately and is reverted at the next start — `GET`/`PATCH
  /api/admin/external` return `env_managed: true` for those and the UI labels
  the card, rather than letting the operator discover it after a restart.

  ⚠ Also fixed on the way: `GET` redacts a stored secret to `"***"`, and the
  admin UI re-sends what it was shown, so a save that only changed the URL would
  have written six asterisks over a working JWT secret. `PATCH` now ignores that
  exact value.

  ⚠ And the other way the same symptom could still be reached: **a URL with no
  JWT secret**. The health probe hits `/healthcheck`, a Document Server with no
  secret configured in filex answers it perfectly happily, and the editor then
  refuses because there is nothing to sign the descriptor with — a green Test
  next to "not configured" all over again. OnlyOffice with no secret is now
  reported `unconfigured` without a probe (a reachable server proves nothing
  there), and the Test button says *which* half is missing. drawio and the
  converter need no secret and are unaffected.

- **Deleted files left their bytes in `thumbs` and `uploads`, so disk was never
  reclaimed** (GitHub issue #18).

  **`thumbs` was an unbounded leak, and had been since v0.1.** Every generator
  writes `<data>/thumbs/<node id>.jpg`; *nothing in the tree ever removed one*.
  The `thumbnails` row went away with its node through the FK cascade, so the
  catalogue looked clean while the directory only grew — a delete did not clear
  it, purging from the trash did not, removing the whole storage did not, and
  `filex thumb` had no prune. Measured against Garage: three files uploaded → 3
  cached JPEGs; trashed → still 3; purged → **still 3, 5642 B**, with the
  objects gone from S3 and the node rows gone from the database.

  Two mechanisms now release them. A **purge** (trash emptied, retention expiry,
  "delete permanently") drops the file and its row there and then, so the space
  comes back when the user asks for it. And a **reconciler** — at boot and every
  `FILEX_THUMBS_SWEEP_INTERVAL` (default 6 h, `0` disables) — deletes cached
  files whose node no longer exists, which is what repairs an install that has
  been accumulating orphans for versions. One log line per pass, including the
  quiet ones.

  ⚠ Deleting cached bytes is destructive, so the reconciler removes a file only
  when **all** of: the name is exactly `<digits>.jpg`; the database *positively*
  reports that id absent from `nodes` (a query error abandons the pass and
  deletes nothing — "I could not ask" is never read as "it is gone"); node ids
  are never reused, so an absent id cannot acquire a file later; and the file is
  older than a 10-minute grace window. **A trashed file keeps its thumbnail** —
  it is restorable, and a cleanup that cannot tell "deleted" from "restorable"
  is the failure this must not be. Measured: live kept, trashed kept and still
  servable after restore, orphan reaped (`scanned=3 removed=1 freed_bytes=2403
  kept=2`).

  **`uploads` was bounded but did not release what a purge made dead.** Staging
  for an abandoned or failed upload ages out of the idle sweeper after
  `FILEX_UPLOAD_STAGING_TTL` (24 h) — verified — but nothing shortened that when
  the file those bytes belonged to was deleted permanently. Measured against
  Garage with a failing transfer: **1,109,269 bytes** still held after the node
  had been trashed *and* purged. The staged row is now looked up **by node id**
  at purge and released with it, so only that node's bytes are touched; a row in
  state `committing` is still left alone, because its bytes are being read by a
  transfer right now. ⚠ Trashing releases nothing: for a failed transfer the
  staging area holds the file's only copy.

- **The storage sync un-deleted files, so a deletion undid itself and an
  infected file left quarantine on its own.** Deleting in filex is a *rename*:
  the bytes move to `.filex-trash/<unix>-<rand>__<name>` and the node row is
  soft-deleted and retagged to that key. The sync worker then walked the
  storage from `/` down with no idea any of that existed — it saw the object,
  found no *live* row for it, found the soft-deleted one, and cleared
  `deleted_at`.

  It fired on **every pass, on every driver, with no condition attached**:
  there is no incremental mode — poll, fsnotify and driver-watch all funnel
  into the same full `RunOnce` walk — so there is no configuration in which it
  does not happen, only ones where it happens sooner. Measured end to end
  against
  a real S3 backend (MinIO on a pinned port): delete `rapor.txt`, trash listing
  shows 1, run one sync, trash listing shows **0** and the row's `deleted_at`
  is back to `NULL` — plus a second row minted for the `.filex-trash` bucket
  itself.

  ⚠ Quarantine is the same operation. The antivirus job condemns a file by
  calling the *same* `SoftDeleteAndRetag` with the *same* key shape, so nothing
  in the catalogue distinguishes a quarantine from a user deletion — and the
  resurrection therefore expired the security control on a timer nobody set.
  There was no way for the sync to tell the two apart, and no need: both must
  survive a sync pass.

  ⚠ The rule was not careless. It was written for GitHub issue #5, where
  `UNIQUE(storage_id, path_hash)` counted soft-deleted rows and one stale
  trashed row permanently blocked a fresh create at the same path. Reviving the
  row was the only way out **of that index**. Migration `00032` makes the index
  live-only, exactly as `00018` already did for
  `(storage_id, parent_id, name)`, and takes the reason away.

  Two rules replace it:

  - **The walk skips `.filex-trash/`.** The rows for everything in there
    already exist, retagged to the very keys on the storage, and the trash
    service owns them — restore and retention purge, never a listing.
  - **A trashed row is never revived.** An object at a path where a trashed row
    still sits is catalogued as a **new node**; the trashed row stays in the
    trash, restorable and on the retention clock. Bytes that reappear at a path
    are not the file that was deleted there — someone restored something out of
    band, or a new file landed with an old name — and reviving the row would
    hand them another file's identity, history, comments and shares while
    nothing downstream ever looked at them again.

  ⚠ Anything found **live** inside `.filex-trash/` is now repaired, which is
  what heals an install that already ran the old code: a revived deletion is
  soft-deleted again (keeping `storage_key`, so restore still knows where it
  came from) and a row minted for the trash's own bytes is dropped outright.
  That distinction matters in both directions — hard-deleting the first kind
  destroys a restorable deletion, and soft-deleting the second puts a
  `.filex-trash` entry in the trash listing whose purge would delete the trash
  directory. Bytes are never touched either way.

  ⚠ `seen` no longer counts trashed objects, so a storage whose trash held more
  than 30 % of its objects trips the whole-listing tombstone guard **once** on
  the first pass after upgrading: one warning, one skipped tombstone pass, and
  the next run compares like with like.

- **Restoring never scanned anything, on either path.** Two operations put
  bytes live without passing an upload surface, and neither enqueued a scan.

  - **Version restore.** Snapshots in `.versions/` are deliberately not scanned
    when taken — every destructive write takes one, so scanning each would
    multiply the scan load by the edit rate for bytes nobody can reach — which
    left "overwrite the infected file with a clean one, then roll back" as a
    way to put an infected file live on an install where every upload is
    scanned. The scan now happens at restore, which is rare and is the moment
    the bytes become live again.
  - **Trash restore**, the neighbouring path, had the same gap and a stranger
    version of it: the trash is where quarantine puts an infected file, so a
    restore could release something ClamAV had condemned. Restoring a folder
    scans every file in the subtree it brings back, not just the row the user
    clicked.

  ⚠ Both are asynchronous, exactly like an upload: the file is live and
  unscanned until the verdict lands, and an infected verdict quarantines it
  straight back. That window is not specific to restore — it is the window
  every uploaded file has, and it is what stops a slow scanner stalling a
  write. Blocking would make restore the only write surface in filex that waits
  on ClamAV.

- **A file created in filex's own text editor was never virus-scanned**, on an
  install where every uploaded file is. Found while auditing write surfaces,
  and reproduced before it was fixed: a real instance with a scanner wired, an
  infected file typed into the browser editor, and an `ops_queue` that shows a
  `content_index` job for that file and no `antivirus_scan` job at all — the
  editor reached the storage driver, the catalogue and the search index, and
  never the scanner.

  `writehook.OnFileWritten` is the single post-write gate every upload path
  goes through, and among other things it enqueues a scan. `save-text` never
  called it, for a defensible reason: routing it through the gate would have
  queued a full ClamAV pass on every Ctrl+S. The answer taken was to scan
  nothing, which is how a text editor became a way to introduce an unscanned
  file.

  The two cases are now split, because they are genuinely different:

  - **Creating** a file in the editor — a write to a path with no catalogue row
    — scans **immediately**, exactly like an upload.
  - **Saving** over a file that already exists schedules **one** scan, half an
    hour out by default. Further saves inside that window are dropped rather
    than rescheduled, so the window starts at the first save and the scan
    cannot be pushed out indefinitely by someone who keeps typing. When it
    runs it reads the file as it stands **then** — the final state, not the
    content of the save that scheduled it. A burst of Ctrl+S costs exactly one
    scan.

  ⚠ The delay is a row in the operation queue (`ops_queue.not_before`), not a
  timer inside the process. A `time.AfterFunc` would die with the process and
  take every pending scan with it — on every deploy, which is exactly when a
  server is most likely to restart. Measured: with a two-minute window, killing
  the server mid-window and starting it again still quarantines the file.

  ⚠ "One pending scan per file" is a new `dedup_key` column on `ops_queue`,
  unique among **pending** rows only — so the key is released the moment a
  worker claims the scan, and a save arriving after that queues a fresh one.
  The asymmetry is deliberate: an extra scan wastes a scan, while a dropped one
  is the bug being fixed. Two saves landing at the same instant produce exactly
  one scan, never zero; the SQL drivers decide it with a guarded insert plus a
  partial unique index, redis with `SET NX`.

- **A Postgres-backed queue could never claim an operation.** `Dequeue` builds
  its candidate SELECT with one placeholder per requested op type, then reused
  that same argument list for the claim UPDATE, which references none of them —
  so Postgres answered `could not determine data type of parameter $1`
  (SQLSTATE 42P18) on every type-filtered dequeue. `queue.Pool` always passes
  its registered handler types, so on `FILEX_QUEUE_DRIVER=postgres` nothing in
  the queue ever ran: no antivirus scans, no content extraction, no async
  copy/move/delete. It survived because no test had ever executed this driver —
  the comment promising integration tests "when `FILEX_TEST_PG_DSN` /
  `FILEX_TEST_REDIS_URL` are set" described tests that did not exist. They do
  now, and they run the same contract against all three drivers.

- **One unhandled op type could stall a Redis-backed queue completely.** Ops
  enter the pending list on the left and are claimed from the right, but a type
  the worker has no handler for was pushed back on the **right** — returning it
  to exactly the position the next claim pops from. The worker re-examined the
  same op until it hit its retry guard and then reported an empty queue, while
  everything queued behind it waited, for as long as that op stayed pending.

- **Delayed queue operations fired at the wrong time on any server not running
  in UTC.** `ops_queue.not_before` is compared in SQL as a string, because
  SQLite has no date type, and the sqlite driver was binding a Go `time.Time`
  — which the SQL driver rendered with an offset and a monotonic-clock suffix
  (`2026-09-06 04:42:59.215071132 +0300 +03 m=+0.118370428`). That does not
  compare with `CURRENT_TIMESTAMP` at all. Measured on this driver: at
  `TZ=Europe/Istanbul` a 100 ms delay was still not runnable 400 ms later,
  while the identical test at `TZ=UTC` passed — which is why nobody had noticed,
  CI runs at UTC, and the one existing test overrode `not_before` rather than
  waiting for it. East of UTC a delayed op sat for the offset in extra hours;
  west of UTC it became runnable immediately, i.e. the delay was silently
  dropped. Times are now written the way SQLite writes them: UTC, second
  resolution. The postgres and redis drivers were always correct (a real
  `timestamptz`, and a ZSET scored by Unix seconds) and are unchanged. This
  affected the queue's retry backoff as well as the new save scans.

- **Half of filex's write surfaces put a file on the storage and told nobody
  it was there.** Reported as "I upload a file and it shows up on screen ten
  minutes later, not straight away". Ten minutes is the storage sync's poll
  interval (900 s by default), and leaning on it was the bug: that sync exists
  to *discover* changes filex did NOT make — an object dropped in a bucket with
  `aws s3 cp`, a file written on the mount by another process. A write filex
  performed itself needs no discovery.

  Measured, not read. Every surface below was driven against a running instance
  with the periodic sync disabled (`sync_mode=ondemand`), then checked three
  ways: is there a node row, is there a Bleve document, does a `change` frame
  reach a subscribed WebSocket. What that turned up:

  - **WebDAV, SFTP, FTPS, NFS and the S3 gateway** — 20 operations between
    them — recorded the row and indexed the file correctly (2-20 ms) and
    emitted **nothing**. `internal/protocolsync`, the package that exists so
    those five protocols share one implementation, had no reference to the
    realtime hub at all.
  - **The AI/MCP surface** (`/api/ai/upload`, MCP `file_write` / `file_mkdir` /
    `file_move` / `file_unzip` / `file_zip`, ShareX, the credential-free upload
    ticket) created the row and neither indexed nor emitted. A file an agent
    wrote was not findable by search and no open explorer was ever told.
  - **`file_delete` was worse than silent**: nothing removed the node from the
    search index, so a file an agent deleted stayed findable for good, under
    its `.filex-trash/…__name` alias, pointing at a path with nothing behind it.
  - **An MCP folder move left its children behind** — only the top row was
    re-homed, so every descendant row and index entry went on pointing at the
    old path, and the listing handed out a path that 404s.
  - **The async ops worker** (copy / move / delete started from the UI) updated
    the row and the index and emitted nothing — so the user who started the
    operation watched their own listing stay wrong.
  - **Archive extract and archive add wrote bytes and did nothing else at all**:
    no row, no index, no event, no frame. The extracted files existed on the
    storage and were invisible to every read path in filex.
  - **Restoring from trash never put the file back in the search index** (delete
    correctly removes it), and **restoring a version left the index holding the
    text of the version that had just been rolled back** — a content search
    returned the file for a phrase it no longer contains and missed the phrase
    it does. That one was not absent but wrong.
  - **Saving from the built-in editor** never re-indexed, so the document kept
    its pre-edit text indefinitely.

  Why the silence mattered more than it looks: an explorer with a live
  WebSocket **does not poll**. The 12 s timer in `useRealtime.ts` is the
  fallback for a socket that has *failed*. Measured in a real browser — a write
  that emitted no frame produced **zero** listing requests in 26 s and never
  appeared; the next announced write produced exactly one request 196 ms later.
  And the periodic sync does not rescue this either: it repairs rows and the
  index and emits nothing, so before this change the only thing that could
  refresh an open folder was the user navigating away and back.

  The fix adds no new mechanism. `internal/protocolsync` — already the one
  place five protocols share — gained the emitter and now announces from
  `Write`, `EnsureDirChain`, `Move`, `Trash` and `Delete`, so all five
  protocols and every future one are covered in one file. The emission is
  deferred, so it fires even on the give-up paths: the bytes are on the storage
  whether or not filex managed to write the row. The AI/MCP surface's private
  cache helpers, which had drifted into a second, worse copy of that package,
  now call into it — which is also what fixes the delete-index leak and the
  folder move, both of which `protocolsync` had solved years earlier.

  **Nothing was slow. Every failure was silence.** Against a 3-second
  acceptance line, all 46 measured operations now land three orders of
  magnitude inside it: the row appears in a listing in 1.4-13 ms, the file
  becomes findable by search in 5-29 ms, and the change frame reaches a
  subscribed client in under 100 ms — usually before the write call has even
  returned. End to end in a real browser, a WebDAV PUT into a folder an
  explorer already had open went from **never appearing** (35.8 s, zero listing
  requests, still absent) to **visible in 224 ms**, of which 200 ms is the
  frontend's deliberate debounce. No timer was shortened to achieve that; the
  numbers were already fast when the announcement existed at all.

- **A search for a file's name could report a hit with an empty index.**
  `/api/files/search` falls back to a SQL `LIKE` over node rows when Bleve
  returns nothing, gated on `storage_id`. That is deliberate and stays — but it
  means a scoped name search proves the *row* exists, not the document. Three
  of the surfaces above looked correctly indexed until they were re-probed
  without `storage_id`. No behaviour change; recorded here because it is the
  reason an earlier pass missed them.

- **Directories created over a protocol stored a non-canonical path.**
  `EnsureDirChain` built `dav/davdir` where every other producer writes
  `/dav/davdir`. `pathkey.Hash` normalizes internally, so lookups, dedupe and
  `parent_id` were all correct and the bug hid — it surfaced only in the string
  `/api/files/search` handed back. The sharp edge was `Move`, which re-homes
  descendants with `strings.TrimPrefix(n.Path, srcClean)`: an unslashed path
  matches no canonical prefix, the trim silently does nothing, and the new path
  comes out doubled.

### Changed

- **`docs/NOTIFICATIONS.md` was describing the notification system filex had
  before webhook v2.** Most visibly it stated there is *"no server-side
  per-event webhook filter"* — the filter has existed for releases and is the
  entire point of the checkboxes above. The whole page was written around a
  single global webhook, so it also said every event produces *exactly one*
  POST (it produces one per destination), that a row is `skipped` whenever
  `FILEX_WEBHOOK_URL` is empty (only when no target matches either), and that
  every file event carries `meta.origin` (only the six write events do). The
  payload table was missing `at`, `node`, `share` and `actor`; the header table
  was missing `X-Filex-Event`, `X-Filex-Delivery` and `X-Filex-Signature`, and
  scoped `Authorization` to every request rather than to the legacy webhook;
  the admin endpoint table did not mention the five `/api/admin/webhooks`
  routes; and both worked examples used `file_dropped`, an event id that was
  renamed to `drop.received`. The per-user `muted_events` / `in_app_enabled`
  fields are now marked for what they are — stored and returned, not yet
  applied by any read path.

- **ClamAV can now be a daemon on the other end of a socket, not just a binary
  on filex's own `$PATH`.** A new **mode** setting picks between `binary`
  (exec `clamdscan`/`clamscan`, as before) and `daemon` (talk clamd's protocol
  over TCP — `clamav:3310` — or a unix socket path). Set it on
  **Settings → Protection**, or seed it from `FILEX_CLAMAV_MODE` /
  `FILEX_CLAMAV_ADDR`.

  This closes a real gap rather than adding a preference. Under Docker, podman
  and Kubernetes, ClamAV is normally its own container (`clamav/clamav`), and
  the filex images ship no scanner — ClamAV plus its signature database is
  close to a gigabyte — so the only previous answer was "build your own image".
  Files are sent with clamd's `INSTREAM`, which streams the bytes down the same
  connection the command went out on, so **filex and clamd need a network route
  and not a shared filesystem**; `SCAN <path>` would have required the daemon
  to see the same file, which is exactly what a container split does not give
  you. It is also why the daemon path never writes a temp file, while the
  binary path still has to.

  ⚠⚠ **An unreachable daemon is visibly unavailable, never silently clean.**
  Every failure — a refused connection, a timeout, a reply filex does not
  recognise, clamd refusing a stream over its own `StreamMaxLength` — returns an
  error, so the queue op fails and retries instead of marking the file scanned.
  The Protection page probes clamd with `PING`/`PONG` and shows `reachable`
  with the error text beside it, because a configured-but-dead daemon would
  otherwise sit behind the same green badge as a working one.
  `GET /api/capabilities` gained `antivirus_mode` for the same reason: two very
  different deployments produce the same `"antivirus": true`.

  `deploy/compose/docker-compose.full.yml` gained a `clamav` service under the
  `clamav` profile, with a volume for the signature database.

- **The antivirus on/off switch moved into the database too**, joining the scan
  ceiling and the save-scan window. There is now a toggle on
  **Settings → Protection** instead of only `FILEX_CLAMAV=0` in compose.

  ⚠⚠ **It takes effect at the next restart, in both directions**, and the UI
  says so at the moment you flip it — turning scanning on does not start it and
  turning it off does not stop it until filex restarts, because the scan
  pipeline is wired once at boot. That is a choice: making "off" immediate
  while "on" stayed deferred would give you a control that is sometimes live
  and sometimes not, with no way to tell which by looking at it. The API
  reports `restart_pending` for as long as the stored configuration differs
  from what the running process booted with, and the page keeps a band up until
  the restart has actually happened. The mode and address settings are deferred
  the same way; the scan ceiling and save-scan window stay live, because they
  tune a pipeline that is already running.

  `FILEX_CLAMAV` is now a **seed** like its siblings: an install that had
  `FILEX_CLAMAV=0` keeps scanning off across the upgrade, and from then on the
  switch is on the Protection page. Before the row exists — the window between
  an upgrade and first-boot seeding — the variable is still honoured, so the
  documented kill switch never stops working mid-upgrade.

- `internal/dbsetting` grew **`BoolSpec`** and **`StringSpec`** beside the
  existing `IntSpec`, so a setting that is a switch or an address is a struct
  literal rather than a hand-rolled reimplementation of resolution order,
  first-boot seeding and validate-on-save. A string setting carries its own
  `Check` hook, because text has no bounds to clamp to: without one,
  `clamav 3310` is accepted at save time and discovered at scan time, by the
  file that did not get scanned.

- **Two antivirus settings moved out of the environment and into the database**,
  where the admin UI can change them without a restart: the **largest file
  scanned** (was `FILEX_CLAMAV_MAX`) and the new **editor save-scan window**.
  Both now have a field on **Settings → Protection**, both are validated when
  you save them rather than silently clamped later, and both take effect on the
  next scan rather than the next restart.

  ⚠⚠ **If you configure antivirus by environment variable, your next change to
  it will silently do nothing.** `FILEX_CLAMAV_MAX` and
  `FILEX_CLAMAV_SAVE_WINDOW_MINUTES` are now **seeds**: they are read on a boot
  where the setting has no stored row, and never again. After that the stored
  value wins, editing your compose file and restarting changes nothing, and no
  warning is printed — because a setting that already has a value is not
  unusual. Use the Protection page.

  Nobody loses their configuration on upgrade. The seed is applied per setting,
  not per family, so a setting whose row does not exist yet is seeded from its
  variable even if its siblings already have rows. `FILEX_CLAMAV_MAX` is in
  bytes while the stored setting is in megabytes — the number an admin types —
  and the conversion rounds **up**, so an upgrade can only leave the ceiling
  the same or very slightly larger, never smaller.

  `FILEX_CLAMAV_BIN` deliberately stays in the environment. The binary is a
  path this server *executes*: an admin-writable field for it would turn an
  admin account into arbitrary command execution as the filex process.
  (`FILEX_CLAMAV`, the kill-switch, has since moved to the database as well —
  see the next entry.)

- A `change` frame now carries the name of the item that changed, from every
  surface. The browser manager's own frames were the only ones that never did,
  so a client keying off `name` — to highlight the row that changed, or to let
  the hub carry a viewer's presence focus across a rename — got nothing from
  the oldest path in the product and something from all the newer ones. A
  single-item move or delete is now named; a batch still is not, because there
  is no one name to give.

- ⚠ The two rooms told about a move now hear **different** names. The source
  hears the name that left and what it became; the destination hears only the
  name that arrived. Sending the destination the source basename named a file
  that is not in that folder.

### Known issues

- **An idempotent delete now broadcasts a refresh for a no-op.** Deleting a
  path with no row and no bytes succeeds (the ops layer reports `ok`, S3
  correctly answers `204`) and now emits a `delete` frame where it previously
  emitted none. This falls out of the deliberate choice to announce on every
  path, and it matters most on S3, where rclone and restic issue speculative
  deletes routinely. Left as is: gating the emission on "a row actually
  changed" would give back exactly the case the deferral exists to protect.

## [0.33.0] - 2026-09-06

### Added

- An i18n key-parity test for the `@brftech/filex-core` catalogue
  (`web/tests/i18n/coreKeys.test.ts`). `web` already had one for its own
  `en.json`/`tr.json`; `packages/core` ships `en.ts`/`tr.ts` but has no test
  runner of its own, so the gate lives in `web`, which depends on the package.
  It also fails on an "English" value that is still Turkish — the shape the
  bug above would take once it wears a key.
- **Almost every absolute URL a multi-tenant install handed out was the
  operator's hostname, not the customer's.** Exactly one place in the codebase
  derived the origin per request — `Auth.redirectBase`, written for the OIDC
  callback and never reused. Everything else concatenated the global
  `FILEX_PUBLIC_URL`: the `/s/` and `/d/` links a tenant sends to their own
  clients, the `wss://…/api/ws` endpoint handed to the browser and opened
  verbatim, the `/s/` and `/u/` URLs returned to AI / MCP / ShareX clients, and
  four e-mails. The worst was the account-created mail: a customer's brand-new
  user received a temporary password next to a link to **someone else's** login
  page — a flow that cannot succeed, and a disclosure of the operator's
  hostname to every tenant that ever adds a user.

  The rule now lives in one place, `internal/tenanturl`, and every builder
  calls it. It has two entry points because not every URL is minted while a
  browser waits: `FromRequest` for the request-driven sites, and
  `ForStorage` / `ForProvider` for the ones reached only through a context (the
  AI/MCP share link and the upload ticket), where the tenant is taken from the
  node's storage rather than from a `Host` header that isn't there.

  **Host-header injection**: the request host is resolved with
  `GetProviderByHost`, which matches an *enabled* provider row exactly, and the
  origin is then assembled from that row's own `host` column — never from the
  request string. An unknown, disabled or absent host falls back to
  `PublicURL`, so `Host: evil.example` mints the operator's configured URL and
  `evil.example` never reaches a link or an e-mail.

  **Single-tenant installs are unchanged.** With `FILEX_MULTI_TENANT` off the
  resolver does not read the request at all (asserted, not assumed: the test
  counts store lookups and requires zero), so the call sites that have no
  request in scope lose nothing. Covered by a new multi-tenant suite that
  drives the real handlers — and, for the four e-mails, a real in-process SMTP
  server, so the assertion is on the bytes a recipient receives.- **The public export's link check had never run once.** `scripts/export-public.sh`
  ends by running `scripts/check-links.mjs` over the tree it just built and
  refusing the export if anything is dead — the guard added on 2026-09-05 after
  two dead links sat in the public README. It was wrapped in
  `if command -v node; else echo "node not found: skipped…"; fi`, and on the
  maintainer's machine it took the `else` branch **every time**: the script is
  invoked as `wsl bash -lc 'bash scripts/export-public.sh …'`, and that WSL had
  nvm and three node versions installed but `node` on no shell's PATH — the
  interactive guard in `~/.bashrc` returns before the nvm block for
  non-interactive shells, and for interactive ones a hard-coded `export PATH=`
  two lines later wiped what nvm had just added. The export succeeded, printed
  its own excuse, and nobody read the line.

  A guard that degrades to nothing is not a guard, so **no node is now fatal**,
  and the script looks for one before giving up: `PATH`, then the newest
  `$NVM_DIR/versions/node/*/bin/node`, then — under WSL — the Windows
  `node.exe`. ⚠ That last one is a *Windows* binary: handed `/mnt/g/…` it
  resolves the path against the current drive and dies with
  `Cannot find module 'G:\mnt\c\…'`, so both the checker and the tree are
  converted with `wslpath -w` first. All three branches were exercised:
  nvm node → `410 relative links … all resolve`; Windows node → the same 410;
  no node at all → exit 1 with a refusal. A deliberately dead link
  (`README.md` → `docs/MIGRATION.md`, which the export withholds) is refused
  with exit 1.

### Fixed

- **The admin UI named a private repository, and every published screenshot
  showed it.** The footer and the About page linked
  `github.com/brf-tech/filex` — the internal development repo, which
  answers 404 to anybody who is not us. `scripts/export-public.sh` rewrites that
  string on the way out, so the *shipped source* was right; a **screenshot** is
  a PNG and no rewrite reaches inside one, so the images in the public README
  showed the dead URL. They now name `github.com/BRF-Tech/filex`, which is where
  the product actually lives — including on our own installs, where a link to a
  repository the reader cannot open was never useful either.

- **Uploads over 8 MiB never reached an S3 storage over plain HTTP, and the
  sync then moved them to trash** (GitHub #16). Reported against a fresh Garage
  deployment: every upload looked like it worked, the bucket stayed empty, and
  the next storage sync put the files in the trash. Files under a few MB were
  fine. Reproduced end to end against a real Garage instance.

  Two independent defects, and the second is the one that cost the user data.

  **1. The commit could not sign a multipart part.** The browser posts files
  under 8 MiB in one request; above that they take the staged path, where the
  commit re-chunks the staging area into a driver multipart upload. Each part
  was cut with `io.LimitReader`, which drops the `Seek` method the staging
  reader has. SigV4 signs the SHA256 of the payload, so the SDK reads a part to
  hash it and then rewinds to send it — with nothing to rewind the request died
  inside the client, before a byte left the process:
  `upload part 1: operation error S3: UploadPart, failed to compute payload
  hash: failed to seek body to start, request stream is not seekable`.

  ⚠ It is the SCHEME that decides this, not the provider — measured, because
  the obvious guesses are wrong. Over `https://` the SDK sends
  `x-amz-content-sha256: UNSIGNED-PAYLOAD`: TLS already protects the body, it is
  never hashed, and a plain reader has always worked. Over `http://` the payload
  hash is what binds the body to the signature, so the body must be read twice.
  Every S3 endpoint filex had previously been pointed at is `https://`; the
  reporter's Garage, a container on a podman network, is not. The bug was
  waiting for the first plaintext endpoint, and it would have hit AWS, MinIO or
  Hetzner over plaintext just the same.

  Parts are now cut with a rewindable, length-bounded window over the staging
  reader, and `UploadPart` on the S3 driver makes any body rewindable before it
  hands it to the SDK — small ones in memory, large ones through a temp file,
  an already-seekable one passed through untouched. The driver's signature
  promises `io.Reader` and now honours it, instead of silently requiring a
  `Seeker` and failing with a message that names the SDK rather than the call.

  **2. A failed upload was silent.** The commit endpoint answers `202` as soon
  as the last chunk lands; the transfer happens afterwards in the ops worker. A
  transfer that failed there produced one `WARN` line in the server log and
  nothing else — the node stayed at `transfer_state="staged"`, which is
  indistinguishable from still-in-flight, no notification fired, and the browser
  had already drawn a finished upload. The client now waits for the transfer by
  default (the `transferring` phase, which was implemented but which no caller
  ever switched on) and reports the failure; the node moves to a new
  `transfer_state="failed"`; and a `file.upload_failed` notification, carrying
  the reason, goes to whoever uploaded.

  ⚠ While turning that wait on, a latent bug in it surfaced: it told a real
  verdict from an incidental fetch error by comparing the message to the literal
  string `"transfer failed"`, which only matches when the server sends no error
  text. Any real error message was swallowed and the poll span for ever. The two
  are now told apart by type, and a run of unreadable polls ends the wait
  instead of hanging.

- **Uploading a file over an existing one destroyed the old bytes, and nothing
  kept a copy anywhere.** `versioning.Service.Snapshot` documents its own
  contract — *"callers should invoke this BEFORE a destructive write… if the
  snapshot itself fails the caller should NOT proceed"* — and the package doc
  stated as fact that snapshots were taken on upload finalize and archive
  extract. Neither was true. Across the whole product `Snapshot` was reached
  from exactly two places: `save-text` and the versions endpoints. No upload,
  drop, ShareX, AI/MCP write, archive extract, OnlyOffice save-back, WebDAV
  `PUT` or S3 gateway write ever called it. Measured on a clean instance:
  uploading `up.txt` twice left `GET /api/files/versions?node_id=1` answering
  `{"versions":null}` with no `.versions/` tree on disk at all, while editing
  the *same* file in the browser recorded one — so version history looked like
  it worked right until the moment you needed it.

  Every destructive write now calls `writehook.BeforeOverwrite` first, which
  snapshots whatever is about to be replaced and **refuses the write** if that
  snapshot cannot be taken: 503 with `"code": "SNAPSHOT_FAILED"`, existing file
  untouched. The covered surfaces are the browser upload (single-POST and
  staged), the public drop link, the ticketed upload, the legacy presigned
  multipart finalize, ShareX, the AI/REST and MCP write/zip/unzip tools,
  archive extract and add, OnlyOffice's save-back, WebDAV `PUT`, and the S3
  gateway's `PutObject`, `CompleteMultipartUpload` and `CopyObject`.

  ⚠ On the staged path the guard runs at **commit**, not at the driver write.
  Two reasons, and both are load-bearing. Publishing the staged node flips it
  to `transfer_state="staged"`, after which `filebody` answers with the
  *incoming* bytes — a snapshot taken later would record the wrong content.
  And `transfer()` picks between two write mechanisms: on any driver
  implementing `storage.PartUploader` — i.e. S3, which is what real
  deployments run — it calls `streamMultipart` and never touches
  `storage.Writer.Write` at all. A guard hung off the driver write would have
  protected small uploads and silently skipped every large one on exactly the
  backend where it matters most. There is a test pinning the S3-shaped case
  that asserts the object really did arrive via `CompleteMultipart`.

  `save-text` is brought under the same rule rather than left as the
  exception: it used to log `snapshot failed (continuing with write)` and
  overwrite anyway, directly contradicting the contract quoted two lines above
  its own call.

  Archive extract and the AI/MCP `unzip` tool differ deliberately: they skip
  just the refused member and keep going, reporting a `refused` count distinct
  from the permanent, user-caused skips in the same loop (a zip-slip entry, a
  file/folder kind clash). If every member was refused they answer 503 rather
  than a misleading `200 {"count":0}`.

  `FILEX_VERSIONS_ON_OVERWRITE=0` turns the guard off for a deployment whose
  storage cannot afford the extra write; `FILEX_VERSIONS_FAIL_OPEN=1` keeps
  attempting the snapshot but lets a failed one through. Both default to the
  safe value and log a WARN at boot when they are not at it — the fail-closed
  default has a sharp edge worth naming, because if the object store fills up
  then every overwrite on the instance is refused until an operator changes an
  env var and restarts.

- **The S3 gateway exposed filex's own internal trees.** `.versions/`,
  `.thumbs/` and `.filex-trash/` were listable AND readable by known key
  through `/s3` — measured, `GET .versions/42/1` answered 200 — while `/dav`,
  `/sftp`, `/ftp`, `/nfs` and the browser listing had all hidden them since
  they were written. They are now refused at any depth on listing, read and
  write. This was always wrong and became urgent with the guard above: before
  it, `.versions/` held a handful of text-editor snapshots; after it, it holds
  a copy of every file any surface has ever replaced, so leaving it reachable
  would hand any S3-key holder the prior contents of files whose folders they
  may since have lost access to.

- **A failed upload named neither the file nor the reason.** The access log
  said only `method=POST path=/api/files/manager status=500`, and no
  write-failure branch logged a filename, so "which file failed yesterday
  afternoon" had no server-side answer. Both the browser upload path and the
  staged transfer — whichever of its two driver write mechanisms fails — now
  log `msg="upload failed"` with `storage`, `path`, `name`, `size` and
  `reason`.

- **`POST /api/files/archive/add` leaked a file descriptor on every error
  path.** Only the success path closed the temp file it built the archive in;
  each of the early returns dropped it. Closed with a `defer`, registered
  after the `defer os.Remove` so the two unwind in the right order.

- **filex spoke Turkish to English users, and no locale setting could stop
  it.** ~45 strings across the explorer, the viewers, the admin UI and the
  backend were hard-coded Turkish literals sitting *beside* the translation
  layer, not inside it. They are now keys in `en`/`tr` and go through `t()`.

  The widest one was the whole **convert modal**. It resolved its labels
  through a private `tt(key, fallback)` helper backed by an optional `t?` prop
  — and three separate things had to be true for it to ever produce English:
  the caller had to pass `t` (`FileExplorer.vue` never did), the `convert.*`
  keys had to exist (they existed in *neither* catalogue), and the prop's
  signature had to match `useLocale`'s (it did not — it declared
  `(key, fallback)` where the real `t` is `(key, vars)`, so passing the real
  one would have rendered the raw key, or spliced the fallback's characters in
  as `{0}`, `{1}` … substitutions). Every user in every locale read Turkish.
  The helper is gone: the modal now takes `locale` and calls `useLocale`, like
  every sibling modal, and its ten labels are real keys.

  Also: ~20 explorer toasts and the drag-and-drop overlay
  (`İşlem başarısız`, `Kopyalandı` / `Taşındı` / `Silindi`, `Kesildi`,
  `Aynı klasöre kesilemez`, `… kuyruğa alındı`, `… öğe geri getirildi`, the
  trash-retention notice, `Dosyaları buraya bırak`); the new-folder validation
  error; the presence bar's people count; the CSV viewer's row count; five
  strings in the preview modal (two of them sitting next to a correct
  `{{ t('viewer.download') }}`); and the SMTP password placeholder in
  `Settings.vue`, whose `label` and `hint` on the same line were already
  translated.

  Where a key already said the same thing it was reused rather than
  duplicated — the sharpest case being the paste toast, which was
  `t('split.cross_copy')` on the cross-storage branch and a Turkish literal on
  the same-storage branch of the *same expression*, while
  `split.copy_queued` ('Copy queued' / 'Kopyalama kuyruğa alındı') already
  existed and was exactly it.

- **A file drop notified the folder's owner in Turkish regardless of their
  language.** `Drop.notifyOwner` never consulted a locale, and its output is
  not confined to one surface: the same title and body go to the in-app
  notification bell, into the **webhook v2 `drop.received` payload** and into
  the owner's **e-mail**. The `Gönderen:` header written into the persisted
  `NOT.txt` beside the uploaded files had the same problem.

  These now follow the same `mailLangEN` selection `mail_templates.go` next
  door already used. The locale is the **folder owner's** (`users.locale`, via
  the new `Drop.ownerLocale`), not the uploader's: a drop link is opened by an
  anonymous visitor who has no account and therefore no locale, and the owner
  is the only person who reads any of it.

- The admin panel's SMTP **Test** mail sent a Turkish sentence followed by an
  English one to whatever address was typed. It now follows the acting
  admin's locale.

- `DrawioViewer`'s untranslated fallbacks were Turkish where every sibling
  fallback (e.g. `CsvViewer`'s `'Loading…'`) is English. The fallback is what
  an embedder who does not pass `t` actually reads.

- `desktop`: the drag-out diagnostic trail logged `'BULUNAMADI'` where every
  neighbouring `dragLog` value is English. This one is **not** a translation
  key — it is a developer log, and it is now `'not found'`.

### Changed

- **The storage sync no longer moves a file to trash just because the object is
  missing** (GitHub #16). This is a behaviour change, and it is the part that made the
  bug above data-shaped rather than merely broken: the upload was what failed,
  but the sync is what deleted the user's file.

  "The catalogue has a node and the backend does not list it" is not proof that
  anyone deleted anything, and it will keep happening for reasons that have
  nothing to do with deletion — a failed upload, a permissions change that hides
  a prefix, a driver that pages a listing badly, an object restored out of band.
  A sync run that answers all of those with "trash it" is a sync run that turns
  someone else's bug into lost data. The tombstone pass now has to be *right*
  about the deletion, which takes two questions:

  - **Did filex ever put the bytes there?** A node whose `transfer_state` is not
    `stored` has never been confirmed on the backend. Its absence is the
    *expected* state, not evidence, and it is never a reason to trash it. This
    also closes a race that existed even when everything worked: a sync landing
    between publishing the node and finishing the transfer would trash a
    perfectly healthy upload.
  - **Is the object really not there?** For the remaining candidates the driver
    is asked directly with `Stat`. Only a definite not-found counts as a
    deletion; an object that the listing missed but `Stat` can see is kept, and
    so is one we could not check at all, because "I could not check" must never
    read as "it is gone".

  A file genuinely deleted in the bucket still goes to trash, so deletions made
  outside filex are still reflected — the guard is about certainty, not about
  never deleting. The existing 30 %-drop guard is unchanged and still runs
  first; `Stat` only runs for nodes that survive it, so a healthy sync costs
  nothing extra.

- **A storage that has just been synced no longer reads "Never ran"** (GitHub #16).
  Two separate breaks, both on the way to the screen. `GET /api/admin/storages`
  never sent `last_sync_state` or `last_sync_error` — the admin UI has always
  read them and they exist only as the storage's last `sync_runs` row, so the
  badge fell through to its "Never ran" default no matter how many runs had
  succeeded. And the store's `syncNow` wrote an optimistic `running` and never
  refetched; since the endpoint runs the sync synchronously, that optimistic
  flip was the last thing written and the row stayed stale until a full page
  reload. The endpoint now sends both fields, and the store refreshes after a
  sync — including after a failed one, so a failure shows its state instead of
  spinning.

- **Source comments are in English.** filex was written as a private codebase, and
  a large part of its commentary — the parts that explain *why* a thing is the way
  it is, usually with a measurement or an incident behind it — was in Turkish.
  Anybody reading the code to contribute had to read those. They are now English,
  with the reasoning, the tone and the `⚠` / `⚠⚠` / `⭐` markers carried over
  rather than flattened into one-line summaries.

  Nothing that is Turkish *on purpose* was touched: the `tr` locale catalogues and
  mail templates (the product speaking Turkish to Turkish users), the Turkish
  filenames and file contents used precisely because they are non-ASCII test
  fixtures, the selectors that assert against the Turkish UI, and UI labels quoted
  inside a comment. Comments only — no code moved, and no behaviour changed.

### Documentation

- **What the site's slug-rule change actually cost, measured.** v0.32.0 moved
  docs.filex.sh onto GitHub's heading-id rule, and noted that deep links into
  headings containing `&`, `/`, an em dash, a dot, an apostrophe or a leading
  digit would change spelling. That warning is now a number: the site was built
  at both commits and the emitted `id=` attributes diffed page by page —
  **245 of 666 headings changed spelling, none disappeared**. Roughly 65 are on
  pages a stranger would deep-link (INSTALLATION, CONFIGURATION, STORAGE, MCP,
  SSO, LDAP, the docs index); the rest are `BACKEND.md`'s per-endpoint reference
  and the generated `RELEASES.md`.

  No `<a id>` aliases were added, and the reasoning is written down beside the
  anchor check in `docs/CONTRIBUTING.md` and pointed at from
  `docs-site/.vitepress/github-slug.mjs`: the old spellings existed only on the
  site and only for the seven weeks it used VitePress's rule, every link written
  against the GitHub rendering was already correct, an unmatched fragment lands
  the reader at the top of the right page rather than a 404 — and ⚠ a fragment
  is never sent to the server, so no redirect of any kind could have rescued one
  anyway.

## [0.32.0] - 2026-09-05

### Added

- **`uiProfile: 'drive'` — the explorer's shell, laid out the way an end user
  expects (GitHub #14).** The reporter of #14 came back to v0.30.1 with four
  annotated mockups: the navigation panel was right, and here is what the rest
  could look like. This is that, as a third **profile** of the same explorer —
  not a second UI. It is a strict superset of `simple`, so everything `simple`
  turns off stays off, and a non-admin signing in at `/drive` now lands on it.

  - **One primary "+ New" menu** in the panel, in place of the Upload / New
    folder pair: upload files · new folder · request files (the file-drop link,
    which opens the access modal straight on its own tab).
  - **One search field across the header**, with a ⌘K / Ctrl+K chip. The field
    searches the folder you are standing in — it sets the same `searchQuery`
    the toolbar box always did — and the chip (or the shortcut, pressed from
    inside the field) hands that query to the **command palette**, which is
    where "everywhere", saved searches and the commands live. One box, one
    shortcut, no third search surface.
  - **A filter row** under the breadcrumb: **Type · Modified · Size**.
  - **Folders and Files as labelled sections** in grid view, with the rows
    grouped rather than trusted to arrive grouped.
  - **The details panel split into Details and Activity**, with **People with
    access** (real grants from `GET /api/files/permissions`, hidden rather than
    faked when the server refuses them) and a **share-link row with a Create
    link button**. Activity is version history + comments.
  - **A storage line** under the navigation, from `GET /api/files/quota/me`.

  Nothing is deleted: density, theme, the shortcut editor, the tour and the
  other view modes moved into the header's "⋯" menu, and the view switcher and
  details toggle onto the breadcrumb row. Both locales, light and dark, a rail
  at 56px and a drawer under 560px.

  ⚠ **No People filter and no Owner column**, though the mockups draw both. A
  listing row carries no owner — `nodes.owner_id` is quota bookkeeping, nil for
  anything a sync discovered, and serialized by nothing — and the listing
  endpoint reads no owner parameter. The three chips that shipped are answered
  from fields every row already has; a fourth would have opened, offered names
  and changed nothing.

- **A Windows copy that runs without being installed.** Linux and macOS already
  had one — the AppImage runs unextracted, the mac `.zip` is unzip-and-run —
  and Windows shipped only an NSIS installer, so a locked-down work machine, a
  USB stick, or "I just want to look at my files on this laptop for ten
  minutes" had no answer at all. `filex-desktop-portable-x64.exe` is one file:
  put it anywhere and double-click it.

  **It keeps its data beside itself, not in `%APPDATA%`.** For an installed app
  the roaming profile is right; nobody wants a program scattering folders
  across their desktop. A run-and-delete copy is the opposite case, and what it
  promises is that deleting one `filex-data` folder leaves nothing of yours on
  a machine that is not yours. That covers the account store, settings, the
  log, the drag-out cache, open-with working copies — and the sync engine's
  bookkeeping, which needed the new **`FILEX_SYNC_DIR`** in the CLI: its store
  defaults to `~/.filex/sync` and holds the local trash, i.e. real copies of
  files it deleted, in somebody else's home directory. *Settings* shows the
  exact path with a button that opens it, because a promise the user cannot
  verify is only a claim.

  ⚠ **It does not update itself, and says so.** A portable `.exe` is not an
  installation: there is no install directory to patch and no installer to hand
  the running copy over to. So it takes the route the unsigned macOS build
  already established — the updater is never wired at all, nothing is
  downloaded that could not be applied, and *Settings* reports a new version
  with a **Download** button instead of sitting at "Checking…" forever. Its
  own words, not the macOS ones: updating means putting a new `.exe` over the
  old one, and the folder beside it keeps your accounts.

  ⚠⚠ **If the `.exe` sits somewhere it cannot write** — `C:\Program Files`, a
  read-only stick, a share — it falls back to
  `%APPDATA%\@brftech\filex-desktop-portable` and says so, with the path. The
  fallback is deliberately *not* the ordinary user-data directory: measured
  before it was fixed, that put the portable copy straight into the **installed
  app's own profile** — a copy carried in from outside reading and writing the
  accounts of whoever owns the machine, and, because the single-instance lock
  is keyed on that directory, exiting silently and raising the installed app's
  window whenever it was already running.

  ⚠ **Your accounts do not travel between machines.** Tokens are sealed with
  the machine's own keychain (Windows DPAPI), so a `filex-data` folder opened
  on another computer, or under another Windows account, fails to decrypt and
  you sign in again. That is the right way round for a stick left on a train.

  `scripts/portable-e2e.mjs` covers it, because a package that *opens* is not a
  package that *works* and "portable" is a claim about where files go that no
  packaging step checks: it launches the real self-extracting `.exe` and waits
  for a renderer to actually load a page, checks that the only things left in
  the folder are the `.exe` and `filex-data`, and asserts the artifact is named
  so it neither overwrites the installer (both targets emit `.exe`) nor carries
  a version (the download links resolve one fixed filename).

- **An existing installation can adopt key escrow.** v0.31.0 pinned
  `FILEX_INSTALLATION_E2E_ESCROW_KEY` at the first boot and refused *every*
  later change — including "no escrow" → "escrow". That read well and measured
  badly: both of our own deployments came out of the rollout with
  `"e2e_escrow_kid": ""`, so acting on the decision to turn escrow on meant
  discarding the data directory. And the general case is worse than ours: a
  product where escrow can only be chosen in the first second of an
  installation's life is a product where nobody chooses it, because nobody
  decides key-escrow policy before they have any files.

  Setting `FILEX_INSTALLATION_E2E_ESCROW_ADOPT=1` alongside the key adopts it,
  once. The flag is read only while the pinned record has no escrow key, so it
  is inert afterwards and can be dropped on the next deploy.

  ⚠⚠ **Adoption is not retroactive and cannot be made so.** A folder wraps its
  master key to the escrow identity when it is created; a folder that already
  exists carries no such copy, and writing one needs the folder password, which
  the server has never had. Folders older than the adoption are outside the
  escrow key permanently. filex says this in the refusal you get without the
  flag, in a WARN line on the boot that adopts, in the record, and in the
  unlock dialog of every affected folder — which now explains the missing
  Escrow tab instead of just not having one.

  The record keeps the two dates apart, because "when was this installed?" and
  "when did it gain a second key?" are different questions:
  `pinned_at` / `pinned_by` still describe the first boot, and
  `e2e_escrow_adopted_at` / `e2e_escrow_adopted_by` /
  `e2e_escrow_adoption_note` describe the adoption. `e2e_escrow_adopted_at` is
  the boundary an operator compares a folder's age against.

  ⚠ Still refused, flag or no flag: pointing the key at a **different** value,
  and **removing** it. Those two leave folders behind that the running
  configuration can no longer describe; adoption leaves nothing behind.

- **`scripts/check-doc-anchors.mjs` — a dead `#section` now fails the build.**
  `vitepress build` fails on a dead *page* link and says nothing whatsoever
  about the anchor half, so the green docs build the release process leans on
  was evidence that every page exists and evidence of nothing else. The new
  check resolves every in-page link in `README.md`, `CHANGELOG.md`, `docs/`,
  `desktop/README.md` and the npm package READMEs, and exits non-zero listing
  the ones that land nowhere.

  ⚠ It reads the heading ids out of the **built HTML** — and, for the pages the
  site does not build, out of VitePress's own `createMarkdownRenderer` loaded
  with this site's markdown options. It does not re-derive the slug rule. A
  second implementation drifts from the real one and starts reporting links
  that work, and a check that cries wolf gets deleted. The two paths are
  cross-checked against each other on every page that has both, so if they ever
  disagree the check exits 2 instead of answering confidently.

  Wired into the `lint` stage as `lint:docs`, which builds the site once and
  runs both gates against that build — the docs site had not been built in CI
  at all until now — and into `docs/CONTRIBUTING.md` → *Release process* step 3
  as a fourth mandatory closing command.

- **An existing encrypted folder can be given an escrow slot — by its owner.**
  Adopting escrow covers folders created after the adoption, which on an
  installation that has been running for a while is nobody's folders: the ones
  that matter already exist. An escrow key that reaches none of them is a key
  to an empty room.

  So when the installation has an escrow key and a folder does not, filex asks
  the folder's **owner**, at the one moment the folder password exists in a
  browser — immediately after a successful unlock. The notice says what
  accepting actually means rather than "enable escrow?": the operator gains a
  second, permanent way in, without the password, and the use-notification is
  an announcement rather than a control. It links to
  `docs/E2E-ENCRYPTION.md`, which says the same thing at length.

  Accepting rewrites only `.filex-e2e.json`. **No file is re-encrypted, moved
  or rewritten**, and the password and recovery key keep working unchanged.
  Doing nothing leaves the folder exactly as it was.

  Declining is a decision, not a delay: it is recorded in the folder's marker
  (`esc_declined`) and filex stops asking — a question that returns on every
  unlock is how people learn to click past security dialogs. The record lives
  in the marker rather than in browser storage because the unit of the decision
  is the folder: the same person on their phone is not asked again, and an
  answer that vanished with a cleared cache would be no answer. It travels with
  the folder through a move, a backup and a restore, exactly as the key slots
  do, and holds no key material.

  The way back for somebody who changes their mind is **Escrow key…** in the
  strip above an unlocked folder. It asks for the password again — the same
  proof of ownership the offer at unlock had.

  ⚠ This changes nothing about what the **operator** can do, and the threat
  model is unchanged: adding a slot needs the folder master key, which needs a
  credential the server has never held. No configuration change, admin action
  or future version reaches an existing folder. The door opens from the inside
  only. `e2e_escrow_adopted_at` keeps its meaning — the boundary of
  *automatic* coverage — and a folder whose owner granted a slot is simply no
  longer described by it.

  ⚠ The offer never appears where it could not be honoured: no installation
  key, an existing slot, a failed unlock, or a v1 marker (whose path is the
  recovery upgrade, which seals an escrow slot in the same step and discloses
  it in the same prompt).

### Fixed

- Three surfaces said an escrow key could **never** open a folder created
  before escrow — the locked-folder dialog ("today or ever"), the
  `e2e_escrow_adoption_note` the server writes into `installation.json`, and
  `docs/CONFIGURATION.md`. That was true of the operator and false of the
  folder, and it is exactly the shape of wrong information that reads as
  authoritative. All three now separate the two: no operator action reaches an
  existing folder, and its owner can grant one.

- **The backend spoke Turkish to every user, once.** The `file.infected`
  notification carried a Turkish title while all eleven of its siblings were
  English — the one message a person reads at the worst possible moment, when
  something on their server has been flagged as malware. `docs/PROTECTION.md`
  documented the Turkish string faithfully, so the page was honest and the
  product was the defect.

- **A page withheld from the public repository was published to the world, and
  the public README pointed at a file that is not there.** `docs/MIGRATION.md`
  is a migration guide for an in-tree package that was never public; the export
  script strips it. It was not in `srcExclude`, so the docs site built it, the
  sidebar linked it and search indexed it — while the *published* README and
  docs index linked a file the published tree does not contain. Neither half was
  catchable: the release process checks links in the source tree, where every
  file exists by definition, and the tree that actually ships had never been
  checked at all. `scripts/check-links.mjs` now checks it, the export script
  runs it on what it just built and refuses to call the export good if it fails.

- **A search stopped answering with a folder called Trash.** The explorer draws
  a virtual `.trash` row at a storage root, and it decided where to draw it from
  the listing's `dirname`. A search answers with the *scope* it searched, so
  `?action=search&path=main://&filter=notes` comes back carrying
  `dirname: "main://"` — byte-identical to the folder listing of that same path.
  The rule could not tell the two apart, so searching a storage root answered
  with the hits **plus** a 0-byte folder named Trash that is not a folder, is
  not a hit, and cannot be searched for.

  Measured rather than guessed: the server sends no `.trash` row for either
  action, and `isStorageRootDir` is true for both responses — the row was always
  the client's, and only the call site knows which of the two it just asked for.
  `injectTrashRow` now takes that answer explicitly. Same family as the
  `.trash` / `.shared` sentinel bugs fixed earlier in this cycle: a virtual row
  rendered somewhere it has no meaning.

  Found in a screenshot, which is the other half of the story — the picture the
  README was going to ship for `uiProfile: 'drive'` had been taken mid-search,
  so it showed the phantom next to the one real hit.

- **Search no longer typo-matches numbers.** v0.30.0's fuzzy pass applied to
  every query word, so `2026` returned `annual report 2025.docx` — ranked
  second — because one digit is one edit. All-digit words are now matched
  literally: a near-miss digit is a different year, invoice or order id, not a
  misspelling of the same one. Exact, prefix, substring and separator-blind
  matching on numbers are unchanged (`2026` still finds `invoice_2026.pdf` and
  `Budget-2026.csv`), mixed words like `v2024x` stay typo-tolerant, and a real
  word typo (`mian.go` → `main.go`) still resolves.

- The unlock-without-the-password dialog labelled the escrow field with the
  **installation's** key id whatever the folder's own escrow slot said, so a
  folder restored from another installation looked openable with a key that
  cannot open it. It now reads the folder's `kid` and names the case.

- **A phone scrolled sideways on the file explorer.** `/admin/explore` and
  `/drive/explore` measured 398 CSS pixels wide inside a 390-pixel viewport —
  the width of an iPhone 12/13/14, and the screen every non-admin lands on. The
  8 pixels were the "Admin panel" *label* in a row that is otherwise icon
  buttons; below `sm` the button keeps its icon and its accessible name and
  drops the text. The label, not the button, is what goes, because the same
  string is longer in every other language filex speaks.

- **The desktop app's pairing never finished for an admin who signed in with a
  password.** The browser half of the hand-off only ran when the app booted, so
  it caught the routes that reload the document — the OIDC callback, and the
  navigation to `/drive/explore` that a non-admin gets — and missed the one that
  does not: an admin's password login stays inside the SPA, nothing remounts,
  and the desktop app sat on its waiting screen until it timed out. The
  hand-off now runs when a session appears, whichever way it appeared. The
  pairing is still minted exactly once.

- **`filex serve` sent no `Content-Type` for `.webmanifest`.** It fell through
  to `http.DetectContentType`, which sees JSON text and answers
  `text/plain; charset=utf-8` — on every platform, not just Windows. It is now
  `application/manifest+json`, said explicitly. A *missing* manifest also 404s
  instead of being served `index.html` by the SPA fallback, which is the break
  that leaves an app un-installable while every status code says fine.

- **"Test connection" took half a minute on an unreachable endpoint.** The S3
  driver retries six times with a backoff capped at ten seconds so that a
  *sync run* rides out an object store's transient 503; run from a button, those
  attempts took a measured 23.6s and 29.7s before answering. The retry budget is
  right where it lives, so the probe got a deadline instead
  (`handlers.ProbeTimeout`, 10s) — which bounds a hung SFTP or WebDAV dial too —
  and a probe that runs out of time now says so rather than showing the SDK's
  paragraph about attempt counts. A local-path probe was and remains ~6ms.

- **A failed login told the server as little as it told the caller.**
  `local.Driver.Login` folded a wrong password, an unknown account, an account
  with no local password and *any error from the user lookup* into one
  `401 invalid credentials`. The answer is unchanged — a single unhelpful 401 is
  what stops an attacker enumerating accounts — but each case now names itself
  in the server's log, and a store failure is no longer reported as
  `ErrUnauthorized`, so `auth.LoginChain` can tell "wrong password" from "the
  directory is down". The two-factor refusals log their reason too.

- **98 of 366 in-page documentation links pointed at nothing on docs.filex.sh.**
  Every table of contents at the top of a `docs/*.md` page, the README's link
  into `MCP.md`, both npm package READMEs — anything whose target heading held
  an `&`, a `/`, an em dash, a dot, an apostrophe or a leading number.

  The links were not sloppy. They were **correct GitHub anchors**: these pages
  are read on two surfaces, and GitHub's slug rule is not VitePress's.
  `## Backup & restore` is `#backup--restore` on GitHub and was
  `#backup-restore` here; a heading reading *Token kinds — `user` vs `app`* is
  `#token-kinds--user-vs-app` there and was `#token-kinds-—-user-vs-app` here,
  em dash and all. Rewriting the 98 links to VitePress's ids would only have
  moved the breakage onto GitHub, so the site's slug rule was changed to
  GitHub's instead (`docs-site/.vitepress/github-slug.mjs`, measured against
  `api.github.com/markdown` and pinned by `web/tests/docs/anchorSlug.test.ts`).
  One rule, both surfaces — which is also what lets a single check guard both.

  ⚠ Anchors on docs.filex.sh that contained one of those characters have
  changed. An external deep link into a section whose heading holds an `&` or
  an em dash now needs the GitHub spelling, which is the one the docs
  themselves use.

- **Five headings carried a non-breaking hyphen, and the anchors into them were
  dead on both surfaces.** `## Read‑only mounts` in `STORAGE.md`, `Per‑path
  modes` and `3. Rules — per‑path modes` in `REPLICATION.md`, `S3 / S3‑compatible`
  in `STORAGE.md`, `Boot‑time backfill` in `thumbnails.md`. U+2011 is not a
  hyphen to either slugger: GitHub deletes it (`#readonly-mounts`) and VitePress
  kept it verbatim, so `#read-only-mounts` — the only spelling anyone would type,
  and the one three links used — matched neither. Invisible in every editor;
  found by comparing bytes, not appearances.

### Changed

- **The explorer no longer draws a Trash folder in listings when the navigation
  panel is already offering one.** The virtual `.trash` row exists for one
  reason: a listing with no other way into the bin. Once the panel carries a
  Trash entry that reason is gone, and what is left is a second door to the same
  place — a 0-byte row that is not a folder, sitting among real ones, while the
  panel's own Trash entry is a few inches to its left.

  The rule is about the panel, not the profile: the duplication is exactly as
  wrong in `standard` with the panel on as it is in `drive`. `trashVisible:
  false` still means no Trash anywhere, and where there is **no** panel —
  `sideNav: false`, or the default under `rootPath` — the row is unchanged,
  because that is the case it was invented for.

  Decided per panel *availability*, not per panel *visibility*: collapsed to the
  icon rail still counts (the entry is still there, one click, still labelled),
  and so does a closed drawer under 560px (the panel toggle is on screen at
  every width). Keying it to what is painted right now would add and remove a
  row from the listing every time somebody opened the drawer.

  ⚠ **Behaviour change for existing embeds.** An embed that mounts the panel —
  the default — will stop seeing the Trash row in its listings. That is the
  intent, and the destination is unchanged: the panel's Trash entry goes to the
  same place. An embed that wants the row back turns the panel off, and one that
  wants no Trash at all still sets `trashVisible: false`.

- **Two CI gates had been failing without anybody seeing them.** `gofmt -l` has
  been red on `main` since 2026-08-17: four files were genuinely unformatted,
  invisible because on a Windows checkout the same command flagged all 553 `.go`
  files for line endings, and the real signal sat under the noise. `pnpm -r lint`
  was red too — locally it printed 32 errors, 27 of them from a generated
  directory that no CI clone even has, which is how the five real ones went
  unread. Both gates are green and, more to the point, legible: the first thing
  the formatting gate did once the noise was gone was catch a file this release
  brought in unformatted.

- **The docs site is now checked, twice, by things that can fail.** Its build was
  in no pipeline at all, and a dead in-page anchor never failed anything —
  VitePress fails a build on a dead *page* link and says nothing about an anchor,
  so a green build was not evidence. It also could not build at all: an unquoted
  value containing a colon in the home page's front matter broke the YAML
  outright. The site now uses **GitHub's own heading-id rule** rather than its
  own, because these pages are read on both surfaces and the 98 in-page links
  that resolved nowhere on the site were all correct GitHub anchors — rewriting
  them would have moved the breakage rather than removed it. `lint:docs` builds
  the site and runs `scripts/check-doc-anchors.mjs` against what it emitted.
  ⚠ Deep links into docs.filex.sh headings containing `&`, `/`, an em dash, a
  dot, an apostrophe or a leading digit have changed spelling.

- **Line endings are the repository's decision, not the contributor's.** A new
  `.gitattributes` pins every text file to LF (`.editorconfig` has said so since
  the beginning; git never read it) and marks the binary fixtures explicitly.
  Nothing committed changes — every tracked blob was already LF — but a Windows
  *working tree* was CRLF, so a shell script from this checkout failed under WSL
  or Linux with `/usr/bin/env: 'bash\r': No such file or directory`, and nothing
  had ever stopped a CRLF blob from being committed by someone with
  `core.autocrlf=false`.

- **`docs/MULTI-TENANCY.md` said it was unfinished.** Its status line still read
  "in progress — Phase 1 landed on `feat/multi-tenant`" while nine of its ten
  phases were shipped, the branch was gone, and a ten-provider deployment had
  been running on the feature since v0.1.61. Three further claims in it were
  wrong in the same direction: it promised a `(provider_id, email)` unique
  migration "00015" that does not exist (00015 is `provider_cookie_domain`, and
  e-mail is still globally unique), it stated per-tenant e-mail uniqueness as
  fact, and it listed per-tenant branding as v2 when branding had shipped
  through prefixed `settings` keys.

## [0.31.0] - 2026-09-05

### Added

- **Encrypted folders can be recovered.** Until now, forgetting the password
  meant the files were gone — that was the documented behaviour and it was a
  bad one. Creating an encrypted folder now shows a recovery key **once**;
  filex never stores it and it opens the folder without the password.

  The file format did not change. Each file's key is still wrapped by exactly
  one key in its 97-byte header — that key is now the folder's master key
  rather than the password key, and the marker holds the master key wrapped
  once per way in. For a folder created before this release the two are the
  same thing, so **existing encrypted files are byte-identical and open
  unchanged**; there is a round-trip test against a frozen copy of the v0.30.1
  crypto module, in both directions, because that is the promise that matters
  most here. Such a folder cannot be given a recovery key by the server — it
  has no password to re-wrap with — so filex offers the upgrade at the one
  moment it holds one: the next successful unlock, behind a visible notice.

- **Optional operator escrow, fixed at install.** `FILEX_INSTALLATION_E2E_ESCROW_KEY`
  adds a second way into every folder created while it is set. It is RSA-OAEP:
  filex holds only the public half, `filex e2e-escrow keygen` prints the private
  half once and writes it nowhere, and the operator supplies it back when they
  need it. A stolen database therefore decrypts nothing.

  ⚠ **This is a backdoor, deliberately, and the documentation says so.** When
  the escrow key is used through filex the folder's owner is notified, and a
  forged "escrow was used" report is refused — the client must first decrypt a
  server-issued nonce sealed to the escrow key. But the notification is an
  announcement, not a control: an operator holding the private key can copy the
  marker and the ciphertext off disk and decrypt offline, with no request, no
  notification and no audit row, and filex cannot detect it. If that is not
  acceptable for your deployment, leave escrow off.

  `FILEX_INSTALLATION_` is a new prefix for settings that are fixed when the
  data directory is initialised. filex refuses to start if one of them changed,
  because for escrow the immutability is arithmetic rather than policy: a folder
  created while escrow was off has no escrow-wrapped key, and switching it on
  later cannot open it.

- **Star is a real action.** It was rendered in the list view and nowhere else,
  so v0.30.0 shipped a Starred view that a user in grid view had no way to fill.
  It is now in the context menu ("Star" / "Unstar", multi-selection aware), on
  grid and gallery cards (on hover or focus, and painted permanently once
  starred, so the Starred view is legible without hovering every tile), and on
  the keyboard as a remappable `S`.

- **Tags in the navigation panel.** Tagging has existed for a long time and
  there was no way to browse by tag inside the explorer — only an admin page.
  The panel now lists the tags that exist and opens the files carrying one.

### Fixed

- **A virtual view opened from its own URL said "Folder not found".** The
  explorer writes the current location into the address bar, so opening Trash
  put `#.trash` there — and on reload that sentinel was handed to the ordinary
  folder loader, which 404'd. Reported for Trash; Recent, Starred and Shared
  behaved the same way. Sentinels are now routed to their view before the
  request is made, so a reload lands back in the view with its own empty state,
  and an unknown dot-path is left alone because a user may own `.config`.

- **The details panel printed `.starred`.** Third surface of the same bug that
  put `.shared` in the tab strip in v0.30.0: the sentinel-to-label map had been
  written more than once. Every surface that renders a path segment now reads
  one map.

- **An app token could manage its owner's credentials.** An API token
  authenticates *as* its owner, and the embeds we run authenticate every visitor
  with one shared token injected by the host's proxy — so v0.30.0's "API keys"
  panel entry meant an embed visitor could list and revoke the credential the
  embed itself runs on, and mint S3, SSH and NFS credentials as the owner.

  A token now declares what it is. `user` is a person's own credential and
  nothing changes for it; `app` is an integration, and the four credential
  surfaces (`/api/tokens`, `/api/auth/s3-keys`, `/api/auth/ssh-keys`,
  `/api/auth/nfs-exports`) refuse it with a 403 that names the token and both
  ways out. The explorer leaves out the surfaces that belong to a single person
  — Recent, Starred, Shared with me, API keys — while Upload, the storages,
  Trash, Tags and "How to connect" stay. Existing tokens migrate to `app`,
  because the restricting direction is the safe default and these surfaces only
  matter when a browser UI is drawn.

- **`/api/files/capabilities` could refuse a caller.** Gating it on the token
  chain meant a revoked token, an unknown token username or a disabled account's
  cookie got a 403 from a public route — the login screen failing closed. It now
  annotates the caller when it can and answers everyone.

- **Moving a plaintext file into an encrypted folder is refused.** Uploads were
  encrypted client-side; move, copy and paste were plain server-side byte
  operations, so filex's own UI would put plaintext inside an encrypted folder
  with no warning. The guard is server-side and covers the ops queue, sync moves
  and the AI/MCP surface.

- **The install banner ate clicks.** Both banners are full-width fixed strips
  that paint a centred card, and without `pointer-events-none` the empty half
  sat on top of the sidebar behind it: measured, five destinations unreachable
  at 1440×900 and eleven on a taller menu. `PendingOpsTray`, the same shape in
  the same corner, already did this correctly.

- **`slim` was not slim.** The tag was built from the full recipe, so
  `docs/DOCKER.md` promised ~40 MB while the registry served **511 MB**
  compressed — `latest`, `slim` and `full` were the same image, and the two
  Dockerfiles differed by one package that nothing calls. There is now a real
  slim image: **43 MB compressed**, the binary and the embedded UI, with image
  thumbnails (pure Go) still working and everything that shells out to ffmpeg,
  ghostscript or libreoffice reported as unavailable rather than failing.

- **`docs/E2E-ENCRYPTION.md` was 183 lines of Turkish** in an English repo,
  linked from the README and published on the docs site. Translated, and
  corrected against the code while translating: it under-counted the places that
  filter the marker, missed two disabled surfaces and the refusal to nest
  encrypted folders, and its "v2 roadmap" listed six things none of which had
  shipped — those are now stated as limitations rather than promises.

### Changed

- **Docker images build natively per architecture.** arm64 was emulated, and the
  emulated part was `apk add libreoffice` plus a JRE — the worst possible thing
  to run under QEMU. Each architecture now builds on its own runner and the tags
  are joined from the digests, so no tag exists until both have landed. The
  docker job also no longer waits for goreleaser: it builds its own binary from
  source and took nothing from the Release, so the dependency only lengthened
  the critical path.

- **The browser suite is a gate.** Cypress defaulted to `https://fm.example.com` —
  the live deployment — and ran in no pipeline. It now boots its own instance
  and runs in CI. Getting there meant fixing 19 red specs, several of which had
  been passing for the wrong reason: one matched the sidebar link instead of the
  dashboard it was meant to assert on, and two asserted a capability slot that
  only existed because production still carried a row from an older version.
  227 of 227 pass, and the run went from 9m58s to 1m25s once the service worker
  stopped re-precaching the bundle between tests.

### Added

- **Encrypted folders can be recovered.** Until now, forgetting the password to
  an E2E-encrypted folder destroyed it — the documentation said so, and it was
  true. Every folder created from this release on gets a **user recovery key**:
  160 bits, shown exactly once when the folder is created, never stored by
  filex, and enough to open the folder without the password. It is a password
  equivalent, so keep it somewhere other than the password.

  The file format did not change. A per-file key was already wrapped by one
  folder key; that key is now a **folder master key** held in the marker,
  wrapped once per way of reaching it. Adding a recovery path costs one more
  wrapped copy of 32 bytes, not a re-encrypt of anything — which is why not a
  byte of anyone's existing data was touched.

- **Optional key escrow for operators**, fixed at install time via
  `FILEX_INSTALLATION_E2E_ESCROW_KEY`. `filex e2e-escrow keygen` mints the pair;
  the server gets the **public** half only, so it can seal new folders to the
  escrow identity and open nothing. The private half is the operator's, and a
  stolen filex database still decrypts nothing. Using the escrow key notifies
  the folder's owner, and the notification is *evidence*: the client must
  decrypt a server-issued challenge sealed to the escrow key before the event
  is recorded, so it cannot be forged — and, stated plainly in the docs, cannot
  be relied on either, because an operator holding the private key can decrypt
  offline without ever asking filex.

- **Install-time settings, as a convention.** Anything prefixed
  `FILEX_INSTALLATION_` is recorded on the first boot and frozen; filex refuses
  to start if it later disagrees with the environment, and says what changed and
  what the operator can and cannot do about it. Escrow is the first of these,
  and the reason is arithmetic rather than policy: a folder created while escrow
  was off carries no escrow-wrapped key, so switching it on later cannot open
  it, and a server that started anyway would be claiming a capability it has
  over only half its data.

### Fixed

- **filex's own UI would put a plaintext file inside an encrypted folder.**
  Uploads are intercepted and encrypted in the browser, but paste,
  drag-and-drop and duplicate are server-side byte copies that never touch the
  crypto — so a file dragged into an encrypted folder was stored exactly as it
  arrived, looked like its encrypted neighbours in the listing, and nothing
  warned anyone. The reverse was as bad: a file moved *out* stayed encrypted
  somewhere no password prompt would ever appear.

  The server cannot fix either by encrypting or decrypting, because it has no
  key, so it refuses: a copy or move may not cross an encryption boundary
  (HTTP 409, naming the file). The rule lives in one place and every transfer
  surface calls it — the ops queue, the synchronous move and the AI/MCP move —
  rather than in the one client that happened to notice.

### Changed

- **Folders created before this release keep working, untouched.** Their format
  is read as a first-class path, not a migration shim, and the test suite proves
  it against a frozen copy of the v0.30.1 module rather than a re-creation of
  it. They cannot be given a recovery key by the server, because that needs the
  folder password and filex does not have it — so filex asks at the one moment
  it does: the next successful unlock. The offer is a visible strip with the
  consequences spelled out, including that accepting on an escrow-enabled
  installation also gives the operator a key. Declining changes nothing.

- The create-folder warning no longer says recovery is impossible, because for
  new folders it is not.

## [0.30.1] - 2026-09-05

### Fixed

- **The tab strip printed the raw sentinel for the new views** — a tab opened on
  Recent, Starred or Shared with me read `.recent`, `.starred`, `.shared`.
  Trash was right, and that is the whole story: the sentinel-to-label map was
  written twice, and when the three new views arrived only the breadcrumb copy
  was extended. A second copy of a mapping is a second chance to forget it, and
  the symptom hides itself — the old view keeps working, so it reads as "only
  the new one is missing something" rather than as a bug. There is one map now
  (`lib/listing.ts`), and every surface that renders a path segment reads it.

## [0.30.0] - 2026-09-04

Everything here came out of two issues opened by the same person, and the most
useful thing in the release is the one we got wrong: v0.29.0 fixed filename
search and **nobody could see it**, because the index only gains new fields for
documents that are re-indexed and nothing ever rebuilt it. He measured our own
demo, correctly concluded that nothing had changed, and reported that content
search "doesn't work at all" — it was the same cause. A fix an existing install
cannot reach is not shipped.

### Added

- **A collapsible navigation panel in the explorer.** Upload as the primary
  action, then Recent, Starred, Shared with me and Trash, then the storages the
  caller can see, with the ones reached through a grant marked as shared. It
  collapses to a 56px icon rail rather than disappearing — a panel that vanishes
  takes its own way back with it — and below 560px it is a drawer instead of a
  column, so the listing keeps its width (measured: 388px with the drawer open
  and closed). The collapsed choice is remembered per viewer.

  It lives in `@brftech/filex-core`, so the web app, the desktop app and every
  embed get the same panel from one implementation. `sideNav` turns it on or
  off; the web component also takes a `sidenav` attribute for hosts that never
  touch JavaScript.

- **`uiProfile: 'standard' | 'simple'`.** The reporter's argument was that most
  of his users are not in IT and will not relearn a file manager: tabs, a split
  pane, four view modes and mount instructions are a power-user tool. `simple`
  turns those off and expands the panel; nothing is removed from the build, and
  an embedder can set either profile. The admin panel keeps today's defaults.

- **Connections and API keys from inside the explorer.** Both surfaces existed
  and neither was reachable: our own web app wired the buttons itself, so an
  embedder mounting `<filex-explorer>` gave their users no way to see how to
  mount a drive or to mint a token. `SelfTokensModal` has moved out of the web
  app into the shared component and the web copy is gone.

  Why it had never moved: the web version hid the write and delete scopes from
  viewer accounts by reading a store only the web app has. The shared component
  does not reproduce that. It offers every scope and lets the server refuse —
  which it already does, in words worth reading (`scope 'write' is not
  available here`). Asking is not granting, and a UI-side role check hides the
  surface from exactly the accounts that need it. For a year the only place to
  mint the token the FTPS guide names was the admin panel.

- **`GET /api/files/manager/shared-with-me`** — the nodes a caller holds a grant
  on. The data existed in `file_grants`; the only listing over it was
  path-scoped and owner-only, so "what has been shared with me" had no answer.
  Tenant-scoped explicitly, like search.

### Fixed

- **The search index now repairs itself after an upgrade.** On start it compares
  the document schema it was built with; if it is behind, it builds a
  replacement **alongside** the live index and swaps it in atomically. The old
  index answers every query until the swap, and extracted text is carried across
  document by document rather than re-derived — which is what makes this safe to
  do automatically. The blackout that argument was made against in v0.29.0 does
  not happen: measured on 20 202 documents, the rebuild took 2.96 s and 601
  searches issued during it returned 0 errors and 0 missing hits.

  An interrupted rebuild is discarded and retried; a crash during the swap
  restores the known-good index; it refuses and keeps serving the old index if
  the disk cannot hold both. `FILEX_SEARCH_AUTO_REBUILD=0` turns it off — but
  off by default would reproduce exactly the failure it exists to fix.

- **Filename ranking is now a port of VS Code's Quick Open scorer.** The
  reporter's words were "VS Code does this fine, I can't do it here", and he was
  pointing at a specific method, so we ported it: a subsequence match scored
  with position bonuses (start of name, after a path separator, after `_ - .`,
  camelCase humps, runs of consecutive characters), the query split into pieces
  matched independently so **word order does not matter**, and the filename
  scored separately from its folders and weighted above them. Bleve still
  retrieves the candidates; the scorer re-ranks them and, crucially, **drops
  candidates that do not answer every piece**.

  Measured against the corpus he tested on: `Code main` went from nine results
  to one. `main code` finds `Code/main.go` too. `Code/main.go` and
  `example/main.go` stopped being the same thing to the search. Exact filename
  still outranks prefix, which outranks everything fuzzy — now asserted by a
  test rather than emergent from merged relevance scores.

  Edit distance stays, ranked below the subsequence pass: `mian.go` finds
  `main.go`, which Quick Open itself would not.

- **Multi-word content search was an OR**, so adding a word *widened* it. That,
  not the filename side, was where most of the noise came from — seven of the
  nine results for `Code main` were files that merely contain the word "code".
  Every word is now required.

- **The typo pass almost never fired.** It was gated on the number of candidates
  the index returned rather than the number that survived filtering, so a query
  that retrieved plenty and kept none still counted as "enough".

- **The recently-opened tray read the wrong field** (`entries` from an endpoint
  that answers `nodes`), so it was empty on every server that ever served it.
  It was also unreachable — the toolbar declared the event that opens it and
  never emitted it. The panel is now the working surface.

- **Starred and Recent rows were unopenable in multi-storage mode**: the rows
  carried no storage name, so no `storage://path` could be built for them.

- **`docs/WEBDAV.md`, `docs/LDAP.md` and `docs/METRICS.md` sent people to
  "Settings → API tokens"** — a screen that has never existed in the product.

- **The web-component embedding example authenticated nothing.** It assigned
  `config` after the import that registers and mounts the element, so the first
  folder request went out without credentials: the explorer rendered and the
  listing said "could not load this folder".

- A query could arrive while the search index handle was being swapped, because
  the read lock was released before the query ran rather than after. One error
  in 269 hammered queries; now impossible by construction.

## [0.29.0] - 2026-09-04

### Added

- **Open an Office document that lives on your own computer, in the editor your
  server runs.** Double-click a `.docx`, `.xlsx` or `.pptx` (ten Office types in
  all) and the desktop app opens it — on a machine with no Office installed.
  Most Linux desktops have none, many Macs have none, and plenty of Windows
  machines have none either; filex already had a perfectly good editor and the
  documents on those disks had no way into it.

  Two routes, picked per document. A document inside a folder you keep on this
  computer opens as **itself** — no copy, no write-back, saving goes to the
  server and sync brings it down again. A *paused* pair is deliberately not
  treated as one: the save would reach the server and never come back, and you
  would believe you had saved. Anything else is copied to a scratch area on the
  server, edited there, and written back over the original path on every save,
  with a strip along the bottom of the window naming the file the whole time.

  The write-back is where an editor destroys work, so: the replace is atomic
  (temp file in the same directory, then rename — never across devices, never a
  partial write over the document); a document deleted while it was open is not
  resurrected, its bytes are kept beside it as `<name>.filex-recovered-<time>`;
  a refused rename keeps the edit, says so in a dialog *and* a notification, and
  names where it kept it; the scratch copy survives a grace period after the
  window closes, because OnlyOffice posts its save roughly ten seconds after the
  editor disconnects and deleting on close would discard the last edit of every
  session; and a sweep at next start clears whatever a crash left behind.

  Registration is deliberately conservative. The installer adds filex to the
  **"Open with"** list and changes nothing that is already set: electron-builder's
  own `fileAssociations` macro writes the *default* ProgId for each extension —
  it takes the file type at install time, which on a machine with no Office is
  precisely how filex would become the handler without anyone being asked — so
  the Windows registration is hand-written instead, and macOS registers as
  `Alternate` rather than `Default`. Making filex the default is always an
  explicit action: `xdg-mime` does it on Linux from Settings, Windows opens the
  default-apps pane (`UserChoice` is hash-protected and cannot be set by an
  application, and filex does not pretend otherwise), and macOS explains the
  Finder route. See [docs/DESKTOP.md](docs/DESKTOP.md#opening-documents-from-your-computer).

- **An end-user front door: `…/drive`.** A non-admin account has always landed
  in the file manager rather than the admin panel — the router redirected them
  out of it and every `/api/admin/*` route re-checked the role — but everything
  around that screen said otherwise. The URL was `/admin/explore`, the browser
  tab said "filex Admin", and the login form said *"Use your filex admin
  credentials"*. Deploy filex for a team and each of your users was told three
  times, before seeing a single file, that this was an administrator's tool.

  The same app is now also served at `/drive`, with a per-route document title
  and login copy written for everyone. `/admin` is untouched and every existing
  bookmark still resolves. (Reported as #14.)

- **`tag:` and `-tag:` filters in search.** Tags have been a filex feature for
  a long time and were not in the search index at all, so `tag:invoice` searched
  for a *file named* "tag:invoice" and came back empty. They are now a filter
  rather than a search term, combinable with free text (`invoice 2026
  tag:paid`), resolved against the database so a re-tag is visible immediately,
  and applied before the result limit so `limit` counts filtered rows. A tag
  that does not exist returns nothing — never the whole storage.

- **The filex.sh landing page is in the repository**, at `site/`, deployed with
  `scripts/sync-site.sh`, and covered by the release documentation audit. It had
  lived only on the static host, so there was nothing to edit and nobody edited
  it: it still described roughly v0.14 — no desktop app, no sync, no protocol
  endpoints, no `filex mount`, no LDAP, no plugins, no search, no
  multi-tenancy — and claimed five storage drivers when there are six plus a
  plugin API.

### Fixed

- **Filename search depended on which separator the file's name used.**
  `invoice 2026` did not find `invoice_2026.pdf`, and `main go` did not find
  `main.go`, while `foo bar` found `foo-bar.txt` — an inconsistency with a
  single cause. Name search was a disjunction of an analysed match and an
  unanalysed `*term*` wildcard: the wildcard half cannot match anything once the
  query contains a space, leaving only the analysed half, whose tokeniser splits
  on `-` but joins on `_` and keeps `main.go` whole. So whether search worked
  was decided by the file's punctuation.

  Names are now indexed a second time in a normalised form (every run of
  non-alphanumerics collapsed to one space) and the query is normalised the same
  way, so `.`, `-`, `_` and a space are interchangeable. Multi-word queries
  require one `*word*` wildcard per word instead of one over the whole string.
  The normalisation is done in Go rather than as a Bleve field mapping on
  purpose: a mapping is frozen into the index when it is created, so a fresh
  install and an upgraded one would have analysed the same filename two
  different ways. (Reported as #15.)

- **One typo is now forgiven.** `mian.go` finds `main.go`. The fuzzy pass runs
  only when the strict pass came back short of the limit, and its hits rank
  below every exact and prefix match. Edit distance scales with word length
  (none at three characters or fewer, one up to seven, two above).

- **Ranking is now decided, not inherited.** The order is exact filename →
  prefix → name → path → fuzzy → content, asserted by a test. It had been
  whatever merging two queries' relevance scores produced: measured before this
  change, `report-final.txt` ranked *above* `report.txt` for the query `report`.
  Exact matching compares the name both with and without its extension.

- **The same query meant different things in different boxes.** `GET
  /api/ai/search` and the MCP `file_search` tool passed the raw string through,
  so `tag:source` was read there as a filename and `invoice 2026` found nothing
  — while the toolbar and `/api/files/search` understood both. All four now
  share one parser and one fallback plan.

- **The explorer's onboarding tour described a search that does not exist.** It
  said "This box filters the current folder"; the toolbar search covers the
  whole storage, and every storage when the multi-storage root is open. Its
  placeholder said "File name". Both were wrong before this release and are
  fixed in the shared component, so every surface gets the correction.

- **The explorer told a non-admin to configure a storage.** With no visible
  storage it showed "No storages configured yet" — for a `user` account that
  almost always means nothing has been *shared* with them, so it sent people to
  fix something they have no permission to fix.

- **An installed PWA ejected itself into a browser tab.** The manifest scope was
  `/admin/`, so the first navigation to the new `/drive` front door left the
  installed app. The manifest scope now covers both; the service worker's scope
  and the manifest `id` are deliberately unchanged (a changed `id` turns every
  existing install into a second app).

## [0.28.0] - 2026-09-03

### Fixed

- **LDAP was configured, initialised, printed in the boot banner — and
  unreachable from every login path.** With `FILEX_AUTH_DRIVERS=local,ldap` a
  directory account was answered `401 invalid credentials` even with the right
  password, in roughly **350 microseconds**: less than one LDAPS round trip, so
  the request never reached the network at all. Nothing was logged, and the docs
  described the behaviour that was missing ("filex tries each enabled driver in
  order"), which made it read as a directory misconfiguration to everyone who
  hit it. Three separate gaps produced it, and each would have been enough on
  its own:

  - the bootstrap assigned the login handler's single `LoginDriver` in the
    `local` case only, so the directory driver was never the one it held;
  - `*ldap.Driver` did not satisfy `auth.LoginDriver` at all — no `Logout` — so
    it could not have been assigned even by hand. The compiler had never been
    asked the question;
  - `ldap.Login` ended with `return user, "", nil` under a comment saying the
    caller would mint the session. No caller did: a *correct* password would
    have handed back an empty cookie, a successful login presenting as a failed
    one.

  Password drivers are now chained (`auth.LoginChain`) in the order they appear
  in `auth.drivers` / `FILEX_AUTH_DRIVERS`. `local` first is deliberate — it is
  a hash compare against a row filex already holds, so `admin@local` and every
  break-glass password stay answerable while the directory is unreachable.

- **A directory account could sign in to the web UI and still be refused by
  WebDAV, SFTP, FTPS, S3 and NFS.** Those protocols check the password against
  `users.password_hash`, which is **empty by construction** for a directory
  account — filex never learns the password. The refusal was identical to a
  wrong one. They now ask the directory when the local table cannot judge
  (`auth.ldap.protocol_login`, on by default). The local hash is still tried
  first, TOTP accounts are still refused on every protocol, and a successful
  check is cached for five minutes exactly as a local one is — without that,
  each request of a WebDAV `PROPFIND` storm would be a fresh LDAPS bind.

- **A search failure and "no such user" were the same answer.** An unreachable
  directory, an expired service account and a typo in `base_dn` all came out as
  `unauthorized` with nothing in the log. Transport and protocol failures are
  now reported and logged apart from a rejected password.

- **`user_filter` silently broke with more than one placeholder.** It was filled
  with `fmt.Sprintf`, which consumes one argument per verb, so the standard
  Active Directory filter that accepts either address form became
  `(userPrincipalName=%!s(MISSING))` — a filter matching nobody. Every `%s` is
  now filled with the same escaped identifier.

- **Searches used a size limit of 1.** Active Directory answers a subtree search
  from the domain root with continuation references alongside the match, and a
  server counting those against a limit of 1 can answer "size limit exceeded"
  instead of the entry. The limit is 2, and a filter that genuinely matches two
  accounts is refused with a warning rather than resolved to whichever came
  first.

### Added

- **`auth.ldap.ca_file` / `FILEX_LDAP_CA_FILE`** — a PEM bundle for a private or
  internal CA, **appended** to the system trust store (the public roots keep
  working) and applied to `ldaps://` and StartTLS alike. Previously the only way
  to reach a directory behind an internal CA was to rebuild the container's
  `/etc/ssl/certs/ca-certificates.crt`. The file is read at boot, so a wrong
  path is a startup error rather than a login failure hours later.
- **`auth.ldap.protocol_login` / `FILEX_LDAP_PROTOCOL_LOGIN`** — set `false` to
  keep directory passwords on the login form only and require an API token on
  the file protocols.
- **The Helm chart can configure LDAP.** `auth.ldap.*` in `values.yaml` renders
  the `FILEX_LDAP_*` variables; the chart listed `ldap` as a valid driver while
  offering no way to configure it, so `drivers: "local,ldap"` produced a driver
  that failed `Init` and was skipped.

## [0.27.6] - 2026-09-01

### Changed

- **Release tags are signed from here on, and there is finally a key to sign
  them with.** `CONTRIBUTING.md` had said `git tag -s` for months while no
  signing key existed on the release machine, so every tag through v0.27.5 is a
  plain annotated one — an instruction nobody can follow is not a policy, it is
  a lie the document tells. The step now also requires `git tag -v` to answer
  `Good signature` before the tag is pushed, and says where the key and its
  passphrase live. Nothing in the shipped software changes in this release.

## [0.27.5] - 2026-09-01

### Fixed

- **A transient `503` from an object store sank the whole upload.** The retry
  budget was widened to six attempts a release ago, and a test proves a 503 is
  classified as retryable — but for an upload none of that could ever fire.
  The SDK rewinds a request body before retrying it, and every upload surface
  hands the S3 driver a plain stream (the handler sniffs the first bytes to
  detect the type and rejoins them), so there was nothing to rewind: the second
  attempt died before it was made and a brief upstream wobble became a
  permanent failure, reported as *"failed to rewind transport stream for retry,
  request stream is not seekable"* — a message about filex's plumbing rather
  than the outage behind it. The budget was real for listings and reads and a
  no-op for writes. An upload that declares a size of at most 8 MiB is now held
  in memory while it is sent, so a retry replays it byte for byte; a larger one
  streams through as before, because buffering every body would trade a rare
  failed upload for an out-of-memory kill. See
  [STORAGE.md](docs/STORAGE.md#s3--s3-compatible).

### Changed

- `pnpm run build:packages` / `build:web` / `build` now quote their workspace
  filters in a way `cmd.exe` also understands. They matched nothing on Windows
  — the single quotes reached pnpm literally — so `build:all`, a documented
  release step, could not run on a Windows workstation at all.
- `CONTRIBUTING.md` says what to do when Go lives in WSL and pnpm does not:
  `build:all` ends in a plain `go build`, and the screenshots boot that binary.

## [0.27.4] - 2026-08-29

### Fixed

- **The Helm chart shipped a 23-release-old image.** `values.yaml` leaves the
  image tag empty, which the chart resolves to `.Chart.appVersion` — so
  appVersion is the version a Helm user actually runs, and it had been sitting
  at `v0.4.0` since it was written. It now tracks the release, moved by
  `scripts/sync-chart-version.mjs` as part of the release steps, and
  `web/tests/deploy/chartVersion.test.ts` fails the build if the two ever drift
  again — and the release workflow itself now refuses to publish a tag whose
  chart is behind, before a single artefact is built. **Every release updates
  the chart; it is a step of the process, not a chore to remember.**

## [0.27.3] - 2026-08-29

### Fixed

- **A folder dragged out of the desktop app arrived empty.** The watcher that
  finds where a stand-in landed was started *after* `webContents.startDrag()` —
  and on Windows that call hands control to the operating system's own drag
  loop, which does not return until the user lets go. The watcher therefore
  went up after the drop had already happened. Worse, a recursive `fs.watch`
  whose event loop is blocked does not deliver the change late; it misses it
  outright (measured: a file created during a 4-second block was never
  reported, before or after). Two changes: the watcher is armed **before** the
  drag, and it runs in a **worker thread**, whose loop keeps running while the
  main thread is inside the drag loop. Single files were never affected,
  because a small selection is prepared in the background and handed to the OS
  as a real file — which is why this only ever showed up on folders.
- **The suite could not have caught it.** Its simulated drop happened after the
  drag call returned, and its test hook skipped `startDrag` entirely rather than
  blocking like the real one. Both are fixed: the drop is now performed *while*
  the drag is in flight, and the hook blocks for the same reason the OS does.

### Added

- **The desktop app keeps a log** at `<userData>/logs/filex-desktop.log`
  (rotated at 2 MB, one previous file kept). A packaged app has no console, so
  `console.log` went nowhere: when a drag-out failed, the only evidence was an
  empty folder. Every step of a drag and of a transfer is one line, and an
  uncaught exception in the main process lands there too.

## [0.27.2] - 2026-08-29

### Fixed

- **A file whose name is not ASCII no longer breaks a download — or the client
  reading it.** `Content-Disposition` carried the filename raw, so a name like
  `Türkçe adlı dosya.txt` put bytes over 127 in an HTTP header. Browsers guess
  their way through that, which is why it went unnoticed for years; a strict
  client does not. Electron's `net.fetch` threw
  `Cannot convert argument to a ByteString … value of 305` from inside its
  response handler — where no caller's try/catch can reach it — so the filex
  desktop app took an uncaught exception and a folder being dragged out stopped
  filling in halfway, silently. Every download now sends RFC 6266:
  `filename="ascii-fallback"` plus `filename*=UTF-8''percent-encoded`, from one
  shared helper used by the manager, share, share-browse and viewer endpoints.
- **The desktop app survives a badly-formed header from any server.** Its
  transfers moved from `net.fetch` (which validates response headers as
  ByteStrings) to `net.request` (which does not), so an older filex — or
  somebody else's server — can no longer stop a drag-out by naming a file in
  Turkish.
- **A failed drag-out no longer freezes the app.** The failure was reported with
  `dialog.showErrorBox`, which is modal: the box sat in front of a frozen window
  until it was clicked. It is a toast in the explorer now, plus an OS
  notification when the window is not in front. An uncaught exception in the
  main process is likewise logged and notified instead of ending in Electron's
  raw JavaScript error box.
- **Drag-outs leave a trail.** The transfer runs after the gesture is over, with
  no window of its own; when it went wrong the only symptom was a folder that
  stayed empty. Each step now prints one line (`[drag …]` / `[xfer …]`).

## [0.27.1] - 2026-08-29

### Fixed

- **A drag-out could fill in the wrong folder.** The drop watcher matched the
  stand-in by NAME across the local drives, so any file that happened to appear
  under the same name while a drag was in flight — a backup job, another
  download — looked like the drop and the user's file was written into that
  folder instead. What was handed to the shell is known exactly (an EMPTY
  stand-in, created inside this drag's window), so anything with content of its
  own is now ignored. Measured: with the guard off, a same-named decoy file
  received the transfer; with it on, the decoy is untouched and the real drop
  still lands.

## [0.27.0] - 2026-08-29

### Added

- **Copy in one storage, paste into another.** Copy/cut in one depo and paste in
  the next now does what it says: the queue carries a destination storage of its
  own and the worker streams the bytes between the two drivers — a whole tree,
  empty folders included, with each file's own mtime and a size check on the far
  side before anything is deleted. A cut across storages is a real move (the
  original is removed once the copy is verified); a **drag** across storages
  copies, the rule Explorer and Finder taught everyone. Pasting into a read-only
  depo is refused with a reason instead of failing later in the worker.
- **Drag files out of the app onto your desktop — at any size.** In the desktop
  app a selection can be dragged out to Explorer/Finder or into another program,
  and folders and multi-selections land as **separate real files and folders**,
  not one archive. The OS copies a dragged file from a path at the moment of the
  drop, so filex answers that in two ways and picks for you: anything it already
  has (kept on this computer, or a small selection prepared in the background the
  moment it was selected) is handed over as a real file, and everything else
  drags as an empty **stand-in** — the shell copies it in microseconds, filex
  finds the folder it landed in and downloads the real content there. A 100 GB
  file therefore starts dragging as fast as a 1 KB one, with no ceiling and no
  waiting ([docs/DESKTOP.md](docs/DESKTOP.md#dragging-files-out)).
  ⚠ A drop onto an *application* rather than a folder writes nothing to disk, so
  the stand-in route cannot fill it in; the app says so instead of pretending it
  worked, and the prepared-copy route (which covers that case) is what small
  selections use. Downloads land on `.filexpart` and are renamed only when
  complete. In a **browser**, a single file can be dragged out the same way
  (Chromium's `DownloadURL`).
- **The agent surface moves between storages too.** `file_move` (MCP) and
  `POST /api/ai/move` no longer answer "cross-storage move not supported": they
  run the same transfer engine as the queue, verify every file on the far side
  and only then remove the source ([docs/MCP.md](docs/MCP.md#moving-files-between-storages)).

### Fixed

- **A cross-storage paste no longer lands in the wrong storage.** `POST
  /api/files/copy|move` resolved the storage from the SOURCES only and applied
  the target's relative path to it, so `alpha://a.txt` → `beta://hedef`
  answered `202` and wrote the file to `alpha://hedef/a.txt` — a folder invented
  inside the depo the user was copying *from*. The endpoint now resolves the
  target's storage, checks editor permission **there**, and refuses an unknown
  or read-only destination up front (measured 2026-08-29).
- **A copy pasted at a storage root is listed again.** The DB mirror looked its
  parent up as `path.Dir("file.txt")` — `"."`, a directory that is never in the
  index — so a copy made at the top of a depo was written to disk and never
  appeared in the listing until the next scan.

## [0.26.1] - 2026-08-27

### Fixed

- **Ticket refusals now say what to DO, not just what went wrong.** The redeem
  endpoint answered bare codes — `{"error":"ticket_expired"}`,
  `{"error":"file_too_large"}` — and the right reaction to each is different:
  an expired ticket needs a new one, an **oversize upload must be retried
  against the same still-valid ticket**, and a chunked body just needs
  `curl -T`. A caller reading a bare code either gives up or mints tickets it
  did not need. Every refusal now carries a `hint` naming the next step (413
  also echoes `sent_bytes`), including the storage-side ones (`503`/`507`).
- **A folder as the ticket destination is refused in the caller's own terms.**
  It used to surface the driver's `storage: path exists with a different kind`,
  which does not tell anyone what to send instead. The refusal now reads
  `"…" already exists as a FOLDER. …` and shows the shape of a correct path
  (`…/<filename>`).
- **The mint reply no longer assumes curl.** Alongside `curl` it returns a
  `powershell` line (`Invoke-WebRequest -Method Put -InFile`) and a `next`
  line saying to run it **on the machine holding the file** — and to hand the
  line to the user when the caller has no shell at all, which is the situation
  of an MCP-only client. Both new fields are exposed through the
  `file_upload_ticket` MCP tool.

## [0.26.0] - 2026-08-27

### Added

- **Upload tickets: an agent can finally upload a large local file.** Every
  agent-facing write surface carried its bytes inside the call — `/api/ai/upload`'s
  JSON body, the MCP `file_write` tool — and on an MCP transport that means through
  the model's context. A 130 MB file is ~173 MB of base64 there: not slow,
  impossible. The agent was left telling its user "I can't upload this" (one
  spreadsheet cost an hour on 2026-08-27).

  `POST /api/ai/upload/ticket` (scope `write`) now splits the job in two. The
  authorized half resolves and **pins** the destination under the caller's own
  token — confinement root, tenant scope and editor grant all checked there, and a
  folder as the destination is refused before a byte moves. The transfer half is a
  plain `PUT`/`POST` to **`/u/{ticket}`** that needs **no credentials at all**, so
  an agent with no filex token (a Coder workspace, a fleet agent) can finish the
  upload with the ready-made `curl -T <file> <url>` line the mint call returns.

  The URL is not a key to filex, it is a key to **one write**: single-use, minutes
  long by default (30 min, max 24 h), no read/list/delete, and the redeemer cannot
  name a path. The bytes are written as — and billed to — the minter, and land on
  the same staging/thumbnail/write-hook path as any other upload. Refusals are
  distinct and non-guessy: `404` unknown *or already redeemed* (the same answer on
  purpose), `410` expired, `409` in flight, `411` no `Content-Length`, `413` over
  the ticket ceiling (the ticket survives, so a corrected upload still works), and
  `503 storage_unavailable` / `507 quota_exceeded` when the backend is the problem.

  Also exposed as the MCP tool **`file_upload_ticket`**, and `file_write`'s
  description now sends anything already on disk there instead of to base64.

## [0.25.3] - 2026-08-25

### Fixed

- **A PIN-protected file-request page rendered blank.** The gate on a
  `/d/{token}` drop link came out with an empty `<title>`, an empty heading, an
  unlabelled PIN box and a blank submit button — nothing on the card told the
  visitor what it wanted. `Drop` reused the shared PIN template but passed no
  string table and no language: the template asks for `{{.T.pin_heading}}`,
  `html/template` renders a missing key as the empty string, and the `Execute`
  error was assigned to `_`. Every drop page (gate, uploader, error pages) now
  goes through the same one-language-per-visitor resolver the share pages use,
  so `?lang=` / `Accept-Language` / `default_locale` decide it — and the
  uploader script's own messages travel with the page instead of being
  hard-coded Turkish for every visitor on Earth.
- **A drop into unreachable storage answered `500` and logged nothing.** When
  the object store behind the folder refuses the write, the upload now answers
  **`503` `{"error":"storage_unavailable"}`** with a message that says the
  storage is down and the link is still good — instead of a generic "could not
  send, try again" that sends people retrying into a wall — and the failure is
  logged at `ERROR` with the storage name, driver, stage and destination, so it
  reaches the error tracker. Out of space is now its own answer: **`507`
  `{"error":"quota_exceeded"}`**. Found the hard way when Hetzner's object
  storage returned `503` for eleven hours and every drop hung for over a minute
  before failing silently.

## [0.25.2] - 2026-08-23

### Fixed

- **Error-tracker events carry the log line's context again.** The slog →
  Sentry/GlitchTip bridge forwarded the message and nothing a person could
  act on: eleven `thumb generate failed` events with no file and no error in
  the issue list or the API, `driver init attempt failed` without the driver.
  Every attribute of the record now travels with the event — as **tags**
  when short (`path`, `err`'s first line, `driver`, `attempt`, `node`, …,
  plus `source` = `file.go:line`), and in full under the **`log` context**
  (a multi-page ffmpeg transcript in `err` is kept whole there). Keys that
  name a credential (`token`, `password`, `secret`, `authorization`,
  `cookie`, `credential`, `private`, `*_key`) are replaced with `[filtered]`
  before either. Proven by a bridge test with a capture transport that fails
  on the previous code (`tags=map[]`, `client_secret` in clear).
- **A listener closed by a shutdown is no longer an error.** `ftps: listener
  stopped` (and its SFTP/NFS twins) logged at `ERROR` on every deploy and
  filed an issue each time. The line is `INFO` with `reason=shutdown` when
  the server is stopping or the listener returned cleanly; only a listener
  that dies while the server is meant to be running is `ERROR` with
  `reason=unexpected` and the error.
- **`driver init attempt failed` says which driver, which error, which
  attempt.** Intermediate attempts are `WARN` (`driver init attempt failed,
  will retry` with `driver`, `attempt`, `of`, `err`); only the last one is
  `ERROR` (`driver init failed after all attempts`). The OIDC caller's
  follow-up line (`oidc: SSO disabled until restart`) states the consequence
  without re-filing the error, so one failure is one issue.

## [0.25.1] - 2026-08-23

### Fixed

- **FTPS survives a DNS hiccup at start-up.** `FILEX_FTPS_PUBLIC_HOST` is
  resolved when the listener starts, and that single lookup was final: on
  the v0.25.0 rollout Docker's embedded DNS timed out four seconds into the
  container's life, the listener never started, and nothing said so except
  one ERROR line at boot — the host port stayed published, `/healthz` stayed
  green. The lookup is now retried for up to two minutes (every 5 s) before
  the listener gives up; a name that truly does not resolve still fails with
  the name in the message. Proven by a test that fails the lookup twice and
  expects the third to be used.

## [0.25.0] - 2026-08-23

### Added

- **Every new share link has a maximum life, and the admin sets it.** A new
  Protection setting, `share.max_ttl_days` (default **7 days**, `0` = no
  ceiling, seeded once from `FILEX_SHARE_MAX_TTL`), caps what a new link or
  file request may be given: a link created without an expiry gets `now +
  max`, a longer request is shortened, and the create response carries the
  stored `expires_at` plus `expiry_clamped: true` when the server changed the
  request. The share dialogs read the ceiling from `/api/capabilities`
  (`share_max_ttl_days`) and offer only choices the server will keep — a 7-day
  server shows *1 day / 7 days*, not *30 days* or *Never* — and print the real
  expiry under the fresh link. **Existing links are never modified**: the
  Protection page and `GET /api/admin/protection` (`shares_over_max_ttl`)
  report how many live links outlive the ceiling, the boot log prints the
  number, and revoking any of them stays a manual decision. One rule, one
  helper (`lib/shareTtl.ts`), every surface: web, desktop and the embeds
  behave the same.
- **The folder-ZIP warmer has a size ceiling; the download button does not.**
  `FILEX_SHAREZIP_WARM_MAX_BYTES` (default **2 GiB**, `0` = unlimited) stops
  the background warmer from pre-building folders bigger than that — the
  16.7 GB incident was three hours of object-storage reads for a link nobody
  opened. Such a folder is zipped the moment a visitor clicks, with the same
  progress page, and cached from then on; nothing is refused for being large.
  Logged once per folder, not once per pass.
- **No cached folder archive older than a week.** `FILEX_SHAREZIP_MAX_AGE`
  (default **7d**, `0` = keep for the share's life) sweeps an archive whatever
  its share's state; the next warm pass or download click rebuilds it. With
  the 7-day link life this bounds the cache to what is actually being shared
  this week.

### Fixed

- **FTPS re-reads its certificate when the files change.** The pair was
  loaded once at start-up, so a mounted, auto-renewing certificate (Caddy,
  certbot) would have been served expired from its first renewal on — with
  `/healthz` green and nothing in the log — which is why FTPS could only be
  run self-signed. Every handshake now checks the files' mtime and size and
  reloads on change; a renewal that lands half-written keeps the previous
  pair serving and warns once. Proven by a test that swaps the files under a
  running server and sees the new serial on the next connection.
- The standalone share dialog's close button was Turkish in every language.

### Changed

- **Release workflow split into `binaries` → `docker` + `desktop`.**
  goreleaser (which creates the GitHub Release) and the multi-arch Docker
  builds were one job, so the desktop installers waited ~25 minutes for
  arm64 QEMU emulation they never needed (v0.20.2: goreleaser done at
  minute 5, job done at minute 30). The installers now attach to the Release
  while the images are still building.
- **`ghcr.io/brf-tech/filex:slim` and `:slim-vX.Y.Z` are pushed.** The
  release notes and `docs/DOCKER.md` have advertised them since the first
  image, but the workflow only ever pushed `:vX.Y.Z`/`:latest` — every
  release page's `docker pull …:slim-vX` was a 404. Same image as `:latest`.

## [0.24.1] - 2026-08-22

### Fixed

- **An interrupted run — uploads included — continues from a checkpoint.**
  0.24.0 made an interrupted first run resume for DOWNLOADS (the copy carries
  the server's mtime, so twins adopt). Uploads could not: the server stamps
  its own mtime on what it receives, and with the history written only by
  the settle pass at the very end, a run killed at file 9,000 of 10,000
  started the next one with no history — every file this machine had pushed
  came back as a "(server copy)" conflict pair. The engine now writes the
  baseline every 50 settled transfers (or 15 seconds): download rows are
  recorded exactly, upload rows are resolved by listing each touched folder
  once, a cancelled run flushes on a short detached context, and the next
  run finishes only what was still pending. Measured in the test: 12
  downloads + 4 uploads, plug pulled — resume does exactly the 8 remaining
  uploads, zero conflicts.

## [0.24.0] - 2026-08-22

### Added

- **Transfers run in parallel.** The engine walked its plan one action at a
  time, so a tree of small files was priced at one full round-trip each —
  measured on a live deployment, 2 GB of ~400 KB files crawled at 0.24 MB/s
  with the network mostly idle. Uploads and downloads now run on a small
  worker pool (default 4, `--transfers` to tune, 1 restores the serial
  engine); directory creation stays first and deletes/conflicts stay serial
  in the planner's careful order.

### Fixed

- **An interrupted first run RESUMES instead of conflicting every finished
  file.** With no baseline, "present on both sides" always meant a conflict —
  so a restart mid-first-run turned every already-downloaded file into a
  "(remote copy)" duplicate; on the tree above that would have been ~7,800 of
  them. Downloads now stamp the server's own mtime on the local copy, and
  twins with no history that match by (size, mtime) are adopted silently.

- **Changing the mirror root keeps each pair's history.** Migration used to
  remove and re-add the pair, throwing the baseline away — and the first-run
  merge that followed conflicted every file the machine had ever uploaded.
  New `filex sync move <id> <path>` repoints a pair and keeps its baseline;
  the desktop root change uses it, and stops the account's watcher first so
  a mid-round rename can never read as a mass delete.

- **The remote walk lists eight folders at a time.** One round-trip per
  folder made the inventory the slow phase of a big sync: measured behind a
  CDN proxy (~0.35s per request), a 3,328-folder invoice tree took ~19
  minutes to list serially — twice per run, since the settle pass walks
  again. The walk is breadth-first by level now, eight listings in flight,
  with the snapshot merge kept single-threaded; the same tree lists in a
  couple of minutes.

- **A filename containing ".." is a filename, not a traversal.** Eleven API
  guards rejected any path CONTAINING the substring — so a real invoice
  named "… Tic. Sic. Gaz..pdf" could be stored but never previewed,
  downloaded or synced (400 "bad path" everywhere). Traversal needs a whole
  `../` segment, and that is what every guard now checks — the same rule
  `sync add` already applied.

- **No-history adoption tolerates coarse mtimes (±2s).** FAT stores mtimes
  in 2-second steps, and any tool that stamps times through float seconds
  can land a millisecond off — measured: a repair pass one millisecond short
  turned 1,667 identical files into conflict pairs. Change detection against
  a baseline stays exact; only the adopt rule for twins with no history is
  tolerant, rsync's modify-window logic.

- **A half-dead connection can no longer freeze a sync forever.** All the
  CLI's parallel streams ride one HTTP/2 connection; when a CDN proxy killed
  it silently mid-first-sync, every stream blocked — for good, since Go's
  http2 sends no health pings by default and the client had no transport
  limits at all. The client now pings an idle connection (ReadIdleTimeout
  30s), bounds dialing, TLS and response headers, and leaves bodies
  unbounded — a big transfer may take long, a hang may not.

- **A folder that could not be listed is not a folder that is gone.** The
  remote walk skipped ANY failed sub-listing as "vanished" — and it now runs
  eight listings wide through proxies the client expects to die under it.
  One 502 on a subtree would have read as "folder removed on the server"
  and binned the local copy of everything below it; in the settle pass it
  would have dropped the subtree from the baseline instead, and every
  uploaded file in it would have come back as a conflict pair next round.
  Only a 404 is skipped now; any other listing error fails the run, names
  the folder, and touches nothing.

- **A pair cannot be pointed at a path that is not there.** `sync move`
  accepted a non-existent path; the next run would have created it empty
  under a surviving baseline, and an empty mirror with history means "every
  file deleted here" — carried to the server. The path must exist and be
  the pair's kind: a folder for a folder pair, a file for a file pair.

- **A missing mirror with history refuses to run.** The engine created a
  missing sync folder and carried on, which turned an unplugged drive or a
  folder moved by hand into a mass delete on the server. A pair whose
  folder is gone while its baseline still remembers files now stops and
  says what to do — `sync move` if it moved, plug the drive back in, `sync
  remove` to stop syncing it. A pair that never synced a file still gets
  its folder created: there is nothing to lose.

- **Moving the filex folder to another drive keeps modification times.**
  The cross-device copy stamped every file "now", and change detection is
  (size, mtime) — so the very history `sync move` preserves would have read
  as every file edited here, and re-uploaded the whole tree. The copy now
  preserves timestamps. If a move fails halfway the pair follows whichever
  side holds the COMPLETE tree (a finished copy with a half-failed cleanup
  points at the new place; a failed copy discards its partial litter), and
  unpairs as the last resort — an unpaired folder syncs nothing and deletes
  nothing.

- **A replaced watcher's late exit no longer unhooks its successor.**
  Stopping an account's watcher for the root move and starting a new one
  could race: the old process's exit handler deleted the supervisor's entry
  for the NEW process, so the next reconcile started a second watcher for
  the same account — two engines over one baseline.

- **The ".." guard also reads Windows paths.** Segments are split on both
  separators, and a segment made only of dots and spaces is refused
  (Windows trims ".. " to "..").

## [0.23.0] - 2026-08-20

### Added

- **Availability at a glance.** Every row in the desktop explorer carries the
  glyph grammar every drive client already taught: ✓ kept on this computer,
  ◐ holding kept items somewhere below, ⟳ being synced right now, ☁
  online-only — so a root listing answers "is anything in here on my disk?"
  without drilling in. And while the engine works, a strip along the bottom
  of the window names the folder and shows live progress — counts and a
  percent bar — parsed by the shell from the same progress lines `--quiet`
  emits, so the CLI output stays the single source of truth.

- **Single FILES can be kept on this computer too.** The sync engine grew
  first-class file pairs (`filex sync add --file`): same planner, same rules,
  same 30-day local trash — the snapshots just carry one entry. The desktop's
  menu offers *Keep on this computer* on files as well; a kept file mirrors
  to `<root>/<storage>/<path>` beside everything else, syncs both ways, and
  *Open local folder* reveals it next to its neighbours instead of launching
  it.

- **Settings shows the mirror root, and can move it.** The root was chosen at
  the first keep and then lived nowhere the user could see. A card in
  Settings now names it, opens it, and changes it: kept mirrors migrate
  (rename + re-pair — the settling pass transfers nothing, and a file pair
  stays a file pair), hand-picked pairs outside the root stay put, and only
  effectively-empty leftovers are swept, never `rm -rf`.

### Fixed

- **Moving the filex folder to another drive no longer unpairs what it moves.**
  The migration removed each pair before relocating its mirror, and `rename`
  cannot cross devices — which is the usual reason to move the root at all — so
  a move to a second disk left every folder sitting where it was, no longer
  synced, with only a dialog to say so. Cross-device moves are copied across
  now, and any failure puts the pair back where its content actually is. A root
  inside the current one (or containing it) is refused outright rather than
  half-applied, and the sweep afterwards touches only the storage folders the
  mirrors emptied — never the root the user chose, and never a folder that was
  already there.

- **"Keep online only" no longer leaves an empty folder skeleton behind.**
  The mirror's intermediate directories (created at keep time) are swept
  after the local copy moves to the Trash — and a folder holding nothing but
  OS litter (`.DS_Store`, `Thumbs.db`, `desktop.ini`) counts as empty, since
  Finder plants `.DS_Store` in any folder the user merely looked at and the
  plain-rmdir sweep stopped dead on it. Anything with real content still
  stops the walk cold.

## [0.22.0] - 2026-08-20

### Security

- **A server cannot name a local folder outside the one you chose.** The path a
  kept folder mirrors under is built from the wire path in the server's own
  listing, so a hostile or compromised server answering with
  `docs://../../Documents` would have had the desktop app create — and then
  two-way sync — a folder outside the account's mirror root, uploading whatever
  it found there on the first pass. Climbing segments are dropped before a path
  is built, the keep is refused before anything is created, and `sync add`
  refuses such a remote outright, so the CLI and every other caller are covered
  rather than one screen.

### Fixed

- **Unkeeping says which folder it is about to bin.** "Keep online only" asked
  what should happen to the local copy without naming it, with *Move to Trash*
  pre-selected — right for a mirror the app made, one Enter away from binning a
  folder it did not for a pair made by hand in Settings. The dialog carries the
  path now, and anything outside the account's root defaults to leaving it.

- **"Open local folder" on a folder whose first sync has not reached it opens
  the nearest one that exists** instead of appearing to do nothing (opening a
  path that is not there yet fails silently).

- **A pair added while the watcher runs is picked up on the next round.**
  `filex sync run --watch` read pairs.json once at start, and the desktop app
  keeps one watcher per account alive for days — so a second folder paired
  later was silently never synced until the app restarted, while the panel
  looked exactly as if it were. The watcher now re-reads the pair list between
  rounds: adds join in, removes drop out. (Reported by the olivov deployment,
  which hit this whole cluster in one afternoon.)

- **Removing a pair before its first run finished no longer fails.** The
  baseline file only exists once a first run completes; unpairing earlier hit
  the missing file and handed the desktop a raw ENOENT error dialog — for a
  remove that had, in fact, already happened.

- **A failed unpair no longer leaves a ghost watcher.** The desktop only told
  the supervisor to reconcile after a successful remove, so the error above
  left a `filex sync run --watch` process syncing a pair that was already gone
  from pairs.json — measurably listing the server every 30 seconds, and
  unstoppable from the UI. Reconciliation now runs whether or not the remove
  threw.

- **The ad-hoc sealed macOS build tells the truth about updates.** Squirrel.Mac
  refuses to swap an app without a Developer ID signature, and electron-updater
  only finds that out after downloading — so "Check now" announced a version
  about to install itself, then fell to a permanent "could not check for
  updates". When the build's own signature says self-update cannot work, the
  app now skips the download entirely, reads the update feed directly, and
  offers the honest thing: the new version's number and a Download button.

### Added

- **Sync progress is visible while it happens — including under `--quiet`.**
  The engine reports phases now (inventory counts while the server tree is
  listed folder by folder, `transfer: 12/345`, settling), and the CLI prints
  them even in quiet mode, which is how the desktop app runs it. A large first
  sync used to spend its entire inventory phase — minutes, on a big tree —
  printing nothing at all, and looked broken enough to cancel; that cancel is
  exactly the path into the two unpair bugs above.

- **"Keep on this computer" — selective sync from the explorer's own menu.**
  Keeping a server folder local used to mean Settings → pair a folder → pick a
  directory, once per folder. Now the first keep asks for ONE root (default
  `~/filex/<server>`), and from then on right-clicking any folder — or a whole
  storage on the drives screen — offers *Keep on this computer*: it mirrors
  under `<root>/<storage>/<path…>` and syncs both ways, while everything else
  stays online-only in the window. Kept folders gain *Open local folder* and
  *Keep online only*; the latter asks, natively, whether the local copy goes
  to the OS Trash or stays. Keeping a parent absorbs already-kept subfolders
  into the one pair. The hooks ride `config.desktopSync` and exist only when
  the desktop shell mounts the explorer — the web admin and the embeds see
  none of it.

## [0.21.6] - 2026-08-19

### Changed

- **A demo instance no longer accepts any new storage backend.** 0.21.4 refused
  the `local` driver, which reaches the server's own filesystem. The remote
  drivers (`s3`, `sftp`, `webdav`, `smb`, …) are refused now too: "attach your
  own bucket" reads as harmless, but what it asks the SERVER to do is open a
  connection to an address a stranger chose — loopback, a private range, a cloud
  metadata endpoint. A demo ships with the storage it demonstrates, and every
  other surface works unchanged.

  A plugin driver is still allowed, because on a demo the plugin subsystem is
  off unless the operator deliberately turned it back on — at which point it is
  their own program.

- **A single failed sync run is no longer reported as an error.** A poll run
  reads the backend's listing; when an object store answers 503/504 under load
  and the retry budget is spent, the run gives up and the catalogue is refreshed
  on the next tick instead. Nothing is lost. Measured on one instance: fifteen
  such failures in six weeks, every one followed by a successful run — fifteen
  reports that meant "the internet had a hiccup".

  Failures are now noted at INFO and reported as a warning only once **three in
  a row** have failed (roughly 45 minutes of a storage genuinely not answering),
  with the streak in the message; recovery is logged too. What an error tracker
  holds should be things worth waking up for.


## [0.21.5] - 2026-08-19

### Fixed

- **A plugin no longer outlives filex on Windows.** Stop filex without letting
  it clean up — a crash, a hard kill, a service restart — and every plugin it
  launched kept running. Measured: two `memfs.exe` processes still alive, one of
  them an hour after the run that started it had gone.

  That is not merely untidy on this platform: a running plugin holds its own
  `.exe` open, so the next install or upgrade of it fails with a sharing
  violation, and the socket it still owns makes the next start look mysteriously
  broken. Plugins are now put in a **job object** with
  `KILL_ON_JOB_CLOSE`, so the kernel reaps them when filex's handles close,
  whether or not filex got to run a line of shutdown. Measured after: 1 plugin
  running, 0 surviving the same hard kill.

  Unix keeps its process group and deliberately does **not** use `Pdeathsig`:
  in Go it fires when the OS thread that forked exits, and the runtime retires
  idle threads, so a healthy plugin could be killed for no reason. An orphan is
  a nuisance; a plugin that dies at random is a bug report nobody can reproduce.


## [0.21.4] - 2026-08-19

### Security

- **A public demo no longer accepts a storage on the server's own filesystem.**
  The `local` driver means "a path on this host", and on a demo every
  admin-only door is a public door. Measured on this project's demo before the
  guard existed: storages rooted at `/data`, `/etc` and `/proc/1` were all
  accepted — the database, the configuration and the process environment of the
  machine. The check that was already there refused `/` and nothing else.

  Drivers that reach a backend the visitor brings (`s3`, `sftp`, `webdav`,
  `smb`, a plugin) are unaffected: a demo where nothing can be connected is not
  a demo.

### Fixed

- **A flaky test on the release path.** `TestStagedUpload_SuccessfulCommit…`
  sampled the node's state right after an asynchronous commit and expected to
  catch it in `staged`. On a loaded runner the worker got there first, so CI
  went red while the code was right — the worst way for a test to be wrong. The
  fake driver now blocks until the assertion has been made, which makes the
  window as long as the test needs instead of as long as the machine happens to
  allow.


## [0.21.3] - 2026-08-19

### Security

- **A demo instance no longer offers the plugin API.** `FILEX_DEMO_MODE`
  publishes an admin login — that is what a demo is for — and the plugin API is
  admin-only, so on a demo "admin-only" means anybody; installing a plugin makes
  filex execute an uploaded program on the host. Demo mode now turns the
  subsystem off unless `FILEX_PLUGINS_DISABLED=0` says otherwise in so many
  words.

  Found by measuring rather than reasoning: the project's own public demo was
  checked after the previous release, the published credentials logged in as
  `role=admin`, and `GET /api/admin/plugins` answered `200`. Nothing had been
  installed, and nothing was stopping it. If you run a filex demo with plugins
  from v0.21.0–0.21.2, set `FILEX_PLUGINS_DISABLED=1` now — upgrading also does
  it, but the switch works today.


## [0.21.2] - 2026-08-19

### Fixed

- **Signature enforcement could not be switched on.** `plugin.New` parsed and
  validated the trusted ed25519 keys into a local variable and never assigned
  them to the manager, so `requires_signature` stayed false and an unsigned
  plugin installed on an instance that had configured keys. Measured through a
  real server, not a unit test — every existing test set the field by hand,
  which is exactly why none of them noticed.
- **The trusted keys and the concurrency ceiling had no way in.** The rejection
  message named `FILEX_PLUGIN_TRUSTED_KEYS`, and nothing read it; `MaxInFlight`
  was likewise only reachable by an embedder. Both are configuration now
  (`FILEX_PLUGIN_TRUSTED_KEYS`, `FILEX_PLUGIN_MAX_INFLIGHT`).
- **A rejected plugin upload is a client error, not a server one.** Install
  failures were classified by searching the message for words like `sha256`, so
  a bad signature answered `500` while a missing one answered `400`. They are
  typed now (`plugin.RejectedError`) and both answer `400`.
- **The generated driver shapes had nothing checking them.** `gen/main.go`
  claimed a test asserted the committed file matched the generator; no such
  test existed. It does now — and it caught the generator emitting unformatted
  source on its first run.


**A plugin now has to prove what it claims, and it can be upgraded without
taking its storages down.**

- **Conformance — every declared capability is probed, and a plugin that fails
  its own claims is refused.** A plugin declares capabilities and filex acts on
  them: it registers a driver whose method set matches, and every surface then
  offers those operations. If the plugin declared `write` and its write is
  broken, the user meets an upload button that fails, a trash move that fails
  and a version snapshot that fails, and reads all three as **filex** being
  broken — the plugin is the faulty part, the product wears the fault. So the
  claims are measured in two places: at install and at every start, against a
  throwaway instance the plugin opens for the new `POST /v1/selftest`; and again
  whenever a storage on it is saved, against that storage's **real**
  configuration, in a scratch folder (`.filex-conformance-<random>`) that is
  removed afterwards. The second gate exists because the first cannot cover it —
  a self-test proves the code works, not that these credentials reach that
  bucket. Probes: `list`, `not_found`, `write`, `read` (bytes compared), `stat`
  (a size that lies breaks ranged serving, quota and sync three different ways),
  `list_after_write`, `range`, `set_mtime` (set then re-stat: a timestamp
  accepted and dropped makes every sync run copy everything again), `copy`,
  `move`, `mkdir`, `delete`, `delete_idempotent`, `presign`, `multipart`,
  `watch`. A failure names the probe, what was expected and what happened.
  `FILEX_PLUGIN_CONFORMANCE=enforce|warn|off`, `enforce` by default. A plugin
  with no self-test endpoint is still installed, but is marked **unverified**
  and probed when the first storage is saved on it. ⚠ What the probes cannot
  check is stated where it matters rather than hidden: `presign` is verified to
  return a URL that parses, **not** one a browser on another network can reach
  (filex may not share the client's network), and `watch` is verified to open a
  stream, not to deliver an event for every change.
- **`storage.Watcher` is finally consumed — and the previous release's
  documentation line about it is now wrong and has been corrected.** v0.21.1
  removed a promise that a change stream bought "change events without polling",
  because nothing subscribed to one. Now a storage in `fsnotify` mode resolves
  in order: inotify when the driver is local, **the driver's own stream** when
  it implements `Watcher` (today: a plugin), polling otherwise. Events are
  coalesced with the same 2-second debounce as the inotify loop and each batch
  triggers the same full run a poll would, so a missed or duplicated event costs
  a scan and never a wrong index — the stream is a hint about *when*, not a
  ledger of *what*. ⚠ A stream that **ends** (the plugin restarts, the
  connection drops) falls back to polling rather than leaving the storage frozen
  with a stale index.
- **Upgrade a plugin in place** — `POST /api/admin/plugins/{id}/upgrade`. The
  row, the name, the driver and every storage built on it survive: stop, swap
  the file, start, verify. Remove-then-install was the only route before, and it
  takes the registration with it — a storage whose driver has gone cannot open.
  **If the new binary does not come up, the previous one is restored and
  started**, and the call answers 400 with the plugin's current status attached,
  so a failed upgrade costs an error message rather than a plugin. ⚠ The plugin
  is stopped first on purpose: a running executable cannot be replaced on Linux
  (`ETXTBSY`).
- **Presigned URLs and multipart uploads over the plugin protocol**, with two
  new capabilities. `presign` lets a plugin hand out a URL the client uses
  directly — share downloads then redirect instead of streaming through filex.
  `multipart` is resumable upload in parts, used by the staged-upload commit
  path, which holds the bytes itself and therefore pushes each part through
  `PUT …/multipart/part` rather than handing out part URLs. New routes:
  `presign-upload`, `presign-download`, `multipart/init|part|complete|abort`,
  plus `POST /v1/selftest`. `multipart` without `write`+`delete` is refused at
  describe time: a resumable upload is still an upload. The Go SDK gains
  `Plugin[T].SelfTest` and the optional `Presigner` / `Multipart` interfaces.
- **ed25519 signature verification for plugin binaries.** With trusted keys
  configured (`plugin.Options.TrustedKeys`, hex or base64), install and upgrade
  both refuse an unsigned or badly signed binary, and the admin API reports
  `requires_signature` so a surface can ask for the signature before the
  rejection rather than after it. What is signed is the binary's lower-case hex
  **sha256**, so an operator can sign the digest they already publish. ⚠ The
  checksum an install already required only proves the file has not changed
  since it arrived — never who it came from; that is the gap this closes.
  ⚠⚠ There is **no environment variable for the keys yet**: the rejection
  message names `FILEX_PLUGIN_TRUSTED_KEYS`, but nothing reads it, so on a stock
  server signature enforcement is off.
- **A ceiling on what a plugin may cost filex.** A plugin is somebody else's
  program in filex's request path, and a backend that accepts connections and
  then says nothing is indistinguishable from one that is merely busy. Each
  plugin now gets 10 concurrent operations (`DefaultMaxInFlight`); a caller that
  cannot get a slot within 5 s is told the storage is saturated instead of
  joining a queue nobody drains. Metadata operations get a 60-second deadline;
  streaming reads and writes deliberately get none, because a 20 GB upload is
  legitimately slow. A plugin's stdout/stderr is rate-limited to 50 lines/s
  (burst 200) with the dropped count reported — a chatty debug build was
  otherwise filex filling the disk, whose first symptom is "the server ran out
  of space", not "the plugin is noisy". On Linux and macOS a plugin is started
  in its own **process group** and killed as one, so a helper it spawned cannot
  outlive it holding the socket. ⚠⚠ filex is **not** a sandbox and the code now
  says so plainly: memory and file-descriptor limits are not set (Go cannot
  apply an rlimit to a child between fork and exec, and capping the parent would
  cap filex), and Windows has neither process groups nor rlimits here.
- **Plugin metrics** — `filex_plugin_ops_total{plugin,op,outcome}`,
  `filex_plugin_op_duration_seconds`, `filex_plugin_in_flight`,
  `filex_plugin_restarts_total`, `filex_plugin_up`. `busy` is its own outcome
  because saturation is a sizing problem, not a fault to chase, and
  `restarts_total` is how a plugin that restarts in a loop becomes visible at
  all — filex retries the instance once, so single requests keep working while
  the process dies every few seconds. Conformance probes and the server-side
  multipart part push are deliberately outside both the ceiling and the
  counters.
- **The driver shapes are generated** (`internal/plugin/gen`, 20 combinations).
  filex reads capabilities by type-asserting optional interfaces at forty-odd
  call sites, so a plugin that cannot write must be handed over as a value with
  **no** `Write` method. With five optional axes that is twenty structs, and
  twenty hand-written structs is where somebody eventually embeds the wrong
  thing and a read-only plugin quietly becomes writable.
- **The Python example gained a self-test area and multipart**, and its
  `acceptance.sh` grew from 11 measured steps to 17 — conformance at install,
  a plugin deliberately edited to **lie** about its writes (accepted by the
  install call, then refused, driver never registered, storage impossible to
  create), multipart, upgrade, upgrade rollback, and the live load figures.

## [0.21.1] - 2026-08-19

- **Copy or move into a storage's root was refused — for every driver.**
  Pasting at the top of a storage sends `<storage>://` as the destination; the
  handler turned that into an empty destination (with a comment saying
  "storage root"), and the operations queue refuses an empty destination with
  `ops: dest required`. Two halves of the same feature disagreed about what
  empty meant, so the paste failed on local disks, S3 and everything else.
  Measured on a built-in local storage. The root is now `/`, which is also
  what the queue keys off to drop a file *into* a directory rather than rename
  it; the join uses `path.Join`, so the root case produces `f.txt` and not
  `/f.txt` — harmless on a disk, a real object with an empty first path
  segment on S3. Pasting into a root also skipped the permission check before
  (an empty destination was not checked); it is checked now.
- **The plugin docs promised a change stream nothing subscribes to.**
  `Watcher` was listed as buying "change events without polling". Nothing in
  filex consumes `storage.Watcher`, no built-in driver implements it, and the
  only event-driven sync mode works on the local driver alone. The protocol
  keeps the endpoint — it costs a plugin nothing and stays forward-compatible
  — but the docs, the SDK and the protocol comments now say plainly that a
  watch-driven sync mode does not exist yet.
- **A second example plugin, in Python, with no SDK**
  (`backend/examples/plugin-diskfs`): the same protocol implemented by hand,
  backed by a real directory, with every optional capability. Its
  `acceptance.sh` drives the whole subsystem through filex — including a 25 MB
  transfer, native ranged reads, trash and restore, a plugin killed with `-9`
  mid-life, the remote kind with a sealed token, and a read-only plugin whose
  driver has no write methods at all.

## [0.21.0] - 2026-08-19

**A storage driver can now live outside the binary.** Somebody who writes
their own storage system could not teach filex to speak it without forking
filex; now they write a program, install it from the admin panel, and their
driver appears in the ordinary storage picker.

- **Plugins** — a plugin is a separate process. filex launches it (or connects
  to one you run), asks what it can do over a small HTTP/JSON protocol, and
  registers it as `plugin:<driver>`. Any language can implement the protocol;
  a plugin crash cannot take filex down; a plugin ships on its own schedule.
  Its **config form comes from the plugin's own describe**, which is what lets
  a driver that did not exist when the frontend was built render in the admin
  UI with no frontend release.
- **What a plugin does not implement is either emulated or honestly absent.**
  Ranged reads, move and copy are emulated by the host; `set_mtime` is only
  offered when the plugin really stores it, because filex can tell "not
  supported" from "applied" but not "applied" from "pretended". A read-only
  plugin is handed to filex as a value with **no** write methods at all, so
  the UI does not offer an upload button that fails at the last moment.
- **Go SDK** (`backend/pkg/pluginsdk`) — implement three methods, call
  `Serve`. Capabilities are derived from the type, so a plugin cannot claim
  one it did not write or hide one it did. A complete example lives in
  `backend/examples/plugin-memfs`, and the test suite builds and runs it.
- **Admin → Plugins** — install by upload, by URL with a required SHA256, or
  as a remote service; enable, disable, restart, remove. The page says what
  will stop working before you remove a plugin that storages are using.
- Security posture, stated rather than implied: a plugin runs with filex's
  privileges and receives the credentials of every storage on it, so the UI
  says so before the install button; a remote plugin's token is sealed with
  `FILEX_SECRET_KEY` (registering one without that key is refused rather than
  stored in plaintext); a launched plugin must listen on loopback; and an
  installed binary's SHA256 is re-checked on **every** start, so a file that
  changed under filex is refused rather than run.
- `FILEX_PLUGINS_DISABLED=1` turns the subsystem off entirely. In
  multi-tenant mode the surface is supertenant-only.
- **Fixed while proving it on Windows:** a plugin uploaded without an
  extension was stored as `memfs` and never started — on Windows the
  extension is what makes a file executable, and Go reports that as
  `executable file not found in %PATH%`, which reads like the file is missing
  when it is sitting right there. Binaries now get `.exe` unless they already
  carry an executable extension.

Migration **00029** (`plugins`). Docs: [PLUGINS.md](docs/PLUGINS.md).

## [0.20.3] - 2026-08-19

Three desktop fixes that came out of a real macOS 26 (Apple Silicon)
deployment (Berk, PR #9), one Connections-page fix, and — as a consequence of
the first — **macOS packages** on the release for the first time.

- **Desktop pairing died in a browser that was already signed in.** The
  router's "signed-in users skip /login" redirect destroyed the query string
  before the login view could stash `desktop_state`/`desktop_challenge`, so
  the browser showed a file manager with no code and no error while the app
  waited forever. The stash now happens in the router guard, ahead of the
  first `await`; a failed hand-off keeps the overlay and says so instead of
  disappearing.
- **The embedded sync engine was x86_64 inside an arm64 app.** `fetch-cli.mjs`
  defaulted `GOARCH` to `amd64`; every launch on Apple Silicon raised macOS
  26's Rosetta deprecation alert. It now follows the host arch (an explicit
  `GOARCH` still wins; CI's x64 runners are unaffected).
- **macOS packages: `filex-desktop-arm64.dmg` + `.zip`**, built on a pinned
  `macos-14` runner. Unsigned, but *ad-hoc sealed* by an `afterPack` hook: a
  no-certificate electron-builder output is only linker-signed, and macOS 26
  treats that as tampering ("malware blocked and moved to Trash", no override);
  the deep ad-hoc re-seal turns it into the ordinary "unverified developer /
  Open Anyway" dialog. Auto-update on macOS stays inert until the app carries a
  Developer ID (Squirrel.Mac refuses to swap an unsigned app); the zip and
  `latest-mac.yml` ship anyway so the feed is right the day it does. The docs,
  the README and the web app's download banner now list macOS honestly —
  Apple Silicon only, unsigned, first-launch step included.
- **Connections page in dark mode: the panel painted its own page ground, and
  five theme tokens did not exist.** `.fe-conn` set `background: var(--fe-bg)`
  and drew a blue-black rectangle over the admin's zinc page (and a white one
  over the light page) that ended where the panel ended; the API-tokens box
  referenced `--fe-surface`, `--fe-muted`, `--fe-accent`, `--fe-surface-2` and
  `--fe-mono`, none declared, so it had no ground, un-muted muted text and a
  hardcoded-blue button next to a token-blue one. Fixed in the shared package
  (web admin and the desktop app render the same component), 17 phantom token
  uses corrected across core, and a test now refuses any `var(--fe-*)` that
  `variables.css` does not declare.
- docs site: the hourly release rebuild had failed silently since 08-11 (no
  `PATH` under cron); it now sets its own, reports failure, and refuses to
  publish an empty release list.

## [0.20.2] - 2026-08-17

Packaging only — the server is identical to 0.20.1. It exists because the
desktop CI job that 0.20.1 introduced had two faults, and both are the kind
that produce a package nobody ever receives:

- **The upload ran before the release existed.** The job built a 125 MB
  AppImage and an 87 MB `.deb` and attached neither, because `gh release
  upload` needs the GitHub Release that goreleaser creates in a different job.
- **The version came from `desktop/package.json`, not the tag.** That file is
  edited by hand and still said 0.20.0, so `latest.yml` — the auto-update feed
  — would have advertised 0.20.0. An app already on 0.20.0 would have fetched
  it, compared equal, and reported itself up to date for good.

## [0.20.1] - 2026-08-17

### Fixed

- **A host name in `FILEX_FTPS_PUBLIC_HOST` stopped the FTPS listener.** The
  setting is documented as "the address to advertise for passive connections",
  so a host name is the obvious value — and it made FTPS refuse to start with
  `invalid passive IP`, while `/healthz` answered 200 and every other endpoint
  came up. One protocol was simply absent, and the only trace was a single
  line in the startup log. A name is now resolved once at startup; a literal
  address still passes through, and empty still means "answer with the control
  connection's own address". The two remaining refusals name the setting and
  the fix, because the old message named neither.

### Changed

- **Desktop packages are built on every tag.** They were produced by hand,
  which is why they drifted: 0.20.0 shipped a server whose headline feature
  was the protocol endpoints while the installed desktop app was still 0.18.2,
  so the Connections and Tokens screens did not exist for anyone who had it.
  Windows and Linux build in CI and attach to the release. macOS is
  deliberately absent until there is a Developer ID certificate — an unsigned
  build is refused by Gatekeeper with a message that reads like a corrupt
  download.

## [0.20.0] - 2026-08-17

### Added

- **filex is now reachable as S3, SFTP, FTPS and NFSv3 — not only as HTTP.**

  The rule this follows: *whatever filex can connect to, it must be connectable
  as*. It could already use S3, SFTP, FTP and WebDAV as storages; now those
  clients can point at filex itself. `rclone`, `restic`, `aws s3`, `mc`, `s3fs`,
  OpenSSH, `sshfs`, WinSCP, FileZilla, `lftp`, `curl --ssl-reqd`, a scanner that
  only ever learned FTP and a media player that only ever learned NFS all land
  in the same tree — with the same RBAC grants, the same trash, the same quota,
  the same search index and the same audit trail as the web UI, because every
  protocol writes through one funnel.

  Each has a credential you can revoke on its own (S3 access keys, SSH public
  keys, API tokens, NFS export paths), and every one of them resolves its caller
  through a single door, `internal/protocolauth`. ⚠ That door exists because of
  a real incident: a protocol that authenticates outside the HTTP middleware
  chain starts with **no tenant scope**, and "no scope" means "see everything" —
  which is how a tenant admin who mapped `/dav` once got all ten tenants
  read-write. It is now impossible to attach an identity and forget the scope,
  because they arrive together or not at all.

  ⚠ An account with **2FA enabled cannot use its password** on any of these.
  None of these protocols has a channel for a second factor, so accepting the
  password would make each of them a documented 2FA bypass; such an account
  mints a token, a key or an access key instead.

  See [docs/PROTOCOLS.md](docs/PROTOCOLS.md). The connection instructions are in
  the app — *Connections → Connect* builds every command from the live
  deployment, so what is on screen is what works.

  - **S3** (`FILEX_S3`, on by default) — SigV4 verified by hand and checked
    against the SDK's own signer; a bucket **is** a storage; ListObjectsV2 *and*
    V1, delimiters, real ranges, composite ETags, `x-amz-meta-mtime`, multipart
    on filex's staging area, the modern `x-amz-checksum-*` contract in header
    *and* trailer form, aws-chunked bodies, and directory markers so `mkdir`
    works over `s3fs`. Path-style and virtual-hosted addressing both work.
  - **SFTP** (`FILEX_SFTP`, off by default, `:2022`) — password (a token) or a
    registered public key, posix-rename, `statvfs` reporting your quota so `df`
    is right, permission bits synthesised from your ACL level. ⚠ Only the `sftp`
    subsystem is served; `exec` and `shell` are refused, because answering an
    exec request is how a file server grows a command-execution surface.
  - **FTPS** (`FILEX_FTPS`, off by default, `:2121`) — explicit TLS **mandatory**
    on control *and* data, passive-only, ASCII conversion **off** (it rewrites
    line endings, which on a file a client guessed wrong about is silent
    corruption), `REST`/`APPE` resume.
  - **NFSv3** (`FILEX_NFS`, off by default, `:2049`) — ⚠⚠ unencrypted, so LAN or
    VPN only. NFSv3 cannot authenticate a request in a way filex can use, so the
    identity is bound to the **export path**, which carries 32 bytes of entropy:
    the path *is* the credential, the mount is pinned to one account, and the
    uid/gid on each request is discarded rather than trusted.

- **`filex mount`** — a remote filex server attached to a folder on this
  machine, over the same HTTPS the browser uses. The only one of these that
  works from anywhere: NFS needs a LAN, an SFTP mount needs sshfs or WinFsp
  configured, this needs a URL and a token, and it reaches the server through
  whatever proxy sits in between because underneath it is the REST API.

  ⚠ **It is not a sync.** Nothing is copied but a bounded read cache, so it
  opens one file out of a hundred thousand without downloading the rest;
  `filex sync` is still the answer for having the files offline.

  ⚠ Linux at first; Windows landed in the same release — see the next entry.

- **`filex mount` on Windows** — a real drive letter (`filex mount Z:`), over the
  same HTTPS as everywhere else, with the same permissions, trash and quota.
  This is what replaces the SMB server: the thing people wanted from SMB was a
  drive on Windows without installing anything unusual.

  ⚠ It is still ONE binary. The objection that made this look impossible was
  CGO — filex ships `CGO_ENABLED=0` everywhere — and cgofuse has a CGO-free path
  on Windows that loads WinFsp's DLL at run time instead of linking it. WinFsp
  (free, [winfsp.dev](https://winfsp.dev)) is installed once by the user; filex
  neither ships nor fetches it.

  ⛔ **macOS is not supported and the command refuses there**, rather than
  appearing to work and doing nothing: macFUSE's Go binding needs a C toolchain
  filex deliberately does not use, and its licence forbids a commercial program
  from installing it. Use folder sync or the desktop app.

- **API tokens can be minted from the connections surface** — in the admin
  panel, the web explorer and the desktop app, from the same component.

  ⚠ Three of the protocols take an API token as their password (FTPS, WebDAV,
  `filex mount`) and their guides say so. Until now the only screen that could
  make one was the admin panel's, so a normal user read the instruction and had
  nowhere to follow it. The route itself was never restricted — only the UI was
  missing, and being an admin hid that completely.

- **SMB / CIFS storage driver** — a NAS, a Windows file server or a Samba box as
  a filex storage. ⚠ The library choice was a licence decision: the maintained
  fork pulls an **LGPL-3.0** dependency into filex's statically linked binary,
  which would stop being satisfiable the day filex ships closed-source, so the
  **BSD-2** upstream was used instead.

- **Every account has a username**, and every surface accepts the e-mail **or**
  the username. An `@` in an SSH or FTP login has to be quoted in most clients'
  config files, which is what this is for.


- **Prometheus metrics at `GET /metrics`**, behind the same admin gate as every
  other operator endpoint (filex is routinely on the public internet, and the
  exposition names storages, counts accounts and shows traffic shape). Staged
  uploads in flight, bytes staged, commits, failures, chunk retries and aborts;
  the staging sweeper's passes and removals; every guard refusal, labelled;
  per-storage transfer duration and bytes; quota usage; and the Go runtime
  metrics. Scrape config and the alerts worth having are in
  **[docs/METRICS.md](docs/METRICS.md)**.

- **`internal/throughput` — one rolling bytes/sec per storage.** Published as
  `filex_storage_throughput_bytes_per_second` and read by
  `internal/filecache` to decide whether a storage is slow enough to be worth
  caching. Deliberately one signal with two consumers: a cache that measured
  slowness its own way would disagree with the dashboard the operator is looking
  at. `Rate` distinguishes "unknown" from "zero", because treating silence as
  slowness would make every fresh boot behave like a NAS on a phone line.

  It measures the time spent inside the driver's `Read` calls, not wall clock
  across the transfer: a download is paced by whoever is downloading, so wall
  clock would let one person on a bad connection mark a fast bucket slow for
  everybody — and publish that as *storage* throughput on the dashboard.
  `internal/filecache` keeps the policy (a measured-fast storage overrules an
  operator's `slow: true` flag; nothing is decided on fewer than three reads
  big enough to mean anything) and asks `throughput.StatAbove` for the rate and
  the evidence behind it, rather than keeping samples of its own.

- **The staging sweeper logs every pass**, including the ones that remove
  nothing — a sweeper that only speaks when it deletes something is
  indistinguishable from a sweeper that has stopped running, and this project
  has already lost 29 GB to temp files nobody was watching. The in-flight and
  staged-bytes gauges are re-measured against the directory on each pass, so a
  restart cannot leave the dashboard lying.

### Fixed

- **On Windows, deleting a file you had just uploaded could fail with a 500.**
  About three times in two hundred, measured. Deleting moves the file into
  `.filex-trash/` with a rename, and on Windows a rename fails outright while
  any handle is open on the file.

  ⚠⚠ And the handle was often filex's own. Go opens files without
  `FILE_SHARE_DELETE`, so the thumbnailer, the content indexer or a download in
  flight blocked the rename of the very file they were reading — filex standing
  on its own foot. A real-time virus scanner does the same on a freshly written
  file and is outside anybody's control, so the holder cannot be removed, only
  waited out; every one of them lets go in milliseconds. The local driver now
  retries a rename or an unlink for up to a second while the filesystem says
  the file is held, and only for that class of error — "no such file" is an
  answer and still comes back immediately. Unix is unaffected: a rename there
  succeeds while the file is open.

- **Revoking a credential now reaches the session it already opened.** "Delete
  the token" did what the operator asked and not what they meant: the token
  could no longer be used to log in, and the SFTP session it had opened kept
  reading and writing files. Same for disabling an account, suspending a tenant,
  revoking an SSH key or taking away a grant — every one of them was true for
  the next login and false for the connection in flight.

  Every credential check happens once, at authentication. Over HTTP that is the
  same as "on every request"; for a protocol where one authentication is
  followed by hours of file operations — or, for an NFS mount, days — it is not.
  Live sessions are now registered and re-checked every 30 seconds, and the ones
  whose credential no longer resolves are cut: the connection is closed for SFTP
  and FTPS, and marked for NFS, which has no connection to close. Deleting or
  disabling a credential also kicks its sessions immediately. ⚠ The sweep is the
  guarantee, not the kick — it is what covers the paths nobody wired: an admin
  disabling an account, an expiry passing, a row edited in the database.

  ⚠⚠ The quieter half of the same bug: the **grant set** was cached for the life
  of the caller, so a permission removed at 09:00 kept serving files until the
  user logged out. It now expires on the same TTL as the password cache — one
  number, one answer to "when does it stop working".

- **A folder grant was unreachable over SFTP, FTPS and NFS.** A grant is
  per-folder, so a caller can hold viewer on `main/projects/acme` and nothing on
  `main`. All three asked for viewer on the folder being *listed*, which refuses
  the two levels above the grant — so `ls /main` answered "no such file" to a
  user who had been granted a subfolder of it, and the folder they were actually
  given could not be reached. Listing and stat now use the traversal rule the web
  UI, `/dav` and the S3 listing always used; reading a file's bytes still
  requires viewer on the file itself, so traversal never becomes access.

- **`/dav` enforced no quota at all.** Every other write surface did — manager,
  AI, ShareX, S3, SFTP, FTPS, NFS. A user at their ceiling could keep writing
  indefinitely by mapping a drive, and because the bytes are counted *after* the
  write, the number in the admin panel simply climbed past the limit. It is now
  refused with **507 Insufficient Storage** before the upload starts.

- **WebDAV locks survive a restart.** They lived in a map, so a deploy silently
  forgot every one of them: a client that took a lock before the restart
  presented a token that named nothing, its save failed with 412, and the server
  would meanwhile have let somebody else lock the same file. The lock said
  "exclusive" and stopped being true without telling anybody.

- **An upload to a folder with no database row wrote the file and created no
  rows at all.** Uploading to `main://newdir/a.txt` left the bytes on disk, the
  subfolder listing found them through the driver fallback, and the level above
  was **empty** — a folder you just uploaded into that did not exist until the
  next sync run. It hit the web explorer, the CLI and the AI upload path equally.


- **A folder share's ZIP no longer outlives the share.** The cache of
  "download all" archives had exactly one cleanup — dropping *older signatures
  of a folder that is still shared* — so when a share expired, was revoked or
  ran out of downloads, its archive simply stayed. Measured on a live instance:
  a 16.7 GB folder was shared for **eleven minutes**, the warmer read the whole
  folder from S3 for **three hours** after the link had already died, wrote a
  **15 GB** archive that was never downloaded once, and that file then went into
  the backup and was mirrored to the disaster-recovery host three times over. A
  disk cleaned from 96 % to 90 % was back at 99 % two days later.

  Three changes, in order of what they save:

  - **Every warmer pass sweeps.** Any `<node>-<sig>.zip` whose node has no
    active folder share is deleted, along with the temp files of builds that
    died with a restart (unclaimed, older than an hour). It reads the same
    share listing the warmer builds from, so there is one definition of
    "active"; it deletes nothing at all until that listing has succeeded once,
    keeps the archives of nodes that are still shared, keeps files a build is
    writing, and does not touch any name that is not one of its own.
  - **A build stops when its share does.** The build still runs detached from
    the request that started it — a downloader who hangs up must not kill an
    archive other people are waiting for — but it now asks, during the walk and
    during a single long file, whether the share still exists, and abandons the
    build and its partial file when it does not. It refuses nothing: a live
    share is built however large it is, and the on-demand path is untouched.
  - **The cache moved out of the data directory** to `<data_dir>/cache/sharezips`
    (existing archives are moved on first start). It is regenerable, and it was
    sitting where every backup, rsync and restore would pick it up. Backup
    guidance in [DEPLOYMENT.md](docs/DEPLOYMENT.md) and
    [SHARING.md](docs/SHARING.md).

- **Quota accounting was dead code, and nobody could have noticed.**
  `quota.AddUsage` and `Store.SetNodeOwner` had **no callers anywhere in the
  tree**: `users.usage_bytes` was never incremented, `GetNodeOwner` always
  returned `nil`, and so the release at trash-purge — the one place it was
  called — could never run either. Nothing was counted, so nothing was ever
  refused. Measured on a real instance: with a **2 MiB** quota, an **8 MiB**
  upload returned `200`, the bytes landed on disk, and `GET
  /api/files/quota/me` still read `used_bytes: 0`.

  The fix is one place, not nine: `internal/quotastore`, a `db.Store`
  decorator over `CreateNode`, `UpdateNodeMeta` and `HardDeleteNode`. Every
  write surface — browser upload, staged upload, staged ingest, WebDAV `PUT`,
  the public file drop, ShareX, the AI/REST API, save-text, archive extract,
  copy — reaches it through the store, so none of them carries quota code and a
  path added later is counted the day it is written. The rules for overwrite,
  move, trash, restore, copy and purge are written down in
  **[docs/QUOTAS.md](docs/QUOTAS.md)**.

  Two attribution holes turned up while wiring it, each a bug of its own:

  - **WebDAV never put the authenticated account on the request context.** It
    authenticates itself (HTTP Basic) and so never ran `auth.Middleware`,
    leaving `auth.UserFrom` nil for every `/dav` write — nodes owned by nobody,
    and file events with **no actor**.
  - **`save-text` created no node row for a new file.** The bytes went to the
    driver and the catalogue only learned about them on the next storage scan,
    so the file was invisible until then and the row it eventually got belonged
    to nobody.

  `RecomputeUserUsage` also filtered `deleted_at IS NULL`, which put the
  reconciler at odds with the rule it was reconciling: a recompute forgave every
  trashed byte, and the purge then released them a second time (clamped at zero,
  so the drift never surfaced as an error).

- **The per-user ceiling now holds on the synchronous upload path too.** Only
  the staged path checked it, so anything under the staging threshold had no
  ceiling at all — a user could pass their limit a few megabytes at a time.

- **`FILEX_SYNC_INTERVAL` does something.** It was parsed into config and read
  by nothing, while the real fallback was a hardcoded `15m` in the poll loop
  that happened to equal the documented default — which is exactly why the dead
  knob was invisible.

### Removed

- **`FILEX_SYNC_WORKERS`.** It was documented as "concurrent storage sync
  workers" and parsed into a field nothing read. There is no pool to size: the
  sync worker runs one goroutine per enabled storage and always has. Setting it
  now has no effect and produces no error. Documenting a knob that does nothing
  is worse than either wiring it up or deleting it.

## [0.19.0] - 2026-08-14

### Added

- **The desktop app has a language setting.** *Settings → Language*: System,
  English or Türkçe. It followed the OS and offered nothing to choose, so
  somebody on an English Windows could not have a Turkish filex — and the
  server's own panel has had a language switcher all along.

  The choice moves the whole app at once, which is the part worth saying: this
  window, the tray menu (built in the main process, which had its labels
  hard-coded in English), and the file explorer inside it — a separate component
  with its own catalogue. Switching is immediate and keeps the folder you are
  looking at; `system` still resolves against the OS at read time, so a laptop
  that changes its language keeps working.

  ⚠ The explorer is updated through its `config` property, not the `locale`
  attribute: the component merges `{...attributes, ...config}` and config wins,
  so setting the attribute changed what the element *reported* while the list on
  screen stayed English. The first version of the test asked the element for its
  locale and passed — a screenshot is what caught it. `scripts/lang-e2e.mjs`
  now reads the rendered text instead, and checks the shell, the stored setting,
  the main process's own resolution, and a restart.

## [0.18.2] - 2026-08-14

### Changed

- **The Windows app installs per-user, always.** The silent update in 0.18.1 was
  only half a promise on a machine where the app had been installed for all
  users: `C:\Program Files` needs administrator rights to write, so every
  background update ended in a UAC prompt — the app could not update itself
  while nobody was at the keyboard, which is the whole point. Discord, Slack and
  VS Code's user installer all land in `%LOCALAPPDATA%` for the same reason.

  The installer no longer offers the choice (`perMachine: false`,
  `allowElevation: false`): one click on "for all users" was enough to put the
  app somewhere its own updater could not reach. Settings and accounts live in
  `%APPDATA%\@brftech\filex-desktop`, outside the install directory, so moving
  an existing install is uninstall-then-install and nothing is lost.

## [0.18.1] - 2026-08-14

### Fixed

- **The desktop app's update handed you an installer window.** Downloading was
  already quiet, and quitting the app already installed silently — but the tray
  entry and the Settings button called `quitAndInstall()`, whose default is
  `isSilent = false`: it runs the NSIS installer with its full wizard. So the
  one visible path through the feature was the one that made a background
  updater feel like being sent back through setup.

  The update now applies itself and the app comes back where it was — in the
  tray. Every install is silent and relaunches (`quitAndInstall(true, true)`),
  and the relaunch is treated like a hidden launch (`--updated`, the flag the
  installer passes us) so no window appears in front of whatever you were doing.

  Because this app lives in the tray for days, it no longer waits for a quit
  that may never come: once an update is downloaded it watches for a quiet
  moment — the machine idle for ten minutes with no window open — and swaps
  itself then, stopping the sync watchers first so no transfer is interrupted.
  Nothing prompts, nothing nags; the tray line now says the update installs
  itself, and clicking it only means "sooner".

  `scripts/update-e2e.mjs` grew two guards against exactly this regression: no
  `quitAndInstall` may omit the silent flags, and the post-update relaunch must
  stay in the tray.

## [0.18.0] - 2026-08-14

### Fixed

- **A share link capped at N downloads handed out more than N.** The cap was
  checked against a counter bumped only *after* the bytes had left, so every
  request that started while an earlier one was still streaming read the same
  pre-download count and was let through. Measured against the shipped build: a
  link capped at **one** download served **three complete files** to three
  overlapping clients; "3 downloads" became four whenever the next click landed
  before the previous transfer finished — which, for anything larger than a text
  file, is most of the time.

  A download is now claimed against the cap in a single statement *before*
  anything is served — on the file itself, on a folder's "download all" ZIP and
  on a single file fetched from a shared folder's browse page alike. A serve
  that fails before a single byte leaves gives its slot back; a transfer the
  visitor abandons half-way has still spent one. The claim is written on a
  context detached from the request, so a client that hangs up cannot make the
  record of its own download disappear.

- **The share dialog's "Create link" button was pushed out of place** by the
  download-limit control added in 0.16.2. The options were one wrapping flex row
  with the button shoved to its right end by `margin-left: auto`; that survives
  two controls and breaks with three. The options are a two-column grid now and
  the action sits underneath at full width, which holds its shape whatever gets
  added next.

### Added

- **The one-line `curl` is back in the share dialog.** It belonged to the old
  standalone share dialog and was left behind when link creation moved into the
  "Share / Permissions" panel — exactly how the download limit had been lost. A
  share link is regularly minted *for a server* ("pull this onto the box"), and
  that reader has no browser. Both surfaces build the command from one helper
  now, so they cannot drift: folder links get `?zip=wait` and a `.zip` output
  name, PIN-protected links carry their PIN.

- **Profile pictures.** Set one in the admin panel → *My profile*, and the
  explorer's collaboration strip draws it instead of your initials — for every
  client of the account, including the desktop app and any API key minted under
  it, which is what makes it worth setting once. Stored on the user row as a
  small data URI (migration 00023), downscaled to 160px in the browser. A shared
  proxy token keeps initials rather than wearing its owner's face, and a host
  proxy that re-identifies an end user may supply that person's own picture with
  `X-Filex-Presence-Avatar`.

### Changed

- **The README screenshots are retaken, in English, against the current UI.**
  `share-modal.png` was showing a share dialog with no download limit — a
  control two releases old — and `viewer-markdown.png` had Turkish buttons in
  it. They are reproducible now: `node e2e/shots/capture.mjs` boots an instance,
  seeds a demo tree and captures the set, and reviewing them is a numbered step
  in the release process (`docs/CONTRIBUTING.md`).

## [0.17.1] - 2026-08-12

### Fixed

- **The desktop package could ship without its updater.** In a pnpm workspace
  `desktop/node_modules/<dep>` is a symlink into the repo root's store, outside
  the app directory, and electron-builder does not follow those — so
  `electron-updater` was absent from the asar. Adding `node_modules/**/*` to
  `files` did not help: the installer came out byte for byte identical. The main
  process is bundled with esbuild now (electron external), so the package
  carries our code and nothing else and packaging no longer depends on how the
  install happens to be linked.

  The symptom was the quiet kind — the installer built, the app launched, and no
  window ever appeared: the main process threw on the import before creating
  one, with no dialog and no log.

  `desktop/scripts/update-e2e.mjs` now points a packaged app at a local feed
  advertising a newer version and watches it download and stage the update for
  real. "The installer built" is not "the app works", and "latest.yml returns
  200" is not "the app updates".

  ⚠ 0.17.0's packages were built from this fix; its tag was not. Rebuilding the
  desktop package from the 0.17.0 tag reproduces the broken one — this release
  is the first whose tag matches what ships.

## [0.17.0] - 2026-08-12

### Added

- **The desktop app keeps itself up to date.** A file manager that syncs folders
  in the background is exactly the kind of program nobody thinks to go and
  re-download; the packages were being installed by hand, so every fix reached
  only whoever remembered to fetch it.

  The feed is a plain static directory on filex.sh — deliberately not the GitHub
  provider, whose private repository would require a token shipped inside the
  app. It downloads quietly and installs **on quit**: an update that interrupts
  a transfer to restart itself is worse than one that waits, and the app
  normally lives in the tray, so quitting is a real moment. The tray grows a
  "Restart to update" line when one is staged, and Settings gained an Updates
  row (state + "Check now").

  Failures are silent by design — no network, a proxy, a feed that 404s: none of
  that is the user's problem while the app works — but the state is recorded so
  Settings can report it. `FILEX_NO_UPDATE=1` turns the whole thing off.

  ⚠ This is the first version that can update itself; reaching it still needs
  one manual install.

## [0.16.3] - 2026-08-12

### Fixed

- **The tab strip appeared in the desktop app but not on the web.** 0.16.0 added
  `tabStrip` and defaulted it to `'auto'`, opting only the desktop app into
  `'always'` — so tabs were permanent in the app and came and went in the web
  explorer and the embeds. This package exists so those surfaces are one
  product; a default that differs between them turns it back into three. The
  default is `'always'` everywhere now, and `'auto'` remains as a deliberate
  opt-out for an embed too short to spend a row on.

- **A scrollbar appeared under the tabs.** 0.15.0's themed-scrollbar block sat
  at the END of the stylesheet, where it outranked the strip's own
  `scrollbar-width: none` (same specificity, later wins) and put a bar across a
  30px row. The generic block now sits at the TOP, before the component rules —
  a general default belongs before the exceptions that override it.

- **The strip grew a vertical scrollbar with nothing to scroll.**
  `overflow-x: auto` alone makes the other axis compute to `auto` as well, so
  the row of tabs had a vertical scroll context it could never use.
  `overflow-y: hidden` is explicit now.

- **Enough tabs ran off the edge with no way to reach them.** The strip did not
  overflow — it GREW: `<filex-explorer>` is almost always a flex item, a flex
  item's default `min-width: auto` floors it at its content's min-content width,
  and so a wide row of tabs pushed the host (and the page) sideways instead of
  handing the overflow to the scroller inside it. Measured before: 63 tabs → a
  3256px host inside a 1264px window, layout running off the right edge, no
  scrollbar anywhere. The host is now `min-width: 0; max-width: 100%` and the
  strip scrolls sideways with a thin bar. Every embedder would otherwise have
  had to know to write that themselves.

- **"New tab" scrolled away with the tabs it creates.** The `+` lived inside the
  scrolling area; past a dozen tabs it was off the right edge, reachable only by
  scrolling a strip most people do not know scrolls. It is pinned outside it.

## [0.16.2] - 2026-08-12

### Fixed

- **A share link changed language when you entered its PIN.** The public pages
  grew one at a time and each picked its own: the PIN gate was English, the
  folder page behind it Turkish, and the "PIN accepted" screen managed both at
  once — an English `<title>` over a Turkish heading.

  There is no session and no user on these pages — a share link is opened by
  strangers — so the language now comes from the request: an explicit `?lang=`,
  then `Accept-Language`, then the server's own `default_locale`, then English.
  It is resolved ONCE per request and handed to the template, so a page cannot
  mix two languages. Covers the PIN gate, the wrong-PIN line, the unlocked
  screen, the ZIP progress page, the error pages and the folder listing.

  ⚠ The counts line ("1 klasör · 2 dosya" / "1 folder · 2 files") is formatted
  by the handler: the two languages do not share a word order, so a template
  that glued numbers to nouns could only ever be right in one of them.

- **The download limit had no way to be set.** It lived in the standalone share
  dialog, and when link creation moved into the Share / Permissions panel the
  field was left behind — the server has honoured `max_downloads` the whole
  time, and nothing could give it a value. It is back, next to Expiry:
  Unlimited / 1 / 3 / 5 / 10 / 25.

  `desktop/scripts/share-limit-e2e.mjs` drives the real panel and then asks the
  SERVER what it stored, because a control that renders but never reaches the
  API would pass a DOM-only check.

## [0.16.1] - 2026-08-12

### Changed

- **A shared folder's gallery tiles are rendered when the link is created**, not
  when the first visitor arrives. 0.16.0 stopped the page shipping originals;
  this makes the *first* visit fast too, which is the visit that matters —
  whoever creates a link normally opens it straight away to check it, and that
  is exactly when the page used to crawl. Bounded at 500 tiles per share (a
  photo archive must not be re-rendered wholesale because somebody minted a
  link); anything past the cap still renders on first view.

## [0.16.0] - 2026-08-12

### Changed

- **A shared folder's gallery served the original photos as its tiles.** The
  public browse page marks image tiles with `?thumb=1`, and that endpoint read
  the file and streamed it — so a folder of 5 MB photos shipped tens of
  megabytes to paint one screen, and the page crawled until it settled. It now
  serves the same cached thumbnail the app's own gallery uses, keyed by node id
  and rendered once. A file that has never had one rendered gets it rendered on
  the spot rather than dispatched to a background job, because the visitor is
  looking at that tile right now.

  Nothing is lost when there is no thumbnail to serve: an unindexed storage, a
  format the pipeline skips, or a source above 64 MiB falls through to the
  original exactly as before. Thumbnail fetches still do not count as downloads.

- **A folder share's ZIP is built when the link is created.** It used to be
  built when somebody clicked download — or by the background warmer, whichever
  came first, which meant a wait of up to five minutes landed on whoever opened
  the link. The person who just created it is usually that person. Creating a
  folder share now starts the build immediately, asynchronously (the response
  does not wait on a multi-gigabyte archive) and with a 30-minute ceiling so a
  build stuck on a sick storage backend releases its slot.

  This is the same build the warmer was going to do anyway, moved earlier — no
  new class of work, and file shares are untouched.

## [0.15.1] - 2026-08-12

### Fixed

- **The account rail put the two identities the wrong way round.** 0.15.0 drew
  the server's Branding logo as a fixed badge at the top of the rail and left
  the accounts underneath as e-mail initials. That is backwards: each row of
  that rail IS a server — a tenant — so the logo its admin set is the truest
  label the row can carry, while the application's own mark is the thing that
  must not move. The rows now carry their own server's logo (initials remain
  the fallback for servers with no branding), and the filex mark sits fixed
  above them.

  Branding is fetched for every signed-in account rather than only the active
  one, so a rail of three tenants paints three logos without being clicked
  through. Branded rows keep the rounded square at all times instead of the
  circle the initials use: logo artwork is authored square, and a 50% radius
  eats its corners — which row is selected is already said by the bar on the
  rail's edge.

## [0.15.0] - 2026-08-12

### Added

- **Day / Night / Automatic in the theme gallery.** The palette picker could
  change *which* theme painted but never whether it painted light or dark —
  that was the embedder's call, passed in `config.theme`, with no way for the
  person looking at the screen to override it. A three-way switch now sits at
  the top of the gallery, which is where people already go looking for "night
  mode". The preference is separate from the theme id, persists in
  `localStorage` (`filex.thememode`), syncs across tabs, and defaults to
  `'host'` — meaning existing embeds look exactly as they did until someone
  actually chooses. `'auto'` follows the operating system.

  ⚠ Every place that read `config.theme` reads the resolved mode instead. A
  choice that reached only the token resolution would leave the root class, the
  modals and the teleported context menus painting the old mode — a half-dark
  window.

- **`tabStrip: 'auto' | 'always'`.** The tab strip rendered only once a *second*
  tab existed, which also hid the `+` button — so in a fresh window tabs were a
  feature you could only reach by guessing the keyboard shortcut. `'always'`
  keeps the strip on screen with a single tab; `'auto'` (the default) preserves
  the old behaviour for small embeds. The desktop app opts in.

- **Themed scrollbars.** The platform scrollbar ignored the theme completely: a
  wide light-grey slab down the edge of a dark panel. Thin, rounded, transparent
  track, colours taken from the existing `--fe-*` tokens so every theme —
  including ones added later — gets a matching bar for free.

  ⚠ Scoped to `.fe` and the two teleported surfaces, never to `*`: this
  stylesheet is loaded by embedders, and a bare `*` rule would repaint the host
  page's scrollbars from inside an embedded file browser.

### Fixed

- **The desktop app's presence entry was the token label.** A user collaborating
  from the desktop app appeared in their own folder as `filex desktop — Win32`
  instead of as themselves. Presence deliberately shows the token *username* for
  API tokens, because every end user behind a shared proxy token maps to one
  filex account and the account name would be misleading — but a token with no
  username allow-list is not a shared proxy. It is one person's own client, and
  that person is the account owner. Such tokens now read
  `Ada (filex desktop)`: the person leads, the client qualifies. Shared proxy
  tokens are untouched.

- **The desktop app's "start when I sign in" registered a bare `electron.exe`.**
  `setLoginItemSettings({ openAtLogin })` with nothing else registers
  `process.execPath`, which in a development run is
  `node_modules/.../electron/dist/electron.exe` — no project path, so every
  sign-in afterwards opened Electron's own welcome window, and the entry
  outlived the checkout it pointed at. The login item is now packaged-only (a
  dev run says so instead of offering a dead switch), the command is written out
  explicitly, and it carries `--hidden`, which the app honours by staying in the
  tray — making true the promise the settings copy was already making. On Linux,
  where Electron has no login-item API at all, an XDG autostart entry is written
  instead of nothing.

- **The server's brand mark never reached the desktop app.** An admin who sets a
  logo under Branding has said what their install is called; the desktop client
  showed only the vendor's icon. The active account's logo now sits at the top
  of the rail and on the waiting screen. `GET /api/branding` is public and is
  called without the token — it is what the login page reads before there is a
  session.

## [0.14.0] - 2026-08-10

### Fixed

- **Office documents opened with "Config fetch 401" in the desktop app, and
  starred files and recently-opened were silently empty.** All three had one
  cause. The explorer accepts a bearer token as a string *or a function*, and
  the function form is the one a desktop app needs — the credential is fetched
  from the main process per call rather than sitting in the renderer between
  requests. Resolving it is therefore asynchronous, and the header builder the
  explorer handed to the preview modal and every viewer was the *synchronous*
  one, which drops a function token entirely. The request went out with no
  `Authorization` header at all; nothing threw, and the only symptom was a
  feature that quietly did not work.

  The builder is async now and every caller awaits it. `authHeadersSync` stays
  for the one caller that genuinely cannot await (XMLHttpRequest's header
  loop), but it remembers the last token it saw instead of emitting nothing —
  a stale credential is a far better failure than an anonymous request.
  `web/tests/api/authHeaders.test.ts` fails the build if a call loses its
  `await` again.

- **"Open in new tab" did nothing at all.** The standalone editor route is
  root-relative (`/files/edit`), which the browser resolves against the *page* —
  correct when the explorer is embedded in the app that serves that route, and
  wrong for every cross-origin embed. In the desktop app the page origin is
  `app://filex`, so the button asked the OS to open `app://filex/files/edit?…`;
  no handler for that scheme exists, so the call returned without error and
  without doing anything. The route is now resolved against `apiBase` when the
  API lives on another origin.

- **Markdown previewed as a blank pane.** `markdown-it` was an external in the
  web-component build, and every consumer of that bundle loads it as a plain
  `<script type="module">` where a bare specifier cannot resolve — so the
  dynamic import always failed, in the desktop app, work.example.com and fishapp
  alike. It is bundled now (~100 KB, lazily chunked, loaded only when a `.md`
  is opened), and if a renderer is ever missing again the pane says so instead
  of rendering nothing.

### Added

- **The desktop app follows the OS language.** Its own chrome — the connect
  screen, settings, the folder picker — was English while the file list beside
  it was Turkish. Both now resolve from one locale (Turkish or English).

- **A real connecting screen.** Between "the window opened" and "the files are
  listed" the app showed a dashed grey box in the top-left corner of an empty
  white window. It is a centred surface now, naming the server it is waiting
  for, with an honest error state and a retry. The window also opens on its own
  background colour and only once it has something to show, instead of flashing
  white first.

- **Images, video, audio and downloads work in the desktop app.** Those
  elements carry no headers by construction, so the app attaches the account's
  bearer to requests bound for its own server's origin — scoped to the
  signed-in origins, never a wildcard, and never overwriting a header the page
  set itself. Downloads of the app's own API URLs go through that session
  rather than being handed to a browser that may not be signed in.

- **`desktop/scripts/files-e2e.mjs`** — the file surface driven end to end
  against a real server: opening documents and media, downloading, renaming,
  searching, starring, deleting, and one blanket rule — nothing the window asks
  its server for may come back 401.

## [0.13.3] - 2026-08-07

### Added

- **The share button now works in the desktop app.** The explorer has always
  had one, gated on `typeof navigator.share === 'function'` — and Electron
  ships no Web Share API, so in the desktop app it simply never appeared.

  Rather than add a second share UI beside the product's own, the app
  polyfills the standard API onto a native handler, and the existing button
  lights up. On macOS that is the real system share sheet; on Windows and Linux
  it is a native menu (copy link, copy the message, email, open in browser),
  because the OS share sheet needs WinRT, which Electron does not expose. Said
  plainly rather than dressed up as something it is not.

### Fixed

- **The sync engine was reported missing when the app ran from source.** The
  bundled binary lives at `desktop/build/bin`, and the lookup only checked the
  packaged location and one level too high. ⚠ The suites passed throughout
  because they *told* the app where its engine was via `FILEX_CLI` — they no
  longer do, so the resolution itself is now under test, unpackaged and
  packaged.
- **The "choose a folder" dialog opened behind the settings panel** that
  launched it — present in the DOM, invisible on screen.
- **The rail icons were off-centre.** `⚙` and `+` were text glyphs, laid out
  against the font baseline, so centring the line box left the shape low and
  left. They are icons now, and the suite measures the offset (0.00 px) rather
  than trusting an eye.


## [0.13.2] - 2026-08-07

### Fixed

- ⭐ **Sixteen components rendered as raw, unstyled HTML in every embedded
  surface.** The share/permissions dialog was the visible one: no box, no
  backdrop, browser-default inputs flowing down the page. Also affected: the
  convert dialog, the presence bar, star/tag/recently-opened controls, and nine
  file viewers.

  Vue's `<style scoped>` compiles to `.cls[data-v-HASH]`. The web-component
  build compiles the components from source but imports CSS produced by a
  *different* build, so the two hashes are unrelated and every scoped rule was
  dead. Measured in the packaged desktop app:

  ```
  DOM element : data-v-b9443460
  CSS rule    : .fx-perm-modal[data-v-cc21190e]
  matches     : false      → position static, no background, no radius
  ```

  Nothing errored, which is why it survived: the components worked perfectly
  and simply had no styling. **This affected every embedder** — the desktop
  app, and any host using `<filex-explorer>` or `@brftech/filex-core` — not
  just one surface. All 16 now use ordinary styles; the class names were
  already prefixed (`fx-`/`fe-`/`filex-`), so `scoped` was buying nothing.

- **Adding a second account did not appear until restart.** The account was
  stored, but the sign-in happens in a different window and the main window was
  never told to repaint.

### Changed

- **The desktop account rail was rebuilt.** It was a pale strip with a lone
  square and a dashed `+` — a wireframe, not a product. It is now a dark rail
  with per-account colours, an edge marker on the active account, and a
  background-sync indicator per avatar.

  ⚠ The colour hash was wrong twice on the way: `h * 31` used only 5 of 10
  colours across 200 accounts, and the replacement returned a *negative* index
  for 44% of inputs — an invisible avatar. Both measured, both fixed.


## [0.13.1] - 2026-08-07

### Fixed

- **The explorer's onboarding tour sat on top of the desktop app's Settings
  panel.** The tour is appended to `<body>`, not to the explorer, so hiding the
  explorer left it exactly where it was — in the middle of Settings.

  ⚠ The v0.13.0 test for this passed while the bug was on screen: it asserted
  that the element *we* hide was hidden, which was true and beside the point.
  It now asks the browser what is actually topmost
  (`document.elementFromPoint`) — which fails on the old code and passes on the
  new, both measured.


## [0.13.0] - 2026-08-07

### Fixed

- **The desktop app opened the admin console.** Signing in landed you on the
  server's dashboard — users, storages, server settings — because the shell
  embedded the whole admin SPA. It now shows the **file explorer and nothing
  else**: the same `<filex-explorer>` component this project ships to
  embedders, pointed at your server with the token the browser sign-in handed
  back.

  Around it: an **account rail** down the left (click to switch, Slack-style),
  and a gear for **this app's** settings — accounts, synced folders, background
  running, start-at-login. Your server's admin panel is a link that opens in
  your browser, where a web console belongs.

- ⭐ **Starred files, recently-opened, starring and tags were broken in every
  cross-origin embed** — silently. Four calls in the core hardcoded
  `credentials: 'include'` while every other request used the configured mode.
  A credentialed cross-origin request may not be answered with
  `Access-Control-Allow-Origin: *`, which is what filex sends, so the browser
  rejected those four before the response was read: empty lists, no error. Two
  more defaulted to `'include'` when the host passed no prop. All six now take
  the mode from `useFileApi`.

  This affected **any** deployment serving the UI from a different origin to
  the API, not only the desktop app.

- **The explorer's multi-storage root mirrors a storage list the host provides**
  — it does not discover storages by itself. Undocumented, and easy to get
  wrong: the desktop window sat on an empty `/` and never issued a listing
  request at all. `docs/INTEGRATION.md` now says so.

### Changed

- **Desktop packages are no longer versioned in the filename**
  (`filex-desktop-x64.exe`, `filex-desktop-amd64.deb`,
  `filex-desktop-x86_64.AppImage`). `releases/latest/download/<name>` only
  resolves for a fixed filename, so the web app can now link straight at the
  right file instead of dropping people on a release page with ten assets and
  no indication of which is an installer and which is portable. The app reports
  its version in Settings.

- **The download offer says what each file does** — installer vs portable, and
  roughly how large. macOS says plainly that no build exists yet rather than
  offering a button that 404s.

### Added

- **[docs/DESKTOP.md](docs/DESKTOP.md)** — install per platform, how sign-in
  works and what to do when the browser cannot come back, where tokens and sync
  state live, and the unsigned-package warning stated rather than buried.
- `filex sync trash --json`.


## [0.12.0] - 2026-08-07

### Added

- **Selective folder sync.** A folder on your computer and a folder on a filex
  server are kept in step in both directions, in the background — the desktop
  app's "Sync folders" panel now actually transfers files rather than only
  recording pairings.

  The engine ships as `filex sync` in the CLI, and the desktop app runs that
  same binary. One implementation, two front ends: a terminal and the app read
  and write the same `~/.filex/sync/pairs.json`, so they can never disagree
  about what is paired.

  ```
  filex sync add ~/Documents/work docs://work
  filex sync run --watch 30s
  filex sync trash              # what sync removed from this machine
  ```

  How it decides, in short:

  - **The first sync of a pair deletes nothing.** With no record of a previous
    run there is no way to tell "you deleted this" from "you have not
    downloaded it yet", and guessing wrong empties a folder. Both sides are
    merged instead.
  - **A delete never beats an edit.** If a file was removed on one side but
    changed on the other since the last sync, the change wins and the file is
    restored.
  - **Changed in both places keeps both.** Your file keeps its name; the
    server's copy lands beside it as `report (server copy 2026-08-07 14-05).xlsx`.
  - **Anything sync removes from your machine is kept for 30 days**
    (`filex sync trash --restore <path>`). The engine never calls delete on
    local content directly.
  - Local and server timestamps are never compared to each other — only
    against what that side looked like at the end of the last run — so clock
    skew and the new mtime an upload gets cannot make files look permanently
    conflicted.
  - Paths in a server listing are validated before anything is written, so a
    listing containing `..` cannot write outside the sync folder.
  - Interrupted downloads are written to a temporary file and renamed, so a
    half-transferred file is never mistaken for a complete one.

- **`filex sync run --dry-run`** prints exactly what would happen before
  anything is touched, and `--account` limits a run to one signed-in server
  (one token cannot speak for two).

### Fixed

- **Pairing a folder to a server path that did not exist yet failed on every
  run.** The listing cannot walk a missing directory, so nothing ever synced
  and the folder had to be created by hand in the web UI first. Found against a
  live server, not in a unit test.


## [0.11.0] - 2026-08-07

### Added

- **Desktop app (Windows, Linux).** An Electron shell that runs the same web UI
  this repo already ships, so there is one file explorer, not two. Sign-in
  happens **in the user's browser**: the app opens the server's own login page
  and waits, so installs that authenticate through an identity provider (OIDC,
  SSO, passkeys, MFA) work — a native username/password form in the app would
  have locked all of those out. The browser hands back a **one-time code** over
  a `filex://` deep link and the app exchanges it for a token using a PKCE
  verifier that never leaves the process; only the code ever travels in a URL.

  When the deep link cannot work — no browser registered the scheme, a
  locked-down machine, or the user finished signing in on their phone — the
  waiting screen shows a copyable sign-in URL and accepts the code by hand.

  Multiple accounts on multiple servers, a background tray (closing the window
  keeps it running), optional start-at-login, and tokens held in the OS
  keychain (`safeStorage`). The app refuses to store a token in plaintext if
  the keychain is unavailable rather than silently downgrading.

- **`POST /api/auth/desktop/complete` and `/api/auth/desktop/exchange`.** The
  server half of that flow. `complete` requires an authenticated session and
  mints a scoped API token; `exchange` is public but one-time, expires in ten
  minutes, and constant-time compares both the code and the PKCE challenge.
  Nothing else about the auth surface changed.

- **Install prompts for both shapes of client.** On a phone or tablet the web
  app offers to install itself as a PWA; on a PC it offers the desktop
  download for the running platform (`.exe`, `.AppImage`, `.deb`) instead of a
  PWA it does not want.

### Fixed

- **The web app could not be embedded cross-origin.** It sent
  `X-Requested-With` on every request, which is not a CORS-safelisted header,
  so browsers preflighted — and go-chi/cors fails the *entire* preflight when
  one requested header is outside its allow-list. Any deployment serving the UI
  from a different origin to the API (the desktop app is one, but so is any
  reverse-proxy split) got a network error on every call. The header carried
  nothing the backend read.

- **Runtime API base URL, bearer token and credential mode are now
  configurable** at load time instead of being fixed at build time, which is
  what lets one bundle serve both the hosted site and the packaged app.

- **The service worker tried to precache ~19 MB**, including the Monaco editor
  chunks, and silently exceeded workbox's per-asset limit. Precache is now 224
  entries / 6.6 MB; the editor loads on demand as before.


## [0.10.2] - 2026-08-06

### Fixed

- **The v0.10.1 guard did not cover every write.** A sweep of the codebase —
  rather than the endpoints the bug report named — found four more places that
  could still write a file onto a folder: browser archive extraction and
  archive creation, the OnlyOffice save-back, and version restore. Replication
  is now guarded too, on both its write and copy paths: the replica is where
  the collision does its real damage, so a refusal there is recorded as a
  replication failure instead of quietly corrupting the mirror.

  v0.10.1 remains a genuine fix for the paths the report named; this closes the
  rest of the class.

## [0.10.1] - 2026-08-06

### Fixed

- **A file could be written onto a folder, corrupting the storage.** Passing a
  folder as the upload target wrote a single object at that exact key, leaving
  `X` (a file) and `X/…` (a folder) side by side. A real filesystem refuses
  this — the OS does it for us — so it never appeared locally; an object store
  has no such rule and accepted it silently.

  The damage showed up far from the cause. On a directory-backed mirror the
  colliding prefix can never settle, so `mc mirror` re-copied it on every run:
  2760 syncs in 24 hours, 1016 versions of a single PNG, a 43 MiB folder
  occupying 45 GB, and a disk at 96%. Quieter and worse, the colliding object
  made everything underneath it unlistable — 314 objects had no backup at all
  and nothing reported it.

  Every write surface now refuses the collision with `409`: the AI/MCP upload,
  the browser upload, the public file-drop link, text save, WebDAV `PUT`, and
  archive extraction. The reverse is refused too — a folder created on top of
  an existing file. Overwriting a file with a file is unchanged; it was never
  the problem. The check fails **open** if the backend cannot answer, because
  one flaky listing must not become an upload outage.

- **Uploads through the public file-drop link still failed on strict S3
  providers.** v0.9.0 fixed this for the browser upload but missed the shared
  ingest path behind the file-drop link, which kept wrapping the body and so
  kept sending it chunked with no `Content-Length` — the exact shape DT Cloud
  S3 answers `411` to.

### Added

- **`filex storage scan-collisions`** reports names that already exist as both
  a file and a folder, for damage that predates the guards above. It only
  reports: choosing which of the two to keep is a judgement about the data, not
  something a command should decide.

## [0.10.0] - 2026-08-06

Two interface fixes reported by olivov, whose tenants mount WebDAV from macOS.

Released as a minor rather than a patch on purpose: both change what the
explorer does on screen, and the update engine may apply a *patch* by itself
when the policy allows it. A release that changes the interface has to be one
the operator opts into.

### Added

- **Hidden (dot-prefixed) files can be shown or hidden; hidden by default.**
  They were listed like any other file, and most of them are not the user's
  files at all: a Mac mounting `/dav` leaves a `.DS_Store` in every folder it
  opens and an AppleDouble `._name` beside every file carrying extended
  attributes. Finder hides its own litter locally, so watching it reappear in
  the web UI reads as corruption — upload `4.jpeg`, find `4.jpeg` and
  `._4.jpeg` next to it.

  A toggle rather than a silent filter, because these *are* real files:
  hiding them outright would leave the ones already uploaded both invisible
  and undeletable, since the UI is the only way most people reach them.
  Right-click empty space, or `Ctrl+Shift+.`; the choice is remembered. It
  stays available to read-only viewers — what you can see is a view
  preference, not a change to anything.

### Changed

- **With exactly one storage visible, that storage now opens directly.** The
  storage list was a one-row page that carried no information and had to be
  clicked through on every visit; "up" is no longer offered where it would
  only lead back to that single row. Installs with more than one visible
  storage — and single-storage mode — are unaffected.

## [0.9.0] - 2026-08-06

Closes the ten items the olivov deployment (multi-tenant, 10 providers, DT
Cloud S3) filed against v0.8.0, plus one follow-up found while reviewing them.

### Security

- **A tenant admin could reach every other tenant over WebDAV.** `/dav`
  performs its own Basic authentication, so it is mounted outside the chain
  that runs the tenant resolver — no scope ever reached the scoped store, and
  the WebDAV root listed every tenant's storages. A second hole ran in
  parallel: storage lookup by name goes through `GetStorageByName`, which the
  scoped store does not wrap. The result was cross-tenant **read, write and
  delete** — and `DELETE` over WebDAV is permanent, it does not go to trash.
  Foreign storages now answer `404`, which keeps a storage that exists
  indistinguishable from one that does not. Supertenant operators stay
  confine-exempt. **Only multi-tenant installs (`FILEX_MULTI_TENANT`) were
  affected**; a single-tenant install has no second tenant to reach.

- **A tenant admin could read, modify and delete another tenant's users.**
  Only the user *list* was tenant-confined; `GET`, `PATCH` and `DELETE` on
  `/api/admin/users/{id}` acted on any id in the install — so a foreign
  account could be read, re-passworded (account takeover), disabled or
  deleted. All three now refuse out-of-tenant ids with `404`.

- **New users silently became platform operators.** `POST /api/admin/users`
  ignored `provider_id`, so every account fell to the store default of
  provider 1 — and provider 1 (`default`) is the *supertenant*. Accounts meant
  for one tenant came out confine-exempt and saw every tenant's storages.
  Absent, a new user now lands in the caller's own tenant; a tenant admin
  naming another gets `403`; an unknown id gets `400`. `PATCH` can re-home a
  user — supertenant only — which is the repair path for accounts already
  stranded in provider 1.

- **An unqualified grant path wrote the grant to an arbitrary storage.** It
  fell back to `storages[0]`. Elsewhere that fallback is harmless because a
  wrong guess shows up in the next listing; a grant is durable authorization
  state written where nobody looks again. It is now `400`, and the same
  tenant gate applies to the lookup.

### Fixed

- **Uploads failed on strict S3 providers (DT Cloud, and any provider that
  requires a length).** The S3 driver accepted a `size` argument and dropped
  it; with a non-seekable body the SDK framed the request chunked with no
  `Content-Length`. AWS and MinIO accept that — the S3 specification leaves it
  to the provider — and DT Cloud S3 answers `411 MissingContentLength`. WebDAV,
  MCP and the empty-folder marker kept working throughout, because all three
  hand the driver a seekable body. The upload handler now rewinds the
  multipart part after mime-sniffing rather than wrapping it, which is what
  destroyed seekability in the first place.

- **Upload failures were invisible.** The browser reported a failed upload
  only through an `error` event, which a standalone deployment has nothing
  listening for; the progress bar still ran to 100%, because the bytes really
  are sent and the server rejects them afterwards. Failures now raise a toast
  and carry the message into the upload row.

- **WebDAV could not create or browse subfolders on S3.** `Stat` only issued
  `HeadObject`, but on an object store a folder is a prefix, so every folder
  looked missing and the pre-`PUT` parent check failed with `409`. `Stat` now
  resolves a prefix as a directory, the way listing already did.

- **`GET /api/admin/quota/{id}` answered `500` for an unknown user**, and set
  and recompute were worse — both ran an `UPDATE` matching nothing and
  answered `200`. Unknown users are now `404`.

- **Creating a user could leave a supertenant account behind.** If homing the
  new user in its tenant failed, the handler returned `500` and left the row
  in provider 1. The half-created account is now removed; if that cleanup also
  fails, the error names the id that needs fixing by hand.

### Added

- **Per-user enable/disable switch** (`users.enabled`, migration 00022,
  defaults to enabled so every existing account keeps working). Refusing the
  next login is not enough — a session minted before the switch and every API
  token the user ever created both outlive it, so all three paths refuse a
  disabled account. Files, quota and grants are untouched: this is an access
  switch, not a soft delete. Disabling the last admin is refused.

- **Per-user quota is served where it was already documented**
  (`/api/admin/users/{id}/quota`); the original flat path keeps working. Usage
  and quota now ride on the user object, so an admin table costs one call
  instead of one request per row.

## [0.8.0] - 2026-07-29

### Added

- **Updates: filex now knows which releases exist, and can install them.**
  Behaviour is decided by which part of `x.y.z` moved — `z` applies itself
  (when the policy allows), `y` is announced and applied with one click, `x` is
  announced with upgrade instructions. Full documentation:
  [docs/UPDATES.md](docs/UPDATES.md).

  - **Nothing moves until you opt in.** The default is `policy: manual` —
    check and announce only. `AUTO_UPGRADE=true` selects the patch policy;
    `FILEX_UPDATE_CHECK=0` stops every outbound request.
  - **Admin → Updates page**: running version, what is available, why filex is
    or is not taking it, the releases being skipped over, and — when it cannot
    act itself — the exact commands for this install shape.
  - **`filex self-update`** (`--check`, `--to <version>`) for binary/systemd
    installs.
  - **Container installs never self-apply, by design.** An image layer is
    immutable: a binary replaced inside a running container disappears at the
    next `docker compose up` and the version silently reverts. filex refuses
    and prints the image-upgrade steps instead. It does not ask for
    `/var/run/docker.sock`.
  - **Release manifest** (`FILEX_UPDATE_MANIFEST_URL`, default
    `https://filex.sh/updates/stable.json`) carries two things a git tag
    cannot: `auto_ok` — a kill switch that pulls a bad release out of automatic
    distribution without deleting it — and `migrations`, which makes "patches
    carry no schema changes" checkable rather than a promise. A patch that
    declares a migration is never applied automatically.
  - **Safety sequence on apply**: SHA-256 verification against the manifest →
    unpack beside the current binary (atomic rename, same filesystem) →
    **smoke-test the new binary** (`filex --version`) → **database snapshot**
    (`VACUUM INTO` for sqlite, `FILEX_UPDATE_PRE_COMMAND` for external
    engines; a failure aborts the upgrade) → keep the old binary as
    `filex.bak-<version>` → swap → restart via systemd if present, otherwise
    report "restart required". Everything before the swap is undone by doing
    nothing.
  - `0.x` guard: while filex is pre-1.0, minor releases are never automatic
    even under `policy: minor` — semver gives them no compatibility promise.
  - New notification events `update_available` (once per version) and
    `update_applied`.
  - `scripts/gen-update-manifest.py` builds the manifest from the published
    GitHub releases, taking digests from goreleaser's `checksums.txt`.

### Changed

- The update check sends exactly one identifying header,
  `User-Agent: filex-updater/<version>` — no hostname, license, instance id or
  usage data. It is an update check, not telemetry.

## [0.7.6] - 2026-07-29

### Fixed

- **AI surface: denials now answer `403`, not `500`.** A confined token
  (`root:<adapter>://<path>` scope) writing or listing outside its root, and a
  bound user without the required grant level, both returned **500**:
  `resolveStorage` surfaced them as plain `fmt.Errorf` values, so `aiStatus`
  fell through to `mapDriverErr`'s server-error default. `errAIForbidden` was
  unmapped as well, so mutating permission denials answered 500 too. Both
  refusals are now wrapped so `errors.Is` matches `confine.ErrOutOfRoot` /
  `errAIForbidden`, and `aiStatus` maps them to `403`. The caller-facing hint
  ("… is outside your confined root … call file_root to see your root") is
  unchanged.

  Why it matters: a `5xx` reads as "server glitch, retry" — scripts, agents and
  HTTP clients retry a request that can never succeed, while the real cause
  (wrong path, missing grant) hides behind a generic server error.

## [0.7.5] - 2026-07-19

### Changed

- **Internal refactor (no behavior change): the storage-scoped path hash is now
  a single `internal/pathkey.Hash()`**. The `md5(cleaned path) + NUL +
  little-endian storage id` body had been copy-pasted across nine call sites
  (handlers, e2e, sync, DAV, and both DB drivers) — the same file could map to
  different rows if any copy drifted. The body is moved verbatim (byte-identical
  output, existing `path_hash` values unaffected), and a hash-equivalence test
  pins it against the previous implementation.

## [0.7.4] - 2026-07-18

### Fixed

- **Split view: the trash bin now renders in the secondary pane too**. The
  virtual "Trash" row was injected only by the main panel, so in split view
  the right pane started one row higher and the two panes' rows were
  visually offset. The trash-row synthesis and the internal-entry filter
  are now a single shared source (`lib/listing.ts`) used by both panes, so
  they always list identical rows. Opening the trash row in the secondary
  pane opens the trash view (with restore actions) in the main panel.
- **Standalone file manager: tall listings scroll inside the pane, not the
  whole page**. The standalone Explore page wrapper was `min-h-screen`
  (min-height), which lets the page grow past the viewport when the listing
  is taller than the screen (e.g. grid view / split view) — so the root
  `.fe` grew with it and its internal `overflow: auto` never engaged,
  scrolling the entire page. It is now `h-screen` (height), which caps the
  shell to the viewport so each pane's listing scrolls internally.

## [0.7.3] - 2026-07-18

### Fixed

- **Split view context menu now matches the main panel exactly**: the
  secondary pane's right-click menu was a separate, shorter list (missing
  rename, delete, share, convert, tags…). Both panels now render the single
  `selectionActionList` source, and pane actions (including rename / delete /
  new folder) are routed to the pane. Right-click and the keyboard shortcuts
  for delete/rename follow the focused pane.
- **Dropping a file onto its own folder no longer errors**: dragging an item
  onto the folder it already lives in (its own breadcrumb or the same
  listing) used to fire a move that the backend rejected with an S3
  "copy object to itself" 400. It is now a silent no-op.
- **Split view breadcrumb alignment**: the secondary pane's breadcrumb used a
  slightly different padding, font size and height than the main one, so the
  two panels were subtly misaligned. They now share identical metrics.

## [0.7.2] - 2026-07-18

### Fixed

- **Split view breadcrumb**: in split view the main (left) panel's
  breadcrumb spanned the whole width instead of just its own half — it now
  sits in the left half, mirroring the secondary pane's breadcrumb. The
  breadcrumb, presence and lock strips moved into a `.fe__primary` wrapper
  that occupies the left half when split (and the active-panel accent moved
  with them).
- **Split view context menu**: right-clicking a row in the secondary pane
  now opens a menu (Open / Open in new tab / Download / Copy / Cut / Paste)
  — previously it only selected the row. All actions target the secondary
  pane. Right-clicking empty space in the pane shows a Paste-only menu, so
  you can paste into an empty folder there.

## [0.7.1] - 2026-07-18

### Fixed

- **The explorer no longer overflows its host by 2px** (`.fe` was
  content-box, so its borders pushed past `height:100%`) — the outer page
  scrollbar that this produced in embeds is gone, and toasts stay visible
  at the bottom of the explorer instead of drifting out of view.
- The tab strip no longer shows a scrollbar under the tabs when many tabs
  are open — overflow still scrolls (wheel/drag), just without the strip.
- **Split view**: the secondary pane now renders with the same view
  components as the main panel (list / grid / gallery) instead of its own
  flat list; the pane keeps its own view mode (inherited from the main
  panel when you split) and the toolbar's view switcher applies to
  whichever pane is focused.
- **Split view**: pasting from the context menu now targets the focused
  pane — copying in the left pane and right-click → Paste in the right
  pane pastes into the right pane's folder (keyboard paste already did).
- Long action rows in the toolbar no longer wrap the whole toolbar onto a
  second line: the row is single-line and actions that do not fit fold
  into a "⋯" menu.
- Global shortcuts stay quiet while a context menu is open — pressing
  Delete with a menu open used to open the confirm dialog underneath the
  menu backdrop, wedging the UI.
- Default accent color darkened (`#3b82f6` → `#2f6fe0`) so white text on
  primary buttons meets WCAG AA (4.7:1); the unknown-file icon color in
  the light theme now meets the 3:1 UI-contrast bar too.

## [0.7.0] - 2026-07-18

### Added

- **Branding**: settings-driven identity for the public share/PIN/drop and
  folder-browse pages plus the admin login — display name, logo, accent
  color and footer text, with per-tenant overrides in multi-tenant mode
  (a new admin "Branding" page with live preview, and a public
  `GET /api/branding`). The "shared with filex" footer stays by default
  and can be turned off.
- **End-to-end encrypted folders (MVP)**: create a password-protected
  folder whose contents are encrypted in the browser (WebCrypto,
  PBKDF2 + AES-256-GCM) — the server never sees the password, a key or
  plaintext. Transparent upload/download/preview once unlocked, lock
  badge + unlock screen, and server-side blind spots closed (no
  thumbnails, no content indexing, no convert/OnlyOffice on encrypted
  blobs). There is NO recovery — a lost password means lost data; see
  docs/E2E-ENCRYPTION.md for the threat model and trade-offs.
- **Cloud readiness (preparation only)**: a `FILEX_CLOUD` master flag
  (default OFF — zero behavior change while off) gating a self-signup
  skeleton wired to multi-tenant provisioning, config-driven plans,
  Stripe stubs that answer 503 until configured, and provider
  plan/limits columns (migration 00021). See docs/CLOUD.md.

## [0.6.0] - 2026-07-17

### Added

- **Tabs**: open multiple locations as tabs (Ctrl+T / middle-click a folder /
  right-click → "Open in new tab"), switch with Ctrl(+Shift)+Tab, reorder by
  dragging, close with Ctrl+W or middle-click. The tab strip only appears
  with two or more tabs, and tabs (including the active one and per-tab
  split state) persist per browser. All tab shortcuts are remappable.
- **Split view**: split the active tab into two panes — the secondary pane
  navigates independently, and dragging files between panes moves them
  (same storage) or copies them (across storages). Keyboard actions follow
  the focused pane; shared clipboard works across panes.
- **Gallery view**: a third view mode with large media thumbnails alongside
  list and grid.
- **Browsable folder shares**: public folder share links now open a
  navigable page (subfolders, per-file open/download) instead of jumping
  straight to the ZIP flow — "Download all" keeps the ZIP path; folders
  that are mostly images/videos render as a gallery.
- **File comments**: per-file comment threads (visible to anyone who can
  see the file; authors and admins can delete) shown in the details panel
  with a count badge, plus a `comment.added` webhook event.

## [0.5.0] - 2026-07-17

### Added

- **Theme gallery**: 8 built-in themes (default, night blue, forest, amber,
  lilac, high contrast, soft gray, terminal green), each with its own light
  and dark variant — theme choice is independent from light/dark mode.
  Applied as `--fe-*` token overrides on the explorer root (embeds keep the
  host page untouched), persisted per browser, synced across tabs.
- **Customizable keyboard shortcuts**: a settings modal (toolbar menu, the
  "?" card or the command palette) lists every action grouped by category;
  press-to-capture rebinding with conflict detection ("unbind the old one"
  flow), per-row and global reset. Only deviations from the defaults are
  stored.
- **Quick look**: Space peeks the selected file in a lightweight overlay —
  arrow keys move the selection with the preview following, Enter promotes
  to the full open flow.
- **Operations center**: uploads and background file operations now live in
  one corner badge with an expandable panel — overall progress ring, per-item
  progress, session history, sticky error rows with retry (failed uploads
  retry into their original target folder).
- **Onboarding tour**: a first-run coach-mark tour (6 steps) highlights the
  core surfaces; steps whose targets are absent are skipped, and the tour
  can be restarted from the toolbar menu or the command palette.
- Command palette: new "Theme", "Shortcuts" and "Restart tour" commands.

### Improved

- Accessibility: grid/list views expose proper `grid`/`listbox` roles with
  keyboard focus rings, the context menu is fully keyboard-navigable
  (arrows/Home/End/Esc with focus restore) and flips at screen edges,
  modals trap focus and restore it on close, icon-only buttons carry
  localized labels, and animations respect `prefers-reduced-motion`.
- Drag & drop: valid drop targets highlight while dragging and the drag
  ghost shows the item name with a count badge for multi-selections.
- Error states: friendlier error cards with a collapsible technical-details
  section alongside retry.

## [0.4.2] - 2026-07-17

### Fixed

- **Trash no longer wedges storage sync** (#5): moving a folder to trash now
  rewrites every descendant's path along with it (restore stays symmetric),
  and the `(storage, parent, name)` unique index ignores soft-deleted rows
  (partial index on SQLite/Postgres, generated `is_live` column on MySQL) —
  a trashed folder's leftovers can no longer block sync from re-creating
  the same names forever.
- **`versions.keep_n` above 20 now works**: the snapshot path trimmed
  version history to a hardcoded 20 on every write, silently overriding
  larger configured limits.
- WebDAV `DELETE` now moves files and folders to the filex trash (matching
  the web UI) instead of deleting permanently; drivers without rename
  support keep the old behavior.

### Added

- **One post-write gate for antivirus + webhooks**: uploads, moves and
  deletes through the AI/token surface, ShareX, WebDAV and the ops worker
  now enqueue antivirus scans and emit `file.*` webhook events just like
  manager uploads (payloads carry an `origin` field: `manager`, `ai`,
  `sharex`, `dav`, `ops`).
- **Webhook delivery status**: webhook targets persist their last delivery
  result (`last_http_status`, `last_error`, `last_delivery_at`) and the
  admin Webhooks page shows a green/red "last delivery" badge.
- **Recursive CLI upload**: `filex client upload -r <dir> <storage://path>`
  mirrors a whole folder tree (empty folders included, symlinks skipped,
  non-zero exit on partial failure).
- Search results in grid view now show the same highlighted content
  snippet as list view.

## [0.4.1] - 2026-07-17

### Added

- **App-store packaging**: ready-to-submit manifests for Umbrel, CasaOS,
  Runtipi, Unraid (Community Applications) and Portainer templates under
  `deploy/`, plus a refreshed Helm chart (appVersion now tracks a real
  image tag).
- **Documentation site**: a VitePress site over `docs/` (see `docs-site/`)
  with local search, feature landing page and dark mode — published at
  https://docs.filex.sh.

### Fixed

- External-service capability probes now hit real health endpoints
  (OnlyOffice `/healthcheck`, converter `/healthz`) and failures are only
  cached for 2 minutes instead of an hour — a transient outage no longer
  pins a "configured but unreachable" banner for the rest of the hour.
- Returning to the root from a dead deep link now clears the stale hash,
  so a reload lands on the root instead of the 404 screen again.

## [0.4.0] - 2026-07-17

### Added

- **Inspector panel**: press `i` (or the toolbar toggle) for a details
  sidebar on the selected item — metadata, copyable path/etag, **version
  history** (list, restore with optional pre-restore snapshot, take a
  version on demand), effective permission with a jump to the permissions
  dialog, and the item's share links. Full-screen overlay in narrow mode.
- **Optional antivirus scanning**: with `clamscan`/`clamdscan` on the host
  (`FILEX_CLAMAV_BIN` or PATH), uploads are scanned asynchronously;
  infected files are quarantined to the trash and a new **`file.infected`**
  event fires through webhooks and in-app notifications. Capability
  endpoint reports `antivirus`. See `docs/PROTECTION.md`.
- **Protection settings**: `GET/PATCH /api/admin/protection` + a new admin
  "Protection" page — trash retention days, version retention (`keep N`),
  antivirus status at a glance.
- **Version retention cron**: with `versions.keep_n` set, a daily sweep
  trims old versions per node.
- **Admin quota UI**: user edit screen gains a storage-quota card with a
  usage bar, limit editor and recompute action; new
  `GET /api/admin/quota/{user_id}` endpoint backs it.
- Shares admin view: sortable download counts and a copy-link row action.
- `POST /api/files/versions/snapshot` records an on-demand version (the
  service existed; the endpoint was never wired).
- File listings now include `etag` when the backend knows it.

### Fixed

- **Admin quota endpoints always returned 400** since introduction — the
  routes bound `{user_id}` while the handlers read `{id}`.
- **Daily trash purge never actually ran** — the retention loop existed
  but was never started; it now runs daily. NOTE: after upgrading,
  soft-deleted files older than the retention window (default 30 days)
  will be permanently purged on the first sweep.

## [0.3.0] - 2026-07-17

### Added

- **WebDAV server**: mount your filex as a network drive. `/dav/<storage>/...`
  speaks class-2 WebDAV (Windows map-drive, macOS Finder, rclone, davfs2) with
  HTTP Basic auth (account password or API token), full RBAC enforcement,
  read-only storage protection and best-effort DB/search-index sync — files
  written over WebDAV show up in the UI and in content search. Kill-switch:
  `FILEX_DAV=0`. See `docs/WEBDAV.md`.
- **`filex client` CLI**: `login`, `ls`, `upload`, `download`, `mkdir`, `rm`,
  `mv`, `search` (content-aware) and `share` subcommands against any remote
  filex — flags/env/`~/.filex/cli.yaml` (0600) config, streaming uploads,
  `--json` output. See `docs/CLI.md`.
- **Webhook targets**: multiple webhook endpoints with per-target event
  filters and HMAC signing (`X-Filex-Signature: sha256=…`, plus
  `X-Filex-Event` / `X-Filex-Delivery` headers). New file events fire on
  uploads, moves, deletes, trash, share creation and file-drop receipts.
  Admin UI for target CRUD + test deliveries; the legacy global webhook keeps
  working unchanged.
- **Narrow / embed mini mode**: below 560px the explorer collapses its
  toolbar behind a "⋯" menu, search expands from an icon, touch devices get a
  bottom-sheet context menu and a floating upload button — wide layouts are
  pixel-for-pixel unchanged.

### Fixed

- WebDAV extension verbs (PROPFIND & friends) are registered with the router
  so they survive `chi` method filtering.

## [0.2.0] - 2026-07-17

### Added

- **Content search**: filex now indexes what's *inside* your files, not just
  their names. Plain text, Markdown, source code, CSV/JSON/YAML, PDF text
  layers and Office documents (docx/xlsx/pptx) are extracted asynchronously
  (never blocking writes) into the embedded Bleve index. Search hits carry a
  highlighted `snippet` and a `matched` field (`name`/`content`/`both`), and
  the search endpoints accept `scope=name|content|all`. Rebuild with content
  via `POST /api/admin/search/rebuild?content=1`. Tunables:
  `FILEX_SEARCH_CONTENT` (kill-switch) and `FILEX_SEARCH_CONTENT_MAX`.
- **Optional OCR**: when a `tesseract` binary is available
  (`FILEX_TESSERACT_BIN` or PATH), image files (png/jpg/webp/tiff) are OCR'd
  into the content index; without the binary the extractor stays silent.
  Capability endpoint now reports `ocr`.
- **Duplicate report**: `GET /api/admin/duplicates` groups files by
  (size, etag) and reports wasted bytes; new read-only admin view lists the
  groups with per-group totals.
- **Search UX**: the command palette gains an "Everywhere" section (global
  search with content-match badges and safe highlighted snippets), list view
  shows snippets under content matches, searches can be saved and re-run from
  the palette, and the admin SearchTest view grows a scope selector.
- **MCP**: `file_search` accepts a `content` flag (default on) and returns
  snippets, so agents can find files by what they contain.

## [0.1.84] - 2026-07-17

### Added

- **Command palette** (`Ctrl/Cmd+K`): fuzzy-jump to files and folders in the
  current listing, run common actions (new folder, upload, toggle view, trash,
  refresh, go up) and jump to a typed path — all from the keyboard.
- **Keyboard shortcuts help**: press `?` to see every shortcut, grouped and
  sourced from a single registry so the sheet never drifts from reality.
- **Date grouping & sorting**: list columns (name / size / date) are now
  sortable; sorting by date segments rows under Today / Yesterday / This week /
  This month / month-year headers.
- **Density toggle**: compact ⇄ comfortable list & grid density, persisted per
  browser.
- **Undo snackbar**: rename, move and trash operations offer a one-click
  "Undo" for 8 seconds.
- **Connection badge**: when the live (WebSocket) channel is unavailable the
  explorer quietly falls back to polling and now says so with a small amber
  pill instead of staying silent.

### Changed

- **File-type icons**: hand-drawn SVG icon set (12 families with per-family
  accent colors, light + dark) replaces the emoji icons in grid and list views;
  thumbnails keep priority.
- **Empty / not-found / error states**: illustrated, actionable screens (drag &
  drop hint + upload button, retry on load failure, distinct empty-trash and
  empty-search states) replace bare text.
- **Skeleton loading**: initial listing shows ghost rows/cards instead of a
  spinner (motion-reduced friendly).
- **Public share pages** (PIN, download, ZIP-preparing, file-drop) redesigned
  with a shared card language, dark-mode support, accessible focus states and
  a subtle "Shared with filex" footer; login page got the same visual pass with
  SSO-first hierarchy.

## [0.1.83] - 2026-07-16

### Fixed

- **Embedded explorers could not scroll in height-constrained hosts** (small
  screens, mobile touch): three compounding layout issues fixed. The
  web-component wrapper no longer forwards the host element's `style`/`class`
  onto the inner `.fe` root (`inheritAttrs: false`) — an embedder's inline
  `display:block` used to override `.fe{display:flex}` and collapse the
  column layout. `.fe__body` gains `min-height: 0` so the flex body shrinks
  to the remaining space and its `overflow:auto` actually engages. The
  stylesheet now ships a `filex-explorer{display:block;height:100%}` default,
  so embedders no longer need inline styles on the host element.

## [0.1.82] - 2026-07-10

### Fixed

- **i18n:** viewer strings (save chip, Edit / Close / Download buttons,
  read-only and no-preview labels) and the presence-bar toggle tooltip now
  follow the UI locale instead of leaking Turkish into English sessions (#2).
- **Thumbnails for AI-surface writes:** files written through `/api/ai/upload`,
  the `file_write` MCP tool, unzip and ShareX captures now dispatch thumbnail
  generation exactly like manager uploads — agent-uploaded images no longer
  show the broken-image placeholder in grid view (#3).
- **Demo landing:** the stale "Open source (soon)" card now reads
  "Open source · MIT" and links to the public GitHub repository (#4).

## [0.1.81] - 2026-07-09

### Added

- **Per-token identities** (`X-Filex-Token-User`): an API token can define a
  list of usernames (first = default); the audit log, shares (`created_via`)
  and presence are attributed per integration. Unknown username → 403.
- `PATCH /api/admin/ai-tokens/{id}` and `PATCH /api/tokens/{id}` — token
  editing (label / usernames) which previously did not exist.
- `/api/ai/*` and `/api/sharex` writes are now audit-logged.

## [0.1.80] - 2026-07-09

### Fixed

- **Embedded grid thumbnails**: thumbs are now fetched with the same auth chain
  as API calls (bearer/proxy) and rendered from blob object-URLs, so embedded
  web-component and PWA contexts show real previews instead of broken images.

### Added

- Presence bar expand/collapse toggle — full-name chips with horizontal scroll,
  preference persisted per browser.

## [0.1.79] - 2026-07-09

### Fixed

- **Presence shows real user identities** in embedded contexts: host proxies
  stamp `X-Filex-Presence-Name` (RFC 2047) + `X-Filex-Presence-Key`, spoofing
  headers are stripped, rosters exclude self, and renames follow focus.
- `realtimeRoom` no longer subscribes to a mis-qualified room in single-storage
  embeds (the root cause of live updates never arriving there).

## [0.1.74] – [0.1.78] - 2026-07-08/09

### Added

- **Real-time collaboration**: `/api/ws` WebSocket presence + live folder
  updates in the core component (native UI *and* embedded contexts via
  short-lived tickets from `POST /api/files/ws-ticket`), API-polling fallback.
- **Folder-share ZIP cache** keyed by content signature, a 5-minute warmer and
  a "preparing %" page for cold hits.
- **ShareX endpoint** (`POST /api/sharex/upload`) returning a ready public link.

### Fixed

- Confined (embedded) WebSocket contexts: optional-auth route, ticket-bound
  RBAC user, relative→absolute room mapping, per-client frame paths.

## [0.1.69] – [0.1.73] - 2026-07-07

### Added

- **Deep links**: the address bar tracks the open folder (`#storage/dir`);
  pasting a link opens that folder, login preserves the hash.
- Web Share (`navigator.share`) button in the share modal.

### Fixed

- Ghost folders now 404 (S3 empty-prefix verification); unauthorized folders
  render the same "not found" screen (no RBAC information leak).
- `GET /api/files/share` list route existed in the UI but not the backend —
  "existing links" no longer always empty.

## [0.1.68] - 2026-07-06

### Fixed

- **OIDC login no longer loops behind a CDN that strips Set-Cookie from
  redirects.** Measured live (nginx `$upstream_http_set_cookie` vs the
  browser through Cloudflare): the origin emits the session `Set-Cookie` on
  the callback's 302, but the CDN strips a **Domain-scoped** Set-Cookie from a
  **3xx** response while passing it on a 200 (host-only cookies survive either
  way) — so the just-minted session cookie vanished and the SPA looped on
  `/api/auth/me` 401. The successful OIDC callback now writes the session
  cookie (unchanged Domain/Secure/SameSite logic) and forwards the browser
  with a minimal **200 `text/html` bounce** (`<meta refresh>` +
  `location.replace` + a `<noscript>` link) to a fixed relative `/admin/`, so
  the cookie rides a 200 the CDN passes through. The bounce target is a
  constant relative path (stays on the tenant host from v0.1.66, zero
  open-redirect surface; html/template-escaped). Error/maintenance branches
  stay 302 (they set no cookie). No config or DB change; works with or without
  a CDN, single- and multi-tenant. The OIDC *state* cookie is host-only and
  already survives the start redirect. (`handlers.OIDCCallback`,
  `writeOIDCBounce`.)

## [0.1.67] - 2026-07-06

### Fixed

- **Session (and OIDC state) cookies are now marked `Secure` on HTTPS.**
  Previously the session cookie never set `Secure`, and the OIDC state cookie
  only did so when `r.TLS != nil` — which is never true behind a
  TLS-terminating reverse proxy (nginx/Caddy), where filex is reached over
  plain HTTP with `X-Forwarded-Proto: https`. Both cookies now derive `Secure`
  from `r.TLS` **or** `X-Forwarded-Proto=https`. On a **`Domain`-scoped**
  cookie this is what Chrome's schemeful-same-site rules require to keep the
  cookie through the OIDC redirect chain — the observed difference from a
  working Roundcube cookie behind the very same proxy. Plain-HTTP installs
  (no `X-Forwarded-Proto`) still get a non-Secure cookie, so TLS-less setups
  keep working. This is both correct hardening and the most likely fix for
  cookie-domain SSO login loops behind a proxy. (`handlers.requestIsHTTPS`,
  `oidc.StartFlow`.)

## [0.1.66] - 2026-07-06

### Fixed

- **Multi-tenant: OIDC callback now redirects to the TENANT's host.** After a
  successful (or failed) IdP round-trip the callback bounced the user to
  `FILEX_PUBLIC_URL` — the operator/supertenant host — instead of the tenant
  host the login started on, stranding them without a session. All three
  callback redirects (success `/admin/`, error `?error=oidc`, maintenance
  `?maintenance=1`) now derive their base from the request host, but only
  when it resolves to an enabled provider row (the same trusted-host model
  as tenant resolution); unknown hosts fall back to `PublicURL`, and
  single-tenant installs are untouched. Scheme honors
  `X-Forwarded-Proto: http` for TLS-less setups, defaulting to https.

### Added

- **Multi-tenant: per-tenant session-cookie `Domain`.** The global
  `FILEX_COOKIE_DOMAIN` (0.1.63) cannot serve tenants on different apex
  domains. In multi-tenant mode the `filex_session` Domain now resolves per
  request: the provider's new optional **`cookie_domain`** column (settable
  via `/api/admin/providers`, migration `00015`) wins; else it is derived
  from the provider host by dropping the first label (`files.example.com` →
  `.example.com`); else the global value. Set and clear stay symmetric.
  Single-tenant behaviour is unchanged. ⚠ Tenants served on a bare apex or
  whose derived value would be a public suffix (`.com.tr`) must set
  `cookie_domain` explicitly — see docs/MULTI-TENANCY.md.

## [0.1.65] - 2026-07-06

### Added

- **Multi-arch Docker images: linux/amd64 + linux/arm64.** Release binaries
  were already cross-built for arm64 (goreleaser), but the container images
  only shipped amd64. The Dockerfiles now pin the Node/Go build stages to
  `$BUILDPLATFORM` and cross-compile the Go binary via `TARGETOS`/`TARGETARCH`
  (CGO=0 makes it free), so only the runtime stage's package installs run
  per-arch; the release workflow builds and pushes both platforms as a
  single manifest (QEMU + buildx). All runtime packages verified present in
  Alpine 3.20 aarch64. A plain single-arch `docker build` keeps working
  unchanged.

### Fixed

- **Full image version stamp.** `Dockerfile.full` still used the `-X
  main.version` ldflags form — a silent no-op — so `:full` images always
  reported "0.1.0-dev (unknown, unknown)". Now uses the fully-qualified
  version package path like the default image.
- **CI: 30-minute hard timeout on the e2e jobs** (source-repo CI) — a hung
  browser e2e run sat 14 hours on a single-slot runner and starved every
  queued pipeline.

## [0.1.64] - 2026-07-06

### Fixed

- **CI lint pass restored.** staticcheck flagged an unused
  `adminIDFiltersIn` type and two files had drifted from gofmt, which kept
  the source repository's tag-triggered release automation from running.
  Dead type removed, files formatted. (No runtime changes — see 0.1.63
  for the feature content.)

## [0.1.63] - 2026-07-05

### Added

- **SSO-first login** (`FILEX_OIDC_AUTO_REDIRECT`, default `false`). With the
  flag on and `oidc` among the auth drivers, the login page starts the OIDC
  flow immediately (redirect to the IdP) instead of rendering the password
  form. Local login stays available for break-glass/`admin@local` behind a
  "Sign in with password" link (`/admin/login?local=1`). Loop guards: the
  auto-redirect is suppressed on `?local=1`, after a failed IdP round-trip
  (`?error=oidc`) and on `?maintenance=1`; demo mode is unaffected. A failed
  OIDC callback now redirects back to the login page with `?error=oidc` and a
  friendly message instead of dead-ending on a raw JSON 401 (the error is
  logged server-side). Exposed to the SPA as `oidc_auto_redirect` in
  `/api/capabilities`. Multi-tenant installs keep their per-host realm
  dispatch — the redirect simply enters the existing `/api/auth/oidc/start`
  flow. **Off by default — existing installs behave exactly as before.**
- **`FILEX_COOKIE_DOMAIN`** (default empty). Sets the `Domain` attribute on
  the `filex_session` cookie (e.g. `.example.com`) so subdomains share the
  session. Applied on **both** login set and logout clear — clearing with a
  different scope would leave a stale cookie behind. `Secure`/`SameSite`/
  `HttpOnly` unchanged. Empty = host-only cookie, the historical behavior.

### Fixed

- **Login-page query params no longer vanish on cold load.** During the
  SPA's initial navigation the axios 401 interceptor (fired by the router
  guard's session probe) pushed a bare `/login`, racing the pending
  navigation and stripping its query (`?redirect=…`, and now `?local=1` /
  `?error=oidc`). The interceptor now stays quiet until the first route has
  settled — the router guard already owns cold-load routing. (`client.ts`.)

## [0.1.62] - 2026-07-05

### Fixed

- **Empty files and empty folders now show a size and a date in the explorer.**
  A real zero renders as `0 B` instead of `—`, and rows without a backend
  mtime (e.g. an empty folder on a synthetic-dir store, which has no
  descendants to aggregate a date from) fall back to when filex first indexed
  them. (`useLocale.formatSize`, `manager.go` index serialization.)
- **The Trash row shows its real size and date.** The explorer's virtual
  `.trash` entry now hydrates from the trash listing — total bytes of trashed
  items + the newest deletion time — instead of a bare `— / —`. It also gets a
  proper 🗑 icon (it rendered as a plain folder). (`FileExplorer.vue`,
  `ListView`/`GridView`.)

## [0.1.61] - 2026-07-05

### Added

- **Native multi-tenancy** (`FILEX_MULTI_TENANT`). One install serves N
  tenants: a *provider* = an auth realm (OIDC or local) bound to a host and
  linked to its storages. A realm's users sign in on their own domain and see
  only their own storages — even admins — and users of other realms are
  invisible (including on the permission/grant picker, search, shares/audit/
  grants lists). Per-tenant OIDC realms (host → provider → cached driver) with
  provider-scoped JIT and an immutable tenant tag; a supertenant provider is
  platform-scoped (at most one, moved only by transfer, undeletable); tenant
  lifecycle API under `/api/admin/providers` (provision / suspend / delete
  with user cascade); maintenance mode (flag off + tenants present ⇒
  supertenant-only login, fully reversible). **Off by default — a
  single-tenant install behaves exactly as before** (migration 00014 is
  additive and inert). Design + status: `docs/MULTI-TENANCY.md`; deploy
  examples: `deploy/compose/docker-compose.multi-tenant.yml` + Helm
  `ingress.extraHosts`.

## [0.1.60] - 2026-07-05

### Fixed

- **Folder dates now appear for existing files after an upgrade.** The folder
  "last activity" date added in 0.1.59 is derived from descendant file mtimes;
  files first indexed by an older version (before mtime was recorded on insert)
  carried no stored mtime, so their folders showed no date until the file's
  content next changed. The sync now backfills a missing mtime from the storage
  on its next pass — one cheap write per file, only while the value is missing.
  (`backend/internal/sync/poll.go`.)

## [0.1.59] - 2026-07-05

### Added

- **Folders now show a date in the explorer.** Alongside the recursive size
  added in 0.1.58, each folder's row reports a "last activity" date — the
  modification time of its newest descendant. It is computed in the same
  end-of-sync aggregation pass (one post-order tree walk, no extra queries) and
  cached in `nodes.backend_mtime`, so the explorer serves it straight from the
  index with no per-folder backend scan. This matters most for object stores
  whose directories are synthetic and carry no native mtime (e.g. S3 prefixes),
  which previously showed no date at all. (`backend/internal/sync/aggregate.go`,
  sqlite/postgres drivers, `db.Store.SetNodeMtime`.)

### Changed

- **The trash sidebar icon reflects its contents** — a full bin when the trash
  holds items, the empty bin otherwise. Refreshed on navigation (e.g. after
  emptying or restoring). Falls back to the empty bin if the count can't be
  fetched. (`web/src/components/Sidebar.vue`, `icons/TrashFull.vue`.)

## [0.1.58] - 2026-07-04

### Added

- **Folder sizes in the explorer.** Each folder's row shows its recursive total
  size (the sum of its descendant files). Sizes are computed once at the end of
  every storage sync and cached in the node index (`nodes.size`) — served from
  the index, never re-scanned per folder (no N+1). (`backend/internal/sync/`,
  `db.Store.AggNodes` / `SetNodeSize`.)
- **`FILEX_DEFAULT_LOCALE`** pins the UI's default language independent of the
  browser (e.g. a public demo can default to English while a user may still
  switch to another supported locale — their choice persists in
  `localStorage`). Exposed via the capabilities endpoint. (`config`,
  `capability`, `web/src/i18n`.)

## [0.1.56] - 2026-07-03

### Added

- **Optional Sentry-wire error reporting** (self-hosted GlitchTip). Set
  `FILEX_SENTRY_DSN` (+ `FILEX_SENTRY_ENVIRONMENT`) and the backend tees
  WARN+ERROR slog records to the DSN, so operational failures already logged —
  the ops worker's "ops: step failed", storage errors, recovered panics —
  surface centrally without scattering capture calls. WARN is only forwarded
  when it carries an `err` attribute (filters benign warnings); ERROR always.
  No DSN → no reporting (default build unchanged). Errors-only (no perf
  tracing). (`backend/internal/observability/`, `config`, `cmd/filex`.)

## [0.1.55] - 2026-07-03

### Fixed

- **S3 CopyObject on special-character keys 404'd** (`NoSuchKey`). The
  `CopySource` header was not URL-encoded, so any file whose name contained a
  space or non-ASCII character (e.g. Turkish `ÜYE BİLGİ … (1).doc`) failed to
  move, rename or delete-to-trash. `CopySource` is now URL-encoded per path
  segment. (`backend/internal/storage/drivers/s3/s3.go`.)
- **Delete/move now tolerate an already-missing source.** A stale index row
  (S3 object deleted out-of-band, or old test artifacts) made
  `Copy`/`Move` 404 and aborted the *entire* batch — so one phantom item broke
  a multi-select delete. The S3 `Copy` now returns `storage.ErrNotFound` for a
  missing source, and the delete/move paths (sync `vfDelete`, async ops) treat
  that as "already gone": they drop the stale cache row and carry on instead of
  failing. (`s3.go`, `ops/service.go`, `manager_mutate.go`.)

## [0.1.54] - 2026-07-03

### Fixed

- **S3 folder delete/move/copy was broken** (empty *and* non-empty folders).
  On an object store a folder is only a key prefix, but the S3 driver's
  `Move`/`Copy`/`Delete` issued a single `CopyObject`/`DeleteObject` on the bare
  folder key — which 404s (`NoSuchKey`) because no object lives at that exact
  key. Every folder delete therefore failed with
  `trash: … S3: CopyObject 404`, and the trash/restore path inherited it. The
  S3 driver is now **directory-aware**: `Move`/`Copy`/`Delete` detect a prefix
  and recurse over every object under it (preserving the relative subtree),
  so folders trash, restore, move and copy correctly. Local/SFTP were already
  dir-native (`os.Rename`/`RemoveAll`); this was S3-specific.
  (`backend/internal/storage/drivers/s3/s3.go`.)

### Changed

- Empty folders on S3 are now marked with a hidden `.empty` keep-object (was a
  bare `<path>/` marker), created by `Mkdir` and filtered from every listing —
  so an empty folder persists and shows as a directory without any visible
  child. Recursive delete/move carries the marker along.

## [0.1.53] - 2026-07-03

### Changed

- **File-drop UX polish** (follow-up to v0.1.52):
  - The public upload page now sets the native file picker's `accept` filter
    to the link's allowed extensions when configured, so pickers only offer
    valid files.
  - The upload-link invite email now spells out the configured limits
    (max files, MB per file, allowed types) and its subject names the target
    folder ("«Folder» — you've been asked to add files"). Limits are read back
    from the drop link's own settings, so the email always matches.
  - Copy buttons added next to the generated PIN in the Share and Request-files
    tabs.
  - "Request files" (Dosya İste) is no longer a separate context action — it
    lives as a tab inside the unified "Share / Permissions" popup, so folders
    expose share, per-user permissions and file-drop from one button.

## [0.1.52] - 2026-07-03

### Added

- **Public file-drop (upload link)** — the inverse of the share/download
  link. "Dosya İste" (Request files), a new folder-only action, mints a
  public `/d/{token}` link that lets anyone UPLOAD one or more files INTO a
  folder without an account. Critically it is a **blind drop**: the uploader
  never sees, lists or downloads the folder's existing contents — the target
  is resolved server-side from the token and confined; the anonymous client
  cannot influence the destination path. Each submission lands in its own
  `<date_time>_<name|anon>` subfolder (no collisions, clear provenance), with
  an optional uploader name + note (`NOT.txt`). Options: PIN, expiry, and an
  "Advanced" panel (max files, MB/file, allowed extensions, ask-name).
  Per-IP rate limiting guards the anonymous write surface. The owner is
  notified on each drop (in-app + email). Backend reuses the manager's ingest
  path (`IngestFile`/`EnsureDir`) so dropped files get identical mime
  detection, node caching and thumbnails. Server-rendered upload page (same
  dependency-free template style as the share PIN/error pages).
  (`shares.kind='drop'` + `max_uploads`/`upload_count`/`drop_settings`
  columns, migration `00013_share_drop`; `internal/api/handlers/drop.go`.)
- **Multiple recipients for share-mail** — both the download share link and
  the new upload link can be emailed to one *or many* addresses at once
  (comma/space/semicolon separated). (`POST /permissions/share-mail` now
  accepts `emails[]` + a `mode:"drop"` upload-worded body.)

## [0.1.2] - 2026-05-09

Patch closing the two follow-up bugs that surfaced after v0.1.1 went out
the door (sweep-2026-05-09 #21 fully + #25). Both are runtime-only
fixes; no schema changes, no breaking API.

### Fixed

- **Copy with collision now auto-suffixes** (sweep bug 25). The async
  copy worker used to ship `joinIntoDir(dest, src)` straight to the
  storage driver; when the user picked "Kopyasını Oluştur" / "Make a
  copy" the destination resolved to the **same key** as the source and
  S3 rejected the request as `InvalidRequest: trying to copy an object
  to itself ...`. The worker now probes the destination with `Stat`
  and falls back to `<base>-copy<ext>`, `<base>-copy-2<ext>`, … (up to
  100) until it finds a free slot, mirroring Finder/Nautilus/Explorer
  behaviour. Also handles the cross-directory paste-into-occupied
  variant (no silent overwrite). (`backend/internal/ops/service.go`)
- **3D viewer host now fully renders real GLB files** (bug 21
  follow-up). The v0.1.1 inline-style fix gave the `<model-viewer>`
  host a layout box (1048×685 instead of 0×0) — but the e2e fixture
  shipped at 104 bytes was a header-only glTF placeholder with no
  mesh data, so model-viewer's poster canvas stayed at 0×0 and emitted
  WebGL framebuffer warnings. Replaced the fixture with the Khronos
  Box.glb sample (~1.6 KB, real cube mesh) — re-verify on
  `https://fm.example.com/admin/files/edit?path=s3-test%3A%2F%2Fexample%2Fcube.glb&type=glb`
  now shows zero console warnings and a properly rendered cube. The
  v0.1.1 inline-style code change is what made the fixture-fix possible
  — both layers were needed.

## [0.1.1] - 2026-05-09

Patch release closing six bugs surfaced by the post-v0.1.0 production
sweep against `https://fm.example.com` (see `sweep-2026-05-09/sweep-report.md`
for the full matrix). No breaking changes; existing storages continue to
work, three previously dead-end UI features are now usable.

### Fixed

- **Frontend `apiBase: ''` (empty string) was silently dropped**
  (sweep bugs 22, 24). `useFileApi.resolveEndpoints` treated falsy
  `apiBase` as "no apiBase, legacy mode" — boolean-coerced empty strings
  collapsed to `null` for every derived endpoint. The relative-root
  variant is now treated as a valid prefix, so admin SPA mounts that pass
  `apiBase: ''` get a fully wired endpoint map (share, copy, move,
  restore, archive, ops). The error message that exposed this — "*XYZ*
  endpoint not configured" — should no longer surface for a legitimate
  relative-root config. (`packages/core/src/composables/useFileApi.ts`)
- **3D viewer JSON-parse crash on unsupported formats** (bugs 19, 20).
  `Viewer3D.vue` previously fed STL/OBJ/FBX/3DS files to
  `<model-viewer>`, which only understands glTF JSON; ASCII STL files
  starting with `solid <name>` triggered `JSON.parse(<solid …>)` →
  uncaught `SyntaxError`. The viewer now guards on extension, mounts
  `<model-viewer>` only for `glb` / `gltf` / `usdz`, and renders a
  download-fallback message (locale-aware
  `viewer.format_unsupported_3d`) for other 3D formats.
- **`<model-viewer>` host element collapsed to 0×0** (bug 21). The
  ancestor flexbox wasn't always granting a height to the viewer, so
  WebGL initialised with a zero-size framebuffer and emitted
  `GL_INVALID_FRAMEBUFFER_OPERATION: Attachment has zero size`. Pinned
  explicit `width: 100%; height: 100%; min-height: 480px; display:
  block` inline on the `<model-viewer>` host so the layout is stable
  regardless of parent context.
- **S3 driver default `path_style` for custom endpoints** (bug 23, part
  1). Hetzner Object Storage / MinIO / Backblaze B2 / Cloudflare R2 all
  serve path-style URLs; AWS S3 itself never sets a custom endpoint.
  When the operator does not explicitly set `path_style` and `endpoint`
  is non-empty, default to `path_style: true`. Existing storages that
  explicitly set `path_style: false` are unchanged.
- **Configurable `disable_presign` for S3 driver** (bug 23, part 2).
  Hetzner Ceph RGW emits `SignatureDoesNotMatch` for AWS SDK v2
  SigV4-presigned URLs (the canonical-string drift is non-trivial to
  unwind on the SDK side). New storage config flag `disable_presign:
  true` makes the driver advertise no-presign capability so the share
  download handler streams the bytes through the backend instead of
  redirecting to a presigned URL the bucket would reject.
- **Share handler honors `Capabilities().Presign` runtime flag.** The
  type-assertion `drv.(storage.Presigner)` always succeeds for drivers
  that implement the interface, even when the operator wants presign
  off. The handler now also checks `drv.Capabilities().Presign` and
  falls through to backend-stream when it's false.

### Notes

- The live `s3-test` storage on `fm.example.com` was retrofitted with
  `path_style: true` + `disable_presign: true` in addition to this
  release. Operators on Hetzner Object Storage should set both flags
  on existing storages (no migration provided since storage configs
  are operator-edited JSON; `path_style` will only auto-flip to true
  on newly-created storages).
- A seventh bug (#25 — duplicate-in-place sends `source == destination`
  to the S3 backend, which 400s as illegal self-copy) was discovered
  during v0.1.1 verification but is out of scope for this patch. See
  `sweep-2026-05-09/bugs.md`.

## [0.1.0] - 2026-05-06

First public release. The skeleton from earlier dev cycles plus the Round B
+ Round C delta work that turns filex into a complete self-hosted file
manager with replication, persistent queue and notifications.

### Added — core (skeleton)

- Standalone Go binary + monorepo (Vue / Web Component / React adapters).
- Storage driver interface with reference implementations: `local`, `s3`
  (Hetzner-tested), `sftp`, `webdav`, `ftp` (jlaffaye/ftp).
- Auth driver interface with reference implementations: `local` (bcrypt),
  `oidc` (Keycloak-tested), `ldap`, `proxy-header` (trusted CIDR enforced).
- DB driver interface with reference implementations: `sqlite` (default,
  modernc.org/sqlite), `mysql`, `postgres`.
- Sync worker with ETag-based diff and tombstone-false-positive guard.
- Bleve full-text search (embedded).
- Thumbnail pipeline (image GD, video ffmpeg, PDF ghostscript, Office
  libreoffice; capability-aware).
- Vue 3 admin UI (embedded into Go binary via `go:embed`).
- `@brftech/filex-core` — Vue 3 SFC source of truth.
- `@brftech/filex` — Web Component wrapper (`<filex-explorer>`).
- `@brftech/filex-react` — React adapter via `@lit/react`.
- First-run console banner with admin credentials + embed instructions.
- Multi-platform release matrix (Linux / macOS / Windows × amd64 / arm64).
- Docker images: `brftech/filex:slim` (~40 MB) and `brftech/filex:full`
  (~250 MB w/ thumbnail tools).
- GitLab CI pipeline (lint + test + build + npm publish + Docker push +
  release matrix).
- Plug & play external services: OnlyOffice, Drawio (URL-configured,
  capability-discovered).
- Monaco eager-load with highlight.js fallback for code preview/edit.

### Added — Round A (storage + auth deltas)

- **FTP driver** (`internal/storage/drivers/ftp`) — full Driver +
  Writer/Mover/Copier/Deleter/Mkdirer; FTPS (explicit AUTH TLS) and
  passive-mode toggles.
- **Storage root path guard** — `ValidateNonRootPath` rejects empty or
  `"/"` storage prefixes (s3.prefix, local.path, ftp/sftp/webdav.root)
  so filex never silently mounts at the bucket root and shadows
  pre-existing files. Wired into Storage create + update API handlers.
- **Proxy-header auth driver** (`internal/auth/drivers/proxyheader`) —
  reads `X-Auth-User`/`X-Auth-Email`/`X-Auth-Roles` from a trusted
  upstream proxy. `trusted_proxies` (CIDR list) is required; missing or
  empty list blocks `Init`. Auto-provisions users on first sight.

### Added — Round B (queue + notify + replica)

- **Persistent op queue** (`internal/queue`) — driver-based
  (`sqlite` default | `redis` | `postgres`). `ops_queue` table with
  status / priority / attempts / max_attempts / last_error /
  enqueued_at / started_at / finished_at / not_before. Worker pool
  with N goroutines, type-filtered Dequeue, exponential backoff,
  graceful Stop. Admin endpoints: `GET /admin/queue/{stats, list,
  {id}}`, `POST /admin/queue/{id}/retry`, `DELETE /admin/queue/{id}`.
- **Notifications subsystem** (`internal/notify`) — single
  `Service.Send` call fans out to (a) the in-app history table and
  (b) a configurable webhook with 3× exponential backoff retry. Per-
  user mute matrix; admin global view + smoke test trigger; webhook
  URL/token can be changed at runtime via the admin UI. Endpoints
  under `/api/notifications/...` (user) and `/admin/notifications/...`
  (admin).
- **Replica storage layer** (`internal/storage/replicated.go` +
  `internal/replica`) — `ReplicatedDriver` wraps a primary Driver and
  fans writes/moves/copies/deletes asynchronously to a replica.
  - **Read fallback**: primary errors → replica retry → emits
    `primary_read_fail` event.
  - **Path-glob rules** (`replica_rules` table) with priority asc;
    modes mirror | append_only | skip. Default-on rule mirrors when
    no rule matches (configurable via `replica_settings.default_mode`).
  - **Failure recorder** (`replica_failures` table, UNIQUE(path, op))
    tracks every fan-out failure; `Resolve` clears on success.
  - **Reconciliation** — admin "Fix all" enqueues `replica_retry` ops;
    queue handler reads from primary and writes to replica, then
    resolves the failure row.
  - **Cron status report** (`replica_status_reports` singleton) —
    user-supplied cron spec generates a snapshot on schedule (full
    payload to webhook, summary in DB + bell). Robfig/cron/v3 parses
    the spec; Reload primitive lets the admin UI change it without
    a restart.

### Added — Round C (admin UI delta pages)

- **Replica.vue** — 4-tab page (Rules / Failures / Report / Settings)
  with per-row Fix, "Fix all", Run-now, cron preset dropdown +
  advanced raw cron input.
- **Notifications.vue** — admin global feed with severity + webhook
  badges, "Send test" CTA, webhook config card (URL + bearer token).
- **Queue.vue** — 5 stat cards + paginated op table + per-row
  Retry/Cancel.
- **NotificationBell.vue** — top-nav bell with unread badge, 15s
  polling, dropdown listing the latest 15 notifications, mark-read
  on click, "View all" deep link.
- Sidebar entries (Replica / Queue / Notifications) and i18n keys
  for both `tr` and `en`.

### Changed

- `db.Store` interface gained 21 new methods (notifications + replica
  CRUD + counts + report singleton + settings).
- Server bootstrap registers `replica_retry`, `replica_report`,
  `reconcile` queue handlers; CronScheduler starts after the queue
  pool and reloads from `replica_settings` on boot.

### Fixed

- `internal/testutil` was importing `auth/drivers/local`,
  `capability` and `share` directly, so each of those packages'
  test files (which used `testutil`) failed with `import cycle not
  allowed in test`. Split into `internal/testutil/dbtest` (minimal,
  db + model + bcrypt only) — three problem suites now reference
  `dbtest` instead and `go test ./...` is green.

### Demo URL

- `files.example.com` → `demo-fm.example.com` rename across `deploy/`,
  `docs/DEPLOY_BRF.md`, `docs/MIGRATION_FISHAPP.md`,
  `deploy/keycloak-client-filex.json`, `deploy/.env.example`,
  `deploy/README.md`. Deploy host moved from main to brkip Caddy
  (DR-site, internal CA TLS).

### Known Gaps for v0.2

- Full B-plan brf-mono backend swap (filex Go binary as the sole
  files backend; legacy `Modules/FishApp/Services/*` removed). v0.1
  ships with the frontend-swap A-plan (filex UI + brf-mono PHP
  backend continues to handle storage). See
  `plan/07-integration-and-release.md` §1.
- `replicated_driver` is wired by ad-hoc admin SQL today
  (storages.role + replica_of_id). v0.2 will auto-discover replica
  pairs from the `storages` table.
- E2E Playwright suite only covers the original flows; new admin UI
  pages (Replica, Queue, Notifications) ship with manual smoke
  testing only.
- Sentry SDK integration deferred to v0.2.
