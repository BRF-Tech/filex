/**
 * 106-virtual-view-menu — the context menu is the same in every view.
 *
 * Owner, 2026-09-19: "son kullanılanlar, ana sayfa gibi sayfalarda context
 * menu eksik kalıyor. CONTEXT MENÜ HER YERDE AYNI OLMALI". Recent, Starred,
 * a tag view and the Home cards list rows from every storage and have no
 * folder behind them; the menu's write verbs (Rename, Delete, Move to, Share /
 * Permissions) were gated on the level of the folder opened LAST — none on the
 * landing page — so a right-click on a Home card or a Recent row came up
 * without them. Intermittently: after a writable folder had been visited the
 * stale level said yes to everything.
 *
 * The rows now carry their own `perm` + `read_only` (handlers/meta.go) and the
 * views forget the folder on entry, so this spec measures the one thing the
 * owner asked for: the set of verbs on the SAME file is identical in its
 * folder, on Starred, on Recent, and on the Home card — minus Paste, the one
 * verb that needs a destination folder and is hidden where there is none —
 * and "New folder" never appears in a virtual view.
 *
 * ⚠ The Recently-opened TRAY is wired the same way (`RecentlyOpened.vue`
 * emits `context`, the explorer opens the same menu) but nothing in the
 * current toolbar opens the tray (`open-recents` is declared and never
 * emitted), so it cannot be reached from a browser and is not measured here.
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName, findNodeIdByBasename } from '../helpers/seed';
import { settled } from '../helpers/stable';

const STORAGE = `e2e-vmenu-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
// A second drive, so the install is multi-storage: with exactly one visible
// storage the explorer opens THAT drive as its root before anything else, and
// the landing-page case below would inherit its (writable) folder level
// instead of arriving with none — which is the state the owner measured.
const OTHER = `e2e-vmenu-other-${Date.now()}`;
const FILE_NAME = 'everywhere.txt';
const QUALIFIED = `${STORAGE}://${FILE_NAME}`;

// The verbs a writable file's menu must offer, wherever the file is listed.
// The verb is "Share" since v0.43.0 (#21: it promised permissions a non-owner cannot have).
const WRITE_VERBS = [/^rename$/i, /^delete$/i, /^share$/i, /^move to…?$/i];
// What a virtual view must NOT offer: there is no folder to create in / paste into.
const FOLDER_ONLY = [/^new folder$/i, /^paste$/i, /^upload$/i];

async function openExplorer(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
  await expect(page.getByTestId(`sidenav-storage-${STORAGE}`)).toBeVisible();
}

/**
 * Open a virtual view and wait until the listing on screen IS that view.
 *
 * ⚠ The same file is on Starred and on Recent, so the row locator below
 * matches the OUTGOING view's row for the ~80 ms the new list is in flight —
 * and Recent draws a "Today" group label exactly where Starred's first row
 * was. Measured 2026-09-21 (full suite): the right-click was aimed at
 * Starred's row, landed on Recent's group label, and the menu that opened was
 * the background one ("Show hidden files"). Waiting for the view's own answer
 * makes the click hit the row of the view the assertion is about.
 */
async function openView(page: Page, view: 'starred' | 'recent', answers: RegExp) {
  const button = page.getByTestId(`sidenav-view-${view}`);
  await Promise.all([
    page.waitForResponse((r) => answers.test(r.url()) && r.ok()),
    button.click(),
  ]);
  // ⚠⚠ The answer above is not proof enough on its own. Up to 0.53 the
  // explorer's mount also asked `star/list` (`?limit=500`, for the inline
  // stars), and on a busy machine THAT answer could land after the click and
  // satisfy the wait before the view had even started loading — "not busy"
  // was then true of the OUTGOING folder, the right-click hit the folder's
  // row, and the menu carried the folder-only Paste (measured on the merged
  // v0.43.0 tree, full suite, 1 run in 3). 0.54 (#207) asks no such list (a
  // row says itself whether it is starred), but Home asks the same endpoint
  // as the Starred view. The view marks its panel row `aria-current="page"`
  // in the same tick it sets `loading` (loadNavView), so once the row says
  // so, "not busy" can only mean the view's own rows.
  await expect(button).toHaveAttribute('aria-current', 'page');
  // The listing is `aria-busy` from the click until the rows of the answer
  // are drawn (FileExplorer `loading`, DataTable / GridView).
  await expect(page.locator('.fe__body [aria-busy="true"]')).toHaveCount(0);
}

/**
 * Open Home and wait for ITS OWN answers — the first page of recent and of
 * starred — before anything on it is aimed at.
 *
 * ⚠ Home draws each card's grid as soon as it has ANY list, even while a
 * fresh load is still in flight (HomeView: `v-if="shownRecent.length"` comes
 * before the loading line), and swaps the fresh rows in when they land. So a
 * card can be on screen from earlier data and be redrawn a moment later.
 *
 * 0.54 (#207): Home asks the views' own pages (FileExplorer fetchNavRows,
 * `?limit=<page>&offset=0`) — the `?limit=50` / `?limit=200` this used to
 * match are gone, and so is the explorer's `star/list?limit=500` a loose wait
 * had to be kept from. The first page is matched by its `offset=0`, the page
 * size left to the explorer.
 */
