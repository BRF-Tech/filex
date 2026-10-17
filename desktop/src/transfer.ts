// A working copy of "Open with filex" to the server and back, in parts.
//
// ⚠ The server's own resumable paths, not a third one:
//
//   up    the STAGED upload every filex client sends a large file with - the
//         explorer (packages/core useUploadChunked), the CLI and the folder
//         sync engine (cliclient.uploadFile) - `/api/files/upload/*`
//         (docs/UPLOADS.md → The protocol): begin, one PUT per part on the
//         grid the server hands out, the server's offset as the only resume
//         point, commit, and the transfer op waited for;
//   down  ranged reads of the download the app already uses (`Range:
//         bytes=A-B`, http.ServeContent on the server), each part checked to
//         be the part asked for, `If-Range` so a file saved again in the
//         middle comes back whole instead of spliced.
//
// A dropped part costs that part: the upload asks the server where it got to
// and goes on from there, the download asks for the same range again. The
// ceiling is the server's own upload limit (capabilities `upload_limit_mb`),
// not a number of this app's.
//
// ⚠ STREAMED both ways: the bytes are never held whole. Up, they are read
// from a SOURCE one part at a time (openwith.ts DocumentReader reads the
// person's document); down, every answer is poured into a SINK as it arrives
// (spool.ts openSpool writes a file of the app's own) and hashed on the way.
// What is held at any moment is one part, whatever the size of the file.
//
// ⚠ PURE: the requests are a transport the caller hands in (openwith-io.ts
// gives Electron's net), so every rule here is measured without a server
// (desktop/test/transfer.test.ts, desktop/test/openwith-stream.test.ts).

import crypto from 'node:crypto';

/** The part size the client asks for (the server's FILEX_UPLOAD_CHUNK_SIZE
 *  default). The server's answer at `begin` is binding. Also the size of one
 *  ranged read, and the size from which an upload goes staged. */
export const PART_BYTES = 8 * 1024 * 1024;

/** One answer: the status, and the body parsed as JSON (null when it is not). */
export interface Answer {
  status: number;
  json: unknown;
}

/** The staged upload's five calls, and the op tray (docs/UPLOADS.md). */
export interface StagedTransport {
  begin(body: Record<string, unknown>): Promise<Answer>;
  /** One part: bytes `start` up to (not including) `end` of `total`. */
  put(id: string, start: number, end: number, total: number, body: Uint8Array): Promise<Answer>;
  status(id: string): Promise<Answer>;
  commit(id: string): Promise<Answer>;
  abort(id: string): Promise<void>;
  op(opId: number): Promise<Answer>;
}

/** A transfer that did not happen, and why - said to the person as it is. */
export class TransferError extends Error {
  readonly status: number | null;
  /** The server has no staged path (404/501 at begin): nothing was sent, the
   *  one-request upload may be used instead. */
  readonly stagedUnsupported: boolean;
  constructor(message: string, status: number | null = null, stagedUnsupported = false) {
    super(message);
    this.name = 'TransferError';
    this.status = status;
    this.stagedUnsupported = stagedUnsupported;
  }
}

function field(json: unknown, ...names: string[]): unknown {
  if (!json || typeof json !== 'object') return undefined;
  for (const n of names) {
    const v = (json as Record<string, unknown>)[n];
    if (v !== undefined && v !== null) return v;
  }
  return undefined;
}

function num(json: unknown, ...names: string[]): number | null {
  const v = field(json, ...names);
  return typeof v === 'number' && Number.isFinite(v) ? v : null;
}

/** What a refusal says: the server's own words when it sent some. */
function refusal(what: string, a: Answer): string {
  const said = field(a.json, 'error', 'message');
  return `${what}: ${typeof said === 'string' && said ? said : 'server said ' + a.status}`;
}

const ok = (s: number) => s >= 200 && s < 300;
/** Answers a retry can change: a part the connection cut short (400
 *  SHORT_CHUNK), a conflict on the grid (409), the server's own trouble. */
const transient = (s: number) => s === 400 || s === 409 || s === 408 || s === 429 || s >= 500;

/**
 * Where an upload reads its bytes from, a part at a time.
 *
 * ⚠ A source that cannot give a part as it was (the file changed under the
 * upload, it cannot be read) THROWS, and the upload ends there - the staging
 * released, nothing committed: a file half old and half new is never put
 * on the server. `verify`, when there is one, is asked once every byte is
 * up and before the commit, and refuses the same way.
 */
