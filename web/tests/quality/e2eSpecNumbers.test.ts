// Every e2e spec has a number of its own, and a spec named in the tree exists.
//
// ⚠ The number is how a spec is spoken of: in a comment ("measured, e2e 158"),
// in a lesson, in a release report, in `--grep`. The 0.48.0 integration found
// three specs numbered 158 and two each of 172, 173 and 174 - parallel
// branches had each taken "the last number + 1" - plus a `95b`, a letter used
// to slot a spec in after 95. A number that names two specs names neither,
// and nothing noticed until a person read the directory (task #105).
//
// A new spec takes the highest number in use + 1, checked against the other
// open branches when the branch is cut (docs/CONTRIBUTING.md, "Browser
// suites"). A letter after the number is not a way out: the run order is the
// file names' plain sort (e2e/run.mjs localSpecs), which is not numeric anyway
// ("126-" runs before "95-"), so a spec must not depend on it.
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '../../..');
const SPEC_DIR = path.join(REPO, 'e2e/tests');
const NAME = /^(\d+)-[a-z0-9]+(?:-[a-z0-9]+)*\.spec\.ts$/;

const specs = readdirSync(SPEC_DIR).filter((f) => f.endsWith('.spec.ts'));

describe('e2e spec numbers', () => {
  it('found the suite', () => {
    expect(specs.length).toBeGreaterThan(50);
  });

  it('every spec is named <number>-<words>.spec.ts, with no letter after the number', () => {
    expect(specs.filter((f) => !NAME.test(f))).toEqual([]);
  });

  it('no number names two specs', () => {
    const byNumber = new Map<number, string[]>();
    for (const f of specs) {
      const m = /^(\d+)/.exec(f);
      if (!m) continue;
      const n = Number(m[1]);
      byNumber.set(n, [...(byNumber.get(n) ?? []), f]);
    }
    const twice = [...byNumber]
      .filter(([, files]) => files.length > 1)
      .map(([n, files]) => `${n}: ${files.sort().join(', ')}`);
    expect(twice).toEqual([]);
  });
});

// Where a spec is named by its file name: the suite itself, the screenshot
// scenes, the unit tests that point at their browser half, the product code
// that cites a measurement, the docs. History (CHANGELOG.md, handovers) keeps
// the names a spec had then and is not read.
const ROOTS = [
  'e2e',
  'web/src',
  'web/tests',
  'web/cypress',
  'packages',
  'backend',
  'desktop/src',
  'desktop/scripts',
  'docs',
  'scripts',
  '.github',
  'README.md',
];
const SKIP_DIRS = new Set([
  'node_modules',
  'dist',
  'test-results',
  'playwright-report',
  '.artifacts',
  '.tmp',
  'embed',
  'handovers',
  'testdata',
]);
const TEXT = /\.(ts|mts|mjs|js|vue|go|md|ya?ml|sh)$/;

// A name kept on purpose for a spec that no longer exists under it.
const FORMER_NAMES: Record<string, string> = {
  '60-profile.spec.ts': 'e2e/tests/60-user-settings.spec.ts says which file it used to be',
};

function walk(p: string, out: string[]): void {
  if (!existsSync(p)) return;
  if (statSync(p).isDirectory()) {
    for (const name of readdirSync(p)) {
      if (!SKIP_DIRS.has(name)) walk(path.join(p, name), out);
    }
  } else if (TEXT.test(p)) {
    out.push(p);
  }
}

describe('a spec named in the tree', () => {
  const files: string[] = [];
  for (const r of ROOTS) walk(path.join(REPO, r), files);
  const REF = /\b(\d+[a-z]?-[a-z0-9]+(?:-[a-z0-9]+)*)\.spec\b/g;

  it('read the places specs are named from', () => {
    expect(files.length).toBeGreaterThan(500);
  });

  it('is one that exists', () => {
    const missing: string[] = [];
    for (const f of files) {
      const text = readFileSync(f, 'utf8');
      for (const m of text.matchAll(REF)) {
        const name = `${m[1]}.spec.ts`;
        if (specs.includes(name) || FORMER_NAMES[name]) continue;
        const line = text.slice(0, m.index).split('\n').length;
        missing.push(`${path.relative(REPO, f).split(path.sep).join('/')}:${line} ${name}`);
      }
    }
    expect(missing).toEqual([]);
  });
});
