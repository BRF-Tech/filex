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

BREAKING CHANGE: the Vue event name changed; update listeners to @share-created.
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

Vitest with happy-dom; Playwright suites live in `web/e2e/` (run separately:
`pnpm --filter='@brftech/filex-admin' e2e`).

### What needs tests

- **Always**: every new HTTP endpoint, every new storage driver method,
  every config knob.
- **Encouraged**: new UI components (Vitest `mount`).
- **Optional but appreciated**: end-to-end Playwright scenarios when the
  flow spans many components.

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

1. Update `CHANGELOG.md` — move `[Unreleased]` to a dated `[vX.Y.Z]` heading.
2. Bump `package.json` versions across all packages:
   ```bash
   pnpm -r exec npm version X.Y.Z --no-git-tag-version
   ```
3. Commit: `chore(release): vX.Y.Z`.
4. Tag: `git tag -s vX.Y.Z -m "vX.Y.Z"`.
5. Push: `git push origin main --tags`.

CI does the rest:
- `release:goreleaser` — multi-arch binaries → GitLab Release.
- `release:npm` — publishes `@brftech/filex-core`, `@brftech/filex`,
  `@brftech/filex-react` to the GitLab npm registry.
- `release:docker` — pushes `:slim-vX.Y.Z`, `:full-vX.Y.Z`, `:latest`.

If something fails, fix forward — never delete a published tag.