export interface UploadSource {
  readonly size: number;
  /** Bytes `start` up to (not including) `end`. */
  read(start: number, end: number): Promise<Uint8Array>;
  verify?(): Promise<void>;
}

/** Bytes already in hand, as a source. */
export function bytesSource(bytes: Uint8Array): UploadSource {
  return { size: bytes.byteLength, read: async (start, end) => bytes.subarray(start, end) };
}

export interface UploadOptions {
  /** The destination folder, `storage://path`. */
  dir: string;
  name: string;
  /** What is sent: a source read part by part, or bytes in hand. */
  source?: UploadSource;
  bytes?: Uint8Array;
  mime?: string;
  /** `<hex>`: declared at begin, verified by the server over the assembled file. */
  sha256?: string;
  chunkSize?: number;
  /** Failures in a row, with no byte gained, before the upload is given up. */
  maxFailures?: number;
  /** How long the transfer to the storage may take after the commit. */
  maxOpWaitMs?: number;
  sleep?: (ms: number) => Promise<void>;
  onProgress?: (sent: number, total: number) => void;
}

export interface UploadOutcome {
  opId: number | null;
  nodeId: number | null;
  /** How many times the server was asked where to go on from after a failure. */
  resumed: number;
}

const defaultSleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

/**
 * Sends the source to `<dir>/<name>` over the staged protocol and waits until
 * the storage holds it.
 *
 * ⚠ The offset comes from the SERVER. A part can fail after its bytes landed
 * (the answer was lost): after any failure the server is asked where it got
 * to, and the upload goes on from there - never from this side's own count,
 * and never from zero. A refusal no retry changes (permission, quota, disk)
 * ends it at once, and the server's staging is released; so is it when the
 * upload is given up, and when the source refuses a part (UploadSource).
 */
export async function uploadStaged(t: StagedTransport, o: UploadOptions): Promise<UploadOutcome> {
  const sleep = o.sleep ?? defaultSleep;
  const src = o.source ?? (o.bytes ? bytesSource(o.bytes) : null);
  if (!src) throw new TransferError('nothing to upload');
  const total = src.size;
  const maxFailures = Math.max(1, o.maxFailures ?? 6);

  let begun: Answer;
  try {
    begun = await t.begin({
      path: o.dir,
      name: o.name,
      size: total,
      mime: o.mime || 'application/octet-stream',
      ...(o.sha256 ? { hash: 'sha256:' + o.sha256 } : {}),
      chunk_size: o.chunkSize ?? PART_BYTES,
    });
  } catch (err) {
    throw new TransferError('could not start the upload: ' + String((err as Error)?.message ?? err));
  }
  if (begun.status === 404 || begun.status === 501) {
    throw new TransferError(refusal('this server takes no staged upload', begun), begun.status, true);
  }
  if (!ok(begun.status)) throw new TransferError(refusal('the server refused the upload', begun), begun.status);
  const id = field(begun.json, 'id');
  if (typeof id !== 'string' || !id) throw new TransferError('the server started no upload (no id)');
  const chunk = num(begun.json, 'chunk_size', 'chunkSize') ?? o.chunkSize ?? PART_BYTES;
  if (chunk <= 0) throw new TransferError('the server handed out no part size');

  let offset = num(begun.json, 'offset') ?? 0;
  let resumed = 0;
  let failures = 0;
  const giveUp = async (err: Error): Promise<never> => {
    await t.abort(id).catch(() => undefined);
    throw err;
  };
  const asError = (err: unknown) => (err instanceof Error ? err : new TransferError(String(err)));
  o.onProgress?.(offset, total);

  while (offset < total) {
    const end = Math.min(offset + chunk, total);
    // ⚠ Outside the try below: a source that refuses is not a dropped
    // connection, and is never retried.
    let body: Uint8Array;
    try {
      body = await src.read(offset, end);
    } catch (err) {
      return giveUp(asError(err));
    }
    if (body.byteLength !== end - offset) {
      return giveUp(new TransferError(`the file gave ${body.byteLength} bytes for a part of ${end - offset}`));
    }
    let answer: Answer | null = null;
    try {
      answer = await t.put(id, offset, end, total, body);
    } catch {
      answer = null; // the connection: the part may or may not have landed
    }
    if (answer && ok(answer.status)) {
      const next = num(answer.json, 'offset') ?? end;
      if (next > offset) {
        offset = next;
        failures = 0;
        o.onProgress?.(offset, total);
        continue;
      }
    } else if (answer && !transient(answer.status)) {
      return giveUp(new TransferError(refusal('the server refused a part of the upload', answer), answer.status));
    }
    // A failure, or an answer that moved nothing: where did the server get to?
    if (++failures >= maxFailures) {
      return giveUp(new TransferError(`the upload stopped at ${offset} of ${total} bytes`));
    }
    await sleep(250 * failures);
    try {
      const st = await t.status(id);
      if (ok(st.status)) {
        const at = num(st.json, 'offset');
        if (at !== null && at !== offset) {
          if (at > offset) failures = 0;
          offset = at;
          o.onProgress?.(offset, total);
        }
        resumed++;
      } else if (!transient(st.status)) {
        return giveUp(new TransferError(refusal('the upload is gone from the server', st), st.status));
      }
    } catch {
      /* the server cannot be asked either: the same part is tried again */
    }
  }

  // Every byte is up: is it still the file it was meant to be?
  if (src.verify) {
    try {
      await src.verify();
    } catch (err) {
      return giveUp(asError(err));
    }
  }

  // The commit is safe to repeat (docs/UPLOADS.md: a failed commit keeps the staging).
  let committed: Answer | null = null;
  for (let attempt = 1; attempt <= maxFailures; attempt++) {
    try {
      committed = await t.commit(id);
    } catch {
      committed = null;
    }
    if (committed && ok(committed.status)) break;
    if (committed && !transient(committed.status)) {
      return giveUp(new TransferError(refusal('the server refused the finished upload', committed), committed.status));
    }
    if (attempt === maxFailures) return giveUp(new TransferError('the finished upload could not be committed'));
    await sleep(250 * attempt);
  }
  const opId = num(committed!.json, 'op_id', 'opId');
  const nodeId = num(committed!.json, 'node_id', 'nodeId');
  if (opId !== null) await waitForTransfer(t, opId, { sleep, maxWaitMs: o.maxOpWaitMs ?? 30 * 60_000 });
  return { opId, nodeId, resumed };
}

