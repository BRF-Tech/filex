// Templates (#314, docs/TEMPLATES.md): the administrator's editor for the
// mails and the public pages, and a public link page wearing the
// administrator's words.
//
//   node e2e/shots/templates.mjs      (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/templates/ (or SHOTS_OUT):
//
//   templates-editor-dark-1440.png  Admin → Settings → Templates, dark: the
//                                   list of mails and pages (the share mail
//                                   marked This server), the share mail's source
//                                   in filex's code editor (Monaco: Liquid
//                                   colours, line numbers) with a sign-off
//                                   added, and the preview the server rendered
//   templates-error-dark-1440.png   the same editor with a misspelled variable
//                                   typed in: the line the engine refused is
//                                   marked, and the engine's words are under
//                                   the preview
//   templates-link-page-1440.png    the page a shared link opens, light: the
//                                   administrator's intro above the file and
//                                   their footer under it (slot.share.intro,
//                                   slot.share.footer)
//
// It also MEASURES what the pictures claim: the edit is saved through the
// editor and the server answers the edited source back; the link page's
// intro and footer are the server's slots, and a script written into one
// never reaches the page.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { pinTimes } from './clock.mjs';
import { syncAndWait } from './fixtures.mjs';
import {
  addLocalStorage,
  bootInstance,
  client,
  dismissToasts,
  documentPDF,
  layoutProblems,
  log,
  mustSay,
  newContext,
  shot,
  signIn,
  sleep,
} from './scene.mjs';

export const INPUTS = [
  'web/src/views/Templates.vue',
  'packages/core/src/components/CodeEditor.vue',
  'packages/core/src/composables/useMonacoLoader.ts',
  'web/src/api/templates.ts',
  'web/src/components/ui/Select.vue',
  'packages/core/src/components/public',
  'packages/core/src/components/ChoiceButtons.vue',
  'packages/core/src/lib/publicPageClasses.ts',
  'packages/core/src/styles',
  'backend/internal/templates',
  'backend/internal/api/handlers',
  'backend/internal/api/public_shell_template.go',
];

const SET = 'templates';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const STORAGE = 'studio';
const DOC = 'brand-guidelines.pdf';

/** What the administrator adds to the share mail: a sign-off. */
const SIGN_OFF = '<p>Best,<br>Northwind Studio</p>';

const INTRO =
  '<p>Files from <strong>Northwind Studio</strong> for the spring campaign. ' +
  'This is the final version of {{ item.name }}.</p>';
const FOOTER =
  '<p>Questions? Write to <a href="mailto:studio@example.com">studio@example.com</a>.</p>' +
  '<script>document.title = "slot script ran"</script>';

