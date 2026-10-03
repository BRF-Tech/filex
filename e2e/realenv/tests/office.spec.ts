/**
 * realenv office - issue #80's S2 and S5 against a REAL ONLYOFFICE Document
 * Server (docs/ONLYOFFICE.md). e2e/realenv/run.sh office starts filex at
 * http://files.office.test:5212 and the document server at
 * http://office.test, twice:
 *
 *   s2  the document server with JWT_ENABLED=false. filex's "Test now" must
 *       say JWT is not enforced (a warning, `jwt_not_enforced`), and the
 *       failed download (-4) comes with the JWT-off advice: with JWT off the
 *       document server applies its private-address filter to the container
 *       network's address and filex never sees the request. The editor stops
 *       earlier still: the document server checks filex's editor token
 *       against its own secret and refuses it (measured with Docs 9.4).
 *   s5  the document server with JWT on and ANOTHER secret than filex's. The
 *       Test says the document server rejected the signature (it never tried
 *       to download), and the editor ends on the token error.
 *
 * Each case records the Test JSON, the editor's words, and the document
 * server's own log lines, under REALENV_RESULTS.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { loginAs } from '../../helpers/auth';
import { adminApi, env, missing, okJSON, readLog } from '../lib/realenv';

const here = path.dirname(fileURLToPath(import.meta.url));
const scenario = process.env.REALENV_OFFICE ?? '';
const why = missing('REALENV_OFFICE', 'office', 'an ONLYOFFICE Document Server');
const BASE = 'http://files.office.test:5212';
const STORE = 'oo';
const notes: string[] = [];

interface TestAnswer {
  service_to_filex: { checked: boolean; ok: boolean; code?: number; advice?: string; detail?: string; jwt_enforced?: boolean };
  advisories: Array<{ code: string; severity: string }>;
  has_warnings: boolean;
}

function record(name: string, body: string) {
  const dir = process.env.REALENV_RESULTS ?? '/work/results/office';
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, name), body);
}

async function configure(api: APIRequestContext): Promise<TestAnswer> {
  await okJSON(
    await api.patch('/api/admin/external/onlyoffice', {
      data: { enabled: true, url: env('REALENV_OO_URL'), secret: env('REALENV_OO_SECRET'), callback_url: env('REALENV_OO_CALLBACK') },
    }),
    'configure the document server',
  );
  const t = await api.post('/api/admin/external/onlyoffice/test', { data: {} });
  const text = await t.text();
  record(`test-now-${scenario}.json`, text);
  expect(t.ok(), text).toBe(true);
  return JSON.parse(text) as TestAnswer;
}

async function seed(api: APIRequestContext, name: string) {
  const list = await okJSON<Array<{ name: string }>>(await api.get('/api/admin/storages'), 'storages');
  if (!list.some((s) => s.name === STORE)) {
    await okJSON(
      await api.post('/api/admin/storages', {
        data: { name: STORE, driver: 'local', mount_path: '/srv/files', config: { path: '/srv/files' }, enabled: true, read_only: false },
      }),
      'a local storage',
    );
  }
  await okJSON(
    await api.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORE}://`,
        'file[]': {
          name,
          mimeType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
          buffer: fs.readFileSync(path.join(here, '../../fixtures/file-types/letter.docx')),
        },
      },
    }),
    `upload ${name}`,
  );
}

/** Opens the editor and waits for ONLYOFFICE to draw the document or to end
 *  on an error; answers what the page says. */
