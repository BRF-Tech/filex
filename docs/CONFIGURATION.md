# Configuration

filex reads configuration in this order (highest precedence first):

1. **Environment variables** (`FILEX_*`)
2. **`config.yaml`** (path via `--config`, `FILEX_CONFIG`, or `~/.filex/config.yaml` if present)
3. **Built-in defaults**

For containers, environment variables are easiest. For rich setups (LDAP,
proxy‑header auth, custom CORS) a `config.yaml` is handier because a few settings
are **file‑only** (noted below). Individual storages are **not** configured here
— they're database records; see [STORAGE.md](STORAGE.md).

- [Install-time settings (`FILEX_INSTALLATION_*`)](#install-time-settings-filex_installation_)
- [Server & networking](#server--networking)
- [Logging](#logging)
- [Database](#database)
- [Authentication](#authentication)
- [Zero-touch seeding](#zero-touch-seeding)
- [External services](#external-services)
- [Storage sync](#storage-sync)
- [Thumbnails](#thumbnails)
- [Search](#search)
- [Queue](#queue)
- [Notifications](#notifications)
- [CORS](#cors)
- [Error reporting (Sentry/GlitchTip)](#error-reporting)
- [Demo mode](#demo-mode)
- [config.yaml](#configyaml)
- [Gotchas](#gotchas)

> **Booleans** are true only for `"1"` or (case‑insensitive) `"true"`. Any other
> non‑empty value is treated as false.

---

## Install-time settings (`FILEX_INSTALLATION_*`)

Settings with the **`FILEX_INSTALLATION_`** prefix are decided once, at the
first boot of an installation, and **cannot be changed afterwards**. filex
records what it was given in the `settings` table (key `installation.pinned`)
and mirrors it to `<data-dir>/installation.json`; on every later boot it
compares the two and **refuses to start** if they disagree, printing what
changed and what your options are.

That is not caution for its own sake. These settings change the *shape of data
already written*, so flipping one later does not reconfigure anything — it
produces an installation whose guarantees are true for some of its data and
false for the rest, with nothing on either half to say which. Refusing to start
is the only honest response.

| Env var | Default | Description |
|---|---|---|
| `FILEX_INSTALLATION_E2E_ESCROW_KEY` | *(unset — escrow off)* | Base64 SPKI **public** key (RSA ≥ 2048, PEM armour and whitespace tolerated) enabling [E2E key escrow](E2E-ENCRYPTION.md#key-escrow-optional-operator-recovery). Generate the pair with `filex e2e-escrow keygen`; put the public half here and keep the private half yourself. |

**Why escrow in particular cannot move.** An encrypted folder wraps its master
key once per recovery path *when the folder is created*. A folder made while
escrow was off carries no escrow-wrapped key, and nothing can add one without
the folder password — which the server never has. So:

- turning escrow **on** later gives you access to nothing that already exists;
- **changing** the key leaves old folders openable only by the old private key
  and new ones only by the new;
- turning it **off** does not un-escrow anything already created.

**If you meant to change it**, the supported paths are: restore the original
value, or start a new installation with a fresh data directory — keeping the
old escrow private key for as long as the folders created under it exist.

⚠ An unparseable value is **fatal**, not ignored. Running without escrow while
the operator believes they configured it is the one failure mode worth crashing
over.

⚠ The private half never reaches filex. `filex e2e-escrow keygen` writes
nothing and touches no database; it prints the pair and exits. That is what
makes a stolen filex database worthless to an attacker even with escrow on —
and what makes losing the private key unrecoverable.

---

## Server & networking

| Env var | Default | Description |
|---|---|---|
| `FILEX_LISTEN` | `0.0.0.0:5212` | Bind address. |
| `FILEX_PUBLIC_URL` | `http://localhost:5212` | **The external URL users open.** Baked into share links, the OIDC redirect and OnlyOffice fetch/callback — set it to your real `https://…` domain behind a proxy. |
| `FILEX_DATA_DIR` | `~/.filex` (`/data` in Docker) | Holds the SQLite DB, search index, thumbnail cache, first‑run secret. |
| `FILEX_DEFAULT_LOCALE` | — | Pin the initial UI language (`en` / `tr`) for users who haven't chosen one, overriding browser detection. A user's explicit language switch still wins. |
| `FILEX_MULTI_TENANT` | `false` | Turn on native multi-tenancy — one install serves N tenants, each a host-bound auth realm (provider) confined to its own storage(s). **Off = a normal single-tenant install, behaviour unchanged.** See [MULTI-TENANCY.md](./MULTI-TENANCY.md). |
| `FILEX_COOKIE_DOMAIN` | — (host-only) | `Domain` attribute for the `filex_session` cookie, e.g. `.example.com` — subdomains of that domain then share the session. Applied on **both** set and clear, so logout removes the same cookie it created. Empty = host-only cookie (unchanged behaviour). `Secure`/`SameSite`/`HttpOnly` are unaffected. **Multi-tenant:** this is only the last-resort fallback — the cookie Domain resolves per tenant: the provider's `cookie_domain` field wins, else it is derived from the provider host by dropping its first label (`files.example.com` → `.example.com`), else this global value. ⚠ A tenant served on its bare apex, or whose derivation would land on a public suffix (`tenant.com.tr` → `.com.tr`, which browsers reject), must set `cookie_domain` explicitly. See [MULTI-TENANCY.md](./MULTI-TENANCY.md). |
| `FILEX_CONFIG` | — | Path to `config.yaml` (same as `--config`). |

---

## Logging

| Env var | Default | Description |
|---|---|---|
| `FILEX_LOG_LEVEL` | `info` | `debug` · `info` · `warn` · `error` |
| `FILEX_LOG_FORMAT` | `text` | `text` · `json` |

---

## Database

| Env var | Default | Description |
|---|---|---|
| `FILEX_DB_DRIVER` | `sqlite` | `sqlite` · `postgres` · `mysql` |
| `FILEX_DB_DSN` | — | Connection string. Empty + sqlite → `<data_dir>/instance.sqlite`. |

DSN examples:
- postgres: `postgres://user:pass@host:5432/dbname?sslmode=require`
- mysql: `user:pass@tcp(host:3306)/dbname?parseTime=true&loc=UTC&charset=utf8mb4`

Migrations **run automatically on startup**; also `filex migrate up|down|status`.
SQLite (pure Go, CGO‑free) is a fine default; **PostgreSQL is recommended for
teams/HA**. MySQL is supported for read‑mostly use (a few upsert paths are
SQLite/Postgres‑only). See [database drivers](#database) note above.

---

## Authentication

Pick drivers with `FILEX_AUTH_DRIVERS` (comma list, tried in order, first match
wins). The **API‑token driver is always on** regardless.

| Env var | Default | Description |
|---|---|---|
| `FILEX_AUTH_DRIVERS` | `local` | e.g. `local,oidc`, `local,ldap`, `proxy_header` |

**OIDC / SSO** (see [SSO.md](SSO.md)):

| Env var (legacy `FILEX_AUTH_OIDC_*` also accepted) | Description |
|---|---|
| `FILEX_OIDC_ISSUER` | IdP issuer URL |
| `FILEX_OIDC_CLIENT_ID` | Client ID |
| `FILEX_OIDC_CLIENT_SECRET` | Client secret |
| `FILEX_OIDC_REDIRECT_URL` | `<public>/api/auth/oidc/callback` |
| `FILEX_OIDC_ROLE_CLAIM` | Claim carrying roles/groups |
| `FILEX_OIDC_ADMIN_GROUP` | Value that elevates to admin |
| `FILEX_OIDC_AUTO_REDIRECT` | **SSO-first login** (default `false`): the login page starts the OIDC flow immediately instead of showing the password form. Local login stays available behind a "Sign in with password" link (`/admin/login?local=1`) for break-glass/`admin@local`. The redirect is skipped on `?local=1`, after a failed IdP round-trip (`?error=oidc`) and on `?maintenance=1`, so a broken IdP can never cause a redirect loop. Requires `oidc` in `FILEX_AUTH_DRIVERS`. Multi-tenant: the flag is instance-global; the flow itself already dispatches per request host to the right tenant realm. |

**LDAP** (enable with `FILEX_AUTH_DRIVERS=local,ldap`):

| Env var | Description |
|---|---|
| `FILEX_LDAP_URL` | Directory URL, e.g. `ldaps://ldap.example.com` |
| `FILEX_LDAP_BIND_DN` | Service bind DN |
| `FILEX_LDAP_BIND_PASSWORD` | Service bind password |
| `FILEX_LDAP_BASE_DN` | Search base for users |
| `FILEX_LDAP_USER_FILTER` | User filter, e.g. `(mail=%s)` |
| `FILEX_LDAP_EMAIL_ATTR` | Attribute holding the email (e.g. `mail`) |
| `FILEX_LDAP_START_TLS` | `true` to upgrade a plain connection with StartTLS |
| `FILEX_LDAP_CA_FILE` | PEM bundle holding the CA that signed the directory certificate. Appended to the system trust store, not substituted for it. Needed for an internal/private CA on `ldaps://` **or** StartTLS. |
| `FILEX_LDAP_PROTOCOL_LOGIN` | `false` to stop directory accounts from signing in over WebDAV/SFTP/FTPS/S3/NFS with their directory password. Default `true`. |

> `FILEX_LDAP_USER_FILTER` may contain the placeholder more than once — every
> `%s` is filled with the same escaped identifier, so the usual AD filter that
> accepts either address form works as written:
> `(&(objectCategory=person)(objectClass=user)(|(mail=%s)(userPrincipalName=%s)))`

**Proxy‑header** — trust an authenticating reverse proxy (enable with
`FILEX_AUTH_DRIVERS=proxy_header`):

| Env var | Description |
|---|---|
| `FILEX_HEADER_EMAIL` | Header carrying the authenticated email (e.g. `X-Auth-Email`) |
| `FILEX_HEADER_GROUP` | Header carrying roles/groups (e.g. `X-Auth-Roles`) |
| `FILEX_HEADER_TRUSTED_IPS` | Comma list of proxy CIDRs allowed to set the headers |
| `FILEX_HEADER_ADMIN_GROUP` | Group value that elevates a user to admin |

> LDAP and proxy‑header can still be set under `auth.ldap.*` /
> `auth.header_proxy.*` in [config.yaml](#configyaml); the env vars above override
> those. See [SSO.md → other auth drivers](SSO.md#other-auth-drivers).

Local auth uses the `filex_session` cookie (12 h), bcrypt passwords, optional
TOTP 2FA. First boot creates `admin@local` (or seed a known admin — see
[Zero‑touch seeding](#zero-touch-seeding) and [INSTALLATION.md](INSTALLATION.md#first-run)).

---

## Zero-touch seeding

These variables **seed the database once, on first boot, only when the target
record is absent.** They let a fresh `docker compose up` / `helm install` come up
fully configured from env alone — no admin‑UI clicks. Once a record exists, later
operator edits in the UI **always win**; changing the env afterwards does **not**
re‑seed or overwrite. (OIDC/LDAP/header auth are read live from env every boot and
so are configured in [Authentication](#authentication), not here.)

**First admin** — created if the user table is empty:

| Env var | Default | Description |
|---|---|---|
| `FILEX_ADMIN_EMAIL` | `admin@local` | Email of the seeded admin account. |
| `FILEX_ADMIN_PASSWORD` | *(random, printed once)* | Password for that admin. Omit both to get a random `admin@local` (see [INSTALLATION.md → first run](INSTALLATION.md#first-run)). |

**SMTP** (mailer) — seeded when host, port and from are all set:

| Env var | Description |
|---|---|
| `FILEX_SMTP_HOST` | SMTP server host. |
| `FILEX_SMTP_PORT` | SMTP server port. |
| `FILEX_SMTP_USERNAME` | Auth username (optional). |
| `FILEX_SMTP_PASSWORD` | Auth password (optional). |
| `FILEX_SMTP_FROM` | From address on outbound mail. |
| `FILEX_SMTP_TLS` | `starttls` · `tls` · `none`. |

**Branding & trash:**

| Env var | Description |
|---|---|
| `FILEX_SITE_NAME` | Instance display name shown in the UI. |
| `FILEX_TRASH_RETENTION_DAYS` | Days to keep trashed items before purge (see [TRASH-VERSIONING.md](TRASH-VERSIONING.md)). |
| `FILEX_SHARE_MAX_TTL` | Longest life a **new** share link may be given — `7`, `7d` or `168h`; `0` = no ceiling. Seeds `share.max_ttl_days` (default 7) once; afterwards the admin **Protection** page owns it. Existing links are never changed (see [SHARING.md](SHARING.md)). |

**Default storage** — seeds one initial storage when **no storage exists yet**, so
a fresh install already has a working place for files. Leave
`FILEX_DEFAULT_STORAGE_DRIVER` empty to seed nothing. (See [STORAGE.md](STORAGE.md)
for the storage model.)

| Env var | Applies to | Description |
|---|---|---|
| `FILEX_DEFAULT_STORAGE_DRIVER` | both | `local` · `s3` (empty = seed no storage). |
| `FILEX_DEFAULT_STORAGE_NAME` | both | Display name / top‑level folder label. |
| `FILEX_DEFAULT_STORAGE_MOUNT` | both | Logical mount point (default `/`). |
| `FILEX_DEFAULT_STORAGE_PATH` | local | On‑disk directory to serve. |
| `FILEX_DEFAULT_STORAGE_S3_BUCKET` | s3 | Bucket name. |
| `FILEX_DEFAULT_STORAGE_S3_PREFIX` | s3 | Key prefix = storage root (keep non‑empty — root guard). |
| `FILEX_DEFAULT_STORAGE_S3_ENDPOINT` | s3 | Custom endpoint (MinIO/R2/Hetzner …); omit for AWS. |
| `FILEX_DEFAULT_STORAGE_S3_REGION` | s3 | e.g. `us-east-1`; `auto` for R2/MinIO. |
| `FILEX_DEFAULT_STORAGE_S3_ACCESS_KEY` | s3 | Access key. |
| `FILEX_DEFAULT_STORAGE_S3_SECRET_KEY` | s3 | Secret key. |
| `FILEX_DEFAULT_STORAGE_S3_PATH_STYLE` | s3 | `true` for path‑style addressing (MinIO/Hetzner/B2/R2). |

---

## External services

Each is optional — an empty URL disables it. Set via env or
`external_services.*`.

| Env var | Description |
|---|---|
| `FILEX_ONLYOFFICE_URL` | OnlyOffice Document Server URL (see [ONLYOFFICE.md](ONLYOFFICE.md)) |
| `FILEX_ONLYOFFICE_JWT` | Shared JWT secret — must match the Document Server |
| `FILEX_DRAWIO_URL` | Drawio embed URL (diagram editing) |
| `FILEX_CONVERT_URL` | External universal converter URL |

> **Mermaid needs no service.** Mermaid diagrams render entirely client‑side in
> the browser via a bundled `mermaid` library — there is nothing to deploy and no
> URL to set (the former `FILEX_MERMAID_URL` was removed).

---

## Protocol endpoints (S3 · SFTP · FTPS · NFS · WebDAV)

filex can be reached as five protocols besides HTTP. Full picture, including
which credential each one takes and the traps that cost real time:
[PROTOCOLS.md](./PROTOCOLS.md).

⚠ **S3 and `/dav` are ON by default; the other three are OFF.** The two that are
on do not open a port of their own and refuse every unsigned or unauthenticated
request, and a credential still has to be minted before anything can reach them.
The three that open a **listener** stay off until asked for — a port nobody
requested is not something to open for them.

| Env var | Default | Description |
|---|---|---|
| `FILEX_SECRET_KEY` | — | ⚠⚠ **Required once anybody mints an S3 access key.** SigV4 verifies a request by recomputing an HMAC chain from the secret, so unlike a token it cannot be hashed — filex seals it with AES-GCM under this key. With no key configured, minting an access key **fails** rather than storing plaintext. **Changing or losing it stops every existing access key from verifying**, so treat it like the database, not like a password: back it up, do not rotate it casually. Any 32+ random bytes. |
| `FILEX_S3` | `1` | The S3-compatible endpoint. Set `0` to switch it off. |
| `FILEX_S3_DOMAIN` | — | Dedicated host for the endpoint, e.g. `s3.example.com`, which also enables virtual-hosted addressing (`bucket.s3.example.com`). Empty leaves the endpoint under `/s3`, path-style only. ⚠⚠ **Never point this at the host the app itself serves** — the whole site then answers as S3. ⚠ Setting it needs a wildcard A record **and** a wildcard certificate for `*.<domain>`; without both, current SDKs (which default to virtual-hosted) fail at TLS with nothing that names the cause. |
| `FILEX_SFTP` | `0` | The SFTP endpoint. Its own TCP listener, not a route. |
| `FILEX_SFTP_ADDR` | `:2022` | Listen address. 2022 by convention — sftpgo and `rclone serve sftp` use it, while 2222 reads as "SSH in a container". |
| `FILEX_SFTP_HOST_KEY_DIR` | `<data>/ssh` | Where the server's host keys live. ⚠ **It must survive a rebuild**: regenerating host keys gives every user the "REMOTE HOST IDENTIFICATION HAS CHANGED" warning, which is indistinguishable from an attack. |
| `FILEX_SFTP_BANNER` | — | Text shown before authentication. |
| `FILEX_FTPS` | `0` | The FTPS endpoint. ⚠ Explicit TLS is **mandatory** on both channels and there is no switch to relax it: plain FTP sends the password in the clear and the file after it. |
| `FILEX_FTPS_ADDR` | `:2121` | Control channel. Port 21 needs root. |
| `FILEX_FTPS_PASV_MIN` / `_MAX` | `30000` / `30100` | The passive data-port range. ⚠⚠ **Open it on the firewall too** — a blocked range makes every transfer *hang* with no error on either side, which is the classic FTP failure and impossible to guess at from the client end. |
| `FILEX_FTPS_PUBLIC_HOST` | — | The address to advertise for passive connections, when it differs from what the server sees (NAT, Docker). A host name or an IPv4 address; a name is resolved at startup, because the PASV reply itself can only carry a dotted quad. ⚠ IPv6 has no PASV representation at all — a v6-only deployment must rely on EPSV, which needs no address and so needs no setting. |
| `FILEX_FTPS_CERT` / `_KEY` | — | TLS certificate. Absent, filex generates a self-signed one and the guide says so, so nobody has to discover it from a client warning. **Re-read when the files change** (mtime/size checked on every handshake), so a real, auto-renewing certificate — Caddy's, certbot's — can be mounted read-only and is picked up by the next connection with no restart. A renewal that lands half-written keeps the previous certificate serving and logs a warning. |
| `FILEX_FTPS_BANNER` | — | Greeting line shown on connect. |
| `FILEX_NFS` | `0` | The NFSv3 endpoint. ⚠⚠ **NFSv3 is unencrypted** — anyone who can read the traffic sees the files, and anyone who learns an export path can mount it. LAN or VPN only; for anything off-LAN the answer is `filex mount`. |
| `FILEX_NFS_ADDR` | `:2049` | Listen address. ⚠ There is no portmapper on 111, so clients must be given `port=` and `mountport=` explicitly — and Windows' "Client for NFS" cannot say that at all, so it only works on the standard 2049. |
| `FILEX_DAV` | `1` | The WebDAV endpoint at `/dav`. |

---

## Storage plugins

Drivers that live outside the binary — see [PLUGINS.md](PLUGINS.md).

| Variable | Default | Meaning |
|---|---|---|
| `FILEX_PLUGINS_DISABLED` | `0`, **`1` in demo mode** | Turns the whole subsystem off: nothing under `<data-dir>/plugins` is launched, no remote plugin is contacted, and the admin API answers 503 saying so. On by default, because a plugin is only ever installed by an admin — but an operator hardening a shared instance may not want the admin role to include “run a program on the server”. | ⚠⚠ **`FILEX_DEMO_MODE` flips this default to off.** A demo publishes an admin login — that is what a demo is — and this API is admin-only, so on a demo "admin-only" means anybody; installing a plugin runs an uploaded program on the host. Set `FILEX_PLUGINS_DISABLED=0` to override that deliberately.
| `FILEX_PLUGIN_CONFORMANCE` | `enforce` | `enforce` · `warn` · `off`. filex **probes every capability a plugin declares** — at install against the plugin's own throwaway area, and again when a storage on it is saved, against that real configuration. `enforce` refuses a plugin that fails its own claims and refuses to save a storage on it. `warn` registers it anyway and keeps the report — for somebody *writing* a plugin, never for a shared instance: the cost of a broken claim is paid by the user, who meets an operation the UI offered and reads the failure as filex being broken. `off` skips both gates. Anything unrecognised falls back to `enforce`. |
| `FILEX_PLUGIN_TRUSTED_KEYS` | — | Comma-separated ed25519 **public** keys (hex or standard base64) allowed to sign a plugin. Set any key and an unsigned or badly signed binary is refused at install *and* at upgrade, and the admin API reports `requires_signature: true` so the UI asks for the signature up front. Left empty, no signature is asked for and the recorded sha256 is all an install carries. See [PLUGINS.md → Signed plugins](PLUGINS.md#signed-plugins). |
| `FILEX_PLUGIN_MAX_INFLIGHT` | `10` | Concurrent operations allowed **per plugin**. A caller that waits 5 s for a slot is refused rather than queued, and counted as `outcome="busy"` in [the metrics](METRICS.md#storage-plugins) — a sizing signal, not a bug. Raise it for a fast local plugin, lower it to keep a slow remote one from occupying the server. `0` or nonsense keeps the default. |
| `FILEX_SECRET_KEY` | — | Also seals a **remote** plugin's bearer token. Without it, registering a remote plugin is refused rather than stored in plaintext (binary plugins get a token minted per start, which is never stored). |

Installed binaries live in `<data-dir>/plugins/<name>/`, and a plugin's socket
in `<data-dir>/plugins/<name>/run/` (mode 0600). In multi-tenant mode the admin
surface is supertenant-only.

What is **not** configurable from the environment, and is cheaper to read here
than to search for:

- **The waiting and deadline figures around the ceiling**: 5 seconds waiting for
  a slot, 60 seconds on metadata calls. Streaming reads and writes are
  deliberately exempt — a 20 GB upload is legitimately slow, and a deadline
  there turns a working transfer into a failed one.
- **The plugin log rate limit** (50 lines/s, burst 200). Dropped lines are
  reported once per window rather than silently lost.

> ⚠ Signature enforcement is **off until you set `FILEX_PLUGIN_TRUSTED_KEYS`**.
> Until then a plugin is accepted on the strength of its sha256, which proves
> only that the file has not changed since it arrived — never who it came from.

---

## Storage sync

> ⚠ **A single failed sync run is not an error.** A run that cannot list the
> backend (a 503/504 from an object store under load, say) is logged at INFO and
> the catalogue is refreshed on the next tick. Only **three consecutive**
> failures are reported as a warning — that is a storage genuinely not
> answering, not a hiccup. The message carries the streak, and recovery is
> logged too.

Fallback cadence for the [sync worker](STORAGE.md#sync), used by storages that
do not set their own `sync_interval_s`. A storage that does set one wins.

| Env var | Default | Description |
|---|---|---|
| `FILEX_SYNC_INTERVAL` | `15m` | Go duration (`30s`, `15m`, `1h`). Values under 5 s are treated as "unset". |

> ⚠ The variable is `FILEX_SYNC_INTERVAL`, **not** `FILEX_SYNC_DEFAULT_INTERVAL`.
> An unparseable value logs a warning at boot and keeps the default rather than
> failing silently.

> ⚠ **`FILEX_SYNC_WORKERS` was removed in v0.20.** It was documented here as
> "concurrent storage sync workers" and parsed into a config field that
> **nothing ever read** — there is no pool to size. The sync worker runs one
> goroutine per enabled storage and always has, so concurrency is the number of
> enabled storages and there is no knob to turn. Setting the variable now has
> no effect and produces no error; delete it from your environment.
>
> (`FILEX_SYNC_INTERVAL` was equally dead until v0.20 — parsed, then read by
> nobody, while the real fallback was a hardcoded `15m` that happened to match
> the documented default. It is wired up now.)

---

## Uploads (staged / resumable)

Large uploads land in filex's own staging area first and are transferred to the
storage backend by a background job, so they survive a dropped connection and
work on every driver. See [UPLOADS.md](UPLOADS.md).

| Env var | Default | Description |
|---|---|---|
| `FILEX_UPLOAD_STAGING_DIR` | `<data_dir>/uploads` | Where in‑flight upload parts live. |
| `FILEX_UPLOAD_CHUNK_SIZE` | `8388608` (8 MiB) | Default part size when the client does not request one. |
| `FILEX_UPLOAD_STAGING_TTL` | `24h` | Idle time before the sweeper removes an abandoned staging directory. |

> ⚠ The whole object passes through the staging directory — put it on a
> filesystem with room for the largest upload you expect. `begin` refuses when
> less than `size × 1.2` is free.

---

## Downloads from slow storage (prepared copies)

When a **big** file lives on a **slow** backend, filex fetches it to local disk
once, tells the user it is preparing (with a percentage), and then serves it —
and every later request — at local-disk speed, with full `Range` support. See
*Slow storage* in [STORAGE.md](STORAGE.md).

| Env var | Default | Description |
|---|---|---|
| `FILEX_CACHE` | `1` | Master switch. `0` disables prepared copies entirely. |
| `FILEX_CACHE_DIR` | `<data_dir>/cache` | Where prepared copies live. |
| `FILEX_CACHE_MIN_SIZE` | `67108864` (64 MiB) | Smallest file worth preparing. Below it, nothing is ever cached. |
| `FILEX_CACHE_MAX_BYTES` | `21474836480` (20 GiB) | **Global** ceiling on the cache directory, enforced with LRU eviction. |
| `FILEX_CACHE_SLOW_BPS` | `10485760` (10 MiB/s) | Measured throughput below which a storage counts as slow. |

A file is prepared only when it is **at least `MIN_SIZE`** *and* its storage is
slow — either flagged by you (`"slow": true` in the storage config) or measured
below `SLOW_BPS`. Small files, and files on storages that measure fast, are
served exactly as they were before: nothing is prepared and nobody waits.

> ⚠ The cap is not optional and cannot be set to "unlimited". When the cache is
> full of entries that are being read, a new file is simply **not** prepared and
> streams from the backend as before — filex will not exceed the ceiling to make
> room.

**The cache directory also holds folder-share ZIPs** (`<cache_dir>/sharezips`,
moved there from `<data_dir>/sharezips` on first start of v0.19.1+). They are
not covered by `FILEX_CACHE_MAX_BYTES`; they are bounded by their shares and by
two knobs of their own — an archive is deleted as soon as no active share can
serve it, and in any case after a week. See *Folder ZIPs are cached* in
[SHARING.md](SHARING.md).

| Env var | Default | Description |
|---|---|---|
| `FILEX_SHAREZIP_WARM_MAX_BYTES` | `2147483648` (2 GiB) | Largest folder (sum of file sizes) the background warmer pre-builds. Bigger folders are zipped on demand when a visitor clicks, never refused. `0` = no ceiling. |
| `FILEX_SHAREZIP_MAX_AGE` | `7d` | A cached archive older than this is swept even if its share is still live, and rebuilt when next needed. Go durations plus a `d` suffix. `0` = keep for the share's life. |

⚠ **Exclude `<data_dir>/cache` from your backups.** Everything under it is
regenerable, and a single folder-share archive can be tens of gigabytes.

---

## Thumbnails

| Env var | Default | Description |
|---|---|---|
| `FILEX_THUMBS_ENABLED` | `true` | Master switch. |
| `FILEX_THUMB_BACKFILL_ON_BOOT` | — | Set `once` to backfill missing thumbnails on startup. |

Kinds and their tool requirements (auto‑detected on `PATH`; the full Docker
image bundles them): images = built‑in; video/audio = `ffmpeg`; PDF = `gs` or
`pdftoppm`; office = `libreoffice`; SVG = `rsvg-convert`. Missing tool → that
kind gets a generic placeholder card. Cache dir + formats are `config.yaml`
only (`thumbs.cache_dir`, `thumbs.formats`). See [thumbnails.md](thumbnails.md).

---

## Search

| Env var | Default | Description |
|---|---|---|
| `FILEX_SEARCH_ENABLED` | `true` | Embedded Bleve full‑text index. |
| `FILEX_SEARCH_CONTENT` | `true` | Extract text from files into the index (content search). `0` stops enqueueing extraction; text already indexed keeps matching. |
| `FILEX_SEARCH_CONTENT_MAX` | `5242880` | Source files larger than this are never content‑extracted. |
| `FILEX_SEARCH_AUTO_REBUILD` | `true` | Rebuild the index in the background at startup when it was written by an older document schema. The replacement is built alongside the live index and swapped in, so search never goes dark. `0` leaves the index alone and reports `needs_rebuild` instead. |

Index path is `config.yaml` only (`search.index_path`, default
`<data_dir>/search.bleve`). See [SEARCH.md](SEARCH.md).

---

## Queue

| Env var | Default | Description |
|---|---|---|
| `FILEX_QUEUE_DRIVER` | `sqlite` | `sqlite` · `postgres` · `redis` |
| `FILEX_QUEUE_DSN` | — | `postgres://…` or `redis://…` (ignored for sqlite — shares the app DB) |
| `FILEX_QUEUE_WORKERS` | `4` | Worker pool size. |
| `FILEX_QUEUE_ENABLED` | `true` | Disable to run without the persistent queue. |

Use **redis** or **postgres** for multi‑node deployments (postgres uses
`SELECT … FOR UPDATE SKIP LOCKED`). sqlite is fine single‑node.

---

## Notifications

| Env var | Default | Description |
|---|---|---|
| `FILEX_NOTIFY_ENABLED` | `true` | In‑app bell + webhook. |
| `FILEX_WEBHOOK_URL` | — | Generic JSON POST per event (empty = in‑app only). |
| `FILEX_WEBHOOK_TOKEN` | — | Sent as `Authorization: Bearer` to the webhook. |

See [NOTIFICATIONS.md](NOTIFICATIONS.md).

---

## CORS

| Env var | Default | Description |
|---|---|---|
| `FILEX_CORS_ALLOWED_ORIGINS` | `*` | Comma list. Restrict when embedding the component from specific origins. |

`allowed_methods` / `allowed_headers` are `config.yaml` only. Default allowed
headers: `Authorization, Content-Type, X-Filex-Pin`. If you use API‑token root
confinement from a browser, add `X-Filex-Token` / `X-Filex-Root`.

---

## Error reporting

Optional Sentry‑wire reporting (works with self‑hosted GlitchTip). Empty DSN =
off.

| Env var | Default | Description |
|---|---|---|
| `FILEX_SENTRY_DSN` | — | Sentry/GlitchTip DSN. |
| `FILEX_SENTRY_ENVIRONMENT` | — | Tag events (e.g. `production`). |

**What is reported.** filex does not sprinkle capture calls through the code;
it forwards its own log. Every `ERROR` record and every `WARN` record that
carries an `err` attribute becomes one event, grouped by the log message
(`thumb generate failed`, `ops: step failed`, …). `INFO` never reaches the
tracker — a listener closed by a shutdown, for instance, is an `INFO` line.

**Log attributes travel with the event.** Everything the log line carries —
`path`, `err`, `driver`, `attempt`, `storage`, `node`, … plus `source`
(`file.go:line` of the call site) — is attached twice:

- as **tags** when the value is short (first line, up to 120 characters), so
  the issue list and the tag filter show *which* file, driver or error;
- in full under the **`log` context** of the event (an ffmpeg transcript in
  `err` is kept whole there, untruncated).

Values whose key names a credential (`token`, `password`, `secret`,
`authorization`, `cookie`, `credential`, `private`, `*_key`, `key`) are
replaced with `[filtered]` before either. Log sites do not log secrets on
purpose, but a wrapped HTTP error or a config dump can carry one, and the key
is the cheapest reliable signal; an S3 object key is caught by the same rule —
the `path` beside it says the same thing.

---

## Updates

Release awareness and — on installs that own their binary — self-upgrade.
Full behaviour, including everything that is checked before anything is applied
automatically, is in [UPDATES.md](./UPDATES.md).

| Env var | Default | Description |
|---|---|---|
| `FILEX_UPDATE_CHECK` | `1` | Master switch for the periodic check. `0` = no outbound request, ever. |
| `FILEX_UPDATE_POLICY` | `manual` | `off` · `manual` · `patch` · `minor` — how far filex may move on its own. The default announces only. |
| `AUTO_UPGRADE` | — | Shorthand for `FILEX_UPDATE_POLICY=patch` (z-moves apply themselves; y and x are announced). An explicit policy set afterwards wins. |
| `FILEX_UPDATE_CHANNEL` | `stable` | Release channel. |
| `FILEX_UPDATE_MANIFEST_URL` | `https://filex.sh/updates/stable.json` | Release index location — point it at your own mirror for air-gapped installs. |
| `FILEX_UPDATE_WINDOW` | — | Daily maintenance window for automatic upgrades, e.g. `03:00-05:00` (server local time). Empty = any time. |
| `FILEX_UPDATE_INTERVAL` | `24h` | Time between checks. Anything under `1h` is raised to `1h`. |
| `FILEX_UPDATE_PRE_COMMAND` | — | Shell command run immediately before a self-upgrade (database dump for postgres/mysql). **A non-zero exit aborts the upgrade.** sqlite is snapshotted by filex itself with `VACUUM INTO`. |
| `FILEX_INSTALL_MODE` | auto-detected | `binary` or `docker`, when detection is wrong for your setup. Container installs never self-apply — the image layer is immutable, so a replaced binary reverts at the next `up`. |

---

## Demo mode

| Env var | Default | Description |
|---|---|---|
| `FILEX_DEMO_MODE` | `false` | Renders an "Open the demo" CTA on the login page. |
| `FILEX_DEMO_USER` | `demo@demo.com` | Demo credentials the CTA submits. |
| `FILEX_DEMO_PASS` | `demo` | (Keep the DB user in sync.) |

---

## config.yaml

Every field is optional; pass with `--config /path/to/config.yaml`.

```yaml
listen: "0.0.0.0:5212"
public_url: "https://files.example.com"
data_dir: "/data"

log:   { level: info, format: text }
db:    { driver: sqlite, dsn: "" }

auth:
  drivers: [local, oidc]           # local | oidc | ldap | proxy_header
  oidc:
    issuer: https://id.example.com/realms/main
    client_id: filex
    client_secret: "…"
    redirect_url: https://files.example.com/api/auth/oidc/callback
    role_claim: realm_access.roles
    admin_group: filex-admin
  ldap:                            # also overridable via FILEX_LDAP_*
    url: ldaps://ldap.example.com
    bind_dn: "cn=svc,dc=example,dc=com"
    bind_password: "…"
    base_dn: "ou=people,dc=example,dc=com"
    user_filter: "(mail=%s)"
    email_attr: mail
    start_tls: false
    ca_file: ""                    # PEM bundle for a private CA (optional)
    protocol_login: true           # directory passwords on WebDAV/SFTP/FTPS/S3/NFS
  header_proxy:                    # trust an auth proxy — also FILEX_HEADER_*
    email_header: X-Auth-Email
    group_header: X-Auth-Roles
    trusted_ips: ["10.0.0.0/8"]
    admin_group: admin

external_services:
  onlyoffice: { url: https://office.example.com, jwt_secret: "…" }
  drawio:     { url: "" }        # mermaid renders client-side — no service
  convert:    { url: "" }

sync:   { default_interval: 15m, workers: 4 }
thumbs: { enabled: true, formats: [image, video, pdf, office], cache_dir: "" }
search: { enabled: true, index_path: "" }
cors:
  allowed_origins: ["*"]
  allowed_methods: [GET, POST, PUT, DELETE, PATCH, OPTIONS]
  allowed_headers: [Authorization, Content-Type, X-Filex-Pin]
queue:  { driver: sqlite, dsn: "", workers: 4, enabled: true }
notify: { enabled: true, webhook_url: "", webhook_token: "" }
demo:   { mode: false, user: demo@demo.com, pass: demo }
sentry: { dsn: "", environment: "" }

seed:                              # first-boot only-if-absent (see Zero-touch seeding)
  admin_email: ""
  admin_password: ""
  site_name: ""
  trash_retention_days: ""
  share_max_ttl_days: ""           # FILEX_SHARE_MAX_TTL — "7", "7d", "0" = no ceiling
  smtp:    { host: "", port: "", username: "", password: "", from: "", tls: starttls }
  storage: { driver: "", name: "", mount_path: "/", path: "",
             bucket: "", prefix: "", endpoint: "", region: "",
             access_key: "", secret_key: "", path_style: false }
```

Some settings (branding, default thumbnail policy) live in the database
`settings` table and are managed from the admin UI, not here.

---

## Gotchas

- `FILEX_SYNC_DEFAULT_INTERVAL` is **not** read — the correct var is
  `FILEX_SYNC_INTERVAL`.
- `FILEX_SYNC_WORKERS` is **not** read either, and was removed in v0.20: there
  is no worker pool to size. See [Storage sync](#storage-sync).
- `FILEX_DEFAULT_STORAGE_*` only takes effect on a **fresh** install (it seeds a
  default storage when none exists yet); it never edits or replaces an existing
  storage. See [Zero‑touch seeding](#zero-touch-seeding).
- Booleans accept only `"1"` / `"true"`; anything else is false.
- LDAP and proxy‑header now have env vars (`FILEX_LDAP_*` / `FILEX_HEADER_*`); the
  env value overrides the matching `config.yaml` field.
- `FILEX_INSTALLATION_*` settings are frozen after the first boot and make filex
  refuse to start if they change — that is deliberate, not a bug. See
  [Install-time settings](#install-time-settings-filex_installation_).