async function putTemplate(admin, key, body) {
  const cur = await admin.json(`/api/admin/templates/${key}`);
  const rev = cur.template.source === 'default' ? 0 : cur.template.revision;
  await admin.json(`/api/admin/templates/${key}`, {
    method: 'PUT',
    body: JSON.stringify({ subject: '', body, text: '', revision: rev, lang: 'en' }),
  });
}

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });

    const root = inst.storageRoot(STORAGE);
    mkdirSync(root, { recursive: true });
    writeFileSync(
      join(root, DOC),
      documentPDF(['Northwind Studio', 'Brand guidelines', 'Spring campaign', 'Logo, colours, type and tone of voice.']),
    );
    pinTimes(root);
    const storage = await addLocalStorage(admin, STORAGE, root);
    await syncAndWait((_t, p, init) => admin.call(p, init), null, storage.id);

    const link = await admin.post('/api/files/share', { path: `${STORAGE}://${DOC}`, kind: 'file', expires_in: 14 * 86400 });
    const token = link.token ?? link.share?.token;
    if (!token) throw new Error(`the share answered no token: ${JSON.stringify(link).slice(0, 200)}`);
    await putTemplate(admin, 'slot.share.intro', INTRO);
    await putTemplate(admin, 'slot.share.footer', FOOTER);

    // ── the editor, dark: the share mail with a sign-off ─────────────────
    const ctx = await newContext(browser, { scheme: 'dark', width: 1440, height: 1000 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    await page.goto(`${inst.url}/admin/templates`);
    await page.getByTestId('templates-list').waitFor({ timeout: 20_000 });
    await page.getByTestId('template-item-mail.share').click();
    await page.waitForSelector('[data-testid="template-editor"][data-key="mail.share"]', { timeout: 10_000 });
    // filex's code editor (core CodeEditor, Monaco): typed into as a person
    // types - the end of the text, then the sign-off.
    await page.waitForSelector('[data-testid="template-source"][data-editor="monaco"]', { timeout: 30_000 });
    const before = (await admin.json('/api/admin/templates/mail.share')).current.body;
    if (!before.includes("'mail.greeting' | t")) throw new Error('the share mail\'s source is not the default one');
    await page.locator('[data-testid="template-source"] .view-lines').click();
    await page.keyboard.press('Control+End');
    await page.keyboard.insertText(`\n${SIGN_OFF}`);
    const frame = page.frameLocator('[data-testid="template-preview-frame"]');
    await frame.locator('body').getByText('Northwind Studio').waitFor({ timeout: 15_000 });
    await page.getByTestId('template-save').click();
    await page.locator('[data-testid="template-item-mail.share"]').getByText('This server').waitFor({ timeout: 10_000 });
    const stored = await admin.json('/api/admin/templates/mail.share');
    if (!stored.current.body.includes(SIGN_OFF)) throw new Error('the saved share mail does not carry the sign-off');
    await dismissToasts(page);
    await page.evaluate(() => document.activeElement?.blur());
    await page.mouse.move(4, 4);
    // The preview is the foot of the page: the window is grown to hold it,
    // so the picture is not stitched.
    const foot = await page.getByTestId('template-preview').boundingBox();
    if (!foot) throw new Error('no preview to measure');
    await page.setViewportSize({ width: 1440, height: Math.min(2600, Math.ceil(foot.y + foot.height + 32)) });
    await sleep(400);
    await mustSay(page.getByTestId('template-editor'), 'the share mail in the editor', [
      'A link shared by mail',
      'mail.share',
      'This server',
      'Send a test mail to me',
      'Subject:',
    ]);
    await mustSay(frame.locator('body'), 'the preview of the share mail', ['Northwind Studio', 'Open']);
    const problems = await layoutProblems(page);
    if (problems.length) throw new Error(`the Templates page does not fit: ${problems.join('; ')}`);
    await shot(page, SET, 'templates-editor-dark-1440.png');

    // ── the same editor, a misspelled variable: the refused line marked ──
    await page.locator('[data-testid="template-source"] .view-lines').click();
    await page.keyboard.press('Control+End');
    await page.keyboard.insertText('\n<p>{{ item.nmae }}</p>');
    await page.waitForSelector('[data-testid="template-source"][data-error-line]', { timeout: 15_000 });
    const marked = Number(await page.getByTestId('template-source').getAttribute('data-error-line'));
    const lines = (await page.locator('[data-testid="template-source"] .view-line').count());
    if (!(marked > 1)) throw new Error(`the refused line is not marked (${marked})`);
    await page.locator('[data-testid="template-source"] .squiggly-error').first().waitFor({ timeout: 10_000 });
    await mustSay(page.getByTestId('template-error'), 'the engine\'s words', ['item.nmae', `line ${marked}`]);
    await dismissToasts(page);
    await page.evaluate(() => document.activeElement?.blur());
    await page.mouse.move(4, 4);
    await sleep(400);
    log(`the refused line: ${marked} of ${lines} drawn`);
    await shot(page.getByTestId('template-editor'), SET, 'templates-error-dark-1440.png');
    await ctx.close();

    // ── the link page, light: the administrator's intro and footer ───────
    const lctx = await newContext(browser, { width: 1440, height: 900 });
    const lpage = await lctx.newPage();
    await lpage.goto(`${inst.url}/s/${token}`);
    const intro = lpage.getByTestId('public-page-intro');
    await intro.waitFor({ timeout: 20_000 });
    await mustSay(intro, 'the intro slot', ['Northwind Studio', 'spring campaign', DOC]);
    const footer = lpage.locator('.fe-ppage__footslot');
    await mustSay(footer, 'the footer slot', ['Questions?', 'studio@example.com']);
    if ((await lpage.title()) === 'slot script ran') throw new Error('a script written into a slot ran on the link page');
    if ((await lpage.locator('.fe-ppage__slot script').count()) > 0) throw new Error('a slot kept its script element');
    const lproblems = await layoutProblems(lpage);
    if (lproblems.length) throw new Error(`the link page does not fit: ${lproblems.join('; ')}`);
    await lpage.mouse.move(4, 4);
    await sleep(300);
    await shot(lpage, SET, 'templates-link-page-1440.png');
    await lctx.close();

    log('the Templates editor with the share mail (dark), the refused line marked, a link page with its intro and footer; the save and the slots measured');
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
