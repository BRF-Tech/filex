# filex client — command-line access to a remote filex server

The `filex` binary doubles as a remote client: the `filex client` subcommand
family talks to any running filex server over its public REST API. Nothing is
installed server-side — the CLI only uses endpoints the web UI already uses.

```
filex client login | ls | upload | download | mkdir | rm | mv | search | share
```

The same binary also carries two commands that are not part of `client`:
[`filex sync`](SYNC.md), which keeps a local folder in step with the server, and
[`filex mount`](#filex-mount--the-server-as-a-folder), which attaches the server
as a folder (or a drive letter on Windows) without copying anything.

## Installation

Grab the release binary for your platform (the same binary that runs the
server) and put it on your `PATH`:

```bash
curl -fL -o filex https://github.com/BRF-Tech/filex/releases/latest/download/filex-linux-amd64
chmod +x filex && sudo mv filex /usr/local/bin/
```

Or build from source: `cd backend && go build ./cmd/filex`.

## Connecting

Connection settings resolve in this order (first non-empty wins, per field):

1. `--url` / `--token` flags
2. `FILEX_URL` / `FILEX_TOKEN` environment variables
3. `~/.filex/cli.yaml` (written by `filex client login`)

The token may be a **session token** (minted by `login`) or a durable
**API token** created in the admin panel / self-service token page — the
server accepts both as `Authorization: Bearer`.

Two variables move where the CLI keeps its own files, for a checkout or a run
that must not touch your real state — both ignored when empty:

| Variable | Default | What it moves |
|---|---|---|
| `FILEX_CLI_CONFIG` | `~/.filex/cli.yaml` | The saved URL + token |
| `FILEX_SYNC_DIR` | `~/.filex/sync` | The [sync](SYNC.md) pairs, per-pair baselines and local trash |

The [portable desktop app](DESKTOP.md#portable-windows) sets `FILEX_SYNC_DIR`
for exactly this reason: its whole promise is that everything it keeps sits in
one folder beside the `.exe`, and the sync trash holds real copies of deleted
files.

### Interactive login

```bash
filex client login --url https://fm.example.com
E-mail: you@example.com
Password: ********
Logged in as you@example.com on https://fm.example.com
Token saved to /home/you/.filex/cli.yaml (0600)
```

- The password prompt never echoes. Piped stdin also works
  (`printf 'pass\n' | filex client login --url … --email you@example.com`),
  which is handy for provisioning scripts.
- Accounts with TOTP enabled pass the second factor via `--totp 123456`.
- The config file is written with owner-only permissions (`0600`) because it
  carries your token. A re-login also tightens a pre-existing looser mode.

### CI / scripts (no config file)

```bash
export FILEX_URL=https://fm.example.com
export FILEX_TOKEN=fxt_…        # durable API token from the panel
filex client upload build/report.pdf docs://ci-artifacts/
```

## Remote paths

Every remote argument uses the `adapter://relative/path` form, where the
adapter is the storage name shown in the panel (e.g. `docs`, `s3-test`).
`filex client ls` with no argument lists the adapters you can access.
`..` segments are rejected client-side.

## Commands

### ls

```bash
filex client ls                      # storage (adapter) overview
filex client ls docs://              # storage root
filex client ls docs://reports/2026
```

```
TYPE  SIZE      MODIFIED          NAME
dir   -         2026-07-01 10:00  taslaklar
file  120.6 KB  2026-07-12 09:31  rapor.pdf
```

### upload

```bash
filex client upload ./rapor.pdf docs://reports/          # keep the local name
filex client upload ./rapor.pdf docs://reports/final.pdf # rename while uploading
```

An existing remote **folder** target keeps the local basename; otherwise the
last path segment becomes the uploaded filename. Nothing is ever buffered in
memory.

**Large files are resumable.** Anything from 8 MiB up goes over the staged
protocol (`docs/UPLOADS.md`): the file is sent in chunks, the server holds them,
and a dropped connection costs the current chunk rather than the file. The
resume point survives the process — a bookmark under `~/.filex/uploads` records
the upload id, and the next run asks the server where to continue:

```bash
filex client upload ./4gb.tar docs://backups/    # link dies at 62%
filex client upload ./4gb.tar docs://backups/    # continues at 62%
```

A `sha256` is declared before the first chunk and verified by the server over
the whole assembled file at commit, so a resume cannot quietly splice two
different files together. If the local file changed in between (different size
or mtime) the bookmark is discarded and the upload starts fresh.

| Variable | Default | Meaning |
|---|---|---|
| `FILEX_UPLOAD_STATE` | `~/.filex/uploads` | where resume bookmarks live |

Servers older than the staged path (or with no staging directory configured)
answer `404`/`501` at `begin`; the client then falls back to the single
multipart POST automatically.

#### Recursive upload (`-r` / `--recursive`)

```bash
filex client upload -r ./proje docs://reports/       # -> docs://reports/proje/…
filex client upload -r ./proje docs://reports/arsiv  # rename form: tree lands AT arsiv/
```

```
Uploaded proje/a.txt -> docs://reports/proje/a.txt
Uploaded proje/sub/b.txt -> docs://reports/proje/sub/b.txt
Done: 2 file(s), 3 folder(s), 0 error(s)
```

- Destination semantics mirror the single-file form: an existing remote
  folder (or trailing `/`) receives the local folder **by name**; a
  non-existing target becomes the new remote folder name.
- Remote folders are created as needed — **empty local folders included**.
  The destination's *parent* must already exist (same as `mkdir`).
- **Symlinks are skipped** with a warning on stderr; they are never
  followed, so link cycles can't loop the walk.
- A failed folder creation skips that subtree; a failed file is recorded
  and the walk continues. The summary lists every failure and the command
  exits **non-zero** when anything failed — safe for scripts.
- With `--json` the command prints one summary object instead:
  `{"local":…,"remote":…,"files":2,"dirs":3,"skipped_symlinks":[…],"errors":[…]}`.

### download

```bash
filex client download docs://reports/rapor.pdf            # ./rapor.pdf
filex client download docs://reports/rapor.pdf /tmp/      # into a directory
filex client download docs://reports/rapor.pdf -          # to stdout (pipe it)
```

An existing local file at the target is overwritten; a failed transfer never
leaves a partial file behind.

### mkdir / rm / mv

```bash
filex client mkdir docs://reports/2027
filex client rm docs://reports/eski.pdf docs://tmp        # multiple args OK
filex client mv docs://inbox/a.pdf docs://reports/        # move into folder
filex client mv docs://inbox/a.pdf docs://inbox/b.pdf     # rename
filex client mv docs://inbox/a.pdf docs://reports/b.pdf   # move + rename
```

- `rm` is a **soft delete** — items land in the server-side trash and can be
  restored from the panel.
- `mv` follows Unix semantics: an existing directory target (or a trailing
  `/`) moves the item into it; otherwise the last segment is the new name.
  Move + rename across folders takes two API calls under the hood (the
  server has no combined verb). Cross-adapter moves are not supported.

### search

```bash
filex client search invoice                     # names + indexed content
filex client search "meeting notes" --scope content
filex client search report --scope name --limit 20 --storage-id 2
filex client search "invoice 2026"              # finds invoice_2026.pdf
filex client search "report tag:accounting"     # narrow to a tag
```

```
PATH                 MATCHED  SNIPPET
/inbox/report.pdf    name
/notes/july.md       content  …figures in the attached «report» are…
```

`--scope` is `name`, `content` or `all` (default). Content hits require the
server's search index (see `docs/SEARCH.md`).

The query itself is the same one the web UI uses: separators (`.`, `-`, `_`,
space) are interchangeable, every word has to match — in any order, and a word
may be answered by a folder (`main code` finds `Code/main.go`) — a single typo
is forgiven, and `tag:` / `-tag:` filter by tag. Results arrive in rank order,
exact filename matches first — quote a query that contains spaces.

### share

```bash
filex client share docs://reports/rapor.pdf --pin --expires-days 7
```

```
URL:     https://fm.example.com/s/6a1b2c…
PIN:     96539559
Expires: 2026-07-24 13:44
```

Folders can be shared too — the public link serves them as a ZIP. The PIN is
generated server-side and shown **once**; `--expires-days 0` (default) means
no expiry.

## `filex mount` — the server as a folder

The same binary attaches a remote filex to this machine, over the same HTTPS the
browser uses. It is a sibling of `filex client` / `filex sync` rather than part
of them, so it takes the same `FILEX_URL` / `FILEX_TOKEN` and the same
`~/.filex/cli.yaml`.

```bash
export FILEX_URL=https://filex.example.com
export FILEX_TOKEN=<token>

mkdir -p ~/filex && filex mount ~/filex        # every storage you can see
filex mount --remote docs:// ~/docs            # one storage
filex mount --remote 'docs://projects/acme' --read-only ~/acme
```

On **Windows** the mountpoint is usually a drive letter, and
[WinFsp](https://winfsp.dev) (free) has to be installed once:

```powershell
filex mount Z:      # ⚠ Z: must be FREE — the letter is created, not reused
```

Stop it by unmounting (`fusermount -u ~/filex`) or with Ctrl-C on Windows.

> ⚠⚠ **It is not a sync.** Nothing is copied to this machine except a bounded
> read cache, so a mount opens one file out of a hundred thousand without
> downloading the rest — and nothing is available when you are offline. For
> that, use [`filex sync`](SYNC.md).

> ⚠ **macOS is not supported.** It needs macFUSE, whose Go binding needs a C
> toolchain filex deliberately does not use and whose licence forbids a
> commercial program from installing it. The command refuses there rather than
> appearing to work and doing nothing.

| Flag | What it does |
|---|---|
| `--remote` | what to mount: empty for every storage, `docs://` for one, `docs://sub/dir` for a subtree |
| `--read-only` | refuse every write through this mount |
| `--block-size` | read granularity (default 4 MiB) — one HTTPS request per block |
| `--cache-blocks` | how many blocks to keep in memory (default 64) |
| `--attr-ttl` | how long a listing is trusted before it is re-fetched (default 5s) |
| `--spool-dir` | where in-flight writes are spooled |
| `--debug` | log every filesystem call |

A file written through the mount is uploaded when the program closes it, not
while it is being written — the REST API takes a whole object, and a partial
upload committed under the real name would replace a good file with a torn one.
Editing a very large file in place is therefore slower here than on a local
disk; copying it in and out is not.

Full protocol picture: [PROTOCOLS.md](PROTOCOLS.md).

## JSON output

Every command accepts `--json` and then prints the server's raw JSON response
(or a small result object for local operations like `download`) — ideal for
`jq` pipelines:

```bash
filex client ls docs://reports --json | jq -r '.files[].basename'
filex client share docs://x.pdf --json | jq -r '.share.url'
```

## Errors & exit codes

Errors go to **stderr** and the process exits **1**. A `401` appends a hint:

```
filex: HTTP 401: unauthorized — token missing/expired; run `filex client login`
```
