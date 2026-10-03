/**
 * 178 — roles and per-user permissions (PR #75), one journey end to end.
 *
 * The pull request's own manual check, made repeatable: a person on a custom
 * role "No delete, except in Scratch" is OFFERED Delete on a file in Scratch
 * and not on one in Finance; the server agrees on both (the explorer only
 * hides what the server would refuse, it decides nothing). Then the two admin
 * screens the change adds or reworks: Admin → Roles is the explorer's own
 * table (the one-table rule), and Admin → Users names the person's role.
 *
 * Everything the journey needs is made through the API as the administrator;
 * every assertion about what the person may do is made from their session.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { apiLogin, dismissInstallBanner, loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';

const RUN = Date.now();
const STORAGE = `e2e-roles-${RUN}`;
const PERSON = `roles-person-${RUN}@example.com`;
const PERSON_PW = 'roles-person-pw-2026';
const ROLE = `No delete except Scratch ${RUN}`;

// Standard user minus files.delete — every permission outside the admin area.
const STANDARD_WITHOUT_DELETE = [
  'files.download', 'files.create', 'files.modify', 'files.rename', 'files.move', 'files.purge', 'files.encrypt',
  'files.tag', 'share.links', 'share.upload_links', 'share.users', 'comments.write', 'ai.use', 'plugins.run',
  'access.webdav', 'access.sftp', 'access.ftp', 'access.s3', 'access.nfs', 'access.api', 'access.desktop',
  'account.edit',
];

let api: APIRequestContext;
let roleId = 0;
let personId = 0;

async function signInAsPerson(page: Page) {
  await dismissInstallBanner(page);
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await page.goto('/admin/login');
  await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(PERSON);
  await page.getByLabel(/password|parola|şifre/i).fill(PERSON_PW);
  await page
    .getByRole('button', { name: 'Sign in', exact: true })
    .or(page.getByRole('button', { name: 'Oturum aç', exact: true }))
    .first()
    .click();
  await page.waitForURL(/\/drive\//, { timeout: 15_000 });
}

/** The labels of the right-click menu on `name` inside `folder`. */
async function menuFor(page: Page, folder: string, name: string): Promise<string[]> {
  await page.goto(`/drive/explore#${encodeURIComponent(STORAGE)}/${folder}`);
  const row = page.locator('[data-fe-path]').filter({ hasText: name }).first();
  await expect(row).toBeVisible({ timeout: 15_000 });
  await row.click({ button: 'right' });
  const menu = page.locator('.fe-ctx').last();
  await expect(menu).toBeVisible();
  // The per-folder answer arrives from the server a moment after the menu
  // opens (POST /api/files/manager?action=allowed); wait for it to settle.
  await page.waitForLoadState('networkidle');
  const labels = (await menu.locator('.fe-ctx__item:visible .fe-ctx__label').allTextContents()).map((s) => s.trim());
  await page.keyboard.press('Escape');
  return labels;
}

test.describe('roles: a folder exception, and the admin screens', () => {
  test.beforeAll(async ({ playwright, baseURL, request }) => {
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await dropStorageByName(request, STORAGE);
    const st = await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
    for (const folder of ['Scratch', 'Finance']) {
      const mk = await api.post('/api/files/manager?action=newfolder', { data: { path: `${STORAGE}://`, name: folder } });
      expect(mk.ok(), `newfolder ${folder}: ${mk.status()}`).toBeTruthy();
      const up = await api.post('/api/files/manager?action=upload', {
        multipart: {
          path: `${STORAGE}://${folder}`,
          'file[]': { name: `${folder.toLowerCase()}.txt`, mimeType: 'text/plain', buffer: Buffer.from(folder) },
        },
      });
      expect(up.ok(), `upload into ${folder}: ${up.status()}`).toBeTruthy();
    }
    const role = await api.post('/api/admin/roles', {
      data: {
        name: ROLE,
        enabled: true,
        permissions: STANDARD_WITHOUT_DELETE,
        effects: { 'files.delete': 'allow' },
        conditions: { storage_ids: [st.id], paths: ['Scratch'] },
      },
    });
    expect(role.status(), await role.text()).toBe(201);
    roleId = (await role.json()).id;
    await apiLogin(request);
    const user = await api.post('/api/admin/users', { data: { email: PERSON, password: PERSON_PW, role: 'viewer' } });
    expect(user.ok(), await user.text()).toBeTruthy();
    personId = (await user.json()).id;
    const give = await api.put(`/api/admin/users/${personId}/roles`, { data: { role_id: roleId } });
    expect(give.ok(), await give.text()).toBeTruthy();
    // The role can change files, so its people are on the User level.
    expect((await give.json()).role).toBe('user');
  });

  test.afterAll(async ({ request }) => {
    await api.delete(`/api/admin/users/${personId}`);
    await api.delete(`/api/admin/roles/${roleId}?to=viewer`);
    await dropStorageByName(request, STORAGE);
    await api.dispose();
  });

  test('Delete is offered in Scratch and not in Finance — and the server agrees', async ({ page }) => {
    await signInAsPerson(page);
    const scratch = await menuFor(page, 'Scratch', 'scratch.txt');
    expect(scratch.some((l) => /^(Delete|Sil)$/.test(l)), `Scratch menu: ${scratch.join(', ')}`).toBe(true);
    const finance = await menuFor(page, 'Finance', 'finance.txt');
    expect(finance.some((l) => /^(Delete|Sil)$/.test(l)), `Finance menu: ${finance.join(', ')}`).toBe(false);
    // Renaming is in the role's own list, so it is offered in both.
    expect(finance.some((l) => /^(Rename|Yeniden adlandır)/.test(l)), `Finance menu: ${finance.join(', ')}`).toBe(true);

    // The server decides, whatever the menu shows: the person's own session.
    const refused = await page.request.post('/api/files/manager?action=delete', {
      data: { path: `${STORAGE}://Finance`, items: [{ path: `${STORAGE}://Finance/finance.txt` }] },
    });
    expect(refused.status()).toBe(403);
    const body = await refused.json();
    expect(body.error).toBe('permission_denied');
    expect(body.permission).toBe('files.delete');
    expect(body.source?.rule_name).toBe(ROLE);
    const allowed = await page.request.post('/api/files/manager?action=delete', {
      data: { path: `${STORAGE}://Scratch`, items: [{ path: `${STORAGE}://Scratch/scratch.txt` }] },
    });
    expect(allowed.status(), await allowed.text()).toBe(200);
  });

  test('Admin → Roles is the explorer table, and Admin → Users names the role', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/roles');
    const table = page.getByTestId('roles-list');
    await expect(table).toBeVisible();
    // The one table: the shared DataTable, with its sortable headers and a
    // row whose verbs sit behind its one Actions control.
    await expect(table.locator('.fe-list')).toBeVisible();
    await expect(page.getByTestId(`role-members-rule-${roleId}`)).toContainText(/1 (member|üye)/);
    await expect(page.getByTestId(`role-actions-rule-${roleId}`)).toBeVisible();
    await expect(page.getByTestId('role-actions-builtin-admin')).toHaveCount(0);

    await page.goto('/admin/users');
    const row = page.locator('[data-fe-path], .fe-list__row').filter({ hasText: PERSON }).first();
    await expect(row).toBeVisible({ timeout: 15_000 });
    await expect(row).toContainText(ROLE);
  });
});
