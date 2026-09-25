// The Releases page of docs.filex.sh is built from GitHub release bodies, and
// VitePress fails the WHOLE build on one dead link. A link in a release body
// may only stay relative if the docs tree the site is built from has the page.
//
// ⚠⚠ 2026-09-25: v0.44.2's notes linked `docs/LAZY-CATALOGUE.md`, a document
// that release introduced. The hourly refresh on the docs server fetched the
// new body at once but still built against v0.43.2's tree, so every refresh
// failed with "Found dead link ./LAZY-CATALOGUE in file RELEASES.md" — the
// second time in two days docs.filex.sh froze.

import { mkdtempSync, mkdirSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const REPO = 'https://github.com/BRF-Tech/filex/blob/main/';

describe('release body links on the Releases page', async () => {
  const { relativeLinks } = (await import('../../../docs-site/scripts/fetch-releases.mjs')) as {
    relativeLinks: (text: string, docsRoot?: string) => string;
  };
  const docs = mkdtempSync(path.join(tmpdir(), 'filex-docs-'));
  writeFileSync(path.join(docs, 'STORAGE.md'), '# Storage\n');
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

  it('leaves absolute links and anchors alone, and repo files go to the repository', () => {
    expect(relativeLinks('[g](https://example.com/x.md)', docs)).toBe('[g](https://example.com/x.md)');
    expect(relativeLinks('[a](#top)', docs)).toBe('[a](#top)');
    expect(relativeLinks('[b](backend/go.mod)', docs)).toBe(`[b](${REPO}backend/go.mod)`);
  });

  it('checks the real tree by default', () => {
    expect(relativeLinks('[c](docs/CONFIGURATION.md)')).toBe('[c](./CONFIGURATION.md)');
  });
});
