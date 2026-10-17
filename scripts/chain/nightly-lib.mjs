// The decisions of the nightly run (scripts/chain/nightly.mjs) and of its
// morning report (scripts/chain/report.mjs), as pure functions: no process,
// no network, no clock and no file of their own - the callers read and pass
// them. web/tests/deploy/chainNightly.test.ts holds them.
//
// Task #175. The night, in order:
//   01:00  nightly.mjs fetches main into its own checkout. When main has not
//          moved since the last night that ran, nothing runs (decideRun).
//          Inside a quiet window, or when the run would not be over before
//          the next one starts, nothing runs either (startWindow): the
//          weekly service-update round on the same host is such a window.
//   then   scripts/chain/run.sh --profile nightly: the full chain plus the
//          NIGHTLY_EXTRAS below, under the build host's shared lock.
//   then   when the run is green and main moved since the last nightly
//          build, the image of that commit, built from its public form
//          (decideBuild, scripts/chain/nightly-build.sh), and pushed right
//          after it when NIGHTLY_PUBLISH=auto - what the build host's settings
//          say (scripts/chain/nightly.env.example): every green change goes
//          out as :nightly, the first one included (registryLogin).
//   then   with a build made, the desktop side (#266, decideDesktop,
//          scripts/chain/nightly-desktop.sh): the amd64 snap for the Snap
//          Store's edge channel (NIGHTLY_SNAP=edge), and the public form of
//          the commit to ghcr for GitHub's runners (NIGHTLY_DESKTOP=github),
//          where .github/workflows/nightly-desktop.yml makes every desktop
//          package - Windows, macOS, Linux - and the arm64 snap from it,
//          every such night. An upload the Store holds for a review is said
//          every morning until edge serves it (snapEdgeState).
//   07:00  report.mjs: GitHub's desktop packages of the night followed up
//          (followVerdict: built, still building, failed, never started),
//          the filex.sh/nightly page made from them (renderNightlyPage), and
//          one notification for the night (composeReport).

import path from 'node:path';

const DAY_MS = 86_400_000;
const short = (sha) => String(sha ?? '').slice(0, 8) || '?';

// ── the extras of the nightly profile ───────────────────────────────────────

/**
 * What the nightly profile runs on top of the full one, in the browser round
 * (scripts/chain/plan.mjs). None of them is in the full chain, each for its
 * own reason:
 *
 *   ds-go    the Go test that needs a real ONLYOFFICE Document Server and the
 *            Convert app (backend/internal/server/office_engine_ds_test.go):
 *            it skips everywhere else. It runs right after the last line
 *            with the Document Server, which is still up for it.
 *   s3-live  the Go tests that talk to a REAL S3 provider (FILEX_TEST_S3_*,
 *            backend/internal/testutil/lives3), against the bucket the build
 *            host's settings name (S3_LIVE_KEYS), never GitHub's. Not every
 *            night: only when the S3 code changed since its last green run
 *            (decideS3Live, S3_LIVE_PATHS) - and without the settings then,
 *            a warning in the morning report, not a red night. When it runs,
 *            a test that only skipped is red.
 *   shots    `pnpm shots --all`, every scene - the app scenes with the app
 *            builds and language packs the chain mounts, the ONLYOFFICE scene
 *            against the chain's Document Server (task #187): a shot script
 *            that no longer fits the product turns the night red, not the
 *            release day, and every picture is compared with the published
 *            one - taken in the very place the published set is taken (the
 *            chain's Playwright container and fontconfig, shots-site
 *            PUBLISH_ENVIRONMENT). It runs right after ds-go, while the
 *            Document Server is still up. A night whose only failed scene
 *            is a language pack behind the tree is a warning, not red
 *            (SHOTS_PACKS_BEHIND=warn; the maintainer, 2026-10-08).
 *   realenv  e2e/realenv/run.sh, filex against the real ACME, SSO and office
 *            servers. It starts containers of its own, so it runs on the host,
 *            not in a job container; the nightly run asks for it once every
 *            NIGHTLY_REALENV_EVERY_DAYS days (extrasFor).
 *
 * CHAIN_NIGHTLY_EXTRAS picks some of them (comma-separated, `none` for none).
 */
export const NIGHTLY_EXTRAS = ['ds-go', 's3-live', 'shots', 'realenv'];

/**
 * The extras that need the chain's Document Server (run.mjs starts it for a
 * browser-round job with `ds`, and stops it once no later job needs it): ds-go
 * for its Go test, shots for the ONLYOFFICE scene (e2e/shots/csvoffice.mjs).
 */
export const DS_EXTRAS = ['ds-go', 'shots'];

/**
 * The settings of the s3-live job: the variables the live S3 suites read
 * (backend/internal/testutil/lives3), set in the nightly settings file
 * (0600). `required` are the ones without which the job is red: the bucket
 * and its key, and the region - it is part of the request signature, an
 * empty one signs "auto", and a provider with a region of its own refuses
 * that with an error that does not name the region (lesson #927: Versity S3
 * Gateway answers 400 AuthorizationHeaderMalformed). The endpoint may be
 * empty only for AWS itself.
 */
export const S3_LIVE_KEYS = {
  required: ['FILEX_TEST_S3_BUCKET', 'FILEX_TEST_S3_ACCESS_KEY', 'FILEX_TEST_S3_SECRET_KEY', 'FILEX_TEST_S3_REGION'],
  optional: ['FILEX_TEST_S3_ENDPOINT', 'FILEX_TEST_S3_PATH_STYLE'],
};

/**
 * The file run.mjs hands the s3-live job (`<run>/etc/s3-live.env`, 0600,
 * removed when the run ends): one `KEY=VALUE` line per S3_LIVE_KEYS variable
 * the settings set. The values reach the job through this file and nothing
 * else - not `docker run -e`, whose arguments every process list and `docker
 * inspect` show - and are never logged: `missing` holds names only, the
 * required ones the settings leave unset.
 */
export function s3LiveFile(env = {}) {
  const lines = [];
  const missing = [];
  for (const key of [...S3_LIVE_KEYS.required, ...S3_LIVE_KEYS.optional]) {
    const value = env[key];
    if (value === undefined || value === '') {
      if (S3_LIVE_KEYS.required.includes(key)) missing.push(key);
      continue;
    }
    if (/[\r\n\0]/.test(String(value))) throw new Error(`${key} holds a line break: one line per value in the settings file`);
    lines.push(`${key}=${value}`);
  }
  return { text: lines.length ? `${lines.join('\n')}\n` : '', missing };
}

/**
 * The code the live S3 tests measure: a change under one of these since their
 * last green run is what makes a night run them (the owner's decision of
 * 2026-10-06: not every night - a provider's account is a demo account, and a
 * change to the drive itself is tested by hand with its own credentials).
 * Read off what the four tests and the driver import:
 *
 *   backend/internal/storage/drivers/s3/   the driver: requests, multipart,
 *                                          presign, retries (resilience.go)
 *   backend/internal/storage/stall/        the stall policy the driver wraps
 *                                          every read and send in
 *   backend/internal/storage/{driver,object,tally,registry,descriptor,validate}.go
 *                                          the contracts it implements and the
 *                                          tests call (Writer, Mover, Presigner,
 *                                          ErrNotFound, Tally, Register)
 *   backend/internal/testutil/lives3/      how the tests find their server
 *   backend/internal/usage/b2              the B2 report reader (TestB2_OverRealS3)
 *   .../handlers/rename_s3_live_test.go    the live rename test itself
 *   scripts/chain/job/s3-live.sh           the job
 *   backend/go.mod                         when a github.com/aws/ line moved:
 *                                          the SDK the driver speaks through
 *
 * Not the catalogue, the sync worker or the handlers the rename test also
 * goes through: their own suites test them on every run, and the live test is
 * there for what only a real provider answers.
 */
export const S3_LIVE_PATHS = [
  'backend/internal/storage/drivers/s3/',
  'backend/internal/storage/stall/',
  'backend/internal/storage/driver.go',
  'backend/internal/storage/object.go',
  'backend/internal/storage/tally.go',
  'backend/internal/storage/registry.go',
  'backend/internal/storage/descriptor.go',
  'backend/internal/storage/validate.go',
  'backend/internal/testutil/lives3/',
  'backend/internal/usage/b2',
  'backend/internal/api/handlers/rename_s3_live_test.go',
  'scripts/chain/job/s3-live.sh',
];

/**
 * What of a change touches the live S3 tests: the changed `files` under
 * S3_LIVE_PATHS (a path ending in `/` is a folder, any other a prefix), and
 * `backend/go.mod` when a line of `goModDiff` (git diff of that file) that
 * moved names github.com/aws/.
 */
