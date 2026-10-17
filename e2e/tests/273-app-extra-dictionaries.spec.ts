/**
 * 273-app-extra-dictionaries - the dictionaries an administrator adds to an
 * app (filex 0.57, backend wasmplugin/extras.go, Admin → Plugins → an app's
 * page → Dictionaries). The office editor carries the spell-check and
 * hyphenation dictionaries of filex's interface languages and reads any
 * other language from `<package>/extra/dictionaries/<lang>/<file>.txt`. In
 * every engine `E2E_BROWSERS` names (`chromium,firefox,webkit`):
 *
 *   1. on the app's page an administrator adds a language (its .aff and
 *      .dic) and the section lists it;
 *   2. the app's interface reads the file from its own package address -
 *      with fetch, and with XHR in a worker started from a blob: address,
 *      which is how the office editor's spell checker asks for it - and a
 *      language nobody added is a 404;
 *   3. removing the language on the app's page takes it away from the
 *      interface too.
 *
 * The app is built here, like 175's and 228's: an interface-only app with
 * `ui.package_fetch` whose script speaks the bridge's hello itself.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';
import { installInterfaceApp, openAppInterface, removeAppByName } from '../helpers/appInterface';
import { zip } from '../helpers/zip';

/** A Hunspell dictionary small enough to read back whole. */
const AFF = 'SET UTF-8\nTRY aeoiuyzlnrstkcpwmdbghfj\n';
const DIC = '2\nkot\npies\n';

/**
 * The page: the bridge's hello (filex marks the frame connected), then
 * `__readExtra(path)`, which reads a path of its own package with fetch and
 * with XHR from a blob: worker, as the office editor's spell checker does.
 */
const APP_JS = `
window.__readExtra = async (rel) => {
  const url = new URL(rel, location.href).href;
  const viaFetch = await fetch(url).then(
    (r) => (r.ok ? r.text().then((t) => 'read ' + t) : 'status ' + r.status),
    (e) => 'blocked ' + e.name,
  );
  const src = 'onmessage = (e) => { const x = new XMLHttpRequest(); x.onload = () => postMessage(x.status === 200 ? "read " + x.responseText : "status " + x.status); x.onerror = () => postMessage("blocked"); x.open("GET", e.data); x.send(); };';
  const viaWorker = await new Promise((ok) => {
    let w;
    try {
      w = new Worker(URL.createObjectURL(new Blob([src], { type: 'text/javascript' })));
    } catch (e) {
      ok('no worker ' + e.name);
      return;
    }
    const timer = setTimeout(() => ok('blocked timeout'), 5000);
    w.onmessage = (ev) => { clearTimeout(timer); w.terminate(); ok(ev.data); };
    w.onerror = () => { clearTimeout(timer); ok('blocked error'); };
    w.postMessage(url);
  });
  return { fetch: viaFetch, worker: viaWorker };
};
(async () => {
  await new Promise((resolve, reject) => {
    addEventListener('message', (ev) => {
      if (ev.source === parent && ev.data && ev.data.type === 'filex:port' && ev.ports[0]) resolve(ev.ports[0]);
    });
    parent.postMessage({ type: 'filex:hello', v: 1 }, '*');
    setTimeout(() => reject(new Error('filex did not answer')), 10000);
  });
  document.body.dataset.ready = '1';
})().catch((e) => { document.body.dataset.error = String((e && (e.message || e.code)) || e); });
`;

const UI = zip({
  'index.html': '<!doctype html><html><head><meta charset="utf-8"><script src="app.js" defer></script></head><body><h1>words</h1></body></html>',
  'app.js': APP_JS,
});

function manifest(name: string, ext: string) {
  return {
    manifest_version: 1,
    name,
    version: '1.0.0',
    label: { en: name },
    permissions: ['files:read', 'files:write'],
    ui: { bundle: {}, package_fetch: true, extras: { dictionaries: {} } },
    views: [{ id: 'view', placement: 'viewer', ui: 'index.html', label: { en: name }, applies: { ext: [ext] } }],
  };
}

type Read = { fetch: string; worker: string };

