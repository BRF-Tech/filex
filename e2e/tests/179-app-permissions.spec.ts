/**
 * 179 — app permissions (0.49.0), one journey end to end in a real browser.
 *
 * An installed app declares what the administrator can give or take away per
 * role and per person (manifest `user_permissions`, backend perm/app.go) —
 * "Request signatures" for the signing app. The fixture is the `echo` module
 * the Go suite runs (backend/internal/wasmplugin/testdata/echo, built by
 * scripts/build-wasm-fixture.sh, read through helpers/echoFixture, which fails
 * the spec on a module older than its sources): it declares ONE, `request`
 * (default: people who can change files), and one action that `requires` it.
 *
 * Walked:
 *   1. The administrator installs the app; Admin → Roles → User lists the
 *      permission under the app's name in the Apps group, its Default saying
 *      what the app's default gives that role (allowed). Deny, Save.
 *   2. A person on the User role no longer has the action in the explorer's
 *      menu, and running it anyway is refused by the server (403, the key, the
 *      built-in role as the source) — the menu hides only what the server
 *      refuses.
 *   3. Allow on the role gives it back: in the menu, and the run is queued.
 *   4. A person's own exception beats the role: on their page the row says
 *      where the answer comes from, Deny there takes it away from them alone.
 *
 * Everything the journey needs besides the screens under test is made through
 * the API as the administrator; what the person may do is asked from their
 * own session.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { ADMIN_EMAIL, ADMIN_PASSWORD, apiLogin, dismissInstallBanner } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';
import { guardFixture, installThroughWizard } from '../helpers/appPlugin';
import { echoFixture } from '../helpers/echoFixture';

const ECHO = echoFixture();

const RUN = Date.now();
const STORAGE = `e2e-appperm-${RUN}`;
const FOLDER = 'Contracts';
const FILE = 'contract.txt';
const PERSON = `appperm-person-${RUN}@example.com`;
const PERSON_PW = 'appperm-person-pw-2026';
const KEY = 'app.echo.request';
/** The action that requires it, in either language the person may read. */
const ACTION = /^(Request signatures probe|İmza isteme denemesi)$/;
/** An action of the same app that requires nothing: proof the app's rows are in the menu at all. */
const OTHER = /^(Upper-case|Büyük harf)$/;

let api: APIRequestContext;
let personId = 0;
/** The User role as it was, put back afterwards. */
let userRoleBefore: { permissions: string[]; apps?: Record<string, string> } | null = null;

async function removeEcho() {
  const list = await api.get('/api/admin/app-plugins');
  if (!list.ok()) return;
  for (const p of (await list.json()).plugins ?? []) {
    if (p.name === 'echo') await api.delete(`/api/admin/app-plugins/${p.id}`);
  }
}

/**
 * Signed in through the page's own cookie jar, WITHOUT a page (task #260).
 *
 * ⚠⚠ Not through the sign-in form. The form lands on the account's start
 * page, and the next step left that page for the explorer while it was still
 * asking the server for things (the bell's count, the start page's lists):
 * WebKit drops its network process on such a navigation now and then (filex
 * lesson #1541), and the explorer then said "The server could not be
 * reached" over an empty listing and never asked for it - the 0.56.3 GitHub
 * gate's webkit red on "taken from the role", 15 s on a row that never came.
 * The page under test is the first page of the tab instead.
 */
async function signIn(page: Page, email: string, password: string) {
  await dismissInstallBanner(page);
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  const res = await page.request.post('/api/auth/login', { data: { email, password } });
  expect(res.ok(), `sign-in as ${email}: ${res.status()}`).toBeTruthy();
}

const signInAsPerson = (page: Page) => signIn(page, PERSON, PERSON_PW);
const signInAsAdmin = (page: Page) => signIn(page, ADMIN_EMAIL, ADMIN_PASSWORD);

