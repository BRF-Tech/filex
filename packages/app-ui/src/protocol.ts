/**
 * The bridge between filex and an app's own interface — the ONE definition.
 *
 * An app's interface (the `ui` block of its filex-app.json) runs in a frame
 * filex draws with `sandbox="allow-scripts"`: an opaque origin, no cookies, no
 * storage, no network (the server's CSP says `connect-src 'none'`). Everything
 * it may do goes through this bridge, and the host decides every call.
 *
 * Both halves import this file — the SDK the app bundles (`src/index.ts`) and
 * filex's own frame (`packages/core/src/components/plugin/AppFrame.vue`) — so a
 * method or an event added here is added on both sides at once.
 *
 * The wire, in order (docs/APP-PLUGINS-API.md → "The interface bridge"):
 *
 *  1. The app posts `{type: 'filex:hello', v: 1}` to its parent window.
 *  2. The host takes it ONLY when `event.source` is the frame it drew. The
 *     origin says nothing here: a sandboxed frame's origin is always "null",
 *     so any other sandboxed frame on the page would look the same (measured,
 *     2026-09-27, Chrome, Firefox and WebKit).
 *  3. The host answers with a MessagePort: `{type: 'filex:port', v: 1}` and
 *     the port transferred. Once per load of the frame; a second hello is
 *     ignored.
 *  4. The app takes the port ONLY when `event.source` is its parent window.
 *  5. From then on, only the port: requests `{id, method, params}`, answers
 *     `{id, result}` or `{id, error}`, events `{event, data}`, and the host's
 *     own requests `{hid, request, params}` answered `{hid, result|error}`.
 */

/** The bridge generation both sides speak. */
export const BRIDGE_VERSION = 1;

/** The app's first message, to `window.parent`. */
export const HELLO = 'filex:hello';
/** The host's answer, carrying the port. */
export const PORT = 'filex:port';

/** What the app may ask the host. Anything else is `unknown_method`. */
export type Method =
  | 'session.get'
  | 'file.read'
  | 'file.save'
  | 'file.saveAs'
  | 'ui.dirty'
  | 'ui.title'
  | 'ui.toast'
  | 'ui.confirm'
  | 'ui.close'
  | 'clipboard.write'
  | 'ui.download'
  | 'engine.call'
  | 'job.submit'
  | 'state.get'
  | 'state.set';

export const METHODS: readonly Method[] = [
  'session.get',
  'file.read',
  'file.save',
  'file.saveAs',
  'ui.dirty',
  'ui.title',
  'ui.toast',
  'ui.confirm',
  'ui.close',
  'clipboard.write',
  'ui.download',
  'engine.call',
  'job.submit',
  'state.get',
  'state.set',
];

/** What the host tells the app, unasked. */
export type HostEvent =
  /** The interface's colours changed (light/dark, palette). `data: Theme`. */
  | 'theme'
  /** The reader's language changed. `data: {locale, dir}`. */
  | 'locale'
  /** The open file changed underneath (another tab, another person). */
  | 'file.changed'
  /** The person closed the frame's surroundings; last chance to say so. */
  | 'close.request'
  /** An administrator installed a new version of this app: reload to use it. */
  | 'app.updated';

/** What the host may ask the app (and wait for). */
export type HostRequest =
  /** Save now (the host's Save button, a draft's "Save to disk"): the app
   *  hands its document to `file.save` and answers when it is written. */
  'save';

/** Why a call was refused. */
export type ErrorCode =
  /** The app was not granted what the call needs (files:write for a save…). */
  | 'not_granted'
  /** No such file (an index past the opened files), no such key. */
  | 'not_found'
  /** The file cannot be written: a read-only storage, a view-only opening. */
  | 'read_only'
  /** The call's parameters are wrong. */
  | 'invalid'
  /** Over a limit (LIMITS). */
  | 'too_large'
  /** The server could not be reached or refused; `message` says what. */
  | 'failed'
  /** The person said no (a confirm, a save-as picker closed). */
  | 'cancelled'
  /** The host does not offer this here (no engine, no save handler). */
  | 'unavailable'
  | 'unknown_method';

export interface BridgeError {
  code: ErrorCode;
  message?: string;
}

/* ── messages ──────────────────────────────────────────────────────────── */

export interface HelloMessage {
  type: typeof HELLO;
  v: number;
}

export interface PortMessage {
  type: typeof PORT;
  v: number;
}

export interface RequestMessage {
  id: number;
  method: Method;
  params?: unknown;
}

export interface ResponseMessage {
  id: number;
  result?: unknown;
  error?: BridgeError;
}

export interface EventMessage {
  event: HostEvent;
  data?: unknown;
}

export interface HostRequestMessage {
  hid: number;
  request: HostRequest;
  params?: unknown;
}

export interface HostResponseMessage {
  hid: number;
  result?: unknown;
  error?: BridgeError;
}

/* ── shapes ────────────────────────────────────────────────────────────── */

