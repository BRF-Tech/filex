# End-to-end encryption - the vault format (level 3)

> **Status: built, off by default.** This page is the normative format and
> protocol of the third encryption level, the **vault**. It was written before
> the code so that the three implementations - the browser (`packages/core`,
> which the web app, the desktop app and the embeds all run), the server, and
> the command line (`filex decrypt`, `filex vault mount`) - build the same
> thing, and all three now do. A server offers vaults only where
> `FILEX_E2E_VAULT` is on ([CONFIGURATION.md](CONFIGURATION.md#end-to-end-encryption-the-vault));
> elsewhere `capabilities.e2e_vault` is `false` and no client offers level 3
> ([roadmap](E2E-ROADMAP.md#3-the-vault-level)).
>
> Levels 1 and 2 are [End-to-end encryption](E2E-ENCRYPTION.md). A vault
> reuses their key slots and their [STREAM](E2E-ENCRYPTION.md#streaming-content-stream)
> content encryption, and says so where it does.

**How to read this page.** "Must" is binding: a reader or writer that does
otherwise is wrong. The byte formats have no "should". The
[test vectors](#test-vectors) are part of the format: an implementation that
disagrees with them is wrong, and where this text and the vectors disagree the
text has a bug.

- [What the server sees](#what-the-server-sees)
- [Decisions](#decisions)
- [Layout](#layout)
- [The key file](#the-key-file)
- [Keys and the nonce rule](#keys-and-the-nonce-rule)
- [Packs](#packs)
- [File contents](#file-contents)
- [The index](#the-index) - [file](#index-file) · [body](#index-body) · [order](#canonical-order) · [generations](#generations) · [limits](#limits)
- [Writing](#writing) - [the commit](#the-commit) · [canonical layout](#canonical-layout)
- [Reading](#reading)
- [Garbage collection](#garbage-collection)
- [The write lock](#the-write-lock) - [semantics](#lock-semantics) · [the holder](#what-the-holder-does) · [API](#api) · [other doors](#writes-from-anywhere-else) · [permissions](#permissions-and-tenancy) · [audit and events](#audit-and-events)
- [Clients](#clients)
- [Versions and later additions](#versions-and-later-additions)
- [Test vectors](#test-vectors)

---

## What the server sees

A vault is one folder. On the storage it holds its key file, a few encrypted
index files and packs of one fixed size (the names are from the
[test vectors](#test-vectors)):

```
Kasa/
  .filex-e2e.json                                key file: v3, req ["vault"]
  v/idx/0000000000000002.fxi                     the tree, one file per generation
  v/idx/0000000000000003.fxi
  v/p/b0/b067d7bcd62c9f817216a5ef1b1b653b.fxp    packs: 4 MiB (or 16 MiB) each
  v/p/1c/1c396c15f908cc04b828eef3e966f80d.fxp
  ...
```

The server, and anyone holding its disk or its backups, sees:

- that the folder is a vault, its own name, its pack size;
- how many packs there are, and when each one was written;
- how large the index is, to within a few percent ([Padmé](#index-file));
- how many generations are kept, and when each was committed;
- who held the write lock, and when.

It does not see how many files or folders the vault holds, their names, sizes
or timestamps, how they are arranged, or which pack holds which file.

## Decisions

The decisions of 2026-10-06 that everything below follows:

- **A level of a folder**, the third of the same picker. Only a **new, empty**
  vault is made; an existing folder is not converted to a vault, and a vault is
  not converted back.
- **Equal packs.** Contents go into packs of one size per vault, **4 MiB** by
  default or **16 MiB** chosen when the vault is created, filled up with random
  bytes. Subfolders do not exist on the storage: the tree is in the encrypted
  index.
- **One writer at a time.** Writing takes a lock the server keeps; while it is
  held everybody else reads. A writer that stays idle loses it: after **3
  minutes** by default, at most **10 minutes**, each person's own setting.
  Concurrent writers are never merged.
- **Then the vault locks itself.** An open vault in which nothing at all
  happens for another **15 minutes** after its writer went back to read-only
  (or after it was opened, when it never wrote) is **locked**: its keys leave
  memory and opening it asks for the password again
  ([idle lock](#the-idle-lock)).
- **No deduplication.** A field is [reserved](#versions-and-later-additions)
  so that it can come later without breaking the format.
- **One index** per vault, with a [ceiling](#limits) and a warning before it.
- **Every storage driver** (local, S3, SFTP, WebDAV, SMB, FTP). The server
  orders the commits, so no storage needs a conditional create.
- **The same key slots** as levels 1 and 2: password, recovery key, escrow.
- **Clients:** the web app, the desktop app (the same explorer),
  `filex decrypt`, and `filex vault mount` (a WebDAV server on this machine
  that the operating system mounts; no cgo, no FUSE).

## Layout

Paths relative to the vault folder. Nothing else belongs to a vault.

| Path | What |
|---|---|
| `.filex-e2e.json` | the [key file](#the-key-file) |
| `v/idx/G.fxi` | the index of generation `G`, `G` written as **16 lower-case hex digits** (`0000000000000003`) |
| `v/p/XX/ID.fxp` | a pack; `ID` is its 16-byte id as **32 lower-case hex digits**, `XX` the first two of them |
| `v/idx/.tmp-*` | the server's temporary file while it commits an index; readers and writers ignore it |

A writer creates nothing else in a vault. A reader ignores anything else it
finds there. Empty `v/p/XX/` folders are harmless; the server may remove them.

## The key file

The marker of [levels 1 and 2](E2E-ENCRYPTION.md#folder-marker---filex-e2ejson),
version 3, with one required feature, `vault`:

```json
{
  "v": 3,
  "req": ["vault"],
  "salt": "<base64, 16 bytes>",
  "iter": 600000,
  "verify": "<base64: IV, AES-GCM(KEK, 'filex-e2e-verify-v1')>",
  "fmk": "wrapped",
  "fmk_pw": "<base64: IV, AES-GCM(KEK, FMK)>",
  "rk": { "salt": "<base64>", "blob": "<base64>" },
  "esc": { "kid": "<hex>", "alg": "RSA-OAEP-256", "blob": "<base64>" },
  "vault": { "v": 1, "id": "<base64url, 16 bytes>", "pack": 22 }
}
```

| Field | Rule |
|---|---|
| `v` | `3` |
| `req` | exactly `["vault"]`. A vault never carries `names`, `rekey` or `conv`, nor their slots: names are inside the index, a re-key is not offered (the FMK derives every key, so a new FMK is a new vault), and nothing is converted. A key file with `vault` and anything else in `req` is malformed |
| `fmk` | `"wrapped"`, with `fmk_pw`. A vault's FMK is always 32 random bytes; `"kek"` is malformed here |
| `salt` / `iter` / `verify` / `fmk_pw` / `rk` / `esc` / `esc_declined` | exactly as in levels 1 and 2 ([format reference](E2E-ENCRYPTION.md#format-reference)) |
| `vault.v` | the format version of packs and index: `1`. A reader that meets a higher one refuses the vault and says a newer filex is needed |
| `vault.id` | 16 random bytes, base64url without padding (22 characters). The salt of every key below, and what the server keys the [write lock](#the-write-lock) by |
| `vault.pack` | the pack size as a power of two. Writers create `22` (4 MiB, the default) or `24` (16 MiB). Readers accept `16` to `24` |

- Unknown fields are kept on a rewrite, as for every key file.
- The key file changes only for what changes it at levels 1 and 2: a new
  password, a reset with the recovery key, an escrow slot added or declined.
  `v`, `req` and `vault` never change after the vault is made, and the server
  refuses a rewrite that changes them ([other doors](#writes-from-anywhere-else)).
- A filex that does not know `vault` refuses the folder and names the feature
  (the [`req` rule](E2E-ENCRYPTION.md#the-marker-v3-and-required-features));
  filex 0.47 and older refuse every v3 key file.

Unlocking is unchanged: the password, the recovery key or the escrow key
yields the 32 FMK bytes. In the browser those bytes are imported **once, as a
non-extractable HKDF key** (`importKey('raw', fmk, 'HKDF', false,
['deriveKey', 'deriveBits'])`) and then zeroed. A vault's FMK is never used as
an AES-GCM key and never encrypts anything itself.

## Keys and the nonce rule

```
FMK (32 bytes, from a key slot)
 |- HKDF-SHA-256(IKM = FMK, salt = vault id,
 |               info = "filex-vault-index-v1"   ‖ seal id)     -> index key of one index file
 '- HKDF-SHA-256(IKM = FMK, salt = vault id,
                 info = "filex-vault-content-v1" ‖ content id)  -> content key of one file version
```

- HKDF is RFC 5869 with SHA-256; the output is 32 bytes, an AES-256-GCM key.
  The salt is the 16 raw bytes of `vault.id`. The labels are ASCII; the 16-byte
  id follows them raw.
- **Seal id**: 16 random bytes, drawn each time an index file is sealed.
- **Content id**: 16 random bytes, drawn each time new contents of a file are
  about to be written.

**The nonce rule. Every AES-GCM key of a vault encrypts exactly one
plaintext.**

- A content key encrypts one version of one file, as a STREAM whose nonce for
  chunk `i` is 7 zero bytes ‖ `i` (uint32, big-endian) ‖ the last-chunk flag:
  different for every chunk under that key.
- An index key encrypts one index file, once, with the all-zero 12-byte nonce.
- Each key comes from its own random 128-bit id, so two keys are equal with
  probability 2^-128 per pair (about 2^-64 after 2^32 ids).

What a writer must therefore do:

- New contents of a file - an upload, a save, an overwrite - always get a new
  content id, **even when the bytes are the same as before**.
- An index is sealed under a new seal id every time it is sealed. Retrying an
  upload that failed re-sends the **same bytes**; anything that changes the
  tree is a new sealing with a new seal id.
- Ids come from the platform's CSPRNG (`crypto.getRandomValues`,
  `crypto/rand`) and from nothing else. No id is derived from a counter, a
  clock, a path or the content.
- Moving encrypted bytes is not encrypting. A pack has no key of its own, so
  copying a file's ciphertext into another pack ([repacking](#garbage-collection),
  a re-uploaded pack) never seals anything a second time.

**Why a key per file version, not a key per pack.** With per-pack keys every
repack would decrypt and re-encrypt, a file's chunk could not continue in the
next pack (chunk sizes would bend to the room left in a pack), and a pack whose
upload timed out could not be sent again under a new name without encrypting
again. Per-file keys make a pack a plain container: its data area is bytes
copied in, and the content encryption is exactly the STREAM of levels 1 and 2
(`lib/e2estream.ts`, `stream.go`). The index stores a 16-byte content id per
file instead of a key.

## Packs

A pack is exactly 2^`vault.pack` bytes:

| Offset | Length | Field |
|---|---|---|
| 0 | 8 | magic: ASCII `filexvlt` |
| 8 | 1 | format version `0x01` |
| 9 | 1 | kind `0x50` (ASCII `P`) |
| 10 | 1 | pack size, log2 (= `vault.pack`) |
| 11 | 5 | zero |
| 16 | 16 | pack id (= its name) |
| 32 | 2^`pack` - 32 | data: [extents](#file-contents) of encrypted file contents, then random padding |

- **Data.** The writer places extents back to back from offset 32. Where the
  last one ends, bytes from the CSPRNG fill the pack to its size. **Padding is
  never zeros**: zeros would show the server how full every pack is.
- **Pack id.** 16 random bytes, drawn when the pack is opened. The pack is
  stored as `v/p/XX/ID.fxp` (lower-case hex).
- **Written once.** A pack is uploaded whole, once, and never appended to,
  rewritten or renamed. When an upload's outcome is unknown (a time-out), the
  writer uploads the same data under a **new pack id** (only the header
  changes) and references that one; the first, if it did land, is an
  [orphan](#garbage-collection).
- **The header.** A reader that fetches byte ranges of a pack does not read
  it. A reader that holds a whole pack (`filex decrypt` on a copy,
  `filex vault prune`) checks magic, version `1`, kind `P`, the log2 against
  `vault.pack`, the five zeros and the id against the name; any mismatch is
  damage.

## File contents

- A file of **0 bytes** stores nothing.
- A file of `n > 0` bytes is encrypted as a **STREAM**
  ([format](E2E-ENCRYPTION.md#streaming-content-stream)) under the content key
  of a new content id: chunks of 2^20 bytes (writers; readers accept a
  recorded log2 of 10 to 24), nonce prefix = **7 zero bytes** (the key is
  used once, so the prefix carries nothing), no associated data. Its
  **body** is `n + 16 × ⌈n ÷ 2^log2⌉` bytes.
- The body is kept as a list of **extents** `(pack, offset, length)`: the
  extents' bytes, read in order and joined, are the body.
  - An extent lies inside one pack's data area: `offset ≥ 32` and
    `offset + length ≤ 2^pack`.
  - A body may span any number of packs, and a pack may hold extents of many
    files.
  - Writers make extents maximal: two extents of one file that follow each
    other in the same pack are written as one. Readers accept either.

**Reading bytes `[a, b)` of a file** (the explorer's preview, a seek in a
video, the mount):

1. The chunks needed are `i0 = ⌊a ÷ 2^log2⌋` to `i1 = ⌊(b - 1) ÷ 2^log2⌋`.
2. Their ciphertext is the body's bytes from `i0 × (2^log2 + 16)` to
   `min((i1 + 1) × (2^log2 + 16), body length)`.
3. Map that span onto the extents and fetch it with HTTP range requests, one
   per run of bytes inside one pack.
4. Decrypt chunk `i` with the nonce 7 zero bytes ‖ `i` (uint32, big-endian)
   ‖ `0x01` when `i` is the file's last chunk (its chunk count minus one),
   `0x00` otherwise, and keep `[a, b)`.

## The index

### Index file

| Offset | Length | Field |
|---|---|---|
| 0 | 8 | magic: ASCII `filexvlt` |
| 8 | 1 | format version `0x01` |
| 9 | 1 | kind `0x49` (ASCII `I`) |
| 10 | 6 | zero |
| 16 | 8 | generation, uint64 big-endian (= its name) |
| 24 | 16 | seal id |
| 40 | n | AES-256-GCM(index key, nonce = 12 zero bytes, plaintext, associated data = bytes 0 to 39) |
| 40 + n | 16 | GCM tag |

- **Plaintext** = the [body](#index-body) followed by zero bytes, as many as
  make the file its padded size.
- **Padded size** = `max(65 536, Padmé(40 + body length + 16))`, where for a
  length `L`: `E = ⌊log2 L⌋`, `S = ⌊log2 E⌋ + 1`, `step = 2^(E - S)`,
  `Padmé(L) = ⌈L ÷ step⌉ × step` (Nikitin et al., *Reducing Metadata Leakage
  from Encrypted Files and Communication with PURBs*, PETS 2019). It leaks
  `O(log log L)` bits of the size and costs at most 12 % (about 3 % at index
  sizes). A Padmé size is its own Padmé size, which is how the server checks
  one.
- **A reader refuses the file as damaged** unless: the magic, version `1` and
  kind `I` are right; the six bytes at 10 are zero; the generation equals its
  name; the size is at most 64 MiB and is the padded size of the body it
  holds; the tag verifies; and every byte after the body is zero.

### Index body

Integers are **uvarint**: unsigned LEB128 - seven bits per byte, the least
significant group first, the high bit set on every byte but the last. The
encoding is **minimal** (a multi-byte uvarint never ends in `0x00`) and the
value is **at most 2^53 - 1**, so a JavaScript number holds every one exactly.
A reader refuses a non-minimal or larger value.

```
body     = version:u8  flags:uvarint
           pack_count:uvarint   pack_id:16B × pack_count
           entry_count:uvarint  entry × entry_count
           grave_count:uvarint  (pack_id:16B  died:uvarint) × grave_count
           ext_len:uvarint      ext:ext_len B

entry    = kind:u8  parent:uvarint  name_len:uvarint  name:name_len B  mtime:uvarint
           [ kind 2 only:  size:uvarint  content ]
           ext_len:uvarint  ext:ext_len B

content  = 0x00                                            size = 0, nothing stored
         | 0x01  content_id:16B  chunk_log2:u8  extent_count:uvarint  extent × extent_count
extent   = pack:uvarint  offset:uvarint  length:uvarint
```

| Field | Rule |
|---|---|
| `version` | `1`. Anything else: refused |
| `flags` | `0`. A set bit is a required feature: a reader refuses a bit it does not know and names it. Bit 0 is reserved for [deduplication](#versions-and-later-additions) |
| pack table | the id of every pack an extent of this generation uses, each once, in ascending byte order, and no other |
| `kind` | `1` folder, `2` file; anything else refused |
| `parent` | `0` = the vault root; `k ≥ 1` = the `k`-th entry of this list (1-based), which comes earlier and is a folder |
| `name` | UTF-8, 1 to 255 bytes; not `.` or `..`; no `/`, `\`, byte `0x00`-`0x1F` or `0x7F`. Writers store it normalised to **NFC**. Readers check the byte rules but **not** the normalisation (two Unicode versions may disagree on it). Siblings have different names, compared as bytes |
| `mtime` | milliseconds since 1970-01-01T00:00:00Z. A file: when its contents last changed (what the uploader or the operating system said, else the commit time). A folder: when it was made. A rename or move keeps an entry's `mtime`. Before 1970 is written as `0` |
| `size` | plaintext bytes |
| content kind | `0` exactly when `size = 0`, `1` exactly when `size > 0`. `2` to `255` are reserved (`2`: deduplicated chunks) and refused |
| `content_id` | the file version's [content id](#keys-and-the-nonce-rule) |
| `chunk_log2` | `10` to `24`; writers `20` |
| extents | at least one; `pack < pack_count`; `length ≥ 1`; `offset ≥ 32`; `offset + length ≤ 2^vault.pack`; the lengths add up to the body size above. Writers never let extents overlap; readers do not check it |
| graveyard | packs the latest tree no longer uses that an older kept generation may still use ([garbage collection](#garbage-collection)); ascending by id, none of them in the pack table, `2 ≤ died ≤` this generation |
| `ext` (entry and body) | extension bytes. Format-1 writers write `ext_len = 0`. A reader skips them. **A writer that finds a non-empty `ext` anywhere in the latest index does not write the vault**: it stays read-only and says that a newer filex wrote it, because the bytes may mean something a change would break |

### Canonical order

Entries are in **pre-order** - a folder is followed directly by its whole
subtree - and **siblings are ordered by the bytes of their names** (byte by
byte, a shorter name first when one is a prefix of the other). Put another
way: sorted by path, compared one segment at a time, each segment as bytes.
Not by locale, not ignoring case: `Zebra` comes before `alfa`, `cay` before
`Çay` before `çay`.

A reader checks this in one pass: it keeps the chain of folders it is inside;
an entry's parent must be on that chain (leave folders until it is; if it is
not there, the index is damaged), and its name must be greater than the
previous name under the same parent.

So a tree, its pack table and its graveyard have **one encoding**: two writers
that agree on them write the same bytes.

### Generations

- **Generation 1** is written when the vault is created: the empty body
  `01 00 00 00 00 00` in a 64 KiB file.
- Each commit writes the next generation, latest + 1. A generation is never
  rewritten.
- The **latest** generation is the highest-numbered `v/idx/*.fxi` present.

### Limits

| | A writer refuses a change beyond | A reader refuses an index beyond |
|---|---|---|
| Entries (files and folders) | 250 000 | 1 000 000 |
| Index file | 32 MiB | 64 MiB |
| Depth (the root's children are depth 1) | 256 | 256 |
| A name | 255 bytes | 255 bytes |

From **200 000 entries or a 24 MiB index**, every client warns before a change
("This vault holds 212 400 of at most 250 000 files and folders"). At the
limit the change is refused before anything is written, and the message says
why. 250 000 entries make an index of roughly 15 to 25 MB, which a browser
tab decrypts and holds whole; splitting a vault's index is not part of
format 1.

---

## Writing

### The commit

Everything here happens under the [write lock](#the-write-lock).

1. **Latest first.** Right after taking the lock the writer asks for the
   latest generation; if it is not the one it has in memory, it loads it
   before changing anything.
2. **Packs.** It applies the changes to its tree in memory. New contents are
   encrypted and appended to the open pack ([canonical layout](#canonical-layout));
   a pack is uploaded when it is full, or padded and uploaded when the change
   ends.
3. **Index.** When every pack the change needs has been stored, it seals the
   new index (generation = latest + 1, a new seal id) and commits it.
4. Only after the commit succeeded is the change reported as saved; only then
   may [garbage collection](#garbage-collection) delete anything.

Packs come before the index, so an index never names a pack that is not
stored; the index is the only thing that makes anything visible.

**What a stop leaves behind.**

| Stopped | What remains | What readers see |
|---|---|---|
| before the commit | packs no index names (orphans) | the previous generation, unchanged |
| during the commit | the server writes the index under `v/idx/.tmp-*` and renames it into place; on S3, one PUT, which is atomic. A storage that can do neither may keep a torn file | the previous generation; a torn latest file fails its tag and is a [damaged latest](#reading) |
| after the commit | garbage, until the next collection | the new generation |

**When to commit.** A writer commits when an operation the person sees has
finished (an upload batch, a new folder, a rename, a move, a delete, a save);
during a long one at least every 30 seconds in which packs were stored; and
before it releases the lock. Operations that end within 2 seconds of each
other may share a generation. The browser never says a change is saved before
its commit succeeded. (`filex vault mount` is the [one exception](#filex-vault-mount),
and says so.)

### Canonical layout

Readers accept any valid layout. Writers follow this one, so that two
implementations given the same operations and the same random bytes write the
same packs and the same index - which is what the [vectors](#test-vectors)
check.

1. A generation starts with **no open pack**. A pack of an earlier generation
   is never added to.
2. Operations are applied in order. For new contents of `n > 0` bytes: draw
   the **content id** (16 bytes), encrypt the body, then append its bytes to
   the open pack. When there is no open pack, open one: draw its **pack id**
   (16 bytes). When the open pack's data area is full it is closed, without
   padding, and the body goes on in a new pack.
3. After the last operation: if a pack is open and not full, draw the
   **padding** (data area minus bytes used) and close it.
4. Build the body: the pack table of the new tree, the entries in
   [canonical order](#canonical-order), and the graveyard = the previous
   graveyard, plus every pack of the previous pack table that the new tree
   does not use (with `died` = this generation), minus the packs this writer
   deleted. Draw the **seal id** (16 bytes) and seal.

All random bytes come from one source, drawn in exactly this order. A pack that
was written in this generation and ends up unused by it (a file written and
deleted again before the commit) is an orphan, not a graveyard entry.

The operations, as the vectors spell them:

| Operation | Effect |
|---|---|
| `mkdir path mtime` | a new folder |
| `write path mtime content` | a new file, or new contents for the file at `path`: a new content id, the new `mtime`, nothing kept of the old version but its place in the tree |
| `delete path` | removes a file, or a folder with everything under it |
| `move from to` | the entry (and, for a folder, everything under it) gets a new parent or name; `mtime` and contents are kept |

---

## Reading

**Finding the latest generation.**

- From a server: `GET .../state` names it; the index is fetched with the
  ordinary download (`GET /api/files/manager?action=download&path=…/v/idx/G.fxi`).
- From a copy (`filex decrypt ./Kasa`): list `v/idx/`, take the highest
  well-formed name.
- **A damaged latest** (it does not verify): wait 2 seconds and fetch it once
  more - a commit may be landing on a storage without an atomic rename. Still
  damaged: show the newest generation that verifies, **read-only**, and say so
  ("The newest state of this vault is damaged. You are looking at the one from
  10:42."). A writer does not write such a vault until its owner chooses
  **Continue from this state**, which commits latest + 1 with that
  generation's tree and graveyard; packs only the damaged generations used
  become orphans.
- **No index file and no pack**: an empty vault whose creation stopped half
  way, at generation 0; its first commit writes generation 1. **No index file
  but packs**: damaged; nothing can be shown and nothing is written.

**Rollback.** Within one session a client remembers the highest generation it
has seen of each vault; if the server later offers a lower one, the client says
so and stays read-only for that vault. Across sessions nothing detects it: a
server that deletes the newest index files serves an older state. That is
within the [threat model](E2E-ENCRYPTION.md#threat-model) - a server that
does that could as well serve hostile JavaScript.

**Reading while somebody writes.** Reading needs no lock and never waits for
one. A reader works on the generation it loaded and learns of a newer one from
the [realtime event](#audit-and-events), or by asking `state` every 30 seconds
while the vault is open and before it opens a file. A pack it needs may have
been deleted in the meantime: it loads the latest generation and tries once
more; if the file is gone or has changed, it says so.

---

## Garbage collection

Two kinds of dead bytes build up:

- the **graveyard**: packs the latest generation no longer uses, which an older
  generation that is still kept may use;
- **orphans**: packs no generation ever used - an interrupted write, a lost
  lock, a pack uploaded again under a new id.

**Retention.** Generation `g` is **expired** when `g ≤ latest - 3` **and**
generation `g + 1` was committed more than **15 minutes** ago (the
modification time of `v/idx/(g+1).fxi` in the listing, compared with the
listing's own `now` - the server's clock, never the client's; an index file
that is gone counts as long ago). The three newest generations, and every generation
replaced less than 15 minutes ago, are kept, so that a reader still working on
one finds its packs.

**What the lock holder deletes**, through [`POST .../delete`](#api) (for good:
no trash, no version):

1. the index files of expired generations;
2. graveyard packs whose last user, generation `died - 1`, is expired;
3. orphans: packs in the listing that are not in the latest pack table, not in
   the graveyard, and not uploaded by this writer since it took the lock.

It deletes nothing while the latest generation is damaged, while the latest
index has extension bytes or a version it does not write, or while there is no
index at all. A deleted pack leaves the graveyard in the writer's next commit;
if no commit follows, the next collection deletes it again and a missing file
counts as deleted.

**Repacking.** After a commit the writer works out, for each pack in the
latest table, its **live bytes** (the extents of the latest generation in it).
Let `S` be the packs with fewer live bytes than half a data area. It repacks
when all three hold:

- `S` has at least 2 packs;
- the live bytes of `S` fit in at least one pack fewer than `S` has
  (`⌈live(S) ÷ data area⌉ < |S|`);
- more than half of all the data areas in the pack table is dead.

It copies the live extents of `S` - entry by entry in index order, each
entry's extents in order, the bytes as they are, **nothing re-encrypted** -
into new packs by the [canonical layout](#canonical-layout), commits a
generation whose extents point there, and the packs of `S` go to the
graveyard.

**Who runs it.** Only the lock holder: the server cannot read the index, and a
reader holds no lock.

- after each commit, a pass of at most 1 000 deletions;
- right after taking the lock, a pass for what earlier sessions left;
- `filex vault prune`, a full pass and repack on demand.

---

## The write lock

This is the server's contract. Everything in it is enforced by the server;
the client's part is to follow it and to stop when told.

### Lock semantics

- **One session.** At most one session holds a vault's write lock: a browser
  tab, a desktop window, one `filex` run or one mount. The same person in a
  second tab is a second session.
- **Reading is free.** A held lock never blocks a reader, and reading never
  takes one.
- **Held while both are true:** the **lease** has not run out - 60 seconds,
  renewed by the holder's heartbeat every 15 seconds and by every vault
  write - and the session is **not idle**: it has made a vault write, or a
  renewal with `active: true`, within the holder's idle time.
- **Idle time** is the person's own setting: 1 to 10 minutes, **3 by
  default**, kept on the server for the person (not per browser, not per
  device; [`/prefs`](#api)). The server reads it when the lock is taken; the
  client never sends it.
- **Free again** at once when the lock ends - released, run out, idle or
  broken - **unless an index write of its holder is still running**. Then it is
  free when that write ends or 60 seconds after it began, whichever is first;
  the server abandons such a write at 60 seconds and never moves it into place
  afterwards.
- **Which vault.** The lock belongs to the key file's `vault.id`, within the
  tenant. Renaming or moving the vault folder keeps its lock. A copy of a vault
  made on the server has the same id and the same keys, and shares the lock
  with the original.
- **The token** is 32 random bytes, returned once when the lock is taken,
  stored only as its SHA-256. It travels in the `X-Filex-Vault-Lock` header of
  every vault write.
- **Stored in the database**, not in memory or a file: filex can run more than
  one process on one database ([DEPLOYMENT.md](DEPLOYMENT.md)). One row per
  tenant and vault id: token hash, holder (person, client, label), taken at,
  lease until, last activity, idle seconds, index write started at, first and
  last generation committed. Taking and renewing are compare-and-set updates.

### What the holder does

- **Heartbeat** every 15 seconds, with `active: true` while it is doing
  something that will write (an upload or a save being prepared).
- **Losing the lock** - a renewal or a write answers `VAULT_LOCK_LOST`, or no
  renewal has succeeded for 45 seconds:
  - stop at once: abort the uploads in flight, commit nothing;
  - keep the last committed generation on screen, read-only, and say why
    ("You did nothing for 3 minutes, so this vault went back to read-only."
    "Ayşe took over writing." "The connection was lost.");
  - list what was not saved. The session may take the lock again when it is
    free, load the latest generation and run the unfinished operations again
    on it, each one checked again (a name taken in the meantime is asked
    about; an item that is gone fails, and says so).
  - What it had uploaded is orphans; nothing half-written is visible.

### The idle lock

Two clocks, one after the other (decided by the owner, 2026-10-06):

1. **Write lock.** After the person's idle time (1-10 minutes, 3 by default)
   without a vault write or an `active: true` renewal, the server ends the
   write lock (`reason: idle`) and the client goes back to read-only, as
   above.
2. **Vault lock.** After a further **15 minutes** in which the open vault sees
   no activity at all - no listing, no opening, no download, no write - the
   **client** locks it: it drops the folder master key and every key derived
   from it from memory (the same as pressing **Lock**), closes what it shows
   of the vault and asks for the password, the recovery key or escrow again
   to reopen it. A vault that was opened and never written starts this clock
   when it is opened. The 15 minutes are fixed in format 1, not a setting.
   The server keeps no key, so this lock is the client's alone; nothing about
   it is stored or sent beyond an optional `vault.unlock` audit reason
   `locked_idle`, and that **only while the session still holds the write
   lock** when it locks the vault (see [`release`](#details-settled-by-the-first-test-runs-2026-10-07)).
   Since this clock runs only after the write lock has ended, a client that
   follows the two clocks holds no lock by then and sends nothing: the
   browser never sends `locked_idle`, and the audit row keeps the reason the
   write lock ended with (`idle`).

`filex vault mount` follows the same two clocks: its write lock ends after
the person's idle time, and after 15 minutes without any operation through
the mount it locks the vault, unmounts and exits.
- **Releasing**: after its last commit (and collection), `release`. When the
  page closes, the browser sends it with `fetch(..., {keepalive: true})`, best
  effort; the lease covers the rest.

### API

Under `/api/files/e2e/vault`, behind sign-in, the tenant confine and the CSRF
rules of the files API. `path` is the vault folder's wire path
(`docs://Kasa`). An error is `{"error": "<CODE>", "message": "...", ...}`,
`message` in the reader's language.

| Request | Answer |
|---|---|
| `POST /create` `{path, marker, index}` | `201 {generation: 1, owner_id, owner_name, owner_self}` |
| `GET /state?path=` | `200 {vault_id, pack_log2, generation, lock, owner_id, owner_name, owner_self}` |
| `GET /list?path=&kind=&after=&limit=` | `200 {items, next}` |
| `POST /lock` `{path, client, label}` | `200 {token, generation, lease_seconds, idle_seconds, expires_at}` · `409 VAULT_LOCKED` |
| `POST /lock/renew` `{path, active}` + token | `200 {expires_at, idle_until}` · `409 VAULT_LOCK_LOST` |
| `POST /lock/release` `{path}` + token | `204` |
| `POST /lock/break` `{path}` | `204` |
| `PUT /pack?path=&id=` body: the pack + token | `201` |
| `PUT /index?path=&generation=` body: the index file + token | `201 {generation}` |
| `POST /delete` `{path, packs, indexes}` + token | `200 {deleted: {packs, indexes}}` |
| `GET` / `PUT /prefs` `{idle_minutes}` | `200 {idle_minutes}` |

**Values.** Times are RFC 3339 strings in UTC (`2026-10-06T19:00:00Z`);
`vault_id` is the key file's id, base64url; `token` is an opaque string;
`next` is a string or `null`. `POST /delete` takes `packs` as an array of
32-hex pack ids and `indexes` as an array of generation numbers, and answers
`deleted` with **counts**: `{packs: 3, indexes: 1}`. Every `list` answer also
carries `now`, the server's clock, which is what [retention](#garbage-collection)
is measured against - never the client's.

- **`create`** makes a new vault. `marker` is the key file (a JSON object);
  `index` is the base64 of generation 1's file. The server checks: `path` does
  not exist, or is an empty folder (`409 VAULT_EXISTS` otherwise); it is not
  inside an encrypted folder; the [encryption rule](E2E-ENCRYPTION.md#who-may-encrypt)
  allows a **new encrypted folder** there (and spends a `new_folder` approval
  under the `approval` policy); `marker` is a vault key file by the rules
  [above](#the-key-file), with `vault.pack` `22` or `24`; `index` is a
  65 536-byte index file of generation 1 (header only: the server cannot open
  it). It writes the folder, the key file and the index, in that order, and
  removes what it wrote when a step fails.
- **`state`**: `generation` is the latest (0 when there is no index); `lock` is
  `null` or `{holder: {name, client, label}, since, expires_at, mine}`.
  `owner_id`, `owner_name` and `owner_self` say who owns the vault folder, in
  the keys a listing row carries (each absent when there is nothing to say;
  `create` answers them too, for the tab that opens the vault it has just
  made). The index records no author and the server knows no file in the
  vault, so every row inside it is shown as the vault folder's. The
  server keeps the latest generation of each vault cached and lists
  `v/idx/` when it does not have it.
- **`list`**: `kind=index` answers `{generation, size, mtime}` per file,
  `kind=pack` `{id, size, mtime}`, in name order, at most `limit` (default
  1 000, at most 10 000) after `after`; `next` is the cursor of the next page,
  or `null`.
- **`lock`**: `client` is `web`, `desktop`, `cli` or `mount`; `label` is a
  short free text ("Firefox, ofis"). `409 VAULT_LOCKED` carries `holder`,
  `since` and `retry_after` (seconds). `400 NOT_A_VAULT` when the key file at
  `path` is not a vault's.
- **`renew`**: `409 VAULT_LOCK_LOST` carries `reason`: `expired`, `idle`,
  `broken`, `released` or `taken`; with `taken` or `broken` it also carries
  `holder` (`{name, client, label}` of whoever holds it now, or broke it), so
  the client can say who.
- **`release`** answers `204` whether or not the token still held the lock.
- **`break`** ends someone else's lock: the vault folder's owner or an
  administrator of the tenant. The holder's next call gets `VAULT_LOCK_LOST`
  `broken`.
- **`pack`**: `id` is 32 lower-case hex digits. The body must be exactly
  2^`vault.pack` bytes with the [header](#packs) of that id and size
  (`400 VAULT_BAD_OBJECT` otherwise). A pack is created, never replaced:
  `409 VAULT_PACK_EXISTS` when the id is taken. This route takes a body of up
  to 16 MiB, and `index` one of up to 64 MiB, whatever the server's limit for
  other JSON or form requests.
- **`index`**: `generation` must be latest + 1 (`409 VAULT_GENERATION`, with
  `latest`, otherwise). The body's header must be an index header of that
  generation, and its size a Padmé size from 65 536 to 64 MiB
  (`400 VAULT_BAD_OBJECT`). The server writes `v/idx/.tmp-<random>` and renames
  it into place (one PUT where the storage cannot rename), abandons the write
  after 60 seconds, updates its cache and sends the event. It removes
  `v/idx/.tmp-*` files older than an hour that it finds on the way.
- **`delete`**: at most 1 000 names; only well-formed pack ids and generations;
  **never one of the three newest generations** (`400 VAULT_KEEP`). The files
  are deleted for good - no trash, no version - and a missing one counts as
  deleted.
- **`prefs`**: `idle_minutes`, an integer from 1 to 10 (`400` otherwise);
  `3` until the person sets it.

**Reads use the existing download**: `GET /api/files/manager?action=download&path=docs://Kasa/v/p/b0/b067….fxp`
with `Range: bytes=a-b` answers `206` with those bytes; an index file is
fetched whole the same way; the key file through the existing preview read.
Whoever may download from the folder may read these; they are ciphertext.

### Details the implementations settled (2026-10-06)

These were left open above and are now part of the format; the server, the
browser and the command line agree on each.

- **`state.lock.mine`** is `true` only when the request carries the lock's
  token in `X-Filex-Vault-Lock` - a session, not a person: the same person in
  another tab sees `mine: false`.
- **`list.now`**: every page carries it; a client takes the first page's.
- **`break`** takes an optional `{client, label}` of whoever breaks it, so the
  holder's `VAULT_LOCK_LOST` `broken` can say who; without them only the name
  is known.
- **`release`** takes an optional `{reason: "locked_idle"}`, sent when the
  client [locks the vault itself](#the-idle-lock) while it still holds the
  write lock ([below](#details-settled-by-the-first-test-runs-2026-10-07)).
  The server records `locked_idle` when the token still holds the lock, and
  `idle` when the lock had already ended (a `state` read in between may have
  closed it).
- **Where a vault may not be made**: inside an encrypted folder
  (`409 VAULT_NESTED`), and at a storage's root (`400 VAULT_BAD_REQUEST`).
- **Errors not in the table above**: `404 VAULT_DISABLED` (the switch is
  off), `400 VAULT_BAD_REQUEST` with a `detail` (a malformed request, `prefs`
  out of range, a root), `503 VAULT_TIMEOUT` (an index write let go after 60
  seconds), `503 VAULT_BUSY` (the lock's compare-and-set ran out of retries),
  `403 permission_denied` (a `break` by someone who may not).
- **The key-file rule** compares `v`, `req` and `vault` as canonical JSON:
  keys sorted, no white space.
- **The latest generation** may be cached for speed, but every decision -
  `state`, `lock`, `index` - lists `v/idx/` afresh.
- **Realtime**: the vault folder's own room gets
  `{type: "vault.generation", path, generation}` and
  `{type: "vault.lock", path, held, holder}`.
- **Repacking** moves only the extents in the set it repacks; every other
  extent keeps its place. A file whose extents end up side by side in one new
  pack keeps them as separate extents (no merging), so two writers of format
  1 lay a repack out alike.
- **"Continue from this state"** (a session that lost the lock and takes it
  again) is offered to whoever may write the vault, not only to its owner.

### Details settled by the first test runs (2026-10-07)

The three implementations were first built and tested together on
2026-10-06; these are what that settled, and they are part of the format too.

- **The listing says where the vaults are.** The explorer's listing
  (`GET /api/files/manager?action=index`, [BACKEND.md](BACKEND.md#file-browsing)) carries two fields
  on a server with vaults on, so a client knows a vault it never opened:
  - `e2e_vault: true` on a folder row that **is** a vault folder (beside the
    `e2e: true` every encrypted folder's row carries);
  - `e2e_vault_root`, on the listing itself, when the folder listed is a
    vault folder or inside one: the vault folder's wire path
    (`docs://Kasa`).

  What such a listing shows inside a vault is the vault's layout on the
  storage (`v/`, packs, index files), never a folder a person opens or
  chooses: a client shows none of it, lists an open vault from its index
  instead, and asks about a vault it has not opened as the vault folder - the
  server never hears a path below it. A folder chooser (move, copy, an app's)
  offers no vault as a destination for anything outside it, and only that
  vault for what is inside it.
- **An upload whose name is taken asks first.** A vault keeps no earlier
  version, so a client never replaces a file in a vault because an upload
  has its name. Before anything is written it asks, for each such file,
  whether it goes up under the next free name (`not (2).txt`, numbered as the
  New document dialog numbers) or not at all; the explorer asks in its
  "already there" dialog. A name taken after the question - by another
  writer's generation, loaded when the lock was taken - gets the next free
  number without asking again. This is the rule for an upload or a copy into a folder of
  the vault; a program writing through [`filex vault mount`](#filex-vault-mount)
  writes the name it opens, as on any disk, after whatever its operating
  system asked.
- **`release` with `reason: "locked_idle"`** is sent only by a session that
  still holds the write lock - a token the server has not ended - at the
  moment it [locks the vault itself](#the-idle-lock). The server records
  `locked_idle` when the lock's row still names that token (held, or run out
  with nobody having closed the row yet), and nothing otherwise: a row that
  was closed keeps the reason it ended with. A session without a token sends
  no `release` at all.
- **Repacking has vectors.** The [repack branch](#test-vectors) of the
  vectors (generations 4 to 6) holds both writers to one layout of a repack,
  byte for byte, and holds their planners to the same set `S`.

### Writes from anywhere else

- **Inside a vault, only the vault API writes.** Every other door - the
  explorer's file API, the operations queue, uploads, the agent API and MCP,
  ShareX, upload tickets, file requests, archives, apps, the document server's
  save, WebDAV, SFTP, FTPS, NFS, S3, `filex mount`, desktop folder sync -
  refuses to create, replace, rename, move, copy into or delete anything
  strictly inside a vault folder, `v/` and everything under it included. Each
  door answers with its own refusal: HTTP `403 VAULT_PATH`, WebDAV `403`, S3
  `AccessDenied`, SFTP `SSH_FX_PERMISSION_DENIED`, FTPS `550`, NFS
  `NFS3ERR_ACCES`, an MCP error result.
- The rule lives in `internal/writegate`, the one question every person-facing
  write asks, beside reserved names and app locks; the vault API is the one
  caller that claims the exception. writegate learns which folders are vaults
  from the server's **one** ACL resolver, the one every door shares: a door
  that builds a resolver of its own does not see the vaults and writes inside
  them (the document server's save did until 0.54, #94).
- **The key file** may be rewritten through its usual doors - the password
  change, the reset with the recovery key, an escrow slot added or declined
  work exactly as at levels 1 and 2 - as long as `v`, `req` and `vault` stay
  the same (compared as compact JSON); otherwise `409 VAULT_KEYFILE`. It is
  never deleted, renamed or moved on its own.
- **The vault folder itself** may be renamed, moved, deleted (to the trash,
  whole) and copied like any encrypted folder: the transfer guard and the
  [encryption rule](E2E-ENCRYPTION.md#what-counts-as-encrypting) apply as
  they do today, and a copy is a new encrypted folder where it lands.
- **Ciphertext everywhere.** No thumbnails, text extraction, previews or
  document editing for anything under a vault; the `filexvlt` magic joins
  `filexe2e` and `filexfxe` in the content sniff. No share links for a vault
  or anything in it.
- **Quotas** count packs and index files like any other bytes. A pack refused
  for the quota fails the change before its commit: nothing becomes visible.

### Permissions and tenancy

| Request | Needs, at the vault folder |
|---|---|
| `create` | `files.create` at the parent, and the [encryption rule](E2E-ENCRYPTION.md#who-may-encrypt): tenant ceiling, tenant policy, `files.encrypt` (and an approval, under `approval`) |
| `state`, `list`, reading packs and index files | the right to list the folder and to download from it (`files.download`) |
| `lock`, `renew`, `release`, `pack`, `index`, `delete` | `files.create`, `files.modify` **and** `files.delete`: a write session can add, change and remove anything inside, and the server cannot tell which. `files.encrypt` is not asked, as for adding a file to an encrypted folder |
| `break` | the vault folder's owner, or an administrator of its tenant |
| `prefs` | signed in |

- Access rules on paths **below** the vault folder have nothing to apply to:
  the server never sees the files. A vault is readable as a whole or not at
  all, writable as a whole or not at all.
- The agent surface (`/api/ai`, MCP) has no vault API: it holds no key.
- Every call is confined to the caller's tenant, and lock rows carry it.

### Audit and events

- **Audit rows:** `vault.create`; `vault.lock` when a lock is taken;
  `vault.unlock` when it ends, with the reason (`released`, `expired`, `idle`,
  `broken`) and the first and last generation committed in it;
  `vault.lock_break`, naming who broke it. There is no row per commit.
- **Realtime:** after a commit, `vault.generation {path, generation}`; when the
  lock changes, `vault.lock {path, held, holder}`; to everyone who can see the
  folder. Clients also ask `state` every 30 seconds while a vault is open.
- **Capability:** `GET /api/files/capabilities` answers `e2e_vault: true` on a
  server that has this API. A client offers level 3 only there.

---

## Clients

### Web and desktop

The vault is part of the explorer in `packages/core`: the web app, the desktop
app and every embed run the same code, and nothing about it is written for
one of them only. Unlocked, a vault opens **read-only**, its tree read from
the index. The first action that writes - an upload, a new folder, a rename, a
move inside the vault, a delete, a save - takes the lock; when somebody else
holds it the explorer says who and since when, and the vault stays readable.
Moving or copying something **into or out of** a vault is an upload or a
download through the browser, never a server-side move. A decrypted download
(one file, or a zip) uses the existing
[decrypted-download](E2E-ENCRYPTION.md#downloading-a-decrypted-copy) machinery.
Searching a vault searches its index, in the browser. An upload whose name is
taken is [asked about first](#details-settled-by-the-first-test-runs-2026-10-07),
and the listing's `e2e_vault` / `e2e_vault_root` tell the explorer where a
vault it has not opened is.

### filex decrypt

`filex decrypt` reads a vault like any encrypted folder: **a copy** of the
folder (or a zip of it), offline - the latest generation that verifies, the
whole tree, all or nothing - and also **a vault on a server**,
`filex decrypt docs://Kasa -o ./Kasa-plain`: `state`, the index and the byte
ranges it needs, over the API, with no lock. `--generation N` takes an older
generation that is still kept.

### filex vault mount

- `filex vault mount docs://Kasa [<mountpoint>]` unlocks the vault (the
  password on the terminal, `--password-stdin`, or `--recovery-key`), loads
  its latest generation, serves the tree from a **WebDAV server on 127.0.0.1**
  (a random port, and a random 128-bit path prefix as its only credential),
  and asks the operating system to mount it - `net use` on Windows,
  `mount_webdav` on macOS, `gio mount` or davfs2 on Linux - or prints the
  address when it cannot. No cgo, no FUSE.
- **Reading**: byte ranges, through a block cache.
- **Writing** takes the lock at the **first write**, not when mounting:
  mounting to look must not stop anybody else writing. When somebody else
  holds it, the write fails ("access denied") and the mount says who. From
  then on the session's ordinary rules apply: heartbeat, the person's idle
  time (after which the next write takes the lock again if it is free),
  collection after commits.
- **The one exception to "saved means committed":** an operating system writes file by file and waits for each, so
  the mount answers a write once its bytes are in the mount's spool, and
  commits when **no write has come for 2 seconds, or the oldest change waiting
  is 5 seconds old**, whichever comes first. A crash in that window loses
  those writes, as a disk's write cache would. The command's help says so.
- **15 minutes with no file system operation** at all: it commits, releases
  the lock, unmounts and exits. An operation is something a person or a
  program does to the vault's files: reading a file's bytes, writing,
  creating, renaming, moving or deleting a file or a folder. What the
  operating system does on its own to keep a window current - `PROPFIND`,
  `OPTIONS`, `HEAD`, a WebDAV `LOCK` refresh - is **not** one, or an open
  Finder or Explorer window would keep the mount open for ever (the owner's
  rule: "if no work is done on the mounted drive, it closes after 15
  minutes").
- The operating system's own litter (`.DS_Store`, `._*`, `Thumbs.db`,
  `desktop.ini`) is kept in the mount's memory and never written to the vault.
- While it does not hold the lock, it follows new generations (the realtime
  event, or `state` every 30 seconds).
- `filex vault prune docs://Kasa` takes the lock and runs a full
  [collection](#garbage-collection), repack included.

---

## Versions and later additions

| Where | Today | Changes it |
|---|---|---|
| `vault.v` in the key file | `1` | an incompatible change of packs or index: `2`, refused by format-1 readers before they read anything |
| pack and index header, byte 8 | `0x01` | the same |
| index body `version` | `1` | the same |
| index body `flags` | `0` | a required feature: a bit, refused by readers that do not know it. Bit 0: deduplication |
| content kind | `0`, `1` | `2` is reserved for deduplicated chunk lists |
| `ext` areas (body, entry) | empty | optional data; readers skip it, older writers stop writing |

**Deduplication later**, without breaking format 1: chunk keys from a keyed
hash of the content (per vault, never across vaults), chunks shared between
files, a flag bit and content kind `2` in the index; packs unchanged; the
collector counts references instead of extents. A format-1 reader refuses such
a vault by its flag.

---

## Test vectors

The vectors are written by one **reference implementation** that is neither
the browser code nor the Go code: `node:crypto`, which is OpenSSL. Both
implementations are tested against what it wrote, byte for byte, so a mistake
the two share still fails.

```bash
node backend/internal/e2edecrypt/testdata/gen_vault_vectors.mjs
```

Node 18 or later, no packages, deterministic. Everything lives beside the
other encryption vectors, in `backend/internal/e2edecrypt/testdata/`:

| File | What |
|---|---|
| `gen_vault_vectors.mjs` | the reference implementation and generator |
| `vault/v3-vault/` | a vault exactly as a storage holds it: the key file, generations 1 to 3, 18 packs of 2^16 bytes |
| `vault-vectors.json` | secrets, keys, every generation's operations and layout, hashes, the 4 MiB variant, the repack branch, layer vectors, body cases |

**The vector random source.** Every random byte comes from one DRBG: the
AES-256-CTR keystream with key `SHA-256(seed)` (the seed as UTF-8), initial
counter block 16 zero bytes, the whole block counting up as a 128-bit
big-endian integer (WebCrypto: `AES-CTR`, `length: 128`). Bytes are drawn in
the order of the [canonical layout](#canonical-layout). The seeds are
`filex vault vectors v1 marker` for the key file and
`filex vault vectors v1 generation N` for generation `N`. `layers.drbg` holds
the first 64 bytes of one seed.

**The vault.** Pack size 2^16 (64 KiB; readers accept it, writers never
create it) keeps the repository small. The same operations at the product
size, 2^22, are in `canonical_4mib`, as hashes only.

| Generation | Operations | Result |
|---|---|---|
| 1 | (creation) | an empty tree |
| 2 | `mkdir Belgeler`, `mkdir Belgeler/Arşiv`, `write not.txt` (33 bytes), `write Belgeler/boş.txt` (0 bytes), `write Belgeler/Arşiv/büyük.bin` (1 MiB + 1 byte) | 17 packs; `büyük.bin` is two STREAM chunks spread over all 17 |
| 3 | `delete Belgeler/Arşiv/büyük.bin`, `move Belgeler/boş.txt boş.txt`, `write not.txt` (new contents) | 1 new pack; the 17 packs of generation 2 in the graveyard, still on the storage (kept for generation 2) |

The password and the recovery key are in `secrets` (the key file uses 1 000
PBKDF2 iterations; readers accept it, writers never write it).

**The repack branch** (`repack` in `vault-vectors.json`). A [repack](#garbage-collection)
needs packs that have gone mostly dead, which the three generations above do
not leave, so a branch of the same vault goes on from generation 3, with
the seeds `filex vault vectors v1 repack generation N`, packs of 2^16, and
hashes and layouts only (nothing of it is in the fixture folder):

| Generation | Operations | Result |
|---|---|---|
| 4 | `write a.bin`, `write b.bin`, `write c.bin` (40 000 bytes each) | 2 packs; `b.bin` spans both |
| 5 | `delete a.bin`, `delete c.bin` | no pack; every pack of the table (generation 3's and both of 4's) is now less than half live, and the planner calls for a repack |
| 6 | the repack of that set `S` (the generation's `repack`, in table order) | 1 new pack: `b.bin`'s two extents and `not.txt`'s one, copied in index order, three extents side by side and none merged; the three packs of `S` in the graveyard with `died` 6 |

**Every implementation must pass**, against these files:

1. **Unlock** the fixture's key file with the password and with the recovery
   key; the FMK is `secrets.fmk`.
2. **Keys**: the index key of every generation and the content key of every
   file equal the vectors' `index_key` and `content_key`.
3. **Index**: each `v/idx/G.fxi` decrypts to `body_hex` followed by zeros, and
   decodes to `tree`, `pack_table` and `graveyard`.
4. **Contents**: every file of generations 2 and 3 reads back to its `sha256`
   (and `text`), whole and by ranges that cross a chunk and a pack boundary
   (for example bytes 65 400 to 65 600 and 1 048 570 to 1 048 577 of
   `büyük.bin`).
5. **Writing**: replaying the operations of generations 1 to 3 with the DRBG
   writes every pack and index file byte for byte (`sha256`); the same at
   2^22 against `canonical_4mib`.
6. **Body cases** (`body_cases`): each parses as `valid` says, and
   `ext_is_skipped` leaves the vault read-only for a writer.
7. **Layers**: `uvarint`, `padme_index_size`, `stream_size`, `name_order`,
   `drbg`.
8. **Negative cases** (`negative`): each mutation of a copy of the fixture
   gives the stated result. An offset in a recipe counts from the start of
   the pack **file**: "offset N of an extent" is the byte at
   `extent.offset + N` of that extent's pack file.
9. **Limits**: a writer refuses the 250 001st entry and warns from the
   200 000th; a reader refuses an index file over 64 MiB before decrypting it.
10. **Repacking**: from generation 3, the repack branch's generations 4 to 6
    write every pack and index file byte for byte (`sha256`) and draw
    `drbg_bytes_used` random bytes each; after generation 5 the planner
    picks exactly `repack` as `S`, and after generation 6 it picks nothing;
    every file of generation 6 reads back to its `sha256`.

When the format changes: change this page, then the generator, then run it,
then the implementations.
