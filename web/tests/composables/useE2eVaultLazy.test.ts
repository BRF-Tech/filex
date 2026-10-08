// The vault's light half (composables/useE2eVault) and its engine
// (composables/e2eVaultEngine, loaded with import()): until a vault is
// opened or made in a tab, the questions the explorer asks on every listing
// are answered without the engine, and nothing of it is loaded (#94: the
// vault's code took the web app's main chunk over workbox's 2 MiB precache
// limit; web/tests/quality/vaultLazy.test.ts holds the import graph to it).
//
// Red on the old code: there is no e2eVaultEngine module to load.

import { afterEach, describe, expect, it, vi } from 'vitest';

import type { E2eMarker } from '@brftech/filex-core/src/lib/e2ecrypto';

const made = vi.hoisted(() => ({ count: 0 }));

vi.mock('@brftech/filex-core/src/composables/e2eVaultEngine', async (importOriginal) => {
  const real = await importOriginal<typeof import('@brftech/filex-core/src/composables/e2eVaultEngine')>();
  return {
    ...real,
    createVaultEngine: (...a: Parameters<typeof real.createVaultEngine>) => {
      made.count++;
      return real.createVaultEngine(...a);
    },
  };
});

import { useE2eVault, type VaultHost } from '@brftech/filex-core/src/composables/useE2eVault';

const KNOWN_KEY = 'filex.e2e.vaults';

function host(calls: { dropped: string[]; locked: Array<[string, string]> }): VaultHost {
  return {
    http: {
      endpoints: { manager: '/api/files/manager' },
      authHeaders: async (extra = {}) => ({ ...extra }),
      credentialsMode: () => 'same-origin',
      downloadUrl: (p) => p,
    },
    headersNow: () => ({}),
    t: (k) => k,
    toast: () => undefined,
    clientKind: () => 'web',
    dropKeys: (r) => void calls.dropped.push(r),
    onLocked: (r, why) => void calls.locked.push([r, why]),
    onChanged: () => undefined,
  };
}

afterEach(() => {
  localStorage.removeItem(KNOWN_KEY);
});

describe('the vault before its engine is loaded', () => {
  it('answers from the vault folders this browser remembers, and loads nothing', () => {
    localStorage.setItem(KNOWN_KEY, JSON.stringify(['docs://Kasa']));
    const before = made.count;
    const vault = useE2eVault(host({ dropped: [], locked: [] }));

    // A path below a remembered vault is asked about as the vault folder.
    expect(vault.rootOf('docs://Kasa/Belgeler/not.txt')).toBe('docs://Kasa');
    expect(vault.rootOf('docs://Kasa2/x')).toBeNull();
    expect(vault.roots()).toEqual(['docs://Kasa']);
    expect(vault.relOf('docs://Kasa', 'docs://Kasa/Belgeler/')).toBe('Belgeler');
    expect(vault.isVaultRow({ vault_root: 'docs://Kasa' } as never)).toBe(true);
    expect(vault.isVaultRow({ path: 'docs://x' } as never)).toBe(false);

    // Nothing is open, so nothing lists.
    expect(vault.isOpen('docs://Kasa')).toBe(false);
    expect(vault.rows('docs://Kasa')).toBeNull();
    expect(vault.search('docs://Kasa', 'not')).toBeNull();
    expect(vault.node('docs://Kasa/not.txt')).toBeNull();
    expect(vault.permOf('docs://Kasa')).toBeUndefined();

    // What the explorer calls on every visibility change, page hide, realtime
    // frame and unmount does not load it either.
    vault.touch('docs://Kasa/x');
    vault.onVisible();
    vault.onPageHide();
    vault.onEvent({ type: 'vault.generation', path: 'docs://Kasa', generation: 9 });
    vault.dismiss('docs://Kasa');
    vault.close('docs://Kasa');
    vault.closeAll('manual');
    expect(made.count).toBe(before);

    vault.forget('docs://Kasa');
    expect(vault.rootOf('docs://Kasa/x')).toBeNull();
    expect(JSON.parse(localStorage.getItem(KNOWN_KEY) ?? '[]')).toEqual([]);
  });

  it('"Lock" without an open vault still drops the keys and shows the lock screen', () => {
    const calls = { dropped: [] as string[], locked: [] as Array<[string, string]> };
    const before = made.count;
    const vault = useE2eVault(host(calls));
    vault.lock('docs://Kasa');
    expect(calls.dropped).toEqual(['docs://Kasa']);
    expect(calls.locked).toEqual([['docs://Kasa', 'manual']]);
    expect(made.count).toBe(before);
  });

  it('opening loads the engine, once per explorer', async () => {
    const before = made.count;
    const vault = useE2eVault(host({ dropped: [], locked: [] }));
    const notAVault = { v: 2, salt: 'c2FsdA==', iter: 1000, verify: 'dmVy' } as unknown as E2eMarker;
    const fmk = {} as CryptoKey;
    // A key file that is not a vault's is refused by the engine, before any
    // request: it had to be loaded to say so.
    await expect(vault.open('docs://Kasa', notAVault, fmk)).rejects.toThrow('vault: not a vault key file');
    await expect(vault.open('docs://Kasa', notAVault, fmk)).rejects.toThrow('vault: not a vault key file');
    expect(made.count).toBe(before + 1);
    expect(vault.isOpen('docs://Kasa')).toBe(false);
  });
});
