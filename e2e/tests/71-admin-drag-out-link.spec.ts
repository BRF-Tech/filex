/**
 * The admin SPA drags a file OUT — onto the desktop — like any other browser
 * surface does (task #71), measured in a real browser with real drags.
 *
 * The admin SPA signs its calls with a bearer (web/src/lib/explorerConfig
 * explorerAuth). The browser's single-file drag-out is a `DownloadURL` on the
 * dataTransfer, which the browser's DOWNLOAD STACK fetches at the drop — with
 * cookies, never with an Authorization header. So until #71 a bearer page
 * offered no drag-out at all (e2e 47 pinned that).
 *
 * Now the server mints a short-lived, single-use, credential-free link to one
 * file (POST /api/files/archive/download {mode:"file"} → /z/<ticket>). The
 * catch is timing: `dragstart` must fill the dataTransfer SYNCHRONOUSLY, so the
 * link is minted before the drag — when the pointer comes to rest on the row
 * and again on the press. What is measured here:
 *
 *   · a listing row and a ⌘K hit, dragged with the mouse: `DownloadURL` is on
 *     the drag, and fetching it with NO credential at all gives the file's
 *     bytes — once;
 *   · the mint's round trip on this machine, and that a drag whose link has
 *     not arrived yet goes without one rather than waiting;
 *   · a folder asks for nothing, and a cookie session (an embed) keeps its
 *     plain download URL and costs no request on hover.
 */
import { test, expect, request as pwRequest, type Page, type Locator } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';

const STORAGE = `e2e-dragout71-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const WORD = 'surukle71';
const FILE = { name: `${WORD}-sözleşme.txt`, body: 'İmzalı sözleşme — sürükle-bırak ile indirildi.\n' };
const OTHERS = Array.from({ length: 6 }, (_, i) => ({ name: `yan-${i + 1}.txt`, body: `yan dosya ${i + 1}\n` }));
const FOLDER = 'Klasör71';
const MINT = /\/api\/files\/archive\/download$/;

async function upload(request: import('@playwright/test').APIRequestContext, dir: string, name: string, body: string) {
  const up = await request.post('/api/files/manager?action=upload', {
    multipart: { path: `${STORAGE}://${dir}`, 'file[]': { name, mimeType: 'text/plain', buffer: Buffer.from(body, 'utf8') } },
  });
  if (!up.ok()) throw new Error(`upload ${name}: ${up.status()} ${await up.text()}`);
}

async function openExplorer(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  // The session this spec is about: the admin SPA's own bearer.
  expect(await page.evaluate(() => !!sessionStorage.getItem('filex.bearer')), 'a bearer session').toBe(true);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
  await page.getByTestId(`sidenav-storage-${STORAGE}`).click();
  await expect(page.locator(`[data-fe-path="${STORAGE}://${FILE.name}"]`).first()).toBeVisible();
}

/**
 * Record what every dragstart put on its dataTransfer. A listener on WINDOW in
 * the bubble phase runs after the row's own handler, still inside dragstart —
 * the one moment the dataTransfer can be read back.
 */
async function probeDrags(page: Page) {
  await page.evaluate(() => {
    const w = window as unknown as { __drags: Array<{ url: string; types: string[]; t: number }> };
    w.__drags = [];
    window.addEventListener('dragstart', (ev) => {
      const dt = ev.dataTransfer;
      w.__drags.push({ url: dt?.getData('DownloadURL') ?? '', types: [...(dt?.types ?? [])], t: performance.now() });
    });
  });
}

async function drags(page: Page): Promise<Array<{ url: string; types: string[] }>> {
  return page.evaluate(() => (window as unknown as { __drags: Array<{ url: string; types: string[] }> }).__drags);
}

/**
 * A real mouse drag of `from`: press, move far enough to start a drag, and let
 * go back on the same row (a file row is not a drop target, so nothing moves).
 */
async function dragOnce(page: Page, from: Locator) {
  const box = (await from.boundingBox())!;
  const x = box.x + Math.min(40, box.width / 3);
  const y = box.y + box.height / 2;
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x + 12, y + 2, { steps: 4 });
  await page.mouse.move(x + 24, y + 1, { steps: 4 });
  await page.mouse.up();
}

/** `mime:name:url` → its parts. */
function parsePayload(payload: string) {
  const m = /^([^:]+):([^:]+):(https?:.*)$/.exec(payload);
  expect(m, `DownloadURL payload: ${payload}`).toBeTruthy();
  return { mime: m![1], name: m![2], url: m![3] };
}

