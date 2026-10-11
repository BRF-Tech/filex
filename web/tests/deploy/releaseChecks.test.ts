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
  dockerfileSources,
  dockerignoreExcludes,
  dockerignoreLeavesOut,
  dockerignoreRules,
  docsPageUrl,
  dryRunTitle,
  globMatch,
  hasContent,
  headingsOf,
  htmlText,
  infraFailure,
  manifestNewest,
  newestTag,
  newHeadings,
  parseFeed,
  partsLine,
  partsVerdict,
  privateHostLines,
  promotionVerdict,
  readmeImages,
  rerunDecision,
  runVerdict,
  setPackageVersion,
  transientGithubError,
  unreleasedBody,
  versionProblems,
  vitestVerdict,
  workspacePackages,
} from '../../../scripts/release/checks.mjs';
import { releaseNotes } from '../../../scripts/release-notes.mjs';
import { matrixState } from '../../../scripts/release/stages.mjs';

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
    expect(releaseNotes(out, '0.45.0')).toContain('filex v0.45.0 - 2026-09-26');
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

  it('finds a private host but lets the contact addresses and the Helm repository through', () => {
    // Hosts built from parts: this file is itself exported (lesson #95).
    const host = ['brf', 'sh'].join('.');
    const module = ['gitlab', 'com/brftech/filemanager'].join('.');
    const lines = [
      `SECURITY.md:3:mail security@${host}`,
      `docs/INSTALLATION.md:5:helm repo add brf-tech https://helm.${host}`,
      `docs/X.md:9:see https://fm.${host}/admin`,
      `go.mod:1:module ${module}`,
    ];
    const hits = privateHostLines(lines, {
      allow: [`security@${host}`, `hello@${host}`, `helm.${host}`],
      forbid: [/brf\.sh/, /gitlab\.com[/:]brftech/],
    });
    expect(hits).toEqual(lines.slice(2));
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
  it('takes the headings a release ADDED to a documentation page', () => {
    const before = ['# Storage', '', '## A removed heading', '', 'Prose.', ''].join('\n');
    const after = [
      '# Storage',
      '',
      '## Drift detection: what a replaced file looks like',
      '### `mount` with <b>html</b>',
      'Some prose, not a heading.',
      '### The [explorer](./EXPLORER.md) **everywhere**',
      '##### Too deep to probe',
      '#hashtag, not a heading',
      '',
    ].join('\n');
    expect(newHeadings(before, after)).toEqual(['Drift detection: what a replaced file looks like', 'The explorer everywhere']);
  });

  it('does not take a # line inside a code block for a heading (v0.50.0, lesson #964)', () => {
    // v0.50.0 added this comment to a yaml block in docs/STORAGE.md; the gate
    // read it from the diff as a heading and called a current site an OLD
    // snapshot, because the page prints the comment with its backticks.
    const block = (comment: string[]) =>
      ['# Storage', '', '## Mount at install time', '', '```yaml', 'storage:', ...comment, '  type: sftp', '```', '', 'Prose.', ''].join('\n');
    const before = block([]);
    const after = block(['# an existing SFTP / NAS - any driver via `config`']);
    expect(headingsOf(after)).toEqual(['Storage', 'Mount at install time']);
    expect(newHeadings(before, after)).toEqual([]);
    // the same line outside the block is a heading, and a new one
    expect(newHeadings(before, `${before}\n# an existing SFTP / NAS - any driver via \`config\`\n`)).toEqual([
      'an existing SFTP / NAS - any driver via config',
    ]);
  });

  it('has one reader of headings: the heading ids of the Releases page use the same fence rules', async () => {
    const { headingLines } = (await import('../../../docs-site/scripts/markdown-headings.mjs')) as {
      headingLines: (md: string) => { level: number; text: string }[];
    };
    const { headingIds } = (await import('../../../docs-site/scripts/fetch-releases.mjs')) as {
      headingIds: (file: string) => Set<string>;
    };
    const { mkdtempSync, writeFileSync } = await import('node:fs');
    const { tmpdir } = await import('node:os');
    const path = await import('node:path');
    const dir = mkdtempSync(path.join(tmpdir(), 'filex-headings-'));
    // a shorter run, and a run with text after it, do not close a four-backtick fence
    const loose = ['## Real', '````md', '```', '# Fake one', '``` text', '# Fake two', '````', '## After', ''].join('\n');
    const cases: Record<string, string> = {
      loose,
      tilde: ['~~~', '# Fake', '~~~', '## After', ''].join('\n'),
      indented: ['   ```sh', '# Fake', '   ```', '## After', ''].join('\n'),
      mixed: ['~~~', '```', '# Fake', '~~~', '## After', ''].join('\n'),
      unclosed: ['## Before', '```yaml', '# Fake', ''].join('\n'),
      front: ['---', '# Fake', 'layout: home', '---', '## After', ''].join('\n'),
      crlf: ['```yaml', '# Fake', '```', '## After', ''].join('\r\n'),
    };
    for (const [name, md] of Object.entries(cases)) {
      const file = path.join(dir, `${name}.md`);
      writeFileSync(file, md);
      const slugs = headingLines(md).map((h) => h.text.toLowerCase());
      expect([...headingIds(file)], name).toEqual(slugs);
      expect([...headingIds(file)].some((id) => id.startsWith('fake')), name).toBe(false);
    }
    expect([...headingIds(path.join(dir, 'loose.md'))]).toEqual(['real', 'after']);
    // the gate drops the levels and shapes it cannot compare; ids keep every level
    expect(headingLines('##### Deep heading\n###### Deeper')).toEqual([
      { level: 5, text: 'Deep heading' },
      { level: 6, text: 'Deeper' },
    ]);
    expect(headingsOf('##### Deep heading')).toEqual([]);
  });

  it('knows ~~~ fences, indented fences, longer fences and a fence never closed', () => {
    const page = (...lines: string[]) => lines.join('\n');
    expect(headingsOf(page('~~~', '# not a heading', '~~~', '## After tildes'))).toEqual(['After tildes']);
    expect(headingsOf(page('   ```bash', '# not a heading', '   ```', '## After an indented fence'))).toEqual(['After an indented fence']);
    // four spaces is an indented code block, not a fence: nothing is opened
    expect(headingsOf(page('    ```', '## Still a heading'))).toEqual(['Still a heading']);
    // a fence closes only on the same character, at least as long
    expect(headingsOf(page('````md', '```yaml', '# not a heading', '```', '# nor this', '````', '## After four'))).toEqual(['After four']);
    expect(headingsOf(page('~~~', '```', '# not a heading', '~~~', '## After mixed'))).toEqual(['After mixed']);
    expect(headingsOf(page('```', '# not a heading', '``` not a closing fence', '# nor this', '```', '## After text'))).toEqual(['After text']);
    // a fence never closed runs to the end of the page
    expect(headingsOf(page('## Before the fence', '```yaml', '# not a heading', '## not one either'))).toEqual(['Before the fence']);
    // inline code at the start of a line opens nothing
    expect(headingsOf(page('```x``` is inline code', '## After inline code'))).toEqual(['After inline code']);
    // YAML front matter is not the page
    expect(headingsOf(page('---', '# a front matter comment', 'layout: home', '---', '', '# The Home Page'))).toEqual(['The Home Page']);
    expect(headingsOf(page('```yaml\r', '# not a heading\r', '```\r', '## After CRLF\r'))).toEqual(['After CRLF']);
  });

  it('counts a heading that only moved as old, and a second one with the same text as new', () => {
    const before = '## Alpha section\n\ntext\n\n## Beta section\n';
    expect(newHeadings(before, '## Beta section\n\n## Alpha section\n\ntext\n')).toEqual([]);
    expect(newHeadings(before, `${before}\n## Alpha section\n`)).toEqual(['Alpha section']);
    expect(newHeadings('', '## A new page\n')).toEqual(['A new page']);
    expect(newHeadings(before, '')).toEqual([]);
  });

  it('leaves a badge out of the heading, because the page renders it as a picture, not text', () => {
    const [probe] = headingsOf('### `PUT /api/admin/storages/order` ![admin](https://img.shields.io/badge/-admin-red)\n');
    const page = htmlText(
      '<h3 id="put-apiadminstoragesorder-"><code>PUT /api/admin/storages/order</code> <img src="https://img.shields.io/badge/-admin-red" alt="admin"> <a class="header-anchor" href="#put-apiadminstoragesorder-">​</a></h3>',
    );
    expect(probe).toBe('PUT /api/admin/storages/order');
    expect(page.includes(probe)).toBe(true);
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

// #76: the tag is made only after GitHub tested its commit. The gate stage
// waits for two runs on the export commit; these are its two judgements.
describe('what GitHub ran on a commit', () => {
  it("names the dry run as release.yml's run-name does, and its verify job looks for", () => {
    // run-name: format('{0} {1} {2}{3}', 'dry run', inputs.only, '', github.sha)
    // for a run by hand with publish off, only=all and no tag
    // (releaseGatesImages.test.ts pins the workflow's side of it)
    expect(dryRunTitle('0123abcd')).toBe('dry run all 0123abcd');
  });

  it('a success anywhere wins, anything still going waits, and only then a run that ended otherwise is the answer', () => {
    const run = (status: string, conclusion: string, id = 1) => ({ id, status, conclusion });
    expect(runVerdict([])).toEqual({ state: 'none', run: null });
    expect(runVerdict(undefined)).toEqual({ state: 'none', run: null });
    // a re-run that passed is a pass, whatever else ran on the commit
    expect(runVerdict([run('completed', 'failure', 1), run('completed', 'success', 2)])).toMatchObject({ state: 'success', run: { id: 2 } });
    expect(runVerdict([run('in_progress', '', 1), run('completed', 'success', 2)]).state).toBe('success');
    // a failed run that is running again is not an answer yet
    expect(runVerdict([run('completed', 'failure', 1), run('queued', '', 2)])).toMatchObject({ state: 'running', run: { id: 2 } });
    // cancelled, timed out, skipped: none of them is a pass
    for (const c of ['failure', 'cancelled', 'timed_out', 'skipped', 'neutral']) {
      expect(runVerdict([run('completed', c)]).state, c).toBe('failure');
    }
  });
});

// #173/#174: ci.yml is a matrix, a job per part. The gate stage reads the
// run part by part: its one conclusion said neither which part failed nor
// that a part never ran, and a matrix that lost a part still ended green.
describe('the parts of a GitHub run', () => {
  const job = (name: string, status: string, conclusion: string | null = null) => ({ name, status, conclusion, url: `https://x/${name}` });
  const EXPECTED = ['Plan', 'Go (a)', 'Go (b)', 'All tests (full)'];

  it('green only when every expected part ran and passed', () => {
    const v = partsVerdict(EXPECTED.map((n) => job(n, 'completed', 'success')), EXPECTED, { finished: true });
    expect(v.state).toBe('success');
    expect(partsLine(v)).toBe('4/4 parts green');
    // a job the plan does not expect changes nothing
    expect(partsVerdict([...EXPECTED.map((n) => job(n, 'completed', 'success')), job('Extra', 'completed', 'failure')], EXPECTED, { finished: true }).state).toBe('success');
  });

  it('a red part is red at once, even while the rest still run, and says which', () => {
    const v = partsVerdict([job('Plan', 'completed', 'success'), job('Go (a)', 'in_progress'), job('Go (b)', 'completed', 'failure')], EXPECTED);
    expect(v.state).toBe('failure');
    expect(v.failed.map((p) => p.name)).toEqual(['Go (b)']);
    expect(v.failed[0]).toMatchObject({ conclusion: 'failure', url: 'https://x/Go (b)' });
    expect(partsLine(v)).toBe('1/4 parts green, 2 running, 1 red');
    // cancelled, timed out, skipped: none of them is a pass
    for (const c of ['cancelled', 'timed_out', 'skipped', 'neutral']) {
      expect(partsVerdict([job('Go (a)', 'completed', c)], ['Go (a)'], { finished: true }).state, c).toBe('failure');
    }
  });

  it('a part with no job waits while the run goes on (GitHub expands a matrix late), and is missing once it ended', () => {
    const jobs = [job('Plan', 'completed', 'success'), job('Go (a)', 'completed', 'success'), job('Go (b)', 'completed', 'success')];
    const going = partsVerdict(jobs, EXPECTED, { finished: false });
    expect(going.state).toBe('running');
    expect(going.running.map((p) => p.name)).toEqual(['All tests (full)']);
    const ended = partsVerdict(jobs, EXPECTED, { finished: true });
    expect(ended.state).toBe('incomplete');
    expect(ended.missing.map((p) => p.name)).toEqual(['All tests (full)']);
    expect(partsLine(ended)).toBe('3/4 parts green, 1 missing');
  });

  it("the run's state with its parts: a red part fails a running run, a green run without a part is incomplete", () => {
    const green = partsVerdict(EXPECTED.map((n) => job(n, 'completed', 'success')), EXPECTED, { finished: true });
    const red = partsVerdict([job('Go (b)', 'completed', 'failure')], EXPECTED, { finished: false });
    const short = partsVerdict([job('Plan', 'completed', 'success')], EXPECTED, { finished: true });
    expect(matrixState('success', green)).toBe('success');
    expect(matrixState('running', red)).toBe('failure');
    expect(matrixState('success', short)).toBe('incomplete');
    expect(matrixState('running', partsVerdict([], EXPECTED))).toBe('running');
    // a run that failed in a job no part names is still red
    expect(matrixState('failure', green)).toBe('failure');
    // no parts named (an older plan): the run's own state, as before #174
    expect(matrixState('success', null)).toBe('success');
    expect(matrixState('none', null)).toBe('none');
  });

  it('a dry run is a release candidate only with every required artifact; an optional one may be missing and is named', () => {
    const required = ['digests-amd64', 'digests-arm64', 'release-files-linux'];
    const optional = ['release-files-macos'];
    expect(promotionVerdict([...required, ...optional], { required, optional })).toEqual({ ok: true, lacking: [], without: [] });
    expect(promotionVerdict(required, { required, optional })).toEqual({ ok: true, lacking: [], without: ['release-files-macos'] });
    expect(promotionVerdict(['digests-amd64', 'release-files-macos'], { required, optional })).toEqual({ ok: false, lacking: ['digests-arm64', 'release-files-linux'], without: [] });
    expect(promotionVerdict(undefined, { required })).toMatchObject({ ok: false, lacking: required });
  });
});

// #165: the pretag's images are built from `git archive` of the release
// commit, streamed to `docker build -`, never from the working tree: BuildKit
// walked the checkout through WSL's 9P bridge for 30-52 minutes per image at
// 0.54.0 and 0.55.0. A tar on stdin gets no .dockerignore (measured), so the
// image gate checks that .dockerignore leaves out no tracked file the
// Dockerfile copies - with a reader that must read .dockerignore as docker does.
describe('.dockerignore, read as docker reads it', () => {
  const REPO = path.resolve(__dirname, '..', '..', '..');
  const real = fs.readFileSync(path.join(REPO, '.dockerignore'), 'utf8');

  // Measured 2026-10-10 with docker 29.7 (BuildKit): this repository's
  // .dockerignore, a context holding exactly these files, `COPY . /x` and
  // `find /x -type f`. The reader must agree with every line.
  it("leaves out exactly what docker left out of a real build's context", () => {
    const { rules, unsupported } = dockerignoreRules(real);
    expect(unsupported).toEqual([]);
    const keptByDocker = ['a/x.log', 'backend/embed/admin/.keep', 'backend/embed/web/.keep', 'backend/filex', 'Dockerfile', 'docker/Dockerfile', 'docker/entrypoint.sh', 'entrypoint.sh', 'entrypoint.sh.bak', 'keep/ok.txt'];
    const leftOutByDocker = [
      'node_modules/x', 'web/node_modules/a/b', 'web/dist/a.js', 'packages/core/dist/i.js', 'backend/embed/admin/index.html', 'backend/embed/web/x.js',
      'filex', 'filex-test', 'x.log', 'e2e/test-results/a', 'e2e/playwright-report/i.html', 'e2e/.playwright-mcp/z', '.idea/w', '.vscode/s',
      'sweep-2026-05-09/bugs.md', 'web/.vite/c', 'web/.turbo/t',
    ];
    for (const f of keptByDocker) expect(dockerignoreExcludes(rules, f), f).toBe(false);
    for (const f of leftOutByDocker) expect(dockerignoreExcludes(rules, f), f).toBe(true);
  });

  it('the last pattern that matches a path or a directory above it decides; `!` takes it back', () => {
    const { rules } = dockerignoreRules('# a comment\n\n/build/\n!build/keep\nlogs\n**/tmp\n*.bak\ndocs/*.md\n!docs/README.md\n');
    expect(rules.map((r) => r.pattern)).toEqual(['build', 'build/keep', 'logs', '**/tmp', '*.bak', 'docs/*.md', 'docs/README.md']);
    expect(dockerignoreExcludes(rules, 'build/out.js')).toBe(true);
    expect(dockerignoreExcludes(rules, 'build/keep')).toBe(false);
    expect(dockerignoreExcludes(rules, 'logs/a/b.txt')).toBe(true);
    expect(dockerignoreExcludes(rules, 'src/logs')).toBe(false);
    expect(dockerignoreExcludes(rules, 'a/b/tmp/c')).toBe(true);
    expect(dockerignoreExcludes(rules, 'tmp')).toBe(true);
    expect(dockerignoreExcludes(rules, 'x.bak')).toBe(true);
    expect(dockerignoreExcludes(rules, 'a/x.bak')).toBe(false);
    expect(dockerignoreExcludes(rules, 'docs/GUIDE.md')).toBe(true);
    expect(dockerignoreExcludes(rules, 'docs/README.md')).toBe(false);
    expect(dockerignoreExcludes(rules, 'docs/sub/GUIDE.md')).toBe(false);
  });

  it('refuses to guess: a class, an escape or a ** inside a segment is unsupported, and then proves nothing', () => {
    for (const p of ['[ab].txt', 'a\\*b', 'x**', '**y', 'a/b**/c']) {
      const r = dockerignoreLeavesOut(`${p}\n`, ['a.txt']);
      expect(r.unsupported, p).toEqual([p]);
      expect(r.excluded).toEqual([]);
    }
    expect(dockerignoreRules('**/x\nx/**\nx/**/y\n**\n').unsupported).toEqual([]);
  });

  it('counts only the tracked files the Dockerfile copies: two tracked notes it leaves out are copied by no image', () => {
    const files = ['sweep-2026-05-09/bugs.md', 'web/src/main.ts', 'backend/embed/admin/.keep', 'backend/embed/admin/stale.js'];
    const sources = ['web/**', 'backend/**'];
    expect(dockerignoreLeavesOut(real, files, sources).excluded).toEqual(['backend/embed/admin/stale.js']);
    expect(dockerignoreLeavesOut(real, files, null).excluded).toEqual(['sweep-2026-05-09/bugs.md', 'backend/embed/admin/stale.js']);
  });
});

describe("a Dockerfile's sources: what its build reads from the context", () => {
  const REPO = path.resolve(__dirname, '..', '..', '..');

  it('reads every COPY and ADD from the context; another stage, an image or a URL is no source', () => {
    const text = [
      'FROM node:20 AS a',
      'COPY package.json pnpm-workspace.yaml ./',
      'COPY --chown=1:1 web/ ./web/',
      'COPY ["scripts/x.mjs", "./scripts/"]',
      'COPY --from=a /out /out',
      'COPY --from=alpine:3 /etc/x /x',
      'ADD https://example.invalid/f.tgz /f',
      'RUN --mount=type=cache,target=/root/.cache echo \\',
      '    still the RUN',
      'COPY docker/entrypoint.sh \\',
      '     /usr/local/bin/',
    ].join('\n');
    expect(dockerfileSources(text)).toEqual(
      ['docker/entrypoint.sh', 'docker/entrypoint.sh/**', 'package.json', 'package.json/**', 'pnpm-workspace.yaml', 'pnpm-workspace.yaml/**', 'scripts/x.mjs', 'scripts/x.mjs/**', 'web/**'].sort(),
    );
  });

  it('cannot tell, and says so (null), for the whole context, a pattern, a variable, a heredoc or a bind mount', () => {
    expect(dockerfileSources('FROM a\nCOPY . /src\n')).toEqual(['**']);
    for (const t of ['COPY src/?.ts /x', 'COPY ${DIR} /x', 'COPY <<EOF /x\nhi\nEOF', 'RUN --mount=target=/src make', 'RUN --mount=type=bind,source=web,target=/w ls']) {
      expect(dockerfileSources(`FROM a\n${t}\n`), t).toBeNull();
    }
    expect(dockerfileSources('FROM a\nRUN --mount=type=bind,from=b,target=/w ls\n')).toEqual([]);
  });

  it("reads this repository's two Dockerfiles: the packages, web, backend and the scripts their builds import", () => {
    for (const df of ['docker/Dockerfile', 'docker/Dockerfile.slim']) {
      const s = dockerfileSources(fs.readFileSync(path.join(REPO, df), 'utf8'));
      expect(s, df).not.toBeNull();
      for (const want of ['packages/**', 'web/**', 'backend/**', 'pnpm-lock.yaml*', 'docker/entrypoint.sh', 'scripts/vite-fonts-as-files.mjs', 'scripts/lib/i18n-catalogue.mjs']) expect(s, `${df}: ${want}`).toContain(want);
      expect(s!.some((x) => x.startsWith('desktop/') || x.startsWith('e2e/') || x.startsWith('docs/')), `${df} copies no desktop/, e2e/ or docs/`).toBe(false);
    }
  });
});

// #165: GitHub not answering is not GitHub answering "no".
describe('GitHub, failing to answer', () => {
  it('a 5xx, a timeout or a dropped connection is asked again; a 4xx or the primary rate limit is an answer', () => {
    for (const t of [
      'gh: Server Error (HTTP 502)',
      'HTTP 503: Service Unavailable',
      'Post "https://api.github.com/graphql": net/http: TLS handshake timeout',
      'read tcp 10.0.0.5:51234->140.82.112.6:443: read: connection reset by peer',
      'dial tcp: lookup api.github.com: no such host',
      'You have exceeded a secondary rate limit. Please wait a few minutes before you try again.',
      'context deadline exceeded',
    ]) {
      expect(transientGithubError(t), t).toBe(true);
    }
    for (const t of ['gh: HTTP 404: Not Found', 'HTTP 401: Bad credentials', 'HTTP 403: API rate limit exceeded for user ID 1', 'gh run list did not answer JSON: <html>', '']) {
      expect(transientGithubError(t), t).toBe(false);
    }
  });
});

// #165: a part red on the runner is re-run once by the gate; a red test never.
// The steps are the real ones of 0.55.0's runs (GitHub API, 2026-10-10).
describe('which red parts the gate re-runs', () => {
  const browser = 'The browser, with its system libraries';
  const part = (name: string, id: number, failedSteps: string[]) => ({ name, id, failedSteps, conclusion: 'failure' });
  const done = { status: 'completed', attempt: 1 };
  const at = '2026-10-09T15:53:00Z';
  const now = Date.parse('2026-10-09T15:55:00Z');

  it("knows the runner's steps from the code's", () => {
    // 37952392755 attempt 1: two Playwright parts, red installing the browser.
    expect(infraFailure([browser])).toBe(true);
    expect(infraFailure(['The browsers, with their system libraries'])).toBe(true);
    for (const s of ['Set up job', 'Initialize containers', 'Run actions/checkout@v4', 'Run pnpm/action-setup@v4', 'Run actions/download-artifact@v4', 'Post Run actions/setup-go@v5', 'Run docker/setup-buildx-action@v3']) {
      expect(infraFailure([s]), s).toBe(true);
    }
    // 37945948715: the Istanbul unit suite and the nopub line, cancelled in their own steps;
    // the dry run 37945996492: the desktop update from the previous release.
    for (const s of ['Unit tests', 'The line', 'The part', 'Update from the previous release on Linux', 'Every part of the matrix passed', 'The shard, under -race']) {
      expect(infraFailure([s]), s).toBe(false);
    }
    expect(infraFailure([browser, 'The part'])).toBe(false);
    expect(infraFailure([])).toBe(false);
    expect(infraFailure(null)).toBe(false);
  });

  it('waits while the run goes on, asks once it has finished, and only for the runner', () => {
    const failed = [part('Playwright + Document Server (webkit)', 11, [browser]), part('Playwright (webkit 3/4)', 12, [browser])];
    expect(rerunDecision({ run: { status: 'in_progress', attempt: 1 }, failed, now }).action).toBe('wait');
    const d = rerunDecision({ run: done, failed, now });
    expect(d.action).toBe('rerun');
    expect(d.retry.map((p) => p.name)).toEqual(['Playwright + Document Server (webkit)', 'Playwright (webkit 3/4)']);
    // a red test beside them: red, nothing re-run
    const mixed = rerunDecision({ run: done, failed: [...failed, part('Frontend (pnpm, TZ=Europe/Istanbul)', 13, ['Unit tests'])], now });
    expect(mixed.action).toBe('red');
    expect(mixed.code.map((p) => p.name)).toEqual(['Frontend (pnpm, TZ=Europe/Istanbul)']);
    // no failed step at all is the code's too
    expect(rerunDecision({ run: done, failed: [part('Go (a)', 14, [])], now }).action).toBe('red');
  });

  it("gives a part one re-run: red again is red; copied into the new attempt without running (lesson #1275) is asked again, twice at most", () => {
    const record = { attempt: 1, at, requests: 1, parts: { 'Playwright (webkit 3/4)': 12 } };
    // the re-run has not started yet: wait, and red once it never does
    expect(rerunDecision({ run: done, failed: [part('Playwright (webkit 3/4)', 12, [browser])], record, now }).action).toBe('wait');
    expect(rerunDecision({ run: done, failed: [part('Playwright (webkit 3/4)', 12, [browser])], record, now: now + 60 * 60_000 }).action).toBe('red');
    const second = { status: 'completed', attempt: 2 };
    // it ran again (a new job id) and is red again
    const again = rerunDecision({ run: second, failed: [part('Playwright (webkit 3/4)', 22, [browser])], record, now });
    expect(again.action).toBe('red');
    expect(again.spent.map((p) => p.name)).toEqual(['Playwright (webkit 3/4)']);
    // GitHub copied the old result (the same job id): asked again
    expect(rerunDecision({ run: second, failed: [part('Playwright (webkit 3/4)', 12, [browser])], record, now }).action).toBe('rerun');
    expect(rerunDecision({ run: { status: 'completed', attempt: 3 }, failed: [part('Playwright (webkit 3/4)', 12, [browser])], record: { ...record, attempt: 2, requests: 2 }, now }).action).toBe('red');
    // a part red on the runner for the first time in the new attempt has its own re-run
    expect(rerunDecision({ run: second, failed: [part('Go (b)', 30, ['Set up job'])], record, now }).action).toBe('rerun');
  });
});
