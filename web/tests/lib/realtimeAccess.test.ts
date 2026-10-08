// #196 - the live socket's `access.changed` frame: the server's word that what
// this person may do may have changed (a grant, a role, a group, a rule, the
// encryption policy, an approval; backend internal/realtime/access.go). The
// explorer then asks the answers its right-click menus depend on again
// (lib/menuAnswers). A socket that comes back after a drop may have missed
// one, so a reconnect says so too.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises } from '@vue/test-utils';

import { RealtimeClient } from '@brftech/filex-core/src/lib/realtime';
import { useRealtime } from '@brftech/filex-core/src/composables/useRealtime';

interface FakeSocket {
  onmessage?: (ev: MessageEvent) => void;
  onopen?: () => void;
  onclose?: () => void;
  onerror?: () => void;
  readyState: number;
}
let sockets: FakeSocket[] = [];

class FakeWS implements FakeSocket {
  static OPEN = 1;
  onmessage?: (ev: MessageEvent) => void;
  onopen?: () => void;
  onclose?: () => void;
  onerror?: () => void;
  readyState = 1;
  constructor() {
    sockets.push(this);
  }
  send() {}
  close() {}
}

const frame = (ws: FakeSocket, body: unknown) =>
  ws.onmessage?.(new MessageEvent('message', { data: JSON.stringify(body) }));

beforeEach(() => {
  sockets = [];
  vi.stubGlobal('WebSocket', FakeWS);
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('the access.changed frame', () => {
  it('reaches the handler, saying whether everybody heard it', async () => {
    const heard = vi.fn();
    const c = new RealtimeClient({
      getTicket: async () => ({ ticket: 't', ws_url: 'ws://x/api/ws' }),
      handlers: { onAccessChanged: heard },
    });
    await flushPromises();
    const ws = sockets[0];
    frame(ws, { type: 'access.changed' });
    frame(ws, { type: 'access.changed', scope: 'all' });
    frame(ws, { type: 'change', path: 'main://', action: 'upload' });
    expect(heard.mock.calls).toEqual([[{ all: false }], [{ all: true }]]);
    c.close();
  });
});

describe('useRealtime tells the explorer', () => {
  it('a frame, as news that is not a resync', async () => {
    const onAccess = vi.fn();
    const rt = useRealtime({ wsTicket: async () => ({ ticket: 't', ws_url: 'ws://x/api/ws' }) }, { reload: vi.fn(), onAccess });
    rt.start();
    await flushPromises();
    sockets[0].onopen?.();
    expect(onAccess, 'the first connection is not news').not.toHaveBeenCalled();
    frame(sockets[0], { type: 'access.changed', scope: 'all' });
    expect(onAccess).toHaveBeenCalledWith({ all: true, resync: false });
    rt.stop();
  });

  it('a socket that came back after a drop, as a resync: it may have missed a frame', async () => {
    // Only the timers: flushPromises schedules on setImmediate.
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
    const onAccess = vi.fn();
    const reload = vi.fn();
    const rt = useRealtime({ wsTicket: async () => ({ ticket: 't', ws_url: 'ws://x/api/ws' }) }, { reload, onAccess });
    rt.start();
    await flushPromises();
    sockets[0].onopen?.();
    sockets[0].onclose?.();
    // The client reconnects after its backoff.
    await vi.advanceTimersByTimeAsync(1_000);
    await flushPromises();
    expect(sockets).toHaveLength(2);
    sockets[1].onopen?.();
    expect(onAccess).toHaveBeenCalledTimes(1);
    expect(onAccess).toHaveBeenCalledWith({ all: false, resync: true });
    rt.stop();
  });
});
