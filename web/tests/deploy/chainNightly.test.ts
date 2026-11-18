// The nightly (tasks #175, #361): when a night pushes, when it must not, what
// it pushes, and what the morning report says of GitHub's verdict on it.
// scripts/chain/nightly-lib.mjs holds the decisions as pure functions;
// scripts/chain/nightly.mjs and scripts/chain/report.mjs only read and write
// around them, and scripts/chain/nightly-export.sh and nightly-push.sh are
// run here against throwaway repositories.
//
// ⚠ Why this exists. Through 0.52 the build host's chain ran only on release
// days, by hand: the red jobs a night could have found were found on the day
// they cost the most (0.51: the mega menu broke 17 selectors and a writehook
// race surfaced after the cut). From 0.53 the build host ran the whole chain
// every night - three to four hours, nights that ran red for weeks on the
// host's own load - while GitHub ran the same matrix on every push of main.
// Since 2026-11-18 (#361; the owner and the maintainer's coordinator) the
// night is GitHub's: the build host only exports the tip and pushes it to
// the `nightly` branch, as ONE commit that names no private commit, by the
// account the public commits are by, and never while GitHub tests a release.
// A night is only worth it if it is quiet when nothing changed, says in one
// message what went red and which commits did it, and builds only from green.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { parseEnvFile } from '../../../scripts/chain/env.mjs';
import {
  PAGE_MARKERS,
  S3_LIVE_KEYS,
  S3_LIVE_PATHS,
  SNAP_ARCHES,
  ciVerdict,
  classifyDesktopFile,
  composeReport,
  decidePush,
  desktopManifest,
  desktopVersion,
  followLines,
  followTrouble,
  followVerdict,
  imageLine,
  jobsDiff,
  lastGreenOf,
  lastPushed,
  localDate,
  nightRecord,
  nightlyCommitMessage,
  nightlyLabels,
  nightlyMeta,
  nightlySettings,
  nightlyTags,
  nightlyVersion,
  parseAuthor,
  platformOfRow,
  releaseBusy,
  renderNightlyPage,
  s3LiveFile,
  s3Touched,
  snapEdgeState,
  snapUploadVerdict,
  wallOf,
} from '../../../scripts/chain/nightly-lib.mjs';
import { rowsOf } from '../../../scripts/chain/nightly-desktop-manifest.mjs';
import { parseNightlyArgs, readHistory, recordCi } from '../../../scripts/chain/nightly.mjs';
import { edgeSummary } from '../../../scripts/chain/nightly-snap-edge.mjs';
import { notifyBody, parseReportArgs } from '../../../scripts/chain/report.mjs';
import { findBash } from '../../../scripts/release/engine.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const CHAIN = path.join(REPO, 'scripts', 'chain');
const read = (rel: string) => fs.readFileSync(path.join(CHAIN, rel), 'utf8');
const bash = findBash();
const fwd = (p: string) => p.split(path.sep).join('/');

const A = 'a'.repeat(40);
const B = 'b'.repeat(40);
const C = 'c'.repeat(40);
const P = '1'.repeat(40);
const ms = (s: string) => Date.parse(s);

type Rec = Record<string, unknown>;

