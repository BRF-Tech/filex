// Short links (0.60, docs/APP-PLUGINS.md → Short links, Setting an app up):
// the setup wizard of an app that holds the link-shortener role, "Get short
// link" in the share dialog, an uploader that answers short, and the
// Reconfigure question with its counts.
//
//   node e2e/shots/shortlinks.mjs     (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/shortlinks/ (or SHOTS_OUT):
//
//   setup-app-1440.png        the wizard's first step: the app's own question
//                             (the alphabet a code is drawn from), the steps
//                             named above it
//   setup-addresses-1440.png  Addresses: the address added on filex's own
//                             address (/l), and a dedicated host being typed,
//                             with the DNS record it needs
//   setup-people-1440.png     Who may use it: the app's permissions per role
//   share-short-dark-1440.png in the dark theme, the share dialog after Get
//                             short link: the short address leads, the full
//                             link one copy away
//   uploader-short-1440.png   the Uploaders tab with Short link on
//   reconfigure-1440.png      Reconfigure: what erasing takes, counted, and
//                             the app's name typed back
//
// It also MEASURES what a visitor gets, over plain HTTP with no cookies: the
// short address answers 302 to the share link, `<code>+` a preview page that
// names where it goes, a code that leads nowhere the one 404 page, an upload
// made with the uploader's key answers its short address, and after
// Reconfigure the short address answers 404.
//
// The app is backend/examples/app-shortlinks, the smallest app that holds
// the role, compiled to WebAssembly from this tree (scripts/lib/go-build.mjs:
// native Go, or WSL) - so the scene needs no app build from outside, and CI
// and the chain take it like any other.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from '@playwright/test';
import { goBuild } from '../../scripts/lib/go-build.mjs';
import { SHOTS_RELEASE } from './release.mjs';
import {
  addLocalStorage,
  bootInstance,
  client,
  dismissToasts,
  installApp,
  log,
  mustSay,
  newContext,
  shootWhole,
  shot,
  signIn,
  sleep,
} from './scene.mjs';

export const INPUTS = [
  'backend/examples/app-shortlinks',
  'web/src/components/plugins/AppSetupWizard.vue',
  'web/src/components/plugins/AppSetupAppStep.vue',
  'web/src/components/plugins/AppSetupAddresses.vue',
  'web/src/components/plugins/AppSetupPeople.vue',
  'web/src/components/plugins/AppEraseDialog.vue',
  'web/src/views/AppPluginPage.vue',
  'packages/core/src/modals/PermissionsModal.vue',
  'packages/core/src/components/UploadersPanel.vue',
  'packages/core/src/components/DataTable.vue',
  'packages/core/src/components/ChoiceButtons.vue',
  'packages/core/src/components/plugin/SurfaceFooterButtons.vue',
  'packages/core/src/lib/shortLink.ts',
  'backend/internal/api/handlers/app_setup.go',
  'backend/internal/api/handlers/short_links.go',
  'backend/internal/api/handlers/route_door.go',
  'backend/internal/shortlink',
  'backend/internal/wasmplugin/setup.go',
];

const SET = 'shortlinks';
const HERE = dirname(fileURLToPath(import.meta.url));
const REPO = join(HERE, '..', '..');
const APP_DIR = join(REPO, 'backend', 'examples', 'app-shortlinks');
const APP = 'shortlinks';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const STORAGE = 'team';
const FILE = 'Proposal 2026.txt';

/** The open wizard's card (the admin Modal's <dialog> is full width). */
function card(page) {
  return page.locator('dialog[open] [role="dialog"]').last();
}

/** Throws unless a fetch answered `status`. */
function must(res, status, what) {
  if (res.status !== status) throw new Error(`${what}: ${res.status}, want ${status}`);
}

async function openApp(page, url) {
  await page.goto(`${url}/admin/plugins/apps/${APP}`);
  await page.getByTestId('app-plugin-page').waitFor({ timeout: 20_000 });
}

