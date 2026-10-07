// A .csv in ONLYOFFICE (0.51, GitHub #81, docs/ONLYOFFICE.md → CSV files),
// against a REAL document server - the spreadsheet in these pictures is drawn
// by ONLYOFFICE itself, and nothing else could draw it.
//
//   SHOTS_ONLYOFFICE_URL=… SHOTS_ONLYOFFICE_JWT=… SHOTS_ONLYOFFICE_CALLBACK_HOST=… \
//     node e2e/shots/csvoffice.mjs        (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/csvoffice/ (or SHOTS_OUT):
//
//   csv-view-1440.png          a double-click on a semicolon CSV: ONLYOFFICE's
//                              spreadsheet, a look first (view mode), and no
//                              "Choose CSV options" dialog in front of it
//   csv-edit-1440.png          its Edit button: the editor tab, with the line
//                              that says what a save as CSV keeps
//   csv-open-with-menu.png     the file's menu: Open with ONLYOFFICE, the
//                              built-in table, Choose an app…
//   csv-choose-app.png         Choose an app…: ONLYOFFICE (spreadsheet editor)
//                              and the table
//   default-apps-csv-1440.png  Plugins → Default apps with ONLYOFFICE
//                              connected: .csv opened by ONLYOFFICE first,
//                              the table second
//   csv-menu-no-onlyoffice.png ONLYOFFICE switched off: the table opens the
//                              file, and an administrator's menu shows the
//                              ONLYOFFICE row greyed, saying where to set it up
//
// ⚠ `documentServer()` is the declaration (scripts/lib/shot-scripts.mjs):
// `pnpm shots` refuses this scene before the build when no document server is
// named, and leaves it out in CI. filex listens on every address here, so the
// document server can download the file and post a save back
// (SHOTS_ONLYOFFICE_CALLBACK_HOST is the name it reaches this machine by).
//
// ⚠ Nothing is saved: the editor tab is closed unchanged. The save itself is
// e2e 199's to prove, not a picture's.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdirSync, mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { syncAndWait } from './fixtures.mjs';
import { addLocalStorage, bootInstance, client, dismissToasts, documentServer, log, mustSay, newContext, shot, signIn, sleep, uploadTree } from './scene.mjs';

const SET = 'csvoffice';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const FILE = 'Q3 budget.csv';
const BUDGET = [
  'Department;Budget;Spent;Owner;Note',
  'Marketing;48000;31250;Lena Fischer;Autumn campaign booked',
  'Engineering;125000;97400;Sam Carter;Two contractors until November',
  'Design;36000;29800;Omar Haddad;Brand refresh, phase 2',
  'Sales;52000;41900;Deniz Kaya;Trade fair in Berlin',
  'Support;27000;18300;Ece Aydın;New help-desk seats',
  'Operations;33000;30100;Dana Reyes;Office move in December',
  'Legal;15000;6200;Dana Reyes;"Contracts; NDAs"',
].join('\r\n') + '\r\n';

/** ONLYOFFICE's spreadsheet is drawn: its frame, then its cell name box. */
async function spreadsheetReady(page) {
  const frame = page.locator('iframe[name^="frameEditor"]');
  await frame.waitFor({ state: 'visible', timeout: 60_000 });
  const inside = page.frameLocator('iframe[name^="frameEditor"]');
  await inside.locator('#ce-cell-name').waitFor({ state: 'visible', timeout: 90_000 });
  if ((await inside.getByText('Choose CSV options').count()) > 0) throw new Error('ONLYOFFICE asked for the CSV options - the config lost the delimiter');
  // The grid is a canvas painted after the toolbar: give it the moment.
  await sleep(2_500);
  return inside;
}

/**
 * ONLYOFFICE's own first-run tips over the editor - "New button: Format" with
 * its "Got it", and the CSV warning it shows itself on opening a CSV for
 * editing (docs/ONLYOFFICE.md → What a CSV cannot keep) - closed, so the
 * picture is of the sheet and filex's line above it. The warning is a
 * closable tip (`.synch-tip-root .close`, measured on Docs 9.4): a click
 * into the sheet, Escape or waiting does not close it.
 */
