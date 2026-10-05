// Default apps (0.50) - which app opens a kind of file and which draws its
// thumbnail, and the app that draws thumbnails filex cannot:
//
//   node e2e/shots/defaultapps.mjs        (from the repo root; `pnpm shots` runs it)
//
// Writes docs/screenshots/<release>/defaultapps/ (the release named in ./release.mjs):
//
//   install-file-types-1440.png    Plugins → Apps → Install, the review of the
//                                  example app pkglist: its permission per kind
//                                  and the File types group
//   explorer-app-thumbnails-1440.png  a folder of packages (.jar, .apk, .whl,
//                                  .vsix) drawn by that app, in the grid
//   default-apps-1440.png          Plugins → Default apps: every kind something
//                                  besides filex handles, one with a rule
//   file-type-editor.png           Edit on .board: who opens it and in which
//                                  order, switches and arrows
//   app-thumbnails.png             the app's page: the kinds it draws and its
//                                  four limits
//   open-with-menu.png             a .board file's menu: Open with each handler
//                                  that is on, and Choose an app…
//   open-with-dialog.png           Choose an app…, "Always use this app"
//   settings-default-apps.png      Settings → Preferences → Default apps, a
//                                  choice the administrator switched off since
//   thumb-repair-handlers-1440.png Tools → Thumbnail repair: who drew the
//                                  thumbnails, and the handlers asked for a file
//                                  nobody could draw
//
// The apps are built from this tree: the board viewer of ./board-app (packed
// as apps.mjs packs it) and backend/examples/app-pkglist, compiled to
// WebAssembly here (scripts/lib/go-build.mjs - native Go, or WSL).
//
// Environment:
//   FILEX_BIN       binary to run (default bin/filex[.exe])
//   SHOTS_OUT       write here instead of the release folder
//   SHOTS_DRY_RUN=1 walk every scene to its picture and write nothing
//   SHOTS_KEEP=1    leave the instance running afterwards

import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { deflateRawSync } from 'node:zlib';
import { chromium } from '@playwright/test';
import { goBuild } from '../../scripts/lib/go-build.mjs';
import { syncAndWait, zipStored } from './fixtures.mjs';
import { packBoardApp } from './board-app/pack.mjs';
import { addLocalStorage, bootInstance, client, dismissToasts, log, newContext, shootWhole, shot, signIn, sleep, uploadTree } from './scene.mjs';

const SET = 'defaultapps';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const HERE = dirname(fileURLToPath(import.meta.url));
const REPO = join(HERE, '..', '..');
const PKGLIST = join(REPO, 'backend', 'examples', 'app-pkglist');

const BOARD_FILE = 'Launch plan.board';
const BOARD = {
  title: 'Launch plan',
  columns: [
    { title: 'To do', cards: [{ title: 'Write the release notes', tag: 'docs' }] },
    { title: 'Done', cards: [{ title: 'Security review', tag: 'security' }] },
  ],
};

/** A zip holding `names` (stored), what a package is under its own name. */
function pkg(names) {
  return zipStored(names.map((name) => ({ name, data: Buffer.from(`${name}\n`) })));
}

const PACKAGES = {
  'billing-service-2.4.1.jar': pkg([
    'META-INF/MANIFEST.MF',
    'META-INF/maven/com.example/billing/pom.xml',
    'com/example/billing/Invoice.class',
    'com/example/billing/InvoiceService.class',
    'com/example/billing/Tax.class',
    'application.yml',
    'logback.xml',
  ]),
  'field-notes-1.3.apk': pkg([
    'AndroidManifest.xml',
    'classes.dex',
    'classes2.dex',
    'resources.arsc',
    'res/layout/main.xml',
    'res/drawable/icon.png',
    'META-INF/CERT.RSA',
    'lib/arm64-v8a/libnotes.so',
  ]),
  'reportgen-0.9.0-py3-none-any.whl': pkg([
    'reportgen/__init__.py',
    'reportgen/pdf.py',
    'reportgen/charts.py',
    'reportgen-0.9.0.dist-info/METADATA',
    'reportgen-0.9.0.dist-info/RECORD',
    'reportgen-0.9.0.dist-info/WHEEL',
  ]),
  'theme-dusk-1.0.2.vsix': pkg([
    'extension.vsixmanifest',
    '[Content_Types].xml',
    'extension/package.json',
    'extension/themes/dusk-color-theme.json',
    'extension/README.md',
    'extension/LICENSE.txt',
  ]),
};

