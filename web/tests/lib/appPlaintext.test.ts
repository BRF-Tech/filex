// filex 0.56, task #189 - an app's interface editing a file of an encrypted
// folder (lib/appPlaintext, lib/appViewer `e2ePlaintext`).
//
// The CryptPad model: the browser decrypts the file for the app's frame and
// encrypts what the app saves; the server only ever carries ciphertext. These
// hold the two halves to it:
//
//  - the door decrypts the ciphertext the server's app door answered, with
//    the folder key the ring holds, and encrypts a save as a file of an
//    encrypted folder (`filexe2e`, the format the explorer's own upload
//    writes), which the same key opens again;
//  - a folder locked meanwhile hands over nothing more (the door asks the
//    ring at each call);
//  - one rule for which app is offered: an app granted files:e2e-plaintext,
//    where the host says it can hand the file over - never ONLYOFFICE, never
//    an app without the grant, never a vault's file or a `.fxe`.
//
// Red before 0.56: lib/appPlaintext did not exist and openHandlersFor gave an
// encrypted file filex's own viewer and nothing else.
import { afterEach, describe, expect, it } from 'vitest';

import {
  APP_PLAINTEXT_LIMIT,
  LedgerNeedsAccount,
  TooLargeToHand,
  VaultAppRefused,
  createAppPlaintextDoor,
  createAppVaultDoor,
  type PlaintextKeys,
  type VaultAppEngine,
  type VaultAppRead,
  type VaultAppSaved,
} from '@brftech/filex-core/src/lib/appPlaintext';
import {
  BUILTIN_VIEWER,
  E2E_PLAINTEXT_GRANT,
  openHandlersFor,
  pickOpenHandler,
  plaintextViewersFor,
  takesPlaintext,
  vaultPlaintextViewersFor,
} from '@brftech/filex-core/src/lib/appViewer';
import { decryptFile, encryptFile, hasMagic } from '@brftech/filex-core/src/lib/e2ecrypto';
import type { PluginViewRow } from '@brftech/filex-core/src/types/Plugins';
import { configurePrefs, resetPrefs } from '@brftech/filex-core/src/lib/prefs';

async function folderKey(): Promise<CryptoKey> {
  return crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt']);
}

function streamOf(b: ArrayBuffer | Uint8Array, piece = 7): ReadableStream<Uint8Array> {
  const all = b instanceof Uint8Array ? b : new Uint8Array(b);
  let at = 0;
  return new ReadableStream<Uint8Array>({
    pull(c) {
      if (at >= all.length) {
        c.close();
        return;
      }
      c.enqueue(all.slice(at, at + piece));
      at += piece;
    },
  });
}

const DOC = new TextEncoder().encode('PK\u0003\u0004 Merhaba dünya: ğüşıöç İĞÜŞÖÇ');

