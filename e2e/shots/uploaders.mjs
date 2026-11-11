// Uploaders (0.60, docs/UPLOADERS.md): the Uploaders tab of the user settings
// dialog, the key a new uploader is given once, and the page its deletion
// link opens.
//
//   node e2e/shots/uploaders.mjs     (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/uploaders/ (or SHOTS_OUT):
//
//   uploaders-1440.png          the tab, whole: the uploaders' table over
//                               the form (name, storage, folder, public or
//                               private, link life with the server's ceiling)
//   uploaders-form-1440.png     the same tab scrolled down: Link and Link
//                               life, the ceiling said, Create uploader
//   uploaders-key-dark-1440.png in the dark theme, right after Create: the key
//                               shown once, Download ShareX settings, the
//                               curl line
//   uploader-delete-1440.png    the page an upload's deletion link opens: the
//                               file's name and one Delete button
//
// It also MEASURES the round trip a person makes: the uploader is made in the
// browser, the request ShareX sends (multipart `file`, the key as a bearer)
// is sent with the key the server answered, the link opens, the deletion
// link's GET deletes nothing and its button moves the file to the trash.
//
// ⚠ The key in the pictures is a fixed example (the answer is rewritten on
// its way to the page): a real key would make the picture change on every
// run, and a picture of a credential is not one to publish. The round trip
// uses the real one.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { existsSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { addLocalStorage, bootInstance, client, dismissToasts, log, mustSay, newContext, shootWhole, shot, signIn, sleep } from './scene.mjs';

export const INPUTS = [
  'packages/core/src/components/UploadersPanel.vue',
  'packages/core/src/components/UserSettingsDialog.vue',
  'packages/core/src/components/DataTable.vue',
  'packages/core/src/components/ChoiceButtons.vue',
  'packages/core/src/components/ChoiceSelect.vue',
  'packages/core/src/lib/userSettingsHost.ts',
  'packages/core/src/types/Uploaders.ts',
  'web/src/components/UserSettingsModal.vue',
  'backend/internal/api/handlers/uploaders.go',
  'backend/internal/api/handlers/uploads.go',
  'backend/internal/api/handlers/upload_delete.go',
  'backend/internal/api/handlers/share.go',
  // The deletion page is a template (page.delete_confirm in page.layout).
  'backend/internal/api/handlers/public_pages.go',
  'backend/internal/templates',
];

const SET = 'uploaders';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
/** What the pictures show instead of the key the server made. */
const EXAMPLE_KEY = 'fxu_EXAMPLE-shown-once-copy-it-now-4Qm7Zt2Lw9';

async function openTab(page, url) {
  await page.goto(`${url}/admin/dashboard?settings=1`);
  const dialog = page.getByTestId('user-settings-dialog');
  await dialog.waitFor({ timeout: 20_000 });
  await page.getByTestId('user-settings-tab-uploaders').click();
  await page.getByTestId('uploaders-panel').waitFor({ timeout: 10_000 });
  await sleep(400);
  return dialog;
}