/**
 * Waits until the commit's transfer to the storage is done: `ok` is the file
 * on the storage, `failed` an upload that did not happen (said, as the
 * explorer says it). A tray that cannot be read a few times in a row ends the
 * wait - the file is listed and readable from staging already.
 */
async function waitForTransfer(
  t: StagedTransport,
  opId: number,
  o: { sleep: (ms: number) => Promise<void>; maxWaitMs: number },
): Promise<void> {
  const until = Date.now() + o.maxWaitMs;
  let delay = 200;
  let misses = 0;
  for (;;) {
    let a: Answer | null = null;
    try {
      a = await t.op(opId);
    } catch {
      a = null;
    }
    const state = a && ok(a.status) ? field(a.json, 'status') : undefined;
    if (state === 'ok') return;
    if (state === 'failed' || state === 'partial') {
      const said = field(a!.json, 'error');
      throw new TransferError('the server could not store the upload: ' + (typeof said === 'string' && said ? said : 'transfer failed'));
    }
    if (state === undefined && ++misses >= 6) return;
    if (state !== undefined) misses = 0;
    if (Date.now() > until) throw new TransferError('the server is still storing the upload; try again in a while');
    await o.sleep(delay);
    delay = Math.min(delay * 2, 2000);
  }
}

// ── one document up: which way ──────────────────────────────────────

/** The two ways a document goes up besides the staged one. */
export interface DocumentTransport {
  staged: StagedTransport;
  /** One request with the bytes in hand: a small file. */
  whole(body: Uint8Array): Promise<void>;
  /** One request read from the source as it is sent: a large file on a
   *  server with no staged path. It asks `verify` before the body ends. */
  streamed(src: UploadSource): Promise<void>;
}

export interface SendOptions extends Omit<UploadOptions, 'source' | 'bytes'> {
  /** The server's own upload limit in bytes; 0 is none. */
  limitBytes?: number;
}

/**
 * A document, to `<dir>/<name>`, the way the explorer sends a file: one
 * request when it is small (PART_BYTES or less - the only size whose bytes
 * are held at once, and checked against the source before they are sent),
 * the STAGED upload in parts above. A server with no staged path takes a
 * large one in one request, streamed. The ceiling is the server's own upload
 * limit; a file over it is refused before a byte is read. Which way it went.
 */
