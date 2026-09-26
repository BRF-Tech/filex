/**
 * New document: the person names the file, the type decides what it is (#56).
 *
 * Issue #56 (ahjephson): the dialog tied the extension to the type — Plain
 * text drew `.txt` as a read-only suffix — so `LICENSE`, `NOTICE`, `Makefile`,
 * `test.conf` or `example.custom` could not be created. Now the field holds
 * the whole name; the type prefills its extension (`Untitled.txt`) and still
 * decides the bytes and the editor.
 *
 * Measured end to end, in a real browser against a real binary:
 *
 *   - the default is `Untitled.txt`, and focusing the field selects the stem;
 *   - `LICENSE` (Plain text), `test.conf` (Plain text), `notes.md` (Markdown)
 *     and a default `.txt` are created under exactly those names and each
 *     opens in its type's editor straight away — `LICENSE` in the text editor
 *     although its name picks no viewer;
 *   - `LICENSE` can be SAVED from that editor (save-text allowed by extension
 *     and refused the first Ctrl+S with 415 before #56), and opens as text
 *     again later, from the listing;
 *   - a type switch keeps the stem; an empty `.docx` made as Plain text is
 *     refused in the dialog;
 *   - the dialog fits at 1280 and 390 px wide, light and dark.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';

const STORE = `e2e-newdoc-${Date.now()}`;

/** The navigation panel is a drawer on a phone (the explorer's own width
 *  under 560px): open it from the toolbar when it is not on screen. */
async function ensureNav(page: Page) {
  const storage = page.getByTestId(`sidenav-storage-${STORE}`);
  if (await storage.isVisible().catch(() => false)) return;
  await page.getByTestId('toolbar-nav').click();
  await expect(storage).toBeVisible();
}

