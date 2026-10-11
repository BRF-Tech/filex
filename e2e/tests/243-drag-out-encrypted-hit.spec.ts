/**
 * 243 - a file of an encrypted folder is never handed OUT of the window, from
 * a ⌘K hit or from any other row that is not a folder listing (task #243,
 * wiring:e2, #189).
 *
 * The drag-out (lib/dragOut, FileExplorer handDragOut) hands a file to the
 * operating system: the browser's `DownloadURL` (the plain download URL in a
 * cookie session, a minted link in a bearer one) or the desktop shell's OS
 * drag. For a file inside an encrypted folder those bytes are ciphertext, and
 * the OS would save them under the file's name as if they were the document,
 * so the explorer refuses it. Up to 0.56 the refusal read the open folder
 * (`e2eActive`) and an `e2e_root` the rows handed to it never carried
 * (`hitItem` and the listing's drag kept only path, name and kind): a ⌘K hit,
 * or a row on Recent, dragged while a plain folder was open, went out.
 *
 * Measured here, in a real browser against a real binary:
 *   · cookie session (an embed): the ⌘K hit of a file in an encrypted folder
 *     is not draggable and its dragstart carries no `DownloadURL`; a plain
 *     hit still does, and its URL is the file;
 *   · bearer session (the admin app): hovering and pressing that hit asks the
 *     server for no link (#242), and the drag carries none;
 *   · the same file on Recent: the in-app drag is still there, the
 *     `DownloadURL` is not.
 *
 * What went out before the fix is written into the test's annotations
 * (`secret hit DownloadURL`, `bytes behind it`) before the assertion fails.
 *
 * ⚠ Language pinned to English on the ACCOUNT (lesson #616) and restored.
 */
import { test, expect, type APIRequestContext, type Locator, type Page } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';
import { settled } from '../helpers/stable';
import { scanNow } from '../helpers/scan';

const STORE = `e2e-dragout243-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORE}`;
const PW = 'correct horse battery staple';
const PREFS = '/api/me/prefs?surface=web';
const FOLDER = 'Kasa243';
// One made-up word in both names and nowhere else on the server.
const WORD = 'kizilcik243';
const SECRET = { name: `${WORD}-gizli.txt`, body: 'gizli içerik: 243\n' };
const PLAIN = { name: `${WORD}-duz.txt`, body: 'düz içerik: 243\n' };
const MINT = /\/api\/files\/archive\/download$/;
const FE_DND_MIME = 'application/x-brf-files';
/** The first bytes of every file the browser encrypts (e2e container). */
const MAGIC = 'filexe2e';

type Hit = { id?: number; name?: string; path?: string; storage?: string; encrypted?: string; e2e_root?: string };

let api: APIRequestContext;
let prefsBefore: Record<string, unknown> = {};
let secretHit: Hit | null = null;

async function initPage(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem('filex.installPrompt.dismissed', '1');
  });
}

/** The drive's root in the drive shell - where the folder is created. */
async function openDrive(page: Page) {
  await initPage(page);
  await loginAs(page);
  await page.goto(`/drive/explore?storage=${encodeURIComponent(STORE)}`);
  await expect(page.getByTestId('sidenav-new').first()).toBeVisible({ timeout: 30_000 });
}

/**
 * The drive's root in the admin app's explorer, in the session asked for:
 * its own bearer, or the cookie alone (how an embed runs - e2e 47 and 71 take
 * the same two). A plain folder is open: nothing on screen is encrypted.
 */
async function openRoot(page: Page, session: 'cookie' | 'bearer') {
  await initPage(page);
  if (session === 'cookie') await page.addInitScript(() => sessionStorage.removeItem('filex.bearer'));
  await loginAs(page);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORE)}`);
  await page.getByTestId(`sidenav-storage-${STORE}`).click();
  await expect(page.locator(`[data-fe-path="${STORE}://${PLAIN.name}"]`).first()).toBeVisible({ timeout: 20_000 });
  await expect(page.locator(`[data-fe-path^="${STORE}://${FOLDER}"]`).first()).toBeVisible();
  expect(await page.evaluate(() => !!sessionStorage.getItem('filex.bearer')), `a ${session} session`).toBe(
    session === 'bearer',
  );
}

function row(page: Page, name: string) {
  return page
    .locator('.fe-list__row[data-fe-path]')
    .filter({ has: page.locator('.fe-list__name', { hasText: new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`) }) })
    .first();
}

