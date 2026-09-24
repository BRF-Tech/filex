/**
 * 114-language-pack — an app that adds a LANGUAGE to filex, end to end.
 *
 * The owner's words: "uygulama filex'e dil paketi ekleyebilmeli — adam Arapça
 * kullanmak isterse app içinden dili ekleyebilsin". The first complete pack
 * (Spanish, 2 900 strings) showed it had never worked end to end: the install
 * was refused (no module; "at most 2000"), the strings never reached the
 * browser (a map on the wire, a list in the client), the pickers offered only
 * English and Turkish, a stored choice was dropped on every reload, and the
 * explorer half rendered English whatever the panel spoke. This walks all of
 * it in a real browser:
 *
 *   1. the pack installs from its MANIFEST alone through the Apps wizard; the
 *      review says it is a language pack and how much of this filex it covers;
 *   2. picked in the settings dialog, the admin panel, the explorer and a
 *      public share page all speak it — and it survives a reload;
 *   3. uninstalled, it leaves every picker and the interface falls back.
 *
 * The pack is `e2e/fixtures/lang-pack/filex-app.json` (a handful of strings).
 * Set FILEX_E2E_LANG_PACK to a complete `translations/es.json` (the Spanish
 * pack) and the same walk runs with the whole translation.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { readFileSync, writeFileSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';

const HERE = dirname(fileURLToPath(import.meta.url));
const STORAGE = `e2e-lang-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const FILE = 'hola.txt';

/** The pack under test: the fixture, or a complete translation laid over it. */
function packManifest(): { path: string; name: string; es: Record<string, string> } {
  const fixture = JSON.parse(readFileSync(resolve(HERE, '../fixtures/lang-pack/filex-app.json'), 'utf8'));
  const full = process.env.FILEX_E2E_LANG_PACK;
  if (full) fixture.ui_locales.es = JSON.parse(readFileSync(full, 'utf8'));
  const dir = mkdtempSync(join(tmpdir(), 'filex-lang-'));
  const path = join(dir, 'filex-app.json');
  writeFileSync(path, JSON.stringify(fixture));
  return { path, name: fixture.name, es: fixture.ui_locales.es };
}
const PACK = packManifest();

/**
 * The same pack under a label long enough to fill the Label column twice over
 * — a German interface name, which is what found the overlap below.
 */
function longLabelManifest(): { path: string; name: string; label: string } {
  const fixture = JSON.parse(readFileSync(resolve(HERE, '../fixtures/lang-pack/filex-app.json'), 'utf8'));
  fixture.name = 'lang-de-e2e';
  fixture.label = {
    en: 'Deutsche Benutzeroberfl\u00e4chen\u00fcbersetzung (e2e)',
    tr: 'Almanca aray\u00fcz \u00e7evirisi (e2e)',
  };
  fixture.ui_locales = { de: fixture.ui_locales.es };
  const dir = mkdtempSync(join(tmpdir(), 'filex-lang-de-'));
  const path = join(dir, 'filex-app.json');
  writeFileSync(path, JSON.stringify(fixture));
  return { path, name: fixture.name, label: fixture.label.en };
}
const LONG = longLabelManifest();

/** A DOM rectangle, as Playwright hands it back. */
type Box = { x: number; y: number; width: number; height: number };

/** Do two drawn boxes cover any of the same pixels? */
function overlaps(a: Box, b: Box): boolean {
  return (
    a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height
  );
}

async function removeByName(api: APIRequestContext, name: string) {
  const list = await api.get('/api/admin/app-plugins');
  if (!list.ok()) return;
  for (const p of (await list.json()).plugins ?? []) {
    if (p.name === name) await api.delete(`/api/admin/app-plugins/${p.id}`);
  }
}

async function removePack(api: APIRequestContext) {
  await removeByName(api, PACK.name);
}

const PREFS = '/api/me/prefs?surface=web';

