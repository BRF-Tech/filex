// An explorer embedded in another product keeps its preferences to itself.
//
// ⚠ The bug (0.57, measured on portal.example.com 2026-10-16): the fishapp admin
// page embeds <filex-explorer> behind a host proxy (`apiBase:
// '/admin/fishapp/filex-api'`). Opening an app records the version the person
// has seen (`markAppSeen` -> `savePref`), and the write went to
// `PUT /api/me/prefs?surface=web` on the HOST page's origin - a 404 on every
// opening. The host never calls `configurePrefs`, so the address was
// `appBase()`, the product's own root, not filex.
//
// The fix is not the explorer's `apiBase` either: behind a proxy with one
// shared token every person of that product is ONE filex account, and a PUT of
// a document this page never read replaces the whole record. Unconfigured,
// nothing is sent: the choice stays on screen and in this browser's mirror.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  PREFS_PUT_DEBOUNCE_MS,
  accountFetch,
  configurePrefs,
  currentPrefs,
  flushPrefs,
  hydratePrefs,
  localPref,
  prefsConfigured,
  prefsSettled,
  resetPrefs,
  savePref,
} from '@brftech/filex-core/src/lib/prefs';

function answer(status: number, body: unknown) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}

let network: ReturnType<typeof vi.fn>;

beforeEach(() => {
  resetPrefs();
  localStorage.clear();
  vi.useFakeTimers();
  network = vi.fn(async () => answer(200, {}));
  vi.stubGlobal('fetch', network);
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  resetPrefs();
});

describe('an embed that never wired the account document', () => {
  it('keeps a written preference on this page and sends nothing to the host page', async () => {
    expect(prefsConfigured()).toBe(false);

    savePref('theme', 'dark');
    savePref('appsSeen', JSON.stringify({ 'office-editor': '0.3.1' }));
    expect(localPref('theme')).toBe('dark');
    expect(currentPrefs()).toEqual({ theme: 'dark', appsSeen: '{"office-editor":"0.3.1"}' });

    vi.advanceTimersByTime(PREFS_PUT_DEBOUNCE_MS);
    await flushPrefs();

    expect(network).not.toHaveBeenCalled();
  });

  it('reads nothing either, and says the question is settled', async () => {
    expect(await hydratePrefs()).toBeNull();
    expect(prefsSettled()).toBe(true);
    expect(network).not.toHaveBeenCalled();
  });

  it('an account route (open-with) is not asked on the host page', async () => {
    expect(await accountFetch('/api/me/open-with/docx', { method: 'PUT', body: '{}' })).toBeNull();
    expect(network).not.toHaveBeenCalled();
  });

  it('a host that wires it still writes, through its own transport', async () => {
    const f = vi.fn(async () => answer(200, {}));
    configurePrefs({ surface: 'web', fetchImpl: f as unknown as typeof fetch });

    savePref('theme', 'dark');
    vi.advanceTimersByTime(PREFS_PUT_DEBOUNCE_MS);
    await flushPrefs();

    expect(f).toHaveBeenCalledTimes(1);
    expect(network).not.toHaveBeenCalled();
  });
});
