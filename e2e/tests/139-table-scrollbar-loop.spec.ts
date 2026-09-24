/**
 * 139-table-scrollbar-loop — PR #39: the one table cannot loop on its own
 * scrollbars.
 *
 * Every table in filex (DataTable) lays itself out against the width of its
 * scroll box, and the layout decides whether that box needs scrollbars. Where
 * a scrollbar takes room — Windows, or macOS set to "always show" — that is a
 * loop waiting to close: the table overflows by a pixel, the horizontal bar
 * takes a row's height, the vertical bar comes, the box is a scrollbar
 * narrower, the table fits, both bars go, the box is wide again — every frame.
 * A field report (a 12-file folder that froze the tab on a Windows PC) blamed
 * exactly that.
 *
 * What was measured (2026-09-24, the PR's review): the real table does NOT do
 * it, on v0.42.2 or v0.43.0 — its auto widths round to the pixel the box snaps
 * to, and Chrome raises no bar for less (40 000+ pane sizes, DPR 1/1.25/1.5,
 * zoom, sub-pixel offsets). But a table that is told it has 2px more than it
 * does on alternate width bands — any future width rule with a threshold in
 * it — loops exactly as described, in a pane its rows just fit. So:
 *
 *   1. the probe is proven able to see a loop (reserve switched off),
 *   2. `scrollbar-gutter: stable` on `.fe-list` stops that same table looping,
 *   3. the table as shipped settles,
 *   4. a REAL resize that comes back quickly is followed — the "settler" the
 *      PR also added held it at the narrower width (300px short of its pane)
 *      and was taken back out.
 *
 * ⚠⚠ Real scrollbars. Playwright launches headless Chromium with
 * --hide-scrollbars, which is precisely the switch that makes a scrollbar take
 * no room. Without removing it every assertion here passes vacuously — case 1
 * is what catches that.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { apiLogin, loginAs } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';
import { setAccountViewMode, VIEW_MODE_LS_KEY } from '../helpers/prefs';

test.use({ launchOptions: { ignoreDefaultArgs: ['--hide-scrollbars'] } });

const STORAGE = `e2e-sbloop-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;

async function seedFiles(request: APIRequestContext) {
  await dropStorageByName(request, STORAGE);
  await seedLocalStorage(request, STORAGE, MOUNT);
  await apiLogin(request);
  for (let i = 1; i <= 12; i++) {
    const name = `quarterly-report-${String(i).padStart(2, '0')}.txt`;
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        'file[]': { name, mimeType: 'text/plain', buffer: Buffer.from(`${name}\n`) },
      },
    });
    if (!up.ok()) throw new Error(`upload failed: ${up.status()} ${await up.text()}`);
  }
}

/**
 * Opens the list with a ResizeObserver that counts what it reports for
 * `.fe-list` — and, with `mutant`, lies to the table: 2px more than the box
 * has on every other 10px band of widths. That is a width rule with a
 * threshold in it, the shape a loop needs.
 */
async function openList(page: Page, mutant: boolean) {
  await page.setViewportSize({ width: 1600, height: 1000 });
  await page.addInitScript(
    ({ key, mutant }) => {
      localStorage.setItem('filex.tourDone', '1');
      localStorage.setItem(key, 'list');
      const Orig = window.ResizeObserver;
      (window as unknown as { __fires: number }).__fires = 0;
      window.ResizeObserver = class extends Orig {
        constructor(cb: ResizeObserverCallback) {
          super((entries, obs) => {
            const listEntries = entries.filter((e) => (e.target as HTMLElement).classList?.contains('fe-list'));
            (window as unknown as { __fires: number }).__fires += listEntries.length;
            if (!mutant) return cb(entries, obs);
            const told = entries.map((e) => {
              const w = e.contentRect.width;
              const lie = listEntries.includes(e) && Math.floor(w) % 20 >= 10 ? 2 : 0;
              return { target: e.target, contentRect: { width: w + lie, height: e.contentRect.height } };
            });
            return cb(told as unknown as ResizeObserverEntry[], obs);
          });
        }
      };
    },
    { key: VIEW_MODE_LS_KEY, mutant },
  );
  await loginAs(page);
  await setAccountViewMode(page.request, 'list');
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
  await expect(page.locator('.fe-list .fe-list__row').first()).toBeVisible();
  await page.waitForTimeout(400);
}

