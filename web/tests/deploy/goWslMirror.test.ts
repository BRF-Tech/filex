// Go under WSL must not read the tree over /mnt/<drive>.
//
// ⚠ Why: /mnt/g is a 9P bridge. Go reads and hashes every package source on
// each build, and every one of those reads goes through dllhost.exe
// (Plan9FileSystem) on the Windows side: 0.5-0.9 of a core per test run, 8.4
// hours of CPU over two days, and a workstation stuck at 60-80% while agents
// ran `go test` on /mnt/g (2026-09-26, lessons #577 and #578). The fix is to
// mirror the repository onto WSL's own disk first and run Go there; every Go
// call this repo makes through WSL goes through one helper that does it.
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { moduleRootOf, repoRootOf, toWslPath, wslMirrorCd } from '../../../scripts/lib/go-build.mjs';
import { goViews } from '../helpers/goRoutes';

const REPO = path.resolve(__dirname, '../../..');
const read = (rel: string) => readFileSync(path.join(REPO, rel), 'utf8');

/** The leading ".." segments of a slash path, and the first name after them. */
function climbOf(p: string): { up: number; next: string } {
  const segs = p.split('/');
  let up = 0;
  while (up < segs.length && segs[up] === '..') up++;
  return { up, next: segs.slice(up).find((s) => s !== '') ?? '' };
}

/** The contents of a whole Go string literal (interpreted or raw), or null. */
function literalOf(code: string): string | null {
  const m = /^"([^"\\]*)"$/.exec(code) ?? /^`([^`]*)`$/.exec(code);
  return m ? m[1] : null;
}

const lineOf = (src: string, at: number) => src.slice(0, at).split('\n').length;

/**
 * Where the Go file `rel` (a repository path under backend/) reaches a file of
 * the repository outside the backend module: the part of the tree the WSL
 * mirror does not copy.
 *
 * A test's working directory and its own source file both sit in its package
 * directory, so the package's depth under backend/ is as far up as a relative
 * path can climb and stay in the module. Two shapes are caught:
 *   - a filepath.Join / path.Join whose run of ".." arguments climbs further
 *     (whatever is joined after it: the run alone is the repository root);
 *   - a string literal that climbs further and then names an entry the
 *     repository has outside backend/ (`topLevel`: e2e, web, packages, ...).
 * A path-traversal INPUT such as "../../../etc/passwd" climbs too, but names
 * nothing of the repository and is handed to the code under test, not read:
 * it is not a hit. Comments are not read (goViews blanks them).
 */
