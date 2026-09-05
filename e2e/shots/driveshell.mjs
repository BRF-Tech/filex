// Screenshots + measurements for the Drive shell (`uiProfile: 'drive'`,
// GitHub #14, the reporter's four mockups).
//
// This change is a LAYOUT: a passing unit test proves almost nothing about it.
// The questions are whether the profile actually changes what renders, whether
// a filter chip filters anything real, whether the folders/files split reads as
// two groups, whether the details panel's two tabs both have something in them,
// whether the whole shell survives 390px without a horizontal scrollbar, and
// whether every new control can be reached from the keyboard. All of those are
// measured here, in a real browser, against a real instance — and the same run
// writes the pictures a reviewer looks at.
//
//   node e2e/shots/driveshell.mjs        (from the repo root)
//
// Environment:
//   FILEX_BIN     binary to run (default: bin/filex.exe on Windows, bin/filex)
//   SHOTS_URL     use an ALREADY-RUNNING instance instead of spawning one
//   SHOTS_OUT     output directory (default: ../docs/screenshots/driveshell)
//   SHOTS_KEEP=1  leave the instance running afterwards
//
// ⚠ Every shot is in English three ways over — browser locale, the stored
//   `filex.locale` preference and the server default. Getting a half-Turkish
//   dialog into the repo took one of those being unset (docs/CONTRIBUTING.md,
//   release step 2). The repo is public and its readers are not Turkish.
//
// ⚠ Do NOT reach for the Playwright MCP browser here: sibling agents share that
//   profile and it locks. This drives the `@playwright/test` chromium under
//   e2e/node_modules directly.

import { spawn } from 'node:child_process';
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from '@playwright/test';
import { seedFixtures } from './fixtures.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO = resolve(HERE, '../..');

// ⚠ Not 5299, and not a port anything else in this repo reaches for. A run on
// 5299 came back with a `cypress-local` storage in its own database: another
// harness on this machine had the port, so the seed and every check went to
// somebody else's instance. `assertOurInstance` below is the belt for the same
// braces — a fixed port is a guess, an empty storage list is a measurement.
const PORT = Number(process.env.SHOTS_PORT ?? 5471);
// ⚠ 127.0.0.1, not localhost: on Windows `localhost` resolves to ::1 first and
// a server bound to 127.0.0.1 answers that with ECONNREFUSED — which looks
// exactly like a server that failed to start (e2e/README.md).
const URL = process.env.SHOTS_URL ?? `http://127.0.0.1:${PORT}`;
const OUT = process.env.SHOTS_OUT ?? join(REPO, 'docs/screenshots/driveshell');
const DATA = join(tmpdir(), 'filex-driveshell-data');

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
    binPath = process.env.FILEX_BIN ?? defaultBin();
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
      FILEX_SECRET_KEY: 'driveshell-shots-key-not-a-real-secret',
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
      // the server's working dir, and two storages read each other's files
      // (e2e/README.md).
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
  return row;
}

