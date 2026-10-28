# The Office editor app

The Office editor app,
[`filex-office-editor`](https://github.com/BRF-Tech/filex-office-editor),
is how filex edits Word, Excel and PowerPoint documents and their kin - and,
from its **0.4.0** (for filex 0.57), how filex draws their thumbnails and
converts them on the server. It needs no ONLYOFFICE Document Server: an
installation that has one can delete its container
([Moving from a Document Server](#moving-from-a-document-server)).

It is an [app](APP-PLUGINS.md) like any other: an administrator installs it,
reviews what it asks for, and approves every update. It is AGPL-3.0, based on
ONLYOFFICE Docs by Ascensio System SIA, and its source, build and
measurements are in its repository. This page says what it does in filex;
[ONLYOFFICE.md](ONLYOFFICE.md) is the reference for office editing as a
whole, with or without a Document Server.

## At a glance

| | Where it runs | What it does |
|---|---|---|
| **The editor** | in the person's browser, in a sandboxed frame filex draws | opens 45 kinds of office documents, edits them alone or with others, saves a new version, prints, downloads in other formats |
| **The office engine** (`engine.zip`, 0.4.0) | on the filex server, in a process of its own, with no network | draws office thumbnails and makes the office conversions apps ask for (the Convert app, MCP's `file_convert`) |

Both come from ONLYOFFICE's own code: the editor's files from the official
Document Server image, and x2t, ONLYOFFICE's converter, compiled to
WebAssembly by the project. On the server the editor's own page layout
(sdkjs) runs in a JavaScript engine compiled to WebAssembly too, so a
thumbnail or a PDF is laid out the way the editor lays it out.

## What it does

### In the browser

- **Opens 45 kinds** in place of the preview - Word: `.docx`, `.docm`,
  `.dotx`, `.dotm`, `.doc`, `.dot`, `.odt`, `.ott`, `.fodt`, `.rtf`, `.wps`,
  `.wpt`, `.stw`, `.sxw`, `.mht`, `.mhtml`; Excel: `.xlsx`, `.xlsm`, `.xltx`,
  `.xltm`, `.xls`, `.xlt`, `.xlsb`, `.ods`, `.ots`, `.fods`, `.csv`, `.et`,
  `.ett`, `.sxc`; PowerPoint: `.pptx`, `.pptm`, `.potx`, `.potm`, `.ppsx`,
  `.ppsm`, `.ppt`, `.pot`, `.pps`, `.odp`, `.otp`, `.fodp`, `.dps`, `.dpt`,
  `.sxi`. Before 0.4.0 it opened six of them (`.docx`, `.xlsx`, `.pptx`,
  `.odt`, `.ods`, `.odp`). A person who may only read a file opens it to
  read.
- **Saves a new version.** **Save** (the editor's button, Ctrl+S, or filex's
  Save) writes the file in its own format as a new version; while there are
  changes it also saves every ten minutes, and filex asks before a person
  leaves unsaved work.
- **A format it cannot write is saved beside the file.** `.doc`, `.xls`,
  `.ppt`, their older templates, the WPS and StarOffice kinds, `.mht` and the
  flat OpenDocument kinds are saved as `.docx`, `.xlsx` or `.pptx` next to
  the original, which stays as it was (`report.doc` → `report.docx`, then
  `report (2).docx`); the editor says where the save went
  ([A save in another format](ONLYOFFICE.md#a-save-in-another-format)).
- **A conflict copy, never a lost change.** A file somebody changed after it
  was opened keeps their version; the save goes beside it as
  `<name>.filex-conflict-<time>.<ext>`
  ([When the document changes while it is open](ONLYOFFICE.md#when-the-document-changes-while-it-is-open)).
- **CSV in its own dialect.** A `.csv` opens with its own delimiter and
  encoding and is saved the same way - delimiter, byte order mark and line
  ends as the file had them, the cells nobody changed byte for byte
  ([CSV files](ONLYOFFICE.md#csv-files)). *Download as* CSV offers every code
  page of the editor's list.
- **Password-protected documents** open after the editor asks for the
  password, and stay protected when they are saved. The password never
  leaves the person's browser.
- **Spelling and hyphenation** in filex's interface languages (English,
  Turkish, German, Spanish, French, Arabic) out of the box; an administrator
  adds any of 43 more with one click, each file checked against the
  release's SHA-256
  ([Dictionaries for the office editor](APP-PLUGINS.md#dictionaries-for-the-office-editor)).
- **Macros** run after the editor asks whether they should, as a Document
  Server runs them.
- **Editing together.** Several people edit one document at once through
  filex's own relay; a person who may only read follows the changes live
  ([Editing together](APP-PLUGINS-API.md#editing-together-coedit-056)).
  Comments and tracked changes carry each person's name.
- **Encrypted folders.** A document in an encrypted folder or vault the
  person has unlocked opens too: the browser decrypts it for the editor and
  encrypts the save before it leaves, so the server sees only encrypted data
  ([E2E-OFFICE.md](E2E-OFFICE.md)).
- **Print and *Download as*.** Through filex: DOCX, ODT, RTF, TXT, XLSX, ODS,
  CSV, PPTX, ODP, their templates, PDF and PDF/A, made in the browser from
  the editor's own pages.
- **New document** gets six rows: a document, a spreadsheet and a
  presentation, as OOXML and as OpenDocument
  ([Creating new documents](ONLYOFFICE.md#creating-new-documents)).
- **The editor's language** is the person's, or the one an administrator
  fixed for everybody ([The editor's language](ONLYOFFICE.md#the-editors-language));
  zoom, units and the other editor settings are kept between openings.
- **On a phone** a document opens in ONLYOFFICE's phone view to read, with
  **Edit**.

### On the server: the office engine

From 0.4.0 the app's release carries `engine.zip`: x2t built for WASI,
`layout-host.wasm` (sdkjs in QuickJS) and the layout's scripts, each named in
the app's manifest by its SHA-256 (`engines.office`; the install review lists
it as `office-engine`). filex runs it in a process of its own - `filex
office-engine`, a hidden command of its own binary - and never links it into
the server
([The office engine without a Document Server](ONLYOFFICE.md#the-office-engine-without-a-document-server)).

- **Office thumbnails.** The picture of the first page, 320 pixels on its
  long side, at a Document Server's sizes: a letter-sized document 247 x 320,
  a spreadsheet 320 x 320 in filex's sheet layout, a 4:3 presentation
  320 x 240 ([Thumbnails](ONLYOFFICE.md#thumbnails)).
- **Office conversions for apps.** The Convert app's office targets (Word,
  Excel, PowerPoint and OpenDocument to each other, PDF and PDF/A, HTML,
  EPUB) and MCP's `file_convert`, with no change to the apps
  ([Office conversions for apps](ONLYOFFICE.md#office-conversions-for-apps-the-office-engine)).
- **No network.** The engine sees the job's own directory and the app's
  package; a picture a document only links to is left out, never fetched.
- **Prepared once.** After an install, an update, or a new filex, the engine
  is compiled in the background - a minute or two on a machine with 8 to 12
  cores, up to five minutes on a slower one - and later processes load it
  from the cache in `<data dir>/office-engine/` in about a second. **Admin →
  Plugins → Default apps → Office engine** says *Being prepared*, then
  *Ready* ([Being prepared](ONLYOFFICE.md#being-prepared)).
- **Memory.** Each module is capped by `FILEX_OFFICE_ENGINE_MAX_MB` (2048 by
  default); an engine process stays far below it for ordinary documents
  ([Memory](ONLYOFFICE.md#memory)).

## Install it

An administrator, once; filex never installs it by itself.

- **From a store.** On [filex Apps](https://apps.filex.sh) press **Install**
  on *Office editor* and give your filex's address: the install review opens
  on your filex ([Installing from a store](APP-PLUGINS.md#installing-from-a-store)).
- **From GitHub.** *Admin → Plugins → Apps → Install an app → GitHub
  repository*: `BRF-Tech/filex-office-editor`, tag `v0.4.0`
  ([From GitHub, step by step](APP-PLUGINS.md#from-github-step-by-step)).
- **From its files**, on a server that cannot reach GitHub: download
  `filex-app.json`, `ui.zip` and `engine.zip` from the release, then *Install
  an app → Upload files* with the manifest, **Interface** `ui.zip` and
  **Office engine** `engine.zip` (no module).

Read the permission review and press **Install**. The review lists the file
it was opened with (`files:read`, `files:write`), the documents of encrypted
folders (`files:e2e-plaintext`), editing together (`files:co-edit`), what its
interface may do (its own package, frames of it, `blob:` addresses, the
script exceptions ONLYOFFICE's editor and x2t need, *Download as* and
*Print*), the 45 kinds it opens and the six New document rows - and
`office-engine`, the engine filex runs on the server.

Which release for which filex:

| filex | Office editor | What it adds |
|---|---|---|
| 0.57 or later | **0.4.0** | the office engine (thumbnails, conversions), 45 kinds, CSV, password-protected documents, dictionaries, macros, conflict copies, OpenDocument rows in New document |
| 0.56 | 0.3.1 (0.2.0, 0.3.0) | encrypted folders (0.2.0), editing together (0.3.0) |
| 0.55 | 0.1.1 | the editor, one person, six kinds |

A filex refuses the manifests made for a later filex, so the store and the
review only offer what fits.

## Update it

Nothing updates by itself. When a new release is out, *Admin → Plugins →
Apps* marks the app **Needs approval**; **Review update** shows the
permissions the new version adds and the release notes. From 0.3.1 to 0.4.0
it adds `office-engine`, the 39 new kinds and the three OpenDocument rows,
and asks, kind by kind, where the app goes in **File types**: first, after
the ones that open the kind now, or off. ⚠ A kind another editor already
opens - a `.csv` a Document Server opens - keeps that editor first unless you
choose **First**; [Default apps](APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail)
changes it later. After the update the engine is prepared again (*Being
prepared*, then *Ready*); while it is, a connected Document Server takes its
jobs.

## Without a Document Server

There is nothing to configure: no address, no secret, no callback. Install
the app; once its engine is *Ready*, office documents get their thumbnails
and the Convert app its office targets. An installation that never had a
Document Server needs nothing else.

## Moving from a Document Server

An installation that used a Document Server for editing, thumbnails and
conversions can move every one of them to the app and stop the container:

1. **Install or update the app to 0.4.0** and wait until **Default apps →
   Office engine** says *Ready* for it. The Document Server keeps working
   meanwhile: by default the app's engine is asked first, the Document
   Server after it ([The engine and a Document Server](ONLYOFFICE.md#the-engine-and-a-document-server)).
2. **Put the app first** under **Default apps** for every kind the Document
   Server opened, `.csv` included. A rule that names the app first keeps it
   first even if a Document Server is connected again later.
3. **Remove the Document Server from the environment**:
   `FILEX_ONLYOFFICE_URL`, `FILEX_ONLYOFFICE_JWT` and
   `FILEX_ONLYOFFICE_FRAME_ORIGIN` (or the `external_services.onlyoffice`
   block of `config.yaml`), and restart filex.
4. ⚠ **Switch it off under *Admin → Settings → External services* too, and
   clear its address and secret.** The environment only gives the first
   value; the stored ONLYOFFICE row then belongs to the admin page
   ([Point filex at it](ONLYOFFICE.md#2-point-filex-at-it)). Removing the
   variables leaves that row as it was, and filex goes on sending the editor,
   the thumbnails and the conversions to the Document Server. Through the
   API: `PATCH /api/admin/external/onlyoffice` with
   `{"enabled": false, "url": "", "secret": ""}`.
5. **Check it.** `GET /api/capabilities` says `"onlyoffice_url": ""` and
   `external.onlyoffice.state` `disabled`; under **Default apps → Office
   engine** the app's engine is *Ready* and the Document Server is no longer
   connected. Open an office document, save it, and look at the thumbnail of
   one you upload.
6. **Stop the Document Server**, and remove what only it used: the proxy's
   site for its address, the `/filex-frame/*` route to filex on it, its
   secret.

The thumbnails the Document Server drew stay valid: both are the same
*ONLYOFFICE* drawer, so nothing is drawn again because the back end changed.

## Known limits

- **Fonts.** The app brings metric-compatible free fonts (Liberation,
  Carlito, Caladea) in place of the Microsoft fonts ONLYOFFICE's image
  installs: lines break in the same places, the glyphs look different. Fonts
  added to a Document Server are not used; if they matter, keep a Document
  Server and put it first under **Office engine**
  ([Where it differs from a Document Server](ONLYOFFICE.md#where-it-differs-from-a-document-server)).
- **Speed.** The engine is slower than a Document Server's native x2t: a
  conversion takes about 1.4 to 3 times as long, and a document's layout
  (a thumbnail, a PDF) 2 to 20 seconds.
- **Formats it does not write.** No FB2. *Download as* in the editor has no
  HTML or EPUB; the Convert app makes both through the engine.
- **Linked pictures** - a picture or a style sheet a document names on
  another server - are left out, in the editor, the thumbnails and the
  conversions alike.
- **ONLYOFFICE plugins** are off: most of them need the network, and none is
  in the package.
- **Large spreadsheets** need memory: a 200,000-row CSV took 2.6 GiB in the
  engine, above the default cap - raise `FILEX_OFFICE_ENGINE_MAX_MB` for
  such files.

## See also

- [ONLYOFFICE.md](ONLYOFFICE.md) - office editing with or without a Document
  Server, CSV, saves in another format, conflict copies, the editor's
  language, the engine in detail.
- [APP-PLUGINS.md](APP-PLUGINS.md) - installing, updating and removing apps,
  Default apps, dictionaries.
- [E2E-OFFICE.md](E2E-OFFICE.md) - editing documents in encrypted folders.
- [thumbnails.md](thumbnails.md) - how office thumbnails are drawn and repaired.
- The app's own [README](https://github.com/BRF-Tech/filex-office-editor#readme)
  and its [comparison with a Document Server, row by row](https://github.com/BRF-Tech/filex-office-editor/blob/main/docs/PARITY.md).
