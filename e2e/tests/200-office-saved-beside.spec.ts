/**
 * 200-office-saved-beside - an office file in an older format is never
 * overwritten with another format (filex 0.51, docs/ONLYOFFICE.md → A save in
 * another format), against a real document server.
 *
 * ONLYOFFICE Docs 9.4 writes no .doc: an edited .doc comes back as DOCX
 * (`filetype: "docx"`, measured; a .xls as XLSX). filex used to write those
 * bytes under the .doc name. Now the .doc is left as it was and the edit is
 * saved beside it as .docx; the person who edited it is told in their
 * language, and an audit row says what happened.
 *
 * The .doc is a Rich Text document under a .doc name, made here: what a lot of
 * ".doc" files in the wild are, and what the document server saves as DOCX
 * the same way it does a Word 97 binary (measured both).
 *
 * ⚠ The document server is the harness's (FILEX_ONLYOFFICE_URL / _JWT /
 * _CALLBACK_URL, as in 199); without one the case is skipped.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';

const RTF = '{\\rtf1\\ansi{\\fonttbl\\f0 Arial;}\\f0\\fs24 Rapor 199\\par}';

interface Row {
  event?: string;
  action?: string;
  meta?: Record<string, unknown>;
  metadata?: Record<string, unknown>;
}

async function documentServer(api: APIRequestContext): Promise<boolean> {
  const res = await api.get('/api/files/capabilities');
  if (!res.ok()) return false;
  const caps = (await res.json()) as { external?: Record<string, { state?: string }> };
  return caps.external?.onlyoffice?.state === 'ok';
}

test.describe.serial('an office file in an older format', () => {
  let api: APIRequestContext;
  let store = '';
  let mount = '';
  let name = '';
  let docx = '';
  let ds = false;

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    const tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    store = `e2e-oobeside-${tag}-${Date.now()}`;
    mount = `/tmp/filex-${store}`;
    name = `rapor-${tag}.doc`;
    docx = `rapor-${tag}.docx`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const root = storageRoot(mount);
    mkdirSync(root, { recursive: true });
    writeFileSync(join(root, name), RTF);
    await seedLocalStorage(api, store, mount);
    ds = await documentServer(api);
  });

  test.afterAll(async () => {
    await dropStorageByName(api, store).catch(() => undefined);
    await api.dispose();
  });

  test('an edited .doc is saved beside it as .docx, and the .doc does not change', async ({ page }) => {
    test.skip(!ds, 'ONLYOFFICE is not configured on this run');
    test.setTimeout(180_000);
    const root = storageRoot(mount);
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto(`/admin/files/edit?path=${encodeURIComponent(`${store}://${name}`)}&type=doc&mode=edit`);
    await expect(page.locator('iframe[name^="frameEditor"]')).toBeVisible({ timeout: 30_000 });
    const frame = page.frameLocator('iframe[name^="frameEditor"]');
    await expect(frame.locator('#editor_sdk')).toBeVisible({ timeout: 60_000 });
    await page.waitForTimeout(1_500);
    await frame.locator('#editor_sdk').click({ position: { x: 300, y: 200 } });
    await page.keyboard.type('Düzenleme 199 ');
    await page.waitForTimeout(2_000);

    // Closing the last editor ends the session; the document server calls
    // filex back about ten seconds later.
    await page.close();
    await expect.poll(() => existsSync(join(root, docx)), { timeout: 90_000, intervals: [1_000] }).toBe(true);
    expect(readFileSync(join(root, name), 'utf8'), 'the .doc is as it was').toBe(RTF);
    const saved = readFileSync(join(root, docx));
    expect(saved.subarray(0, 4).toString('latin1'), 'the edit is a DOCX').toBe('PK\x03\x04');

    // The person who edited it is told, in every built-in language.
    await expect
      .poll(
        async () => {
          const items = (((await (await api.get('/api/notifications?limit=100')).json()).items ?? []) as Row[]).filter(
            (n) => n.event === 'file.uploaded' && String(n.meta?.saved_beside ?? '').endsWith(name),
          );
          return items[0]?.meta?.title_tr ?? '';
        },
        { timeout: 15_000 },
      )
      .toBe(`Düzenlemeniz ${docx} olarak kaydedildi`);

    // The audit log says what happened.
    const res = await api.get(`/api/admin/audit?action=${encodeURIComponent('file.office_saved_beside')}&limit=50`);
    expect(res.ok()).toBe(true);
    const body = (await res.json()) as Row[] | { items?: Row[]; entries?: Row[] };
    const rows = Array.isArray(body) ? body : (body.items ?? body.entries ?? []);
    // The row's shape is the audit page's (the entry, its target's name, the
    // person): the new file's name is in it.
    const row = rows.find((r) => JSON.stringify(r).includes(`/${docx}`));
    expect(row, 'file.office_saved_beside').toBeTruthy();
  });
});
