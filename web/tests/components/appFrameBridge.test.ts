// An app's own interface (docs/APP-PLUGINS-API.md → An app's own interface):
// the frame filex draws and the bridge it talks over. Every rule here was
// measured in Chrome, Firefox and WebKit before it was written (lessons #635
// and #624) — these tests hold the code to them.
//
//  - the sandbox is on the element BEFORE its address, and before it is in
//    the document; never allow-same-origin, never an `allow` attribute;
//  - the hello is taken only from the frame we drew (`event.source`) — a
//    sibling frame's forged hello or save is ignored, whatever it says;
//  - one port per element: a second hello is ignored, and a second load of
//    the frame closes the port;
//  - a call the app was not granted is refused, and a save goes only to the
//    file the interface was opened with.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import AppFrame from '@brftech/filex-core/src/components/plugin/AppFrame.vue';
import { createAppBridge } from '@brftech/filex-core/src/lib/appBridge';
import { HELLO, PORT } from '../../../packages/app-ui/src/protocol';
import { teardownDom, unmountAll } from '../helpers/teardown';
import { answerAccountPrefs } from '../helpers/accountPrefs';

// A frame that opens writes "seen this version" to the account (400 ms later).
answerAccountPrefs();

// Pages down first (in-flight work lands, pages unmount, <body> empties),
// while this file's mocks still answer; only then are the mocks taken away.
afterEach(async () => {
  await teardownDom();
  vi.restoreAllMocks();
});

