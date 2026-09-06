// Retakes every screenshot the README shows — in English, against the build in
// this working tree.
//
// It exists because the alternative is what happened: the share dialog picture
// was two releases out of date (no download limit) and the markdown viewer
// picture had Turkish buttons in it. A screenshot is a promise about what the
// product looks like now, so retaking them is a release step
// (docs/CONTRIBUTING.md → Release process), and a release step has to be one
// command.
//
//   node e2e/shots/capture.mjs        (from the repo root)
//
// Environment:
//   FILEX_BIN        path to a filex binary (default: ../bin/filex[.exe])
//   SHOTS_URL        use an ALREADY-RUNNING instance instead of spawning one
//   SHOTS_STORAGE    where to write the demo fixtures (this machine's view)
//   SHOTS_MOUNT      the same directory as the SERVER sees it (differs when the
//                    server runs in a VM/WSL/container; defaults to SHOTS_STORAGE)
//   SHOTS_OUT        output directory (default: ../docs/screenshots)
//   SHOTS_KEEP=1     leave the instance running for poking around
//   SHOTS_PLUGIN_BIN a prebuilt example plugin (else: go build, then WSL)
//   SHOTS_ALLOW_SKIP=1  do not fail when a shot could not be taken
//
// Every shot is taken with the UI language pinned to English three ways over:
// the browser locale, the stored preference and the server default. Getting a
// half-Turkish dialog into the repo took one of those being unset.

import { execFileSync, spawn } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from '@playwright/test';
import { seedFixtures } from './fixtures.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO = resolve(HERE, '../..');

const PORT = Number(process.env.SHOTS_PORT ?? 5298);
const URL = process.env.SHOTS_URL ?? `http://127.0.0.1:${PORT}`;
const OUT = process.env.SHOTS_OUT ?? join(REPO, 'docs/screenshots');
const STORAGE = process.env.SHOTS_STORAGE ?? join(tmpdir(), 'filex-shots-storage');
const MOUNT = process.env.SHOTS_MOUNT ?? STORAGE;
const DATA = process.env.SHOTS_DATA ?? join(tmpdir(), 'filex-shots-data');
// The host the share links in the screenshots are shown with. The instance is
// reached at URL (a loopback address); a README picture of a link reading
// `http://127.0.0.1:5298/s/…` teaches the reader nothing, so the PUBLIC url —
// which is what the link actually renders from — gets a presentable value.
const PUBLIC_URL = process.env.SHOTS_PUBLIC_URL ?? 'https://files.example.com';
const EMAIL = 'demo@demo.com';
const PASSWORD = 'demo-shots';

const log = (...a) => console.log('•', ...a);

// Shots this run was asked for and could not take. ⚠ A skipped shot is a
// FAILURE, not a warning: the repo keeps whatever picture is already committed,
// so the release ships an old screenshot that reads as a current promise. This
// is exactly how admin-plugins.png stayed six releases out of date.
// SHOTS_ALLOW_SKIP=1 opts out, deliberately, for a partial local run.
const skipped = [];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const firstLine = (err) => String(err.message).split(String.fromCharCode(10))[0];

// ── the instance ──────────────────────────────────────────────────────────
function defaultBin() {
  for (const p of [join(REPO, 'bin/filex'), join(REPO, 'bin/filex.exe')]) {
    if (existsSync(p)) return p;
  }
  return null;
}

async function waitForHealth(deadlineMs = 30_000) {
  const until = Date.now() + deadlineMs;
  while (Date.now() < until) {
    try {
      const r = await fetch(`${URL}/healthz`);
      if (r.ok) return;
    } catch {
      /* not up yet */
    }
    await sleep(300);
  }
  throw new Error(`no healthy instance at ${URL}`);
}

// binPath is the binary this run can drive from the command line (for the
// thumbnail backfill). Null when pointed at an instance we did not start.
let binPath = null;

