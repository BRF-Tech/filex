/**
 * @brftech/filex-app-ui — the SDK an app's own interface bundles.
 *
 * ```js
 * import { connect } from '@brftech/filex-app-ui';
 *
 * const fx = await connect();          // the handshake with filex
 * const file = await fx.open();        // the file the app was opened with
 * editor.setValue(await file.text());
 * editor.onChange(() => fx.dirty(true));
 * fx.onSave(async () => editor.getValue()); // filex's Save, a draft's "Save to disk", Ctrl+S
 * ```
 *
 * No dependency, a few KB. The interface runs in a sandboxed frame with no
 * network and no storage of its own: every call below goes to filex over the
 * MessagePort the handshake set up, and filex decides it (docs/APP-PLUGINS.md
 * → "An app's own interface"). The protocol itself is `./protocol`.
 */
import {
  BRIDGE_VERSION,
  HELLO,
  LIMITS,
  isPortMessage,
  type BridgeError,
  type ConfirmParams,
  type ErrorCode,
  type FileInfo,
  type HostEvent,
  type HostRequestMessage,
  type Method,
  type ReadResult,
  type SaveAsResult,
  type SaveResult,
  type Session,
  type Theme,
  type ToastParams,
  type DownloadResult,
  type LicenseInfo,
} from './protocol';

export * from './protocol';

/** A call filex refused, with the reason as a code. */
export class FilexError extends Error {
  readonly code: ErrorCode;
  constructor(err: BridgeError) {
    super(err.message || err.code);
    this.name = 'FilexError';
    this.code = err.code;
  }
}

/** Content the app hands to a save. */
export type SaveData = string | Blob | ArrayBuffer | ArrayBufferView | ReadableStream<Uint8Array>;

/** One opened file, read the way the app needs it. */
export interface OpenedFile extends FileInfo {
  text(): Promise<string>;
  bytes(): Promise<ArrayBuffer>;
  stream(): Promise<ReadableStream<Uint8Array>>;
  /** Save new content over this file (a new version, or the draft it is). */
  save(data: SaveData, mime?: string): Promise<SaveResult>;
}

export interface ConnectOptions {
  /** How long to wait for filex (default 10 s). */
  timeoutMs?: number;
  /**
   * Put filex's colours (`--fe-*`), `lang`, `dir` and `data-theme` on
   * `<html>`, and keep them current (default true).
   */
  applyTheme?: boolean;
  /** Ctrl/Cmd+S inside the interface runs the save handler (default true). */
  saveShortcut?: boolean;
}

export interface FilexApp {
  readonly session: Session;
  /** The file at `index` (default the first) the interface was opened with. */
  open(index?: number): Promise<OpenedFile>;
  /** Save new content over an opened file. */
  save(data: SaveData, opts?: { index?: number; mime?: string }): Promise<SaveResult>;
  /** Save a NEW file; filex asks the person where (its own folder picker). */
  saveAs(name: string, data: SaveData, mime?: string): Promise<SaveAsResult>;
  /** Unsaved changes, or not: filex asks before the person leaves them. */
  dirty(on: boolean): void;
  title(text: string): void;
  toast(text: string, tone?: ToastParams['tone']): void;
  confirm(opts: ConfirmParams | string): Promise<boolean>;
  /** Ask filex to close the interface (it asks first when there are changes). */
  close(): void;
  /** Put text on the clipboard (filex does it, the same way in every browser). */
  copy(text: string): Promise<void>;
  /**
   * Hand the person a file for their own disk (the app needs `ui.download`
   * in its manifest). Call it from a click or a key press: without one,
   * filex asks the person first. `cancelled` when they say no.
   */
  download(name: string, data: SaveData, mime?: string): Promise<DownloadResult>;
  /** Call the app's own module (`ui_call` export) with the opened files. */
  call<T = unknown>(method: string, params?: unknown): Promise<T>;
  /** Queue one of the app's actions on the opened files; answers the op. */
  submit(action: string, params?: Record<string, unknown>): Promise<{ op: unknown }>;
  /** This person's small store for this app (JSON values, 8 KiB each). */
  state: {
    get<T = unknown>(key: string): Promise<T | undefined>;
    set(key: string, value: unknown): Promise<void>;
  };
  /**
   * The app's license (filex 0.52.0; a paid app installed from a store):
   * `{status: "free"}` for a free app, `{status: "valid", valid_until,
   * updates_until}` for a licensed one. Never the key, never the licensee.
   */
  license: {
    get(): Promise<LicenseInfo>;
  };
  /** Listen to filex. Returns the unsubscribe. */
  on(event: HostEvent, handler: (data: unknown) => void): () => void;
  /**
   * What to save when filex asks (its Save button, a draft's "Save to disk",
   * Ctrl+S): return the document and the SDK saves it over the opened file;
   * return nothing if the handler saved by itself.
   */
  onSave(handler: () => SaveData | void | Promise<SaveData | void>): () => void;
  /** Raw call, for a method this SDK has no helper for. */
  request<T = unknown>(method: Method, params?: unknown, transfer?: Transferable[]): Promise<T>;
}

