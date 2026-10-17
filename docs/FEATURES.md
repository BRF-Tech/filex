# Features

Every feature of filex, one entry each: how it behaves, its limits and the guide
that documents it. The [README](../README.md) is the short tour, with pictures;
[Installation](INSTALLATION.md) gets a server running in a few minutes.

- [The explorer](#the-explorer)
- [Viewers, editors, search and thumbnails](#viewers-editors-search-and-thumbnails)
- [Storage and protocols](#storage-and-protocols)
- [Sharing and protection](#sharing-and-protection)
- [People and access](#people-and-access)
- [Desktop app, phone and CLI](#desktop-app-phone-and-cli)
- [Embedding](#embedding)
- [Apps](#apps)
- [AI agents and MCP](#ai-agents-and-mcp)
- [Running it](#running-it)

## The explorer

- **A browser client for your users, not just for you** - hand someone a `user` or
  `viewer` account and `…/drive` and they get the file manager itself: their storages,
  uploads, sharing, search, the editor. No admin panel to walk through, no separate
  frontend to deploy. `…/admin` is the operator's door to the same app.
- **The shell** - one layout, for the operator and the end user alike, in the admin app, the desktop app and every embed: a top bar spanning the full width with the collapse control and the product mark at its left edge, one **search field** whose ⌘K / Ctrl+K chip hands the query to the command palette (the field searches this folder; the palette is where "everywhere", saved searches and commands live), a primary **+ New** menu (upload files · new folder · **new document** · request files), a **Type · Owner · Modified · Size** filter row under the breadcrumb, **Folders** and **Files** as labelled sections in grid view, an info panel split into **Details** (with "People with access" and a share-link row) and **Activity** (version history and comments), and a **storage line** under the navigation. Theme, palette, language, density, the time zone, the start page, the keyboard shortcuts and the notification switches all live in **user settings**, reached from the avatar, in two parts that say where each is saved: **Account** (the same wherever you sign in - the web app and the desktop app keep your theme, palette, density and language there, not in the browser) and **This device** (the start page, one click or two, the keyboard shortcuts, this device's notifications); *Restart the tour* is in the avatar menu. Nothing is removed from the build - an embed, which has no settings dialog, keeps a "⋯" menu that still holds them ([docs/INTEGRATION.md](INTEGRATION.md)).
- **Home, inside the shell** - the landing view for everybody, admins included: your storages, what you opened last and what you starred, as cards in the content area with the same navigation panel and the same header as the files. Moving between Home and a folder changes the content and nothing else. An operator who would rather land on the admin dashboard chooses it in their profile settings.
- **Navigation panel** - the **+ New** menu as the primary action, the destinations Home / My files / Shared with me / **My shares** / Recent / Starred / **Drafts** / Trash, the storages you can see (one somebody shared with you simply appears there: one click, no mount instructions) - **in your own order** (drag a row, or Move up / Move down / Sort by name from its menu; kept on your account), else in the order the administrator set on the Storages page ([docs/STORAGE.md](STORAGE.md#ordering-storages)) - an **Apps** section when an installed app has a home screen, and **Connections** + **API keys**: the per-protocol guides and the self-service token manager, opened from inside the explorer so an embedded copy's users can mint the credential WebDAV/FTPS/`filex mount` ask for instead of asking an administrator. Collapsible to an icon rail (remembered per browser) from the top bar, a drawer instead of a column under 560px. On by default in the web app, the desktop app and every embed; `uiProfile: 'simple'` additionally turns off the tab strip, the split pane, the gallery view mode and the "Connections" surface without removing any of them from the build ([docs/INTEGRATION.md](INTEGRATION.md)).
- **A listing that behaves like a table** - resize a column, hide one, drag one to a new place; the table scrolls sideways rather than dropping a column when it runs out of room, and the actions column stays pinned to the right. Where there is no room for a table at all - a phone, the details panel - each row puts its other values on a line under its name, each with its column's name and " · " between them, the way a phone's file manager does, and every table in the product does the same. Sort by name, type, date or size, in either direction, and **the grid and the list obey the same sort**. Sorted by date, all three views group the rows under **Today · Yesterday · This week · This month** and then month by month, in **your** time zone rather than the browser's.
- **A folder remembers how you left it** - optional, from user settings: the view mode and the sort of each folder you actually set up, kept **per person on the server** so they follow you to another machine and to the desktop app, and never leak to anyone else looking at the same folder. Off by default, in which case your last choice simply applies everywhere ([docs/INTEGRATION.md](INTEGRATION.md)).
- **Who owns a file** - every node carries its owner, the listing has an **Owner** column and the filter row an **Owner** entry, and quota counts against the owner rather than whoever last touched the file.
- **Open the way each device expects** - with a **mouse**, a single click selects and a **double click opens** (Enter opens the selection) - the classic file-manager gesture, and a per-viewer preference (`ExplorerConfig.openTrigger`, default `'double'`; the desktop app, like the web app, exposes it as **User settings → Preferences → This device → Open files with**, and `'single'` restores one-click open). On a **touchscreen** a tap always opens - there is no hover-to-select. On every device the **checkbox** is the one click or tap that selects (Shift extends the range) and a right click or long press opens the menu; list rows, grid cards and gallery tiles all carry it.
- **Keyboard, and it says so** - every verb in the right-click menu and the toolbar prints the key that runs it, read from the registry so it follows a remap. Thirty-two actions are remappable from *Shortcut settings* (stored per browser); the handful of combinations a browser takes for itself, like `Ctrl+W`, are refused with a reason instead of stored as a key that would never fire.
- **Tabs, themes & deep links** - several folders open side by side, light/dark/auto theme, and an address bar that tracks the open folder so a pasted link lands there. Eight palettes ship in the theme gallery, each one a map of the `--fe-*` tokens rather than a second stylesheet, so a host page or an embed can pick one - or set its own values - without forking any CSS; an operator can add their own (see *Appearance*).
- **Appearance: your colours, everywhere** - the admin panel's **Appearance** screen composes named themes - twelve colours for light and for dark, a corner radius, a font stack - previewed as you type, and makes one the **instance default**. The text on a coloured button is chosen by contrast rather than assumed to be white, the rest of the palette is derived on the server, and the theme reaches the sign-in page and every public link - in its own tones, or in colours you give those two pages of their own - because branding that stops at the login is not branding: a signed-out page wears the instance default, never the palette of whoever last used that browser, and a signed-in person's own pick wins. A signature request from your instance carries your name, not filex's. Themes export and import as one JSON file. A **custom stylesheet** is the dangerous tool beside it, and it is off until you switch it on, never served to anyone who is not signed in, cannot fetch anything, and cannot reach the screen that turns it off ([docs/INTEGRATION.md](INTEGRATION.md#themes)).
- **One table, everywhere** - there is one table left in filex, the explorer's, and every other list is it: the admin panel's menus, **My shares**, an app's own screens. Each freezes its first column on the left and its actions on the right, resizes, reorders and sorts the same way, and ends each row in **one pinned Actions menu** holding everything that row can do - the same menu the explorer's ⋮ opens, so a second table cannot drift away from the first. An installed app with a home screen gets its own row under **Apps** in the panel's navigation. Lists follow the same rule: there is **no native dropdown** left, every choice is filex's own list (or a strip of buttons for two to four answers), drawn in your theme, in dark mode and right to left, with a phone's touch targets and the keyboard you expect ([No native dropdown](CONTRIBUTING.md#no-native-dropdown---one-list-control-in-core)).
- **Take a selection with you** - pick several files and folders and **Download** streams them as one archive, built on the fly: no temporary file is written into your storage, nothing is buffered in the tab, and a 700 MB archive costs the server under a megabyte of memory. **Move to** and **Copy to** open a folder chooser that spans every storage and refuses a destination you cannot write to - server-side, not just in the dialog.
- **New document** - create a Word, Excel, PowerPoint or OpenDocument file, or any text or code format, from the **+ New** menu: name it - any name, `LICENSE`, `Makefile` or `test.conf` included - choose where it goes, and it opens in the editor that handles it. The templates are real, minimal, valid documents compiled into the binary, so this works on an install with no office suite at all; a type this deployment could not then open is not offered in the first place, and the dialog says why - in the server's words, naming a family only when nothing here makes any of its kinds, so an office editor app's Word, Excel and PowerPoint rows stand without a word about a document server. A new document is a **draft** until its first save: nothing appears in the folder until you press Save (a name taken in the meantime is asked about - `report (2).txt`? - never replaced), closing it asks *Save to disk / Keep in Drafts / Discard*, and **Drafts** in the navigation panel keeps the ones you are not done with, visible to nobody else - 50 per person by default, set on the admin Protection page ([docs/ONLYOFFICE.md](ONLYOFFICE.md#drafts-nothing-is-in-the-folder-until-you-save)).
- **Archives in every common format** - make, open and extract ZIP, 7z, TAR and its
  gzip/bzip2/xz forms (RAR where the server's 7-Zip has it), with a password for ZIP and
  7z, as a background job with progress. Links and devices inside an archive are refused
  before anything is written, and size and entry limits stop an archive bomb at the
  limit ([docs/ARCHIVES.md](ARCHIVES.md)). Contributed by Alex (@ahjephson).
- **Tags, personal or your team's** - a tag is either **personal** - yours
  alone, never named to anybody else - or a **team** tag shared inside the
  tenant, which takes edit permission on the file to add or remove. A tag opens
  every file carrying it, from every folder they live in, and `tag:` narrows a
  search. Capitals are kept as typed.
- **Real-time collaboration** - presence bar with live avatars + focus (a profile picture set once on the account, shown for every client signed in as you), instant file-change updates over WebSocket in the native UI *and* in embedded contexts (short-lived ticket auth), polling fallback. One write is announced the moment it lands; a burst (a zip extraction, a folder upload, an NFS client writing chunk after chunk) is merged into one frame per window so the folder stays live without flooding the page ([docs/REALTIME.md](REALTIME.md)). Editing one file together is an app's, through filex's relay over a socket of its own ([Editing together](#apps)).
- **Notifications** - generic JSON webhooks (Slack/Discord-agnostic): any number of targets, each with its own signing secret and its own per-event subscription, plus an in-app bell with read/unread and a per-user mute matrix. The unread count is a **badge on the bell** - exact to 99, `99+` above, and on the desktop app's dock icon where the system has one - a row is clickable exactly when it has somewhere to go (a signature request opens the signing screen, not a notifications page), and **View all** opens every one of your notifications over the explorer, for everybody rather than only administrators. A write that **creates** a file and a write that **replaces** one are different events (`file.uploaded` / `file.updated`), and the ones an operator most wants on their own - an infected upload quarantined, a failed upload, an encrypted folder opened with its recovery key - are subscribable individually ([docs/NOTIFICATIONS.md](NOTIFICATIONS.md)). An optional **digest** - off out of the box - holds the kinds an administrator or a person turns off for a window of 1-15 minutes and tells them in one notification that says folder by folder what changed (*Reports: 30 files added*), so a busy folder is one badge step instead of thirty; every event still keeps its own row and reaches every webhook at once ([The digest](NOTIFICATIONS.md#the-digest)). **The server says every notification**: the bell, the desktop app's toast, a push and an email show the same sentence, in the reader's account language; a webhook is told in the language set for it and also gets the message untranslated for a receiver that translates for itself ([What a notification says](NOTIFICATIONS.md#what-a-notification-says)). **Web Push** (*Push notifications on this device*, in user settings) brings them to a phone or a browser with filex closed (on an iPhone or iPad, the web app added to the Home Screen, iOS 16.4 or later): the same kinds, mutes and digest as the bell ([Web Push](NOTIFICATIONS.md#web-push)).
- **i18n** - English + Turkish out of the box, **public links included**: a
  share link, a PIN gate, a file-request page or an app's signing screen
  renders in the visitor's language, and the public shell **offers a picker**,
  because a stranger's browser language is a guess and the person reading a
  contract should be able to correct it. The plain no-JS pages resolve
  `?lang=`, then `Accept-Language`, then the server default. **The text the
  server writes comes from the same catalogue** - mail, the notification
  phrases, the no-JavaScript pages and an install's permission review - each
  addressed to the reader it has always had, falling back to English per key,
  and a translation whose placeholders do not match the English is not used at
  run time, so a mail never loses its link or PIN. A signed-in person has one
  language, the account's: the screen and the notifications on every channel
  follow it - a language picked in the web app, the explorer or the desktop
  app changes it everywhere - and so does the ONLYOFFICE editor unless an
  administrator pins one.
- **Language packs** - any other language is an **app with nothing that runs**:
  a manifest of strings, installed from a GitHub repository, an upload or a URL
  like any other app, listed under **Plugins → Apps** with its coverage of the
  running version (*Español - 97% translated · the rest shows in English*). Its
  language joins every picker - the settings dialog, the admin header, public
  share pages - and translates the explorer, the admin panel and the public
  pages alike. Plural forms follow **CLDR categories**, so a pack writes `zero`,
  `one`, `two`, `few`, `many` and `other` where its language has them. Spanish,
  German and French ship as examples, and a template repository plus
  `scripts/i18n-export.mjs` / `i18n-validate.mjs` walk a translator from export
  to install - the validator holds a pack to the rules the built-in languages
  keep, a plain hyphen where a text would reach for a long dash among them
  ([write one](PLUGIN-KIT.md#writing-a-language-pack)).
- **Right-to-left layout** - Arabic, Hebrew, Persian, Urdu and any other
  right-to-left language lay the whole interface out right to left: the
  navigation panel, the tables, the drag-and-drop and column-resize geometry,
  the menus and the direction icons. What must **not** mirror does not - a PDF
  field editor works in document space, and a path, a command or any other
  machine text is isolated so it reads left to right inside a right-to-left
  sentence, including sentences the server wrote. The rule is enforced by a
  guard test: layout is written in logical CSS properties only
  ([docs/RTL.md](RTL.md)).

## Viewers, editors, search and thumbnails

- **Viewers & editors** - image/video/audio, PDF, Markdown (split editor + preview), CSV (ONLYOFFICE's spreadsheet when a Document Server is connected, or the office editor app's from its 0.4.0, where a save is written in the file's own delimiter, line ends and encoding and keeps the cells nobody changed as they were written ([docs/ONLYOFFICE.md](ONLYOFFICE.md#cells-nobody-changed-keep-their-text)); a read-only table otherwise), code (Monaco), Office documents in the [office editor app](#apps) - no Document Server needed - or through an optional ONLYOFFICE Document Server, Drawio + Mermaid diagrams (**Edit** on a Mermaid diagram opens its source in the text editor and saves it as text), 3D models. A document ONLYOFFICE can only save in a newer format (a `.doc` edited, saved as DOCX) is kept **beside** the original under the right extension, never written over it, and the people who edited it are told ([docs/ONLYOFFICE.md](ONLYOFFICE.md#a-save-in-another-format)). A document that changes while it is open in the editor - on the server or, in the desktop app, on disk - is never saved over: with nothing unsaved the editor loads the new version, with edits it asks *Keep the outside version* / *Write mine* / *Keep both* ([When the document changes while it is open](ONLYOFFICE.md#when-the-document-changes-while-it-is-open)). The editor's script can run on the Document Server's own origin instead of filex's pages (`FILEX_ONLYOFFICE_FRAME_ORIGIN`), out of reach of the signed-in session ([The editor in a frame of its own](ONLYOFFICE.md#the-editor-in-a-frame-of-its-own)). OnlyOffice's **Test now** fetches through the same door a document uses and warns when the document server does not enforce JWT, and after *Download failed* the editor says which of the two failures behind that message it was ([docs/ONLYOFFICE.md](ONLYOFFICE.md#failure-editor-shows-download-failed)). The editor opens in each person's own language, or in one the administrator picks for everybody on **External services → ONLYOFFICE** (`FILEX_ONLYOFFICE_LANG`) ([The editor's language](ONLYOFFICE.md#the-editors-language)). With more than one app or viewer for a kind of file, **Open with** and **Choose an app…** pick one, and *Always use this app* is kept on your account.
- **A picture from another server waits for you** - a file's preview (a Markdown file, a notebook's cells and HTML outputs, a Mermaid diagram - a `.mmd` file or a fence in Markdown - an EPUB book, an app's release notes on the update review) loads nothing from another server by itself, because loading a picture tells the server it is on that the file was opened, and from which IP address. Such a picture is drawn as a placeholder with its alt text and its server's name; clicking it asks in filex's own dialog, which names the server: **Load image** loads that one, **Load all remote images in this file** the others too, until the file is closed. **User settings → Preferences → Load remote images in previews** (off by default, kept on your account) loads them without asking. A loaded picture is fetched with no referrer. A Mermaid diagram is laid out in a frame of its own that fetches nothing, and its image nodes (`@{ img: ... }`) and label pictures are placeholders like a file's. An EPUB chapter is drawn in a frame that loads only the book's own files; the reader says when pictures were held, and **Load all remote images in this file** (one answer for the whole book) or the setting draws the chapter again with its pictures and style sheets from elsewhere, with no referrer (a url() in the chapter's own styles and a font from elsewhere never load). An office document opened in the Office editor app cannot reach another server either: the app's frame loads only the app's own files, and the office engine that draws thumbnails and converts on the server has no network ([ONLYOFFICE.md](ONLYOFFICE.md#how-it-runs)). Nothing else in a file reaches another server either: a `srcset` or `<picture>` source from elsewhere is dropped, and a style `url()`, `background`, `poster`, `<link>`, `<object>` and `<embed>` never survive. **Thumbnails never load them**, whatever the setting says: a Markdown file's thumbnail is its text, and the cards and folder previews draw no file's markup. A public share link draws no file's markup at all. The web interface, the desktop app and every embed behave the same.
- **Search** - Bleve embedded, full-text + metadata, permission-aware. VS Code-style filename scoring: folders count and word order does not (`main code` finds `Code/main.go`), separators and typos forgiven (`invoice 2026` finds `invoice_2026.pdf`, `mian.go` finds `main.go`) while numbers are matched literally (`2026` never means `2025`), `tag:` filters, exact matches ranked first. A search is narrowed on the server - by type, MIME type, date, size, folder and owner - before its limit counts, and the answer says how many hits there were ([Narrowing a search](SEARCH.md#narrowing-a-search)). A ⌘K result can be downloaded (a folder as one zip) or dragged out where it stands ([docs/SEARCH.md](SEARCH.md)).
- **Thumbnails you can read**: a PDF shows its **first page**, top-anchored so the title is in the card; a video its first frame that is not black (a clip shorter than a second included); an Office document its rendered first page; and a text, code or CSV file **fills the card with its own first lines** rather than repeating the extension the row already prints. image, video (ffmpeg), PDF (ghostscript), Office (ONLYOFFICE's converter: the office engine the [office editor app](#apps) brings from its 0.4.0, run on the server, or an ONLYOFFICE Document Server, when one is connected); capability-aware, and a server missing one of those binaries says so in its log at boot. A cached thumbnail is released when the file it belongs to is deleted for good, and a periodic reconciler reclaims the orphans an older install accumulated. A thumbnail **follows its file**: one changed outside filex, or one that never had a picture, is drawn again when a listing or the sync sees it; **SVG** is drawn by a built-in engine on every install (with size and time limits an administrator sets), and **HEIC/AVIF** photos through ImageMagick; transparent pictures sit on a checkerboard; a **folder shows the files that came into it last**, drawn with the folder in the grid, the gallery and the list, and what it holds on hover (an administrator can turn that off); text files show their first lines and archives what is in them; a file whose tool is missing is named, not papered over; and **Admin → Tools → Thumbnail repair** redraws a file, a folder or a storage on demand ([docs/thumbnails.md](thumbnails.md)).

## Storage and protocols

- **Multi-storage** - mount many storages at once (local, S3, FTP, SFTP, WebDAV, SMB/NAS); each appears as a top-level folder. Each also carries an address that never moves: the storage's name is the first path segment on WebDAV, SFTP, NFS and the S3 API, so renaming one would re-address it - a mount written against its **uid** survives every rename. **Copy or cut in one and paste in another**: filex streams the tree between the two drivers, keeps each file's timestamp, and only removes the original once the copy is verified. A store that is down is reported within seconds, and only silence is timed out, never a transfer that keeps moving (S3, WebDAV, FTP, SFTP and SMB: [docs/STORAGE.md](STORAGE.md#when-the-store-is-down)). An entry the storage could not answer for (neither "here" nor "not found") is kept, marked with a **!** and the storage's own answer, and nothing is done with it - in the explorer, the REST and agent APIs, sharing and the editors (the file protocols do not read the mark) - until the storage answers again ([PLUGINS.md](PLUGINS.md#an-entry-your-stat-cannot-answer-for)). A storage's credentials are never shown back, to administrators either: they read as `***`, and a save that sends the mask keeps the saved one only while the address stays the same.
- **Symlinks, at the storage boundary** - a link inside a `local` storage that points inside it is followed and opens as what it points at; one that leaves the storage is **listed with a badge and the reason**, and refused for reading, writing and deleting - unless you switch on *Follow symlinks that leave this folder* for that storage ([docs/STORAGE.md](STORAGE.md#symlinks)).
- **Storage plugins** - a storage filex has never heard of is a **separate program** you install from the admin panel: it describes its own config form, filex speaks a small HTTP/JSON protocol to it, and its driver then behaves like any built-in one. Any language; a Go SDK makes it three methods. filex **probes every capability a plugin claims** - at install, and again against the configuration you type when you save a storage on it - and refuses one that cannot do what it says, because a half-working driver produces failures that look like filex being broken. Upgrades replace the binary in place and roll back if the new one does not come up; every start checks the binary's hash and signature again, and each plugin keeps a **log** of its starts, failures and the entries it could not answer for (*Actions → Log*) ([docs/PLUGINS.md](PLUGINS.md)). A signature names the build - the plugin, its version, its platform and its SHA-256 - so it vouches for nothing else; one over the SHA-256 alone is still taken, with a warning on the plugin's row, at least until filex 0.61 and 2027-10-09, whichever comes later ([What is signed](PLUGINS.md#what-is-signed)). A plugin built for a release keeps working unchanged for at least 6 minor releases or 12 months, whichever is longer, and an old form is refused only after a CHANGELOG has announced it ([Compatibility window](PLUGINS.md#compatibility-window)). An **app store** lists storage plugins too: installed through its link and the same review, held to its pin, signed by it and checked by its plugin validator before it was listed, a paid one **held** - nothing runs, nothing is removed - while its license does not hold ([Installing from a store](PLUGINS.md#installing-from-a-store)).
- **DB-backed file tree** - listings come from the DB cache (1-5 ms), not the storage backend (~100 ms); a periodic sync catches out-of-band changes, by etag where the backend reports one and by size + modification time where it does not. That sync never judges a change filex is making: every rename, move, delete and restore - the explorer's, the queue's, the protocols', an agent's, a draft's, the antivirus quarantine's - holds the storage's **row gate** from its first byte to its last row, a long one holds up nothing but its own storage's scan (a folder moved object by object on an object store fences only its own two paths), the queue runs the other storages' jobs meanwhile, and a change that waited checks again before it acts ([The row gate](ARCHITECTURE.md#the-row-gate), [metrics](METRICS.md#the-row-gate)). A storage's **Paths to exclude from scanning** (`.*`, `downloads/incomplete/**`, `*.tmp`) keeps the parts of an existing tree filex has no use for out of the walk, the catalogue, the search index and the virus scanner - a cost control, not an access control ([docs/STORAGE.md](STORAGE.md#scan-exclusions)).
- **Lazy catalogue for big local trees** - `sync_mode: lazy` skips the walk up front: the folder you open is listed straight from disk at once and catalogued first, and the rest is catalogued by a slow background pass that yields to people (or only as folders are opened). Opened folders are watched within a budget, a folder nobody visited is never treated as deleted, and search, folder sizes and usage say plainly when they do not cover everything yet ([docs/STORAGE.md](STORAGE.md#lazy-catalogue), [design](LAZY-CATALOGUE.md)). Idea by Alex ([#45](https://github.com/BRF-Tech/filex/issues/45)).
- **Protocol gateway** - the same tree is reachable as **S3** (SigV4; aws-cli, rclone, restic, mc, s3fs), **SFTP** (OpenSSH, WinSCP, FileZilla, sshfs), **FTPS** (explicit TLS, for the equipment that only learned FTP; hand it your reverse proxy's auto-renewing certificate - it is re-read on change), **NFSv3** (LAN NAS clients, media players) and **WebDAV** - each with its own credential you can revoke on its own, and all of them behind the same filex permissions (per person, group and folder), trash and quota as the UI; the POSIX bits and ACLs on the disk are not mapped to filex users ([docs/PROTOCOLS.md](PROTOCOLS.md)). A rename, move or delete made there goes through the same row gate as the explorer's, so a storage scan running beside it never drops the moved row with its shares, versions and comments, or a trashed one with its trash entry ([The row gate](ARCHITECTURE.md#the-row-gate)).
- **A name is kept exactly, and refused by one rule** - Turkish, Chinese, emoji, a space at the end, `#`, `%`, `?` and quotes are stored byte for byte and handed back the same by every surface; the explorer, the API, file requests, agents, WebDAV, S3, SFTP, FTPS and NFS refuse the same few new names (empty, `/` or `\`, a control character, `.`, `..`) with the protocol's own error; a right-to-left override can no longer hide a file's extension, and folder sync brings down a name your computer cannot hold under a safe one without renaming anything on the server ([docs/FILE-NAMES.md](FILE-NAMES.md)).
- **`filex mount`** - attach a remote filex server to a folder over ordinary HTTPS: a folder on Linux, a **drive letter on Windows** (`filex mount Z:`, needs the free [WinFsp](https://winfsp.dev)). Not a sync: nothing is copied but a bounded read cache, so it opens one file out of a hundred thousand without downloading the rest.
- **Usage & cost** - filex does not meter your provider's bill; it reads the report the provider already writes, normalises it and prices it with a table you can edit. Backblaze B2's daily CSVs are read over the same S3 API filex already speaks, so no new dependency and no new credential type. Free allowances are their own fields rather than constants in a formula, and the page keeps the provider's account-level row apart from its per-bucket rows - summing them counts the same transactions twice, by exactly the amount nobody notices ([docs/USAGE.md](USAGE.md)).

## Sharing and protection

- **Sharing** - public links with PIN, expiry and max-downloads, under an admin-set **maximum link life** (default 7 days - the dialog only offers what the server will keep); folder links stream as ZIP (cached, pre-warmed up to a size ceiling, swept after a week); **file-request** upload links for inbound drops; ShareX-compatible upload endpoint. **My shares** lists the links you created - for everybody, not only administrators - with *Copy link*, *Copy PIN* and *Revoke*: a link's PIN is kept sealed beside the hash that guards it, so its creator or an administrator can read it back when somebody needs it again, and every read is written to the audit log. Five wrong PINs shut any public link for ten minutes. A download link, a file request and an app's page are **one branded public screen** - your instance's name, logo and colours, one PIN gate, one expiry story and a language picker ([docs/SHARING.md](SHARING.md)). Every listed link says where it stands - active, expired, used up or revoked - and a new link comes with its own `curl` and PowerShell download command. A link mailed from the Share dialog is written by the server from the link itself, in each recipient's language, and never carries its PIN ([Emailing a link](SHARING.md#emailing-a-link)).
- **Trash & version history** - deletes are reversible within a retention window, writes keep snapshots; both live in the storage you already mounted ([docs/TRASH-VERSIONING.md](TRASH-VERSIONING.md)). The Trash pages through everything it holds, and *Empty trash* first shows the server's count and size of exactly what it will delete.
- **Write protection** - optional ClamAV scanning of every file written - the built-in editor included, and files the storage sync finds on the backend rather than through filex - reached through a local binary or a clamd container over the network; plus trash/version retention behind one admin surface. The switch, the scanner mode and address, the size ceiling and the editor save-scan window live on **Settings → Protection**; the `FILEX_CLAMAV*` variables seed them on a first boot and then step aside (the scanner's binary path stays environment-only, deliberately - it is a command this server executes) ([docs/PROTECTION.md](PROTECTION.md)).
- **E2E encrypted folders** - client-side WebCrypto; the server stores ciphertext and never receives a key. A folder has a **level**: contents only (the default - WebDAV, the CLI and desktop sync keep working with its names) or **contents and names** (AES-SIV, so the server keeps no readable name), and it can be raised later, resumably, from its **Encryption settings**, where its password is changed too. A third level, the **vault**, hides the shape of the tree as well - the server stores only packs of one size and an encrypted index, and one person writes at a time under a lock it keeps; it is built and off by default (`FILEX_E2E_VAULT`), and the web and desktop apps, `filex decrypt` and `filex vault mount` open it ([the vault's format](E2E-VAULT-FORMAT.md)). A folder you already have is **encrypted in place**, files over 200 MB included; **any single file can be encrypted on its own** (a self-contained `.fxe` with its own password and recovery key); files of any size are encrypted as a stream; an unlocked folder downloads as a **decrypted zip** made in the browser; `filex decrypt` opens a downloaded folder or `.fxe` on your own machine, and **`filex encrypt`** makes an encrypted folder from one on disk, or encrypts a folder on the server where it is - for folders too large for a tab, resumable, the keys made on your machine ([docs/CLI.md](CLI.md#filex-encrypt---make-a-folder-an-encrypted-folder)). Each folder gets a **recovery key**, shown once, so a forgotten password is not automatically lost data; an operator can optionally enable **key escrow** - at install, or adopted later on a running installation; it never reaches existing folders on its own, but their owners are offered the choice at unlock - and its use notifies the folder's owner ([docs/E2E-ENCRYPTION.md](E2E-ENCRYPTION.md)). Every file row the server answers with says whether the file is encrypted - in a folder, in a vault or as a single `.fxe` - in every view, so the explorer offers such a file no ONLYOFFICE and no app action, and the server decrypts nothing for anybody ([Which rows say `encrypted`](APP-PLUGINS-API.md#which-rows-say-encrypted)). **Editing in an app** is the browser's: an app you installed with the `files:e2e-plaintext` grant is offered a file of a folder you have unlocked, or of a vault you have opened, and edits it in its own frame - your browser decrypts the file for the app and encrypts what it saves, an app picks a level-2 file by the name your browser decrypted, and what the app keeps while it edits is sealed with the folder's key; the server sees ciphertext, and in a vault not even the file's name ([Editing in an app](E2E-ENCRYPTION.md#editing-in-an-app), [In a vault](E2E-ENCRYPTION.md#in-a-vault)). An administrator's switch turns it off for every app. **Who may encrypt** is the organisation's call: a platform operator's switch per tenant, a tenant policy (off, administrators only, everyone whose role allows it, or **after an administrator's approval** - a request with a reason, approved for one person, one folder and one kind of encryption, once), and the `files.encrypt` permission, asked at every door that could make something new encrypted, copies included ([docs/E2E-ENCRYPTION.md](E2E-ENCRYPTION.md#who-may-encrypt)).
- **Replica + reconciliation** - a storage linked to a replication target copies every write to it (mirror / append-only / skip per path-glob rule), each storage into a folder of its own there and filex's own folders (trash, versions, thumbnails, drafts) never; the files it already held go over in a resumable **initial copy** whose progress the Replication page shows; read fallback while the primary is down, a scheduled status report, and failures kept per storage and replayed with one-click "Fix all" ([docs/REPLICATION.md](REPLICATION.md)).
- **Audit log** - every mutation recorded with actor, integration identity and metadata.

## People and access

- **Roles and per-user permissions** - 30 named permissions (each file action, each kind of
  sharing, each protocol, API keys, the desktop app, the AI assistant, five admin areas), and everyone has one
  role: Administrator, User, Viewer or a custom role, which can differ in some folders ("no
  delete, except in Scratch") and carry limits (link lifetime and password, blocked file types,
  largest file, required two-factor). Per-person exceptions beat the role, a delegated
  administrator can manage users without ever holding more than they hand out, and the same
  answer holds on every door - the web app, the agent API, WebDAV, SFTP, FTPS, S3, NFS and API
  keys. An installed app can add permissions of its own ("Request signatures"), handed out
  the same way, and a public link stays open only while its creator may still make it
  ([docs/PERMISSIONS.md](PERMISSIONS.md)). A permission a later version stored survives
  every save, and a role an older version's page may have saved without *Encrypt* is pointed
  out on **Admin → Roles**, with one click to give it back
  ([Things to know](PERMISSIONS.md#things-to-know)).
- **Groups** - named sets of people, per tenant: share a folder with a group as with a
  person, and give a group a role that everyone in it without a role of their own holds
  (a role priority decides between groups). People join by hand, or through the groups
  their sign-in carries - an OIDC claim, LDAP `memberOf`, the operating system's groups or
  a proxy header - and leave when the identity provider says so
  ([docs/GROUPS.md](GROUPS.md)). A group linked to a directory group by its full DN can
  make its members administrators - the directory then decides who runs filex, and the last
  administrator is never removed ([Administrators](GROUPS.md#administrators)).
- **RBAC + item permissions** - per-file/folder grants with inheritance under **Admin → Folder access**, a whole storage given to people and groups from the storage's **Access** section ([docs/RBAC.md](RBAC.md#a-whole-storage-for-people-and-groups)), share invites by email (SMTP), grant-aware search and listings. **Shared with me** answers the reverse question from the recipient's side - what other people granted you, and which storages you reach only through a grant ([docs/RBAC.md](RBAC.md)).
- **Identity providers, managed from the panel** - **Admin → Identity
  providers** drives sign-in:
  OIDC, LDAP, the header proxy, the local password form and the operating
  system's own accounts - **Windows** (local or domain, `LogonUserW`, nothing to
  install) and **Linux PAM** - each with a **Test now** that really probes it and
  says which leg it verified. An operating-system provider is switched on only
  by a test that signed a real account in, and that account becomes a super
  administrator; it cannot be switched on from the environment. Every provider
  follows **one first-sign-in rule** - whether it may open an account
  (`auto_create`, off by default for Windows and PAM) and for which groups
  (`allowed_groups`). What is set in the environment or in `config.yaml` **wins,
  visibly**; a client secret or bind password is write-only and sealed at rest
  with `FILEX_SECRET_KEY`, never sent back; and the last way in cannot be
  switched off from the page ([docs/SSO.md](SSO.md),
  [docs/LDAP.md](LDAP.md), [docs/OS-LOGIN.md](OS-LOGIN.md)).
- **OIDC SSO-first** - optional auto-redirect to your IdP with break-glass local login (`?local=1`), and the admin role follows an IdP group at every sign-in.
- **LDAP / Active Directory** - **directory sync** (*Sync now*, or on a schedule) opens an account for everyone in the directory before their first sign-in, keeps group memberships in step, brings the directory's groups in as filex groups, and disables in filex whoever the directory disables (sessions, API keys and SSH keys with them); people are known by their permanent directory id, so a changed e-mail keeps its account and a reused one does not inherit it; several directories each manage only their own people and groups ([directory sync](LDAP.md#directory-sync), [several directories](LDAP.md#several-directories)). Directory accounts sign in on the same password form as local ones, and with the same password on WebDAV, SFTP and FTPS (S3 and NFS take the keys and exports they mint); private-CA support, and `local` stays first so `admin@local` works while the directory is down. An account's e-mail is always an address: the entry's mail attribute, else a name typed as `name@domain`, else `name@local` (`name@<realm>.local` in a tenant's realm; the one rule the operating-system providers use too); an account an older filex opened under the bare name is **adopted** at its next sign-in, files, shares and role unchanged ([docs/LDAP.md](LDAP.md)).
- **Sign-in security** - wrong passwords are counted per account identifier
  (whether or not the account exists, so the count gives nothing away) and per
  client address, on the web form, WebDAV, FTPS and SFTP alike: 5 per account
  and 10 per address within 10 minutes shut the door for a minute, doubling up
  to 15, and a lock refuses even the right password. The sign-in form says how
  many tries are left, in the reader's language. An **IP allow-list** is the
  way back in - no account is privileged, the first administrator included
  (only a public demo's shared account is counted per address alone,
  [docs/DEMO.md](DEMO.md)) - and **Admin → Sign-in security** holds the
  limits, the list, the locks with *Lift the lock*, and the sign-in trail: every wrong
  attempt, lock, release and settings change, once each, whichever door an
  administrator used (the panel, an API key, MCP). The address counted is the
  socket's peer unless that peer is a proxy you trust (`FILEX_TRUSTED_PROXIES`,
  by default `auto`: this machine and, in a container, the other containers on
  its network - never the gateway, never the LAN; the page names a peer that
  sends forwarded addresses without being trusted and offers to add it), so a
  client cannot pick its own address by writing `X-Forwarded-For`
  ([docs/CONFIGURATION.md](CONFIGURATION.md#sign-in-attempt-limits)); since 0.57 the variable
  seeds the page's list at the first start and the saved list wins after that
  ([Trusted proxies](CONFIGURATION.md#trusted-proxies)).
- **Changes from another site are refused** - a request that changes something and
  carries only the visitor's session (the cookie, or a trusted proxy's sign-in header)
  must come from filex's own pages, its own address or an origin listed in
  `FILEX_CORS_ALLOWED_ORIGINS`; anything else is answered `403 cross_origin_refused`
  before a route runs. Keys, share and drop links, upload tickets, S3 and scripts are
  unaffected ([docs/CONFIGURATION.md](CONFIGURATION.md#requests-from-other-origins)).
- **Native multi-tenancy** - provider/tenant mode with per-tenant isolation on one instance, turned on and off with one switch (**Admin → Multi-tenant mode**, the platform operator's alone; `FILEX_MULTI_TENANT` pins it). Off, no screen shows a tenant or a realm, and turning it off puts the tenants in maintenance mode and deletes none of them ([mode gating](MULTI-TENANCY.md#3-mode-gating-backward-compat-is-non-negotiable)). On, storage-per-tenant with native tenancy mode, RBAC roles + per-item grants, confined API tokens, per-token identities for audit trails, and app-vs-user token kinds so a shared embed credential cannot manage anybody's keys. A token names the permissions it holds - an empty list is refused rather than read as "everything" - and **no credential it issues is ever wider than itself**: an API key, an S3 key, an NFS export or an SSH key minted through a narrow token cannot exceed its verbs, leave its folder or outlive its expiry (`403 token_ceiling`). The tenant boundary is enforced on every route that names a row, not only on the ones that list them, and instance-wide settings are reserved to the supertenant. Each tenant has a **realm** - its sign-in name, given at creation and never changed, so two tenants' `alex` are two people - and a sign-in names its tenant by the tenant's own address (the web page, the WebDAV `Host`, the FTPS certificate name) or by the realm: a **Realm** field on the sign-in form, `realm/name` over SFTP. The account lookup never leaves the tenant, and a realm typed on the platform's page for a tenant with an address of its own is **handed over** there with a one-use, 60-second ticket ([docs/MULTI-TENANCY.md](MULTI-TENANCY.md), [realms](MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)). Tenants run themselves on **Admin → Tenants** and **My tenant**: the operator binds shared sign-in providers to one tenant or several, a tenant's administrator adds the tenant's own OIDC or LDAP, every tenant gets a platform subdomain, and a tenant's own domain is proven by a CNAME and served with a certificate from your proxy, from filex itself (ACME) or its own ([docs/TENANT-ADMIN.md](TENANT-ADMIN.md)).
- **An admin panel you can find your way in** - the administrator's pages sit in a mega menu in the top bar: the dashboard, then **Files & storage**, **People**, **Settings** (every page that changes how the instance behaves, in three columns: General, Security & data, File processing & integrations) and **Maintenance & records**, each a panel of named sections with a short line under every page. A tenant's administrator is not offered the pages about the instance, which the server would refuse them. Every page is two clicks away at the address it always had, a delegated administrator is offered only the pages their permissions open, the keyboard and screen readers are covered, and a phone gets the same pages as a list in a drawer. A **search** beside the menu (Ctrl+K, and a button on a phone) finds a page, a single setting, a person, a group, an API key, an app, a storage or a share by its name in your language or in English, files on demand (`file:`), and only what you may open ([Admin panel](ADMIN-PANEL.md), [Search](ADMIN-PANEL.md#search)).

## Desktop app, phone and CLI

The explorer also ships as a **Windows / Linux / macOS desktop app** - the same component
the web UI and the embeds render, not a separate half-copy. Install it from
[docs/DESKTOP.md](DESKTOP.md#install).

- **Several accounts at once** - a rail of servers/tenants, each showing its own branding.
- **Drag files out** - drag a selection onto the desktop or into another app: folders and
  multi-selections arrive as separate real files and folders. Anything already kept on
  this computer drags instantly; the rest is fetched once and cached. In a browser, a
  single file drags out the same way - in the admin app too, through a one-file link
  that lasts a minute and works once ([docs/DESKTOP.md](DESKTOP.md#dragging-files-out)).
- **Keep on this computer** - right-click any folder, file or whole storage to mirror it
  under one filex folder on the machine (one per account, movable from App settings);
  everything else stays online-only, and every row says which it is (✓ ◐ ⟳ ☁). "Keep online only" hands the
  local copy back to the Trash, or leaves it
  ([docs/DESKTOP.md](DESKTOP.md#keeping-folders-on-this-computer)).
- **Folder sync** - pair a local folder with a server folder and they stay in step both
  ways while the app sits in the tray, **live**: a save in the browser is on disk in about
  a second and a local save on the server just as fast (the engine follows the server's
  change stream and the file system, with a full check every 30 s as the safety net),
  both versions kept when both sides change at once, parallel transfers and listings, a
  first run that resumes where it was interrupted, deletions restored from the server's trash, and an engine that
  refuses to turn a missing folder into a mass delete ([docs/SYNC.md](SYNC.md)).
- **Opens Office documents off your own disk** - double-click a `.docx`/`.xlsx`/`.pptx`
  (or any of the ten Office types, or a `.csv`) and it opens in the editor your server runs, on a
  machine with no Office installed - and every other kind an app on one of your servers opens (a
  `.psd` where a server has an app for it), with filex added to the system's "Open with" for those
  kinds as the apps come and go. A document inside a folder you keep on this computer
  opens as itself; anything else is copied up, edited, and written back over the original -
  or beside it, when the editor saves it in another format (an old `.doc` comes back as `.docx`)
  ([docs/DESKTOP.md](DESKTOP.md#opening-documents-from-your-computer)). A document that
  changes on disk while it is open - an agent rewrote it - is never saved over: the editor
  shows the new version, or asks first when you have unsaved edits
  ([When the file changes while it is open](DESKTOP.md#when-the-file-changes-while-it-is-open)).
- **Mount as a drive** - one button in App settings attaches the server as a drive of the
  operating system over WebDAV, and one detaches it; the account's own token is the
  credential and never appears on a command line. Measured on Windows; the macOS and
  Linux paths are there but not yet verified ([docs/DESKTOP.md](DESKTOP.md#mounting-the-server-as-a-drive)).
- **⌘K searches every account** on the rail, grouped under one badge per account, each
  searched, downloaded and dragged out with its own sign-in ([docs/SEARCH.md](SEARCH.md)).
- **Your notifications and your account in the window** - the top bar ends as the web
  app's does: the **bell** (unread count, the newest rows, *Mark all read*, the full list)
  and the **avatar** with *User settings* - the web app's own settings dialog, opened
  **inside the window** - and *Admin panel* for an administrator. A click on a
  notification lands in the window, the folder with the file selected. Signing out stays
  in the app's own *App settings → Accounts*
  ([docs/DESKTOP.md](DESKTOP.md#notifications-and-your-account)).
- **Speaks your account's language** - the window, the tray menu, the notifications and
  the sync engine's messages under each folder use the language of the account on
  screen, and *User settings → Preferences → Language* changes the account's, so the web app follows
  ([docs/DESKTOP.md](DESKTOP.md#language)).
- **Signs in through your browser**, so SSO and MFA behave exactly as they do on the web.
- **Updates itself** - downloads quietly, installs on quit; `FILEX_NO_UPDATE=1` opts out.
  The Mac app does not update itself yet: the build is unsigned, so it is updated by
  downloading the new one (or `brew upgrade`) until it is signed.
  Every release is installed over the previous one on real Windows and Ubuntu machines,
  x64 and arm64, before it ships, and must leave one copy of the new version where the
  old one was, with your accounts and settings ([Updates](DESKTOP.md#updates)).
- **Runs without being installed**, if that is what you need: the Windows **portable**
  `.exe`, the Linux AppImage and the macOS `.zip` all run from wherever you put them. The
  portable Windows copy keeps everything it has in one `filex-data` folder beside itself,
  so deleting that folder leaves nothing of yours on a machine that is not yours - the
  trade is that it does not update itself.
- **Windows of its own** - each document opens in **its own window** (titled with the file's name), the windows are **frameless** with the app's own controls (native traffic lights on macOS), and **User settings → Preferences → This device → Open files with** chooses single- or double-click to open ([docs/DESKTOP.md](DESKTOP.md), [docs/SYNC.md](SYNC.md)).
- **Linux, and Chromium's sandbox** - the `.deb`, `.rpm` and AppImage never run without
  Chromium's sandbox. The `.deb` and `.rpm` need nothing; on Ubuntu 23.10 and later an
  AppImage needs a one-time AppArmor profile, and the app says so and shows the step
  ([AppImage on recent Ubuntu](DESKTOP.md#appimage-on-recent-ubuntu)). The Snap runs
  without Chromium's sandbox, inside the snap's strict confinement, and needs nothing
  either ([The snap and the sandbox](DESKTOP.md#the-snap-and-the-sandbox)). It is built on
  `core24` (Ubuntu 24.04) with Snapcraft's GNOME extension, on x64 and arm64: its first
  install also brings the GNOME runtime, the graphics libraries and the GTK themes as
  shared snaps.
- **On a phone, the web app installs like an app** - an icon on the home screen and a window of its own, under your instance's name and logo when it is branded ([The installed app](CONFIGURATION.md#the-installed-app-pwa)). A band at the bottom of the sign-in page and of the file list offers it from the first visit: Android's **Install**, the browser menu's way until the browser offers one, or the iPhone and iPad's **Share → Add to Home Screen**; a phone is never offered the desktop app, and once installed nothing is offered. Notifications reach the phone while filex is open, and with it closed through Web Push once you turn it on for the device ([On a phone or a tablet](DESKTOP.md#on-a-phone-or-a-tablet-the-web-app), [Web Push](NOTIFICATIONS.md#web-push)).
- **CLI client** - the same binary reaches a remote server (`filex client`, `filex sync`) with no server-side plugin: copy and move across storages, the trash, versions, tags, app actions, archives and your links, each server job followed to its end; `filex client login --realm` signs in to a tenant, `filex encrypt` makes encrypted folders, `filex vault` mounts and tidies a vault, `filex sync run --json` hands a program the engine's events (one JSON event per line, each with a stable code and the engine's sentence in the account's language - the desktop app shows exactly those: [the event stream](SYNC.md#the-event-stream---json)), a refusal prints the server's own sentence, and a saved session is only ever sent to the address it was saved with ([docs/CLI.md](CLI.md)).

## Embedding

- **Embeds anywhere** - the same UI ships as a Vue 3 component, a React component and a
  framework-agnostic `<filex-explorer>` web component. Put a real file manager inside
  *your* product, backed by your own filex server and locked to a per-tenant folder.
  The navigation panel comes with it - `<filex-explorer sidenav ui-profile="simple">`
  is the whole opt-in for a host page that never touches JavaScript.

The React component and the web component have **no stylesheet to import** - the look
travels inside the bundle and is injected on mount (the Vue package's is
`@brftech/filex-core/style.css`). ⚠ A bundler will need the
optional viewer packages externalized (`monaco-editor` and friends), which
[docs/INTEGRATION.md](INTEGRATION.md) shows in one `rollupOptions.external`
line; every one of those imports is guarded, so the viewers degrade rather than break.

`sidenav` turns the navigation panel on (it is on by default; the attribute is
there so a host page can state it either way), `connections` adds its "How to
connect" and "API keys" entries, and `ui-profile="simple"` presets the
power-user chrome off. All three are ordinary `config` keys, so the Vue and
React wrappers set them the same way - see
[docs/INTEGRATION.md](INTEGRATION.md).

Multi-tenant hosts typically proxy the API server-side, inject a **confined token**
(`root: tenant-folder`) per request, and strip client headers - the sandbox is enforced by
the backend, not the widget. Such a token is `kind: "app"`, so the panel hides the
surfaces that belong to one person - API keys, Recent, Starred, Shared with me -
while Upload, the storages, Trash and "Connections" stay. See
[docs/INTEGRATION.md](INTEGRATION.md) and
[docs/MCP.md](MCP.md#token-kinds---user-vs-app).

⚠ An embed that rides on the visitor's own filex **session cookie** (no token) from a
page on another origin - a sibling subdomain included - can read, but every
change it sends is refused (`403 cross_origin_refused`) until that origin is in
`FILEX_CORS_ALLOWED_ORIGINS`; the default `*` does not grant it. A bearer token, a host
that proxies with a key, the desktop app and the installed web app need nothing
([docs/CONFIGURATION.md](CONFIGURATION.md#requests-from-other-origins)).

⚠ Keep the packages and the server on one version: **`@brftech/filex` `X.Y` needs a
filex `X.Y` server**. Which files open for editing, the input limits and the version line come
from the server's capabilities, whether a file is end-to-end encrypted comes from its
row, and the packages keep no copy to fall back on ([docs/API.md](API.md)).

⚠ An app that prints (`ui.print`, 0.55) prints from a page of filex's own, in a frame.
Where the web component runs on another site, list that site in `FILEX_FRAME_ANCESTORS`,
or the app is told printing is unavailable
([Security headers and framing](CONFIGURATION.md#security-headers-and-framing)).

## Apps

A **storage plugin** teaches filex a backend it has never heard of. An **app**
teaches it a *thing to do with files* - sign them, convert them, send them to
somebody outside - and it is a different kind of plugin on purpose: a
**WebAssembly module that runs inside filex, in a sandbox** that hands it nothing
it was not granted. No filesystem, no network, no environment, no program on your
server: only the host functions its manifest asks for, each shown to the
administrator in plain words before anything is installed, and only the files
the person who ran it actually selected. The heavy engines an app may want
(ffmpeg, ImageMagick, Ghostscript, poppler, rsvg) are the server's own, offered
one permission per engine; office documents go through the office engine - the
one the office editor app brings, run on the server in a process of its own,
or an ONLYOFFICE Document Server you connect - filex runs no LibreOffice.

An app may also bring - or be nothing but - **an interface of its own**: HTML,
CSS and JavaScript its author wrote, an editor or a viewer for a format. filex
serves it from the package you approved (pinned by its SHA-256) in a **sandboxed
frame**: an opaque origin that cannot read filex's session, cookies or pages, a
content policy filex writes from the app's grant (no connection, no storage, no
forms, no pop-ups), and one checked message channel through which filex hands it
only the files it was opened with and saves over them - a new version, or a
draft. It can add its own kind of file to **New document**, open in the editor
tab, and hand you a file to keep - each time you allow it. Its SDK is
`@brftech/filex-app-ui` ([an app's own interface](APP-PLUGINS.md#an-apps-own-interface)).
⚠ Browsers cannot entirely stop a page from sending data out (WebRTC ignores a content policy;
Chrome lets filex close it, in Firefox filex can only take it out of the page -
a seat belt, not a wall), so the install review says so plainly: **trust an
app with an interface as far as you trust its author with the files you open
in it.**

An editor that runs in the browser may ask for two narrow exceptions, each a
line of the review: frames of **its own package's** pages (`ui:frame-package`)
and reading the `blob:` addresses it made (`ui:connect-blob`) - never another
site, another app or a page of filex. A sandboxed frame cannot open the print
dialog either, so an interface with `ui:print` hands filex a PDF and **filex
prints it** from a page of its own: it asks every time, and the print dialog
opens only on the person's click on *Allow*
([Printing a PDF](APP-PLUGINS-API.md#printing-a-pdf-uiprint-055)). An
**end-to-end encrypted file reaches no app on the server's side** - no action,
no ONLYOFFICE, and filex decrypts nothing for an app; it tells an interface
that a file is encrypted rather than pretending otherwise
([Which rows say `encrypted`](APP-PLUGINS-API.md#which-rows-say-encrypted)).
The one way in is the browser's and yours: an interface whose manifest asks
for encrypted folders is granted **`files:e2e-plaintext`** - the review says
in its own words that the app sees every such file opened with it - and is
offered a file of a folder you have unlocked or a vault you have opened, which
your browser decrypts for its frame and encrypts again before it is saved
([A file of an encrypted folder, in the clear](APP-PLUGINS-API.md#a-file-of-an-encrypted-folder-in-the-clear-encrypted_folders-056),
[Editing in an app](E2E-ENCRYPTION.md#editing-in-an-app)). An app granted
**`files:co-edit`** edits a file **together** with other people through
filex's relay - what each one changes reaches the others sealed, in one order,
the app never holds a key, a person who may only read the file watches every
change live, and changes a dropped connection left behind are sent when the
file is opened again ([Editing together](APP-PLUGINS-API.md#editing-together-coedit-056)).
The office editor app, [`filex-office-editor`](https://github.com/BRF-Tech/filex-office-editor)
(ONLYOFFICE's editor in the browser, with no Document Server), is built on
exactly these: it prints through filex, on a phone it opens a document to
read, with **Edit** opening the folded desktop editor
([On a phone](E2E-OFFICE.md#on-a-phone)), its 0.2.0 and 0.3.0, made for
filex 0.56, edit documents in encrypted folders and together, and its 0.4.0,
made for filex 0.57, brings an office engine filex runs on the server for
office thumbnails and the apps' office conversions. So **you do not need a
Document Server** - not to edit office documents, alone or together, and not
for filex's office thumbnails or server-side office conversions: the office
editor app is enough, and a Document Server you have stays supported and
optional ([ONLYOFFICE.md → The office engine without a Document Server](ONLYOFFICE.md#the-office-engine-without-a-document-server)).

What an app adds lives where everything else does: rows in the file menu, screens
filex draws for it or its own interface, jobs in the same
queue as a copy - with progress, **Cancel** and a result that is versioned,
scanned and indexed like any other write - a section in a file's details, a home
screen under **Apps** in the navigation, and, when it needs somebody without an
account, a link that is an ordinary **share**: in the same list, under the same
PIN lock-out and expiry policy, revocable by you like every other link, and never
worth more than its creator: a job started from it passes the same checks as one
started inside filex (an action you switched off stays off, the creator's access
to the document is read again), and it stops working when the creator's account
is switched off - until it is switched back on. The screens filex draws obey
filex's rules whoever wrote them - every choice visible rather than hidden in a
dropdown, nothing folded behind "advanced", one question per step. An app you
grant `schedule` is also woken once an hour, at the minute it chose, to do its own
scheduled work as an ordinary job in the queue - a signature request that closes
itself at its deadline and sends the reminders you asked for.

Not every app runs code. A **language pack** is a manifest of strings and nothing
else: it installs from the manifest alone - no module, no Go, no release - never
starts a runtime, and adds its language to the explorer, the admin panel, the
public pages and the text the server writes. **Plugins → Apps** lists it as a
*Language pack* with its coverage of the running version, and anything it lacks
shows in English. Spanish, German and French ship as examples, and
`BRF-Tech/filex-lang-template` walks a translator from export to install.

Five apps ship alongside filex, as public repositories you can install, read and
fork. The first two are modules; the other three are only an interface, with nothing
that runs on the server:

- **[e-Signature](https://github.com/BRF-Tech/filex-sign)** - `BRF-Tech/filex-sign`. **Sign…**, **Request signatures…**, **Sign / Fill** and **Verify** on a PDF, and only on a PDF (an office document is turned into one with **Convert** first). People on this filex sign inside it, from a notification that opens the right screen; anybody else, by name or email, gets a **private link**, behind a PIN unless you say otherwise - one filex keeps for you (see *Sharing*). A request is a short wizard: who signs and in what order; the boxes **defined first** - a name, whose it is, required or not, a date's layout - and **placed on the page after**, two questions on two screens; how long it stays open, whether the file is **frozen** meanwhile (for everybody, administrators included), and whether an **audit trail PDF** is written at the end. Reminders and the deadline run on their own, and the requester follows every signer in the file's details and on the app's home screen. The result is a PAdES-signed PDF that is **certified and sealed**: the first signature certifies the document so later ones may only fill in and sign, and when the last one lands **filex itself seals the whole file** with the installation's own seal, locked so that any change after it is reported as not permitted. The **SHA-256 of exactly those sealed bytes**, the seal's fingerprint and how to check them go to the requester and to every signer, inside and outside, and into the audit trail, and every signer gets a receipt. Optionally the signed file stays **locked in filex** until an administrator lifts it. **Verify** reports any signed PDF: every signature, the certification, the seal, and whether this is the file whose hash was sent. **Signing keys never leave the server**: the instance's own certificate authority (or one you import) issues a certificate per signer, and the key that made a signature is destroyed seconds later - the seal's key is the one exception, held by the host and never handed out ([docs/APP-PLUGINS.md](APP-PLUGINS.md#signing-documents-end-to-end)).
- **[Convert](https://github.com/BRF-Tech/filex-convert)** - `BRF-Tech/filex-convert`. **Convert…** on any file or selection: images, video, audio, documents, e-books, archives, data, subtitles and fonts. The target is picked from buttons grouped under their category, then only the settings that matter for it, then a review. Most conversions run in pure Go inside the sandbox; the rest use the server's engines when they are installed, and a target that needs a missing one says so instead of being silently absent ([docs/APP-PLUGINS.md](APP-PLUGINS.md#converting-files)).
- **[Office editor](https://github.com/BRF-Tech/filex-office-editor)** - `BRF-Tech/filex-office-editor`. Word, Excel and PowerPoint documents (and their OpenDocument kin) open in **ONLYOFFICE's editor, running in the browser**, in place of the preview and with **no Document Server**: the editor's files come from the app's package, the document is converted in the browser, and **Save** writes a new version. **New document** gets Word, Excel and PowerPoint rows, *Download as* and **Print** go through filex, and on a phone a document opens to read, with **Edit**. Its 0.2.0 and 0.3.0, made for filex 0.56, add documents in encrypted folders and several people editing one at once ([E2E-OFFICE.md](E2E-OFFICE.md)); its 0.4.0, made for filex 0.57, opens every kind a Document Server edits (`.doc`, `.xls`, `.ppt`, `.rtf`, `.csv` and the rest), saves a legacy document beside it as OOXML and a changed one as a conflict copy, takes dictionaries for more languages, and brings the office engine filex runs on the server for office thumbnails and conversions ([ONLYOFFICE.md](ONLYOFFICE.md#the-office-engine-without-a-document-server)). AGPL-3.0, based on ONLYOFFICE Docs by Ascensio System SIA.
- **[filextext](https://github.com/BRF-Tech/filextext-app)** - `BRF-Tech/filextext-app`. An **end-to-end encrypted text workspace** in a single `.fxtxt` file: pages and folders on the left, tabs on top, AFFiNE's BlockSuite editor in the middle (headings, lists, to-dos, code, tables, images, links between pages, Markdown in and out). It is encrypted **in your browser** with the same keys and recovery key as filex's [encrypted folders](E2E-ENCRYPTION.md); filex stores ciphertext and never sees the password or a word of the text. A `.fxtxt` opens in it in place of the preview, and **New document** gets *Encrypted workspace (.fxtxt)*.
- **[draw.io](https://github.com/BRF-Tech/filex-drawio)** - `BRF-Tech/filex-drawio`. The **draw.io** diagram editor, inside filex: `.drawio` and `.dio` files open in it in place of the preview, **Save** writes a new version, and **New document** gets *draw.io diagram*. draw.io's own files are served from the app's package (pinned by its SHA-256); it reaches nothing outside it.

**Install one from GitHub** - *Admin → Plugins → **Apps** → **Install an app** →
GitHub repository*: type `BRF-Tech/filex-sign` and the release tag. filex reads the
repository's `filex-app.json`, downloads the module (or the interface's
package) it names and refuses it unless its SHA-256 matches, then stops at the
**permission review**. Nothing is
installed until you have read every permission and ticked *I understand*; the
grant is exactly that list, and an upgrade that asks for more stops at the review
again. `FILEX_PLUGIN_TRUSTED_KEYS` makes signed modules mandatory (a GitHub
install carries no signature, so on such an instance upload the module with its
signature instead). Every download - an app, its update check, a storage plugin - goes
to public addresses only, judged after DNS and on every redirect: to install from a
server on your own network, upload the files. Apps are off in demo mode. An API key - an agent, a script,
the CLI - cannot install one: it **leaves a request**, filex freezes the bytes
and permissions it would install, and an administrator approves it under
**Plugins → Install requests**.

**Install one from a store** - an app store such as [filex Apps](https://apps.filex.sh)
sends you to this filex with an **install link**
(`/admin/store-install#store=…&intent=…`). The link only opens the same review: filex
reads the app from its GitHub repository at the commit the store approved, holds it to
the store's pins - the manifest's, the module's and the interface's SHA-256, the name,
the version, the permissions - and installs nothing until you press **Install**. The
first link from a store asks you to **trust** it and shows the fingerprints of the keys
it signs with; compare them with what the store publishes. A link is made for one filex
and works once, and the store is told how it ended. `FILEX_APP_STORE_URLS` /
`FILEX_APP_STORE_KEYS` trust stores by configuration instead, and then no other
([Installing from a store](APP-PLUGINS.md#installing-from-a-store)). A store
lists **storage plugins** too, through the same link and the same review: the
build is held to the store's pin, its signature names the plugin, the version and the
platform ([What is signed](PLUGINS.md#what-is-signed)), and the store's plugin
validator ran its Linux build in a container with no network before it was listed
([Installing a storage plugin from a store](PLUGINS.md#installing-from-a-store)).

**Let people ask for one** - the navigation panel's **Apps → App store** opens filex's own
screen over a trusted store's catalog, in the web app and the desktop app alike: read and
verified on the server from the store's signed index, never a frame of the store. A person
asks for an app - or, on the **Storage plugins** tab, a storage plugin - with a reason and
follows it under **My requests**; the request waits on
**Plugins → Install requests**, and approving it asks the store for a fresh install link and
opens the same review. **Admin → Plugins → Apps → Store screen** turns the screen on, picks
the stores and who sees it, and **Trusted stores → Connect** binds this filex to a store with
a one-time code ([The store screen](APP-PLUGINS.md#the-store-screen)).

**Paid apps.** A store may sell an app, and its license is the store's: filex keeps the
key encrypted and shows only its first characters, asks the store at install and every
day after, and when the store says a license is revoked, expired or out of seats the app
is **held** - installed, with its settings and records, running nothing - until the
license holds again, with a band on every admin page that names it. Nothing is removed by
a license. An app reads its own status with `fx.license.get()`, never the key
([Paid apps](APP-PLUGINS.md#paid-apps)).

**Who may use it.** An app can declare **permissions of its own** - a signing
app puts *Request signatures* behind one, while signing what you were sent
needs none - and you hand them out per role and per person like filex's own;
an action somebody does not hold is not in their menu and is refused if asked
for ([App permissions](APP-PLUGINS.md#app-permissions)).

**Thumbnails and Default apps.** An app can **draw thumbnails** of kinds filex does not
(handed one file's bytes and nothing else), and **Default apps** decides, per kind of
file, which app opens it and which draws its thumbnail, in which order; each person
picks among the openers left on, with *Always use this app* kept on their account - one
choice for the browser, the desktop app and an embed
([Default apps](APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail)).

**Nothing updates itself.** Once a day (and on **Check for updates**) filex
asks where each app came from - its GitHub releases, a language pack's branch,
or the manifest address it was installed from - for a newer version this filex
can run, and tells you: it waits under *Update available* (or *Needs approval*,
when it asks for more) until an administrator has reviewed what it changes -
permissions, module, interface files, notes - and approved it, and everybody
then uses that version. **Back to *version*** puts the one it replaced back.
Storage plugins can follow a source the same way. An app says which filex
versions it works with (`"filex": ">=0.47.0"` in its manifest), and filex will
not install it outside that range. filex's own promise runs the other way: an
app built for a release keeps working unchanged for at least 6 minor releases
or 12 months, whichever is longer, and an old manifest field, host function or
bridge call is refused only after a CHANGELOG has announced it
([Compatibility window](PLUGIN-KIT.md#compatibility-window)).

**Operator's guide:** [docs/APP-PLUGINS.md](APP-PLUGINS.md) - installing and
controlling apps, the signing round end to end, the converter, scheduled
wake-ups, and what guards an app's public links. **Writing one** (stock Go,
`GOOS=wasip1`, with a test kit): start from the template repository
[BRF-Tech/filex-app-template](https://github.com/BRF-Tech/filex-app-template) and
[docs/PLUGIN-KIT.md](PLUGIN-KIT.md); the
wire contract: [docs/APP-PLUGINS-API.md](APP-PLUGINS-API.md). The other kind
of plugin, a storage backend: [docs/PLUGINS.md](PLUGINS.md).

## AI agents and MCP

filex ships a token-authenticated automation surface at `/api/ai` (list, read, write,
move, copy, delete, search, share, zip) and speaks **Model Context Protocol** at `/api/ai/mcp`.
An agent also runs the explorer's own operations - copy across storages, app actions such
as **convert**, the operations queue, trash and version history, 7z/TAR archives, its
links and file requests, the bell, stars, comments and the permissions on an item it
owns - through the explorer's own handlers, so the rules are the explorer's. An admin
key reaches what the panel does (tenants, identity providers, sign-in security, Default
apps, webhooks, storages) through the panel's own handlers. `/api/ai` and `/api/files`
are described in an [OpenAPI 3.1 file](../backend/internal/api/openapi.json) a test
holds to the router. Hand an agent a token confined to one folder and it works there
with the explorer's own operations, and nothing outside it:

```bash
claude mcp add filex --transport http https://files.example.com/api/ai/mcp \
  --header "Authorization: Bearer <api-token>"
```

**An AI assistant in the explorer** - off until an administrator sets it up on
*Admin → Settings → AI assistant* against **Anthropic**, **OpenAI** or any
OpenAI-compatible server, or a local **Ollama** that keeps everything on your
network. It works through these same MCP tools, called inside the server **as
the person who asks** - their folder access, their root, their tenant, nothing
wider - and its panel streams the reply and shows every step a tool took. A
change that can be undone happens at once with an **Undo**; one that cannot (a
public link, an access change) waits on an **approval card**. Encrypted folders
and the vault never reach a model, every file content sent to one is in the
audit log with the provider and the model, conversations are kept per person on
the server for a set number of days, and the `ai.assistant` permission says who
gets the button ([docs/AI-ASSISTANT.md](AI-ASSISTANT.md)).

An API key carries permissions by verb (`read`, `write`, `delete`, plus `mcp` and `admin`)
and **comments** at a level of their own - `read`, or `comments:rw` to add and delete one,
which `write` does not give
([permissions with a level](RBAC.md#permissions-with-a-level-comments)) -
optionally **confined to a single folder**, gated by the same RBAC grants and roles as the UI,
and stamped with per-key identities so audit logs, shares and presence show *who* (which
integration) did what. The verbs hold on **every surface the key reaches** - `/api/ai`, the
MCP tools, the explorer's own routes (and so `filex client` and an embed), WebDAV, SFTP, FTPS,
and the S3 keys and NFS exports minted from it. Some acts are never a key's: installing a
plugin - an agent **leaves an install request** an administrator approves in the panel - and
making someone an administrator. A key must name
at least one permission - an empty list is refused, never read as "all of them" - and
**what it hands out can never be wider than the key itself**: asking through a read-only or
folder-confined key for an API token, an S3 access key, an NFS export or an SSH key with more
verbs, a root outside its own, or a longer life is refused with `403 token_ceiling` naming what
was too wide. An
agent's **move never overwrites**: an item headed for a name that is taken lands
beside it under a free one (`report-copy.txt`), exactly as a move in the UI does,
and the answer names the path it really landed on. And it knows
**encrypted folders**: every row says whether it is encrypted, ciphertext is never
handed out as if it were the file (`409 E2E_ENCRYPTED`), and plaintext written into an
encrypted folder is refused unless the caller says it means it (`allow_plaintext`)
([docs/MCP.md](MCP.md#encrypted-folders-and-fxe)).

A large file already on the agent's disk never fits through a tool call - its bytes would
have to travel through the model's context. **Upload tickets** fix that: one authorized
call pins the destination and returns a short-lived, single-use URL that needs **no
credentials**, so even an agent with no filex token can finish the transfer with
`curl -T bigfile <url>`. Details: [docs/MCP.md](MCP.md).

When filex says no, it says it the same way on every door: a stable `error` code to branch
on and the server's sentence in `message`, in the reader's language, to pass on as it is -
an app's run, an app store link, a plugin request and an install refusal included
([docs/API-ERRORS.md](API-ERRORS.md)). And a link an agent makes comes back with its
own download command, the `curl` and PowerShell lines written by the server
([docs/SHARING.md](SHARING.md)).

## Running it

- **Boringly deployable** - one binary or one container, on a host of its own or under a
  sub-path of one you share; SQLite by default, Postgres/MySQL
  when you want them; every driver switched by env vars. All three engines are
  migrated, compared against each other and written to by CI on every change
  ([what "supported" is checked to mean](DATABASES.md#what-supported-is-checked-to-mean)).
- **Driver-pluggable everything** - storage / auth / DB / queue drivers opt-in via env (`FILEX_AUTH_DRIVERS=local,oidc`, `FILEX_QUEUE_DRIVER=postgres`, …); the operating-system sign-in (`windows`, `pam`) is the exception, switched on from the admin panel once its test has passed.
- **Persistent op queue** - restart-safe queue in your own database (SQLite / Postgres / MySQL) or in Redis, worker pool with retries + cancel + admin dashboard. Every driver orders by priority, so the antivirus scan for a file somebody just uploaded is served ahead of the twenty thousand a first import queued. Unset, the driver follows the database ([Queue](CONFIGURATION.md#queue)).
- **Self-updating** - minor releases are announced for one-click upgrade, and patch releases install themselves once you allow it (`AUTO_UPGRADE=true`; out of the box filex only checks and tells you); an install a package manager owns (Homebrew, winget, Snap, a distribution package) or a container is told about new releases and the command to take them, and the admin page says it will only announce ([docs/UPDATES.md](UPDATES.md)).
- **Nightly builds** - the server image (`ghcr.io/brf-tech/filex:nightly`, and one tag per night) and the desktop app for Windows, macOS and Linux of every night the full test run passed on a change, on [filex.sh/nightly](https://filex.sh/nightly/) with the night, the commit and the checksums, and as a snap on the Snap Store's `edge` channel (x64 and arm64); not supported and not signed, for a copy of your data ([docs/INSTALLATION.md](INSTALLATION.md#nightly-builds)). Every release goes to all four of the Snap Store's channels, x64 and arm64 alike ([The Snap Store's channels](DESKTOP.md#the-snap-stores-channels)).
- **Settings live on the admin panel** - a variable that gives a setting its value only seeds it: the first start writes it, and the stored value wins from then on - the server's defaults, the search settings, `FILEX_TRUSTED_PROXIES`, the external services alike - and `filex settings reseed <key>` writes a changed variable on purpose; each row names the variable that gave its first value ([docs/CONFIGURATION.md](CONFIGURATION.md#variables-that-seed-a-stored-setting)). `FILEX_MULTI_TENANT` and the update policy stay in the environment.
- **Single binary** - goreleaser matrix: linux/macOS/Windows × amd64/arm64. CGO=0, modernc.org/sqlite.
- **The server's words, one shape for every refusal** - a refusal answers a stable
  `error` code and the server's sentence in the reader's language (`message`), and the
  explorer, the admin panel, the desktop app, the CLI and an agent show the same words
  ([docs/API-ERRORS.md](API-ERRORS.md)) - an app's refusals, every app store and
  plugin request refusal, an install's refusal and the install review's filex range
  among them. The status lines are the server's too: what an update of an app or a
  storage plugin needs, an update undone, the version kept to go back to, and the Apps
  tab's own header. The rules a client would otherwise keep copies of -
  which files open for editing, the input limits, the notification events that cannot
  happen on this install - are published by the server, so no screen judges them
  differently ([Rules the server publishes](BACKEND.md#rules-the-server-publishes)).
