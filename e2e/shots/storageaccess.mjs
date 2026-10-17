// Storage access — a whole storage given to people and groups (0.57, task
// #263, GitHub #117).
//
//   node e2e/shots/storageaccess.mjs       (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/storageaccess/ (or SHOTS_OUT):
//
//   storage-access-1440.png          Admin → Storages → a storage → Access:
//                                    the people and the group given the whole
//                                    storage, their levels, "Revoke all access"
//   storage-access-off-1440.png      the same section on a storage with per-item
//                                    access control off: why nobody can be given
//                                    it yet, and "Turn on per-item access"
//   storage-access-turn-on-1440.png  the dialog that turns it on: what that does,
//                                    and who keeps access, chosen first
//   storage-row-access-1440.png      the explorer's storage list: a storage
//                                    row's menu with "Access…"
//   storage-root-dialog-1440.png     the access dialog on a storage: People
//                                    only - no link switch, no file request
//   folder-access-whole-1440.png     Admin → Folder access: a grant on the whole
//                                    storage named "Whole storage", not "/"
//
// It also MEASURES, in a real browser and in both languages (jsdom has no
// layout): at 958 and 1440 px the storage page, with the section in both of
// its states, has no horizontal scroll and nothing of its own forms sticks
// out. A failed measurement throws; the pictures are only written by a run
// that passed it.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdirSync } from 'node:fs';
import { chromium } from '@playwright/test';
import { bootInstance, client, layoutProblems, log, mustSay, newContext, setLanguage, shot, signIn, sleep } from './scene.mjs';

