// The server half of "Open with filex": the six calls the document round trip
// needs, and the two reads that say what a server opens and in which app
// (readServerApps) - nothing else.
//
// Kept apart from openwith.ts so the logic that can lose an edit stays testable
// without Electron, and apart from main.ts so the HTTP shape of the round trip
// reads in one place.

import { net } from 'electron';

import { serverUrl } from './server-url.js';
import {
  OFFICE_MIME_TYPES,
  SCRATCH_DIR_NAME,
  openDocument,
  serverAppsFrom,
  type LocalVersion,
  type RemoteStat,
  type ServerApps,
} from './openwith.js';
import { openSpool, type Spooled } from './spool.js';
import {
  downloadRangedTo,
  sendDocument,
  type Answer,
  type RangeAnswer,
  type StagedTransport,
  type UploadSource,
} from './transfer.js';

export interface RemoteContext {
  serverUrl: string;
  token: string;
}

export interface RemoteEntry {
  basename: string;
  type: 'file' | 'dir';
  size?: number;
  lastModified?: number;
  etag?: string;
}

/** How long one request (one part of a transfer) may take before it is
 *  taken as a dropped connection and asked again. */
const REQUEST_TIMEOUT_MS = 5 * 60_000;

/**
 * One request, through Electron's `net.request` rather than `net.fetch`.
 *
 * ⚠⚠ Not a style preference — the same trap dragout.ts documents. `net.fetch`
 * builds a WHATWG `Headers` from the response and validates every value as a
 * ByteString, so a server that sends a raw non-ASCII byte in a header (a
 * `Content-Disposition` naming `Bütçe Özeti.xlsx` — precisely the files this
 * feature exists for) makes it THROW, from inside the response event where no
 * caller's try/catch can reach it. That lands as an uncaught exception in the
 * main process and the awaited download never settles. `net.request` hands
 * headers back as a plain object and never validates them.
 */
function request(
  ctx: RemoteContext,
  opts: { method: string; url: string; body?: Buffer; contentType?: string; headers?: Record<string, string> },
): Promise<{ status: number; body: Buffer; contentRange?: string; lastModified?: string }> {
  return new Promise((resolve, reject) => {
    const req = net.request({ method: opts.method, url: opts.url });
    req.setHeader('Authorization', 'Bearer ' + ctx.token);
    if (opts.contentType) req.setHeader('Content-Type', opts.contentType);
    for (const [k, v] of Object.entries(opts.headers ?? {})) req.setHeader(k, v);
    // A part that never answers is a dropped connection, not a wait for ever:
    // the transfer asks the server where it got to and goes on (transfer.ts).
    const timer = setTimeout(() => {
      req.abort();
      reject(new Error('no answer in ' + REQUEST_TIMEOUT_MS / 1000 + ' s'));
    }, REQUEST_TIMEOUT_MS);
    const header = (v: string | string[] | undefined) => (Array.isArray(v) ? v[0] : v);
    req.on('response', (res) => {
      const chunks: Buffer[] = [];
      res.on('data', (c: Buffer) => chunks.push(c));
      res.on('end', () => {
        clearTimeout(timer);
        resolve({
          status: res.statusCode,
          body: Buffer.concat(chunks),
          contentRange: header(res.headers['content-range']),
          lastModified: header(res.headers['last-modified']),
        });
      });
      res.on('error', (e: Error) => {
        clearTimeout(timer);
        reject(e);
      });
    });
    req.on('error', (e) => {
      clearTimeout(timer);
      reject(e);
    });
    if (opts.body) req.write(opts.body);
    req.end();
  });
}

/** How much of a streamed answer may wait for its reader before the
 *  connection is held back (network-level backpressure: Electron's
 *  IncomingMessage does not ask the network for more while it is paused). */
const STREAM_HIGH_WATER = 4 * 1024 * 1024;
const STREAM_LOW_WATER = 1024 * 1024;

/** A response whose body is read as it arrives - never collected whole. */
interface Streamed {
  status: number;
  header(name: string): string | undefined;
  body: AsyncIterable<Buffer>;
  /** The body will not be read (any further): the request is cut. */
  discard(): void;
}

/**
 * One request whose answer is STREAMED (the way back of a working copy: a
 * part of it, or a whole new version of several GB), through `net.request`
 * for the reason request() gives. The head must come within
 * REQUEST_TIMEOUT_MS, and then the body may not stall that long between two
 * chunks - a slow link moving a large file is not a dead one.
 */
