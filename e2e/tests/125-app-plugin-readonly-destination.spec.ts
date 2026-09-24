/**
 * 125-app-plugin-readonly-destination — where an app's result goes when it
 * cannot go beside its source.
 *
 * v0.43.0 wave 2 (2026-09-22). On a read-only storage the converter's
 * "Convert…" was offered and then failed at the end of the wizard: the job
 * wrote its sibling into a storage nobody may write. The owner's decision
 * (2026-09-22) was that the action stays, and the wizard asks where the
 * result should go — with the existing destination picker, defaulting to the
 * person's own home folder.
 *
 * That is a PLATFORM option, not a converter trick: a manifest may declare
 * `output.elsewhere`, and a job may then answer `output {mode: "folder",
 * dir}`. The server re-checks the chosen folder every time — storage
 * enabled, in the caller's tenant, not read-only, the folder exists, the
 * caller is at least an editor there, and `writegate` (locks, internal
 * dirs) agrees.
 *
 * What this spec measures, in a browser:
 *   1. a read-only storage still offers "Convert…";
 *   2. the wizard has a "Where" step, and it opens with a writable default
 *      that is NOT the read-only storage;
 *   3. the person can pick another folder with the destination picker, and
 *      the converted file lands THERE — not next to the source, which is
 *      read-only and stays untouched;
 *   4. the server refuses a destination that cannot be written, even when
 *      the answer comes straight from an API caller (the screen is not the
 *      authority).
 *
 * The module is the real `filex-convert` build (e2e/helpers/app-locations.mjs);
 * without it the spec skips, unless FILEX_REQUIRE_WASM_FIXTURE=1.
 */
import { test, expect, type Page, type Locator } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';
import { guardFixture, installThroughWizard, resolveApp, PNG_1PX } from '../helpers/appPlugin';
import { openView, removeApp, surfaceEvent } from '../helpers/surface';

const APP = resolveApp('convert');

const RO = `e2e-rodest-src-${Date.now()}`;
const DEST = `e2e-rodest-out-${Date.now()}`;
const OUT = 'out';
const PNG = 'photo.png';
const SRC = `${RO}://${PNG}`;

/** The files a folder holds right now, by basename. */
async function listing(page: Page, folder: string): Promise<string[]> {
  const res = await page.request.get(
    `/api/files/manager?action=index&path=${encodeURIComponent(folder)}`,
  );
  if (!res.ok()) return [];
  const body = (await res.json()) as { files?: { basename: string }[] };
  return (body.files ?? []).map((f) => f.basename);
}

/** The step the wizard's spine marks as the one the person is on. */
async function activeStep(modal: Locator): Promise<string> {
  const active = modal.locator('.fe-steps__item[aria-current="step"]').first();
  if (!(await active.count())) return '';
  return (await active.getAttribute('data-step')) ?? '';
}

/** Right-click the PNG and read the menu's labels. */
async function menuOnTheSource(page: Page): Promise<Locator> {
  const row = page.locator(`[data-fe-path="${SRC}"]`).first();
  await expect(row).toBeVisible();
  await row.click({ button: 'right' });
  const menu = page.getByRole('menu').first();
  await expect(menu).toBeVisible();
  return menu;
}

