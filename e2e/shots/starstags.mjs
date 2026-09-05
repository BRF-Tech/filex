// Screenshots + measurements for starring-as-an-action and the Tags section.
//
// A star that only exists in one view, and a tag list nobody can open, are UI
// facts: a passing unit test says nothing about either. The questions here are
// whether a user in GRID view can star something at all, whether the action
// reads the state it is in, whether the Starred view then holds exactly what
// was starred, whether a tag opens its files, and whether every surface that
// prints a path segment prints the TAG NAME instead of the sentinel. All of
// them are measured in a real browser against a real instance, and the same
// run writes the pictures a reviewer looks at.
//
//   node e2e/shots/starstags.mjs        (from the repo root)
//
// Environment:
//   FILEX_BIN     binary to run (default: bin/filex-starstags.exe, bin/filex.exe, bin/filex)
//   SHOTS_URL     use an ALREADY-RUNNING instance instead of spawning one
//   SHOTS_OUT     output directory (default: ../docs/screenshots/starstags)
//   SHOTS_KEEP=1  leave the instance running afterwards
//
// ⚠ Every shot is in English three ways over — browser locale, the stored
//   `filex.locale` preference and the server default (docs/CONTRIBUTING.md,
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

const PORT = Number(process.env.SHOTS_PORT ?? 5311);
// ⚠ 127.0.0.1, not localhost: on Windows `localhost` resolves to ::1 first and
// a server bound to 127.0.0.1 answers that with ECONNREFUSED — which looks
// exactly like a server that failed to start (e2e/README.md).
const URL = process.env.SHOTS_URL ?? `http://127.0.0.1:${PORT}`;
const OUT = process.env.SHOTS_OUT ?? join(REPO, 'docs/screenshots/starstags');
const DATA = join(tmpdir(), 'filex-starstags-data');

const ADMIN = { email: 'admin@local', password: 'admin' };

const log = (...a) => console.log('•', ...a);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const results = [];
function check(name, ok, detail) {
  results.push({ name, ok, detail });
  console.log(`${ok ? '  PASS' : '  FAIL'}  ${name}${detail ? ` — ${detail}` : ''}`);
}

