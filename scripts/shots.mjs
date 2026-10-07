#!/usr/bin/env node
// `pnpm shots` — the screenshots that changed, from the build in this working
// tree, in one command; only what changed is put in front of a person.
//
//   pnpm shots                          take the scenes whose inputs changed → compare → review sheet
//   pnpm shots --all                    every scene, whatever changed (the nightly run, a major release)
//   pnpm shots --only sidenav,capture   these scenes; the rest stand as published or as last taken
//   pnpm shots --skip packages,web      reuse builds you trust (the verify step still runs)
//   pnpm shots --no-build [--binary p]  shoot an existing binary (still verified)
//   pnpm shots --without-apps           leave out the scenes that need app builds (CI's default)
//   then: node scripts/shots-site.mjs upload  &&  node scripts/shots-site.mjs accept --looked
//
// ⚠⚠ Why this exists. The screenshots for v0.41.0 were taken three times by
// hand. Not for want of automation — every script in e2e/shots/ is Playwright —
// but for three reasons, and each step below removes one of them:
//
//   1. THE SCRIPTS ROTTED SILENTLY. The UI was rebuilt and five of six scripts
//      still encoded the old layout; nothing ran them until a release needed
//      pictures. → CI runs this command on every tag and on demand (the
//      `shots` job / the Screenshots workflow), so a script that no longer fits
//      the product turns a job red instead of a release night.
//   2. NOTHING CHECKED WHICH BUILD WAS PHOTOGRAPHED. The binary embeds the UI
//      that `sync:embed` last copied; it was rebuilt without that step, carried
//      a 16-hour-old interface, passed every API check, and 71 screenshots of a
//      product that no longer existed were taken — then reported as bugs.
//      → the whole chain is built in order, and scripts/check-embed.mjs proves
//      the binary serves web/dist byte for byte BEFORE anything is shot.
//   3. NOBODY LOOKED UNTIL IT WAS TOO LATE. A half-translated dialog, or a shot
//      with the onboarding tour across it, is only caught by a person scrolling
//      seventy PNGs. → one contact sheet, printed at the end.
//
// ⚠⚠ And why it takes only what changed (task #176, 2026-10-06). Retaking all
// 150+ pictures into a new docs/screenshots/vX.Y.Z/ every release put ~40 MB
// in the repository each time and 150 pictures in front of a person when ~25
// had changed. So:
//
//   · each scene has a DIGEST of what its pictures depend on
//     (scripts/lib/shots-site.mjs → sceneDigest: its own source and imports,
//     the fixtures, the product sources or the INPUTS it declares, the locale,
//     the platform, the release number). A scene whose digest is the one
//     recorded with the published set, or the one the last run took it with,
//     is not taken again; when no scene is left, nothing is even built.
//   · every picture taken is compared with the published one PIXEL BY PIXEL
//     (scripts/lib/png.mjs). Below the threshold it is the same picture, and
//     the published file stands. Above it, the sheet shows it beside the
//     published one with a map of where it moved.
//   · the pictures live on filex.sh/shots under names that carry their content
//     hash, not in git; e2e/shots/manifest.json names the current file of
//     each. `node scripts/shots-site.mjs` publishes the reviewed ones and
//     points README, the docs and the site at them. e2e/shots/README.md.
//
// Steps, stopping loudly at the first failure:
//
//   plan      every scene's digest → shoot, published, kept or left out
//   build     pnpm run build:packages → build:web → sync:embed → go build
//             (scripts/lib/go-build.mjs: native Go, or WSL on a Windows box)
//   verify    the binary is booted and every file of web/dist and of the web
//             component bundle is fetched back from it and compared
//   shoot     the scenes to shoot — found, not listed: a file in e2e/shots/
//             that imports @playwright/test and that no sibling imports
//   compare   every picture taken against the published one (pixels)
//   stage     the changed and new pictures, under their published names
//   sheet     e2e/.artifacts/shots/contact-sheet.html + review.json
//   cleanup   every process the run started, by marker, then listed again
//
// ⚠ The scenes that photograph ONLYOFFICE's own editor (a script calling
// `documentServer()`: csvoffice.mjs) need a real document server, named by
// SHOTS_ONLYOFFICE_URL, SHOTS_ONLYOFFICE_JWT and SHOTS_ONLYOFFICE_CALLBACK_HOST.
// They are treated exactly like the app scenes below: refused before the build
// when it is not named, left out in CI and with --without-apps.
//
// ⚠⚠ The scenes that photograph an APP (a script calling `findApp('sign')` —
// apps.mjs and signing.mjs) need that app's build, which is not in this tree:
// filex-sign and filex-convert are their own repositories. Locally a missing
// build is a refusal, before the build step rather than an hour into it.
// In CI (`CI` set, as GitHub Actions and GitLab both set it) those scenes are
// LEFT OUT instead, loudly — in the log, the verdict and the contact sheet —
// and their published pictures stand. `--with-apps` overrides that;
// `--without-apps` asks for it anywhere. Why CI does not fetch or build the
// apps (decided for v0.43.0, when both repositories are published first):
//   · at the tag there may be nothing to fetch — the apps' first public
//     releases are cut at the same release, and a CI that depends on another
//     repository's tag turns "tag filex" into "tag three things in order";
//   · the converter scene also needs the conversion engines, which it gets
//     from Docker (e2e/shots/scene.mjs → bootInstance({ engines })), and the
//     private CI's runner cannot run Docker;
//   · CI's pictures are an artefact that catches a script that no longer fits
//     the product; the pictures that ship are taken on the build host
//     (docs/CONTRIBUTING.md → Release process, step 2), where the sibling
//     checkouts exist.
//
// ⚠ The published set is taken on Linux, the build host (PUBLISH_PLATFORM, the
// owner's decision of 2026-10-06): fonts are the system's, and a README with
// pictures from two systems has two typefaces. A run anywhere else still
// takes, compares and shows its pictures - to look at - but stages nothing for
// the site, and `shots-site.mjs accept` refuses it.