export async function sendDocument(
  t: DocumentTransport,
  src: UploadSource,
  o: SendOptions,
): Promise<'whole' | 'staged' | 'streamed'> {
  const limit = o.limitBytes ?? 0;
  if (limit > 0 && src.size > limit) {
    throw new TransferError('the document is larger than this server takes (' + Math.round(limit / (1024 * 1024)) + ' MB)');
  }
  if (src.size <= PART_BYTES) {
    const body = await src.read(0, src.size);
    if (body.byteLength !== src.size) throw new TransferError(`the file gave ${body.byteLength} of ${src.size} bytes`);
    await src.verify?.();
    await t.whole(body);
    return 'whole';
  }
  try {
    await uploadStaged(t.staged, { ...o, source: src });
    return 'staged';
  } catch (err) {
    if (!(err instanceof TransferError && err.stagedUnsupported)) throw err;
  }
  await t.streamed(src);
  return 'streamed';
}

// ── the way back: ranged reads ──────────────────────────────────────

/** One ranged read's answer. */
export interface RangeAnswer {
  status: number;
  contentRange?: string;
  lastModified?: string;
  /** The body's length as the server declared it (Content-Length), when it did. */
  contentLength?: number;
  /** The body: in hand, or streamed as it arrives. */
  body: Uint8Array | AsyncIterable<Uint8Array>;
  /** The body will not be read (any further): let the connection go. */
  discard?: () => void;
}

/** A GET with `Range` (and `If-Range` once a first part said the version). */
export type RangeTransport = (range: string, ifRange?: string) => Promise<RangeAnswer>;

/** Where a download goes, at positions (spool.ts openSpool is one). */
export interface RangeSink {
  /** What was written goes: a whole new version is coming. */
  restart(): Promise<void>;
  write(at: number, data: Uint8Array): Promise<void>;
}

export interface DownloadOptions {
  chunkSize?: number;
  /** The largest file this side will hold. */
  maxBytes?: number;
  maxFailures?: number;
  sleep?: (ms: number) => Promise<void>;
}

/** What a download put in its sink. */
export interface Downloaded {
  size: number;
  sha256: string;
}

/** `bytes A-B/T` → its numbers; null for anything else. */
export function parseContentRange(v: string | undefined): { start: number; end: number; total: number } | null {
  const m = /^bytes (\d+)-(\d+)\/(\d+)$/.exec(String(v ?? '').trim());
  if (!m) return null;
  return { start: Number(m[1]), end: Number(m[2]), total: Number(m[3]) };
}

async function* chunksOf(body: RangeAnswer['body']): AsyncGenerator<Uint8Array> {
  if (body instanceof Uint8Array) {
    if (body.byteLength) yield body;
    return;
  }
  yield* body;
}

/**
 * Pours an answer's body into the sink from `pos`, hashing it into `h`.
 * `null`: the connection broke. `over`: more came than `max` (the rest is
 * let go). A sink that cannot write THROWS - a full disk is not a dropped
 * connection.
 */
async function pour(
  a: RangeAnswer,
  sink: RangeSink,
  pos: number,
  h: crypto.Hash,
  max: number,
): Promise<{ n: number; over: boolean } | null> {
  const it = chunksOf(a.body)[Symbol.asyncIterator]();
  let n = 0;
  for (;;) {
    let next: IteratorResult<Uint8Array>;
    try {
      next = await it.next();
    } catch {
      a.discard?.();
      return null;
    }
    if (next.done) return { n, over: false };
    const c = next.value;
    if (n + c.byteLength > max) {
      a.discard?.();
      await it.return?.(undefined).catch(() => undefined);
      return { n, over: true };
    }
    h.update(c);
    try {
      await sink.write(pos + n, c);
    } catch (err) {
      a.discard?.();
      throw err;
    }
    n += c.byteLength;
  }
}

/**
 * Reads a whole file in ranged parts into `sink`, and says its size and its
 * sha256 - hashed as the parts land, never read back.
 *
 * ⚠ Each part must be the part asked for (its Content-Range says so), or it
 * is not taken - "2xx" is not "the file" (download-guard.ts: a 202 status
 * report written under the file's name lost files once). A failed part is
 * asked for again, from where it starts; what it left in the sink is written
 * over, and only a part that came whole is hashed. `If-Range` carries the
 * version the first part said: a file saved again in the middle answers 200
 * with the whole new version, which is taken whole (the sink starts over),
 * never spliced onto the old one; a 200 that breaks off, or that is not the
 * length it declared, is asked for again from the start.
 */