const settle = async () => {
  for (let i = 0; i < 4; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};

/** A window-like the frame "is": what postMessage it received. */
function fakeWindow() {
  const sent: Array<{ data: unknown; ports: MessagePort[] }> = [];
  return {
    sent,
    postMessage(data: unknown, _origin: string, transfer?: Transferable[]) {
      sent.push({ data, ports: (transfer ?? []) as MessagePort[] });
    },
  };
}

function hello(source: unknown) {
  window.dispatchEvent(new MessageEvent('message', { data: { type: HELLO, v: 1 }, source: source as Window, origin: 'null' }));
}

/** Ask over a port and wait for the answer. */
function ask(port: MessagePort, id: number, method: string, params?: unknown): Promise<any> {
  return new Promise((resolve) => {
    const prev = port.onmessage;
    port.onmessage = (ev) => {
      if (ev.data?.id === id) {
        port.onmessage = prev;
        resolve(ev.data);
      }
    };
    port.postMessage({ id, method, params });
  });
}

describe('the bridge (lib/appBridge)', () => {
  it('takes a hello only from the frame it drew, and only once', async () => {
    const mine = fakeWindow();
    const sibling = fakeWindow();
    const frame = { contentWindow: mine } as unknown as HTMLIFrameElement;
    const connected = vi.fn();
    const b = createAppBridge({ frame: () => frame, handlers: { 'session.get': () => ({ ok: 1 }) }, onConnect: connected });

    hello(sibling); // another sandboxed frame on the page: origin "null" too
    expect(mine.sent).toHaveLength(0);
    expect(sibling.sent).toHaveLength(0);
    expect(connected).not.toHaveBeenCalled();

    window.dispatchEvent(new MessageEvent('message', { data: { type: 'filex:save', v: 1 }, source: mine as unknown as Window, origin: 'null' }));
    expect(mine.sent).toHaveLength(0);

    hello(mine);
    expect(mine.sent).toHaveLength(1);
    expect((mine.sent[0].data as { type: string }).type).toBe(PORT);
    expect(mine.sent[0].ports).toHaveLength(1);
    expect(connected).toHaveBeenCalledTimes(1);

    hello(mine); // a second hello: no second port
    expect(mine.sent).toHaveLength(1);

    const port = mine.sent[0].ports[0];
    const answer = await ask(port, 1, 'session.get');
    expect(answer).toEqual({ id: 1, result: { ok: 1 } });
    expect((await ask(port, 2, 'file.delete')).error.code).toBe('unknown_method');
    expect((await ask(port, 3, 'file.read')).error.code).toBe('unavailable');
    b.destroy();
  });

  it('closes the port when the frame loads a second document, and takes no hello after it', async () => {
    const mine = fakeWindow();
    const frame = { contentWindow: mine } as unknown as HTMLIFrameElement;
    const gone = vi.fn();
    const b = createAppBridge({ frame: () => frame, handlers: {}, onDisconnect: gone });
    b.frameLoaded(); // the first document
    hello(mine);
    expect(b.connected()).toBe(true);
    b.frameLoaded(); // it navigated itself somewhere
    expect(b.connected()).toBe(false);
    expect(gone).toHaveBeenCalledWith('reload');
    hello(mine);
    expect(mine.sent).toHaveLength(1);
    b.destroy();
  });

  it('never takes a hello after a second load even if none came before', () => {
    const mine = fakeWindow();
    const frame = { contentWindow: mine } as unknown as HTMLIFrameElement;
    const b = createAppBridge({ frame: () => frame, handlers: {} });
    b.frameLoaded();
    b.frameLoaded();
    hello(mine);
    expect(mine.sent).toHaveLength(0);
    b.destroy();
  });
});

// ── the frame (AppFrame.vue) ─────────────────────────────────────────────

function apiStub(over: Record<string, unknown> = {}) {
  return {
    // about: — happy-dom would otherwise try to load the frame's address.
    appUIUrl: (u: string) => `about:blank#${u}`,
    pluginUISave: vi.fn(async () => ({ saved: true, path: 'main://doc.sketch', name: 'doc.sketch', size: 2 })),
    pluginUICall: vi.fn(async () => ({ result: 42 })),
    pluginActionRun: vi.fn(async () => ({ op: { id: 9 } })),
    fetchResponse: vi.fn(async () => new Response('file body', { headers: { 'content-length': '9' } })),
    ...over,
  };
}

const UI = { url: '/_appui/sketch/0123456789abcdef/index.html', grants: ['files:read', 'files:write', 'ui'], engine: false, version: '1.0.0' };

function mountFrame(props: Record<string, unknown> = {}) {
  const api = apiStub();
  const w = mount(AppFrame, {
    props: {
      api, app: 'sketch', view: 'editor', placement: 'viewer', ui: UI, locale: 'en',
      files: [{ path: 'main://doc.sketch', name: 'doc.sketch', size: 9 }],
      ...props,
    },
    attachTo: document.body,
  });
  return { w, api };
}

describe('the frame (AppFrame.vue)', () => {
  it('puts the sandbox on the element before its address and before it is in the document', async () => {
    const order: string[] = [];
    const set = Element.prototype.setAttribute;
    vi.spyOn(Element.prototype, 'setAttribute').mockImplementation(function (this: Element, name: string, value: string) {
      if (this.tagName === 'IFRAME') order.push(`${name}${this.isConnected ? '@connected' : ''}`);
      return set.call(this, name, value);
    });
    mountFrame();
    await settle();
    const f = document.querySelector('iframe')!;
    expect(f).toBeTruthy();
    expect(order.indexOf('sandbox')).toBeGreaterThanOrEqual(0);
    expect(order.indexOf('sandbox')).toBeLessThan(order.indexOf('src'));
    expect(order.filter((o) => o.endsWith('@connected'))).toEqual([]);
    expect(f.getAttribute('sandbox')).toBe('allow-scripts');
    expect(f.getAttribute('referrerpolicy')).toBe('no-referrer');
    expect(f.hasAttribute('allow')).toBe(false);
    expect(f.getAttribute('src')).toBe('about:blank#/_appui/sketch/0123456789abcdef/index.html');
  });

  it('answers the interface, deciding every call itself', async () => {
    const { w, api } = mountFrame();
    await settle();
    const f = document.querySelector('iframe') as HTMLIFrameElement;
    const win = fakeWindow();
    Object.defineProperty(f, 'contentWindow', { get: () => win });
    hello(win);
    const port = win.sent[0].ports[0];

    const s = await ask(port, 1, 'session.get');
    expect(s.result.app).toEqual({ name: 'sketch', version: '1.0.0' });
    expect(s.result.files).toEqual([{ index: 0, name: 'doc.sketch', ext: 'sketch', size: 9, mime: '', readOnly: false }]);
    // The interface is never told a storage path.
    expect(JSON.stringify(s.result)).not.toContain('main://');

    expect((await ask(port, 2, 'file.read', { as: 'text' })).result.text).toBe('file body');
    expect((await ask(port, 3, 'file.read', { index: 5, as: 'text' })).error.code).toBe('not_found');

    const saved = await ask(port, 4, 'file.save', { data: 'v2' });
    expect(saved.result).toEqual({ saved: true, size: 2 });
    expect(api.pluginUISave).toHaveBeenCalledWith('sketch', 'editor', { path: 'main://doc.sketch' }, expect.any(Blob));

    expect((await ask(port, 5, 'engine.call', { method: 'x' })).error.code).toBe('unavailable');
    expect((await ask(port, 6, 'job.submit', { action: 'x' })).error.code).toBe('unavailable');

    await ask(port, 7, 'ui.dirty', { dirty: true });
    expect(w.emitted('dirty')?.at(-1)).toEqual([true]);
  });

  it('refuses what the app was not granted, and saves over nothing a draft does not name', async () => {
    const { api } = mountFrame({
      ui: { ...UI, grants: ['ui'] },
    });
    await settle();
    const f = document.querySelector('iframe') as HTMLIFrameElement;
    const win = fakeWindow();
    Object.defineProperty(f, 'contentWindow', { get: () => win });
    hello(win);
    const port = win.sent[0].ports[0];
    expect((await ask(port, 1, 'file.read', { as: 'text' })).error.code).toBe('not_granted');
    expect((await ask(port, 2, 'file.save', { data: 'x' })).error.code).toBe('not_granted');
    expect(api.fetchResponse).not.toHaveBeenCalled();
    expect(api.pluginUISave).not.toHaveBeenCalled();
  });

  it('writes a draft where the host says, and refuses a view-only opening', async () => {
    const { api } = mountFrame({ savePath: 'main://.filex-drafts/1/abcdef12/doc.sketch' });
    await settle();
    let f = document.querySelector('iframe') as HTMLIFrameElement;
    let win = fakeWindow();
    Object.defineProperty(f, 'contentWindow', { get: () => win });
    hello(win);
    await ask(win.sent[0].ports[0], 1, 'file.save', { data: 'x' });
    expect(api.pluginUISave).toHaveBeenCalledWith('sketch', 'editor', { path: 'main://.filex-drafts/1/abcdef12/doc.sketch' }, expect.any(Blob));

    unmountAll();
    const ro = mountFrame({ readOnly: true });
    await settle();
    f = document.querySelector('iframe') as HTMLIFrameElement;
    win = fakeWindow();
    Object.defineProperty(f, 'contentWindow', { get: () => win });
    hello(win);
    expect((await ask(win.sent[0].ports[0], 1, 'file.save', { data: 'x' })).error.code).toBe('read_only');
    expect(ro.api.pluginUISave).not.toHaveBeenCalled();
  });
});

// ── what needs the person (security review UI-5, UI-10, UI-14) ─────────────

/** Connect to a mounted frame; the port the interface would hold. */
async function connectFrame(props: Record<string, unknown> = {}) {
  const m = mountFrame(props);
  await settle();
  const f = document.querySelector('iframe') as HTMLIFrameElement;
  const win = fakeWindow();
  Object.defineProperty(f, 'contentWindow', { get: () => win });
  hello(win);
  return { ...m, port: win.sent[0].ports[0] };
}

/** The consent row's Allow answers once armed (AppFrame CONSENT_ARM_MS). */
const armed = () => new Promise((r) => setTimeout(r, 650));

/** navigator.userActivation as the host page sees it. */
function activation(active: boolean) {
  Object.defineProperty(navigator, 'userActivation', { configurable: true, get: () => ({ isActive: active, hasBeenActive: active }) });
}

describe('what an interface may do only for the person', () => {
  afterEach(() => {
    delete (navigator as unknown as Record<string, unknown>).userActivation;
  });

  it('copies to the clipboard on a gesture, once, and otherwise asks in filex\u2019s own words (UI-10)', async () => {
    const writeText = vi.fn(async () => undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, get: () => ({ writeText }) });
    const { port } = await connectFrame({ title: 'Sketch' });

    // No gesture: filex asks, naming the app; "no" is cancelled.
    activation(false);
    let answer = ask(port, 1, 'clipboard.write', { text: 'secret' });
    await settle();
    const bar = document.querySelector('[data-testid="appframe-consent"]');
    expect(bar?.textContent).toContain('Sketch');
    expect(bar?.textContent).toContain('clipboard');
    (document.querySelector('[data-testid="appframe-consent-deny"]') as HTMLButtonElement).click();
    expect((await answer).error.code).toBe('cancelled');
    expect(writeText).not.toHaveBeenCalled();

    // "Allow": written.
    answer = ask(port, 2, 'clipboard.write', { text: 'ok' });
    await settle();
    await armed();
    (document.querySelector('[data-testid="appframe-consent-allow"]') as HTMLButtonElement).click();
    expect((await answer).result).toBe(null);
    expect(writeText).toHaveBeenCalledWith('ok');

    // A gesture: written at once — and one gesture is ONE call.
    activation(true);
    expect((await ask(port, 3, 'clipboard.write', { text: 'a' })).result).toBe(null);
    expect(writeText).toHaveBeenLastCalledWith('a');
    answer = ask(port, 4, 'clipboard.write', { text: 'b' });
    await settle();
    expect(document.querySelector('[data-testid="appframe-consent"]')).toBeTruthy();
    (document.querySelector('[data-testid="appframe-consent-deny"]') as HTMLButtonElement).click();
    expect((await answer).error.code).toBe('cancelled');
    expect(writeText).toHaveBeenCalledTimes(2);
  });

  it('arms Allow only after the question has been on screen a moment', async () => {
    const writeText = vi.fn(async () => undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, get: () => ({ writeText }) });
    const { port } = await connectFrame({ title: 'Sketch' });
    activation(false);
    const answer = ask(port, 1, 'clipboard.write', { text: 'x' });
    await settle();
    const allow = document.querySelector('[data-testid="appframe-consent-allow"]') as HTMLButtonElement;
    expect(allow.disabled, 'a click meant for the interface cannot land on Allow').toBe(true);
    allow.click();
    await settle();
    expect(writeText).not.toHaveBeenCalled();
    expect(document.querySelector('[data-testid="appframe-consent"]'), 'still asking').toBeTruthy();
    await armed();
    expect(allow.disabled).toBe(false);
    allow.click();
    expect((await answer).result).toBe(null);
    expect(writeText).toHaveBeenCalledWith('x');
  });

  it('asks when the browser refuses the page a write on the frame’s gesture (WebKit)', async () => {
    const writeText = vi
      .fn(async (_t: string) => undefined)
      .mockImplementationOnce(async () => {
        throw new DOMException('not allowed', 'NotAllowedError');
      });
    Object.defineProperty(navigator, 'clipboard', { configurable: true, get: () => ({ writeText }) });
    const { port } = await connectFrame({ title: 'Sketch' });
    activation(true);
    const answer = ask(port, 1, 'clipboard.write', { text: 'y' });
    await settle();
    expect(document.querySelector('[data-testid="appframe-consent"]'), 'the person is asked').toBeTruthy();
    await armed();
    (document.querySelector('[data-testid="appframe-consent-allow"]') as HTMLButtonElement).click();
    expect((await answer).result).toBe(null);
    expect(writeText).toHaveBeenCalledTimes(2);
  });

  it('does not take a gesture on filex’s own page for one in the interface', async () => {
    const writeText = vi.fn(async () => undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, get: () => ({ writeText }) });
    // The double-click that opened the app left the page active.
    activation(true);
    const { port } = await connectFrame({ title: 'Sketch' });
    let answer = ask(port, 1, 'clipboard.write', { text: 'at once' });
    await settle();
    expect(document.querySelector('[data-testid="appframe-consent"]'), 'the opening gesture is spent').toBeTruthy();
    (document.querySelector('[data-testid="appframe-consent-deny"]') as HTMLButtonElement).click();
    expect((await answer).error.code).toBe('cancelled');

    // A click on filex's own page (its toolbar, its Save) is not one either.
    vi.useFakeTimers({ toFake: ['Date'] });
    try {
      vi.setSystemTime(Date.now() + 6000);
      window.dispatchEvent(new Event('pointerdown'));
      answer = ask(port, 2, 'clipboard.write', { text: 'after a host click' });
      await settle();
      expect(document.querySelector('[data-testid="appframe-consent"]'), 'a host click is filex’s').toBeTruthy();
      (document.querySelector('[data-testid="appframe-consent-deny"]') as HTMLButtonElement).click();
      expect((await answer).error.code).toBe('cancelled');

      // Later, an activation with no click of filex's own came from the frame.
      vi.setSystemTime(Date.now() + 6000);
      expect((await ask(port, 3, 'clipboard.write', { text: 'in the frame' })).result).toBe(null);
    } finally {
      vi.useRealTimers();
    }
    expect(writeText).toHaveBeenCalledTimes(1);
    expect(writeText).toHaveBeenCalledWith('in the frame');
  });

  it('starts a job only on a gesture or after the person says yes (UI-5)', async () => {
    const { api, port } = await connectFrame({ title: 'Sketch', ui: { ...UI, engine: true } });
    activation(false);
    let answer = ask(port, 1, 'job.submit', { action: 'export' });
    await settle();
    const bar = document.querySelector('[data-testid="appframe-consent"]');
    expect(bar?.textContent).toContain('Sketch');
    expect(bar?.textContent).toContain('export');
    (document.querySelector('[data-testid="appframe-consent-deny"]') as HTMLButtonElement).click();
    expect((await answer).error.code).toBe('cancelled');
    expect(api.pluginActionRun).not.toHaveBeenCalled();

    activation(true);
    answer = ask(port, 2, 'job.submit', { action: 'export' });
    expect((await answer).result).toEqual({ op: { id: 9 } });
    expect(api.pluginActionRun).toHaveBeenCalledTimes(1);
  });

  it('never has two questions on screen, and a closing frame takes its question away', async () => {
    const writeText = vi.fn(async () => undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, get: () => ({ writeText }) });
    const { w, port } = await connectFrame();
    activation(false);
    const first = ask(port, 1, 'clipboard.write', { text: 'a' });
    await settle();
    expect((await ask(port, 2, 'clipboard.write', { text: 'b' })).error.code).toBe('unavailable');
    w.unmount();
    // The port is closed with the frame: the question is gone, nothing written.
    expect(document.querySelector('[data-testid="appframe-consent"]')).toBeNull();
    expect(writeText).not.toHaveBeenCalled();
    void first;
  });

  it('says whose toast and whose question it is (UI-14)', async () => {
    const { w, port } = await connectFrame({ title: 'Sketch' });
    await ask(port, 1, 'ui.toast', { text: 'Your session expired, sign in again' });
    expect(w.emitted('toast')?.at(-1)).toEqual([{ text: 'Sketch: Your session expired, sign in again', tone: 'info' }]);
    const q = ask(port, 2, 'ui.confirm', { title: 'filex', text: 'Delete everything?' });
    await settle();
    expect(document.body.textContent).toContain('Sketch: filex');
    void q;
  });

  it('names the app when the label does not (a label cannot pass for filex)', async () => {
    const { w, port } = await connectFrame({ title: 'filex' });
    await ask(port, 1, 'ui.toast', { text: 'Sign in again' });
    expect(w.emitted('toast')?.at(-1)).toEqual([{ text: 'filex (sketch): Sign in again', tone: 'info' }]);
  });
});

