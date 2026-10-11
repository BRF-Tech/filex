// filex 0.56, task #189 - an app's interface holding a file of an encrypted
// folder in the clear, as AppFrame hands it over:
//
//  - `state.set` / `state.get` use the app's ENCRYPTED store for that folder
//    (lib/appStateE2e, keys from the door): what reaches the server's account
//    document is sealed, and reads back opened; the app's ordinary store
//    (lib/appState) is neither read nor written - an ordinary session of the
//    same app does not see the encrypted session's settings, nor the other
//    way round;
//  - a folder locked meanwhile: `state.get` answers an empty store (no
//    error), `state.set` answers `unavailable` and nothing is written;
//  - a folder whose NAMES are encrypted (level 2): the file opens by its
//    decrypted name, and no request this page sends - the app door's read
//    and save, the account document - carries the name or its extension.
//
// Red before 0.56: `state.set` wrote the app's ordinary store, in the clear,
// whatever the frame held.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import AppFrame from '@brftech/filex-core/src/components/plugin/AppFrame.vue';
import { createAppPlaintextDoor, type PlaintextKeys } from '@brftech/filex-core/src/lib/appPlaintext';
import { encryptFile } from '@brftech/filex-core/src/lib/e2ecrypto';
import { appStateGet, appStateSet } from '@brftech/filex-core/src/lib/appState';
import { configurePrefs, currentPrefs, flushPrefs, resetPrefs } from '@brftech/filex-core/src/lib/prefs';
import { HELLO } from '../../../packages/app-ui/src/protocol';
import { wordsIn } from '@brftech/filex-core/src/lib/errorWords';
import { teardownDom } from '../helpers/teardown';

/** Every PUT of the account document, as the server would receive it. */
let prefsSent: string[] = [];

beforeEach(() => {
  prefsSent = [];
  const fetchImpl = vi.fn(async (url: unknown, init?: RequestInit) => {
    // The session's account, which the sealed store is bound to.
    if (String(url).endsWith('/api/auth/me')) return new Response(JSON.stringify({ user: { id: 7 } }), { status: 200 });
    if (init?.method === 'PUT') prefsSent.push(String(init.body ?? ''));
    return new Response('{}', { status: 200 });
  });
  configurePrefs({ surface: 'web', fetchImpl: fetchImpl as unknown as typeof fetch });
});

