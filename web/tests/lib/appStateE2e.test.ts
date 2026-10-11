// filex 0.56, task #189 - an app's own small store in an ENCRYPTED SESSION
// (lib/appStateE2e): what the app keeps with `state.set` while it holds a
// file of an encrypted folder in the clear is sealed in this tab with a key
// derived from the folder key, one ledger per app, and stored apart from the
// app's ordinary store (lib/appState).
//
// Held here:
//  - what reaches the server (the account document's PUT) carries no
//    plaintext - neither the value nor the key the app chose;
//  - a ledger read back opens to what was kept;
//  - the encrypted ledger and the ordinary store never mix;
//  - each app, and each folder, has its own ledger: one moved into another's
//    place does not open (it reads as empty, not as an error);
//  - the size rules hold for the sealed form, encryption included;
//  - mid re-key the previous key's ledger is read, and the first write moves
//    it to the new key;
//  - a vault's folder key (an HKDF key) gives a ledger too;
//  - each ACCOUNT has its own ledger: a folder key is shared by everybody who
//    may open the folder, and one member's ledger copied into another
//    member's preferences reads as empty there; with no known account there
//    is no sealed store at all.
//
// Red before 0.56: there was no lib/appStateE2e. The account's half: red
// before the ledger was bound to the account (one member's ledger opened in
// another's preferences).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { appStateLedger, sealedStateGet, sealedStateSet } from '@brftech/filex-core/src/lib/appStateE2e';
import { AppStateTooLarge, appStateGet, appStateSet } from '@brftech/filex-core/src/lib/appState';
import { configurePrefs, currentPrefs, flushPrefs, resetPrefs, savePref } from '@brftech/filex-core/src/lib/prefs';
import { resetServerRules, serverLimit, takeServerRules } from '@brftech/filex-core/src/lib/serverRules';

const APP = 'office-editor';
const SECRET = 'Gizli bütçe 2027: ğüşıöç İĞÜŞÖÇ';

let puts: string[] = [];
let seeded: number | undefined;

/** The account document's transport, answering `/api/auth/me` as `account`
 *  (null: nobody signed in). */
function signedInAs(account: number | null) {
  const fetchImpl = vi.fn(async (url: unknown, init?: RequestInit) => {
    if (String(url).endsWith('/api/auth/me')) {
      return account === null
        ? new Response('{"error":"unauthenticated"}', { status: 401 })
        : new Response(JSON.stringify({ user: { id: account, name: `person ${account}` } }), { status: 200 });
    }
    if (init?.method === 'PUT') puts.push(String(init.body ?? ''));
    return new Response('{}', { status: 200 });
  });
  configurePrefs({ surface: 'web', fetchImpl: fetchImpl as unknown as typeof fetch });
  return fetchImpl;
}

beforeEach(() => {
  puts = [];
  seeded = serverLimit('app_state_max_bytes');
  signedInAs(7);
});

afterEach(() => {
  resetPrefs();
  takeServerRules({ limits: { app_state_max_bytes: seeded ?? 16384 } });
});

const folderKey = () => crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt']);

/** The same folder key as each member's browser holds it: its own key object
 *  from the same 32 bytes. */
const sharedFolderKey = (raw: Uint8Array<ArrayBuffer>) => crypto.subtle.importKey('raw', raw, { name: 'AES-GCM' }, false, ['encrypt', 'decrypt']);

/** A ledger that must exist (the session's account is known). */
async function ledgerOf(fmk: CryptoKey, app: string, previous?: CryptoKey | null) {
  const l = await appStateLedger(fmk, app, previous);
  if (!l) throw new Error('no ledger: the account was not known');
  return l;
}

function sealedStore(): Record<string, Record<string, string>> {
  return JSON.parse(currentPrefs().appStateE2e ?? '{}');
}

