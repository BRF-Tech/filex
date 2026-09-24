/**
 * 108-column-reorder — dragging a header moves the column EXACTLY one place,
 * and the frozen columns hold both edges.
 *
 * Owner, 2026-09-20, from his own session on the list view:
 *   · "bir sütunu tutup bir adım ileri çekince iki adım ilerliyor"
 *   · "ileri çekip, bırakmadan kendi yerine geri getirip bırakınca yine bir
 *      adım ileri gidiyor"
 *   · "geri yönde çekip geri getirdiğinde sütun başlığı sütunun ortasına
 *      düşüyor"
 *   · "yatay kaydırmada ad solda, işlemler sağda kalsın; ikisinin de kendi
 *      gölgesi olsun"
 *
 * The arithmetic itself is pinned by `web/tests/lib/columnReorder.test.ts`
 * (pure `reorderColumns`). What THAT file cannot see is the half that was
 * actually broken: the translation from a pointer position to an index, which
 * needs a real pointer over real boxes. Both failures were an index computed
 * against the drawn columns WITH the dragged one in them and spent against a
 * list WITHOUT it, so a unit test of either list alone stayed green.
 *
 * ⚠ Every assertion here is on the ORDER OF THE HEADER CELLS, which is the
 * thing a person sees. Asserting on the stored document would pass even if the
 * table drew something else.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { apiLogin, loginAs } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';
import { setAccountViewMode, VIEW_MODE_LS_KEY } from '../helpers/prefs';

const STORAGE = `e2e-cols-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;

/**
 * ⚠ The header only exists when there are rows: an empty folder draws the
 * "This folder is empty" card instead, and every case here is about the
 * header. Two files, so a sort has something to do.
 */
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

/** The drawn columns, by the `fe-list__col--<id>` suffix, in header order. */
async function columns(page: Page): Promise<string[]> {
  return page.evaluate(() =>
    [...document.querySelectorAll('.fe-list__head .fe-list__col')].map(
      (c) => (c.className.match(/fe-list__col--(\w+)/) ?? [])[1],
    ),
  );
}

/**
 * A point just inside a header cell's LEADING edge — i.e. the gap in front of
 * it, named by the column that follows it rather than by a pixel offset from
 * the one before.
 */
async function edgeOf(page: Page, col: string): Promise<{ x: number; y: number }> {
  return page.evaluate((c) => {
    const el = document.querySelector(`.fe-list__head .fe-list__col--${c}`)!;
    const r = el.getBoundingClientRect();
    return { x: r.left + 6, y: r.top + r.height / 2 };
  }, col);
}

/** The centre of a header cell, in viewport coordinates. */
async function centreOf(page: Page, col: string): Promise<{ x: number; y: number }> {
  return page.evaluate((c) => {
    const el = document.querySelector(`.fe-list__head .fe-list__col--${c}`)!;
    const r = el.getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
  }, col);
}

/** Which cell currently carries the drop marker, and on which side. */
async function marker(page: Page): Promise<string[]> {
  return page.evaluate(() =>
    [...document.querySelectorAll('.fe-list__head .fe-list__col')]
      .filter((c) => /is-drop-/.test(c.className))
      .map(
        (c) =>
          `${(c.className.match(/fe-list__col--(\w+)/) ?? [])[1]}:${
            c.className.includes('is-drop-after') ? 'after' : 'before'
          }`,
      ),
  );
}

/**
 * How far each header label sits from its own cell's left edge.
 *
 * ⚠ The third complaint was about this and nothing else: after an abandoned
 * drag the label drifted to the middle of its column and then to the far left.
 * Zero for every label is what "the header went back to its own alignment"
 * means, and it is checked after every gesture that should change nothing.
 */
async function labelOffsets(page: Page): Promise<Record<string, number>> {
  return page.evaluate(() => {
    const out: Record<string, number> = {};
    for (const c of document.querySelectorAll('.fe-list__head .fe-list__col')) {
      const label = c.querySelector('.fe-list__sort, .fe-list__head-label');
      if (!label) continue;
      const id = (c.className.match(/fe-list__col--(\w+)/) ?? [])[1];
      out[id] = Math.round(label.getBoundingClientRect().left - c.getBoundingClientRect().left);
    }
    return out;
  });
}