describe('whether a night pushes', () => {
  it('pushes the first night, and every night main moved', () => {
    expect(decidePush({ head: A, last: null })).toEqual({ push: true, why: 'the first nightly' });
    expect(decidePush({ head: B, last: { sha: A } })).toEqual({ push: true, why: `main moved: ${A.slice(0, 8)}..${B.slice(0, 8)}` });
  });

  it('does not push again on a main that has not moved, unless forced', () => {
    expect(decidePush({ head: A, last: { sha: A, result: 'pushed' } })).toEqual({ push: false, why: `main has not moved since the last nightly (${A.slice(0, 8)})` });
    expect(decidePush({ head: A, last: { sha: A }, force: true }).push).toBe(true);
  });

  it('tries again after a night whose export was refused or whose push failed: such a night leaves no last export', () => {
    const mjs = read('nightly.mjs');
    // Only a push, and an export equal to the public main, move it.
    expect(mjs.match(/state\.last_export = \{ sha: head, result: '(pushed|unchanged)'/g)?.length).toBe(2);
    expect(mjs.indexOf("state.last_export = { sha: head, result: 'pushed'")).toBeGreaterThan(mjs.indexOf("if (pushed.state !== 'pushed')"));
  });
});

describe('a release on GitHub keeps the night back (NIGHTLY_RELEASE_BUSY)', () => {
  const run = (over: Rec) => ({ status: 'in_progress', path: '.github/workflows/ci.yml', event: 'push', head_branch: 'main', head_sha: A, html_url: 'https://github.com/x/y/actions/runs/1', ...over });

  it("waits while release.yml runs or ci.yml tests a push of main - the run a release's gate reads", () => {
    expect(releaseBusy([run({})])).toEqual({ url: 'https://github.com/x/y/actions/runs/1', why: `ci.yml tests a push of main (${A.slice(0, 8)})` });
    expect(releaseBusy([run({ path: '.github/workflows/release.yml', event: 'push', head_branch: 'v0.62.0', display_title: 'v0.62.0' })])?.why).toBe('release.yml runs (v0.62.0)');
    expect(releaseBusy([run({ path: '.github/workflows/release.yml@refs/heads/main', event: 'workflow_dispatch', status: 'queued' })])).not.toBeNull();
  });

  it("does not wait for a pull request, the night's own runs, the nightly builds or a run that is over", () => {
    expect(releaseBusy([])).toBeNull();
    expect(releaseBusy([run({ event: 'pull_request', head_branch: 'feat/x' })])).toBeNull();
    expect(releaseBusy([run({ head_branch: 'nightly' })])).toBeNull();
    expect(releaseBusy([run({ path: '.github/workflows/nightly-desktop.yml', event: 'workflow_run' })])).toBeNull();
    expect(releaseBusy([run({ status: 'completed' })])).toBeNull();
  });

  it('does not wait for a run GitHub left queued for good: the first night met one from three months before', () => {
    const now = ms('2026-11-18T04:58:00Z');
    const ghost = run({ status: 'queued', path: '.github/workflows/release.yml', head_branch: 'v0.10.1', created_at: '2026-08-06T16:36:29Z' });
    expect(releaseBusy([ghost], { nowMs: now })).toBeNull();
    expect(releaseBusy([{ ...ghost, created_at: '2026-11-18T03:30:00Z' }], { nowMs: now })?.why).toBe('release.yml runs (v0.10.1)');
    expect(releaseBusy([{ ...ghost, created_at: '2026-11-17T16:00:00Z' }], { nowMs: now, maxAgeH: 12 })).toBeNull();
    expect(releaseBusy([{ ...ghost, created_at: '2026-11-17T18:00:00Z' }], { nowMs: now, maxAgeH: 12 })).not.toBeNull();
    expect(read('nightly.mjs')).toContain('releaseBusy(await activeRuns(cfg), { nowMs: Date.now() })');
  });

  it('asks GitHub only when the night would push, and pushes anyway when GitHub cannot be asked', () => {
    const mjs = read('nightly.mjs');
    expect(mjs.indexOf('releaseBusy(await activeRuns(cfg)')).toBeGreaterThan(mjs.indexOf('if (!d.push) return record'));
    expect(mjs).toMatch(/could not ask GitHub whether a release runs \(\$\{e\.message\}\): pushing anyway/);
    expect(mjs).toMatch(/for \(const status of \['in_progress', 'queued'\]\)/);
  });
});

describe("the night's commit", () => {
  it('names the night and the public commit it is made on - never the private one (lesson #55)', () => {
    const m = nightlyCommitMessage({ date: '2026-11-19', base: P });
    expect(m.split('\n')[0]).toBe('nightly 2026-11-19: the tip of main, as the next release would export it');
    expect(m).toContain(`commit on top of ${P.slice(0, 8)}.`);
    expect(m).toMatch(/\n\nNightly-Date: 2026-11-19\n$/);
    expect(m).not.toMatch(/[0-9a-f]{9,}/);
    expect(() => nightlyCommitMessage({ date: '20261119', base: P })).toThrow(/date/);
    // The script fills the public commit in once it fetched it.
    expect(nightlyCommitMessage({ date: '2026-11-19', base: '@BASE@' })).toContain('commit on top of @BASE@.');
    expect(read('nightly-export.sh')).toContain('sed "s/@BASE@/${base:0:8}/g" "$NE_MESSAGE_FILE"');
  });

  it('is by the account the settings name, "Name <address>"; the code names nobody', () => {
    expect(parseAuthor('nightly-bot <bot@example.com>')).toEqual({ name: 'nightly-bot', email: 'bot@example.com' });
    for (const bad of ['', 'nightly-bot', '<bot@example.com>', 'a <b>', 'a <b@c> d']) expect(parseAuthor(bad), bad).toBeNull();
    expect(read('nightly.env.example')).toMatch(/^NIGHTLY_COMMIT_AUTHOR=$/m);
    expect(read('nightly.mjs')).toMatch(/NIGHTLY_COMMIT_AUTHOR is not set/);
  });
});

describe("the nightly's version, tags and labels", () => {
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

  it('gives every desktop package one version, cut to what the Snap Store takes', () => {
    expect(desktopVersion('0.57.0-nightly.20261014+2ca25999')).toBe('0.57.0-nightly.20261014+2ca25999');
    expect(desktopVersion('0.57.0-nightly.20261014+2ca25999').length).toBe(32);
    expect(desktopVersion('0.100.0-nightly.20261014+2ca25999')).toBe('0.100.0-nightly.20261014+2ca2599');
    expect(desktopVersion('10.100.0-nightly.20261014+2ca25999')).toBe('10.100.0-n.20261014+2ca2599');
    expect(() => desktopVersion('100.1000.10000-nightly.20261014+2ca25999')).toThrow(/snap version/);
  });

  it("takes the night from the commit's trailer - the build host's - and a commit pushed by hand from its own time", () => {
    const meta = nightlyMeta({ released: '0.61.1', sha: C, message: 'nightly\n\nNightly-Date: 2026-11-19\n', committedMs: ms('2026-11-18T23:00:00Z') });
    expect(meta).toEqual({
      revision: C,
      version: `0.62.0-nightly.20261119+${C.slice(0, 8)}`,
      desktop_version: `0.62.0-nightly.20261119+${C.slice(0, 8)}`,
      date: '2026-11-19',
      ymd: '20261119',
      tags: 'nightly,nightly-20261119',
    });
    expect(nightlyMeta({ released: '0.61.1', sha: C, message: 'by hand', committedMs: ms('2026-11-18T23:00:00Z') }).date).toBe('2026-11-18');
    expect(() => nightlyMeta({ released: '0.61.1', sha: C, message: 'by hand' })).toThrow(/names no night/);
  });

  it('says from outside which commit it is and that CI was green on it (#266)', () => {
    const labels = nightlyLabels({ counts: { passed: 40, failed: 0, skipped: 2, total: 42 }, run: 'https://github.com/BRF-Tech/filex/actions/runs/1' });
    expect(labels).toEqual({
      'org.opencontainers.image.source': 'https://github.com/BRF-Tech/filex',
      'org.opencontainers.image.description': expect.stringMatching(/^filex nightly build: .*Not a release and not supported/),
      'sh.filex.nightly.tests': 'green: 40 passed, 2 skipped of 42 jobs (https://github.com/BRF-Tech/filex/actions/runs/1)',
    });
    expect(nightlyLabels({})['sh.filex.nightly.tests']).toBeUndefined();
    // The labels go to the build one a line: no value may break a line.
    for (const v of Object.values(labels)) expect(v).not.toMatch(/[\r\n]/);
  });
});

describe('the export and the push, against throwaway repositories', () => {
  const ID = { GIT_CONFIG_NOSYSTEM: '1' };
  const sh = (cwd: string, args: string[], env: Record<string, string> = {}) => {
    const r = spawnSync('git', ['-c', 'user.name=t', '-c', 'user.email=t@example.com', '-C', cwd, ...args], { encoding: 'utf8', env: { ...process.env, ...env } });
    if (r.status !== 0) throw new Error(`git ${args.join(' ')}: ${r.stderr}`);
    return r.stdout.trim();
  };
  /**
   * A public repository (bare, its main one commit), a private checkout with
   * a stand-in scripts/export-public.sh (STUB_MODE: change writes a file,
   * same changes nothing, refuse refuses), and the directories the scripts
   * write to. No global git identity: the scripts must set their own.
   */
  function rig() {
    const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'nightly-export-'));
    const seed = path.join(tmp, 'seed');
    fs.mkdirSync(seed);
    sh(tmp, ['init', '-q', '-b', 'main', 'seed']);
    fs.writeFileSync(path.join(seed, 'README.md'), 'filex\n');
    sh(seed, ['add', '-A']);
    sh(seed, ['commit', '-q', '-m', 'v0.61.1']);
    const pub = path.join(tmp, 'public.git');
    sh(tmp, ['clone', '-q', '--bare', fwd(seed), fwd(pub)]);
    const priv = path.join(tmp, 'private');
    fs.mkdirSync(path.join(priv, 'scripts'), { recursive: true });
    sh(tmp, ['init', '-q', '-b', 'main', 'private']);
    fs.writeFileSync(
      path.join(priv, 'scripts', 'export-public.sh'),
      [
        '#!/usr/bin/env bash',
        'set -e',
        'case "${STUB_MODE:-change}" in',
        '  refuse) echo "Refusing to call this export good: a private name"; exit 1 ;;',
        '  same) git -C "$1" add -A ;;',
        '  *) echo "${STUB_MODE:-change}" > "$1/NIGHTLY.txt"; git -C "$1" add -A ;;',
        'esac',
        '',
      ].join('\n'),
    );
    sh(priv, ['add', '-A']);
    sh(priv, ['commit', '-q', '-m', 'private main']);
    const empty = path.join(tmp, 'gitconfig');
    fs.writeFileSync(empty, '');
    return { tmp, pub, priv, privSha: sh(priv, ['rev-parse', 'HEAD']), base: sh(pub, ['rev-parse', 'main']), clone: path.join(tmp, 'clone'), out: path.join(tmp, 'out'), env: { ...ID, GIT_CONFIG_GLOBAL: fwd(empty) } };
  }
  const exportOf = (r: ReturnType<typeof rig>, mode: string, sha = r.privSha) => {
    fs.mkdirSync(r.out, { recursive: true });
    const message = path.join(r.tmp, 'message.txt');
    fs.writeFileSync(message, nightlyCommitMessage({ date: '2026-11-19', base: '@BASE@' }));
    return spawnSync(bash!, [fwd(path.join(CHAIN, 'nightly-export.sh'))], {
      encoding: 'utf8',
      env: {
        ...process.env,
        ...r.env,
        STUB_MODE: mode,
        NE_SRC: fwd(r.priv),
        NE_SHA: sha,
        NE_PUBLIC_DIR: fwd(r.clone),
        NE_PUBLIC_REMOTE: fwd(r.pub),
        NE_OUT: fwd(r.out),
        NE_AUTHOR_NAME: 'nightly-bot',
        NE_AUTHOR_EMAIL: 'bot@example.com',
        NE_MESSAGE_FILE: fwd(message),
      },
    });
  };

  it.runIf(!!bash)('commits the export on the public main as one commit by the account named, and bundles it with the public main as its prerequisite', () => {
    const r = rig();
    try {
      const e = exportOf(r, 'change');
      expect(e.status, `${e.stdout}${e.stderr}`).toBe(0);
      const [sha, base] = fs.readFileSync(path.join(r.out, 'commit.txt'), 'utf8').trim().split(' ');
      expect(base).toBe(r.base);
      expect(sh(r.clone, ['rev-parse', `${sha}^`])).toBe(r.base);
      expect(sh(r.clone, ['log', '-1', '--format=%an <%ae>|%cn <%ce>', sha])).toBe('nightly-bot <bot@example.com>|nightly-bot <bot@example.com>');
      const message = sh(r.clone, ['log', '-1', '--format=%B', sha]);
      expect(message).toContain(`commit on top of ${r.base.slice(0, 8)}.`);
      expect(message).toContain('Nightly-Date: 2026-11-19');
      expect(message).not.toContain(r.privSha.slice(0, 8));
      expect(sh(r.clone, ['show', '--name-only', '--format=', sha])).toBe('NIGHTLY.txt');
      // The bundle needs the public main and brings the commit alone.
      const other = path.join(r.tmp, 'other');
      sh(r.tmp, ['clone', '-q', '--no-checkout', fwd(r.pub), fwd(other)]);
      sh(other, ['bundle', 'verify', '-q', fwd(path.join(r.out, 'nightly.bundle'))]);
      sh(other, ['fetch', '-q', fwd(path.join(r.out, 'nightly.bundle')), 'refs/heads/filex-nightly:refs/heads/got']);
      expect(sh(other, ['rev-parse', 'got'])).toBe(sha);
    } finally {
      fs.rmSync(r.tmp, { recursive: true, force: true });
    }
  });

  it.runIf(!!bash)('pushes nothing when the export is the public main as it is, and nothing the export refused; never from a checkout at another commit', () => {
    const r = rig();
    try {
      const same = exportOf(r, 'same');
      expect(same.status, `${same.stdout}${same.stderr}`).toBe(3);
      expect(fs.readFileSync(path.join(r.out, 'commit.txt'), 'utf8').trim()).toBe(r.base);
      expect(fs.existsSync(path.join(r.out, 'nightly.bundle'))).toBe(false);
      const refused = exportOf(r, 'refuse');
      expect(refused.status).toBe(1);
      expect(refused.stdout).toMatch(/the export refused/);
      expect(sh(r.clone, ['status', '--porcelain'])).toBe('');
      expect(sh(r.clone, ['rev-parse', 'HEAD'])).toBe(r.base);
      expect(exportOf(r, 'change', A).status).toBe(2);
    } finally {
      fs.rmSync(r.tmp, { recursive: true, force: true });
    }
  });

  it.runIf(!!bash)("pushes from the workspace's own clone, through the export gate, under its wrapper - and replaces the branch, never main", () => {
    const r = rig();
    try {
      expect(exportOf(r, 'change').status).toBe(0);
      const [sha] = fs.readFileSync(path.join(r.out, 'commit.txt'), 'utf8').trim().split(' ');
      // The gate's installer and the wrapper, stood in for: each says it ran.
      const gate = path.join(r.tmp, 'gate');
      fs.mkdirSync(path.join(gate, 'scripts', 'hooks'), { recursive: true });
      fs.writeFileSync(path.join(gate, 'scripts', 'hooks', 'install-export-hook.sh'), `echo "gate $1" >> "${fwd(r.tmp)}/gate.log"\n`);
      const wrap = path.join(r.tmp, 'wrap.sh');
      fs.writeFileSync(wrap, `#!/usr/bin/env bash\necho wrapped >> "${fwd(r.tmp)}/wrap.log"\nexec "$@"\n`);
      fs.chmodSync(wrap, 0o755);
      const dir = path.join(r.tmp, 'ws-clone');
      const push = (bundle: string, commit: string, branch: string) =>
        spawnSync(bash!, ['-s', '--', fwd(bundle), commit, branch, fwd(wrap), fwd(dir), fwd(gate), fwd(r.pub)], {
          input: read('nightly-push.sh'),
          encoding: 'utf8',
          env: { ...process.env, ...r.env, HOME: fwd(r.tmp) },
        });
      const bundle = path.join(r.out, 'nightly.bundle');
      const ok = push(bundle, sha, 'nightly');
      expect(ok.status, `${ok.stdout}${ok.stderr}`).toBe(0);
      expect(sh(r.pub, ['rev-parse', 'refs/heads/nightly'])).toBe(sha);
      expect(fs.readFileSync(path.join(r.tmp, 'gate.log'), 'utf8').trim()).toBe(`gate ${fwd(dir)}`);
      expect(fs.readFileSync(path.join(r.tmp, 'wrap.log'), 'utf8').trim()).toBe('wrapped');
      // The next night's commit is not a descendant of this one: the branch is replaced.
      fs.renameSync(bundle, path.join(r.tmp, 'first.bundle'));
      expect(exportOf(r, 'another night').status).toBe(0);
      const [second] = fs.readFileSync(path.join(r.out, 'commit.txt'), 'utf8').trim().split(' ');
      expect(push(bundle, second, 'nightly').status).toBe(0);
      expect(sh(r.pub, ['rev-parse', 'refs/heads/nightly'])).toBe(second);
      // Never main, never a commit the bundle does not carry.
      expect(push(bundle, second, 'main').status).toBe(2);
      expect(sh(r.pub, ['rev-parse', 'refs/heads/main'])).toBe(r.base);
      const wrong = push(bundle, sha, 'nightly');
      expect(wrong.status).toBe(2);
      expect(wrong.stdout).toMatch(/the bundle carries/);
    } finally {
      fs.rmSync(r.tmp, { recursive: true, force: true });
    }
  });

  it('sends the push script on stdin, removes the bundle it copied in, and never reads the token itself', () => {
    const mjs = read('nightly.mjs');
    expect(mjs).toMatch(/\['exec', '-i', \.\.\.\(p\.user \? \['-u', p\.user\] : \[\]\), p\.container, 'bash', '-l', '-s', '--', inside, sha, p\.branch, p\.wrap, p\.dir, p\.gateSrc, p\.remote\]/);
    expect(mjs).toMatch(/spawnSync\('docker', argv, \{ input: script,/);
    expect(mjs).toMatch(/spawnSync\('docker', \['exec', '-u', '0', p\.container, 'rm', '-f', inside\]/);
    for (const f of ['nightly.mjs', 'nightly-push.sh', 'nightly-export.sh']) expect(read(f), f).not.toMatch(/\b(?:GH_TOKEN|GITHUB_TOKEN)\b/);
    // The export runs from tonight's tree; nothing here builds or tests.
    expect(mjs).toMatch(/spawnSync\('bash', \[path\.join\(cfg\.src, 'scripts', 'chain', 'nightly-export\.sh'\)\]/);
    for (const f of ['nightly.mjs', 'nightly-export.sh', 'nightly-push.sh']) {
      expect(read(f), f).not.toMatch(/run\.sh|docker build|snapcraft (?:upload|pack)|oras push|rm -rf/);
    }
    expect(read('nightly-export.sh')).toMatch(/bash "\$NE_SRC\/scripts\/export-public\.sh" "\$dir"/);
    expect(read('nightly-export.sh')).not.toMatch(/git (?:-C "\$dir" )?push/);
  });

  it('says it when a pushed change touched the S3 code nothing tests on its own any more', () => {
    expect(s3Touched(['backend/internal/storage/drivers/s3/multipart.go', 'web/src/App.vue'])).toEqual(['backend/internal/storage/drivers/s3/multipart.go']);
    expect(s3Touched(['backend/go.mod'], '-\tgithub.com/aws/aws-sdk-go-v2 v1.30.0\n+\tgithub.com/aws/aws-sdk-go-v2 v1.31.0\n')).toEqual(['backend/go.mod']);
    expect(s3Touched(['backend/go.mod'], '+\tgolang.org/x/net v0.30.0\n')).toEqual([]);
    for (const p of ['backend/internal/storage/drivers/s3/', 'backend/internal/storage/stall/', 'backend/internal/testutil/lives3/', 'scripts/chain/job/s3-live.sh']) expect(S3_LIVE_PATHS).toContain(p);
  });
});

describe('the live S3 job of the chain (s3-live, a person\'s run)', () => {
  const all = { FILEX_TEST_S3_BUCKET: 'b', FILEX_TEST_S3_ACCESS_KEY: 'k', FILEX_TEST_S3_SECRET_KEY: 's', FILEX_TEST_S3_REGION: 'eu-central-003', FILEX_TEST_S3_ENDPOINT: 'https://s3.example.com' };

  it('hands the job exactly the FILEX_TEST_S3_* settings, one line each, and nothing else', () => {
    expect(s3LiveFile({ ...all, OTHER: 'x' }).text).toBe(
      'FILEX_TEST_S3_BUCKET=b\nFILEX_TEST_S3_ACCESS_KEY=k\nFILEX_TEST_S3_SECRET_KEY=s\nFILEX_TEST_S3_REGION=eu-central-003\nFILEX_TEST_S3_ENDPOINT=https://s3.example.com\n',
    );
    expect(() => s3LiveFile({ ...all, FILEX_TEST_S3_SECRET_KEY: 'a\nb' })).toThrow(/line break/);
  });

  it('names - and only names - what is missing, the region included (lesson #927)', () => {
    expect(s3LiveFile({}).missing).toEqual(S3_LIVE_KEYS.required);
    expect(s3LiveFile({ ...all, FILEX_TEST_S3_REGION: '' }).missing).toEqual(['FILEX_TEST_S3_REGION']);
  });
});

describe("GitHub's verdict on a nightly", () => {
  const run = (over: Rec = {}) => ({ id: 7, html_url: 'https://github.com/x/y/actions/runs/7', run_attempt: 1, status: 'completed', conclusion: 'success', run_started_at: '2026-11-18T23:02:00Z', updated_at: '2026-11-19T00:04:00Z', ...over });
  const jobs = (over: Rec = {}) =>
    Object.entries({ 'Go vet + build': 'success', 'Go shard 1/8': 'success', 'Playwright (webkit 2/4)': 'success', 'Playwright DS': 'skipped', 'All tests (full)': 'success', ...over }).map(([name, conclusion]) => ({
      name,
      conclusion,
      html_url: `https://github.com/x/y/actions/runs/7/job/${name.length}`,
    }));

  it('is the verdict of `All tests (full)`, with every part counted', () => {
    const v = ciVerdict({ run: run(), jobs: jobs() });
    expect(v).toMatchObject({ state: 'green', run_id: 7, all: 'success', counts: { passed: 4, failed: 0, skipped: 1, total: 5 }, secs: 3720, bad: [] });
    expect(v.jobs).toMatchObject({ 'Go shard 1/8': 'passed', 'Playwright DS': 'skipped' });
    expect(wallOf(v.secs)).toBe('1h02m');
  });

  it('is red with the parts that failed, and their pages', () => {
    const v = ciVerdict({ run: run({ conclusion: 'failure' }), jobs: jobs({ 'Playwright (webkit 2/4)': 'failure', 'All tests (full)': 'failure' }) });
    expect(v.state).toBe('red');
    expect(v.bad).toEqual([
      { name: 'Playwright (webkit 2/4)', url: 'https://github.com/x/y/actions/runs/7/job/23', conclusion: 'failure' },
      { name: 'All tests (full)', url: 'https://github.com/x/y/actions/runs/7/job/16', conclusion: 'failure' },
    ]);
  });

  it('says a run not over, a cancelled one, and no run at all for what they are', () => {
    expect(ciVerdict({ run: null })).toEqual({ state: 'not-found' });
    expect(ciVerdict({ run: run({ status: 'in_progress', conclusion: null }) })).toMatchObject({ state: 'running', status: 'in_progress' });
    expect(ciVerdict({ run: run({ conclusion: 'cancelled' }), jobs: jobs({ 'All tests (full)': 'cancelled' }) }).state).toBe('cancelled');
    // No verdict job: the run's own conclusion.
    expect(ciVerdict({ run: run({ conclusion: 'failure' }), jobs: [] }).state).toBe('red');
  });

  it('is written into the night it belongs to once it is final, for the nights after it to compare with', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'nightly-state-'));
    try {
      const pushed = { night: '2026-11-19', decision: 'pushed', sha: A, push: { sha: C, branch: 'nightly' } };
      fs.writeFileSync(path.join(dir, 'history.jsonl'), `${JSON.stringify({ night: '2026-11-18', decision: 'skipped', sha: A })}\n${JSON.stringify(pushed)}\n`);
      fs.writeFileSync(path.join(dir, 'tonight.json'), JSON.stringify({ ...pushed, phase: 'done' }));
      expect(recordCi(dir, C, { state: 'running' })).toBe(false);
      const v = ciVerdict({ run: run(), jobs: jobs() });
      expect(recordCi(dir, C, v)).toBe(true);
      const h = readHistory(dir);
      expect(h[1]).toMatchObject({ ci: { state: 'green', run_id: 7, counts: v.counts, secs: 3720 }, jobs: v.jobs });
      expect(h[0]).not.toHaveProperty('ci');
      expect(JSON.parse(fs.readFileSync(path.join(dir, 'tonight.json'), 'utf8')).ci.state).toBe('green');
      expect(recordCi(dir, B, v)).toBe(false);
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
});

describe("GitHub's packages of a night and filex.sh/nightly (#266)", () => {
  const now = ms('2026-11-19T04:00:00Z');
  const version = '0.62.0-nightly.20261119+cccccccc';
  const source = { revision: C, version, at: '2026-11-18T23:01:00Z' };

  it('knows what each package is from its name, and leaves out what is none', () => {
    expect(classifyDesktopFile('filex-desktop-x64.exe')).toEqual({ platform: 'windows', arch: 'x64', kind: 'installer' });
    expect(classifyDesktopFile('filex-desktop-portable-arm64.exe')).toEqual({ platform: 'windows', arch: 'arm64', kind: 'portable' });
    expect(classifyDesktopFile('filex-desktop-arm64.dmg')).toEqual({ platform: 'macos', arch: 'arm64', kind: 'dmg' });
    expect(classifyDesktopFile('filex-desktop-x86_64.AppImage')).toEqual({ platform: 'linux', arch: 'x64', kind: 'AppImage' });
    expect(classifyDesktopFile('filex-desktop-amd64.deb')).toEqual({ platform: 'linux', arch: 'x64', kind: 'deb' });
    expect(classifyDesktopFile('filex-desktop-aarch64.rpm')).toEqual({ platform: 'linux', arch: 'arm64', kind: 'rpm' });
    for (const n of ['filex-desktop-x64.exe.blockmap', 'latest.yml', 'filex-desktop-arm64.snap', 'filex-desktop-portable-x64.dmg', 'filex-desktop-ppc64.deb', 'filex-nightly-src.tar.gz']) {
      expect(classifyDesktopFile(n), n).toBeNull();
    }
    expect(platformOfRow('mac')).toBe('macos');
    expect(platformOfRow('linux-arm64')).toBe('linux');
    expect(platformOfRow('snap-arm64')).toBeNull();
    expect(platformOfRow('snap')).toBeNull();
  });

  it("writes the night's manifest: the packages in the page's order, every expected row, both snaps", () => {
    const m = desktopManifest({
      files: [
        { name: 'filex-desktop-amd64.deb', size: 3, sha256: 'd' },
        { name: 'filex-desktop-arm64.exe', size: 2, sha256: 'b' },
        { name: 'filex-desktop-x64.exe', size: 1, sha256: 'a' },
        { name: 'notes.txt', size: 1, sha256: 'z' },
      ],
      version,
      revision: C,
      date: '2026-11-19',
      rows: rowsOf(['windows built\n', 'linux-x64 built\nsnap built\n', 'snap-arm64 failed\n'], ['windows', 'linux-x64', 'snap', 'mac', 'snap-arm64']),
      snap: { amd64: { state: 'released', revision: 50 }, arm64: { state: 'skipped', why: 'skipped: no Snap Store credential' } },
    });
    expect(m.files.map((f) => f.name)).toEqual(['filex-desktop-x64.exe', 'filex-desktop-arm64.exe', 'filex-desktop-amd64.deb']);
    expect(m.rows).toEqual({ windows: 'built', 'linux-x64': 'built', snap: 'built', mac: 'failed', 'snap-arm64': 'failed' });
    expect(m).toMatchObject({ schema: 1, version, date: '2026-11-19', snap: { amd64: { state: 'released' }, arm64: { state: 'skipped' } } });
    expect(SNAP_ARCHES).toEqual(['amd64', 'arm64']);
    expect(read('nightly-desktop-manifest.mjs')).toContain('`snap-${arch}.json`');
  });

  const template = path.join(REPO, 'site', 'nightly.src.html');
  it.runIf(fs.existsSync(template))("makes the page from the site's own template: every platform, what was not built, the warning, nothing unescaped", () => {
    const tpl = fs.readFileSync(template, 'utf8');
    for (const m of PAGE_MARKERS) expect(tpl).toContain(`<!--nightly:${m}-->`);
    const manifest = desktopManifest({
      files: [
        { name: 'filex-desktop-x64.exe', size: 104857600, sha256: 'a' },
        { name: 'filex-desktop-x86_64.AppImage', size: 2097152, sha256: 'b' },
      ],
      version: '0.62.0-nightly.1+<b>',
      revision: C,
      date: '2026-11-19',
      run: 'https://github.com/BRF-Tech/filex/actions/runs/1',
      rows: { windows: 'built', 'linux-x64': 'built', 'linux-arm64': 'failed', mac: 'failed' },
    });
    const page = renderNightlyPage({ template: tpl, manifest });
    expect(page).not.toMatch(/<!--nightly:/);
    expect(page).toContain('0.62.0-nightly.1+&lt;b&gt;');
    expect(page).toContain('<a href="./filex-desktop-x64.exe">Installer (.exe)</a> <span class="meta">x64 · 100 MB</span>');
    expect(page).toContain('<a href="./filex-desktop-x86_64.AppImage">AppImage</a> <span class="meta">x64 · 2.0 MB</span>');
    expect(page).toMatch(/<h2>macOS<\/h2>\n<p class="meta">Not built this night\.<\/p>/);
    expect(page).toContain('<p class="meta">Not built this night: linux-arm64.</p>');
    expect(page).toContain('<a href="./SHA256SUMS">SHA256SUMS</a>');
    expect(page).toContain('Not supported.');
    expect(page).toContain(C.slice(0, 8));
    expect(() => renderNightlyPage({ template: tpl.replace('<!--nightly:downloads-->', ''), manifest })).toThrow(/nightly:downloads/);
  });

  it('follows the last nightly: built, building, failed, none when CI was not green, waiting while CI runs, not taken after four hours', () => {
    const at = (r: string) => ({ revision: r });
    expect(followVerdict({ source: null, nowMs: now })).toBeNull();
    expect(followVerdict({ source, built: at(C), nowMs: now })).toMatchObject({ state: 'built', version });
    expect(followVerdict({ source, built: at('old'), attempted: at(C), run: { status: 'in_progress', url: 'u' }, nowMs: now })).toMatchObject({ state: 'building', url: 'u' });
    expect(followVerdict({ source, attempted: at(C), run: { status: 'completed', conclusion: 'failure', url: 'u' }, nowMs: now })).toMatchObject({ state: 'failed', conclusion: 'failure' });
    expect(followVerdict({ source, attempted: at(C), run: { status: 'completed', conclusion: 'success', url: 'u' }, nowMs: now }).why).toMatch(/published no packages/);
    expect(followVerdict({ source, ci: { state: 'red' }, nowMs: now })).toMatchObject({ state: 'none', why: 'ci.yml was red on it' });
    expect(followVerdict({ source, ci: { state: 'running' }, nowMs: ms('2026-11-19T06:00:00Z') })).toMatchObject({ state: 'waiting' });
    expect(followVerdict({ source, ci: { state: 'green' }, nowMs: ms('2026-11-19T02:00:00Z') })).toMatchObject({ state: 'waiting' });
    expect(followVerdict({ source, ci: { state: 'green' }, nowMs: ms('2026-11-19T03:30:00Z') })).toMatchObject({ state: 'not-started', hours: 4 });
  });

  it('says it in the morning report - a warning when GitHub failed, a row is missing, the page or a snap did not go out; never red', () => {
    const built = {
      state: 'built',
      version,
      platforms: ['windows', 'macos', 'linux'],
      rows: { windows: 'built', mac: 'built' },
      site: { state: 'published', detail: `filex.sh/nightly: ${version}` },
      snaps: { amd64: { state: 'released', revision: 50 }, arm64: { state: 'released', revision: 51 } },
    };
    expect(followLines(built)).toEqual([
      `desktop packages ${version}: built on GitHub's runners (windows, macos, linux)`,
      `  filex.sh/nightly: filex.sh/nightly: ${version}`,
      `  snap ${version} on edge (amd64): revision 50`,
      `  snap ${version} on edge (arm64): revision 51`,
    ]);
    expect(followTrouble(built)).toBe(false);
    const skipped = { ...built, snaps: { amd64: { state: 'skipped', why: 'skipped: no Snap Store credential (SNAPCRAFT_STORE_CREDENTIALS is not set)' } } };
    expect(followLines(skipped)).toContain('  snap amd64: skipped: no Snap Store credential (SNAPCRAFT_STORE_CREDENTIALS is not set)');
    expect(followTrouble(skipped)).toBe(false);
    const held = { state: 'held', arch: 'amd64', version, edge: '0.61.1 (revision 48)', days: 2 };
    for (const f of [
      { ...built, rows: { windows: 'built', mac: 'failed' } },
      { ...built, site: { state: 'failed', detail: 'exit 1' } },
      { ...built, snaps: { arm64: { state: 'review', revision: 52 } } },
      { ...built, edge: { amd64: held } },
      { state: 'failed', version, conclusion: 'failure', url: 'u' },
      { state: 'not-started', version, hours: 5 },
    ]) {
      expect(followTrouble(f), JSON.stringify(f)).toBe(true);
    }
    expect(followTrouble({ state: 'none', version, why: 'ci.yml was red on it' })).toBe(false);
    expect(followLines({ ...built, edge: { amd64: held } }).join('\n')).toMatch(/WARNING snap: edge \(amd64\) serves 0\.61\.1/);
    expect(followLines({ ...built, site: { state: 'kept', detail: 'NIGHTLY_SITE_CMD is not set; the page is in /d/site' } })).toContain('  filex.sh/nightly NOT updated: NIGHTLY_SITE_CMD is not set; the page is in /d/site');
  });

  it('says what the nightly image is, and warns when the workflow ran and the image is not this nightly\'s', () => {
    expect(imageLine({ state: 'built', version, revision: C }, null)).toBe(`nightly image ghcr.io/brf-tech/filex:nightly: ${version}`);
    expect(imageLine({ state: 'other', version: '0.62.0-nightly.20261118+bbbbbbbb' }, { state: 'waiting' })).toBeNull();
    expect(imageLine({ state: 'other', version: '0.62.0-nightly.20261118+bbbbbbbb' }, { state: 'built' })).toMatch(/^WARNING nightly image .* is still 0\.62\.0-nightly\.20261118\+bbbbbbbb/);
    expect(imageLine({ state: 'unknown', why: 'x' }, null)).toMatch(/^WARNING/);
    expect(imageLine(null, null)).toBeNull();
  });

  it('knows from the public channel map whether edge serves a nightly snap, per architecture', () => {
    const last = { version, revision: 50, at: '2026-11-17T03:00:00Z' };
    const entry = (v: string, revision: number, architecture = 'amd64') => ({ channel: { name: 'edge', architecture }, version: v, revision });
    expect(snapEdgeState([entry(version, 50)], last, { nowMs: now })).toEqual({ state: 'live', arch: 'amd64', version, revision: 50 });
    expect(snapEdgeState([entry('0.61.1', 48), entry(version, 51, 'arm64')], last, { nowMs: now })).toEqual({
      state: 'held',
      arch: 'amd64',
      version,
      revision: 50,
      edge: '0.61.1 (revision 48)',
      since: last.at,
      days: 2,
    });
    expect(snapEdgeState([entry('0.61.1', 48), entry(version, 51, 'arm64')], { ...last, revision: 51 }, { arch: 'arm64', nowMs: now })?.state).toBe('live');
    expect(snapEdgeState([], last, { nowMs: now })?.edge).toBe('no revision');
    expect(snapEdgeState([entry(version, 50)], null, { nowMs: now })).toBeNull();
  });

  it("takes the Store's version without the build metadata, and the revision before the version (the first night on GitHub)", () => {
    // Measured 2026-11-18: edge listed revisions 63 (amd64) and 62 (arm64) as
    // 0.62.0-nightly.20261118, the manifest said 0.62.0-nightly.20261118+0d849cb7.
    const uploaded = { version: '0.62.0-nightly.20261118+0d849cb7', revision: 63, at: '2026-11-18T06:25:00Z' };
    const entry = (v: string, revision: number, architecture = 'amd64') => ({ channel: { name: 'edge', architecture }, version: v, revision });
    expect(snapEdgeState([entry('0.62.0-nightly.20261118', 63)], uploaded, { nowMs: now })?.state).toBe('live');
    // An older revision of the same day's version is not this upload.
    expect(snapEdgeState([entry('0.62.0-nightly.20261118', 61)], uploaded, { nowMs: now })?.state).toBe('held');
    // No revision known: the version decides, without its metadata.
    expect(snapEdgeState([entry('0.62.0-nightly.20261118', 61)], { ...uploaded, revision: undefined }, { nowMs: now })?.state).toBe('live');
    expect(snapEdgeState([entry('0.61.2', 60)], { ...uploaded, revision: undefined }, { nowMs: now })?.state).toBe('held');
  });

  it("reads snapcraft's answer: released, held for a review, or failed", () => {
    expect(snapUploadVerdict(0, "Revision 31 created for 'filex-app' and released to 'edge'")).toEqual({ state: 'released', revision: 31 });
    expect(snapUploadVerdict(2, 'Revision 32 created for filex-app.\nThe revision is waiting for manual review.')).toEqual({ state: 'review', revision: 32 });
    expect(snapUploadVerdict(1, 'error: upload failed\nStore API error 502')).toEqual({ state: 'failed', why: 'error: upload failed / Store API error 502' });
    expect(snapUploadVerdict(1, '').why).toBe('exit 1');
  });

  it('runs the follow-up before the report is composed, with the CI verdict, and publishes a page once per revision', () => {
    const report = read('report.mjs');
    expect(report.indexOf('followCi(cfg, push.revision)')).toBeGreaterThan(0);
    expect(report.indexOf('followDesktop(cfg, { dry: !!args.dryRun, ci })')).toBeGreaterThan(report.indexOf('followCi(cfg, push.revision)'));
    expect(report.indexOf('followDesktop(cfg, { dry: !!args.dryRun, ci })')).toBeLessThan(report.indexOf('composeReport({'));
    expect(report).toMatch(/if \(!args\.dryRun\) recordCi\(cfg\.state, push\.revision, ci\);/);
    const mjs = read('nightly.mjs');
    expect(mjs).toMatch(/if \(state\.last_site\?\.revision === source\.revision\) \{/);
    expect(mjs).toMatch(/NIGHTLY_SITE_REF: digest \? `\$\{cfg\.desktop\.repo\}@\$\{digest\}` : `\$\{cfg\.desktop\.repo\}:nightly`/);
    expect(mjs).toMatch(/fs\.readFileSync\(path\.join\(cfg\.src, 'site', 'nightly\.src\.html'\), 'utf8'\)/);
  });

  it('makes every package of a night with one script, unsigned, the CLI of each for its own platform', () => {
    const job = read('nightly-desktop-job.sh');
    expect(job).toMatch(/^SRC="\$\{ND_JOB_SRC:-\/w\/src\}"$/m);
    expect(job).toMatch(/^export CSC_IDENTITY_AUTO_DISCOVERY=false$/m);
    for (const [platform, arch] of [['linux', 'x64'], ['linux', 'arm64'], ['win32', 'x64'], ['win32', 'arm64']]) {
      expect(job).toContain(`node scripts/fetch-cli.mjs --platform ${platform} --arch ${arch}`);
    }
    expect(job).toContain('node scripts/fetch-cli.mjs --platform darwin');
    for (const t of ['linux-x64', 'linux-arm64', 'windows', 'mac', 'snap', 'snap-arm64']) expect(job).toMatch(new RegExp(`^ {4}${t}\\)$`, 'm'));
    expect(job).toMatch(/electron-builder --linux dir --x64 --publish never/);
    expect(job).toMatch(/electron-builder --linux dir --arm64 --publish never/);
    expect(job).not.toMatch(/snapcraft upload|gh release|docker push|git push|oras push|rm -rf/);
    const edge = read('nightly-snap-edge.mjs');
    expect(edge).toMatch(/\['upload', '--release=edge', file\]/);
    expect(edge).not.toMatch(/--release=(?!edge)/);
    expect(edge).toMatch(/\.split\(creds\)\.join\('<credential>'\)/);
  });
});

describe('the nightly snap to edge from a runner (#266)', () => {
  const run = (env: Record<string, string>) => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'snap-edge-'));
    try {
      const r = spawnSync(process.execPath, [path.join(CHAIN, 'nightly-snap-edge.mjs'), path.join(dir, 'filex-desktop-arm64.snap'), path.join(dir, 'out.json')], {
        encoding: 'utf8',
        env: { ...process.env, SNAPCRAFT_STORE_CREDENTIALS: '', ...env },
      });
      const out = fs.existsSync(path.join(dir, 'out.json')) ? JSON.parse(fs.readFileSync(path.join(dir, 'out.json'), 'utf8')) : null;
      return { code: r.status, stdout: r.stdout, out };
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  };

  it('says "skipped: no Snap Store credential" without one, and stays green', () => {
    const r = run({});
    expect(r.code).toBe(0);
    expect(r.stdout.split('\n')[0]).toBe('skipped: no Snap Store credential (SNAPCRAFT_STORE_CREDENTIALS is not set)');
    expect(r.stdout).toMatch(/::notice title=Snap Store::skipped: no Snap Store credential/);
    expect(r.out).toEqual({ state: 'skipped', why: 'skipped: no Snap Store credential (SNAPCRAFT_STORE_CREDENTIALS is not set)' });
  });

  it('is red when there is no snap to upload, and says what it would have said for each outcome', () => {
    const r = run({ SNAPCRAFT_STORE_CREDENTIALS: 'a credential' });
    expect(r.code).toBe(1);
    expect(r.out).toMatchObject({ state: 'failed' });
    expect(r.stdout).not.toContain('a credential');
    expect(edgeSummary({ state: 'review', revision: 41 }, 's.snap').annotation).toMatch(/^::warning title=Snap Store: review::/);
    expect(edgeSummary({ state: 'released', revision: 40 }, 's.snap')).toEqual({ line: 's.snap is on edge (revision 40)', annotation: '' });
    expect(edgeSummary({ state: 'failed', why: 'x' }, 's.snap').annotation).toMatch(/^::error /);
  });
});

describe('filex.sh/nightly on the site host (#266)', () => {
  it('sends its half on stdin, never as an inline script, and only to an absolute site directory', () => {
    const sh = read('nightly-site.sh');
    expect(sh).toMatch(/cat "\$REMOTE"\n\} \| ssh -o BatchMode=yes "\$SSH_DEST" bash -s$/m);
    expect(sh).toMatch(/case "\$SITE" in \/\?\*\) ;; \*\) echo "--path must be an absolute directory, not \/" >&2; exit 2 ;; esac/);
    for (const v of ['SITE', 'REF', 'ORAS', 'PAGE_B64', 'JSON_B64']) expect(sh).toMatch(new RegExp(`printf '${v}=%q\\\\n'`));
    const install = read('install-nightly.sh');
    expect(install).toMatch(/for f in nightly\.mjs nightly-lib\.mjs report\.mjs env\.mjs nightly-site\.sh nightly-site-remote\.sh; do/);
  });

  // sync-site.sh is withheld from the public export (it names our host): in
  // the public tree, where GitHub runs this suite, there is nothing to read.
  it.runIf(fs.existsSync(path.join(REPO, 'scripts', 'export-public.sh')))('is never deleted by a deploy of the site', () => {
    const sync = fs.readFileSync(path.join(REPO, 'scripts', 'sync-site.sh'), 'utf8');
    expect(sync).toMatch(/--exclude=\/nightly --exclude=\/\.nightly\//);
    expect(sync).toMatch(/grep -qi 'deleting\.\*\\\(desktop\\\|updates\\\|shots\\\|nightly\\\)'/);
  });

  it.runIf(!!bash && process.platform === 'linux')('pulls, checks, puts the page beside the packages and swaps the link in one rename; a package that does not match changes nothing', () => {
    const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'nightly-site-'));
    try {
      const site = path.join(tmp, 'site');
      const bin = path.join(tmp, 'bin');
      fs.mkdirSync(site);
      fs.mkdirSync(bin);
      fs.writeFileSync(path.join(tmp, 'mode'), 'good');
      // A fake `docker run ... -v <dir>:/w ... pull <ref>`: the artifact's files.
      fs.writeFileSync(
        path.join(bin, 'docker'),
        [
          '#!/usr/bin/env bash',
          `tmp="${fwd(tmp)}"`,
          'dir=""; prev=""',
          'for a in "$@"; do if [ "$prev" = "-v" ]; then dir="${a%%:/w}"; fi; prev="$a"; done',
          'printf x > "$dir/filex-desktop-x64.exe"',
          'printf \'{"version":"v"}\' > "$dir/nightly.json"',
          'if [ "$(cat "$tmp/mode")" = good ]; then (cd "$dir" && sha256sum filex-desktop-x64.exe > SHA256SUMS); else echo "0000000000000000000000000000000000000000000000000000000000000000  filex-desktop-x64.exe" > "$dir/SHA256SUMS"; fi',
          '',
        ].join('\n'),
      );
      fs.chmodSync(path.join(bin, 'docker'), 0o755);
      const b64 = (s: string) => Buffer.from(s).toString('base64');
      const go = () =>
        spawnSync(bash!, [fwd(path.join(CHAIN, 'nightly-site-remote.sh'))], {
          encoding: 'utf8',
          env: {
            ...process.env,
            PATH: `${bin}${path.delimiter}${process.env.PATH}`,
            SITE: fwd(site),
            REF: 'ghcr.io/acme/filex-desktop-nightly@sha256:1',
            ORAS: 'oras:pinned',
            PAGE_B64: b64('<p>page</p>'),
            JSON_B64: b64('{\n  "schema": 1,\n  "version": "0.57.0-nightly.20261018+cccccccc",\n  "date": "2026-10-18"\n}\n'),
          },
        });
      const ok = go();
      expect(ok.status, ok.stderr).toBe(0);
      expect(ok.stdout).toMatch(/filex\.sh\/nightly: 0\.57\.0-nightly\.20261018\+cccccccc \(20261018\), 1 package\(s\)/);
      expect(fs.readlinkSync(path.join(site, 'nightly'))).toBe('.nightly/20261018');
      expect(fs.readFileSync(path.join(site, 'nightly', 'index.html'), 'utf8')).toBe('<p>page</p>');
      expect(JSON.parse(fs.readFileSync(path.join(site, 'nightly', 'nightly.json'), 'utf8')).date).toBe('2026-10-18');
      // A package that does not match its checksum: nothing published, the night before stays.
      fs.writeFileSync(path.join(tmp, 'mode'), 'bad');
      const bad = go();
      expect(bad.status).toBe(1);
      expect(bad.stderr).toMatch(/does not match its SHA256SUMS: nothing published/);
      expect(fs.readlinkSync(path.join(site, 'nightly'))).toBe('.nightly/20261018');
      // A `nightly` that is no link is never replaced.
      fs.unlinkSync(path.join(site, 'nightly'));
      fs.mkdirSync(path.join(site, 'nightly'));
      fs.writeFileSync(path.join(tmp, 'mode'), 'good');
      const dir = go();
      expect(dir.status).toBe(2);
      expect(dir.stderr).toMatch(/is not a link: move it away first/);
    } finally {
      fs.rmSync(tmp, { recursive: true, force: true });
    }
  });
});

