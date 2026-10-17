/**
 * 247 — a whole storage given to people and groups (task #263, GitHub #117).
 *
 * An assignment is a grant on the storage's ROOT (`<storage>://`, path prefix
 * ""), made from two places: the storage's own page in the admin panel (its
 * Access section) and the explorer's storage row ("Access…", the access
 * dialog on the root). Measured here, in a real browser against a real
 * binary, and checked against the SERVER after every step:
 *
 *   · per-item access control off: the section says nobody can be given the
 *     storage yet and offers "Turn on per-item access" in place; its dialog
 *     says what turning it on does, collects who keeps access FIRST and only
 *     then switches it on and gives them the storage - and the person then
 *     reads a file two folders down that no grant of its own covers;
 *   · the section's table changes a level in the row, and "Revoke all
 *     access" takes back the storage-wide rows while a folder grant of the
 *     same person stays and still answers;
 *   · the explorer's storage row offers "Access…" to whoever owns the root -
 *     an administrator, and a person given the storage as owner (not on a
 *     storage they only edit) - and opens the dialog with People only: no
 *     link switch, no file request;
 *   · Folder access names the root grant "Whole storage", not "/";
 *   · no door hands a whole storage to a public link.
 *
 * ⚠ Words matched in both languages: another spec may leave the account in
 * Turkish. Signed in through the page's own cookie jar, the page under test
 * the first one the tab opens (lesson #1541).
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { ADMIN_EMAIL, ADMIN_PASSWORD, apiLogin, dismissInstallBanner } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';

const STAMP = Date.now();
const STORE = `e2e-whole247-${STAMP}`;
// A second storage, so the explorer's top level lists storages rather than
// opening the only one.
const OTHER = `e2e-whole247b-${STAMP}`;
const MEMBER = `whole247-${STAMP}@example.com`;
/** How every screen names the person (lib/personName, model.PersonLabel):
 *  the display name, else the username - derived from the address when the
 *  account is made - else the address. A chip and a row of the access
 *  dialog say the name; the dialog keeps the address in the row's title. */
const MEMBER_NAME = `whole247-${STAMP}`;
const MEMBER_PW = 'whole-storage-247-pw';
const GROUP = `Whole247 ${STAMP}`;
const FILE = 'plan247.txt';
const BODY = 'the deep plan, 247';

let api: APIRequestContext;
let member: APIRequestContext;
let storageId = 0;
let memberId = 0;
let groupId = 0;

type Grant = { id: number; kind?: string; user_id?: number; group_id?: number; path_prefix: string; level: string };

async function direct(path: string): Promise<Grant[]> {
  const res = await api.get(`/api/files/permissions?path=${encodeURIComponent(path)}`);
  expect(res.ok(), `permissions at ${path}: ${res.status()}`).toBeTruthy();
  return ((await res.json()) as { direct?: Grant[] }).direct ?? [];
}

async function rbacOn(): Promise<boolean> {
  const res = await api.get(`/api/admin/storages/${storageId}`);
  return ((await res.json()) as { rbac_enabled?: boolean }).rbac_enabled === true;
}

async function memberLists(rel: string): Promise<{ status: number; names: string[] }> {
  const res = await member.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${STORE}://${rel}`)}`);
  if (!res.ok()) return { status: res.status(), names: [] };
  const body = (await res.json()) as { files?: Array<{ basename: string }> };
  return { status: res.status(), names: (body.files ?? []).map((f) => f.basename) };
}

