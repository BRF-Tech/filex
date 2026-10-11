// filex 0.56, task #189 - an app's interface editing a file of an encrypted
// folder (AppFrame `e2e`, lib/appPlaintext; backend app_ui_e2e.go).
//
// The frame is handed the folder's door by a host that holds the key. These
// hold it to the CryptPad model:
//
//  - the interface is told the file is handed over in the clear
//    (`plaintext`) and may be saved, only with the door AND the grant
//    files:e2e-plaintext; otherwise 0.55's answer (read-only, refused);
//  - `file.read` fetches through the server's door with `e2e=1` (never the
//    preview), decrypts in this page and hands the plaintext over as a
//    TRANSFERRED buffer;
//  - `file.save` sends the server only ciphertext (`filexe2e`), with
//    `e2e=1` and the file's `expect`, then the next save's `expect` from
//    the answer;
//  - while the frame holds plaintext nothing else leaves toward the server:
//    save-as and the module answer `unavailable`;
//  - a folder locked meanwhile: `unavailable`, nothing sent.
//
// Red before 0.56: AppFrame had no `e2e`, told every encrypted file
// read-only and refused its save.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import AppFrame from '@brftech/filex-core/src/components/plugin/AppFrame.vue';
import { createAppPlaintextDoor, type PlaintextKeys } from '@brftech/filex-core/src/lib/appPlaintext';
import { decryptFile, encryptFile, hasMagic } from '@brftech/filex-core/src/lib/e2ecrypto';
import { HELLO } from '../../../packages/app-ui/src/protocol';
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
const PATH = 'docs://Kasa/rapor.docx';
const DOC = 'Merhaba dünya: ğüşıöç İĞÜŞÖÇ';

async function setup(opts: { grants?: string[]; door?: boolean; engine?: boolean; locked?: () => boolean } = {}) {
  const fmk = await crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt']);
  const ct = await encryptFile(fmk, new TextEncoder().encode(DOC).buffer);
  const keys = (): PlaintextKeys | null => (opts.locked?.() ? null : { fmk, previous: null });
  const door = createAppPlaintextDoor(() => keys());
  const pluginUIRead = vi.fn(async () => new Response(ct.slice(0), { headers: { 'content-length': String(ct.byteLength) } }));
  const saved: Blob[] = [];
  const pluginUISave = vi.fn(async (_p: string, _v: string, _t: unknown, body: Blob, _o?: { e2e?: boolean; expect?: string }) => {
    saved.push(body);
    return { saved: true, path: PATH, name: 'rapor.docx', size: body.size, modified: 1760000000999 };
  });
  const api = {
    appUIUrl: (u: string) => `about:blank#${u}`,
    pluginUIRead,
    pluginUISave,
    pluginUICall: vi.fn(async () => ({ result: 1 })),
    pluginActionRun: vi.fn(async () => ({ op: { id: 1 } })),
    fetchResponse: vi.fn(async () => new Response('the preview: never for an encrypted file')),
  };
  mount(AppFrame, {
    props: {
      api: api as never,
      app: 'office-editor',
      view: 'editor',
      placement: 'viewer',
      ui: { url: '/_appui/office-editor/0123456789abcdef/index.html', grants: opts.grants ?? GRANTED, engine: !!opts.engine, version: '0.2.0' },
      locale: 'tr',
      files: [{ path: PATH, name: 'rapor.docx', size: ct.byteLength, modified: 1760000000000, encrypted: 'folder' }],
      e2e: opts.door === false ? null : door,
    },
    attachTo: document.body,
  });
  await settle();
  const f = document.querySelector('iframe') as HTMLIFrameElement;
  const win = fakeWindow();
  Object.defineProperty(f, 'contentWindow', { get: () => win });
  window.dispatchEvent(new MessageEvent('message', { data: { type: HELLO, v: 1 }, source: win as unknown as Window, origin: 'null' }));
  const port = win.sent[0].ports[0];
  return { port, api, fmk, ct, saved, pluginUIRead, pluginUISave };
}

