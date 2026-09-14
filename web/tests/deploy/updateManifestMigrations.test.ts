// The update manifest's `migrations` flag is read from the tags, not typed.
//
// The flag decides whether an install may take a release without asking (a
// release that changes the schema is confirmed, so a backup is taken first).
// It used to be a hand-kept list in the publishing wrapper, and the list went
// stale: measured 2026-09-14, the live manifest did not mark v0.31.0 — which
// added 00030_api_token_kind — and a run of the wrapper would have dropped
// v0.34.0 through v0.41.0 as well. scripts/gen-update-manifest.py now derives
// it: a tag whose tree holds a migration file no earlier tag held.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const here = path.dirname(fileURLToPath(import.meta.url));
const REPO = path.resolve(here, '../../..');
const GEN = path.join(REPO, 'scripts/gen-update-manifest.py');
const PYTHON = process.platform === 'win32' ? 'python' : 'python3';

function derived(repoDir: string): string[] {
  return execFileSync(PYTHON, [GEN, '--print-migrations', '--repo-dir', repoDir], { encoding: 'utf8' })
    .split(/\r?\n/)
    .filter(Boolean);
}

function git(dir: string, ...args: string[]) {
  execFileSync('git', ['-C', dir, '-c', 'user.name=t', '-c', 'user.email=t@t', '-c', 'commit.gpgsign=false', '-c', 'tag.gpgsign=false', ...args], {
    stdio: 'ignore',
  });
}

describe('update manifest: migrations come from the tags', () => {
  it('marks exactly the tags that add a migration file', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-manifest-'));
    git(dir, 'init', '-q');
    const put = (rel: string) => {
      fs.mkdirSync(path.dirname(path.join(dir, rel)), { recursive: true });
      fs.writeFileSync(path.join(dir, rel), '-- sql\n');
    };
    const release = (tag: string) => {
      git(dir, 'add', '-A');
      git(dir, 'commit', '-q', '--allow-empty', '-m', tag);
      git(dir, 'tag', tag);
    };

    put('migrations/00001_init.sql'); // the earliest, flat layout
    release('v0.1.0');
    fs.writeFileSync(path.join(dir, 'README'), 'no schema change\n');
    release('v0.1.1');
    // The layout moved: the same migration under a new path is not a new one.
    fs.rmSync(path.join(dir, 'migrations'), { recursive: true });
    put('backend/db/migrations/sqlite/00001_init.sql');
    put('backend/db/migrations/postgres/00001_init.sql');
    release('v0.2.0');
    put('backend/db/migrations/sqlite/00002_users.sql');
    put('backend/db/migrations/postgres/00002_users.sql');
    release('v0.3.0');
    release('v0.10.0'); // sorted as a version, not as text: after v0.3.0
    put('backend/db/migrations/sqlite/00003_tokens.sql');
    release('v0.11.0');

    expect(derived(dir)).toEqual(['v0.3.0', 'v0.11.0']);
    fs.rmSync(dir, { recursive: true, force: true });
  });

  // ⚠ A CI checkout without tags (fetch-depth 1) has nothing to read; the
  // skip says so instead of passing quietly.
  const hasTag = execFileSync('git', ['-C', REPO, 'tag', '-l', 'v0.31.0'], { encoding: 'utf8' }).trim() !== '';
  it.skipIf(!hasTag)('this repository: v0.31.0 carries 00030 and is marked (needs the tags fetched)', () => {
    const marked = derived(REPO);
    expect(marked).toContain('v0.31.0');
    expect(marked).not.toContain('v0.30.1');
  });
});
