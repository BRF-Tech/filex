// The published screenshots: content-named files on filex.sh, the manifest that
// names them, and the rules that decide what a `pnpm shots` run takes again.
//
// ⚠⚠ Why the pictures left the repository (task #176, the owner's decision of
// 2026-10-06). Every release used to take all of them again into a new
// `docs/screenshots/vX.Y.Z/` - ~40 MB a release, 338 MB by 0.52.0 - and the
// release-day review went over 150+ pictures when ~25 had changed. Now:
//
//   · a run takes only the scenes whose inputs changed (sceneDigest below);
//   · a picture counts as changed only when its PIXELS moved (scripts/lib/png.mjs),
//     and only changed and new pictures are shown for review;
//   · a reviewed picture is published once, at a name that carries its content
//     hash (`sidenav/sidenav-rail-1440.<12 hex>.png`), under SHOTS_SITE_BASE;
//   · e2e/shots/manifest.json says which file is current for every picture,
//     and README, the docs and filex.sh link exactly those URLs.
//
// ⚠ Why the hash is in the NAME. filex.sh sits behind Cloudflare since
// 2026-10-06, and the edge keeps a .png it has seen. A picture that changed
// under the same name would be served stale until somebody purged it; a new
// name is a new URL, nothing to purge, and the old URL keeps showing what it
// always showed - which is also what an old README (a tag, a fork) links.
// Nothing published is ever deleted or overwritten.
//
// This module is pure where it can be (naming, references, decisions), so the
// rules are testable without a browser, a server or the network.

import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { SHOTS_RELEASE, SHOTS_ROOT_REL } from '../../e2e/shots/release.mjs';
import { PACKS_BEHIND } from './shot-scripts.mjs';

/** Where the published pictures live. One directory, never pruned. */
export const SHOTS_SITE_BASE = 'https://filex.sh/shots/';

/** The record of what is published, repo-relative. */
export const MANIFEST_REL = 'e2e/shots/manifest.json';

/** How much of the sha256 goes into a published file's name. 48 bits. */
export const HASH_CHARS = 12;

/** Where a run leaves its work, all under e2e/.artifacts (git-ignored). */
export const ARTIFACTS_REL = 'e2e/.artifacts/shots';
export const CAPTURE_REL = SHOTS_ROOT_REL;
export const REVIEW_REL = `${ARTIFACTS_REL}/review.json`;
export const PUBLISH_REL = `${ARTIFACTS_REL}/publish`;
export const DIFF_REL = `${ARTIFACTS_REL}/diff`;
/** Published pictures already downloaded, by sha256. Outside ARTIFACTS_REL so
 *  a run (and CI's artefact) does not carry them. */
export const CACHE_REL = 'e2e/.artifacts/shots-cache';

/**
 * When a picture counts as changed: more than `pixels` pixels moved by more
 * than `tolerance` (0-255) in some channel. Measured against two takes of the
 * same screen, which differ by a few anti-aliased pixels at most; a word that
 * changed moves a few hundred. Overridable per run with SHOTS_DIFF_TOLERANCE
 * and SHOTS_DIFF_PIXELS.
 */
export const DIFF_DEFAULTS = { tolerance: 24, pixels: 32 };

export function diffThresholds(env = process.env) {
  const num = (v, d) => (v !== undefined && v !== '' && Number.isFinite(Number(v)) && Number(v) >= 0 ? Number(v) : d);
  return { tolerance: num(env.SHOTS_DIFF_TOLERANCE, DIFF_DEFAULTS.tolerance), pixels: num(env.SHOTS_DIFF_PIXELS, DIFF_DEFAULTS.pixels) };
}

/** True when a diff (scripts/lib/png.mjs diffImages) is past the threshold. */
export const isChanged = (diff, { pixels = DIFF_DEFAULTS.pixels } = {}) => !diff.sameSize || diff.pixels > pixels;

export const sha256 = (buf) => createHash('sha256').update(buf).digest('hex');

const NAME_RE = /^[A-Za-z0-9][A-Za-z0-9._-]*(?:\/[A-Za-z0-9][A-Za-z0-9._-]*)*\.png$/;

/** Is `name` a picture name a manifest may hold (`set/file.png`, no `..`)? */
export const isPictureName = (name) => typeof name === 'string' && NAME_RE.test(name) && !name.includes('..');

/** `sidenav/x-1440.png` + sha256 → `sidenav/x-1440.<12 hex>.png`. */
export function publishedName(name, sha) {
  if (!isPictureName(name)) throw new Error(`not a picture name: ${name}`);
  if (!/^[0-9a-f]{64}$/.test(String(sha))) throw new Error(`${name}: not a sha256: ${sha}`);
  return name.replace(/\.png$/, `.${sha.slice(0, HASH_CHARS)}.png`);
}

