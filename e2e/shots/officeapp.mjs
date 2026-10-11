// The office example app (./office-app) - what filex 0.56 shows around an app
// that edits office documents without a document server (#217, #189):
//
//   node e2e/shots/officeapp.mjs     (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/officeapp/ (or SHOTS_OUT):
//
//   office-app-review-1440.png         Plugins → Apps → Install, the review of
//                                      the app: files:e2e-plaintext and
//                                      files:co-edit in filex's own words,
//                                      beside the Interface group
//   newdoc-office-app-1280.png         New document with the app installed and
//                                      no ONLYOFFICE: its Word, Excel and
//                                      PowerPoint rows under Apps, and no line
//                                      saying office documents need a document
//                                      server - only draw.io's, in the
//                                      server's words (#217)
//   newdoc-office-app-dark-tr-1280.png the same in Turkish, dark: the server's
//                                      sentence in the reader's language
//   office-app-phone-390.png           the app open on a 390-px phone: its
//                                      interface takes the viewer's whole
//                                      width, the previous/next arrows over its
//                                      edges (#217)
//   coedit-editor-1280.png             editing together (files:co-edit): Dana
//                                      types, the app lists who is in the
//                                      session - Sam, who may only read the
//                                      file, as a watcher
//   coedit-watcher-1280.png            Sam's screen: the watcher's line, Dana's
//                                      sentence as she typed it, her cursor
//
// It also MEASURES: the New document type list scrolls nowhere (a picture of
// cut tiles), its line names no document server for office documents in
// either language, and on the phone the app's frame is as wide as the stage.
//
// The app is ./office-app, packed here with the SDK this tree just built; it is
// installed through the wizard (the review is a picture). The co-editing
// relay needs FILEX_SECRET_KEY in an ordinary folder - bootInstance sets one.
// The second person is a viewer on the team storage (RBAC): filex lets them
// in as a watcher.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { DOCS, packOfficeApp, writeDocx } from './office-app/pack.mjs';
import {
  bootInstance,
  client,
  dismissToasts,
  log,
  mustSay,
  newContext,
  setLanguage,
  shootWhole,
  shot,
  signIn,
  sleep,
  uploadTree,
} from './scene.mjs';

const SET = 'officeapp';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const SAM = { email: 'sam.ito@example.com', password: 'sam-ito-shots-2026', display_name: 'Sam Ito' };
const APP = 'office';
const FOLDER = 'Plans';
const PLAN = 'Launch plan.docx';
const MINUTES = 'Board minutes.docx';
const TYPED = ' The beta opens to everyone on October 20.';

/** The explorer on the team storage, its listing settled. */
async function openTeam(page, url, base = '/admin/explore') {
  await page.goto(`${url}${base}?storage=Team`);
  await page.locator('.fe__body').first().waitFor({ state: 'visible', timeout: 25_000 });
  await page.locator('.fe__body [aria-busy="true"]').first().waitFor({ state: 'detached', timeout: 20_000 });
}

/** Opens a file of the Plans folder in the app and waits for it to draw. */
async function openInApp(page, name) {
  const folder = page.locator(`[data-fe-path="Team://${FOLDER}"]`).first();
  await folder.waitFor({ timeout: 25_000 });
  await folder.dblclick();
  const row = page.locator(`[data-fe-path="Team://${FOLDER}/${name}"]`).first();
  await row.waitFor({ timeout: 15_000 });
  // ⚠ The pane ignores a second open within 500 ms of the last one
  // (FilePane OPEN_GUARD_MS): the folder above opened just now.
  await sleep(700);
  await row.dblclick();
  const appframe = page.locator(`.fe-appframe[data-app="${APP}"]`);
  await page.locator(`.fe-appframe[data-app="${APP}"][data-connected="true"]`).waitFor({ timeout: 20_000 });
  const frame = await (await appframe.locator('iframe').first().elementHandle()).contentFrame();
  if (!frame) throw new Error('the app frame has no document');
  await frame.waitForFunction(() => document.body.dataset.ready === '1' || !!document.body.dataset.error, null, { timeout: 30_000 });
  const failed = await frame.evaluate(() => document.body.dataset.error ?? null);
  if (failed) throw new Error(`the office app did not start: ${failed}`);
  return frame;
}

