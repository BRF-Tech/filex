# Sharing & file requests

filex has two kinds of public link, both token‑based and account‑free for the
recipient:

- **Share links** (`/s/{token}`) — let someone **download** a file or folder.
- **File requests** (`/d/{token}`) — let someone **upload** files *into* a
  folder without ever seeing its contents ("file‑drop" / "Request files").

Both are created from the explorer's **Share** dialog on any item (a share
link needs ≥editor on the item); a file request can also be started for the
folder you are in from the navigation panel's **+ New → Request files**.

The same dialog carries **People with access** — the per-item grants — for the
item's **owner** only: an editor cannot read the grant list, so the section is
not offered to them (and the details panel's **Manage permissions** is an
owner's button). On a storage with RBAC switched off a grant changes nothing;
an administrator then sees the section greyed with where to switch RBAC on
(Admin → **Storages** → the storage → *Per-item access control*), and nobody
else is offered it.

An [app plugin](APP-PLUGINS.md) opens the third kind on the same machinery: the
link it sends an outside signer is an ordinary share carrying that app's
screen, so it appears in **Shares** with everything else and you revoke it the
same way.

**What the visitor sees is one shell**, whichever of the three they were sent:
your instance's name, logo, colours and footer (Admin → **Branding**, and the
default theme picked under **Appearance**), the PIN gate, the expiry, the visit
counter, the language picker and the wording for a link that is over. A
signature request from a renamed instance does not say "filex". Behind it the
plain server-rendered pages are kept for a browser with no JavaScript — a share
link is opened by strangers on whatever browser they have — and anything that
is not a browser navigating
(`curl -O`, wget, a backup script) still gets the **bytes**, not a page.

