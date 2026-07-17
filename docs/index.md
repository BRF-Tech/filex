---
layout: home
title: filex — self-hosted file manager
titleTemplate: false

hero:
  name: filex
  text: Self-hosted file manager
  tagline: One Go binary. Your storage, your rules — local, S3, SFTP, WebDAV and more.
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
      text: GitHub
      link: https://github.com/BRF-Tech/filex

features:
  - icon: 🔍
    title: Content search
    details: Find files by name or by what is inside them — an embedded full-text index across every mounted storage.
    link: /SEARCH
    linkText: Search docs
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
