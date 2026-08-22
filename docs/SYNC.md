# Folder sync

Keep a folder on your computer and a folder on a filex server in step, in both
directions. This is the Dropbox-shaped feature: files live on your disk, work
offline, and catch up when you reconnect.

It is available two ways, and they are the same engine:

- **The desktop app** — Settings → *Sync folders*. Runs in the background while
  the app sits in the tray.
- **The CLI** — `filex sync`, for servers, scripts and headless machines.

Both read and write `~/.filex/sync/pairs.json`, so a folder paired in the app is
visible to the CLI and the other way round.

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
| Changed in **both** places | **Both are kept** — yours keeps its name, the server's copy lands beside it as `report (server copy 2026-08-07 14-05).xlsx` |
| Deleted on one side, untouched on the other | The delete carries across |
| Deleted on one side, **edited** on the other | The edit wins; the file comes back |
| A folder on one side, a file of the same name on the other | Refused, both kept — the same collision the server-side guard rejects |

### The first run never deletes

With no record of a previous sync there is no way to tell *"you deleted this"*
from *"you have not downloaded it yet"*. Guessing wrong empties someone's
folder, so the first pass is a union merge. From the second run on, deletes
propagate.

### An interrupted first run resumes

A first run of a large tree can be cut short — a closed laptop, a dropped
connection. On the next run there is still no history, and both sides hold the
files that already came down. Those are **adopted**, not conflicted: a download
stamps the server's own modification time on the local copy, and two files with
no history, the same size and a modification time within **two seconds** of each
other are taken to be the same file (FAT stores times in two-second steps, and
some tools land a millisecond off). Outside that window the usual rule applies
and both copies are kept. Change detection against a recorded baseline stays
exact — the tolerance exists only for files with no history.

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
filex sync trash [--pair <id>] [--restore <path>]
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
- **Polling, not file-system events.** `--watch` re-scans on an interval
  (the desktop app uses 30 seconds). Very large folders take as long as a walk
  takes — the server side is listed eight folders at a time, the local side is
  one directory walk.
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
the same thing with more detail.

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
