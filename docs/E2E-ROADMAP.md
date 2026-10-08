# End-to-end encryption - roadmap

> What filex does today is [End-to-end encryption](E2E-ENCRYPTION.md); where
> the two pages disagree, that one is right. This page is the plan: what is
> built, what is being built, and the design of what is not, with its open
> questions. A design here is not a promise of a date.

## Where it stands

Encryption is a **level of a folder** - chosen when the folder is encrypted,
shown in the strip above it, changed in its **Encryption settings…**. There is
no separate "vault" area or tab: an encrypted folder is a folder, at whichever
level ([levels](E2E-ENCRYPTION.md#encryption-levels)).

| Piece | Status |
|---|---|
| Level 1 - contents only (the default) | Built |
| Level 2 - contents and names, names sealed per folder ([folder ids](E2E-ENCRYPTION.md#folder-ids)) | Built (v0.48) |
| Raising a folder from level 1 to level 2 | Built - [changing the level](E2E-ENCRYPTION.md#changing-the-level) |
| Encrypting a folder you already have, in place | Built - in the browser ([encrypting a folder you already have](E2E-ENCRYPTION.md#encrypting-a-folder-you-already-have)), and from the command line since v0.50 ([`filex encrypt`](CLI.md#filex-encrypt---make-a-folder-an-encrypted-folder)) |
| [A single encrypted file, and the streamed format](#1-encrypting-a-single-file) (no 200 MB limit) | Built (v0.48) - [single encrypted files](E2E-ENCRYPTION.md#single-encrypted-files-fxe), [streaming content](E2E-ENCRYPTION.md#streaming-content-stream) |
| [Level 3 - the vault](#3-the-vault-level) | Built (0.54), off by default: offered only where the server has `FILEX_E2E_VAULT` on. The format and the server's lock: [E2E-VAULT-FORMAT.md](E2E-VAULT-FORMAT.md) |
| [Editing office documents in an encrypted folder](E2E-OFFICE.md) (ONLYOFFICE in the browser, alone or together) | Designed; protocol prototype, not offered anywhere. The editor's side is an app of its own (`filex-office-editor`, AGPL); filex gives it the platform |

---

## 1. Encrypting a single file

**Status:** built in v0.48. [E2E-ENCRYPTION.md → Single encrypted
files](E2E-ENCRYPTION.md#single-encrypted-files-fxe) and [Streaming
content](E2E-ENCRYPTION.md#streaming-content-stream) describe what shipped,
and [the format reference](E2E-ENCRYPTION.md#format-reference) is the
normative text; the design that stood here is replaced by them.

**What it gave.** One file encrypted without a folder for it, as a
self-contained `.fxe` with its own password, recovery key and - where the
installation has one - escrow slot; a file anyone with the password and
`filex decrypt` can open without an account; and, through the streamed format,
files of any size in encrypted folders.

### Open questions

- **A password per file, or a key per person?** A password per file is simple
  and works for someone with no account. A personal key pair (unlocked once per
  session, like a folder) would let people encrypt files without typing a
  password each time and is the base for sharing - but it is a new identity
  system, and a lost personal key loses everything under it.
- **Sharing an encrypted file by link.** The key could ride in the URL
  fragment (`#k=…`), which browsers never send to the server - the Firefox Send
  model. It would make "sharing is off in encrypted files" untrue for single
  files. Worth it?

---

## 2. Encrypting a folder that already exists

**Status:** built - right-click a folder → **Encrypt with E2EE…**
([how it works](E2E-ENCRYPTION.md#encrypting-a-folder-you-already-have)), or
`filex encrypt` from the command line (v0.50).
The key file carries the required feature `conv` while the files are
converted; the server keeps no plaintext version of those writes and, when the
conversion ends, drops the thumbnails and extracted text it held for the
folder and - the owner's choice - its versions and trash entries.

Since v0.50 the conversion writes files over 200 MB in the streamed format
([above](#1-encrypting-a-single-file)), read and sent as streams
([how](E2E-ENCRYPTION.md#encrypting-a-folder-you-already-have)), and the
same job runs from the command line: `filex encrypt docs://folder`, through
the same API with the Go twin of the browser code, for folders too large to
convert in a tab; `filex encrypt ./folder` makes an encrypted folder from one
on disk, to upload
([CLI.md](CLI.md#filex-encrypt---make-a-folder-an-encrypted-folder)). What is
left:

- **Offering the command line first** for a large folder: a threshold (say
  2 GB or 5,000 files) in the dialog that says the command and why.

### Open questions

- **Lock the folder while it converts?** An app-style write lock would stop a
  WebDAV client adding plaintext mid-conversion; without it the job finds the
  new file on its next run.
- **Should the server refuse plaintext writes into an encrypted folder** (WebDAV,
  CLI, AI) altogether? That is the gap [E2E-ENCRYPTION.md](E2E-ENCRYPTION.md#ways-plaintext-still-reaches-the-server)
  already names; conversion makes it more visible.

---

## 3. The vault level

**Status:** built in 0.54, behind `FILEX_E2E_VAULT` (off by default;
[CONFIGURATION.md](CONFIGURATION.md#end-to-end-encryption-the-vault)). The
browser (the web app, the desktop app and the embeds), the server and the
command line (`filex decrypt`, `filex vault mount`, `filex vault prune`)
implement the format, the keys, the commit order, the garbage collection and
the server's write lock written down, with test vectors, in
[E2E-VAULT-FORMAT.md](E2E-VAULT-FORMAT.md); that page is the normative text,
and this section is the summary and what was decided.

**What for.** In an encrypted folder today the server sees how many files there
are, how big each one is and how the tree is shaped. At the **vault** level it
sees a number of equal-sized blocks and nothing else. It is the third level of
the same picker - offered when a folder is encrypted, never forced, and
**never a separate area or tab** - because its cost is real. Where the server
does not have it on (`capabilities.e2e_vault` is `false`) it is not in the
picker at all.

### What it costs - the text people see before choosing

> **Vault: the server sees only encrypted blocks of equal size.** It cannot
> tell how many files you keep here, how big they are, how they are arranged or
> when each one changed. The price: **only the filex web and desktop apps,
> `filex decrypt` and `filex vault mount` can open anything in it**, and **one
> person writes at a time** - the others read until they are done. WebDAV, the
> command line, desktop sync, share links, the AI tools and every other app
> see blocks, not files. Moving a file into or out of a vault is an upload,
> not a move. A vault starts empty: an existing folder is not turned into one.

The level picker then lists three levels: **1 · Contents only** (still the
default) · **2 · Contents and names** · **3 · Vault**.

### Layout on the server

```
Kasa/
  .filex-e2e.json                 v3, req ["vault"], vault: {v, id, pack}
  v/idx/0000000000000003.fxi      the encrypted tree, one file per generation, padded (Padmé, 64 KiB at least)
  v/p/b0/b067d7bcd62c….fxp        packs, exactly 4 MiB (or 16 MiB) each, random names
```

- **Packs** hold the encrypted contents of files, each file a
  [STREAM](E2E-ENCRYPTION.md#streaming-content-stream) under its own key, laid
  end to end. Small files share a pack, large ones span several. Every pack is
  filled up with random bytes to exactly its size - **4 MiB** by default,
  **16 MiB** when chosen at creation - and named at random, so the server sees
  a count of equal blocks. Packs are written once and never changed.
- **The index** is the whole tree: folders, names, sizes, timestamps, and for
  each file where its encrypted bytes lie, as `(pack, offset, length)`. It is
  encrypted under a key derived from the folder key (HKDF), written as a new
  file - a **generation** - on every change, and padded so that its size says
  only roughly how large the tree is.
- **Reading a file** fetches the byte ranges it needs from the packs, decrypts
  them and assembles the file in the browser.
- **Keys** are the levels' own: the password, the recovery key and the escrow
  key reach the folder key, from which every other key is derived. Changing
  the password changes nothing in the vault.

### One writer at a time

Two writers are never merged. Instead the server keeps a **write lock** per
vault:

- writing - an upload, a new folder, a rename, a move, a delete, a save -
  takes the lock; while one session holds it, every other session reads,
  and reading never waits for it;
- a writer that stays idle loses it: after **3 minutes** by default, up to
  **10 minutes**, each person's own setting; a lease of a minute, renewed in
  the background, frees it when a tab or a computer simply goes away;
- the server orders the commits (generation `n + 1` only after `n`), so no
  storage needs a conditional create, and **every storage** works - local,
  S3, SFTP, WebDAV, SMB, FTP;
- the writer stores the packs first and the index last, so a write that stops
  half-way leaves only unreferenced packs behind and the vault as it was;
- inside a vault only this protocol writes: WebDAV, S3, SFTP, the agent tools
  and every other door are refused.

### Garbage collection

Deleting a file only drops it from the index; its bytes stay in their packs.
The lock holder cleans up, because the server cannot read the index: after a
commit it deletes the packs no kept generation uses (the newest three, and any
replaced less than 15 minutes ago, are kept for readers still on them) and the
packs left by interrupted writes, and when more than half of the packs' room
is dead it copies the live bytes of mostly-empty packs into new ones - copied,
not re-encrypted. `filex vault prune` does a full pass on demand. This is
restic's `prune` and Kopia's maintenance, on a smaller scale.

### Clients

- **Web and desktop**: the explorer in `packages/core`, the same code in both
  and in every embed.
- **`filex decrypt`**: a vault copied off a storage, offline, or one on a
  server (`filex decrypt docs://Kasa`).
- **`filex vault mount`**: a WebDAV server on this machine, mounted by the
  operating system (no cgo and no FUSE, so it is not limited to where
  `filex mount` runs - macOS included); it takes the lock at its first
  write, follows the same idle rule, and closes itself after 15 minutes
  without any file operation.

### Prior art

- **restic** - packs of encrypted blobs, separate index files, lock files and
  `prune`; content-defined chunking. <https://restic.readthedocs.io/en/stable/100_references.html#design>
- **Kopia** - content-addressed blobs in pack files, index blobs, epoch-based
  index management and scheduled maintenance. <https://kopia.io/docs/advanced/architecture/>
- **Tahoe-LAFS** - capabilities, encrypted immutable and mutable files and
  directory nodes, erasure-coded shares no storage server can read.
  <https://tahoe-lafs.readthedocs.io/en/latest/architecture.html>
- **Cryptomator** - for contrast: it encrypts names and flattens directories
  into `d/` by directory ID, but file count and sizes remain visible to the
  storage. <https://docs.cryptomator.org/en/latest/security/architecture/>
- **Padmé** - the padding of the index. Nikitin et al., *Reducing Metadata
  Leakage from Encrypted Files and Communication with PURBs*, PETS 2019.

### Effort

The estimate was 16 to 20 days; the parts were built side by side in two
days (2026-10-06/07), each against the test vectors.

| Part | Status |
|---|---|
| Format, keys, test vectors ([E2E-VAULT-FORMAT.md](E2E-VAULT-FORMAT.md)), a repack among them | done |
| Browser: index, packs, reading by ranges, writing, collection, the explorer's vault mode, loaded only when a vault is opened | done |
| Server: the lock, the vault API, the write rule on every door, the document server's save included | done |
| `filex decrypt` for vaults, `filex vault mount`, `filex vault prune` | done; the mount is tested against the API, not yet measured mounted by each operating system |
| Tests (interrupted writes, a lost lock, collection, both writers' fixtures opened by the other) and documentation | done |

### Decisions (2026-10-06)

The open questions this section used to end with are answered:

- **Pack size**: chosen when the vault is created, **4 MiB** (the default) or
  **16 MiB**.
- **Deduplication**: **not** in the first version. The index reserves a flag
  and a content kind for it, so it can come later without a new format.
- **Concurrent writers**: **one writer at a time**, under the server's lock
  (above), instead of a merge.
- **How large a vault may get**: **one index** per vault, up to 250 000 files
  and folders (and a 32 MiB index), with a warning from 200 000.
- **Which storages**: **all of them**. The lock is the server's, so none of
  them needs a conditional create or a lock file.
- **Changing the level**: **a new, empty vault only**. Neither an existing
  folder into a vault nor a vault back.
- **Recovery**: the same key slots as levels 1 and 2 - password, recovery key,
  escrow.
- **Clients**: the web and desktop apps, `filex decrypt`, and
  `filex vault mount` as a local WebDAV server.

---

## Order and total

| | Days | Depends on |
|---|---|---|
| 1. Single file (+ streaming, lifts 200 MB) | built (v0.48) | - |
| 2. `filex encrypt`, large files in a conversion | built (v0.50) | the streaming format from 1 |
| 3. Vault level | built (0.54), off by default | the streaming format from 1; the server's write lock |

The vault was most of what was left, and the one piece that changes what the
server can know.
