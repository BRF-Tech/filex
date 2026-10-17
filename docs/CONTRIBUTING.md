# Contributing to filex

Thanks for considering a contribution. This is a small, opinionated codebase -
before opening a sizeable PR please file an issue describing what you're
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
- (optional) Docker, ffmpeg, ghostscript for thumbnail dev; an ONLYOFFICE
  Document Server (the `onlyoffice/documentserver` image) for office
  thumbnails and the apps' office engine - filex runs no LibreOffice

```bash
git clone https://github.com/brf-tech/filex.git
cd filemanager

pnpm install            # all workspace packages
pnpm run dev            # parallel: package watch + admin Vite dev server

# In another shell - Go backend
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
# Terminal 1 - Go (recompiles on save with air)
go install github.com/air-verse/air@latest
cd backend && air

# Terminal 2 - admin SPA + packages
pnpm run dev
```

---

## Workflow

1. **Fork** + create a feature branch off `main`.
2. **Code** + write tests.
3. **Lint locally**: `pnpm run lint` and `cd backend && go vet ./... && staticcheck ./...`.
4. **Test locally**: `pnpm run test` and `cd backend && go test -race -timeout 30m ./...`.
5. **Open MR** against `main`. CI runs lint + test + build.
6. **Address review** + squash if asked.
7. **Merge** - maintainer squashes; commit message becomes a CHANGELOG line.

---

## Branches

- `main` - protected, always green.
- `feat/<short-name>`, `fix/<short-name>`, `chore/<short-name>` - feature branches.
- `sec/<short-name>` - an embargoed security fix (a hole in a released
  version). It lives in the maintainers' private repository only, passes the
  full chain there (Testing -> The private pipeline) and reaches `main` with
  the patch release that ships it.