function requestStream(
  ctx: RemoteContext,
  opts: { method: string; url: string; headers?: Record<string, string> },
): Promise<Streamed> {
  return new Promise((resolve, reject) => {
    const req = net.request({ method: opts.method, url: opts.url });
    req.setHeader('Authorization', 'Bearer ' + ctx.token);
    for (const [k, v] of Object.entries(opts.headers ?? {})) req.setHeader(k, v);
    let headTimer: ReturnType<typeof setTimeout> | null = setTimeout(() => {
      headTimer = null;
      req.abort();
      reject(new Error('no answer in ' + REQUEST_TIMEOUT_MS / 1000 + ' s'));
    }, REQUEST_TIMEOUT_MS);
    const header = (v: string | string[] | undefined) => (Array.isArray(v) ? v[0] : v);
    // Once the body streams, a failure of the request is the body's.
    let bodyFailed: ((e: Error) => void) | null = null;
    req.on('response', (res) => {
      if (headTimer) clearTimeout(headTimer);
      headTimer = null;
      const queue: Buffer[] = [];
      let queued = 0;
      let ended = false;
      let failure: Error | null = null;
      let wake: (() => void) | null = null;
      const poke = () => {
        const w = wake;
        wake = null;
        w?.();
      };
      let idle: ReturnType<typeof setTimeout> | null = null;
      const stop = () => {
        if (idle) clearTimeout(idle);
        idle = null;
      };
      const arm = () => {
        stop();
        idle = setTimeout(() => {
          failure ??= new Error('no data in ' + REQUEST_TIMEOUT_MS / 1000 + ' s');
          req.abort();
          poke();
        }, REQUEST_TIMEOUT_MS);
      };
      arm();
      // ⚠ Typed as an event emitter; at run time Electron's IncomingMessage
      // is a Readable, whose pause() holds the network back.
      const flow = res as unknown as { pause?: () => void; resume?: () => void };
      res.on('data', (c: Buffer) => {
        queue.push(c);
        queued += c.length;
        arm();
        if (queued >= STREAM_HIGH_WATER) flow.pause?.();
        poke();
      });
      res.on('end', () => {
        ended = true;
        stop();
        poke();
      });
      res.on('aborted', () => {
        failure ??= new Error('the connection was cut');
        stop();
        poke();
      });
      res.on('error', (e: Error) => {
        failure ??= e;
        stop();
        poke();
      });
      // Destroyed without an error, or without its end: not the whole body.
      (res as unknown as NodeJS.EventEmitter).on('close', () => {
        if (!ended) failure ??= new Error('the connection closed before the end');
        stop();
        poke();
      });
      bodyFailed = (e) => {
        failure ??= e;
        stop();
        poke();
      };
      const body: AsyncIterable<Buffer> = {
        async *[Symbol.asyncIterator]() {
          for (;;) {
            const c = queue.shift();
            if (c) {
              queued -= c.length;
              if (queued < STREAM_LOW_WATER) flow.resume?.();
              yield c;
              continue;
            }
            if (failure) throw failure;
            if (ended) return;
            await new Promise<void>((r) => (wake = r));
          }
        },
      };
      resolve({
        status: res.statusCode,
        header: (name) => header(res.headers[name.toLowerCase()]),
        body,
        discard: () => {
          stop();
          queue.length = 0;
          queued = 0;
          if (!ended) {
            failure ??= new Error('discarded');
            req.abort();
          }
          poke();
        },
      });
    });
    req.on('error', (e) => {
      if (headTimer) clearTimeout(headTimer);
      headTimer = null;
      if (bodyFailed) bodyFailed(e);
      else reject(e);
    });
    req.end();
  });
}

/** An answer as transfer.ts reads it: the status and the JSON body. */
function answerOf(res: { status: number; body: Buffer }): Answer {
  let json: unknown = null;
  try {
    json = JSON.parse(res.body.toString('utf8')) as unknown;
  } catch {
    /* not JSON: the status says it */
  }
  return { status: res.status, json };
}

/**
 * The staged upload's calls (docs/UPLOADS.md → The protocol) - what the
 * explorer's and the CLI's large uploads speak - over this app's requests.
 */
