# Editing encrypted office documents

> **Status: designed, with a protocol prototype. Nothing in filex offers it
> yet.** Today a document in an encrypted folder opens read-only and
> ONLYOFFICE is not offered for it ([E2E-ENCRYPTION.md, feature
> trade-offs](E2E-ENCRYPTION.md#feature-trade-offs)). This page is the design
> of editing it - alone or with other people, in the browser, without any
> server ever reading it - and what of it exists.

The editor's side is **an app**, `filex-office-editor`, in a repository of its
own ([BRF-Tech/filex-office-editor](https://github.com/BRF-Tech/filex-office-editor),
AGPL-3.0-or-later, see [licensing](#licensing)): an administrator
installs it like any other [app](APP-PLUGINS.md), it has its own version and
releases, and it is never built into filex. It needs no Document Server, so
an instance without one can install it to edit office documents in the
browser - in encrypted folders and, if wanted, in ordinary ones. filex keeps
its half of the protocol (the relay, the keys, who saves) and the platform
features the app runs on ([What filex gives the app](#what-filex-gives-the-app)),
all MIT.

| Piece | Where | Status |
|---|---|---|
| The relay: one order for the editors' sealed entries, the changes lease, who is in a session, which part of the log a save holds | `backend/internal/e2eoffice` | Prototype: in memory, one process, no route reaches it |
| Session keys, sealed entries, the reader that refuses a log the editors did not write | `packages/core/src/lib/e2eoffice.ts` | Prototype: no screen uses it |
| Who saves, and when | `packages/core/src/lib/e2eofficeSave.ts` | Prototype: no screen uses it |
| The bridge (a Document Server for one editor, in the browser), the socket.io stand-in, the x2t driver | the `filex-office-editor` app (AGPL-3.0-or-later) | Prototype: unit tests, no build, no release yet |
| The app's page and editor frame, ONLYOFFICE's editor files in its package, the x2t WebAssembly build | the `filex-office-editor` app | Not built |
| The platform features the app needs, the relay's routes and database tables, "Edit (encrypted)" in the explorer | filex | Not built |

## The idea

ONLYOFFICE's editor is a browser program. A Document Server gives it two
things: its files, and a socket.io connection over which the editor sends
its changes, asks for locks and is told about the other people. The
document itself is converted by the Document Server (x2t) into the editor's
own format before the editor opens it, and back when it is saved.

In an encrypted folder none of that may happen on a server. So the editor
runs in a frame of its own, from ONLYOFFICE's own files, unchanged except one:
the socket.io client is replaced by a stand-in (the app's
`shim.ts`) whose "server" is a **bridge** in the same frame
(`bridge.ts`). The bridge answers every message the way a Document Server
does (Docs 9.4, `DocsCoServer.js`), but whatever has to reach the other
people - a batch of changes, a lock request, a released lock - it hands to
the filex page, which seals it and sends it to filex's **relay**. The relay
puts the sealed entries in one order without reading them, and every
editor's bridge applies the same entries in the same order. The conversion
runs in the browser too: x2t compiled to WebAssembly (`x2t.ts` drives it).

This is the model CryptPad uses for its office documents, measured on a
Document Server 9.4 before it was designed: the editor's unchanged files,
with only the socket.io file replaced, opened a document converted in the
browser, two tabs edited it together and ended with byte-identical
documents, and the result went back to docx with its formatting and its
Turkish characters - while the Document Server served the editor's files and
received no document, no change, no image and no connection.
Two things are deliberately different from CryptPad:

- **Lock and save rules are the Document Server's, not a stand-in's.**
  CryptPad's bridge always answers "nobody is saving" and lets two people
  believe they hold the same paragraph. Here the relay grants a **lease**
  for writing changes only to an editor that has received every change
  already in the log, and lock requests go through the log, so every bridge
  runs the server's lock rules on the same sequence: the first request for a
  block wins everywhere.
- **The file stays a docx, xlsx or pptx.** The session's log is temporary;
  saving writes a normal encrypted file version.

```
filex page (keys, network)            the app's frame (filex-office-editor)
+---------------------------+  Port   +-------------------------------+
| seals and opens entries   |<------->| bridge (the "Document Server")|
| relay connection          |         |   ^ socket.io stand-in        |
| saves the file            |         | ONLYOFFICE editor, unchanged  |
+-------------+-------------+         | x2t (WebAssembly)             |
              | sealed only           +-------------------------------+
              v
+---------------------------+
| filex relay               |   the Document Server sees nothing:
| order, lease, members,    |   no document, no change, no image,
| sealed log, sealed blobs  |   no conversion
+---------------------------+
```

The frame never holds a key and never touches the network; the filex page
never runs the editor's code. They meet over the message channel filex sets
up with every app's interface
([APP-PLUGINS.md → An app's own interface](APP-PLUGINS.md#an-apps-own-interface)),
in the sandboxed frame filex serves the app's package in.

## Keys

A session has its own random 32-byte key. The folder key seals it for the
server to keep, the way a file's own key is sealed in its header: the
folder's key file does not change, and when a session ends its key goes with
it. From the session key, HKDF-SHA-256 (salted with the session id) derives
three keys, one per use: one for the log's entries, one for cursor frames,
one for the base document and its images. None of them can be read back out
of the browser.

Every sealed thing names what it is in its authenticated data (AES-256-GCM),
so nothing sealed for one purpose opens for another:

| Sealed | Authenticated with it |
|---|---|
| The session key | the session and the file |
| A log entry | the session, its place in the log, its writer, the writer's counter, its kind |
| A cursor frame | the session, its writer, its counter |
| The base document, an image | the session and the blob's name |

## One order

A member writes an entry against the last place it has seen; the relay puts
it right after that place or refuses it, and the writer catches up and seals
it again for the new place. Because the place is part of what is sealed, the
order the relay hands out is the order the writers sealed. Each entry also
carries the digest of the sealed entry before it. A reader
(`OfficeLogReader`) takes entries one by one and refuses the first one that
is missing, moved, altered, replayed or of an unknown kind - and everything
after it: editors that read different logs would build different documents,
so the editor stops instead.

The relay writes three kinds of entries itself, in plain: a member joined
(with the editor's per-session user index, which is never given twice), a
member left, a save happened.

## The lease

A Document Server lets one editor save changes at a time, and only one that
has received every change before (its `isSaveLock`). The relay keeps that
promise with a lease: changes are accepted only from the member holding it,
and it is given only to a member whose bridge has applied every change in
the log. Each accepted batch renews it; it ends when the editor's last batch
is in, when its holder leaves, or after 60 seconds without a batch.

## Locks

A lock request is an entry like any other. Every bridge applies the Document
Server's rules (text, spreadsheet, presentation, and the spreadsheet's lock
recalculation after rows or columns are inserted or deleted) to the same
requests in the same order, so all of them reach the same table without a
server that can read which paragraph or cell is locked. The rules are kept as
Docs 9.4 has them, quirks included, because the editor was written against
them.

## Saving

The server has no key, so a browser saves: the bridge's editor gives its
document, x2t turns it back into a docx, xlsx or pptx, the filex page
encrypts it as a new version and writes it only if the file is still the one
the session started from (the same rule as
[a document that changed while it was open](ONLYOFFICE.md#when-the-document-changes-while-it-is-open)),
then records the save in the log. Every browser derives the same answer to
"who saves now" from the same log:

- while there are unsaved changes, **a version every 10 minutes**, counted
  from the first change after the last save, by the person online who may
  write and joined first;
- **Save** (the editor's button, Ctrl+S) saves at once, by whoever pressed it;
- **the last person who may write saves when leaving**;
- unsaved changes nobody saved **stay in the session for 30 days**: whoever
  opens the document next joins the same session, sees them and can save
  them.

## In a vault

In a level 3 vault the editor runs **alone**: no relay, no session on the
server, nothing leaves the browser until the save. A vault promises not to
tell the server which document changes when; a co-editing session would tell
it that somebody is editing one, and how much.

## What the server sees

The relay cannot read an entry, a cursor, the base document or an image. It
does see:

- which file has a session, who joined and left it and when, and whether each
  may write;
- how many entries of each kind (changes, lock requests, releases) were
  written, how big they are and when - not which paragraph, cell or slide
  they concern;
- how often cursors move;
- the size of the base document and of each image;
- when a save happened and which part of the log it holds.

A member without the right to write (filex's own rules: an editor role and
`files.modify` on the file) reads along and writes nothing: the relay refuses
its entries, its lease and its saves. That is a server rule, not a
cryptographic one: everyone in a session holds the same key. Who a member is
and whether it may write are the server's answer - the account signed in on
the request and the file's rules, asked by the relay itself when somebody
joins - never something the joining connection says about itself.

## What filex gives the app

An app's interface runs in a sandbox that is closed on purpose
([APP-PLUGINS.md → An app's own interface](APP-PLUGINS.md#an-apps-own-interface)).
The editor needs a few doors in it. Each is a general platform feature -
any app may ask for it, and the administrator sees it in the install review
like every other permission - and each is MIT, in filex:

| The editor needs | Today | Planned |
|---|---|---|
| A frame of its own: ONLYOFFICE's `DocsAPI.DocEditor` opens the editor page in a frame | `frame-src 'none'` | frames from the app's own package only, in the same sandbox (a permission) |
| To load the document from a `blob:` address | `connect-src 'none'`, or the package | `blob:` in `connect-src` (a permission; a `blob:` address never leaves the page) |
| The plaintext of a document in an unlocked encrypted folder | an app is never given a file there | the explorer decrypts and hands the bytes over, and encrypts the save as a new version written only if the file is still the one the editor opened; a permission of its own, with a stern warning in the review: the app sees every document opened with it in an encrypted folder |
| Editing together | - | the relay's routes and WebSocket, its tables and blob store, and a bridge method to append to and read the session's log |
| A package of about 80-95 MB zipped (1,400 files, the largest 38.8 MB) | within the limits (128 MiB zipped, 20 000 files, 64 MiB a file), served uncompressed | the package's files served compressed and cached by version, so a first opening downloads about 15-20 MB, not 80-90 MB |

Two more things the editor expects - `localStorage`, which an opaque frame
does not have, and inline scripts, which the package's policy refuses - the
app handles itself (an in-memory stand-in, and a build that moves the
scripts into files).

## Licensing

filex is MIT. The bridge carries the Document Server's lock rules over from
its source and the stand-in runs inside the ONLYOFFICE editor, both AGPL-3.0,
so they live in the `filex-office-editor` app, a repository of its own under
**AGPL-3.0-or-later** (its lock rules, a modified version of ONLYOFFICE
Docs, under AGPL-3.0-only, as ONLYOFFICE licenses them). The filex core, the
server and the filex page talk to it only through messages, and it is
installed as an app, never built into the filex binary or image. The app
carries ONLYOFFICE's editor files as ONLYOFFICE ships them in its official
Document Server image (one release, pinned by the image's digest), with
their source named, the editor's "About" left as it is, and the legal
notice ONLYOFFICE's terms ask for. The app's name says what it is and
ONLYOFFICE only what it is based on: "ONLYOFFICE" is Ascensio System SIA's
trademark. The platform features above are filex's and stay MIT.

## What is not built yet

In the app (`filex-office-editor`):

- The editor's files in the app's package: ONLYOFFICE's editor taken from
  the official Document Server image at a pinned release (without help
  pages, dictionaries and the editors it does not need), the stand-in in
  place of socket.io, inline scripts moved into files, so an instance
  without a Document Server needs nothing else. The app checks every week
  whether ONLYOFFICE has published a newer release.
- The x2t WebAssembly build from ONLYOFFICE core at the same version as the
  editor files, and its comparison with the Document Server's own x2t.
- The app's page: the editor frame, the protocol over the app's message
  channel, saving.
- Images added while editing, printing and PDF, the editor asking for images
  by address: the bridge says it does not do them yet rather than answer
  wrongly.

In filex:

- The platform features in [What filex gives the app](#what-filex-gives-the-app).
- The relay's routes and WebSocket, its database tables (sessions and log,
  the lease as a compare-and-set row) and the blob store inside the encrypted
  folder; until then a restart forgets every session.
- The explorer offering the app in an unlocked encrypted folder, the
  unsaved-session sign on a file, and the measurements: spreadsheets and
  presentations in a real editor, large documents, Firefox and Safari,
  phones.

## See also

- [E2E-ENCRYPTION.md](E2E-ENCRYPTION.md) - encrypted folders, their keys and
  what the server knows about them
- [E2E-ROADMAP.md](E2E-ROADMAP.md) - the vault level
- [ONLYOFFICE.md](ONLYOFFICE.md) - ONLYOFFICE for files that are not encrypted
- [APP-PLUGINS.md](APP-PLUGINS.md) - apps, their interfaces and the sandbox
  they run in
