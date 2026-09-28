// The SDK an app's interface bundles (@brftech/filex-app-ui): the app's half
// of the handshake takes the port ONLY from its parent window, and then speaks
// over the port alone — requests, events, and the host's own `save` request.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PORT } from '../../../packages/app-ui/src/protocol';

afterEach(() => {
  vi.restoreAllMocks();
  vi.resetModules();
});

async function freshSdk() {
  return import('../../../packages/app-ui/src/index');
}

/** Make this window a frame: its parent is `parent`. */
function asFrame(parent: { postMessage: (...a: unknown[]) => void }) {
  const desc = Object.getOwnPropertyDescriptor(window, 'parent');
  Object.defineProperty(window, 'parent', { configurable: true, get: () => parent });
  return () => {
    if (desc) Object.defineProperty(window, 'parent', desc);
  };
}

describe('connect()', () => {
  it('says hello to its parent, takes the port only from it, then asks for the session', async () => {
    const parent = { postMessage: vi.fn() };
    const restore = asFrame(parent);
    try {
      const { connect } = await freshSdk();
      const p = connect({ applyTheme: false, saveShortcut: false });
      expect(parent.postMessage).toHaveBeenCalledWith({ type: 'filex:hello', v: 1 }, '*');

      const impostor = new MessageChannel();
      window.dispatchEvent(new MessageEvent('message', { data: { type: PORT, v: 1 }, source: window as Window, ports: [impostor.port2] }));
      const impostorAsked = vi.fn();
      impostor.port1.onmessage = impostorAsked;

      const ch = new MessageChannel();
      const host = ch.port1;
      const saves: unknown[] = [];
      host.onmessage = (ev) => {
        const m = ev.data;
        if (m.method === 'session.get') {
          host.postMessage({ id: m.id, result: { v: 1, app: { name: 'x', version: '1' }, files: [{ index: 0, name: 'a.txt' }], grants: [] } });
        } else if (m.method === 'file.save') {
          saves.push(m.params);
          host.postMessage({ id: m.id, result: { saved: true, size: 5 } });
        } else if (m.hid) {
          saves.push({ answer: m });
        }
      };
      window.dispatchEvent(new MessageEvent('message', { data: { type: PORT, v: 1 }, source: parent as unknown as Window, ports: [ch.port2] }));
      const fx = await p;
      expect(fx.session.app.name).toBe('x');
      await new Promise((r) => setTimeout(r, 10));
      expect(impostorAsked).not.toHaveBeenCalled();

      // The host's own request: save now. The handler's document is saved.
      fx.onSave(() => 'hello');
      host.postMessage({ hid: 1, request: 'save' });
      await new Promise((r) => setTimeout(r, 20));
      expect(saves[0]).toMatchObject({ index: 0, data: 'hello' });
      expect(saves[1]).toMatchObject({ answer: { hid: 1, result: { saved: true, size: 5 } } });
    } finally {
      restore();
    }
  });

  it('refuses to run outside filex', async () => {
    const { connect } = await freshSdk();
    await expect(connect()).rejects.toMatchObject({ code: 'unavailable' });
  });
});