/**
 * The account's preference document as it was before this file ran.
 *
 * ⚠⚠ Restored EXACTLY, not "set to English". Writing `locale: 'en'` into a
 * document that had no `locale` key leaks into every later spec: the server's
 * copy then outranks a browser's own fresh choice, and 60-user-settings
 * (which picks Turkish and reloads) opened in English — measured in the full
 * run that first carried this file.
 */
let prefsBefore: Record<string, unknown> = {};

async function readPrefs(api: APIRequestContext): Promise<Record<string, unknown>> {
  const got = await api.get(PREFS);
  const doc = got.ok() ? (await got.json()).prefs : {};
  return doc && typeof doc === 'object' && !Array.isArray(doc) ? doc : {};
}

async function restoreAccount(api: APIRequestContext) {
  await api.put(PREFS, { data: { prefs: prefsBefore } }).catch(() => undefined);
  await api.patch('/api/auth/profile', { data: { locale: 'en' } }).catch(() => undefined);
}

async function openPreferences(page: Page) {
  await page.goto('/admin/dashboard?settings=1');
  await expect(page.getByTestId('user-settings-dialog')).toBeVisible({ timeout: 15_000 });
  await page.getByTestId('user-settings-tab-preferences').click();
  await expect(page.getByTestId('user-settings-locale')).toBeVisible();
}