describe('the door (lib/appPlaintext)', () => {
  it('decrypts what the server holds with the folder key, piece by piece', async () => {
    const fmk = await folderKey();
    const ct = await encryptFile(fmk, DOC.slice().buffer);
    const door = createAppPlaintextDoor(() => ({ fmk, previous: null }));
    const plain = await door.open('docs://Kasa/rapor.docx', streamOf(ct));
    expect(plain).not.toBeNull();
    expect(Array.from(plain!)).toEqual(Array.from(DOC));
  });

  it('decrypts a file still under the previous key of a folder mid re-key', async () => {
    const old = await folderKey();
    const fmk = await folderKey();
    const ct = await encryptFile(old, DOC.slice().buffer);
    const door = createAppPlaintextDoor(() => ({ fmk, previous: old }));
    expect(Array.from((await door.open('docs://Kasa/rapor.docx', streamOf(ct)))!)).toEqual(Array.from(DOC));
  });

  it('encrypts a save as a file of an encrypted folder, which the folder key opens again', async () => {
    const fmk = await folderKey();
    const door = createAppPlaintextDoor(() => ({ fmk, previous: null }));
    const ct = await door.seal('docs://Kasa/rapor.docx', DOC.slice());
    expect(ct).not.toBeNull();
    expect(hasMagic(ct!)).toBe(true);
    expect(new Uint8Array(ct!)[8]).toBe(1);
    // Nothing of the plaintext travels in the clear.
    expect(new TextDecoder().decode(ct!)).not.toContain('Merhaba');
    expect(Array.from(new Uint8Array(await decryptFile(fmk, ct!)))).toEqual(Array.from(DOC));
  });

  it('hands over nothing once the folder is locked: it asks the ring at each call', async () => {
    const fmk = await folderKey();
    let keys: PlaintextKeys | null = { fmk, previous: null };
    const door = createAppPlaintextDoor(() => keys);
    const ct = await encryptFile(fmk, DOC.slice().buffer);
    keys = null;
    let cancelled = false;
    const s = new ReadableStream<Uint8Array>({
      pull(c) {
        c.enqueue(new Uint8Array(ct));
      },
      cancel() {
        cancelled = true;
      },
    });
    expect(await door.open('docs://Kasa/rapor.docx', s)).toBeNull();
    expect(cancelled).toBe(true);
    expect(await door.seal('docs://Kasa/rapor.docx', DOC.slice())).toBeNull();
  });

  it('a wrong key is an error, never ciphertext handed over as the file', async () => {
    const fmk = await folderKey();
    const other = await folderKey();
    const ct = await encryptFile(fmk, DOC.slice().buffer);
    const door = createAppPlaintextDoor(() => ({ fmk: other, previous: null }));
    await expect(door.open('docs://Kasa/rapor.docx', streamOf(ct))).rejects.toThrow();
  });

  it('holds what it hands over to a limit, both ways', async () => {
    const fmk = await folderKey();
    const door = createAppPlaintextDoor(() => ({ fmk, previous: null }));
    const ct = await encryptFile(fmk, DOC.slice().buffer);
    await expect(door.open('docs://Kasa/rapor.docx', streamOf(ct), 8)).rejects.toBeInstanceOf(TooLargeToHand);
    await expect(door.seal('docs://Kasa/rapor.docx', DOC.slice(), 8)).rejects.toBeInstanceOf(TooLargeToHand);
    expect(APP_PLAINTEXT_LIMIT).toBe(200 << 20);
  });
});

// ── which app is offered (lib/appViewer) ──────────────────────────────────

const view = (plugin: string, grants: string[]): PluginViewRow =>
  ({
    plugin,
    id: 'editor',
    placement: 'viewer',
    label: { en: plugin },
    applies: { kind: 'file', ext: ['docx'] },
    ui: { url: `/_appui/${plugin}/0123456789abcdef/index.html`, grants, engine: false, version: '1' },
  }) as unknown as PluginViewRow;

const VIEWS = [view('office-editor', ['files:read', 'files:write', 'ui', E2E_PLAINTEXT_GRANT]), view('peek', ['files:read', 'ui'])];
const encDocx = { type: 'file', basename: 'rapor.docx', extension: 'docx', encrypted: 'folder' };
const ids = (hs: { id: string }[]) => hs.map((h) => h.id);

