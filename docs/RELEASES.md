---
title: Releases
description: Every filex release with a plain-English summary of what changed — generated from the GitHub releases at release time.
---

<!-- GENERATED FILE — do not edit by hand. Your edits will be overwritten.
     Source:      GitHub Releases for BRF-Tech/filex
     Generator:   docs-site/scripts/fetch-releases.mjs
     Summaries:   docs-site/data/release-highlights.json (hand-written)
     Regenerate:  cd docs-site && npm run releases -->

# Releases

Every published filex release, newest first. This page is generated from the
[GitHub releases](https://github.com/BRF-Tech/filex/releases) by `npm run releases`, which is
run once when a release is cut — not by the site build, which would rewrite this
file on every contributor who ran it.

Whether filex installs a release by itself depends on which part of the version moved —
see [Updates](./UPDATES.md).

::: tip Latest — v0.38.0, 11 September 2026
Reported from the outside (#19): migration `00029` aborted the first boot of
:::

```bash
docker pull ghcr.io/brf-tech/filex:slim-v0.38.0
docker pull ghcr.io/brf-tech/filex:full-v0.38.0
```

## v0.38.0

<span class="filex-release-date">11 September 2026</span>

## What changed

### Upgrade notes

- ⚠ **PostgreSQL and MySQL installs: this is the release where they work.**
  Reported from the outside (#19): migration `00029` aborted the first boot of
  every PostgreSQL install, because `binary` is a reserved word there — and in
  MySQL, and only SQLite accepts it unquoted. That column was the first thing
  that broke, not the only one. Nothing in the repository had ever run against
  another engine, so every test stayed green while neither engine could finish
  a first boot, and on PostgreSQL the file-operation queue and the background
  job queue both failed silently on a server that reported itself healthy.

- ⚠ **Three columns were renamed** to get out of the way of reserved words:
  `plugins.binary` → `binary_path`, and `key` → `setting_key` / `meta_key` on
  `settings`, `node_meta` and `user_node_meta`. Migrations 00034–00036 do it on
  every engine; nothing in the API or the UI changes name. Existing SQLite and
  PostgreSQL installs are migrated in place on the next start.

- ⚠ **`FILEX_QUEUE_DRIVER` unset now follows the database** instead of meaning
  `sqlite`. If you were relying on the old default while running PostgreSQL,
  you were relying on a queue that logged a syntax error on every poll and ran
  nothing; it now runs. An explicit driver still wins.

- **MySQL/MariaDB minimum is now stated**: MySQL 8.0.13, MariaDB 10.5.2 — two
  migrations need `DEFAULT` on a `TEXT`/`JSON` column and `RENAME COLUMN`.
  filex also fills in `parseTime`, `loc=UTC` and `time_zone='+00:00'` when a
  MySQL DSN omits them; without the third, the server's clock and filex's
  disagree and a scheduled job becomes runnable hours early or late.

### Added

- **The document server gets its own address.**
  `FILEX_ONLYOFFICE_CALLBACK_URL`, and the matching field under the Document
  Server URL in *Settings → External services*, is the address the document
  server uses to reach filex. Empty — the default — keeps using the public URL,
  so installs where one address serves both are untouched. It exists because
  `FILEX_PUBLIC_URL` builds two different things, the share links people click
  and the document URL the document server fetches, and issue #17's reporter
  needed those to be different addresses. Applies live, no restart.

- **The third leg is measured, not disclaimed.** The Test button used to report
  two legs and say the document server's route back to filex could not be
  checked, "because filex has no way to make another container issue a request
  on demand". It can: the document server's conversion endpoint takes a URL and
  downloads it, so filex hands it a one-shot, unguessable URL of its own and
  watches for the request to arrive. The admin page now answers *the document
  server reached filex* or *it did not*, with the exact URL it was given. A
  server that rejects the signature, or that does not answer the conversion
  endpoint at all, is reported as **unmeasured** rather than as a broken route.

- **[docs/DATABASES.md](./DATABASES.md)** — which engine to pick, what each
  one needs, how the queue follows it, and exactly what "supported" is checked
  to mean.

- **CI runs against real PostgreSQL and MySQL service containers**
  (`test:go:engines`). The suites skip themselves without a DSN, so nobody has
  to keep two database servers running to work on filex.

### Fixed

- **PostgreSQL: the first boot.** `00029` used a reserved word as a column
  name (#19). Renamed on every engine.
- **MySQL: everything.** Five migrations wrapped many statements in one goose
  block, which reaches the server as a single multi-statement query and is
  refused — `00001` failed. Migration `00009` was missing from the dialect
  entirely, so `CreateStorage` died on "Unknown column replica_target_id".
  Seventeen columns were `NOT NULL` with no default where SQLite has one. Every
  upsert in the shared store was SQLite-only syntax; they are rewritten into
  `ON DUPLICATE KEY UPDATE` at query time rather than kept as a second copy.
- **PostgreSQL: copy, move and delete.** The `pending_ops` table was created at
  boot from hand-written SQLite DDL, so on PostgreSQL it never existed and every
  file operation failed on "relation pending_ops does not exist" while the
  server answered `/healthz` with 200. It is a migration now, and the queue's
  statements are rebound to `$1…$n` with the id read back through `RETURNING`.
- **PostgreSQL and MySQL: background jobs.** The queue driver defaulted to
  `sqlite` regardless of the database. It follows the database now, and the
  SQLite driver serves MySQL under its own name with UTC time expressions,
  `FOR UPDATE SKIP LOCKED` and a claim whose result is actually checked — four
  workers used to be handed the same op, three of which then failed the ack.
- **MySQL: a fresh install had no external services.** Seeding a service that
  has never been health-checked bound a zero timestamp, which MySQL rejects in
  strict mode, so OnlyOffice, drawio and the converter were missing from the
  settings page.
- **Documentation that had stopped being true**: `CONFIGURATION.md` still said
  MySQL was for "read-mostly use", `DEPLOYMENT.md` repeated it, and
  `MULTI-TENANCY.md` listed the postgres/mysql CI job as pending.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0380---2026-09-11)

- **Documentation** — &lt;https://docs.filex.sh>
- **Report a bug** — &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** — &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** — &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.38.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.38.0`

## v0.37.0

<span class="filex-release-date">7 September 2026</span>

A release about checks that were narrower than they looked. The External Services Test button probed only from the filex server, so a container-internal address answered it happily while the browser that has to load the editor could not resolve the name at all -- a green light meaning "the server can reach this", read by everyone as "this is configured". It now probes from the browser as well and reports the two legs separately, using the same load paths the editor itself uses so the answer is the real one. The shop-window gate that shipped yesterday stopped sampling six admin routes and started walking the whole table -- 359 of them, classified by asking the running server rather than by reading a list somebody has to maintain -- and found a hole on its first run: /metrics was registered for every method inside the admin group, so the demo guard refused none of them. And ninety published release pages that had been showing a commit hash now carry the changelog entry they always had.

## What changed

### Upgrade notes

- ⚠ **If OnlyOffice or drawio "tests fine" but fails when you open a file**,
  press Test again. It now probes from **your browser** as well as from the
  filex server and reports the two separately, because they answer different
  questions and only one of them was ever asked. A container-internal address
  like `http://onlyoffice` is reachable from filex and not from the browser
  that has to load the editor — the green light said "configured" and meant
  "the server can reach it". Reported from the outside, twice, by the same
  person before we saw it.

- ⚠ **`GET /metrics` was inside the admin group but registered with `r.Handle`**,
  so chi bound it to *every* method and the demo guard refused none of them.
  Read-only either way, so nothing was exposed that a GET did not already
  expose — closed so the rule has no exceptions left.

### Added

- **The Test button now probes from the browser too, and says which machine
  answered.** Issue #17's reporter came back: the v0.34.2 fix was real, and it
  did not close his problem. Three different machines have to reach three
  different addresses before the Office editor works — the **browser** loads
  the editor's JavaScript from the Document Server URL, the **filex process**
  polls the same URL, and the **Document Server** fetches the document and
  POSTs the save back to `FILEX_PUBLIC_URL` — and only the middle one was ever
  checked. So an operator on podman typed the container name
  `http://onlyoffice`, filex reached it, **Test went green**, and his browser
  could not resolve that name at all; the editor then failed with the same
  message as a missing configuration. The defect was never the probe. It was
  that the check was narrower than the badge implied — the same family as
  everything else fixed this week: a control that reads as verified and is not.

  - **Two probes, two results, one badge.** The admin page runs in the very
    browser that will open the editor, so it now tests that leg directly
    instead of disclaiming it. The result is reported as two separate
    sentences — *From the filex server: reachable* / *From this browser: not
    reachable* — plus a third line for the leg nobody can probe. When they
    disagree the page says what it means: a container-internal address filex
    can use and a browser cannot.
  - **The mechanism is the one the real viewer uses**, not a `fetch`. ⚠ A
    plain `fetch()` is blocked by CORS on a document server that is working
    perfectly, so a naive `catch` would report failure for a healthy service.
    OnlyOffice is probed with a `<script>` at
    `/web-apps/apps/api/documents/api.js` — load detection is not subject to
    CORS, and a successful load defines `window.DocsAPI`, which proves the
    thing that answered really is a Document Server. drawio is probed with a
    hidden `<iframe>` at `?embed=1&proto=json` and its `{"event":"init"}`
    handshake, the same one `DrawioViewer.vue` uses. Both were verified
    against **live** servers before being relied on. Each distinguishes
    *could not reach* from *reached, wrong thing* with a second, `no-cors`
    signal, and each is bounded by a timeout that reports as its own state.
  - **The badge no longer conflates "reachable" with "configured".**
    `Complete` is reserved for a service where both probes answered and
    nothing is warned about; a service the server can reach and the browser
    cannot reads `Server-reachable only`, in a different colour.
  - **The reverse path gets the only honest treatment available.** filex
    cannot make the Document Server issue a request on demand, so it does not
    claim a check it cannot perform: it warns on the shapes that certainly
    cannot work (a public URL of `localhost`, `127.0.0.1` or `0.0.0.0`) and
    notes the ones that are merely suspicious (a container-name public URL, a
    hostname filex itself cannot resolve). ⚠ A warning that fires on a working
    setup is worse than none, so every trigger is paired with a test that
    proves it stays silent on a setup that works — `https://office.example.com`,
    `http://192.168.1.10:8080`, and `http://localhost:8080` when filex itself
    is reached over localhost. That last suppression is the interesting one:
    a loopback document server is *correct* for somebody browsing from the
    same host, so it is never warned about there.
  - **Advisories ride on the list, not only on Test**, because the whole
    defect was a control that read as settled without anyone pressing
    anything. Opening the page is enough to see a browser-unreachable address.
  - **The three-address requirement moved to where the field is filled in** —
    the top of the admin page and the top of `docs/ONLYOFFICE.md`, with the
    two commands that tell an operator which half is wrong. It was already in
    the docs, around a prerequisites list further down the page, and he hit it
    anyway: a green Test outranks a prerequisites list.

  - ⚠ **Known limitation, deliberately not fixed here: filex has one public
    URL, and some setups need two.** `FILEX_PUBLIC_URL` is a single value that
    the OnlyOffice fetch/callback and every share link, invite mail, drop link,
    OIDC redirect and WebSocket ticket are all built from. An install whose
    Document Server can only reach filex by a container-network name
    (`http://filex:5212`), while its users need a browser-facing address,
    cannot express that today. The admin page now raises a **note** when it
    sees a container-name public URL rather than leaving it silent; splitting
    the value is tracked separately.

- **A release gate for the shop window — the surfaces a stranger touches
  before they trust us.** On 2026-09-07, hours before the public launch, a
  person looking at filex from outside found seven defects, and not one had
  been caught by a test, a lint, or any of the eleven steps of the release
  process. Several had been shipping for months: a dead `Issues` link on 104
  of the 105 published release pages, a public demo that answered all 101
  admin routes with no refusal, `GET /api/files/capabilities` handing
  anonymous callers the operator's internal hostname, a docs site built from
  the private tree, release bodies that were a commit hash, a headline
  `docker run` that put the reader's files in the database directory, and the
  demo's own advertised search returning nothing. The pattern is why this is
  a gate and not a checklist: **everything a stranger touches first is the
  least tested surface in the project.**

  It is split by what each check needs, because a check that cannot say
  *which* it is ends up either useless or dishonest:

  - **Offline, repo-only** — `web/tests/deploy/shopWindow.test.ts`, so it runs
    on every push, in `pnpm test`, and in CI, which already gates the tag. It
    covers the URL grammar (nothing pastes a GitLab route onto a GitHub host,
    and every GitLab route we link is one the export can *translate* — the
    export's own guard cannot see a route it drops the `/-/` from and gets
    wrong anyway), the headline `docker run` against the compose file it is a
    shorthand for, `site/` — which reaches filex.sh verbatim with no converter
    in the way — and the names the export does **not** rewrite, such as a bare
    IP address.
  - **Against a running instance** — `node scripts/check-shop-window.mjs
    --instance --boot bin/filex`. It boots a throwaway in demo mode on a pinned
    port with its own data directory, then proves a signed-in visitor is
    refused on the state-changing admin routes *and still gets the read-only
    ones*, that an anonymous capabilities call carries the feature flags and
    not the operator's host, and that the searches the demo advertises return
    the files they promise.
  - **Against the published product** — `--published`. Release bodies carry no
    `/-/` route and no bare commit hash where prose belongs, their links
    answer, and docs.filex.sh serves the released build with no private
    repository URL on it.

  Three exit codes, and the last two are the point: `0` passed, `1` **checked
  and wrong** — fail the release — and `2` **could not check** (no binary, no
  network, a GitHub rate limit, a fixture that is not set up). ⚠ A gate that
  turns an outage into a failed build is an outage of its own, so a network
  failure is never `1`; it is also never `0`, and the run names the check that
  did not happen. The same rule applies to a fixture: an instance with no
  external service configured would satisfy "the anonymous answer names no
  host" while leaking the moment an operator configured one, so that reports
  `skip`, not `ok`.

  Every check was proved against the defect it is for — the bug
  re-introduced, the check watched going red, then green: a `/-/` link, an
  unknown GitLab route, the old `docker run`, a production IP in a published
  file, the private repo on the landing page, the runbook publishing from the
  wrong tree, an advertised query nobody is shown, the redaction removed from
  `capabilities.go`, the demo guard removed from the router, an advertised
  query that answers nothing, and a fixture serving each published defect in
  turn. Release process step 12 in `docs/CONTRIBUTING.md` says what it does
  not cover.

- **The shop-window gate now walks the route table instead of sampling it, and
  covers four surfaces it had listed as gaps.** The gate above shipped with an
  honest account of what it did *not* reach; this closes what could be closed
  and says plainly what could not.

  - **Every route, not six of them.** The live check probes six admin routes,
    which proves the demo guard is installed and nothing more — a new operator
    surface at a *fourth* prefix would have passed, and that is exactly how the
    first hole appeared, because `/api/ai/admin` was the same admin panel behind
    a different front door. `backend/internal/api/shop_window_route_table_test.go`
    walks all 359 routes out of chi and classifies each one by **asking the
    running server**: a route an anonymous caller gets 401 on and a signed-in
    non-admin gets 403 on is an operator surface, one that answers both
    identically is not role-gated at all, and everything else is the product.
    Nothing in it names a route, so a new admin prefix goes red the day it is
    added with no list to update — and a second test measures the other
    direction, so the guard cannot be widened over the product to make the first
    one quiet. (Middleware would have been the obvious signal and chi cannot
    show it: `r.Group` bakes its chain into the handler before registration, and
    all 359 entries report the same four top-level middlewares.)
  - **`/metrics` was the fourth prefix**, found by that walk on its first run.
    It is mounted inside the admin-only group with `r.Handle`, so chi registers
    it for every method, and a public demo refused none of them. The exposition
    is read-only, so nothing was ever going to break — it is guarded now because
    that lets the walk state its rule with no exceptions at all.
    `GET`/`HEAD`/`OPTIONS` still pass: a Prometheus scrape job is untouched.
  - **All three external services are proved, not one.** The redaction covers
    OnlyOffice, drawio and the converter through one loop *and* three flat
    aliases blanked by name, so a single seeded host exercised the loop and left
    two aliases unproved. The booted instance now carries a distinct sentinel
    per service, and the anonymous payload is additionally asserted to carry
    **no `url` key anywhere** under `external` — the only form that reaches a
    service nobody has added yet (`mermaid` is already there, with no
    environment variable and no alias).
  - **The demo's own corpus, asked of the demo.** The instance check seeds the
    file it then searches for, so it proves the query grammar and not what
    demo.filex.sh holds — and the corpus is where the defect was. `--published`
    now signs in to the live demo with the credentials the demo itself
    publishes, and types the queries the splash advertises. Unreachable is a
    `2`, never a false green.
  - **GitHub's About box, and filex.sh.** Neither is code, so neither was ever
    checked. The About blurb had no source of truth at all — it is typed into a
    settings form and lived only in GitHub's database — so `REPO_ABOUT` in
    `scripts/shop-window-data.mjs` is now that source: asserted offline to fit
    GitHub's 350 characters, to **name every driver the backend registers**
    (measured from the `storage.Register` calls, and the blurb that was
    published when this was written still said five after SMB shipped), and to
    punctuate the way every other surface does. `--published` compares it with
    the live value and prints the exact line to paste, GitHub having no deploy
    step for it. The front page is checked for the hosts it has to link —
    docs.filex.sh above all, which `site/index.html` links in three places and
    the deployed page did not link at all.
  - **Screenshot staleness, with its limits written down.** A picture committed
    before the last change to the code that draws it cannot be showing that
    change. `SCREENSHOTS` declares what each README picture depicts — the one
    thing here a person has to know — and the offline half asserts every
    declared path still exists, so a renamed component cannot leave a picture
    looking fresh for ever. The threshold is measured, not chosen:
    `admin-plugins.png` shipped **six** releases stale, so six released versions
    of unfollowed change is the line, and lesser drift is reported and passes.
    ⚠ What it cannot see is whether a picture is actually *wrong*: it compares
    commit dates, so a comment counts and a theme change does not. Looking is
    still release step 2.

### Fixed

- **Two release-gate suites could report success while running no tests.**
  `describe.skipIf` skips every assertion inside it — including the "this list
  is not empty" guards written to stop those blocks passing vacuously, which had
  been placed inside the very blocks they guard. Measured across the suite: in
  the **published** tree, which CI runs, `siteAssets.test.ts` reported 0 of its
  8 tests and `shopWindow.test.ts` 8 of its 17, both green. Both files now
  assert the shape of the checkout *unconditionally*, and as one fact — the
  export withholds `site/` and `scripts/export-public.sh` together, so a tree
  holding exactly one of them is neither the source nor the published product,
  and says so instead of skipping in silence.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0370---2026-09-07)

- **Documentation** — &lt;https://docs.filex.sh>
- **Report a bug** — &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** — &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** — &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.37.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.37.0`

## v0.36.0

<span class="filex-release-date">7 September 2026</span>

The release that came out of looking at filex the way a stranger arriving from a link would. The public demo was the worst of it: the demo account is an administrator and the guard covered two things, so walking the real route table found all 101 admin routes answering and not one refusing -- resetting the shared password, deleting users, repointing a storage, making the server connect wherever a visitor pointed it. Writes are refused now, reads are not, because showing the operator surfaces is the point of a demo. The capabilities endpoint, which is public on purpose so embedders can probe it, was handing anonymous callers the operator's internal hostname; it still says whether a service is configured and no longer says where. Every published release page carried a dead "Issues" link -- 104 of 105 of them -- because the export translated the host but not GitLab's URL grammar; the pages are corrected and the export refuses to produce another. Release notes were a commit hash where the changelog had real prose. The README's headline command dropped the reader into an empty file manager with their files in the database directory. And the container can finally drop root: PUID and PGID, with the upgrade from a root-owned data directory measured rather than hoped for.

## What changed

### Upgrade notes

- ⚠⚠ **Running a public demo (`FILEX_DEMO_MODE`)? Upgrade before you link it
  anywhere.** The demo account is an administrator, and the guard covered
  storage creation and plugins only. Measured by walking the real route table:
  **all 101 routes under `/api/admin` answered, and not one returned 403** —
  resetting the shared account's password, deleting users, repointing an
  existing storage, making the server connect wherever a visitor pointed
  `smtp-test`, applying an update. `/api/ai/admin/*` is the same surface behind
  a token and was reachable the same way, proven end to end. Writes are refused
  now; reads are not, because showing the operator surfaces is what a demo is
  for.

- ⚠ **`GET /api/files/capabilities` no longer returns service URLs to
  anonymous callers.** It still says *whether* OnlyOffice, drawio and the
  converter are configured — that is what embedders probe it for — but not
  *where* they live, because it was handing the operator's internal hostname to
  anyone with curl. Signed-in responses are unchanged. **If you read
  `onlyoffice_url`, `drawio_url` or `convert_url` from an unauthenticated
  call**, send credentials; the first-party consumers all already do.

- **The container can drop root.** Set `PUID`/`PGID` (or `user:` /
  `runAsUser`) and filex runs as that user, chowning `/data` once. Nothing
  changes without them: no variable means root, exactly as before, and the
  upgrade path from a root-owned data directory was measured — the database
  survives byte-identical and `rm -rf ./data` stops needing `sudo`.

### Fixed

- **docs.filex.sh was built from the private tree, so it published an import
  path that does not compile.** `docs/PLUGINS.md` tells a plugin author to
  import `github.com/brf-tech/filex/backend/pkg/pluginsdk`; the published
  module is `github.com/brf-tech/filex/backend`. Anyone following the plugin
  guide copied a path that names a repository they cannot reach and does not
  build. `CONTRIBUTING` carried two more of the same. The release step now
  pushes the site's prose from the **export**, which is where every other
  public artifact comes from.

  It also explains a second symptom: the site had been serving a v0.33.0 build,
  so `/REALTIME` was a 404 while the README's first paragraph advertises
  real-time collaboration and nine pages link to it, `/CONFIGURATION` never
  mentioned ClamAV (nineteen times in the repo), and `/PROTECTION` still said
  WebDAV and AI/MCP writes were "not yet" scanned — a security page describing
  a gap that had been closed.

### Added

- **`PUID` / `PGID`.** The image installed `su-exec` and created a `filex`
  account, then ran everything as root and used neither — so `/data` came back
  owned by `root:root` and deleting one's own data directory needed `sudo`.
  There is now a `docker/entrypoint.sh`: set `PUID`/`PGID` and it takes
  ownership of the data directory once, records what it chowned to in a
  `.filex-uid` marker so later boots skip the walk, and drops privilege.

  ⚠ The default is unchanged — **still root**. An existing install whose data
  directory is full of root-owned files must keep working on upgrade, so
  opting in is the operator's decision. `docker run --user` / compose `user:`
  / Kubernetes `runAsUser` are detected and left alone (nothing to drop, and
  no permission to chown); `PUID` alongside them says so in the log rather
  than pretending. Measured on the published image: an old root-owned data
  directory, then `PUID=1000`, gives a 200 on `/healthz`, a byte-identical
  `installation.json`, and a `rm -rf ./data` that no longer needs `sudo`.

  ⚠ Only the DATA directory is chowned. Storage roots — a bind mount, an NFS
  or SMB share — are left as they are; they may be shared with other software
  and re-owning them is not a container's decision.

### Fixed

- **Every GitHub release page ended with two dead links, one of them
  "Issues".** `scripts/export-public.sh` rewrote the GitLab host to
  `github.com` but not the URL *grammar*: GitLab's `/-/` route separator
  arrived verbatim, so `https://github.com/…/filex/-/issues` — the link a
  reader clicks to report a bug — answered **404**. Measured 2026-09-07:
  **104 of the 105 published releases** carried it, not the recent few. The
  export now translates the route shape (and renames `merge_requests` →
  `pulls`, `pipelines` → `actions`), refuses to publish a tree where a
  `github.com/…/-/…` survives, and the release footer points at
  &lt;https://docs.filex.sh> instead of raw markdown. All 105 published bodies
  were corrected.

- **Release notes said nothing.** GoReleaser builds the body from `git log`,
  and the config filters drop `docs:`, `test:`, `chore:` and `ci:` — so the
  published v0.34.2 page was, in full, a heading and one commit hash, while
  `CHANGELOG.md` carried 1,445 characters of prose for that same version. The
  release workflow now derives the body from `CHANGELOG.md` (extending
  `scripts/release-notes.mjs`, which already did this for the Umbrel store)
  and hands it to `goreleaser --release-notes`. A version with no changelog
  section fails the run **before** anything is published. ⚠ Capped at 20,000
  characters, cut at a group or bullet boundary, with a link to the full
  entry: measured on goreleaser v2.17.1, a body over 125,000 characters is
  truncated *silently*, and what it cuts is the footer.

- **The README's headline command left the reader in an empty file manager.**
  `-v $(pwd)/data:/data` mounts filex's own state directory — database, search
  index, thumbnail cache — so files dropped into `./data` were invisible and
  the UI said *"No storage configured"*. The command now seeds a local storage
  from `$PWD` and keeps `/data` in a named volume, which is also what
  `docs/INSTALLATION.md` told people to run.

### Changed

- **filex.sh links to the documentation site.** 18 anchors, and
  `docs.filex.sh` appeared in none of them: the nav "Docs" and the footer
  "Documentation" both pointed at raw GitHub markdown while a 38-page
  VitePress site sat unlinked.

### Security

- ⚠⚠ **A public demo no longer hands every visitor a working admin panel.**
  `FILEX_DEMO_MODE` publishes the credentials on purpose, which makes
  "admin-only" mean "public" on that instance — and until now the only thing
  refused was *adding a storage*. Measured on a local demo: all **101** admin
  routes answered, none with a 403, including
  `POST /api/admin/users/{id}/reset-password`,
  `DELETE /api/admin/users/{id}`, `PATCH /api/admin/settings`,
  `PATCH /api/admin/storages/{id}` (repointing an existing storage — the
  create-side guard never covered it), `POST /api/admin/webhooks` and
  `POST /api/admin/update/apply`. One visitor resetting the shared password
  locked out every other reader until the nightly restore.

  Every state-changing method under `/api/admin/…` and `/api/ai/admin/…` is
  now refused with 403 on a demo, as are changes to the shared account itself
  (`/api/auth/password`, `/api/auth/profile`, `/api/auth/totp/…`). **Reads are
  untouched** — a demo exists to show the operator surfaces — and so is every
  ordinary install, where the middleware is a pass-through. Full list:
  [docs/DEMO.md](./DEMO.md).

  ⚠ `/api/ai/admin/…` is the same admin surface behind an admin-scoped API
  token, and a visitor could mint one at `POST /api/admin/ai-tokens` (201) and
  then drive `PATCH /api/ai/admin/settings` (200) with it. Guarding one mount
  point without the other would only have moved the door.

- ⚠⚠ **`/api/capabilities` no longer tells anonymous callers where the
  operator's services live.** The endpoint is public by design (embedders probe
  it before logging in) and it published `external.<service>.url` plus the flat
  `onlyoffice_url` / `drawio_url` / `convert_url` to anybody who asked —
  measured on demo.filex.sh: `"url": "https://docs.example.com"`, with no
  credential. This affected **every install**, not just demos. Anonymous
  callers now get `enabled` and `state` and no host; authenticated callers see
  the payload unchanged, because the draw.io iframe and the convert modal need
  a real address. OnlyOffice's document-server URL was never needed here — the
  browser gets it from the authenticated `POST /api/files/onlyoffice/config`.

- On a demo, the audit log and the dashboard's `recent_activity` no longer
  print client IP addresses: with published credentials, one visitor's address
  is readable by the next. Ordinary installs still show them.

- On a demo, `GET /api/admin/auth-providers` no longer returns credentials in
  clear. Its `config_redacted` block masks by leaf NAME, and the admin UI saves
  a provider's whole config as one leaf called `config`, so an OIDC
  `client_secret` went out in full under a field named "redacted".

### Fixed

- The demo advertised two searches that returned nothing. The login splash
  promised `"invoice 2026" finds invoice_2026.pdf` and the search box suggested
  `tag:report`; on the demo corpus both answer 0 results — there is no file
  with "invoice" in its name and no tags at all — while `mian.go` and
  `package main` work. The examples now name files the demo actually holds, and
  a test pins them to the recorded corpus so a suggestion that stops being true
  fails a build instead of greeting visitors.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0360---2026-09-07)

- **Documentation** — &lt;https://docs.filex.sh>
- **Report a bug** — &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** — &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** — &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.36.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.36.0`

## v0.35.0

<span class="filex-release-date">7 September 2026</span>

A security release. On a multi-tenant install the tenant boundary was enforced on the routes that list rows and assumed on the routes that name one: an administrator of any tenant, pointed at another tenant's ids, reached fifteen of the sixteen such routes. Resetting a password returned the other customer's new password in the response body; reading a storage returned its S3 or SFTP secret; minting an API token bound it to somebody else's user. The file API was worse, because it serves bytes rather than JSON. Separately, and on every install rather than only multi-tenant ones, thumbnails were readable without logging in — a rendered preview of any document to anyone who could guess a node id — so thumbnail URLs now carry a short-lived signature that an &lt;img> tag can use and an anonymous request cannot forge. Emptying the trash for one storage emptied all of them, permanently, while the dialog promised otherwise. Restoring a version required no permission at all, so a read-only account could overwrite live files. Directory logins provisioned accounts into the tenant that sees every other one. And a family of controls that saved and were never read is gone or wired up: five admin settings that wrote rows nothing consulted, a demo password that broke the demo login it claimed to secure, and a storage sync mode that accepted typos and silently polled.

## What changed

### Upgrade notes

- ⚠⚠ **Running `FILEX_MULTI_TENANT`? Upgrade.** The tenant boundary was enforced
  on the routes that *list* rows and assumed on the routes that *name* one. An
  administrator of any tenant, pointed at another tenant's ids, reached fifteen
  of the sixteen such routes — including `POST /api/admin/users/{id}/reset-password`,
  which answered 200 and returned the other customer's **new password in the
  response body**, and `GET /api/admin/storages/{id}`, which returned the storage
  config: for an S3 or SFTP storage, the access key and secret. `/api/files` was
  worse still, because it serves bytes. All of it is closed. See **Security** below.

- ⚠⚠ **Everyone: thumbnails were readable without logging in.** `GET /api/files/thumb/{id}`
  sat outside every auth group and returned a rendered preview — the readable
  content of a document — to anyone who could guess a node id. This is not
  specific to multi-tenant installs. Thumbnail URLs now carry a short-lived
  signature, and an authenticated caller is checked. **If you build thumbnail
  URLs yourself**, take them from the listing rather than constructing them;
  a hand-built URL without the stamp is now a 401.

- ⚠ **Emptying the trash for one storage used to empty all of them.** If you
  have used *Empty trash* with a storage selected on an earlier version, it
  purged every storage's trash, permanently, while the dialog said otherwise.

- ⚠ **Directory logins now refuse rather than mis-home.** On a multi-tenant
  install, LDAP and proxy-header logins used to provision accounts into the
  supertenant, which sees every tenant. They now inherit the tenant from the
  request's host; where there is no host to read (LDAP over SFTP/FTPS/NFS) the
  login is **refused** unless you set `auth.ldap.provider` / `FILEX_LDAP_PROVIDER`,
  because the only alternative was the supertenant. Existing mis-homed accounts
  are **not** moved automatically — the boot log now names them and points at
  `PATCH /api/admin/users/{id}`.

- ⚠ **Version restore now requires `editor`.** It required nothing before: a
  read-only user could overwrite live file content with an old version.

- **Five admin Settings controls were removed** (`public_url`, `sync_interval_seconds`,
  `log_level`, `default_locale`, `default_timezone`). They wrote rows that
  nothing read; the environment variables that do work are named in their place.

- **`storage.sync_mode` is validated now.** An unsupported value is rejected
  instead of stored and silently polled. Rows that already carry `push` keep
  working (they poll, and say so once in the log); only a *change* to an
  unsupported mode is refused.

### Security

- **`GET /api/files/thumb/{id}` served rendered previews to anyone who could
  count.** The route sat outside every auth group, `checkSig` returned true when
  the `sig` parameter was **absent**, `manager.go` emitted `thumb_url` with no
  signature so no deployment was ever on the signed path, and nothing in the
  codebase ever wrote `settings.thumb_signing_key` — so the `key == ""` branch
  waved a supplied signature through too. Measured: an anonymous `curl` with no
  cookie and no token got **200, `image/jpeg` and the full body**, and
  `?sig=deadbeef` got the same, on a server where anonymous
  `GET /api/files/quota/me` correctly answered 401. Node ids are dense integers,
  so that was "walk the range and collect the rendered first page of every file
  on the instance" — and it was **not** a tenancy bug: a single-tenant install
  leaked exactly as much. This is the most widely exposed of the three fixed
  here.

  ⚠ The fix could not simply be "require auth". Thumbnails render into
  `<img src>`, which sends no `Authorization` header, and filex's session cookie
  is `SameSite=Lax`, so an `<img>` inside a third-party embed sends no cookie
  either — a blunt auth requirement would have closed the hole and blanked every
  embedded explorer in production, including a customer's. The endpoint now takes
  **one of two proofs**: a short-lived stamp on the URL (`?exp=&sig=`, an
  HMAC-SHA256 over node id + expiry under a key that is now actually generated),
  which the listing puts on every `thumb_url` it emits and which an `<img>` can
  carry; or an authenticated caller who clears the node's tenancy scope, the
  token's `root:` confinement and an ACL check at viewer level. Neither → 401.

  All four consumers were re-measured after the change: the admin SPA (cookie),
  the desktop app (bearer), an `<img>` in an embedded `@brftech/filex` explorer
  (the stamp, no credentials at all) and the public share page — which never used
  this endpoint and still does not, since it serves the same artefact through
  `/s/{token}/f/<path>?thumb=1`, scoped to the share token. A root-confined embed
  token now also stops at its own subtree: previously it could read previews of
  any node id on the instance, because a node id is not a path and
  `confine.Middleware` had nothing to rewrite.

  `FILEX_THUMBS_URL_TTL` (default `24h`, matching the endpoint's `Cache-Control`)
  bounds the stamp; the expiry is quantized to the hour so the URL string — the
  cache key for both the browser and `useThumbs` — stays stable within a window
  instead of forcing a re-download on every listing.

- **LDAP and proxy-header logins provisioned into the supertenant.**
  `db.Store.CreateUser` hard-codes `provider_id` to the `default` provider, and
  `default` is seeded `is_supertenant = 1` — which `CanAccessStorage` treats as
  confine-**exempt**. Measured on a two-tenant install: a header-trust login
  arriving on `diyetlif.local` created the account with `provider_id = 1`, and
  that account's own storage listing came back as **both** tenants' storages. The
  account was not mis-filed, it was privileged.

  A login has no caller whose tenant could be inherited — the account being
  created *is* the caller — so the signal is the request **Host**, the same one
  `multioidc.Dispatcher` already uses to pick a realm. `handlers.Auth.Login`
  stamps it onto the context (`auth.LoginDriver.Login` takes only a `ctx`, so a
  driver in the chain cannot see the request), the proxy-header driver stamps its
  own, and `auth.ProvisionUser` homes the new row — deleting it if homing fails,
  as the invite path already did.

  ⚠ The protocol logins (SFTP/FTPS/NFS, through `internal/protocolauth`) present
  a password on a socket and have **no Host at all**. There, on a multi-tenant
  install, the driver now **refuses to create** rather than falling back: the
  only fallback available is the supertenant, so "provision anyway" is the bug
  rather than a convenience. An account that already exists signs in over every
  protocol exactly as before. New `auth.ldap.provider` / `FILEX_LDAP_PROVIDER`
  (and `auth.header_proxy.provider` / `FILEX_HEADER_PROVIDER`, plus Helm
  `auth.ldap.provider` and a `auth.headerProxy` block that did not exist) let an
  operator name the tenant explicitly when they want it.

  ⚠ **Rows an older build already stranded are not migrated.** Nothing records
  which driver created a user, and the break-glass `admin@local` is deliberately
  in the supertenant, so a blanket re-home would take an operator's own account
  away from them. Instead a multi-tenant install with either driver enabled now
  logs **one WARN at boot** naming every non-admin account homed in the
  supertenant; re-homing stays `PATCH /api/admin/users/{id}` with `provider_id`.

- **`/api/files/versions` had no ownership or ACL check on any install.** The
  `Versions` handler had no `ACL *acl.Resolver` field at all — the only file
  surface in `routes.go` with no `AttachACL` call — and `versioning.Service`
  verifies only that a version belongs to the node it names before overwriting
  live bytes. Measured on a **single-tenant** install with a `viewer`-role
  account: `POST /api/files/versions/restore` answered 200 and `contract.txt` on
  disk went from its current content to the old version's, and
  `POST /api/files/versions/snapshot` answered 200 and wrote a version row. A
  read-only account could roll back, and force snapshots of, any file whose node
  id it could name.

  Every route in the file now resolves the node first and applies four gates —
  existence, tenancy, `root:` confinement, RBAC. Restore and snapshot require
  **editor**; listing the timeline stays at **viewer**, because somebody who can
  read the file is not being told its history is a secret. The admin hard-delete
  gains the same check (redundant behind `RequireAdmin` today, deliberately, so
  that un-admin-ing the route cannot silently drop authorization). The first
  three gates answer 404, identical to a node that never existed; only the RBAC
  refusal is 403.

  ⚠ One behaviour change beyond the permission gate: a **trashed** node is no
  longer reachable through these routes (404). A trashed row's live path is its
  `storage_key` — where restore would put it back — so restoring a version onto
  one wrote bytes to a path the catalogue says holds nothing. It is the same
  exclusion the comments handler already makes.

  ⚠ Restore is a destructive **write** and was the one write surface in filex
  that went through neither half of `writehook`. It now calls
  `BeforeOverwrite` first — so the bytes it replaces are snapshotted, and a
  snapshot that fails answers 503 `SNAPSHOT_FAILED` instead of destroying them —
  and `OnFileWritten` after, which is what finally makes a restore emit
  `file.updated` to webhook subscribers. `snapshot_current` now does work only
  when that guard is switched off, so the same bytes are never recorded twice.

### Fixed

- **A user could mute a notification event and keep receiving it.**
  `muted_events` and `in_app_enabled` round-tripped through
  `GET`/`PATCH /api/notifications/settings` from the day they were added and
  **no read path applied either of them**: the history query filtered on the
  user and the read flag only, so a muted event still appeared in the list and
  still counted towards the unread badge. A preference that saves and does
  nothing is worse than an absent one — the user believes the noise is handled.

  Both now gate the **read**: `in_app_enabled: false` empties the bell,
  `muted_events` drops those event ids from the list *and* the unread count.
  What deliberately did **not** change is as important: `Send` still records
  every event (the audit is not a preference), webhook delivery is untouched
  (it is global), and the admin/global view is never filtered by one user's
  mutes. The filter is applied in SQL rather than to the returned page, so a
  filtered page comes back full and its total counts what the caller will
  actually show. An unreadable settings row **fails open** — a display
  preference must not be able to hide an antivirus hit behind a DB hiccup.

  `docs/NOTIFICATIONS.md` said so in one place ("nothing applies them yet") and
  contradicted itself in another, prescribing them under *Too many
  notifications* as a live remedy. Both are now true.

- **`FILEX_AUTH_DRIVERS=proxy_header` enabled nothing.** The loader matched
  `proxy-header` with a hyphen; `docs/CONFIGURATION.md` (twice),
  `docs/ARCHITECTURE.md` and the Helm chart all told people to write the
  underscore. Following the documentation produced one
  `unknown auth driver` log line and an install with reverse-proxy SSO that
  reads as configured and silently is not. Driver names now fold `_` to `-`.

- **The release pipeline could publish the 510 MB image as `:slim`.** The
  default build was tagged `slim`/`slim-vX.Y.Z` and those tags were overwritten
  by the slim build that followed — which is only true when the second build
  runs. The job is `allow_failure` (the dind runner is unreliable) and pushed
  with `--all-tags`, which is not selective. The full build no longer claims
  those tags, and the push names the tags it built.

- Helm `Chart.yaml` credited `scripts/sync-chart-version.mjs` for keeping
  `appVersion` current. No such file exists; the script is
  `scripts/sync-deploy-versions.mjs`.

- The storage badge labelled a storage with no `sync_mode` as **On demand**,
  while the backend defaults an unset mode to **poll** — the opposite of what
  was running.

- **`FILEX_DEMO_PASS` broke the demo button it was supposed to configure.**
  The loader parsed it, `docs/CONFIGURATION.md` listed it, and no code read
  `config.Demo.Pass` — while the demo landing's only button submitted the
  literal string `demo` and the credentials hint under it printed the same
  literal. An operator who set the variable therefore *broke* the demo login
  while believing they had secured it, and the page went on advertising a
  password that no longer worked.

  The password now travels the same road the demo user already did: the
  capabilities response carries it and the CTA submits what it is given.
  ⚠ It is published **only when demo mode is on** — where the page prints the
  credentials next to the button anyway, which is the entire point of a demo —
  and a normal install returns nothing, whatever `FILEX_DEMO_PASS` says. Both
  fallbacks stay `demo`, so an install that never set the variable (including
  the public demo, whose account password is `demo` in the database) behaves
  exactly as before. The documentation now also says the thing that was
  missing: neither variable creates or changes the account.

- **`sync_mode` was never validated, and `push` was a mode with nothing behind
  it.** Any string the admin API decoded was persisted, and the sync worker's
  switch has no branch for it, so `fsnotifiy` stored happily and the storage
  ran the poll loop while the page displayed the typo back. `push` was the same
  defect wearing a nicer costume: a declared enum member, no receiver, silently
  polling.

  The gate lives in the **store**, not in one handler, because three writers
  reach that column — the admin API, the config seed and the CLI — and the
  message names the modes that exist. `push` is rejected as unimplemented, with
  a message pointing at `ondemand` + `POST /api/admin/storages/{id}/sync`,
  which is what an external writer actually wants.

  What happens to rows that already say `push`: **nothing is rewritten.** They
  keep polling as they always did, they stay editable — an unrelated rename or
  disable still saves, since refusing it would strand an operator with a row
  they cannot fix — and only a *change* to an unsupported mode is refused. The
  worker now logs `sync: unsupported sync_mode, falling back to poll` once per
  storage at startup, so the discrepancy is visible instead of silent, and the
  storages list labels such a row **Unsupported (polling)** instead of the raw
  i18n key. ⚠ The rejection currently surfaces as HTTP 500 with the message in
  the body; mapping it to 400 belongs in the storages handler.

- **Two load-bearing environment variables were undocumented.**
  `FILEX_TESSERACT_BIN` decides whether images are OCR'd into the content
  index — and when set it is *authoritative*, so a wrong path turns OCR off
  rather than falling back to `$PATH`, which is exactly the kind of thing an
  operator must be told before they debug an empty index. `FILEX_UPDATE_TARGET`
  is the one variable in `docs/CONFIGURATION.md` that filex **sets** rather than
  reads: it is exported into `FILEX_UPDATE_PRE_COMMAND`'s environment with the
  version about to be installed, so a backup command can name its dump after it.
  Both are now in `docs/CONFIGURATION.md`, with what happens when they are unset.

- **Helm `nameOverride` was rendered by every template and declared nowhere.**
  `_helpers.tpl` has always read `.Values.nameOverride`, so it worked — for
  anyone who read the templates. A value you can only discover by reading the
  chart's internals is not configurable in practice; it is now declared in
  `values.yaml` with what it changes (`helm template rel ./filex --set
  nameOverride=custom` → `rel-custom-*`).

- **Five of the six controls on the admin Settings page did nothing.**
  `public_url`, `sync_interval_seconds`, `log_level`, `default_locale` and
  `default_timezone` were PATCHed, written to the `settings` table, echoed back
  and re-rendered in the form — and no code on the server ever read those rows.
  The live values come from `FILEX_PUBLIC_URL`, `FILEX_SYNC_INTERVAL`,
  `FILEX_LOG_LEVEL` and `FILEX_DEFAULT_LOCALE`; there is no timezone knob at
  all. `site_name`, the one key with a reader (share-invite mail), sat on the
  same form and made the page look trustworthy, and the hints promised
  specifics — *"Used for share links and email templates"* under a field that
  changed no link.

  The five controls are gone, replaced by a line naming the variables that do
  work. Nothing is migrated: the stale rows are harmless and were never read.

- **A failed sync was painted the same grey as one that never ran.** The
  backend writes `"failed"` (`internal/sync/poll.go`); both `syncTone` copies —
  Storages and Dashboard — matched `'error'`, the spelling the *sync-runs* list
  translates to. So the badge text said "failed" while the dot, which is what
  an operator actually scans a list for, said "nothing to see". Both spellings
  are now accepted, and there is one copy of the mapping
  (`web/src/lib/syncTone.ts`) instead of two that drifted identically.

- **The dashboard's storage cards always read "0 B · 0 files".** They render
  the same rows as the storages list, which reads `stats.total_size_bytes`
  with the flat legacy field as a fallback; the dashboard read only the flat
  field, which the endpoint stopped filling when the nested `stats` object
  arrived.

- **The archive preview ignored `apiBase`.** `ArchiveViewer` hardcoded
  `fetch('/api/files/archive/list')` while every other call in
  `@brftech/filex-core` goes through the configured endpoints — so in the
  package's headline use case, an embed on a host page pointed at
  `https://files.example.com`, the zip preview posted to the *host page's*
  origin and 404'd. It now takes the endpoint from the explorer's config and
  falls back to the same-origin path.

- **`showInfoPanel` was documented public API that nothing read.** An embedder
  who set it false got the inspector toggle anyway. It now hides the toggle, as
  documented; default (and every existing embed) unchanged.

- **The SMB driver had no translations.** Seven i18n keys its descriptor names
  (`storages.driver.smb`, `fields.share`, `fields.domain`,
  `fields.dialTimeout`, `fieldHelp.smb{Share,Domain,Root}`) existed in neither
  catalogue, so SMB was the one driver whose form rendered in English inside
  the Turkish UI.

**This release has more to it than fits on one page.** The rest of the
entry — and every earlier release — is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0350---2026-09-07).

- **Documentation** — &lt;https://docs.filex.sh>
- **Report a bug** — &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** — &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** — &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.35.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.35.0`

## v0.34.2

<span class="filex-release-date">6 September 2026</span>

Two things that only ever bit installs nobody had configured by hand. filex guesses http://localhost:5212 as its own address when FILEX_PUBLIC_URL is unset, and the realtime ticket handed that guess to the browser as fact -- so on any other port the socket connection was refused and the client fell back to twelve-second polling with nothing logged anywhere. It reads as "live updates do not work" while the server is entirely healthy. The socket address now follows the request when no public URL was chosen, which is safe there and nowhere else: that response goes back to the client that asked for it, while a share link is mailed to somebody else and still uses the configured origin. The startup banner had the same fault and printed a URL nothing was serving. Also two browser tests that asserted Turkish sentences and went red when English became the fallback language, without either feature having changed.

## What changed

### Fixed

- **An instance that was never told its public URL sent every browser to
  `localhost:5212`.** The realtime ticket built `ws_url` from `PublicURL`,
  whose default is a guess — so filex on any other port advertised a socket
  address nothing was listening on, the connection was refused, and the client
  dropped to 12-second polling **with nothing logged**. It reads as "live
  updates do not work" while the server is perfectly healthy, and it is the
  default state of any install that has not set `FILEX_PUBLIC_URL`.

  With no public URL configured the socket address now comes from the request,
  which is safe *here and only here*: the ticket goes back to the client that
  asked for it, so a forged `Host` can mislead nobody but its sender. Share
  links are mailed to third parties and still use the configured origin — a
  test pins that difference.

  The startup banner had the same fault, printing the guess under "Listening
  on". It prints the real listen address, and says so, when no public URL was
  chosen.

- Two browser tests asserted **translated strings** and so failed once English
  became the fallback language in v0.33.0, with nothing about the features
  having changed: `82-capability-gating` looked for the Turkish OnlyOffice
  message (now a stable selector), and `17-theme-locale` was fixed in 0.34.1.
  A test that fails when the UI is translated is testing the translation.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0342---2026-09-06)

- **Documentation** — &lt;https://docs.filex.sh>
- **Report a bug** — &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** — &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** — &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.34.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.34.2`

## v0.34.1

<span class="filex-release-date">6 September 2026</span>

A patch for two things v0.34.0 shipped over. A user who had chosen Turkish saw an English admin panel on any second device: the language stored on the account was written by Profile and never read, so it only ever worked in the browser you saved it in. It had been invisible while Turkish was the fallback for everybody, and surfaced when v0.33.0 correctly made that fallback English. And the release could not be stopped by a failing test suite -- CI ran on the branch push while the release ran on the tag two seconds later, in parallel and unaware of each other, with no required check anywhere. CI had been red since v0.31.0 and four releases went out over it, carrying exactly that locale bug. Nothing publishes now until the suite is green.

## What changed

### Fixed

- **A user who had chosen Turkish saw an English admin panel on any second
  device.** The language on the account (`users.locale`) was written by
  Profile → Save and then **never read**. Saving also set it in this browser's
  `localStorage`, so it looked right on the machine you saved it on; sign in
  from another browser, another device or a private window and the account's
  choice was ignored and the browser's language used instead.

  It was invisible while `tr` was the fallback for everybody — a Turkish user
  got Turkish either way, from the default rather than from their preference.
  v0.33.0 made the fallback `en`, which was the right change, and this
  never-read preference surfaced behind it.

  The account's language is applied wherever the user is loaded, ranked below a
  choice made on this device (the language switcher writes only locally, so it
  is the newer decision) and above the instance default.

- **A red CI suite could not stop a release, and had not.** `release.yml` now
  calls `ci.yml` as its first job and nothing publishes until it is green.

  Until now CI ran on the branch push and the release workflow on the tag
  pushed two seconds later — in parallel, unaware of each other, with no
  required status check anywhere in the repository. Measured 2026-09-06: **CI
  had been red since v0.31.0 and four tags shipped over it**, carrying the
  locale bug above. None of the release steps would have caught it: they check
  the README, the screenshots, the links, the anchors and the version
  manifests, and never run a test.

- `17-theme-locale.cy.ts` asserted the account's language while measuring the
  **browser's**: with no stored choice the app fell back to browser detection,
  so it passed on a Turkish workstation and failed on an English CI runner. It
  now pins the browser to English, so only the saved preference can satisfy it.

- `siteAssets.test.ts` failed on every public CI run: `site/` is withheld from
  the published tree, so the directory it compares does not exist there. It
  skips where the directory is absent and still gates where it is present. The
  failure also cascaded — the image-build job `needs` it, so v0.34.0's images
  were never verified on main.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0341---2026-09-06)

- **Documentation** — &lt;https://docs.filex.sh>
- **Report a bug** — &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** — &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** — &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.34.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.34.1`

## v0.34.0

<span class="filex-release-date">6 September 2026</span>

The release where writes stopped being silent. Half of filex's write surfaces — WebDAV, SFTP, FTPS, NFS, the S3 gateway, the AI/MCP API, the archive extractor, the async copy worker and the built-in editor — put a file on the storage and told nothing else, so a browser with that folder open never showed it: an explorer with a live socket does not poll, and the periodic sync repairs rows without announcing anything. They all announce now, and a burst is merged into one frame per window instead of one per file, so extracting a five-thousand-file archive no longer leaves the folder empty for two minutes. Antivirus got the other half of the attention. ClamAV can now be a daemon in its own container reached over the network — the filex images ship no scanner, so under Docker or Kubernetes the previous answer was `build your own image` — and the switch, the mode, the address, the size ceiling and a new editor save-scan window moved onto Settings → Protection, with the `FILEX_CLAMAV*` variables demoted to first-boot seeds. Four write paths that were never scanned are now: the text editor, restoring a version, restoring from the trash, and the OnlyOffice save-back — which also announced nothing and re-indexed nothing, so an edited document kept its old text in search. So are files the storage sync finds on the backend rather than through filex, which is the case an operator most assumes is covered. ⚠ One behaviour change to check before upgrading: a write that **replaced** an existing file now emits `file.updated` rather than `file.uploaded`, so a webhook subscribed to the latter in order to see edits has to tick the new one as well. Four long-standing data bugs go with it. The storage sync used to un-delete files on every pass — and since quarantine is the same operation, it let infected files out of quarantine on a timer nobody set. A moved or renamed file lost its version history silently, because the row kept pointing at its old path. A deleted file never gave back its cached thumbnail or its upload staging bytes, so disk only grew. And on backends that report no ETag — local, SFTP, SMB, FTP — a file replaced outside filex was never noticed at all, so its size, its indexed text and its virus verdict stayed as they were forever. On a multi-tenant install, the instance-wide admin surfaces — antivirus and retention, the external editor, the auth providers, the self-upgrade — had no supertenant gate, so any tenant admin could turn scanning off for everybody or point the scanner at a host of their own; they are gated now. Finally the queue: all three drivers honour priority now, so an upload's scan is served ahead of a twenty-thousand-file first import; the Postgres driver, which could never claim a job at all, works; ⚠ and a Redis queue converts its pending list at startup, keeping every queued job, after which downgrading is not supported.

## What changed

### Upgrade notes

- ⚠ **Webhook subscribers: a write that REPLACES a file now emits
  `file.updated`, not `file.uploaded`.** Until this release every write
  announced `file.uploaded` whether it had created a file or overwritten one,
  because the post-write gate hard-coded the id — no filter could tell the two
  apart. A target subscribed to `file.uploaded` in order to see edits will go
  quiet; tick `file.updated` beside it on Settings → Webhooks. A target with an
  empty event list still receives everything, and a target that only ever
  wanted new files needs no change and now gets a quieter feed.

- ⚠ **`FILEX_CLAMAV`, `FILEX_CLAMAV_BIN` and `FILEX_CLAMAV_MAX` are now
  first-boot seeds, not live switches.** The antivirus on/off state, its mode
  and the clamd address live in the settings table and are edited on
  Settings → Protection. An existing `FILEX_CLAMAV=0` survives the upgrade —
  it seeds the row — but changing it in `compose.yml` afterwards will have no
  effect, which is a change in what that variable means.

- **Redis queue users:** the pending queue converts from a list to an ordered
  set on first boot so that priority is honoured. Every queued op is kept, in
  the order the old build was about to serve them. Downgrading afterwards is
  not supported.

### Added

- **Three events the server has been emitting all along are finally
  subscribable, and a fourth that had no name at all now has one.** The
  webhook subscription checkboxes were driven by a hand-written copy of the
  backend's event list, and it had drifted: `file.upload_failed`,
  `file.infected` and `comment.added` were being delivered to targets with an
  empty allow-list but could not be ticked by anyone who wanted only those.
  `file.infected` is the one that matters — filex scans uploads for viruses and
  there was no way to ask it to tell you when it found one.

  A fifth event was worse off. The escrow announcement — *an encrypted folder
  was opened with the recovery key rather than its owner's passphrase*, about
  as security-relevant as this product gets — was written as an inline
  `notify.EventType("e2e.escrow_used")` inside a handler. It is emitted on
  every escrow unlock, and because it was not a constant, nothing that reads
  the event catalogue could see it. It is `EventE2EEscrowUsed` now and appears
  in the list with the rest.

  Every event in the list also gained a label an operator can read, in English
  and Turkish, with the wire name kept underneath the checkbox — the checkbox
  used to be labelled `file.trashed` and nothing else.

- **`file.updated` — a write that replaced an existing file no longer claims a
  new one arrived.** ⚠ **This is a behaviour change for existing webhook
  subscribers.** Until now every write on every surface emitted
  `file.uploaded`, whether the bytes created a file or overwrote one that was
  already there, so nothing downstream could tell "a document arrived in this
  folder" from "somebody edited that document" — and no filter could separate
  them, because they were literally the same event id.

  From this release the post-write gate splits them: **created →
  `file.uploaded`, replaced → `file.updated`.** A target subscribed to
  `file.uploaded` in order to see edits will go quiet and has to tick
  `file.updated` as well (the admin UI lists it next to `file.uploaded`); a
  target with an empty event list still receives everything, and one that only
  ever wanted new files needs no change and gets a quieter feed.

  It is one decision in one place — `writehook.OnFileWritten` now takes the
  kind, so every surface answers the same question from the fact it already
  had: the cache-row lookup it does anyway. That covers the browser upload
  form, WebDAV `PUT`, S3 `PutObject`/`CopyObject`, SFTP, FTPS, NFS, the AI/MCP
  write, the archive extractor and the ops-worker copy. Two paths are called
  out because they answer it differently:

  - The **staged (large-file) upload** asks the storage driver rather than the
    catalogue, one statement before the overwrite. It has to: its node row is
    published at commit time, before the bytes move, so by transfer time the DB
    has a row either way and has forgotten whether it minted it — and unlike a
    flag carried from commit, the driver's answer survives a restart in between.
  - Where a surface genuinely **cannot tell** (the DB mirror was unreachable,
    so there is no row to compare against) it reports `file.uploaded` — the
    value the event carried before, rather than a wrong claim that a file was
    edited.

- **The text editor announced nothing at all.** `/api/files/save-text` wrote
  the bytes, updated the row, re-indexed the file and scheduled the antivirus
  scan — and never emitted an event, so a file created or rewritten in the
  browser reached no webhook and no notification bell. It now goes through the
  shared gate like every other write surface (`file.uploaded` on create,
  `file.updated` on save), using the same `existing` lookup that already chose
  between an immediate and a debounced scan. It keeps its own debounced scan:
  the divergence is in the scan, never in the event.

### Fixed

- **The Office editor was the one write nothing looked at afterwards.** Every
  other write surface in filex fans out through the shared post-write gate —
  announce the change to open explorers, re-index for search, fire the webhook,
  queue an antivirus scan — and this release *extended* that gate to two more
  places. The OnlyOffice save-back was not one of them: it took its version
  snapshot, wrote the revision, refreshed the node row, and stopped. So a
  `.docx` edited in the browser kept its **pre-edit text in content search**,
  never reached a `file.updated` subscriber, left another browser with the
  folder open showing a stale listing, and — on an install where every upload
  is scanned — **was never scanned**. Office documents are exactly the file
  type macro-borne malware travels in, which is what made this the wrong gap to
  ship a release about scan coverage with.

  The callback now goes through the gate like everything else, as a
  **replacement** (`file.updated`, never `file.uploaded` — a save-back
  overwrites a document that was already there) stamped with a new
  `meta.origin: "onlyoffice"`, because the bytes are assembled and posted by
  the document server rather than by the browser that opened the file.

  ⚠ **Whether the scan runs now or on the save window is decided by the
  callback status, not by a rule of thumb.** The reason the browser's text
  editor got a debounced window is that it cannot tell a mid-session Ctrl+S
  from the last one. The document server does not make filex guess:

  - **status 2** (ready for saving) arrives once per editing session, after
    every editor has *closed* the document and the server has assembled the
    final revision — roughly ten seconds after the last one disconnects. The
    bytes are final and nobody is still typing, so it is scanned **immediately**,
    like an upload. Deferring it would coalesce nothing (there is one save) and
    would leave a finished document unscanned for up to a full window.
  - **status 6** (force save) is an *interim* save with the document still
    open. filex never asks for one and the document server does not send them
    by default, but an operator can switch them on, and then they repeat for as
    long as somebody keeps the document open — the shape of a Ctrl+S burst. It
    takes the **debounced** window, the same one the text editor uses.

  A session with force-save on therefore costs one scan per window while it is
  open plus one immediate scan when it closes. That last one is deliberate: a
  document server that dies mid-session never sends status 2, and then the
  debounced scan of the interim bytes is the only one there will ever be.

  ⚠ The driver `Stat` still lands on the row **before** the index runs, and
  that ordering is load-bearing: content re-extraction is triggered by the
  content fingerprint drifting, the fingerprint prefers the etag, and indexing
  against the pre-edit etag would refresh the metadata while leaving the **old
  text** searchable — most of the symptom being fixed.

- **`/api/admin/protection` let any tenant admin turn antivirus off for every
  tenant** — and three other instance-wide surfaces were open the same way.
  Everything under `/api/admin` has passed `RequireAdmin`, and in multi-tenant
  mode that means an admin of *some* tenant: the tenant resolver labels the
  request without denying anything, and the scoped store filters three list
  queries and nothing else. `/providers` and `/plugins` asked the extra
  question. These did not:

  - **`/protection`** — the antivirus switch, its mode and the clamd address
    moved into the global settings table in this release, so one customer's
    admin could disable scanning platform-wide or point the scanner at a host
    they control. Trash and version retention sat beside it.
  - **`/external`** — one document server, one converter, one shared JWT
    secret: repointing it is enough to read and rewrite every tenant's office
    documents in transit, and the Test button dials whatever it is given.
  - **`/auth-providers`** — the global `auth.*` rows that decide who can sign
    in to the instance at all (OIDC issuer and client secret, LDAP bind).
  - **`/update`** — replaces the binary every tenant is served by.

  All four now ask `requireSupertenant`, one predicate shared with `/providers`
  and `/plugins` rather than a second mechanism. ⚠ The check lives in the
  **handler**, not on the chi route, because the route is not the only door:
  `/api/ai/admin` mounts the same handler instances behind an `admin`-scoped
  API token, and the MCP admin tools drive those same methods in-process — a
  route middleware would have closed one of three.

  ⚠ **Reads are gated too, not only writes.** `/protection` returns the clamd
  address in force and a live reachability probe; `/external` returns where the
  document server lives. That is a map of the operator's internal
  infrastructure, handed to somebody with no standing to act on it.

  ⚠ **Single-tenant installs are unaffected, by construction.** The tenant
  resolver attaches no scope when multi-tenant mode is off, absence means
  "unscoped", and unscoped passes — there is no flag to set. The gate fails
  *closed* the other way: an authenticated user whose provider cannot be
  resolved already gets a deny-all scope, and it is refused rather than read as
  "no scope, therefore single-tenant".

  The surfaces that are still instance-wide and still ungated are now
  **named** rather than half-closed — `/settings` (the same global table, but
  `branding.*` keys are legitimately per-tenant, so it needs a per-key
  classification and not a route gate), webhooks, replication targets and
  rules, the search rebuild, the queue, the trash sweep, and a set of
  cross-tenant metadata reads. See
  [MULTI-TENANCY.md](./MULTI-TENANCY.md#instance-wide-admin-surfaces).

- **A 403 that had a sentence to say showed a machine code instead.** The admin
  SPA's error extractor preferred `data.error` over `data.message`, so a
  response carrying both — the two `supertenant_only` and `plugins_disabled`
  shapes — put `supertenant_only` on screen and threw away the explanation
  written for the person reading it. `message` now wins when both are present;
  the handlers that return only `error` are the majority and put the sentence
  there, so they are unchanged.

- **The plugins gate answered "plugins are disabled" before "not yours".** A
  tenant admin who may not touch the surface at all could still learn whether
  the operator had the subsystem switched on. The tenancy check runs first now.
  Single-tenant installs see no difference: no scope is attached, the gate
  passes, and a disabled subsystem still answers `503`.

- **The docs-site build no longer hands you somebody else's diff.**
  `cd docs-site && npm run build` is a mandatory release gate, so everybody
  runs it — and it was `npm run releases && vitepress build`, which refetched
  the GitHub releases and rewrote `docs/RELEASES.md` and
  `docs-site/data/releases.json` every single time, restamping today's date
  even when nothing had changed. Three people hit it in one day, each reverted
  it by hand, and one release nearly committed the churn under an unrelated
  message.

  The build now runs an offline check instead (`check-releases.mjs`: the page
  exists and lists at least one release, no network, no writes), and
  regenerating is an explicit `npm run releases` at the release step that means
  to do it. `docs/RELEASES.md` stays tracked and published — ignoring it was
  never an option, readers see that page.

  The generator is idempotent as well, which is the other half: `generatedAt`
  only moves when the release list actually moved, and neither file is written
  unless its bytes changed. So a curious `npm run releases` cannot dirty the
  tree either — only a real new release can.

- **Two gates now catch the drift that caused the above, instead of a user
  reporting it.** The UI's event list stays a hand-maintained mirror on purpose
  — the admin SPA is compiled into the server binary, so an endpoint listing
  the events could never disagree with the bundled UI and would only add a way
  for the checkbox list to render empty. What a mirror needs is a build-time
  check, so it has one: `web/tests/webhooks/eventCatalog.test.ts` parses the Go
  constants and fails when the two sets differ or an event is missing its
  English or Turkish label, and `backend/internal/notify/catalog_test.go`
  refuses an inline `EventType("x.y")` anywhere in the backend, so an event
  cannot be born somewhere the catalogue does not look.

- **The Redis queue driver ignored `Priority`.** Its pending set was a LIST
  consumed with `BLMOVE`, and a list is positional: an op's priority was
  persisted, returned by `Get` and rendered in the admin UI, and had no effect
  whatsoever on the order ops came out. So the rule the SQLite and Postgres
  drivers enforce — a scan the storage sync discovered sits one step below a
  person's upload, `ORDER BY priority DESC, enqueued_at ASC` — simply did not
  apply on Redis: a first import of 20 000 files was still FIFO and an upload's
  scan still waited behind all of it. Measured on a real Redis with that
  backlog, an interactive op waited **20.0 s** behind 18 000 sweep ops; it is
  now served **first, in 1 ms**.

  The pending set is a **sorted set** whose score encodes `priority DESC,
  arrival ASC` exactly, and claiming is **one Lua script**: the removal from
  pending, the move to running, the status write, the attempt bump and the
  release of the coalescing key happen as one indivisible step. That is
  stricter than what it replaces — `BLMOVE` moved the id and a *second*
  transaction flipped the hash, and a process that died in the gap left an op
  in no list at all, permanently `pending` in its own hash, which
  `RecoverOrphans` then dropped. Type filtering no longer mutates anything
  either: the script walks candidates in priority order and steps over the ones
  this worker cannot handle, instead of popping them and pushing them back.
  `Enqueue` is a script too, so it is one round trip rather than two and can no
  longer leave a coalescing claim behind a write that failed.

  ⚠ **What it costs:** the claim is no longer a blocking Redis command, because
  a script cannot block. A worker with nothing to do now waits on a capped
  doorbell list that every push into pending writes a token to, so an arriving
  op still wakes a worker in about a millisecond. Two honest differences: a
  token can be taken by a worker whose type filter does not match the op that
  produced it, and a drained burst can leave a few stale tokens behind. Each
  costs one extra script call, never a missed op.

  ⚠ **Upgrading:** an install already running the Redis driver has a LIST at
  the pending key, and `ZADD` against a list is a `WRONGTYPE` error. Startup
  converts it, keeping every queued op and the order the old build was about to
  serve them in — and improving it, since those ops now carry their priority.
  Downgrading afterwards is not supported.

- **A file replaced under a local storage was never noticed.** The storage sync
  exists to find changes filex did not make; on the drivers that report no
  etag — **local, SFTP, SMB, FTP**, and any WebDAV server that omits the header
  — it found none, ever. Drift was an etag comparison, and comparing two empty
  strings is never a difference, so a file swapped out underneath filex looked
  unchanged on every pass: its size stayed stale in the catalogue, its extracted
  text stayed stale in the search index (you found the old words, not the new
  ones), and since the sync began queueing antivirus scans, a clean file could
  be replaced with an infected one and nothing would ever read it again.

  Where the backend reports no etag, drift is now the file's **size and
  modification time** — the two fields every one of those drivers does report,
  both already in the listing the walk just made, so a full walk costs what it
  always did (20 000 files on local disk: 3.0–4.0 s per pass before, 3.0–4.0 s
  after, with zero drift reported on three consecutive unchanged passes).

  It catches an ordinary edit, a rewrite that keeps the same size, a file that
  grew or shrank with its mtime preserved, and a restore whose mtime is *older*
  than the row's. It does **not** catch a replacement that preserves both the
  size and the modification time, or a rewrite landing in the same clock second
  as the recorded mtime with the size unchanged — those need the content, and
  hashing every file on every pass is not a walk anyone can run. See
  [STORAGE.md → Drift detection](./STORAGE.md#drift-detection-what-a-replaced-file-looks-like).

  Times are compared **to the second** deliberately: Postgres stores
  microseconds, FTP's `MDTM` has no sub-second field and FAT keeps two-second
  steps, so a finer comparison would report drift on every file on every pass —
  which on an install with antivirus enabled is the whole storage re-scanned
  every sync interval, forever. Directories are exempt for the same reason:
  their row holds the cached *recursive* size, which a listing never reports.

- **`Store.MoveNode` did not update `storage_key`, so a renamed or moved file
  kept the key it had at its old path.** `storage_key` is not decoration:
  versioning (`Snapshot` *and* `Restore`), the antivirus quarantine, the
  id-addressed download in `Manager.Read` and the sync tombstone pass all hand
  it to the storage driver **in preference to** `path`. Every one of them fails
  *silently* on a miss, which is why this survived so long:

  - `Snapshot` stats the stale key, gets `ErrNotFound`, and returns "nothing to
    snapshot" — so the pre-overwrite guard passes with **zero versions written**
    and the destructive write goes ahead. A moved file lost its history.
  - The antivirus quarantine moves the stale key into `.filex-trash/`, tolerates
    the `ErrNotFound`, marks the file quarantined and drops it from the search
    index — **while the infected bytes stay live** at the real path, now
    invisible to any future rescan.
  - `Restore` writes the restored version to the stale path, leaving the real
    file untouched, then stamps the node with the restored size and etag.
  - A download by id 404s on a file the listing shows.
  - `confirmGone` stats the stale key, gets a miss and tombstones a file that
    is perfectly fine.

**This release has more to it than fits on one page.** The rest of the
entry — and every earlier release — is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0340---2026-09-06).

- **Documentation** — &lt;https://docs.filex.sh>
- **Report a bug** — &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** — &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** — &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.34.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.34.0`

## v0.33.0

<span class="filex-release-date">5 September 2026</span>

A release of things that were quietly wrong. A large upload to an S3 endpoint served over plain HTTP died inside the client and the storage sync then moved the file to the trash — reported from the outside, and worth knowing why it had never been seen here: SigV4 hashes the request body over `http` and does not over `https`, so filex was requiring a rewindable stream it never asked for, and the bug sat waiting for the first plaintext endpoint. Overwriting a file now keeps the old bytes: the snapshot happens before the write at every destructive surface, and if it cannot be taken the write is refused rather than losing the file — until now only the in-browser text editor versioned anything, so uploading over a file destroyed it. The S3 gateway stopped exposing filex's own `.versions/`, `.thumbs/` and `.filex-trash/` trees, which every other protocol had hidden since it was written. On a multi-tenant install every absolute URL now names the tenant's host rather than the operator's — share links, the WebSocket endpoint, and the account-created e-mail that carried a temporary password to somebody else's login page. And the product stopped speaking Turkish to people who never asked: the convert dialog, eighteen notifications, the file-request e-mail, and an embed that defaulted to Turkish now follow the locale, or the browser's, or English.

## What changed

### Added

- An i18n key-parity test for the `@brftech/filex-core` catalogue
  (`web/tests/i18n/coreKeys.test.ts`). `web` already had one for its own
  `en.json`/`tr.json`; `packages/core` ships `en.ts`/`tr.ts` but has no test
  runner of its own, so the gate lives in `web`, which depends on the package.
  It also fails on an "English" value that is still Turkish — the shape the
  bug above would take once it wears a key.
- **Almost every absolute URL a multi-tenant install handed out was the
  operator's hostname, not the customer's.** Exactly one place in the codebase
  derived the origin per request — `Auth.redirectBase`, written for the OIDC
  callback and never reused. Everything else concatenated the global
  `FILEX_PUBLIC_URL`: the `/s/` and `/d/` links a tenant sends to their own
  clients, the `wss://…/api/ws` endpoint handed to the browser and opened
  verbatim, the `/s/` and `/u/` URLs returned to AI / MCP / ShareX clients, and
  four e-mails. The worst was the account-created mail: a customer's brand-new
  user received a temporary password next to a link to **someone else's** login
  page — a flow that cannot succeed, and a disclosure of the operator's
  hostname to every tenant that ever adds a user.

  The rule now lives in one place, `internal/tenanturl`, and every builder
  calls it. It has two entry points because not every URL is minted while a
  browser waits: `FromRequest` for the request-driven sites, and
  `ForStorage` / `ForProvider` for the ones reached only through a context (the
  AI/MCP share link and the upload ticket), where the tenant is taken from the
  node's storage rather than from a `Host` header that isn't there.

  **Host-header injection**: the request host is resolved with
  `GetProviderByHost`, which matches an *enabled* provider row exactly, and the
  origin is then assembled from that row's own `host` column — never from the
  request string. An unknown, disabled or absent host falls back to
  `PublicURL`, so `Host: evil.example` mints the operator's configured URL and
  `evil.example` never reaches a link or an e-mail.

  **Single-tenant installs are unchanged.** With `FILEX_MULTI_TENANT` off the
  resolver does not read the request at all (asserted, not assumed: the test
  counts store lookups and requires zero), so the call sites that have no
  request in scope lose nothing. Covered by a new multi-tenant suite that
  drives the real handlers — and, for the four e-mails, a real in-process SMTP
  server, so the assertion is on the bytes a recipient receives.- **The public export's link check had never run once.** `scripts/export-public.sh`
  ends by running `scripts/check-links.mjs` over the tree it just built and
  refusing the export if anything is dead — the guard added on 2026-09-05 after
  two dead links sat in the public README. It was wrapped in
  `if command -v node; else echo "node not found: skipped…"; fi`, and on the
  maintainer's machine it took the `else` branch **every time**: the script is
  invoked as `wsl bash -lc 'bash scripts/export-public.sh …'`, and that WSL had
  nvm and three node versions installed but `node` on no shell's PATH — the
  interactive guard in `~/.bashrc` returns before the nvm block for
  non-interactive shells, and for interactive ones a hard-coded `export PATH=`
  two lines later wiped what nvm had just added. The export succeeded, printed
  its own excuse, and nobody read the line.

  A guard that degrades to nothing is not a guard, so **no node is now fatal**,
  and the script looks for one before giving up: `PATH`, then the newest
  `$NVM_DIR/versions/node/*/bin/node`, then — under WSL — the Windows
  `node.exe`. ⚠ That last one is a *Windows* binary: handed `/mnt/g/…` it
  resolves the path against the current drive and dies with
  `Cannot find module 'G:\mnt\c\…'`, so both the checker and the tree are
  converted with `wslpath -w` first. All three branches were exercised:
  nvm node → `410 relative links … all resolve`; Windows node → the same 410;
  no node at all → exit 1 with a refusal. A deliberately dead link
  (`README.md` → `docs/MIGRATION.md`, which the export withholds) is refused
  with exit 1.

### Fixed

- **The admin UI named a private repository, and every published screenshot
  showed it.** The footer and the About page linked
  `github.com/brf-tech/filex` — the internal development repo, which
  answers 404 to anybody who is not us. `scripts/export-public.sh` rewrites that
  string on the way out, so the *shipped source* was right; a **screenshot** is
  a PNG and no rewrite reaches inside one, so the images in the public README
  showed the dead URL. They now name `github.com/BRF-Tech/filex`, which is where
  the product actually lives — including on our own installs, where a link to a
  repository the reader cannot open was never useful either.

- **Uploads over 8 MiB never reached an S3 storage over plain HTTP, and the
  sync then moved them to trash** (GitHub #16). Reported against a fresh Garage
  deployment: every upload looked like it worked, the bucket stayed empty, and
  the next storage sync put the files in the trash. Files under a few MB were
  fine. Reproduced end to end against a real Garage instance.

  Two independent defects, and the second is the one that cost the user data.

  **1. The commit could not sign a multipart part.** The browser posts files
  under 8 MiB in one request; above that they take the staged path, where the
  commit re-chunks the staging area into a driver multipart upload. Each part
  was cut with `io.LimitReader`, which drops the `Seek` method the staging
  reader has. SigV4 signs the SHA256 of the payload, so the SDK reads a part to
  hash it and then rewinds to send it — with nothing to rewind the request died
  inside the client, before a byte left the process:
  `upload part 1: operation error S3: UploadPart, failed to compute payload
  hash: failed to seek body to start, request stream is not seekable`.

  ⚠ It is the SCHEME that decides this, not the provider — measured, because
  the obvious guesses are wrong. Over `https://` the SDK sends
  `x-amz-content-sha256: UNSIGNED-PAYLOAD`: TLS already protects the body, it is
  never hashed, and a plain reader has always worked. Over `http://` the payload
  hash is what binds the body to the signature, so the body must be read twice.
  Every S3 endpoint filex had previously been pointed at is `https://`; the
  reporter's Garage, a container on a podman network, is not. The bug was
  waiting for the first plaintext endpoint, and it would have hit AWS, MinIO or
  Hetzner over plaintext just the same.

  Parts are now cut with a rewindable, length-bounded window over the staging
  reader, and `UploadPart` on the S3 driver makes any body rewindable before it
  hands it to the SDK — small ones in memory, large ones through a temp file,
  an already-seekable one passed through untouched. The driver's signature
  promises `io.Reader` and now honours it, instead of silently requiring a
  `Seeker` and failing with a message that names the SDK rather than the call.

  **2. A failed upload was silent.** The commit endpoint answers `202` as soon
  as the last chunk lands; the transfer happens afterwards in the ops worker. A
  transfer that failed there produced one `WARN` line in the server log and
  nothing else — the node stayed at `transfer_state="staged"`, which is
  indistinguishable from still-in-flight, no notification fired, and the browser
  had already drawn a finished upload. The client now waits for the transfer by
  default (the `transferring` phase, which was implemented but which no caller
  ever switched on) and reports the failure; the node moves to a new
  `transfer_state="failed"`; and a `file.upload_failed` notification, carrying
  the reason, goes to whoever uploaded.

  ⚠ While turning that wait on, a latent bug in it surfaced: it told a real
  verdict from an incidental fetch error by comparing the message to the literal
  string `"transfer failed"`, which only matches when the server sends no error
  text. Any real error message was swallowed and the poll span for ever. The two
  are now told apart by type, and a run of unreadable polls ends the wait
  instead of hanging.

- **Uploading a file over an existing one destroyed the old bytes, and nothing
  kept a copy anywhere.** `versioning.Service.Snapshot` documents its own
  contract — *"callers should invoke this BEFORE a destructive write… if the
  snapshot itself fails the caller should NOT proceed"* — and the package doc
  stated as fact that snapshots were taken on upload finalize and archive
  extract. Neither was true. Across the whole product `Snapshot` was reached
  from exactly two places: `save-text` and the versions endpoints. No upload,
  drop, ShareX, AI/MCP write, archive extract, OnlyOffice save-back, WebDAV
  `PUT` or S3 gateway write ever called it. Measured on a clean instance:
  uploading `up.txt` twice left `GET /api/files/versions?node_id=1` answering
  `{"versions":null}` with no `.versions/` tree on disk at all, while editing
  the *same* file in the browser recorded one — so version history looked like
  it worked right until the moment you needed it.

  Every destructive write now calls `writehook.BeforeOverwrite` first, which
  snapshots whatever is about to be replaced and **refuses the write** if that
  snapshot cannot be taken: 503 with `"code": "SNAPSHOT_FAILED"`, existing file
  untouched. The covered surfaces are the browser upload (single-POST and
  staged), the public drop link, the ticketed upload, the legacy presigned
  multipart finalize, ShareX, the AI/REST and MCP write/zip/unzip tools,
  archive extract and add, OnlyOffice's save-back, WebDAV `PUT`, and the S3
  gateway's `PutObject`, `CompleteMultipartUpload` and `CopyObject`.

  ⚠ On the staged path the guard runs at **commit**, not at the driver write.
  Two reasons, and both are load-bearing. Publishing the staged node flips it
  to `transfer_state="staged"`, after which `filebody` answers with the
  *incoming* bytes — a snapshot taken later would record the wrong content.
  And `transfer()` picks between two write mechanisms: on any driver
  implementing `storage.PartUploader` — i.e. S3, which is what real
  deployments run — it calls `streamMultipart` and never touches
  `storage.Writer.Write` at all. A guard hung off the driver write would have
  protected small uploads and silently skipped every large one on exactly the
  backend where it matters most. There is a test pinning the S3-shaped case
  that asserts the object really did arrive via `CompleteMultipart`.

  `save-text` is brought under the same rule rather than left as the
  exception: it used to log `snapshot failed (continuing with write)` and
  overwrite anyway, directly contradicting the contract quoted two lines above
  its own call.

  Archive extract and the AI/MCP `unzip` tool differ deliberately: they skip
  just the refused member and keep going, reporting a `refused` count distinct
  from the permanent, user-caused skips in the same loop (a zip-slip entry, a
  file/folder kind clash). If every member was refused they answer 503 rather
  than a misleading `200 {"count":0}`.

  `FILEX_VERSIONS_ON_OVERWRITE=0` turns the guard off for a deployment whose
  storage cannot afford the extra write; `FILEX_VERSIONS_FAIL_OPEN=1` keeps
  attempting the snapshot but lets a failed one through. Both default to the
  safe value and log a WARN at boot when they are not at it — the fail-closed
  default has a sharp edge worth naming, because if the object store fills up
  then every overwrite on the instance is refused until an operator changes an
  env var and restarts.

- **The S3 gateway exposed filex's own internal trees.** `.versions/`,
  `.thumbs/` and `.filex-trash/` were listable AND readable by known key
  through `/s3` — measured, `GET .versions/42/1` answered 200 — while `/dav`,
  `/sftp`, `/ftp`, `/nfs` and the browser listing had all hidden them since
  they were written. They are now refused at any depth on listing, read and
  write. This was always wrong and became urgent with the guard above: before
  it, `.versions/` held a handful of text-editor snapshots; after it, it holds
  a copy of every file any surface has ever replaced, so leaving it reachable
  would hand any S3-key holder the prior contents of files whose folders they
  may since have lost access to.

- **A failed upload named neither the file nor the reason.** The access log
  said only `method=POST path=/api/files/manager status=500`, and no
  write-failure branch logged a filename, so "which file failed yesterday
  afternoon" had no server-side answer. Both the browser upload path and the
  staged transfer — whichever of its two driver write mechanisms fails — now
  log `msg="upload failed"` with `storage`, `path`, `name`, `size` and
  `reason`.

- **`POST /api/files/archive/add` leaked a file descriptor on every error
  path.** Only the success path closed the temp file it built the archive in;
  each of the early returns dropped it. Closed with a `defer`, registered
  after the `defer os.Remove` so the two unwind in the right order.

- **filex spoke Turkish to English users, and no locale setting could stop
  it.** ~45 strings across the explorer, the viewers, the admin UI and the
  backend were hard-coded Turkish literals sitting *beside* the translation
  layer, not inside it. They are now keys in `en`/`tr` and go through `t()`.

  The widest one was the whole **convert modal**. It resolved its labels
  through a private `tt(key, fallback)` helper backed by an optional `t?` prop
  — and three separate things had to be true for it to ever produce English:
  the caller had to pass `t` (`FileExplorer.vue` never did), the `convert.*`
  keys had to exist (they existed in *neither* catalogue), and the prop's
  signature had to match `useLocale`'s (it did not — it declared
  `(key, fallback)` where the real `t` is `(key, vars)`, so passing the real
  one would have rendered the raw key, or spliced the fallback's characters in
  as `{0}`, `{1}` … substitutions). Every user in every locale read Turkish.
  The helper is gone: the modal now takes `locale` and calls `useLocale`, like
  every sibling modal, and its ten labels are real keys.

  Also: ~20 explorer toasts and the drag-and-drop overlay
  (`İşlem başarısız`, `Kopyalandı` / `Taşındı` / `Silindi`, `Kesildi`,
  `Aynı klasöre kesilemez`, `… kuyruğa alındı`, `… öğe geri getirildi`, the
  trash-retention notice, `Dosyaları buraya bırak`); the new-folder validation
  error; the presence bar's people count; the CSV viewer's row count; five
  strings in the preview modal (two of them sitting next to a correct
  `&#123;&#123; t('viewer.download') }}`); and the SMTP password placeholder in
  `Settings.vue`, whose `label` and `hint` on the same line were already
  translated.

  Where a key already said the same thing it was reused rather than
  duplicated — the sharpest case being the paste toast, which was
  `t('split.cross_copy')` on the cross-storage branch and a Turkish literal on
  the same-storage branch of the *same expression*, while
  `split.copy_queued` ('Copy queued' / 'Kopyalama kuyruğa alındı') already
  existed and was exactly it.

- **A file drop notified the folder's owner in Turkish regardless of their
  language.** `Drop.notifyOwner` never consulted a locale, and its output is
  not confined to one surface: the same title and body go to the in-app
  notification bell, into the **webhook v2 `drop.received` payload** and into
  the owner's **e-mail**. The `Gönderen:` header written into the persisted
  `NOT.txt` beside the uploaded files had the same problem.

  These now follow the same `mailLangEN` selection `mail_templates.go` next
  door already used. The locale is the **folder owner's** (`users.locale`, via
  the new `Drop.ownerLocale`), not the uploader's: a drop link is opened by an
  anonymous visitor who has no account and therefore no locale, and the owner
  is the only person who reads any of it.

