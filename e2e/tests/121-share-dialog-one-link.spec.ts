/**
 * 121 — the share dialog: one of everything, and nothing it cannot deliver.
 *
 * QA sweep, 2026-09-21:
 *   #33  "Create link" after the switch had already made a link minted a
 *        SECOND live link (the old one without the new PIN); the header's link
 *        was listed a second time underneath; three kinds of Copy button; the
 *        PIN a native checkbox under a switch.
 *   #21  the menu promised "Share / permissions" to people who cannot get
 *        permissions; for them the dialog asked for the grant list anyway and
 *        swallowed the 403; for an administrator on a storage with RBAC off the
 *        add-people form WORKED under a box saying grants do not apply.
 *
 * Each assertion that can be is checked against the SERVER (how many live
 * links exist), not only against the dialog — a dialog can look right over a
 * second link it quietly left behind.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { apiLogin, dismissInstallBanner, loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';

const STORAGE = `e2e-share1-${Date.now()}`;
const FILE = 'teklif.txt';
const EDITOR = `share-editor-${Date.now()}@example.com`;
const EDITOR_PW = 'share-editor-pw-2026';

async function liveLinks(api: APIRequestContext): Promise<string[]> {
  const res = await api.get(`/api/files/share?path=${encodeURIComponent(`${STORAGE}://${FILE}`)}`);
  const body = (await res.json()) as { shares?: Array<{ url: string; kind?: string }> };
  return (body.shares ?? []).filter((s) => s.kind !== 'drop').map((s) => s.url);
}

async function openShare(page: Page) {
  await page.goto('/drive/explore');
  await page.getByTestId(`sidenav-storage-${STORAGE}`).click();
  const row = page.locator('[data-fe-path]').filter({ hasText: FILE }).first();
  await row.click({ button: 'right' });
  const entry = page.getByRole('menuitem', { name: /^(Share|Paylaş)\b/ });
  // #21 — the verb is "Share": no promise of permissions in the menu.
  await expect(entry.locator('.fe-ctx__label')).toHaveText(/^(Share|Paylaş)$/);
  await entry.click();
  await expect(page.locator('.fe-share')).toBeVisible();
}

test.describe('share dialog', () => {
  let api: APIRequestContext;

  test.beforeAll(async ({ playwright, baseURL, request }) => {
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
    const up = await api.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://`, 'file[]': { name: FILE, mimeType: 'text/plain', buffer: Buffer.from('teklif') } },
    });
    expect(up.ok(), `upload ${up.status()}`).toBeTruthy();
    await apiLogin(request);
    await request.post('/api/admin/users', { data: { email: EDITOR, password: EDITOR_PW, role: 'user' } });
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await api.dispose();
  });

  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  });

  test('the switch makes THE link; changing its options replaces it — never a second live link', async ({ page }) => {
    await loginAs(page);
    await openShare(page);
    await page.getByTestId('share-switch').click();
    await expect(page.getByTestId('share-switch')).toHaveAttribute('aria-checked', 'true');
    await expect.poll(() => liveLinks(api)).toHaveLength(1);
    const first = (await liveLinks(api))[0];

    await page.getByTestId('share-options-toggle').click();
    // One on/off control in this dialog: the PIN is a switch, not a checkbox.
    await expect(page.locator('.fe-share input[type="checkbox"]')).toHaveCount(0);
    await page.getByTestId('share-pin-switch').click();
    await expect(page.getByTestId('share-create')).toHaveText(/Replace the link|Bağlantıyı bu ayarlarla yenile/);
    await page.getByTestId('share-create').click();

    await expect.poll(() => liveLinks(api)).toHaveLength(1);
    const now = (await liveLinks(api))[0];
    expect(now).not.toBe(first);
    // The header's link is not listed again underneath.
    await expect(page.getByTestId('share-other-links')).toHaveCount(0);
    await expect(page.locator('.fe-share').getByText(now, { exact: true })).toHaveCount(1);
    // Every Copy is the same control.
    const copyClasses = await page
      .locator('.fe-share button')
      .filter({ hasText: /^(Copy|Kopyala)$/ })
      .evaluateAll((els) => els.map((e) => e.className));
    expect(copyClasses.length).toBeGreaterThanOrEqual(2);
    expect(new Set(copyClasses)).toEqual(new Set(['fe-share__copy']));
  });

  test('RBAC off, administrator: the add-people form is greyed and says where to switch it on', async ({ page }) => {
    await loginAs(page);
    await openShare(page);
    await page.getByTestId('share-people-toggle').click();
    await expect(page.getByTestId('share-rbac-off')).toContainText(/Storages|Depolar/);
    await expect(page.getByTestId('share-rbac-off')).not.toContainText(/disk/i);
    await expect(page.getByTestId('share-add-person').locator('input')).toBeDisabled();
  });

  test('an editor is offered the link and nothing about permissions — and nothing is asked on their behalf', async ({ page }) => {
    const permissionCalls: string[] = [];
    page.on('request', (r) => {
      if (r.url().includes('/api/files/permissions')) permissionCalls.push(r.url());
    });
    await dismissInstallBanner(page);
    await page.goto('/admin/login');
    await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(EDITOR);
    await page.getByLabel(/password|şifre/i).fill(EDITOR_PW);
    await page
      .getByRole('button', { name: 'Sign in', exact: true })
      .or(page.getByRole('button', { name: 'Giriş yap', exact: true }))
      .first()
      .click();
    await page.waitForURL(/\/drive\//, { timeout: 15_000 });
    await openShare(page);
    await expect(page.getByTestId('share-switch')).toBeVisible();
    await expect(page.getByTestId('share-people')).toHaveCount(0);
    expect(permissionCalls).toEqual([]);
  });
});