// ── the instance ──────────────────────────────────────────────────────────
function defaultBin() {
  for (const p of [
    join(REPO, 'bin/filex-starstags.exe'),
    join(REPO, 'bin/filex.exe'),
    join(REPO, 'bin/filex'),
  ]) {
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
  if (!bin) throw new Error('no filex binary — build one first');
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
      FILEX_SECRET_KEY: 'starstags-shots-key-not-a-real-secret',
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

async function makeStorage(token, name, root) {
  mkdirSync(root, { recursive: true });
  const res = await api(token, '/api/admin/storages', {
    method: 'POST',
    body: JSON.stringify({
      name,
      driver: 'local',
      mount_path: root,
      // ⚠ The root of a local storage is `config.path` (e2e/README.md).
      config: { path: root },
      sync_mode: 'manual',
      sync_interval_s: 0,
      enabled: true,
      read_only: false,
      rbac_enabled: false,
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
// ⚠ NOTHING is starred and NOTHING is tagged here. Both are what the browser
// half of this run does through the UI: seeding them over the API would prove
// the endpoints work, which was never in doubt — the report is that the user
// cannot REACH them.
async function seed() {
  const token = await login(ADMIN.email, ADMIN.password);
  await api(token, '/api/auth/profile', {
    method: 'PATCH',
    body: JSON.stringify({ locale: 'en', display_name: 'Operator' }),
  });

  const root = mkdtempSync(join(tmpdir(), 'filex-starstags-'));
  seedFixtures(root); // Photos/ with real PNGs, Documents/, …
  writeFileSync(join(root, 'Documents', 'Q3 budget.xlsx'), 'PK placeholder');
  writeFileSync(join(root, 'Documents', 'Proposal.docx'), 'PK placeholder');
  const storage = await makeStorage(token, 'My files', root);

  for (const p of ['My files://', 'My files://Photos', 'My files://Documents']) {
    await indexPath(token, p);
  }
  await api(token, `/api/admin/storages/${storage.id}/sync`, { method: 'POST' });
  await backfillThumbs();

  const photos = await indexPath(token, 'My files://Photos');
  const docs = await indexPath(token, 'My files://Documents');
  log(`seeded — ${photos.length} in Photos, ${docs.length} in Documents`);
  return { token, photos, docs };
}

/** Tag files over the API — the tags the PANEL then has to list. Done from
 *  inside the run so the "no tags yet" state can be photographed first. */
async function seedTags(token, photos, docs) {
  const files = [...photos, ...docs].filter((f) => f.type === 'file');
  // More than the panel's peek of 8, so "Show all (N)" has to appear.
  const plan = [
    ['invoices', files.slice(0, 3)],
    ['holiday', files.slice(3, 5)],
    ['contracts', files.slice(5, 6)],
  ];
  const filler = ['archive', 'budget', 'design', 'legal', 'press', 'raw', 'travel', 'urgent'];
  for (const [tag, set] of plan) {
    for (const f of set) {
      const res = await api(token, '/api/files/manager/tags', {
        method: 'POST',
        body: JSON.stringify({ node_id: f.id, tags: [tag] }),
      });
      if (!res.ok) throw new Error(`tag ${tag}: ${res.status} ${await res.text()}`);
    }
  }
  // The filler tags all land on ONE file, purely to make the list long.
  const carrier = files[files.length - 1];
  const res = await api(token, '/api/files/manager/tags', {
    method: 'POST',
    body: JSON.stringify({ node_id: carrier.id, tags: filler }),
  });
  if (!res.ok) throw new Error(`filler tags: ${res.status} ${await res.text()}`);
  const all = await (await api(token, '/api/files/manager/tags/all')).json();
  log(`tagged — ${all.tags.length} distinct tags: ${all.tags.join(', ')}`);
  return { tagged: plan, all: all.tags, carrier };
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

async function signIn(page, base, who) {
  await page.goto(`${URL}${base}login`);
  await page.fill('#email', who.email);
  await page.fill('#password', who.password);
  await page.click('button[type="submit"]');
  await page.waitForURL(/\/(admin|drive)\/(dashboard|explore)/, { timeout: 25_000 });
}

async function waitForExplorer(page) {
  await page.waitForSelector('.fe [data-testid="sidenav"]', { timeout: 25_000 });
  await page.waitForSelector('.fe-list__row, .fe-grid__card, .fe-gal__card, .fe-state', {
    timeout: 25_000,
  });
  await sleep(600);
}

async function shot(target, name) {
  mkdirSync(OUT, { recursive: true });
  await target.screenshot({ path: join(OUT, name) });
  log(`wrote ${name}`);
}

/** Park the pointer somewhere neutral — Playwright leaves the mouse where it
 *  last clicked, and a hovered row reads as a second selected item. */
async function parkPointer(page) {
  await page.mouse.move(4, 4);
  await sleep(150);
}

async function openRow(page, name) {
  await page.evaluate((n) => {
    const row = [...document.querySelectorAll('.fe-list__row, .fe-grid__card, .fe-gal__card')].find(
      (r) => (r.textContent ?? '').includes(n),
    );
    row?.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
  }, name);
  await sleep(1200);
}

/** Switch the main pane's view mode. ⚠ The toolbar renders every action twice,
 *  once in an aria-hidden measuring strip that still has a box — click the
 *  real one. */
async function setViewMode(page, which) {
  await page.evaluate((w) => {
    const real = [...document.querySelectorAll('.fe-toolbar__view button')].filter(
      (b) => !b.closest('.fe-toolbar__measure') && !b.closest('[aria-hidden="true"]'),
    );
    real.find((b) => new RegExp(w, 'i').test(b.getAttribute('title') ?? ''))?.click();
  }, which);
  await sleep(1000);
}

/** Right-click the card/row whose label contains `name`, at its own centre. */
async function rightClick(page, name) {
  const box = await page.evaluate((n) => {
    const el = [...document.querySelectorAll('.fe-list__row, .fe-grid__card, .fe-gal__card')].find(
      (r) => (r.textContent ?? '').includes(n),
    );
    if (!el) return null;
    const r = el.getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.top + Math.min(30, r.height / 2) };
  }, name);
  if (!box) throw new Error(`no row/card for "${name}"`);
  await page.mouse.click(box.x, box.y, { button: 'right' });
  await page.waitForSelector('.fe-ctx', { timeout: 8000 });
  await sleep(350);
}

// ⚠ Strip the leading glyph. Every entry renders as "<icon><label>" in one
// text node run ("☆Star"), so a naive /^Star$/ never matches and the
// check fails on a menu that is perfectly correct.
const menuLabels = (page) =>
  page.evaluate(() =>
    [...document.querySelectorAll('.fe-ctx .fe-ctx__item')].map((b) =>
      (b.textContent ?? '').trim().replace(/^[^\p{L}]+/u, ''),
    ),
  );

async function clickMenu(page, label) {
  const ok = await page.evaluate((l) => {
    const item = [...document.querySelectorAll('.fe-ctx .fe-ctx__item')].find((b) =>
      (b.textContent ?? '').trim().includes(l),
    );
    if (!item) return false;
    item.click();
    return true;
  }, label);
  if (!ok) throw new Error(`no menu entry "${label}"`);
  await sleep(900);
}

const rowNames = (page) =>
  page.evaluate(() =>
    [...document.querySelectorAll('.fe-list__row, .fe-grid__card, .fe-gal__card')].map((r) =>
      (r.querySelector('.fe-list__name, .fe-grid__label, .fe-gal__label')?.textContent ?? '').trim(),
    ),
  );

const tabLabels = (page) =>
  page.evaluate(() =>
    [...document.querySelectorAll('.fe-tabs__label, .fe-tabs button')].map((b) =>
      (b.textContent ?? '').trim(),
    ),
  );

const crumbText = (page) =>
  page
    .locator('.fe-breadcrumb')
    .innerText()
    .then((s) => s.replace(/\s+/g, ' ').trim())
    .catch(() => '');

const notFoundVisible = (page) =>
  page.evaluate(() => {
    const t = document.querySelector('.fe-state__title')?.textContent ?? '';
    return /not found|could not be found|bulunamadı/i.test(t);
  });

const stateTitle = (page) =>
  page.evaluate(() => (document.querySelector('.fe-state__title')?.textContent ?? '').trim());

async function run(seeded) {
  const browser = await chromium.launch();
  try {
    const ctx = await newContext(browser, 1440, 900);
    const page = await ctx.newPage();
    await signIn(page, '/admin/', ADMIN);
    await page.goto(`${URL}/admin/explore`);
    await waitForExplorer(page);

    // ── 0. the panel with NO tags at all ─────────────────────────────────
    check(
      'with nothing tagged the panel still shows a Tags section that says so',
      (await page.locator('[data-testid="sidenav"] .fe-sidenav__hint').count()) === 1,
      `hint: "${(await page.locator('.fe-sidenav__hint').innerText().catch(() => '')).trim()}"`,
    );
    await parkPointer(page);
    await shot(page, 'tags-panel-empty.png');

    // Now tag files over the API and reload, so the section fills.
    const tags = await seedTags(seeded.token, seeded.photos, seeded.docs);

    await openRow(page, 'My files');
    await openRow(page, 'Photos');

    // ── 1. starring from the context menu, in LIST view ──────────────────
    await setViewMode(page, 'list');
    const listNames = (await rowNames(page)).filter(Boolean);
    const first = listNames[0];
    await rightClick(page, first);
    let labels = await menuLabels(page);
    check(
      'the list context menu offers Star, next to Tags',
      labels.some((l) => /^Star$/.test(l)) && labels.some((l) => /^Tags/.test(l)),
      `menu: [${labels.join(' | ')}]`,
    );
    await shot(page, 'star-menu-list.png');
    await clickMenu(page, 'Star');
    await parkPointer(page);
    const listStarred = await page.evaluate(
      (n) =>
        [...document.querySelectorAll('.fe-list__row')]
          .find((r) => (r.textContent ?? '').includes(n))
          ?.querySelector('.filex-star-btn')
          ?.getAttribute('aria-pressed') === 'true',
      first,
    );
    check('starring from the menu lights the row star in list view', listStarred, `file "${first}"`);

    // …and the entry now reads Unstar, on the same file.
    await rightClick(page, first);
    labels = await menuLabels(page);
    check(
      'the entry flips to Unstar once the file is starred',
      labels.some((l) => /^Unstar$/.test(l)) && !labels.some((l) => /^Star$/.test(l)),
      `menu: [${labels.filter((l) => /star/i.test(l)).join(' | ')}]`,
    );
    await shot(page, 'star-menu-unstar.png');
    await page.keyboard.press('Escape');
    await sleep(300);

    // ── 2. GRID view — the mode the panel's own screenshots show ─────────
    await setViewMode(page, 'grid');
    await sleep(800);
    const gridNames = (await rowNames(page)).filter(Boolean);
    const second = gridNames.find((n) => n !== first);
    // The chip is painted on hover; and permanently once starred.
    const chipHidden = await page.evaluate(
      (n) => {
        const card = [...document.querySelectorAll('.fe-grid__card')].find((c) =>
          (c.textContent ?? '').includes(n),
        );
        const star = card?.querySelector('.fe-grid__star');
        return star ? getComputedStyle(star).opacity : 'no-chip';
      },
      second,
    );
    await page.hover(`.fe-grid__card:has-text("${second}")`).catch(() => {});
    await sleep(400);
    const chipOnHover = await page.evaluate(
      (n) => {
        const card = [...document.querySelectorAll('.fe-grid__card')].find((c) =>
          (c.textContent ?? '').includes(n),
        );
        const star = card?.querySelector('.fe-grid__star');
        return star ? getComputedStyle(star).opacity : 'no-chip';
      },
      second,
    );
    check(
      'a grid card carries a star chip that appears on hover',
      chipHidden === '0' && chipOnHover === '1',
      `opacity ${chipHidden} at rest → ${chipOnHover} hovered`,
    );
    await shot(page, 'star-grid-hover.png');

    await rightClick(page, second);
    labels = await menuLabels(page);
    check(
      'the grid context menu offers Star too',
      labels.some((l) => /^Star$/.test(l)),
      `menu: [${labels.join(' | ')}]`,
    );
    await shot(page, 'star-menu-grid.png');
    await clickMenu(page, 'Star');
    await parkPointer(page);
    const gridChipPersists = await page.evaluate(
      (n) => {
        const card = [...document.querySelectorAll('.fe-grid__card')].find((c) =>
          (c.textContent ?? '').includes(n),
        );
        const star = card?.querySelector('.fe-grid__star');
        const btn = card?.querySelector('.filex-star-btn');
        return {
          opacity: star ? getComputedStyle(star).opacity : 'no-chip',
          pressed: btn?.getAttribute('aria-pressed'),
        };
      },
      second,
    );
    check(
      'a starred card keeps its star visible without hovering',
      gridChipPersists.opacity === '1' && gridChipPersists.pressed === 'true',
      `opacity ${gridChipPersists.opacity}, aria-pressed ${gridChipPersists.pressed}`,
    );
    await shot(page, 'star-grid-starred.png');

    // ── 3. GALLERY view ──────────────────────────────────────────────────
    await setViewMode(page, 'gallery');
    await sleep(900);
    const galNames = (await rowNames(page)).filter(Boolean);
    const third = galNames.find((n) => n !== first && n !== second);
    await rightClick(page, third);
    labels = await menuLabels(page);
    check(
      'the gallery context menu offers Star as well',
      labels.some((l) => /^Star$/.test(l)),
      `menu: [${labels.join(' | ')}]`,
    );
    await shot(page, 'star-menu-gallery.png');
    await clickMenu(page, 'Star');
    await parkPointer(page);
    const galStarred = await page.evaluate(
      (n) =>
        [...document.querySelectorAll('.fe-gal__card')]
          .find((c) => (c.textContent ?? '').includes(n))
          ?.querySelector('.filex-star-btn')
          ?.getAttribute('aria-pressed') === 'true',
      third,
    );
    check('starring works from the gallery too', galStarred, `file "${third}"`);
    await shot(page, 'star-gallery-starred.png');

    // ── 3b. the keyboard ─────────────────────────────────────────────────
    await setViewMode(page, 'list');
    await sleep(600);
    const fourth = (await rowNames(page))
      .filter(Boolean)
      .find((n) => n !== first && n !== second && n !== third);
    await page.evaluate((n) => {
      const row = [...document.querySelectorAll('.fe-list__row')].find((r) =>
        (r.textContent ?? '').includes(n),
      );
      row?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    }, fourth);
    await sleep(300);
    await page.keyboard.press('s');
    await sleep(800);
    const keyStarred = await page.evaluate(
      (n) =>
        [...document.querySelectorAll('.fe-list__row')]
          .find((r) => (r.textContent ?? '').includes(n))
          ?.querySelector('.filex-star-btn')
          ?.getAttribute('aria-pressed') === 'true',
      fourth,
    );
    check('S stars the selection from the keyboard', keyStarred, `file "${fourth}"`);
    // …and back off again, so the Starred view check below stays exact.
    await page.keyboard.press('s');
    await sleep(800);
    const keyUnstarred = await page.evaluate(
      (n) =>
        [...document.querySelectorAll('.fe-list__row')]
          .find((r) => (r.textContent ?? '').includes(n))
          ?.querySelector('.filex-star-btn')
          ?.getAttribute('aria-pressed') === 'false',
      fourth,
    );
    check('S unstars it again — one key, both directions', keyUnstarred, `file "${fourth}"`);

    // ── 4. the Starred view holds exactly those three ────────────────────
    await setViewMode(page, 'list');
    await page.locator('[data-testid="sidenav-view-starred"]').click();
    await sleep(1400);
    const starredNames = (await rowNames(page)).filter(Boolean).sort();
    const expected = [first, second, third].sort();
    check(
      'the Starred view contains exactly what was starred, and nothing else',
      JSON.stringify(starredNames) === JSON.stringify(expected),
      `[${starredNames.join(', ')}] vs [${expected.join(', ')}]`,
    );
    await parkPointer(page);
    await shot(page, 'starred-view.png');

    // ── 5. the Tags section ──────────────────────────────────────────────
    await page.reload();
    await waitForExplorer(page);
    const panelTags = await page.evaluate(() =>
      [...document.querySelectorAll('[data-testid^="sidenav-tag-"]')].map((b) =>
        (b.textContent ?? '').trim(),
      ),
    );
    check(
      'the panel lists the tags that exist, capped at 8 with a "Show all"',
      panelTags.length === 8 && (await page.locator('[data-testid="sidenav-tags-more"]').count()) === 1,
      `${panelTags.length} of ${tags.all.length} shown: [${panelTags.join(', ')}]`,
    );
    await parkPointer(page);
    await shot(page, 'tags-panel.png');

    await page.locator('[data-testid="sidenav-tags-more"]').click();
    await sleep(400);
    const allShown = await page.locator('[data-testid^="sidenav-tag-"]').count();
    check(
      '"Show all" reveals the rest without pushing anything off a fixed panel',
      allShown === tags.all.length,
      `${allShown} rows`,
    );
    await parkPointer(page);
    await shot(page, 'tags-panel-all.png');

    // ── 6. opening a tag ─────────────────────────────────────────────────
    await page.locator('[data-testid="sidenav-tag-invoices"]').click();
    await sleep(1500);
    const tagRows = (await rowNames(page)).filter(Boolean);
    const tabs = await tabLabels(page);
    const crumb = await crumbText(page);
    const hash = await page.evaluate(() => window.location.hash);
    check(
      'a tag opens the files carrying it',
      tagRows.length === 3,
      `${tagRows.length} rows: [${tagRows.join(', ')}]`,
    );
    check(
      'the tab strip shows the TAG NAME, not the sentinel',
      tabs.some((l) => l.includes('invoices')) && !tabs.some((l) => l.includes('.tag')),
      `tabs: [${tabs.join(' | ')}]`,
    );
    check(
      'the breadcrumb shows the TAG NAME, not the sentinel',
      crumb.includes('invoices') && !crumb.includes('.tag'),
      `breadcrumb: "${crumb}"`,
    );
    check(
      'the address-bar hash carries the tag by name',
      hash.includes('invoices'),
      `location.hash = "${hash}"`,
    );
    await parkPointer(page);
    await shot(page, 'tag-view.png');

    // ── 7. a tag with nothing in it ──────────────────────────────────────
    await page.goto(`${URL}/admin/explore#.tag~nothing-here`);
    await page.reload();
    await waitForExplorer(page);
    const emptyTitle = await stateTitle(page);
    check(
      'an empty tag says which tag is empty, and is not a "not found"',
      !(await notFoundVisible(page)) && emptyTitle.includes('nothing-here'),
      `state: "${emptyTitle}"`,
    );
    await parkPointer(page);
    await shot(page, 'tag-empty.png');

    // ── 8. every sentinel survives a reload ──────────────────────────────
    // The bug the owner reported: `#.trash` came back "Folder not found — this
    // folder does not exist, was moved, or you do not have access to it", for a
    // view that exists and is merely empty.
    for (const [testid, sentinel, word] of [
      ['sidenav-view-trash', '.trash', 'trash'],
      ['sidenav-view-starred', '.starred', 'starred'],
      ['sidenav-view-recent', '.recent', 'recent'],
      ['sidenav-view-shared', '.shared', 'shared'],
    ]) {
      await page.goto(`${URL}/admin/explore`);
      await waitForExplorer(page);
      await page.locator(`[data-testid="${testid}"]`).click();
      await sleep(1200);
      const before = await page.evaluate(() => window.location.hash);
      await page.reload();
      await waitForExplorer(page);
      const active =
        (await page.locator(`[data-testid="${testid}"].is-active`).count()) === 1;
      const crumbAfter = await crumbText(page);
      const notFound = await notFoundVisible(page);
      check(
        `a reload on ${sentinel} lands back in the view, not on "folder not found"`,
        !notFound && active && new RegExp(word, 'i').test(crumbAfter),
        `hash ${before} → panel row active=${active}, breadcrumb "${crumbAfter}", not-found=${notFound}`,
      );
      if (sentinel === '.trash') {
        await parkPointer(page);
        await shot(page, 'reload-trash.png');
      }
    }

    // …and the tag view is deep-linkable the same way.
    await page.goto(`${URL}/admin/explore#.tag~invoices`);
    await page.reload();
    await waitForExplorer(page);
    check(
      'a reload on a tag sentinel lands back in that tag',
      !(await notFoundVisible(page)) &&
        (await rowNames(page)).filter(Boolean).length === 3 &&
        (await crumbText(page)).includes('invoices'),
      `breadcrumb "${await crumbText(page)}"`,
    );
    await parkPointer(page);
    await shot(page, 'reload-tag.png');

    // ── 9. the rail ──────────────────────────────────────────────────────
    await page.goto(`${URL}/admin/explore`);
    await waitForExplorer(page);
    await page.locator('[data-testid="sidenav-toggle"]').click();
    await sleep(500);
    check(
      'the rail keeps ONE Tags button instead of a column of identical glyphs',
      (await page.locator('[data-testid="sidenav-tags-rail"]').count()) === 1 &&
        (await page.locator('[data-testid^="sidenav-tag-"]').count()) === 0,
    );
    await parkPointer(page);
    await shot(page, 'tags-rail.png');
    await page.locator('[data-testid="sidenav-tags-rail"]').click();
    await sleep(500);
    check(
      'the rail Tags button opens the panel on the tag list',
      (await page.locator('[data-testid^="sidenav-tag-"]').count()) > 0,
    );
    await ctx.close();

    // ── 10. 390px — a phone ──────────────────────────────────────────────
    const mob = await newContext(browser, 390, 844);
    const mpage = await mob.newPage();
    await signIn(mpage, '/admin/', ADMIN);
    await mpage.goto(`${URL}/admin/explore`);
    await mpage.waitForSelector('.fe', { timeout: 25_000 });
    await sleep(1400);
    await mpage.locator('[data-testid="toolbar-nav"]').click();
    await sleep(700);
    await mpage.waitForSelector('[data-testid^="sidenav-tag-"]', { timeout: 15_000 }).catch(() => {});
    const drawerTags = await mpage.locator('[data-testid^="sidenav-tag-"]').count();
    check(
      'the 390px drawer lists tags with their labels (never the rail)',
      (await mpage.locator('[data-testid="sidenav"].fe-sidenav--drawer').count()) === 1 &&
        drawerTags === 8 &&
        (await mpage.locator('[data-testid="sidenav-tags-rail"]').count()) === 0,
      `${drawerTags} tag rows in the drawer`,
    );
    // ⚠ The EXPLORER's own overflow, not the page's. The admin shell's header
    // (`DIV.ml-auto…`, web/src — not this package) overhangs 390px by 8px, and
    // it does so on the v0.30.1 binary too: measured 398/390 with the panel
    // closed AND open, on a build that has none of this branch's code. Asserting
    // on the page total would therefore fail on a change that did nothing.
    const noHScroll = await mpage.evaluate(() => {
      const fe = document.querySelector('.fe');
      const body = document.querySelector('.fe__body');
      const nav = document.querySelector('[data-testid="sidenav"]');
      return {
        fe: (fe?.scrollWidth ?? 0) <= (fe?.clientWidth ?? 0),
        body: (body?.scrollWidth ?? 0) <= (body?.clientWidth ?? 0),
        navRight: nav ? Math.round(nav.getBoundingClientRect().right) : 0,
        docW: document.documentElement.scrollWidth,
        clientW: document.documentElement.clientWidth,
      };
    });
    check(
      'the explorer does not scroll horizontally at 390px with the tag list open',
      noHScroll.fe && noHScroll.body && noHScroll.navRight <= 390,
      `explorer ok=${noHScroll.fe && noHScroll.body}, drawer right edge ${noHScroll.navRight}px; ` +
        `page ${noHScroll.docW}/${noHScroll.clientW} (the 8px is the admin shell's header, ` +
        `398/390 on v0.30.1 too)`,
    );
    await shot(mpage, 'tags-drawer-390.png');
    await mpage.locator('[data-testid="sidenav-tag-invoices"]').click();
    await sleep(1500);
    check(
      'tapping a tag at 390px closes the drawer and shows its files',
      (await mpage.locator('[data-testid="sidenav"].fe-sidenav--drawer').count()) === 0 &&
        (await rowNames(mpage)).filter(Boolean).length === 3,
    );
    await shot(mpage, 'tag-view-390.png');
    await mob.close();
  } finally {
    await browser.close();
  }
}

// ── main ──────────────────────────────────────────────────────────────────
let proc = null;
try {
  proc = await boot();
  const seeded = await seed();
  await run(seeded);
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
