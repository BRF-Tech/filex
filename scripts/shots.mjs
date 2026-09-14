#!/usr/bin/env node
// `pnpm shots` — every screenshot of this release, from the build in this
// working tree, in one command.
//
//   pnpm shots                          build → verify → shoot → sync → contact sheet
//   pnpm shots --only sidenav,capture   a subset (no site sync, no leftover check)
//   pnpm shots --skip packages,web      reuse builds you trust (the verify step still runs)
//   pnpm shots --no-build [--binary p]  shoot an existing binary (still verified)
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
//      seventy PNGs. → one contact sheet, every picture of the run with its path
//      and the script that took it, printed at the end.
//
// Steps, stopping loudly at the first failure:
//
//   build     pnpm run build:packages → build:web → sync:embed → go build
//             (scripts/lib/go-build.mjs: native Go, or WSL on a Windows box)
//   verify    the binary is booted and every file of web/dist and of the web
//             component bundle is fetched back from it and compared
//   shoot     every shot script in e2e/shots/ — found, not listed: a file that
//             imports @playwright/test and that no sibling imports is a script
//   sync      node scripts/sync-site-assets.mjs, then --check
//   sheet     e2e/.artifacts/shots/contact-sheet.html
//   cleanup   every process the run started, by marker, then listed again
//
// ⚠ The release folder is named once, in e2e/shots/release.mjs.

import { spawn, spawnSync } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import fs from 'node:fs';
import { createRequire } from 'node:module';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { SHOTS_RELEASE, SHOTS_ROOT, SHOTS_ROOT_REL } from '../e2e/shots/release.mjs';
import { checkEmbeddedUI, freePort } from './check-embed.mjs';
import { goBuild } from './lib/go-build.mjs';
import { findShotScripts } from './lib/shot-scripts.mjs';
import { RUN_MARKER, describeProcess, killProcess, listProcesses, runProcesses, sweepRun } from './lib/procs.mjs';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const SHOTS_DIR = path.join(REPO, 'e2e', 'shots');
const ARTIFACTS = path.join(REPO, 'e2e', '.artifacts', 'shots');
const IS_WIN = process.platform === 'win32';
const RUN_PREFIX = 'filex-shots-run-';
const toRepo = (p) => path.relative(REPO, p).split(path.sep).join('/');

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
  console.log(fs.readFileSync(fileURLToPath(import.meta.url), 'utf8').split('\n').slice(1, 8).join('\n').replace(/^\/\/ ?/gm, ''));
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
          goBuild({ cwd: path.join(REPO, 'backend'), pkg: './cmd/filex', out: runBin, log: say });
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

function scriptEnv(runBin, runTmp, port) {
  const env = {};
  for (const [k, v] of Object.entries(process.env)) {
    // ⚠ A developer's shell must not reach the instances: SHOTS_URL would point
    // a script at somebody else's server, SHOTS_ALLOW_SKIP would let a shot go
    // missing, and any FILEX_* (a database URL, a config file) would be spread
    // into every filex the scripts boot.
    if (/^(FILEX_|NOTIFY_|E2E_)/i.test(k)) continue;
    if (/^SHOTS_/i.test(k) && k.toUpperCase() !== 'SHOTS_VERBOSE') continue;
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

// ── the contact sheet ───────────────────────────────────────────────────────
const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c]);

function pngSize(file) {
  const b = Buffer.alloc(24);
  const fd = fs.openSync(file, 'r');
  fs.readSync(fd, b, 0, 24, 0);
  fs.closeSync(fd);
  return b.toString('ascii', 12, 16) === 'IHDR' ? `${b.readUInt32BE(16)}×${b.readUInt32BE(20)}` : '?';
}

