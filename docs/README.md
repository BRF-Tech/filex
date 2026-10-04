# filex documentation

Self-hosted file manager - a Go backend with a Vue 3 / React / Web Component
frontend, and pluggable **storage**, **auth**, **DB** and **queue** drivers.

New here? Start with [Installation](INSTALLATION.md), then add a storage
([Storage](STORAGE.md)) and, if you want, sign-in via [SSO](SSO.md).

## Getting started

- [Installation](INSTALLATION.md) - minimal → full Compose → Helm → binary
- [Configuration](CONFIGURATION.md) - every `FILEX_*` variable + `config.yaml`
- [Admin panel](ADMIN-PANEL.md) - the administrator's menu: where every page
  lives (Files & storage, People & security, System), who is offered which
  page, the keyboard, screen readers and the phone's drawer
- [Databases](DATABASES.md) - SQLite, PostgreSQL, MySQL/MariaDB: which to pick,
  what each needs, and what "supported" is checked to mean
- [Releases](RELEASES.md) - every release with a plain-English summary
  (generated from the GitHub releases when a release is cut, by
  `npm run releases`)
- [Updates](UPDATES.md) - how filex checks for, and installs, a new release
- [Demo mode](DEMO.md) - running filex as a public playground: the published
  account (counted per address, never locked for everybody), the read-only
  guard over the whole admin surface - the MCP admin tools included - and what
  a demo does *not* publish (the visitors' and the operator's addresses)

## Storage

