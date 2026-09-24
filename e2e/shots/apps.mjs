// Apps (app plugins) — what an administrator and a person meet first: the
// install wizard stopped at its permission review, an installed app's detail,
// and the converter's wizard on a photo.
//
//   node e2e/shots/apps.mjs        (from the repo root; `pnpm shots` runs it)
//
// Writes docs/screenshots/<release>/apps/ (the release named in ./release.mjs):
//
//   apps-install-review-1440.png  Plugins → Apps → Install, stopped at the review
//                                 of every permission filex-sign asks for
//   apps-detail-1440.png          the installed app's detail: where it came from,
//                                 what was granted, its settings and actions
//   convert-wizard-1440.png       Convert… on a photo — three steps, the target
//                                 picker headed by category
//
// The apps are the real builds of BRF-Tech/filex-sign and BRF-Tech/filex-convert,
// found where the e2e suite finds them (scene.mjs → findApp). A missing build
// FAILS this script: a skipped shot keeps the old picture, and a README that
// shows an old picture as the current product is exactly what this job stops.
//
// ⚠⚠ The converter's wizard lists the conversion engines the SERVER found, and
// names each missing one "Not installed on this server". A picture of that is
// the shop window telling strangers the feature is broken. So the instance is
// booted with the engines the converter's manifest asks for, and when this
// host lacks any of them (a Windows workstation has none) the SAME build runs
// inside the full container image, which carries them all — Docker, nothing
// installed here (scene.mjs → bootInstance({ engines })). The picture is
// refused, not taken, while anything says "Not installed".
//
// Environment:
//   FILEX_BIN              binary to run (default bin/filex[.exe])
//   FILEX_SIGN_APP_DIR     a directory holding filex-sign's plugin.wasm + filex-app.json
//   FILEX_CONVERT_APP_DIR  the same for filex-convert
//   SHOTS_ENGINES          auto (default) | host | container — where the engines come from
//   SHOTS_ENGINES_IMAGE    the image for `container` (default ghcr.io/brf-tech/filex:full)
//   SHOTS_LINUX_BIN        a linux build of this tree for the container (else built on demand)
//   SHOTS_OUT              write here instead of the release folder
//   SHOTS_DRY_RUN=1        walk every scene to its picture and write nothing
//   SHOTS_KEEP=1           leave the instance running afterwards

import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { seedFixtures } from './fixtures.mjs';
import {
  addLocalStorage,
  bootInstance,
  client,
  findApp,
  installApp,
  log,
  newContext,
  shot,
  signIn,
  sleep,
  uploadTree,
} from './scene.mjs';

const SET = 'apps';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };

/**
 * Close every toast still on screen, and wait for the layer to be empty.
 *
 * ⚠ A toast is a notice about the LAST thing that happened; the picture is
 * about the screen. One left over from the install sat across two lines of
 * the app's own grants in the first v0.43.0 take.
 */
async function dismissToasts(page) {
  const layer = page.getByTestId('toast-layer');
  for (let i = 0; i < 12 && (await layer.locator('button').count()) > 0; i++) {
    await layer.locator('button').first().click({ timeout: 2_000 }).catch(() => {});
    await sleep(150);
  }
  await sleep(250);
}

