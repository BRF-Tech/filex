/**
 * 156-driver-time-limits — issue #73, in a real browser against a real binary.
 *
 * WebDAV cut every transfer at 60 s, moving or not; FTP waited forever on a
 * server that stopped answering, holding the whole storage. Both drivers now
 * have the three time settings S3 got in #44 (153-s3-dead-store), declared in
 * their descriptors, so the storage form draws them with no form code of its
 * own.
 *
 * What is measured:
 *   · the settings are on the WebDAV and the FTP form under "Advanced
 *     settings", with each driver's defaults (WebDAV 30 s / 3 / 15 s, FTP
 *     15 s / 3 / 15 s) and the shared bounds; WebDAV's help names the 10-minute
 *     wait for copies and moves, FTP's does not (it has no such request);
 *   · they REACH the driver: "Test connection" against a server that accepts
 *     the connection and never greets answers in the driver's words ("ftp
 *     server … is unavailable: the server sent nothing for 1s") within the 1 s
 *     and 3 s typed into the form. With the defaults it would wait 15 s and run
 *     into the probe's own 10 s limit; before #73 it never answered at all;
 *   · a Turkish admin reads them in Turkish, with no English left on the form.
 */
import net from 'node:net';

import { test, expect, type Page } from '@playwright/test';
import { apiLogin, loginAs } from '../helpers/auth';

const STAMP = Date.now();
const TR_ADMIN = `time-limits-${STAMP}@example.com`;
const TR_PW = 'Time-limits-2026!';

/** A server that takes the connection and never says a word: a hung FTP daemon. */
async function silentServer(): Promise<{ port: number; close: () => Promise<void> }> {
  const held: net.Socket[] = [];
  const srv = net.createServer((sock) => {
    held.push(sock);
  });
  await new Promise<void>((resolve) => srv.listen(0, '127.0.0.1', resolve));
  const { port } = srv.address() as net.AddressInfo;
  return {
    port,
    close: async () => {
      for (const s of held) s.destroy();
      await new Promise<void>((resolve) => srv.close(() => resolve()));
    },
  };
}

async function openForm(page: Page, driver: string, driverLabel: string, advanced: string) {
  await page.goto('/admin/storages/new');
  await page.getByLabel(driverLabel, { exact: true }).selectOption(driver);
  await page.getByRole('button', { name: new RegExp(advanced) }).click();
}

async function expectSettings(page: Page, attempt: string) {
  const expected: Array<[string, string, string, string]> = [
    ['Attempt timeout (seconds)', attempt, '1', '600'],
    ['Attempts per request', '3', '1', '20'],
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
  await expect(page.getByText('and neither is an upload that has started sending.')).toBeVisible();
}

test.describe('driver time limits (#73)', () => {
  test.beforeAll(async ({ request }) => {
    await apiLogin(request);
    const made = await request.post('/api/admin/users', {
      data: { email: TR_ADMIN, password: TR_PW, role: 'admin', locale: 'tr', display_name: 'Süre Denetçisi' },
    });
    expect(made.ok(), await made.text()).toBe(true);
  });

  test('the WebDAV form offers the time settings, with its defaults and the server-work wait', async ({ page }) => {
    await loginAs(page);
    await openForm(page, 'webdav', 'Driver', 'Advanced settings');
    await expectSettings(page, '30');
    await expect(page.getByText('Copies, moves and deletes wait up to 10 minutes for the answer')).toBeVisible();
  });

  test('the FTP form offers them too, with its own defaults and no server-work wait', async ({ page }) => {
    await loginAs(page);
    await openForm(page, 'ftp', 'Driver', 'Advanced settings');
    await expectSettings(page, '15');
    await expect(page.getByText('Copies, moves and deletes wait up to 10 minutes')).toHaveCount(0);
  });

  test('the settings reach the FTP driver: a hung server is reported in its words, within the budget typed in', async ({ page }) => {
    const silent = await silentServer();
    const port = silent.port;
    try {
      await loginAs(page);
      await openForm(page, 'ftp', 'Driver', 'Advanced settings');

      await page.getByRole('textbox', { name: 'Host', exact: true }).fill('127.0.0.1');
      await page.getByLabel('Port', { exact: true }).fill(String(port));
      await page.getByRole('textbox', { name: 'User', exact: true }).fill('u');
      await page.getByRole('textbox', { name: 'Password', exact: true }).fill('p');
      await page.getByRole('textbox', { name: 'Base path', exact: true }).fill('/files');
      await page.getByLabel('Attempt timeout (seconds)', { exact: true }).fill('1');
      await page.getByLabel('Give up after (seconds)', { exact: true }).fill('3');

      const answered = page.waitForResponse((r) => r.url().includes('/api/admin/storages/test'));
      const started = Date.now();
      await page.getByRole('button', { name: 'Test connection' }).click();
      const res = await answered;
      const took = Date.now() - started;
      const body = (await res.json()) as { ok?: boolean; error?: string };

      expect(body.ok, JSON.stringify(body)).toBe(false);
      expect(body.error).toContain(`ftp server 127.0.0.1:${port} is unavailable: the server sent nothing for 1s`);
      expect(body.error, 'the probe ran into its own limit: the settings did not reach the driver').not.toContain('timed out after');
      expect(took, `answered after ${took} ms`).toBeLessThan(6000);
      await expect(page.getByText('is unavailable: the server sent nothing for 1s')).toBeVisible();
    } finally {
      await silent.close();
    }
  });

  test('a Turkish admin reads them in Turkish', async ({ page }) => {
    await loginAs(page, TR_ADMIN, TR_PW);
    for (const driver of ['webdav', 'ftp']) {
      await openForm(page, driver, 'Sürücü', 'Gelişmiş ayarlar');
      for (const label of ['Deneme zaman aşımı (saniye)', 'İstek başına deneme sayısı', 'Vazgeçme süresi (saniye)']) {
        await expect(page.getByLabel(label, { exact: true }), `${driver}: ${label}`).toBeVisible();
      }
      await expect(page.getByText('İlerleyen bir aktarım, süresi ne olursa olsun hiçbir zaman kesilmez')).toBeVisible();
      await expect(page.getByText('gönderilmeye başlamış bir yükleme de yeniden denenmez')).toBeVisible();
      if (driver === 'webdav') {
        await expect(page.getByText('Kopyalama, taşıma ve silme cevap için 10 dakikaya kadar bekler')).toBeVisible();
      }
      const main = await page.locator('main').innerText();
      for (const english of ['Attempt timeout', 'Attempts per request', 'Give up after', 'never cut']) {
        expect(main, `${driver}: English on the Turkish form: ${english}`).not.toContain(english);
      }
    }
  });
});
