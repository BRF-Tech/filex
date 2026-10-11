// Editing together, filex 0.56's second round (task #189, lib/coeditSession
// + lib/coeditOutbox):
//
//   - an encrypted folder: the member that starts the session makes its key
//     in this page and gives the server only the key sealed with the folder
//     key (…/key); a member that joins opens it with the folder key (or the
//     previous one, mid re-key); a wrong folder key is refused, never used;
//   - a change is kept in this browser until the relay placed it: a request
//     that got no answer is sent again by itself and the app sees one
//     answer; the change kept in an encrypted folder is sealed, never JSON;
//   - the changes of an earlier opening are sent at once when nobody changed
//     the document since, after the person's yes otherwise (the server's
//     words), thrown away on a no, kept when nobody answered;
//   - a lease request that got no answer is "not now", not an error.
//
// All red before this round: the session refused an encrypted folder's key,
// failed an append on the first lost request, and kept nothing.
//
// ⚠ Timers: `fast` below runs EVERY timer the session sets at once - its
// catch-up deadline too (until(): 15 s becomes 0 ms). Reading a sealed entry
// (openEntry + entryDigest) goes through WebCrypto, whose work runs on the
// thread pool, so a test that needs the page to read one before it goes on
// races that 0 ms deadline: under load (a wide vitest slice, the nightly
// chain) the deadline came first, recover() gave up and nothing was asked
// ("behind" red, 23 of 30 runs under load). Such a test stands the clock
// still (fake timers) and waits for the event itself, never for a number of
// ticks.
import { describe, expect, it, vi } from 'vitest';

import { CoEditSession, type CoEditEvents, type CoEditSocket, type CoEditTransport } from '../../../packages/core/src/lib/coeditSession';
import { memoryOutbox, openOutboxBody, type OutboxStore } from '../../../packages/core/src/lib/coeditOutbox';
import { bytesToB64 } from '../../../packages/core/src/lib/e2ecrypto';
import {
  OFFICE_CHAIN_START,
  deriveSessionKeys,
  entryDigest,
  openEntry,
  sealEntry,
  unwrapSessionKey,
  wrapSessionKey,
  type OfficeSessionKeys,
} from '../../../packages/core/src/lib/e2eoffice';

const SID = 'abcdef0123456789abcdef0123456789';
const DOC = 'd0c'.repeat(16);
const OWNER = 'owner-7';
const PATH = 'main://kasa/doc.docx';

function refusal(status: number, code: string, extra: Record<string, unknown> = {}) {
  return Object.assign(new Error(code), { status, code, detail: JSON.stringify({ error: code, ...extra }), said: true });
}
/** useFileApi's networkFailure: no status, said. */
function noAnswer() {
  return Object.assign(new Error('network'), { said: true, detail: 'Failed to fetch' });
}

async function folderKey(): Promise<CryptoKey> {
  return crypto.subtle.importKey('raw', crypto.getRandomValues(new Uint8Array(32)), { name: 'AES-GCM' }, false, ['encrypt', 'decrypt']);
}

interface Entry {
  seq: number;
  kind: string;
  client: string;
  at: number;
  ctr?: number;
  ct?: string;
  member?: Record<string, unknown>;
}

class Sock implements CoEditSocket {
  readyState = 0;
  onopen: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: ((ev: unknown) => void) | null = null;
  onerror: ((ev: unknown) => void) | null = null;
  constructor(private readonly relay: Relay) {}
  send(data: string) {
    const m = JSON.parse(data);
    if (m.type === 'subscribe') this.relay.subscribe(this, m.from);
  }
  close() {
    this.readyState = 3;
  }
  deliver(frame: unknown) {
    this.onmessage?.({ data: JSON.stringify(frame) });
  }
}

/** The server as the page sees it: a session of an ordinary or an encrypted folder. */
class Relay {
  keys: OfficeSessionKeys | null = null;
  raw = crypto.getRandomValues(new Uint8Array(32));
  log: Entry[] = [];
  socks = new Set<Sock>();
  calls: { op: string; body: Record<string, unknown> }[] = [];
  joins: Record<string, unknown>[] = [];
  /** folder: the key the starter stored (sealed), '' until then. */
  sealedKey = '';
  /** Requests that get no answer before the next one is answered. */
  noAnswer = 0;
  leaseGranted = true;
  /** false: the person may only read the file (a watcher). */
  canEdit = true;
  digest = OFFICE_CHAIN_START;
  changesHead = 0;
  /** While set, every request waits for it (an answer that has not come yet). */
  hold: Promise<void> | null = null;
  otherCtr = 0;
  constructor(
    readonly mode: 'server' | 'folder',
    readonly created: boolean,
  ) {}

