#!/usr/bin/env node
// What a CI browser part needs before `node e2e/run.mjs`, for every CI that
// runs one (task #181): GitHub's ci.yml and CircleCI's config call this same
// file, so the two can never prepare the suite differently.
//
//   node scripts/ci/browser-prep.mjs apps <dir>
//       The apps the suite installs, into <dir>/<name>: every entry of
//       e2e/helpers/app-locations.mjs. An app with a module (sign, convert)
//       comes from its repository's latest release (plugin.wasm and
//       filex-app.json, which is all a test install reads); a language pack
//       is its manifest and has no build, so it comes from the default
//       branch. Then each is looked up the way the specs look it up, and its
//       FILEX_*_APP_DIR is handed to the steps after this one.
//   node scripts/ci/browser-prep.mjs fonts <dir>
//       The build host's font map (scripts/chain/run.mjs, FONTS_CONF) in
//       <dir>/fontconfig/fonts.conf, Liberation Sans installed when it is
//       missing, and XDG_CONFIG_HOME=<dir> for the steps after this one.
//
// ⚠⚠ No list of its own. Which apps, where the specs look and under which
// variable is app-locations.mjs (the specs and the screenshots read the same
// table); the font map is the chain's. A second list here is how a CI comes
// to skip, or fail on, an app the table gained (CircleCI's first run: every
// app spec failed to load, "FILEX_REQUIRE_WASM_FIXTURE is set and the sign
// app's plugin.wasm is not built", because nothing fetched the apps there).
//
// ⚠ Why the fonts: the Playwright image and `playwright install --with-deps`
// bring CJK faces, and fontconfig then answers sans-serif and system-ui with
// one that sets Latin text wide; the suite's layout checks (a value that must
// fit its card, a label that must fit its button) are calibrated on a Latin
// face and go red on it.
//
// The variables go where the running CI reads them: $GITHUB_ENV (NAME=value)
// on GitHub, $BASH_ENV (export lines, sourced by every later step) on
// CircleCI; with neither, the export lines are printed for `eval`.
// Downloads need no token: the app repositories are public.
//
// Exit 0: everything is in place. 1: an app could not be fetched or is not
// found where the specs look, or the fonts could not be set (named). 2: usage.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

import { APP_LOCATIONS, locateApp } from '../../e2e/helpers/app-locations.mjs';
import { FONTS_CONF } from '../chain/run.mjs';

/** The family every alias of the font map prefers (FONTS_CONF). */
export const LATIN_FACE = 'Liberation Sans';

const GITHUB = 'https://github.com';
const RAW = 'https://raw.githubusercontent.com';

/**
 * Where each app comes from and where it lands: one row per entry of
 * app-locations.mjs, in its order.
 */
export function appPlan(dir, locations = APP_LOCATIONS) {
  return Object.entries(locations).map(([name, spot]) => {
    if (!spot.repo) throw new Error(`app-locations.mjs: ${name} names no repository to fetch it from`);
    if (!spot.env) throw new Error(`app-locations.mjs: ${name} names no variable to find it by`);
    const files = spot.dataOnly
      ? [{ file: 'filex-app.json', url: `${RAW}/${spot.repo}/HEAD/filex-app.json` }]
      : ['plugin.wasm', 'filex-app.json'].map((file) => ({ file, url: `${GITHUB}/${spot.repo}/releases/latest/download/${file}` }));
    return { name, env: spot.env, repo: spot.repo, dataOnly: !!spot.dataOnly, dir: path.resolve(dir, name), files };
  });
}

async function download(url, to, fetchImpl, tries = 3) {
  let last;
  for (let i = 1; i <= tries; i += 1) {
    try {
      const res = await fetchImpl(url, { redirect: 'follow', signal: AbortSignal.timeout?.(180_000) });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const bytes = Buffer.from(await res.arrayBuffer());
      if (bytes.length === 0) throw new Error('an empty file');
      fs.writeFileSync(to, bytes);
      return bytes.length;
    } catch (e) {
      last = e;
      if (i < tries) await new Promise((r) => setTimeout(r, 2000 * i));
    }
  }
  throw new Error(`${url}: ${last?.message ?? last}`);
}

/**
 * Fetch every app into `dir` and prove the specs find it there. Returns the
 * variables to hand on ({ FILEX_SIGN_APP_DIR: <dir>/sign, ... }).
 */
