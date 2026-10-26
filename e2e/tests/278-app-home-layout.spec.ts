/** An own-interface home fills the space below its heading; Surface homes
 * remain scrolling documents. No business app or external service is needed. */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { openSignedIn } from '../helpers/auth';
import { newAuthedRequest } from '../helpers/seed';
import { installInterfaceApp, removeAppByName } from '../helpers/appInterface';
import { zip } from '../helpers/zip';

const bundle = zip({
  'index.html': '<!doctype html><html><head><link rel="stylesheet" href="style.css"><script src="app.js" defer></script></head><body><h1>Layout fixture</h1><div id="long">Scrollable content</div><p id="end">End of app</p></body></html>',
  'style.css': 'body { margin: 0; } #long { height: 1600px; }',
  'app.js': `addEventListener('message', e => {
    if (e.source === parent && e.data?.type === 'filex:port') document.body.dataset.ready = '1';
  }); parent.postMessage({type: 'filex:hello', v: 1}, '*');`,
});

test.describe('App home viewport layout', () => {
  let api: APIRequestContext;
  let app: string;
  test.beforeAll(async ({ playwright, baseURL }, info) => {
    app = `layout-${info.project.name.replace(/[^a-z]/g, '').slice(0, 12)}`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await removeAppByName(api, app);
    await installInterfaceApp(api, {
      manifest_version: 1, name: app, version: '1.0.0', label: { en: 'Layout fixture' },
      permissions: [], ui: { bundle: {} },
      views: [{ id: 'home', placement: 'home', ui: 'index.html', label: { en: 'Layout fixture' } }],
    }, bundle);
  });
  test.afterAll(async () => { await removeAppByName(api, app); await api.dispose(); });

  for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }, { width: 900, height: 400 }]) {
    test(`own interface fits ${viewport.width}x${viewport.height} and scrolls inside its frame`, async ({ page }) => {
      await page.setViewportSize(viewport);
      await openSignedIn(page, `/drive/app/${app}/home`);
      const iframe = page.getByTestId('app-frame');
      await expect(page.locator('.fe-appframe[data-connected="true"]')).toBeVisible();
      const box = await iframe.boundingBox();
      expect(box).not.toBeNull();
      const geometry = await page.getByTestId('app-screen').evaluate(el => {
        const card = el.querySelector('.card')!;
        const body = el.querySelector('.fe-apppage__body--ui')!;
        return { bottom: el.getBoundingClientRect().bottom, padding: parseFloat(getComputedStyle(el).paddingBottom),
          border: getComputedStyle(card).borderTopWidth, bodyPadding: getComputedStyle(body).paddingTop,
          overflowX: document.documentElement.scrollWidth > innerWidth + 1,
          overflowY: document.documentElement.scrollHeight > innerHeight + 1 };
      });
      expect(geometry.overflowX).toBe(false);
      expect(geometry.overflowY).toBe(false);
      expect(geometry.border).toBe('0px');
      expect(geometry.bodyPadding).toBe('0px');
      expect(Math.abs(box!.y + box!.height - (geometry.bottom - geometry.padding))).toBeLessThanOrEqual(2);
      await iframe.hover();
      await page.mouse.wheel(0, 2400);
      await expect.poll(async () => {
        const frame = await (await iframe.elementHandle())!.contentFrame();
        return frame!.evaluate(() => scrollY);
      }).toBeGreaterThan(0);
      await expect(page.getByTestId('app-screen-back')).toBeInViewport();
    });
  }

  test('Surface homes keep their document scrolling and card', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.route('**/api/files/plugins/actions*', route => route.fulfill({ json: { actions: [], views: [] } }));
    await page.route(`**/api/files/plugins/views/${app}/home*`, route => route.fulfill({ json: { surface: {
      title: { en: 'Surface fixture' }, state: {},
      nodes: Array.from({ length: 60 }, (_, i) => ({ type: 'text', props: { text: { en: `Surface row ${i}` } } })), actions: [],
    } } }));
    await openSignedIn(page, `/drive/app/${app}/home`);
    await expect(page.getByText('Surface row 59', { exact: true })).toBeAttached();
    expect(await page.getByTestId('app-frame').count()).toBe(0);
    expect(await page.evaluate(() => document.documentElement.scrollHeight > innerHeight)).toBe(true);
    await page.getByText('Surface row 59', { exact: true }).scrollIntoViewIfNeeded();
    await expect(page.getByText('Surface row 59', { exact: true })).toBeInViewport();
    expect(await page.locator('[data-testid="app-screen"] > .card').evaluate(el => getComputedStyle(el).borderTopWidth)).not.toBe('0px');
  });
});
