// App permissions — what an installed app lets the administrator give or take
// away per role and per person (0.49.0, backend perm/app.go).
//
//   node e2e/shots/apppermissions.mjs     (from the repo root; `pnpm shots` runs it)
//
// Writes docs/screenshots/<release>/apppermissions/:
//
//   role-user.png            Admin → Roles → "Edit the User role": the Apps
//                            group under the permission groups, the signing
//                            app's "Request signatures" with Default (allowed)
//                            / Allow / Deny — Default says what the app's own
//                            default gives this role
//   role-custom.png          a custom role's permissions: the same grid, the
//                            role's own decision (Deny) picked
//   person-exceptions.png    a person's page: their exception (Deny) beside the
//                            answer and where it comes from, and Default still
//                            saying what their role alone would give them
//   person-delegated.png     the same page as a delegated administrator: the
//                            app rows read-only, and why
//
// ⚠ The app is the real signing app (e2e/helpers/app-locations.mjs → sign),
// from 0.2.0 on — the first build that declares `user_permissions`. An older
// build has nothing for these screens to show, so the scene refuses it rather
// than photograph an empty group.
//
// Environment: FILEX_BIN, FILEX_SIGN_APP_DIR, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { chromium } from '@playwright/test';
import { bootInstance, client, findApp, installApp, log, newContext, shot, signIn, sleep } from './scene.mjs';

const SET = 'apppermissions';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const PERSON = { email: 'deniz@example.com', password: 'deniz-shots-2026', display_name: 'Deniz Kaya' };
const DELEGATE = { email: 'ece@example.com', password: 'ece-shots-2026', display_name: 'Ece Aydın' };
const STANDARD = [
  'files.download', 'files.create', 'files.modify', 'files.rename', 'files.move', 'files.purge', 'files.encrypt',
  'files.tag', 'share.links', 'share.upload_links', 'share.users', 'comments.write', 'ai.use', 'plugins.run',
  'access.webdav', 'access.sftp', 'access.ftp', 'access.s3', 'access.nfs', 'access.api', 'access.desktop',
  'account.edit',
];

/** Collapses Files (open by default) and opens Apps, so the picture is about the apps. */
async function focusApps(scope) {
  await scope.getByTestId('perm-group-toggle-files').first().click();
  await scope.getByTestId('perm-group-toggle-apps').first().click();
  await scope.getByTestId('perm-group-apps').first().waitFor();
}

/** Throws unless `el` says every one of `wants`. */
async function mustSay(el, what, wants) {
  const text = (await el.innerText()).replace(/\s+/g, ' ');
  for (const w of wants) {
    if (!text.includes(w)) throw new Error(`${what} does not say "${w}": "${text.slice(0, 600)}"`);
  }
}

/**
 * Grows the window until the open dialog fits, then shoots it (pluginrequests.mjs).
 *
 * ⚠ The CARD, not the <dialog>: the admin panel's Modal (web/src/components/
 * ui/Modal.vue) is a full-width <dialog> with the card centred inside it, so
 * shooting the <dialog> puts the blurred, dimmed page on both sides of the
 * editor — the first 0.49.0 take of role-user.png had the admin table
 * showing through the backdrop.
 */
async function shootDialog(page, file) {
  const dialog = page.locator('dialog[open] [role="dialog"]').last();
  for (let i = 0; i < 6; i++) {
    await page.evaluate(() => document.querySelector('dialog[open]')?.scrollTo(0, 0));
    await sleep(200);
    const box = await dialog.boundingBox();
    const view = page.viewportSize();
    if (!box || !view) throw new Error('the dialog has no box to measure');
    if (box.y >= 0 && box.y + box.height <= view.height) {
      await shot(dialog, SET, file);
      return;
    }
    await page.setViewportSize({ width: view.width, height: Math.min(2600, Math.ceil(Math.max(0, box.y) + box.height + 96)) });
    await sleep(300);
  }
  throw new Error(`${file}: the dialog does not fit the window — the picture would be stitched`);
}

