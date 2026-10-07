// The ONLYOFFICE editor in a frame of its own (task #92).
//
// When the server serves a page for the editor on another origin - the
// document server's own (FILEX_ONLYOFFICE_FRAME_ORIGIN, /filex-frame/editor)
// or the app-interface origin (FILEX_APP_UI_ORIGIN) - its editor config names
// that page (`frame`), and the viewer runs the document server's api.js there
// instead of in its own page: api.js then has no way to the bearer in
// sessionStorage, to filex's API as the person, or to the page. What crosses
// is narrow, and both halves are held to it here:
//
//  - the page's half (core lib/officeFrame): a hello only from the frame
//    element it drew, from the origin its address names, with the session it
//    put in that address, once; the config handed over exactly as the server
//    signed it, to that origin by name, with one port; only the protocol's
//    events taken from the port;
//  - the frame's half (backend/internal/onlyoffice/frame/frame.js, run here
//    against stand-ins for window, document and location): an open only from
//    its parent, only with its session, only once, and the editor's events
//    over that port and nowhere else;
//  - the viewer (PreviewModal) uses the frame whenever the server names one:
//    no api.js in the page, the frame built sandbox-first, an editor that
//    cannot go on closed, a warning left to the document server.
//
// Every test here is red before #92: none of it existed.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';

import PreviewModal from '@brftech/filex-core/src/modals/PreviewModal.vue';
import {
  OFFICE_FRAME_ALLOW,
  OFFICE_FRAME_SANDBOX,
  buildOfficeFrame,
  frameOriginOf,
  frameSetting,
  linkOfficeFrame,
  newFrameSession,
  parseFrameEvent,
  parseFrameHello,
  resetEditorInPageWarning,
  warnEditorInPage,
} from '@brftech/filex-core/src/lib/officeFrame';
import { en } from '@brftech/filex-core/src/locales/en';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';
import {
  DS_FRAME_URL,
  DS_ORIGIN,
  FRAME_URL,
  UI_ORIGIN,
  installFramed,
  installInPage,
  type OfficeEditors,
} from '../helpers/officeEditors';

const SESSION = 'abcdefghijklmnopqrstuv';

const settle = async () => {
  for (let i = 0; i < 4; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
  }
};
const portTurn = () => new Promise((r) => setTimeout(r, 10));

/** A window-like the frame "is": what it was sent. */
function fakeFrame() {
  const sent: Array<{ data: Record<string, unknown>; origin: string; ports: MessagePort[] }> = [];
  const cw = {
    postMessage(data: Record<string, unknown>, origin: string, transfer?: Transferable[]) {
      sent.push({ data, origin, ports: (transfer ?? []) as MessagePort[] });
    },
  };
  return { el: { contentWindow: cw } as unknown as HTMLIFrameElement, cw, sent };
}

function hello(source: unknown, over: { origin?: string; data?: unknown } = {}) {
  window.dispatchEvent(
    new MessageEvent('message', {
      // `in`, not `??`: a null payload is a payload to refuse, not "the default".
      data: 'data' in over ? over.data : { proto: 'filex-oo', v: 1, type: 'hello', session: SESSION },
      origin: over.origin ?? UI_ORIGIN,
      source: source as Window,
    }),
  );
}

// ── the page's half: lib/officeFrame ─────────────────────────────────────

