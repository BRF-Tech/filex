// The note a release train runs from (scripts/train/new-train.mjs +
// RELEASE-TEMPLATE.md): the train rule, the cut, what is on board and what
// came after, the merge queue, the steps, and "every task to Done".
//
// ⚠ Why this exists (#179, handed over from #169): the rule - one train a
// day, cut at 10:00 Istanbul, what lands after the cut waits for the next,
// an exploitable hole ships on a patch of its own - was written down on
// 2026-10-06 after 0.51 (+3 h after its cut) and 0.52 (four restarts in one
// day) each took work on board after they had left. The note puts the rule,
// read from docs/CONTRIBUTING.md and never copied, and the exact list of what
// is on board in front of whoever cuts the release. The fixture below dates
// its commits around a cut, so "after the cut" is tested, not assumed.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';

import { parseQueue } from '../../../scripts/train/merge-queue.mjs';
import { cutOf, describeCommit, fill, istanbulDate, trainRule, trainValues } from '../../../scripts/train/new-train.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const TEMPLATE = fs.readFileSync(path.join(REPO, 'scripts', 'train', 'RELEASE-TEMPLATE.md'), 'utf8');
const CONTRIBUTING = fs.readFileSync(path.join(REPO, 'docs', 'CONTRIBUTING.md'), 'utf8');

const roots: string[] = [];
afterAll(() => {
  for (const r of roots) fs.rmSync(r, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
});

describe('the cut', () => {
  it('is 10:00 in Istanbul, 07:00 UTC, all year', () => {
    expect(cutOf('2026-10-07').toISOString()).toBe('2026-10-07T07:00:00.000Z');
    expect(cutOf('2027-01-15').toISOString()).toBe('2027-01-15T07:00:00.000Z');
    expect(() => cutOf('7 Oct')).toThrow(/YYYY-MM-DD/);
  });

  it("takes the train's day in Istanbul, not in UTC", () => {
    expect(istanbulDate(new Date('2026-10-06T22:30:00Z'))).toBe('2026-10-07');
    expect(istanbulDate(new Date('2026-10-07T20:00:00Z'))).toBe('2026-10-07');
  });
});

describe('the rule', () => {
  it('is read from CONTRIBUTING, the whole "When" paragraph and nothing after it', () => {
    const rule = trainRule(CONTRIBUTING);
    expect(rule.startsWith('**When: one train a day')).toBe(true);
    expect(rule).toContain('10:00');
    expect(rule).toContain('patch release (`X.Y.Z+1`)');
    expect(rule).toContain('only=macos');
    expect(rule).not.toContain('**Cut it with');
  });

  it('is refused when CONTRIBUTING no longer has it', () => {
    expect(() => trainRule('# Contributing\n\nNothing about trains.\n')).toThrow(/train rule/);
  });
});

describe('the commits on the list', () => {
  it('name the branch of a merge and the numbers it mentions', () => {
    const c = describeCommit('137b9a97aaaaaaaa', 'Merge feat/167-tenancy-switch (137b9a97): multi-tenant mode becomes a switch - task #167.');
    expect(c.branch).toBe('feat/167-tenancy-switch');
    expect(c.refs).toEqual(['#167']);
    expect(c.line).toMatch(/^- `137b9a97aa` \*\*feat\/167-tenancy-switch\*\* - Merge/);
    expect(describeCommit('abc', "Merge branch 'fix/x' into main").branch).toBe('fix/x');
    expect(describeCommit('abc', 'fix(api): a thing (#12, #12, #13)').refs).toEqual(['#12', '#13']);
    expect(describeCommit('abc', 'docs: no merge').branch).toBeNull();
  });
});

describe('the template', () => {
  it('refuses a placeholder nothing fills', () => {
    expect(() => fill('{{nope}}', {})).toThrow(/nope/);
    expect(fill('{{a}}-{{a}}', { a: 1 })).toBe('1-1');
  });
});

// ── a train on a real history ───────────────────────────────────────────────

function fixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-train-test-'));
  roots.push(root);
  const gitconfig = path.join(root, 'gitconfig');
  fs.writeFileSync(gitconfig, '[user]\n\tname = Train Test\n\temail = train@test.invalid\n[init]\n\tdefaultBranch = main\n[commit]\n\tgpgSign = false\n[tag]\n\tgpgSign = false\n');
  const base: NodeJS.ProcessEnv = { ...process.env };
  for (const k of Object.keys(base)) if (k.startsWith('GIT_')) delete base[k];
  const env = { ...base, GIT_CONFIG_GLOBAL: gitconfig, GIT_CONFIG_NOSYSTEM: '1' };
  const repo = path.join(root, 'repo');
  fs.mkdirSync(repo);
  const git = (args: string[], at?: string) => {
    const r = spawnSync('git', ['-C', repo, ...args], { env: at ? { ...env, GIT_AUTHOR_DATE: at, GIT_COMMITTER_DATE: at } : env, encoding: 'utf8' });
    if (r.status !== 0) throw new Error(`git ${args.join(' ')}: ${r.stderr}`);
    return r.stdout.trim();
  };
  git(['init', '-q']);
  let n = 0;
  const commit = (subject: string, at: string) => {
    fs.writeFileSync(path.join(repo, `f${n++}.txt`), subject);
    git(['add', '-A'], at);
    git(['commit', '-q', '-m', subject], at);
  };
  commit('chore(release): v0.1.0', '2026-10-05 12:00:00 +0000');
  git(['tag', 'v0.1.0']);
  commit('Merge feat/one (1111111): the first thing - task #12.', '2026-10-06 20:00:00 +0000');
  commit('fix: a direct fix (#13)', '2026-10-07 06:30:00 +0000');
  commit('Merge feat/late (2222222): it came after the cut (#14)', '2026-10-07 08:15:00 +0000');
  return { repo };
}

