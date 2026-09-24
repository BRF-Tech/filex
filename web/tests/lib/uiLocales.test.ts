// The language list is not a constant any more.
//
// A LANGUAGE PACK — an app whose manifest carries `ui_locales` — adds a
// language to filex ITSELF. The server LISTS it on /api/public/branding and
// serves its strings from /api/public/ui-locales/{code}; the interface offers
// it (public shell included), marks it with the app it came from, and drops
// it when the app is removed.
//
// ⚠⚠ The first block reads the SERVER's own bytes
// (backend/internal/api/handlers/testdata/wire/public-*.json, written by
// app_plugins_wire_test.go). For a whole release the branding field was a MAP
// on the wire and a LIST here, every test in this file fed it a list, and no
// pack's string ever reached a screen — the Spanish pack showed that in a
// browser on 2026-09-21. A fixture typed the browser's way can only agree
// with the browser.
import fs from 'node:fs';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  BUILTIN_LOCALES,
  acceptableLocale,
  availableLocales,
  ensureLocaleStrings,
  hasLocale,
  loadLocales,
  localeLabel,
  localeStrings,
  localeTable,
  localesKnown,
  localesVersion,
  normalizeLocaleCode,
  registerLocale,
  resetLocales,
  setLocales,
  setLocalesFromBranding,
  unregisterPluginLocales,
} from '@brftech/filex-core';
import type { PublicBranding } from '@brftech/filex-core';

const WIRE = path.resolve(__dirname, '../../../backend/internal/api/handlers/testdata/wire');
const branding = JSON.parse(fs.readFileSync(path.join(WIRE, 'public-branding.json'), 'utf8')) as PublicBranding;
const esStrings = JSON.parse(fs.readFileSync(path.join(WIRE, 'public-ui-locale.json'), 'utf8')) as {
  code: string;
  strings: Record<string, string>;
};

/** A fetch that answers branding and one language, and records what it was asked. */
function serverFetch(opts: { es?: number } = {}) {
  const asked: string[] = [];
  const f = vi.fn(async (url: string) => {
    asked.push(url);
    if (url.endsWith('/api/public/branding')) return { ok: true, status: 200, json: async () => branding };
    if (url.endsWith('/api/public/ui-locales/es')) {
      const status = opts.es ?? 200;
      return { ok: status === 200, status, json: async () => esStrings };
    }
    return { ok: false, status: 404, json: async () => ({}) };
  });
  return { f: f as unknown as typeof fetch, asked };
}

afterEach(() => {
  resetLocales();
  vi.unstubAllGlobals();
});

describe('the wire, as the server writes it', () => {
  it('lists added languages as rows, without strings', () => {
    expect(Array.isArray(branding.ui_locales)).toBe(true);
    expect(branding.ui_locales!.map((r) => r.code)).toEqual(['ar', 'es']);
    expect(branding.ui_locales!.every((r) => r.strings === undefined)).toBe(true);
  });

  it('a branding answer puts the pack languages on offer, with their app and their rtl flag', () => {
    setLocalesFromBranding(branding);
    expect(availableLocales().map((o) => o.code)).toEqual(['en', 'tr', 'ar', 'es']);
    expect(availableLocales().find((o) => o.code === 'es')).toMatchObject({ source: 'plugin', plugin: 'lang-es', label: 'Español' });
    expect(availableLocales().find((o) => o.code === 'ar')).toMatchObject({ rtl: true, label: 'العربية' });
    expect(localesKnown.value).toBe(true);
  });

  it("a language's strings are fetched from /api/public/ui-locales/{code} the first time its table is asked for", async () => {
    const { f, asked } = serverFetch();
    await loadLocales({ fetchImpl: f });
    expect(asked).toEqual(['/api/public/branding']);
    // English meanwhile — never a raw key…
    expect(localeTable('es')['ctx.download']).toBe(localeTable('en')['ctx.download']);
    const before = localesVersion.value;
    expect(await ensureLocaleStrings('es')).toBe(true);
    expect(asked).toEqual(['/api/public/branding', '/api/public/ui-locales/es']);
    // …and the pack's words once they land, which bumps the version every
    // reactive reader watches.
    expect(localesVersion.value).toBeGreaterThan(before);
    expect(localeTable('es')['ctx.download']).toBe('Descargar');
    expect(localeTable('es')['ctx.delete']).toBe(localeTable('en')['ctx.delete']);
    // Asked again: no second request.
    await ensureLocaleStrings('es');
    expect(asked.filter((u) => u.includes('ui-locales'))).toHaveLength(1);
  });

  it('a pack gone between the list and the fetch leaves English, and no request storm', async () => {
    const { f, asked } = serverFetch({ es: 404 });
    await loadLocales({ fetchImpl: f });
    expect(await ensureLocaleStrings('es')).toBe(false);
    localeTable('es');
    localeTable('es');
    expect(asked.filter((u) => u.includes('ui-locales'))).toHaveLength(1);
    expect(localeTable('es')['ctx.download']).toBe(localeTable('en')['ctx.download']);
  });

  it('a failed branding fetch leaves the two built-ins and makes the list KNOWN', async () => {
    const f = (async () => ({ ok: false, status: 500, json: async () => ({}) })) as unknown as typeof fetch;
    await loadLocales({ fetchImpl: f });
    expect(availableLocales().map((o) => o.code)).toEqual([...BUILTIN_LOCALES]);
    // ⚠ Known, so a held choice falls back instead of waiting forever.
    expect(localesKnown.value).toBe(true);
    expect(acceptableLocale('es')).toBe(false);
  });
});

