#!/usr/bin/env node
// Fails the build on a markdown link that points at a heading anchor which is
// not emitted anywhere.
//
// ⚠⚠ This exists because `vitepress build` does NOT cover it. VitePress fails
// the build on a dead *page* link and says nothing at all about a dead
// *anchor* — so a green docs build is evidence that every page exists, and no
// evidence whatsoever that the `#section` half of any link lands. Measured
// 2026-09-05 on a green build: 366 in-page links, 98 of them dead.
//
// ⚠⚠ The heading ids are READ FROM VITEPRESS, never re-derived here. A second
// implementation of the slug rule drifts from the real one and starts
// reporting links that actually work, and a check that cries wolf gets
// deleted. So:
//
//   * pages the site builds  -> ids parsed out of `.vitepress/dist/*.html`,
//     i.e. the bytes a reader's browser receives.
//   * pages the site does NOT build (the repo root README/CHANGELOG, the
//     `srcExclude`d docs, the npm package READMEs) -> ids from VitePress's own
//     `createMarkdownRenderer`, loaded with THIS SITE's markdown options via
//     `resolveConfig`, so the same anchor plugin with the same slugify runs.
//
// Those two paths are cross-checked against each other on every page that has
// both. If the renderer ever stops agreeing with the built HTML the check
// exits 2 and says so, rather than quietly trusting the wrong one — which is
// exactly what caught the `markdown.anchor.slugify` change while it was being
// made.
//
//   node scripts/check-doc-anchors.mjs           # needs a built site
//   node scripts/check-doc-anchors.mjs --build   # build the site first
//   node scripts/check-doc-anchors.mjs --dist path/to/dist
//
// Exit codes: 0 clean, 1 dead anchors found, 2 the check could not run.

import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { execFileSync } from 'node:child_process';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const SITE = path.join(REPO, 'docs-site');

const argv = process.argv.slice(2);
const distArg = argv.indexOf('--dist');

if (argv.includes('--build')) {
  execFileSync('npm', ['run', 'build'], {
    cwd: SITE,
    stdio: 'inherit',
    shell: process.platform === 'win32',
  });
}

// VitePress lives in docs-site's node_modules, not the repo root's.
const require = createRequire(path.join(SITE, 'package.json'));
let vitepress;
try {
  vitepress = await import(pathToFileURL(require.resolve('vitepress')).href);
} catch (err) {
  console.error(
    'check-doc-anchors: cannot load vitepress from docs-site — run `pnpm install` first\n  ' +
      err.message,
  );
  process.exit(2);
}

const siteConfig = await vitepress.resolveConfig(SITE, 'build', 'production');
const DOCS = path.resolve(siteConfig.srcDir);
const DIST = distArg !== -1 ? path.resolve(REPO, argv[distArg + 1]) : path.resolve(siteConfig.outDir);

if (!fs.existsSync(DIST)) {
  console.error(
    `check-doc-anchors: no built site at ${rel(DIST)}\n` +
      '  run: cd docs-site && npm run build   (or pass --build)',
  );
  process.exit(2);
}

function rel(p) {
  return path.relative(REPO, p).split(path.sep).join('/');
}

// ---------------------------------------------------------------------------
// Which markdown files take part
// ---------------------------------------------------------------------------
// Sources of links AND targets of links: everything a reader can reach. The
// docs site, the GitHub landing page, and the npm package READMEs — those link
// into `docs/…#section` too, so a dead anchor on an npm page is just as dead.
//
// `docs/handovers/**` is deliberately absent: dated working notes between
// maintainers, `srcExclude`d from the site and stripped from the public
// export, so an anchor rotting inside one is not a reader-visible defect.
const FILES = [
  'README.md',
  'CHANGELOG.md',
  'SECURITY.md',
  'CODE_OF_CONDUCT.md',
  'desktop/README.md',
  ...ls(DOCS)
    .filter((f) => f.endsWith('.md'))
    .map((f) => `${rel(DOCS)}/${f}`),
  ...ls(path.join(REPO, 'packages'))
    .filter((d) => fs.existsSync(path.join(REPO, 'packages', d, 'README.md')))
    .map((d) => `packages/${d}/README.md`),
].filter((f) => fs.existsSync(path.join(REPO, f)));

function ls(dir) {
  return fs.existsSync(dir) ? fs.readdirSync(dir).sort() : [];
}

// A docs page is published at dist/<name>.html; nothing else in the list is.
function distFileFor(relPath) {
  const inDocs = path.relative(DOCS, path.join(REPO, relPath)).split(path.sep).join('/');
  if (inDocs.startsWith('..') || inDocs.includes('/')) return null;
  return path.join(DIST, inDocs.replace(/\.md$/, '.html'));
}

// ---------------------------------------------------------------------------
// Heading ids
// ---------------------------------------------------------------------------
const HEADING_ID = /<h[1-6]\b[^>]*\bid="([^"]*)"/g;

function idsFromHtml(html) {
  const ids = new Set();
  for (const m of html.matchAll(HEADING_ID)) ids.add(decodeEntities(m[1]));
  return ids;
}

function decodeEntities(s) {
  return s
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&');
}

// ⚠ `siteConfig.markdown` — not `{}`. Passing empty options renders every
// heading with VitePress's DEFAULT slugify, which this site overrides; the
// cross-check below turns that mistake into an exit 2 rather than a wrong
// answer, but it is worth not making.
const md = await vitepress.createMarkdownRenderer(DOCS, siteConfig.markdown, siteConfig.site.base);

