# Contributing to filex

Thanks for considering a contribution. This is a small, opinionated codebase
— before opening a sizeable PR please file an issue describing what you're
about to do.

- [Development setup](#development-setup)
- [Workflow](#workflow)
- [Branches](#branches)
- [Commit messages](#commit-messages)
- [Testing](#testing)
- [Code style](#code-style)
- [Docs](#docs)
- [Screenshots](#screenshots)
- [Release process](#release-process)

---

## Development setup

Requirements:
- Go 1.25+ (`backend/go.mod` declares 1.25.0; the images build on golang:1.25)
- Node.js 20+
- pnpm 9+
- (optional) Docker, ffmpeg, ghostscript, libreoffice for thumbnail dev

```bash
git clone https://github.com/brf-tech/filex.git
cd filemanager

pnpm install            # all workspace packages
pnpm run dev            # parallel: package watch + admin Vite dev server

# In another shell — Go backend
# once, on a fresh clone: the binary embeds these two directories, and
# `go build` refuses a //go:embed pattern that matches nothing
mkdir -p backend/embed/admin backend/embed/web
touch backend/embed/admin/.placeholder backend/embed/web/.placeholder

cd backend
FILEX_LISTEN=127.0.0.1:5212 FILEX_DATA_DIR=./.dev-data go run ./cmd/filex serve
```

⚠ `serve` takes its settings from the environment (or `--config`), not from
flags: `--listen` and `--data-dir` are refused with `unknown flag`.

The admin SPA is served by Vite at <http://localhost:5173> in dev mode and
proxies `/api/*` to the Go server at `:5212`. For the embedded build (what
ships in the binary), use `pnpm run build:all`.

### Running with hot-reload

```bash
# Terminal 1 — Go (recompiles on save with air)
go install github.com/air-verse/air@latest
cd backend && air

# Terminal 2 — admin SPA + packages
pnpm run dev
```

---

## Workflow

1. **Fork** + create a feature branch off `main`.
2. **Code** + write tests.
3. **Lint locally**: `pnpm run lint` and `cd backend && go vet ./... && staticcheck ./...`.
4. **Test locally**: `pnpm run test` and `cd backend && go test -race ./...`.
5. **Open MR** against `main`. CI runs lint + test + build.
6. **Address review** + squash if asked.
7. **Merge** — maintainer squashes; commit message becomes a CHANGELOG line.

---

## Branches

- `main` — protected, always green.
- `feat/<short-name>`, `fix/<short-name>`, `chore/<short-name>` — feature branches.
- `release/v0.X.Y` — short-lived branch only used to cut a release.

We don't run a `develop` branch. Trunk-based development with feature flags
when something needs to land partially.

---

## Commit messages

[Conventional Commits](https://www.conventionalcommits.org/). The CHANGELOG
generator depends on the prefixes:

```
<type>(<scope>): <subject>

<body, wrapped at 100>

<optional footer; e.g. BREAKING CHANGE: ...>
```

Types we use:

| Type     | Meaning                                            |
|----------|----------------------------------------------------|
| `feat`   | new user-visible feature                           |
| `fix`    | bug fix                                            |
| `perf`   | performance change with no behaviour change        |
| `refactor`| internal restructuring, no behaviour change        |
| `docs`   | documentation only                                 |
| `test`   | tests only                                         |
| `chore`  | tooling, deps, CI; no functional change            |
| `ci`     | CI config only                                     |
| `build`  | build pipeline / Dockerfiles                       |

Scopes (optional but encouraged): `backend`, `core`, `webcomponent`, `react`,
`web`, `docker`, `ci`, `docs`, `storage:s3`, `auth:oidc`, etc.

Examples:
```
feat(storage:s3): add use_path_style for MinIO compatibility
fix(auth:oidc): refresh token before expiry instead of after
docs(api): document /api/admin/external/:name/test
build(docker): pin alpine to 3.20 to dodge ghostscript regression
```

Breaking changes:
```
feat(api)!: rename @file-explorer-share to @share-created

BREAKING CHANGE: the Vue event name changed. See docs/API.md.
```

---

## Testing

### Go

```bash
cd backend
go test -race ./...
go test -race -cover ./...      # with coverage
go test -run TestStorageS3 -v ./internal/storage/s3
```

For driver tests, we have integration suites under `internal/storage/*/integration_test.go`
guarded by `//go:build integration`. Run with:

```bash
go test -tags=integration ./internal/storage/s3 \
  -test-bucket="$TEST_BUCKET" -test-region=us-east-1
```

### Web

```bash
pnpm run test                        # all workspaces
pnpm --filter='@brftech/filex-core' test
```

Vitest with happy-dom.

### Browser suites

There are two, and both run against a throwaway instance this repo starts for
them — never against a live host, never with a secret:

```bash
bash scripts/build-wasm-fixture.sh   # once: the app-plugin fixture 95-app-plugins installs (skips without it)
node e2e/run.mjs local      # Playwright — e2e/tests/*.spec.ts
node e2e/run.mjs cypress    # Cypress   — web/cypress/e2e/*.cy.ts
```

Add `--build` on the first run (it builds the packages, the admin UI, the embed
assets and the Go binary); afterwards a binary in `bin/` is enough.

**Which one do I add a test to?**

| | Playwright (`e2e/`) | Cypress (`web/cypress/`) |
|---|---|---|
| Shape | one user journey per spec, end to end | many small cases per surface |
| Best at | flows that cross screens — upload → trash → restore, share → open with a PIN, pair a desktop app | HTTP contracts, admin screens, envelope shapes, "every route answers" sweeps |
| Reaches | the UI a person sees | the UI **and** the API underneath it, in the same file |
| Gates | the release (`docs/CONTRIBUTING.md` → Release process) | every push and PR (`.github/workflows/ci.yml`) |

Rule of thumb: **if you can describe it as a story ("a user does X, then Y, and
sees Z"), it is Playwright. If you can describe it as a rule ("this endpoint
answers 503 when the integration is off"), it is Cypress.** A regression in a
shared package usually deserves one of each — the contract in Cypress, the
journey in Playwright.

`e2e/README.md` and `web/cypress/README.md` carry the traps for each.

### What needs tests

- **Always**: every new HTTP endpoint, every new storage driver method,
  every config knob.
- **Encouraged**: new UI components (Vitest `mount`).
- **Optional but appreciated**: an end-to-end scenario when the flow spans many
  components — see the table above for which suite it belongs in.

---

## Code style

### Go

- `gofmt -s` (CI checks `gofmt -l .` is empty).
- `go vet ./...` clean.
- `staticcheck ./...` clean.
- Public symbols documented (`// FuncName does X`).
- Error wrapping with `fmt.Errorf("...: %w", err)`.
- No global state outside of `cmd/filex`.

### TypeScript / Vue

- ESLint with `eslint-plugin-vue` recommended config.
- Strict TypeScript: `noImplicitAny`, `strictNullChecks`.
- Prefer composables for reusable logic; SFC for components.
- No default exports (named only) — easier IDE refactor.

### Do not write the same logic twice

Anything repeated is added in one place and managed from one place. This is not
a style preference — it is the rule this codebase has broken most often, and
every time it was found by a person looking at a screen: a 464-line second
listing pane, one `mime_type === 'inode/storage'` test in three view
components, the brand mark hand-typed into five files (one still painted the
pre-rebrand indigo), two byte formatters that round differently on the same
screen. The second copy always gets written because the first is inconvenient
to reach, nothing notices, and the two drift.

**The gate:** `web/tests/quality/duplication.test.ts`, running
`scripts/dup-scan.mjs`. Run it yourself with `node scripts/dup-scan.mjs` — it
prints a ranked report and takes about two seconds. It checks three things:

| | what it catches | threshold |
|---|---|---|
| **fragments — verbatim** | a block copy-pasted with its names intact | ≥ 100 contiguous tokens (~15 lines) |
| **fragments — renamed** | a block re-typed, or copied and adapted, so no name matches | ≥ 140 tokens of identical structure, ≥ 14 distinct keywords/operators |
| **concepts** | a second implementation of something that has one home — byte formatting, date formatting, the storage-row test, the logo | any occurrence outside its home |
| **listing surfaces** | a component that renders the view components but hand-rolls the breadcrumb / filter row / view switch | any missing shared piece |

Scope is `packages/core/src`, `web/src`, `backend/internal`, `desktop/src`.
Tests, locale catalogues, generated files and build output are out of scope.

**When it fires: extract the shared thing and call it from both places.** That
is the answer nearly every time, and it is usually smaller than it looks.
Adding a second copy *and* an allowlist entry is not an answer — it is the
failure this gate exists to stop, written down.

**To declare a legitimate twin,** add an entry to the right register in the
test file, with a reason **about this code**. "Known issue" is rejected by the
gate; so is anything under 60 characters.

- `LEGITIMATE_TWINS` — the duplication is the design and will not be removed
  (the Postgres and SQLite drivers are two dialects of one interface). No
  ceiling: these grow on purpose.
- `KNOWN_DUPLICATION` — debt. Real, pre-existing, owed. Carries a `maxTokens`
  ceiling, so the area cannot quietly grow a *bigger* copy than it already has.
- `CONCEPT_EXEMPTIONS` / `COMPOSITION_DEBT` — the same, per concept and per
  surface.

**The registers are kept honest by going stale loudly.** An entry that no
longer matches anything *fails*. So when you fix a duplicate the build turns
red and tells you to delete its entry — that is intended, and the fix is one
line. An allowlist nobody prunes becomes the place the next duplicate hides.

Two limits worth knowing, so you do not mistake silence for absence: the
fragment passes only see duplication that is still *shaped* like the original
(a re-implementation with a different structure — which is what the old
`SecondaryPane` was, before one `FilePane` replaced both halves of the split —
is caught by the listing-surface rule instead, not by tokens), and Vue
`<template>` and `<style>` blocks are not scanned at all.

### UI rules

#### One table — the explorer's — and nothing else draws one

**filex has exactly one table: `DataTable`
(`packages/core/src/components/DataTable.vue`), which is the explorer's own
list view with the files taken out of it.** The explorer's listing renders
through it, and so does every other table in the product — every admin page,
the connection panels, My shares, the notifications list, an archive's or a
spreadsheet's preview, and an app's `list` node. **If something is tabular, it
is a `DataTable`.** No `<table>`, no table roles, no copy of the `fe-list`
markup, no second table component — anywhere in `web/src` or
`packages/core/src`.

The owner, 2026-09-21: *"Artık explore tablomuz bizim her yerde kullanacağımız
tablo yapısıdır; bir yere tablo gerekiyorsa bu tabloyu koymak zorundayız. Bunu
kural olarak yazalım, çok önemli bir kural."* ("From now on the explorer's
table is the table we use everywhere; wherever a table is needed, this is the
table that goes there. Write it down as a rule — a very important one.")

**Why it is a rule and not a preference.** The round before it built
`ui/Table.vue`: *one* admin table, with the explorer's frozen edges and its
Actions menu — an **imitation**. It had exactly the parts somebody remembered
to copy. Resizing a column, sorting, the column menu, reordering and
remembering the arrangement all live in the explorer's code, and none of them
reached the admin panel. Nothing failed; the owner opened the Users page and
found he could not widen a column or sort it (*"Admin tabloları hâlâ explore
tablolarıyla AYNI KODDA DEĞİL … tablo sütunları düzenlenebilir değil, büyütme
küçültme yok, sıralama yok"*). The same code gets everything the explorer can
do, and keeps getting whatever is added to it next. A look-alike never does.

What every table gets, whoever draws it:

- **resizable columns** — drag the edge, arrow keys on the focused handle,
  double-click for the shipped width;
- **sorting** — click a header, click again to reverse. ⚠ Over one page of a
  server-paged list the headers **close and say why** instead of re-ordering
  25 rows of 300 and calling it sorted; a caller whose server can sort passes a
  controlled `sort` and handles `@sort`;
- **the column menu** — the header's ⋮ or a right-click on the header: show,
  hide, step left/right, reset; plus dragging a header to move its column;
- **remembered** — per table, on the person's account (`table-id`, stored in
  the per-person view document under `t`). The explorer's listing is
  remembered per folder instead;
- **the frozen lead and ONE Actions control** — the column that says which row
  this is stays on the left, the row's verbs are one labelled control on the
  right (`:row-actions`), and the table scrolls sideways rather than dropping a
  column. ⚠ Either is frozen only while it leaves room to scroll: a sticky
  cell carries an opaque ground, so in a pane it cannot spare (an app's list
  in the details panel is ~265px) it would cover the very columns it was
  frozen to keep company. Below that nothing is pinned, the row scrolls as
  one piece, and the lead falls to its own minimum instead of taking the
  whole pane — still without hiding anything.

How to use it:

```vue
<DataTable
  table-id="admin.widgets"
  :columns="[{ id: 'name', label: t('…'), sortable: true, width: 220 },
             { id: 'size', label: t('…'), sortable: true, align: 'right',
               format: (r) => formatBytes(r.size), sortValue: (r) => r.size }]"
  :rows="rows"
  row-key="id"
  :row-actions="(r) => [{ key: 'delete', label: t('…'), danger: true }]"
  @row-action="(key, r) => …"
>
  <template #cell-name="{ row }"><div>{{ row.name }}<span class="tbl-sub">{{ row.path }}</span></div></template>
</DataTable>
```

- Every `DataTable` has a **unique `table-id`** (`admin.<page>[.<table>]`,
  `conn.<panel>`, `app.<plugin>.<node>`). The one exception is a table whose
  columns are whatever the data brings (the CSV preview): it binds
  `:table-id="undefined"` and says why beside it.
- A cell slot is a flex row: **wrap a cell that stacks two lines in one
  `<div>`**, or the lines sit side by side. A `mt-1` on a second root node of
  the slot is a margin on a flex ITEM — it does not start a line, it pushes
  the box down ON TOP of the one beside it (v0.43.0 QA: the Apps table's
  Label cell drew the "Language pack" badge over the label and the coverage
  line over the badge, at every width). `.tbl-sub` as a direct child is the
  one shape the stylesheet handles on its own.
- A list the server pages: pass `:page`, `:page-size` and `:total` (or
  `:pages`). ⚠ Pass `:total` even when there is **no pager** and the endpoint
  answers "the first N of M" — that is what tells the table the rows on screen
  are not the whole list.
- Language and light/dark reach every table from the host once (`TABLE_ENV` —
  `web/src/lib/tableEnv.ts`, and the explorer provides its own); a page does not
  pass them. A unit test that mounts a page on its own provides `TABLE_ENV` if
  it asserts translated table text.

**The gate:** `web/tests/ui/tablePinnedActions.test.ts` scans both trees and
fails on a raw `<table>`, on table elements, table roles or the `fe-list` table
markup outside `DataTable.vue`, on a `DataTable` without a `table-id` or with a
duplicate one, on a `RowActions` drawn anywhere but inside the table, and on a
`#cell-*` slot whose second root node carries a top or bottom margin (the
overlap above). There
are **no exemptions** — the earlier version of this rule let the file previews
keep tables of their own "because they render foreign content", and an
exemption list is where the next second table hides.

#### A service that is not there: disabled with a reason, or not offered

For an action that needs an optional external service — ONLYOFFICE, draw.io,
the converter, outgoing mail — that is not configured (or not answering):
**an administrator sees the action greyed, with a sentence that says what is
missing and where to set it up; everybody else is not offered it at all.**
Nobody is ever shown a raw HTTP status or a JSON body. The owner, after
`Config fetch 503: {"error":"onlyoffice not configured"}` reached a person who
had clicked Open on a `.docx`: *"disabled with a reason for administrators,
hidden for everybody else."* Use `gateOnService()` (`packages/core/src/lib/
serviceGate.ts`); "is this person an administrator who could fix it" is the
server's answer (`capabilities.caller_admin`), never a role guessed in the
browser.

#### Words: one term per concept

A thing on screen has **one name**, in every language filex ships, on every
surface. The release-candidate sweep of v0.43.0 (2026-09-21) found the API key
called "API anahtarı", "API jetonu" and "API token" on three neighbouring
screens, a share's PIN called "PIN" in the dialog that made it and "Kod" on the
page that asks for it, and "Giriş" meaning both *Home* and *sign in*. A person
reading two names assumes two things.

| Concept | English | Türkçe | Not these |
|---|---|---|---|
| The key a person creates for a device, a mount or an AI agent | API key | API anahtarı | API token, token, API jetonu, jeton |
| The secret a share link can require | PIN | PIN | code, kod |
| A place filex keeps files (an admin adds it under Storages) | storage | depo | disk, drive, sürücü; "bucket / kova" only for the S3 bucket behind or in front of one |
| What an API key is allowed to do (the checkboxes on the key, the column that lists them afterwards) | permission | izin | scope, kapsam, yetki, "can do". A **provider's** `scope` parameter (`authProviders.fields.scopes`) is OIDC's word and stays |
| A GitHub (or other code) repository | repository | repo | depo — that is a storage |
| Deleted files, until they expire | Trash | Çöp kutusu | Çöp Kutusu, çöp |
| What something is called | name | ad (display name: görünen ad) | isim |
| What a person signs in with | password | parola | şifre (şifreleme is *encryption* and stays) |
| The first screen of the file manager | Home | Ana sayfa | Giriş |
| Starting a session | sign in / sign-in | oturum aç / oturum açma | log in, login, giriş yap |
| Ending it | sign out | oturumu kapat | log out, çıkış yap |
| The short name a person signs in with | username | kullanıcı adı | login name, giriş adı |
| An electronic mail address, or a message | email | e-posta | e-mail, mail |
| filex reading a storage to bring its catalogue up to date (Sync runs, Sync now, Last sync) | sync | senkron (noun), senkronize et (verb) | eşitleme, senkronizasyon |
| The desktop app keeping a copy of folders on a computer, both ways (folder sync, "Syncing…") — and any other tool that does the same | folder sync, sync | klasör eşitleme, eşitle | klasör senkronu, senkronizasyon |
| A replica write mode: wait for the replica, or don't | synchronous / asynchronous | eşzamanlı / eşzamansız | Sync / Async, senkron / asenkron |
| When a file last changed (column, filter, sort, details) | Modified | Değiştirilme | Tarih, Değiştirildi |
| Who a file belongs to (column and filter) | Owner | Sahibi | People, Kişiler |
| An address that opens a share | link | bağlantı | link (in Turkish) |
| Narrowing a list | filter | filtre | süzgeç |

**Spelling is American English.** color, license, favorite, center, gray,
behavior, organize, analyze, catalog, defense, customize — never colour,
licence, favourite, centre, grey, behaviour, organise, catalogue. (v0.43.0:
"Colour palette", "your own colours", "the colour palette" and macFUSE's
"licence" survived the sweep beside "Accent color" and "License: {license}" on
the next screen.) The gate below fails a British spelling.

**Case.** Sentence case for every label, button, menu item, title and column,
in both languages: "Delete permanently", "Kalıcı olarak sil", "Keyboard
shortcuts". A proper name keeps its own case (filex, WebDAV, Finder, a menu
name quoted from another program's screen). `web/tests/i18n/labelCase.test.ts`
fails a label written twice in two cases and a short label in Title Case.

**Turkish is written in the "siz" form.** A sentence that addresses the reader
says "Tekrar deneyin", "hesabınızla", "görebilirsiniz" — never "Tekrar dene",
"hesabınla", "görebilirsin", never "sen". A command — a button, a menu item, a
placeholder — is the bare verb, as every Turkish interface writes it: "Kaydet",
"Yeni sekmede aç", "Ara…". That is not the "sen" form, and it is not changed.

The machine-checkable part of this table is `web/tests/i18n/vocabulary.test.ts`:
it reads every catalogue — explorer, admin, and the server's `server.*` text —
and fails on a word from the right-hand column. A technical name that is
somebody else's (the OIDC *token* endpoint, an HTTP header, a webhook target's
*Bearer token*) is listed there by key, with the reason. Add a row here and a
pattern there together.

#### A person is named one way

Wherever filex shows a person — the Owner column, the details panel, the share
dialog, the account menu, an admin table, a notification — it prints **their
display name, else their username, else their email address**. In the browser
that is `personName()` (`packages/core/src/lib/personName.ts`); on the server
it is `model.PersonLabel` (`backend/internal/model/user.go`), which the Owner
column's name lookup and every row that names a person use. An email address
is shown as the name only when an account has nothing else; where it helps (an
admin table, a tooltip) it is the second line, never the first.

#### Dates and numbers: the explorer's format, everywhere

A date a person reads is the explorer's: "Sep 21, 2026, 2:50 PM",
"21 Eyl 2026, 14:50" — `formatWhen()` from `@brftech/filex-core`, in the
viewer's language and the viewer's chosen time zone. The admin panel's
`formatDate()` (`web/src/lib/format.ts`) and every share line call it; nothing
builds its own `Intl.DateTimeFormat`. A byte count is `formatByteSize()`, with
the catalogue's unit words (`unit.*`) and the viewer's number format, so
Turkish reads "1,96 KB" and French "1,96 Ko". ISO 8601 is for machines only: a
log line, an export, an API document — never a label.

### General

- **Line endings are LF, and `.gitattributes` enforces it** — you do not need to
  set `core.autocrlf`, and setting it will not override the repository. Every
  text file is stored and checked out LF on every platform; `*.bat`, `*.cmd` and
  `*.ps1` are the deliberate CRLF exceptions and `e2e/fixtures/**` is never
  converted in either direction, because those bytes are what the file-type
  suite is testing. This is not cosmetic: a `.sh` file checked out with CRLF
  fails on Linux and under WSL with `/usr/bin/env: 'bash\r': No such file or
  directory`, which is what the repository shipped until 2026-09-05.
- ASCII characters by default. Add comments in English even if the codebase
  is bilingual.
- No `console.log` left over — use `import.meta.env.DEV` guards in dev-only
  code paths.

---

## Docs

Doc updates live alongside code changes in the same PR. The pattern:

- New endpoint → update [BACKEND.md](BACKEND.md).
- New component prop / event → update [API.md](API.md).
- New config field → update [CONFIGURATION.md](CONFIGURATION.md).
- New driver → update [ARCHITECTURE.md](ARCHITECTURE.md) + driver-specific
  section in [CONFIGURATION.md](CONFIGURATION.md). ⚠ Both places: the
  ARCHITECTURE list sat at four drivers for two releases after `smb` and `ftp`
  shipped, and contradicted a paragraph on its own page.
- New external service → all of the above.
- New **webhook event** → [NOTIFICATIONS.md](NOTIFICATIONS.md), and add the
  constant to the backend catalogue — `backend/internal/notify/catalog_test.go`
  refuses an inline `EventType("x.y")` and
  `web/tests/webhooks/eventCatalog.test.ts` fails if the UI's mirror or either
  translation is missing.
- New **realtime frame or socket behaviour** → [REALTIME.md](REALTIME.md), which
  is the contract embedders code against.
- A setting that **moves from the environment into the `settings` table** →
  both [CONFIGURATION.md](CONFIGURATION.md) (the variable becomes a *seed*, and
  the Gotchas list is where somebody looks after their change did nothing) and
  the page that owns the feature.
- Behaviour change → CHANGELOG entry under `## [Unreleased]`.

---

## Screenshots

**Every UI feature ships with its screenshots, in the same change.** A screen
that changed under a picture that did not is wrong information in the README,
not missing information.

1. **Take them with `pnpm shots`.** A screen with no picture yet gets one in the
   `e2e/shots/` script that owns it, or in a new script there — the command
   runs every file in that directory that imports `@playwright/test`, so a new
   script cannot be left out.
2. **Look at the contact sheet it prints** (`e2e/.artifacts/shots/contact-sheet.html`):
   every picture of the run with its path and the script that took it. Each one
   must be in English, show what its name says, and have nothing across it — no
   onboarding tour, no install banner, no dialog caught mid-fade.
3. **Commit the pictures** in `docs/screenshots/<release>/` with the code.

What the command does, so a red run can be read: builds
`build:packages → build:web → sync:embed → go build` (Go native, or through WSL
on Windows); boots the binary and **refuses to shoot unless it serves `web/dist`
byte for byte** (`scripts/check-embed.mjs`; source maps only have to exist, their
bytes are not reproducible — a binary built without `sync:embed` carries an older
UI and still passes every API check); runs every script, the
E2E-escrow instance and its throwaway key pair included; syncs `site/assets`;
fails on a picture in the release folder that no script wrote; and ends every
process it started. `--only <script>` while you work on one screen,
`--no-build` to shoot a binary you already built (still verified). It needs
Playwright's Chromium once: `pnpm --dir e2e exec playwright install chromium`.

CI runs the same command on every tag and on demand (GitLab `shots`, GitHub
*Screenshots*) and uploads the pictures with the sheet, so a script that no
longer fits the product turns a job red instead of a release night.

**The app scenes are taken locally.** `apps.mjs` and `signing.mjs` photograph
the two apps filex ships alongside itself, and their builds are not in this
repository: they come from sibling checkouts of
[filex-sign](https://github.com/BRF-Tech/filex-sign) and
[filex-convert](https://github.com/BRF-Tech/filex-convert) (`../filex-sign/dist`,
`../filex-convert`), or from `FILEX_SIGN_APP_DIR` / `FILEX_CONVERT_APP_DIR`.
Without one, `pnpm shots` stops before it builds anything and says which. In CI
(`CI` is set) those two scenes are **left out** instead — named in the log, the
verdict and the contact sheet, and their folders spared the leftover check —
because at a tag there may be no app release to fetch yet, and the converter
scene needs Docker, which the GitLab runner does not have. `--with-apps` puts
them back; `--without-apps` leaves them out anywhere.

**The converter picture needs the conversion engines.** Its wizard lists what
the server found and names every missing engine *Not installed on this server*
— not a picture for the README. When the host lacks one (a Windows workstation
lacks all of them), `apps.mjs` runs **this tree's build inside the full image**
(`ghcr.io/brf-tech/filex:full`, which carries ffmpeg, ImageMagick, Ghostscript,
poppler, LibreOffice and rsvg) with Docker — nothing is installed on the host,
the image is pulled once, and the container's UI is checked byte for byte
against `web/dist` like the host binary's. `SHOTS_ENGINES=host|container`
forces one side. The scene refuses to take the picture while anything on it
says an engine is missing. To rehearse a scene without replacing its pictures:
`SHOTS_DRY_RUN=1 node e2e/shots/apps.mjs` walks to every picture and writes
none.

---

## Release process

Maintainer-only. Reproducible, automated by CI.

**Cut it with `pnpm release X.Y.Z`.** The steps below are what that command
does, in this order, and every step it can check is a gate that stops the
release when it is red — there is no option to skip one, and an option it does
not know is refused rather than ignored. It never signs, pushes or deploys: at
those steps it stops, prints the exact commands, and on `--resume` reads back
what was done — each tag's signature and target, what both remotes now hold (a
public tag naming a private commit is refused out loud), and what the servers,
the update feeds and docs.filex.sh actually serve. The two judgements no script
can make are a person's to confirm: the README, screenshot and documentation
audit (steps 1-3, `--ack audit`) and the parts of the deploy nothing can read
back (`--ack deploy`).

```bash
pnpm release 0.45.0 --plan      # every stage and gate, in order; runs nothing
pnpm release 0.45.0 --dry-run   # every gate, nothing written: the stamp goes to
                                # a scratch copy, the export to a throwaway clone
pnpm release 0.45.0             # stops at the first red gate or person's step
pnpm release 0.45.0 --resume    # carry on (exit 3 = waiting for you, 1 = a red gate)
pnpm release 0.45.0 --status    # where the recorded run got to
```

> ⚠ The gates live in `scripts/release/plan.mjs` (this repository's list,
> guarded by `web/tests/deploy/releasePlan.test.ts`) and
> `scripts/release/stages.mjs` (the order, and what every release checks).
> A new rule goes there, not only on this page: on the night of 2026-09-24/25
> four releases were re-cut, each for a step that was written down here and
> skipped anyway.

1. **Re-read `README.md` against what actually shipped since the last tag.**
   Run `git log --oneline vPREVIOUS..HEAD`, then ask of every new surface — a
   client, a feature, a docs page — whether it appears in the intro, *Why
   filex*, *Features* and *Documentation*. The README is the page most readers
   see and the one nobody remembers to touch: a feature documented only under
   `docs/` does not exist as far as a new reader is concerned. Update
   `docs/README.md` (the index) in the same pass.

   > Why this is step 1: by 2026-08-13 the desktop app, folder sync, the CLI,
   > trash & versioning, E2E folders and self-update had all shipped — six
   > minor releases' worth — and not one of them had reached the README.

2. **Retake the screenshots and look at them.** Bump `SHOTS_RELEASE` in
   `e2e/shots/release.mjs` to the release being cut — each release's pictures go
   in a new `docs/screenshots/vX.Y.Z/`, and older folders are never retaken,
   moved or deleted — point the README and docs at the new folder, then run

   ```bash
   pnpm shots
   ```

   and **open the contact sheet it prints** before committing the folder: every
   picture in English, current, nothing covering it. [Screenshots](#screenshots)
   says what the command checks on the way.

   > Why this is a numbered step: by 2026-08-14 `share-modal.png` showed a
   > share dialog with no download limit — a control that had shipped two
   > releases earlier — and `viewer-markdown.png` had Turkish buttons in it.
   > The v0.41.0 set then had to be taken three times by hand: scripts that no
   > longer fit the UI, and a binary carrying a 16-hour-old interface that
   > passed every API check. `pnpm shots` exists so neither happens again.

3. **Audit the documentation on every surface. Never skip this.** The README
   pass above is one leg of it; a feature can be finished, tested and shipped and
   still not exist for anybody who did not write it.

   ⚠⚠ **Do not work from a fixed list** — a list looks complete, and the surface
   that is not on it gets skipped. The rule is *every text that describes the
   product or explains how to use it*. Find them first:

   ```bash
   ls **/README.md docs/*.md docs/index.md
   grep -rn '"description"\|description:' package.json packages/*/package.json \
     deploy/helm/*/Chart.yaml deploy/*/*app*.yml deploy/*/docker-compose*.yml
   ```

   In this repo that is at least twelve places:

   | Surface | Why it counts |
   |---|---|
   | `README.md` | step 1 above |
   | **`site/index.html`** | the **filex.sh landing page** — the first thing anyone reads about the product, and it drifted two months and a dozen features out of date while it lived only on the static host. Deployed with `scripts/sync-site.sh`. Both live in the maintainers checkout only; the page is this project own site, not part of what you install |
   | **`web/src/views/Login.vue`** (`demo.*` in `web/src/locales/*.json`) | the **demo.filex.sh landing page** — rendered by the app when `FILEX_DEMO_MODE=true`, so it looks like code and gets audited like nothing. It went untouched from 2026-05-07 to 2026-09-05 still selling *"5 Storage drivers"*. Ships in the release image; see `docs/DEPLOY_BRF.md` §4b |
   | `docs/*.md` | the new feature has a page — **and the old pages are still true** |
   | `docs/README.md` | every `docs/*.md` is in the index |
   | **`docs/index.md`** | the docs site's **home page** — its hero line and feature cards are the first thing a visitor reads |
   | `docs-site/.vitepress/config.mts` | the new page is in the **sidebar** |
   | `packages/*/README.md` | these are the **npm pages** — an export nobody documents does not exist for anybody installing the package |
   | `desktop/README.md` | what the app actually does |
   | `deploy/*/README.md` | install instructions per target |
   | `deploy/umbrel/*/umbrel-app.yml`, `deploy/casaos/*` (`x-casaos.description`), `deploy/runtipi/*/metadata/description.md` | **app-store listings** — public product copy. Three stores, and the Runtipi one is a whole markdown page rather than one line, which is exactly why it is the one that rots |
   | `deploy/helm/*/Chart.yaml` | shown by `helm search` |
   | `package.json` descriptions | shown on npm |
   | `deploy/compose/*.yml` | new env vars and **published ports** with the traps beside them |

   > ⚠ The dangerous case is not a missing page, it is a **page that lies**.
   > On 2026-08-17 `STORAGE.md` still said *"There is no `nfs` or `smb` driver,
   > and there doesn't need to be"* — the `smb` driver had shipped in that very
   > release.

   > ⚠ A surface does not have to be a `.md` file. The demo landing page is
   > markup and translation strings, so it reads as code and slipped every
   > documentation pass for four months — while being, for anyone who clicks
   > *Try the live demo*, the **first** description of the product they meet.
   > Ask what a text *does*, not what extension it has.

   > ⚠⚠ A page missing from the sidebar is **not** unpublished. VitePress builds
   > every file under `srcDir`, so it is reachable by URL and indexable whether
   > or not anything links to it. To actually keep a page off the site, add it to
   > **`srcExclude`**. On 2026-08-17 five pages were live but unreachable from the
   > nav, and `CLOUD.md` — whose own first line says *"NOT a live service"* — was
   > being published.

   Five commands finish the step, all required:

   ```bash
   # every relative markdown link resolves to a real file
   node scripts/check-links.mjs

   # ⚠ and again on the tree that actually ships. The published repo is NOT
   # this one: scripts/export-public.sh withholds a list of files, so a link
   # to one of them resolves here and 404s there. On 2026-09-05 the public
   # README and docs/README.md both pointed at docs/MIGRATION.md, which the
   # export strips -- two dead links in the shop window, and green here every
   # time. export-public.sh now runs this itself and refuses the export, but
   # run it by hand if you are looking at a tree it did not just build.
   node scripts/check-links.mjs /path/to/filex-export

   # the site must BUILD — VitePress fails the build on a dead link
   # ⚠ a subshell: the two commands after this one are repo-root-relative,
   # and for a while this line was a bare `cd docs-site` that left them inside
   # docs-site. The YAML command below then globbed `deploy/**` from there,
   # matched nothing, and printed `yaml ok` without opening a single file.
   # This build writes NOTHING — see the note below. `git status` must be as
   # clean after it as it was before.
   (cd docs-site && npm run build)

   # …and every in-page ANCHOR must land, which the build says nothing about.
   # VitePress fails on a dead PAGE link and ignores the `#section` half
   # entirely: 98 of 366 in-page links were dead on a green build (2026-09-05).
   # It reads the ids out of the HTML the build just produced, so run it after.
   node scripts/check-doc-anchors.mjs

   # every YAML you touched still parses — breaking a store listing is silent
   python3 -c "import yaml,glob; [yaml.safe_load(open(f,encoding='utf-8')) \
     for f in glob.glob('deploy/**/*.yml', recursive=True)]; print('yaml ok')"

   # every packaged deployment target names the version you are about to
   # release -- the Helm chart AND the CasaOS/Umbrel/Runtipi manifests
   node scripts/sync-deploy-versions.mjs --check
   ```

   > ⚠⚠ **Changing the slug rule re-spells every deep link into docs.filex.sh.**
   > The site uses GitHub's heading-id rule (`docs-site/.vitepress/github-slug.mjs`),
   > adopted on 2026-09-05 because these pages are read on two surfaces and the
   > in-page links were correct *GitHub* anchors. The cost of that switch was
   > measured rather than guessed — both commits built, the emitted `id=`
   > attributes diffed page by page: **245 of 666 headings changed spelling**,
   > and **none disappeared**. Every heading holding an `&`, a `/`, a `.`, an
   > apostrophe, an em dash or a leading digit moved: `#backup-restore` →
   > `#backup--restore`, `#v0-31-0` → `#v0310`, `#config-yaml` → `#configyaml`,
   > `#_1-pick-a-wrapper` → `#1-pick-a-wrapper`. Sixty-odd of them are on pages a
   > stranger would link (INSTALLATION, CONFIGURATION, STORAGE, MCP, SSO, LDAP);
   > the rest are `BACKEND.md`'s per-endpoint reference and the generated
   > `RELEASES.md`.
   >
   > **No aliases were added, deliberately.** The old spellings existed only on
   > the site and only for the seven weeks it used VitePress's rule; every link
   > written against the GitHub rendering — the repo README, both npm package
   > READMEs, every in-page TOC — was already correct, which is why the *rule*
   > was changed instead of the 98 links; and an unmatched fragment lands the
   > reader at the top of the right page, not on a 404. 245 hand-maintained
   > `<a id>` aliases would need their own check to stay honest and would clutter
   > markdown that is also read on GitHub, where those spellings never existed.
   >
   > ⚠ A URL fragment is **never sent to the server**, so a Caddy rule, a
   > VitePress `rewrite` or a `_redirects` file cannot rescue an old anchor —
   > only a per-heading `<a id>` or client-side JS can. If the rule is ever
   > changed again, re-run the measurement (build at both commits, diff the
   > emitted `id=` attributes per page) before deciding what it costs.

   > ⚠⚠ **This gate does not refresh `RELEASES.md`, and that is deliberate.**
   > `npm run build` used to be `npm run releases && vitepress build`, so every
   > person running this mandatory step came away with two modified files —
   > `docs/RELEASES.md` and `docs-site/data/releases.json` — belonging to
   > nobody's change. On 2026-09-06 three agents hit it in one day, each
   > reverted it by hand, and one release nearly swept the churn into an
   > unrelated commit. A gate that dirties the tree it is gating is a trap.
   >
   > The build now runs `docs-site/scripts/check-releases.mjs` instead: it
   > asserts the generated page is present and lists at least one release,
   > offline, writing nothing. Refreshing is **step 10**, run on purpose after
   > the release exists. The generator is idempotent too — running
   > `npm run releases` when nothing has changed leaves both files untouched
   > rather than restamping today's date on them.

   ⚠ A relative link to a page that is in `srcExclude` is a dead link *on the
   site* even though it resolves in the repo — link those by full GitHub URL.
   That is how the "not published" list in `docs/README.md` broke the build the
   first time it was written.

4. Update `CHANGELOG.md` — move `[Unreleased]` to a dated `[vX.Y.Z]` heading.
5. **Every release updates every packaged deployment target. No exceptions.**
   (Burak's rule, 2026-08-29: *"her yeni tag'de versiyonda helm zorunlu"*.) Bump
   the `package.json` versions across all packages, then the deploy targets:
   ```bash
   pnpm -r exec npm version X.Y.Z --no-git-tag-version
   node scripts/sync-deploy-versions.mjs    # Helm chart + CasaOS + Umbrel + Runtipi
   ```
   ⚠ Run this **after** step 4, not before: the same script also derives
   Umbrel's `releaseNotes` from the `## [X.Y.Z]` section of `CHANGELOG.md`, and
   exits 2 saying so when that section does not exist yet. Those notes are
   generated rather than typed because a hand-written "what's new" carries no
   version number — a stale one describes a release the user is not getting and
   nothing about it looks wrong.
   ⚠ None of these are labels — each decides which image a real installation
   pulls. The chart's `values.yaml` ships `tag: ""` and the image helper
   resolves that to `.Chart.appVersion`; the three store manifests pin the tag
   outright and compare their `version` field to decide an update exists.

   ⚠⚠ It has gone wrong twice, the same way, because nothing failed when it
   drifted. The chart sat at `v0.4.0` for twenty-three releases (found
   2026-08-29). The fix covered only the chart — so on 2026-09-06 the three
   **store manifests were still at `v0.4.0`, twenty-nine behind**, and anyone
   installing filex from CasaOS, Umbrel or Runtipi got a build from February.
   `web/tests/deploy/deployVersions.test.ts` now fails the build if any of the
   seven pins drifts, and `--check` reports them without writing.
6. Commit: `chore(release): vX.Y.Z`.
   ⚠⚠ **Not with `git add -A`, and not before these checks.** The release
   commit is the one commit in the project that is allowed to touch
   everything, which is exactly why it must not be written blind:

   ```bash
   git status --porcelain | grep '^??' && echo "untracked files — commit them or move them to their branch"
   pnpm -s --filter ./web build      # vue-tsc + vite, the gate nothing else runs
   (cd web && npx vitest run)        # the unit suite on your clock…
   (cd web && TZ=UTC npx vitest run) # …and on CI's, which is UTC
   docker build --platform linux/amd64 -f docker/Dockerfile      -t filex:release-check .
   docker build --platform linux/amd64 -f docker/Dockerfile.slim -t filex:release-check-slim .
   node e2e/run.mjs cypress          # the suite release.yml waits for, run BEFORE the tag is public
   node e2e/run.mjs local            # Playwright — the journeys Cypress does not walk
   ```

   Measured 2026-09-24, on v0.43.0: **the npm packages and the GitHub Release
   were published, and the container images were not.** The Dockerfiles'
   frontend stage copied `packages/` and `web/`, but the build configs import
   two files from `scripts/`; `vite build` failed on every attempt, and
   nothing local had ever built an image. `web/tests/deploy/dockerFrontendInputs.test.ts`
   now fails if the build reaches a file the images do not copy, but only a
   real `docker build` proves the rest of the recipe — so both images are built
   here, before the tag. v0.43.0's CI also failed a unit test that passed on
   the UTC+3 machine it was cut on; the suite runs under `TZ=UTC` here too, so
   the clock of whoever cuts the release cannot hide one again.

   Measured 2026-09-14, on v0.41.0: the explorer had been rebuilt and every
   account now landed on Home instead of the dashboard. Nothing local had run
   either end-to-end suite during the cycle, and both still waited for
   `/admin/dashboard` after signing in — so every spec behind the login helpers
   would have gone red in the tag's own CI run, after the tag was public, and
   held back every binary, image and package. Fourteen Cypress failures were
   stale selectors, not product bugs; that is only knowable by running them.

   Measured 2026-09-12, on v0.38.1: `git add -A` swept in two work-in-progress
   files from a feature branch — an admin page with no route, no menu entry and
   no translations. `vue-tsc` refused them, the release's own test suite failed,
   and binaries, images and npm were all skipped. Nothing shipped, so the tag
   was deleted from both remotes and re-cut on the corrected commit; the version
   number survived because nothing had been published under it.

   The backend suite and every documentation gate above pass without compiling
   a single line of frontend, so the admin build is the only local check that
   would have caught it — CI catches it afterwards, when the tag is already
   public.
   ⚠⚠ **The tag is now gated on the test suite, and it did not used to be.**
   `release.yml`'s first job calls `ci.yml`, and everything that publishes —
   binaries, images, npm, the installers — waits for it. Before this, CI ran on
   the branch push and the release on the tag pushed two seconds later, in
   parallel and unaware of each other, with no required status check anywhere
   in the repository. Measured 2026-09-06: **CI had been red since v0.31.0 and
   four tags shipped over it.** The failure was real (a user who had chosen
   Turkish saw an English admin panel on any second device) and none of the
   steps above would ever have caught it — they check README, screenshots,
   links, anchors and version manifests, and never run a test.
   ⚠⚠ **The gate builds both images, and cannot be told not to.** Until
   v0.43.2 the release called `ci.yml` with `skip_docker: true` ("the release's
   own docker job builds the same image") — but `binaries`, `docker` and `npm`
   start *beside* one another once the gate passes, so when v0.43.0's images
   failed, npm and the Release were already public. The input is gone, the
   image job has no `if:`, and `web/tests/deploy/releaseGatesImages.test.ts`
   fails if either comes back or a publishing job stops waiting for the gate.
   (It reads `.github/workflows`; in a checkout without them, point
   `FILEX_WORKFLOWS_DIR` at the published ones.)

7. Tag: `git tag -s vX.Y.Z -m "vX.Y.Z"` — **signed**, and `git tag -v vX.Y.Z`
   must answer `Good signature` before you push. Releases up to and including
   v0.27.5 are plain annotated tags: the instruction said `-s` for months while
   no signing key existed, so nobody could follow it and nobody noticed. The
   maintainer key is `EFA3B126 2FD99280 0DBBB5E3 A8FEBA97 FF786513` (ed25519,
   expires 2028-08-31); its passphrase and a recovery copy live in the team
   vault, not on disk.
8. Push: `git push origin main` and then the one tag you just made
   (`git push origin refs/tags/vX.Y.Z`). Push the tag by name rather than
   `--tags`: this checkout accumulates local tags, and `--tags` publishes
   every one of them, including any you were not ready to release.

   > ⚠ Steps 6-8 happen in the checkout whose `origin` is **GitHub** — that is
   > what `release.yml` watches. Development happens on GitLab; the public tree
   > is produced by `scripts/export-public.sh`, and the signed tag is made
   > there, on the commit that is actually published.

CI does the rest (GitHub Actions `release.yml`: the `test` gate above, then
five jobs):
- `binaries` (needs `test`) — goreleaser: multi-arch binaries → the GitHub
  Release. It is what *creates* the Release, so `desktop` below depends on it.
- `docker` (needs `test`, nothing else) — a **matrix**, one native runner per
  architecture (amd64 on `ubuntu-latest`, arm64 on `ubuntu-24.04-arm`), each
  pushing by digest. ⚠ It does **not** wait for `binaries` — it builds its own
  binary and never wanted the release. arm64 used to run under QEMU behind
  `needs: binaries` and took 20-30 minutes; on a native runner the whole
  critical path is about seven.
- `docker-manifest` (needs `docker`) — joins the two digests into the tags
  people pull: `:vX.Y.Z`, `:slim-vX.Y.Z`, `:full-vX.Y.Z`, `:latest`, `:slim`,
  `:full`.
- `desktop` (needs `binaries`, **not** `docker`) — one matrix job per OS,
  attached to the Release while the images are still building. Before v0.25.0
  it waited for the docker builds it never needed — ~25 idle minutes a release.
  ⚠ The upload step globs `desktop/release/*.exe` (and `*.AppImage`, `*.deb`,
  `*.dmg`, `*.zip`, `latest*.yml`) rather than naming files, which is why the
  Windows portable `.exe` needed no workflow change — but it also means a
  target that silently stops producing an artifact shows up as a shorter
  release, not as a failure. The `What was produced` step exists to make that
  readable in the log.
  ⚠ The **filex.sh/desktop/ feed is uploaded by hand**, and a portable copy's
  *Settings → Updates* **Download** button points into it. Put
  `filex-desktop-portable-x64.exe` there with the installer, or that button
  leads to a file that is not on the server.
- `npm` (needs `test`, nothing else) — publishes `@brftech/filex-core`,
  `@brftech/filex`, `@brftech/filex-react`.

9. **Publish the two update feeds, then prove they moved.** CI attaches every
   artifact to the GitHub Release; it publishes **neither feed**, and a feed is
   the only place an installed copy ever looks. Both live on the static host and
   are deliberately excluded from `scripts/sync-site.sh` (so a website deploy
   cannot delete them) — which also means nothing refreshes them but you.

   - `filex.sh/updates/stable.json` — **the server and CLI**. Every install with
     `AUTO_UPGRADE` reads this and nothing else. Generate it, do not hand-edit
     it: `python3 scripts/gen-update-manifest.py --repo-dir <the export checkout>
     --previous <the live stable.json> --out stable.json` lists every published
     release with its digests, derives `migrations` from the tags, and carries
     over what a person decided in the live file — a kill switch, a security
     flag, a `min_version`, hand-written notes (`--no-auto vX.Y.Z` pulls a
     release out of automatic upgrades). Without `--previous` those decisions
     are silently undone. Point `--repo-dir` at the checkout the signed tags
     were made in: a release it has no tag for stops the generator rather than
     publishing a guessed `migrations: false`. Diff the result against the live
     file before you upload it.
   - `filex.sh/desktop/` — the desktop app. Upload the installers, the
     AppImage/deb, the dmg/zip, the portable `.exe`, and all three
     `latest*.yml`.

   ```bash
   curl -s https://filex.sh/updates/stable.json | head -5
   for f in latest.yml latest-mac.yml latest-linux.yml; do
     printf '%-18s %s
' "$f" "$(curl -s https://filex.sh/desktop/$f | head -1)"
   done   # every one must name the version you just tagged
   ```

   > Why this is a numbered step and not a footnote: it *was* a footnote, inside
   > a bullet describing a CI job, and it was skipped release after release.
   > Measured 2026-09-05, with v0.31.0 out: all three desktop feeds read
   > `version: 0.27.4` and `stable.json` read `v0.28.0`. Every installed desktop
   > app on every platform had been told it was up to date since v0.27.4, and
   > every server with `AUTO_UPGRADE` on saw v0.28.0 as the newest release there
   > is. The artifacts were built and attached to each Release the whole time;
   > only this step was missing, and nothing anywhere said so.
   >
   > ⚠ This is the same failure as v0.29.0's, one layer out: there, a fix
   > shipped that no existing install could see. Here, releases shipped that no
   > existing install was told about. A release that reaches nobody is not a
   > release, and neither gate is automatic — check the feeds, do not assume.

10. **Refresh the generated Releases page and commit it.** The GitHub Release
    now exists, so `docs/RELEASES.md` can finally include it — which is why
    this is here and not back at step 3. Write the release's one-paragraph
    summary first, then regenerate:

    ```bash
    $EDITOR docs-site/data/release-highlights.json   # add the "vX.Y.Z" entry
    (cd docs-site && npm run releases)
    git add docs/RELEASES.md docs-site/data/releases.json
    git commit -m "docs(releases): vX.Y.Z"
    ```

    `npm run releases` is the **only** thing that writes these two files;
    nothing else in the release does, and the docs build gate deliberately does
    not. It writes only when the content actually changed, so a run that says
    `nothing to do` is a run you can ignore rather than revert.

    > ⚠ Without the `release-highlights.json` entry the generator renders the
    > "Latest" blurb from the commit subjects, or as a bare em dash. That file
    > is hand-written from this repository's own `CHANGELOG.md`.
    >
    > ⚠ If GitHub is unreachable the generator keeps the committed cache, says
    > so loudly on stderr, and exits 0 — it never publishes an empty page. In
    > that case the new release is simply not on the page yet; run it again
    > later.

11. **Push the documentation prose to docs.filex.sh.** A cron on the server
    (`/root/filex-docs-refresh.sh`, versioned here as
    `docs-site/scripts/refresh-on-main.sh`) rebuilds and republishes the site —
    but it reads a **snapshot** at `/root/filex-docs-src`, and it deliberately
    refreshes only `RELEASES.md` inside it. Everything else in `docs/` reaches
    the site when a person copies it there, and nothing scripted does that.

    ```bash
    ssh main 'cp -a /root/filex-docs-src /root/filex-docs-src.bak-$(date +%Y%m%d-%H%M%S)'
    ssh main 'rm -rf /root/filex-docs-src/docs'
    # ⚠⚠ From the EXPORT, not from here. docs.filex.sh is a public site, and
    # this tree carries the private module path: `docs/PLUGINS.md` tells a
    # plugin author to import `github.com/brf-tech/filex/backend/pkg/
    # pluginsdk`, while the published module is `github.com/brf-tech/filex/
    # backend`. Pushed from the source, the site hands strangers an import
    # path that does not compile and names a repository they cannot reach.
    # Measured 2026-09-07: PLUGINS had one such line and CONTRIBUTING two.
    cd /g/filex-export
    tar czf - docs README.md CHANGELOG.md | ssh main 'tar xzf - -C /root/filex-docs-src'
    tar czf - --exclude=node_modules --exclude=.vitepress/dist               --exclude=.vitepress/cache docs-site       | ssh main 'tar xzf - -C /root/filex-docs-src'
    ssh main 'bash /root/filex-docs-refresh.sh'
    ```

    Then read the live page back — a page that builds is not a page that
    published:

    ```bash
    curl -s https://docs.filex.sh/RELEASES | grep -o 'Latest — v[0-9.]*'
    ```

    > ⚠ Keep `docs-site/node_modules` on the server: the refresh script builds
    > there and does not install. The `rm -rf` above is scoped to `docs/` for
    > that reason.
    >
    > Why this is a numbered step: measured 2026-09-05, hours after v0.32.0 was
    > tagged and deployed, `/root/filex-docs-src/docs/index.md` was still the
    > 4 September copy — the whole release's documentation round, including a
    > new feature card and a change to how every heading id is spelled, had not
    > reached the site. The cron had been running the whole time and was working
    > exactly as designed; the step it does not do is this one.
    >
    > ⚠ Step 10 above must already have happened: this copies `docs/` to the
    > server, and the refresh script there deliberately regenerates only
    > `RELEASES.md`. A `release-highlights.json` that never reached the server
    > gives the Latest blurb as a bare em dash.

12. **Check the shop window — what a stranger touches before they trust us.**
    Everything above audits the product from the inside: prose, screenshots,
    links, anchors, version manifests, and a test suite that runs against code.
    This step looks at the surfaces a first-time reader actually receives.

    ```bash
    # before the tag — needs a binary, no network
    pnpm run build:backend
    node scripts/check-shop-window.mjs --instance --boot bin/filex

    # after step 11 — needs the network, nothing else
    node scripts/check-shop-window.mjs --published
    ```

    Three exit codes, and the last two are the point: **0** everything checked
    passed, **1** a defect is present and the release does not go out, **2**
    something *could not be checked* — no binary, no network, a GitHub rate
    limit, a fixture that is not set up. ⚠ Exit 2 is deliberately not 1: a gate
    that turns an outage into a failed build is an outage of its own. It is
    also not 0 — the run says out loud which check did not happen, and you
    re-run it before you tag rather than assuming.

    The third of this gate that needs neither a server nor the network —
    the URL grammar, the quickstart command, the publish paths that carry no
    converter — is `web/tests/deploy/shopWindow.test.ts`, so it runs on every
    push and in `pnpm test`, and step 6's CI gate already blocks the tag on it.
    Nothing to run by hand.

    ⚠ `--instance` boots a throwaway on port 5941 with demo mode on, an
    external service host set as a sentinel and its own temp data directory,
    interviews it and kills it. It never touches a live host: two of its
    checks write. Point it at a URL instead (`--instance http://127.0.0.1:…`)
    only for an instance you booted yourself, and expect `skip` rather than
    `ok` on anything its fixture does not cover.

    > Why this is a numbered step: on 2026-09-07, hours before the public
    > launch, a person looking at filex from outside found seven defects — and
    > **not one had been caught by a test, a lint, or any of the eleven steps
    > above**. Several had been shipping for months. A dead `Issues` link on
    > **104 of the 105** published release pages, because the export translated
    > the host and not the URL grammar. A public demo that answered **all 101**
    > admin routes with no refusal: reset the shared password, delete users,
    > repoint a storage, make the server connect wherever a visitor pointed it.
    > `GET /api/files/capabilities` handing anonymous callers the operator's
    > internal hostname. docs.filex.sh built from the **private** tree, so the
    > plugin guide published an import path that names an unreachable
    > repository and does not compile. Release bodies that were a commit hash
    > where the changelog had 1,445 characters of prose. A headline
    > `docker run` that dropped the reader into an empty file manager with
    > their files in the database directory. And the demo's own advertised
    > search query returning zero results.
    >
    > The pattern is the reason this is a step and not a habit: **everything a
    > stranger touches first is the least tested surface in the project**,
    > precisely because everyone who works on it arrives from the inside.

    The exhaustive half of the demo check is a **Go test**, not this script:
    `backend/internal/api/shop_window_route_table_test.go` walks the whole chi
    route table — 359 entries — and classifies every state-changing one by
    asking the running server whether a role gate stands in front of it
    (anonymous 401, signed-in non-admin 403). Every operator surface it finds
    must be refused on a demo, and no route an ordinary user may use may be.
    The six routes this script probes are the smoke test that the guard is
    installed at all; the Go test is what makes a **fourth** guarded prefix
    impossible to add unnoticed — it found `/metrics` on its first run. It runs
    in `go test ./...`, so step 6 already blocks the tag on it. ⚠ Nothing in it
    names a route, and it has to stay that way: the moment it becomes a list it
    stops covering the surface nobody has written yet.

    > ⚠ What this gate does **not** cover, so that nobody reads a green run as
    > more than it is:
    >
    > * **It does not look at a picture.** It compares commit dates — a
    >   screenshot older than the code that draws it cannot be showing that
    >   code — and it fails only once a picture has been left behind through
    >   six released versions, which is the distance `admin-plugins.png` had
    >   actually drifted. A comment added to a component counts as a change; a
    >   theme, font or browser change counts as nothing; and a picture that was
    >   wrong the day it was taken is invisible to it. **Step 2 is still
    >   opening the PNGs.**
    > * **It does not read prose for staleness.** filex.sh is checked for the
    >   hosts it must link and for private URLs, not for whether its sentences
    >   are still true (step 3).
    > * **A route with bespoke authorization can hide from the walk.** The
    >   classification is behavioural: a route that answers an ordinary
    >   signed-in user exactly as it answers an anonymous one reads as "not
    >   role-gated". Anything mounted as an opaque all-method handler with its
    >   own check, rather than behind `auth.RequireAdmin` or
    >   `RequireScope("admin")`, is only as visible as its status codes make it.
    > * **The About blurb is compared, not published.** GitHub has no deploy
    >   step for it: the check prints the exact line and a person pastes it into
    >   Settings → General → Description.
    >
    > The demo host's own corpus **is** covered now — `--published` signs in to
    > demo.filex.sh with the credentials the demo publishes and types the
    > queries the splash advertises — but only when the demo is reachable. An
    > unreachable demo is a `2`, and a `2` means nobody proved anything.

If something fails, fix forward — never delete a published tag.
