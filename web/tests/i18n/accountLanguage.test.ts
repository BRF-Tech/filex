// The screen's language IS the account's (#191, the maintainers' rule,
// 2026-10-08).
//
// The server says every notification - the bell, a push to the phone, an
// email, the desktop app's toast - in the language of the person's ACCOUNT.
// A browser that kept a language of its own showed its screen in one
// language and the same person's notifications in another.
//
// ⚠ Red on the code before: this browser's `filex.locale` outranked the
// account's users.locale, so a language changed on another surface (the
// desktop app, the profile form) never reached a browser that had once
// picked one; and an account with no language of its own stayed without one
// while the screen spoke the browser's.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

interface Call {
  url: string;
  method: string;
  body: string;
}

function stubServer(): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url: String(url), method: String(init?.method ?? 'GET'), body: String(init?.body ?? '') });
      return { ok: true, status: 200, json: async () => ({ ok: true, prefs: {} }) };
    }),
  );
  return calls;
}

async function freshI18n() {
  vi.resetModules();
  return import('@/i18n');
}

function lastPut(calls: Call[]): { prefs?: { locale?: string } } {
  const puts = calls.filter((c) => c.method === 'PUT' && c.url.includes('/api/me/prefs'));
  expect(puts.length).toBeGreaterThan(0);
  return JSON.parse(puts[puts.length - 1].body) as { prefs?: { locale?: string } };
}

beforeEach(() => {
  localStorage.clear();
});
afterEach(() => {
  vi.unstubAllGlobals();
});

// Cold imports (vi.resetModules), as in packLocaleBoot.test.ts.
const COLD = { timeout: 30_000 };

describe("the screen's language is the account's", COLD, () => {
  it("the account's language outranks this browser's", async () => {
    stubServer();
    localStorage.setItem('filex.locale', 'en');
    const mod = await freshI18n();
    expect(mod.i18n.global.locale.value).toBe('en');
    // users.locale arrives from /api/auth/me.
    mod.applyAccountLocale('tr');
    expect(mod.i18n.global.locale.value).toBe('tr');
  });

  it('a language picked on screen is written to the account at once', async () => {
    const calls = stubServer();
    const mod = await freshI18n();
    mod.applyAccountLocale('en');
    mod.setStoredLocale('tr');
    // Not after the debounce: the write is awaited by whatever asks the
    // server for words said in the account's language (the notification
    // lists).
    await mod.accountLocaleWritten();
    expect(lastPut(calls).prefs?.locale).toBe('tr');
    expect(mod.i18n.global.locale.value).toBe('tr');
  });

  it('an account that holds no language is given one by the server at the sign-in, not by this page', async () => {
    // 0.54 full run (001b652e): the panel wrote the screen's language to such
    // an account from the page the sign-in was leaving (/admin/login ->
    // /drive/), the write was cut off or sent from pagehide, and the browser
    // suites hung on it. The sign-in now carries the screen's language and the
    // server records it (handlers/auth.go adoptSignInLanguage); the panel has
    // no adoption of its own left to call.
    stubServer();
    const mod = await freshI18n();
    expect('adoptScreenLocale' in mod).toBe(false);
  });
});
