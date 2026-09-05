# filex admin Cypress suite

Browser tests for the admin SPA, the `/drive` end-user front door and the HTTP
contracts they are built on.

**They run against a throwaway instance this repo starts for them.** Not
against production, not against a shared staging box, and with no secret to
find first.

```bash
node e2e/run.mjs cypress --build     # first run: builds everything, then runs
node e2e/run.mjs cypress             # afterwards: a binary in bin/ is enough
node e2e/run.mjs cypress --spec "cypress/e2e/14-explorer-sidenav.cy.ts"
```

That command boots `filex serve` on a free port against a temp data dir with a
deterministic `admin@local` / `admin`, seeds one local storage, runs every
spec, and deletes all of it again. The exit code is the suite's.

## Why this suite exists next to `e2e/` (Playwright)

Two browser suites is a fair thing to be suspicious of, so here is the split in
one line each:

- **Playwright (`e2e/`)** walks **journeys**. One spec is one story a person
  lives through: upload a file, delete it, restore it from trash; create a
  share, open it in a fresh context, type the PIN. It is the release gate.
- **Cypress (`web/cypress/`)** pins **rules**. One spec is one surface with
  many small cases: what every admin GET answers, what shape the `external`
  envelope has, what the navigation panel does when you collapse it, what the
  OnlyOffice config endpoint returns when no Document Server is configured. It
  runs on every push and pull request.

The practical difference is that a Cypress case can assert the UI and the API
that feeds it in the same chain — `cy.request` next to `cy.get` — which is what
makes the contract sweeps (`70-admin-endpoints`, `65-manager-api`,
`95-capabilities-sync`) cheap enough to be worth having. A Playwright spec doing
the same would be a slower way to write the same assertion.

Rule of thumb when adding a test: **a story goes in `e2e/`, a rule goes here.**
A regression in a shared package usually deserves one of each.

## Running against something else

A live instance stays reachable, deliberately as an explicit act:

```bash
CYPRESS_BASE_URL=https://fm.example.com \
CYPRESS_ADMIN_PASSWORD=<from the vault> \
  pnpm --filter @brftech/filex-admin cy:run
```

⚠ Never in CI, and never as the default. The default used to be
`https://fm.example.com`, and that one line is why this suite was never automated: it
needed a secret nobody could commit, it could not be aimed at the build under
review, and a red run had two possible causes. It also hid real failures — see
the traps below.

`pnpm --filter @brftech/filex-admin cy:open` opens the interactive runner
against whatever `CYPRESS_BASE_URL` says (default `http://127.0.0.1:5212`).

## Layout

```
cypress.config.ts            baseUrl, env vars, retries OFF
cypress/
  e2e/
    00-smoke                 healthz, capabilities, the SPA boots
    05-routing               every admin route hydrates
    10 / 11 / 15-login…      auth happy + sad paths, logout
    12-drive-front-door      /drive — the end-user prefix (v0.30.0)
    13-navigation-ui         admin sidebar → route
    14-explorer-sidenav      the explorer nav panel: rail, drawer, views
    16-explorer-connections  "How to connect" + "API keys" from the panel
    17…45                    theme, dashboard, storages, users, shares,
                             replication, sync
    46-star-action           star as an ACTION (menu / toolbar / S / cards)
                             and the gate that hides it for an app token
    48-tags-panel            tags listed in the panel, and browsable
    49-virtual-segments      .trash/.recent/.starred/.shared read as names in
                             the tab strip, the breadcrumb AND the inspector
    50…62                    shares, sync, explorer
    64-token-kinds           app token vs user token on the four self-service
                             credential routes, BOTH directions
    65…86                    manager API, trash, versions, search, uploads
    87-search-fuzzy          filename search ORDER and FILTERING, not "returns
                             an array"
    88…99                    PWA, webhooks, OnlyOffice, capabilities, public
  support/
    e2e.ts                   global setup — read the two exception filters
    commands.ts              cy.apiLogin / cy.uiLogin / cy.adminGet
```

## Traps this suite has already paid for