  async init() {
    if (this.mode === 'server') this.keys = await deriveSessionKeys(new Uint8Array(this.raw), SID);
    this.push({ kind: 'join', client: 'c1', member: { client: 'c1', user: 'u1', name: 'Ayşe', index_user: 1, can_edit: true } });
    return this;
  }

  push(e: Omit<Entry, 'seq' | 'at'>) {
    const entry = { seq: this.log.length + 1, at: 1_791_000_000_000, ...e } as Entry;
    this.log.push(entry);
    if (entry.kind === 'changes') this.changesHead = entry.seq;
    for (const s of this.socks) s.deliver({ type: 'entry', entry });
    return entry;
  }

  /** Another member (c2) writes changes, sealed and chained as its page would. */
  async other(body: unknown) {
    const seq = this.log.length + 1;
    const ctr = ++this.otherCtr;
    const ct = await sealEntry(this.keys!.log, { sid: SID, seq, client: 'c2', ctr, kind: 'changes' }, this.digest, body);
    this.digest = await entryDigest(ct);
    return this.push({ kind: 'changes', client: 'c2', ctr, ct });
  }

  subscribe(s: Sock, from: number) {
    this.socks.add(s);
    for (const e of this.log.slice(from)) s.deliver({ type: 'entry', entry: e });
  }

  socket = () => {
    const s = new Sock(this);
    queueMicrotask(() => {
      s.readyState = 1;
      s.onopen?.({});
    });
    return s;
  };

  transport(): CoEditTransport {
    return {
      join: async (_path, extra) => {
        this.joins.push({ ...(extra ?? {}) });
        const ans: Record<string, unknown> = {
          session: SID, doc: DOC, owner: OWNER, created: this.created, head: this.log.length, changes_head: this.changesHead, saved_through: 0,
          me: { client: 'c1', user: 'u1', name: 'Ayşe', index_user: 1, can_edit: this.canEdit }, token: 'tok',
          key_mode: this.mode, socket: { url: 'wss://files.example.com/api/coedit/ws', ticket: 't' },
        };
        if (this.mode === 'server') ans.key = bytesToB64(new Uint8Array(this.raw));
        else ans.sealed_key = this.sealedKey;
        if (extra?.unsent) {
          ans.unsent_words = { behind: 'BEHIND', ended: 'ENDED', apply: 'APPLY', discard: 'DISCARD' };
        }
        return ans;
      },
      call: async (_sid, op, body) => {
        if (this.hold) await this.hold;
        if (this.noAnswer > 0) {
          this.noAnswer--;
          throw noAnswer();
        }
        this.calls.push({ op, body });
        if (op === 'key') {
          if (this.sealedKey) throw refusal(400, 'coedit_invalid');
          this.sealedKey = String(body.sealed);
          return { stored: true };
        }
        if (op === 'append') {
          if (!this.canEdit) throw refusal(403, 'coedit_read_only');
          if (body.base !== this.log.length) throw refusal(409, 'coedit_conflict', { head: this.log.length });
          this.digest = await entryDigest(String(body.ct));
          const e = this.push({ kind: String(body.kind), client: 'c1', ctr: Number(body.ctr), ct: String(body.ct) });
          return { seq: e.seq };
        }
        if (op === 'lease') return { granted: this.leaseGranted };
        if (op === 'ticket') return { url: 'wss://files.example.com/api/coedit/ws', ticket: 't2' };
        return { left: true };
      },
      putBlob: async () => undefined,
      getBlob: async () => new ArrayBuffer(0),
    };
  }
}