function stagedTransport(ctx: RemoteContext): StagedTransport {
  const at = (p: string) => serverUrl(ctx.serverUrl, '/api/files/upload' + p).toString();
  const json = (body: unknown) => Buffer.from(JSON.stringify(body), 'utf8');
  return {
    begin: async (body) => answerOf(await request(ctx, { method: 'POST', url: at('/begin'), contentType: 'application/json', body: json(body) })),
    put: async (id, start, end, total, body) =>
      answerOf(
        await request(ctx, {
          method: 'PUT',
          url: at('/' + encodeURIComponent(id)),
          contentType: 'application/octet-stream',
          headers: { 'Content-Range': `bytes ${start}-${end - 1}/${total}` },
          body: Buffer.from(body.buffer, body.byteOffset, body.byteLength),
        }),
      ),
    status: async (id) => answerOf(await request(ctx, { method: 'GET', url: at('/' + encodeURIComponent(id)) })),
    commit: async (id) => answerOf(await request(ctx, { method: 'POST', url: at('/' + encodeURIComponent(id) + '/commit') })),
    abort: async (id) => {
      await request(ctx, { method: 'DELETE', url: at('/' + encodeURIComponent(id)) });
    },
    op: async (opId) => answerOf(await request(ctx, { method: 'GET', url: serverUrl(ctx.serverUrl, '/api/files/ops/' + opId).toString() })),
  };
}

function managerUrl(ctx: RemoteContext, params: Record<string, string>): string {
  const url = serverUrl(ctx.serverUrl, '/api/files/manager');
  for (const [k, v] of Object.entries(params)) url.searchParams.set(k, v);
  return url.toString();
}

/** The failure text a server sends back, or a plain status line. */
function explain(status: number, body: Buffer): string {
  try {
    const j = JSON.parse(body.toString('utf8')) as { error?: string; message?: string };
    const m = j.error || j.message;
    if (m) return m + ' (' + status + ')';
  } catch {
    /* not JSON — the status is all there is */
  }
  return 'server said ' + status;
}

/** The storages this account can see, in the server's own order. */
export async function listStorages(ctx: RemoteContext): Promise<string[]> {
  const res = await request(ctx, { method: 'GET', url: managerUrl(ctx, { action: 'index' }) });
  if (res.status < 200 || res.status >= 300) throw new Error(explain(res.status, res.body));
  const body = JSON.parse(res.body.toString('utf8')) as { storages?: string[] };
  return body.storages ?? [];
}

/** One directory listing, normalised. A directory that does not exist lists as
 *  empty rather than throwing — the scratch folder is created lazily. */
export async function listDir(ctx: RemoteContext, wireDir: string): Promise<RemoteEntry[]> {
  const res = await request(ctx, {
    method: 'GET',
    url: managerUrl(ctx, { action: 'index', path: wireDir }),
  });
  if (res.status === 404) return [];
  if (res.status < 200 || res.status >= 300) throw new Error(explain(res.status, res.body));
  const body = JSON.parse(res.body.toString('utf8')) as {
    files?: Array<{ basename?: string; type?: string; size?: number; last_modified?: number; etag?: string }>;
  };
  return (body.files ?? [])
    .filter((f) => typeof f.basename === 'string' && f.basename)
    .map((f) => ({
      basename: String(f.basename),
      type: f.type === 'dir' ? 'dir' : 'file',
      size: typeof f.size === 'number' ? f.size : undefined,
      lastModified: typeof f.last_modified === 'number' ? f.last_modified : undefined,
      etag: typeof f.etag === 'string' && f.etag ? f.etag : undefined,
    }));
}

/** What the server currently holds for one file, or null when it is not there. */
export async function statRemote(
  ctx: RemoteContext,
  wireDir: string,
  basename: string,
): Promise<RemoteStat | null> {
  const entries = await listDir(ctx, wireDir);
  const hit = entries.find((e) => e.basename === basename && e.type === 'file');
  if (!hit) return null;
  return { size: hit.size, lastModified: hit.lastModified, etag: hit.etag };
}

/** Creates `<storage>://.filex-open`. Already existing is success, not failure
 *  — every open after the first hits that path. */
