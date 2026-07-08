# ShareX uploader

Push screenshots, images, files, and text captures from
[ShareX](https://getsharex.com/) (the Windows screenshot & upload tool) straight
into filex and get back a **public, browser‑viewable link** in one step.

filex exposes a single token‑authenticated endpoint —
`POST /api/sharex/upload` — that stores the capture, indexes it, mints a public
[`/s/{token}` share link](SHARING.md#share-links-download), and returns it as
JSON. Ready‑to‑import ShareX configs live in [`docs/sharex/`](sharex/):

| File | ShareX destination | Use |
|------|--------------------|-----|
| [`image.sxcu`](sharex/image.sxcu) | Image uploader | screenshots / captured images |
| [`file.sxcu`](sharex/file.sxcu)   | File uploader  | any file (drag‑drop, clipboard, "Upload file") |
| [`text.sxcu`](sharex/text.sxcu)   | Text uploader  | text/code snippets (ShareX sends these as a file) |

- [1. Create a token in filex](#1-create-a-token-in-filex)
- [2. Import the configs into ShareX](#2-import-the-configs-into-sharex)
- [3. How the returned link behaves](#3-how-the-returned-link-behaves)
- [Endpoint reference](#endpoint-reference)
- [Optional: choose a target folder](#optional-choose-a-target-folder)
- [Troubleshooting](#troubleshooting)

---

## 1. Create a token in filex

The endpoint authenticates with a filex API token (the same kind AI agents and
the MCP server use).

1. Open the filex admin UI and go to **API / MCP** (left sidebar → *Access* →
   **API / MCP**, at `/admin/api-mcp`).
2. Click **New token**, give it a label (e.g. `ShareX`), and select the
   **`write`** scope. That is the only scope the uploader needs — `write` covers
   both storing the file and minting its share link. (Leaving *all* scopes
   unchecked also works: an empty scope set grants everything, but a
   `write`‑only token is the least‑privilege choice.)
3. Optionally bind the token to a **root folder** (confinement) so every ShareX
   upload is restricted to that subtree.
4. Copy the **plaintext token** — it is shown **once**. Only its hash is stored;
   if you lose it you must issue a new one.

---

## 2. Import the configs into ShareX

For each of the three `.sxcu` files:

1. In ShareX: **Destinations → Custom uploader settings…**
2. Click **Import → From file…** and pick the `.sxcu`
   (double‑clicking a `.sxcu` in Explorer also imports it).
3. Select the imported uploader on the left, then in **Headers** replace
   `YOUR_TOKEN_HERE` with the token you copied in step 1. (The value belongs to
   the `X-Filex-Token` header — leave the header name unchanged.)
4. Click **Test** to confirm you get a URL back.

Then point ShareX at these uploaders — **Destinations** menu:

- **Image uploader** → *filex (image)*
- **File uploader** → *filex (file)*
- **Text uploader** → *filex (text)*

Now the usual capture hotkeys (e.g. **Ctrl+PrtSc** for a region grab) upload to
filex and copy the link to your clipboard.

> **Self‑hosted host name.** The bundled configs post to
> `https://fm.brf.sh/api/sharex/upload`. If your filex lives elsewhere, edit the
> **Request URL** to `https://<your-filex-host>/api/sharex/upload`.

---

## 3. How the returned link behaves

The endpoint replies with:

```json
{ "url": "https://fm.brf.sh/s/AbC123?inline=1" }
```

ShareX parses `url` from the response (`URL` field = `{json:url}`) and gives you
that link.

- It is a normal filex **share link** (`/s/{token}`) — public and
  account‑free for whoever opens it.
- The **`?inline=1`** suffix makes the file render **in the browser**
  (`Content-Disposition: inline`) instead of forcing a download — so pasted
  screenshots and text snippets just *show*. (Drop the suffix, or use the
  Share/Permissions dialog, if you'd rather force a download.)
- Uploads land in a **`sharex/` folder** at the token's root by default. Each
  capture is stored under a random‑prefixed filename, so every upload gets its
  own fresh link — a same‑named capture never overwrites or repoints an earlier
  one.
- The link has **no expiry / download limit** by default. Revoke it anytime from
  the item's Share/Permissions dialog in the explorer, or via the shares admin.

---

## Endpoint reference

```
POST /api/sharex/upload
Content-Type: multipart/form-data
X-Filex-Token: <token>        (or: Authorization: Bearer <token>)
```

| Form field | Required | Meaning |
|------------|----------|---------|
| `file`     | yes      | the uploaded bytes (ShareX's default `FileFormName`) |
| `folder`   | no       | target directory (default `sharex`); created if missing |

**Response** `200 application/json`:

```json
{ "url": "<public share link>?inline=1" }
```

Errors return `{"error":"…"}` with an appropriate status (`400` bad multipart /
missing `file`, `401` bad/absent token, `403` scope/permission denied, `503` no
storage configured).

A quick `curl` sanity check:

```bash
curl -H "X-Filex-Token: $TOKEN" -F "file=@shot.png" \
  https://fm.brf.sh/api/sharex/upload
```

---

## Optional: choose a target folder

To file uploads somewhere other than `sharex/`, add a `folder` body argument in
ShareX: **Custom uploader settings → (your uploader) → Arguments →** add
`folder` = `screens` (for example). In the raw `.sxcu` that is:

```json
"Arguments": {
  "folder": "screens"
}
```

Nested paths work (`screens/2026`); the folder chain is created and indexed
automatically. Values are sanitized and any `..` traversal segments are
stripped, and a confined token can still only write inside its own root.

---

## Troubleshooting

- **`missing file field` (400)** — the uploader's **Body** must be
  *Form data (multipart/form-data)* and **File form name** must be `file`.
- **`token missing scope: write` (403)** — the token lacks the `write` scope;
  issue a new one on the API / MCP page with `write` selected.
- **401 unauthorized** — the `X-Filex-Token` header value is wrong or the token
  was revoked. Re‑copy it (tokens are shown only once at creation).
- **Link downloads instead of previewing** — confirm the returned URL still ends
  with `?inline=1`; some clients strip query strings when re‑sharing.
- **Wrong host** — the `.sxcu` **Request URL** must point at your filex host's
  `/api/sharex/upload`.