/**
 * Sizes `.fe-list` itself through every width in [w1, w2], at the two ends of
 * the band of heights a loop needs — the rows fit while no horizontal bar is
 * showing, and do not once it is — and reports the sizes at which the table
 * did not settle. (Inside that band the height changes nothing: the rows fit
 * or they do not, so its two ends stand for all of it.)
 */
async function loopsIn(page: Page, w1: number, w2: number, stopAtFirst = false) {
  return page.evaluate(
    async ({ w1, w2, stopAtFirst }) => {
      const el = document.querySelector('.fe-list') as HTMLElement;
      el.style.flex = 'none';
      const raf = () => new Promise<void>((r) => requestAnimationFrame(() => r()));
      const frames = async (n: number) => {
        for (let i = 0; i < n; i++) await raf();
      };
      // The rows' own height, and the horizontal bar's thickness.
      el.style.width = '300px';
      el.style.height = '120px';
      await frames(3);
      const content = el.scrollHeight;
      const bar = el.offsetHeight - el.clientHeight;
      const found: string[] = [];
      let tried = 0;
      const heights = [...new Set([content, content + Math.max(bar, 1) - 1])];
      for (let w = w1; w <= w2; w++) {
        for (const h of heights) {
          tried++;
          el.style.width = `${w}px`;
          el.style.height = `${h}px`;
          await frames(3);
          const before = (window as unknown as { __fires: number }).__fires;
          await frames(6);
          const fires = (window as unknown as { __fires: number }).__fires - before;
          if (fires >= 3) {
            found.push(`${w}x${h}: ${fires} observations in 6 frames`);
            if (stopAtFirst) return { tried, bar, content, found };
          }
        }
      }
      return { tried, bar, content, found };
    },
    { w1, w2, stopAtFirst },
  );
}

test.describe('The one table and its own scrollbars', () => {
  test.describe.configure({ timeout: 120_000 });

  test.beforeAll(async ({ request }) => {
    await seedFiles(request);
  });
  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('the probe sees a loop when a width rule can close one and nothing reserves the room', async ({
    page,
  }) => {
    await openList(page, true);
    await page.addStyleTag({ content: '.fe-list { scrollbar-gutter: auto !important; }' });
    const r = await loopsIn(page, 700, 740, true);
    expect(r.bar, 'scrollbars take no room in this browser — every other case here would pass vacuously').toBeGreaterThan(0);
    expect(r.found.length, `no loop found in ${r.tried} sizes; the probe cannot see one`).toBeGreaterThan(0);
  });

  test('the reserved gutter stops that same table looping', async ({ page }) => {
    await openList(page, true);
    const r = await loopsIn(page, 700, 740);
    expect(r.tried).toBeGreaterThan(40);
    expect(r.found, `looped in ${r.found.length} of ${r.tried} sizes`).toEqual([]);
    // …and it is the reserve that does it, on the one scroll box every table uses.
    expect(await page.evaluate(() => getComputedStyle(document.querySelector('.fe-list')!).scrollbarGutter)).toBe(
      'stable',
    );
  });

  test('the table as shipped settles in a pane its rows just fit', async ({ page }) => {
    await openList(page, false);
    // Around the width where the explorer's columns reach their minimums and
    // the table starts to scroll sideways (576px): the one place a width rule
    // changes regime.
    const r = await loopsIn(page, 556, 600);
    expect(r.found, `looped in ${r.found.length} of ${r.tried} sizes`).toEqual([]);
  });

  test('a real resize that comes straight back is followed, not held at the narrower width', async ({
    page,
  }) => {
    await openList(page, false);
    const r = await page.evaluate(async () => {
      const el = document.querySelector('.fe-list') as HTMLElement;
      const head = document.querySelector('.fe-list__head') as HTMLElement;
      el.style.flex = 'none';
      el.style.height = '900px';
      const raf = () => new Promise<void>((res) => requestAnimationFrame(() => res()));
      const wait = (ms: number) => new Promise((res) => setTimeout(res, ms));
      el.style.width = '1000px';
      await wait(600);
      // A panel toggled open and shut: 300px taken and given back within a few frames.
      el.style.width = '700px';
      await raf(); await raf(); await raf();
      el.style.width = '1000px';
      await raf(); await raf(); await raf();
      await wait(900);
      return { pane: el.clientWidth, table: parseFloat(head.style.width) };
    });
    expect(Math.abs(r.table - r.pane), `table ${r.table}px in a ${r.pane}px pane`).toBeLessThanOrEqual(1);
  });
});
