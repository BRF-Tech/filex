/**
 * 115-tag-kinds — personal and team tags, in the browser.
 *
 * ⚠⚠ The finding (tester, 2026-09-22): a non-admin tagged a file "Müşteri
 * Teklifi"; the tag appeared — lower-cased — in another user's and the
 * admin's sidebar and on the file, and the other user could remove it. Tags
 * were one label per file, shared with every account on the server, while the
 * code called them per-user, and nothing on screen ever said so.
 *
 * The owner's decision: personal (yours, like a star) and team (everyone in
 * the tenant who can see the file; changing one needs edit permission). This
 * spec walks the tester's own steps on a real server and a real browser:
 *
 *   1. the non-admin tags a file from the right-click menu — the chip keeps
 *      the capitals and says PERSONAL, and the admin's panel does not list it;
 *   2. a team tag is listed for a colleague under "Team", opens a view whose
 *      address and crumb name the kind, and a viewer sees it with no × and is
 *      told why "Team" is not offered.
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs, apiLogin, dismissInstallBanner } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName, findNodeIdByBasename, newAuthedRequest } from '../helpers/seed';

const STORAGE = `e2e-tagkind-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const FILE_NAME = 'teklif.txt';
const QUALIFIED = `${STORAGE}://${FILE_NAME}`;

const OWNER = { email: 'tag-owner@example.com', password: 'tag-owner-pw-2026' };
const VIEWER = { email: 'tag-viewer@example.com', password: 'tag-viewer-pw-2026' };

async function loginUser(page: Page, who: { email: string; password: string }) {
  await dismissInstallBanner(page);
  await page.addInitScript(() => {
    try {
      localStorage.setItem('filex.tourDone', '1');
    } catch {
      /* storage blocked — the tour will just be present */
    }
  });
  await page.goto('/admin/login');
  await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(who.email);
  await page.getByLabel(/password|parola/i).fill(who.password);
  await page
    .getByRole('button', { name: 'Sign in', exact: true })
    .or(page.getByRole('button', { name: 'Oturum aç', exact: true }))
    .first()
    .click();
  await page.waitForURL(/\/drive\//, { timeout: 15_000 });
}

/** Open the file's tag editor from its right-click menu. */
async function openTagEditor(page: Page, explorePath: string) {
  await page.goto(`${explorePath}?storage=${encodeURIComponent(STORAGE)}`);
  await page.getByTestId(`sidenav-storage-${STORAGE}`).click();
  const row = page.locator(`.fe__body [data-fe-path="${QUALIFIED}"]`).first();
  await expect(row).toBeVisible();
  await row.click({ button: 'right' });
  await page.getByRole('menuitem', { name: /^(tags|etiketler)…?/i }).click();
  const picker = page.locator('.filex-tag-picker').last();
  await expect(picker).toBeVisible();
  await expect(picker).not.toHaveClass(/is-loading/);
  return picker;
}

/** Close the tag editor with its own × (the dialog is the explorer's modal). */
async function closeTagEditor(page: Page) {
  const close = page.locator('.fe-modal__card .fe-modal__close').last();
  await close.click();
  await expect(page.locator('.filex-tag-picker')).toHaveCount(0);
}

