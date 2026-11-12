// A file a docs page hands out for downloading is there twice: in docs/,
// where a reader of the repository opens it, and in the site's DOWNLOADS
// (docs-site/.vitepress/config.mts), which copies it into docs.filex.sh at the
// same path, so the page's one relative link works in both places.
//
// ⚠ The link is a raw `<a href="…" download>`, not a markdown link: VitePress
// reads a markdown link to an extension it does not know (`.sxcu`) as a page,
// fails the build on it as dead, and its router would open it as one. A raw
// anchor is checked by nothing else - scripts/check-links.mjs reads markdown
// links only, and the site build never sees it - so a download missing from
// either place would be a dead link only a reader finds. This test is that
// check (2026-11-12, the ShareX template, docs/uploaders/filex.sxcu).
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';

import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '../../..');
const DOCS = path.join(REPO, 'docs');

/** Every markdown page of docs/, handovers aside (the site leaves them out). */
function pages(dir = DOCS): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    const p = path.join(dir, name);
    if (statSync(p).isDirectory()) {
      if (name !== 'handovers') out.push(...pages(p));
    } else if (name.endsWith('.md')) out.push(p);
  }
  return out;
}

/** The site's DOWNLOADS list, read from the config's source. */
function siteDownloads(): string[] {
  const src = readFileSync(path.join(REPO, 'docs-site/.vitepress/config.mts'), 'utf8');
  const m = /const DOWNLOADS = \[([^\]]*)\]/.exec(src);
  expect(m, 'docs-site/.vitepress/config.mts declares const DOWNLOADS = [...]').not.toBeNull();
  return [...m![1].matchAll(/'([^']+)'/g)].map((x) => x[1]);
}

/** The relative download anchors of the pages: [page, href, docs-relative path]. */
function downloadLinks(): Array<[string, string, string]> {
  const out: Array<[string, string, string]> = [];
  for (const page of pages()) {
    const text = readFileSync(page, 'utf8');
    for (const a of text.matchAll(/<a\s[^>]*>/g)) {
      if (!/\sdownload[\s>=]/.test(a[0])) continue;
      const href = /\shref="([^"]+)"/.exec(a[0])?.[1];
      if (!href || /^(https?:|mailto:|#|\/)/.test(href)) continue;
      const file = path.relative(DOCS, path.resolve(path.dirname(page), href.replace(/[?#].*$/, '')));
      out.push([path.relative(REPO, page), href, file.split(path.sep).join('/')]);
    }
  }
  return out;
}

describe('a download a docs page links to', () => {
  const downloads = siteDownloads();
  const links = downloadLinks();

  it('the ShareX template is one of them', () => {
    expect(links.map((l) => l[2])).toContain('uploaders/filex.sxcu');
  });

  it.each(links)('%s → %s is a file in docs/ and in the site', (_page, _href, file) => {
    expect(existsSync(path.join(DOCS, file)), `docs/${file} is not there`).toBe(true);
    expect(downloads, `docs-site DOWNLOADS does not copy ${file} into the site`).toContain(file);
  });

  it('every file the site copies is there to copy', () => {
    for (const file of downloads) expect(existsSync(path.join(DOCS, file)), `docs/${file}`).toBe(true);
  });
});
