// Go under WSL must not read the tree over /mnt/<drive>.
//
// ⚠ Why: /mnt/g is a 9P bridge. Go reads and hashes every package source on
// each build, and every one of those reads goes through dllhost.exe
// (Plan9FileSystem) on the Windows side: 0.5-0.9 of a core per test run, 8.4
// hours of CPU over two days, and a workstation stuck at 60-80% while agents
// ran `go test` on /mnt/g (2026-09-26, lessons #577 and #578). The fix is to
// mirror the repository onto WSL's own disk first and run Go there; every Go
// call this repo makes through WSL goes through one helper that does it.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { moduleRootOf, repoRootOf, toWslPath, wslMirrorCd } from '../../../scripts/lib/go-build.mjs';

const REPO = path.resolve(__dirname, '../../..');
const read = (rel: string) => readFileSync(path.join(REPO, rel), 'utf8');

describe('Go under WSL runs from a mirror on WSL\'s own disk', () => {
  const script = wslMirrorCd(path.join(REPO, 'backend'));

  it('finds the repository and the Go module the directory belongs to', () => {
    expect(path.resolve(repoRootOf(path.join(REPO, 'backend', 'internal')))).toBe(path.resolve(REPO));
    expect(path.resolve(moduleRootOf(path.join(REPO, 'backend', 'internal', 'ops')))).toBe(path.resolve(REPO, 'backend'));
  });

  it('copies the module only, not the whole repository', () => {
    expect(script).toMatch(/'(\/mnt\/[a-z])?\/[^']+\/backend\/' "\$HOME\/wt\/[^"/]+\/backend\/"/);
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
    expect(toWslPath(['G:', 'filex', 'backend'].join(String.fromCharCode(92)))).toBe('/mnt/g/filex/backend');
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
});