describe('a frame that asks too much (UI-14)', () => {
  it('answers busy past the in-flight cap and past the rate', async () => {
    const { BRIDGE_CAPS } = await import('@brftech/filex-core/src/lib/appBridge');
    const mine = fakeWindow();
    const frame = { contentWindow: mine } as unknown as HTMLIFrameElement;
    const never = () => new Promise(() => undefined);
    const b = createAppBridge({ frame: () => frame, handlers: { 'engine.call': never, 'session.get': () => 1 } });
    hello(mine);
    const port = mine.sent[0].ports[0];
    const answers: any[] = [];
    port.onmessage = (ev) => answers.push(ev.data);
    for (let i = 0; i < BRIDGE_CAPS.inFlight + 3; i++) port.postMessage({ id: i + 1, method: 'engine.call', params: {} });
    await settle();
    expect(answers).toHaveLength(3);
    expect(answers.every((a) => a.error?.code === 'unavailable' && a.error?.message === 'busy')).toBe(true);
    b.destroy();

    const mine2 = fakeWindow();
    const frame2 = { contentWindow: mine2 } as unknown as HTMLIFrameElement;
    const b2 = createAppBridge({ frame: () => frame2, handlers: { 'session.get': () => 1 } });
    hello(mine2);
    const port2 = mine2.sent[0].ports[0];
    const got: any[] = [];
    port2.onmessage = (ev) => got.push(ev.data);
    for (let i = 0; i < BRIDGE_CAPS.perWindow + 5; i++) port2.postMessage({ id: i + 1, method: 'session.get' });
    await settle();
    expect(got.filter((a) => a.result === 1)).toHaveLength(BRIDGE_CAPS.perWindow);
    expect(got.filter((a) => a.error?.message === 'busy')).toHaveLength(5);
    b2.destroy();
  });
});