- The admin panel's SMTP **Test** mail sent a Turkish sentence followed by an
  English one to whatever address was typed. It now follows the acting
  admin's locale.

- `DrawioViewer`'s untranslated fallbacks were Turkish where every sibling
  fallback (e.g. `CsvViewer`'s `'Loading…'`) is English. The fallback is what
  an embedder who does not pass `t` actually reads.

- `desktop`: the drag-out diagnostic trail logged `'BULUNAMADI'` where every
  neighbouring `dragLog` value is English. This one is **not** a translation
  key — it is a developer log, and it is now `'not found'`.

**This release has more to it than fits on one page.** The rest of the
entry — and every earlier release — is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0330---2026-09-06).

- **Documentation** — &lt;https://docs.filex.sh>
- **Report a bug** — &lt;https://github.com/BRF-Tech/filex/issues>
- **Full changelog** — &lt;https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md>
- **Every release** — &lt;https://github.com/BRF-Tech/filex/releases>

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.33.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.33.0`

## v0.32.0

<span class="filex-release-date">5 September 2026</span>

The explorer gained a third shape. `uiProfile: 'drive'` lays it out the way someone arriving from Google Drive expects — one **+ New** menu, a single search field across the header with the command palette behind it, Type / Modified / Size filters, Folders and Files as labelled sections, and Details / Activity in the info panel. It is a preset of the same explorer rather than a second UI, so the npm packages and embeds get it too, and a non-admin lands on it. Windows finally has a copy that runs without being installed: one .exe that keeps its data beside itself, so deleting one folder leaves nothing behind on a machine that is not yours — Linux and macOS already had this. On encrypted folders, an installation that already exists can now adopt key escrow, and — the part that makes escrow reach anything real — the owner of a folder created before escrow was turned on can grant it themselves, from the unlock dialog, with the consequence spelled out. Search stopped treating numbers as typos, so `2026` no longer returns last year's report. Several things that had been quietly broken were fixed rather than found by users: the install banner covered the sign-in button, middle-click did nothing in gallery view, a search at a storage root answered with a folder called Trash that is not a folder, and the file-infected notification spoke Turkish to everybody.

## What changed

### Added

- **`uiProfile: 'drive'` — the explorer's shell, laid out the way an end user
  expects (GitHub #14).** The reporter of #14 came back to v0.30.1 with four
  annotated mockups: the navigation panel was right, and here is what the rest
  could look like. This is that, as a third **profile** of the same explorer —
  not a second UI. It is a strict superset of `simple`, so everything `simple`
  turns off stays off, and a non-admin signing in at `/drive` now lands on it.

  - **One primary "+ New" menu** in the panel, in place of the Upload / New
    folder pair: upload files · new folder · request files (the file-drop link,
    which opens the access modal straight on its own tab).
  - **One search field across the header**, with a ⌘K / Ctrl+K chip. The field
    searches the folder you are standing in — it sets the same `searchQuery`
    the toolbar box always did — and the chip (or the shortcut, pressed from
    inside the field) hands that query to the **command palette**, which is
    where "everywhere", saved searches and the commands live. One box, one
    shortcut, no third search surface.
  - **A filter row** under the breadcrumb: **Type · Modified · Size**.
  - **Folders and Files as labelled sections** in grid view, with the rows
    grouped rather than trusted to arrive grouped.
  - **The details panel split into Details and Activity**, with **People with
    access** (real grants from `GET /api/files/permissions`, hidden rather than
    faked when the server refuses them) and a **share-link row with a Create
    link button**. Activity is version history + comments.
  - **A storage line** under the navigation, from `GET /api/files/quota/me`.

  Nothing is deleted: density, theme, the shortcut editor, the tour and the
  other view modes moved into the header's "⋯" menu, and the view switcher and
  details toggle onto the breadcrumb row. Both locales, light and dark, a rail
  at 56px and a drawer under 560px.

  ⚠ **No People filter and no Owner column**, though the mockups draw both. A
  listing row carries no owner — `nodes.owner_id` is quota bookkeeping, nil for
  anything a sync discovered, and serialized by nothing — and the listing
  endpoint reads no owner parameter. The three chips that shipped are answered
  from fields every row already has; a fourth would have opened, offered names
  and changed nothing.

- **A Windows copy that runs without being installed.** Linux and macOS already
  had one — the AppImage runs unextracted, the mac `.zip` is unzip-and-run —
  and Windows shipped only an NSIS installer, so a locked-down work machine, a
  USB stick, or "I just want to look at my files on this laptop for ten
  minutes" had no answer at all. `filex-desktop-portable-x64.exe` is one file:
  put it anywhere and double-click it.

  **It keeps its data beside itself, not in `%APPDATA%`.** For an installed app
  the roaming profile is right; nobody wants a program scattering folders
  across their desktop. A run-and-delete copy is the opposite case, and what it
  promises is that deleting one `filex-data` folder leaves nothing of yours on
  a machine that is not yours. That covers the account store, settings, the
  log, the drag-out cache, open-with working copies — and the sync engine's
  bookkeeping, which needed the new **`FILEX_SYNC_DIR`** in the CLI: its store
  defaults to `~/.filex/sync` and holds the local trash, i.e. real copies of
  files it deleted, in somebody else's home directory. *Settings* shows the
  exact path with a button that opens it, because a promise the user cannot
  verify is only a claim.

  ⚠ **It does not update itself, and says so.** A portable `.exe` is not an
  installation: there is no install directory to patch and no installer to hand
  the running copy over to. So it takes the route the unsigned macOS build
  already established — the updater is never wired at all, nothing is
  downloaded that could not be applied, and *Settings* reports a new version
  with a **Download** button instead of sitting at "Checking…" forever. Its
  own words, not the macOS ones: updating means putting a new `.exe` over the
  old one, and the folder beside it keeps your accounts.

  ⚠⚠ **If the `.exe` sits somewhere it cannot write** — `C:\Program Files`, a
  read-only stick, a share — it falls back to
  `%APPDATA%\@brftech\filex-desktop-portable` and says so, with the path. The
  fallback is deliberately *not* the ordinary user-data directory: measured
  before it was fixed, that put the portable copy straight into the **installed
  app's own profile** — a copy carried in from outside reading and writing the
  accounts of whoever owns the machine, and, because the single-instance lock
  is keyed on that directory, exiting silently and raising the installed app's
  window whenever it was already running.

  ⚠ **Your accounts do not travel between machines.** Tokens are sealed with
  the machine's own keychain (Windows DPAPI), so a `filex-data` folder opened
  on another computer, or under another Windows account, fails to decrypt and
  you sign in again. That is the right way round for a stick left on a train.

  `scripts/portable-e2e.mjs` covers it, because a package that *opens* is not a
  package that *works* and "portable" is a claim about where files go that no
  packaging step checks: it launches the real self-extracting `.exe` and waits
  for a renderer to actually load a page, checks that the only things left in
  the folder are the `.exe` and `filex-data`, and asserts the artifact is named
  so it neither overwrites the installer (both targets emit `.exe`) nor carries
  a version (the download links resolve one fixed filename).

- **An existing installation can adopt key escrow.** v0.31.0 pinned
  `FILEX_INSTALLATION_E2E_ESCROW_KEY` at the first boot and refused *every*
  later change — including "no escrow" → "escrow". That read well and measured
  badly: both of our own deployments came out of the rollout with
  `"e2e_escrow_kid": ""`, so acting on the decision to turn escrow on meant
  discarding the data directory. And the general case is worse than ours: a
  product where escrow can only be chosen in the first second of an
  installation's life is a product where nobody chooses it, because nobody
  decides key-escrow policy before they have any files.

  Setting `FILEX_INSTALLATION_E2E_ESCROW_ADOPT=1` alongside the key adopts it,
  once. The flag is read only while the pinned record has no escrow key, so it
  is inert afterwards and can be dropped on the next deploy.

  ⚠⚠ **Adoption is not retroactive and cannot be made so.** A folder wraps its
  master key to the escrow identity when it is created; a folder that already
  exists carries no such copy, and writing one needs the folder password, which
  the server has never had. Folders older than the adoption are outside the
  escrow key permanently. filex says this in the refusal you get without the
  flag, in a WARN line on the boot that adopts, in the record, and in the
  unlock dialog of every affected folder — which now explains the missing
  Escrow tab instead of just not having one.

  The record keeps the two dates apart, because "when was this installed?" and
  "when did it gain a second key?" are different questions:
  `pinned_at` / `pinned_by` still describe the first boot, and
  `e2e_escrow_adopted_at` / `e2e_escrow_adopted_by` /
  `e2e_escrow_adoption_note` describe the adoption. `e2e_escrow_adopted_at` is
  the boundary an operator compares a folder's age against.

  ⚠ Still refused, flag or no flag: pointing the key at a **different** value,
  and **removing** it. Those two leave folders behind that the running
  configuration can no longer describe; adoption leaves nothing behind.

- **`scripts/check-doc-anchors.mjs` — a dead `#section` now fails the build.**
  `vitepress build` fails on a dead *page* link and says nothing whatsoever
  about the anchor half, so the green docs build the release process leans on
  was evidence that every page exists and evidence of nothing else. The new
  check resolves every in-page link in `README.md`, `CHANGELOG.md`, `docs/`,
  `desktop/README.md` and the npm package READMEs, and exits non-zero listing
  the ones that land nowhere.

  ⚠ It reads the heading ids out of the **built HTML** — and, for the pages the
  site does not build, out of VitePress's own `createMarkdownRenderer` loaded
  with this site's markdown options. It does not re-derive the slug rule. A
  second implementation drifts from the real one and starts reporting links
  that work, and a check that cries wolf gets deleted. The two paths are
  cross-checked against each other on every page that has both, so if they ever
  disagree the check exits 2 instead of answering confidently.

  Wired into the `lint` stage as `lint:docs`, which builds the site once and
  runs both gates against that build — the docs site had not been built in CI
  at all until now — and into `docs/CONTRIBUTING.md` → *Release process* step 3
  as a fourth mandatory closing command.

