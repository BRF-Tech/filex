/**
 * 111-internal-dirs — filex's own folders never reach a person, however they
 * got there.
 *
 * Owner's report, 2026-09-21, verbatim: *"Bazı notification'lara tıkladığımda
 * webapp'te `.filex-trash` klasörünü ve `.filex-open` klasörünü görüyorum.
 * Onlar hem bildirim içinde hem de tıklanınca webapp içinde gözükmüyor olması
 * lazım."*
 *
 * Measured in a browser on the unfixed build (same seed as below):
 *   - the bell read "New file / File changed / Moved to trash:
 *     a1b2c3d4e5f6-Bütçe Özeti.xlsx", each over `/.filex-open/…`;
 *   - "Moved to trash: report.txt" opened `#docs/.filex-trash` — breadcrumb
 *     `docs › .filex-trash`, "This folder is empty", nothing to restore;
 *   - "File changed: …Bütçe…" opened `#docs/.filex-open` and listed the
 *     desktop's working copies;
 *   - typing `#docs/.filex-open` did the same.
 *
 * The seed is the same HTTP the explorer and the desktop app's open-with flow
 * send (desktop/src/openwith-io.ts): create `.filex-open`, upload the working
 * copy, replace it (the editor's save), delete it (the sweep).
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';
import { loginAs } from '../helpers/auth';

test.describe.configure({ mode: 'serial' });

const STAMP = Date.now();
const STORAGE = `e2e-internal-${STAMP}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const DELETED = `report-${STAMP}.txt`;
const ORIGINAL = `Bütçe ${STAMP}.xlsx`;
const WORKING_COPY = `a1b2c3d4e5f6-${ORIGINAL}`;
const LIVE_COPY = `0123456789ab-Plan-${STAMP}.docx`;
const INTERNAL = /\.filex-|\.versions|\.thumbs/;

async function upload(req: APIRequestContext, dir: string, name: string, body: string) {
  const res = await req.post('/api/files/manager?action=upload', {
    multipart: { path: dir, 'file[]': { name, mimeType: 'application/octet-stream', buffer: Buffer.from(body) } },
  });
  expect(res.ok(), `upload ${dir}/${name}: ${res.status()} ${await res.text()}`).toBeTruthy();
}

async function mutate(req: APIRequestContext, action: string, data: unknown) {
  const res = await req.post(`/api/files/manager?action=${action}`, { data });
  expect(res.ok(), `${action}: ${res.status()} ${await res.text()}`).toBeTruthy();
}

async function openBell(page: Page) {
  await page.getByTestId('notification-bell').click();
  await expect(page.getByTestId('notification-panel')).toBeVisible();
}

async function toExplorer(page: Page, hash = '') {
  await page.addInitScript(() => {
    try {
      localStorage.setItem('filex.tourDone', '1');
    } catch {
      /* storage blocked */
    }
  });
  await loginAs(page);
  await page.goto(`/admin/explore${hash}`);
}