const rendered = new Map(); // rel -> { ids, hrefs }
for (const f of FILES) {
  const abs = path.join(REPO, f);
  const html = md.render(fs.readFileSync(abs, 'utf8'), {
    path: abs,
    relativePath: path.relative(DOCS, abs).split(path.sep).join('/'),
    cleanUrls: false,
  });
  // Drop the `<a class="header-anchor" href="#id">` VitePress staples onto
  // every heading: they are generated FROM the id, always resolve, and would
  // drown the hand-written links in the count.
  const body = html.replace(/<a class="header-anchor"[^>]*>.*?<\/a>/g, '');
  rendered.set(f, {
    ids: idsFromHtml(html),
    hrefs: [...body.matchAll(/href="([^"]*)"/g)].map((m) => decodeEntities(m[1])),
  });
}

// The ids a browser actually sees, page by page.
const idsFor = new Map();
const drift = [];
for (const f of FILES) {
  const built = distFileFor(f);
  if (built && fs.existsSync(built)) {
    const fromBuild = idsFromHtml(fs.readFileSync(built, 'utf8'));
    idsFor.set(f, fromBuild);
    const missing = [...rendered.get(f).ids].filter((id) => !fromBuild.has(id));
    if (missing.length) drift.push(`${f}: renderer emitted ${missing.join(', ')}; the build did not`);
  } else {
    idsFor.set(f, rendered.get(f).ids);
  }
}

if (drift.length) {
  console.error(
    'check-doc-anchors: the VitePress renderer and the built site disagree on heading ids,\n' +
      'so neither can be trusted. Is the build stale, or is the checker rendering with\n' +
      'different markdown options than the build used?\n',
  );
  for (const d of drift.slice(0, 10)) console.error('  ' + d);
  if (drift.length > 10) console.error(`  … and ${drift.length - 10} more pages`);
  process.exit(2);
}

// ---------------------------------------------------------------------------
// Resolve every in-page link
// ---------------------------------------------------------------------------
// `markdown.config` rewrites repo-relative links to GitHub blobs at render
// time, so both spellings of "our own docs" show up here.
const GH_BLOB = /^https?:\/\/github\.com\/BRF-Tech\/filex\/(?:blob|tree)\/[^/]+\//;
const SITE_HOST = /^https?:\/\/docs\.filex\.sh\//;

// A link target -> the markdown file whose ids apply, or null if not ours.
function resolveTarget(fromRel, href) {
  const hashAt = href.indexOf('#');
  if (hashAt === -1) return null;
  const fragment = href.slice(hashAt + 1);
  if (!fragment) return null;
  let page = href.slice(0, hashAt);

  if (page === '') return { rel: fromRel, fragment };

  let abs;
  if (GH_BLOB.test(page)) {
    abs = path.join(REPO, page.replace(GH_BLOB, '')); // repo-relative
  } else if (SITE_HOST.test(page)) {
    abs = path.join(DOCS, page.replace(SITE_HOST, '')); // site-relative -> srcDir
  } else if (/^[a-z][a-z0-9+.-]*:/i.test(page) || page.startsWith('//')) {
    return null; // somebody else's site
  } else if (page.startsWith('/')) {
    abs = path.join(DOCS, page.slice(1));
  } else {
    abs = path.resolve(path.dirname(path.join(REPO, fromRel)), page);
  }

  let target = rel(abs).replace(/\.html$/, '.md');
  if (!target.endsWith('.md')) target += '.md';
  return idsFor.has(target) ? { rel: target, fragment } : null;
}

// Advisory only — never used to decide pass/fail, so this loose comparison
// cannot smuggle in a second slug rule.
function suggest(fragment, ids) {
  const loose = (s) => s.toLowerCase().replace(/[^a-z0-9]+/g, '');
  const key = loose(fragment);
  const hit = [...ids].find((id) => loose(id) === key);
  return hit && hit !== fragment ? hit : null;
}

const dead = [];
let checked = 0;
for (const f of FILES) {
  const lines = fs.readFileSync(path.join(REPO, f), 'utf8').split('\n');
  for (const href of rendered.get(f).hrefs) {
    const target = resolveTarget(f, href);
    if (!target) continue;
    checked++;
    const ids = idsFor.get(target.rel);
    if (ids.has(target.fragment)) continue;
    const lineNo = lines.findIndex(
      (l) => l.includes(`#${target.fragment})`) || l.includes(`#${target.fragment}"`),
    );
    dead.push({
      from: f,
      line: lineNo === -1 ? null : lineNo + 1,
      page: target.rel,
      fragment: target.fragment,
      hint: suggest(target.fragment, ids),
    });
  }
}

if (!dead.length) {
  console.log(
    `check-doc-anchors: ${checked} in-page links across ${FILES.length} files, all resolve`,
  );
  process.exit(0);
}

console.error(
  `check-doc-anchors: ${dead.length} of ${checked} in-page links point at a heading that is not emitted\n`,
);
for (const d of dead) {
  const where = `${d.from}${d.line ? ':' + d.line : ''}`;
  const hint = d.hint
    ? `(did you mean #${d.hint} ?)`
    : '(no heading on that page produces this id)';
  console.error(`  ${where}\n    -> ${d.page}#${d.fragment}  ${hint}`);
}
console.error(
  `\n${dead.length} dead. Heading ids come from the built site, so the fix is the link — ` +
    'or the heading, if the id it produces is not one anybody would type.',
);
process.exit(1);