/** Press on `col`, travel to `to`, and release — one complete gesture. */
async function dragHeader(
  page: Page,
  col: string,
  to: { x: number; y: number },
  opts: { via?: { x: number; y: number }; cancel?: boolean; expectMarker?: number } = {},
) {
  const from = await centreOf(page, col);
  await page.mouse.move(from.x, from.y);
  await page.mouse.down();
  // Past DRAG_SLOP first, or the gesture stays a sort click.
  await page.mouse.move(from.x + 20, from.y, { steps: 4 });
  /* ⚠ Proved, not assumed: below the slop the gesture is still a SORT CLICK,
     and a test whose drag never armed reports "the column did not move" —
     which is exactly what a broken reorder reports too.

     ⚠ A RETRYING expectation, not a bare `count()`. The class is applied by
     Vue on the tick after the pointermove handler runs; a single read at the
     wrong microtask is a test that fails for a reason that has nothing to do
     with what it measures (measured: 6 of 7 cases red on one run, 0 on the
     next, same build). */
  await expect(
    page.locator('.fe-list__col.is-col-dragging'),
    `the drag on ${col} never armed`,
  ).toHaveCount(1);
  if (opts.via) await page.mouse.move(opts.via.x, opts.via.y, { steps: 8 });
  await page.mouse.move(to.x, to.y, { steps: 10 });
  // Same reason: let the marker settle before reading it.
  await expect
    .poll(() => marker(page), { timeout: 2000 })
    .toHaveLength(opts.expectMarker ?? 1);
  const seen = await marker(page);
  if (opts.cancel) await page.keyboard.press('Escape');
  await page.mouse.up();
  await page.waitForTimeout(250);
  return seen;
}

async function openList(page: Page) {
  // ⚠ The view mode is set through the explorer's OWN storage key rather than
  // by clicking the toolbar's view control: that control collapses into an
  // overflow menu at narrow widths and its accessible name is a translated
  // string, so a click on it is two things that can change under this spec
  // without the columns changing at all.
  await page.addInitScript((key) => {
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem(key, 'list');
  }, VIEW_MODE_LS_KEY);
  await loginAs(page);
  /* ⚠⚠ The storage key is a FIRST-PAINT CACHE, not the preference: the view
     mode lives on the ACCOUNT now (`packages/core/src/lib/viewPrefs.ts`), and
     the account's answer overwrites the seeded one a moment after boot. So the
     preference is written where it actually lives — otherwise this spec
     silently depends on whatever the admin account last looked at, and
     `102-touch-tap-opens` is the proof that it eventually disagrees. */
  await setAccountViewMode(page.request, 'list');
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
  await expect(page.getByTestId(`sidenav-storage-${STORAGE}`)).toBeVisible();
  /* The toolbar control stays as a belt-and-braces fallback: it costs one
     `isVisible` when the header is already there. */
  const head = page.locator('.fe-list__head');
  if (!(await head.isVisible().catch(() => false))) {
    await page.evaluate(() => {
      const b = [...document.querySelectorAll('button')].find((x) =>
        /^(list|liste)$/i.test((x.getAttribute('title') || x.getAttribute('aria-label') || '').trim()),
      );
      (b as HTMLButtonElement | undefined)?.click();
    });
  }
  await expect(head).toBeVisible();
  // The shipped column order — every case below is written against
  // `name type owner modified size ★`, and the cases themselves reorder it.
  //
  // ⚠ "Reset columns" is GATED on something having been customised, so on the
  // first case of a run it does not exist at all and waiting for it would time
  // out on a table that was already in the right order. Clicked when offered,
  // skipped when not.
  await page.locator('.fe-list__colmenu-btn').click();
  const reset = page.getByRole('button', { name: /reset columns|sütunları sıfırla/i });
  if (await reset.isVisible().catch(() => false)) await reset.click();
  await page.keyboard.press('Escape');
  /* ⚠⚠ WAIT FOR THE BACKDROP TO GO. The column menu is a full-screen overlay
     (`.fe-colmenu__backdrop`), and while it is up every pointerdown lands on
     IT rather than on the header underneath — so the first drag of a run
     silently never armed and the case failed as "the column did not move",
     which is also what a genuinely broken reorder looks like. Measured
     2026-09-20: 6 of 7 cases red on one run and 0 on the next, same build. */
  await expect(page.locator('.fe-colmenu__backdrop')).toHaveCount(0);
  await page.waitForTimeout(300);
  expect(await columns(page), 'the run did not start from the shipped order').toEqual([
    'check',
    'name',
    'type',
    'owner',
    'mod',
    'size',
    'star',
    'menu',
  ]);
}

