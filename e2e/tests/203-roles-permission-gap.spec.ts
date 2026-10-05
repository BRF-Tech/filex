/**
 * 203 - a role that may have lost a permission (PR #86, Berk Başarır).
 *
 * A save on 0.50 or older takes files.encrypt away without a word: the
 * built-in User role's page writes its list back without the key it does not
 * know, and the role editor keeps only the folder permissions it knows. 0.51
 * merged the key once and does not again, and nothing stored tells such a
 * list from one an administrator chose. So Admin → Roles points each one out,
 * with one click to give the permission back and one to say it was on
 * purpose - nothing is given back by itself.
 *
 * The journey makes the two lists the way a client that never showed Encrypt
 * does (the API, without `shown` - exactly what an older page is), then
 * answers them on the page as an administrator.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { newAuthedRequest } from '../helpers/seed';

const RUN = Date.now();
const ROLE = `Drop box ${RUN}`;

let api: APIRequestContext;
let roleId = 0;
let original: string[] = [];

test.describe('roles: a permission an older version dropped', () => {
  test.beforeAll(async ({ playwright, baseURL }) => {
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const got = await api.get('/api/admin/roles/builtin');
    expect(got.ok(), await got.text()).toBeTruthy();
    original = (await got.json()).permissions;
    expect(original).toContain('files.encrypt');

    // The User role as 0.50's page writes it back.
    const put = await api.put('/api/admin/roles/builtin', {
      data: { permissions: original.filter((k) => k !== 'files.encrypt') },
    });
    expect(put.ok(), await put.text()).toBeTruthy();
    // A folder part as 0.50's editor leaves it.
    const role = await api.post('/api/admin/roles', {
      data: {
        name: ROLE,
        enabled: true,
        permissions: ['files.download'],
        effects: { 'files.create': 'allow' },
        conditions: { paths: ['Drop'] },
      },
    });
    expect(role.status(), await role.text()).toBe(201);
    roleId = (await role.json()).id;
  });

  test.afterAll(async () => {
    await api.put('/api/admin/roles/builtin', { data: { permissions: original } });
    await api.delete(`/api/admin/roles/${roleId}`);
    await api.dispose();
  });

  test('Admin → Roles points them out; one is given back, one dismissed', async ({ page }) => {
    const userGap = 'builtin:user:files.encrypt';
    const ruleGap = `role:${roleId}:files.encrypt`;

    await loginAs(page);
    await page.goto('/admin/roles');
    const box = page.getByTestId('role-gaps');
    await expect(box).toBeVisible();
    await expect(box).toContainText(/2 roles may have lost a permission|2 rolde bir izin düşmüş olabilir/);
    await expect(box).toContainText(/0\.50/);
    await expect(page.getByTestId(`role-gap-${userGap}`)).toContainText(/Encrypt|Şifreleme/);
    await expect(page.getByTestId(`role-gap-${ruleGap}`)).toContainText(ROLE);

    // One click gives the User role Encrypt back - and nothing else changes.
    await page.getByTestId(`role-gap-restore-${userGap}`).click();
    await expect(page.getByTestId(`role-gap-${userGap}`)).toHaveCount(0);
    const now = (await (await api.get('/api/admin/roles/builtin')).json()).permissions as string[];
    expect([...now].sort()).toEqual([...original].sort());

    // The drop box was meant that way.
    await page.getByTestId(`role-gap-dismiss-${ruleGap}`).click();
    await expect(box).toHaveCount(0);
    const role = await (await api.get('/api/admin/roles')).json();
    const saved = role.rules.find((r: { id: number }) => r.id === roleId);
    expect(saved.effects).toEqual({ 'files.create': 'allow' });

    // Dismissed stays dismissed.
    await page.reload();
    await expect(page.getByTestId('roles-list')).toBeVisible();
    await expect(page.getByTestId('role-gaps')).toHaveCount(0);

    // Both answers are in the audit log.
    for (const action of ['permission_gap.restore', 'permission_gap.dismiss']) {
      const res = await api.get(`/api/admin/audit?action=${action}&limit=5`);
      expect(res.ok(), await res.text()).toBeTruthy();
      expect(JSON.stringify(await res.json()), action).toContain(action);
    }
  });
});
