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
| `tests/101-quicklook-hint.spec.ts` | the quick-look legend stays a pill (issue #22) |
| `tests/102-touch-tap-opens.spec.ts` | on a touch screen a tap opens, a long press selects (issue #26) |

`helpers/auth.ts`  → `loginAs`, `apiLogin`, `logout`
`helpers/seed.ts`  → `seedLocalStorage`, `dropStorageByName`, `waitForOp`
`fixtures/`         → small files used by upload tests

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
`driveshell`, `sidenav`, `starstags`, `e2e-recovery`, …), syncs the site assets
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
| `shots.yml` | every `v*` tag, and on demand | `pnpm shots` on Linux — a shot script that no longer fits the product turns red here instead of on release night |

⚠ **No CI job runs the Playwright suite** (`node e2e/run.mjs local`). It gates a
release because the release process runs it (`docs/CONTRIBUTING.md` → *Release
process*, step 6, together with the Cypress profile), not because a pipeline
does. Run both before tagging.
