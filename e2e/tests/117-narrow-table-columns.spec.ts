/**
 * 117-narrow-table-columns — THE table in a pane too narrow for it: what the
 * person is shown FIRST.
 *
 * The owner's rule is that no column is ever shed for want of room ("no room
 * demesin, kenara devam eden bir scroll getirsin"): the table stays whole and
 * scrolls sideways. That rule says what the table CONTAINS. It does not say
 * what is painted before anybody scrolls, and v0.43.0 shipped two ways of
 * getting that wrong in the same table:
 *
 *   1. the lead stopped shrinking at `leadAuto` — a DESKTOP default of 240px
 *      — so in a 265px panel it was the whole pane and every other column
 *      started past the right edge;
 *   2. the trailing `Actions` control is `position: sticky` with an opaque
 *      ground, so at 104px of that same pane it covered what little was left.
 *
 * Together they turned an app's signer list into names, buttons, and a blank
 * gap between them, while the documentation beside it promised "Waiting /
 * Invited / Opened it / Signed / Refused" on every row.
 *
 * ⚠⚠ Why a browser: both halves are PAINT. The arithmetic is unit-tested
 * (web/tests/components/tableNarrowPane.test.ts) and was green throughout —
 * a cell that is in the DOM, has a width, and sits under a sticky cell is
 * invisible to every assertion except a measured one. So every case here
 * measures boxes against the scrollport and against the pinned cell.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { apiLogin, loginAs } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';
import { setAccountViewMode, VIEW_MODE_LS_KEY } from '../helpers/prefs';

const STORAGE = `e2e-narrow-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;

/** A phone, and a narrow embed of one. */
const PHONE = { width: 390, height: 844 };
const TIGHT = { width: 320, height: 720 };

async function seedFiles(request: APIRequestContext) {
  await dropStorageByName(request, STORAGE);
  await seedLocalStorage(request, STORAGE, MOUNT);
  await apiLogin(request);
  for (const name of ['alpha.txt', 'beta.txt']) {
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        'file[]': { name, mimeType: 'text/plain', buffer: Buffer.from(`${name}\n`) },
      },
    });
    if (!up.ok()) throw new Error(`upload failed: ${up.status()} ${await up.text()}`);
  }
}

async function openList(page: Page, size: { width: number; height: number }) {
  await page.setViewportSize(size);
  await page.addInitScript((key) => {
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem(key, 'list');
  }, VIEW_MODE_LS_KEY);
  await loginAs(page);
  await setAccountViewMode(page.request, 'list');
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
  await expect(page.locator('.fe-list__head')).toBeVisible();
  await expect(page.locator('.fe-list__row').first()).toBeVisible();
  // The widths settle one frame after the ResizeObserver's first answer.
  await page.waitForTimeout(400);
}

/**
 * What the person can actually SEE of each column, in the row (not the
 * header): the width of its box after clipping by the scrollport and by the
 * frozen control that paints over it.
 *
 * ⚠ The pinned cell is subtracted only while it IS pinned — that is the whole
 * of the second half of this fix, and reading `position` rather than assuming
 * it keeps the measurement honest either way.
 */
async function visibleWidths(page: Page): Promise<Record<string, number>> {
  return page.evaluate(() => {
    const list = document.querySelector('.fe-list')!;
    const port = list.getBoundingClientRect();
    const menuEl = document.querySelector('.fe-list__row .fe-list__col--menu');
    const menu = menuEl?.getBoundingClientRect();
    const pinned = menuEl ? getComputedStyle(menuEl).position === 'sticky' : false;
    const rightEdge = pinned && menu ? Math.min(port.right, menu.left) : port.right;
    const out: Record<string, number> = {};
    for (const cell of document.querySelectorAll('.fe-list__row:first-child .fe-list__col')) {
      const id = (cell.className.match(/fe-list__col--(\w+)/) ?? [])[1];
      if (!id || id === 'menu') continue;
      const r = cell.getBoundingClientRect();
      out[id] = Math.round(Math.max(0, Math.min(r.right, rightEdge) - Math.max(r.left, port.left)));
    }
    return out;
  });
}

/** The drawn columns, header order — nothing here may ever shed one. */
async function columns(page: Page): Promise<string[]> {
  return page.evaluate(() =>
    [...document.querySelectorAll('.fe-list__head .fe-list__col')].map(
      (c) => (c.className.match(/fe-list__col--(\w+)/) ?? [])[1],
    ),
  );
}

test.describe('The one table in a narrow pane', () => {
  test.beforeAll(async ({ request }) => {
    await seedFiles(request);
  });
  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('on a phone the lead gives way, and the column after it is READ, not merely present', async ({
    page,
  }) => {
    await openList(page, PHONE);

    // Nothing is shed: the rule the owner set is untouched.
    expect(await columns(page)).toEqual(['check', 'name', 'type', 'owner', 'mod', 'size', 'star', 'menu']);
    const overflows = await page.evaluate(() => {
      const l = document.querySelector('.fe-list')!;
      return l.scrollWidth > l.clientWidth;
    });
    expect(overflows, 'a phone cannot hold this table — it is meant to scroll').toBe(true);

    const seen = await visibleWidths(page);
    /* The lead at its own minimum rather than at its desktop width: 240 of a
       390px pane leaves nothing for anything else, which is what shipped. */
    expect(seen.name, `Name took the whole pane (${JSON.stringify(seen)})`).toBeLessThanOrEqual(160);
    expect(seen.name).toBeGreaterThan(60);
    /* …and the next column is genuinely painted. 24px is deliberately low:
       this is "the person can see there is a Type column and read some of
       it", not a claim about how much fits. */
    expect(seen.type, `Type is not visible (${JSON.stringify(seen)})`).toBeGreaterThanOrEqual(24);
  });

  test('a control that would cover the pane is not frozen to it', async ({ page }) => {
    await openList(page, TIGHT);

    const list = page.locator('.fe-list');
    // Below the threshold nothing is pinned and the row scrolls as one piece.
    await expect(list).not.toHaveClass(/is-pin-menu/);
    const position = await page.evaluate(
      () => getComputedStyle(document.querySelector('.fe-list__row .fe-list__col--menu')!).position,
    );
    expect(position, 'the ⋮ is still sticky over a pane it covers').not.toBe('sticky');

    // Nothing is shed here either, and the first columns are readable.
    expect(await columns(page)).toContain('type');
    const seen = await visibleWidths(page);
    expect(seen.type, `Type is not visible (${JSON.stringify(seen)})`).toBeGreaterThanOrEqual(24);
  });

  test('a pane with room keeps the frozen control it always had', async ({ page }) => {
    await openList(page, { width: 1280, height: 800 });

    await expect(page.locator('.fe-list')).toHaveClass(/is-pin-menu/);
    const position = await page.evaluate(
      () => getComputedStyle(document.querySelector('.fe-list__row .fe-list__col--menu')!).position,
    );
    expect(position, 'the ⋮ stopped riding the edge on a wide table').toBe('sticky');
  });
});
