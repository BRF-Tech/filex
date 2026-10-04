// scripts/export-public.sh refuses to publish one of the project's own server
// addresses, a real person's, tenant's or customer's name, or a credential
// written as a shell default. It has to look for them in exactly the files the
// export publishes: every one of them, and nothing else.
//
// ⚠⚠ 0.50 (2026-10-03, lesson #962): the scan was a `grep -r` of the whole
// public checkout, git-ignored build output included. backend/embed/web there
// held the previous release's UI (the release copies the new one in only after
// the export), a comment in the 0.49 bundle named a person the 0.50 list had
// just learned, and the export stopped half-way through rebuilding the tree:
// 795 modified, 8 deleted and 726 untracked files to put back by hand, over a
// file no release publishes.
//
// These drive the real script in its --scan-only mode against a scratch git
// repository. The names and addresses are the script's own lists, read from it
// at run time: they are written down there alone, never in a test.
import { execFileSync, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';

import { findBash } from '../../../scripts/release/engine.mjs';
import { bashArray } from '../helpers/exporterArrays';

const REPO = path.resolve(__dirname, '../../..');
const slash = (p: string) => p.split(path.sep).join('/');
const SCRIPT = path.join(REPO, 'scripts', 'export-public.sh');
// ⚠ The exporter is never published, so the public tree skips this whole
// file. Read unconditionally, a test like this one failed the public release
// gate of v0.45.0 with ENOENT (exportRewriteScope.test.ts).
const script = fs.existsSync(SCRIPT) ? fs.readFileSync(SCRIPT, 'utf8') : '';
// Git Bash on Windows, never whatever `bash` the PATH finds first (WSL's
// System32 copy cannot read this checkout): scripts/release/engine.mjs.
const bash = script ? findBash() : null;
const TIMEOUT = 60_000;

const names = script ? bashArray(script, 'private_names') : [];
const addrs = script ? bashArray(script, 'infra_addrs') : [];

// One line of source per check, each built at run time. The credential is put
// together from pieces so that no line of this file is one.
const FINDINGS: Array<[string, () => string]> = [
  ['a private name', () => `// reviewed by ${names[0]}\n`],
  ['a server address', () => `const upstream = 'https://${addrs[0]}:8443';\n`],
  ['a credential written as a shell default', () => `API_TOKEN=${'$'}{SEED${':-'}${'k'.repeat(24)}}\n`],
];

const temps: string[] = [];
afterEach(() => {
  for (const d of temps.splice(0)) fs.rmSync(d, { recursive: true, force: true });
});

function git(dir: string, ...args: string[]) {
  return execFileSync('git', ['-C', dir, ...args], { encoding: 'utf8', stdio: 'pipe' });
}

/**
 * A scratch checkout holding `files`, with `tracked` in the index and the rest
 * left untracked, and a .gitignore that ignores the build output this
 * repository's does.
 */
function checkout(files: Record<string, string>, tracked: string[] = []) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-export-scan-'));
  temps.push(dir);
  git(dir, 'init', '-q');
  const all = { '.gitignore': '/backend/embed/web/\n/backend/embed/admin/\n/desktop/release/\n', ...files };
  for (const [rel, body] of Object.entries(all)) {
    fs.mkdirSync(path.join(dir, path.dirname(rel)), { recursive: true });
    fs.writeFileSync(path.join(dir, rel), body);
  }
  git(dir, 'add', '--', '.gitignore', ...tracked);
  return dir;
}

function scan(dir: string) {
  const r = spawnSync(bash!, [slash(SCRIPT), '--scan-only', slash(dir)], { encoding: 'utf8' });
  return { status: r.status, out: `${r.stdout}${r.stderr}` };
}

describe.skipIf(!script || !bash)('the export refuses what must never be published, in the files it publishes', () => {
  it('reads its lists from the script', () => {
    expect(names.length, 'scripts/export-public.sh lists no private_names').toBeGreaterThan(0);
    expect(addrs.length, 'scripts/export-public.sh lists no infra_addrs').toBeGreaterThan(0);
  });

  it.each(FINDINGS)(
    'refuses %s in a tracked file',
    (_what, line) => {
      const r = scan(checkout({ 'src/a.ts': line() }, ['src/a.ts']));
      expect(r.status, r.out).toBe(1);
      expect(r.out).toContain('Refusing');
      expect(r.out).toContain('src/a.ts:1:');
    },
    TIMEOUT,
  );

  it.each(FINDINGS)(
    'refuses %s in an untracked file that is not ignored: `git add -A` publishes it',
    (_what, line) => {
      const r = scan(checkout({ 'docs/new.md': line() }));
      expect(r.status, r.out).toBe(1);
      expect(r.out).toContain('docs/new.md:1:');
    },
    TIMEOUT,
  );

  it.each(FINDINGS)(
    "lets %s pass in ignored build output: the last release's UI in backend/embed/web is not published",
    (_what, line) => {
      const dir = checkout(
        {
          'backend/embed/web/index-0ld.js': line(),
          'backend/embed/admin/assets/index-0ld.js': line(),
          'desktop/release/latest.yml': line(),
          'src/a.ts': 'export const ok = true;\n',
        },
        ['src/a.ts'],
      );
      const r = scan(dir);
      expect(r.status, r.out).toBe(0);
      expect(r.out).not.toContain('Refusing');
    },
    TIMEOUT,
  );

  it(
    "keeps a name's exemptions (history, the public checkout's own workflows), and only a name's",
    { timeout: TIMEOUT },
    () => {
      const history = ['CHANGELOG.md', 'backend/internal/db/migrations/0001_init.sql', 'docs-site/data/releases.json'];
      const exempt = [...history, '.github/workflows/ci.yml'];
      const name = FINDINGS[0]![1]();
      const quiet = scan(checkout(Object.fromEntries(exempt.map((f) => [f, name])), exempt));
      expect(quiet.status, quiet.out).toBe(0);

      const addr = FINDINGS[1]![1]();
      const loud = scan(checkout({ 'CHANGELOG.md': addr }, ['CHANGELOG.md']));
      expect(loud.status, loud.out).toBe(1);
      expect(loud.out).toContain('CHANGELOG.md:1:');
    },
  );

  it('changes nothing in the checkout it scans', { timeout: TIMEOUT }, () => {
    const name = FINDINGS[0]![1]();
    const dir = checkout({ 'backend/embed/web/index-0ld.js': name, 'src/a.ts': name }, ['src/a.ts']);
    const before = git(dir, 'status', '--porcelain', '--ignored', '--untracked-files=all');
    expect(scan(dir).status).toBe(1);
    expect(git(dir, 'status', '--porcelain', '--ignored', '--untracked-files=all')).toBe(before);
    expect(fs.readFileSync(path.join(dir, 'backend/embed/web/index-0ld.js'), 'utf8')).toBe(name);
  });

  it('will not call a directory that is not a checkout clean (exit 2)', { timeout: TIMEOUT }, () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-export-scan-'));
    temps.push(dir);
    const r = scan(dir);
    expect(r.status, r.out).toBe(2);
  });
});
