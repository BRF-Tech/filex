// What a document window leaves in the log (task #366).
//
// The windows that show a document (main.ts makeDocumentWindow: the viewer,
// the "Open with filex" editor and the app store) are remote pages, and until
// now nothing that happened to them reached filex-desktop.log. On 2026-11-18 a
// .doc opened with filex stopped taking keystrokes for half an hour in a
// process that had been running since the morning: the menus worked, no edit
// reached the server, and the log held the [openwith] lines and nothing else.
// The process was gone before anyone could look at it, so nobody can say
// whether the page hung, its renderer died, the editor's frame crashed or its
// requests stalled.
//
// A document window now writes, through src/log.ts:
//
//   [docwin]  its life: opened (kind, file name), closed (how long it was
//             open), unresponsive / responsive (how long it was stuck), its
//             renderer gone (reason, exit code), a frame inside it whose
//             process went away, a load that failed, and the page's console
//             ERRORS - at most CONSOLE_LIMIT a minute per window, a repeat
//             counted instead of written again;
//   [docreq]  its requests to the account's server that fail (net::ERR_*),
//             answer with an error status (404 aside), or wait longer than
//             SLOW_MS for an answer: once while still waiting, and again when
//             the answer finally comes. At most REQUEST_LIMIT a minute.
//
// ⚠⚠ Nothing here may write a credential. A request line carries the method,
// the path and the query with every value that could be one masked
// (redactUrl), never a header; a console message goes through redactText.
// test/doc-diag.test.ts proves it with the shapes filex's own tokens have
// (share links, tickets, coedit tokens, JWTs, `?token=`).
//
// Pure on purpose: no electron import, so node:test drives it with fake
// windows and fake requests. main.ts hands it the real ones.

/** A request with no answer after this long is written as waiting. */
export const SLOW_MS = 15_000;
/** How often the open windows are looked at (waiting requests, frames). */
export const SWEEP_MS = 5_000;
/** Console errors written per window and minute; the rest are counted. */
export const CONSOLE_LIMIT = 10;
/** Request lines written per window and minute; the rest are counted. */
export const REQUEST_LIMIT = 20;
export const GATE_PERIOD_MS = 60_000;
/** Requests in flight remembered at once (for the "waiting" line). The oldest
 *  goes when it is full: memory stays bounded whatever a page does. */
export const MAX_PENDING = 512;

const MASK = '*';

// ─────────────────────────── redaction ───────────────────────────

/** Parts of a query key whose value is never written. A substring of the
 *  lowercased key, so `access_token`, `X-Amz-Signature`, `apikey` and
 *  `share_password` are all caught; masking a harmless value by mistake costs
 *  a detail, writing a credential costs the account. */
const SECRET_KEY_PARTS = [
  'token', 'key', 'ticket', 'sig', 'secret', 'pass', 'pwd', 'auth', 'code', 'state',
  'session', 'sid', 'jwt', 'bearer', 'cred', 'otp', 'pin', 'nonce', 'md5', 'hash', 'amz', 'goog',
];

export function secretKey(key: string): boolean {
  const k = key.toLowerCase();
  return SECRET_KEY_PARTS.some((p) => k.includes(p));
}

/** Path segments after which the next one IS the secret: a share link
 *  (`/s/<token>`), a file request (`/d/<token>`), a download or archive link
 *  (`/z/<ticket>`) and an upload ticket (`/u/<ticket>`), at the root and under
 *  `/api/public/`. */
const DOORS = new Set(['s', 'd', 'z', 'u']);

const JWT = /eyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}(?:\.[A-Za-z0-9_-]*)?/;
const JWT_ALL = new RegExp(JWT.source, 'g');
/** One or two file extensions at the end (`.docx`, `.tar.gz`). */
const EXTENSION = /(?:\.[A-Za-z][A-Za-z0-9]{0,7}){1,2}$/;

/**
 * True for a string that looks like a credential rather than a name:
 * base64 / base64url with both letter cases (tickets, keys), a JWT, a run of
 * 24+ letters and digits with no separator (hex: share tokens are 32
 * characters, coedit tokens 64). A space, a slash, a dot inside or a non-ASCII
 * letter makes it a name ("Bütçe Özeti.docx", `Object.handleKey`); so do
 * separators between lower-case words ("2026-11-18-backup-final"). A file
 * extension is looked through: the stem decides, so `<ticket>.json` is still
 * masked.
 */
