/**
 * 197 - who may encrypt, under the `approval` policy, one journey end to end
 * (docs/E2E-ENCRYPTION.md → Who may encrypt → Approvals).
 *
 * A person who may encrypt but needs an administrator's approval:
 *   1. the New folder dialog offers "Request an encrypted folder…" instead of
 *      "Create encrypted folder…"; the request is sent from the dialog;
 *   2. an administrator approves it; the dialog now offers to create one, and
 *      the server lets ONE new encrypted folder be made there, once;
 *   3. that approval is a new folder's: a folder there that holds something
 *      still offers only "Request encryption…" (the kinds are separate,
 *      operator decision 2026-10-03), and the server agrees;
 *   4. with the policy `off` nothing is offered at all.
 *
 * The instance policy is read first and put back afterwards: the suite shares
 * one server.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { apiLogin, dismissInstallBanner } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';

const RUN = Date.now();
const STORAGE = `e2e-policy-${RUN}`;
const PERSON = `policy-person-${RUN}@example.com`;
const PERSON_PW = 'policy-person-pw-2026';
const MARKER = '.filex-e2e.json';

let api: APIRequestContext;
let personId = 0;
let policyBefore = 'permitted';

async function setPolicy(policy: string) {
  const res = await api.patch('/api/admin/e2e', { data: { policy } });
  expect(res.ok(), `policy ${policy}: ${res.status()} ${await res.text()}`).toBeTruthy();
}

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

async function openFolder(page: Page, folder: string) {
  await page.goto(`/drive/explore#${encodeURIComponent(STORAGE)}/${folder}`);
  await page.waitForLoadState('networkidle');
}

/** The New folder dialog of the folder on screen, open. */
async function newFolderDialog(page: Page) {
  await page.getByTestId('sidenav-new').first().click();
  await page
    .locator('.fe-ctx__item', { has: page.locator('.fe-ctx__label', { hasText: /^(New folder|Yeni klasör)$/ }) })
    .first()
    .click();
  const dialog = page.locator('.fe-modal__card').filter({ hasText: /New folder|Yeni klasör/ }).last();
  await expect(dialog).toBeVisible();
  // The encryption answer arrives from the server a moment after the dialog
  // opens (POST /api/files/e2e/allowed): wait for it.
  await page.waitForLoadState('networkidle');
  return dialog;
}

/** The labels of the right-click menu on the folder `name` in the folder on
 *  screen. The menu asks the server for the encryption answer when it first
 *  opens on a row (POST /api/files/e2e/allowed) and shows "denied" until it
 *  arrives, so the menu is opened once to ask, and read the second time. */
async function menuFor(page: Page, name: string): Promise<string[]> {
  const row = page.locator('[data-fe-path]').filter({ hasText: name }).first();
  await expect(row).toBeVisible({ timeout: 15_000 });
  const asked = page.waitForResponse((r) => r.url().includes('/api/files/e2e/allowed'), { timeout: 10_000 }).catch(() => null);
  await row.click({ button: 'right' });
  await expect(page.locator('.fe-ctx').last()).toBeVisible();
  await asked;
  await page.waitForLoadState('networkidle');
  await page.keyboard.press('Escape');
  await row.click({ button: 'right' });
  const menu = page.locator('.fe-ctx').last();
  await expect(menu).toBeVisible();
  const labels = (await menu.locator('.fe-ctx__item:visible .fe-ctx__label').allTextContents()).map((s) => s.trim());
  await page.keyboard.press('Escape');
  return labels;
}

const CREATE = /Create encrypted folder…|Şifreli klasör oluştur…/;
const REQUEST_NEW = /Request an encrypted folder…|Şifreli klasör iste…/;
const ENCRYPT = /^(Encrypt with E2EE…|E2EE ile şifrele…)$/;
const REQUEST = /^(Request encryption…|Şifreleme iste…)$/;

