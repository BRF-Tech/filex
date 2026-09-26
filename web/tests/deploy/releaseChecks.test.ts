// The text-only rules of `pnpm release` (scripts/release/checks.mjs), one by
// one. The release command itself is exercised end to end in
// releaseCli.test.ts; this file pins each judgement it makes, next to the
// incident that made it necessary.

import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import {
  capabilitiesBuild,
  classifyDeletions,
  closingKeywords,
  compareVersions,
  dateChangelog,
  docsPageUrl,
  globMatch,
  hasContent,
  headingsAdded,
  htmlText,
  manifestNewest,
  newestTag,
  parseFeed,
  privateHostLines,
  readmeImages,
  setPackageVersion,
  unreleasedBody,
  versionProblems,
  vitestVerdict,
  workspacePackages,
} from '../../../scripts/release/checks.mjs';
import { releaseNotes } from '../../../scripts/release-notes.mjs';

const REPO = path.resolve(__dirname, '..', '..', '..');

describe('release numbers', () => {
  it('accepts the next number and refuses one that is not newer', () => {
    expect(versionProblems('0.45.0', 'v0.44.2')).toEqual([]);
    expect(versionProblems('1.1.0', 'v0.44.2')).toEqual([]);
    expect(versionProblems('0.44.2', 'v0.44.2')[0]).toContain('not newer');
    expect(versionProblems('0.44.1', 'v0.44.2')[0]).toContain('not newer');
  });

  it('has no 1.0.x and no 0.x patch past 99 (the Store packs them, lesson #468)', () => {
    expect(versionProblems('1.0.0', 'v0.44.2').join()).toContain('there is no 1.0.x');
    expect(versionProblems('1.0.7', null).join()).toContain('there is no 1.0.x');
    expect(versionProblems('0.44.100', 'v0.44.2').join()).toContain('cannot pass 99');
  });

  it('wants X.Y.Z, nothing else', () => {
    for (const bad of ['v0.45.0', '0.45', '0.45.0-rc1', '00.45.0', 'next']) {
      expect(versionProblems(bad, null), bad).toHaveLength(1);
    }
  });

  it('finds the newest release tag by number, not by spelling', () => {
    expect(newestTag(['v0.9.0', 'v0.10.0', 'v0.2.1'])).toBe('v0.10.0');
    expect(newestTag(['backend/v0.44.2', 'v0.44.1', 'v0.45.0-rc1', 'latest'])).toBe('v0.44.1');
    expect(newestTag([])).toBeNull();
    expect(compareVersions('v0.44.2', '0.44.10')).toBeLessThan(0);
  });
});

describe('CHANGELOG.md', () => {
  const log = '# Changelog\n\n## [Unreleased]\n\n### Added\n\n- **A thing.** It does it.\n\n## [0.44.2] - 2026-09-25\n\n- **Old.** Yes.\n';

  it('dates [Unreleased] and leaves an empty one above it (lesson #392)', () => {
    const out = dateChangelog(log, '0.45.0', '2026-09-26');
    expect(out).toContain('## [Unreleased]\n\n## [0.45.0] - 2026-09-26\n\n### Added\n\n- **A thing.**');
    expect(hasContent(unreleasedBody(out))).toBe(false);
    // The notes the stores and the release page are built from can read it.
    expect(releaseNotes(out, '0.45.0')).toContain('filex v0.45.0 — 2026-09-26');
    expect(releaseNotes(out, '0.45.0')).toContain('• A thing.');
  });

  it('refuses an empty [Unreleased], a missing one, and a second [X.Y.Z]', () => {
    expect(() => dateChangelog('## [Unreleased]\n\n<!-- nothing yet -->\n\n## [0.1.0] - x\n', '0.2.0', '2026-01-01')).toThrow(/empty/);
    expect(() => dateChangelog('# Changelog\n\n## [0.1.0]\n', '0.2.0', '2026-01-01')).toThrow(/no "## \[Unreleased\]"/);
    expect(() => dateChangelog(log, '0.44.2', '2026-01-01')).toThrow(/already has/);
    expect(() => dateChangelog(log, '0.45.0', '26.09.2026')).toThrow(/not a date/);
  });
});

