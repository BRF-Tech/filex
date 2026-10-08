// The nightly run of the test chain (task #175): when a night runs, when it
// must not, what the morning report says, and when a night makes a nightly
// build. scripts/chain/nightly-lib.mjs holds the decisions as pure functions;
// scripts/chain/nightly.mjs and scripts/chain/report.mjs only read and write
// around them.
//
// ⚠ Why this exists. Through 0.52 the build host's chain ran only on release
// days, by hand: the red jobs a night could have found were found on the day
// they cost the most (0.51: the mega menu broke 17 selectors and a writehook
// race surfaced after the cut). A nightly run is only worth it if it is quiet
// when nothing changed, never runs into the host's other scheduled work,
// says in one message what went red and which commits did it, and builds an
// image only from a green change.

import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { parseEnvFile } from '../../../scripts/chain/env.mjs';
import {
  composeReport,
  decideBuild,
  decideRun,
  decideS3Live,
  expectedRunMin,
  extrasFor,
  imagesToPrune,
  jobsDiff,
  lastGreenOf,
  localDate,
  nextS3LiveState,
  nightRecord,
  nightlyRunId,
  nightlySettings,
  nightlyTags,
  nightlyVersion,
  parseQuiet,
  quietState,
  registryLogin,
  resultFields,
  runsToPrune,
  S3_LIVE_KEYS,
  S3_LIVE_PATHS,
  s3LiveFile,
  s3Touched,
  startWindow,
} from '../../../scripts/chain/nightly-lib.mjs';
import { parseNightlyArgs } from '../../../scripts/chain/nightly.mjs';
import { notifyBody, parseReportArgs } from '../../../scripts/chain/report.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const CHAIN = path.join(REPO, 'scripts', 'chain');
const read = (rel: string) => fs.readFileSync(path.join(CHAIN, rel), 'utf8');

const A = 'a'.repeat(40);
const B = 'b'.repeat(40);
const C = 'c'.repeat(40);
const ms = (s: string) => Date.parse(s);

type Rec = Record<string, unknown>;
const ran = (over: Rec = {}): Rec => ({ decision: 'ran', sha: A, finished: true, ok: true, stopped: false, exit: 0, secs: 7200, wall: '2h00m00s', jobs: {}, ...over });

describe('whether a night runs', () => {
  it('runs the first night, and every night main moved', () => {
    expect(decideRun({ head: A, last: null })).toMatchObject({ run: true, why: 'the first nightly run' });
    expect(decideRun({ head: B, last: ran() })).toEqual({ run: true, why: `main moved: ${A.slice(0, 8)}..${B.slice(0, 8)}` });
  });

  it('does not run again on a main that has not moved, green or red', () => {
    expect(decideRun({ head: A, last: ran() })).toMatchObject({ run: false, why: expect.stringMatching(/has not moved .*green/) });
    expect(decideRun({ head: A, last: ran({ ok: false, exit: 1 }) })).toMatchObject({ run: false, why: expect.stringMatching(/red/) });
  });

  it('runs again on the same commit when the last run did not finish: stopped, a setup error, no result', () => {
    expect(decideRun({ head: A, last: ran({ stopped: true, ok: false, exit: 130 }) }).run).toBe(true);
    expect(decideRun({ head: A, last: ran({ exit: 2, ok: false }) }).run).toBe(true);
    expect(decideRun({ head: A, last: ran({ finished: false, ok: false, exit: 2 }) }).run).toBe(true);
    expect(decideRun({ head: A, last: ran(), force: true })).toEqual({ run: true, why: 'forced (--force)' });
  });

  it('compares with the last night that RAN, not with a night that was skipped or refused', () => {
    const history = [ran({ sha: A }), { decision: 'skipped', sha: A }, { decision: 'refused', sha: B }];
    const last = history.filter((r) => r.decision === 'ran').at(-1) ?? null;
    expect(decideRun({ head: B, last }).run).toBe(true);
    expect(expectedRunMin(history, 180)).toBe(120);
    expect(expectedRunMin([], 180)).toBe(180);
    expect(expectedRunMin([ran({ stopped: true, secs: 99 })], 180)).toBe(180);
  });
});

