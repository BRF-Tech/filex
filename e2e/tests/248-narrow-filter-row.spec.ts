/**
 * 248-narrow-filter-row — the explorer on a phone: the end of the filter row,
 * the stacked header, and the dash beside the ⋮ (tasks #264 and #249).
 *
 * The owner, 2026-10-15, at 340px (Turkish, dark):
 *   · at the end of the chip row "an empty box: a stray divider and a ⋮". It
 *     was the listing's own ⋮, pinned (sticky) over a row that scrolled, at
 *     rest on top of the "Size" chip: the chip's start edge and the stem of
 *     its first letter on one side, its caret on the other, through the row's
 *     end padding a sticky box does not cover. The ⋮ is now OUTSIDE the
 *     scrolling track;
 *   · under it "Name ↑ Type Owner Modified Size ⋮" ran like plain text over
 *     rows whose facts read "Type Folder · Owner System" - "neither a header
 *     nor a row". Stacked, the header is a band that leads with "Sort by";
 *   · (#249) a "-" beside the ⋮ of a focused or hovered FOLDER row: the star
 *     column's slot draws nothing on a folder, Vue then draws the slot's
 *     fallback, and the fallback was the facts' "no value" dash. A control
 *     column draws nothing now.
 *
 * Every case is PAINT, so every case measures boxes in a browser. The markup
 * and the reading of the scroll are unit-tested in
 * web/tests/components/filterBarTrack.test.ts and tableStacked.test.ts.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { apiLogin, ADMIN_EMAIL, ADMIN_PASSWORD, dismissInstallBanner } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';
import { setAccountViewMode, VIEW_MODE_LS_KEY } from '../helpers/prefs';

const STORAGE = `e2e-narrowbar-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;

async function seed(request: APIRequestContext) {
  await dropStorageByName(request, STORAGE);
  await seedLocalStorage(request, STORAGE, MOUNT);
  await apiLogin(request);
  for (const name of ['Contracts', 'Photos']) {
    const mk = await request.post('/api/files/manager?action=newfolder', { data: { path: `${STORAGE}://`, name } });
    if (!mk.ok()) throw new Error(`newfolder ${name}: ${mk.status()} ${await mk.text()}`);
  }
  const up = await request.post('/api/files/manager?action=upload', {
    multipart: {
      path: `${STORAGE}://`,
      'file[]': { name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('notes\n') },
    },
  });
  if (!up.ok()) throw new Error(`upload: ${up.status()} ${await up.text()}`);
}

/**
 * Signed in through the page's own cookie jar, and the explorer is the first
 * page the tab opens (filex lessons #1541, #1508): WebKit drops its network
 * process when a filex page is left with requests in flight.
 */
