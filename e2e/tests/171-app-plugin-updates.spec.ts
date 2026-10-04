/**
 * 171-app-plugin-updates — an installed app follows the source it came from,
 * and says which filex versions it works with. Walked in a real browser:
 *
 *   1. ⚠⚠ Nothing updates itself (0.48, owner's rule). A language pack whose
 *      source publishes a new version is ANNOUNCED when "Check for updates"
 *      asks — "Update available", the jump, the bell once — and does not
 *      move. "Review update" → Upgrade moves it for everybody; the version it
 *      replaced is kept, and "Back to 1.0.0" puts it back.
 *   2. An app whose new version asks for a permission it was not granted is
 *      NOT installed: the row says "Needs approval" and what it adds, and
 *      "Review update" opens the review of that version — the jump and the
 *      new permission marked — without asking for a source again.
 *   3. A manifest whose `filex` range leaves this server out is said at the
 *      install review, and Install stays off.
 *   4. The Version cell, which carries all of it, stacks: its badge and its
 *      lines do not overlap and nothing is cut, at 958 and 1440px (#433).
 *   5. The review's release notes are Markdown, drawn safely (#122).
 *
 * ⚠ The "source" is a tiny HTTP server on 127.0.0.1 inside this spec (plain
 * http is accepted for loopback only, wasmplugin/fetch.go, and only with
 * FILEX_PLUGIN_LOOPBACK_SOURCES=1, which e2e/run.mjs sets); nothing here
 * reaches GitHub. Point 3 needs a binary stamped with a RELEASE version: a
 * development build checks no range (wasmplugin/compat.go) — run with a
 * binary built with `-X …/internal/version.Version=0.47.0`.
 *
 * Point 2 installs the `echo` fixture module (backend/internal/wasmplugin/
 * testdata/echo, scripts/build-wasm-fixture.sh, read through
 * helpers/echoFixture), skips when it is absent and fails on a module older
 * than its sources.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { createServer, type Server } from 'node:http';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import type { AddressInfo } from 'node:net';
import { loginAs } from '../helpers/auth';
import { newAuthedRequest } from '../helpers/seed';
import { guardFixture } from '../helpers/appPlugin';
import { echoFixture } from '../helpers/echoFixture';

const ECHO = echoFixture();

const PACK = 'lang-e2e-updates';

/** What the fake source serves, by path — changed between steps. */
const served = new Map<string, Buffer>();
let server: Server;
let origin = '';

function packManifest(version: string, extra: Record<string, unknown> = {}): Buffer {
  return Buffer.from(
    JSON.stringify({
      manifest_version: 1,
      name: PACK,
      version,
      label: { en: 'Esperanto (e2e updates)', tr: 'Esperanto (e2e güncellemeler)' },
      languages: ['en', 'tr'],
      permissions: [],
      ui_locales: { eo: { 'common.cancel': 'Nuligi' } },
      ...extra,
    }),
  );
}

function echoManifest(version: string, extraPermissions: string[] = []): Buffer {
  const m = JSON.parse(readFileSync(ECHO.manifestPath, 'utf8'));
  m.version = version;
  m.permissions = [...m.permissions, ...extraPermissions];
  const wasm = readFileSync(ECHO.wasm);
  m.wasm = { url: `${origin}/echo/plugin.wasm`, sha256: createHash('sha256').update(wasm).digest('hex') };
  return Buffer.from(JSON.stringify(m));
}

async function removeByName(api: APIRequestContext, name: string) {
  const list = await api.get('/api/admin/app-plugins');
  if (!list.ok()) return;
  for (const p of (await list.json()).plugins ?? []) {
    if (p.name === name) await api.delete(`/api/admin/app-plugins/${p.id}`);
  }
}

async function openApps(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await page.goto('/admin/plugins');
  await page.getByTestId('plugins-tab-apps').click();
}

async function checkNow(page: Page) {
  const answered = page.waitForResponse((r) => r.url().endsWith('/api/admin/app-plugins/updates/check'), { timeout: 120_000 });
  await page.getByTestId('app-plugins-check-updates').click();
  const res = await answered;
  expect(res.status(), await res.text()).toBe(200);
}