describe('the quiet windows (NIGHTLY_QUIET, UTC)', () => {
  const weekly = parseQuiet('Mon 01:45-06:00');

  it('reads "<day> HH:MM-HH:MM" lists, every day as *, and refuses anything else', () => {
    expect(parseQuiet('Mon 01:45-06:00, * 23:30-00:30')).toEqual([
      { day: 1, start: 105, end: 360, text: 'Mon 01:45-06:00' },
      { day: -1, start: 1410, end: 30, text: '* 23:30-00:30' },
    ]);
    expect(parseQuiet('')).toEqual([]);
    for (const bad of ['Monday 01:00-02:00', 'Mon 1-2', 'Mon 25:00-26:00', 'Mon 01:00-01:00', 'Mon 01:61-02:00']) {
      expect(() => parseQuiet(bad), bad).toThrow(/NIGHTLY_QUIET/);
    }
  });

  it('knows when it is inside a window and when the next one starts, across midnight', () => {
    // 2026-10-05 is a Monday.
    expect(quietState(ms('2026-10-05T03:00:00Z'), weekly).inside?.text).toBe('Mon 01:45-06:00');
    expect(quietState(ms('2026-10-04T22:00:00Z'), weekly)).toEqual({ inside: null, nextStart: ms('2026-10-05T01:45:00Z') });
    const late = parseQuiet('Sun 23:30-00:30');
    expect(quietState(ms('2026-10-05T00:10:00Z'), late).inside?.text).toBe('Sun 23:30-00:30');
    expect(quietState(ms('2026-10-05T00:40:00Z'), late).inside).toBeNull();
  });

  it('starts a run only when it can be over before the next window, and waits for the lock only that long', () => {
    // Sunday 22:00 UTC (01:00 at UTC+3): 3 h 45 min to the Monday window.
    const sunday = ms('2026-10-04T22:00:00Z');
    expect(startWindow({ nowMs: sunday, windows: weekly, expectMin: 180, marginMin: 15, lockWaitMin: 120 })).toEqual({
      ok: true,
      deadline: ms('2026-10-05T01:30:00Z'),
      lockWaitMin: 30,
    });
    const tooLong = startWindow({ nowMs: sunday, windows: weekly, expectMin: 240, marginMin: 15, lockWaitMin: 120 });
    expect(tooLong.ok).toBe(false);
    expect(tooLong.why).toMatch(/would not be over before the quiet window at 2026-10-05 01:45 UTC/);
    expect(startWindow({ nowMs: ms('2026-10-05T02:00:00Z'), windows: weekly, expectMin: 60, marginMin: 15, lockWaitMin: 120 })).toEqual({
      ok: false,
      why: 'inside the quiet window Mon 01:45-06:00 UTC',
    });
    // Any other night has the whole week to the window: the lock wait is the setting's.
    expect(startWindow({ nowMs: ms('2026-10-06T22:00:00Z'), windows: weekly, expectMin: 180, marginMin: 15, lockWaitMin: 120 }).lockWaitMin).toBe(120);
    expect(startWindow({ nowMs: sunday, windows: [], expectMin: 999, marginMin: 15, lockWaitMin: 120 })).toEqual({ ok: true, deadline: null, lockWaitMin: 120 });
  });

  it('keeps the example settings clear of a weekly round on Monday at 02:00 UTC', () => {
    const env = parseEnvFile(read('nightly.env.example'));
    const windows = parseQuiet(env.NIGHTLY_QUIET);
    expect(quietState(ms('2026-10-05T02:00:00Z'), windows).inside).not.toBeNull();
    expect(quietState(ms('2026-10-05T05:59:00Z'), windows).inside).not.toBeNull();
  });
});

describe('the extras of a night', () => {
  const now = ms('2026-10-07T22:00:00Z');
  const asked = ['ds-go', 'shots', 'realenv'];

  it('runs realenv once every NIGHTLY_REALENV_EVERY_DAYS days, and the others every night', () => {
    expect(extrasFor({ asked, everyDays: 7, lastRealenvMs: null, nowMs: now })).toEqual(asked);
    expect(extrasFor({ asked, everyDays: 7, lastRealenvMs: now - 3 * 86_400_000, nowMs: now })).toEqual(['ds-go', 'shots']);
    // Half a day of slack: last week's run started ten minutes later.
    expect(extrasFor({ asked, everyDays: 7, lastRealenvMs: now - 7 * 86_400_000 + 600_000, nowMs: now })).toEqual(asked);
    expect(extrasFor({ asked, everyDays: 1, lastRealenvMs: now - 86_400_000, nowMs: now })).toEqual(asked);
    expect(extrasFor({ asked, everyDays: 0, lastRealenvMs: null, nowMs: now })).toEqual(['ds-go', 'shots']);
    expect(extrasFor({ asked: ['shots'], everyDays: 7, lastRealenvMs: null, nowMs: now })).toEqual(['shots']);
  });
});

describe('the live S3 job (s3-live)', () => {
  const full = {
    FILEX_TEST_S3_ENDPOINT: 'https://s3.eu-central-003.backblazeb2.com',
    FILEX_TEST_S3_REGION: 'eu-central-003',
    FILEX_TEST_S3_BUCKET: 'filex-nightly',
    FILEX_TEST_S3_ACCESS_KEY: 'K003example',
    FILEX_TEST_S3_SECRET_KEY: 'not/a+real=secret',
    CHAIN_LOCK: '/var/lib/filex-chain/lock',
  };

  it('hands the job exactly the FILEX_TEST_S3_* settings, one line each, and nothing else', () => {
    const f = s3LiveFile(full);
    expect(f.missing).toEqual([]);
    expect(f.text.split('\n').filter(Boolean)).toEqual([
      'FILEX_TEST_S3_BUCKET=filex-nightly',
      'FILEX_TEST_S3_ACCESS_KEY=K003example',
      'FILEX_TEST_S3_SECRET_KEY=not/a+real=secret',
      'FILEX_TEST_S3_REGION=eu-central-003',
      'FILEX_TEST_S3_ENDPOINT=https://s3.eu-central-003.backblazeb2.com',
    ]);
    expect(f.text).not.toContain('CHAIN_LOCK');
  });

  it('names - and only names - what is missing, the region included (lesson #927)', () => {
    const partial: Record<string, string> = { ...full };
    delete partial.FILEX_TEST_S3_REGION;
    delete partial.FILEX_TEST_S3_SECRET_KEY;
    const f = s3LiveFile(partial);
    expect(f.missing).toEqual(['FILEX_TEST_S3_SECRET_KEY', 'FILEX_TEST_S3_REGION']);
    expect(JSON.stringify(f.missing)).not.toContain('K003example');
    expect(s3LiveFile({})).toEqual({ text: '', missing: S3_LIVE_KEYS.required });
    // A value that would smuggle a second line into the job's file.
    expect(() => s3LiveFile({ ...full, FILEX_TEST_S3_SECRET_KEY: 'a\nFILEX_TEST_S3_BUCKET=other' })).toThrow(/line break/);
  });

  it('are named in the example settings, with no value in them', () => {
    const example = read('nightly.env.example');
    for (const k of [...S3_LIVE_KEYS.required, ...S3_LIVE_KEYS.optional]) expect(example, k).toMatch(new RegExp(`^# ${k}=`, 'm'));
    const env = parseEnvFile(example);
    for (const k of [...S3_LIVE_KEYS.required, ...S3_LIVE_KEYS.optional]) expect(env[k], k).toBeUndefined();
  });
});