async function indexPath(token, qualified) {
  const res = await api(token, `/api/files/manager?action=index&path=${encodeURIComponent(qualified)}`);
  if (!res.ok) return [];
  const body = await res.json();
  return Array.isArray(body.files) ? body.files : [];
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

// ── the world the shots are taken in ──────────────────────────────────────
// A believable drive: nested folders, images so thumbnails render, documents of
// several types (the Type chip has to have something to separate), a couple of
// tags and a share. A filter row over four files proves nothing.
/** Refuse to measure somebody else's server. */
async function assertOurInstance(token) {
  const res = await api(token, '/api/admin/storages');
  if (!res.ok) throw new Error(`storage list: ${res.status} ${await res.text()}`);
  const rows = await res.json();
  if (Array.isArray(rows) && rows.length > 0) {
    throw new Error(
      `the instance at ${URL} already has ${rows.length} storage(s) ` +
        `[${rows.map((r) => r.name).join(', ')}] — this is not the fresh instance ` +
        `this script booted. Another harness has the port; set SHOTS_PORT.`,
    );
  }
}

async function seed() {
  const adminToken = await login(ADMIN.email, ADMIN.password);
  await assertOurInstance(adminToken);
  await api(adminToken, '/api/auth/profile', {
    method: 'PATCH',
    body: JSON.stringify({ locale: 'en', display_name: 'Operator' }),
  });

  const demoRoot = mkdtempSync(join(tmpdir(), 'filex-driveshell-demo-'));
  seedFixtures(demoRoot);
  mkdirSync(join(demoRoot, 'Documents', 'Contracts'), { recursive: true });
  mkdirSync(join(demoRoot, 'Design'), { recursive: true });
  mkdirSync(join(demoRoot, 'Code'), { recursive: true });
  writeFileSync(join(demoRoot, 'Documents', 'Q3 budget.xlsx'), 'PK placeholder');
  writeFileSync(join(demoRoot, 'Documents', 'Proposal.docx'), 'PK placeholder');
  writeFileSync(join(demoRoot, 'Documents', 'Contracts', 'msa-2026.md'), '# MSA 2026\n\nDraft.\n');
  writeFileSync(join(demoRoot, 'Design', 'brand.md'), '# Brand\n');
  writeFileSync(join(demoRoot, 'Code', 'app.ts'), 'export const greet = () => "hello";\n');
  writeFileSync(join(demoRoot, 'Code', 'server.go'), 'package main\n');
  writeFileSync(join(demoRoot, 'notes.txt'), 'a plain note\n');
  writeFileSync(join(demoRoot, 'data.csv'), 'a,b\n1,2\n');
  // One file comfortably over 1 MB, so the Size chip has both sides of a
  // boundary to separate — every fixture is otherwise a few hundred bytes and
  // "Under 1 MB" would select all of them, which measures nothing.
  writeFileSync(join(demoRoot, 'archive.zip'), Buffer.alloc(2 * 1024 * 1024, 7));
  // ⚠ The root is what the hero screenshot shows, so it has to read as
  // somebody's drive rather than as a fixture: enough folders and files for the
  // Folders/Files split, the filter row and the grid to all mean something.
  mkdirSync(join(demoRoot, 'Invoices'), { recursive: true });
  mkdirSync(join(demoRoot, 'Projects'), { recursive: true });
  writeFileSync(join(demoRoot, 'Invoices', '2026-08.md'), ['# August', ''].join('\n'));
  writeFileSync(join(demoRoot, 'Projects', 'roadmap.md'), ['# Roadmap', ''].join('\n'));
  writeFileSync(join(demoRoot, 'handbook.pdf'), ['%PDF-1.4 placeholder', ''].join('\n'));
  writeFileSync(join(demoRoot, 'Roadmap.docx'), 'PK placeholder');

  // ⚠ One image among the documents, on purpose. Filtering Photos by "Images"
  // keeps every row, so the check would pass on a filter that does nothing at
  // all; a mixed folder is the only place the answer can be wrong.
  {
    const shots = readdirSync(join(demoRoot, 'Photos')).filter((f) => /\.(png|jpe?g)$/i.test(f));
    if (shots.length) {
      copyFileSync(join(demoRoot, 'Photos', shots[0]), join(demoRoot, 'Documents', 'scan.png'));
      // …and one at the root, so the hero's Files row carries a real picture
      // rather than a wall of generated placeholders.
      copyFileSync(join(demoRoot, 'Photos', shots[0]), join(demoRoot, 'cover.png'));
    }
  }
  const demo = await makeStorage(adminToken, 'My files', demoRoot, false);

  const teamRoot = mkdtempSync(join(tmpdir(), 'filex-driveshell-team-'));
  mkdirSync(join(teamRoot, 'Q3 campaign'), { recursive: true });
  mkdirSync(join(teamRoot, 'Brand assets'), { recursive: true });
  mkdirSync(join(teamRoot, 'Payroll'), { recursive: true });
  writeFileSync(join(teamRoot, 'Q3 campaign', 'brief.md'), '# Q3 campaign brief\n');
  writeFileSync(join(teamRoot, 'Brand assets', 'logo-usage.md'), '# Logo usage\n');
  writeFileSync(join(teamRoot, 'Payroll', 'secret.md'), '# Not for Ayse\n');
  const team = await makeStorage(adminToken, 'Marketing', teamRoot, true);

  for (const [st, paths] of [
    [
      demo,
      [
        'My files://',
        'My files://Photos',
        'My files://Documents',
        'My files://Documents/Contracts',
        'My files://Design',
        'My files://Code',
        'My files://Invoices',
        'My files://Projects',
      ],
    ],
    [team, ['Marketing://', 'Marketing://Q3 campaign', 'Marketing://Brand assets', 'Marketing://Payroll']],
  ]) {
    for (const p of paths) await indexPath(adminToken, p);
    await api(adminToken, `/api/admin/storages/${st.id}/sync`, { method: 'POST' });
  }

  const mk = await api(adminToken, '/api/admin/users', {
    method: 'POST',
    body: JSON.stringify({
      email: USER.email,
      password: USER.password,
      role: 'user',
      display_name: 'Ayse Yilmaz',
      locale: 'en',
    }),
  });
  if (!mk.ok) throw new Error(`create user: ${mk.status} ${await mk.text()}`);
  const user = await mk.json();

  // A second person, so "People with access" has more than one row to draw.
  const mk2 = await api(adminToken, '/api/admin/users', {
    method: 'POST',
    body: JSON.stringify({
      email: 'deniz@local',
      password: 'UserPass2!',
      role: 'user',
      display_name: 'Deniz Kaya',
      locale: 'en',
    }),
  });
  const user2 = mk2.ok ? await mk2.json() : null;

  for (const [path, uid, level] of [
    ['Marketing://Q3 campaign', user.id, 'owner'],
    ['Marketing://Brand assets', user.id, 'viewer'],
    ...(user2 ? [['Marketing://Q3 campaign', user2.id, 'editor']] : []),
  ]) {
    const res = await api(adminToken, '/api/files/permissions', {
      method: 'POST',
      body: JSON.stringify({ path, user_id: uid, level }),
    });
    if (!res.ok) log(`⚠ grant ${path}: ${res.status} ${await res.text()}`);
  }

  // A quota, so the storage line has a ceiling to draw a bar against.
  await api(adminToken, `/api/admin/users/${user.id}/quota`, {
    method: 'POST',
    body: JSON.stringify({ quota_bytes: 5 * 1024 * 1024 * 1024 }),
  });

  // Thumbnails are rendered on UPLOAD; fixtures written straight to disk have
  // none, and every grid shot comes out as a wall of generic icons.
  await backfillThumbs();

  const userToken = await login(USER.email, USER.password);

  // ⚠ And some usage that is actually THEIRS. `used_bytes` is
  // `SUM(nodes.size) WHERE owner_id = me`, and `nodes.owner_id` is written by
  // the upload path only — it is nil for everything a storage sync discovered.
  // Fixtures written straight to disk therefore leave the storage line reading
  // "0 B", which measures the seed rather than the feature.
  for (const [name, body] of [
    ['Meeting notes.md', ['# Notes', '', 'Uploaded by Ayse.', ''].join('\n')],
    ['expenses.csv', ['item,amount', 'train,42', ''].join('\n')],
  ]) {
    const form = new FormData();
    form.append('path', 'My files://Documents');
    form.append('file[]', new Blob([body], { type: 'text/plain' }), name);
    const up = await fetch(`${URL}/api/files/manager?action=upload`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${userToken}` },
      body: form,
    });
    if (!up.ok) log(`⚠ upload ${name}: ${up.status} ${await up.text()}`);
  }

  const photos = await indexPath(userToken, 'My files://Photos');
  const docs = await indexPath(userToken, 'My files://Documents');
  const pool = [...photos, ...docs].filter((f) => f.type === 'file');
  for (const f of pool.slice(0, 3)) {
    await api(userToken, '/api/files/manager/star', {
      method: 'POST',
      body: JSON.stringify({ node_id: f.id, starred: true }),
    });
  }
  for (const f of pool.slice(1, 6)) {
    await api(userToken, '/api/files/manager/recent', {
      method: 'POST',
      body: JSON.stringify({ node_id: f.id }),
    });
    await sleep(60);
  }
  // A couple of tags, and a share link on a file in the shared drive, so the
  // details panel has a link row with something in it.
  for (const [f, tag] of [
    [pool[0], 'design'],
    [pool[1], 'project alpha'],
  ]) {
    if (!f) continue;
    await api(userToken, '/api/files/manager/tags', {
      method: 'POST',
      body: JSON.stringify({ node_id: f.id, tags: [tag] }),
    });
  }

  log(`seeded — ${pool.length} files in the pool, 2 storages, 2 people`);
  return { adminToken, userToken };
}

// ── browser ───────────────────────────────────────────────────────────────
async function newContext(browser, width, height, scheme = 'light') {
  const ctx = await browser.newContext({
    viewport: { width, height },
    deviceScaleFactor: 2,
    locale: 'en-US',
    colorScheme: scheme,
  });
  await ctx.addInitScript((s) => {
    localStorage.setItem('filex.locale', 'en');
    localStorage.setItem('filex.theme', s);
    // ⚠ The exact key FileExplorer checks (TOUR_LS_KEY). Guessing it wrong
    // leaves the onboarding tour open and its backdrop swallows every click.
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem('filex.installPrompt.dismissed', '1');
  }, scheme);
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
  // wherever it last clicked, so a row sits there with its :hover background
  // and reads as a second selected item.
  const page = target.mouse ? target : target.page?.();
  if (page?.mouse) {
    await page.mouse.move(4, 4);
    await sleep(150);
  }
  await target.screenshot({ path: join(OUT, name) });
  log(`wrote ${name}`);
}

const primaryWidth = (page) =>
  page.evaluate(() => document.querySelector('.fe__primary')?.getBoundingClientRect().width ?? 0);

async function openRow(page, name) {
  await page.evaluate((n) => {
    const row = [...document.querySelectorAll('.fe-list__row, .fe-grid__card')].find((r) =>
      (r.textContent ?? '').includes(n),
    );
    row?.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
  }, name);
  await sleep(1300);
}

const rowCount = (page) => page.locator('.fe-list__row, .fe-grid__card').count();

/** Click a teleported context-menu entry by its visible label. */
async function clickMenuItem(page, label) {
  const ok = await page.evaluate((l) => {
    const item = [...document.querySelectorAll('.fe-ctx__item')].find((b) =>
      (b.textContent ?? '').trim().includes(l),
    );
    if (!item) return false;
    item.click();
    return true;
  }, label);
  await sleep(500);
  return ok;
}

/**
 * A plain HTML page mounting `<filex-explorer>` with NO navigation panel — the
 * shape an embedder gets from `sideNav: false` (and the default under
 * `rootPath`). This is the deployment the virtual Trash row was invented for,
 * so it is the only place its two directions can be measured in a browser:
 * the row IS there in a folder listing, and is NOT there in a search of that
 * same folder.
 *
 * ⚠ `config` is assigned BEFORE the module import. The import is what upgrades
 * the element, and the explorer loads its first folder on mount — a config set
 * afterwards arrives too late for that request, which then goes out
 * unauthenticated against the default adapter.
 */
const EMBED_HOST = (token, storages) => `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Acme Portal — Files</title>
<style>
  body { margin: 0; font: 15px system-ui, sans-serif; background: #f4f5f7; color: #1a1e27; }
  header { padding: 18px 28px; background: #fff; border-bottom: 1px solid #e2e6ed; }
  main { padding: 24px 28px; }
  .box { height: 620px; background: #fff; border-radius: 10px; overflow: hidden;
         box-shadow: 0 1px 3px rgba(15,23,42,.12); }
</style></head>
<body>
  <header><b>Acme Portal</b></header>
  <main><div class="box"><filex-explorer id="fx" api-base="${URL}"></filex-explorer></div></main>
  <script type="module">
    const el = document.getElementById('fx');
    el.config = {
      apiBase: ${JSON.stringify(URL)},
      auth: { kind: 'bearer', token: ${JSON.stringify(token)} },
      locale: 'en',
      theme: 'light',
      multiStorageRoot: true,
      sideNav: false,
      trashVisible: true,
      storages: ${JSON.stringify(storages)},
    };
    await import('/embed.js');
  </script>
</body></html>`;

async function run(tokens) {
  const browser = await chromium.launch();
  try {
    // ══ 1440px, the end user, light ═════════════════════════════════════
    const ctx = await newContext(browser, 1440, 900);
    const page = await ctx.newPage();
    await signIn(page, '/drive/', USER);
    await page.goto(`${URL}/drive/explore`);
    await waitForExplorer(page);

    // ── the profile actually changes what renders ─────────────────────────
    check(
      'the drive profile puts ONE search field in the header, with the palette hint',
      (await page.locator('[data-testid="drive-search"]').count()) === 1 &&
        (await page.locator('[data-testid="drive-search-palette"]').count()) === 1,
    );
    check(
      'there is exactly one search box on screen, not two',
      (await page.locator('.fe-toolbar input[type="search"]').count()) === 1,
      `${await page.locator('.fe-toolbar input[type=\"search\"]').count()} search inputs in the toolbar`,
    );
    check(
      'the panel shows one "+ New" instead of the Upload / New folder pair',
      (await page.locator('[data-testid="sidenav-new"]').count()) === 1 &&
        (await page.locator('[data-testid="sidenav-upload"]').count()) === 0 &&
        (await page.locator('[data-testid="sidenav-new-folder"]').count()) === 0,
    );
    check(
      'the view switcher and the details toggle moved onto the breadcrumb row',
      (await page.locator('.fe-subhead__actions [data-testid="view-grid"]').count()) === 1 &&
        (await page.locator('.fe-subhead__actions [data-testid="subhead-inspector"]').count()) === 1 &&
        (await page.locator('.fe-toolbar .fe-toolbar__view').count()) === 0,
    );
    check(
      'the storage line is under the navigation, with a real figure behind it',
      (await page.locator('[data-testid="sidenav-quota"]').count()) === 1 &&
        /\d/.test(await page.locator('.fe-sidenav__quota-text').innerText().catch(() => '')),
      (await page.locator('.fe-sidenav__quota-text').innerText().catch(() => '—')).trim(),
    );

    await page.locator('[data-testid="sidenav-storage-My files"]').click();
    await sleep(1300);
    check(
      'the filter row is under the breadcrumb',
      (await page.locator('[data-testid="filterbar"]').count()) === 1 &&
        (await page.locator('[data-testid="filter-type"]').count()) === 1 &&
        (await page.locator('[data-testid="filter-modified"]').count()) === 1 &&
        (await page.locator('[data-testid="filter-size"]').count()) === 1,
    );
    // ⚠ Three chips, not the mockup's four. A People chip is left out because
    // no listing row carries an owner and the endpoint reads no owner
    // parameter — see the report on this branch.
    check(
      'there is no People chip (nothing behind it — deliberate, see the report)',
      (await page.locator('[data-testid="filterbar"] .fe-filterbar__chip').count()) === 3,
      `${await page.locator('[data-testid="filterbar"] .fe-filterbar__chip').count()} chips`,
    );

    // ── grid, and the folders/files split ────────────────────────────────
    await page.locator('.fe-subhead__actions [data-testid="view-grid"]').click();
    await sleep(900);
    const headings = await page.locator('.fe-grid__heading').allInnerTexts();
    check(
      'grid view labels Folders and Files as two sections',
      headings.length === 2 && /folder/i.test(headings[0]) && /file/i.test(headings[1]),
      `headings [${headings.join(', ')}]`,
    );
    const orderOk = await page.evaluate(() => {
      const kids = [...document.querySelectorAll('.fe-grid > *')];
      const firstFileHeading = kids.findIndex((k) => k.classList.contains('fe-grid__heading') && /file/i.test(k.textContent ?? ''));
      const dirsAfter = kids
        .slice(firstFileHeading + 1)
        .filter((k) => k.classList.contains('fe-grid__card') && k.classList.contains('is-dir')).length;
      return { firstFileHeading, dirsAfter };
    });
    check(
      'every folder is above the Files heading, none below it',
      orderOk.dirsAfter === 0,
      `${orderOk.dirsAfter} folder cards under "Files"`,
    );
    await shot(page, 'driveshell-grid-1440.png');
    check(
      'the grid shows real thumbnails, not a wall of generic icons',
      (await page.locator('.fe-grid__thumb img').count()) >= 3,
      `${await page.locator('.fe-grid__thumb img').count()} tiles with an image`,
    );

    // ── the HERO, and its preconditions asserted BEFORE the shutter ──────
    // ⚠ This is the picture the README shows for `uiProfile: 'drive'`, and the
    // first version of it was taken later in this run — after the palette test
    // had typed "brief" into the search field — so the canonical shot of the
    // shell was a mid-search view of two rows. A screenshot that is wrong is
    // not missing information, it is false information (docs/CONTRIBUTING.md,
    // release step 2), so the state it claims to show is measured here rather
    // than assumed: empty field, no filter set, both sections, enough rows to
    // read as somebody's drive instead of a fixture.
    const heroState = await page.evaluate(() => {
      const field = document.querySelector('[data-testid="drive-search"] input');
      const headings = [...document.querySelectorAll('.fe-grid__heading')].map((h) =>
        (h.textContent ?? '').trim(),
      );
      return {
        query: field ? field.value : null,
        chipsSet: document.querySelectorAll('.fe-filterbar__chip.is-set').length,
        headings,
        folders: document.querySelectorAll('.fe-grid__card.is-dir').length,
        files: document.querySelectorAll('.fe-grid__card:not(.is-dir)').length,
        thumbs: document.querySelectorAll('.fe-grid__thumb img').length,
        trashCards: [...document.querySelectorAll('.fe-grid__card')].filter((c) =>
          /trash/i.test(c.textContent ?? ''),
        ).length,
        panelTrash: document.querySelectorAll('[data-testid="sidenav-view-trash"]').length,
      };
    });
    check(
      'the hero shot is the shell as somebody LANDS on it: empty search, no filter',
      heroState.query === '' && heroState.chipsSet === 0,
      `search field ${JSON.stringify(heroState.query)}, ${heroState.chipsSet} filter chips set`,
    );
    check(
      'the hero shot shows a populated drive, not a fixture',
      heroState.folders >= 5 &&
        heroState.files >= 5 &&
        heroState.headings.length === 2 &&
        heroState.thumbs >= 2,
      `${heroState.folders} folders + ${heroState.files} files under [${heroState.headings.join(', ')}], ` +
        `${heroState.thumbs} real thumbnails`,
    );
    // ⚠ The reason this round exists. The panel is offering Trash three inches
    // to the left, so a Trash card among the real folders is the same door
    // twice — and a screenshot is exactly where it slipped through unnoticed
    // the last two times. Asserted before the shutter so it cannot come back
    // invisibly.
    check(
      'no Trash card in the Folders section — the panel already offers that door',
      heroState.trashCards === 0 && heroState.panelTrash === 1,
      `${heroState.trashCards} Trash cards in the listing, ` +
        `${heroState.panelTrash} Trash entry in the panel`,
    );
    await shot(page, 'driveshell-hero-1440.png');

    // ── the "+ New" menu ─────────────────────────────────────────────────
    await page.locator('[data-testid="sidenav-new"]').click();
    await sleep(400);
    const newItems = await page.locator('.fe-ctx__item .fe-ctx__label').allInnerTexts();
    check(
      'the New menu offers upload, new folder and a file request',
      newItems.length === 3 &&
        /upload/i.test(newItems[0]) &&
        /folder/i.test(newItems[1]) &&
        /request/i.test(newItems[2]),
      `[${newItems.join(' · ')}]`,
    );
    await shot(page, 'driveshell-new-menu-1440.png');
    // It has to DO something, not just open: New folder opens the real modal.
    await clickMenuItem(page, 'New folder');
    check(
      'the New menu opens the folder modal, not a dead entry',
      (await page.locator('.fe-modal__card').count()) >= 1,
    );
    await page.keyboard.press('Escape');
    await sleep(400);

    // ── the filter row filters something REAL ────────────────────────────
    // ⚠ Inside Documents — a MIXED folder — not the storage root and not Photos.
    // The root holds no image at all, so "Type → Images" there returns zero and
    // a check that only asserts "fewer rows" passes on a filter that removed
    // everything; Photos holds nothing but images, so the same filter removes
    // nothing and the check passes on a filter that does not work either. Only a
    // folder with both can tell those two apart.
    await openRow(page, 'Documents');
    const before = await rowCount(page);
    await page.locator('[data-testid="filter-type"]').click();
    await sleep(300);
    await page.locator('[data-testid="filter-opt-image"]').click();
    await sleep(600);
    const afterImages = await rowCount(page);
    const chipLabel = (await page.locator('[data-testid="filter-type"]').innerText()).trim();
    const nonImages = await page.evaluate(() =>
      [...document.querySelectorAll('.fe-grid__card')].filter((c) => {
        const label = c.querySelector('.fe-grid__label')?.textContent ?? '';
        return !/\.(png|jpe?g|gif|webp|svg|bmp|avif|tiff?)$/i.test(label.trim());
      }).length,
    );
    check(
      'Type → Images keeps the images and drops the rest',
      afterImages > 0 && afterImages < before && nonImages === 0,
      `${before} rows → ${afterImages}, ${nonImages} non-image rows left, chip reads "${chipLabel}"`,
    );
    check(
      'the chip says which filter is on, and the count says how much it hid',
      /image/i.test(chipLabel) &&
        (await page.locator('[data-testid="filter-count"]').count()) === 1,
      (await page.locator('[data-testid="filter-count"]').innerText().catch(() => '')).trim(),
    );
    await shot(page, 'driveshell-filter-1440.png');

    // A size filter over a folder that has one big file and many small ones.
    await page.locator('[data-testid="filter-clear"]').click();
    await sleep(500);
    await page.locator('[data-testid="filter-size"]').click();
    await sleep(300);
    await page.locator('[data-testid="filter-opt-gt100"]').click();
    await sleep(600);
    check(
      'a filter that matches nothing says so, and offers the way back',
      (await page.locator('[data-testid="empty-filtered"]').count()) === 1,
    );
    await shot(page, 'driveshell-filter-empty-1440.png');
    await page.locator('[data-testid="empty-filtered"] .fe-btn').click();
    await sleep(600);
    check(
      'clearing the filters brings every row back',
      (await rowCount(page)) === before,
      `${await rowCount(page)} rows, was ${before}`,
    );

    // ── list view ────────────────────────────────────────────────────────
    await page.locator('.fe-subhead__actions [data-testid="view-list"]').click();
    await sleep(800);
    check('list view still renders rows in the drive shell', (await rowCount(page)) > 0);
    await shot(page, 'driveshell-list-1440.png');

    // ── the details panel, both tabs ─────────────────────────────────────
    // ⚠ A named FILE, not `.fe-list__row:first`. Row one is the virtual `.trash`
    // entry the explorer injects at a storage root — selecting it puts the
    // panel on a folder that cannot be shared, and "Create link" then fails for
    // a reason that has nothing to do with the button.
    await page.evaluate(() => {
      const row = [...document.querySelectorAll('.fe-list__row')].find((r) =>
        (r.textContent ?? '').includes('Q3 budget.xlsx'),
      );
      row?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });
    await sleep(500);
    if ((await page.locator('.fe-inspector').count()) === 0) {
      await page.locator('[data-testid="subhead-inspector"]').click();
      await sleep(700);
    }
    check(
      'the details panel opens on Details, with an Activity tab beside it',
      (await page.locator('[data-testid="inspector-tab-details"].is-active').count()) === 1 &&
        (await page.locator('[data-testid="inspector-tab-activity"]').count()) === 1,
    );
    check(
      'Details carries the share-link row with its Create link button',
      (await page.locator('[data-testid="inspector-create-link"]').count()) === 1 ||
        (await page.locator('.fe-inspector__shares').count()) === 1,
    );
    await shot(page, 'driveshell-info-details-1440.png');
    await page.locator('[data-testid="inspector-tab-activity"]').click();
    await sleep(600);
    check(
      'Activity is a real tab: version history and comments, or an honest empty state',
      (await page.locator('.fe-inspector__versions').count()) +
        (await page.locator('.fe-inspector__comment-form').count()) +
        (await page.locator('[data-testid="inspector-activity-empty"]').count()) >
        0,
    );
    await shot(page, 'driveshell-info-activity-1440.png');

    // Create link has to MINT one, not just sit there.
    await page.locator('[data-testid="inspector-tab-details"]').click();
    await sleep(400);
    if ((await page.locator('[data-testid="inspector-create-link"]').count()) === 1) {
      await page.locator('[data-testid="inspector-create-link"]').click();
      await page.waitForSelector('.fe-inspector__share-url', { timeout: 15_000 }).catch(() => {});
      const url = await page.locator('.fe-inspector__share-url').first().innerText().catch(() => '');
      check(
        'Create link mints a real share link and the panel then shows it',
        /^https?:\/\//.test(url.trim()),
        url.trim().slice(0, 60),
      );
      await shot(page, 'driveshell-info-link-1440.png');
    }

    // ── people with access, where there ARE people ───────────────────────
    await page.locator('[data-testid="sidenav-storage-Marketing"]').click();
    await sleep(1300);
    // ⚠ "Q3 campaign" by name: `GET /api/files/permissions` is OWNER-gated, and
    // this account is only a viewer on "Brand assets" — which sorts first. On
    // that row the section correctly hides, so picking row one would measure
    // the 403 path and call it a missing feature.
    await page.evaluate(() => {
      const row = [...document.querySelectorAll('.fe-list__row')].find((r) =>
        (r.textContent ?? '').includes('Q3 campaign'),
      );
      row?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });
    await sleep(1000);
    const peopleRows = await page.locator('[data-testid="inspector-people"] .fe-inspector__person').count();
    check(
      '"People with access" lists the real grants on an RBAC drive',
      peopleRows > 0,
      `${peopleRows} people`,
    );
    if (peopleRows > 0) await shot(page, 'driveshell-info-people-1440.png');

    // ── ⌘K from inside the field opens the palette, carrying the query ───
    await page.locator('[data-testid="drive-search"] input').click();
    await page.keyboard.type('brief');
    await sleep(500);
    await page.keyboard.press('Control+k');
    await sleep(700);
    const paletteQuery = await page
      .locator('.fe-cmdp__input')
      .first()
      .inputValue()
      .catch(() => '');
    check(
      'Ctrl+K inside the field opens the palette carrying what was typed',
      paletteQuery === 'brief',
      `palette query "${paletteQuery}"`,
    );
    await shot(page, 'driveshell-palette-1440.png');
    await page.keyboard.press('Escape');
    await sleep(400);
    // …and the chip does the same with a mouse.
    await page.locator('[data-testid="drive-search-palette"]').click();
    await sleep(600);
    check(
      'the ⌘K chip is a button a mouse can press, not a printed hint',
      (await page.locator('.fe-cmdp').count()) >= 1,
    );
    await page.keyboard.press('Escape');
    await sleep(400);

    // ── searching, and the sentinel that used to answer with it ──────────
    await page.locator('[data-testid="sidenav-storage-My files"]').click();
    await sleep(1200);
    await page.locator('.fe-subhead__actions [data-testid="view-grid"]').click();
    await sleep(800);

    // ⚠ In THIS deployment the panel is on, so the virtual Trash row is not
    // drawn in either case — that is the new rule, and the no-panel embed at
    // the end of this run is where the row's two directions are measured
    // instead. What is still worth pinning here is that the search answers
    // with hits and nothing else.
    // ⚠ Clear the field FIRST. The palette check above typed into it and the
    // query outlives a navigation, so measuring the "folder listing" here
    // without clearing measures another search — which is exactly how the old
    // canonical screenshot became a mid-search view of two rows.
    await page.locator('[data-testid="drive-search"] input').fill('');
    await sleep(1400);
    const trashInListing = await page
      .locator('.fe-grid__card')
      .evaluateAll((cards) => cards.filter((c) => /trash/i.test(c.textContent ?? '')).length);
    await page.locator('[data-testid="drive-search"] input').fill('notes');
    await sleep(1600);
    const searchState = await page.evaluate(() => {
      const cards = [...document.querySelectorAll('.fe-grid__card')];
      return {
        rows: cards.length,
        trash: cards.filter((c) => /trash/i.test(c.textContent ?? '')).length,
        names: cards.map((c) => (c.querySelector('.fe-grid__label')?.textContent ?? '').trim()),
      };
    });
    check(
      'with the panel on, neither the listing nor a search shows a Trash card',
      searchState.rows > 0 && searchState.trash === 0 && trashInListing === 0,
      `folder listing had ${trashInListing} Trash rows; search "notes" → ${searchState.rows} rows ` +
        `[${searchState.names.join(', ')}], ${searchState.trash} of them Trash`,
    );
    // ⚠ Kept as the SEARCH picture, and asserted to still be one: the README
    // captions it that way, and a caption is a claim about the pixels.
    const shownQuery = await page.locator('[data-testid="drive-search"] input').inputValue();
    check(
      'the search screenshot is actually mid-search',
      shownQuery.length > 0,
      `field reads ${JSON.stringify(shownQuery)}`,
    );
    // ⚠ Renamed from `driveshell-1440.png`: it is the SEARCH picture, and a
    // file whose name describes the wrong thing is how a screenshot gets
    // misfiled and then goes stale without anybody noticing.
    await shot(page, 'driveshell-search-1440.png');
    await page.locator('[data-testid="drive-search"] input').fill('');
    await sleep(1200);
    const expanded = await primaryWidth(page);
    await page.locator('[data-testid="sidenav-toggle"]').click();
    await sleep(600);
    const rail = await primaryWidth(page);
    check(
      'collapsing to the rail keeps a round "+ New" and gives the listing its width back',
      (await page.locator('[data-testid="sidenav"].fe-sidenav--rail [data-testid="sidenav-new"]').count()) === 1 &&
        rail > expanded + 100,
      `listing ${Math.round(expanded)}px → ${Math.round(rail)}px`,
    );
    // ⚠ A rail is still offering Trash: the entry is rendered, one click, and
    // labelled for a screen reader — only its text is gone. So the listing must
    // not grow the row back when somebody collapses the panel.
    check(
      'collapsed to the rail, the panel still offers Trash and the listing still does not',
      (await page.locator('[data-testid="sidenav-view-trash"]').count()) === 1 &&
        (await page
          .locator('.fe-grid__card')
          .evaluateAll((c) => c.filter((x) => /trash/i.test(x.textContent ?? '')).length)) === 0,
    );
    await shot(page, 'driveshell-rail-1440.png');
    await page.locator('[data-testid="sidenav-toggle"]').click();
    await sleep(500);

    // ── keyboard reachability of every NEW control ───────────────────────
    const wanted = [
      'sidenav-new',
      'drive-search-palette',
      'filter-type',
      'filter-modified',
      'filter-size',
      'view-list',
      'view-grid',
      'subhead-inspector',
    ];
    await page.evaluate(() => document.querySelector('[data-testid="toolbar-nav"]')?.focus());
    const reached = new Set();
    for (let i = 0; i < 80; i++) {
      const seen = await page.evaluate(() => {
        const el = document.activeElement;
        if (!el) return '';
        if (el.tagName === 'INPUT' && el.closest('[data-testid="drive-search"]')) return 'drive-search-input';
        return el.getAttribute('data-testid') ?? '';
      });
      if (seen) reached.add(seen);
      await page.keyboard.press('Tab');
      await sleep(25);
    }
    const missed = wanted.filter((w) => !reached.has(w));
    check(
      'Tab reaches every control the drive shell adds',
      missed.length === 0 && reached.has('drive-search-input'),
      `missed [${missed.join(', ')}]${reached.has('drive-search-input') ? '' : ' + the search field itself'}`,
    );
    await ctx.close();

    // ══ 1440px, dark ════════════════════════════════════════════════════
    const dctx = await newContext(browser, 1440, 900, 'dark');
    const dpage = await dctx.newPage();
    await signIn(dpage, '/drive/', USER);
    await dpage.goto(`${URL}/drive/explore`);
    await waitForExplorer(dpage);
    await dpage.locator('[data-testid="sidenav-storage-My files"]').click();
    await sleep(1300);
    await dpage.locator('.fe-subhead__actions [data-testid="view-grid"]').click();
    await sleep(900);
    check(
      'the drive shell paints itself in dark mode too',
      (await dpage.locator('[data-testid="drive-search"]').count()) === 1 &&
        (await dpage.evaluate(
          () => getComputedStyle(document.querySelector('.fe-drivesearch')).backgroundColor,
        )) !== 'rgba(0, 0, 0, 0)',
      await dpage.evaluate(
        () => getComputedStyle(document.querySelector('.fe-drivesearch')).backgroundColor,
      ),
    );
    await shot(dpage, 'driveshell-dark-1440.png');
    await dpage.locator('.fe-list__row, .fe-grid__card').first().click();
    await sleep(500);
    if ((await dpage.locator('.fe-inspector').count()) === 0) {
      await dpage.locator('[data-testid="subhead-inspector"]').click();
      await sleep(700);
    }
    await shot(dpage, 'driveshell-dark-info-1440.png');
    await dctx.close();

    // ══ 390px — a phone ═════════════════════════════════════════════════
    for (const scheme of ['light', 'dark']) {
      const mob = await newContext(browser, 390, 844, scheme);
      const mpage = await mob.newPage();
      await signIn(mpage, '/drive/', USER);
      await mpage.goto(`${URL}/drive/explore`);
      await mpage.waitForSelector('.fe', { timeout: 25_000 });
      await sleep(1200);
      await openRow(mpage, 'My files');

      // ⚠ The EXPLORER's own box, measured on `.fe` and `.fe__body` — not the
      // document. There is a known 8px overflow at 390px coming from the admin
      // shell's header, which another agent is fixing; asserting on
      // documentElement here would either hide behind their fix or fail for
      // their reason.
      const box = await mpage.evaluate(() => {
        const pick = (sel) => {
          const el = document.querySelector(sel);
          return el ? { sw: el.scrollWidth, cw: el.clientWidth } : null;
        };
        return {
          fe: pick('.fe'),
          body: pick('.fe__body'),
          toolbar: pick('.fe-toolbar'),
          filterbar: pick('.fe-filterbar'),
          doc: {
            sw: document.documentElement.scrollWidth,
            cw: document.documentElement.clientWidth,
          },
        };
      });
      const noOverflow =
        box.fe && box.fe.sw <= box.fe.cw && box.body && box.body.sw <= box.body.cw &&
        box.toolbar && box.toolbar.sw <= box.toolbar.cw;
      check(
        `at 390px (${scheme}) the explorer's own box does not scroll horizontally`,
        !!noOverflow,
        `fe ${box.fe?.sw}/${box.fe?.cw}, body ${box.body?.sw}/${box.body?.cw}, ` +
          `toolbar ${box.toolbar?.sw}/${box.toolbar?.cw}, filter row ${box.filterbar?.sw}/${box.filterbar?.cw} ` +
          `(the filter row scrolls ON ITSELF by design), document ${box.doc.sw}/${box.doc.cw}`,
      );
      check(
        `at 390px (${scheme}) the panel is a drawer, closed until asked for`,
        (await mpage.locator('[data-testid="sidenav"]').count()) === 0,
      );
      // ⚠ The drawer is CLOSED here, so the panel is not on screen at all — and
      // the row still must not come back. The toolbar's panel toggle is visible
      // at every width, so the destination is one tap away; keying the rule to
      // "is the panel currently painted" would add and remove a row from the
      // listing every time somebody opened or closed the drawer.
      check(
        `at 390px (${scheme}) a closed drawer still counts as offering Trash`,
        (await mpage
          .locator('.fe-list__row, .fe-grid__card')
          .evaluateAll((c) => c.filter((x) => /trash/i.test(x.textContent ?? '')).length)) === 0 &&
          (await mpage.locator('[data-testid="toolbar-nav"]').isVisible()),
      );
      const chipVisible = await mpage
        .locator('[data-testid="drive-search-palette"]')
        .isVisible()
        .catch(() => false);
      check(
        `at 390px (${scheme}) the search field keeps the row and drops only the shortcut chip`,
        (await mpage.locator('[data-testid="drive-search"]').count()) === 1 && !chipVisible,
        `field ${(await mpage.locator('[data-testid="drive-search"]').count())}, chip visible ${chipVisible}`,
      );
      await shot(mpage, `driveshell-390${scheme === 'dark' ? '-dark' : ''}.png`);
      if (scheme === 'light') {
        await mpage.locator('[data-testid="toolbar-nav"]').click();
        await sleep(700);
        check(
          'the drawer at 390px carries the "+ New" button and the storage line',
          (await mpage.locator('[data-testid="sidenav"].fe-sidenav--drawer [data-testid="sidenav-new"]').count()) === 1 &&
            (await mpage.locator('[data-testid="sidenav-quota"]').count()) === 1,
        );
        await shot(mpage, 'driveshell-drawer-390.png');
      }
      await mob.close();
    }

    // ══ the administrator — the standard profile is untouched ═══════════
    const adm = await newContext(browser, 1440, 900);
    const apage = await adm.newPage();
    await signIn(apage, '/admin/', ADMIN);
    await apage.goto(`${URL}/admin/explore`);
    await waitForExplorer(apage);
    check(
      'an administrator gets the standard explorer, with no drive chrome',
      (await apage.locator('[data-testid="drive-search"]').count()) === 0 &&
        (await apage.locator('[data-testid="filterbar"]').count()) === 0 &&
        (await apage.locator('[data-testid="sidenav-new"]').count()) === 0 &&
        (await apage.locator('[data-testid="sidenav-upload"]').count()) === 1,
    );
    check(
      'an administrator keeps the full view switcher, in the toolbar where it was',
      (await apage.locator('.fe-toolbar .fe-toolbar__view button').count()) === 3,
      `${await apage.locator('.fe-toolbar .fe-toolbar__view button').count()} view buttons in the toolbar`,
    );
    await apage.locator('[data-testid="sidenav-storage-My files"]').click();
    await sleep(1300);
    await shot(apage, 'standard-profile-1440.png');
    await adm.close();

    // ══ no panel — the case the Trash row exists for ═════════════════════
    // Both directions of the rule, in a browser, with a real control:
    // without a panel there IS no other door to the bin, so the row stays;
    // and a SEARCH still must not answer with it.
    const emb = await newContext(browser, 1440, 900);
    const epage = await emb.newPage();
    const HOST = `${URL}/acme-portal.html`;
    await epage.route(HOST, (route) =>
      route.fulfill({
        contentType: 'text/html',
        body: EMBED_HOST(tokens.userToken, [{ name: 'My files' }, { name: 'Marketing' }]),
      }),
    );
    await epage.goto(HOST);
    await epage.waitForSelector('filex-explorer .fe', { timeout: 30_000 });
    await sleep(1800);
    await openRow(epage, 'My files');
    const embedListing = await epage.evaluate(() => {
      const rows = [...document.querySelectorAll('.fe-list__row, .fe-grid__card')];
      return {
        panel: document.querySelectorAll('[data-testid="sidenav"]').length,
        rows: rows.length,
        trash: rows.filter((r) => /trash/i.test(r.textContent ?? '')).length,
      };
    });
    check(
      'with NO panel, the listing still offers the Trash row — the door it was invented for',
      embedListing.panel === 0 && embedListing.rows > 0 && embedListing.trash === 1,
      `${embedListing.panel} panels, ${embedListing.rows} rows, ${embedListing.trash} of them Trash`,
    );
    await shot(epage, 'embed-no-panel-1440.png');

    await epage.evaluate(() => {
      const input = document.querySelector('.fe-toolbar input[type="search"]');
      if (!input) return;
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLInputElement.prototype,
        'value',
      ).set;
      setter.call(input, 'notes');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await sleep(1800);
    const embedSearch = await epage.evaluate(() => {
      const rows = [...document.querySelectorAll('.fe-list__row, .fe-grid__card')];
      return {
        rows: rows.length,
        trash: rows.filter((r) => /trash/i.test(r.textContent ?? '')).length,
      };
    });
    check(
      'with NO panel, a search still does not answer with the Trash row',
      embedSearch.rows > 0 && embedSearch.trash === 0,
      `listing ${embedListing.rows} rows / ${embedListing.trash} Trash → ` +
        `search ${embedSearch.rows} rows / ${embedSearch.trash} Trash`,
    );
    await emb.close();
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