async function main() {
  // Both builds are looked up BEFORE anything boots, so a missing one costs a
  // second rather than a browser session.
  const sign = findApp('sign');
  const convert = findApp('convert');
  log(`sign ${sign.manifest.version} from ${sign.wasm}`);
  log(`convert ${convert.manifest.version} from ${convert.wasm}`);

  // The engines are the converter's own list — its manifest asks for each one
  // as `engines:<name>`, which is also the key the server reports it under.
  const engines = convert.manifest.permissions
    .filter((p) => p.startsWith('engines:'))
    .map((p) => p.slice('engines:'.length));
  const inst = await bootInstance({
    name: SET,
    admin: ADMIN,
    // No release announcement in the bell of a picture, and no outbound call.
    env: { FILEX_UPDATE_CHECK: '0' },
    engines,
  });
  const browser = await chromium.launch();
  const seed = mkdtempSync(join(tmpdir(), 'filex-shots-apps-seed-'));
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Demo' });

    // The photo the converter is opened on, and a storage to hold it —
    // uploaded through filex, because inside a container this machine's disk
    // is not the instance's disk.
    seedFixtures(seed);
    await addLocalStorage(admin, 'demo', inst.storageRoot('demo'), { onHost: !inst.container });
    await uploadTree(admin, 'demo://', seed);

    // The converter is present from the start; the signing app is installed
    // through the wizard below, because the wizard is the picture.
    await installApp(admin, convert);

    const ctx = await newContext(browser, { height: 1000 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);

    // ── 1. the install review ────────────────────────────────────────────
    await page.goto(`${inst.url}/admin/plugins`);
    await page.getByTestId('plugins-tab-apps').click();
    await page.getByTestId('app-plugins').waitFor();
    await page.getByTestId('app-plugin-add').click();
    await page.getByTestId('app-plugin-wizard').waitFor();
    await page.getByTestId('app-plugin-source-file').click();
    await page.getByTestId('app-plugin-wasm').setInputFiles(sign.wasm);
    await page.getByTestId('app-plugin-manifest').setInputFiles(sign.manifestPath);
    await page.getByTestId('app-plugin-review').click();
    const perms = page.getByTestId('app-plugin-permissions');
    await perms.waitFor({ timeout: 30_000 });
    // Every permission the manifest asks for is on the screen before the
    // picture is taken — a review that is still filling in is not the review.
    for (const perm of sign.manifest.permissions) {
      await page.getByTestId(`perm-${perm}`).waitFor({ timeout: 10_000 });
    }
    // ⚠ The reasons are the manifest's own words, in the reader's language.
    // A review that prints `{"en": …, "tr": …}` is a bug on screen, and a
    // picture of it would put Turkish into the English shop window.
    const reviewText = await perms.innerText();
    if (/"en"\s*:/.test(reviewText)) {
      throw new Error(
        'the permission review prints the reasons as raw JSON ({"en": …, "tr": …}) — fix reasonOf() in ' +
          'web/src/components/plugins/AppPluginInstallWizard.vue before this picture is taken',
      );
    }
    await sleep(400);
    // ⚠⚠ The review is TALLER than the window — nine permissions, each with a
    // sentence — and an element screenshot of something taller than the
    // viewport is stitched by the browser. Over a dialog that floats above a
    // scrolling page the stitch came back as the top of the review, a grey
    // band, and the page underneath bleeding through it (v0.43.0, first take:
    // 1344×3308 of which two thirds were nothing). So the window is grown to
    // hold the whole dialog, and it is MEASURED to fit before the shutter.
    const dialog = page.locator('[role="dialog"]').filter({ has: perms });
    let fits = '';
    for (let i = 0; i < 5; i++) {
      const box = await dialog.boundingBox();
      const view = page.viewportSize();
      if (!box || !view) throw new Error('the install review dialog has no box to measure');
      if (box.y >= 0 && box.y + box.height <= view.height) {
        fits = 'yes';
        break;
      }
      fits = `${Math.ceil(box.y + box.height)}px of dialog in a ${view.height}px window`;
      await page.setViewportSize({ width: view.width, height: Math.min(2600, Math.ceil(box.y + box.height + 48)) });
      await sleep(300);
    }
    if (fits !== 'yes') throw new Error(`the install review does not fit the window (${fits}) — the picture would be stitched`);
    await shot(dialog, SET, 'apps-install-review-1440.png');
    // Back to the window every other picture here is framed in.
    await page.setViewportSize({ width: 1440, height: 1000 });
    await sleep(300);

    await page.getByLabel(/I understand/).check();
    await page.getByTestId('app-plugin-install').click();
    await page.getByTestId('app-plugin-sign').waitFor({ timeout: 30_000 });
    // The wizard ends on its own "done" pane; close it the way a person does.
    await page.keyboard.press('Escape');
    await sleep(300);

    // ── 2. the installed app's detail ────────────────────────────────────
    // The row's one pinned "Actions" control (DataTable draws core RowActions); its menu is
    // teleported to <body>, so the entry is found on the page, by its words.
    await page.getByTestId('app-plugin-actions-sign').click();
    await page.getByRole('menuitem', { name: /Details/ }).click();
    await page.getByTestId('app-plugin-detail').waitFor();
    await page.getByTestId('app-plugin-granted').waitFor();
    // ⚠ The install left an "e-Signature installed" toast in the corner, and
    // it sat across two lines of what this picture is about. Dismiss what is
    // still up — the shot is of the page, not of the moment it arrived.
    await dismissToasts(page);
    await sleep(600);
    await shot(page, SET, 'apps-detail-1440.png');
    await page.keyboard.press('Escape');

    // ── 3. the converter's wizard ────────────────────────────────────────
    await page.goto(`${inst.url}/admin/explore?storage=demo`);
    const folder = page.locator('[data-fe-path="demo://Photos"]').first();
    await folder.waitFor({ timeout: 25_000 });
    await folder.dblclick();
    const photo = page.locator('[data-fe-path="demo://Photos/aurora.png"]').first();
    await photo.waitFor({ timeout: 15_000 });
    await photo.click({ button: 'right' });
    await page.getByRole('menuitem', { name: /^Convert/ }).click();
    const view = page.getByTestId('plugin-view');
    await view.waitFor({ timeout: 20_000 });
    await view.getByTestId('surface-steps').waitFor();
    await view.getByText('What should it become?').waitFor();
    // A choice is made, so the picture shows the picker answering a question
    // rather than a screen nobody has touched yet.
    await view.getByTestId('fe-choice-target_image-jpg').click();
    // ⚠⚠ Pressing a choice sends it to the app and the footer's primary is
    // disabled while that is in flight (SurfaceFooterButtons → `conv.busy`).
    // A fixed 500 ms landed inside that window: the first v0.43.0 take showed
    // JPEG chosen and "Next" greyed out — a picture of a wizard that will not
    // let you go on. Wait for the step to be ready to move, and refuse the
    // shot rather than take that one again.
    // ⚠ Found by its ROLE in the footer, not by an id: the action's id is the
    // APP's word for it (filex-convert calls this step's primary `submit`
    // while the button reads "Next"), and an id guessed from the label is a
    // 20-second timeout that stops the whole run.
    const next = page.locator('[data-testid^="plugin-view-action-"].fe-btn--primary').first();
    await next.waitFor({ timeout: 20_000 });
    for (let i = 0; i < 60 && (await next.isDisabled()); i++) await sleep(250);
    if (await next.isDisabled()) {
      const id = (await next.getAttribute('data-testid')) ?? '?';
      throw new Error(`the converter wizard still has its primary button (${id}) disabled after a target was chosen — the picture would show a dead end`);
    }
    await sleep(300);
    // ⚠⚠ The shop window must not say the feature is broken. bootInstance
    // already refused an instance without the engines; this reads the screen
    // itself, because the screen is what the picture shows.
    const wizardText = await view.innerText();
    const broken = /Not installed on this server|Not available on this server/.exec(wizardText);
    if (broken) {
      throw new Error(
        `the converter wizard says "${broken[0]}" — a picture of it would tell strangers the feature is broken:\n` +
          wizardText.split('\n').filter((l) => /server|needs/i.test(l)).join('\n'),
      );
    }
    await shot(page.locator('.fe-modal__card').filter({ has: view }), SET, 'convert-wizard-1440.png');
    await page.keyboard.press('Escape');

    await ctx.close();
  } finally {
    await browser.close();
    await inst.stop();
    rmSync(seed, { recursive: true, force: true });
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