describe('the live S3 tests run only when the S3 code changed (the owner, 2026-10-06)', () => {
  // ⚠ Why. The build host's credentials are a provider's demo account, and a
  // change to the drive itself is tested by hand with credentials brought for
  // it: running the live tests every night spends the account on nights the
  // S3 code did not move, and a night without credentials must not read red.
  const missing = ['FILEX_TEST_S3_BUCKET', 'FILEX_TEST_S3_ACCESS_KEY', 'FILEX_TEST_S3_SECRET_KEY', 'FILEX_TEST_S3_REGION'];

  it('counts the driver, its stall policy, the storage contracts, the B2 reader, the live tests and the AWS SDK line of go.mod', () => {
    const files = [
      'backend/internal/storage/drivers/s3/s3.go',
      'backend/internal/storage/stall/conn.go',
      'backend/internal/storage/driver.go',
      'backend/internal/usage/b2.go',
      'backend/internal/usage/pricing.go',
      'backend/internal/storage/kindguard.go',
      'backend/internal/sync/worker.go',
      'web/src/App.vue',
    ];
    expect(s3Touched(files)).toEqual([
      'backend/internal/storage/drivers/s3/s3.go',
      'backend/internal/storage/stall/conn.go',
      'backend/internal/storage/driver.go',
      'backend/internal/usage/b2.go',
    ]);
    const bump = '--- a/backend/go.mod\n+++ b/backend/go.mod\n-\tgithub.com/aws/aws-sdk-go-v2 v1.30.0\n+\tgithub.com/aws/aws-sdk-go-v2 v1.31.0\n';
    expect(s3Touched(['backend/go.mod'], bump)).toEqual(['backend/go.mod']);
    expect(s3Touched(['backend/go.mod'], '-\tgithub.com/spf13/cobra v1.8.0\n+\tgithub.com/spf13/cobra v1.9.0\n')).toEqual([]);
    expect(s3Touched(['web/src/App.vue'], ' \tgithub.com/aws/aws-sdk-go-v2 v1.30.0\n')).toEqual([]);
    // Every place on the list is there: a renamed folder would make every night "unchanged".
    for (const p of S3_LIVE_PATHS) {
      const at = path.join(REPO, ...p.replace(/\/$/, '').split('/'));
      expect(fs.existsSync(at) || fs.existsSync(`${at}.go`), p).toBe(true);
    }
  });

  it('unchanged: no run, and not red - "skipped - S3 driver unchanged since <commit>"', () => {
    expect(decideS3Live({ base: A, touched: [], missing })).toEqual({ run: false, status: 'unchanged', since: A, why: `S3 driver unchanged since ${A.slice(0, 8)}` });
    expect(decideS3Live({ base: A, touched: [], missing: [] }).run).toBe(false);
  });

  it('changed with credentials: it runs', () => {
    const d = decideS3Live({ base: A, touched: ['backend/internal/storage/drivers/s3/s3.go'], missing: [] });
    expect(d).toMatchObject({ run: true, status: 'run', since: A });
    expect(d.why).toBe(`1 file(s) changed since ${A.slice(0, 8)}: backend/internal/storage/drivers/s3/s3.go`);
    // No green run on record, or its commit gone from the checkout: changed.
    expect(decideS3Live({ base: null, touched: null, missing: [] })).toMatchObject({ run: true, why: 'no green live S3 run on record' });
    expect(decideS3Live({ base: A, touched: null, missing: [] })).toMatchObject({ run: true, why: `${A.slice(0, 8)}, the last live S3 run, is not in this checkout` });
  });

  it('changed without credentials: no run, and a warning that names the change - not a red night', () => {
    const d = decideS3Live({ base: A, touched: ['backend/internal/storage/stall/conn.go'], missing });
    expect(d).toMatchObject({ run: false, status: 'no-credentials', since: A, files: ['backend/internal/storage/stall/conn.go'] });
    expect(d.why).toBe(
      `the S3 driver changed (1 file(s) changed since ${A.slice(0, 8)}: backend/internal/storage/stall/conn.go) and the live tests did not run: no credentials (${missing.join(', ')})`,
    );
    expect(decideS3Live({ asked: false, base: A, touched: ['x'], missing })).toMatchObject({ run: false, status: 'not-asked' });
  });

  it('measures from the last green run: a change nobody tested is reported again the next night', () => {
    const prev = { sha: A, at: 'a', by: 'night' };
    expect(nextS3LiveState(prev, { head: C, base: A, passed: true, at: 't' })).toEqual({ sha: C, at: 't', by: 'night' });
    expect(nextS3LiveState(prev, { head: C, base: A, passed: false, at: 't' })).toBe(prev);
    expect(nextS3LiveState(undefined, { head: C, base: B, passed: false, at: 't' })).toEqual({ sha: B, at: 't', by: 'start' });
    expect(nextS3LiveState(prev, { head: C, base: A, passed: true, at: 't', by: 'hand' })).toEqual({ sha: C, at: 't', by: 'hand' });
  });

  it('leaves s3-live out of the night unless the decision runs it, and moves the measure only on green', () => {
    const night = read('nightly.mjs');
    expect(night).toContain(".filter((x) => x !== 's3-live' || s3.run);");
    expect(night).toContain("const passed = extras.includes('s3-live') && jobStatuses(result)['s3-live'] === 'passed';");
    expect(night).toMatch(/state\.s3_live = nextS3LiveState\(state\.s3_live, \{ head, base: s3\.since, passed, at: tonight\.at \}\);/);
  });
});