// ── ui.download: a file for the person's own disk (the maintainer, 2026-09-27) ──────
//
// Through filex, never from the frame (a sandboxed frame cannot download);
// only with the grant (`ui:download`), only on a gesture or the person's yes,
// never past LIMITS.maxDownloadBytes. Chromium streams it to the file the
// person picks (File System Access); the others get a Blob.

describe('ui.download', () => {
  const DL = { ...UI, grants: ['files:read', 'files:write', 'ui', 'ui:download'] };
  afterEach(() => {
    delete (navigator as unknown as Record<string, unknown>).userActivation;
    delete (window as unknown as Record<string, unknown>).showSaveFilePicker;
  });

  /** One frame on the page at a time: the helpers find THE iframe. */
  function clearFrames() {
    unmountAll();
  }

  /** Ask with the data transferred, as the SDK sends a stream. */
  function askT(port: MessagePort, id: number, params: { name: string; data: unknown }): Promise<any> {
    return new Promise((resolve) => {
      port.onmessage = (ev) => {
        if (ev.data?.id === id) resolve(ev.data);
      };
      const transfer = params.data instanceof ReadableStream ? [params.data as unknown as Transferable] : [];
      port.postMessage({ id, method: 'ui.download', params }, transfer);
    });
  }

  function blobCapture() {
    const got: { blob?: Blob; name?: string; clicks: number } = { clicks: 0 };
    vi.spyOn(URL, 'createObjectURL').mockImplementation((b: Blob | MediaSource) => {
      got.blob = b as Blob;
      return 'blob:filex/1';
    });
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => undefined);
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
      got.name = this.download;
      got.clicks++;
    });
    return got;
  }

  function chunks(n: number, size: number) {
    const piece = new Uint8Array(size);
    let i = 0;
    return new ReadableStream<Uint8Array>({
      pull(c) {
        if (i++ < n) c.enqueue(piece);
        else c.close();
      },
    });
  }

  it('is refused without the grant, and for a name that is not one', async () => {
    const got = blobCapture();
    const { port } = await connectFrame({ title: 'Sketch' });
    activation(true);
    expect((await ask(port, 1, 'ui.download', { name: 'a.txt', data: 'x' })).error.code).toBe('not_granted');
    clearFrames();
    const { port: p2 } = await connectFrame({ title: 'Sketch', ui: DL });
    for (const name of ['', '../a.txt', 'a/b.txt', 'a\\b.txt', '..', 'x'.repeat(256)]) {
      expect((await ask(p2, 2, 'ui.download', { name, data: 'x' })).error.code, name).toBe('invalid');
    }
    expect(got.clicks).toBe(0);
  });

  it('asks the person without a gesture; "no" writes nothing, "yes" hands over a Blob', async () => {
    const got = blobCapture();
    const { port } = await connectFrame({ title: 'Sketch', ui: DL });
    activation(false);
    let answer = ask(port, 1, 'ui.download', { name: 'plan.drawio', data: '<mxfile/>' });
    await settle();
    const bar = document.querySelector('[data-testid="appframe-consent"]');
    expect(bar?.textContent).toContain('Sketch');
    expect(bar?.textContent).toContain('plan.drawio');
    (document.querySelector('[data-testid="appframe-consent-deny"]') as HTMLButtonElement).click();
    expect((await answer).error.code).toBe('cancelled');
    expect(got.clicks).toBe(0);

    answer = ask(port, 2, 'ui.download', { name: 'plan.drawio', data: '<mxfile/>' });
    await settle();
    await armed();
    (document.querySelector('[data-testid="appframe-consent-allow"]') as HTMLButtonElement).click();
    expect((await answer).result).toEqual({ saved: true, size: 9 });
    expect(got.clicks).toBe(1);
    expect(got.name).toBe('plan.drawio');
    expect(got.blob?.size).toBe(9);
  });

  it('streams to the file the person picks where the browser can (File System Access)', async () => {
    const got = blobCapture();
    const written: number[] = [];
    const writable = { write: vi.fn(async (c: Uint8Array) => void written.push(c.byteLength)), close: vi.fn(async () => undefined), abort: vi.fn(async () => undefined) };
    const picker = vi.fn(async () => ({ createWritable: async () => writable }));
    (window as unknown as Record<string, unknown>).showSaveFilePicker = picker;
    const { port } = await connectFrame({ title: 'Sketch', ui: DL });
    activation(true);
    const r = await askT(port, 1, { name: 'big.bin', data: chunks(3, 1024) });
    expect(r.result).toEqual({ saved: true, size: 3072 });
    expect(picker).toHaveBeenCalledWith(expect.objectContaining({ suggestedName: 'big.bin' }));
    expect(written).toEqual([1024, 1024, 1024]);
    expect(writable.close).toHaveBeenCalled();
    expect(got.clicks, 'no Blob where the stream went to disk').toBe(0);

    // The person closes the picker: cancelled.
    picker.mockImplementationOnce(async () => {
      throw new DOMException('closed', 'AbortError');
    });
    clearFrames();
    // A live activation at mount is the one that opened the frame (spent):
    // mount without one, then the gesture.
    activation(false);
    const { port: p2 } = await connectFrame({ title: 'Sketch', ui: DL });
    activation(true);
    expect((await ask(p2, 1, 'ui.download', { name: 'big.bin', data: 'x' })).error.code).toBe('cancelled');
  });

  it('never goes past the limit, streamed or not', async () => {
    const { LIMITS } = await import('../../../packages/app-ui/src/protocol');
    const got = blobCapture();
    const mib = 1 << 20;
    const over = Math.floor(LIMITS.maxDownloadBytes / mib) + 1;
    const { port } = await connectFrame({ title: 'Sketch', ui: DL });
    activation(true);
    expect((await askT(port, 1, { name: 'huge.bin', data: chunks(over, mib) })).error.code).toBe('too_large');
    expect(got.clicks).toBe(0);

    const writable = { write: vi.fn(async () => undefined), close: vi.fn(async () => undefined), abort: vi.fn(async () => undefined) };
    (window as unknown as Record<string, unknown>).showSaveFilePicker = vi.fn(async () => ({ createWritable: async () => writable }));
    clearFrames();
    // A live activation at mount is the one that opened the frame (spent):
    // mount without one, then the gesture.
    activation(false);
    const { port: p2 } = await connectFrame({ title: 'Sketch', ui: DL });
    activation(true);
    expect((await askT(p2, 1, { name: 'huge.bin', data: chunks(over, mib) })).error.code).toBe('too_large');
    expect(writable.abort).toHaveBeenCalled();
    expect(writable.close).not.toHaveBeenCalled();
  });
});