/** Install through the admin API: the review first, then exactly what it asked for. */
async function installVia(admin, parts) {
  const form = (grant) => {
    const fd = new FormData();
    for (const [field, file] of Object.entries(parts)) fd.append(field, new Blob([readFileSync(file)]), field === 'manifest' ? 'filex-app.json' : `${field}.bin`);
    fd.append('grant', JSON.stringify({ permissions: grant }));
    return fd;
  };
  const dry = await admin.call('/api/admin/app-plugins?dry_run=1', { method: 'POST', body: form([]) });
  if (!dry.ok) throw new Error(`review: ${dry.status} ${(await dry.text()).slice(0, 300)}`);
  const perms = (await dry.json()).permissions.map((p) => p.id);
  const res = await admin.call('/api/admin/app-plugins', { method: 'POST', body: form(perms) });
  if (!res.ok) throw new Error(`install: ${res.status} ${(await res.text()).slice(0, 300)}`);
  return res.json();
}

async function main() {
  const tmp = mkdtempSync(join(tmpdir(), 'filex-shots-defaultapps-'));
  const inst = await bootInstance({ name: 'defaultapps', admin: ADMIN });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);

    // The example app, compiled to WebAssembly from this tree.
    const wasm = join(tmp, 'pkglist.wasm');
    goBuild({ cwd: join(REPO, 'backend'), pkg: './examples/app-pkglist', out: wasm, goos: 'wasip1', goarch: 'wasm', buildmode: 'c-shared', log });
    const board = packBoardApp(tmp);

    // The storage: a board, packages and one that is not a package after all.
    const seed = join(tmp, 'seed');
    mkdirSync(join(seed, 'Documents'), { recursive: true });
    writeFileSync(join(seed, 'Documents', BOARD_FILE), `${JSON.stringify(BOARD, null, 2)}\n`);
    const demo = await addLocalStorage(admin, 'demo', inst.storageRoot('demo'), { onHost: !inst.container });
    await uploadTree(admin, 'demo://', seed);
    // ⚠ One finished sync: uploads never finish a storage's FIRST sync, and
    // until it has, every folder here carried "This storage's first sync has
    // not finished" above its listing - in two of this set's pictures from
    // v0.50.0 to v0.51.0 (appearance.mjs does the same).
    await syncAndWait((_token, path, init) => admin.call(path, init), null, demo.id);
    await installVia(admin, { manifest: board.manifestPath, ui: board.uiZip });

    const ctx = await newContext(browser, { width: 1440, height: 900 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);

    // ── 1. the install review of an app that draws thumbnails ────────────
    await page.goto(`${inst.url}/admin/plugins?tab=apps`);
    await page.getByTestId('app-plugins').waitFor();
    await page.getByTestId('app-plugin-add').click();
    await page.getByTestId('app-plugin-wizard').waitFor();
    await page.getByTestId('app-plugin-source-file').click();
    await page.getByTestId('app-plugin-manifest').setInputFiles(join(PKGLIST, 'filex-app.json'));
    await page.getByTestId('app-plugin-wasm').setInputFiles(wasm);
    await page.getByTestId('app-plugin-review').click();
    await page.getByTestId('app-plugin-permissions').waitFor({ timeout: 60_000 });
    const types = page.getByTestId('install-file-types');
    await types.waitFor({ timeout: 10_000 });
    await page.getByTestId('install-file-type-thumbnail-jar').waitFor();
    await sleep(400);
    await shootWhole(page, page.locator('[role="dialog"]').filter({ has: types }), SET, 'install-file-types-1440.png', { restore: { width: 1440, height: 900 } });
    await page.getByLabel(/I understand/).check();
    await page.getByTestId('app-plugin-install').click();
    await page.getByTestId('app-plugin-pkglist').waitFor({ timeout: 90_000 });
    await page.keyboard.press('Escape');
    await sleep(300);

    // ── 2. packages drawn by the app ─────────────────────────────────────
    const pkgs = join(tmp, 'packages');
    mkdirSync(join(pkgs, 'Packages'), { recursive: true });
    for (const [name, body] of Object.entries(PACKAGES)) writeFileSync(join(pkgs, 'Packages', name), body);
    // Not a package after all: the app cannot read it, and filex has nobody else.
    writeFileSync(join(pkgs, 'Packages', 'broken-download.jar'), deflateRawSync(Buffer.from('half a download')));
    await uploadTree(admin, 'demo://', pkgs);
    await page.goto(`${inst.url}/admin/explore?storage=demo`);
    const folder = page.locator('[data-fe-path="demo://Packages"]').first();
    await folder.waitFor({ timeout: 25_000 });
    await sleep(700);
    await folder.dblclick();
    await page.locator('[data-fe-path="demo://Packages/billing-service-2.4.1.jar"]').first().waitFor({ timeout: 15_000 });
    // Wait until every package has its picture (drawn at upload, or by the
    // listing's refresher), then shoot.
    for (let i = 0; i < 60; i++) {
      const drawn = await page.locator('.fe-grid img[src^="blob:"], .fe-grid img[src*="/thumb/"]').count();
      if (drawn >= Object.keys(PACKAGES).length) break;
      await sleep(1000);
      if (i % 10 === 9) await page.reload();
    }
    await dismissToasts(page);
    await sleep(800);
    await shot(page, SET, 'explorer-app-thumbnails-1440.png');

    // ── 3. Default apps, with one kind given a rule ───────────────────────
    const put = await admin.call('/api/admin/file-types/board', {
      method: 'PUT',
      body: JSON.stringify({ open: { order: ['app:board/board', 'builtin'], off: [] } }),
    });
    if (!put.ok) throw new Error(`file-types put: ${put.status} ${await put.text()}`);
    await page.goto(`${inst.url}/admin/plugins?tab=defaults`);
    await page.getByTestId('default-apps-tab').waitFor({ timeout: 20_000 });
    await page.getByTestId('default-apps-row-jar').waitFor({ timeout: 20_000 });
    await sleep(500);
    await shot(page, SET, 'default-apps-1440.png');

    await page.getByTestId('default-apps-edit-board').click();
    await page.getByTestId('default-apps-edit-board-edit').click();
    const editor = page.getByTestId('file-type-editor');
    await editor.waitFor();
    await sleep(400);
    await shot(page.locator('[role="dialog"]').filter({ has: editor }).first(), SET, 'file-type-editor.png');
    await page.keyboard.press('Escape');
    await sleep(300);

    // ── 4. the app's page: what it draws, and its limits ─────────────────
    // An app's page is addressed by its name (router: plugins/apps/:name).
    await page.goto(`${inst.url}/admin/plugins/apps/pkglist`);
    const limits = page.getByTestId('app-thumb-limits');
    await limits.waitFor({ timeout: 20_000 });
    await limits.scrollIntoViewIfNeeded();
    await sleep(400);
    await shot(limits, SET, 'app-thumbnails.png');

    // ── 5. Open with, on a board ─────────────────────────────────────────
    await page.goto(`${inst.url}/admin/explore?storage=demo`);
    const docs = page.locator('[data-fe-path="demo://Documents"]').first();
    await docs.waitFor({ timeout: 25_000 });
    await sleep(700);
    await docs.dblclick();
    const row = page.locator(`[data-fe-path="demo://Documents/${BOARD_FILE}"]`).first();
    await row.waitFor({ timeout: 15_000 });
    await sleep(700);
    await row.click({ button: 'right' });
    // By role: a menu entry's id is set on the entry object, which the
    // screenshot gate does not read (web/tests/deploy/shotsFixtures.test.ts).
    const choose = page.getByRole('menuitem', { name: /^Choose an app/ });
    await choose.waitFor({ timeout: 10_000 });
    await sleep(300);
    await shot(page, SET, 'open-with-menu.png');
    await choose.click();
    const dialog = page.getByTestId('open-with-dialog');
    await dialog.waitFor();
    await dialog.getByRole('radio', { name: /built-in/ }).click();
    await page.getByTestId('open-with-always').check();
    await sleep(300);
    await shot(page.locator('.fe-modal__card').filter({ has: dialog }), SET, 'open-with-dialog.png');
    await page.getByTestId('open-with-open').click();
    await sleep(800);
    await page.keyboard.press('Escape');

    // ── 6. the person's choices, one switched off since ─────────────────
    const off = await admin.call('/api/admin/file-types/board', {
      method: 'PUT',
      body: JSON.stringify({ open: { order: ['app:board/board'], off: ['builtin'] } }),
    });
    if (!off.ok) throw new Error(`file-types put: ${off.status} ${await off.text()}`);
    await page.goto(`${inst.url}/admin/dashboard?settings=1`);
    await page.getByTestId('user-settings-dialog').waitFor({ timeout: 15_000 });
    await page.getByTestId('user-settings-tab-preferences').click();
    await page.getByTestId('user-settings-app-gone-board').waitFor({ timeout: 10_000 });
    // Default apps is the last part of Preferences: brought into view.
    await page.getByTestId('user-settings-apps').scrollIntoViewIfNeeded();
    await sleep(400);
    await shot(page.getByTestId('user-settings-dialog'), SET, 'settings-default-apps.png');
    await page.keyboard.press('Escape');

    // ── 7. who drew the thumbnails, and a file nobody could ──────────────
    await page.goto(`${inst.url}/admin/tools?tab=thumbnails`);
    await page.getByTestId('thumb-generators').waitFor({ timeout: 20_000 });
    for (let i = 0; i < 30; i++) {
      if (await page.locator('[data-testid^="thumb-problem-actions-"]').count()) break;
      await sleep(1000);
      await page.getByTestId('thumb-problems-refresh').click().catch(() => {});
    }
    await sleep(600);
    await shot(page, SET, 'thumb-repair-handlers-1440.png');

    await ctx.close();
  } finally {
    await browser.close();
    await inst.stop();
    rmSync(tmp, { recursive: true, force: true });
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