describe('the nightly build', () => {
  const green = { schema: 1, profile: 'nightly', sha: B, finished: '2026-10-07T03:10:00Z', ok: true, stopped: false, dirty_files: 0 };

  it('is made from a green nightly run of a commit that was not built yet', () => {
    expect(decideBuild({ result: green, lastBuildSha: A })).toEqual({
      build: true,
      why: `main moved since the last nightly build: ${A.slice(0, 8)}..${B.slice(0, 8)}`,
    });
    expect(decideBuild({ result: green })).toEqual({ build: true, why: 'the first nightly build' });
  });

  it('is not made on a red night, on a night main did not move, or from anything but a clean nightly run', () => {
    expect(decideBuild({ result: { ...green, ok: false } }).build).toBe(false);
    expect(decideBuild({ result: { ...green, finished: null, ok: null } }).build).toBe(false);
    expect(decideBuild({ result: green, lastBuildSha: B })).toEqual({ build: false, why: `no commit since the last nightly build (${B.slice(0, 8)})` });
    expect(decideBuild({ result: { ...green, profile: 'full' } }).build).toBe(false);
    expect(decideBuild({ result: { ...green, dirty_files: 2 } }).build).toBe(false);
    expect(decideBuild({ result: null }).build).toBe(false);
    expect(decideBuild({ result: green, mode: 'off' })).toEqual({ build: false, why: 'NIGHTLY_BUILD=off' });
  });

  it('reports the next minor as a pre-release with the commit as build metadata, and goes out as :nightly and :nightly-<date>', () => {
    expect(nightlyVersion('0.52.0', '20261007', B)).toBe(`0.53.0-nightly.20261007+${B.slice(0, 8)}`);
    expect(nightlyVersion('v0.9.3', '20261007', B)).toBe(`0.10.0-nightly.20261007+${B.slice(0, 8)}`);
    expect(() => nightlyVersion('0.52', '20261007', B)).toThrow(/released version/);
    expect(() => nightlyVersion('0.52.0', '2026-10-07', B)).toThrow(/date/);
    expect(() => nightlyVersion('0.52.0', '20261007', 'main')).toThrow(/commit/);
    expect(nightlyTags('20261007')).toEqual(['nightly', 'nightly-20261007']);
  });

  it('reads the version the binary reports the way the update check does', () => {
    // backend/internal/update/semver.go drops +metadata and keeps the
    // pre-release: a nightly sorts below the release it leads to.
    const semver = fs.readFileSync(path.join(REPO, 'backend', 'internal', 'update', 'semver.go'), 'utf8');
    expect(semver).toMatch(/IndexByte\(body, '\+'\)/);
    expect(semver).toMatch(/IndexByte\(body, '-'\)/);
  });

  it('builds from the public form of the commit, never from the private tree, and publishes nothing itself', () => {
    const sh = read('nightly-build.sh');
    expect(sh).toMatch(/bash "\$NB_SRC\/scripts\/export-public\.sh" "\$NB_PUBLIC_DIR"/);
    expect(sh).toMatch(/docker build [^\n]*-f "\$NB_PUBLIC_DIR\/\$DOCKERFILE"/);
    expect(sh).toMatch(/-t "\$NB_IMAGE:\$NB_TAG" "\$NB_PUBLIC_DIR"/);
    expect(sh).not.toMatch(/docker push/);
    expect(sh).not.toMatch(/rm -rf/);
  });
});

