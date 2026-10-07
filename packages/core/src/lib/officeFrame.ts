/**
 * officeFrame - the page's half of the ONLYOFFICE frame (task #92).
 *
 * ONLYOFFICE's editor API is a script, api.js, that an integrator loads into
 * its own page. Loaded into filex's page it runs with everything that page
 * can do - the bearer the web client keeps in sessionStorage, filex's API in
 * the signed-in person's name, the page itself. When the server serves a
 * page for it on another origin - the document server's own
 * (FILEX_ONLYOFFICE_FRAME_ORIGIN), or the app-interface origin
 * (FILEX_APP_UI_ORIGIN) - the editor config names that page (`frame`), and
 * the viewer frames it instead: api.js runs on another ORIGIN, out of reach
 * of all three. Another origin is enough, the same site included: browsers
 * keep session storage, local storage, pages and windows apart per origin.
 *
 * The protocol, `filex-oo` version 1 (the frame's half is
 * backend/internal/onlyoffice/frame/frame.js):
 *
 *   1. the frame -> here: {proto, v, type: 'hello', session}. Taken ONLY when
 *      the event's source is the frame element this page drew, its origin is
 *      the frame's address's origin, and the session is the one this page put
 *      in that address's fragment; once per element, and never after the
 *      element loaded a second document.
 *   2. here -> the frame: {proto, v, type: 'open', session, config} with one
 *      MessagePort, posted to the frame's origin by name (a frame that went
 *      elsewhere does not get it). `config` is the server's answer as it came:
 *      signed, untouched. This page adds nothing to it.
 *   3. the frame -> here, over the port only: 'started', 'ready',
 *      'state' {dirty}, 'error' {channel, code, description},
 *      'failed' {reason}. Anything else is dropped. Nothing goes the other
 *      way: closing the editor is removing the frame.
 *
 * The frame element is built with its sandbox, its permissions and its
 * referrer policy BEFORE its address and before it is in the document
 * (lesson #635): a sandbox set afterwards applies from the next navigation
 * only. `allow-same-origin` is safe here and needed - the frame's origin is
 * another origin than filex's, and the document server's editor wants storage
 * of its own. The sandbox attribute also takes `document.domain` away from the
 * frame and everything in it, so a sibling host cannot relax its way back to
 * filex's origin (filex's pages never set it either).
 */
import type { OfficeEventChannel } from './officeDiagnosis';
import { b64urlEncode } from './e2enames';

export const OFFICE_FRAME_PROTOCOL = 'filex-oo';
export const OFFICE_FRAME_VERSION = 1;

/**
 * The frame's sandbox. Scripts and its own origin (the editor), forms (its
 * dialogs), popups (help, a print preview, an "open in a new tab"), downloads
 * ("Download as") and modals (print, its leave-page question). Not
 * allow-top-navigation: the editor never takes the page away.
 */
export const OFFICE_FRAME_SANDBOX =
  'allow-scripts allow-same-origin allow-forms allow-popups allow-downloads allow-modals';

/**
 * What the frame may pass on to the editor's own frame: the clipboard (its
 * paste and copy buttons), full screen (a slideshow) and autoplay (media in a
 * presentation). Nothing else - no camera, no microphone, no screen.
 */
export const OFFICE_FRAME_ALLOW = 'clipboard-read; clipboard-write; fullscreen; autoplay';

/** How long the frame has to say hello before the editor is called unreachable. */
export const OFFICE_FRAME_HELLO_MS = 20_000;

export type OfficeFrameEvent =
  | { type: 'started' }
  | { type: 'ready' }
  | { type: 'state'; dirty: boolean }
  | { type: 'error'; channel: OfficeEventChannel; code: number | null; description: string }
  | { type: 'failed'; reason: 'script' | 'api' };

const SESSION_RE = /^[A-Za-z0-9_-]{16,128}$/;

