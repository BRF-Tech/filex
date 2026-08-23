# Sharing & file requests

filex has two kinds of public link, both token‑based and account‑free for the
recipient:

- **Share links** (`/s/{token}`) — let someone **download** a file or folder.
- **File requests** (`/d/{token}`) — let someone **upload** files *into* a
  folder without ever seeing its contents ("file‑drop" / "Dosya İste").

Both are created from the explorer's **Share / Permissions** dialog on any item
(a share link needs ≥editor on the item).

- [Share links (download)](#share-links-download)
- [File requests (upload / file-drop)](#file-requests-upload--file-drop)
- [Emailing a link](#emailing-a-link)
- [Failure modes & troubleshooting](#failure-modes--troubleshooting)

---

## Share links (download)

**Create.** Explorer → **Share / Permissions → Link**, or
`POST /api/files/share`:

```jsonc
{ "path": "s3://reports/q3.pdf",
  "password": true,          // generate an 8-digit PIN (returned once)
  "expires_at": "2026-08-01T00:00:00Z",
  "max_downloads": 50 }
```

The response includes the public URL (`https://files.example.com/s/<token>`) and,
if requested, the one‑time PIN.

**Open** `/s/{token}`:
- **A file** streams as a download (presigned redirect where the storage
  supports it, otherwise streamed by filex). `?inline=1` renders inline.
- **A folder** streams **every file under it as a ZIP** (internal folders like
  `.filex-trash` are skipped).
- **PIN‑protected** links show a PIN form first; a correct PIN unlocks the
  download. The PIN can also be passed as `?pin=` or the `X-Filex-Pin` header.

**Options.**

| Option | Meaning |
|---|---|
| `password` | Generate a random PIN (shown once). |
| `expires_at` | Absolute expiry (RFC3339). Capped by the server's **maximum link life** (below). |
| `max_downloads` | Auto‑expire after N downloads. |

**Every new link has a maximum life.** The admin sets it under **Protection →
Share links** (`share.max_ttl_days`, default **7 days**, `0` = no ceiling;
seeded once from `FILEX_SHARE_MAX_TTL`). A link created without `expires_at`
gets `now + max`; one asking for more is shortened to it. The response says so
— `expires_at` is the date actually stored and `expiry_clamped: true` marks a
request the server changed — and the dialogs only offer choices the server will
keep (a 7-day server shows *1 day / 7 days*, not *30 days* or *Never*), with the
real expiry printed under the fresh link.

⚠ **Links that already exist are never touched.** Lowering the ceiling changes
what new links get, not what old ones have: a customer's link minted last month
keeps its own expiry (or none). What the server does instead is *count* them —
`GET /api/admin/protection` returns `shares_over_max_ttl`, the Protection page
shows the number, and the boot log prints it — so whoever lowered the limit can
revoke any of them by hand under **Shares**, or leave them alone.

**The download cap is exact.** A download is claimed against the cap *before*
the bytes are served, so "3 downloads" hands out three files even when several
people click at once, or when a large transfer is still running as the next one
starts. (It used to be counted afterwards, so every request that began inside
that window read the same pre-download count and was waved through: measured on
a live instance, a link capped at ONE download served three complete files to
three overlapping clients.) A serve that fails before a single byte leaves
gives its slot back; a transfer the visitor abandons half-way has spent one.

Each real byte-serve counts once: the file itself, a folder's "download all"
ZIP, and a single file fetched from a shared folder's browse page. The gallery
thumbnails on that page, the ZIP progress poll and the "preparing…" page do not.

**Command line.** The dialog also shows a one-line `curl` for the finished link
— a share is often made *for a server*, and that reader has no browser:

```bash
curl -fSL -o 'q3.pdf' 'https://files.example.com/s/<token>?pin=12345678'
```

`-L` matters: an S3-backed instance answers with a redirect to a presigned URL,
and without it curl saves the redirect instead of the file. For a folder link
the command targets `?zip=wait`, which blocks until the archive is built and
then streams it.

**Folder ZIPs are cached, and the cache is disposable.** A shared folder's
archive is built once and kept at `<cache_dir>/sharezips/<node>-<signature>.zip`,
so the second visitor does not pay for the walk again. A background warmer
pre-builds it when the link is created and re-checks every active folder share
every 5 minutes; the signature covers the file set, sizes and mtimes, so editing
the folder invalidates the archive and the next pass rebuilds it. While a build
is running, `/s/{token}` shows a "preparing… %" page (`?zip=status` polls,
`?zip=wait` blocks); nothing about that is counted as a download.

Four rules keep that cache from becoming a disk problem:

- **Nothing outlives its share.** Each warmer pass deletes every archive whose
  node no longer has an active folder share — expired, revoked, or out of
  downloads — plus the leftovers of builds that died with a restart. An archive
  is regenerable, so a link that cannot be used has no archive.
- **A build stops when its share does.** A build that is still running when its
  share expires abandons itself within about a minute and deletes its partial
  file. (A 16.7 GB folder shared for eleven minutes once kept reading from S3
  for three hours after the link had died, then left a 15 GB archive nobody ever
  downloaded.)
- **The warmer has a ceiling; the download button does not.** Folders whose
  files add up to more than `FILEX_SHAREZIP_WARM_MAX_BYTES` (default **2 GiB**,
  `0` = no ceiling) are *not* pre-built — not when the link is created, not on
  the five-minute pass. They are zipped the moment a visitor clicks download,
  with the same "preparing… %" page as before, and cached from then on like any
  other. Nothing is refused for being large; the server just does not spend
  hours of object-storage reads on a link nobody may ever open. The warmer logs
  each such folder once.
- **No archive older than a week.** Whatever its share's state, a cached ZIP
  older than `FILEX_SHAREZIP_MAX_AGE` (default **7d**, `0` = keep for the
  share's life) is swept and rebuilt on demand — or by the next warm pass, if
  the folder is under the ceiling. Together with the 7-day default link life
  this bounds the cache to what is actually being shared this week.

⚠ **Operators: exclude the cache directory from backups.** `<data_dir>/cache`
(prepared copies *and* folder-share ZIPs) is regenerable by definition; backing
it up puts throwaway gigabytes into your snapshots, your off-site copy and every
restore. Older installs kept the ZIPs in `<data_dir>/sharezips` — filex moves
that directory into the cache directory on first start, so exclude
`<data_dir>/cache` and, for a while, `**/sharezips/**` too.

**Metadata** (no PIN needed): `GET /api/files/share/{token}` →
`requires_pin, expires_at, download_count, max_downloads, downloads_remaining,
filename, size, mime, is_directory`.

**Revoke.** `DELETE /api/files/share/{id}` (owner or admin) soft‑revokes the
link (sets expiry to now, keeps the audit trail). Expired links show a styled
404 page.

---

## File requests (upload / file-drop)

The inverse of a share link: a public page where anyone can **drop files into a
folder** — collecting documents, photos, submissions — without an account and
**without seeing what's already in the folder** ("blind drop"). The target
folder is resolved server‑side from the token; the uploader can never influence
the destination.

**Create.** On a **folder**, Explorer → **Share / Permissions → Request files**,
or `POST /api/files/share` with `kind: "drop"`:

```jsonc
{ "path": "s3://inbox",
  "kind": "drop",
  "password": true,                    // optional PIN
  "expires_at": "2026-08-01T00:00:00Z",
  "drop_settings": {
    "max_files": 10,                   // per submission (default 20)
    "max_file_size_mb": 200,           // per file (default 500)
    "allowed_ext": ["pdf", "jpg"],     // empty = all types
    "ask_name": true                   // optional uploader name field
  },
  "max_uploads": 100 }                 // lifetime cap on total files received
```

You get a `https://files.example.com/d/<token>` link.

**How a drop works.** The visitor opens `/d/{token}`, optionally enters a PIN, an
optional name + note, and drops files. Each submission lands in its **own
subfolder** named `YYYY-MM-DD_HHMMSS_<name|anon>` (so submissions never collide
and you can see who sent what); an optional note is saved as `NOT.txt` beside
the files. The owner is notified (in‑app + email, best‑effort).

**Limits & safety** (enforced server‑side): per‑submission file count and
per‑file size, an optional extension allowlist, an optional PIN, an expiry, a
lifetime `max_uploads` cap, and **per‑IP rate limiting** on the anonymous upload
endpoint. Read‑only storages reject drops.

**Options.**

| `drop_settings` key | Default | Meaning |
|---|---|---|
| `max_files` | `20` | Max files per submission. |
| `max_file_size_mb` | `500` | Max size per file. |
| `allowed_ext` | all | Allowlist of extensions (e.g. `["pdf","png"]`). |
| `ask_name` | `true` | Show an optional "your name" field. |
| (share) `max_uploads` | — | Cap on total files the link may ever receive. |
| (share) `password` / `expires_at` | — | PIN / expiry, as for download links. |

---

## Emailing a link

After creating a link you can email it to **one or many** recipients:
`POST /api/files/permissions/share-mail` (editor‑gated) with `email` and/or
`emails: [...]` (comma/space/newline‑separated addresses are also split). For a
drop link (`mode: "drop"`), the invite spells out the folder + the configured
limits. Returns `{emailed, sent[], failed[]}`. If SMTP isn't configured the UI
keeps showing the link so you can copy it manually. (SMTP is configured in the
admin settings.)

---

## Failure modes & troubleshooting

- **Link shows a 404 page** — expired, past its download/upload cap, or revoked.
- **"Request files" not offered** — you're on a file, not a folder (drop links
  are folder‑only), or you lack ≥editor on it.
- **Drop rejected** — hit `max_files`, `max_file_size_mb`, a disallowed
  extension, the per‑IP rate limit, or a read‑only storage. The page shows which.
- **Uploader sees folder contents?** — they don't; the drop page never lists the
  folder. If you want them to *see* files, use a download share instead.
- **Share link opens the wrong URL / host** — `FILEX_PUBLIC_URL` is wrong. It's
  baked into every generated link (see [CONFIGURATION.md](CONFIGURATION.md)).
- **Email not sent** — SMTP not configured/verified; the response is
  `{emailed:false}` and the UI still shows the link to share manually.

---

## See also

- [RBAC.md](RBAC.md) — who can create shares / access items
- [CONFIGURATION.md](CONFIGURATION.md) · [STORAGE.md](STORAGE.md)
