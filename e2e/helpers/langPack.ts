/**
 * The Arabic language pack an e2e run installs — one source for every spec
 * that needs a right-to-left language the server itself flags.
 *
 * ⚠ Extracted from 126-rtl-server-text when a second and a third spec needed
 * the same pack (v0.43.0). A copy per spec is how two of them end up
 * installing different packs and disagreeing about what "Arabic" contains.
 *
 * The LOCAL unpublished pack (`G:/filex-lang-ar`, or FILEX_E2E_LANG_PACK_AR)
 * when it is on this machine — the whole interface, which is what a real
 * reader gets — and otherwise a manifest built here from the repo's fixture,
 * with whatever extra strings the caller asks for. Either way `ar` becomes an
 * OFFERED language the server flags right to left, which is what turns a page
 * around.
 *
 * ⚠ Arabic is filex's right-to-left TEST fixture and nothing else: it is not
 * published and not advertised (Burak, 2026-09-19), and no screenshot shows
 * it.
 */
import { expect, type APIRequestContext } from '@playwright/test';
import { existsSync, readFileSync, writeFileSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));

export interface LangPack {
  /** The manifest to upload. */
  path: string;
  /** Its `name` — how to find it again, and how to remove it. */
  name: string;
  /** Is this the real local pack, or the repo's small fixture? */
  local: boolean;
  /** The Arabic strings it carries, by key (read back from the manifest). */
  strings: Record<string, string>;
}

/**
 * The pack to install. `extra` is merged into its Arabic strings — a spec that
 * measures one sentence can put exactly that sentence in.
 */
export function arabicPack(extra: Record<string, string> = {}): LangPack {
  const local = process.env.FILEX_E2E_LANG_PACK_AR ?? 'G:/filex-lang-ar/filex-app.json';
  if (existsSync(local)) {
    const manifest = JSON.parse(readFileSync(local, 'utf8'));
    const strings = { ...(manifest.ui_locales?.ar ?? {}) };
    if (Object.keys(extra).some((k) => strings[k] !== extra[k])) {
      // The local pack, with the spec's sentence written into a copy — the
      // pack on disk is somebody's working tree and is not ours to edit.
      manifest.ui_locales = { ...manifest.ui_locales, ar: { ...strings, ...extra } };
      const dir = mkdtempSync(join(tmpdir(), 'filex-lang-ar-'));
      const path = join(dir, 'filex-app.json');
      writeFileSync(path, JSON.stringify(manifest));
      return { path, name: manifest.name, local: true, strings: { ...strings, ...extra } };
    }
    return { path: local, name: manifest.name, local: true, strings };
  }
  const fixture = JSON.parse(readFileSync(resolve(HERE, '../fixtures/lang-pack/filex-app.json'), 'utf8'));
  fixture.name = 'lang-ar-e2e';
  fixture.label = { en: 'Arabic (e2e)', ar: '\u0627\u0644\u0639\u0631\u0628\u064a\u0629 (e2e)' };
  fixture.ui_locales = { ar: { ...(fixture.ui_locales?.ar ?? {}), ...extra } };
  const dir = mkdtempSync(join(tmpdir(), 'filex-lang-ar-'));
  const path = join(dir, 'filex-app.json');
  writeFileSync(path, JSON.stringify(fixture));
  return { path, name: fixture.name, local: false, strings: fixture.ui_locales.ar };
}

/** Install it, and check the SERVER is the one that calls Arabic right to left. */
export async function installLangPack(api: APIRequestContext, pack: LangPack): Promise<void> {
  const res = await api.post('/api/admin/app-plugins', {
    multipart: { manifest: { name: 'filex-app.json', mimeType: 'application/json', buffer: readFileSync(pack.path) } },
  });
  expect(res.ok(), `installing the Arabic pack: ${res.status()} ${await res.text()}`).toBeTruthy();
  const body = await res.json();
  expect(body.kind, 'it is a language pack').toBe('language_pack');
  expect(
    (body.languages ?? []).some((l: { code: string; rtl?: boolean }) => l.code === 'ar' && l.rtl === true),
    'the SERVER is the one that says Arabic is right to left',
  ).toBe(true);
}

/** Take it off again, by name, however many copies are installed. */
export async function removeLangPack(api: APIRequestContext, pack: LangPack): Promise<void> {
  const list = await api.get('/api/admin/app-plugins');
  if (!list.ok()) return;
  for (const p of (await list.json()).plugins ?? []) {
    if (p.name === pack.name) await api.delete(`/api/admin/app-plugins/${p.id}`);
  }
}
