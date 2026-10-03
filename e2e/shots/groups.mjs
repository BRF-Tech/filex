// Groups — named sets of people with folder access and a role (0.50.0,
// GitHub PR #78).
//
//   node e2e/shots/groups.mjs       (from the repo root; `pnpm shots` runs it)
//
// Writes docs/screenshots/<release>/groups/ (or SHOTS_OUT):
//
//   groups-list-1440.png    Admin → Groups in the explorer's table: each
//                           group's role and priority, members, folders and
//                           the SSO groups it follows
//   group-page-1440.png     one group's page: name, role, role priority, SSO
//                           groups, its members and the folders it reaches
//   user-groups-1440.png    a person's page: the role they hold through a
//                           group, and the groups they are in
//   share-group-1440.png    the explorer's sharing panel: a group offered
//                           beside people, and the question asked in the
//                           dialog before a group is given Owner
//
// It also MEASURES, in a real browser and in both languages (jsdom has no
// layout): at 958 and 1440 px the groups page, a group's page and a person's
// page have no horizontal scroll and nothing of their own forms sticks out,
// and the tables keep their columns inside their own frames. A failed
// measurement throws; the pictures are only written by a run that passed it.
//
// ⚠ No SSO sign-in happens here, so every member is "Added": the SSO links
// are seeded to be seen, not followed.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdirSync } from 'node:fs';
import { chromium } from '@playwright/test';
import { bootInstance, client, layoutProblems, log, mustSay, newContext, setLanguage, shot, signIn, sleep } from './scene.mjs';

