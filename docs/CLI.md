# filex client — command-line access to a remote filex server

The `filex` binary doubles as a remote client: the `filex client` subcommand
family talks to any running filex server over its public REST API. Nothing is
installed server-side — the CLI only uses endpoints the web UI already uses.

```
filex client login | ls | upload | download | mkdir | rm | mv | search | share
```

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
last path segment becomes the uploaded filename. The body is streamed
(multipart), so large files don't load into memory.

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
filex client search fatura                      # names + indexed content
filex client search "toplantı notu" --scope content
filex client search rapor --scope name --limit 20 --storage-id 2
```

```
PATH                 MATCHED  SNIPPET
/inbox/rapor.pdf     name
/notlar/temmuz.md    content  …ekteki «rapor» taslağı üzerinden…
```

`--scope` is `name`, `content` or `all` (default). Content hits require the
server's search index (see `docs/SEARCH.md`).

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
