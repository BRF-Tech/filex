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
- [Release process](#release-process)

---

## Development setup

Requirements:
- Go 1.22+
- Node.js 20+
- pnpm 9+
- (optional) Docker, ffmpeg, ghostscript, libreoffice for thumbnail dev

```bash
git clone https://github.com/brf-tech/filex.git
cd filemanager

pnpm install            # all workspace packages
pnpm run dev            # parallel: package watch + admin Vite dev server

# In another shell — Go backend
cd backend
go run ./cmd/filex serve --listen 127.0.0.1:5212 --data-dir ./.dev-data
```

The admin SPA is served by Vite at <http://localhost:5173> in dev mode and
proxies `/api/*` to the Go server at `:5212`. For the embedded build (what
ships in the binary), use `pnpm run build:all`.

### Running with hot-reload

```bash
# Terminal 1 — Go (recompiles on save with air)
go install github.com/cosmtrek/air@latest
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

BREAKING CHANGE: the Vue event name changed. See docs/MIGRATION.md.
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

### General

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
  section in [CONFIGURATION.md](CONFIGURATION.md).
- New external service → all of the above.
- Behaviour change → CHANGELOG entry under `## [Unreleased]`.

---

## Release process

Maintainer-only. Reproducible, automated by CI.

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

2. **Open every screenshot the README shows and check it against the shipped
   UI — in English.** Same failure as step 1, in pictures: a screenshot is
   read as a promise about what the product looks like *now*, and a stale one
   is wrong information rather than missing information. Two things to
   confirm, image by image:

   - **Language.** These are the repo's shop window and its readers are not
     Turkish. Every visible string — buttons, menus, dialog footers — must be
     English. Capture with the UI language set to English and re-read the
     result; a half-translated dialog ("Düzenle" next to "Download") is worse
     than no screenshot, because it looks like a product that cannot pick a
     language.
   - **Currency.** If a release touched a screen a screenshot shows, retake
     it. A control added this cycle must be visible in the picture that claims
     to show that screen.

   Retake them with:

   ```bash
   pnpm run build:all                     # the shots come from this binary
   pnpm --dir e2e run install:browsers    # once
   node e2e/shots/capture.mjs             # → docs/screenshots/*.png
   ```

   ⚠ `build:all` ends in `build:backend`, which is a plain `go build` — it
   needs Go on the **same** PATH as pnpm. On a Windows workstation whose
   toolchain lives in WSL, run the first three steps natively and cross-build
   the binary the shots boot:

   ```bash
   wsl -e bash -lc 'cd /mnt/g/filex/backend && \
     GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" \
     -o /tmp/filex.exe ./cmd/filex && cp /tmp/filex.exe /mnt/g/filex/bin/filex.exe'
   ```

   It boots a local instance, seeds a demo tree, pins the UI language to
   English three ways over (browser locale, stored preference, server default)
   and writes the set. `admin-plugins.png` needs the example plugin built for
   the shots machine — the script builds it when `go` is on PATH, otherwise
   point `SHOTS_PLUGIN_BIN` at a binary you built (on a Windows workstation
   with the toolchain in WSL, cross-build it: `GOOS=windows go build -o …`). Then **look at the PNGs** before committing them — see
   `e2e/README.md` for the knobs (running server, VM/WSL paths, thumbnails, the
   demo-mode landing page).

   > Why this is a numbered step: by 2026-08-14 `share-modal.png` showed a
   > share dialog with no download limit — a control that had shipped two
   > releases earlier — and `viewer-markdown.png` had Turkish buttons in it.

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
   | `docs/*.md` | the new feature has a page — **and the old pages are still true** |
   | `docs/README.md` | every `docs/*.md` is in the index |
   | **`docs/index.md`** | the docs site's **home page** — its hero line and feature cards are the first thing a visitor reads |
   | `docs-site/.vitepress/config.mts` | the new page is in the **sidebar** |
   | `packages/*/README.md` | these are the **npm pages** — an export nobody documents does not exist for anybody installing the package |
   | `desktop/README.md` | what the app actually does |
   | `deploy/*/README.md` | install instructions per target |
   | `deploy/umbrel/*/umbrel-app.yml`, `deploy/casaos/*` (`x-casaos.description`) | **app-store listings** — public product copy |
   | `deploy/helm/*/Chart.yaml` | shown by `helm search` |
   | `package.json` descriptions | shown on npm |
   | `deploy/compose/*.yml` | new env vars and **published ports** with the traps beside them |

   > ⚠ The dangerous case is not a missing page, it is a **page that lies**.
   > On 2026-08-17 `STORAGE.md` still said *"There is no `nfs` or `smb` driver,
   > and there doesn't need to be"* — the `smb` driver had shipped in that very
   > release.

   > ⚠⚠ A page missing from the sidebar is **not** unpublished. VitePress builds
   > every file under `srcDir`, so it is reachable by URL and indexable whether
   > or not anything links to it. To actually keep a page off the site, add it to
   > **`srcExclude`**. On 2026-08-17 five pages were live but unreachable from the
   > nav, and `CLOUD.md` — whose own first line says *"NOT a live service"* — was
   > being published.

   Three commands finish the step, all required:

   ```bash
   # every relative markdown link resolves to a real file
   python3 - <<'PY'
   import os, re
   roots = ['README.md', 'CHANGELOG.md', 'docs', 'packages/core/README.md',
            'packages/webcomponent/README.md', 'packages/react/README.md',
            'desktop/README.md', 'e2e/README.md']
   files = []
   for r in roots:
       files += ([os.path.join(r, f) for f in os.listdir(r) if f.endswith('.md')]
                 if os.path.isdir(r) else [r] if os.path.exists(r) else [])
   bad = 0
   for f in files:
       base = os.path.dirname(f) or '.'
       for m in re.finditer(r'\]\(([^)\s]+?)(#[^)]*)?\)', open(f, encoding='utf-8').read()):
           t = m.group(1)
           if t.startswith(('http', 'mailto:', '#', '/')):
               continue
           if not os.path.exists(os.path.normpath(os.path.join(base, t))):
               print('MISSING', f, '->', t); bad += 1
   print('broken links =', bad)
   PY

   # the site must BUILD — VitePress fails the build on a dead link
   cd docs-site && npm run build

   # every YAML you touched still parses — breaking a store listing is silent
   python3 -c "import yaml,glob; [yaml.safe_load(open(f,encoding='utf-8')) \
     for f in glob.glob('deploy/**/*.yml', recursive=True)]; print('yaml ok')"

   # the Helm chart ships the version you are about to release
   node scripts/sync-chart-version.mjs --check
   ```

   ⚠ A relative link to a page that is in `srcExclude` is a dead link *on the
   site* even though it resolves in the repo — link those by full GitHub URL.
   That is how the "not published" list in `docs/README.md` broke the build the
   first time it was written.

4. Update `CHANGELOG.md` — move `[Unreleased]` to a dated `[vX.Y.Z]` heading.
5. **Every release updates the Helm chart. No exceptions.** (Burak's rule,
   2026-08-29: *"her yeni tag'de versiyonda helm zorunlu"*.) Bump the
   `package.json` versions across all packages, then the chart:
   ```bash
   pnpm -r exec npm version X.Y.Z --no-git-tag-version
   node scripts/sync-chart-version.mjs      # Chart.yaml appVersion + chart version
   ```
   ⚠ The chart's `appVersion` is not a label: `values.yaml` ships `tag: ""`, and
   the image helper resolves that to `.Chart.appVersion` — so it IS the image a
   Helm user runs. It sat at `v0.4.0` for twenty-three releases before anyone
   noticed (2026-08-29). `web/tests/deploy/chartVersion.test.ts` fails the build
   if the two drift, and `--check` reports it without writing.
6. Commit: `chore(release): vX.Y.Z`.
7. Tag: `git tag -s vX.Y.Z -m "vX.Y.Z"` — **signed**, and `git tag -v vX.Y.Z`
   must answer `Good signature` before you push. Releases up to and including
   v0.27.5 are plain annotated tags: the instruction said `-s` for months while
   no signing key existed, so nobody could follow it and nobody noticed. The
   maintainer key is `EFA3B126 2FD99280 0DBBB5E3 A8FEBA97 FF786513` (ed25519,
   expires 2028-08-31); its passphrase and a recovery copy live in the team
   vault, not on disk.
8. Push: `git push origin main --tags`.

CI does the rest (GitHub Actions `release.yml`, four jobs):
- `binaries` — goreleaser: multi-arch binaries → the GitHub Release. It is
  what *creates* the Release, so the two uploaders below depend on it and on
  nothing else.
- `docker` (needs `binaries`) — pushes `:vX.Y.Z`, `:slim-vX.Y.Z`,
  `:full-vX.Y.Z`, `:latest`, `:slim`, `:full` to ghcr.io. The slow one
  (arm64 under QEMU, 20-30 min).
- `desktop` (needs `binaries`, **not** `docker`) — three installers, attached
  to the Release while the images are still building. Before v0.25.0 it
  waited for the docker builds it never needed — ~25 idle minutes a release.
- `npm` (independent) — publishes `@brftech/filex-core`, `@brftech/filex`,
  `@brftech/filex-react`.

If something fails, fix forward — never delete a published tag.
