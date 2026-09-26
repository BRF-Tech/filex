# filex — E2E test suite

End-to-end tests powered by [Playwright](https://playwright.dev). They
drive the same Vue 3 admin UI a real user sees, against a running
`filex` HTTP server.

## Prerequisites

- Node 20+, pnpm 9+
- Docker (for the most repeatable run, but not strictly required)

## Run locally

One command. It starts a filex binary on a free port against a throwaway data
dir with a deterministic admin, waits for `/healthz`, runs the suite, and tears
everything down again:

```bash
cd e2e && pnpm install && pnpm install:browsers   # once
node e2e/run.mjs local --build                     # from the repo root
```

Drop `--build` once you have a binary in `bin/`, or point at one with
`--binary <path>`. Other flags:

| Flag | What it does |
|---|---|
| `--s3` | also starts MinIO in Docker, creates a bucket and registers an `s3` storage, then runs `26-s3-storage.spec.ts` against it |
| `--keep` | leaves the server (and the data dir) up afterwards so you can poke at it |
| `--port <n>` | fixed port instead of a free one |
| `--grep <pattern>` | passed through to Playwright |

The **cypress** profile starts the same kind of instance and drives
`web/cypress` instead. The two suites are not duplicates — Playwright walks
journeys, Cypress pins the HTTP contracts and the admin screens that read them
(`web/cypress/README.md` has the split, and `docs/CONTRIBUTING.md` has the "which
one do I add a test to" table):

```bash
node e2e/run.mjs cypress                # every spec
node e2e/run.mjs cypress --spec "cypress/e2e/14-explorer-sidenav.cy.ts"
```

It seeds one deterministic local storage before running. ⚠ That seed is not a
nicety: a bare instance has zero storages, and most Cypress specs discover "the
first storage" and then quietly assert nothing when there is none — a green run
that measured almost nothing.

The **deployment** profile is a separate, read-only smoke against something
already live, and is deliberately not part of a build check:

```bash
node e2e/run.mjs deployment --url https://fm.example.com
```

⚠ Keep the two apart. `90-deployment-smoke.spec.ts` talks to production, so a
run that mixes it into the local suite goes red when production is slow — which
means it can no longer answer the only question a pre-release run exists to
answer: *is this build good?*

⚠ **There is no `FILEX_E2E_BOOTSTRAP` env var.** This file and
`playwright.config.ts` both documented one for a long time; the binary has never
read it. Use `FILEX_ADMIN_EMAIL` / `FILEX_ADMIN_PASSWORD` (which is what
`run.mjs` does).

⚠ Use `127.0.0.1`, not `localhost`: on Windows `localhost` resolves to `::1`
first, and a server bound to `127.0.0.1` answers that with `ECONNREFUSED` —
indistinguishable from a server that failed to start.

⚠ **Never give the server's stdout to a Node pipe.** `run.mjs` hands the child
a file descriptor (`stdio: ['ignore', logFd, logFd]`). The obvious alternative
— `'pipe'` plus `child.stdout.pipe(writeStream)` — deadlocks the server: the
suite runs under `spawnSync`, which blocks Node's event loop, so nothing drains
the pipe, the 64 KiB OS buffer fills, and filex (one log line per HTTP request,
written from inside the request path) blocks forever in `write(2)`. Measured:
551 requests served, then dead to everything including `/healthz` for the rest
of the run — 20 specs failing with connection timeouts that look exactly like a
product deadlock. `tests/01-harness.spec.ts` guards both the mechanism and the
call.

⚠ **A storage's root is `config.path`.** Not `mount_path`, and not
`config.root` when `config.path` is also set — `local.Driver.Init` reads `path`
first. `helpers/seed.ts` used to send `{root: mountPath, path: 'fileman'}`, so
every storage every spec created resolved to the same `./fileman` directory
under the server's working dir and specs read each other's files. Use
`seedLocalStorage`, which asserts the server stored the root you asked for.

`pnpm test:ui` opens the Playwright UI mode for stepping through tests
visually. `pnpm test:debug` opens the inspector.

## Test layout

Every spec opens with a comment naming what it pins and, for a regression, the
report it came from — read that before changing an assertion.

| File | Coverage |
|------|----------|
| `tests/00-smoke.spec.ts` | server up, healthz, capabilities, login page renders |
| `tests/01-harness.spec.ts` | the harness itself: no piped server log, isolated storages |
| `tests/10-login.spec.ts` | bad creds rejected, good creds land on **Home**, logout |
| `tests/20-storage.spec.ts` | admin storage list + dashboard widget |
| `tests/25-connections.spec.ts` | the connection guides (WebDAV, SFTP, S3, mount) name the real address |
| `tests/26-s3-storage.spec.ts` | a real MinIO (`--s3`, two storages): round trip, ranged read, re-chunking, trash, a >8 MiB move between object stores |
| `tests/27-usage.spec.ts` | the *Usage & cost* page |
| `tests/30-files.spec.ts` | upload, list, soft-delete |
| `tests/40-share.spec.ts` / `77-share.spec.ts` | the admin share list / share creation + public access |
| `tests/50-search.spec.ts` | admin search index page + rebuild |
| `tests/60-user-settings.spec.ts` | the user-settings dialog (`?settings=1`): language, password, TOTP enroll |
| `tests/70-multi-storage.spec.ts` | adapter-prefix routing across storages |
| `tests/75-navigation.spec.ts` | breadcrumb root crumb, go-up, Alt+↑ |
| `tests/76-trash.spec.ts` | soft-delete, restore, admin purge |
| `tests/78-save-text.spec.ts` / `83-meta-and-markdown.spec.ts` | the editors' write-back |
| `tests/79-per-verb-async.spec.ts` | `/copy` `/move` `/delete` → 202 + op polling |
| `tests/80-file-types.spec.ts` / `100-viewer-audit.spec.ts` | MIME contract / a viewer mounts for every extension |
| `tests/82-capability-gating.spec.ts` | features hidden when the server lacks them |
| `tests/85-resumable-upload.spec.ts` / `86-slow-storage-cache.spec.ts` | resumable chunks / prepared copies on slow storage |
| `tests/90-deployment-smoke.spec.ts` | **deployment profile only** — read-only smoke against a live URL |
| `tests/91-rounds-4-6-regression.spec.ts` | round 4-8 regressions; seeds its own fixtures, or `E2E_FIXTURE_STORAGE` |
| `tests/95-app-plugins.spec.ts` | an app plugin end to end: install → menu → job → output → public page (the `echo` fixture) |
| `tests/96-app-plugin-convert.spec.ts` | the **convert** app: install, the target picker as a row of buttons, the grey list for a missing engine, PNG → a real JPEG |
| `tests/97-app-plugin-sign.spec.ts` | the **sign** app: install, state-aware menu rows, every screen against the renderer rules, a signed sibling carrying a real PAdES signature |
| `tests/101-quicklook-hint.spec.ts` | the quick-look legend stays a pill (issue #22) |
| `tests/102-touch-tap-opens.spec.ts` | on a touch screen a tap opens, a long press selects (issue #26) |
| `tests/98-custom-theme.spec.ts` | the **Appearance** screen: compose a theme, make it the instance default, see it on the explorer and on the signed-out page |
| `tests/99-app-plugin-sign-round.spec.ts` | a whole signature round: inside signer, outside signer behind a PIN, the finished document |
| `tests/103-sso-button-branding.spec.ts` | the sign-in page wears the instance default, never the last viewer's palette |
| `tests/104-readonly-storage.spec.ts` | a read-only storage says so to everybody, and its menu offers only what it can do |
| `tests/106-virtual-view-menu.spec.ts` | a right-click on Home's cards and the other virtual views opens the background menu |
| `tests/107-search-scope-placeholder.spec.ts` | the header field says what it will search, and the mode matches the place |
| `tests/108-column-reorder.spec.ts` | the one table: resize, hide and drag a column, everywhere it is drawn |
| `tests/109-notifications-non-admin.spec.ts` | the bell's count and **View all** for somebody who is not an administrator |
| `tests/110-symlink-badge.spec.ts` | a symlink that leaves the storage is badged with the reason, and refused |
| `tests/111-internal-dirs.spec.ts` | `.filex-trash`, `.versions`, `.thumbs`, `.filex-open`: refused as names, absent from every view |
| `tests/112-language-pack-server-text.spec.ts` | a pack's language reaches the text the SERVER writes (mail, the pages behind a link) |
| `tests/113-app-plugin-sign-seal.spec.ts` | the platform seal: the finished document sealed by filex, and the hash everybody is sent |
| `tests/114-language-pack.spec.ts` | a **language pack** end to end: install from a manifest with no module, the pickers, the explorer and the admin panel in it |
| `tests/115-tag-kinds.spec.ts` | personal vs team tags: who sees which, and who may take one off |
| `tests/116-admin-says-which.spec.ts` | the panel names the thing, not only its kind (audit target, file history by name) |
| `tests/120-tour-once-per-person.spec.ts` | the first-use tour is offered once per person, not once per mount |
| `tests/121-share-dialog-one-link.spec.ts` | the share dialog makes ONE link, with one copy style |
| `tests/122-public-drop-page.spec.ts` | the public drop page: the name it asks for, its limits, a refused file said in words |
| `tests/123-trash-columns-and-touch-menu.spec.ts` | the Trash's facts (deleted when, from where, how long left) and a phone's row menu |
| `tests/124-app-plugin-levels.spec.ts` | an app action a person could only be refused is not offered; an admin sees it greyed with the reason |
| `tests/125-app-plugin-readonly-destination.spec.ts` | Convert on a read-only storage: the wizard's **Where** step and the server's re-check |
| `tests/126-rtl-server-text.spec.ts` | a right-to-left language: the layout turns, and machine text stays left to right inside it |
| `tests/158-sidebar-storage-order.spec.ts` | the storages' order (#57): a person drags / uses the row menu / the keyboard / a finger in the navigation panel, the administrator reorders the Storages table, and a person with no order of their own sees the administrator's; nothing overflows at 1280 and 390 px, light and dark |

`helpers/auth.ts`     → `loginAs`, `apiLogin`, `logout`
`helpers/seed.ts`     → `seedLocalStorage`, `dropStorageByName`, `waitForOp`
`helpers/surface.ts`  → app-plugin surfaces over HTTP: `checkSurface` / `checkLanguages` (the renderer's rules, the browser-side twin of `pkg/pluginkit/plugintest`), `choices`, `driveToJob`
`helpers/appPlugin.ts` → finding and installing a real app module: `resolveApp`, `guardFixture`, `installThroughWizard` (adds `APP_INSTALL_ALLOWANCE_MS` = 90 s to the test's timeout — filex compiles the module before it answers, ~23 s idle and well over 30 s on a busy machine; a spec sets no install timeout of its own)
`helpers/rowMenu.ts`  → an admin row's verbs: `openRowMenu`, `rowMenuVerbs`, `pickRowAction`, `confirmRowAction`. ⚠ Every admin row ends in ONE **Actions** control and its menu is teleported to `<body>`, so `row.getByRole('button', …)` can never reach a verb; entries are addressed by the words a person reads
`helpers/prefs.ts`    → `setAccountViewMode`. ⚠ The view mode, the sort and the columns live on the ACCOUNT (`/api/files/manager/view-prefs`), and so do theme, palette, density and language (`/api/me/prefs?surface=web`). The `localStorage` keys are a FIRST-PAINT CACHE that the account's answer overwrites a moment after boot, so a spec that only seeds them measures a race it usually loses
`fixtures/`            → small files used by upload tests

> ⚠ The two app specs read their module from a sibling checkout's `dist/`, and
> `dist/filex-app.json` there is refreshed only by `bash scripts/build.sh
> --stamp`. A plain `build.sh` leaves the previous release's manifest beside a
> new `plugin.wasm`, filex refuses the pair with `describe_mismatch`, and the
> install simply never completes. `installThroughWizard` reads the wizard's own
> error so that shows up as the sentence the screen is displaying rather than
> as a timeout.

> The two app specs need a built `plugin.wasm` from a sibling checkout
> (`../filex-convert`, `../filex-sign/dist`, or `FILEX_CONVERT_APP_DIR` /
> `FILEX_SIGN_APP_DIR`). Without one they SKIP — unless
> `FILEX_REQUIRE_WASM_FIXTURE=1`, which CI sets so that "green" can never
> mean "skipped".

## Screenshots (`shots/`)

Every picture the README and the docs show comes from the scripts in `shots/`,
in English, against the build in this working tree. Reviewing them is a
numbered step in the release process (`docs/CONTRIBUTING.md`): a stale
screenshot is wrong information, not missing information.

```bash
pnpm shots                          # build → verify the embedded UI → shoot → sync → contact sheet
pnpm shots --only sidenav,capture   # a subset
```

`scripts/shots.mjs` builds the whole chain in order, proves with
`scripts/check-embed.mjs` that the binary serves `web/dist` byte for byte
**before** a picture is taken, runs every script in `shots/` (`capture`,
`driveshell`, `sidenav`, `starstags`, `tags`, `langpack`, `notifications`,
`e2e-recovery`, `apps`, `signing`, `appearance`, `symlinks` — all twelve),
syncs the site assets
and writes one contact sheet, `e2e/.artifacts/shots/contact-sheet.html` — look
at it. Pictures land in `docs/screenshots/<release>/`.

Each script can still be run on its own (`node e2e/shots/capture.mjs`); it
boots its own instance, generates the demo tree (`shots/fixtures.mjs` — PNGs
encoded with Node's zlib, no image dependency) and waits for the sync a seed
depends on (`syncAndWait`: the sync endpoint answers 202). Useful environment
variables:

| Variable | Why |
|---|---|
| `FILEX_BIN` | binary to run (default `bin/filex`) |
| `SHOTS_OUT` | output directory (default `docs/screenshots/<release>/`) |
| `SHOTS_URL` | shoot an instance that is ALREADY running instead of booting one |
| `SHOTS_STORAGE` / `SHOTS_MOUNT` | the fixture directory as *this machine* and as the *server* see it — they differ when the server runs in a VM / WSL / container |
| `SHOTS_SEED_ONLY`, `SHOTS_SKIP_SEED` | two passes: seed, run `filex thumb backfill` out of band, then capture. Thumbnails are rendered on UPLOAD, so fixtures written straight to disk have none and the hero shot comes out as a grid of generic icons |
| `SHOTS_DEMO` | capture `demo-landing.png` — needs an instance booted with `FILEX_DEMO_MODE=true`, because that page replaces the login screen |
| `SHOTS_PLUGIN_BIN` | an already-built `examples/plugin-memfs` binary for the shots machine. Normally unnecessary: when `go` is not on the PATH the script cross-builds the plugin **through WSL**. ⚠ A path that is set and wrong is an error, not a shrug |
| `SHOTS_ALLOW_SKIP` | permit a deliberate partial run. ⚠ Without it, **a shot the script was asked for and could not take fails the run** — that is the point: `admin-plugins.png` sat outdated for several releases behind a script that logged one line, skipped it and exited 0, and a release step that reports success while leaving the old file in place is not a gate |
| `SHOTS_KEEP` | leave the instance running afterwards |
| `FILEX_SIGN_APP_DIR` / `FILEX_CONVERT_APP_DIR` | where `apps.mjs` and `signing.mjs` find the two apps' `plugin.wasm` + `filex-app.json` — the same variables and the same fallbacks (`../filex-sign/dist`, `../filex-convert`) as the Playwright specs' `resolveApp`. Set, a directory is the only one looked in. Missing, the script **fails** rather than skipping the pictures; `pnpm shots` passes these two through and no other `FILEX_*` |

`apps.mjs`, `signing.mjs`, `appearance.mjs` and `symlinks.mjs` share one stage,
`shots/scene.mjs`: an instance on its own port and data directory with no
`FILEX_*` inherited from your shell, an API client per person, a browser
pinned to English, `shot()`, and the agreement PDF the signing scenes send
round. ⚠ `symlinks.mjs` needs a host that can create symlinks (Windows only
with Developer Mode or elevation) and fails where it cannot.

## Notes

- Tests are **serialized** (`workers: 1`) because the backend is
  single-tenant and shares a single SQLite DB across the run.
- `E2E_AUTOSTART=1` makes Playwright spin the Docker image up itself
  via `webServer` config. Nothing in CI sets it.
- Skips must be **measured and explained**, never a hedge. A skip whose
  condition can no longer become false is a deleted test with extra steps:
  the old `60-profile` spec skipped its locale and password cases on every run for as long as
  anyone looked, because it matched `/old password/` against a field labelled
  "Current password". If you write `test.skip`, the reason string has to name
  the thing that is missing (`rsvg-convert is not on PATH here`) so a reader
  can tell "this machine can't" from "this build is broken".
- Environment-dependent cases gate on the capability probe, not on a hostname:
  OnlyOffice (`external.onlyoffice.state`), SVG thumbnails
  (`thumbs.svg` / rsvg-convert), office thumbnails (`libreoffice`), S3
  (`--s3`). On a host with those installed they become real assertions with no
  code change.

## CI

The public repository's GitHub Actions (`.github/workflows/`):

| Workflow · job | When | What |
|---|---|---|
| `ci.yml` · `browser` | every push to `main` and every pull request | `node e2e/run.mjs cypress --build` — the Cypress suite against a throwaway build of that commit; failure screenshots and video are uploaded |
| `shots.yml` | every `v*` tag, and on demand | `pnpm shots` on Linux — a shot script that no longer fits the product turns red here instead of on release night. ⚠ The scenes that need an app build (`apps.mjs`, `signing.mjs`) are **left out** in CI and taken locally at release step 2 — see [CONTRIBUTING.md → Screenshots](../docs/CONTRIBUTING.md#screenshots) |

⚠ **No CI job runs the Playwright suite** (`node e2e/run.mjs local`). It gates a
release because the release process runs it (`docs/CONTRIBUTING.md` → *Release
process*, step 6, together with the Cypress profile), not because a pipeline
does. Run both before tagging.