// ── what an interface is told when something fails (security review UI-15) ─

describe('a failure, as the interface hears it', () => {
  it("is a code and a word of filex's own, never the server's or the browser's text", async () => {
    const { requestFailure } = await import('@brftech/filex-core/src/lib/errorWords');
    const fail = (status: number, body: object) => requestFailure(status, JSON.stringify(body), 'en');
    const answers = [
      fail(500, { error: 'save failed', message: 'open /srv/filex/data/doc.sketch: permission denied' }),
      fail(507, { error: 'quota_exceeded', message: 'there is not enough room left in your quota' }),
      fail(404, { error: 'not found', message: 'file main://secret/plan.sketch' }),
      fail(409, { error: 'read_only', message: 'this storage is read-only' }),
      fail(413, { error: 'too_large' }),
      fail(422, { error: 'not_applicable', message: 'this app does not save this kind of file' }),
      fail(403, { error: 'encrypted', message: 'an app cannot write into an encrypted folder' }),
      new Error('internal: /home/filex/.cache/x failed'),
    ];
    let i = 0;
    const { api } = mountFrame();
    api.pluginUISave.mockImplementation(async () => {
      throw answers[i++];
    });
    await settle();
    const f = document.querySelector('iframe') as HTMLIFrameElement;
    const win = fakeWindow();
    Object.defineProperty(f, 'contentWindow', { get: () => win });
    hello(win);
    const port = win.sent[0].ports[0];
    const got: Array<{ code: string; message: string }> = [];
    for (let n = 0; n < answers.length; n++) got.push((await ask(port, 10 + n, 'file.save', { data: 'x' })).error);
    expect(got).toEqual([
      { code: 'failed', message: 'failed' },
      { code: 'failed', message: 'quota_exceeded' },
      { code: 'not_found', message: 'not_found' },
      { code: 'read_only', message: 'read_only' },
      { code: 'too_large', message: 'too_large' },
      { code: 'invalid', message: 'not_applicable' },
      { code: 'failed', message: 'encrypted' },
      { code: 'failed', message: 'failed' },
    ]);
    expect(JSON.stringify(got)).not.toMatch(/srv|home|main:\/\/|permission denied/);
  });
});

