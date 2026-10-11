// The filex page's half of editing together (task #189, filex 0.56,
// lib/coeditSession): what AppFrame does with an interface's coedit.* calls.
//
// What has to hold, and why each test is here:
//   - joining waits a moment for a session's creator to store the base, and
//     the page derives the session's keys from the key the server hands it -
//     the app is told who it is, never a key;
//   - the page reads the WHOLE log, in order, through the reader that refuses
//     a log the writers did not write, and hands the app only the entries
//     after the place it subscribed from (held until it subscribes);
//   - an append is sealed at the place the page read last, chained to the
//     sealed entry before it; when somebody else's entry came first the page
//     reads it, seals again at the new place and sends it again - the app
//     sees one answer;
//   - a forged or missing entry stops the session (`broken`), the end of the
//     membership says `gone`;
//   - cursors are sealed, a few a second at most, and a replayed one is not
//     handed over; blobs are sealed under their name;
//   - a socket that drops is reconnected from the last entry read;
//   - a save names the session only up to what the page has read.
// All red before 0.56: lib/coeditSession did not exist.
import { beforeEach, describe, expect, it } from 'vitest';

import {
  CoEditSession,
  coeditFailure,
  type CoEditEvents,
  type CoEditSocket,
  type CoEditTransport,
} from '../../../packages/core/src/lib/coeditSession';
import { bytesToB64 } from '../../../packages/core/src/lib/e2ecrypto';
import {
  OFFICE_CHAIN_START,
  deriveSessionKeys,
  entryDigest,
  openSessionBlob,
  sealEntry,
  sealEph,
  type OfficeSessionKeys,
} from '../../../packages/core/src/lib/e2eoffice';
import type { CoEditEntry } from '../../../packages/app-ui/src/protocol';

const SID = '0123456789abcdef0123456789abcdef';

/** A refusal the way useFileApi throws one (requestFailure). */
function refusal(status: number, code: string, extra: Record<string, unknown> = {}) {
  return Object.assign(new Error(code), { status, code, detail: JSON.stringify({ error: code, ...extra }), said: true });
}

class FakeSocket implements CoEditSocket {
  readyState = 0;
  onopen: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: ((ev: unknown) => void) | null = null;
  onerror: ((ev: unknown) => void) | null = null;
  sent: Record<string, unknown>[] = [];
  closed = false;
  constructor(readonly url: string, private readonly relay: FakeRelay) {}
  send(data: string) {
    const m = JSON.parse(data);
    this.sent.push(m);
    if (m.type === 'subscribe') this.relay.subscribe(this, m.from);
  }
  close() {
    this.closed = true;
    this.readyState = 3;
  }
  deliver(frame: unknown) {
    this.onmessage?.({ data: JSON.stringify(frame) });
  }
  drop() {
    this.readyState = 3;
    this.onclose?.({});
  }
}

interface WireEntry {
  seq: number;
  kind: string;
  client: string;
  at: number;
  ctr?: number;
  ct?: string;
  member?: Record<string, unknown>;
  through?: number;
}

/** The relay and the server around it, in memory, as the page sees them. */
class FakeRelay {
  readonly raw = crypto.getRandomValues(new Uint8Array(32));
  keys!: OfficeSessionKeys;
  log: WireEntry[] = [];
  sockets: FakeSocket[] = [];
  subscribed = new Map<FakeSocket, number>();
  blobs = new Map<string, Uint8Array>();
  calls: { op: string; body: Record<string, unknown>; opts?: { keepalive?: boolean } }[] = [];
  notReadyTimes = 0;
  created = true;
  /** The digest the next member entry names (the writers' chain). */
  digest = OFFICE_CHAIN_START;
  otherCtr = 0;
  tickets = 0;

  async init() {
    this.keys = await deriveSessionKeys(new Uint8Array(this.raw), SID);
    this.push({ kind: 'join', client: 'c1', member: { client: 'c1', user: 'aa11', name: 'Ayşe', index_user: 1, can_edit: true } });
    return this;
  }