describe('package versions', () => {
  it('rewrites the top-level "version" and nothing else', () => {
    const text = '{\n  "name": "x",\n  "version": "0.44.2",\n  "nested": {\n    "version": "9.9.9"\n  }\n}\n';
    expect(setPackageVersion(text, '0.45.0')).toBe(text.replace('"version": "0.44.2"', '"version": "0.45.0"'));
    expect(() => setPackageVersion('{\n  "name": "x"\n}\n', '0.45.0')).toThrow(/found 0/);
  });

  it("reads this repository's workspace: the seven packages every release bumps", () => {
    const dirs = ['web', 'desktop', 'e2e', 'docs-site', 'packages/core', 'packages/react', 'packages/webcomponent', 'backend/x'];
    const yaml = fs.readFileSync(path.join(REPO, 'pnpm-workspace.yaml'), 'utf8');
    expect(workspacePackages(yaml, dirs)).toEqual(['desktop', 'docs-site', 'e2e', 'packages/core', 'packages/react', 'packages/webcomponent', 'web']);
    for (const d of workspacePackages(yaml, dirs)) expect(fs.existsSync(path.join(REPO, d, 'package.json')), d).toBe(true);
  });

  it('refuses a workspace pattern it cannot expand, rather than bump part of it', () => {
    expect(() => workspacePackages('packages:\n  - "apps/**"\n', [])).toThrow(/not supported/);
    expect(() => workspacePackages('packages:\n  - "gone"\n', ['web'])).toThrow(/no package.json/);
  });
});

describe('the public export', () => {
  it('sorts deletions into followed, withheld and unexplained', () => {
    const k = classifyDeletions(['a.md', 'private.md', 'mystery.md'], { sourceDeleted: ['a.md'], sourceFiles: ['private.md', 'kept.md'] });
    expect(k).toEqual({ deletedInSource: ['a.md'], withheld: ['private.md'], unexplained: ['mystery.md'] });
  });

  it('finds a private host but lets the contact addresses through', () => {
    // Hosts built from parts: this file is itself exported (lesson #95).
    const host = ['brf', 'sh'].join('.');
    const module = ['gitlab', 'com/brftech/filemanager'].join('.');
    const lines = [`SECURITY.md:3:mail security@${host}`, `docs/X.md:9:see https://fm.${host}/admin`, `go.mod:1:module ${module}`];
    const hits = privateHostLines(lines, { allow: [`security@${host}`, `hello@${host}`], forbid: [/brf\.sh/, /gitlab\.com[/:]brftech/] });
    expect(hits).toEqual(lines.slice(1));
  });

  it('spots a GitHub closing keyword in a commit message (lesson #163)', () => {
    expect(closingKeywords('v0.41.2 - Fixes #26, #27')).toEqual(['Fixes #26']);
    expect(closingKeywords('closes BRF-Tech/filex#3 and resolved: #9')).toHaveLength(2);
    expect(closingKeywords('v0.45.0 - teams (#12), issue 12, see #7, prefix #3')).toEqual([]);
  });
});

describe('a vitest report', () => {
  const report = (tests: Array<[string, string]>, extra = {}) => ({
    numFailedTestSuites: 0,
    testResults: [{ name: 'x.test.ts', status: 'passed', assertionResults: tests.map(([title, status]) => ({ title, fullName: title, status })) }],
    ...extra,
  });

  it('passes only when the named guards actually ran', () => {
    expect(vitestVerdict(report([['guard', 'passed'], ['other', 'passed']]), { mustPass: ['guard'] }).ok).toBe(true);
    const skipped = vitestVerdict(report([['guard', 'skipped'], ['other', 'passed']]), { mustPass: ['guard'] });
    expect(skipped.ok).toBe(false);
    expect(skipped.problems.join()).toContain('a skipped guard proves nothing');
    expect(vitestVerdict(report([['other', 'passed']]), { mustPass: ['guard'] }).problems.join()).toContain('no test named');
  });

  it('fails on a failed test, a file that died before its tests, and an empty report', () => {
    expect(vitestVerdict(report([['a', 'failed']])).ok).toBe(false);
    const died = { numFailedTestSuites: 1, testResults: [{ name: 'y.test.ts', status: 'failed', message: 'SyntaxError', assertionResults: [] }] };
    expect(vitestVerdict(died).problems.join()).toContain('before its tests ran');
    expect(vitestVerdict({ testResults: [] }).ok).toBe(false);
    expect(vitestVerdict(null).ok).toBe(false);
  });
});

