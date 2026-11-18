// The decisions of the nightly (scripts/chain/nightly.mjs) and of its
// morning report (scripts/chain/report.mjs), as pure functions: no process,
// no network, no clock and no file of their own - the callers read and pass
// them. web/tests/deploy/chainNightly.test.ts holds them.
//
// Task #361 (2026-11-18). The night, in order:
//   02:00  nightly.mjs fetches the private main into its own checkout. When
//          it has not moved since the last night that pushed, nothing happens
//          (decidePush); while GitHub tests a release, the night waits for the
//          next one (releaseBusy). Otherwise the tip is exported the way a
//          release is (scripts/export-public.sh, never the private tree),
//          committed on top of the public main as ONE commit
//          (nightlyCommitMessage) and force-pushed to the public repository's
//          `nightly` branch. That is all the build host does at night.
//   then   on GitHub: ci.yml runs its full matrix on the push; green, the
//          nightly workflow (.github/workflows/nightly-desktop.yml, on
//          workflow_run) builds the nightly image (ghcr.io/brf-tech/filex:nightly,
//          nightlyMeta, nightlyLabels) and every desktop package - Windows,
//          macOS, Linux - and both snaps for the Snap Store's edge channel.
//   07:00  report.mjs: GitHub's verdict on the commit (ciVerdict: `All tests
//          (full)` and every part), the image and the desktop packages
//          followed up (followVerdict), the filex.sh/nightly page made from
//          them (renderNightlyPage), and one notification for the night
//          (composeReport).
//
// Until 2026-11-18 the build host ran the whole test chain every night
// (run.sh --profile nightly, three to four hours), then built the image, the
// amd64 snap and a source archive for GitHub itself. The chain's nightly
// profile is still there for a person to run (plan.mjs, below); nothing
// starts it on a timer any more.

import path from 'node:path';

const DAY_MS = 86_400_000;
const short = (sha) => String(sha ?? '').slice(0, 8) || '?';

// ── the extras of the chain's nightly profile ───────────────────────────────

/**
 * What the chain's nightly profile (run.sh --profile nightly, a person's run
 * on the build host since #361) runs on top of the full one, in the browser
 * round (scripts/chain/plan.mjs). None of them is in the full chain or in
 * GitHub's matrix, each for its own reason:
 *
 *   ds-go    the Go test that needs a real ONLYOFFICE Document Server and the
 *            Convert app (backend/internal/server/office_engine_ds_test.go):
 *            it skips everywhere else. It runs right after the last line
 *            with the Document Server, which is still up for it.
 *   s3-live  the Go tests that talk to a REAL S3 provider (FILEX_TEST_S3_*,
 *            backend/internal/testutil/lives3), against the bucket the build
 *            host's settings name (S3_LIVE_KEYS), never GitHub's. When it
 *            runs, a test that only skipped is red.
 *   shots    `pnpm shots --all`, every scene - the app scenes with the app
 *            builds and language packs the chain mounts, the ONLYOFFICE scene
 *            against the chain's Document Server (task #187) - each picture
 *            compared with the published one. It runs right after ds-go,
 *            while the Document Server is still up. A run whose only failed
 *            scene is a language pack behind the tree is a warning, not red
 *            (SHOTS_PACKS_BEHIND=warn; the maintainer, 2026-10-08).
 *   realenv  e2e/realenv/run.sh, filex against the real ACME, SSO and office
 *            servers. It starts containers of its own, so it runs on the host,
 *            not in a job container.
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
 * (backend/internal/testutil/lives3), set in the chain's settings file
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
 * The code the live S3 tests measure. Nothing runs them on its own since
 * #361 (GitHub has no bucket of ours, and the build host runs no tests at
 * night), so the night says it when a change it pushes touches one of these:
 * the morning report warns, names the files and the command that runs the
 * tests by hand. Read off what the four tests and the driver import:
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

/** The extras a nightly-profile run adds: CHAIN_NIGHTLY_EXTRAS, else all of them, always in NIGHTLY_EXTRAS order. */
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

// ── settings ────────────────────────────────────────────────────────────────

/** "Name <address>", the form git and NIGHTLY_COMMIT_AUTHOR take: { name, email }, or null. */
export function parseAuthor(text) {
  const m = /^\s*([^<>\n]*[^<>\s])\s*<([^<>\s@]+@[^<>\s]+)>\s*$/.exec(String(text ?? ''));
  return m ? { name: m[1], email: m[2] } : null;
}