  push(e: Omit<WireEntry, 'seq' | 'at'>) {
    const entry = { seq: this.log.length + 1, at: 1_791_000_000_000 + this.log.length, ...e } as WireEntry;
    this.log.push(entry);
    for (const [s, from] of this.subscribed) if (entry.seq > from) s.deliver({ type: 'entry', entry });
    return entry;
  }

  subscribe(s: FakeSocket, from: number) {
    this.subscribed.set(s, from);
    for (const e of this.log.slice(from)) s.deliver({ type: 'entry', entry: e });
  }

  /** Another member writes (c2), sealed and chained the way its page would. */
  async other(kind: 'changes' | 'lock' | 'release', body: unknown) {
    const seq = this.log.length + 1;
    const ctr = ++this.otherCtr;
    const ct = await sealEntry(this.keys.log, { sid: SID, seq, client: 'c2', ctr, kind }, this.digest, body);
    this.digest = await entryDigest(ct);
    return this.push({ kind, client: 'c2', ctr, ct });
  }

  socket = (url: string) => {
    const s = new FakeSocket(url, this);
    this.sockets.push(s);
    queueMicrotask(() => {
      s.readyState = 1;
      s.onopen?.({});
    });
    return s;
  };

  transport(): CoEditTransport {
    return {
      join: async () => {
        if (this.notReadyTimes > 0) {
          this.notReadyTimes--;
          throw refusal(409, 'coedit_not_ready');
        }
        return {
          session: SID, created: this.created, head: this.log.length, changes_head: 0, saved_through: 0,
          me: { client: 'c1', user: 'aa11', name: 'Ayşe', index_user: 1, can_edit: true },
          key_mode: 'server', key: bytesToB64(new Uint8Array(this.raw)), token: 'tok-c1',
          socket: { url: 'wss://files.example.com/api/coedit/ws', ticket: `t${++this.tickets}` },
        };
      },
      call: async (_sid, op, body, opts) => {
        this.calls.push({ op, body, opts });
        if (op === 'append') {
          if (body.base !== this.log.length) throw refusal(409, 'coedit_conflict', { head: this.log.length });
          this.digest = await entryDigest(String(body.ct));
          const e = this.push({ kind: String(body.kind), client: 'c1', ctr: Number(body.ctr), ct: String(body.ct) });
          return { seq: e.seq };
        }
        if (op === 'lease') return { granted: true };
        if (op === 'ticket') return { url: 'wss://files.example.com/api/coedit/ws', ticket: `t${++this.tickets}` };
        return { left: true };
      },
      putBlob: async (_sid, name, _m, bytes) => {
        this.blobs.set(name, new Uint8Array(bytes));
      },
      getBlob: async (_sid, name) => {
        const b = this.blobs.get(name);
        if (!b) throw refusal(404, 'coedit_no_blob');
        return b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength) as ArrayBuffer;
      },
    };
  }
}

function recorder() {
  const entries: CoEditEntry[] = [];
  const cursors: { client: string; cursor: unknown }[] = [];
  const dropped: { from: number; reason: string }[] = [];
  const ev: CoEditEvents = {
    entry: (e) => entries.push(e),
    cursor: (c) => cursors.push(c),
    dropped: (d) => dropped.push(d),
  };
  return { entries, cursors, dropped, ev };
}

const settle = async () => {
  for (let i = 0; i < 20; i++) await new Promise((r) => setTimeout(r, 0));
};

async function joined(relay: FakeRelay, opts: Record<string, unknown> = {}) {
  const r = recorder();
  const s = await CoEditSession.join(relay.transport(), 'main://doc.docx', r.ev, { socket: relay.socket, cursorGapMs: 0, ...opts });
  await settle();
  return { s, ...r };
}

let relay: FakeRelay;
beforeEach(async () => {
  relay = await new FakeRelay().init();
});

