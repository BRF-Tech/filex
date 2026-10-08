// Push notifications on this device (#191): core's one flow
// (packages/core/src/lib/webPush.ts), driven against a fake browser - its
// Notification permission, its service worker's PushManager - and a fake server.
//
// What is pinned:
//   1. The permission is asked FIRST in enable(), before anything is awaited
//      (Safari on an iPhone asks only from the tap itself), and nothing is
//      subscribed without it.
//   2. The subscription is made with the SERVER's key and handed over as the
//      browser gives it, with a label naming the device.
//   3. This device is found in the person's list by the hash of its endpoint
//      - the endpoint itself never comes back from the server.
//   4. reconcile() asks nothing and sends nothing where the person did not
//      turn push on in this browser; where they did, a rotated key or a
//      device the server forgot is subscribed again.
//   5. Signing out forgets this browser (server and browser).
//
// ⚠ Red on the code before it: lib/webPush does not exist.
import { describe, expect, it, vi } from 'vitest';

import {
  base64UrlToBytes,
  createWebPush,
  deviceLabel,
  WEB_PUSH_NOTE_KEY,
  type WebPushApi,
  type WebPushStatus,
} from '@brftech/filex-core/src/lib/webPush';

const KEY_A = 'BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8';
const KEY_B = 'BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4';

/** A browser's PushSubscription. */
function fakeSubscription(endpoint: string, key: string) {
  return {
    endpoint,
    options: { applicationServerKey: base64UrlToBytes(key).buffer },
    toJSON: () => ({ endpoint, keys: { p256dh: 'p256-' + endpoint, auth: 'auth-' + endpoint } }),
    unsubscribe: vi.fn(async () => true),
  };
}

type FakeSub = ReturnType<typeof fakeSubscription>;

