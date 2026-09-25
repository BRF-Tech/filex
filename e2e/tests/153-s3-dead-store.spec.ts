/**
 * 153-s3-dead-store — issue #44, in a real browser against a real binary.
 *
 * A drop into an S3 folder while the object store was down took 85.6 s to say
 * so. The S3 driver now has three settings for it — attempt timeout, attempts
 * per request, give up after — declared in its descriptor, so the storage form
 * draws them with no form code of its own.
 *
 * What is measured:
 *   · the three settings are on the S3 form under "Advanced settings", with the
 *     driver's defaults (10 s / 6 / 15 s), its bounds, and the help that says a
 *     moving transfer is never cut — and in Turkish for a Turkish admin;
 *   · they REACH the driver: "Test connection" against a port nothing listens on
 *     answers in the driver's words ("is unavailable: the connection was
 *     refused") within the 3 s typed into the form. Without them the probe
 *     would run into its own 10 s limit (the SDK's schedule took 26 s).
 */
import net from 'node:net';

import { test, expect, type Page } from '@playwright/test';
import { apiLogin, loginAs } from '../helpers/auth';

const STAMP = Date.now();
const TR_ADMIN = `dead-store-${STAMP}@example.com`;
const TR_PW = 'Dead-store-2026!';

async function deadPort(): Promise<number> {
  const srv = net.createServer();
  await new Promise<void>((resolve) => srv.listen(0, '127.0.0.1', resolve));
  const { port } = srv.address() as net.AddressInfo;
  await new Promise<void>((resolve) => srv.close(() => resolve()));
  return port;
}

async function openS3Form(page: Page, driverLabel: string, advanced: string) {
  await page.goto('/admin/storages/new');
  await page.getByLabel(driverLabel, { exact: true }).selectOption('s3');
  await page.getByRole('button', { name: new RegExp(advanced) }).click();
}

test.beforeAll(async ({ request }) => {
  await apiLogin(request);
  const made = await request.post('/api/admin/users', {
    data: { email: TR_ADMIN, password: TR_PW, role: 'admin', locale: 'tr', display_name: 'Depo Denetçisi' },
  });
  expect(made.ok(), await made.text()).toBe(true);
});

test('the S3 form offers the dead-store settings, with the driver’s defaults and bounds', async ({ page }) => {
  await loginAs(page);
  await openS3Form(page, 'Driver', 'Advanced settings');

  const expected: Array<[string, string, string, string]> = [
    ['Attempt timeout (seconds)', '10', '1', '600'],
    ['Attempts per request', '6', '1', '20'],
    ['Give up after (seconds)', '15', '1', '3600'],
  ];
  for (const [label, value, min, max] of expected) {
    const box = page.getByLabel(label, { exact: true });
    await expect(box, label).toBeVisible();
    await expect(box, label).toHaveAttribute('type', 'number');
    await expect(box, label).toHaveValue(value);
    await expect(box, label).toHaveAttribute('min', min);
    await expect(box, label).toHaveAttribute('max', max);
  }
  await expect(page.getByText('A transfer that keeps moving is never cut, however long it takes')).toBeVisible();
  await expect(page.getByText('A refusal (403, a missing bucket, a host name that does not resolve) is never retried.')).toBeVisible();
});

test('the settings reach the driver: a dead endpoint is reported in its words, within the budget typed in', async ({ page }) => {
  const port = await deadPort();
  await loginAs(page);
  await openS3Form(page, 'Driver', 'Advanced settings');

  await page.getByRole('textbox', { name: 'Bucket', exact: true }).fill('b');
  await page.getByRole('textbox', { name: 'Prefix', exact: true }).fill('fx');
  await page.getByRole('textbox', { name: 'Endpoint', exact: true }).fill(`http://127.0.0.1:${port}`);
  await page.getByRole('textbox', { name: 'Access key', exact: true }).fill('fake');
  await page.getByRole('textbox', { name: 'Secret key', exact: true }).fill('fake');
  await page.getByLabel('Attempt timeout (seconds)', { exact: true }).fill('1');
  await page.getByLabel('Give up after (seconds)', { exact: true }).fill('3');

  const answered = page.waitForResponse((r) => r.url().includes('/api/admin/storages/test'));
  const started = Date.now();
  await page.getByRole('button', { name: 'Test connection' }).click();
  const res = await answered;
  const took = Date.now() - started;
  const body = (await res.json()) as { ok?: boolean; error?: string };

  expect(body.ok, JSON.stringify(body)).toBe(false);
  expect(body.error).toContain('is unavailable: the connection was refused');
  expect(body.error, 'the probe ran into its own limit: the settings did not reach the driver').not.toContain('timed out after');
  expect(took, `answered after ${took} ms`).toBeLessThan(6000);
  await expect(page.getByText('is unavailable: the connection was refused')).toBeVisible();
});

test('a Turkish admin reads the settings in Turkish', async ({ page }) => {
  await loginAs(page, TR_ADMIN, TR_PW);
  await openS3Form(page, 'Sürücü', 'Gelişmiş ayarlar');

  for (const label of ['Deneme zaman aşımı (saniye)', 'İstek başına deneme sayısı', 'Vazgeçme süresi (saniye)']) {
    await expect(page.getByLabel(label, { exact: true }), label).toBeVisible();
  }
  await expect(page.getByText('İlerleyen bir aktarım, süresi ne olursa olsun hiçbir zaman kesilmez')).toBeVisible();
  const main = await page.locator('main').innerText();
  for (const english of ['Attempt timeout', 'Attempts per request', 'Give up after', 'never cut']) {
    expect(main, `English on the Turkish form: ${english}`).not.toContain(english);
  }
});