- [Storage](STORAGE.md) - how mounts work, adding one, and the adapters:
  local · S3 / S3-compatible · SFTP · WebDAV · FTP · SMB/CIFS - including
  [symlinks inside a local storage](STORAGE.md#symlinks) and the
  *Follow symlinks that leave this folder* option
- [Moving files between storages](STORAGE.md#moving-files-between-storages) - what
  copy, cut and drag mean when the two ends are different storages
- [Ordering storages](STORAGE.md#ordering-storages) - the order the navigation
  panel lists the drives in: each person's own (drag, or the row menu), else the
  one the administrator set on the Storages page, else creation order
- [NAS over NFS / SMB](STORAGE.md#nas-nfs-smb-and-friends) - mount it with the
  OS, serve it with `local`, and the three traps that come with it
- [Slow storage](STORAGE.md#slow-storage) - what is already cached, and what is
  actually worth tuning
- [Lazy catalogue](LAZY-CATALOGUE.md) - `sync_mode: lazy` for big local
  trees: list the opened folder from disk at once, catalogue it first, fill in
  the rest in the background or only on open; the deletion-safety invariant,
  the watch budget and what search, sizes and usage say meanwhile
- [Usage & cost](USAGE.md) - reading the provider's own daily report, pricing it
  with a table you can edit, and the two rows that must never be added together
- [Storage plugins](PLUGINS.md) - teaching filex a backend it does not ship:
  installing one, upgrading it in place, and writing one (the protocol, the Go
  SDK, presigned URLs and multipart) - plus **conformance**, the probes that
  refuse a plugin which cannot do what it claims, and
  [updates from a source](PLUGINS.md#updates-from-a-source) that wait for your
  review, each plugin's [log](PLUGINS.md#plugin-log), and
  [an entry the storage could not answer for](PLUGINS.md#an-entry-your-stat-cannot-answer-for)
  (kept, marked, and left alone until the storage answers)
- [Apps (app plugins)](APP-PLUGINS.md) - sandboxed WebAssembly apps that add
  actions to the file menu, screens filex draws for them, and public links for
  outside participants: the four that ship (**e-Signature**,
  `BRF-Tech/filex-sign`, **Convert**, `BRF-Tech/filex-convert`, **filextext**,
  `BRF-Tech/filextext-app`, and **draw.io**, `BRF-Tech/filex-drawio`), installing
  one from GitHub through the permission review,
  [install requests](APP-PLUGINS.md#install-requests) (an API key asks, an
  administrator decides), what the administrator controls,
  [app permissions](APP-PLUGINS.md#app-permissions) handed out per role and per
  person, [updates](APP-PLUGINS.md#updates) that each wait for an
  administrator and [going back](APP-PLUGINS.md#going-back) to the previous
  version, [an app's own interface](APP-PLUGINS.md#an-apps-own-interface) (a
  viewer for a file type, in a sandboxed frame), apps that wake up on their own,
  [signing documents end to end](APP-PLUGINS.md#signing-documents-end-to-end)
  (inside and outside signers, PINs, deadlines, the audit trail, verifying, your
  own certificate authority), [converting files](APP-PLUGINS.md#converting-files),
  [default apps](APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail)
  (which app opens a kind of file and which draws its thumbnail, Open with and
  "Always use this app"), what guards an app's public links, limits,
  troubleshooting
- [Writing an app plugin](PLUGIN-KIT.md) - the manifest (and the
  [user permissions](PLUGIN-KIT.md#user-permissions-what-an-administrator-hands-out)
  an app declares for the administrator to hand out), the exports,
  every host function and its permission, the screen catalogue, **an app's own
  interface** (HTML/JS in a sandboxed frame, the `@brftech/filex-app-ui` SDK,
  with or without a module), the **hourly
  wake-up** that lets an app schedule its own work to the minute, the test kit
  that runs before the wasm build, and a signing walk-through with stock Go
- [App plugin wire contract](APP-PLUGINS-API.md) - the exact routes, JSON
  shapes and frontend conventions the explorer, admin panel and public shell
  are built against

## Language & direction

- [Writing a language pack](PLUGIN-KIT.md#writing-a-language-pack) - adding a
  language to filex without waiting for a release: what makes a manifest a
  language pack, the exported catalogue and the per-key context that comes with
  it, the byte limits, CLDR plural categories (`<key>_few` …), the `server.*`
  table that carries the text the server writes - mail, notifications, the
  pages behind a link - and the validator that refuses a pack this version
  would not accept. Spanish, German and French ship as examples, and
  `BRF-Tech/filex-lang-template` is the template to start from
- [Right-to-left languages](RTL.md) - how filex turns for Arabic, Hebrew,
  Persian, Urdu: the one list that decides, the `dir` rule for pages and
  embeds, what never mirrors (document space, machine text), mixed-direction
  text, and the logical-properties rule the RTL guard test enforces

## Reaching filex without a browser

- [Protocols](PROTOCOLS.md) - the map: which protocols filex connects *to*, which
  it can be *reached as*, and the credential each one takes
- [WebDAV](WEBDAV.md) - mapping a drive from Windows / macOS / Linux
- [`filex mount`](PROTOCOLS.md#filex-mount) - a remote server over ordinary
  HTTPS: a folder on Linux, a drive letter on Windows; ⚠ not a sync, and not
  available on macOS
- [CLI client](CLI.md) - `filex client` and `filex sync` against a remote server:
  [copy and move](CLI.md#mkdir--rm--mv--cp) across storages, [the trash and
  versions](CLI.md#trash--versions), [tags](CLI.md#tag), [app
  actions](CLI.md#actions--run), [archives](CLI.md#archive), [plugin install
  requests](CLI.md#plugin-requests), [signing in to a tenant by its
  realm](CLI.md#multi-tenant-servers-the-realm) and
  [`filex encrypt`](CLI.md#filex-encrypt---make-a-folder-an-encrypted-folder)
  from the command line; a saved session goes only to the address it was saved
  with ([connecting](CLI.md#connecting))

## Authentication & access

- [SSO (OIDC)](SSO.md) - sign in with Keycloak / Auth0 / Authentik / Okta / …
- [LDAP & reverse-proxy auth](LDAP.md) - Active Directory / LDAP, header auth
- [Sign in with an operating-system account](OS-LOGIN.md) - Windows (local or domain) and Linux PAM,
  the first-sign-in rule, the e-mail token, and how the provider is tested
- [Sign-in attempt limits](CONFIGURATION.md#sign-in-attempt-limits) - wrong
  passwords counted per account and per address on the web form, WebDAV, FTPS
  and SFTP, the lock that doubles up to 15 minutes, the IP allow-list, the
  **Sign-in security** page and its API, and
  [`FILEX_TRUSTED_PROXIES`](CONFIGURATION.md#server--networking): the proxies
  whose `X-Forwarded-For` filex believes - by default
  [`auto`](CONFIGURATION.md#trusted-proxies), worked out from where filex runs
  (a proxy on the host in front of the container, on another machine, or a
  public hop such as a CDN's edge has to be listed; the page names one that
  is not)
- [Requests from other origins](CONFIGURATION.md#requests-from-other-origins) -
  why a change sent from another site with a visitor's session is refused
  (`403 cross_origin_refused`), what passes with no setting, and
  `FILEX_CORS_ALLOWED_ORIGINS` for a page of yours that needs it
- [RBAC, folder access & API tokens](RBAC.md) - account roles, per-storage RBAC, per-item grants
  (**Admin → Folder access**), what an API token's verbs allow on every surface,
  and the acts that need an administrator signed in
- [Roles & per-user permissions](PERMISSIONS.md) - what an account may do: 29
  permissions, built-in and custom roles, per-person exceptions, delegated
  admins, the permissions installed apps declare, public links that follow
  their creator's right to share, per-protocol enforcement
- [Groups](GROUPS.md) - named sets of people: folder access and a role for
  everyone in them, members by hand or through the groups a sign-in carries
  (OIDC, LDAP, operating system, header proxy), per tenant

## Integrations

- [OnlyOffice](ONLYOFFICE.md) - in-browser editing of Office documents, New
  document, and [drafts](ONLYOFFICE.md#drafts-nothing-is-in-the-folder-until-you-save):
  nothing is in the folder until you save; the three addresses that must work,
  the **Test** that fetches through a document's own door and warns when JWT is
  off, and [what "Download failed" was](ONLYOFFICE.md#failure-editor-shows-download-failed)
  (the editor says which of its two failures it was), and
  [CSV files](ONLYOFFICE.md#csv-files): a `.csv` opens in ONLYOFFICE's
  spreadsheet and is saved back as the same kind of CSV (0.51)

## Features

- [Desktop app](DESKTOP.md) - Windows/Linux/macOS app: multiple accounts, background sync,
  [dragging files out onto the desktop](DESKTOP.md#dragging-files-out),
  [opening Office documents off your own disk](DESKTOP.md#opening-documents-from-your-computer),
  [a portable Windows copy that installs nothing](DESKTOP.md#portable-windows),
  [the notification bell and your account menu in the window](DESKTOP.md#notifications-and-your-account),
  and on Linux [Chromium's sandbox, always](DESKTOP.md#appimage-on-recent-ubuntu)
  (the one-time AppArmor profile an AppImage needs on recent Ubuntu, the snap's
  `browser-sandbox` connection)
- [Folder sync](SYNC.md) - how a folder on your PC is kept in step with the server
- [Uploads](UPLOADS.md) - the staged, resumable upload path: chunked, works on
  every driver, survives a dropped connection
- [Sharing & file requests](SHARING.md) - public download links + upload/file-drop;
  **My shares** and reading a link's PIN back, the one branded public screen and
  its PIN lock-out, the maximum link life, how the folder-ZIP cache is bounded,
  and [a link that follows its creator](SHARING.md#a-link-follows-its-creator)
- [Thumbnails](thumbnails.md) - image, SVG (a built-in engine, on every
  install), HEIC/AVIF, video, PDF, Office, text and archive previews;
  [folder previews](thumbnails.md#folder-previews); thumbnails that follow
  their file; [thumbnails drawn by apps](thumbnails.md#thumbnails-drawn-by-apps-one-chain-per-kind);
  and [Admin → Tools → Thumbnail repair](thumbnails.md#repair-catching-up-existing-files)
- [Search](SEARCH.md) - embedded full-text index: forgiving filename
  matching (separators, several words in any order, folders, typos), VS
  Code-style subsequence scoring, ranked results, `tag:` filters, and an index
  that rebuilds itself after an upgrade without going dark
- [Realtime updates & presence](REALTIME.md) - the WebSocket an open explorer
  runs on: the ticket, the change and presence frames, how a burst is
  coalesced (and why a plain trailing debounce starves), and the 12 s polling
  fallback
- [Notifications](NOTIFICATIONS.md) - webhook + in-app bell: the unread badge,
  the full list inside the explorer for everybody, and where a click goes
- [Trash & versioning](TRASH-VERSIONING.md) - soft-delete/restore + file history
- [Replication](REPLICATION.md) - primary→replica mirroring & reconcile
- [Quotas](QUOTAS.md) - per-user ceilings: what counts, when it is
  released, and how a public drop link is billed
- [Protection & antivirus](PROTECTION.md) - ClamAV scanning, through a local
  binary or a clamd daemon over TCP or a unix socket, plus the trash and version
  retention windows, the share-link life ceiling and how many drafts a person
  may keep, behind one admin screen
- [Archives](ARCHIVES.md) - ZIP/7z/TAR creation, multi-format extraction and encryption providers
- [End-to-end encryption](E2E-ENCRYPTION.md) - client-side WebCrypto folders
  at a level of the owner's choosing: contents only (the default), or contents
  and names; the server stores ciphertext and never receives a key. Encrypting
  a folder you already have, a single file (`.fxe`), files of any size (the
  streamed format), downloading a decrypted copy, recovery keys, optional
  operator key escrow, `filex decrypt` for taking a folder or a file out,
  `filex encrypt` for making one on the command line, exactly what each
  one can and cannot open, and [who may encrypt](E2E-ENCRYPTION.md#who-may-encrypt):
  a tenant's policy, the files.encrypt permission and an administrator's
  approval, asked for with a reason (0.51)
- [End-to-end encryption roadmap](E2E-ROADMAP.md) - what is built
  (`filex encrypt` and large files in an in-place conversion since 0.50) and
  the design of the vault level, what is left, with open questions and estimates
- [Multi-tenancy](MULTI-TENANCY.md) - provider/tenant mode, per-tenant isolation
  on one instance, and [realms](MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for):
  the tenant's sign-in name - the Realm field, `realm/name` over SFTP, the
  tenant's own address on WebDAV and FTPS, and the handoff to a tenant's address
- [Tenant self-service](TENANT-ADMIN.md) - the Tenants screen, sign-in providers
  bound to tenants, a tenant's own OIDC and LDAP, platform subdomains, own
  domains proven by a CNAME and their certificates
- [ShareX](SHAREX.md) - the screenshot-upload endpoint and its custom uploader

## Deployment

- [Deployment](DEPLOYMENT.md) - reverse proxy, HTTPS, serving under a sub-path (`/filex/`), scaling, backup
- [Docker](DOCKER.md) - images, compose details, and which user the container runs as
- [Metrics](METRICS.md) - the Prometheus surface, how to scrape it, and
  the handful of alerts worth having
- Packaging in the repo: [`deploy/compose/`](../deploy/compose/) (minimal + full)
  and [`deploy/helm/filex/`](../deploy/helm/filex/) (Kubernetes)

## Develop & integrate

- [Architecture](ARCHITECTURE.md) - how the pieces fit
- [Backend](BACKEND.md) - internals; the routes under `/api/files` and `/api/ai`
  are also an [OpenAPI 3.1 description](../backend/internal/api/openapi.json),
  held to the router by a test
- [HTTP / component API](API.md)
- [Themes & appearance](INTEGRATION.md#themes) - the shipped palettes, an
  operator's own themes and instance default (Admin → **Appearance**), and the
  custom stylesheet that is off until you switch it on
- [Embedding the explorer](INTEGRATION.md) - Vue / React / Web Component, and the
  two options every wrapper shares: the **navigation panel** (`sideNav`) and how
  much of the explorer to show (`uiProfile`: `standard` · `simple` - two values,
  and the third one, `drive`, was **removed** after v0.40.0; pass `simple`)
- [AI & MCP](MCP.md) - API tokens (including the `user` / `app` token kinds), the permissions a token names - at least one, and never a blank list meaning all of them, and holding on every surface the token reaches - the ceiling that stops a narrow token issuing a wider credential (`403 token_ceiling`), the MCP endpoint for agents, the [plugin tools](MCP.md#plugin-tools) that read plugins and leave install requests, and credential-free upload tickets for large local files

## Repo only - not published to docs.filex.sh

These live in the repo and are deliberately kept off the published site
(`srcExclude` in `docs-site/.vitepress/config.mts`) - ⚠ a page VitePress builds
is reachable by URL and indexable whether or not anything links to it, so
"leave it out of the sidebar" is not the same as "do not publish it".

⚠ Linked by full URL rather than relatively: a relative link to a page the site
does not build is a **dead link that fails the docs build**, which is how this
list was written the first time.

- [Cloud preparation](https://github.com/BRF-Tech/filex/blob/main/docs/CLOUD.md) -
  scaffolding for a hosted offering that has **not** launched; publishing it
  would announce a service that does not exist
- **This page.** `docs/README.md` is the index GitHub renders when you open
  `docs/`, and it stays in the repository - but the site already has a home
  page and a sidebar, so publishing it put a second, competing table of
  contents at `/README`, one whose last section explains which pages are kept
  off the site. That is a note for contributors, not for readers

⚠ Three more are excluded from the site **and** from this repository, so they
are deliberately listed without links - a deployment runbook carrying one
installation's real host names and paths, and two migration guides written for
downstream applications that were never public (`MIGRATION.md`, off the in-tree
`@brftech/file-explorer`, and `MIGRATION_FISHAPP.md`). They would be dead links
here, which is exactly the failure the note above describes.

> `MIGRATION.md` was the third case and did not look like one: it was stripped
> from the public repository like the other two, but nothing removed the links
> to it, and it was not in `srcExclude` - so the published site carried it
> while the published README pointed at a file that is not there.

Handover notes under `docs/handovers/` are excluded by glob for the same
reason: they are working notes between maintainers, and the next one must be
excluded by default rather than by somebody remembering to add it.

---

Found something wrong or missing? Please open an issue - see
[CONTRIBUTING.md](CONTRIBUTING.md).