test.describe('List view — moving a column', () => {
  test.beforeAll(async ({ request }) => {
    await seedFiles(request);
  });
  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('one step forward moves ONE step, and the marker promised that gap', async ({ page }) => {
    await openList(page);
    const before = await columns(page);
    const type = before.indexOf('type');
    expect(type, 'the shipped order starts with Type').toBeGreaterThan(0);

    /* ⚠ Aimed at the LEADING EDGE of the column two places along, not at
       "just past the midpoint of the next one": the midpoint depends on that
       column's width, which is derived from the pane and differs between
       window sizes, so a few pixels either way silently turns the gesture
       into a drop on the column's own place — which correctly does nothing
       and reads as a broken test. An edge is unambiguous. */
    const target = await edgeOf(page, before[type + 2]);
    const seen = await dragHeader(page, 'type', target);

    const after = await columns(page);
    // The marker named the gap it landed in — the picture and the drop agree.
    expect(after.indexOf('type'), 'Type moved exactly one place').toBe(type + 1);
    expect(seen, `exactly one drop marker (order after: ${after.join(' ')})`).toHaveLength(1);
    expect(after.filter((c) => c !== 'type')).toEqual(before.filter((c) => c !== 'type'));
  });

  test('one step back moves ONE step', async ({ page }) => {
    await openList(page);
    const before = await columns(page);
    // Start from the second movable column so there is somewhere to go.
    const mod = before.indexOf('mod');
    // The leading edge of the column before it = the gap in front of that one.
    await dragHeader(page, 'mod', await edgeOf(page, before[mod - 1]));

    const after = await columns(page);
    expect(after.indexOf('mod'), 'Modified moved exactly one place back').toBe(mod - 1);
    expect(after.filter((c) => c !== 'mod')).toEqual(before.filter((c) => c !== 'mod'));
  });

  test('dropped back on its own place, NOTHING changes — and the labels stay put', async ({ page }) => {
    await openList(page);
    const before = await columns(page);
    const labelsBefore = await labelOffsets(page);
    const home = await centreOf(page, 'type');

    // Out to the right, then home again, then release. Twice: the owner's
    // report was that repeating it walked the column further each time.
    for (let i = 0; i < 2; i++) {
      const seen = await dragHeader(page, 'type', home, {
        via: { x: home.x + 120, y: home.y },
        // Its own place: the marker is deliberately ABSENT, which is the whole
        // point of the case.
        expectMarker: 0,
      });
      expect(seen, 'no marker over its own place').toHaveLength(0);
      expect(await columns(page), `round ${i + 1}: the order changed`).toEqual(before);
    }
    expect(await labelOffsets(page), 'a header label drifted inside its cell').toEqual(labelsBefore);
    expect(await page.locator('.is-col-dragging').count(), 'the drag ghost outlived the drag').toBe(0);
  });

  test('a column can never be dropped past the star', async ({ page }) => {
    await openList(page);
    const star = await page.evaluate(() => {
      const el = document.querySelector('.fe-list__head .fe-list__col--star')!;
      const r = el.getBoundingClientRect();
      return { x: r.right + 60, y: r.top + r.height / 2 };
    });
    await dragHeader(page, 'type', star);

    const after = await columns(page);
    // ★ is a control that lives beside the row menu; nothing may pass it.
    expect(after[after.length - 1]).toBe('menu');
    expect(after[after.length - 2]).toBe('star');
    expect(after.indexOf('type'), 'Type came to rest just before the star').toBe(after.length - 3);
  });

  test('Escape abandons the drag and leaves no state behind', async ({ page }) => {
    await openList(page);
    const before = await columns(page);
    const labelsBefore = await labelOffsets(page);
    const size = await centreOf(page, 'size');
    await dragHeader(page, 'type', { x: size.x + 8, y: size.y }, { cancel: true });

    expect(await columns(page), 'Escape still moved the column').toEqual(before);
    expect(await marker(page), 'a drop marker survived Escape').toHaveLength(0);
    expect(await page.locator('.is-col-dragging').count()).toBe(0);
    expect(await labelOffsets(page)).toEqual(labelsBefore);
  });

  test('Ctrl+Arrow moves the focused column, one place per press', async ({ page }) => {
    await openList(page);
    const before = await columns(page);
    const at = before.indexOf('type');

    await page.locator('.fe-list__head .fe-list__col--type .fe-list__sort').focus();
    await page.keyboard.press('Control+ArrowRight');
    await page.waitForTimeout(250);
    expect(await columns(page).then((c) => c.indexOf('type'))).toBe(at + 1);

    await page.locator('.fe-list__head .fe-list__col--type .fe-list__sort').focus();
    await page.keyboard.press('Control+ArrowLeft');
    await page.waitForTimeout(250);
    expect(await columns(page)).toEqual(before);
  });
});