import { spawn, spawnSync } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import fs from 'node:fs';
import { createRequire } from 'node:module';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { SHOTS_LDFLAGS, SHOTS_RELEASE, SHOTS_ROOT, SHOTS_ROOT_REL } from '../e2e/shots/release.mjs';
import { checkEmbeddedUI, freePort } from './check-embed.mjs';
import { APP_LOCATIONS, locateApp } from '../e2e/helpers/app-locations.mjs';
import { removeRunContainers } from './lib/containers.mjs';
import { goBuild } from './lib/go-build.mjs';
import { decodePng, diffImages, diffOverlay, pngSize as pngDims } from './lib/png.mjs';
import {
  DOCUMENT_SERVER_VARS,
  appScenesLeftOutBy,
  documentServerFor,
  findShotScripts,
  planAppScenes,
  scriptNeeds,
} from './lib/shot-scripts.mjs';
import {
  CACHE_REL,
  MANIFEST_REL,
  PUBLISH_PLATFORM,
  REVIEW_REL,
  decideScenes,
  diffThresholds,
  fetchBytes,
  fileHasher,
  gitFiles,
  isChanged,
  mapLimit,
  publishedName,
  publishedUrl,
  readManifest,
  sceneDigest,
  scriptInputs,
  sha256,
} from './lib/shots-site.mjs';
import { RUN_MARKER, describeProcess, killProcess, listProcesses, runProcesses, sweepRun } from './lib/procs.mjs';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const SHOTS_DIR = path.join(REPO, 'e2e', 'shots');
const ARTIFACTS = path.join(REPO, 'e2e', '.artifacts', 'shots');
const REVIEW = path.join(REPO, ...REVIEW_REL.split('/'));
const CACHE = path.join(REPO, ...CACHE_REL.split('/'));
const PUBLISH = path.join(ARTIFACTS, 'publish');
const DIFF = path.join(ARTIFACTS, 'diff');
const IS_WIN = process.platform === 'win32';
const RUN_PREFIX = 'filex-shots-run-';
const toRepo = (p) => path.relative(REPO, p).split(path.sep).join('/');
const toName = (p) => path.relative(SHOTS_ROOT, p).split(path.sep).join('/');

// ── argv ────────────────────────────────────────────────────────────────────
const argv = process.argv.slice(2);
const has = (name) => argv.includes(`--${name}`);
const value = (name) => {
  const i = argv.indexOf(`--${name}`);
  return i > -1 && argv[i + 1] && !argv[i + 1].startsWith('--') ? argv[i + 1] : undefined;
};
const list = (name) => (value(name) ?? '').split(',').map((s) => s.trim()).filter(Boolean);

const BUILD_STEP_IDS = ['packages', 'web', 'embed', 'backend'];
if (has('help')) {
  console.log(fs.readFileSync(fileURLToPath(import.meta.url), 'utf8').split('\n').slice(1, 12).join('\n').replace(/^\/\/ ?/gm, ''));
  process.exit(0);
}
const skip = new Set(has('no-build') ? BUILD_STEP_IDS : list('skip'));
if (value('binary')) skip.add('backend');
for (const s of skip) {
  if (!BUILD_STEP_IDS.includes(s)) {
    console.error(`--skip ${s}: not a build step (${BUILD_STEP_IDS.join(', ')})`);
    process.exit(2);
  }
}
const only = list('only').map((s) => s.replace(/\.mjs$/, ''));
const timeoutMs = Number(value('timeout-min') ?? 30) * 60_000;
const partial = only.length > 0;
const mode = has('all') ? 'all' : partial ? 'only' : 'incremental';
const whyWithoutApps = appScenesLeftOutBy({ withApps: has('with-apps'), withoutApps: has('without-apps') });
const withoutApps = whyWithoutApps !== '';

const say = (msg) => console.log(`[shots] ${msg}`);
const banner = (msg) => console.log(`\n[shots] ━━ ${msg} ${'━'.repeat(Math.max(4, 64 - msg.length))}`);

class Refusal extends Error {}

// ── the run's private directory, and every process in it ────────────────────
const RUN_ID = randomBytes(4).toString('hex');
let runDir = null;
let sweptAtExit = false;
const activeChildren = new Set();

function finalSweep(reason) {
  if (!runDir || sweptAtExit) return { killed: [], survivors: [] };
  sweptAtExit = true;
  for (const pid of activeChildren) killProcess(pid);
  const res = sweepRun({ dir: runDir, marker: RUN_ID, roots: [...activeChildren] });
  if (res.killed.length) {
    say(`cleanup (${reason}): ended ${res.killed.length} process(es) the run left behind`);
    for (const p of res.killed) say(`    ${describeProcess(p)}`);
  }
  // ⚠ A container is not a process of this run: the sweep above cannot see
  // it. Every one a scene starts is labelled with the run id instead
  // (scripts/lib/containers.mjs).
  const boxes = removeRunContainers(RUN_ID);
  if (boxes.length) say(`cleanup (${reason}): removed container(s) the run left behind: ${boxes.join(', ')}`);
  try {
    fs.rmSync(runDir, { recursive: true, force: true, maxRetries: 10, retryDelay: 300 });
  } catch (err) {
    say(`could not remove ${runDir}: ${err.message}`);
  }
  return res;
}

process.on('exit', () => finalSweep('exit'));
for (const sig of ['SIGINT', 'SIGTERM', 'SIGHUP', ...(IS_WIN ? ['SIGBREAK'] : [])]) {
  process.on(sig, () => {
    console.error(`\n[shots] ${sig} — stopping and cleaning up`);
    finalSweep(sig);
    process.exit(130);
  });
}

/**
 * Leftovers of runs that died without cleaning up (a killed terminal, a
 * crashed machine) are ended before a new run starts — and a run that is
 * genuinely still going is a reason to refuse: two runs write the same folder.
 */