describe('a train on a real history', () => {
  it('lists main up to the cut on board, and what came after for the next train', () => {
    const { repo } = fixture();
    const v = trainValues({ repo, version: '0.2.0', date: '2026-10-07', now: new Date('2026-10-07T09:00:00Z') });
    expect(v.prevTag).toBe('v0.1.0');
    expect(v.profile).toBe('minor');
    expect(v.cut).toBe('2026-10-07 10:00');
    expect(v.cutUtc).toBe('07:00');
    expect(v.onTrain).toContain('fix: a direct fix (#13)');
    expect(v.onTrain).toContain('**feat/one**');
    expect(v.onTrain).not.toContain('feat/late');
    expect(v.afterCut).toContain('**feat/late**');
    expect(v.tasks).toBe('#13, #12');
  });

  it('before the cut, lists main as it is and says the list is not final', () => {
    const { repo } = fixture();
    const v = trainValues({ repo, version: '0.2.0', date: '2026-10-07', now: new Date('2026-10-07T06:00:00Z') });
    expect(v.onTrain).toContain('feat/late');
    expect(v.cutState).toContain('not yet');
    expect(v.afterCut).toContain('has not happened yet');
  });

  it('calls a patch a patch, with its own train', () => {
    const { repo } = fixture();
    const v = trainValues({ repo, version: '0.1.1', date: '2026-10-07', now: new Date('2026-10-07T09:00:00Z') });
    expect(v.profile).toBe('patch');
    expect(v.kind).toContain('rides no minor');
  });

  it('fills every placeholder of the template, keeps an empty merge queue the queue can read, and ends on Done', () => {
    const { repo } = fixture();
    const values = { ...trainValues({ repo, version: '0.2.0', date: '2026-10-07', now: new Date('2026-10-07T09:00:00Z') }), trainRule: trainRule(CONTRIBUTING) };
    const names = [...TEMPLATE.matchAll(/\{\{(\w+)\}\}/g)].map((m) => m[1]);
    for (const n of names) expect(values, `{{${n}}}`).toHaveProperty(n);
    const note = fill(TEMPLATE, values);
    expect(note).not.toContain('{{');
    expect(note).toContain('one train a day');
    expect(note).toContain('```queue');
    expect(parseQueue(note, { dir: repo, msgDir: repo })).toEqual([]);
    expect(note).toContain('moved to Done, in the same turn, each with its evidence');
    expect(note).toContain('bash scripts/train/filex-ship.sh 0.2.0');
    expect(note).toContain('pnpm release 0.2.0 --resume --ack deploy');
  });
});
