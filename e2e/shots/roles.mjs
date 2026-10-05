// Roles and folder access — the administrator's two answers to "who may do
// what" (0.49.0: custom roles, their names in other languages, and the
// per-folder grants page apart from them).
//
//   node e2e/shots/roles.mjs       (from the repo root; `pnpm shots` runs it)
//
// Writes docs/screenshots/<release>/roles/:
//
//   roles-list-1440.png     Admin → Roles: the three built-in roles and two
//                           custom ones in the explorer's table — how many
//                           people hold each, what it allows, where it differs
//                           by folder, its limits, and the switch that turns a
//                           custom role off
//   role-editor-1440.png    a custom role's editor, whole: name, description,
//                           "Name and description in other languages" open
//                           with the Turkish name filled in, the permissions,
//                           the folders it differs in and the SSO group that
//                           starts people on it
//   folder-access-1440.png  Admin → Folder access (the page that used to be
//                           called Permissions): every per-folder grant — who,
//                           which storage, which path, which level
//   roles-gaps-1280.png     Admin → Roles at 1280 with the warning above the
//                           table (0.52.0, PR #86): the built-in User role and
//                           a custom "Drop box" role saved the way 0.50's pages
//                           wrote them, each with Give back and Dismiss
//
// ⚠ No app is installed here, on purpose: this scene runs in CI too. What an
// app adds to these screens (the Apps group on a role and on a person) is
// apppermissions.mjs's job.
//
// ⚠ The people and roles are seeded through the API as a signed-in
// administrator; the grants need a storage with RBAC on (the server refuses a
// grant elsewhere, handlers/grants.go → Create).
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdirSync } from 'node:fs';
import { chromium } from '@playwright/test';
import { bootInstance, client, log, newContext, shot, signIn, sleep } from './scene.mjs';

const SET = 'roles';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const PEOPLE = [
  { email: 'deniz@example.com', display_name: 'Deniz Kaya', role: 'accounting' },
  { email: 'ece@example.com', display_name: 'Ece Aydın', role: 'accounting' },
  { email: 'sam@example.com', display_name: 'Sam Carter', role: 'contractors' },
  { email: 'lena@example.com', display_name: 'Lena Fischer', role: 'user' },
  { email: 'omar@example.com', display_name: 'Omar Haddad', role: 'viewer' },
];
const FOLDERS = ['Archive', 'Clients', 'Finance', 'Scratch'];
/** Every permission of the built-in User role (e2e/tests/178's list, with delete). */
const STANDARD = [
  'files.download', 'files.create', 'files.modify', 'files.rename', 'files.move', 'files.delete', 'files.purge',
  'files.encrypt', 'files.tag', 'share.links', 'share.upload_links', 'share.users', 'comments.write', 'ai.use',
  'plugins.run', 'access.webdav', 'access.sftp', 'access.ftp', 'access.s3', 'access.nfs', 'access.api',
  'access.desktop', 'account.edit',
];
const without = (...drop) => STANDARD.filter((p) => !drop.includes(p));

/** Throws unless `el` says every one of `wants`. */
async function mustSay(el, what, wants) {
  const text = (await el.innerText()).replace(/\s+/g, ' ');
  for (const w of wants) {
    if (!text.includes(w)) throw new Error(`${what} does not say "${w}": "${text.slice(0, 600)}"`);
  }
}

/**
 * Grows the window until the open dialog's card fits, then shoots the card
 * (apppermissions.mjs → shootDialog). ⚠ The <dialog> is scrolled back to its
 * top before every measurement (lesson from pluginrequests.mjs): a dialog
 * scrolled by an earlier click starts above the window, and growing the
 * window alone never brings it back.
 */