async function main() {
  const tmp = mkdtempSync(join(tmpdir(), 'filex-shots-shortlinks-'));
  const wasm = join(tmp, 'shortlinks.wasm');
  goBuild({ cwd: join(REPO, 'backend'), pkg: './examples/app-shortlinks', out: wasm, goos: 'wasip1', goarch: 'wasm', buildmode: 'c-shared', log });
  // ⚠ The example says the release it ships with (">=0.60.0"); the binary
  // a scene photographs is stamped with SHOTS_RELEASE, which moves to that
  // release only at release step 2, and filex refuses an app whose range it
  // is outside. Module and binary are built from the same tree, so the scene
  // installs the manifest with the range of the binary it runs on.
  const manifest = JSON.parse(readFileSync(join(APP_DIR, 'filex-app.json'), 'utf8'));
  manifest.filex = `>=${SHOTS_RELEASE.replace(/^v/, '')}`;
  const manifestPath = join(tmp, 'filex-app.json');
  writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
  const app = { name: APP, wasm, manifestPath, manifest };

  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });
    await addLocalStorage(admin, STORAGE, inst.storageRoot(STORAGE));
    const form = new FormData();
    form.append('path', `${STORAGE}://`);
    form.append('file[]', new Blob([Buffer.from('The proposal, in plain text.\n')]), FILE);
    const up = await admin.call('/api/files/manager?action=upload', { method: 'POST', body: form });
    if (!up.ok) throw new Error(`upload: ${up.status}`);
    const share = await admin.post('/api/files/share', { path: `${STORAGE}://${FILE}`, kind: 'file' });
    const shareURL = share.url ?? share.share?.url;
    if (!shareURL) throw new Error(`the share answered no url: ${JSON.stringify(share).slice(0, 300)}`);

    const installed = await installApp(admin, app);
    const appId = installed.id ?? installed.plugin?.id;
    const listed = await admin.json('/api/admin/app-plugins');
    const row = (listed.plugins ?? listed).find((p) => p.name === APP);
    if (row?.state !== 'setup') throw new Error(`the app installed as ${row?.state}, want setup`);
    await admin.post('/api/notifications/read-all', {});

    // ── the wizard, light ─────────────────────────────────────────────────
    const ctx = await newContext(browser, { width: 1440, height: 1000 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    await openApp(page, inst.url);
    await page.getByTestId('app-plugin-page-needs-setup').waitFor();
    await page.getByTestId('app-plugin-page-setup').click();
    const wizard = card(page);
    await page.getByTestId('app-setup-surface').waitFor({ timeout: 20_000 });
    await sleep(400);
    await page.mouse.move(4, 4);
    await mustSay(wizard, 'the app step', ['Alphabet', 'Addresses', 'Who may use it']);
    await shootWhole(page, wizard, SET, 'setup-app-1440.png');

    // The app's own step: its one question, answered with its default.
    await page.getByTestId('app-setup-action-action-save').click();
    await page.getByTestId('app-setup-app-done').waitFor({ timeout: 10_000 });
    await page.getByTestId('app-setup-next').click();

    // Addresses: one on filex's own address, then a dedicated host typed.
    await page.getByTestId('app-setup-addresses').waitFor();
    await page.locator('input[name="app-setup-prefix-go"]').fill('l');
    await page.getByTestId('app-setup-add-button-go').click();
    await page.locator('[data-testid^="app-setup-base-"]').first().waitFor({ timeout: 10_000 });
    await page.getByTestId('app-setup-kind-go-host').click();
    await page.locator('input[name="app-setup-host-go"]').fill('l.example.com');
    await page.locator('input[name="app-setup-prefix-go"]').fill('');
    await page.getByTestId('app-setup-dns-go').waitFor();
    await sleep(300);
    await page.mouse.move(4, 4);
    await mustSay(wizard, 'the Addresses step', ['/l/', 'l.example.com']);
    await shootWhole(page, wizard, SET, 'setup-addresses-1440.png');
    await page.getByTestId('app-setup-next').click();

    // Who may use it.
    await page.getByTestId('app-setup-people').waitFor();
    await sleep(300);
    await page.mouse.move(4, 4);
    await shootWhole(page, wizard, SET, 'setup-people-1440.png');
    await page.getByTestId('app-setup-next').click();
    await page.getByTestId('app-setup-finish').waitFor();
    await page.getByTestId('app-setup-complete').click();
    await page.getByTestId('app-setup-wizard').waitFor({ state: 'hidden', timeout: 15_000 });
    const after = (await admin.json('/api/admin/app-plugins')).plugins?.find?.((p) => p.name === APP);
    if (after && after.state !== 'running') throw new Error(`after Finish the app is ${after.state}, want running`);
    await ctx.close();

    // ── the share dialog, dark: Get short link ───────────────────────────
    const dark = await newContext(browser, { scheme: 'dark', width: 1440, height: 1000 });
    const dpage = await dark.newPage();
    await signIn(dpage, inst.url, ADMIN);
    await dpage.goto(`${inst.url}/drive/explore`);
    await dpage.getByTestId(`sidenav-storage-${STORAGE}`).click();
    const fileRow = dpage.locator('[data-fe-path]').filter({ hasText: FILE }).first();
    await fileRow.click({ button: 'right' });
    await dpage.getByRole('menuitem', { name: /^Share\b/ }).click();
    await dpage.getByTestId('share-short-get').click();
    await dpage.getByTestId('share-full-link').waitFor({ timeout: 10_000 });
    const sheet = dpage.locator('.fe-share');
    const shown = (await sheet.innerText()).match(/https?:\/\/\S+\/l\/\S+/);
    if (!shown) throw new Error(`the dialog shows no short address: ${(await sheet.innerText()).slice(0, 400)}`);
    const short = shown[0];
    await dismissToasts(dpage);
    await dpage.mouse.move(4, 4);
    await mustSay(sheet, 'the share dialog', ['Full link', short]);
    await shot(sheet, SET, 'share-short-dark-1440.png');
    await dark.close();

    // ── what a visitor gets ──────────────────────────────────────────────
    // The addresses are the instance's public ones (files.example.com in the
    // pictures); the requests go to the instance itself.
    const local = (u) => u.replace(/^https?:\/\/[^/]+/, inst.url);
    const visit = await fetch(local(short), { redirect: 'manual' });
    must(visit, 302, 'the short address');
    if (visit.headers.get('location') !== shareURL) throw new Error(`it leads to ${visit.headers.get('location')}, want ${shareURL}`);
    if (visit.headers.get('set-cookie')) throw new Error('the short address sets a cookie');
    if (!(visit.headers.get('cache-control') ?? '').includes('no-store')) throw new Error('the redirect may be cached');
    const preview = await fetch(local(`${short}+`), { redirect: 'manual' });
    must(preview, 200, 'the preview page');
    if (!(await preview.text()).includes(shareURL)) throw new Error('the preview page does not name where the link goes');
    const miss = await fetch(local(`${short.slice(0, short.lastIndexOf('/') + 1)}zzzzzzzzzzzz`), { redirect: 'manual' });
    must(miss, 404, 'a code that leads nowhere');

    // ── an uploader that answers short ───────────────────────────────────
    const uctx = await newContext(browser, { width: 1440, height: 1000 });
    const upage = await uctx.newPage();
    await signIn(upage, inst.url, ADMIN);
    await upage.goto(`${inst.url}/admin/dashboard?settings=1`);
    const settings = upage.getByTestId('user-settings-dialog');
    await settings.waitFor({ timeout: 20_000 });
    await upage.getByTestId('user-settings-tab-uploaders').click();
    await upage.getByTestId('uploaders-panel').waitFor({ timeout: 10_000 });
    await upage.getByTestId('uploaders-name').fill('Screenshots');
    await upage.getByTestId('uploaders-short-on').click();
    await upage.getByTestId('uploaders-short').scrollIntoViewIfNeeded();
    await sleep(300);
    await upage.mouse.move(4, 4);
    await mustSay(settings, 'the Uploaders tab', ['Short link', 'Its short address']);
    await shot(settings, SET, 'uploader-short-1440.png');
    let made = null;
    await upage.route('**/api/auth/uploaders', async (route) => {
      if (route.request().method() !== 'POST') return route.continue();
      const res = await route.fetch();
      made = await res.json();
      await route.fulfill({ response: res, json: made });
    });
    await upage.getByTestId('uploaders-submit').click();
    await upage.getByTestId('uploaders-key').waitFor({ timeout: 10_000 });
    if (made?.uploader?.short_link !== true) throw new Error('the uploader was not made with short_link');
    await uctx.close();
    const ufd = new FormData();
    ufd.append('file', new Blob([Buffer.from('a capture\n')], { type: 'text/plain' }), 'capture.txt');
    const sent = await fetch(`${inst.url}/api/uploads`, { method: 'POST', headers: { Authorization: `Bearer ${made.key}` }, body: ufd });
    const ans = await sent.json();
    must(sent, 200, 'the upload');
    if (!/\/l\/[^/]+$/.test(ans.url ?? '')) throw new Error(`the upload answered ${ans.url}, want its short address`);
    const upVisit = await fetch(local(ans.url), { redirect: 'manual' });
    must(upVisit, 302, "the upload's short address");

    // ── Reconfigure: counted, typed back, then the links are gone ────────
    const rctx = await newContext(browser, { width: 1440, height: 1000 });
    const rpage = await rctx.newPage();
    await signIn(rpage, inst.url, ADMIN);
    await openApp(rpage, inst.url);
    await rpage.getByTestId('app-plugin-page-reconfigure').click();
    const erase = rpage.getByTestId('app-erase');
    await rpage.getByTestId('app-erase-counts').waitFor({ timeout: 15_000 });
    await rpage.getByTestId('app-erase-type').locator('input').fill(APP);
    await sleep(300);
    await rpage.mouse.move(4, 4);
    await mustSay(erase, 'the Reconfigure question', [APP, 'Every short link this app made stops working']);
    await shootWhole(rpage, card(rpage), SET, 'reconfigure-1440.png');
    await rpage.getByTestId('app-erase-confirm').click();
    await rpage.getByTestId('app-setup-wizard').waitFor({ timeout: 15_000 });
    await rpage.getByTestId('app-setup-later').click();
    await rctx.close();
    const gone = await fetch(local(short), { redirect: 'manual' });
    must(gone, 404, 'the short address after Reconfigure');

    log(`the wizard (3 steps), Get short link (dark), the uploader, Reconfigure; 302/preview/404 measured, app ${appId}`);
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