describe('joining', () => {
  it('waits for a session’s creator, then answers who the app is - never a key', async () => {
    relay.notReadyTimes = 2;
    relay.created = false;
    const { s } = await joined(relay, { setTimeout: (fn: () => void) => setTimeout(fn, 0) });
    const h = s.hello();
    expect(h.session).toBe(SID);
    expect(h.created).toBe(false);
    expect(h.me).toEqual({ client: 'c1', user: 'aa11', name: 'Ayşe', indexUser: 1, canEdit: true });
    expect(JSON.stringify(h)).not.toContain(bytesToB64(new Uint8Array(relay.raw)));
    expect(relay.sockets).toHaveLength(1);
    expect(relay.sockets[0].url).toContain('ticket=t1');
    expect(relay.sockets[0].sent[0]).toEqual({ type: 'subscribe', from: 0 });
  });

  it('says why it cannot join as a code the app can branch on', async () => {
    const t = relay.transport();
    t.join = async () => {
      throw refusal(503, 'coedit_no_secret_key');
    };
    await expect(CoEditSession.join(t, 'main://doc.docx', recorder().ev, { socket: relay.socket })).rejects.toMatchObject({
      code: 'unavailable',
      message: 'coedit_no_secret_key',
    });
    expect(coeditFailure(refusal(409, 'coedit_no_lease'))).toMatchObject({ code: 'failed', message: 'no_lease' });
    expect(coeditFailure(refusal(409, 'coedit_blob_exists'))).toMatchObject({ code: 'failed', message: 'exists' });
    expect(coeditFailure(refusal(403, 'coedit_read_only'))).toMatchObject({ code: 'read_only' });
    expect(coeditFailure(new Error('/srv/files/secret path'))).toMatchObject({ code: 'failed', message: 'failed' });
  });
});

describe('the log', () => {
  it('hands the app the entries after its place, held until it subscribes, checked one by one', async () => {
    const { s, entries } = await joined(relay);
    await relay.other('lock', { blocks: ['p1'] });
    await settle();
    expect(entries, 'nothing before subscribe').toHaveLength(0);
    s.subscribe(1);
    expect(entries.map((e) => e.seq)).toEqual([2]);
    expect(entries[0]).toMatchObject({ kind: 'lock', client: 'c2', body: { blocks: ['p1'] } });
    relay.push({ kind: 'join', client: 'c3', member: { client: 'c3', user: 'bb22', name: 'Sam', index_user: 3, can_edit: false } });
    relay.push({ kind: 'saved', client: 'c2', through: 2 });
    await settle();
    expect(entries.slice(1)).toMatchObject([
      { seq: 3, kind: 'join', member: { client: 'c3', user: 'bb22', name: 'Sam', indexUser: 3, canEdit: false } },
      { seq: 4, kind: 'saved', through: 2 },
    ]);
  });

  it('stops at an entry that does not check out, and says so once', async () => {
    const { s, entries, dropped } = await joined(relay);
    s.subscribe(0);
    const e = await relay.other('lock', { blocks: ['p1'] });
    const forged = { ...e, seq: e.seq + 1, ct: e.ct };
    relay.log.push(forged);
    relay.sockets[0].deliver({ type: 'entry', entry: forged });
    await settle();
    expect(dropped).toEqual([{ from: 2, reason: 'broken' }]);
    expect(entries.map((x) => x.seq)).toEqual([1, 2]);
    await expect(s.append('lock', {})).rejects.toMatchObject({ code: 'unavailable', message: 'broken' });
  });

  it('says `gone` when the membership ends', async () => {
    const { dropped } = await joined(relay);
    relay.sockets[0].deliver({ type: 'ended', reason: 'timeout' });
    await settle();
    expect(dropped).toEqual([{ from: 1, reason: 'gone' }]);
  });
});

