// An app edits a file of an end-to-end encrypted folder or of a vault - in
// the browser, the server holding only ciphertext (filex 0.56, #189,
// files:e2e-plaintext; docs/E2E-ENCRYPTION.md → Editing in an app):
//
//   node e2e/shots/e2eapp.mjs     (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/e2eapp/ (or SHOTS_OUT):
//
//   open-with-encrypted-1280.png  Choose an app… on a file of a folder whose
//                                 names are encrypted (level 2): the name the
//                                 browser decrypted, the office app beside
//                                 filex's own viewer, and no "Always use this
//                                 app" (that choice is kept on the account, and
//                                 would tell the server the kind)
//   app-encrypted-folder-1280.png the file open in the app: the document in
//                                 the clear in the app's frame, decrypted by
//                                 the browser
//   vault-lock-soon-1280.png      the app on a file of a VAULT with changes it
//                                 has not saved, a minute before the vault
//                                 locks itself: filex's warning in the
//                                 server's words, and "Keep it open"
//
// The vault's minute is reached on the page's own clock (Playwright's
// clock.fastForward, the clock every scene runs on - clock.mjs), not by
// waiting fourteen minutes; the warning is asked of a fresh tab that opened
// the vault read-only, as a person coming back to it would (a tab that holds
// the vault's write lock goes back to read-only first).
//
// ⚠ The instance is booted with FILEX_E2E_VAULT=1 (vault.mjs: the vault is off
// by default). The app is ./office-app, installed through the API here; its
// review is officeapp.mjs's picture. The documents are uploaded THROUGH THE
// BROWSER, which encrypts them, by path with their fixed fixture time
// (lesson: a buffer is dated the real now, and in a vault that is the
// Modified time).
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { DOCS, packOfficeApp, writeDocx } from './office-app/pack.mjs';
import {
  addLocalStorage,
  bootInstance,
  client,
  dismissToasts,
  installInterfaceApp,
  log,
  mustSay,
  newContext,
  shot,
  signIn,
  sleep,
} from './scene.mjs';

const SET = 'e2eapp';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const PW = 'correct horse battery staple';
const APP = 'office';
const FOLDER = 'Contracts';
const VAULT = 'Vault';
const AGREEMENT = 'Supplier agreement.docx';
const MINUTES = 'Board minutes - September.docx';

async function openTeam(page, url) {
  await page.goto(`${url}/drive/explore#${encodeURIComponent('Team')}`);
  await page.locator('.fe__body').first().waitFor({ state: 'visible', timeout: 20_000 });
  await page.locator('.fe__body [aria-busy="true"]').first().waitFor({ state: 'detached', timeout: 20_000 });
  await sleep(1000);
}

function row(page, name) {
  return page.locator('.fe-list__row[data-fe-path]').filter({ has: page.locator('.fe-list__name', { hasText: name }) }).first();
}

async function openFolder(page, name) {
  await row(page, name).dblclick();
  await sleep(900);
}

/** New folder → Create encrypted folder…, at `level` (names / vault). */
async function createEncrypted(page, name, level) {
  await page.getByTestId('sidenav-new').first().click();
  await page.locator('.fe-ctx__item', { has: page.locator('.fe-ctx__label', { hasText: /^New folder$/ }) }).first().click();
  await page.getByText('Create encrypted folder…', { exact: true }).click();
  const dialog = page.locator('.fe-modal__card').filter({ has: page.getByTestId('e2e-create-names') });
  await dialog.waitFor({ state: 'visible', timeout: 10_000 });
  await dialog.locator('input[type="text"]').first().fill(name);
  const pws = dialog.locator('input[type="password"]');
  await pws.nth(0).fill(PW);
  await pws.nth(1).fill(PW);
  await dialog.getByTestId(`e2e-level-${level}`).check();
  await dialog.getByTestId('e2e-create-ack').check();
  await dialog.getByRole('button', { name: 'Create encrypted folder', exact: true }).click();
  await page.locator('.fe-e2e-rk__key').waitFor({ state: 'visible', timeout: 30_000 });
  await page.locator('.fe-e2e-rk .fe-e2e-ack input[type="checkbox"]').check();
  await page.getByRole('button', { name: 'Done', exact: true }).click();
  await row(page, name).waitFor({ state: 'visible', timeout: 20_000 });
  await sleep(600);
}

