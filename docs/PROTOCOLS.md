# Protocols — reaching filex without a browser

filex speaks a protocol in **two directions**. Whatever it can connect *to*, it can also
be reached *as*:

| Protocol | filex connects to it (storage driver) | filex is reachable as it (server) |
|---|---|---|
| Local disk | ✅ | — (it *is* the disk) |
| S3 | ✅ | ✅ **on by default** (`FILEX_S3=0` to switch off) |
| SFTP | ✅ | ✅ `FILEX_SFTP=1` — off by default |
| FTP / FTPS | ✅ | ✅ `FILEX_FTPS=1` — off by default, explicit TLS only |
| WebDAV | ✅ | ✅ `/dav`, **on by default** |
| NFSv3 | — (mount it on the host and use the local driver) | ✅ `FILEX_NFS=1` — off by default |
| SMB / CIFS | ✅ | ❌ — see [Why no SMB server](#why-there-is-no-smb-server) |
| HTTPS (`filex mount`) | — | ✅ always, no server-side switch |

A backend that is in **neither** column can still be mounted: a
[storage plugin](PLUGINS.md) is a separate program that teaches filex a backend
it does not ship, and a storage on one is served over every protocol in the
right-hand column exactly like a built-in — WebDAV, SFTP, FTPS, NFS and the S3
endpoint each have a test against a live plugin. ⚠ When such a plugin is down,
those surfaces answer an **error**, never an empty listing: a mirroring client
that saw an empty success would delete the user's local copy.

Everything below lands in the **same tree**, with the same RBAC grants, the same trash,
the same quota, the same search index and the same audit trail as the web UI. A file
uploaded over FTPS is thumbnailed, indexed and counted exactly like one dropped in the
browser, because every protocol writes through the same funnel.

**The connection instructions are in the app**, not here: *Connections → Connect*, in the
web UI and the desktop app, builds every command from *this* deployment — its host, its
port, your login, the key or export you just minted. This document is the map; that page
is the thing you copy from.

---

## The one thing to understand first: credentials

Each protocol has a credential you can revoke **on its own**, without touching your
password and without disturbing the others.

| Protocol | What you present | Where it is minted |
|---|---|---|
| S3 | access key id + secret (SigV4) | Connections → S3 |
| SFTP | your login + an **API token** as the password, or a registered **SSH public key** | Connections → SFTP |
| FTPS | your login + an **API token** as the password | Connections → FTPS |
| NFSv3 | **the export path itself** — it carries 32 bytes of entropy | Connections → NFS |
| WebDAV | your login + an **API token** as the password | any token |
| `filex mount` | `FILEX_URL` + an **API token** | Tokens |

Every credential in that table is a **person's** — mint them while signed in as
yourself, or from a `user` API token. An *app* token (a host app's proxy, a bot)
is refused by all four self-service credential surfaces (API tokens, S3 access
keys, SSH keys, NFS exports); its explorer does not show the **API keys** entry,
and "How to connect" keeps the guides but replaces the mint forms with a line
saying this session cannot create credentials. See
[MCP.md → Token kinds](MCP.md#token-kinds--user-vs-app).

Your login is your **username** if you have set one, otherwise your e-mail. Both work
everywhere; an `@` in an SSH or FTP login has to be quoted in most clients' config files,
which is what usernames are for.

Your **account password** is accepted too, wherever the table says "API token" — a token
is simply the credential you can revoke without changing your password. On an install with
[LDAP/AD](LDAP.md) that includes the **directory** password: the directory is asked when
the local users table cannot judge the password, and a successful check is cached for five
minutes because these protocols present the credential on every request. Operators can
switch that off with `auth.ldap.protocol_login: false`.

> ⚠ **An account with 2FA enabled cannot use its password on any of these.** None of
> these protocols has a channel for a second factor, so accepting the password would make
> each of them a documented 2FA bypass. Mint a token (or a key, or an access key) —
> that is the app-specific-password pattern, and each one is individually revocable.

> ⚠ **Revoking reaches sessions that are already open.** Deleting a token, disabling a
> key or revoking an export cuts the SFTP/FTPS connection it opened and stops the NFS
> mount within about half a minute — not only the *next* login.

---

## S3

`FILEX_S3=1`. A bucket **is** a storage: `main`, `photos`, `archive` are what
`ListBuckets` returns, filtered to the ones you can see.

- SigV4 verified against the SDK's own signer, so the clients that matter agree byte for
  byte. Tested against **aws-cli, rclone, restic, mc and s3fs**.
- `ListObjectsV2` **and V1**, delimiters and `CommonPrefixes`, real `Range` requests,
  composite ETags, `x-amz-meta-mtime`, multipart uploads on filex's own staging area,
  the modern `x-amz-checksum-*` contract (header *and* trailer), and directory markers so
  `mkdir` works over `s3fs`.
- Path-style and virtual-hosted addressing both work.

> ⚠ **Give the endpoint its own hostname** (`FILEX_S3_DOMAIN=s3.example.com`) if you can.
> Without one it lives under `/s3`, and a client pointed at the application root gets the
> web app's HTML — rclone reports *"XML syntax error on line 10"*, which says nothing
> about what is wrong. Never point `FILEX_S3_DOMAIN` at the host the app itself serves.

> ⚠ A bucket you cannot reach answers **NoSuchBucket**, never AccessDenied — the same
> thing S3 does cross-account, because the alternative is an existence oracle. A *write*
> you are not allowed answers **AccessDenied**, because a client told "no such key" would
> retry forever against a permission problem.

## SFTP

`FILEX_SFTP=1`. Its own TCP listener (default `:2022` — sftpgo and `rclone serve sftp`
both use it, while 2222 means "SSH in a container"), not a route.

- Password (an API token) **or** a registered public key. `ssh-copy-id` cannot work
  against filex — it appends to `~/.ssh/authorized_keys` over a shell and filex has no
  shell — so keys are pasted at *Connections → SFTP*.
- `exit-status` is sent on the subsystem channel, so **`scp` reports success correctly**.
  OpenSSH 9+ speaks SFTP for `scp`, and without this every copy ended in a silent
  `exit 1` with the bytes already transferred.
- posix-rename, `statvfs` (your quota is what `df` shows), permission bits synthesised
  from your ACL level, resumable reads and writes.
- Tested with **OpenSSH, sshfs, WinSCP, FileZilla, rclone**.

> ⚠ Only the `sftp` subsystem is served. `exec` and `shell` are refused: filex has no
> shell, and answering an exec request is how a file server grows a command-execution
> surface.

## FTPS

`FILEX_FTPS=1`. For the equipment that only ever learned FTP — scanners,
multifunction printers, older cameras, industrial controllers.

- **Explicit TLS is mandatory**, on the control channel *and* the data channel. There is
  no switch to turn it off: plain FTP sends the password in the clear and the file after
  it.
- **Passive mode only.** Active mode has the server dial the client, which does not
  survive NAT and turns the endpoint into something that makes outbound connections to an
  address the client chose.
- ASCII conversion is **disabled**. FTP's ASCII mode rewrites line endings, and on a file
  a client guessed wrong about that is silent corruption of somebody's data.
- `REST`/`APPE` resume, and a transfer that fails mid-flight does not leave a truncated
  file under the real name.

> ⚠ **The passive port range matters as much as the port.** A firewall that blocks it
> makes every transfer *hang* with no error on either side — the classic FTP failure,
> impossible to guess at from the client end. Set `FILEX_FTPS_PASV_MIN`/`_MAX` and open
> both.

**Certificates.** Without `FILEX_FTPS_CERT`/`_KEY` filex generates a self-signed
pair and the connection guide says so. With them, the files are **re-read
whenever they change** — every handshake checks their mtime and size — so a real,
auto-renewing certificate can be bound: mount your reverse proxy's certificate
directory read-only, point the two variables at the `.crt`/`.key`, and the
renewal Caddy or certbot writes every couple of months is what the next FTPS
client sees, with no restart. (Before v0.25 the pair was read once at start-up,
which is why a renewed certificate would have been served expired for weeks
while `/healthz` stayed green.) A renewal that lands half-written, or a key that
does not match its certificate, keeps the previous pair serving and logs a
warning once; a good pair written afterwards is picked up.

## NFSv3

`FILEX_NFS=1`, **off by default and meant for a LAN or a VPN**.

NFSv3 cannot authenticate a request in a way filex can use: real identity means
RPCSEC_GSS, which means Kerberos, and AUTH_SYS — what every NAS actually ships — is the
client asserting *"I am uid 1000"* with nothing to check it against.

So **the identity is bound to the export, not to the request**. Each export gets a path
with 32 bytes of entropy in it, and *the path is the credential*. The mount is pinned to
one account for its whole lifetime; the uid and gid on each request are discarded rather
than trusted, and the permissions you see are synthesised from your ACL.

> ⚠⚠ NFSv3 is **unencrypted**. Anyone who can read the traffic sees your files, and
> anyone who learns the path can mount them. LAN or VPN only — for anything off-LAN the
> answer is `filex mount`.

> ⚠ There is **no portmapper** on port 111, so every client must be told the port
> explicitly: `-o port=2049,mountport=2049,nfsvers=3,nolock`. Windows' "Client for NFS"
> has no way to say that at all, so it only works when filex is on the standard 2049.

## WebDAV

`/dav`, on by default. The oldest surface, and the one Windows Explorer and macOS Finder
map natively.

- Class-2 locking, which those clients require before they will write. **The locks are
  durable**: they are written to `<data>/dav/dav-locks.json` and read back at boot with
  their tokens intact, so a restart or a deploy does not silently drop a lock a client
  still believes it holds.
- Quota enforced with a proper **507 Insufficient Storage** before the upload starts.

> ⚠ Locks are per-process, not distributed. One filex process per deployment is the
> assumption; two processes serving the same storage would each grant locks the other does
> not know about.

## `filex mount`

No server-side switch — it is the same binary as the CLI, talking to the REST API over
ordinary HTTPS.

```bash
export FILEX_URL=https://filex.example.com
export FILEX_TOKEN=<token>
mkdir -p ~/filex && filex mount ~/filex
fusermount -u ~/filex        # ⚠ unmount this way, not by killing the process
```

It is the only option here that works from anywhere: no LAN, no extra port, no
third-party client, and it reaches the server through whatever proxy or tunnel the
browser goes through.

**It is not a sync.** Nothing is copied to the machine except a bounded read cache, so it
opens one file out of a hundred thousand without downloading the rest. If you want the
files when you are offline, use folder sync ([SYNC.md](SYNC.md)) instead.

On **Windows** the mountpoint is usually a drive letter, and WinFsp
([winfsp.dev](https://winfsp.dev), free) has to be installed once:

```powershell
$env:FILEX_URL   = "https://filex.example.com"
$env:FILEX_TOKEN = "<token>"
filex mount Z:            # ⚠ Z: must be FREE — it is created, not reused
```

Stop it with Ctrl-C in that window.

> ⚠ **macOS is not supported.** It needs macFUSE, whose Go binding needs a C
> toolchain filex deliberately does not use and whose licence forbids a commercial
> program from installing it. The command refuses there rather than appearing to work
> and doing nothing — use the desktop app with folder sync instead.

---

## Connecting *to* an SMB / NAS share

SMB is the one asymmetric row: filex can use a NAS as a storage, but does not serve SMB.

Add a storage with driver **SMB / CIFS**, give it the host, the share name alone
(`media`, not `\\nas\media`), an account and optionally a sub-folder. Everything else —
RBAC, trash, versions, quota, search, thumbnails — behaves as it does on any other
storage.

> ⚠ A NAS is usually the slowest storage in an install. Pair it with the download cache
> and the staged-upload path ([STORAGE.md](STORAGE.md)).

### Why there is no SMB server

Not a licence problem and not a protocol problem — a size problem, stated plainly:

- There is **no MIT/Apache-licensed, mature Go SMB *server***. The one that genuinely
  works (`macos-fuse-t/go-smb2`) is AGPL-3.0, which would relicense filex; the MIT one
  (`gentlemanautomaton/smb`) says *"not yet suitable for use"* in its own README.
- Writing it means MS-SMB2 from NEGOTIATE upward: NTLMSSP session setup, TREE_CONNECT,
  CREATE with its create-contexts, READ/WRITE, IOCTL, locks, CHANGE_NOTIFY, oplocks and
  leases, durable handles, signing and encryption. Windows does not consider a share
  "working" until most of that is there. It is legal (MS-SMB2 is an open specification)
  and it is the single largest item on filex's board.
- And the thing people actually want from it — a drive letter — is already answered by
  `filex mount` off-LAN and by NFSv3 on-LAN, at a fraction of the cost.

If it is ever written, note that port 445 is not carried across the public internet by
most ISPs; the way out is **SMB over QUIC** (TLS 1.3 on UDP/443, ALPN token `smb`), which
needs Windows 11 24H2+ / Server 2025 on the client side, or Samba 4.23+ on Linux.

---

## Turning them on

```bash
# S3 and WebDAV are ON already; these switch them OFF.
FILEX_S3=0
FILEX_DAV=0

FILEX_S3_DOMAIN=s3.example.com  # ⚠ never the app's own host

FILEX_SFTP=1
FILEX_SFTP_ADDR=:2022           # the default

FILEX_FTPS=1
FILEX_FTPS_ADDR=:2121           # the default; 21 needs root
FILEX_FTPS_PASV_MIN=30000
FILEX_FTPS_PASV_MAX=30100       # ⚠ open this range on the firewall too

FILEX_NFS=1                     # ⚠ LAN / VPN only
FILEX_NFS_ADDR=:2049            # the default; Windows can only use 2049

FILEX_SECRET_KEY=<32+ random bytes>   # ⚠ required once S3 keys exist
```

> S3 and `/dav` are on because neither opens a port of its own and both refuse every
> unsigned or unauthenticated request; a credential still has to be minted before
> anything can reach them. The three that open a **listener** are off until asked for — a
> port nobody requested is not something to open for them.

> ⚠⚠ **`FILEX_SECRET_KEY` is a deploy requirement, not an option.** S3 secrets cannot be
> hashed the way tokens are — SigV4 verifies a request by recomputing an HMAC chain from
> the secret, so the server must be able to recover it. It is sealed with AES-GCM under
> this key. Without the key configured, minting an access key **fails** rather than
> storing plaintext; **change or lose the key and every existing access key stops
> verifying.** Back it up with your database.

Full variable reference: [CONFIGURATION.md](CONFIGURATION.md).