- **An existing encrypted folder can be given an escrow slot — by its owner.**
  Adopting escrow covers folders created after the adoption, which on an
  installation that has been running for a while is nobody's folders: the ones
  that matter already exist. An escrow key that reaches none of them is a key
  to an empty room.

  So when the installation has an escrow key and a folder does not, filex asks
  the folder's **owner**, at the one moment the folder password exists in a
  browser — immediately after a successful unlock. The notice says what
  accepting actually means rather than "enable escrow?": the operator gains a
  second, permanent way in, without the password, and the use-notification is
  an announcement rather than a control. It links to
  `docs/E2E-ENCRYPTION.md`, which says the same thing at length.

  Accepting rewrites only `.filex-e2e.json`. **No file is re-encrypted, moved
  or rewritten**, and the password and recovery key keep working unchanged.
  Doing nothing leaves the folder exactly as it was.

  Declining is a decision, not a delay: it is recorded in the folder's marker
  (`esc_declined`) and filex stops asking — a question that returns on every
  unlock is how people learn to click past security dialogs. The record lives
  in the marker rather than in browser storage because the unit of the decision
  is the folder: the same person on their phone is not asked again, and an
  answer that vanished with a cleared cache would be no answer. It travels with
  the folder through a move, a backup and a restore, exactly as the key slots
  do, and holds no key material.

  The way back for somebody who changes their mind is **Escrow key…** in the
  strip above an unlocked folder. It asks for the password again — the same
  proof of ownership the offer at unlock had.

  ⚠ This changes nothing about what the **operator** can do, and the threat
  model is unchanged: adding a slot needs the folder master key, which needs a
  credential the server has never held. No configuration change, admin action
  or future version reaches an existing folder. The door opens from the inside
  only. `e2e_escrow_adopted_at` keeps its meaning — the boundary of
  *automatic* coverage — and a folder whose owner granted a slot is simply no
  longer described by it.

  ⚠ The offer never appears where it could not be honoured: no installation
  key, an existing slot, a failed unlock, or a v1 marker (whose path is the
  recovery upgrade, which seals an escrow slot in the same step and discloses
  it in the same prompt).