/**
 * The nightly's settings, from the same KEY=VALUE file the chain reads
 * (scripts/chain/env.mjs). CHAIN_ROOT is the nightly's own directory - its
 * checkout of the private repository, its clone of the public one, its state.
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
  if (!env.CHAIN_ROOT) throw new Error('CHAIN_ROOT is not set: the nightly keeps its checkouts and state there');
  if (env.NIGHTLY_COMMIT_AUTHOR && !parseAuthor(env.NIGHTLY_COMMIT_AUTHOR)) {
    throw new Error(`NIGHTLY_COMMIT_AUTHOR=${env.NIGHTLY_COMMIT_AUTHOR}: "Name <address>"`);
  }
  const root = path.resolve(env.CHAIN_ROOT);
  return {
    root,
    state: path.join(root, 'nightly'),
    src: path.resolve(env.NIGHTLY_SRC || path.join(root, 'src')),
    remote: env.NIGHTLY_REMOTE_NAME || 'origin',
    branch: env.NIGHTLY_BRANCH || 'main',
    keepHistory: num('NIGHTLY_KEEP_HISTORY', 120),
    tz: env.NIGHTLY_TZ || 'UTC',
    push: {
      // The public repository, the clone of it the export is made in, and the
      // branch the night's commit replaces.
      remote: env.NIGHTLY_PUBLIC_REMOTE || 'https://github.com/BRF-Tech/filex.git',
      publicDir: path.resolve(env.NIGHTLY_PUBLIC_DIR || path.join(root, 'public')),
      branch: env.NIGHTLY_PUSH_BRANCH || 'nightly',
      // Who the commit is by: the account the public repository's commits
      // are by. Required for a push; the code names nobody.
      author: env.NIGHTLY_COMMIT_AUTHOR || '',
      // Where the push is made from. Empty: from the clone here, with this
      // host's own git credential. A container (the release workspace): the
      // commit goes in as a bundle, and the push is made there by
      // scripts/chain/nightly-push.sh under NIGHTLY_PUSH_WRAP - the command
      // that hands git the workspace's GitHub token for that one push, so
      // the token never leaves the workspace.
      container: env.NIGHTLY_PUSH_CONTAINER || '',
      user: env.NIGHTLY_PUSH_USER || '',
      wrap: 'NIGHTLY_PUSH_WRAP' in env ? String(env.NIGHTLY_PUSH_WRAP ?? '') : 'with-gh-token',
      // Inside the container: its clone of the public repository, and the
      // private checkout the export gate reads its lists from (empty: the
      // script's defaults, ~/.cache/filex-nightly/public and ~/filex).
      dir: env.NIGHTLY_PUSH_DIR || '',
      gateSrc: env.NIGHTLY_PUSH_GATE_SRC || '',
      // skip: no push while GitHub tests a release (releaseBusy); push: push anyway.
      busy: oneOf('NIGHTLY_RELEASE_BUSY', 'skip', ['skip', 'push']),
    },
    github: {
      repo: env.NIGHTLY_GITHUB_REPO || 'BRF-Tech/filex',
      ci: env.NIGHTLY_CI_WORKFLOW || 'ci.yml',
      // A token for GitHub's API, read for the rate limit only (any scope:
      // everything read is public). Without one, the API's anonymous limit.
      tokenFile: env.NIGHTLY_GITHUB_TOKEN_FILE || '',
    },
    image: { repo: env.NIGHTLY_IMAGE_REPO || 'ghcr.io/brf-tech/filex' },
    snap: { name: env.NIGHTLY_SNAP_NAME || 'filex-app' },
    desktop: {
      // github: the morning follows GitHub's desktop packages of the last
      // nightly up and puts them on filex.sh/nightly; off: neither.
      mode: oneOf('NIGHTLY_DESKTOP', 'github', ['github', 'off']),
      repo: env.NIGHTLY_DESKTOP_REPO || 'ghcr.io/brf-tech/filex-desktop-nightly',
      workflowRepo: env.NIGHTLY_DESKTOP_WORKFLOW_REPO || env.NIGHTLY_GITHUB_REPO || 'BRF-Tech/filex',
      workflow: env.NIGHTLY_DESKTOP_WORKFLOW || 'nightly-desktop.yml',
      // The ORAS client, run as a container: it reads the OCI artifacts.
      // Pinned, as every image the chain runs.
      oras: env.NIGHTLY_ORAS_IMAGE || 'ghcr.io/oras-project/oras:v1.2.2',
      // A command that publishes the filex.sh/nightly page and its packages,
      // given a directory with index.html and nightly.json as its one
      // argument (sh -c '<command> "$1"'), NIGHTLY_SITE_REF naming the
      // artifact (scripts/chain/nightly-site.sh). Empty: the page is made and
      // kept here, and the morning report says it was not published.
      siteCmd: env.NIGHTLY_SITE_CMD || '',
      out: path.resolve(env.NIGHTLY_DESKTOP_DIR || path.join(root, 'desktop')),
    },
    notify: {
      url: env.CHAIN_NOTIFY_URL || '',
      keyFile: env.CHAIN_NOTIFY_KEY_FILE || '',
      group: env.NIGHTLY_NOTIFY_GROUP || env.CHAIN_NOTIFY_GROUP || 'infra',
      source: env.NIGHTLY_NOTIFY_SOURCE || 'filex-nightly',
    },
    report: {
      waitMin: num('NIGHTLY_REPORT_WAIT_MIN', 30),
      maxAgeH: num('NIGHTLY_REPORT_MAX_AGE_H', 20),
    },
  };
}

// ── the night ───────────────────────────────────────────────────────────────

/**
 * Whether tonight pushes. `last` is what the last night that went through
 * left (state.json `last_export`: the private commit it exported, pushed or
 * found equal to the public main), or null. A night whose export was refused
 * or whose push failed leaves nothing, so the next one tries again.
 */
