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
  - icon: 🌐
    title: A browser client for your users
    details: Give someone a user or viewer account and the address …/drive, and they get the file manager itself — their storages, uploads, sharing, search and the editor — with no admin panel around it and no separate frontend to deploy. …/admin is the operator's door to the same application.
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
    details: A folder on your PC kept in step with a folder on the server, both ways — from the desktop menu or the CLI. The first sync deletes nothing and a delete never beats an edit.
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
  - icon: 💾
    title: Mount it as a drive
    details: filex mount attaches a remote server over ordinary HTTPS — a folder on Linux, a drive letter on Windows. Not a sync it opens one file out of a hundred thousand without downloading the rest.
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
    details: 'A left panel with a prominent Upload; the views Recent, Starred, Shared with me and Trash; your tags, each one opening the files carrying it; and the storages you can reach — a storage somebody granted you simply appears there, one click, no mount instructions. It is also where "How to connect" and your own API keys live, so an embedded copy of the explorer can hand a user the credential WebDAV or FTPS asks for — unless the embed is proxied with one shared app token, in which case the surfaces that belong to a single person are left out. Collapse it to an icon rail when you want the width back, and switch the drive profile on for people who want a file drive rather than a file manager: one "+ New" menu, one search field in the header with its palette shortcut, a Type/Modified/Size filter row, Folders and Files as sections, and Details/Activity in the info panel.'
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
  - icon: 🔔
    title: Webhooks
    details: Every event fans out to a persistent in-app bell and an outbound webhook from a single call.
    link: /NOTIFICATIONS
    linkText: Notifications docs
  - icon: 🦠
    title: Antivirus
    details: Optional ClamAV scanning on upload, with quarantine, retention windows and infected-file events.
    link: /PROTECTION
    linkText: Protection docs
---

<!-- This file is the docs.filex.sh home page (VitePress `layout: home`).
     The documentation itself lives in the sibling *.md files. -->