test.describe('Tags — personal and team', () => {
  test.describe.configure({ mode: 'serial' });

  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    await apiLogin(request);
    for (const [who, role] of [
      [OWNER, 'user'],
      [VIEWER, 'viewer'],
    ] as const) {
      // Best-effort: a rerun against the same data dir already has them.
      await request.post('/api/admin/users', { data: { email: who.email, password: who.password, role } });
    }
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        'file[]': { name: FILE_NAME, mimeType: 'text/plain', buffer: Buffer.from('teklif\n') },
      },
    });
    if (!up.ok()) throw new Error(`upload failed: ${up.status()} ${await up.text()}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('a non-admin’s tag keeps its capitals, says PERSONAL, and stays out of the admin’s panel', async ({
    page,
    browser,
    request,
  }) => {
    await loginUser(page, OWNER);
    const picker = await openTagEditor(page, '/drive/explore');

    await picker.locator('.filex-tag-add-btn').click();
    // Personal is chosen before anything is typed — the default, visibly.
    await expect(picker.getByTestId('tag-kind-personal')).toHaveAttribute('aria-checked', 'true');
    await picker.locator('.filex-tag-add input').fill('Müşteri Teklifi');
    await picker.locator('.filex-tag-add input').press('Enter');

    const chip = picker.locator('.filex-tag').filter({ hasText: 'Müşteri Teklifi' });
    await expect(chip).toHaveAttribute('data-tag-kind', 'personal');
    // Exactly as typed — the old server stored "müşteri teklifi".
    await expect(chip.locator('.filex-tag-open')).toHaveText('Müşteri Teklifi');
    await expect(chip.locator('.filex-tag-open')).toHaveAttribute('title', /personal|kişisel/i);
    await closeTagEditor(page);

    // Their panel lists it under Personal…
    const mine = page.getByTestId('sidenav-tag-Müşteri Teklifi');
    await expect(mine).toBeVisible();
    await expect(mine).toHaveAttribute('data-tag-kind', 'personal');
    await expect(page.getByTestId('sidenav-tags-personal')).toBeVisible();
    await page.screenshot({ path: test.info().outputPath('owner-personal-tag.png') });

    // …and the admin's does not — the tester's exact complaint.
    await apiLogin(request);
    const all = await request.get('/api/files/manager/tags/all');
    expect(all.ok()).toBe(true);
    expect(JSON.stringify(await all.json())).not.toContain('Müşteri');
    const ctx = await browser.newContext();
    const admin = await ctx.newPage();
    await admin.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(admin);
    await admin.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
    await expect(admin.getByTestId(`sidenav-storage-${STORAGE}`)).toBeVisible();
    await expect(admin.getByTestId('sidenav-tag-Müşteri Teklifi')).toHaveCount(0);
    await ctx.close();
  });

  test('a TEAM tag is shared, labelled, opens its own view — and a viewer cannot take it off', async ({
    page,
    browser,
    playwright,
    baseURL,
  }) => {
    // The owner adds a team tag through the picker: choose Team, then type.
    await loginUser(page, OWNER);
    const picker = await openTagEditor(page, '/drive/explore');
    await picker.locator('.filex-tag-add-btn').click();
    await picker.getByTestId('tag-kind-team').click();
    await expect(picker.getByTestId('tag-kind-team')).toHaveAttribute('aria-checked', 'true');
    await picker.locator('.filex-tag-add input').fill('Rapor');
    await picker.locator('.filex-tag-add input').press('Enter');
    await expect(picker.locator('.filex-tag').filter({ hasText: 'Rapor' })).toHaveAttribute('data-tag-kind', 'team');
    await closeTagEditor(page);

    // The viewer: sees the team tag (not the owner's personal one), with no ×,
    // and the Team choice disabled with the reason written under it.
    const viewerApi = await newAuthedRequest(playwright, baseURL!, VIEWER.email, VIEWER.password);
    const seen = await viewerApi.get('/api/files/manager/tags/all');
    expect((await seen.json()).items).toEqual([{ name: 'Rapor', kind: 'team' }]);
    const id = await findNodeIdByBasename(viewerApi, `${STORAGE}://`, FILE_NAME);
    expect(id).toBeTruthy();
    const refused = await viewerApi.post('/api/files/manager/tags', { data: { node_id: id, items: [] } });
    expect(refused.status(), 'a viewer removed a team tag').toBe(403);
    await viewerApi.dispose();

    // A browser of the viewer's own: one context per account, as a person has.
    const ctx = await browser.newContext();
    const vpage = await ctx.newPage();
    await loginUser(vpage, VIEWER);
    const vpicker = await openTagEditor(vpage, '/drive/explore');
    const teamChip = vpicker.locator('.filex-tag[data-tag-kind="team"]');
    await expect(teamChip).toContainText('Rapor');
    await expect(teamChip.locator('.filex-tag-x')).toHaveCount(0);
    await expect(vpicker.locator('.filex-tag').filter({ hasText: 'Müşteri' })).toHaveCount(0);
    await vpicker.locator('.filex-tag-add-btn').click();
    await expect(vpicker.getByTestId('tag-kind-team')).toBeDisabled();
    await expect(vpicker.getByTestId('tag-kind-team')).toContainText(/edit permission|düzenleme yetkin/i);
    await vpage.screenshot({ path: test.info().outputPath('viewer-team-tag.png') });
    await closeTagEditor(vpage);

    // The panel groups it under Team; clicking opens the TEAM view, whose
    // address and crumb say so.
    const row = vpage.getByTestId('sidenav-tag-Rapor');
    await expect(row).toHaveAttribute('data-tag-kind', 'team');
    await expect(vpage.getByTestId('sidenav-tags-team')).toBeVisible();
    await expect(vpage.getByTestId('sidenav-tags-personal')).toHaveCount(0);
    await row.click();
    await expect(vpage).toHaveURL(/\.teamtag~Rapor/);
    await expect(vpage.locator(`.fe__body [data-fe-path="${QUALIFIED}"]`).first()).toBeVisible();
    await expect(vpage.getByText(/#Rapor · (Team|Ekip)/).first()).toBeVisible();
    await ctx.close();
  });
});
