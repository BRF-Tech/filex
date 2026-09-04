// Screenshots + measurements for the explorer's navigation panel (GitHub #14).
//
// A navigation panel is a layout change, so a passing unit test proves nothing
// about it: the questions are whether the listing keeps a usable width when the
// panel is expanded, whether collapsing gives that width back, whether a phone
// gets a drawer instead of a column, and whether the whole thing is reachable
// from the keyboard. All four are measured here, in a real browser, against a
// real instance — and the same run writes the pictures a reviewer looks at.
//
//   node e2e/shots/sidenav.mjs        (from the repo root)
//
// Environment:
//   FILEX_BIN     binary to run (default: bin/filex.exe on Windows, bin/filex)
//   SHOTS_URL     use an ALREADY-RUNNING instance instead of spawning one
//   SHOTS_OUT     output directory (default: ../docs/screenshots/sidenav)
//   SHOTS_KEEP=1  leave the instance running afterwards
//
// ⚠ Every shot is in English three ways over — browser locale, the stored
//   `filex.locale` preference and the server default. Getting a half-Turkish
//   dialog into the repo took one of those being unset (docs/CONTRIBUTING.md,
//   release step 2). The repo is public and its readers are not Turkish.

import { spawn } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from '@playwright/test';
import { seedFixtures } from './fixtures.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO = resolve(HERE, '../..');

const PORT = Number(process.env.SHOTS_PORT ?? 5297);
// ⚠ 127.0.0.1, not localhost: on Windows `localhost` resolves to ::1 first and
// a server bound to 127.0.0.1 answers that with ECONNREFUSED — which looks
// exactly like a server that failed to start (e2e/README.md).
const URL = process.env.SHOTS_URL ?? `http://127.0.0.1:${PORT}`;
const OUT = process.env.SHOTS_OUT ?? join(REPO, 'docs/screenshots/sidenav');
const DATA = join(tmpdir(), 'filex-sidenav-data');

const ADMIN = { email: 'admin@local', password: 'admin' };
const USER = { email: 'ayse@local', password: 'UserPass1!' };

const log = (...a) => console.log('•', ...a);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const results = [];
function check(name, ok, detail) {
  results.push({ name, ok, detail });
  console.log(`${ok ? '  PASS' : '  FAIL'}  ${name}${detail ? ` — ${detail}` : ''}`);
}

// ── the instance ──────────────────────────────────────────────────────────
function defaultBin() {
  for (const p of [join(REPO, 'bin/filex.exe'), join(REPO, 'bin/filex')]) {
    if (existsSync(p)) return p;
  }
  return null;
}

