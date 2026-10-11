/**
 * coeditSession - the filex page's half of editing a file together (task
 * #189, filex 0.56): what AppFrame does with an interface's `coedit.*`
 * calls. Design: docs/APP-PLUGINS-API.md → Editing together; the relay is
 * backend/internal/coedit.
 *
 * The app never holds a key. This page:
 *
 *   - joins the file's session through the server (which says who the
 *     person is and whether they may write) and gets the session's key -
 *     the server's own for an ordinary folder (an encrypted folder's is
 *     sealed with the folder key, which waits for files:e2e-plaintext);
 *   - reads the WHOLE log from its socket, in order, through the reader that
 *     refuses a log the writers did not write (lib/e2eoffice
 *     OfficeLogReader: a gap, a swap, an altered or replayed entry), and hands
 *     the app the entries after the place it subscribed from;
 *   - seals what the app appends at the place it saw last, chained to the
 *     sealed entry before it, and when somebody else's entry came first,
 *     waits until it has read that one, seals again at the new place and
 *     sends it again - the app sees one answer;
 *   - seals cursors (not kept) and blobs (the base document, images), and
 *     opens the others';
 *   - reconnects by itself when the socket drops, from the last entry it
 *     read; the app is told only when the membership ended (`gone`) or the
 *     log stopped checking out (`broken`);
 *   - in an end-to-end encrypted folder (the host hands it the folder key,
 *     lib/appPlaintext) makes the session's key itself when it starts the
 *     session and seals it with the folder key for the server to keep
 *     (`…/key`), or opens the one the starter sealed: the server never holds
 *     a key it could open;
 *   - keeps every change the app appends in this browser until the relay
 *     placed it (lib/coeditOutbox): a request that gets no answer is sent
 *     again, by itself, when the connection is back, and what is left when
 *     the file is opened again is sent then - at once when nobody changed
 *     the document since, after the person's yes otherwise (the server's
 *     words).
 */
import type { CoEditDropped, CoEditEntry, CoEditHello, CoEditKind, CoEditMember } from '@brftech/filex-app-ui/protocol';
import { b64ToBytes } from './e2ecrypto';
import {
  deriveSessionKeys,
  newSessionKey,
  OfficeLogReader,
  openEph,
  openSessionBlob,
  sealEntry,
  sealEph,
  sealSessionBlob,
  unwrapSessionKey,
  wrapSessionKey,
  type OfficeSessionKeys,
  type OfficeWireEntry,
} from './e2eoffice';
import { BridgeFailure } from './appBridge';
import { memoryOutbox, openOutboxBody, sealOutboxBody, type OutboxRecord, type OutboxStore } from './coeditOutbox';

/** What the session needs of the server (useFileApi's coedit* calls). */
export interface CoEditTransport {
  /** `extra`: `e2e` (the page holds the folder key), `unsent` (what this
   *  browser kept of an earlier opening: the answer carries the words). */
  join(path: string, extra?: { e2e?: boolean; unsent?: { count: number } }): Promise<Record<string, unknown>>;
  call(sid: string, op: 'append' | 'lease' | 'ticket' | 'leave' | 'key', body: Record<string, unknown>, opts?: { keepalive?: boolean }): Promise<Record<string, unknown>>;
  putBlob(sid: string, name: string, member: { client: string; token: string }, bytes: Uint8Array): Promise<void>;
  getBlob(sid: string, name: string, member: { client: string; token: string }): Promise<ArrayBuffer>;
}

/** What the session tells the interface (AppFrame emits them). */
export interface CoEditEvents {
  entry(e: CoEditEntry): void;
  cursor(c: { client: string; cursor: unknown }): void;
  dropped(d: CoEditDropped): void;
}

/** A WebSocket, as far as the session uses one (a test hands a fake). */
export interface CoEditSocket {
  readonly readyState: number;
  onopen: ((ev: unknown) => void) | null;
  onmessage: ((ev: { data: unknown }) => void) | null;
  onclose: ((ev: unknown) => void) | null;
  onerror: ((ev: unknown) => void) | null;
  send(data: string): void;
  close(code?: number, reason?: string): void;
}

