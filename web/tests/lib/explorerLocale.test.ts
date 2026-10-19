// An embed's language (task #275, the owner's rule 2026-10-19).
//
// An explorer embedded in another product (`<filex-explorer>`, or core's
// FileExplorer mounted by a host that wired no account preferences) speaks:
//   (a) the language the person picked IN THIS BROWSER ("⋯" → Language),
//   (b) the account's language - only when a person is behind the credential,
//   (c) the browser's languages, matched against what the server offers
//       (English, Turkish and the server's language packs),
//   (d) the host's `config.locale`,
//   (e) English.
// A personal embed reads the account's language and writes the person's own
// pick to it; it never writes the HOST's language there any more. A
// non-personal embed (an app token every visitor shares) asks the account
// nothing. The web app and the desktop app keep #191's order, unchanged.
//
// RED on the code before: lib/explorerLocale did not exist; the explorer put
// the host's `locale` above the browser in every embed, offered no language
// setting, and wrote the host's language to an account that had none.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  EMBED_LOCALE_LS_KEY,
  accountLanguage,
  embedLocaleChoice,
  explorerIsEmbedded,
  pickEmbedLanguage,
  resolveExplorerLocale,
  setEmbedLocaleChoice,
  type AccountLanguageDeps,
  type ExplorerLocaleSources,
} from '@brftech/filex-core/src/lib/explorerLocale';
import {
  LOCALES_LIST_TIMEOUT_MS,
  availableLocales,
  hasLocale,
  loadLocales,
  loadLocalesOnce,
  localesKnown,
  resetLocales,
  setLocalesFromBranding,
} from '@brftech/filex-core/src/lib/uiLocales';
import { resolveLocale } from '@brftech/filex-core/src/locales/resolve';
import { configurePrefs, resetPrefs } from '@brftech/filex-core/src/lib/prefs';
import { userSettingsApi } from '@brftech/filex-core/src/lib/userSettingsHost';

const REPO = path.resolve(__dirname, '../../..');
const read = (rel: string) => readFileSync(path.join(REPO, rel), 'utf8');

/** The server's list: the built-ins plus these packs. */
function offer(...codes: string[]): void {
  setLocalesFromBranding({ ui_locales: codes.map((code) => ({ code, source: 'plugin', plugin: `lang-${code}` })) } as never);
}