describe('a file of an encrypted folder, handed to a granted app in the clear', () => {
  it('tells the interface so, and that it may save it', async () => {
    const { port } = await setup();
    const s = await ask(port, 1, 'session.get');
    expect(s.result.files).toEqual([
      { index: 0, name: 'rapor.docx', ext: 'docx', size: expect.any(Number), mime: '', readOnly: false, encrypted: 'folder', plaintext: true },
    ]);
    expect(JSON.stringify(s.result)).not.toContain('docs://');
  });

  it('reads through the server’s door with e2e=1, decrypts here, and transfers the plaintext', async () => {
    const { port, api, pluginUIRead } = await setup();
    const r = await ask(port, 1, 'file.read', { as: 'bytes' });
    expect(r.error).toBeUndefined();
    expect(pluginUIRead).toHaveBeenCalledWith('office-editor', 'editor', PATH, { e2e: true });
    expect(api.fetchResponse).not.toHaveBeenCalled();
    expect(r.result.bytes).toBeInstanceOf(ArrayBuffer);
    expect(new TextDecoder().decode(r.result.bytes)).toBe(DOC);
    expect(r.result.size).toBe(new TextEncoder().encode(DOC).byteLength);

    const t = await ask(port, 2, 'file.read', { as: 'text' });
    expect(t.result.text).toBe(DOC);
  });

  it('saves only ciphertext, with e2e=1 and the file’s expect, then the answer’s', async () => {
    const { port, fmk, ct, saved, pluginUISave } = await setup();
    const data = new TextEncoder().encode(`${DOC} - kaydedildi`).buffer;
    const r = await ask(port, 1, 'file.save', { data }, [data]);
    expect(r.error).toBeUndefined();
    expect(r.result).toEqual({ saved: true, size: new TextEncoder().encode(`${DOC} - kaydedildi`).byteLength });
    expect(pluginUISave).toHaveBeenCalledWith('office-editor', 'editor', { path: PATH }, expect.any(Blob), {
      e2e: true,
      expect: `${ct.byteLength}:1760000000000`,
    });
    const sent = new Uint8Array(await saved[0].arrayBuffer());
    expect(hasMagic(sent)).toBe(true);
    expect(new TextDecoder().decode(sent)).not.toContain('kaydedildi');
    expect(new TextDecoder().decode(await decryptFile(fmk, sent.buffer))).toBe(`${DOC} - kaydedildi`);

    const again = await ask(port, 2, 'file.save', { data: 'ikinci' });
    expect(again.error).toBeUndefined();
    expect(pluginUISave.mock.calls[1][4]).toEqual({ e2e: true, expect: `${saved[0].size}:1760000000999` });
  });

  it('lets nothing else leave toward the server while it holds plaintext', async () => {
    const { port, api } = await setup({ engine: true });
    expect((await ask(port, 1, 'file.saveAs', { name: 'kopya.docx', data: 'x' })).error.code).toBe('unavailable');
    expect((await ask(port, 2, 'engine.call', { method: 'm' })).error.code).toBe('unavailable');
    expect((await ask(port, 3, 'job.submit', { action: 'a' })).error.code).toBe('unavailable');
    expect(api.pluginUISave).not.toHaveBeenCalled();
    expect(api.pluginUICall).not.toHaveBeenCalled();
    expect(api.pluginActionRun).not.toHaveBeenCalled();
  });

  it('a folder locked meanwhile hands over nothing and takes nothing', async () => {
    let locked = false;
    const { port, pluginUISave } = await setup({ locked: () => locked });
    locked = true;
    expect((await ask(port, 1, 'file.read', { as: 'bytes' })).error.code).toBe('unavailable');
    expect((await ask(port, 2, 'file.save', { data: 'v2' })).error.code).toBe('unavailable');
    expect(pluginUISave).not.toHaveBeenCalled();
  });
});

describe('without the door or the grant: 0.55’s answer', () => {
  it('no grant: read-only, no plaintext, the read goes to the server as before (which refuses it)', async () => {
    const { port, pluginUIRead, pluginUISave } = await setup({ grants: ['files:read', 'files:write', 'ui'] });
    const s = await ask(port, 1, 'session.get');
    expect(s.result.files[0]).toMatchObject({ encrypted: 'folder', readOnly: true });
    expect(s.result.files[0].plaintext).toBeUndefined();
    await ask(port, 2, 'file.read', { as: 'bytes' });
    expect(pluginUIRead).toHaveBeenCalledWith('office-editor', 'editor', PATH);
    expect((await ask(port, 3, 'file.save', { data: 'v2' })).error.code).toBe('read_only');
    expect(pluginUISave).not.toHaveBeenCalled();
  });

  it('no door: the same', async () => {
    const { port, pluginUISave } = await setup({ door: false });
    const s = await ask(port, 1, 'session.get');
    expect(s.result.files[0].plaintext).toBeUndefined();
    expect(s.result.files[0].readOnly).toBe(true);
    expect((await ask(port, 2, 'file.save', { data: 'v2' })).error.code).toBe('read_only');
    expect(pluginUISave).not.toHaveBeenCalled();
  });
});