afterEach(async () => {
  await teardownDom();
  resetPrefs();
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

const APP = 'office-editor';
const GRANTED = ['files:read', 'files:write', 'ui', 'ui-viewer:.docx', 'files:e2e-plaintext'];
/** A level-2 folder: the stored name is ciphertext, the name is the browser's. */
const STORED = 'docs://Kasa/ugkmCcOceKUyA7tsdZxzltk';
const NAME = 'Maaş bordrosu.docx';
const DOC = 'Gizli bordro: ğüşıöç İĞÜŞÖÇ';

async function setup(opts: { locked?: () => boolean; plaintext?: boolean } = {}) {
  const fmk = await crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt']);
  const ct = await encryptFile(fmk, new TextEncoder().encode(DOC).buffer);
  const keys = (): PlaintextKeys | null => (opts.locked?.() ? null : { fmk, previous: null });
  const door = createAppPlaintextDoor(() => keys());
  const calls: unknown[][] = [];
  const pluginUIRead = vi.fn(async (...a: unknown[]) => {
    calls.push(a);
    return new Response(ct.slice(0), { headers: { 'content-length': String(ct.byteLength) } });
  });
  const pluginUISave = vi.fn(async (...a: unknown[]) => {
    calls.push(a);
    const body = a[3] as Blob;
    return { saved: true, path: STORED, name: 'ugkmCcOceKUyA7tsdZxzltk', size: body.size, modified: 1760000000999 };
  });
  const api = {
    appUIUrl: (u: string) => `about:blank#${u}`,
    pluginUIRead,
    pluginUISave,
    pluginUICall: vi.fn(async () => ({ result: 1 })),
    pluginActionRun: vi.fn(async () => ({ op: { id: 1 } })),
    fetchResponse: vi.fn(async () => new Response('never')),
  };
  mount(AppFrame, {
    props: {
      api: api as never,
      app: APP,
      view: 'editor',
      placement: 'viewer',
      ui: { url: `/_appui/${APP}/0123456789abcdef/index.html`, grants: GRANTED, engine: false, version: '0.2.0' },
      locale: 'tr',
      files: [{ path: STORED, name: NAME, size: ct.byteLength, modified: 1760000000000, ...(opts.plaintext === false ? {} : { encrypted: 'folder' as const }) }],
      e2e: opts.plaintext === false ? null : door,
    },
    attachTo: document.body,
  });
  await settle();
  const f = document.querySelector('iframe') as HTMLIFrameElement;
  const win = fakeWindow();
  Object.defineProperty(f, 'contentWindow', { get: () => win });
  window.dispatchEvent(new MessageEvent('message', { data: { type: HELLO, v: 1 }, source: win as unknown as Window, origin: 'null' }));
  const port = win.sent[0].ports[0];
  return { port, calls, pluginUISave };
}

describe('state in an encrypted session', () => {
  it('is sealed on its way to the server and opens again here', async () => {
    const { port } = await setup();
    expect((await ask(port, 1, 'state.set', { key: 'lastSearch', value: DOC })).error).toBeUndefined();
    expect((await ask(port, 2, 'state.get', { key: 'lastSearch' })).result).toBe(DOC);
    await flushPrefs();

    expect(prefsSent.length).toBeGreaterThan(0);
    for (const body of prefsSent) {
      expect(body).not.toContain('Gizli');
      expect(body).not.toContain('lastSearch');
    }
    expect(currentPrefs().appStateE2e).toBeTruthy();
    expect(appStateGet(APP, 'lastSearch'), 'the ordinary store is not written').toBeUndefined();
  });

  it('does not see the ordinary session’s settings, nor they its', async () => {
    appStateSet(APP, 'zoom', 'ordinary');
    const { port } = await setup();
    expect((await ask(port, 1, 'state.get', { key: 'zoom' })).result).toBeNull();
    await ask(port, 2, 'state.set', { key: 'zoom', value: 'sealed' });
    expect((await ask(port, 3, 'state.get', { key: 'zoom' })).result).toBe('sealed');
    expect(appStateGet(APP, 'zoom')).toBe('ordinary');
    await teardownDom();

    // The same app on a file that is not encrypted: its ordinary store.
    const plain = await setup({ plaintext: false });
    expect((await ask(plain.port, 4, 'state.get', { key: 'zoom' })).result).toBe('ordinary');
  });

  it('a locked folder: an empty store to read, nothing written', async () => {
    let locked = false;
    const { port } = await setup({ locked: () => locked });
    await ask(port, 1, 'state.set', { key: 'k', value: 'v' });
    locked = true;
    const got = await ask(port, 2, 'state.get', { key: 'k' });
    expect(got.error, 'no error: the app starts with an empty store').toBeUndefined();
    expect(got.result).toBeNull();
    const before = currentPrefs().appStateE2e;
    expect((await ask(port, 3, 'state.set', { key: 'k', value: 'w' })).error.code).toBe('unavailable');
    expect(currentPrefs().appStateE2e).toBe(before);
  });
});

describe('a folder whose names are encrypted (level 2)', () => {
  it('the app is told the decrypted name; no request carries it or its extension', async () => {
    const { port, calls, pluginUISave } = await setup();
    const s = await ask(port, 1, 'session.get');
    expect(s.result.files[0]).toMatchObject({ name: NAME, ext: 'docx', encrypted: 'folder', plaintext: true, readOnly: false });

    const r = await ask(port, 2, 'file.read', { as: 'text' });
    expect(r.result.text).toBe(DOC);
    const data = new TextEncoder().encode(`${DOC} - v2`).buffer;
    expect((await ask(port, 3, 'file.save', { data }, [data])).error).toBeUndefined();
    await ask(port, 4, 'state.set', { key: 'recent', value: NAME });
    await flushPrefs();

    expect(pluginUISave).toHaveBeenCalledTimes(1);
    // What each request names (addresses, paths, options): never the name,
    // never its kind.
    const named = calls.map((a) => JSON.stringify(a.filter((x) => !(x instanceof Blob))));
    for (const w of named) {
      expect(w).not.toContain('Maaş');
      expect(w).not.toContain('bordro');
      expect(w).not.toContain('docx');
    }
    expect(named[0]).toContain('ugkmCcOceKUyA7tsdZxzltk');
    // What they carry (the encrypted save, the sealed account document):
    // nothing of the name or the text either. (Ciphertext and base64 can hold
    // any four letters, so the kind is looked for as a name would carry it.)
    const carried = [
      ...(await Promise.all(calls.flatMap((a) => a.filter((x): x is Blob => x instanceof Blob)).map((b) => b.text()))),
      ...prefsSent,
    ];
    expect(carried.length).toBeGreaterThan(1);
    for (const w of carried) {
      expect(w).not.toContain('Maaş');
      expect(w).not.toContain('bordro');
      expect(w).not.toContain('.docx');
      expect(w).not.toContain('"docx"');
      expect(w).not.toContain('Gizli');
    }
  });
});

// filex 0.56: the sealed store is bound to the account (lib/appStateE2e), and
// where the page cannot tell which account it is signed in as, state.set was
// refused with the LOCKED folder's words ("the encrypted folder was locked")
// - the folder was open. It says what it is now (core text: the server
// cannot know what this page could not learn). Red before: the locked words.
describe('no account known', () => {
  it('state.set is refused in words that say so, not as a locked folder; state.get reads an empty store', async () => {
    configurePrefs({
      surface: 'web',
      fetchImpl: (async (url: unknown, init?: RequestInit) => {
        if (String(url).endsWith('/api/auth/me')) return new Response('{"error":"unauthorized"}', { status: 401 });
        if (init?.method === 'PUT') prefsSent.push(String(init.body ?? ''));
        return new Response('{}', { status: 200 });
      }) as unknown as typeof fetch,
    });
    const { port } = await setup();
    const r = await ask(port, 1, 'state.set', { key: 'k', value: 'v' });
    expect(r.error).toEqual({ code: 'unavailable', message: wordsIn('tr')('appframe.state_no_account') });
    expect(r.error.message).not.toBe('the encrypted folder was locked');
    const got = await ask(port, 2, 'state.get', { key: 'k' });
    expect(got.error).toBeUndefined();
    expect(got.result).toBeNull();
    await flushPrefs();
    expect(prefsSent.join('')).not.toContain('appStateE2e');
  });
});