/** The PNG ShareX would send: a valid 1x1 image. */
const PNG = Buffer.from(
  '89504e470d0a1a0a0000000d4948445200000001000000010806000000' +
    '1f15c4890000000d49444154789c6360f8cf00000301010018dd8db40000000049454e44ae426082',
  'hex',
);

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });
    const root = inst.storageRoot('team');
    await addLocalStorage(admin, 'team', root);

    // Two uploaders already there, so the table says what one row says.
    await admin.post('/api/auth/uploaders', { name: 'Phone shortcut', storage: 'team', folder: 'Phone', visibility: 'private' });
    await admin.post('/api/auth/uploaders', { name: 'Build bot', storage: 'team', folder: 'builds', visibility: 'public', link_days: 3 });

    // ── the tab, light ────────────────────────────────────────────────────
    const ctx = await newContext(browser, { width: 1440, height: 1000 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    const dialog = await openTab(page, inst.url);
    await page.getByTestId('uploaders-name').fill('Screenshots');
    await page.mouse.move(4, 4);
    await mustSay(dialog, 'the Uploaders tab', ['Uploaders', 'Public link', 'Private link', 'Link life', 'Phone shortcut', 'Build bot']);
    await shootWhole(page, dialog, SET, 'uploaders-1440.png');
    // The dialog is a fixed height and the pane scrolls: the form's lower
    // half (the link and its life) is a picture of its own.
    await page.locator('.fx-us__body').evaluate((el) => el.scrollTo(0, el.scrollHeight));
    await sleep(300);
    await mustSay(dialog, 'the lower half of the form',['Link life', 'Longest allowed', 'at most 7 days', 'Create uploader']);
    await shot(dialog, SET, 'uploaders-form-1440.png');
    await ctx.close();

    // ── made in the browser, dark: the key once ──────────────────────────
    const dark = await newContext(browser, { scheme: 'dark', width: 1440, height: 1000 });
    const dpage = await dark.newPage();
    let real = null;
    await dpage.route('**/api/auth/uploaders', async (route) => {
      if (route.request().method() !== 'POST') return route.continue();
      const res = await route.fetch();
      const body = await res.json();
      real = body;
      const shown = JSON.parse(JSON.stringify(body).split(body.key).join(EXAMPLE_KEY));
      await route.fulfill({ response: res, json: shown });
    });
    await signIn(dpage, inst.url, ADMIN);
    const ddialog = await openTab(dpage, inst.url);
    await dpage.getByTestId('uploaders-name').fill('Screenshots');
    await dpage.getByTestId('uploaders-submit').click();
    await dpage.getByTestId('uploaders-key').waitFor({ timeout: 10_000 });
    if (!real?.key?.startsWith('fxu_')) throw new Error('the server answered no key');
    // The key must be ON SCREEN, not merely in the pane: Create sits at the
    // foot of the form and the key at the head of the pane.
    await sleep(400);
    const keyBox = await dpage.getByTestId('uploaders-key').boundingBox();
    const paneBox = await dpage.locator('.fx-us__body').boundingBox();
    if (!keyBox || !paneBox || keyBox.y < paneBox.y - 1 || keyBox.y > paneBox.y + paneBox.height - 60) {
      throw new Error('the key that was just made is off screen');
    }
    await dismissToasts(dpage);
    await dpage.mouse.move(4, 4);
    await mustSay(ddialog, 'the key shown once', ['Download ShareX settings (.sxcu)', EXAMPLE_KEY, 'curl -H']);
    await shootWhole(dpage, ddialog, SET, 'uploaders-key-dark-1440.png');

    // The settings file is the server's: one uploader for images, text and files.
    const sx = real.setup.sharex;
    if (sx.DestinationType !== 'ImageUploader, TextUploader, FileUploader') throw new Error(`sxcu DestinationType ${sx.DestinationType}`);
    if (sx.DeletionURL !== '{json:deletion_url}' || sx.URL !== '{json:url}') throw new Error('sxcu does not read the door\'s answer');
    await dark.close();

    // ── the request ShareX sends, with the real key ──────────────────────
    const form = new FormData();
    form.append('file', new Blob([PNG], { type: 'image/png' }), 'capture.png');
    const up = await fetch(`${inst.url}/api/uploads`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${real.key}` },
      body: form,
    });
    const ans = await up.json();
    if (up.status !== 200) throw new Error(`upload: ${up.status} ${JSON.stringify(ans)}`);
    const local = (u) => u.replace(/^https?:\/\/[^/]+/, inst.url);
    const opened = await fetch(local(ans.url));
    if (opened.status !== 200 || !(opened.headers.get('content-disposition') ?? '').includes('inline')) {
      throw new Error(`the link did not open inline: ${opened.status}`);
    }
    const folder = join(root, 'uploads');
    const stored = readdirSync(folder).filter((n) => n.endsWith('-capture.png'));
    if (stored.length !== 1) throw new Error(`expected one stored capture, found ${stored.join(', ')}`);

    // ── the deletion link: GET asks, POST deletes ────────────────────────
    const lctx = await newContext(browser, { width: 1440, height: 900 });
    const lpage = await lctx.newPage();
    // The stored name has a random prefix: the picture shows a fixed one, so
    // it does not change on every run (the page itself is the server's).
    const EXAMPLE_NAME = '3f9a1c-capture.png';
    await lpage.route(local(ans.deletion_url), async (route) => {
      if (route.request().method() !== 'GET') return route.continue();
      const res = await route.fetch();
      const html = (await res.text()).split(stored[0]).join(EXAMPLE_NAME);
      await route.fulfill({ response: res, body: html });
    });
    // A server-rendered page (handlers/upload_delete.go): found by its role,
    // not by a test id, which the interface scan of web/tests cannot see.
    await lpage.goto(local(ans.deletion_url));
    await lpage.getByRole('button', { name: 'Delete' }).waitFor({ timeout: 10_000 });
    if (!existsSync(join(folder, stored[0]))) throw new Error('opening the deletion link deleted the file');
    await mustSay(lpage.locator('main'), 'the deletion page', ['Delete this file?', EXAMPLE_NAME, 'Delete']);
    await lpage.mouse.move(4, 4);
    await shot(lpage, SET, 'uploader-delete-1440.png');
    await lpage.getByRole('button', { name: 'Delete' }).click();
    await lpage.waitForLoadState('load');
    await mustSay(lpage.locator('main'), 'the done page', ['File deleted']);
    if (existsSync(join(folder, stored[0]))) throw new Error('the deletion link\'s button left the file in its folder');
    const after = await fetch(local(ans.url));
    if (after.status === 200) throw new Error('the public link still opens after the deletion');
    await lctx.close();

    log('the Uploaders tab, the key once (dark), the deletion page; the upload, the link and the deletion measured');
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