export function s3Touched(files, goModDiff = '') {
  const hit = files.filter((f) => S3_LIVE_PATHS.some((p) => f.startsWith(p)));
  const aws = String(goModDiff)
    .split('\n')
    .some((l) => /^[+-](?![+-])/.test(l) && /github\.com\/aws\//.test(l));
  if (aws && !hit.includes('backend/go.mod')) hit.push('backend/go.mod');
  return hit;
}

/**
 * Whether tonight runs s3-live, and what the morning report says of it.
 *
 *   asked     CHAIN_NIGHTLY_EXTRAS includes it
 *   base      the commit the S3 code is measured from: the last green s3-live
 *             run's, else the night the measuring started (state.json s3_live)
 *   touched   s3Touched(base..head), or null when base is not in the checkout
 *   missing   the required FILEX_TEST_S3_* the settings leave unset
 *
 * `status`: not-asked; unchanged (no run, not red: "skipped - S3 driver
 * unchanged since <base>"); no-credentials (no run, not red: a warning that
 * names what changed and how to test it by hand); run.
 */
export function decideS3Live({ asked = true, base = null, touched = null, missing = [] }) {
  if (!asked) return { run: false, status: 'not-asked', since: base, why: 'CHAIN_NIGHTLY_EXTRAS leaves s3-live out' };
  if (base && Array.isArray(touched) && touched.length === 0) {
    return { run: false, status: 'unchanged', since: base, why: `S3 driver unchanged since ${short(base)}` };
  }
  const what = !base
    ? 'no green live S3 run on record'
    : touched === null
      ? `${short(base)}, the last live S3 run, is not in this checkout`
      : `${touched.length} file(s) changed since ${short(base)}: ${touched.slice(0, 5).join(', ')}${touched.length > 5 ? ', ...' : ''}`;
  if (missing.length) {
    return {
      run: false,
      status: 'no-credentials',
      since: base,
      files: touched ?? [],
      why: `the S3 driver changed (${what}) and the live tests did not run: no credentials (${missing.join(', ')})`,
    };
  }
  return { run: true, status: 'run', since: base, files: touched ?? [], why: what };
}

/**
 * Where the S3 code is measured from after a night: a green s3-live moves it
 * to the night's commit; anything else leaves it - a change that was not
 * tested is reported again the next night, until it is (by a green night or
 * by hand). The first night pins it to `base`, so it never drifts with main.
 */
export function nextS3LiveState(prev, { head, base, passed, at, by = 'night' }) {
  if (passed) return { sha: head, at, by };
  return prev ?? { sha: base ?? null, at, by: 'start' };
}

/** The extras a nightly run adds: CHAIN_NIGHTLY_EXTRAS, else all of them, always in NIGHTLY_EXTRAS order. */
export function nightlyExtras(env = {}) {
  const v = String(env.CHAIN_NIGHTLY_EXTRAS ?? '').trim();
  if (!v) return [...NIGHTLY_EXTRAS];
  if (v === 'none') return [];
  const asked = v.split(/[\s,]+/).filter(Boolean);
  for (const a of asked) {
    if (!NIGHTLY_EXTRAS.includes(a)) throw new Error(`CHAIN_NIGHTLY_EXTRAS: "${a}" is none of ${NIGHTLY_EXTRAS.join(', ')} (or none)`);
  }
  return NIGHTLY_EXTRAS.filter((x) => asked.includes(x));
}

/**
 * Tonight's extras: what was asked for, less realenv when it ran (passed or
 * red) fewer than `everyDays` days ago. `everyDays` 0 never runs it, 1 runs
 * it every night. Half a day of slack: a run that started at 01:10 last week
 * is due at 01:00 tonight.
 */
export function extrasFor({ asked, everyDays, lastRealenvMs = null, nowMs }) {
  if (!asked.includes('realenv')) return [...asked];
  const due = everyDays > 0 && (lastRealenvMs === null || nowMs - lastRealenvMs >= (everyDays - 0.5) * DAY_MS);
  return due ? [...asked] : asked.filter((x) => x !== 'realenv');
}

// ── settings ────────────────────────────────────────────────────────────────

/**
 * The nightly run's settings, from the same KEY=VALUE file the chain reads
 * (scripts/chain/env.mjs). CHAIN_ROOT is the nightly run's own directory -
 * its checkout, runs, caches and state - and CHAIN_LOCK the build host's
 * shared lock, which it must name: a nightly run with a lock of its own would
 * share the host's memory with the release chain instead of waiting for it.
 */
export function nightlySettings(env) {
  const num = (key, def) => {
    const v = env[key];
    if (v === undefined || v === '') return def;
    const n = Number(v);
    if (!Number.isFinite(n) || n < 0) throw new Error(`${key}=${v} is not a number`);
    return n;
  };
  const oneOf = (key, def, allowed) => {
    const v = env[key] || def;
    if (!allowed.includes(v)) throw new Error(`${key}=${v}: one of ${allowed.join(', ')}`);
    return v;
  };
  if (!env.CHAIN_ROOT) throw new Error('CHAIN_ROOT is not set: the nightly run keeps its checkout, runs and state there');
  if (!env.CHAIN_LOCK) throw new Error('CHAIN_LOCK is not set: name the lock every build on this host takes (or none)');
  const root = path.resolve(env.CHAIN_ROOT);
  return {
    root,
    state: path.join(root, 'nightly'),
    runs: path.join(root, 'runs'),
    src: path.resolve(env.NIGHTLY_SRC || path.join(root, 'src')),
    remote: env.NIGHTLY_REMOTE_NAME || 'origin',
    branch: env.NIGHTLY_BRANCH || 'main',
    lock: env.CHAIN_LOCK,
    prefix: env.CHAIN_PREFIX || 'fxnightly',
    quiet: parseQuiet(env.NIGHTLY_QUIET || ''),
    marginMin: num('NIGHTLY_QUIET_MARGIN_MIN', 15),
    expectMin: num('NIGHTLY_EXPECT_MIN', 180),
    lockWaitMin: num('NIGHTLY_LOCK_WAIT_MIN', 120),
    realenvEveryDays: num('NIGHTLY_REALENV_EVERY_DAYS', 7),
    extras: nightlyExtras(env),
    // Names only: the values stay in the settings file and reach the job
    // through run.mjs (s3LiveFile).
    s3LiveMissing: S3_LIVE_KEYS.required.filter((k) => !env[k]),
    keepRuns: num('NIGHTLY_KEEP_RUNS', 14),
    keepHistory: num('NIGHTLY_KEEP_HISTORY', 120),
    tz: env.NIGHTLY_TZ || 'UTC',
    build: {
      mode: oneOf('NIGHTLY_BUILD', 'on', ['on', 'off']),
      publish: oneOf('NIGHTLY_PUBLISH', 'manual', ['manual', 'auto', 'off']),
      image: env.NIGHTLY_IMAGE || 'filex-nightly',
      dockerfile: env.NIGHTLY_DOCKERFILE || 'docker/Dockerfile',
      publicRemote: env.NIGHTLY_PUBLIC_REMOTE || 'https://github.com/BRF-Tech/filex.git',
      publicDir: path.resolve(env.NIGHTLY_PUBLIC_DIR || path.join(root, 'public')),
      pushRepo: env.NIGHTLY_PUSH_REPO || '',
      registryUser: env.NIGHTLY_REGISTRY_USER || '',
      registryTokenFile: env.NIGHTLY_REGISTRY_TOKEN_FILE || '',
      keepImages: num('NIGHTLY_KEEP_IMAGES', 3),
      expectMin: num('NIGHTLY_BUILD_EXPECT_MIN', 25),
    },
    // #266: the desktop side of a nightly build (decideDesktop). Off in the
    // code, as publishing is: a checkout with no Snap Store credential and no
    // snapcraft would turn every green night into a warning. The build
    // host's settings switch them on (nightly.env.example).
    snap: {
      mode: oneOf('NIGHTLY_SNAP', 'off', ['edge', 'off']),
      name: env.NIGHTLY_SNAP_NAME || 'filex-app',
      // The output of `snapcraft export-login`, in a 0600 file: read when the
      // upload runs, handed to snapcraft in its environment, never logged.
      credentialsFile: env.NIGHTLY_SNAP_CREDENTIALS_FILE || '',
      snapcraft: env.NIGHTLY_SNAPCRAFT || 'snapcraft',
    },
    desktop: {
      // github: every night that makes a build, the public form of its commit
      // goes to `srcRepo` (an OCI artifact), and GitHub's runners make the
      // desktop packages from it (`workflow`, in `workflowRepo`) into `repo`.
      mode: oneOf('NIGHTLY_DESKTOP', 'off', ['github', 'off']),
      srcRepo: env.NIGHTLY_DESKTOP_SRC_REPO || 'ghcr.io/brf-tech/filex-nightly-src',
      repo: env.NIGHTLY_DESKTOP_REPO || 'ghcr.io/brf-tech/filex-desktop-nightly',
      workflowRepo: env.NIGHTLY_DESKTOP_WORKFLOW_REPO || 'BRF-Tech/filex',
      workflow: env.NIGHTLY_DESKTOP_WORKFLOW || 'nightly-desktop.yml',
      // The ORAS client, run as a container: it pushes and reads the OCI
      // artifacts. Pinned, as every image the chain runs.
      oras: env.NIGHTLY_ORAS_IMAGE || 'ghcr.io/oras-project/oras:v1.2.2',
      // A command that publishes the filex.sh/nightly page and its packages,
      // given a directory with index.html and nightly.json as its one
      // argument (sh -c '<command> "$1"'), NIGHTLY_SITE_REF naming the
      // artifact (scripts/chain/nightly-site.sh). Empty: the page is made and
      // kept here, and the morning report says it was not published.
      siteCmd: env.NIGHTLY_SITE_CMD || '',
      // electron-builder's own image, for the app the amd64 snap is packed
      // from. Pinned by date, as every image the chain runs.
      image: env.NIGHTLY_DESKTOP_IMAGE || 'electronuserland/builder:22-wine-05.26',
      publicDir: path.resolve(env.NIGHTLY_DESKTOP_PUBLIC_DIR || path.join(root, 'public-desktop')),
      out: path.resolve(env.NIGHTLY_DESKTOP_DIR || path.join(root, 'desktop')),
      keep: num('NIGHTLY_DESKTOP_KEEP', 4),
      expectMin: num('NIGHTLY_DESKTOP_EXPECT_MIN', 30),
      cache: {
        goroot: path.resolve(env.CHAIN_GOROOT || path.join(root, 'cache', 'go-desktop')),
        gomod: path.resolve(env.CHAIN_GOMODCACHE || path.join(root, 'cache', 'gomod')),
        gocache: path.resolve(env.CHAIN_GOCACHE_ROOT || path.join(root, 'cache', 'gocache-root')),
        pnpm: path.resolve(env.CHAIN_PNPM_STORE || path.join(root, 'cache', 'pnpm-store')),
        electron: path.resolve(env.CHAIN_ELECTRON_CACHE || path.join(root, 'cache', 'electron')),
      },
    },
    notify: {
      url: env.CHAIN_NOTIFY_URL || '',
      keyFile: env.CHAIN_NOTIFY_KEY_FILE || '',
      group: env.NIGHTLY_NOTIFY_GROUP || env.CHAIN_NOTIFY_GROUP || 'infra',
      source: env.NIGHTLY_NOTIFY_SOURCE || 'filex-nightly',
    },
    report: {
      waitMin: num('NIGHTLY_REPORT_WAIT_MIN', 120),
      maxAgeH: num('NIGHTLY_REPORT_MAX_AGE_H', 20),
    },
  };
}

// ── when a night may run ────────────────────────────────────────────────────

const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

/**
 * NIGHTLY_QUIET: comma-separated windows in which no nightly run may be going,
 * `<day> HH:MM-HH:MM` in UTC (`*` for every day; an end before the start ends
 * on the next day). UTC because the jobs they keep clear of are scheduled in
 * UTC by cron, as the weekly service-update round is.
 */
export function parseQuiet(spec) {
  const out = [];
  for (const text of String(spec).split(',').map((s) => s.trim()).filter(Boolean)) {
    const m = /^(\*|Sun|Mon|Tue|Wed|Thu|Fri|Sat)\s+(\d{1,2}):(\d{2})-(\d{1,2}):(\d{2})$/.exec(text);
    if (!m) throw new Error(`NIGHTLY_QUIET: "${text}" is not "<day> HH:MM-HH:MM" (UTC; day Mon..Sun or *)`);
    const [h1, m1, h2, m2] = [m[2], m[3], m[4], m[5]].map(Number);
    if (h1 > 23 || m1 > 59 || h2 > 24 || m2 > 59 || (h2 === 24 && m2 > 0)) throw new Error(`NIGHTLY_QUIET: "${text}" is not a time of day`);
    const start = h1 * 60 + m1;
    const end = h2 * 60 + m2;
    if (start === end) throw new Error(`NIGHTLY_QUIET: "${text}" is empty`);
    out.push({ day: m[1] === '*' ? -1 : DAYS.indexOf(m[1]), start, end, text });
  }
  return out;
}

/** Where `nowMs` stands against the windows: the one it is inside (or null), and when the next one starts (or null). */
export function quietState(nowMs, windows) {
  const midnight = Math.floor(nowMs / DAY_MS) * DAY_MS;
  let inside = null;
  let nextStart = null;
  for (let d = -1; d <= 7; d += 1) {
    const day0 = midnight + d * DAY_MS;
    const dow = new Date(day0).getUTCDay();
    for (const w of windows) {
      if (w.day !== -1 && w.day !== dow) continue;
      const s = day0 + w.start * 60_000;
      const e = day0 + (w.end > w.start ? w.end : w.end + 1440) * 60_000;
      if (!inside && s <= nowMs && nowMs < e) inside = { ...w, startMs: s, endMs: e };
      if (s > nowMs && (nextStart === null || s < nextStart)) nextStart = s;
    }
  }
  return { inside, nextStart };
}

/**
 * Whether a run may start now: not inside a quiet window, and over - with the
 * minutes it is expected to take, and `marginMin` to spare - before the next
 * one starts. `deadline` is when the run must be stopped if it is still going
 * (null: none); `lockWaitMin` how long it may wait for the build lock.
 */
export function startWindow({ nowMs, windows, expectMin, marginMin, lockWaitMin }) {
  const { inside, nextStart } = quietState(nowMs, windows);
  if (inside) return { ok: false, why: `inside the quiet window ${inside.text} UTC` };
  if (nextStart === null) return { ok: true, deadline: null, lockWaitMin };
  const deadline = nextStart - marginMin * 60_000;
  const spare = Math.floor((deadline - nowMs) / 60_000 - expectMin);
  if (spare < 0) {
    return {
      ok: false,
      why: `a run of ~${Math.round(expectMin)} min would not be over before the quiet window at ${isoMinute(nextStart)} (less ${marginMin} min)`,
    };
  }
  return { ok: true, deadline, lockWaitMin: Math.min(lockWaitMin, spare) };
}

/** Minutes a nightly run is expected to take: the last finished one's, else the setting. */
export function expectedRunMin(history, fallbackMin) {
  const last = [...history].reverse().find((r) => r.decision === 'ran' && r.finished && !r.stopped && Number.isFinite(r.secs));
  return last ? Math.ceil(last.secs / 60) : fallbackMin;
}

/**
 * Whether tonight runs. `last` is the history's last record of a night that
 * ran (decision "ran"), or null. Main that has not moved is not run again -
 * unless the last run on it did not finish (stopped, or a setup error).
 */
export function decideRun({ head, last, force = false }) {
  if (force) return { run: true, why: 'forced (--force)' };
  if (!last) return { run: true, why: 'the first nightly run' };
  if (last.sha !== head) return { run: true, why: `main moved: ${short(last.sha)}..${short(head)}` };
  if (!last.finished || last.stopped || last.exit === 2 || last.exit === 130) {
    return { run: true, why: `the last run on ${short(head)} did not finish (${last.stopped ? 'stopped' : `exit ${last.exit ?? '?'}`})` };
  }
  return { run: false, why: `main has not moved since the last nightly run (${short(head)}, ${last.ok ? 'green' : 'red'})` };
}

/** The last record of a night that ran, before index `before` (default: the end). */
export function lastRan(history, before = history.length) {
  for (let i = Math.min(before, history.length) - 1; i >= 0; i -= 1) if (history[i].decision === 'ran') return history[i];
  return null;
}

// ── the nightly build ───────────────────────────────────────────────────────

/**
 * Whether tonight's result makes a nightly build: a finished green nightly
 * run on a clean checkout, of a commit no nightly build was made of yet. A red
 * night, or a night main did not move, makes none: there is no build every
 * day, only one per green change.
 */
export function decideBuild({ result, mode = 'on', lastBuildSha = '' }) {
  if (mode === 'off') return { build: false, why: 'NIGHTLY_BUILD=off' };
  if (!result) return { build: false, why: 'no result of a nightly run' };
  if (result.profile !== 'nightly') return { build: false, why: `the run is a ${result.profile ?? '?'} run, not a nightly one` };
  if (!result.finished || result.ok !== true) return { build: false, why: `the nightly run on ${short(result.sha)} is not green` };
  if (result.dirty_files) return { build: false, why: `the run had ${result.dirty_files} uncommitted file(s)` };
  if (lastBuildSha && lastBuildSha === result.sha) return { build: false, why: `no commit since the last nightly build (${short(result.sha)})` };
  return {
    build: true,
    why: lastBuildSha ? `main moved since the last nightly build: ${short(lastBuildSha)}..${short(result.sha)}` : 'the first nightly build',
  };
}

/**
 * The version a nightly build reports: the next minor after the released one
 * (packages/core/package.json), as a pre-release of it, with the commit as
 * build metadata - `0.53.0-nightly.20261007+1a2b3c4d`. It sorts above every
 * 0.52.x and below 0.53.0, so an install on it is offered 0.53.0 when it ships
 * (backend/internal/update/semver.go reads the pre-release and drops the
 * metadata).
 */
export function nightlyVersion(released, ymd, sha) {
  const m = /^v?(\d+)\.(\d+)\.(\d+)$/.exec(String(released ?? '').trim());
  if (!m) throw new Error(`"${released}" is not a released version (x.y.z)`);
  if (!/^\d{8}$/.test(String(ymd))) throw new Error(`"${ymd}" is not a date (YYYYMMDD)`);
  if (!/^[0-9a-f]{8,40}$/.test(String(sha))) throw new Error(`"${sha}" is not a commit`);
  return `${m[1]}.${Number(m[2]) + 1}.0-nightly.${ymd}+${sha.slice(0, 8)}`;
}

/** The tags a published nightly build goes out under: the moving one, and the night's. */
export function nightlyTags(ymd) {
  return ['nightly', `nightly-${ymd}`];
}

/**
 * The labels a nightly image carries besides its version and commit (#266):
 * where its source is, what it is, and the night's test result - so `docker
 * inspect` on a pulled `:nightly` says which commit it is and whether its
 * night was green, with no account and no other page. nightly-build.sh adds
 * `sh.filex.nightly.public-base` itself, the public repository's main the
 * export was staged on (the last release's public commit: a nightly is that
 * plus the CHANGELOG's [Unreleased]), which it knows only once it fetched.
 *
 *   counts   result.json `counts` of the green night
 *   runId    the chain run's id
 *
 * One `key=value` a label, as nightly-build.sh reads them (NB_LABELS_FILE).
 */
export function nightlyLabels({ counts = null, runId = '', source = 'https://github.com/BRF-Tech/filex' } = {}) {
  const tests = counts && Number.isFinite(counts.total) ? `green: ${counts.passed} passed, ${counts.skipped ?? 0} skipped of ${counts.total} jobs${runId ? ` (${runId})` : ''}` : '';
  const labels = {
    'org.opencontainers.image.source': source,
    'org.opencontainers.image.description': 'filex nightly build: the tip of main after a green nightly test run. Not a release and not supported - for trying what the next release brings.',
    ...(tests ? { 'sh.filex.nightly.tests': tests } : {}),
  };
  for (const [k, v] of Object.entries(labels)) if (/[\r\n]/.test(v)) throw new Error(`label ${k} holds a line break`);
  return labels;
}

// ── the nightly desktop packages and the snap on edge (#266) ───────────────

/**
 * The Snap Store refuses a version longer than 32 characters or with
 * anything but letters, digits and `.+~-:` (and it must start and end with
 * a letter or digit). A nightly's version (nightlyVersion) is 32 exactly
 * while the minor has two digits: past that, the commit in the build
 * metadata is cut to seven characters, then `nightly.` to `n.`. Every desktop
 * package of a night carries this one version.
 */
export function desktopVersion(version) {
  const ok = (v) => v.length <= 32 && /^[A-Za-z0-9](?:[A-Za-z0-9.+~:-]*[A-Za-z0-9])?$/.test(v);
  let v = String(version ?? '');
  if (ok(v)) return v;
  v = v.replace(/\+([0-9a-f]{7})[0-9a-f]+$/, '+$1');
  if (ok(v)) return v;
  v = v.replace('-nightly.', '-n.');
  if (ok(v)) return v;
  throw new Error(`"${version}" cannot be made a snap version (32 characters, letters, digits and .+~-:)`);
}

/**
 * What the night does for the desktop, after a nightly build (`built`: the
 * image of a green change was made tonight):
 *
 *   snap    NIGHTLY_SNAP=edge: the amd64 snap, packed on this host, for the
 *           Snap Store's edge channel
 *   source  NIGHTLY_DESKTOP=github: the public form of the commit, pushed to
 *           ghcr for GitHub's runners, which make every desktop package and
 *           the arm64 snap from it (.github/workflows/nightly-desktop.yml)
 *
 * Both every night that makes a build. A night without one does nothing:
 * there is nothing new to package (the nightly run itself runs only when
 * main moved). Returns { targets: ['snap', 'source'], why }.
 */
export function decideDesktop({ built, snapMode = 'off', desktopMode = 'off' }) {
  if (!built) return { targets: [], why: 'no nightly build tonight' };
  const targets = [];
  const why = [];
  if (snapMode === 'edge') {
    targets.push('snap');
    why.push('the amd64 snap for edge');
  }
  if (desktopMode === 'github') {
    targets.push('source');
    why.push("the source of the desktop packages GitHub's runners make");
  }
  return { targets, why: why.length ? why.join(' and ') : 'no desktop target asked for' };
}

/**
 * What `snapcraft upload --release=edge` answered: `released` (a revision,
 * released to edge), `review` (uploaded, held for the Store's review - the
 * channel keeps its old revision until the review passes, so the night's
 * report warns, and so does every night after it until edge serves it) or
 * `failed`. `revision` when the output names one.
 */
export function snapUploadVerdict(status, text) {
  const out = String(text ?? '');
  const revision = Number(/Revision\s+(\d+)/i.exec(out)?.[1] ?? NaN);
  const rev = Number.isFinite(revision) ? { revision } : {};
  if (/manual review|review(?:ed)? (?:is )?pending|waiting for (?:a )?review/i.test(out)) return { state: 'review', ...rev };
  if (status === 0) return { state: 'released', ...rev };
  return { state: 'failed', ...rev, why: out.trim().split('\n').filter(Boolean).slice(-3).join(' / ') || `exit ${status}` };
}

/**
 * Whether the Snap Store's edge channel serves the nightly snap the last
 * upload sent (`last`: state.json last_snap), read from the public channel
 * map (api.snapcraft.io/v2/snaps/info). `live` when edge/<arch> carries that
 * version; `held` when it does not - with how long since the upload - so a
 * review that never ends is said every morning, not once.
 */
export function snapEdgeState(map, last, { arch = 'amd64', nowMs } = {}) {
  if (!last?.version) return null;
  const entry = (map ?? []).find((m) => m?.channel?.name === 'edge' && m?.channel?.architecture === arch);
  if (entry && entry.version === last.version) return { state: 'live', version: entry.version, revision: entry.revision };
  const days = last.at ? Math.floor((nowMs - Date.parse(last.at)) / DAY_MS) : null;
  return {
    state: 'held',
    arch,
    version: last.version,
    revision: last.revision ?? null,
    edge: entry ? `${entry.version} (revision ${entry.revision})` : 'no revision',
    since: last.at ?? null,
    days,
  };
}

/** The local desktop runs to remove: every <ymd>-<sha8> directory but the newest `keep` and `protect`. */
export function desktopDirsToPrune(names, keep, protect = []) {
  return names.filter((n) => /^\d{8}-[0-9a-f]{8}$/.test(n)).sort().reverse().slice(keep).filter((n) => !protect.includes(n));
}

// ── the desktop packages GitHub's runners make (#266) ──────────────────────

const ARCH_OF = { x64: 'x64', x86_64: 'x64', amd64: 'x64', arm64: 'arm64', aarch64: 'arm64' };

/** The platforms of the nightly desktop packages, in the order the page shows them. */
export const DESKTOP_PLATFORMS = [
  { id: 'windows', label: 'Windows' },
  { id: 'macos', label: 'macOS' },
  { id: 'linux', label: 'Linux' },
];

const KIND_LABEL = {
  installer: 'Installer (.exe)',
  portable: 'Portable (.exe, nothing installed)',
  dmg: 'Disk image (.dmg)',
  zip: 'App (.zip)',
  AppImage: 'AppImage',
  deb: '.deb (Debian, Ubuntu)',
  rpm: '.rpm (Fedora, openSUSE)',
};

/**
 * What a desktop package is, from the name electron-builder gives it
 * (desktop/electron-builder.yml: `filex-desktop-${arch}.${ext}`, the portable
 * .exe `filex-desktop-portable-${arch}.exe`): { platform, arch, kind }, or
 * null for anything else (a blockmap, a feed, a snap - the Snap Store serves
 * those).
 */
export function classifyDesktopFile(name) {
  const m = /^filex-desktop-(portable-)?([a-z0-9_]+)\.(exe|AppImage|deb|rpm|dmg|zip)$/.exec(String(name ?? ''));
  if (!m) return null;
  const arch = ARCH_OF[m[2]];
  if (!arch) return null;
  if (m[3] === 'exe') return { platform: 'windows', arch, kind: m[1] ? 'portable' : 'installer' };
  if (m[1]) return null;
  if (m[3] === 'dmg' || m[3] === 'zip') return { platform: 'macos', arch, kind: m[3] };
  return { platform: 'linux', arch, kind: m[3] };
}

/**
 * The night's desktop manifest, nightly.json - what the workflow pushes
 * beside the packages and what the page is made from. `files`: { name, size,
 * sha256 } of every file the rows made (anything that is no package is left
 * out); `rows`: each row's outcome ({ 'linux-x64': 'built', ... }); `snap`:
 * the arm64 snap's upload to edge ({ arm64: { state, revision?, why? } }).
 */
export function desktopManifest({ files = [], version, revision, date, run = '', rows = {}, snap = {}, builtAt = '' }) {
  const rank = (f) => DESKTOP_PLATFORMS.findIndex((p) => p.id === f.platform) * 100 + (f.arch === 'x64' ? 0 : 50) + Object.keys(KIND_LABEL).indexOf(f.kind);
  const out = files
    .map((f) => ({ name: f.name, size: Number(f.size) || 0, sha256: String(f.sha256 ?? ''), ...classifyDesktopFile(f.name) }))
    .filter((f) => f.platform)
    .sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
  return { schema: 1, version, revision, date, run, built_at: builtAt, rows, snap, files: out };
}

/** The platform a row of the workflow makes packages for (nightly-desktop-job.sh targets), or null (the snap). */
export const platformOfRow = (row) => (row === 'mac' ? 'macos' : row === 'windows' ? 'windows' : /^linux-/.test(row) ? 'linux' : null);

const html = (v) => String(v ?? '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

/** The markers site/nightly.src.html carries, each replaced by renderNightlyPage. */
export const PAGE_MARKERS = ['version', 'date', 'commit', 'downloads', 'run'];

/**
 * The filex.sh/nightly page: site/nightly.src.html (the site's own page, its
 * look the site's) with its markers (<!--nightly:NAME-->) filled from the
 * night's manifest. A platform with no package says it was not built that
 * night - an old night's package is never shown as tonight's. Every value is
 * escaped. Throws when the template lacks a marker: a page that silently
 * drops the downloads is worse than none.
 */
export function renderNightlyPage({ template, manifest }) {
  for (const m of PAGE_MARKERS) if (!String(template).includes(`<!--nightly:${m}-->`)) throw new Error(`the page template has no <!--nightly:${m}--> marker`);
  const mb = (n) => `${(n / 1048576).toFixed(n >= 10485760 ? 0 : 1)} MB`;
  const blocks = DESKTOP_PLATFORMS.map((p) => {
    const files = manifest.files.filter((f) => f.platform === p.id);
    const failed = Object.entries(manifest.rows ?? {}).filter(([row, st]) => platformOfRow(row) === p.id && st !== 'built').map(([row]) => row);
    const items = files.map((f) => `<li><a href="./${html(encodeURIComponent(f.name))}">${html(KIND_LABEL[f.kind] ?? f.kind)}</a> <span class="meta">${html(f.arch)} · ${html(mb(f.size))}</span></li>`);
    const missing = !files.length ? '<p class="meta">Not built this night.</p>' : failed.length ? `<p class="meta">Not built this night: ${html(failed.join(', '))}.</p>` : '';
    return `<h2>${html(p.label)}</h2>\n${items.length ? `<ul class="files">\n${items.join('\n')}\n</ul>\n` : ''}${missing}`;
  }).join('\n');
  const values = {
    version: html(manifest.version),
    date: html(manifest.date),
    commit: html(String(manifest.revision ?? '').slice(0, 8)),
    downloads: `${blocks}\n<p class="meta"><a href="./SHA256SUMS">SHA256SUMS</a> - every file's checksum</p>`,
    run: manifest.run ? `<a href="${html(manifest.run)}">the build</a>` : 'the build',
  };
  let out = String(template);
  for (const m of PAGE_MARKERS) out = out.split(`<!--nightly:${m}-->`).join(values[m]);
  return out;
}

/**
 * Where GitHub's desktop packages of the last pushed source stand, from what
 * the registry and GitHub say: `source` (state.json last_source), `built` and
 * `attempted` (the revision annotations of the artifact's `nightly` and
 * `attempted` tags), `run` (the workflow's newest run: status, conclusion,
 * url). Returns null with no source, else { state, ... }:
 *   built        the packages of that revision are on ghcr
 *   building     the workflow took it and runs
 *   failed       the workflow took it and did not publish the packages
 *   waiting      not taken yet, within `graceH` hours (it runs hourly)
 *   not-started  not taken after `graceH` hours
 */
export function followVerdict({ source, built = null, attempted = null, run = null, nowMs, graceH = 3 }) {
  if (!source?.revision) return null;
  const base = { version: source.version, revision: source.revision };
  if (built?.revision === source.revision) return { ...base, state: 'built' };
  if (attempted?.revision === source.revision) {
    if (run && run.status !== 'completed') return { ...base, state: 'building', url: run.url ?? '' };
    return { ...base, state: 'failed', url: run?.url ?? '', conclusion: run?.conclusion ?? 'unknown', why: run?.conclusion === 'success' ? 'it ended green and published no packages' : '' };
  }
  const hours = source.at ? Math.floor((nowMs - Date.parse(source.at)) / 3_600_000) : null;
  return { ...base, state: hours !== null && hours >= graceH ? 'not-started' : 'waiting', hours };
}

/**
 * The morning report's lines for GitHub's desktop packages (followVerdict,
 * with what the follow-up did: `site` - the page published, kept or failed -
 * `rows` and `snapArm64` from the manifest, `edgeArm64` from the Store).
 */
export function followLines(f) {
  if (!f) return [];
  const lines = [];
  if (f.state === 'built') {
    const failed = Object.entries(f.rows ?? {}).filter(([, st]) => st !== 'built').map(([r]) => r);
    lines.push(`desktop packages ${f.version}: built on GitHub's runners${f.platforms?.length ? ` (${f.platforms.join(', ')})` : ''}`);
    if (failed.length) lines.push(`  WARNING not built this night: ${failed.join(', ')}${f.url ? ` - ${f.url}` : ''}`);
    if (f.site?.state === 'published') lines.push(`  filex.sh/nightly: ${f.site.detail}`);
    else if (f.site?.state === 'same') lines.push('  filex.sh/nightly already shows them');
    else if (f.site?.state === 'kept') lines.push(`  filex.sh/nightly NOT updated: ${f.site.detail}`);
    else if (f.site?.state === 'failed') lines.push(`  WARNING filex.sh/nightly NOT updated: ${f.site.detail}`);
  } else if (f.state === 'building') lines.push(`desktop packages ${f.version}: still being built on GitHub (${f.url}); the next report publishes them`);
  else if (f.state === 'failed') lines.push(`WARNING desktop packages ${f.version}: GitHub's build FAILED (${f.conclusion}${f.why ? `, ${f.why}` : ''})${f.url ? ` - ${f.url}` : ''}`);
  else if (f.state === 'waiting') lines.push(`desktop packages ${f.version}: GitHub takes the source within the hour`);
  else if (f.state === 'not-started') lines.push(`WARNING desktop packages ${f.version}: GitHub has not taken the source in ${f.hours} h (the workflow runs every hour; is it disabled?)`);
  else if (f.state === 'unknown') lines.push(`WARNING desktop packages: ${f.why}`);
  const s = f.snapArm64;
  if (s) {
    if (s.state === 'released') lines.push(`  snap ${f.version} on edge (arm64)${s.revision ? `: revision ${s.revision}` : ''}`);
    else if (s.state === 'skipped') lines.push(`  snap arm64: ${s.why}`);
    else if (s.state === 'review') lines.push(`  WARNING snap arm64: revision ${s.revision ?? '?'} uploaded and HELD for the Snap Store's review`);
    else lines.push(`  WARNING snap arm64 upload to edge FAILED: ${s.why ?? 'see the run'}`);
  }
  const el = snapEdgeLine(f.edgeArm64);
  if (el) lines.push(el);
  return lines;
}

/** Whether the follow-up is worth a warning. */
export const followTrouble = (f) =>
  !!f &&
  (['failed', 'not-started', 'unknown'].includes(f.state) ||
    Object.values(f.rows ?? {}).some((st) => st !== 'built') ||
    f.site?.state === 'failed' ||
    (f.snapArm64 && !['released', 'skipped'].includes(f.snapArm64.state)) ||
    f.edgeArm64?.state === 'held');

/**
 * How a publish signs in to the registry: `own` - a Docker config of its own,
 * logged in with NIGHTLY_REGISTRY_USER and the token in
 * NIGHTLY_REGISTRY_TOKEN_FILE and removed afterwards - when both are set;
 * `host` - the host's Docker config - when neither is.
 *
 * ⚠ One without the other is refused. With NIGHTLY_PUBLISH=auto every green
 * night pushes unattended; a token file without its user would quietly fall
 * back to whatever the host's config holds and push as somebody else, or fail
 * with an "unauthorized" that names neither setting.
 */
export function registryLogin({ registryUser = '', registryTokenFile = '' } = {}) {
  if (registryUser && registryTokenFile) return 'own';
  if (!registryUser && !registryTokenFile) return 'host';
  const missing = registryUser ? 'NIGHTLY_REGISTRY_TOKEN_FILE' : 'NIGHTLY_REGISTRY_USER';
  throw new Error(`${missing} is not set: a publish signs in with NIGHTLY_REGISTRY_USER and NIGHTLY_REGISTRY_TOKEN_FILE together, or with neither (the host's Docker config)`);
}

// ── what a night leaves ─────────────────────────────────────────────────────

/** The local date of `ms` in `tz`: YYYY-MM-DD. */
export function localDate(ms, tz = 'UTC') {
  const parts = Object.fromEntries(
    new Intl.DateTimeFormat('en-CA', { timeZone: tz, year: 'numeric', month: '2-digit', day: '2-digit' })
      .formatToParts(new Date(ms))
      .map((p) => [p.type, p.value]),
  );
  return `${parts.year}-${parts.month}-${parts.day}`;
}

/** The local time of `ms` in `tz`: HH:MM. */
export function localTime(ms, tz = 'UTC') {
  return new Intl.DateTimeFormat('en-GB', { timeZone: tz, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(new Date(ms));
}

function isoMinute(ms) {
  return new Date(ms).toISOString().slice(0, 16).replace('T', ' ') + ' UTC';
}

/** Each job's status in a result: { name: passed | failed | skipped | pending | running }. */
export function jobStatuses(result) {
  return Object.fromEntries((result?.jobs ?? []).map((j) => [j.name, j.status]));
}

/** The fields of a result a night's record keeps (history.jsonl stays small). */
export function resultFields(result) {
  if (!result) return { finished: false };
  return {
    finished: !!result.finished,
    ok: result.ok === true,
    stopped: !!result.stopped,
    secs: result.secs,
    wall: result.wall,
    counts: result.counts,
    jobs: jobStatuses(result),
    ...(result.host ? { host: hostFields(result.host) } : {}),
  };
}

/**
 * What a night keeps of its host (run.mjs result.json `host`, task #194): the
 * budget it ran on and the worst pressure it met - the night-by-night record
 * of whether the chain still stalls its host.
 */
export function hostFields(h) {
  return {
    budget_gb: h.budget_gb,
    mem_full_max: h.mem_full_max,
    io_full_max: h.io_full_max,
    disk_write_ms_max: h.disk_write_ms_max,
    // Whether run.sh's pause hook freed the host first: the pressure of a
    // paused night and of one that was not are not the same measurement.
    ...(h.pause?.status ? { pause: h.pause.status } : {}),
  };
}

/**
 * What run.sh's pause hook (CHAIN_PAUSE_CMD) did before the run, in the words
 * of chain.log and the morning report; '' when none ran. `p`: result.json
 * `host.pause`, { status: paused|failed|timeout, exit, secs }.
 */
export function pauseWords(p) {
  if (!p?.status) return '';
  const secs = Number.isFinite(p.secs) ? ` after ${p.secs} s` : '';
  if (p.status === 'paused') return `pause hook done${secs}, before the budget was measured`;
  const what = p.status === 'timeout' ? 'timed out (CHAIN_HOOK_TIMEOUT_S)' : `failed (exit ${Number.isFinite(p.exit) ? p.exit : '?'})`;
  return `pause hook ${what}${secs}: the run went on beside what it was to pause, and nothing was resumed`;
}

/**
 * The marks a host stall leaves on a job (run.mjs records them per job,
 * result.json `load`; the morning report names them): memory or IO "full"
 * over these for ten seconds (lesson #1222: at 0.3 everything runs 20-60
 * times slower), or a write that took a second or more on the run's disk
 * (lesson #1232: a disk that stops for 15 s freezes every fsync).
 */
export const STALL = { memFull: 0.3, ioFull: 0.5, diskWriteMs: 1000 };

/** What of a job's `load` crossed STALL, in words ("memory full 0.62, disk writes 10256 ms"), or ''. */
export function stallWords(load) {
  if (!load) return '';
  const out = [];
  if (load.host_mem_full >= STALL.memFull) out.push(`memory full ${load.host_mem_full.toFixed(2)}`);
  if (load.host_io_full >= STALL.ioFull) out.push(`io full ${load.host_io_full.toFixed(2)}`);
  if (load.disk_write_ms >= STALL.diskWriteMs) out.push(`disk writes ${load.disk_write_ms} ms`);
  return out.join(', ');
}

/** The report's line on the host: the budget and the worst pressure of the run, or null without a `host` in the result. */
export function hostLine(h) {
  if (!h) return null;
  const budget = h.budget_cut
    ? `budget ${h.budget_gb} of ${h.configured_gb} GiB (MemAvailable ${h.mem_available_start_gb} GiB at the start${h.outside ? `; outside the chain: ${h.outside}` : ''})`
    : `budget ${h.budget_gb} GiB`;
  const peaks = [
    Number.isFinite(h.mem_full_max) ? `memory full ${h.mem_full_max.toFixed(2)}` : null,
    Number.isFinite(h.io_full_max) ? `io full ${h.io_full_max.toFixed(2)}` : null,
    Number.isFinite(h.disk_write_ms_max) ? `disk writes ${Math.round(h.disk_write_ms_max)} ms${h.disk ? ` (${h.disk})` : ''}` : null,
  ].filter(Boolean);
  const temp = h.temp_wait_secs > 0 ? `; waited ${Math.round(h.temp_wait_secs / 60)} min for the disk to cool` : '';
  // run.mjs trackDisk (#194): a heavy browser line waiting for the pool's heavy writers.
  const io = h.io_wait_secs > 0 ? `; the browser round waited ${Math.round(h.io_wait_secs / 60)} min for the disk` : '';
  const pause = pauseWords(h.pause);
  return `host: ${budget}${peaks.length ? `; worst ${peaks.join(', ')}` : ''}${temp}${io}${pause ? `; ${pause}` : ''}`;
}

/** What history.jsonl keeps of a night: the record without the night's working fields. */
export function nightRecord(t) {
  const b = t.build;
  return {
    night: t.night,
    at: t.at,
    ended: t.ended,
    decision: t.decision,
    why: t.why,
    sha: t.sha,
    subject: t.subject,
    prev_sha: t.prev_sha,
    run_id: t.run_id,
    result: t.result,
    exit: t.exit,
    finished: t.finished,
    ok: t.ok,
    stopped: t.stopped,
    stoppedFor: t.stoppedFor,
    secs: t.secs,
    wall: t.wall,
    counts: t.counts,
    jobs: t.jobs,
    host: t.host,
    extras: t.extras,
    s3live: t.s3live ? { status: t.s3live.status, since: t.s3live.since, why: t.s3live.why } : undefined,
    build: b
      ? { decision: b.decision, why: b.why, image: b.image, version: b.version, tag: b.tag, sha: b.sha, published: b.published, publishError: b.publishError, log: b.log }
      : undefined,
    desktop: t.desktop && t.desktop.decision !== 'skipped'
      ? { decision: t.desktop.decision, version: t.desktop.version, targets: t.desktop.targets, dir: t.desktop.dir, snap: t.desktop.snap, source: t.desktop.source }
      : undefined,
    snapEdge: t.snapEdge?.state === 'held' ? { version: t.snapEdge.version, revision: t.snapEdge.revision, since: t.snapEdge.since } : undefined,
  };
}

/** The run directories to remove: every nightly-* one but the newest `keep` and `protect`. */
export function runsToPrune(names, keep, protect = []) {
  const runs = names.filter((n) => /^nightly-\d{8}-\d{6}Z-[0-9a-z]+$/.test(n)).sort().reverse();
  return runs.slice(keep).filter((n) => !protect.includes(n));
}

/** The local image tags to remove: all but the `keep` newest (`tags` newest first) and `protect`. */
export function imagesToPrune(tags, keep, protect = []) {
  return tags.filter((t) => /^[0-9a-f]{8}$/.test(t)).slice(keep).filter((t) => !protect.includes(t));
}

/** The run id the nightly run gives the chain (run.mjs --run-id), sortable by time. */
export function nightlyRunId(nowMs, sha) {
  const stamp = new Date(nowMs).toISOString().replace(/[-:]/g, '').replace(/\..*/, '').replace('T', '-');
  return `nightly-${stamp}Z-${String(sha).slice(0, 8)}`;
}

// ── the morning report ──────────────────────────────────────────────────────

/** Changes between two nights' job statuses. */
export function jobsDiff(prevJobs = {}, jobs = {}) {
  const red = (s) => s === 'failed' || s === 'skipped';
  const names = Object.keys(jobs);
  return {
    newRed: names.filter((n) => red(jobs[n]) && !red(prevJobs[n])),
    stillRed: names.filter((n) => red(jobs[n]) && red(prevJobs[n])),
    fixed: Object.keys(prevJobs).filter((n) => red(prevJobs[n]) && jobs[n] === 'passed'),
  };
}

/** The last night (before index `before`) on which `job` passed: its record, or null. */
export function lastGreenOf(history, job, before = history.length) {
  for (let i = Math.min(before, history.length) - 1; i >= 0; i -= 1) {
    const r = history[i];
    if (r.decision === 'ran' && r.jobs?.[job] === 'passed') return r;
  }
  return null;
}

function signedMinutes(secs) {
  const m = Math.round(secs / 60);
  return `${m >= 0 ? '+' : '-'}${Math.abs(m)} min`;
}

function buildLine(b) {
  if (!b) return null;
  if (b.decision === 'built') {
    const where = b.published
      ? `published as ${b.published.refs.join(', ')}`
      : b.publishError
        ? `publishing FAILED: ${b.publishError}`
        : b.publishHint
          ? `not published (NIGHTLY_PUBLISH=${b.publishMode ?? 'manual'}): ${b.publishHint}`
          : 'not published';
    return `nightly build: ${b.image} (${b.version}), ${where}`;
  }
  if (b.decision === 'failed') return `nightly build FAILED: ${b.why}${b.log ? ` (log ${b.log})` : ''}`;
  return `nightly build: none - ${b.why}`;
}

const SEVERITY_RANK = { info: 0, success: 1, warning: 2, danger: 3 };
const worse = (a, b) => (SEVERITY_RANK[b] > SEVERITY_RANK[a] ? b : a);
const buildTrouble = (b) => b?.decision === 'failed' || !!b?.publishError;

/**
 * The morning report's lines for the night's desktop side (#266): the amd64
 * snap's upload to edge and the source pushed for GitHub's runners (what they
 * made of it is followLines'). None when the night built nothing for the
 * desktop because it made no nightly build (the build line already says
 * why); one line saying why when a build was made and nothing was due
 * (`asked`: the settings ask for a desktop side at all).
 */
export function desktopLines(d) {
  if (!d || d.decision === 'skipped') return d?.decision === 'skipped' && d.asked ? [`desktop: none - ${d.why}`] : [];
  const lines = [];
  const targets = Object.entries(d.targets ?? {}).map(([t, s]) => `${t} ${s}`).join(', ');
  if (d.decision === 'failed' && !Object.keys(d.targets ?? {}).length) {
    lines.push(`desktop FAILED: ${d.why}${d.log ? ` (log ${d.log})` : ''}`);
    return lines;
  }
  lines.push(`desktop ${d.version}: ${targets}${d.decision === 'built' ? '' : ` - ${d.why}${d.log ? ` (log ${d.log})` : ''}`}`);
  if (d.source?.ref) lines.push(`  the source for GitHub's desktop packages: ${d.source.ref}`);
  else if (d.source?.error) lines.push(`  WARNING the source for GitHub's desktop packages was NOT pushed: ${d.source.error}`);
  const s = d.snap;
  if (s) {
    if (s.state === 'released') lines.push(`  snap ${d.version} on edge (amd64)${s.revision ? `: revision ${s.revision}` : ''}`);
    else if (s.state === 'review') lines.push(`  WARNING snap: revision ${s.revision ?? '?'} uploaded and HELD for the Snap Store's review - edge keeps its old revision until it passes (snapcraft status ${s.name ?? 'filex-app'})`);
    else if (s.state === 'skipped') lines.push(`  snap: not uploaded - ${s.why}`);
    else lines.push(`  WARNING snap upload to edge FAILED: ${s.why ?? 'see the log'}`);
  }
  return lines;
}

/** The report's line on an earlier nightly snap edge still does not serve (snapEdgeState), or null. */
export function snapEdgeLine(e) {
  if (!e || e.state !== 'held') return null;
  const ago = Number.isFinite(e.days) ? `${e.days} day(s) ago` : 'earlier';
  return `WARNING snap: edge (${e.arch ?? 'amd64'}) serves ${e.edge}, not the nightly ${e.version} uploaded ${ago}${e.revision ? ` (revision ${e.revision})` : ''}: the Snap Store still holds it - snapcraft status filex-app`;
}

const desktopTrouble = (d) =>
  !!d &&
  (d.decision === 'failed' ||
    d.decision === 'partial' ||
    !!d.source?.error ||
    (d.snap && !['released', 'skipped'].includes(d.snap.state)));

/** The morning report's line for s3-live (decideS3Live), or null when there is none to say. */
function s3LiveLine(s) {
  if (!s) return null;
  if (s.status === 'unchanged') return `s3-live: skipped - ${s.why}`;
  if (s.status === 'no-credentials') return `WARNING s3-live: ${s.why}.${s.hint ? ` Test it by hand: ${s.hint}` : ''}`;
  return null;
}
const s3LiveTrouble = (s) => s?.status === 'no-credentials';

/**
 * The morning report: one notification for the night.
 *
 *   tonight   the night's record (nightly.mjs writes tonight.json), or null
 *   result    its result.json when it ran (final, or as far as it got)
 *   history   the earlier nights' records, oldest first (history.jsonl);
 *             tonight's is not in it when the night is still going
 *   commits   (from, to) => [{ sha, subject }], newest first: git log from..to
 *   running   whether the nightly run that wrote `tonight` is still alive
 *   nowMs, tz, maxAgeH (older than that, tonight is not tonight)
 *
 * Returns { severity, title, message }: severity success (green), danger
 * (red), warning (stopped, still going or gone, not run, no record, a failed
 * build or publish, a green job's warnings) or info (main did not move).
 */
export function composeReport({
  tonight,
  result = null,
  history = [],
  commits = () => [],
  running = true,
  nowMs,
  tz = 'UTC',
  maxAgeH = 20,
  maxCommits = 8,
  maxJobs = 25,
  follow = null,
}) {
  const lines = [];
  const at = tonight ? Date.parse(tonight.at) : NaN;
  if (!tonight || !Number.isFinite(at) || nowMs - at > maxAgeH * 3_600_000) {
    const last = history.at(-1);
    return {
      severity: 'warning',
      title: `filex nightly ${localDate(nowMs, tz)}: no run recorded`,
      message: [
        `The nightly run left no record in the last ${maxAgeH} h: its timer did not fire, or it died before it wrote one.`,
        last ? `Last record: ${last.night} ${last.decision} on ${short(last.sha)}${last.why ? ` (${last.why})` : ''}.` : 'There is no earlier record either.',
        'Look at: journalctl -u filex-nightly.service',
      ].join('\n'),
    };
  }
  const night = tonight.night ?? localDate(at, tz);
  // The night before tonight that ran: tonight's record may already be the last line.
  const own = history.findIndex((r) => r.run_id && r.run_id === tonight.run_id && r.night === tonight.night);
  const before = own >= 0 ? own : history.length;
  const prev = lastRan(history, before);
  const head = `main ${short(tonight.sha)}${tonight.subject ? ` ${tonight.subject}` : ''}`;

  if (tonight.phase && tonight.phase !== 'done') {
    const c = result?.counts;
    const how = running
      ? `still going at ${localTime(nowMs, tz)}: ${tonight.phase}`
      : `the nightly run is gone (pid ${tonight.pid ?? '?'}) and left the night at "${tonight.phase}": it died or was killed - journalctl -u filex-nightly.service`;
    lines.push(head, `started ${localTime(at, tz)} (${tz}), ${how}`);
    if (c) lines.push(`so far ${c.passed} passed, ${c.failed} red, ${c.skipped} skipped of ${c.total}`);
    for (const j of (result?.jobs ?? []).filter((x) => x.status === 'failed').slice(0, maxJobs)) lines.push(`RED ${j.name}: ${j.summary ?? ''}`, `  log ${j.log ?? '?'}`);
    if (tonight.result) lines.push(`result ${tonight.result}`);
    return {
      severity: 'warning',
      title: `filex nightly ${night}: ${running ? 'still running' : `died while ${tonight.phase}`} (${short(tonight.sha)})`,
      message: finish(lines),
    };
  }

  if (tonight.decision === 'skipped') {
    lines.push(tonight.why ?? 'main has not moved');
    if (prev) lines.push(`last run: ${prev.night} on ${short(prev.sha)}, ${prev.ok ? 'green' : 'RED'}${prev.wall ? ` in ${prev.wall}` : ''}`);
    const bl = buildLine(tonight.build);
    if (bl) lines.push(bl);
    // A snap the Store still holds is said every morning, a quiet one too.
    const el = snapEdgeLine(tonight.snapEdge);
    if (el) lines.push(el);
    // So is GitHub's work on the last source (#266).
    lines.push(...followLines(follow));
    return {
      severity: buildTrouble(tonight.build) || el || followTrouble(follow) ? 'warning' : 'info',
      title: `filex nightly ${night}: no run, main unchanged (${short(tonight.sha)})`,
      message: finish(lines),
    };
  }

  if (tonight.decision !== 'ran') {
    lines.push(head, tonight.why ?? '');
    if (prev) lines.push(`last run: ${prev.night} on ${short(prev.sha)}, ${prev.ok ? 'green' : 'RED'}`);
    return { severity: 'warning', title: `filex nightly ${night}: did not run (${tonight.decision})`, message: finish(lines) };
  }

  // It ran.
  lines.push(head);
  if (prev && prev.sha && prev.sha !== tonight.sha) {
    const list = safeCommits(commits, prev.sha, tonight.sha);
    lines.push(`since the last run (${prev.night}, ${short(prev.sha)}): ${list === null ? 'the range is not in this checkout' : `${list.length} commit(s)`}`);
    for (const c of (list ?? []).slice(0, maxCommits)) lines.push(`  ${short(c.sha)} ${c.subject}`);
    if (list && list.length > maxCommits) lines.push(`  ... and ${list.length - maxCommits} more`);
  } else if (!prev) {
    lines.push('the first nightly run');
  }
  if (!result || !result.finished) {
    lines.push(`the run left no final result (exit ${tonight.exit ?? '?'})${tonight.result ? `: ${tonight.result}` : ''}`);
    const bl = buildLine(tonight.build);
    if (bl) lines.push(bl);
    return { severity: 'warning', title: `filex nightly ${night}: no result (${short(tonight.sha)})`, message: finish(lines) };
  }
  const c = result.counts ?? {};
  const delta = prev && Number.isFinite(prev.secs) && Number.isFinite(result.secs) ? `, previous ${prev.wall} (${signedMinutes(result.secs - prev.secs)})` : '';
  lines.push(`wall ${result.wall}${delta} · ${c.passed} passed, ${c.failed} red, ${c.skipped} skipped of ${c.total}`);
  if (tonight.stoppedFor) lines.push(`stopped: ${tonight.stoppedFor}`);
  const hl = hostLine(result.host);
  if (hl) lines.push(hl);

  const jobs = jobStatuses(result);
  const diff = jobsDiff(prev?.jobs ?? {}, jobs);
  const bad = (result.jobs ?? []).filter((j) => j.status === 'failed' || j.status === 'skipped');
  // Red jobs grouped by the night they last passed: one commit range each.
  // The red ones first: a skipped job only says that a job it needed failed.
  const shown = [...bad.filter((j) => j.status === 'failed'), ...bad.filter((j) => j.status !== 'failed')].slice(0, maxJobs);
  const groups = new Map();
  for (const j of shown) {
    const green = lastGreenOf(history, j.name, before);
    const key = green ? green.sha : '';
    if (!groups.has(key)) groups.set(key, { green, jobs: [] });
    groups.get(key).jobs.push(j);
  }
  for (const { green, jobs: group } of groups.values()) {
    for (const j of group) {
      const since = diff.newRed.includes(j.name) ? 'new tonight' : prev ? `red on ${prev.night} too` : 'red';
      lines.push(`${j.status === 'skipped' ? 'SKIPPED' : 'RED'} ${j.name} (${since}): ${j.summary ?? ''}`);
      if (j.log) lines.push(`  log ${j.log}`);
      const stalled = stallWords(j.load);
      if (stalled) lines.push(`  the host stalled while it ran (${stalled}): read a timeout there as the host's before the test's`);
    }
    if (green && green.sha !== tonight.sha) {
      const list = safeCommits(commits, green.sha, tonight.sha);
      lines.push(
        `  last green ${green.night} on ${short(green.sha)}; the range ${short(green.sha)}..${short(tonight.sha)}: ${list === null ? 'not in this checkout' : `${list.length} commit(s)`}`,
      );
      for (const cm of (list ?? []).slice(0, maxCommits)) lines.push(`    ${short(cm.sha)} ${cm.subject}`);
      if (list && list.length > maxCommits) lines.push(`    ... and ${list.length - maxCommits} more`);
    } else if (green) {
      lines.push(`  green on ${green.night} on this very commit: not a change, look for a flaky test or the host`);
    } else {
      lines.push('  never green in the nights on record');
    }
  }
  if (bad.length > shown.length) lines.push(`... and ${bad.length - shown.length} more red or skipped job(s): see the result`);
  // What a green job wants read (job/common.sh warn, run.mjs jobWarnings):
  // the shots job when its only failed scene is a language pack behind the
  // tree (#187). Not red, but not a green night either.
  const warned = (result.jobs ?? []).filter((j) => j.warnings?.length);
  for (const j of warned) for (const w of j.warnings) lines.push(`WARNING ${j.name}: ${w}`);
  if (diff.fixed.length) lines.push(`fixed since ${prev?.night ?? 'the last run'}: ${diff.fixed.join(', ')}`);
  const sl = s3LiveLine(tonight.s3live);
  if (sl) lines.push(sl);
  const bl = buildLine(tonight.build);
  if (bl) lines.push(bl);
  lines.push(...desktopLines(tonight.desktop));
  const el = snapEdgeLine(tonight.snapEdge);
  if (el) lines.push(el);
  lines.push(...followLines(follow));
  if (tonight.result) lines.push(`result ${tonight.result}`);
  if (result.ok) lines.push(`release evidence: pnpm release <version> --resume --chain <a copy of ${tonight.latest ?? 'runs/latest-nightly.json'}>`);

  let severity = result.ok ? 'success' : result.stopped ? 'warning' : 'danger';
  if (buildTrouble(tonight.build)) severity = worse(severity, 'warning');
  if (desktopTrouble(tonight.desktop) || el || followTrouble(follow)) severity = worse(severity, 'warning');
  if (s3LiveTrouble(tonight.s3live)) severity = worse(severity, 'warning');
  if (warned.length) severity = worse(severity, 'warning');
  const verdict = result.ok
    ? warned.length
      ? `green, ${warned.length} job(s) with a warning`
      : desktopTrouble(tonight.desktop) || el || followTrouble(follow)
        ? 'green, the desktop packages or the snap with a warning'
        : 'green'
    : result.stopped
      ? 'stopped'
      : `RED ${bad.length} of ${c.total}`;
  return {
    severity,
    title: `filex nightly ${night}: ${verdict} (${short(tonight.sha)}, ${result.wall})`,
    message: finish(lines),
  };
}

function safeCommits(commits, from, to) {
  try {
    return commits(from, to);
  } catch {
    return null;
  }
}

/** The notification's text: Notify keeps up to 8000 characters. */
function finish(lines, max = 7900) {
  const text = lines.filter((l) => l !== null && l !== undefined).join('\n');
  return text.length > max ? `${text.slice(0, max - 20)}\n... (cut, see result)` : text;
}