test.describe("filex's internal folders", () => {
  test.beforeAll(async ({ request, playwright, baseURL }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    const req = await newAuthedRequest(playwright, baseURL ?? '');
    try {
      await mutate(req, 'newfolder', { path: `${STORAGE}://`, name: 'Documents' });
      await upload(req, `${STORAGE}://Documents`, DELETED, 'quarterly report');
      await mutate(req, 'delete', {
        path: `${STORAGE}://Documents`,
        items: [{ path: `${STORAGE}://Documents/${DELETED}` }],
      });
      // The desktop's open-with round trip.
      await mutate(req, 'newfolder', { path: `${STORAGE}://`, name: '.filex-open' });
      await upload(req, `${STORAGE}://.filex-open`, WORKING_COPY, 'v1');
      await upload(req, `${STORAGE}://.filex-open`, WORKING_COPY, 'v2 — saved in the editor');
      await mutate(req, 'delete', {
        path: `${STORAGE}://.filex-open`,
        items: [{ path: `${STORAGE}://.filex-open/${WORKING_COPY}` }],
      });
      // A second document still open: its copy stays in the working area.
      await upload(req, `${STORAGE}://.filex-open`, LIVE_COPY, 'still open');
    } finally {
      await req.dispose();
    }
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('the bell names no internal folder, and says what happened to the document', async ({ page }) => {
    await toExplorer(page);
    await openBell(page);
    const panel = page.getByTestId('notification-panel');
    await expect(panel).toContainText(`Moved to trash: ${DELETED}`);
    // The editor's save of the working copy is a change to the ORIGINAL.
    await expect(panel).toContainText(`File changed: ${ORIGINAL}`);
    const text = await panel.innerText();
    expect(text, 'a notification still names an internal folder').not.toMatch(INTERNAL);
    expect(text, 'the working copy\'s session prefix reached the bell').not.toContain('a1b2c3d4e5f6-');
    expect(text).not.toContain(LIVE_COPY);
    // Placing and sweeping the working copy are not events at all.
    expect(text).not.toMatch(new RegExp(`New file: .*${STAMP}\\.xlsx`));
    expect(text).not.toMatch(new RegExp(`Moved to trash: .*${STAMP}\\.xlsx`));
    // Nothing on the server is the original: the row reads, and goes nowhere.
    const save = page.getByTestId('notification-row').filter({ hasText: `File changed: ${ORIGINAL}` });
    await expect(save).toHaveAttribute('data-clickable', 'no');
  });

  test('a "Moved to trash" notification opens the Trash view on the file', async ({ page }) => {
    await toExplorer(page);
    await openBell(page);
    await page
      .getByTestId('notification-row')
      .filter({ hasText: `Moved to trash: ${DELETED}` })
      .first()
      .click();
    await expect(page).toHaveURL(/#\.trash$/);
    const row = page.locator('[data-fe-path]').filter({ hasText: DELETED });
    await expect(row).toHaveCount(1);
    await expect(row).toHaveAttribute('aria-selected', 'true');
    expect(decodeURIComponent(page.url())).not.toMatch(INTERNAL);
    await expect(page.locator('body')).not.toContainText('.filex-trash');
  });

  test('the Trash never offers the swept working copy', async ({ page }) => {
    await toExplorer(page, '#.trash');
    await expect(page.locator('[data-fe-path]').filter({ hasText: DELETED })).toHaveCount(1);
    await expect(page.locator('body')).not.toContainText(ORIGINAL);
    await expect(page.locator('body')).not.toContainText('.filex-open');
  });

  test('an address typed into .filex-open lands in the storage instead', async ({ page }) => {
    await toExplorer(page, `#${STORAGE}/.filex-open`);
    await expect(page.locator('[data-fe-path]').filter({ hasText: 'Documents' })).toHaveCount(1);
    await expect(page.locator('body')).not.toContainText(LIVE_COPY);
    await expect(page.locator('body')).not.toContainText('.filex-open');
    expect(decodeURIComponent(page.url())).not.toContain('.filex-open');
  });

  // The coordinator's follow-up to the owner's report: hiding the folders was
  // half of it — anybody could still CREATE `.filex-trash`, upload into it or
  // rename a document to `.versions`, and what they made vanished the moment
  // it was written. (The seed above is the desktop's own round trip — create
  // `.filex-open`, upload, save, delete — so it passing is the proof that the
  // one exception still works.)
  test("nobody can create, upload or rename into filex's own folders", async ({ playwright, baseURL }) => {
    const req = await newAuthedRequest(playwright, baseURL ?? '');
    try {
      const attempts: Array<[string, () => Promise<{ status(): number; text(): Promise<string> }>]> = [
        ['new folder .filex-trash', () =>
          req.post('/api/files/manager?action=newfolder', { data: { path: `${STORAGE}://`, name: '.filex-trash' } })],
        ['new folder .versions', () =>
          req.post('/api/files/manager?action=newfolder', { data: { path: `${STORAGE}://Documents`, name: '.versions' } })],
        ['upload into .filex-trash', () =>
          req.post('/api/files/manager?action=upload', {
            multipart: { path: `${STORAGE}://.filex-trash`, 'file[]': { name: 'x.txt', mimeType: 'text/plain', buffer: Buffer.from('x') } },
          })],
        ['rename Documents to .thumbs', () =>
          req.post('/api/files/manager?action=rename', {
            data: { path: `${STORAGE}://`, item: `${STORAGE}://Documents`, name: '.thumbs' },
          })],
      ];
      for (const [what, send] of attempts) {
        const res = await send();
        const body = await res.text();
        expect(res.status(), `${what}: ${body}`).toBe(403);
        expect(body, what).toContain('RESERVED_NAME');
      }
    } finally {
      await req.dispose();
    }
  });

  test('the new-folder dialog says why, before anything is sent', async ({ page }) => {
    await toExplorer(page, `#${STORAGE}`);
    await expect(page.locator('[data-fe-path]').filter({ hasText: 'Documents' })).toHaveCount(1);
    const pane = page.locator('.fe__body').first();
    const box = await pane.boundingBox();
    if (!box) throw new Error('pane has no box');
    await pane.click({ button: 'right', position: { x: box.width - 12, y: box.height - 12 } });
    await page.getByRole('menuitem', { name: /^(new folder|yeni klasör)$/i }).click();
    const sent: string[] = [];
    page.on('request', (r) => {
      if (r.url().includes('action=newfolder')) sent.push(r.url());
    });
    const input = page.locator('.fe-modal input.fe-input, [role="dialog"] input.fe-input').first();
    await input.fill('.filex-trash');
    await input.press('Enter');
    await expect(page.locator('.fe-form__error')).toContainText(
      /“\.filex-trash” (is reserved for filex’s own use|adı filex’in kendi kullanımına ayrılmış)/,
    );
    expect(sent, 'the refused name still went to the server').toHaveLength(0);
    await expect(page.locator('[data-fe-path]').filter({ hasText: '.filex-trash' })).toHaveCount(0);
  });
});
