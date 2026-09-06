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
cd backend
go run ./cmd/filex serve --listen 127.0.0.1:5212 --data-dir ./.dev-data
```

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
   and writes the set. `admin-plugins.png` needs the example plugin built and
   running: the script tries `go build`, then cross-builds through WSL, then
   `SHOTS_PLUGIN_BIN` if you point it at a binary yourself. Then **look at the
   PNGs** before committing them — see `e2e/README.md` for the knobs (running
   server, VM/WSL paths, thumbnails, the demo-mode landing page).

   Then copy the ones filex.sh shows:

   ```bash
   node scripts/sync-site-assets.mjs
   ```

   ⚠ `site/assets/` is documented as a copy of `docs/screenshots/` and nothing
   kept it one: on 2026-09-06 the marketing page was still showing the plugins
   picture whose footer named the **private** GitLab repo, after the README had
   been fixed. `web/tests/deploy/siteAssets.test.ts` fails the build now.

   ⚠ **A shot the script could not take exits 1.** It used to log a line and
   exit 0, which is how a picture stayed behind for several releases with a
   `github.com/brf-tech/filex` footer in it — in the README of the public
   GitHub repo. If you genuinely want a partial run, say so with
   `SHOTS_ALLOW_SKIP=1`; do not commit around it.

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
   | **`web/src/views/Login.vue`** (`demo.*` in `web/src/locales/*.json`) | the **demo.filex.sh landing page** — rendered by the app when `FILEX_DEMO_MODE=true`, so it looks like code and gets audited like nothing. It went untouched from 2026-05-07 to 2026-09-05 still selling *"5 Storage drivers"*. Ships in the release image; see `docs/DEPLOY_BRF.md` §4b |
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

CI does the rest (GitHub Actions `release.yml`, five jobs):
- `binaries` — goreleaser: multi-arch binaries → the GitHub Release. It is
  what *creates* the Release, so `desktop` below depends on it.
- `docker` — a **matrix**, one native runner per architecture (amd64 on
  `ubuntu-latest`, arm64 on `ubuntu-24.04-arm`), each pushing by digest.
  ⚠ It has **no `needs:`** — it builds its own binary and never wanted the
  release. arm64 used to run under QEMU behind `needs: binaries` and took
  20-30 minutes; on a native runner the whole critical path is about seven.
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
- `npm` (independent) — publishes `@brftech/filex-core`, `@brftech/filex`,
  `@brftech/filex-react`.

9. **Publish the two update feeds, then prove they moved.** CI attaches every
   artifact to the GitHub Release; it publishes **neither feed**, and a feed is
   the only place an installed copy ever looks. Both live on the static host and
   are deliberately excluded from `scripts/sync-site.sh` (so a website deploy
   cannot delete them) — which also means nothing refreshes them but you.

   - `filex.sh/updates/stable.json` — **the server and CLI**. Prepend a record
     for the new version: `version`, `date`, `auto_ok`, `migrations`, `notes`,
     `notes_url`, `image`, `assets`. Every install with `AUTO_UPGRADE` reads
     this and nothing else.
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

If something fails, fix forward — never delete a published tag.