describe('the page takes the frame (lib/officeFrame)', () => {
  const links: Array<{ close(): void }> = [];
  afterEach(() => links.splice(0).forEach((l) => l.close()));

  function link(over: Partial<Parameters<typeof linkOfficeFrame>[0]> = {}) {
    const f = fakeFrame();
    const events: unknown[] = [];
    const config = { document: { key: 'k1', url: 'https://files.example.com/api/files/onlyoffice/fetch?n=1' }, token: 'signed' };
    const l = linkOfficeFrame({
      frame: () => f.el,
      origin: UI_ORIGIN,
      session: SESSION,
      config,
      onEvent: (e) => events.push(e),
      ...over,
    });
    links.push(l);
    return { f, l, events, config };
  }

  it('hands the signed config over once, to the frame it drew, at the frame\'s origin, with one port', () => {
    const { f, l, config } = link();
    hello(f.cw);
    expect(f.sent).toHaveLength(1);
    const { data, origin, ports } = f.sent[0];
    expect(origin, 'posted to the frame\'s origin by name, never "*"').toBe(UI_ORIGIN);
    expect(data).toEqual({ proto: 'filex-oo', v: 1, type: 'open', session: SESSION, config });
    expect(data.config, 'the server\'s config itself, nothing added').toBe(config);
    expect(config).not.toHaveProperty('events');
    expect(ports).toHaveLength(1);
    expect(l.opened()).toBe(true);

    hello(f.cw);
    expect(f.sent, 'one hello per frame').toHaveLength(1);
  });

  it('refuses a hello from another window, another origin, another session, or not shaped like one', () => {
    const { f, l } = link();
    const sibling = fakeFrame();
    hello(sibling.cw);
    hello(f.cw, { origin: 'null' });
    hello(f.cw, { origin: 'https://evil.example' });
    hello(f.cw, { origin: 'http://localhost:3000' });
    // The same site as the frame (the document server beside it) is another
    // origin all the same.
    hello(f.cw, { origin: DS_ORIGIN });
    for (const data of [
      { proto: 'filex-oo', v: 1, type: 'hello', session: 'bcdefghijklmnopqrstuvw' },
      { proto: 'filex-oo', v: 2, type: 'hello', session: SESSION },
      { proto: 'filex', v: 1, type: 'hello', session: SESSION },
      { proto: 'filex-oo', v: 1, type: 'open', session: SESSION },
      { proto: 'filex-oo', v: 1, type: 'hello' },
      { proto: 'filex-oo', v: 1, type: 'hello', session: 'short' },
      { proto: 'filex-oo', v: 1, type: 'hello', session: SESSION + '"; x' },
      [{ proto: 'filex-oo', v: 1, type: 'hello', session: SESSION }],
      'hello',
      null,
    ]) {
      hello(f.cw, { data });
    }
    expect(f.sent).toEqual([]);
    expect(sibling.sent).toEqual([]);
    expect(l.opened()).toBe(false);
  });

  it('a frame that loaded a second document gets nothing, before its hello or after', () => {
    const before = link();
    before.l.frameLoaded();
    before.l.frameLoaded();
    hello(before.f.cw);
    expect(before.f.sent).toEqual([]);

    const after = link();
    after.l.frameLoaded();
    hello(after.f.cw);
    expect(after.l.opened()).toBe(true);
    after.l.frameLoaded();
    expect(after.l.opened(), 'the port is closed').toBe(false);
  });

  it('takes only the protocol\'s events from the port', async () => {
    const { f, events } = link();
    hello(f.cw);
    const port = f.sent[0].ports[0];
    port.postMessage({ proto: 'filex-oo', v: 1, type: 'state', dirty: true });
    port.postMessage({ proto: 'filex-oo', v: 1, type: 'error', channel: 'onError', code: -4, description: 'Download failed.' });
    port.postMessage({ proto: 'filex-oo', v: 1, type: 'state', dirty: 'yes' });
    port.postMessage({ proto: 'filex-oo', v: 1, type: 'save', path: 'depo://x' });
    port.postMessage({ proto: 'filex-oo', v: 1, type: 'error', channel: 'onEval', code: 1 });
    port.postMessage({ type: 'state', dirty: true });
    await portTurn();
    expect(events).toEqual([
      { type: 'state', dirty: true },
      { type: 'error', channel: 'onError', code: -4, description: 'Download failed.' },
    ]);
  });

  it('a frame that never says hello is called silent once; one that does, never', async () => {
    const silent = vi.fn();
    link({ onSilent: silent, helloMs: 20 });
    const spoke = vi.fn();
    const ok = link({ onSilent: spoke, helloMs: 20 });
    hello(ok.f.cw);
    await new Promise((r) => setTimeout(r, 60));
    expect(silent).toHaveBeenCalledTimes(1);
    expect(spoke).not.toHaveBeenCalled();
  });

  it('closed: no hello is taken', () => {
    const { f, l } = link();
    l.close();
    hello(f.cw);
    expect(f.sent).toEqual([]);
  });

  it('reads the shapes', () => {
    expect(parseFrameHello({ proto: 'filex-oo', v: 1, type: 'hello', session: SESSION })).toEqual({ session: SESSION });
    expect(parseFrameEvent({ proto: 'filex-oo', v: 1, type: 'ready' })).toEqual({ type: 'ready' });
    expect(parseFrameEvent({ proto: 'filex-oo', v: 1, type: 'failed', reason: 'script' })).toEqual({ type: 'failed', reason: 'script' });
    expect(parseFrameEvent({ proto: 'filex-oo', v: 1, type: 'failed', reason: 'other' })).toBeNull();
    expect(parseFrameEvent({ proto: 'filex-oo', v: 1, type: 'error', channel: 'onWarning', code: 'x', description: 7 })).toEqual({
      type: 'error',
      channel: 'onWarning',
      code: null,
      description: '',
    });
    expect(frameOriginOf(FRAME_URL)).toBe(UI_ORIGIN);
    expect(frameOriginOf(DS_FRAME_URL)).toBe(DS_ORIGIN);
    expect(frameSetting(FRAME_URL)).toBe('FILEX_APP_UI_ORIGIN');
    expect(frameSetting(DS_FRAME_URL)).toBe('FILEX_ONLYOFFICE_FRAME_ORIGIN');
    expect(frameOriginOf('javascript:alert(1)')).toBeNull();
    expect(frameOriginOf('/_appui/_onlyoffice/editor')).toBeNull();
    expect(frameOriginOf('https://user:pw@apps.usercontent.example/x')).toBeNull();
    const a = newFrameSession();
    expect(a).toMatch(/^[A-Za-z0-9_-]{22}$/);
    expect(newFrameSession()).not.toBe(a);
  });

  it('builds the frame sandbox first: flags, permissions and referrer policy before its address', () => {
    const order: string[] = [];
    const set = Element.prototype.setAttribute;
    const spy = vi.spyOn(Element.prototype, 'setAttribute').mockImplementation(function (this: Element, name: string, value: string) {
      if (this.tagName === 'IFRAME') order.push(name);
      return set.call(this, name, value);
    });
    const f = buildOfficeFrame(FRAME_URL, SESSION, 'Rapor.docx');
    spy.mockRestore();
    expect(f.isConnected, 'not in the document yet').toBe(false);
    expect(order.indexOf('sandbox')).toBe(0);
    expect(order.indexOf('src')).toBe(order.length - 1);
    expect(order.indexOf('allow')).toBeLessThan(order.indexOf('src'));
    expect(order.indexOf('referrerpolicy')).toBeLessThan(order.indexOf('src'));
    expect(f.getAttribute('sandbox')).toBe(OFFICE_FRAME_SANDBOX);
    expect(OFFICE_FRAME_SANDBOX.split(' ')).not.toContain('allow-top-navigation');
    expect(OFFICE_FRAME_SANDBOX.split(' ')).not.toContain('allow-popups-to-escape-sandbox');
    expect(f.getAttribute('allow')).toBe(OFFICE_FRAME_ALLOW);
    expect(OFFICE_FRAME_ALLOW).not.toMatch(/camera|microphone|display-capture|geolocation/);
    expect(f.getAttribute('referrerpolicy')).toBe('no-referrer');
    expect(f.getAttribute('src')).toBe(`${FRAME_URL}#${SESSION}`);
  });
});

