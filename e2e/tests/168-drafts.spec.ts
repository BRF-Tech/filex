/**
 * Drafts — a new document is a draft until its first save (issue #71).
 *
 * GitHub #71 (ahjephson): New document created the file before the editor
 * opened, so choosing it, naming it and closing the editor left an empty file
 * behind. Now it writes a DRAFT — a real file in the person's own drafts area
 * of that storage — and the folder gets nothing until the draft is saved.
 *
 * Measured end to end, in a real browser against a real binary:
 *
 *   - New document → a draft: nothing at the target, the editor on the draft
 *     with a bar saying where it will go; typing is written into the draft;
 *   - closing asks Save to disk / Keep in Drafts / Discard; Keep → the
 *     panel's Drafts row counts 1; the Drafts view (THE table) lists it;
 *     opening it from there brings back what was typed; Save → the file is in
 *     the folder with that content, and the count is gone;
 *   - a file that took the name in the meantime: Save asks "save as
 *     name (2).ext?" and saves exactly that, leaving the other file alone;
 *   - Discard → the draft is in the Trash (as "from Drafts"), not in Drafts;
 *   - at the limit, New document says so and opens Drafts — no file instead;
 *   - the badge and the three-option question at 1280 and 390 px.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';

const STORE = `e2e-drafts-${Date.now()}`;
const OUT = process.env.E2E_SHOTS_DIR ?? '';

interface DraftWire {
  key: string;
  name: string;
  path: string;
  target: string;
}

async function drafts(api: APIRequestContext): Promise<DraftWire[]> {
  const r = await api.get('/api/files/drafts');
  expect(r.ok(), `drafts: ${r.status()}`).toBe(true);
  return ((await r.json()) as { drafts: DraftWire[] }).drafts;
}

async function names(api: APIRequestContext, dir = 'Docs'): Promise<string[]> {
  const r = await api.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${STORE}://${dir}`)}`);
  expect(r.ok(), `index: ${r.status()}`).toBe(true);
  return ((await r.json()) as { files: Array<{ basename: string }> }).files.map((f) => f.basename);
}

async function content(api: APIRequestContext, wire: string): Promise<string> {
  const r = await api.get(`/api/files/manager?action=preview&path=${encodeURIComponent(wire)}`);
  expect(r.ok(), `preview ${wire}: ${r.status()}`).toBe(true);
  return r.text();
}

/** Every draft of the account, discarded — each test starts from none. */
async function clearDrafts(api: APIRequestContext) {
  for (const d of await drafts(api)) {
    const r = await api.delete(`/api/files/drafts/${d.key}`);
    expect(r.ok(), `discard ${d.name}: ${r.status()}`).toBe(true);
  }
}

async function ensureNav(page: Page) {
  const row = page.getByTestId('sidenav-view-drafts');
  if (await row.isVisible().catch(() => false)) return;
  await page.getByTestId('toolbar-nav').click();
  await expect(row).toBeVisible();
}

