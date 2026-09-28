// An app's approved version changing under an open interface (M4): nothing
// updates itself, an administrator approves — and then every open frame on
// that app says so, with "Reload", and the first opening after it says
// "updated to X" once per person.
//
//  - the realtime frame `app.updated` reaches the frames of THAT app only,
//    and not when it names the version already open;
//  - "Reload" asks about unsaved changes first (the closing question), and
//    opens the newer interface's address from the server's list;
//  - the "updated" note is said once: the version seen is kept in the
//    account's preferences.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';

import AppFrame from '@brftech/filex-core/src/components/plugin/AppFrame.vue';
import { announceAppUpdated } from '@brftech/filex-core/src/lib/appUpdates';
import { RealtimeClient } from '@brftech/filex-core/src/lib/realtime';
import { currentPrefs, savePref } from '@brftech/filex-core/src/lib/prefs';
import { HELLO } from '../../../packages/app-ui/src/protocol';

const mounted: VueWrapper[] = [];
afterEach(() => {
  mounted.splice(0).forEach((w) => w.unmount());
  vi.restoreAllMocks();
  document.body.innerHTML = '';
});

const settle = async () => {
  for (let i = 0; i < 4; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};

const UI = { url: '/_appui/sketch/0123456789abcdef/index.html', grants: ['files:read', 'files:write', 'ui'], engine: false, version: '1.0.0' };
const NEXT = { ...UI, url: '/_appui/sketch/fedcba9876543210/index.html', version: '1.1.0' };

function mountFrame() {
  const api = {
    appUIUrl: (u: string) => `about:blank#${u}`,
    pluginUISave: vi.fn(async () => ({ saved: true, path: 'main://doc.sketch', name: 'doc.sketch', size: 2 })),
    pluginActions: vi.fn(async () => ({
      actions: [],
      views: [{ plugin: 'sketch', id: 'editor', placement: 'viewer', label: 'Sketch', ui: NEXT }],
    })),
    fetchResponse: vi.fn(async () => new Response('x')),
  };
  const w = mount(AppFrame, {
    props: {
      api, app: 'sketch', view: 'editor', placement: 'viewer', ui: UI, locale: 'en',
      files: [{ path: 'main://doc.sketch', name: 'doc.sketch', size: 1 }],
    },
    attachTo: document.body,
  });
  mounted.push(w);
  return { w, api };
}

/** Connect the frame's bridge and return what the interface received. */
function connect() {
  const f = document.querySelector('iframe') as HTMLIFrameElement;
  const got: unknown[] = [];
  const win = {
    postMessage(_d: unknown, _o: string, transfer?: Transferable[]) {
      const port = (transfer ?? [])[0] as MessagePort;
      port.onmessage = (ev) => got.push(ev.data);
      (win as { port?: MessagePort }).port = port;
    },
  } as { postMessage: (...a: never[]) => void; port?: MessagePort };
  Object.defineProperty(f, 'contentWindow', { get: () => win, configurable: true });
  window.dispatchEvent(new MessageEvent('message', { data: { type: HELLO, v: 1 }, source: win as unknown as Window, origin: 'null' }));
  return { got, port: () => win.port! };
}

describe('a newer version approved while the interface is open', () => {
  it('is said on the frames of that app only, and told to the interface', async () => {
    savePref('appsSeen', JSON.stringify({ sketch: '1.0.0' }));
    mountFrame();
    await settle();
    const { got } = connect();
    await settle();

    announceAppUpdated('other', '9.0.0');
    announceAppUpdated('sketch', '1.0.0');
    await settle();
    expect(document.querySelector('[data-testid="appframe-updated"]'), 'another app, or the version already open').toBeNull();

    announceAppUpdated('sketch', '1.1.0');
    await settle();
    const note = document.querySelector('[data-testid="appframe-updated"]');
    expect(note?.textContent).toContain('approved a new version of sketch');
    expect(document.querySelector('[data-testid="appframe-reload"]')).toBeTruthy();
    expect(got).toContainEqual({ event: 'app.updated', data: { version: '1.1.0' } });
  });

  it('"Reload" asks about unsaved changes first, then opens the newer interface', async () => {
    savePref('appsSeen', JSON.stringify({ sketch: '1.0.0' }));
    const { api } = mountFrame();
    await settle();
    const { port } = connect();
    await settle();
    port().postMessage({ id: 1, method: 'ui.dirty', params: { dirty: true } });
    await settle();

    announceAppUpdated('sketch', '1.1.0');
    await settle();
    (document.querySelector('[data-testid="appframe-reload"]') as HTMLButtonElement).click();
    await settle();
    expect(document.querySelector('[data-testid="appframe-unsaved"]'), 'the closing question').toBeTruthy();
    (document.querySelector('[data-testid="appframe-keep"]') as HTMLButtonElement).click();
    await settle();
    expect(api.pluginActions).not.toHaveBeenCalled();
    expect(document.querySelector('iframe')!.getAttribute('src')).toContain('0123456789abcdef');

    (document.querySelector('[data-testid="appframe-reload"]') as HTMLButtonElement).click();
    await settle();
    (document.querySelector('[data-testid="appframe-discard"]') as HTMLButtonElement).click();
    await settle();
    expect(api.pluginActions).toHaveBeenCalledTimes(1);
    const frames = document.querySelectorAll('iframe');
    expect(frames).toHaveLength(1);
    expect(frames[0].getAttribute('src')).toBe('about:blank#/_appui/sketch/fedcba9876543210/index.html');
    expect(frames[0].getAttribute('sandbox'), 'the new element is sandboxed like the first').toBe('allow-scripts');
    expect(document.querySelector('[data-testid="appframe-updated"]')).toBeNull();
    expect(JSON.parse(currentPrefs().appsSeen ?? '{}').sketch).toBe('1.1.0');
  });
});

describe('the first opening after an approval', () => {
  it('says "updated to" once per person, and never on a first use', async () => {
    savePref('appsSeen', JSON.stringify({ sketch: '0.9.0' }));
    mountFrame();
    await settle();
    expect(document.querySelector('[data-testid="appframe-updated"]')?.textContent).toContain('sketch was updated to 1.0.0');
    expect(document.querySelector('[data-testid="appframe-reload"]'), 'nothing to reload').toBeNull();
    expect(JSON.parse(currentPrefs().appsSeen ?? '{}').sketch).toBe('1.0.0');

    mounted.splice(0).forEach((w) => w.unmount());
    document.body.innerHTML = '';
    mountFrame();
    await settle();
    expect(document.querySelector('[data-testid="appframe-updated"]'), 'once').toBeNull();

    mounted.splice(0).forEach((w) => w.unmount());
    document.body.innerHTML = '';
    savePref('appsSeen', JSON.stringify({}));
    mountFrame();
    await settle();
    expect(document.querySelector('[data-testid="appframe-updated"]'), 'a first use is not an update').toBeNull();
    expect(JSON.parse(currentPrefs().appsSeen ?? '{}').sketch).toBe('1.0.0');
  });
});

describe('the realtime frame', () => {
  it('reaches the handler only in its own shape', async () => {
    const sockets: Array<{ onmessage?: (ev: MessageEvent) => void; onopen?: () => void }> = [];
    class FakeWS {
      onmessage?: (ev: MessageEvent) => void;
      onopen?: () => void;
      onclose?: () => void;
      onerror?: () => void;
      constructor() {
        sockets.push(this);
      }
      send() {}
      close() {}
    }
    vi.stubGlobal('WebSocket', FakeWS);
    const heard = vi.fn();
    const c = new RealtimeClient({
      getTicket: async () => ({ ticket: 't', ws_url: 'ws://x/api/ws' }),
      handlers: { onAppUpdated: heard },
    });
    await settle();
    const ws = sockets[0];
    ws.onmessage?.(new MessageEvent('message', { data: JSON.stringify({ type: 'app.updated', app: 'sketch', version: '1.1.0' }) }));
    ws.onmessage?.(new MessageEvent('message', { data: JSON.stringify({ type: 'app.updated', app: 'sketch' }) }));
    ws.onmessage?.(new MessageEvent('message', { data: JSON.stringify({ type: 'app.updated', app: 7, version: '1' }) }));
    expect(heard).toHaveBeenCalledTimes(1);
    expect(heard).toHaveBeenCalledWith({ type: 'app.updated', app: 'sketch', version: '1.1.0' });
    c.close();
    vi.unstubAllGlobals();
  });
});