// ── the frame's half: frame.js ───────────────────────────────────────────

const FRAME_JS = readFileSync(path.resolve(__dirname, '../../../backend/internal/onlyoffice/frame/frame.js'), 'utf8');
const API = 'https://docs.example.com/web-apps/apps/api/documents/api.js';

type Posted = Array<Record<string, unknown>>;

/** Run frame.js against stand-ins for window, document and location. */
function runFrame(opts: { hash?: string; topLevel?: boolean; api?: string } = {}) {
  const listeners: Array<(ev: unknown) => void> = [];
  const toParent: Array<{ data: Record<string, unknown>; origin: string }> = [];
  const parent = { postMessage: (data: Record<string, unknown>, origin: string) => toParent.push({ data, origin }) };
  const scripts: Array<{ src?: string; onload?: () => void; onerror?: () => void }> = [];
  const win: Record<string, unknown> = {
    addEventListener: (type: string, fn: (ev: unknown) => void) => {
      if (type === 'message') listeners.push(fn);
    },
  };
  win.parent = opts.topLevel ? win : parent;
  const doc = {
    documentElement: { getAttribute: (n: string) => (n === 'data-api' ? (opts.api ?? API) : null) },
    createElement: () => ({}),
    head: { appendChild: (el: { src?: string }) => scripts.push(el) },
  };
  const loc = { hash: opts.hash ?? '#' + SESSION };
  // eslint-disable-next-line @typescript-eslint/no-implied-eval
  new Function('window', 'document', 'location', FRAME_JS)(win, doc, loc);
  const port = { posted: [] as Posted, postMessage(m: Record<string, unknown>) { this.posted.push(m); } };
  const open = (over: Record<string, unknown> = {}) => ({ proto: 'filex-oo', v: 1, type: 'open', session: SESSION, config: { document: { key: 'k1' }, token: 'signed' }, ...over });
  const deliver = (ev: { source: unknown; data: unknown; ports?: unknown[] }) => listeners.forEach((fn) => fn(ev));
  return { win, parent, toParent, scripts, listeners, port, open, deliver };
}

