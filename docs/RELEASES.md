---
title: Releases
description: Every filex release with a plain-English summary of what changed - generated from the GitHub releases at release time.
---

<!-- GENERATED FILE - do not edit by hand. Your edits will be overwritten.
     Source:      GitHub Releases for BRF-Tech/filex
     Generator:   docs-site/scripts/fetch-releases.mjs
     Summaries:   docs-site/data/release-highlights.json (hand-written)
     Regenerate:  cd docs-site && npm run releases -->

# Releases

Every published filex release, newest first. This page is generated from the
[GitHub releases](https://github.com/BRF-Tech/filex/releases) by `npm run releases`, which is
run once when a release is cut - not by the site build, which would rewrite this
file on every contributor who ran it.

Whether filex installs a release by itself depends on which part of the version moved -
see [Updates](./UPDATES.md).

::: tip Latest - v0.50.0, 3 October 2026
Office documents open, convert and get their thumbnails through the connected ONLYOFFICE Document Server: LibreOffice is gone from every image and from filex, and the Test on the ONLYOFFICE card now downloads the way a document does. Groups, contributed by @manjotsc (#78): named sets of people with folder access and a role, kept in step with the groups a sign-in carries. People can sign in with the server's own Windows or Linux (PAM) accounts, wrong passwords are counted per account, and FILEX_TRUSTED_PROXIES names the proxies whose forwarded address is believed. Multi-tenant installs gain tenant realms, sign-in providers bound to tenants and self-service for a tenant's own domain; an OIDC sign-in finds its account by its SSO identity inside its own tenant, and a tenant's administrator can no longer change the built-in roles, found and fixed by @berkbasarir (#77). Also: SVG, HEIC, text, archive and folder thumbnails, Default apps per file type, e-Signature for PDFs only, and Chromium's sandbox required by every Linux desktop package. Read the upgrade notes first: LDAP accounts, realms, provider bindings and SSO trust change on upgrade.
:::

```bash
docker pull ghcr.io/brf-tech/filex:slim-v0.50.0
docker pull ghcr.io/brf-tech/filex:full-v0.50.0
```

## v0.50.0

<span class="filex-release-date">3 October 2026</span>

Office documents open, convert and get their thumbnails through the connected ONLYOFFICE Document Server: LibreOffice is gone from every image and from filex, and the Test on the ONLYOFFICE card now downloads the way a document does. Groups, contributed by @manjotsc (#78): named sets of people with folder access and a role, kept in step with the groups a sign-in carries. People can sign in with the server's own Windows or Linux (PAM) accounts, wrong passwords are counted per account, and FILEX_TRUSTED_PROXIES names the proxies whose forwarded address is believed. Multi-tenant installs gain tenant realms, sign-in providers bound to tenants and self-service for a tenant's own domain; an OIDC sign-in finds its account by its SSO identity inside its own tenant, and a tenant's administrator can no longer change the built-in roles, found and fixed by @berkbasarir (#77). Also: SVG, HEIC, text, archive and folder thumbnails, Default apps per file type, e-Signature for PDFs only, and Chromium's sandbox required by every Linux desktop package. Read the upgrade notes first: LDAP accounts, realms, provider bindings and SSO trust change on upgrade.

## What changed

> ⚠ **Desktop app on Linux: Chromium's sandbox is no longer optional**
> (Security). On Ubuntu 23.10 and later (24.04 LTS included) an
> AppImage needs a one-time AppArmor profile before it opens, and says so when
> it is missing - including an AppImage added to the menu by AppImageLauncher,
> Gear Lever or appimaged, whose entry used to turn the sandbox off. The Snap
> asks snapd for the sandbox: until the Snap Store connects that by itself
> (it reviews the permission by hand, so the new revision can wait for the
> review before it reaches stable), run
> `sudo snap connect filex-app:browser-sandbox` once; the app says so too. The
> `.deb` and the `.rpm` need nothing
> ([DESKTOP.md](./DESKTOP.md#appimage-on-recent-ubuntu)).
>
> ⚠ **LDAP: an entry with no e-mail attribute is `name@local` now**
> (Changed). An account an older filex opened under the bare name
> (`alex`) is adopted at that person's next sign-in - web or file protocol -
> and becomes `alex@local` (`alex@<realm>.local` in a tenant's realm on a
> multi-tenant install): the same account, its files, shares, permissions,
> role and quota unchanged, with an audit row `auth.account_adopted`. A local
> password on it is kept; an account bound to SSO is signed in to as it is,
> not renamed. Nobody is refused and nothing is left to do by hand. Set
> `FILEX_OS_LOGIN_EMAIL_TOKEN` **before** upgrading if `local` is not the
> token you want - it cannot be changed later. On a multi-tenant install only
> an account in the sign-in's own tenant is adopted: one in another tenant is
> left alone and the platform operator is told once
> (`ldap_legacy_account_elsewhere`), so re-home the directory accounts an older
> build left in the supertenant before upgrading. A file-protocol sign-in says
> which tenant it is for by its address or by `realm/name`; one that names
> nothing is the platform's own and adopts nothing (or pin `provider`)
> ([LDAP.md](./LDAP.md#accounts-an-older-filex-opened-under-the-bare-name)).
>
> ⚠ **Multi-tenant: on the platform's address an empty realm is the platform's
> own tenant** (Added, tenant realms). A tenant's people who used to
> sign in there with their e-mail alone type their tenant's **realm** in the new
> Realm field - or sign in on their tenant's own address, where the field is
> filled in for them. Over SFTP (and FTPS with no SNI, and WebDAV on the
> platform's address) they write `realm/name`.
> Existing tenants' realms are their slugs; an API token or an SSH key needs no
> realm. Single-tenant installs are unaffected.
>
> ⚠ **Multi-tenant on SQLite or PostgreSQL: two tenants whose slugs differ only
> in case stop the upgrade.** Migration 00073 gives every tenant the realm
> `LOWER(slug)` under a unique index, so `Acme` and `acme` cannot both have
> one: the migration fails and the server does not start until one of them is
> renamed (MySQL's default collation never let such a pair exist). Look
> before upgrading, and change one slug while still on 0.49:
> `SELECT LOWER(slug), COUNT(*) FROM providers GROUP BY LOWER(slug) > HAVING COUNT(*) > 1;`. The realm is copied once and never changes after
> that
> ([MULTI-TENANCY.md → Realms](./MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)).
>
> ⚠ **Multi-tenant: sign-in providers are bound to tenants now**
> (Added, tenant self-service). The upgrade binds **every** provider
> that exists (OIDC, LDAP, the proxy header, Windows, Linux PAM, environment
> and page alike) to **every** tenant that exists, which is what happened
> before: one directory served every tenant. **Review the bindings** on Admin →
> Identity providers (the page says so until you have) and remove the tenants
> a provider should not serve. A provider added after the upgrade serves the
> platform's own tenant only, and a tenant created after it has no shared
> provider until one is bound. `FILEX_LDAP_PROVIDER` / `FILEX_HEADER_PROVIDER`
> keep working and count as a binding to the pinned tenant. A tenant's own
> OIDC on its provider row is unchanged. Single-tenant installs read no
> bindings ([TENANT-ADMIN.md](./TENANT-ADMIN.md#moving-existing-installs-over-nothing-breaks-on-upgrade)).
>
> ⚠ **SSO: which account a sign-in opens** (Security). An OIDC
> sign-in finds its account inside the tenant it is for only, by the SSO
> identity the account is bound to (`iss` + `sub`), and by the email address
> only while the account is bound to none - and then only when the provider
> says `email_verified: true` or its addresses are trusted. On upgrade:
> - **Every OIDC provider that exists keeps its old behaviour**: its new
>   setting **Trust this provider's email addresses** comes on, and its card
>   says the upgrade set it. Switch it off on a provider that sends
>   `email_verified`. A provider added after the upgrade starts with it off.
>   The environment's OIDC follows `FILEX_OIDC_TRUST_EMAIL`; unset, it trusts
>   when the database had accounts that came through SSO (decided once, at
>   the first start of 0.50), and not on a fresh install.
> - With trust off, an unverified address opens no existing account that is
>   bound to no identity yet (`email_unverified`), and a new account for it is
>   opened **switched off** (`account_pending`): switching it on in Admin →
>   Users approves it.
> - An account is bound to the identity it first signs in with. **Changing
>   the identity provider, or its issuer address (a renamed Keycloak realm),
>   refuses its accounts** (`identity_mismatch`) until each account's SSO
>   bind is removed (Admin → Users → Remove SSO bind, `sso_unlink`).
> - Multi-tenant: an SSO callback that comes back without its flow cookie is
>   refused (`expired`); a sign-in in flight during the upgrade starts again.
>   A tenant's account signing in through OIDC in maintenance mode gets the
>   generic answer now, not `maintenance`.
> ([SSO.md](./SSO.md#which-account-an-sso-sign-in-opens))
>
> ⚠ **A forwarded client address is believed only from a trusted proxy now**
> (Changed, `FILEX_TRUSTED_PROXIES`). Up to 0.49 every
> `X-Forwarded-For` was believed, whoever wrote it. The default is `auto`,
> worked out from where filex runs: this machine (loopback) and, in a
> container on a network of its own (Docker, Podman, a Kubernetes pod), the
> other containers on that network - never the network's gateway, never
> filex's own address, never the LAN, never a macvlan or ipvlan network. A
> proxy container next to filex, and a proxy on the same machine as a plain
> install, need nothing. **A proxy on the host in front of filex's container**
> (nginx reaching a port published on `127.0.0.1` arrives from the Docker
> gateway), **a proxy on another machine**, an ingress controller on another
> Kubernetes node, a proxy that reaches filex over `100.64.0.0/10` (Tailscale,
> Cloudflare WARP) and a CDN in front of your proxy must be listed, keeping
> the word - `FILEX_TRUSTED_PROXIES=auto, 172.18.0.1`. Until they are, every
> visitor resolves to the proxy's address: the access log, the audit log, the
> file-request limit and the new sign-in limit all see that one address, and
> one visitor's wrong passwords lock out everybody who arrives through it.
> **Admin → Sign-in security** names such a proxy - a peer that sends
> forwarded addresses without being trusted - and adds it in one click; the
> log names it too. Helm: list the cluster's pod network
> (`trustedProxies: "auto, 10.244.0.0/16"`). Clients on a LAN that reach
> filex with no proxy in front need nothing set
> ([CONFIGURATION.md → Trusted proxies](./CONFIGURATION.md#trusted-proxies)).
>
> ⚠ **Apps and storage plugins are downloaded from public addresses only**
> (Security). An app installed from an address or a repository,
> an install request, the update check and a storage plugin from a URL or a
> source now refuse an address on this machine, on the private network or of
> the cloud metadata service - judged after DNS and on every redirect - and a
> redirect from `https://` to plain `http://`. An administrator who installed
> from an internal server is refused now: upload the files instead (an app
> installed that way earlier says *Could not check* at the update check).
> `FILEX_PLUGIN_LOOPBACK_SOURCES=1` opens this machine, and nothing else, for
> development and tests - never on a server
> ([CONFIGURATION.md](./CONFIGURATION.md#storage-plugins)).
>
> ⚠ **Storage plugins** (Security): a plugin keeps the driver
> name it was first accepted with - another plugin may not take it, even while
> the first is off; a remote plugin that answers with a redirect is refused
> (register the address it points to); and a share download goes straight to a
> plugin's presigned address only when that address is `https://`.
>
> ⚠ **The bundled S3 server is Versity S3 Gateway, not MinIO** (Changed).
> `minio/minio` is no longer published, so a Compose stack or Helm release that
> bundled MinIO copies its bucket across once: the `versitygw` Compose profile
> and `versitygw.enabled` in Helm replace `minio`, and MinIO's own data format
> is not readable by the new server. Nothing is deleted for you: Compose keeps
> the `minio-data` volume, and the chart refuses to render while
> `minio.enabled` is `true`, with the commands that keep the old volume and
> MinIO serving while you copy
> ([STORAGE.md → Moving off the bundled MinIO](./STORAGE.md#moving-off-the-bundled-minio)).
>
> ⚠ **LibreOffice is gone: office conversions and thumbnails need a connected
> ONLYOFFICE** (Changed, Removed). The full image
> drops LibreOffice and its Java runtime (about 420 MB less to download and
> 1.1 GB less on disk: 642 → 225 MB compressed), and filex no longer runs a
> LibreOffice installed next to the bare binary either. Office documents are
> the ONLYOFFICE Document Server's you connect under **External services**:
> their thumbnails, and the apps' office engine - the Convert app's Word,
> Excel, PowerPoint and OpenDocument conversions, PDF/A included. With
> ONLYOFFICE connected there is nothing to do: the engine uses the editor's
> address, secret and callback address and turns on without a restart, and
> the thumbnails LibreOffice drew before are kept and drawn again by
> ONLYOFFICE, one document at a time, as they are listed (or all at once with
> a *Fix* repair). Without it, office documents show their type icon instead
> of a thumbnail and the Thumbnail repair tab says "ONLYOFFICE is not
> configured", the converter's office targets are greyed for an administrator
> with "connect it under External services", and a job that asks anyway says
> so - nothing fails silently; documents still open in the read-only preview
> and **+ New** still makes them. An app that asks for `engines:libreoffice`
> keeps working: it is the same engine as `engines:office`, and the same
> grant. ONLYOFFICE does not make HTML from a spreadsheet or one CSV per
> sheet, as LibreOffice did
> ([ONLYOFFICE.md](./ONLYOFFICE.md#office-conversions-for-apps-the-office-engine),
> [thumbnails.md → Office through OnlyOffice](./thumbnails.md#office-through-onlyoffice)).
>
> ⚠ **A page on another origin that changes things with filex's session cookie
> needs that origin in `FILEX_CORS_ALLOWED_ORIGINS`** (Security).
> The default `*` does not grant it, and a page on a sibling subdomain is
> another origin too. An embed with a bearer token, a host that proxies with a
> key (the recommended pattern), the desktop app, the installed web app, a
> dashboard that frames filex, share and drop links, upload tickets, WebDAV and
> S3 clients, and scripts are unaffected. A refused change answers
> `403 cross_origin_refused`.
>
> Also on upgrade:
> - The operating-system providers (`windows`, `pam`) are switched on from
>   **Admin → Identity providers** only, after their test. Written into
>   `FILEX_AUTH_DRIVERS` or `auth.drivers`, either is refused at start with a
>   warning, and the other providers start as usual.
> - A link into docs.filex.sh at a heading that held a long dash has a new
>   fragment (`#token-kinds--user-vs-app` is `#token-kinds---user-vs-app`);
>   the old one opens the top of the right page (Changed).
> - The CLI's saved session (`~/.filex/cli.yaml`) is used only at the address
>   it was saved with: a script that reaches the same server by another name
>   or an IP signs in there once, or passes `FILEX_TOKEN` (Security).
> - An agent (REST `/api/ai`, MCP) or a ShareX setup that writes into an
>   end-to-end encrypted folder is refused now (`409 E2E_PLAINTEXT_REFUSED`):
>   pass `allow_plaintext` where storing plaintext there is meant
>   (Security).
> - Read Security if you handed out folder-confined API tokens
>   (one could change sharing grants outside its folder, on any install) or
>   ran 0.49.0 multi-tenant (a tenant's administrator could rewrite the
>   built-in roles), and if you run OIDC (an identity provider could sign a
>   person in to another account, another tenant's on a multi-tenant install).

**This release has more to it than fits on one page.** The rest of the
entry - and every earlier release - is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0500---2026-10-02).

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.50.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.50.0`

## v0.49.0

<span class="filex-release-date">29 September 2026</span>

Roles and per-person permissions, contributed by @manjotsc (#75): 28 named permissions, one role per person built from them, per-person exceptions on top, and refusals that name the permission and the role in the reader's language; a custom role can carry its name in more than one language. Apps can declare permissions of their own, decided per role and per person on the same screens (the e-signature app's “Request signatures”), and are told which of them the person holds. An agent or an API key no longer installs or approves an app: it leaves an install request an administrator decides on the Plugins page. API-key scopes apply on every surface, account changes need write, a public link answers only while its creator may still make it, and the admin page called Permissions is now Folder access.

## What changed

Everyone has one role now - Administrator, User, Viewer or a custom role with
its own list of permissions - and an app can declare permissions of its own
that an administrator hands out the same way. An API key is held to its
`read`, `write` and `delete` verbs on every route and protocol, and the acts
that make somebody an administrator or install a plugin are a signed-in
person's: a key leaves an install request instead. Roles and per-user
permissions are manjotsc's pull request
([#75](https://github.com/BRF-Tech/filex/pull/75)).

> ⚠ **An API key's verbs now hold everywhere** (Security): an
> integration that changes or deletes files - through `/api/files` (and so
> `filex client`), the MCP file tools, WebDAV, SFTP, FTPS, or an S3 key or NFS
> export minted from a token - with a token that does not name `write` or
> `delete` gets `403 token missing scope: <verb>`. Changing the account's
> profile, password or two-factor with a token needs `write` too; the desktop
> app's pairing carries it for every role, a viewer's included, and a viewer
> who paired before 0.49.0 signs the app in again once (**Reconnect**). Give
> each token the verbs it uses; tokens issued before 0.43.0 carry the full
> list and are unaffected.

> ⚠ **Some acts are a signed-in administrator's only** (Security):
> installing, upgrading, going back, switching, removing and re-permissioning
> an app or a storage plugin; creating an administrator, promoting an account,
> changing an administrator's role or setting their password; and allowing an
> admin-area permission (`admin.*`) through an exception, a custom role or a
> built-in role. An API key gets `403 session_required` - for a plugin it
> leaves a request instead (`POST /api/admin/plugin-requests`). Taking a right
> away stays open to a key.

> ⚠ **A public link follows its creator's right to make it**
> (Security): a download link answers only while the person who
> made it still holds `share.links` and edit rights on the item (a file
> request: `share.upload_links`). A link whose creator has since become a
> viewer or lost the folder answers `404` after the upgrade, and answers again
> once the right is given back. Deleting an account now deletes the download
> links and file requests it opened. An app's own public pages are not
> affected, and neither are links whose creator was deleted before 0.49.0 -
> they name no creator.

> ⚠ **For app authors** (Added): filex before 0.49.0 refuses a
> manifest that carries `user_permissions` or `requires` (unknown fields are
> refused), so a manifest that uses them says `"filex": ">=0.49.0"`. An older
> server cannot read even that range: its update check says *Could not check*
> for such a release, rather than *needs a newer filex*. From 0.49.0 the
> update check says *needs a newer filex* for a version that carries a field
> this filex does not know (Fixed).

### Added

- **Everyone has one role, and a role is a list of permissions you choose**
  ([docs/PERMISSIONS.md](./PERMISSIONS.md)). 28 permissions - file actions
  (download, create, modify, rename, move, delete, permanent delete, tag),
  sharing, comments, the agent API, apps, each protocol, API keys, the desktop
  app, editing one's own account, and five admin areas - granted through
  **roles**. Everyone has **one role**, picked in the Role field on their page:
  Administrator, User, Viewer (the last two now editable), or a **custom
  role** - its own list of permissions, optionally different in some folders
  ("no delete, except in Scratch"). A custom role can be the starting role of
  new SSO accounts in given groups. Per-person exceptions beat the role. A
  custom role can carry limits - share-link lifetime and password, blocked
  file types, largest file, required two-factor authentication. Enforced on
  every door: the web app and REST API, the agent API, WebDAV, SFTP, FTPS, S3
  and NFS, and API keys. A refusal says which permission and which role, in
  the reader's language.
- **An app can declare permissions an administrator hands out per role and
  per person** ([docs/APP-PLUGINS.md → App permissions](./APP-PLUGINS.md#app-permissions)).
  The manifest's `user_permissions` names them - a signing app can put
  *Request signatures* behind one while signing what somebody sent you needs
  none - and an action or a screen that `requires` one is left out of the menu,
  and refused with `403 permission_denied` naming the permission and where the
  answer came from, for an account that does not hold it. A key is
  `app.<app>.<id>`; the answer is an administrator's always, else the person's
  exception, else their custom role's (`settings.apps`), else the built-in
  role's (`/api/admin/roles/builtin` → `apps`), else the app's own default -
  `viewer`, `user` or `admin`. `GET /api/admin/roles/catalogue` lists the
  installed apps' permissions under `apps`. **Admin → Roles** shows them as an
  **Apps** group in every role's editor, each *Default (…)* / *Allow* /
  *Deny*, *Default* saying what it comes to for that role; a person's page
  shows the same rows among their exceptions, beside the answer and where it
  comes from (the exceptions answer carries `effective.apps`). A preset and
  *Clear exceptions* leave a person's app exceptions as they are; the Apps
  group resets them on its own. A delegated administrator leaves a person's
  app permissions as they are, and sees them read-only.
- **An app is told which of its own permissions the person holds**
  ([docs/PLUGIN-KIT.md → User permissions](./PLUGIN-KIT.md#user-permissions-what-an-administrator-hands-out)).
  A job (`action_run`), a screen's event and an interface's `ui_call` carry
  `actor.permissions`: the ids of the app's `user_permissions` the person
  holds, decided by the same question filex asks at the door, so an
  administrator holds every one. A public page's event and work nobody
  started carry none. The Go SDK reads it with `Actor.Can(id)`, and
  `plugintest`'s default actor, an administrator, holds every permission the
  manifest declares. An app uses it to leave out a hint to an action the
  reader cannot run.
- **An API key asks for a plugin; an administrator installs it** (migration
  00070, `plugin_requests`). An API key - an agent, a script, the CLI - leaves
  a request to install or upgrade an app or a storage plugin, with a required
  reason. filex resolves the source at once and freezes what it answered: the
  manifest, the SHA-256 the bytes must have and the permissions it asks for.
  An administrator approves or rejects it under **Admin → Plugins → Install
  requests** (the explorer's table; the review shows the install wizard's
  permission list with the app's reasons). Approval installs exactly the
  frozen bytes with exactly the frozen permissions; a source that serves
  anything else by then closes the request as **Source changed**
  (`superseded`) and installs nothing. One waiting request per source; a
  request nobody decides expires after 14 days
  (`FILEX_PLUGIN_REQUEST_TTL_DAYS`). Supertenant-only in multi-tenant mode.
  REST: [BACKEND.md → Admin: plugin requests](./BACKEND.md#admin-plugin-requests).
  - **Plugin tools for agents** (admin MCP): `admin_app_plugins_list`,
    `admin_app_plugin_get`, `admin_app_plugin_logs`,
    `admin_app_plugins_check_updates`, `admin_plugins_list`,
    `admin_plugin_get`, `admin_plugins_check_updates`,
    `admin_plugin_request_install`, `admin_plugin_request_upgrade`,
    `admin_plugin_requests_list`, `admin_plugin_request_get` - reading and
    requesting only; there is no tool to approve or reject
    ([MCP.md → Plugin tools](./MCP.md#plugin-tools)).
  - **`filex client plugins request` / `filex client plugins requests`** -
    leave and follow a request from the command line
    ([CLI.md](./CLI.md#plugin-requests)).
  - **`plugin_requested`** notification: the administrators hear of a new
    request in the bell and on every webhook that carries operator alarms.
  - Audit entries `plugin_request.create`, `.approve`, `.reject`, `.expire`,
    `.supersede`.
- **Administration can be delegated**: an account without the admin role can
  be given users, folder grants, shares, audit or monitoring - never an
  administrator's account, never more than it holds. A change is judged by
  what the account would hold afterwards, so lifting a Deny, ending a
  restrictive role or creating a User account counts too; a password is reset
  or set only for an account that holds nothing the delegate lacks.
- **The desktop app for Windows on Arm and Linux arm64.** An arm64 installer
  and portable `.exe` for Windows, and an arm64 AppImage, `.deb`, `.rpm` and
  Snap Store revision for Linux - built by every release, installed and opened
  on arm64 machines before they are published, and updated from their own
  feeds (Windows: one feed, the x64 installer first, so existing installs see
  what they saw before). The Microsoft Store gets x64 and arm64 in one bundle,
  and the winget manifest names both installers. 0.48.1's arm64 packages were
  added to its release afterwards.
- **Every release runs the arm64 CLI, server and images on arm64 machines**
  (Linux, Windows, macOS): serve, sign in, upload, download byte for byte,
  move, delete - before anything is published.
- Admin → Users shows each person's role, exceptions and where every answer
  comes from; Admin → Roles lists the built-in and custom roles, in the
  explorer's table like every other admin list. Changes are audited with
  before and after.
- **A custom role in every language** (migration 00071): the role editor's
  *Name and description in other languages* gives a role its name and
  description in each interface language the server offers - the built-in
  ones and every installed language pack's. Everyone sees the role in their
  own language on the Roles, Users and person pages, and a refusal names it in
  the reader's account language. The role's own name stays required and is
  the fallback; `source.rule_name` and the audit log keep it.
  `POST` / `PUT /api/admin/roles` take `names` and `descriptions`
  (`{"tr": "…"}`); a language the server does not offer is refused
  ([PERMISSIONS.md → API](./PERMISSIONS.md#api)).
- SSO sign-ins record the provider's groups claim; a new account whose groups
  a custom role names starts with that role.

### Changed

- Every account keeps what its role could do before: migration 00069 only
  adds tables and columns, and the built-in User and Viewer roles start as the
  Standard user and Read-only presets.
- **Admin → Permissions is now Admin → Folder access** (*Klasör erişimi*):
  the page that lists every per-file and per-folder grant, apart from Roles.
- **The explorer reads the account's permissions itself** when its host
  passes none (`GET /api/auth/me`): the desktop app and the embedded explorer
  now hide the actions a role refuses, as the web app does, instead of
  offering them and answering `403`. An administrator is not narrowed, and an
  answer without permissions changes nothing.
- Explorer rename checks the new name as well as the old one, and the
  invite-by-email fallback that creates a public link needs the right to
  create public links.
- **A viewer's desktop app changes its own account.** Its pairing now carries
  `read,write` (never `delete`), so the account menu saves the profile, the
  password and two-factor as the web app does; the viewer role still refuses
  every file change, link and grant. A viewer who paired before 0.49.0
  presses **Reconnect** once ([DESKTOP.md](./DESKTOP.md#the-token-the-app-is-given)).

### Fixed

- **The Users page's search box filters the list.** It sent the text to a
  server that returns every account and never read it, so typing changed
  nothing.
- **The CLI inside the desktop app says which release it is.** It answered
  `filex --version` with "0.1.0-dev" in every package; it is now stamped with
  the version, commit and build date like the released CLI.
- **A dialog behind another no longer takes the focus from the one in
  front.** Every dialog puts the focus in itself shortly after it opens. When
  a second dialog opened over it in that moment (the "Close this draft?"
  question, when a draft was closed right after it was opened), the dialog
  behind took the focus a moment later: "Keep in Drafts" lost it to the
  draft's text box, and an Enter went into the text instead of answering the
  question. Only the dialog in front takes the focus now.
- **A file dropped through a file request over its creator's size limit
  answers `413 file_too_large`** with the drop page's own sentence, not
  `503 storage_unavailable`.
- **The Releases page on docs.filex.sh** keeps a release note's references to
  its own sections as plain words; they used to land in an older release's
  section of the same page.
- **Every door that makes a link is hidden without its permission.**
  *New → Request files* stayed in the menu for an account without
  `share.upload_links`, and the server refused it. Now the explorer asks
  the same permissions the server asks, per folder when the role says so, in
  every host: *Request files* and the share dialog's file-request section need
  `share.upload_links`, the dialog's link switch and options and the details
  panel's *Create link* need `share.links`, the people section and *Manage*
  need `share.users`. *Share* (and its key, Shift+S) is offered only when one
  of them applies - a file takes no file request.
- **The explorer's per-folder question works with a read-only token.**
  `POST /api/files/manager?action=allowed` changes nothing and now needs
  `read`; under `write` a read-only token's question was refused, and the
  explorer, reading that as "unknown", offered every action that differs from
  folder to folder.
- **An app update that needs a newer filex says so, even when this filex
  cannot read all of it.** A newer version whose manifest carries a field this
  filex does not know (or a newer `manifest_version`) made the update check
  answer *Could not check* with `unknown field`, as if the app were broken.
  The check now reads its name, version and range, and lists it as needing a
  newer filex - or steps over it to an older release that fits. Installing or
  upgrading to such a manifest is still refused.

### Security

- Harden plugin installation: installing, upgrading, going back, switching,
  removing and changing the action overrides of an app
  (`/api/admin/app-plugins`), and installing, upgrading, `PATCH` and `DELETE`
  of a storage plugin (`/api/admin/plugins`), now need an administrator signed
  in to the admin panel; an API key gets `403 session_required`, naming
  `/api/admin/plugin-requests`, where it leaves a request. Reading - the lists,
  one plugin, the logs, the update check and the install review
  (`?dry_run=1`) - stays open to an admin-scoped key
  ([APP-PLUGINS.md → Install requests](./APP-PLUGINS.md#install-requests)).
- Harden API token permissions: a token's verbs - `read`, `write`, `delete` -
  are now enforced on every route and protocol a token can reach: the web
  explorer's `/api/files` routes (and so `filex client`), the MCP file tools,
  WebDAV, SFTP, FTPS, and the S3 keys and NFS exports minted from a token.
  Without `write` a token keeps what a viewer keeps (preferences, stars,
  recently opened, personal tags, comments, notifications) and the document
  editor opens read-only; MCP `tools/list` offers only the file tools the
  token can use. Changing the account itself - `PATCH /api/auth/profile`,
  `POST /api/auth/password` and the three `/api/auth/totp/*` routes - needs
  `write` as well. ⚠ An integration that changes or deletes files with a token
  that does not name `write` or `delete` now gets `403 token missing scope`:
  give its token the verbs it uses. Tokens issued before 0.43.0 carry the full
  list and are unaffected. See [RBAC.md](./RBAC.md#api-tokens-verbs-on-every-surface).
- Harden public links: one rule makes a link on every surface - the explorer,
  `POST /api/ai/share` and the MCP `file_share` tool alike - edit rights on the
  item and the `share.links` permission (`share.upload_links` for a file
  request). A link answers only while its creator still holds that right on
  the item, and answers again when the right comes back; `/s/`, its metadata,
  folder browsing, `/d/` and `/api/public/*` ask the same question. A file
  request applies its creator's blocked file types and largest file to what is
  dropped ([SHARING.md](./SHARING.md#a-link-follows-its-creator)).
- Harden account deletion: deleting an account deletes the download links and
  file requests it opened, in the same transaction, on every path that deletes
  one - an administrator, a tenant deletion, a rolled-back provisioning. They
  used to stay with no creator, and a link with no creator kept answering.
  The deletion's audit entry records `links_closed`, the number still open.
  An app's own public pages stay.
- Harden administrator accounts: creating an administrator, promoting an
  account to administrator, changing an administrator's role, setting or
  resetting an administrator's password, and allowing an admin-area
  permission - as a person's exception, in a custom role (its list or its
  folder part), or in a built-in role - need an administrator signed in to
  the admin panel; an API key gets `403 session_required`, on `/api/admin`,
  `/api/ai/admin` and the `admin_users_*` MCP tools alike. Managing accounts
  that are not administrators, and taking a permission away, are unchanged.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0490---2026-09-28)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.49.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.49.0`

## v0.48.1

<span class="filex-release-date">28 September 2026</span>

Apps can bring an interface of their own - HTML and JavaScript filex serves from the approved package into a sandboxed frame, opened as the viewer of a file type, as a kind of file in New document, or handing you a file to keep - and nothing updates itself any more: every newer version of an app waits for an administrator's review, and Back to version undoes one. A new document is a draft until its first save (#71). Encrypted folders get a level, contents only (the default) or contents and names; a folder you already have, or a single file as a .fxe, is encrypted where it is, and an unlocked folder downloads decrypted. The desktop app has the notification bell and your account menu in its window. The iframe converter is gone (the Convert app replaces it), a dashboard that shows filex in a frame now needs FILEX_FRAME_ANCESTORS, an item deleted outside filex no longer shows in the Trash (#74), and several hardening fixes; upgrade. (0.48.0 was tagged but its release run failed a test and nothing was published; 0.48.1 carries it.)

## What changed

> 0.48.0 was tagged but its release run failed a test (where the draft
> close question put the focus, now fixed); nothing was published - 0.48.1
> carries it.

Apps can bring an interface of their own, a new document stays a draft until
its first save, encrypted folders can hide their names too and be made from a
folder you already have - or from a single file - and the desktop app gets the
notification bell and the account menu in its window. Every newer version of
an app now waits for an administrator, and the iframe converter is gone.

> ⚠ **Nothing updates itself any more** (Changed): 0.47 installed
> a newer version of an app by itself when it asked for no new permission.
> Now every version waits for an administrator's **Review update**; the
> per-app automatic-update switch is gone and `PATCH …/app-plugins/{id}` with
> `auto_update` answers `400`.

> ⚠ **filex in someone else's frame** (Changed): a dashboard that
> shows filex in an `<iframe>` (Homarr, Organizr and the like) needs its
> origin in the new `FILEX_FRAME_ANCESTORS`; otherwise the browser refuses the
> frame. The embedded explorer is not a frame and is not affected.

> ⚠ **The iframe converter is removed** (Removed): conversion is
> the Convert app. `FILEX_CONVERT_URL`, the compose `convert` profile and the
> Helm `convert` values are gone; a server that still sets the variable says
> so once, at start.

> ⚠ **For app authors** (Changed): the app platform API is
> narrower - paths an app passes must be the call's inputs or files it keeps
> state on, `share_create` and `file_lock` need **editor**, an `http:`
> permission names a host, and screen calls are capped per app. The two
> published apps are unaffected.

> ⚠ **For integrators** (Changed): `@brftech/filex-core` now
> depends on `@headlessui/vue`, `lucide-vue-next` and the new
> `@brftech/filex-app-ui`.

> ⚠ **Encrypted folders get a level, and level 2 is refused by older filex**
> (Added): level 1 (contents only) stays the default and keeps its
> v2 key file, which every filex since 0.31 opens. Level 2 (contents and
> names) - chosen in the create dialog, or later in the folder's settings -
> writes marker v3, which filex 0.47 and older will not open: on purpose,
> because they would upload files under their plaintext names next to the
> encrypted ones. Every folder that already exists stays at level 1.

> ⚠ **Large and single encrypted files need filex 0.48** (Added): a
> file over 200 MB in an encrypted folder is now written as a stream (header
> `0x02`) and a single encrypted file is a `.fxe`; filex 0.47 and older open
> neither. The folder, and its files up to 200 MB, still open there.

### Added

- **An app may bring its own interface**
  ([docs/APP-PLUGINS.md](./APP-PLUGINS.md#an-apps-own-interface)). Besides
  the screens filex draws, an app can ship HTML, CSS and JavaScript of its
  own - an editor, a viewer - and an app can be only that, with no
  WebAssembly module at all.
  - filex serves the interface from the app's package (a zip pinned by its
    SHA-256, checked once at install and never unpacked) and runs it in a
    sandboxed frame whose policy allows no connection, no storage, no cookie
    and no reach into the page around it. The page policy is built from what
    you approved, never from the package: scripts only from the package,
    `connect-src 'none'`.
  - Everything the interface does goes through filex over one checked
    channel: reading the files it was opened with, saving over them (a new
    version, or the draft it is), a new file where the person picks, asking
    the app's own module (`ui_call`), a toast, a question. The server checks
    every save again.
  - A new placement, **viewer**: an app opens a file type the way filex's
    own viewers do, and the file menu gets **Open with &lt;app>**. Unsaved
    changes are asked about before the interface closes; drafts (#71) work
    unchanged.
  - The install review has an **Interface** group: the package, every
    address outside it - **mirrored** (filex downloads it once and serves it
    itself) or **live** (the reader's browser fetches it; a permission, in
    yellow, with what that means) - the script-policy exceptions, and a
    plain note that a browser cannot fully stop a page sending data out
    (WebRTC). An upgrade that adds an address asks again.
  - An interface may **read its own package** at run time
    (`ui.package_fetch`, permission `ui:package-fetch`, on the review as
    "reads its own package") - what an editor that loads its own shape
    libraries and translations needs. Only that version's files: the page's `connect-src` and
    Chrome's `Connection-Allowlist` name the package's own path; another
    app, filex's API and the network stay closed. Every interface's
    allowlist now names its package path instead of filex's whole origin.
  - **New document** can make an app's kind of file (`new_documents`,
    [docs/APP-PLUGINS-API.md](./APP-PLUGINS-API.md#new-documents)): a row
    in the app's own words under **Apps**, made from the app's template (or
    empty) and opened in the app's viewer - a draft until its first save.
    Each kind is on the install review (`ui-new:.<ext>`); the rows are told
    to signed-in people only, while the app runs.
  - An interface may **hand you a file for your own disk** (`ui.download` in
    the manifest, `fx.download` in the SDK, permission `ui:download`): filex
    does it, on your click in the interface or your yes, never over 256 MiB -
    in Chromium streamed into the file you pick, elsewhere a download.
  - **The editor tab** (`/files/edit`, *Open in new tab*) opens a file in the
    app that views it, by the explorer's own rule (*Open with*'s choice
    travels as `app=<plugin>/<view>`); before, the tab showed filex's own
    viewer.
  - What holds an interface in: an address outside its package is a plain
    public https address - no IP, no internal name; its page is revalidated
    at every opening, so a narrowed grant takes effect at once; a signed
    app's module must describe the same interface as its manifest, and a
    mirrored file is served only as the kind it was declared; a viewer names
    the file types it opens, each a line of the review (`ui-viewer:.md`), so
    an update that widens them asks again; a save counts against the quota,
    and "save as" keeps to the view's file type and never writes into an
    encrypted folder; the interface is told an error code, never the
    server's text; a job or a clipboard write needs a gesture in its frame or
    the person's yes, and everything filex draws for it names the app; one
    frame's calls are capped, and its calls to the module share the app's
    screen-call ceiling; filex's pages frame interfaces by path
    (`<host>/_appui/`), so an interface cannot send its frame to another
    filex page; and the interface's page switches off every powerful browser
    feature (devices, sensors, credentials, ads).
  - For authors: the `@brftech/filex-app-ui` SDK (a few KB, no
    dependency), `pluginkit` `Plugin.UI` + `plugintest` `Harness.UICall`,
    and [Writing an interface](./PLUGIN-KIT.md#writing-an-interface).
    `FILEX_APP_PLUGIN_MAX_UI_MB` (128) caps a package; migration `00065`
    adds `app_plugins.ui_sha256`.
- **A new document is a draft until its first save**
  ([#71](https://github.com/BRF-Tech/filex/issues/71)). Choosing New
  document, naming it and closing the editor used to leave an empty file in
  the folder. Now Create makes a draft - a real file with the name and type
  you chose, kept in your own drafts area of that storage - and the folder
  gets nothing until you press **Save**
  ([docs/ONLYOFFICE.md](./ONLYOFFICE.md#drafts-nothing-is-in-the-folder-until-you-save)).
  - The editor opens on the draft under a bar that says where it will go,
    and every editor writes into it as you type: the text, code and Markdown
    editors every few seconds, the document server and the diagram editor
    through their own saves. The browser asks before a tab with unsaved
    changes is closed, and so does the desktop app's document window.
  - **Save** puts it in its folder. If a file has taken the name meanwhile,
    filex asks - *save as `report (2).txt` instead?* - and nothing moves
    before the answer; a save never replaces a file.
  - Closing a draft that was never saved asks **Save to disk**, **Keep in
    Drafts** (the default) or **Discard**, which moves it to the Trash;
    restoring it from there puts it back in Drafts.
  - **Drafts**, in the navigation panel beside Recent, Starred and Trash,
    lists your drafts from every storage in the explorer's own table - name,
    where it will be saved, storage, modified - with Open, Save to disk and
    Delete, and the panel shows how many there are. No notifications.
  - A draft is its owner's alone: not in any listing, search, share, WebDAV,
    S3, SFTP, FTPS or NFS view, desktop sync, storage usage figure or version
    history, and not shown to anybody else - administrators included. On
    disk it is `.filex-drafts/<user id>/<key>/<name>`, one of the names filex
    keeps for itself.
  - At most **50 drafts per person** by default: *Admin → Protection →
    Drafts* (1-1000), or `FILEX_DRAFTS_LIMIT` on a fresh install. At the limit
    New document says so and offers Drafts; it never creates the file in the
    folder instead.
  - For integrators: `POST /api/files/drafts` takes `newfile`'s body, with
    `GET`, `…/count`, `…/{key}`, `POST …/{key}/save` (`409 TARGET_TAKEN` with
    a `suggested` name) and `DELETE …/{key}`
    ([docs/BACKEND.md](./BACKEND.md#drafts)). Drafts belong to a person, so
    an app token and an embed confined to one folder by its host keep
    creating the file directly; `capabilities.drafts` is present exactly when
    the caller's New document makes drafts. Migration `00064` adds the
    `drafts` table.
- **Encryption levels, and encrypted file and folder names.** An encrypted
  folder has a level: **1 · Contents only** (the default) or **2 · Contents
  and names**, chosen in the create dialog, shown in the strip, changed in the
  folder's new **Encryption settings…** (which also hold the password and the
  escrow slot). At level 2 the names are encrypted in the browser too -
  AES-SIV (RFC 5297) under a name key sealed by the folder key, with the id of
  the folder a name is in as associated data, so the same name in two folders
  is two different stored names; base64url on the server, a folder's id
  carried in its own name, long names shortened with a sidecar. The server
  stores and indexes only ciphertext names; every surface of the explorer -
  listing, split pane, breadcrumb, tabs, destination picker, Recent, Starred,
  tags, Home, trash, search, the details panel, uploads, downloads - shows the
  plaintext while the folder is unlocked and "🔒 Encrypted item" while it is
  not. The vault level is designed, not built, and not offered.
  [E2E-ENCRYPTION.md → Encryption levels and names](./E2E-ENCRYPTION.md#encryption-levels-and-names)
- **Encrypt a single file.** Right-click any file and choose *Encrypt with
  E2EE…*: it becomes one self-contained `<name>.fxe`, encrypted in the browser
  under a password of its own, with a recovery key shown once and - where the
  installation has key escrow - an escrow slot, said up front. Open, preview
  and download decrypt in the browser; *Change password…* rewrites the header
  only; *Remove encryption…* puts the plaintext back. The name can be hidden
  too (`encrypted-<hex>.fxe`). The dialog says what the server already saw of
  the original (the trash, its versions, a thumbnail, backups) before
  anything happens.
  [E2E-ENCRYPTION.md → Single encrypted files](./E2E-ENCRYPTION.md#single-encrypted-files-fxe)
- **Desktop app: the notification bell and your account menu in the top bar.**
  The app raised a native notification for everything new and had nowhere in
  its window to read one, mark it read or follow it - and no account menu at
  all. The top bar now ends as the web app's does: the **bell** (the unread
  count on it, the newest fifteen, *Mark all read*, *View all* with pages and
  *Unread only*) and the **avatar**, which holds your settings as a person:
  *User settings* opens the web app's settings dialog **inside the window**
  (profile, time zone, appearance, compact list, default folder view,
  notification switches, password, two-factor sign-in), *Admin panel ↗* for an
  administrator, and the file list's shortcut and tour rows that sat behind
  the "…". A click on a notification lands in the window: the folder with the
  file selected, the Trash with the item selected, or an app's home view. They
  are the web app's own components, not a second copy - see *Changed*.
  Signing out stays where the app's own settings are, *Settings → Accounts*,
  with the language, sync and the other accounts.
  ([docs/DESKTOP.md → Notifications and your account](./DESKTOP.md#notifications-and-your-account))
- **Encrypt a folder you already have, in place.** Right-click a folder →
  **Encrypt with E2EE…**: the same dialog as creating one (level 1 by
  default), and every file is encrypted where it is, resumably; at level 2 the
  names follow. The server keeps no plaintext version of those writes, and
  when it finishes drops the thumbnails and extracted text it held for the
  folder and - the owner's choice, on by default - its older versions and
  trash entries (`POST /api/files/e2e/cleanup`). The key file carries the new
  required feature `conv` while it runs, which filex 0.47 and older refuse.
  [E2E-ENCRYPTION.md → Encrypting a folder you already have](./E2E-ENCRYPTION.md#encrypting-a-folder-you-already-have)
- **Raise a folder you already have to level 2.** **Encryption settings… →
  Change level…**, after saying what changes; nothing is proposed on unlock.
  It renames only (no content is touched), writes the key file first, and an
  interrupted run continues where it stopped. **Fix names** in the same place
  renames stray plaintext names written later over WebDAV or the CLI, and
  repairs names moved in from another folder outside filex.
- **Change an encrypted folder's password.** **Encryption settings… → Change
  password…**, proved with the current password or the recovery key. A
  folder created since v0.31 rewrites only its key file - no file is touched,
  the recovery key, escrow and encrypted names keep working. A folder from
  before v0.31, whose key comes from its password, is re-keyed: a new folder
  key, every file's key re-wrapped (the contents are not re-encrypted),
  resumable, with a new recovery key; any folder can be re-keyed on purpose
  ("Also replace the folder key"), which is also how a leaked recovery key is
  revoked. Opening a folder with its recovery key now asks for a new password
  straight away. Every change is recorded in the audit log and the folder's
  owner is notified (`e2e.password_changed`, subscribable by webhook).
  [E2E-ENCRYPTION.md → Changing the password](./E2E-ENCRYPTION.md#changing-the-password)
- **No size limit in encrypted folders.** A file over 200 MB is encrypted in
  the browser as a STREAM (chunked AES-256-GCM, 1 MiB chunks, truncation and
  reordering detected) while it uploads, and decrypted as it downloads -
  nothing is held in memory. These files use header version `0x02`, which
  filex 0.47 and older refuse (the folder and its smaller files still open
  there); files up to 200 MB are written exactly as before.
  [E2E-ENCRYPTION.md → Streaming content](./E2E-ENCRYPTION.md#streaming-content-stream)
- **Download an encrypted folder decrypted.** Downloading an unlocked
  encrypted folder, a subfolder or a selection inside one now gives a zip with
  the real names and the decrypted content, made in the browser as it is
  saved; *Download encrypted copy* keeps the server's ciphertext zip. Chrome,
  Edge and the desktop app save it straight to disk at any size; Firefox and
  Safari up to 1 GB, and say so above that.
  [E2E-ENCRYPTION.md → Downloading a decrypted copy](./E2E-ENCRYPTION.md#downloading-a-decrypted-copy)
- **The escrow key opens a single encrypted file - and its owner is told.**
  On an installation with key escrow, the unlock dialog of a `.fxe` sealed to
  its key offers *Use the escrow key*: the same flow as an encrypted folder's,
  the browser proving it holds the private key and the server notifying the
  file's owner before anything is decrypted. `/api/files/e2e/escrow/challenge`
  and `/used` accept a `.fxe` path.
  [E2E-ENCRYPTION.md → Key escrow](./E2E-ENCRYPTION.md#key-escrow-optional-operator-recovery)
- **A `.fxe` password change is announced, and the server sees it too.** The
  owner is notified and the audit log records `e2e.password_change`, as for a
  folder; independently, every rewrite of a `.fxe` on any surface is audited
  by the server as `e2e.fxe_header_rewritten` (password, recovery key, escrow,
  content…), and a changed password deletes the versions that would still
  open the file's current contents with the old one.
  [E2E-ENCRYPTION.md → Who is told](./E2E-ENCRYPTION.md#who-is-told)
- **Delete the original for good while encrypting it (administrators).** The
  encrypt dialog offers an administrator to delete the original's versions and
  its trash entry as soon as the encrypted copy is saved; for someone else's
  file it names the owner and asks a second, explicit confirmation.
  [E2E-ENCRYPTION.md → What the server already saw](./E2E-ENCRYPTION.md#what-the-server-already-saw-of-the-original)
- **Over 1 GB in Firefox or Safari, the decrypted download says so and hands
  over the way out.** Instead of an error line, a dialog - before the password
  is asked, for a `.fxe` - with *Download encrypted file* (or the encrypted
  folder as a zip) and the `filex decrypt` command to copy. Chrome, Edge and
  the desktop app still stream it to disk at any size.
  [E2E-ENCRYPTION.md → Where a decrypted download goes](./E2E-ENCRYPTION.md#where-a-decrypted-download-goes)
- **`filex decrypt`** - decrypts a downloaded encrypted folder (or its zip, or
  one file) on your own machine, with the folder password or the recovery
  key, offline, into a folder with the original names. The password is read
  from the terminal or standard input, never from an argument; a wrong password
  or a damaged file stops it with no partial output. Reads every marker version.
  [CLI.md → filex decrypt](./CLI.md)
- **`filex decrypt file.fxe`** writes a single encrypted file back under its
  original name (or `-o`), and `filex decrypt` streams `0x02` folder files; a
  wrong password (exit 5) or a damaged, truncated or extended file (exit 6)
  leaves nothing behind.
  [CLI.md → A single encrypted file](./CLI.md#a-single-encrypted-file-fxe)
- **The server records every rewrite of an encrypted folder's key file** - an
  audit row `e2e.key_file_rewritten` with which slots changed (`password`,
  `recovery_key`, `escrow`, `level`, `rekey`) and the surface it came from,
  whether or not the client announced it. When the password or recovery slot
  changed, the key file's earlier versions are deleted: each of them still
  opened the folder with the old password.
  [E2E-ENCRYPTION.md → Who is told](./E2E-ENCRYPTION.md#who-is-told)
- **Notifications never print an encrypted name.** A row about an item inside
  an encrypted folder carries `meta.e2e_root`; the bell, the notifications
  page, the browser toast and the desktop app show "🔒 Encrypted item" - or
  the real name, where this tab's explorer has the folder unlocked - instead
  of the ciphertext the server stores.
  [NOTIFICATIONS.md](./NOTIFICATIONS.md)

**This release has more to it than fits on one page.** The rest of the
entry - and every earlier release - is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0481---2026-09-28).

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.48.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.48.1`

## v0.47.0

<span class="filex-release-date">26 September 2026</span>

filex runs under a sub-path behind a reverse proxy (a FILEX_PUBLIC_URL with a path works across the web app, WebDAV, S3, the desktop app, the CLI and Helm), apps keep themselves up to date within the filex version range they declare, and the desktop app is in the Microsoft Store, which signs, installs and updates it. Twelve pull requests by Berk Başarır move long folder jobs out of the request and have them say what they are doing; reviewing them closed security holes in the operations queue, so upgrade. Also fixed: uploads from a LAN address over plain HTTP, and a refresh that undid a switch to Starred, Recent, Shared or a tag.

## What changed

filex runs under a sub-path behind a reverse proxy, apps keep themselves up
to date, and long folder jobs leave the request and say what they are doing.
The last of these is twelve pull requests by Berk Başarır
([#58](https://github.com/BRF-Tech/filex/pull/58)-[#69](https://github.com/BRF-Tech/filex/pull/69)),
from an audit of a production install where a person reported "no positive
or negative message, nothing at all". A review of them found and closed a
few older holes in the operations queue on the way in.

> ⚠ **Security fixes in the operations queue** (Security): the
> generic queue endpoint took any job kind a client named and ignored a
> folder-confined token's root, a trash restore did not check the entry's
> tenant, and every member of a storage could read every other member's
> queued operations. Upgrade.
>
> ⚠ **For integrators** (Changed): `POST /api/files/ops` now
> takes only `copy`, `move` and `delete` (`400 BAD_KIND` for anything else),
> and a refused change is also said inside the explorer; hosts that show
> their own message can turn that off with `refusalToasts: false`.
>
> ⚠ **Behind a proxy that strips a path** (Added): if
> `FILEX_PUBLIC_URL` has a path (`https://example.com/filex`) and the proxy
> takes it off before filex sees the request, switch the proxy to pass the
> full path - that path is now filex's base, and filex takes it off itself.
>
> ⚠ **For app authors** (Added): a `filex-app.json` that declares
> the new `filex` version range installs on filex 0.47 and later only; 0.46
> and earlier refuse a manifest field they do not know.

### Added

- **The desktop app is in the Microsoft Store**, as
  [filex File Manager](https://apps.microsoft.com/detail/9PKXDJLVZWXW): the
  one Windows build Microsoft signs, so there is no SmartScreen prompt, and
  the Store installs and updates it. The "Install the desktop app" prompt
  and filex.sh put it first on Windows; the installer and the portable
  `.exe` stay for machines without the Store. Every release is submitted to
  the Store by the release itself from now on
  ([docs/DESKTOP.md](./DESKTOP.md)).
- **filex runs under a sub-path - `https://example.com/filex/` behind a
  reverse proxy - as well as at the root of its own host**
  ([#70](https://github.com/BRF-Tech/filex/issues/70)).
  - One setting: `FILEX_BASE_PATH=/filex` (`base_path`), or nothing at all
    with `FILEX_PUBLIC_URL=https://example.com/filex`, whose path is then the
    base. It is checked at startup (leading slash, no trailing slash, no
    `.`/`..`, unreserved characters); a bad one, or one that disagrees with
    the public URL's path, stops the server with a message that says what to
    write instead, and the startup log names the base in effect and where it
    came from.
  - The proxy passes the **full** path (Caddy `handle`, not `handle_path`;
    nginx `proxy_pass` without a URI part). filex takes the prefix off itself
    and answers 404 to everything outside it before any sign-in check runs,
    except `/healthz`, which also answers at the root for container health
    checks.
  - Every address filex hands a browser carries the base - redirects, the
    OIDC bounce and callback, share and file-request links and the no-JS
    pages behind them, links in e-mails, the realtime socket, a tenant's
    origin in multi-tenant mode - and every cookie it sets is scoped to it
    (`Path=/filex`).
  - The web app is built once and served for any base: the server rewrites
    `index.html` and the PWA manifest as it serves them (and serves both byte
    for byte at the root, so an installed app keeps its identity), and the
    service worker is registered under the base.
  - WebDAV lives at `/filex/dav/`, with every `PROPFIND` href and
    `MOVE`/`COPY` destination under it; path-style S3 at `/filex/s3`, the
    signature checked against the path the client signed. A dedicated S3
    host (`FILEX_S3_DOMAIN`) is served at its own root as before.
  - The desktop app keeps a server address's path (the sign-in field used to
    drop it); the CLI already did. The Helm chart has a `basePath` value.
  - Caddy and nginx examples, measured against a real filex:
    [DEPLOYMENT.md → Serving filex under a sub-path](./DEPLOYMENT.md#serving-filex-under-a-sub-path);
    the setting: [CONFIGURATION.md → Base path](./CONFIGURATION.md#base-path).
- **Apps follow their source and update themselves, and say which filex
  versions they work with.** Once a day, and on **Check for updates** in
  Admin → Apps, filex asks where each app came from for a newer version this
  filex can run: a GitHub app installed at a tag follows the repository's
  releases, a language pack follows its branch, an app installed from an
  address follows that manifest address.
  - A newer version that asks for no new permission installs by itself,
    through the same verified fetch and atomic upgrade as a manual install.
    One that asks for a new permission, or a pack that now brings a module,
    waits as *Needs approval*; **Review update** shows the version jump, the
    new permissions marked **New**, and the ones it dropped.
  - Automatic updates are switched per app from its Actions menu: on by
    default, off for a URL install pinned by SHA-256, never applied on a
    signed-only instance. Every one is logged, audited (`app_plugin.update`)
    and announced to administrators (`app_updated`, and once per version
    `app_update_available`, `app_update_needs_approval`,
    `app_update_failed`). `FILEX_APP_PLUGIN_UPDATE_CHECK=0` turns the daily
    check off; demo instances never check.
  - `filex-app.json` gains `filex`, a version range (`">=0.47.0 <0.60.0"`).
    Installs and upgrades outside it are refused (`incompatible`) and the
    review says so first; updates take the newest version that fits; an
    installed app filex has outgrown keeps running with a warning. Release
    candidates count as their release, development builds check no range,
    and `min_filex` is honoured now. Migration 00063.
  - [docs/APP-PLUGINS.md](./APP-PLUGINS.md).
- **The Trash says who deleted each item.** A delete now records the person
  on the item, and on every file inside a deleted folder, whether it ran in
  the request or later through the operations queue ([#64](https://github.com/BRF-Tech/filex/pull/64)).
  - The explorer's Trash shows it in a "Deleted by" column ("You" for your
    own deletes); the admin's Trash page shows the name.
  - An item nobody in filex deleted shows a dash, with a hover text saying
    why: the scanner found it gone from the storage, or it was deleted
    before this release, when nothing was recorded.
  - A restore clears the record. The trash listing carries
    `deleted_by_id`, `deleted_by_name` and `deleted_by_self`. Migration 00061
    adds `nodes.deleted_by`; nothing is backfilled.
- **Renaming a folder, restoring from the trash and deleting permanently run
  as jobs of the operations queue.** On an object store a folder is one
  request per object, so these waited with nothing on screen until a proxy
  gave up, then said the change had failed while the server carried on
  ([#61](https://github.com/BRF-Tech/filex/pull/61), [#63](https://github.com/BRF-Tech/filex/pull/63)).
  - `POST /api/files/manager?action=rename`, `POST /api/files/manager/restore`
    (which then takes a `node_ids` batch, at most 1,000) and
    `DELETE /api/admin/trash/{id}` take `queued=1`. The same checks answer at
    once; what they allow becomes a job (`202 {op}` / `{ops}`, kinds `rename`,
    `restore`, `purge`), listed under `capabilities.queued`.
  - The explorer and the admin's Trash page use them on a server that lists
    them. The dialog closes at once, the row says "Restoring…" or "Deleting
    permanently…", and the listing follows when the job ends - with the undo
    a rename always offered.
  - These jobs run in a lane of their own, so a long folder rename never
    holds up the copies, moves, deletes and upload commits queued behind it.
    Once running they are not cancelled half-way (`cancellable: false`;
    cancelling answers `409 NOT_CANCELLABLE`).
  - A server restart does not lose them: a restore stopped between entries
    carries on at the next start, and a rename interrupted half-way finishes
    the move instead of failing on the half that had arrived.
  - A purge takes what is in the trash when it runs. An entry restored while
    the purge waited in the queue is left alone.
- **"Delete permanently" in the explorer's Trash deletes.** Everyone was
  offered it; its dialog said the items "will be moved to trash", about items
  already there, and then nothing was deleted. Someone the server lets purge
  now purges, after a dialog that says it is for good; anyone else no longer
  sees it, and the Delete key says how the trash empties itself
  ([#69](https://github.com/BRF-Tech/filex/pull/69)). A press of many items is
  one job batch: one "Deleting N items permanently…", one summary with the
  reason for anything that could not be deleted, one reload.
- **A copy, move or delete of one folder says how far it has got.** Moving a
  folder of 4,000 files on an object store read "0/1" with a 0% badge for the
  minutes it took. The storage driver now counts the objects it works
  through, a running operation carries `objects_total` / `objects_done`, and
  the operations centre says "25 of 100 items"
  ([#67](https://github.com/BRF-Tech/filex/pull/67)). Queued renames,
  restores and purges count their objects too.
- **The explorer says what became of what you asked it to do**
  ([#59](https://github.com/BRF-Tech/filex/pull/59), [#65](https://github.com/BRF-Tech/filex/pull/65), [#69](https://github.com/BRF-Tech/filex/pull/69)):
  - A paste, drag-move, duplicate or copy the server refused is said on
    screen, in the reader's words; it went out as the explorer's `error`
    event only, which looked exactly like a change that worked. A refusal
    that arrives after its dialog has closed is said as a notice.
  - A dialog whose request is on its way - Rename, New folder, Delete,
    Delete permanently, the archive dialogs, the destination picker, an
    app's screen, the converter - keeps its buttons shut with a label that
    says so, and Escape, a click outside and × wait for the answer. A long
    one that can be stopped (the converter) asks inside the dialog before it
    closes. Paste, Restore and a multi-item Download take one press at a
    time too.
  - Undo says "Undoing…" and then what it did: queued, undone in part
    ("2 of 5"), or undone.
  - A listing being read again draws a thin moving bar over the rows it
    will replace, and fades them, after 300 ms.
  - "Catalog everything" follows the scan it started and says how it ended.
  - "Move to…" says a move is queued only when it was, keeps the Undo of a
    move made at once, and says so when the items are already there.
  - Restore from the trash says what did not come back, and why.
  - An upload whose bytes are all in filex says "Saving to the storage…"
    while the server writes it, instead of 100%.
  - The converter names its step (reading, converting, saving) and waits up
    to 30 minutes; ⌘K's "Everywhere" says when it could not search; the
    archive preview says why a listing takes long; restoring a version and
    taking a snapshot say so while they run; an app's screen opens under the
    action's name while its answer is on the way.
- **The admin panel says what its long jobs are doing**
  ([#66](https://github.com/BRF-Tech/filex/pull/66)): "Sync now" says
  "started" or "already running" and when the scan is done, failed or
  stopped (`GET /api/admin/storages` carries `running`); "Rebuild index"
  follows the rebuild to its end and says it failed, and why, when it did
  (search stats carry `last_rebuild_error` / `last_rebuild_finished_at`);
  deleting a large storage says it is still deleting until it is gone;
  replica "Fix all" queues each retry once (`{queued, already_queued}`); a
  storage plugin's install waits up to 180 s.
- **Desktop: the app says what sync, downloads and drag-out are doing**
  ([#68](https://github.com/BRF-Tech/filex/pull/68)):
  - A folder no pass has finished yet reads "waiting for its first check",
    and every phase keeps its figures ("listing the server - 48,211 items so
    far", "97 changes to make").
  - An engine that stopped on its own is started again after 5 s, 15 s, a
    minute, then every five minutes, and the line shows the engine's own
    reason. A restart that brought no engine up is tried again.
  - A download to disk moves the dock / taskbar bar, and a notification says
    "Downloaded" (click to show it in its folder) or "Download failed".
  - A folder dragged out counts its files, at most four reports a second,
    and **Stop** ends the file in flight too; two drops at once each have
    their own Stop.
  - The tray tooltip carries the pause, sync's state and the unread count.
- **Embedding: `ExplorerConfig.dragOut.stop` and a `files` count on the
  drag-out progress** let a host stop a drag-out and say how far it got
  ([docs/INTEGRATION.md](./INTEGRATION.md)).
- **An embedded explorer whose `apiBase` has a path keeps it for every
  address it builds.** A multi-selection ZIP and the drag-out link
  (`/z/<ticket>`) kept only the origin, so behind a host proxy such as
  `/files-proxy` they went to the host's root and failed; the "open in a new
  tab" editor route and the connection guides (the WebDAV address, the
  Cyberduck path) dropped it too. The server's relative addresses
  (`thumb_url`, a download's `/z/<ticket>`) are joined onto `apiBase`
  ([INTEGRATION.md](./INTEGRATION.md#serving-filex-under-a-sub-path)).

### Changed

- **`POST /api/files/ops` takes only `copy`, `move` and `delete`.** Every
  other kind answers `400 BAD_KIND`; renames, restores and purges are asked
  for through their own endpoints with `queued=1`, which run their checks
  first.
- **A refused change is said inside the explorer as well as emitted as
  `error`.** Hosts that already show their own message set
  `refusalToasts: false` ([docs/API.md](./API.md)).
- **The trash listing pages over everything the caller may see.** It is
  ordered by deletion time (`deleted_at DESC, id DESC`), and a `limit` above
  500 reads as 50.
- **Mount as a drive uses `~/filex-drives/filex-<storage>` on macOS and
  Linux.** macOS mounted onto `/Volumes/filex-<storage>`, a folder a user
  cannot make, and ignored the failure; it now says so, in the app's
  language, when the folder cannot be made. Not yet verified end to end on a
  Mac.

### Fixed

- **Switching to Starred, Recent, Shared or a tag could be undone by a
  refresh.** The view's address moved only when its rows arrived, so a
  refresh landing in between (a live change, the Refresh button, the end of
  an action) loaded the view being left again, and when that answer came
  last it was put back on screen under a panel that no longer marked the
  view you had clicked. The address now moves with the click.
- **A folder renamed, moved, trashed, restored or purged on an object store
  could be left half done when the client stopped waiting.** These ran
  under the request's context, which a closed tab or a proxy that stops
  waiting (nginx after 60 s, Cloudflare after 100 s) cancels. The folder was
  left in two places and a retry was refused by the half that had arrived.
  Once its checks have passed, the change now runs to the end whether or not
  anybody is still waiting - in the explorer, the trash, the agent surface
  (`/api/ai/move`, `/api/ai/delete`) and WebDAV (`DELETE`, `MOVE`)
  ([#60](https://github.com/BRF-Tech/filex/pull/60)).
- **A purge queued before its entry was restored no longer deletes the
  restored folder.** The job hard-deleted whatever it found under the id,
  live or not, with its shares, tags, comments and versions. A purge of an
  entry already gone counts as done instead of failing on "no rows".
- **A storage made from the admin panel is switched on.** The form never
  sends `enabled`, and the server saved it disabled: no scan, and "Sync now"
  answered 404.
- **Saving a storage no longer restarts its scan for nothing** - including
  on PostgreSQL and MySQL, which spell a stored configuration differently -
  and a new setting stops every scan of the old one.
- **Deleting a large storage finishes, and a failed delete leaves the
  storage as it was.**
- **The trash listing reached only its first page** when some entries were
  hidden from the caller (it reported the page's length as the total).
- **"Deleted by" is not given to a person for files the scanner had found
  gone** under a folder that was deleted in place.
- **The archive dialogs and "Send by email" send once per press**, Enter
  included; "Extract here" says it is reading the archive and a second
  choice starts nothing.
- **A file request says the server is saving, and a drop that arrived is not
  called failed.** The server finishes writing a drop that has fully arrived
  when a proxy gives up, and a gateway timeout after every byte was sent
  reads "Sent, but the server did not confirm it in time". A `502` still
  reads as a failure: a down server answers it after the last byte too.
- **The archive preview** no longer lets a listing land on the next file's
  screen, and closing it stops the server's download.
- **An error message that mentions "already exists"** is no longer read as
  "something with that name is already there" unless it is the queue's own.
- **The operations centre** no longer announces a colleague's job to an
  administrator's explorer, names a restore or purge by its item count, and
  shows a count only where it can move.
- **Desktop:**
  - moving the filex folder runs once, and sync stays off until it ends;
  - "Open with filex" opens a document once, and finds the synced copy under
    a path with a Turkish "İ" in it (it could open the wrong file);
  - "Stop syncing" and "Keep online only" stop the pass under way;
  - a pair list that could not be read no longer stops every sync watcher;
  - Settings stops losing clicks while sync prints;
  - the update card says "Up to date" only after a check, and the tray's
    Settings opens Settings on a hidden start.
- **Uploading from a page opened over plain http at an address other than
  localhost did nothing.** The explorer named every upload with
  `crypto.randomUUID()`, which browsers only define on https or localhost;
  picking a file stopped at "crypto.randomUUID is not a function" before a
  byte was sent.
- **Apps:** an upgrade that failed and was rolled back no longer breaks the
  app at the next restart; an app that is switched off stays off after an
  upgrade; an upgrade interrupted by a crash is undone at the next start;
  and a language pack installs from its manifest address alone (the install
  looked for a module address, which a pack does not have).
- **Two source files were stored as binary by git** (a raw NUL byte), so
  their changes could not be reviewed; every source file is now checked.

**This release has more to it than fits on one page.** The rest of the
entry - and every earlier release - is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0470---2026-09-26).

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.47.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.47.0`

## v0.46.1

<span class="filex-release-date">26 September 2026</span>

## What changed

### Fixed

- **The version line no longer runs off the menu.** The server reports its
  version as the release followed by the full commit hash and the build time,
  and the line printed all of it: the explorer's avatar menu grew a sideways
  scroll bar, the sign-in page carried the 40-character hash twice, and the
  About page ran it off its card. The account menus, user settings and the
  sign-in page now name the release only (`filex v0.46.1`); the About page
  shows the release with the commit shortened to seven characters and the
  build date under it, and its copy button still copies the whole string.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0461---2026-09-26)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.46.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.46.1`

## v0.46.0

<span class="filex-release-date">26 September 2026</span>

## What changed

### Added

- **Put the storages in the navigation panel in your own order (#57).** Drag a
  storage row, or use its menu (right click, a long press, or Shift+F10):
  Move up, Move down, Sort by name, Use default order. The order is kept on
  your account, so it follows you to every browser, and Home's storage cards
  follow it too.
- **Administrators set the order everybody starts from (#57).** The admin
  Storages page is a table now: drag a row by its handle, use Move up / Move
  down in its Actions menu or the arrow keys on the handle, and Reset to
  default order. People who have not arranged their own see this order;
  `PUT /api/admin/storages/order` sets it (migration 00060 adds
  `storages.sort_order`).

### Changed

- **New document: name the file anything - `LICENSE`, `Makefile`,
  `test.conf`, `example.custom` (#56).** The dialog drew the type's extension
  as a read-only suffix, so a Plain text document was always `<name>.txt`. The
  name field now holds the whole file name: the type prefills it
  (`Untitled.txt`), focusing it selects the stem like a rename, and a type
  switch keeps what you typed (only swapping the previous type's default
  extension). The type still decides the contents and the editor - `LICENSE`
  made as Plain text opens in the text editor, `README` made as Markdown in
  the markdown editor. Office documents and diagrams keep their extension,
  because their editors find them by it: the dialog says "will be created as
  `report.docx`" instead of locking the field, and an empty `x.docx` made as
  Plain text is refused. Behind it: `POST /api/files/manager?action=newfile`
  takes `exact_name: true` (without it the extension is appended exactly as
  before, so older clients are unchanged), `newdoc_types` rows carry
  `ext_required`, and a refused borrowed extension answers `400 EXT_NEEDS_TYPE`. Name checks - a leaf, no `..`, reserved names - are as
  strict as they were.
- **A text file whose name has no known extension opens as text and can be
  saved (#56).** `LICENSE`, `NOTICE` or `notes.custom` fell through to the
  viewer's Download fallback, and save-text - which allows by extension -
  refused them with `415`. The viewer now opens such a file as plain text when
  the server's mime for it says text, and save-text saves an existing file the
  catalogue calls text (a file it calls anything else, or a path with no
  catalogue row, is refused as before). The listing draws such a file with the
  text icon and names it "Plain text" instead of "?" and "-".

### Fixed

- **A new document opens once in the desktop app.** It came up twice: in its
  own window and in the viewer over the explorer, because creating a document
  opened the in-page viewer before telling the app. It now takes the same path
  as every other open (the app's window only). Found while building #56.
- **An app screen speaks the language ON SCREEN, not the account's.** An
  embedded explorer (`<filex-explorer>` with `config.locale: 'tr'`) draws the
  language its host page chose, and nobody asked the account - whose language
  defaults to English. The server told every app the ACCOUNT's language
  (Accept-Language ranks below it on purpose), so an app's plain strings came
  out English in a Turkish popup: the signing app's wizard asked "Identity"
  with "One signer per line…" under it. The explorer now names its language on
  every app call (`?lang=` on `…/run`, the view `GET`, every `…/event`), and
  the server puts that explicit choice first - then the account's, then
  Accept-Language, as before. The standalone app is unchanged: its language
  and the account's are the same one.
- **The operations tray says an app's job in the reader's language.** The job
  row froze the action's label and the app's result in ONE language when the
  job was created, so a Turkish embed read "Convert…" and an English result.
  The row now keeps every language the app wrote and the tray's poll
  (`/api/files/ops?lang=`) reads them in its reader's - the label, the result,
  and the reason an app gave for failing. Rows written before read as they
  were; no migration.
- **Every core dialog's close button is named in the explorer's language.**
  The × of every dialog (modals/Modal.vue) took its accessible name from the
  dialog's own `locale` prop and fell back to English, and not one of the
  twenty core dialogs passed it: a screen reader said "Close" over a Turkish
  app popup. A dialog now inherits the language of the explorer it sits in
  (an explicit `locale` still wins).
- **An archive being made no longer looks like a click that did nothing.** An
  archive job read "0%" beside an empty ring for most of its run: reading the
  members off their storage - most of the time on a remote store - was worth
  0-10% of the bar, counted per member, and the built-in ZIP and the write of
  the result reported nothing. Measured in production, 175 files off S3: two
  minutes between 0% and 9%, and the person who started it saw no sign of it.
  Each phase now moves in bytes while it runs - reading 0-45%, compressing
  45-90%, writing 90-99% - and creating or extracting an archive opens the
  operations panel on the job, so its name, progress bar and Cancel are on
  screen when the dialog closes rather than a small badge in the corner.

- **Typing in the code editor no longer triggers the explorer's shortcuts.**
  In Chrome and Edge the editor types through the browser's EditContext, so
  its focused element is not a text field, and the explorer took the
  keystrokes: `I` opened the info panel, `S` starred the file, Space opened
  quick look and Backspace went up a folder - "MIT License" typed into a new
  file arrived as "MLcene". The shortcut guard and quick look's arrow keys now
  treat the editor (and any `role="textbox"`) as typing. Found while building
  #56.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0460---2026-09-26)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.46.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.46.0`

## v0.45.1

<span class="filex-release-date">25 September 2026</span>

## What changed

0.45.0 as it was meant to ship. **v0.45.0 was tagged but never published**:
one of its unit tests read a file only the maintainers' private repository has,
the release workflow's test gate failed on it in the public tree, and nothing -
no images, binaries, npm packages or desktop apps - went out. Everything
0.45.0 carried is below, in this release, together with the fix and a new
release gate that runs the web tests in the public tree before anything is
tagged.

### Security

- **A token confined to one folder could reach files outside it through the
  app doors and the selection archive.** A `root:`-scoped API token (the kind
  a host application hands an embed, confined to the tenant's folder) could
  run an app on any file of the storage - its result written next to that
  file - and put files from outside its folder into a selection archive
  (`POST /api/files/archive/download`). The path confinement rewrote the body
  keys it knew and passed a `paths` array through untouched, and those doors
  checked the person's permissions but not the token's root. Both layers now
  refuse: the confinement covers `paths`, and every app door (run, view
  events, the chosen output folder) and the archive check each path against
  the token's root. Found while building #71.

### Added

- **Drag a file out of the web admin onto the desktop (#71).** The admin app
  signs its requests with a bearer token, which the browser's drag-out
  download cannot carry, so it offered no drag-out. The explorer now asks the
  server for a one-file link while the pointer rests on a row or a ⌘K result
  (`POST /api/files/archive/download` with `"mode":"file"` → `/z/<ticket>`, on
  the existing archive tickets): it lasts a minute at most and works once, and
  at the drop it is checked again as its owner - account, token, tenant host,
  tenant storage and at least viewer on the file. Every drag-out download is in
  the audit log (`file.download_link`, `file.download_link_refused`); the link
  itself never is. Folders are refused (`409 IS_FOLDER`). A cookie session
  keeps its plain download URL.

- **Desktop: Mount as a drive.** One button in Settings attaches a filex
  server as a drive of the operating system over WebDAV, and one detaches it -
  the one-click counterpart of the connection guide, whose commands now come
  from the same code. The account's own API token is the WebDAV credential and
  reaches the OS on stdin, never on a command line. On Windows it uses
  WNetAddConnection2, starts the WebClient service if it is stopped, and says
  up front what WebClient needs (HTTPS for a password). The macOS and Linux
  paths are there but not yet verified on those systems (#36).
- **⌘K search results you can act on.** A result in the palette's
  *Everywhere* group has a Download button (a folder arrives as one zip) and
  drags out like a row of the file list - through the desktop app's own drag,
  or as the browser's single-file download where the session is a cookie. A
  drag let go on the palette itself no longer starts an upload (#47).
- **Desktop: ⌘K searches every account on the rail.** Results are grouped
  under one badge per account, the one you are looking at first. Each account
  is searched, downloaded and dragged with its own sign-in, and opening another
  account's result switches the rail to it. Embedders get the same through the
  new `accountSearch` config hook
  ([docs/INTEGRATION.md](./INTEGRATION.md)) (#47).
- **`pnpm release X.Y.Z` cuts a release in order, and a red gate stops it.**
  The release process in [docs/CONTRIBUTING.md](./CONTRIBUTING.md) is one
  command now: preflight, the README/screenshot/docs audit, the doc gates, the
  version stamp, the whole pre-tag chain (both images, three database engines,
  TZ=UTC, Cypress, Playwright), the public export and its guards, then the
  signed tags, the push, the workflow's output and the deploy. No option skips
  a gate. The tool never signs, pushes or deploys: at those steps it prints the
  commands, and on `--resume` it checks what was done - each tag's signature
  and target, what both remotes hold, and what the servers, the update feed,
  the desktop feeds (bytes against their sha512) and docs.filex.sh serve.
  `--dry-run` runs every gate and writes nothing (#48).

### Fixed

- **The Storage line says how full the drives are, not how much you uploaded
  (#54, Berk Başarır).** The line at the foot of the explorer's navigation
  (web and desktop) and the storage chip in the admin top bar printed the
  person's own upload counter under a label about the drive: on an S3 drive
  holding 245.3 GB it read 523.5 MB, because files a sync discovered belong to
  nobody. With a quota the line still shows the person's usage against it;
  without one it shows the size of the drives the panel lists, the figure
  Home's cards print, and "at least …" where a drive is not fully counted.
  Refresh reads it again, and a failed poll keeps the last figure.
- **The release checks the public tree the way the release workflow does.**
  `pnpm release` built and tested the web code only in the private tree; it
  now runs the Frontend job (packages, admin build, web and package unit
  tests) in the exported tree as well, and `--resume` gets past its own
  export after a red export gate instead of stopping until the checkout is
  cleared by hand.
- **Desktop app: the sidebar entries are left-aligned again (#53).** Home,
  Trash, each storage and *How to connect* sat centred in their rows on macOS
  and Windows. The explorer renders into the page that hosts it, and the
  desktop app's own rule for its dialog buttons (`justify-content: center`)
  reached the sidebar's buttons, which left that property unsaid. They say it
  now, so a host page with a global button rule no longer moves them.
- **WebDAV: a file saved through a mapped Windows drive keeps its content.**
  Windows' WebDAV client sends a PROPPATCH with the Win32 times after every
  save, and answering it committed an empty upload over the file - so every
  file saved through a mapped drive landed on the server empty, with no error
  anywhere. A write-open that is not an upload now only describes the file.
- **A big local storage is catalogued about three times faster (#70).** A
  folder's catalogue rows were written one statement at a time and every new
  file indexed for search on its own, a disk-bound write each. They now go in
  one database transaction per folder (SQLite, PostgreSQL and MySQL), the
  folder's deletions included, and into the search index in one batch after it
  commits. On a 100,020-file tree the lazy catalogue's background fill went
  from 34 to 95 files/s and a full scan from 46 to 162 files/s; the lazy
  catalogue's deletion-safety rules are unchanged. Content extraction, queued
  per file as before, now trails behind the catalogue
  ([docs/LAZY-CATALOGUE.md](./LAZY-CATALOGUE.md)).
- **WebDAV storages no longer cut transfers at 60 seconds (#73).** The driver
  bounded every request, body included, to one minute, so any upload or
  download longer than that failed however well it was moving. Now only
  silence is bounded: connecting, the answer and each next piece of a transfer
  each wait `attempt_timeout_s` (30 s); copies, moves and deletes wait up to 10
  minutes. A dead server is reported as unavailable within seconds, failures
  that can pass are retried within `total_timeout_s`, and an upload is never
  sent twice. The driver speaks HTTP/1.1.
- **An FTP server that stops answering no longer freezes its storage (#73).**
  Only the connect was bounded: the liveness check held the storage's single
  connection forever, so every other operation on it waited too, and so did a
  server that never greeted and a transfer that stopped. Every wait now ends
  after `attempt_timeout_s` (15 s), a fresh connection is tried within the
  budget, a request whose client leaves stops waiting, and moving transfers
  are never cut. FTPS data connections are encrypted by the driver itself.
  WebDAV and FTP storages have the same three time settings as S3 on the
  storage form.
- **S3: a store that is down is reported within seconds, not a minute and a
  half (#44).** A drop into an S3 folder while the object storage was down
  answered `503 storage_unavailable` after 85 s; a refused port took 26 s, a
  host off the network ~145 s, and a store that accepted the connection but
  never answered did not return at all. Every wait that is not a moving
  transfer is now bounded per attempt - connecting (DNS included), the TLS
  handshake, the answer after the request, an answer that stops arriving, an
  upload the store stops taking - and retries are held to a time budget: with
  the defaults a dead store is reported within 15 s as
  `s3 endpoint … is unavailable: <reason> (N attempts in Xs)`. A transfer that
  keeps moving is never cut; copies, renames and multipart completion wait up
  to 10 minutes. A certificate that does not verify is no longer retried, and
  uploads over 2 MB (`Expect: 100-continue`) have an answer limit too. New
  storage settings `attempt_timeout_s` (10), `max_attempts` (6) and
  `total_timeout_s` (15) appear in the storage form; see
  [docs/STORAGE.md](./STORAGE.md).
- **Ops → Updates no longer promises what the install cannot do (#72).** On
  an install a package manager owns (Homebrew, winget, Snap, a distribution
  package) or a container, a saved policy of `patch` or `minor` made the badge
  read "Policy: install patches" although filex never replaces such a binary.
  The server works out the effective behaviour in one place, shared with the
  upgrade decision, and the status carries `behavior` and `policy_limit`: the
  badge reads **Announces only**, and the line under it says the saved policy
  has no effect on this install, with the package manager and its command.
  Checking switched off reads **Checking off**; `minor` on 0.x reads
  **Installs patches**. The saved policy is kept as set.
- **The release badges on Ops → Updates have their colours again.** Security,
  schema change and the size of the step all rendered grey: they passed a
  property the badge component does not have.
- **App screens wear the theme everywhere (#57).** On a branded, dark
  instance the wizard's current-step number and the people picker's avatar
  were white on the theme's primary (2.3:1 on a pale one); they now use the
  palette's `--fe-text-on-primary`, like filex's own buttons, and a signing
  box's number takes its ink from the signer's colour. An app's page and a
  public page (share, file request, signing link) now turn with light and dark
  while they are open, instead of keeping the mode they were opened in.
  [docs/APP-PLUGINS-API.md](./APP-PLUGINS-API.md) lists the theme tokens
  each surface node reads and what stays fixed on purpose (the PDF page,
  signer colours, the signature pad's paper), and a test fails when a surface
  node carries a fixed colour, corner or font.
- **Apps: a modal screen opened on several files keeps all of them.** An
  action that applies to a selection and opens a view saw every file on its
  first screen, but from the first form edit (or the submit) on it was
  answered about the first file only, and the job it queued ran on that one
  file (#64). The explorer now sends the whole selection with every view
  event; the server re-checks each path for the person asking, re-checks
  `applies` on every file at submit, and takes at most 500 paths, as `run`
  does.
- **Desktop: a copy prepared for one account's drag-out is not handed to
  another account's.** Two accounts with a storage of the same name (the same
  server, or the same drive name) shared the prepared copy of a path, so a drag
  from the second could carry the first account's file. The copies are keyed
  by account as well now (#47).
- **Linux: the desktop app opens on a minimal install.** Electron loads the
  ALSA sound library at start, and the `.deb` and `.rpm` did not depend on it:
  on a system without it the app did not open. They now require
  `libasound2 | libasound2t64` (Debian, Ubuntu) and `(alsa-lib or libasound2)`
  (Fedora, openSUSE).
- **Linux: the desktop app warns when the Snap Store copy is installed too.**
  The snap keeps its own accounts and its own sync history, so a folder paired
  in both copies was synced by two apps that did not know about each other -
  duplicate uploads, conflict copies, a delete reaching the wrong side. A
  `.deb`, `.rpm`, AppImage or AUR copy now says so in Settings, with the
  commands to keep one.
- **`filex self-update` leaves a distribution package alone.** A filex in
  `/usr/bin` (or `/usr/sbin`, `/bin`, `/sbin`) was put there by a package
  manager, and is now treated like a Homebrew, winget or Snap install: no
  self-replacement, upgrade with the package manager. A hand install in
  `/usr/local/bin`, where [docs/CLI.md](./CLI.md) puts it, is unchanged.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0451---2026-09-25)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.45.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.45.1`

## v0.44.2

<span class="filex-release-date">25 September 2026</span>

The release 0.44.0 was meant to be, and the one to install the desktop app from, including through Homebrew, winget and Snap. 0.44.0 and 0.44.1 both published their container images, npm packages and CLI binaries, then stopped at the winget step before the desktop packages and the stores. Everything 0.44.0 describes is in 0.44.2.

## What changed

0.44.0 as it was meant to ship, and **the one to install the desktop app
from**. v0.44.0 and v0.44.1 both published their npm packages, container
images and CLI binaries and then stopped at the winget step, so neither
shipped the desktop packages or reached the stores. Everything 0.44.0
describes below is in 0.44.2.

### Fixed

- **The release reaches the desktop packages and the stores.** GoReleaser
  accepts a publisher's repository token only as a single `.Env` variable
  reference - nothing around it, not even an `index` lookup - and checks it
  only when it publishes. The winget token is written that way now, and the
  template test checks every token against GoReleaser's own rule and that the
  release hands each variable to the GoReleaser step.
- **A desktop page that cannot load now stops the release.** Nothing ran
  the desktop unit tests before, which is how 0.43.x shipped a main window
  whose script never parsed; the release's gate now runs them. Found and
  proposed by Berk Başarır ([#52](https://github.com/BRF-Tech/filex/pull/52)).

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0442---2026-09-25)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.44.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.44.2`

## v0.44.1

<span class="filex-release-date">25 September 2026</span>

Tagged, but it stopped at the same winget step as 0.44.0; its container images, npm packages and CLI binaries were published. Use 0.44.2.

> ⚠ **Use [v0.44.2](https://github.com/BRF-Tech/filex/releases/tag/v0.44.2) instead.** v0.44.1 published its container images, CLI binaries and npm packages, then stopped at a packaging step of the release (a GoReleaser template), so it has **no desktop app and no store packages**. v0.44.2 carries everything in this release plus those. Nothing published here is broken; it is just incomplete.

## What changed

0.44.0 as it was meant to ship, and **the one to install the desktop app
from**. v0.44.0's npm packages, container images and CLI binaries went out,
but its release stopped at the winget step, so its desktop packages and every
store and package-manager step behind it never ran. Everything 0.44.0
describes below is in 0.44.1.

### Fixed

- **The release reaches the desktop packages and the stores again.** The
  winget and Homebrew-tap sections of `.goreleaser.yml` guarded their tokens
  with a template function the release's GoReleaser does not define
  (`envOrDefault`); it was evaluated only when publishing, after the Release,
  npm and the images were out. They use `&#123;&#123; index .Env "NAME" }}` now, and a
  test fails on any template function GoReleaser does not have.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0441---2026-09-25)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.44.1) · `ghcr.io/brf-tech/filex:slim-v0.44.1`

## v0.44.0

<span class="filex-release-date">25 September 2026</span>

Big local storages open at once: the new lazy catalogue lists a folder straight from disk and catalogues it behind the listing, with a throttled pass that fills in the rest (or only the folders people visit). Archives gain 7z, the TAR family and password-protected ZIP and 7z, contributed by Alex (@ahjephson) and hardened on the way in. The desktop app and the CLI install with Homebrew, winget and Snap, and the desktop app's blank main window from 0.43.0-0.43.2 is fixed.

> ⚠ **Use [v0.44.2](https://github.com/BRF-Tech/filex/releases/tag/v0.44.2) instead.** v0.44.0 published its container images, CLI binaries and npm packages, then stopped at a packaging step of the release (a GoReleaser template), so it has **no desktop app and no store packages**. v0.44.2 carries everything in this release plus those. Nothing published here is broken; it is just incomplete.

## What changed

Big storages open at once, archives come in every common format, and the
desktop app arrives through the stores. A **lazy catalogue** for local
storages (`sync_mode: lazy`, the idea from Alex / @ahjephson in #45) lists a
folder straight from disk the moment it is opened and catalogues it behind the
listing, with a throttled pass that fills in the rest - or, for a huge
archive, only the folders people visit. **Archives** gain 7z, TAR and its
compressed forms and password-protected ZIP and 7z (RAR too, where the
server's 7-Zip has it), contributed by Alex in #48 and hardened on the way in. The **desktop app and the CLI** ship
through Homebrew, winget and Snap, with a Microsoft Store build and an `.rpm`
beside them.

⚠ **Upgrade the desktop app if it is on 0.43.0 - 0.43.2**: its main window
stayed blank (see *Fixed*). ⚠ On Linux the desktop app's command is now
`filex-app`. ⚠ The desktop app needs macOS 13 or later (Electron 44). ⚠ The
full container image moves to Alpine 3.24 and grows by about 390 MB
unpacked (see *Changed*).

### Added

- **The desktop app and the CLI in package managers.** The desktop app is
  `filex-app` everywhere and the CLI is plain `filex`: `sudo snap install filex-app` (also in Ubuntu's App Center), `brew install brf-tech/filex/filex-app` and `brew install brf-tech/filex/filex` from the new
  tap [BRF-Tech/homebrew-filex](https://github.com/BRF-Tech/homebrew-filex),
  `winget install BRFTech.filex-app` and `winget install BRFTech.filex`. The
  release job publishes all of them from the release's own files, pinned by
  SHA-256 to the versioned download (goreleaser for the CLI,
  `desktop/scripts/pkg-manifests.mjs` for the app), and says so in the run when
  a store's credentials are missing instead of skipping it quietly. A new
  winget package waits for winget's review for its first release. A copy
  installed by a store never runs its own updater: Settings names the store
  and opens its page.
- **An `.rpm` for Fedora and openSUSE**, next to the `.deb` and the AppImage.
- **A Microsoft Store (MSIX) build of the desktop app** (`pnpm dist:store`,
  checked as an installed Store copy by `pnpm e2e:store`). It is ready for
  Partner Center; the Store listing comes later. The Store version carries a
  mapping the Store requires (0.43.x → 1.0.43xx.0); the app keeps reporting its
  own version.
- **A privacy page, [filex.sh/privacy](https://filex.sh/privacy/)** (English and
  Turkish): what the desktop app, the website and the public demo do and do
  not do with information. The stores link to it.

- **A third install mode, `package`: filex installed by Homebrew, winget or
  Snap leaves its binary to the package manager.** filex recognizes the
  layout around the running binary (links followed): a Homebrew
  `Caskroom`/`Cellar` keg, a winget `WinGet\Packages\<id>_<source>` folder, or
  `SNAP`/`SNAP_NAME` with the binary under `$SNAP`. There `filex self-update`
  refuses and prints the manager's command (`brew upgrade --cask filex`,
  `winget upgrade BRFTech.filex`, `snap refresh filex`), `--check` still
  reports the release, and no update policy ever replaces the binary - the
  package manager would otherwise keep recording the old version and write
  over ours at its next upgrade (a snap is read-only). **Ops → Updates** names
  the manager and shows its command instead of **Upgrade now**.
  `FILEX_INSTALL_MODE` also takes `package`, `homebrew`, `winget` and `snap`
  (a `.deb`/`.rpm`/AUR package can declare itself). See
  [UPDATES.md](./UPDATES.md#package-manager-installs).
- **Archives are made, opened and extracted in the explorer, with passwords,
  in the background.** Select files and choose **Create archive…** for a ZIP,
  7z, TAR, TAR.GZ, TAR.BZ2 or TAR.XZ, with a password (ZIP, 7z) and hidden
  file names (7z) if you like; open an archive to browse it like a folder;
  extract all of it or **Extract here**. An encrypted archive asks for its
  password first. Both directions run as operations of the queue, in a lane
  of their own, with progress and cancel in the operations centre, and
  **Settings → Archives** sets the formats, the size, entry and time limits,
  and tests the provider. By Alex ([@ahjephson](https://github.com/ahjephson),
  [#48](https://github.com/BRF-Tech/filex/pull/48)); see `docs/ARCHIVES.md`.
  - filex reads plain ZIP, TAR, TAR.GZ and TAR.BZ2 itself, on every install.
    7z, XZ and password-protected ZIP, and creating anything but a plain ZIP,
    need 7-Zip 25.01 or newer: the full image ships 26.01, and a server
    without it offers ZIP only, with no password fields. RAR extraction needs
    a 7-Zip built with RAR support, which Alpine's package (the full image's)
    is not; `FILEX_ARCHIVE_7Z_BIN` can point at one that is.
  - An archive is refused before anything is written when it holds a link or
    a special file - also when 7-Zip shows one only by its file mode (a
    `zip -y` or `7zz -snl` symlink, a FIFO or device in a TAR, a RAR5 file
    copy) - or a member that does not declare its size. The size limit stops
    a TAR-family archive at the exact byte, a gzip whose trailer lies
    included, because filex reads those itself. The archive type comes from
    the file name, never from its bytes; a password reaches 7-Zip on its
    standard input, never on its command line; and every member lands
    through the same gate as any other write, so it cannot replace a document
    an app has locked or land in filex's own folders.

- **Lazy catalogue for big local storages - `sync_mode: lazy`**
  ([#45](https://github.com/BRF-Tech/filex/issues/45); the idea is Alex's,
  @ahjephson). Every other mode catalogues a storage by walking all of it first;
  on a multi-terabyte NAS that is hours before the first folder is right. Now
  the folder somebody opens is listed straight from disk at once, with what the
  catalogue already knows laid over it, and is catalogued first in the
  background. Two behaviours per storage: **click first, fill in the
  background** (the default - a slow pass catalogues the rest, slowing down
  while people use the storage, honouring scan exclusions and carrying on after
  a restart) and **only on open** (nothing runs in the background; an
  administrator can catalogue everything once). Opened folders are watched for
  outside changes within a budget (`lazy_max_watches`, `lazy_watch_ttl`), and a
  desktop sync pair gets its whole subtree catalogued and kept current. ⚠ A
  folder nobody visited is never treated as deleted: rows are only removed from
  a folder that was just listed in full, each one confirmed gone. See
  [docs/STORAGE.md](./STORAGE.md#lazy-catalogue) and the design in
  [docs/LAZY-CATALOGUE.md](./LAZY-CATALOGUE.md).
- **The storage form offers the sync mode.** It could only be set through the
  API; the new and edit forms now have **Sync mode**, and the lazy catalogue's
  settings drawn from the driver descriptor (`lazy_fields`).
- **Search, folder sizes and drive usage say when they do not cover a whole
  storage.** One line above the listing and the search results names the
  reason (a first sync still running, a lazy catalogue still filling, a storage
  catalogued only on open); a folder whose size leaves something out reads
  `≥ 1.2 GB`, or `-` when nothing below it is catalogued yet; Home's drive card
  says *at least … used*. The storage page shows the catalogue's progress, the
  background pass and the watch budget.

### Changed

- **Linux: the desktop app's command is now `filex-app`,** and so is its
  package (`.deb`, `.rpm`) and desktop entry - `filex` is the CLI's name, and
  with both installed the command you typed depended on the order of your
  `PATH`. Installing the new package replaces the old `filex` one in a single
  step, and the app moves its *Start when I sign in* entry and the default-app
  choices made under the old name.
- **The container images move from Alpine 3.20 to 3.24, and the full image
  grows by about 390 MB** (its download by 134 MB, 545 → 679 MB). Alpine
  3.20 left support in April 2026, and archives need 7-Zip 25.01 or newer
  (25.00 and 25.01 fixed its ZIP symlink traversal, a RAR5 overflow, a
  compound-document crash and link handling during extraction:
  CVE-2025-11001/11002, CVE-2025-53816/53817, CVE-2025-55188). 3.24 ships
  7-Zip 26.01 and newer ffmpeg (8.1),
  Ghostscript (10.07), ImageMagick, poppler, librsvg and LibreOffice (25.8).
  On Alpine since 3.22, LibreOffice depends on Qt 6, which brings Mesa and
  LLVM with it: the full image's package layer grows from 1.13 GB to 1.52 GB.
  Thumbnails and every conversion engine (ffmpeg, ImageMagick, LibreOffice,
  Ghostscript, poppler, rsvg) were smoke-tested on the new image. The slim
  image moves to 3.24 as well and still ships no 7-Zip.

### Fixed

- **A right-click menu taller than the window scrolls.** With a few apps
  installed the menu outgrew a small window and its last items could not be
  reached; it now fits the window and scrolls inside itself.
- **Desktop app (0.43.0 - 0.43.2): the main window runs again.** An HTML
  comment in the Settings template quoted a function name in backticks inside
  a JavaScript template literal, which ended the literal early; the browser
  rejected the page's whole script, so after signing in the window drew its
  static frame and nothing worked. A test now parses every inline script of
  the desktop pages.
- **Desktop app (0.43.0 - 0.43.2): Settings shows your synced folders again.**
  The folder cards read a value that 0.43.0 had moved into another function,
  so with any folder paired Settings stopped drawing at that point: no folder
  card, no status line, no *Stop* button.
- **Linux: signing in through the browser returns to the app.** No Linux
  package declared the `filex://` link, so the browser had nothing to hand
  the sign-in back to and only pasting the code worked.
- **Linux: without a keyring the app says so before the sign-in.** It used to
  send you through the browser, spend the one-time code and then fail in
  English; it now refuses up front and explains what to install or connect
  (for the snap, the exact `snap connect` command).
- **Desktop app: a failed browser sign-in shows its reason again** (the error
  window did not refresh when it was already open).

- **Two filex on one computer no longer sync the same folder at the same
  time.** Nothing stopped a second engine on a pair that one was already
  syncing - the desktop app and `filex sync run` in a terminal, or an
  installed copy of the app and the Microsoft Store one, which reads the same
  sign-ins and, living in its own virtualised profile, does not see the other
  copy's single-instance lock. Both planned from the same history, so files
  were uploaded twice, conflict copies appeared out of nothing and a delete
  could travel to the side that did not ask for it - silently. Every run now
  holds an operating-system lock on its pair (`~/.filex/sync/locks/`,
  `LockFileEx` on Windows, `flock` elsewhere), released by the OS however the
  process ends. A pair another process holds is left untouched: `filex sync run` says `lock: busy`, syncs the rest and exits with status 4; `--watch`
  waits and takes the pair over when the other process stops; the desktop app
  shows *Another filex on this computer is syncing this folder* under it
  instead of an error, and does not restart its engine.

- **During a storage's first sync, a folder shows everything in it.** A
  partly catalogued folder - above all the root of a big storage - used to list
  only the entries the sync had reached so far, for as long as it ran. Until the
  first sync has finished, a folder is listed from the storage with the
  catalogue laid over it (ids, owners, thumbnails and tags kept), on every
  driver.
- **A full sync that met another writer at a folder no longer skips that
  folder's contents.** When the insert of a folder row lost a race, the walk
  gave up on the subtree and the sync finished with a hole in the catalogue;
  it now carries on with the row the other writer created.
- **The desktop's upload precondition is judged against the disk when the
  listing came from the disk.** A file with no catalogue row (or a drifted one)
  is compared with the file that is actually there, and `expect=none` is
  refused when a file already sits at that path.

### Security

- **Desktop: the app runs on Electron 44** (Chromium 152, Node 24), up from
  Electron 31, whose support ended on 2025-01-14 - every Chromium security fix
  since then was missing from the desktop app. Electron 44 is supported until
  2027-03-02. **The macOS build now needs macOS 13 (Ventura) or later**; Windows
  10/11 64-bit and 64-bit Linux are unchanged. An account signed in under the
  old runtime stays signed in (measured: a profile written by Electron 31 opens
  signed in under 44, and the other way round). The Windows installer grows
  from 96 MB to 132 MB: the runtime binary itself grew (181 → 246 MB unpacked;
  ANGLE is linked into it now) and Chromium ships a DirectX shader compiler
  (27 MB).
  Three behaviour changes of the new runtime are handled in the app rather
  than shipped: a failed browser sign-in still says why (the sign-in window
  had stopped redrawing when it was sent to the address it was already on),
  "Copy link" in the share menu waits for the now-asynchronous clipboard, and
  the Settings folder picker opens where the last pick was made instead of in
  Downloads every time.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0440---2026-09-25)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.44.0) · `ghcr.io/brf-tech/filex:slim-v0.44.0`

## v0.43.2

<span class="filex-release-date">24 September 2026</span>

The release that 0.43.1 was meant to be: container images for everything in 0.43.0 (whose images never published), the storage-credential scrub, Berk Başarır's per-tile thumbnail rendering and a desktop sign-in fix for servers behind a private CA. Its desktop packages have a blank main window - use 0.44.0.

> [!WARNING]
> **Desktop app: don't install the v0.43.0-v0.43.2 desktop packages.** Their main window stays blank - a backtick inside an HTML comment in the Settings template ends a template literal early, so the window's script never runs. The fix shipped in [v0.44.2](https://github.com/BRF-Tech/filex/releases/tag/v0.44.2): install the desktop app from there (the in-app updater offers it too; on macOS, install it by hand - the Mac build is not code-signed yet). The server, the CLI, the container images and the npm packages of v0.43.2 are not affected.

## What changed

A fix-forward release for 0.43.0, and **the one to deploy. v0.43.0's
container images were never published**, and neither was anything from
v0.43.1: that tag's release gate stopped the run before a single package,
image or page went out. 0.43.2 is 0.43.1 as it was meant to ship, plus the two
fixes that gate asked for (below). Its npm packages and its GitHub
Release went out, but the image build failed on every attempt, so
`ghcr.io/brf-tech/filex` has no `v0.43.0` or `slim-v0.43.0` tag and `latest`
stayed on v0.42.2 until this release. The Helm chart and the CasaOS, Umbrel
and Runtipi manifests at 0.43.0 name that missing image, so installing or
upgrading from them fails to pull; at 0.43.1 they name one that exists.
Everything the 0.43.0 notes below describe is in 0.43.1 - **including its
security fixes, which an install that runs the image gets only now** - and two
more fixes: from Berk Başarır, a big folder no longer re-renders itself for
every thumbnail that arrives, and the desktop app signs in to a server behind a
private CA.

### Security

- **Container, Helm and app-store installs get 0.43.0's security fixes only
  with this release - upgrade promptly.** With no v0.43.0 image, every install
  that runs `ghcr.io/brf-tech/filex` is still on v0.42.2 or older, without
  anything 0.43.0 closed: an unsigned ONLYOFFICE save callback that lets
  anybody who can reach the server overwrite a file, a narrow API token that
  could mint a full-rights one, API answers a caching proxy could hand to
  every visitor, and a notification bell that named files its reader could
  not open. Read 0.43.0's ⚠⚠ notes before upgrading: a Document Server running
  with `JWT_ENABLED` off stops saving, and desktop sync needs the new desktop
  app as well as the server.
- **The public repository no longer carries a storage credential.**
  `scripts/seed-example-fixtures.sh`, which seeds the project's own demo
  server, held an object-storage access key and secret as the fallback values
  of `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`, and it was published with
  every release from the first one through v0.43.0. The key has been revoked
  and replaced. The script now takes both from the machine it runs on and is
  no longer published. No filex install used it - it only ever wrote demo
  files to the project's own bucket.
- **A test fixture no longer names the project's own servers.**
  `e2e/fixtures/file-types/diagram.drawio` labelled two boxes with the public
  addresses of the project's main server and its mirror. They are RFC 5737
  documentation addresses (`192.0.2.10`, `198.51.100.20`) with neutral labels
  now.
- **The export that builds the public tree refuses both.** It now stops on any
  of the project's own server addresses and on any credential written as a
  shell default (`…SECRET…=${VAR:-literal}`), in whatever file it turns up.

### Fixed

- **The container images build again, and a release can no longer publish
  without them.** The frontend stage of `docker/Dockerfile` and
  `docker/Dockerfile.slim` copied the workspace manifests, `packages/` and
  `web/`, but the build reaches outside them: its configs import two files
  from `scripts/` (`vite-fonts-as-files.mjs` and `lib/i18n-catalogue.mjs`,
  whose type declaration the admin build's type-check reads too), and the
  translator's catalogue is built from the server's string tables in
  `backend/internal/srvtext/locales/`. Both images copy all of it now. Nothing had built an image before the tag: the release ran the CI
  workflow as its gate with the image build switched off, then published the
  npm packages and the Release beside the image job rather than after it. The
  gate now builds both images, with no switch to turn that off, before
  anything is published, and the release checklist builds both locally before
  a tag is made. Two tests keep it that way: one follows what the frontend
  build reaches outside `packages/` and `web/` - imports, their type
  declarations and the paths they read - and fails if either image does not
  copy it; the other fails if the release skips the image build or publishes
  before its gate.
- **A big folder no longer re-renders itself once per thumbnail.** Each
  thumbnail that arrived re-rendered the whole grid, gallery or table: a
  folder of 344 files, about 240 of them with thumbnails, ran a Chrome tab on
  a Windows PC out of memory and froze Edge for about 30 seconds. Every
  thumbnail is drawn by a tile of its own now, so an arrival re-renders that
  tile and nothing else. A thumbnail is also fetched only when its tile comes
  near the screen, not fetched again because its signed link was renewed on
  the hour (a new version of the file still is), and past the cache's 500
  pictures, dropping the oldest no longer sets off an endless round of
  re-fetches. Found and fixed by Berk Başarır
  ([#50](https://github.com/BRF-Tech/filex/pull/50)).
- **The docs site's release page no longer fails to build when a release note
  wraps a code span across lines.** v0.43.0's notes wrapped the command
  `filex sync confirm <pair>` so that its second line opened with the
  placeholder, which the site read as an unclosed component, and every hourly
  refresh of the site failed. The span is joined back onto one line now.
- **Desktop: signing in to a server whose certificate comes from a private CA
  no longer fails with "fetch failed".** The request that trades the browser's
  one-time code for the app's token was the only one in the app's main process
  that went through Node's own HTTP client, which ignores the operating
  system's certificate store and proxy settings. A server behind a company or
  home CA - trusted by the browser, so the browser half worked - failed there
  after the server had already spent the code. It goes through Electron's
  network stack now, like every other request the app makes, and a test fails
  if anything in the main process uses the other client again
  ([#36](https://github.com/BRF-Tech/filex/issues/36)).
- **A release page that has to be shortened ends on a line break.** The
  GitHub release body is capped, and when an entry's opening paragraph alone
  ran past the cap the cut fell mid-word; it now falls back to the last line
  break.

### Tests

- **"Empty trash" leaves what is deleted while it runs - now proven on a run
  longer than one batch, and on one that waits its turn.** v0.43.0 already
  bounds an empty by the moment it was asked for: the ops row's `created_at`,
  on the database's clock, which a restart does not lose. Berk Başarır's field
  report - on the pull request's earlier runner, a 1 h 48 min run purged
  61,845 rows of a total of 61,844, the extra one a file a member deleted six
  minutes before the end - came with a test that deletes a file while a run of
  more than one batch is under way. It now runs against the job at both the
  trash and the ops level, beside a new one for a run queued behind another
  tenant's ([#47](https://github.com/BRF-Tech/filex/pull/47)).
- **The viewer audit opens draw.io and ONLYOFFICE when they answer.** e2e 100
  waited for a capability state of `reachable`, which the server has never
  sent (it says `ok`), so both rich viewers were skipped on every run, even
  with the services configured.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0432---2026-09-24)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

## Security advisories

- [GHSA-gj84-9xx7-2g9f](https://github.com/BRF-Tech/filex/security/advisories/GHSA-gj84-9xx7-2g9f) - an unsigned ONLYOFFICE save callback was accepted (High, 7.5)
- [GHSA-55vh-f285-5wv3](https://github.com/BRF-Tech/filex/security/advisories/GHSA-55vh-f285-5wv3) - a restricted API token could issue credentials wider than itself (High, 8.8)

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.43.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.43.2`

## v0.43.0

<span class="filex-release-date">24 September 2026</span>

Apps. filex gains a second kind of plugin: a sandboxed WebAssembly app that adds actions to the file menu, screens filex draws for it, and public pages for people without an account - installed from a GitHub repository, a file or a URL, and always through a review of every permission it asks for. Two ship alongside this release, each as a public repository you can read and fork: e-Signature (filex-sign) sends a document round for signature - the boxes defined, then placed, a PIN-protected link for a signer without an account, the document frozen for everyone while it is out if you ask for it, a PAdES signature from the instance’s own signing authority, and a final seal by filex itself whose SHA-256 goes to every party - and Convert (filex-convert) turns a file into another format with the engines the server has, asking where to put the result when the file sits on a storage it cannot write to. An app can lock a file until it lifts the lock, keep per-file state and list its own work, fetch a pinned asset once, share the file its own job is writing, send addressed notifications and mail, open a home page of its own under Apps, and be woken on the hour to act at the minute it named; the SDK ships a test kit that runs before the module is built.

Language packs. A manifest with nothing that runs adds a language to filex - no module, no Go, no release - and translates the explorer, the admin panel and the public pages alike, with its coverage of this version shown beside it. The text filex’s server writes goes through the same catalogue: e-mails, notification phrases, the no-JavaScript pages behind a link and the install review all answer in the reader’s language. Plural forms follow CLDR, so a pack may write the forms its language actually has. And the interface lays itself out right to left for Arabic, Hebrew, Persian and Urdu - mirrored where mirroring is right, never in document space or in machine text. A template repository walks a translator from export to install, and Spanish, German and French ship as examples.

Around them: a download share, a file request and an app’s page are one branded shell with one PIN gate, and five wrong PINs shut any of them for ten minutes. People see the links they created under My shares and can read their PINs back. The Appearance screen composes themes for the whole instance, sign-in page and share visitors included. Admin → Identity providers now really drives sign-in, with a Test now that tests it. Tags are personal or shared with the team. Preferences follow the person rather than the browser, and a folder can remember the view and sort you left it in. Desktop folder sync is live in both directions - an edit on either side arrives in about a second instead of on the next 30-second lap - with bandwidth limits, a sync window, and a first run that holds back a re-upload and asks. The bell carries its count, and everybody can read all of their notifications. Symlinked folders inside a local storage open, a link that cannot is badged with the reason, and follow_symlinks decides where the storage ends. Every table in filex is the explorer’s table, in the admin panel too, each row ending in one pinned Actions menu.

Security - upgrade promptly. With ONLYOFFICE configured, the save callback was accepted without a JWT even when filex held a secret, so anybody who could reach the server could have a file overwritten; it is now refused. A Document Server running with JWT_ENABLED off must have it turned on, or its saves will fail. Separately, every door that issues a credential - desktop pairing, self-service API tokens, S3 access keys, NFS exports, SSH keys - trusted the account behind the caller rather than the caller, so a read-only or folder-confined token could mint a full-rights one; they all refuse now with 403 token_ceiling, and desktop pairing takes a signed-in browser only. An API token with no scopes no longer means every scope. Tags were visible to every account on the server, an app’s freeze held only in the explorer, anybody who could write could write into filex’s own folders, a local storage could be escaped on Windows with a backslash path, and symlinks leaving a local storage were followed by everything including recursive delete - all fixed.

Upgrade notes: migrations 00042-00055 run on the first start. Tokens created with no scopes are rewritten to an explicit list that includes admin - review and narrow them. Identity providers saved on the admin page before this release come back switched off, marked for review. Existing tags become team tags, and a client that sends no kind makes personal ones. Custom CSS stays off until you switch it on again under Appearance, and custom_css is gone from GET /api/branding. Live desktop sync needs the new desktop build, and a reverse proxy must pass WebSocket upgrades on /api/ws. A rename onto a taken name answers 409 instead of replacing the file; an agent’s move still takes a free name. A program that wants the 202 “preparing” answer must ask for it with X-Filex-Accept-Prepare: 1. Symlinks already inside a local storage are re-catalogued on the next scan: an in-root link gains its contents, an out-of-root one stops being indexed, scanned and counted against quota unless follow_symlinks is on.

> [!WARNING]
> **The v0.43.0 container images were not published** - the image build failed on this tag. If you run filex in Docker, use **v0.43.2**, which carries the same changes with working images (v0.43.1 was tagged but never published). The binaries, desktop packages and npm packages on this page are complete.

## What changed

The release that makes filex extensible. **Apps** are a second kind of
plugin - a sandboxed WebAssembly module that adds things to *do* with
files - and two ship alongside it as public repositories: **e-Signature**
and **Convert**.

A **language pack** is an app with nothing that runs, so filex can be
translated without waiting for a release; the text the server writes - mail,
notifications, the pages behind a link - comes from the same catalogue; and
the interface lays itself out **right to left** for the languages that read
that way.

Alongside them: desktop folder sync is now **live** in both directions, the
explorer's table is the only table left in the product, an operator composes
the instance's **theme**, tags are personal or shared with the team, and
every door that issues a credential refuses to issue one wider than the
caller.

> ⚠⚠ **Security - upgrade promptly.** Every filex up to v0.42.2 with ONLYOFFICE
> configured accepts an unsigned save callback, which lets anybody who can
> reach the server overwrite a file (Security). filex has never
> enabled the editor without a JWT secret - an install with no secret has
> editing off and is unaffected - so the one thing to check is the other
> side: a Document Server running with `JWT_ENABLED` off sends unsigned
> callbacks, and after this upgrade its saves fail. Turn JWT on there, with
> the same secret filex holds.
>
> ⚠⚠ **A narrow API token could mint a wide one.** Every door that issues
> a credential - the desktop pairing endpoint, self-service API tokens, S3
> access keys, NFS exports and SSH keys - measured the account behind the
> caller instead of the caller itself, so a token restricted to reading, or
> confined to one folder, could create a full-rights credential for the same
> account and then use it (Security). Every one of them now
> refuses with `403 token_ceiling`. The desktop door was found and fixed by
> Berk Başarır ([#35](https://github.com/BRF-Tech/filex/pull/35)).
>
> ⚠⚠ **Desktop sync could replace real files - upgrade the server AND the
> desktop app.** A field report, traced and fixed by Berk Başarır
> ([#35](https://github.com/BRF-Tech/filex/pull/35)), found filex's own sync client writing a server's
> `202 "preparing"` status report to disk as the file and uploading it over
> the original, conflict copies nesting by the thousand, a stale mirror
> re-uploaded over a cleaned-up server, and a rename onto a taken name
> destroying the file that had it (Fixed). The server fix protects
> every client already installed; the new desktop build carries the rest.
>
> ⚠⚠ **A cache in front of filex could hand one person's answers to
> everybody.** API answers carried no `Cache-Control`, so a CDN rule that
> caches everything kept `GET /api/auth/me` for two hours and served one
> administrator's identity to every visitor. Every `/api` answer is now
> `no-store` unless it is one of the four answers that say who the instance
> is (Security). Found and fixed by Berk Başarır
> ([#41](https://github.com/BRF-Tech/filex/pull/41)).
>
> ⚠⚠ **The notification bell named files its reader could not open.** Queued
> copies, moves and deletes were announced to every account, a member of an
> RBAC storage read the names of files in folders they have no grant on, one
> person's "mark all read" read everybody's alerts, and a tenant admin could
> read every tenant's notification history (Security). Found and
> fixed by Berk Başarır ([#42](https://github.com/BRF-Tech/filex/pull/42), [#43](https://github.com/BRF-Tech/filex/pull/43)).
>
> ⚠ **Before upgrading, read Upgrade notes**: API/MCP tokens
> created with no scopes are rewritten to an explicit list that includes
> `admin` - review and narrow them; identity providers saved on the admin
> page come back switched off; existing tags become team tags, and a client
> that sends no kind now makes personal ones; custom CSS is off until
> switched on (see *Changed*); live desktop sync needs the new desktop build;
> a desktop pairing needs a signed-in browser, and existing pairings keep
> their old token until paired again; a rename onto a taken name answers
> `409`; a program that wants the `202` "preparing" answer must now ask for
> it (`X-Filex-Accept-Prepare: 1`); with SSO, sign-out now ends the identity
> provider's session too, and the provider must allow filex's sign-in pages
> as a post-logout return address; `POST /api/admin/trash/empty` answers
> `202` while a large trash is still being emptied, and `400` for a value it
> cannot read.

### Added

- **Apps (app plugins) - a sandboxed plugin that adds things to *do* with
  files.** A WebAssembly module that adds actions to the file menu, screens
  filex draws for it, and public pages for outside participants. Install from a GitHub repository
  URL, a file upload or a URL, always through a permission review; the grant
  is exactly the manifest's list and an upgrade that asks for more stops at
  the review again. Actions run as ops-queue jobs (progress, cancel, open the
  output), write through the same path as every other write, and are gated
  by ACL, read-only storages and encrypted folders at submit. Host functions
  under one permission each: files, per-file state, settings (secrets sealed),
  the server's engines (ffmpeg, ImageMagick, LibreOffice, Ghostscript, poppler,
  rsvg) with bare-token arguments, directory lookup, notifications (new
  `plugin.notice` event), mail (60/hour), guarded outbound HTTP, public links
  for outside participants (PIN with lock-out, exposed copies, jobs as the
  link's creator) and host-held signing (per-tenant CA, certificates,
  `host_sign`, admin CA download/rotate). Admin: **Plugins → Apps** tab with
  the install wizard, settings, per-action overrides and logs. Explorer:
  menu rows, ops-tray jobs, surface screens (form, steps, list, progress,
  people picker, PIN, file chooser, preview, PDF fields, signature pad),
  details-panel sections and an **Apps** side-bar section. Guest SDK
  `pkg/pluginkit` (stock Go, `GOOS=wasip1`). Docs: `APP-PLUGINS.md`,
  `PLUGIN-KIT.md`. Off in demo mode (`FILEX_APP_PLUGINS_DISABLED`).
- **Language packs - filex can be translated without a release.** An app can
  add a language to filex, and it now does so end to end. A manifest that only carries `ui_locales` is a *language pack*:
  it installs from the manifest alone (upload, GitHub repository or URL - no
  module, no Go, no release), never starts a runtime, and is listed in
  **Plugins → Apps** as a *Language pack* with each language's coverage of the
  running version's catalogue (*Español - 97% translated · the rest shows in
  English*). Its language joins every picker - the settings dialog, the admin
  header, public share pages - and translates the explorer, the admin panel
  and the public pages alike; anything it lacks shows in English. Integrity
  moves to the manifest (the sha256 pin and, with `FILEX_PLUGIN_TRUSTED_KEYS`,
  the signature are over the manifest). A right-to-left language lays the
  interface out right to left (see *Right-to-left layout*). Format, grammar and limits:
  `PLUGIN-KIT.md` → *Writing a language pack*; a template repository,
  `BRF-Tech/filex-lang-template`, walks a translator from export to install.
- **Right-to-left layout.** Arabic, Hebrew, Persian, Urdu and every other
  right-to-left language a language pack adds now lays the whole interface
  out right to left - the explorer, the admin panel, the public share, PIN
  and file-request pages, the signing wizard and the embeddable components.
  Whether a language is right to left is the server's one list
  (`wire.IsRTL`, the `rtl` flag on the offered-language rows); a page's `dir`
  is derived from its `lang`, and an embedded explorer takes its direction
  from its OWN `locale`, not the host page's (menus drawn under `<body>`
  carry it too). The stylesheets are written in logical properties and the
  admin panel in Tailwind's logical utilities (`ms-`, `pe-`, `start-`,
  `text-end`, …), so English and Turkish draw the same pixels as before.
  Gestures turn with the layout: a column's edge grows the way the pointer
  drags it, a dragged column lands on the side of its neighbour the pointer
  is on, the arrow keys move things the way they point, menus open toward the
  line's end and flip at the screen's edge, and the frozen Name and Actions
  columns draw their edges while a right-to-left table scrolls. Icons that
  mean a direction (back/forward, breadcrumb and disclosure chevrons, undo,
  sign-out, send, the panel icons) are mirrored; "open in a new tab",
  refresh, media controls and **document space** - a PDF page's signature
  boxes, images - never are. Commands, paths and URLs stay left to right;
  file names, paths and people are isolated (`<bdi>`), and number pairs,
  `tag:…` tokens and names inside a right-to-left sentence are isolated so
  they keep their order. List fields (recipients, extensions, tags,
  identities) accept the Arabic `،`, ideographic `、` and fullwidth `，`
  commas. A source check (`web/tests/quality/rtlLogical.test.ts`) keeps
  physical `left`/`right` from coming back. Docs: `docs/RTL.md`.
- **Appearance: the instance can wear your colours.** A new admin screen
  (**Appearance**, `/admin/appearance`) where an operator composes
  named themes - twelve colours per light/dark variant, a corner radius and a
  font stack - and picks one as the instance default. Ten more tokens are
  derived server-side and stored with the theme, so the browser does no colour
  maths at paint time, and the text colour on a coloured button is chosen by
  WCAG contrast rather than assumed to be white. Served to the login page and
  to anonymous share visitors as well, because a palette that stops at the
  sign-in screen is not branding. Themes export and import as one JSON
  document - the exported file *is* the upload body (migration 00051).
- **Desktop sync is live: an edit on either side arrives in about a second.**
  A save in the web app's text editor, an ONLYOFFICE save, an upload or a
  delete now reaches the synced folder on the desktop as it happens, and a
  save on the desktop reaches the server just as fast - instead of waiting for
  the engine's next 30-second lap. Measured on one Windows machine against a
  local server, six edits each at random moments: browser text save → file on
  disk 6.2-24.8 s (median 15.2 s) before, 0.19-0.22 s (median 0.20 s) now;
  ONLYOFFICE callback → disk 0.5-25.9 s before, 0.19-0.21 s now; local save →
  server 9.4-29.0 s (median 25.3 s) before, 0.43-0.61 s (median 0.46 s) now.
  `filex sync run --watch` subscribes to the server's change stream (the same
  WebSocket the web explorer uses, authenticated with the engine's own token
  through a ws-ticket) and watches the local folders with file-system events;
  a change reconciles just the folder it happened in (one listing, not a walk
  of the tree), and the `--watch` interval is the safety net - it no longer
  walks every pair, see *Changed*. `--live=false` leaves only the interval. The
  engine prints `live: connected|polling|offline - …` and the desktop app
  shows it as one word under each synced folder (*Live* / *Polling* /
  *Offline*). A newly paired folder starts syncing at once instead of on the
  next lap. See [Folder sync](./SYNC.md#how-fast-a-change-arrives).
- **Realtime: recursive, presence-less `watch` subscriptions.**
  `{"type":"watch","paths":[…]}` registers any number of roots per socket,
  authorised exactly like a room subscribe (confinement, RBAC, tenant), always
  acknowledged with `watching` (the capability probe for older servers), and
  delivered as `tree_change` frames with root-relative folders, coalesced like
  rooms and never dropped on a full queue. Folder-size refresh frames are not
  delivered to watches. See [Realtime](./REALTIME.md#watching-a-whole-tree-sync-clients).
- **Conditional uploads.** The multipart upload and the staged commit accept
  `expect` (`none`, or the `<size>:<last_modified>` a listing showed) and
  answer `412 PRECONDITION_FAILED` without writing when the file changed in
  between. The sync engine sends it on every upload, so a browser save that
  lands in the same second as a desktop save is kept beside it instead of
  being replaced. See [Uploads](./UPLOADS.md#conditional-uploads-expect).
- **A first sync that would re-upload a stale copy holds it and asks.** With no
  history, "new here" and "deleted on the server" look the same, and one client
  put 9,665 cleaned-up files back on a server. A first run that would upload
  more than 100 local-only files into a server folder that already has files
  holds them (and any file that differs) and runs the rest: `filex sync confirm <pair>` sends them, `filex sync discard <pair>` moves them to the local sync
  trash so the folder matches the server. `sync list --json` carries `hold_new`
  / `held`; the desktop app shows the count with **Upload them** and **Move to
  local trash**. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Bandwidth limits and a sync window.** `filex sync run --limit-down` /
  `--limit-up <KiB/s>` cap all transfers of a run together (the bodies are
  paced, never the connection), and `--window HH:MM-HH:MM` only syncs in that
  part of the day - outside it nothing talks to the server, not even the change
  stream, and a pass still busy when it closes stops cleanly and continues in
  the next window. The desktop app offers both as presets in Settings. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Transfer progress in bytes, with an estimate:** `transfer: 120/11704 (1.2 GiB of 52.6 GiB, about 8h 10m left)`, printed at least every 5 seconds. The desktop
  app shows it on each folder's line, in its own language. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **`GET /api/files/manager?action=changes&path=…&since=<cursor>`** answers "has
  anything under this folder changed since my cursor?" as `{ cursor, changed }`,
  from an in-memory change log fed by every write surface. Every doubt - no
  cursor, a restart, a cursor older than the log - is `changed`, and a change
  counts only if the caller can see what it touched. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Rescan one folder: `POST /api/admin/storages/{id}/sync?path=<folder>`.** The
  same walk over one catalogued folder, synchronously, answering `{path, scanned, added, updated, removed, reconciled}` (504 after ten minutes). Only
  rows inside the folder can be removed, a listing that failed part-way removes
  nothing, and no sync-run row is written. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Scan exclusions: tell a storage what not to catalogue.** A new storage
  setting, *Paths to exclude from scanning* (`config.scan_exclude`, on every
  driver), takes glob patterns relative to the storage root, one per line:
  `*` within a name, `**` across folders, and a pattern without a `/` names an
  entry at any depth (`.*` skips every hidden file and folder, `@eaDir` every
  Synology thumbnail folder, `*.tmp` every temp file), while `/build` or
  `downloads/incomplete/**` is anchored at the root. The scan does not go
  **into** a matching folder - a `.git`, a `.snapshots` or a download client's
  `incomplete/` costs nothing - and nothing matching is catalogued, indexed,
  thumbnailed or virus-scanned. One rule decides every walk that catalogues
  (the full scan, the one-pass object-store listing, a folder rescan - which
  refuses an excluded folder with 400 - the catalogue of a copied folder) and
  the `fsnotify` watcher, where a change to an excluded path no longer starts
  a scan. **A cost control, not an access control:** the files stay on the
  storage and are still served by path, over the file protocols and to the AI
  tools. Rows catalogued before a pattern was added stay as they are and are
  never moved to the trash for being unseen; filex's own names (`.filex-open`,
  `.keepdir`, an encrypted folder's marker) are outside the patterns; a pattern
  that would exclude everything, a `!`, a `..` or a broken glob is refused on
  save with `SCAN_EXCLUDE_INVALID` and a sentence naming the pattern in the
  reader's language - as is an empty or `/` storage root (`ROOT_PATH_FORBIDDEN`,
  which used to answer one English sentence in `error`; the code stays there
  and the sentence moved to `message`). Docs: [STORAGE.md → Scan exclusions](./STORAGE.md#scan-exclusions).
  ([#44](https://github.com/BRF-Tech/filex/issues/44))
- **Desktop: Pause sync,** in the tray menu and Settings, remembered across
  restarts, reboots and the hidden start at sign-in. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **Desktop: the computer stays awake while sync moves files** (the screen still
  locks); an overnight first sync lost 1 h 40 min to idle sleep. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **The access log says who asked:** `user_id`, `token_id` (the row id, never the
  secret) and, in multi-tenant mode, `tenant` on every `msg=http` line, plus
  `action` for `/api/files/manager` (one of its own verbs, or `other`). The
  query string is still never logged. ([#35](https://github.com/BRF-Tech/filex/pull/35))
- **A cut search result says so:** both search responses carry `truncated`, and
  the explorer shows "More results than shown - narrow your search". ([#35](https://github.com/BRF-Tech/filex/pull/35))

- **Apps: the platform seal.** `cert_issue {purpose: "platform"}` hands an app
  with `sign` the installation's own seal: one key per tenant and app, kept by
  the host (never destroyed - `key_destroy` refuses it), CN *filex document
  seal*, OU the app's name, issued by the live authority and re-issued by the
  next one after a rotation; it signs through `host_sign` from jobs only. The
  signing app seals every completed request with it (the owner, 2026-09-22:
  "the final bytes are sealed by filex itself"). SDK: `pluginkit.PlatformSeal`,
  `plugintest.Host.PlatformSeal`.
- **Apps: a lock until lifted, and a lock on the job's own output.**
  `file_lock` takes `ttl_days: -1` (`pluginkit.LockUntilLifted`) - no end,
  until the app or an administrator (audited) lifts it - and a `ref` naming one
  of the job's OWN outputs, which is promised and taken when the output is
  committed (never when the job fails). How the signing app keeps a finished
  document locked for good, when the request asks for it.

**This release has more to it than fits on one page.** The rest of the
entry - and every earlier release - is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0430---2026-09-24).

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

## Security advisories

- [GHSA-gj84-9xx7-2g9f](https://github.com/BRF-Tech/filex/security/advisories/GHSA-gj84-9xx7-2g9f) - an unsigned ONLYOFFICE save callback was accepted (High, 7.5)
- [GHSA-55vh-f285-5wv3](https://github.com/BRF-Tech/filex/security/advisories/GHSA-55vh-f285-5wv3) - a restricted API token could issue credentials wider than itself (High, 8.8)

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.43.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.43.0`

## v0.42.2

<span class="filex-release-date">19 September 2026</span>

A fix release for the issue 32 follow-up and three things that were wrong on screen. The context menu is now the same on Recent, Starred, Shared with me, tag views, the Home cards and the Recently-opened tray as in a folder - rows carry their own permission level, so Rename, Move, Delete and Share no longer vanish there (or leak in from the last folder). Every admin table pins its actions column to the right edge and scrolls sideways instead of squeezing it off screen. The admin Shares page copies the link the server builds from the configured public URL, not the browser's address. And presigned URLs are off by default on S3 storages: downloads and share links stream through filex, so a LAN-only MinIO no longer turns share downloads into dead links - set disable_presign: false to get the redirect back.

Upgrade notes: no migrations. S3 storages saved without disable_presign now stream downloads. GET /api/admin/shares rows gain url; recent/starred/tagged rows gain perm and read_only. The search placeholder now names the storage it searches.

## What changed

A fix release for the issue 32 follow-up and three things that were wrong on
screen.

### Changed

- **Presigned URLs are off by default on S3 storages** (`disable_presign: true`). A presigned link hands the browser the bucket's endpoint, which on
  the LAN-only MinIO most self-hosters run turned every share download into a
  dead link (#32). Uploads and the downloads behind public share links now
  stream through filex unless the operator sets `disable_presign: false` -
  worth it only when the endpoint is reachable from users' browsers and
  accepts SDK-signed URLs. ⚠ A storage saved without the key streams from now
  on; set `false` explicitly to get the redirect back.
- **The top search field says what it searches.** It searches the whole
  storage (every storage at the root), never the open folder, so the
  placeholder now names the storage - "Search in Photos storage" - and
  "Search all storages" on Home, the root and the Recent/Starred/Shared/tag
  views. The folder-scoped box is still the filter bar's "Filter in this
  folder…".

### Fixed

- **The context menu is the same everywhere.** On Recent, Starred, Shared with
  me, tag views, the Home cards and the Recently-opened tray a row offered only
  Open/Download/Copy/Star - no Rename, Move, Delete or Share - or, if a
  writable folder had been opened first, all of them plus a meaningless Paste.
  Rows in those views never carried their own permission level and the
  folder's level leaked in. Every listed row now carries `perm` and
  `read_only` from the server, the views forget the previous folder's level on
  entry, and the menu is built per row; only New folder/Upload/Paste stay out
  where there is no folder to put things in.
- **Admin tables keep their actions column in view.** Every admin table - and
  the token/key panels in the explorer - now scrolls sideways when it is wider
  than the page and pins the actions column to the right edge, the way the
  explorer's list view pins its ⋮ menu; the column no longer squeezes off
  screen on narrow windows. One shared stylesheet (`web/src/styles/table.css`)
  and `ui/Table.vue`'s `pinned` column option.
- **The admin Shares page copies the right link.** It built the link from the
  browser's own address, so an administrator signed in on localhost or through
  a proxy copied a link nobody else could open (#32). `GET /api/admin/shares`
  rows now carry the canonical `url` from the configured public origin - the
  same one the share dialog has always used - and the page prefers it.
- The S3 "Disable presigned URLs" help text, `STORAGE.md` and `SHARING.md` say
  the switch governs share downloads too; the minimal compose example warns
  that `http://localhost:5212` is only right on the machine running it.
- **The admin sidebar no longer opens over the page on a phone.** Below
  1024px the sidebar is a drawer with a backdrop, and it started open on every
  admin page, so the first tap went to the backdrop instead of the page. It now
  starts closed there, closes when a page is chosen, and comes back as the
  column when the window is widened.
- The search field said "Search all storages" on every admin folder: the
  scope check looked at the multi-storage *mode* (which the admin explorer is
  always in) rather than at whether a storage was open. It now names the open
  storage.

### Upgrade notes

- No migrations. S3 storages without `disable_presign` now stream downloads
  (see Changed). `GET /api/admin/shares` rows gain `url`; the recent/starred/
  tagged listing rows gain `perm` and `read_only`.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0422---2026-09-19)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.42.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.42.2`

## v0.42.1

<span class="filex-release-date">19 September 2026</span>

A fix release for four reports on 0.42.0. A bucket with several top-level folders is one form now, not one per folder: Storages → Add lists the folders under the root you typed and creates one storage per ticked folder with the same credentials (#31) - the bucket root itself is still never mounted. A read-only storage looks read-only to the people using it: a tag on its row, no + New menu, no New folder or Upload on it (#30). A scan of a large S3 storage is one listing instead of one request per folder, only one run per storage walks at a time, and the per-storage scan interval is on the form (#33). And when FILEX_PUBLIC_URL was never set, administrators see a banner instead of finding out from a share link to localhost (#32).

Upgrade notes: no migrations. POST /api/admin/storages/{id}/sync answers status "running" instead of starting a second scan while one is in flight; GET /api/files/capabilities gains public_url_configured; POST /api/admin/storages/discover is new.

## What changed

A fix release for four reports on 0.42.0.

### Added

- **Mount several folders at once** (#31). *Storages → Add* lists the folders
  directly under the root you typed - the bucket root included - and creates
  one storage per ticked folder with the same credentials, so a bucket with
  N top-level folders is one form, not N. The bucket root itself is still
  never mounted (`ROOT_PATH_FORBIDDEN`); this is how "the whole bucket" is
  offered instead. Behind it: `POST /api/admin/storages/discover`
  `{driver, config}` → `{ok, root_key, folders:[{name, root}]}`. Every driver
  with a root field (s3, local, sftp, ftp, webdav, smb).
- **Scan every (minutes)** on the storage form (#33). The per-storage poll
  cadence (`sync_interval_s`) existed on the row and in the API and was
  reachable from no form; empty = the server default (15 min).
- **A sign when `FILEX_PUBLIC_URL` is unset** (#32). Administrators see a
  banner in the panel until it is set: every share link, file-request link
  and mailed link is otherwise built on `http://localhost:5212`. The API says
  the same as `public_url_configured` on `GET /api/files/capabilities`. The
  report had set `FILEX_APPLICATION_URL`, a variable filex has never read;
  [CONFIGURATION.md → Public URL](./CONFIGURATION.md#public-url) now says
  so in as many words.

### Changed

- **One sync run per storage at a time** (#33). "Scan now" while a run is
  walking starts no second full walk over the same rows - it answers **202**
  with `status: "running"`, the scan asked for being the one in progress; a
  poll tick that finds the previous run still in flight is skipped and logged
  at INFO, not counted as a failure.
- **An S3 scan is one listing, not one request per folder** (#33). The S3
  driver hands the sync worker the whole tree in a single un-delimited
  `ListObjectsV2` pass (`storage.TreeWalker`), so 150,000 objects in a few
  thousand prefixes cost ~150 calls instead of a few thousand; the walk then
  reads directories out of memory. Over two million objects the worker falls
  back to the per-directory walk. Same rows, same order guarantees, measured
  against the per-directory walk.

### Fixed

- **A read-only storage looks read-only to its users** (#30). The navigation
  panel's storage row carries a *Read-only* tag and its Home card says so; on
  such a storage the panel's **+ New** menu, the toolbar's New folder / Upload
  and the write entries of the context menu are not offered. Before, only
  the admin list had an "RO" badge and every attempt ended in the server's
  403. Sharing a read-only file is still allowed - the ACL level is unchanged,
  only the write affordances go (`@brftech/filex-core` `permCanEdit` folds the
  listing's `read_only` in, so every embed gets the same).

### Upgrade notes

- No migrations. `POST /api/admin/storages/{id}/sync` answers 202 with
  `status: "running"` (instead of `"started"`) while a run is already in
  flight; nothing is started twice.
- A client that reads `GET /api/files/capabilities` sees one more boolean,
  `public_url_configured`.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0421---2026-09-19)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.42.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.42.1`

## v0.42.0

<span class="filex-release-date">16 September 2026</span>

## What changed

### Changed

- **A single click selects; a double click opens (mouse).** The default open
  gesture is now the classic desktop file-manager one - a single click selects a
  row/card, a double click opens it, and **Enter** opens the selection. This
  reverses 0.41.x's "any click opens" for a mouse; it is a per-viewer setting
  (`ExplorerConfig.openTrigger: 'single' | 'double'`, default `'double'`), and
  the desktop app exposes it under **Settings → Open files with**.
  - ⚠ **Touch is untouched.** A finger tap always opens (the mobile convention,
    and there is no hover-to-select on a touchscreen); the checkbox is still the
    one click that selects, on every device and in either mode.

### Added

- **`ExplorerConfig.openInHost`** - when set, opening a file emits `file-opened`
  and the explorer does NOT mount its own in-page preview; the host opens the
  file itself. The desktop app uses this to open **every document in its own
  window**, one per file (any type), so a future editor drops into the same
  path. Directories still navigate inline; Space quick-look still peeks in-page;
  E2E-encrypted files keep the in-page decrypted preview.
- **Desktop: frameless document + main windows with our own window controls.**
  The native OS caption is gone. On Windows/Linux the app draws its own
  minimize / maximize / close (the main window in a slim title bar, each document
  window in a reserved top bar so it never sits on the viewer's own top row -
  OnlyOffice's profile/share stays clear); on macOS the native traffic lights
  are kept (`titleBarStyle: 'hiddenInset'`, top-left) and no buttons are drawn.
  The top strip is the drag handle.
- **Window / tab titles name the open document.** A document window's title (and,
  on the web, the `/files/edit` browser tab) is the file's name rather than the
  server's Branding name; the main explorer window stays the whitelabel name, or
  `filex` when the server sets no branding. On the desktop the title is pinned in
  the main process (`page-title-updated` guard) so the admin SPA can't override
  it; on the web the router's per-route title (`lib/documentTitle`) names the
  file on the editor route.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0420---2026-09-16)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.42.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.42.0`

## v0.41.4

<span class="filex-release-date">15 September 2026</span>

A fix release that carries 0.41.3, whose release build never finished. Only the checkbox selects now, and any other click or tap on a file or folder opens it - beside the name, on the size and date cells, with a selection, with Ctrl held (#26). Grid and gallery cards gained a checkbox of their own, shown on hover and on every card once something is selected, so a phone can pick several files after a long press. From 0.41.3: on a phone one tap opens, even where the browser swallows the click, and the external services Test button says what its probe saw - an HTTP status, a timeout or the connection error - with a docservice hint for an ONLYOFFICE 502 (#17).

Upgrade notes that matter: a click beside a name, or a Ctrl/Shift click, no longer selects - tick the checkbox. POST /api/admin/external/:name/test gains a detail field. No migrations.

## What changed

### Changed

- **Only the checkbox selects; any other click or tap opens** (#26, fourth
  round). In 0.41.2 a click opened only when it landed on the name itself - the
  reporter: "need to click precisely on name, if a lil bit on the right then it
  selects file, change it so if only clicking on checkbox it selects it, any
  other click will open." That is now the rule, with a mouse and on a phone
  alike:
  - A click or tap **anywhere** on a list row, grid card or gallery tile opens
    it - the name, the icon, the empty space to its right, the size and date
    cells - with or without a selection, and with Ctrl or Shift held.
  - The **checkbox** is the one click that selects. Shift on a checkbox still
    extends the range from the last tick. A right click or a long press still
    opens the menu; the star and the ⋮ button still do their own job.
  - **Grid and gallery cards have a checkbox now.** It shows on hover, when the
    card is focused or selected, and on every card once anything is selected -
    so on a phone a long press selects the first file and the boxes are there
    for the rest. In the grid it takes the type icon's place, so the name does
    not move; in the gallery it sits in the thumbnail's corner opposite the
    star. The recent and starred cards on Home have none: a click there opens,
    and there is no selection to add to.
  - On a phone the tap opens at the moment the finger lifts wherever it lands
    on the item (0.41.3 did that for the name only), so it never depends on the
    browser delivering a click.
  - Ticking a checkbox with the mouse leaves the keyboard focus where it was,
    so tick-then-Space quick-looks the file instead of unticking it again.
  - A double-click still opens only the first item: the second click lands on
    the listing that just opened and is ignored for half a second.

  For embedders: `GridView` and `GalleryView` take a `selectable` prop (default
  on) that draws the card checkbox; the `click-row` / `click-card` payload marks
  a checkbox click with `check: true`, and `FilePane` treats every other click
  as an open.

### Fixed

- **The release build of 0.41.3 stopped on a race in the SFTP server's test
  harness.** `Addr()` read the listener while `ListenAndServe` was still
  assigning it, and an interface value read in the middle of that assignment
  can carry its type with a nil pointer - `(*TCPListener).Addr` then panicked.
  The SFTP and NFS servers (the same code) now guard the listener, and a
  `Close` that arrives before the listener is up no longer leaves it open.
  `go test -race` is clean on both packages.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0414---2026-09-15)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.41.4) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.41.4`

## v0.41.2

<span class="filex-release-date">15 September 2026</span>

A fix release for four reports on 0.41.1. A press on a file or folder name now opens it on every device - a tap on a phone, a click on a desktop - even while something is selected; the checkbox selects, and a right click or a long press opens the menu (#26). After a move, upload or delete, folder sizes follow within seconds instead of waiting for the next sync, and a move between two storages shows the bytes it has moved rather than a bar frozen at 0% (#27). The SSO button on the sign-in page takes your own label from Admin → Branding, and its default no longer names Keycloak (#28). A black or white branding accent no longer hides that button: its label and edge adapt to the accent and to the light or dark theme (#29).

Upgrade notes that matter: a single click on a name now opens it with a mouse too - select with the checkbox, a click beside the name, or Ctrl/Shift. GET /api/files/ops carries bytes_done and bytes_total while a transfer between storages runs. No migrations.

## What changed

### Changed

- **A press on a file or folder name opens it, on every device** (#26). The
  first round made a tap open on a phone only while nothing was selected; after
  a long press every tap toggled the selection, so a name could no longer be
  opened. The rule is now the one the reporter asked for, on a phone and with a
  mouse alike: a click or tap on the **name** opens the item, with or without a
  selection · the **checkbox** selects, as before · a **right click** or a
  **long press** opens the menu · Ctrl/Shift on a name still add to or extend
  the selection · a click beside the name (the size or date cells) still
  selects with a mouse. A habitual double-click on a folder name opens that
  folder only: the second click is ignored instead of opening whatever the new
  listing put under the pointer. Measured in a real browser at phone size with
  touch and at desktop size with a mouse; the double-click guard was proven by
  removing it, which opened the sub-folder under the pointer.

- **The SSO button's label is yours** (#28). *Admin → Branding → SSO button
  label* (setting `branding.sso_label`, per tenant, up to 60 characters). Empty
  keeps the default, which no longer names a provider: it reads **Sign in with
  SSO** instead of "Sign in with SSO (Keycloak)", whatever the identity provider
  is ([docs/SSO.md](./SSO.md#3-sign-in)).

### Fixed

- **A black or white accent no longer hides the sign-in button** (#29). The
  branding accent was painted as the button's fill with the theme's label colour
  and nothing else, so a black accent drew a dark label on a black button on the
  dark card, and a white accent a white label on a white button on the light
  card. The button is now designed per theme: the label is picked from the
  accent itself, and whenever the fill does not stand out from the card of the
  theme it is shown in, the button draws an edge in that theme. Measured in a
  real browser for black and white in both themes: label contrast ≥ 3:1 on the
  fill, and fill or edge ≥ 1.6:1 against the card. The public share page's
  accent button follows the same rule.

- **Folder sizes follow a move, an upload or a delete right away** (#27). They
  were only recomputed at the end of a sync pass, so on a storage that syncs
  rarely - or manually - a moved file stayed counted in its old folder and
  missing from the new one for hours. Every change now schedules a recompute of
  that storage's folder sizes (2 s after a burst, at most 15 s into a long one)
  and refreshes the open listings, whichever surface made the change - the
  explorer, WebDAV, S3, SFTP or NFS. Measured on two MinIO storages: both folders
  show their new size within seconds; with the refresh removed, even an upload's
  folder stayed at 0.

- **A running move shows how far it has got** (#27). A queued operation counts
  what you selected, so moving one large file - or one folder - was "0 of 1"
  until the end: a bar frozen at 0% that then vanished. A transfer between two
  storages now reports the bytes it has moved and the total it measured, and
  both progress surfaces (the explorer's operations center and the admin tray)
  draw that; when there is no honest percentage they show a moving indicator
  instead of 0%. `GET /api/files/ops` carries `bytes_done` / `bytes_total` while
  such an operation runs ([docs/BACKEND.md](./BACKEND.md#get-apifilesops-)).

- **A file at a storage's root no longer shows a lone "-" under its name in
  grid view** - search results and the Recent, Starred and Shared views, where
  the card names the folder a result lives in. The gallery and the list's
  Location column already left it empty; the grid printed a dash that read as a
  stray character. Found while checking this release's screenshots.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0412---2026-09-15)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.41.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.41.2`

## v0.41.1

<span class="filex-release-date">14 September 2026</span>

A fix release for three reports and the rough edges left after 0.41.0. Moving a file larger than 8 MiB onto an S3 storage served over plain http (Garage or MinIO on a container network) works again (#27). On a phone a tap now opens a file or folder and a long press selects it (#26). The password reset in Users asks the right question and shows the new password, and an account can be created without one (#25).

Upgrade notes that matter: with an OIDC admin group configured, the admin role now follows the group at every sign-in - someone removed from the group goes back to user at their next sign-in; the setup account and the last admin are never demoted. Content-Range joins the default CORS allow-list, so large cross-origin uploads from an embed work without a config change. FILEX_USAGE_* variables now seed the usage settings on first boot. Also: notifications, the audit log and the dashboard read in the panel's language, two explorers on one page keep their own clocks, and the update manifest's migrations flag is derived from the tags.

## What changed

### Changed

- **The OIDC admin mapping now holds on every sign-in, not only when the account
  is created.** With `FILEX_OIDC_ROLE_CLAIM` and `FILEX_OIDC_ADMIN_GROUP` set,
  someone added to the admin group becomes an admin at their next sign-in, and
  someone **removed** from it goes back to `user` - before this, an ex-admin in
  the identity provider kept administering filex for good. The mapping owns the
  admin role and nothing else: a `viewer` set by hand stays a viewer unless the
  group now grants admin. Two accounts are never demoted, because demoting
  either could leave nobody able to administer filex: the account filex was set
  up with (the one the recovery sign-in admits) and the last admin; each such
  sign-in logs a `WARN`. Measured against a real OIDC provider: added to the
  group → `admin` on the next sign-in, removed → `user`, the setup account kept
  `admin` ([docs/SSO.md](./SSO.md#roles--admin-access)).

- **`Content-Range` is in the default CORS allow-list.** An explorer on another
  origin uploads a file past one chunk (8 MiB) as PUTs carrying that header, and
  the default preflight refused it - every small upload worked and every large
  one failed, which reads as a size limit. If you set `cors.allowed_headers`
  yourself, keep it in the list.

### Fixed

- **Moving a file larger than 8 MiB onto an S3 storage served over plain
  `http://` failed** (#27) with `failed to compute payload hash: failed to seek body to start, request stream is not seekable` - Garage or MinIO on a
  container network, for instance. Over `http://` the S3 signer hashes the body
  and rewinds it to send it, and a move hands the writer the source storage's
  stream, which cannot rewind. It is the #16 fault one method over: part uploads
  learned to accept such a body, whole-object writes had not. A body that is too
  large to hold and cannot rewind now goes out as a multipart upload in 8 MiB
  parts: memory stays bounded by one part, every part is retryable on its own,
  and a body that ends early aborts the upload instead of publishing a truncated
  object. Reproduced and verified against a real MinIO by moving 20 MiB between
  two S3 storages.

- **On a phone, a tap selected a file or folder instead of opening it** (#26),
  in every browser: the explorer spoke the mouse's grammar - click selects,
  double-click opens - and a finger has no double-click. A tap now opens what it
  lands on; a long press selects it (and opens its menu), and while something is
  selected a tap adds to or removes from the selection. The decision is made
  from the gesture, not the screen size, so a touch laptop's trackpad keeps
  click-to-select. The long-press code the list, grid and gallery each carried a
  copy of is one composable now.

- **The password reset button in *Users* asked "Delete user …?"** (#25) - its
  dialog showed the delete confirmation, so the key icon read as a second delete
  button. Confirming it anyway was worse: the password was reset and the account
  signed out everywhere, and the new password was never shown (the server
  answers `new_password`, the page read `password`). The list and the user page
  each had their own copy of the dialog, and the copies had drifted; there is
  one now. The same page offered two fields the server ignored: **Add user**
  marked the password optional and then refused every request without one - an
  account can now be created without a password, for SSO or API-token use - and
  an *OIDC subject* field was sent and dropped (SSO matches accounts by e-mail),
  so it is gone, as is the editable e-mail on the user page, which the server
  never changed.

- **Saving from the editor into a folder the catalogue had not seen yet** filed
  the new file at the storage root, where it listed under neither folder until
  the next scan. The folder rows are created on the way.

- **Operational notifications were written in English** on every panel -
  `filex 0.42.0 available`, the replica alarms. They are phrased on the reader's
  side now, in both languages, like the file events.

- **The dashboard's *Recent activity* and the audit log printed wire names**
  (`user.update` over `- · user:12`). They read `User: updated` and `User #12`,
  in the panel's language; the raw action stays in the tooltip. A gate reads the
  Go that writes audit rows and fails on an action with no translation. The
  dashboard's rows never named who acted - the payload carried no e-mail, so
  every line began with a dash; they do now. The storage card's bare count reads
  `12 files`.

- **Every `FILEX_USAGE_*` variable was declared and never read.** They now seed
  the *Usage & cost* settings on first boot, like the antivirus family.

- **Two explorers on one page shared one clock.** Each printed dates in the
  zone of whichever explorer mounted last; each now reads its own
  `config.timeZone` and account, while the viewer's own choice still applies to
  both.

- **On the sign-in page the desktop-app card covered the sign-in form** when SSO
  was offered too (1280×800: the element at the submit button's centre was the
  card's subtitle). When the card and the form would overlap, the page shows the
  corner chip every other page uses.

- The share dialog's detail line read `… 10:00 AM. · 3 downloads`; the API key
  name field suggested the address the page happened to talk to
  (`127.0.0.1:5297`, visible in the README screenshots) and now suggests a name;
  the list view printed "Folder" twice per row while the Type column was on; the
  service worker's source map shipped the build machine's temp path, user name
  included - `scripts/check-embed.mjs` now refuses any shipped map that names an
  absolute path.

- **The update manifest's `migrations` flag is derived from the tags.**
  `scripts/gen-update-manifest.py` marks a release whose tag holds a migration
  file no earlier tag held, and lists every published release. The hand-kept
  list it replaces had gone stale: the published manifest never marked v0.31.0.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0411---2026-09-15)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.41.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.41.1`

## v0.41.0

<span class="filex-release-date">14 September 2026</span>

The explorer has one face now, on every surface. It is rebuilt around the end-user shell a contributor designed on top of filex (#14): a top bar with one search field and + New, a panel with Home, Shared with me, Recent, Starred and Trash, a filter row, a selection bar and a details panel with Activity. The admin app, the desktop app and every embed draw the same layout, and the split pane is the same pane twice. Built on it: a listing that behaves like a table, per-folder view memory kept per person on the server, date headings, an owner on every file, archive download of a selection, Move to and Copy to across storages, New document, thumbnails you can read, and a notification bell for every account.

Upgrade notes that matter: sign-in with SSO alone (FILEX_AUTH_DRIVERS=oidc) works now, and the administrator created at installation keeps a password recovery sign-in for the day the identity provider is down. A cancelled request can no longer lock a SQLite server into refusing every sign-in until a restart. A move or a restore no longer overwrites the file that holds the name. MySQL needs 8.0.17 or MariaDB 11.4, and migration 00041 rebuilds the nodes table there. Tokens are now limited to their own scopes on the admin routes - review scoped tokens minted on administrator accounts.

## What changed

> ⚠ **0.40.0 was never finished.** Its npm packages and git tag were published,
> but the container images, the binaries, the desktop builds and the GitHub
> Release were not - so the app-store manifests that pinned `v0.40.0` pointed
> at an image that does not exist. This release is the first complete one after
> 0.39.1, it carries everything listed under 0.40.0 below except the **Drive**
> theme (removed here - see *Changed*), and it moves every pin to itself.

### Added

- **A new face for the whole product.** The explorer was rebuilt around the
  end-user shell [@alfatm](https://github.com/alfatm) designed on top of filex
  and put up for review in #14 - measured screen by screen and adopted as
  filex's own look rather than offered as a theme. One layout for the operator
  and the end user alike, in the admin app, the desktop app and every embed:
  a full-width top bar with the product mark, one search field, a **+ New**
  menu, a 192px navigation panel with **Home · Shared with me · Recent ·
  Starred · Trash**, the storages and the connection guides, a breadcrumb row
  with the view switcher, a **Type · People · Modified · Size** filter row with
  a sort control, a selection bar that replaces the filter row while anything is
  ticked, and an info panel split into **Details** and **Activity**. The palette,
  metrics, type scale and control heights are `--fe-*` tokens, so a theme or a
  host page restyles all of it without forking a stylesheet. The tab strip, the
  split pane, the gallery view, the palettes and the keyboard editor - filex's
  own additions - are kept.

- **Home is a view inside the explorer**, not a page beside it: your storages,
  what you opened last and what you starred, under the same panel and header as
  the files. It is where everybody lands, administrators included; an operator
  who prefers the dashboard picks it under **User settings → Preferences →
  Start page**.

- **User settings**, one dialog behind the avatar: profile and photo, language,
  time zone (a search field with the offset and local time on each row), start
  page, light/dark and palette, density, per-folder view memory, notification
  switches, password and two-factor. Language and theme moved here from the
  header, so no preference has two controls.

- **A listing that behaves like a table.** Resize a column, hide one, drag one
  to a new place; the table scrolls sideways when the columns outgrow the pane,
  with the actions column pinned right, instead of dropping a column. Name is an
  ordinary column you can narrow. **The grid and the list obey the same sort** -
  it used to be private state inside the list, so switching views reordered the
  rows under you.

- **Per-folder view memory** - optional, from user settings. The view mode and
  sort of each folder you set up, stored **per person on the server**
  (`GET/PUT /api/files/manager/view-prefs`, migration `00039`), so it follows you
  to another machine and never leaks to anyone else looking at the same folder.
  Capped and least-recently-used. An embed turns it off with
  `rememberFolderView: false`.

- **Who owns a file.** Every node records its owner and its last writer
  (migration `00038`); the list has an **Owner** column and the filter row a
  **People** filter; quota counts against the owner. A storage scan no longer
  attributes a whole bucket to whoever pressed *Scan now*. Search hits carry the
  storage name and the owner too, which lifts the old single-storage limit on
  content search in the advanced search dialog.

- **Download a selection as one archive.** Pick several files and folders and
  Download streams a ZIP built on the fly (`POST /api/files/archive/download`
  mints a single-use ticket, `GET /z/<token>` streams it): nothing is written
  into your storage, nothing is buffered in the tab, and a 700 MB archive costs
  the server under a megabyte. Every member is re-checked against the caller's
  own permissions on the server.

- **Move to / Copy to**, with a folder chooser that spans every storage, lists a
  read-only folder as read-only, and refuses a destination you cannot write to -
  and the server refuses it regardless of what the dialog offered.

- **New document** under **+ New**: a Word, Excel, PowerPoint or OpenDocument
  file, or any text or code format - name it, choose where it goes, and it opens
  in the editor that handles it. The Office templates are minimal valid
  documents compiled into the binary (verified by LibreOffice and by
  OnlyOffice's own converter), so this works on the slim image; a type this
  deployment could not then open is not offered, and the dialog says why.

- **Thumbnails you can read.** A PDF shows its first page, top-anchored so the
  title is in the card; a video its first frame that is not black; an Office
  document its rendered first page; a text, code or CSV file fills the card with
  its own content. A server missing ffmpeg, ghostscript or LibreOffice now says
  so in its log at boot instead of quietly drawing coloured rectangles.

- **Date headings in every view.** A listing sorted by Modified groups itself
  under **Today · Yesterday · This Week · This Month · *September 2026*** - in
  the list, the grid and the gallery. The ladder lives in one module
  (`packages/core/src/lib/dateGroups.ts`) and all three views read it.
  - ⚠ A heading is drawn **only when it is true**. Any other sort key draws
    none, and neither does a search's ranked answer.
  - ⚠ "This Week" is the six days before yesterday, not a calendar week.
  - In the grid the date headings **replace** "Files" rather than stacking on
    it; "Folders" stays as one run at the top.
  - The boundary between today and yesterday is midnight in the **viewer's**
    chosen time zone, not the browser's.

- **Tags you can follow.** A tag chip in the details panel opens the tag view:
  everything carrying that tag, folders as well as files, across storages, with
  the ordinary filter row and sort on top.

- **A notification bell in the top bar**, for every account. Non-admins were
  raised browser notifications but had no way to open the list, mark one read
  or follow one to what it was about.

- **The desktop-app offer is a chip in the corner** of the app, not a card over
  the file listing, with a permanent home under User settings. "Do not show this
  again" is remembered against the account, not the browser.

- **Browser notifications**, and a notification opens the thing it is about.

- **The split pane is one pane component rendered twice**, so the right-hand
  pane has the same breadcrumb, filter row, sort, view switcher and selection
  bar as the left - it used to be a separate, thinner implementation.

- **Time zones resolve the same way everywhere.** One ordered list decides the
  zone every date is printed in: **the viewer's own pick → the host page's
  `config.timeZone` → the account behind the token → the device.** The account
  tier applies only to a person's token; an `app` token shared by many visitors
  never imposes one account's zone on all of them. An embed gains a **Time zone**
  row in its `⋯` menu, stored in the browser, because it has no settings dialog;
  the web app and an embed use the same picker and the same resolver, so the two
  can no longer disagree. New: `config.timeZone`, and a `time-zone` attribute on
  `<filex-explorer>`.

- **Operator custom CSS.** A stylesheet pasted under *Settings* is served with
  the branding payload and applied last on every browser surface, the sign-in
  page included, so an installation can override the `--fe-*` tokens without
  forking anything. It is capped at 64 KB, stored as one global row (in
  multi-tenant mode only the supertenant may set it), and injected as the text
  of a single `<style>` element, never parsed as HTML. See
  [docs/INTEGRATION.md](./INTEGRATION.md#operator-custom-css).

- **Home tells everyone how full a storage is**, not only an administrator:
  `GET /api/files/quota/storages` answers the same figure the admin storage list
  carries, for the storages the caller may see and nothing about the others.

- **Recovery sign-in for SSO-only installations.** With no `local` driver
  enabled, the administrator filex created at installation can still sign in
  with its password - and no other account can - so an identity provider that
  is down, a client secret that expired or a broken realm no longer locks out
  the one person who can fix it. The login page offers it behind an
  *Administrator recovery sign-in* link; two-factor still applies and every
  such sign-in is logged at WARN. On by default, `FILEX_AUTH_RECOVERY_LOGIN=false`
  turns it off. Installations from before this release get the account worked
  out once at startup: the oldest administrator that has a local password. See
  [docs/SSO.md](./SSO.md#the-identity-provider-is-down-and-nobody-can-sign-in).

- **Brand config for embeds**: `config.brand` (`name`, `markUrl`). A host
  cannot fill any slot in `<filex-explorer>` - Vue projects light DOM only
  through a shadow root and the element deliberately has none - so this is how
  an embed puts its mark in the corner.

- **A duplicate-code gate** (`scripts/dup-scan.mjs`, run by the web test suite):
  near-duplicate fragments, the same concept implemented outside its one home,
  and listing surfaces that build their own chrome. The rule and how to answer it
  are in `docs/CONTRIBUTING.md`.

### Changed

- ⚠⚠ **`uiProfile: 'drive'` is removed.** It shipped as a third profile in
  0.32.0 and became an alias of `'simple'` during this cycle; there are now two
  profiles, `'standard'` and `'simple'`, and no alias of either.

  **If you pass `'drive'`, pass `'simple'` instead.** An unrecognised value -
  a typo, or this retired name - resolves to `'standard'` (the documented
  default) and logs one console line naming it. That direction is deliberate:
  mapping the retired name onto `'simple'` would be the alias again under
  another name, and it would also mean a plain typo silently REDUCED somebody's
  UI, which looks like features going missing and points at nothing. The
  argument is written out in `packages/core/src/lib/uiProfile.ts`.

- ⚠ **The Drive theme added in 0.40.0 is removed.** Its palette became the
  product's stock palette, so the theme had nothing left to change.

- **The product colour is blue** (`#2f6ceb` light, `#5b8cff` dark) - the mark,
  the favicon and PWA icon, the admin panel, the desktop app, the public share
  page and the project site all moved off indigo together.

- **Byte sizes are decimal everywhere** (1 KB = 1000 B). The explorer used 1024
  and the admin panel 1000, so the same file read `1.43 MB` in one and `1.5 MB`
  in the other; a quota typed as 10 GB read back as 9.31 GB in the side panel.
  Turkish gets its own decimal separator.

- **The sign-in page follows the operating system's light/dark setting and the
  browser's language**; both are chosen in user settings once you are in.

- **`/admin/profile` opens user settings.** The profile page is gone - every
  field it had lives in the user settings dialog, which a non-admin can open
  too. The address keeps working, because the startup banner and
  `<data>/.first-run.txt` on existing installs still point a new operator at it.

- **"Copy node id" left the right-click menu** and the selection bar; the id is
  in the details panel, beside Path and ETag, one click to copy.

- **`@brftech/filex-react` needs no stylesheet import** - the look is injected
  by the bundle. A bundler build needs the optional viewer packages
  externalized; see `docs/INTEGRATION.md`.

- ⚠ **MySQL needs 8.0.17 or newer, MariaDB 11.4 or newer.** Migration `00041`
  compares file names byte for byte with `utf8mb4_0900_bin`, which MySQL added
  in 8.0.17, and it rebuilds the `nodes` table - on a large catalogue that takes
  as long as an `ALTER TABLE` of that table takes on your server. The previous
  documentation promised MariaDB 10.5.2; MariaDB 10.x never got past migration
  `00001`. Measured versions are listed in
  [docs/DATABASES.md](./DATABASES.md#supported-versions).

**This release has more to it than fits on one page.** The rest of the
entry - and every earlier release - is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0410---2026-09-14).

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.41.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.41.0`

## v0.39.1

<span class="filex-release-date">12 September 2026</span>

The quick-look key legend is a small pill again. Pressing Space over a file opens the preview with a legend at the bottom edge; in the web UI it was drawn as a giant rounded shape across the whole window, on top of the file being previewed. The hint carries the explorer's root class so it can read the theme variables, and the admin UI sized the embedded explorer with a rule that reached every descendant carrying that class - a host selector outranks the package's own, so the pill inherited the window height. The desktop app has no such wrapper, which is why the same build looked right there.

The same audit found the legend lying for a second reason. Shortcuts are remappable, and three surfaces spelled a key out by hand: this legend, the drive shell's search chip and two steps of the onboarding tour. The quick-look overlay also compared against the default key, so remapping it gave three different answers to one question - the new key opened the peek, the old one still closed it, and the pill named the old one. Every hint reads the binding now, and shortcutHint() is exported for embedders who draw their own.

## What changed

### Changed

- **Every key hint now reads the key that is actually bound.** Shortcuts are
  remappable, and several hints spelled a key out by hand: the quick-look
  legend, the drive shell's search chip, and two steps of the onboarding tour.
  Remap the command palette and the chip on the search field - the one control
  whose whole job is to teach that key - kept naming `Ctrl+K`.

  Worse than a stale label, the quick-look overlay also *compared* against the
  default key. Remapping quick-look onto `Q` gave you three different answers
  to one question: `Q` opened the peek, `Space` still closed it, and the pill
  said "Space". The overlay and the search field now ask the registry, so the
  key that is named, the key that opens and the key that closes are the same
  key.

  `shortcutHint(action)` and `eventMatchesShortcut(event, action)` are exported
  for embedders who render their own hints ([docs/API.md](./API.md#naming-a-key-on-screen)).
  Two gates keep it honest every release: one remaps an action and measures the
  surface that names it, the other fails the build on a key typed into a
  template or a locale string.

### Fixed

- **The quick-look key legend no longer fills the window** (#22). Pressing
  Space over a file opens the preview with a small pill at the bottom edge
  reading "Space close · ↑↓ previous/next · Enter open". In the web UI that
  pill was drawn as a giant rounded shape across the whole viewport, on top of
  the file being previewed.

  Nothing was wrong with the component. The hint carries the explorer's root
  class, which is how it reaches the theme variables, and the admin app sized
  the embedded explorer with a rule that reached *every* descendant carrying
  that class - a host selector, so it outranked the package's own. The pill
  inherited the full viewport height. The desktop app wraps the explorer in
  nothing of the sort, which is why the same build looked right there and
  wrong in the browser.

  The hint is now teleported under `<body>`, out of reach of any host
  container, the package rule that sizes it no longer depends on source order
  to win, and the admin app's own rule targets the explorer root alone instead
  of anything below it. `e2e/tests/101-quicklook-hint.spec.ts` measures the
  rendered pill in a real browser, because a cascade bug renders the same DOM
  either way and no unit test can see it.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0391---2026-09-12)

- **Documentation** - &lt;https://docs.filex.sh>
- **Report a bug** - &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** - &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** - &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.39.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.39.1`

## Earlier releases

The 111 releases before v0.39.1, in brief. Full notes are on GitHub.

| Version | Date | What changed |
|---|---|---|
| [v0.39.0](https://github.com/BRF-Tech/filex/releases/tag/v0.39.0) | 12 September 2026 | Two things a storage was missing. First, an address that does not move: a storage's name is the first path segment on WebDAV, SFTP, NFS and the S3-compatible API, so renaming one silently re-addressed it and every mount written… |
| [v0.38.2](https://github.com/BRF-Tech/filex/releases/tag/v0.38.2) | 12 September 2026 | Editing a storage now takes effect on the running process. Creating one started its syncer and deleting one stopped it, but editing one did neither: the row was written and the save reported as successful while the syncer and… |
| [v0.38.1](https://github.com/BRF-Tech/filex/releases/tag/v0.38.1) | 11 September 2026 | Renaming a folder no longer puts its contents in the trash. The rename moved one row and left every file and subfolder pointing at the old path, so the next sync tombstoned them and then collided with itself once per file; a… |
| [v0.38.0](https://github.com/BRF-Tech/filex/releases/tag/v0.38.0) | 11 September 2026 | The release where PostgreSQL and MySQL started actually working. Both were listed as supported and neither had ever been run against a test: `binary` is a reserved word on both engines, one MySQL migration had never been written,… |
| [v0.37.0](https://github.com/BRF-Tech/filex/releases/tag/v0.37.0) | 7 September 2026 | A release about checks that were narrower than they looked. The External Services Test button probed only from the filex server, so a container-internal address answered it happily while the browser that has to load the editor… |
| [v0.36.0](https://github.com/BRF-Tech/filex/releases/tag/v0.36.0) | 7 September 2026 | The release that came out of looking at filex the way a stranger arriving from a link would. |
| [v0.35.0](https://github.com/BRF-Tech/filex/releases/tag/v0.35.0) | 7 September 2026 | A security release. On a multi-tenant install the tenant boundary was enforced on the routes that list rows and assumed on the routes that name one: an administrator of any tenant, pointed at another tenant's ids, reached fifteen… |
| [v0.34.2](https://github.com/BRF-Tech/filex/releases/tag/v0.34.2) | 6 September 2026 | Two things that only ever bit installs nobody had configured by hand. filex guesses http://localhost:5212 as its own address when FILEX_PUBLIC_URL is unset, and the realtime ticket handed that guess to the browser as fact -- so… |
| [v0.34.1](https://github.com/BRF-Tech/filex/releases/tag/v0.34.1) | 6 September 2026 | A patch for two things v0.34.0 shipped over. A user who had chosen Turkish saw an English admin panel on any second device: the language stored on the account was written by Profile and never read, so it only ever worked in the… |
| [v0.34.0](https://github.com/BRF-Tech/filex/releases/tag/v0.34.0) | 6 September 2026 | The release where writes stopped being silent. Half of filex's write surfaces - WebDAV, SFTP, FTPS, NFS, the S3 gateway, the AI/MCP API, the archive extractor, the async copy worker and the built-in editor - put a file on the… |
| [v0.33.0](https://github.com/BRF-Tech/filex/releases/tag/v0.33.0) | 5 September 2026 | A release of things that were quietly wrong. A large upload to an S3 endpoint served over plain HTTP died inside the client and the storage sync then moved the file to the trash - reported from the outside, and worth knowing why… |
| [v0.32.0](https://github.com/BRF-Tech/filex/releases/tag/v0.32.0) | 5 September 2026 | The explorer gained a third shape. `uiProfile: 'drive'` lays it out the way someone arriving from Google Drive expects - one **+ New** menu, a single search field across the header with the command palette behind it, Type /… |
| [v0.31.0](https://github.com/BRF-Tech/filex/releases/tag/v0.31.0) | 5 September 2026 | Forgetting the password on an encrypted folder no longer means losing the files. Creating one now shows a recovery key once - filex never stores it, and it opens the folder without the password. |
| [v0.30.1](https://github.com/BRF-Tech/filex/releases/tag/v0.30.1) | 4 September 2026 | A one-line patch. Opening Recent, Starred, Shared or Trash put the raw internal name of the view in the tab - `.shared` instead of Shared. |
| [v0.30.0](https://github.com/BRF-Tech/filex/releases/tag/v0.30.0) | 4 September 2026 | The explorer grew a collapsible navigation panel: Recent, Starred, Shared with me, tags and Trash on the left, with Upload as the primary action. |
| [v0.29.0](https://github.com/BRF-Tech/filex/releases/tag/v0.29.0) | 4 September 2026 | Open an Office document that lives on your own computer in the editor your filex server already runs: the desktop app registers for the usual extensions, so a machine with no Word or Excel installed can still edit one. |
| [v0.28.0](https://github.com/BRF-Tech/filex/releases/tag/v0.28.0) | 3 September 2026 | If you run filex against LDAP or Active Directory, this is the release where that actually works. |
| [v0.27.6](https://github.com/BRF-Tech/filex/releases/tag/v0.27.6) | 1 September 2026 | them with.** `CONTRIBUTING.md` had said `git tag -s` for months while no |
| [v0.27.5](https://github.com/BRF-Tech/filex/releases/tag/v0.27.5) | 1 September 2026 | budget was widened to six attempts a release ago, and a test proves a 503 is |
| [v0.27.4](https://github.com/BRF-Tech/filex/releases/tag/v0.27.4) | 29 August 2026 | image tag empty, which the chart resolves to `.Chart.appVersion` - so |
| [v0.27.3](https://github.com/BRF-Tech/filex/releases/tag/v0.27.3) | 29 August 2026 | finds where a stand-in landed was started *after* `webContents.startDrag()` - |
| [v0.27.2](https://github.com/BRF-Tech/filex/releases/tag/v0.27.2) | 29 August 2026 | reading it.** `Content-Disposition` carried the filename raw, so a name like |
| [v0.27.1](https://github.com/BRF-Tech/filex/releases/tag/v0.27.1) | 29 August 2026 | stand-in by NAME across the local drives, so any file that happened to appear |
| [v0.27.0](https://github.com/BRF-Tech/filex/releases/tag/v0.27.0) | 28 August 2026 | the next now does what it says: the queue carries a destination storage of its |
| [v0.26.1](https://github.com/BRF-Tech/filex/releases/tag/v0.26.1) | 27 August 2026 | endpoint answered bare codes - `{"error":"ticket_expired"}`, |
| [v0.26.0](https://github.com/BRF-Tech/filex/releases/tag/v0.26.0) | 27 August 2026 | agent-facing write surface carried its bytes inside the call - `/api/ai/upload`'s |
| [v0.25.3](https://github.com/BRF-Tech/filex/releases/tag/v0.25.3) | 25 August 2026 | `/d/{token}` drop link came out with an empty `<title>`, an empty heading, an |
| [v0.25.2](https://github.com/BRF-Tech/filex/releases/tag/v0.25.2) | 23 August 2026 | A patch for whoever reads the error tracker. Events forwarded from filex's log used to arrive as a bare message - "thumb generate failed" eleven times with no file and no error to act on. |
| [v0.25.1](https://github.com/BRF-Tech/filex/releases/tag/v0.25.1) | 23 August 2026 | A same-day fix for FTPS: the public host name is looked up when the listener starts, and a single DNS timeout in the container's first seconds used to leave FTPS off for good while everything else reported healthy. |
| [v0.25.0](https://github.com/BRF-Tech/filex/releases/tag/v0.25.0) | 23 August 2026 | Share links now have a ceiling on how long they live, and the admin holds it: a new Protection setting (default seven days) caps every new link and file request - a link created without an expiry gets one, a longer request is… |
| [v0.24.1](https://github.com/BRF-Tech/filex/releases/tag/v0.24.1) | 22 August 2026 | A follow-up to 0.24.0's resumable first run: it now covers uploads too. The engine writes its sync history while it works - every 50 transfers or 15 seconds - instead of only at the very end, so a run interrupted at file 9,000 of… |
| [v0.24.0](https://github.com/BRF-Tech/filex/releases/tag/v0.24.0) | 22 August 2026 | A night of syncing a real 10,000-file tree, and what it taught the engine. Transfers and server listings now run several at a time, so a tree of small files is no longer priced at one round-trip each - the tree that crawled at… |
| [v0.23.0](https://github.com/BRF-Tech/filex/releases/tag/v0.23.0) | 20 August 2026 | The rest of “keep on this computer”, the same day it shipped. Every row now says where it lives - ✓ on this computer, ◐ holding kept items below, ⟳ syncing right now, ☁ online-only - and a strip along the bottom of the window… |
| [v0.22.0](https://github.com/BRF-Tech/filex/releases/tag/v0.22.0) | 20 August 2026 | Folders you also want on the computer are now one right-click away. “Keep on this computer” mirrors a server folder - or a whole storage - under a single filex folder chosen once per account, while everything else stays… |
| [v0.21.6](https://github.com/BRF-Tech/filex/releases/tag/v0.21.6) | 19 August 2026 | Two decisions about noise and blast radius. A demo instance no longer accepts any new storage backend - 0.21.4 stopped it reaching the server's own filesystem, and now the remote drivers go too, because "attach your own bucket"… |
| [v0.21.5](https://github.com/BRF-Tech/filex/releases/tag/v0.21.5) | 19 August 2026 | Plugins no longer outlive filex on Windows. If filex was stopped without a chance to clean up - a crash, a hard kill, a service restart - every plugin it had launched kept running; and since a running plugin holds its own .exe… |
| [v0.21.4](https://github.com/BRF-Tech/filex/releases/tag/v0.21.4) | 19 August 2026 | The other half of the demo hardening in 0.21.3. A demo instance publishes an admin login, and the `local` storage driver means "a path on this host" - so on a demo it was possible to add a storage rooted at /data, /etc or /proc/1… |
| [v0.21.3](https://github.com/BRF-Tech/filex/releases/tag/v0.21.3) | 19 August 2026 | A security default, found by measuring the project's own public demo. A demo instance publishes an admin login - that is what a demo is for - and the plugin API is admin-only, so on a demo "admin-only" means anybody, and… |
| [v0.21.2](https://github.com/BRF-Tech/filex/releases/tag/v0.21.2) | 19 August 2026 | A plugin now has to PROVE what it claims before filex will use it. Every capability a plugin declares is probed - at install against the plugin's own throwaway area, and again when you save a storage on it - and one that fails… |
| [v0.21.1](https://github.com/BRF-Tech/filex/releases/tag/v0.21.1) | 19 August 2026 | Pasting a file into the top level of a storage failed - on every driver, not just the new plugins: the explorer asks for the storage root, and the operations queue read that as "no destination given". |
| [v0.21.0](https://github.com/BRF-Tech/filex/releases/tag/v0.21.0) | 19 August 2026 | filex can now be taught a storage it has never heard of. A plugin is a separate program - in any language - that filex launches (or connects to) and speaks a small HTTP/JSON protocol to; once it is running, its driver appears in… |
| [v0.20.3](https://github.com/BRF-Tech/filex/releases/tag/v0.20.3) | 18 August 2026 | The desktop app now ships for macOS (Apple Silicon) - unsigned but ad-hoc sealed, so first launch is the ordinary Open Anyway step rather than a refusal; auto-update on the Mac waits for a signed build. |
| [v0.20.2](https://github.com/BRF-Tech/filex/releases/tag/v0.20.2) | 17 August 2026 | Packaging only - the server is identical to 0.20.1. It exists because the |
| [v0.20.1](https://github.com/BRF-Tech/filex/releases/tag/v0.20.1) | 17 August 2026 | setting is documented as "the address to advertise for passive connections", |
| [v0.20.0](https://github.com/BRF-Tech/filex/releases/tag/v0.20.0) | 17 August 2026 | filex is now reachable without a browser. It could already use S3, SFTP, FTP and WebDAV as storages; now those clients can point at filex itself - as an S3 endpoint, an SFTP server, an FTPS server and an NFSv3 export, alongside… |
| [v0.19.0](https://github.com/BRF-Tech/filex/releases/tag/v0.19.0) | 14 August 2026 | The desktop app gets a language setting - System, English or Türkçe - where before it followed the operating system and offered nothing to choose. |
| [v0.18.2](https://github.com/BRF-Tech/filex/releases/tag/v0.18.2) | 14 August 2026 | The Windows app installs per-user now, and that is what makes the quiet update in 0.18.1 real: an app under C:Program Files needs administrator rights to replace its own files, so every background update ended in a UAC prompt -… |
| [v0.18.1](https://github.com/BRF-Tech/filex/releases/tag/v0.18.1) | 14 August 2026 | The desktop app updates itself the way it always should have. Downloading was quiet and quitting installed silently, but the tray entry and the Settings button ran the installer with its wizard - so the one visible path through… |
| [v0.18.0](https://github.com/BRF-Tech/filex/releases/tag/v0.18.0) | 14 August 2026 | Share links keep their word, and you get a face. A link capped at three downloads could hand out four: the cap was checked against a counter that was only bumped after the bytes had left, so any request that started while an… |
| [v0.17.1](https://github.com/BRF-Tech/filex/releases/tag/v0.17.1) | 12 August 2026 | A packaging fix, and the first release whose tag matches what ships. The desktop package could be built without the updater inside it: in a pnpm workspace the dependency is a symlink pointing outside the app directory and… |
| [v0.17.0](https://github.com/BRF-Tech/filex/releases/tag/v0.17.0) | 12 August 2026 | The desktop app keeps itself up to date. It checks a static feed on filex.sh (not GitHub - that mirror is private and the provider would need a token inside the app), downloads quietly, and installs when you quit, because an… |
| [v0.16.3](https://github.com/BRF-Tech/filex/releases/tag/v0.16.3) | 12 August 2026 | The tab strip, fixed properly. It was permanent in the desktop app and came and went on the web, because 0.16.0 gave the two surfaces different defaults - this package exists so they are one product, so the default is now the… |
| [v0.16.2](https://github.com/BRF-Tech/filex/releases/tag/v0.16.2) | 12 August 2026 | Two things a share link got wrong. It changed language when you entered its PIN - the gate was English, the page behind it Turkish, and the screen in between managed both at once. |
| [v0.16.1](https://github.com/BRF-Tech/filex/releases/tag/v0.16.1) | 12 August 2026 | Follow-up to 0.16.0: a shared folder's gallery tiles are now rendered when the link is created rather than when the first visitor arrives, so the first open is fast too - which is the open that matters, since whoever creates a… |
| [v0.16.0](https://github.com/BRF-Tech/filex/releases/tag/v0.16.0) | 12 August 2026 | Two things a shared folder was doing the slow way. Its gallery tiles were the original photos - the page asked for a thumbnail and the server streamed the whole file - so a folder of a few dozen photos shipped tens of megabytes… |
| [v0.15.1](https://github.com/BRF-Tech/filex/releases/tag/v0.15.1) | 12 August 2026 | The desktop app's account rail had its two identities the wrong way round. Each row of that rail is a server - a tenant - so it now carries that server's own Branding logo, which is a better label than initials taken from an… |
| [v0.15.0](https://github.com/BRF-Tech/filex/releases/tag/v0.15.0) | 12 August 2026 | Mostly the desktop app, and one thing that was writing to your operating system. "Start when I sign in" registered whichever executable happened to be running the app - which, for anyone who had ever run it from source, was a… |
| [v0.14.0](https://github.com/BRF-Tech/filex/releases/tag/v0.14.0) | 10 August 2026 | The desktop app can reach its own server again. Opening any office document showed "Config fetch 401", starred files and recently-opened were silently empty, and "Open in new tab" did nothing at all - one cause under the first… |
| [v0.13.4](https://github.com/BRF-Tech/filex/releases/tag/v0.13.4) | 10 August 2026 | A fix for a disk that fills up on its own. Uploads larger than 32 MiB are buffered to a temporary file, and those files were never removed - every request answered normally while the disk quietly drained (29 GB in two hours on a… |
| [v0.13.3](https://github.com/BRF-Tech/filex/releases/tag/v0.13.3) | 7 August 2026 | The share button now appears in the desktop app. The explorer has always had one, but it was gated on the Web Share API, which Electron does not ship - so rather than build a second share UI, the app puts a native handler behind… |
| [v0.13.2](https://github.com/BRF-Tech/filex/releases/tag/v0.13.2) | 7 August 2026 | Sixteen components were rendering as raw, unstyled HTML in every embedded surface - the share dialog, the convert dialog, the presence bar and nine file viewers. |
| [v0.13.1](https://github.com/BRF-Tech/filex/releases/tag/v0.13.1) | 7 August 2026 | The explorer's onboarding tour sat on top of the desktop app's Settings panel. The tour attaches to `<body>`, so hiding the explorer left it exactly where it was. |
| [v0.13.0](https://github.com/BRF-Tech/filex/releases/tag/v0.13.0) | 7 August 2026 | The desktop app is a file manager, not an admin console. Signing in used to land you on the server's dashboard because the shell embedded the whole admin SPA; it now shows the file explorer, an account rail down the left, and a… |
| [v0.12.0](https://github.com/BRF-Tech/filex/releases/tag/v0.12.0) | 7 August 2026 | Selective folder sync. A folder on your computer and a folder on a filex server are kept in step in both directions, in the background. |
| [v0.11.0](https://github.com/BRF-Tech/filex/releases/tag/v0.11.0) | 7 August 2026 | The desktop app, for Windows and Linux. It runs the same web UI this repo already ships, and sign-in happens in your browser - the app opens the server's own login page and waits, so installs behind an identity provider (OIDC,… |
| [v0.10.2](https://github.com/BRF-Tech/filex/releases/tag/v0.10.2) | 6 August 2026 | Completes the guard added in 0.10.1: a sweep found four more places that could still write a file onto a folder - archive extraction and creation, the OnlyOffice save-back, and version restore - plus replication, where a… |
| [v0.10.0](https://github.com/BRF-Tech/filex/releases/tag/v0.10.0) | 6 August 2026 | Two explorer changes reported by a deployment whose users mount WebDAV from macOS. Dot-prefixed files can now be shown or hidden and are hidden by default (a Mac leaves `.DS_Store` and `._name` litter in every folder it opens),… |
| [v0.9.0](https://github.com/BRF-Tech/filex/releases/tag/v0.9.0) | 6 August 2026 | Closes the ten items a multi-tenant deployment filed against v0.8.0. The important one is a security fix: a tenant admin could reach every other tenant's storages over WebDAV, because `/dav` does its own Basic authentication and… |
| [v0.8.0](https://github.com/BRF-Tech/filex/releases/tag/v0.8.0) | 29 July 2026 | filex now knows which releases exist, and can install them. What it does is decided by which part of the version moved: a patch applies itself when the policy allows, a minor is announced and applied with one click, a major is… |
| [v0.7.6](https://github.com/BRF-Tech/filex/releases/tag/v0.7.6) | 29 July 2026 | Denials on the AI/MCP surface answer `403` instead of `500`. A `5xx` reads as "server glitch, retry", so agents and HTTP clients were retrying requests that could never succeed while the real cause - a path outside the token's… |
| [v0.7.5](https://github.com/BRF-Tech/filex/releases/tag/v0.7.5) | 19 July 2026 | An internal refactor with no behaviour change: the storage-scoped path hash had been copy-pasted across nine call sites, so the same file could map to different rows if any copy drifted. |
| [v0.7.4](https://github.com/BRF-Tech/filex/releases/tag/v0.7.4) | 18 July 2026 | Two explorer fixes: the trash bin now appears in the secondary pane of split view (the two panes were offset by a row), and tall listings scroll inside their pane instead of scrolling the whole page. |
| [v0.7.3](https://github.com/BRF-Tech/filex/releases/tag/v0.7.3) | 18 July 2026 | Split view's right-click menu now matches the main panel exactly - it was a shorter, separate list missing rename, delete, share, convert and tags. |
| [v0.7.2](https://github.com/BRF-Tech/filex/releases/tag/v0.7.2) | 18 July 2026 | Split-view polish: the main panel's breadcrumb no longer spans the full width, and right-clicking a row in the secondary pane opens a real menu instead of only selecting the row. |
| [v0.7.1](https://github.com/BRF-Tech/filex/releases/tag/v0.7.1) | 18 July 2026 | A round of layout and accessibility fixes for embedders. The explorer no longer overflows its host by 2px (the outer scrollbar that produced in embeds is gone), the toolbar folds overflowing actions into a `⋯` menu instead of… |
| [v0.7.0](https://github.com/BRF-Tech/filex/releases/tag/v0.7.0) | 17 July 2026 | Three additions. **Branding** - a settings-driven identity (name, logo, accent, footer) for the public share, PIN, file-drop and folder-browse pages plus the admin login, with per-tenant overrides. |
| [v0.6.0](https://github.com/BRF-Tech/filex/releases/tag/v0.6.0) | 17 July 2026 | Tabs and split view. Open several locations as tabs, split the active tab into two panes that navigate independently, and drag files between them to move (same storage) or copy (across storages). |
| [v0.5.0](https://github.com/BRF-Tech/filex/releases/tag/v0.5.0) | 17 July 2026 | A large interface release: eight built-in themes with independent light and dark variants, fully rebindable keyboard shortcuts, Quick Look (peek the selected file with Space), an operations centre that collects uploads and… |
| [v0.4.2](https://github.com/BRF-Tech/filex/releases/tag/v0.4.2) | 17 July 2026 | Cleanup release. Moving a folder to trash no longer wedges storage sync (a trashed folder's leftovers could block sync from ever re-creating those names), `versions.keep_n` above 20 works instead of being silently capped, and… |
| [v0.4.1](https://github.com/BRF-Tech/filex/releases/tag/v0.4.1) | 17 July 2026 | Packaging and documentation. Ready-to-submit app-store manifests for Umbrel, CasaOS, Runtipi, Unraid and Portainer, a refreshed Helm chart, and this documentation site. |
| [v0.4.0](https://github.com/BRF-Tech/filex/releases/tag/v0.4.0) | 17 July 2026 | An inspector panel (press `i`) with metadata, version history, effective permissions and share links for the selected item. |
| [v0.3.0](https://github.com/BRF-Tech/filex/releases/tag/v0.3.0) | 17 July 2026 | Connectivity release. A WebDAV server so you can mount filex as a network drive from Windows, Finder, rclone or davfs2 with full RBAC enforcement; a `filex client` CLI against any remote filex; and multiple webhook targets with… |
| [v0.2.0](https://github.com/BRF-Tech/filex/releases/tag/v0.2.0) | 17 July 2026 | Content search. filex now indexes what is inside your files - text, source code, CSV/JSON/YAML, PDF text layers and Office documents - extracted asynchronously into an embedded index, with highlighted snippets in the results. |
| [v0.1.84](https://github.com/BRF-Tech/filex/releases/tag/v0.1.84) | 17 July 2026 | A design pass over the explorer: a command palette (`Ctrl/Cmd+K`), a shortcuts sheet sourced from one registry so it cannot drift, sortable and date-grouped list columns, a density toggle, an undo snackbar for rename/move/trash,… |
| [v0.1.83](https://github.com/BRF-Tech/filex/releases/tag/v0.1.83) | 16 July 2026 | screens, mobile touch): three compounding layout issues fixed. The |
| [v0.1.82](https://github.com/BRF-Tech/filex/releases/tag/v0.1.82) | 10 July 2026 | read-only and no-preview labels) and the presence-bar toggle tooltip now |
| [v0.1.81](https://github.com/BRF-Tech/filex/releases/tag/v0.1.81) | 9 July 2026 | list of usernames (first = default); the audit log, shares (`created_via`) |
| [v0.1.80](https://github.com/BRF-Tech/filex/releases/tag/v0.1.80) | 9 July 2026 | as API calls (bearer/proxy) and rendered from blob object-URLs, so embedded |
| [v0.1.79](https://github.com/BRF-Tech/filex/releases/tag/v0.1.79) | 9 July 2026 | stamp `X-Filex-Presence-Name` (RFC 2047) + `X-Filex-Presence-Key`, spoofing |
| [v0.1.78](https://github.com/BRF-Tech/filex/releases/tag/v0.1.78) | 8 July 2026 | Ws - Resolve embedded confined subscribes to the absolute room + per-client frame paths. |
| [v0.1.77](https://github.com/BRF-Tech/filex/releases/tag/v0.1.77) | 8 July 2026 | Ws - Authorize ticketed subscribes as the ticket's user (RBAC) |
| [v0.1.76](https://github.com/BRF-Tech/filex/releases/tag/v0.1.76) | 8 July 2026 | Ws - Allow ticket-only (embedded/cross-origin) WebSocket connections. |
| [v0.1.75](https://github.com/BRF-Tech/filex/releases/tag/v0.1.75) | 8 July 2026 | Realtime - Embed WS live-collab in the core (ticket auth + polling fallback) |
| [v0.1.74](https://github.com/BRF-Tech/filex/releases/tag/v0.1.74) | 8 July 2026 | updates in the core component (native UI *and* embedded contexts via |
| [v0.1.73](https://github.com/BRF-Tech/filex/releases/tag/v0.1.73) | 7 July 2026 | Share - Add GET /api/files/share list endpoint so the modal lists existing links. |
| [v0.1.72](https://github.com/BRF-Tech/filex/releases/tag/v0.1.72) | 7 July 2026 | Share - Native "Paylaş" (Web Share API) button under the mail row. |
| [v0.1.71](https://github.com/BRF-Tech/filex/releases/tag/v0.1.71) | 7 July 2026 | Explorer - 'folder not found' for dead deep links - phantom prefixes 404, denied dirs render as not-found. |
| [v0.1.70](https://github.com/BRF-Tech/filex/releases/tag/v0.1.70) | 7 July 2026 | Web - Don't double the explorer hash in the login redirect. |
| [v0.1.69](https://github.com/BRF-Tech/filex/releases/tag/v0.1.69) | 6 July 2026 | pasting a link opens that folder, login preserves the hash. |
| [v0.1.68](https://github.com/BRF-Tech/filex/releases/tag/v0.1.68) | 6 July 2026 | redirects.** Measured live (nginx `$upstream_http_set_cookie` vs the |
| [v0.1.67](https://github.com/BRF-Tech/filex/releases/tag/v0.1.67) | 5 July 2026 | Previously the session cookie never set `Secure`, and the OIDC state cookie |
| [v0.1.66](https://github.com/BRF-Tech/filex/releases/tag/v0.1.66) | 5 July 2026 | successful (or failed) IdP round-trip the callback bounced the user to |
| [v0.1.65](https://github.com/BRF-Tech/filex/releases/tag/v0.1.65) | 5 July 2026 | were already cross-built for arm64 (goreleaser), but the container images |
| [v0.1.64](https://github.com/BRF-Tech/filex/releases/tag/v0.1.64) | 5 July 2026 | `adminIDFiltersIn` type and two files had drifted from gofmt, which kept |
| [v0.1.63](https://github.com/BRF-Tech/filex/releases/tag/v0.1.63) | 5 July 2026 | flag on and `oidc` among the auth drivers, the login page starts the OIDC |
| [v0.1.62](https://github.com/BRF-Tech/filex/releases/tag/v0.1.62) | 5 July 2026 | A real zero renders as `0 B` instead of `-`, and rows without a backend |
| [v0.1.61](https://github.com/BRF-Tech/filex/releases/tag/v0.1.61) | 5 July 2026 | tenants: a *provider* = an auth realm (OIDC or local) bound to a host and |
| [v0.1.60](https://github.com/BRF-Tech/filex/releases/tag/v0.1.60) | 5 July 2026 | "last activity" date added in 0.1.59 is derived from descendant file mtimes; |
| [v0.1.59](https://github.com/BRF-Tech/filex/releases/tag/v0.1.59) | 5 July 2026 | added in 0.1.58, each folder's row reports a "last activity" date - the |
| [v0.1.58](https://github.com/BRF-Tech/filex/releases/tag/v0.1.58) | 4 July 2026 | size (the sum of its descendant files). Sizes are computed once at the end of |
| [v0.1.57](https://github.com/BRF-Tech/filex/releases/tag/v0.1.57) | 4 July 2026 | Storage - Connect any external storage from env (sftp/webdav/ftp/s3) + fix JSON port. |

---

<small>Last refreshed 2026-10-03 from 131 published releases.</small>