test.describe.serial('App updates — a source followed, a range said', () => {
  let api: APIRequestContext;
  // ⚠ The language is pinned on the ACCOUNT and restored (lesson #616): the
  // assertions read English ("Update available"), and an earlier spec's page
  // (163, filex.locale=tr) leaves the shared admin's document Turkish; in a
  // full run this spec read "Güncelleme var".
  const PREFS = '/api/me/prefs?surface=web';
  let prefsBefore: Record<string, unknown> = {};

  test.beforeAll(async ({ playwright, baseURL }) => {
    server = createServer((req, res) => {
      const body = served.get((req.url ?? '').split('?')[0]);
      if (!body) {
        res.writeHead(404).end();
        return;
      }
      res.writeHead(200, { 'Content-Type': 'application/octet-stream' }).end(body);
    });
    await new Promise<void>((ok) => server.listen(0, '127.0.0.1', ok));
    origin = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const got = await api.get(PREFS);
    const doc = got.ok() ? (await got.json()).prefs : {};
    prefsBefore = doc && typeof doc === 'object' && !Array.isArray(doc) ? doc : {};
    expect((await api.put(PREFS, { data: { prefs: { ...prefsBefore, locale: 'en' } } })).ok()).toBe(true);
    await removeByName(api, PACK);
    await removeByName(api, 'echo');
  });

  test.afterAll(async () => {
    await api?.put(PREFS, { data: { prefs: prefsBefore } }).catch(() => undefined);
    await removeByName(api, PACK);
    await removeByName(api, 'echo');
    await api.dispose();
    await new Promise<void>((ok) => server.close(() => ok()));
  });

  test('a language pack’s new version is announced, not installed — approved, then undone', async ({ page }) => {
    served.set('/pack/filex-app.json', packManifest('1.0.0'));
    const made = await api.post('/api/admin/app-plugins', {
      data: { url: '', manifest_url: `${origin}/pack/filex-app.json`, permissions: [] },
    });
    expect(made.status(), await made.text()).toBe(201);
    const installed = await made.json();
    expect(installed.auto_update, 'there is no switch any more').toBeUndefined();
    expect(installed.update_source).toBe('url');

    served.set('/pack/filex-app.json', packManifest('1.0.1'));
    await openApps(page);
    const row = page.locator('.fe-list__row').filter({ has: page.getByTestId(`app-plugin-${PACK}`) });
    await expect(row).toBeVisible();
    await checkNow(page);

    await expect(page.getByTestId(`app-plugin-update-available-${PACK}`)).toHaveText('Update available');
    await expect(page.getByTestId(`app-plugin-version-${PACK}`), 'nothing moved by itself').toHaveText('1.0.0');
    await expect(page.getByTestId(`app-plugin-updates-${PACK}`)).toContainText('1.0.0 → 1.0.1');
    await page.screenshot({ path: test.info().outputPath('apps-update-available.png') });

    // The administrators' bell heard it once, in words, not the event id.
    const noticesOf = async (event: string) =>
      (((await (await api.get('/api/notifications?limit=50')).json()).items ?? []) as { event: string; meta?: Record<string, unknown> }[]).filter(
        (n) => n.event === event && n.meta?.plugin === PACK,
      );
    expect(await noticesOf('app_update_available'), 'one notice for one version').toHaveLength(1);
    await checkNow(page);
    expect(await noticesOf('app_update_available'), 'and not again the next day').toHaveLength(1);
    expect(await noticesOf('app_updated'), 'nothing was installed').toHaveLength(0);

    // The administrator approves: Review update → Upgrade.
    await page.getByTestId(`app-plugin-actions-${PACK}`).click();
    await page.getByTestId(`app-plugin-actions-${PACK}-update`).click();
    await expect(page.getByTestId('app-plugin-upgrade-jump')).toHaveText('Version 1.0.0 → 1.0.1', { timeout: 60_000 });
    await page.getByLabel(/I understand|Anladım/).check();
    await page.getByTestId('app-plugin-install').click();
    // The list closes the dialog on success and redraws the row.
    await expect(page.getByTestId(`app-plugin-version-${PACK}`)).toHaveText('1.0.1', { timeout: 60_000 });
    await expect(page.getByTestId(`app-plugin-updates-${PACK}`)).toContainText('Version 1.0.0 is kept to go back to');
    expect(await noticesOf('app_updated'), 'the approved change is told once').toHaveLength(1);

    // Back to 1.0.0, after the question.
    page.once('dialog', (d) => void d.accept());
    await page.getByTestId(`app-plugin-actions-${PACK}`).click();
    await page.getByTestId(`app-plugin-actions-${PACK}-rollback`).click();
    await expect(page.getByTestId(`app-plugin-version-${PACK}`)).toHaveText('1.0.0', { timeout: 60_000 });
    await expect(page.getByTestId(`app-plugin-updates-${PACK}`)).toContainText('Version 1.0.1 is kept to go back to');
    await page.screenshot({ path: test.info().outputPath('apps-rolled-back.png') });
  });

  test('a version that asks for a new permission waits — and "Review update" shows what it asks', async ({ page }) => {
    guardFixture(ECHO, test.skip);
    served.set('/echo/plugin.wasm', readFileSync(ECHO.wasm));
    served.set('/echo/filex-app.json', echoManifest('0.0.1'));
    const perms = JSON.parse(served.get('/echo/filex-app.json')!.toString()).permissions as string[];
    const made = await api.post('/api/admin/app-plugins', {
      data: { url: `${origin}/echo/plugin.wasm`, manifest_url: `${origin}/echo/filex-app.json`, permissions: perms },
      timeout: 180_000,
    });
    expect(made.status(), await made.text()).toBe(201);

    // The next version asks for one host more.
    served.set('/echo/filex-app.json', echoManifest('0.0.2', ['http:updates.example.org']));
    await openApps(page);
    await checkNow(page);

    const cell = page.getByTestId('app-plugin-updates-echo');
    await expect(page.getByTestId('app-plugin-update-approval-echo')).toHaveText('Needs approval');
    await expect(cell).toContainText('0.0.1 → 0.0.2');
    await expect(cell).toContainText('new: http:updates.example.org');
    const row = page.locator('.fe-list__row').filter({ has: page.getByTestId('app-plugin-echo') });
    await expect(page.getByTestId('app-plugin-version-echo'), 'nothing was installed by itself').toHaveText('0.0.1');
    await expect(row).toBeVisible();

    await page.getByTestId('app-plugin-actions-echo').click();
    await page.getByTestId('app-plugin-actions-echo-update').click();
    await expect(page.getByTestId('app-plugin-upgrade-jump')).toHaveText('Version 0.0.1 → 0.0.2', { timeout: 60_000 });
    await expect(page.getByTestId('app-plugin-upgrade-added')).toContainText('http:updates.example.org');
    await expect(page.getByTestId('perm-new-http:updates.example.org')).toBeVisible();
    await expect(page.getByTestId('app-plugin-from-source')).toHaveCount(0); // it went straight to the review
    await page.screenshot({ path: test.info().outputPath('apps-review-update.png') });
  });

  test('a range that leaves this filex out is said at the review, and Install stays off', async ({ page }) => {
    const runtime = (await (await api.get('/api/admin/app-plugins')).json()).runtime;
    test.skip(runtime.compat_enforced !== true, `a development build (${runtime.filex_version}) checks no range`);
    served.set('/ahead/filex-app.json', Buffer.from(packManifest('3.0.0', { name: 'lang-e2e-ahead', filex: '>=99.0.0' })));
    await openApps(page);
    await page.getByTestId('app-plugin-add').click();
    await page.getByTestId('app-plugin-source-url').click();
    await page.getByLabel(/Manifest URL/).fill(`${origin}/ahead/filex-app.json`);
    await page.getByTestId('app-plugin-review').click();
    const box = page.getByTestId('app-plugin-incompatible');
    await expect(box).toContainText('works with filex >=99.0.0');
    await expect(box).toContainText(`this is filex ${runtime.filex_version}`);
    await page.getByLabel(/I understand|Anladım/).check();
    await expect(page.getByTestId('app-plugin-install')).toBeDisabled();
  });

  /**
   * ⚠ Measured, not asserted about: the badge's LABEL text and the lines
   * under it, as line rectangles (Range.getClientRects — a badge box can be
   * squeezed below its own label, lesson #433), and no cell wider than its
   * box.
   */
  test('the Version cell stacks, it does not overlap — at 958 and 1440px', async ({ page }) => {
    // It measures the row the previous test left.
    guardFixture(ECHO, test.skip);
    await openApps(page);
    for (const width of [958, 1440]) {
      await page.setViewportSize({ width, height: 900 });
      await page.waitForTimeout(150);
      for (const name of ['echo', PACK]) {
        const cell = page.getByTestId(`app-plugin-updates-${name}`);
        await cell.scrollIntoViewIfNeeded();
        const measured = await cell.evaluate((el) => {
          const rects = (n: Element) => {
            const r = document.createRange();
            r.selectNodeContents(n);
            return [...r.getClientRects()].map((x) => ({ x: x.x, y: x.y, w: x.width, h: x.height }));
          };
          const badge = el.querySelector('[data-testid^="app-plugin-update-"]')!;
          const lines = [...el.children].filter((c) => !c.contains(badge));
          const cellBox = el.closest('.fe-list__cell') as HTMLElement;
          return {
            badge: rects(badge),
            lines: lines.flatMap(rects),
            squeezed: (badge as HTMLElement).scrollWidth > (badge as HTMLElement).clientWidth + 1,
            overflow: cellBox.scrollWidth > cellBox.clientWidth + 1,
          };
        });
        const hit = (a: { x: number; y: number; w: number; h: number }, b: typeof a) =>
          a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
        expect(measured.badge.length, `${name} @${width}: the badge label is drawn`).toBeGreaterThan(0);
        for (const b of measured.badge) {
          for (const l of measured.lines) expect(hit(b, l), `${name} @${width}: a line covers the badge`).toBe(false);
        }
        expect(measured.squeezed, `${name} @${width}: the badge is narrower than its label`).toBe(false);
        expect(measured.overflow, `${name} @${width}: the cell is wider than its column`).toBe(false);
      }
      await page.screenshot({ path: test.info().outputPath(`apps-updates-${width}.png`) });
    }
  });

  /**
   * #122: a release's notes are Markdown (a GitHub release's body), drawn
   * through the explorer preview's pipeline - markdown-it, then the document
   * sanitizer - so `**bold**` is bold and nothing in them runs. 0.48.1 printed
   * them as plain text: asterisks and dashes.
   *
   * ⚠ Notes come from a GitHub source's releases, and this spec's source is a
   * loopback URL (nothing here reaches GitHub), so the review's answer is
   * given its notes on the way to the page; everything else in it is the
   * server's.
   */
  test('"Review update" draws the release notes as Markdown, and nothing in them runs', async ({ page }) => {
    const NOTES = [
      '## 1.0.2',
      '',
      '- **Kalın** bir düzeltme: `Ctrl+S` artık kaydediyor',
      '- [Full Changelog](https://github.com/BRF-Tech/filextext-app/compare/v1.0.1...v1.0.2)',
      '',
      'Fixed <img src="x.png" onerror="window.pwned=1"> and <script>window.pwned=2</script> too.',
    ].join('\n');
    served.set('/pack/filex-app.json', packManifest('1.0.2'));
    await openApps(page);
    await checkNow(page);
    await page.route(/\/api\/admin\/app-plugins\/\d+\/upgrade\?(.*&)?dry_run=1/, async (route) => {
      const res = await route.fetch();
      const body = await res.json();
      body.upgrade = { ...(body.upgrade ?? {}), notes: NOTES };
      await route.fulfill({ response: res, json: body });
    });
    await page.getByTestId(`app-plugin-actions-${PACK}`).click();
    await page.getByTestId(`app-plugin-actions-${PACK}-update`).click();

    const notes = page.getByTestId('app-plugin-upgrade-notes-md');
    await expect(notes).toBeVisible({ timeout: 60_000 });
    await expect(notes.locator('h2')).toHaveText('1.0.2');
    await expect(notes.locator('strong')).toHaveText('Kalın');
    await expect(notes.locator('li')).toHaveCount(2);
    await expect(notes.locator('code')).toHaveText('Ctrl+S');
    await expect(notes).not.toContainText('**');
    await expect(notes.locator('script')).toHaveCount(0);
    expect(await notes.locator('img').evaluateAll((els) => els.map((e) => e.getAttribute('onerror')))).toEqual([null]);
    expect(await page.evaluate(() => (window as unknown as { pwned?: number }).pwned)).toBeUndefined();
    await page.screenshot({ path: test.info().outputPath('apps-review-update-notes.png') });
  });
});