function writeContactSheet({ meta, results, orphans, duplicates, verdict }) {
  fs.mkdirSync(ARTIFACTS, { recursive: true });
  const sheet = path.join(ARTIFACTS, 'contact-sheet.html');
  const href = (abs) => path.relative(ARTIFACTS, abs).split(path.sep).map(encodeURIComponent).join('/');
  const dupOf = new Map();
  for (const group of duplicates) for (const f of group) dupOf.set(f, group.filter((g) => g !== f));
  const card = (abs, script) => {
    const rel = path.relative(SHOTS_ROOT, abs).split(path.sep).join('/');
    const twins = dupOf.get(abs);
    return `<figure class="card${twins ? ' warn' : ''}"><a href="${href(abs)}" target="_blank" rel="noopener"><img loading="lazy" src="${href(abs)}" alt="${esc(rel)}"></a>
<figcaption><code>${esc(rel)}</code><span>${esc(script)} · ${pngSize(abs)} · ${(fs.statSync(abs).size / 1024).toFixed(0)} KB</span>${
      twins ? `<b>byte-identical to ${twins.map((t) => esc(path.relative(SHOTS_ROOT, t).split(path.sep).join('/'))).join(', ')}</b>` : ''
    }</figcaption></figure>`;
  };
  const total = results.reduce((n, r) => n + r.files.length, 0);
  const sections = results
    .map((r) => {
      const state = r.notRun ? 'not run' : r.ok ? 'passed' : r.timedOut ? 'timed out' : `failed (exit ${r.code})`;
      return `<section><h2><span class="pill ${r.notRun ? 'idle' : r.ok ? 'ok' : 'bad'}">${esc(state)}</span> e2e/shots/${esc(r.file)}</h2>
<p class="sub">${r.files.length} picture(s)${r.checks ? ` · ${esc(r.checks)}` : ''}${r.ms ? ` · ${(r.ms / 1000).toFixed(0)} s` : ''}${
        r.log ? ` · <a href="${href(r.log)}">log</a>` : ''
      }</p>
<div class="grid">${r.files.map((f) => card(f, r.file)).join('\n')}</div></section>`;
    })
    .join('\n');
  const html = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Contact sheet · ${esc(SHOTS_RELEASE)}</title>
<style>
:root{color-scheme:light dark;--bg:#f6f7f9;--fg:#15171c;--mut:#5b6270;--card:#fff;--line:#dde1e7;--ok:#1d7a46;--bad:#b42318;--warn:#b54708;--chk:#eceff3}
@media (prefers-color-scheme:dark){:root{--bg:#0f1115;--fg:#e8eaee;--mut:#9aa1ad;--card:#171a20;--line:#2a2f38;--chk:#1f232b}}
body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.5 system-ui,-apple-system,Segoe UI,sans-serif;padding:24px 16px 64px}
main{max-width:1600px;margin:0 auto}h1{font-size:22px;margin:0 0 4px}h2{font-size:16px;margin:32px 0 2px;display:flex;gap:10px;align-items:center}
.sub,.meta{color:var(--mut);margin:0 0 12px}.size{position:sticky;top:0;z-index:1;background:var(--bg);padding:6px 0;margin:0;color:var(--mut)}
main:has(#big:checked) .grid{grid-template-columns:1fr}main:has(#big:checked) .card img{height:auto}.meta code{color:var(--fg)}
.verdict{padding:12px 14px;border-radius:8px;border:1px solid var(--line);background:var(--card);margin:12px 0;white-space:pre-wrap;font:12.5px/1.5 ui-monospace,Consolas,monospace}
.verdict.bad{border-color:var(--bad)}.verdict.warn{border-color:var(--warn)}
.pill{font-size:12px;padding:1px 8px;border-radius:99px;color:#fff;background:var(--mut)}.pill.ok{background:var(--ok)}.pill.bad{background:var(--bad)}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(340px,1fr));gap:14px}
.card{margin:0;background:var(--card);border:1px solid var(--line);border-radius:8px;overflow:hidden}.card.warn{border-color:var(--warn)}
.card img{display:block;width:100%;height:300px;object-fit:contain;cursor:zoom-in;background:repeating-conic-gradient(var(--chk) 0 25%,transparent 0 50%) 0 0/16px 16px}
figcaption{padding:8px 10px;display:grid;gap:2px;border-top:1px solid var(--line)}figcaption code{font-size:12.5px;word-break:break-all}
figcaption span{color:var(--mut);font-size:12px}figcaption b{color:var(--warn);font-size:12px;font-weight:600}
</style></head><body><main>
<h1>${esc(SHOTS_ROOT_REL)} — ${total} picture(s) from this run</h1>
<p class="size"><label><input type="checkbox" id="big"> full width — read every string: English, current, nothing covering it</label></p>
<p class="meta">${esc(meta.when)} · <code>${esc(meta.git)}</code> · binary <code>${esc(meta.binary)}</code>${partial ? ' · <b>partial run (--only)</b>' : ''}</p>
<div class="verdict${verdict.ok ? '' : ' bad'}">${esc(verdict.text)}</div>
${orphans.length ? `<div class="verdict bad">${orphans.length} picture(s) in ${esc(SHOTS_ROOT_REL)} were NOT written by this run — no script takes them any more, so they show an older product:\n${orphans.map((o) => '  ' + esc(path.relative(SHOTS_ROOT, o).split(path.sep).join('/'))).join('\n')}</div>` : ''}
${duplicates.length ? `<div class="verdict warn">${duplicates.length} set(s) of byte-identical pictures — two captions, one image. Check that each shows what its name claims.</div>` : ''}
${sections}
${orphans.length ? `<section><h2><span class="pill bad">not written</span> left over in ${esc(SHOTS_ROOT_REL)}</h2><div class="grid">${orphans.map((f) => card(f, 'no script')).join('\n')}</div></section>` : ''}
</main></body></html>
`;
  fs.writeFileSync(sheet, html);
  return sheet;
}

// ── main ────────────────────────────────────────────────────────────────────
async function main() {
  const t0 = Date.now();
  banner(`pnpm shots · ${SHOTS_RELEASE}${partial ? ` · only ${only.join(', ')}` : ''}`);

  const found = findShotScripts(SHOTS_DIR);
  if (found.stray.length) {
    throw new Refusal(
      `e2e/shots/${found.stray.join(', e2e/shots/')}: neither a shot script (it does not import @playwright/test) nor a ` +
        'module a shot script imports. Make it one or the other, or move it out of e2e/shots/.',
    );
  }
  const unknown = only.filter((o) => !found.scripts.includes(`${o}.mjs`));
  if (unknown.length) throw new Refusal(`--only ${unknown.join(', ')}: no such shot script (${found.scripts.join(', ')})`);
  const scripts = partial ? found.scripts.filter((f) => only.includes(f.replace(/\.mjs$/, ''))) : found.scripts;
  say(`shot scripts: ${found.scripts.join(', ')}  (modules: ${found.modules.join(', ')})`);

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
  const runBin = path.join(runDir, IS_WIN ? 'filex.exe' : 'filex');
  say(`run directory ${runDir}`);

  build(runBin);

  banner('verify: does the binary serve the UI that was just built?');
  const baseEnv = scriptEnv(runBin, runTmp, 0);
  const embed = await checkEmbeddedUI({ binary: runBin, workDir: runTmp, env: baseEnv, log: say });
  if (!embed.ok) {
    throw new Refusal(
      `the binary does NOT carry the current UI — refusing to take a single screenshot of it.\n${embed.report}`,
    );
  }
  say('✓ the binary serves web/dist and the web component bundle byte for byte');

  fs.rmSync(ARTIFACTS, { recursive: true, force: true });
  fs.mkdirSync(path.join(ARTIFACTS, 'logs'), { recursive: true });
  const results = scripts.map((file) => ({ file, files: [], ok: false, notRun: true }));
  let failure = null;
  for (const r of results) {
    banner(`shoot: e2e/shots/${r.file}`);
    const before = pngState();
    const port = await freePort();
    const log = path.join(ARTIFACTS, 'logs', r.file.replace(/\.mjs$/, '.log'));
    const res = await runScript(r.file, scriptEnv(runBin, runTmp, port), log);
    Object.assign(r, res, { notRun: false, log, ok: res.code === 0 });
    const after = pngState();
    r.files = [...after].filter(([f, s]) => before.get(f) !== s).map(([f]) => f).sort();
    const { killed, survivors } = sweepRun({ dir: runDir, marker: RUN_ID });
    if (killed.length) {
      say(`${r.file} left ${killed.length} process(es) running after it exited — ended:`);
      for (const p of killed) say(`    ${describeProcess(p)}`);
    }
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

  // Every picture in the release folder this run did not write is a picture of
  // an older product — a renamed shot, or one a script stopped taking.
  const written = new Set(results.flatMap((r) => r.files));
  const orphans = failure || partial ? [] : [...pngState().keys()].filter((f) => !written.has(f)).sort();
  const bySha = new Map();
  for (const f of written) {
    const h = createHash('sha256').update(fs.readFileSync(f)).digest('hex');
    bySha.set(h, [...(bySha.get(h) ?? []), f]);
  }
  const duplicates = [...bySha.values()].filter((g) => g.length > 1);

  if (!failure && !partial) {
    banner('sync: site/assets from the release folder');
    if (!fs.existsSync(path.join(REPO, 'site', 'assets'))) {
      say('site/ is not part of this checkout (the public export withholds it) — nothing to sync');
    } else {
      for (const args of [[], ['--check']]) {
        const r = spawnSync(process.execPath, [path.join(REPO, 'scripts', 'sync-site-assets.mjs'), ...args], { cwd: REPO, stdio: 'inherit' });
        if (r.status !== 0) {
          failure = `node scripts/sync-site-assets.mjs ${args.join(' ')} exited ${r.status}`;
          break;
        }
      }
    }
  }
  if (!failure && orphans.length) {
    failure = `${orphans.length} picture(s) in ${SHOTS_ROOT_REL} were not written by this run (listed on the contact sheet) — delete them or make a script take them`;
  }

  const git = spawnSync('git', ['log', '-1', '--format=%h %s'], { cwd: REPO, encoding: 'utf8' }).stdout.trim();
  const dirty = spawnSync('git', ['status', '--porcelain'], { cwd: REPO, encoding: 'utf8' }).stdout.split('\n').filter(Boolean).length;
  const verdictText = [
    failure ? `✗ ${failure}` : `✓ ${written.size} picture(s), every script passed${partial ? ' (partial run)' : ', site assets in sync'}`,
    '',
    'UI check — the binary served back:',
    embed.report,
  ].join('\n');
  const sheet = writeContactSheet({
    meta: {
      when: new Date().toISOString().replace('T', ' ').slice(0, 19) + 'Z',
      git: `${git}${dirty ? ` (+${dirty} uncommitted)` : ''}`,
      binary: `${skip.has('backend') ? path.resolve(value('binary') ?? path.join(REPO, 'bin', IS_WIN ? 'filex.exe' : 'filex')) : 'built by this run'} · sha256 ${createHash('sha256').update(fs.readFileSync(runBin)).digest('hex').slice(0, 12)}`,
    },
    results,
    orphans,
    duplicates,
    verdict: { ok: !failure, text: verdictText },
  });

  banner('cleanup');
  const { survivors } = finalSweep('end of run');
  const left = runProcesses({ dir: runDir, marker: RUN_ID });
  say(`processes left from this run: ${left.length + survivors.length}`);

  banner(failure ? 'FAILED' : 'done');
  say(`contact sheet: ${sheet}`);
  say(`             ${pathToFileURL(sheet).href}`);
  if (duplicates.length) say(`⚠ ${duplicates.length} set(s) of byte-identical pictures — see the sheet`);
  say(`${((Date.now() - t0) / 60_000).toFixed(1)} min`);
  if (failure) throw new Refusal(failure);
  if (left.length + survivors.length) throw new Refusal('processes of this run are still running (listed above)');
  say('Now LOOK at the contact sheet — every picture, in English, showing what its name says — then commit the folder.');
}

try {
  await main();
} catch (err) {
  console.error(`\n[shots] ✗ ${err instanceof Refusal ? err.message : err.stack}`);
  process.exitCode = 1;
}
