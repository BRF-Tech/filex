// The filex page's half of encrypted office editing (task #189): the session
// key, the sealed log entries, and the reader that refuses a log the editors
// did not write.
//
// What has to hold, and why each test is here:
//   - the session key opens only under the folder key it was sealed with,
//     and only for its own session and file (a key the server moves to
//     another session must not open there);
//   - a sealed entry opens only at the place, from the writer, with the
//     counter and the kind it was sealed for: the relay decides the order,
//     but cannot change it unseen;
//   - the reader takes a log the writers produced and refuses the first entry
//     that is missing, moved, altered, replayed or of an unknown kind - and
//     everything after it, because editors that read different logs build
//     different documents.
import { beforeAll, describe, expect, it } from 'vitest';

import {
  OFFICE_CHAIN_START,
  OfficeLogReader,
  deriveSessionKeys,
  entryDigest,
  newSessionKey,
  openEntry,
  openEph,
  openSessionBlob,
  sealEntry,
  sealEph,
  sealSessionBlob,
  unwrapSessionKey,
  wrapSessionKey,
  type OfficeEntryHeader,
  type OfficeSessionKeys,
  type OfficeWireEntry,
} from '../../../packages/core/src/lib/e2eoffice';

async function folderKey(): Promise<CryptoKey> {
  const raw = crypto.getRandomValues(new Uint8Array(32));
  return crypto.subtle.importKey('raw', raw, { name: 'AES-GCM' }, false, ['encrypt', 'decrypt']);
}

const SID = '6f1c0a9e2b7d4e58a1c3f0d2b4e6a8c0';
const FILE = 'main/Projects/plan.docx';

let keys: OfficeSessionKeys;

beforeAll(async () => {
  keys = await deriveSessionKeys(newSessionKey(), SID);
});

describe('the session key', () => {
  it('opens under the folder key it was sealed with, and the keys it gives work', async () => {
    const fmk = await folderKey();
    const raw = newSessionKey();
    const wrapped = await wrapSessionKey(fmk, raw, { sid: SID, file: FILE });
    const opened = await unwrapSessionKey(fmk, wrapped, { sid: SID, file: FILE });
    expect(opened).not.toBeNull();
    const direct = await deriveSessionKeys(raw, SID);
    const h: OfficeEntryHeader = { sid: SID, seq: 1, client: 'c1', ctr: 1, kind: 'lock' };
    const ct = await sealEntry(direct.log, h, OFFICE_CHAIN_START, { blocks: ['p1'] });
    expect(await openEntry(opened!.log, h, ct)).toEqual({ prev: OFFICE_CHAIN_START, body: { blocks: ['p1'] } });
  });

  it('does not open under another folder key', async () => {
    const wrapped = await wrapSessionKey(await folderKey(), newSessionKey(), { sid: SID, file: FILE });
    expect(await unwrapSessionKey(await folderKey(), wrapped, { sid: SID, file: FILE })).toBeNull();
  });

  it('does not open for another session or another file', async () => {
    const fmk = await folderKey();
    const wrapped = await wrapSessionKey(fmk, newSessionKey(), { sid: SID, file: FILE });
    expect(await unwrapSessionKey(fmk, wrapped, { sid: 'another-session', file: FILE })).toBeNull();
    expect(await unwrapSessionKey(fmk, wrapped, { sid: SID, file: 'main/other.docx' })).toBeNull();
    expect(await unwrapSessionKey(fmk, 'not base64 !!', { sid: SID, file: FILE })).toBeNull();
  });

  it('is 32 bytes, and the derived keys cannot be read back', async () => {
    expect(newSessionKey()).toHaveLength(32);
    await expect(wrapSessionKey(await folderKey(), new Uint8Array(16), { sid: SID, file: FILE })).rejects.toThrow();
    for (const k of [keys.log, keys.eph, keys.blob]) {
      expect(k.extractable).toBe(false);
      await expect(crypto.subtle.exportKey('raw', k)).rejects.toThrow();
    }
  });
});