/** No caret and no selection in the app: a blinking caret is a picture that differs every time. */
async function still(frame) {
  await frame.evaluate(() => {
    document.activeElement?.blur?.();
    document.getSelection()?.removeAllRanges();
  });
}

/** New document, opened from the side panel's New menu (English or Turkish). */
async function openNewDocument(page) {
  await page.getByTestId('sidenav-new').first().click();
  await page.locator('.fe-ctx__item', { hasText: /^(New document|Yeni belge)$/ }).first().click();
  const dialog = page.getByTestId('newdoc-modal');
  await dialog.waitFor({ timeout: 10_000 });
  for (const ext of ['docx', 'xlsx', 'pptx']) await page.getByTestId(`newdoc-type-app:${APP}:${ext}`).waitFor({ timeout: 10_000 });
  return dialog;
}

/**
 * The line under the types: draw.io's sentence (and where to set it up), the
 * server's words in the page's language - never one about a document server
 * while the app makes office documents (#217).
 */
async function withheldProblems(page, caps, look) {
  const out = [];
  const line = page.getByTestId('newdoc-withheld');
  const text = (await line.count()) ? (await line.innerText()).replace(/\s+/g, ' ').trim() : '';
  const words = caps.newdoc_withheld ?? {};
  if (/ONLYOFFICE/i.test(text)) out.push(`${look}: the line names a document server beside the app's office rows: "${text}"`);
  if (!words.drawio || !text.includes(words.drawio)) out.push(`${look}: the line is not the server's draw.io sentence ("${words.drawio}"): "${text}"`);
  return out;
}

/** The type list must not scroll: a list scrolled is a picture of cut tiles (apps.mjs, 0.53). */
async function typesScroll(page) {
  const list = await page.locator('.fe-newdoc__types').evaluate((el) => ({
    scrolled: Math.round(el.scrollTop),
    hidden: Math.round(el.scrollHeight - el.clientHeight),
  }));
  return list.scrolled > 0 || list.hidden > 1 ? `the type list scrolls (${list.hidden}px more than it shows)` : '';
}

