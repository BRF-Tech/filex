// The public tree keeps the executable bit of every file it shares with this
// repository (lesson #1108).
//
// ⚠⚠ What went wrong: the export rebuilds the public tree with
// `git archive | tar -x` and stages it with `git add -A`. On Windows git cannot
// see an executable bit (core.filemode is false there), so every NEW file was
// staged 100644. e2e/realenv/run.sh - 100755 here, and docs/CONTRIBUTING.md
// tells a reader to run it as it is - reached the public repository 100644
// and answered "Permission denied" in a public checkout. git said nothing, and
// CI calls `bash script.sh`, which runs regardless.
//
// The export now copies every bit from HEAD into the index it stages
// (scripts/export-public.sh, section 4b); these drive that step on its own
// (`--modes-only`) against a scratch checkout. The release reads the bits back
// independently (scripts/release/stages.mjs → execBitDrift, pinned below and
// end to end in releaseCli.test.ts).
import { execFileSync, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';

import { execBitDrift, gitModes } from '../../../scripts/release/checks.mjs';
import { findBash } from '../../../scripts/release/engine.mjs';

const REPO = path.resolve(__dirname, '../../..');
const slash = (p: string) => p.split(path.sep).join('/');
const SCRIPT = path.join(REPO, 'scripts', 'export-public.sh');
// ⚠ The exporter is never published, so the public tree skips the half of
// this file that runs it (exportRewriteScope.test.ts, v0.45.0).
const script = fs.existsSync(SCRIPT) ? fs.readFileSync(SCRIPT, 'utf8') : '';
const bash = script ? findBash() : null;
const TIMEOUT = 60_000;
const NUL = String.fromCharCode(0);

const temps: string[] = [];
afterEach(() => {
  for (const d of temps.splice(0)) fs.rmSync(d, { recursive: true, force: true });
});

function git(dir: string, ...args: string[]) {
  return execFileSync('git', ['-C', dir, ...args], { encoding: 'utf8', stdio: 'pipe' });
}

/** path → mode, as `git ls-files -s` (the index) or `git ls-tree -r HEAD` says. */
function indexModes(dir: string) {
  return gitModes(git(dir, 'ls-files', '-s', '-z')) as Map<string, string>;
}

/** One file of each mode from THIS repository's HEAD: the script's source. */
function repoFiles() {
  const modes = gitModes(git(REPO, 'ls-tree', '-r', '-z', 'HEAD')) as Map<string, string>;
  const exe = [...modes].find(([p, m]) => m === '100755' && p.endsWith('.sh'))?.[0];
  const plain = [...modes].find(([p, m]) => m === '100644' && p.endsWith('.md'))?.[0];
  return { exe, plain };
}

/**
 * A scratch checkout holding `files` (path → mode in its index), set exactly
 * as given whatever the file system can keep.
 */
function checkout(files: Record<string, '100644' | '100755'>) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-export-modes-'));
  temps.push(dir);
  git(dir, 'init', '-q');
  for (const [rel, mode] of Object.entries(files)) {
    fs.mkdirSync(path.join(dir, path.dirname(rel)), { recursive: true });
    fs.writeFileSync(path.join(dir, rel), `${rel}\n`);
    fs.chmodSync(path.join(dir, rel), mode === '100755' ? 0o755 : 0o644);
    git(dir, 'add', '--', rel);
    git(dir, 'update-index', `--chmod=${mode === '100755' ? '+x' : '-x'}`, '--', rel);
  }
  return dir;
}

function modes(dir: string) {
  const r = spawnSync(bash!, [slash(SCRIPT), '--modes-only', slash(dir)], { encoding: 'utf8' });
  return { status: r.status, out: `${r.stdout}${r.stderr}` };
}

describe.skipIf(!script || !bash)('the export copies every executable bit from this repository (lesson #1108)', () => {
  it('gives a file the bit it has here, and takes it from a file that has none here', { timeout: TIMEOUT }, () => {
    const { exe, plain } = repoFiles();
    expect(exe, 'this repository has no executable .sh file to test with').toBeTruthy();
    expect(plain, 'this repository has no plain .md file to test with').toBeTruthy();
    // As the Windows export staged them: the script without its bit, and a
    // document with one it never had.
    const dir = checkout({ [exe!]: '100644', [plain!]: '100755' });
    const r = modes(dir);
    expect(r.status, r.out).toBe(0);
    const after = indexModes(dir);
    expect(after.get(exe!), `${exe} lost its executable bit on the way out`).toBe('100755');
    expect(after.get(plain!), `${plain} gained an executable bit on the way out`).toBe('100644');
  });

  it("leaves alone what this repository does not have: the public checkout's own workflows, and its other files", { timeout: TIMEOUT }, () => {
    const { exe } = repoFiles();
    const dir = checkout({ '.github/workflows/ci.yml': '100644', 'tools/only-public.sh': '100755', [exe!]: '100755' });
    const before = indexModes(dir);
    const r = modes(dir);
    expect(r.status, r.out).toBe(0);
    expect(indexModes(dir)).toEqual(before);
  });

  it('will not call a directory that is not a checkout done (exit 2)', { timeout: TIMEOUT }, () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-export-modes-'));
    temps.push(dir);
    const r = modes(dir);
    expect(r.status, r.out).toBe(2);
  });

  it('runs section 4b on every export, right after the tree is staged', () => {
    const add = script.indexOf('git -C "$exp" add -A');
    const mirror = script.indexOf('mirror_exec_bits "$exp" || exit $?', add);
    expect(add, 'the export no longer stages with `git add -A`').toBeGreaterThan(0);
    expect(mirror, 'the export stages the tree and never copies the executable bits').toBeGreaterThan(add);
  });
});

describe('the release reads the executable bits back (execBitDrift)', () => {
  const z = (rows: Array<[string, string]>, ls: 'tree' | 'index') =>
    rows.map(([mode, p]) => (ls === 'tree' ? `${mode} blob 0123456789abcdef0123456789abcdef01234567\t${p}` : `${mode} 0123456789abcdef0123456789abcdef01234567 0\t${p}`)).join(NUL) + NUL;

  it('names a script that lost its bit and a file that gained one', () => {
    const priv = z([['100755', 'e2e/realenv/run.sh'], ['100644', 'README.md'], ['100644', 'docs/a b.md']], 'tree');
    const pub = z([['100644', 'e2e/realenv/run.sh'], ['100755', 'README.md'], ['100644', 'docs/a b.md']], 'index');
    const d = execBitDrift(priv, pub);
    expect(d.lost).toEqual(['e2e/realenv/run.sh']);
    expect(d.gained).toEqual(['README.md']);
    expect(d.checked).toBe(3);
  });

  it("compares only regular files both trees hold: not the public checkout's workflows, not a withheld file, not a symlink", () => {
    const priv = z([['100755', 'scripts/export-public.sh'], ['120000', 'link'], ['100755', 'run.sh']], 'tree');
    const pub = z([['100755', '.github/workflows/ci.yml'], ['100644', 'link'], ['100755', 'run.sh']], 'index');
    const d = execBitDrift(priv, pub);
    expect(d).toEqual({ lost: [], gained: [], checked: 1 });
  });

  it('reads a path with a tab-free, unquoted name as -z gives it', () => {
    const m = gitModes(z([['100755', 'küçük dosya.sh']], 'index')) as Map<string, string>;
    expect([...m]).toEqual([['küçük dosya.sh', '100755']]);
  });
});