describe('a sealed entry', () => {
  const h: OfficeEntryHeader = { sid: SID, seq: 7, client: 'c2', ctr: 3, kind: 'changes' };
  const body = { changes: ['52;AgAAADEA'], start: true, end: true };

  it('opens with its own header', async () => {
    const ct = await sealEntry(keys.log, h, 'abc', body);
    expect(await openEntry(keys.log, h, ct)).toEqual({ prev: 'abc', body });
  });

  it.each([
    ['another place in the log', { seq: 8 }],
    ['another writer', { client: 'c3' }],
    ['another counter', { ctr: 4 }],
    ['another kind', { kind: 'lock' as const }],
    ['another session', { sid: 'x' }],
  ])('does not open at %s', async (_what, change) => {
    const ct = await sealEntry(keys.log, h, 'abc', body);
    expect(await openEntry(keys.log, { ...h, ...change }, ct)).toBeNull();
  });

  it('does not open under another key or once altered', async () => {
    const ct = await sealEntry(keys.log, h, 'abc', body);
    expect(await openEntry(keys.eph, h, ct)).toBeNull();
    const bytes = Uint8Array.from(atob(ct), (c) => c.charCodeAt(0));
    bytes[bytes.length - 1] ^= 1;
    expect(await openEntry(keys.log, h, btoa(String.fromCharCode(...bytes)))).toBeNull();
  });

  it('is not readable as a cursor frame or a blob', async () => {
    const ct = await sealEntry(keys.log, h, 'abc', body);
    expect(await openEph(keys.log, { sid: SID, client: 'c2', ctr: 3 }, ct)).toBeUndefined();
    const raw = Uint8Array.from(atob(ct), (c) => c.charCodeAt(0));
    expect(await openSessionBlob(keys.log, { sid: SID, name: 'base' }, raw)).toBeNull();
  });
});

describe('cursor frames and blobs', () => {
  it('round-trip, and only under their own name', async () => {
    const eph = await sealEph(keys.eph, { sid: SID, client: 'c1', ctr: 9 }, { cursor: 'x;1;2' });
    expect(await openEph(keys.eph, { sid: SID, client: 'c1', ctr: 9 }, eph)).toEqual({ cursor: 'x;1;2' });
    expect(await openEph(keys.eph, { sid: SID, client: 'c2', ctr: 9 }, eph)).toBeUndefined();

    const base = new TextEncoder().encode('DOCY;v10;0;KANARYA-7f3a9c ğüşıöç');
    const sealed = await sealSessionBlob(keys.blob, { sid: SID, name: 'base' }, base);
    expect(new TextDecoder().decode(sealed)).not.toContain('KANARYA');
    expect(await openSessionBlob(keys.blob, { sid: SID, name: 'base' }, sealed)).toEqual(base);
    expect(await openSessionBlob(keys.blob, { sid: SID, name: 'image1.png' }, sealed)).toBeNull();
  });
});

/** A log the way two writers produce it: sealed, chained, at their places. */
async function writeLog(): Promise<OfficeWireEntry[]> {
  const log: OfficeWireEntry[] = [];
  let prev = OFFICE_CHAIN_START;
  const ctr: Record<string, number> = {};
  const put = async (client: string, kind: 'changes' | 'lock' | 'release', body: unknown) => {
    ctr[client] = (ctr[client] ?? 0) + 1;
    const seq = log.length + 1;
    const ct = await sealEntry(keys.log, { sid: SID, seq, client, ctr: ctr[client], kind }, prev, body);
    log.push({ seq, kind, client, ctr: ctr[client], ct });
    prev = await entryDigest(ct);
  };
  log.push({ seq: 1, kind: 'join', client: 'c1' });
  log.push({ seq: 2, kind: 'join', client: 'c2' });
  await put('c1', 'lock', { blocks: ['p1'] });
  await put('c2', 'lock', { blocks: ['p2'] });
  await put('c1', 'changes', { changes: ['a'], start: true, end: true });
  log.push({ seq: log.length + 1, kind: 'saved', client: 'c1' });
  await put('c2', 'release', { deleteIndex: null, locks: true });
  log.push({ seq: log.length + 1, kind: 'leave', client: 'c2' });
  return log;
}