async function signIn(page: Page) {
  await dismissInstallBanner(page);
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  const res = await page.request.post('/api/auth/login', { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
  expect(res.ok(), `sign-in: ${res.status()}`).toBeTruthy();
}

/** Per-item access control on, from the API - for a test that is not about
 *  turning it on (each test sets up what it needs: lesson #1522). */
async function ensureRbacOn() {
  const res = await api.patch(`/api/admin/storages/${storageId}`, { data: { rbac_enabled: true } });
  expect(res.ok(), `rbac on: ${res.status()}`).toBeTruthy();
}

async function grant(body: Record<string, unknown>) {
  const res = await api.post('/api/files/permissions', { data: body });
  expect(res.ok(), `grant ${JSON.stringify(body)}: ${res.status()} ${await res.text()}`).toBeTruthy();
}

async function openStoragePage(page: Page) {
  await signIn(page);
  await page.goto(`/admin/storages/${storageId}`);
  await expect(page.getByTestId('storage-access')).toBeVisible({ timeout: 20_000 });
}

test.describe('a whole storage given to people and groups', () => {
  test.beforeAll(async ({ playwright, baseURL, request }) => {
    api = await newAuthedRequest(playwright, baseURL ?? '');
    for (const name of [STORE, OTHER]) await dropStorageByName(request, name);
    storageId = (await seedLocalStorage(request, STORE, `/tmp/filex-${STORE}`)).id;
    await seedLocalStorage(request, OTHER, `/tmp/filex-${OTHER}`);
    for (const [path, name] of [
      [`${STORE}://`, 'Ekip'],
      [`${STORE}://Ekip`, 'derin'],
      [`${STORE}://`, 'Gizli'],
    ]) {
      const made = await api.post('/api/files/manager?action=newfolder', { data: { path, name } });
      expect(made.ok(), `folder ${name}: ${made.status()}`).toBeTruthy();
    }
    const up = await api.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORE}://Ekip/derin`,
        'file[]': { name: FILE, mimeType: 'text/plain', buffer: Buffer.from(BODY) },
      },
    });
    expect(up.ok(), `upload ${up.status()}`).toBeTruthy();

    await apiLogin(request);
    const made = await request.post('/api/admin/users', { data: { email: MEMBER, password: MEMBER_PW, role: 'user' } });
    expect(made.ok(), `member: ${made.status()} ${await made.text()}`).toBeTruthy();
    const body = (await made.json()) as { id?: number; user?: { id?: number } };
    memberId = Number(body.id ?? body.user?.id ?? 0);
    expect(memberId).toBeGreaterThan(0);
    member = await newAuthedRequest(playwright, baseURL ?? '', MEMBER, MEMBER_PW);

    const group = await api.post('/api/admin/groups', { data: { name: GROUP } });
    expect(group.status(), `group: ${await group.text()}`).toBe(201);
    groupId = Number(((await group.json()) as { group: { id: number } }).group.id);
  });

  test.afterAll(async ({ request }) => {
    await member?.dispose();
    for (const name of [STORE, OTHER]) await dropStorageByName(request, name).catch(() => undefined);
    if (groupId) await api?.delete(`/api/admin/groups/${groupId}`).catch(() => undefined);
    if (memberId) await api?.delete(`/api/admin/users/${memberId}`).catch(() => undefined);
    await api?.dispose();
  });

  test('per-item access off: the section turns it on in place, keeping the people chosen first', async ({ page }) => {
    expect(await rbacOn(), 'the storage starts with per-item access control off').toBe(false);
    await openStoragePage(page);
    const section = page.getByTestId('storage-access');
    await expect(section.getByTestId('storage-access-rbac-off')).toBeVisible();
    await expect(section.getByTestId('storage-access-add'), 'nobody can be given it yet').toHaveCount(0);

    await section.getByTestId('storage-access-enable').click();
    const dialog = page.getByTestId('storage-access-enable-dialog');
    await expect(dialog).toBeVisible();
    await expect(page.getByTestId('storage-access-enable-warning')).toContainText(/first, then turn it on|Önce .* sonra açın/);
    await expect(page.getByTestId('storage-access-keepers-none')).toBeVisible();

    await page.getByTestId('storage-access-keep-level-editor').click();
    await page.getByTestId('storage-access-keep-search').locator('input').fill(`whole247-${STAMP}`);
    await page.getByTestId(`storage-access-keep-option-u${memberId}`).click();
    await expect(page.getByTestId('storage-access-keepers')).toContainText(MEMBER_NAME);
    expect(await rbacOn(), 'choosing changes nothing on the server').toBe(false);
    expect(await direct(`${STORE}://`)).toEqual([]);

    await page.getByTestId('storage-access-enable-confirm').click();
    await expect(dialog).toBeHidden({ timeout: 15_000 });
    await expect.poll(rbacOn).toBe(true);
    const given = await direct(`${STORE}://`);
    expect(given).toHaveLength(1);
    expect(given[0]).toMatchObject({ user_id: memberId, path_prefix: '', level: 'editor' });
    await expect(section.getByTestId('storage-access-rbac-off')).toHaveCount(0);
    await expect(section.getByTestId(`storage-access-row-u${given[0].id}`)).toContainText(MEMBER);

    // The person reaches the whole storage: a file two folders down that no
    // grant of its own covers, read through the same doors every client uses.
    const deep = await memberLists('Ekip/derin');
    expect(deep.status).toBe(200);
    expect(deep.names).toContain(FILE);
    const read = await member.get(`/api/files/manager?action=download&path=${encodeURIComponent(`${STORE}://Ekip/derin/${FILE}`)}`);
    expect(read.status()).toBe(200);
    expect(await read.text()).toBe(BODY);
  });

  test('the table changes a level; "Revoke all access" keeps a folder grant of the same person', async ({ page }) => {
    // The person and a group on the whole storage, and a folder grant of the
    // person's own.
    await ensureRbacOn();
    await grant({ path: `${STORE}://`, user_id: memberId, level: 'editor' });
    await grant({ path: `${STORE}://`, group_id: groupId, level: 'viewer' });
    await grant({ path: `${STORE}://Ekip`, user_id: memberId, level: 'viewer' });
    const person = (await direct(`${STORE}://`)).find((g) => g.user_id === memberId);
    expect(person).toBeTruthy();

    await openStoragePage(page);
    const table = page.locator('[data-table-id="admin.storage.access"]');
    await expect(table.getByText(GROUP)).toBeVisible();
    await page.getByTestId(`storage-access-level-u${person!.id}-owner`).click();
    await expect
      .poll(async () => (await direct(`${STORE}://`)).find((g) => g.user_id === memberId)?.level)
      .toBe('owner');

    page.once('dialog', (d) => d.accept());
    await page.getByTestId('storage-access-revoke-all').click();
    await expect.poll(async () => (await direct(`${STORE}://`)).length).toBe(0);
    await expect(table.locator('[data-testid^="storage-access-row-"]')).toHaveCount(0);

    const folder = await direct(`${STORE}://Ekip`);
    expect(folder, 'the folder grant is another row, and it stays').toEqual([
      expect.objectContaining({ user_id: memberId, path_prefix: 'Ekip', level: 'viewer' }),
    ]);
    expect((await memberLists('Ekip/derin')).names, 'and still answers').toContain(FILE);
    expect((await memberLists('Gizli')).status, 'the rest of the storage went with the root').not.toBe(200);
  });

  test('the explorer’s storage row: "Access…" opens the dialog with People only', async ({ page }) => {
    await ensureRbacOn();
    await grant({ path: `${STORE}://`, user_id: memberId, level: 'viewer' });

    await signIn(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORE)}`);
    await expect(page.locator(`[data-fe-path^="${STORE}://Ekip"]`).first()).toBeVisible({ timeout: 20_000 });
    await page.getByTestId('breadcrumb-home').click();
    const row = page.locator(`[data-fe-path="${STORE}"]`).first();
    await expect(row).toBeVisible({ timeout: 20_000 });
    await row.click({ button: 'right' });
    const entry = page.getByRole('menuitem', { name: /^(Access…|Erişim…)/ });
    await expect(entry).toBeVisible();
    await entry.click();

    const share = page.locator('.fe-share');
    await expect(share).toBeVisible();
    await expect(share.locator('.fe-share__title')).toContainText(STORE);
    await expect(page.getByTestId('share-people')).toBeVisible();
    await expect(page.getByTestId('share-storage-root-hint')).toBeVisible();
    await expect(page.getByTestId('share-lead'), 'no link switch on a whole storage').toHaveCount(0);
    await expect(page.getByTestId('share-link-section')).toHaveCount(0);
    await expect(page.getByTestId('share-drop'), 'no file request on a whole storage').toHaveCount(0);
    await expect(share.locator('.fe-share__person', { hasText: MEMBER_NAME })).toHaveAttribute('title', MEMBER);
  });

  test('a person who owns the storage is offered "Access…" too; on a storage they only edit, not', async ({ page }) => {
    // The panel routes let an owner of the root manage the whole storage
    // (requireOwner); the explorer offers it to the same people.
    await ensureRbacOn();
    await grant({ path: `${STORE}://`, user_id: memberId, level: 'owner' });

    await dismissInstallBanner(page);
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    const res = await page.request.post('/api/auth/login', { data: { email: MEMBER, password: MEMBER_PW } });
    expect(res.ok(), `member sign-in: ${res.status()}`).toBeTruthy();
    await page.goto('/drive/explore');
    await page.getByTestId(`sidenav-storage-${STORE}`).click();
    await expect(page.locator(`[data-fe-path^="${STORE}://Ekip"]`).first()).toBeVisible({ timeout: 20_000 });
    await page.getByTestId('breadcrumb-home').click();

    const offersAccess = async (name: string): Promise<boolean> => {
      const row = page.locator(`[data-fe-path="${name}"]`).first();
      await expect(row).toBeVisible({ timeout: 20_000 });
      await row.click({ button: 'right' });
      const menu = page.locator('.fe-ctx, .fe-sheet').last();
      await expect(menu.getByRole('menuitem').first()).toBeVisible();
      const n = await menu.getByRole('menuitem', { name: /^(Access…|Erişim…)/ }).count();
      await page.keyboard.press('Escape');
      await expect(menu).toBeHidden();
      return n > 0;
    };
    expect(await offersAccess(STORE), 'the owner of the root').toBe(true);
    expect(await offersAccess(OTHER), 'a storage the person only edits (access control off)').toBe(false);

    await page.locator(`[data-fe-path="${STORE}"]`).first().click({ button: 'right' });
    await page.getByRole('menuitem', { name: /^(Access…|Erişim…)/ }).click();
    await expect(page.getByTestId('share-people')).toBeVisible();
    await expect(page.getByTestId('share-lead')).toHaveCount(0);
    await expect(page.locator('.fe-share .fe-share__person', { hasText: MEMBER_NAME })).toHaveAttribute('title', MEMBER);
  });

  test('Folder access names the root grant as the whole storage', async ({ page }) => {
    await ensureRbacOn();
    await grant({ path: `${STORE}://`, user_id: memberId, level: 'viewer' });
    await signIn(page);
    await page.goto('/admin/grants');
    const table = page.locator('[data-table-id="admin.grants"]');
    await expect(table.locator('.fe-list__row').first()).toBeVisible({ timeout: 20_000 });
    await page.getByPlaceholder(/Search user, path, storage|Kullanıcı, yol, depo ara/).fill(STORE);
    const row = table.locator('.fe-list__row', { hasText: MEMBER }).filter({ has: page.getByTestId('grant-whole-storage') });
    await expect(row).toHaveCount(1);
    await expect(row.getByTestId('grant-whole-storage')).toHaveText(/^(Whole storage|Tüm depo)$/);
  });

  test('no door hands a whole storage to a public link', async () => {
    for (const kind of [undefined, 'drop']) {
      const res = await api.post('/api/files/share', { data: { path: `${STORE}://`, ...(kind ? { kind } : {}) } });
      expect(res.status(), `share door${kind ? ` (${kind})` : ''}`).toBeGreaterThanOrEqual(400);
      expect(res.status()).toBeLessThan(500);
    }
    const invite = await api.post('/api/files/permissions/invite', {
      data: { path: `${STORE}://`, email: `nobody247-${STAMP}@example.com`, level: 'viewer' },
    });
    expect(invite.status()).toBe(400);
    expect(((await invite.json()) as { error?: string }).error).toBe('root_link');
    const links = await api.get(`/api/files/share?path=${encodeURIComponent(`${STORE}://`)}`);
    if (links.ok()) expect(((await links.json()) as { shares?: unknown[] }).shares ?? []).toEqual([]);
  });
});