async function openEditor(page: Page, file: string) {
  // Signed in already (testOnPage).
  await page.goto(`/admin/files/edit?path=${encodeURIComponent(`${STORE}://${file}`)}&mode=edit`);
  const fallback = page.getByTestId('office-fallback');
  const frame = page.frameLocator('iframe[name^="frameEditor"]');
  await expect(page.locator('iframe[name^="frameEditor"], [data-testid="office-fallback"]').first()).toBeVisible({ timeout: 90_000 });
  const deadline = Date.now() + 150_000;
  let state = 'pending';
  while (Date.now() < deadline) {
    if (await fallback.count()) {
      state = 'error';
      break;
    }
    if (await frame.locator('#editor_sdk canvas, #id_viewer').count().catch(() => 0)) {
      await page.waitForTimeout(8000);
      state = (await fallback.count()) ? 'error' : 'drawn';
      break;
    }
    await page.waitForTimeout(1000);
  }
  if (state === 'error') await page.getByTestId('office-diagnosis').waitFor({ timeout: 15_000 }).catch(() => undefined);
  const words = (await fallback.count()) ? (await fallback.innerText()).replace(/\s+/g, ' ').trim() : '';
  const diagnosis = (await page.getByTestId('office-diagnosis').count()) ? (await page.getByTestId('office-diagnosis').innerText()).trim() : '';
  const dir = process.env.REALENV_RESULTS ?? '/work/results/office';
  fs.mkdirSync(dir, { recursive: true });
  await page.screenshot({ path: path.join(dir, `editor-${scenario}.png`) });
  notes.push(`${scenario} editor: state=${state} words="${words}" diagnosis="${diagnosis}"`);
  return { state, words, diagnosis };
}

/** The "Test now" card on the admin page, as the operator sees it. */
async function testOnPage(page: Page) {
  await loginAs(page);
  await page.goto('/admin/external');
  const card = page.getByTestId('legs-onlyoffice');
  await expect(card).toBeVisible();
  const oo = page.locator('div.card').filter({ has: page.getByTestId('overall-onlyoffice') });
  await oo.getByRole('button', { name: 'Test now' }).click();
  await expect(page.getByTestId('leg-callback-onlyoffice')).not.toContainText('not measured yet', { timeout: 60_000 });
  const dir = process.env.REALENV_RESULTS ?? '/work/results/office';
  fs.mkdirSync(dir, { recursive: true });
  await page.screenshot({ path: path.join(dir, `test-now-${scenario}.png`), fullPage: true });
}

test.describe('realenv: issue #80 against a real document server', () => {
  test.skip(!!why, why);
  test.describe.configure({ mode: 'default' });
  test.use({ baseURL: BASE });

  let api: APIRequestContext;
  test.beforeAll(async ({ playwright }) => {
    api = await adminApi(playwright.request, BASE);
  });
  test.afterAll(async () => {
    record(`notes-${scenario}.txt`, notes.join('\n'));
    await api?.dispose();
  });

  test('S2: JWT off on the document server - Test warns that JWT is not enforced, the -4 comes with the JWT-off advice, and the editor stops on the token', async ({ page }) => {
    test.skip(scenario !== 's2', 'the document server of this run has JWT on');
    const t = await configure(api);
    notes.push(`s2 test: ${JSON.stringify(t.service_to_filex)}`);
    expect(t.service_to_filex.jwt_enforced, JSON.stringify(t)).toBe(false);
    expect(t.advisories.map((a) => a.code)).toContain('jwt_not_enforced');
    expect(t.advisories.find((a) => a.code === 'jwt_not_enforced')!.severity).toBe('warning');
    expect(t.has_warnings).toBe(true);
    expect(t.service_to_filex.code).toBe(-4);
    expect(t.service_to_filex.ok).toBe(false);
    expect(t.service_to_filex.advice).toBe('jwt_off');
    // The document server's own words for it: it would not download from a
    // private address.
    const ds = ['converter', 'docservice'].map((d) => readLog(`${env('REALENV_OO_DS_LOGS')}/${d}/out.log`)).join('\n');
    const privateLines = ds.split('\n').filter((l) => /private ip/i.test(l));
    notes.push(`s2 document server log: ${privateLines.slice(-3).join(' | ')}`);
    expect(privateLines.length, ds.slice(-3000)).toBeGreaterThan(0);

    await testOnPage(page);
    await expect(page.getByTestId('advisory-onlyoffice-jwt_not_enforced')).toBeVisible();
    // ...and says what such a server does (measured below, in the editor).
    await expect(page.getByTestId('advisory-onlyoffice-jwt_not_enforced')).toContainText('JWT is off');
    await expect(page.getByTestId('advisory-onlyoffice-jwt_not_enforced')).toContainText('documents do not open');
    // The health check passed: the toast says so and points at the card.
    await expect(page.getByText('Reachable, but not everything is right: see the card')).toBeVisible();
    await expect(page.getByText('Health check failed')).toHaveCount(0);
    await expect(page.getByTestId('leg-callback-onlyoffice')).toContainText('(its error -4)');

    await seed(api, 'S2 letter.docx');
    const r = await openEditor(page, 'S2 letter.docx');
    // Measured with Docs 9.4: a document server with JWT off still checks the
    // token in filex's editor configuration, against its own secret, and
    // refuses it ("checkJwt error ... invalid signature" in its log). The
    // editor ends there, before any download, and the words are text.
    expect(r.state, JSON.stringify(r)).toBe('error');
    expect(r.words).toContain('The document security token is not correctly formed. Please contact your Document Server administrator.');
    expect(r.words).not.toContain('<br');
    const docservice = readLog(`${env('REALENV_OO_DS_LOGS')}/docservice/out.log`);
    expect(docservice).toMatch(/checkJwt error.*invalid signature/);
  });

  test('S5: a secret that does not match - Test says the signature was rejected, and the editor ends on the token error', async ({ page }) => {
    test.skip(scenario !== 's5', 'the document server of this run has JWT off');
    const t = await configure(api);
    notes.push(`s5 test: ${JSON.stringify(t.service_to_filex)}`);
    expect(t.service_to_filex.checked, JSON.stringify(t)).toBe(false);
    expect(t.service_to_filex.detail).toMatch(/rejected the request's signature/);
    expect(t.service_to_filex.jwt_enforced).toBe(true);
    expect(t.advisories.map((a) => a.code)).not.toContain('jwt_not_enforced');

    await testOnPage(page);
    await expect(page.getByTestId('leg-callback-onlyoffice')).toContainText("rejected the request's signature");

    await seed(api, 'S5 letter.docx');
    const r = await openEditor(page, 'S5 letter.docx');
    expect(r.state, JSON.stringify(r)).toBe('error');
    expect(r.words).toContain('The document security token is not correctly formed. Please contact your Document Server administrator.');
    expect(r.words).not.toContain('<br');
  });
});
