/**
 * 95-app-plugins — an app plugin, end to end, through the browser.
 *
 * The fixture is the same `echo` module the Go suite runs against
 * (backend/internal/wasmplugin/testdata/echo, built by
 * scripts/build-wasm-fixture.sh). The spec skips when it has not been built —
 * on CI it is built before the browsers start.
 *
 * What is measured, in one walk:
 *   1. Admin → Plugins → Apps → Install (files): the wizard stops at the
 *      permission review, lists every permission, and only installs once the
 *      administrator ticks "I understand".
 *   2. The explorer's context menu on a .txt file offers the app's action
 *      (`Upper-case`) under the built-in verbs, and not on a .png.
 *   3. Running it queues a job the ops tray shows; when it lands, the sibling
 *      output exists next to the input with the app's bytes.
 *   4. v2 — ONE app contributes DIFFERENT rows to the same menu depending on
 *      the file's state (`applies.state` / `no_state`), and a `hidden` action
 *      is in no menu at all.
 *   5. v2 — an action whose view is placed `page` opens a TAB, not a dialog,
 *      at `{base}apps/{plugin}/{view}?path=…`.
 *   6. v2 — a file an app has locked says so on its row, and the server
 *      refuses to rename it with `423 locked` until an administrator lifts
 *      the lock through the admin API.
 *   7. The app's public page walk over the anonymous API: an app page is a
 *      SHARE (`/s/<token>`, `/api/public/s/<token>`), so PIN required → wrong
 *      PIN refused → right PIN unlocks → the surface answers an `open` event,
 *      and the retired `/api/p/*` prefix still 301s to it.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName, waitForOp } from '../helpers/seed';
import { APP_INSTALL_ALLOWANCE_MS } from '../helpers/appPlugin';

const FIXTURE_DIR = resolve(dirname(fileURLToPath(import.meta.url)), '../../backend/internal/wasmplugin/testdata/echo');
const WASM = resolve(FIXTURE_DIR, 'echo.wasm');
const MANIFEST = resolve(FIXTURE_DIR, 'manifest.json');
const HAVE_FIXTURE = existsSync(WASM);

const STORAGE = `e2e-apps-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const TXT = 'notes.txt';
const PNG = 'pic.png';
/** Its own file, so the state rows are measured before and after ONE run. */
const STATEFUL = 'stateful.txt';
/** Its own file again: a lock is not something to leave on a shared fixture. */
const LOCKED = 'frozen.txt';

async function openExplorer(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
  await expect(page.getByTestId(`sidenav-storage-${STORAGE}`)).toBeVisible();
}

