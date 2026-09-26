/**
 * 96-app-plugin-convert — the converter app, end to end.
 *
 * The walk a person makes: install the app, right-click a file, pick
 * **Convert…**, choose a target, and find the converted file next to the
 * original. Nothing is mocked — this is the real `filex-convert` module
 * running inside filex's wasm runtime.
 *
 * What it measures, in order:
 *   1. the install wizard reviews every permission the converter asks for
 *      (eight of them, six being engines) and installs only after the tick;
 *   2. the explorer's menu offers **Convert…** on a file and not on a folder;
 *   3. the first step of the wizard obeys the v3 renderer rules and the
 *      target picker is a readable CHOICE (rows of buttons, never a
 *      dropdown), whose options are the formats this server can reach;
 *   4. submitting it queues a job that writes a real JPEG next to the PNG,
 *      and the explorer shows the sibling without a reload;
 *   5. a format whose engine is absent is not offered — it is in the grey
 *      list under the picker, naming the engine it would need.
 *
 * ⚠ Convert is a WIZARD now (Format → Settings → Review), not one screen
 * with everything on it. The target picker is therefore not one `target`
 * field: it is one button group PER CATEGORY, keyed `target_<category>` —
 * and, once a format is chosen, `target_<category>__<chosen>`, so a second
 * pick in another group does not leave two buttons lit (filex-convert
 * view.CategoryField says why). `targetChoices` below reads the picker the
 * way a person sees it — every button on the step, whichever group it sits
 * in — which is the same property the old single-field assertions protected.
 *
 * ⚠⚠ A press on a format only SELECTS it (filex-convert 0.1.0). It used to
 * move the wizard on by itself, and in the v0.43.0 sweep that queued jobs
 * nobody confirmed: the person picked, reached for Next where it had been,
 * and the review's Convert was under the pointer by then. The walk below
 * presses Next on the format step with what the step has LIT — never
 * `autoFill`, which presses the first button of every group, i.e. picks a
 * video, a document and an archive at once; the wizard then takes the first
 * of those and a PNG goes to ffmpeg as an MP4 (measured 2026-09-21:
 * "ffmpeg exit 3752568763: Conversion failed!" on a 1-pixel PNG).
 *
 * The module lives in a sibling checkout; without it the spec skips, unless
 * FILEX_REQUIRE_WASM_FIXTURE=1 (CI) makes that a failure.
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName, waitForOp } from '../helpers/seed';
import { guardFixture, installThroughWizard, resolveApp, PNG_1PX } from '../helpers/appPlugin';
import {
  autoFill,
  checkLanguages,
  checkSurface,
  fieldsOf,
  openView,
  removeApp,
  surfaceEvent,
  textOf,
  walkNodes,
  type Field,
  type FieldText,
  type Node,
  type Surface,
} from '../helpers/surface';

const APP = resolveApp('convert');

const STORAGE = `e2e-convert-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const PNG = 'photo.png';
const FOLDER = 'a-folder';
const QUALIFIED = `${STORAGE}://${PNG}`;

/**
 * The key every format button group carries: `target_image`,
 * `target_document`… — with `__<chosen format>` appended once a format is
 * chosen (the key moves with the answer).
 */
const TARGET_GROUP = /^target_[a-z]+(__[a-z0-9.]+)?$/;

/** What the form of a screen holds right now: its `values`, as drawn. */
function formValues(s: Surface): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  walkNodes(s.nodes, (_w, n) => {
    if (n.type === 'form') Object.assign(out, (n.props?.values as Record<string, unknown>) ?? {});
  });
  return out;
}

/** The picker's groups: one per format category that has anything reachable. */
function targetGroups(s: Surface): Field[] {
  return fieldsOf(s).filter((f) => TARGET_GROUP.test(f.key));
}

/**
 * Every format button on the step, whichever group it sits in, tagged with
 * the group it came from. This is the picker as the person sees it.
 */
function targetChoices(s: Surface): { value: string; label: string; group: string }[] {
  return targetGroups(s).flatMap((f) =>
    (f.options ?? []).map((o) => ({ value: o.value, label: textOf(o.label), group: f.key })),
  );
}