describe('what was published', () => {
  it('takes the headings a release ADDED to documentation pages', () => {
    const diff = [
      'diff --git a/docs/STORAGE.md b/docs/STORAGE.md',
      '--- a/docs/STORAGE.md',
      '+++ b/docs/STORAGE.md',
      '@@ -10,0 +11,3 @@',
      '+## Drift detection: what a replaced file looks like',
      '+### `mount` with <b>html</b>',
      '-## A removed heading',
      '+Some prose, not a heading.',
      '+++ b/docs/index.md',
      '+### The [explorer](./EXPLORER.md) **everywhere**',
    ].join('\n');
    expect(headingsAdded(diff)).toEqual([
      { file: 'docs/STORAGE.md', text: 'Drift detection: what a replaced file looks like' },
      { file: 'docs/index.md', text: 'The explorer everywhere' },
    ]);
  });

  it('leaves a badge out of the heading, because the page renders it as a picture, not text', () => {
    const diff = [
      '+++ b/docs/BACKEND.md',
      '+### `PUT /api/admin/storages/order` ![admin](https://img.shields.io/badge/-admin-red)',
    ].join('\n');
    const [probe] = headingsAdded(diff);
    const page = htmlText(
      '<h3 id="put-apiadminstoragesorder-"><code>PUT /api/admin/storages/order</code> <img src="https://img.shields.io/badge/-admin-red" alt="admin"> <a class="header-anchor" href="#put-apiadminstoragesorder-">​</a></h3>',
    );
    expect(probe.text).toBe('PUT /api/admin/storages/order');
    expect(page.includes(probe.text)).toBe(true);
  });

  it('reads rendered pages as text and maps docs files to site pages', () => {
    expect(htmlText('<h2 id="a">Backup &amp; restore&#39;s <code>data</code><a class="header-anchor">#</a></h2>')).toBe("Backup & restore's data #");
    expect(docsPageUrl('https://docs.filex.sh/', 'docs/STORAGE.md')).toBe('https://docs.filex.sh/STORAGE');
    expect(docsPageUrl('https://docs.filex.sh', 'docs/index.md')).toBe('https://docs.filex.sh/');
  });

  it('reads an electron-builder feed, and refuses one without hashes', () => {
    const feed = 'version: 0.44.2\nfiles:\n  - url: filex-desktop-x64.exe\n    sha512: abc==\n    size: 131843255\n  - url: other.zip\n    sha512: def==\n    size: 3\n    blockMapSize: 1\npath: filex-desktop-x64.exe\nsha512: abc==\n';
    expect(parseFeed(feed)).toEqual({
      version: '0.44.2',
      files: [{ url: 'filex-desktop-x64.exe', sha512: 'abc==', size: 131843255 }, { url: 'other.zip', sha512: 'def==', size: 3 }],
    });
    expect(() => parseFeed('version: 0.44.2\nfiles:\n  - url: a.exe\n    size: 1\n')).toThrow(/without a sha512/);
    expect(() => parseFeed('files:\n  - url: a.exe\n    sha512: x\n')).toThrow(/no version/);
  });

  it('finds the newest release in the update manifest, and the build a server runs', () => {
    expect(manifestNewest({ releases: [{ version: 'v0.44.2' }, { version: 'v0.45.0' }, { version: 'junk' }] })).toBe('v0.45.0');
    expect(capabilitiesBuild('v0.44.2 (503fc90b4b9ab45bee6c208a1af7e1f548843fbe, 2026-09-25T08:09:57Z)')).toEqual({
      tag: 'v0.44.2',
      commit: '503fc90b4b9ab45bee6c208a1af7e1f548843fbe',
      date: '2026-09-25T08:09:57Z',
    });
    expect(capabilitiesBuild('0.1.0-dev')).toBeNull();
  });
});

describe('README and globs', () => {
  it('lists the pictures a README shows, not the ones it links', () => {
    const md = '![a](docs/screenshots/v1/a.png)\n<img src="./docs/b.png" width="10">\n![c](https://example.invalid/c.png)\n[not a picture](docs/x.md)\n';
    expect(readmeImages(md)).toEqual(['docs/screenshots/v1/a.png', 'docs/b.png']);
  });

  it('matches * inside a directory and ** across them', () => {
    expect(globMatch('docs/*.md', 'docs/STORAGE.md')).toBe(true);
    expect(globMatch('docs/*.md', 'docs/handovers/x.md')).toBe(false);
    expect(globMatch('deploy/**/README.md', 'deploy/README.md')).toBe(true);
    expect(globMatch('deploy/**/README.md', 'deploy/helm/filex/README.md')).toBe(true);
    expect(globMatch('web/src/locales/*.json', 'web/src/locales/en.json')).toBe(true);
    expect(globMatch('a.b', 'aXb')).toBe(false);
  });
});