describe('which app opens an encrypted file (lib/appViewer e2ePlaintext)', () => {
  it('the grant', () => {
    expect(E2E_PLAINTEXT_GRANT).toBe('files:e2e-plaintext');
    expect(takesPlaintext(['files:read', 'files:e2e-plaintext'])).toBe(true);
    expect(takesPlaintext(['files:read'])).toBe(false);
    expect(takesPlaintext(undefined)).toBe(false);
  });

  it('only filex’s own viewer while the host cannot hand it over (0.55 unchanged)', () => {
    expect(ids(openHandlersFor(VIEWS, encDocx, null, { onlyOffice: true }).on)).toEqual([BUILTIN_VIEWER]);
    expect(ids(openHandlersFor(VIEWS, encDocx, null, { inEncrypted: true, e2ePlaintext: false }).on)).toEqual([BUILTIN_VIEWER]);
  });

  it('the app granted files:e2e-plaintext beside filex’s own viewer - never one without the grant, never ONLYOFFICE', () => {
    const on = ids(openHandlersFor(VIEWS, encDocx, null, { onlyOffice: true, inEncrypted: true, e2ePlaintext: true }).on);
    expect(on).toEqual(['app:office-editor/editor', BUILTIN_VIEWER]);
    expect(pickOpenHandler(VIEWS, encDocx, null, null, null, { e2ePlaintext: true })?.view?.plugin).toBe('office-editor');
    expect(plaintextViewersFor(VIEWS, encDocx, { e2ePlaintext: true }).map((v) => v.plugin)).toEqual(['office-editor']);
  });

  it('the administrator’s order and switches apply as to any file', () => {
    const rules = { docx: { order: [BUILTIN_VIEWER], off: [] } };
    expect(ids(openHandlersFor(VIEWS, encDocx, rules, { e2ePlaintext: true }).on)).toEqual([BUILTIN_VIEWER, 'app:office-editor/editor']);
    const off = { docx: { order: [], off: ['app:office-editor/editor'] } };
    expect(ids(openHandlersFor(VIEWS, encDocx, off, { e2ePlaintext: true }).on)).toEqual([BUILTIN_VIEWER]);
  });

  it('never a single encrypted file, whatever the host says', () => {
    const n = { ...encDocx, encrypted: 'file' };
    expect(ids(openHandlersFor(VIEWS, n, null, { e2ePlaintext: true }).on)).toEqual([BUILTIN_VIEWER]);
  });

  // filex 0.56: a vault's file is no longer "never" - it is offered its own
  // way (vaultPlaintextViewersFor, below); the encrypted folders' rule still
  // never takes one.
  it('the encrypted folders’ rule never takes a vault’s file', () => {
    expect(plaintextViewersFor(VIEWS, { ...encDocx, encrypted: 'vault' }, { e2ePlaintext: true })).toEqual([]);
  });
});

// ── a vault's file (filex 0.56) ───────────────────────────────────────────
//
// A vault keeps no file the server could name: its tree is an encrypted
// index, which only the browser that unlocked the vault reads. So the kind
// rule - which app opens which file - is applied in the browser, to the name
// the tab read (vaultPlaintextViewersFor), and the file goes to the app by
// the vault's own door (createAppVaultDoor over the vault engine), never the
// server's. Red before 0.56: neither existed, and openHandlersFor gave a
// vault's file filex's own viewer only.

const vaultDocx = {
  type: 'file',
  path: 'docs://Kasa/Bütçe/plan 2027.docx',
  basename: 'plan 2027.docx',
  extension: 'docx',
  mime_type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  encrypted: 'vault',
  vault_root: 'docs://Kasa',
};

describe('which app opens a vault’s file (lib/appViewer vaultPlaintextViewersFor)', () => {
  it('the granted app, by the kind the tab read from the vault’s index, beside filex’s own viewer - never ONLYOFFICE', () => {
    const opts = { onlyOffice: true, inEncrypted: true, e2ePlaintext: true };
    expect(ids(openHandlersFor(VIEWS, vaultDocx, null, opts).on)).toEqual(['app:office-editor/editor', BUILTIN_VIEWER]);
    expect(vaultPlaintextViewersFor(VIEWS, vaultDocx, opts).map((v) => v.plugin)).toEqual(['office-editor']);
    expect(pickOpenHandler(VIEWS, vaultDocx, null, null, null, opts)?.view?.plugin).toBe('office-editor');
  });

  it('a kind no granted app opens: filex’s own viewer', () => {
    const txt = { ...vaultDocx, path: 'docs://Kasa/not.txt', basename: 'not.txt', extension: 'txt', mime_type: 'text/plain' };
    expect(ids(openHandlersFor(VIEWS, txt, null, { inEncrypted: true, e2ePlaintext: true }).on)).toEqual([BUILTIN_VIEWER]);
    expect(vaultPlaintextViewersFor(VIEWS, txt, { e2ePlaintext: true })).toEqual([]);
  });

  it('only where the host can hand it over: the vault open in this tab, the server’s switch on', () => {
    expect(ids(openHandlersFor(VIEWS, vaultDocx, null, { inEncrypted: true, e2ePlaintext: false }).on)).toEqual([BUILTIN_VIEWER]);
    expect(vaultPlaintextViewersFor(VIEWS, vaultDocx, null)).toEqual([]);
  });

  it('only a vault’s file: a file of an encrypted folder is the other rule’s', () => {
    expect(vaultPlaintextViewersFor(VIEWS, encDocx, { e2ePlaintext: true })).toEqual([]);
    expect(vaultPlaintextViewersFor(VIEWS, { ...vaultDocx, type: 'dir' }, { e2ePlaintext: true })).toEqual([]);
  });

  it('the administrator’s Default apps rule applies as to any file', () => {
    const off = { docx: { order: [], off: ['app:office-editor/editor'] } };
    expect(ids(openHandlersFor(VIEWS, vaultDocx, off, { inEncrypted: true, e2ePlaintext: true }).on)).toEqual([BUILTIN_VIEWER]);
    const first = { docx: { order: [BUILTIN_VIEWER], off: [] } };
    expect(ids(openHandlersFor(VIEWS, vaultDocx, first, { inEncrypted: true, e2ePlaintext: true }).on)).toEqual([
      BUILTIN_VIEWER,
      'app:office-editor/editor',
    ]);
  });
});