let connecting: Promise<FilexApp> | null = null;

/**
 * The handshake. Resolves once filex answered with its port and the session;
 * rejects outside filex or when filex did not answer in time. Calling it again
 * answers the same connection.
 */
export function connect(opts: ConnectOptions = {}): Promise<FilexApp> {
  if (!connecting) connecting = open(opts);
  return connecting;
}

function open(opts: ConnectOptions): Promise<FilexApp> {
  return new Promise<FilexApp>((resolve, reject) => {
    if (typeof window === 'undefined' || window.parent === window) {
      reject(new FilexError({ code: 'unavailable', message: 'not running inside filex' }));
      return;
    }
    const parent = window.parent;
    // Where filex runs, when the browser says so (Chrome, Safari). A frame
    // drawn with referrerpolicy="no-referrer" has no document.referrer, and
    // Firefox has no ancestorOrigins: then `source === parent` is the check,
    // which is the one that matters (only the parent window IS the parent).
    const expected = ancestorOrigin();
    const timer = setTimeout(() => {
      window.removeEventListener('message', onMessage);
      reject(new FilexError({ code: 'unavailable', message: 'filex did not answer' }));
    }, opts.timeoutMs ?? LIMITS.connectTimeoutMs);

    function onMessage(ev: MessageEvent) {
      if (ev.source !== parent || !isPortMessage(ev.data) || !ev.ports[0]) return;
      if (expected && ev.origin !== expected) return;
      window.removeEventListener('message', onMessage);
      clearTimeout(timer);
      const port = ev.ports[0];
      const app = bridge(port, opts);
      app
        .request<Session>('session.get')
        .then((session) => {
          (app as { session: Session }).session = session;
          if (opts.applyTheme !== false) {
            applyLook(session.theme, session.locale, session.dir);
            app.on('theme', (t) => applyLook(t as Theme));
            app.on('locale', (l) => {
              const v = l as { locale?: string; dir?: 'ltr' | 'rtl' };
              applyLook(undefined, v.locale, v.dir);
            });
          }
          resolve(app);
        })
        .catch(reject);
    }
    window.addEventListener('message', onMessage);
    parent.postMessage({ type: HELLO, v: BRIDGE_VERSION }, '*');
  });
}

function ancestorOrigin(): string {
  try {
    const list = (window.location as Location & { ancestorOrigins?: DOMStringList }).ancestorOrigins;
    if (list && list.length > 0 && list[0] !== 'null') return list[0];
  } catch {
    /* no such API */
  }
  return '';
}

