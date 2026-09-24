// Every file the frontend build reaches outside packages/ and web/ must be
// copied into the Docker images' frontend-build stage.
//
// ⚠⚠ v0.43.0, run 36026548054. The npm packages and the GitHub Release were
// published; the container images were not. packages/core and
// packages/webcomponent's vite.config.ts import
// `../../scripts/vite-fonts-as-files.mjs`, web/vite.config.ts imports
// `../scripts/lib/i18n-catalogue.mjs`, and docker/Dockerfile and
// docker/Dockerfile.slim copied only package.json, the workspace file, the
// lockfile, packages/ and web/ into the stage that runs `vite build`. It failed
// the same way on every run. Nothing local caught it: the dev machine has the
// whole tree, and the CI job that builds the images was skipped when the
// release called the CI workflow.
//
// A Docker build takes minutes and needs Docker; this takes milliseconds and
// runs on every push. It follows the build configs' relative imports
// transitively (the same traversal a bundler starts with), keeps what lands
// outside packages/ and web/, and requires each such file to be COPY'd into
// the frontend-build stage of BOTH images, at the path the import expects.
//
// ⚠ …and each one's type declaration. The admin build is `vue-tsc --noEmit &&
// vite build`, and vue-tsc type-checks web/vite.config.ts: an imported `.mjs`
// whose `.d.mts` sibling is missing is TS7016 under `noImplicitAny`. The first
// real image build of 0.43.1 failed exactly so, after this test had passed on
// the two `.mjs` files alone — so a declaration beside an input is an input.
//
// ⚠ …and what those modules READ. scripts/lib/i18n-catalogue.mjs builds the
// translator's catalogue during `vite build` from files it opens itself —
// `path.join(root, 'backend', 'internal', 'srvtext', 'locales')` — and the
// next real image build failed with ENOENT on en.json. So every
// `path.join(root, '<literal>', …)` in an outside module names an input too
// (a directory must be copied whole). A path assembled at run time is beyond
// this trace; the release run's real `docker build` of both images is the
// backstop, and this test exists so the cheap check catches what it can.
import { describe, expect, it } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';

const REPO = path.resolve(__dirname, '../../..');
const BUILD_CONFIGS = [
  'packages/core/vite.config.ts',
  'packages/webcomponent/vite.config.ts',
  'packages/react/vite.config.ts',
  'web/vite.config.ts',
];
const IMAGES = ['docker/Dockerfile', 'docker/Dockerfile.slim'];

const rel = (p: string) => path.relative(REPO, p).split(path.sep).join('/');
const insideCopiedTrees = (r: string) => r.startsWith('packages/') || r.startsWith('web/');

