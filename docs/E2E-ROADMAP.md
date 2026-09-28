# End-to-end encryption — roadmap

> What filex does today is [End-to-end encryption](E2E-ENCRYPTION.md); where
> the two pages disagree, that one is right. This page is the plan: what is
> built, what is being built, and the design of what is not, with its open
> questions. A design here is not a promise of a date.

## Where it stands

Encryption is a **level of a folder** — chosen when the folder is encrypted,
shown in the strip above it, changed in its **Encryption settings…**. There is
no separate "vault" area or tab: an encrypted folder is a folder, at whichever
level ([levels](E2E-ENCRYPTION.md#encryption-levels)).

| Piece | Status |
|---|---|
| Level 1 — contents only (the default) | Built |
| Level 2 — contents and names, names sealed per folder ([folder ids](E2E-ENCRYPTION.md#folder-ids)) | Built (v0.48) |
| Raising a folder from level 1 to level 2 | Built — [changing the level](E2E-ENCRYPTION.md#changing-the-level) |
| Encrypting a folder you already have, in place | Built in the browser — [encrypting a folder you already have](E2E-ENCRYPTION.md#encrypting-a-folder-you-already-have); the command-line twin is [below](#2-encrypting-a-folder-that-already-exists) |
| [A single encrypted file, and the streamed format](#1-encrypting-a-single-file) (no 200 MB limit) | Built (v0.48) — [single encrypted files](E2E-ENCRYPTION.md#single-encrypted-files-fxe), [streaming content](E2E-ENCRYPTION.md#streaming-content-stream) |
| [Level 3 — the vault](#3-the-vault-level) | Designed, not built — not offered anywhere until it works |

---

## 1. Encrypting a single file

**Status:** built in v0.48. [E2E-ENCRYPTION.md → Single encrypted
files](E2E-ENCRYPTION.md#single-encrypted-files-fxe) and [Streaming
content](E2E-ENCRYPTION.md#streaming-content-stream) describe what shipped,
and [the format reference](E2E-ENCRYPTION.md#format-reference) is the
normative text; the design that stood here is replaced by them.

**What it gave.** One file encrypted without a folder for it, as a
self-contained `.fxe` with its own password, recovery key and — where the
installation has one — escrow slot; a file anyone with the password and
`filex decrypt` can open without an account; and, through the streamed format,
files of any size in encrypted folders.

### Open questions

- **A password per file, or a key per person?** A password per file is simple
  and works for someone with no account. A personal key pair (unlocked once per
  session, like a folder) would let people encrypt files without typing a
  password each time and is the base for sharing — but it is a new identity
  system, and a lost personal key loses everything under it.
- **Sharing an encrypted file by link.** The key could ride in the URL
  fragment (`#k=…`), which browsers never send to the server — the Firefox Send
  model. It would make "sharing is off in encrypted files" untrue for single
  files. Worth it?

---

## 2. Encrypting a folder that already exists

**Status:** built in the browser — right-click a folder → **Encrypt with
E2EE…** ([how it works](E2E-ENCRYPTION.md#encrypting-a-folder-you-already-have)).
The key file carries the required feature `conv` while the files are
converted; the server keeps no plaintext version of those writes and, when the
conversion ends, drops the thumbnails and extracted text it held for the
folder and — the owner's choice — its versions and trash entries.

What is left:

- **`filex encrypt <remote folder>`** — the same job from the command line,
  streaming through the same API with the Go twin of the browser code, for
  folders too large to convert in a tab. A threshold (say 2 GB or 5,000
  files) would decide which is offered first. 2–3 days.
- **Files over the one-shot limit.** The browser leaves them as they are and
  says so. The streamed format ([above](#1-encrypting-a-single-file)) is in
  since v0.48, but the conversion does not use it yet. 1 day.

### Open questions

- **Lock the folder while it converts?** An app-style write lock would stop a
  WebDAV client adding plaintext mid-conversion; without it the job finds the
  new file on its next run.
- **Should the server refuse plaintext writes into an encrypted folder** (WebDAV,
  CLI, AI) altogether? That is the gap [E2E-ENCRYPTION.md](E2E-ENCRYPTION.md#ways-plaintext-still-reaches-the-server)
  already names; conversion makes it more visible.

---

## 3. The vault level

**What for.** In an encrypted folder today the server sees how many files there
are, how big each one is and how the tree is shaped. At the **vault** level it
sees a number of equal-sized blocks and nothing else. It is the third level of
the same picker — offered when a folder is encrypted and in its settings,
never forced, and **never a separate area or tab** — because its cost is real.
Until it works it is not in the picker at all.

### What it costs — the text people see before choosing

> **Vault: the server sees only encrypted blocks of equal size.** It cannot
> tell how many files you keep here, how big they are, how they are arranged or
> when each one changed. The price: **only the filex web app and `filex
> decrypt` can open anything in it.** WebDAV, the command line, desktop sync,
> share links, the AI tools and every other app see blocks, not files. Moving a
> file into or out of a vault is an upload, not a move.

The level picker then lists three levels: **1 · Contents only** (still the
default) · **2 · Contents and names** · **3 · Vault**.

### Layout on the server

```
Kasa/
  .filex-e2e.json          v3, req ["vault"], vault: {pack_size, generation}
  v/idx/<generation>.fxi    encrypted index snapshots, padded to a size bucket
  v/p/<2 hex>/<32 hex>.fxp  packs, exactly pack_size bytes each
```

- **Packs** hold encrypted chunks of file contents (1 MiB chunks, the
  [streaming format](E2E-ENCRYPTION.md#streaming-content-stream)). Small files share a
  pack, large ones span several. Every pack is padded with random bytes to
  exactly `pack_size` (4 MiB by default) and named at random, so the server
  sees a count of equal blocks. Packs are immutable once written.
- **The index** is the whole tree: folders, names, sizes, timestamps, and for
  each file the list of (pack, offset, length) of its chunks. It is encrypted
  under a key sealed by the FMK, written as a new immutable snapshot on every
  change, and padded to the next size bucket (64 KiB, 128 KiB, …) so its size
  says only roughly how large the tree is.
- **Reading a file** fetches its chunks from the packs with HTTP range
  requests, decrypts them and assembles the file in the browser.

### Concurrent writers

Two browsers editing one vault must not lose each other's changes:

- new packs never collide (random names, write-once);
- a new index snapshot is written **only if its generation is still free** — a
  conditional create (`If-None-Match: *` on S3, an exclusive create on a local
  disk). filex's upload API needs this as a flag; it has none today;
- a writer that loses the race reads the winner's index, merges the two
  changes (different entries: both apply; the same name twice: keep both,
  the way a sync conflict is kept) and tries the next generation.

### Garbage collection

Deleting a file only drops it from the index; its chunks stay in their packs.
A maintenance job — in the browser, or `filex vault prune` — finds packs no
retained index snapshot refers to and deletes them, and repacks packs that are
mostly dead, under a short-lived lease file so two prunes never run at once.
This is restic's `prune` and Kopia's maintenance, on a smaller scale.

### Prior art

- **restic** — packs of encrypted blobs, separate index files, lock files and
  `prune`; content-defined chunking. <https://restic.readthedocs.io/en/stable/100_references.html#design>
- **Kopia** — content-addressed blobs in pack files, index blobs, epoch-based
  index management and scheduled maintenance. <https://kopia.io/docs/advanced/architecture/>
- **Tahoe-LAFS** — capabilities, encrypted immutable and mutable files and
  directory nodes, erasure-coded shares no storage server can read.
  <https://tahoe-lafs.readthedocs.io/en/latest/architecture.html>
- **Cryptomator** — for contrast: it encrypts names and flattens directories
  into `d/` by directory ID, but file count and sizes remain visible to the
  storage. <https://docs.cryptomator.org/en/latest/security/architecture/>

### Effort

| Part | Days |
|---|---|
| Format spec, JS index + packs + padding | 6–8 |
| Conditional create in the upload API and every driver | 2 |
| Concurrent-writer merge, garbage collection, lease | 4–5 |
| Explorer: list, open, upload, rename and move from the index | 5–7 |
| `filex decrypt` for vaults (and later `filex vault mount`) | 2–3 |
| Tests (races, interrupted writes, GC), docs | 3 |
| **Total** | **22–28** |

### Open questions

- **Pack size and padding.** 4 MiB wastes up to 4 MiB on a nearly empty vault
  and hides small files well; 16 MiB hides more and wastes more. Fixed, or
  chosen at creation?
- **Deduplication.** Content-defined chunking plus keyed deduplication inside
  one vault saves space; deduplication across vaults would leak equality and
  is out. Worth the complexity for a first version?
- **How large may a vault get in a browser?** The whole index is in memory;
  at ~100 bytes per entry a million files is 100 MB. Splitting the index per
  folder is possible and makes merges harder.
- **Which storages?** A vault needs range reads and a conditional create. Local
  and S3 have both; SFTP and WebDAV backends would need a lock-file fallback.
- **Changing the level** of a folder to the vault and back — the
  [conversion job](#2-encrypting-a-folder-that-already-exists), with packs.

---

## Order and total

| | Days | Depends on |
|---|---|---|
| 1. Single file (+ streaming, lifts 200 MB) | built (v0.48) | — |
| 2. `filex encrypt`, large files in a conversion | 3–4 | the streaming format from 1 |
| 3. Vault level | 22–28 | the streaming format from 1; conditional create |

The vault is most of what is left, and the one piece that changes what the
server can know.
