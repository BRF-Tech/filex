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
| `expires_at` | Absolute expiry (RFC3339). |
| `max_downloads` | Auto‑expire after N downloads. |

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
