---
layout: home
title: filex — self-hosted file manager
titleTemplate: false

hero:
  name: filex
  text: Self-hosted file manager
  tagline: One Go binary. Connect it to local disks, S3, SFTP, WebDAV, FTP or a NAS — and reach it back as S3, SFTP, FTPS, NFS or a mounted drive.
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
  - icon: 🖥️
    title: Desktop app
    details: The same explorer in its own window on Windows, Linux and macOS — several accounts at once, and sync that keeps running when the window is closed.
    link: /DESKTOP
    linkText: Desktop docs
  - icon: 🔁
    title: Folder sync
    details: A folder on your PC kept in step with a folder on the server, both ways. The first sync deletes nothing and a delete never beats an edit.
    link: /SYNC
    linkText: Sync docs
  - icon: 🔍
    title: Content search
    details: Find files by name or by what is inside them — an embedded full-text index across every mounted storage.
    link: /SEARCH
    linkText: Search docs
  - icon: 🔌
    title: Speaks your tools' protocols
    details: Point rclone, restic, aws s3, WinSCP, FileZilla, a scanner that only learned FTP or a media player that only learned NFS straight at filex — S3, SFTP, FTPS, NFS and WebDAV, all on the same tree with the same permissions and trash as the web UI.
    link: /PROTOCOLS
    linkText: Protocol docs
  - icon: 🧩
    title: Teach it a new storage
    details: A backend filex has never heard of is a separate program you install from the admin panel — it describes its own config form, and its driver then behaves like any built-in one. Any language; a Go SDK makes it three methods.
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
  - icon: 🧩
    title: Embeddable UI
    details: Drop the explorer into any app as a Vue 3 or React component, or a framework-free web component.
    link: /INTEGRATION
    linkText: Integration docs
  - icon: 🤖
    title: MCP for AI agents
    details: A token-authenticated automation surface that speaks Model Context Protocol — let agents browse, read and write files.
    link: /MCP
    linkText: MCP docs
  - icon: 🛡️
    title: RBAC
    details: Roles plus per-file and per-folder permissions with inheritance — enforced in the backend, off by default.
    link: /RBAC
    linkText: RBAC docs
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