describe('an encrypted session’s store is sealed in the browser', () => {
  it('the server is sent no plaintext, and the ledger opens to what was kept', async () => {
    const ledger = await ledgerOf(await folderKey(), APP);
    await sealedStateSet(APP, ledger, 'lastSearch', SECRET);
    await sealedStateSet(APP, ledger, 'zoom', 125);
    await flushPrefs();

    expect(puts.length).toBeGreaterThan(0);
    for (const body of puts) {
      expect(body).not.toContain('Gizli');
      expect(body).not.toContain('bütçe');
      expect(body, 'the key the app chose is sealed too').not.toContain('lastSearch');
      expect(body).toContain('appStateE2e');
    }
    const stored = sealedStore()[APP];
    expect(Object.keys(stored)).toEqual([ledger.id]);
    expect(stored[ledger.id]).toMatch(/^[A-Za-z0-9_-]+$/);

    expect(await sealedStateGet(APP, ledger, 'lastSearch')).toBe(SECRET);
    expect(await sealedStateGet(APP, ledger, 'zoom')).toBe(125);
    expect(await sealedStateGet(APP, ledger, 'missing')).toBeUndefined();

    await sealedStateSet(APP, ledger, 'zoom', undefined);
    expect(await sealedStateGet(APP, ledger, 'zoom')).toBeUndefined();
  });

  it('never mixes with the app’s ordinary store', async () => {
    const ledger = await ledgerOf(await folderKey(), APP);
    appStateSet(APP, 'theme', 'ordinary');
    await sealedStateSet(APP, ledger, 'theme', 'sealed');
    expect(appStateGet(APP, 'theme')).toBe('ordinary');
    expect(await sealedStateGet(APP, ledger, 'theme')).toBe('sealed');
    expect(currentPrefs().appState ?? '').not.toContain('sealed');
    expect(currentPrefs().appStateE2e ?? '').not.toContain('ordinary');

    await sealedStateSet(APP, ledger, 'onlySealed', 1);
    expect(appStateGet(APP, 'onlySealed')).toBeUndefined();
    appStateSet(APP, 'onlyOrdinary', 2);
    expect(await sealedStateGet(APP, ledger, 'onlyOrdinary')).toBeUndefined();
  });

  it('one ledger per app and per folder: another’s, moved into its place, reads as empty', async () => {
    const fmk = await folderKey();
    const mine = await ledgerOf(fmk, APP);
    const otherApp = await ledgerOf(fmk, 'sketch');
    const otherFolder = await ledgerOf(await folderKey(), APP);
    expect(new Set([mine.id, otherApp.id, otherFolder.id]).size).toBe(3);
    expect(await ledgerOf(fmk, APP)).toMatchObject({ id: mine.id, account: 'u7' });

    await sealedStateSet('sketch', otherApp, 'k', SECRET);
    await sealedStateSet(APP, otherFolder, 'k', SECRET);
    expect(await sealedStateGet(APP, mine, 'k')).toBeUndefined();

    // The server puts another app's ledger where this one's is: it does not
    // open (the app and the id are sealed in), and the app starts empty.
    const store = sealedStore();
    savePref('appStateE2e', JSON.stringify({ ...store, [APP]: { [mine.id]: store.sketch[otherApp.id] } }));
    expect(await sealedStateGet(APP, mine, 'k')).toBeUndefined();
    savePref('appStateE2e', JSON.stringify({ [APP]: { [mine.id]: 'not-a-sealed-ledger' } }));
    expect(await sealedStateGet(APP, mine, 'k')).toBeUndefined();
  });
});

describe('the size rules hold for the sealed form', () => {
  it('a value over 8 KiB as JSON is refused, as in an ordinary session', async () => {
    const ledger = await ledgerOf(await folderKey(), APP);
    await expect(sealedStateSet(APP, ledger, 'k', 'x'.repeat(9 << 10))).rejects.toBeInstanceOf(AppStateTooLarge);
    await expect(sealedStateSet(APP, ledger, '', 1)).rejects.toBeInstanceOf(AppStateTooLarge);
    expect(currentPrefs().appStateE2e).toBeUndefined();
  });

  it('the app’s share is measured on what is stored - encryption and base64 included', async () => {
    takeServerRules({ limits: { app_state_max_bytes: 1000 } });
    const ledger = await ledgerOf(await folderKey(), APP);
    // 800 bytes of JSON fit the share in the clear; sealed, they do not.
    const value = 'y'.repeat(790);
    expect(JSON.stringify({ k: value }).length).toBeLessThan(1000);
    await expect(sealedStateSet(APP, ledger, 'k', value)).rejects.toBeInstanceOf(AppStateTooLarge);
    expect(currentPrefs().appStateE2e, 'nothing written').toBeUndefined();

    await sealedStateSet(APP, ledger, 'k', 'y'.repeat(500));
    expect(await sealedStateGet(APP, ledger, 'k')).toBe('y'.repeat(500));

    resetServerRules();
    await sealedStateSet(APP, ledger, 'k', value);
    expect(await sealedStateGet(APP, ledger, 'k'), 'no number: the server judges the save').toBe(value);
  });
});

