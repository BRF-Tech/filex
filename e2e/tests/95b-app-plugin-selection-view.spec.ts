/**
 * 95b-app-plugin-selection-view — a modal app screen opened on a SELECTION
 * keeps the whole selection until its job is queued (filex #64).
 *
 * The bug, as a person met it (2026-09-24, the app-template check): an action
 * that applies to several files and opens a screen was run on two files. The
 * first screen said "2 files: metin.md, notes.txt". The first edit in its form
 * (the debounced `change`) — or the submit — came back saying "the file
 * metin.md", the field that only a multi-file screen shows vanished, and the
 * queued job ran on ONE file (`sources: ["metin.md"]`). The opening `run` had
 * carried every path; every later event echoed only `path`, the first row.
 * Neither the Go suite nor the API path saw it, because both send `paths`
 * themselves — only the browser's own conversation dropped them.
 *
 * The walk, through the real explorer and the real `echo` module (its `gather`
 * action opens the `picks` modal, which prints how many files each event was
 * handed and which; its submit queues `gather` on all of them):
 *   1. install echo through the wizard;
 *   2. tick three .txt files (a fourth stays unticked), right-click → Gather;
 *   3. the screen says n=3; type in its form → the `change` it sends names
 *      the three files and the redrawn screen still says n=3;
 *   4. Gather → the submit names the three files, the job is queued on the
 *      three, and three results land beside them — none beside the fourth.
 *
 * The fixture is backend/internal/wasmplugin/testdata/echo (built by
 * scripts/build-wasm-fixture.sh); the spec skips without it unless
 * FILEX_REQUIRE_WASM_FIXTURE=1 makes that a failure.
 *
 * ⚠ Named `95b` so it runs right after 95 (the other echo spec) and not
 * before it: 95 reads the newest `plugin-action` row off the ops list, and a
 * finished row of ours at the top of that list is not a race worth adding.
 */
import { test, expect, type Page, type Request } from '@playwright/test';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName, waitForOp } from '../helpers/seed';
import { guardFixture, installThroughWizard, type AppFixture, type AppManifest } from '../helpers/appPlugin';
import { removeApp } from '../helpers/surface';

const FIXTURE_DIR = resolve(dirname(fileURLToPath(import.meta.url)), '../../backend/internal/wasmplugin/testdata/echo');
const WASM = resolve(FIXTURE_DIR, 'echo.wasm');
const MANIFEST = resolve(FIXTURE_DIR, 'manifest.json');
const PRESENT = existsSync(WASM);
const ECHO: AppFixture = {
  name: 'echo',
  present: PRESENT,
  wasm: WASM,
  manifestPath: MANIFEST,
  manifest: PRESENT ? (JSON.parse(readFileSync(MANIFEST, 'utf8')) as AppManifest) : undefined,
  languages: ['en', 'tr'],
  skipReason: `echo.wasm not built: bash scripts/build-wasm-fixture.sh (${WASM})`,
};

const STORAGE = `e2e-pick-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const FILES: Record<string, string> = { 'a.txt': 'alpha', 'b.txt': 'bravo', 'c.txt': 'charlie', 'd.txt': 'delta' };
const PICKED = ['a.txt', 'b.txt', 'c.txt'];
const QUALIFIED = PICKED.map((n) => `${STORAGE}://${n}`);

const row = (page: Page, name: string) => page.locator(`[data-fe-path="${STORAGE}://${name}"]`).first();

/** The body the browser sent for one view event of `picks`. */
function isPicksEvent(event: string) {
  return (r: Request) =>
    r.method() === 'POST' && r.url().includes('/plugins/views/echo/picks/event') && r.postDataJSON()?.event === event;
}