export function decidePush({ head, last = null, force = false }) {
  if (force) return { push: true, why: 'forced (--force)' };
  if (!last?.sha) return { push: true, why: 'the first nightly' };
  if (last.sha !== head) return { push: true, why: `main moved: ${short(last.sha)}..${short(head)}` };
  return { push: false, why: `main has not moved since the last nightly (${short(head)})` };
}

/**
 * A release GitHub is testing now, from the repository's runs that are not
 * over (the Actions API, `status` in_progress and queued): release.yml, or
 * ci.yml on a push of main - the run a release's gate waits for. The night
 * does not push then (NIGHTLY_RELEASE_BUSY=skip): its full matrix would share
 * the organisation's 20 jobs at once with the release's. Returns { url, why }
 * or null.
 *
 * A run created more than `maxAgeH` hours before `nowMs` is no release being
 * tested: GitHub can leave a run `queued` for good (2026-11-18: a release.yml
 * run of v0.10.1, queued since 2026-08-06, held the first night back), and a
 * release's gate does not wait half a day.
 */
export function releaseBusy(runs = [], { nowMs = null, maxAgeH = 12 } = {}) {
  for (const r of runs ?? []) {
    if (!r || r.status === 'completed') continue;
    const created = Date.parse(r.created_at ?? '');
    if (nowMs !== null && Number.isFinite(created) && nowMs - created > maxAgeH * 3_600_000) continue;
    const file = String(r.path ?? '').replace(/@.*$/, '').split('/').pop();
    if (file === 'release.yml') return { url: r.html_url ?? '', why: `release.yml runs (${r.display_title || r.head_branch || '?'})` };
    if (file === 'ci.yml' && r.event === 'push' && r.head_branch === 'main') {
      return { url: r.html_url ?? '', why: `ci.yml tests a push of main (${short(r.head_sha)})` };
    }
  }
  return null;
}

/**
 * The night's commit message. It names the night and the public commit it
 * is made on - never the private commit it was exported from (an open tree
 * does not name what is not open; lesson #55). The `Nightly-Date:` trailer
 * is read back on GitHub (nightlyMeta): the night, in the build host's time
 * zone, for the version and the tags.
 */
export function nightlyCommitMessage({ date, base }) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(String(date))) throw new Error(`"${date}" is not a date (YYYY-MM-DD)`);
  return [
    `nightly ${date}: the tip of main, as the next release would export it`,
    '',
    `The development branch on ${date}, exported the way a release is, as one`,
    `commit on top of ${short(base)}. Not a release: ci.yml tests it here, and a`,
    'green run builds the nightly image and desktop packages from it. This',
    'branch is replaced every night.',
    '',
    `Nightly-Date: ${date}`,
    '',
  ].join('\n');
}

/**
 * The version a nightly reports: the next minor after the released one
 * (packages/core/package.json), as a pre-release of it, with the commit as
 * build metadata - `0.53.0-nightly.20261007+1a2b3c4d`. It sorts above every
 * 0.52.x and below 0.53.0, so an install on it is offered 0.53.0 when it ships
 * (backend/internal/update/semver.go reads the pre-release and drops the
 * metadata). The commit is the public nightly commit.
 */
export function nightlyVersion(released, ymd, sha) {
  const m = /^v?(\d+)\.(\d+)\.(\d+)$/.exec(String(released ?? '').trim());
  if (!m) throw new Error(`"${released}" is not a released version (x.y.z)`);
  if (!/^\d{8}$/.test(String(ymd))) throw new Error(`"${ymd}" is not a date (YYYYMMDD)`);
  if (!/^[0-9a-f]{8,40}$/.test(String(sha))) throw new Error(`"${sha}" is not a commit`);
  return `${m[1]}.${Number(m[2]) + 1}.0-nightly.${ymd}+${sha.slice(0, 8)}`;
}

/** The tags a nightly image goes out under: the moving one, and the night's. */
export function nightlyTags(ymd) {
  return ['nightly', `nightly-${ymd}`];
}

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
 * What GitHub's nightly workflow needs of a nightly commit
 * (scripts/chain/nightly-meta.mjs reads them from the checkout): `released`
 * the version in packages/core/package.json, `sha` the commit, `message` its
 * message (the night: its `Nightly-Date:` trailer), `committedMs` its commit
 * time (the night when there is no trailer: a commit pushed by hand).
 * Returns { revision, version, desktop_version, date, ymd, tags }.
 */
export function nightlyMeta({ released, sha, message = '', committedMs = NaN }) {
  const trailer = /^Nightly-Date:\s*(\d{4}-\d{2}-\d{2})\s*$/m.exec(String(message))?.[1];
  const date = trailer ?? (Number.isFinite(committedMs) ? new Date(committedMs).toISOString().slice(0, 10) : '');
  if (!date) throw new Error('the commit names no night (no Nightly-Date: trailer, no commit time)');
  const ymd = date.replace(/-/g, '');
  const version = nightlyVersion(released, ymd, sha);
  return { revision: sha, version, desktop_version: desktopVersion(version), date, ymd, tags: nightlyTags(ymd).join(',') };
}