async function main() {
  const sign = findApp('sign');
  const declared = sign.manifest.user_permissions ?? [];
  if (!declared.some((p) => p.id === 'request')) {
    throw new Error(
      `the signing app build at ${sign.manifestPath} (${sign.manifest.version}) declares no "request" user permission — ` +
        'it predates 0.2.0; build filex-sign (bash scripts/build.sh --stamp) and run again',
    );
  }
  const KEY = 'app.sign.request';
  log(`sign ${sign.manifest.version}: ${declared.map((p) => p.id).join(', ')}`);

  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Demo' });
    await installApp(admin, sign);

    const cat = await admin.json('/api/admin/roles/catalogue');
    if (!(cat.apps ?? []).some((a) => a.key === KEY)) throw new Error(`the catalogue lists no ${KEY}: ${JSON.stringify(cat.apps)}`);

    // A custom role that takes asking for signatures away; a person on User
    // with their own exception; a delegated administrator (admin.users only).
    const legal = await admin.post('/api/admin/roles', {
      name: 'Contractors',
      description: 'Outside staff: every file action, no signature requests',
      enabled: true,
      permissions: STANDARD,
      settings: { apps: { [KEY]: 'deny' } },
    });
    const person = await admin.post('/api/admin/users', { ...PERSON, role: 'user', locale: 'en' });
    await admin.json(`/api/admin/users/${person.id}/exceptions`, {
      method: 'PUT',
      body: JSON.stringify({ overrides: { [KEY]: 'deny' } }),
    });
    const delegate = await admin.post('/api/admin/users', { ...DELEGATE, role: 'user', locale: 'en' });
    await admin.json(`/api/admin/users/${delegate.id}/exceptions`, {
      method: 'PUT',
      body: JSON.stringify({ overrides: { 'admin.users': 'allow' } }),
    });
    await admin.post('/api/notifications/read-all', {});

    const ctx = await newContext(browser, { height: 1000 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);

    // 1. The User role.
    await page.goto(`${inst.url}/admin/roles`);
    await page.getByTestId('roles-list').waitFor({ timeout: 20_000 });
    await page.getByTestId('role-actions-builtin-user').click();
    await page.locator('.fe-ctx [data-testid="role-actions-builtin-user-edit"]').last().click();
    const builtin = page.getByTestId('builtin-role-user');
    await builtin.waitFor({ timeout: 15_000 });
    await focusApps(builtin);
    await mustSay(builtin, 'the User role', ['Apps', 'e-Signature', 'Request signatures', 'Default (allowed)']);
    await shootDialog(page, 'role-user.png');
    await page.keyboard.press('Escape');
    await page.setViewportSize({ width: 1440, height: 1000 });

    // 2. A custom role.
    await page.getByTestId(`role-actions-rule-${legal.id}`).click();
    await page.locator(`.fe-ctx [data-testid="role-actions-rule-${legal.id}-edit"]`).last().click();
    const grid = page.getByTestId('role-permissions');
    await grid.waitFor({ timeout: 15_000 });
    await focusApps(grid);
    const denied = grid.getByTestId(`perm-${KEY}-deny`);
    if ((await denied.getAttribute('aria-checked')) !== 'true') throw new Error("the custom role's Deny is not the picked choice");
    await grid.scrollIntoViewIfNeeded();
    await shot(grid, SET, 'role-custom.png');
    await page.keyboard.press('Escape');

    // 3. The person's page.
    await page.goto(`${inst.url}/admin/users/${person.id}`);
    const card = page.getByTestId('user-permissions-card');
    await card.waitFor({ timeout: 20_000 });
    await focusApps(card);
    const said = card.getByTestId(`perm-effective-${KEY}`);
    if ((await said.getAttribute('data-source')) !== 'override' || (await said.getAttribute('data-allowed')) !== 'false') {
      throw new Error(`the person's answer is not their own Deny: ${await said.innerText()}`);
    }
    await mustSay(card, "the person's card", ['Request signatures', 'Default (allowed)']);
    await card.scrollIntoViewIfNeeded();
    await shot(card, SET, 'person-exceptions.png');
    await ctx.close();

    // 4. The same page, as a delegated administrator.
    const dctx = await newContext(browser, { height: 1000 });
    const dpage = await dctx.newPage();
    await signIn(dpage, inst.url, DELEGATE);
    await dpage.goto(`${inst.url}/admin/users/${person.id}`);
    const dcard = dpage.getByTestId('user-permissions-card');
    await dcard.waitFor({ timeout: 20_000 });
    await focusApps(dcard);
    await dcard.getByTestId('perm-apps-read-only').waitFor();
    if (!(await dcard.getByTestId(`perm-${KEY}-deny`).isDisabled())) throw new Error('the app rows are not read-only for a delegated administrator');
    await dcard.scrollIntoViewIfNeeded();
    await shot(dcard, SET, 'person-delegated.png');
    await dctx.close();
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
