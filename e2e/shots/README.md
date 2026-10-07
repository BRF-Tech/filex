# Screenshots: taken when they change, published on filex.sh

Every picture the README, the docs and filex.sh show comes from the scripts in
this directory, in English, against the build in your working tree. Since
task #176 (2026-10-06) the pictures are **not kept in the repository**: they
are published on filex.sh under names that carry their content hash, and
[`manifest.json`](manifest.json) names the current file of each one.

```bash
pnpm shots                                   # take what changed, compare, stage
node scripts/shots-site.mjs upload           # publish the staged files (maintainers)
node scripts/shots-site.mjs accept --looked  # after LOOKING: manifest + every page relinked
git add e2e/shots/manifest.json README*.md docs site deploy   # what accept rewrote
```

## Why it works this way

Until 0.52.0 each release took all of its pictures again into a new
`docs/screenshots/vX.Y.Z/` folder and committed it: about 40 MB a release,
338 MB by 0.52.0, and a release-day review of 150+ pictures of which some 25
had changed. Two things replace that:

- **A scene is taken only when what it shows can have changed.** Each script
  has a digest of what its pictures depend on; a scene whose digest is the
  one recorded with the published set is skipped, and when every scene is
  skipped nothing is even built.
- **A picture counts as changed only when its pixels moved.** Every picture a
  run takes is compared with the published one; under the threshold it is the
  same picture and the published file stands. The contact sheet shows only the
  changed, new and removed pictures, each beside the published one with a map
  of where it moved.

