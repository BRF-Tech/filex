// The user settings dialog's two parts (task #258, docs/NOTIFICATIONS.md,
// docs/DESKTOP.md):
//
//   node e2e/shots/usersettings.mjs     (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/usersettings/ (or SHOTS_OUT):
//
//   preferences-account-1440.png   Preferences, the Account part: language,
//                                  time zone, the look, the folder view
//   preferences-device-1440.png    Preferences, the This device part: the
//                                  start page, the click, Keyboard shortcuts
//                                  with its Edit… button
//   shortcuts-over-dialog-1440.png the keyboard shortcuts' editor opened from
//                                  that row, over the dialog
//   preferences-remote-images-1440.png Preferences, further down the Account
//                                  part: Load remote images in previews (0.57)
//   notifications-tr-dark-1440.png Notifications in Turkish, in the dark
//                                  theme: Hesabım, the in-app bell's switch
//                                  and the events it tells about
//
// It also MEASURES what a happy-dom test cannot: the editor really is in
// front of the dialog (the topmost element at its centre is the editor's),
// and Escape closes the editor and leaves the dialog open.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { chromium } from '@playwright/test';
import { bootInstance, client, log, mustSay, newContext, setLanguage, shot, signIn, sleep } from './scene.mjs';

const SET = 'usersettings';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };

/** The dialog open on a pane, the page settled. */
async function openPane(page, url, tab) {
  await page.goto(`${url}/admin/dashboard?settings=1`);
  const dialog = page.getByTestId('user-settings-dialog');
  await dialog.waitFor({ timeout: 20_000 });
  await page.getByTestId(`user-settings-tab-${tab}`).click();
  await sleep(400);
  return dialog;
}

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });

    // ── Preferences, English, light ──────────────────────────────────────
    const ctx = await newContext(browser, { width: 1440, height: 1000 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    await setLanguage(admin, page, 'en');
    const dialog = await openPane(page, inst.url, 'preferences');
    await mustSay(page.getByTestId('user-settings-prefs-account'), 'the Account part', ['Account', 'Language', 'Time zone']);
    await shot(dialog, SET, 'preferences-account-1440.png');

    const device = page.getByTestId('user-settings-prefs-device');
    await device.scrollIntoViewIfNeeded();
    await sleep(300);
    await mustSay(device, 'the This device part', ['This device', 'Keyboard shortcuts', 'Edit…']);
    await shot(dialog, SET, 'preferences-device-1440.png');

    // ── the shortcuts' editor over the dialog ────────────────────────────
    await page.getByTestId('user-settings-shortcuts-edit').click();
    const editor = page.locator('.fe-modal__card').filter({ has: page.locator('.fe-shortset') });
    await editor.waitFor({ timeout: 10_000 });
    await sleep(400);
    const box = await editor.boundingBox();
    if (!box) throw new Error('the shortcuts editor has no box');
    const onTop = await page.evaluate(
      ([x, y]) => !!document.elementFromPoint(x, y)?.closest('.fe-shortset'),
      [box.x + box.width / 2, box.y + Math.min(80, box.height / 2)],
    );
    if (!onTop) throw new Error('the shortcuts editor is not in front of the settings dialog');
    // ⚠ The pointer off the dialog: it stayed on the Change button it opened
    // the editor from, drawn hovered in the 0.57 picture.
    await page.mouse.move(4, 4);
    await sleep(200);
    await shot(page, SET, 'shortcuts-over-dialog-1440.png');
    await page.keyboard.press('Escape');
    await sleep(400);
    if (await editor.count()) throw new Error('Escape did not close the shortcuts editor');
    if (!(await dialog.isVisible())) throw new Error('Escape closed the settings dialog under the editor too');

    // ── Preferences, further down the Account part (0.57) ───────────────
    // Below the fold of preferences-account-1440.png: its own picture, taken
    // last so the pictures above keep the scroll they were taken at.
    const remote = page.getByTestId('user-settings-remote-images-row');
    await remote.evaluate((el) => el.scrollIntoView({ block: 'center' }));
    await page.mouse.move(4, 4);
    await sleep(300);
    await mustSay(remote, 'the remote images row', ['Load remote images in previews']);
    await shot(dialog, SET, 'preferences-remote-images-1440.png');
    await ctx.close();

    // ── Notifications, Turkish, dark ─────────────────────────────────────
    const dark = await newContext(browser, { scheme: 'dark', width: 1440, height: 1000 });
    const dpage = await dark.newPage();
    await signIn(dpage, inst.url, ADMIN);
    await setLanguage(admin, dpage, 'tr');
    const ddialog = await openPane(dpage, inst.url, 'notifications');
    await mustSay(ddialog, 'the Turkish Notifications pane', ['Hesabım', 'Bu cihaz']);
    await shot(ddialog, SET, 'notifications-tr-dark-1440.png');
    await setLanguage(admin, dpage, 'en');
    await dark.close();

    log('Preferences in two parts, the shortcuts editor on top, Notifications in Turkish dark');
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