const SET = 'storageaccess';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const PEOPLE = [
  { email: 'alice@example.com', display_name: 'Alice Martin' },
  { email: 'bob@example.com', display_name: 'Bob Chen' },
  { email: 'carol@example.com', display_name: 'Carol Diaz' },
];

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });

    // Two storages: Team with per-item access control on, Archive with it off.
    const storage = async (name, rbac) => {
      const root = inst.storageRoot(name.toLowerCase());
      if (!inst.container) mkdirSync(root, { recursive: true });
      return admin.post('/api/admin/storages', {
        name,
        driver: 'local',
        mount_path: root,
        config: { path: root },
        sync_mode: 'ondemand',
        sync_interval_s: 0,
        enabled: true,
        read_only: false,
        rbac_enabled: rbac,
      });
    };
    const team = await storage('Team', true);
    const archive = await storage('Archive', false);
    for (const name of ['Clients', 'Design', 'Finance']) {
      await admin.post('/api/files/manager?action=newfolder', { path: 'Team://', name });
    }

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
    const design = await admin.post('/api/admin/groups', { name: 'Design', description: 'Brand and product design' });
    const designId = design.group.id;
    await admin.post(`/api/admin/groups/${designId}/members`, { user_ids: [ids['carol@example.com']] });

    // The whole of Team: a person as editor, another as viewer, a group as
    // viewer - and, beside them, a folder grant of the viewer's own.
    await admin.post('/api/files/permissions', { path: 'Team://', user_id: ids['alice@example.com'], level: 'editor' });
    await admin.post('/api/files/permissions', { path: 'Team://', user_id: ids['bob@example.com'], level: 'viewer' });
    await admin.post('/api/files/permissions', { path: 'Team://', group_id: designId, level: 'viewer' });
    await admin.post('/api/files/permissions', { path: 'Team://Finance', user_id: ids['bob@example.com'], level: 'editor' });
    await admin.post('/api/notifications/read-all', {});

    const ctx = await newContext(browser, { height: 900 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);

    const open = async (url, ready, width, height = 900) => {
      await page.setViewportSize({ width, height });
      await page.goto(url);
      await page.getByTestId(ready).first().waitFor({ timeout: 20_000 });
      await page.mouse.move(4, 4);
      await sleep(500);
    };
    const teamPage = `${inst.url}/admin/storages/${team.id}`;
    const archivePage = `${inst.url}/admin/storages/${archive.id}`;
    const section = page.getByTestId('storage-access');

    // ── measure: both widths, both languages ────────────────────────────────
    const problems = [];
    for (const locale of ['en', 'tr']) {
      await setLanguage(admin, page, locale);
      for (const width of [958, 1440]) {
        for (const [what, url] of [['Team (access control on)', teamPage], ['Archive (access control off)', archivePage]]) {
          await open(url, 'storage-access', width);
          for (const x of await layoutProblems(page, { frames: ['storage-access'] })) problems.push(`${locale} ${width}px ${what}: ${x}`);
        }
      }
      if (locale === 'tr') {
        await open(teamPage, 'storage-access', 1440);
        await mustSay(section, 'the Turkish Access section', ['Erişim', 'Tüm erişimi geri al', 'Düzenleyen', 'Klasör erişimi']);
        await open(archivePage, 'storage-access', 1440);
        await mustSay(section, 'the Turkish section, access control off', ['öğe bazlı erişim denetimi kapalı', 'Öğe bazlı erişimi aç']);
      }
    }
    if (problems.length) throw new Error(`layout problems:\n  ${problems.join('\n  ')}`);
    log('no overflow or overlap at 958 and 1440 px, in English and Turkish');

    // ── pictures, English ───────────────────────────────────────────────────
    await setLanguage(admin, page, 'en');

    // 1. The Access section of a storage with access control on.
    await open(teamPage, 'storage-access', 1440, 1000);
    await section.scrollIntoViewIfNeeded();
    await mustSay(section, 'the Access section', ['Access', 'Alice Martin', 'Bob Chen', 'Design', 'Editor', 'Viewer', 'Revoke all access', 'Folder access']);
    await sleep(300);
    await shot(section, SET, 'storage-access-1440.png');

    // 2. The same section, access control off.
    await open(archivePage, 'storage-access', 1440, 1000);
    await section.scrollIntoViewIfNeeded();
    await mustSay(section, 'the section with access control off', ['Per-item access control is off', 'Turn on per-item access']);
    await sleep(300);
    await shot(section, SET, 'storage-access-off-1440.png');

    // 3. Its dialog, with the person who keeps access chosen first.
    await page.getByTestId('storage-access-enable').click();
    const dialog = page.getByTestId('storage-access-enable-dialog');
    await dialog.waitFor({ timeout: 10_000 });
    await page.getByTestId('storage-access-keep-search').locator('input').fill('ali');
    await page.getByTestId(`storage-access-keep-option-u${ids['alice@example.com']}`).click();
    await page.getByTestId('storage-access-keepers').waitFor({ timeout: 10_000 });
    await mustSay(dialog, 'the turn-on dialog', ['Choose who keeps access first', 'Alice Martin']);
    await page.mouse.move(4, 4);
    await sleep(400);
    await shot(page.locator('dialog[open] > [role="dialog"]'), SET, 'storage-access-turn-on-1440.png');
    await page.keyboard.press('Escape');

    // 4. The explorer's storage list: a storage row's menu.
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(`${inst.url}/admin/explore`);
    const row = page.locator('[data-fe-path="Team"]').first();
    await row.waitFor({ timeout: 20_000 });
    await row.click({ button: 'right' });
    const entry = page.getByRole('menuitem', { name: /^Access…/ });
    await entry.waitFor({ timeout: 10_000 });
    await sleep(300);
    await shot(page, SET, 'storage-row-access-1440.png');

    // 5. The access dialog on the storage: People only.
    await entry.click();
    const share = page.locator('.fe-share');
    await share.waitFor({ timeout: 15_000 });
    await page.getByTestId('share-storage-root-hint').waitFor({ timeout: 10_000 });
    await mustSay(share, 'the access dialog on a storage', ['Access to "Team"', 'Whole storage', 'Alice Martin', 'Design']);
    await page.mouse.move(4, 4);
    await sleep(400);
    await shot(share, SET, 'storage-root-dialog-1440.png');
    await page.keyboard.press('Escape');

    // 6. Folder access: the grant on the whole storage, in words.
    await open(`${inst.url}/admin/grants`, 'grant-whole-storage', 1440);
    await mustSay(page.locator('main'), 'Folder access', ['Whole storage', 'Alice Martin', 'Finance/…']);
    await shot(page, SET, 'folder-access-whole-1440.png');

    log('the Access section on and off, its turn-on dialog, the storage row, the dialog on a storage, Folder access');
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
