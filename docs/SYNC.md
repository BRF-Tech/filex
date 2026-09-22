# Folder sync

Keep a folder on your computer and a folder on a filex server in step, in both
directions. This is the Dropbox-shaped feature: files live on your disk, work
offline, and catch up when you reconnect.

It is available two ways, and they are the same engine:

- **The desktop app** — Settings ⚙ → *Synced folders*. Runs in the background
  while the app sits in the tray.
- **The CLI** — `filex sync`, for servers, scripts and headless machines.

Both read and write `~/.filex/sync/pairs.json`, so a folder paired in the app is
visible to the CLI and the other way round.

**`FILEX_SYNC_DIR` moves that whole directory** — pairs, the per-pair baselines
and the local trash — somewhere else. The
[portable Windows app](DESKTOP.md#portable-windows) sets it so its state lands
beside the `.exe` rather than in the home directory of a machine the user is
only borrowing; set it yourself when you want a self-contained checkout or a
test run that cannot disturb your real pairings. An empty value is ignored.

---

## Quick start

```bash
filex client login                        # once, per server
filex sync add ~/Documents/work docs://work
filex sync run                            # one pass
filex sync run --watch 30s                # keep going
```

The remote side is always `storage://path`. A bare path is ambiguous as soon as
a server hosts more than one storage, so it is refused rather than guessed.

---

## What it does, and what it refuses to do

Sync deletes files for a living, so the rules below are chosen so that the
failure mode is *too many copies*, never *the file is gone*.

| Situation | What happens |
|---|---|
| First run of a pair | **Nothing is deleted.** Both sides are merged. |
| New on one side | Copied to the other |
| Changed on one side | Copied over |
| Changed in **both** places, to the **same bytes** | Nothing to keep twice: the path is settled, no copy is made |
| Changed in **both** places, differently | **Both are kept, on both sides** — yours keeps its name, the server's version lands beside it as `report (server copy 2026-08-07 14-05).xlsx` here *and* on the server |
| Deleted on one side, untouched on the other | The delete carries across |
| Deleted on one side, **edited** on the other | The edit wins; the file comes back |
| A folder on one side, a file of the same name on the other | Refused, nothing touched — the same collision the server-side guard rejects |

### A conflict compares the bytes first

"Changed in both places" is decided on size and modification time, and most
such changes are the same file: a pair whose history was lost, a reinstalled
client, a scanner that touched a timestamp. So before keeping two copies the
engine downloads the server's version and **compares it byte for byte** with
yours. The same bytes settle the file; only a real difference makes a copy.

When it does, the copy goes to the server too, under the same name, and both
files are recorded at once — a copy you later tidy away on the server is
removed here as well (into the local trash) instead of coming back as a new
file. A copy's name never nests: a conflict on `report (server copy …).xlsx`
makes another `report (server copy …).xlsx`, not `report (server copy …)
(server copy …).xlsx`, and a name already taken gets ` (2)`. (Before this, one
busy spreadsheet on a client that kept losing its history grew 14,724 nested
copies, one every ~30 seconds.)

### An edit made while a run is busy is not lost

A run of a large tree takes a while — a first sync can take hours. Only what
the run actually **transferred** is recorded as in step; a file that changed
on either side while the run was busy with others keeps its previous history,
so the next run sees the change and carries it across.

### The first run never deletes

With no record of a previous sync there is no way to tell *"you deleted this"*
from *"you have not downloaded it yet"*. Guessing wrong empties someone's
folder, so the first pass is a union merge. From the second run on, deletes
propagate.

### A first run that would re-upload a stale copy asks first

With no history, a file that is **new here** and a file that was **deleted on
the server** look exactly the same — and the first run copies both up. That is
right for a folder you are pairing for the first time, and exactly wrong for
an old mirror of a folder that has since been tidied on the server: one client
put 9,665 cleaned-up files back that way.

So a first run that would upload **more than 100** files the server does not
have, into a server folder that **already has files**, holds them instead. The
rest of the run goes ahead (downloads, identical files); the held items — and
any file that differs between the two sides — are left exactly as they are,
and the pair shows `holding N item(s)` until you decide:

```bash
filex sync confirm <pair-id>   # they are wanted: the next run uploads them
filex sync discard <pair-id>   # they are stale: into the local sync trash; the next run makes this side match the server
```

`discard` never touches a file you edited after it was held, and everything it
moves is recoverable with `filex sync trash` for 30 days. A first sync into an
**empty** server folder is never held. `filex sync list --json` carries
`hold_new` and `held` for the desktop app, which offers the same two choices.

### An interrupted first run resumes

A run of a large tree can be cut short — a closed laptop, a killed watcher, a
dropped connection. Two things make the next run pick up where it stopped
instead of starting over:

- **The engine checkpoints its history while it works.** Every 50 settled
  transfers (or 15 seconds), the files that are now in step on both sides are
  written to the pair's history — uploads included, with the server asked for
  what it stored. The next run is then an ordinary incremental one: it finishes
  the transfers that were still pending and touches nothing that settled.
- **Files with no history that match are adopted, not conflicted.** A download
  stamps the server's own modification time on the local copy, and two files
  with no history, the same size and a modification time within **two
  seconds** of each other are taken to be the same file (FAT stores times in
  two-second steps, and some tools land a millisecond off). Outside that window
  the usual rule applies and both copies are kept. Change detection against a
  recorded baseline stays exact — the tolerance exists only for files with no
  history.

A checkpoint records only files that settled on both sides; everything else is
still "never synced" and is copied across, never deleted.

### A mirror that is missing is not a mirror that was emptied

If a pair's local folder is **gone** — its drive unplugged, the folder moved by
hand — while the pair still has history, the run refuses and says so. Creating
the folder empty and carrying on would read as "every file deleted here", and
the next round would carry that to the server. Use `filex sync move` if the
folder moved, plug the drive back in, or `filex sync remove` to stop syncing
it. The same holds for the server side: a folder that could not be **listed**
(a timeout, a proxy error) fails the run instead of reading as deleted — only a
folder the server says does not exist is skipped.

### Deletions are recoverable for 30 days

Anything sync removes **from your machine** is moved aside, not deleted:

```bash
filex sync trash                                  # what can still be recovered
filex sync trash --restore reports/2026/q1.xlsx   # put it back
```

A restored file is treated as new on the next run, so it goes back to the server
too. Files deleted **on the server** go to the server's own trash, as usual.

### Clocks are never compared across the two sides

An upload gives the server its own modification time, so the two sides
legitimately differ the moment after a successful sync. Comparing them would
make every file look permanently conflicted. Each side is compared against what
*it* looked like at the end of the last run instead, which also means clock skew
between your machine and the server changes nothing.

---

## In the desktop app: "Keep on this computer"

Nothing below changes when the desktop app drives the engine — but you rarely
type any of it there. Right-click a folder, a single file, or a whole storage in the window →
**Keep on this computer**, and the app makes the pair for you: one root folder
per account, chosen once (and movable later from Settings), with every kept item
mirrored under it as `<root>/<storage>/<path…>`. Every row then carries a badge
saying where it lives — ✓ here, ◐ holding kept items below, ⟳ syncing now,
☁ online-only. Keeping a parent absorbs kept children into a single
pair; **Keep online only** removes the pair and asks whether the local copy
should go to the Trash or stay. See
**[docs/DESKTOP.md](DESKTOP.md#keeping-folders-on-this-computer)**.

The app also has a **Pause sync** switch (tray menu and Settings). Paused, it
runs no watcher at all — for any account, and across restarts — until it is
resumed. A `filex sync run` you start in a terminal is not affected: the pause
is the app's, not the pairs'.

A pair that is holding items for a decision (`hold_new` / `held` in
`filex sync list --json`) gets a notice on its card in Settings with the count
and two buttons — **Upload them** runs `filex sync confirm <pair>`, **Move to
local trash** runs `filex sync discard <pair>` after asking — and the account's
watcher is restarted after either.

Settings' **Download limit**, **Upload limit** and **When to sync** presets are
handed to every watcher as `--limit-down` / `--limit-up` (KiB/s) and
`--window HH:MM-HH:MM`; a change restarts the watchers. Nothing is passed while
they are left at *Unlimited* / *Any time*.

When the server refuses an account's token (HTTP 401 — revoked or expired), the
engine stops instead of retrying, and the app keeps that account's watcher
stopped — across restarts — until you **Reconnect** it. Reconnecting as the
same person keeps the account's pairs, so the next round is an ordinary
incremental one.

⚠ A pair's remote path may not contain a `..` segment. Nothing legitimate needs
one — the server resolves paths from its own storage root — and a client that
turns a remote path into a local folder name would otherwise be told, by the
server, to write outside the folder the user chose.

---

## Commands

```
filex sync add <local-folder> <storage://path> [--account <label>] [--file]
filex sync list [--json]
filex sync move <pair-id> <new-local-path>
filex sync remove <pair-id>
filex sync run [--pair <id>] [--account <label>] [--watch <interval>] [--dry-run] [--quiet] [--transfers <n>]
               [--limit-down <KiB/s>] [--limit-up <KiB/s>] [--window HH:MM-HH:MM]
               [--watch-max <duration>] [--full-every <duration>]
filex sync trash [--pair <id>] [--restore <path>]
filex sync confirm <pair-id>
filex sync discard <pair-id>
```

`move` repoints a pair at a folder (or file) that you have **already moved** on
disk, and keeps its sync history — the next run is an ordinary incremental pass,
not a first-run merge. Removing and re-adding the pair instead throws the history
away, and the merge that follows treats every file the machine ever uploaded as
changed in both places. The new path must exist: a pair pointed at nothing would
be created empty on the next run, and an empty mirror under surviving history
reads as "every file deleted here". (The desktop app's *change the filex folder*
uses `move` for exactly this reason.)

`--transfers` caps how many uploads and downloads run at once — **4 by default**,
`1` restores the fully serial engine. A tree of small files is otherwise priced
at one full round-trip per file; measured on a live deployment, 2 GB of ~400 KB
files crawled at 0.24 MB/s with the network idle. Folder creation still goes
first, and deletes and conflict copies still run one at a time in the planner's
deepest-first order. Server folders are listed eight at a time for the same
reason: the inventory of a 3,000-folder tree is minutes rather than a quarter of
an hour.

`--limit-down` / `--limit-up` cap the transfer rate in KiB/s — **all transfers
of the run together**, not each one: `--transfers` only changes how many files
move at once, never how fast. A first sync of 52 GiB once held a server's
~18 Mbit line for nine hours and everyone else using that server felt it. The
limit paces the file bodies, not the connection, so the client's
dead-connection pings are never delayed by it.

`--window 22:00-07:00` only starts rounds between those local times (a window
may wrap midnight). A round still busy when the window closes is stopped the
way Ctrl-C stops it — its checkpoint written — and carries on in the next
window. Outside the window a watcher says `sync: waiting for the sync window …`
once and waits; a one-off `sync run` does nothing and says so.

The `transfer:` progress line carries bytes and, after the first few seconds,
an estimate: `transfer: 120/11704 (1.2 GiB of 52.6 GiB, about 8h 10m left)`.

`--dry-run` prints exactly what would happen and touches nothing — worth running
the first time you pair a folder that already has files in it.

`--file` pairs **one file** instead of a folder: `filex sync add ~/notes.md
docs://team/notes.md --file`. Same planner, same rules, same 30-day local trash —
the snapshots simply carry one entry. Both sides keep the file's name (a pair
that renamed across would sync the wrong entry, so it is refused), and a restore
from `sync trash` lands beside the file rather than inside it.

`--account` limits a run to the pairs recorded against one signed-in server. One
token authenticates against exactly one server, so the desktop app runs one
watcher per account rather than one for all of them.

`--quiet` drops the per-file lines and keeps the summary — but progress lines
still print: inventory counts while the server tree is listed, `transfer: 12/345`,
settling. The desktop app runs the engine exactly this way and mirrors the last
line into its panel, and a first sync of a large store spends minutes listing
before it transfers anything; silence there reads as a broken app.

A watcher started with `--watch` **re-reads `pairs.json` between rounds**, so a
folder paired — or unpaired — while it runs joins (or leaves) the next round.
The desktop app keeps one watcher per account alive for days and does not
restart it for a new pair.

Removing a pair stops the syncing and **leaves every file where it is**, on both
sides. Unpairing is not deleting.

---

## What is not synced

- The engine's own state (`.filex-sync`), or it would sync its bookkeeping,
  which changes, which schedules another sync — forever.
- Its own half-written downloads (`.filex-part-*`). A download lands in a
  temporary file beside its destination and is renamed into place when
  complete; one left behind by a crash is never uploaded as a file nobody
  named.
- Symlinks. A link pointing outside the folder would upload files you never put
  there; one pointing inside makes the walk infinite.
- OS clutter: `.DS_Store`, `Thumbs.db`, `desktop.ini`, recycle bins.
- Anything unreadable — a locked file is reported, not fatal. One file must not
  stop the other thousand.

---

## Limits worth knowing

- **Change detection is size + modification time**, not a content hash. A file
  edited so that its size *and* timestamp are unchanged is not noticed. Hashing
  every file on every pass would make large folders unusable; this is the same
  trade-off rsync makes by default.
- **Asking, not walking.** `--watch` looks at every pair each interval (the
  desktop app uses 30 seconds), but a pair only **runs** when something moved:
  its local tree changed (one local walk, no request), or the server's change
  log says something under its folder changed (`action=changes`, one request),
  or its last run failed. A full walk still happens every `--full-every`
  (default 30 minutes) as a safety net for changes the log cannot see — bytes
  written straight into the storage and found by a scan. Before this, one Mac
  with 7,048 synced folders listed its whole tree every round: 100–150
  thousand requests an hour, around the clock. Against a server without the
  change log, a quiet pair's walks back off from the interval to `--watch-max`
  (default 5 minutes) and snap back as soon as something moves.
- **A dead connection is detected, not waited out.** The client pings an idle
  HTTP/2 connection (30 s) and bounds dialing, TLS and the wait for response
  headers; a transfer's body is deliberately unbounded, so a large file may
  take as long as it takes — a hang may not.
- **Moves are a delete plus an add.** A renamed 2 GB file is re-uploaded, not
  moved server-side.
- **A failed transfer is retried on the next run,** and is deliberately *not*
  recorded as settled — one broken file cannot wedge the folder. Since the move
  to staged uploads that retry is **cheap for large files**: anything from 8 MiB
  up is sent in chunks the server keeps, and the next run continues from the
  offset it reports rather than from byte 0 — across a restarted watcher, a
  closed laptop or a reboot. See *Resuming in the CLI* in `docs/UPLOADS.md`.

---

## Troubleshooting

**"The sync engine is not bundled with this build."** The desktop package could
not find the `filex` binary it ships. Install the CLI and point the app at it
with `FILEX_CLI=/path/to/filex`, or reinstall the app.

**Nothing transfers and the panel shows an error.** The line under each pair is
the engine's own last message. `filex sync run --pair <id>` in a terminal shows
the same thing with more detail. The desktop app clears it once a later round of
that pair goes through, so an error still on screen is one that is still
happening.

**A conflict copy appeared and I only edited it in one place.** Something else
wrote to the server copy — another device, a share, or a web-UI save. Both
versions are on disk; keep the one you want and delete the other.

**"sync folder … is missing but pair … has history; nothing was touched."** The
pair's local folder is not where the pair says it is. If you moved it,
`filex sync move <pair-id> <new-path>` keeps the history; if it lives on a drive
that is not plugged in, plug it in; if you meant to stop syncing it,
`filex sync remove <pair-id>`. The engine will not create the folder empty for
you — see above.

**"list …: HTTP 502" (or a timeout) and nothing happened.** The server folder
could not be listed, so the run stopped rather than treat the folder as gone.
It is retried on the next round.

**The watcher stopped: "signed out: the server no longer accepts this token
(HTTP 401)".** The token was revoked (or has expired), and a watcher that kept
retrying it would only fill the server's log. `filex sync run` stops at the
first 401 with **exit status 3** — every other failure exits 1 — so a
supervisor can tell "sign in again" from "try again". Sign in again
(`filex client login`, or *Reconnect* in the desktop app) and start it again.
A stop request (Ctrl-C, SIGTERM) cancels the run in flight cleanly: the
checkpoint is written, so the next run resumes where this one stopped.
