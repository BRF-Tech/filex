/**
 * 117-narrow-table-columns — THE table in a pane too narrow for it.
 *
 * Two rules meet here, and this spec holds both:
 *
 *   · the owner's, from the start: no column is ever shed for want of room
 *     ("no room demesin, kenara devam eden bir scroll getirsin"). Every
 *     column the person chose is still drawn, at every width;
 *   · and the owner's, 2026-10-11 (the e-Signature details panel, whose
 *     signer table read "Invited ..." and "until S..." in 300px): where the
 *     table cannot be a table, its cells are laid out under the lead, every
 *     narrow table at once - and COMPACT, the way a phone's file manager
 *     draws a row. Below `TableLayout.stackBelow` (the lead and the first
 *     three columns side by side at their minimums, lib/tableColumns) each
 *     row is its tick, its name and its ⋮ on one line, and every other cell
 *     after them on one line that wraps only when it must, each with its
 *     column's name small in front of it and " · " between them: "Type
 *     Plain text · Owner You · Modified ... · Size 10 B", whole. Above it
 *     the table is a table, scrolling sideways to the rest, with its frozen
 *     edges.
 *
 * v0.43.0 shipped this pane as names, buttons and a blank gap; 0.55 as two
 * 56px columns with an ellipsis each. Both were PAINT: the cells were in the
 * DOM with a width, and invisible or cut. So every case measures boxes and
 * text in a browser (the arithmetic and the markup are unit-tested in
 * web/tests/components/tableStacked.test.ts and tableNarrowPane.test.ts).
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { apiLogin, ADMIN_EMAIL, ADMIN_PASSWORD, dismissInstallBanner } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';
import { setAccountViewMode, VIEW_MODE_LS_KEY } from '../helpers/prefs';

const STORAGE = `e2e-narrow-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;

/** A phone, and a pane that holds Name and three columns but not all of them.
 *  ⚠ Under 560px of explorer, so the side panel is a drawer and the listing
 *  has the whole width (FileExplorer); at 560 the panel takes a column. */
const PHONE = { width: 390, height: 844 };
const MIDDLE = { width: 540, height: 800 };

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

/**
 * Signed in through the page's own cookie jar, and the page under test is the
 * first one the tab opens: WebKit drops its network process when a filex page
 * is left with requests still in flight (filex lesson #1541), and a login form
 * followed by a jump to the explorer is exactly that.
 */
