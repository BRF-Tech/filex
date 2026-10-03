/**
 * 192-job-opens-its-result - a page that queued a job follows it, and the
 * job's result can carry the person on to a screen on what it made
 * (filex #78).
 *
 * The bug, as the owner met it: "İmza sihirbazı çalışmıyor gibi gözüküyor."
 * A DOCX sent for signature opens the signing app's page, which says the
 * document has to be a PDF first and offers "Convert to PDF". Pressed, the
 * conversion was queued and the page said "The job is queued ... you will be
 * told when it lands". Nothing ever told anybody: the page did not follow
 * the job, and the explorer in the other tab announces only the jobs IT
 * queued. The PDF landed in silence and the wizard never went on to it.
 *
 * The signing app no longer converts anything (filex-sign 0.3 signs PDFs
 * only; an office document goes through the Convert app first), but what
 * the host learned here is for every app: a page follows the job it queued,
 * and a job can send its person on to what it made.
 *
 * The walk, through the real page and the real `echo` module (its `wizard`
 * page has one button, which queues `upper` on the page's file and asks the
 * job - `then: "wizard"` - to bring the person back to the wizard on what it
 * wrote: the shape of the flow that found it, a job that makes a file and a
 * screen that goes on with it, without LibreOffice or an app build):
 *   1. install echo through the wizard;
 *   2. open the wizard page on notes.txt and press its button: the job is
 *      queued and the page says so;
 *   3. the job finishes and THE SAME TAB lands on the wizard for
 *      notes-upper.txt - the file the job wrote, under the name the host gave
 *      it - with the job's work in it.
 *
 * Red on the code before #78: the page stays on "The job is queued" and the
 * address never changes (step 3 times out).
 *
 * The fixture is backend/internal/wasmplugin/testdata/echo (built by
 * scripts/build-wasm-fixture.sh); the spec skips without it unless
 * FILEX_REQUIRE_WASM_FIXTURE=1 makes that a failure.
 */
import { test, expect } from '@playwright/test';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';
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

const STORAGE = `e2e-jobopen-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const SOURCE = 'notes.txt';
const RESULT = 'notes-upper.txt';
const BODY = 'sign here';

/** The `path=` an app page's address carries, decoded. */
function pagePath(url: string): string {
  return new URL(url).searchParams.get('path') ?? '';
}

test.describe('App plugins - a finished job carries its page on to what it made', () => {
  test.describe.configure({ mode: 'serial' });
  guardFixture(ECHO, test.skip);

  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    await apiLogin(request);
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://`, 'file[]': { name: SOURCE, mimeType: 'text/plain', buffer: Buffer.from(BODY) } },
    });
    if (!up.ok()) throw new Error(`upload ${SOURCE} failed: ${up.status()} ${await up.text()}`);
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

  test('the page follows its job and lands on the wizard for the file the job wrote', async ({ page, request }) => {
    test.setTimeout(90_000);
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto(`/admin/apps/echo/wizard?path=${encodeURIComponent(`${STORAGE}://${SOURCE}`)}`);
    await expect(page.getByTestId('plugin-page-title')).toHaveText('Wizard');
    await expect(page.getByTestId('plugin-page')).toContainText('page open');
    expect(pagePath(page.url())).toBe(`${STORAGE}://${SOURCE}`);

    // The press queues the job (the primary button arrives as `submit`).
    const queued = page.waitForResponse(
      (r) => r.request().method() === 'POST' && r.url().includes('/plugins/views/echo/wizard/event'),
    );
    await page.getByTestId('plugin-page-action-upper').click();
    const answer = await queued;
    expect(answer.status(), await answer.text()).toBe(202);

    // ⚠⚠ The whole point: the SAME tab goes on to the wizard on the output.
    // The page used to stop at "The job is queued" for good.
    await page.waitForURL((u) => pagePath(u.toString()) === `${STORAGE}://${RESULT}`, { timeout: 45_000 });
    expect(new URL(page.url()).pathname).toMatch(/\/apps\/echo\/wizard$/);
    await expect(page.getByTestId('plugin-page-title')).toHaveText('Wizard');
    await expect(page.getByTestId('plugin-page')).toContainText('page open');
    await expect(page.getByTestId('plugin-page-queued')).toHaveCount(0);

    // And it is the file the job really wrote.
    await apiLogin(request);
    const raw = await request.get(
      `/api/files/manager?action=download&path=${encodeURIComponent(`${STORAGE}://${RESULT}`)}`,
    );
    expect(raw.ok()).toBe(true);
    expect(await raw.text()).toBe(BODY.toUpperCase());
  });
});