/** A vault engine in memory: what the door asks it, and what it answers. */
function fakeVaultEngine(answer: { read?: VaultAppRead; save?: VaultAppSaved; key?: CryptoKey | null }) {
  const asked: Array<{ op: string; path: string; limit?: number; bytes?: Uint8Array; expect?: string }> = [];
  const engine: VaultAppEngine = {
    async readForApp(path, limit) {
      asked.push({ op: 'read', path, limit });
      return answer.read ?? { ok: false, why: 'failed' };
    },
    async saveForApp(path, bytes, expect) {
      asked.push({ op: 'save', path, bytes: bytes.slice(), expect });
      return answer.save ?? { ok: false, why: 'failed' };
    },
    keyForApp: () => answer.key ?? null,
  };
  return { engine, asked };
}

describe('the vault door (lib/appPlaintext createAppVaultDoor)', () => {
  const PATH = 'docs://Kasa/Bütçe/plan 2027.docx';

  it('hands over the bytes the vault decrypted and the version read', async () => {
    const { engine, asked } = fakeVaultEngine({ read: { ok: true, bytes: DOC.slice(), sig: '42:1760000000000:ab' } });
    const r = await createAppVaultDoor(engine).read(PATH);
    expect(asked).toEqual([{ op: 'read', path: PATH, limit: APP_PLAINTEXT_LIMIT }]);
    expect(r?.sig).toBe('42:1760000000000:ab');
    expect(Array.from(r!.bytes)).toEqual(Array.from(DOC));
  });

  it('hands over bytes whose buffer is exactly them (a transfer moves the whole buffer), and leaves no copy behind', async () => {
    const big = new Uint8Array(DOC.length + 16);
    big.set(DOC, 8);
    const view = big.subarray(8, 8 + DOC.length);
    const { engine } = fakeVaultEngine({ read: { ok: true, bytes: view, sig: 's' } });
    const r = await createAppVaultDoor(engine).read(PATH);
    expect(r!.bytes.byteOffset).toBe(0);
    expect(r!.bytes.buffer.byteLength).toBe(DOC.length);
    expect(Array.from(r!.bytes)).toEqual(Array.from(DOC));
    expect(Array.from(big).every((b) => b === 0), 'the larger buffer it came in is zeroed').toBe(true);
  });

  it('a vault locked meanwhile hands over nothing; past the limit is TooLargeToHand; the rest is the vault’s refusal', async () => {
    expect(await createAppVaultDoor(fakeVaultEngine({ read: { ok: false, why: 'locked' } }).engine).read(PATH)).toBeNull();
    const big = fakeVaultEngine({ read: { ok: false, why: 'too_large' } });
    await expect(createAppVaultDoor(big.engine).read(PATH, 8)).rejects.toBeInstanceOf(TooLargeToHand);
    expect(big.asked[0].limit, 'the limit is asked of the vault: nothing larger is fetched').toBe(8);
    for (const why of ['gone', 'failed'] as const) {
      const p = createAppVaultDoor(fakeVaultEngine({ read: { ok: false, why } }).engine).read(PATH);
      await expect(p).rejects.toBeInstanceOf(VaultAppRefused);
      await expect(p).rejects.toMatchObject({ why });
    }
  });

  it('a save goes to the vault with the version it is written over, and answers the new one', async () => {
    const { engine, asked } = fakeVaultEngine({ save: { ok: true, size: 5, sig: 'next' } });
    const r = await createAppVaultDoor(engine).save(PATH, new TextEncoder().encode('yeni!'), 'read-sig');
    expect(r).toEqual({ size: 5, sig: 'next' });
    expect(asked).toHaveLength(1);
    expect(asked[0]).toMatchObject({ op: 'save', path: PATH, expect: 'read-sig' });
    expect(new TextDecoder().decode(asked[0].bytes)).toBe('yeni!');
  });

  it('a save the vault refused is its refusal; one past the limit never reaches the vault', async () => {
    for (const why of ['held', 'lock_lost', 'changed', 'gone', 'read_only', 'locked'] as const) {
      const p = createAppVaultDoor(fakeVaultEngine({ save: { ok: false, why } }).engine).save(PATH, DOC.slice(), 's');
      await expect(p, why).rejects.toMatchObject({ name: 'VaultAppRefused', why });
    }
    const { engine, asked } = fakeVaultEngine({ save: { ok: true, size: 1, sig: 'x' } });
    await expect(createAppVaultDoor(engine).save(PATH, DOC.slice(), 's', 8)).rejects.toBeInstanceOf(TooLargeToHand);
    expect(asked).toEqual([]);
  });

  it('the host’s door carries the vault half when it opens vaults, and says none otherwise', () => {
    const vaultDoor = createAppVaultDoor(fakeVaultEngine({}).engine);
    expect(createAppPlaintextDoor(() => null, vaultDoor).vault).toBe(vaultDoor);
    expect(createAppPlaintextDoor(() => null).vault).toBeNull();
  });
});

