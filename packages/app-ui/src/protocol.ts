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
  | 'state.set'
  /** 0.52.0: what the app's license says (a paid app from a store), `LicenseInfo`. */
  | 'license.get'
  /** 0.55.0: filex prints a PDF the app hands it (`PrintParams`, grant `ui:print`). */
  | 'ui.print'
  /**
   * Editing together (`coedit.*`, task #189): the session's log the host
   * seals, orders through filex's relay and opens again. Defined in filex
   * 0.55, which answers each `unavailable`; offered from 0.56 to an app
   * granted `files:co-edit` (see `CoEditHello`). `unavailable` = edit alone.
   */
  | 'coedit.join'
  | 'coedit.subscribe'
  | 'coedit.append'
  | 'coedit.lease'
  | 'coedit.cursor'
  | 'coedit.blob.put'
  | 'coedit.blob.get'
  | 'coedit.leave';

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
  'license.get',
  'ui.print',
  'coedit.join',
  'coedit.subscribe',
  'coedit.append',
  'coedit.lease',
  'coedit.cursor',
  'coedit.blob.put',
  'coedit.blob.get',
  'coedit.leave',
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
  | 'app.updated'
  /** Editing together: the next entry of the session's log, opened. `data: CoEditEntry`. */
  | 'coedit.entry'
  /** Editing together: another member's cursor (not kept). `data: CoEditCursor`. */
  | 'coedit.cursor'
  /**
   * Editing together: the host stopped handing entries over. `data:
   * CoEditDropped` - `gone`: the membership ended (join again); `broken`: an
   * entry did not check out (stop editing together). A connection that drops
   * for a moment is the host's own: it reconnects and says nothing.
   */
  | 'coedit.dropped';

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
  /**
   * 0.55.0: the file is end-to-end encrypted - `folder` in an encrypted
   * folder, `vault` in a vault (where only one person edits at a time),
   * `file` for a single encrypted file (`.fxe`). As the server says it;
   * ABSENT for a file that is not encrypted.
   *
   * ⚠ The server holds such a file only as ciphertext and has no key. Unless
   * `plaintext` is true, filex hands the app nothing of it: `file.read` is
   * refused (`failed` with `encrypted`), a save is refused, and `readOnly`
   * is true.
   */
  encrypted?: 'folder' | 'vault' | 'file';
  /**
   * 0.56.0: filex hands this encrypted file to the app in the clear. The
   * person's browser, which holds the folder key while the folder is
   * unlocked, decrypts it for `file.read` and encrypts what `file.save`
   * hands over before it leaves the browser; the server sees only
   * ciphertext. Only with `encrypted: 'folder'` or `'vault'`, for an app
   * whose manifest says `encrypted_folders: {"open": true}` (the grant
   * `files:e2e-plaintext`) - never for a `.fxe`. ABSENT otherwise.
   *
   * In a vault (`encrypted: 'vault'`) the browser reads the file from the
   * vault's packs and writes a save as the vault's next generation, under
   * the vault's write lock (one writer at a time). A save is refused -
   * `failed` with `vault_locked` - while somebody else writes the vault, and
   * with `vault_lock_lost` when this session's write lock ended before the
   * save was committed: nothing was written, save again. The vault locked
   * meanwhile (its keys left the browser): `unavailable`, and filex closes
   * the app.
   *
   * What changes for the app: a read and a save are held whole in the page
   * (at most LIMITS.maxEncryptedBytes); a save is written only over the file
   * as it was opened (`failed` with `changed` when it changed meanwhile);
   * `file.saveAs` answers `unavailable` (a copy elsewhere would leave the
   * folder decrypted); the app's module (`engine.call`, `job.submit`) is
   * never handed the file; `state.get` / `state.set` use a store of their
   * own for this folder, sealed in the person's browser (the server keeps it
   * and cannot read it; apart from the app's ordinary store, a little smaller
   * as it is measured sealed). Everything the app holds is plaintext: keep it
   * in the frame. In a folder whose names are encrypted too, `name` and
   * `ext` are the decrypted name's - the server never sees them.
   */
  plaintext?: true;
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
  /**
   * 0.55.0, editing together: the last log entry (`CoEditEntry.seq`) the
   * saved document holds, so the session knows what is saved. Ignored
   * outside a session.
   */
  through?: number;
}