export async function ensureScratchDir(ctx: RemoteContext, storage: string): Promise<void> {
  const res = await request(ctx, {
    method: 'POST',
    url: managerUrl(ctx, { action: 'newfolder' }),
    contentType: 'application/json',
    body: Buffer.from(JSON.stringify({ path: storage + '://', name: SCRATCH_DIR_NAME }), 'utf8'),
  });
  if (res.status >= 200 && res.status < 300) return;
  // A conflict means it is already there, which is exactly what was wanted.
  if (res.status === 409) return;
  const text = res.body.toString('utf8').toLowerCase();
  if (text.includes('exist')) return;
  throw new Error(explain(res.status, res.body));
}

/**
 * The document at `doc.path`, as the version `doc.version` (what filex hashed
 * and declares to the server), to `<destDir>/<basename>` the way the
 * explorer sends a file (transfer.ts sendDocument): one request when it is
 * small, the STAGED upload in parts from PART_BYTES up (a dropped part costs
 * that part, the server says where to go on from), and a server with no
 * staged path takes a large one in one streamed request. The ceiling is the
 * server's own upload limit (`limitBytes`, capabilities `upload_limit_mb`; 0
 * is none).
 *
 * ⚠ Read a part at a time from the file and held to that version
 * (openwith.ts DocumentReader): saved again meanwhile, it is
 * SourceChangedError and nothing is committed. Memory is one part.
 */
export async function sendWorkingCopy(
  ctx: RemoteContext,
  destDir: string,
  basename: string,
  doc: { path: string; version: LocalVersion },
  opts: { limitBytes?: number } = {},
): Promise<void> {
  const src = await openDocument(doc.path, doc.version);
  try {
    await sendDocument(
      {
        staged: stagedTransport(ctx),
        whole: (body) => uploadFile(ctx, destDir, basename, Buffer.from(body.buffer, body.byteOffset, body.byteLength)),
        streamed: (s) => uploadFileStreamed(ctx, destDir, basename, s),
      },
      src,
      { dir: destDir, name: basename, mime: mimeOf(basename), sha256: doc.version.sha256, limitBytes: opts.limitBytes ?? 0 },
    );
  } finally {
    await src.close();
  }
}

function mimeOf(basename: string): string {
  const ext = basename.slice(basename.lastIndexOf('.') + 1).toLowerCase();
  return OFFICE_MIME_TYPES[ext] ?? 'application/octet-stream';
}

/** The multipart upload the browser's own toolbar uses for a small file —
 *  same endpoint, same field name, so a document that lands here is a
 *  document the explorer would have produced. */
async function uploadFile(
  ctx: RemoteContext,
  destDir: string,
  basename: string,
  bytes: Buffer,
): Promise<void> {
  const { contentType, head, tail } = multipartFrame(destDir, basename);
  const res = await request(ctx, {
    method: 'POST',
    url: managerUrl(ctx, { action: 'upload' }),
    contentType,
    body: Buffer.concat([head, bytes, tail]),
  });
  if (res.status < 200 || res.status >= 300) throw new Error(explain(res.status, res.body));
}

/** One write of a streamed upload body. */
const STREAM_WRITE_BYTES = 1024 * 1024;

/**
 * The same upload for a LARGE file, on a server with no staged path: the
 * body is written as the file is read, a part at a time (chunked transfer -
 * without it Electron collects the whole body in memory before sending).
 *
 * ⚠ The closing boundary goes last, after the source said it is still the
 * version it was (`verify`): changed under the upload, the request is cut
 * there, and a multipart body with no end is a body the server stores
 * nothing of (it parses the whole form before it writes a byte).
 */