/** Which group holds a given format — a spec must never hardcode that. */
function groupHolding(s: Surface, value: string): string {
  const hit = targetChoices(s).find((o) => o.value === value);
  expect(hit, `no "${value}" button on the format step`).toBeTruthy();
  return hit!.group;
}

async function openExplorer(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
  await expect(page.getByTestId(`sidenav-storage-${STORAGE}`)).toBeVisible();
}

async function menuVerbs(page: Page, qualified: string): Promise<string[]> {
  const row = page.locator(`[data-fe-path="${qualified}"]`).first();
  await expect(row).toBeVisible();
  await row.click({ button: 'right' });
  const menu = page.getByRole('menu').first();
  await expect(menu).toBeVisible();
  const names = (await menu.locator('[role="menuitem"] .fe-ctx__label').allInnerTexts()).map((s) => s.trim());
  await page.keyboard.press('Escape');
  await expect(menu).toBeHidden();
  return names;
}

test.describe('App plugin: convert — install, choose a target, get the file', () => {
  // One walk, in order: the menu needs the app the first test installs.
  test.describe.configure({ mode: 'serial' });
  guardFixture(APP, test.skip);

  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    await apiLogin(request);
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://`, 'file[]': { name: PNG, mimeType: 'image/png', buffer: PNG_1PX } },
    });
    if (!up.ok()) throw new Error(`upload ${PNG} failed: ${up.status()} ${await up.text()}`);
    // ⚠ `action=newfolder`, with a JSON body. `action=mkdir` is not an action
    // this API has (501 "action not implemented"), and because the failure
    // was only annotated, the folder never existed and the assertion below —
    // "the menu must NOT offer Convert on a folder" — silently measured
    // nothing from the day it was written until 2026-09-23.
    const mk = await request.post('/api/files/manager?action=newfolder', {
      data: { path: `${STORAGE}://`, name: FOLDER },
    });
    if (!mk.ok()) throw new Error(`mkdir ${FOLDER} failed: ${mk.status()} ${await mk.text()}`);
    await removeApp(request, 'convert');
  });

  test.afterAll(async ({ request }) => {
    await apiLogin(request);
    await removeApp(request, 'convert');
    await dropStorageByName(request, STORAGE);
  });

  test('the wizard reviews the converter permissions before installing it', async ({ page }) => {
    await loginAs(page);
    // installThroughWizard asserts that EVERY permission in the manifest is
    // shown in the review and that nothing installs before the tick; the
    // wizard closes on success, so the rows are gone by the time it returns.
    await installThroughWizard(page, APP);
    // Six engines: an app that may run ffmpeg is exactly what the review
    // has to name before it runs anything.
    for (const engine of ['ffmpeg', 'imagemagick', 'libreoffice', 'ghostscript', 'poppler', 'rsvg']) {
      expect(APP.manifest?.permissions ?? []).toContain(`engines:${engine}`);
    }
    await expect(page.getByTestId('app-plugin-convert')).toBeVisible();
  });

  test('the menu offers Convert on a file and not on a folder', async ({ page }) => {
    await openExplorer(page);
    const onFile = await menuVerbs(page, QUALIFIED);
    expect(
      onFile.some((v) => /convert|dönüştür/i.test(v)),
      `the converter's row must be in the file menu: [${onFile.join(', ')}]`,
    ).toBe(true);

    const folderRow = page.locator(`[data-fe-path="${STORAGE}://${FOLDER}"]`).first();
    await expect(folderRow, 'the folder the hook made is in the listing').toBeVisible();
    const onFolder = await menuVerbs(page, `${STORAGE}://${FOLDER}`);
    expect(
      onFolder.some((v) => /convert|dönüştür/i.test(v)),
      `applies.kind is "file", so a folder must not offer it: [${onFolder.join(', ')}]`,
    ).toBe(false);
  });

  test('the options screen is drawable and the target picker is a readable choice', async ({ request }) => {
    await apiLogin(request);
    const s = await openView(request, 'convert', 'options', QUALIFIED);

    // Every v3 renderer rule, measured on what the server returned.
    checkSurface(s, 'convert/options');
    checkLanguages(s, APP.languages, 'convert/options');

    // The wizard opens ON the format step, and says so: the spine names the
    // steps and marks exactly one of them as the one the person is on
    // (`checkSurface` pins the "exactly one" half).
    const spine: Node[] = [];
    walkNodes(s.nodes, (_w, n) => {
      if (n.type === 'steps') spine.push(n);
    });
    expect(spine.length, 'a wizard has to show where the person is in it').toBe(1);
    const items = (spine[0].props?.items as { id: string; state: string }[]) ?? [];
    expect(items.map((i) => i.id), 'Format is the first step').toContain('format');
    expect(items.find((i) => i.state === 'active')?.id, 'it opens on the format step').toBe('format');

    const groups = targetGroups(s);
    expect(groups.length, 'the screen must ask which format to convert to').toBeGreaterThan(0);
    for (const g of groups) {
      expect(
        g.type,
        `${g.key}: the target picker is a select — filex draws it as a row of buttons`,
      ).toBe('select');
      // The category is the HEADING of its own group, which is what makes
      // thirty formats readable without opening anything.
      expect(textOf(g.label).trim(), `${g.key}: a button group with no heading`).not.toBe('');
    }

    const opts = targetChoices(s);
    expect(opts.length, 'a PNG reaches several formats with no engine at all').toBeGreaterThan(3);
    const values = opts.map((o) => o.value);
    expect(new Set(values).size, 'every button is a different format').toBe(values.length);
    expect(values, 'JPEG is reachable from a PNG in pure Go').toContain('jpg');
    for (const o of opts) {
      expect(o.label, `option ${o.value} must be labelled`).not.toBe('');
    }

    // One step asks one thing: Next plus Cancel, and Next is off until a
    // format is chosen.
    const primary = (s.actions ?? []).filter((a) => a.primary);
    expect(primary.length, 'one primary button').toBe(1);
    expect(primary[0].disabled, 'nothing chosen yet, so there is nowhere to go').toBeTruthy();
  });

  test('a format whose engine is missing is in the grey list, not in the buttons', async ({ request }) => {
    await apiLogin(request);
    const s = await openView(request, 'convert', 'options', QUALIFIED);
    // ⚠ Every group, not one field. A `choices(s, 'target')` here would be
    // an empty set that made `offered.has(...)` false for everything, and
    // the loop below would then pass without measuring anything at all.
    const offered = new Set(targetChoices(s).map((o) => o.value));
    expect(offered.size, 'nothing is offered, so nothing below is measured').toBeGreaterThan(0);

    const lists: Node[] = [];
    walkNodes(s.nodes, (_w, n) => {
      if (n.type === 'list') lists.push(n);
    });
    // A test server has no ffmpeg, so the grey list must be there. If a
    // machine somehow has every engine, there is nothing to grey out.
    if (!lists.length) {
      test.info().annotations.push({ type: 'note', description: 'every engine is installed here; no grey list' });
      return;
    }
    const rows = (lists[0].props?.rows as { id: string; cells: Record<string, FieldText> }[]) ?? [];
    expect(rows.length, 'the grey list must name the formats it cannot reach').toBeGreaterThan(0);
    for (const row of rows) {
      expect(offered.has(row.id), `${row.id} is in the grey list, so it must not also be a button`).toBe(false);
      expect(
        textOf(row.cells?.needs).trim(),
        `the grey row for ${row.id} must say which engine it needs`,
      ).not.toBe('');
    }
  });

  test('choosing JPEG queues a job that writes the sibling', async ({ page, request }) => {
    await openExplorer(page);
    await apiLogin(request);

    const opened = await openView(request, 'convert', 'options', QUALIFIED);
    // ⚠ Pressing a format button only SELECTS it: the wizard stays on the
    // format step, says what was chosen, and lights that one button. The
    // group the button sits in is looked up, never hardcoded.
    const jpgGroup = groupHolding(opened, 'jpg');
    const chosen = await surfaceEvent(request, 'convert', 'options', {
      path: QUALIFIED,
      event: 'change',
      state: opened.state ?? {},
      data: { values: { [jpgGroup]: 'jpg' } },
    });
    expect(chosen.surface, 'choosing a format redraws the screen').toBeTruthy();
    checkSurface(chosen.surface!, 'convert/options (jpg chosen)');
    // The answer is carried in the wizard's state, not left in a form value
    // that the next step's values would replace.
    expect(chosen.surface!.state?.target, 'the wizard remembers what was chosen').toBe('jpg');
    expect(chosen.surface!.state?.step, 'a press only selects; it does not move the wizard on').toBe('format');
    expect(chosen.op, 'a press never queues a job').toBeFalsy();
    const lit = Object.entries(formValues(chosen.surface!));
    expect(lit, 'exactly the chosen button is lit').toEqual([[expect.stringMatching(/^target_image__jpg$/), 'jpg']]);
    const nowPrimary = (chosen.surface!.actions ?? []).find((a) => a.primary);
    expect(nowPrimary?.disabled, 'with a format chosen, the wizard can go on').toBeFalsy();

    // Next on the format step, with what the step has lit — as a person
    // presses it. (autoFill here would press every group's first button.)
    const leftFormat = await surfaceEvent(request, 'convert', 'options', {
      path: QUALIFIED,
      event: 'submit',
      action_id: nowPrimary!.id,
      state: chosen.surface!.state ?? {},
      data: { values: formValues(chosen.surface!) },
    });
    expect(leftFormat.op, 'Next on the format step never queues a job').toBeFalsy();
    expect(leftFormat.surface?.state?.step, 'Next leaves the format step').not.toBe('format');
    expect(leftFormat.surface?.state?.target, 'and carries the chosen format on').toBe('jpg');

    // Press through whatever steps this route has (Settings, then Review) to
    // the job. Each screen is checked on the way, and a wizard that cannot be
    // finished is the finding rather than a timeout somewhere unhelpful.
    let surface = leftFormat.surface!;
    let submitted: Awaited<ReturnType<typeof surfaceEvent>> | undefined;
    for (let step = 0; step < 6; step++) {
      checkSurface(surface, `convert/options step ${step + 2}`);
      checkLanguages(surface, APP.languages, `convert/options step ${step + 2}`);
      const primary = (surface.actions ?? []).find((a) => a.primary);
      expect(primary, `step ${step + 2} has no primary button: ${JSON.stringify(surface.actions)}`).toBeTruthy();
      const res = await surfaceEvent(request, 'convert', 'options', {
        path: QUALIFIED,
        event: 'submit',
        action_id: primary!.id,
        state: surface.state ?? {},
        data: { values: autoFill(surface) },
      });
      if (res.op) {
        submitted = res;
        break;
      }
      expect(res.surface, `step ${step + 2} answered neither a screen nor a job`).toBeTruthy();
      expect(
        Object.keys(res.surface!.errors ?? {}),
        `step ${step + 2} refused its own defaults`,
      ).toEqual([]);
      surface = res.surface!;
    }
    expect(submitted, 'the wizard never reached a job').toBeTruthy();
    expect(submitted!.status, `submitting must queue the job: ${JSON.stringify(submitted!.body)}`).toBe(202);
    expect(submitted!.op?.id, 'the queued job has an ops row').toBeTruthy();

    const done = await waitForOp(request, submitted!.op!.id, 60_000);
    expect(done.status, `the conversion finished: ${JSON.stringify(done)}`).toBe('ok');

    const list = await request.get(
      `/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://`)}`,
    );
    const names = ((await list.json()).files as { basename: string }[]).map((f) => f.basename);
    expect(names, `the sibling lands next to the original: [${names.join(', ')}]`).toContain('photo.jpg');

    const raw = await request.get(
      `/api/files/manager?action=download&path=${encodeURIComponent(`${STORAGE}://photo.jpg`)}`,
    );
    const body = await raw.body();
    expect(body.length, 'the output is not empty').toBeGreaterThan(2);
    expect(
      [body[0], body[1]],
      `the output must actually be a JPEG, got ${body.subarray(0, 4).toString('hex')}`,
    ).toEqual([0xff, 0xd8]);

    // And the person sees it without reloading.
    await expect(page.locator(`[data-fe-path="${STORAGE}://photo.jpg"]`).first()).toBeVisible({ timeout: 15_000 });
  });
});