function sweepStaleRuns() {
  const tmp = os.tmpdir();
  const dirs = fs.readdirSync(tmp).filter((n) => n.startsWith(RUN_PREFIX));
  if (dirs.length === 0) return;
  const procs = listProcesses();
  for (const name of dirs) {
    const dir = path.join(tmp, name);
    let info = null;
    try {
      info = JSON.parse(fs.readFileSync(path.join(dir, 'run.json'), 'utf8'));
    } catch {
      /* half-created */
    }
    const live = info && procs.find((p) => p.pid === info.pid && /shots\.mjs/.test(p.cmd));
    if (live) {
      throw new Refusal(
        `another \`pnpm shots\` is running (pid ${info.pid}, started ${info.started}) and writes the same ` +
          `${SHOTS_ROOT_REL}. Wait for it, or stop it.`,
      );
    }
    const { killed, survivors } = sweepRun({ dir, marker: info?.id ?? '', procs });
    if (killed.length) say(`ended ${killed.length} process(es) left by an earlier run that did not finish (${name})`);
    const boxes = removeRunContainers(info?.id ?? '');
    if (boxes.length) say(`removed container(s) left by an earlier run that did not finish: ${boxes.join(', ')}`);
    if (survivors.length) throw new Refusal(`could not end processes of an earlier run: ${survivors.map(describeProcess).join('; ')}`);
    fs.rmSync(dir, { recursive: true, force: true, maxRetries: 5, retryDelay: 300 });
  }
}

// ── build ───────────────────────────────────────────────────────────────────
function pnpm(args) {
  const r = spawnSync('pnpm', args, {
    cwd: REPO,
    stdio: 'inherit',
    shell: IS_WIN,
    env: { NODE_OPTIONS: '--max-old-space-size=4096', ...process.env },
  });
  if (r.status !== 0) throw new Refusal(`pnpm ${args.join(' ')} exited ${r.status ?? r.error?.message}`);
}

function build(runBin) {
  const steps = [
    { id: 'packages', label: 'pnpm run build:packages', run: () => pnpm(['run', 'build:packages']) },
    { id: 'web', label: 'pnpm run build:web', run: () => pnpm(['run', 'build:web']) },
    { id: 'embed', label: 'pnpm run sync:embed', run: () => pnpm(['run', 'sync:embed']) },
    {
      id: 'backend',
      label: 'go build ./cmd/filex',
      run: () => {
        try {
          goBuild({ cwd: path.join(REPO, 'backend'), pkg: './cmd/filex', out: runBin, ldflags: SHOTS_LDFLAGS, log: say });
        } catch (err) {
          throw new Refusal(err.message);
        }
      },
    },
  ];
  for (const step of steps) {
    if (skip.has(step.id)) {
      say(`skipped: ${step.label} (--skip ${step.id})`);
      continue;
    }
    banner(step.label);
    step.run();
  }
  if (skip.has('backend')) {
    const src = path.resolve(value('binary') ?? path.join(REPO, 'bin', IS_WIN ? 'filex.exe' : 'filex'));
    if (!fs.existsSync(src)) throw new Refusal(`no binary at ${src} — drop --skip backend, or pass --binary`);
    // ⚠ Copied, not used in place: the copy is what makes every process it
    // starts recognisable as this run's (scripts/lib/procs.mjs), and nothing
    // can rebuild it underneath the run.
    fs.copyFileSync(src, runBin);
    say(`binary: ${src} (copied into the run)`);
  }
}

// ── the shot scripts ────────────────────────────────────────────────────────
function pngState() {
  const state = new Map();
  if (!fs.existsSync(SHOTS_ROOT)) return state;
  const walk = (d) => {
    for (const e of fs.readdirSync(d, { withFileTypes: true })) {
      const full = path.join(d, e.name);
      if (e.isDirectory()) walk(full);
      else if (e.name.endsWith('.png')) {
        const st = fs.statSync(full);
        state.set(full, `${st.mtimeMs}:${st.size}`);
      }
    }
  };
  walk(SHOTS_ROOT);
  return state;
}

const APP_DIR_VARS = new Set([
  'FILEX_SIGN_APP_DIR',
  'FILEX_CONVERT_APP_DIR',
  // The langpack scene's three packs (e2e/shots/langpack.mjs): without them a
  // pack built for this release could not be pointed at, and the scene found
  // the checkouts' last published pack instead (2026-09-28, v0.49.0: 94%).
  'FILEX_LANG_ES_APP_DIR',
  'FILEX_LANG_DE_APP_DIR',
  'FILEX_LANG_FR_APP_DIR',
]);
// Where the converter scene's engines come from (e2e/shots/scene.mjs →
// bootInstance), and the document server the ONLYOFFICE scenes are taken
// against (scene.mjs → documentServer; scripts/lib/shot-scripts.mjs). They
// choose a machine to run on; none of them can make a picture go missing,
// which is what the SHOTS_* filter below is for: a scene that needs a document
// server and has none is refused or left out before anything is built.
// SHOTS_STORE_HOST: the name store.mjs serves its store under (https, port 443).
// SHOTS_REAL_CLOCK=1: the scenes' browsers on the real clock instead of the
// scene's (e2e/shots/clock.mjs) - to tell a scene that breaks on the clock
// from one that breaks on its own; its pictures then carry today's dates.
const SHOTS_PASS = new Set(['SHOTS_VERBOSE', 'SHOTS_ENGINES', 'SHOTS_ENGINES_IMAGE', 'SHOTS_LINUX_BIN', 'SHOTS_STORE_HOST', 'SHOTS_REAL_CLOCK', ...DOCUMENT_SERVER_VARS]);

function scriptEnv(runBin, runTmp, port) {
  const env = {};
  for (const [k, v] of Object.entries(process.env)) {
    // ⚠ A developer's shell must not reach the instances: SHOTS_URL would point
    // a script at somebody else's server, SHOTS_ALLOW_SKIP would let a shot go
    // missing, SHOTS_OUT would write a picture where nothing compares it, and
    // any FILEX_* (a database URL, a config file) would be spread into every
    // filex the scripts boot.
    //
    // The one exception is where the APP BUILDS are: FILEX_SIGN_APP_DIR and
    // FILEX_CONVERT_APP_DIR are read by the scripts that photograph the apps
    // (e2e/shots/scene.mjs → findApp). They configure nothing in a filex, and
    // scene.mjs's boot strips every FILEX_* before it spawns one regardless.
    if (/^(FILEX_|NOTIFY_|E2E_)/i.test(k) && !APP_DIR_VARS.has(k.toUpperCase())) continue;
    if (/^SHOTS_/i.test(k) && !SHOTS_PASS.has(k.toUpperCase())) continue;
    if (/^(TEMP|TMP|TMPDIR)$/i.test(k)) continue;
    env[k] = v;
  }
  return {
    ...env,
    TEMP: runTmp,
    TMP: runTmp,
    TMPDIR: runTmp,
    FILEX_BIN: runBin,
    NOTIFY_BIN: runBin,
    SHOTS_PORT: String(port),
    NOTIFY_PORT: String(port),
    [RUN_MARKER]: RUN_ID,
  };
}