test.describe.serial('Language pack — install, every surface, uninstall', () => {
  let api: APIRequestContext;
  let token = '';

  test.beforeAll(async ({ playwright, baseURL, request }) => {
    api = await newAuthedRequest(playwright, baseURL ?? '');
    prefsBefore = await readPrefs(api);
    await removePack(api);
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    const up = await api.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://`, 'file[]': { name: FILE, mimeType: 'text/plain', buffer: Buffer.from('hola') } },
    });
    expect(up.ok(), `upload ${up.status()}`).toBeTruthy();
    const made = await api.post('/api/files/share', { data: { path: `${STORAGE}://${FILE}` } });
    expect(made.ok(), `share ${made.status()}`).toBeTruthy();
    token = (await made.json()).share.token;
  });

  test.afterAll(async ({ request }) => {
    await removePack(api);
    await removeByName(api, LONG.name);
    await restoreAccount(api);
    await dropStorageByName(request, STORAGE);
    await api.dispose();
  });

  test('installs from its manifest alone, and the review says what it is', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto('/admin/plugins');
    await page.getByTestId('plugins-tab-apps').click();
    await page.getByTestId('app-plugin-add').click();
    await page.getByTestId('app-plugin-source-file').click();
    // ⚠ NO module: a language pack is its manifest.
    await page.getByTestId('app-plugin-manifest').setInputFiles(PACK.path);
    await page.getByTestId('app-plugin-review').click();

    await expect(page.getByTestId('app-plugin-is-language-pack')).toBeVisible();
    await expect(page.getByTestId('app-plugin-language-es')).toBeVisible();
    await expect(page.getByTestId('app-plugin-language-es-coverage')).toHaveText(/^\d+% translated$/);
    // Arabic installs with the rest, and — the right-to-left layout being in
    // this release (docs/RTL.md) — without a "comes later" notice.
    await expect(page.getByTestId('app-plugin-language-ar')).toBeVisible();
    await expect(page.getByTestId('app-plugin-language-ar-rtl')).toHaveCount(0);
    await expect(page.getByTestId('app-plugin-permissions')).toHaveCount(0);

    await page.getByLabel(/I understand|Anladım/).check();
    await page.getByTestId('app-plugin-install').click();
    await expect(page.getByTestId(`app-plugin-${PACK.name}`)).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId(`app-plugin-kind-${PACK.name}`)).toHaveText('Language pack');
    await expect(page.getByTestId('app-plugin-language-es-coverage').first()).toHaveText(/^\d+% translated$/);
  });

  /**
   * ⚠⚠ The Label cell composes instead of stacking on top of itself.
   *
   * QA found it in the v0.43.0 translation pass, in ENGLISH as well as German:
   * the slot put a `<span>` label, a "Language pack" `<Badge class="ms-1">`
   * and `<AppPluginLanguages class="mt-1">` side by side inside a DataTable
   * cell, and a cell is `display: flex`. `ms-1` and `mt-1` are margins on
   * flex ITEMS: the badge was drawn over the label and the coverage line over
   * the badge, in a 180px track, at 958px and at 1440px alike.
   *
   * Measured, not asserted about: three real bounding boxes, at three widths,
   * with a label long enough to want the whole column. The unit gate that
   * stops the shape coming back is web/tests/ui/tablePinnedActions.test.ts.
   */
  test('the Label cell stacks, it does not overlap — at 958, 1280 and 1440px', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto('/admin/plugins');
    await page.getByTestId('plugins-tab-apps').click();

    // A second pack, under a long German label — the row the overlap was
    // reported on. It installs from its manifest like the first one.
    await page.getByTestId('app-plugin-add').click();
    await page.getByTestId('app-plugin-source-file').click();
    await page.getByTestId('app-plugin-manifest').setInputFiles(LONG.path);
    await page.getByTestId('app-plugin-review').click();
    await page.getByLabel(/I understand|Anladım/).check();
    await page.getByTestId('app-plugin-install').click();
    await expect(page.getByTestId(`app-plugin-${LONG.name}`)).toBeVisible({ timeout: 15_000 });

    for (const width of [958, 1280, 1440]) {
      await page.setViewportSize({ width, height: 900 });
      await page.waitForTimeout(150);
      for (const pack of [PACK.name, LONG.name]) {
        /* ⚠ Scoped to the ROW, not to the cell's wrapper: a wrapper is what
           this test is checking FOR, and a measurement that can only find the
           parts when they are already wrapped cannot see the bug. */
        const row = page.locator('.fe-list__row').filter({ has: page.getByTestId(`app-plugin-${pack}`) });
        await expect(row, `${pack} @${width}`).toBeVisible();
        const label = await row.getByTestId(`app-plugin-label-${pack}`).boundingBox();
        const badge = await row.getByTestId(`app-plugin-kind-${pack}`).boundingBox();
        const langs = await row.getByTestId('app-plugin-languages').boundingBox();
        expect(label && badge && langs, `${pack} @${width}: a part of the cell is not drawn`).toBeTruthy();

        expect(overlaps(label!, badge!), `${pack} @${width}: the badge covers the label`).toBe(false);
        expect(overlaps(label!, langs!), `${pack} @${width}: the coverage line covers the label`).toBe(false);
        expect(overlaps(badge!, langs!), `${pack} @${width}: the coverage line covers the badge`).toBe(false);

        /* The shape, not only the absence of a collision.
           ⚠ The badge follows the label in READING ORDER — beside it when both
           fit the column, on the line under it when they do not. It used to be
           pinned beside it ("badge.x >= label right edge"), which was the right
           assertion while the row could not wrap; the row wraps now, so that
           spelling failed for exactly the labels this test was added for and
           would have been "fixed" by taking the wrap out again. What must
           never happen is the badge starting before the label on the label's
           own line — that is the smear this whole test exists for, and it is
           still caught. */
        const sameLine = Math.abs(badge!.y - label!.y) < Math.max(label!.height, badge!.height) / 2;
        if (sameLine) {
          expect(badge!.x, `${pack} @${width}: the badge is not after the label`).toBeGreaterThanOrEqual(label!.x + label!.width - 1);
        } else {
          expect(badge!.y, `${pack} @${width}: the badge is above the label`).toBeGreaterThanOrEqual(label!.y + label!.height - 1);
        }
        expect(langs!.y, `${pack} @${width}: the coverage line is not on its own line`).toBeGreaterThanOrEqual(
          Math.max(label!.y + label!.height, badge!.y + badge!.height) - 1,
        );
        // And the cell keeps its own track: nothing bleeds into Version.
        const track = await row.getByTestId(`app-plugin-cell-label-${pack}`).boundingBox();
        expect(langs!.x, `${pack} @${width}: the coverage line starts outside its cell`).toBeGreaterThanOrEqual(track!.x - 1);
        expect(
          langs!.x + langs!.width,
          `${pack} @${width}: the coverage line runs past its column`,
        ).toBeLessThanOrEqual(track!.x + track!.width + 1);
      }
    }
    await page.setViewportSize({ width: 1280, height: 900 });
    await removeByName(api, LONG.name);
  });

  test('picked once, the panel, the explorer and a public page speak it — and a reload keeps it', async ({ page, browser }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await openPreferences(page);
    // ⚠ The picker lists the OFFERED languages, not a fixed pair.
    await page.getByTestId('user-settings-locale-es').click();
    await expect(page.locator('html')).toHaveAttribute('lang', 'es');
    // Spanish is written left to right: `dir` follows `lang` (syncDocumentDir).
    await expect(page.locator('html')).not.toHaveAttribute('dir', 'rtl');
    await expect(page.getByTestId('user-settings-locale')).toContainText('Español');
    await page.keyboard.press('Escape');

    // The admin panel.
    await page.goto('/admin/plugins');
    await expect(page.getByText(PACK.es['nav.plugins'], { exact: true }).first()).toBeVisible({ timeout: 15_000 });

    // A reload: the stored choice is HELD until the offered list arrives,
    // then the pack's words replace English.
    await page.reload();
    await expect(page.locator('html')).toHaveAttribute('lang', 'es');
    await expect(page.getByText(PACK.es['nav.plugins'], { exact: true }).first()).toBeVisible({ timeout: 15_000 });

    // The explorer half — this is what `resolveLocale` used to throw away.
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
    await expect(page.getByRole('columnheader', { name: new RegExp(PACK.es['col.name']) }).first()).toBeVisible({ timeout: 15_000 });

    // A public share page, opened by a stranger whose browser asks for Spanish.
    const stranger = await browser.newContext({ locale: 'es-ES' });
    try {
      const pub = await stranger.newPage();
      await pub.goto(`/s/${token}`);
      await expect(pub.getByTestId('public-share-download')).toHaveText(PACK.es['ctx.download'], { timeout: 15_000 });
      await expect(pub.getByTestId('public-language-es')).toBeVisible();
    } finally {
      await stranger.close();
    }

    // …and one whose browser does not, picking it from the page's own picker.
    const other = await browser.newContext({ locale: 'en-US' });
    try {
      const pub = await other.newPage();
      await pub.goto(`/s/${token}`);
      await expect(pub.getByTestId('public-share-download')).toHaveText('Download', { timeout: 15_000 });
      await pub.getByTestId('public-language-es').click();
      await expect(pub.getByTestId('public-share-download')).toHaveText(PACK.es['ctx.download']);
    } finally {
      await other.close();
    }
  });

  test('uninstalled, it leaves every picker and the interface falls back', async ({ page }) => {
    await removePack(api);
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await openPreferences(page);
    await expect(page.getByTestId('user-settings-locale-en')).toBeVisible();
    await expect(page.getByTestId('user-settings-locale-es')).toHaveCount(0);
    // The account still SAYS `es` — held for a pack that may come back — but
    // nothing offers it, so the interface is in English, not in a limbo.
    await expect(page.locator('html')).toHaveAttribute('lang', 'en');

    const anon = await page.context().browser()!.newContext({ locale: 'es-ES' });
    try {
      const pub = await anon.newPage();
      await pub.goto(`/s/${token}`);
      await expect(pub.getByTestId('public-share-download')).toHaveText('Download', { timeout: 15_000 });
      await expect(pub.getByTestId('public-language-es')).toHaveCount(0);
    } finally {
      await anon.close();
    }
  });
});