async function openHome(page: Page) {
  const firstPage = (r: { url(): string; ok(): boolean }, path: string) => {
    if (!r.ok()) return false;
    const u = new URL(r.url());
    return u.pathname.endsWith(path) && u.searchParams.get('offset') === '0';
  };
  await Promise.all([
    page.waitForResponse((r) => firstPage(r, '/api/files/manager/recent')),
    page.waitForResponse((r) => firstPage(r, '/api/files/manager/star/list')),
    page.getByTestId('sidenav-view-home').click(),
  ]);
  await expect(page.getByTestId('home-view')).toBeVisible();
  // Nothing on Home still says it is loading.
  await expect(page.locator('[data-testid="home-view"] .fe-home__loading')).toHaveCount(0);
}

/** Right-click the file's row/card inside `scope` and return the menu's verbs. */
async function menuVerbsOn(page: Page, scope: ReturnType<Page['locator']>) {
  const row = scope.locator(`[data-fe-path="${QUALIFIED}"]`).first();
  await expect(row).toBeVisible();
  /*
   * ⚠⚠ The row must have STOPPED MOVING before the pointer lands on it, and
   * "visible" does not say that. A virtual view re-rendered when the inline
   * star list (`star/list?limit=500`, up to 0.53, the request openView's note
   * above names) answered AFTER the view's own rows were drawn: the row slides
   * out from under the click, the contextmenu handler finds nothing selected,
   * and the EMPTY-BACKGROUND menu opens instead — in Starred that menu is one
   * item, so the failure reads "menu lacks /^rename$/i — got [Show hidden
   * files]" and looks like a missing verb rather than a missed row. Measured
   * on the full suite, 2026-09-23.
   *
   * Two identical boxes a frame apart is the whole guard; Playwright's own
   * stability check inside `click()` looks at the element it already resolved
   * and cannot see the list reflow that replaces it.
   */
  //
  // ⚠⚠ …and two identical boxes were NOT enough: this spec kept flaking on
  // the Home card after that guard (v0.43.0, the pr47 round). A list that
  // redraws when a late answer lands can put a NEW node exactly where the old
  // one was — same box, different element. `settled` (e2e/helpers/stable.ts)
  // requires the SAME node, still attached, with an unchanged box across
  // several reads, and hands that node back: the right-click goes to exactly
  // the element that was measured.
  const target = await settled(row);
  await target.click({ button: 'right' });
  await target.dispose();
  const menu = page.getByRole('menu').first();
  await expect(menu).toBeVisible();
  // The label span alone: a menuitem's innerText also carries its
  // aria-hidden shortcut hint ("Rename F2"), which is not the verb.
  const names = (await menu.locator('[role="menuitem"] .fe-ctx__label').allInnerTexts())
    .map((s) => s.trim())
    .filter(Boolean);
  await page.keyboard.press('Escape');
  await expect(menu).toBeHidden();
  return names;
}

function expectWriteVerbs(names: string[], where: string) {
  for (const re of WRITE_VERBS) {
    expect(names.some((n) => re.test(n)), `${where}: menu lacks ${re} — got [${names.join(', ')}]`).toBe(true);
  }
}

function expectNoFolderVerbs(names: string[], where: string) {
  for (const re of FOLDER_ONLY) {
    expect(names.some((n) => re.test(n)), `${where}: menu offers ${re} — got [${names.join(', ')}]`).toBe(false);
  }
}