function runScript(file, env, logFile) {
  return new Promise((resolve) => {
    const started = Date.now();
    const out = fs.createWriteStream(logFile);
    const child = spawn(process.execPath, [path.join(SHOTS_DIR, file)], {
      cwd: REPO,
      env,
      stdio: ['ignore', 'pipe', 'pipe'],
      windowsHide: true,
    });
    activeChildren.add(child.pid);
    let text = '';
    const tee = (stream) => (d) => {
      stream.write(d);
      out.write(d);
      text = (text + d).slice(-200_000);
    };
    child.stdout.on('data', tee(process.stdout));
    child.stderr.on('data', tee(process.stderr));
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      say(`${file} ran past ${timeoutMs / 60_000} min — ending it`);
      killProcess(child.pid);
    }, timeoutMs);
    child.on('close', (code, signal) => {
      clearTimeout(timer);
      activeChildren.delete(child.pid);
      out.end();
      const checks = [...text.matchAll(/(\d+)\/(\d+) checks passed/g)].at(-1);
      resolve({
        code: timedOut ? null : code,
        signal,
        timedOut,
        pid: child.pid,
        ms: Date.now() - started,
        checks: checks ? `${checks[1]}/${checks[2]} checks passed` : null,
      });
    });
  });
}

// ── comparing with the published set ───────────────────────────────────────

/**
 * The published file of a picture, on this disk: from the cache, or fetched
 * from the site once and kept there (by sha256, so a cache hit is exactly the
 * published bytes). `{ file }`, or `{ note }` saying why there is none.
 */
async function baselineOf(name, pub, base) {
  const cached = path.join(CACHE, `${pub.sha256}.png`);
  if (fs.existsSync(cached) && sha256(fs.readFileSync(cached)) === pub.sha256) return { file: cached };
  const url = publishedUrl(base, name, pub.sha256);
  const r = await fetchBytes(url);
  if (r.ok && sha256(r.bytes) === pub.sha256) {
    fs.mkdirSync(CACHE, { recursive: true });
    fs.writeFileSync(cached, r.bytes);
    return { file: cached };
  }
  return { note: r.ok ? `${url} serves other bytes than the manifest names` : `could not fetch ${url} (${r.why})` };
}

/**
 * What one picture taken by this run is, against the manifest:
 *   new      the manifest has no picture of that name
 *   same     the published file's bytes, or pixels within the threshold
 *   changed  pixels past the threshold - or no published file to compare
 *            with, which a person must then look at as if it had changed
 */
async function comparePicture(name, taken, manifest, thresholds) {
  const buf = fs.readFileSync(taken.file);
  const sha = sha256(buf);
  const dims = pngDims(buf) ?? { width: 0, height: 0 };
  const pub = manifest.pictures[name];
  const entry = {
    status: 'new',
    sha256: sha,
    previous: pub?.sha256 ?? null,
    width: dims.width,
    height: dims.height,
    bytes: buf.length,
    scene: taken.scene,
    file: toRepo(taken.file),
  };
  if (!pub) return entry;
  if (pub.sha256 === sha) return { ...entry, status: 'same' };
  const base = await baselineOf(name, pub, manifest.base);
  if (!base.file) return { ...entry, status: 'changed', diff: { note: base.note, baseline: publishedUrl(manifest.base, name, pub.sha256) } };
  try {
    const before = decodePng(fs.readFileSync(base.file));
    const after = decodePng(buf);
    const d = diffImages(before, after, thresholds);
    const status = isChanged(d, thresholds) ? 'changed' : 'same';
    const diff = { pixels: d.pixels, total: d.total, sameSize: d.sameSize, boxes: d.boxes.slice(0, 8), baseline: toRepo(base.file) };
    if (status === 'changed' && d.sameSize) {
      const out = path.join(DIFF, ...name.replace(/\.png$/, '.diff.png').split('/'));
      fs.mkdirSync(path.dirname(out), { recursive: true });
      fs.writeFileSync(out, diffOverlay(after, d));
      diff.image = toRepo(out);
    }
    if (!d.sameSize) diff.note = `${pub.width}×${pub.height} published, ${dims.width}×${dims.height} now`;
    return { ...entry, status, diff };
  } catch (err) {
    return { ...entry, status: 'changed', diff: { note: `could not compare: ${err.message}`, baseline: toRepo(base.file) } };
  }
}

// ── the contact sheet ───────────────────────────────────────────────────────
const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c]);