export interface CoEditOptions {
  /** Opens the session's socket (default: `new WebSocket`). */
  socket?: (url: string) => CoEditSocket;
  /** How long a join waits for a session's creator to store the base. */
  readyWaitMs?: number;
  /** How long an append waits to read the entry that came first. */
  catchUpMs?: number;
  /** The least time between two cursors this page sends. */
  cursorGapMs?: number;
  /** Timers (tests). */
  setTimeout?: (fn: () => void, ms: number) => unknown;
  clearTimeout?: (h: unknown) => void;
  /**
   * The folder key of the file's end-to-end encrypted folder while it is
   * unlocked in this tab (null: locked) - given for a file of an encrypted
   * folder only. The session's key is then sealed with it (KeyFolder), and
   * so are the changes kept in this browser.
   */
  folder?: () => { fmk: CryptoKey; previous: CryptoKey | null } | null;
  /** Where the changes not placed yet are kept (default: memory). */
  outbox?: OutboxStore;
  /**
   * Ask the person whether the changes of an earlier opening are applied:
   * true = apply, false = discard, null = not answered (kept). The words are
   * the server's. Absent: they are kept, not applied.
   */
  askUnsent?: (q: { text: string; apply: string; discard: string }) => Promise<boolean | null>;
  /** How long a request that got no answer waits before it is sent again. */
  retryMs?: number[];
}

/** The waits between two tries of a request that got no answer. */
const RETRY_MS = [1000, 2000, 4000, 8000, 15000, 30000];

/** A refusal that says nothing about the request: the connection, a busy or
 *  restarting server. The request is sent again. */
function transient(e: unknown): boolean {
  const r = refusalOf(e);
  // useFileApi's networkFailure: no answer at all (offline, a dropped
  // connection) - said, with no status. Anything else without a status is
  // not the connection's.
  if (!r) return (e as { said?: unknown })?.said === true;
  if (r.status === 0) return true;
  if (r.code === 'coedit_off' || r.code === 'coedit_no_secret_key') return false;
  return r.status === 502 || r.status === 503 || r.status === 504;
}

/** WebSocket.OPEN. */
const OPEN = 1;

/** The relay's refusal, as a server code (`coedit_conflict`…) and its body. */
interface Refusal {
  status: number;
  code: string;
  head?: number;
}

function refusalOf(e: unknown): Refusal | null {
  const f = (e ?? {}) as { status?: unknown; code?: unknown; detail?: unknown };
  if (typeof f.status !== 'number') return null;
  let head: number | undefined;
  if (typeof f.detail === 'string') {
    try {
      const body = JSON.parse(f.detail) as { head?: unknown };
      if (typeof body.head === 'number') head = body.head;
    } catch {
      /* not JSON: no head */
    }
  }
  return { status: f.status, code: typeof f.code === 'string' ? f.code : '', head };
}

/**
 * What the interface is told for a refusal of the relay: a bridge code and a
 * short word the app can branch on (`no_lease`, `log_full`, `not_ready`,
 * `exists`…) - never the server's sentence.
 */
export function coeditFailure(e: unknown): BridgeFailure {
  if (e instanceof BridgeFailure) return e;
  const r = refusalOf(e);
  if (!r) return new BridgeFailure('failed', 'failed');
  switch (r.code) {
    case 'coedit_read_only':
      return new BridgeFailure('read_only', 'read_only');
    case 'coedit_too_large':
      return new BridgeFailure('too_large', 'too_large');
    case 'coedit_no_blob':
      return new BridgeFailure('not_found', 'not_found');
    case 'coedit_blob_exists':
      return new BridgeFailure('failed', 'exists');
    case 'coedit_no_lease':
    case 'coedit_log_full':
    case 'coedit_save_order':
    case 'coedit_conflict':
      return new BridgeFailure('failed', r.code.slice('coedit_'.length));
    case 'coedit_gone':
    case 'coedit_not_ready':
    case 'coedit_nothing_to_watch':
    case 'coedit_busy':
      return new BridgeFailure('unavailable', r.code.slice('coedit_'.length));
    case 'coedit_off':
    case 'coedit_no_secret_key':
    case 'coedit_build':
    case 'coedit_file_changed':
    case 'coedit_full':
    case 'encrypted':
      return new BridgeFailure('unavailable', r.code);
    case 'not_granted':
      return new BridgeFailure('not_granted', 'not_granted');
    case 'coedit_invalid':
    case 'bad_json':
      return new BridgeFailure('invalid', 'invalid');
  }
  if (r.status === 404) return new BridgeFailure('not_found', r.code || 'not_found');
  return new BridgeFailure('failed', /^[a-z][a-z0-9_]{0,39}$/.test(r.code) ? r.code : 'failed');
}