### Fixed

- Three surfaces said an escrow key could **never** open a folder created
  before escrow — the locked-folder dialog ("today or ever"), the
  `e2e_escrow_adoption_note` the server writes into `installation.json`, and
  `docs/CONFIGURATION.md`. That was true of the operator and false of the
  folder, and it is exactly the shape of wrong information that reads as
  authoritative. All three now separate the two: no operator action reaches an
  existing folder, and its owner can grant one.

- **The backend spoke Turkish to every user, once.** The `file.infected`
  notification carried a Turkish title while all eleven of its siblings were
  English — the one message a person reads at the worst possible moment, when
  something on their server has been flagged as malware. `docs/PROTECTION.md`
  documented the Turkish string faithfully, so the page was honest and the
  product was the defect.

- **A page withheld from the public repository was published to the world, and
  the public README pointed at a file that is not there.** `docs/MIGRATION.md`
  is a migration guide for an in-tree package that was never public; the export
  script strips it. It was not in `srcExclude`, so the docs site built it, the
  sidebar linked it and search indexed it — while the *published* README and
  docs index linked a file the published tree does not contain. Neither half was
  catchable: the release process checks links in the source tree, where every
  file exists by definition, and the tree that actually ships had never been
  checked at all. `scripts/check-links.mjs` now checks it, the export script
  runs it on what it just built and refuses to call the export good if it fails.

