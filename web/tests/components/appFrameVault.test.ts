// filex 0.56, task #189 - an app's interface editing a file of a VAULT
// (AppFrame `e2e` with the door's vault half: lib/appPlaintext
// createAppVaultDoor over the explorer's vault engine).
//
// A vault keeps no file the server could hand an app: its tree is an
// encrypted index and its contents are packs. So the frame never asks the
// server's app doors for such a file - the vault engine reads it from its
// packs in this tab and writes the app's save as the vault's next
// generation. These hold the frame to it:
//
//  - the interface is told the file is handed over in the clear
//    (`encrypted: 'vault'`, `plaintext`) and may be saved, only with the
//    door's vault half AND the grant files:e2e-plaintext;
//  - `file.read` goes to the vault, never to the server (no app door, no
//    preview), and the plaintext is transferred to the interface;
//  - `file.save` goes to the vault with the version the interface read (or
//    the row's, unread), then with the one it wrote; the bytes it handed
//    over do not stay behind in this page;
//  - the vault's refusals reach the interface as protocol codes: somebody
//    else writing (`failed` / `vault_locked`), the write lock lost
//    (`failed` / `vault_lock_lost`), changed meanwhile (`failed` /
//    `changed`), gone (`not_found`), read-only, the vault locked
//    (`unavailable`);
//  - while the frame holds the file, nothing else leaves toward the server.
//
// Red before 0.56: AppFrame told a vault's file read-only, refused its save
// and sent its read to the server's app door, which refuses a vault.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import AppFrame from '@brftech/filex-core/src/components/plugin/AppFrame.vue';
import {
  createAppPlaintextDoor,
  createAppVaultDoor,
  type VaultAppEngine,
  type VaultAppRead,
  type VaultAppSaved,
} from '@brftech/filex-core/src/lib/appPlaintext';
import { HELLO } from '../../../packages/app-ui/src/protocol';
import { appStateGet } from '@brftech/filex-core/src/lib/appState';
import { configurePrefs, currentPrefs, flushPrefs } from '@brftech/filex-core/src/lib/prefs';
import { wordsIn } from '@brftech/filex-core/src/lib/errorWords';
import { teardownDom } from '../helpers/teardown';
import { answerAccountPrefs } from '../helpers/accountPrefs';

answerAccountPrefs();

afterEach(async () => {
  await teardownDom();
  vi.restoreAllMocks();
});