export const publishedUrl = (base, name, sha) => `${base}${publishedName(name, sha)}`;

/** The current URL of a manifest picture, or null when it is not there. */
export function urlOf(manifest, name) {
  const p = manifest.pictures[name];
  return p ? publishedUrl(manifest.base, name, p.sha256) : null;
}

/**
 * `https://filex.sh/shots/sidenav/x-1440.0123456789ab.png` → `{ name:
 * 'sidenav/x-1440.png', short: '0123456789ab' }`. A URL under the base without
 * a hash gives `short: null`; anything else gives null.
 */
export function parsePublishedUrl(url, base = SHOTS_SITE_BASE) {
  if (typeof url !== 'string' || !url.startsWith(base)) return null;
  const rest = url.slice(base.length);
  const hashed = new RegExp(`^(.+)\\.([0-9a-f]{${HASH_CHARS}})\\.png$`).exec(rest);
  if (hashed && isPictureName(`${hashed[1]}.png`)) return { name: `${hashed[1]}.png`, short: hashed[2] };
  return isPictureName(rest) ? { name: rest, short: null } : null;
}

// ── the manifest ─────────────────────────────────────────────────────────────

export const MANIFEST_ABOUT =
  'The screenshots README, the docs and filex.sh show, as published under `base`. ' +
  'Written by scripts/shots-site.mjs (accept, adopt) - never by hand. e2e/shots/README.md';

export function emptyManifest() {
  return { about: MANIFEST_ABOUT, base: SHOTS_SITE_BASE, platform: null, environment: null, pictures: {}, scenes: {} };
}

/** Reads a manifest. A missing file is an empty manifest when `missingOk`. */
export function readManifest(file, { missingOk = false } = {}) {
  if (!fs.existsSync(file)) {
    if (missingOk) return emptyManifest();
    throw new Error(`${file} does not exist`);
  }
  const m = JSON.parse(fs.readFileSync(file, 'utf8'));
  return { ...emptyManifest(), ...m, pictures: m.pictures ?? {}, scenes: m.scenes ?? {} };
}

/**
 * A picture's record. `taken`: when its published file was taken. `checked`:
 * the last run that took it again and found the same pixels - what the
 * staleness check (scripts/check-shop-window.mjs) measures from, because a
 * retake that changed nothing proves the picture current.
 */
const PICTURE_KEYS = ['sha256', 'width', 'height', 'bytes', 'scene', 'taken', 'checked'];

/** Stable JSON: sorted names, fixed key order, two spaces, newline at the end. */
export function serializeManifest(m) {
  const pictures = {};
  for (const name of Object.keys(m.pictures).sort()) {
    const p = m.pictures[name];
    pictures[name] = Object.fromEntries(PICTURE_KEYS.filter((k) => p[k] !== undefined).map((k) => [k, p[k]]));
  }
  const scenes = {};
  for (const s of Object.keys(m.scenes).sort()) scenes[s] = { digest: m.scenes[s]?.digest ?? null };
  return `${JSON.stringify({ about: m.about ?? MANIFEST_ABOUT, base: m.base, platform: m.platform ?? null, environment: m.environment ?? null, pictures, scenes }, null, 2)}\n`;
}

export function writeManifest(file, m) {
  fs.writeFileSync(file, serializeManifest(m));
}

/**
 * What is wrong with a manifest, as sentences; [] when nothing is. `scripts`
 * (the shot scripts that exist) makes a picture attributed to a script that
 * is gone a problem too.
 */
export function manifestProblems(m, { scripts = null } = {}) {
  const out = [];
  if (typeof m.base !== 'string' || !/^https:\/\/[^/]+\/.+\/$/.test(m.base)) out.push(`base is not an https URL ending in "/": ${m.base}`);
  if (!m.pictures || typeof m.pictures !== 'object') return [...out, 'pictures is missing'];
  for (const [name, p] of Object.entries(m.pictures)) {
    if (!isPictureName(name)) out.push(`${name}: not a picture name`);
    if (!/^[0-9a-f]{64}$/.test(String(p.sha256))) out.push(`${name}: sha256 is not 64 hex digits`);
    for (const k of ['width', 'height', 'bytes']) if (!(Number.isInteger(p[k]) && p[k] > 0)) out.push(`${name}: ${k} is not a positive integer`);
    if (!p.taken || Number.isNaN(Date.parse(p.taken))) out.push(`${name}: taken is not a date`);
    if (p.checked !== undefined && Number.isNaN(Date.parse(p.checked))) out.push(`${name}: checked is not a date`);
    if (scripts && !scripts.includes(p.scene)) out.push(`${name}: taken by ${p.scene}, which is not a shot script`);
  }
  for (const [s, v] of Object.entries(m.scenes ?? {})) {
    if (scripts && !scripts.includes(s)) out.push(`scenes.${s}: not a shot script`);
    if (v?.digest !== null && !/^[0-9a-f]{64}$/.test(String(v?.digest))) out.push(`scenes.${s}: digest is neither null nor a sha256`);
  }
  return out;
}