- **A search stopped answering with a folder called Trash.** The explorer draws
  a virtual `.trash` row at a storage root, and it decided where to draw it from
  the listing's `dirname`. A search answers with the *scope* it searched, so
  `?action=search&path=main://&filter=notes` comes back carrying
  `dirname: "main://"` — byte-identical to the folder listing of that same path.
  The rule could not tell the two apart, so searching a storage root answered
  with the hits **plus** a 0-byte folder named Trash that is not a folder, is
  not a hit, and cannot be searched for.

  Measured rather than guessed: the server sends no `.trash` row for either
  action, and `isStorageRootDir` is true for both responses — the row was always
  the client's, and only the call site knows which of the two it just asked for.
  `injectTrashRow` now takes that answer explicitly. Same family as the
  `.trash` / `.shared` sentinel bugs fixed earlier in this cycle: a virtual row
  rendered somewhere it has no meaning.

  Found in a screenshot, which is the other half of the story — the picture the
  README was going to ship for `uiProfile: 'drive'` had been taken mid-search,
  so it showed the phantom next to the one real hit.

- **Search no longer typo-matches numbers.** v0.30.0's fuzzy pass applied to
  every query word, so `2026` returned `annual report 2025.docx` — ranked
  second — because one digit is one edit. All-digit words are now matched
  literally: a near-miss digit is a different year, invoice or order id, not a
  misspelling of the same one. Exact, prefix, substring and separator-blind
  matching on numbers are unchanged (`2026` still finds `invoice_2026.pdf` and
  `Budget-2026.csv`), mixed words like `v2024x` stay typo-tolerant, and a real
  word typo (`mian.go` → `main.go`) still resolves.

- The unlock-without-the-password dialog labelled the escrow field with the
  **installation's** key id whatever the folder's own escrow slot said, so a
  folder restored from another installation looked openable with a key that
  cannot open it. It now reads the folder's `kid` and names the case.

- **A phone scrolled sideways on the file explorer.** `/admin/explore` and
  `/drive/explore` measured 398 CSS pixels wide inside a 390-pixel viewport —
  the width of an iPhone 12/13/14, and the screen every non-admin lands on. The
  8 pixels were the "Admin panel" *label* in a row that is otherwise icon
  buttons; below `sm` the button keeps its icon and its accessible name and
  drops the text. The label, not the button, is what goes, because the same
  string is longer in every other language filex speaks.

- **The desktop app's pairing never finished for an admin who signed in with a
  password.** The browser half of the hand-off only ran when the app booted, so
  it caught the routes that reload the document — the OIDC callback, and the
  navigation to `/drive/explore` that a non-admin gets — and missed the one that
  does not: an admin's password login stays inside the SPA, nothing remounts,
  and the desktop app sat on its waiting screen until it timed out. The
  hand-off now runs when a session appears, whichever way it appeared. The
  pairing is still minted exactly once.

