# filex client - command-line access to a remote filex server

The `filex` binary doubles as a remote client: the `filex client` subcommand
family talks to any running filex server over its public REST API. Nothing is
installed server-side - the CLI only uses endpoints the web UI already uses.

```
filex client login | ls | upload | download | mkdir | rm | mv | cp | search | share
filex client trash | versions | tag | actions | run | archive | plugins
```

The same binary also carries three commands that are not part of `client`:
[`filex sync`](SYNC.md), which keeps a local folder in step with the server,
[`filex mount`](#filex-mount---the-server-as-a-folder), which attaches the server
as a folder (or a drive letter on Windows) without copying anything, and
[`filex decrypt`](#filex-decrypt---an-encrypted-folder-offline), which turns a
downloaded end-to-end encrypted folder - or a single encrypted `.fxe` file -
back into plain files, offline.

The commands that run on the server machine against its own database
(`filex serve`, `filex migrate`, `filex admin`, `filex storage`) are not
remote: `filex storage add` - its driver, config and sync mode, the lazy
catalogue's settings included - is in [STORAGE.md → CLI](STORAGE.md#cli).

## Installation

Grab the release binary for your platform (the same binary that runs the
server) and put it on your `PATH`:

```bash
curl -fL -o filex https://github.com/BRF-Tech/filex/releases/latest/download/filex-linux-amd64
# arm64 (a Raspberry Pi 4/5, an Ampere or Graviton server): filex-linux-arm64
chmod +x filex && sudo mv filex /usr/local/bin/
```

Every platform has an arm64 build beside the x64 one:

| Platform | x64 | arm64 |
|---|---|---|
| Linux | [`filex-linux-amd64`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-linux-amd64) | [`filex-linux-arm64`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-linux-arm64) |
| macOS | [`filex-darwin-amd64`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-darwin-amd64) (Intel) | [`filex-darwin-arm64`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-darwin-arm64) (Apple Silicon) |
| Windows | [`filex-windows-amd64.exe`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-windows-amd64.exe) | [`filex-windows-arm64.exe`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-windows-arm64.exe) |

`uname -m` says which one a Linux or macOS machine is (`x86_64` is x64,
`aarch64` and `arm64` are arm64); on Windows, Settings → System → About →
*System type*. Each release starts the arm64 builds on arm64 machines -
server, login, upload, download - before it publishes them.

Or build from source: `cd backend && go build ./cmd/filex`.

Or with a package manager - the CLI is plain **`filex`** in each (the desktop
app is `filex-app`):

```bash
brew install brf-tech/filex/filex     # macOS and Linux - Homebrew tap BRF-Tech/homebrew-filex
```

On macOS the binary is not signed with an Apple Developer ID, so macOS may
refuse its first run: allow it once in System Settings → Privacy & Security
(*Open Anyway*).

On Windows the package will be `winget install BRFTech.filex` (it puts `filex`
on the PATH; open a new terminal after it). Every release submits it, and it is
**not installable yet**: a new winget package waits for its first review by the
winget moderators, and until that is approved `winget` does not find it. Take
`filex-windows-amd64.exe` from the release meanwhile, as above.

A binary installed this way upgrades itself with `filex self-update`
([UPDATES.md](./UPDATES.md)). A filex that came from a package manager
(Homebrew, winget, Snap) is upgraded by that package manager instead:
`filex self-update` refuses there and prints the command to run - see
[Package-manager installs](./UPDATES.md#package-manager-installs).

## Connecting

Connection settings resolve in this order (first non-empty wins, per field):

1. `--url` / `--token` flags
2. `FILEX_URL` / `FILEX_TOKEN` environment variables
3. `~/.filex/cli.yaml` (written by `filex client login`)

⚠ **The saved session goes only to the address it was saved with.**
`~/.filex/cli.yaml` holds one server's session: a `--url` or `FILEX_URL` that
names another address gets neither its token nor its realm, and the command
stops before it sends anything, with
`no saved session for https://other.example.com - … run filex client login --url https://other.example.com or set FILEX_TOKEN`.

- *The same address* is the same scheme, host (any case, a final dot aside),
  port (`443` / `80` written or not) and base path, a trailing slash aside -
  `https://fm.example.com/filex/` is `https://FM.example.com:443/filex`.
- Another name for the same machine - its IP, a short name - is another
  address: nothing on this side can prove it is the same server. So is `http`
  against `https` (sign in over `https`), another port and another base path.
- After a [handoff](#multi-tenant-servers-the-realm) the saved address is the
  tenant's own, so the platform's address needs a login of its own.
- A token you give on purpose - `--token` or `FILEX_TOKEN` - goes to whatever
  address you give with it.
- `filex sync` and `filex mount` resolve their connection the same way.

Before 0.50 the saved token went to any `--url` or `FILEX_URL`: a mistyped or
borrowed address received your session.

The token may be a **session token** (minted by `login`) or a durable
**API token** - an API key from the admin panel's API / MCP page or the explorer's
**API keys** entry - the
server accepts both as `Authorization: Bearer`.

⚠ An API token does only what its verbs name, on every command: `ls`,
`download` and `search` need `read`; `upload`, `mkdir`, `mv` and `share` need
`write`; `rm` needs `delete`. A command the token cannot run fails with
`HTTP 403: token missing scope: <verb>` - mint a token with the verbs the
script uses ([RBAC.md → API tokens](RBAC.md#api-tokens-verbs-on-every-surface)).
The account behind it is held to its own permissions too
([PERMISSIONS.md](PERMISSIONS.md)): `share` also needs `share.links`.

Two variables move where the CLI keeps its own files, for a checkout or a run
that must not touch your real state - both ignored when empty:

| Variable | Default | What it moves |
|---|---|---|
| `FILEX_CLI_CONFIG` | `~/.filex/cli.yaml` | The saved URL + token (and the [realm](#multi-tenant-servers-the-realm) it was signed in to) |
| `FILEX_SYNC_DIR` | `~/.filex/sync` | The [sync](SYNC.md) pairs, per-pair baselines and local trash |

The [portable desktop app](DESKTOP.md#portable-windows) sets `FILEX_SYNC_DIR`
for exactly this reason: its whole promise is that everything it keeps sits in
one folder beside the `.exe`, and the sync trash holds real copies of deleted
files.

### Interactive login

A filex [served under a sub-path](DEPLOYMENT.md#serving-filex-under-a-sub-path)
is logged in to with its path: `--url https://example.com/filex`.

```bash
filex client login --url https://fm.example.com
Email: you@example.com
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
- `--email` takes a user name too, as the web sign-in form does.
- Multi-tenant servers: `--realm` names your tenant - see the next section.

### Multi-tenant servers: the realm

On a [multi-tenant](MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)
filex two tenants may each have an `alex`, so a password sign-in says which
tenant it is for, the way the web sign-in form does:

- **At a tenant's own address** the address says it:
  `filex client login --url https://files.acme.example` needs nothing more.
- **At the platform's address** name the tenant with `--realm` (no `--realm`
  there is the platform's own accounts):

```bash
filex client login --url https://files.example.com --realm acme --email alex
Password: ********
Logged in as alex (realm acme) on https://files.acme.example
Signed in at https://files.example.com and handed over to the tenant's own address; later commands use it.
Token saved to /home/you/.filex/cli.yaml (0600)
```

- **A tenant with an address of its own** does not get its session at the
  platform's address: the server answers with a one-use **handoff** code for
  the tenant's address, and the CLI redeems it there
  (`POST /api/auth/handoff`), as the web page does. It saves *that* address, so
  `ls`, `upload`, `filex sync` and `filex mount` talk to the tenant's own
  address from then on. The code works once, on that address only, for 60
  seconds; when the tenant's address does not accept it (it is not reachable
  from where you are, say) the error names the address to sign in at
  directly. The CLI never sends the code to anything but an `http(s)` address,
  and never from an `https` address to an `http` one.
- A tenant with **no** address of its own is signed in to right at the
  platform's address.
- **The realm is saved** in `cli.yaml` with the address (`realm: acme`) and
  used again by the next `filex client login` at that address: pass `--realm`
  again only to change it, and `--realm ""` for the platform's own accounts
  (which also forgets the saved one). A `--url` or `FILEX_URL` that names
  another server never gets it.
- **A wrong realm is a wrong password.** A realm nobody has, or one that is not
  the address's tenant, is answered - and counted against the
  [sign-in limit](CONFIGURATION.md#sign-in-attempt-limits) - exactly like a
  wrong password, so the error cannot say which of the three was wrong; it
  names the realm among them. Nothing is saved.
- A **single-tenant** filex ignores `--realm`.
- An **API token** needs no realm: it names its account (`FILEX_TOKEN`). That
  is also why there is no `FILEX_REALM`: the realm belongs to a password
  sign-in, like the e-mail and the `--totp` code, and a script uses a token.

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
resume point survives the process - a bookmark under `~/.filex/uploads` records
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
- Remote folders are created as needed - **empty local folders included**.
  The destination's *parent* must already exist (same as `mkdir`).
- **Symlinks are skipped** with a warning on stderr; they are never
  followed, so link cycles can't loop the walk. **Named pipes, sockets and
  devices** are skipped with a warning too - opening a pipe nobody writes to
  would never return.
- A failed folder creation skips that subtree; a failed file is recorded
  and the walk continues. The summary lists every failure and the command
  exits **non-zero** when anything failed - safe for scripts.
- With `--json` the command prints one summary object instead:
  `{"local":…,"remote":…,"files":2,"dirs":3,"skipped_symlinks":[…],"skipped_special":[…],"errors":[…]}`.

### download

```bash
filex client download docs://reports/rapor.pdf            # ./rapor.pdf
filex client download docs://reports/rapor.pdf /tmp/      # into a directory
filex client download docs://reports/rapor.pdf -          # to stdout (pipe it)
```

An existing local file at the target is overwritten; a failed transfer never
leaves a partial file behind.

### mkdir / rm / mv / cp

```bash
filex client mkdir docs://reports/2027
filex client rm docs://reports/eski.pdf docs://tmp        # multiple args OK
filex client mv docs://inbox/a.pdf docs://reports/        # move into folder
filex client mv docs://inbox/a.pdf docs://inbox/b.pdf     # rename
filex client mv docs://inbox/a.pdf docs://reports/b.pdf   # move + rename, one step
filex client mv depo://docs/b.txt rbac://docs             # into another storage
filex client cp docs://reports/q3.xlsx archive://2026/    # copy, across storages too
filex client cp docs://a.txt docs://b.txt docs://shared/  # several sources: into a folder
```

```
Moved depo://docs/b.txt into rbac://docs
Copied docs://reports/q3.xlsx into archive://2026
```

- `rm` is a **soft delete** - items land in the server-side trash;
  [`trash restore`](#trash--versions) brings them back.
- `mv` and `cp` follow Unix semantics: the storage root, an existing folder or
  a target ending in `/` receives the item under its own name; otherwise the
  target is the item's full new path. With several sources the target must be
  an existing folder. Folders are moved and copied whole.
- **Source and target may be on different storages.** The server streams the
  bytes across (a move removes the source once they have arrived).
- Everything except a rename in place is a job of the server's operations
  queue (`POST /api/files/move` / `copy`), the same one the web explorer's
  paste uses. The command **waits** for the job and exits non-zero when it
  failed or only partly succeeded, with the server's reason. A move to another
  folder under another name is **one** step on the server (`name`), so it
  cannot stop half-way between a move and a rename. Ctrl-C stops the waiting,
  not the job.
- ⚠ **Nothing is ever replaced.**
  - A rename in place (`mv` within one folder of one storage) onto a taken
    name fails with `409 NAME_TAKEN` and nothing moves; a case-only rename
    still works.
  - A full target path that is taken is refused before anything is sent:
    `the target already exists: docs://reports/b.pdf - nothing was moved`.
  - An item moved or copied **into a folder** that already holds its name is
    put beside it under a free name (`a-copy.pdf`), as in the web explorer.
  - Unix `mv` and `cp` would overwrite; a script that relied on that must delete
    the target first, or pick a free name. (An **agent's** move, over MCP or
    `/api/ai/move`, also takes a free name beside a taken one.)
- With `--json` each source prints the operation's final row (one line per
  source); a rename prints the server's listing answer.

### trash / versions

```bash
filex client trash ls                       # newest first; --storage-id, --limit, --offset
filex client trash restore 41 42            # ids from `trash ls`
filex client versions ls docs://reports/q3.xlsx
filex client versions restore docs://reports/q3.xlsx 5   # a version id from `versions ls`
```

```
ID  DELETED           SIZE  BY   LOCATION
41  2026-09-30 13:00  5 B   you  docs://inbox/eski.txt

VERSION  ID  SIZE     RECORDED
2        5   10.2 KB  2026-09-30 13:00
```

- The trash lists the entries you may see: your own deletes, and on storages
  you can edit, other people's. `trash restore` puts an entry back where it
  was deleted from; a place that is taken again is refused (`409 EXISTS`) and
  the entry stays in the trash.
- A file's versions are addressed by the server's id for the file, which the
  CLI reads off its folder's listing - name the file. A file that is on the
  storage but not catalogued yet has no versions; `--id` takes the id when you
  have it. `versions restore` keeps the current content as a version first, so
  a restore can be undone the same way.

### tag

```bash
filex client tag ls                                   # every tag you can see
filex client tag ls docs://reports/q3.xlsx            # one file's tags
filex client tag add docs://reports/q3.xlsx acil      # a personal tag
filex client tag add docs://reports/q3.xlsx 2026 --team
filex client tag rm docs://reports/q3.xlsx acil       # either kind; --team / --personal narrows
filex client tag files 2026                           # the files carrying a tag
```

A **personal** tag is yours alone, like a star; a **team** tag is seen by
everyone in your organisation who can see the file, and adding or removing one
needs edit permission on it ([SEARCH.md](SEARCH.md)). `add` and `rm` change only
the names you give: the other tags on the file - and other people's personal
tags, which you never see - stay. Names compare without regard to case.
`search "report tag:2026"` filters a search by a tag.

### actions / run

```bash
filex client actions                                    # what the installed apps offer you
filex client run convert convert docs://data/table.csv --param target=xlsx
filex client run sign request docs://contracts/nda.pdf --param-json signers='["ada@example.com"]'
```

```
convert/convert: converted to Excel (.xlsx)
Wrote docs://data/table.xlsx
```

`run <app> <action> <files...>` runs an action an installed app offers in the
explorer's file menu ([APP-PLUGINS.md](APP-PLUGINS.md)) - Convert, signing, an
app of your own - on files of one storage, and waits for the job. `--param
key=value` passes a text value, `--param-json key=<json>` a number, a list or
an object. An action that asks for its input in a form in the explorer needs
its fields as parameters; run without any, it says so instead of running. The
command prints the files the job wrote and exits non-zero when it failed.

### archive

```bash
filex client archive create docs://out/2026.7z docs://reports/2026 --password-stdin < pw.txt
filex client archive create docs://out/logs.tar.gz docs://logs
filex client archive extract docs://in/paket.tar.gz                      # into its own folder
filex client archive extract docs://in/gizli.zip docs://acilan --password-stdin
```

The server packs and unpacks - nothing is downloaded. ZIP, 7z and the TAR
family, and passwords for ZIP and 7z, as far as the administrator allows them
(Settings -> Archives, [ARCHIVES.md](ARCHIVES.md)). The format comes from the
archive's name unless `--format` says otherwise; `--encrypt-names` encrypts a
7z's file names too, `--compression 1-9` sets the level, `--member` extracts
one entry (repeatable). An archive is extracted on its own storage.

The **password is read from standard input** (`--password-stdin`; without echo
when it is a terminal), never from the command line, where the shell's history
and the process list would keep it. An archive that needs one, or got a wrong
one, is refused with nothing extracted. A target archive that exists is
refused: nothing is overwritten.

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
space) are interchangeable, every word has to match - in any order, and a word
may be answered by a folder when the server has its search index (`main code`
finds `Code/main.go`; without the index every word has to be in the file's own
name) - a single typo is forgiven, and `tag:` / `-tag:` filter by tag. Results
arrive in rank order, exact filename matches first - quote a query that contains
spaces.

### share

```bash
filex client share docs://reports/rapor.pdf --pin --expires-days 7
filex client share ls                     # the links you created; --active, --limit, --offset
filex client share rm 12                  # revoke by id (from `share ls`)
```

```
URL:     https://fm.example.com/s/6a1b2c…
PIN:     96539559
Expires: 2026-07-24 13:44
```

Folders can be shared too - the public link serves them as a ZIP. The PIN is
generated server-side and shown **once**; `--expires-days 0` (default) means
no expiry.

`share ls` lists the links you created - public links and file requests
(`drop`), with their downloads and expiry - and `share rm` revokes them: the URL
stops working at once. A token confined to one folder lists only the links
inside it.

### Plugin requests

An API key cannot install a plugin: it **asks**, and an administrator signed
in to the admin panel approves or rejects the request
([APP-PLUGINS.md → Install requests](APP-PLUGINS.md#install-requests)). With
an admin-scoped key:

```bash
filex client plugins request --kind app --github-repo BRF-Tech/filex-sign --ref v0.1.1 \
  --reason "The legal team signs contracts in filex"
filex client plugins request --kind storage --name myfs --source acme/filex-myfs --reason "Archive storage"
filex client plugins request --kind app --upgrade --name sign --reason "Security fix"
filex client plugins requests                 # waiting ones; --status all for every state
```

```
Request #7: install app sign 0.1.1 - pending
Permissions: files:read, files:write, …
sha256:      4dddf289c4b0…
The request is waiting for an administrator's approval in the admin panel (Plugins → Install requests). …
```

`--reason` is required. Asking again for the same source answers the waiting
request (`Already requested #7`). There is no command to approve or reject.

## `filex mount` - the server as a folder

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
filex mount Z:      # ⚠ Z: must be FREE - the letter is created, not reused
```

Stop it by unmounting (`fusermount -u ~/filex`) or with Ctrl-C on Windows.

> ⚠⚠ **It is not a sync.** Nothing is copied to this machine except a bounded
> read cache, so a mount opens one file out of a hundred thousand without
> downloading the rest - and nothing is available when you are offline. For
> that, use [`filex sync`](SYNC.md).

> ⚠ **macOS is not supported.** It needs macFUSE, whose Go binding needs a C
> toolchain filex deliberately does not use and whose licence forbids a
> commercial program from installing it. The command refuses there rather than
> appearing to work and doing nothing.

| Flag | What it does |
|---|---|
| `--remote` | what to mount: empty for every storage, `docs://` for one, `docs://sub/dir` for a subtree |
| `--read-only` | refuse every write through this mount |
| `--block-size` | read granularity (default 4 MiB) - one HTTPS request per block |
| `--cache-blocks` | how many blocks to keep in memory (default 64) |
| `--attr-ttl` | how long a listing is trusted before it is re-fetched (default 5s) |
| `--spool-dir` | where in-flight writes are spooled |
| `--debug` | log every filesystem call |

A file written through the mount is uploaded when the program closes it, not
while it is being written - the REST API takes a whole object, and a partial
upload committed under the real name would replace a good file with a torn one.
Editing a very large file in place is therefore slower here than on a local
disk; copying it in and out is not.

Full protocol picture: [PROTOCOLS.md](PROTOCOLS.md).

## `filex decrypt` - an encrypted folder, offline

An [end-to-end encrypted folder](E2E-ENCRYPTION.md) is decrypted in the browser
while you use it. `filex decrypt` is the way out of filex: it takes the folder
as you downloaded it and writes a plain folder with the real file and folder
names - with **no server, no config and no network**. It is the tool to reach
for when you want your files back in the clear, when the server is gone, or to
check that a backup of an encrypted folder really opens.

```bash
filex decrypt ~/Downloads/Kasa.zip                  # → ~/Downloads/Kasa-decrypted/
filex decrypt ./Kasa -o ./Kasa-plain                # a folder copied off the storage
filex decrypt ./Kasa --recovery-key                 # lost the password
filex decrypt ~/Downloads/one-file --marker ./Kasa/.filex-e2e.json
pass show kasa | filex decrypt ./Kasa --password-stdin
filex decrypt ~/Downloads/Rapor.pdf.fxe             # → ~/Downloads/Rapor.pdf
filex decrypt ./encrypted-3fa2c1d0.fxe -o ./x.pdf   # a hidden-name file, to a name you pick
```

**What it takes.** The encrypted folder itself, a `.zip` of it (download the
folder from its parent in the web UI - the archive carries the folder's key
file, `.filex-e2e.json`), or a single encrypted file. A subfolder or a single
file downloaded on its own has no key file of its own: pass the encrypted
folder's with `--marker`.

**The password is never an argument.** It is asked for on the terminal without
echo; with `--password-stdin` (or when stdin is a pipe) it is read as one line.
There is no flag and no environment variable for it on purpose - both end up in
shell history, `ps` output and CI logs. `--recovery-key` asks for the recovery
key shown when the folder was created instead; case, spaces and dashes do not
matter.

**All or nothing.** The output is assembled in `<out>.partial-*` next to the
target and renamed into place only when every name and every file decrypted. A
wrong password, a wrong recovery key or one damaged file leaves **no output at
all** - a half-decrypted folder cannot be mistaken for a whole one. An existing
output directory is refused rather than merged into.

**What it reads.** Every folder format filex has written: folders from before
v0.31 (password only), folders with a recovery key, and folders whose file and
folder names are encrypted, including one that is half-way through being
switched to encrypted names. Large files (over 200 MB, the STREAM format,
header version `0x02`) are decrypted as a stream - memory stays flat whatever
their size - and a truncated, reordered or extended one is damage like any
other. A folder that needs a feature this build does not know is refused with
the feature named and exit status `7` - update filex.

### A single encrypted file (`.fxe`)

A file encrypted on its own from the web UI (*Encrypt with E2EE…*) carries its
own password slot and recovery key, so it needs no key file:

- **Output**: the file's ORIGINAL name, sealed inside it, next to the input -
  `Rapor.pdf.fxe` and `encrypted-3fa2c1d0.fxe` both come out as `Rapor.pdf`.
  With `-o` it goes to the path you name instead. Either must not exist yet:
  an existing file is never overwritten.
- **All or nothing**, as for a folder: the plaintext is written into a hidden
  `.<name>.partial-*` file next to the target and renamed into place only when
  its last chunk verified. A wrong password (exit `5`), a damaged, truncated or
  extended file (exit `6`) leave nothing behind.
- **One at a time.** Every `.fxe` has its own password, so a folder that
  merely holds `.fxe` files is not something one password opens; the command
  says so instead of looking for a key file. Loop over them in the shell:
  `for f in *.fxe; do filex decrypt "$f"; done`.
- A `.fxe` found **inside** an encrypted folder is copied into the output as
  it is, with a warning: its password is its own.

**What it tells you.** A summary line (files, folders, warnings) on stdout, and
one warning on stderr for everything that was not what an encrypted folder
should contain but is not damage either: a file that was never encrypted
(written into the folder over WebDAV, say) is copied as it is; a name that was
never encrypted is kept; a name that is not valid on this computer (`a:b.txt`
on Windows) or that clashes with another ignoring case is written under a safe
variant. `--quiet` drops the warnings, not the summary.

| Flag | What it does |
|---|---|
| `-o`, `--output` | output directory (default `<input>-decrypted`, `.zip` dropped); for a `.fxe`, the file to write (default: its original name next to it); must not exist |
| `--marker` | the encrypted folder's `.filex-e2e.json`, for an input that does not carry it |
| `--recovery-key` | unlock with the recovery key instead of the password |
| `--password-stdin` | read the password (or recovery key) as one line from stdin |
| `-q`, `--quiet` | print only the summary |

> ⚠ **The operator's escrow key is not accepted.** Escrow use in the web UI
> notifies the folder's owner before it opens anything; an offline tool cannot,
> so filex does not ship one. What escrow can and cannot promise is spelled out
> in [E2E-ENCRYPTION.md](E2E-ENCRYPTION.md#what-escrow-can-and-cannot-do).

> ⚠ The decrypted files are plaintext on your disk. They are written readable
> by you only (`0600`, folders `0700`), and nothing else about them is
> protected any more.

## `filex encrypt` - make a folder an encrypted folder

The command-line twin of *Encrypt with E2EE…*
([encrypting a folder you already have](E2E-ENCRYPTION.md#encrypting-a-folder-you-already-have)):
the same key file, the same file formats, a recovery key shown once. Everything
is encrypted **on this machine**; the server only ever receives ciphertext and
the key file. A folder it encrypts opens in the web app with the password, and
`filex decrypt` opens it offline.

```bash
filex encrypt ./Kasa                                 # → ./Kasa-encrypted/, to upload
filex encrypt ./Kasa --level 2 -o ./Kasa-up          # names encrypted too
filex encrypt docs://Kasa                            # on the server, where it is
filex encrypt docs://Kasa --level 2 --keep-versions
pass show kasa | filex encrypt docs://Kasa --password-stdin --recovery-key-file ./kasa.key
```

**A folder on this machine** is read and left as it is. The encrypted folder is
written next to it, `<folder>-encrypted` (or `-o`), with its key file
`.filex-e2e.json` at the root. Upload it - the web app, or
`filex client upload -r ./Kasa-encrypted docs://` - and it is an encrypted
folder there. It is written readable by you only (`0600`, folders `0700`): at
level 1 the names in it are the plaintext ones. Symbolic links are not followed (they are skipped, with a
warning); a folder that is already encrypted, or holds one, is refused, as in
the browser. Without a server there is no escrow key to seal to; pass the
installation's with `--escrow-public-key` (base64, or `@file`) for the same
folder the browser would make, or let the folder's owner add the slot later
([offering a slot](E2E-ENCRYPTION.md#offering-an-existing-folder-an-escrow-slot)).

**A folder on a server** (`adapter://path`) is encrypted where it is, in the
order the browser follows: no encrypted folder inside it, then - when the
server has [key escrow](E2E-ENCRYPTION.md#key-escrow-optional-operator-recovery) -
a notice that its operator holds a second key, then the password, then the key
file (with the conversion under way, `req: ["conv"]`), then the recovery key,
then every file. Each file is downloaded, encrypted as it arrives (the
plaintext never touches this disk; the ciphertext waits in a temporary file),
and sent back over itself as a conversion write: only if it is still the file
that was listed (`expect`), and with no plaintext version kept
(`e2e_convert`). At `--level 2` the names are encrypted after the contents. At
the end the key file drops `conv` and the server removes what it held from
before - thumbnails and search text always, older versions and trash entries
unless `--keep-versions` / `--keep-trash` (only the folder's owner or an
administrator can; anyone else gets a warning, and the folder is encrypted all
the same). The connection is the one `filex client` uses: `--url` / `--token`,
`FILEX_URL` / `FILEX_TOKEN`, or the session `filex client login` saved.

**Files over 200 MB** are written in the streamed format (STREAM, header
`0x02`), exactly as the browser writes them; memory never holds more than one
file up to 200 MB.

**Stopped half-way, it continues.** Ctrl-C, a dropped connection, a file that
changed meanwhile: run the same command again. It asks for the folder password
(or, with `--recovery-key`, the recovery key) and does only what is left - a
file already encrypted is left alone, on the server as in the browser. A local
run keeps what it wrote in `<out>.partial` (its key file first, so the same
password continues it) and renames it into place when it is whole. A server
folder whose conversion the **browser** started is continued the same way, and
one already encrypted is "nothing to do".

**The password is never an argument.** A new folder's password is asked twice
without echo (at least 8 characters, the web dialog's rule); `--password-stdin`
reads it as one line. Before the password comes the web dialog's warning: lose
both the password and the recovery key, and the files are gone. The **recovery
key** is shown once, as soon as the key file exists, and the command waits for
`yes` before it goes on (on a terminal); `--recovery-key-file` writes it to a
new file (`0600`, never overwritten) instead.

| Flag | What it does |
|---|---|
| `--level` | `1` contents only (the default; opens in filex 0.31 and later), `2` contents and names (0.48 and later) |
| `-o`, `--output` | local folder only: where to write (default `<folder>-encrypted`); must not exist |
| `--password-stdin` | read the password (or, continuing, the recovery key) as one line from stdin |
| `--recovery-key` | continuing a stopped run: unlock it with the recovery key |
| `--recovery-key-file` | write the new recovery key to this file instead of showing it |
| `--escrow-public-key` | local folder only: an escrow public key to seal the folder key to |
| `--keep-versions`, `--keep-trash` | server folder only: keep the older versions / trash entries |
| `--url`, `--token` | server folder only: as for `filex client` |
| `-q`, `--quiet` | print only warnings and the summary |

> ⚠ The folder name itself is never encrypted, at either level: an encrypted
> folder's own name is public, in the browser too.

## JSON output

Every command accepts `--json` and then prints the server's raw JSON response
(or a small result object for local operations like `download`) - ideal for
`jq` pipelines:

```bash
filex client ls docs://reports --json | jq -r '.files[].basename'
filex client share docs://x.pdf --json | jq -r '.share.url'
```

## Errors & exit codes

Errors go to **stderr** and the process exits **1**. A `401` appends a hint:

```
filex: HTTP 401: unauthorized - token missing/expired; run `filex client login`
```

`filex sync run` has two statuses of its own, for a supervisor that acts on
*why* it stopped ([Folder sync](SYNC.md#troubleshooting)):

| Status | Meaning |
|---|---|
| `3` | The server refused the token (HTTP 401): sign in again rather than retry. |
| `4` | At least one pair was skipped because another filex on this computer is syncing it; the other pairs ran. `--watch` never exits with it - it waits and takes the pair over. |

`filex decrypt` has three, so a script can tell a typo from a broken folder
([above](#filex-decrypt---an-encrypted-folder-offline)):

| Status | Meaning |
|---|---|
| `5` | Wrong password or recovery key. Nothing was written. |
| `6` | A damaged file or name (or a key file that is not one). Nothing was written. |
| `7` | The folder or `.fxe` needs a newer filex (an unknown required feature, or a format version this build does not know). |

`filex encrypt` uses the same numbers where they mean the same
([above](#filex-encrypt---make-a-folder-an-encrypted-folder)):

| Status | Meaning |
|---|---|
| `5` | Continuing a stopped run: wrong password or recovery key. Nothing was written. |
| `6` | Some files (or names) are not encrypted yet - changed meanwhile, not writable, or the run was stopped. What is done stays done; run the same command again. |
| `7` | The folder needs a newer filex. |
