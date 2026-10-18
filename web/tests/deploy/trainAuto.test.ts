// The autonomous release round (scripts/train/auto.mjs, auto-host.mjs,
// install-auto.sh, task #165): a release a person queued, cut by a timer in
// the release workspace with nobody at the keyboard.
//
// ⚠ Why these tests: the round does the steps `pnpm release` leaves to a
// person - the land, the signed tags, the pushes, the ship - so what it
// confirms and what it undoes is the whole of its safety. They hold:
//   - the audit is confirmed on the commit the request names and no other,
//     and the export's withheld files never by the round;
//   - each person step is done once a round; a stage that still waits after
//     it stops the round for a person, never loops;
//   - a red stage is rolled back only while nothing is on a remote, and only
//     the commits the release tool made;
//   - the tags carry what the sign stage asks for (one helper for both);
//   - the build host's half says when the release workspace is down, and the
//     installer copies everything its script imports.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { landMessage } from '../../../scripts/release/checks.mjs';
import { SURFACES_LINE, tagMessages } from '../../../scripts/release/surfaces.mjs';
import { downVerdict, hostCommand } from '../../../scripts/train/auto-host.mjs';
import {
  BEFORE_LAND,
  LOCK_BUSY,
  decide,
  hasHighlight,
  landMerge,
  mergeOf,
  morningReport,
  nextVersion,
  owed,
  parseArgs,
  requestProblems,
  rollbackPlan,
  signScript,
  stageAt,
} from '../../../scripts/train/auto.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const read = (rel: string) => fs.readFileSync(path.join(REPO, rel), 'utf8');
const AUDITED = 'a'.repeat(40);
const STAMP = 's'.repeat(40);
const REQ = { version: '0.57.0', commit: AUDITED, summary: 'the server lane (#252)', at: '2026-10-16T06:00:00Z' };
const ORDER = ['preflight', 'audit', 'docs', 'stamp', 'pretag', 'export', 'land', 'gate', 'sign', 'push', 'ci', 'deploy'];

/** A release record: every stage before `at` done, `at` itself `status`. */
function record(at: string | null, status = 'waiting', extra: Record<string, unknown> = {}) {
  const stages: Record<string, unknown> = {};
  for (const id of ORDER) {
    if (id === at) {
      stages[id] = { status, ...extra };
      break;
    }
    stages[id] = { status: 'done' };
  }
  return { tag: 'v0.57.0', stages };
}

describe('the request', () => {
  it('takes a version, the audited commit and one line; refuses what would release the wrong thing', () => {
    expect(requestProblems(REQ)).toEqual([]);
    expect(requestProblems({ ...REQ, profile: 'server', task: '252' })).toEqual([]);
    expect(requestProblems({ ...REQ, version: 'v0.57.0' }).join()).toMatch(/not a release number/);
    expect(requestProblems({ ...REQ, commit: 'abc123' }).join()).toMatch(/not a full commit id/);
    expect(requestProblems({ ...REQ, summary: '' }).join()).toMatch(/no summary/);
    expect(requestProblems({ ...REQ, summary: 'a\nb' }).join()).toMatch(/one line/);
    // On GitHub "Fixes #N" in the export commit closes the issue (lesson #163).
    expect(requestProblems({ ...REQ, summary: 'Fixes #12 in the server' }).join()).toMatch(/CLOSES the issue/);
    expect(requestProblems({ ...REQ, profile: 'everything' }).join()).toMatch(/profile "everything"/);
    expect(requestProblems({ ...REQ, task: 'soon' }).join()).toMatch(/not a task number/);
  });

  it('counts the next version from release tags alone', () => {
    const tags = ['v0.56.3', 'v0.56.10', 'v0.9.0', 'backend/v0.56.11', 'v0.57.0-rc.1'];
    expect(nextVersion(tags, 'patch')).toBe('0.56.11');
    expect(nextVersion(tags, 'minor')).toBe('0.57.0');
    expect(() => nextVersion(tags, 'major')).toThrow(/patch or minor/);
  });

  it("wants the release's Releases-page summary on the audited commit", () => {
    expect(hasHighlight(JSON.stringify({ 'v0.57.0': 'A paragraph.' }), 'v0.57.0')).toBe(true);
    expect(hasHighlight(JSON.stringify({ 'v0.57.0': '  ' }), 'v0.57.0')).toBe(false);
    expect(hasHighlight(JSON.stringify({ 'v0.56.3': 'Old.' }), 'v0.57.0')).toBe(false);
    expect(hasHighlight('{ not json', 'v0.57.0')).toBe(false);
  });

  it('reads its commands and refuses an unknown one', () => {
    expect(parseArgs(['request', '0.57.0', '--summary', 'x', '--profile', 'server', '--task', '252'])).toMatchObject({ cmd: 'request', version: '0.57.0', summary: 'x', profile: 'server', task: '252' });
    expect(parseArgs(['run', '--dry-run'])).toMatchObject({ cmd: 'run', dry: true });
    expect(() => parseArgs(['release'])).toThrow(/no such command/);
    expect(() => parseArgs(['run', '--force'])).toThrow(/unknown option --force/);
  });
});