describe('appending', () => {
  it('seals at the place it read last, and after a conflict reads the entry that came first and seals again', async () => {
    const { s } = await joined(relay);
    s.subscribe(0);
    // c2 writes; the page has not read it yet when it appends.
    relay.subscribed.clear();
    const first = await relay.other('lock', { blocks: ['a'] });
    const answer = s.append('lock', { blocks: ['b'] });
    await settle();
    const tries = relay.calls.filter((c) => c.op === 'append');
    expect(tries[0].body.base, 'the place it had read').toBe(1);
    // The entry that came first reaches the page now.
    relay.sockets[0].deliver({ type: 'entry', entry: first });
    const r = await answer;
    expect(r.seq).toBe(3);
    const again = relay.calls.filter((c) => c.op === 'append');
    expect(again).toHaveLength(2);
    expect(again[1].body.base).toBe(2);
    expect(Number(again[1].body.ctr)).toBeGreaterThan(Number(again[0].body.ctr));
    expect(again[1].body).toMatchObject({ client: 'c1', token: 'tok-c1', kind: 'lock' });
  });

  it('refuses a kind members do not write', async () => {
    const { s } = await joined(relay);
    await expect(s.append('join' as never, {})).rejects.toMatchObject({ code: 'invalid' });
  });
});

describe('cursors and blobs', () => {
  it('seals a cursor for the others and opens theirs, not a replayed one', async () => {
    const { s, cursors } = await joined(relay);
    s.cursor({ x: 1 });
    s.cursor({ x: 2 });
    await settle();
    const sent = relay.sockets[0].sent.filter((m) => m.type === 'cursor');
    expect(sent).toHaveLength(1);
    expect(JSON.stringify(sent[0])).not.toContain('"x"');
    const ct = await sealEph(relay.keys.eph, { sid: SID, client: 'c2', ctr: 5 }, { x: 9 });
    relay.sockets[0].deliver({ type: 'eph', client: 'c2', ctr: 5, ct });
    relay.sockets[0].deliver({ type: 'eph', client: 'c2', ctr: 5, ct });
    await settle();
    expect(cursors).toEqual([{ client: 'c2', cursor: { x: 9 } }]);
  });

  it('seals a blob under its name, and refuses one that does not open', async () => {
    const { s } = await joined(relay);
    const bytes = new TextEncoder().encode('the base document');
    await s.putBlob('base', bytes.buffer.slice(0) as ArrayBuffer);
    const stored = relay.blobs.get('base')!;
    expect(new TextDecoder().decode(stored)).not.toContain('base document');
    expect(await openSessionBlob(relay.keys.blob, { sid: SID, name: 'base' }, stored)).not.toBeNull();
    expect(new TextDecoder().decode(await s.getBlob('base'))).toBe('the base document');
    relay.blobs.set('m.a.png', stored);
    await expect(s.getBlob('m.a.png'), 'sealed for another name').rejects.toMatchObject({ code: 'failed', message: 'forged' });
    await expect(s.getBlob('m.none.png')).rejects.toMatchObject({ code: 'not_found' });
    await expect(s.putBlob('../x', new ArrayBuffer(1))).rejects.toMatchObject({ code: 'invalid' });
  });
});

describe('the connection and saving', () => {
  it('reconnects with a fresh ticket from the last entry it read', async () => {
    const { s } = await joined(relay, { setTimeout: (fn: () => void) => setTimeout(fn, 0) });
    s.subscribe(0);
    await relay.other('lock', { blocks: ['a'] });
    await settle();
    relay.subscribed.clear();
    relay.sockets[0].drop();
    await settle();
    expect(relay.calls.some((c) => c.op === 'ticket')).toBe(true);
    expect(relay.sockets).toHaveLength(2);
    expect(relay.sockets[1].url).toContain('ticket=t2');
    expect(relay.sockets[1].sent[0]).toEqual({ type: 'subscribe', from: 2 });
  });

  it('names the session in a save only up to what it has read, and leaves with keepalive', async () => {
    const { s } = await joined(relay);
    expect(s.saveParams(1)).toEqual({ coedit: SID, client: 'c1', token: 'tok-c1', through: '1' });
    expect(s.saveParams(7), 'past what the page read').toBeNull();
    await s.leave({ keepalive: true });
    expect(relay.calls.at(-1)).toMatchObject({ op: 'leave', opts: { keepalive: true } });
    expect(relay.sockets[0].closed).toBe(true);
    expect(s.saveParams(1), 'gone').toBeNull();
  });
});
