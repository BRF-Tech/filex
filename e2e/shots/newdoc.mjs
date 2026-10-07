// New document: any file name (issue #56), and drafts (issue #71).
//
//   node e2e/shots/newdoc.mjs        (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/newdoc/:
//
//   newdoc-any-name-1280.png     the dialog with Plain text chosen and the name
//                                field holding `LICENSE` — the whole name, no
//                                read-only `.txt` beside it — and the line that
//                                says nothing is created until it is saved.
//   newdoc-any-name-390-dark.png the same dialog on a phone, dark.
//   newdoc-license-editor-1280.png  what Create opens: `LICENSE` in the text
//                                editor, although its name picks no viewer —
//                                a DRAFT, with the bar that says where Save
//                                puts it.
//   drafts-close-1280.png        closing a draft that was never saved: Save to
//                                disk / Keep in Drafts / Discard.
//   drafts-close-390-dark.png    the same question on a phone, dark.
//   drafts-taken-1280.png        Save, when a file took the name in the
//                                meantime: "save as plan (2).txt?"
//   drafts-view-1280.png         the Drafts view (the explorer's own table) and
//                                the panel's count beside it.
//
// The office half (a .docx keeps its extension) is not pictured: this
// instance has no document server, so the dialog does not offer Word — which
// is the product being honest, and not what #56 changed.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { pinTimes } from './clock.mjs';
import { addLocalStorage, bootInstance, client, newContext, shot, signIn, sleep } from './scene.mjs';

const SET = 'newdoc';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };

async function openExplorer(page, url, { list = true } = {}) {
  await page.goto(`${url}/admin/explore?storage=projects`);
  await page.getByTestId('toolbar-nav').waitFor({ timeout: 25_000 });
  if (list) await page.getByTestId('view-list').click();
  await page.locator('[data-fe-path="projects://README.md"]').first().waitFor({ timeout: 25_000 });
}

/** On a phone the navigation panel is a drawer, opened from the toolbar. */
async function showNav(page) {
  if (!(await page.getByTestId('sidenav-new').isVisible())) await page.getByTestId('toolbar-nav').click();
  await page.getByTestId('sidenav-new').waitFor();
}

async function openDialog(page, url, { list = true, name = 'LICENSE' } = {}) {
  await openExplorer(page, url, { list });
  await showNav(page);
  await page.getByTestId('sidenav-new').click();
  await page.locator('.fe-ctx__item', { hasText: 'New document' }).click();
  await page.getByTestId('newdoc-modal').waitFor();
  await page.getByTestId('newdoc-type-txt').click();
  const input = page.getByTestId('newdoc-name');
  // Typed the way a person does. ⚠ Not `fill()`: it focuses the field as it
  // fills, focusing selects the stem, and fill then replaced only `Untitled`
  // (`LICENSE.txt`) - see e2e/tests/155-new-document-any-name.spec.ts.
  await input.click();
  await page.keyboard.press('ControlOrMeta+a');
  await page.keyboard.type(name);
  // Not focused: a picture of a caret blinking in a field is a picture that
  // differs every time it is taken.
  await input.blur();
  await page.mouse.move(0, 0);
  await sleep(500);
  const value = await input.inputValue();
  if (value !== name) throw new Error(`the name field reads "${value}", not ${name}`);
  if ((await page.locator('.fe-newdoc__suffix').count()) !== 0) {
    throw new Error('a read-only extension is still drawn beside the name field');
  }
  const said = await page.locator('[data-testid="newdoc-collision"], [data-testid="newdoc-name-error"]').count();
  if (said) throw new Error('the dialog is showing an error under the name — not the picture this is');
  // #71: Create makes a draft, and the dialog says so before it is pressed.
  await page.getByTestId('newdoc-draft-hint').waitFor();
}

/** Create, and wait for the text editor on the draft. */
async function createDraft(page) {
  await page.getByTestId('newdoc-create').click();
  await page.getByTestId('newdoc-modal').waitFor({ state: 'detached' });
  await page.getByTestId('draft-bar').waitFor({ timeout: 20_000 });
  await page.locator('.fe-preview__code-editor:not(.is-hidden)').waitFor({ timeout: 20_000 });
}

/** Type into the open text editor and wait until the draft has it. */
async function typeIntoDraft(page, text) {
  await page.locator('.fe-preview__code-editor:not(.is-hidden)').click();
  const written = page.waitForResponse(
    (r) => r.url().includes('/api/files/save-text') && r.request().method() === 'POST',
    { timeout: 15_000 },
  );
  await page.keyboard.type(text);
  const res = await written;
  if (res.status() !== 200) throw new Error(`the autosave into the draft answered ${res.status()}`);
}

/** Ask to close the draft, and wait for the three answers. */
async function askToClose(page) {
  await page.getByTestId('viewer-close').click();
  await page.getByTestId('draft-close-dialog').waitFor();
  await page.mouse.move(0, 0);
  await sleep(500);
}