export function opaque(s: string): boolean {
  if (s.startsWith('eyJ') && JWT.test(s)) return true;
  const stem = s.replace(EXTENSION, '');
  if (stem.length < 20 || !/^[A-Za-z0-9_+=-]+$/.test(stem)) return false;
  const upper = /[A-Z]/.test(stem);
  const lower = /[a-z]/.test(stem);
  const digit = /[0-9]/.test(stem);
  if (upper && lower && (digit || stem.length >= 32)) return true;
  // Hex (share tokens are 32, coedit tokens 64) and other single-case runs.
  if (digit && (upper || lower) && stem.length >= 24 && !/[-_]/.test(stem)) return true;
  return false;
}

/** Every credential-looking word in a free text replaced by the mask. */
function maskWords(text: string): string {
  return text
    .replace(JWT_ALL, MASK)
    .replace(/[A-Za-z0-9_+=-]{20,}(?:\.[A-Za-z][A-Za-z0-9]{0,7})?/g, (w) => (opaque(w) ? MASK : w));
}

function clip(s: string, max: number): string {
  return s.length <= max ? s : `${s.slice(0, max - 1)}…`;
}

/** The head and the tail of a long path: the tail is the file name. */
function clipMiddle(s: string, max: number): string {
  if (s.length <= max) return s;
  const tail = Math.floor(max / 3);
  return `${s.slice(0, max - tail - 1)}…${s.slice(s.length - tail)}`;
}

function decode(s: string): string {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}

/** A URL path, segment by segment: decoded for reading, door secrets and
 *  credential-looking segments masked, each segment and the whole clipped. */
export function redactPath(pathname: string, max = 160): string {
  const segs = pathname.split('/').map(decode);
  const out = segs.map((seg, i) => {
    if (!seg) return seg;
    if (i > 0 && DOORS.has(segs[i - 1])) return MASK;
    if (opaque(seg)) return MASK;
    return clip(maskWords(seg), 48);
  });
  return clipMiddle(out.join('/'), max);
}

function redactQuery(params: URLSearchParams): string {
  const parts: string[] = [];
  let n = 0;
  for (const [k, v] of params) {
    if (++n > 12) {
      parts.push('…');
      break;
    }
    const key = clip(maskWords(k), 32);
    if (secretKey(k)) parts.push(`${key}=${MASK}`);
    else if (v.startsWith('/')) parts.push(`${key}=${redactPath(v, 120)}`);
    else parts.push(`${key}=${opaque(v) ? MASK : clip(maskWords(v), 80)}`);
  }
  return parts.join('&');
}

const WEB_SCHEME = /^(https?|wss?):$/;

/** Same server, whatever the scheme says: `wss://host` is the server of
 *  `https://host`, and the websocket of a page is its own server's. */
function sameServer(u: URL, home: string | null | undefined): boolean {
  if (!home) return false;
  try {
    const h = new URL(home);
    const plain = (p: string) => (p === 'wss:' ? 'https:' : p === 'ws:' ? 'http:' : p);
    return plain(u.protocol) === plain(h.protocol) && u.host === h.host;
  } catch {
    return false;
  }
}

/**
 * A URL as a log line may carry it. The account's own server (`home`) is
 * left out - every [docreq] line is about it - so its requests read as a
 * path; another origin keeps its scheme and host. Never a fragment (an OAuth
 * answer travels there), never a user:password, never a secret query value
 * (secretKey), never a credential-looking segment or value (opaque).
 */
export function redactUrl(raw: string, home?: string | null): string {
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return clip(maskWords(raw), 160);
  }
  if (u.protocol === 'data:') return 'data:…';
  if (u.protocol === 'blob:') return 'blob:…';
  if (u.protocol === 'file:') return 'file:…';
  const prefix = sameServer(u, home) ? '' : WEB_SCHEME.test(u.protocol) || u.host ? `${u.protocol}//${u.host}` : u.protocol;
  const query = redactQuery(u.searchParams);
  return `${prefix}${redactPath(u.pathname)}${query ? `?${query}` : ''}`;
}

/**
 * A free text (a console message) as a log line may carry it: every URL in
 * it through redactUrl, `Bearer …` and `Basic …` masked, `key=value` and
 * `"key": "value"` pairs with a secret key masked, credential-looking words
 * masked, and the whole clipped.
 */