// The app's sealed store for a vault (filex 0.56): the vault door's ledger
// comes from the vault's own key (an HKDF key) and the account; no vault open
// is no ledger, no account known is LedgerNeedsAccount - never the locked
// answer. Red before: the vault door had no ledger.
describe('the vault door’s sealed store (createAppVaultDoor ledger)', () => {
  const PATH = 'docs://Kasa/Bütçe/plan 2027.docx';
  const hkdf = () => crypto.subtle.importKey('raw', crypto.getRandomValues(new Uint8Array(32)), 'HKDF', false, ['deriveKey', 'deriveBits']);

  it('a ledger from the vault’s key while it is open; none once it is not', async () => {
    // No account document kept (prefs not configured): the "local" account.
    const key = await hkdf();
    const open = createAppVaultDoor(fakeVaultEngine({ key }).engine);
    const ledger = await open.ledger(PATH, 'office-editor');
    expect(ledger?.id).toMatch(/^[A-Za-z0-9_-]+$/);
    expect(ledger?.previous ?? null, 'a vault has no re-key').toBeNull();
    expect(await createAppVaultDoor(fakeVaultEngine({ key: null }).engine).ledger(PATH, 'office-editor')).toBeNull();
  });
});

describe('a door whose page knows no account (LedgerNeedsAccount)', () => {
  const PATH_FOLDER = 'docs://Kasa/rapor.docx';
  const PATH_VAULT = 'docs://Kasa/Bütçe/plan 2027.docx';
  afterEach(() => resetPrefs());

  it('the folder door and the vault door both say so, not "locked"', async () => {
    configurePrefs({ surface: 'web', fetchImpl: (async () => new Response('{}', { status: 401 })) as unknown as typeof fetch });
    const fmk = await folderKey();
    await expect(createAppPlaintextDoor(() => ({ fmk, previous: null })).ledger(PATH_FOLDER, 'office-editor')).rejects.toBeInstanceOf(LedgerNeedsAccount);
    const key = await crypto.subtle.importKey('raw', crypto.getRandomValues(new Uint8Array(32)), 'HKDF', false, ['deriveKey', 'deriveBits']);
    await expect(createAppVaultDoor(fakeVaultEngine({ key }).engine).ledger(PATH_VAULT, 'office-editor')).rejects.toBeInstanceOf(LedgerNeedsAccount);
    // A locked folder is still null, as before.
    expect(await createAppPlaintextDoor(() => null).ledger(PATH_FOLDER, 'office-editor')).toBeNull();
  });
});