test.describe('who may encrypt: the approval policy', () => {
  test.beforeAll(async ({ playwright, baseURL, request }) => {
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const before = await api.get('/api/admin/e2e');
    expect(before.ok(), await before.text()).toBeTruthy();
    policyBefore = (await before.json()).policy ?? 'permitted';
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
    for (const folder of ['Proje', 'Ekip']) {
      const mk = await api.post('/api/files/manager?action=newfolder', { data: { path: `${STORAGE}://`, name: folder } });
      expect(mk.ok(), `newfolder ${folder}: ${mk.status()}`).toBeTruthy();
    }
    const up = await api.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://Ekip`,
        'file[]': { name: 'bordro.txt', mimeType: 'text/plain', buffer: Buffer.from('maaşlar') },
      },
    });
    expect(up.ok(), `upload: ${up.status()}`).toBeTruthy();
    await apiLogin(request);
    const user = await api.post('/api/admin/users', { data: { email: PERSON, password: PERSON_PW, role: 'user' } });
    expect(user.ok(), await user.text()).toBeTruthy();
    personId = (await user.json()).id;
    await setPolicy('approval');
  });

  test.afterAll(async ({ request }) => {
    await setPolicy(policyBefore);
    await api.delete(`/api/admin/users/${personId}`);
    await dropStorageByName(request, STORAGE);
    await api.dispose();
  });

  test('a new encrypted folder: requested in the dialog, approved, spent once', async ({ page }) => {
    await signInAsPerson(page);
    await openFolder(page, 'Proje');

    // 1. Only the request is offered.
    let dialog = await newFolderDialog(page);
    await expect(dialog.getByText(CREATE)).toHaveCount(0);
    await dialog.getByTestId('e2e-request-option').click();
    const ask = page.locator('.fe-modal__card').filter({ has: page.getByTestId('e2e-request') });
    await expect(ask).toBeVisible();
    await expect(ask.getByTestId('e2e-request-lead')).toContainText(/one new encrypted folder|yeni bir şifreli klasör/);
    await ask.getByTestId('e2e-request-reason').fill('Sözleşmeler, yalnız hukuk görsün');
    await ask.getByTestId('e2e-request-send').click();
    await expect(ask).toBeHidden();

    const mine = await page.request.get('/api/files/e2e/requests');
    expect(mine.ok()).toBeTruthy();
    const requests = (await mine.json()).requests as Array<{ id: number; path: string; kind: string; status: string }>;
    expect(requests).toHaveLength(1);
    expect(requests[0]).toMatchObject({ path: `${STORAGE}://Proje`, kind: 'new_folder', status: 'pending' });

    // 2. An administrator approves it: the dialog offers to create one now.
    const approve = await api.post(`/api/admin/e2e/requests/${requests[0].id}/approve`, { data: { note: 'Tamam' } });
    expect(approve.ok(), await approve.text()).toBeTruthy();
    // The server's answer for this kind, as the explorer asks it.
    const asked = await page.request.post('/api/files/e2e/allowed', {
      data: { items: [{ path: `${STORAGE}://Proje`, kind: 'new_folder' }, { path: `${STORAGE}://Proje`, kind: 'folder' }] },
    });
    expect(asked.ok()).toBeTruthy();
    expect((await asked.json()).encrypt).toEqual(['allowed', 'request']);
    // The explorer remembers an answer until the listing is read again.
    await page.reload();
    await page.waitForLoadState('networkidle');
    dialog = await newFolderDialog(page);
    await expect(dialog.getByText(CREATE)).toBeVisible();
    await expect(dialog.getByText(REQUEST_NEW)).toHaveCount(0);
    await page.keyboard.press('Escape');

    // ... and the server lets exactly one new encrypted folder be made there.
    for (const name of ['Yeni', 'Yeni2']) {
      const mk = await page.request.post('/api/files/manager?action=newfolder', { data: { path: `${STORAGE}://Proje`, name } });
      expect(mk.ok(), `newfolder ${name}: ${mk.status()}`).toBeTruthy();
    }
    const keyFile = (dir: string) =>
      page.request.post('/api/files/manager?action=upload', {
        multipart: {
          path: `${STORAGE}://Proje/${dir}`,
          'file[]': { name: MARKER, mimeType: 'application/json', buffer: Buffer.from('{"v":2}') },
        },
      });
    const first = await keyFile('Yeni');
    expect(first.status(), await first.text()).toBe(200);
    const second = await keyFile('Yeni2');
    expect(second.status()).toBe(403);
    expect(await second.json()).toMatchObject({ error: 'e2e_not_allowed', reason: 'approval_required' });
    const after = await page.request.get('/api/files/e2e/requests');
    expect(((await after.json()).requests as Array<{ status: string }>)[0].status).toBe('used');
  });

  test('a folder that holds something is encrypted in place: its own kind, its own request', async ({ page }) => {
    const ok = await api.post('/api/files/e2e/requests', {
      data: { path: `${STORAGE}://`, kind: 'new_folder', reason: 'kök' },
    });
    // The administrator needs no approval: not requestable for them.
    expect(ok.status()).toBe(400);

    await signInAsPerson(page);
    await openFolder(page, '');
    const labels = await menuFor(page, 'Ekip');
    expect(labels.some((l) => REQUEST.test(l)), `menu: ${labels.join(', ')}`).toBe(true);
    expect(labels.some((l) => ENCRYPT.test(l)), `menu: ${labels.join(', ')}`).toBe(false);
    const refused = await page.request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://Ekip`,
        'file[]': { name: MARKER, mimeType: 'application/json', buffer: Buffer.from('{"v":2}') },
      },
    });
    expect(refused.status()).toBe(403);
    expect(await refused.json()).toMatchObject({ error: 'e2e_not_allowed', reason: 'approval_required' });
  });

  test('with the policy off nothing is offered: not to encrypt, not to request', async ({ page }) => {
    await setPolicy('off');
    try {
      await signInAsPerson(page);
      await openFolder(page, 'Proje');
      const dialog = await newFolderDialog(page);
      await expect(dialog.getByText(CREATE)).toHaveCount(0);
      await expect(dialog.getByText(REQUEST_NEW)).toHaveCount(0);
      await page.keyboard.press('Escape');
      await openFolder(page, '');
      const labels = await menuFor(page, 'Ekip');
      expect(labels.some((l) => REQUEST.test(l) || ENCRYPT.test(l)), `menu: ${labels.join(', ')}`).toBe(false);
    } finally {
      await setPolicy('approval');
    }
  });
});