async function openFolder(page: Page, name: string) {
  const target = await settled(row(page, name));
  const wire = ((await target.getAttribute('data-fe-path')) ?? '').replace(/\/+$/, '');
  const listed = page
    .waitForResponse(
      (r) => {
        const u = new URL(r.url());
        return u.searchParams.get('action') === 'index' && (u.searchParams.get('path') ?? '').replace(/\/+$/, '') === wire;
      },
      { timeout: 20_000 },
    )
    .catch(() => undefined);
  await target.dblclick();
  await target.dispose();
  if (wire) await listed;
  await page.waitForTimeout(700);
}

async function createEncryptedFolder(page: Page, name: string) {
  await page.getByTestId('sidenav-new').first().click();
  await page
    .locator('.fe-ctx__item', { has: page.locator('.fe-ctx__label', { hasText: /^New folder$/ }) })
    .first()
    .click();
  await page.getByText('Create encrypted folder…', { exact: true }).click();
  const dialog = page.locator('.fe-modal__card').filter({ has: page.getByTestId('e2e-create-names') });
  await expect(dialog).toBeVisible();
  await dialog.locator('input[type="text"]').first().fill(name);
  const pws = dialog.locator('input[type="password"]');
  await pws.nth(0).fill(PW);
  await pws.nth(1).fill(PW);
  // Contents only: the names stay plaintext, so the server's search finds the
  // file by its name - the way a ⌘K hit of it exists at all.
  await expect(dialog.getByTestId('e2e-level-content')).toBeChecked();
  await dialog.getByTestId('e2e-create-ack').check();
  await dialog.getByRole('button', { name: 'Create encrypted folder', exact: true }).click();
  const keyEl = page.locator('.fe-e2e-rk__key');
  await expect(keyEl).toBeVisible({ timeout: 20_000 });
  await page.locator('.fe-e2e-rk .fe-e2e-ack input[type="checkbox"]').check();
  await page.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(keyEl).toBeHidden();
}

async function uploadFile(page: Page, name: string, body: string) {
  const answered = page.waitForResponse(
    (r) => r.request().method() === 'POST' && new URL(r.url()).searchParams.get('action') === 'upload',
  );
  await page.locator('input[type="file"]').first().setInputFiles({ name, mimeType: 'text/plain', buffer: Buffer.from(body, 'utf8') });
  await answered;
  await expect(row(page, name)).toBeVisible({ timeout: 15_000 });
}

/** ⌘K, the word, and the "Everywhere" rows once they have arrived. */
async function paletteHits(page: Page) {
  await page.keyboard.press('Control+k');
  const input = page.locator('.fe-cmdp__input');
  await expect(input).toBeVisible();
  await input.fill(WORD);
  const rows = page.locator('.fe-cmdp__item--hit');
  await expect(rows).toHaveCount(2, { timeout: 15_000 });
  const pick = (name: string) => rows.filter({ has: page.locator('.fe-cmdp__label', { hasText: name }) });
  return { secret: pick(SECRET.name), plain: pick(PLAIN.name) };
}

/** What a dragstart on `el` put on the drag: the browser's own download, and
 *  the types (the in-app payload is `application/x-brf-files`). */
async function dragProbe(locator: Locator): Promise<{ url: string; types: string[]; prevented: boolean }> {
  return locator.evaluate((el) => {
    const dt = new DataTransfer();
    const ev = new DragEvent('dragstart', { bubbles: true, cancelable: true, dataTransfer: dt });
    el.dispatchEvent(ev);
    return { url: dt.getData('DownloadURL'), types: [...dt.types], prevented: ev.defaultPrevented };
  });
}