async function waitForHealth(deadlineMs = 40_000) {
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

let binPath = null;

async function boot() {
  if (process.env.SHOTS_URL) {
    log(`using the instance already running at ${URL}`);
    await waitForHealth();
    return null;
  }
  const bin = process.env.FILEX_BIN ?? defaultBin();
  if (!bin) throw new Error('no filex binary — run `pnpm run build:all` or set FILEX_BIN');
  binPath = bin;
  rmSync(DATA, { recursive: true, force: true });
  mkdirSync(DATA, { recursive: true });
  log(`booting ${bin} on :${PORT}`);
  // ⚠ `filex serve` takes NO flags — listen address and data dir come from the
  // environment. Passing --listen exits with "unknown flag" before anything
  // starts.
  const proc = spawn(bin, ['serve'], {
    env: {
      ...process.env,
      FILEX_LISTEN: `127.0.0.1:${PORT}`,
      FILEX_DATA_DIR: DATA,
      FILEX_ADMIN_EMAIL: ADMIN.email,
      FILEX_ADMIN_PASSWORD: ADMIN.password,
      FILEX_DEFAULT_LOCALE: 'en',
      FILEX_PUBLIC_URL: 'https://files.example.com',
      FILEX_SECRET_KEY: 'sidenav-shots-key-not-a-real-secret',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  proc.stdout.on('data', (d) => process.env.SHOTS_VERBOSE && process.stdout.write(d));
  proc.stderr.on('data', (d) => process.env.SHOTS_VERBOSE && process.stderr.write(d));
  await waitForHealth();
  return proc;
}

// ── API helpers ───────────────────────────────────────────────────────────
async function api(token, path, init = {}) {
  return fetch(`${URL}${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(init.headers ?? {}),
    },
  });
}

async function login(email, password) {
  const res = await api(null, '/api/auth/login', {
    method: 'POST',
    body: JSON.stringify({ email, password }),
  });
  if (!res.ok) throw new Error(`login ${email}: ${res.status} ${await res.text()}`);
  return (await res.json()).token;
}

async function makeStorage(token, name, root, rbac) {
  mkdirSync(root, { recursive: true });
  const res = await api(token, '/api/admin/storages', {
    method: 'POST',
    body: JSON.stringify({
      name,
      driver: 'local',
      mount_path: root,
      // ⚠ The root of a local storage is `config.path`. Sending `root` instead
      // silently resolves every storage to the same ./fileman directory under
      // the server's working dir, and the two storages here read each other's
      // files (e2e/README.md).
      config: { path: root },
      sync_mode: 'manual',
      sync_interval_s: 0,
      enabled: true,
      read_only: false,
      rbac_enabled: !!rbac,
    }),
  });
  if (!res.ok) throw new Error(`storage ${name}: ${res.status} ${await res.text()}`);
  const row = await res.json();
  if (row.config?.path !== root) {
    throw new Error(`storage ${name}: asked for ${root}, server stored ${row.config?.path}`);
  }
  if (!!rbac !== !!row.rbac_enabled) {
    throw new Error(`storage ${name}: rbac_enabled came back ${row.rbac_enabled}`);
  }
  return row;
}

async function indexPath(token, qualified) {
  const res = await api(token, `/api/files/manager?action=index&path=${encodeURIComponent(qualified)}`);
  if (!res.ok) return [];
  const body = await res.json();
  return Array.isArray(body.files) ? body.files : [];
}

// ── the world the shots are taken in ──────────────────────────────────────
async function seed() {
  const adminToken = await login(ADMIN.email, ADMIN.password);
  await api(adminToken, '/api/auth/profile', {
    method: 'PATCH',
    body: JSON.stringify({ locale: 'en', display_name: 'Operator' }),
  });

  // "My files" — an ordinary storage everyone reaches by role.
  const demoRoot = mkdtempSync(join(tmpdir(), 'filex-sidenav-demo-'));
  seedFixtures(demoRoot);
  // …plus a couple of Office documents, because "a regular user wants
  // thumbnails and their documents" is the whole point of the report.
  writeFileSync(join(demoRoot, 'Documents', 'Q3 budget.xlsx'), 'PK placeholder');
  writeFileSync(join(demoRoot, 'Documents', 'Proposal.docx'), 'PK placeholder');
  mkdirSync(join(demoRoot, 'Documents', 'Contracts'), { recursive: true });
  writeFileSync(join(demoRoot, 'Documents', 'Contracts', 'msa-2026.md'), '# MSA 2026\n\nDraft.\n');
  const demo = await makeStorage(adminToken, 'My files', demoRoot, false);

  // A shared drive: RBAC on, so it is invisible until somebody is granted
  // something in it. This is the reporter's "shared volumes should just appear
  // there, one click, no mount instructions".
  const teamRoot = mkdtempSync(join(tmpdir(), 'filex-sidenav-team-'));
  mkdirSync(join(teamRoot, 'Q3 campaign'), { recursive: true });
  mkdirSync(join(teamRoot, 'Brand assets'), { recursive: true });
  mkdirSync(join(teamRoot, 'Payroll'), { recursive: true });
  writeFileSync(join(teamRoot, 'Q3 campaign', 'brief.md'), '# Q3 campaign brief\n');
  writeFileSync(join(teamRoot, 'Brand assets', 'logo-usage.md'), '# Logo usage\n');
  writeFileSync(join(teamRoot, 'Payroll', 'secret.md'), '# Not for Ayse\n');
  const team = await makeStorage(adminToken, 'Marketing', teamRoot, true);

  for (const [st, paths] of [
    [demo, ['My files://', 'My files://Photos', 'My files://Documents', 'My files://Documents/Contracts']],
    [team, ['Marketing://', 'Marketing://Q3 campaign', 'Marketing://Brand assets', 'Marketing://Payroll']],
  ]) {
    for (const p of paths) await indexPath(adminToken, p);
    await api(adminToken, `/api/admin/storages/${st.id}/sync`, { method: 'POST' });
  }

  // The end user this whole change is for.
  const mk = await api(adminToken, '/api/admin/users', {
    method: 'POST',
    body: JSON.stringify({
      email: USER.email,
      password: USER.password,
      role: 'user',
      display_name: 'Ayse',
      locale: 'en',
    }),
  });
  if (!mk.ok) throw new Error(`create user: ${mk.status} ${await mk.text()}`);
  const user = await mk.json();

  // Two folders in the shared drive, and deliberately NOT the third: a
  // "Shared with me" that lists everything is not a share, it is a directory.
  for (const [path, level] of [
    ['Marketing://Q3 campaign', 'editor'],
    ['Marketing://Brand assets', 'viewer'],
  ]) {
    const res = await api(adminToken, '/api/files/permissions', {
      method: 'POST',
      body: JSON.stringify({ path, user_id: user.id, level }),
    });
    if (!res.ok) throw new Error(`grant ${path}: ${res.status} ${await res.text()}`);
  }

  // Render thumbnails. The pipeline runs on UPLOAD, so fixtures written
  // straight to disk have none and every photo shot comes out as a generic
  // icon.
  await backfillThumbs();

  // Now act as the user, so Starred / Recent / Trash have real content that
  // belongs to the account the screenshots are taken with.
  const userToken = await login(USER.email, USER.password);
  const photos = await indexPath(userToken, 'My files://Photos');
  const docs = await indexPath(userToken, 'My files://Documents');
  const pool = [...photos, ...docs].filter((f) => f.type === 'file');

  for (const f of pool.slice(0, 4)) {
    await api(userToken, '/api/files/manager/star', {
      method: 'POST',
      body: JSON.stringify({ node_id: f.id, starred: true }),
    });
  }
  for (const f of pool.slice(2, 8)) {
    await api(userToken, '/api/files/manager/recent', {
      method: 'POST',
      body: JSON.stringify({ node_id: f.id }),
    });
    await sleep(60); // distinct timestamps, so the order is not arbitrary
  }
  // Something in the trash, deleted by the user themselves.
  const doomed = docs.filter((f) => f.type === 'file').slice(-2);
  if (doomed.length) {
    const res = await api(userToken, '/api/files/manager?action=delete', {
      method: 'POST',
      body: JSON.stringify({
        path: 'My files://Documents',
        items: doomed.map((f) => ({ path: f.path })),
      }),
    });
    if (!res.ok) log(`⚠ trash seed failed: ${res.status} ${await res.text()}`);
  }

  const shared = await (await api(userToken, '/api/files/manager/shared-with-me')).json();
  log(
    `seeded — starred ${Math.min(4, pool.length)}, recent ${Math.min(6, Math.max(0, pool.length - 2))}, ` +
      `trashed ${doomed.length}, shared ${shared.total} in [${(shared.storages ?? []).join(', ')}]`,
  );
  check(
    'shared-with-me lists the two granted folders and not the third',
    shared.total === 2 && !JSON.stringify(shared.files).includes('Payroll'),
    `total=${shared.total}`,
  );
  return { adminToken, userToken };
}

function backfillThumbs() {
  if (!binPath) {
    log('⚠ no local binary — thumbnails will be generic icons');
    return Promise.resolve();
  }
  return new Promise((done) => {
    const p = spawn(binPath, ['thumb', 'backfill'], {
      env: { ...process.env, FILEX_DATA_DIR: DATA },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    p.stdout.on('data', (d) => process.env.SHOTS_VERBOSE && process.stdout.write(d));
    p.on('close', () => done());
    p.on('error', () => done());
  });
}

// ── browser ───────────────────────────────────────────────────────────────
async function newContext(browser, width, height) {
  const ctx = await browser.newContext({
    viewport: { width, height },
    deviceScaleFactor: 2,
    locale: 'en-US',
    colorScheme: 'light',
  });
  await ctx.addInitScript(() => {
    localStorage.setItem('filex.locale', 'en');
    // ⚠ The exact key FileExplorer checks (TOUR_LS_KEY). Guessing it wrong
    // leaves the onboarding tour open and its backdrop swallows every click.
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem('filex.installPrompt.dismissed', '1');
  });
  return ctx;
}

// ⚠ By id, and waiting for the destination page. Matching the fields by label
// picks up the wrong control and the submit never fires; and because the login
// page's own URL already contains the base prefix, waiting on the prefix alone
// resolves instantly and every later step runs signed OUT.
async function signIn(page, base, who) {
  await page.goto(`${URL}${base}login`);
  await page.fill('#email', who.email);
  await page.fill('#password', who.password);
  await page.click('button[type="submit"]');
  await page.waitForURL(/\/(admin|drive)\/(dashboard|explore)/, { timeout: 25_000 });
}

async function waitForExplorer(page) {
  await page.waitForSelector('.fe [data-testid="sidenav"]', { timeout: 25_000 });
  await page.waitForSelector('.fe-list__row, .fe-grid__card, .fe-state', { timeout: 25_000 });
  await sleep(700);
}

async function shot(target, name) {
  mkdirSync(OUT, { recursive: true });
  // ⚠ Park the pointer somewhere neutral first. Playwright leaves the mouse
  // wherever it last clicked, so a panel row sat there with its :hover
  // background in the first set of shots and read as a second selected item.
  const page = target.mouse ? target : target.page?.();
  if (page?.mouse) {
    await page.mouse.move(4, 4);
    await sleep(150);
  }
  await target.screenshot({ path: join(OUT, name) });
  log(`wrote ${name}`);
}

/** Width of the listing pane — the number the whole "does it squeeze" question
 *  comes down to. */
const primaryWidth = (page) =>
  page.evaluate(() => document.querySelector('.fe__primary')?.getBoundingClientRect().width ?? 0);

const navWidth = (page) =>
  page.evaluate(() => document.querySelector('[data-testid="sidenav"]')?.getBoundingClientRect().width ?? 0);

/** Open a row by name — the way a person opens it. */
async function openRow(page, name) {
  await page.evaluate((n) => {
    const row = [...document.querySelectorAll('.fe-list__row, .fe-grid__card')].find((r) =>
      (r.textContent ?? '').includes(n),
    );
    row?.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
  }, name);
  await sleep(1200);
}

/**
 * EMBED_HOST — a plain HTML page, no framework, no bundler: the way somebody
 * embedding filex in their own product actually reaches it. Served from the
 * instance's own origin so the element's requests carry the same cookies a real
 * host page's would.
 *
 * It sets `sidenav` as an ATTRIBUTE and everything else through the `config`
 * property, which is the combination worth proving: `buildConfig` merges
 * `{...attributes, ...config}`, so a key the config object does not carry
 * survives from the attribute — and a key it does carry wins.
 */
const EMBED_HOST = (token, storages, connections) => `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Acme Portal — Files</title>
<style>
  body { margin: 0; font: 15px system-ui, sans-serif; background: #f4f5f7; color: #1a1e27; }
  header { padding: 18px 28px; background: #fff; border-bottom: 1px solid #e2e6ed; }
  header b { font-size: 17px; }
  header span { color: #5a6475; margin-left: 10px; font-size: 13px; }
  main { padding: 24px 28px; }
  .box { height: 620px; background: #fff; border-radius: 10px; overflow: hidden;
         box-shadow: 0 1px 3px rgba(15,23,42,.12); }
</style></head>
<body>
  <header><b>Acme Portal</b><span>Documents — embedded with &lt;filex-explorer&gt;</span></header>
  <main><div class="box"><filex-explorer id="fx" api-base="${URL}" sidenav ui-profile="simple"${connections ? ' connections' : ''}></filex-explorer></div></main>
  <script type="module">
    // ⚠ Set \`config\` BEFORE the module import, not after. The import is what
    // registers and upgrades the element, and the explorer loads its first
    // folder on mount — a config assigned afterwards arrives too late for that
    // first request, which then goes out unauthenticated (401) against the
    // default adapter. Measured here: "Could not load this folder", with the
    // panel rendered perfectly beside it.
    const el = document.getElementById('fx');
    el.config = {
      // ⚠ NOT \`apiBase: ''\`. The core refuses a config with neither apiBase
      // nor endpoint, and the config property WINS over the attribute — so an
      // empty string here overwrites a perfectly good \`api-base\` attribute
      // and the element throws before it renders anything.
      apiBase: '${URL}',
      auth: { kind: 'bearer', token: ${JSON.stringify(token)} },
      locale: 'en',
      theme: 'light',
      multiStorageRoot: true,
      storages: ${JSON.stringify(storages)},
    };
    await import('/embed.js');
  </script>
</body></html>`;

async function run(tokens) {
  const browser = await chromium.launch();
  try {
    // ── 1440px, as the end user ──────────────────────────────────────────
    const ctx = await newContext(browser, 1440, 900);
    const page = await ctx.newPage();
    await signIn(page, '/drive/', USER);
    await page.goto(`${URL}/drive/explore`);
    await waitForExplorer(page);

    check(
      'the panel is expanded by default, for a user who never chose',
      (await page.locator('[data-testid="sidenav"].fe-sidenav--rail').count()) === 0,
      `nav width ${Math.round(await navWidth(page))}px`,
    );
    check(
      'the shared drive is marked as shared in the storage list',
      (await page.locator('[data-testid="sidenav-storage-Marketing"] .fe-sidenav__tag').count()) === 1,
    );

    // Into a real folder, so the shot shows files and thumbnails.
    await page.locator('[data-testid="sidenav-storage-My files"]').click();
    await sleep(1200);
    await openRow(page, 'Photos');
    // Grid, so the shot shows the thumbnails a "regular user" was promised —
    // the reporter's list ends with them. ⚠ The toolbar renders every action
    // twice, once in an aria-hidden measuring strip that still has a box, so
    // click the real one.
    await page.evaluate(() => {
      const real = [...document.querySelectorAll('.fe-toolbar__view button')].filter(
        (b) => !b.closest('.fe-toolbar__measure') && !b.closest('[aria-hidden="true"]'),
      );
      real.find((b) => /grid/i.test(b.getAttribute('title') ?? ''))?.click();
    });
    await sleep(1200);
    await shot(page, 'sidenav-expanded-1440.png');
    const wideExpanded = await primaryWidth(page);
    const navExpanded = await navWidth(page);
    check(
      'the grid shows real thumbnails, not a wall of generic icons',
      (await page.locator('.fe-grid__thumb img').count()) >= 6,
      `${await page.locator('.fe-grid__thumb img').count()} tiles with an image`,
    );

    // ── collapse to the rail ─────────────────────────────────────────────
    await page.locator('[data-testid="sidenav-toggle"]').click();
    await sleep(500);
    check(
      'collapsing leaves an icon rail, not an empty gutter',
      (await page.locator('[data-testid="sidenav"].fe-sidenav--rail').count()) === 1 &&
        (await navWidth(page)) > 40,
      `rail ${Math.round(await navWidth(page))}px`,
    );
    await shot(page, 'sidenav-rail-1440.png');
    const wideRail = await primaryWidth(page);
    const navRail = await navWidth(page);
    check(
      'the listing gets the width back when the panel collapses',
      wideRail > wideExpanded + 100,
      `listing ${Math.round(wideExpanded)}px expanded → ${Math.round(wideRail)}px on the rail ` +
        `(+${Math.round(wideRail - wideExpanded)}px); panel ${Math.round(navExpanded)} → ${Math.round(navRail)}`,
    );

    // ── the rail is fully keyboard-reachable ─────────────────────────────
    const railIds = await page.evaluate(() =>
      [...document.querySelectorAll('[data-testid="sidenav"] button')].map(
        (b) => b.getAttribute('data-testid') ?? b.className,
      ),
    );
    await page.evaluate(() => {
      const first = document.querySelector('[data-testid="sidenav-toggle"]');
      first?.focus();
    });
    const reached = new Set();
    for (let i = 0; i < railIds.length + 6; i++) {
      const id = await page.evaluate(() => {
        const el = document.activeElement;
        return el ? el.getAttribute('data-testid') ?? el.className : '';
      });
      if (id) reached.add(id);
      await page.keyboard.press('Tab');
      await sleep(40);
    }
    const missed = railIds.filter((id) => !reached.has(id));
    check(
      'Tab reaches every control on the rail',
      missed.length === 0,
      `${railIds.length} controls, missed [${missed.join(', ')}]`,
    );

    // ── the collapse survives a reload ───────────────────────────────────
    await page.reload();
    await waitForExplorer(page);
    check(
      'the collapsed state survives a reload',
      (await page.locator('[data-testid="sidenav"].fe-sidenav--rail').count()) === 1,
      `filex.sidenav = ${await page.evaluate(() => localStorage.getItem('filex.sidenav'))}`,
    );

    // Back to expanded for the view shots — that is how the profile ships.
    await page.locator('[data-testid="sidenav-toggle"]').click();
    await sleep(500);

    // ── the four views, with real content ────────────────────────────────
    for (const view of ['recent', 'starred', 'shared', 'trash']) {
      await page.locator(`[data-testid="sidenav-view-${view}"]`).click();
      await sleep(1400);
      const rows = await page.locator('.fe-list__row, .fe-grid__card').count();
      const crumb = await page.locator('.fe-breadcrumb').innerText().catch(() => '');
      check(
        `the ${view} view lists real content and the breadcrumb says where you are`,
        rows > 0 && /recent|starred|shared|trash/i.test(crumb),
        `${rows} rows, breadcrumb "${crumb.replace(/\s+/g, ' ').trim()}"`,
      );
      await shot(page, `view-${view}-1440.png`);
    }

    // ── the Connections entries, opened from inside the explorer ─────────
    // ⚠ Measured against the onboarding tour, not assumed: the tour is appended
    // to <body> at z-index 96 and the same surface was painted over once
    // already in the web app. elementFromPoint at the card's centre has to come
    // back as something inside the overlay, or the panel is under something.
    for (const [entry, testid, shot_] of [
      ['sidenav-connect', 'connections-panel', 'connect-1440.png'],
      ['sidenav-apikeys', 'api-tokens', 'apikeys-1440.png'],
    ]) {
      await page.locator(`[data-testid="${entry}"]`).click();
      await page.waitForSelector(`[data-testid="${testid}"]`, { timeout: 20_000 });
      await sleep(900);
      const onTop = await page.evaluate(() => {
        const card = document.querySelector('.fe-overlay__card');
        if (!card) return 'no overlay';
        const r = card.getBoundingClientRect();
        const hit = document.elementFromPoint(r.left + r.width / 2, r.top + 40);
        return hit?.closest('.fe-overlay') ? 'overlay' : (hit?.className ?? 'unknown');
      });
      check(
        `${entry} opens ${testid} inside the explorer, on top of everything`,
        onTop === 'overlay',
        `topmost element at the card is "${onTop}"`,
      );
      await shot(page, shot_);
      await page.keyboard.press('Escape');
      await sleep(400);
      check(
        `Escape closes the ${testid} overlay`,
        (await page.locator('[data-testid="explorer-overlay"]').count()) === 0,
      );
    }

    // The API-key surface has to be the FULL one — the rich form used to live
    // in the web app only, so an embed got users who could not mint a token.
    await page.locator('[data-testid="sidenav-apikeys"]').click();
    await page.waitForSelector('[data-testid="api-tokens"]', { timeout: 20_000 });
    await sleep(600);
    check(
      'the API-key surface is the full one (scopes, folder limit, expiry)',
      (await page.locator('[data-testid="token-form-full"]').count()) === 1 &&
        (await page.locator('[data-testid="token-scope-write"]').count()) === 1 &&
        (await page.locator('[data-testid="token-root"]').count()) === 1 &&
        (await page.locator('[data-testid="token-expiry"]').count()) === 1,
    );
    // …and it actually mints, as the non-admin account, which is the thing that
    // was impossible before this surface existed anywhere but the admin panel.
    await page.locator('[data-testid="token-label"]').fill('WebDAV — laptop');
    await page.locator('[data-testid="token-mint"]').click();
    await page.waitForSelector('[data-testid="token-secret"]', { timeout: 20_000 });
    check(
      'a non-admin can mint an API key from the panel',
      (await page.locator('[data-testid="token-secret"] code').innerText()).length > 20,
    );
    await sleep(400);
    await shot(page, 'apikeys-minted-1440.png');
    await page.keyboard.press('Escape');
    await sleep(400);

    // ── the simple profile actually drops the chrome ─────────────────────
    await page.locator('[data-testid="sidenav-storage-My files"]').click();
    await sleep(1000);
    check(
      'the simple profile has no tab strip',
      (await page.locator('.fe-tabs').count()) === 0,
    );
    check(
      'the simple profile offers list + grid, not the full switcher',
      (await page.locator('.fe-toolbar__view button').count()) === 2,
      `${await page.locator('.fe-toolbar__view button').count()} view buttons`,
    );
    check(
      'this deployment opts a non-admin INTO Connections (25-connections.spec)',
      (await page.locator('[data-testid="sidenav-connect"]').count()) === 1 &&
        (await page.locator('[data-testid="sidenav-apikeys"]').count()) === 1,
    );
    await ctx.close();

    // ── 390px — a phone ──────────────────────────────────────────────────
    const mob = await newContext(browser, 390, 844);
    const mpage = await mob.newPage();
    await signIn(mpage, '/drive/', USER);
    await mpage.goto(`${URL}/drive/explore`);
    await mpage.waitForSelector('.fe', { timeout: 25_000 });
    await sleep(1200);
    await mpage.evaluate(() => {
      const row = [...document.querySelectorAll('.fe-list__row')].find((r) =>
        (r.textContent ?? '').includes('My files'),
      );
      row?.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    });
    await sleep(1400);

    check(
      'at 390px the panel is not a column — it is closed until asked for',
      (await mpage.locator('[data-testid="sidenav"]').count()) === 0,
    );
    const listingClosed = await primaryWidth(mpage);
    await shot(mpage, 'sidenav-closed-390.png');

    const noHScroll = await mpage.evaluate(() => ({
      doc: document.documentElement.scrollWidth <= document.documentElement.clientWidth,
      docW: document.documentElement.scrollWidth,
      clientW: document.documentElement.clientWidth,
      body:
        (document.querySelector('.fe__body')?.scrollWidth ?? 0) <=
        (document.querySelector('.fe__body')?.clientWidth ?? 0),
    }));
    check(
      'nothing scrolls horizontally at 390px',
      noHScroll.doc && noHScroll.body,
      `page ${noHScroll.docW}/${noHScroll.clientW}, listing overflow ${noHScroll.body ? 'none' : 'YES'}`,
    );

    await mpage.locator('[data-testid="toolbar-nav"]').click();
    await sleep(600);
    check(
      'the toolbar toggle opens the drawer at 390px',
      (await mpage.locator('[data-testid="sidenav"].fe-sidenav--drawer').count()) === 1,
    );
    const listingOpen = await primaryWidth(mpage);
    check(
      'the drawer floats over the listing instead of squeezing it',
      Math.abs(listingOpen - listingClosed) < 2,
      `listing ${Math.round(listingClosed)}px closed, ${Math.round(listingOpen)}px with the drawer open`,
    );
    await shot(mpage, 'sidenav-drawer-390.png');
    await mob.close();

    // ── the panel inside somebody else's page ────────────────────────────
    // First without the attribute: `ui-profile="simple"` alone must leave the
    // Connections entries off, which is the reporter's half of the bargain.
    let embedConnections = false;
    const emb = await newContext(browser, 1440, 900);
    const epage = await emb.newPage();
    const HOST = `${URL}/acme-portal.html`;
    await epage.route(HOST, (route) =>
      route.fulfill({
        contentType: 'text/html',
        body: EMBED_HOST(
          tokens.userToken,
          [{ name: 'My files' }, { name: 'Marketing' }],
          embedConnections,
        ),
      }),
    );
    await epage.goto(HOST);
    await epage.waitForSelector('filex-explorer [data-testid="sidenav"]', { timeout: 30_000 });
    await sleep(1500);
    await epage.evaluate(() => {
      const row = [...document.querySelectorAll('.fe-list__row')].find((r) =>
        (r.textContent ?? '').includes('My files'),
      );
      row?.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    });
    await sleep(1500);
    check(
      'the panel renders inside a plain-HTML embed, switched on by the attribute',
      (await epage.locator('filex-explorer [data-testid="sidenav"]').count()) === 1,
    );
    check(
      'the embed took ui-profile="simple" from the attribute too',
      (await epage.locator('filex-explorer .fe-tabs').count()) === 0 &&
        (await epage.locator('filex-explorer .fe-toolbar__view button').count()) === 2,
    );
    check(
      'ui-profile="simple" alone leaves Connections off in an embed',
      (await epage.locator('[data-testid="sidenav-connect"]').count()) === 0,
    );
    await shot(epage, 'embed-webcomponent-1440.png');

    // …and one attribute turns it on, which is the owner's half.
    embedConnections = true;
    await epage.goto(HOST);
    await epage.waitForSelector('filex-explorer [data-testid="sidenav-connect"]', { timeout: 30_000 });
    await sleep(1500);
    await epage.evaluate(() => {
      const row = [...document.querySelectorAll('.fe-list__row')].find((r) =>
        (r.textContent ?? '').includes('My files'),
      );
      row?.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
    });
    await sleep(1400);
    await epage.locator('[data-testid="sidenav-apikeys"]').click();
    await epage.waitForSelector('[data-testid="api-tokens"]', { timeout: 20_000 });
    await sleep(800);
    check(
      'the connections="" attribute gives the embed both entries and the key surface',
      (await epage.locator('filex-explorer [data-testid="sidenav-connect"]').count()) === 1 &&
        (await epage.locator('filex-explorer [data-testid="token-form-full"]').count()) === 1,
    );
    await shot(epage, 'embed-apikeys-1440.png');
    await emb.close();

    // ── the administrator, same panel, collapsed ─────────────────────────
    const adm = await newContext(browser, 1440, 900);
    const apage = await adm.newPage();
    await signIn(apage, '/admin/', ADMIN);
    await apage.goto(`${URL}/admin/explore`);
    await waitForExplorer(apage);
    check(
      'an administrator gets the same panel',
      (await apage.locator('[data-testid="sidenav"]').count()) === 1,
    );
    check(
      'an administrator keeps the full view switcher',
      (await apage.locator('.fe-toolbar__view button').count()) === 3,
      `${await apage.locator('.fe-toolbar__view button').count()} view buttons`,
    );
    const admExpanded = await primaryWidth(apage);
    await apage.locator('[data-testid="sidenav-toggle"]').click();
    await sleep(500);
    await apage.locator('[data-testid="sidenav-storage-My files"]').click();
    await sleep(1400);
    await shot(apage, 'admin-rail-1440.png');
    const admRail = await primaryWidth(apage);
    check(
      "the admin's existing UI is not squeezed once the panel is on the rail",
      admRail > admExpanded + 100 && admRail > 1300,
      `${Math.round(admExpanded)}px expanded → ${Math.round(admRail)}px on the rail`,
    );
    await adm.close();
  } finally {
    await browser.close();
  }
}

// ── main ──────────────────────────────────────────────────────────────────
let proc = null;
try {
  proc = await boot();
  const tokens = await seed();
  await run(tokens);
} finally {
  if (proc && !process.env.SHOTS_KEEP) proc.kill();
  if (process.env.SHOTS_KEEP) log(`instance left running at ${URL}`);
}

const failed = results.filter((r) => !r.ok);
console.log(`\n${results.length - failed.length}/${results.length} checks passed`);
if (failed.length) {
  for (const f of failed) console.log(`  FAILED: ${f.name}${f.detail ? ` — ${f.detail}` : ''}`);
  process.exitCode = 1;
}