async function boot() {
  if (process.env.SHOTS_URL) {
    log(`using the instance already running at ${URL}`);
    binPath = process.env.FILEX_BIN ?? null;
    await waitForHealth();
    return null;
  }
  const bin = process.env.FILEX_BIN ?? defaultBin();
  if (!bin) {
    throw new Error('no filex binary — run `pnpm run build:all` or set FILEX_BIN');
  }
  rmSync(DATA, { recursive: true, force: true });
  mkdirSync(DATA, { recursive: true });
  binPath = bin;
  log(`booting ${bin} on :${PORT}`);
  // ⚠ `filex serve` takes NO flags — listen address and data dir come from the
  // environment (FILEX_LISTEN / FILEX_DATA_DIR). Passing --listen exits with
  // "unknown flag" before anything starts.
  const proc = spawn(bin, ['serve'], {
    env: {
      ...process.env,
      FILEX_LISTEN: `127.0.0.1:${PORT}`,
      FILEX_DATA_DIR: DATA,
      FILEX_ADMIN_EMAIL: EMAIL,
      FILEX_ADMIN_PASSWORD: PASSWORD,
      FILEX_DEFAULT_LOCALE: 'en',
      FILEX_PUBLIC_URL: PUBLIC_URL,
      // ⚠ The protocol listeners are on for the shots instance, and on a
      // throwaway port each. The connection guide renders a red "this endpoint
      // is switched off" banner otherwise — true for a default install, and a
      // picture of the release's headline feature captioned "switched off" is
      // not what the README should show.
      FILEX_SFTP: '1',
      FILEX_SFTP_ADDR: '127.0.0.1:0',
      FILEX_FTPS: '1',
      FILEX_FTPS_ADDR: '127.0.0.1:0',
      FILEX_NFS: '1',
      FILEX_NFS_ADDR: '127.0.0.1:0',
      FILEX_SECRET_KEY: 'screenshots-only-key-not-a-real-secret',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  proc.stdout.on('data', (d) => process.env.SHOTS_VERBOSE && process.stdout.write(d));
  proc.stderr.on('data', (d) => process.env.SHOTS_VERBOSE && process.stderr.write(d));
  await waitForHealth();
  return proc;
}

// ── seeding ───────────────────────────────────────────────────────────────
async function api(token, path, init = {}) {
  const res = await fetch(`${URL}${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(init.headers ?? {}),
    },
  });
  return res;
}

async function seed() {
  // SHOTS_SKIP_SEED keeps an already-prepared instance as it is — the second
  // pass after an out-of-band `filex thumb backfill` (a server running in a VM
  // cannot be driven from here) would otherwise wipe the fixtures it just
  // rendered thumbnails for.
  if (process.env.SHOTS_SKIP_SEED) {
    log('reusing the instance as-is (SHOTS_SKIP_SEED=1)');
    const res = await api(null, '/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ email: EMAIL, password: PASSWORD }),
    });
    if (!res.ok) throw new Error(`login failed: ${res.status}`);
    return (await res.json()).token;
  }
  log(`seeding fixtures into ${STORAGE}`);
  seedFixtures(STORAGE);

  const login = await api(null, '/api/auth/login', {
    method: 'POST',
    body: JSON.stringify({ email: EMAIL, password: PASSWORD }),
  });
  if (!login.ok) throw new Error(`login failed: ${login.status} ${await login.text()}`);
  const { token } = await login.json();

  // English on the account too, so anything rendered server-side agrees with
  // the browser.
  await api(token, '/api/auth/profile', {
    method: 'PATCH',
    body: JSON.stringify({ locale: 'en', display_name: 'Demo' }),
  });

  const existing = await (await api(token, '/api/admin/storages')).json();
  for (const s of Array.isArray(existing) ? existing : []) {
    if (s.name === 'demo') await api(token, `/api/admin/storages/${s.id}`, { method: 'DELETE' });
  }
  const made = await api(token, '/api/admin/storages', {
    method: 'POST',
    body: JSON.stringify({
      name: 'demo',
      driver: 'local',
      mount_path: MOUNT,
      config: { root: MOUNT },
      sync_mode: 'manual',
      sync_interval_s: 0,
      enabled: true,
      read_only: false,
    }),
  });
  if (!made.ok) throw new Error(`storage create failed: ${made.status} ${await made.text()}`);

  const storage = await made.json();

  // Index the folders so the grid has nodes…
  for (const p of ['demo://', 'demo://Photos', 'demo://Documents']) {
    await api(token, `/api/files/manager?q=index&path=${encodeURIComponent(p)}`);
  }
  // …and run a sync, which is what enqueues the thumbnail jobs. Listing alone
  // does not: the grid renders generic icons and the hero shot looks like a
  // product with no previews.
  await api(token, `/api/admin/storages/${storage.id}/sync`, { method: 'POST' });
  return token;
}

/**
 * backfillThumbs renders thumbnails for files that were placed on disk rather
 * than uploaded.
 *
 * ⚠ The pipeline runs on UPLOAD (handlers/manager.go). Fixtures written
 * straight into the storage root are indexed by a sync but never dispatched,
 * so without this the hero shot is a grid of generic file icons — which is
 * exactly what it looked like the first time this script ran.
 */
function backfillThumbs(bin) {
  if (!bin) {
    log('⚠ no local binary — run `filex thumb backfill` yourself for thumbnails');
    return;
  }
  return new Promise((resolve) => {
    const p = spawn(bin, ['thumb', 'backfill'], {
      env: { ...process.env, FILEX_DATA_DIR: DATA },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    p.stdout.on('data', (d) => process.env.SHOTS_VERBOSE && process.stdout.write(d));
    p.on('close', () => resolve());
  });
}

/** Revokes any link already on a file, so the dialog's "Existing links" list
 *  shows the one this run just made rather than a pile from earlier runs. */
async function clearShares(token, path) {
  const res = await api(token, `/api/files/share?path=${encodeURIComponent(path)}`);
  const body = await res.json().catch(() => ({}));
  for (const s of body.shares ?? []) {
    await api(token, `/api/files/share/${s.uuid}`, { method: 'DELETE' });
  }
}

/** Waits until the photo tiles have real thumbnails — a grid of generic icons
 *  is not the screenshot anybody wants. */
async function waitForThumbs(token, tries = 60) {
  for (let i = 0; i < tries; i++) {
    const res = await api(token, `/api/files/manager?q=index&path=${encodeURIComponent('demo://Photos')}`);
    const body = await res.json().catch(() => ({}));
    const files = (body.files ?? []).filter((f) => f.type === 'file');
    const ready = files.filter((f) => f.thumb_url);
    if (files.length && ready.length >= Math.min(6, files.length)) {
      log(`thumbnails ready (${ready.length}/${files.length})`);
      return;
    }
    await sleep(500);
  }
  log('⚠ thumbnails never finished — the grid shot will show icons');
}

// ── the shots ─────────────────────────────────────────────────────────────
async function newContext(browser, scheme, height = 940) {
  const ctx = await browser.newContext({
    viewport: { width: 1600, height },
    deviceScaleFactor: 2,
    locale: 'en-US',
    colorScheme: scheme,
  });
  // The stored preference wins over browser detection — pin it before any app
  // code runs.
  await ctx.addInitScript(() => {
    localStorage.setItem('filex.locale', 'en');
    // ⚠ The exact key the explorer checks (FileExplorer.vue → TOUR_LS_KEY).
    // Guessing it wrong leaves the onboarding tour open, and the tour's
    // backdrop swallows every click the capture needs to make.
    localStorage.setItem('filex.tourDone', '1');
    // …and the desktop-app promo banner, which otherwise parks itself across
    // the bottom of every explorer shot.
    localStorage.setItem('filex.installPrompt.dismissed', '1');
  });
  return ctx;
}

// ⚠ By id, and waiting for the DASHBOARD specifically. Matching the fields by
// label picked up the wrong control and the submit never fired — and because
// the login page's own URL already contains "/admin/", a `waitForURL(/\/admin\//)`
// resolved instantly and every later step ran signed OUT.
async function signIn(page) {
  await page.goto(`${URL}/admin/login`);
  await page.fill('#email', EMAIL);
  await page.fill('#password', PASSWORD);
  await page.click('button[type="submit"]');
  await page.waitForURL(/\/admin\/(dashboard|explore)/, { timeout: 20_000 });
}

async function dismissTour(page) {
  for (let i = 0; i < 6; i++) {
    const skip = page.locator('.fe-tour button', { hasText: /skip|close|got it/i });
    if ((await skip.count()) === 0) return;
    await skip.first().click().catch(() => {});
    await sleep(200);
  }
}

// The explorer lives at /admin/explore; ?storage= opens it inside one storage
// rather than on the storage list. Sub-folders are reached the way a user
// reaches them — by opening the row.
async function openExplorer(page, folder = '') {
  await page.goto(`${URL}/admin/explore?storage=demo`);
  await page.waitForSelector('.fe-list__row, .fe-grid__item', { timeout: 25_000 });
  await dismissTour(page);
  await sleep(800);
  if (folder) {
    await page.evaluate((name) => {
      const row = [...document.querySelectorAll('.fe-list__row, .fe-grid__item')].find((r) =>
        (r.textContent ?? '').includes(name),
      );
      row?.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    }, folder);
    await sleep(1500);
  }
}

async function setGridView(page) {
  // The toolbar renders every action twice — once in an aria-hidden measuring
  // strip that still has a box. Click only the real one.
  await page.evaluate(() => {
    const real = [...document.querySelectorAll('button')].filter(
      (b) => !b.closest('.fe-toolbar__measure') && !b.closest('[aria-hidden="true"]'),
    );
    const grid = real.find((b) => /grid/i.test(b.getAttribute('title') ?? b.getAttribute('aria-label') ?? ''));
    grid?.click();
  });
  await sleep(900);
}

async function shot(target, name) {
  mkdirSync(OUT, { recursive: true });
  const path = join(OUT, name);
  await target.screenshot({ path });
  log(`wrote ${name}`);
}

/**
 * wslCrossBuild builds the example plugin for Windows from inside WSL and puts
 * it where BOTH sides can see it — the repo's own gitignored bin/ — returning
 * the Windows path, or null.
 *
 * ⚠ Platform-specific on purpose, and the only kind that is allowed: this is
 * about where the Go toolchain lives on a Windows workstation (in WSL, while
 * node runs on Windows), not about filex behaving differently anywhere.
 */
function wslCrossBuild(outPath) {
  if (process.platform !== 'win32') return null;
  const m = /^([A-Za-z]):[\\/](.*)$/.exec(REPO);
  if (!m) return null;
  const repoWsl = `/mnt/${m[1].toLowerCase()}/${m[2].replace(/\\/g, '/')}`;
  const name = basename(outPath);
  const winOut = join(REPO, 'bin', name);
  mkdirSync(join(REPO, 'bin'), { recursive: true });
  try {
    execFileSync(
      'wsl',
      ['-e', 'bash', '-lc',
       `cd ${repoWsl}/backend && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 ` +
       `go build -trimpath -o ${repoWsl}/bin/${name} ./examples/plugin-memfs`],
      { stdio: 'pipe' },
    );
  } catch (err) {
    log(`WSL cross-build failed: ${firstLine(err)}`);
    return null;
  }
  return existsSync(winOut) ? winOut : null;
}

/**
 * installExamplePlugin builds backend/examples/plugin-memfs and installs it
 * through the very API the admin page uses, so the plugins screenshot shows a
 * REAL running plugin rather than an empty table.
 *
 * The binary is built for the machine this script runs on, because that is
 * the machine running the instance being shot. With no Go toolchain it says
 * so and returns false: the shot is then skipped rather than showing an empty
 * table that claims to be the feature.
 */
async function installExamplePlugin(token) {
  // SHOTS_PLUGIN_BIN: an already-built plugin, for the same reason
  // FILEX_BIN exists. On this Windows workstation the Go toolchain lives
  // in WSL, so `go build` here is ENOENT while a binary cross-built there
  // is perfectly good.
  let out = process.env.SHOTS_PLUGIN_BIN ?? null;
  if (out && !existsSync(out)) {
    // Told exactly where the binary is and it is not there: that is a typo to
    // fix, not a shot to quietly drop.
    throw new Error(`SHOTS_PLUGIN_BIN does not exist: ${out}`);
  }
  if (!out) {
  out = join(tmpdir(), process.platform === 'win32' ? 'filex-shot-memfs.exe' : 'filex-shot-memfs');
  try {
    execFileSync('go', ['build', '-o', out, './examples/plugin-memfs'], {
      cwd: join(REPO, 'backend'),
      env: { ...process.env, CGO_ENABLED: '0' },
      stdio: 'pipe',
    });
  } catch (err) {
    log(`go build: ${firstLine(err)} - trying the WSL toolchain`);
    const cross = wslCrossBuild(out);
    if (!cross) return false;
    out = cross;
  }
  }
  const form = new FormData();
  form.append('name', 'memfs');
  form.append('file', new Blob([readFileSync(out)]), 'memfs');
  const res = await fetch(`${URL}/api/admin/plugins`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${token}` },
    body: form,
  });
  if (!res.ok) {
    log(`plugin install failed (${res.status}) - skipping that shot`);
    return false;
  }
  // Starting is asynchronous: the row says "starting" until the handshake
  // and the describe are done, and a picture of that shows nothing.
  for (let i = 0; i < 40; i++) {
    const list = await api(token, '/api/admin/plugins');
    const body = await list.json().catch(() => ({}));
    const row = (body.plugins ?? [])[0];
    if (row?.state === 'running') return true;
    if (row?.state === 'refused') {
      log(`plugin refused: ${row.state_error ?? ''}`);
      return false;
    }
    await sleep(250);
  }
  log('plugin never reached running - skipping that shot');
  return false;
}

async function run() {
  const proc = await boot();
  try {
    const token = await seed();
    await backfillThumbs(binPath);
    // SHOTS_SEED_ONLY prepares an instance and stops. Two reasons to want it:
    // a server that has to be reached over a network cannot have its
    // `thumb backfill` driven from here, and the shots themselves leave traces
    // (created and revoked links) that would otherwise show up as the
    // dashboard's "recent activity" on the next run.
    if (process.env.SHOTS_SEED_ONLY) {
      log('seeded — rerun with SHOTS_SKIP_SEED=1 to capture');
      return;
    }
    await waitForThumbs(token);

    const browser = await chromium.launch();

    // The demo landing lives on an instance booted with FILEX_DEMO_MODE=true —
    // it REPLACES the login page, so it cannot be captured in the same pass as
    // everything else. Boot a demo-mode instance and run with SHOTS_DEMO=1.
    if (process.env.SHOTS_DEMO) {
      const dctx = await newContext(browser, 'light', 1000);
      const dpage = await dctx.newPage();
      await dpage.goto(`${URL}/admin/login`);
      await dpage.waitForSelector('text=/Open the demo|demo/i', { timeout: 15_000 });
      await sleep(1500);
      await shot(dpage, 'demo-landing.png');
      await dctx.close();
      await browser.close();
      return;
    }

    // 1 + 2 — the hero: the explorer's thumbnail grid, both schemes.
    for (const scheme of ['light', 'dark']) {
      const ctx = await newContext(browser, scheme, 620);
      const page = await ctx.newPage();
      await signIn(page);
      await openExplorer(page, 'Photos');
      await setGridView(page);
      await shot(page, `explorer-grid-${scheme}.png`);
      await ctx.close();
    }

    const ctx = await newContext(browser, 'light');
    const page = await ctx.newPage();
    await signIn(page);

    // 3 — the operator panel. Taken FIRST: the share and preview steps below
    // land in the audit feed, and a dashboard whose "recent activity" is this
    // script's own share.create/share.delete churn tells the reader nothing.
    await page.goto(`${URL}/admin/dashboard`);
    await page.waitForSelector('.card', { timeout: 15_000 });
    await sleep(1200);
    await shot(page, 'admin-dashboard.png');

    // 3a - Plugins. A storage driver filex does not ship is the headline of
    // this release, and a picture of the empty table would show none of it,
    // so the example plugin is really installed and really running here.
    if (await installExamplePlugin(token)) {
      await page.goto(`${URL}/admin/plugins`);
      await page.waitForSelector('[data-testid="plugin-memfs"]', { timeout: 15_000 });
      await sleep(600);
      await shot(page, 'admin-plugins.png');
    } else {
      skipped.push('admin-plugins.png');
    }

    // 3b — the connection guide. filex being reachable AS S3, SFTP, FTPS and
    // NFS is the largest thing in this release and the README had no picture of
    // it; the guide is also the honest one to show, because it is built from
    // the live deployment rather than being a template with angle brackets.
    //
    // ⚠ SFTP on purpose: it is the protocol most readers already have a client
    // for, and its page shows both halves — the credential panel above and the
    // real commands below.
    await page.goto(`${URL}/admin/connections`);
    await page.waitForSelector('[data-testid="tab-connect"]', { timeout: 15_000 });
    await page.locator('[data-testid="tab-connect"]').click();
    await page.locator('[data-testid="guide-protocol"]').selectOption('sftp');
    await page.waitForSelector('[data-testid="guide-facts"]', { timeout: 15_000 });
    await sleep(800);
    await shot(page, 'connections-guide.png');

    // 4 — the share dialog, showing what it can actually do today: a PIN, an
    // expiry, a DOWNLOAD LIMIT and the one-line curl for the finished link.
    await clearShares(token, 'demo://Photos/aurora.png');
    await openExplorer(page, 'Photos');
    await page.locator('.fe-list__row, .fe-grid__item').first().click();
    await sleep(400);
    await page.evaluate(() => {
      const el = [...document.querySelectorAll('button, [role="menuitem"], .fe-ctx__item')]
        .filter((e) => !e.closest('.fe-toolbar__measure') && !e.closest('[aria-hidden="true"]'))
        .find((e) => /Share \/ Permissions/i.test(e.textContent ?? ''));
      el?.click();
    });
    await page.waitForSelector('.fx-perm-modal', { timeout: 10_000 });
    await page.locator('.fx-perm-tab', { hasText: /^Link$/ }).click();
    await sleep(400);
    await page.evaluate(() => {
      const label = [...document.querySelectorAll('.fx-perm-modal label')].find((l) =>
        /Download limit/i.test(l.textContent ?? ''),
      );
      const sel = label?.querySelector('select');
      if (sel) {
        sel.value = '3';
        sel.dispatchEvent(new Event('change', { bubbles: true }));
      }
    });
    await page.locator('.fx-perm-create').click();
    await page.waitForSelector('.fx-perm-cli', { timeout: 10_000 });
    await sleep(600);
    await shot(page.locator('.fx-perm-modal'), 'share-modal.png');
    await page.keyboard.press('Escape');

    // 4 — the markdown VIEWER.
    // ⚠ Neither a double-click nor Space: double-clicking a .md opens the
    // standalone EDITOR (the first attempt at this shot was a raw-text pane),
    // and Space is the quick-look peek, which caught itself mid-animation.
    // The read-only preview is the context menu's "Preview" — the same route a
    // reader would take.
    await openExplorer(page, '');
    const readme = page.locator('.fe-list__row, .fe-grid__item').filter({ hasText: 'README.md' }).first();
    await readme.click({ button: 'right' });
    await sleep(500);
    // ⚠ Not /^Preview$/ — every context-menu item is prefixed with an emoji
    // ("👁Preview"), so an anchored match finds nothing.
    await page.locator('.fe-ctx__item', { hasText: 'Preview' }).first().click();
    // The CARD, not its body: the viewer's title bar and its actions are part
    // of what the picture is claiming to show.
    await page.waitForSelector('.fe-modal__card', { timeout: 15_000 });
    await sleep(2000);
    await shot(page.locator('.fe-modal__card').first(), 'viewer-markdown.png');
    await page.keyboard.press('Escape');

    await ctx.close();
    await browser.close();

    if (skipped.length && !process.env.SHOTS_ALLOW_SKIP) {
      throw new Error(
        `not taken: ${skipped.join(', ')} — the committed picture stays, which ` +
        `means the release would ship a stale one. Fix the cause, or set ` +
        `SHOTS_ALLOW_SKIP=1 if you meant a partial run.`,
      );
    }
  } finally {
    if (proc && !process.env.SHOTS_KEEP) proc.kill();
    if (process.env.SHOTS_KEEP) log('instance left running (SHOTS_KEEP=1)');
  }
}

run().catch((e) => {
  console.error('✗', e.message);
  process.exit(1);
});