export interface SaveResult {
  saved: true;
  size: number;
}

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

/**
 * `ui.print` (filex 0.55.0): a PDF for the browser's print dialog. A
 * sandboxed frame may not open that dialog (measured: Chromium ignores
 * `print()` there and says so, Firefox ignores it), so filex prints the PDF
 * from a page of its own, with the app's `ui:print` grant, never over
 * LIMITS.maxPrintBytes. filex asks every time, above the frame, and the
 * dialog opens only on the person's click on filex's Allow. The print dialog
 * can save the PDF too, so `ui:print` is the same kind of grant as
 * `ui:download`. filex 0.54 and older answer `unknown_method`.
 */
export interface PrintParams {
  /** The document's name, for the question filex may ask - a name, not a path. */
  name: string;
  /** The PDF: a transferred ArrayBuffer or ReadableStream<Uint8Array>. */
  data: ReadableStream<Uint8Array> | ArrayBuffer;
  /** `application/pdf` when given; nothing else is printed. */
  mime?: string;
}

export interface PrintResult {
  /** The browser's print dialog was opened with the PDF. */
  printed: true;
  size: number;
}

/* ── editing together (coedit.*: filex 0.55 defined, 0.56 offers) ──────── */

/** What a member appends: the editor's changes, a lock request, a release. */
export type CoEditKind = 'changes' | 'lock' | 'release';

/** A member of the session. */
export interface CoEditMember {
  /** The relay's id for the connection. */
  client: string;
  /** An opaque id of the person, the same for each of their connections in
   *  this session; never an account id. */
  user: string;
  /** The person's display name. */
  name: string;
  /** The editor's per-session user index: given once, never twice. */
  indexUser: number;
  /** May append changes (an editor role and the right to change the file). */
  canEdit: boolean;
}

/** `coedit.join` params: join (or start) the session of an opened file. */
export interface CoEditJoinParams {
  index?: number;
}

/**
 * What `coedit.join` answers: who the app is in the session and how far
 * the log has gone. The entries come as `coedit.entry` events once the app
 * subscribes (`coedit.subscribe` from 0 for the whole log). `unavailable`
 * when filex offers no editing together here (filex 0.55, editing together
 * turned off, an encrypted file, a vault): the app edits alone - the message
 * says why (`not_ready` after waiting for a session's creator, `encrypted`,
 * `nothing_to_watch` for a person who may only read the file while nobody
 * edits it, or the server's code: `coedit_off`, `coedit_build`,
 * `coedit_file_changed`).
 */
export interface CoEditHello {
  session: string;
  me: CoEditMember;
  /**
   * The last entry's seq when filex handed the session over (0: an empty
   * log). filex 0.56: the changes this browser kept from an earlier opening
   * of the file (a lost connection, a closed tab), which filex sends before
   * it answers, are in the log up to here - an editor that starts once it
   * has read this far opens with them.
   */
  head: number;
  /** The last `changes` entry's seq, as far as `head`. */
  changesHead: number;
  /** The last entry the file on the storage holds. */
  savedThrough: number;
  /**
   * filex 0.56: this member started the session. Before anybody else can
   * join it puts the base document - EXACTLY the bytes it opened - with
   * `coedit.blob.put('base', bytes)`. A member that did not start it opens
   * `coedit.blob.get('base')`, never the file: the file may hold a save of
   * the session already, and the log applies to the base.
   */
  created: boolean;
}

/** `coedit.dropped` event data (filex 0.56). */
export interface CoEditDropped {
  /** The last entry the host handed over. */
  from: number;
  /** `gone`: the membership ended (the session closed, or the page was away
   *  too long) - join again. `broken`: an entry did not check out (missing,
   *  moved, altered, replayed) - stop editing together. */
  reason: 'gone' | 'broken';
}

