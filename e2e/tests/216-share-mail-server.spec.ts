/**
 * 216 — "Send by e-mail" is written by the server, from the link (0.54).
 *
 * The share mail used to carry what the request said: the link's address, its
 * PIN, the expiry, whether the item was a folder, its size — and anybody who
 * could make links on an item could have the instance mail any of that to any
 * number of addresses. Now the request names the LINK (its token), the
 * addresses and the language; the server checks the caller manages that link,
 * writes the message from the link itself, never puts a PIN in it, sends to at
 * most 20 addresses at once, and says the outcome in the composer's language.
 *
 * The mails are read off a real SMTP conversation (helpers/smtpSink).
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { apiLogin, loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';
import { mailToSink, startSink, waitMail, type Sink } from '../helpers/smtpSink';

const STORAGE = `e2e-sharemail-${Date.now()}`;
const FILE = 'informe.txt';
// The dialog test's own file: the API tests above make links on FILE, and a
// file that already has a link opens the dialog with Link sharing ON - a click
// on the switch then takes every link away instead of making one.
const DIALOG_FILE = 'dialogo.txt';
const OTHER = `share-mail-other-${Date.now()}@example.com`;
const OTHER_PW = 'share-mail-other-pw-2026';

async function makeLink(api: APIRequestContext, data: Record<string, unknown> = {}) {
  const res = await api.post('/api/files/share', { data: { path: `${STORAGE}://${FILE}`, ...data } });
  expect(res.ok(), await res.text()).toBeTruthy();
  return (await res.json()).share as { token: string; url: string; password_pin?: string };
}

async function openShare(page: Page, file: string) {
  await page.goto('/drive/explore');
  await page.getByTestId(`sidenav-storage-${STORAGE}`).click();
  const row = page.locator('[data-fe-path]').filter({ hasText: file }).first();
  await row.click({ button: 'right' });
  await page.getByRole('menuitem', { name: /^(Share|Paylaş)\b/ }).click();
  await expect(page.locator('.fe-share')).toBeVisible();
}

test.describe.serial('share mail — the server writes it from the link', () => {
  let api: APIRequestContext;
  let other: APIRequestContext;
  let sink: Sink;
  let restoreMail: () => Promise<void> = async () => undefined;

  test.beforeAll(async ({ playwright, baseURL, request }) => {
    sink = await startSink();
    api = await newAuthedRequest(playwright, baseURL ?? '');
    restoreMail = await mailToSink(api, sink);
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
    for (const name of [FILE, DIALOG_FILE]) {
      const up = await api.post('/api/files/manager?action=upload', {
        multipart: { path: `${STORAGE}://`, 'file[]': { name, mimeType: 'text/plain', buffer: Buffer.from('hola') } },
      });
      expect(up.ok(), `upload ${name}: ${up.status()}`).toBeTruthy();
    }
    await apiLogin(request);
    await request.post('/api/admin/users', { data: { email: OTHER, password: OTHER_PW, role: 'user' } });
    other = await newAuthedRequest(playwright, baseURL ?? '', OTHER, OTHER_PW);
  });

  test.afterAll(async ({ request }) => {
    await restoreMail();
    await dropStorageByName(request, STORAGE);
    await other?.dispose();
    await api.dispose();
    await sink.close();
  });

  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  });

  test('what the request says about the link is not mailed; the link is, without its PIN', async () => {
    const link = await makeLink(api, { password: true });
    expect(link.password_pin, 'the creator is shown the PIN once').toBeTruthy();
    const before = sink.mails.length;
    const sent = await api.post('/api/files/permissions/share-mail', {
      data: {
        share: link.token, email: 'amigo@e2e.test', locale: 'en',
        url: 'https://phish.example/login', pin: '0000', expires_days: 365, is_dir: true, size: 987654321, mode: 'drop',
      },
    });
    expect(sent.ok(), await sent.text()).toBeTruthy();
    const out = await sent.json();
    expect(out.pin_withheld).toBe(true);
    expect(out.message).toContain('The PIN is not in the email');

    const { h, body } = await waitMail(sink, before + 1);
    expect(h.Subject).toBe(`${FILE} has been shared with you`);
    expect(body).toContain(`Download it here:\n${link.url}`);
    expect(body).toContain(`File: ${FILE}`);
    expect(body).toContain('This link is protected with a PIN.');
    const rest = body.split(link.token).join('');
    for (const never of ['phish.example', '0000', String(link.password_pin), '365', 'Folder:']) {
      expect(rest, never).not.toContain(never);
    }
  });

  test('a link somebody else made is not theirs to mail, in either request shape', async () => {
    const link = await makeLink(api);
    const before = sink.mails.length;
    const refused = await other.post('/api/files/permissions/share-mail', {
      data: { share: link.token, email: 'disari@e2e.test' },
    });
    expect(refused.status(), await refused.text()).toBe(403);
    const message = await other.get(`/api/files/permissions/share-message?share=${encodeURIComponent(link.token)}`);
    expect(message.status()).toBe(403);
    const oldShape = await other.post('/api/files/permissions/share-mail', {
      data: { path: `${STORAGE}://${FILE}`, email: 'disari@e2e.test', url: 'https://phish.example/login' },
    });
    expect(oldShape.status(), await oldShape.text()).toBe(400);
    expect(sink.mails.length).toBe(before);
  });

  test('one send reaches at most twenty addresses — an over-long list reaches nobody', async () => {
    const link = await makeLink(api);
    const before = sink.mails.length;
    const emails = Array.from({ length: 21 }, (_, i) => `kisi${i}@e2e.test`);
    const res = await api.post('/api/files/permissions/share-mail', { data: { share: link.token, emails, locale: 'en' } });
    expect(res.status()).toBe(400);
    const out = await res.json();
    expect(out.error).toBe('too_many_recipients');
    expect(out.message).toBe('Send to at most 20 addresses at a time.');
    expect(sink.mails.length).toBe(before);
  });

  test('the share sheet gets the mail words from the server', async () => {
    const link = await makeLink(api, { password: true });
    const res = await api.get(`/api/files/permissions/share-message?share=${encodeURIComponent(link.token)}&lang=en`);
    expect(res.ok(), await res.text()).toBeTruthy();
    const out = await res.json();
    expect(out.subject).toBe(`${FILE} has been shared with you`);
    expect(out.body).toContain(link.url);
    expect(out.body).toContain('This link is protected with a PIN.');
    expect(link.password_pin).toBeTruthy();
    expect(out.body.split(link.token).join('')).not.toContain(String(link.password_pin));
  });

  test('the dialog sends the link and the addresses, and shows what the server says', async ({ page }) => {
    await loginAs(page);
    await openShare(page, DIALOG_FILE);
    await expect(page.getByTestId('share-switch'), 'a file with no link yet').toHaveAttribute('aria-checked', 'false');
    await page.getByTestId('share-switch').click();
    await expect(page.getByTestId('share-switch')).toHaveAttribute('aria-checked', 'true');
    await page.getByTestId('share-options-toggle').click();

    const before = sink.mails.length;
    const box = page.getByTestId('share-mail-row').locator('input');
    await box.fill('alici@e2e.test');
    const [req] = await Promise.all([
      page.waitForRequest((r) => r.url().includes('/api/files/permissions/share-mail') && r.method() === 'POST'),
      box.press('Enter'),
    ]);
    const sentBody = JSON.parse(req.postData() ?? '{}') as Record<string, unknown>;
    // The link's token and the addresses - and no language: nobody picked a
    // Recipient's language, so the server writes the mail in its own
    // (#191 follow-up: `locale` travels only when somebody picked one).
    expect(Object.keys(sentBody).sort()).toEqual(['emails', 'share']);
    expect(sentBody.emails).toEqual(['alici@e2e.test']);

    await expect(page.locator('.fe-share__notice').filter({ hasText: /Email sent to 1 person|E-posta 1 kişiye gönderildi/ })).toBeVisible();
    const { body } = await waitMail(sink, before + 1);
    expect(body).toContain(`/s/${String(sentBody.share)}`);
  });
});