async function readAll(log: OfficeWireEntry[]) {
  const reader = new OfficeLogReader(keys.log, SID);
  const out = [];
  for (const e of log) out.push(await reader.read(e));
  return { reader, out };
}

describe('OfficeLogReader', () => {
  it('takes the log the writers wrote, relay entries included', async () => {
    const log = await writeLog();
    const { reader, out } = await readAll(log);
    expect(out.every((r) => r.ok)).toBe(true);
    expect(reader.head).toBe(log.length);
    expect(out[2]).toMatchObject({ ok: true, kind: 'lock', client: 'c1', body: { blocks: ['p1'] } });
    expect(reader.counter('c2')).toBe(2);
  });

  it('refuses a missing entry', async () => {
    const log = await writeLog();
    log.splice(3, 1);
    const { out } = await readAll(log);
    expect(out[3]).toEqual({ ok: false, seq: log[3].seq, problem: 'gap' });
  });

  it('refuses an entry moved to another place, even with its number changed to fit', async () => {
    const log = await writeLog();
    const [a, b] = [log[2], log[3]];
    log[2] = { ...b, seq: a.seq };
    log[3] = { ...a, seq: b.seq };
    const { out } = await readAll(log);
    expect(out[2]).toEqual({ ok: false, seq: 3, problem: 'forged' });
  });

  it('refuses an entry the server left out and renumbered around', async () => {
    // Drop entry 3 and pull every later entry one place forward, the way a
    // server hiding it would: the next entry names a predecessor nobody got,
    // and its place is no longer the one it was sealed for.
    const log = await writeLog();
    const tail = log.slice(3).map((e) => ({ ...e, seq: e.seq - 1 }));
    const shortened = [...log.slice(0, 2), ...tail];
    const { out } = await readAll(shortened);
    expect(out[2].ok).toBe(false);
  });

  it('refuses an entry that names the wrong predecessor', async () => {
    const log = await writeLog();
    const e = log[3];
    const ct = await sealEntry(keys.log, { sid: SID, seq: e.seq, client: e.client, ctr: e.ctr!, kind: 'lock' }, 'f'.repeat(64), {
      blocks: ['p2'],
    });
    log[3] = { ...e, ct };
    const { out } = await readAll(log);
    expect(out[3]).toEqual({ ok: false, seq: 4, problem: 'chain' });
  });

  it("refuses a writer's counter going back", async () => {
    const log = await writeLog();
    // c1 writes again with counter 1, correctly sealed and chained: only the
    // counter is wrong.
    const reader = new OfficeLogReader(keys.log, SID);
    for (const e of log) await reader.read(e);
    const seq = log.length + 1;
    const ct = await sealEntry(keys.log, { sid: SID, seq, client: 'c1', ctr: 1, kind: 'lock' }, reader.digest, { blocks: [] });
    expect(await reader.read({ seq, kind: 'lock', client: 'c1', ctr: 1, ct })).toEqual({ ok: false, seq, problem: 'replay' });
  });

  it('refuses a kind nobody writes, and an entry without its seal', async () => {
    const r1 = new OfficeLogReader(keys.log, SID);
    expect(await r1.read({ seq: 1, kind: 'chat', client: 'c1', ctr: 1, ct: 'AA==' })).toMatchObject({ problem: 'kind' });
    const r2 = new OfficeLogReader(keys.log, SID);
    expect(await r2.read({ seq: 1, kind: 'lock', client: 'c1', ctr: 1 })).toMatchObject({ problem: 'forged' });
  });

  it('after a refusal, refuses everything: the editors would no longer build one document', async () => {
    const log = await writeLog();
    const bad = [...log];
    bad[2] = { ...bad[2], ct: bad[3].ct };
    const { out } = await readAll(bad);
    expect(out.slice(2).every((r) => !r.ok)).toBe(true);
  });

  it('can start in the middle of a log, from a known point', async () => {
    const log = await writeLog();
    const full = new OfficeLogReader(keys.log, SID);
    for (const e of log.slice(0, 4)) await full.read(e);
    const resumed = new OfficeLogReader(keys.log, SID, full.head, full.digest);
    for (const e of log.slice(4)) expect((await resumed.read(e)).ok).toBe(true);
  });
});
