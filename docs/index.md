---
layout: home
title: filex — self-hosted file manager
titleTemplate: false

hero:
  name: filex
  text: Self-hosted file manager
  tagline: One Go binary. Connect it to local disks, S3, SFTP, WebDAV, FTP or SMB — and reach it back as S3, SFTP, FTPS, NFS, WebDAV or a mounted drive.
  image:
    src: /logo.png
    alt: filex
  actions:
    - theme: brand
      text: Get Started
      link: /INSTALLATION
    - theme: alt
      text: Live Demo
      link: https://demo.filex.sh
    - theme: alt
      text: What's new
      link: /RELEASES
    - theme: alt
      text: GitHub
      link: https://github.com/BRF-Tech/filex

features:
  - icon: ✍️
    title: Apps — things to do with a file
    details: A second kind of plugin — a sandboxed WebAssembly app that adds actions to the file menu, screens filex draws for it, and public pages for people with no account — installed from a GitHub address through a review of every permission it asks for, and able to do exactly that and nothing else. Two ship alongside filex as public repositories — e-Signature, which sends a document round for signature and seals the finished file with the installation's own seal, and Convert. Write your own in stock Go, against a test kit.
    link: /APP-PLUGINS
    linkText: Apps docs
  - icon: 🌍
    title: Your language, right to left included
    details: English and Turkish are built in; any other language is a language pack — a manifest of strings, no code and no release — which joins every picker and translates the explorer, the admin panel, the public pages and the text the server writes, with its coverage of this version shown beside it. Plural forms follow CLDR. Arabic, Hebrew, Persian and Urdu lay the whole interface out right to left, and stop where mirroring would be wrong.
    link: /RTL
    linkText: Right-to-left docs
  - icon: 🎨
    title: Wear your own colours
    details: Compose named themes on the admin Appearance screen — twelve colours for light and for dark, a corner radius, a font stack, previewed as you type — and make one the instance default. It reaches the sign-in page and every public link too, because branding that stops at the login is not branding.
    link: /INTEGRATION#themes
    linkText: Themes & appearance
  - icon: 🌐
    title: A browser client for your users
    details: Give someone a user or viewer account and the address …/drive, and they land on their own Home — their storages, what they opened last, what they starred — one click from the file manager itself, with its uploads, sharing, search and editor, and with no admin panel around it and no separate frontend to deploy. …/admin is the operator's door to the same application, and it opens on the same Home.
    link: /RBAC
    linkText: Roles and access
  - icon: 🖥️
    title: Desktop app
    details: The same explorer in its own window on Windows, Linux and macOS — several accounts at once, right-click a folder or file to keep it on the computer, sync that keeps running when the window is closed, and double-click an Office document on your own disk to edit it without Office installed. Not allowed to install software? Every platform also has a copy that runs from wherever you put it, and the Windows portable one keeps all its files in one folder beside the .exe.
    link: /DESKTOP
    linkText: Desktop docs
  - icon: 🪄
    title: Drag files out, copy between storages
    details: Drag a selection onto your desktop and it lands as separate real files and folders, not an archive. Copy or cut in one storage and paste in another — filex streams the tree between the two backends, keeps every timestamp, and only removes the original once the copy is verified.
    link: /DESKTOP#dragging-files-out
    linkText: How dragging out works
  - icon: 🔁
    title: Folder sync
    details: A folder on your PC kept in step with a folder on the server, both ways, live — an edit on either side arrives in about a second instead of on the next 30-second lap — from the desktop menu or the CLI, with a bandwidth limit and a sync window when you want them. The first sync deletes nothing, holds back a big re-upload and asks first, and a delete never beats an edit.
    link: /SYNC
    linkText: Sync docs
  - icon: 🔍
    title: Search that forgives
    details: Find files by name or by what is inside them, across every mounted storage. "invoice 2026" finds invoice_2026.pdf, "mian.go" finds main.go, and tag:source narrows to what you tagged — with exact matches always ranked first.
    link: /SEARCH
    linkText: Search docs
  - icon: 🔌
    title: Speaks your tools' protocols
    details: Point rclone, restic, aws s3, WinSCP, FileZilla, a scanner that only learned FTP or a media player that only learned NFS straight at filex — S3, SFTP, FTPS, NFS and WebDAV, all on the same tree with the same permissions and trash as the web UI.
    link: /PROTOCOLS
    linkText: Protocol docs
  - icon: 🧩
    title: Teach it a new storage
    details: A backend filex has never heard of is a separate program you install from the admin panel — it describes its own config form, and its driver then behaves like any built-in one. Any language; a Go SDK makes it three methods. Every capability it claims is probed before anyone can build a storage on it.
    link: /PLUGINS
    linkText: Plugin docs
  - icon: 📊
    title: What your storage costs
    details: filex does not meter your provider's bill — it reads the report the provider already writes and prices it with a table you can edit. Backblaze B2's daily report is read over the same S3 API filex already speaks, and the free allowances are shown beside the billable lines, because "your downloads were free this month" is usually the thing worth knowing.
    link: /USAGE
    linkText: Usage & cost docs
  - icon: 🖼️
    title: Thumbnails you can read
    details: A PDF shows its first page, top-anchored so the title is in the card; a video its first frame that is not black, because a fade-in used to produce a black square; an Office document its rendered first page; and a text, code or CSV file its own first lines instead of repeating the extension the row already prints. A server missing ffmpeg, ghostscript or libreoffice says so in its log at boot rather than quietly drawing coloured rectangles.
    link: /thumbnails
    linkText: Thumbnail docs
  - icon: 💾
    title: Mount it as a drive
    details: filex mount attaches a remote server over ordinary HTTPS — a folder on Linux, a drive letter on Windows. Not a sync — it opens one file out of a hundred thousand without downloading the rest.
    link: /PROTOCOLS#filex-mount
    linkText: How to mount
  - icon: 🌐
    title: WebDAV
    details: Mount your drives in Finder, Explorer or davfs2 — every storage served over one WebDAV endpoint.
    link: /WEBDAV
    linkText: WebDAV docs
  - icon: ⌨️
    title: CLI
    details: The same binary doubles as a remote client — script uploads, downloads and syncs over the public REST API.
    link: /CLI
    linkText: CLI docs
  - icon: 🧭
    title: Navigation people already know
    details: 'A left panel led by one "+ New" menu — upload files, a new folder, a new document, a file request; the destinations Home, My files, Shared with me, Recent, Starred and Trash; your tags, each one opening the files carrying it; and the storages you can reach — a storage somebody granted you simply appears there, one click, no mount instructions. It is also where "How to connect" and your own API keys live, so an embedded copy of the explorer can hand a user the credential WebDAV or FTPS asks for — unless the embed is proxied with one shared app token, in which case the surfaces that belong to a single person are left out. Collapse it to an icon rail from the top bar when you want the width back. Everything around it is one shell, drawn by every embed with no string passed — one search field in the header with its palette shortcut, a Type/People/Modified/Size filter row, Folders and Files as sections, and Details/Activity in the info panel — and uiProfile ''simple'' reduces it for people who want a file drive rather than a file manager.'
    link: /INTEGRATION
    linkText: Turning it on
  - icon: 🧩
    title: Embeddable UI
    details: Drop the explorer into any app as a Vue 3 or React component, or a framework-free web component.
    link: /INTEGRATION
    linkText: Integration docs
  - icon: 🤖
    title: MCP for AI agents
    details: A token-authenticated automation surface that speaks Model Context Protocol — let agents browse, read and write files, and hand them credential-free upload tickets for files too big to pass through a model.
    link: /MCP
    linkText: MCP docs
  - icon: 🛡️
    title: RBAC
    details: Roles plus per-file and per-folder permissions with inheritance — enforced in the backend, off by default.
    link: /RBAC
    linkText: RBAC docs
  - icon: 🔐
    title: End-to-end encrypted folders
    details: "Encrypted in the browser with WebCrypto: the server stores ciphertext and never receives a key. Each folder gets a recovery key, shown once, so a forgotten password is not automatically lost data — and an operator can optionally hold an escrow key, with the limits stated rather than implied."
    link: /E2E-ENCRYPTION
    linkText: How it works
  - icon: 🏢
    title: LDAP / Active Directory
    details: Directory accounts sign in on the same password form as local ones — and on WebDAV, SFTP, FTPS, S3 and NFS too. Private CA supported; local login stays first, so your break-glass account works while the directory is down.
    link: /LDAP
    linkText: LDAP docs
  - icon: ⚡
    title: Live updates and presence
    details: An open explorer is told what changed over a WebSocket instead of polling for it, and shows who else is in the folder and what they are looking at. One write appears the moment it lands; a burst — a zip extraction, a folder upload, an NFS client writing chunk after chunk — is merged into one frame per window, so five thousand files arriving at once cost the page a bounded trickle rather than five thousand redraws.
    link: /REALTIME
    linkText: Realtime docs
  - icon: 🔔
    title: Webhooks
    details: Any number of webhook targets, each with its own signing secret and its own list of events, plus a persistent in-app bell — all filled from the same single call. A file that arrived and a file somebody replaced are different events, and an infected upload, a failed one or an encrypted folder opened with its recovery key can each be subscribed to on their own.
    link: /NOTIFICATIONS
    linkText: Notifications docs
  - icon: 🦠
    title: Antivirus
    details: Optional ClamAV scanning on every write, including the built-in editor and files the storage sync discovers — through a local binary or a clamd container over the network — with quarantine, retention windows and infected-file events.
    link: /PROTECTION
    linkText: Protection docs
---

<!-- This file is the docs.filex.sh home page (VitePress `layout: home`).
     The documentation itself lives in the sibling *.md files. -->