function writeContactSheet({ meta, review, results, duplicates, verdict }) {
  fs.mkdirSync(ARTIFACTS, { recursive: true });
  const sheet = path.join(ARTIFACTS, 'contact-sheet.html');
  const href = (p) => (/^https?:\/\//.test(p) ? p : path.relative(ARTIFACTS, path.join(REPO, p)).split(path.sep).map(encodeURIComponent).join('/'));
  const pics = Object.entries(review.pictures);
  const by = (status) => pics.filter(([, p]) => p.status === status).sort(([a], [b]) => a.localeCompare(b));
  const changed = by('changed');
  const added = by('new');
  const same = by('same');
  const dupOf = new Map();
  for (const group of duplicates) for (const f of group) dupOf.set(f, group.filter((g) => g !== f));
  const img = (src, alt) => `<a href="${esc(href(src))}" target="_blank" rel="noopener"><img loading="lazy" src="${esc(href(src))}" alt="${esc(alt)}"></a>`;
  const where = (d) => {
    if (!d) return '';
    if (d.note) return `<b>${esc(d.note)}</b>`;
    const share = ((d.pixels / d.total) * 100).toFixed(d.pixels / d.total < 0.001 ? 3 : 2);
    const boxes = (d.boxes ?? []).map((b) => `${b.w}×${b.h} at ${b.x},${b.y}`).join('; ');
    return `${d.pixels} px differ (${share}%) in ${(d.boxes ?? []).length} region(s)${boxes ? `: ${esc(boxes)}` : ''}`;
  };
  const card = ([name, p], { before = false } = {}) => {
    const twins = dupOf.get(path.join(REPO, p.file ?? ''));
    const frames = [
      ...(before && p.diff?.baseline ? [`<div><span>published</span>${img(p.diff.baseline, `${name} (published)`)}</div>`] : []),
      `<div><span>${before ? 'now' : 'new'}</span>${img(p.file, name)}</div>`,
      ...(before && p.diff?.image ? [`<div><span>what moved</span>${img(p.diff.image, `${name} (difference)`)}</div>`] : []),
    ];
    return `<figure class="card${twins ? ' warn' : ''}"><div class="frames n${frames.length}">${frames.join('')}</div>
<figcaption><code>${esc(name)}</code><span>${esc(p.scene ?? '')} · ${p.width}×${p.height} · ${(p.bytes / 1024).toFixed(0)} KB</span>${
      before ? `<span>${where(p.diff)}</span>` : ''
    }${twins ? `<b>byte-identical to ${twins.map((t) => esc(toRepo(t))).join(', ')}</b>` : ''}</figcaption></figure>`;
  };
  const sceneRows = results
    .map((r) => {
      const s = review.scenes[r.file];
      const state =
        s.action === 'shoot' ? (s.status === 'passed' ? 'taken' : s.status) : s.action === 'left-out' ? 'left out' : s.action === 'not-asked' ? 'not asked' : s.action;
      const cls = s.action === 'shoot' ? (s.status === 'passed' ? 'ok' : 'bad') : 'idle';
      const n = s.action === 'published' ? `${s.published} published` : `${s.pictures.length}`;
      return `<li><span class="pill ${cls}">${esc(state)}</span> <code>e2e/shots/${esc(r.file)}</code> <span class="mut">${esc(s.why ?? '')} · ${n} picture(s)${r.checks ? ` · ${esc(r.checks)}` : ''}${r.ms ? ` · ${(r.ms / 1000).toFixed(0)} s` : ''}${r.log ? ` · <a href="${esc(href(toRepo(r.log)))}">log</a>` : ''}</span></li>`;
    })
    .join('\n');
  const toLook = changed.length + added.length + review.removed.length;
  const html = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Contact sheet · ${esc(SHOTS_RELEASE)}</title>
<style>
:root{color-scheme:light dark;--bg:#f6f7f9;--fg:#15171c;--mut:#5b6270;--card:#fff;--line:#dde1e7;--ok:#1d7a46;--bad:#b42318;--warn:#b54708;--chk:#eceff3}
@media (prefers-color-scheme:dark){:root{--bg:#0f1115;--fg:#e8eaee;--mut:#9aa1ad;--card:#171a20;--line:#2a2f38;--chk:#1f232b}}
body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.5 system-ui,-apple-system,Segoe UI,sans-serif;padding:24px 16px 64px}
main{max-width:1800px;margin:0 auto}h1{font-size:22px;margin:0 0 4px}h2{font-size:16px;margin:32px 0 8px}
.meta,.mut{color:var(--mut)}.meta{margin:0 0 12px}.size{position:sticky;top:0;z-index:1;background:var(--bg);padding:6px 0;margin:0;color:var(--mut)}
main:has(#big:checked) .grid{grid-template-columns:1fr}main:has(#big:checked) .frames img{height:auto}.meta code{color:var(--fg)}
.verdict{padding:12px 14px;border-radius:8px;border:1px solid var(--line);background:var(--card);margin:12px 0;white-space:pre-wrap;font:12.5px/1.5 ui-monospace,Consolas,monospace}
.verdict.bad{border-color:var(--bad)}.verdict.warn{border-color:var(--warn)}
.pill{font-size:12px;padding:1px 8px;border-radius:99px;color:#fff;background:var(--mut)}.pill.ok{background:var(--ok)}.pill.bad{background:var(--bad)}
ul.scenes{list-style:none;padding:0;margin:0;display:grid;gap:4px}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(520px,1fr));gap:14px}
.card{margin:0;background:var(--card);border:1px solid var(--line);border-radius:8px;overflow:hidden}.card.warn{border-color:var(--warn)}
.frames{display:grid;gap:2px;background:var(--line)}.frames.n2{grid-template-columns:1fr 1fr}.frames.n3{grid-template-columns:1fr 1fr 1fr}
.frames div{background:var(--card);position:relative}.frames span{position:absolute;top:4px;left:4px;font-size:11px;padding:0 6px;border-radius:4px;background:var(--fg);color:var(--bg)}
.frames img{display:block;width:100%;height:280px;object-fit:contain;cursor:zoom-in;background:repeating-conic-gradient(var(--chk) 0 25%,transparent 0 50%) 0 0/16px 16px}
figcaption{padding:8px 10px;display:grid;gap:2px;border-top:1px solid var(--line)}figcaption code{font-size:12.5px;word-break:break-all}
figcaption span{color:var(--mut);font-size:12px}figcaption b{color:var(--warn);font-size:12px;font-weight:600}
details{margin-top:24px}details li{font:12.5px/1.6 ui-monospace,Consolas,monospace}
</style></head><body><main>
<h1>${toLook} picture(s) to look at — ${changed.length} changed, ${added.length} new, ${review.removed.length} removed</h1>
<p class="size"><label><input type="checkbox" id="big"> full width — read every string: English, current, nothing covering it</label></p>
<p class="meta">${esc(meta.when)} · <code>${esc(meta.git)}</code> · ${esc(review.mode)} run on ${esc(review.platform)} (${esc(review.environment)}) · binary <code>${esc(meta.binary)}</code></p>
<div class="verdict${verdict.ok ? '' : ' bad'}">${esc(verdict.text)}</div>
${duplicates.length ? `<div class="verdict warn">${duplicates.length} set(s) of byte-identical pictures — two captions, one image. Check that each shows what its name claims.</div>` : ''}
<h2>Scenes</h2><ul class="scenes">${sceneRows}</ul>
${changed.length ? `<h2>Changed — the published picture, this run's, and what moved</h2><div class="grid">${changed.map((e) => card(e, { before: true })).join('\n')}</div>` : ''}
${added.length ? `<h2>New — no published picture of this name</h2><div class="grid">${added.map((e) => card(e)).join('\n')}</div>` : ''}
${
  review.removed.length
    ? `<h2>Removed — published, and no scene takes them any more</h2><div class="grid">${review.removed
        .map((n) => `<figure class="card warn"><div class="frames n1"><div>${img(review.removedUrls[n], n)}</div></div><figcaption><code>${esc(n)}</code></figcaption></figure>`)
        .join('\n')}</div>`
    : ''
}
${
  same.length
    ? `<details><summary>${same.length} picture(s) taken again and unchanged — not to review</summary><ul>${same
        .map(([n, p]) => `<li>${esc(n)}${p.diff ? ` <span class="mut">(${p.diff.pixels} px under the threshold)</span>` : ''}</li>`)
        .join('')}</ul></details>`
    : ''
}
</main></body></html>
`;
  fs.writeFileSync(sheet, html);
  return sheet;
}

// ── main ────────────────────────────────────────────────────────────────────
async function main() {
  const t0 = Date.now();
  if (has('all') && partial) throw new Refusal('--all and --only ask for different things — pick one');
  banner(`pnpm shots · ${SHOTS_RELEASE} · ${mode}${partial ? ` ${only.join(', ')}` : ''}`);

  const found = findShotScripts(SHOTS_DIR);
  if (found.stray.length) {
    throw new Refusal(
      `e2e/shots/${found.stray.join(', e2e/shots/')}: neither a shot script (it does not import @playwright/test) nor a ` +
        'module a shot script imports. Make it one or the other, or move it out of e2e/shots/.',
    );
  }
  const unknown = only.filter((o) => !found.scripts.includes(`${o}.mjs`));
  if (unknown.length) throw new Refusal(`--only ${unknown.join(', ')}: no such shot script (${found.scripts.join(', ')})`);
  const scripts = found.scripts;
  say(`shot scripts: ${scripts.join(', ')}  (modules: ${found.modules.join(', ')})`);

  // ── plan: what each scene reads, and whether that changed ──────────────────
  const needs = new Map(scripts.map((f) => [f, scriptNeeds(SHOTS_DIR, f)]));
  const ds = documentServerFor(process.env);
  let excluded = new Map();
  if (withoutApps) {
    const p = planAppScenes({ needs, withoutApps: true, locate: locateApp, documentServer: ds });
    if (p.refused.length) throw new Refusal(p.refused.join('\n  '));
    excluded = p.excluded;
  }
  const manifest = readManifest(path.join(REPO, MANIFEST_REL), { missingOk: true });
  let previous = null;
  try {
    previous = fs.existsSync(REVIEW) ? JSON.parse(fs.readFileSync(REVIEW, 'utf8')) : null;
  } catch {
    previous = null;
  }
  const hash = fileHasher(REPO);
  const filesMemo = new Map();
  const filesUnder = (prefixes) => {
    const key = prefixes.join('\n');
    if (!filesMemo.has(key)) filesMemo.set(key, gitFiles(REPO, prefixes));
    return filesMemo.get(key);
  };
  const digests = new Map();
  for (const f of scripts) {
    const n = needs.get(f);
    const tokens = n.apps.map((app) => {
      const at = locateApp(app);
      if (!at.present) return `app:${app}:missing`;
      return `app:${app}:${sha256(fs.readFileSync(at.manifestPath))}:${at.wasm ? sha256(fs.readFileSync(at.wasm)) : '-'}`;
    });
    if (n.documentServer) tokens.push(`document-server:${ds.present ? ds.url : 'none'}`);
    digests.set(f, sceneDigest({ repo: REPO, script: f, declared: scriptInputs(SHOTS_DIR, f), tokens, hash, filesUnder }));
  }
  // A scene the last run took stands for this run when its pictures are still
  // in the capture folder, byte for byte as that run recorded them.
  const keptOk = (f, prev) =>
    (prev.pictures ?? []).every((name) => {
      const rec = previous?.pictures?.[name];
      const file = path.join(SHOTS_ROOT, ...name.split('/'));
      return rec && fs.existsSync(file) && sha256(fs.readFileSync(file)) === rec.sha256;
    });
  const plan = decideScenes({ scripts, digests, manifest, previous, mode, only, excluded, keptOk });
  const toShoot = scripts.filter((f) => plan.get(f).action === 'shoot');
  const counts = {};
  for (const p of plan.values()) counts[p.action] = (counts[p.action] ?? 0) + 1;
  say(`plan: ${Object.entries(counts).map(([a, n]) => `${n} ${a}`).join(', ')}`);
  for (const f of scripts) say(`  ${plan.get(f).action.padEnd(9)} ${f} — ${plan.get(f).why}`);
  if (excluded.size) say('  left-out scenes keep their published pictures; theirs are taken on the build host (docs/CONTRIBUTING.md → Release process, step 2)');

  // The scenes that need an app build — checked BEFORE the build step: an hour
  // of building is no way to learn that a sibling checkout is missing.
  if (!withoutApps && toShoot.length) {
    const { refused } = planAppScenes({
      needs: new Map(toShoot.map((f) => [f, needs.get(f)])),
      withoutApps: false,
      locate: locateApp,
      documentServer: ds,
    });
    if (refused.length) {
      const repos = Object.values(APP_LOCATIONS).map((a) => `${a.repo} (${a.env})`).join(', ');
      throw new Refusal(`${refused.join('\n  ')}\n  Build the app — ${repos} — or run with --without-apps to leave these scenes out.`);
    }
  }

  const results = scripts.map((file) => ({ file, ...plan.get(file), files: [], ok: false, ran: false }));
  let failure = null;
  let embed = { report: 'nothing was built: no scene was taken' };
  let runBin = null;

  if (toShoot.length) {
    // Playwright's browser, before an hour of building: the failure is cheap to
    // name now and expensive to meet after the build.
    try {
      const { chromium } = createRequire(path.join(REPO, 'e2e', 'package.json'))('@playwright/test');
      if (!fs.existsSync(chromium.executablePath())) throw new Error(`no browser at ${chromium.executablePath()}`);
    } catch (err) {
      throw new Refusal(`Playwright's Chromium is not installed (${err.message.split('\n')[0]}) — run: pnpm --dir e2e exec playwright install chromium`);
    }

    sweepStaleRuns();
    runDir = fs.mkdtempSync(path.join(os.tmpdir(), RUN_PREFIX));
    fs.writeFileSync(path.join(runDir, 'run.json'), JSON.stringify({ pid: process.pid, id: RUN_ID, started: new Date().toISOString() }));
    const runTmp = path.join(runDir, 'tmp');
    fs.mkdirSync(runTmp);
    runBin = path.join(runDir, IS_WIN ? 'filex.exe' : 'filex');
    say(`run directory ${runDir}`);

    build(runBin);

    banner('verify: does the binary serve the UI that was just built?');
    const baseEnv = scriptEnv(runBin, runTmp, 0);
    embed = await checkEmbeddedUI({ binary: runBin, workDir: runTmp, env: baseEnv, log: say });
    if (!embed.ok) {
      throw new Refusal(`the binary does NOT carry the current UI — refusing to take a single screenshot of it.\n${embed.report}`);
    }
    say('✓ the binary serves web/dist and the web component bundle byte for byte');

    // ⚠ The capture folder and the last review stay: a scene kept from the last
    // run is read from there. Only what this run writes anew is cleared.
    for (const d of [path.join(ARTIFACTS, 'logs'), DIFF, PUBLISH]) fs.rmSync(d, { recursive: true, force: true });
    fs.mkdirSync(path.join(ARTIFACTS, 'logs'), { recursive: true });
    fs.mkdirSync(SHOTS_ROOT, { recursive: true });
    for (const r of results) {
      if (r.action !== 'shoot') continue;
      banner(`shoot: e2e/shots/${r.file}`);
      const before = pngState();
      const port = await freePort();
      const log = path.join(ARTIFACTS, 'logs', r.file.replace(/\.mjs$/, '.log'));
      const res = await runScript(r.file, scriptEnv(runBin, runTmp, port), log);
      Object.assign(r, res, { ran: true, log, ok: res.code === 0 });
      const after = pngState();
      r.files = [...after].filter(([f, s]) => before.get(f) !== s).map(([f]) => f).sort();
      const { killed, survivors } = sweepRun({ dir: runDir, marker: RUN_ID });
      if (killed.length) {
        say(`${r.file} left ${killed.length} process(es) running after it exited — ended:`);
        for (const p of killed) say(`    ${describeProcess(p)}`);
      }
      const boxes = removeRunContainers(RUN_ID);
      if (boxes.length) say(`${r.file} left container(s) running after it exited — removed: ${boxes.join(', ')}`);
      if (survivors.length) {
        failure = `could not end processes ${r.file} left behind: ${survivors.map(describeProcess).join('; ')}`;
        break;
      }
      say(`${r.file}: ${r.ok ? 'passed' : r.timedOut ? 'TIMED OUT' : `FAILED (exit ${r.code ?? r.signal})`} · ${r.files.length} picture(s) · ${(r.ms / 1000).toFixed(0)} s${r.checks ? ` · ${r.checks}` : ''}`);
      if (!r.ok) {
        failure = `e2e/shots/${r.file} ${r.timedOut ? 'timed out' : 'failed'} — see its output above (log: ${toRepo(log)}). Stopping: the rest were not run.`;
        break;
      }
    }
  } else {
    say('nothing to take: every scene stands as published or as the last run took it — no build needed');
    for (const d of [DIFF, PUBLISH]) fs.rmSync(d, { recursive: true, force: true });
  }

  // ── compare: every picture this run stands behind, against the published one
  banner('compare: the pictures against the published set');
  const taken = new Map();
  for (const r of results) {
    if (r.action === 'shoot' && r.ran) for (const f of r.files) taken.set(toName(f), { file: f, scene: r.file });
    if (r.action === 'kept') {
      for (const name of previous.scenes[r.file].pictures ?? []) taken.set(name, { file: path.join(SHOTS_ROOT, ...name.split('/')), scene: r.file });
    }
  }
  const thresholds = diffThresholds();
  const compared = await mapLimit([...taken], 4, async ([name, t]) => [name, await comparePicture(name, t, manifest, thresholds)]);
  const pictures = Object.fromEntries(compared.sort(([a], [b]) => a.localeCompare(b)));

  // Published pictures no scene takes any more: of a scene that ran (or was
  // kept) and did not write them, or - when every scene is accounted for - of
  // a script that is gone.
  const stood = new Set(results.filter((r) => (r.action === 'shoot' && r.ran && r.ok) || r.action === 'kept').map((r) => r.file));
  const complete = !failure && results.every((r) => (r.action === 'shoot' && r.ok) || r.action === 'kept' || r.action === 'published');
  const removed = Object.entries(manifest.pictures)
    .filter(([name, p]) => !taken.has(name) && (stood.has(p.scene) || (complete && !scripts.includes(p.scene))))
    .map(([name]) => name)
    .sort();

  // The capture folder holds what this run stands behind, nothing older.
  const keep = new Set([...taken.values()].map((t) => path.resolve(t.file)));
  let dropped = 0;
  for (const f of pngState().keys()) {
    if (!keep.has(path.resolve(f))) {
      fs.rmSync(f, { force: true });
      dropped++;
    }
  }
  if (dropped) say(`${dropped} older picture(s) dropped from ${SHOTS_ROOT_REL}`);

  // ── stage: the changed and new pictures, under their published names ───────
  // Only where the published set is taken: a picture from another system is
  // for looking at, never for the site (PUBLISH_PLATFORM).
  fs.rmSync(PUBLISH, { recursive: true, force: true });
  const publishable = process.platform === PUBLISH_PLATFORM;
  let staged = 0;
  for (const [name, p] of Object.entries(pictures)) {
    if (!publishable) break;
    if (p.status !== 'new' && p.status !== 'changed') continue;
    const out = path.join(PUBLISH, ...publishedName(name, p.sha256).split('/'));
    fs.mkdirSync(path.dirname(out), { recursive: true });
    fs.copyFileSync(path.join(REPO, p.file), out);
    staged++;
  }

  const bySha = new Map();
  for (const t of taken.values()) {
    if (!fs.existsSync(t.file)) continue;
    const h = createHash('sha256').update(fs.readFileSync(t.file)).digest('hex');
    bySha.set(h, [...(bySha.get(h) ?? []), t.file]);
  }
  const duplicates = [...bySha.values()].filter((g) => g.length > 1);

  const head = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: REPO, encoding: 'utf8' }).stdout.trim();
  const git = spawnSync('git', ['log', '-1', '--format=%h %s'], { cwd: REPO, encoding: 'utf8' }).stdout.trim();
  const dirty = spawnSync('git', ['status', '--porcelain'], { cwd: REPO, encoding: 'utf8' }).stdout.split('\n').filter(Boolean).length;
  const statusOf = (r) => (r.action !== 'shoot' ? null : !r.ran ? 'not run' : r.ok ? 'passed' : r.timedOut ? 'timed out' : 'failed');
  const review = {
    schema: 1,
    when: new Date().toISOString(),
    head,
    git: `${git}${dirty ? ` (+${dirty} uncommitted)` : ''}`,
    release: SHOTS_RELEASE,
    platform: process.platform,
    // The scenes' clock (e2e/shots/clock.mjs): 'real' under SHOTS_REAL_CLOCK=1.
    clock: process.env.SHOTS_REAL_CLOCK === '1' ? 'real' : 'scene',
    // Where on that platform: 'chain' in the build host's test chain
    // (scripts/chain/job/shots.sh), where the published set is taken
    // (scripts/lib/shots-site.mjs PUBLISH_ENVIRONMENT).
    environment: process.env.SHOTS_ENVIRONMENT || 'local',
    mode,
    complete,
    failure,
    base: manifest.base,
    thresholds,
    scenes: Object.fromEntries(
      results.map((r) => [
        r.file,
        {
          action: r.action,
          why: r.why,
          digest: r.digest,
          status: statusOf(r),
          pictures: [...taken].filter(([, t]) => t.scene === r.file).map(([n]) => n).sort(),
          published: Object.values(manifest.pictures).filter((p) => p.scene === r.file).length,
        },
      ]),
    ),
    pictures,
    removed,
    removedUrls: Object.fromEntries(removed.map((n) => [n, publishedUrl(manifest.base, n, manifest.pictures[n].sha256)])),
  };
  fs.mkdirSync(ARTIFACTS, { recursive: true });
  fs.writeFileSync(REVIEW, `${JSON.stringify(review, null, 2)}\n`);

  const tally = (s) => Object.values(pictures).filter((p) => p.status === s).length;
  const toLook = tally('changed') + tally('new') + removed.length;
  const verdictText = [
    failure
      ? `✗ ${failure}`
      : `✓ ${taken.size} picture(s) stood behind: ${tally('changed')} changed, ${tally('new')} new, ${tally('same')} unchanged; ${removed.length} removed`,
    `  scenes: ${Object.entries(counts).map(([a, n]) => `${n} ${a}`).join(', ')}${complete ? '' : ' (not every scene is accounted for: a partial set)'}`,
    `  threshold: more than ${thresholds.pixels} px moved by more than ${thresholds.tolerance}/255`,
    ...(excluded.size ? [`  left out (${whyWithoutApps}): ${[...excluded.keys()].join(', ')} — their published pictures stand`] : []),
    publishable
      ? `  staged for the site: ${staged} file(s) in ${toRepo(PUBLISH)}`
      : `  staged for the site: nothing - this run is on ${process.platform}, the published set is taken on ${PUBLISH_PLATFORM} (the build host)`,
    '',
    'UI check — the binary served back:',
    embed.report,
  ].join('\n');
  const sheet = writeContactSheet({
    meta: {
      when: review.when.replace('T', ' ').slice(0, 19) + 'Z',
      git: review.git,
      binary: !runBin
        ? 'none (nothing was taken)'
        : `${skip.has('backend') ? path.resolve(value('binary') ?? path.join(REPO, 'bin', IS_WIN ? 'filex.exe' : 'filex')) : 'built by this run'} · sha256 ${sha256(fs.readFileSync(runBin)).slice(0, 12)}`,
    },
    review,
    results,
    duplicates,
    verdict: { ok: !failure, text: verdictText },
  });

  banner('cleanup');
  const { survivors } = finalSweep('end of run');
  const left = runDir ? runProcesses({ dir: runDir, marker: RUN_ID }) : [];
  say(`processes left from this run: ${left.length + survivors.length}`);

  banner(failure ? 'FAILED' : 'done');
  say(`contact sheet: ${sheet}`);
  say(`             ${pathToFileURL(sheet).href}`);
  if (duplicates.length) say(`⚠ ${duplicates.length} set(s) of byte-identical pictures — see the sheet`);
  say(`${((Date.now() - t0) / 60_000).toFixed(1)} min`);
  if (failure) throw new Refusal(failure);
  if (left.length + survivors.length) throw new Refusal('processes of this run are still running (listed above)');
  if (!publishable) {
    say(`This run is on ${process.platform}: look at it, but the published set is taken on ${PUBLISH_PLATFORM}, the build host -`);
    say('nothing was staged, and accept refuses it. Take them in its test chain: CHAIN_EXTRAS=shots on a targeted run, or the nightly run.');
  } else if (toLook) {
    say(`Now LOOK at the contact sheet — the ${toLook} changed, new and removed picture(s): English, current, nothing covering them.`);
    say('Then publish them and point every page at them:');
    say('  node scripts/shots-site.mjs upload');
    say('  node scripts/shots-site.mjs accept --looked');
  } else {
    say('Nothing changed: the published pictures stand.');
    if (results.some((r) => r.action === 'shoot' && r.ok)) say('Record what was taken, so the next run skips it: node scripts/shots-site.mjs accept --looked');
  }
}

try {
  await main();
} catch (err) {
  console.error(`\n[shots] ✗ ${err instanceof Refusal ? err.message : err.stack}`);
  process.exitCode = 1;
}