export async function downloadRangedTo(get: RangeTransport, sink: RangeSink, o: DownloadOptions = {}): Promise<Downloaded> {
  const sleep = o.sleep ?? defaultSleep;
  const chunk = Math.max(1, o.chunkSize ?? PART_BYTES);
  const maxFailures = Math.max(1, o.maxFailures ?? 6);
  const maxBytes = o.maxBytes ?? Number.MAX_SAFE_INTEGER;
  const tooBig = (n: number) =>
    new TransferError(`the file is ${n} bytes, more than this computer can hold in one piece (${maxBytes})`);

  let total = -1;
  let version: string | undefined;
  let at = 0;
  let failures = 0;
  let hash = crypto.createHash('sha256');
  while (total < 0 || at < total) {
    const end = total < 0 ? chunk - 1 : Math.min(at + chunk, total) - 1;
    let a: RangeAnswer | null = null;
    try {
      a = await get(`bytes=${at}-${end}`, version);
    } catch {
      a = null;
    }
    let broke = false;
    if (a && a.status === 200) {
      // The whole object: a server that ignores Range, or the file changed
      // since the first part (If-Range) - the new version, whole.
      if (a.contentLength !== undefined && a.contentLength > maxBytes) {
        a.discard?.();
        throw tooBig(a.contentLength);
      }
      await sink.restart();
      const h = crypto.createHash('sha256');
      const got = await pour(a, sink, 0, h, maxBytes);
      if (got?.over) throw tooBig(got.n + 1);
      if (got && (a.contentLength === undefined || got.n === a.contentLength)) {
        return { size: got.n, sha256: h.digest('hex') };
      }
      // Broken off, or short of what it declared: from the start again.
      total = -1;
      version = undefined;
      at = 0;
      hash = crypto.createHash('sha256');
      broke = true;
    } else if (a && a.status === 416 && total < 0 && /^bytes \*\/0$/.test(String(a.contentRange ?? '').trim())) {
      a.discard?.();
      await sink.restart();
      return { size: 0, sha256: crypto.createHash('sha256').digest('hex') }; // an empty file has no byte 0 to start at
    } else if (a && a.status === 206) {
      const cr = parseContentRange(a.contentRange);
      const want = total < 0 ? cr && cr.start === 0 : cr && cr.start === at && cr.total === total;
      if (cr && want && cr.end >= cr.start) {
        if (total < 0 && cr.total > maxBytes) {
          a.discard?.();
          throw tooBig(cr.total);
        }
        const len = cr.end - cr.start + 1;
        if (total < 0) await sink.restart();
        // ⚠ Hashed into a copy: a part that breaks off must not be in the hash.
        const h = hash.copy();
        const got = await pour(a, sink, cr.start, h, len);
        if (got && !got.over && got.n === len) {
          if (total < 0) {
            total = cr.total;
            version = a.lastModified || undefined;
          }
          hash = h;
          at = cr.end + 1;
          failures = 0;
          continue;
        }
        broke = true;
      } else {
        a.discard?.();
      }
    } else if (a && !transient(a.status) && a.status !== 202) {
      a.discard?.();
      throw new TransferError(`the server refused the download (server said ${a.status})`, a.status);
    } else {
      a?.discard?.();
    }
    // The connection, a part that is not the one asked for, a server still
    // preparing the file (202): the same part again, after a moment.
    if (++failures >= maxFailures) {
      throw new TransferError(a?.status === 202 && !broke ? 'the server is still preparing this file; try again in a moment' : `the download stopped at ${at} bytes`);
    }
    await sleep(250 * failures);
  }
  return { size: total, sha256: hash.digest('hex') };
}

/** A sink in memory: what downloadRanged returns. */
function memorySink(): RangeSink & { bytes(size: number): Buffer } {
  let buf = Buffer.alloc(0);
  return {
    restart: async () => {
      buf = Buffer.alloc(0);
    },
    write: async (at, data) => {
      const need = at + data.byteLength;
      if (need > buf.length) {
        const grown = Buffer.alloc(Math.max(need, buf.length * 2));
        buf.copy(grown);
        buf = grown;
      }
      buf.set(data, at);
    },
    bytes: (size) => Buffer.from(buf.subarray(0, size)),
  };
}

/**
 * downloadRangedTo, into memory: the whole file as one Buffer. For a read
 * whose size is known to be small; "Open with filex" brings a save home into
 * a spool (openwith-io.ts downloadToSpool).
 */
export async function downloadRanged(get: RangeTransport, o: DownloadOptions = {}): Promise<Buffer> {
  const sink = memorySink();
  const got = await downloadRangedTo(get, sink, o);
  return sink.bytes(got.size);
}