/**
 * The labels a nightly image carries besides its version and commit (#266):
 * where its source is, what it is, and its test result - so `docker inspect`
 * on a pulled `:nightly` says which commit it is and whether GitHub's CI was
 * green on it, with no account and no other page.
 *
 *   counts   the CI run's jobs: { passed, skipped, total } (ciVerdict)
 *   run      the CI run's page
 *
 * One `key=value` a label, as the workflow passes them to docker build.
 */
export function nightlyLabels({ counts = null, run = '', source = 'https://github.com/BRF-Tech/filex' } = {}) {
  const tests = counts && Number.isFinite(counts.total) ? `green: ${counts.passed} passed, ${counts.skipped ?? 0} skipped of ${counts.total} jobs${run ? ` (${run})` : ''}` : '';
  const labels = {
    'org.opencontainers.image.source': source,
    'org.opencontainers.image.description': 'filex nightly build: the tip of main after a green CI run. Not a release and not supported - for trying what the next release brings.',
    ...(tests ? { 'sh.filex.nightly.tests': tests } : {}),
  };
  for (const [k, v] of Object.entries(labels)) if (/[\r\n]/.test(v)) throw new Error(`label ${k} holds a line break`);
  return labels;
}

// ── GitHub's verdict on the night ───────────────────────────────────────────

const JOB_STATE = { success: 'passed', skipped: 'skipped', neutral: 'skipped', cancelled: 'cancelled' };

/**
 * What GitHub's CI said of the nightly commit: `run` the newest ci.yml run of
 * its push (the Actions API's workflow run, or null) and `jobs` its jobs
 * ({ name, conclusion, html_url }, every page). The verdict is the `All tests
 * (full)` job's - the one a release's gate reads too - and the run's own
 * conclusion only when that job is not there.
 *
 * Returns { state, run_id, url, counts, jobs: { name: passed|failed|skipped|cancelled },
 * bad: [{ name, url, conclusion }], secs, finished_at }; `state` is green,
 * red, cancelled, running (not over yet) or not-found (no run of that push).
 */