/** The labels of the right-click menu on the contract, from the person's session. */
async function personMenu(page: Page): Promise<string[]> {
  // The app rows come from /api/files/plugins/actions, asked once when the
  // explorer starts: wait for that ANSWER (filex lesson #1508), not for a
  // quiet network, which a page with a socket and a poll may never have.
  const actions = page
    .waitForResponse((r) => r.url().includes('/api/files/plugins/actions') && r.request().method() === 'GET', {
      timeout: 15_000,
    })
    .catch(() => null);
  await page.goto(`/drive/explore#${encodeURIComponent(STORAGE)}/${FOLDER}`);
  const row = page.locator('[data-fe-path]').filter({ hasText: FILE }).first();
  await expect(row).toBeVisible({ timeout: 15_000 });
  expect((await actions)?.ok(), 'the app rows were never asked for').toBe(true);
  await row.click({ button: 'right' });
  const menu = page.locator('.fe-ctx').last();
  await expect(menu).toBeVisible();
  // The app's own row that requires nothing is in every menu of this
  // journey: once it is drawn, so is everything the app gives this person.
  await expect(menu.locator('.fe-ctx__item:visible .fe-ctx__label').filter({ hasText: OTHER }).first()).toBeVisible();
  const labels = (await menu.locator('.fe-ctx__item:visible .fe-ctx__label').allTextContents()).map((s) => s.trim());
  await page.keyboard.press('Escape');
  return labels;
}

/** Runs the action as the signed-in person, whatever the menu shows. */
async function runAsPerson(page: Page) {
  return page.request.post('/api/files/plugins/actions/echo/request/run', {
    data: { paths: [`${STORAGE}://${FOLDER}/${FILE}`] },
  });
}

/** Admin → Roles → the User role's editor, its Apps group open. */
async function openUserRole(page: Page) {
  await page.goto('/admin/roles');
  await expect(page.getByTestId('roles-list')).toBeVisible();
  await page.getByTestId('role-actions-builtin-user').click();
  await page.locator('.fe-ctx [data-testid="role-actions-builtin-user-edit"]').last().click();
  const editor = page.getByTestId('builtin-role-user');
  await expect(editor).toBeVisible();
  await editor.getByTestId('perm-group-toggle-apps').click();
  return editor;
}

async function userRoleApps(): Promise<Record<string, string>> {
  const got = await api.get('/api/admin/roles/builtin?role=user');
  expect(got.ok(), await got.text()).toBeTruthy();
  return (await got.json()).apps ?? {};
}