export function redactText(text: string, home?: string | null, max = 300): string {
  const out = text
    .replace(/\b(?:https?|wss?|blob|data):[^\s"'<>()[\]{}`]+/g, (m) => redactUrl(m, home))
    .replace(/\b(Bearer|Basic)\s+[^\s"',;]+/gi, (_m, scheme: string) => `${scheme} ${MASK}`)
    .replace(/"([A-Za-z0-9_.-]+)"\s*:\s*"[^"]*"/g, (m, k: string) => (secretKey(k) ? `"${k}":"${MASK}"` : m))
    .replace(/([A-Za-z0-9_.-]+)=([^&\s"';,)]+)/g, (m, k: string, v: string) =>
      secretKey(k) && v !== MASK ? `${k}=${MASK}` : m,
    );
  return clip(maskWords(out), max);
}

// ─────────────────────────── rate gate ───────────────────────────

/** What a written line carries about the lines held back before it. */
export interface Held {
  /** Lines identical to the previous written one, held back since it. */
  repeated: number;
  /** Other lines held back by the limit. */
  dropped: number;
}

/**
 * At most `limit` lines a period, and a line identical to the previous one
 * within the period counted instead of written. A page that logs the same
 * error in a loop leaves one line a minute with the count, not a megabyte;
 * the next line written (or the window's last, drain) says what was held.
 */
export class RateGate {
  private periodStart = Number.NEGATIVE_INFINITY;
  private used = 0;
  private last: string | null = null;
  private lastAt = Number.NEGATIVE_INFINITY;
  private repeated = 0;
  private dropped = 0;
  // ⚠ No parameter properties anywhere in this file: the desktop tests run
  // it with `node --experimental-strip-types`, which refuses them.
  private readonly limit: number;
  private readonly periodMs: number;

  constructor(limit: number, periodMs = GATE_PERIOD_MS) {
    this.limit = limit;
    this.periodMs = periodMs;
  }

  /** null: hold this line back (it is counted). Otherwise write it, with
   *  what was held since the previous written line. */
  pass(key: string, now: number): Held | null {
    if (now - this.periodStart >= this.periodMs) {
      this.periodStart = now;
      this.used = 0;
    }
    if (key === this.last && now - this.lastAt < this.periodMs) {
      this.repeated++;
      return null;
    }
    if (this.used >= this.limit) {
      this.dropped++;
      return null;
    }
    this.used++;
    const held = { repeated: this.repeated, dropped: this.dropped };
    this.repeated = 0;
    this.dropped = 0;
    this.last = key;
    this.lastAt = now;
    return held;
  }

  /** What was held and never written: for the window's last line. */
  drain(): Held | null {
    if (!this.repeated && !this.dropped) return null;
    const held = { repeated: this.repeated, dropped: this.dropped };
    this.repeated = 0;
    this.dropped = 0;
    return held;
  }
}

/** The counts worth writing (a zero is left out of the line). */
function heldFields(h: Held | null): Partial<Held> {
  if (!h) return {};
  return { ...(h.repeated ? { repeated: h.repeated } : {}), ...(h.dropped ? { dropped: h.dropped } : {}) };
}

// ─────────────────────────── requests in flight ───────────────────────────

export interface Pending {
  id: number;
  /** The webContents the request came from. */
  wc: number;
  method: string;
  /** Already redacted: a credential is never even kept here. */
  path: string;
  type: string;
  start: number;
  /** Response headers have arrived. */
  responded: boolean;
  /** Written as waiting already. */
  flagged: boolean;
}

/** The requests of the document windows still in flight, bounded. */
export class PendingRequests {
  private readonly byId = new Map<number, Pending>();
  /** Entries pushed out because the list was full, since last read. */
  evicted = 0;

  private readonly max: number;

  constructor(max = MAX_PENDING) {
    this.max = max;
  }

  get size(): number {
    return this.byId.size;
  }

  /** A request leaves. A redirect is the same request (the same id): it keeps
   *  the time it first left at. */
  start(p: Omit<Pending, 'responded' | 'flagged'>): void {
    if (this.byId.has(p.id)) return;
    if (this.byId.size >= this.max) {
      const oldest = this.byId.keys().next().value;
      if (oldest !== undefined) this.byId.delete(oldest);
      this.evicted++;
    }
    this.byId.set(p.id, { ...p, responded: false, flagged: false });
  }

  get(id: number): Pending | undefined {
    return this.byId.get(id);
  }

  /** The request is over (completed or failed). */
  end(id: number): Pending | undefined {
    const p = this.byId.get(id);
    this.byId.delete(id);
    return p;
  }

  /** Requests with no answer for `slowMs` that were not written yet; they
   *  are marked written. */
  overdue(now: number, slowMs: number): Pending[] {
    const out: Pending[] = [];
    for (const p of this.byId.values()) {
      if (p.responded || p.flagged || now - p.start < slowMs) continue;
      p.flagged = true;
      out.push(p);
    }
    return out;
  }

  /** A window is gone: what it had in flight is forgotten. */
  dropWindow(wc: number): void {
    for (const [id, p] of this.byId) if (p.wc === wc) this.byId.delete(id);
  }
}

// ─────────────────────────── frames ───────────────────────────

export interface FrameSample {
  /** frameTreeNodeId: the same frame across navigations. */
  key: string;
  /** Its renderer's OS process id (0: none to read). */
  pid: number;
  url: string;
  /** The frame can no longer be read: what a frame whose renderer died
   *  looks like (subframes). */
  dead?: boolean;
}

/**
 * A frame inside the page whose renderer process went away.
 *
 * ⚠ Electron reports a crash of the PAGE's renderer (render-process-gone),
 * not of a frame in another process: with site isolation a cross-site
 * <iframe> (a document server's editor) runs in a renderer of its own, and
 * when that one dies the page around it keeps working - menus answer,
 * nothing types. So the frames are looked at instead, at every sweep.
 *
 * Measured on Electron 44 (scripts/docwin-e2e.mjs): once such a frame's
 * renderer is gone the frame stays in `framesInSubtree` with its
 * frameTreeNodeId, `detached` turns true, and every other property throws
 * "Render frame was disposed before WebFrameMain could be accessed". So a
 * frame seen alive in a process is gone when it is dead (or still read in
 * that same process) and that process is no longer running - at two looks
 * in a row: a frame being torn down (the page reloading after a save) is
 * detached for a moment too. A frame that moved to a new process is a
 * navigation, not a crash. Written once per frame and process.
 */
export class FrameWatch {
  private readonly alive = new Map<string, { pid: number; url: string }>();
  private readonly told = new Map<string, number>();
  private readonly suspect = new Map<string, number>();

  check(frames: readonly FrameSample[], live: ReadonlySet<number>): Array<FrameSample & { lastPid: number }> {
    const gone: Array<FrameSample & { lastPid: number }> = [];
    const present = new Set<string>();
    for (const f of frames) {
      present.add(f.key);
      if (!f.dead && f.pid > 0 && live.has(f.pid)) {
        this.alive.set(f.key, { pid: f.pid, url: f.url });
        this.suspect.delete(f.key);
        continue;
      }
      const seen = this.alive.get(f.key);
      if (!seen) continue;
      if ((!f.dead && f.pid > 0 && f.pid !== seen.pid) || live.has(seen.pid)) {
        this.suspect.delete(f.key);
        continue;
      }
      const looks = (this.suspect.get(f.key) ?? 0) + 1;
      this.suspect.set(f.key, looks);
      if (looks < 2 || this.told.get(f.key) === seen.pid) continue;
      this.told.set(f.key, seen.pid);
      gone.push({ key: f.key, pid: f.pid, url: seen.url, lastPid: seen.pid });
    }
    for (const m of [this.alive, this.told, this.suspect]) {
      for (const k of [...m.keys()]) if (!present.has(k)) m.delete(k);
    }
    return gone;
  }
}

// ─────────────────────────── the watcher ───────────────────────────

/** The slice of Electron's WebFrameMain read here. */
export interface FrameLike {
  readonly processId: number;
  readonly osProcessId: number;
  readonly frameTreeNodeId: number;
  readonly url: string;
  readonly detached?: boolean;
  readonly framesInSubtree: readonly FrameLike[];
}

/** The slice of Electron's WebContents read here. */
export interface DocContents {
  readonly id: number;
  on(event: string, listener: (...args: any[]) => void): unknown;
  getOSProcessId(): number;
  readonly mainFrame: FrameLike;
}

/** The fields of Electron's webRequest details read here (never a header). */
export interface RequestDetails {
  id: number;
  url: string;
  method: string;
  resourceType?: string;
  webContentsId?: number;
  statusCode?: number;
  error?: string;
}

export type DocKind = 'viewer' | 'editor' | 'store';

export interface DocWindowInfo {
  kind: DocKind;
  /** The file name (the window's title). The account's own file. */
  name: string;
  /** The account's server origin: its requests are the ones watched. */
  home: string | null;
}

export interface DocDiagDeps {
  log: (tag: string, step: string, detail?: unknown) => void;
  now?: () => number;
  slowMs?: number;
  /** OS process ids of the app's running processes (app.getAppMetrics). */
  liveProcesses?: () => ReadonlySet<number>;
  /** A repeating timer; returns its stop. Tests pass none and call sweep(). */
  every?: ((fn: () => void, ms: number) => () => void) | null;
  /** Seconds this process has been running. */
  uptime?: () => number;
}

interface Tracked {
  label: string;
  info: DocWindowInfo;
  wc: DocContents;
  opened: number;
  hungAt: number | null;
  pid: number;
  console: RateGate;
  requests: RateGate;
  frames: FrameWatch;
}

const CONSOLE_LEVELS = ['verbose', 'info', 'warning', 'error'];
const NO_PROCESSES: ReadonlySet<number> = new Set();

function defaultEvery(fn: () => void, ms: number): () => void {
  const h = setInterval(fn, ms);
  h.unref?.();
  return () => clearInterval(h);
}

/**
 * Watches the document windows: main.ts calls track() for each one it makes
 * and forwards the default session's webRequest events (wireWebRequest).
 *
 * ⚠ Every entry point swallows its own errors. These run inside Electron's
 * event and timer callbacks in the main process, where an exception is an
 * error dialog in front of the person - a diagnostic must never be the
 * thing that breaks the window it watches.
 */
export class DocDiagnostics {
  private seq = 0;
  private readonly windows = new Map<number, Tracked>();
  private readonly pending = new PendingRequests();
  private stopSweep: (() => void) | null = null;
  private readonly log: DocDiagDeps['log'];
  private readonly now: () => number;
  private readonly slowMs: number;
  private readonly liveProcesses: () => ReadonlySet<number>;
  private readonly every: DocDiagDeps['every'];
  private readonly uptime: () => number;
  private complained = false;

  constructor(deps: DocDiagDeps) {
    this.log = deps.log;
    this.now = deps.now ?? Date.now;
    this.slowMs = deps.slowMs ?? SLOW_MS;
    this.liveProcesses = deps.liveProcesses ?? (() => NO_PROCESSES);
    this.every = deps.every === undefined ? defaultEvery : deps.every;
    this.uptime = deps.uptime ?? (() => process.uptime());
  }

  /** Windows watched, for the tests. */
  get watching(): number {
    return this.windows.size;
  }

  /** In flight, for the tests. */
  get inFlight(): number {
    return this.pending.size;
  }

  /** One line; a field with no value is left out rather than written empty. */
  private write(tag: string, step: string, detail: Record<string, unknown>): void {
    const clean: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(detail)) if (v !== undefined) clean[k] = v;
    this.log(tag, step, clean);
  }

  track(wc: DocContents, info: DocWindowInfo): string {
    const t: Tracked = {
      label: `doc#${++this.seq}`,
      info,
      wc,
      opened: this.now(),
      hungAt: null,
      pid: 0,
      console: new RateGate(CONSOLE_LIMIT),
      requests: new RateGate(REQUEST_LIMIT),
      frames: new FrameWatch(),
    };
    this.windows.set(wc.id, t);
    this.write('docwin', 'opened', {
      win: t.label,
      kind: info.kind,
      name: info.name,
      uptimeMin: Math.round(this.uptime() / 60),
    });
    const on = (event: string, fn: (...args: any[]) => void) => wc.on(event, this.guard(fn));
    on('did-finish-load', () => {
      t.pid = wc.getOSProcessId() || t.pid;
    });
    on('unresponsive', () => {
      if (t.hungAt !== null) return;
      t.hungAt = this.now();
      this.write('docwin', 'unresponsive', { win: t.label, pid: t.pid || undefined });
    });
    on('responsive', () => {
      if (t.hungAt === null) return;
      this.write('docwin', 'responsive', { win: t.label, ms: this.now() - t.hungAt });
      t.hungAt = null;
    });
    on('render-process-gone', (_e: unknown, d: { reason?: string; exitCode?: number } | undefined) => {
      this.write('docwin', 'renderer gone', {
        win: t.label,
        reason: d?.reason,
        exitCode: d?.exitCode,
        pid: t.pid || undefined,
      });
    });
    on('did-fail-load', (_e: unknown, code: number, error: string, url: string, isMainFrame: boolean) => {
      // -3 is ERR_ABORTED: a navigation replaced by another, not a failure.
      if (code === -3) return;
      const detail = { code, error, url: redactUrl(String(url ?? ''), info.home), main: !!isMainFrame };
      const held = t.requests.pass(`load ${detail.url} ${code}`, this.now());
      if (held) this.write('docwin', 'load failed', { win: t.label, ...detail, ...heldFields(held) });
    });
    on('console-message', (e: unknown, ...legacy: unknown[]) => this.consoleMessage(t, e, legacy));
    on('destroyed', () => this.untrack(wc.id));
    this.ensureSweep();
    return t.label;
  }

  private consoleMessage(t: Tracked, e: unknown, legacy: unknown[]): void {
    // Electron 35+ hands the fields on the event; before, they came as
    // arguments (level 0-3). Both are read, so an Electron bump is not a
    // silent loss of every console line.
    const ev = (e ?? {}) as { level?: unknown; message?: unknown; lineNumber?: unknown; sourceId?: unknown; frame?: unknown };
    const level =
      typeof ev.level === 'string' ? ev.level : typeof legacy[0] === 'number' ? CONSOLE_LEVELS[legacy[0]] : '';
    if (level !== 'error') return;
    const home = t.info.home;
    const msg = redactText(String(ev.message ?? legacy[1] ?? ''), home);
    const held = t.console.pass(msg, this.now());
    if (!held) return;
    const line = Number(ev.lineNumber ?? legacy[2] ?? 0);
    const source = String(ev.sourceId ?? legacy[3] ?? '');
    let frame: string | undefined;
    try {
      const f = ev.frame as FrameLike | null | undefined;
      if (f && f.frameTreeNodeId !== t.wc.mainFrame.frameTreeNodeId) frame = redactUrl(f.url, home);
    } catch {
      /* a frame already gone: the line is still worth writing */
    }
    this.write('docwin', 'console error', {
      win: t.label,
      msg,
      src: source ? `${redactUrl(source, home)}:${line}` : undefined,
      frame,
      ...heldFields(held),
    });
  }

  private untrack(id: number): void {
    const t = this.windows.get(id);
    if (!t) return;
    this.windows.delete(id);
    this.pending.dropWindow(id);
    const consoleHeld = t.console.drain();
    const requestHeld = t.requests.drain();
    if (consoleHeld || requestHeld) {
      this.write('docwin', 'held back', {
        win: t.label,
        ...(consoleHeld ? { console: heldFields(consoleHeld) } : {}),
        ...(requestHeld ? { requests: heldFields(requestHeld) } : {}),
      });
    }
    const now = this.now();
    this.write('docwin', 'closed', {
      win: t.label,
      ms: now - t.opened,
      ...(t.hungAt !== null ? { unresponsiveMs: now - t.hungAt } : {}),
    });
    if (!this.windows.size && this.stopSweep) {
      this.stopSweep();
      this.stopSweep = null;
    }
  }

  private ensureSweep(): void {
    if (this.stopSweep || !this.every) return;
    this.stopSweep = this.every(this.guard(() => this.sweep()), SWEEP_MS);
  }

  private requestLine(t: Tracked, step: string, detail: Record<string, unknown>): void {
    const key = `${step} ${detail.method} ${detail.path} ${detail.status ?? detail.error ?? ''}`;
    const held = t.requests.pass(key, this.now());
    if (held) this.write('docreq', step, { win: t.label, ...detail, ...heldFields(held) });
  }

  // webRequest, forwarded by main.ts (wireWebRequest). Only the requests of a
  // watched window count; the main window's and the main process's are not
  // this file's business.

  requestStart(d: RequestDetails): void {
    this.guard(() => {
      const t = this.windows.get(d.webContentsId ?? -1);
      // ⚠ A websocket is open for as long as the page is: it would be
      // "waiting" every time. Its failure is still written (requestError).
      if (!t || d.resourceType === 'webSocket') return;
      this.pending.start({
        id: d.id,
        wc: d.webContentsId as number,
        method: d.method,
        path: redactUrl(d.url, t.info.home),
        type: d.resourceType ?? '',
        start: this.now(),
      });
    })();
  }

  requestResponse(d: RequestDetails): void {
    this.guard(() => {
      const p = this.pending.get(d.id);
      if (!p || p.responded) return;
      p.responded = true;
      const ms = this.now() - p.start;
      const t = this.windows.get(p.wc);
      if (t && ms >= this.slowMs) {
        this.requestLine(t, 'request slow', { method: p.method, path: p.path, type: p.type, status: d.statusCode, ms });
      }
    })();
  }

  requestDone(d: RequestDetails): void {
    this.guard(() => {
      const p = this.pending.end(d.id);
      const t = this.windows.get(d.webContentsId ?? p?.wc ?? -1);
      const status = d.statusCode ?? 0;
      // 404 is an answer pages ask for on purpose (is there a draft?).
      if (!t || status < 400 || status === 404) return;
      this.requestLine(t, 'request http', {
        method: d.method,
        path: p?.path ?? redactUrl(d.url, t.info.home),
        type: d.resourceType ?? '',
        status,
        ms: p ? this.now() - p.start : undefined,
      });
    })();
  }

  requestError(d: RequestDetails): void {
    this.guard(() => {
      const p = this.pending.end(d.id);
      const t = this.windows.get(d.webContentsId ?? p?.wc ?? -1);
      if (!t) return;
      const ms = p ? this.now() - p.start : undefined;
      const error = d.error ?? 'unknown';
      // ⚠ ERR_ABORTED is the page cancelling: a navigation, a closed player,
      // an aborted fetch. Written only for a request that had waited the
      // whole SLOW_MS with no answer - a hang the page gave up on.
      if (error === 'net::ERR_ABORTED' && !(p && !p.responded && (ms ?? 0) >= this.slowMs)) return;
      this.requestLine(t, 'request failed', {
        method: d.method,
        path: p?.path ?? redactUrl(d.url, t.info.home),
        type: d.resourceType ?? '',
        error,
        ms,
      });
    })();
  }

  /** The periodic look: requests waiting too long, frames whose process went. */
  sweep(): void {
    const now = this.now();
    for (const p of this.pending.overdue(now, this.slowMs)) {
      const t = this.windows.get(p.wc);
      if (t) this.requestLine(t, 'request waiting', { method: p.method, path: p.path, type: p.type, ms: now - p.start });
    }
    if (this.pending.evicted) {
      this.write('docreq', 'too many in flight, oldest forgotten', { count: this.pending.evicted, max: MAX_PENDING });
      this.pending.evicted = 0;
    }
    let live: ReadonlySet<number> | null = null;
    for (const t of this.windows.values()) {
      const frames = subframes(t.wc);
      if (frames.length) live ??= this.liveProcesses();
      for (const g of t.frames.check(frames, live ?? NO_PROCESSES)) {
        this.write('docwin', 'frame gone', { win: t.label, frame: redactUrl(g.url, t.info.home), pid: g.lastPid });
      }
    }
  }

  private guard<A extends unknown[]>(fn: (...args: A) => void): (...args: A) => void {
    return (...args: A) => {
      try {
        fn(...args);
      } catch (err) {
        if (this.complained) return;
        this.complained = true;
        try {
          this.write('docwin', 'diagnostics error (written once)', { error: redactText(String(err)) });
        } catch {
          /* the log is a courtesy */
        }
      }
    };
  }
}

/** The frames of a page that run (or ran) in a renderer other than the
 *  page's own. A frame that can no longer be read is `dead` (FrameWatch). */
function subframes(wc: DocContents): FrameSample[] {
  const frames: FrameSample[] = [];
  let main: FrameLike;
  let all: readonly FrameLike[];
  try {
    main = wc.mainFrame;
    all = main.framesInSubtree;
  } catch {
    return frames;
  }
  for (const f of all) {
    if (f === main) continue;
    let key: string;
    try {
      key = String(f.frameTreeNodeId);
    } catch {
      continue;
    }
    try {
      if (f.detached) {
        frames.push({ key, pid: 0, url: '', dead: true });
        continue;
      }
      if (f.processId === main.processId) continue;
      frames.push({ key, pid: f.osProcessId, url: f.url });
    } catch {
      frames.push({ key, pid: 0, url: '', dead: true });
    }
  }
  return frames;
}