test.describe.serial('Dictionaries an administrator adds to an app', () => {
  let api: APIRequestContext;
  let tag = '';
  let store = '';
  let app = '';
  let ext = '';

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    store = `e2e-dict-${tag}-${Date.now()}`;
    app = `dict-${tag}`;
    ext = `dct${tag.slice(0, 3)}`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await removeAppByName(api, app);
    const mount = `/tmp/filex-${store}`;
    const root = storageRoot(mount);
    mkdirSync(root, { recursive: true });
    writeFileSync(join(root, `note.${ext}`), 'a note');
    await seedLocalStorage(api, store, mount);
    const perms = (await installInterfaceApp(api, manifest(app, ext), UI)).map((p) => p.id);
    expect(perms, 'it reads its own package').toContain('ui:package-fetch');
  });

  test.afterAll(async () => {
    await removeAppByName(api, app);
    await dropStorageByName(api, store);
    await api.dispose();
  });

  test('the administrator adds a language on the app\'s page', async ({ page }) => {
    await loginAs(page);
    await page.goto(`/admin/plugins/apps/${app}`);
    const card = page.getByTestId('app-dictionaries');
    await expect(card).toBeVisible({ timeout: 15_000 });
    await card.getByTestId('app-dictionaries-add').click();
    await page.getByTestId('app-dictionaries-lang').locator('input').fill('pl_PL');
    await page.getByTestId('app-dictionaries-aff').setInputFiles({ name: 'pl_PL.aff', mimeType: 'text/plain', buffer: Buffer.from(AFF) });
    await page.getByTestId('app-dictionaries-dic').setInputFiles({ name: 'pl_PL.dic', mimeType: 'text/plain', buffer: Buffer.from(DIC) });
    await page.getByTestId('app-dictionaries-save').click();
    await expect(card.getByTestId('app-dictionary-pl_PL')).toBeVisible();

    const list = await api.get('/api/admin/app-plugins');
    const id = ((await list.json()) as { plugins: Array<{ id: number; name: string }> }).plugins.find((p) => p.name === app)?.id;
    const extras = (await (await api.get(`/api/admin/app-plugins/${id}/extras`)).json()) as { files: Array<{ path: string }> };
    expect(extras.files.map((f) => f.path).sort()).toEqual(['dictionaries/pl_PL/pl_PL.aff.txt', 'dictionaries/pl_PL/pl_PL.dic.txt']);
  });

  test('the interface reads it from its own package, from a worker too', async ({ page }) => {
    const frame = await openAppInterface(page, store, `note.${ext}`);
    const added = (await frame.evaluate((p) => (window as unknown as { __readExtra(p: string): Promise<Read> }).__readExtra(p), 'extra/dictionaries/pl_PL/pl_PL.dic.txt')) as Read;
    expect(added).toEqual({ fetch: `read ${DIC}`, worker: `read ${DIC}` });
    const missing = (await frame.evaluate((p) => (window as unknown as { __readExtra(p: string): Promise<Read> }).__readExtra(p), 'extra/dictionaries/de_AT/de_AT.dic.txt')) as Read;
    expect(missing.fetch, 'a language nobody added').toBe('status 404');
  });

  test('removing it on the app\'s page takes it away', async ({ page }) => {
    await loginAs(page);
    await page.goto(`/admin/plugins/apps/${app}`);
    const card = page.getByTestId('app-dictionaries');
    await expect(card.getByTestId('app-dictionary-pl_PL')).toBeVisible({ timeout: 15_000 });
    await page.getByTestId('app-dictionary-actions-pl_PL').click();
    await page.locator('.fe-ctx').last().getByRole('menuitem', { name: /^(Remove|Kaldır)$/ }).click();
    await page.getByTestId('app-dictionaries-remove-confirm').click();
    await expect(card.getByTestId('app-dictionary-pl_PL')).toHaveCount(0);
  });

  test('and the interface no longer reads it', async ({ page }) => {
    const frame = await openAppInterface(page, store, `note.${ext}`);
    const gone = (await frame.evaluate((p) => (window as unknown as { __readExtra(p: string): Promise<Read> }).__readExtra(p), 'extra/dictionaries/pl_PL/pl_PL.dic.txt')) as Read;
    expect(gone.fetch).toBe('status 404');
  });
});
