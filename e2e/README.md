# filex — E2E test suite

End-to-end tests powered by [Playwright](https://playwright.dev). They
drive the same Vue 3 admin UI a real user sees, against a running
`filex` HTTP server.

## Prerequisites

- Node 20+, pnpm 9+
- Docker (for the most repeatable run, but not strictly required)

## Run locally

```bash
# 1. From the repo root, build the Docker image once
docker build -t filex:test -f docker/Dockerfile .

# 2. Start it with the e2e bootstrap flag (deterministic admin user)
docker run --rm -d --name filex-e2e -p 5212:5212 \
  -e FILEX_E2E_BOOTSTRAP=1 \
  filex:test serve

# 3. Install browsers + run the suite
cd e2e
pnpm install
pnpm install:browsers
pnpm test
```

`pnpm test:ui` opens the Playwright UI mode for stepping through tests
visually. `pnpm test:debug` opens the inspector.

## Test layout

| File | Coverage |
|------|----------|
| `tests/00-smoke.spec.ts`    | server up, healthz, capabilities, login page renders |
| `tests/10-login.spec.ts`    | bad creds rejected, good creds land on dashboard, logout |
| `tests/20-storage.spec.ts`  | UI flow to add a local storage + verify in dashboard |
| `tests/30-files.spec.ts`    | upload fixture, soft-delete to trash, restore from trash |
| `tests/40-share.spec.ts`    | share token + public viewer with PIN |
| `tests/50-search.spec.ts`   | admin search/index stats + rebuild button |
| `tests/60-profile.spec.ts`  | locale switch, password change, TOTP enroll |

`helpers/auth.ts`  → `loginAs`, `apiLogin`, `logout`
`helpers/seed.ts`  → `seedLocalStorage`, `dropStorageByName`
`fixtures/`         → small files used by upload tests

## Notes

- Tests are **serialized** (`workers: 1`) because the backend is
  single-tenant and shares a single SQLite DB across the run.
- `E2E_AUTOSTART=1` makes Playwright spin the Docker image up itself
  via `webServer` config — used in CI.
- A few tests use `test.skip(true, '...')` when the corresponding admin
  UI surface isn't yet wired (e.g. TOTP, search rebuild). They turn
  into real assertions as soon as the UI lands.

## CI

The `e2e` job in `.github/workflows/ci.yml` (optional, non-blocking
initially) runs the suite against the freshly built Docker image on
tag pushes.