describe('the frame takes the page (frame.js)', () => {
  it('says hello to its parent, with the session from its own address, and nothing else', () => {
    const fx = runFrame();
    expect(fx.toParent).toEqual([{ data: { proto: 'filex-oo', v: 1, type: 'hello', session: SESSION }, origin: '*' }]);
    expect(fx.scripts, 'no api.js before the page opens a document').toEqual([]);
  });

  it('opened on its own, or with no session of a page\'s: says nothing, listens to nothing', () => {
    for (const fx of [runFrame({ topLevel: true }), runFrame({ hash: '' }), runFrame({ hash: '#short' }), runFrame({ api: '' })]) {
      expect(fx.toParent).toEqual([]);
      expect(fx.listeners).toEqual([]);
    }
  });

  it('takes the open only from its parent, only with its session, only well formed, only once', () => {
    const fx = runFrame();
    const sibling = { postMessage: () => undefined };
    fx.deliver({ source: sibling, data: fx.open(), ports: [fx.port] });
    fx.deliver({ source: fx.parent, data: fx.open({ session: 'bcdefghijklmnopqrstuvw' }), ports: [fx.port] });
    fx.deliver({ source: fx.parent, data: fx.open({ proto: 'filex' }), ports: [fx.port] });
    fx.deliver({ source: fx.parent, data: fx.open({ v: 2 }), ports: [fx.port] });
    fx.deliver({ source: fx.parent, data: fx.open({ type: 'hello' }), ports: [fx.port] });
    fx.deliver({ source: fx.parent, data: fx.open({ config: [1, 2] }), ports: [fx.port] });
    fx.deliver({ source: fx.parent, data: fx.open({ config: 'x' }), ports: [fx.port] });
    fx.deliver({ source: fx.parent, data: fx.open(), ports: [] });
    fx.deliver({ source: fx.parent, data: fx.open(), ports: [fx.port, fx.port] });
    expect(fx.scripts).toEqual([]);

    fx.deliver({ source: fx.parent, data: fx.open(), ports: [fx.port] });
    expect(fx.scripts).toHaveLength(1);
    expect(fx.scripts[0].src, 'the document server the server named, never one the page names').toBe(API);
    fx.deliver({ source: fx.parent, data: fx.open({ config: { other: true } }), ports: [fx.port] });
    expect(fx.scripts, 'once').toHaveLength(1);
  });

  it('opens the editor on the signed config, and says what it does over the port alone', () => {
    const fx = runFrame();
    fx.deliver({ source: fx.parent, data: fx.open(), ports: [fx.port] });
    const made: Array<{ id: string; cfg: Record<string, any> }> = [];
    fx.win.DocsAPI = {
      DocEditor: class {
        constructor(id: string, cfg: Record<string, any>) {
          made.push({ id, cfg });
        }
      },
    };
    fx.scripts[0].onload!();
    expect(made).toHaveLength(1);
    expect(made[0].id).toBe('filex-oo-editor');
    expect(made[0].cfg.token).toBe('signed');
    expect(made[0].cfg.document).toEqual({ key: 'k1' });
    expect(Object.keys(made[0].cfg.events).sort()).toEqual(['onDocumentReady', 'onDocumentStateChange', 'onError', 'onWarning']);

    const ev = made[0].cfg.events;
    ev.onDocumentReady();
    ev.onDocumentStateChange({ data: true });
    ev.onDocumentStateChange({ data: false });
    ev.onError({ data: { errorCode: -4, errorDescription: 'Download failed.' } });
    ev.onWarning({ data: {} });
    expect(fx.port.posted).toEqual([
      { proto: 'filex-oo', v: 1, type: 'started' },
      { proto: 'filex-oo', v: 1, type: 'ready' },
      { proto: 'filex-oo', v: 1, type: 'state', dirty: true },
      { proto: 'filex-oo', v: 1, type: 'state', dirty: false },
      { proto: 'filex-oo', v: 1, type: 'error', channel: 'onError', code: -4, description: 'Download failed.' },
      { proto: 'filex-oo', v: 1, type: 'error', channel: 'onWarning', code: null, description: '' },
    ]);
    expect(fx.toParent, 'nothing but the hello ever goes to the parent window').toHaveLength(1);
  });

  it('api.js that did not load, or defined no editor: says so', () => {
    const noScript = runFrame();
    noScript.deliver({ source: noScript.parent, data: noScript.open(), ports: [noScript.port] });
    noScript.scripts[0].onerror!();
    expect(noScript.port.posted).toEqual([{ proto: 'filex-oo', v: 1, type: 'failed', reason: 'script' }]);

    const noApi = runFrame();
    noApi.deliver({ source: noApi.parent, data: noApi.open(), ports: [noApi.port] });
    noApi.scripts[0].onload!();
    expect(noApi.port.posted).toEqual([{ proto: 'filex-oo', v: 1, type: 'failed', reason: 'api' }]);
  });
});