async function uploadFileStreamed(ctx: RemoteContext, destDir: string, basename: string, src: UploadSource): Promise<void> {
  const { contentType, head, tail } = multipartFrame(destDir, basename);
  const req = net.request({ method: 'POST', url: managerUrl(ctx, { action: 'upload' }) });
  req.chunkedEncoding = true;
  req.setHeader('Authorization', 'Bearer ' + ctx.token);
  req.setHeader('Content-Type', contentType);
  let settled = false;
  const answer = new Promise<{ status: number; body: Buffer }>((resolve, reject) => {
    req.on('response', (res) => {
      const chunks: Buffer[] = [];
      res.on('data', (c: Buffer) => chunks.push(c));
      res.on('end', () => {
        settled = true;
        resolve({ status: res.statusCode, body: Buffer.concat(chunks) });
      });
      res.on('error', (e: Error) => {
        settled = true;
        reject(e);
      });
    });
    req.on('error', (e) => {
      settled = true;
      reject(e);
    });
  });
  // An answer before the body is done is a refusal: the writing stops.
  const early = answer.then((a) => {
    throw new Error(explain(a.status, a.body));
  });
  early.catch(() => undefined);
  let idle: ReturnType<typeof setTimeout> | null = null;
  const arm = () => {
    if (idle) clearTimeout(idle);
    idle = setTimeout(() => req.abort(), REQUEST_TIMEOUT_MS);
  };
  const write = (b: Buffer) =>
    Promise.race([
      new Promise<void>((resolve) => {
        req.write(b, undefined, () => resolve());
      }),
      early,
    ]);
  try {
    arm();
    await write(head);
    for (let at = 0; at < src.size; ) {
      const end = Math.min(at + STREAM_WRITE_BYTES, src.size);
      const part = await src.read(at, end);
      arm();
      await write(Buffer.from(part.buffer, part.byteOffset, part.byteLength));
      at = end;
    }
    await src.verify?.();
    await write(tail);
    req.end();
    const res = await answer;
    if (res.status < 200 || res.status >= 300) throw new Error(explain(res.status, res.body));
  } catch (err) {
    if (!settled) req.abort();
    throw err;
  } finally {
    if (idle) clearTimeout(idle);
  }
}