describe('keys', () => {
  it('mid re-key: the previous key’s ledger is read, and the first write moves it', async () => {
    const before = await folderKey();
    const after = await folderKey();
    const old = await ledgerOf(before, APP);
    await sealedStateSet(APP, old, 'k', SECRET);

    const now = await ledgerOf(after, APP, before);
    expect(now.previous?.id).toBe(old.id);
    expect(await sealedStateGet(APP, now, 'k')).toBe(SECRET);

    await sealedStateSet(APP, now, 'other', 1);
    expect(Object.keys(sealedStore()[APP])).toEqual([now.id]);
    const fresh = await ledgerOf(after, APP);
    expect(await sealedStateGet(APP, fresh, 'k')).toBe(SECRET);
    expect(await sealedStateGet(APP, fresh, 'other')).toBe(1);
  });

  it('a vault’s folder key (an HKDF key) gives a ledger too', async () => {
    const raw = crypto.getRandomValues(new Uint8Array(32));
    const vaultFmk = await crypto.subtle.importKey('raw', raw, 'HKDF', false, ['deriveKey', 'deriveBits']);
    const ledger = await ledgerOf(vaultFmk, APP);
    await sealedStateSet(APP, ledger, 'k', SECRET);
    expect(await sealedStateGet(APP, ledger, 'k')).toBe(SECRET);
    expect(currentPrefs().appStateE2e ?? '').not.toContain('Gizli');
  });

  it('two writes at once both land', async () => {
    const ledger = await ledgerOf(await folderKey(), APP);
    await Promise.all([sealedStateSet(APP, ledger, 'a', 1), sealedStateSet(APP, ledger, 'b', 2), sealedStateSet(APP, ledger, 'c', 3)]);
    expect(await sealedStateGet(APP, ledger, 'a')).toBe(1);
    expect(await sealedStateGet(APP, ledger, 'b')).toBe(2);
    expect(await sealedStateGet(APP, ledger, 'c')).toBe(3);
  });
});

describe('one ledger per account', () => {
  it('a member’s ledger copied into another member’s preferences reads as empty there', async () => {
    const raw = crypto.getRandomValues(new Uint8Array(32));
    // Ada (account 7) keeps something while editing a file of the shared folder.
    const ada = await ledgerOf(await sharedFolderKey(raw), APP);
    await sealedStateSet(APP, ada, 'lastSearch', SECRET);
    const adaStore = sealedStore();

    // Grace (account 8) holds the same folder key in her own browser.
    signedInAs(8);
    const grace = await ledgerOf(await sharedFolderKey(raw), APP);
    expect(grace.account).toBe('u8');
    expect(grace.id, 'another account, another ledger').not.toBe(ada.id);

    // The server puts Ada's store into Grace's preferences as it is ...
    savePref('appStateE2e', JSON.stringify(adaStore));
    expect(await sealedStateGet(APP, grace, 'lastSearch')).toBeUndefined();
    // ... and under the id Grace's ledger is stored by: it does not open.
    savePref('appStateE2e', JSON.stringify({ [APP]: { [grace.id]: adaStore[APP][ada.id] } }));
    expect(await sealedStateGet(APP, grace, 'lastSearch')).toBeUndefined();

    // Grace's own ledger is hers, and Ada's still opens for Ada.
    await sealedStateSet(APP, grace, 'lastSearch', 'Grace');
    expect(await sealedStateGet(APP, grace, 'lastSearch')).toBe('Grace');
    signedInAs(7);
    savePref('appStateE2e', JSON.stringify(adaStore));
    expect(await sealedStateGet(APP, await ledgerOf(await sharedFolderKey(raw), APP), 'lastSearch')).toBe(SECRET);
  });

  it('nobody signed in: no sealed store - nothing to read, nothing written', async () => {
    signedInAs(null);
    expect(await appStateLedger(await folderKey(), APP)).toBeNull();
    expect(currentPrefs().appStateE2e).toBeUndefined();
  });
});