test.describe('List view — the frozen edges', () => {
  test.beforeAll(async ({ request }) => {
    await seedFiles(request);
  });
  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('scrolled sideways, Name stays left and the ⋮ stays right, each with its own edge', async ({
    page,
  }) => {
    await openList(page);

    /* ⚠⚠ MAKE THE TABLE OVERFLOW, and narrowing the window is not how. Until
       somebody sizes a column the widths are derived from the pane
       (`tableLayout`, auto) — so the table always fits, at every window size,
       and a test that shrank the viewport would measure a table that had
       quietly shrunk with it. Dragging one handle commits every drawn width
       (`freezeWidths`), and from then on the pane has no opinion: widen Name
       and the table really is wider than its pane.

       ⚠ Widened by 240px rather than by half the pane: the frozen lead is
       conditional on leaving room to scroll (a sticky cell wider than its pane
       would cover the pane), so a Name dragged to the moon would correctly
       turn the pinning OFF and this case would fail for the right reason. */
    const handle = page.locator('.fe-list__head .fe-list__col--name .fe-list__resize');
    const box = (await handle.boundingBox())!;
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width / 2 + 240, box.y + box.height / 2, { steps: 12 });
    await page.mouse.up();
    await page.waitForTimeout(500);

    const list = page.locator('.fe-list');
    await expect(list).toHaveClass(/is-pin-lead/);
    const overflows = await page.evaluate(() => {
      const l = document.querySelector('.fe-list')!;
      return l.scrollWidth > l.clientWidth;
    });
    expect(overflows, 'the table has to overflow for any of this to mean anything').toBe(true);

    await page.evaluate(() => {
      const l = document.querySelector('.fe-list')!;
      // All the way: the point is that something really passes UNDER the
      // frozen lead, and a nudge may not move any column that far.
      l.scrollLeft = l.scrollWidth;
      l.dispatchEvent(new Event('scroll'));
    });
    await page.waitForTimeout(400);

    const m = await page.evaluate(() => {
      const l = document.querySelector('.fe-list')!;
      const lb = l.getBoundingClientRect();
      const cell = (id: string) => {
        const el = document.querySelector(`.fe-list__head .fe-list__col--${id}`)!;
        const cs = getComputedStyle(el);
        const r = el.getBoundingClientRect();
        return { pos: cs.position, shadow: cs.boxShadow, left: r.left, right: r.right };
      };
      return {
        listLeft: lb.left,
        listRight: lb.right,
        scrolled: l.classList.contains('is-scrolled-x'),
        check: cell('check'),
        name: cell('name'),
        menu: cell('menu'),
        mid: cell('type'),
      };
    });

    expect(m.scrolled).toBe(true);
    expect(m.check.pos).toBe('sticky');
    expect(m.name.pos).toBe('sticky');
    expect(m.menu.pos).toBe('sticky');
    // The tick rides the pane's left edge and Name sits right after it.
    expect(Math.round(m.check.left)).toBe(Math.round(m.listLeft));
    expect(m.name.left).toBeGreaterThan(m.check.left);
    expect(m.name.left).toBeLessThan(m.check.right + 12);
    // The ⋮ rides the right edge.
    expect(Math.round(m.menu.right)).toBe(Math.round(m.listRight));
    // Each frozen edge draws its own divider, and only while scrolled.
    expect(m.name.shadow, 'Name has no trailing edge').not.toBe('none');
    expect(m.menu.shadow, 'the ⋮ has no leading edge').not.toBe('none');
    // A column in between really is sliding UNDER the frozen lead: Type is
    // the first column after Name, so it is the first to go under it.
    expect(m.mid.left, 'nothing slid under the frozen Name column').toBeLessThan(m.name.right);
  });
});