/** Poll the ops queue for the first row that matches. `undefined` on timeout. */
type OpsRow = { id: number; kind: string; action?: string; status: string };
async function pollForOp(
  request: APIRequestContext,
  match: (o: OpsRow) => boolean,
  timeoutMs = 15_000,
): Promise<OpsRow | undefined> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const res = await request.get('/api/files/ops');
    if (res.ok()) {
      const found = ((await res.json()).ops as OpsRow[]).find((o) => o && o.kind === 'plugin-action' && match(o));
      if (found) return found;
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  return undefined;
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

test.describe('App plugins — install, run, output, public page', () => {
  // One walk in order: the menu and the page need the app the first test
  // installs, so these must not be spread across workers.
  test.describe.configure({ mode: 'serial' });
  test.skip(!HAVE_FIXTURE, `echo.wasm not built: bash scripts/build-wasm-fixture.sh (${WASM})`);

  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    await apiLogin(request);
    for (const [name, mime, body] of [
      [TXT, 'text/plain', 'the quick brown fox'],
      [PNG, 'image/png', 'not really a png'],
      [STATEFUL, 'text/plain', 'state'],
      [LOCKED, 'text/plain', 'frozen'],
    ] as const) {
      const up = await request.post('/api/files/manager?action=upload', {
        multipart: { path: `${STORAGE}://`, 'file[]': { name, mimeType: mime, buffer: Buffer.from(body) } },
      });
      if (!up.ok()) throw new Error(`upload ${name} failed: ${up.status()} ${await up.text()}`);
    }
    // A previous run may have left the app installed.
    const list = await request.get('/api/admin/app-plugins');
    if (list.ok()) {
      const body = await list.json();
      for (const p of body.plugins ?? []) {
        if (p.name === 'echo') await request.delete(`/api/admin/app-plugins/${p.id}`);
      }
    }
  });

  test.afterAll(async ({ request }) => {
    await apiLogin(request);
    const list = await request.get('/api/admin/app-plugins');
    if (list.ok()) {
      const body = await list.json();
      for (const p of body.plugins ?? []) {
        if (p.name === 'echo') await request.delete(`/api/admin/app-plugins/${p.id}`);
      }
    }
    await dropStorageByName(request, STORAGE);
  });

  test('the wizard reviews permissions before installing', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto('/admin/plugins');
    await page.getByTestId('plugins-tab-apps').click();
    await expect(page.getByTestId('app-plugins')).toBeVisible();
    await page.getByTestId('app-plugin-add').click();
    await expect(page.getByTestId('app-plugin-wizard')).toBeVisible();
    await page.getByTestId('app-plugin-source-file').click();
    await page.getByTestId('app-plugin-wasm').setInputFiles(WASM);
    await page.getByTestId('app-plugin-manifest').setInputFiles(MANIFEST);
    await page.getByTestId('app-plugin-review').click();

    // The review lists every permission the manifest asks for.
    const manifest = JSON.parse(readFileSync(MANIFEST, 'utf8')) as { permissions: string[] };
    const rows = page.getByTestId('app-plugin-permissions');
    await expect(rows).toBeVisible();
    for (const perm of manifest.permissions) {
      await expect(rows, `permission ${perm} is reviewed`).toContainText(perm);
    }
    // Nothing installs until the administrator says so.
    const install = page.getByTestId('app-plugin-install');
    await expect(install).toBeDisabled();
    await page.getByLabel(/I understand|Anladım/).check();
    await expect(install).toBeEnabled();
    // ⚠ The server COMPILES the module before it answers; on a busy machine
    // that outran a 15 s wait here (measured in a full run, green on the
    // rerun). The same allowance every other install in the suite gets.
    test.info().setTimeout(test.info().timeout + APP_INSTALL_ALLOWANCE_MS);
    await install.click();
    // The tab closes the wizard on success and the list shows the app running.
    await expect(page.getByTestId('app-plugin-echo')).toBeVisible({ timeout: APP_INSTALL_ALLOWANCE_MS });
    await expect(page.getByTestId('app-plugins')).toContainText(/running|çalışıyor/i);
  });

  test('the menu offers the action where it applies and a job writes the sibling', async ({ page, request }) => {
    await openExplorer(page);
    const txtVerbs = await menuVerbs(page, `${STORAGE}://${TXT}`);
    expect(txtVerbs.some((v) => /upper-case|büyük harf/i.test(v)), `menu on .txt: [${txtVerbs.join(', ')}]`).toBe(true);
    const pngVerbs = await menuVerbs(page, `${STORAGE}://${PNG}`);
    expect(pngVerbs.some((v) => /upper-case|büyük harf/i.test(v)), `menu on .png: [${pngVerbs.join(', ')}]`).toBe(false);

    // Run it from the menu; the tray shows the job; the output lands.
    const row = page.locator(`[data-fe-path="${STORAGE}://${TXT}"]`).first();
    await row.click({ button: 'right' });
    await page.getByRole('menuitem', { name: /upper-case|büyük harf/i }).click();

    await apiLogin(request);
    // The queue row is the proof the browser's click became a job; poll it
    // through the API rather than through the tray's animation.
    const deadline = Date.now() + 15_000;
    // ⚠ A named type, not `typeof op[]`: with `op` starting out `undefined`
    // TypeScript narrows that cast to `never[]` and the whole line stops
    // being checked (`Property 'kind' does not exist on type 'never'`).
    type OpRow = { id: number; kind: string; status: string; outputs?: { path: string }[] };
    let op: OpRow | undefined;
    while (Date.now() < deadline && !op) {
      const res = await request.get('/api/files/ops');
      const body = await res.json();
      op = (body.ops as OpRow[]).find((o) => o?.kind === 'plugin-action');
      if (!op) await new Promise((r) => setTimeout(r, 250));
    }
    expect(op, 'a plugin-action op was queued').toBeTruthy();
    const done = await waitForOp(request, op!.id, 30_000);
    expect(done.status).toBe('ok');

    const list = await request.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://`)}`);
    const listing = await list.json();
    const names = (listing.files as { basename: string }[]).map((f) => f.basename);
    expect(names).toContain('notes-upper.txt');
    const raw = await request.get(`/api/files/manager?action=download&path=${encodeURIComponent(`${STORAGE}://notes-upper.txt`)}`);
    expect(await raw.text()).toBe('THE QUICK BROWN FOX');

    // The explorer shows the new sibling without a reload.
    await expect(page.locator(`[data-fe-path="${STORAGE}://notes-upper.txt"]`).first()).toBeVisible({ timeout: 10_000 });
  });

  test('one app, two rows: the menu follows what the app knows about the file', async ({ page, request }) => {
    // ⚠⚠ This is the point of `applies.state`: a signing app has to offer
    // "Sign / Fill" where a signature is pending and "Request signatures"
    // where none is, as ORDINARY rows beside the built-in verbs — not as two
    // apps, not as a screen you have to open to find out. `echo` stands in
    // with `again` (needs the `runs` key) and `fresh` (must not have it).
    await openExplorer(page);
    const before = await menuVerbs(page, `${STORAGE}://${STATEFUL}`);
    expect(before.some((v) => /^Fresh/.test(v)), `fresh row on a file with no state: [${before.join(', ')}]`).toBe(true);
    expect(before.some((v) => /^Again/.test(v))).toBe(false);
    // ⚠ A `hidden` action is the second half of a flow, never a row: the
    // server does not even list it, and `run` answers 404 for it.
    expect(before.some((v) => /Hidden apply/i.test(v)), 'a hidden action is in no menu').toBe(false);

    // `upper` sets the `runs` key on the file it ran on.
    await apiLogin(request);
    const run = await request.post(`/api/files/plugins/actions/echo/upper/run`, {
      data: { paths: [`${STORAGE}://${STATEFUL}`] },
    });
    expect(run.status(), await run.text()).toBe(202);
    const done = await waitForOp(request, (await run.json()).op.id, 30_000);
    expect(done.status).toBe('ok');

    // The listing carries the keys (`app_state: ["echo:runs"]`), so the menu
    // swaps without anything else changing.
    await page.reload();
    await expect(page.getByTestId(`sidenav-storage-${STORAGE}`)).toBeVisible();
    const after = await menuVerbs(page, `${STORAGE}://${STATEFUL}`);
    expect(after.some((v) => /^Again/.test(v)), `again row after a run: [${after.join(', ')}]`).toBe(true);
    expect(after.some((v) => /^Fresh/.test(v))).toBe(false);
  });

  test('an action whose view is placed `page` opens a tab, not a dialog', async ({ page, context }) => {
    await openExplorer(page);
    const row = page.locator(`[data-fe-path="${STORAGE}://${TXT}"]`).first();
    await row.click({ button: 'right' });
    // ⚠ The tab is opened by `window.open`, so it is a NEW PAGE on the
    // context — waited for before the click resolves, or the race is lost.
    const opened = context.waitForEvent('page');
    // ⚠ `exact`: "Lock probe" is a substring of "Unlock probe", and the app
    // ships both — a loose name matches two rows and the click never lands.
    await page.getByRole('menuitem', { name: 'Lock probe', exact: true }).click();
    const tab = await opened;
    await tab.waitForLoadState('domcontentloaded');

    // ⚠ The address lives under the SPA's MOUNT BASE: a bare `/apps/…` is a
    // server 404, because only /admin/, /drive/ and /p/ fall back to
    // index.html (routes.go → wireStatic).
    expect(tab.url()).toContain('/admin/apps/echo/wizard');
    expect(decodeURIComponent(tab.url())).toContain(`path=${STORAGE}://${TXT}`);
    await expect(tab.getByTestId('plugin-page-title')).toHaveText('Wizard');
    // The same surface a modal would draw — the plugin's `open` event.
    await expect(tab.getByTestId('plugin-page')).toContainText('page open');
    // A whole page, not a dialog in a tab.
    expect(await tab.locator('.fe-modal').count()).toBe(0);
    await tab.close();

    // ⚠ And nothing was QUEUED: a `page` action does not go through `…/run`;
    // its job is born from the page's own submit.
    await expect(page.getByTestId('plugin-view')).toHaveCount(0);
  });

  test('a locked file says so, and the server refuses to rename it', async ({ page, request }) => {
    await apiLogin(request);
    // `params: {}` (not absent) runs the action instead of opening its view.
    const run = await request.post('/api/files/plugins/actions/echo/lock/run', {
      data: { paths: [`${STORAGE}://${LOCKED}`], params: {} },
    });
    expect(run.status(), await run.text()).toBe(202);
    expect((await waitForOp(request, (await run.json()).op.id, 30_000)).status).toBe('ok');

    // ⚠⚠ The refusal is the half that must not read as a permission problem:
    // the caller IS allowed, an app is holding the file until a date.
    const renamed = await request.post('/api/files/manager?action=rename', {
      data: { path: `${STORAGE}://`, item: `${STORAGE}://${LOCKED}`, name: 'thawed.txt' },
    });
    expect(renamed.status()).toBe(423);
    const refusal = await renamed.json();
    expect(refusal.error).toBe('locked');
    expect(refusal.plugin).toBe('echo');

    // The row says who is holding it, without a round trip per file.
    await openExplorer(page);
    const row = page.locator(`[data-fe-path="${STORAGE}://${LOCKED}"]`).first();
    await expect(row).toBeVisible();
    const badge = row.locator('[data-testid="lock-badge"]');
    await expect(badge).toBeVisible();
    // ⚠⚠ The badge a person READS names the app the way every other screen
    // does — its label, "Echo Fixture" — while the wire keeps the manifest id
    // that addresses it, asserted above (`refusal.plugin === 'echo'`). `lockView`
    // sends `plugin_label` beside `plugin` for exactly that (v0.43.0: the
    // details banner said "sign locked this file" where the rest of the panel
    // says "e-Signature").
    //
    // ⚠ The fixture's label is deliberately MORE than its id capitalised, so
    // this can only pass when the label is the thing being drawn: a client
    // that fell back to the id would title the badge "echo locked this file",
    // which matches neither line below. Matching /echo/i instead would pass
    // for either, and would have hidden the swap it is here to prove.
    const lockTitle = await badge.getAttribute('title');
    expect(lockTitle).toContain('Echo Fixture');
    expect(lockTitle).not.toMatch(/\becho\b/);
    // A file nobody locked wears no badge — the badge is a fact, not decoration.
    await expect(
      page.locator(`[data-fe-path="${STORAGE}://${TXT}"]`).first().locator('[data-testid="lock-badge"]'),
    ).toHaveCount(0);

    // ⚠⚠ The notice the app sent with the lock is ADDRESSED: `notify_send`
    // carried `target: {ref, action: "sign"}`, so clicking the bell row must
    // land on the file AND open what the app asked for on it. A "please sign"
    // that drops somebody in a folder has made them find the screen
    // themselves, which for a signer is where the flow stops.
    await page.getByTestId('notification-bell').click();
    const notice = page.getByTestId('notification-row').first();
    await expect(notice).toBeVisible();
    await expect(notice).toHaveAttribute('data-clickable', 'yes');
    await notice.click();
    await expect(page).toHaveURL(/app=echo/);
    await expect(page).toHaveURL(/appAction=sign/);
    // The deep link ran the action, not just the navigation.
    const queued = await pollForOp(request, (o) => o.action === 'sign');
    expect(queued, 'the bell click queued the app’s own action').toBeTruthy();

    // An administrator can lift it by force — the way out when an app dies
    // mid-flow and the document would otherwise never move again.
    const locks = await request.get('/api/admin/app-plugins/locks');
    expect(locks.status()).toBe(200);
    const held = ((await locks.json()).locks as Array<{ plugin: string; path: string; storage_id: number }>).find(
      (l) => l.plugin === 'echo' && l.path.endsWith(LOCKED),
    );
    expect(held, 'the admin list names the lock the app took').toBeTruthy();
    const lifted = await request.delete('/api/admin/app-plugins/locks', {
      data: { storage_id: held!.storage_id, path: held!.path },
    });
    expect(lifted.ok(), await lifted.text()).toBe(true);
    await page.reload();
    await expect(page.getByTestId(`sidenav-storage-${STORAGE}`)).toBeVisible();
    await expect(
      page.locator(`[data-fe-path="${STORAGE}://${LOCKED}"]`).first().locator('[data-testid="lock-badge"]'),
    ).toHaveCount(0);
  });

  test('a public page walks PIN → surface over the anonymous API and in a browser', async ({ request, playwright, baseURL, browser }) => {
    await apiLogin(request);
    const run = await request.post('/api/files/plugins/actions/echo/invite/run', {
      data: { paths: [`${STORAGE}://${TXT}`], params: { pin: 'auto' } },
    });
    expect(run.status(), await run.text()).toBe(202);
    const { op } = await run.json();
    const done = await waitForOp(request, op.id, 30_000);
    expect(done.status).toBe('ok');
    /**
     * The link and the PIN come back as the action's MESSAGE, and the ops row
     * carries it — but only by DECORATION: `pending_ops` has no message column,
     * so `GET /api/files/ops/{id}` reads it off the `app_plugin_jobs` row
     * (`wasmplugin.Registry.DecorateOps`). Polled rather than read once,
     * because the two rows are written by two different writers.
     *
     * ⚠ Measured 2026-09-20, once in three full runs: the op said `ok` and the
     * message was EMPTY for the rest of the run. That is not lag — see the
     * finding filed against `handlers/app_plugins.go`, where the request
     * writes the job row again (for `op_id`) out of a stale struct AFTER the
     * queue was poked, so a job that finishes first has its status, message
     * and outputs overwritten with the pending ones. The poll below cannot
     * heal that; it is here so a benign lag is not read as the defect, and so
     * the failure text says which of the two it was.
     */
    let message = String(done.message ?? '');
    for (const deadline = Date.now() + 5_000; !message && Date.now() < deadline; ) {
      await new Promise((r) => setTimeout(r, 250));
      message = String(((await (await request.get(`/api/files/ops/${op.id}`)).json()) as { message?: string }).message ?? '');
    }
    expect(
      message,
      'the finished action reports its link; an empty message on an `ok` op means the job row was overwritten after it finished',
    ).toBeTruthy();
    const [url, pin] = message.split('|');
    const token = url.slice(url.lastIndexOf('/') + 1);
    /* ⚠⚠ An app plugin's public page IS A SHARE now (v3 §1.1): the app asks
       for one through `public_page_create`, the host mints an ordinary share
       row, and the visitor's address is `/s/<token>` — the same 32-hex share
       token every download link carries, not the old 64-char page token. The
       link the action reports has to be that one, or the page an outside
       signer receives is a page nothing serves. */
    expect(url, 'the app link is a share link').toContain('/s/');
    expect(token).toHaveLength(32);
    expect(pin).toHaveLength(6);

    // A visitor with no session at all, over the ONE public surface
    // (`/api/public/s/<token>`, docs/BACKEND.md → "The public surface").
    const anon = await playwright.request.newContext({ baseURL: baseURL! });
    const info = await anon.get(`/api/public/s/${token}`);
    expect(info.status()).toBe(200);
    expect(info.headers()['cache-control']).toContain('no-store');
    const facts = await info.json();
    expect(facts.kind, 'a link carrying an app page says so').toBe('app');
    expect(facts.needs_pin).toBe(true);
    expect(facts.unlocked).toBe(false);
    expect(facts.expired).toBe(false);
    expect(facts.revoked).toBe(false);
    /* ⚠ The node is DELIBERATELY absent on an app link: the file behind it is
       the anchor the app's state hangs on, and the only bytes a visitor may
       have are the copies the app exposed. */
    expect(facts.node, 'an app link never names the document behind it').toBeUndefined();
    expect(facts.app?.plugin).toBe('echo');
    expect(facts.app?.page).toBe('signer');

    /* The surface is an EVENT, not a GET: the opening screen and every later
       one are the same call to the app, and an empty body means
       `{"event":"open"}`. It is behind the PIN like everything else. */
    expect((await anon.post(`/api/public/s/${token}/event`, { data: {} })).status()).toBe(401);
    expect((await anon.post(`/api/public/s/${token}/pin`, { data: { pin: '000000' } })).status()).toBe(401);
    expect((await anon.post(`/api/public/s/${token}/pin`, { data: { pin } })).status()).toBe(200);
    const view = await anon.post(`/api/public/s/${token}/event`, { data: { event: 'open' } });
    expect(view.status()).toBe(200);
    const surface = (await view.json()).surface;
    expect(JSON.stringify(surface)).toContain('doc=the quick brown fox');
    expect((await anon.get(`/api/public/s/${token}/file/pub:0`)).status()).toBe(200);
    expect((await anon.get(`/api/public/s/${'0'.repeat(32)}`)).status()).toBe(404);

    /* ⚠ The retired addresses still answer, and answer with a redirect rather
       than a 404: a link already sitting in somebody's inbox has to land
       somewhere that can explain itself. Followed automatically by the client,
       so the assertion is on where it ENDED UP. */
    const moved = await anon.get(`/api/p/${token}`, { maxRedirects: 0 });
    expect(moved.status(), 'the retired JSON prefix redirects to the share').toBe(301);
    expect(moved.headers()['location']).toBe(`/api/public/s/${token}`);
    await anon.dispose();

    // The same page in a browser with no session: PIN form → surface → Sign
    // → "received". The SPA under /s/ must not bounce to the login page.
    const visitor = await browser.newContext();
    const vp = await visitor.newPage();
    await vp.goto(`/s/${token}`);
    await expect(vp.getByTestId('public-page')).toBeVisible();
    /* ⚠⚠ A LOCKED GATE NAMES NOTHING, and that is deliberate — do not
       "restore" the heading here. The heading is drawn only once the link has
       OPENED, because a title above the PIN box tells somebody who cannot get
       in what is behind it: the file's name is the very thing the PIN
       withholds (v0.43.0 public shell; the unit half is publicShell's "a
       locked gate does not name what is behind it").

       So the gate is measured by ABSENCE — no heading, and the document's
       name nowhere on the page, not in the body and not in the tab's title.
       A spec that merely stopped asking for the heading would tolerate the
       guarantee; this one fails the day a name leaks back onto a gate. */
    await expect(vp.getByTestId('public-page-title')).toHaveCount(0);
    await expect(vp.locator('body')).not.toContainText(TXT);
    expect(await vp.title(), 'the tab names the file a stranger cannot open').not.toContain(TXT);
    await expect(vp.getByTestId('public-page-pin')).toBeVisible();
    expect(vp.url()).toContain(`/s/${token}`);
    await vp.getByTestId('public-page-pin-input').fill('000000');
    await vp.getByTestId('public-page-pin-submit').click();
    await expect(vp.getByTestId('public-page-pin-error')).toBeVisible();
    await vp.getByTestId('public-page-pin-input').fill(pin);
    await vp.getByTestId('public-page-pin-submit').click();
    /* Opened, it says what it is — the other half of the same guarantee.
       ⚠ Two different titles, and the difference is the point: the manifest's
       PAGE label ("Sign the document") is what the gate withholds, and what
       the opened page wears is the SURFACE's own title, which this fixture
       sets to "Sign" (testdata/echo/main.go). Asserting the page label here
       would look right and measure nothing — it is never drawn again. */
    await expect(vp.getByTestId('public-page-title')).toHaveText('Sign');
    await expect(vp.getByTestId('public-page')).toContainText('doc=the quick brown fox');

    /* ⚠⚠ A language chosen mid-visit ASKS AGAIN; it does not start again.
       Re-loading the link re-opens the surface at its first screen, so
       somebody three steps into signing — title typed, signature drawn —
       lost all of it by pressing a button that sits right beside the
       wizard. The re-ask carries the state and the values, which is why the
       fixture echoes the event and the note back. */
    await vp.getByRole('textbox', { name: /Note|Not/ }).fill('kept');
    await expect(vp.getByTestId('public-page')).toContainText('note=kept');
    await vp.getByTestId('public-language-tr').click();
    await expect(vp.getByTestId('public-page')).toContainText('olay=change');
    await expect(vp.getByTestId('public-page')).toContainText('not=kept');
    await expect(vp.getByRole('textbox', { name: /Not/ })).toHaveValue('kept');
    await vp.getByTestId('public-language-en').click();
    await expect(vp.getByTestId('public-page')).toContainText('event=change');

    await vp.getByTestId('public-page-action-submit').click();
    await expect(vp.getByTestId('public-page-accepted')).toBeVisible();
    await visitor.close();
  });
});
