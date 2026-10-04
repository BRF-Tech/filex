// The README's translations (README.<lang>.md at the root) are a second copy
// of every link the README makes, so the link checks read them, and every page
// of the six links the other five.
//
// ⚠ Why: PR #84 (2026-10-04) added five translations and taught
// scripts/check-links.mjs and scripts/check-doc-anchors.mjs to read
// `README.*.md`. Without that, a docs page renamed or withheld by the export
// leaves a dead link in each translation that no check sees (on 2026-09-05 the
// public README and docs/README.md each linked a page the export withholds,
// which is why check-links exists). The anchor check needs a built docs site
// and is run by the release
// (`node scripts/check-doc-anchors.mjs --build`); this file holds what can be
// proved without one.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';

const ROOT = path.resolve(__dirname, '../../..');
const CHECK_LINKS = path.join(ROOT, 'scripts/check-links.mjs');

const roots: string[] = [];
afterAll(() => {
  for (const r of roots) fs.rmSync(r, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
});

/** A throwaway repository root holding exactly these files. */
function tree(files: Record<string, string>): string {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-readme-translations-'));
  roots.push(root);
  for (const [f, text] of Object.entries(files)) {
    fs.mkdirSync(path.dirname(path.join(root, f)), { recursive: true });
    fs.writeFileSync(path.join(root, f), text);
  }
  return root;
}

function checkLinks(root: string) {
  return spawnSync(process.execPath, [CHECK_LINKS, root], { encoding: 'utf8' });
}

describe('check-links reads every README translation', () => {
  it('names a dead link that only a translation has', () => {
    const root = tree({
      'README.md': '[Install](docs/INSTALL.md)\n',
      'README.tr.md': '[Kurulum](docs/INSTALL.md) · [Eski sayfa](docs/GONE.md)\n',
      'docs/INSTALL.md': '# Install\n',
    });
    const r = checkLinks(root);
    expect(r.status, r.stdout + r.stderr).toBe(1);
    expect(r.stdout).toContain('README.tr.md:1 -> docs/GONE.md');
  });

  it('counts the links of a translation whose language tag has a region (zh-CN)', () => {
    const root = tree({
      'README.md': '[Install](docs/INSTALL.md)\n',
      'README.tr.md': '[Kurulum](docs/INSTALL.md)\n',
      'README.zh-CN.md': '[安装](docs/INSTALL.md)\n',
      'docs/INSTALL.md': '# Install\n',
    });
    const r = checkLinks(root);
    expect(r.status, r.stdout + r.stderr).toBe(0);
    expect(r.stdout).toContain('check-links: 3 relative links');
  });
});

describe('every README page links every other one', () => {
  const pages = fs
    .readdirSync(ROOT)
    .filter((f) => /^README(\.[\w-]+)?\.md$/.test(f))
    .sort();

  it('the README has translations', () => {
    expect(pages).toContain('README.md');
    expect(pages.length).toBeGreaterThan(1);
  });

  for (const page of pages) {
    it(`${page}: the language line names every page, itself in bold and unlinked`, () => {
      const text = fs.readFileSync(path.join(ROOT, page), 'utf8');
      const line = text.split('\n').find((l) => / · /.test(l) && /\]\(README[.\w-]*\.md\)/.test(l));
      expect(line, `${page} has no language line`).toBeTruthy();
      for (const other of pages) {
        if (other === page) expect(line).not.toContain(`](${other})`);
        else expect(line, `${page} does not link ${other}`).toContain(`](${other})`);
      }
      expect(line).toMatch(/^(\S.*· )?\*\*[^*]+\*\*( ·|$)/);
    });
  }

  for (const page of pages.filter((p) => p !== 'README.md')) {
    it(`${page} names the README it was translated from`, () => {
      const first = fs.readFileSync(path.join(ROOT, page), 'utf8').split('\n')[0];
      expect(first).toMatch(/^<!-- Translated from README\.md as of \S+/);
    });
  }
});