async function signIn(page: Page, size: { width: number; height: number }) {
  await page.setViewportSize(size);
  await dismissInstallBanner(page);
  await page.addInitScript((key) => {
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem(key, 'list');
  }, VIEW_MODE_LS_KEY);
  const res = await page.request.post('/api/auth/login', { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
  expect(res.ok(), `sign-in: ${res.status()}`).toBeTruthy();
}

async function openList(page: Page, size: { width: number; height: number }) {
  await signIn(page, size);
  await setAccountViewMode(page.request, 'list');
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
  await expect(page.locator('.fe-list__head')).toBeVisible();
  await expect(page.locator('.fe-list__row').first()).toBeVisible();
  // The table decides one frame after its ResizeObserver's first answer.
  await page.waitForTimeout(400);
}

/** The drawn columns, header order — nothing here may ever shed one. */
async function columns(page: Page): Promise<string[]> {
  return page.evaluate(() =>
    [...document.querySelectorAll('.fe-list__head .fe-list__col')].map(
      (c) => (c.className.match(/fe-list__col--(\w+)/) ?? [])[1],
    ),
  );
}

/**
 * One stacked row, measured: where each fact is against the row's first line
 * and the scrollport, the label and the " · " the stylesheet draws for it,
 * whether its words are cut, and how the facts flow - in reading order, from
 * under the lead's start, on how many lines.
 */
async function measureRow(page: Page, rowSel: string, listSel = '.fe-list') {
  return page.evaluate(
    ({ rowSel, listSel }) => {
      const list = document.querySelector(listSel)!;
      const row = list.querySelector(rowSel)!;
      const port = list.getBoundingClientRect();
      const lead = row.querySelector('.fe-list__col--lead')!.getBoundingClientRect();
      const rtl = getComputedStyle(list).direction === 'rtl';
      /* Inline start and end, whichever physical side they are. */
      const start = (b: DOMRect) => (rtl ? -b.right : b.left);
      const end = (b: DOMRect) => (rtl ? -b.left : b.right);
      const cells: Record<
        string,
        { label: string; labelRoom: number; text: string; cut: boolean; inside: boolean; under: boolean; sep: boolean; last: boolean }
      > = {};
      const boxes: DOMRect[] = [];
      for (const cell of row.querySelectorAll<HTMLElement>('.is-stack-fact')) {
        const id = cell.dataset.col ?? '?';
        const r = cell.getBoundingClientRect();
        const value = cell.querySelector<HTMLElement>('.fe-list__stack-value');
        const v = value?.getBoundingClientRect();
        const words = cell.querySelector<HTMLElement>('.fe-list__cell-text') ?? value;
        cells[id] = {
          // The label is drawn from `data-label` by the stylesheet; that it
          // is PAINTED is read off the room it takes before the value (an
          // engine may serialise `content: attr()` unresolved).
          label: cell.dataset.label ?? '',
          labelRoom: v ? Math.round(rtl ? r.right - v.right : v.left - r.left) : -1,
          text: (cell.textContent ?? '').trim(),
          cut: !!words && words.scrollWidth > words.clientWidth + 1,
          inside: r.left >= port.left - 1 && r.right <= port.right + 1,
          under: r.top >= lead.bottom - 1,
          // The " · " after it: `content` serialises as `"·" / ""` where the
          // alternative text is known, `none` on the last fact.
          sep: getComputedStyle(cell, '::after').content.includes('\u00b7'),
          last: cell.classList.contains('is-stack-last'),
        };
        boxes.push(r);
      }
      /* The flow: each fact after the one before it on the same line, or on a
         line below it. */
      let inOrder = true;
      for (let i = 1; i < boxes.length; i++) {
        const a = boxes[i - 1];
        const b = boxes[i];
        const oneLine = Math.abs(a.top - b.top) < 4;
        if (oneLine ? start(b) < end(a) - 1 : b.top < a.bottom - 1) inOrder = false;
      }
      /* Lines: tops more than a few pixels apart. */
      let factLines = 0;
      let lastTop = -Infinity;
      for (const t of boxes.map((b) => b.top).sort((x, y) => x - y)) {
        if (t - lastTop > 4) factLines++;
        lastTop = t;
      }
      const menu = row.querySelector('.fe-list__col--menu')?.getBoundingClientRect();
      const check = row.querySelector('.fe-list__col--check')?.getBoundingClientRect();
      const sameLine = (b?: DOMRect) => !!b && b.top < lead.bottom && b.bottom > lead.top;
      return {
        stacked: list.classList.contains('is-stacked'),
        overflow: list.scrollWidth - list.clientWidth,
        menuOnFirstLine: sameLine(menu),
        menuInside: !!menu && menu.right <= port.right + 1 && menu.left >= port.left - 1,
        checkOnFirstLine: check ? sameLine(check) : null,
        rowHeight: Math.round(row.getBoundingClientRect().height),
        factLines,
        firstIndent: boxes.length ? Math.round(start(boxes[0]) - start(lead)) : null,
        inOrder,
        cells,
      };
    },
    { rowSel, listSel },
  );
}

/** What every stacked row promises about its facts (see the file's header). */
function expectFacts(seen: Awaited<ReturnType<typeof measureRow>>) {
  const entries = Object.entries(seen.cells);
  expect(entries.length, 'no facts under the lead').toBeGreaterThan(0);
  for (const [id, c] of entries) {
    expect(c.under, `${id} is not under the lead`).toBe(true);
    expect(c.inside, `${id} runs out of the pane`).toBe(true);
    expect(c.cut, `${id} is cut: "${c.text}"`).toBe(false);
    expect(c.label, `${id} carries no label`).not.toBe('');
    expect(c.labelRoom, `${id}'s label is not drawn before its value`).toBeGreaterThanOrEqual(12);
    // The label and the " · " are the stylesheet's, not the cell's text.
    expect(c.text.startsWith(c.label), `${id}'s label is in its text`).toBe(false);
    expect(c.text.includes('\u00b7'), `${id}'s separator is in its text`).toBe(false);
    // " · " after every fact but the last.
    expect(c.sep, `${id}: separator ${c.sep ? 'after the last fact' : 'missing'}`).toBe(!c.last);
  }
  expect(entries.filter(([, c]) => c.last).length, 'exactly one last fact').toBe(1);
  expect(entries[entries.length - 1][1].last, 'the last fact is the last one drawn').toBe(true);
  // The facts start under the lead's start, not under the tick, and flow in
  // the person's column order.
  expect(Math.abs(seen.firstIndent ?? 99), `the facts start ${seen.firstIndent}px off the lead`).toBeLessThanOrEqual(2);
  expect(seen.inOrder, 'the facts are not in reading order').toBe(true);
}

test.describe('The one table in a narrow pane', () => {
  test.beforeAll(async ({ request }) => {
    await seedFiles(request);
  });
  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('on a phone the listing stacks: its facts under the name, labelled, compact and whole', async ({ page }) => {
    await openList(page, PHONE);

    // Nothing is shed: the owner's rule is untouched.
    expect(await columns(page)).toEqual(['check', 'name', 'type', 'owner', 'mod', 'size', 'star', 'menu']);

    const seen = await measureRow(page, '.fe-list__row');
    console.log(`117 phone: ${JSON.stringify(seen)}`);
    // What was measured, kept beside the result (test-results/).
    await page.screenshot({ path: test.info().outputPath('explorer-phone-390.png') });
    expect(seen.stacked, 'a 390px pane kept a table of minimum-width columns').toBe(true);
    expect(seen.overflow, 'stacked rows do not scroll sideways').toBeLessThanOrEqual(1);
    expect(seen.checkOnFirstLine, 'the tick is on the name’s line').toBe(true);
    expect(seen.menuOnFirstLine, 'the ⋮ is on the name’s line').toBe(true);
    expect(seen.menuInside, 'the ⋮ is inside the pane').toBe(true);

    const ids = Object.keys(seen.cells);
    expect(ids).toEqual(['type', 'owner', 'modified', 'size']);
    expectFacts(seen);
    /* COMPACT: the facts share lines rather than taking one each (the owner:
       a phone's file row is about two lines tall, not five). */
    expect(seen.factLines, `${ids.length} facts on ${seen.factLines} lines`).toBeLessThan(ids.length);

    // The header keeps its sort buttons and its column menu, and loses what
    // means nothing without columns.
    await expect(page.locator('.fe-list__head .fe-list__resize')).toHaveCount(0);
    await expect(page.locator('.fe-list__head .fe-list__colmenu-btn')).toBeVisible();
    await expect(page.locator('.fe-list__head [data-col="name"] .fe-list__sort')).toBeVisible();
  });

  test('stacked, a row is still the row: the tick selects, the ⋮ and a right-click open its menu', async ({ page }) => {
    await openList(page, PHONE);
    await expect(page.locator('.fe-list')).toHaveClass(/is-stacked/);
    const row = page.locator(`[data-fe-path="${STORAGE}://beta.txt"]`).first();

    await row.locator('.fe-list__check').first().click();
    await expect(row).toHaveAttribute('aria-selected', 'true');

    await row.locator('.fe-list__menu').first().click();
    await expect(page.getByRole('menu').first()).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('menu')).toHaveCount(0);

    await row.locator('[data-col="size"]').click({ button: 'right' });
    await expect(page.getByRole('menu').first()).toBeVisible();
    await page.keyboard.press('Escape');
  });

  test('the header still sorts the stacked rows', async ({ page }) => {
    await openList(page, PHONE);
    const names = () => page.locator('.fe-list__row .fe-list__name').allTextContents();
    await expect.poll(names).toEqual(['alpha.txt', 'beta.txt']);
    await page.locator('.fe-list__head [data-col="name"] .fe-list__sort').click();
    await expect.poll(names).toEqual(['beta.txt', 'alpha.txt']);
    await page.locator('.fe-list__head [data-col="name"] .fe-list__sort').click();
    await expect.poll(names).toEqual(['alpha.txt', 'beta.txt']);
  });

  test('a pane that holds Name and three columns is a table that scrolls to the rest', async ({ page }) => {
    await openList(page, MIDDLE);
    const list = page.locator('.fe-list');
    await expect(list).not.toHaveClass(/is-stacked/);
    expect(await columns(page)).toEqual(['check', 'name', 'type', 'owner', 'mod', 'size', 'star', 'menu']);
    const shape = await page.evaluate(() => {
      const l = document.querySelector('.fe-list')!;
      return { width: l.clientWidth, overflows: l.scrollWidth > l.clientWidth };
    });
    console.log(`117 middle: ${JSON.stringify(shape)}`);
    await page.screenshot({ path: test.info().outputPath('explorer-540.png') });
    expect(shape.overflows, 'this pane cannot hold all seven columns — it is meant to scroll').toBe(true);
    await expect(page.locator('.fe-list__head .fe-list__resize').first()).toBeAttached();
    await expect(list).toHaveClass(/is-pin-menu/);
  });

  test('a pane with room keeps the frozen control it always had', async ({ page }) => {
    await openList(page, { width: 1280, height: 800 });

    await expect(page.locator('.fe-list')).not.toHaveClass(/is-stacked/);
    await expect(page.locator('.fe-list')).toHaveClass(/is-pin-menu/);
    const position = await page.evaluate(
      () => getComputedStyle(document.querySelector('.fe-list__row .fe-list__col--menu')!).position,
    );
    expect(position, 'the ⋮ stopped riding the edge on a wide table').toBe('sticky');
  });

  test('an admin table on a phone stacks too, its Actions on the first line', async ({ page }) => {
    await signIn(page, PHONE);
    await page.goto('/admin/users');
    const list = page.locator('[data-table-id="admin.users"]');
    await expect(list.locator('.fe-list__row').first()).toBeVisible({ timeout: 15_000 });
    await page.waitForTimeout(400);
    await expect(list).toHaveClass(/is-stacked/);
    const seen = await measureRow(page, '.fe-list__row', '[data-table-id="admin.users"]');
    console.log(`117 users: ${JSON.stringify(seen)}`);
    await page.screenshot({ path: test.info().outputPath('admin-users-phone-390.png') });
    expect(seen.overflow).toBeLessThanOrEqual(1);
    expect(seen.menuOnFirstLine, 'Actions is on the email’s line').toBe(true);
    expect(seen.menuInside, 'Actions is inside the pane').toBe(true);
    expect(Object.keys(seen.cells).length).toBeGreaterThanOrEqual(3);
    expectFacts(seen);
    expect(seen.factLines).toBeLessThan(Object.keys(seen.cells).length);
    const actions = list.locator('[data-testid^="user-actions-"]').first();
    await actions.click();
    await expect(page.getByRole('menu').first()).toBeVisible();
    await page.keyboard.press('Escape');
  });
});
