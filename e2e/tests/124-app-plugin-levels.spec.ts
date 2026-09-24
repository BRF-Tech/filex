/**
 * 116-app-plugin-levels — what an app offers depends on who is asking.
 *
 * v0.43.0 wave 2 (2026-09-22), measured in a browser:
 *
 *   1. A person granted `viewer` on an RBAC storage was offered "Convert…"
 *      on every file there, and the click answered 403 "insufficient
 *      permission": the file menu never read what an action needs. The menu
 *      now asks the level the server asks (lib/pluginMenu `pluginActionNeed`
 *      ↔ handlers/app_plugins.go `pluginACLNeed`), so a viewer is not
 *      offered it and an editor is.
 *   2. The converter told EVERY account which engines the server lacks
 *      ("Not installed on this server: ImageMagick, …") and listed the
 *      formats each would unlock. What the server lacks is the
 *      administrator's to fix (the owner's rule: greyed with the reason for
 *      an administrator, hidden for everybody else), so only an
 *      administrator is shown it (filex-convert view.canInstallEngines).
 *
 * The module is the real `filex-convert` build (e2e/helpers/app-locations.mjs);
 * without it the spec skips, unless FILEX_REQUIRE_WASM_FIXTURE=1.
 */
import { test, expect, type Page, type APIRequestContext } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';
import { guardFixture, installThroughWizard, resolveApp, PNG_1PX } from '../helpers/appPlugin';
import { openView, removeApp, walkNodes, type Node, type Surface } from '../helpers/surface';

const APP = resolveApp('convert');
const STORAGE = `e2e-levels-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const PNG = 'photo.png';
const QUALIFIED = `${STORAGE}://${PNG}`;
const VIEWER = { email: 'levels-viewer@example.test', password: 'levels-viewer-pw-2026', name: 'Level Viewer' };
const EDITOR = { email: 'levels-editor@example.test', password: 'levels-editor-pw-2026', name: 'Level Editor' };

async function userId(request: APIRequestContext, email: string): Promise<number> {
  const raw = (await (await request.get('/api/admin/users')).json()) as
    | { id: number; email: string }[]
    | { users?: { id: number; email: string }[]; items?: { id: number; email: string }[] };
  const list = Array.isArray(raw) ? raw : (raw.users ?? raw.items ?? []);
  const me = list.find((u) => u.email === email);
  expect(me, `${email} exists`).toBeTruthy();
  return me!.id;
}

async function signInAs(page: Page, who: { email: string; password: string }) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await page.goto('/admin/login');
  await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(who.email);
  await page.getByLabel(/password|şifre/i).fill(who.password);
  await page
    .getByRole('button', { name: 'Sign in', exact: true })
    .or(page.getByRole('button', { name: 'Giriş yap', exact: true }))
    .first()
    .click();
  await page.waitForURL(/\/drive\//);
}

/** The labels of the right-click menu on the PNG. */
async function menuOnThePng(page: Page): Promise<string[]> {
  await page.goto(`/drive/explore?storage=${encodeURIComponent(STORAGE)}`);
  const row = page.locator(`[data-fe-path="${QUALIFIED}"]`).first();
  await expect(row).toBeVisible();
  await row.click({ button: 'right' });
  const menu = page.getByRole('menu').first();
  await expect(menu).toBeVisible();
  const names = (await menu.locator('[role="menuitem"] .fe-ctx__label').allInnerTexts()).map((s) => s.trim());
  await page.keyboard.press('Escape');
  return names;
}

/** The grey list and the missing-engine note, as a screen carries them. */
function engineTalk(s: Surface): { lists: Node[]; notes: string[] } {
  const lists: Node[] = [];
  const notes: string[] = [];
  walkNodes(s.nodes, (_w, n) => {
    if (n.type === 'list') lists.push(n);
    if (n.type === 'text') {
      const text = n.props?.text as Record<string, string> | string | undefined;
      const en = typeof text === 'string' ? text : (text?.en ?? '');
      if (/not installed on this server|engine missing/i.test(en)) notes.push(en);
    }
  });
  return { lists, notes };
}

test.describe('App plugins: the level on the file, the role on the server', () => {
  test.describe.configure({ mode: 'serial' });
  guardFixture(APP, test.skip);

  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT, { rbac_enabled: true });
    await apiLogin(request);
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://`, 'file[]': { name: PNG, mimeType: 'image/png', buffer: PNG_1PX } },
    });
    expect(up.ok(), `upload: ${up.status()}`).toBe(true);
    for (const [who, level] of [
      [VIEWER, 'viewer'],
      [EDITOR, 'editor'],
    ] as const) {
      await request.post('/api/admin/users', {
        data: { email: who.email, password: who.password, display_name: who.name, role: 'user' },
      });
      const g = await request.post('/api/files/permissions', {
        data: { path: `${STORAGE}://`, user_id: await userId(request, who.email), level, is_dir: true },
      });
      expect(g.ok(), `grant ${level}: ${g.status()} ${await g.text()}`).toBe(true);
    }
    await removeApp(request, 'convert');
  });

  test.afterAll(async ({ request }) => {
    await apiLogin(request);
    await removeApp(request, 'convert');
    await dropStorageByName(request, STORAGE);
  });

  test('install', async ({ page }) => {
    await loginAs(page);
    await installThroughWizard(page, APP);
  });

  test('a viewer is not offered Convert on a file it could only be refused; an editor is', async ({ browser }) => {
    const convert = /^(Convert…|Dönüştür…)$/;

    const asViewer = await browser.newPage();
    await signInAs(asViewer, VIEWER);
    const viewerMenu = await menuOnThePng(asViewer);
    // The menu is there (so the assertion below measures something), and the
    // app's writer is not in it.
    expect(viewerMenu.some((l) => /^(Download|İndir)$/.test(l)), JSON.stringify(viewerMenu)).toBe(true);
    expect(viewerMenu.some((l) => convert.test(l)), `a viewer's menu: ${JSON.stringify(viewerMenu)}`).toBe(false);
    // …and the server agrees: the action it is not offered is refused.
    const refused = await asViewer.request.post('/api/files/plugins/actions/convert/convert/run', {
      data: { paths: [QUALIFIED] },
    });
    expect(refused.status()).toBe(403);
    await asViewer.close();

    const asEditor = await browser.newPage();
    await signInAs(asEditor, EDITOR);
    const editorMenu = await menuOnThePng(asEditor);
    expect(editorMenu.some((l) => convert.test(l)), `an editor's menu: ${JSON.stringify(editorMenu)}`).toBe(true);
    await asEditor.close();
  });

  test('only an administrator is told which engines the server lacks', async ({ request, playwright, baseURL }) => {
    await apiLogin(request);
    const admin = engineTalk(await openView(request, 'convert', 'options', QUALIFIED));
    // A test server has no ffmpeg; a machine with every engine has nothing
    // to grey out, and then there is nothing to hide either.
    if (!admin.lists.length) {
      test.info().annotations.push({ type: 'note', description: 'every engine is installed here; nothing to hide' });
      return;
    }
    expect(admin.notes.length, 'the administrator reads which engines are missing').toBeGreaterThan(0);

    const editor = await playwright.request.newContext({ baseURL });
    try {
      await apiLogin(editor, EDITOR.email, EDITOR.password);
      const s = engineTalk(await openView(editor, 'convert', 'options', QUALIFIED));
      expect(s.notes, 'an ordinary account is not told which engines the server lacks').toEqual([]);
      expect(s.lists, 'an ordinary account gets no grey list of formats it cannot have').toEqual([]);
    } finally {
      await editor.dispose();
    }
  });
});
