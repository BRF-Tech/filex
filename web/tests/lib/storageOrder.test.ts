// The order a person puts their storages in (GitHub #57, "Sidebar Storage
// sorting or manual reordering").
//
// The rules these tests hold:
//   - THREE layers, most personal first: the person's own saved order, else
//     the administrator's order (`sortOrder`, 1 = first; storages without one
//     after the placed ones), else the order the host handed over — so
//     nothing changes for anybody when nobody reorders;
//   - a saved order wins for the storages it names; a storage it does NOT name
//     (added since) keeps the position the administrator's / default order
//     gives it, and the named ones fill the other positions in the person's
//     order; a name it holds that no longer exists is dropped without a word;
//   - move up / move down / drop-at / sort-by-name all produce the WHOLE new
//     order, and "reset" is the empty order;
//   - a storage this view cannot see keeps its place in the saved order when
//     the visible ones are reordered, so an order made in one place is not
//     erased by a reorder made in a place that sees fewer drives;
//   - the order lives on the ACCOUNT beside the palette (`lib/prefs`, key
//     `storageOrder`), so it follows the person to their next browser, and a
//     palette change afterwards does not drop it.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  dropStorageAt,
  mergeStorageOrder,
  moveStorage,
  orderStorages,
  parseStorageOrder,
  resetStorageOrderState,
  saveStorageOrder,
  serializeStorageOrder,
  sortStoragesByName,
  storageOrderKey,
  useStorageOrder,
} from '@brftech/filex-core/src/lib/storageOrder';
import {
  PREFS_PUT_DEBOUNCE_MS,
  PREF_KEYS,
  PREF_LS_KEYS,
  configurePrefs,
  currentPrefs,
  flushPrefs,
  forgetPersonalPrefs,
  hydratePrefs,
  resetPrefs,
  savePref,
} from '@brftech/filex-core/src/lib/prefs';

const S = (name: string, extra: Record<string, unknown> = {}) => ({ name, ...extra });
const names = (list: Array<{ name: string }>) => list.map((s) => s.name);

function answer(status: number, body: unknown) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}

beforeEach(() => {
  resetPrefs();
  resetStorageOrderState();
  localStorage.clear();
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
  resetPrefs();
  resetStorageOrderState();
});

describe('applying a saved order', () => {
  const host = [S('Work'), S('Photos'), S('Archive')];

  it('changes nothing when the person never reordered', () => {
    expect(names(orderStorages(host, []))).toEqual(['Work', 'Photos', 'Archive']);
  });

  it('puts the storages in the saved order', () => {
    expect(names(orderStorages(host, ['Archive', 'Work', 'Photos']))).toEqual(['Archive', 'Work', 'Photos']);
  });

  it('a storage the order does not name keeps its default position; the named ones fill the rest', () => {
    // A drive added at the END by the server stays at the end…
    const more = [...host, S('New B')];
    expect(names(orderStorages(more, ['Archive', 'Photos', 'Work']))).toEqual(['Archive', 'Photos', 'Work', 'New B']);
    // …and one the default order puts FIRST stays first; the person's three
    // keep their own order around it.
    const first = [S('New A'), ...host];
    expect(names(orderStorages(first, ['Archive', 'Photos', 'Work']))).toEqual(['New A', 'Archive', 'Photos', 'Work']);
  });

  it('drops a saved name that no longer exists, silently', () => {
    expect(names(orderStorages(host, ['Gone', 'Archive', 'Deleted', 'Photos', 'Work']))).toEqual([
      'Archive',
      'Photos',
      'Work',
    ]);
  });

  it('matches a storage by its uid first and its name second', () => {
    const withUid = [S('Work', { uid: 'u-1' }), S('Photos', { uid: 'u-2' }), S('Archive')];
    // `u-2` survives a rename; `Work` is an order saved before the host sent uids.
    expect(names(orderStorages(withUid, ['u-2', 'Archive', 'Work']))).toEqual(['Photos', 'Archive', 'Work']);
    expect(storageOrderKey(withUid[0])).toBe('u-1');
    expect(storageOrderKey(withUid[2])).toBe('Archive');
  });
});