async function main() {
  const work = mkdtempSync(join(tmpdir(), 'filex-shots-officeapp-'));
  const office = packOfficeApp(work);
  // ⚠ FILEX_PUBLIC_URL emptied: every scene's filex advertises
  // https://files.example.com (scene.mjs PUBLIC_URL, for the links pictures
  // show), and a WebSocket ticket names that host - the co-editing relay's
  // socket then never connects and nobody is ever "in" the document. Unset,
  // the server takes the socket's address from the request (ws.go wsURL).
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0', FILEX_PUBLIC_URL: '' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });

    // The team storage, RBAC on: Sam may only read it.
    const root = inst.storageRoot('team');
    if (!inst.container) mkdirSync(root, { recursive: true });
    await admin.post('/api/admin/storages', {
      name: 'Team',
      driver: 'local',
      mount_path: root,
      config: { path: root },
      sync_mode: 'ondemand',
      sync_interval_s: 0,
      enabled: true,
      read_only: false,
      rbac_enabled: true,
    });
    const seed = join(work, 'seed');
    mkdirSync(join(seed, FOLDER), { recursive: true });
    writeDocx(join(seed, FOLDER, PLAN), DOCS.plan, `${FOLDER}/${PLAN}`);
    writeDocx(join(seed, FOLDER, MINUTES), DOCS.minutes, `${FOLDER}/${MINUTES}`);
    await uploadTree(admin, 'Team://', seed);
    const sam = await admin.post('/api/admin/users', { ...SAM, role: 'user', locale: 'en' });
    const samId = sam.id ?? sam.user?.id;
    if (!samId) throw new Error(`creating ${SAM.email} answered no id: ${JSON.stringify(sam).slice(0, 200)}`);
    await admin.post('/api/files/permissions', { path: 'Team://', user_id: samId, level: 'viewer', is_dir: true });

    // ── 1. the review: the two permissions 0.56 derives ─────────────────
    const ctx = await newContext(browser, { width: 1440, height: 1000 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    await page.goto(`${inst.url}/admin/plugins`);
    await page.getByTestId('plugins-tab-apps').click();
    await page.getByTestId('app-plugins').waitFor();
    await page.getByTestId('app-plugin-add').click();
    await page.getByTestId('app-plugin-wizard').waitFor();
    await page.getByTestId('app-plugin-source-file').click();
    await page.getByTestId('app-plugin-manifest').setInputFiles(office.manifestPath);
    await page.getByTestId('app-plugin-ui').setInputFiles(office.uiZip);
    await page.getByTestId('app-plugin-review').click();
    const perms = page.getByTestId('app-plugin-permissions');
    await perms.waitFor({ timeout: 30_000 });
    await page.getByTestId('app-plugin-ui-group').waitFor({ timeout: 10_000 });
    for (const id of ['files:read', 'files:write', 'files:e2e-plaintext', 'files:co-edit', 'ui-new:.docx']) {
      await page.getByTestId(`perm-${id}`).waitFor({ timeout: 10_000 });
    }
    // filex's sentences, not the ids: what the administrator reads.
    await mustSay(page.getByTestId('perm-files:e2e-plaintext'), 'the e2e-plaintext row', ['encrypted folders']);
    await mustSay(page.getByTestId('perm-files:co-edit'), 'the co-edit row', ['together']);
    if (/"en"\s*:/.test(await perms.innerText())) throw new Error('the review prints a reason as raw JSON');
    await page.mouse.move(4, 4);
    await sleep(400);
    await shootWhole(page, page.locator('[role="dialog"]').filter({ has: perms }), SET, 'office-app-review-1440.png', {
      restore: { width: 1440, height: 1000 },
    });
    await page.getByLabel(/I understand/).check();
    await page.getByTestId('app-plugin-install').click();
    await page.getByTestId(`app-plugin-${APP}`).waitFor({ timeout: 30_000 });
    await page.keyboard.press('Escape');
    await ctx.close();

    // ── 2. New document: the app's rows, and no document server asked for ─
    const problems = [];
    const looks = [
      { scheme: 'light', locale: 'en', file: 'newdoc-office-app-1280.png' },
      { scheme: 'dark', locale: 'tr', file: 'newdoc-office-app-dark-tr-1280.png' },
    ];
    for (const look of looks) {
      const lctx = await newContext(browser, { scheme: look.scheme, width: 1280, height: 1200 });
      const p = await lctx.newPage();
      await signIn(p, inst.url, ADMIN);
      await setLanguage(admin, p, look.locale);
      // ⚠ A new document, or the language does not change (lesson #1359).
      await p.goto('about:blank');
      await openTeam(p, inst.url);
      await p.waitForFunction((l) => document.documentElement.lang === l, look.locale, { timeout: 20_000 });
      const dialog = await openNewDocument(p);
      await p.getByTestId(`newdoc-type-app:${APP}:docx`).click();
      const name = p.getByTestId('newdoc-name');
      await name.click();
      await p.keyboard.press('ControlOrMeta+a');
      await p.keyboard.type(look.locale === 'tr' ? 'Dördüncü çeyrek raporu' : 'Q4 report');
      await name.blur();
      await p.mouse.move(0, 0);
      await sleep(500);
      // The server's words for this account (its language is the look's).
      const caps = await admin.json('/api/files/capabilities');
      problems.push(...(await withheldProblems(p, caps, `${look.scheme} ${look.locale}`)));
      const scroll = await typesScroll(p);
      if (scroll) problems.push(`${look.scheme} ${look.locale}: ${scroll}`);
      await shot(p.locator('.fe-modal__card').filter({ has: dialog }), SET, look.file);
      await p.keyboard.press('Escape');
      await setLanguage(admin, p, 'en');
      await lctx.close();
    }
    if (problems.length) throw new Error(`New document:\n  ${problems.join('\n  ')}`);
    log('New document lists the app\'s office rows and asks for no document server, in English and Turkish');

    // ── 3. on a phone: the app takes the viewer's whole width ───────────
    const pctx = await newContext(browser, { width: 390, height: 844 });
    const phone = await pctx.newPage();
    await signIn(phone, inst.url, ADMIN);
    await openTeam(phone, inst.url);
    const pframe = await openInApp(phone, MINUTES);
    await phone.locator('.fe-viewer__chev--prev, .fe-viewer__chev--next').first().waitFor({ state: 'visible', timeout: 10_000 });
    const stage = await phone.getByTestId('viewer-stage').boundingBox();
    const box = await phone.locator(`.fe-appframe[data-app="${APP}"] iframe`).first().boundingBox();
    if (!stage || !box) throw new Error('the viewer stage or the app frame has no box on the phone');
    if (stage.width - box.width > 2 || box.width < 360) {
      throw new Error(`on a 390-px phone the app's frame is ${Math.round(box.width)} px of a ${Math.round(stage.width)}-px stage: the arrows still keep a gutter (#217)`);
    }
    await still(pframe);
    await dismissToasts(phone);
    await phone.mouse.move(2, 2);
    await sleep(500);
    await shot(phone, SET, 'office-app-phone-390.png');
    await pctx.close();

    // ── 4. editing together: Dana writes, Sam watches ───────────────────
    const dctx = await newContext(browser, { width: 1280, height: 860 });
    const dana = await dctx.newPage();
    await signIn(dana, inst.url, ADMIN);
    await openTeam(dana, inst.url);
    const dframe = await openInApp(dana, PLAN);
    if ((await dframe.evaluate(() => document.body.dataset.together)) !== 'editing') {
      throw new Error(`Dana is not editing together: ${await dframe.evaluate(() => document.body.dataset.together)}`);
    }

    const sctx = await newContext(browser, { width: 1280, height: 860 });
    const sam2 = await sctx.newPage();
    await signIn(sam2, inst.url, SAM);
    await openTeam(sam2, inst.url, '/drive/explore');
    const sframe = await openInApp(sam2, PLAN);
    const samSays = await sframe.evaluate(() => document.body.dataset.together);
    if (samSays !== 'watching') throw new Error(`Sam may only read the file and should watch, the app says "${samSays}"`);
    await dframe.waitForFunction(() => document.body.dataset.members === '2', null, { timeout: 20_000 });
    await sframe.waitForFunction(() => document.body.dataset.members === '2', null, { timeout: 20_000 });

    // Dana adds a sentence to the first paragraph; it reaches Sam through the
    // relay, and so does where her cursor is.
    const first = dframe.locator('#page p[data-i="1"]');
    await first.click();
    await dana.keyboard.press('End');
    await dana.keyboard.type(TYPED, { delay: 15 });
    await sframe.waitForFunction((t) => document.getElementById('page').textContent.includes(t.trim()), TYPED, { timeout: 20_000 });
    await sframe.waitForFunction(() => !!document.querySelector('#page [data-by]'), null, { timeout: 20_000 });
    await mustSay(sframe.locator('#people'), "Sam's list of who is in", ['Dana Reyes', 'Sam Ito (you)']);
    await mustSay(dframe.locator('#people'), "Dana's list of who is in", ['Dana Reyes (you)', 'Sam Ito', 'watching']);

    await still(dframe);
    await dismissToasts(dana);
    await dana.mouse.move(4, 4);
    await sleep(600);
    await shot(dana, SET, 'coedit-editor-1280.png');
    await dismissToasts(sam2);
    await sam2.mouse.move(4, 4);
    await sleep(300);
    await shot(sam2, SET, 'coedit-watcher-1280.png');
    log('two people in one document: Dana edits, Sam watches and sees her sentence and her cursor');
    await sctx.close();
    await dctx.close();
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
