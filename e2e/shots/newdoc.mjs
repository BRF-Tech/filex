// New document: any file name (issue #56).
//
//   node e2e/shots/newdoc.mjs        (from the repo root; `pnpm shots` runs it)
//
// Writes docs/screenshots/<release>/newdoc/:
//
//   newdoc-any-name-1280.png     the dialog with Plain text chosen and the name
//                                field holding `LICENSE` — the whole name, no
//                                read-only `.txt` beside it.
//   newdoc-any-name-390-dark.png the same dialog on a phone, dark.
//   newdoc-license-editor-1280.png  what Create opens: `LICENSE` in the text
//                                editor, although its name picks no viewer.
//
// The office half (a .docx keeps its extension) is not pictured: this
// instance has no document server, so the dialog does not offer Word — which
// is the product being honest, and not what #56 changed.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { addLocalStorage, bootInstance, client, newContext, shot, signIn, sleep } from './scene.mjs';

const SET = 'newdoc';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };

async function openDialog(page, url, { list = true } = {}) {
  await page.goto(`${url}/admin/explore?storage=projects`);
  await page.getByTestId('toolbar-nav').waitFor({ timeout: 25_000 });
  if (list) await page.getByTestId('view-list').click();
  await page.locator('[data-fe-path="projects://README.md"]').first().waitFor({ timeout: 25_000 });
  // On a phone the navigation panel is a drawer, opened from the toolbar.
  if (!(await page.getByTestId('sidenav-new').isVisible())) await page.getByTestId('toolbar-nav').click();
  await page.getByTestId('sidenav-new').click();
  await page.locator('.fe-ctx__item', { hasText: 'New document' }).click();
  await page.getByTestId('newdoc-modal').waitFor();
  await page.getByTestId('newdoc-type-txt').click();
  const input = page.getByTestId('newdoc-name');
  // Typed the way a person does. ⚠ Not `fill()`: it focuses the field as it
  // fills, focusing selects the stem, and fill then replaced only `Untitled`
  // (`LICENSE.txt`) — see e2e/tests/158-new-document-any-name.spec.ts.
  await input.click();
  await page.keyboard.press('ControlOrMeta+a');
  await page.keyboard.type('LICENSE');
  // Not focused: a picture of a caret blinking in a field is a picture that
  // differs every time it is taken.
  await input.blur();
  await page.mouse.move(0, 0);
  await sleep(500);
  const value = await input.inputValue();
  if (value !== 'LICENSE') throw new Error(`the name field reads "${value}", not LICENSE`);
  if ((await page.locator('.fe-newdoc__suffix').count()) !== 0) {
    throw new Error('a read-only extension is still drawn beside the name field');
  }
  const said = await page.locator('[data-testid="newdoc-collision"], [data-testid="newdoc-name-error"]').count();
  if (said) throw new Error('the dialog is showing an error under the name — not the picture this is');
}

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Demo' });

    // A small project folder, so the dialog opens over a real listing.
    const root = join(inst.files, 'projects');
    mkdirSync(join(root, 'src'), { recursive: true });
    mkdirSync(join(root, 'docs'), { recursive: true });
    writeFileSync(join(root, 'README.md'), '# Projects\n\nOne folder per client.\n');
    writeFileSync(join(root, 'Makefile'), 'build:\n\tgo build ./...\n');
    writeFileSync(join(root, 'config.yaml'), 'port: 8080\n');
    writeFileSync(join(root, 'src', 'main.go'), 'package main\n\nfunc main() {}\n');
    await addLocalStorage(admin, 'projects', root);
    await admin.post('/api/notifications/read-all', {});

    // ── the dialog, phone, dark ─────────────────────────────────────────────
    // ⚠ First: the desktop scene below CREATES LICENSE, and a dialog asked for
    // LICENSE after that says "LICENSE is already here" — true, and not what
    // this picture is of.
    {
      const ctx = await newContext(browser, { scheme: 'dark', width: 390, height: 844 });
      const page = await ctx.newPage();
      await signIn(page, inst.url, ADMIN);
      await openDialog(page, inst.url, { list: false });
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
      if (overflow > 0) throw new Error(`the page scrolls sideways by ${overflow}px at 390px`);
      await shot(page, SET, 'newdoc-any-name-390-dark.png');
      await ctx.close();
    }

    // ── the dialog, desktop, light ──────────────────────────────────────────
    {
      const ctx = await newContext(browser, { width: 1280, height: 860 });
      const page = await ctx.newPage();
      await signIn(page, inst.url, ADMIN);
      await openDialog(page, inst.url);
      await shot(page, SET, 'newdoc-any-name-1280.png');

      // …and what Create opens.
      await page.getByTestId('newdoc-create').click();
      await page.getByTestId('newdoc-modal').waitFor({ state: 'detached' });
      await page.locator('.fe-preview__code-wrap').waitFor({ timeout: 20_000 });
      await page.locator('.fe-preview__code-editor:not(.is-hidden)').waitFor({ timeout: 20_000 });
      await page.locator('.fe-preview__code-editor').click();
      await page.keyboard.type('MIT License\n\nCopyright (c) 2026 Demo\n');
      await page.locator('.fe-preview__code-toolbar .fe-btn--primary').click();
      await page.locator('.fe-preview__code-status--ok').waitFor({ timeout: 15_000 });
      const lang = (await page.locator('.fe-preview__code-lang').innerText()).trim().toLowerCase();
      if (lang !== 'plaintext') throw new Error(`LICENSE opened as "${lang}", not plain text`);
      await page.mouse.move(0, 0);
      await sleep(600);
      await shot(page, SET, 'newdoc-license-editor-1280.png');
      await ctx.close();
    }
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