/** The browser: permission, a worker with a PushManager, and storage. */
function fakeBrowser(permission: NotificationPermission, existing: FakeSub | null = null) {
  const order: string[] = [];
  const browser = {
    order,
    sub: existing as FakeSub | null,
    notification: {
      permission,
      requestPermission: vi.fn(async () => {
        order.push('ask');
        browser.notification.permission = 'granted';
        return 'granted' as NotificationPermission;
      }),
    },
    pushManager: {
      getSubscription: vi.fn(async () => browser.sub),
      subscribe: vi.fn(async (opts: { userVisibleOnly: boolean; applicationServerKey: Uint8Array }) => {
        order.push('subscribe');
        const key = btoa(String.fromCharCode(...opts.applicationServerKey))
          .replace(/\+/g, '-')
          .replace(/\//g, '_')
          .replace(/=+$/, '');
        browser.sub = fakeSubscription('https://fcm.googleapis.com/fcm/send/new', key);
        return browser.sub;
      }),
    },
    store: new Map<string, string>(),
  };
  return browser;
}

function fakeServer(status: Partial<WebPushStatus> = {}) {
  const st: WebPushStatus = { available: true, public_key: KEY_A, devices: [], ...status };
  const api = {
    status: vi.fn(async () => {
      fake.order?.push('status');
      return st;
    }),
    subscribe: vi.fn(async (body: { endpoint: string }) => {
      const dev = { id: st.devices.length + 1, label: 'x', endpoint_hash: 'h:' + body.endpoint, created_at: '' };
      st.devices.push(dev);
      return dev;
    }),
    forget: vi.fn(async (endpoint: string) => {
      st.devices = st.devices.filter((d) => d.endpoint_hash !== 'h:' + endpoint);
    }),
    remove: vi.fn(async (id: number) => {
      st.devices = st.devices.filter((d) => d.id !== id);
    }),
    test: vi.fn(async () => ({ sent: st.devices.length, failed: 0 })),
  } satisfies WebPushApi;
  const fake = { api, st, order: null as string[] | null };
  return fake;
}

function flowFor(browser: ReturnType<typeof fakeBrowser>, server: ReturnType<typeof fakeServer>, userId = 7) {
  server.order = browser.order;
  return createWebPush(server.api, {
    userId,
    registration: async () => ({ pushManager: browser.pushManager, active: {} }) as unknown as ServiceWorkerRegistration,
    notification: browser.notification,
    supported: () => true,
    storage: {
      getItem: (k) => browser.store.get(k) ?? null,
      setItem: (k, v) => void browser.store.set(k, v),
      removeItem: (k) => void browser.store.delete(k),
    },
    userAgent:
      'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0 Mobile Safari/537.36',
    sha256: async (text) => 'h:' + text,
  });
}

describe('push on this device: turning it on', () => {
  it('asks the permission first - before anything is awaited - then subscribes with the server key', async () => {
    const browser = fakeBrowser('default');
    const server = fakeServer();
    const flow = flowFor(browser, server);
    const state = await flow.enable();
    expect(browser.order[0]).toBe('ask');
    expect(browser.pushManager.subscribe).toHaveBeenCalledTimes(1);
    const opts = browser.pushManager.subscribe.mock.calls[0][0];
    expect(opts.userVisibleOnly).toBe(true);
    expect(Array.from(opts.applicationServerKey)).toEqual(Array.from(base64UrlToBytes(KEY_A)));
    expect(server.api.subscribe).toHaveBeenCalledWith({
      endpoint: 'https://fcm.googleapis.com/fcm/send/new',
      keys: { p256dh: 'p256-https://fcm.googleapis.com/fcm/send/new', auth: 'auth-https://fcm.googleapis.com/fcm/send/new' },
      label: 'Chrome · Android',
    });
    expect(state.on).toBe(true);
    expect(state.thisDevice).toBe(1);
    expect(browser.store.get(`${WEB_PUSH_NOTE_KEY}.7`)).toBe('1');
  });

  it('subscribes nothing when the browser says no, or the server has no push', async () => {
    const denied = fakeBrowser('denied');
    const s1 = fakeServer();
    const st1 = await flowFor(denied, s1).enable();
    expect(denied.pushManager.subscribe).not.toHaveBeenCalled();
    expect(s1.api.subscribe).not.toHaveBeenCalled();
    expect(st1.on).toBe(false);
    expect(st1.permission).toBe('denied');

    const off = fakeBrowser('granted');
    const s2 = fakeServer({ available: false, reason: 'no_secret_key', public_key: undefined });
    const st2 = await flowFor(off, s2).enable();
    expect(off.pushManager.subscribe).not.toHaveBeenCalled();
    expect(st2.on).toBe(false);
    expect(st2.status?.reason).toBe('no_secret_key');
  });

  it('replaces a subscription made with another key (the server rotated it)', async () => {
    const old = fakeSubscription('https://fcm.googleapis.com/fcm/send/old', KEY_B);
    const browser = fakeBrowser('granted', old);
    const server = fakeServer();
    await flowFor(browser, server).enable();
    expect(old.unsubscribe).toHaveBeenCalled();
    expect(browser.pushManager.subscribe).toHaveBeenCalledTimes(1);
  });
});

describe('push on this device: finding it, turning it off, removing devices', () => {
  it('finds this device by the hash of its endpoint', async () => {
    const sub = fakeSubscription('https://fcm.googleapis.com/fcm/send/mine', KEY_A);
    const browser = fakeBrowser('granted', sub);
    const server = fakeServer({
      devices: [
        { id: 3, label: 'Firefox · Windows', endpoint_hash: 'h:https://elsewhere', created_at: '' },
        { id: 4, label: 'Chrome · Android', endpoint_hash: 'h:https://fcm.googleapis.com/fcm/send/mine', created_at: '' },
      ],
    });
    const state = await flowFor(browser, server).state();
    expect(state.thisDevice).toBe(4);
    expect(state.on).toBe(true);
  });

  it('turning it off forgets this browser on the server and in the browser', async () => {
    const sub = fakeSubscription('https://fcm.googleapis.com/fcm/send/mine', KEY_A);
    const browser = fakeBrowser('granted', sub);
    browser.store.set(`${WEB_PUSH_NOTE_KEY}.7`, '1');
    const server = fakeServer({
      devices: [{ id: 4, label: '', endpoint_hash: 'h:https://fcm.googleapis.com/fcm/send/mine', created_at: '' }],
    });
    const state = await flowFor(browser, server).disable();
    expect(server.api.forget).toHaveBeenCalledWith('https://fcm.googleapis.com/fcm/send/mine');
    expect(sub.unsubscribe).toHaveBeenCalled();
    expect(browser.store.has(`${WEB_PUSH_NOTE_KEY}.7`)).toBe(false);
    expect(state.on).toBe(false);
  });

  it('removing another device leaves this one alone; removing this one turns it off here', async () => {
    const sub = fakeSubscription('https://fcm.googleapis.com/fcm/send/mine', KEY_A);
    const browser = fakeBrowser('granted', sub);
    browser.store.set(`${WEB_PUSH_NOTE_KEY}.7`, '1');
    const server = fakeServer({
      devices: [
        { id: 3, label: '', endpoint_hash: 'h:https://elsewhere', created_at: '' },
        { id: 4, label: '', endpoint_hash: 'h:https://fcm.googleapis.com/fcm/send/mine', created_at: '' },
      ],
    });
    const flow = flowFor(browser, server);
    let state = await flow.remove(3);
    expect(sub.unsubscribe).not.toHaveBeenCalled();
    expect(state.on).toBe(true);
    state = await flow.remove(4);
    expect(sub.unsubscribe).toHaveBeenCalled();
    expect(browser.store.has(`${WEB_PUSH_NOTE_KEY}.7`)).toBe(false);
  });
});

describe('push on this device: on every start, and on sign-out', () => {
  it('asks nothing and sends nothing where the person did not turn it on here', async () => {
    const browser = fakeBrowser('granted');
    const server = fakeServer();
    await flowFor(browser, server).reconcile();
    expect(server.api.status).not.toHaveBeenCalled();
    expect(browser.pushManager.subscribe).not.toHaveBeenCalled();
    expect(browser.notification.requestPermission).not.toHaveBeenCalled();
  });

  it('where they did, a device the server forgot (or a rotated key) subscribes again, silently', async () => {
    const old = fakeSubscription('https://fcm.googleapis.com/fcm/send/old', KEY_B);
    const browser = fakeBrowser('granted', old);
    browser.store.set(`${WEB_PUSH_NOTE_KEY}.7`, '1');
    const server = fakeServer();
    await flowFor(browser, server).reconcile();
    expect(old.unsubscribe).toHaveBeenCalled();
    expect(browser.pushManager.subscribe).toHaveBeenCalledTimes(1);
    expect(server.api.subscribe).toHaveBeenCalledTimes(1);
    expect(browser.notification.requestPermission).not.toHaveBeenCalled();

    // Registered with the right key: nothing to do.
    await flowFor(browser, server).reconcile();
    expect(server.api.subscribe).toHaveBeenCalledTimes(1);
  });

  it('the note is per person: another account on this browser is not subscribed', async () => {
    const browser = fakeBrowser('granted');
    browser.store.set(`${WEB_PUSH_NOTE_KEY}.7`, '1');
    const server = fakeServer();
    await flowFor(browser, server, 8).reconcile();
    expect(server.api.subscribe).not.toHaveBeenCalled();
  });

  it('signing out forgets this browser', async () => {
    const sub = fakeSubscription('https://fcm.googleapis.com/fcm/send/mine', KEY_A);
    const browser = fakeBrowser('granted', sub);
    const server = fakeServer();
    await flowFor(browser, server).forgetDevice();
    expect(server.api.forget).toHaveBeenCalledWith('https://fcm.googleapis.com/fcm/send/mine');
    expect(sub.unsubscribe).toHaveBeenCalled();
  });
});

describe('the device label', () => {
  it.each([
    ['Mozilla/5.0 (iPhone; CPU iPhone OS 17_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Mobile/15E148 Safari/604.1', 'Safari · iPhone'],
    ['Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0 Safari/537.36 Edg/129.0', 'Edge · Windows'],
    ['Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0', 'Firefox · Linux'],
    ['Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/25.0 Chrome/121.0 Mobile Safari/537.36', 'Samsung Internet · Android'],
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Safari/605.1.15', 'Safari · macOS'],
    ['', ''],
  ])('%s', (ua, label) => {
    expect(deviceLabel(ua)).toBe(label);
  });
});
