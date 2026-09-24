/**
 * 116-admin-says-which — the admin panel says WHICH thing, in words, and
 * counts what its labels say (v0.43.0 release-candidate sweep, 2026-09-21/22).
 *
 * Every assertion is measured in a real browser, in a Turkish admin session of
 * its own (a second administrator created here, so the shared admin's language
 * is never flipped under another spec):
 *
 *   1. Panel — "Queue depth" is the job queue's pending + running (it was the
 *      number of storage watchers); a storage card names its driver and
 *      "Salt okunur" in the one StorageTags vocabulary (the admin list said
 *      "RO"/"local", Connections "SALT OKUNUR"/"LOCAL"); a sync state reads
 *      "Tamam", not "ok"; Recent activity names the created user.
 *   2. Audit — the IP has no port, the resource filter works by NAME.
 *   3. ONLYOFFICE with an address and no JWT secret — a red warning on the
 *      Panel and External services that cannot be dismissed, gone once a
 *      secret is set.
 *   4. Corporate identity — the live preview is the public page's own shell.
 *   5. File history — a file is found by name; no node id is asked for or
 *      shown.
 *   6. Forms — an empty required box is refused under the box in Turkish,
 *      never with the browser's own (English) bubble; nothing is sent.
 *   7. Words — pages that printed wire values or English do not any more.
 */
import { test, expect, type Page, type APIRequestContext } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { apiLogin, dismissInstallBanner } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName, storageRoot } from '../helpers/seed';

const STAMP = Date.now();
const ADMIN = `says-which-${STAMP}@example.com`;
const ADMIN_PW = 'Says-which-2026!';
const RO = `e2e-sw-ro-${STAMP}`;
const RW = `e2e-sw-rw-${STAMP}`;
const FILE = `sozlesme-${STAMP}.txt`;