test.describe('app permissions: per role and per person', () => {
  test.describe.configure({ mode: 'serial' });
  guardFixture(ECHO, test.skip);

  test.beforeAll(async ({ playwright, baseURL, request }) => {
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
    const mk = await api.post('/api/files/manager?action=newfolder', { data: { path: `${STORAGE}://`, name: FOLDER } });
    expect(mk.ok(), `newfolder: ${mk.status()}`).toBeTruthy();
    const up = await api.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://${FOLDER}`, 'file[]': { name: FILE, mimeType: 'text/plain', buffer: Buffer.from('sign me') } },
    });
    expect(up.ok(), `upload: ${up.status()}`).toBeTruthy();
    await removeEcho();

    const before = await api.get('/api/admin/roles/builtin?role=user');
    expect(before.ok()).toBeTruthy();
    userRoleBefore = await before.json();

    await apiLogin(request);
    const user = await api.post('/api/admin/users', { data: { email: PERSON, password: PERSON_PW, role: 'user' } });
    expect(user.ok(), await user.text()).toBeTruthy();
    personId = (await user.json()).id;
  });

  test.afterAll(async ({ request }) => {
    if (userRoleBefore) {
      await api.put('/api/admin/roles/builtin?role=user', {
        data: { permissions: userRoleBefore.permissions, apps: userRoleBefore.apps ?? {} },
      });
    }
    if (personId) await api.delete(`/api/admin/users/${personId}`);
    await removeEcho();
    await dropStorageByName(request, STORAGE);
    await api.dispose();
  });

  test("the User role lists the app's permission under Apps; Deny is saved as the role's decision", async ({ page }) => {
    await signInAsAdmin(page);
    await installThroughWizard(page, ECHO);

    const editor = await openUserRole(page);
    const apps = editor.getByTestId('perm-group-apps');
    await expect(apps.getByTestId('perm-app-echo')).toHaveText(/Echo Fixture|Yankı/);
    const row = apps.getByTestId(`perm-row-${KEY}`);
    await expect(row).toContainText(/Request signatures|İmza isteme/);
    // Default says what it comes to for this role: the app's default is
    // "people who can change files", which the User role is.
    const byDefault = row.getByTestId(`perm-${KEY}-inherit`);
    await expect(byDefault).toHaveAttribute('data-default', 'allowed');
    await expect(byDefault).toHaveText(/Default \(allowed\)|Varsayılan \(izin var\)/);
    await expect(byDefault).toHaveAttribute('aria-checked', 'true');

    await row.getByTestId(`perm-${KEY}-deny`).click();
    await expect(row.getByTestId(`perm-${KEY}-deny`)).toHaveAttribute('aria-checked', 'true');
    await editor.page().getByTestId('builtin-role-save').click();
    await expect(editor).toBeHidden();
    expect((await userRoleApps())[KEY]).toBe('deny');
  });

  test('taken from the role: not in the person\'s menu, and running it anyway is refused', async ({ page }) => {
    await signInAsPerson(page);
    const labels = await personMenu(page);
    expect(labels.some((l) => OTHER.test(l)), `menu: ${labels.join(', ')}`).toBe(true);
    expect(labels.some((l) => ACTION.test(l)), `menu: ${labels.join(', ')}`).toBe(false);

    const refused = await runAsPerson(page);
    expect(refused.status(), await refused.text()).toBe(403);
    const body = await refused.json();
    expect(body.error).toBe('permission_denied');
    expect(body.permission).toBe(KEY);
    expect(body.source?.kind).toBe('base');
  });

  test("Allow on the role gives it back: in the person's menu, and the run is queued", async ({ page, browser }) => {
    await signInAsAdmin(page);
    const editor = await openUserRole(page);
    const row = editor.getByTestId(`perm-row-${KEY}`);
    await expect(row.getByTestId(`perm-${KEY}-deny`)).toHaveAttribute('aria-checked', 'true');
    await row.getByTestId(`perm-${KEY}-allow`).click();
    await editor.page().getByTestId('builtin-role-save').click();
    await expect(editor).toBeHidden();
    expect((await userRoleApps())[KEY]).toBe('allow');

    const ctx = await browser.newContext();
    const person = await ctx.newPage();
    try {
      await signInAsPerson(person);
      const labels = await personMenu(person);
      expect(labels.some((l) => ACTION.test(l)), `menu: ${labels.join(', ')}`).toBe(true);
      const run = await runAsPerson(person);
      expect(run.status(), await run.text()).toBe(202);
    } finally {
      await ctx.close();
    }
  });

  test("a person's own exception beats the role, and their page says where each answer comes from", async ({ page, browser }) => {
    await signInAsAdmin(page);
    await page.goto(`/admin/users/${personId}`);
    const card = page.getByTestId('user-permissions-card');
    await expect(card).toBeVisible();
    await card.getByTestId('perm-group-toggle-apps').click();
    const row = card.getByTestId(`perm-row-${KEY}`);
    // The role allows it (the previous step): allowed, from the built-in role.
    const said = row.getByTestId(`perm-effective-${KEY}`);
    await expect(said).toHaveAttribute('data-allowed', 'true');
    await expect(said).toHaveAttribute('data-source', 'base');
    await expect(row.getByTestId(`perm-${KEY}-inherit`)).toHaveAttribute('data-default', 'allowed');

    await row.getByTestId(`perm-${KEY}-deny`).click();
    await card.getByTestId('user-permissions-save').click();
    await expect(said).toHaveAttribute('data-allowed', 'false');
    await expect(said).toHaveAttribute('data-source', 'override');
    // Default still says what the role alone would give them.
    await expect(row.getByTestId(`perm-${KEY}-inherit`)).toHaveAttribute('data-default', 'allowed');
    const saved = await api.get(`/api/admin/users/${personId}/exceptions`);
    expect((await saved.json()).overrides[KEY]).toBe('deny');

    const ctx = await browser.newContext();
    const person = await ctx.newPage();
    try {
      await signInAsPerson(person);
      const labels = await personMenu(person);
      expect(labels.some((l) => ACTION.test(l)), `menu: ${labels.join(', ')}`).toBe(false);
      const refused = await runAsPerson(person);
      expect(refused.status()).toBe(403);
      expect((await refused.json()).source?.kind).toBe('override');
    } finally {
      await ctx.close();
    }
  });
});