async function openStorage(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORE)}`);
  await expect(page.getByTestId('toolbar-nav')).toBeVisible();
  await ensureNav(page);
  await page.getByTestId(`sidenav-storage-${STORE}`).click();
}

async function openDialog(page: Page) {
  await ensureNav(page);
  await page.getByTestId('sidenav-new').click();
  await page.locator('.fe-ctx__item', { hasText: 'New document' }).click();
  await expect(page.getByTestId('newdoc-modal')).toBeVisible();
}

/**
 * Replace the whole name the way a person does: click in, select all, type.
 *
 * ⚠ Not `locator.fill()` on an unfocused field. Fill focuses it while it
 * fills, focusing selects the stem (the rename behaviour this feature adds),
 * and fill's text then replaced only `Untitled`: `fill('LICENSE')` produced
 * `LICENSE.txt` — measured in Chromium, the dialog doing what it should,
 * measured with the wrong tool.
 */
async function typeName(page: Page, value: string) {
  const input = page.getByTestId('newdoc-name');
  await input.click();
  await page.keyboard.press('ControlOrMeta+a');
  await page.keyboard.type(value);
  await expect(input).toHaveValue(value);
}

/** Create `name` as `type` through the dialog; the viewer opens on success. */
async function createAs(page: Page, type: string, name: string) {
  await openDialog(page);
  await page.getByTestId(`newdoc-type-${type}`).click();
  await typeName(page, name);
  const create = page.getByTestId('newdoc-create');
  await expect(create).toBeEnabled();
  await create.click();
  await expect(page.getByTestId('newdoc-modal')).toHaveCount(0);
}

async function closeViewer(page: Page) {
  await page.locator('.fe-viewer__close').first().click();
  await expect(page.locator('.fe-viewer__close')).toHaveCount(0);
}

async function names(api: APIRequestContext): Promise<string[]> {
  const r = await api.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${STORE}://`)}`);
  expect(r.ok(), `index: ${r.status()}`).toBe(true);
  const body = (await r.json()) as { files: Array<{ basename: string }> };
  return body.files.map((f) => f.basename);
}

test.describe('New document — any file name (#56)', () => {
  let api: APIRequestContext;

  test.beforeAll(async ({ request, playwright, baseURL }) => {
    await dropStorageByName(request, STORE);
    await seedLocalStorage(request, STORE, `/tmp/filex-${STORE}`);
    api = await newAuthedRequest(playwright, baseURL ?? '');
  });

  test.afterAll(async ({ request }) => {
    await api?.dispose();
    await dropStorageByName(request, STORE);
  });

  test('the default is Untitled.txt, and the field selects the stem like a rename', async ({ page }) => {
    await openStorage(page);
    await openDialog(page);
    await page.getByTestId('newdoc-type-txt').click();
    const input = page.getByTestId('newdoc-name');
    await expect(input).toHaveValue('Untitled.txt');
    await expect(page.locator('.fe-newdoc__suffix'), 'no read-only extension beside the field').toHaveCount(0);

    // A real click: focus happens on mousedown, and the caret lands on mouseup.
    await input.click();
    const sel = await input.evaluate((el: HTMLInputElement) => [el.selectionStart, el.selectionEnd]);
    expect(sel, 'the stem is selected, the extension is not').toEqual([0, 'Untitled'.length]);
    await page.keyboard.type('draft');
    await expect(input).toHaveValue('draft.txt');
    await page.getByTestId('newdoc-create').click();
    await expect(page.getByTestId('newdoc-modal')).toHaveCount(0);

    await expect(page.locator('.fe-preview__code-lang')).toHaveText('plaintext');
    expect(await names(api)).toContain('draft.txt');
    await closeViewer(page);
  });

  test('LICENSE as Plain text: created under that name, opened in the text editor, and saved', async ({ page }) => {
    await openStorage(page);
    await createAs(page, 'txt', 'LICENSE');

    expect(await names(api)).toContain('LICENSE');
    expect(await names(api)).not.toContain('LICENSE.txt');
    // The name picks no viewer; the type it was made as does.
    await expect(page.locator('.fe-preview__code-wrap')).toBeVisible();
    await expect(page.locator('.fe-preview__code-lang')).toHaveText('plaintext');

    // Save from the editor. Before #56 save-text allowed by extension only,
    // and this answered 415.
    const editor = page.locator('.fe-preview__code-editor:not(.is-hidden)');
    await expect(editor, 'the editor mounts').toBeVisible({ timeout: 20_000 });
    await editor.click();
    await page.keyboard.type('MIT License');
    const saved = page.waitForResponse((r) => r.url().includes('/api/files/save-text'));
    await page.locator('.fe-preview__code-toolbar .fe-btn--primary').click();
    expect((await saved).status(), 'save-text accepts the file it just opened').toBe(200);
    await expect(page.locator('.fe-preview__code-status--ok')).toBeVisible();

    const prev = await api.get(
      `/api/files/manager?action=preview&path=${encodeURIComponent(`${STORE}://LICENSE`)}`,
    );
    expect(await prev.text()).toContain('MIT License');
    await closeViewer(page);

    // Later, from the listing: the server calls the bytes text, so it opens
    // as text again rather than as a download.
    await page.getByTestId('view-list').click();
    await page.locator(`[data-fe-path="${STORE}://LICENSE"]`).first().dblclick();
    await expect(page.locator('.fe-preview__code-wrap')).toBeVisible();
    await expect(page.locator('.fe-preview__code-lang')).toHaveText('plaintext');
    await closeViewer(page);
  });

  test('test.conf as Plain text and notes.md as Markdown open in their editors', async ({ page }) => {
    await openStorage(page);

    await createAs(page, 'txt', 'test.conf');
    await expect(page.locator('.fe-preview__code-wrap')).toBeVisible();
    await expect(page.locator('.fe-preview__code-lang'), 'Plain text is what was asked for').toHaveText('plaintext');
    await closeViewer(page);

    await createAs(page, 'md', 'notes.md');
    await expect(page.locator('.fe-preview__md-split'), 'the markdown editor, split view').toBeVisible();
    await closeViewer(page);

    const listed = await names(api);
    expect(listed).toContain('test.conf');
    expect(listed).toContain('notes.md');
    expect(listed).not.toContain('test.conf.txt');
  });

  test('a type switch keeps the stem, and an empty .docx is refused as Plain text', async ({ page }) => {
    await openStorage(page);
    await openDialog(page);
    await page.getByTestId('newdoc-type-txt').click();
    const input = page.getByTestId('newdoc-name');
    await typeName(page, 'minutes.txt');
    await page.getByTestId('newdoc-type-md').click();
    await expect(input).toHaveValue('minutes.md');
    await typeName(page, 'Makefile');
    await page.getByTestId('newdoc-type-txt').click();
    await expect(input).toHaveValue('Makefile');

    await typeName(page, 'x.docx');
    await expect(page.getByTestId('newdoc-name-error')).toContainText('.docx');
    await expect(page.getByTestId('newdoc-create')).toBeDisabled();
  });

  for (const width of [1280, 390]) {
    for (const scheme of ['light', 'dark'] as const) {
      test(`the dialog fits at ${width}px, ${scheme}`, async ({ page }) => {
        await page.setViewportSize({ width, height: width > 600 ? 860 : 844 });
        await page.emulateMedia({ colorScheme: scheme });
        await openStorage(page);
        await openDialog(page);
        await page.getByTestId('newdoc-type-txt').click();
        await typeName(page, 'a-rather-long-file-name-for-a-licence-text-LICENSE.custom');
        await page.getByTestId('newdoc-name').blur();

        const m = await page.evaluate(() => {
          const card = document.querySelector('[data-testid="newdoc-modal"]')!.closest('.fe-modal__card') as HTMLElement;
          const input = document.querySelector('[data-testid="newdoc-name"]') as HTMLElement;
          const create = document.querySelector('[data-testid="newdoc-create"]') as HTMLElement;
          const c = card.getBoundingClientRect();
          const i = input.getBoundingClientRect();
          const b = create.getBoundingClientRect();
          const bg = getComputedStyle(card).backgroundColor.match(/\d+(\.\d+)?/g)!.map(Number);
          return {
            pageOverflow: document.documentElement.scrollWidth - window.innerWidth,
            cardLeft: c.left,
            cardRight: c.right,
            cardOverflow: card.scrollWidth - card.clientWidth,
            inputLeft: i.left,
            inputRight: i.right,
            createRight: b.right,
            luminance: (0.2126 * bg[0] + 0.7152 * bg[1] + 0.0722 * bg[2]) / 255,
          };
        });
        expect(m.pageOverflow, 'no horizontal page scroll').toBeLessThanOrEqual(0);
        expect(m.cardOverflow, 'nothing overflows the dialog').toBeLessThanOrEqual(0);
        expect(m.cardLeft).toBeGreaterThanOrEqual(0);
        expect(m.cardRight).toBeLessThanOrEqual(width);
        expect(m.inputLeft).toBeGreaterThanOrEqual(m.cardLeft);
        expect(m.inputRight, 'the name field stays inside the dialog').toBeLessThanOrEqual(m.cardRight);
        expect(m.createRight, 'Create stays inside the dialog').toBeLessThanOrEqual(m.cardRight);
        if (scheme === 'dark') expect(m.luminance, 'a dark dialog in a dark window').toBeLessThan(0.35);
        else expect(m.luminance, 'a light dialog in a light window').toBeGreaterThan(0.8);
      });
    }
  }
});
