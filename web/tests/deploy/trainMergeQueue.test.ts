// The train's merge queue (scripts/train/merge-queue.mjs), proved on a
// throwaway repository: branches merged one after another with --no-ff and
// their own messages, the CHANGELOG conflict merged by section, a conflict
// anywhere else aborted for a person, and a second run carrying on where the
// first stopped.
//
// ⚠ Why this exists (#179): the 0.52 and 0.53 rounds merged their branches by
// hand in the main session, five commands a branch, the CHANGELOG conflict
// solved with a scratch script and the build checked once at the end. An
// agent cannot merge onto main (the permission system refuses it), so this is
// the one command the main session approves for the whole queue - and the
// order, the refusal and the resume are what a unit test cannot show.
//
// Nothing here touches the network or the user's git configuration
// (GIT_CONFIG_GLOBAL, GIT_CONFIG_NOSYSTEM), and the queue is told about an
// empty settings file, so it notifies nobody.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';

import { goShellArgv } from '../../../scripts/lib/go-build.mjs';
import { parseBody, splitUnreleased, unreleasedProblems } from '../../../scripts/train/changelog-merge.mjs';
import { chainVerdict, isEmbargoed, needsAChain } from '../../../scripts/lib/embargo.mjs';
import { embargoProblem, goCheckScript, msgName, parseArgs, parseQueue, splitConflicts } from '../../../scripts/train/merge-queue.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');
const QUEUE = path.join(REPO, 'scripts', 'train', 'merge-queue.mjs');
const TIMEOUT = 180_000;