async function quietEditor(page, inside) {
  const gotIt = inside.getByText('Got it', { exact: true });
  const tips = inside.locator('.synch-tip-root');
  for (let i = 0; i < 30; i++) {
    if (await gotIt.first().isVisible().catch(() => false)) await gotIt.first().click().catch(() => {});
    const close = tips.locator('.close');
    for (let n = (await close.count()) - 1; n >= 0; n--) {
      if (await close.nth(n).isVisible().catch(() => false)) await close.nth(n).click().catch(() => {});
    }
    await sleep(400);
    const shown = (await gotIt.first().isVisible().catch(() => false)) || (await tips.filter({ visible: true }).count()) > 0;
    if (!shown) {
      await page.mouse.move(4, 4);
      await sleep(600);
      return;
    }
    await sleep(500);
  }
  throw new Error("ONLYOFFICE's tips are still over the editor - the picture would show them, not the sheet");
}

async function main() {
  const ds = documentServer();
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' }, office: ds });
  const tmp = mkdtempSync(join(tmpdir(), 'filex-shots-csvoffice-'));
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });

    // The document server answers, by filex's own probe: what the menu and
    // Default apps read.
    let state = '';
    for (let i = 0; i < 60; i++) {
      const caps = await admin.json('/api/files/capabilities');
      state = caps.external?.onlyoffice?.state ?? '';
      if (state === 'ok') break;
      await sleep(1000);
    }
    if (state !== 'ok') throw new Error(`ONLYOFFICE at ${ds.url} does not answer filex's probe (state "${state}")`);

    const seed = join(tmp, 'seed');
    mkdirSync(join(seed, 'Reports'), { recursive: true });
    writeFileSync(join(seed, 'Reports', FILE), BUDGET);
    writeFileSync(join(seed, 'Reports', 'Team contacts.csv'), 'Name;Team;Phone\r\nLena Fischer;Marketing;+49 30 1234 5601\r\nSam Carter;Engineering;+44 20 7946 0102\r\n');
    writeFileSync(join(seed, 'Reports', 'Summary.md'), '# Q3 summary\n\nSpending is on plan in every department.\n');
    const demo = await addLocalStorage(admin, 'demo', inst.storageRoot('demo'), { onHost: !inst.container });
    await uploadTree(admin, 'demo://', seed);
    // A first sync to its end: until then the folder carries "This storage's
    // first sync has not finished", which is not what these pictures are about.
    await syncAndWait((_token, path, init) => admin.call(path, init), null, demo.id);
    await admin.post('/api/notifications/read-all', {});

    const ctx = await newContext(browser, { width: 1440, height: 900 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    const row = () => page.locator(`[data-fe-path="demo://Reports/${FILE}"]`).first();
    const openFolder = async () => {
      await page.goto(`${inst.url}/admin/explore?storage=demo`);
      const folder = page.locator('[data-fe-path="demo://Reports"]').first();
      await folder.waitFor({ timeout: 25_000 });
      await sleep(700);
      await folder.dblclick();
      await row().waitFor({ timeout: 15_000 });
      await sleep(600);
    };

    // ── 1. a double-click: a look in ONLYOFFICE ─────────────────────────
    await openFolder();
    await row().getByText(FILE, { exact: true }).dblclick();
    await spreadsheetReady(page);
    if ((await page.locator('.filex-viewer-csv').count()) > 0) throw new Error('the table opened, not ONLYOFFICE');
    await page.mouse.move(4, 4);
    await dismissToasts(page);
    await shot(page, SET, 'csv-view-1440.png');

    // ── 2. Edit: the editor tab, and what a save as CSV keeps ───────────
    const [tab] = await Promise.all([ctx.waitForEvent('page'), page.locator('button.fe-viewer__act[title="Edit"]').click()]);
    await tab.waitForLoadState();
    if (!tab.url().includes('app=onlyoffice') || !tab.url().includes('mode=edit')) throw new Error(`the Edit tab is ${tab.url()}`);
    const editor = await spreadsheetReady(tab);
    await quietEditor(tab, editor);
    const note = tab.getByTestId('office-csv-note');
    await note.waitFor({ state: 'visible', timeout: 10_000 });
    await mustSay(note, 'the CSV note', ['Saved as CSV: only the values of the active sheet are kept.']);
    await tab.mouse.move(4, 4);
    await shot(tab, SET, 'csv-edit-1440.png');
    await tab.close();
    await page.getByTestId('viewer-close').click();
    await sleep(500);

    // ── 3. the file's menu ───────────────────────────────────────────────
    // Right-clicked beside the name, so the menu does not cover it.
    await row().click({ button: 'right', position: { x: 420, y: 20 } });
    const menu = page.locator('.fe-ctx').last();
    await menu.waitFor({ state: 'visible', timeout: 10_000 });
    await mustSay(menu, 'the file menu', ['Open with ONLYOFFICE', 'Choose an app']);
    await page.getByRole('menuitem', { name: 'Open with ONLYOFFICE' }).hover();
    await sleep(400);
    await shot(page, SET, 'csv-open-with-menu.png');

    // ── 4. Choose an app… ────────────────────────────────────────────────
    await page.getByRole('menuitem', { name: /^Choose an app/ }).click();
    const dialog = page.getByTestId('open-with-dialog');
    await dialog.waitFor({ state: 'visible', timeout: 10_000 });
    await mustSay(dialog, 'Choose an app', ['ONLYOFFICE (spreadsheet editor)', 'Opens it now', 'filex viewer (built-in)']);
    await page.mouse.move(4, 4);
    await sleep(400);
    await shot(page.locator('.fe-modal__card').filter({ has: dialog }).last(), SET, 'csv-choose-app.png');
    await page.keyboard.press('Escape');
    await sleep(400);

    // ── 5. Default apps: .csv, ONLYOFFICE first ──────────────────────────
    await page.goto(`${inst.url}/admin/plugins?tab=defaults`);
    await page.getByTestId('default-apps-tab').waitFor({ timeout: 20_000 });
    const csvRow = page.getByTestId('default-apps-row-csv');
    await csvRow.waitFor({ timeout: 20_000 });
    await mustSay(page.getByTestId('default-apps-open-csv'), 'the .csv row', ['ONLYOFFICE']);
    await csvRow.scrollIntoViewIfNeeded();
    await page.mouse.move(4, 4);
    await sleep(500);
    await shot(page, SET, 'default-apps-csv-1440.png');

    // ── 6. ONLYOFFICE switched off: the table, and the greyed row ────────
    await admin.patch('/api/admin/external/onlyoffice', { enabled: false });
    try {
      for (let i = 0; i < 30; i++) {
        const caps = await admin.json('/api/files/capabilities');
        if (caps.external?.onlyoffice?.state !== 'ok') break;
        await sleep(1000);
      }
      await openFolder();
      await row().click({ button: 'right', position: { x: 420, y: 20 } });
      const off = page.getByRole('menuitem', { name: 'Open with ONLYOFFICE' });
      await off.waitFor({ state: 'visible', timeout: 10_000 });
      if (!(await off.isDisabled())) throw new Error('with ONLYOFFICE off, its row is still enabled');
      const title = (await off.getAttribute('title')) ?? '';
      if (!/ONLYOFFICE is not set up/.test(title)) throw new Error(`the greyed row does not say where to set it up: "${title}"`);
      await off.hover();
      await sleep(1_200);
      await shot(page, SET, 'csv-menu-no-onlyoffice.png');
      await page.keyboard.press('Escape');
    } finally {
      await admin.patch('/api/admin/external/onlyoffice', { enabled: true, url: ds.url, secret: ds.jwt });
    }
    await ctx.close();
    log('a .csv in ONLYOFFICE: the look, the editor, the menu, Choose an app, Default apps, switched off');
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