function envelope(data: unknown): Record<string, unknown> | null {
  if (!data || typeof data !== 'object' || Array.isArray(data)) return null;
  const m = data as Record<string, unknown>;
  if (m.proto !== OFFICE_FRAME_PROTOCOL || m.v !== OFFICE_FRAME_VERSION || typeof m.type !== 'string') return null;
  return m;
}

/** The frame's hello, or null for anything that is not exactly one. */
export function parseFrameHello(data: unknown): { session: string } | null {
  const m = envelope(data);
  if (!m || m.type !== 'hello') return null;
  const session = m.session;
  if (typeof session !== 'string' || !SESSION_RE.test(session)) return null;
  return { session };
}

/** One of the frame's events, or null for anything the protocol does not name. */
export function parseFrameEvent(data: unknown): OfficeFrameEvent | null {
  const m = envelope(data);
  if (!m) return null;
  const type = m.type as string;
  if (type === 'started') return { type: 'started' };
  if (type === 'ready') return { type: 'ready' };
  if (type === 'state') {
    const dirty = m.dirty;
    return typeof dirty === 'boolean' ? { type: 'state', dirty } : null;
  }
  if (type === 'error') {
    const channel: OfficeEventChannel | null =
      m.channel === 'onError' ? 'onError' : m.channel === 'onWarning' ? 'onWarning' : null;
    if (!channel) return null;
    const rawCode = m.code;
    const code = typeof rawCode === 'number' && Number.isFinite(rawCode) ? rawCode : null;
    const rawText = m.description;
    const description = typeof rawText === 'string' ? rawText.slice(0, 1000) : '';
    return { type: 'error', channel, code, description };
  }
  if (type === 'failed') {
    if (m.reason === 'script') return { type: 'failed', reason: 'script' };
    if (m.reason === 'api') return { type: 'failed', reason: 'api' };
    return null;
  }
  return null;
}

let warnedInPage = false;

/**
 * Said once per page, in the console, when the server named no frame: api.js
 * runs in this page, with this page's session. The operator reads it at boot
 * and on the ONLYOFFICE card (editor_same_origin); this is for whoever opens
 * the console of an embed. Returns whether it was said now.
 */
export function warnEditorInPage(): boolean {
  if (warnedInPage) return false;
  warnedInPage = true;
  console.warn(
    "[filex] ONLYOFFICE's api.js runs in this page, with this page's session. " +
      "Set FILEX_ONLYOFFICE_FRAME_ORIGIN (the document server's origin) on the server to run it in a frame of its own (docs/ONLYOFFICE.md).",
  );
  return true;
}

/** Tests only: the next warnEditorInPage() speaks again. */
export function resetEditorInPageWarning(): void {
  warnedInPage = false;
}

/** The origin a frame address is on: an absolute http(s) address, else null. */
export function frameOriginOf(address: string | null | undefined): string | null {
  if (!address) return null;
  try {
    const u = new URL(address);
    if (u.protocol !== 'https:' && u.protocol !== 'http:') return null;
    if (u.username || u.password) return null;
    return u.origin;
  } catch {
    return null;
  }
}

/**
 * The setting that put a frame where it is, for the sentence an administrator
 * reads when it does not load: the interface route is FILEX_APP_UI_ORIGIN's,
 * anything else FILEX_ONLYOFFICE_FRAME_ORIGIN's (`/filex-frame/editor`).
 */
export function frameSetting(address: string): string {
  try {
    return new URL(address).pathname.includes('/_appui/') ? 'FILEX_APP_UI_ORIGIN' : 'FILEX_ONLYOFFICE_FRAME_ORIGIN';
  } catch {
    return 'FILEX_ONLYOFFICE_FRAME_ORIGIN';
  }
}

/** A fresh session for one frame: 16 random bytes, base64url (22 characters). */
export function newFrameSession(): string {
  return b64urlEncode(crypto.getRandomValues(new Uint8Array(16)));
}