// ── references: every place a picture is shown ───────────────────────────────

const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/**
 * The files that show screenshots: the README and its translations, the docs
 * (not docs/handovers - notes between maintainers, history as written), the
 * filex.sh page, the app-store manifests under deploy/, and the READMEs of the
 * packages, the desktop app and e2e.
 */
export function referenceFiles(repo) {
  const out = [];
  const exists = (rel) => fs.existsSync(path.join(repo, rel));
  for (const f of fs.readdirSync(repo).sort()) if (/^README(\.[\w-]+)?\.md$/.test(f)) out.push(f);
  const walk = (rel, keep, skip = () => false) => {
    if (!exists(rel)) return;
    for (const e of fs.readdirSync(path.join(repo, rel), { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
      const r = `${rel}/${e.name}`;
      if (skip(r) || e.name === 'node_modules' || e.name.startsWith('.')) continue;
      if (e.isDirectory()) walk(r, keep, skip);
      else if (keep(e.name)) out.push(r);
    }
  };
  walk('docs', (n) => n.endsWith('.md'), (r) => r === 'docs/handovers' || r === 'docs/screenshots');
  if (exists('site/index.html')) out.push('site/index.html');
  walk('deploy', (n) => /\.(ya?ml|md|json)$/.test(n));
  for (const rel of ['desktop/README.md', 'e2e/README.md', 'e2e/shots/README.md']) if (exists(rel)) out.push(rel);
  if (exists('packages')) {
    for (const p of fs.readdirSync(path.join(repo, 'packages')).sort()) {
      if (exists(`packages/${p}/README.md`)) out.push(`packages/${p}/README.md`);
    }
  }
  return out;
}

/**
 * Every screenshot reference in a text, in order:
 *
 *   published   `<base><name>.<hash>.png` (or `<base><name>` without a hash)
 *   legacy      a path into the old folders - `docs/screenshots/vX.Y.Z/<name>`,
 *               `screenshots/vX.Y.Z/<name>` from docs/, `./docs/…`, an older
 *               unversioned `docs/screenshots/<name>`, and the same file on
 *               raw.githubusercontent.com (the app-store manifests)
 *   site-asset  `assets/<name>.png` on the filex.sh page (`siteAssets`), and
 *               only for a name the manifest holds at its root: the page's
 *               own files (the social card, a crop for an issue) are not
 *               screenshots and are left alone
 *
 * `{ index, length, raw, kind, name, short }`; `short` is the hash in a
 * published URL, null elsewhere.
 */
export function findReferences(text, { base = SHOTS_SITE_BASE, siteAssets = false, names = null } = {}) {
  const found = [];
  const add = (m, kind, name, short = null) => found.push({ index: m.index, length: m[0].length, raw: m[0], kind, name, short });
  for (const m of text.matchAll(new RegExp(`${esc(base)}([A-Za-z0-9._/-]+?\\.png)`, 'g'))) {
    const p = parsePublishedUrl(m[0], base);
    if (p) add(m, 'published', p.name, p.short);
  }
  for (const m of text.matchAll(/https:\/\/raw\.githubusercontent\.com\/BRF-Tech\/filex\/[A-Za-z0-9._-]+\/docs\/screenshots\/(?:v\d+\.\d+\.\d+\/)?([A-Za-z0-9._/-]+?\.png)/g)) {
    add(m, 'legacy', m[1]);
  }
  for (const m of text.matchAll(/(?<![\w/.-])(?:\.\.?\/)*(?:docs\/)?screenshots\/v\d+\.\d+\.\d+\/([A-Za-z0-9._/-]+?\.png)/g)) add(m, 'legacy', m[1]);
  for (const m of text.matchAll(/(?<![\w/.-])(?:\.\.?\/)*docs\/screenshots\/(?!v\d)([A-Za-z0-9._/-]+?\.png)/g)) add(m, 'legacy', m[1]);
  if (siteAssets) {
    for (const m of text.matchAll(/(?<![\w/.-])assets\/([A-Za-z0-9._-]+\.png)/g)) {
      if (!names || names.has(m[1])) add(m, 'site-asset', m[1]);
    }
  }
  found.sort((a, b) => a.index - b.index);
  // Overlaps cannot happen between the patterns above, but a later edit could
  // make them: keep the first of any two that overlap rather than corrupt text.
  const out = [];
  let end = -1;
  for (const r of found) {
    if (r.index < end) continue;
    out.push(r);
    end = r.index + r.length;
  }
  return out;
}

const lineOf = (text, index) => text.slice(0, index).split('\n').length;

/**
 * Points every reference in `text` at the manifest's current URL.
 * `{ text, changes: [{ line, from, to }], unresolved: [{ line, ref, why }] }`.
 */
export function relinkText(text, manifest, { siteAssets = false } = {}) {
  const rootNames = new Set(Object.keys(manifest.pictures).filter((n) => !n.includes('/')));
  const refs = findReferences(text, { base: manifest.base, siteAssets, names: rootNames });
  const changes = [];
  const unresolved = [];
  let out = '';
  let at = 0;
  for (const r of refs) {
    out += text.slice(at, r.index);
    at = r.index + r.length;
    const url = urlOf(manifest, r.name);
    if (!url) {
      unresolved.push({ line: lineOf(text, r.index), ref: r.raw, why: `${r.name} is not in ${MANIFEST_REL}` });
      out += r.raw;
      continue;
    }
    if (url !== r.raw) changes.push({ line: lineOf(text, r.index), from: r.raw, to: url });
    out += url;
  }
  out += text.slice(at);
  return { text: out, changes, unresolved };
}

/** relinkText over every reference file: `[{ file, text, changes, unresolved }]`. */
export function relinkRepo(repo, manifest) {
  return referenceFiles(repo).map((file) => {
    const before = fs.readFileSync(path.join(repo, file), 'utf8');
    const r = relinkText(before, manifest, { siteAssets: file === 'site/index.html' });
    return { file, before, ...r };
  });
}

/** Every published URL the reference files show, with where: `[{ file, line, url, name }]`. */
export function publishedReferences(repo, manifest) {
  const out = [];
  for (const file of referenceFiles(repo)) {
    const text = fs.readFileSync(path.join(repo, file), 'utf8');
    for (const r of findReferences(text, { base: manifest.base })) {
      if (r.kind === 'published') out.push({ file, line: lineOf(text, r.index), url: r.raw, name: r.name });
    }
  }
  return out;
}

// ── what a scene reads: its digest ───────────────────────────────────────────

/**
 * What every scene's pictures depend on besides the product: the fixture
 * files the scenes upload and the board app apps.mjs packs. Read by path, not
 * imported, so the import walk below cannot see them.
 */
export const SCENE_DATA = ['e2e/fixtures', 'e2e/shots/board-app'];

/**
 * What a scene that declares its INPUTS reads besides them: the shell every
 * screen sits in, the theme, the English catalogues and the dependency lock.
 */
export const SHARED_INPUTS = [
  'packages/core/src/styles',
  'packages/core/src/locales',
  'web/src/locales/en.json',
  'web/src/App.vue',
  'web/src/main.ts',
  'web/index.html',
  'pnpm-lock.yaml',
];

/**
 * What a scene that declares NOTHING reads: the whole product. Conservative
 * on purpose - a scene skipped on a change it did depend on is a stale
 * picture, which is wrong information; a scene taken once too often costs a
 * minute, and its unchanged pictures are found unchanged by their pixels.
 */
export function productInputs(repo) {
  const dir = path.join(repo, 'packages');
  const packages = fs.existsSync(dir)
    ? fs
        .readdirSync(dir, { withFileTypes: true })
        .filter((e) => e.isDirectory())
        .map((e) => `packages/${e.name}/src`)
        .sort()
    : [];
  return ['backend', 'web/src', 'web/public', 'web/index.html', 'pnpm-lock.yaml', ...packages];
}

/** Product files that cannot change a picture: tests, test data, prose. */
const NOT_PRODUCT = [/_test\.go$/, /(^|\/)testdata\//, /\.(test|spec)\.[cm]?[jt]sx?$/, /(^|\/)__tests__\//, /\.md$/];

/**
 * A scene's declared inputs: `export const INPUTS = [...]` in its own source,
 * paths from the repository root plus the token `'@version'` when its pictures
 * show the release number. Null when it declares none.
 *
 * ⚠ Read off the source, like scriptNeeds' `findApp` and `const SET`: the
 * declaration sits next to the code that takes the pictures, where whoever
 * changes what a scene shows will see it.
 */
export function scriptInputs(dir, file) {
  const src = fs.readFileSync(path.join(dir, file), 'utf8');
  const m = /\bexport\s+const\s+INPUTS\s*=\s*\[([\s\S]*?)\]/.exec(src);
  if (!m) return null;
  return [...m[1].matchAll(/['"`]([^'"`]+)['"`]/g)].map((x) => x[1]);
}

const IMPORT_RE = /(?:\bfrom\s*|\bimport\s*\(\s*|^\s*import\s+)['"](\.\.?\/[^'"]+)['"]/gm;

/**
 * A file and every file it imports by a relative path, transitively, inside
 * the repository. Repo-relative, forward slashes, sorted.
 */
export function localImports(repo, rel) {
  const seen = new Set();
  const visit = (r) => {
    if (seen.has(r)) return;
    const abs = path.join(repo, r);
    if (!fs.existsSync(abs) || !fs.statSync(abs).isFile()) return;
    seen.add(r);
    if (!/\.(mjs|js|ts|mts)$/.test(r)) return;
    for (const m of fs.readFileSync(abs, 'utf8').matchAll(IMPORT_RE)) {
      const target = path.relative(repo, path.resolve(path.dirname(abs), m[1])).split(path.sep).join('/');
      if (target.startsWith('..') || target.includes('node_modules')) continue;
      visit(target);
    }
  };
  visit(rel);
  return [...seen].sort();
}

/**
 * The files git knows under `prefixes` - tracked, and new ones not yet added
 * (but not ignored ones: build output is not an input). Forward slashes.
 */
export function gitFiles(repo, prefixes) {
  const present = prefixes.filter((p) => fs.existsSync(path.join(repo, p)));
  if (!present.length) return [];
  const r = spawnSync('git', ['-C', repo, '-c', 'core.quotepath=off', 'ls-files', '-co', '--exclude-standard', '-z', '--', ...present], {
    encoding: 'utf8',
    maxBuffer: 256 * 1024 * 1024,
  });
  if (r.status !== 0) throw new Error(`git ls-files failed: ${r.stderr || r.error?.message}`);
  return r.stdout.split('\0').filter(Boolean).sort();
}

/** sha256 of each file's bytes, remembered for the run (scenes share most). */
export function fileHasher(repo) {
  const memo = new Map();
  return (rel) => {
    if (!memo.has(rel)) {
      const abs = path.join(repo, rel);
      memo.set(rel, fs.existsSync(abs) && fs.statSync(abs).isFile() ? sha256(fs.readFileSync(abs)) : 'missing');
    }
    return memo.get(rel);
  };
}

/** One digest over files (by path and content) and tokens. Order-free. */
export function digestOf(files, hash, tokens = []) {
  const h = createHash('sha256');
  for (const f of [...new Set(files)].sort()) h.update(`file\0${f}\0${hash(f)}\n`);
  for (const t of [...new Set(tokens)].sort()) h.update(`token\0${t}\n`);
  return h.digest('hex');
}

/**
 * The tokens every scene's digest carries besides files: the browser locale
 * the scenes pin (scene.mjs → newContext, capture.mjs), the operating system
 * (fonts are the system's, so a picture taken elsewhere is a different
 * picture), and the release number the binary is stamped with - for a scene
 * that declares INPUTS only when it lists '@version'.
 */
export function baseTokens({ platform = process.platform, release = SHOTS_RELEASE, declared = null } = {}) {
  const tokens = ['locale:en-US', `platform:${platform}`];
  if (!declared || declared.includes('@version')) tokens.push(`version:${release}`);
  return tokens;
}

/**
 * The digest of one scene: what has to change for its pictures to change.
 *
 *   its own source and every module it imports (not release.mjs, whose one
 *   variable is the release number - a token, see baseTokens)
 *   + SCENE_DATA
 *   + its INPUTS and SHARED_INPUTS when it declares INPUTS, the whole
 *     product (productInputs) when it does not
 *   + tokens: baseTokens, and what the caller adds (an app build's hash, the
 *     document server a scene is pointed at)
 *
 * Two runs with the same digest take the same pictures, so the second is
 * skipped. ⚠ Only as true as the declaration: a scene whose INPUTS miss a
 * component it shows keeps a stale picture until a full run (`pnpm shots
 * --all`, every night) finds its pixels moved - and says so.
 */
export function sceneDigest({ repo, shotsDirRel = 'e2e/shots', script, declared = null, tokens = [], hash = fileHasher(repo), filesUnder = (p) => gitFiles(repo, p) }) {
  const files = localImports(repo, `${shotsDirRel}/${script}`).filter((f) => f !== `${shotsDirRel}/release.mjs`);
  const inputs = declared ? [...SHARED_INPUTS, ...declared.filter((d) => !d.startsWith('@'))] : productInputs(repo);
  for (const f of filesUnder([...SCENE_DATA, ...inputs])) if (!NOT_PRODUCT.some((re) => re.test(f))) files.push(f);
  return digestOf(files, hash, [...baseTokens({ declared }), ...tokens]);
}

/** Which folder each script writes its pictures into: Map(set → script), '' the root. */
export function shotSets(dir, scripts) {
  const out = new Map();
  for (const f of scripts) {
    const src = fs.readFileSync(path.join(dir, f), 'utf8');
    const set = /\bconst\s+SET\s*=\s*['"]([\w-]+)['"]/.exec(src)?.[1] ?? /\bshotsDir\(\s*['"]([\w-]+)['"]\s*\)/.exec(src)?.[1];
    if (set !== undefined) {
      if (!out.has(set)) out.set(set, f);
    } else if (/\bshotsDir\(\s*\)/.test(src) && !out.has('')) out.set('', f);
  }
  return out;
}

/** The script that takes `name`, from its folder (shotSets), or null. */
export function sceneOfName(sets, name) {
  const slash = name.indexOf('/');
  return sets.get(slash === -1 ? '' : name.slice(0, slash)) ?? null;
}

// ── what a run does with each scene, and what it found ───────────────────────

/**
 * Decides, before anything is built, what each scene is:
 *
 *   shoot      taken by this run
 *   published  skipped: its digest is the one recorded with the published set
 *   kept       skipped: the last run took it with this digest and its
 *              pictures are still in the capture folder (`keptOk`)
 *   left-out   needs an app build or a document server this run has none of
 *   not-asked  `--only` named other scenes and nothing can stand in for it
 *
 * `mode`: 'incremental' (the default), 'all' (`--all`: every scene) or
 * 'only' (with `only`, the script names without .mjs).
 */
export function decideScenes({ scripts, digests, manifest, previous = null, mode = 'incremental', only = [], excluded = new Map(), keptOk = () => false }) {
  const plan = new Map();
  for (const f of scripts) {
    const digest = digests.get(f) ?? null;
    const set = (action, why) => plan.set(f, { action, why, digest });
    if (excluded.has(f)) {
      set('left-out', excluded.get(f));
      continue;
    }
    if (mode === 'all') {
      set('shoot', '--all');
      continue;
    }
    if (mode === 'only' && only.includes(f.replace(/\.mjs$/, ''))) {
      set('shoot', '--only');
      continue;
    }
    const recorded = manifest.scenes?.[f]?.digest ?? null;
    if (digest && recorded === digest) {
      set('published', 'nothing it reads changed since the published set');
      continue;
    }
    const prev = previous?.scenes?.[f];
    const prevOk = prev && ((prev.action === 'shoot' && prev.status === 'passed') || prev.action === 'kept');
    if (digest && prevOk && prev.digest === digest && keptOk(f, prev)) {
      set('kept', 'the last run took it, and nothing it reads changed since');
      continue;
    }
    if (mode === 'only') {
      set('not-asked', 'not in --only, and nothing taken or published stands in for it');
      continue;
    }
    set('shoot', recorded ? 'something it reads changed since the published set' : 'no digest recorded with the published set');
  }
  return plan;
}

/**
 * What a run reports for the scenes that failed (`[{ file, timedOut, log }]`,
 * in the order they ran), or null when none did.
 *
 * Without `keepGoing` the first failure stops the run, and the rest were not
 * run. With it (`pnpm shots --keep-going`, the nightly run's shots job) every
 * other scene was still taken - one stale language pack must not leave a
 * night's other scenes untaken - and every failed scene is named. Either way
 * the run is a failure, and `accept` refuses it.
 */
export function runFailure(failed, { keepGoing = false } = {}) {
  if (!failed?.length) return null;
  const one = (f) =>
    `e2e/shots/${f.file} ${f.timedOut ? 'timed out' : 'failed'}${f.reason === PACKS_BEHIND ? ': its language packs are behind this tree' : ''}`;
  if (!keepGoing) return `${one(failed[0])} — see its output above (log: ${failed[0].log}). Stopping: the rest were not run.`;
  return (
    `${failed.length} scene(s) failed, and every other scene was still taken (--keep-going): ` +
    failed.map((f) => `${one(f)} (log: ${f.log})`).join('; ')
  );
}

/**
 * The scenes of a failed run that failed ONLY because their language packs
 * are behind this tree (PACKS_BEHIND, e2e/shots/langpack.mjs), when those are
 * all of its failures - or null: no failure, another scene failed, timed out
 * or was not run, or processes were left that could not be ended.
 *
 * The nightly chain's shots job reports such a run as a warning, not a red
 * night (the maintainer, 2026-10-08, task #187): the strings this tree added are
 * translated the night after, and the rest of the scenes were taken. The run
 * itself stays a failure - `accept` refuses it - and a release run is red.
 */
export function onlyPacksBehind(review) {
  if (!review?.failure || review.stuck) return null;
  const notOk = Object.entries(review.scenes ?? {}).filter(([, s]) => s.action === 'shoot' && s.status !== 'passed');
  if (!notOk.length) return null;
  if (!notOk.every(([, s]) => s.status === 'failed' && s.reason === PACKS_BEHIND)) return null;
  return notOk.map(([f]) => f).sort();
}

/**
 * The manifest a reviewed run turns into: new and changed pictures take their
 * new file, removed ones go, unchanged ones keep the published file (their
 * pixels did not move, so neither does their URL), and every scene that was
 * taken or kept records its digest so the next run can skip it.
 */
export function nextManifest(manifest, review) {
  const next = { ...manifest, pictures: { ...manifest.pictures }, scenes: { ...manifest.scenes } };
  let moved = false;
  for (const [name, p] of Object.entries(review.pictures ?? {})) {
    if (p.status === 'new' || p.status === 'changed') {
      next.pictures[name] = { sha256: p.sha256, width: p.width, height: p.height, bytes: p.bytes, scene: p.scene, taken: review.when, checked: review.when };
      moved = true;
    } else if (p.status === 'same' && next.pictures[name]) {
      next.pictures[name] = { ...next.pictures[name], scene: p.scene ?? next.pictures[name].scene, checked: review.when };
    }
  }
  for (const name of review.removed ?? []) {
    delete next.pictures[name];
    moved = true;
  }
  if (review.complete) {
    for (const s of Object.keys(next.scenes)) if (!review.scenes?.[s]) delete next.scenes[s];
  }
  for (const [s, v] of Object.entries(review.scenes ?? {})) {
    if ((v.action === 'shoot' && v.status === 'passed') || v.action === 'kept') next.scenes[s] = { digest: v.digest };
  }
  if (moved) {
    next.platform = review.platform;
    next.environment = review.environment ?? null;
  }
  return next;
}

/**
 * The operating system the published set is taken on: Linux, the build host
 * (the owner's decision of 2026-10-06, task #176) - `process.platform` there.
 * A run anywhere else is for looking only: `pnpm shots` stages nothing for the
 * site, and `accept` refuses it, whole or partial.
 */
export const PUBLISH_PLATFORM = 'linux';

/**
 * WHERE on Linux (decided 2026-10-06): the build host's test chain - the
 * Playwright image scripts/chain/run.mjs names, with its FONTS_CONF, which
 * sets sans-serif and system-ui in Liberation Sans - as its shots job takes
 * them every night (scripts/chain/job/shots.sh, SHOTS_ENVIRONMENT=chain) and a
 * release takes them (a targeted run, CHAIN_EXTRAS=shots). The build host
 * itself sets the same pages in DejaVu Sans: both are "linux", and a picture
 * from one beside pictures from the other is a README in two typefaces. The
 * 0.52.0 set, adopted when the pictures left the repository, was taken on the
 * host; the first night's --all run retakes every one in the chain, once.
 * Since task #187 the chain takes every scene - the app scenes with the app
 * builds and language packs it mounts, the ONLYOFFICE scene against its own
 * Document Server - so no scene has to be taken on the host any more.
 */
export const PUBLISH_ENVIRONMENT = 'chain';

/**
 * A warning for a run taken outside PUBLISH_ENVIRONMENT, or ''. `accept`
 * refuses such a run (acceptRefusal) unless `--outside-chain` says it is
 * meant, and then prints this: the chain takes every scene (#187), and a
 * picture taken anywhere else reads in that place's typeface.
 */
export function environmentNote(manifest, review) {
  const env = review?.environment ?? 'local';
  if (env === PUBLISH_ENVIRONMENT) return '';
  return (
    `this run was taken in "${env}", and the published set in "${manifest.environment ?? PUBLISH_ENVIRONMENT}" - the build host's chain ` +
    "(its Playwright image and fontconfig, scripts/chain/job/shots.sh): this run's pictures may read in another typeface. " +
    'The chain takes every scene: CHAIN_EXTRAS=shots bash scripts/chain/run.sh --profile targeted --src <this checkout>.'
  );
}

/**
 * Why a reviewed run may not become the published set, or '' when it may.
 *
 * ⚠ The platform rule. Fonts are the operating system's: a picture taken on
 * Linux beside pictures taken on Windows is a README in two typefaces. So
 *   · a run not taken on `publishPlatform` (PUBLISH_PLATFORM) is refused,
 *     even when it took every scene: the published set is taken in one place;
 *   · a manifest whose set came from elsewhere (an older one, recorded in
 *     its `platform`) is replaced only whole - `--all`, every scene taken,
 *     none left out - never a scene at a time.
 *
 * ⚠ The environment rule (the maintainer, 2026-10-08, task #187). On Linux too, the
 * build host itself sets the pages in DejaVu Sans and the chain's Playwright
 * container in Liberation Sans: a run taken outside the chain
 * (PUBLISH_ENVIRONMENT; `SHOTS_ENVIRONMENT` unset is "local") is refused,
 * since the chain takes every scene. `outsideChain` (`accept
 * --outside-chain`) lets a person take such a run on purpose; accept then
 * still prints environmentNote.
 */
export function acceptRefusal(manifest, review, { publishPlatform = PUBLISH_PLATFORM, outsideChain = false } = {}) {
  if (!review) return `there is no run to accept (${REVIEW_REL}) - run pnpm shots first`;
  if (review.failure) return `the run failed: ${review.failure}`;
  if (review.clock === 'real') {
    return (
      'this run was taken on the real clock (SHOTS_REAL_CLOCK=1): its pictures show the dates and times of the day it ran, ' +
      'and every one of them would change again the next time. Take them on the scene clock (e2e/shots/clock.mjs).'
    );
  }
  if (review.platform !== publishPlatform) {
    return (
      `this run was taken on ${review.platform}, and the published set is taken on ${publishPlatform} - the build host: ` +
      'a picture from here beside the published ones puts two typefaces in one README. Look at this run, then take the ' +
      `pictures in the build host's test chain (CHAIN_EXTRAS=shots on a targeted run, or the nightly run's shots job) and accept that run.`
    );
  }
  if (manifest.platform && review.platform !== manifest.platform && !(review.mode === 'all' && review.complete)) {
    return (
      `the published set was taken on ${manifest.platform} and this run on ${review.platform}: ` +
      'mixing them puts two typefaces in one README. Take them where the published set was taken, ' +
      `or replace the set whole with pnpm shots --all on ${review.platform}.`
    );
  }
  const env = review.environment ?? 'local';
  if (env !== PUBLISH_ENVIRONMENT && !outsideChain) {
    return (
      `this run was taken in "${env}", and the published set is taken in "${PUBLISH_ENVIRONMENT}" - the build host's test chain ` +
      '(its Playwright image and fontconfig): a picture from here reads in another typeface. Take them there - ' +
      'CHAIN_EXTRAS=shots bash scripts/chain/run.sh --profile targeted --src <this checkout> - and accept that run; ' +
      'or, to accept this one on purpose, add --outside-chain.'
    );
  }
  return '';
}

// ── reading the published pictures back ──────────────────────────────────────

/** GET a URL, `{ ok, status, bytes?, why? }`; never throws. */
export async function fetchBytes(url, { timeoutMs = 30_000, fetchImpl = globalThis.fetch } = {}) {
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), timeoutMs);
  try {
    const res = await fetchImpl(url, { signal: ctl.signal, redirect: 'follow', headers: { 'user-agent': 'filex-shots' } });
    if (!res.ok) return { ok: false, status: res.status, why: `HTTP ${res.status}` };
    return { ok: true, status: res.status, bytes: Buffer.from(await res.arrayBuffer()) };
  } catch (err) {
    return { ok: false, status: 0, why: err?.name === 'AbortError' ? `no answer in ${timeoutMs / 1000} s` : String(err?.message ?? err) };
  } finally {
    clearTimeout(timer);
  }
}

/** Runs `fn` over `items`, `limit` at a time; results in order. */
export async function mapLimit(items, limit, fn) {
  const out = new Array(items.length);
  let next = 0;
  const worker = async () => {
    while (next < items.length) {
      const i = next++;
      out[i] = await fn(items[i], i);
    }
  };
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
  return out;
}

/**
 * Is every `{ url, sha256 }` published - answering 200 with exactly those
 * bytes? `[{ url, ok, why }]`. A 200 with other bytes is a failure too: a
 * name that carries a hash and serves something else is the one thing this
 * scheme must never do.
 */
export async function verifyPublished(entries, { concurrency = 6, fetchImpl, timeoutMs } = {}) {
  return mapLimit(entries, concurrency, async ({ url, sha256: want }) => {
    const r = await fetchBytes(url, { fetchImpl, timeoutMs });
    if (!r.ok) return { url, ok: false, why: r.why };
    const got = sha256(r.bytes);
    return got === want ? { url, ok: true, why: '' } : { url, ok: false, why: `serves other bytes (sha256 ${got.slice(0, HASH_CHARS)}…)` };
  });
}