describe('the morning report', () => {
  const now = ms('2026-10-07T04:00:00Z');
  const tz = 'Europe/Istanbul';
  const commits = (from: string, to: string) => {
    const all = [
      { sha: C, subject: 'fix(race): the writehook waits for its flush' },
      { sha: B, subject: 'feat(admin): the mega menu' },
    ];
    if (from === A && to === C) return all;
    if (from === B && to === C) return all.slice(0, 1);
    throw new Error(`no range ${from}..${to}`);
  };
  const result = (over: Rec = {}) => ({
    schema: 1,
    profile: 'nightly',
    sha: C,
    finished: '2026-10-07T00:40:00Z',
    ok: true,
    stopped: false,
    secs: 8100,
    wall: '2h15m00s',
    counts: { passed: 3, failed: 0, skipped: 0, total: 3 },
    jobs: [
      { name: 'build', status: 'passed' },
      { name: 'race-handlers-3', status: 'passed', log: '/r/logs/race-handlers-3.log' },
      { name: 'e2e-webkit', status: 'passed', log: '/r/logs/e2e-webkit.log' },
    ],
    ...over,
  });
  const tonight = (over: Rec = {}) => ({
    night: '2026-10-07',
    at: '2026-10-06T22:00:00Z',
    phase: 'done',
    decision: 'ran',
    sha: C,
    subject: 'fix(race): the writehook waits for its flush',
    run_id: 'nightly-20261006-220000Z-cccccccc',
    result: '/r/result.json',
    latest: '/r/../latest-nightly.json',
    ...over,
  });
  const history = [
    ran({ night: '2026-10-05', sha: A, jobs: { build: 'passed', 'race-handlers-3': 'passed', 'e2e-webkit': 'failed' } }),
    ran({ night: '2026-10-06', sha: B, wall: '2h05m00s', secs: 7500, jobs: { build: 'passed', 'race-handlers-3': 'passed', 'e2e-webkit': 'failed' } }),
  ];

  it('says green, the wall time against the previous night, the commits since it, and what was fixed', () => {
    const r = composeReport({ tonight: tonight(), result: result(), history, commits, nowMs: now, tz });
    expect(r.severity).toBe('success');
    expect(r.title).toBe(`filex nightly 2026-10-07: green (${C.slice(0, 8)}, 2h15m00s)`);
    expect(r.message).toContain(`since the last run (2026-10-06, ${B.slice(0, 8)}): 1 commit(s)`);
    expect(r.message).toContain(`${C.slice(0, 8)} fix(race): the writehook waits for its flush`);
    expect(r.message).toContain('wall 2h15m00s, previous 2h05m00s (+10 min)');
    expect(r.message).toContain('fixed since 2026-10-06: e2e-webkit');
    expect(r.message).toMatch(/release evidence: pnpm release <version> --resume --chain/);
  });

  it("says a green job's warnings and makes the night a warning, not green and not red (#187, language packs behind the tree)", () => {
    // The maintainer, 2026-10-08: a night whose only failed screenshot scene is a
    // language pack behind the tree is a WARNING (the shots job exits 0 and
    // says JOBWARN, run.mjs keeps it as `warnings`); a release run stays red.
    const w = 'shots: the language packs are behind this tree, so langpack.mjs was not taken; every other scene was (150 of 151 pictures).';
    const warned = result({
      counts: { passed: 4, failed: 0, skipped: 0, total: 4 },
      jobs: [...result().jobs, { name: 'shots', status: 'passed', log: '/r/logs/shots.log', summary: 'shots=0 (warning: ...)', warnings: [w] }],
    });
    const r = composeReport({ tonight: tonight(), result: warned, history, commits, nowMs: now, tz });
    expect(r.severity).toBe('warning');
    expect(r.title).toBe(`filex nightly 2026-10-07: green, 1 job(s) with a warning (${C.slice(0, 8)}, 2h15m00s)`);
    expect(r.message).toContain(`WARNING shots: ${w}`);
    expect(r.message).not.toMatch(/^RED shots/m);
    // A red night stays red: a warning never softens it.
    const red = result({ ok: false, counts: { passed: 3, failed: 1, skipped: 0, total: 4 }, jobs: [...warned.jobs.slice(0, 2), { name: 'e2e-webkit', status: 'failed', log: '/r/logs/e2e-webkit.log', summary: 'e2e=1' }, warned.jobs[3]] });
    expect(composeReport({ tonight: tonight(), result: red, history, commits, nowMs: now, tz }).severity).toBe('danger');
  });

  it('says red, each red job with its summary and log, and the commits since it last passed (a deliberate red names its range)', () => {
    const red = result({
      ok: false,
      counts: { passed: 2, failed: 1, skipped: 0, total: 3 },
      jobs: [
        { name: 'build', status: 'passed' },
        { name: 'race-handlers-3', status: 'failed', summary: 'race=1 tests=210 failed=1 races=1', log: '/r/logs/race-handlers-3.log' },
        { name: 'e2e-webkit', status: 'passed' },
      ],
    });
    // race-handlers-3 last passed on the night of B: its range is B..C, the one commit since.
    const r = composeReport({ tonight: tonight(), result: red, history, commits, nowMs: now, tz });
    expect(r.severity).toBe('danger');
    expect(r.title).toBe(`filex nightly 2026-10-07: RED 1 of 3 (${C.slice(0, 8)}, 2h15m00s)`);
    expect(r.message).toContain('RED race-handlers-3 (new tonight): race=1 tests=210 failed=1 races=1');
    expect(r.message).toContain('  log /r/logs/race-handlers-3.log');
    expect(r.message).toContain(`last green 2026-10-06 on ${B.slice(0, 8)}; the range ${B.slice(0, 8)}..${C.slice(0, 8)}: 1 commit(s)`);
    expect(r.message).not.toMatch(/release evidence/);
  });

  it('tells a job red the night before too from a new red, and keeps the range from where it last passed', () => {
    const red = result({
      ok: false,
      counts: { passed: 2, failed: 1, skipped: 0, total: 3 },
      jobs: [
        { name: 'build', status: 'passed' },
        { name: 'race-handlers-3', status: 'passed' },
        { name: 'e2e-webkit', status: 'failed', summary: 'e2e=1 3 failed', log: '/r/logs/e2e-webkit.log' },
      ],
    });
    const r = composeReport({ tonight: tonight(), result: red, history, commits, nowMs: now, tz });
    expect(r.message).toContain('RED e2e-webkit (red on 2026-10-06 too): e2e=1 3 failed');
    expect(r.message).toContain('never green in the nights on record');
  });

  it('says so, quietly, when main did not move', () => {
    const r = composeReport({
      tonight: tonight({ decision: 'skipped', why: `main has not moved since the last nightly run (${C.slice(0, 8)}, green)`, run_id: undefined }),
      history: [...history, ran({ night: '2026-10-06', sha: C })],
      nowMs: now,
      tz,
    });
    expect(r.severity).toBe('info');
    expect(r.title).toBe(`filex nightly 2026-10-07: no run, main unchanged (${C.slice(0, 8)})`);
  });

  it('warns about a run still going, one that died, one that did not start, and a night with no record at all', () => {
    const going = composeReport({ tonight: tonight({ phase: 'running', pid: 4242 }), result: result({ finished: null, ok: null }), history, nowMs: now, tz, running: true });
    expect(going.severity).toBe('warning');
    expect(going.title).toMatch(/still running/);
    const died = composeReport({ tonight: tonight({ phase: 'running', pid: 4242 }), history, nowMs: now, tz, running: false });
    expect(died.title).toMatch(/died while running/);
    const refused = composeReport({ tonight: tonight({ decision: 'refused', why: 'inside the quiet window Mon 01:45-06:00 UTC', run_id: undefined }), history, nowMs: now, tz });
    expect(refused).toMatchObject({ severity: 'warning', title: expect.stringMatching(/did not run \(refused\)/) });
    expect(refused.message).toContain('inside the quiet window');
    const none = composeReport({ tonight: tonight({ at: '2026-10-05T22:00:00Z' }), history, nowMs: now, tz });
    expect(none).toMatchObject({ severity: 'warning', title: 'filex nightly 2026-10-07: no run recorded' });
    expect(composeReport({ tonight: null, history: [], nowMs: now, tz }).message).toMatch(/no earlier record/);
  });

  it('names the nightly build, and turns a failed build or publish into a warning', () => {
    const built = { decision: 'built', image: 'filex-nightly:cccccccc', version: '0.53.0-nightly.20261007+cccccccc', publishMode: 'manual', publishHint: 'node /x/nightly.mjs publish --env /e' };
    const ok = composeReport({ tonight: tonight({ build: built }), result: result(), history, commits, nowMs: now, tz });
    expect(ok.severity).toBe('success');
    expect(ok.message).toContain('nightly build: filex-nightly:cccccccc (0.53.0-nightly.20261007+cccccccc), not published (NIGHTLY_PUBLISH=manual): node /x/nightly.mjs publish --env /e');
    const failed = composeReport({ tonight: tonight({ build: { decision: 'failed', why: 'the export refused this commit', log: '/r/b.log' } }), result: result(), history, commits, nowMs: now, tz });
    expect(failed.severity).toBe('warning');
    expect(failed.message).toContain('nightly build FAILED: the export refused this commit (log /r/b.log)');
    const pushFailed = composeReport({ tonight: tonight({ build: { ...built, publishError: 'docker push failed' } }), result: result(), history, commits, nowMs: now, tz });
    expect(pushFailed.severity).toBe('warning');
    expect(pushFailed.message).toContain('publishing FAILED: docker push failed');
    // NIGHTLY_PUBLISH=auto: the image went out with the night, and the report says where.
    const refs = ['ghcr.io/brf-tech/filex:nightly', 'ghcr.io/brf-tech/filex:nightly-20261007'];
    const pushed = composeReport({ tonight: tonight({ build: { ...built, publishMode: 'auto', publishHint: undefined, published: { refs } } }), result: result(), history, commits, nowMs: now, tz });
    expect(pushed.severity).toBe('success');
    expect(pushed.message).toContain(`nightly build: filex-nightly:cccccccc (0.53.0-nightly.20261007+cccccccc), published as ${refs.join(', ')}`);
  });

  it('says s3-live was skipped on an unchanged night, and warns - without turning red - when the S3 code changed and nobody could test it', () => {
    const unchanged = composeReport({
      tonight: tonight({ s3live: { status: 'unchanged', since: B, why: `S3 driver unchanged since ${B.slice(0, 8)}` } }),
      result: result(),
      history,
      commits,
      nowMs: now,
      tz,
    });
    expect(unchanged.severity).toBe('success');
    expect(unchanged.message).toContain(`s3-live: skipped - S3 driver unchanged since ${B.slice(0, 8)}`);
    const untested = composeReport({
      tonight: tonight({
        s3live: {
          status: 'no-credentials',
          since: B,
          why: 'the S3 driver changed (1 file(s) changed since bbbbbbbb: backend/internal/storage/drivers/s3/s3.go) and the live tests did not run: no credentials (FILEX_TEST_S3_BUCKET)',
          hint: 'node /x/nightly.mjs s3-live --env /e --s3-env <a file with FILEX_TEST_S3_*>',
        },
      }),
      result: result(),
      history,
      commits,
      nowMs: now,
      tz,
    });
    expect(untested.severity).toBe('warning');
    expect(untested.title).toMatch(/: green \(/);
    expect(untested.message).toContain('WARNING s3-live: the S3 driver changed');
    expect(untested.message).toContain('Test it by hand: node /x/nightly.mjs s3-live --env /e --s3-env');
    // A night it ran says nothing extra: the job is in the result like any other.
    const ranIt = composeReport({ tonight: tonight({ s3live: { status: 'run', since: B, why: '1 file(s) changed' } }), result: result(), history, commits, nowMs: now, tz });
    expect(ranIt.message).not.toMatch(/s3-live/);
  });

  it("finds tonight's own line in the history and compares with the night before it", () => {
    const own = ran({ night: '2026-10-07', sha: C, run_id: 'nightly-20261006-220000Z-cccccccc', jobs: { build: 'passed' } });
    const r = composeReport({ tonight: tonight(), result: result(), history: [...history, own], commits, nowMs: now, tz });
    expect(r.message).toContain(`since the last run (2026-10-06, ${B.slice(0, 8)})`);
  });

  it('stays one notification Notify takes: at most 7900 characters, at most 25 jobs listed', () => {
    const jobs = Array.from({ length: 80 }, (_, i) => ({ name: `race-x-${i}`, status: 'failed', summary: 'x'.repeat(150), log: `/r/logs/race-x-${i}.log` }));
    const r = composeReport({ tonight: tonight(), result: result({ ok: false, jobs, counts: { passed: 0, failed: 80, skipped: 0, total: 80 } }), history, commits, nowMs: now, tz });
    expect(r.message.length).toBeLessThanOrEqual(7900);
    expect(notifyBody(r, { group: 'infra', source: 'filex-nightly' })).toEqual({ group: 'infra', source: 'filex-nightly', severity: 'danger', title: r.title, message: r.message });
  });

  it('diffs two nights job by job', () => {
    expect(jobsDiff({ a: 'passed', b: 'failed', c: 'failed' }, { a: 'failed', b: 'failed', c: 'passed', d: 'skipped' })).toEqual({
      newRed: ['a', 'd'],
      stillRed: ['b'],
      fixed: ['c'],
    });
    expect(lastGreenOf(history, 'race-handlers-3')?.night).toBe('2026-10-06');
    expect(lastGreenOf(history, 'e2e-webkit')).toBeNull();
  });
});

describe('what a night leaves', () => {
  it('names its run so that runs sort by time, and prunes only its own runs beyond the newest', () => {
    expect(nightlyRunId(ms('2026-10-06T22:00:05Z'), C)).toBe('nightly-20261006-220005Z-cccccccc');
    const names = ['latest-nightly.json', 'full-20261001-100000Z-aaaaaaaa', 'nightly-20261004-220000Z-aaaaaaaa', 'nightly-20261005-220000Z-bbbbbbbb', 'nightly-20261006-220000Z-cccccccc'];
    expect(runsToPrune(names, 2)).toEqual(['nightly-20261004-220000Z-aaaaaaaa']);
    expect(runsToPrune(names, 1, ['nightly-20261005-220000Z-bbbbbbbb'])).toEqual(['nightly-20261004-220000Z-aaaaaaaa']);
    expect(imagesToPrune(['cccccccc', 'bbbbbbbb', 'aaaaaaaa', 'latest'], 2)).toEqual(['aaaaaaaa']);
    expect(imagesToPrune(['cccccccc', 'bbbbbbbb', 'aaaaaaaa'], 1, ['aaaaaaaa'])).toEqual(['bbbbbbbb']);
  });

  it("keeps a night's record small: each job's status, not the whole result", () => {
    const fields = resultFields({ finished: 'x', ok: true, stopped: false, secs: 10, wall: '0h00m10s', counts: { passed: 1 }, jobs: [{ name: 'build', status: 'passed', log: '/l', summary: 's' }] });
    expect(fields).toEqual({ finished: true, ok: true, stopped: false, secs: 10, wall: '0h00m10s', counts: { passed: 1 }, jobs: { build: 'passed' } });
    expect(resultFields(null)).toEqual({ finished: false });
    const rec = nightRecord({ night: 'n', at: 'a', phase: 'done', pid: 1, decision: 'ran', sha: C, latest: '/l', build: { decision: 'built', image: 'i', publishHint: 'h', id: 'sha256:x' } });
    expect(rec).not.toHaveProperty('phase');
    expect(rec).not.toHaveProperty('pid');
    expect(rec.build).not.toHaveProperty('publishHint');
  });

  it('names the night by its local date', () => {
    expect(localDate(ms('2026-10-06T22:00:00Z'), 'Europe/Istanbul')).toBe('2026-10-07');
    expect(localDate(ms('2026-10-06T22:00:00Z'), 'UTC')).toBe('2026-10-06');
  });
});

describe('the settings', () => {
  const base = { CHAIN_ROOT: '/var/lib/filex-nightly', CHAIN_LOCK: '/var/lib/filex-chain/lock' };

  it("need the run's own directory and the host's shared lock, by name", () => {
    expect(() => nightlySettings({ CHAIN_LOCK: '/l' })).toThrow(/CHAIN_ROOT/);
    expect(() => nightlySettings({ CHAIN_ROOT: '/r' })).toThrow(/CHAIN_LOCK/);
    const cfg = nightlySettings(base);
    expect(cfg.lock).toBe('/var/lib/filex-chain/lock');
    expect(cfg.prefix).toBe('fxnightly');
    expect(cfg.extras).toEqual(['ds-go', 's3-live', 'shots', 'realenv']);
    expect(cfg.build).toMatchObject({ mode: 'on', publish: 'manual', pushRepo: '' });
  });

  it('refuse a publish mode or a build mode they do not know', () => {
    expect(() => nightlySettings({ ...base, NIGHTLY_PUBLISH: 'always' })).toThrow(/NIGHTLY_PUBLISH/);
    expect(() => nightlySettings({ ...base, NIGHTLY_BUILD: 'yes' })).toThrow(/NIGHTLY_BUILD/);
    expect(() => nightlySettings({ ...base, NIGHTLY_KEEP_RUNS: 'many' })).toThrow(/NIGHTLY_KEEP_RUNS/);
  });

  it('read the example as they read any settings file', () => {
    const env = parseEnvFile(read('nightly.env.example'));
    const cfg = nightlySettings(env);
    expect(cfg.prefix).not.toBe('fxchain');
    expect(env.CHAIN_DS_SUBNET).not.toBe('172.30.81.0/24');
    expect(cfg.tz).toBe('Europe/Istanbul');
  });

  it('publish every green night on the build host, the first one included (the owner, 2026-10-06)', () => {
    // ⚠ Until 2026-10-06 the example said `manual` and the first nightly image
    // was to be pushed by hand: a night could be green and its image sit on
    // the host until somebody remembered. The build host's settings are
    // written from this file (install-nightly.sh), so the decision lives here;
    // the code's default stays `manual` for a checkout that names no registry.
    const cfg = nightlySettings(parseEnvFile(read('nightly.env.example')));
    expect(cfg.build.publish).toBe('auto');
    expect(cfg.build.pushRepo).toBe('ghcr.io/brf-tech/filex');
    expect(nightlySettings(base).build.publish).toBe('manual');
  });

  it('sign a publish in with the user and the token together, or with neither - never one of them', () => {
    expect(registryLogin({ registryUser: 'brf-bot', registryTokenFile: '/var/lib/filex-nightly/registry.token' })).toBe('own');
    expect(registryLogin({})).toBe('host');
    expect(() => registryLogin({ registryTokenFile: '/t' })).toThrow(/NIGHTLY_REGISTRY_USER is not set/);
    expect(() => registryLogin({ registryUser: 'brf-bot' })).toThrow(/NIGHTLY_REGISTRY_TOKEN_FILE is not set/);
    // The publish goes through it before it tags or pushes anything.
    expect(read('nightly.mjs')).toMatch(/const login = registryLogin\(\{ registryUser, registryTokenFile \}\);/);
  });

  it('name the S3 settings a night lacks, and nothing of their values', () => {
    expect(nightlySettings(base).s3LiveMissing).toEqual(S3_LIVE_KEYS.required);
    const set = Object.fromEntries(S3_LIVE_KEYS.required.map((k: string) => [k, 'v']));
    expect(nightlySettings({ ...base, ...set }).s3LiveMissing).toEqual([]);
    const rec = nightRecord({ night: 'n', decision: 'ran', s3live: { status: 'no-credentials', since: A, why: 'w', hint: 'h', files: ['f'] } });
    expect(rec.s3live).toEqual({ status: 'no-credentials', since: A, why: 'w' });
  });

  it('parse the command lines of the run and the report', () => {
    expect(parseNightlyArgs(['s3-live', '--env', '/e', '--s3-env', '/root/s3-test.env'])).toEqual({ cmd: 's3-live', env: '/e', s3Env: '/root/s3-test.env' });
    expect(() => parseNightlyArgs(['s3-live', '--s3-env'])).toThrow(/--s3-env/);
    expect(parseNightlyArgs(['run', '--env', '/e', '--force'])).toEqual({ cmd: 'run', env: '/e', force: true });
    expect(parseNightlyArgs(['--dry-run'])).toEqual({ cmd: 'run', dryRun: true });
    expect(() => parseNightlyArgs(['deploy'])).toThrow(/unknown command/);
    expect(() => parseNightlyArgs(['run', '--env'])).toThrow(/--env/);
    expect(parseReportArgs(['--env', '/e', '--dry-run', '--wait-min', '0'])).toEqual({ env: '/e', dryRun: true, waitMin: 0 });
    expect(() => parseReportArgs(['--wait-min', 'soon'])).toThrow(/minutes/);
  });
});

describe('the timers and the installer', () => {
  const unit = (f: string) => read(path.join('systemd', f));

  it('start the run at night and the report in the morning, from the installed copies', () => {
    expect(unit('filex-nightly.timer')).toMatch(/^OnCalendar=\*-\*-\* @AT@$/m);
    expect(unit('filex-nightly.timer')).toMatch(/^Persistent=false$/m);
    expect(unit('filex-nightly-report.timer')).toMatch(/^OnCalendar=\*-\*-\* @REPORT_AT@$/m);
    expect(unit('filex-nightly.service')).toMatch(/^ExecStart=@NODE@ @BIN@\/nightly\.mjs run --env @ENV@$/m);
    expect(unit('filex-nightly.service')).toMatch(/^SuccessExitStatus=1$/m);
    expect(unit('filex-nightly-report.service')).toMatch(/^ExecStart=@NODE@ @BIN@\/report\.mjs --env @ENV@$/m);
  });

  it('installs every file the installed copies import, and fills every placeholder the units carry', () => {
    const sh = read('install-nightly.sh');
    const copied = /for f in ([a-z. -]+); do/.exec(sh)?.[1].split(' ') ?? [];
    for (const f of ['nightly.mjs', 'report.mjs']) {
      for (const m of read(f).matchAll(/^import [^;]* from '\.\/([^']+)';$/gm)) expect(copied, `${f} imports ${m[1]}`).toContain(m[1]);
    }
    for (const m of read('nightly-lib.mjs').matchAll(/^import [^;]* from '\.\/([^']+)';$/gm)) expect(copied).toContain(m[1]);
    const units = ['filex-nightly.service', 'filex-nightly.timer', 'filex-nightly-report.service', 'filex-nightly-report.timer'];
    for (const u of units) {
      expect(sh).toContain(u);
      for (const p of unit(u).match(/@[A-Z_]+@/g) ?? []) expect(sh, `${u}: ${p}`).toContain(`"s|${p}|`);
    }
    // It never overwrites the settings and never enables anything unasked.
    expect(sh).toMatch(/if \[ -e "\$ENV_FILE" \]; then/);
    expect(sh).toMatch(/if \[ "\$ENABLE" = 1 \]; then/);
    expect(sh).not.toMatch(/rm -rf/);
  });
});