async function names(admin, dir) {
  const body = await admin.json(`/api/files/manager?action=index&path=${encodeURIComponent(dir)}`);
  return (body.files ?? []).map((f) => f.basename);
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
    pinTimes(root);
    await addLocalStorage(admin, 'projects', root);
    await admin.post('/api/notifications/read-all', {});

    // ── the dialog and the close question, phone, dark ──────────────────────
    // ⚠ First: the desktop scene below SAVES LICENSE, and a dialog asked for
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

      await createDraft(page);
      await typeIntoDraft(page, 'MIT License\n');
      await askToClose(page);
      const wide = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
      if (wide > 0) throw new Error(`the close question scrolls sideways by ${wide}px at 390px`);
      await shot(page, SET, 'drafts-close-390-dark.png');
      // Discarded: the desktop scene makes its own LICENSE.
      await page.getByTestId('draft-close-discard').click();
      await page.locator('.fe-viewer').waitFor({ state: 'detached' });
      await ctx.close();
    }

    // ── the dialog, the editor on a draft, Save — desktop, light ────────────
    {
      const ctx = await newContext(browser, { width: 1280, height: 860 });
      const page = await ctx.newPage();
      await signIn(page, inst.url, ADMIN);
      await openDialog(page, inst.url);
      await shot(page, SET, 'newdoc-any-name-1280.png');

      // …and what Create opens: the draft, with the bar that says where Save
      // puts it. Nothing is in the folder yet.
      await createDraft(page);
      await typeIntoDraft(page, 'MIT License\n\nCopyright (c) 2026 Demo\n');
      const lang = (await page.locator('.fe-preview__code-lang').innerText()).trim().toLowerCase();
      if (lang !== 'plaintext') throw new Error(`LICENSE opened as "${lang}", not plain text`);
      if ((await names(admin, 'projects://')).includes('LICENSE')) {
        throw new Error('LICENSE is in the folder before it was saved — New document did not make a draft');
      }
      await page.mouse.move(0, 0);
      await sleep(600);
      await shot(page, SET, 'newdoc-license-editor-1280.png');

      await page.getByTestId('draft-save').click();
      await page.getByTestId('draft-saved-note').waitFor({ timeout: 15_000 });
      if (!(await names(admin, 'projects://')).includes('LICENSE')) {
        throw new Error('Save did not put LICENSE in the folder');
      }
      await page.getByTestId('viewer-close').click();
      await page.locator('.fe-viewer').waitFor({ state: 'detached' });
      await ctx.close();
    }

    // ── closing a draft, and Save beside a taken name — desktop, light ──────
    {
      const ctx = await newContext(browser, { width: 1280, height: 860 });
      const page = await ctx.newPage();
      await signIn(page, inst.url, ADMIN);
      await openDialog(page, inst.url, { name: 'plan.txt' });
      await createDraft(page);
      await typeIntoDraft(page, 'Q4 plan\n\n- ship drafts\n');
      await askToClose(page);
      await shot(page, SET, 'drafts-close-1280.png');
      // Escape is "not now": the question goes, the draft stays open.
      await page.keyboard.press('Escape');
      await page.getByTestId('draft-close-dialog').waitFor({ state: 'detached' });

      // Somebody saves a plan.txt into the folder while the draft is open.
      await admin.post('/api/files/save-text', { path: 'projects://plan.txt', content: 'the other plan\n' });
      await page.getByTestId('draft-save').click();
      await page.getByTestId('draft-taken-dialog').waitFor();
      const offer = (await page.getByTestId('draft-taken-confirm').innerText()).trim();
      if (offer !== 'Save as plan (2).txt') throw new Error(`the question offers "${offer}"`);
      await page.mouse.move(0, 0);
      await sleep(500);
      await shot(page, SET, 'drafts-taken-1280.png');
      await page.getByTestId('draft-taken-cancel').click();
      await askToClose(page);
      await page.getByTestId('draft-close-keep').click();
      await page.locator('.fe-viewer').waitFor({ state: 'detached' });
      await ctx.close();
    }

    // ── the Drafts view — desktop, light ────────────────────────────────────
    {
      // Two more drafts, kept the way Keep in Drafts keeps them.
      await admin.post('/api/files/drafts', { path: 'projects://docs', name: 'Meeting notes.md', type: 'md', exact_name: true });
      await admin.post('/api/files/drafts', { path: 'projects://src', name: 'todo.txt', type: 'txt', exact_name: true });
      const ctx = await newContext(browser, { width: 1280, height: 860 });
      const page = await ctx.newPage();
      await signIn(page, inst.url, ADMIN);
      await openExplorer(page, inst.url);
      await showNav(page);
      const badge = page.getByTestId('sidenav-drafts-count');
      await badge.waitFor();
      const count = (await badge.innerText()).trim();
      if (count !== '3') throw new Error(`the panel counts ${count} drafts, not 3`);
      await page.getByTestId('sidenav-view-drafts').click();
      await page.getByTestId('drafts-view').waitFor();
      await page.getByTestId('drafts-view').locator('.fe-list__row').nth(2).waitFor();
      const rows = await page.getByTestId('drafts-view').locator('.fe-list__row').count();
      if (rows !== 3) throw new Error(`the Drafts view lists ${rows} drafts, not 3`);
      await page.mouse.move(0, 0);
      await sleep(600);
      await shot(page, SET, 'drafts-view-1280.png');
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