// ── the viewer: PreviewModal ─────────────────────────────────────────────

const docx = {
  path: 'depo://rapor.docx',
  basename: 'rapor.docx',
  type: 'file',
  extension: 'docx',
  size: 38_000,
  last_modified: 1_757_376_000,
} as FileNode;

const CONFIG = '/api/files/onlyoffice/config';
const SERVER_CONFIG = { document: { key: 'k-1', url: 'https://files.example.com/api/files/onlyoffice/fetch?n=1&sig=x' }, token: 'signed.jwt' };

describe('the viewer runs the editor in the frame the server names (PreviewModal)', () => {
  let editors: OfficeEditors;
  let requested: string[] = [];
  let withFrame = true;
  let frameUrl = FRAME_URL;
  const opened: VueWrapper[] = [];

  beforeEach(() => {
    requested = [];
    withFrame = true;
    frameUrl = FRAME_URL;
    resetEditorInPageWarning();
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        requested.push(url);
        if (url === CONFIG) {
          return {
            ok: true,
            status: 200,
            json: async () => ({
              documentServerUrl: 'https://docs.example.com',
              config: structuredClone(SERVER_CONFIG),
              ...(withFrame ? { frame: frameUrl } : {}),
            }),
            text: async () => '',
          };
        }
        return { ok: true, status: 200, json: async () => ({ verdict: 'not_requested', scope: 'this_process' }), text: async () => '' };
      }),
    );
  });
  afterEach(() => {
    opened.splice(0).forEach((w) => w.unmount());
    editors?.uninstall();
    vi.unstubAllGlobals();
  });

  async function open(canConfigure = false) {
    const w = mount(PreviewModal, {
      attachTo: document.body,
      props: {
        open: true,
        locale: 'en',
        file: docx,
        previewUrl: (p: string) => `/preview?path=${p}`,
        downloadUrl: (p: string) => `/download?path=${p}`,
        onlyOfficeBase: 'https://docs.example.com',
        onlyOfficeConfigEndpoint: CONFIG,
        canConfigure,
      },
    });
    opened.push(w);
    await settle();
    return w;
  }

  it('no api.js in this page: the frame, sandbox and all, in the mount, given the config as the server signed it', async () => {
    editors = installFramed();
    const warn = vi.spyOn(console, 'warn');
    await open();
    expect(document.getElementById('fe-onlyoffice-api-js'), 'api.js is never loaded into the page').toBeNull();
    expect(document.querySelector('script[src*="api.js"]')).toBeNull();
    expect((window as unknown as { DocsAPI?: unknown }).DocsAPI).toBeUndefined();

    const frames = document.querySelectorAll('iframe[data-testid="office-frame"]');
    expect(frames).toHaveLength(1);
    const f = frames[0] as HTMLIFrameElement;
    expect(f.closest('.fe-preview__office'), 'inside the mount, not in its place').not.toBeNull();
    expect(f.getAttribute('sandbox')).toBe(OFFICE_FRAME_SANDBOX);
    expect(f.getAttribute('allow')).toBe(OFFICE_FRAME_ALLOW);
    expect(f.getAttribute('referrerpolicy')).toBe('no-referrer');
    const src = new URL(f.getAttribute('src')!);
    expect(src.origin + src.pathname).toBe(FRAME_URL);
    expect(src.hash).toMatch(/^#[A-Za-z0-9_-]{22}$/);

    expect(editors.created).toEqual(['k-1']);
    expect(editors.lastConfig(), 'exactly the server\'s config: no events, nothing of the page').toEqual(SERVER_CONFIG);
    expect(warn.mock.calls.flat().join(' ')).not.toMatch(/runs in this page/);
    expect(warnEditorInPage(), 'the in-page warning was never said').toBe(true);
  });

  it('an edit in the frame is an edit here (the leave-page question, #184\'s rule)', async () => {
    editors = installFramed();
    const w = await open();
    await editors.state(true);
    expect(w.emitted('office-edited')?.[0]).toEqual([true]);
  });

  it('"Download failed" from the frame: the frame goes, the fallback and its diagnosis have the screen', async () => {
    editors = installFramed();
    const w = await open();
    await editors.problem('onError', -4, 'Download failed.');
    await settle();
    expect(editors.frames(), 'no dead editor beside the fallback').toBe(0);
    expect(w.find('[data-testid="office-fallback"]').exists()).toBe(true);
    expect(w.text()).toContain('Download failed.');
    expect(requested.some((u) => u.startsWith('/api/files/onlyoffice/diagnose?path='))).toBe(true);
  });

  it('a warning from the frame is the document server\'s to show: the editor stays', async () => {
    editors = installFramed();
    const w = await open();
    await editors.problem('onWarning', -122, 'The file version has been changed.');
    await settle();
    expect(editors.frames()).toBe(1);
    expect(w.find('[data-testid="office-fallback"]').exists()).toBe(false);
  });

  it('api.js that did not load in the frame: the document server is not answering', async () => {
    editors = installFramed();
    const w = await open(true);
    expect(editors.frames()).toBe(1);
    // Not an ONLYOFFICE event: frame.js says it when its <script> fails.
    await editors.raw({ type: 'failed', reason: 'script' });
    await settle();
    expect(editors.frames()).toBe(0);
    expect(w.text()).toContain(en['viewer.office_unreachable_admin']);
  });

  it('a forged hello from another window on the page opens nothing', async () => {
    editors = installFramed();
    await open();
    const other = document.createElement('iframe');
    document.body.appendChild(other);
    const before = editors.created.length;
    window.dispatchEvent(
      new MessageEvent('message', {
        data: { proto: 'filex-oo', v: 1, type: 'hello', session: SESSION },
        origin: UI_ORIGIN,
        source: other.contentWindow,
      }),
    );
    await settle();
    expect(editors.created).toHaveLength(before);
    other.remove();
  });

  it('no frame named: api.js in the page, as before, and the console says so once', async () => {
    withFrame = false;
    editors = installInPage();
    const warn = vi.spyOn(console, 'warn');
    await open();
    expect(editors.created).toEqual(['k-1']);
    expect(document.querySelectorAll('iframe[data-testid="office-frame"]')).toHaveLength(0);
    expect(warn.mock.calls.flat().join(' ')).toMatch(/runs in this page.*FILEX_ONLYOFFICE_FRAME_ORIGIN/);
  });

  // The maintainers' placement for their own install (no new domain): the page on the document
  // server's own origin. The same site as filex is enough - the browser keeps
  // storage and pages apart per origin.
  it("the frame on the document server's own origin: the same path, the same checks", async () => {
    frameUrl = DS_FRAME_URL;
    editors = installFramed(DS_FRAME_URL);
    const w = await open();
    expect(document.getElementById('fe-onlyoffice-api-js')).toBeNull();
    expect((window as unknown as { DocsAPI?: unknown }).DocsAPI).toBeUndefined();
    const f = document.querySelector('iframe[data-testid="office-frame"]') as HTMLIFrameElement;
    const src = new URL(f.getAttribute('src')!);
    expect(src.origin + src.pathname).toBe(DS_FRAME_URL);
    expect(f.getAttribute('sandbox')).toBe(OFFICE_FRAME_SANDBOX);
    expect(editors.created).toEqual(['k-1']);
    expect(editors.lastConfig()).toEqual(SERVER_CONFIG);
    await editors.state(true);
    expect(w.emitted('office-edited')?.[0]).toEqual([true]);
  });
});