export async function fetchApps(dir, { fetchImpl = globalThis.fetch, log = (s) => console.error(s), tries = 3 } = {}) {
  const vars = {};
  for (const app of appPlan(dir)) {
    fs.rmSync(app.dir, { recursive: true, force: true });
    fs.mkdirSync(app.dir, { recursive: true });
    for (const f of app.files) {
      try {
        await download(f.url, path.join(app.dir, f.file), fetchImpl, tries);
      } catch (e) {
        throw new Error(`${app.repo}: no ${f.file} (${e.message})`);
      }
    }
    let version;
    try {
      version = JSON.parse(fs.readFileSync(path.join(app.dir, 'filex-app.json'), 'utf8')).version;
    } catch (e) {
      throw new Error(`${app.repo}: its filex-app.json is not JSON (${e.message})`);
    }
    // The lookup the specs make (helpers/appPlugin.ts → locateApp), with the
    // variable they will be given: found here, or the run would fail later.
    const before = process.env[app.env];
    process.env[app.env] = app.dir;
    try {
      const found = locateApp(app.name);
      if (!found.present) throw new Error(`${app.repo}: fetched, but the specs would not find it (${found.how})`);
    } finally {
      if (before === undefined) delete process.env[app.env];
      else process.env[app.env] = before;
    }
    vars[app.env] = app.dir;
    log(`${app.repo} ${version ?? '(no version)'} -> ${app.dir}`);
  }
  return vars;
}

/** The font map, where fontconfig looks with XDG_CONFIG_HOME=<dir>. */
export function writeFontsConf(dir) {
  const file = path.join(dir, 'fontconfig', 'fonts.conf');
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, FONTS_CONF);
  return { XDG_CONFIG_HOME: path.resolve(dir) };
}

function fcHas(face) {
  const r = spawnSync('fc-list', [], { encoding: 'utf8' });
  if (r.error) throw new Error(`fc-list: ${r.error.message} (fontconfig is not installed)`);
  return r.stdout.includes(face);
}

/** Liberation Sans, installed the way the runner allows when it is missing. */
function ensureLatinFace(log) {
  if (fcHas(LATIN_FACE)) return;
  const asRoot = process.getuid?.() === 0;
  const cmd = (args) => (asRoot ? ['apt-get', args] : ['sudo', ['apt-get', ...args]]);
  for (const args of [['update', '-qq'], ['install', '-y', '-qq', 'fonts-liberation']]) {
    const [bin, argv] = cmd(args);
    log(`${bin} ${argv.join(' ')}`);
    const r = spawnSync(bin, argv, { stdio: 'inherit', env: { ...process.env, DEBIAN_FRONTEND: 'noninteractive' } });
    if (r.status !== 0) throw new Error(`${bin} ${argv.join(' ')} exited ${r.status ?? r.error?.message}`);
  }
  if (!fcHas(LATIN_FACE)) throw new Error(`${LATIN_FACE} is still not installed`);
}

const shellQuote = (v) => `'${String(v).replace(/'/g, `'\\''`)}'`;

/**
 * How `vars` reach the steps after this one: the file the running CI reads,
 * and what goes in it; with no such file, export lines to print.
 */
export function envAssignments(vars, env = process.env) {
  const entries = Object.entries(vars);
  for (const [k, v] of entries) {
    if (/[\r\n]/.test(String(v))) throw new Error(`${k}: a value with a line break cannot be handed on`);
  }
  if (env.GITHUB_ENV) return { file: env.GITHUB_ENV, text: entries.map(([k, v]) => `${k}=${v}\n`).join('') };
  const text = entries.map(([k, v]) => `export ${k}=${shellQuote(v)}\n`).join('');
  if (env.BASH_ENV) return { file: env.BASH_ENV, text };
  return { file: '', text };
}

function handOn(vars) {
  const { file, text } = envAssignments(vars);
  if (file) fs.appendFileSync(file, text);
  else process.stdout.write(text);
}

const USAGE = 'usage: node scripts/ci/browser-prep.mjs apps <dir> | fonts <dir>';

async function main(argv) {
  const [what, dir, ...rest] = argv;
  if (!dir || rest.length || !['apps', 'fonts'].includes(what)) {
    console.error(USAGE);
    return 2;
  }
  const log = (s) => console.error(s);
  try {
    if (what === 'apps') {
      handOn(await fetchApps(dir, { log }));
    } else {
      ensureLatinFace(log);
      const vars = writeFontsConf(dir);
      handOn(vars);
      const m = spawnSync('fc-match', ['sans-serif'], { encoding: 'utf8', env: { ...process.env, ...vars } });
      const face = (m.stdout || m.stderr || '').trim();
      log(`sans-serif is now: ${face}`);
      if (!face.includes(LATIN_FACE)) throw new Error(`fontconfig still answers sans-serif with ${face || 'nothing'}, not ${LATIN_FACE}`);
    }
    return 0;
  } catch (e) {
    console.error(`browser-prep ${what}: ${e.message}`);
    return 1;
  }
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  main(process.argv.slice(2)).then((code) => process.exit(code));
}