async function openDrive(page: Page, size: { width: number; height: number }) {
  await page.setViewportSize(size);
  await dismissInstallBanner(page);
  await page.addInitScript((key) => {
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem(key, 'list');
  }, VIEW_MODE_LS_KEY);
  const res = await page.request.post('/api/auth/login', { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
  expect(res.ok(), `sign-in: ${res.status()}`).toBeTruthy();
  await setAccountViewMode(page.request, 'list');
  const listing = page.waitForResponse((r) => r.url().includes('/api/files/manager') && r.url().includes('index'));
  await page.goto(`/drive/explore?path=${encodeURIComponent(`${STORAGE}://`)}`);
  await listing;
  await expect(page.getByTestId('filterbar')).toBeVisible();
  await expect(page.locator('.fe-list__row').first()).toBeVisible();
  // The table and the track decide one frame after their observers answer.
  await page.waitForTimeout(400);
}

test.beforeAll(async ({ request }) => {
  await seed(request);
});
test.afterAll(async ({ request }) => {
  await dropStorageByName(request, STORAGE);
});

for (const width of [340, 390]) {
  test.describe(`at ${width}px`, () => {
    test('the ⋮ ends the filter row on its own: nothing under it, nothing beside it', async ({ page }) => {
      await openDrive(page, { width, height: 800 });
      const m = await page.evaluate(() => {
        const row = document.querySelector<HTMLElement>('[data-testid="filterbar"]')!;
        const track = row.querySelector<HTMLElement>('[data-testid="filterbar-track"]')!;
        const dots = row.querySelector<HTMLElement>('[data-testid="listing-actions"]')!;
        const r = dots.getBoundingClientRect();
        const rowBox = row.getBoundingClientRect();
        const cy = r.top + r.height / 2;
        const at = (x: number) => document.elementFromPoint(x, cy);
        const chipAt = (x: number) => !!at(x)?.closest('.fe-filterbar__chip, .fe-filterbar__find, .fe-filterbar__sort');
        return {
          inTrack: track.contains(dots),
          sticky: getComputedStyle(dots).position,
          left: r.left,
          right: r.right,
          view: window.innerWidth,
          rowRight: rowBox.right,
          overflows: track.scrollWidth > track.clientWidth + 1,
          moreEnd: track.classList.contains('is-more-end'),
          // What is ON the ⋮, and what is in the strip between it and the
          // row's end edge (where the old row showed a chip's caret).
          onDots: !!at(r.left + r.width / 2)?.closest('[data-testid="listing-actions"]'),
          afterDots: [r.right + 1, (r.right + rowBox.right) / 2, rowBox.right - 1].filter((x) => x > r.right).map(chipAt),
          // The track stops before the ⋮ starts.
          trackEnd: track.getBoundingClientRect().right,
        };
      });
      expect(m.inTrack, 'the ⋮ is inside the scrolling track').toBe(false);
      expect(m.sticky).not.toBe('sticky');
      expect(m.left).toBeGreaterThanOrEqual(0);
      expect(m.right).toBeLessThanOrEqual(m.view);
      expect(m.onDots, 'something is painted over the ⋮').toBe(true);
      expect(m.afterDots, 'a chip shows past the ⋮').toEqual(m.afterDots.map(() => false));
      expect(m.trackEnd, 'the track runs under the ⋮').toBeLessThanOrEqual(m.left + 0.5);
      // Four chips, the name box and the sort do not fit a phone: the track
      // scrolls, and says so at its end.
      expect(m.overflows).toBe(true);
      expect(m.moreEnd).toBe(true);
    });

    test('scrolled to its end, the track fades at its start instead', async ({ page }) => {
      await openDrive(page, { width, height: 800 });
      const track = page.getByTestId('filterbar-track');
      await track.evaluate((el) => {
        el.scrollLeft = el.scrollWidth;
        el.dispatchEvent(new Event('scroll'));
      });
      await expect(track).toHaveClass(/is-more-start/);
      await expect(track).not.toHaveClass(/is-more-end/);
      // The sort control and the name box are reachable at the end of it.
      const sortBox = await page.getByTestId('sort-dir').boundingBox();
      const dotsBox = await page.getByTestId('listing-actions').boundingBox();
      expect(sortBox && dotsBox && sortBox.x + sortBox.width <= dotsBox.x + 0.5).toBe(true);
    });

    test('stacked, the header is a band that says it sorts', async ({ page }) => {
      await openDrive(page, { width, height: 800 });
      await expect(page.locator('.fe-list.is-stacked')).toHaveCount(1);
      const h = await page.evaluate(() => {
        const head = document.querySelector<HTMLElement>('.fe-list.is-stacked .fe-list__head')!;
        const row = document.querySelector<HTMLElement>('.fe-list.is-stacked .fe-list__row')!;
        const hb = head.getBoundingClientRect();
        const lead = head.querySelector<HTMLElement>('.fe-list__col--lead .fe-list__sort')!.getBoundingClientRect();
        const buttons = [...head.querySelectorAll<HTMLElement>('.fe-list__sort')];
        const rtl = getComputedStyle(head).direction === 'rtl';
        const plain = [...head.querySelectorAll<HTMLElement>('.fe-list__head-label')].filter((l) => {
          const b = l.getBoundingClientRect();
          return b.width > 1 && b.height > 1;
        });
        return {
          label: head.dataset.sortLabel ?? '',
          headBg: getComputedStyle(head).backgroundColor,
          rowBg: getComputedStyle(row).backgroundColor,
          // "Sort by" is painted: the first key starts that far in.
          labelRoom: Math.round(rtl ? hb.right - lead.right : lead.left - hb.left),
          bordered: buttons.every((b) => parseFloat(getComputedStyle(b).borderTopWidth) >= 1),
          activeFilled: buttons.some((b) => {
            const sorted = b.parentElement?.getAttribute('aria-sort');
            return (sorted === 'ascending' || sorted === 'descending') && getComputedStyle(b).backgroundColor !== 'rgba(0, 0, 0, 0)';
          }),
          plainWords: plain.map((l) => l.textContent?.trim()),
          height: hb.height,
        };
      });
      expect(h.label).toBe('Sort by');
      expect(h.headBg, 'the header has no ground of its own').not.toBe(h.rowBg);
      expect(h.labelRoom, '"Sort by" is not painted before the keys').toBeGreaterThan(30);
      expect(h.bordered, 'a sort key that does not look like a button').toBe(true);
      expect(h.activeFilled, 'the active key is not marked').toBe(true);
      // A column that cannot sort leaves the band (its header stays for a
      // screen reader): no plain word between the buttons.
      expect(h.plainWords).toEqual([]);
      // English fits one line at a phone's width.
      expect(h.height).toBeLessThanOrEqual(44);
    });

    test('a folder row says nothing beside its ⋮, focused or not (#249)', async ({ page }) => {
      await openDrive(page, { width, height: 800 });
      const folder = page.locator('.fe-list__row.is-dir').first();
      await folder.locator('.fe-list__col--check').click();
      await folder.locator('.fe-list__col--check').click();
      const m = await folder.evaluate((row) => {
        const star = row.querySelector<HTMLElement>('.fe-list__col--star')!;
        const menu = row.querySelector<HTMLElement>('.fe-list__col--menu')!;
        return { star: (star.textContent ?? '').trim(), menuBg: getComputedStyle(menu).backgroundColor };
      });
      expect(m.star, 'the star column drew a dash on a folder').toBe('');
      // Not frozen, the ⋮ has no ground of its own to show as a box.
      expect(m.menuBg).toBe('rgba(0, 0, 0, 0)');
    });
  });
}

test('wide: hovering a folder row shows no dash where the star would be (#249)', async ({ page }) => {
  await openDrive(page, { width: 1280, height: 800 });
  const folder = page.locator('.fe-list__row.is-dir').first();
  await folder.hover();
  const star = folder.locator('.fe-list__col--star');
  await expect(star).toHaveCSS('opacity', '1');
  expect(((await star.textContent()) ?? '').trim()).toBe('');
});