test.describe('Virtual views — the context menu is the same everywhere', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await dropStorageByName(request, OTHER);
    await seedLocalStorage(request, STORAGE, MOUNT);
    await seedLocalStorage(request, OTHER, `/tmp/filex-${OTHER}`);
    await apiLogin(request);
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        'file[]': { name: FILE_NAME, mimeType: 'text/plain', buffer: Buffer.from('same menu everywhere\n') },
      },
    });
    if (!up.ok()) throw new Error(`upload failed: ${up.status()} ${await up.text()}`);
    const id = await findNodeIdByBasename(request, `${STORAGE}://`, FILE_NAME);
    if (!id) throw new Error(`no node id for ${FILE_NAME}`);
    // Star it and open it once, so it is on Starred, on Recent and on both
    // Home blocks.
    const star = await request.post('/api/files/manager/star', { data: { node_id: id, starred: true } });
    if (!star.ok()) throw new Error(`star failed: ${star.status()} ${await star.text()}`);
    const recent = await request.post('/api/files/manager/recent', { data: { node_id: id } });
    if (!recent.ok()) throw new Error(`recent failed: ${recent.status()} ${await recent.text()}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await dropStorageByName(request, OTHER);
  });

  test('Starred, Recent and the Home card offer what the folder row offers', async ({ page }) => {
    await openExplorer(page);

    // The reference: the file in its own folder.
    await page.getByTestId(`sidenav-storage-${STORAGE}`).click();
    const pane = page.locator('.fe__body').first();
    const inFolder = await menuVerbsOn(page, pane);
    expectWriteVerbs(inFolder, 'folder');
    // Paste is the one folder-only verb a row menu carries; everything else
    // must travel with the row.
    const expected = inFolder.filter((n) => !/^paste$/i.test(n)).sort();

    await openView(page, 'starred', /\/api\/files\/manager\/star\/list/);
    const onStarred = await menuVerbsOn(page, pane);
    expectWriteVerbs(onStarred, 'starred');
    expectNoFolderVerbs(onStarred, 'starred');
    expect([...onStarred].sort(), 'Starred menu differs from the folder menu').toEqual(expected);

    await openView(page, 'recent', /\/api\/files\/manager\/recent\b/);
    const onRecent = await menuVerbsOn(page, pane);
    expectWriteVerbs(onRecent, 'recent');
    expectNoFolderVerbs(onRecent, 'recent');
    expect([...onRecent].sort(), 'Recent menu differs from the folder menu').toEqual(expected);

    await openHome(page);
    const onHomeStarred = await menuVerbsOn(page, page.getByTestId('home-starred'));
    expectWriteVerbs(onHomeStarred, 'home/starred card');
    expectNoFolderVerbs(onHomeStarred, 'home/starred card');
    expect([...onHomeStarred].sort(), 'Home starred-card menu differs from the folder menu').toEqual(expected);

    const onHomeRecent = await menuVerbsOn(page, page.getByTestId('home-recent'));
    expectWriteVerbs(onHomeRecent, 'home/recent card');
    expectNoFolderVerbs(onHomeRecent, 'home/recent card');
    expect([...onHomeRecent].sort(), 'Home recent-card menu differs from the folder menu').toEqual(expected);
  });

  test('Home as the FIRST screen — no folder ever opened — still offers the write verbs', async ({ page }) => {
    // The landing-page case: `dirPerm` has never been set by a folder
    // listing. This is the state the owner measured "eksik" in.
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto('/admin/explore');
    await expect(page.getByTestId('sidenav-view-home')).toBeVisible();
    await openHome(page);
    const names = await menuVerbsOn(page, page.getByTestId('home-recent'));
    expectWriteVerbs(names, 'home/first screen');
    expectNoFolderVerbs(names, 'home/first screen');
  });

  /*
   * ⚠⚠ A refresh while a view is loading must not undo the switch. Measured
   * 2026-09-26 on the v0.47.0 release gate — the test above, twice in two
   * full runs, "Recent never became aria-current": Starred → click Recent.
   * `navView` said Recent at once, but the view's address (`currentPath`)
   * still said `.starred` until Recent's rows arrived, and a refresh that
   * landed in between (the realtime layer's reload, the Refresh button, the
   * end of an action) re-read `.starred` and loaded Starred again. The click
   * was undone — when the refresh's Starred answer came back AFTER Recent's.
   * Here both answers are held and let go in that order, so the race happens
   * every time: Recent is held until Refresh has been pressed, then answers;
   * whatever Starred list the refresh asks for is held until Recent has
   * answered, then let go.
   */
  test('a refresh while Recent is loading keeps Recent', async ({ page }) => {
    await openExplorer(page);
    await openView(page, 'starred', /\/api\/files\/manager\/star\/list/);
    const isRecent = (u: URL) => u.pathname.endsWith('/api/files/manager/recent');
    const isStarList = (u: URL) => u.pathname.endsWith('/api/files/manager/star/list');
    let letRecentGo!: () => void;
    let letStarredGo!: () => void;
    const recentHeld = new Promise<void>((r) => (letRecentGo = r));
    const starredHeld = new Promise<void>((r) => (letStarredGo = r));
    await page.route(isRecent, async (route) => {
      await recentHeld;
      await route.continue();
    });
    await page.route(isStarList, async (route) => {
      await starredHeld;
      await route.continue();
    });
    const recent = page.getByTestId('sidenav-view-recent');
    await recent.click();
    await page.getByRole('button', { name: 'Refresh', exact: true }).click();
    const recentAnswered = page.waitForResponse((r) => isRecent(new URL(r.url())));
    letRecentGo();
    await recentAnswered;
    letStarredGo();
    // Long enough for a held Starred answer to land and redraw, if one was asked for.
    await page.waitForTimeout(1500);
    await expect(recent).toHaveAttribute('aria-current', 'page');
    await expect(page.locator('.fe__body [aria-busy="true"]')).toHaveCount(0);
    await expect(page.getByTestId('sidenav-view-starred')).not.toHaveAttribute('aria-current', 'page');
    await page.unroute(isRecent);
    await page.unroute(isStarList);
  });
});
