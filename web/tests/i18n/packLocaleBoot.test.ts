// The admin panel and a language pack's language, from a cold start.
//
// ⚠⚠ The failure this pins: with `filex.locale=es` stored AND the account set
// to `es`, the panel opened in English on every load. Each source of a
// language choice was judged against the offered list the moment it arrived
// and dropped when it failed — and every one of them arrives before the list
// does. web/src/i18n now HOLDS a plausible choice until the list lands, then
// decides (measured with the Spanish pack in a browser, 2026-09-21).
import fs from 'node:fs';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const WIRE = path.resolve(__dirname, '../../../backend/internal/api/handlers/testdata/wire');
const branding = JSON.parse(fs.readFileSync(path.join(WIRE, 'public-branding.json'), 'utf8'));
const es = JSON.parse(fs.readFileSync(path.join(WIRE, 'public-ui-locale.json'), 'utf8'));

function serve(answers: Record<string, unknown>) {
  const f = vi.fn(async (url: string) => {
    const hit = Object.entries(answers).find(([suffix]) => url.endsWith(suffix));
    if (!hit) return { ok: false, status: 404, json: async () => ({}) };
    return { ok: true, status: 200, json: async () => hit[1] };
  });
  vi.stubGlobal('fetch', f);
  return f;
}

async function freshI18n() {
  vi.resetModules();
  return import('@/i18n');
}

beforeEach(() => {
  localStorage.clear();
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe('a stored pack language survives a cold start', () => {
  it("is held before the list arrives, and becomes the pack's words when it does", async () => {
    localStorage.setItem('filex.locale', 'es');
    serve({ '/api/public/branding': branding, '/api/public/ui-locales/es': es });
    const mod = await freshI18n();
    // Held at once — not thrown away for a list that has not been fetched.
    expect(mod.i18n.global.locale.value).toBe('es');
    await mod.loadOfferedLocales();
    await vi.waitFor(() => expect(mod.i18n.global.t('appPlugins.title')).toBe('Aplicaciones'));
    // Untranslated keys read English, never a raw key.
    expect(mod.i18n.global.t('common.save')).toBe('Save');
    // ⚠ Only the admin panel's own keys are folded into vue-i18n: the
    // explorer's (`ctx.*`) live in core's table, and folding its prefix pairs
    // into nested objects would drop one of each.
    expect(Object.keys(mod.i18n.global.getLocaleMessage('es') as object)).not.toContain('ctx');
  });

  it('a pack key that names a BRANCH of the admin catalogue cannot wipe the strings under it', async () => {
    // A pack written for an older filex (where `appPlugins.wizard` was one
    // string) must not turn today's whole `appPlugins.wizard.*` subtree into
    // that string — only keys that are the admin catalogue's own LEAVES are
    // folded (mutation-tested: folding every key turns this red).
    localStorage.setItem('filex.locale', 'es');
    serve({
      '/api/public/branding': branding,
      '/api/public/ui-locales/es': {
        code: 'es',
        strings: { 'appPlugins.title': 'Aplicaciones', 'appPlugins.wizard.install': 'Instalar', 'appPlugins.wizard': 'viejo' },
      },
    });
    const mod = await freshI18n();
    await mod.loadOfferedLocales();
    await vi.waitFor(() => expect(mod.i18n.global.t('appPlugins.title')).toBe('Aplicaciones'));
    expect(mod.i18n.global.t('appPlugins.wizard.install')).toBe('Instalar');
  });

  it('the account preference arriving first is held the same way', async () => {
    serve({ '/api/public/branding': branding, '/api/public/ui-locales/es': es });
    const mod = await freshI18n();
    mod.applyPrefLocale('es');
    expect(mod.i18n.global.locale.value).toBe('es');
    await mod.loadOfferedLocales();
    await vi.waitFor(() => expect(mod.i18n.global.t('appPlugins.title')).toBe('Aplicaciones'));
  });

  it('falls back when the list says the pack is gone — and keeps the choice for when it returns', async () => {
    localStorage.setItem('filex.locale', 'es');
    serve({ '/api/public/branding': { locales: ['en', 'tr'] } });
    const mod = await freshI18n();
    await mod.loadOfferedLocales();
    expect(mod.i18n.global.locale.value).toBe('en');
    expect(localStorage.getItem('filex.locale')).toBe('es');
  });

  it("a pack that OVERLAYS Turkish changes its keys and leaves the rest Turkish (a deep fold, not a shallow spread)", async () => {
    localStorage.setItem('filex.locale', 'tr');
    serve({
      '/api/public/branding': { locales: ['en', 'tr'], ui_locales: [{ code: 'tr', source: 'plugin', plugin: 'tr-fix' }] },
      '/api/public/ui-locales/tr': { code: 'tr', strings: { 'appPlugins.title': 'UYGULAMALAR' } },
    });
    const mod = await freshI18n();
    await mod.loadOfferedLocales();
    await vi.waitFor(() => expect(mod.i18n.global.t('appPlugins.title')).toBe('UYGULAMALAR'));
    // The shallow spread this replaced swapped the whole `appPlugins` subtree
    // for the one string, and every sibling fell back to ENGLISH.
    const tr = JSON.parse(fs.readFileSync(path.resolve(__dirname, '../../src/locales/tr.json'), 'utf8'));
    expect(mod.i18n.global.t('appPlugins.subtitle')).toBe(tr.appPlugins.subtitle);
  });
});