const ev = (): CoEditEvents & { dropped: (d: unknown) => void } => ({ entry: () => {}, cursor: () => {}, dropped: () => {} });
/** Waits (a retry, the lease's "again in a second") at once - and the catch-up deadline too (see the top). */
const fast = (fn: () => void) => setTimeout(fn, 0);
const settle = async () => {
  for (let i = 0; i < 30; i++) await new Promise((r) => setTimeout(r, 0));
};
/** The session's own timers (the 15 s catch-up deadline) on a clock that moves only when the test moves it. */
async function stillClock<T>(run: () => Promise<T>): Promise<T> {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
  try {
    return await run();
  } finally {
    vi.useRealTimers();
  }
}
/** An outbox that says when the page kept a change (the record is there, sealed or not). */
function watchedOutbox(): { store: OutboxStore; kept: Promise<void> } {
  const inner = memoryOutbox();
  let said: () => void = () => {};
  const kept = new Promise<void>((r) => (said = r));
  const store: OutboxStore = {
    ...inner,
    add: async (r) => {
      const id = await inner.add(r);
      said();
      return id;
    },
  };
  return { store, kept };
}

describe('an encrypted folder', () => {
  it('the starter makes the key here and gives the server only the sealed key', async () => {
    const relay = await new Relay('folder', true).init();
    const fmk = await folderKey();
    const s = await CoEditSession.join(relay.transport(), PATH, ev(), { socket: relay.socket, folder: () => ({ fmk, previous: null }) });
    expect(relay.joins[0]).toMatchObject({ e2e: true });
    const stored = relay.calls.find((c) => c.op === 'key');
    expect(stored?.body).toMatchObject({ client: 'c1', token: 'tok' });
    const sealed = String(stored?.body.sealed);
    expect(sealed).not.toBe('');
    // The server's copy opens only with the folder key, for this session and file.
    expect(await unwrapSessionKey(fmk, sealed, { sid: SID, file: DOC })).not.toBeNull();
    expect(await unwrapSessionKey(fmk, sealed, { sid: SID, file: 'another' })).toBeNull();
    expect(await unwrapSessionKey(await folderKey(), sealed, { sid: SID, file: DOC })).toBeNull();
    s.close();
  });

  it('a joiner opens the key with the folder key, never with another', async () => {
    const relay = await new Relay('folder', false).init();
    const fmk = await folderKey();
    const raw = crypto.getRandomValues(new Uint8Array(32));
    relay.sealedKey = await wrapSessionKey(fmk, raw.slice(), { sid: SID, file: DOC });
    const fresh = await folderKey();
    const s = await CoEditSession.join(relay.transport(), PATH, ev(), { socket: relay.socket, folder: () => ({ fmk: fresh, previous: fmk }) });
    expect(relay.calls.some((c) => c.op === 'key'), 'a joiner stores no key').toBe(false);
    s.close();
    await expect(
      CoEditSession.join(relay.transport(), PATH, ev(), { socket: relay.socket, folder: () => ({ fmk: fresh, previous: null }) }),
    ).rejects.toMatchObject({ code: 'failed', message: 'forged' });
    await expect(CoEditSession.join(relay.transport(), PATH, ev(), { socket: relay.socket, folder: () => null })).rejects.toMatchObject({
      code: 'unavailable',
      message: 'locked',
    });
  });
});

describe('a change is kept until the relay placed it', () => {
  it('a request that got no answer is sent again; the app sees one answer; the kept record goes once placed', async () => {
    const relay = await new Relay('server', true).init();
    const outbox = memoryOutbox();
    const s = await CoEditSession.join(relay.transport(), PATH, ev(), { socket: relay.socket, outbox, setTimeout: fast, retryMs: [0] });
    await settle();
    relay.noAnswer = 2;
    const placed = s.append('changes', { changes: ['typed'] });
    await settle();
    const r = await placed;
    expect(r.seq).toBe(2);
    expect(relay.calls.filter((c) => c.op === 'append')).toHaveLength(1);
    expect(await outbox.listPath(PATH)).toHaveLength(0);
  });

  it('a change of an encrypted folder is kept sealed, and opens again with the folder key', async () => {
    const relay = await new Relay('folder', true).init();
    const fmk = await folderKey();
    const { store: kept, kept: sealedOnce } = watchedOutbox();
    const s = await CoEditSession.join(relay.transport(), PATH, ev(), {
      socket: relay.socket, outbox: kept, folder: () => ({ fmk, previous: null }), setTimeout: fast,
    });
    await settle();
    let answer: () => void = () => {};
    relay.hold = new Promise<void>((r) => (answer = r));
    const appended = s.append('changes', { changes: ['secret words'] });
    // Sealing is WebCrypto work (the thread pool): waited for, not counted in ticks.
    await sealedOnce;
    // Before the answer the record is there, sealed.
    const rows = await kept.listPath(PATH);
    expect(rows).toHaveLength(1);
    expect(rows[0].sealed).toBe(true);
    expect(rows[0].body).not.toContain('secret words');
    expect(await openOutboxBody([fmk], DOC, rows[0].body)).toEqual({ changes: ['secret words'] });
    relay.hold = null;
    answer();
    await appended;
    expect(await kept.listPath(PATH), 'placed: no longer kept').toHaveLength(0);
  });

  it('a watcher keeps nothing: the relay refuses its change, nothing waits in the browser', async () => {
    const relay = await new Relay('server', false).init();
    relay.canEdit = false;
    const outbox = memoryOutbox();
    const s = await CoEditSession.join(relay.transport(), PATH, ev(), { socket: relay.socket, outbox, setTimeout: fast });
    expect(s.hello().me.canEdit).toBe(false);
    await expect(s.append('changes', { changes: ['x'] })).rejects.toMatchObject({ code: 'read_only' });
    expect(await outbox.listPath(PATH)).toHaveLength(0);
    s.close();
  });

  it('a lease request that got no answer is "not now"', async () => {
    const relay = await new Relay('server', true).init();
    const s = await CoEditSession.join(relay.transport(), PATH, ev(), { socket: relay.socket });
    relay.noAnswer = 1;
    expect(await s.lease('acquire', 0)).toBe(false);
  });
});