describe("the administrator's order", () => {
  // `sortOrder` as the server sends it: 1 = first, null/absent = not placed.
  const host = [S('Work'), S('Photos', { sortOrder: 2 }), S('Archive', { sortOrder: 1 }), S('Team', { sortOrder: null })];

  it('is the default: placed storages first by position, the rest after in the host order', () => {
    expect(names(orderStorages(host, []))).toEqual(['Archive', 'Photos', 'Work', 'Team']);
  });

  it("gives way to the person's own order", () => {
    expect(names(orderStorages(host, ['Team', 'Work', 'Photos', 'Archive']))).toEqual([
      'Team',
      'Work',
      'Photos',
      'Archive',
    ]);
  });

  it('comes back when the person lets their own order go (the empty order)', () => {
    expect(names(orderStorages(host, ['Team', 'Work']))).not.toEqual(names(orderStorages(host, [])));
    expect(names(orderStorages(host, []))).toEqual(['Archive', 'Photos', 'Work', 'Team']);
  });

  it('places a storage the person never saw where the administrator put it', () => {
    // The person ordered three; the administrator then added "New" at the top.
    const later = [...host.slice(0, 3), S('New', { sortOrder: 0 })];
    expect(names(orderStorages(later, ['Work', 'Photos', 'Archive']))).toEqual(['New', 'Work', 'Photos', 'Archive']);
  });

  it('a host that sends no positions keeps its own order (the desktop app sends names only)', () => {
    expect(names(orderStorages([S('b'), S('a')], []))).toEqual(['b', 'a']);
  });
});

describe('the stored form', () => {
  it('is a JSON list of keys; empty means "never reordered"', () => {
    expect(serializeStorageOrder(['a', 'b'])).toBe('["a","b"]');
    expect(serializeStorageOrder([])).toBe('');
    expect(parseStorageOrder('["a","b"]')).toEqual(['a', 'b']);
  });

  it('reads anything else as no order at all', () => {
    for (const bad of ['', 'nope', '{"a":1}', '42', undefined, null]) {
      expect(parseStorageOrder(bad as string | undefined)).toEqual([]);
    }
    // Duplicates and non-strings are dropped, not obeyed.
    expect(parseStorageOrder('["a",1,"b","a",null,""]')).toEqual(['a', 'b']);
  });
});

describe('the gestures', () => {
  const list = [S('Work'), S('Photos'), S('Archive')];

  it('move up / move down swap with the neighbour and return the whole order', () => {
    expect(moveStorage(list, 'Photos', -1)).toEqual(['Photos', 'Work', 'Archive']);
    expect(moveStorage(list, 'Photos', 1)).toEqual(['Work', 'Archive', 'Photos']);
  });

  it('refuses a move past either end', () => {
    expect(moveStorage(list, 'Work', -1)).toBeNull();
    expect(moveStorage(list, 'Archive', 1)).toBeNull();
    expect(moveStorage(list, 'Nobody', 1)).toBeNull();
  });

  it('a drop lands in the gap it was released over', () => {
    // Gaps are counted among the rows on screen, the dragged one included.
    expect(dropStorageAt(list, 'Archive', 0)).toEqual(['Archive', 'Work', 'Photos']);
    expect(dropStorageAt(list, 'Work', 3)).toEqual(['Photos', 'Archive', 'Work']);
    expect(dropStorageAt(list, 'Work', 2)).toEqual(['Photos', 'Work', 'Archive']);
  });

  it('a drop into its own two gaps changes nothing', () => {
    expect(dropStorageAt(list, 'Photos', 1)).toBeNull();
    expect(dropStorageAt(list, 'Photos', 2)).toBeNull();
  });

  it('sort by name reads the label a person sees, numbers as numbers, case ignored', () => {
    const disks = [S('d10', { label: 'Disk 10' }), S('zeta'), S('d2', { label: 'Disk 2' }), S('alpha')];
    expect(sortStoragesByName(disks)).toEqual(['alpha', 'd2', 'd10', 'zeta']);
  });
});