/** `mime:name:url` → its parts. */
function parsePayload(payload: string) {
  const m = /^([^:]+):([^:]+):(https?:.*)$/.exec(payload);
  expect(m, `DownloadURL payload: ${payload}`).toBeTruthy();
  return { mime: m![1], name: m![2], url: m![3] };
}

/** The first bytes behind a URL, fetched with this page's session. */
async function headOf(page: Page, url: string): Promise<string> {
  return page.evaluate(async (u) => {
    const r = await fetch(u, { credentials: 'include' });
    const b = new Uint8Array(await r.arrayBuffer());
    return `${r.status} ${String.fromCharCode(...b.slice(0, 8))}`;
  }, url);
}

/** Write down what went out BEFORE the assertion, so a red run says it. */
async function record(page: Page, what: string, probe: { url: string; types: string[]; prevented: boolean }) {
  test.info().annotations.push({ type: `${what} DownloadURL`, description: probe.url || '(none)' });
  test.info().annotations.push({ type: `${what} drag types`, description: JSON.stringify(probe.types) });
  let behind = '';
  if (probe.url) {
    const m = /^([^:]+):([^:]+):(https?:.*)$/.exec(probe.url);
    if (m) behind = await headOf(page, m[3]);
    test.info().annotations.push({ type: `${what} bytes behind it`, description: behind });
  }
  console.log(
    `[#243] ${what}: DownloadURL=${probe.url || '(none)'} behind=${behind || '-'} types=${JSON.stringify(probe.types)} prevented=${probe.prevented}`,
  );
}