const SET = 'groups';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const PEOPLE = [
  { email: 'deniz@example.com', display_name: 'Deniz Kaya' },
  { email: 'ece@example.com', display_name: 'Ece Aydın' },
  { email: 'sam@example.com', display_name: 'Sam Carter' },
  { email: 'lena@example.com', display_name: 'Lena Fischer' },
  { email: 'omar@example.com', display_name: 'Omar Haddad' },
];
const FOLDERS = ['Clients', 'Design', 'Finance'];
/** Every permission of the built-in User role (roles.mjs's list). */
const STANDARD = [
  'files.download', 'files.create', 'files.modify', 'files.rename', 'files.move', 'files.delete', 'files.purge',
  'files.tag', 'share.links', 'share.upload_links', 'share.users', 'comments.write', 'ai.use', 'plugins.run',
  'access.webdav', 'access.sftp', 'access.ftp', 'access.s3', 'access.nfs', 'access.api', 'access.desktop',
  'account.edit',
];
const without = (...drop) => STANDARD.filter((p) => !drop.includes(p));

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });

    // A team storage with RBAC on, and the folders the groups reach.
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
    for (const name of FOLDERS) {
      await admin.post('/api/files/manager?action=newfolder', { path: 'Team://', name });
    }

    const accounting = await admin.post('/api/admin/roles', {
      name: 'Accounting',
      description: 'Invoices and ledgers, no public links',
      enabled: true,
      permissions: without('share.links', 'share.upload_links'),
    });
    const contractors = await admin.post('/api/admin/roles', {
      name: 'Contractors',
      description: 'Outside staff, no deleting',
      enabled: true,
      permissions: without('files.delete', 'files.purge', 'access.nfs', 'access.s3'),
    });

    const ids = {};
    for (const p of PEOPLE) {
      const made = await admin.post('/api/admin/users', {
        email: p.email,
        password: `${p.email.split('@')[0]}-shots-2026`,
        display_name: p.display_name,
        role: 'user',
        locale: 'en',
      });
      ids[p.email] = made.id;
    }

    const group = async (body, members, grants) => {
      const made = await admin.post('/api/admin/groups', body);
      const id = made.group.id;
      await admin.post(`/api/admin/groups/${id}/members`, { user_ids: members.map((e) => ids[e]) });
      for (const [path, level] of grants) await admin.post('/api/files/permissions', { path, group_id: id, level });
      return id;
    };
    const finance = await group(
      {
        name: 'Finance',
        description: 'Everyone who approves invoices',
        role_id: accounting.id,
        links: [{ kind: 'sso', value: 'finance' }, { kind: 'sso', value: 'accounts-payable' }],
      },
      ['deniz@example.com', 'ece@example.com'],
      [['Team://Finance', 'editor']],
    );
    await group(
      { name: 'Contractors', description: 'Agencies we work with', role_id: contractors.id, priority: 5 },
      ['sam@example.com'],
      [['Team://Clients', 'viewer']],
    );
    await group(
      { name: 'Design', description: 'Brand and product design', links: [{ kind: 'sso', value: 'design' }] },
      ['lena@example.com', 'omar@example.com'],
      [['Team://Design', 'editor'], ['Team://Clients', 'viewer']],
    );
    await admin.post('/api/notifications/read-all', {});

    const ctx = await newContext(browser, { height: 900 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);

    const pages = [
      { what: 'the Groups page', url: `${inst.url}/admin/groups`, ready: `group-${finance}`, frames: ['groups-list'] },
      { what: "a group's page", url: `${inst.url}/admin/groups/${finance}`, ready: 'group-members', frames: ['group-members', 'group-grants'] },
      { what: "a person's page", url: `${inst.url}/admin/users/${ids['deniz@example.com']}`, ready: 'user-groups-card', frames: ['user-groups-card'] },
    ];
    const open = async (p, width, height = 900) => {
      await page.setViewportSize({ width, height });
      await page.goto(p.url);
      await page.getByTestId(p.ready).first().waitFor({ timeout: 20_000 });
      await page.mouse.move(4, 4);
      await sleep(500);
    };

    // ── measure: both widths, both languages ────────────────────────────────
    const problems = [];
    for (const locale of ['en', 'tr']) {
      await setLanguage(admin, page, locale);
      for (const width of [958, 1440]) {
        for (const p of pages) {
          await open(p, width);
          for (const x of await layoutProblems(page, { frames: p.frames })) problems.push(`${locale} ${width}px ${p.what}: ${x}`);
        }
      }
      if (locale === 'tr') {
        await open(pages[1], 1440);
        await mustSay(page.locator('main'), 'the Turkish group page', ['Rol önceliği', 'SSO grupları', 'Üyeler', 'Klasör erişimi', 'Nasıl katıldı', 'Düzenleyen']);
        await open(pages[2], 1440);
        await mustSay(page.locator('main'), 'the Turkish person page', ['Gruplar', '“Finance” grubu üzerinden']);
      }
    }
    if (problems.length) throw new Error(`layout problems:\n  ${problems.join('\n  ')}`);
    log('no overflow or overlap at 958 and 1440 px, in English and Turkish');

    // ── pictures, English ───────────────────────────────────────────────────
    await setLanguage(admin, page, 'en');

    // 1. Admin → Groups.
    await open(pages[0], 1440);
    await mustSay(page.locator('main'), 'the Groups page', [
      'Groups', 'Finance', 'Contractors', 'Design', 'Accounting', 'priority 5', '2 members', '2 folders', 'finance, accounts-payable',
    ]);
    await shot(page, SET, 'groups-list-1440.png');

    // 2. One group's page, whole.
    await open(pages[1], 1440, 1250);
    await mustSay(page.locator('main'), "the Finance group's page", [
      'Finance', 'Role priority', 'SSO groups', 'Members', 'Deniz Kaya', 'Ece Aydın', 'Folder access', 'Team://Finance', 'Editor',
    ]);
    await shot(page, SET, 'group-page-1440.png');

    // 3. A person's page: the role through the group, and the group.
    await open(pages[2], 1440, 1100);
    await mustSay(page.locator('main'), "Deniz's page", ['Accounting', 'through the group “Finance”', 'Groups', 'Finance', 'Added']);
    await page.getByTestId('user-groups-card').scrollIntoViewIfNeeded();
    await sleep(300);
    await shot(page, SET, 'user-groups-1440.png');

    // 4. The sharing panel: a group beside people, and Owner asked in the dialog.
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(`${inst.url}/drive/explore`);
    await page.getByTestId('sidenav-storage-Team').click();
    const row = page.locator('[data-fe-path]').filter({ hasText: 'Design' }).first();
    await row.waitFor({ timeout: 20_000 });
    await row.click({ button: 'right' });
    await page.getByRole('menuitem', { name: /^Share\b/ }).click();
    const dialog = page.locator('.fe-share');
    await dialog.waitFor({ timeout: 15_000 });
    const people = page.getByTestId('share-people-toggle');
    if ((await people.getAttribute('aria-expanded')) !== 'true') await people.click();
    await page.getByTestId('share-add-person').locator('select').selectOption('owner');
    await page.getByTestId('share-add-person').locator('input').fill('fin');
    await page.getByTestId('share-suggest-group').first().waitFor({ timeout: 10_000 });
    await page.getByTestId('share-suggest-group').first().dispatchEvent('mousedown');
    await page.getByTestId('share-group-owner-ask').waitFor({ timeout: 10_000 });
    await mustSay(dialog, 'the sharing panel', ['Design', 'Group', 'Owner lets everyone in “Finance”', 'Give owner access']);
    await page.mouse.move(4, 4);
    await sleep(400);
    await shot(dialog, SET, 'share-group-1440.png');
    log('groups list, a group page, a person with a group role, the sharing panel with a group');
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