/** `coedit.subscribe` params: entries after `from` arrive as `coedit.entry`. */
export interface CoEditSubscribeParams {
  from: number;
}

/** `coedit.append` params: the host seals it and the relay places it. */
export interface CoEditAppendParams {
  kind: CoEditKind;
  body: unknown;
}

export interface CoEditAppendResult {
  /** Where it landed; it comes back as a `coedit.entry` too. */
  seq: number;
}

/** `coedit.lease`: the changes lease (one writer at a time, one that has
 *  seen every change). */
export interface CoEditLeaseParams {
  op: 'acquire' | 'release';
  /** The seq of the last `changes` entry the app applied. */
  changesSeen: number;
}

export interface CoEditLeaseResult {
  granted: boolean;
}

/** `coedit.cursor` params: passed to the other members, not kept. The host
 *  sends a few a second at most (the last one wins). */
export interface CoEditCursorParams {
  cursor: unknown;
}

/** `coedit.blob.put` / `coedit.blob.get`: the session's sealed blobs (the
 *  base document, images added while editing). A name, not a path. */
export interface CoEditBlobPutParams {
  name: string;
  data: ArrayBuffer;
}

export interface CoEditBlobGetParams {
  name: string;
}

export interface CoEditBlobGetResult {
  name: string;
  bytes: ArrayBuffer;
}

interface CoEditEntryBase {
  seq: number;
  /** The relay's time, Unix ms - the same for every member. */
  at: number;
  client: string;
}

/** One entry of the session's log, opened and checked by filex. */
export type CoEditEntry =
  | (CoEditEntryBase & { kind: CoEditKind; body: unknown })
  | (CoEditEntryBase & { kind: 'join'; member: CoEditMember })
  | (CoEditEntryBase & { kind: 'leave' })
  | (CoEditEntryBase & { kind: 'saved'; through: number });

/** `coedit.cursor` event data. */
export interface CoEditCursor {
  client: string;
  cursor: unknown;
}

/**
 * `file.saveAs`: a NEW file, in a folder the person picks in filex's own
 * folder dialog (the one its Move to… uses), which opens in the opened
 * file's folder. Closing the dialog answers `cancelled`.
 */
export interface SaveAsParams {
  /** The file name — a name, not a path. */
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

/**
 * `license.get` (filex 0.52.0): what the app's license says. A paid app is
 * installed from a store, which issues its license and is asked about it every
 * day; filex keeps the key and never hands it to the app. `status`:
 *
 *   - `free`: not a paid app;
 *   - `valid`: the store said valid at its last check;
 *   - `grace`: valid, but the store could not be asked since - it holds until
 *     the grace the store signed ends;
 *   - anything else (`revoked`, `expired`, `invalid`, `seats_exhausted`,
 *     `wrong_app`, `grace_expired`, `missing`, `unverified`): the app is held
 *     and its interface is not served, so an app rarely reads one of these.
 *
 * `valid_until` / `updates_until` are there for a valid license (dates as
 * RFC 3339). Who holds the license is not the app's to read. filex 0.51.0
 * and older answer `unknown_method`.
 */
export interface LicenseInfo {
  status: string;
  valid_until?: string;
  updates_until?: string;
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
  /**
   * The largest PDF `ui.print` prints (filex 0.55). Lower than a download:
   * a download can stream to disk, a print is held whole in the page (one
   * Blob) until the print dialog has read it, and a document worth printing
   * is far smaller.
   */
  maxPrintBytes: 64 << 20,
  /**
   * The largest file of an encrypted folder filex hands an app in the clear
   * or takes back from it (filex 0.56, `FileInfo.plaintext`): it is
   * decrypted and encrypted whole in the page, as filex's own explorer does
   * for a file of this size.
   */
  maxEncryptedBytes: 200 << 20,
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