afterEach(() => {
  setEmbedLocaleChoice('');
  resetLocales();
  resetPrefs();
  localStorage.clear();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

const embed = (s: Omit<ExplorerLocaleSources, 'embedded'>) => resolveExplorerLocale({ embedded: true, ...s });
const app = (s: Omit<ExplorerLocaleSources, 'embedded'>) => resolveExplorerLocale({ embedded: false, ...s });

describe('an embed: the pick, the account, the browser, the host, English', () => {
  it("(a) the pick made in this browser outranks the account, the browser and the host", () => {
    offer();
    expect(embed({ chosen: 'tr', account: 'en', browser: ['en-US', 'en'], host: 'en' })).toEqual({ locale: 'tr', tier: 'chosen' });
  });

  it("(b) a personal embed: the account's language, above the browser's", () => {
    offer();
    expect(embed({ account: 'tr', browser: ['en-US', 'en'], host: 'en' })).toEqual({ locale: 'tr', tier: 'account' });
  });

  it("(c) the browser's languages, best first, matched against what the server offers - a pack's included", () => {
    offer('es');
    expect(embed({ browser: ['de-DE', 'es-MX', 'en'], host: 'tr' })).toEqual({ locale: 'es', tier: 'browser' });
    expect(embed({ browser: ['tr-TR', 'en-US'], host: 'en' })).toEqual({ locale: 'tr', tier: 'browser' });
    // Without the pack the same browser falls through to English, which it also asks for.
    resetLocales();
    offer();
    expect(embed({ browser: ['de-DE', 'es-MX', 'en'], host: 'tr' })).toEqual({ locale: 'en', tier: 'browser' });
  });

  it("(d) the host's locale only when the browser asks for nothing the server offers", () => {
    offer();
    expect(embed({ browser: ['de-DE', 'fr'], host: 'tr' })).toEqual({ locale: 'tr', tier: 'host' });
  });

  it('(e) English last', () => {
    offer();
    expect(embed({ browser: ['de'], host: '' })).toEqual({ locale: 'en', tier: 'default' });
    expect(embed({})).toEqual({ locale: 'en', tier: 'default' });
    expect(embed({ browser: ['de'], host: 'xx' })).toEqual({ locale: 'en', tier: 'default' });
  });

  it("a pack language picked earlier is held while the list is in flight, and dropped once the server no longer offers it", () => {
    expect(localesKnown.value).toBe(false);
    expect(embed({ chosen: 'es', account: 'tr', browser: ['en'] })).toEqual({ locale: 'es', tier: 'chosen' });
    offer(); // the server's answer: no Spanish (the pack was removed)
    expect(embed({ chosen: 'es', account: 'tr', browser: ['en'] })).toEqual({ locale: 'tr', tier: 'account' });
  });
});

describe('the web and desktop apps are not embeds, and keep #191', () => {
  it('the account, then the host, then the browser, then English - an embed pick does not count', () => {
    offer();
    expect(app({ chosen: 'tr', account: 'en', browser: ['tr'], host: 'tr' })).toEqual({ locale: 'en', tier: 'account' });
    expect(app({ host: 'tr', browser: ['en-US'] })).toEqual({ locale: 'tr', tier: 'host' });
    expect(app({ browser: ['tr-TR'] })).toEqual({ locale: 'tr', tier: 'browser' });
    expect(app({ browser: ['de'] })).toEqual({ locale: 'en', tier: 'default' });
  });

  it("answers exactly what the explorer's `resolveLocale(account || host)` answered before #275", () => {
    offer('es');
    const browsers: string[][] = [['en-US', 'en'], ['tr-TR'], ['es-MX'], ['de-DE']];
    for (const browser of browsers) {
      vi.spyOn(navigator, 'languages', 'get').mockReturnValue(browser);
      for (const account of ['', 'tr', 'en', 'es', 'xx']) {
        for (const host of ['', 'tr', 'en', 'es', 'yy']) {
          const was = resolveLocale(account || host);
          expect(app({ account, host, browser }).locale, `${account}|${host}|${browser}`).toBe(was);
        }
      }
    }
  });

  it('tells an embed by what its host did NOT wire: no accountPrefs and no configurePrefs', () => {
    resetPrefs();
    expect(explorerIsEmbedded({})).toBe(true);
    expect(explorerIsEmbedded({ accountPrefs: { surface: 'desktop' } })).toBe(false);
    configurePrefs({ surface: 'web' });
    expect(explorerIsEmbedded({})).toBe(false);
  });

  it('the web app wires its preferences before it mounts anything, and the desktop names its surface', () => {
    const main = read('web/src/main.ts');
    const wired = main.indexOf("configurePrefs({ surface: 'web'");
    expect(wired).toBeGreaterThan(0);
    expect(wired).toBeLessThan(main.indexOf('.mount('));
    expect(read('desktop/ui/app.html')).toMatch(/accountPrefs: \{ surface: 'desktop' \}/);
  });
});

describe('the pick is kept in this browser, and only there', () => {
  it("writes its own key - never the web app's `filex.locale`, never /api/me/prefs", () => {
    const net = vi.fn();
    vi.stubGlobal('fetch', net);
    setEmbedLocaleChoice('TR');
    expect(embedLocaleChoice()).toBe('tr');
    expect(localStorage.getItem(EMBED_LOCALE_LS_KEY)).toBe('tr');
    expect(EMBED_LOCALE_LS_KEY).toBe('filex.embed.locale');
    expect(localStorage.getItem('filex.locale')).toBeNull();
    setEmbedLocaleChoice('');
    expect(localStorage.getItem(EMBED_LOCALE_LS_KEY)).toBeNull();
    expect(embedLocaleChoice()).toBe('');
    expect(net).not.toHaveBeenCalled();
  });

  it('a value that is not a language tag is no pick', () => {
    setEmbedLocaleChoice('../etc');
    expect(embedLocaleChoice()).toBe('');
    expect(localStorage.getItem(EMBED_LOCALE_LS_KEY)).toBeNull();
  });

  /** A browser whose site data is blocked: every storage call throws. */
  const blockedStorage = () => {
    const refuse = vi.fn(() => {
      throw new DOMException('blocked', 'SecurityError');
    });
    vi.stubGlobal('localStorage', { getItem: refuse, setItem: refuse, removeItem: refuse });
    return refuse;
  };

  it('a browser that refuses storage still switches this page, without an error', () => {
    const refuse = blockedStorage();
    expect(() => setEmbedLocaleChoice('tr')).not.toThrow();
    expect(refuse).toHaveBeenCalled();
    expect(embedLocaleChoice()).toBe('tr');
    vi.unstubAllGlobals();
  });

  it("another tab's pick arrives", () => {
    localStorage.setItem(EMBED_LOCALE_LS_KEY, 'tr');
    window.dispatchEvent(new StorageEvent('storage', { key: EMBED_LOCALE_LS_KEY }));
    expect(embedLocaleChoice()).toBe('tr');
  });

  it('is read when the page loads, so a reload opens in the pick - and an unreadable storage is no pick', async () => {
    localStorage.setItem(EMBED_LOCALE_LS_KEY, 'tr');
    vi.resetModules();
    const fresh = await import('@brftech/filex-core/src/lib/explorerLocale');
    expect(fresh.embedLocaleChoice()).toBe('tr');
    const refuse = blockedStorage();
    vi.resetModules();
    const blocked = await import('@brftech/filex-core/src/lib/explorerLocale');
    expect(refuse).toHaveBeenCalled();
    expect(blocked.embedLocaleChoice()).toBe('');
    vi.unstubAllGlobals();
  });
});

describe("the account's language", () => {
  /** The explorer's dependencies, over the real profile client and a recording fetch. */
  function rig(over: Partial<AccountLanguageDeps> & { user?: { locale?: unknown } | null } = {}) {
    const calls: Array<{ url: string; method: string; body: string }> = [];
    const jsonFetch = vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url, method: String(init?.method ?? 'GET'), body: String(init?.body ?? '') });
      return {} as never;
    });
    const me = vi.fn(async () => ({ user: over.user === undefined ? { locale: '' } : over.user }));
    const profile = userSettingsApi(jsonFetch, 'https://files.example/');
    const d: AccountLanguageDeps = {
      person: true,
      embedded: true,
      host: 'tr',
      me,
      updateProfile: (p) => profile.updateProfile(p),
      ...over,
    };
    return { d, me, calls };
  }
  const settle = () => new Promise((r) => setTimeout(r, 0));

  it('an embed behind an app token asks the account nothing and writes nothing', async () => {
    const { d, me, calls } = rig({ person: false });
    expect(await accountLanguage(d)).toBe('');
    expect(pickEmbedLanguage('tr', d)).toBe('tr');
    await settle();
    expect(me).not.toHaveBeenCalled();
    expect(calls).toEqual([]);
    expect(localStorage.getItem(EMBED_LOCALE_LS_KEY)).toBe('tr');
  });

  it("a personal embed reads the account's language", async () => {
    const { d, calls } = rig({ user: { locale: 'es' } });
    expect(await accountLanguage(d)).toBe('es');
    await settle();
    expect(calls).toEqual([]);
  });

  it("a personal embed does NOT write the host's language to an account that has none", async () => {
    const { d, me, calls } = rig({ user: { locale: '' }, host: 'tr' });
    expect(await accountLanguage(d)).toBe('');
    await settle();
    expect(me).toHaveBeenCalledTimes(1);
    expect(calls).toEqual([]);
  });

  it('the web and desktop apps still seed an account that has none with the host language (#191)', async () => {
    const { d, calls } = rig({ embedded: false, user: { locale: '' }, host: 'tr' });
    expect(await accountLanguage(d)).toBe('');
    await settle();
    expect(calls).toEqual([{ url: 'https://files.example/api/auth/profile', method: 'PATCH', body: '{"locale":"tr"}' }]);
  });

  it('nobody signed in: nothing', async () => {
    const { d, calls } = rig({ me: vi.fn(async () => Promise.reject(new Error('401'))) });
    expect(await accountLanguage(d)).toBe('');
    const empty = rig({ user: null });
    expect(await accountLanguage(empty.d)).toBe('');
    await settle();
    expect(calls).toEqual([]);
    expect(empty.calls).toEqual([]);
  });

  it("a personal embed's pick goes to the account as well; going back to the default leaves the account alone", async () => {
    const { d, calls } = rig();
    expect(pickEmbedLanguage('tr', d)).toBe('tr');
    await settle();
    expect(calls).toEqual([{ url: 'https://files.example/api/auth/profile', method: 'PATCH', body: '{"locale":"tr"}' }]);
    expect(localStorage.getItem(EMBED_LOCALE_LS_KEY)).toBe('tr');
    expect(pickEmbedLanguage('', d)).toBe('');
    await settle();
    expect(calls).toHaveLength(1);
    expect(localStorage.getItem(EMBED_LOCALE_LS_KEY)).toBeNull();
  });

  it('a refused profile write keeps the pick in this browser', async () => {
    const d = { person: true, updateProfile: vi.fn(async () => Promise.reject(new Error('403'))) };
    expect(pickEmbedLanguage('tr', d)).toBe('tr');
    await settle();
    expect(embedLocaleChoice()).toBe('tr');
  });
});