async function shootDialog(page, file) {
  const card = page.locator('dialog[open] [role="dialog"]').last();
  for (let i = 0; i < 6; i++) {
    await page.evaluate(() => document.querySelector('dialog[open]')?.scrollTo(0, 0));
    await sleep(200);
    const box = await card.boundingBox();
    const view = page.viewportSize();
    if (!box || !view) throw new Error('the dialog has no box to measure');
    if (box.y >= 0 && box.y + box.height <= view.height) {
      await shot(card, SET, file);
      return;
    }
    await page.setViewportSize({ width: view.width, height: Math.min(3200, Math.ceil(Math.max(0, box.y) + box.height + 96)) });
    await sleep(300);
  }
  throw new Error(`${file}: the dialog does not fit the window — the picture would be stitched`);
}

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });

    // A team storage with RBAC on, and the folders the roles and grants name.
    const root = inst.storageRoot('team');
    if (!inst.container) mkdirSync(root, { recursive: true });
    const team = await admin.post('/api/admin/storages', {
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
    for (const name of FOLDERS) {
      await admin.post('/api/files/manager?action=newfolder', { path: 'Team://', name });
    }
    for (const [parent, name] of [['Finance', '2026'], ['Clients', 'Northwind']]) {
      await admin.post('/api/files/manager?action=newfolder', { path: `Team://${parent}`, name });
    }

    // Two custom roles. Accounting has a Turkish name, differs in one folder
    // and is what the SSO group "finance" starts on; Contractors carries limits.
    const accounting = await admin.post('/api/admin/roles', {
      name: 'Accounting',
      description: 'Invoices and ledgers, no public links',
      names: { tr: 'Muhasebe' },
      descriptions: { tr: 'Faturalar ve defterler, herkese açık bağlantı yok' },
      enabled: true,
      permissions: without('share.links', 'share.upload_links'),
      effects: { 'files.modify': 'deny', 'files.delete': 'deny' },
      conditions: { storage_ids: [team.id], paths: ['Archive'] },
      targets: [{ kind: 'sso_group', value: 'finance' }],
    });
    const contractors = await admin.post('/api/admin/roles', {
      name: 'Contractors',
      description: 'Outside staff - no deleting, links for a week at most',
      enabled: true,
      permissions: without('files.delete', 'files.purge', 'access.nfs', 'access.s3'),
      settings: { share_link_max_days: 7, share_link_password_required: true, require_2fa: true },
    });
    const roleIds = { accounting: accounting.id, contractors: contractors.id };

    const ids = {};
    for (const p of PEOPLE) {
      const builtin = p.role === 'viewer' ? 'viewer' : 'user';
      const made = await admin.post('/api/admin/users', {
        email: p.email,
        password: `${p.email.split('@')[0]}-shots-2026`,
        display_name: p.display_name,
        role: builtin,
        locale: 'en',
      });
      ids[p.email] = made.id;
      if (roleIds[p.role]) {
        await admin.json(`/api/admin/users/${made.id}/roles`, {
          method: 'PUT',
          body: JSON.stringify({ role_id: roleIds[p.role] }),
        });
      }
    }
    // ⚠ Read back one non-ASCII name: a mangled body would put "Ayd�n" into
    // every picture below and read like a product bug.
    const ece = await admin.json(`/api/admin/users/${ids['ece@example.com']}`);
    const eceName = ece.display_name ?? ece.user?.display_name;
    if (eceName !== 'Ece Aydın') throw new Error(`the seeded name came back as ${JSON.stringify(eceName)}`);

    for (const [path, email, level] of [
      ['Team://Finance', 'deniz@example.com', 'owner'],
      ['Team://Finance/2026', 'ece@example.com', 'editor'],
      ['Team://Clients/Northwind', 'sam@example.com', 'editor'],
      ['Team://Archive', 'lena@example.com', 'viewer'],
      ['Team://Clients', 'omar@example.com', 'viewer'],
    ]) {
      await admin.post('/api/files/permissions', { path, user_id: ids[email], level });
    }
    await admin.post('/api/notifications/read-all', {});

    const ctx = await newContext(browser, { height: 900 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);

    // 1. Admin → Roles.
    await page.goto(`${inst.url}/admin/roles`);
    const list = page.getByTestId('roles-list');
    await list.waitFor({ timeout: 20_000 });
    await page.getByTestId(`role-name-rule-${accounting.id}`).waitFor({ timeout: 15_000 });
    await page.getByTestId(`role-name-rule-${contractors.id}`).waitFor({ timeout: 15_000 });
    await mustSay(page.locator('main'), 'the Roles page', [
      'Roles', 'Administrator', 'User', 'Viewer', 'Accounting', 'Contractors', 'Invoices and ledgers',
    ]);
    await mustSay(page.getByTestId(`role-members-rule-${accounting.id}`), "Accounting's members", ['2']);
    await page.mouse.move(4, 4);
    await sleep(600);
    await shot(page, SET, 'roles-list-1440.png');

    // 2. A custom role's editor, its other-language names open.
    await page.getByTestId(`role-actions-rule-${accounting.id}`).click();
    await page.locator(`.fe-ctx [data-testid="role-actions-rule-${accounting.id}-edit"]`).last().click();
    const editor = page.getByTestId('rule-editor');
    await editor.waitFor({ timeout: 15_000 });
    const names = editor.getByTestId('role-translations');
    await names.locator('summary').click();
    // The test id sits on the Input component's wrapper, not on the <input>.
    const trName = editor.getByTestId('role-translation-name-tr').locator('input');
    await trName.waitFor({ timeout: 10_000 });
    if ((await trName.inputValue()) !== 'Muhasebe') throw new Error(`the Turkish name box holds "${await trName.inputValue()}"`);
    await editor.getByTestId('role-folder-effects').waitFor({ timeout: 10_000 });
    await mustSay(editor, 'the role editor', [
      'Name and description in other languages', 'Role name (Türkçe)', 'Permissions', 'Different in some folders', 'Archive',
    ]);
    await page.mouse.move(4, 4);
    await shootDialog(page, 'role-editor-1440.png');
    await page.keyboard.press('Escape');
    await page.setViewportSize({ width: 1440, height: 900 });

    // 3. Admin → Folder access.
    await page.goto(`${inst.url}/admin/grants`);
    await page.waitForFunction(
      () => document.querySelectorAll('[data-testid^="grant-actions-"]').length >= 5,
      undefined,
      { timeout: 20_000 },
    );
    await mustSay(page.locator('main'), 'the Folder access page', [
      'Folder access', 'Deniz Kaya', 'Sam Carter', 'Team', 'Finance', 'owner', 'editor', 'viewer',
    ]);
    await page.mouse.move(4, 4);
    await sleep(600);
    await shot(page, SET, 'folder-access-1440.png');

    // 4. A role that may have lost a permission (0.52.0, PR #86). Taken last:
    // it changes the User role. Both lists are written the way 0.50's pages
    // wrote them - the User role's list without files.encrypt, a folder part
    // that allows files.create - through the API without `shown`, as
    // e2e/tests/203 does, so Admin → Roles points both out.
    const builtin = await admin.json('/api/admin/roles/builtin');
    await admin.json('/api/admin/roles/builtin', {
      method: 'PUT',
      body: JSON.stringify({ permissions: builtin.permissions.filter((k) => k !== 'files.encrypt') }),
    });
    await admin.post('/api/admin/roles', {
      name: 'Drop box',
      description: 'Partners leave files in Drop and see nothing else',
      enabled: true,
      permissions: ['files.download'],
      effects: { 'files.create': 'allow' },
      conditions: { paths: ['Drop'] },
    });
    await sleep(1000);
    await admin.post('/api/notifications/read-all', {});
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(`${inst.url}/admin/roles`);
    const gaps = page.getByTestId('role-gaps');
    await gaps.waitFor({ timeout: 20_000 });
    await page.getByTestId('roles-list').waitFor({ timeout: 15_000 });
    await mustSay(gaps, 'the warning above the table', [
      '2 roles may have lost a permission', '0.50', 'User', 'Drop box', 'Give back', 'Dismiss, it was on purpose',
    ]);
    await page.mouse.move(4, 4);
    await sleep(600);
    await shot(page, SET, 'roles-gaps-1280.png');
    log('roles, a role editor with its Turkish name, folder access, and the warning about a lost permission');
    await ctx.close();
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
