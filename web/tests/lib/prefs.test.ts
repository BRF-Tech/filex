// The look-and-language preferences live on the ACCOUNT, not in one browser.
//
// ⚠⚠ The bug: "the palette I picked in one browser is not there in the other
// one". Theme, palette, density and language were each stored in
// `localStorage` and nowhere else, so they were facts about a MACHINE. They
// are facts about a person.
//
// ⚠ localStorage is not gone and must not be: it is the FIRST-PAINT cache.
// The fetch cannot beat the first frame, so the window paints from the cache
// and re-paints when the account answers — no flash of the stock palette.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  PREFS_PUT_DEBOUNCE_MS,
  PREF_LS_KEYS,
  configurePrefs,
  currentPrefs,
  flushPrefs,
  hydratePrefs,
  localPref,
  localPrefs,
  onPrefs,
  prefsHydrated,
  resetPrefs,
  savePref,
} from '@brftech/filex-core';

function answer(status: number, body: unknown) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}

beforeEach(() => {
  resetPrefs();
  localStorage.clear();
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
  resetPrefs();
});

describe('reading the account’s answer', () => {
  it('asks for the surface it was configured with and mirrors what comes back', async () => {
    const f = vi.fn(async () => answer(200, { theme: 'dark', palette: 'night-blue', density: 'compact', locale: 'tr' }));
    configurePrefs({ surface: 'web', fetchImpl: f as unknown as typeof fetch });
    const got = await hydratePrefs();
    expect(f.mock.calls[0][0]).toBe('/api/me/prefs?surface=web');
    expect(got).toEqual({ theme: 'dark', palette: 'night-blue', density: 'compact', locale: 'tr' });
    expect(prefsHydrated()).toBe(true);
    // The mirror now holds the ACCOUNT's answer, under the keys the
    // first-paint readers already use.
    expect(localStorage.getItem(PREF_LS_KEYS.palette)).toBe('night-blue');
    expect(localPrefs()).toEqual({ theme: 'dark', palette: 'night-blue', density: 'compact', locale: 'tr' });
  });

  it('the desktop app is the SAME code with another surface name', async () => {
    // ⚠ The one thing that may differ per surface is the query. A branch on
    // "am I the desktop app?" anywhere in that module is the bug this
    // product keeps having to un-write.
    const f = vi.fn(async () => answer(200, {}));
    configurePrefs({ surface: 'desktop', fetchImpl: f as unknown as typeof fetch });
    await hydratePrefs();
    expect(f.mock.calls[0][0]).toBe('/api/me/prefs?surface=desktop');
  });

  it('an older server, no session or no network is NOT an error', async () => {
    // The mirror has already painted the window; a preference is the last
    // thing that should put an error in front of somebody.
    configurePrefs({ surface: 'web', fetchImpl: (async () => answer(404, {})) as unknown as typeof fetch });
    expect(await hydratePrefs()).toBeNull();
    configurePrefs({
      surface: 'web',
      fetchImpl: (async () => {
        throw new Error('offline');
      }) as unknown as typeof fetch,
    });
    expect(await hydratePrefs()).toBeNull();
    expect(prefsHydrated()).toBe(false);
  });

  it('ignores anything that is not one of the four keys', async () => {
    const f = vi.fn(async () => answer(200, { prefs: { theme: 'dark', rogue: 'x', density: 7 } }));
    configurePrefs({ surface: 'web', fetchImpl: f as unknown as typeof fetch });
    // ⚠ `{prefs: {…}}` and a bare object are both accepted: the wrapper is a
    // shape the server may grow, not a contract worth failing over.
    expect(await hydratePrefs()).toEqual({ theme: 'dark' });
  });
});