const MEMBER_KINDS: readonly string[] = ['changes', 'lock', 'release'];

/** A kept change's body (JSON); undefined when it does not parse. */
function parseBody(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

/** A join entry's member, from the relay's words to the protocol's. */
function memberOf(m: unknown): CoEditMember | null {
  if (!m || typeof m !== 'object') return null;
  const o = m as Record<string, unknown>;
  if (typeof o.client !== 'string') return null;
  return {
    client: o.client,
    user: typeof o.user === 'string' ? o.user : '',
    name: typeof o.name === 'string' ? o.name : '',
    indexUser: typeof o.index_user === 'number' ? o.index_user : 0,
    canEdit: o.can_edit === true,
  };
}

/** One frame of the session's socket. */
interface SocketFrame {
  type?: string;
  entry?: {
    seq?: number;
    kind?: string;
    client?: string;
    at?: number;
    ctr?: number;
    ct?: string;
    member?: unknown;
    through?: number;
  };
  client?: string;
  ctr?: number;
  ct?: string;
  reason?: string;
}

/** A member's session, as the filex page holds it. One per joined frame. */
export class CoEditSession {
  private readonly reader: OfficeLogReader;
  private readonly t: Required<Pick<CoEditOptions, 'catchUpMs' | 'cursorGapMs'>> & CoEditOptions;
  /** The app subscribed from here (null: not yet - entries wait). */
  private from: number | null = null;
  /** Entries read before the app subscribed. */
  private held: CoEditEntry[] = [];
  /** Entries waiting to be read, in order (the reader takes one at a time). */
  private inbox: OfficeWireEntry[] = [];
  private reading = false;
  private sock: CoEditSocket | null = null;
  private closed = false;
  private broken = false;
  private ctr = 0;
  private ephCtr = 0;
  private readonly ephSeen = new Map<string, number>();
  private appending: Promise<unknown> = Promise.resolve();
  private readonly waiters: { head: number; done: () => void; fail: (e: BridgeFailure) => void }[] = [];
  /** What the socket said of each entry besides what the reader checks. */
  private readonly extras = new Map<number, { at: number; member?: unknown; through?: number }>();
  private cursorTimer: unknown = null;
  private cursorNext: { cursor: unknown } | null = null;
  private cursorLast = 0;
  private reconnectDelay = 1000;
  /** The seq of the last `changes` entry the reader took. */
  private changesSeen = 0;
  private readonly outbox: OutboxStore;
  private doc = '';
  private owner = '';
  /** The file's path as the host named it (where its kept changes are found). */
  private path = '';
  /** Releases this member's lock (lib/coeditOutbox `hold`). */
  private release: (() => void) | null = null;

  private constructor(
    private readonly transport: CoEditTransport,
    private readonly ev: CoEditEvents,
    private readonly sid: string,
    private readonly me: CoEditMember,
    private readonly token: string,
    private readonly keys: OfficeSessionKeys,
    private readonly info: Omit<CoEditHello, 'me' | 'session'>,
    opts: CoEditOptions,
  ) {
    this.reader = new OfficeLogReader(keys.log, sid);
    this.t = {
      catchUpMs: 15_000,
      cursorGapMs: 200,
      ...opts,
    };
    this.outbox = opts.outbox ?? memoryOutbox();
  }

  /**
   * Join (or start) the session of the file at `path`. A session whose
   * creator has not stored the base yet is asked again for a while, then
   * `unavailable` (`not_ready`).
   */
  static async join(transport: CoEditTransport, path: string, ev: CoEditEvents, opts: CoEditOptions = {}): Promise<CoEditSession> {
    const waitUntil = Date.now() + (opts.readyWaitMs ?? 20_000);
    const sleep = (ms: number) => new Promise<void>((resolve) => (opts.setTimeout ?? setTimeout)(resolve, ms));
    const outbox = opts.outbox ?? memoryOutbox();
    // What this browser kept of an earlier opening of this file: told to the
    // server, which answers the words of the question (whose they are is
    // checked once the answer names the person). A record whose member's
    // page is still open (another tab, holding the member's lock) is that
    // page's to send, not this one's.
    const listed = await outbox.listPath(path).catch(() => [] as OutboxRecord[]);
    const kept: OutboxRecord[] = [];
    for (const r of listed) if (!(await outbox.live(r.client).catch(() => false))) kept.push(r);
    const extra: { e2e?: boolean; unsent?: { count: number } } = {};
    if (opts.folder) extra.e2e = true;
    if (kept.length > 0) extra.unsent = { count: kept.length };
    let ans: Record<string, unknown>;
    for (;;) {
      try {
        ans = await transport.join(path, extra);
        break;
      } catch (e) {
        const r = refusalOf(e);
        if (r?.code === 'coedit_not_ready' && Date.now() < waitUntil) {
          await sleep(1000);
          continue;
        }
        throw coeditFailure(e);
      }
    }
    const sid = String(ans.session ?? '');
    const me = memberOf(ans.me);
    const token = String(ans.token ?? '');
    if (!/^[0-9a-f]{32}$/.test(sid) || !me || !token) throw new BridgeFailure('failed', 'failed');
    const doc = String(ans.doc ?? '');
    const keys = await CoEditSession.keysOf(transport, ans, sid, me.client, token, doc, opts);
    const s = new CoEditSession(transport, ev, sid, me, token, keys, {
      head: Number(ans.head ?? 0),
      changesHead: Number(ans.changes_head ?? 0),
      savedThrough: Number(ans.saved_through ?? 0),
      created: ans.created === true,
    }, opts);
    s.doc = doc;
    s.owner = String(ans.owner ?? '');
    s.path = path;
    s.release = outbox.hold(me.client);
    const socket = (ans.socket ?? {}) as { url?: unknown; ticket?: unknown };
    s.connect(String(socket.url ?? ''), String(socket.ticket ?? ''));
    const mine = kept.filter((r) => r.doc === s.doc && r.owner === s.owner && s.owner !== '');
    if (mine.length > 0) {
      const words = (ans.unsent_words ?? {}) as Record<string, unknown>;
      await s.recover(mine, Number(ans.head ?? 0), words);
    }
    return s;
  }

  /**
   * The session's three keys. An ordinary folder: the server's key, handed
   * over. An encrypted folder (KeyFolder): the member that started the
   * session makes the key here, seals it with the folder key and gives the
   * server only that; a member that joins opens the one the starter sealed.
   */
  private static async keysOf(
    transport: CoEditTransport,
    ans: Record<string, unknown>,
    sid: string,
    client: string,
    token: string,
    doc: string,
    opts: CoEditOptions,
  ): Promise<OfficeSessionKeys> {
    if (ans.key_mode === 'server' && typeof ans.key === 'string') {
      const raw = b64ToBytes(ans.key);
      try {
        return await deriveSessionKeys(raw, sid);
      } finally {
        raw.fill(0);
      }
    }
    if (ans.key_mode !== 'folder' || !opts.folder || !doc) throw new BridgeFailure('unavailable', 'encrypted');
    const folder = opts.folder();
    if (!folder) throw new BridgeFailure('unavailable', 'locked');
    const scope = { sid, file: doc };
    if (ans.created === true) {
      const raw = newSessionKey();
      try {
        const sealed = await wrapSessionKey(folder.fmk, raw, scope);
        try {
          await transport.call(sid, 'key', { client, token, sealed });
        } catch (e) {
          throw coeditFailure(e);
        }
        return await deriveSessionKeys(raw, sid);
      } finally {
        raw.fill(0);
      }
    }
    const sealed = typeof ans.sealed_key === 'string' ? ans.sealed_key : '';
    for (const fmk of [folder.fmk, folder.previous]) {
      if (!fmk || !sealed) continue;
      const keys = await unwrapSessionKey(fmk, sealed, scope);
      if (keys) return keys;
    }
    // Not the folder key it was sealed with, or not this session's.
    throw new BridgeFailure('failed', 'forged');
  }

  /**
   * The changes of an earlier opening, kept in this browser (lib/coeditOutbox):
   * sent at once when they are this session's and nobody changed the
   * document since; otherwise the person is asked (the server's words), and
   * they are sent on top of what is there now, or thrown away. Sent before
   * the interface reads the log, so its editor opens with them in it.
   */
  private async recover(mine: OutboxRecord[], head: number, words: Record<string, unknown>): Promise<void> {
    try {
      await this.until(head);
    } catch {
      // The log did not come in time: kept for the next opening.
      return;
    }
    const first = mine[0];
    const quiet = first.sid === this.sid && first.changesBase === this.changesSeen;
    let apply = quiet;
    if (!quiet) {
      const text = String((first.sid === this.sid ? words.behind : words.ended) ?? '');
      // No words (an older server) or nobody to ask: kept for an opening
      // that can ask.
      if (!text || !this.t.askUnsent) return;
      const answer = await this.t.askUnsent({ text, apply: String(words.apply ?? ''), discard: String(words.discard ?? '') }).catch(() => null);
      // Not answered (the frame went): kept.
      if (answer === null) return;
      apply = answer;
    }
    if (!apply) {
      for (const r of mine) if (r.id !== undefined) await this.outbox.remove(r.id).catch(() => undefined);
      return;
    }
    const folder = this.t.folder?.() ?? null;
    const keys = folder ? [folder.fmk, folder.previous].filter((k): k is CryptoKey => !!k) : [];
    let held = false;
    let placed = 0;
    try {
      for (const r of mine) {
        const body = r.sealed ? await openOutboxBody(keys, r.doc, r.body) : parseBody(r.body);
        if (r.id !== undefined) await this.outbox.remove(r.id).catch(() => undefined);
        if (body === undefined) continue;
        held = held || (await this.takeLease());
        if (!held) {
          // Somebody else is writing: kept for the next opening.
          await this.keep('changes', body);
          continue;
        }
        placed = (await this.append('changes', body)).seq;
      }
    } finally {
      if (held) await this.lease('release', this.changesSeen).catch(() => false);
    }
    if (placed > 0) {
      // The interface is told the log's head with them in it (CoEditHello
      // head): an editor that starts once it has read that far opens with
      // them, as part of the document, not as a change of its own to answer.
      await this.until(placed).catch(() => undefined);
      this.info.head = Math.max(this.info.head, placed);
      this.info.changesHead = Math.max(this.info.changesHead, placed);
    }
  }

  /** The lease for the changes being recovered, a few tries while another writer holds it. */
  private async takeLease(): Promise<boolean> {
    for (let i = 0; i < 10 && !this.closed; i++) {
      if (await this.lease('acquire', this.changesSeen).catch(() => false)) return true;
      await new Promise<void>((resolve) => (this.t.setTimeout ?? setTimeout)(resolve, 1000));
    }
    return false;
  }

  /** What `coedit.join` answers the interface. */
  hello(): CoEditHello {
    return { session: this.sid, me: { ...this.me }, ...this.info };
  }

  /** The query a save made in this session adds (`file.save`'s `through`). */
  saveParams(through: number): Record<string, string> | null {
    if (this.closed || !Number.isInteger(through) || through < 0 || through > this.reader.head) return null;
    return { coedit: this.sid, client: this.me.client, token: this.token, through: String(through) };
  }

  /** The entries after `from` go to the interface, in order, then each new one. */
  subscribe(from: number): void {
    if (!Number.isInteger(from) || from < 0) throw new BridgeFailure('invalid', 'from is an entry seq');
    this.from = from;
    const held = this.held;
    this.held = [];
    for (const e of held) if (e.seq > from) this.ev.entry(e);
  }

  /** Seal an entry at the place this page read last and have the relay place it. */
  append(kind: CoEditKind, body: unknown): Promise<{ seq: number }> {
    if (!MEMBER_KINDS.includes(kind)) return Promise.reject(new BridgeFailure('invalid', 'kind is changes, lock or release'));
    const run = this.appending.then(() => this.place(kind, body));
    this.appending = run.catch(() => undefined);
    return run;
  }

  private async place(kind: CoEditKind, body: unknown): Promise<{ seq: number }> {
    // A change is kept in this browser until the relay placed it: a lost
    // connection, a closed tab or a crash loses none (recover, on the next
    // opening). Locks and releases belong to this membership and are not.
    const kept = kind === 'changes' ? await this.keep(kind, body) : undefined;
    const forget = () => (kept !== undefined ? this.outbox.remove(kept).catch(() => undefined) : undefined);
    const waits = this.t.retryMs ?? RETRY_MS;
    let quiet = 0;
    for (let attempt = 0; attempt < 50; ) {
      if (this.closed || this.broken) throw new BridgeFailure('unavailable', this.broken ? 'broken' : 'gone');
      const base = this.reader.head;
      const ctr = ++this.ctr;
      const ct = await sealEntry(this.keys.log, { sid: this.sid, seq: base + 1, client: this.me.client, ctr, kind }, this.reader.digest, body);
      try {
        const r = await this.transport.call(this.sid, 'append', { client: this.me.client, token: this.token, kind, base, ctr, ct });
        await forget();
        return { seq: Number(r.seq ?? base + 1) };
      } catch (e) {
        const ref = refusalOf(e);
        if (ref?.code === 'coedit_conflict' && typeof ref.head === 'number') {
          // Somebody else's entry came first: read up to it, then seal again.
          attempt++;
          await this.until(ref.head);
          continue;
        }
        if (transient(e)) {
          // No answer (the connection, a server restarting): the same
          // change goes again once it is back. The interface waits.
          await new Promise<void>((resolve) => (this.t.setTimeout ?? setTimeout)(resolve, waits[Math.min(quiet, waits.length - 1)]));
          quiet++;
          continue;
        }
        if (ref?.code === 'coedit_gone') {
          // The membership ended with the change unplaced: it stays kept,
          // for the next opening to send.
          this.gone();
          throw coeditFailure(e);
        }
        await forget();
        throw coeditFailure(e);
      }
    }
    throw new BridgeFailure('unavailable', 'busy');
  }

  /** Keep a change in this browser (sealed in an encrypted folder); its record's id. */
  private async keep(kind: CoEditKind, body: unknown): Promise<number | undefined> {
    // A watcher writes nothing (the relay refuses it): nothing to keep.
    if (kind !== 'changes' || !this.doc || !this.owner || !this.me.canEdit) return undefined;
    const folder = this.t.folder?.() ?? null;
    let text: string;
    let sealed = false;
    if (this.t.folder) {
      // An encrypted folder's change is its plaintext: never kept in the
      // clear. A folder locked meanwhile: not kept (nothing to seal it with).
      if (!folder) return undefined;
      text = await sealOutboxBody(folder.fmk, this.doc, body);
      sealed = true;
    } else {
      text = JSON.stringify(body);
    }
    try {
      return await this.outbox.add({
        doc: this.doc, owner: this.owner, sid: this.sid, client: this.me.client, path: this.path, changesBase: this.changesSeen,
        at: Date.now(), body: text, sealed,
      });
    } catch {
      return undefined;
    }
  }

  /** Resolves once the reader has taken entry `head` (or fails after catchUpMs). */
  private until(head: number): Promise<void> {
    if (this.reader.head >= head) return Promise.resolve();
    return new Promise<void>((resolve, reject) => {
      const set = this.t.setTimeout ?? setTimeout;
      const clear = this.t.clearTimeout ?? ((h: unknown) => clearTimeout(h as ReturnType<typeof setTimeout>));
      const w = {
        head,
        done: () => {
          clear(timer);
          resolve();
        },
        fail: (e: BridgeFailure) => {
          clear(timer);
          reject(e);
        },
      };
      const timer = set(() => {
        const i = this.waiters.indexOf(w);
        if (i >= 0) this.waiters.splice(i, 1);
        reject(new BridgeFailure('unavailable', 'behind'));
      }, this.t.catchUpMs);
      this.waiters.push(w);
    });
  }

  /** The changes lease: one writer at a time, and only one that has seen every change. */
  async lease(op: 'acquire' | 'release', changesSeen: number): Promise<boolean> {
    if (op !== 'acquire' && op !== 'release') throw new BridgeFailure('invalid', 'op is acquire or release');
    try {
      const r = await this.transport.call(this.sid, 'lease', { client: this.me.client, token: this.token, op, changes_seen: changesSeen });
      return r.granted === true;
    } catch (e) {
      // No answer: not now (the editor asks again later).
      if (transient(e)) return false;
      if (refusalOf(e)?.code === 'coedit_gone') this.gone();
      throw coeditFailure(e);
    }
  }

  /** A cursor for the others: sealed, not kept, a few a second at most (the last one wins). */
  cursor(cursor: unknown): void {
    if (this.closed || !this.me.canEdit) return;
    this.cursorNext = { cursor };
    if (this.cursorTimer !== null) return;
    const wait = Math.max(0, this.cursorLast + this.t.cursorGapMs - Date.now());
    const set = this.t.setTimeout ?? setTimeout;
    this.cursorTimer = set(() => {
      this.cursorTimer = null;
      const next = this.cursorNext;
      this.cursorNext = null;
      if (next) void this.sendCursor(next.cursor);
    }, wait);
  }

  private async sendCursor(cursor: unknown): Promise<void> {
    if (!this.sock || this.sock.readyState !== OPEN) return;
    this.cursorLast = Date.now();
    const ctr = ++this.ephCtr;
    const ct = await sealEph(this.keys.eph, { sid: this.sid, client: this.me.client, ctr }, cursor);
    if (this.sock && this.sock.readyState === OPEN) this.sock.send(JSON.stringify({ type: 'cursor', ctr, ct }));
  }

  /** Seal and store a blob of the session (the base document, an image). */
  async putBlob(name: string, data: ArrayBuffer): Promise<void> {
    if (!/^[A-Za-z0-9._-]{1,128}$/.test(name)) throw new BridgeFailure('invalid', 'a blob name');
    const sealed = await sealSessionBlob(this.keys.blob, { sid: this.sid, name }, new Uint8Array(data));
    try {
      await this.transport.putBlob(this.sid, name, { client: this.me.client, token: this.token }, sealed);
    } catch (e) {
      throw coeditFailure(e);
    }
  }

  /** Read and open a blob of the session. */
  async getBlob(name: string): Promise<ArrayBuffer> {
    if (!/^[A-Za-z0-9._-]{1,128}$/.test(name)) throw new BridgeFailure('invalid', 'a blob name');
    let sealed: ArrayBuffer;
    try {
      sealed = await this.transport.getBlob(this.sid, name, { client: this.me.client, token: this.token });
    } catch (e) {
      throw coeditFailure(e);
    }
    const plain = await openSessionBlob(this.keys.blob, { sid: this.sid, name }, new Uint8Array(sealed));
    if (!plain) throw new BridgeFailure('failed', 'forged');
    return plain.buffer.slice(plain.byteOffset, plain.byteOffset + plain.byteLength) as ArrayBuffer;
  }

  /** Leave the session. `keepalive` for a page that is going away. */
  async leave(opts: { keepalive?: boolean } = {}): Promise<void> {
    if (this.closed) return;
    this.close();
    try {
      await this.transport.call(this.sid, 'leave', { client: this.me.client, token: this.token }, opts);
    } catch {
      // Gone already, or the page is going: the relay drops a member whose
      // page went away by itself.
    }
  }

  /** Stop: no socket, no event, nothing waits. */
  close(): void {
    this.closed = true;
    this.release?.();
    this.release = null;
    const s = this.sock;
    this.sock = null;
    if (s) {
      s.onclose = null;
      s.onmessage = null;
      try {
        s.close(1000);
      } catch {
        /* closed */
      }
    }
    if (this.cursorTimer !== null) (this.t.clearTimeout ?? ((h: unknown) => clearTimeout(h as ReturnType<typeof setTimeout>)))(this.cursorTimer);
    this.cursorTimer = null;
  }

  // -------------------------------------------------------------------------
  // The socket
  // -------------------------------------------------------------------------

  private connect(url: string, ticket: string): void {
    if (this.closed || !url || !ticket) return;
    const open = this.t.socket ?? ((u: string) => new WebSocket(u) as unknown as CoEditSocket);
    let sock: CoEditSocket;
    try {
      sock = open(`${url}${url.includes('?') ? '&' : '?'}ticket=${encodeURIComponent(ticket)}`);
    } catch {
      this.reconnectLater();
      return;
    }
    this.sock = sock;
    sock.onopen = () => {
      this.reconnectDelay = 1000;
      // Always the whole log from where this page has read: the reader
      // checks the chain from the start (the app's own `from` only says
      // which entries it is handed).
      sock.send(JSON.stringify({ type: 'subscribe', from: this.reader.head + this.inbox.length }));
    };
    sock.onmessage = (m) => this.onFrame(m.data);
    sock.onclose = () => {
      if (this.sock === sock) this.sock = null;
      this.reconnectLater();
    };
    sock.onerror = () => {
      /* onclose follows */
    };
  }

  private reconnectLater(): void {
    if (this.closed || this.broken) return;
    const delay = this.reconnectDelay;
    this.reconnectDelay = Math.min(this.reconnectDelay * 2, 30_000);
    (this.t.setTimeout ?? setTimeout)(() => void this.reconnect(), delay);
  }

  private async reconnect(): Promise<void> {
    if (this.closed || this.broken || this.sock) return;
    try {
      const r = await this.transport.call(this.sid, 'ticket', { client: this.me.client, token: this.token });
      this.connect(String(r.url ?? ''), String(r.ticket ?? ''));
    } catch (e) {
      if (refusalOf(e)?.code === 'coedit_gone') {
        this.gone();
        return;
      }
      this.reconnectLater();
    }
  }

  private onFrame(data: unknown): void {
    if (this.closed || typeof data !== 'string') return;
    let f: SocketFrame;
    try {
      f = JSON.parse(data) as SocketFrame;
    } catch {
      return;
    }
    switch (f.type) {
      case 'entry':
        if (f.entry) this.take(f.entry);
        return;
      case 'eph':
        void this.takeCursor(f);
        return;
      case 'ended':
        if (f.reason === 'broken') this.stopBroken();
        else this.gone();
        return;
    }
  }

  private take(e: NonNullable<SocketFrame['entry']>): void {
    if (typeof e.seq !== 'number' || typeof e.kind !== 'string' || typeof e.client !== 'string') return;
    const last = this.reader.head + this.inbox.length;
    // A reconnect may hand back an entry already read: it is the same one.
    if (e.seq <= last) return;
    this.inbox.push({ seq: e.seq, kind: e.kind, client: e.client, ctr: e.ctr, ct: e.ct });
    const extra = { at: typeof e.at === 'number' ? e.at : 0, member: e.member, through: e.through };
    this.extras.set(e.seq, extra);
    void this.drain();
  }

  private async drain(): Promise<void> {
    if (this.reading) return;
    this.reading = true;
    try {
      while (this.inbox.length > 0 && !this.closed && !this.broken) {
        const w = this.inbox.shift() as OfficeWireEntry;
        const extra = this.extras.get(w.seq) ?? { at: 0 };
        this.extras.delete(w.seq);
        const res = await this.reader.read(w);
        if (!res.ok) {
          this.stopBroken();
          return;
        }
        if (res.kind === 'changes') this.changesSeen = res.seq;
        const entry = this.entryOf(res.seq, res.kind, res.client, res.body, extra);
        if (entry) {
          if (this.from === null) this.held.push(entry);
          else if (entry.seq > this.from) this.ev.entry(entry);
        }
        for (const wt of [...this.waiters]) {
          if (this.reader.head >= wt.head) {
            this.waiters.splice(this.waiters.indexOf(wt), 1);
            wt.done();
          }
        }
      }
    } finally {
      this.reading = false;
    }
  }

  private entryOf(seq: number, kind: string, client: string, body: unknown, extra: { at: number; member?: unknown; through?: number }): CoEditEntry | null {
    const base = { seq, at: extra.at, client };
    switch (kind) {
      case 'join': {
        const member = memberOf(extra.member);
        return member ? { ...base, kind: 'join', member } : null;
      }
      case 'leave':
        return { ...base, kind: 'leave' };
      case 'saved':
        return { ...base, kind: 'saved', through: typeof extra.through === 'number' ? extra.through : 0 };
      case 'changes':
      case 'lock':
      case 'release':
        return { ...base, kind, body };
    }
    return null;
  }

  private async takeCursor(f: SocketFrame): Promise<void> {
    if (typeof f.client !== 'string' || typeof f.ctr !== 'number' || typeof f.ct !== 'string' || f.client === this.me.client) return;
    if (f.ctr <= (this.ephSeen.get(f.client) ?? 0)) return;
    // Claimed before it is opened: the same frame twice is one cursor.
    this.ephSeen.set(f.client, f.ctr);
    const cursor = await openEph(this.keys.eph, { sid: this.sid, client: f.client, ctr: f.ctr }, f.ct);
    if (cursor === undefined || this.closed) return;
    this.ev.cursor({ client: f.client, cursor });
  }

  /** The membership ended: say so once, stop. */
  private gone(): void {
    if (this.closed) return;
    const from = this.reader.head;
    this.close();
    this.rejectWaiters('gone');
    this.ev.dropped({ from, reason: 'gone' });
  }

  /** The log stopped checking out: the editors would build different documents. */
  private stopBroken(): void {
    if (this.broken || this.closed) return;
    this.broken = true;
    const from = this.reader.head;
    this.close();
    this.rejectWaiters('broken');
    this.ev.dropped({ from, reason: 'broken' });
  }

  private rejectWaiters(why: string): void {
    for (const w of this.waiters.splice(0)) w.fail(new BridgeFailure('unavailable', why));
  }
}