describe("the server's languages, asked with a limit", () => {
  it('a server that never answers: English and Turkish once the limit has passed, and the request is aborted', async () => {
    vi.useFakeTimers();
    const hang = vi.fn((_url: string, _init?: RequestInit) => new Promise<Response>(() => {}));
    const p = loadLocales({ base: 'https://slow.test', fetchImpl: hang as unknown as typeof fetch, timeoutMs: 10_000 });
    let done = false;
    void p.then(() => (done = true));
    await vi.advanceTimersByTimeAsync(9_999);
    expect(done).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(await p).toEqual(availableLocales());
    expect(availableLocales().map((l) => l.code)).toEqual(['en', 'tr']);
    expect(localesKnown.value).toBe(true);
    expect(hang.mock.calls[0][1]?.signal?.aborted).toBe(true);
  });

  it('an answer inside the limit is used as before; without a limit the request carries no signal', async () => {
    const f = vi.fn(async (_url: string, _init?: RequestInit) => ({ ok: true, json: async () => ({ ui_locales: [{ code: 'es', source: 'plugin' }] }) }) as Response);
    await loadLocales({ fetchImpl: f as unknown as typeof fetch, timeoutMs: 10_000 });
    expect(hasLocale('es')).toBe(true);
    resetLocales();
    await loadLocales({ fetchImpl: f as unknown as typeof fetch });
    expect(f.mock.calls[1][1]).not.toHaveProperty('signal');
  });

  it('once per page: two explorers, or a host that already asked, cost one request', async () => {
    const f = vi.fn(async () => ({ ok: true, json: async () => ({ ui_locales: [{ code: 'es', source: 'plugin' }] }) }) as Response);
    await Promise.all([loadLocalesOnce({ fetchImpl: f }), loadLocalesOnce({ fetchImpl: f })]);
    await loadLocalesOnce({ fetchImpl: f });
    expect(f).toHaveBeenCalledTimes(1);
    expect(hasLocale('es')).toBe(true);
  });

  it('the desktop app and an embed wait the same ten seconds', () => {
    expect(LOCALES_LIST_TIMEOUT_MS).toBe(10_000);
    expect(read('desktop/ui/app.html')).toContain(`timeoutMs: ${LOCALES_LIST_TIMEOUT_MS}`);
    expect(read('packages/core/src/FileExplorer.vue')).toMatch(/timeoutMs: LOCALES_LIST_TIMEOUT_MS/);
  });
});