/** Relative imports of one file, resolved to files that exist. */
function importsOf(file: string): string[] {
  const src = fs.readFileSync(file, 'utf8');
  const out: string[] = [];
  const re = /(?:import\s[^'"]*?from\s*|import\(\s*|require\(\s*)['"](\.{1,2}\/[^'"]+)['"]/g;
  for (const m of src.matchAll(re)) {
    const base = path.resolve(path.dirname(file), m[1]);
    for (const cand of [base, `${base}.mjs`, `${base}.ts`, `${base}.js`, path.join(base, 'index.mjs')]) {
      if (fs.existsSync(cand) && fs.statSync(cand).isFile()) {
        out.push(cand);
        break;
      }
    }
  }
  return out;
}

/** The type declarations TypeScript reads for a JS module: `x.mjs` → `x.d.mts`,
 *  `x.js` → `x.d.ts`, `x.cjs` → `x.d.cts` — the ones that exist. */
function declarationsOf(file: string): string[] {
  const m = /\.(mjs|cjs|js)$/.exec(file);
  if (!m) return [];
  const ext = { mjs: '.d.mts', cjs: '.d.cts', js: '.d.ts' }[m[1] as 'mjs' | 'cjs' | 'js'];
  const decl = file.slice(0, -m[0].length) + ext;
  return fs.existsSync(decl) ? [decl] : [];
}

/** The repo paths a module opens as `path.join(root, '<literal>', …)` — a
 *  directory ends in `/`. Only all-literal calls: a segment computed at run
 *  time is invisible here (see the note at the top). */
function readsOf(file: string): string[] {
  const src = fs.readFileSync(file, 'utf8');
  const out: string[] = [];
  const call = /path\.join\(\s*root\s*((?:,\s*'[^'\n]+'\s*)+)\)/g;
  for (const m of src.matchAll(call)) {
    const segs = [...m[1].matchAll(/'([^']+)'/g)].map((x) => x[1]);
    const abs = path.join(REPO, ...segs);
    if (!fs.existsSync(abs)) continue;
    out.push(rel(abs) + (fs.statSync(abs).isDirectory() ? '/' : ''));
  }
  return out;
}

/** Everything the build configs reach outside packages/ and web/, transitively:
 *  what they import, the declarations the type-check reads for each, and the
 *  paths those modules open. */
function outsideInputs(): string[] {
  const seen = new Set<string>();
  const outside = new Set<string>();
  const stack = BUILD_CONFIGS.map((c) => path.join(REPO, c)).filter((p) => fs.existsSync(p));
  while (stack.length) {
    const f = stack.pop()!;
    if (seen.has(f)) continue;
    seen.add(f);
    if (!insideCopiedTrees(rel(f))) {
      outside.add(rel(f));
      for (const d of declarationsOf(f)) outside.add(rel(d));
      for (const r of readsOf(f)) if (!insideCopiedTrees(r)) outside.add(r);
    }
    stack.push(...importsOf(f));
  }
  // A file inside a directory that is itself an input is covered by it.
  const dirs = [...outside].filter((o) => o.endsWith('/'));
  return [...outside].filter((o) => !dirs.some((d) => d !== o && o.startsWith(d))).sort();
}

/** The COPY sources of the stage that runs the frontend build, as repo paths. */
function frontendStageCopies(dockerfile: string): { sources: string[]; destFor: Map<string, string> } {
  const lines = fs.readFileSync(path.join(REPO, dockerfile), 'utf8').split(/\r?\n/);
  const start = lines.findIndex((l) => /^FROM\s.*\sAS\s+frontend-build\b/i.test(l));
  expect(start, `${dockerfile} has a frontend-build stage`).toBeGreaterThanOrEqual(0);
  const end = lines.findIndex((l, i) => i > start && /^FROM\s/i.test(l));
  const stage = lines.slice(start, end < 0 ? undefined : end);
  const sources: string[] = [];
  const destFor = new Map<string, string>();
  for (const l of stage) {
    const m = /^COPY\s+(?!--from)(.+)$/i.exec(l.trim());
    if (!m) continue;
    const parts = m[1].trim().split(/\s+/);
    const dest = parts.pop()!;
    for (const s of parts) {
      sources.push(s);
      destFor.set(s, dest);
    }
  }
  return { sources, destFor };
}

/** Whether a COPY source covers a repo path and lands it where it lived. */
function copiedInPlace(file: string, sources: string[], destFor: Map<string, string>): boolean {
  return sources.some((s) => {
    const src = s.replace(/^\.\//, '');
    const dest = (destFor.get(s) ?? '').replace(/^\.\//, '');
    if (src.endsWith('/')) {
      // A directory: `COPY scripts/ ./scripts/` keeps the layout.
      return file.startsWith(src) && dest === src;
    }
    // A single file into a directory that mirrors its own.
    return src === file && dest === path.posix.dirname(file) + '/';
  });
}

describe('the Docker images build the frontend from what they copy', () => {
  it('the build configs reach files outside packages/ and web/ (the fixture is live)', () => {
    // Without this the loop below could pass on an empty list: a traversal
    // broken by a refactor would find nothing and call every image complete.
    expect(outsideInputs()).toEqual(
      expect.arrayContaining([
        'scripts/vite-fonts-as-files.mjs',
        'scripts/lib/i18n-catalogue.mjs',
        'scripts/lib/i18n-catalogue.d.mts',
        'backend/internal/srvtext/locales/',
      ]),
    );
  });

  for (const image of IMAGES) {
    it(`${image} copies every one of them, at the path the import expects`, () => {
      const { sources, destFor } = frontendStageCopies(image);
      const missing = outsideInputs().filter((f) => !copiedInPlace(f, sources, destFor));
      expect(missing, `${image}'s frontend-build stage does not COPY what the build imports`).toEqual([]);
    });
  }

  it('the copies come before the first build step (a COPY after `vite build` is no copy)', () => {
    for (const image of IMAGES) {
      const lines = fs.readFileSync(path.join(REPO, image), 'utf8').split(/\r?\n/);
      const firstBuild = lines.findIndex((l) => /^RUN\s+pnpm\b.*\bbuild\b/.test(l));
      expect(firstBuild, `${image} runs a pnpm build`).toBeGreaterThan(0);
      for (const f of outsideInputs()) {
        const at = lines.findIndex((l) => /^COPY\s/i.test(l) && l.includes(f));
        if (at >= 0) expect(at, `${image}: ${f} is copied before the build`).toBeLessThan(firstBuild);
      }
    }
  });
});