describe('what the round does after each run of the release tool', () => {
  it('finds the stage a record stands at', () => {
    expect(stageAt(record('pretag'))).toEqual({ id: 'pretag', status: 'waiting' });
    expect(stageAt(record(null))).toBeNull();
    expect(stageAt({ stages: {} })).toEqual({ id: 'preflight', status: 'not run' });
  });

  it('confirms the audit on the audited commit only, and once', () => {
    expect(decide({ code: 3, state: record('audit'), request: REQ, head: AUDITED })).toEqual({ do: 'ack', acks: ['audit'] });
    const moved = decide({ code: 3, state: record('audit'), request: REQ, head: 'b'.repeat(40) });
    expect(moved).toMatchObject({ do: 'person', stage: 'audit' });
    expect(moved.why).toMatch(/audit this commit, then queue it again/);
    expect(decide({ code: 3, state: record('audit'), request: REQ, head: AUDITED, done: new Set(['ack:audit']) })).toMatchObject({ do: 'person' });
  });

  it("defers the pretag's other-machine checks, and never confirms the export's withheld files", () => {
    expect(decide({ code: 3, state: record('pretag'), request: REQ, head: AUDITED, deferredAcks: ['store-deferred'] })).toEqual({ do: 'ack', acks: ['store-deferred'] });
    expect(decide({ code: 3, state: record('pretag'), request: REQ, deferredAcks: ['store-deferred'], done: new Set(['ack:store-deferred']) })).toMatchObject({ do: 'person', stage: 'pretag' });
    expect(decide({ code: 3, state: record('pretag'), request: REQ })).toMatchObject({ do: 'person' });
    const exp = decide({ code: 3, state: record('export'), request: REQ });
    expect(exp).toMatchObject({ do: 'person', stage: 'export' });
    expect(exp.why).toMatch(/--ack export-withheld/);
  });

  it("does each of a person's steps once a round; a stage that still waits after it is a person's", () => {
    for (const [stage, step] of [['land', 'land'], ['sign', 'sign'], ['push', 'push']]) {
      expect(decide({ code: 3, state: record(stage), request: REQ }), stage).toEqual({ do: step });
      expect(decide({ code: 3, state: record(stage), request: REQ, done: new Set([step]) }), stage).toMatchObject({ do: 'person', stage });
    }
    expect(decide({ code: 3, state: record('deploy'), request: REQ })).toEqual({ do: 'ship' });
    expect(decide({ code: 3, state: record('deploy'), request: REQ, done: new Set(['ship']) })).toEqual({ do: 'ack', acks: ['deploy'] });
    expect(decide({ code: 3, state: record('deploy'), request: REQ, done: new Set(['ship', 'ack:deploy']) })).toMatchObject({ do: 'person' });
  });

  it('asks GitHub again a limited number of times', () => {
    for (const stage of ['gate', 'ci']) {
      expect(decide({ code: 3, state: record(stage), request: REQ, waited: 7, waits: 8 })).toEqual({ do: 'wait', stage });
      expect(decide({ code: 3, state: record(stage), request: REQ, waited: 8, waits: 8 })).toMatchObject({ do: 'person', stage });
    }
  });

  it('is red on a red gate, done only when every stage is, and ships a release a store holds', () => {
    expect(decide({ code: 1, state: record('pretag', 'red'), request: REQ })).toMatchObject({ do: 'red', stage: 'pretag' });
    expect(decide({ code: 0, state: record(null), request: REQ })).toEqual({ do: 'done' });
    expect(decide({ code: 0, state: record('deploy'), request: REQ })).toMatchObject({ do: 'red', stage: 'deploy' });
    expect(decide({ code: 2, state: record('docs'), request: REQ })).toMatchObject({ do: 'red' });
    const held = record('ci', 'red', { held: ['the Snap Store'] });
    expect(decide({ code: 1, state: held, request: REQ })).toEqual({ do: 'ship', held: ['the Snap Store'] });
    expect(decide({ code: 1, state: held, request: REQ, done: new Set(['ship']) })).toEqual({ do: 'held', held: ['the Snap Store'] });
    expect(decide({ code: 1, state: record('ci', 'red'), request: REQ })).toMatchObject({ do: 'red', stage: 'ci' });
  });

  // v0.57.0 (2026-10-17): the deploy checks read what the ship deploys, so
  // before the ship the release tool stops RED at deploy (exit 1), not
  // waiting (exit 3); the round stopped there and the ship was started by hand.
  it('ships when the deploy checks are red before the ship, and stops when they are red after it', () => {
    const red = record('deploy', 'red');
    expect(decide({ code: 1, state: red, request: REQ })).toEqual({ do: 'ship' });
    const after = decide({ code: 1, state: red, request: REQ, done: new Set(['ship']) });
    expect(after).toMatchObject({ do: 'red', stage: 'deploy' });
    expect(after.why).toMatch(/red after the ship/);
    // A red stage before the deploy is never a ship.
    for (const stage of ['land', 'gate', 'sign', 'push']) {
      expect(decide({ code: 1, state: record(stage, 'red'), request: REQ }), stage).toMatchObject({ do: 'red', stage });
    }
    // The stage itself: red, not waiting, while its checks are red.
    const src = read('scripts/release/stages.mjs');
    const deployFn = src.slice(src.indexOf('export async function deploy(R)'), src.indexOf('export const RUNNERS'));
    expect(deployFn).toMatch(/if \(!ok\) \{[\s\S]*?return red\(\);/);
  });

  it('rolls back only the stages before the land', () => {
    expect(BEFORE_LAND).toEqual(ORDER.slice(0, ORDER.indexOf('land')));
  });
});

describe('the signed tags', () => {
  it('carry the messages the sign stage asks for, a server patch its surfaces line, from one helper', () => {
    expect(tagMessages('v1.2.3')).toEqual(['v1.2.3']);
    expect(tagMessages('v1.2.4', { server: true })).toEqual(['v1.2.4', SURFACES_LINE]);
    const s = signScript({ repo: '/w/filex', exp: '/w/filex-export', tag: 'v1.2.4', releaseCommit: 'r'.repeat(40), exportCommit: 'e'.repeat(40), server: true });
    expect(s).toContain(`git -C '/w/filex' tag -s 'v1.2.4' -m 'v1.2.4' -m '${SURFACES_LINE}' ${'r'.repeat(40)}`);
    expect(s).toContain(`git -C '/w/filex-export' tag -s 'v1.2.4' -m 'v1.2.4' -m '${SURFACES_LINE}' ${'e'.repeat(40)}`);
    expect(s.split('\n')[0]).toBe('set -euo pipefail');
    expect(s).toMatch(/tag -v 'v1\.2\.4'/);
    // The sign stage prints its commands from the same helper.
    expect(read('scripts/release/stages.mjs')).toMatch(/tagMessages\(tag, \{ server \}\)/);
  });

  it('never makes a tag that is already there (the sign stage judges it; a tag is never moved)', () => {
    const s = signScript({ repo: '/w/filex', exp: '/w/filex-export', tag: 'v1.2.3', releaseCommit: 'r', exportCommit: 'e', have: { repo: true, exp: false } });
    expect(s).not.toMatch(/filex' tag -s/);
    expect(s).toMatch(/filex' tag -v 'v1\.2\.3'/);
    expect(s).toMatch(/filex-export' tag -s 'v1\.2\.3' -m 'v1\.2\.3' e/);
  });
});

describe('rolling back a red round', () => {
  const state = { tag: 'v0.57.0', stampCommit: STAMP, releaseCommit: STAMP, exportTree: 'tree-of-the-export', stages: { export: { status: 'done' } } };
  const base = {
    state,
    request: REQ,
    repo: '/w/filex',
    exp: '/w/filex-export',
    head: STAMP,
    headParent: AUDITED,
    remote: { main: AUDITED, tag: false, expMain: 'm'.repeat(40), expTag: false },
    expHead: 'm'.repeat(40),
    expHeadParent: 'p'.repeat(40),
    expDirty: true,
    expHeadTree: 'x',
  };

  it("drops the stamp commit and puts the export's changes aside, keeping both", () => {
    const p = rollbackPlan(base);
    expect(p.ok).toBe(true);
    expect(p.steps.map((s) => [s.dir, s.args])).toEqual([
      ['/w/filex', ['reset', '--keep', AUDITED]],
      ['/w/filex-export', ['stash', 'push', '--include-untracked', '-m', 'filex auto: the export of v0.57.0, rolled back']],
    ]);
    // An export commit made here and never pushed goes too - only that one.
    const made = rollbackPlan({ ...base, expDirty: false, expHead: 'c'.repeat(40), expHeadParent: 'm'.repeat(40), expHeadTree: 'tree-of-the-export' });
    expect(made.steps.at(-1)).toMatchObject({ dir: '/w/filex-export', args: ['reset', '--keep', 'm'.repeat(40)] });
    // Nothing made yet: nothing to do.
    expect(rollbackPlan({ ...base, head: AUDITED, expDirty: false }).steps).toEqual([]);
  });

  it('undoes nothing a remote has, nothing after the land, and no commit the release tool did not make', () => {
    const refuse = (over: Record<string, unknown>, why: RegExp) => {
      const p = rollbackPlan({ ...base, ...over });
      expect(p.ok).toBe(false);
      expect(p.steps).toEqual([]);
      expect(p.why).toMatch(why);
    };
    refuse({ remote: { ...base.remote, main: STAMP } }, /release commit is on the private remote/);
    refuse({ remote: { ...base.remote, tag: true } }, /on a remote/);
    refuse({ remote: { ...base.remote, expTag: true } }, /on a remote/);
    refuse({ remote: { error: 'could not resolve host' } }, /unknown is not "not pushed"/);
    refuse({ state: { ...state, stages: { land: { status: 'done' } } } }, /mains were pushed/);
    refuse({ head: 'f'.repeat(40) }, /left for a person/);
    refuse({ headParent: 'f'.repeat(40) }, /left for a person/);
    refuse({ expHead: 'c'.repeat(40), expHeadParent: 'm'.repeat(40), expHeadTree: 'another tree', expDirty: false }, /public checkout .* left for a person/);
  });
});

describe('the round on a checkout', () => {
  const src = read('scripts/train/auto.mjs');

  it('holds the build lock around the stages that build, and says a busy lock is no failure', () => {
    expect(src).toMatch(/const building = cfg\.chainLock && !isDone\(state, 'export'\);/);
    expect(src).toMatch(/\['-E', String\(LOCK_BUSY\), '-w', String\(Math\.round\(cfg\.lockWaitMin \* 60\)\), cfg\.chainLock, process\.execPath, \.\.\.args\]/);
    expect(LOCK_BUSY).toBe(75);
    expect(src).toMatch(/busy: 'queued'/);
  });

  it('pushes each tag by its ref, never --tags, never --force; lands with the request line', () => {
    expect(src).toMatch(/'push', remote, `refs\/tags\/\$\{tag\}`/);
    expect(src).not.toMatch(/'--tags'|'--force'|'-f'/);
    expect(src).toMatch(/'commit', '--quiet', '-m', `\$\{tag\} - \$\{req\.summary\}`/);
  });

  it('keeps every command\'s output in a file, never a pipe (lessons #722/#726)', () => {
    expect(src).toMatch(/stdio: \['ignore', fd, fd\]/);
  });

  // A real checkout: the request is written beside the release tool's
  // records, refused without the release's summary, and `run` with nothing
  // queued does nothing.
  it('queues a release of the audited commit, and refuses one without its summary or not after the newest tag', () => {
    const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-auto-'));
    const gitIn = (...args: string[]) => spawnSync('git', ['-C', tmp, ...args], { encoding: 'utf8' });
    const auto = (...args: string[]) =>
      spawnSync(process.execPath, [path.join(REPO, 'scripts', 'train', 'auto.mjs'), ...args], {
        encoding: 'utf8',
        env: { ...process.env, FILEX_AUTO_REPO: tmp, FILEX_TRAIN_ENV: path.join(tmp, 'train.env'), GH_TOKEN: '' },
      });
    try {
      fs.writeFileSync(path.join(tmp, 'train.env'), '');
      gitIn('init', '-q', '-b', 'main');
      gitIn('config', 'user.email', 'alice@example.com');
      gitIn('config', 'user.name', 'alice');
      // A maintainer's global config may sign every commit and tag.
      gitIn('config', 'commit.gpgSign', 'false');
      gitIn('config', 'tag.gpgSign', 'false');
      fs.mkdirSync(path.join(tmp, 'docs-site', 'data'), { recursive: true });
      const hl = path.join(tmp, 'docs-site', 'data', 'release-highlights.json');
      fs.writeFileSync(hl, JSON.stringify({ 'v0.56.3': 'Old.' }));
      gitIn('add', '-A');
      gitIn('commit', '-q', '-m', 'first');
      gitIn('tag', 'v0.56.3');
      const none = auto('request', '0.57.0', '--summary', 'the server lane (#252)', '--commit', 'HEAD');
      expect(none.status).toBe(2);
      expect(none.stderr).toMatch(/has no "v0\.57\.0" summary/);
      fs.writeFileSync(hl, JSON.stringify({ 'v0.56.3': 'Old.', 'v0.57.0': 'The server lane.' }));
      gitIn('commit', '-q', '-am', 'the summary');
      const head = gitIn('rev-parse', 'HEAD').stdout.trim();
      const old = auto('request', '0.56.3', '--summary', 'again', '--commit', 'HEAD');
      expect(old.status).toBe(2);
      expect(old.stderr).toMatch(/is not after v0\.56\.3/);
      const ok = auto('request', '0.57.0', '--summary', 'the server lane (#252)', '--commit', 'HEAD', '--profile', 'server');
      expect(ok.status, ok.stderr).toBe(0);
      const req = JSON.parse(fs.readFileSync(path.join(tmp, '.git', 'filex-release', 'auto', 'request.json'), 'utf8'));
      expect(req).toMatchObject({ version: '0.57.0', commit: head, profile: 'server', status: 'queued' });
      // Without the GitHub token (or the signing command) a round does not start.
      const dry = auto('run', '--dry-run');
      expect(dry.stdout).toMatch(/would release v0\.57\.0 \(server\) from [0-9a-f]{10}: a fresh start/);
      expect(dry.stdout).toMatch(/missing here: .*GH_TOKEN/);
      expect(auto('cancel').status).toBe(0);
      const idle = auto('run');
      expect(idle.status).toBe(0);
      expect(idle.stdout).toMatch(/nothing queued: nothing to do/);
    } finally {
      fs.rmSync(tmp, { recursive: true, force: true });
    }
  });
});

// The outside pull requests the export commit merges (--merge-pr): GitHub
// marks a pull request merged once its head is on main, and the export
// commit used to have one parent (#120 stayed open after 0.57.0).
describe('the pull requests the land merges', () => {
  const HEAD121 = 'c'.repeat(40);
  const merge = { base: 'B', tree: 'T', prs: [{ number: 121, head: HEAD121, label: 'berkbasarir:fix/csv-edited-decimal-comma' }] };
  const state = { mergePrs: [120, 121], exportTree: 'T', exportBase: 'B', stages: { land: { status: 'waiting', merge } } };

  it('are named in the request, and read back from the land stage that checked them, never guessed', () => {
    expect(parseArgs(['request', '0.57.1', '--summary', 'x', '--merge-pr', '120,121'])).toMatchObject({ mergePrs: [120, 121] });
    expect(() => parseArgs(['request', '0.57.1', '--merge-pr', '12x'])).toThrow(/not a pull request number/);
    expect(requestProblems({ ...REQ, mergePrs: [120, 121] })).toEqual([]);
    expect(requestProblems({ ...REQ, mergePrs: [] }).join()).toMatch(/pull request numbers/);
    expect(requestProblems({ ...REQ, mergePrs: [7, 7] }).join()).toMatch(/named twice/);
    // #120's head on the public main already leaves #121 alone to merge.
    expect(mergeOf(state)).toEqual(merge);
    expect(mergeOf({ stages: {} })).toEqual({ prs: [] });
    expect(mergeOf({ ...state, stages: { land: { status: 'waiting' } } }).error).toMatch(/recorded no check/);
    expect(mergeOf({ ...state, exportTree: 'T2' }).error).toMatch(/another export/);
    expect(mergeOf({ ...state, stages: { land: { merge: { ...merge, prs: [{ number: 9, head: HEAD121 }] } } } }).error).toMatch(/names/);
    // The round passes the list on to the release tool's fresh start.
    const src = read('scripts/train/auto.mjs');
    expect(src).toMatch(/if \(req\.mergePrs\?\.length\) args\.push\('--merge-pr', req\.mergePrs\.join\(','\)\);/);
  });

  it("stops for a person when the export carries one in another form; any other red at the land is red", () => {
    const d = decide({ code: 1, state: record('land', 'red', { ask: 'merge-pr' }), request: REQ });
    expect(d).toMatchObject({ do: 'person', stage: 'land' });
    expect(d.why).toMatch(/--ack merge-pr/);
    expect(decide({ code: 1, state: record('land', 'red'), request: REQ })).toMatchObject({ do: 'red', stage: 'land' });
  });

  // A real repository: the public remote holds a pull request's head under
  // refs/pull/N/head, as GitHub does; the export is staged in the public
  // checkout; the land makes the merge and main is pushed.
  it("makes the export commit a merge whose tree is the export's, byte for byte, and puts the head on main (what GitHub reads)", () => {
    const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-land-merge-'));
    const env = { ...process.env, GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: path.join(tmp, 'gitconfig') };
    fs.writeFileSync(env.GIT_CONFIG_GLOBAL, '[user]\n\tname = Release Test\n\temail = release@test.invalid\n[init]\n\tdefaultBranch = main\n[commit]\n\tgpgSign = false\n[core]\n\tautocrlf = false\n');
    const g = (dir: string, ...args: string[]) => {
      const r = spawnSync('git', ['-C', dir, ...args], { encoding: 'utf8', env });
      if (r.status !== 0) throw new Error(`git ${args.join(' ')}: ${r.stderr}`);
      return r.stdout.trim();
    };
    const put = (dir: string, file: string, text: string) => {
      fs.mkdirSync(path.dirname(path.join(dir, file)), { recursive: true });
      fs.writeFileSync(path.join(dir, file), text);
    };
    try {
      const remote = path.join(tmp, 'public.git').split(path.sep).join('/');
      g(tmp, 'init', '-q', '--bare', remote);
      const pub = path.join(tmp, 'pub');
      fs.mkdirSync(pub);
      g(pub, 'init', '-q');
      // landMerge runs git in its own process, without this test's env: the
      // identity goes in the checkout itself (GitHub's runners have none).
      g(pub, 'config', 'user.name', 'Release Test');
      g(pub, 'config', 'user.email', 'release@test.invalid');
      g(pub, 'remote', 'add', 'origin', remote);
      put(pub, 'csv_keep.go', 'package officesave\n\nfunc Keep() {}\n');
      g(pub, 'add', '-A');
      g(pub, 'commit', '-q', '-m', 'v0.57.0 - the last release');
      g(pub, 'push', '-q', 'origin', 'main');
      // The contributor's pull request, on the public main.
      const fork = path.join(tmp, 'fork');
      g(tmp, 'clone', '-q', remote, fork);
      put(fork, 'csv_keep.go', 'package officesave\n\nfunc Keep() {}\n\nfunc KeepComma() {}\n');
      g(fork, 'commit', '-q', '-am', 'fix(onlyoffice): a decimal typed into a CSV keeps the comma');
      g(fork, 'push', '-q', 'origin', 'HEAD:refs/pull/121/head');
      const prHead = g(fork, 'rev-parse', 'HEAD');
      // The export: the pull request's change and the rest of the release, staged.
      g(pub, 'fetch', '-q', 'origin', '+refs/pull/121/head:refs/remotes/origin/pull/121');
      put(pub, 'csv_keep.go', 'package officesave\n\nfunc Keep() {}\n\nfunc KeepComma() {}\n\nfunc KeepQuote() {}\n');
      put(pub, 'CHANGELOG.md', '## [0.57.1]\n');
      g(pub, 'add', '-A');
      const base = g(pub, 'rev-parse', 'HEAD');
      const tree = g(pub, 'write-tree');
      const pulls = [{ number: 121, head: prHead, label: 'berkbasarir:fix/csv-edited-decimal-comma' }];
      const message = landMessage('v0.57.1', 'a decimal typed into a CSV keeps its comma (#121)', pulls);

      // Refused: a checkout off the public main, or an index that is not the export.
      expect(landMerge({ exp: pub, base: prHead, tree, heads: [prHead], message }).error).toMatch(/not the public main the export ran on/);
      expect(landMerge({ exp: pub, base, tree: base, heads: [prHead], message }).error).toMatch(/index is not the export/);

      const made = landMerge({ exp: pub, base, tree, heads: [prHead], message });
      expect(made.error).toBeUndefined();
      expect(g(pub, 'rev-parse', 'HEAD')).toBe(made.commit);
      expect(g(pub, 'show', '-s', '--format=%P', 'HEAD').split(' ')).toEqual([base, prHead]);
      expect(g(pub, 'rev-parse', 'HEAD^{tree}')).toBe(tree);
      expect(g(pub, 'status', '--porcelain')).toBe('');
      expect(g(pub, 'log', '-1', '--format=%B')).toBe(`${message[0]}\n\n${message[1]}`);
      // The first parent's diff is exactly the export (what the public
      // pre-push gate scans), and the pull request's commit is history.
      expect(g(pub, 'diff', '--name-only', 'HEAD^1', 'HEAD').split('\n')).toEqual(['CHANGELOG.md', 'csv_keep.go']);
      g(pub, 'push', '-q', 'origin', 'main');
      const reachable = spawnSync('git', [`--git-dir=${remote}`, 'merge-base', '--is-ancestor', prHead, 'main'], { env });
      expect(reachable.status, 'the head is on the public main: GitHub marks the pull request merged').toBe(0);
    } finally {
      fs.rmSync(tmp, { recursive: true, force: true });
    }
  });
});

describe('the morning report', () => {
  it("names each round, the request, and what a release left to a person", () => {
    const states = {
      'v0.57.1': {
        profile: 'server',
        matrix: { state: 'red' },
        deferred: { 'store-deferred': { gate: 'the Store copy', checkedAck: 'store-checked' } },
        stages: { ci: { status: 'red', held: ['the Snap Store'] } },
      },
    };
    const lines = morningReport({
      rounds: [
        { tag: 'v0.57.1', result: 'released', started: '2026-10-16T01:00:00Z', ended: '2026-10-16T01:31:00Z' },
        { tag: 'v0.57.2', result: 'red', stage: 'pretag', why: 'a gate is red', rolledBack: true },
      ],
      request: { ...REQ, version: '0.57.2', status: 'stopped', why: 'a gate is red' },
      states,
    }).join('\n');
    expect(lines).toMatch(/v0\.57\.1: released in 31m 00s/);
    expect(lines).toMatch(/v0\.57\.2: red at pretag - a gate is red \(rolled back: the number is not spent\)/);
    expect(lines).toMatch(/request: v0\.57\.2 on aaaaaaaaaa - stopped: a gate is red/);
    expect(lines).toMatch(/the Store copy: deferred at the pretag; when it ran green elsewhere: --ack store-checked/);
    expect(lines).toMatch(/held by a store: the Snap Store/);
    expect(lines).toMatch(/GitHub's full matrix behind the server patch: red/);
    expect(lines).toMatch(/the replies on the issues/);
    expect(morningReport({ rounds: [] })[0]).toBe('no round since the last report');
    expect(owed({ profile: 'server', matrix: { state: 'green' }, stages: {} })).toEqual([]);
  });
});

describe("the build host's half", () => {
  it('runs the round in the workspace, as its user, in a login shell, under the token wrapper', () => {
    expect(hostCommand({}, 'run').error).toMatch(/FILEX_AUTO_CONTAINER/);
    expect(hostCommand({ FILEX_AUTO_CONTAINER: 'ws' }, 'deploy').error).toMatch(/no such command/);
    expect(hostCommand({ FILEX_AUTO_CONTAINER: 'ws', FILEX_AUTO_USER: 'dev' }, 'run')).toEqual({
      bin: 'docker',
      args: ['exec', '-u', 'dev', 'ws', 'bash', '-lc', 'cd "$HOME/filex" && exec with-gh-token node scripts/train/auto.mjs run'],
    });
    const quoted = hostCommand({ FILEX_AUTO_CONTAINER: 'ws', FILEX_AUTO_CHECKOUT: "/home/dev/it's", FILEX_AUTO_WRAP: '' }, 'report');
    expect(quoted.args?.at(-1)).toBe(`cd '/home/dev/it'"'"'s' && exec node scripts/train/auto.mjs report`);
  });

  it('says the workspace is down at once and once a day, and that it is back', () => {
    const first = downVerdict(null, false, 0);
    expect(first.tell).toBe('down');
    expect(downVerdict(first.mark, false, 60_000).tell).toBeNull();
    const day = downVerdict(first.mark, false, 24 * 60 * 60 * 1000);
    expect(day.tell).toBe('down');
    expect(day.mark?.since).toBe(first.mark?.since);
    expect(downVerdict(first.mark, true, 1)).toEqual({ tell: 'back', mark: null });
    expect(downVerdict(null, true, 1)).toEqual({ tell: null, mark: null });
  });

  it('installs every file the host script imports, and units that run it', () => {
    const installer = read('scripts/train/install-auto.sh');
    // Walk auto-host.mjs's local imports, and theirs.
    const seen = new Set<string>();
    const todo = ['scripts/train/auto-host.mjs'];
    while (todo.length) {
      const f = todo.pop()!;
      if (seen.has(f)) continue;
      seen.add(f);
      for (const m of read(f).matchAll(/^import [^'"]*['"](\.{1,2}\/[^'"]+)['"]/gm)) todo.push(path.posix.join(path.posix.dirname(f), m[1]));
    }
    for (const f of seen) {
      const rel = f.replace(/^scripts\//, '');
      expect(installer, f).toContain(`install -m 0644 "$SCRIPTS/${rel}" "$ROOT/bin/${rel}"`);
    }
    const unit = read('scripts/train/systemd/filex-release-auto.service');
    expect(unit).toMatch(/^ExecStart=@NODE@ @BIN@\/train\/auto-host\.mjs run --env @ENV@$/m);
    expect(unit).toMatch(/^SuccessExitStatus=1 3$/m);
    expect(read('scripts/train/systemd/filex-release-auto-report.service')).toMatch(/^ExecStart=@NODE@ @BIN@\/train\/auto-host\.mjs report --env @ENV@$/m);
    expect(read('scripts/train/systemd/filex-release-auto.timer')).toMatch(/^OnCalendar=@EVERY@$/m);
    expect(read('scripts/train/systemd/filex-release-auto-report.timer')).toMatch(/^OnCalendar=\*-\*-\* @REPORT_AT@$/m);
    // Nothing schedules itself: the timers are enabled by --enable alone.
    expect(installer).toMatch(/if \[ "\$ENABLE" = 1 \]; then\n\s+run systemctl enable --now filex-release-auto\.timer filex-release-auto-report\.timer/);
  });
});
