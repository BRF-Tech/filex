/**
 * 227-onlyoffice-editor-language - the ONLYOFFICE editor opens in the
 * person's language (filex 0.54, GitHub Discussion #93, docs/ONLYOFFICE.md →
 * The editor's language). The server chooses it (backend onlyoffice/lang.go):
 *
 *   1. External services lists the editor language on the ONLYOFFICE row -
 *      "auto" unless FILEX_ONLYOFFICE_LANG pins another, and the languages the
 *      editor offers, named in themselves; a language the editor does not
 *      offer is refused (400 editor_lang_invalid) and changes nothing;
 *   2. with ONLYOFFICE configured, a document asked for by a Turkish screen
 *      (Accept-Language tr-TR, what the viewer sends) gets an editor config
 *      with lang "tr" and region "tr-TR"; the request's own `lang` comes
 *      before the screen's; a language the editor does not offer falls to the
 *      next one the request gives;
 *   3. a fixed language on External services ("de") is everybody's, whatever
 *      the screen says; back to automatic, the screen's again.
 *
 * ⚠ 2 and 3 need ONLYOFFICE configured on the run (FILEX_ONLYOFFICE_URL /
 * _JWT, as in 199); without it they are skipped. The setting is put back to
 * what it was when the spec started.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';

const CONFIG = '/api/files/onlyoffice/config';
const EXTERNAL = '/api/admin/external';

type Row = {
  Name: string;
  editor_lang?: string;
  editor_lang_env_managed?: boolean;
  editor_languages?: Array<{ code: string; name: string }>;
};

/** ONLYOFFICE is configured and answering, by the server's own probe. */
async function documentServer(api: APIRequestContext): Promise<boolean> {
  const res = await api.get('/api/files/capabilities');
  if (!res.ok()) return false;
  const caps = (await res.json()) as { external?: Record<string, { state?: string }> };
  return caps.external?.onlyoffice?.state === 'ok';
}

async function onlyOfficeRow(api: APIRequestContext): Promise<Row | undefined> {
  const res = await api.get(EXTERNAL);
  expect(res.ok(), await res.text()).toBe(true);
  const body = (await res.json()) as { entries: Row[] | null };
  return (body.entries ?? []).find((r) => r.Name === 'onlyoffice');
}

test.describe.serial('the ONLYOFFICE editor speaks the person’s language', () => {
  let api: APIRequestContext;
  let store = '';
  let mount = '';
  let path = '';
  let ds = false;
  let before = 'auto';

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    const tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    store = `e2e-oolang-${tag}-${Date.now()}`;
    mount = `/tmp/filex-${store}`;
    const name = `rapor-${tag}.docx`;
    path = `${store}://${name}`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const root = storageRoot(mount);
    mkdirSync(root, { recursive: true });
    writeFileSync(join(root, name), 'PK not really a docx');
    await seedLocalStorage(api, store, mount);
    ds = await documentServer(api);
    const row = await onlyOfficeRow(api);
    before = row?.editor_lang ?? 'auto';
  });

  test.afterAll(async () => {
    await api.patch(`${EXTERNAL}/onlyoffice`, { data: { editor_lang: before } }).catch(() => undefined);
    await dropStorageByName(api, store).catch(() => undefined);
    await api.dispose();
  });

  /** The editor config's lang and region for the document, asked as a screen in `accept` would. */
  async function editorSays(accept: string, extra: Record<string, unknown> = {}): Promise<{ lang?: string; region?: string }> {
    let said: { lang?: string; region?: string } = {};
    // The file is indexed by the storage's first scan; ask until it is there.
    await expect
      .poll(
        async () => {
          const res = await api.post(CONFIG, {
            data: { path, mode: 'view', ...extra },
            headers: { 'Accept-Language': accept },
          });
          if (!res.ok()) return res.status();
          const body = (await res.json()) as { config: { editorConfig: { lang?: string; region?: string } } };
          said = { lang: body.config.editorConfig.lang, region: body.config.editorConfig.region };
          return 200;
        },
        { timeout: 30_000, intervals: [500] },
      )
      .toBe(200);
    return said;
  }

  test('External services lists the editor language and refuses one the editor does not offer', async () => {
    const row = await onlyOfficeRow(api);
    expect(row, 'the ONLYOFFICE row').toBeDefined();
    const names = Object.fromEntries((row!.editor_languages ?? []).map((l) => [l.code, l.name]));
    expect(['auto', ...Object.keys(names)], 'the setting is automatic or one of the list').toContain(row!.editor_lang);    expect(names.tr).toBe('Türkçe');
    expect(names.de).toBe('Deutsch');
    expect(names['pt-PT']).toBeTruthy();
    expect(names['zh-TW']).toBeTruthy();

    const bad = await api.patch(`${EXTERNAL}/onlyoffice`, { data: { editor_lang: 'klingon' } });
    expect(bad.status()).toBe(400);
    expect(((await bad.json()) as { error?: string }).error).toBe('editor_lang_invalid');
    expect((await onlyOfficeRow(api))!.editor_lang, 'a refused value changes nothing').toBe(row!.editor_lang);
  });

  test('a Turkish screen gets a Turkish editor; the request’s own language first', async () => {
    test.skip(!ds, 'ONLYOFFICE is not configured on this run');
    const auto = await api.patch(`${EXTERNAL}/onlyoffice`, { data: { editor_lang: 'auto' } });
    expect(auto.ok(), await auto.text()).toBe(true);

    expect(await editorSays('tr-TR,tr;q=0.9,en;q=0.8')).toEqual({ lang: 'tr', region: 'tr-TR' });
    expect((await editorSays('tr-TR', { lang: 'es' })).lang).toBe('es');
    expect((await editorSays('fa-IR,fa;q=0.9,fr;q=0.5')).lang, 'Persian is not offered: the next one').toBe('fr');
  });

  test('a fixed language is everybody’s; automatic gives the screen its own back', async () => {
    test.skip(!ds, 'ONLYOFFICE is not configured on this run');
    const fixed = await api.patch(`${EXTERNAL}/onlyoffice`, { data: { editor_lang: 'de' } });
    expect(fixed.ok(), await fixed.text()).toBe(true);
    expect(await editorSays('tr-TR,tr;q=0.9', { lang: 'tr' })).toEqual({ lang: 'de', region: 'de-DE' });

    const auto = await api.patch(`${EXTERNAL}/onlyoffice`, { data: { editor_lang: 'auto' } });
    expect(auto.ok(), await auto.text()).toBe(true);
    expect((await editorSays('tr-TR')).lang).toBe('tr');
  });
});