const roots: string[] = [];
afterAll(() => {
  for (const r of roots) fs.rmSync(r, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
});

describe('the queue file', () => {
  it('reads the first ```queue block of a note, comments and blank lines aside', () => {
    const dir = path.resolve('/q');
    const q = parseQueue('# Release 0.53\n\n```queue\n# branch  message\nfeat/a  msg/a.txt\n\nfix/b   # its message is in msg/\n```\n\nfeat/not-in-the-block\n', {
      dir,
      msgDir: path.join(dir, 'msg'),
    });
    expect(q).toEqual([
      { branch: 'feat/a', msg: path.resolve(dir, 'msg/a.txt') },
      { branch: 'fix/b', msg: path.resolve(dir, path.join(dir, 'msg', 'fix-b.txt')) },
    ]);
  });

  it('reads every line of a plain file', () => {
    const dir = path.resolve('/q');
    expect(parseQueue('feat/a\nfeat/b\n', { dir, msgDir: dir }).map((l: { branch: string }) => l.branch)).toEqual(['feat/a', 'feat/b']);
  });

  it('refuses what it could misread', () => {
    const o = { dir: path.resolve('/q'), msgDir: path.resolve('/q') };
    expect(() => parseQueue('feat/a msg/a b.txt\n', o)).toThrow(/at most one message file/);
    expect(() => parseQueue('feat/a\nfeat/a\n', o)).toThrow(/listed twice/);
    expect(() => parseQueue('-x\n', o)).toThrow(/not a branch name/);
  });

  it('names a message file after its branch', () => {
    expect(msgName('feat/178-ship')).toBe('feat-178-ship.txt');
    expect(msgName('sec/053-root')).toBe('sec-053-root.txt');
  });

  it('wants a queue, and takes repeatable checks', () => {
    expect(() => parseArgs([])).toThrow(/--queue/);
    const o = parseArgs(['--queue', 'q.md', '--check', 'a', '--check', 'b', '--no-go', '--onto', 'int/053']);
    expect(o).toMatchObject({ queue: 'q.md', checks: ['a', 'b'], go: false, onto: 'int/053' });
  });
});

describe('what it does with a conflict', () => {
  it('merges CHANGELOG.md itself and leaves every other file to a person', () => {
    expect(splitConflicts(['CHANGELOG.md', 'src/a.txt', ''])).toEqual({ changelog: true, others: ['src/a.txt'] });
    expect(splitConflicts(['CHANGELOG.md'])).toEqual({ changelog: true, others: [] });
  });
});

describe('the Go check after every merge', () => {
  it('builds and vets the module, with the embed directories go:embed needs', () => {
    const s = goCheckScript();
    expect(s).toContain('go build ./... && go vet ./...');
    expect(s).toContain('embed/admin');
    expect(s).toContain('embed/web');
  });

  it('runs Go the one way this repository runs it (scripts/lib/go-build.mjs), as the release gates do', () => {
    const dir = path.join(REPO, 'backend');
    const native = goShellArgv({ dir, checkout: REPO, script: 'go vet ./...', toolchain: 'native', bash: 'bash' });
    expect(native[0]).toBe('bash');
    expect(native[2]).toMatch(/^export FILEX_CHECKOUT='[^']+' GOTOOLCHAIN=go\d+\.\d+\.\d+ && cd '[^']+backend' && go vet \.\/\.\.\.$/);
    const wsl = goShellArgv({ dir, checkout: REPO, script: 'go vet ./...', toolchain: 'wsl' });
    expect(wsl.slice(0, 4)).toEqual(['wsl', '-e', 'bash', '-lc']);
    expect(wsl[4]).toContain('GOFLAGS=-buildvcs=false');
    expect(wsl[4]).toContain('rsync -a --delete');
    // The release plan's Go gates build their command through the same
    // function; a second copy of the WSL recipe is how one of them drifts.
    const planSrc = fs.readFileSync(path.join(REPO, 'scripts', 'release', 'plan.mjs'), 'utf8');
    expect(planSrc).toContain('goShellArgv(');
    expect(planSrc).not.toMatch(/\['wsl', '-e'/);
  });
});

// ── the queue on a real history ─────────────────────────────────────────────

function isolatedEnv(root: string): NodeJS.ProcessEnv {
  const gitconfig = path.join(root, 'gitconfig');
  fs.writeFileSync(
    gitconfig,
    '[user]\n\tname = Queue Test\n\temail = queue@test.invalid\n[init]\n\tdefaultBranch = main\n' +
      '[core]\n\tautocrlf = false\n[commit]\n\tgpgSign = false\n[tag]\n\tgpgSign = false\n',
  );
  const settings = path.join(root, 'train.env');
  fs.writeFileSync(settings, '# nobody is told anything\n');
  const env: NodeJS.ProcessEnv = { ...process.env };
  for (const k of Object.keys(env)) if (k.startsWith('GIT_') || k.startsWith('FILEX_WAKE') || k.startsWith('FILEX_NOTIFY')) delete env[k];
  return { ...env, GIT_CONFIG_GLOBAL: gitconfig, GIT_CONFIG_NOSYSTEM: '1', GIT_TERMINAL_PROMPT: '0', NO_COLOR: '1', FILEX_TRAIN_ENV: settings };
}

function sh(env: NodeJS.ProcessEnv, bin: string, args: string[], cwd?: string) {
  const r = spawnSync(bin, args, { cwd, env, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
  if (r.status !== 0) throw new Error(`${bin} ${args.join(' ')} -> ${r.status}\n${r.stdout}\n${r.stderr}`);
  return r.stdout.trim();
}

function write(file: string, text: string) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, text);
}

const A = '- **A.** There before the train.';
const O = '- **One.** The first branch.';
const T = '- **Two.** The second branch.';
const F = '- **Fix.** The second branch fixed something.';
const changelog = (added: string[], fixed: string[] = []) =>
  `# Changelog\n\n## [Unreleased]\n\n### Added\n\n${added.join('\n')}\n\n${fixed.length ? `### Fixed\n\n${fixed.join('\n')}\n\n` : ''}## [0.1.0] - 2026-01-01\n\n### Added\n\n- **First.** It exists.\n`;

function makeFixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-queue-test-'));
  roots.push(root);
  const env = isolatedEnv(root);
  const repo = path.join(root, 'repo');
  fs.mkdirSync(repo);
  const git = (...args: string[]) => sh(env, 'git', ['-C', repo, ...args]);
  git('init', '-q');
  write(path.join(repo, 'CHANGELOG.md'), changelog([A]));
  write(path.join(repo, 'src/a.txt'), 'one\ntwo\nthree\n');
  git('add', '-A');
  git('commit', '-q', '-m', 'start');

  git('checkout', '-q', '-b', 'feat/one');
  write(path.join(repo, 'CHANGELOG.md'), changelog([O, A]));
  write(path.join(repo, 'src/a.txt'), 'ONE\ntwo\nthree\n');
  git('commit', '-q', '-am', 'feat: one');

  git('checkout', '-q', 'main');
  git('checkout', '-q', '-b', 'feat/two');
  write(path.join(repo, 'CHANGELOG.md'), changelog([T, A], [F]));
  write(path.join(repo, 'src/new.txt'), 'new\n');
  git('add', '-A');
  git('commit', '-q', '-m', 'feat: two');

  git('checkout', '-q', 'main');
  git('checkout', '-q', '-b', 'feat/three');
  write(path.join(repo, 'src/a.txt'), 'uno\ntwo\nthree\n');
  git('commit', '-q', '-am', 'feat: three');
  git('checkout', '-q', 'main');

  write(path.join(root, 'msg', 'feat-one.txt'), 'Merge feat/one: the first branch.\n\n#178 a line that starts with a number sign stays.\n');
  write(path.join(root, 'msg', 'feat-two.txt'), 'Merge feat/two: the second branch.\n');
  write(path.join(root, 'msg', 'feat-three.txt'), 'Merge feat/three: the third branch.\n');
  const queue = path.join(root, 'RELEASE.md');
  write(queue, '# Release\n\n```queue\nfeat/one\nfeat/two    # CHANGELOG conflicts with feat/one\nfeat/three  # src/a.txt conflicts with feat/one\n```\n');
  const runQueue = (...extra: string[]) =>
    spawnSync(process.execPath, [QUEUE, '--queue', queue, '--repo', repo, '--no-go', ...extra], { cwd: root, env, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
  return { root, repo, env, git, runQueue };
}

describe('the merge queue on a real history', () => {
  it(
    'merges in order, solves the changelog, stops for a person on code, and carries on after',
    () => {
      const f = makeFixture();

      // --plan merges nothing.
      const plan = f.runQueue('--plan');
      expect(plan.status, plan.stdout + plan.stderr).toBe(0);
      expect(plan.stdout).toContain('feat/three');
      expect(f.git('rev-list', '--count', 'HEAD')).toBe('1');

      // It never switches branches.
      f.git('checkout', '-q', 'feat/one');
      const wrong = f.runQueue();
      expect(wrong.status).toBe(1);
      expect(wrong.stdout).toContain('check out main first');
      f.git('checkout', '-q', 'main');

      const first = f.runQueue('--check', 'test -f src/a.txt');
      expect(first.status, first.stdout + first.stderr).toBe(3);
      expect(first.stdout).toContain('WAITING FOR A PERSON');
      expect(first.stdout).toContain('src/a.txt');
      expect(first.stdout).toContain('CHANGELOG.md merged by section');

      // Two merge commits, each with two parents and its own message.
      expect(f.git('log', '--first-parent', '--format=%s', '-2')).toBe('Merge feat/two: the second branch.\nMerge feat/one: the first branch.');
      for (const ref of ['HEAD', 'HEAD~1']) expect(f.git('rev-list', '--parents', '-n', '1', ref).split(' ')).toHaveLength(3);
      expect(f.git('log', '-1', '--format=%B', 'HEAD~1')).toContain('#178 a line that starts with a number sign stays.');

      // The changelog: both entries under one heading, the fix in its section.
      const text = fs.readFileSync(path.join(f.repo, 'CHANGELOG.md'), 'utf8');
      const sections = parseBody(splitUnreleased(text)!.body).sections.map((s: { title: string; blocks: Array<{ text: string }> }) => ({
        title: s.title,
        entries: s.blocks.map((b) => b.text),
      }));
      expect(sections).toEqual([
        { title: 'Added', entries: [O, T, A] },
        { title: 'Fixed', entries: [F] },
      ]);
      expect(unreleasedProblems(text)).toEqual([]);

      // The third merge was aborted: nothing half-done is left behind.
      expect(f.git('status', '--porcelain')).toBe('');
      expect(spawnSync('git', ['-C', f.repo, 'rev-parse', '-q', '--verify', 'MERGE_HEAD'], { env: f.env }).status).not.toBe(0);
      expect(fs.readFileSync(path.join(f.repo, 'src/a.txt'), 'utf8')).toBe('ONE\ntwo\nthree\n');

      // A person merges the third by hand; the queue carries on and finds
      // nothing left to do.
      spawnSync('git', ['-C', f.repo, '-c', 'rerere.enabled=true', 'merge', '--no-ff', '--no-commit', 'feat/three'], { env: f.env });
      write(path.join(f.repo, 'src/a.txt'), 'ONE (uno)\ntwo\nthree\n');
      f.git('add', 'src/a.txt');
      f.git('-c', 'rerere.enabled=true', 'commit', '--cleanup=whitespace', '-F', path.join(f.root, 'msg', 'feat-three.txt'));

      const second = f.runQueue();
      expect(second.status, second.stdout + second.stderr).toBe(0);
      expect(second.stdout.match(/already in/g)).toHaveLength(3);
      expect(second.stdout).toContain('every branch is in main');
    },
    TIMEOUT,
  );

  it(
    'does not start without a message for every branch it has to merge',
    () => {
      const f = makeFixture();
      fs.rmSync(path.join(f.root, 'msg', 'feat-two.txt'));
      const r = f.runQueue();
      expect(r.status).toBe(1);
      expect(r.stdout).toContain('feat/two: no message');
      expect(f.git('rev-list', '--count', 'HEAD')).toBe('1');
    },
    TIMEOUT,
  );

  it(
    'stops on a red check with the merge made, and says how to take it back',
    () => {
      const f = makeFixture();
      const r = f.runQueue('--check', 'test -f does-not-exist');
      expect(r.status).toBe(1);
      expect(r.stdout).toContain('a check is red');
      expect(r.stdout).toContain('reset --hard');
      expect(f.git('log', '-1', '--format=%s')).toBe('Merge feat/one: the first branch.');
    },
    TIMEOUT,
  );
});

// ── an embargoed branch enters only with its chain (task #182) ──────────────
//
// On 2026-10-10 an embargoed branch (sec/<name>) was pushed with
// `-o ci.skip`: the private pipeline recorded a "skipped" run, nothing ran,
// and the branch was merged into main that afternoon without the chain. The
// queue now asks the private pipeline about an embargoed tip before it
// merges anything, and an explicit --embargo-unchecked is written into the
// merge commit, where the history keeps it.

describe('an embargoed branch and its chain', () => {
  const ROW = { branch: 'sec/hole', tip: 'f2cb5832aaaabbbbcccc', merged: false };
  const SETTINGS = { EMBARGO_API: 'https://forge.example.com/api/v4/projects/7', EMBARGO_TOKEN_FILE: '/dev/null' };
  const asks = (pipelines: unknown[]) => async () => pipelines;
  const token = () => 'test-token';

  it('says whether the chain passed on a tip, and why not', () => {
    expect(chainVerdict([{ status: 'success' }]).ok).toBe(true);
    expect(chainVerdict([{ status: 'skipped' }, { status: 'success' }]).ok).toBe(true);
    expect(chainVerdict([]).why).toMatch(/no pipeline/);
    expect(chainVerdict([{ status: 'skipped' }]).why).toMatch(/skipped.*ci\.skip/);
    expect(chainVerdict([{ status: 'running' }]).why).toMatch(/still running/);
    expect(chainVerdict([{ status: 'failed' }, { status: 'skipped' }]).why).toMatch(/ended failed/);
    // Only a chain that never ran needs starting: red is waiting for a fix.
    expect(needsAChain([])).toBe(true);
    expect(needsAChain([{ status: 'skipped' }])).toBe(true);
    expect(needsAChain([{ status: 'failed' }])).toBe(false);
    expect(isEmbargoed('sec/hole')).toBe(true);
    expect(isEmbargoed('fix/sec-thing')).toBe(false);
  });

  it('asks only about an embargoed branch that is not in yet', async () => {
    const never = async () => {
      throw new Error('asked');
    };
    for (const row of [
      { ...ROW, branch: 'fix/thing' },
      { ...ROW, merged: true },
      { ...ROW, tip: null },
    ]) {
      expect(await embargoProblem(row, { settings: SETTINGS, ask: never, token })).toBe('');
    }
  });

  it('refuses a tip whose chain was skipped, did not pass, or cannot be asked about', async () => {
    expect(await embargoProblem(ROW, { settings: SETTINGS, ask: asks([{ status: 'skipped' }]), token })).toMatch(/sec\/hole: its pipeline was skipped/);
    expect(await embargoProblem(ROW, { settings: SETTINGS, ask: asks([{ status: 'failed' }]), token })).toMatch(/ended failed/);
    expect(await embargoProblem(ROW, { settings: {}, ask: asks([{ status: 'success' }]), token })).toMatch(/EMBARGO_API/);
    const down = async () => {
      throw new Error('GET /pipelines: 502');
    };
    expect(await embargoProblem(ROW, { settings: SETTINGS, ask: down, token })).toMatch(/could not be asked.*502/);
    // Green: nothing to say.
    expect(await embargoProblem(ROW, { settings: SETTINGS, ask: asks([{ status: 'success' }]), token })).toBe('');
    // Told why not: nothing to refuse (the reason goes into the commit).
    expect(await embargoProblem(ROW, { settings: SETTINGS, unchecked: 'shipped as a normal fix', ask: asks([]), token })).toBe('');
  });

  it('wants the reason in words', () => {
    expect(() => parseArgs(['--queue', 'q', '--embargo-unchecked', '   '])).toThrow(/reason/);
    expect(parseArgs(['--queue', 'q', '--embargo-unchecked', 'shipped as a normal fix']).embargoUnchecked).toBe('shipped as a normal fix');
  });

  it(
    'on a real history: does not start without the private pipeline, and says --embargo-unchecked in the commit',
    () => {
      const f = makeFixture();
      f.git('checkout', '-q', '-b', 'sec/hole');
      write(path.join(f.repo, 'src/hole.txt'), 'closed\n');
      f.git('add', '-A');
      f.git('commit', '-q', '-m', 'fix: the hole');
      f.git('checkout', '-q', 'main');
      write(path.join(f.root, 'msg', 'sec-hole.txt'), 'Merge sec/hole: the hole is closed.\n');
      write(path.join(f.root, 'RELEASE.md'), '```queue\nsec/hole\n```\n');

      const refused = f.runQueue();
      expect(refused.status, refused.stdout + refused.stderr).toBe(1);
      expect(refused.stdout).toContain('NOT STARTED');
      expect(refused.stdout).toContain('sec/hole: an embargoed branch enters only with a green chain');
      expect(f.git('rev-list', '--count', 'HEAD')).toBe('1');

      const waved = f.runQueue('--embargo-unchecked', 'shipped as a normal fix, decided by the owner');
      expect(waved.status, waved.stdout + waved.stderr).toBe(0);
      expect(waved.stdout).toContain('merged without a green chain');
      const body = f.git('log', '-1', '--format=%B');
      expect(body).toContain('Merge sec/hole: the hole is closed.');
      expect(body).toContain('Embargo-Unchecked: shipped as a normal fix, decided by the owner');
    },
    TIMEOUT,
  );
});