/** The interface's look: `mode` and filex's own `--fe-*` custom properties. */
export interface Theme {
  mode: 'light' | 'dark';
  /** `--fe-bg`, `--fe-text`, `--fe-accent`… — the SDK sets them on `<html>`. */
  tokens: Record<string, string>;
}

/** One file the interface was opened with. Never a storage path. */
export interface FileInfo {
  index: number;
  name: string;
  /** Lower-case, no dot. */
  ext: string;
  size: number;
  mime: string;
  /** It cannot be saved over: read-only storage, view-only opening, or the
   *  app holds no `files:write`. */
  readOnly: boolean;
}

/** What `session.get` answers — what the app knows about where it runs. */
export interface Session {
  v: number;
  app: { name: string; version: string };
  view: { id: string; placement: 'modal' | 'page' | 'inspector' | 'home' | 'viewer' };
  /** The reader's language tag (`tr`, `pt-br`) and its direction. */
  locale: string;
  dir: 'ltr' | 'rtl';
  theme: Theme;
  /** The person's display name; never their e-mail, never a token. */
  user: { name: string };
  files: FileInfo[];
  /** The permissions this app was granted — so it can hide what it may not do. */
  grants: string[];
  /** The administrator's non-secret settings for this app (grant `settings`). */
  settings?: Record<string, string>;
}

/** `file.read` params. */
export interface ReadParams {
  index?: number;
  /** `stream` (a transferred ReadableStream<Uint8Array>), `bytes` (an
   *  ArrayBuffer), or `text` (UTF-8, at most LIMITS.maxTextBytes). */
  as?: 'stream' | 'bytes' | 'text';
}

export interface ReadResult {
  name: string;
  size: number;
  mime: string;
  stream?: ReadableStream<Uint8Array>;
  bytes?: ArrayBuffer;
  text?: string;
}

/** `file.save` params: the new content of the opened file. */
export interface SaveParams {
  index?: number;
  /** A transferred ReadableStream<Uint8Array>, an ArrayBuffer or a string. */
  data: ReadableStream<Uint8Array> | ArrayBuffer | string;
  mime?: string;
}

export interface SaveResult {
  saved: true;
  size: number;
}

/** `file.saveAs`: a NEW file, somewhere the person picks in filex's own dialog. */
/**
 * `ui.download`: a file for the person's own disk. filex does it (a sandboxed
 * frame cannot download), with the app's `ui:download` grant, on a gesture in
 * the frame or the person's yes, never over LIMITS.maxDownloadBytes.
 */
export interface DownloadParams {
  /** The file name offered — a name, not a path. */
  name: string;
  /** A transferred ReadableStream<Uint8Array>, an ArrayBuffer or a string. */
  data: ReadableStream<Uint8Array> | ArrayBuffer | string;
  mime?: string;
}

export interface DownloadResult {
  saved: true;
  size: number;
}

export interface SaveAsParams {
  name: string;
  data: ReadableStream<Uint8Array> | ArrayBuffer | string;
  mime?: string;
}

export interface SaveAsResult {
  saved: true;
  name: string;
  size: number;
}

export interface ToastParams {
  text: string;
  tone?: 'info' | 'success' | 'warning' | 'error';
}

export interface ConfirmParams {
  title?: string;
  text: string;
  confirm?: string;
  cancel?: string;
  danger?: boolean;
}

/** `engine.call`: the app's own module, `ui_call` export. */
export interface EngineCallParams {
  method: string;
  params?: unknown;
}

/** `job.submit`: queue one of the app's actions on the opened files. */
export interface JobSubmitParams {
  action: string;
  params?: Record<string, unknown>;
}

/* ── limits ────────────────────────────────────────────────────────────── */

export const LIMITS = {
  /** `file.read({as: 'text'})` refuses a file larger than this; read a stream. */
  maxTextBytes: 32 << 20,
  /** One `state.set` value, as JSON. */
  maxStateBytes: 8 << 10,
  /** A `ui.toast` / `ui.title` text, in characters. */
  maxLineChars: 300,
  /** How long the SDK waits for the host's port. */
  connectTimeoutMs: 10_000,
  /** The largest file `ui.download` hands the person (streamed or not). */
  maxDownloadBytes: 256 << 20,
} as const;

/* ── guards ────────────────────────────────────────────────────────────── */

export function isHello(d: unknown): d is HelloMessage {
  return !!d && typeof d === 'object' && (d as HelloMessage).type === HELLO && typeof (d as HelloMessage).v === 'number';
}

export function isPortMessage(d: unknown): d is PortMessage {
  return !!d && typeof d === 'object' && (d as PortMessage).type === PORT;
}

export function isRequest(d: unknown): d is RequestMessage {
  return (
    !!d &&
    typeof d === 'object' &&
    typeof (d as RequestMessage).id === 'number' &&
    typeof (d as RequestMessage).method === 'string'
  );
}

export function isKnownMethod(m: string): m is Method {
  return (METHODS as readonly string[]).includes(m);
}