- `release/v0.X.Y` - short-lived branch only used to cut a release.

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
go test -race -timeout 30m ./...        # internal/api/handlers alone is ~9 min, past the default 10m under -race
go test -race -timeout 30m -cover ./... # with coverage
go test -run TestStorageS3 -v ./internal/storage/s3
```

Where the whole suite has to be fast it runs in parts, side by side - see
[Shards](#shards).

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

**The admin app's main chunk has a size budget.** It carries the explorer
from `packages/core` and must fit workbox's 2 MiB precache limit
(`web/pwa.config.ts`), or `vite build` fails, so a branch that adds to core
runs `pnpm -C web build && pnpm -C web size` before it is merged (it prints
the chunk's size and the room left) and moves a dialog a person opens out of
the chunk (`packages/core/lazySurfaces.ts`) rather than raising the limit.

**A unit test never reaches the network, and it does not tidy up after
itself.** `web/tests/setup.ts` refuses every request a test did not mock
(`fetch`, `XMLHttpRequest`, `WebSocket`/`EventSource` and happy-dom's own
loads underneath them) and fails that test with the address, even when the
page swallows the error; mock the API module (`vi.mock('@/api/…')`) or stub
`fetch`, and in the rare file that truly needs the network say why with
`allowNetwork('…')` from `web/tests/helpers/noNetwork.ts`. An `<iframe src>`
stays a blank frame there (its `contentWindow` works, nothing is fetched). A
file whose tests write a preference (a theme, a density, an app frame
opening) calls `answerAccountPrefs()` from `web/tests/helpers/accountPrefs.ts`:
the account's copy is written 400 ms later, so unanswered it fails only on a
loaded run.
After every test the same setup lets in-flight work land, unmounts every
page `mount()` returned and only then empties `<body>`
(`web/tests/helpers/teardown.ts`), so a test does not keep a list of what it
mounted and never writes `document.body.innerHTML = ''` on its own - wiping
`<body>` under a page that is still mounted is what made a late answer draw
into missing DOM and fail the v0.49.0 release run with every test green. A
test that starts over half-way calls `unmountAll()`; an `afterEach` that
takes mocks away (`vi.restoreAllMocks`, `vi.unstubAllGlobals`, real timers)
calls `await teardownDom()` first, so what is still in flight lands on the
mocks.

### Browser suites

There are two, and both run against a throwaway instance this repo starts for
them - never against a live host, never with a secret:

```bash
bash scripts/build-wasm-fixture.sh   # the echo app the app-plugin specs install; again after its sources change (a stale one fails them)
node e2e/run.mjs local      # Playwright - e2e/tests/*.spec.ts
node e2e/run.mjs cypress    # Cypress   - web/cypress/e2e/*.cy.ts
```

Add `--build` on the first run (it builds the packages, the admin UI, the embed
assets and the Go binary); afterwards a binary in `bin/` is enough.

A new Playwright spec is named `<number>-<words>.spec.ts` with the highest
number in use + 1 - counted across the other open branches too, when you cut
yours - and no letter after the number (`web/tests/quality/e2eSpecNumbers.test.ts`
refuses a number used twice). Specs run in the file names' plain sort
(`126-` before `95-`), so a spec never relies on running after another.

WebKit's line runs without the panel's service worker (`serviceWorkers:
'block'` for the `webkit` project in `e2e/playwright.config.ts`, task #199):
Playwright's WebKit lost its network process while the worker installed
(Known flaky tests, below). A spec that tests the worker itself opts back in
with `test.use({ serviceWorkers: 'allow' })`, as 164's installable-app test
does. A spec that answers or holds requests with `page.route` blocks it in
every engine, because a request the worker carries is not routed (85, 97,
132, 231).

**Which one do I add a test to?**

| | Playwright (`e2e/`) | Cypress (`web/cypress/`) |
|---|---|---|
| Shape | one user journey per spec, end to end | many small cases per surface |
| Best at | flows that cross screens - upload → trash → restore, share → open with a PIN, pair a desktop app | HTTP contracts, admin screens, envelope shapes, "every route answers" sweeps |
| Reaches | the UI a person sees | the UI **and** the API underneath it, in the same file |
| Gates | the release (`docs/CONTRIBUTING.md` → Release process) | every push and PR (`.github/workflows/ci.yml`) |

Rule of thumb: **if you can describe it as a story ("a user does X, then Y, and
sees Z"), it is Playwright. If you can describe it as a rule ("this endpoint
answers 503 when the integration is off"), it is Cypress.** A regression in a
shared package usually deserves one of each - the contract in Cypress, the
journey in Playwright.

`e2e/README.md` and `web/cypress/README.md` carry the traps for each.

### Shards

The two longest suites, Playwright and Go, run in parts side by side - on
one machine or on many. A runner that splits them (the build host's chain in
`scripts/chain/`, a CI workflow) takes its parts from the two places below and
never from a list of its own, so two runners cannot split differently and
leave a test out.

**Playwright** splits itself:

```bash
node e2e/run.mjs local --shard 1/4      # then 2/4, 3/4, 4/4: together, the whole suite
node scripts/test-shards.mjs e2e-check --shards 4 --browsers chromium,firefox,webkit
```

- `--shard` is Playwright's own. It cuts the tests Playwright collected, after
  `--grep`, `--grep-invert` and `E2E_BROWSERS`, so the parts add up to the
  whole run by construction; it balances by number of tests; a spec file is
  never cut (with `fullyParallel: false` a file is one group); a part is a
  contiguous run of the sorted list. `e2e-check` lists the suite
  (`playwright test --list`: no server, no browser) whole and once per part,
  and is red unless every test is in exactly one part.
- Each part starts its own server on a free port with its own data directory,
  writes Playwright's output to `e2e/test-results/shard-i-of-N` (Playwright
  empties its output directory when a run starts, so two parts must not share
  one) and keeps the server log in `e2e/.artifacts/shard-i-of-N`. A part
  whose free port is taken by another before filex binds it starts again on
  another port (three tries; never when `--port` named it).
- A spec must still not depend on one that ran before it: in a part it may be
  the first.

**Go** has one list, `scripts/test-shards.json`:

- `internal/api/handlers` and `internal/wasmplugin` each outlast every other
  package (under `-race` on GitHub's runner at 0.55: about 215 and 78
  minutes). They are cut into units by **test file**, and a unit runs
  `go test -run '^(TestA|TestB|…)$'` with the exact names its files declare.
  The unit marked `rest` also runs every test file no unit lists: a new file
  is tested without an edit of the list.
- Every other package belongs to a package unit, and the package `rest` takes
  every package `go list ./...` knows that no unit names. The package units
  are cut by subject and weighed: `db`, `io` (storage, sync and the WebDAV
  and S3 servers), `auth` (the auth packages, and the SFTP, FTP and NFS
  servers with the sign-in they share) and the rest.
- A profile groups the units into jobs: `go` (plain `go test`, two handlers
  units a job, `io` + `auth` + rest in one) and `race` (`go test -race`, one
  unit a job, `db` + `io` in one). A profile runs every unit exactly once.

```bash
node scripts/test-shards.mjs go --profile race                  # the shards, as JSON
node scripts/test-shards.mjs go --profile race --format chain    # scripts/chain's list format
node scripts/test-shards.mjs go --profile go --format matrix     # {"include":[{"shard":…}]} for a CI matrix
mapfile -t A < <(node scripts/test-shards.mjs go --profile race --shard handlers-3 --format args)
(cd backend && go test -race "${A[@]}")                          # one shard
node scripts/test-shards.mjs go --profile go --node 3/8 --format args   # copy 3 of a CI job run 8 times
node scripts/test-shards.mjs check                               # the list against the tree
```

`--node i/N` picks the shard by position, for a CI that runs one job N times
side by side and numbers the copies (CircleCI's `parallelism`, GitLab's
`parallel`): the job holds a count, the list holds the shards. N must be the
profile's number of shards, or it is an error that names both - a job run on
fewer copies would leave the last shards out with every copy green. The
CircleCI config does exactly this for the `go` profile, and runs the
Playwright suite on its copies with `--shard i/N`;
`web/tests/deploy/circleciConfig.test.ts` holds its copy count to the list
on every web test run ([When GitHub Actions is down](#when-github-actions-is-down)).

GitHub runs one job per shard instead, named after it: `ci.yml`'s matrix
(Release process, step 7) takes its parts from `scripts/ci-parts.mjs`, which
reads the `go` and `race` profiles of this list and cuts Playwright into
`--shard i/N` parts of every engine, and gives each part the job name the
release reads it by. `node scripts/ci-parts.mjs list` prints every job of the
full matrix; `--profile pr` those of a pull request.
`web/tests/deploy/ciFullMatrix.test.ts` holds the workflow to it.

`check` is red when a profile's packages are not exactly what `go list ./...`
prints (a package left out, one that does not exist, one run twice), when a
test `go test -list` knows runs in no shard or in two, when a shard runs no
test or its regex is too long to pass as one argument, or when the list names
a test file that is no longer on disk. A test file the list does not name is
not red - it runs in its package's rest - and `check` names it, for the next
rebalance. `check --offline` needs no Go toolchain (it walks the tree for
packages); `web/tests/deploy/testShards.test.ts` holds the same against the
tree on every web test run.

**The split is cut from measured times.** ⚠ The longest `-race` shard is how
long ci.yml's full run takes, and that run is the release's land → green gate
(Release process, step 7; 50 minutes). 0.53's split was an estimate nobody
re-cut: by 0.55 the handlers' rest unit had taken every new test file (244 →
437 tests) and ran 57 minutes, the run 71-75. `scripts/test-times.json` holds
what GitHub's `-race` jobs measured - per test file of a cut package and per
other package, the median seconds over the runs its `source` names - and
how those seconds become a job's minutes on that runner (`model`):

```bash
node scripts/test-shards.mjs estimate             # the minutes each -race shard will take; red past 35
node scripts/test-shards.mjs rebalance            # the cut packages' file lists, from the times
node scripts/test-shards.mjs rebalance --write    # saved, with every unit's weight_s
```

A test file added since the measurement weighs its package's mean per test,
and runs in its package's rest unit until the next `rebalance`.
`web/tests/deploy/testShards.test.ts` is red when `estimate` puts a `-race`
shard past `RACE_BUDGET_MIN` (35 minutes: a `-race` job may also wait ~8
minutes for one of the organisation's 20 runners, and a slow runner takes up
to 30% longer than the median one the estimate is for). Red on a cut package: run
`rebalance --write`. Red on a package shard: move a package between the
package units by hand and run `estimate` again (a package moves whole, so
`rebalance` leaves those units alone), then `rebalance --write` for their
`weight_s`. Keep the number of `-race` jobs: one more is a runner fewer for
the rest of the run.

Re-measure when a release's `-race` shard ends far from its estimate. Each
`-race` job keeps its `-v` log as the artifact `race-<shard>` for 7 days:

```bash
for r in <run id> <run id>; do gh run download "$r" -R BRF-Tech/filex -p 'race-*' -D "race-logs/$r"; done
node scripts/test-shards.mjs times --from race-logs --source "GitHub Actions ubuntu-latest: the -race jobs of runs <ids> (<versions>)" --write
node scripts/test-shards.mjs rebalance --write
```

`times` keeps the file's `model`: set it by hand from a run's job minutes
when the runner changes (`$comment` says how it was measured). One log of a
cut package (`go test -json` or `-v`) can still re-cut that package alone:
`rebalance --package ./internal/api/handlers --from race-handlers.out
[--write]`. The chain's cut shards run a prebuilt binary and print no package
line: `times --package <pkg>` says whose tests they are.

### Against the real servers

Both suites stub or fake the servers filex talks to. A third one,
`e2e/realenv/`, runs filex against the real ones in Docker: an ACME authority
(Pebble) for `FILEX_TLS_MODE=acme`, Caddy for `FILEX_TLS_MODE=proxy`, Keycloak
and OpenLDAP for sign-in, the ONLYOFFICE Document Server for issue #80's S2
and S5:

```bash
e2e/realenv/run.sh            # every stage; or: tls, sso, office
```

Linux with Docker only. A stage whose images are not on the machine is skipped
with the image named (`REALENV_PULL=1` pulls them), and so is every stage on a
machine without Docker, so it is safe to call anywhere. What each stage
starts, and how the pieces trust each other, is in `e2e/realenv/README.md`.
A change to ACME, the TLS hooks, OIDC/LDAP sign-in or the document server's
diagnosis is measured there before a release. The nightly run (below) runs it
once a week.

### The whole chain on one Linux host

Everything above that runs on Linux, on one machine with Docker, in one
command - what a maintainer runs on a build host before a release:

```bash
bash scripts/chain/run.sh --plan                  # the jobs, the memory budget and the expected wall time; runs nothing
bash scripts/chain/run.sh --env ~/chain.env       # the full profile
bash scripts/chain/run.sh --profile targeted      # the build and its fast gates, then only what CHAIN_* asks for
```

**What runs.** The build (`build:all`, the echo app fixture) first. Then, side
by side:

- **the browser round**, one job at a time in the order of
  `scripts/chain/lists/e2e.txt`: Cypress; Playwright on Chromium, Firefox and
  WebKit with an ONLYOFFICE Document Server connected; the office specs
  without one; the store specs without `FILEX_PUBLIC_URL`; the S3 specs
  against a local gateway. A Playwright line may be one part of a split run
  (`--shard i/N`, Shards above): a part clears and keeps only its own
  `e2e/test-results/shard-i-of-N` and `e2e/.artifacts/shard-i-of-N`, and
  every line has a port of its own (`CHAIN_E2E_PORT` plus its place in the
  list) for the Document Server to call back on;
- **the pool**: vitest, vue-tsc and the desktop checks; the docs gates; the
  e2e typecheck; `shards-check`, which is `node scripts/test-shards.mjs check`
  (a test the shards leave out turns the run red); the Go shards next to
  PostgreSQL, MySQL, Redis and Samba; the migrations up, down three steps and
  up again on SQLite, PostgreSQL and MySQL through the real CLI; and the
  `-race` shards, where the -race binary of `internal/api/handlers` and of
  `internal/wasmplugin` is built once and every shard of it runs it.

The Go and `-race` jobs are the shards of `scripts/test-shards.json` (Shards,
above): the chain asks `node scripts/test-shards.mjs go --profile go|race
--format chain` for them, so it splits the Go suite exactly the way every
other runner does. Each job is named after its shard (`go-handlers-1-2`,
`race-handlers-3`, `race-rest`), and its expected minutes come from the
list's weights - GitHub's seconds, scaled to the host (`SHARD_TIMING` in
`scripts/chain/plan.mjs`: 0.8 for a cut shard, 0.67 for a package one). A
`-race` shard of a cut package runs `-v`, so its
`out/race-<shard>/race.out` is what `rebalance` reads: put a package's
shards' logs together (`cat out/race-handlers-*/race.out`) and re-cut it from
the times of a real run.

`--profile full` runs all of it; `nightly` runs the same plus its extras
in the browser round (The nightly run, below); `targeted` runs the build and
its fast gates, then only what is asked for (`CHAIN_GO_PKGS`,
`CHAIN_RACE_PKGS`, `CHAIN_MIGRATE=1`, `CHAIN_CY_SPECS`, `CHAIN_E2E_GREP` on
`CHAIN_E2E_BROWSERS`, and `CHAIN_EXTRAS` - nightly extras by name, such as
`s3-live`, or `shots` for a release's pictures, which `CHAIN_SHOTS_ONLY`
narrows to some shot scripts). `CHAIN_E2E_GREP` reaches Playwright's `--grep`
as one argument, spaces and all (a test title, or `01-smoke 02-`): a browser
line's arguments travel to its job one a line (split into words, as they
were before #244, every word after the first became a file filter).

**Memory decides what runs at once.** Every job is budgeted for the memory
its container was measured to hold on the build host (`WEIGHTS` and
`JOB_WEIGHTS` in `scripts/chain/plan.mjs`, from `result.json`
`load.mem_gb`: re-read them there when a job grows). The browser round holds
the share of its heaviest line still to run, plus the Document Server's while
a line still needs it: Firefox's 5.75 GiB until Firefox is done, then
WebKit's, and 1.75 GiB for the light lines after them. The share
only shrinks, so the round never waits for memory the pool took. The
databases hold theirs until the last Go or migration job ends, and the pool
takes what is left, at most `CHAIN_POOL_MAX` jobs at once. A job that does not
fit keeps its place: the lighter jobs behind it cannot take the memory it
waits for. A job that cannot fit beside the round at all (the web gates,
3.25 GiB) waits for the round or the databases to let go instead of starting
over the budget. A job also waits while the host's `MemAvailable` is under
its weight plus `CHAIN_MEM_RESERVE_GB`.

A Playwright line holds more than its browser: its `/tmp` is a tmpfs (below),
so the server's data and the specs' storages are memory, and one worker runs
every spec of the line. The Chromium line held 6.62-6.85 GiB on every night of
0.56, and 7.15 GiB at its end when it ran on its own, while it was budgeted at
4.25 - the nights stayed calm only because the budget, 8, was itself 2 GiB
short of what the host gave. Most of it was the worker, which kept what
recording every test left it: 1.5 GiB resident seven minutes in, 3.4 GiB
fifteen minutes later. The same line recording nothing held 4.15 GiB (the
worker 1.2 at its end) and ended in 23.5 minutes instead of 27.9.

**What a browser line records.** So the chain records a test only when it
fails: `CHAIN_E2E_RECORD=retry` (the default) runs its Playwright lines with
`E2E_RECORD=retry` (`e2e/helpers/record.ts`): nothing is recorded on a test's
first attempt, a test that fails runs once more with its trace and video
recorded, and the line is red all the same - a test that passes only on its
second attempt is flaky, and `failOnFlakyTests` makes that a failure. What it
gives up is the recording of the FIRST failing attempt (the screenshot of it is
still taken); `CHAIN_E2E_RECORD=failures` records every test again, for a run
chasing a failure that does not come back on a retry, and needs the memory
for it. A run on your machine and GitHub's keep recording every test
(`E2E_RECORD` unset).

The budget is `CHAIN_MEM_GB` (10), or less when the host has less to give: a
run takes `MemAvailable` at its start less `CHAIN_MEM_RESERVE_GB` when that is
smaller, and `chain.log` names the containers that are not the chain's and
what they hold. Another workload on the host makes a run longer, not red.
`web/tests/deploy/chainSchedule.test.ts` replays a full run with the measured
minutes through the same scheduler and holds the budget, the Go shards next
to Chromium and `-race` next to Firefox and WebKit, one heavy writer at a
time, the three-hour bound, and that every job runs on a smaller budget.

**One heavy writer at a time.** The build host's disk (an NVMe without its
own DRAM, nearly full) stops for seconds when two jobs that write a lot
fsync together, and everything that waits on it that minute - a login, an
upload, a test that waits a fixed time - waits with it. `IO_HEAVY_JOBS` and
`ioOf` in `scripts/chain/plan.mjs` mark the jobs measured to write gigabytes
in a run (`result.json` `load.written_gb`): the Playwright lines that run the
suite or a part of it (not one that a `--grep` narrows to a few specs),
`race-rest`, `go-io-rest`, `race-db-io` and `shots`; every other job of a full
run wrote under 1 GB (`race-auth`, cut from `race-rest` and `race-db-io` in
#174, is light until a night measures it). At most `CHAIN_IO_HEAVY_MAX` (1) of them run at once,
the browser round's line included. A heavy line of the round waits for the
heavy pool job running, at most `CHAIN_IO_WAIT_MIN` minutes, and from the
moment the round turns to it the pool starts no heavy job; a heavy pool job
waits without holding memory back, so the light jobs behind it go on, and
runs in a lane of its own beside the `CHAIN_POOL_MAX` light ones. In practice
the pool's heavy jobs run after the round's heavy lines, one after another:
a full run takes about half an hour longer. `run.mjs --plan` marks them in
its `disk` column, `chain.log` says when the round waits, and `result.json`
records each job's `io` and `host.io_wait_secs`. `CHAIN_IO_HEAVY_MAX=0` turns
the rule off.

**The host's pressure.** Every 5 s the run reads the kernel's pressure stall
information (`/proc/pressure`), the write latency of the disk it writes to
(`/proc/diskstats`; `CHAIN_DISK`) and each job container's cgroup. The pool
starts one job per `CHAIN_START_GAP_S` seconds, and none while memory or IO
"full" is over `CHAIN_PSI_MEM_MAX` or `CHAIN_PSI_IO_MAX` (for at most
`CHAIN_PSI_WAIT_MIN` minutes at a time). `chain.log` gets a `PRESSURE` line
while the host is stalled and a `LOAD` line per job. A Go test job's `/tmp`
is a tmpfs (`CHAIN_TMPFS_GB`), so a test's fsync never waits for the disk (the
Go build directory stays on the disk), and so is a Playwright line's
`e2e/test-results`: every test's trace and video is written there and deleted
when it passes, and a red test's are copied to the run. Firefox keeps its HTTP
cache in memory and no history database (`e2e/playwright.config.ts`), as a
Chromium context does: before that the Firefox line wrote 33-34 GB in a run.
A Playwright line's `/tmp` is a tmpfs of its own (`CHAIN_BROWSER_TMPFS_GB`,
6), and that is where everything the line wrote to the disk went: the
browser's profile, the filex server's data directory (its database, search
index and log) and the storages the specs make, and Chromium's shared memory
(Playwright starts it with `--disable-dev-shm-usage`, which puts its shared
memory in files under `/tmp`). Most of the 11.8 GB the Firefox line wrote was
the panel's service worker precaching the app into every context's storage,
50 fsyncs a second; on the disk a closed context's storage stayed in the one
profile, on the tmpfs Firefox empties it. The same 125 Firefox tests wrote
2.11 GiB with `/tmp` on the disk and nothing on the tmpfs, in 4 min 19 s
instead of 7 min 51 s. The shots job is the same harness and gets the same
`/tmp` (its run directory, with the binary it builds and runs, each scene's
server data and the browser's profile; 1.3-1.43 GiB a night on the disk);
its pictures stay in the tree. Before a Playwright line the round
waits for `CHAIN_TEMP_QUERY` to cool to `CHAIN_TEMP_MAX`, at most
`CHAIN_TEMP_WAIT_MIN` per line and `CHAIN_TEMP_WAIT_TOTAL_MIN` in a run. A
timeout in a run is first a question about the host: memory "full" of 0.3, or
a write that takes seconds, slows everything that runs that minute 20-60
times.

**The processors are shared by weight.** Every job may use `CHAIN_CPUS`
cores; when they are all busy, the browser jobs and the vitest job get four
times the share of a Go, `-race` or docs job (`docker run --cpu-shares` 4096
against 1024). Those two hold every test to a timeout of a few seconds of
wall-clock time and a Go test has no such limit, so they are the ones a busy
host turns red: next to the `-race` shards a vitest file that takes 0.8 s alone
took 15 s at the same weight, and one of its tests went over its 5 s. vitest
runs at most `CHAIN_WEB_WORKERS` forks (8): its own default is one per core
but one, 19 on the build host, where they held 3 GiB.

**The lock.** `run.sh` holds `CHAIN_LOCK` with `flock(1)` for the whole run -
the lock every other build on the host takes, so a chain and a build wait for
each other instead of sharing the memory. It waits for it as long as it
takes, or `CHAIN_LOCK_WAIT_MIN` minutes when the process environment sets
that, and then exits 3 without starting.

**What else the host runs.** A host that also runs something heavy - a
virtual machine, an Android emulator that holds 3-4 GiB - can hand it out of
the run's way. Once the lock is held, and before the run measures
`MemAvailable` for its budget, `run.sh` runs `CHAIN_PAUSE_CMD` with `bash -c`;
when that exits 0, it runs `CHAIN_RESUME_CMD` once when the run ends - green,
red, a setup error, or stopped (TERM, INT or HUP reach `run.mjs` as TERM, it
stops the run, and the resume runs after it), with the run's exit code in
`CHAIN_EXIT`. Both are commands on the host, one line each, and carry no
secret: a script there that reads its own settings. They come from the
settings file (`run.sh --print-hooks` shows them), so a hand run, a CI run and
the nightly run all take them, and run still under the lock, without its file
descriptor, in a session of their own (no terminal; a stop of the nightly run
does not cut them short), at most `CHAIN_HOOK_TIMEOUT_S` seconds each (900).
A pause that fails or times out does not stop the run: `chain.log` and
`result.json` `host.pause` say so, the run takes the smaller budget the host
leaves, and nothing is resumed. The resume gets no second chance from the
run: a resume script that cannot start what it paused says so itself.

**What it leaves**, under `$CHAIN_ROOT/runs/<run-id>/`: `chain.log`, a
`JOBSTART` and a `JOBEXIT` line per job; `logs/<job>.log`; `out/<job>/`, the
job's test output; and `result.json` - per job its name, start, end, seconds,
exit code, log, a one-line summary and its `load` (the most its container
held and wrote, and the worst memory and IO "full" and disk write while it
ran), plus the commit, the budget, the lowest `MemAvailable` the run saw and
`host` (the budget it took and why, what the pause hook did, and the worst
pressure of the run; the nightly report says it, and names a host stall beside
the red job it may explain). It is rewritten after every job and final
once `finished` is set; `runs/latest-<profile>.json` is the last final one.
With `CHAIN_NOTIFY_URL` set, the end of the run is posted there. The exit code
is 0 when every job is green.

`CHAIN_GO_LIST`, `CHAIN_RACE_LIST` and `CHAIN_E2E_LIST` replace the shards
and the browser list with a file or with `cmd:<command>`, whose output is a
list in the same line format - for a one-off run, never as a second list kept
beside `scripts/test-shards.json`. Every setting, with its default, is in
`scripts/chain/chain.env.example`.

### The private pipeline: embargoed branches and the last-resort gate

The maintainers' private repository has one CI pipeline, and it runs the chain
above on the build host's own runner - nothing else. It has no test definition
of its own: every job is `scripts/chain/run.sh`, so it cannot drift from what
a hand run tests. The pipeline file and the runner's setup stay in the private
repository; this is what they do.

**When a pipeline starts** - two ways, and no other (no schedule, no merge
request, no other branch, no tag):

- **a push to `sec/<name>`**, an embargoed security branch. A fix for a hole in
  a released version stays off `main` and off the public repository until its
  patch's release window (Release process, below); this is where it passes the
  full chain before that. A push option narrows a run
  (`git push -o ci.variable="CHAIN_PROFILE=targeted"`), and a newer push to the
  same branch supersedes a run still going. A push that skips the pipeline
  (`-o ci.skip`) does not skip the chain: the build host notices a `sec/`
  tip that no chain ever ran on and starts the whole chain on it within ten
  minutes, and the release train's merge queue refuses a `sec/` branch whose
  tip has no green chain - unless it is given the reason
  (`--embargo-unchecked "<why>"`), which goes into the merge commit as an
  `Embargo-Unchecked:` trailer (task #182: one embargoed branch reached
  `main` without the chain, its push skipped);
- **a run started by hand, or through the API, on `main` or a `sec/` branch**,
  with `CHAIN_PROFILE` (`full` or `targeted`) and the `targeted` variables
  (`CHAIN_GO_PKGS`, `CHAIN_E2E_GREP`, ...). This is the last-resort release
  gate when GitHub and CircleCI are both down: the job's `result.json` is what
  `pnpm release X.Y.Z --resume --chain result.json` takes.

**The nightly run is not a pipeline.** The build host's own timer starts it
([The nightly run](#the-nightly-run), below), and it is the night's one
trigger: a GitLab schedule beside it would run the same chain a second time on
the same host, under the same lock. So the pipeline has no schedule and no
`nightly` profile, and both the runner's gate and the job refuse a scheduled
pipeline.

**Where it runs.** Every job names the one runner tag of the build host's
runner itself, and that runner takes nothing else: a project runner locked to
the private repository, protected (protected branches only: `main` and
`sec/*`), no untagged jobs, one job at a time. A gate on the host, before a
job's code is fetched, refuses another project, a project that is not private,
an unprotected or other branch, a tag, a merge request, a schedule and any
source but a push, a run by hand or the API; a branch that edits the pipeline
cannot change it. The runner holds `CHAIN_LOCK` like every other build on the
host (the chain takes it), and the host, not the pipeline, decides the lock,
the root and the container prefix: a job that sets them is refused. Its jobs
spend none of the CI minutes the plan gives the shared runners, and none can
land on a shared runner.

**What it leaves**, as the job's artifact, `chain-result/`: `result.json`,
`chain.log`, the last lines of every red job's log, `summary.txt` and
`job.json`. The test output (`out/`, traces) stays on the host, under the
chain's root, as `runs/gl-<pipeline>-<job>/`; CI runs older than
`CHAIN_CI_KEEP_DAYS` (7) are removed by the next job, hand runs never.

**What never leaves**, so that an embargoed branch reaches no one before its
window:

- no artifact is public (developers of the private project only), and none
  carries test output;
- no cache is uploaded, and there is no Pages site, release, environment,
  image push, `include:` or child pipeline;
- on a `sec/` branch the chain posts nothing to the notification service,
  which forwards beyond the project (the post carries the commit subject);
- the public repository has no self-hosted runner of ours, and a `sec/` branch
  reaches it only through the release that ships the fix (Release process).

`web/tests/deploy/gitlabPipeline.test.ts` holds every rule above (the public
tree has no pipeline and skips it).

### The nightly run

The same chain runs every night on the build host, on the tip of `main`, so a
release day starts from a night that already ran everything and only what
changed since is left (`scripts/chain/nightly.mjs`):

```bash
sudo bash scripts/chain/install-nightly.sh --root /var/lib/filex-nightly \
  --env /etc/filex-nightly.env --remote <clone URL> --dry-run      # once; then without --dry-run
node /var/lib/filex-nightly/bin/nightly.mjs run --env /etc/filex-nightly.env --dry-run   # what tonight would do
node /var/lib/filex-nightly/bin/nightly.mjs status --env /etc/filex-nightly.env          # tonight and the last nights
node /var/lib/filex-nightly/bin/report.mjs --env /etc/filex-nightly.env --dry-run        # the morning report, printed
```

The installer gives the run a directory and a checkout of its own, writes the
settings file (`scripts/chain/nightly.env.example`; it never overwrites one)
and installs two systemd timers from `scripts/chain/systemd/`. The timers run
the copies it puts in `<root>/bin`, never the checkout the run resets every
night: run the installer again to update them.

**When it runs.** Its timer starts it at 01:00. It fetches `main` and:

- runs nothing when `main` has not moved since the last night that ran -
  unless that run did not finish (stopped, or a setup error);
- runs nothing inside a `NIGHTLY_QUIET` window, nor when the run - as long as
  the last one took - would not be over before the next window starts, and
  stops a run still going `NIGHTLY_QUIET_MARGIN_MIN` (15) minutes before one.
  Other work scheduled on the same host goes there: a weekly service-update
  round, for example. The windows are in UTC, as cron schedules them;
- otherwise checks `main` out clean (`git clean -x`, `node_modules` kept) and
  starts `run.sh --profile nightly` with its own `CHAIN_PREFIX` and
  Document Server subnet, under `CHAIN_LOCK` - the lock every build on the
  host takes, which the nightly settings must name: a release chain and the
  nightly run wait for each other. It waits for the lock only as long as the
  run could still end in time. `CHAIN_PAUSE_CMD` and `CHAIN_RESUME_CMD` in the
  nightly settings work as in any run (What else the host runs, above): the
  night's chain pauses what they name and resumes it when the chain ends,
  before the nightly build. The systemd unit gives a stop 10 minutes, so the
  resume runs after a stop too.

**What it runs.** The full chain, and in the browser round the extras no
other run has (`NIGHTLY_EXTRAS` in `scripts/chain/nightly-lib.mjs`;
`CHAIN_NIGHTLY_EXTRAS` picks some of them):

- `ds-go`: the Go test of the office engine against a real Document Server
  and the Convert app (`backend/internal/server/office_engine_ds_test.go`),
  which skips itself everywhere else. It runs right after the last browser
  line with the Document Server, while it is still up, with the Convert build
  `CHAIN_CONVERT_APP_DIR` names. A run in which it only skipped is red;
- `s3-live`: the Go tests against a real S3 provider - the provider
  conformance suite (`TestLiveProviderConformance`), the B2 report reader,
  issue #21's rename and the driver's `Init` - against the bucket the nightly
  settings name: `FILEX_TEST_S3_ENDPOINT`, `_REGION`, `_BUCKET`, `_ACCESS_KEY`,
  `_SECRET_KEY` (and `_PATH_STYLE=0` for virtual-hosted addressing; the build
  host's are a B2 demo account). The suites read them through
  `backend/internal/testutil/lives3`, region included: a server with a region
  of its own refuses a request signed for `auto`. **Only when the S3 code
  changed** since the last green `s3-live` (or, before the first, since the
  night the measuring started; `state.json`, `s3_live`): the driver
  (`storage/drivers/s3/`), the stall policy it wraps every transfer in
  (`storage/stall/`), the storage contracts it implements (`driver.go`,
  `object.go`, `tally.go`, `registry.go`, `descriptor.go`, `validate.go`),
  `testutil/lives3/`, the B2 reader (`usage/b2*`), the live tests and their job,
  and a `github.com/aws/` line of `go.mod` (`S3_LIVE_PATHS`). Unchanged, the
  morning report says `s3-live: skipped - S3 driver unchanged since <commit>`
  and nothing is red. Changed and the settings name no bucket, the job does
  not run either, and the report carries a **warning** that names the files
  and the command to test it by hand (below) - not a red night. When it runs,
  a test that only skipped is red, and only a green run moves the commit the
  S3 code is measured from: a change nobody tested is reported again the next
  night. The bucket's key reaches nothing else and is never given to GitHub;
  the values reach the job in a `0600` file of the run, never on a command
  line or in a log, and the job's output is written with the key and the
  secret blanked out;
- `shots`: `pnpm shots --all --keep-going` on the packages and the web build
  the chain made - every scene, whatever its digest says, so a scene whose
  `INPUTS` missed something it shows is caught the night it changed. The app
  scenes are taken too, with the app builds and the language packs the
  settings name (`CHAIN_SIGN_APP_DIR`, `CHAIN_CONVERT_APP_DIR`,
  `CHAIN_LANG_ES_DIR`, `CHAIN_LANG_DE_DIR`, `CHAIN_LANG_FR_DIR`), copied into
  the job before it starts, and the ONLYOFFICE scene against the chain's
  Document Server: the job runs right after `ds-go`, while that is still up.
  Without one of them the job is red before it builds anything, unless
  `CHAIN_REQUIRE_APPS=0` leaves those scenes out. A shot script that no
  longer fits the product turns a night red instead of a release day, and a
  failed scene does not stop the others. A language pack behind the tree
  (the strings the day added are translated after the night's run) fails
  `langpack.mjs`; when that is the night's only failed scene, the job is
  green with a **warning** in the morning report, not red - a release's run
  of the same job stays red (2026-10-08). Every picture is compared with the published set,
  which is taken right here - Linux, the chain's Playwright container and
  fontconfig - and the pictures, the review and the contact sheet are kept
  in the run's `out/shots/`;
- `realenv`: `e2e/realenv/run.sh` (Against the real servers, above), once
  every `NIGHTLY_REALENV_EVERY_DAYS` days (7). The one job that runs on the
  host itself and not in a container, since it starts containers of its own;
  it pulls the images it lacks, and a stage it still could not run is red.

The chain's own end-of-run notification is off in a nightly run: the morning
report is the night's one.

**The live S3 tests by hand.** When the S3 driver itself changes, or the
report warns that a change went untested, bring credentials for one run - a
provider's own test bucket, the demo account - in a file of
`FILEX_TEST_S3_*` lines and run, on the build host:

```bash
node /var/lib/filex-nightly/bin/nightly.mjs s3-live --env /etc/filex-nightly.env --s3-env /root/s3-test.env
```

It fetches `main`, checks it out in the nightly run's checkout and runs the
build, its fast gates and `s3-live` alone (`run.sh --profile targeted` with
`CHAIN_EXTRAS=s3-live`) under the build lock; the credentials reach the job
the same way as at night, and nothing logs them. Green, the nightly run
measures the S3 code from that commit, and the warning stops. Without the
nightly run, from any checkout on the build host:
`CHAIN_EXTRAS=s3-live bash scripts/chain/run.sh --profile targeted --env <a file with the chain's settings and the FILEX_TEST_S3_* lines>`.

**The morning report.** At 07:00 a second timer sends one notification for the
night (`scripts/chain/report.mjs`): green or red and the wall time against the
previous night; every red job with its one-line summary, its log, the night
it last passed and the commits since then; every warning a green job left
(a job's `JOBWARN` lines, such as the shots job's language packs behind the
tree), which makes the night a warning rather than green; what the previous
night had red that passes now; the nightly build; and, for a night that did not run, why. A
night that left no record at all is reported too, and a run still going is
waited for, at most `NIGHTLY_REPORT_WAIT_MIN` (120) minutes. It posts to
`CHAIN_NOTIFY_URL` with the key in `CHAIN_NOTIFY_KEY_FILE`, which it never
logs.

**The nightly build.** After a green night on a commit no nightly build was
made of yet, `scripts/chain/nightly-build.sh` builds the image of that commit
from its public form - the tree `scripts/export-public.sh` makes of it, with
the same rewrite and the same refusals as a release - and never from the
private tree. Its filex reports the next minor as a pre-release with the
commit as build metadata (`0.53.0-nightly.20261007+1a2b3c4d`: above every
0.52.x, below 0.53.0, so the update check offers 0.53.0 when it ships). A red
night, or a night `main` did not move, builds nothing. **Every green change
goes out**: the build host's settings say `NIGHTLY_PUBLISH=auto`, so the image
is pushed to `NIGHTLY_PUSH_REPO` (`ghcr.io/brf-tech/filex`) as `:nightly` and
`:nightly-<date>` right after the build - the first one too, nobody pushes it
by hand. It signs in with `NIGHTLY_REGISTRY_USER` and the token in
`NIGHTLY_REGISTRY_TOKEN_FILE` (both, or neither for the host's own Docker
login; one of them alone is refused). A push that fails turns the morning
report into a warning that names it, and `nightly.mjs publish` pushes that
build again. `manual` - the code's default, for a checkout whose settings name
no registry - only builds and tags, and leaves the push to `nightly.mjs
publish`; `off` never pushes.

What a nightly is can be read off it from outside (#266): its version names
the night and the commit (`0.57.0-nightly.20261014+2ca25999`), and so do its
labels - the commit (`org.opencontainers.image.revision`), the public commit its
tree was exported on (`sh.filex.nightly.public-base`, set by
`nightly-build.sh` once it fetched), the night's test result
(`sh.filex.nightly.tests`), the source and what the image is
(`nightlyLabels` in `scripts/chain/nightly-lib.mjs`). How a user tries one:
[INSTALLATION.md → Nightly builds](INSTALLATION.md#nightly-builds).

**The desktop side of a nightly build** (#266). On a night that made a
nightly build, `scripts/chain/nightly-desktop.sh` works from the same public
form, in a public clone of its own (`NIGHTLY_DESKTOP_PUBLIC_DIR`;
`scripts/chain/nightly-export.sh` is the export both share), every package
carrying one version (`desktopVersion`: the nightly's, cut to the Snap
Store's 32 characters when it would be longer):

- **the amd64 snap, for the Snap Store's `edge` channel** (`NIGHTLY_SNAP=edge`),
  every such night. electron-builder's own container (`NIGHTLY_DESKTOP_IMAGE`;
  Go mounted from `CHAIN_GOROOT`) leaves the x64 app unpacked with its amd64
  CLI (`scripts/chain/nightly-desktop-job.sh`, target `snap`), and
  electron-builder on the host packs the snap from it with snapcraft in LXD - a
  container has no LXD, and since 0.55 the snap is core24, built by snapcraft.
  `nightly.mjs` uploads it with the credential in `NIGHTLY_SNAP_CREDENTIALS_FILE`
  (the output of `snapcraft export-login`, 0600, in snapcraft's environment for
  that one command, never logged) through `scripts/chain/nightly-snap-edge.mjs`
  (`snapcraft upload --release=edge`), the script GitHub's arm64 runner uploads
  the arm64 snap with. A revision the Store holds for its review (`review`) or
  an upload that failed is a warning in the morning report, not a red night -
  and the morning reports after it keep saying so, a quiet night's too, until
  the public channel map shows `edge` serving that version (`snapEdgeState`,
  `state.json` `last_snap`). A snap asked for and not uploaded (no credential)
  is a warning as well, never a quiet skip;
- **the source of every other desktop package** (`NIGHTLY_DESKTOP=github`):
  the public tree as `filex-nightly-src.tar.gz`, taken before anything is
  built in it and pushed to `NIGHTLY_DESKTOP_SRC_REPO`
  (`ghcr.io/brf-tech/filex-nightly-src:nightly`, signed in as the image push
  is), its annotations the commit, the version and the night.
  `.github/workflows/nightly-desktop.yml` (packaging/ci/nightly-desktop.patch,
  below) looks every hour: a revision it has neither built nor tried (the
  `attempted` tag, so a source that fails is not tried every hour; `force`
  by hand) is built on GitHub's runners by the same
  `nightly-desktop-job.sh` - Windows x64 and arm64 (`windows-latest`), macOS
  (`macos-15`), Linux x64 and arm64 (`ubuntu-latest`, `ubuntu-24.04-arm`:
  AppImage, `.deb`, `.rpm`), unsigned - and the arm64 snap, packed with
  snapcraft in LXD on the arm64 runner and uploaded to `edge` with the
  repository's `SNAPCRAFT_STORE_CREDENTIALS` (without it: the line
  `skipped: no Snap Store credential`, not a red run). The packages go to ghcr
  as ONE OCI artifact, `filex-desktop-nightly:nightly-<date>` and `:nightly`,
  with `nightly.json` (the night's manifest:
  `scripts/chain/nightly-desktop-manifest.mjs`, `desktopManifest`) and
  `SHA256SUMS`; the last `NIGHTLY_KEEP` (7) nights stay. A row that failed
  turns the run red, and the night's manifest says which.

**The morning's follow-up** (`report.mjs`, before it composes the report;
`nightly.mjs follow` by hand). It asks the registry and GitHub where the last
source stands (`followVerdict`: built, still building, failed, not taken
within three hours). Built, it reads the night's `nightly.json` from the
artifact, makes the page from `site/nightly.src.html` (`renderNightlyPage`:
the night, the commit, a download per platform, the checksums, and that a
nightly is not supported) and runs `NIGHTLY_SITE_CMD` once per revision -
the repository's
`scripts/chain/nightly-site.sh --ssh <site host> --path <site directory>`,
which sends `nightly-site-remote.sh` to the site host on stdin: the site host pulls the packages from ghcr itself, checks them
against `SHA256SUMS`, puts the page beside them in `.nightly/<date>/` and
points `nightly` at that directory in one rename (`scripts/sync-site.sh`
never deletes either). And it reads whether `edge` serves the arm64 snap the
workflow uploaded. What is not as it should be is a warning in the report,
never a red night.

The desktop side waits for the build lock, keeps to the quiet windows
(`NIGHTLY_DESKTOP_EXPECT_MIN`, 30) and never turns a green night red: what it
could not do is a warning in the morning report.

**What it leaves**, under its `CHAIN_ROOT`: the checkout (`src/`); the runs
(`runs/`, the last `NIGHTLY_KEEP_RUNS` nights), where
`runs/latest-nightly.json` is the result a release can take as its `--chain`
evidence (Release process, step 6) when it is green and `main` has not moved
since the night; and `nightly/` - `tonight.json` (the night so far),
`history.jsonl` (one line per night, what the report compares with) and
`state.json` (the last nightly build, the last nightly snap, the last
source pushed and the last page published, the last realenv night, and `s3_live`:
the commit the S3 code is measured from).
`web/tests/deploy/chainNightly.test.ts` holds the decisions above and the
report's form.

### Known flaky tests

A part of the full matrix (`ci.yml`) is red when one of its tests failed
twice: with `CI` set, Playwright retries a failed test once (`retries: 1` in
`e2e/playwright.config.ts`), so a test that failed and then passed is
reported as `flaky` and its part stays green. The build host's chain sets no
`CI` and retries nothing; Go, vitest and Cypress retry nothing anywhere.

What follows is what the first full-matrix runs on GitHub measured: 0.53.0's
release day and the runs after it (runs 37596680800 to 37702037032, task
#173). When a part is red on a test listed here and on nothing else, re-run
its failed jobs (`gh run rerun <id> --failed -R BRF-Tech/filex`) before you
look any further. A red test that is not listed is a fault until a re-run of
the same commit passes it; then it goes on this list, with the run that
showed it. A row leaves the list with the commit that removes its cause.

**Red: failed on both tries in a run, passed in another run of the same code.**

| Test | Part | Runs | What fails |
|---|---|---|---|
| `158-sidebar-storage-order`: "sort by name writes a name order", then on the retry "the administrator drags a storage to the top of the Storages table" | `Playwright (webkit 2/4)` | red in 37613557303 | WebKit's network process crashed (see below): the page's loads failed at once with "WebKit encountered an internal error", the panel's row never came, and on the retry `GET /api/admin/storages` answered 401 - the session cookie lives in the network process and went with it. Since #199 WebKit runs without the service worker; the night runs measure whether this goes. |
| `109-notifications-non-admin`: "paging reaches rows the bell never showed" | `Playwright (webkit 1/4)` | red in 37624094959, green on its re-run | WebKit's network process crashed (see below): ten loads of `/drive/explore` failed within two milliseconds, the explorer's chunk with them, and the page stayed blank under "The server could not be reached"; the retry's `page.goto` timed out. Since #199 WebKit runs without the service worker; the night runs measure whether this goes. |
| `139-table-scrollbar-loop`: "the table as shipped settles in a pane its rows just fit" | `Playwright (webkit 4/4)` | red in 37606303144 | WebKit's network process crashed: the trace says so ("WebSocket network error: Network process crashed"), `page.goto` failed with "WebKit encountered an internal error", and the retry's `page.goto` timed out. Since #199 WebKit runs without the service worker; the night runs measure whether this goes. |

The three rows above are one fault, and it is the browser's: Playwright's
WebKit on GitHub's `ubuntu-latest` lost its network process for a moment
(measured in the traces of runs 37606303144, 37613557303 and 37624094959).
Every load in flight fails with "WebKit encountered an internal error", and
the cookies go with the process. In all three traces the panel's service
worker had registered on the sign-in page 2.4 to 3.3 seconds before (its
install fetches the precache, every script and stylesheet of the panel)
and the next page was loading: suspected, not proven, as the trigger.
Since task #199 WebKit's line runs without the worker (`serviceWorkers:
'block'` in `e2e/playwright.config.ts`; 164's installable-app test lets it
back in). Whether that ends the crashes is measured in the night runs that
follow: the rows stay on this list until those runs say so. Until then, a
WebKit part red on one of them alone is re-run.

The build host's chain measured the same fault with the worker blocked, so
the worker is not its only trigger: 0.55's full chain (`3521526c`),
`129-sign-box-name-and-dates`, "every box on the define step can be reached -
a phone". Right after the sign-in the next page's manifest load failed with
"WebKit encountered an internal error", its requests went out with neither
the session cookie nor the token, the server answered 401 and the page was
the sign-in page (the server had answered the same requests 200 a moment
before). The 1440x900 and laptop runs of the same test before it passed.

Left the list with the commit that removed their cause (task #199, and
task #260 for the first one):

- `179-app-permissions`, "taken from the role: not in the person's menu, and
  running it anyway is refused" (WebKit; red on both tries in 38398358691,
  the 0.56.3 gate, green on the re-run). The spec signed the person in
  through the form, which lands on the start page, and left that page for
  the explorer while it was still asking for things - the navigation WebKit
  drops its network process on (filex lesson #1541): the explorer showed
  "The server could not be reached" over a listing it never asked for, and
  the bell's count, cancelled under it, was logged as a 500. The spec now
  signs in through the API with the explorer as the tab's first page and
  waits for the app rows' answer, not for a quiet network; the product no
  longer raises the strip for a request the page cancelled or the browser
  dropped while the page was being left (core `lib/connection`), and the
  server logs a request its client gave up on as 499, not 500.

- `202-store-install`, "the session gone, a second link in that tab goes into
  no sign-in address" (Chromium; red in 37702037032, flaky in 37598598805
  and 37661356185). The panel's: a 401 sent it to the sign-in page while it
  still believed in the session, the sign-in page sent that reader on to
  Home, and Home's own 401 named `/home` over the store page's sign-in. A
  401 now asks the server first (`web/src/lib/sessionGone.ts`); e2e
  `231-session-gone-sign-in` forces the order the race needed.
- `163-explorer-trash-purge`, "an operator's Delete permanently removes the
  item from the trash" (WebKit; red in 37702037032, flaky in 37661356185).
  The explorer's: a live refresh of the trash landed at the right-click, and
  every listing of the trash cleared the selection, so the dialog's button
  found nothing to delete and closed. A reload of the trash on screen keeps
  the selection now; the spec's second test refreshes between the pick and
  the delete on purpose.
- `199-csv-onlyoffice`, "Edit opens it in ONLYOFFICE; the saved file is the
  same kind of CSV with the new value" (Firefox; red in 37606303144 and
  37661356185). The spec's: Ctrl+A in ONLYOFFICE's name box did not always
  select its text in Firefox, so "B2" went in front of the old "A1". The
  spec empties the box and reads the address back before the Enter, and
  waits for the cell to move and the document server to hold the edit
  instead of for fixed times.

**Flaky: failed once, passed on the retry; the part stayed green.** WebKit
unless another engine is named:

- `100-viewer-audit` (html, webp; odt and psd with the Document Server),
  `101-quicklook-hint`, `102-touch-tap-opens`, `109-notifications-non-admin`
  (two more of its tests), `114-language-pack` (the Label cell at 1280 px, in
  four runs), `115-tag-kinds`, `130-sign-fill-only-and-pins` (install),
  `135-version-where-a-person-finds-it`, `150-archives`,
  `155-new-document-any-name` and `168-drafts` (the 390 px checks),
  `156-driver-time-limits`, `157-plugin-surface-theme`,
  `158-sidebar-storage-order` (sort by name; Move down; no sideways overflow
  at 1280 px), `164-sub-path`, `173-e2e-single-file` (the 200 MB stream file),
  `174-e2e-encrypt-existing`, `175-app-interface-sandbox`, `180-thumbnails`,
  `195-office-thumbnails`, `197-e2e-policy-approval`, `207-admin-search`,
  `50-search`, `60-user-settings`, `83-meta-and-markdown`, `95-app-plugins`;
- `159-queued-rename-restore` and `172-e2e-names`: a folder listed empty for
  a moment after a queued rename. Suspected: the queue's rename and the
  storage scan racing over the same rows (the build host's chain caught it
  too);
- `202-store-install`: "someone who is not an administrator" (Chromium) and
  "a repository serving other bytes" (WebKit);
- Chromium: `197-e2e-policy-approval` (a folder that holds something is
  encrypted in place); Firefox: `190-open-while-relisting`.

**Red once and fixed: not flaky.**

- `Go -race (handlers-5)`, 37596680800: the shard passed (`ok`, 736 s) and
  its step failed. GitHub runs a `run:` block under `bash -e`, so a `grep`
  that found no failure line ended a green shard red; the step runs with
  `set +e` since.
- `Go -race (handlers-7)`, 37598598805: `panic: close of closed channel` in
  bleve's `Close`, from a search rebuild that ended after the index was
  closed (it hit `TestSearchEndpoint_ForgivingNames`). Fixed in
  `internal/search`: a rebuild ending after `Close` no longer closes the index
  a second time (`close_rebuild_test.go`).
- `Playwright (chromium 2/4)`, 37610419458: three `173-e2e-single-file` tests
  could not import `packages/core/dist/filex-core.js`. The e2e build artifact
  carries it now.
- `Go -race (rest)`, 37613557303: `loginguard`'s
  `TestTheLoopSweepsWithoutTraffic` read the audit log between the row and its
  entry. It waits for the entry now.

**Not a test.** In run 37661356185 two parts (`chromium 4/4`, `firefox 4/4`)
hung for 44 minutes in `playwright install --with-deps`, on `apt-get update`,
and were cancelled at their 45-minute limit; the run took 69.7 minutes.
The install step has a 10-minute limit of its own since (task #199,
`packaging/ci/ci-full-matrix.patch`): a hang ends there. Re-run the part.

The chain on the build host has reds of its own under load, and they are no
flaky test: a timeout at a moment the host stalled (memory or disk) is the
host's. Look at the host first (The whole chain on one Linux host, above).
What a stall exposes is a fixed wait in a spec, and the fix is to wait for the
condition: 0.55's full chain (disk writes up to 4.7 s) made a draft's create
take 5.8 s against `168-drafts`' 5 s for the dialog to close, and a folder's
listing 1.2 s against `172-e2e-names`' 700 ms after the double click (the
upload went into the folder still on screen). Both wait for the server's
answer now.

### What needs tests

- **Always**: every new HTTP endpoint, every new storage driver method,
  every config knob.
- **Encouraged**: new UI components (Vitest `mount`).
- **Optional but appreciated**: an end-to-end scenario when the flow spans many
  components - see the table above for which suite it belongs in.

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
- A package's build (`pnpm run build:packages`) fails on any TypeScript
  diagnostic its declaration build (vite-plugin-dts) reports, through
  `afterDiagnostic: failOnDtsDiagnostics(...)` from `scripts/vite-dts-strict.mjs`
  in every `packages/*/vite.config.ts` that writes `.d.ts` files. That build
  types `.vue` files through its own `@vue/language-core` and finds errors
  `vue-tsc --noEmit` does not (a `useSlots()` loop shipped `DataTable`'s slots
  as `any` with vue-tsc green); fix the cause rather than loosening the hook.
  `web/tests/deploy/dtsStrict.test.ts` fails on a `dts()` without it.
- Prefer composables for reusable logic; SFC for components.
- No default exports (named only) - easier IDE refactor.

### Do not write the same logic twice

Anything repeated is added in one place and managed from one place. This is not
a style preference - it is the rule this codebase has broken most often, and
every time it was found by a person looking at a screen: a 464-line second
listing pane, one `mime_type === 'inode/storage'` test in three view
components, the brand mark hand-typed into five files (one still painted the
pre-rebrand indigo), two byte formatters that round differently on the same
screen. The second copy always gets written because the first is inconvenient
to reach, nothing notices, and the two drift.

**The gate:** `web/tests/quality/duplication.test.ts`, running
`scripts/dup-scan.mjs`. Run it yourself with `node scripts/dup-scan.mjs` - it
prints a ranked report and takes about two seconds. It checks three things:

| | what it catches | threshold |
|---|---|---|
| **fragments - verbatim** | a block copy-pasted with its names intact | ≥ 100 contiguous tokens (~15 lines) |
| **fragments - renamed** | a block re-typed, or copied and adapted, so no name matches | ≥ 140 tokens of identical structure, ≥ 14 distinct keywords/operators |
| **concepts** | a second implementation of something that has one home - byte formatting, date formatting, the storage-row test, the logo | any occurrence outside its home |
| **listing surfaces** | a component that renders the view components but hand-rolls the breadcrumb / filter row / view switch | any missing shared piece |

Scope is `packages/core/src`, `web/src`, `backend/internal`, `desktop/src`.
Tests, locale catalogues, generated files and build output are out of scope.

**When it fires: extract the shared thing and call it from both places.** That
is the answer nearly every time, and it is usually smaller than it looks.
Adding a second copy *and* an allowlist entry is not an answer - it is the
failure this gate exists to stop, written down.

**To declare a legitimate twin,** add an entry to the right register in the
test file, with a reason **about this code**. "Known issue" is rejected by the
gate; so is anything under 60 characters.

- `LEGITIMATE_TWINS` - the duplication is the design and will not be removed
  (the Postgres and SQLite drivers are two dialects of one interface). No
  ceiling: these grow on purpose.
- `KNOWN_DUPLICATION` - debt. Real, pre-existing, owed. Carries a `maxTokens`
  ceiling, so the area cannot quietly grow a *bigger* copy than it already has.
- `CONCEPT_EXEMPTIONS` / `COMPOSITION_DEBT` - the same, per concept and per
  surface.

**The registers are kept honest by going stale loudly.** An entry that no
longer matches anything *fails*. So when you fix a duplicate the build turns
red and tells you to delete its entry - that is intended, and the fix is one
line. An allowlist nobody prunes becomes the place the next duplicate hides.

Two limits worth knowing, so you do not mistake silence for absence: the
fragment passes only see duplication that is still *shaped* like the original
(a re-implementation with a different structure - which is what the old
`SecondaryPane` was, before one `FilePane` replaced both halves of the split -
is caught by the listing-surface rule instead, not by tokens), and Vue
`<template>` and `<style>` blocks are not scanned at all.

### UI rules

#### One table - the explorer's - and nothing else draws one

**filex has exactly one table: `DataTable`
(`packages/core/src/components/DataTable.vue`), which is the explorer's own
list view with the files taken out of it.** The explorer's listing renders
through it, and so does every other table in the product - every admin page,
the connection panels, My shares, the notifications list, an archive's or a
spreadsheet's preview, and an app's `list` node. **If something is tabular, it
is a `DataTable`.** No `<table>`, no table roles, no copy of the `fe-list`
markup, no second table component - anywhere in `web/src` or
`packages/core/src`.

The owner, 2026-09-21: *"Artık explore tablomuz bizim her yerde kullanacağımız
tablo yapısıdır; bir yere tablo gerekiyorsa bu tabloyu koymak zorundayız. Bunu
kural olarak yazalım, çok önemli bir kural."* ("From now on the explorer's
table is the table we use everywhere; wherever a table is needed, this is the
table that goes there. Write it down as a rule - a very important one.")

**Why it is a rule and not a preference.** The round before it built
`ui/Table.vue`: *one* admin table, with the explorer's frozen edges and its
Actions menu - an **imitation**. It had exactly the parts somebody remembered
to copy. Resizing a column, sorting, the column menu, reordering and
remembering the arrangement all live in the explorer's code, and none of them
reached the admin panel. Nothing failed; the owner opened the Users page and
found he could not widen a column or sort it (*"Admin tabloları hâlâ explore
tablolarıyla AYNI KODDA DEĞİL … tablo sütunları düzenlenebilir değil, büyütme
küçültme yok, sıralama yok"*). The same code gets everything the explorer can
do, and keeps getting whatever is added to it next. A look-alike never does.

What every table gets, whoever draws it:

- **resizable columns** - drag the edge, arrow keys on the focused handle,
  double-click for the shipped width;
- **sorting** - click a header, click again to reverse. ⚠ Over one page of a
  server-paged list the headers **close and say why** instead of re-ordering
  25 rows of 300 and calling it sorted; a caller whose server can sort passes a
  controlled `sort` and handles `@sort`;
- **the column menu** - the header's ⋮ or a right-click on the header: show,
  hide, step left/right, reset; plus dragging a header to move its column;
- **remembered** - per table, on the person's account (`table-id`, stored in
  the per-person view document under `t`). The explorer's listing is
  remembered per folder instead;
- **the frozen lead and ONE Actions control** - the column that says which row
  this is stays on the left, the row's verbs are one labelled control on the
  right (`:row-actions`), and the table scrolls sideways rather than dropping a
  column. ⚠ Either is frozen only while it leaves room to scroll: a sticky
  cell carries an opaque ground, so in a pane it cannot spare (an app's list
  in the details panel is ~265px) it would cover the very columns it was
  frozen to keep company. Below that nothing is pinned, the row scrolls as
  one piece, and the lead falls to its own minimum instead of taking the
  whole pane - still without hiding anything;
- **stacked rows where it cannot be a table** (0.56.1) - in a pane narrower
  than the lead and the first three columns side by side at their own
  minimums (`TableLayout.stackBelow`, `lib/tableColumns`; a phone, the
  details panel), every row is its tick, its lead and its actions on one
  line and every other cell after them, COMPACT: on one line under the lead
  that wraps only when it must, each value with its column's name small in
  front of it and " · " between them - "Type Plain text · Owner You ·
  Size 10 B", whole, the way a phone's file manager draws a row. The table
  decides from its OWN width (its scrollport, watched), not the window's,
  and goes back to being a table when the pane widens. The rows are the
  same rows - the same cells in the same order with the same roles; the
  label (from `data-label`) and the " · " are CSS with empty alternative
  text, so neither is part of a cell's text nor read out, and a value is
  still named by its column header - so selection, the right-click menu,
  the Actions control, the keyboard and a test that reads a cell keep
  working; the header keeps sorting and the column menu, and drops the
  resize handles and header dragging, which mean nothing without columns.
  Stacked, the header is a band of its own that leads with "Sort by" (from
  `data-sort-label`, CSS with empty alternative text) and draws each key as
  a button, the active one filled; a column that cannot sort leaves the
  band but keeps its header for a screen reader (0.57, task #264: it read
  as a line of plain text over the rows' facts). A column with no header
  label (the explorer's star) is a CONTROL: it stays on the first line,
  and where it has nothing to control it draws nothing - never the facts'
  "-" (0.57, task #249: Vue draws a slot's fallback when the slot renders
  only a `v-if` comment, so every folder row carried a hidden dash beside
  its ⋮). Nothing for a page to do: a `DataTable` stacks by itself. Owner, 2026-10-11, after the e-Signature panel cut its signers'
  State and When to "Invited ..." and "until S...": the one table lays its
  cells out under the lead in a narrow place, compact, every narrow table
  at once.

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
  the slot is a margin on a flex ITEM - it does not start a line, it pushes
  the box down ON TOP of the one beside it (v0.43.0 QA: the Apps table's
  Label cell drew the "Language pack" badge over the label and the coverage
  line over the badge, at every width). `.tbl-sub` as a direct child is the
  one shape the stylesheet handles on its own.
- A pill in a cell (a status, "Installed", "Waiting", a role, a source) is a
  **`tbl-pill` with its words in a `tbl-pill__text`** - the panel's `Badge`
  carries both on its own, so a `Badge` needs nothing more. In a column
  narrower than the pill, it keeps its border and dot and its words end in an
  ellipsis inside it, and the table gives the whole text as its title while
  it is cut. A pill drawn by hand (a `rounded-full` span) takes the two
  classes too, or its words run out through its border at a phone's width
  (the 0.55 store screen: "Bekliyor" over the edge of its own pill). Do not
  make the cell grow to fit the pill instead: a cell is its column's track,
  shared with the header and every other row. A header too narrow for its
  name ends in an ellipsis the same way, with the whole name in its title.
- A list the server pages: pass `:page`, `:page-size` and `:total` (or
  `:pages`). ⚠ Pass `:total` even when there is **no pager** and the endpoint
  answers "the first N of M" - that is what tells the table the rows on screen
  are not the whole list.
- Language and light/dark reach every table from the host once (`TABLE_ENV` -
  `web/src/lib/tableEnv.ts`, and the explorer provides its own); a page does not
  pass them. A unit test that mounts a page on its own provides `TABLE_ENV` if
  it asserts translated table text.

**The gate:** `web/tests/ui/tablePinnedActions.test.ts` scans both trees and
fails on a raw `<table>`, on table elements, table roles or the `fe-list` table
markup outside `DataTable.vue`, on a `DataTable` without a `table-id` or with a
duplicate one, on a `RowActions` drawn anywhere but inside the table, and on a
`#cell-*` slot whose second root node carries a top or bottom margin (the
overlap above). There
are **no exemptions** - the earlier version of this rule let the file previews
keep tables of their own "because they render foreign content", and an
exemption list is where the next second table hides.