/** The app's frame, connected and drawn. */
async function appFrame(page) {
  const el = page.locator(`.fe-appframe[data-app="${APP}"] iframe`).first();
  await page.locator(`.fe-appframe[data-app="${APP}"][data-connected="true"]`).waitFor({ timeout: 30_000 });
  const frame = await (await el.elementHandle()).contentFrame();
  if (!frame) throw new Error('the app frame has no document');
  await frame.waitForFunction(() => document.body.dataset.ready === '1' || !!document.body.dataset.error, null, { timeout: 30_000 });
  const failed = await frame.evaluate(() => document.body.dataset.error ?? null);
  if (failed) throw new Error(`the office app did not start: ${failed}`);
  return frame;
}

async function still(frame) {
  await frame.evaluate(() => {
    document.activeElement?.blur?.();
    document.getSelection()?.removeAllRanges();
  });
}

async function main() {
  const work = mkdtempSync(join(tmpdir(), 'filex-shots-e2eapp-'));
  const office = packOfficeApp(work);
  const agreement = join(work, AGREEMENT);
  writeDocx(agreement, DOCS.agreement, `${SET}/${AGREEMENT}`);
  const minutes = join(work, MINUTES);
  writeDocx(minutes, DOCS.minutes, `${SET}/${MINUTES}`);

  // FILEX_PUBLIC_URL emptied, as in officeapp.mjs: the app joins the file's
  // editing session, whose socket must reach this filex.
  const inst = await bootInstance({
    name: SET,
    admin: ADMIN,
    env: { FILEX_UPDATE_CHECK: '0', FILEX_E2E_VAULT: '1', FILEX_PUBLIC_URL: '' },
  });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });
    await addLocalStorage(admin, 'Team', inst.storageRoot('team'), { onHost: !inst.container });
    const caps = await admin.json('/api/files/capabilities');
    if (caps?.e2e_vault !== true) throw new Error('the instance does not offer the vault (FILEX_E2E_VAULT)');
    if (caps?.apps_e2e_plaintext !== true) throw new Error('apps may not open encrypted files here (apps.e2e_plaintext)');
    await installInterfaceApp(admin, office, { expect: ['files:e2e-plaintext', 'files:co-edit'] });

    // ── 1. a folder whose names are encrypted: Choose an app… ───────────
    const ctx = await newContext(browser, { width: 1280, height: 860 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    await openTeam(page, inst.url);
    await createEncrypted(page, FOLDER, 'names');
    await openFolder(page, FOLDER);
    await page.locator('input[type="file"]').first().setInputFiles([agreement]);
    await row(page, AGREEMENT).waitFor({ state: 'visible', timeout: 60_000 });
    await dismissToasts(page);
    await sleep(700);
    await row(page, AGREEMENT).locator('.fe-list__name').click({ button: 'right' });
    // By role: a menu entry's id is set on the entry object, which the
    // screenshot gate does not read (defaultapps.mjs).
    const choose = page.getByRole('menuitem', { name: /^Choose an app/ });
    await choose.waitFor({ timeout: 10_000 });
    await choose.click();
    const dialog = page.getByTestId('open-with-dialog');
    await dialog.waitFor({ timeout: 10_000 });
    await mustSay(dialog, 'Choose an app…', [AGREEMENT, 'Office']);
    if ((await page.getByTestId('open-with-always').count()) !== 0) {
      throw new Error('Choose an app… offers "Always use this app" for a file whose name is encrypted - that choice would tell the server the kind');
    }
    await dialog.getByRole('radio', { name: /Office/ }).click();
    await page.mouse.move(4, 4);
    await sleep(300);
    await shot(page.locator('.fe-modal__card').filter({ has: dialog }), SET, 'open-with-encrypted-1280.png');
    await page.getByTestId('open-with-open').click();

    // ── 2. …the file in the app, decrypted by the browser ───────────────
    const frame = await appFrame(page);
    const lock = (await frame.locator('#lock').innerText()).trim();
    if (!/Encrypted folder/.test(lock)) throw new Error(`the app was not handed an encrypted file in the clear (its bar says "${lock}")`);
    await mustSay(frame.locator('#page'), 'the decrypted document', [DOCS.agreement.title, 'Confidentiality']);
    await still(frame);
    await dismissToasts(page);
    await page.mouse.move(4, 4);
    await sleep(500);
    await shot(page, SET, 'app-encrypted-folder-1280.png');
    await page.getByTestId('viewer-close').click();
    await sleep(500);

    // ── 3. a vault, and the minute before it locks itself ───────────────
    await page.goto('about:blank');
    await openTeam(page, inst.url);
    await createEncrypted(page, VAULT, 'vault');
    await openFolder(page, VAULT);
    await page.getByTestId('e2e-vault-strip').waitFor({ state: 'visible', timeout: 20_000 });
    await page.locator('input[type="file"]').first().setInputFiles([minutes]);
    await row(page, MINUTES).waitFor({ state: 'visible', timeout: 60_000 });
    await ctx.close();

    // A fresh tab opens the vault read-only, like a person coming back to it.
    const vctx = await newContext(browser, { width: 1280, height: 860 });
    const vpage = await vctx.newPage();
    await signIn(vpage, inst.url, ADMIN);
    await openTeam(vpage, inst.url);
    await openFolder(vpage, VAULT);
    const lockScreen = vpage.locator('.fe-e2e-lock');
    await lockScreen.waitFor({ state: 'visible', timeout: 20_000 });
    await lockScreen.locator('input[type="password"]').fill(PW);
    await lockScreen.getByRole('button', { name: 'Unlock', exact: true }).click();
    await vpage.getByTestId('e2e-vault-strip').waitFor({ state: 'visible', timeout: 30_000 });
    await sleep(600);
    await row(vpage, MINUTES).dblclick();
    const vframe = await appFrame(vpage);
    const vlock = (await vframe.locator('#lock').innerText()).trim();
    if (!/vault/i.test(vlock)) throw new Error(`the app was not handed the vault's file in the clear (its bar says "${vlock}")`);
    // A change the app has not saved: it says so (ui.dirty), which is
    // activity in the vault, and the vault's own lock counts from there.
    const decisions = vframe.locator('#page p').last();
    await decisions.click();
    await vpage.keyboard.press('End');
    await vpage.keyboard.type(' The new office opens in March.', { delay: 15 });
    await vframe.waitForFunction(() => document.getElementById('state').textContent === 'Not saved', null, { timeout: 10_000 });
    await still(vframe);
    await dismissToasts(vpage);
    // Fourteen minutes and ten seconds on, on the page's clock.
    await vpage.clock.fastForward('14:10');
    const warning = vpage.locator('.fe-toast', { hasText: `An app has changes in “${VAULT}” that are not saved, and the vault locks itself in a minute.` });
    await warning.waitFor({ state: 'visible', timeout: 20_000 });
    await warning.getByRole('button', { name: 'Keep it open', exact: true }).waitFor({ timeout: 5_000 });
    await vpage.mouse.move(4, 4);
    await sleep(500);
    await shot(vpage, SET, 'vault-lock-soon-1280.png');
    // …and "Keep it open" keeps it: the server's word for that.
    await warning.getByRole('button', { name: 'Keep it open', exact: true }).dispatchEvent('click');
    await vpage.locator('.fe-toast', { hasText: `“${VAULT}” stays open` }).waitFor({ state: 'visible', timeout: 10_000 });
    log('an app opens a file of an encrypted folder and of a vault; a minute before the vault locks, its unsaved changes are warned about');
    await vctx.close();
  } finally {
    await browser.close();
    await inst.stop();
    rmSync(work, { recursive: true, force: true });
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