describe('the first load after the upgrade', () => {
  it('an account that has stored NOTHING adopts what this browser already had', async () => {
    // ⚠ Every one of these preferences lived in localStorage alone until v3.
    // Meeting an empty document and clearing the mirror would hand the person
    // back the stock palette they had chosen their way out of — the silent
    // reset this module exists to prevent, arriving with the fix for it.
    localStorage.setItem(PREF_LS_KEYS.palette, 'night-blue');
    localStorage.setItem(PREF_LS_KEYS.density, 'compact');
    const f = vi.fn(async () => answer(200, { prefs: {} }));
    configurePrefs({ surface: 'web', fetchImpl: f as unknown as typeof fetch });

    expect(await hydratePrefs()).toEqual({ palette: 'night-blue', density: 'compact' });
    expect(localPref('palette')).toBe('night-blue');

    // ...and the account is told, so the NEXT browser gets them too.
    vi.advanceTimersByTime(PREFS_PUT_DEBOUNCE_MS);
    await flushPrefs();
    const put = f.mock.calls.find((c) => (c[1] as RequestInit)?.method === 'PUT');
    expect(put).toBeTruthy();
    expect(JSON.parse((put![1] as RequestInit).body as string)).toEqual({
      prefs: { palette: 'night-blue', density: 'compact' },
    });
  });

  it('one stored key is enough to make the ACCOUNT the answer', async () => {
    // Somebody who cleared their palette on another device has a document.
    // Nothing here may resurrect what they cleared.
    localStorage.setItem(PREF_LS_KEYS.palette, 'night-blue');
    const f = vi.fn(async () => answer(200, { prefs: { theme: 'dark' } }));
    configurePrefs({ surface: 'web', fetchImpl: f as unknown as typeof fetch });

    expect(await hydratePrefs()).toEqual({ theme: 'dark' });
    expect(localPref('palette')).toBe('');
    vi.advanceTimersByTime(PREFS_PUT_DEBOUNCE_MS);
    await flushPrefs();
    expect(f.mock.calls.every((c) => (c[1] as RequestInit)?.method !== 'PUT')).toBe(true);
  });

  it('nothing on either side writes nothing', async () => {
    const f = vi.fn(async () => answer(200, { prefs: {} }));
    configurePrefs({ surface: 'web', fetchImpl: f as unknown as typeof fetch });
    expect(await hydratePrefs()).toEqual({});
    vi.advanceTimersByTime(PREFS_PUT_DEBOUNCE_MS);
    await flushPrefs();
    expect(f.mock.calls.every((c) => (c[1] as RequestInit)?.method !== 'PUT')).toBe(true);
  });
});

describe('writing one', () => {
  it('mirrors immediately and PUTs once, debounced', async () => {
    const f = vi.fn(async () => answer(200, {}));
    configurePrefs({ surface: 'web', fetchImpl: f as unknown as typeof fetch });

    savePref('theme', 'dark');
    savePref('palette', 'night-blue');
    // ⚠ On screen and in the mirror BEFORE the network is touched: a failed
    // write must still leave this device the way the person left it.
    expect(localPref('theme')).toBe('dark');
    expect(currentPrefs()).toEqual({ theme: 'dark', palette: 'night-blue' });
    expect(f).not.toHaveBeenCalled();

    vi.advanceTimersByTime(PREFS_PUT_DEBOUNCE_MS);
    await flushPrefs();
    expect(f).toHaveBeenCalledTimes(1);
    const init = f.mock.calls[0][1] as RequestInit;
    expect(init.method).toBe('PUT');
    // ⚠⚠ The WHOLE document, wrapped in `{prefs: …}`. The endpoint REPLACES
    // what it is given (handlers/userprefs.go), so sending only what changed
    // would drop every other preference the account holds — a data loss that
    // looks like "my palette reset itself".
    expect(JSON.parse(init.body as string)).toEqual({ prefs: { theme: 'dark', palette: 'night-blue' } });
  });

  it('a failed PUT is swallowed — the device keeps the choice', async () => {
    configurePrefs({
      surface: 'web',
      fetchImpl: (async () => {
        throw new Error('offline');
      }) as unknown as typeof fetch,
    });
    savePref('density', 'compact');
    vi.advanceTimersByTime(PREFS_PUT_DEBOUNCE_MS);
    await expect(flushPrefs()).resolves.toBeUndefined();
    expect(localPref('density')).toBe('compact');
  });

  it('tells the listeners when the account’s answer lands', async () => {
    const seen: unknown[] = [];
    configurePrefs({ surface: 'web', fetchImpl: (async () => answer(200, { locale: 'tr' })) as unknown as typeof fetch });
    const off = onPrefs((p) => seen.push(p));
    await hydratePrefs();
    expect(seen).toEqual([{ locale: 'tr' }]);
    off();
  });
});