async function openDocs(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem('filex.locale', 'en');
  });
  await loginAs(page);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORE)}`);
  await expect(page.getByTestId('toolbar-nav')).toBeVisible();
  await ensureNav(page);
  await page.getByTestId(`sidenav-storage-${STORE}`).click();
  await page.locator(`[data-fe-path="${STORE}://Docs"]`).first().click();
  await expect(page.locator('.fe-breadcrumb, .fe-crumbs').first()).toContainText('Docs');
}

async function newTextDocument(page: Page, name: string) {
  await ensureNav(page);
  await page.getByTestId('sidenav-new').click();
  await page.locator('.fe-ctx__item', { hasText: 'New document' }).click();
  await expect(page.getByTestId('newdoc-modal')).toBeVisible();
  await page.getByTestId('newdoc-type-txt').click();
  const input = page.getByTestId('newdoc-name');
  // The dialog's own suggestion first (the server's dry run): typing over a
  // field it has not filled yet races its answer. Since 0.55 a late answer no
  // longer replaces a typed name; until the first keystroke it still may.
  await expect(input).toHaveValue(/^Untitled( \(\d+\))?\.txt$/, { timeout: 15_000 });
  await input.click();
  await page.keyboard.press('ControlOrMeta+a');
  await page.keyboard.type(name);
  await expect(input).toHaveValue(name);
  await expect(page.getByTestId('newdoc-draft-hint')).toBeVisible();
  // The draft's own answer, not a fixed 5 s: it is a file and four catalogue
  // rows, and the 0.55 full chain measured 4.9 and 5.8 s for it on a build
  // host whose disk writes stalled for seconds (Chromium, while a Go job
  // wrote 1.7 GiB beside it).
  const created = page.waitForResponse(
    (r) => new URL(r.url()).pathname === '/api/files/drafts' && r.request().method() === 'POST',
    { timeout: 30_000 },
  );
  await page.getByTestId('newdoc-create').click();
  expect((await created).status(), 'the draft is created').toBe(201);
  await expect(page.getByTestId('newdoc-modal')).toHaveCount(0);
  await expect(page.getByTestId('draft-bar')).toBeVisible();
}

/** Type into the text editor, and wait until the draft has it. */
async function typeIntoDraft(page: Page, text: string) {
  const editor = page.locator('.fe-preview__code-editor:not(.is-hidden)');
  await expect(editor, 'the editor mounts').toBeVisible({ timeout: 20_000 });
  await editor.click();
  const written = page.waitForResponse(
    (r) => r.url().includes('/api/files/save-text') && r.request().method() === 'POST',
    { timeout: 10_000 },
  );
  await page.keyboard.type(text);
  expect((await written).status(), 'the autosave writes into the draft').toBe(200);
}

async function closeAsking(page: Page) {
  await page.getByTestId('viewer-close').click();
  await expect(page.getByTestId('draft-close-dialog')).toBeVisible();
}

const PREFS = '/api/me/prefs?surface=web';

test.describe('Drafts (#71)', () => {
  let api: APIRequestContext;
  /* The account's preference document as it was, put back EXACTLY after
     (helpers/prefs.ts; 114-language-pack). The interface language lives on
     the account, and an earlier spec of the full run can leave it Turkish:
     this file reads English words ("New document"), so it pins English for
     its own run. */
  let prefsBefore: Record<string, unknown> = {};

  test.beforeAll(async ({ request, playwright, baseURL }) => {
    await dropStorageByName(request, STORE);
    await seedLocalStorage(request, STORE, `/tmp/filex-${STORE}`);
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const r = await api.post('/api/files/manager?action=newfolder', { data: { path: `${STORE}://`, name: 'Docs' } });
    expect(r.ok(), `newfolder: ${r.status()}`).toBe(true);
    const got = await api.get(PREFS);
    const doc = got.ok() ? ((await got.json()) as { prefs?: unknown }).prefs : {};
    prefsBefore = doc && typeof doc === 'object' && !Array.isArray(doc) ? (doc as Record<string, unknown>) : {};
    const pin = await api.put(PREFS, { data: { prefs: { ...prefsBefore, locale: 'en' } } });
    expect(pin.ok(), `prefs: ${pin.status()}`).toBe(true);
  });

  test.beforeEach(async () => {
    await clearDrafts(api);
  });

  test.afterAll(async ({ request }) => {
    if (api) {
      await api.patch('/api/admin/protection', { data: { drafts_limit: 50 } });
      await clearDrafts(api);
      await api.put(PREFS, { data: { prefs: prefsBefore } }).catch(() => undefined);
      await api.dispose();
    }
    await dropStorageByName(request, STORE);
  });

  test('New document is a draft; kept, counted, listed, reopened and saved into its folder', async ({ page }) => {
    await openDocs(page);
    await newTextDocument(page, 'minutes.txt');

    // Nothing in the folder: it is a draft, in the person's drafts area.
    await expect(page.getByTestId('draft-bar-target')).toHaveText(`will be saved to ${STORE} / Docs`);
    expect(await names(api)).not.toContain('minutes.txt');
    const [d] = await drafts(api);
    expect(d?.name).toBe('minutes.txt');
    expect(d.target).toBe(`${STORE}://Docs/minutes.txt`);
    expect(d.path).toMatch(new RegExp(`^${STORE}://\\.filex-drafts/\\d+/[0-9a-f]{16}/minutes\\.txt$`));

    await typeIntoDraft(page, 'first line');
    expect(await content(api, d.path)).toBe('first line');

    // Close → the three answers; the default keeps it.
    await closeAsking(page);
    await expect(page.getByTestId('draft-close-keep')).toBeFocused();
    await page.getByTestId('draft-close-keep').click();
    await expect(page.locator('.fe-viewer')).toHaveCount(0);
    expect(await names(api)).not.toContain('minutes.txt');

    // The panel counts it — a count, not a notification.
    await ensureNav(page);
    await expect(page.getByTestId('sidenav-drafts-count')).toHaveText('1');
    await expect(page.getByTestId('sidenav-view-drafts')).toHaveAttribute('aria-label', 'Drafts, 1 draft');

    // The Drafts view: THE table.
    await page.getByTestId('sidenav-view-drafts').click();
    const view = page.getByTestId('drafts-view');
    await expect(view).toBeVisible();
    const row = page.getByTestId(`draft-row-${d.key}`);
    await expect(row).toBeVisible();
    await expect(row).toContainText('minutes.txt');
    await expect(row).toContainText('Docs');
    await expect(row).toContainText(STORE);
    await expect(view.locator('.fe-list__row')).toHaveCount(1);
    expect(await view.locator('table').count(), 'THE table, not a <table> of its own').toBe(0);

    // Reopen: what was typed is there.
    await page.getByTestId(`draft-open-${d.key}`).click();
    await expect(page.getByTestId('draft-bar')).toBeVisible();
    await expect(page.locator('.fe-preview__code-editor:not(.is-hidden)')).toContainText('first line', { timeout: 20_000 });

    // Save → it is a file in its folder now, and the count is gone.
    await page.getByTestId('draft-save').click();
    await expect(page.getByTestId('draft-saved-note')).toContainText(`minutes.txt was saved to ${STORE} / Docs.`);
    expect(await names(api)).toContain('minutes.txt');
    expect(await content(api, `${STORE}://Docs/minutes.txt`)).toBe('first line');
    expect(await drafts(api)).toHaveLength(0);
    await page.getByTestId('viewer-close').click();
    await expect(page.getByTestId('draft-close-dialog')).toHaveCount(0);
    await expect(page.locator('.fe-viewer')).toHaveCount(0);
    await expect(page.getByTestId('sidenav-drafts-count')).toHaveCount(0);
    await expect(page.getByTestId('empty-drafts')).toBeVisible();
  });

  test('a name taken in the meantime: Save asks for name (2).ext and saves exactly that', async ({ page }) => {
    await openDocs(page);
    await newTextDocument(page, 'report.txt');
    await typeIntoDraft(page, 'the draft');

    // Somebody puts a report.txt into Docs while the draft is open.
    const other = await api.post('/api/files/save-text', { data: { path: `${STORE}://Docs/report.txt`, content: 'already here' } });
    expect(other.ok()).toBe(true);

    await page.getByTestId('draft-save').click();
    const ask = page.getByTestId('draft-taken-dialog');
    await expect(ask).toBeVisible();
    await expect(ask).toContainText(`${STORE} / Docs already has a file called report.txt`);
    await expect(page.getByTestId('draft-taken-confirm')).toHaveText('Save as report (2).txt');
    expect(await names(api), 'nothing moves before the answer').not.toContain('report (2).txt');

    await page.getByTestId('draft-taken-confirm').click();
    await expect(page.getByTestId('draft-saved-note')).toContainText('report (2).txt was saved');
    expect(await content(api, `${STORE}://Docs/report (2).txt`)).toBe('the draft');
    expect(await content(api, `${STORE}://Docs/report.txt`), 'the other file is untouched').toBe('already here');
    await page.getByTestId('viewer-close').click();
    await expect(page.locator('.fe-viewer')).toHaveCount(0);
  });

  test('Discard sends the draft to the Trash — from Drafts, and out of Drafts', async ({ page }) => {
    await openDocs(page);
    await newTextDocument(page, 'scratch.txt');
    await typeIntoDraft(page, 'not needed');
    await closeAsking(page);
    await page.getByTestId('draft-close-discard').click();
    await expect(page.locator('.fe-viewer')).toHaveCount(0);

    expect(await drafts(api)).toHaveLength(0);
    expect(await names(api)).not.toContain('scratch.txt');
    const trash = await api.get('/api/files/manager/trash');
    const entries = ((await trash.json()) as { entries: Array<{ name: string; draft?: boolean; path: string }> }).entries;
    const entry = entries.find((e) => e.name === 'scratch.txt');
    expect(entry, 'the discarded draft is in the trash').toBeTruthy();
    expect(entry?.draft).toBe(true);
    expect(JSON.stringify(entries)).not.toContain('.filex-drafts');

    await ensureNav(page);
    await page.getByTestId('sidenav-view-trash').click();
    const trashed = page.locator('.fe-list__row', { hasText: 'scratch.txt' });
    await expect(trashed).toBeVisible();
    await expect(trashed, 'where it came from: Drafts').toContainText('Drafts');
  });

  // ⚠ Escape reaches two listeners: the dialog's own (modals/Modal, which asks
  // through the editor's close) and the explorer's keyboard shortcuts, whose
  // Escape closed every overlay, the editor included. Measured: Escape on the
  // question took the question AND the editor away.
  test('Escape asks before a draft closes, and Escape on the question keeps the draft open', async ({ page }) => {
    await openDocs(page);
    await newTextDocument(page, 'escape.txt');
    await typeIntoDraft(page, 'still here');

    await page.keyboard.press('Escape');
    await expect(page.getByTestId('draft-close-dialog'), 'Escape on the editor asks first').toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByTestId('draft-close-dialog')).toHaveCount(0);
    await expect(page.getByTestId('draft-bar'), 'the editor is still open').toBeVisible();
    await expect(page.locator('.fe-preview__code-editor:not(.is-hidden)')).toContainText('still here');

    await closeAsking(page);
    await page.getByTestId('draft-close-keep').click();
    await expect(page.locator('.fe-viewer')).toHaveCount(0);
    expect((await drafts(api)).map((d) => d.name)).toEqual(['escape.txt']);
  });

  test('at the limit, New document says so and opens Drafts — and creates no file instead', async ({ page }) => {
    for (const n of ['one.txt', 'two.txt']) {
      const r = await api.post('/api/files/drafts', {
        data: { path: `${STORE}://Docs`, name: n, type: 'txt', exact_name: true },
      });
      expect(r.status(), `draft ${n}`).toBe(201);
    }
    const lim = await api.patch('/api/admin/protection', { data: { drafts_limit: 2 } });
    expect(lim.ok(), `set the limit: ${lim.status()}`).toBe(true);

    await openDocs(page);
    await ensureNav(page);
    await expect(page.getByTestId('sidenav-drafts-count')).toHaveText('2');
    await page.getByTestId('sidenav-new').click();
    await page.locator('.fe-ctx__item', { hasText: 'New document' }).click();
    await page.getByTestId('newdoc-type-txt').click();
    await page.getByTestId('newdoc-create').click();
    const limit = page.getByTestId('newdoc-draft-limit');
    await expect(limit).toContainText('You already keep as many drafts as this server allows (2)');
    expect(await drafts(api)).toHaveLength(2);
    expect((await names(api)).filter((n) => n.startsWith('Untitled'))).toEqual([]);

    await page.getByTestId('newdoc-open-drafts').click();
    await expect(page.getByTestId('newdoc-modal')).toHaveCount(0);
    await expect(page.getByTestId('drafts-view')).toBeVisible();
    await expect(page.getByTestId('drafts-view').locator('.fe-list__row')).toHaveCount(2);
    await expect(page.getByTestId('drafts-count-line')).toHaveText('2 of 2 drafts');

    await api.patch('/api/admin/protection', { data: { drafts_limit: 50 } });
  });

  for (const width of [1280, 390]) {
    for (const scheme of ['light', 'dark'] as const) {
      test(`the badge and the close question fit at ${width}px, ${scheme}`, async ({ page }) => {
        await page.setViewportSize({ width, height: width > 600 ? 860 : 844 });
        await page.emulateMedia({ colorScheme: scheme });
        const r = await api.post('/api/files/drafts', {
          data: { path: `${STORE}://Docs`, name: 'a-rather-long-draft-name-for-a-narrow-screen.txt', type: 'txt', exact_name: true },
        });
        expect(r.status()).toBe(201);
        await openDocs(page);
        await ensureNav(page);

        // The badge sits inside its row, at the row's end, and says 1.
        const badge = page.getByTestId('sidenav-drafts-count');
        await expect(badge).toHaveText('1');
        const nav = await page.evaluate(() => {
          const row = document.querySelector('[data-testid="sidenav-view-drafts"]')!.getBoundingClientRect();
          const b = document.querySelector('[data-testid="sidenav-drafts-count"]')!.getBoundingClientRect();
          return { rowL: row.left, rowR: row.right, rowT: row.top, rowB: row.bottom, bL: b.left, bR: b.right, bT: b.top, bB: b.bottom, bW: b.width };
        });
        expect(nav.bL).toBeGreaterThanOrEqual(nav.rowL);
        expect(nav.bR).toBeLessThanOrEqual(nav.rowR + 0.5);
        expect(nav.bT).toBeGreaterThanOrEqual(nav.rowT - 0.5);
        expect(nav.bB).toBeLessThanOrEqual(nav.rowB + 0.5);
        expect(nav.bW, 'a readable badge').toBeGreaterThanOrEqual(16);
        if (OUT) await page.screenshot({ path: `${OUT}/drafts-badge-${width}-${scheme}.png` });

        // Open it from Drafts and ask to close.
        await page.getByTestId('sidenav-view-drafts').click();
        await page.locator('[data-testid^="draft-open-"]').first().click();
        await expect(page.getByTestId('draft-bar')).toBeVisible();
        if (OUT) await page.screenshot({ path: `${OUT}/drafts-editor-${width}-${scheme}.png` });
        await closeAsking(page);
        const m = await page.evaluate(() => {
          const dialog = document.querySelector('[data-testid="draft-close-dialog"]')!;
          const card = dialog.closest('.fe-modal__card') as HTMLElement;
          const c = card.getBoundingClientRect();
          const btns = ['draft-close-discard', 'draft-close-save', 'draft-close-keep'].map((id) => {
            const r = document.querySelector(`[data-testid="${id}"]`)!.getBoundingClientRect();
            return { id, l: r.left, r: r.right, t: r.top, b: r.bottom, w: r.width, h: r.height };
          });
          // The dialog is the thing on top where its buttons are.
          const onTop = btns.every((b) => {
            const el = document.elementFromPoint(b.l + b.w / 2, b.t + b.h / 2);
            return !!el && !!el.closest(`[data-testid="${b.id}"]`);
          });
          const bg = getComputedStyle(card).backgroundColor.match(/\d+(\.\d+)?/g)!.map(Number);
          return {
            pageOverflow: document.documentElement.scrollWidth - window.innerWidth,
            cardL: c.left,
            cardR: c.right,
            cardOverflow: card.scrollWidth - card.clientWidth,
            btns,
            onTop,
            luminance: (0.2126 * bg[0] + 0.7152 * bg[1] + 0.0722 * bg[2]) / 255,
          };
        });
        expect(m.pageOverflow, 'no horizontal page scroll').toBeLessThanOrEqual(0);
        expect(m.cardOverflow, 'nothing overflows the dialog').toBeLessThanOrEqual(0);
        expect(m.cardL).toBeGreaterThanOrEqual(0);
        expect(m.cardR).toBeLessThanOrEqual(width);
        for (const b of m.btns) {
          expect(b.l, `${b.id} inside the dialog`).toBeGreaterThanOrEqual(m.cardL);
          expect(b.r, `${b.id} inside the dialog`).toBeLessThanOrEqual(m.cardR);
          expect(b.h, `${b.id} is a real target`).toBeGreaterThanOrEqual(28);
        }
        expect(m.onTop, 'the question is above the editor, not under it').toBe(true);
        if (scheme === 'dark') expect(m.luminance, 'a dark dialog in a dark window').toBeLessThan(0.35);
        else expect(m.luminance, 'a light dialog in a light window').toBeGreaterThan(0.8);
        if (OUT) await page.screenshot({ path: `${OUT}/drafts-close-${width}-${scheme}.png` });
        await page.getByTestId('draft-close-keep').click();
        await expect(page.locator('.fe-viewer')).toHaveCount(0);
      });
    }
  }
});
