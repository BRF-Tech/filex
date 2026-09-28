/**
 * appBridge — filex's half of the bridge to an app's own interface.
 *
 * The interface runs in a frame `AppFrame.vue` draws with
 * `sandbox="allow-scripts"`: an opaque origin with no network, no storage and
 * no cookie. It may only talk to us, and only like this (the protocol is the
 * SDK's `protocol.ts`, imported here so the two halves cannot drift):
 *
 *  - its first message must be `filex:hello`, and it is taken ONLY when
 *    `event.source` is the frame we drew — a sandboxed frame's origin is
 *    "null" whoever it is, so the origin tells two of them apart no better
 *    than a payload field does (measured 2026-09-27: a naive listener took a
 *    sibling frame's forged `save`; lesson #635);
 *  - we answer ONCE, with a MessagePort; from then on only the port counts;
 *  - one port per frame element: when the frame loads a second time (the
 *    interface navigated itself somewhere) the port is closed and no new
 *    hello is taken — the document there is not the package the
 *    administrator approved for this view. Reloading an interface means a
 *    new element (AppFrame does it).
 *
 * Every request is answered by a handler the host passes in; a method with
 * no handler answers `unavailable`, one the protocol does not know
 * `unknown_method`. Nothing here decides what a call may do — the handlers
 * do (AppFrame: grants, the opened files, read-only).
 */
import {
  BRIDGE_VERSION,
  PORT,
  isHello,
  isKnownMethod,
  isRequest,
  type BridgeError,
  type ErrorCode,
  type HostEvent,
  type HostRequest,
  type Method,
} from '@brftech/filex-app-ui/protocol';

/**
 * How much ONE frame may ask (security review UI-14): at most `inFlight`
 * calls unanswered at once, and at most `perWindow` requests in any
 * `windowMs`. Past either, a request is answered `unavailable` with the
 * message `busy` — the page around the frame is filex's, and an interface
 * that floods it (a million toasts, a thousand module calls) must not freeze
 * it. Generous for any real editor: an autosave, a title, a dirty flag.
 */
export const BRIDGE_CAPS = { inFlight: 32, perWindow: 400, windowMs: 10_000 } as const;

/** A refusal a handler throws: it reaches the app as `{code, message}`. */
export class BridgeFailure extends Error {
  readonly code: ErrorCode;
  constructor(code: ErrorCode, message?: string) {
    super(message || code);
    this.name = 'BridgeFailure';
    this.code = code;
  }
}

export type BridgeHandler = (params: unknown) => unknown | Promise<unknown>;

export interface AppBridgeOptions {
  /** The window the frame lives in (default: the global one). */
  win?: Window;
  /** The frame we drew. */
  frame: () => HTMLIFrameElement | null;
  handlers: Partial<Record<Method, BridgeHandler>>;
  /** The port is up (the interface said hello). */
  onConnect?: () => void;
  /** The port was closed (a second load, or destroy). */
  onDisconnect?: (why: 'reload' | 'destroyed') => void;
}

export interface AppBridge {
  readonly connected: () => boolean;
  /** Tell the interface something. Dropped while there is no port. */
  emit(event: HostEvent, data?: unknown, transfer?: Transferable[]): void;
  /** Ask the interface something and wait for its answer. */
  ask(request: HostRequest, params?: unknown, timeoutMs?: number): Promise<unknown>;
  /** The frame element fired `load` — call it from the element's listener. */
  frameLoaded(): void;
  destroy(): void;
}

/** A value that may cross postMessage as the result's transfer list. */
function transferables(v: unknown): Transferable[] {
  if (!v || typeof v !== 'object') return [];
  const out: Transferable[] = [];
  for (const x of Object.values(v as Record<string, unknown>)) {
    if (x instanceof ArrayBuffer) out.push(x);
    else if (typeof ReadableStream !== 'undefined' && x instanceof ReadableStream) out.push(x as unknown as Transferable);
  }
  return out;
}

/** A server refusal's `error` field, when it is a code (`quota_exceeded`). */
const CODE_TOKEN = /^[a-z][a-z0-9_]{0,39}$/;

/**
 * What the interface is told when a handler failed.
 *
 * ⚠ Security review UI-15: a BridgeFailure is filex's own refusal and says
 * its own words. ANYTHING else — a server refusal (useFileApi's
 * requestFailure), a browser error, a bug — is told as a protocol code plus,
 * at most, the server's short `error` code: never its sentence, never an
 * exception's text. Those can hold a path on the server's disk, a storage
 * path, a driver's words; the interface is an app, not an administrator.
 */
function asError(e: unknown): BridgeError {
  if (e instanceof BridgeFailure) return { code: e.code, message: e.message };
  const f = (e ?? {}) as { status?: unknown; code?: unknown };
  const status = typeof f.status === 'number' ? f.status : 0;
  const token = typeof f.code === 'string' && CODE_TOKEN.test(f.code) ? f.code : '';
  let code: ErrorCode = 'failed';
  if (status === 404) code = 'not_found';
  else if (status === 413) code = 'too_large';
  else if (status === 400 || status === 422) code = 'invalid';
  else if (status === 409 && token === 'read_only') code = 'read_only';
  else if (status === 403 && token === 'not_granted') code = 'not_granted';
  return { code, message: token || code };
}