Looking is still a person's job, and still a release step: no picture is
published, and no page links one, that nobody looked at
([CONTRIBUTING.md, Release process step 2](../../docs/CONTRIBUTING.md#release-process)).

## What a run does

`pnpm shots` (`scripts/shots.mjs`):

1. **Plans.** For every script it computes the scene's digest
   (`scripts/lib/shots-site.mjs`, `sceneDigest`) from:
   - the script and every module it imports by a relative path (not
     `release.mjs`, whose one variable is the release number);
   - the fixtures the scenes upload (`e2e/fixtures`) and the board app
     (`board-app/`);
   - the whole product (`backend`, `web/src`, `web/public`, every
     `packages/*/src`, the lockfile), tests and prose left out - or, for a
     script that declares `export const INPUTS = [...]`, those paths plus the
     shared shell (styles, the English catalogues, `App.vue`);
   - tokens: the browser locale the scenes pin (`en-US`), the operating
     system (fonts are the system's), the release number (`SHOTS_RELEASE`; a
     script with INPUTS carries it only when it lists `'@version'`), an app
     build's hash and the document server a scene is pointed at.

   A scene is **published** (skipped) when its digest is the one in
   `manifest.json`, **kept** (skipped) when the last run took it with this
   digest and its pictures are still in the capture folder, **left out** when
   it needs an app build or a document server the run has none of, and
   **taken** otherwise.
2. **Builds and verifies** (only when something is taken): the packages, the
   web UI, the embed, the binary, and the proof that the binary serves the UI
   that was just built, byte for byte.
3. **Shoots** the scenes to take. Every script writes into the capture folder,
   `e2e/.artifacts/shots/capture/<set>/` (git-ignored; `release.mjs` names it).
4. **Compares** every picture it stands behind with the published file
   (`scripts/lib/png.mjs`, downloaded once into `e2e/.artifacts/shots-cache/`):
   - **same**: the published bytes, or at most 32 pixels that moved by more
     than 24/255 in a channel (`SHOTS_DIFF_PIXELS`, `SHOTS_DIFF_TOLERANCE`);
   - **changed**: past the threshold, or nothing published to compare with;
   - **new**: the manifest has no picture of that name;
   - **removed**: published, and the scene that took it ran and did not.
5. **Stages** the changed and new pictures under their published names in
   `e2e/.artifacts/shots/publish/`, and writes `review.json` and the contact
   sheet, `e2e/.artifacts/shots/contact-sheet.html`.

```bash
pnpm shots                        # the scenes whose digest moved
pnpm shots --all                  # every scene: the nightly run, a major release, a set from another OS
pnpm shots --only roles,groups    # these; the rest stand as published or as last taken
pnpm shots --no-build             # shoot bin/filex as it is (still verified)
pnpm shots --without-apps         # leave out the scenes that need app builds (CI does)
```

A run that failed half way is not wasted: the scenes it took before the
failure are **kept** by the next run, so after a fix `pnpm shots` takes the
failed scene and the ones after it.

## Publishing

`node scripts/shots-site.mjs`:

| Command | What it does |
|---|---|
| `upload [--dry-run]` | Sends the staged files to `https://filex.sh/shots/` and reads every one back over HTTPS. Add-only: a name that is already there holds these bytes, nothing is overwritten or deleted. The transfer itself is `scripts/shots-upload.sh`, which lives in the maintainers' checkout only. |
| `accept --looked` | The reviewed run becomes the published set: refuses a run not taken on Linux, and unless every new and changed picture answers with exactly its bytes, then writes `manifest.json` and points every page at the new URLs. `--looked` is your word that you opened the contact sheet. |
| `verify [--live]` | Every page links the manifest's current file, no repository path and no older hash; `--live` reads every linked URL back. The release audit runs it with `--live`. |
| `relink [--write]` | Points every link at the current file; `--write` refuses while a target is not published (`--offline` skips that read). |
| `adopt <dir> [--rev <rev>] [--rewrite] [--platform <os>]` | Stages a folder of pictures as published: how the 0.52.0 set left the repository. `--platform` records the system the folder was taken on. |

The pages that are relinked: `README.md` and its translations, `docs/**/*.md`
(not `docs/handovers`), `site/index.html` (only its screenshots, never the
page's own files), the app-store manifests under `deploy/`, and the READMEs of
the packages, the desktop app and e2e.

### Why the hash is in the name

filex.sh is behind Cloudflare, and the edge keeps a `.png` it has seen. A
picture that changed under the same name would be served stale until somebody
purged it. A new name is a new URL: nothing to purge, and the old URL keeps
showing what it always showed, which is also what an old README (a tag, a fork)
links. The upload purges the new URLs once anyway, in case a page opened before
the upload left a cached "not found" for them.

### Order

**Publish first, link second.** The public repository's README is exported from
this tree, so a link to a picture nobody uploaded is a broken image in the
shop window. `accept` and `relink --write` read the new URLs back before they
write a page, and the release audit (`verify --live`) does it again before any
export.

### One typeface: taken in the build host's test chain

Fonts come from the operating system - and on Linux from its fontconfig - so
a picture taken in one place beside pictures taken in another is a README in
two typefaces. **The published set is taken where the nightly run takes it:
on Linux, in the build host's test chain** (decided 2026-10-06) - the
Playwright image `scripts/chain/run.mjs` names, with its `FONTS_CONF`, which
sets sans-serif and system-ui in Liberation Sans. The build host itself sets
the same pages in DejaVu Sans; both are Linux. `PUBLISH_PLATFORM` and
`PUBLISH_ENVIRONMENT` in `scripts/lib/shots-site.mjs` say it, and the manifest
records it (`"platform": "linux"`, `"environment": "chain"`):

- a run on Windows or macOS still takes, compares and shows its pictures - to
  look at while you work on a screen - but stages nothing for the site, and
  `accept` refuses it, partial or whole;
- a run on Linux outside the chain (`SHOTS_ENVIRONMENT` is not `chain`) is
  accepted with a warning: the scenes the chain cannot take yet - those that
  need an app build, a Document Server or the converter's engines - are taken
  on the build host itself, and their pictures read in its typeface. ⚠ Its
  environment then goes into the manifest (`"environment": "local"`), and
  `web/tests/deploy/shotsSite.test.ts` holds the manifest to `chain`: take
  those scenes in the chain's Playwright image with its fontconfig
  (`scripts/chain/run.mjs` `FONTS_CONF`) and `SHOTS_ENVIRONMENT=chain`, in one
  run with the rest;
- a manifest whose set came from another platform is replaced only whole
  (`--all`, every scene taken), never a scene at a time;
- `adopt <dir> --platform <os>` records where an adopted folder was taken.

Every night the chain's shots job (`scripts/chain/job/shots.sh`, `pnpm shots
--all`) takes every scene it can and keeps the pictures, the review and the
contact sheet in the run's `out/shots/`, so the changes since the published
set are known the morning after they land. A release takes them the same
way, into its own checkout:

```bash
CHAIN_EXTRAS=shots bash scripts/chain/run.sh --profile targeted --src <the release checkout>
```

then `upload` and `accept` from that checkout. ⚠ The 0.52.0 set, adopted when
the pictures left the repository, was taken on the build host itself (DejaVu
Sans): the first night's `--all` shows every picture of it changed, and
accepting that run moves the set to the chain - once.

### One clock: Tuesday, September 15, 2026, 10:30 UTC

A picture that shows a date would otherwise change every night - each
upload's time, the day in a date picker, a notification's "2 minutes ago" -
and the pixel comparison would put it in front of a person every time.
[`clock.mjs`](clock.mjs) gives every scene one clock (task #176):

- **the browser** starts at `SCENE_NOW` (2026-09-15 10:30 UTC, Playwright's
  `context.clock.setSystemTime`) in the `UTC` time zone, and runs from there.
  Not a frozen clock: the explorer measures the time between two clicks (a
  click right after an open, or after a drag, is swallowed), and on a clock
  that never moves every click after the first open would be;
- **the server's times** - an upload, a notification, a share's expiry, a
  licence - are written by the real clock, so every API answer the page reads
  is moved into scene time on the way (`context.route`), and every time the
  page sends back (an expiry it chose, a date filter) is moved back. Signed and
  session answers (`/api/auth/`, the ONLYOFFICE config, E2E) pass untouched,
  and so does a host only the browser resolves (`realm.mjs`'s tenant host);
- **what a person reads** is snapped to the hour around `SCENE_NOW`: a date
  prints as `Sep 15, 2026, 10:30 AM`, and "x seconds/minutes ago" under half
  an hour as "now" - whatever a scene creates lands minutes from its start,
  and the minutes differ between runs;
- **the fixtures' files** carry fixed dates before `SCENE_NOW`
  (`fixtureTime`, by path): `pinTimes(root)` after the last write into a
  storage folder and before its sync - `seedFixtures` and `writeOfficeFile`
  do it for their own files.

Every browser context a script makes is created with `...SCENE_CONTEXT` and
put on the clock with `stageClock(ctx)` - `scene.mjs`'s `newContext` does
both; `web/tests/deploy/shotsFixtures.test.ts` fails on a context that does
not. `SHOTS_REAL_CLOCK=1` leaves the browsers on the real clock, to tell a
scene that breaks on the clock from one that breaks on its own; `accept`
refuses such a run.

## Writing a scene

- Write the picture with `shot()` from `scene.mjs` (or the script's own
  writer) under its set folder; `const SET = '<set>'` names the folder.
- Make browser contexts with `scene.mjs`'s `newContext`, or with
  `browser.newContext({ ...SCENE_CONTEXT, ... })` followed by
  `stageClock(ctx)` (`clock.mjs`), and date the files you write into a
  storage with `pinTimes(root)` before it is synced.
- Wait for what the picture claims, never a fixed time, then look at the
  picture: "passed" does not check what is in it.
- A scene that shows a narrow part of the product can declare what it reads,
  so that it is skipped when nothing of that changed:

  ```js
  // the view and the widgets a reader looks at the picture to see
  export const INPUTS = ['web/src/views/Roles.vue', 'web/src/lib/roleName.ts', '@version'];
  ```

  ⚠ A declaration that misses a component the scene shows keeps a stale
  picture until a full run (`pnpm shots --all`) finds its pixels moved. Without
  a declaration the scene is taken whenever anything in the product changed,
  which is always correct and costs a minute.

Each script still runs on its own (`node e2e/shots/roles.mjs`); see
[e2e/README.md](../README.md#screenshots-shots) for the environment variables.