- **`filex serve` sent no `Content-Type` for `.webmanifest`.** It fell through
  to `http.DetectContentType`, which sees JSON text and answers
  `text/plain; charset=utf-8` — on every platform, not just Windows. It is now
  `application/manifest+json`, said explicitly. A *missing* manifest also 404s
  instead of being served `index.html` by the SPA fallback, which is the break
  that leaves an app un-installable while every status code says fine.

- **"Test connection" took half a minute on an unreachable endpoint.** The S3
  driver retries six times with a backoff capped at ten seconds so that a
  *sync run* rides out an object store's transient 503; run from a button, those
  attempts took a measured 23.6s and 29.7s before answering. The retry budget is
  right where it lives, so the probe got a deadline instead
  (`handlers.ProbeTimeout`, 10s) — which bounds a hung SFTP or WebDAV dial too —
  and a probe that runs out of time now says so rather than showing the SDK's
  paragraph about attempt counts. A local-path probe was and remains ~6ms.

- **A failed login told the server as little as it told the caller.**
  `local.Driver.Login` folded a wrong password, an unknown account, an account
  with no local password and *any error from the user lookup* into one
  `401 invalid credentials`. The answer is unchanged — a single unhelpful 401 is
  what stops an attacker enumerating accounts — but each case now names itself
  in the server's log, and a store failure is no longer reported as
  `ErrUnauthorized`, so `auth.LoginChain` can tell "wrong password" from "the
  directory is down". The two-factor refusals log their reason too.

- **98 of 366 in-page documentation links pointed at nothing on docs.filex.sh.**
  Every table of contents at the top of a `docs/*.md` page, the README's link
  into `MCP.md`, both npm package READMEs — anything whose target heading held
  an `&`, a `/`, an em dash, a dot, an apostrophe or a leading number.

  The links were not sloppy. They were **correct GitHub anchors**: these pages
  are read on two surfaces, and GitHub's slug rule is not VitePress's.
  `## Backup & restore` is `#backup--restore` on GitHub and was
  `#backup-restore` here; a heading reading *Token kinds — `user` vs `app`* is
  `#token-kinds--user-vs-app` there and was `#token-kinds-—-user-vs-app` here,
  em dash and all. Rewriting the 98 links to VitePress's ids would only have
  moved the breakage onto GitHub, so the site's slug rule was changed to
  GitHub's instead (`docs-site/.vitepress/github-slug.mjs`, measured against
  `api.github.com/markdown` and pinned by `web/tests/docs/anchorSlug.test.ts`).
  One rule, both surfaces — which is also what lets a single check guard both.

  ⚠ Anchors on docs.filex.sh that contained one of those characters have
  changed. An external deep link into a section whose heading holds an `&` or
  an em dash now needs the GitHub spelling, which is the one the docs
  themselves use.

**This release has more to it than fits on one page.** The rest of the
entry — and every earlier release — is in [CHANGELOG.md](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0320---2026-09-05).

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.32.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.32.0`

## v0.31.0

<span class="filex-release-date">5 September 2026</span>

Forgetting the password on an encrypted folder no longer means losing the files. Creating one now shows a recovery key once — filex never stores it, and it opens the folder without the password. The file format did not change: existing encrypted files are byte-identical and open unchanged, verified by a round-trip test against a frozen copy of the previous release's crypto module. A folder made before this release cannot be given a recovery key by the server, because the server has no password to re-wrap with, so filex offers the upgrade at the one moment it holds one — the next successful unlock. Operators who need a second way in can set an escrow key at install time; it is RSA-OAEP, filex keeps only the public half, and the documentation says plainly that this is a backdoor and what its notification can and cannot promise. Also here: starring is a real action rather than a badge that only existed in list view, tags are browsable from the navigation panel, opening a virtual view like Trash from its own URL no longer says "Folder not found", and API tokens are split into user and app kinds so a shared embed credential can no longer manage its owner's keys.

## What changed

### Added

- **Encrypted folders can be recovered.** Until now, forgetting the password
  meant the files were gone — that was the documented behaviour and it was a
  bad one. Creating an encrypted folder now shows a recovery key **once**;
  filex never stores it and it opens the folder without the password.

  The file format did not change. Each file's key is still wrapped by exactly
  one key in its 97-byte header — that key is now the folder's master key
  rather than the password key, and the marker holds the master key wrapped
  once per way in. For a folder created before this release the two are the
  same thing, so **existing encrypted files are byte-identical and open
  unchanged**; there is a round-trip test against a frozen copy of the v0.30.1
  crypto module, in both directions, because that is the promise that matters
  most here. Such a folder cannot be given a recovery key by the server — it
  has no password to re-wrap with — so filex offers the upgrade at the one
  moment it holds one: the next successful unlock, behind a visible notice.

- **Optional operator escrow, fixed at install.** `FILEX_INSTALLATION_E2E_ESCROW_KEY`
  adds a second way into every folder created while it is set. It is RSA-OAEP:
  filex holds only the public half, `filex e2e-escrow keygen` prints the private
  half once and writes it nowhere, and the operator supplies it back when they
  need it. A stolen database therefore decrypts nothing.

  ⚠ **This is a backdoor, deliberately, and the documentation says so.** When
  the escrow key is used through filex the folder's owner is notified, and a
  forged "escrow was used" report is refused — the client must first decrypt a
  server-issued nonce sealed to the escrow key. But the notification is an
  announcement, not a control: an operator holding the private key can copy the
  marker and the ciphertext off disk and decrypt offline, with no request, no
  notification and no audit row, and filex cannot detect it. If that is not
  acceptable for your deployment, leave escrow off.

  `FILEX_INSTALLATION_` is a new prefix for settings that are fixed when the
  data directory is initialised. filex refuses to start if one of them changed,
  because for escrow the immutability is arithmetic rather than policy: a folder
  created while escrow was off has no escrow-wrapped key, and switching it on
  later cannot open it.

- **Star is a real action.** It was rendered in the list view and nowhere else,
  so v0.30.0 shipped a Starred view that a user in grid view had no way to fill.
  It is now in the context menu ("Star" / "Unstar", multi-selection aware), on
  grid and gallery cards (on hover or focus, and painted permanently once
  starred, so the Starred view is legible without hovering every tile), and on
  the keyboard as a remappable `S`.

- **Tags in the navigation panel.** Tagging has existed for a long time and
  there was no way to browse by tag inside the explorer — only an admin page.
  The panel now lists the tags that exist and opens the files carrying one.

### Fixed

- **A virtual view opened from its own URL said "Folder not found".** The
  explorer writes the current location into the address bar, so opening Trash
  put `#.trash` there — and on reload that sentinel was handed to the ordinary
  folder loader, which 404'd. Reported for Trash; Recent, Starred and Shared
  behaved the same way. Sentinels are now routed to their view before the
  request is made, so a reload lands back in the view with its own empty state,
  and an unknown dot-path is left alone because a user may own `.config`.

- **The details panel printed `.starred`.** Third surface of the same bug that
  put `.shared` in the tab strip in v0.30.0: the sentinel-to-label map had been
  written more than once. Every surface that renders a path segment now reads
  one map.

- **An app token could manage its owner's credentials.** An API token
  authenticates *as* its owner, and the embeds we run authenticate every visitor
  with one shared token injected by the host's proxy — so v0.30.0's "API keys"
  panel entry meant an embed visitor could list and revoke the credential the
  embed itself runs on, and mint S3, SSH and NFS credentials as the owner.

  A token now declares what it is. `user` is a person's own credential and
  nothing changes for it; `app` is an integration, and the four credential
  surfaces (`/api/tokens`, `/api/auth/s3-keys`, `/api/auth/ssh-keys`,
  `/api/auth/nfs-exports`) refuse it with a 403 that names the token and both
  ways out. The explorer leaves out the surfaces that belong to a single person
  — Recent, Starred, Shared with me, API keys — while Upload, the storages,
  Trash, Tags and "How to connect" stay. Existing tokens migrate to `app`,
  because the restricting direction is the safe default and these surfaces only
  matter when a browser UI is drawn.

- **`/api/files/capabilities` could refuse a caller.** Gating it on the token
  chain meant a revoked token, an unknown token username or a disabled account's
  cookie got a 403 from a public route — the login screen failing closed. It now
  annotates the caller when it can and answers everyone.

- **Moving a plaintext file into an encrypted folder is refused.** Uploads were
  encrypted client-side; move, copy and paste were plain server-side byte
  operations, so filex's own UI would put plaintext inside an encrypted folder
  with no warning. The guard is server-side and covers the ops queue, sync moves
  and the AI/MCP surface.

- **The install banner ate clicks.** Both banners are full-width fixed strips
  that paint a centred card, and without `pointer-events-none` the empty half
  sat on top of the sidebar behind it: measured, five destinations unreachable
  at 1440×900 and eleven on a taller menu. `PendingOpsTray`, the same shape in
  the same corner, already did this correctly.

- **`slim` was not slim.** The tag was built from the full recipe, so
  `docs/DOCKER.md` promised ~40 MB while the registry served **511 MB**
  compressed — `latest`, `slim` and `full` were the same image, and the two
  Dockerfiles differed by one package that nothing calls. There is now a real
  slim image: **43 MB compressed**, the binary and the embedded UI, with image
  thumbnails (pure Go) still working and everything that shells out to ffmpeg,
  ghostscript or libreoffice reported as unavailable rather than failing.

- **`docs/E2E-ENCRYPTION.md` was 183 lines of Turkish** in an English repo,
  linked from the README and published on the docs site. Translated, and
  corrected against the code while translating: it under-counted the places that
  filter the marker, missed two disabled surfaces and the refusal to nest
  encrypted folders, and its "v2 roadmap" listed six things none of which had
  shipped — those are now stated as limitations rather than promises.

### Changed

- **Docker images build natively per architecture.** arm64 was emulated, and the
  emulated part was `apk add libreoffice` plus a JRE — the worst possible thing
  to run under QEMU. Each architecture now builds on its own runner and the tags
  are joined from the digests, so no tag exists until both have landed. The
  docker job also no longer waits for goreleaser: it builds its own binary from
  source and took nothing from the Release, so the dependency only lengthened
  the critical path.

- **The browser suite is a gate.** Cypress defaulted to `https://fm.example.com` —
  the live deployment — and ran in no pipeline. It now boots its own instance
  and runs in CI. Getting there meant fixing 19 red specs, several of which had
  been passing for the wrong reason: one matched the sidebar link instead of the
  dashboard it was meant to assert on, and two asserted a capability slot that
  only existed because production still carried a row from an older version.
  227 of 227 pass, and the run went from 9m58s to 1m25s once the service worker
  stopped re-precaching the bundle between tests.

### Added

- **Encrypted folders can be recovered.** Until now, forgetting the password to
  an E2E-encrypted folder destroyed it — the documentation said so, and it was
  true. Every folder created from this release on gets a **user recovery key**:
  160 bits, shown exactly once when the folder is created, never stored by
  filex, and enough to open the folder without the password. It is a password
  equivalent, so keep it somewhere other than the password.

  The file format did not change. A per-file key was already wrapped by one
  folder key; that key is now a **folder master key** held in the marker,
  wrapped once per way of reaching it. Adding a recovery path costs one more
  wrapped copy of 32 bytes, not a re-encrypt of anything — which is why not a
  byte of anyone's existing data was touched.

- **Optional key escrow for operators**, fixed at install time via
  `FILEX_INSTALLATION_E2E_ESCROW_KEY`. `filex e2e-escrow keygen` mints the pair;
  the server gets the **public** half only, so it can seal new folders to the
  escrow identity and open nothing. The private half is the operator's, and a
  stolen filex database still decrypts nothing. Using the escrow key notifies
  the folder's owner, and the notification is *evidence*: the client must
  decrypt a server-issued challenge sealed to the escrow key before the event
  is recorded, so it cannot be forged — and, stated plainly in the docs, cannot
  be relied on either, because an operator holding the private key can decrypt
  offline without ever asking filex.

- **Install-time settings, as a convention.** Anything prefixed
  `FILEX_INSTALLATION_` is recorded on the first boot and frozen; filex refuses
  to start if it later disagrees with the environment, and says what changed and
  what the operator can and cannot do about it. Escrow is the first of these,
  and the reason is arithmetic rather than policy: a folder created while escrow
  was off carries no escrow-wrapped key, so switching it on later cannot open
  it, and a server that started anyway would be claiming a capability it has
  over only half its data.

### Fixed

- **filex's own UI would put a plaintext file inside an encrypted folder.**
  Uploads are intercepted and encrypted in the browser, but paste,
  drag-and-drop and duplicate are server-side byte copies that never touch the
  crypto — so a file dragged into an encrypted folder was stored exactly as it
  arrived, looked like its encrypted neighbours in the listing, and nothing
  warned anyone. The reverse was as bad: a file moved *out* stayed encrypted
  somewhere no password prompt would ever appear.

  The server cannot fix either by encrypting or decrypting, because it has no
  key, so it refuses: a copy or move may not cross an encryption boundary
  (HTTP 409, naming the file). The rule lives in one place and every transfer
  surface calls it — the ops queue, the synchronous move and the AI/MCP move —
  rather than in the one client that happened to notice.

### Changed

- **Folders created before this release keep working, untouched.** Their format
  is read as a first-class path, not a migration shim, and the test suite proves
  it against a frozen copy of the v0.30.1 module rather than a re-creation of
  it. They cannot be given a recovery key by the server, because that needs the
  folder password and filex does not have it — so filex asks at the one moment
  it does: the next successful unlock. The offer is a visible strip with the
  consequences spelled out, including that accepting on an escrow-enabled
  installation also gives the operator a key. Declining changes nothing.

- The create-folder warning no longer says recovery is impossible, because for
  new folders it is not.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0310---2026-09-05)

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.31.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.31.0`

## v0.30.1

<span class="filex-release-date">4 September 2026</span>

A one-line patch. Opening Recent, Starred, Shared or Trash put the raw internal name of the view in the tab — `.shared` instead of Shared.

## What changed

### Fixed

- **The tab strip printed the raw sentinel for the new views** — a tab opened on
  Recent, Starred or Shared with me read `.recent`, `.starred`, `.shared`.
  Trash was right, and that is the whole story: the sentinel-to-label map was
  written twice, and when the three new views arrived only the breadcrumb copy
  was extended. A second copy of a mapping is a second chance to forget it, and
  the symptom hides itself — the old view keeps working, so it reads as "only
  the new one is missing something" rather than as a bug. There is one map now
  (`lib/listing.ts`), and every surface that renders a path segment reads it.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0301---2026-09-05)

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.30.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.30.1`

## v0.30.0

<span class="filex-release-date">4 September 2026</span>

The explorer grew a collapsible navigation panel: Recent, Starred, Shared with me, tags and Trash on the left, with Upload as the primary action. It is part of the shared component, so it reaches the npm packages and every embed, not just the admin app — and `uiProfile: 'simple'` turns the admin-facing parts off for an end user. Connecting a client and minting an API key moved into the explorer too; both surfaces existed but lived only in the admin app. On the search side, the index now repairs itself after an upgrade instead of waiting for someone to rebuild it by hand, filename ranking is a port of VS Code's Quick Open scorer, and multi-word content search was an OR — so adding a word made the result set larger rather than smaller.

## What changed

Everything here came out of two issues opened by the same person, and the most
useful thing in the release is the one we got wrong: v0.29.0 fixed filename
search and **nobody could see it**, because the index only gains new fields for
documents that are re-indexed and nothing ever rebuilt it. He measured our own
demo, correctly concluded that nothing had changed, and reported that content
search "doesn't work at all" — it was the same cause. A fix an existing install
cannot reach is not shipped.

### Added

- **A collapsible navigation panel in the explorer.** Upload as the primary
  action, then Recent, Starred, Shared with me and Trash, then the storages the
  caller can see, with the ones reached through a grant marked as shared. It
  collapses to a 56px icon rail rather than disappearing — a panel that vanishes
  takes its own way back with it — and below 560px it is a drawer instead of a
  column, so the listing keeps its width (measured: 388px with the drawer open
  and closed). The collapsed choice is remembered per viewer.

  It lives in `@brftech/filex-core`, so the web app, the desktop app and every
  embed get the same panel from one implementation. `sideNav` turns it on or
  off; the web component also takes a `sidenav` attribute for hosts that never
  touch JavaScript.

- **`uiProfile: 'standard' | 'simple'`.** The reporter's argument was that most
  of his users are not in IT and will not relearn a file manager: tabs, a split
  pane, four view modes and mount instructions are a power-user tool. `simple`
  turns those off and expands the panel; nothing is removed from the build, and
  an embedder can set either profile. The admin panel keeps today's defaults.

- **Connections and API keys from inside the explorer.** Both surfaces existed
  and neither was reachable: our own web app wired the buttons itself, so an
  embedder mounting `<filex-explorer>` gave their users no way to see how to
  mount a drive or to mint a token. `SelfTokensModal` has moved out of the web
  app into the shared component and the web copy is gone.

  Why it had never moved: the web version hid the write and delete scopes from
  viewer accounts by reading a store only the web app has. The shared component
  does not reproduce that. It offers every scope and lets the server refuse —
  which it already does, in words worth reading (`scope 'write' is not
  available here`). Asking is not granting, and a UI-side role check hides the
  surface from exactly the accounts that need it. For a year the only place to
  mint the token the FTPS guide names was the admin panel.

- **`GET /api/files/manager/shared-with-me`** — the nodes a caller holds a grant
  on. The data existed in `file_grants`; the only listing over it was
  path-scoped and owner-only, so "what has been shared with me" had no answer.
  Tenant-scoped explicitly, like search.

### Fixed

- **The search index now repairs itself after an upgrade.** On start it compares
  the document schema it was built with; if it is behind, it builds a
  replacement **alongside** the live index and swaps it in atomically. The old
  index answers every query until the swap, and extracted text is carried across
  document by document rather than re-derived — which is what makes this safe to
  do automatically. The blackout that argument was made against in v0.29.0 does
  not happen: measured on 20 202 documents, the rebuild took 2.96 s and 601
  searches issued during it returned 0 errors and 0 missing hits.

  An interrupted rebuild is discarded and retried; a crash during the swap
  restores the known-good index; it refuses and keeps serving the old index if
  the disk cannot hold both. `FILEX_SEARCH_AUTO_REBUILD=0` turns it off — but
  off by default would reproduce exactly the failure it exists to fix.

- **Filename ranking is now a port of VS Code's Quick Open scorer.** The
  reporter's words were "VS Code does this fine, I can't do it here", and he was
  pointing at a specific method, so we ported it: a subsequence match scored
  with position bonuses (start of name, after a path separator, after `_ - .`,
  camelCase humps, runs of consecutive characters), the query split into pieces
  matched independently so **word order does not matter**, and the filename
  scored separately from its folders and weighted above them. Bleve still
  retrieves the candidates; the scorer re-ranks them and, crucially, **drops
  candidates that do not answer every piece**.

  Measured against the corpus he tested on: `Code main` went from nine results
  to one. `main code` finds `Code/main.go` too. `Code/main.go` and
  `example/main.go` stopped being the same thing to the search. Exact filename
  still outranks prefix, which outranks everything fuzzy — now asserted by a
  test rather than emergent from merged relevance scores.

  Edit distance stays, ranked below the subsequence pass: `mian.go` finds
  `main.go`, which Quick Open itself would not.

- **Multi-word content search was an OR**, so adding a word *widened* it. That,
  not the filename side, was where most of the noise came from — seven of the
  nine results for `Code main` were files that merely contain the word "code".
  Every word is now required.

- **The typo pass almost never fired.** It was gated on the number of candidates
  the index returned rather than the number that survived filtering, so a query
  that retrieved plenty and kept none still counted as "enough".

- **The recently-opened tray read the wrong field** (`entries` from an endpoint
  that answers `nodes`), so it was empty on every server that ever served it.
  It was also unreachable — the toolbar declared the event that opens it and
  never emitted it. The panel is now the working surface.

- **Starred and Recent rows were unopenable in multi-storage mode**: the rows
  carried no storage name, so no `storage://path` could be built for them.

- **`docs/WEBDAV.md`, `docs/LDAP.md` and `docs/METRICS.md` sent people to
  "Settings → API tokens"** — a screen that has never existed in the product.

- **The web-component embedding example authenticated nothing.** It assigned
  `config` after the import that registers and mounts the element, so the first
  folder request went out without credentials: the explorer rendered and the
  listing said "could not load this folder".