export function createAppBridge(opts: AppBridgeOptions): AppBridge {
  const win = opts.win ?? window;
  let port: MessagePort | null = null;
  /** A hello was taken for this element: never another. */
  let used = false;
  /** How many times the element has loaded a document. */
  let loads = 0;
  let destroyed = false;
  let hseq = 0;
  const asking = new Map<number, { resolve: (v: unknown) => void; reject: (e: unknown) => void; timer: ReturnType<typeof setTimeout> }>();

  function close(why: 'reload' | 'destroyed') {
    if (port) {
      port.onmessage = null;
      try {
        port.close();
      } catch {
        /* already gone */
      }
      port = null;
      for (const [, a] of asking) {
        clearTimeout(a.timer);
        a.reject(new BridgeFailure('unavailable', 'the interface went away'));
      }
      asking.clear();
      opts.onDisconnect?.(why);
    }
  }

  /** Calls being answered, and the requests of the current window (UI-14). */
  let inFlight = 0;
  let windowStart = 0;
  let windowCount = 0;

  function withinRate(): boolean {
    const now = Date.now();
    if (now - windowStart >= BRIDGE_CAPS.windowMs) {
      windowStart = now;
      windowCount = 0;
    }
    if (windowCount >= BRIDGE_CAPS.perWindow) return false;
    windowCount++;
    return true;
  }

  async function answer(p: MessagePort, id: number, method: string, params: unknown) {
    let msg: { id: number; result?: unknown; error?: BridgeError };
    let transfer: Transferable[] = [];
    if (!withinRate()) {
      msg = { id, error: { code: 'unavailable', message: 'busy' } };
    } else if (!isKnownMethod(method)) {
      msg = { id, error: { code: 'unknown_method', message: method } };
    } else {
      const h = opts.handlers[method];
      if (!h) {
        msg = { id, error: { code: 'unavailable', message: `${method} is not offered here` } };
      } else if (inFlight >= BRIDGE_CAPS.inFlight) {
        msg = { id, error: { code: 'unavailable', message: 'busy' } };
      } else {
        inFlight++;
        try {
          const result = await h(params);
          msg = { id, result };
          transfer = transferables(result);
        } catch (e) {
          msg = { id, error: asError(e) };
        } finally {
          inFlight--;
        }
      }
    }
    if (p !== port) return; // closed while the handler ran
    try {
      p.postMessage(msg, transfer);
    } catch (e) {
      // A result that cannot cross (a stream this browser cannot transfer):
      // say so rather than leave the call hanging.
      p.postMessage({ id, error: { code: 'failed', message: 'the answer cannot be sent to the interface' } });
    }
  }

  function onPortMessage(p: MessagePort) {
    return (ev: MessageEvent) => {
      const m = ev.data as Record<string, unknown> | null;
      if (!m || typeof m !== 'object') return;
      if (typeof m.hid === 'number') {
        const a = asking.get(m.hid);
        if (!a) return;
        asking.delete(m.hid);
        clearTimeout(a.timer);
        if (m.error) {
          const e = m.error as BridgeError;
          a.reject(new BridgeFailure(e.code ?? 'failed', e.message));
        } else a.resolve(m.result);
        return;
      }
      if (!isRequest(m)) return;
      void answer(p, m.id, m.method, m.params);
    };
  }

  function onWindowMessage(ev: MessageEvent) {
    if (destroyed || used) return;
    const frame = opts.frame();
    const cw = frame?.contentWindow;
    // ⚠ The SOURCE decides, never the origin (always "null" here) and never
    // anything in the payload.
    if (!frame || !cw || ev.source !== cw) return;
    if (!isHello(ev.data)) return;
    // After a second load, the document in the frame is not the one we
    // opened (see the file's header): no port for it.
    if (loads > 1) return;
    used = true;
    const ch = new MessageChannel();
    port = ch.port1;
    port.onmessage = onPortMessage(port);
    port.start?.();
    // '*' because the frame's origin is opaque — there is no origin to name.
    // What guarantees the port reaches the right document is that `cw` is
    // the frame we drew and this runs synchronously on its hello.
    cw.postMessage({ type: PORT, v: BRIDGE_VERSION }, '*', [ch.port2]);
    opts.onConnect?.();
  }

  win.addEventListener('message', onWindowMessage);

  return {
    connected: () => port !== null,
    emit(event, data, transfer = []) {
      if (!port) return;
      try {
        port.postMessage({ event, data }, transfer);
      } catch {
        /* the interface is gone */
      }
    },
    ask(request, params, timeoutMs = 60_000) {
      const p = port;
      if (!p) return Promise.reject(new BridgeFailure('unavailable', 'the interface is not connected'));
      const hid = ++hseq;
      return new Promise((resolve, reject) => {
        const timer = setTimeout(() => {
          asking.delete(hid);
          reject(new BridgeFailure('failed', `the interface did not answer "${request}"`));
        }, timeoutMs);
        asking.set(hid, { resolve, reject, timer });
        p.postMessage({ hid, request, params });
      });
    },
    frameLoaded() {
      loads++;
      if (loads > 1) close('reload');
    },
    destroy() {
      destroyed = true;
      win.removeEventListener('message', onWindowMessage);
      close('destroyed');
    },
  };
}