describe('the changes of an earlier opening', () => {
  function keptRecord(sid: string, changesBase: number, body: unknown) {
    return { doc: DOC, owner: OWNER, sid, client: 'c-earlier', path: PATH, changesBase, at: 1, body: JSON.stringify(body), sealed: false };
  }

  it('are sent at once when they are this session’s and nobody changed the document since', async () => {
    const relay = await new Relay('server', false).init();
    const outbox = memoryOutbox();
    await outbox.add(keptRecord(SID, 0, { changes: ['kept'] }));
    let asked = false;
    const s = await CoEditSession.join(relay.transport(), PATH, ev(), {
      socket: relay.socket, outbox, setTimeout: fast, askUnsent: async () => {
        asked = true;
        return true;
      },
    });
    expect(relay.joins[0]).toMatchObject({ unsent: { count: 1 } });
    expect(asked, 'nothing to ask').toBe(false);
    const appended = relay.log.find((e) => e.kind === 'changes');
    expect(appended, 'sent before the interface reads the log').toBeTruthy();
    const opened = await openEntry(relay.keys!.log, { sid: SID, seq: appended!.seq, client: 'c1', ctr: appended!.ctr!, kind: 'changes' }, appended!.ct!);
    expect(opened?.body).toEqual({ changes: ['kept'] });
    expect(s.hello(), 'the head the interface is handed has them in it').toMatchObject({ head: appended!.seq, changesHead: appended!.seq });
    expect(relay.calls.map((c) => c.op)).toEqual(expect.arrayContaining(['lease', 'append']));
    expect(await outbox.listPath(PATH)).toHaveLength(0);
    s.close();
  });

  it('ask the person when others changed the document since - in the server’s words - and do what they say', async () => {
    for (const [answer, sent, left] of [
      [true, 1, 0],
      [false, 0, 0],
      [null, 0, 1],
    ] as const) {
      const relay = await new Relay('server', false).init();
      const outbox = memoryOutbox();
      await outbox.add(keptRecord('another-session-0000000000000000', 0, { changes: ['mine'] }));
      const questions: string[] = [];
      const s = await CoEditSession.join(relay.transport(), PATH, ev(), {
        socket: relay.socket, outbox, setTimeout: fast, askUnsent: async (q) => {
          questions.push(`${q.text}|${q.apply}|${q.discard}`);
          return answer;
        },
      });
      expect(questions, String(answer)).toEqual(['ENDED|APPLY|DISCARD']);
      expect(relay.log.filter((e) => e.kind === 'changes' && e.client === 'c1'), String(answer)).toHaveLength(sent);
      expect(await outbox.listPath(PATH), String(answer)).toHaveLength(left);
      s.close();
    }
  });

  it('ask with the "behind" words when the session is the same but others wrote since', async () => {
    // The others' entry is sealed: the page reads it (WebCrypto) before it can
    // tell "behind" from "quiet". The clock stands still meanwhile, so the
    // question waits for the reading however busy the machine is.
    await stillClock(async () => {
      const relay = await new Relay('server', false).init();
      await relay.other({ changes: ['theirs'] });
      const outbox = memoryOutbox();
      await outbox.add(keptRecord(SID, 0, { changes: ['mine'] }));
      const questions: string[] = [];
      const s = await CoEditSession.join(relay.transport(), PATH, ev(), {
        socket: relay.socket, outbox, askUnsent: async (q) => {
          questions.push(q.text);
          return true;
        },
      });
      expect(questions).toEqual(['BEHIND']);
      expect(relay.log.filter((e) => e.kind === 'changes').map((e) => e.client)).toEqual(['c2', 'c1']);
      expect(await outbox.listPath(PATH)).toHaveLength(0);
      s.close();
    });
  });

  it('are kept, and nothing is asked, when the log does not come in time', async () => {
    await stillClock(async () => {
      const relay = await new Relay('server', false).init();
      await relay.other({ changes: ['theirs'] });
      const outbox = memoryOutbox();
      await outbox.add(keptRecord(SID, 0, { changes: ['mine'] }));
      // The socket opens but the log never comes.
      let subscribed: () => void = () => {};
      const reached = new Promise<void>((r) => (subscribed = r));
      relay.subscribe = (sock) => {
        relay.socks.add(sock);
        subscribed();
      };
      const questions: string[] = [];
      let joined = false;
      const joining = CoEditSession.join(relay.transport(), PATH, ev(), {
        socket: relay.socket, outbox, catchUpMs: 15_000, askUnsent: async (q) => {
          questions.push(q.text);
          return true;
        },
      }).then((s) => {
        joined = true;
        return s;
      });
      await reached;
      await vi.advanceTimersByTimeAsync(14_999);
      expect(joined, 'still waiting for the log').toBe(false);
      await vi.advanceTimersByTimeAsync(1);
      const s = await joining;
      expect(questions, 'nothing read, nothing to ask').toEqual([]);
      expect(relay.calls.map((c) => c.op), 'nothing sent').toEqual([]);
      expect(await outbox.listPath(PATH), 'kept for the next opening').toHaveLength(1);
      s.close();
    });
  });

  it('are left to the page that made them while it is open (another tab holds the member)', async () => {
    const relay = await new Relay('server', false).init();
    const outbox = memoryOutbox();
    await outbox.add(keptRecord(SID, 0, { changes: ['the other tab'] }));
    const otherTab = outbox.hold('c-earlier');
    const s = await CoEditSession.join(relay.transport(), PATH, ev(), { socket: relay.socket, outbox, setTimeout: fast });
    expect(relay.joins[0]?.unsent, 'not counted as this page’s').toBeUndefined();
    expect(relay.log.some((e) => e.kind === 'changes'), 'not sent twice').toBe(false);
    expect(await outbox.listPath(PATH)).toHaveLength(1);
    s.close();
    // That tab closed: the next opening sends it.
    otherTab();
    const again = await CoEditSession.join(relay.transport(), PATH, ev(), { socket: relay.socket, outbox, setTimeout: fast });
    expect(relay.log.filter((e) => e.kind === 'changes')).toHaveLength(1);
    expect(await outbox.listPath(PATH)).toHaveLength(0);
    again.close();
  });

  it('a page holds its member while it is in the session, and lets go when it leaves', async () => {
    const relay = await new Relay('server', false).init();
    const outbox = memoryOutbox();
    const s = await CoEditSession.join(relay.transport(), PATH, ev(), { socket: relay.socket, outbox, setTimeout: fast });
    const me = s.hello().me.client;
    expect(await outbox.live(me)).toBe(true);
    await s.leave();
    expect(await outbox.live(me)).toBe(false);
  });

  it('are another person’s to send, never this one’s', async () => {
    const relay = await new Relay('server', false).init();
    const outbox = memoryOutbox();
    await outbox.add({ ...keptRecord(SID, 0, { changes: ['theirs'] }), owner: 'somebody-else' });
    const s = await CoEditSession.join(relay.transport(), PATH, ev(), { socket: relay.socket, outbox, setTimeout: fast });
    expect(relay.log.some((e) => e.kind === 'changes')).toBe(false);
    expect(await outbox.listPath(PATH), 'left for its owner').toHaveLength(1);
    s.close();
  });
});