// ── a large save goes in chunks (useFileApi.pluginUISave) ──────────────────

describe('an interface saving a large file', () => {
  it('sends it in 8 MiB chunks, the last marked final, and streams a stream as it is read', async () => {
    const { useFileApi, UI_SAVE_CHUNK } = await import('@brftech/filex-core/src/composables/useFileApi');
    const calls: Array<{ url: string; size: number }> = [];
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const u = String(input);
      const size = init?.body instanceof Blob ? init.body.size : 0;
      calls.push({ url: u, size });
      const received = calls.reduce((n, c) => n + c.size, 0);
      const final = u.includes('final=1') || !u.includes('chunk=start') && !u.includes('session=');
      return new Response(JSON.stringify(final ? { saved: true, path: 'main://big.sketch', name: 'big.sketch', size: received } : { session: 'S', received }), {
        status: final ? 200 : 202,
        headers: { 'content-type': 'application/json' },
      });
    });
    const api = useFileApi({ apiBase: '' } as never);

    const blob = new Blob([new Uint8Array(UI_SAVE_CHUNK * 2 + 5)]);
    const r = await api.pluginUISave('sketch', 'editor', { path: 'main://big.sketch' }, blob);
    expect(r.size).toBe(UI_SAVE_CHUNK * 2 + 5);
    expect(calls.map((c) => c.size)).toEqual([UI_SAVE_CHUNK, UI_SAVE_CHUNK, 5]);
    expect(calls[0].url).toContain('chunk=start');
    expect(calls[1].url).toContain(`session=S&offset=${UI_SAVE_CHUNK}`);
    expect(calls[2].url).toContain('final=1');

    calls.length = 0;
    const piece = 6 << 20;
    let sent = 0;
    const stream = new ReadableStream<Uint8Array>({
      pull(c) {
        if (sent++ < 3) c.enqueue(new Uint8Array(piece));
        else c.close();
      },
    });
    await api.pluginUISave('sketch', 'editor', { path: 'main://big.sketch' }, stream);
    expect(calls.map((c) => c.size)).toEqual([UI_SAVE_CHUNK, UI_SAVE_CHUNK, 3 * piece - 2 * UI_SAVE_CHUNK]);
    expect(calls.at(-1)!.url).toContain('final=1');

    calls.length = 0;
    await api.pluginUISave('sketch', 'editor', { path: 'main://small.sketch' }, new Blob(['tiny']));
    expect(calls).toHaveLength(1);
    expect(calls[0].url).not.toContain('chunk=');
  });
});