describe('the morning report', () => {
  const now = ms('2026-11-19T04:00:00Z');
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
  const D = 'd'.repeat(40);
  const push = { sha: D, base: P, branch: 'nightly', version: `0.62.0-nightly.20261119+${D.slice(0, 8)}`, url: `https://github.com/BRF-Tech/filex/commit/${D}` };
  const tonight = (over: Rec = {}) => ({
    night: '2026-11-19',
    at: '2026-11-18T23:00:00Z',
    phase: 'done',
    decision: 'pushed',
    sha: C,
    subject: 'fix(race): the writehook waits for its flush',
    push,
    ...over,
  });
  const last = { revision: D, sha: C, version: push.version, at: '2026-11-18T23:01:00Z' };
  const pushed = (night: string, sha: string, jobs: Rec, secs = 3600) => ({ night, decision: 'pushed', sha, push: { sha: `${night}`.padEnd(40, '0') }, ci: { state: 'red', secs }, jobs });
  const history = [
    pushed('2026-11-17', A, { 'Go shard 1/8': 'passed', 'Playwright (webkit 2/4)': 'passed', 'All tests (full)': 'passed' }),
    pushed('2026-11-18', B, { 'Go shard 1/8': 'passed', 'Playwright (webkit 2/4)': 'failed', 'All tests (full)': 'failed' }, 3300),
  ];
  const ci = (over: Rec = {}) => ({
    state: 'green',
    run_id: 9,
    url: 'https://github.com/BRF-Tech/filex/actions/runs/9',
    counts: { passed: 3, failed: 0, skipped: 0, total: 3 },
    jobs: { 'Go shard 1/8': 'passed', 'Playwright (webkit 2/4)': 'passed', 'All tests (full)': 'passed' },
    bad: [],
    secs: 3720,
    ...over,
  });

  it("says green with GitHub's numbers, the wall time against the night before, the commits since the last nightly, and what was fixed", () => {
    const r = composeReport({ tonight: tonight(), history, ci: ci(), push: last, commits, nowMs: now, tz });
    expect(r.severity).toBe('success');
    expect(r.title).toBe(`filex nightly 2026-11-19: green (${D.slice(0, 8)}, 1h02m)`);
    const lines = r.message.split('\n');
    expect(lines[0]).toBe(`main ${C.slice(0, 8)} fix(race): the writehook waits for its flush`);
    expect(lines[1]).toBe(`nightly ${D.slice(0, 8)} (${push.version}) on nightly - ${push.url}`);
    expect(r.message).toContain(`since the last nightly (2026-11-18, ${B.slice(0, 8)}): 1 commit(s)`);
    expect(r.message).toContain('ci.yml green: 3 passed, 0 red, 0 skipped of 3 jobs, 1h02m, previous 0h55m - https://github.com/BRF-Tech/filex/actions/runs/9');
    expect(r.message).toContain('fixed since 2026-11-18: Playwright (webkit 2/4), All tests (full)');
  });

  it('says red, each red part with its page, and the commits since it last passed', () => {
    const red = ci({
      state: 'red',
      counts: { passed: 1, failed: 2, skipped: 0, total: 3 },
      jobs: { 'Go shard 1/8': 'failed', 'Playwright (webkit 2/4)': 'failed', 'All tests (full)': 'failed' },
      bad: [
        { name: 'Go shard 1/8', url: 'https://x/job/1', conclusion: 'failure' },
        { name: 'Playwright (webkit 2/4)', url: 'https://x/job/2', conclusion: 'failure' },
      ],
    });
    const r = composeReport({ tonight: tonight(), history, ci: red, push: last, commits, nowMs: now, tz });
    expect(r.severity).toBe('danger');
    expect(r.title).toBe(`filex nightly 2026-11-19: RED 2 of 3 (${D.slice(0, 8)}, 1h02m)`);
    expect(r.message).toContain('RED Go shard 1/8 (new tonight) - https://x/job/1');
    expect(r.message).toContain(`  last green 2026-11-18 on ${B.slice(0, 8)}; the range ${B.slice(0, 8)}..${C.slice(0, 8)}: 1 commit(s)`);
    expect(r.message).toContain('RED Playwright (webkit 2/4) (red on 2026-11-18 too) - https://x/job/2');
    expect(r.message).toContain(`  last green 2026-11-17 on ${A.slice(0, 8)}; the range ${A.slice(0, 8)}..${C.slice(0, 8)}: 2 commit(s)`);
  });

  it('warns while CI still runs, when GitHub has no run of the commit, and when it cannot be asked', () => {
    const running = composeReport({ tonight: tonight(), history, ci: { state: 'running', status: 'in_progress', url: 'u' }, push: last, commits, nowMs: now, tz });
    expect(running.severity).toBe('warning');
    expect(running.title).toMatch(/: CI still running \(/);
    expect(running.message).toContain('ci.yml still running at 07:00 (in_progress) - u');
    const none = composeReport({ tonight: tonight(), history, ci: { state: 'not-found' }, push: last, commits, nowMs: now, tz });
    expect(none.message).toContain("WARNING no ci.yml run on GitHub for dddddddd: is `nightly` in ci.yml's push branches?");
    expect(composeReport({ tonight: tonight(), history, ci: { state: 'unknown', why: 'HTTP 403' }, push: last, commits, nowMs: now, tz }).message).toContain('WARNING GitHub could not be asked about dddddddd: HTTP 403');
  });

  it('turns a green night into a warning when the S3 code changed, the image did not follow, or a package did not go out', () => {
    const s3 = composeReport({ tonight: tonight({ s3: { since: B, files: ['backend/internal/storage/drivers/s3/multipart.go'] } }), history, ci: ci(), push: last, commits, nowMs: now, tz });
    expect(s3.severity).toBe('warning');
    expect(s3.title).toMatch(/: green, with a warning \(/);
    expect(s3.message).toMatch(/WARNING s3-live: the S3 code changed \(1 file\(s\) since bbbbbbbb: backend\/internal\/storage\/drivers\/s3\/multipart\.go\) and nothing runs the live S3 tests on its own - run them by hand on the build host: CHAIN_EXTRAS=s3-live bash scripts\/chain\/run\.sh --profile targeted/);
    const image = composeReport({ tonight: tonight(), history, ci: ci(), push: last, follow: { state: 'built', version: push.version }, image: { state: 'other', version: 'old' }, commits, nowMs: now, tz });
    expect(image.severity).toBe('warning');
    const pkg = composeReport({ tonight: tonight(), history, ci: ci(), push: last, follow: { state: 'failed', version: push.version, conclusion: 'failure', url: 'u' }, commits, nowMs: now, tz });
    expect(pkg.severity).toBe('warning');
    expect(pkg.message).toContain(`WARNING desktop packages ${push.version}: GitHub's build FAILED (failure) - u`);
  });

  it("says so, quietly, when main did not move, when the export is the public main already, and when GitHub was testing a release", () => {
    const quiet = composeReport({ tonight: tonight({ decision: 'skipped', why: `main has not moved since the last nightly (${C.slice(0, 8)})`, push: undefined }), history, ci: ci(), push: last, commits, nowMs: now, tz });
    expect(quiet.severity).toBe('info');
    expect(quiet.title).toBe(`filex nightly 2026-11-19: no push, main unchanged (${C.slice(0, 8)})`);
    expect(quiet.message).toContain(`last nightly: ${D.slice(0, 8)} (${push.version}), ci.yml green - https://github.com/BRF-Tech/filex/actions/runs/9`);
    const busy = composeReport({ tonight: tonight({ decision: 'deferred', why: 'GitHub is testing a release - release.yml runs (v0.62.0); the next night pushes', busy: { url: 'https://x/runs/3' }, push: undefined }), history, push: last, commits, nowMs: now, tz });
    expect(busy.severity).toBe('info');
    expect(busy.title).toMatch(/not pushed, GitHub is testing a release/);
    expect(busy.message).toContain('  https://x/runs/3');
    const same = composeReport({ tonight: tonight({ decision: 'unchanged', why: 'the export is the public main', push: undefined }), history, push: last, commits, nowMs: now, tz });
    expect(same.title).toMatch(/no push, the export is the public main already/);
    // A warning on GitHub's side is still said on a quiet night.
    expect(composeReport({ tonight: tonight({ decision: 'skipped', why: 'w', push: undefined }), history, push: last, follow: { state: 'not-started', version: 'v', hours: 9 }, commits, nowMs: now, tz }).severity).toBe('warning');
  });

  it('is red when the export refused the change, a warning when the push failed, the night is still going, died, or left no record', () => {
    const refused = composeReport({ tonight: tonight({ decision: 'refused', why: 'the export refused cccccccc: a private name', log: '/n/tonight/nightly-export.log', push: undefined }), history, nowMs: now, tz });
    expect(refused.severity).toBe('danger');
    expect(refused.message).toContain('the export REFUSED it: the export refused cccccccc: a private name');
    expect(refused.message).toContain('log /n/tonight/nightly-export.log');
    const failed = composeReport({ tonight: tonight({ decision: 'failed', why: 'coder-ws, the container the push is made from, is not running', push: undefined }), history, nowMs: now, tz });
    expect(failed.severity).toBe('warning');
    expect(failed.title).toMatch(/: not pushed \(/);
    expect(composeReport({ tonight: tonight({ phase: 'push', push: undefined }), history, running: true, nowMs: now, tz }).title).toMatch(/still running/);
    expect(composeReport({ tonight: tonight({ phase: 'export', push: undefined }), history, running: false, nowMs: now, tz }).title).toMatch(/died while export/);
    const none = composeReport({ tonight: null, history, nowMs: now, tz });
    expect(none.severity).toBe('warning');
    expect(none.title).toBe('filex nightly 2026-11-19: no run recorded');
    expect(composeReport({ tonight: tonight({ at: '2026-11-17T23:00:00Z' }), history, nowMs: now, tz }).title).toMatch(/no run recorded/);
  });

  it('stays one notification Notify takes: at most 7900 characters, at most 25 parts listed', () => {
    const bad = Array.from({ length: 60 }, (_, i) => ({ name: `Go shard ${i}/60 ${'x'.repeat(150)}`, url: 'u', conclusion: 'failure' }));
    const r = composeReport({ tonight: tonight(), history, ci: ci({ state: 'red', bad, counts: { passed: 0, failed: 60, skipped: 0, total: 60 }, jobs: {} }), push: last, commits, nowMs: now, tz });
    expect(r.message.length).toBeLessThanOrEqual(7900);
    expect(r.message.split('\n').filter((l: string) => l.startsWith('RED ')).length).toBeLessThanOrEqual(25);
  });

  it('diffs two nights job by job, and finds the night a job last passed on GitHub', () => {
    expect(jobsDiff({ a: 'passed', b: 'failed', c: 'failed' }, { a: 'failed', b: 'failed', c: 'passed', d: 'cancelled' })).toEqual({ newRed: ['a', 'd'], stillRed: ['b'], fixed: ['c'] });
    expect(lastGreenOf(history, 'Playwright (webkit 2/4)')?.night).toBe('2026-11-17');
    expect(lastGreenOf(history, 'nothing')).toBeNull();
    expect(lastPushed(history)?.night).toBe('2026-11-18');
    expect(lastPushed([{ decision: 'skipped' }])).toBeNull();
  });
});

describe('what a night leaves', () => {
  it("keeps a night's record small: GitHub's verdict once it is final, not the working fields", () => {
    const rec = nightRecord({ night: 'n', at: 'a', phase: 'done', pid: 1, decision: 'pushed', sha: C, busy: { url: 'u' }, push: { sha: A, base: B, branch: 'nightly', version: 'v', url: 'u', at: 't' }, ci: { state: 'green', run_id: 3, url: 'u', counts: { passed: 1 }, secs: 9, jobs: { x: 'passed' }, bad: [] } });
    expect(rec).not.toHaveProperty('phase');
    expect(rec).not.toHaveProperty('pid');
    expect(rec).not.toHaveProperty('busy');
    expect(rec.push).toEqual({ sha: A, base: B, branch: 'nightly', version: 'v', url: 'u' });
    expect(rec.ci).toEqual({ state: 'green', run_id: 3, url: 'u', counts: { passed: 1 }, secs: 9 });
    expect(rec.jobs).toEqual({ x: 'passed' });
    expect(nightRecord({ night: 'n', decision: 'pushed', ci: { state: 'running', jobs: {} } }).ci).toBeUndefined();
    expect(nightRecord({ night: 'n', decision: 'pushed', s3: { since: A, files: [] } }).s3).toBeUndefined();
  });

  it('names the night by its local date', () => {
    expect(localDate(ms('2026-11-18T23:00:00Z'), 'Europe/Istanbul')).toBe('2026-11-19');
    expect(localDate(ms('2026-11-18T23:00:00Z'), 'UTC')).toBe('2026-11-18');
  });
});

describe('the settings', () => {
  const base = { CHAIN_ROOT: '/var/lib/filex-nightly' };

  it("need the nightly's own directory, and nothing of the chain any more", () => {
    expect(() => nightlySettings({})).toThrow(/CHAIN_ROOT/);
    const cfg = nightlySettings(base);
    expect(cfg.src).toBe(path.resolve('/var/lib/filex-nightly/src'));
    expect(cfg.push).toMatchObject({ remote: 'https://github.com/BRF-Tech/filex.git', branch: 'nightly', author: '', container: '', wrap: 'with-gh-token', busy: 'skip' });
    expect(cfg.push.publicDir).toBe(path.resolve('/var/lib/filex-nightly/public'));
    expect(cfg.github).toEqual({ repo: 'BRF-Tech/filex', ci: 'ci.yml', tokenFile: '' });
    expect(cfg.desktop).toMatchObject({ mode: 'github', repo: 'ghcr.io/brf-tech/filex-desktop-nightly', workflow: 'nightly-desktop.yml', oras: 'ghcr.io/oras-project/oras:v1.2.2', siteCmd: '' });
    expect(cfg.image.repo).toBe('ghcr.io/brf-tech/filex');
    // A wrapper can be switched off, an author must be one.
    expect(nightlySettings({ ...base, NIGHTLY_PUSH_WRAP: '' }).push.wrap).toBe('');
    expect(() => nightlySettings({ ...base, NIGHTLY_COMMIT_AUTHOR: 'somebody' })).toThrow(/NIGHTLY_COMMIT_AUTHOR/);
    expect(() => nightlySettings({ ...base, NIGHTLY_RELEASE_BUSY: 'wait' })).toThrow(/NIGHTLY_RELEASE_BUSY/);
    expect(() => nightlySettings({ ...base, NIGHTLY_DESKTOP: 'weekly' })).toThrow(/NIGHTLY_DESKTOP/);
    expect(() => nightlySettings({ ...base, NIGHTLY_KEEP_HISTORY: 'many' })).toThrow(/NIGHTLY_KEEP_HISTORY/);
  });

  it('read the example as they read any settings file, and the example names no value of a credential', () => {
    const text = read('nightly.env.example');
    const cfg = nightlySettings(parseEnvFile(text));
    expect(cfg.tz).toBe('Europe/Istanbul');
    expect(cfg.push.author).toBe('');
    // What no longer runs at night says where it stands.
    expect(text).toContain('bash scripts/chain/run.sh --profile nightly --env <file>');
    expect(text).toMatch(/CHAIN_EXTRAS=s3-live bash scripts\/chain\/run\.sh --profile targeted/);
    for (const k of ['CHAIN_LOCK', 'NIGHTLY_PUBLISH', 'NIGHTLY_SNAP', 'NIGHTLY_QUIET', 'FILEX_TEST_S3_SECRET_KEY']) expect(text, k).not.toMatch(new RegExp(`^#? ?${k}=`, 'm'));
  });

  it('parse the command lines of the night and the report', () => {
    expect(parseNightlyArgs(['run', '--env', '/e', '--force'])).toEqual({ cmd: 'run', env: '/e', force: true });
    expect(parseNightlyArgs(['--dry-run'])).toEqual({ cmd: 'run', dryRun: true });
    expect(parseNightlyArgs(['follow', '--dry-run'])).toEqual({ cmd: 'follow', dryRun: true });
    for (const gone of ['build', 'publish', 's3-live', 'deploy']) expect(() => parseNightlyArgs([gone])).toThrow(/unknown command/);
    expect(() => parseNightlyArgs(['run', '--env'])).toThrow(/--env/);
    expect(parseReportArgs(['--env', '/e', '--dry-run', '--wait-min', '0'])).toEqual({ env: '/e', dryRun: true, waitMin: 0 });
    expect(() => parseReportArgs(['--wait-min', 'soon'])).toThrow(/minutes/);
    expect(notifyBody({ severity: 'info', title: 't', message: 'm' }, { group: 'infra', source: 'filex-nightly' })).toEqual({ group: 'infra', source: 'filex-nightly', severity: 'info', title: 't', message: 'm' });
  });
});

describe('the timers and the installer', () => {
  const unit = (f: string) => read(path.join('systemd', f));

  it('start the night and the report in the morning, from the installed copies', () => {
    expect(unit('filex-nightly.timer')).toMatch(/^OnCalendar=\*-\*-\* @AT@$/m);
    expect(unit('filex-nightly.timer')).toMatch(/^Persistent=false$/m);
    expect(unit('filex-nightly-report.timer')).toMatch(/^OnCalendar=\*-\*-\* @REPORT_AT@$/m);
    expect(unit('filex-nightly.service')).toMatch(/^ExecStart=@NODE@ @BIN@\/nightly\.mjs run --env @ENV@$/m);
    expect(unit('filex-nightly.service')).toMatch(/^SuccessExitStatus=1$/m);
    expect(unit('filex-nightly.service')).toMatch(/^TimeoutStartSec=1h$/m);
    expect(unit('filex-nightly-report.service')).toMatch(/^ExecStart=@NODE@ @BIN@\/report\.mjs --env @ENV@$/m);
    expect(read('install-nightly.sh')).toMatch(/^AT="02:00 Europe\/Istanbul"$/m);
  });

  it('installs every file the installed copies import, and fills every placeholder the units carry', () => {
    const sh = read('install-nightly.sh');
    const copied = /for f in ([a-z. -]+); do/.exec(sh)?.[1].split(' ') ?? [];
    for (const f of ['nightly.mjs', 'report.mjs', 'nightly-lib.mjs']) {
      for (const m of read(f).matchAll(/^import [^;]* from '\.\/([^']+)';$/gm)) expect(copied, `${f} imports ${m[1]}`).toContain(m[1]);
    }
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

  it('left no script of the night that built on the host behind', () => {
    for (const f of ['nightly-build.sh', 'nightly-desktop.sh']) expect(fs.existsSync(path.join(CHAIN, f)), f).toBe(false);
  });
});
