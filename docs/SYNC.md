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

## Commands

```
filex sync add <local-folder> <storage://path> [--account <label>]
filex sync list [--json]
filex sync remove <pair-id>
filex sync run [--pair <id>] [--account <label>] [--watch <interval>] [--dry-run] [--quiet]
filex sync trash [--pair <id>] [--restore <path>]
```

`--dry-run` prints exactly what would happen and touches nothing — worth running
the first time you pair a folder that already has files in it.

`--account` limits a run to the pairs recorded against one signed-in server. One
token authenticates against exactly one server, so the desktop app runs one
watcher per account rather than one for all of them.

Removing a pair stops the syncing and **leaves every file where it is**, on both
sides. Unpairing is not deleting.

---

## What is not synced

- The engine's own state (`.filex-sync`), or it would sync its bookkeeping,
  which changes, which schedules another sync — forever.
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
  takes.
- **Moves are a delete plus an add.** A renamed 2 GB file is re-uploaded, not
  moved server-side.
- **A failed transfer is retried on the next run,** and is deliberately *not*
  recorded as settled — one broken file cannot wedge the folder.

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
