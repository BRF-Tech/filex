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
const WRITE_VERBS = [/^rename$/i, /^delete$/i, /^share \/ permissions$/i, /^move to…?$/i];
// What a virtual view must NOT offer: there is no folder to create in / paste into.
const FOLDER_ONLY = [/^new folder$/i, /^paste$/i, /^upload$/i];

async function openExplorer(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
  await expect(page.getByTestId(`sidenav-storage-${STORAGE}`)).toBeVisible();
}

/** Right-click the file's row/card inside `scope` and return the menu's verbs. */
async function menuVerbsOn(page: Page, scope: ReturnType<Page['locator']>) {
  const row = scope.locator(`[data-fe-path="${QUALIFIED}"]`).first();
  await expect(row).toBeVisible();
  await row.click({ button: 'right' });
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

    await page.getByTestId('sidenav-view-starred').click();
    const onStarred = await menuVerbsOn(page, pane);
    expectWriteVerbs(onStarred, 'starred');
    expectNoFolderVerbs(onStarred, 'starred');
    expect([...onStarred].sort(), 'Starred menu differs from the folder menu').toEqual(expected);

    await page.getByTestId('sidenav-view-recent').click();
    const onRecent = await menuVerbsOn(page, pane);
    expectWriteVerbs(onRecent, 'recent');
    expectNoFolderVerbs(onRecent, 'recent');
    expect([...onRecent].sort(), 'Recent menu differs from the folder menu').toEqual(expected);

    await page.getByTestId('sidenav-view-home').click();
    await expect(page.getByTestId('home-view')).toBeVisible();
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
    await page.getByTestId('sidenav-view-home').click();
    await expect(page.getByTestId('home-view')).toBeVisible();
    const names = await menuVerbsOn(page, page.getByTestId('home-recent'));
    expectWriteVerbs(names, 'home/first screen');
    expectNoFolderVerbs(names, 'home/first screen');
  });
});