/** Fetch with NO credential — the way the browser's download stack arrives. */
async function fetchAnonymously(url: string) {
  const anon = await pwRequest.newContext();
  try {
    const res = await anon.get(url, { maxRedirects: 0 });
    return { status: res.status(), body: await res.body(), disposition: res.headers()['content-disposition'] ?? '' };
  } finally {
    await anon.dispose();
  }
}

test.describe('Admin SPA — dragging a file out rides a minted link (#71)', () => {
  test.describe.configure({ mode: 'serial' });

  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    await apiLogin(request);
    await upload(request, '', FILE.name, FILE.body);
    for (const o of OTHERS) await upload(request, '', o.name, o.body);
    const mk = await request.post('/api/files/manager?action=newfolder', { data: { path: `${STORAGE}://`, name: FOLDER } });
    if (!mk.ok()) throw new Error(`newfolder: ${mk.status()} ${await mk.text()}`);
    await expect
      .poll(
        async () => {
          const r = await request.get(`/api/files/search?q=${encodeURIComponent(WORD)}&limit=8&scope=all`);
          const b = (await r.json()) as { results?: Array<{ storage?: string }> };
          return (b.results ?? []).filter((h) => h.storage === STORAGE).length;
        },
        { timeout: 30_000, message: 'the index never picked the file up' },
      )
      .toBe(1);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('a listing row: the drag carries a DownloadURL, and that URL downloads the file — once', async ({ page }) => {
    await openExplorer(page);
    await probeDrags(page);
    const row = page.locator(`[data-fe-path="${STORAGE}://${FILE.name}"]`).first();

    // Rest the pointer on the row: that is when the link is asked for.
    const minted = page.waitForResponse((r) => MINT.test(new URL(r.url()).pathname) && r.request().method() === 'POST');
    const t0 = Date.now();
    await row.hover();
    const res = await minted;
    const mintMs = Date.now() - t0;
    expect(res.status()).toBe(200);
    expect(JSON.parse(res.request().postData() ?? '{}')).toEqual({ paths: [`${STORAGE}://${FILE.name}`], mode: 'file' });
    test.info().annotations.push({ type: 'hover → link minted (ms)', description: String(mintMs) });
    console.log(`[#71] hover → link minted: ${mintMs} ms`);

    await dragOnce(page, row);
    const got = await drags(page);
    expect(got, 'one dragstart').toHaveLength(1);
    const { mime, name, url } = parsePayload(got[0].url);
    expect(mime).toMatch(/^text\/plain/);
    expect(name).toBe(FILE.name);
    expect(new URL(url).pathname).toMatch(/^\/z\/[A-Za-z0-9_-]{20,}$/);
    // The drag is still filex's own inside the window (a move onto a folder).
    expect(got[0].types).toContain('application/x-brf-files');

    const first = await fetchAnonymously(url);
    expect(first.status, 'no credential, and still the file').toBe(200);
    expect(first.body.toString('utf8')).toBe(FILE.body);
    expect(first.disposition).toContain('attachment');
    expect(first.disposition).toContain(encodeURIComponent(FILE.name));
    const again = await fetchAnonymously(url);
    expect(again.status, 'a used link is gone').toBe(404);
    await page.screenshot({ path: test.info().outputPath('drag-row.png') });
  });

  test('a ⌘K hit drags out the same way', async ({ page }) => {
    await openExplorer(page);
    await probeDrags(page);
    await page.keyboard.press('Control+k');
    await page.locator('.fe-cmdp__input').fill(WORD);
    const hit = page.locator('.fe-cmdp__item--hit').filter({ has: page.locator('.fe-cmdp__label', { hasText: FILE.name }) });
    await expect(hit).toHaveCount(1, { timeout: 10_000 });
    // #47 withheld the drag in a bearer session; #71 offers it.
    await expect(hit).toHaveAttribute('draggable', 'true');

    const minted = page.waitForResponse((r) => MINT.test(new URL(r.url()).pathname) && r.request().method() === 'POST');
    await hit.hover();
    expect((await minted).status()).toBe(200);
    await dragOnce(page, hit.locator('.fe-cmdp__label'));
    const got = await drags(page);
    expect(got).toHaveLength(1);
    const { name, url } = parsePayload(got[0].url);
    expect(name).toBe(FILE.name);
    const body = await fetchAnonymously(url);
    expect(body.status).toBe(200);
    expect(body.body.toString('utf8')).toBe(FILE.body);
    await page.screenshot({ path: test.info().outputPath('drag-hit.png') });
  });

  test('measured: the mint round trip, and a drag that beats it goes without — it never waits', async ({ page }) => {
    await openExplorer(page);
    await probeDrags(page);
    const times: number[] = [];
    // Rest the pointer on each row in turn, long enough for its link to land,
    // and read the browser's own timing of that request.
    for (const o of OTHERS) {
      const r = page.locator(`[data-fe-path="${STORAGE}://${o.name}"]`).first();
      const wait = page.waitForResponse((x) => MINT.test(new URL(x.url()).pathname));
      await r.hover();
      const res = await wait;
      await res.finished();
      times.push(Math.round(res.request().timing().responseEnd));
    }
    const sorted = [...times].sort((a, b) => a - b);
    const p50 = sorted[Math.floor(sorted.length / 2)];
    const summary = `n=${sorted.length} min=${sorted[0]} p50=${p50} max=${sorted[sorted.length - 1]}`;
    test.info().annotations.push({ type: 'mint round trip (ms, request start → response end)', description: summary });
    console.log(`[#71] mint round trip (ms): ${summary}`);
    expect(sorted.length).toBe(OTHERS.length);

    // Hold the next mint back, then drag a row whose link cannot be there yet.
    let release: () => void = () => undefined;
    const held = new Promise<void>((r) => (release = r));
    let routed: Promise<void> = Promise.resolve();
    await page.route(
      MINT,
      (route) => {
        routed = held.then(() => route.continue());
        return routed;
      },
      { times: 1 },
    );
    const row = page.locator(`[data-fe-path="${STORAGE}://${FILE.name}"]`).first();
    await dragOnce(page, row);
    const fast = await drags(page);
    expect(fast, 'the drag started at once').toHaveLength(1);
    expect(fast[0].url, 'no link yet: no DownloadURL, rather than a URL that 401s').toBe('');
    expect(fast[0].types, 'and the drag is still filex’s own').toContain('application/x-brf-files');
    release();
    await routed;
    // …and once the link is in, the next drag carries it.
    await expect.poll(async () => {
      await row.hover();
      await page.waitForTimeout(150);
      await dragOnce(page, row);
      const all = await drags(page);
      return all[all.length - 1].url;
    }, { timeout: 10_000 }).toMatch(/:https?:\/\/[^/]+\/z\//);
  });

  test('a folder asks for no link', async ({ page }) => {
    await openExplorer(page);
    await probeDrags(page);
    const mints: string[] = [];
    page.on('request', (r) => {
      if (MINT.test(new URL(r.url()).pathname)) mints.push(r.postData() ?? '');
    });
    const folder = page.locator(`[data-fe-path="${STORAGE}://${FOLDER}"]`).first();
    await folder.hover();
    await page.waitForTimeout(500);
    expect(mints, 'hovering a folder costs nothing').toEqual([]);
  });

  test('a cookie session (an embed) keeps its plain download URL and costs no request on hover', async ({ page }) => {
    await page.addInitScript(() => sessionStorage.removeItem('filex.bearer'));
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
    await page.getByTestId(`sidenav-storage-${STORAGE}`).click();
    const row = page.locator(`[data-fe-path="${STORAGE}://${FILE.name}"]`).first();
    await expect(row).toBeVisible();
    await probeDrags(page);
    const mints: string[] = [];
    page.on('request', (r) => {
      if (MINT.test(new URL(r.url()).pathname)) mints.push(r.postData() ?? '');
    });
    await row.hover();
    await page.waitForTimeout(500);
    await dragOnce(page, row);
    expect(mints, 'no mint: the cookie travels with the plain URL').toEqual([]);
    const got = await drags(page);
    const { url } = parsePayload(got[0].url);
    expect(new URL(url).pathname).toBe('/api/files/manager');
  });

  test('the drop is in the audit log, for the admin who dragged it', async ({ request }) => {
    await apiLogin(request);
    const res = await request.get('/api/admin/audit?action=file.download_link&limit=20');
    expect(res.ok(), `audit: ${res.status()}`).toBe(true);
    const body = (await res.json()) as { items?: Array<Record<string, unknown>>; entries?: Array<Record<string, unknown>> } | Array<Record<string, unknown>>;
    const rows = Array.isArray(body) ? body : (body.items ?? body.entries ?? []);
    const mine = rows.filter((r) => JSON.stringify(r).includes(`${STORAGE}://${FILE.name}`));
    expect(mine.length, JSON.stringify(rows).slice(0, 600)).toBeGreaterThanOrEqual(2);
  });
});
