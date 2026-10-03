// The Releases page of docs.filex.sh is built from GitHub release bodies, and
// VitePress fails the WHOLE build on one dead link. A link in a release body
// may only stay relative if the docs tree the site is built from has the page.
//
// ⚠⚠ 2026-09-25: v0.44.2's notes linked `docs/LAZY-CATALOGUE.md`, a document
// that release introduced. The hourly refresh on the docs server fetched the
// new body at once but still built against v0.43.2's tree, so every refresh
// failed with "Found dead link ./LAZY-CATALOGUE in file RELEASES.md" — the
// second time in two days docs.filex.sh froze.

import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const REPO = 'https://github.com/BRF-Tech/filex/blob/main/';

describe('release body links on the Releases page', async () => {
  const { relativeLinks } = (await import('../../../docs-site/scripts/fetch-releases.mjs')) as {
    relativeLinks: (text: string, docsRoot?: string) => string;
  };
  const docs = mkdtempSync(path.join(tmpdir(), 'filex-docs-'));
  writeFileSync(
    path.join(docs, 'STORAGE.md'),
    '# Storage\n\n## S3\n\n```sh\n# Not a heading\n```\n\n## `filex decrypt` - an encrypted folder\n\n## S3\n',
  );
  mkdirSync(path.join(docs, 'screenshots'));

  it('keeps a link to a page this tree publishes on the site', () => {
    expect(relativeLinks('[s](docs/STORAGE.md)', docs)).toBe('[s](./STORAGE.md)');
    expect(relativeLinks('[s](docs/STORAGE.md#s3)', docs)).toBe('[s](./STORAGE.md#s3)');
    expect(relativeLinks('[s](STORAGE.md)', docs)).toBe('[s](./STORAGE.md)');
    expect(relativeLinks('[s](./STORAGE.md#s3)', docs)).toBe('[s](./STORAGE.md#s3)');
    expect(relativeLinks('[d](docs/screenshots)', docs)).toBe('[d](./screenshots)');
  });

  it('sends a page the tree does not have yet to the repository, not to a dead link', () => {
    expect(relativeLinks('[l](docs/LAZY-CATALOGUE.md)', docs)).toBe(`[l](${REPO}docs/LAZY-CATALOGUE.md)`);
    expect(relativeLinks('[l](docs/LAZY-CATALOGUE.md#modes)', docs)).toBe(
      `[l](${REPO}docs/LAZY-CATALOGUE.md#modes)`,
    );
    expect(relativeLinks('[l](./LAZY-CATALOGUE.md)', docs)).toBe(`[l](${REPO}docs/LAZY-CATALOGUE.md)`);
  });

  it('leaves absolute links alone, and repo files go to the repository', () => {
    expect(relativeLinks('[g](https://example.com/x.md)', docs)).toBe('[g](https://example.com/x.md)');
    expect(relativeLinks('[b](backend/go.mod)', docs)).toBe(`[b](${REPO}backend/go.mod)`);
  });

  // ⚠ v0.48.1's notes point at their own sections — "([Removed](#removed))",
  // "([Changed](#changed))" — which exist in CHANGELOG.md and on nothing else.
  // This page holds every release: `#removed` matched no heading at all (the
  // anchor gate's one dead link, 2026-09-28), and `#changed` quietly landed
  // in v0.47.0's section. An in-page anchor from a release body keeps its
  // words and loses the link.
  it('turns a release body\'s own section anchors into plain words', () => {
    const v0481 =
      '> ⚠ **The iframe converter is removed** ([Removed](#removed)): conversion is\n' +
      '> ⚠ **Nothing updates itself any more** ([Changed](#changed)): 0.47 installed';
    const out = relativeLinks(v0481, docs);
    expect(out).toBe(
      '> ⚠ **The iframe converter is removed** (Removed): conversion is\n' +
        '> ⚠ **Nothing updates itself any more** (Changed): 0.47 installed',
    );
    expect(relativeLinks('[a](#top) and [s](docs/STORAGE.md#s3)', docs)).toBe('a and [s](./STORAGE.md#s3)');
  });

  it('the Releases page it wrote carries no in-page anchor from a release body', () => {
    const page = readFileSync(path.join(__dirname, '..', '..', '..', 'docs', 'RELEASES.md'), 'utf8');
    const bare = page.split('\n').filter((l) => /\]\(#[^)]*\)/.test(l));
    expect(bare, 'regenerate it: cd docs-site && npm run releases').toEqual([]);
  });

  it('checks the real tree by default', () => {
    expect(relativeLinks('[c](docs/CONFIGURATION.md)')).toBe('[c](./CONFIGURATION.md)');
  });

  // ⚠ 2026-09-30: the long dash left every heading, so CLI.md's "filex
  // decrypt", em dash, "an encrypted folder offline" became "... - ..." and
  // its id grew a hyphen.
  // v0.49.0's notes on GitHub still link the old id; the page keeps the link
  // and drops the fragment instead of publishing one the anchor gate calls dead.
  it('keeps the page and drops a section the page no longer has', () => {
    expect(relativeLinks('[d](docs/STORAGE.md#filex-decrypt---an-encrypted-folder)', docs)).toBe(
      '[d](./STORAGE.md#filex-decrypt---an-encrypted-folder)',
    );
    expect(relativeLinks('[d](docs/STORAGE.md#filex-decrypt--an-encrypted-folder)', docs)).toBe('[d](./STORAGE.md)');
    expect(relativeLinks('[s](./STORAGE.md#s3-1)', docs)).toBe('[s](./STORAGE.md#s3-1)');
    expect(relativeLinks('[n](./STORAGE.md#not-a-heading)', docs)).toBe('[n](./STORAGE.md)');
  });
});

// Nothing a person reads carries an em or an en dash (owner's ruling,
// 2026-09-30), but a published GitHub release body is history and keeps its
// own: the page renders them as "-", every time, so no refresh brings one back.
describe('release bodies on the Releases page', async () => {
  const { plainDashes } = (await import('../../../docs-site/scripts/fetch-releases.mjs')) as {
    plainDashes: (markdown: string) => string;
  };
  const em = String.fromCharCode(0x2014);
  const en = String.fromCharCode(0x2013);

  it('says "-" where a body says a long dash, ranges and code included', () => {
    expect(plainDashes(`one ${em} two, 3${en}60, \`live: polling ${em} ...\``)).toBe('one - two, 3-60, `live: polling - ...`');
  });

  it('never turns a wrapped sentence into a list item', () => {
    expect(plainDashes(`the page\n${em} and the link\n> quoted\n> ${em} still quoted`)).toBe(
      'the page -\nand the link\n> quoted -\n> still quoted',
    );
    expect(plainDashes(`\n${em} alone`)).toBe('\n- alone');
  });

  it('the Releases page it wrote carries none', () => {
    const page = readFileSync(path.join(__dirname, '..', '..', '..', 'docs', 'RELEASES.md'), 'utf8');
    expect(page.split('\n').filter((l) => l.includes(em) || l.includes(en))).toEqual([]);
  });
});
