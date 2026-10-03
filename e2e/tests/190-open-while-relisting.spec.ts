/**
 * 190-open-while-relisting - a file opened while the explorer is between two
 * listings opens with the person's own level (#103).
 *
 * Seen once in the app-platform measurement (2026-09-28): a double-click that
 * landed while a folder was being listed opened the file READ-ONLY for a
 * person who could edit it. A row with no `perm` of its own was judged by
 * `dirPerm`, the level of the folder the explorer was showing at the moment
 * of the click. Leaving Starred for a folder clears the view at once and
 * keeps Starred's rows on screen until the folder answers; a virtual view
 * forgets the folder's level on entry, so in that gap `dirPerm` was '' and
 * `permCanEdit('')` said no - the editor came up as the read-only preview.
 *
 * The server on this branch stamps `perm` on every row, which hides the bug
 * from a plain run; this spec takes the per-row level off the answers (a host
 * or an older server that states the level once, for the folder) so the
 * explorer has to judge rows the way the race made it judge them. Then it
 * holds the folder's answer back and double-clicks into the gap
 * (lesson #608: hold the answer, do not just slow the click).
 *
 * Before the fix (lib/rowLevel): the markdown file opened without its editor
 * (no `.fe-preview__md-split-input`). After: the editor, as in the folder.
 */
import { test, expect, type Page } from '@playwright/test';
import { apiLogin, loginAs } from '../helpers/auth';
import { dropStorageByName, findNodeIdByBasename, seedLocalStorage } from '../helpers/seed';

const STORAGE = `e2e-relist-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
// A second drive keeps the install multi-storage: with exactly one storage the
// explorer opens it as its root and the side panel has no storage row to click.
const OTHER = `e2e-relist-other-${Date.now()}`;
const FILE_NAME = 'relist-103.md';
const QUALIFIED = `${STORAGE}://${FILE_NAME}`;

/** A one-shot hold on the NEXT folder listing (`?q=index`). */
interface Hold {
  reached: Promise<void>;
  release: () => void;
}
let nextHold: { arrived: () => void; released: Promise<void> } | null = null;
function holdNextListing(): Hold {
  let arrived!: () => void;
  let release!: () => void;
  const reached = new Promise<void>((r) => (arrived = r));
  const released = new Promise<void>((r) => (release = r));
  nextHold = { arrived, released };
  return { reached, release };
}

/** Folder listings and the Starred list answer without a per-row level. */
async function stripRowLevels(page: Page) {
  await page.route(
    (url) => url.pathname.endsWith('/api/files/manager') && url.searchParams.get('q') === 'index',
    async (route) => {
      const hold = nextHold;
      nextHold = null;
      if (hold) {
        hold.arrived();
        await hold.released;
      }
      const res = await route.fetch();
      const body = await res.json();
      if (Array.isArray(body?.files)) for (const f of body.files) delete f.perm;
      await route.fulfill({ response: res, json: body });
    },
  );
  await page.route(/\/api\/files\/manager\/star\/list\b/, async (route) => {
    const res = await route.fetch();
    const body = await res.json();
    if (Array.isArray(body?.nodes)) for (const n of body.nodes) delete n.perm;
    await route.fulfill({ response: res, json: body });
  });
}

test.describe('#103 - a file opened between two listings', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await dropStorageByName(request, OTHER);
    await seedLocalStorage(request, STORAGE, MOUNT);
    await seedLocalStorage(request, OTHER, `/tmp/filex-${OTHER}`);
    await apiLogin(request);
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        'file[]': { name: FILE_NAME, mimeType: 'text/markdown', buffer: Buffer.from('# relist\n') },
      },
    });
    if (!up.ok()) throw new Error(`upload failed: ${up.status()} ${await up.text()}`);
    const id = await findNodeIdByBasename(request, `${STORAGE}://`, FILE_NAME);
    if (!id) throw new Error(`no node id for ${FILE_NAME}`);
    const star = await request.post('/api/files/manager/star', { data: { node_id: id, starred: true } });
    if (!star.ok()) throw new Error(`star failed: ${star.status()} ${await star.text()}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await dropStorageByName(request, OTHER);
  });

  test('a starred file double-clicked while its folder is still loading opens in the editor', async ({ page }) => {
    await stripRowLevels(page);
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
    const storageRow = page.getByTestId(`sidenav-storage-${STORAGE}`);
    await expect(storageRow).toBeVisible();
    const pane = page.locator('.fe__body').first();
    const row = () => pane.locator(`[data-fe-path="${QUALIFIED}"]`).first();

    // The folder first, so its level has been known once.
    await storageRow.click();
    await expect(row()).toBeVisible();

    // Starred: entering it forgets the folder's level.
    const starred = page.getByTestId('sidenav-view-starred');
    await Promise.all([
      page.waitForResponse((r) => /\/api\/files\/manager\/star\/list\?limit=200\b/.test(r.url()) && r.ok()),
      starred.click(),
    ]);
    await expect(starred).toHaveAttribute('aria-current', 'page');
    await expect(page.locator('.fe__body [aria-busy="true"]')).toHaveCount(0);
    await expect(row()).toBeVisible();

    // Back to the folder, with its answer held: the view is left at once and
    // Starred's rows stay on screen - the gap the double-click landed in.
    const hold = holdNextListing();
    await storageRow.click();
    await hold.reached;
    await expect(starred).not.toHaveAttribute('aria-current', 'page');
    await expect(row()).toBeVisible();

    try {
      await row().dblclick();
      // The editor: the markdown split view with its text box. The read-only
      // preview draws the rendered page alone.
      await expect(
        page.locator('.fe-preview__md-split-input'),
        'the file opened read-only although the person may edit it',
      ).toBeVisible({ timeout: 10_000 });
    } finally {
      hold.release();
    }
    await page.getByTestId('viewer-close').click();
  });
});