async function openExplorer(page: Page, storage: string) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(storage)}`);
  await expect(page.getByTestId(`sidenav-storage-${storage}`)).toBeVisible();
}

test.describe('App plugins: a read-only source, and a destination the person chooses', () => {
  test.describe.configure({ mode: 'serial' });
  guardFixture(APP, test.skip);

  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, RO);
    await dropStorageByName(request, DEST);
    // Seeded WRITABLE so the fixture file can be put there, then flipped:
    // a read-only storage is one nothing may write, including the harness.
    const src = await seedLocalStorage(request, RO, `/tmp/filex-${RO}`);
    await apiLogin(request);
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: { path: `${RO}://`, 'file[]': { name: PNG, mimeType: 'image/png', buffer: PNG_1PX } },
    });
    expect(up.ok(), `upload: ${up.status()} ${await up.text()}`).toBe(true);
    const ro = await request.patch(`/api/admin/storages/${src.id}`, { data: { read_only: true } });
    expect(ro.ok(), `flip to read-only: ${ro.status()} ${await ro.text()}`).toBe(true);
    const back = await request.get(`/api/admin/storages/${src.id}`);
    expect(((await back.json()) as { read_only?: boolean }).read_only, 'the source is read-only').toBe(true);

    await seedLocalStorage(request, DEST, `/tmp/filex-${DEST}`);
    const mk = await request.post('/api/files/manager?action=newfolder', {
      data: { path: `${DEST}://`, name: OUT },
    });
    expect(mk.ok(), `mkdir ${OUT}: ${mk.status()} ${await mk.text()}`).toBe(true);
    await removeApp(request, 'convert');
  });

  test.afterAll(async ({ request }) => {
    await apiLogin(request);
    await removeApp(request, 'convert');
    await dropStorageByName(request, RO);
    await dropStorageByName(request, DEST);
  });

  test('install', async ({ page }) => {
    await loginAs(page);
    await installThroughWizard(page, APP);
  });

  test('a read-only source keeps Convert… and the wizard asks where the result goes', async ({ page }) => {
    await openExplorer(page, RO);
    const menu = await menuOnTheSource(page);
    const labels = (await menu.locator('[role="menuitem"] .fe-ctx__label').allInnerTexts()).map((s) => s.trim());
    expect(
      labels.some((l) => /^(Convert…|Dönüştür…)$/.test(l)),
      `a read-only storage still offers the converter: [${labels.join(', ')}]`,
    ).toBe(true);

    await menu
      .getByRole('menuitem')
      .filter({ hasText: /^(Convert…|Dönüştür…)$/ })
      .first()
      .click();
    const modal = page.getByTestId('plugin-view');
    await expect(modal).toBeVisible();

    // The spine says the step exists before the person gets there — a wizard
    // that grows a step under the pointer is the bug this replaced.
    const where = modal.locator('.fe-steps__item[data-step="where"]');
    await expect(where, 'a read-only source adds the "Where" step to the spine').toBeVisible();

    // Format: press JPEG (the one format a PNG reaches with no engine at
    // all), then Next until the Where step is the active one.
    await modal.locator('[data-testid^="fe-choice-target_"][data-testid$="-jpg"]').first().click();
    // ⚠ The footer lives in the dialog's actions slot, NOT inside
    // `plugin-view` — a `modal.getByTestId(...)` here finds nothing.
    const submit = page.getByTestId('plugin-view-action-submit');
    await expect(submit).toBeEnabled();
    // ⚠ Wait for the step to CHANGE after each press. Reading the spine
    // straight after a click reads the screen the person just left (the
    // answer is a round trip away), and the loop then pressed Next twice:
    // measured 2026-09-23, the wizard sailed past Where to Review.
    for (let step = 0; step < 5; step++) {
      const now = await activeStep(modal);
      if (now === 'where') break;
      await submit.click();
      await expect.poll(() => activeStep(modal), { timeout: 15_000 }).not.toBe(now);
    }
    expect(await activeStep(modal), 'the wizard reaches the Where step').toBe('where');
    await expect(where).toHaveClass(/is-active/);

    // It opens on a writable default — the person's home — and never on the
    // storage that cannot be written.
    const chooser = modal.getByTestId('surface-file-chooser');
    await expect(chooser).toBeVisible();
    const value = modal.getByTestId('surface-file-chooser-value');
    // ⚠ "not empty text" is not the measurement: an unanswered chooser still
    // prints a placeholder ("No folder chosen"), which read as an answer. The
    // class the chooser puts on itself when it holds nothing is the truth —
    // without it, a host that hands the app no home passed this test
    // (measured 2026-09-23 by mutating `homeOf` to return "").
    await expect(value, 'the Where step opens with a default folder').not.toHaveClass(/fe-sfile__value--empty/);
    const fallback = (await value.innerText()).trim();
    expect(fallback, `the default must not be the read-only storage: ${fallback}`).not.toContain(RO);

    // Pick another folder with the destination picker filex already has.
    const choose = modal.getByTestId('surface-file-chooser-open');
    await expect(choose, 'the chooser can be opened from a plugin screen').toBeEnabled();
    await choose.click();
    const picker = page.getByTestId('destpicker');
    await expect(picker).toBeVisible();
    for (let up = 0; up < 6; up++) {
      const row = picker.getByTestId(`destpicker-row-${DEST}`);
      if (await row.count()) break;
      const upBtn = picker.getByTestId('destpicker-up');
      if (await upBtn.isDisabled()) break;
      await upBtn.click();
      await page.waitForTimeout(250);
    }
    await picker.getByTestId(`destpicker-row-${DEST}`).click();
    await expect(picker.getByTestId(`destpicker-row-${OUT}`)).toBeVisible();
    await picker.getByTestId(`destpicker-row-${OUT}`).click();
    // ⚠ The picker's confirm is in the dialog's actions slot too, beside the
    // rows rather than inside them.
    const confirm = page.getByTestId('destpicker-confirm');
    await expect(confirm).toBeEnabled();
    await confirm.click();
    await expect(picker).toBeHidden();

    const chosen = (await modal.getByTestId('surface-file-chooser-value').innerText()).trim();
    expect(chosen, `the screen shows what was chosen: ${chosen}`).toContain(OUT);

    // Review says where it will be saved, then Convert.
    await submit.click();
    await expect(modal.locator('.fe-steps__item[data-step="review"]')).toHaveClass(/is-active/);
    await expect(modal, 'the review names the chosen folder').toContainText(OUT);
    await submit.click();

    // The result lands in the CHOSEN folder…
    await expect
      .poll(async () => listing(page, `${DEST}://${OUT}`), {
        message: 'the converted file lands in the folder the person chose',
        timeout: 60_000,
      })
      .toContain('photo.jpg');
    // …and the read-only storage is exactly as it was.
    expect(await listing(page, `${RO}://`), 'nothing was written beside the source').toEqual([PNG]);
  });

  test('the server refuses a destination that cannot be written', async ({ request }) => {
    await apiLogin(request);
    const opened = await openView(request, 'convert', 'options', SRC);
    const chosen = await surfaceEvent(request, 'convert', 'options', {
      path: SRC,
      event: 'change',
      state: opened.state ?? {},
      data: { values: { target_image: 'jpg' } },
    });
    expect(chosen.surface?.state?.target, 'the wizard holds the chosen format').toBe('jpg');

    // Straight to the last step with a destination the screen would never
    // offer: the read-only storage itself. The plugin is happy to ask for
    // it; the HOST is the authority.
    const state = { ...(chosen.surface!.state ?? {}), step: 'review', dest: `${RO}://` };
    const res = await request.post('/api/files/plugins/views/convert/options/event', {
      data: { path: SRC, event: 'submit', action_id: 'submit', state, data: { values: {} } },
    });
    expect(
      res.status(),
      `a read-only destination must be refused, not queued: ${res.status()} ${await res.text()}`,
    ).toBe(409);
    expect((await res.json()).error).toBe('read_only');
  });
});