/**
 * The frame element, ready to go into the document. The ORDER is the
 * security: sandbox, permissions and referrer policy first, the address last
 * (tests read the order back).
 */
export function buildOfficeFrame(address: string, session: string, title: string): HTMLIFrameElement {
  const f = document.createElement('iframe');
  f.setAttribute('sandbox', OFFICE_FRAME_SANDBOX);
  f.setAttribute('allow', OFFICE_FRAME_ALLOW);
  f.setAttribute('referrerpolicy', 'no-referrer');
  f.setAttribute('title', title);
  f.className = 'fe-preview__officeframe';
  f.dataset.testid = 'office-frame';
  const u = new URL(address);
  u.hash = session;
  f.setAttribute('src', u.toString());
  return f;
}

export interface OfficeFrameOptions {
  /** The window the frame lives in (default: the global one). */
  win?: Window;
  /** The frame element this page drew. */
  frame: () => HTMLIFrameElement | null;
  /** frameOriginOf(the frame's address). */
  origin: string;
  /** The session in the frame's address. */
  session: string;
  /** The editor config, exactly as the server answered it. */
  config: unknown;
  onEvent: (e: OfficeFrameEvent) => void;
  /** The frame said hello and was handed the config. */
  onOpen?: () => void;
  /** No hello within `helloMs`: the frame did not load, or is not ours. */
  onSilent?: () => void;
  helloMs?: number;
}

export interface OfficeFrameLink {
  /** Call from the frame element's `load` listener. */
  frameLoaded(): void;
  /** Stop listening and close the port. */
  close(): void;
  readonly opened: () => boolean;
}

/** Listen for the frame's hello and, on it, open the editor in it (step 2). */
export function linkOfficeFrame(opts: OfficeFrameOptions): OfficeFrameLink {
  const win = opts.win ?? window;
  let port: MessagePort | null = null;
  let helloTaken = false;
  let documents = 0;
  let closed = false;
  let timer: ReturnType<typeof setTimeout> | null = setTimeout(() => {
    timer = null;
    if (!helloTaken && !closed) opts.onSilent?.();
  }, opts.helloMs ?? OFFICE_FRAME_HELLO_MS);

  const stopTimer = () => {
    if (timer !== null) clearTimeout(timer);
    timer = null;
  };

  const shutPort = () => {
    if (!port) return;
    port.onmessage = null;
    try {
      port.close();
    } catch {
      /* gone already */
    }
    port = null;
  };

  const onMessage = (ev: MessageEvent) => {
    if (closed || helloTaken || documents > 1) return;
    const el = opts.frame();
    const target = el?.contentWindow ?? null;
    // ⚠ All three, every time: the element this page drew (another frame on
    // the page can say anything), the origin its address names (the frame
    // has a real origin, unlike an app's opaque one), and the session this
    // page put in that address.
    if (!target || ev.source !== target) return;
    if (ev.origin !== opts.origin) return;
    const hello = parseFrameHello(ev.data);
    if (!hello || hello.session !== opts.session) return;
    helloTaken = true;
    stopTimer();
    const channel = new MessageChannel();
    port = channel.port1;
    port.onmessage = (m: MessageEvent) => {
      if (closed) return;
      const e = parseFrameEvent(m.data);
      if (e) opts.onEvent(e);
    };
    target.postMessage(
      {
        proto: OFFICE_FRAME_PROTOCOL,
        v: OFFICE_FRAME_VERSION,
        type: 'open',
        session: opts.session,
        config: opts.config,
      },
      opts.origin,
      [channel.port2],
    );
    opts.onOpen?.();
  };

  win.addEventListener('message', onMessage);

  return {
    frameLoaded() {
      documents++;
      // A second document in the element is not the page the server served
      // for this session (it navigated itself): it hears nothing more.
      if (documents > 1) shutPort();
    },
    close() {
      closed = true;
      stopTimer();
      win.removeEventListener('message', onMessage);
      shutPort();
    },
    opened: () => port !== null,
  };
}