test.describe('App plugins — a modal on a selection keeps the selection', () => {
  test.describe.configure({ mode: 'serial' });
  guardFixture(ECHO, test.skip);

  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    await apiLogin(request);
    for (const [name, body] of Object.entries(FILES)) {
      const up = await request.post('/api/files/manager?action=upload', {
        multipart: { path: `${STORAGE}://`, 'file[]': { name, mimeType: 'text/plain', buffer: Buffer.from(body) } },
      });
      if (!up.ok()) throw new Error(`upload ${name} failed: ${up.status()} ${await up.text()}`);
    }
    await removeApp(request, 'echo');
  });

  test.afterAll(async ({ request }) => {
    await apiLogin(request);
    await removeApp(request, 'echo');
    await dropStorageByName(request, STORAGE);
  });

  test('install the fixture app', async ({ page }) => {
    await loginAs(page);
    await installThroughWizard(page, ECHO);
  });

  test('three ticked files stay three through change and submit', async ({ page, request }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
    await expect(page.getByTestId(`sidenav-storage-${STORAGE}`)).toBeVisible();

    // Tick, never click: a click on a row OPENS it (the suite pins the
    // one-click open gesture), the checkbox is what selects.
    for (const name of PICKED) {
      await row(page, name).locator('.fe-list__check').click();
      await expect(row(page, name)).toHaveAttribute('aria-selected', 'true');
    }
    await expect(row(page, 'd.txt')).toHaveAttribute('aria-selected', 'false');

    await row(page, 'c.txt').click({ button: 'right' });
    const opened = page.waitForRequest((r) => r.method() === 'POST' && r.url().includes('/plugins/actions/echo/gather/run'));
    await page.getByRole('menuitem', { name: /^(Gather|Topla)/ }).click();
    expect((await opened).postDataJSON()?.paths, 'the run carries the selection').toEqual(QUALIFIED);

    const view = page.getByTestId('plugin-view');
    await expect(view).toBeVisible();
    await expect(view).toContainText(`picks open n=3: ${QUALIFIED.join(', ')}`);

    // ⚠⚠ The event that used to shrink the selection: the first form edit.
    const changed = page.waitForRequest(isPicksEvent('change'));
    const redrawn = page.waitForResponse((r) => isPicksEvent('change')(r.request()));
    await view.locator('#fe-cf-note').pressSequentially('hello');
    const change = (await changed).postDataJSON();
    expect(change.paths, 'the change names every ticked file').toEqual(QUALIFIED);
    expect((await redrawn).status()).toBe(200);
    await expect(view).toContainText(`picks change n=3: ${QUALIFIED.join(', ')}`);
    await expect(view).not.toContainText('n=1');

    // ⚠ The typing may have produced more than one debounced change; the
    // submit waits for the screen to settle on what was typed.
    await expect(view.locator('#fe-cf-note')).toHaveValue('hello');
    const submitted = page.waitForRequest(isPicksEvent('submit'));
    const queuedRes = page.waitForResponse((r) => isPicksEvent('submit')(r.request()));
    await page.getByTestId('plugin-view-action-gather').click();
    const submit = (await submitted).postDataJSON();
    expect(submit.paths, 'the submit names every ticked file').toEqual(QUALIFIED);
    expect(submit.data?.values?.note).toBe('hello');
    const queued = await queuedRes;
    expect(queued.status(), await queued.text()).toBe(202);
    await expect(view).toBeHidden();

    // The job ran on the three — as the ops row says and as the storage shows.
    await apiLogin(request);
    const opId = ((await queued.json()) as { op: { id: number } }).op.id;
    const done = (await waitForOp(request, opId, 30_000)) as { status: string; sources?: string[] };
    expect(done.status).toBe('ok');
    expect(done.sources).toEqual(PICKED);

    const list = await request.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://`)}`);
    const names = ((await list.json()).files as { basename: string }[]).map((f) => f.basename);
    for (const name of PICKED) {
      const out = name.replace('.txt', '-gathered.txt');
      expect(names, `${out} was written`).toContain(out);
      const raw = await request.get(`/api/files/manager?action=download&path=${encodeURIComponent(`${STORAGE}://${out}`)}`);
      expect(await raw.text()).toBe(`hello:${FILES[name]}`);
    }
    expect(names, 'the unticked file was left alone').not.toContain('d-gathered.txt');

    // And the explorer shows the results without a reload.
    for (const name of PICKED) {
      await expect(row(page, name.replace('.txt', '-gathered.txt'))).toBeVisible({ timeout: 10_000 });
    }
  });
});