function bridge(port: MessagePort, opts: ConnectOptions): FilexApp {
  let seq = 0;
  const pending = new Map<number, { resolve: (v: unknown) => void; reject: (e: unknown) => void }>();
  const listeners = new Map<HostEvent, Set<(data: unknown) => void>>();
  let saveHandler: (() => SaveData | void | Promise<SaveData | void>) | null = null;

  port.onmessage = (ev: MessageEvent) => {
    const m = ev.data as Record<string, unknown> | null;
    if (!m || typeof m !== 'object') return;
    if (typeof m.id === 'number') {
      const p = pending.get(m.id);
      if (!p) return;
      pending.delete(m.id);
      if (m.error) p.reject(new FilexError(m.error as BridgeError));
      else p.resolve(m.result);
      return;
    }
    if (typeof m.hid === 'number') {
      void answerHost(m as unknown as HostRequestMessage);
      return;
    }
    if (typeof m.event === 'string') {
      for (const h of listeners.get(m.event as HostEvent) ?? []) {
        try {
          h(m.data);
        } catch (e) {
          console.error('[filex-app-ui] listener failed', e);
        }
      }
    }
  };

  async function answerHost(m: HostRequestMessage) {
    if (m.request === 'save') {
      if (!saveHandler) {
        port.postMessage({ hid: m.hid, error: { code: 'unavailable', message: 'this app has no save handler' } });
        return;
      }
      try {
        const res = await runSave();
        port.postMessage({ hid: m.hid, result: res ?? { saved: true } });
      } catch (e) {
        const err = e instanceof FilexError ? { code: e.code, message: e.message } : { code: 'failed', message: String((e as Error)?.message ?? e) };
        port.postMessage({ hid: m.hid, error: err });
      }
      return;
    }
    port.postMessage({ hid: m.hid, error: { code: 'unknown_method' } });
  }

  async function runSave(): Promise<SaveResult | null> {
    if (!saveHandler) return null;
    const data = await saveHandler();
    if (data === undefined || data === null) return null;
    return app.save(data as SaveData);
  }

  function request<T>(method: Method, params?: unknown, transfer: Transferable[] = []): Promise<T> {
    const id = ++seq;
    return new Promise<T>((resolve, reject) => {
      pending.set(id, { resolve: resolve as (v: unknown) => void, reject });
      try {
        port.postMessage({ id, method, params }, transfer);
      } catch (e) {
        pending.delete(id);
        reject(e);
      }
    });
  }

  function notify(method: Method, params?: unknown) {
    request(method, params).catch((e) => console.warn('[filex-app-ui]', method, e));
  }

  async function payload(data: SaveData): Promise<{ data: ReadableStream<Uint8Array> | ArrayBuffer | string; transfer: Transferable[] }> {
    if (typeof data === 'string') return { data, transfer: [] };
    if (data instanceof ArrayBuffer) return { data, transfer: [data] };
    if (ArrayBuffer.isView(data)) {
      const copy = data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength) as ArrayBuffer;
      return { data: copy, transfer: [copy] };
    }
    if (typeof Blob !== 'undefined' && data instanceof Blob) {
      const buf = await data.arrayBuffer();
      return { data: buf, transfer: [buf] };
    }
    if (typeof ReadableStream !== 'undefined' && data instanceof ReadableStream) {
      return { data, transfer: [data as unknown as Transferable] };
    }
    throw new FilexError({ code: 'invalid', message: 'save takes a string, a Blob, bytes or a ReadableStream' });
  }

  const app: FilexApp = {
    session: undefined as unknown as Session,
    async open(index = 0) {
      const info = app.session?.files?.[index];
      if (!info) throw new FilexError({ code: 'not_found', message: `no file at ${index}` });
      const read = <K extends keyof ReadResult>(as: 'stream' | 'bytes' | 'text', key: K) =>
        request<ReadResult>('file.read', { index, as }).then((r) => r[key] as NonNullable<ReadResult[K]>);
      return {
        ...info,
        text: () => read('text', 'text'),
        bytes: () => read('bytes', 'bytes'),
        stream: () => read('stream', 'stream'),
        save: (data: SaveData, mime?: string) => app.save(data, { index, mime }),
      };
    },
    async save(data, o = {}) {
      const p = await payload(data);
      return request<SaveResult>('file.save', { index: o.index ?? 0, data: p.data, mime: o.mime }, p.transfer);
    },
    async saveAs(name, data, mime) {
      const p = await payload(data);
      return request<SaveAsResult>('file.saveAs', { name, data: p.data, mime }, p.transfer);
    },
    async download(name, data, mime) {
      const p = await payload(data);
      return request<DownloadResult>('ui.download', { name, data: p.data, mime }, p.transfer);
    },
    dirty(on) {
      notify('ui.dirty', { dirty: !!on });
    },
    title(text) {
      notify('ui.title', { text: String(text).slice(0, LIMITS.maxLineChars) });
    },
    toast(text, tone) {
      notify('ui.toast', { text: String(text).slice(0, LIMITS.maxLineChars), tone });
    },
    confirm(o) {
      const params = typeof o === 'string' ? { text: o } : o;
      return request<boolean>('ui.confirm', params);
    },
    close() {
      notify('ui.close');
    },
    copy(text) {
      return request<void>('clipboard.write', { text: String(text) });
    },
    call<T>(method: string, params?: unknown) {
      return request<T>('engine.call', { method, params });
    },
    submit(action, params) {
      return request<{ op: unknown }>('job.submit', { action, params });
    },
    state: {
      get<T>(key: string) {
        return request<T | undefined>('state.get', { key });
      },
      set(key: string, value: unknown) {
        return request<void>('state.set', { key, value });
      },
    },
    license: {
      get() {
        return request<LicenseInfo>('license.get');
      },
    },
    on(event, handler) {
      let set = listeners.get(event);
      if (!set) listeners.set(event, (set = new Set()));
      set.add(handler);
      return () => set!.delete(handler);
    },
    onSave(handler) {
      saveHandler = handler;
      return () => {
        if (saveHandler === handler) saveHandler = null;
      };
    },
    request,
  };

  if (opts.saveShortcut !== false && typeof document !== 'undefined') {
    // ⚠ The keystroke is handled HERE and never forwarded: a frame that could
    // send key events to filex could press filex's own shortcuts. Ctrl+S runs
    // the app's own save handler, which saves through the bridge like any call.
    document.addEventListener(
      'keydown',
      (e) => {
        if ((e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && (e.key === 's' || e.key === 'S') && saveHandler) {
          e.preventDefault();
          runSave().catch((err) => app.toast(String((err as Error)?.message ?? err), 'error'));
        }
      },
      true,
    );
  }
  return app;
}

/** Paint filex's look onto the interface's own document. */
function applyLook(theme?: Theme, locale?: string, dir?: 'ltr' | 'rtl') {
  if (typeof document === 'undefined') return;
  const root = document.documentElement;
  if (theme) {
    root.dataset.theme = theme.mode;
    root.style.colorScheme = theme.mode;
    for (const [k, v] of Object.entries(theme.tokens ?? {})) {
      if (/^--fe-[a-z0-9-]+$/.test(k) && typeof v === 'string') root.style.setProperty(k, v);
    }
  }
  if (locale) root.lang = locale;
  if (dir) root.dir = dir;
}