function outsideModuleReads(rel: string, src: string, topLevel: Set<string>): string[] {
  const depth = path.posix.dirname(rel).split('/').length - 1;
  // `masked` has every literal's contents blanked and its quotes kept, `code`
  // the same text at the same index with the contents in place.
  const { code, masked } = goViews(src);
  const hits: string[] = [];

  for (let i = 0; i < masked.length; i++) {
    const quote = masked[i];
    if (quote !== '"' && quote !== '`') continue;
    const close = masked.indexOf(quote, i + 1);
    if (close < 0) break;
    const text = code.slice(i + 1, close);
    const { up, next } = climbOf(text);
    if (up > depth && topLevel.has(next)) {
      hits.push(`${rel}:${lineOf(src, i)}: "${text}" climbs out of backend/ to ${next}/`);
    }
    i = close;
  }

  for (const call of masked.matchAll(/\b(filepath|path)\.Join\(/g)) {
    const at = call.index ?? 0;
    // The call's arguments, split on the commas at its own level.
    const args: string[] = [];
    let from = at + call[0].length;
    let level = 1;
    for (let j = from; j < masked.length && level > 0; j++) {
      const c = masked[j];
      if ('([{'.includes(c)) level++;
      else if (')]}'.includes(c)) level--;
      if (level === 0 || (level === 1 && c === ',')) {
        args.push(code.slice(from, j).trim());
        from = j + 1;
      }
    }
    let run = 0;
    let most = 0;
    for (const arg of args) {
      const lit = literalOf(arg);
      const { up, next } = lit === null ? { up: 0, next: '?' } : climbOf(lit);
      run = up > 0 ? run + up : 0;
      most = Math.max(most, run);
      if (next !== '') run = 0;
    }
    if (most > depth) {
      hits.push(`${rel}:${lineOf(src, at)}: ${call[1]}.Join climbs ${most} directories from a package ${depth} below backend/`);
    }
  }
  return hits;
}

describe('Go under WSL runs from a mirror on WSL\'s own disk', () => {
  const script = wslMirrorCd(path.join(REPO, 'backend'));

  it('finds the repository and the Go module the directory belongs to', () => {
    expect(path.resolve(repoRootOf(path.join(REPO, 'backend', 'internal')))).toBe(path.resolve(REPO));
    expect(path.resolve(moduleRootOf(path.join(REPO, 'backend', 'internal', 'ops')))).toBe(path.resolve(REPO, 'backend'));
  });

  it('copies the module only, not the whole repository', () => {
    expect(script).toMatch(/'(\/mnt\/[a-z])?\/[^']+\/backend\/' "\$HOME\/wt\/[^"/]+\/backend\/"/);
  });

  // ⚠ Copying the module only holds as long as no Go test reads outside it.
  // TestNoTool_DrawnOnceTheProgramArrives (internal/thumb) read its clip from
  // e2e/fixtures: the private release run passed on an old whole-tree mirror,
  // and the public export's Go gate, on a clean module-only mirror, failed with
  // "no such file" (0.50.0, 2026-10-03, #141).
  it('no Go test reads a file outside the backend module', () => {
    const tracked = execFileSync('git', ['-C', REPO, 'ls-files', '-z'], { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 })
      .split('\0')
      .filter(Boolean);
    const topLevel = new Set(tracked.map((f) => f.split('/')[0]).filter((name) => name !== 'backend'));
    const tests = tracked.filter((f) => f.startsWith('backend/') && f.endsWith('_test.go'));
    // A scan of an empty list would pass whatever the tests read.
    expect(tests.length).toBeGreaterThan(300);
    expect(topLevel).toContain('e2e');
    expect(tests.flatMap((f) => outsideModuleReads(f, read(f), topLevel))).toEqual([]);
  });

  it('tells a fixture outside the module from a path-traversal input', () => {
    const top = new Set(['e2e', 'web', 'packages', 'docs', 'scripts', 'desktop']);
    const thumb = 'backend/internal/thumb/notool_test.go';
    // The line 0.50.0 shipped, and the other ways to write it.
    expect(
      outsideModuleReads(
        thumb,
        'clip, err := os.ReadFile(filepath.Join(filepath.Dir(self), "..", "..", "..", "e2e", "fixtures", "file-types", "sample.mp4"))',
        top,
      ),
    ).toHaveLength(1);
    expect(outsideModuleReads(thumb, 'os.ReadFile("../../../e2e/fixtures/file-types/sample.mp4")', top)).toHaveLength(1);
    expect(outsideModuleReads(thumb, 'repo := filepath.Join(\n\tfilepath.Dir(self),\n\t"..", "../..",\n)', top)).toEqual([
      `${thumb}:1: filepath.Join climbs 3 directories from a package 2 below backend/`,
    ]);
    // internal/storage/drivers/local/local_test.go hands this to the driver to
    // prove it cannot climb out of its root: it reads nothing, and from four
    // levels down it does not even leave backend/.
    expect(outsideModuleReads('backend/internal/storage/drivers/local/local_test.go', 'cases := []string{\n\t"../../../etc/passwd",\n}', top)).toEqual([]);
    // Climbing past backend/ to something the repository does not have is a
    // traversal input too.
    expect(outsideModuleReads(thumb, 'browse("../../../../../../etc/passwd")', top)).toEqual([]);
    // backend/ itself, and anything a comment says, are fine.
    expect(outsideModuleReads('backend/cmd/filex/decrypt_test.go', 'var fx = filepath.Join("..", "..", "internal", "e2edecrypt", "testdata")', top)).toEqual([]);
    expect(outsideModuleReads(thumb, '// it used to read "../../../e2e/fixtures/file-types/sample.mp4"', top)).toEqual([]);
  });

  it('keeps a subdirectory of the module inside the mirror', () => {
    expect(wslMirrorCd(path.join(REPO, 'backend', 'examples'))).toMatch(/cd "\$HOME\/wt\/[^"/]+\/backend\/examples"$/);
  });

  it('copies the tree without node_modules and .git, twice if a file was being saved', () => {
    const syncs = script.match(/rsync -a --delete --exclude node_modules --exclude \.git '(\/mnt\/[a-z])?\/[^']+\/' "\$HOME\/wt\/[^"]+\/"/g) ?? [];
    expect(syncs).toHaveLength(2);
    expect(script).toMatch(/rsync [^|]+\|\| rsync /);
  });

  it('then changes into the same directory inside the mirror', () => {
    expect(script).toMatch(/cd "\$HOME\/wt\/[^"]+\/backend"$/);
  });

  it('never changes into the Windows drive', () => {
    expect(script).not.toMatch(/cd '?\/mnt\//);
  });

  it('reaches a Windows drive through /mnt, whichever machine builds the snippet', () => {
    expect(toWslPath(['G:', 'src', 'app', 'backend'].join(String.fromCharCode(92)))).toBe('/mnt/g/src/app/backend');
    expect(toWslPath('C:/Users/x')).toBe('/mnt/c/Users/x');
  });

  it('is what every WSL Go call in the repository uses', () => {
    for (const rel of ['scripts/lib/go-build.mjs', 'scripts/release/plan.mjs', 'scripts/release/gates/engines.mjs']) {
      const src = read(rel);
      expect(src, rel).not.toMatch(/cd \$\{(shq|q)\(toWslPath\(/);
    }
    expect(read('scripts/release/plan.mjs')).toMatch(/wslMirrorCd\(/);
    expect(read('scripts/release/gates/engines.mjs')).toMatch(/wslMirrorCd\(/);
    expect(read('scripts/lib/go-build.mjs')).toMatch(/wslMirrorCd\(cwd\)/);
  });

  // ⚠ `node e2e/run.mjs local --build` spawned a bare `go build`: on a Windows
  // machine whose only Go is in WSL it failed with "'go' is not recognized"
  // (2026-09-27). Every build of the binary goes through build-backend/goBuild.
  it('the e2e runner builds the binary through build-backend, never a bare go', () => {
    const src = read('e2e/run.mjs');
    expect(src).not.toMatch(/run\(\s*'go'/);
    expect(src).toMatch(/run\('node', \['scripts\/build-backend\.mjs', '--out', out\]/);
  });
});