#### No native dropdown - one list control, in core

**filex draws no native `select` element, anywhere.** A choice is one of two
controls, both in `packages/core` (exported from `@brftech/filex-core`):

- **`ChoiceButtons`** - two to four answers, every one readable without a
  click. Its default look is a row of buttons, each with an optional second
  line (`help`); with **`segmented`** it is one strip with the chosen cell
  filled, the shape for short answers sitting inline beside other fields (a
  unit after a number, a filter in a toolbar, the sharing dialog's access
  level); with **`iconOnly`** the cells show only their option's `icon`, the
  label being the accessible name. A segmented cell's `help` is its meaning
  on hover and on keyboard focus (the header's hover-tip look,
  `.fe-toolbar__soon-tip` - not a tooltip of its own), its
  `aria-describedby`, and - on a screen that cannot hover - one small line
  under the strip for the chosen cell.
- **`ChoiceSelect`** - a longer list, or one whose length the data decides
  (storages, people, buckets, a plugin's options). A combobox and a listbox in
  filex's own palette; `v-model` hands back the chosen option's own value (a
  number stays a number); `options` are `{ value, label, help?, disabled? }`.
  In `web/src`, use **`ui/Select`** - the admin panel's labelled frame (label,
  hint, the error in the panel's words) around this same component.

The owner, 2026-10-04: *"hiç bir yerde öyle basit bir dropdown kullanma"*
("don't use a plain dropdown like that anywhere"). A native select opens the
operating system's list: it ignores the palette, opens white over a dark
panel, is a different control on every platform and a sheet nobody designed
on a phone. Both controls take the dark mode, the chosen theme, right-to-left
layout and a 44px touch target with them, wherever they are drawn.

What `ChoiceSelect` does, so a page never has to:

- **keyboard** - ArrowDown / ArrowUp / Enter / Space open; the arrows, Home /
  End and PageUp / PageDown move; typing jumps to the next label that starts
  with what was typed; Enter or Space choose; Escape closes the list without
  a change and is not passed on (the dialog around it stays open); Tab
  chooses and moves on. A disabled option stays listed and is skipped.
- **where the list goes** - teleported to `<body>` (or into the open
  `<dialog>` the field sits in), under the field or above it when there is no
  room below, from the field's inline-start edge, inside the window
  (`lib/listPlacement`), on the layer `lib/popupLayer` measures, wearing the
  field's computed `--fe-*` tokens; a press outside closes it.
- **forms** - `name` and `required` put a hidden input in the surrounding
  form; a refusal suppresses the browser's bubble, marks and focuses the field
  (the first refused field of a form, as `web/src/lib/formCheck` does) and
  emits `invalid` for the host to word.

**In a test**, a list is driven the way a person drives it: open it, click
the option. `web/tests/helpers/choiceSelect.ts` (`pickOption`,
`listedOptions`, `chosenValue`, `listOffering`) and its e2e twin
`e2e/helpers/choiceSelect.ts` do it; `setValue()` and Playwright's
`selectOption()` only ever drove the native element.

**The gate:** `web/tests/ui/noNativeSelect.test.ts` scans `packages/core/src`
and `web/src` and fails on a native select element in a template, in markup a
script builds, or through `createElement` / `h()` - comments excepted. There
is no exemption list.

#### A service that is not there: disabled with a reason, or not offered

For an action that needs an optional external service - ONLYOFFICE, draw.io,
the converter, outgoing mail - that is not configured (or not answering):
**an administrator sees the action greyed, with a sentence that says what is
missing and where to set it up; everybody else is not offered it at all.**
Nobody is ever shown a raw HTTP status or a JSON body. The owner, after
`Config fetch 503: {"error":"onlyoffice not configured"}` reached a person who
had clicked Open on a `.docx`: *"disabled with a reason for administrators,
hidden for everybody else."* Use `gateOnService()` (`packages/core/src/lib/
serviceGate.ts`); "is this person an administrator who could fix it" is the
server's answer (`capabilities.caller_admin`), never a role guessed in the
browser.

The one exception is a switch that waits for a **tenant's own setting**, not
for a service of the instance: the two encryption request switches in the
settings dialog (`e2e.request_created`, `e2e.request_decided`) wait for the
tenant's encryption policy, which the tenant's own administrators set. There
the person who could fix it - and who a new request is sent to - is an
administrator *account*, not `caller_admin` (the supertenant's alone on a
multi-tenant install, which would leave a tenant's own administrator out).
Since 0.54 the server says it per event: `capabilities.event_off[event]
.fixable` (`backend/internal/api/handlers/capabilities_rules.go`
`eventsOff`), and the dialog reads that instead of the account's role.

#### A rule the server applies is the server's (0.54)

A decision, a number, a rule or a sentence the server applies is made on the
server and only shown by the clients: the server publishes it (a capabilities
field, a field on each row, an endpoint) and the client reads it. A client
that keeps its own copy drifts the first time one side changes - in 0.54's
audit the preview offered Edit on `.graphql`, which save-text refused, and the
Add user form spelled `gözlük@…` as `g.zl.k` where the server's rule gives
`gozluk`. Where a rule cannot travel as a value because the client applies it
while a person types or picks, its cases go into
`backend/internal/api/handlers/testdata/rule-mirrors.json`, and both sides
test against that file.

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
| A GitHub (or other code) repository | repository | repo | depo - that is a storage |
| Deleted files, until they expire | Trash | Çöp kutusu | Çöp Kutusu, çöp |
| What something is called | name | ad (display name: görünen ad) | isim |
| What a person signs in with | password | parola | şifre (şifreleme is *encryption* and stays) |
| The first screen of the file manager | Home | Ana sayfa | Giriş |
| Starting a session | sign in / sign-in | oturum aç / oturum açma | log in, login, giriş yap |
| Ending it | sign out | oturumu kapat | log out, çıkış yap |
| The short name a person signs in with | username | kullanıcı adı | login name, giriş adı |
| An electronic mail address, or a message | email | e-posta | e-mail, mail |
| filex reading a storage to bring its catalogue up to date (Sync runs, Sync now, Last sync) | sync | senkron (noun), senkronize et (verb) | eşitleme, senkronizasyon |
| The desktop app keeping a copy of folders on a computer, both ways (folder sync, "Syncing…") - and any other tool that does the same | folder sync, sync | klasör eşitleme, eşitle | klasör senkronu, senkronizasyon |
| A replica write mode: wait for the replica, or don't | synchronous / asynchronous | eşzamanlı / eşzamansız | Sync / Async, senkron / asenkron |
| When a file last changed (column, filter, sort, details) | Modified | Değiştirilme | Tarih, Değiştirildi |
| Who a file belongs to (column and filter) | Owner | Sahibi | People, Kişiler |
| An address that opens a share | link | bağlantı | link (in Turkish) |
| Narrowing a list | filter | filtre | süzgeç |
| The named set of permissions every person has exactly one of | role | rol | rule, kural, group |
| The three roles filex ships (Administrator is locked; User and Viewer are edited on Roles) | built-in role: Administrator, User, Viewer | yerleşik rol: Yönetici, Kullanıcı, İzleyici | default role; Görüntüleyici (a file *viewer* is "görüntüleyici", in lower case) |
| A role an administrator made. Its name is theirs and is never translated | custom role | özel rol | rule, kural |
| One thing a role allows (Download, Rename, Share links…) | permission | izin | yetki, hak |
| An Allow or Deny an administrator sets for one person alone, beating their role | exception | istisna | override, geçersiz kılma, "set for this account" |
| What a permission check answered, beside each permission on a person's page | Allowed / Denied | İzin var / İzin yok | Allow / Deny - those are the buttons that set an exception |
| A ready-made set of ticks (Standard user, Read-only, Upload-only, Guest) | preset | hazır ayar | template, şablon |
| One file or folder opened to a person (Share → People), and the admin page listing them all | grant; the page is **Folder access** | yetki; the page is **Klasör erişimi** | permission, izin - since 0.49 those are a role's words |
| Whoever runs a multi-tenant filex: the supertenant's administrators, who set each tenant's ceiling | platform operator | platform işletmecisi | service provider, hizmet veren, hizmet sağlayıcı, platform operatörü, platform yöneticisi |

**Spelling is American English.** color, license, favorite, center, gray,
behavior, organize, analyze, catalog, defense, customize - never colour,
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
says "Tekrar deneyin", "hesabınızla", "görebilirsiniz" - never "Tekrar dene",
"hesabınla", "görebilirsin", never "sen". A command - a button, a menu item, a
placeholder - is the bare verb, as every Turkish interface writes it: "Kaydet",
"Yeni sekmede aç", "Ara…". That is not the "sen" form, and it is not changed.

**Turkish is written with its own letters** - ı İ ş Ş ğ Ğ ü Ü ö Ö ç Ç - in
every string, examples and sample folder names included: "Arşiv", never
"Arsiv". (0.49's role editor shipped "ör. Arsiv veya
Musteriler/*/Sozlesmeler".) The gate knows the words interface text keeps
reaching for; the rest is review.

The machine-checkable part of this table is `web/tests/i18n/vocabulary.test.ts`:
it reads every catalogue - explorer, admin, and the server's `server.*` text -
and fails on a word from the right-hand column. A technical name that is
somebody else's (the OIDC *token* endpoint, an HTTP header, a webhook target's
*Bearer token*) is listed there by key, with the reason. Add a row here and a
pattern there together.

#### A person is named one way

Wherever filex shows a person - the Owner column, the details panel, the share
dialog, the account menu, an admin table, a notification - it prints **their
display name, else their username, else their email address**. In the browser
that is `personName()` (`packages/core/src/lib/personName.ts`); on the server
it is `model.PersonLabel` (`backend/internal/model/user.go`), which the Owner
column's name lookup and every row that names a person use. An email address
is shown as the name only when an account has nothing else; where it helps (an
admin table, a tooltip) it is the second line, never the first.

#### Dates and numbers: the explorer's format, everywhere

A date a person reads is the explorer's: "Sep 21, 2026, 2:50 PM",
"21 Eyl 2026, 14:50" - `formatWhen()` from `@brftech/filex-core`, in the
viewer's language and the viewer's chosen time zone. The admin panel's
`formatDate()` (`web/src/lib/format.ts`) and every share line call it; nothing
builds its own `Intl.DateTimeFormat`. A byte count is `formatByteSize()`, with
the catalogue's unit words (`unit.*`) and the viewer's number format, so
Turkish reads "1,96 KB" and French "1,96 Ko". ISO 8601 is for machines only: a
log line, an export, an API document - never a label.

### General

- **Line endings are LF, and `.gitattributes` enforces it** - you do not need to
  set `core.autocrlf`, and setting it will not override the repository. Every
  text file is stored and checked out LF on every platform; `*.bat`, `*.cmd` and
  `*.ps1` are the deliberate CRLF exceptions and `e2e/fixtures/**` is never
  converted in either direction, because those bytes are what the file-type
  suite is testing. This is not cosmetic: a `.sh` file checked out with CRLF
  fails on Linux and under WSL with `/usr/bin/env: 'bash\r': No such file or
  directory`, which is what the repository shipped until 2026-09-05.
- **A script or test that starts a process keeps it off the screen on
  Windows.** There a child that finds no console to share is given a new,
  visible one: a window on the desktop of whoever runs the tests, which can
  take the focus while they type. A detached process has no console at all,
  so every console program it starts opens such a window, and `windowsHide`
  on the detached process does not reach it. So `detached` depends on
  `process.platform` (a process group of its own on Linux, the parent's
  console on Windows) and comes with `windowsHide: true`, and every launch the
  parent does not wait for (`spawn`, `execFile`, `exec`, `fork`) in
  `scripts/` and `web/` carries `windowsHide: true` too.
  `web/tests/quality/hiddenConsoleWindows.test.ts` reads the source for both
  (#197; a script that stops anywhere but Linux before its work, like
  `scripts/chain/`, is left out).
- ASCII characters by default. Add comments in English even if the codebase
  is bilingual.
- No `console.log` left over - use `import.meta.env.DEV` guards in dev-only
  code paths.

### Adding a permission

Every permission says what the keys and roles that existed before it hold -
it is decided when the permission is added, never by accident at the upgrade.
filex has two kinds:

- **An API key's permission with a level** (`backend/internal/tokenperm`;
  today `comments`, read or read and write). Declare it in the catalogue with
  its `Levels` and its **`DefaultLevel`**, and add its line to
  `shippedDefaults` in `tokenperm_test.go`. The default is **`read`**: every
  existing key holds it the moment the version starts, with no migration, and
  so does a new key minted without choosing. The one exception is a
  super-administrator kind of permission - administering the server, tenants,
  system settings: mark it `Superadmin` and its default is **none**. A shipped
  default never changes (a key that never named the permission would change
  with it); a permission whose default must change is a new permission.
  `tokenperm_test.go` is red for a declaration without a `DefaultLevel`, a
  default that breaks the rule, or a shipped default that changed. A kind of
  token that should hold more (the desktop pairing's `comments:rw`) is given it
  where it is minted, with the reason written there, plus a migration for the
  tokens of that kind that already exist - never by raising the default. Then
  ask it in the handler every door runs (`auth.AllowTokenPerm`), list its MCP tools in
  `fileToolPerm` (`handlers/ai_mcp.go`), its routes in `tokenPermRoutes`
  (`token_verbs_test.go`) and `x-filex-token-permission` in `openapi.json`, and
  write it into [RBAC.md → Permissions with a
  level](RBAC.md#permissions-with-a-level-comments).
- **A role permission** (`backend/internal/perm`; `comments.write`,
  `files.encrypt`): one action, allowed or not. A saved role is given it at the
  upgrade only when it is carved out of one the role already allows
  (`inheritOnAdd`), and an administration permission (`admin.*`) never -
  [PERMISSIONS.md → Things to know](PERMISSIONS.md#things-to-know).

---

## Compatibility window for plugins and apps

What a storage plugin or an app sends filex - a build signature, a manifest
field, a host function, a call of the interface bridge, a field of the storage
protocol - is a contract with code this repository does not build. Nobody
should have to rebuild a plugin for every minor release, and nothing they
built should stop working without notice. So when you replace one of these
with a newer form:

- **Keep accepting the old form, with a warning, for at least 6 minor
  releases or 12 months, whichever is longer**, counted from the release that
  first warns about it (normally the one that ships the replacement). A plugin
  or an app published, built or signed for that release then runs unchanged
  through the window. The warning goes where the administrator looks (the
  plugin's or the app's row, its log, the audit row) and names the end of the
  window; a sentence a person reads is the server's (`srvtext`) and takes the
  end as `{version}` and `{date}`.
- **Register the window** in `backend/internal/compat/windows.json` in the same
  change: `id`, `form` (one sentence), `since` (`MAJOR.MINOR`), `since_date`
  (that release's date) and `docs` (the page and anchor that describe the
  form). The code that accepts the old form reads the end with
  `compat.Get(id).Until()` - never a literal release, in code, in a sentence or
  in a comment. Add its row to the table below and to the author's page:
  [PLUGINS.md → Compatibility window](PLUGINS.md#compatibility-window) for a
  storage plugin, [PLUGIN-KIT.md → Compatibility window](PLUGIN-KIT.md#compatibility-window)
  for an app.
- **Say it in the CHANGELOG** of that release: what is old, what replaces it,
  and the window's end.
- **Never promise an earlier end.** 0.55 wrote that 0.56, one release later,
  would stop taking the old storage plugin signature; the rule replaced that
  promise. A page, a sentence or a comment either states the end from
  windows.json or says "during its compatibility window".

Removing an old form, once its window is over:

1. The release that will refuse it is past the window:
   `compat.Get(id).Open(release, date)` is false for it - its version is at
   least the window's end version and its date is not before the end date.
2. A published release already announced it: its CHANGELOG section names the
   `id` and the release that will refuse it. That release is `announced`; it
   is published before the removal is, so the removal lands at least one
   release after the notice.
3. The removing change sets `announced` and `removed` (the release that refuses
   it) on the window instead of deleting the row, takes the row out of the
   author's page, and puts the removal under `### Removed` in the CHANGELOG,
   naming the `id`.

`web/tests/docs/compatWindow.test.ts` holds the rest to windows.json: the
rule's numbers on this page and the authors' pages, the table below and the
authors' tables, no living page, catalogue sentence or plugin source comment
promising a refusal in a release later than the last published one other than
at a window's end, and, for a `removed` window, an end it does not cut short and
an `announced` section that is published, earlier and names the `id`.
`backend/internal/compat` tests the arithmetic.

Old forms still accepted:

| id | Old form | Since | Accepted at least until (the later of the two) | Described in |
|---|---|---|---|---|
| `storage-build-signature-sha256` | A storage plugin build signature over the SHA-256 alone | 0.55 (2026-10-09) | 0.61 and 2027-10-09 | [PLUGINS.md → What is signed](PLUGINS.md#what-is-signed) |

Older spellings filex still reads have no window: an app manifest's
`min_filex` (beside `filex`), the `public_page_*` host functions and the
`PublicPage*` wrappers (beside `share_*`), and the `libreoffice` engine name
(for `office`). They are accepted with no end; retiring one starts its window
at the release that first warns about it, with a row here.

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
  constant to the backend catalogue - `backend/internal/notify/catalog_test.go`
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

**A user guide says what is, briefly, and links the rest.** A page opens with
what it is for and its short path (install, switch on, first use), then its
reference. A fact lives on one page and the others link to it; the README keeps
the short tour and every feature's full entry is in [FEATURES.md](FEATURES.md).
History - "since 0.50", "used to", "fixed after", which release changed what - is
the CHANGELOG's: a guide keeps only what a reader on today's version needs (a
version a feature needs, an upgrade note that still bites). A reason or a
measurement behind a behaviour that the CHANGELOG does not hold goes under
[Background notes from the user guides](#background-notes-from-the-user-guides),
and the guide links it where a reader would ask why. (#218: a reader on
r/selfhosted called the docs "extremely verbose and lengthy documentation that
is unclear at times"; the README was 20,270 words and #218 made it 5,937, with
the rest in FEATURES.md.)

**A plain hyphen, never a long dash.** Documentation, the changelog, filex.sh,
store listings and package descriptions write `-` where a text would reach for
an em dash or an en dash, code blocks included: ` - ` between two clauses,
`3-60` for a range, a lone `-` for "nothing", `-1` for a negative number and
`a - b` for arithmetic. Nor a character that passes for a hyphen - U+2010,
U+2011 (non-breaking), U+2012, U+2015, U+2212 (minus sign): they look like `-`,
and a search for the word does not find them (389 non-breaking hyphens sat in
the docs until 0.50). The one exception is the interface's minus sign as a
whole label on its own, the zoom-out button beside "+"; the docs have none. A
sentence that wraps with the dash first on its line puts it at the end of the
line before, or the line turns into a list item.
`web/tests/i18n/noLongDashesDocs.test.ts` fails on one (the interface code and
the translations have `noLongDashes.test.ts`); both read the rule from
`dashProblem` in `scripts/i18n-validate.mjs`, the language pack validator's. A heading's
anchor changes with its dash: `node scripts/check-doc-anchors.mjs` names every
link that has to follow it.

**The README's translations follow the English one.** `README.md` is the
source; `README.<lang>.md` at the root (`tr`, `de`, `es`, `fr`, `zh-CN`) are
translations of it, linked from the language line under the badges. Each opens
with a comment naming the commit it was translated from, so
`git diff <that commit> -- README.md` is exactly what it is missing. A change
to `README.md` does not wait for them: carry it into the translations in the
same change when you can, and when you cannot, leave them whole and behind
rather than half-updated - each one says in its first lines that the English
text holds. Three things never differ from the English: code blocks and
commands, link targets, image paths. And an interface label is written as the
interface shows it in that language (the Turkish catalogue, a language pack's
`translations/`), not as a translator would word it, so the page names what
is on screen. A window that does not speak that language keeps its English
label, glossed once: the desktop app's own windows (*App settings → Accounts*,
*App settings → Synced folders*) are English and Turkish only, whatever language
pack the explorer inside them wears. `scripts/check-links.mjs` and `scripts/check-doc-anchors.mjs`
read every `README.*.md` at the root; the long-dash test reads every tracked
markdown file, these included.

---

## Background notes from the user guides

Reasons, measurements and design history that used to sit in the user guides
(#218), kept for whoever changes the code behind them. Each guide links the note
it relies on; the release that made a change is in the CHANGELOG.

### Configuration background

Notes behind [CONFIGURATION.md](CONFIGURATION.md).

#### Configuration escrow adoption and its second variable

`FILEX_INSTALLATION_E2E_ESCROW_KEY` is an install-time setting: any later change
stops the server. Adoption is the one supervised exception, and it exists
because the alternative was worse: escrow could only ever be chosen in the
first second of an installation's life, and nobody decides key-escrow policy
before they have a single file
([E2E-ENCRYPTION.md → Adopting escrow](E2E-ENCRYPTION.md#adopting-escrow-on-an-installation-that-already-exists)).

Why adoption takes a separate variable (`FILEX_INSTALLATION_E2E_ESCROW_ADOPT=1`)
rather than "the key appeared, so they must have meant it": an escrow key
arrives by being pasted into a compose file, a Helm values file or a `.env`,
usually copied from another deployment. That is the shape of an accident, and
the accident is silent - the installation gains a second key holder, only new
folders get it, and nothing looks wrong until somebody needs the key on an old
folder. One extra line, in the file the operator is already editing, is the
smallest thing that is still a decision.

#### Configuration trusted proxies and the gateway

Why `auto` carves the gateway and filex's own address out of a container
network ([CONFIGURATION.md → Trusted proxies](CONFIGURATION.md#trusted-proxies)),
measured on Docker 29 and Podman 5.8:

- a connection to a port published on `127.0.0.1` is relayed by `docker-proxy`
  and reaches the container from the gateway (`172.x.0.1`), and a host process
  that dials the container's address directly arrives from it too; rootful
  Podman relays from `10.88.0.1` the same way. Trusting the gateway would let
  anything that reaches the published port choose its own address;
- rootless Podman on a user-defined network relays every published-port
  connection - from the internet too - from the container's **own** address;
- a client on the LAN that reaches a published port arrives with its own
  address, which `auto` does not trust either.

Whether an interface is a `veth`, a macvlan or an ipvlan is asked of the kernel
over netlink (what `ip -d link` reads); `/sys/class/net` shows the three the
same way, so it cannot be used to tell them apart.

#### Configuration antivirus restart in both directions

The ClamAV switch, mode and clamd address
([CONFIGURATION.md → Antivirus (ClamAV)](CONFIGURATION.md#antivirus-clamav))
apply only after a restart, in both directions, because the scan pipeline is
wired once at boot: the queue handler is registered and the write paths are
handed an enqueue function only when scanning resolved as available.

This is a deliberate choice, not an oversight. Making "off" immediate while
"on" stayed deferred - the shape a naive implementation falls into, since
suppressing work is easy and creating a worker-pool handler at runtime is not -
would give a control that is sometimes live and sometimes not, with no way to
tell which from looking at it. The scan-size ceiling and the save-scan window
are read per file: they tune a pipeline that is already running, while the
three restart-bound settings decide whether it exists.

#### Configuration ClamAV save-scan window bounds

Why `FILEX_CLAMAV_SAVE_WINDOW_MINUTES` (`antivirus.save_scan_window_minutes`) is
held to 2-60 minutes and refuses `0`
([CONFIGURATION.md → The editor save-scan window](CONFIGURATION.md#the-editor-save-scan-window)):

- **2 minutes minimum**: a save every couple of minutes is still one editing
  session, so a shorter window stops coalescing anything while multiplying
  scans.
- **1 hour maximum**: the window *is* the time an infected file written through
  the editor stays live. Longer is not a tuning preference, it is a different
  security posture.
- **`0` refused**: it reads equally as "scan every save" and "never scan", which
  are opposite behaviours, and the second would silently re-open the gap the
  window lives inside. Turning scanning off is the separate switch
  (`antivirus.enabled`), which is explicit about being a separate control and
  about applying at the next restart.
- A value out of range is refused on save, so the administrator finds out while
  looking at the field; one already stored out of range is clamped on read with
  a WARN rather than refusing to boot over a scan interval.

#### Configuration delete workers and object stores

`FILEX_OPS_DELETE_WORKERS` (default 4, [CONFIGURATION.md → File operations](CONFIGURATION.md#file-operations-copy--move--delete))
exists because on an object store every trashed file is several round trips:
deleting one item at a time, a delete job of tens of thousands of files ran for
hours. `1` restores that one-at-a-time behaviour.

#### Configuration migrations behind the vault and co-editing

The vault's write lock is the `vault_locks` table (migration 00096). The
co-editing relay keeps its sessions in the database (migration 00107) and the
members' cursors in `coedit_cursors` (migration 00109), which is how several
filex processes on one database serve the same sessions
([CONFIGURATION.md → Editing together](CONFIGURATION.md#editing-together)).

### Desktop app background

Notes behind [DESKTOP.md](DESKTOP.md).

#### Desktop snap without Chromium's sandbox

Chromium's sandbox inside a snap needs `browser-support` with
`allow-sandbox: true`, which Snapcraft limits to trusted publishers, never
connects by itself, and reviews by hand. 0.50 and 0.51 asked for it (as a
`browser-sandbox` plug, with `sudo snap connect filex-app:browser-sandbox` to
connect it); their revisions waited in the Snap Store's manual review and the
stable channel stayed on 0.49. Since 0.52 the snap asks for no such permission,
as Snapcraft advises for Electron apps, and its updates reach the stable channel
again by themselves. A `browser-sandbox` connection made by hand goes away with
the plug; there is nothing to undo. The cost, which the user guide states, is
that the confinement walls the app off from the system but not the pages it
shows from the app ([DESKTOP.md → The snap and the sandbox](DESKTOP.md#the-snap-and-the-sandbox)).

The release check: every release installs the snap on x64 and arm64 Ubuntu
24.04 machines before it publishes, and checks that it asks for no
`allow-sandbox` and that every process of the app runs under the snap's
AppArmor profile, in enforce mode, with a seccomp filter. Measured on Ubuntu
26.04 (x64) with a snap built this way: `browser-support` connected by itself,
nothing was left to connect for the app to open, and its browser, zygote,
renderer, GPU and utility processes all ran as
`snap.filex-app.filex-app (enforce)` with a seccomp filter.

In 0.55 the snap moved from `core20` to `core24` with Snapcraft's GNOME
extension; how it is confined and what it may reach did not change, and the
window still opens through X11 (XWayland on a Wayland desktop).

#### Desktop AppImage and the sandbox, how it got here

- Up to 0.49 an AppImage that could not build Chromium's sandbox simply did not
  appear, and the reason was only on a terminal: `The SUID sandbox helper
  binary was found, but is not configured correctly`. 0.50 added the launcher
  that shows the steps and exits with code 78.
- Up to 0.49 the AppImage's own menu entry - the one AppImageLauncher, Gear
  Lever and appimaged copy - started the app with `--no-sandbox`
  (electron-builder's default), on every distribution. Since 0.50 it does not,
  so an integrated AppImage on Ubuntu 23.10 and later needs the AppArmor
  profile like any other.
- The AppImage's entry point `AppRun`, as electron-builder 26 writes it, adds
  `--no-sandbox` by itself whenever it cannot create a user namespace - which,
  without the profile, is always on Ubuntu 23.10 and later. So the AppImage
  keeps the entry point it had up to 0.54 (electron-builder 24's), which passes
  on exactly the arguments it was given, and without the profile the launcher
  still stops and shows the steps (0.55).

#### Desktop code signing

The downloads are not code-signed yet, and that is a real gap, not something
to wave away: a signing certificate is a paid, separate step. On macOS the app
is ad-hoc sealed because without that seal macOS 26 does not warn but
*refuses* - "malware blocked and moved to Trash", no override (CHANGELOG
0.20.3). Signing and notarization are the actual fix there, the same paid
decision as on Windows.

#### Desktop bearer token as a function

The app authenticates with a bearer token rather than a cookie, and hands the
explorer a *function* that returns the current account's token - the token
changes when you switch accounts, and a value captured once would be the wrong
account's. Up to v0.13.4 the explorer read that header builder synchronously,
which quietly dropped a token supplied that way: the OnlyOffice config request,
the starred list and recently-opened all answered `401`, and an editor that
cannot fetch its own config simply never appears. The builder is asynchronous
since and every viewer waits for it. In the same versions **Open in new tab**
resolved its relative link against the app's internal `app://filex` origin, an
address no operating system can open, so the menu entry did nothing visible
(CHANGELOG 0.14.0).

#### Desktop Open with filex, why it exists

Most Linux desktops have no Microsoft Office, many Macs have none, and plenty
of Windows machines have none either; filex already had a good editor, and the
documents on the desktop had no way into it. The type list is deliberately
office documents and `.csv` only: taking a file type away from an app that
handles it better is not an improvement. `.csv` is in it for what the editor
does with it - a save as CSV keeps the file's delimiter, byte order mark, line
ends and the text of every cell nobody changed, which neither a text editor nor
a spreadsheet app without import settings gives you. The strip that names the
local target file under the editor is there because you are editing a copy and
should see where it goes home to, and a save that fails silently is the one
outcome the feature must never produce.

#### Desktop default apps per operating system

- **Windows**: since Windows 10 the `UserChoice` registry key is protected by a
  hash over the extension, the account's SID and a Microsoft salt. An
  application cannot write it, and forging that hash is exactly what the
  protection exists to stop; an installer that appears to manage it is either
  overwriting the plain `.docx` ProgId behind your back or tampering. So the
  button opens the Default apps page.
- **macOS**: the API exists (`LSSetDefaultRoleHandlerForContentType`) but
  Electron exposes no binding for it, and filex ships no native code.

#### Desktop window and settings choices

- There is no appearance switch in the app's *Settings* on purpose: the theme
  is a choice about the file list, and a second switch would be a second answer
  to the same question.
- *Shortcuts* and *Restart the tour* used to sit behind the "..." at the top
  right of the file list; they are in the avatar menu now.
- *Start when I sign in* shows what the OS did, not what was asked for:
  policies and sandboxes refuse the request often enough that showing the app's
  own intent back would be a lie.

#### Desktop drag-out link timing

The web explorer asks for the one-file drag link while the pointer rests on a
row and again on press. Measured against a local server (2026-09-25, e2e 71): a
mint takes 3-4 ms in the browser's own request timing (n = 6), and 66 ms passed
from Playwright starting to hover the row to the link being in hand - a person
rests on a row far longer than either before a drag, so the gap only shows over
a very slow connection.

Measured with a real mouse in `e2e/tests/71-admin-drag-out-link.spec.ts`
(bearer: row, ⌘K result, the bytes behind the link, a drag that beats the link,
the cookie session) and `e2e/tests/47-palette-everywhere.spec.ts`; the server
side in `backend/internal/api/handlers/download_link_test.go`.

### ONLYOFFICE background

Notes behind [ONLYOFFICE.md](ONLYOFFICE.md).

#### ONLYOFFICE Test, the measurement behind the JWT line

The Test's second request without a token, and what a Document Server with JWT
off does, were measured with Docs 9.4 (`e2e/realenv`, the S2 case of issue #80):
its log says `checkJwt error ... invalid signature` for the editor and `... is
not allowed. Because, It is private IP address` for the download. A Document
Server that refuses the probe's signature is reported as **unmeasured** rather
than as a broken route because that is a JWT problem, and sending the operator
to the wrong address would be worse than saying nothing
([ONLYOFFICE.md → Three machines, three addresses](ONLYOFFICE.md#three-machines-three-addresses)).

#### ONLYOFFICE private-address filter, where the table comes from

The table in [ONLYOFFICE.md → Failure: editor shows "Download failed"](ONLYOFFICE.md#failure-editor-shows-download-failed)
was read from the ONLYOFFICE server source: 7.4.0 changed
`request-filtering-agent.allowPrivateIPAddress` from `true` to `false`
([e0aaeee6](https://github.com/ONLYOFFICE/server/commit/e0aaeee64331aff30270251bba1d830c65568346)),
7.5.0 added `allowPrivateIPAddressForSignedRequests`, default `true`
([1deefe3e](https://github.com/ONLYOFFICE/server/commit/1deefe3ee2b4483f0ad717172352508fcb39877e)),
and 8.1.0 replaced it with `externalRequest`
([446245c0](https://github.com/ONLYOFFICE/server/commit/446245c0aa3282e8b3c8999170cee03303f55d3c);
defaults in the [server configuration reference](https://api.onlyoffice.com/docs/docs-api/get-started/configuration/server-config/)).

#### ONLYOFFICE save scan, why it is not deferred

A finished document (callback status 2) is scanned at once rather than on the
save window's timer: scanning it on a timer would buy no coalescing (there is
only one save to coalesce) and would leave it unscanned for up to a full
window. The window is only worth paying for where saves actually repeat, which
is force-save (status 6)
([ONLYOFFICE.md → When the scan runs](ONLYOFFICE.md#when-the-scan-runs-and-why-the-status-code-decides-it)).

#### ONLYOFFICE session records and the cache

The `office_sessions` table (migration 00092) has filex's in-process cache in
front of it (`internal/memcache`; no Redis, nothing outside the process). It
only makes the frequent reads cheap - every editing config asks whether its
session is recorded already. The decisions - may this save go over the file,
did the person drop it, is the editor's session still current - are always
read from the database, because another instance's answer is not in this
instance's cache; a write goes to the database first and to the cache only when
the database took it. The modification time is stored as an integer (Unix
nanoseconds), so it compares exactly on every engine
([ONLYOFFICE.md → When the document changes while it is open](ONLYOFFICE.md#when-the-document-changes-while-it-is-open)).

#### ONLYOFFICE new documents, why the bytes are built in

New office documents are minimal valid documents embedded in the binary
(`backend/internal/newdoc`), not made by the Document Server or LibreOffice,
because `:slim` and the bare binary deliberately carry no external tools, and a
feature that works on one image and answers 500 on another is worse than one
with a narrower promise. The server only states which service a type's editor
needs (`newdoc_types[].requires`) and the client resolves it, deliberately: an
embedder may point at a document server this process cannot reach, so the
client is the only place that knows the true answer. What the client must not
do is re-derive the dependency from an extension list of its own - that list
rots the moment the registry grows a type
([ONLYOFFICE.md → Creating new documents](ONLYOFFICE.md#creating-new-documents),
[BACKEND.md → Rules the server publishes](BACKEND.md#rules-the-server-publishes)).

#### ONLYOFFICE CSV measurements

- Opening a CSV with `document.options` filled in (delimiter and UTF-8) was
  measured on Docs 9.4: ready in about 4 seconds, no "Choose CSV options"
  dialog.
- The filex process that refused a CSV save gives the next opening of the file
  a new editing session because, measured on Docs 9.4, an editor opened on the
  session whose save was refused never finished loading.
- Every cell rule below is pinned by the bytes a Docs 9.4.0 saved
  (`backend/internal/officesave/csv_keep_measured_test.go`).

#### ONLYOFFICE CSV cell values as measured

[ONLYOFFICE.md → Cells nobody changed keep their text](ONLYOFFICE.md#cells-nobody-changed-keep-their-text)
gives the rules; these are the values behind them, as measured. 0.51.0 put
back only the file's delimiter, byte order mark and line ends; measured on
0.51.0 with Docs 9.4.0, one cell edited in a semicolon file, the editor in
English, in cells nobody had touched `05320000001` came back as `5320000001`,
`007` as `7`, `000` as `0` and `01.02.2026` as `1/2/2026` (`15.03.2026`
stayed: ONLYOFFICE read the month first, and 15 is no month), and every data
row gained an empty cell at its end; with the editor in Turkish the dates came
back as `1.02.2026`. In the lists below, "written as in 0.51.0" means the
file's delimiter, byte order mark and line ends with ONLYOFFICE's values.

"The same value written ONLYOFFICE's way" is what Docs 9.4.0 was measured
writing for a cell nobody touched (the editor in English, Turkish, German and
French), and nothing else. It is one way only: `7` is what ONLYOFFICE writes
for `007`, so the file keeps `007`; `007` is never what it writes for `7`.

- **A whole number** without its leading zeros (`007` and `7`, `05320000001`
  and `5320000001`), its `+` (`+905320000001` and `905320000001`) or the
  spaces in front of it and one after it (` 42` and `42`); one written as
  hexadecimal (`0x10` and `16`); and one too long for a spreadsheet's number:
  `9007199254740993` comes back as `9007199254740992`, `123456789012345678`
  as `1.2345678901234568e+17`, and every one from 2^63 up, either way, as
  `-9.2233720368547758e+18` (`12345678901234567890`, a 24-digit account
  number).
- **A number with a decimal point**, which ONLYOFFICE keeps to six decimals:
  `03.50` and `3.50`, `0.1234567` and `0.1234570`, `+1.5` and `1.5`; with
  nothing before the point, `.5` as `.0` (the value gone) and `-.5` as
  `-0.5`. Never a decimal comma: `3,5` is text to ONLYOFFICE in every
  language measured and comes back as it was.
- **A date with a four-digit year from 1900 on**, written the way the editor's
  language writes one, with the same three numbers in the same order. In
  English month/day/year without leading zeros: `01.02.2026`, `01/02/2026`
  and `1-2-2026` as `1/2/2026`. In Turkish day.month.year, the month in two
  digits: `01.02.2026`, `1/2/2026` and `1-2-2026` as `1.02.2026`, `15/3/2026`
  as `15.03.2026`. An ISO date: `2026-02-01` as `2/1/2026` and as
  `1.02.2026`.
- **`true` and `false`**, saved as `TRUE` and `FALSE` (`True` comes back as it
  was).
- **A tab inside a cell**, which ONLYOFFICE drops (unless the file is
  tab-separated), and a text longer than 32767 characters, which it cuts
  there.

⚠ Nothing that only looks like one of these. A date ONLYOFFICE does not read
as one (`15.03.2026` in English, the 15th month) comes back as it was, and
what a person types over it is theirs: in the English editor a typed
`15/3/2026`, `01.02.2026` or `1.2.2026` is saved as typed, and the file gets
it. That is also why German's `01.02.2026` and French's `01/02/2026` are no
rule. A line end inside a quoted cell comes back as it was. Every rule is
pinned by the bytes a Docs 9.4.0 saved
(`backend/internal/officesave/csv_keep_measured_test.go`).

⚠ What is still written as ONLYOFFICE writes it:

- **The cell you edit.** Type `007` into a cell and the file gets `7`.
- **A change that is only another way of writing the same value** cannot be
  made in ONLYOFFICE. It shows `007` as `7` already and saves the same text
  whether or not somebody retyped it, so filex cannot tell the two apart and
  the file keeps `007`. Edit the file as text for that.
- **What ONLYOFFICE changes in a way no rule puts back**, edited or not
  (measured, every language unless said): a time (`08:05:30` as `8:05`,
  `00:13` as `0:12`, a minute short), a percent (`12.34%` as `12.3%`), a
  number with an exponent (`1e3` as `1000.00000`), `5.` as `5`, a date with a
  time (`2026-02-01T10:30:45Z` as `2/1/2026 10:30`), a two-digit year
  (`01.02.26` as `1/2/2026`), a year before 1900 (`01.02.1850` as
  `1/2/3750`), a day that is not in its month (`31.02.2026` as `3.03.2026`
  in Turkish), a formula (`=1+1` as `2`). Keep such a file out of
  ONLYOFFICE, or edit it as text.
- **A date in an editor language other than English and Turkish**, unless
  that language happens to write it the English or the Turkish way (German
  writes `15/3/2026` as `15.03.2026`, which is the Turkish way, and it is
  kept; `1.2.2026` as `01.02.2026`, which is not). Since 0.54 the editor
  opens in the language of the person who edits
  ([The editor's language](ONLYOFFICE.md#the-editors-language)), so this is a person whose
  filex speaks German, French or another language ONLYOFFICE has - or
  everybody, when the administrator fixes such a language. The same goes for
  a regional setting other than the language's own (`en-GB` writes day first).
- **A date typed as text in the way the other language writes one.** The
  English editor keeps a date typed with dots as text (measured: `01.02.2026`
  and `1.2.2026` typed are saved as typed). Typed as `1.02.2026` over a cell
  whose text has the same three numbers in the same order (`1.2.2026`), it is
  the Turkish way of writing that text, and filex keeps the file's; the same
  holds the other way round for a date the Turkish editor cannot read
  (`1/13/2026` typed over `01/13/2026`). Only the spelling is lost: the
  numbers and their order are the cell's.
- **A file that is not UTF-8** (ONLYOFFICE asked for its encoding when it
  opened): its text cannot be compared with what ONLYOFFICE saved, so its
  first save is written as in 0.51.0, in UTF-8 with a byte order mark. From
  then on it is a UTF-8 file and its cells are kept.
- **A file over 64 MiB or two million rows, or with a row of more than 16384
  cells** (as many columns as an ONLYOFFICE sheet has): written as in 0.51.0.
- **A column added, removed or moved**: the cells from that column on are
  written as ONLYOFFICE saved them, in every row. The cells before it keep
  their text only while they are more than half of the row's filled cells;
  otherwise the whole row is ONLYOFFICE's. A last column that was removed
  leaves an empty cell at the end of each row.
- **A row filex cannot tell from a new one**: a row in which half of the cells
  or more were changed at once, a row that was edited and also moved (edited,
  then the list sorted), and every one of several rows that differ only in
  how a value is written (`A;007` and `A;7`) when one of them was deleted or
  another added.
- **A row typed to read like another row of the file** (`5` typed where
  another row says `05`) is written as typed while the rows around it stand
  where they stood. When rows were added, deleted or moved there in the same
  save, or that other row was itself changed, filex cannot tell which of the
  rows that now read the same is the file's: the first of them in the save
  gets the file's text, `05`.

#### ONLYOFFICE CSV: a decimal typed, as measured

[ONLYOFFICE.md → A decimal you type keeps the file's comma](ONLYOFFICE.md#a-decimal-you-type-keeps-the-files-comma)
gives the rule. Measured on Docs 9.4.0.1 (2026-10-17) in two sessions, each a
semicolon file whose `value` column said `3,5` in every row, each `3,5` typed
over through the cell name box. The first: two rows `1250,75` and `0,25`
nobody touched and a row added with `2,75`, the editor in Turkish (`tr`,
region `tr-TR`), English (`en-US`) and German (`de-DE`). The second (`1,1`,
`2,675`, `99999999999999,9`, `0,000001`, `'4.5`, `=7/2` below): one row
`1250,75` nobody touched, no row added, Turkish and English only. What the
save held (the file then gets the comma wherever the save has a number with
a point and digits):

| Typed | Turkish | English | German |
|---|---|---|---|
| `4,5` | `4.5` | `4,5` (text) | `4.5` |
| `-4,5` | `-4.5` | `-4,5` (text) | `-4.5` |
| `1.250,75` | `1250.75` | `1.250,75` (text) | `1250.75` |
| `4,50` | `4.5` | `4,50` (text) | `4.5` |
| `,5` | `0.5` | `,5` (text) | `0.5` |
| `4,` | `4` | `4,` (text) | `4` |
| `1,1` | `1.1000000000000001` | `1,1` (text) | |
| `2,675` | `2.6749999999999998` | `2675` | |
| `12345678,123456` | `12345678.123455999` | `12345678123456` | `12345678.123455999` |
| `99999999999999,9` | `99999999999999.906` | `99999999999999,9` (text) | |
| `0,000001` | `9.9999999999999995e-07` | `0,000001` (text) | |
| `1,5E+20` | `1.50e+20` | `1,5E+20` (text) | `1.50e+20` |
| `4,5%` | `4.50%` | `4,5%` (text) | `4.50%` |
| `4.5` | `4.May` | `4.5` | `04.Mai` |
| `4.50` | `Nis.50` | `4.5` | `Apr.50` |
| `-4.5` | `-4.5` | `-4.5` | `-4.5` |
| `'4.5` | `4.5` | `4.5` | |
| `=7/2` | `3.5` | `3.5` | |
| `1 250,75` | `1 250,75` (text) | `1 250,75` (text) | `1 250,75` (text) |

The untouched `1250,75` and `0,25` came back as they were in every editor.
The English editor reads a comma as a thousands separator when a whole part
other than `0` stands before it (`2,675` as `2675`, `12345678,123456` as
`12345678123456`), and keeps `0,0001` or `0,000001` as text: such a typed
number is whole, and nothing puts a comma back. The saved bytes are in
`backend/internal/officesave/csv_keep_comma_test.go`, and the file of the
report (`3,5` typed over as `4,5` in Turkish, saved `4.5`) in
`backend/internal/officesave/officesavetest/docs94_comma.go`.

### Apps background

Notes behind [APP-PLUGINS.md](APP-PLUGINS.md).

#### Apps store screen row rule

Whether the navigation's **Apps → App store** row is drawn is one rule in the
explorer for every host: the host has the page, the caller is a person (a
browser session or the desktop app's own pairing), and the server shows them
the screen. Keeping it one rule is why the web app and the desktop app cannot
disagree about it ([APP-PLUGINS.md → The store screen](APP-PLUGINS.md#the-store-screen)).

#### Apps hidden actions are not switches

An app's hidden actions (e-Signature's *Apply a signer's submission*, a
scheduled *expire*) are its machinery. Before 0.43.0 they were offered as
switches under **Menu actions**, and switching one off half-broke the app in a
way no menu showed. They are no longer listed, an override sent for one is not
stored, and one already in the table is not honoured
([APP-PLUGINS.md → What the administrator controls after install](APP-PLUGINS.md#what-the-administrator-controls-after-install)).

#### Apps screen rules

The three rules filex enforces on every screen an app describes - every choice
visible, nothing behind "advanced", one step asks one thing - hold whichever
app drew the screen because a form that hides what it is asking is the same bug
in every app. And filex refuses to install an app whose own screens miss a
language it promised: a half-translated screen is the author's bug, and the
author should meet it before an administrator does
([APP-PLUGINS.md → What a person sees](APP-PLUGINS.md#what-a-person-sees)).

#### Apps open-with choices from before 0.50

Since 0.50 a person's "always open this kind with this app" choice is one
record on the account. Choices an earlier build kept per surface (in each
surface's preference document) were adopted once: those of the surface
document written last
([APP-PLUGINS.md → Default apps](APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail)).

#### Apps e-Signature certify seal and lock

The closing of a completed signature request - the first signature certifies
(DocMDP P=2), filex seals the whole file (P=1), the SHA-256 of the sealed bytes
goes to every party, and the signed file can stay locked - is the owner's
answer (2026-09-22) to the question "after a document is fully signed and
someone changes it, does the signature say so, or do we lock the file?": both,
plus a seal ([APP-PLUGINS.md → When it is done](APP-PLUGINS.md#when-it-is-done)).

#### Apps signing authority import of p12

The signing authority import takes PEM only, and the page tells the operator to
convert an encrypted `.p12`/`.pfx` first
(`openssl pkcs12 -in ca.p12 -nodes -out ca.pem`): the encrypted containers in
the wild are more varied than any one library reads, and a half-supported
import is worse than an honest instruction
([APP-PLUGINS.md → The signing authority](APP-PLUGINS.md#the-signing-authority)).

#### Apps signing certificates for documents only

Before 0.50.0 a signer's certificate and the platform seal also carried
emailProtection. With the name and the address the app chose, that made each
one an S/MIME certificate under the operator's authority. Since 0.50.0 they
carry only the document-signing usages (RFC 9336's `1.3.6.1.5.5.7.3.36` and
Adobe's `1.2.840.113583.1.1.5`); a seal issued before is replaced the next time
it is used
([APP-PLUGINS.md → The signing authority](APP-PLUGINS.md#the-signing-authority)).

### Protocols background

Notes behind [PROTOCOLS.md](PROTOCOLS.md).

#### What an SMB server for filex would take

The user's answer is in [PROTOCOLS.md → Why there is no SMB server](PROTOCOLS.md#why-there-is-no-smb-server);
this is the size of the job behind it.

- Writing it means MS-SMB2 from NEGOTIATE upward: NTLMSSP session setup, TREE_CONNECT,
  CREATE with its create-contexts, READ/WRITE, IOCTL, locks, CHANGE_NOTIFY, oplocks and
  leases, durable handles, signing and encryption. Windows does not consider a share
  "working" until most of that is there. It is legal (MS-SMB2 is an open specification)
  and it is the single largest item on filex's board.
- If it is ever written, note that port 445 is not carried across the public internet by
  most ISPs; the way out is **SMB over QUIC** (TLS 1.3 on UDP/443, ALPN token `smb`), which
  needs Windows 11 24H2+ / Server 2025 on the client side, or Samba 4.23+ on Linux.

#### Why the SFTP server sends exit-status

OpenSSH 9+ speaks SFTP for `scp`, and without `exit-status` on the subsystem channel every
copy ended in a silent `exit 1` with the bytes already transferred
([PROTOCOLS.md → SFTP](PROTOCOLS.md#sftp)).

---

## Screenshots

**Every UI feature ships with its screenshots, in the same change.** A screen
that changed under a picture that did not is wrong information in the README,
not missing information.

The pictures are **not kept in this repository** (task #176, 2026-10-06). They
are published on filex.sh under names that carry their content hash,
`e2e/shots/manifest.json` names the current file of each, and the README, the
docs and filex.sh link exactly those URLs. [`e2e/shots/README.md`](../e2e/shots/README.md)
is the whole flow.

1. **Take them with `pnpm shots` - in the build host's test chain.** The
   published set is taken on Linux, in the chain's Playwright container with
   its fontconfig (Liberation Sans), where the nightly run takes it
   (2026-10-06): fonts are the system's, and pictures from two places are a
   README in two typefaces. A run on Windows or macOS takes, compares and
   shows its pictures for you to look at while you work on a screen, stages
   nothing for the site, and `accept` refuses it. A run on the build host
   outside the chain (`SHOTS_ENVIRONMENT` is not `chain`) is refused too
   (2026-10-08): the chain takes every scene, the app and Document Server
   scenes included (task #187), so no scene needs the host.
   `accept --looked --outside-chain` takes such a run on purpose; it still
   prints a warning, and the manifest then says `"environment": "local"`,
   which the shot gates refuse until the chain takes the set again. A
   screen with no picture yet gets one in the
   `e2e/shots/` script that owns it, or in a new script there - the command
   finds every file in that directory that imports `@playwright/test`, so a new
   script cannot be left out. It takes only the scenes whose inputs changed
   since the published set (`--all` takes every one; the nightly run takes
   every one every night, in the chain).
2. **Look at the contact sheet it prints** (`e2e/.artifacts/shots/contact-sheet.html`):
   only the pictures whose pixels moved past the threshold, the new ones and the
   removed ones, each changed one beside the published picture with a map of
   where it moved. Each must be in English, show what its name says, and have
   nothing across it - no onboarding tour, no install banner, no dialog caught
   mid-fade.
3. **Publish them, then link them**: `node scripts/shots-site.mjs upload`
   (maintainers; it reads every file back from the site), then
   `node scripts/shots-site.mjs accept --looked`, which refuses a run not
   taken on Linux, a run taken outside the build host's test chain (unless
   `--outside-chain`) and a failed run, and refuses until every new picture answers from the site, then
   writes the manifest and points every page at the new URLs. Commit the manifest and those pages with the code. Never a
   PNG: there is no screenshot folder in the tree any more, and
   `web/tests/deploy/shotsSite.test.ts` fails on one.

**Every scene runs at one clock** - Tuesday, September 15, 2026, 10:30 UTC
(`e2e/shots/clock.mjs`): the browser starts there, the API's times are moved
into it on the way to the page, dates print snapped to the hour and the
fixtures' files carry fixed dates, so a picture changes only when its screen
did, not every night with the date on it. A new script makes its browser
contexts through `scene.mjs`'s `newContext` (or with `SCENE_CONTEXT` and
`stageClock`); `web/tests/deploy/shotsFixtures.test.ts` fails on one that does
not. `SHOTS_REAL_CLOCK=1` turns it off for a diagnosis, and `accept` refuses
that run.

What the command does, so a red run can be read: works out each scene's digest
(its script and imports, the fixtures, the product or the `INPUTS` it declares,
the locale, the platform, the release number) and skips the scenes whose
digest is the published one - with none left, it builds nothing; builds
`build:packages → build:web → sync:embed → go build` (Go native, or through WSL
on Windows); boots the binary and **refuses to shoot unless it serves `web/dist`
byte for byte** (`scripts/check-embed.mjs`; source maps only have to exist, their
bytes are not reproducible - a binary built without `sync:embed` carries an older
UI and still passes every API check); runs the scenes to take, the
E2E-escrow instance and its throwaway key pair included; compares every picture
with the published one (more than 32 pixels moved by more than 24/255 is a
change: `SHOTS_DIFF_PIXELS`, `SHOTS_DIFF_TOLERANCE`); stages the changed and new
ones under their published names; and ends every process it started.
`--only <script>` while you work on one screen, `--no-build` to shoot a binary
you already built (still verified). It needs Playwright's Chromium once:
`pnpm --dir e2e exec playwright install chromium`.

CI runs the same command with `--all` on every tag and on demand (GitHub
*Screenshots*) and uploads the pictures, the diffs and the sheet,
so a script that no longer fits the product turns a job red instead of a
release night. CI publishes nothing.

**The app scenes are taken outside CI, in the build host's test chain.** `apps.mjs`,
`apppermissions.mjs`, `langpack.mjs`, `pluginrequests.mjs` and `signing.mjs` photograph
e-Signature, Convert and the language packs, which filex ships alongside itself, and their
builds are not in this repository: they come from sibling checkouts of
[filex-sign](https://github.com/BRF-Tech/filex-sign),
[filex-convert](https://github.com/BRF-Tech/filex-convert) and the language
pack repositories (`../filex-sign/dist`, `../filex-convert`,
`../filex-lang-es`, ...), or from `FILEX_SIGN_APP_DIR`, `FILEX_CONVERT_APP_DIR`
and `FILEX_LANG_{ES,DE,FR}_APP_DIR`. The chain's shots job sets those from
the directories its settings name (`CHAIN_SIGN_APP_DIR`, `CHAIN_CONVERT_APP_DIR`,
`CHAIN_LANG_ES_DIR`, `CHAIN_LANG_DE_DIR`, `CHAIN_LANG_FR_DIR`) and runs with
`--with-apps`. Without one, `pnpm shots` stops before it builds anything and
says which. In CI (`CI` is set) those scenes are **left out** instead - named
in the log, the verdict and the contact sheet, and their published pictures
stand - because at a tag there may be no app release to fetch yet, and the
converter scene needs Docker for its engines. `--with-apps` puts
them back; `--without-apps` leaves them out anywhere.

**The converter picture needs the conversion engines.** Its wizard lists what
the server found and names every missing engine *Not installed on this server* -
not a picture for the README. When the host lacks one (a Windows workstation
lacks all of them), `apps.mjs` runs **this tree's build inside the full image**
(`ghcr.io/brf-tech/filex:full`, which carries ffmpeg, ImageMagick, Ghostscript,
poppler and rsvg) with Docker - nothing is installed on the host,
the image is pulled once, and the container's UI is checked byte for byte
against `web/dist` like the host binary's. `SHOTS_ENGINES=host|container`
forces one side. The chain's shots job installs the five in its own
container (they draw the thumbnails too) and sets `SHOTS_ENGINES=host`: it
has no Docker to fall back on, and a missing engine fails the scene. The office engine is not in the image: since 0.50 it is the
ONLYOFFICE Document Server filex is connected to, so the scene points the
instance at `SHOTS_ONLYOFFICE_URL` / `SHOTS_ONLYOFFICE_JWT` when they are set,
and otherwise at a placeholder that marks the engine connected for a picture
that converts nothing. The scene refuses to take the picture while anything on it
says an engine is missing. To rehearse a scene without replacing its pictures:
`SHOTS_DRY_RUN=1 node e2e/shots/apps.mjs` walks to every picture and writes
none.

**The ONLYOFFICE scene needs a real document server.** `csvoffice.mjs` (0.51)
photographs ONLYOFFICE's own spreadsheet, which only a Document Server can
draw, so it is declared by calling `documentServer()` (e2e/shots/scene.mjs) and
needs all three of `SHOTS_ONLYOFFICE_URL` (where filex and the browser reach
the document server), `SHOTS_ONLYOFFICE_JWT` (its secret) and
`SHOTS_ONLYOFFICE_CALLBACK_HOST` (the host name the document server reaches
this machine by: the scene's filex listens on every address, because the
document server downloads the file from it). Without them `pnpm shots` stops
before it builds anything and says which is missing; in CI, and with
`--without-apps`, the scene is left out exactly like the app scenes. The
chain's shots job points them at the chain's own Document Server
(`SHOTS_ONLYOFFICE_CALLBACK_HOST=filex`, the name the chain gives its jobs'
network on the server's network); the job runs while that server is up.

**`--keep-going`** runs every scene even after one failed. The run is still a
failure - the review names every failed scene and `accept` refuses it - but
the other scenes are taken and compared. The chain's shots job uses it. A
scene whose language packs are behind this tree (`langpack.mjs` exits 3,
`PACKS_BEHIND_EXIT` in `scripts/lib/shot-scripts.mjs`) is recorded in the
review as `"reason": "packs-behind"`; when every failed scene of a run is
one of those, the nightly chain's shots job is green with a warning instead
of red (`SHOTS_PACKS_BEHIND=warn`, which only the nightly profile sets).

---

## Translations and language packs

filex itself speaks English and Turkish: `packages/core/src/locales/{en,tr}.ts`
(the explorer), `web/src/locales/{en,tr}.json` (the admin panel) and
`backend/internal/srvtext/locales/{en,tr}.json` (the text the server writes). A
new or reworded string changes the English and the Turkish in the same change.
Every other language is a **language pack**: a repository of its own, made from
[filex-lang-template](https://github.com/BRF-Tech/filex-lang-template), with its
translation, the catalogue it follows, a glossary and its own validators
([PLUGIN-KIT.md → Writing a language pack](PLUGIN-KIT.md#writing-a-language-pack)).
Spanish, German and French ship as examples.

**The packs follow `main`, not the release.** Until 0.52 the new strings were
translated on release day, after the cut - 189 for 0.51, 287 for 0.52, where a
pull request that landed that afternoon kept the release waiting 76 minutes for
its translations. Now the packs are brought up to date between releases, and
release day only refreshes them from the release's catalogue.

`scripts/langpacks.mjs` works on the pack checkouts beside this one
(`../filex-lang-<tag>`; from a worktree, beside the main checkout), on the
directories in `FILEX_LANG_PACKS`, or on `--packs a,b`:

```bash
node scripts/langpacks.mjs status                # what every pack lacks against this tree
node scripts/langpacks.mjs status --keys         # ...key by key
node scripts/langpacks.mjs todo --out <dir>      # the translator's worklists and AGENT.md
node scripts/langpacks.mjs apply --worklist <dir> --commit
```

- **`status`** gives three lists per language: *missing* (no translation - the
  new keys), *changed* (translated for English that has changed since) and
  *removed* (keys filex no longer has: nothing to translate, `apply` and
  `release` drop them from the translation). A pack's `catalogue/` is the English its
  translation was written for, which is how a changed string is told apart:
  `pack.mjs sync` alone keeps the old translation of a reworded string without
  a word. `--json <file>` writes the report, `--check` exits 1 while anything is
  pending. `--catalogue <dir|vX.Y.Z>` compares with an exported catalogue or a
  release's instead of this tree.
- **`todo`** writes one JSON worklist per pack that needs something. Every item
  carries its English; for a changed one also the old English and the old
  translation; the Turkish reference, where the key is used, its table's
  syntax, and the plural forms the language needs beyond the catalogue's keys.
  Beside the worklists: the catalogue they were made from, `status.json` and
  `AGENT.md`, the instructions for whoever translates. No pack is touched.
- **The translation** is drafted by an agent, or written by a person, in the
  worklist (each answer is the line right after its item's `key`): the pack's
  glossary first, then the rules the worklist repeats - the fixed names (filex,
  ONLYOFFICE in that spelling, the protocol names), the plain hyphen, every
  placeholder, the syntax of the item's table, the language's own letters and
  one term per concept.
- **`apply`** checks every answer before it writes anything: the fixed names,
  then the language pack validator's rules on the answered keys, errors and
  warnings alike (the packs are kept at 0 and 0). It names the items to fix and
  touches no file while one is wrong. Then, per pack: the pack's own
  `pack.mjs sync` from the worklist's catalogue (so a tree that moved on since
  `todo` changes nothing), the keys filex no longer has dropped from every
  translation of the pack, the answers, `pack.mjs build`, the pack's `validate`
  script (written to `validate-output.txt`) and, with `--commit`, a local commit
  *Sync to filex `<commit>`: N of N* that names the dropped keys. A pack whose
  own validators go red is put back as it was. The pack's version does not
  move, and nothing is pushed.
- **A key filex drops leaves the packs on its own.** Every release drops some.
  The packs' `pack.mjs sync` kept such a key in `translations/<tag>.json`
  through 0.53, and the German, Spanish and French validators refuse it
  (`ERROR UNKNOWN`), so 0.53's `tenants.modeOff` sent every pack back until
  it was deleted by hand. `apply` and `release` (the template's example
  included) now remove it right after the pack's sync, filled or empty,
  whatever the pack's own script does; a plural form the language adds to a
  key filex still has (`<key>_few`) stays. Its wording is in the pack's git
  history.

**Every night** the build host translates what the packs lack against `main`,
after its [nightly run](#the-nightly-run) (`scripts/langpacks-nightly.mjs`).
The pack checkouts it commits to live there, not on a maintainer's machine,
which is not on every night:

```bash
sudo bash scripts/chain/install-langpacks.sh --root /var/lib/filex-langpacks \
  --env /etc/filex-langpacks.env --git-name "<name>" --git-email "<email>" \
  --init de,es,fr --dry-run                                          # once; then without --dry-run
node /var/lib/filex-langpacks/bin/scripts/langpacks-nightly.mjs check --probe          # what a night needs, proven
node /var/lib/filex-langpacks/bin/scripts/langpacks-nightly.mjs run --dry-run --no-wait  # what tonight would do
node /var/lib/filex-langpacks/bin/scripts/langpacks-nightly.mjs status                  # the last nights
```

The installer copies the driver and the modules it imports to `<root>/bin`
(the timer runs those copies; run the installer again to update them), clones
the packs given with `--pack <tag>=<URL>` into `<root>/filex-lang-<tag>` and
makes an empty checkout for each `--init <tag>`, a pack with no clone URL that
a maintainer pushes in (`git push nightly main`, below). In every pack
checkout it sets the identity of the nightly commits and
`receive.denyCurrentBranch=updateInstead`, so that release day can push the
release commit back. It writes the settings file once
(`scripts/langpacks-nightly.env.example`) and installs
`filex-langpacks.{service,timer}` from `scripts/chain/systemd/`. It installs
no agent: Claude Code is checked for, and named when it is missing.

- **When.** The timer starts `run` at 03:30 (Istanbul). It waits while the
  nightly run's `tonight.json` - a file on the same host,
  `LANGPACKS_NIGHT_FILE` - says the night is going and its process is alive,
  at most `LANGPACKS_WAIT_MAX_MIN` (150) minutes; a night that did not start,
  or whose process is gone, is not waited for. The whole run, the wait
  included, has `LANGPACKS_BUDGET_MIN` (330); a pack it could not start by
  then waits for the next night. It is a timer of its own rather than a step
  of the nightly run: the packs follow `main` whatever the night's tests
  said, a night that did not run still translates, and the translation needs
  neither the build lock nor the quiet windows.
- **What.** It fetches `origin/main` (`LANGPACKS_REF`) into the nightly run's
  checkout and checks it out in a worktree of its own (`<root>/tree`, outside
  that checkout, which the nightly run cleans with `git clean`; removed after
  the run), then runs `status --check` there - nothing pending, nothing is
  done. Then, pack by pack: `todo`; one agent session on copies (the
  worklist, `AGENT.md`, the catalogue, the glossary and the pack's
  translation) in a directory of its own, with the prompt
  `scripts/lib/langpacks-prompt.md` (the worklist's rules and the fixed names,
  word for word); its answers taken
  into todo's worklist; `apply --commit`. Answers `apply` refuses, and a pack
  whose validators go red (put back as it was), go back to the agent with what
  was said, `LANGPACKS_FIX_ROUNDS` (1) more times.
- **The boundary.** The agent is Claude Code with no tool that runs a command
  or reaches the web, no MCP server, none of the host user's settings or
  hooks, a HOME of its own, its file tools confined to its directory, and
  every permission prompt refused (`claudeArgs` in
  `scripts/lib/langpacks-nightly.mjs`); the model is always named
  (`LANGPACKS_AGENT_MODEL`, `opus`). Only `text` and `forms` cross from what it
  wrote: the worklist `apply` reads - and the pack it names - is todo's, in a
  directory the agent never sees. The pack checkouts, the tree and that
  directory are measured before and after every session; a change there stops
  the night with nothing applied.
- **Its credential.** The project's Claude account, asked from the work
  server at every run (`account.credential` over MCP, `LANGPACKS_WORK_URL`,
  with a project token read from `LANGPACKS_WORK_TOKEN_FILE`): the host keeps
  no copy of the credential, and a token renewed in the vault is the one the
  next night uses. A run by hand can name a token file instead
  (`LANGPACKS_CLAUDE_TOKEN_FILE`).
- **Its account's limit.** A session that the account's usage or rate limit
  ends (HTTP 429, read from Claude Code's JSON result: `accountLimit` in
  `scripts/lib/langpacks-nightly.mjs`) stops the night there: no fix round,
  the packs left are not started, nothing is applied. The night is not red:
  its title says `Claude account limit (429): translation tried again next
  night`, its first line quotes the account's words (when the limit resets),
  and it is told at severity info with no agent woken. Nothing is kept that
  holds the next night, which translates as any other.
- **What it says.** One notification (`FILEX_NOTIFY_*` for a person - the
  test chain's key will do - and `FILEX_WAKE_*` for an agent, as the release
  train's tools): per language, what was translated and what is left, every
  red pack with what `apply` said, the commits made. A night with nothing
  pending says nothing (`LANGPACKS_NOTIFY`). The worklists, the prompts and
  the agent's logs of the last `LANGPACKS_KEEP_NIGHTS` (14) nights stay in
  `<root>/nights`, with `last.json` and `history.jsonl`; outside every
  repository. Exit 0 green or nothing to do, 1 something left or red, or the
  account's limit, 2 could not run - a failed unit.

Nothing is pushed or tagged from the build host: the commits wait in its pack
checkouts for release day. Every setting, with its default, is in
`scripts/langpacks-nightly.env.example`; `web/tests/i18n/langPacksNightly.test.ts`
holds the decisions, the boundary, the credential and the installer. A string
that landed after the night is still untranslated at the cut: release day
names it (below).

**On release day** - step 13 of the [Release process](#release-process) - on
the maintainer's machine, where the tags are signed. Each pack checkout there
names the build host's checkout as its remote `nightly`, once:

```bash
git -C ../filex-lang-de remote add nightly ssh://<build host>/var/lib/filex-langpacks/filex-lang-de
```

Then:

```bash
node scripts/langpacks.mjs pull                      # the nightly translations, fast-forwarded
node scripts/langpacks.mjs release X.Y.Z --dry-run   # every step, nothing kept
node scripts/langpacks.mjs release X.Y.Z
```

`pull` fetches every pack's `nightly` remote and fast-forwards to it; a
checkout with changes of its own, or one both sides moved on, is named and left
for a person. `release` refuses a pack whose `nightly` remote still has
commits it lacks (pull first), and a remote it cannot reach is said and not
waited for. For every pack it refreshes the catalogue from the release's own
(`filex-catalogue-en.json` and `filex-catalogue-context.json`, attached to the
GitHub Release; `--catalogue <dir>` for a copy), drops the keys the release no
longer has from its translations (as `apply` does: a key can go after the last
night, or on a night with nothing to translate and so no `apply`), moves the
version one patch up in `filex-app.json` and `package.json`, rewrites the
README's status block -
everything between `<!-- langpack:status -->` and `<!-- /langpack:status -->` -
runs the pack's validators, commits, and prints the signed tag and the push
commands: to the pack's own remote (never `nightly`), and `push nightly <branch>`,
so that the build host's checkout takes the release commit and the next night,
and the next `pull`, go on from it. It signs, tags and pushes nothing itself,
and run a second time for the same release it finds its own commit and changes
nothing. A pack that still lacks a translation of the release's catalogue
(work that landed after the last night) is held back with the keys named:
`todo --catalogue vX.Y.Z`, translate, `apply --commit`, then `release` again.
A string whose English changed holds the pack back the same way;
`--keep-changed` releases it with the old translation and says so in the
commit. The template (`filex-lang-template` beside the packs, or
`--template <dir>`) gets the release's catalogue in the same run, because it
is what a new translator starts from: its example language keeps exactly the
catalogue's keys, its README names the version, and it takes a push but no
version and no tag.

---

## Release process

Maintainer-only. Reproducible, automated by CI.

**When: one train a day, cut at 10:00 Istanbul time (07:00 UTC).** If `main`
holds work the last tag does not, that day's minor is cut from `main` as it
stood at 10:00; if it holds nothing new, there is no release that day. Work that
lands after the cut, pull requests from outside included, waits for the next
day's train: a train that has left takes nothing new on board. Two kinds of
security work are told apart:

- **A hole someone can exploit in a released version** ships at once as its own
  patch release (`X.Y.Z+1`) and never rides a minor that is already running.
  Its branch stays off `main` and off the public repository until that patch's
  release window.
- **Hardening with no live hole behind it** (a second layer, a consistency fix)
  is ordinary work: it lands on `main`, rides the next train, and the CHANGELOG
  says `Security` and nothing more.

When GitHub's macOS runners are down, the release goes out without the macOS
packages and its notes say so; once the runners are back, a run started by hand
with `only=macos` adds them (step 8; 0.52.0 did it with run 37379732291). The
other channels do not wait for macOS: the dry run of step 7 is then started by
hand without the macOS row (`-f macos=false`), and the tag run promotes the rest.
When GitHub Actions as a whole is down, CircleCI stands in for the gate and the
release is packaged from a maintainer's machine:
[When GitHub Actions is down](#when-github-actions-is-down).

**Cut it with `pnpm release X.Y.Z`.** The steps below are what that command
does, in this order, and every step it can check is a gate that stops the
release when it is red - there is no option to skip one, and an option it does
not know is refused rather than ignored. It never commits the export, signs,
pushes or deploys: at those steps it stops, prints the exact commands, and on
`--resume` reads back what was done - each tag's signature and target, what both
remotes now hold (a public tag naming a private commit is refused out loud), and
what the servers, the update feeds and docs.filex.sh actually serve. The one
thing it starts on GitHub is a dry run of `release.yml`, which publishes nothing
(step 7). The two judgements no script can make are a person's to confirm: the
README, screenshot and documentation audit (steps 1-3, `--ack audit`) and the
parts of the deploy nothing can read back (`--ack deploy`). Off Windows there
is a third: leaving the Store copy's check for a Windows machine
(`--ack store-deferred`, step 6).

**The tag comes last.** Both `main` branches are pushed without a tag, GitHub
tests that very commit, and only a commit that passed is tagged (steps 7-8). A
red run spends no version number: fix `main` and resume.

```bash
pnpm release 0.45.0 --plan      # every stage and gate, in order; runs nothing
pnpm release 0.45.0 --dry-run   # every gate, nothing written: the stamp goes to
                                # a scratch copy, the export to a throwaway clone
pnpm release 0.45.0             # stops at the first red gate or person's step
pnpm release 0.45.0 --resume    # carry on (exit 3 = waiting for you, 1 = a red gate)
pnpm release 0.45.0 --status    # where the recorded run got to
pnpm release 0.45.0 --resume --only deploy   # once the tags are out: the deploy
                                # checks alone, on the tagged commits
pnpm release 0.45.1 --profile patch   # X.Y.Z+1: builds and packaging always,
                                # the rest only for what changed (step 6)
pnpm release 0.45.0 --profile full    # every suite here, before the export
pnpm release 0.45.0 --resume --no-cache   # run every gate, even one that
                                # passed on exactly these inputs before
pnpm release 0.45.0 --resume --chain result.json   # a green scripts/chain run
                                # on this tree stands in for the heavy gates
pnpm release 0.45.0 --resume --gate circleci   # GitHub Actions is down: the
                                # gate reads CircleCI (see the last section)
pnpm release 0.45.0 --resume --ack store-deferred   # off Windows: the Store
                                # copy's check is left for a Windows machine (step 6)
pnpm release 0.45.0 --resume --ack store-checked    # it ran green there since
```

> ⚠ The gates live in `scripts/release/plan.mjs` (this repository's list,
> guarded by `web/tests/deploy/releasePlan.test.ts`) and
> `scripts/release/stages.mjs` (the order, and what every release checks).
> A new rule goes there, not only on this page: on the night of 2026-09-24/25
> four releases were re-cut, each for a step that was written down here and
> skipped anyway.

**Around the tool, three more commands** ([The release train's
tools](#the-release-trains-tools)): `pnpm train X.Y.Z` writes the day's
release note (the rule above, the cut, what is on the train, the merge queue),
`pnpm merge-queue --queue <note>` merges the train's branches one after another,
and once the tag run is green `bash scripts/train/filex-ship.sh X.Y.Z` does the
deploy and steps 9-12 and ends with the deploy checks. A long step wakes whoever
waits for it (`scripts/train/when-done.mjs`) instead of being watched.

1. **Re-read `README.md` against what actually shipped since the last tag.**
   Run `git log --oneline vPREVIOUS..HEAD`, then ask of every new surface - a
   client, a feature, a docs page - whether it appears in *Features* and
   *Documentation*, and which part of *Why filex* it belongs to. In *Features*
   it is an entry in the full list, and a word in the table above that list
   only when one of its rows no longer covers it. In *Why filex* it goes into
   the block under its part, the one that opens on a click; the part itself
   stays a line, at most four bullets and one picture, so a new surface
   replaces words there or stays out of it. The opening is not a list of what
   shipped either: the three-line description, the buttons and six cards. Read the
   comparison table against today as well: what it says another product is,
   and every "no" it says of filex. The README is the page most readers see
   and the one nobody remembers to touch: a feature documented only under
   `docs/` does not exist as far as a new reader is concerned. Update
   `docs/README.md` (the index) in the same pass, and carry the change into
   the translated READMEs or leave them behind whole ([Docs](#docs)).

   > Why this is step 1: by 2026-08-13 the desktop app, folder sync, the CLI,
   > trash & versioning, E2E folders and self-update had all shipped - six
   > minor releases' worth - and not one of them had reached the README.

2. **Retake the screenshots and look at them.** Bump `SHOTS_RELEASE` in
   `e2e/shots/release.mjs` to the release being cut (it is the version the
   pictures print), then take them **in the build host's test chain**, where
   the published set is taken (its Playwright container and fontconfig; a run
   on another system stages nothing and `accept` refuses it), into the release
   checkout:

   ```bash
   CHAIN_EXTRAS=shots bash scripts/chain/run.sh --profile targeted --src <the release checkout>
   ```

   and **open the contact sheet it prints**. It lists only what moved: the
   pictures whose pixels changed since the published set, beside the published
   ones with a map of the difference, the new ones and the removed ones; the
   rest were taken again and found the same, or not taken because nothing they
   show changed. Every listed picture in English, current, nothing covering it.
   The last night's sheet (`out/shots/` of the nightly run) shows the same
   changes the morning after they landed, so release day holds no surprise.

   It takes every scene in one place (task #187): the six that call
   `findApp` or `documentServer` too, with the app builds and the language
   packs the chain's settings name (`CHAIN_SIGN_APP_DIR`,
   `CHAIN_CONVERT_APP_DIR`, `CHAIN_LANG_ES_DIR`, `CHAIN_LANG_DE_DIR`,
   `CHAIN_LANG_FR_DIR` - the builds of this release) and against the chain's
   own Document Server. Until 0.53.0 those six were taken on the build host
   itself, and the README showed its typeface beside the chain's.
   `langpack.mjs` refuses a language pack that does not cover this tree
   whole (`node scripts/langpacks.mjs status`); the other scenes are still
   taken, but the run failed - here red, where the nightly run only warns -
   and `accept` refuses it. Until the packs are
   translated, take the rest with `CHAIN_SHOTS_ONLY` (the other scripts'
   names, comma-separated) on the same command, and the published language
   pack picture stands (0.53.0: 319 strings missing in every pack).

   Then publish and link them:

   ```bash
   node scripts/shots-site.mjs upload
   node scripts/shots-site.mjs accept --looked
   ```

   `accept` refuses until every new picture answers from filex.sh with exactly
   its bytes, then writes `e2e/shots/manifest.json` and points the README, the
   docs and filex.sh at the new URLs; commit those. The audit runs
   `node scripts/shots-site.mjs verify --live` and stops while any page links a
   picture that is not published - the public README is exported from this tree.
   [Screenshots](#screenshots) says what the command checks on the way;
   `e2e/shots/README.md` the rest.

   > Why this is a numbered step: by 2026-08-14 `share-modal.png` showed a
   > share dialog with no download limit - a control that had shipped two
   > releases earlier - and `viewer-markdown.png` had Turkish buttons in it.
   > The v0.41.0 set then had to be taken three times by hand: scripts that no
   > longer fit the UI, and a binary carrying a 16-hour-old interface that
   > passed every API check. `pnpm shots` exists so neither happens again.

3. **Audit the documentation on every surface. Never skip this.** The README
   pass above is one leg of it; a feature can be finished, tested and shipped and
   still not exist for anybody who did not write it.

   ⚠⚠ **Do not work from a fixed list** - a list looks complete, and the surface
   that is not on it gets skipped. The rule is *every text that describes the
   product or explains how to use it*. Find them first:

   ```bash
   ls **/README.md docs/*.md docs/index.md
   grep -rn '"description"\|description:\|<Overview>' package.json packages/*/package.json \
     deploy/helm/*/Chart.yaml deploy/*/*/umbrel-app.yml deploy/*/docker-compose*.yml \
     deploy/runtipi/*/config.json deploy/unraid/*.xml deploy/portainer/*.json
   ```

   In this repo that is at least twelve places:

   | Surface | Why it counts |
   |---|---|
   | `README.md` | step 1 above |
   | **`site/index.html`** | the **filex.sh landing page** - the first thing anyone reads about the product, and it drifted two months and a dozen features out of date while it lived only on the static host. Deployed with `scripts/sync-site.sh`. Both live in the maintainers checkout only; the page is this project own site, not part of what you install |
   | **`web/src/views/Login.vue`** (`demo.*` in `web/src/locales/*.json`) | the **demo.filex.sh landing page** - rendered by the app when `FILEX_DEMO_MODE=true`, so it looks like code and gets audited like nothing. It went untouched from 2026-05-07 to 2026-09-05 still selling *"5 Storage drivers"*. Ships in the release image; see `docs/DEPLOY_BRF.md` §4b |
   | `docs/*.md` | the new feature has a page - **and the old pages are still true** |
   | `docs/README.md` | every `docs/*.md` is in the index |
   | **`docs/index.md`** | the docs site's **home page** - its hero line and feature cards are the first thing a visitor reads |
   | `docs-site/.vitepress/config.mts` | the new page is in the **sidebar** |
   | `packages/*/README.md` | these are the **npm pages** - an export nobody documents does not exist for anybody installing the package |
   | `desktop/README.md` | what the app actually does |
   | `deploy/*/README.md` | install instructions per target |
   | `deploy/umbrel/*/umbrel-app.yml`, `deploy/casaos/*` (`x-casaos.description`), `deploy/runtipi/*/metadata/description.md` and `config.json`, `deploy/unraid/filex.xml` (`<Overview>`), `deploy/portainer/template.json` | **app-store listings** - public product copy. Five stores, and the Runtipi one is a whole markdown page rather than one line, which is exactly why it is the one that rots. The Unraid and Portainer ones are XML and JSON, so a search for `description:` passes them by: at the 0.56 audit both still said only *e-Signature and Convert* ship, two apps and three releases behind |
   | `deploy/helm/*/Chart.yaml` | shown by `helm search` |
   | `package.json` descriptions | shown on npm |
   | `deploy/compose/*.yml` | new env vars and **published ports** with the traps beside them |

   > ⚠ The dangerous case is not a missing page, it is a **page that lies**.
   > On 2026-08-17 `STORAGE.md` still said *"There is no `nfs` or `smb` driver,
   > and there doesn't need to be"* - the `smb` driver had shipped in that very
   > release.

   > ⚠ A surface does not have to be a `.md` file. The demo landing page is
   > markup and translation strings, so it reads as code and slipped every
   > documentation pass for four months - while being, for anyone who clicks
   > *Try the live demo*, the **first** description of the product they meet.
   > Ask what a text *does*, not what extension it has.

   > ⚠⚠ A page missing from the sidebar is **not** unpublished. VitePress builds
   > every file under `srcDir`, so it is reachable by URL and indexable whether
   > or not anything links to it. To actually keep a page off the site, add it to
   > **`srcExclude`**. On 2026-08-17 five pages were live but unreachable from the
   > nav, and `CLOUD.md` - whose own first line says *"NOT a live service"* - was
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

   # the site must BUILD - VitePress fails the build on a dead link
   # ⚠ a subshell: the two commands after this one are repo-root-relative,
   # and for a while this line was a bare `cd docs-site` that left them inside
   # docs-site. The YAML command below then globbed `deploy/**` from there,
   # matched nothing, and printed `yaml ok` without opening a single file.
   # This build writes NOTHING - see the note below. `git status` must be as
   # clean after it as it was before.
   (cd docs-site && npm run build)

   # …and every in-page ANCHOR must land, which the build says nothing about.
   # VitePress fails on a dead PAGE link and ignores the `#section` half
   # entirely: 98 of 366 in-page links were dead on a green build (2026-09-05).
   # It reads the ids out of the HTML the build just produced, so run it after.
   node scripts/check-doc-anchors.mjs

   # every YAML you touched still parses - breaking a store listing is silent
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
   > measured rather than guessed - both commits built, the emitted `id=`
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
   > written against the GitHub rendering - the repo README, both npm package
   > READMEs, every in-page TOC - was already correct, which is why the *rule*
   > was changed instead of the 98 links; and an unmatched fragment lands the
   > reader at the top of the right page, not on a 404. 245 hand-maintained
   > `<a id>` aliases would need their own check to stay honest and would clutter
   > markdown that is also read on GitHub, where those spellings never existed.
   >
   > ⚠ A URL fragment is **never sent to the server**, so a Caddy rule, a
   > VitePress `rewrite` or a `_redirects` file cannot rescue an old anchor -
   > only a per-heading `<a id>` or client-side JS can. If the rule is ever
   > changed again, re-run the measurement (build at both commits, diff the
   > emitted `id=` attributes per page) before deciding what it costs.

   > ⚠⚠ **This gate does not refresh `RELEASES.md`, and that is deliberate.**
   > `npm run build` used to be `npm run releases && vitepress build`, so every
   > person running this mandatory step came away with two modified files -
   > `docs/RELEASES.md` and `docs-site/data/releases.json` - belonging to
   > nobody's change. On 2026-09-06 three agents hit it in one day, each
   > reverted it by hand, and one release nearly swept the churn into an
   > unrelated commit. A gate that dirties the tree it is gating is a trap.
   >
   > The build now runs `docs-site/scripts/check-releases.mjs` instead: it
   > asserts the generated page is present and lists at least one release,
   > offline, writing nothing. Refreshing is **step 10**, run on purpose after
   > the release exists. The generator is idempotent too - running
   > `npm run releases` when nothing has changed leaves both files untouched
   > rather than restamping today's date on them.

   ⚠ A relative link to a page that is in `srcExclude` is a dead link *on the
   site* even though it resolves in the repo - link those by full GitHub URL.
   That is how the "not published" list in `docs/README.md` broke the build the
   first time it was written.

4. Update `CHANGELOG.md` - move `[Unreleased]` to a dated `[vX.Y.Z]` heading.
5. **Every release updates every packaged deployment target. No exceptions.**
   (the maintainer's rule, 2026-08-29: *"her yeni tag'de versiyonda helm zorunlu"*.) Bump
   the `package.json` versions across all packages, then the deploy targets:
   ```bash
   pnpm -r exec npm version X.Y.Z --no-git-tag-version
   node scripts/sync-deploy-versions.mjs    # Helm chart + CasaOS + Umbrel + Runtipi
   ```
   ⚠ Run this **after** step 4, not before: the same script also derives
   Umbrel's `releaseNotes` from the `## [X.Y.Z]` section of `CHANGELOG.md`, and
   exits 2 saying so when that section does not exist yet. Those notes are
   generated rather than typed because a hand-written "what's new" carries no
   version number - a stale one describes a release the user is not getting and
   nothing about it looks wrong.
   ⚠ None of these are labels - each decides which image a real installation
   pulls. The chart's `values.yaml` ships `tag: ""` and the image helper
   resolves that to `.Chart.appVersion`; the three store manifests pin the tag
   outright and compare their `version` field to decide an update exists.

   ⚠⚠ It has gone wrong twice, the same way, because nothing failed when it
   drifted. The chart sat at `v0.4.0` for twenty-three releases (found
   2026-08-29). The fix covered only the chart - so on 2026-09-06 the three
   **store manifests were still at `v0.4.0`, twenty-nine behind**, and anyone
   installing filex from CasaOS, Umbrel or Runtipi got a build from February.
   `web/tests/deploy/deployVersions.test.ts` now fails the build if any of the
   seven pins drifts, and `--check` reports them without writing.

   The chart then publishes itself:
   [BRF-Tech/helm-charts](https://github.com/BRF-Tech/helm-charts) packages
   `deploy/helm/filex` from the tag and puts it in `https://helm.brf.sh` and
   `oci://ghcr.io/brf-tech/charts/filex`, nobody in the loop. Its `publish`
   workflow runs every ten minutes, but GitHub starts a schedule late or not
   at all (on that new repository it did not start for hours), so the ship's
   `helm` step starts the workflow itself
   (`gh workflow run publish.yml -R BRF-Tech/helm-charts`) and reads the
   index until it lists `appVersion: vX.Y.Z` - see
   [The ship](#the-ship-filex-shipsh). It publishes a
   chart version once and refuses a release whose chart reuses one with a
   different `appVersion` - which is why the script above bumps the chart's
   `version` whenever `appVersion` moves. A refused release shows as a red run
   in that repository's Actions.
6. Commit: `chore(release): vX.Y.Z`.
   ⚠⚠ **Not with `git add -A`, and not before these checks.** The release
   commit is the one commit in the project that is allowed to touch
   everything, which is exactly why it must not be written blind. The tool's
   `pretag` stage runs the fast checks on the release commit, here, before the
   export:

   ```bash
   git status --porcelain | grep '^??' && echo "untracked files - commit them or move them to their branch"
   pnpm -s --filter ./web build      # vue-tsc + vite, the gate nothing else runs
   (cd web && TZ=UTC npx vitest run) # the unit suite on CI's clock, which is UTC
   git archive HEAD | docker build --platform linux/amd64 -f docker/Dockerfile      -t filex:release-check -
   git archive HEAD | docker build --platform linux/amd64 -f docker/Dockerfile.slim -t filex:release-check-slim -
   (cd desktop && pnpm run build && node scripts/fetch-cli.mjs --platform win32 && pnpm e2e:store)  # Windows, Developer Mode: the Store package as a Store copy; no window comes on screen (--keep-windows-hidden), only a test toast
   ```

   **Off Windows the Store copy's check is deferred, never skipped** (#248).
   `store-e2e` installs and activates an MSIX, which only Windows can do (the
   GitHub runner never could). A release cut on Linux or macOS names the gate
   as `deferred` in its pretag and waits - before the other gates run - for a
   person to say so: `pnpm release X.Y.Z --resume --ack store-deferred`. The
   ack is bound to the release commit, like the audit's; on Windows it is
   refused, because there the gate runs. The Store still gets its bundle:
   `release.yml`'s Store job builds it (or takes the dry run's
   `release-files-store`), checks the package statically and submits it. The
   check stays owed in the run's record: `--status`, the deploy stage and the
   ship list the steps - on Windows with Developer Mode, in a clean checkout
   of the tag, the UI and the Windows binary built, then the gate's own
   command - until it ran green there and a person records it where the
   release was cut: `pnpm release X.Y.Z --resume --ack store-checked` (a
   complete release takes it too). Green means the Store copy's own checks
   printed PASS, not only "ALL PASSED": they do not run when the packaged app
   never opened. It does not hold the deploy.

   The heavy suites run before the tag too, but not all here and not all
   first. Where each runs is the **profile** (`--profile`, task #172):

   | Profile | Here, in `pretag` (before the export) | Here, during the `gate` stage | GitHub, on the export commit (step 7) |
   |---|---|---|---|
   | `minor` (the default) | the builds and vue-tsc, the embedded UI check, the unit suites in UTC, the desktop typecheck and unit suite, the Store copy, both images, the shop window instance | - (with `--gate circleci`: what CircleCI does not run) | every heavy suite, in `ci.yml`'s full matrix: the Go suite in shards and again under `-race`, the migrations on three engines and through the CLI, the unit suite on two clocks, Cypress, Playwright on three engines in parts and the Document Server specs with one, the store and S3 lines; and the dry run of the whole release |
   | `patch` (X.Y.Z+1 only) | the builds, the images, the desktop and the Store copy always; every other gate only when something it reads changed since the last tag; the Go suite on the changed packages and every package that imports one; the three engines when the schema changed | - | the same as a minor: every suite |
   | `full` | everything, as every release did up to 0.52 | - | the same |
   | `server` (X.Y.Z+1 that reaches the server alone, chosen by itself) | the builds, both images, the embedded UI check, the shop window instance, and the changed Go packages with their importers under `-race`; nothing of the interface or the desktop app | - | ci.yml's build part before the tag; every other suite behind the release, read by `matrix-watch.mjs` before `auto_ok` is turned on ([A server patch](#a-server-patch)) |

   Since `ci.yml` became the full matrix (#173), a minor runs no heavy suite
   here: every one has its parts on GitHub, and the `gate` stage reads them
   part by part (step 7). A heavy suite the CI being read does not run - with
   `--gate circleci`, Cypress and this machine's clock - starts when the wait
   starts and runs beside it, on the release commit, with the UI and the
   binary it tests built again there; the stage passes only when both did. A
   green run of the whole chain on the build host can stand in for them
   (`--chain <result.json>`, from `scripts/chain`): it must be a finished green
   run of a whole-chain profile, on a clean checkout of a commit this release
   contains, with nothing changed since but what the stamp writes. A green
   result of the nightly run (`runs/latest-nightly.json`, The nightly run,
   above) is such a run when `main` has not moved since the night. A patch
   profile is refused for anything but a patch step from the last tag; a patch
   after a release that was never published is a whole release - cut it as a
   minor.

   > Why the heavy suites left the fast pretag (#165, measured 0.45-0.52): the
   > pretag here took 71-90 minutes and, from 0.50 to 0.52, found no product
   > fault the build host and GitHub had not. 0.51 ran the Go suite five times
   > and vitest eight. Every red sent the next run back to the start - about
   > 935 minutes over seven releases - and every patch ran the whole chain.

   Gates run side by side in lanes (at most `jobs` at once, `plan.mjs`): the
   builds, the unit suites, the desktop, the images and the Go module's WSL
   mirror each keep one lane, and a gate that needs another's output waits for
   it. **A red gate does not send the next run back to the start.** A gate that
   passed on exactly the same inputs - the content of the files it reads, the
   command it runs, this machine - passes from the cache
   (`.git/filex-release/gate-cache/`) and says so: a fix in `e2e/` runs the
   builds and what reads `e2e/` again, and finds the Go suite and the
   migrations where they were. Only green is kept; an entry the tool cannot
   read, a working tree that is not `HEAD`, or an input outside the repository
   it cannot see means the gate runs. `--no-cache` runs every gate. Builds are
   never taken from the cache - later gates use what they make. The two
   images are (#165), on what each is made of: the files its Dockerfile
   copies, the Dockerfile, `.dockerignore` and the gate's own script - so a
   fix to a test, the docs or the desktop app does not build them again. The
   key leaves out the commit and the date the build stamps into the
   throwaway image (CI builds the published ones), and the entry keeps the
   image's id: a hit holds only while that very image is still under its tag,
   since the two image gates after it run it.

   **The images are built from the release commit, not from the working
   tree** (#165): `git archive` of the commit, streamed to `docker build -`
   (`scripts/release/gates/docker-build.mjs`). On the maintainer's workstation
   docker is the WSL docker-ce behind a forwarder, so `docker build .` read
   the checkout through WSL's 9P bridge, and BuildKit walked all of it - the
   `.git` directory, every worktree under `.claude/worktrees`, the ignored
   build output - before it sent a byte. Measured from the build logs: 30.4
   and 31.6 minutes per image at 0.55.0 (276 kB sent), 49.5 and 52.4 at
   0.54.0, while the builds themselves took one to five minutes; the four
   pretag runs of 0.55.0 took 4.5 of that day's hours, nearly all of it this
   walk. The commit's tree is ~70 MB and a second, and nothing loose in the
   working tree can reach the image. BuildKit applies no `.dockerignore` to a
   tar on stdin, so the gate first checks that `.dockerignore` leaves out no
   tracked file the Dockerfile copies - the context is then the one CI's
   checkout gives docker - and is red, before docker runs, when it does or
   when `.dockerignore` holds a pattern its reader does not know (`[...]`, a
   `\` escape, `**` inside a segment). With the walk gone the images need no
   local build: they start beside the UI build, and the pretag's longest path
   is the UI build and then the web unit suite - about 12-15 minutes.

   The export (the next stage) runs no test suite of its own any more: GitHub
   runs both on the very commit that lands, before anything is tagged. What it
   still checks is what nothing after the land would catch, or would catch
   only once it is public - what must never be published, the deletions and
   the executable bits, that the rewritten public module builds, goreleaser's
   check, and the workflow guards.

   Measured 2026-09-24, on v0.43.0: **the npm packages and the GitHub Release
   were published, and the container images were not.** The Dockerfiles'
   frontend stage copied `packages/` and `web/`, but the build configs import
   two files from `scripts/`; `vite build` failed on every attempt, and
   nothing local had ever built an image. `web/tests/deploy/dockerFrontendInputs.test.ts`
   now fails if the build reaches a file the images do not copy, but only a
   real `docker build` proves the rest of the recipe - so both images are built
   here, before the tag. v0.43.0's CI also failed a unit test that passed on
   the UTC+3 machine it was cut on; the suite runs under `TZ=UTC` here too, so
   the clock of whoever cuts the release cannot hide one again.

   Measured 2026-09-14, on v0.41.0: the explorer had been rebuilt and every
   account now landed on Home instead of the dashboard. Nothing local had run
   either end-to-end suite during the cycle, and both still waited for
   `/admin/dashboard` after signing in - so every spec behind the login helpers
   would have gone red in the tag's own CI run, after the tag was public, and
   held back every binary, image and package. Fourteen Cypress failures were
   stale selectors, not product bugs; that is only knowable by running them.

   Measured 2026-09-12, on v0.38.1: `git add -A` swept in two work-in-progress
   files from a feature branch - an admin page with no route, no menu entry and
   no translations. `vue-tsc` refused them, the release's own test suite failed,
   and binaries, images and npm were all skipped. Nothing shipped, so the tag
   was deleted from both remotes and re-cut on the corrected commit; the version
   number survived because nothing had been published under it.

   The backend suite and every documentation gate above pass without compiling
   a single line of frontend, so the admin build is the only local check that
   would have caught it - CI caught it afterwards, when the tag was already
   public. (Since #76 GitHub tests before the tag; the admin build still runs
   here first, in every profile.)
   ⚠⚠ **Nothing publishes over a red suite, and it used to.** Everything in
   `release.yml` that publishes - binaries, images, npm, the installers -
   waits for its gate, `verify` (step 7 says what it checks). Before
   2026-09-06, CI ran on the branch push and the release on the tag pushed two
   seconds later, in parallel and unaware of each other, with no required
   status check anywhere in the repository. Measured 2026-09-06: **CI had been
   red since v0.31.0 and four tags shipped over it.** The failure was real (a
   user who had chosen Turkish saw an English admin panel on any second
   device) and none of the steps above would ever have caught it - they check
   README, screenshots, links, anchors and version manifests, and never run a
   test.
   ⚠⚠ **Both images are built before the tag, and nothing can switch that
   off.** Until v0.43.2 the release called `ci.yml` with `skip_docker: true`
   ("the release's own docker job builds the same image") - but `binaries`,
   `docker` and `npm` start *beside* one another once the gate passes, so when
   v0.43.0's images failed, npm and the Release were already public. The input
   is gone, `ci.yml`'s image job has no `if:`, and since #174 the images a tag
   publishes are the ones the dry run of its commit built and smoke-tested
   (step 8): a tag run builds none. `web/tests/deploy/releaseGatesImages.test.ts`
   fails if a switch comes back, if a publishing job stops waiting for
   `verify`, or if `verify` stops asking for both runs of step 7;
   `releasePromote.test.ts` fails if a tag run builds an image or a desktop
   package again. (They read `.github/workflows`; in a checkout without them,
   point `FILEX_WORKFLOWS_DIR` at the published ones.)

7. **Push both `main` branches without a tag, and let GitHub test that
   commit.** Commit the export in the public checkout - one commit, exactly
   what the export staged, no `Fixes #N` (it would close the issue) - then
   push the private `main` and the public `main`, and no tag. On `--resume`
   the tool starts

   ```bash
   gh workflow run release.yml -R BRF-Tech/filex --ref main -f publish=false
   ```

   a dry run of the whole release on that commit - both images (each
   smoke-tested on its own architecture), goreleaser `--snapshot`, every
   desktop package including arm64, nothing published - and waits until it
   and `ci.yml`, which the push started, have both passed on the export
   commit. The workflow names that run `dry run all <commit>` (its
   `run-name`): GitHub's API does not give a run's inputs, so the name is how
   the tool, and later the tag run, tell the dry run of everything from any
   other run started by hand.

   **The test is the push run: `ci.yml`'s full matrix** (#173). Every heavy
   suite is a set of jobs that run side by side on GitHub's runners, each part
   named by `scripts/ci-parts.mjs`: the Go suite in the shards of
   `scripts/test-shards.json` beside PostgreSQL, MySQL, Redis and Samba, and
   again under `-race`; `go vet`; the migrations on three engines and through
   the CLI (up, three steps down, up); the unit suites in UTC and on Istanbul's
   clock; the typechecks and the docs gates; Cypress; Playwright on Chromium,
   Firefox and WebKit, each in parts (`--shard i/N`), the specs whose path
   differs with an ONLYOFFICE Document Server again with one, the store
   install and the S3 lines; both images. Its last job, `All tests (full)`, is
   green only when every part was. The gate reads the run **part by part**: a
   red part stops the wait at once and is named, with its job's link, and a
   run that passed without a part is no full matrix and does not pass. A pull
   request runs the same less `-race`, Firefox, WebKit and the Document Server.
   The dry run runs no suite of its own any more: it packages at once, beside
   the matrix, so the slower of the two sets the time (the goal: 50 minutes).

   **The dry run is the release candidate** (#174). On a version no tag has
   yet it stamps that version itself (not `-dryrun`), pushes both images to
   ghcr.io by digest only - with no tag anybody pulls - and keeps each desktop
   row's files with their sha256 sums for 14 days; the tag run of the same
   commit promotes them (step 8). The gate checks it kept them all; macOS alone
   may be missing, when GitHub has no macOS runner and the dry run was started
   by hand without it:

   ```bash
   gh workflow run release.yml -R BRF-Tech/filex --ref main -f publish=false -f macos=false
   ```

   The release then goes out without the macOS packages, and says so.

   When GitHub Actions cannot run anything, this step waits on CircleCI
   instead: [When GitHub Actions is down](#when-github-actions-is-down).

   **A part red on the runner is re-run once, by the tool** (#165): when every
   step that failed or was cancelled in a red part is one that only sets the
   machine up - GitHub's job setup or service containers, a checkout, a
   toolchain, a cache or an artifact moved, the browser install through apt
   (`checks.mjs` → `INFRA_STEPS`) - the gate waits for the run to finish,
   asks GitHub once (`gh run rerun <id> --failed`), keeps that with the
   release's state and reads the new attempt. A part red again after it, and
   any part red in a step of its own (a test, a build, a script), is red as
   before: the tool never re-runs a red test - a flake is a finding, and a
   person decides it is one ([Known flaky tests](#known-flaky-tests)). A part
   GitHub copied into the new attempt without running it again (lesson
   #1275) is asked for once more, twice in all. Measured at 0.55.0: two
   Playwright parts hit the browser install's 10-minute limit on apt, and the
   gate waited 38 minutes for a person to type the re-run. And GitHub not
   answering - a 5xx, a timeout, a dropped connection - is asked again at the
   next poll, ten polls in a row at most, instead of stopping the gate: 0.55.0's
   stopped on one `Server Error (HTTP 502)` while reading a run that was green.
   A 4xx is an answer, and stops it.

   A red run spends no version number, because nothing is tagged yet:

   * a flake in a test: re-run its failed jobs (`gh run rerun <id> --failed`,
     the tool prints it), then `--resume`;
   * a fault in the code: fix it on `main` - a commit on top of the release
     commit - push it, and `--resume`: the chain, the export, this step and
     the gate run again on the fix;
   * a fault in a workflow: fix it in the public checkout, commit, push
     `main`, and `--resume`: the export has nothing new to stage, and the gate
     runs on the new public `main`.

   > Why the tag moved to the end (#76): the tag used to start the test. 0.43.0
   > shipped without images, 0.43.1 stopped at the gate, 0.44.0 and 0.44.1
   > failed in goreleaser after npm and the images were out, and 0.45.0
   > published nothing - five numbers, each one fixable only by the next. From
   > 0.45.1 `main` went out first and the tag waited for CI by hand; the tool
   > now does it, and adds the dry run, because 0.44.0 and 0.44.1 died in a
   > build step no test runs.

   > ⚠ `ci.yml` never cancels the run of a push: it groups a push by its
   > commit, so a later push of `main` cannot take the verdict of the commit
   > being released away (only a pull request's superseded run is cancelled).
   > Until #174 `release.yml` also called `ci.yml`, which then ran with the
   > caller's `github` context: grouped by the ref alone, the dry run on `main`
   > and the CI run of that push shared a group, and the newer cancelled the
   > older - one of the two runs the tag waits for.

8. **Tag the commits GitHub tested, then push the tags.** The tool prints
   both commits: `git tag -s vX.Y.Z -m "vX.Y.Z" <release commit>` in this
   checkout and `git tag -s vX.Y.Z -m "vX.Y.Z" <export commit>` in the public
   one - **signed**, and `git tag -v vX.Y.Z` must answer `Good signature`
   before you push. Releases up to and including v0.27.5 are plain annotated
   tags: the instruction said `-s` for months while no signing key existed, so
   nobody could follow it and nobody noticed. The maintainer key is
   `EFA3B126 2FD99280 0DBBB5E3 A8FEBA97 FF786513` (ed25519, expires
   2028-08-31); its passphrase and a recovery copy live in the team vault, not
   on disk.

   Then push each tag by name, one at a time
   (`git push origin refs/tags/vX.Y.Z`), never `--tags`: a checkout
   accumulates local tags, and `--tags` publishes every one of them, including
   any you were not ready to release.

   The tag run publishes. Its gate, `verify`, publishes nothing unless GitHub
   holds a successful `ci.yml` run started by a push and a successful run named
   `dry run all <commit>` on the tagged commit itself, and it fails when it
   cannot ask - so a tag on a commit nobody tested publishes nothing. (A tag
   pushed before the gate was green waits the same way: when the gate is
   green, re-run that tag run's failed jobs.) `verify` also asks that the
   `ci.yml` run was the full matrix - its `All tests (full)` job passed.
   When GitHub lacks either run on the commit - it was gated on CircleCI
   while Actions was down - `verify` takes a green CircleCI `ci` workflow on
   that commit instead, and only then
   ([When GitHub Actions is down](#when-github-actions-is-down)); GitHub's
   own runs, when they are there, are all it reads.

   **The tag run promotes; it does not build again** (#174). It takes the
   images and the desktop files the dry run of its commit kept: each image
   digest is pulled on a runner of its architecture and must say, in its
   labels, that it was built from the tag's commit as the tag's version, and
   answer `filex --version` with both (`promote-check`); only then are the
   digests joined into the tags people pull (`docker-manifest`) - minutes after
   the tag, not the 30-50 a rebuild took. Each desktop row downloads its files
   and checks them against the sums the dry run wrote, byte for byte, and its
   update feeds against the version (`release-files.mjs check`), before
   anything is attached or sent to a store. The CLI binaries are the one
   thing still built from the tag: goreleaser is what creates the Release,
   opens the winget pull request and commits the Homebrew cask, and GoReleaser
   OSS cannot publish a build it did not make. A dry run that kept nothing
   (its version was tagged already, or what it kept expired) leaves `verify`
   red and nothing published: start a dry run on that commit again, then
   re-run the tag run's failed jobs. A tag run with no dry run to promote -
   a commit CircleCI passed while Actions was down, which `verify` lets
   through on CircleCI's word - builds both images and every desktop row
   itself instead, as tag runs did before #174, and checks and tags the
   images the same way (#181). A run started by hand that adds the ARM packages to a release (`-f only=arm64
   -f publish=true`) is asked the same of that tag's commit, so it publishes
   nothing for a tag cut before this check existed. So is one that adds the
   macOS desktop packages and the Homebrew desktop cask (`-f only=macos
   -f publish=true`), for a release whose tag run could not build them
   (0.52.0: GitHub Actions was down, and every other channel went out from a
   PC), and one that adds the Linux arm64 snap alone (`-f only=snap-arm64
   -f publish=true`: snapcraft cannot cross-build it). That one sends the
   snap to the Snap Store and attaches `filex-desktop-arm64.snap` and no
   other file: the rest of the Release stays byte for byte as published,
   since the winget pull request and the update feeds pin its files by hash.
   And one sends a release to the stores its tag run never reached
   (`-f only=stores -f publish=true`; 0.53.0: GitHub failed to create the
   tag run's desktop jobs, with an internal error, on a run it would not
   retry): the amd64 snap to the Snap Store, the desktop app's winget pull
   request and the Microsoft Store bundle. It builds nothing the Release has
   and attaches nothing: the windows and linux rows download the Release's
   `filex-desktop-x64.exe`, `filex-desktop-arm64.exe` and
   `filex-desktop-amd64.snap` by name and check them against the digests the
   Release gives, so the winget manifest hashes the very files its URLs
   name. No Release carries the Store bundle, so the store row sends the one
   the dry run of the tag's commit kept (checked against its sums), or
   builds it from the tag when none was kept (a commit gated on CircleCI, or
   a dry run past its 14 days). The Homebrew desktop cask is `only=macos`'s.
   None of them re-runs the tag run, and none builds or publishes anything
   else. When the tag run has no Store job, the release tool's Store gate
   reads the `only=stores` run's (`publish stores vX.Y.Z <commit>`): a
   bundle built or promoted, and submitted without a warning. With neither -
   a bundle submitted by hand in Partner Center - it reads the Store's
   public listing instead (green once certification passes, hours to three
   working days later). A Store job that ran and failed stays red.

   After the tags are pushed, the `ci` stage waits for the tag's run to
   finish before it reads what it published (#165; the plan's `tagRunWait`:
   a poll a minute, up to two hours, or at once when no run of the tag has
   appeared in 15 minutes). However that run ended, what it published is then
   read and judged exactly as before: waiting passes nothing. 0.55.0's `ci`
   stage read everything red three minutes into a 32-minute tag run, and the
   next look was a person's resume half an hour later.

   Once a tag is on a remote, `--resume` never goes back to the stamp or the
   test chain, whatever `main` has done since: the release is the tag. When
   the `ci` stage is held red by something that is not ours to hurry (0.51.0:
   a Snap Store review still open from the previous release),
   `pnpm release X.Y.Z --resume --only deploy` runs the deploy checks alone,
   on the tagged commits, and confirms nothing.

   A `published` gate may say it is such a thing (`held` in
   `scripts/release/plan.mjs`): the Snap Store's, which reads the release's
   revision back on `stable`, `candidate` and `beta` for amd64 and arm64
   (#265). When it is red, the `ci` stage tells a person at once through the
   maintainers' notifications (`scripts/lib/notify.mjs`, the train settings'
   `FILEX_NOTIFY_*` and `FILEX_WAKE_*`) - which channel and architecture lag,
   and the commands to look (`snapcraft status filex-app`) and to release a
   revision by hand - instead of leaving it in a log nobody reads. When the
   held gates are the only red ones, the stage's record says so (`held`), and
   whoever drives the release next goes on to the deploy checks.

   > ⚠ The public halves of steps 7 and 8 happen in the checkout whose
   > `origin` is **GitHub** - that is what `release.yml` watches. Development
   > happens on GitLab; the public tree is produced by
   > `scripts/export-public.sh`, and the signed tag is made there, on the
   > commit that is actually published.

   > ⚠ **An export that refuses half-way leaves that checkout half rebuilt.**
   > `scripts/export-public.sh` checks the tree after it has rewritten it, so
   > a refusal (a private name, a server address, a dead link, a missing
   > workflow script) leaves modified, deleted and untracked files behind: at
   > 0.50 it was 795, 8 and 726 (2026-10-03). Put the checkout back before you
   > fix the source and run the export again - and not with `git stash`, which
   > keeps the half-rewritten tree around to be applied somewhere later.
   >
   > The `grep` below is the proof that every untracked file is one the export
   > copied in from the private tree; the export refuses to start on a checkout
   > with changes, so nothing else can be. If it prints a line, stop: that
   > file is not the export's, and deleting it loses somebody's work.

   ```bash
   cd /path/to/filex-export
   git reset -q          # unstage, in case it got as far as `git add -A`
   git -c core.quotepath=off ls-files --others --exclude-standard > /tmp/left-over
   git -C /path/to/private/checkout -c core.quotepath=off ls-tree -r --name-only HEAD > /tmp/private-tree
   grep -vxF -f /tmp/private-tree /tmp/left-over      # expect: nothing
   tr '\n' '\0' < /tmp/left-over | xargs -0 rm -f
   git checkout -- .
   git status --short    # expect: nothing
   ```

   > What the export refuses is looked for only in the files it publishes -
   > the tracked ones and the untracked ones the checkout does not ignore - so
   > ignored build output can no longer stop it. (At 0.50 it was the previous
   > release's UI in `backend/embed/web`, which the release copies the new UI
   > into only after the export; the export now empties both embed
   > directories.) `bash scripts/export-public.sh --scan-only <checkout>` runs
   > just those checks on a checkout and changes nothing.

   > ⚠ **The export copies every file's executable bit from the private tree.**
   > On Windows git cannot see the bit, so the export's `git add -A` used to
   > stage every new file without it: `e2e/realenv/run.sh`, which
   > [Against the real servers](#against-the-real-servers) tells you to run as
   > it is, reached the public repository unrunnable, and nothing said so. The
   > export now sets each file's bit in the index it stages to what the private
   > `HEAD` says (`--modes-only <checkout>` runs that step alone), and the
   > release's export stage reads the bits back and stops on a difference.

   > ⚠⚠ **That checkout refuses a push that could publish the private history.**
   > On 2026-08-27 a `git push … main --tags` from the *private* repository
   > sent 47 of its release tags to GitHub: `main` was refused (unrelated
   > histories), the tags were not, and each one made a private commit - with
   > its whole history - reachable by SHA. Deleting a tag does not take that
   > back; GitHub and every fork keep the objects. So the public checkout has a
   > pre-push gate, `scripts/hooks/export-pre-push.sh`, installed with
   > `bash scripts/hooks/install-export-hook.sh /path/to/public/checkout` and
   > tested with `bash scripts/hooks/test-export-pre-push.sh` (all three live
   > in the maintainers' checkout only; the installer copies the gate, so
   > re-run it after changing the gate). It refuses:
   >
   > * a tag on a commit that is not on `main`, moving a published tag, and
   >   deleting one - a published tag is permanent; fix forward with a new
   >   version;
   > * a `main` that does not fast-forward, and deleting `main`;
   > * any ref whose new commits bring in a root commit - a history that does
   >   not grow out of the public one, which is exactly what the private
   >   repository is;
   > * new commits in which gitleaks finds a secret or a private name: its
   >   default rules; GitLab tokens matched to their full length (the stock
   >   rule stops at 26 characters, which is not even half of the routable
   >   `.01.` form); credentials written as `${VAR:-value}` shell defaults; the
   >   private forge path; host names and addresses under the project's own
   >   domain other than the two contact addresses and the Helm repository
   >   (`helm.brf.sh`, which users add); the server addresses, the
   >   names (people, tenants, customers, the project's own machines) and the
   >   withheld files that `scripts/export-public.sh` itself lists; and
   >   handover notes anywhere in the tree. The names are looked for in what
   >   the commits change, with the export's own exceptions (`CHANGELOG.md`,
   >   the release notes, the SQL migrations, the workflows), never in a
   >   commit's author or message.
   >
   > Pull-request branches (anything under `refs/heads/` except `main`) are
   > rebased, so they may be force-pushed and deleted; they still pass the
   > root-commit check and the scan. A machine without gitleaks is refused,
   > not skipped.
   >
   > ⚠ On Windows, a hook that is **killed by a signal** reads to git as
   > success: MSYS reports the signal in the high byte of the exit code and git
   > keeps only the low one. So `git push … | head -30` once published a commit
   > the gate had just refused - `head` closed the pipe, the gate died of
   > SIGPIPE, and git sent the push (2026-09-28). The gate now ignores SIGPIPE,
   > runs itself as a child, and passes a push only when that child reached its
   > verdict. Still: run a push into a file or straight to the terminal, not into
   > `head`, and judge it by what the remote holds afterwards.
   >
   > **Never push with `--no-verify`.** There is no case where it is the right
   > answer: a refusal means something about to become public should not -
   > stop and tell the maintainer first.

CI does the rest (GitHub Actions `release.yml` on the tag: the `verify` gate
of step 8, then these jobs):
- `binaries` (needs `verify`) - goreleaser: multi-arch binaries → the GitHub
  Release. It is what *creates* the Release, so `desktop` below depends on it.
- `docker` - **in the dry run only** (a tag run promotes, #174), and in a tag
  run with no dry run to promote (a commit gated on CircleCI, #181): a **matrix**,
  one native runner per architecture (amd64 on `ubuntu-latest`, arm64 on
  `ubuntu-24.04-arm`), each image smoke-tested on its own architecture and, in
  a release candidate, pushed by digest with no tag, its digest kept for the
  tag run. arm64 used to run under QEMU and took 20-30 minutes.
- `promote-check` (needs `verify`) - one native runner per architecture: each
  digest the dry run pushed (or, with none to promote, `docker` in the same
  run), pulled and checked - built from the tag's commit, as the tag's
  version, `filex --version` saying both.
- `docker-manifest` (needs `promote-check`) - joins the two digests into the
  tags people pull: `:vX.Y.Z`, `:slim-vX.Y.Z`, `:full-vX.Y.Z`, `:latest`,
  `:slim`, `:full`.
- `desktop` (needs `binaries`, **not** `docker`) - one matrix job per OS. In
  a tag run it builds nothing: it downloads the files its dry run kept, checks
  their sums and the feeds' version, and attaches them and sends them to the
  stores; the rows are the ones the dry run kept files for (without macOS
  after a macOS-less dry run). A tag run with no dry run to promote (#181)
  builds every row of the plan instead, as a dry run does. Before v0.25.0 it
  waited for the docker builds it never needed - ~25 idle minutes a release.
  ⚠ The upload step globs `desktop/release/*.exe` (and `*.AppImage`, `*.deb`,
  `*.dmg`, `*.zip`, `latest*.yml`) rather than naming files, which is why the
  Windows portable `.exe` needed no workflow change - but it also means a
  target that silently stops producing an artifact shows up as a shorter
  release, not as a failure. The `What was produced` step exists to make that
  readable in the log.
  ⚠ The **filex.sh/desktop/ feed is uploaded by hand**, and a portable copy's
  *App settings → Updates* **Download** button points into it. Put
  `filex-desktop-portable-x64.exe` there with the installer, or that button
  leads to a file that is not on the server.
- `npm` (needs `verify`, nothing else) - publishes every package under
  `packages/`: `@brftech/filex-core`, `@brftech/filex`, `@brftech/filex-react`
  and (since 0.48) `@brftech/filex-app-ui`, the SDK an app's own interface
  bundles. ⚠ The core package depends on the SDK at run time, so a release
  whose npm job cannot publish it leaves `@brftech/filex-core` uninstallable.

  It publishes through npm's **trusted publishing** (OIDC), with **no token**
  (#146): the job holds `id-token: write`, and npm trades the job's GitHub
  identity for a short-lived publish token per package, with provenance. The
  token it used to read (`NPM_TOKEN`, an npm granular token, 90 days at most)
  ran out unnoticed and 0.50.0 went out without its npm packages.
  - **Every package has a trusted publisher on npmjs.com** (the trusted
    publisher section of the package's Settings page): GitHub Actions, organization
    `BRF-Tech`, repository `filex`, workflow file `release.yml`, no
    environment, and `npm publish` among the allowed actions. Every field is
    case-sensitive. With npm 11.15.0 or later the same is
    `npm trust github <package> --repo BRF-Tech/filex --file release.yml --allow-publish`.
    A package without one is refused (`ENEEDAUTH`) and the job fails.
  - **A new package is published by hand once**, before the release that
    first ships it: npm sets a trusted publisher only on a package that
    already exists. Publish its first version from a maintainer's machine
    (`npm login`, then `pnpm publish --otp <code>` in its folder), add its
    trusted publisher, and only then tag. The job's `pnpm publish` skips a
    version npm already has.
  - The job installs npm 11 over the npm 10 that Node 22 brings and checks
    both before it publishes: trusted publishing needs npm 11.5.1 or later on
    Node 22.14.0 or later.
  - npm checks the provenance against each package's `repository.url`,
    letter case included (a mismatch is refused with `E422`). The export
    writes `github.com/brf-tech/filex`, GitHub signs `github.com/BRF-Tech/filex`,
    so the job sets the url from the repository the run is in before it
    publishes.

  The workflow change is `packaging/ci/release-npm-trusted-publishing.patch`;
  it reaches the public checkout as its own commit, landed with the export
  that brings its guards (`web/tests/deploy/releaseNpmTrusted.test.ts`).

**After the tag run: one command.** The deploy of the maintainers' instances
and steps 9-12 below - and the rest a script can do after them - run as
`bash scripts/train/filex-ship.sh X.Y.Z`: a backup, the trusted-proxy check,
the deploy instance by instance with its rollback, both feeds and the CDN purge,
the Releases page, the Helm chart, docs.filex.sh, npm and the embeds, and last
`pnpm release X.Y.Z --resume --only deploy`
([The ship](#the-ship-filex-shipsh)). The steps below are what it runs, and
what to do by hand where it is not set up; step 13 it leaves to a person and
prints.

**The workflow patches, in order.** The public workflows change only through
`packaging/ci/*.patch`, each a `git diff` of the public checkout, and the
ones not yet published apply on top of one another in this order:

1. `release-npm-trusted-publishing.patch` (#146): npm without a token.
2. `ci-full-matrix.patch` (#173): `ci.yml` as the full matrix, and its helper
   scripts under `.github/workflows/scripts/` (`ci-apps.sh`, `ci-fonts.sh`,
   `ci-docserver.sh`, `ci-s3.sh`, `migrate-roundtrip.sh`; the first two are
   gone again with 7).
3. `release-promote.patch` (#174): `release.yml` without its own test suite,
   `verify` asking for the full matrix, the release candidate and the
   promotion, `-f macos=false`, and `release-files.mjs`.
4. `release-verify-circleci.patch` (#181): `verify` takes a green CircleCI
   `ci` workflow on the commit when GitHub lacks its full matrix or its dry
   run, through `.github/workflows/scripts/circleci-green.mjs`; such a tag
   run has nothing to promote, so `docker` builds the images in it and
   `desktop` builds every row. It needs the `CIRCLECI_TOKEN` secret
   ([When GitHub Actions is down](#when-github-actions-is-down), "Setting it
   up").
5. `release-snap-core24.patch` (#68, 0.55): the desktop job on Node 22
   (electron-builder 26) and snapcraft + LXD on both Linux rows, for the
   core24 snap.
6. `release-desktop-upgrade.patch` (#68, 0.55), on top of 5: before
   `desktop-arm64-check` installs this build fresh, each of its machines
   installs the previous release's packages, opens them, and installs this
   build over them the way an installed copy updates itself - the NSIS
   installer with `--updated /S --force-run` (x64 and arm64), `dpkg -i` over
   the .deb, the AppImage replaced in place, the snap refreshed to a new
   revision - and wants one copy, of this version, where the old one was,
   with the user's data. The scripts are new:
   `.github/workflows/scripts/previous-release.mjs` (the newest release below
   this build's version), `desktop-upgrade-windows.ps1` and
   `desktop-upgrade-linux.sh`; the Windows row's artifact carries the x64
   installer as well. The first run of it is the release's dry run.
7. `ci-browser-prep.patch` (#181, 0.56): every browser part of `ci.yml`
   (`playwright`, `playwright-ds`, `playwright-extra`) is prepared by
   `node scripts/ci/browser-prep.mjs apps` and `fonts`, the script CircleCI's
   Playwright job calls too, instead of `ci-apps.sh` and `ci-fonts.sh`,
   which it deletes. The apps are `e2e/helpers/app-locations.mjs`'s,
   fetched without a token (so the three jobs lose their `GH_TOKEN`), the
   font map is the build host's; `ci-docserver.sh` names the new step. It
   needs `scripts/ci/browser-prep.mjs` in the tree, so it lands with the
   export that brings it.
8. `release-snap-channels.patch` (#265, 0.57): each Linux row's snap goes to
   the Snap Store's `stable`, `candidate`, `beta` and `edge` in one upload
   (`snapcraft upload --release=stable,candidate,beta,edge`), and the step's
   summary and its manual-review warning name the four. Up to 0.56.3 a
   release went to `stable` alone, and the three preview channels sat on
   0.52.0, amd64 only. The release tool reads `stable`, `candidate` and `beta`
   back at its `ci` stage, and a channel that lags is said to a person at once
   (`held`, below) - not `edge`, which the nightly run moves on after a
   release ([The Snap Store's channels](DESKTOP.md#the-snap-stores-channels)).
9. `release-winget-first-package.patch` (#68, 0.57), on top of 8: until
   winget has the desktop app (`manifests/b/BRFTech/filex-app` is not on
   winget-pkgs' master), the release does not touch it while a first pull
   request of ours for it is open (the owner's decision of 2026-10-16): the
   windows row's new step "Does the desktop app wait for a winget
   moderator?" (`.github/workflows/scripts/winget-pending.sh`, new) only asks,
   says `skipped: BRFTech.filex-app waits for a moderator on #<number>` (a
   notice and a line of the run's summary) and sets `WINGET_DESKTOP_SKIP`,
   which skips the submission, the CLA step and the closing - no new pull
   request, no update of the open one, no comment. When GitHub cannot be
   asked it skips as well, and says why; with no first pull request open it
   lets the release open one. `winget-supersede.sh` closes nothing of a
   package winget does not have yet. Fourteen first submissions passed
   validation and none was approved: each release opened a new one and
   closed the one before, and the moderators' queue started again. The
   release tool's winget read-back (ci stage) says the same in one line
   instead of a red gate (`wingetWaits`). Once winget has the package, every
   release submits a new version and closes the older one, as the CLI's
   `BRFTech.filex` does.
10. `release-aur-linux-only.patch` (#145, 0.57), on top of 9: the `aur` job
    needs the Linux packages, not the whole desktop matrix. It runs once every
    desktop row has ended, however each ended (`!cancelled()`), as long as
    verify passed and the desktop job ran at all, and its first step after the
    key asks the Release for `filex-desktop-amd64.deb` and
    `filex-desktop-x86_64.AppImage`: both there, it goes on; either missing,
    the Linux row did not publish, and the job is red. Up to 0.56 a Windows row
    that failed skipped the AUR as well. Without `AUR_SSH_PRIVATE_KEY` (the
    AUR's account registration was closed when `filex-app-bin` was to be
    published, and the channel stays closed) the job prints
    `skipped: no AUR key` and skips every other step - green, never red.
11. `nightly-desktop.patch` (#266, 0.57), on top of 10: a new workflow,
    `.github/workflows/nightly-desktop.yml` - every hour, the nightly source
    the build host pushed (`filex-nightly-src:nightly`) built into every
    desktop package and the arm64 snap when it is new, the packages pushed
    to ghcr as `filex-desktop-nightly:nightly-<date>` and `:nightly`, the
    arm64 snap uploaded to `edge` alone ([The nightly run](#the-nightly-run)).
    It touches neither `release.yml` nor `ci.yml`, and publishes nothing a
    release does.
12. `release-surfaces.patch` (#252, 0.57), on top of 11: a tag whose message
    says `surfaces: server` is a server patch ([A server patch](#a-server-patch)):
    `plan` reads the line (`surfaces` output; anything but none, `all` or
    `server` stops the run), `verify` publishes once ci.yml's `Go vet + build`
    part of the push run is green - polled while it runs, at most 45 minutes,
    and no dry run - with nothing to promote, `binaries` hands goreleaser
    neither the Homebrew nor the winget secret and `FILEX_SURFACES` (no
    "latest" release), and `npm` and `desktop` do not run. It needs
    `.goreleaser.yml`'s `make_latest` in the tree, so it lands with the export
    that brings it.

Apply them in the public checkout **after** the first preflight of
`pnpm release X.Y.Z` passed (it wants that checkout in step with GitHub) and
before `--resume` reaches the pretag, which runs their guards against them -
one commit each (`git apply <patch>`, then commit by path) - and never push
them on their own: the land pushes them with the export that brings their
guards (`ciFullMatrix.test.ts`, `releasePromote.test.ts`,
`releaseVerifyCircleci.test.ts`, `scripts/ci-parts.mjs`, `releaseArm64.test.ts`,
`releaseDesktopUpgrade.test.ts`).
A workflow pushed ahead of its export goes red on GitHub (0.52.0).

9. **Publish the two update feeds, then prove they moved.** CI attaches every
   artifact to the GitHub Release; it publishes **neither feed**, and a feed is
   the only place an installed copy ever looks. Both live on the static host and
   are deliberately excluded from `scripts/sync-site.sh` (so a website deploy
   cannot delete them) - which also means nothing refreshes them but you.

   - `filex.sh/updates/stable.json` - **the server and CLI**. Every install with
     `AUTO_UPGRADE` reads this and nothing else. Generate it, do not hand-edit
     it: `python3 scripts/gen-update-manifest.py --repo-dir <the export checkout>
     --previous <the live stable.json> --out stable.json` lists every published
     release with its digests, derives `migrations` from the tags, and carries
     over what a person decided in the live file - a kill switch, a security
     flag, a `min_version`, hand-written notes (`--no-auto vX.Y.Z` pulls a
     release out of automatic upgrades). Without `--previous` those decisions
     are silently undone. Point `--repo-dir` at the checkout the signed tags
     were made in: a release it has no tag for stops the generator rather than
     publishing a guessed `migrations: false`. Diff the result against the live
     file before you upload it.
   - `filex.sh/desktop/` - the desktop app. Upload the installers, the
     AppImage/deb, the dmg/zip, the portable `.exe`, and all three
     `latest*.yml`.

   ```bash
   curl -s https://filex.sh/updates/stable.json | head -5
   for f in latest.yml latest-mac.yml latest-linux.yml; do
     printf '%-18s %s\n' "$f" "$(curl -s https://filex.sh/desktop/$f | head -1)"
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
   > release, and neither gate is automatic - check the feeds, do not assume.

10. **Refresh the generated Releases page and commit it.** The GitHub Release
    now exists, so `docs/RELEASES.md` can finally include it - which is why
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
    > "Latest" blurb from the commit subjects, or as a bare `-`. That file
    > is hand-written from this repository's own `CHANGELOG.md`.
    >
    > ⚠ If GitHub is unreachable the generator keeps the committed cache, says
    > so loudly on stderr, and exits 0 - it never publishes an empty page. In
    > that case the new release is simply not on the page yet; run it again
    > later.

11. **Push the documentation prose to docs.filex.sh.** A cron on the server
    (`/root/filex-docs-refresh.sh`, versioned in the source repository as
    `docs-site/scripts/refresh-on-main.sh`, which the export leaves out)
    rebuilds and republishes the site -
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

    Then read the live page back - a page that builds is not a page that
    published:

    ```bash
    curl -s https://docs.filex.sh/RELEASES | grep -o 'Latest - v[0-9.]*'
    ```

    > ⚠ Keep `docs-site/node_modules` on the server: the refresh script builds
    > there and does not install. The `rm -rf` above is scoped to `docs/` for
    > that reason.
    >
    > Why this is a numbered step: measured 2026-09-05, hours after v0.32.0 was
    > tagged and deployed, `/root/filex-docs-src/docs/index.md` was still the
    > 4 September copy - the whole release's documentation round, including a
    > new feature card and a change to how every heading id is spelled, had not
    > reached the site. The cron had been running the whole time and was working
    > exactly as designed; the step it does not do is this one.
    >
    > ⚠ Step 10 above must already have happened: this copies `docs/` to the
    > server, and the refresh script there deliberately regenerates only
    > `RELEASES.md`. A `release-highlights.json` that never reached the server
    > gives the Latest blurb as a bare `-`.

12. **Check the shop window - what a stranger touches before they trust us.**
    Everything above audits the product from the inside: prose, screenshots,
    links, anchors, version manifests, and a test suite that runs against code.
    This step looks at the surfaces a first-time reader actually receives.

    ```bash
    # before the tag - needs a binary, no network
    pnpm run build:backend
    node scripts/check-shop-window.mjs --instance --boot bin/filex

    # after step 11 - needs the network, nothing else
    node scripts/check-shop-window.mjs --published
    ```

    Three exit codes, and the last two are the point: **0** everything checked
    passed, **1** a defect is present and the release does not go out, **2**
    something *could not be checked* - no binary, no network, a GitHub rate
    limit, a fixture that is not set up. ⚠ Exit 2 is deliberately not 1: a gate
    that turns an outage into a failed build is an outage of its own. It is
    also not 0 - the run says out loud which check did not happen, and you
    re-run it before you tag rather than assuming.

    The third of this gate that needs neither a server nor the network -
    the URL grammar, the quickstart command, the publish paths that carry no
    converter - is `web/tests/deploy/shopWindow.test.ts`, so it runs on every
    push and in `pnpm test`, and steps 6 and 7 already block the tag on it.
    Nothing to run by hand.

    ⚠ `--instance` boots a throwaway on port 5941 with demo mode on, an
    external service host set as a sentinel and its own temp data directory,
    interviews it and kills it. It never touches a live host: two of its
    checks write. Point it at a URL instead (`--instance http://127.0.0.1:…`)
    only for an instance you booted yourself, and expect `skip` rather than
    `ok` on anything its fixture does not cover.

    > Why this is a numbered step: on 2026-09-07, hours before the public
    > launch, a person looking at filex from outside found seven defects - and
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
    route table - 359 entries - and classifies every state-changing one by
    asking the running server whether a role gate stands in front of it
    (anonymous 401, signed-in non-admin 403). Every operator surface it finds
    must be refused on a demo, and no route an ordinary user may use may be.
    The six routes this script probes are the smoke test that the guard is
    installed at all; the Go test is what makes a **fourth** guarded prefix
    impossible to add unnoticed - it found `/metrics` on its first run. It runs
    in `go test ./...`, so step 6 already blocks the tag on it. ⚠ Nothing in it
    names a route, and it has to stay that way: the moment it becomes a list it
    stops covering the surface nobody has written yet.

    > ⚠ What this gate does **not** cover, so that nobody reads a green run as
    > more than it is:
    >
    > * **It does not look at a picture.** It compares the date a picture was
    >   last taken (`taken` or `checked` in `e2e/shots/manifest.json`) with
    >   commit dates - a screenshot older than the code that draws it cannot be
    >   showing that code - and it fails only once a picture has been left behind through
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
    > The demo host's own corpus **is** covered now - `--published` signs in to
    > demo.filex.sh with the credentials the demo publishes and types the
    > queries the splash advertises - but only when the demo is reachable. An
    > unreachable demo is a `2`, and a `2` means nobody proved anything.

13. **Bring the language packs to the release.** Once the GitHub Release
    carries `filex-catalogue-en.json`:

    ```bash
    node scripts/langpacks.mjs pull
    node scripts/langpacks.mjs release X.Y.Z
    ```

    then run the tag and push commands it prints, pack by pack - the last one
    of each, `push nightly <branch>`, gives the build host's checkout the
    release commit. The packs were translated against `main` every night on
    the build host; `pull` brings those commits home, and `release`
    refreshes their catalogue, moves their version and commits; a pack held
    back names the keys still to translate, or that it was not pulled
    ([Translations and language packs](#translations-and-language-packs)).

    > Why this is a numbered step: it lived in nobody's checklist but the
    > maintainer's memory, after the deploy, and through 0.52 it was also where
    > the release's new strings got translated - over an hour on 0.52's
    > critical path.

If something fails, fix forward - never delete a published tag.

### A server patch

A patch whose changes reach the server alone goes out on a lane of its own
(#252): about half an hour from the code being done to the maintainers'
instances running it, where a patch of every surface takes two hours - and
nothing it does not change gets a new version. The owner's rule, 2026-10-12:
if the CLI or the interface did not change, they are not updated.

**Which patch it is.** `scripts/release/surfaces.mjs` places every changed file
on the surfaces it reaches - the server (both images, the server archives and
the update manifest, the instances, the Helm chart), the CLI's package
managers, the interface (npm, the embeds), the desktop app (its packages,
feeds and stores) and the documentation - or on none (a test, the release
tooling, a note). A patch step (`X.Y.Z+1`) whose files reach the server, and
at most the documentation, with no migration and every file placed, takes the
`server` profile by itself; `pnpm release X.Y.Z` says so, and
`--profile patch` keeps the whole patch instead. A file no rule places makes
it a release of every surface: in doubt, everything is published. A backend
package counts as the CLI's when the CLI's commands (`backend/cmd/filex`, every
file but the server's own - `main.go`, `settings.go` and `office_engine.go`,
`SERVER_COMMANDS`) reach it, directly or through another package - sync stands
on `syspath` and `regfile` though no command names them, and plugin validation
opens the storage and database layers (`CLI_PACKAGES`, 39 of the module's
packages; `releaseSurfaces.test.ts` walks the imports and holds the list to
them) - and `backend/pkg/**` and the module's `go.mod` too. The handlers,
search, previews, office, sharing and the protocol servers are the server's
alone: the CLI binary links them, but no command runs them.

**What runs.**

- **audit**: steps 1-3 go by the changed files - a patch step that changes
  no file of the interface or the desktop app (a server patch always; a
  `patch` profile too when nothing else changed) passes the README and the
  screenshots with that reason, and the documentation check is the pages
  that describe what changed. The README is read when it changed itself;
  the CHANGELOG entry and the Releases-page summary are asked for as always
  (`auditAsks` in `scripts/release/stages.mjs`).
- **pretag**: the builds, both images and their smoke tests, the embedded UI
  check, the shop window instance, and the Go packages the patch changed and
  every package that imports them, **under `-race`** (`go-targeted.sh` with
  `FILEX_GO_RACE=1`; it needs a C compiler, and says so). No unit suite of the
  interface, no desktop typecheck, no Store copy: a server patch publishes
  none of them (`serverSkip`), so nothing is deferred to a Windows machine
  either.
- **gate**: GitHub builds the export commit - ci.yml's `Go vet + build` part
  of the push run, green (`serverGate`) - and no dry run is started.
- **sign**: both tags carry the line `surfaces: server` (the stage prints the
  commands with it, and refuses a server patch's tag without it, and any other
  release's tag with it).
- **the tag run** (`release-surfaces.patch`, below): `plan` reads the line from
  the tag; `verify` waits for that same build part instead of the full matrix
  and a dry run; `docker` builds both images in the run (there is nothing to
  promote); `binaries` makes the Release with the server archives and the CLI
  binaries, without the CLI's Homebrew cask and winget pull request, and does
  not mark it GitHub's latest release (`make_latest` in `.goreleaser.yml`):
  every `releases/latest/download` link names a desktop package or a CLI
  binary, and they stay on the last release that has them. `desktop` and `npm`
  do not run.
- **ci and deploy**: the release tool reads back what a server patch
  publishes and names the rest as not this release's (`surface` on each
  published gate of `scripts/release/plan.mjs`).
- **the ship**: backup, proxies, deploy, the manifest - with
  `--no-auto vX.Y.Z`, so `auto_ok` is off - the Releases page, the Helm chart,
  docs.filex.sh and the read-back; the desktop feed, its purge, npm and the
  embeds are listed as not this release's and left as they are.

**Behind it: the full matrix, then `auto_ok`.** The push of the public `main`
still runs ci.yml's whole matrix on the commit; it no longer holds the patch.
`node scripts/release/matrix-watch.mjs X.Y.Z` reads it part by part: green, it
regenerates the manifest with `FILEX_MANIFEST_AUTO=vX.Y.Z`
(`scripts/gen-update-manifest.py --auto`, which a publishing wrapper that
passes its own options alone carries too), purges it from the CDN, reads it
back and says so; red, it leaves `auto_ok` off and says which parts failed -
installs that update by themselves do not take the patch, and a person
decides (a new patch, or a re-run that passed and the watcher again). Its
outcome is kept in the release's record (`matrix`). The autonomous release
round starts it (`scripts/train/auto.mjs`).

### The autonomous release round

A release goes out from the release workspace on the build host, with nobody
at the keyboard (#165): the code is written anywhere, and once the work for a
release is on `main`, a timer cuts it. The audit stays a person's - the
README, the screenshots, every page that describes the product - and the
round releases exactly the commit it was done on.

**Queue it.** On the commit a person audited, with its Releases-page summary
in `docs-site/data/release-highlights.json` (step 10 above, written before,
not after):

```bash
node scripts/train/auto.mjs request 0.57.0 --summary "one line: what this release is" \
  [--commit <the audited commit; default: the private remote's main>] \
  [--profile minor|patch|full|server] [--task <work task the signing is recorded on>]
```

It refuses a version that is not after the newest tag, a summary that says
"Fixes #N" (on GitHub that closes the issue), and a commit without the
release's summary. `auto.mjs status` shows the request and the last rounds,
`auto.mjs cancel` drops it.

**The round** (`auto.mjs run`, every half hour; nothing queued, nothing
done): it fast-forwards both checkouts, wants them clean, on `main`, and the
private one on the audited commit, with `FILEX_AUTO_MIN_FREE_GB` (30) free,
then runs `pnpm release` and does what the tool leaves to a person:

| The release tool waits at | The round |
|---|---|
| audit | confirms it (`--ack audit`) only on the commit the request names |
| pretag, a check deferred to another machine | confirms the deferral (`--ack store-deferred`); the record keeps it owed |
| export, withheld files | stops: only a person confirms that list |
| land | commits the export (`vX.Y.Z - <summary>`) and pushes both mains |
| gate, ci | asks again, `FILEX_AUTO_WAITS` (8) times at most |
| sign | signs both tags (with `surfaces: server` for a server patch) under `FILEX_AUTO_SIGN_CMD`, which opens the signing key for that command alone |
| push | pushes the two tags, one ref at a time |
| deploy | runs the ship (`filex-ship.sh`), pushes its Releases-page commit, then `--ack deploy` - which the release tool takes only once its own read-back is green |

The stages that build (up to the export) hold `CHAIN_LOCK`; from the land on
it holds nothing, since it waits for GitHub. A server patch's round then runs
`matrix-watch.mjs`: GitHub's full matrix behind it, and `auto_ok` on.

**When it stops.** Red before the mains were pushed, the round rolls back -
the stamp commit is dropped, the export's changes go to a stash in the public
checkout, the release's record is kept beside under another name - and the
number is not spent: fix `main`, audit, queue again. Red or waiting after
that, nothing is undone (a pushed `main` and a tag are never moved): the
notification says where it stopped, with the release tool's own words. A
store that holds the read-back (a Snap Store review) is `held`: the round
ships what is out and reads the stores again every round, quietly, until they
pass. Every round that started is said to a person (`FILEX_NOTIFY_*`), and
`auto.mjs report` at 07:00 sums up the rounds since the last report and what
they left to a person: a deferred Store check, a held store, a matrix that is
not green, the replies on the issues, the language packs.

**Where it runs.** The release workspace (a container on the build host)
holds both checkouts, the release train's settings
(`~/.config/filex-train.env`, the keys in `scripts/train/train.env.example`),
the narrow GitHub token wrapper and the signing command. The build host
starts it: `scripts/train/install-auto.sh` installs
`filex-release-auto.timer` (a round every half hour) and
`filex-release-auto-report.timer` (07:00), which run
`scripts/train/auto-host.mjs` - it `docker exec`s the round in the
workspace, and when the workspace is not running it says so at once and
once a day, instead of rounds that never happen.

### The Store check without a Windows desktop

The deferral above is a bridge (#248). Windows is needed for the Store copy's
check - activating an MSIX - but not a person's Windows: what made GitHub's
runner fail every activation check from 0.44.2 on is its account. `runneradmin`
is the built-in Administrator renamed (RID 500): with no UAC and a full
administrator token, and an MSIX app's Chromium does not start under it
(actions/runner-images#9537). `store-e2e` says so itself now: every run prints
the account, its SID, its token's integrity level and the UAC policy, and a
failed activation reads the app's log where the Store copy writes it
(`~/.filex/desktop/logs`, then the package's redirected userData) and ends on a
verdict - the built-in Administrator, UAC off, or neither
(`accountVerdict` in `desktop/scripts/lib/store-activation.mjs`).

**Path A, tried first:** the same runner, another account.
`packaging/store-e2e-lab/` is all of a separate, single-purpose public
repository, so the trial changes nothing in this release:
`run-store-e2e-as.ps1` turns Developer Mode on, makes a local administrator
`e2e` with a random password (masked, kept nowhere), opens a Remote Desktop
session for it from the runner to itself with FreeRDP (`127.0.0.2`: the client
refuses `127.0.0.1`), and starts `store-e2e.mjs` in that session as `e2e`, not
elevated (`schtasks /ru e2e /it`), with neither `CI` nor `GITHUB_ACTIONS` set -
every check strict, none only a warning. `store-e2e.yml` checks a released tag
of `BRF-Tech/filex` out and builds what the release tool's check builds (the
UI, the Windows binary, the desktop app and its CLI) first. Set it up once and
run it on a released tag, three to six times (about ten minutes each):

```bash
gh repo create BRF-Tech/store-e2e-lab --public --description "filex: the Store package's activation checks on a GitHub runner"
# in a clone of it: packaging/store-e2e-lab/store-e2e.yml -> .github/workflows/store-e2e.yml,
# packaging/store-e2e-lab/run-store-e2e-as.ps1 -> run-store-e2e-as.ps1; commit, push
gh workflow run store-e2e.yml -R BRF-Tech/store-e2e-lab -f tag=v0.56.3
```

Green in every run - the Store copy's own checks printed PASS, the window
checks too - and the release's Store job takes the same steps: `release.yml`'s
`store` row runs `store-e2e` as a second account in its own session, the
release tool reads that job at the gate stage (green and with no Store
warning, as `storeJobProblems` reads the submission), and the pretag stops
deferring the check off Windows. Red, or a session that never opens, and path
B takes over: a Windows 11 virtual machine on the home server, signed in as an
ordinary user with Developer Mode on, which the release tool reaches over ssh.

### When GitHub Actions is down

On 2026-10-05 the 0.52.0 dry run could not get a runner, twice, and the
release went out from a PC. A runner of our own that takes its work from
GitHub stops with Actions, whatever machine it runs on. CircleCI schedules its
own jobs, and runs the same tests on every push of the public `main`
(`.circleci/config.yml`, workflow `ci`):

- the Go suite in the shards of `scripts/test-shards.json`, one copy of the
  `go` job per shard of the `go` profile ([Shards](#shards)), beside
  PostgreSQL, MySQL, Redis and Samba (`GO_SERVICES` in
  `scripts/ci-parts.mjs`, the list GitHub's Go parts are held to too), and
  red unless the engine suites really ran on both engines
  (`scripts/ci/engines-ran.mjs`: without a DSN they skip, and a skip is a
  pass); `go vet` and the embedded UI check in the build job;
- the web and package unit suites in UTC, and the desktop unit tests;
- the Playwright suite in Chromium, in parts (`--shard i/N`), against the
  binary the build job made (`BROWSER_BUILD`, what GitHub's build hands its
  parts), prepared as GitHub's parts are, by the same script
  (`scripts/ci/browser-prep.mjs`): the apps of
  `e2e/helpers/app-locations.mjs` from their public releases, and the build
  host's font map. The first run (2026-10-10) had neither, and every app
  spec failed to load.

When step 7 waits on a GitHub that cannot run anything, resume the gate on
CircleCI:

```bash
CIRCLECI_TOKEN=... pnpm release X.Y.Z --resume --gate circleci
pnpm release X.Y.Z --plan --gate circleci      # what is read from CircleCI, and what runs here
```

- The gate stage reads the `ci` workflow on the export commit instead of
  `ci.yml` and the dry run, and starts nothing on GitHub. The heavy gates
  CircleCI does not run - Cypress, the unit suite on this machine's clock -
  run here at the gate stage, beside the wait. A red run spends no number,
  as on GitHub: re-run the workflow from its failed jobs on CircleCI, or fix
  `main`, and resume.
- The choice is kept for later resumes, and `--gate github` names GitHub
  again once Actions is back. The record says which CI passed the commit
  (`pnpm release X.Y.Z --status`), and so do the tag commands the tool prints.
- It is a person's choice, never the tool's: a GitHub that answers red is a
  red gate, not an outage.
- ⚠ What CircleCI does not test is the release dry run: GoReleaser's
  snapshot, the images and the desktop packages are not built before the
  tag, and there is no release candidate to promote.
- **Once Actions is back, the tag run publishes the commit** (#181). Its
  `verify` reads GitHub first; missing the full matrix or the dry run on
  the commit, it asks CircleCI, and a green `ci` workflow on that very
  commit lets the run publish - a tag push, and a run started by hand that
  adds to a release (`only=arm64`, `only=macos`, `only=snap-arm64`,
  `only=stores`), alike.
  A red, unfinished or missing workflow, or a CircleCI that does not
  answer, publishes nothing. With nothing to promote, the tag run builds as
  tag runs did before #174: `docker` builds both images on their own
  architectures and pushes them by digest, `promote-check` checks them
  (built from the tag's commit, as the tag's version, `filex --version`
  saying both) and `docker-manifest` tags them; `desktop` builds every row
  of the plan, macOS included, and publishes it. That is the 30-50 minutes
  a promotion saves, back on the tag run; the record and the tag run's
  summary say which CI tested the commit.
- So a commit gated on CircleCI ships one of two ways: wait for Actions and
  push the tags (the tag run builds and publishes everything), or package it
  off GitHub now (below). Not both: a tag run whose tag has a GitHub Release
  already publishes nothing on CircleCI's word - `verify` stops it - so a tag
  run that starts late, once Actions is back, cannot tag images or attach
  files over the ones packaged off GitHub, which winget and the feeds pin by
  hash. Such a release gets its macOS packages and its arm64 snap from
  `only=macos` and `only=snap-arm64` (its amd64 snap by hand, see below),
  and reaches the Snap Store (amd64),
  winget and the Microsoft Store with `only=stores`, which `verify` lets
  through on CircleCI's word.

**Why the config lives in this repository** when the GitHub workflows do not.
`scripts/export-public.sh` keeps the public checkout's `.github/workflows` and
never writes them, and a change to them reaches the public repository as a
`packaging/ci/*.patch`: they hold the publishing tokens and the tag run, and
an export must never rewrite the pipeline that publishes it. The CircleCI
config publishes nothing, holds no secret and asks for none, and it calls this
tree's scripts (`scripts/test-shards.mjs`, `e2e/run.mjs`,
`scripts/ci/engines-ran.mjs`, `scripts/ci/browser-prep.mjs`), so it has to
change in the same commit as they do. It is ordinary source: the export copies it with the code it tests, and
the pipeline of a landed commit runs the config written for that commit. A
workflow pushed to the public repository ahead of its export goes red there,
which is what happened to a `packaging/ci` patch on 0.52.0.
`web/tests/deploy/circleciConfig.test.ts` holds the config to the shard list,
to `GO_SERVICES` and `BROWSER_BUILD`, to the shared preparation of the
browser parts (and to naming no app of its own), to the scripts it calls and
every file they import (in the tree, and not withheld by the export), to the
toolchain versions the tree pins (`backend/go.mod`, `pnpm-lock.yaml`) and to
the names the gate reads, on every web test run.

**Packaging off GitHub.** When the release cannot wait for Actions - the gate
on CircleCI, or a tag run that cannot get a runner - it is packaged from a
maintainer's machine: the path 0.52.0 took by hand, now one command that runs
release.yml's own steps on the tag's tree:

```bash
node scripts/release/package-local.mjs X.Y.Z                    # the plan: every step, its commands, what it publishes
node scripts/release/package-local.mjs X.Y.Z --run              # build everything; publish nothing
node scripts/release/package-local.mjs X.Y.Z --run --publish    # the GitHub Release (goreleaser), ghcr, the desktop files
node scripts/release/package-local.mjs X.Y.Z --run --only images,desktop-windows
```

- It refuses unless the public checkout holds the signed tag and GitHub has
  the same tag; with `--publish`, `gh` must be signed in and `docker login
  ghcr.io` done. It clones the public checkout at the tag (on Windows, the
  Linux steps run in WSL on WSL's own disk) and builds there, never in either
  checkout.
- goreleaser creates the Release with the CHANGELOG's notes; both images are
  built for amd64 and arm64 (arm64 under QEMU) with the labels and tags the tag
  run would give them; the Windows installers (x64 and arm64, one
  `latest.yml`, x64 first) and the Linux packages (x64 and arm64: AppImage,
  .deb, .rpm) are built, checked to be the architecture on their label, and
  attached.
- **macOS is never built here**, and neither is a snap: the release goes out
  without them and its notes say so. Since 0.55 both snaps are core24, which
  snapcraft builds in LXD (there is no prebuilt template any more), and WSL
  has no LXD. `only=macos` and `only=snap-arm64` add the macOS packages and
  the arm64 snap once GitHub can - after GitHub has tested the commit, since
  a run that adds to a release is asked what a tag run is. The amd64 snap is
  built on a Linux machine with snapcraft 8.x and LXD (`pnpm --filter
  ./desktop run dist:snap` in a tree at the tag) and attached with `gh release
  upload`, before `only=stores`; the notes print both commands.
  The stores need tokens GitHub alone holds: `only=stores` sends the amd64
  snap, the desktop app's winget pull request and the Microsoft Store bundle
  (the one the dry run kept, or built there: no Release carries one), and
  `only=macos` commits the Homebrew desktop cask. npm is a person's, with
  their own sign-in; the command prints what to run.
- Then `pnpm release X.Y.Z --resume --only deploy`.
  `web/tests/deploy/packageLocal.test.ts` holds the plan to these rules.

**Setting it up**, once, by the owner of the GitHub organization:

1. Sign in at circleci.com with GitHub and install the CircleCI GitHub App
   for `BRF-Tech/filex` alone.
2. Create the project for that repository, with the default config source,
   `.circleci/config.yml`, and a trigger on pushes (the config itself runs
   on `main` only).
3. Stay on the Free plan: a public repository on it gets CircleCI's
   open-source credits (400,000 a month for Linux, Arm and Docker, 30 jobs
   at once), with no application. Build no forked pull requests, pass no
   secret to them, and put no environment variable or context in the
   project: the config reads none.
4. Write the project's slug (Project Settings, Overview: `circleci/` and two
   ids on the GitHub App, `gh/BRF-Tech/filex` with OAuth) into
   `scripts/release/plan.mjs`, or set `CIRCLECI_PROJECT_SLUG`.
5. Make a personal API token (User Settings, Personal API Tokens) for the
   release tool. It goes in `CIRCLECI_TOKEN` when the tool runs, and in the
   team vault, never in a file.
6. Give the tag run's `verify` the same kind of token: a repository secret
   of `BRF-Tech/filex` named **`CIRCLECI_TOKEN`** (Settings, Secrets and
   variables, Actions; or `gh secret set CIRCLECI_TOKEN -R BRF-Tech/filex`,
   which reads it from stdin). `verify` hands it to `circleci-green.mjs`
   alone, which sends it as the `Circle-Token` header and never prints it;
   no other job reads it. If the project's slug is not `gh/BRF-Tech/filex`
   (a project on the GitHub App: `circleci/<org id>/<project id>`), set it
   as a repository **variable** `CIRCLECI_PROJECT_SLUG` too. Without the
   secret CircleCI may still answer for a public project; when it does not,
   `verify` says so and publishes nothing.
7. Read the first run once the config is on the public `main` (it gets there
   with the next export; CircleCI's Trigger Pipeline on `main` starts one by
   hand): every job green, and its test counts the same as GitHub's on the
   same commit, suite by suite.

### The release train's tools

Five commands in `scripts/train/` carry a release from the cut to the last
read-back (#178, #179, #165). They share one settings file - `~/.config/filex-train.env`,
or the file `FILEX_TRAIN_ENV` or `--env` names, KEY=VALUE, the process
environment winning - whose keys `scripts/train/train.env.example` lists with
placeholders. No server name and no secret is in the repository: hosts, paths
and the publishing commands are settings, and a key is a file (or the name of a
variable the session already holds) that is read when it is used.

> Why they exist (measured in #165): 0.51 took 10 h 34 min from its cut to the
> last surface, and a good part of it was not testing. Branches were merged by
> hand, five commands each, with the CHANGELOG conflict solved by a scratch
> script; the deploy and everything after it took 50-80 minutes of a person's
> turns in an order kept in notes; and the session that drove the release
> spent turn after turn asking whether a four-hour chain had finished.

#### The note: `pnpm train X.Y.Z`

Writes the train's note to `.git/filex-train/RELEASE-X.Y.Z.md` (never into the
working tree, whose first release gate is "clean"): the train rule above, read
from this page each time; the cut, 10:00 Istanbul of the day; `main` from the
last tag up to the cut, merge by merge, with the task numbers they name; what
landed after the cut, for the next train; a merge-queue block; the steps; and
the closing line - every task on the train moved to Done, with its evidence, in
the same turn. Before 10:00 the list is `main` as it is now and the note says
so; run it again after the cut. `--date`, `--onto`, `--out`, `--stdout`.

#### The merge queue: `pnpm merge-queue --queue FILE`

Merges the train's branches onto the release branch one after another, each on
top of the one before, each with `--no-ff` and its own message file, and checks
each merge before the next one starts. The queue is the first ```` ```queue ````
block of the note (or a plain file): a branch per line and, optionally, its
message file; without one it is `msg/<branch, "/" as "-">.txt` beside the queue.
A message is English and the merge commit's whole message, kept as written.

```bash
node scripts/train/merge-queue.mjs --queue .git/filex-train/RELEASE-0.53.0.md --plan
node scripts/train/merge-queue.mjs --queue .git/filex-train/RELEASE-0.53.0.md
```

For every line, in order:

1. a branch whose tip is already in the release branch is skipped - so running
   the queue again after a stop carries on where it stopped;
2. `git merge --no-ff --no-commit` with rerere on, so a conflict resolved once
   (in a rehearsal, or by hand) resolves itself the next time;
3. a conflict in `CHANGELOG.md` is merged by Keep a Changelog section
   (`scripts/train/changelog-merge.mjs`: every entry of both sides, ours first,
   an edited entry in its place, the heading once; an entry both sides changed
   is a person's call); a conflict in any other file aborts the merge, leaves
   the tree as it was, and the queue stops (exit 3) with the commands to merge
   that branch by hand;
4. `[Unreleased]` is left with one heading per kind even when git saw no
   conflict (a clean merge of five branches left three `### Changed` at 0.48);
5. the commit, with the line's message;
6. the checks: `[Unreleased]` well formed, `go build ./... && go vet ./...` on
   the module (through `scripts/lib/go-build.mjs`, so the maintainer's WSL works
   too; `--no-go` skips it), and every `--check '<command>'`. A red check stops
   the queue with the merge made: the queue never undoes a merge. Fix it on top
   and run the queue again, or take it back with the `git reset` it prints.

It never switches branches (the release branch must be the one checked out) and
does not start on a tree with changes, a merge in progress, a branch that does
not exist or a missing message. To rehearse a train, run it in a scratch
worktree of a throwaway branch made from `main` (`--repo`, `--onto`): the rerere
cache is shared, so what is resolved there replays on the real run.

`node scripts/train/changelog-merge.mjs` is the CHANGELOG part on its own:
`--git` during a conflicted merge (it reads the three versions git holds),
a file path for a file with conflict markers, `--normalize` and `--check`.

#### The ship: `filex-ship.sh`

`bash scripts/train/filex-ship.sh X.Y.Z` (or `pnpm ship X.Y.Z`), once the tag run
is green and in the checkout the release was cut in. The steps run side by side
where they can, each with its own log under `.git/filex-ship/vX.Y.Z/logs/`:

| Step | What it does |
|---|---|
| `preflight` | the tag in this repository and in the public checkout, the tag on GitHub, the public checkout clean, the release's record here, the server answering |
| `backup` | every instance into a new `pre-vX.Y.Z-<time>` directory on the server: its compose file, its settings (mode 600), the image it runs and a copy of its SQLite database made with SQLite's online backup, checked with `PRAGMA integrity_check`, with the newest migration it holds |
| `proxies` | every instance's network gateway, measured now, is in the trusted-proxy list it goes by - the one saved in its settings (`login.trusted_proxies`, which `FILEX_TRUSTED_PROXIES` only seeds since 0.57) when it holds one, else `FILEX_TRUSTED_PROXIES` as the compose file gives it; a gap stops the ship before anything moves, naming where to add the gateway |
| `deploy` | instance by instance, in the order the settings list them (the demo first: it is the canary): the image line, `docker compose up -d`, `/healthz`, the image it runs, its database at the newest migration the tag carries, its gateway trusted (by the same list the `proxies` step reads). A failed instance is rolled back - the compose file, and the database when migrations ran (the failed copy is kept beside it) - and the instances after it are not touched (`--on-fail hold` leaves it as it failed) |
| `manifest` | the update manifest servers and the CLI read (step 9) |
| `desktop`, `purge` | the release's desktop packages and feeds from the GitHub Release onto the feed host, then every one of those files out of the CDN's cache: their names never change, and a stale package fails the sha512 its new feed promises (step 9) |
| `releases` | the Releases page: the release's summary must be in `docs-site/data/release-highlights.json` and publishable, the page is regenerated and, when it changed, committed on `main` as `docs(releases): vX.Y.Z` (step 10; not pushed) |
| `helm` | the Helm chart (step 5): `SHIP_HELM_CMD` starts the charts repository's publishing, then `SHIP_HELM_INDEX` is read every 30 seconds until it lists the chart at `appVersion: vX.Y.Z`, for at most `SHIP_HELM_WAIT` minutes (15); an index that already lists it is green without starting anything. It waits only for `preflight`, so a red Releases page does not hold the chart back. Red at the end of the wait, with what the index lists and what to look at |
| `docs` | docs.filex.sh: the server's snapshot copied, the public checkout's pages and the summaries copied in, the site rebuilt (step 11) |
| `npm` | waits until npm serves every package of the release (the registry can lag minutes behind the job) |
| `embeds` | the vendored explorer moved to the release, and deployed when a command for it is set |
| `verify` | `pnpm release X.Y.Z --resume --only deploy`: the release tool's own read-back of the servers, the feeds, docs.filex.sh and the shop window (step 12) - the same checks, not a second set |

`--plan` prints every step and its command and runs nothing; `--resume`
carries on after a stop without running a step that passed again (backup,
proxies and deploy run again together while the deploy has not passed: every
attempt has its own backup); `--only a,b` runs single steps. A step whose
publishing command is not set is not run: it is listed at the end with what to
do by hand. What no step can do is listed at the end too - the language packs
(step 13, `node scripts/langpacks.mjs release X.Y.Z`), the replies on the
issues and pull requests the
release answers, the push of the `docs(releases)` commit, a check the release
deferred to another machine (the Store copy, when it was cut off Windows) - and then
`pnpm release X.Y.Z --resume --ack deploy`. The aim is 20 minutes from the
deploy to everything read back; the ship says when it took longer.

> ⚠ The server steps are files (`scripts/train/remote/*.sh`) sent over ssh on
> stdin, saved to a temporary file on the server and run there, with plain
> words as arguments (a value that is not one is refused before anything
> runs). A script written inline into an ssh command once lost a quote to an
> apostrophe in a comment, and the rest of it ran as root on the wrong side
> (2026-05-10).

#### Waking up: `when-done.mjs`

`node scripts/train/when-done.mjs --title "pretag 0.53.0" -- pnpm release 0.53.0 --resume`
runs the command as it is, passes its output through, keeps its exit code, and
when it ends says so: to a person through a JSON POST (`FILEX_NOTIFY_*`), and
to the agent waiting for it through an MCP server's `notify` tool
(`FILEX_WAKE_*`), with how long it took and its last lines. The merge queue and
the ship say so themselves when they end, and so does a run of the build
host's chain (`scripts/chain/run.mjs`, `CHAIN_NOTIFY_*`), all through
`scripts/lib/notify.mjs`.

#### The autonomous round: `auto.mjs`

`node scripts/train/auto.mjs request X.Y.Z --summary "..."` queues a release of
the audited commit, and `auto.mjs run`, started by the build host's timer,
cuts it with nobody at the keyboard - the person's steps of `pnpm release`
included, a red stage before the land rolled back
([The autonomous release round](#the-autonomous-release-round)).