- [Share links (download)](#share-links-download)
- [File requests (upload / file-drop)](#file-requests-upload--file-drop)
- [Emailing a link](#emailing-a-link)
- [Failure modes & troubleshooting](#failure-modes--troubleshooting)

---

## Share links (download)

**Create.** Explorer → **Share**: the **Link sharing** switch at the top makes
the link, with the settings under **Link options** (PIN, expiry, download limit).
⚠ **One link per item from this dialog.** With a link already on, the button
under the options reads **Replace the link with these settings**: it revokes the
link and makes a new one — a new address, the old one stops working — rather
than leaving a second live link beside the first. (Until v0.43.0 it quietly
made a second one, and the header's link was also listed again underneath.) Or
`POST /api/files/share`:

```jsonc
{ "path": "s3://reports/q3.pdf",
  "password": true,          // generate an 8-digit PIN (returned in the response)
  "expires_at": "2026-08-01T00:00:00Z",
  "max_downloads": 50 }
```

The response includes the public URL (`https://files.example.com/s/<token>`) and,
if requested, the generated PIN. The PIN is not lost after that: the link's
creator and an administrator can read it back — see
[Your own links, and their PINs](#your-own-links-and-their-pins).

**Open** `/s/{token}`:
- **A file** streams as a download through filex. (An S3 storage with
  `disable_presign: false` answers with a redirect to a presigned bucket URL
  instead — off by default since v0.42.2, see [STORAGE.md](STORAGE.md#s3--s3-compatible).)
  `?inline=1` renders inline.
- **A folder** streams **every file under it as a ZIP** (internal folders like
  `.filex-trash` are skipped).
- **PIN‑protected** links show a PIN form first; a correct PIN unlocks the
  download for twelve hours (an HttpOnly cookie that carries no PIN and opens
  only that one link). The PIN can also be passed as `?pin=` or the
  `X-Filex-Pin` header.
  ⚠ **Five wrong answers shut the gate for ten minutes.** The count lives on
  the link itself, so it survives a restart and holds across two instances
  behind one address, and the *correct* PIN is refused while the lock is on —
  a lock the right answer lifts is no lock at all. A shut gate is not a dead
  link: it opens by itself. (Until this release the lock only ever guarded an
  app plugin's page; a PIN on a `/s/` link could be walked through at the
  speed of HTTP.)

**Options.**

| Option | Meaning |
|---|---|
| `password` | Generate a random 8-digit PIN, returned in the response and readable again later ([below](#your-own-links-and-their-pins)). |
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

Apps obey the same ceiling, and are told it: every call an app gets carries
`share_max_ttl_days`, read from the same setting the clamp reads, so an app's
screen can offer only what its links will keep (the e-Signature app's Time
step says "at most 7" on a 7-day server instead of offering 14 and quietly
getting 7).

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

`-L` matters: an S3 storage with presigned URLs turned on answers with a
redirect to the bucket, and without it curl saves the redirect instead of the file. For a folder link
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

**Revoke.** From **My shares** (your own links) or, for an administrator,
**Shares** (everybody's): the link's **Actions** menu → **Revoke**. Underneath,
`DELETE /api/files/share/{id}` (owner or admin; what **My shares** calls) and
`POST /api/admin/shares/{id}/revoke` (the admin page) soft‑revoke the link:
its expiry is set to now **and the revoke is recorded** (`shares.revoked_at`,
migration 00053), so both **My shares** and **Shares** say *Revoked* rather
than *Expired*. ⚠ Links revoked **before v0.43.0** carry no such record and
still read as expired. Either way the link shows a styled 404 page — and to a
visitor `expired`, `revoked` and a shut PIN gate are three different answers
([BACKEND.md](BACKEND.md)).

Revoking (or deleting) a link an **app** opened — a signing link, say — also
wakes that app within seconds, and the app finds the link gone and acts on it:
the e-Signature app closes the signature request the link belonged to, tells
the requester whose link it was, and releases the document. Nothing is sent to
the app; it is woken and asks (see APP-PLUGINS-API.md → `share_state`).

### Your own links, and their PINs

**My shares** lists the links you created, for everybody — not only for
administrators. In the web app it sits in the explorer's navigation panel
directly under **Shared with me** (`/drive/my-shares`), and each row's
**Actions** menu offers **Copy link**, **Copy PIN** and **Revoke**.
Administrators keep **Shares** for everybody's links, with the same **Copy
PIN** entry. The listing is `GET /api/shares`.

**A PIN can be read back.** A PIN is stored twice: as a bcrypt hash, which is
still the only thing the PIN gate checks, and sealed with AES-256-GCM under
`FILEX_SECRET_KEY`, so the link's creator — or an administrator — can copy it
again later (`GET /api/shares/{id}/pin`). Every read writes an audit row
(`share.pin_revealed`), the PIN goes to the clipboard and nowhere else, and an
`app` token cannot read one: a PIN is a credential, and an app token has no
person behind it. **Copy PIN** is not offered when there is nothing to show:

- the link has no PIN;
- the link was created before v0.43.0, or while the server had no key — only
  the hash was kept, so make a new link if you need a PIN you can see again;
- the server has no `FILEX_SECRET_KEY`, so there is nothing to seal a PIN
  with. The link works exactly the same; only reading its PIN back does not.

---

## File requests (upload / file-drop)

The inverse of a share link: a public page where anyone can **drop files into a
folder** — collecting documents, photos, submissions — without an account and
**without seeing what's already in the folder** ("blind drop"). The target
folder is resolved server‑side from the token; the uploader can never influence
the destination.

**Create.** On a **folder**, Explorer → **Share**, section **Request files** —
or **+ New → Request files** for the folder you are in — or `POST
/api/files/share` with `kind: "drop"`:

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

On the page a JavaScript browser gets:

- the limits (size, types, how many files are left) are stated **before**
  anything is picked, and the **name** field — when the link asks for one — sits
  above the drop area, because dropping sends;
- a file the link does not take (its type, its size, one file too many) is
  **not sent**: its row says why, and the rest of the drop still goes;
- everything one drop carries goes up in **one** request — one drop, one
  submission folder (it used to be one request, and one folder, per file);
- a refusal the server makes is shown in the server's own words (below).

**Limits & safety** (enforced server‑side): per‑submission file count and
per‑file size, an optional extension allowlist, an optional PIN, an expiry, a
lifetime `max_uploads` cap, and **per‑IP rate limiting** on the anonymous upload
endpoint. Read‑only storages reject drops.

**Language.** Every public page — the PIN gate, the uploader, the error pages
and the download-share pages — renders in ONE language per visitor. In the
shell that a JavaScript browser gets, that is the browser's own language, and
a **picker** in the header changes it (remembered in that browser, and nowhere
else: there is no account behind a share link). The plain server-rendered
pages resolve `?lang=` (`tr` / `en`), then `Accept-Language`, then the
server's `default_locale`; add `?lang=en` to a link you are sending to
somebody whose browser is set to neither.

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
- **An app's link says it is gone, although nobody revoked it** — the account
  that created it was switched off or deleted (re-enabling the account brings
  every one of its links back), or the app that answers it was stopped or
  removed. A link whose creator has since lost access to the document still
  opens, but the step that would start the app's work is refused and the
  visitor is asked to get a new link from the person who sent it.
- **"Request files" not offered** — you're on a file, not a folder (drop links
  are folder‑only), or you lack ≥editor on it.
- **Drop rejected** — hit `max_files`, `max_file_size_mb`, a disallowed
  extension, the per‑IP rate limit, or a read‑only storage. The page shows which:
  every refusal of `POST /d/{token}` and `POST /api/public/d/{token}/upload`
  carries the code a script branches on (`error`, e.g. `ext_not_allowed`) **and**
  the sentence a person reads (`message`, in the visitor's language).
- **"The file storage is unreachable right now"** — the link, the PIN and the
  files are all fine; the storage behind the folder refused the write. The
  endpoint answers **`503` `{"error":"storage_unavailable"}`** (not a 500) and
  logs the failure with the storage name and driver, so an outage is visible
  where outages are looked for. The link stays valid — the same upload works
  once the backend is back. Out of space is the neighbouring case: **`507`
  `{"error":"quota_exceeded"}`**, which is the *owner's* problem, not the
  uploader's.
- **Uploader sees folder contents?** — they don't; the drop page never lists the
  folder. If you want them to *see* files, use a download share instead.
- **Share link opens the wrong URL / host** — `FILEX_PUBLIC_URL` is wrong or
  unset. It's baked into every generated link; unset, every link says
  `http://localhost:5212`, and the admin panel shows a banner until it is set.
  The variable is spelled exactly `FILEX_PUBLIC_URL` — nothing else is read
  (see [Public URL](CONFIGURATION.md#public-url)).
- **Email not sent** — SMTP not configured/verified; the response is
  `{emailed:false}` and the UI still shows the link to share manually.

---

## See also

- [RBAC.md](RBAC.md) — who can create shares / access items
- [CONFIGURATION.md](CONFIGURATION.md) · [STORAGE.md](STORAGE.md)