/** The multipart body around one file: what goes before its bytes, and after. */
function multipartFrame(destDir: string, basename: string): { contentType: string; head: Buffer; tail: Buffer } {
  const boundary = '----filexopenwith' + Date.now().toString(36) + Math.random().toString(36).slice(2);
  const mime = mimeOf(basename);
  // ⚠ The filename goes on the wire as raw UTF-8 inside a quoted string. Go's
  // mime/multipart reads it back as UTF-8; percent- or RFC 2231-encoding it
  // would arrive as the literal escape text and the copy would be called
  // `B%C3%BCt%C3%A7e.xlsx` on the server.
  const safe = basename.replace(/["\\\r\n]/g, '_');
  const head = Buffer.from(
    '--' + boundary + '\r\n' +
      'Content-Disposition: form-data; name="path"\r\n\r\n' +
      destDir + '\r\n' +
      '--' + boundary + '\r\n' +
      'Content-Disposition: form-data; name="file[]"; filename="' + safe + '"\r\n' +
      'Content-Type: ' + mime + '\r\n\r\n',
    'utf8',
  );
  const tail = Buffer.from('\r\n--' + boundary + '--\r\n', 'utf8');
  return { contentType: 'multipart/form-data; boundary=' + boundary, head, tail };
}

/**
 * One remote file, brought into a SPOOL of the app's own in `spool.dir`
 * (spool.ts openSpool: the file is on the disk and fsynced before it has its
 * name), in ranged parts (transfer.ts downloadRangedTo): each part checked to
 * be the part asked for - never a status report standing in for it
 * (download-guard.ts: a 202 body opened in Excel is the lucky case) - a
 * failed one asked for again from where it starts, a file saved again in the
 * middle taken whole (If-Range), and the sha256 taken as the bytes land.
 *
 * ⚠ Streamed to the disk as it arrives: memory is one network buffer,
 * whatever the size of the file. The caller owns the spool it gets back and
 * disposes of it (spool.ts disposeSpool).
 */
export async function downloadToSpool(
  ctx: RemoteContext,
  wirePath: string,
  spool: { dir: string; tag: string },
): Promise<Spooled> {
  const url = managerUrl(ctx, { action: 'download', path: wirePath });
  const get = async (range: string, ifRange?: string): Promise<RangeAnswer> => {
    const res = await requestStream(ctx, { method: 'GET', url, headers: { Range: range, ...(ifRange ? { 'If-Range': ifRange } : {}) } });
    // A length is a check only on the bytes as sent: a proxy that compressed
    // the answer declared the compressed length (the network stack hands the
    // body over decoded).
    const encoding = (res.header('content-encoding') ?? 'identity').trim().toLowerCase();
    const declared = encoding === 'identity' ? res.header('content-length') : undefined;
    const length = Number(declared);
    return {
      status: res.status,
      contentRange: res.header('content-range'),
      lastModified: res.header('last-modified'),
      contentLength: declared !== undefined && Number.isSafeInteger(length) && length >= 0 ? length : undefined,
      body: res.body,
      discard: res.discard,
    };
  };
  const w = await openSpool(spool.dir, spool.tag);
  try {
    const got = await downloadRangedTo(get, w);
    return await w.finish(got.size, got.sha256);
  } catch (err) {
    await w.discard();
    throw err;
  }
}

/**
 * Removes scratch copies.
 *
 * ⚠ This is the server's ordinary delete, which means the copies land in the
 * account's TRASH rather than vanishing — the same thing that happens to any
 * other file deleted through filex, and they age out under the same retention
 * policy. Hard-purging would need the admin trash endpoint, which a plain user
 * token cannot call; inventing a second, privileged delete path for a temp file
 * is not worth the surface.
 */
export async function deleteRemote(
  ctx: RemoteContext,
  parentDir: string,
  wirePaths: string[],
): Promise<void> {
  if (!wirePaths.length) return;
  const res = await request(ctx, {
    method: 'POST',
    url: managerUrl(ctx, { action: 'delete' }),
    contentType: 'application/json',
    body: Buffer.from(
      JSON.stringify({ path: parentDir, items: wirePaths.map((p) => ({ path: p })) }),
      'utf8',
    ),
  });
  if (res.status < 200 || res.status >= 300) throw new Error(explain(res.status, res.body));
}

/** A GET of one of the server's JSON answers: the status, and the body parsed (null when it is not JSON). */
async function getJson(ctx: RemoteContext, path: string): Promise<{ status: number; json: unknown }> {
  const res = await request(ctx, { method: 'GET', url: serverUrl(ctx.serverUrl, path).toString() });
  let json: unknown = null;
  try {
    json = JSON.parse(res.body.toString('utf8')) as unknown;
  } catch {
    /* not JSON - the status says what happened */
  }
  return { status: res.status, json };
}

/** What a server said about the apps that open files on it (readServerApps). */
export type ServerAppsAnswer =
  | { status: 'ok'; apps: ServerApps }
  /** The server no longer takes the account's token (401). */
  | { status: 'signed-out' }
  /** No answer worth acting on: the network, a 5xx, a page that is not filex. */
  | { status: 'unreachable'; reason: string };

/**
 * What a server opens (openwith.ts serverAppsFrom): asked of the answers the
 * editor page reads - the capabilities (is a Document Server there), the
 * apps' views (GET /api/files/plugins/actions; the views are the
 * `ui-viewer:` kinds an administrator granted, with Default apps' rules) and
 * the person's own "always open with" choices (GET /api/me/open-with, the
 * account's record the browser reads too). A server too old for the choices,
 * or one that cannot say them, has none: nothing else changes.
 *
 * ⚠ "Could not ask" is not "opens nothing" (sync's own rule, lesson #30): a
 * network failure or a 5xx is `unreachable`, and the caller keeps what it
 * knew. Only a definite answer changes it - apps switched off on the server
 * (403/404 for the views: no apps open anything there) included.
 */
export async function readServerApps(ctx: RemoteContext): Promise<ServerAppsAnswer> {
  let caps: { status: number; json: unknown };
  let views: { status: number; json: unknown };
  let choices: { status: number; json: unknown } | null;
  try {
    [caps, views, choices] = await Promise.all([
      getJson(ctx, '/api/files/capabilities'),
      getJson(ctx, '/api/files/plugins/actions'),
      getJson(ctx, '/api/me/open-with').catch(() => null),
    ]);
  } catch (err) {
    return { status: 'unreachable', reason: String((err as Error)?.message ?? err) };
  }
  if (caps.status === 401 || views.status === 401) return { status: 'signed-out' };
  if (caps.status < 200 || caps.status >= 300 || !caps.json || typeof caps.json !== 'object') {
    return { status: 'unreachable', reason: 'capabilities: server said ' + caps.status };
  }
  const appsOff = views.status === 403 || views.status === 404;
  if (!appsOff && (views.status < 200 || views.status >= 300 || !views.json || typeof views.json !== 'object')) {
    return { status: 'unreachable', reason: 'apps: server said ' + views.status };
  }
  try {
    const mine = choices && choices.status >= 200 && choices.status < 300 ? choices.json : null;
    return { status: 'ok', apps: serverAppsFrom(caps.json, appsOff ? null : views.json, mine) };
  } catch (err) {
    return { status: 'unreachable', reason: String((err as Error)?.message ?? err) };
  }
}