describe('a stored choice before the list arrives', () => {
  it('is HELD while the list is in flight, judged once it lands', () => {
    // The panel reads `filex.locale=es` at boot; the list is a network fetch
    // away. Refusing `es` here is what opened every page in English.
    expect(localesKnown.value).toBe(false);
    expect(acceptableLocale('es')).toBe(true);
    expect(acceptableLocale('../etc')).toBe(false);
    setLocalesFromBranding(branding);
    expect(acceptableLocale('es')).toBe(true);
    setLocalesFromBranding({ locales: ['en', 'tr'] });
    expect(acceptableLocale('es')).toBe(false);
  });
});

describe('codes', () => {
  it('is the tag the interface uses, built-ins by their primary subtag', () => {
    expect(normalizeLocaleCode('tr-TR')).toBe('tr');
    expect(normalizeLocaleCode('EN_us')).toBe('en');
    expect(normalizeLocaleCode('ar')).toBe('ar');
    expect(normalizeLocaleCode('')).toBe('');
    expect(normalizeLocaleCode('../etc')).toBe('');
  });

  it('keeps a regional pack whole, and maps a browser tag onto the pack that answers it', () => {
    setLocales([
      { code: 'pt-br', source: 'plugin', plugin: 'lang-pt' },
      { code: 'es', source: 'plugin', plugin: 'lang-es' },
    ]);
    expect(normalizeLocaleCode('pt-BR')).toBe('pt-br');
    expect(normalizeLocaleCode('pt')).toBe('pt-br');
    expect(normalizeLocaleCode('es-MX')).toBe('es');
    expect(hasLocale('es-AR')).toBe(true);
    // Unknown and not yet offered: kept whole, so a HELD choice means what
    // the person picked.
    expect(normalizeLocaleCode('zh-Hant')).toBe('zh-hant');
  });

  it('names a language in its own words, even one the table has never heard of', () => {
    expect(localeLabel('es')).toBe('Español');
    expect(localeLabel('sv')).toMatch(/^Svenska$/i);
    expect(localeLabel('qqq')).toBe('QQQ');
  });
});

describe('what the instance offers', () => {
  it('is the two we ship, until something adds one', () => {
    expect(availableLocales().map((o) => o.code)).toEqual([...BUILTIN_LOCALES]);
    expect(hasLocale('ar')).toBe(false);
    registerLocale({ code: 'ar', label: 'العربية', source: 'plugin', plugin: 'sign', strings: { 'ctx.download': 'تحميل' } });
    expect(hasLocale('ar')).toBe(true);
    expect(availableLocales().find((o) => o.code === 'ar')).toMatchObject({ label: 'العربية', source: 'plugin', plugin: 'sign' });
    expect(availableLocales().slice(0, 2).map((o) => o.code)).toEqual([...BUILTIN_LOCALES]);
  });

  it('falls back to English for everything the pack did not translate', () => {
    registerLocale({ code: 'ar', source: 'plugin', plugin: 'sign', strings: { 'ctx.download': 'تحميل', 'ctx.rename': ' ' } });
    const table = localeTable('ar');
    expect(table['ctx.download']).toBe('تحميل');
    // An EMPTY (or blank) value is untranslated, not "translate as nothing".
    expect(table['ctx.rename']).toBe(localeTable('en')['ctx.rename']);
    expect(localeStrings('ar')).toEqual({ 'ctx.download': 'تحميل' });
    expect(localeStrings('en')).toEqual({});
  });

  it("drops keys that would walk into an object's machinery", () => {
    registerLocale({
      code: 'xx',
      source: 'plugin',
      plugin: 'evil',
      strings: { '__proto__.polluted': 'yes', 'a.constructor.prototype.x': 'yes', 'ctx.download': 'ok' },
    });
    expect(localeStrings('xx')).toEqual({ 'ctx.download': 'ok' });
    expect(({} as Record<string, unknown>).polluted).toBeUndefined();
  });

  it('a pack OVERLAYS a built-in language, it does not take it over', () => {
    registerLocale({ code: 'tr', source: 'plugin', plugin: 'sign', strings: { 'ctx.download': 'İNDİR' } });
    const table = localeTable('tr');
    expect(table['ctx.download']).toBe('İNDİR');
    expect(table['ctx.rename']).toBe('Yeniden adlandır');
    expect(availableLocales().map((o) => o.code)).toEqual([...BUILTIN_LOCALES]);
  });

  it('leaves with the app that brought it', () => {
    registerLocale({ code: 'ar', source: 'plugin', plugin: 'sign', strings: { a: 'b' } });
    registerLocale({ code: 'fa', source: 'plugin', plugin: 'other', strings: { a: 'b' } });
    unregisterPluginLocales('sign');
    expect(hasLocale('ar')).toBe(false);
    expect(hasLocale('fa')).toBe(true);
  });

  it('bumps a version every time, so a picker already on screen re-reads it', () => {
    const before = localesVersion.value;
    registerLocale({ code: 'ar', strings: {} });
    expect(localesVersion.value).toBeGreaterThan(before);
  });

  it('setLocales replaces the whole extra set', () => {
    registerLocale({ code: 'ar', strings: { a: 'b' } });
    setLocales([{ code: 'fa', source: 'plugin', plugin: 'x', strings: { a: 'b' } }]);
    expect(hasLocale('ar')).toBe(false);
    expect(hasLocale('fa')).toBe(true);
  });
});