export function ciVerdict({ run = null, jobs = [] } = {}) {
  if (!run) return { state: 'not-found' };
  const base = { run_id: run.id ?? null, url: run.html_url ?? '', attempt: run.run_attempt ?? 1 };
  if (run.status !== 'completed') return { ...base, state: 'running', status: run.status ?? '?' };
  const st = (j) => JOB_STATE[j.conclusion] ?? 'failed';
  const map = Object.fromEntries(jobs.map((j) => [j.name, st(j)]));
  const all = jobs.find((j) => /^All tests \(/.test(j.name ?? ''));
  const values = Object.values(map);
  const counts = {
    passed: values.filter((s) => s === 'passed').length,
    failed: values.filter((s) => s === 'failed').length,
    skipped: values.filter((s) => s === 'skipped' || s === 'cancelled').length,
    total: values.length,
  };
  const bad = jobs.filter((j) => ['failed', 'cancelled'].includes(st(j))).map((j) => ({ name: j.name, url: j.html_url ?? '', conclusion: j.conclusion ?? 'unknown' }));
  const started = Date.parse(run.run_started_at ?? run.created_at ?? '');
  const ended = Date.parse(run.updated_at ?? '');
  const green = all ? all.conclusion === 'success' : run.conclusion === 'success';
  const state = green ? 'green' : run.conclusion === 'cancelled' || all?.conclusion === 'cancelled' ? 'cancelled' : 'red';
  return {
    ...base,
    state,
    conclusion: run.conclusion ?? null,
    all: all ? all.conclusion : null,
    counts,
    jobs: map,
    bad,
    secs: Number.isFinite(started) && Number.isFinite(ended) ? Math.round((ended - started) / 1000) : null,
    finished_at: run.updated_at ?? null,
  };
}

/** "1h02m" from seconds. */
export function wallOf(secs) {
  if (!Number.isFinite(secs)) return '?';
  const m = Math.round(secs / 60);
  return `${Math.floor(m / 60)}h${String(m % 60).padStart(2, '0')}m`;
}

// ── the nightly desktop packages and the snaps on edge (#266) ──────────────

/**
 * What `snapcraft upload --release=edge` answered: `released` (a revision,
 * released to edge), `review` (uploaded, held for the Store's review - the
 * channel keeps its old revision until the review passes, so the night's
 * report warns, and so does every morning after it until edge serves it) or
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
 * Whether the Snap Store's edge channel serves a nightly snap (`last`: its
 * version, revision and upload time), read from the public channel map
 * (api.snapcraft.io/v2/snaps/info). `live` when edge/<arch> carries that
 * revision, or that version; `held` when it does not - with how long since
 * the upload - so a review that never ends is said every morning, not once.
 *
 * The Store lists a nightly's version without its build metadata
 * (`0.62.0-nightly.20261118`, not `...+0d849cb7`): the first nightly built on
 * GitHub (2026-11-18) was on edge as revisions 63 and 62 and read as held.
 * The revision decides when both are known; versions are compared without
 * what follows a `+`.
 */
export function snapEdgeState(map, last, { arch = 'amd64', nowMs } = {}) {
  if (!last?.version) return null;
  const entry = (map ?? []).find((m) => m?.channel?.name === 'edge' && m?.channel?.architecture === arch);
  const bare = (v) => String(v ?? '').replace(/\+.*$/, '');
  const same = entry && (Number.isFinite(last.revision) && Number.isFinite(entry.revision) ? entry.revision === last.revision : bare(entry.version) === bare(last.version));
  if (same) return { state: 'live', arch, version: entry.version, revision: entry.revision };
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

// ── the desktop packages GitHub's runners make (#266) ──────────────────────

const ARCH_OF = { x64: 'x64', x86_64: 'x64', amd64: 'x64', arm64: 'arm64', aarch64: 'arm64' };

/** The platforms of the nightly desktop packages, in the order the page shows them. */
export const DESKTOP_PLATFORMS = [
  { id: 'windows', label: 'Windows' },
  { id: 'macos', label: 'macOS' },
  { id: 'linux', label: 'Linux' },
];

/** The snaps a night uploads to edge, one per architecture (the Store's names). */
export const SNAP_ARCHES = ['amd64', 'arm64'];

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
 * each snap's upload to edge ({ amd64: { state, revision?, why? }, arm64: ... }).
 */
export function desktopManifest({ files = [], version, revision, date, run = '', rows = {}, snap = {}, builtAt = '' }) {
  const rank = (f) => DESKTOP_PLATFORMS.findIndex((p) => p.id === f.platform) * 100 + (f.arch === 'x64' ? 0 : 50) + Object.keys(KIND_LABEL).indexOf(f.kind);
  const out = files
    .map((f) => ({ name: f.name, size: Number(f.size) || 0, sha256: String(f.sha256 ?? ''), ...classifyDesktopFile(f.name) }))
    .filter((f) => f.platform)
    .sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
  return { schema: 1, version, revision, date, run, built_at: builtAt, rows, snap, files: out };
}

/** The platform a row of the workflow makes packages for (nightly-desktop-job.sh targets), or null (a snap). */
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
 * Where GitHub's packages of the last nightly stand, from what the registry
 * and GitHub say: `source` (state.json last_push: the public commit, its
 * version, when it was pushed), `built` and `attempted` (the revision
 * annotations of the desktop artifact's `nightly` and `attempted` tags),
 * `run` (the nightly workflow's newest run: status, conclusion, url), `ci`
 * (ciVerdict of that commit: the workflow builds only on a green one).
 * Returns null with no source, else { state, ... }:
 *   built        the packages of that revision are on ghcr
 *   building     the workflow took it and runs
 *   failed       the workflow took it and did not publish the packages
 *   none         CI was not green on it: nothing is built from it
 *   waiting      not taken yet, within `graceH` hours of the push (CI comes first)
 *   not-started  not taken after `graceH` hours
 */
export function followVerdict({ source, built = null, attempted = null, run = null, ci = null, nowMs, graceH = 4 }) {
  if (!source?.revision) return null;
  const base = { version: source.version, revision: source.revision };
  if (built?.revision === source.revision) return { ...base, state: 'built' };
  if (attempted?.revision === source.revision) {
    if (run && run.status !== 'completed') return { ...base, state: 'building', url: run.url ?? '' };
    return { ...base, state: 'failed', url: run?.url ?? '', conclusion: run?.conclusion ?? 'unknown', why: run?.conclusion === 'success' ? 'it ended green and published no packages' : '' };
  }
  if (ci && ['red', 'cancelled'].includes(ci.state)) return { ...base, state: 'none', why: `ci.yml was ${ci.state} on it` };
  const hours = source.at ? Math.floor((nowMs - Date.parse(source.at)) / 3_600_000) : null;
  return { ...base, state: hours !== null && hours >= graceH && ci?.state !== 'running' ? 'not-started' : 'waiting', hours };
}

const snapWord = (arch, s, version) => {
  if (s.state === 'released') return `  snap ${version} on edge (${arch})${s.revision ? `: revision ${s.revision}` : ''}`;
  if (s.state === 'skipped') return `  snap ${arch}: ${s.why}`;
  if (s.state === 'review') return `  WARNING snap ${arch}: revision ${s.revision ?? '?'} uploaded and HELD for the Snap Store's review`;
  return `  WARNING snap ${arch} upload to edge FAILED: ${s.why ?? 'see the run'}`;
};

/**
 * The morning report's lines for GitHub's nightly packages (followVerdict,
 * with what the follow-up did: `site` - the page published, kept or failed -
 * `rows` and `snaps` from the manifest, `edge` from the Store).
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
  else if (f.state === 'none') lines.push(`desktop packages ${f.version}: none - ${f.why}`);
  else if (f.state === 'waiting') lines.push(`desktop packages ${f.version}: GitHub builds them once ci.yml is green on the commit`);
  else if (f.state === 'not-started') lines.push(`WARNING desktop packages ${f.version}: GitHub has not taken the commit in ${f.hours} h (is the nightly workflow disabled?)`);
  else if (f.state === 'unknown') lines.push(`WARNING desktop packages: ${f.why}`);
  for (const arch of SNAP_ARCHES) if (f.snaps?.[arch]) lines.push(snapWord(arch, f.snaps[arch], f.version));
  for (const arch of SNAP_ARCHES) {
    const el = snapEdgeLine(f.edge?.[arch]);
    if (el) lines.push(el);
  }
  return lines;
}

/** Whether the follow-up is worth a warning. */
export const followTrouble = (f) =>
  !!f &&
  (['failed', 'not-started', 'unknown'].includes(f.state) ||
    Object.values(f.rows ?? {}).some((st) => st !== 'built') ||
    f.site?.state === 'failed' ||
    Object.values(f.snaps ?? {}).some((s) => s && !['released', 'skipped'].includes(s.state)) ||
    Object.values(f.edge ?? {}).some((e) => e?.state === 'held'));

/** The report's line on a nightly snap edge still does not serve (snapEdgeState), or null. */
export function snapEdgeLine(e) {
  if (!e || e.state !== 'held') return null;
  const ago = Number.isFinite(e.days) ? `${e.days} day(s) ago` : 'earlier';
  return `WARNING snap: edge (${e.arch ?? 'amd64'}) serves ${e.edge}, not the nightly ${e.version} uploaded ${ago}${e.revision ? ` (revision ${e.revision})` : ''}: the Snap Store still holds it - snapcraft status filex-app`;
}

/**
 * The report's line on the nightly image (ghcr.io/brf-tech/filex:nightly):
 * `image` what its labels say ({ state: built | other | unknown, version?,
 * revision?, why? }), `f` the packages' follow-up - the workflow that builds
 * both. Built: one line. Not built while the workflow already ran on the
 * commit: a warning. Not built yet: nothing (the packages' line says why).
 */
export function imageLine(image, f, ref = 'ghcr.io/brf-tech/filex:nightly') {
  if (!image) return null;
  if (image.state === 'built') return `nightly image ${ref}: ${image.version ?? '?'}`;
  if (image.state === 'unknown') return `WARNING nightly image ${ref}: ${image.why}`;
  if (f && ['built', 'failed'].includes(f.state)) return `WARNING nightly image ${ref} is still ${image.version ?? short(image.revision)}, not this nightly's`;
  return null;
}

const imageTrouble = (line) => typeof line === 'string' && line.startsWith('WARNING');

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

/**
 * What history.jsonl keeps of a night: the record without its working
 * fields, and - once the morning report has it - GitHub's verdict: the run,
 * the counts and each job's state (what the next nights compare with).
 */
export function nightRecord(t) {
  const c = t.ci;
  return {
    night: t.night,
    at: t.at,
    ended: t.ended,
    decision: t.decision,
    why: t.why,
    sha: t.sha,
    subject: t.subject,
    prev_sha: t.prev_sha,
    exit: t.exit,
    push: t.push ? { sha: t.push.sha, base: t.push.base, branch: t.push.branch, version: t.push.version, url: t.push.url } : undefined,
    s3: t.s3?.files?.length ? { since: t.s3.since, files: t.s3.files } : undefined,
    ci: c && c.state !== 'running' ? { state: c.state, run_id: c.run_id, url: c.url, counts: c.counts, secs: c.secs } : undefined,
    jobs: c?.jobs && c.state !== 'running' ? c.jobs : undefined,
  };
}

/** The last record of a night that pushed, before index `before` (default: the end). */
export function lastPushed(history, before = history.length) {
  for (let i = Math.min(before, history.length) - 1; i >= 0; i -= 1) if (history[i].decision === 'pushed') return history[i];
  return null;
}

// ── the host a chain runs on (run.mjs) ──────────────────────────────────────

/**
 * What run.sh's pause hook (CHAIN_PAUSE_CMD) did before the run, in the words
 * of chain.log; '' when none ran. `p`: result.json `host.pause`, { status:
 * paused|failed|timeout, exit, secs }.
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
 * result.json `load`; chain.log names them): memory or IO "full" over these
 * for ten seconds (lesson #1222: at 0.3 everything runs 20-60 times slower),
 * or a write that took a second or more on the run's disk (lesson #1232: a
 * disk that stops for 15 s freezes every fsync).
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

/** The line on the host a run ran on: the budget and the worst pressure of the run, or null without a `host` in the result. */
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

// ── the morning report ──────────────────────────────────────────────────────

/** Changes between two nights' job states. */
export function jobsDiff(prevJobs = {}, jobs = {}) {
  const red = (s) => s === 'failed' || s === 'cancelled';
  const names = Object.keys(jobs);
  return {
    newRed: names.filter((n) => red(jobs[n]) && !red(prevJobs[n])),
    stillRed: names.filter((n) => red(jobs[n]) && red(prevJobs[n])),
    fixed: Object.keys(prevJobs).filter((n) => red(prevJobs[n]) && jobs[n] === 'passed'),
  };
}

/** The last night (before index `before`) on which `job` passed on GitHub: its record, or null. */
export function lastGreenOf(history, job, before = history.length) {
  for (let i = Math.min(before, history.length) - 1; i >= 0; i -= 1) {
    const r = history[i];
    if (r.decision === 'pushed' && r.jobs?.[job] === 'passed') return r;
  }
  return null;
}

const SEVERITY_RANK = { info: 0, success: 1, warning: 2, danger: 3 };
const worse = (a, b) => (SEVERITY_RANK[b] > SEVERITY_RANK[a] ? b : a);

/** The morning report's line for a pushed change to the S3 code nothing tests on its own, or null. */
function s3Line(s) {
  if (!s?.files?.length) return null;
  const files = `${s.files.slice(0, 5).join(', ')}${s.files.length > 5 ? ', ...' : ''}`;
  return `WARNING s3-live: the S3 code changed (${s.files.length} file(s) since ${short(s.since)}: ${files}) and nothing runs the live S3 tests on its own - run them by hand on the build host: CHAIN_EXTRAS=s3-live bash scripts/chain/run.sh --profile targeted --env <the chain's settings with FILEX_TEST_S3_*>`;
}

/** The lines on the last nightly's CI, for a night that did not push. */
function lastCiLine(push, ci) {
  if (!push?.revision) return null;
  const what = !ci ? 'not asked' : ci.state === 'green' ? 'green' : ci.state === 'red' ? `RED (${ci.counts?.failed ?? '?'} of ${ci.counts?.total ?? '?'} jobs)` : ci.state;
  return `last nightly: ${short(push.revision)} (${push.version ?? '?'}), ci.yml ${what}${ci?.url ? ` - ${ci.url}` : ''}`;
}

/**
 * The morning report: one notification for the night.
 *
 *   tonight   the night's record (nightly.mjs writes tonight.json), or null
 *   history   the earlier nights' records, oldest first (history.jsonl)
 *   ci        ciVerdict of the last nightly commit (or { state: unknown, why })
 *   push      state.json last_push: the last nightly commit pushed
 *   follow    the follow-up of GitHub's packages (followVerdict + what it did)
 *   image     the nightly image's labels (imageLine)
 *   commits   (from, to) => [{ sha, subject }], newest first: git log from..to
 *             of the private checkout (the report is not public)
 *   running   whether the nightly that wrote `tonight` is still alive
 *   nowMs, tz, maxAgeH (older than that, tonight is not tonight)
 *
 * Returns { severity, title, message }: success (GitHub green), danger
 * (GitHub red, or the export refused the change), warning (CI not over or
 * not found, a push that failed, no record, a package, the image, the page or
 * a snap that did not go out, S3 code nothing tested) or info (nothing to
 * push, or a release on GitHub kept the night back).
 */
export function composeReport({
  tonight,
  history = [],
  ci = null,
  push = null,
  follow = null,
  image = null,
  imageRef = 'ghcr.io/brf-tech/filex:nightly',
  commits = () => [],
  running = true,
  nowMs,
  tz = 'UTC',
  maxAgeH = 20,
  maxCommits = 8,
  maxJobs = 25,
}) {
  const lines = [];
  const at = tonight ? Date.parse(tonight.at) : NaN;
  if (!tonight || !Number.isFinite(at) || nowMs - at > maxAgeH * 3_600_000) {
    const last = history.at(-1);
    return {
      severity: 'warning',
      title: `filex nightly ${localDate(nowMs, tz)}: no run recorded`,
      message: [
        `The nightly left no record in the last ${maxAgeH} h: its timer did not fire, or it died before it wrote one.`,
        last ? `Last record: ${last.night} ${last.decision} on ${short(last.sha)}${last.why ? ` (${last.why})` : ''}.` : 'There is no earlier record either.',
        'Look at: journalctl -u filex-nightly.service',
      ].join('\n'),
    };
  }
  const night = tonight.night ?? localDate(at, tz);
  const head = `main ${short(tonight.sha)}${tonight.subject ? ` ${tonight.subject}` : ''}`;
  const fl = followLines(follow);
  const il = imageLine(image, follow, imageRef);
  const githubTrouble = followTrouble(follow) || imageTrouble(il);

  if (tonight.phase && tonight.phase !== 'done') {
    const how = running
      ? `still going at ${localTime(nowMs, tz)}: ${tonight.phase}`
      : `the nightly is gone (pid ${tonight.pid ?? '?'}) and left the night at "${tonight.phase}": it died or was killed - journalctl -u filex-nightly.service`;
    lines.push(head, `started ${localTime(at, tz)} (${tz}), ${how}`);
    return { severity: 'warning', title: `filex nightly ${night}: ${running ? 'still running' : `died while ${tonight.phase}`} (${short(tonight.sha)})`, message: finish(lines) };
  }

  if (['skipped', 'unchanged', 'deferred'].includes(tonight.decision)) {
    if (tonight.decision !== 'skipped') lines.push(head);
    lines.push(tonight.why ?? 'main has not moved');
    if (tonight.busy?.url) lines.push(`  ${tonight.busy.url}`);
    const lc = lastCiLine(push, ci);
    if (lc) lines.push(lc);
    if (il) lines.push(il);
    lines.push(...fl);
    const title = { skipped: `no push, main unchanged (${short(tonight.sha)})`, unchanged: `no push, the export is the public main already (${short(tonight.sha)})`, deferred: `not pushed, GitHub is testing a release (${short(tonight.sha)})` }[tonight.decision];
    return { severity: githubTrouble ? 'warning' : 'info', title: `filex nightly ${night}: ${title}`, message: finish(lines) };
  }

  if (tonight.decision !== 'pushed') {
    lines.push(head, `${tonight.decision === 'refused' ? 'the export REFUSED it' : tonight.decision === 'failed' ? 'the push FAILED' : 'the nightly could not run'}: ${tonight.why ?? ''}`);
    if (tonight.log) lines.push(`log ${tonight.log}`);
    const lc = lastCiLine(push, ci);
    if (lc) lines.push(lc);
    return {
      severity: tonight.decision === 'refused' ? 'danger' : 'warning',
      title: `filex nightly ${night}: ${tonight.decision === 'refused' ? 'the export refused main' : tonight.decision === 'failed' ? 'not pushed' : 'did not run'} (${short(tonight.sha)})`,
      message: finish(lines),
    };
  }

  // It pushed.
  const p = tonight.push ?? {};
  const own = history.findIndex((r) => r.decision === 'pushed' && r.push?.sha && r.push.sha === p.sha);
  const before = own >= 0 ? own : history.length;
  const prev = lastPushed(history, before);
  lines.push(head, `nightly ${short(p.sha)} (${p.version ?? '?'}) on ${p.branch ?? 'nightly'}${p.url ? ` - ${p.url}` : ''}`);
  if (prev?.sha && prev.sha !== tonight.sha) {
    const list = safeCommits(commits, prev.sha, tonight.sha);
    lines.push(`since the last nightly (${prev.night}, ${short(prev.sha)}): ${list === null ? 'the range is not in this checkout' : `${list.length} commit(s)`}`);
    for (const c of (list ?? []).slice(0, maxCommits)) lines.push(`  ${short(c.sha)} ${c.subject}`);
    if (list && list.length > maxCommits) lines.push(`  ... and ${list.length - maxCommits} more`);
  } else if (!prev) lines.push('the first nightly');

  const c = ci ?? { state: 'unknown', why: 'GitHub was not asked' };
  let severity = 'success';
  let verdict;
  if (c.state === 'green' || c.state === 'red' || c.state === 'cancelled') {
    const n = c.counts ?? {};
    const delta = prev?.ci && Number.isFinite(prev.ci.secs) && Number.isFinite(c.secs) ? `, previous ${wallOf(prev.ci.secs)}` : '';
    lines.push(`ci.yml ${c.state === 'green' ? 'green' : c.state === 'red' ? 'RED' : 'CANCELLED'}: ${n.passed} passed, ${n.failed} red, ${n.skipped} skipped of ${n.total} jobs, ${wallOf(c.secs)}${delta} - ${c.url}`);
    const diff = jobsDiff(prev?.jobs ?? {}, c.jobs ?? {});
    const shown = (c.bad ?? []).slice(0, maxJobs);
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
        lines.push(`${j.conclusion === 'cancelled' ? 'CANCELLED' : 'RED'} ${j.name} (${since})${j.url ? ` - ${j.url}` : ''}`);
      }
      if (green && green.sha !== tonight.sha) {
        const list = safeCommits(commits, green.sha, tonight.sha);
        lines.push(`  last green ${green.night} on ${short(green.sha)}; the range ${short(green.sha)}..${short(tonight.sha)}: ${list === null ? 'not in this checkout' : `${list.length} commit(s)`}`);
        for (const cm of (list ?? []).slice(0, maxCommits)) lines.push(`    ${short(cm.sha)} ${cm.subject}`);
        if (list && list.length > maxCommits) lines.push(`    ... and ${list.length - maxCommits} more`);
      } else if (green) lines.push(`  green on ${green.night} on this very commit: not a change, look for a flaky test (re-run: gh run rerun ${c.run_id} --failed)`);
      else lines.push('  never green in the nights on record');
    }
    if ((c.bad ?? []).length > shown.length) lines.push(`... and ${c.bad.length - shown.length} more: see the run`);
    if (diff.fixed.length) lines.push(`fixed since ${prev?.night ?? 'the last nightly'}: ${diff.fixed.join(', ')}`);
    severity = c.state === 'green' ? 'success' : c.state === 'red' ? 'danger' : 'warning';
    verdict = c.state === 'green' ? 'green' : c.state === 'red' ? `RED ${n.failed} of ${n.total}` : 'cancelled';
  } else if (c.state === 'running') {
    lines.push(`ci.yml still running at ${localTime(nowMs, tz)} (${c.status}) - ${c.url}`);
    severity = 'warning';
    verdict = 'CI still running';
  } else if (c.state === 'not-found') {
    lines.push(`WARNING no ci.yml run on GitHub for ${short(p.sha)}: is \`${p.branch ?? 'nightly'}\` in ci.yml's push branches?`);
    severity = 'warning';
    verdict = 'no CI run';
  } else {
    lines.push(`WARNING GitHub could not be asked about ${short(p.sha)}: ${c.why ?? 'unknown'}`);
    severity = 'warning';
    verdict = 'CI unknown';
  }
  const sl = s3Line(tonight.s3);
  if (sl) lines.push(sl);
  if (il) lines.push(il);
  lines.push(...fl);
  if (sl || githubTrouble) severity = worse(severity, 'warning');
  if (verdict === 'green' && (sl || githubTrouble)) verdict = 'green, with a warning';
  return {
    severity,
    title: `filex nightly ${night}: ${verdict} (${short(p.sha)}${c.secs ? `, ${wallOf(c.secs)}` : ''})`,
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
  return text.length > max ? `${text.slice(0, max - 20)}\n... (cut, see the run)` : text;
}