- A query could arrive while the search index handle was being swapped, because
  the read lock was released before the query ran rather than after. One error
  in 269 hammered queries; now impossible by construction.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0300---2026-09-04)

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.30.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.30.0`

## v0.29.0

<span class="filex-release-date">4 September 2026</span>

Open an Office document that lives on your own computer in the editor your filex server already runs: the desktop app registers for the usual extensions, so a machine with no Word or Excel installed can still edit one. Non-admin accounts get a front door of their own at `/drive` rather than landing in the admin shell. Search stopped depending on which separator a filename happened to use, forgives one typo, and has a stated ranking order instead of an inherited one — and `tag:` and `-tag:` filters put a feature that had existed for a long time within reach of the search box.

## What changed

### Added

- **Open an Office document that lives on your own computer, in the editor your
  server runs.** Double-click a `.docx`, `.xlsx` or `.pptx` (ten Office types in
  all) and the desktop app opens it — on a machine with no Office installed.
  Most Linux desktops have none, many Macs have none, and plenty of Windows
  machines have none either; filex already had a perfectly good editor and the
  documents on those disks had no way into it.

  Two routes, picked per document. A document inside a folder you keep on this
  computer opens as **itself** — no copy, no write-back, saving goes to the
  server and sync brings it down again. A *paused* pair is deliberately not
  treated as one: the save would reach the server and never come back, and you
  would believe you had saved. Anything else is copied to a scratch area on the
  server, edited there, and written back over the original path on every save,
  with a strip along the bottom of the window naming the file the whole time.

  The write-back is where an editor destroys work, so: the replace is atomic
  (temp file in the same directory, then rename — never across devices, never a
  partial write over the document); a document deleted while it was open is not
  resurrected, its bytes are kept beside it as `<name>.filex-recovered-<time>`;
  a refused rename keeps the edit, says so in a dialog *and* a notification, and
  names where it kept it; the scratch copy survives a grace period after the
  window closes, because OnlyOffice posts its save roughly ten seconds after the
  editor disconnects and deleting on close would discard the last edit of every
  session; and a sweep at next start clears whatever a crash left behind.

  Registration is deliberately conservative. The installer adds filex to the
  **"Open with"** list and changes nothing that is already set: electron-builder's
  own `fileAssociations` macro writes the *default* ProgId for each extension —
  it takes the file type at install time, which on a machine with no Office is
  precisely how filex would become the handler without anyone being asked — so
  the Windows registration is hand-written instead, and macOS registers as
  `Alternate` rather than `Default`. Making filex the default is always an
  explicit action: `xdg-mime` does it on Linux from Settings, Windows opens the
  default-apps pane (`UserChoice` is hash-protected and cannot be set by an
  application, and filex does not pretend otherwise), and macOS explains the
  Finder route. See [docs/DESKTOP.md](./DESKTOP.md#opening-documents-from-your-computer).

- **An end-user front door: `…/drive`.** A non-admin account has always landed
  in the file manager rather than the admin panel — the router redirected them
  out of it and every `/api/admin/*` route re-checked the role — but everything
  around that screen said otherwise. The URL was `/admin/explore`, the browser
  tab said "filex Admin", and the login form said *"Use your filex admin
  credentials"*. Deploy filex for a team and each of your users was told three
  times, before seeing a single file, that this was an administrator's tool.

  The same app is now also served at `/drive`, with a per-route document title
  and login copy written for everyone. `/admin` is untouched and every existing
  bookmark still resolves. (Reported as #14.)

- **`tag:` and `-tag:` filters in search.** Tags have been a filex feature for
  a long time and were not in the search index at all, so `tag:invoice` searched
  for a *file named* "tag:invoice" and came back empty. They are now a filter
  rather than a search term, combinable with free text (`invoice 2026
  tag:paid`), resolved against the database so a re-tag is visible immediately,
  and applied before the result limit so `limit` counts filtered rows. A tag
  that does not exist returns nothing — never the whole storage.

- **The filex.sh landing page is in the repository**, at `site/`, deployed with
  `scripts/sync-site.sh`, and covered by the release documentation audit. It had
  lived only on the static host, so there was nothing to edit and nobody edited
  it: it still described roughly v0.14 — no desktop app, no sync, no protocol
  endpoints, no `filex mount`, no LDAP, no plugins, no search, no
  multi-tenancy — and claimed five storage drivers when there are six plus a
  plugin API.

### Fixed

- **Filename search depended on which separator the file's name used.**
  `invoice 2026` did not find `invoice_2026.pdf`, and `main go` did not find
  `main.go`, while `foo bar` found `foo-bar.txt` — an inconsistency with a
  single cause. Name search was a disjunction of an analysed match and an
  unanalysed `*term*` wildcard: the wildcard half cannot match anything once the
  query contains a space, leaving only the analysed half, whose tokeniser splits
  on `-` but joins on `_` and keeps `main.go` whole. So whether search worked
  was decided by the file's punctuation.

  Names are now indexed a second time in a normalised form (every run of
  non-alphanumerics collapsed to one space) and the query is normalised the same
  way, so `.`, `-`, `_` and a space are interchangeable. Multi-word queries
  require one `*word*` wildcard per word instead of one over the whole string.
  The normalisation is done in Go rather than as a Bleve field mapping on
  purpose: a mapping is frozen into the index when it is created, so a fresh
  install and an upgraded one would have analysed the same filename two
  different ways. (Reported as #15.)

- **One typo is now forgiven.** `mian.go` finds `main.go`. The fuzzy pass runs
  only when the strict pass came back short of the limit, and its hits rank
  below every exact and prefix match. Edit distance scales with word length
  (none at three characters or fewer, one up to seven, two above).

- **Ranking is now decided, not inherited.** The order is exact filename →
  prefix → name → path → fuzzy → content, asserted by a test. It had been
  whatever merging two queries' relevance scores produced: measured before this
  change, `report-final.txt` ranked *above* `report.txt` for the query `report`.
  Exact matching compares the name both with and without its extension.

- **The same query meant different things in different boxes.** `GET
  /api/ai/search` and the MCP `file_search` tool passed the raw string through,
  so `tag:source` was read there as a filename and `invoice 2026` found nothing
  — while the toolbar and `/api/files/search` understood both. All four now
  share one parser and one fallback plan.

- **The explorer's onboarding tour described a search that does not exist.** It
  said "This box filters the current folder"; the toolbar search covers the
  whole storage, and every storage when the multi-storage root is open. Its
  placeholder said "File name". Both were wrong before this release and are
  fixed in the shared component, so every surface gets the correction.

- **The explorer told a non-admin to configure a storage.** With no visible
  storage it showed "No storages configured yet" — for a `user` account that
  almost always means nothing has been *shared* with them, so it sent people to
  fix something they have no permission to fix.

- **An installed PWA ejected itself into a browser tab.** The manifest scope was
  `/admin/`, so the first navigation to the new `/drive` front door left the
  installed app. The manifest scope now covers both; the service worker's scope
  and the manifest `id` are deliberately unchanged (a changed `id` turns every
  existing install into a second app).

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0290---2026-09-04)

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.29.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.29.0`

## v0.28.0

<span class="filex-release-date">3 September 2026</span>

If you run filex against LDAP or Active Directory, this is the release where that actually works. The directory driver could be configured, initialised and printed in the boot banner while being unreachable from every login path: a directory account with the correct password was refused in under a millisecond — less than one LDAPS round trip, so the request never left the machine. Directory users can now sign in on the normal password form, and on WebDAV, SFTP, FTPS, S3 and NFS as well; their accounts hold no password hash in filex, which is why those protocols used to refuse them forever. Local login is still tried first, so your break-glass admin keeps working while the directory is down. Also here: a `ca_file` option for a private CA (no more rebuilding the container's trust store), a filter that fills every placeholder rather than the first one, and search failures that are finally distinguishable from a wrong password in the log.

## What changed

### Fixed

- **LDAP was configured, initialised, printed in the boot banner — and
  unreachable from every login path.** With `FILEX_AUTH_DRIVERS=local,ldap` a
  directory account was answered `401 invalid credentials` even with the right
  password, in roughly **350 microseconds**: less than one LDAPS round trip, so
  the request never reached the network at all. Nothing was logged, and the docs
  described the behaviour that was missing ("filex tries each enabled driver in
  order"), which made it read as a directory misconfiguration to everyone who
  hit it. Three separate gaps produced it, and each would have been enough on
  its own:

  - the bootstrap assigned the login handler's single `LoginDriver` in the
    `local` case only, so the directory driver was never the one it held;
  - `*ldap.Driver` did not satisfy `auth.LoginDriver` at all — no `Logout` — so
    it could not have been assigned even by hand. The compiler had never been
    asked the question;
  - `ldap.Login` ended with `return user, "", nil` under a comment saying the
    caller would mint the session. No caller did: a *correct* password would
    have handed back an empty cookie, a successful login presenting as a failed
    one.

  Password drivers are now chained (`auth.LoginChain`) in the order they appear
  in `auth.drivers` / `FILEX_AUTH_DRIVERS`. `local` first is deliberate — it is
  a hash compare against a row filex already holds, so `admin@local` and every
  break-glass password stay answerable while the directory is unreachable.

- **A directory account could sign in to the web UI and still be refused by
  WebDAV, SFTP, FTPS, S3 and NFS.** Those protocols check the password against
  `users.password_hash`, which is **empty by construction** for a directory
  account — filex never learns the password. The refusal was identical to a
  wrong one. They now ask the directory when the local table cannot judge
  (`auth.ldap.protocol_login`, on by default). The local hash is still tried
  first, TOTP accounts are still refused on every protocol, and a successful
  check is cached for five minutes exactly as a local one is — without that,
  each request of a WebDAV `PROPFIND` storm would be a fresh LDAPS bind.

- **A search failure and "no such user" were the same answer.** An unreachable
  directory, an expired service account and a typo in `base_dn` all came out as
  `unauthorized` with nothing in the log. Transport and protocol failures are
  now reported and logged apart from a rejected password.

- **`user_filter` silently broke with more than one placeholder.** It was filled
  with `fmt.Sprintf`, which consumes one argument per verb, so the standard
  Active Directory filter that accepts either address form became
  `(userPrincipalName=%!s(MISSING))` — a filter matching nobody. Every `%s` is
  now filled with the same escaped identifier.

- **Searches used a size limit of 1.** Active Directory answers a subtree search
  from the domain root with continuation references alongside the match, and a
  server counting those against a limit of 1 can answer "size limit exceeded"
  instead of the entry. The limit is 2, and a filter that genuinely matches two
  accounts is refused with a warning rather than resolved to whichever came
  first.

### Added

- **`auth.ldap.ca_file` / `FILEX_LDAP_CA_FILE`** — a PEM bundle for a private or
  internal CA, **appended** to the system trust store (the public roots keep
  working) and applied to `ldaps://` and StartTLS alike. Previously the only way
  to reach a directory behind an internal CA was to rebuild the container's
  `/etc/ssl/certs/ca-certificates.crt`. The file is read at boot, so a wrong
  path is a startup error rather than a login failure hours later.
- **`auth.ldap.protocol_login` / `FILEX_LDAP_PROTOCOL_LOGIN`** — set `false` to
  keep directory passwords on the login form only and require an API token on
  the file protocols.
- **The Helm chart can configure LDAP.** `auth.ldap.*` in `values.yaml` renders
  the `FILEX_LDAP_*` variables; the chart listed `ldap` as a valid driver while
  offering no way to configure it, so `drivers: "local,ldap"` produced a driver
  that failed `Init` and was skipped.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0280---2026-09-03)

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.28.0) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.28.0`

## v0.27.6

<span class="filex-release-date">1 September 2026</span>

## What changed

### Changed

- **Release tags are signed from here on, and there is finally a key to sign
  them with.** `CONTRIBUTING.md` had said `git tag -s` for months while no
  signing key existed on the release machine, so every tag through v0.27.5 is a
  plain annotated one — an instruction nobody can follow is not a policy, it is
  a lie the document tells. The step now also requires `git tag -v` to answer
  `Good signature` before the tag is pushed, and says where the key and its
  passphrase live. Nothing in the shipped software changes in this release.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0276---2026-09-01)

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.27.6) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.27.6`

## v0.27.5

<span class="filex-release-date">1 September 2026</span>

## What changed

### Fixed

- **A transient `503` from an object store sank the whole upload.** The retry
  budget was widened to six attempts a release ago, and a test proves a 503 is
  classified as retryable — but for an upload none of that could ever fire.
  The SDK rewinds a request body before retrying it, and every upload surface
  hands the S3 driver a plain stream (the handler sniffs the first bytes to
  detect the type and rejoins them), so there was nothing to rewind: the second
  attempt died before it was made and a brief upstream wobble became a
  permanent failure, reported as *"failed to rewind transport stream for retry,
  request stream is not seekable"* — a message about filex's plumbing rather
  than the outage behind it. The budget was real for listings and reads and a
  no-op for writes. An upload that declares a size of at most 8 MiB is now held
  in memory while it is sent, so a retry replays it byte for byte; a larger one
  streams through as before, because buffering every body would trade a rare
  failed upload for an out-of-memory kill. See
  [STORAGE.md](./STORAGE.md#s3--s3-compatible).

### Changed

- `pnpm run build:packages` / `build:web` / `build` now quote their workspace
  filters in a way `cmd.exe` also understands. They matched nothing on Windows
  — the single quotes reached pnpm literally — so `build:all`, a documented
  release step, could not run on a Windows workstation at all.
- `CONTRIBUTING.md` says what to do when Go lives in WSL and pnpm does not:
  `build:all` ends in a plain `go build`, and the screenshots boot that binary.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0275---2026-09-01)

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.27.5) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.27.5`

## v0.27.4

<span class="filex-release-date">29 August 2026</span>

## What changed

### Fixed

- **The Helm chart shipped a 23-release-old image.** `values.yaml` leaves the
  image tag empty, which the chart resolves to `.Chart.appVersion` — so
  appVersion is the version a Helm user actually runs, and it had been sitting
  at `v0.4.0` since it was written. It now tracks the release, moved by
  `scripts/sync-chart-version.mjs` as part of the release steps, and
  `web/tests/deploy/chartVersion.test.ts` fails the build if the two ever drift
  again — and the release workflow itself now refuses to publish a tag whose
  chart is behind, before a single artefact is built. **Every release updates
  the chart; it is a step of the process, not a chore to remember.**

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0274---2026-08-29)

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.27.4) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.27.4`

## v0.27.3

<span class="filex-release-date">29 August 2026</span>

## What changed

### Fixed

- **A folder dragged out of the desktop app arrived empty.** The watcher that
  finds where a stand-in landed was started *after* `webContents.startDrag()` —
  and on Windows that call hands control to the operating system's own drag
  loop, which does not return until the user lets go. The watcher therefore
  went up after the drop had already happened. Worse, a recursive `fs.watch`
  whose event loop is blocked does not deliver the change late; it misses it
  outright (measured: a file created during a 4-second block was never
  reported, before or after). Two changes: the watcher is armed **before** the
  drag, and it runs in a **worker thread**, whose loop keeps running while the
  main thread is inside the drag loop. Single files were never affected,
  because a small selection is prepared in the background and handed to the OS
  as a real file — which is why this only ever showed up on folders.
- **The suite could not have caught it.** Its simulated drop happened after the
  drag call returned, and its test hook skipped `startDrag` entirely rather than
  blocking like the real one. Both are fixed: the drop is now performed *while*
  the drag is in flight, and the hook blocks for the same reason the OS does.

### Added

- **The desktop app keeps a log** at `<userData>/logs/filex-desktop.log`
  (rotated at 2 MB, one previous file kept). A packaged app has no console, so
  `console.log` went nowhere: when a drag-out failed, the only evidence was an
  empty folder. Every step of a drag and of a transfer is one line, and an
  uncaught exception in the main process lands there too.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0273---2026-08-29)

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.27.3) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.27.3`

## v0.27.2

<span class="filex-release-date">29 August 2026</span>

## What changed

### Fixed

- **A file whose name is not ASCII no longer breaks a download — or the client
  reading it.** `Content-Disposition` carried the filename raw, so a name like
  `Türkçe adlı dosya.txt` put bytes over 127 in an HTTP header. Browsers guess
  their way through that, which is why it went unnoticed for years; a strict
  client does not. Electron's `net.fetch` threw
  `Cannot convert argument to a ByteString … value of 305` from inside its
  response handler — where no caller's try/catch can reach it — so the filex
  desktop app took an uncaught exception and a folder being dragged out stopped
  filling in halfway, silently. Every download now sends RFC 6266:
  `filename="ascii-fallback"` plus `filename*=UTF-8''percent-encoded`, from one
  shared helper used by the manager, share, share-browse and viewer endpoints.
- **The desktop app survives a badly-formed header from any server.** Its
  transfers moved from `net.fetch` (which validates response headers as
  ByteStrings) to `net.request` (which does not), so an older filex — or
  somebody else's server — can no longer stop a drag-out by naming a file in
  Turkish.
- **A failed drag-out no longer freezes the app.** The failure was reported with
  `dialog.showErrorBox`, which is modal: the box sat in front of a frozen window
  until it was clicked. It is a toast in the explorer now, plus an OS
  notification when the window is not in front. An uncaught exception in the
  main process is likewise logged and notified instead of ending in Electron's
  raw JavaScript error box.
- **Drag-outs leave a trail.** The transfer runs after the gesture is over, with
  no window of its own; when it went wrong the only symptom was a folder that
  stayed empty. Each step now prints one line (`[drag …]` / `[xfer …]`).

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0272---2026-08-29)

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.27.2) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.27.2`

## v0.27.1

<span class="filex-release-date">29 August 2026</span>

## What changed

### Fixed

- **A drag-out could fill in the wrong folder.** The drop watcher matched the
  stand-in by NAME across the local drives, so any file that happened to appear
  under the same name while a drag was in flight — a backup job, another
  download — looked like the drop and the user's file was written into that
  folder instead. What was handed to the shell is known exactly (an EMPTY
  stand-in, created inside this drag's window), so anything with content of its
  own is now ignored. Measured: with the guard off, a same-named decoy file
  received the transfer; with it on, the decoy is untouched and the real drop
  still lands.

[Full changelog entry](https://github.com/BRF-Tech/filex/blob/main/CHANGELOG.md#0271---2026-08-29)

[Downloads and checksums](https://github.com/BRF-Tech/filex/releases/tag/v0.27.1) · desktop packages included · `ghcr.io/brf-tech/filex:slim-v0.27.1`

## Earlier releases

The 88 releases before v0.27.1, in brief. Full notes are on GitHub.

| Version | Date | What changed |
|---|---|---|
| [v0.27.0](https://github.com/BRF-Tech/filex/releases/tag/v0.27.0) | 28 August 2026 | the next now does what it says: the queue carries a destination storage of its |
| [v0.26.1](https://github.com/BRF-Tech/filex/releases/tag/v0.26.1) | 27 August 2026 | endpoint answered bare codes — `{"error":"ticket_expired"}`, |
| [v0.26.0](https://github.com/BRF-Tech/filex/releases/tag/v0.26.0) | 27 August 2026 | agent-facing write surface carried its bytes inside the call — `/api/ai/upload`'s |
| [v0.25.3](https://github.com/BRF-Tech/filex/releases/tag/v0.25.3) | 25 August 2026 | `/d/{token}` drop link came out with an empty `<title>`, an empty heading, an |
| [v0.25.2](https://github.com/BRF-Tech/filex/releases/tag/v0.25.2) | 23 August 2026 | A patch for whoever reads the error tracker. Events forwarded from filex's log used to arrive as a bare message — "thumb generate failed" eleven times with no file and no error to act on. |
| [v0.25.1](https://github.com/BRF-Tech/filex/releases/tag/v0.25.1) | 23 August 2026 | A same-day fix for FTPS: the public host name is looked up when the listener starts, and a single DNS timeout in the container's first seconds used to leave FTPS off for good while everything else reported healthy. |
| [v0.25.0](https://github.com/BRF-Tech/filex/releases/tag/v0.25.0) | 23 August 2026 | Share links now have a ceiling on how long they live, and the admin holds it: a new Protection setting (default seven days) caps every new link and file request — a link created without an expiry gets one, a longer request is… |
| [v0.24.1](https://github.com/BRF-Tech/filex/releases/tag/v0.24.1) | 22 August 2026 | A follow-up to 0.24.0's resumable first run: it now covers uploads too. The engine writes its sync history while it works — every 50 transfers or 15 seconds — instead of only at the very end, so a run interrupted at file 9,000 of… |
| [v0.24.0](https://github.com/BRF-Tech/filex/releases/tag/v0.24.0) | 22 August 2026 | A night of syncing a real 10,000-file tree, and what it taught the engine. Transfers and server listings now run several at a time, so a tree of small files is no longer priced at one round-trip each — the tree that crawled at… |
| [v0.23.0](https://github.com/BRF-Tech/filex/releases/tag/v0.23.0) | 20 August 2026 | The rest of “keep on this computer”, the same day it shipped. Every row now says where it lives — ✓ on this computer, ◐ holding kept items below, ⟳ syncing right now, ☁ online-only — and a strip along the bottom of the window… |
| [v0.22.0](https://github.com/BRF-Tech/filex/releases/tag/v0.22.0) | 20 August 2026 | Folders you also want on the computer are now one right-click away. “Keep on this computer” mirrors a server folder — or a whole storage — under a single filex folder chosen once per account, while everything else stays… |
| [v0.21.6](https://github.com/BRF-Tech/filex/releases/tag/v0.21.6) | 19 August 2026 | Two decisions about noise and blast radius. A demo instance no longer accepts any new storage backend — 0.21.4 stopped it reaching the server's own filesystem, and now the remote drivers go too, because "attach your own bucket"… |
| [v0.21.5](https://github.com/BRF-Tech/filex/releases/tag/v0.21.5) | 19 August 2026 | Plugins no longer outlive filex on Windows. If filex was stopped without a chance to clean up — a crash, a hard kill, a service restart — every plugin it had launched kept running; and since a running plugin holds its own .exe… |
| [v0.21.4](https://github.com/BRF-Tech/filex/releases/tag/v0.21.4) | 19 August 2026 | The other half of the demo hardening in 0.21.3. A demo instance publishes an admin login, and the `local` storage driver means "a path on this host" — so on a demo it was possible to add a storage rooted at /data, /etc or /proc/1… |
| [v0.21.3](https://github.com/BRF-Tech/filex/releases/tag/v0.21.3) | 19 August 2026 | A security default, found by measuring the project's own public demo. A demo instance publishes an admin login — that is what a demo is for — and the plugin API is admin-only, so on a demo "admin-only" means anybody, and… |
| [v0.21.2](https://github.com/BRF-Tech/filex/releases/tag/v0.21.2) | 19 August 2026 | A plugin now has to PROVE what it claims before filex will use it. Every capability a plugin declares is probed — at install against the plugin's own throwaway area, and again when you save a storage on it — and one that fails… |
| [v0.21.1](https://github.com/BRF-Tech/filex/releases/tag/v0.21.1) | 19 August 2026 | Pasting a file into the top level of a storage failed — on every driver, not just the new plugins: the explorer asks for the storage root, and the operations queue read that as "no destination given". |
| [v0.21.0](https://github.com/BRF-Tech/filex/releases/tag/v0.21.0) | 19 August 2026 | filex can now be taught a storage it has never heard of. A plugin is a separate program — in any language — that filex launches (or connects to) and speaks a small HTTP/JSON protocol to; once it is running, its driver appears in… |
| [v0.20.3](https://github.com/BRF-Tech/filex/releases/tag/v0.20.3) | 18 August 2026 | The desktop app now ships for macOS (Apple Silicon) — unsigned but ad-hoc sealed, so first launch is the ordinary Open Anyway step rather than a refusal; auto-update on the Mac waits for a signed build. |
| [v0.20.2](https://github.com/BRF-Tech/filex/releases/tag/v0.20.2) | 17 August 2026 | Packaging only — the server is identical to 0.20.1. It exists because the |
| [v0.20.1](https://github.com/BRF-Tech/filex/releases/tag/v0.20.1) | 17 August 2026 | setting is documented as "the address to advertise for passive connections", |
| [v0.20.0](https://github.com/BRF-Tech/filex/releases/tag/v0.20.0) | 17 August 2026 | filex is now reachable without a browser. It could already use S3, SFTP, FTP and WebDAV as storages; now those clients can point at filex itself — as an S3 endpoint, an SFTP server, an FTPS server and an NFSv3 export, alongside… |
| [v0.19.0](https://github.com/BRF-Tech/filex/releases/tag/v0.19.0) | 14 August 2026 | The desktop app gets a language setting — System, English or Türkçe — where before it followed the operating system and offered nothing to choose. |
| [v0.18.2](https://github.com/BRF-Tech/filex/releases/tag/v0.18.2) | 14 August 2026 | The Windows app installs per-user now, and that is what makes the quiet update in 0.18.1 real: an app under C:Program Files needs administrator rights to replace its own files, so every background update ended in a UAC prompt —… |
| [v0.18.1](https://github.com/BRF-Tech/filex/releases/tag/v0.18.1) | 14 August 2026 | The desktop app updates itself the way it always should have. Downloading was quiet and quitting installed silently, but the tray entry and the Settings button ran the installer with its wizard — so the one visible path through… |
| [v0.18.0](https://github.com/BRF-Tech/filex/releases/tag/v0.18.0) | 14 August 2026 | Share links keep their word, and you get a face. A link capped at three downloads could hand out four: the cap was checked against a counter that was only bumped after the bytes had left, so any request that started while an… |
| [v0.17.1](https://github.com/BRF-Tech/filex/releases/tag/v0.17.1) | 12 August 2026 | A packaging fix, and the first release whose tag matches what ships. The desktop package could be built without the updater inside it: in a pnpm workspace the dependency is a symlink pointing outside the app directory and… |
| [v0.17.0](https://github.com/BRF-Tech/filex/releases/tag/v0.17.0) | 12 August 2026 | The desktop app keeps itself up to date. It checks a static feed on filex.sh (not GitHub — that mirror is private and the provider would need a token inside the app), downloads quietly, and installs when you quit, because an… |
| [v0.16.3](https://github.com/BRF-Tech/filex/releases/tag/v0.16.3) | 12 August 2026 | The tab strip, fixed properly. It was permanent in the desktop app and came and went on the web, because 0.16.0 gave the two surfaces different defaults — this package exists so they are one product, so the default is now the… |
| [v0.16.2](https://github.com/BRF-Tech/filex/releases/tag/v0.16.2) | 12 August 2026 | Two things a share link got wrong. It changed language when you entered its PIN — the gate was English, the page behind it Turkish, and the screen in between managed both at once. |
| [v0.16.1](https://github.com/BRF-Tech/filex/releases/tag/v0.16.1) | 12 August 2026 | Follow-up to 0.16.0: a shared folder's gallery tiles are now rendered when the link is created rather than when the first visitor arrives, so the first open is fast too — which is the open that matters, since whoever creates a… |
| [v0.16.0](https://github.com/BRF-Tech/filex/releases/tag/v0.16.0) | 12 August 2026 | Two things a shared folder was doing the slow way. Its gallery tiles were the original photos — the page asked for a thumbnail and the server streamed the whole file — so a folder of a few dozen photos shipped tens of megabytes… |
| [v0.15.1](https://github.com/BRF-Tech/filex/releases/tag/v0.15.1) | 12 August 2026 | The desktop app's account rail had its two identities the wrong way round. Each row of that rail is a server — a tenant — so it now carries that server's own Branding logo, which is a better label than initials taken from an… |
| [v0.15.0](https://github.com/BRF-Tech/filex/releases/tag/v0.15.0) | 12 August 2026 | Mostly the desktop app, and one thing that was writing to your operating system. "Start when I sign in" registered whichever executable happened to be running the app — which, for anyone who had ever run it from source, was a… |
| [v0.14.0](https://github.com/BRF-Tech/filex/releases/tag/v0.14.0) | 10 August 2026 | The desktop app can reach its own server again. Opening any office document showed "Config fetch 401", starred files and recently-opened were silently empty, and "Open in new tab" did nothing at all — one cause under the first… |
| [v0.13.4](https://github.com/BRF-Tech/filex/releases/tag/v0.13.4) | 10 August 2026 | A fix for a disk that fills up on its own. Uploads larger than 32 MiB are buffered to a temporary file, and those files were never removed — every request answered normally while the disk quietly drained (29 GB in two hours on a… |
| [v0.13.3](https://github.com/BRF-Tech/filex/releases/tag/v0.13.3) | 7 August 2026 | The share button now appears in the desktop app. The explorer has always had one, but it was gated on the Web Share API, which Electron does not ship — so rather than build a second share UI, the app puts a native handler behind… |
| [v0.13.2](https://github.com/BRF-Tech/filex/releases/tag/v0.13.2) | 7 August 2026 | Sixteen components were rendering as raw, unstyled HTML in every embedded surface — the share dialog, the convert dialog, the presence bar and nine file viewers. |
| [v0.13.1](https://github.com/BRF-Tech/filex/releases/tag/v0.13.1) | 7 August 2026 | The explorer's onboarding tour sat on top of the desktop app's Settings panel. The tour attaches to `<body>`, so hiding the explorer left it exactly where it was. |
| [v0.13.0](https://github.com/BRF-Tech/filex/releases/tag/v0.13.0) | 7 August 2026 | The desktop app is a file manager, not an admin console. Signing in used to land you on the server's dashboard because the shell embedded the whole admin SPA; it now shows the file explorer, an account rail down the left, and a… |
| [v0.12.0](https://github.com/BRF-Tech/filex/releases/tag/v0.12.0) | 7 August 2026 | Selective folder sync. A folder on your computer and a folder on a filex server are kept in step in both directions, in the background. |
| [v0.11.0](https://github.com/BRF-Tech/filex/releases/tag/v0.11.0) | 7 August 2026 | The desktop app, for Windows and Linux. It runs the same web UI this repo already ships, and sign-in happens in your browser — the app opens the server's own login page and waits, so installs behind an identity provider (OIDC,… |
| [v0.10.2](https://github.com/BRF-Tech/filex/releases/tag/v0.10.2) | 6 August 2026 | Completes the guard added in 0.10.1: a sweep found four more places that could still write a file onto a folder — archive extraction and creation, the OnlyOffice save-back, and version restore — plus replication, where a… |
| [v0.10.0](https://github.com/BRF-Tech/filex/releases/tag/v0.10.0) | 6 August 2026 | Two explorer changes reported by a deployment whose users mount WebDAV from macOS. Dot-prefixed files can now be shown or hidden and are hidden by default (a Mac leaves `.DS_Store` and `._name` litter in every folder it opens),… |
| [v0.9.0](https://github.com/BRF-Tech/filex/releases/tag/v0.9.0) | 6 August 2026 | Closes the ten items a multi-tenant deployment filed against v0.8.0. The important one is a security fix: a tenant admin could reach every other tenant's storages over WebDAV, because `/dav` does its own Basic authentication and… |
| [v0.8.0](https://github.com/BRF-Tech/filex/releases/tag/v0.8.0) | 29 July 2026 | filex now knows which releases exist, and can install them. What it does is decided by which part of the version moved: a patch applies itself when the policy allows, a minor is announced and applied with one click, a major is… |
| [v0.7.6](https://github.com/BRF-Tech/filex/releases/tag/v0.7.6) | 29 July 2026 | Denials on the AI/MCP surface answer `403` instead of `500`. A `5xx` reads as "server glitch, retry", so agents and HTTP clients were retrying requests that could never succeed while the real cause — a path outside the token's… |
| [v0.7.5](https://github.com/BRF-Tech/filex/releases/tag/v0.7.5) | 19 July 2026 | An internal refactor with no behaviour change: the storage-scoped path hash had been copy-pasted across nine call sites, so the same file could map to different rows if any copy drifted. |
| [v0.7.4](https://github.com/BRF-Tech/filex/releases/tag/v0.7.4) | 18 July 2026 | Two explorer fixes: the trash bin now appears in the secondary pane of split view (the two panes were offset by a row), and tall listings scroll inside their pane instead of scrolling the whole page. |
| [v0.7.3](https://github.com/BRF-Tech/filex/releases/tag/v0.7.3) | 18 July 2026 | Split view's right-click menu now matches the main panel exactly — it was a shorter, separate list missing rename, delete, share, convert and tags. |
| [v0.7.2](https://github.com/BRF-Tech/filex/releases/tag/v0.7.2) | 18 July 2026 | Split-view polish: the main panel's breadcrumb no longer spans the full width, and right-clicking a row in the secondary pane opens a real menu instead of only selecting the row. |
| [v0.7.1](https://github.com/BRF-Tech/filex/releases/tag/v0.7.1) | 18 July 2026 | A round of layout and accessibility fixes for embedders. The explorer no longer overflows its host by 2px (the outer scrollbar that produced in embeds is gone), the toolbar folds overflowing actions into a `⋯` menu instead of… |
| [v0.7.0](https://github.com/BRF-Tech/filex/releases/tag/v0.7.0) | 17 July 2026 | Three additions. **Branding** — a settings-driven identity (name, logo, accent, footer) for the public share, PIN, file-drop and folder-browse pages plus the admin login, with per-tenant overrides. |
| [v0.6.0](https://github.com/BRF-Tech/filex/releases/tag/v0.6.0) | 17 July 2026 | Tabs and split view. Open several locations as tabs, split the active tab into two panes that navigate independently, and drag files between them to move (same storage) or copy (across storages). |
| [v0.5.0](https://github.com/BRF-Tech/filex/releases/tag/v0.5.0) | 17 July 2026 | A large interface release: eight built-in themes with independent light and dark variants, fully rebindable keyboard shortcuts, Quick Look (peek the selected file with Space), an operations centre that collects uploads and… |
| [v0.4.2](https://github.com/BRF-Tech/filex/releases/tag/v0.4.2) | 17 July 2026 | Cleanup release. Moving a folder to trash no longer wedges storage sync (a trashed folder's leftovers could block sync from ever re-creating those names), `versions.keep_n` above 20 works instead of being silently capped, and… |
| [v0.4.1](https://github.com/BRF-Tech/filex/releases/tag/v0.4.1) | 17 July 2026 | Packaging and documentation. Ready-to-submit app-store manifests for Umbrel, CasaOS, Runtipi, Unraid and Portainer, a refreshed Helm chart, and this documentation site. |
| [v0.4.0](https://github.com/BRF-Tech/filex/releases/tag/v0.4.0) | 17 July 2026 | An inspector panel (press `i`) with metadata, version history, effective permissions and share links for the selected item. |
| [v0.3.0](https://github.com/BRF-Tech/filex/releases/tag/v0.3.0) | 17 July 2026 | Connectivity release. A WebDAV server so you can mount filex as a network drive from Windows, Finder, rclone or davfs2 with full RBAC enforcement; a `filex client` CLI against any remote filex; and multiple webhook targets with… |
| [v0.2.0](https://github.com/BRF-Tech/filex/releases/tag/v0.2.0) | 17 July 2026 | Content search. filex now indexes what is inside your files — text, source code, CSV/JSON/YAML, PDF text layers and Office documents — extracted asynchronously into an embedded index, with highlighted snippets in the results. |
| [v0.1.84](https://github.com/BRF-Tech/filex/releases/tag/v0.1.84) | 17 July 2026 | A design pass over the explorer: a command palette (`Ctrl/Cmd+K`), a shortcuts sheet sourced from one registry so it cannot drift, sortable and date-grouped list columns, a density toggle, an undo snackbar for rename/move/trash,… |
| [v0.1.83](https://github.com/BRF-Tech/filex/releases/tag/v0.1.83) | 16 July 2026 | screens, mobile touch): three compounding layout issues fixed. The |
| [v0.1.82](https://github.com/BRF-Tech/filex/releases/tag/v0.1.82) | 10 July 2026 | read-only and no-preview labels) and the presence-bar toggle tooltip now |
| [v0.1.81](https://github.com/BRF-Tech/filex/releases/tag/v0.1.81) | 9 July 2026 | list of usernames (first = default); the audit log, shares (`created_via`) |
| [v0.1.80](https://github.com/BRF-Tech/filex/releases/tag/v0.1.80) | 9 July 2026 | as API calls (bearer/proxy) and rendered from blob object-URLs, so embedded |
| [v0.1.79](https://github.com/BRF-Tech/filex/releases/tag/v0.1.79) | 9 July 2026 | stamp `X-Filex-Presence-Name` (RFC 2047) + `X-Filex-Presence-Key`, spoofing |
| [v0.1.78](https://github.com/BRF-Tech/filex/releases/tag/v0.1.78) | 8 July 2026 | Ws — Resolve embedded confined subscribes to the absolute room + per-client frame paths. |
| [v0.1.77](https://github.com/BRF-Tech/filex/releases/tag/v0.1.77) | 8 July 2026 | Ws — Authorize ticketed subscribes as the ticket's user (RBAC) |
| [v0.1.76](https://github.com/BRF-Tech/filex/releases/tag/v0.1.76) | 8 July 2026 | Ws — Allow ticket-only (embedded/cross-origin) WebSocket connections. |
| [v0.1.75](https://github.com/BRF-Tech/filex/releases/tag/v0.1.75) | 8 July 2026 | Realtime — Embed WS live-collab in the core (ticket auth + polling fallback) |
| [v0.1.74](https://github.com/BRF-Tech/filex/releases/tag/v0.1.74) | 8 July 2026 | updates in the core component (native UI *and* embedded contexts via |
| [v0.1.73](https://github.com/BRF-Tech/filex/releases/tag/v0.1.73) | 7 July 2026 | Share — Add GET /api/files/share list endpoint so the modal lists existing links. |
| [v0.1.72](https://github.com/BRF-Tech/filex/releases/tag/v0.1.72) | 7 July 2026 | Share — Native "Paylaş" (Web Share API) button under the mail row. |
| [v0.1.71](https://github.com/BRF-Tech/filex/releases/tag/v0.1.71) | 7 July 2026 | Explorer — 'folder not found' for dead deep links — phantom prefixes 404, denied dirs render as not-found. |
| [v0.1.70](https://github.com/BRF-Tech/filex/releases/tag/v0.1.70) | 7 July 2026 | Web — Don't double the explorer hash in the login redirect. |
| [v0.1.69](https://github.com/BRF-Tech/filex/releases/tag/v0.1.69) | 6 July 2026 | pasting a link opens that folder, login preserves the hash. |
| [v0.1.68](https://github.com/BRF-Tech/filex/releases/tag/v0.1.68) | 6 July 2026 | redirects.** Measured live (nginx `$upstream_http_set_cookie` vs the |
| [v0.1.67](https://github.com/BRF-Tech/filex/releases/tag/v0.1.67) | 5 July 2026 | Previously the session cookie never set `Secure`, and the OIDC state cookie |
| [v0.1.66](https://github.com/BRF-Tech/filex/releases/tag/v0.1.66) | 5 July 2026 | successful (or failed) IdP round-trip the callback bounced the user to |
| [v0.1.65](https://github.com/BRF-Tech/filex/releases/tag/v0.1.65) | 5 July 2026 | were already cross-built for arm64 (goreleaser), but the container images |
| [v0.1.64](https://github.com/BRF-Tech/filex/releases/tag/v0.1.64) | 5 July 2026 | `adminIDFiltersIn` type and two files had drifted from gofmt, which kept |
| [v0.1.63](https://github.com/BRF-Tech/filex/releases/tag/v0.1.63) | 5 July 2026 | flag on and `oidc` among the auth drivers, the login page starts the OIDC |
| [v0.1.62](https://github.com/BRF-Tech/filex/releases/tag/v0.1.62) | 5 July 2026 | A real zero renders as `0 B` instead of `—`, and rows without a backend |
| [v0.1.61](https://github.com/BRF-Tech/filex/releases/tag/v0.1.61) | 5 July 2026 | tenants: a *provider* = an auth realm (OIDC or local) bound to a host and |
| [v0.1.60](https://github.com/BRF-Tech/filex/releases/tag/v0.1.60) | 5 July 2026 | "last activity" date added in 0.1.59 is derived from descendant file mtimes; |
| [v0.1.59](https://github.com/BRF-Tech/filex/releases/tag/v0.1.59) | 5 July 2026 | added in 0.1.58, each folder's row reports a "last activity" date — the |
| [v0.1.58](https://github.com/BRF-Tech/filex/releases/tag/v0.1.58) | 4 July 2026 | size (the sum of its descendant files). Sizes are computed once at the end of |
| [v0.1.57](https://github.com/BRF-Tech/filex/releases/tag/v0.1.57) | 4 July 2026 | Storage — Connect any external storage from env (sftp/webdav/ftp/s3) + fix JSON port. |

---

<small>Last refreshed 2026-09-11 from 108 published releases.</small>