// ⚠ Not serial: each check stands on its own, so a run on a tree without the
// fix measures all three paths instead of stopping at the first red. The
// folder is made once per worker, in beforeAll (a worker that restarts after
// a red loads the module again and makes its own, under a new name).
test.describe('A file of an encrypted folder is not dragged out of a ⌘K hit or of Recent (#243)', () => {
  test.beforeAll(async ({ request, playwright, baseURL, browser }) => {
    test.setTimeout(150_000);
    await dropStorageByName(request, STORE);
    const storeId = (await seedLocalStorage(request, STORE, MOUNT)).id;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const got = await api.get(PREFS);
    const doc = got.ok() ? (await got.json()).prefs : {};
    prefsBefore = doc && typeof doc === 'object' && !Array.isArray(doc) ? doc : {};
    expect((await api.put(PREFS, { data: { prefs: { ...prefsBefore, locale: 'en' } } })).ok()).toBe(true);
    await setAccountViewMode(api, 'list');
    const up = await api.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORE}://`, 'file[]': { name: PLAIN.name, mimeType: 'text/plain', buffer: Buffer.from(PLAIN.body, 'utf8') } },
    });
    expect(up.ok(), `upload ${PLAIN.name}: ${up.status()}`).toBe(true);

    // The encrypted folder and its file, made the way a person makes them.
    const ctx = await browser.newContext({ baseURL });
    const page = await ctx.newPage();
    try {
      await openDrive(page);
      await createEncryptedFolder(page, FOLDER);
      await openFolder(page, FOLDER);
      await uploadFile(page, SECRET.name, SECRET.body);
    } finally {
      await ctx.close();
    }
    const stored = fs.readFileSync(path.join(storageRoot(MOUNT), FOLDER, SECRET.name));
    expect(stored.subarray(0, 8).toString('latin1'), 'the upload is ciphertext on disk').toBe(MAGIC);

    // ⚠ 60 s, and one "Scan now" after 15: in the WebKit leg of a targeted
    // run (f243-2) the index still had only one of the two after 30 s.
    let hits: Hit[] = [];
    const t0 = Date.now();
    let scanned = false;
    await expect
      .poll(
        async () => {
          if (!scanned && Date.now() - t0 > 15_000) {
            scanned = true;
            await scanNow(api, storeId);
          }
          const r = await api.get(`/api/files/search?q=${encodeURIComponent(WORD)}&limit=8&scope=all`);
          hits = (((await r.json()) as { results?: Hit[] }).results ?? []).filter((h) => h.storage === STORE);
          return hits.length;
        },
        { timeout: 60_000, message: 'the index never picked the two files up' },
      )
      .toBe(2);
    secretHit = hits.find((h) => h.name === SECRET.name) ?? null;
    // The server says it on the hit: the explorer has only to read it.
    expect(secretHit).toMatchObject({ encrypted: 'folder', e2e_root: `${STORE}://${FOLDER}` });
  });

  test.afterAll(async () => {
    await api?.put(PREFS, { data: { prefs: prefsBefore } }).catch(() => undefined);
    await dropStorageByName(api, STORE).catch(() => undefined);
    await api?.dispose();
  });

  test('cookie session: the ⌘K hit is not handed out, a plain hit is', async ({ page }) => {
    await openRoot(page, 'cookie');
    const { secret, plain } = await paletteHits(page);

    const s = await dragProbe(secret);
    await record(page, 'secret hit', s);
    test.info().annotations.push({ type: 'secret hit draggable', description: String(await secret.getAttribute('draggable')) });
    expect(s.url, 'no DownloadURL for a file of an encrypted folder').toBe('');
    await expect(secret, 'nothing to drag it out with').toHaveAttribute('draggable', 'false');

    // The control: a plain hit, same palette, same session.
    await expect(plain).toHaveAttribute('draggable', 'true');
    const p = await dragProbe(plain);
    const { name, url } = parsePayload(p.url);
    expect(name).toBe(PLAIN.name);
    const body = await page.evaluate(async (u) => (await fetch(u, { credentials: 'include' })).text(), url);
    expect(body).toBe(PLAIN.body);
    await page.screenshot({ path: test.info().outputPath('palette-cookie.png') });
  });

  test('bearer session: hovering and pressing the hit mints no link, and the drag carries none', async ({ page }) => {
    await openRoot(page, 'bearer');
    const mints: string[] = [];
    page.on('request', (r) => {
      if (r.method() === 'POST' && MINT.test(new URL(r.url()).pathname)) mints.push(r.postData() ?? '');
    });
    const { secret, plain } = await paletteHits(page);

    // The control: the plain hit's link IS asked for on hover, so the silence
    // below is the rule and not a measurement that could not hear.
    const minted = page.waitForResponse((r) => r.request().method() === 'POST' && MINT.test(new URL(r.url()).pathname));
    await plain.hover();
    expect((await minted).status()).toBe(200);

    await secret.hover();
    await secret.dispatchEvent('pointerdown');
    await page.waitForTimeout(800);
    const s = await dragProbe(secret);
    await record(page, 'secret hit (bearer)', s);
    test.info().annotations.push({ type: 'mint requests', description: JSON.stringify(mints) });
    expect(mints.filter((m) => m.includes(FOLDER)), 'no link asked for the encrypted file').toEqual([]);
    expect(s.url, 'and no DownloadURL').toBe('');
  });

  test('cookie session: the same file on Recent keeps its in-app drag and is not handed out', async ({ page }) => {
    expect(secretHit?.id, 'the setup found the file').toBeTruthy();
    const opened = await api.post('/api/files/manager/recent', { data: { node_id: secretHit!.id } });
    expect(opened.ok(), `recent: ${opened.status()}`).toBe(true);
    await openRoot(page, 'cookie');
    const button = page.getByTestId('sidenav-view-recent');
    await Promise.all([
      page.waitForResponse((r) => /\/api\/files\/manager\/recent\b/.test(r.url()) && r.ok()),
      button.click(),
    ]);
    await expect(button).toHaveAttribute('aria-current', 'page');
    const recentRow = page.locator(`[data-fe-path="${STORE}://${FOLDER}/${SECRET.name}"]`).first();
    await expect(recentRow).toBeVisible({ timeout: 15_000 });

    const s = await dragProbe(recentRow);
    await record(page, 'secret on Recent', s);
    // The drag itself is filex's own (a move onto a folder still works) …
    expect(s.types, 'the in-app drag').toContain(FE_DND_MIME);
    // … and the browser is handed nothing to download.
    expect(s.url, 'no DownloadURL').toBe('');
  });
});