describe('keeping what this view cannot see', () => {
  it('a storage hidden here keeps its place after the one it followed', () => {
    const visible = [S('Work'), S('Archive')];
    // `Team` is in the saved order but this view does not draw it (a grant
    // that is not here, or a host that hands over fewer drives).
    const saved = ['Work', 'Team', 'Archive'];
    expect(mergeStorageOrder(['Archive', 'Work'], visible, saved)).toEqual(['Archive', 'Work', 'Team']);
  });

  it('a hidden storage at the head stays at the head', () => {
    expect(mergeStorageOrder(['B', 'A'], [S('A'), S('B')], ['Team', 'A', 'B'])).toEqual(['Team', 'B', 'A']);
  });

  it('a visible storage saved under its name is not kept twice when it now has a uid', () => {
    const visible = [S('Work', { uid: 'u-1' }), S('Archive')];
    expect(mergeStorageOrder(['Archive', 'u-1'], visible, ['Work', 'Archive'])).toEqual(['Archive', 'u-1']);
  });
});

describe('where the order is kept', () => {
  it('lives on the account document, so a palette change does not drop it', async () => {
    expect(PREF_KEYS).toContain('storageOrder');
    const f = vi.fn(async () => answer(200, { palette: 'night-blue' }));
    configurePrefs({ surface: 'web', fetchImpl: f as unknown as typeof fetch });
    await hydratePrefs();

    saveStorageOrder(['Archive', 'Work'], [S('Work'), S('Archive')]);
    savePref('palette', 'default');
    await vi.advanceTimersByTimeAsync(PREFS_PUT_DEBOUNCE_MS + 10);
    await flushPrefs();

    // The LAST write — the palette's — still carries the order.
    const put = f.mock.calls.filter((c) => (c[1] as RequestInit)?.method === 'PUT').pop();
    expect(put).toBeTruthy();
    const body = JSON.parse((put![1] as RequestInit).body as string);
    expect(body.prefs.storageOrder).toBe('["Archive","Work"]');
    expect(body.prefs.palette).toBe('default');
    // …and this browser's first paint reads the same answer.
    expect(localStorage.getItem(PREF_LS_KEYS.storageOrder)).toBe('["Archive","Work"]');
  });

  it('is sent at once, not after the debounce — the account outranks this browser on the next load', async () => {
    // ⚠ Measured in the browser (e2e 158): a reorder followed by a closed tab
    // or a reload inside the 400 ms debounce never reached the account, and
    // the next load read the OLD order back from the server over the mirror.
    const f = vi.fn(async () => answer(200, { palette: 'night-blue' }));
    configurePrefs({ surface: 'web', fetchImpl: f as unknown as typeof fetch });
    await hydratePrefs();
    saveStorageOrder(['Archive', 'Work'], [S('Work'), S('Archive')]);
    // No timer advanced: only microtasks.
    for (let i = 0; i < 5; i++) await Promise.resolve();
    const puts = f.mock.calls.filter((c) => (c[1] as RequestInit)?.method === 'PUT');
    expect(puts).toHaveLength(1);
    expect(JSON.parse((puts[0][1] as RequestInit).body as string).prefs.storageOrder).toBe('["Archive","Work"]');
  });

  it('arrives with the account and reorders a mounted explorer', async () => {
    const order = useStorageOrder();
    expect(order.saved.value).toEqual([]);
    const f = vi.fn(async () => answer(200, { storageOrder: '["Photos","Work"]' }));
    configurePrefs({ surface: 'web', fetchImpl: f as unknown as typeof fetch });
    await hydratePrefs();
    expect(order.saved.value).toEqual(['Photos', 'Work']);
    expect(order.custom.value).toBe(true);
  });

  it('is never written to the account before the account has answered', async () => {
    let release: (v: unknown) => void = () => {};
    const f = vi.fn((_url: string, init?: RequestInit) => {
      if (init?.method === 'PUT') return Promise.resolve(answer(200, { ok: true }));
      return new Promise((r) => {
        release = r;
      });
    });
    configurePrefs({ surface: 'web', fetchImpl: f as unknown as typeof fetch });
    const hydrating = hydratePrefs();

    saveStorageOrder(['B', 'A'], [S('A'), S('B')]);
    await vi.advanceTimersByTimeAsync(PREFS_PUT_DEBOUNCE_MS * 3);
    expect(f.mock.calls.filter((c) => (c[1] as RequestInit)?.method === 'PUT')).toHaveLength(0);
    // The screen already has it.
    expect(useStorageOrder().saved.value).toEqual(['B', 'A']);

    release(answer(200, { palette: 'night-blue', locale: 'tr' }));
    await hydrating;
    await vi.advanceTimersByTimeAsync(PREFS_PUT_DEBOUNCE_MS + 10);
    await flushPrefs();
    const puts = f.mock.calls.filter((c) => (c[1] as RequestInit)?.method === 'PUT');
    expect(puts).toHaveLength(1);
    const body = JSON.parse((puts[0][1] as RequestInit).body as string);
    // The account's own palette and language survive the write.
    expect(body.prefs).toEqual({ palette: 'night-blue', locale: 'tr', storageOrder: '["B","A"]' });
    expect(useStorageOrder().saved.value).toEqual(['B', 'A']);
  });

  it('an embed with no account copy keeps it in this browser and asks no server', async () => {
    const f = vi.fn(async () => answer(200, {}));
    vi.stubGlobal('fetch', f);
    try {
      saveStorageOrder(['B', 'A'], [S('A'), S('B')]);
      await vi.advanceTimersByTimeAsync(PREFS_PUT_DEBOUNCE_MS * 3);
      expect(f).not.toHaveBeenCalled();
      expect(localStorage.getItem(PREF_LS_KEYS.storageOrder)).toBe('["B","A"]');
      // A new mount reads it back.
      resetStorageOrderState();
      expect(useStorageOrder().saved.value).toEqual(['B', 'A']);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('reset is the empty order, on the account and in the mirror', async () => {
    const f = vi.fn(async () => answer(200, { storageOrder: '["B","A"]' }));
    configurePrefs({ surface: 'web', fetchImpl: f as unknown as typeof fetch });
    await hydratePrefs();
    const order = useStorageOrder();
    expect(order.custom.value).toBe(true);

    saveStorageOrder([], [S('A'), S('B')]);
    expect(order.saved.value).toEqual([]);
    expect(order.custom.value).toBe(false);
    expect(localStorage.getItem(PREF_LS_KEYS.storageOrder)).toBeNull();
    await vi.advanceTimersByTimeAsync(PREFS_PUT_DEBOUNCE_MS + 10);
    await flushPrefs();
    expect(currentPrefs().storageOrder ?? '').toBe('');
  });

  it('goes with the person at sign-out', async () => {
    const f = vi.fn(async () => answer(200, { storageOrder: '["B","A"]' }));
    configurePrefs({ surface: 'web', fetchImpl: f as unknown as typeof fetch });
    await hydratePrefs();
    expect(localStorage.getItem(PREF_LS_KEYS.storageOrder)).toBe('["B","A"]');
    forgetPersonalPrefs();
    expect(localStorage.getItem(PREF_LS_KEYS.storageOrder)).toBeNull();
    resetStorageOrderState();
    expect(useStorageOrder().saved.value).toEqual([]);
  });
});