const settle = async () => {
  for (let i = 0; i < 6; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};

function fakeWindow() {
  const sent: Array<{ data: unknown; ports: MessagePort[] }> = [];
  return {
    sent,
    postMessage(data: unknown, _origin: string, transfer?: Transferable[]) {
      sent.push({ data, ports: (transfer ?? []) as MessagePort[] });
    },
  };
}

function ask(port: MessagePort, id: number, method: string, params?: unknown, transfer: Transferable[] = []): Promise<any> {
  return new Promise((resolve) => {
    const prev = port.onmessage;
    port.onmessage = (ev) => {
      if (ev.data?.id === id) {
        port.onmessage = prev;
        resolve(ev.data);
      }
    };
    port.postMessage({ id, method, params }, transfer);
  });
}

const GRANTED = ['files:read', 'files:write', 'ui', 'ui-viewer:.docx', 'files:e2e-plaintext'];
/** The file's wire path: the vault folder, then a path that exists only in this tab. */
const PATH = 'docs://Kasa/Bütçe/plan 2027.docx';
const NAME = 'plan 2027.docx';
const DOC = 'Merhaba dünya: ğüşıöç İĞÜŞÖÇ';
const READ_SIG = '40:1760000000000:0123456789abcdef0123456789abcdef';

interface SetupOptions {
  grants?: string[];
  /** false: a host whose door has no vault half (an older embed). */
  vault?: boolean;
  read?: VaultAppRead;
  save?: VaultAppSaved;
  engine?: boolean;
  /** The vault's key as the engine holds it while the vault is open; null:
   *  not open any more. Default: a key. */
  vaultKey?: () => CryptoKey | null;
}

/** A vault's folder key: an HKDF key (lib/e2evault). */
async function vaultKey(): Promise<CryptoKey> {
  return crypto.subtle.importKey('raw', crypto.getRandomValues(new Uint8Array(32)), 'HKDF', false, ['deriveKey', 'deriveBits']);
}

async function setup(opts: SetupOptions = {}) {
  const key = await vaultKey();
  const reads: string[] = [];
  const saves: Array<{ path: string; bytes: Uint8Array; seen: string; expect?: string }> = [];
  const engine: VaultAppEngine = {
    async readForApp(path) {
      reads.push(path);
      return opts.read ?? { ok: true, bytes: new TextEncoder().encode(DOC), sig: READ_SIG };
    },
    async saveForApp(path, bytes, expect) {
      saves.push({ path, bytes, seen: new TextDecoder().decode(bytes), expect });
      return opts.save ?? { ok: true, size: bytes.byteLength, sig: `written-${saves.length}` };
    },
    keyForApp: () => (opts.vaultKey ? opts.vaultKey() : key),
  };
  const door = createAppPlaintextDoor(() => null, opts.vault === false ? null : createAppVaultDoor(engine));
  const api = {
    appUIUrl: (u: string) => `about:blank#${u}`,
    pluginUIRead: vi.fn(async () => new Response('the server: never asked for a vault’s file')),
    pluginUISave: vi.fn(async () => ({ saved: true, path: PATH, name: NAME, size: 1 })),
    pluginUICall: vi.fn(async () => ({ result: 1 })),
    pluginActionRun: vi.fn(async () => ({ op: { id: 1 } })),
    fetchResponse: vi.fn(async () => new Response('the preview: never for a vault’s file')),
    index: vi.fn(async () => ({ storages: ['docs'] })),
  };
  const wrapper = mount(AppFrame, {
    props: {
      api: api as never,
      app: 'office-editor',
      view: 'editor',
      placement: 'viewer',
      ui: { url: '/_appui/office-editor/0123456789abcdef/index.html', grants: opts.grants ?? GRANTED, engine: !!opts.engine, version: '0.2.0' },
      locale: 'tr',
      files: [{ path: PATH, name: NAME, size: 40, modified: 1760000000000, encrypted: 'vault' }],
      e2e: door,
    },
    attachTo: document.body,
  });
  await settle();
  const f = document.querySelector('iframe') as HTMLIFrameElement;
  const win = fakeWindow();
  Object.defineProperty(f, 'contentWindow', { get: () => win });
  window.dispatchEvent(new MessageEvent('message', { data: { type: HELLO, v: 1 }, source: win as unknown as Window, origin: 'null' }));
  const port = win.sent[0].ports[0];
  return { port, api, reads, saves, wrapper };
}

/** Nothing of the file went to the server: no app door, no preview, no listing. */
function serverUntouched(api: Awaited<ReturnType<typeof setup>>['api']) {
  expect(api.pluginUIRead).not.toHaveBeenCalled();
  expect(api.pluginUISave).not.toHaveBeenCalled();
  expect(api.fetchResponse).not.toHaveBeenCalled();
  expect(api.pluginUICall).not.toHaveBeenCalled();
  expect(api.pluginActionRun).not.toHaveBeenCalled();
  expect(api.index).not.toHaveBeenCalled();
}

describe('a file of a vault, handed to a granted app in the clear', () => {
  it('tells the interface so, and that it may save it - never the path', async () => {
    const { port } = await setup();
    const s = await ask(port, 1, 'session.get');
    expect(s.result.files).toEqual([
      { index: 0, name: NAME, ext: 'docx', size: 40, mime: '', readOnly: false, encrypted: 'vault', plaintext: true },
    ]);
    expect(JSON.stringify(s.result)).not.toContain('docs://');
    expect(JSON.stringify(s.result)).not.toContain('Bütçe');
  });

  it('reads from the vault, in this page: the server is never asked, and the plaintext is transferred', async () => {
    const { port, api, reads } = await setup();
    const r = await ask(port, 1, 'file.read', { as: 'bytes' });
    expect(r.error).toBeUndefined();
    expect(reads).toEqual([PATH]);
    expect(r.result.bytes).toBeInstanceOf(ArrayBuffer);
    expect(new TextDecoder().decode(r.result.bytes)).toBe(DOC);
    expect(r.result.size).toBe(new TextEncoder().encode(DOC).byteLength);
    const t = await ask(port, 2, 'file.read', { as: 'text' });
    expect(t.result.text).toBe(DOC);
    serverUntouched(api);
  });

  it('saves to the vault over the version it read, then over the one it wrote; nothing stays behind', async () => {
    const { port, api, saves, wrapper } = await setup();
    await ask(port, 1, 'file.read', { as: 'text' });
    const data = new TextEncoder().encode(`${DOC} - kaydedildi`).buffer;
    const r = await ask(port, 2, 'file.save', { data }, [data]);
    expect(r.error).toBeUndefined();
    expect(r.result).toEqual({ saved: true, size: new TextEncoder().encode(`${DOC} - kaydedildi`).byteLength });
    expect(saves).toHaveLength(1);
    expect(saves[0]).toMatchObject({ path: PATH, seen: `${DOC} - kaydedildi`, expect: READ_SIG });
    // The page's copy of what the interface handed over is zeroed once saved.
    expect(Array.from(saves[0].bytes).every((b) => b === 0)).toBe(true);
    expect(wrapper.emitted('saved')?.[0]?.[0]).toEqual({ path: PATH, size: new TextEncoder().encode(`${DOC} - kaydedildi`).byteLength, index: 0 });

    const again = await ask(port, 3, 'file.save', { data: 'ikinci' });
    expect(again.error).toBeUndefined();
    expect(saves[1].expect, 'the next save names the version the last one wrote').toBe('written-1');
    serverUntouched(api);
  });

  it('a save of a file it did not read names the version its row said', async () => {
    const { port, saves } = await setup();
    const r = await ask(port, 1, 'file.save', { data: 'okumadan' });
    expect(r.error).toBeUndefined();
    expect(saves[0].expect).toBe('40:1760000000000');
  });

  it('the vault’s refusals reach the interface as protocol codes, and nothing counts as saved', async () => {
    const cases: Array<[VaultAppSaved & { ok: false }, string, string]> = [
      [{ ok: false, why: 'held' }, 'failed', 'vault_locked'],
      [{ ok: false, why: 'lock_lost' }, 'failed', 'vault_lock_lost'],
      [{ ok: false, why: 'changed' }, 'failed', 'changed'],
      [{ ok: false, why: 'gone' }, 'not_found', ''],
      [{ ok: false, why: 'read_only' }, 'read_only', ''],
      [{ ok: false, why: 'locked' }, 'unavailable', ''],
    ];
    for (const [save, code, message] of cases) {
      const { port, api, wrapper } = await setup({ save });
      const r = await ask(port, 1, 'file.save', { data: 'v2' });
      expect(r.error?.code, save.why).toBe(code);
      if (message) expect(r.error?.message, save.why).toBe(message);
      expect(wrapper.emitted('saved'), save.why).toBeUndefined();
      serverUntouched(api);
      await teardownDom();
    }
  });

  it('a vault locked meanwhile hands over nothing', async () => {
    const { port, api } = await setup({ read: { ok: false, why: 'locked' } });
    expect((await ask(port, 1, 'file.read', { as: 'bytes' })).error.code).toBe('unavailable');
    serverUntouched(api);
  });

  it('lets nothing else leave toward the server while it holds the vault’s file', async () => {
    const { port, api } = await setup({ engine: true });
    expect((await ask(port, 1, 'file.saveAs', { name: 'kopya.docx', data: 'x' })).error.code).toBe('unavailable');
    expect((await ask(port, 2, 'engine.call', { method: 'm' })).error.code).toBe('unavailable');
    expect((await ask(port, 3, 'job.submit', { action: 'a' })).error.code).toBe('unavailable');
    serverUntouched(api);
  });
});

describe('a vault is one person’s at a time', () => {
  it('its file is never edited together: the join is not even asked of the server', async () => {
    const { port, api } = await setup({ grants: [...GRANTED, 'files:co-edit'] });
    const join = vi.fn(async () => ({}));
    Object.assign(api, { coeditJoin: join, coeditCall: vi.fn(async () => ({})) });
    const r = await ask(port, 1, 'coedit.join', { index: 0 });
    expect(r.error).toMatchObject({ code: 'unavailable', message: 'encrypted' });
    expect(join).not.toHaveBeenCalled();
    serverUntouched(api);
  });
});

describe('without the vault half or the grant: 0.55’s answer', () => {
  it('a host whose door has no vault half: read-only, no plaintext, the vault never asked', async () => {
    const { port, reads, saves } = await setup({ vault: false });
    const s = await ask(port, 1, 'session.get');
    expect(s.result.files[0]).toMatchObject({ encrypted: 'vault', readOnly: true });
    expect(s.result.files[0].plaintext).toBeUndefined();
    expect((await ask(port, 2, 'file.save', { data: 'v2' })).error.code).toBe('read_only');
    expect(reads).toEqual([]);
    expect(saves).toEqual([]);
  });

  it('an app without files:e2e-plaintext: the same', async () => {
    const { port, saves } = await setup({ grants: ['files:read', 'files:write', 'ui'] });
    const s = await ask(port, 1, 'session.get');
    expect(s.result.files[0].plaintext).toBeUndefined();
    expect(s.result.files[0].readOnly).toBe(true);
    expect((await ask(port, 2, 'file.save', { data: 'v2' })).error.code).toBe('read_only');
    expect(saves).toEqual([]);
  });
});

// The app's own small store while it holds a vault's file (filex 0.56): the
// SEALED store (lib/appStateE2e), its keys derived from the vault's own key
// through the door's vault half, bound to the account the page is signed in
// as - never the app's ordinary store. Red before: the vault half had no
// ledger and the frame asked the folder door, which knows no vault.
describe('the app’s own store, while it holds a vault’s file', () => {
  /** The account document's transport: who is signed in, and every PUT. */
  function account(me: number | null) {
    const sent: string[] = [];
    const fetchImpl = (async (url: unknown, init?: RequestInit) => {
      if (String(url).endsWith('/api/auth/me')) {
        return me === null ? new Response('{"error":"unauthorized"}', { status: 401 }) : new Response(JSON.stringify({ user: { id: me } }), { status: 200 });
      }
      if (init?.method === 'PUT') sent.push(String(init.body ?? ''));
      return new Response('{}', { status: 200 });
    }) as unknown as typeof fetch;
    configurePrefs({ surface: 'web', fetchImpl });
    return sent;
  }

  it('is sealed with the vault’s key on its way to the server, opens again here, and leaves the ordinary store alone', async () => {
    const sent = account(7);
    const { port, api } = await setup();
    expect((await ask(port, 1, 'state.set', { key: 'lastSearch', value: DOC })).error).toBeUndefined();
    expect((await ask(port, 2, 'state.get', { key: 'lastSearch' })).result).toBe(DOC);
    await flushPrefs();
    expect(sent.length).toBeGreaterThan(0);
    for (const body of sent) {
      expect(body).not.toContain('Merhaba');
      expect(body).not.toContain('lastSearch');
    }
    expect(currentPrefs().appStateE2e).toBeTruthy();
    expect(appStateGet('office-editor', 'lastSearch'), 'the ordinary store is not written').toBeUndefined();
    serverUntouched(api);
  });

  it('the vault no longer open: an empty store to read, nothing written', async () => {
    account(7);
    let open = true;
    const key = await vaultKey();
    const { port } = await setup({ vaultKey: () => (open ? key : null) });
    await ask(port, 1, 'state.set', { key: 'k', value: 'v' });
    open = false;
    const got = await ask(port, 2, 'state.get', { key: 'k' });
    expect(got.error).toBeUndefined();
    expect(got.result).toBeNull();
    const before = currentPrefs().appStateE2e;
    expect((await ask(port, 3, 'state.set', { key: 'k', value: 'w' })).error.code).toBe('unavailable');
    expect(currentPrefs().appStateE2e).toBe(before);
  });

  it('no account known (an embed without /api/auth/me): refused in words that say so, not as a locked vault', async () => {
    const sent = account(null);
    const { port } = await setup();
    const r = await ask(port, 1, 'state.set', { key: 'k', value: 'v' });
    expect(r.error).toEqual({ code: 'unavailable', message: wordsIn('tr')('appframe.state_no_account') });
    const got = await ask(port, 2, 'state.get', { key: 'k' });
    expect(got.error).toBeUndefined();
    expect(got.result).toBeNull();
    await flushPrefs();
    expect(sent.join('')).not.toContain('appStateE2e');
  });
});