- **The install banner covers the sidebar.** `InstallPrompt.vue`'s wrapper is
  `fixed inset-x-0 bottom-0 z-40` with no `pointer-events-none` (its neighbour
  `PendingOpsTray` has one), so the whole bottom strip of every admin page is a
  hit target — including the 256px the sidebar occupies. At the configured
  1440x900 viewport that is eleven nav destinations (Settings, Branding,
  Protection, External, Replication, Queue, Notifications, Webhooks, Plugins,
  Audit, Updates, About) that neither Cypress nor a person can click without
  dismissing the banner first. `support/e2e.ts` dismisses it before every load
  using the product's own key; `90-pwa-install` opts back in.
  **The overlap itself is NOT fixed — it is a real UI bug.**
- **Cypress calls a link clipped outside a scrollable ancestor hidden.** The
  sidebar is `overflow-y-auto`, so anything below the fold needs
  `.scrollIntoView()` before `.click()`. Without it seven cases in
  `13-navigation-ui` died in the hook.
- **`ResizeObserver loop completed with undelivered notifications` is not an
  error.** It is a browser notice, and the explorer triggers it on any viewport
  change because it watches its own container to pick narrow mode. Cypress
  fails the test on it unless it is filtered — which is why there was no
  drawer/narrow coverage at all.
- **A failing test makes the NEXT `cy.visit` in the same file hang for the full
  60-second page-load timeout.** One broken selector therefore reads as two
  failures and costs a minute. Fixing four real assertions took the same 47
  specs from 9m58s to well under three.
- **Go serializes an empty slice as `null`, not `[]`** — `{"nodes": null}`,
  `{"items": null}`. Assert the envelope, or seed a row and assert the array.
- **`omitempty` fields are absent, not null.** `storages[].replica_target_id`
  only appears once a storage is paired.
- **Do not hardcode a list of external slots.** The old specs asserted
  `mermaid`, which this build has not had for a long time — they passed only
  because production's `external` table still holds the row from an older
  version. Read the slots from `/api/files/capabilities` and assert the two
  endpoints agree.
- **The login identifier is `input[name="email"]`, not `input[type="email"]`.**
  It accepts a username too, so it cannot be `type="email"`.
- **On a PC the product offers the desktop app, not a browser install.**
  `canPromptInstall` is false whenever a desktop platform is detected, so the
  native-install path only exists under a phone user-agent.
- **Environment-dependent cases gate on the capability probe, never on a
  hostname.** `92-onlyoffice` reads `external.onlyoffice` and expects 503 when
  the integration is off and a 4xx when it is on — on a host with a Document
  Server it becomes a stronger assertion with no code change.

- **A `--port` you did not check can be somebody else's instance.** `filex serve`
  fails to bind when a port is taken and exits — but `/healthz` answers anyway,
  from the stranger, so the harness seeds and measures the wrong tree and
  reports a confident number about a build nobody asked about. It happened
  twice in one afternoon. `e2e/run.mjs` now refuses to continue unless the
  child it started reached its own listener without a bind error AND the
  instance reports zero storages (`assertOurOwnInstance`) — read the comment
  there before weakening it: two earlier versions of that check sampled the
  race instead of waiting for its verdict, and both let a stranger through.
- **A control character in a regex is invisible and always passes.**
  `14-explorer-sidenav` carried a literal 0x08 BACKSPACE where `\b` was meant,
  so its `not.match` could never fail — the regression guard for the reported
  `.shared` tab-strip defect was decoration for as long as it existed. eslint's
  `no-control-regex` had been reporting it. Scan for bytes < 0x09 and 0x0B–0x1F
  rather than reading the line.

## Credentials

None are committed, and none are needed for the default run: the harness's
throwaway admin is `admin@local` / `admin` and exists only inside a temp data
dir that is deleted afterwards. Pointing the suite at a real instance means
supplying `CYPRESS_ADMIN_EMAIL` / `CYPRESS_ADMIN_PASSWORD` yourself.

## CI

`.github/workflows/ci.yml` → the **Browser E2E (Cypress)** job. It builds the
packages, the admin UI, the embed assets and the binary, runs
`node e2e/run.mjs cypress --build`, and uploads screenshots plus video as
artifacts when anything fails. It **gates the build**: retries are off, there is
no network dependency and no secret, so a red run means this commit is broken.