async function signIn(page: Page) {
  await dismissInstallBanner(page);
  await page.addInitScript(() => {
    try {
      localStorage.setItem('filex.tourDone', '1');
    } catch {
      /* private window */
    }
  });
  await page.goto('/admin/login');
  await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(ADMIN);
  await page.getByLabel(/password|şifre/i).first().fill(ADMIN_PW);
  await page
    .getByRole('button', { name: 'Sign in', exact: true })
    .or(page.getByRole('button', { name: 'Giriş yap', exact: true }))
    .first()
    .click();
  await page.waitForURL(/\/admin\/(home|dashboard)([?#]|$)/);
}

async function onlyoffice(request: APIRequestContext, body: Record<string, unknown>) {
  const res = await request.patch('/api/admin/external/onlyoffice', { data: body });
  expect(res.ok(), await res.text()).toBe(true);
}

test.describe('the admin panel says which thing, in words', () => {
  test.beforeAll(async ({ request }) => {
    await apiLogin(request);
    const made = await request.post('/api/admin/users', {
      data: { email: ADMIN, password: ADMIN_PW, role: 'admin', locale: 'tr', display_name: 'Denetçi' },
    });
    expect(made.ok(), await made.text()).toBe(true);
    await dropStorageByName(request, RO);
    await dropStorageByName(request, RW);
    await seedLocalStorage(request, RO, `/tmp/filex-${RO}`, { read_only: true });
    const rw = await seedLocalStorage(request, RW, `/tmp/filex-${RW}`);
    const root = storageRoot(`/tmp/filex-${RW}`);
    fs.mkdirSync(path.join(root, 'belgeler'), { recursive: true });
    fs.writeFileSync(path.join(root, 'belgeler', FILE), 'imza bekliyor\n');
    const sync = await request.post(`/api/admin/storages/${rw.id}/sync`);
    expect(sync.status()).toBeLessThan(300);
  });

  test.afterAll(async ({ request }) => {
    await apiLogin(request);
    await dropStorageByName(request, RO);
    await dropStorageByName(request, RW);
    // Leave ONLYOFFICE as a fresh install has it: off, no address, no secret.
    await request.patch('/api/admin/external/onlyoffice', { data: { enabled: false, url: '', secret: '' } });
  });

  test('Panel: the queue, the storages and the activity in words', async ({ page, request }) => {
    // A user made just now, so Recent activity has a row to name.
    await apiLogin(request);
    const who = `yeni-${STAMP}@example.com`;
    const made = await request.post('/api/admin/users', { data: { email: who, password: 'Yeni-kisi-2026!', role: 'user' } });
    expect(made.ok(), await made.text()).toBe(true);
    await signIn(page);

    await page.goto('/admin/dashboard');
    const stats = await (await request.get('/api/admin/queue/stats')).json();
    const depth = String((stats.pending ?? 0) + (stats.running ?? 0));
    const tile = page.locator('.stat-card', { hasText: /Kuyruk derinliği/i });
    await expect(tile.locator('.stat-card__value')).toHaveText(depth);

    const roCard = page.locator('.card', { hasText: RO }).first();
    const tags = roCard.getByTestId('storage-tags');
    await expect(tags.getByTestId('storage-tag-readonly')).toHaveText('Salt okunur');
    await expect(tags.getByTestId('storage-tag-driver')).toHaveText('Yerel dosya sistemi');
    await expect(roCard).not.toContainText(/\bRO\b/);

    await expect(page.getByTestId('dashboard-storage-state').first()).not.toHaveText(/^ok$/);
    const target = page.getByTestId('dashboard-activity-target').filter({ hasText: who });
    await expect(target.first()).toBeVisible();
    await expect(target.first()).toHaveText(`Kullanıcı “${who}”`);
  });

  // Translator report (2026-09-22): the Panel's six tiles shared a row on a
  // laptop and a value ("3,03 Ko") ran out of its ~64 px slot; status pills
  // wrapped onto two lines.
  test('Panel: tiles and status pills fit at laptop and phone widths', async ({ page }) => {
    await signIn(page);
    for (const width of [1024, 1280, 1440, 390]) {
      await page.setViewportSize({ width, height: 900 });
      await page.goto('/admin/dashboard');
      await expect(page.locator('.stat-card').first()).toBeVisible();
      // The widest value the tile can hold in any language: a size in the
      // hundreds with its unit.
      await page.locator('.stat-card__value').nth(3).evaluate((el) => (el.textContent = '999,9 Go'));
      const cut = await page.locator('.stat-card').evaluateAll((cards) =>
        cards.filter((c) => {
          const v = c.querySelector('.stat-card__value') as HTMLElement;
          const cr = c.getBoundingClientRect();
          const vr = v.getBoundingClientRect();
          return v.scrollWidth > v.clientWidth + 1 || vr.right > cr.right + 0.5;
        }).length,
      );
      expect(cut, `${width}px: a tile value does not fit its card`).toBe(0);
      const tall = await page.locator('span.ring-1').evaluateAll((pills) =>
        pills.filter((p) => p.getBoundingClientRect().height > 26).map((p) => p.textContent?.trim()),
      );
      expect(tall, `${width}px: a status pill wrapped`).toEqual([]);
    }
  });

  test('Audit: the address without its port, the filter by name', async ({ page }) => {
    await signIn(page);
    await page.goto('/admin/audit');
    const ips = await page.locator('.fe-list__row .fe-list__cell').allInnerTexts();
    expect(ips.some((x) => /^\d+\.\d+\.\d+\.\d+:\d+$/.test(x.trim())), 'an IPv4 address with a port').toBe(false);
    await page.getByTestId('audit-resource-filter').locator('select').selectOption({ label: 'Kullanıcı' });
    await expect(page.locator('[data-testid="audit-target"]').first()).toBeVisible();
    const actions = await page.locator('.fe-list__row').allInnerTexts();
    expect(actions.length).toBeGreaterThan(0);
    for (const row of actions) expect(row, row).toMatch(/Kullanıcı/);
  });

  test('ONLYOFFICE without a JWT secret: a red warning that stays until a secret is set', async ({ page, request }) => {
    await apiLogin(request);
    await onlyoffice(request, { enabled: true, url: 'http://127.0.0.1:9', secret: '' });
    await signIn(page);

    await page.goto('/admin/dashboard');
    const alert = page.getByTestId('onlyoffice-no-secret');
    await expect(alert).toBeVisible();
    await expect(alert).toHaveAttribute('role', 'alert');
    await expect(alert.getByRole('button')).toHaveCount(0);
    await expect(alert).toContainText('JWT');
    // Red: the palette's danger colour on its border.
    const border = await alert.evaluate((el) => getComputedStyle(el).borderTopColor);
    const danger = await page.evaluate(() => {
      const probe = document.createElement('div');
      probe.style.color = 'var(--fe-danger)';
      document.body.appendChild(probe);
      const c = getComputedStyle(probe).color;
      probe.remove();
      return c;
    });
    expect(border).toBe(danger);
    await alert.getByRole('link').click();
    await expect(page).toHaveURL(/\/admin\/external/);
    // One copy once the page transition has finished — External services' own.
    await expect(page.getByTestId('onlyoffice-no-secret')).toHaveCount(1);
    await expect(page.getByTestId('onlyoffice-no-secret')).toBeVisible();
    await expect(page.getByTestId('onlyoffice-no-secret').getByRole('link')).toHaveCount(0);

    await onlyoffice(request, { secret: 'a-long-shared-secret-2026' });
    await page.goto('/admin/dashboard');
    await expect(page.locator('.stat-card').first()).toBeVisible();
    await expect(page.getByTestId('onlyoffice-no-secret')).toHaveCount(0);
  });

  test('Corporate identity: the preview is the public page itself', async ({ page }) => {
    await signIn(page);
    await page.goto('/admin/branding');
    const preview = page.getByTestId('public-link-preview');
    await expect(preview.getByTestId('public-page')).toBeVisible();
    await page.getByPlaceholder('Acme Cloud').fill('Denetim Bulutu');
    await expect(preview.getByTestId('public-brand')).toContainText('Denetim Bulutu');
    await page.getByPlaceholder('#2f6ceb').fill('#aa3355');
    await expect(preview.getByTestId('public-share-download')).toHaveCSS('background-color', 'rgb(170, 51, 85)');
    // ⚠ The accent reaches the TINT as well as the solid button. It did not:
    // the button turned the operator's colour while the round badge, the
    // focus ring on the PIN box and the row hover stayed the stock blue,
    // because accentStyleOf emitted only --fe-primary / -hover / ink-on.
    await expect(preview.getByTestId('public-page-badge')).toHaveCSS('color', 'rgb(170, 51, 85)');
    // ⚠⚠ The preview is the page, so this measures the page's OWN shape and
    // not a taste: the instance's mark stands ABOVE the card, centred on it.
    // This assertion used to say the opposite ("the brand is at the start of
    // the card, not centred") and was pinning the regression the owner
    // reported on 2026-09-23 — v0.43.0 unified the public pages onto the
    // plainest of them instead of the best. The shape itself is measured in
    // tests/127-public-look.spec.ts; what matters HERE is that the operator's
    // preview shows it rather than something else.
    const card = await preview.locator('.fe-ppage__card').boundingBox();
    const brand = await preview.getByTestId('public-brand').boundingBox();
    expect(card && brand, 'the preview draws a card and a brand').toBeTruthy();
    expect(brand!.y + brand!.height, 'the brand sits above the card').toBeLessThanOrEqual(card!.y + 1);
    const off = Math.abs(brand!.x + brand!.width / 2 - (card!.x + card!.width / 2));
    expect(off, `the brand is centred on the card (off by ${off}px)`).toBeLessThan(4);
  });

  test('File history: a file is found by name, and no node id is asked for', async ({ page }) => {
    await signIn(page);
    await page.goto('/admin/files');
    await expect(page.getByText(/node id/i)).toHaveCount(0);
    await page.getByTestId('admin-files-q').locator('input').fill(`sozlesme-${STAMP}`);
    const row = page.getByTestId('admin-files-results').locator('.fe-list__row', { hasText: FILE });
    // The storage's first scan indexes the name; search again until it has.
    await expect(async () => {
      await page.getByTestId('admin-files-go').click();
      await expect(row).toBeVisible({ timeout: 2_000 });
    }).toPass({ timeout: 30_000 });
    await row.click();
    await expect(page).toHaveURL(/\/admin\/files\/\d+\/versions/);
    await expect(page.getByTestId('versions-file')).toContainText(FILE);
    await expect(page.getByTestId('versions-file')).toContainText(`/belgeler/${FILE}`);
    await expect(page.getByText(/Node #/)).toHaveCount(0);
  });

  test('Forms: an empty required box is refused in the panel language, nothing is sent', async ({ page }) => {
    await signIn(page);
    const writes: string[] = [];
    page.on('request', (r) => {
      if (r.method() !== 'GET' && r.url().includes('/api/admin/storages')) writes.push(r.url());
    });
    await page.goto('/admin/storages/new');
    await page.getByRole('button', { name: 'Oluştur', exact: true }).click();
    const err = page.getByTestId('field-error').first();
    await expect(err).toHaveText('Bu alanı doldurun.');
    expect(writes, 'the empty form was sent').toEqual([]);
    // The browser's own bubble would leave the box's validationMessage in the
    // browser's language and no text of ours on the page; ours is there.
    // Typing clears that box's sentence.
    const first = page.locator('input[required]').first();
    await expect(first).toHaveAttribute('aria-invalid', 'true');
    await first.fill('x');
    await expect(first).not.toHaveAttribute('aria-invalid', 'true');
  });

  test('Words: no wire values and no English on the Turkish pages', async ({ page }) => {
    await signIn(page);
    const pages: Array<[string, RegExp[]]> = [
      ['/admin/queue', [/content_index/, /\{"node_id"/, /24S TAMAM/i]],
      ['/admin/settings', [/\bNone\b/]],
      ['/admin/about', [/THUMBNAİL/, /Sqlite/, /search:/, /unknown, unknown/]],
      ['/admin/updates', [/politika: (manual|off|patch|minor)/i, /\d{4}-\d\d-\d\dT\d\d:\d\d/]],
      ['/admin/sync', [/^\s*ok\s*$/m, /\b\d+s\b/]],
      ['/admin/search', [/döküman/i]],
    ];
    for (const [url, bad] of pages) {
      await page.goto(url);
      await page.waitForLoadState('networkidle');
      const main = page.locator('main');
      const text = (await main.count()) ? await main.innerText() : await page.locator('body').innerText();
      const opts = await page.locator('main option').allInnerTexts();
      for (const re of bad) expect(`${text}\n${opts.join('\n')}`, `${url} still shows ${re}`).not.toMatch(re);
    }
  });
});
