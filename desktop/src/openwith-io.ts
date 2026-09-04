// The server half of "Open with filex": the six calls the document round trip
// needs, and nothing else.
//
// Kept apart from openwith.ts so the logic that can lose an edit stays testable
// without Electron, and apart from main.ts so the HTTP shape of the round trip
// reads in one place.

import { net } from 'electron';

import { OFFICE_MIME_TYPES, SCRATCH_DIR_NAME, type RemoteStat } from './openwith.js';

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

/** The ceiling on a document this app is willing to shuttle. Office files are
 *  small; anything above this is not a document, it is a mistake, and the
 *  multipart upload path is the server's small-file route. */
export const MAX_DOCUMENT_BYTES = 256 * 1024 * 1024;

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
  opts: { method: string; url: string; body?: Buffer; contentType?: string },
): Promise<{ status: number; body: Buffer }> {
  return new Promise((resolve, reject) => {
    const req = net.request({ method: opts.method, url: opts.url });
    req.setHeader('Authorization', 'Bearer ' + ctx.token);
    if (opts.contentType) req.setHeader('Content-Type', opts.contentType);
    req.on('response', (res) => {
      const chunks: Buffer[] = [];
      res.on('data', (c: Buffer) => chunks.push(c));
      res.on('end', () => resolve({ status: res.statusCode, body: Buffer.concat(chunks) }));
      res.on('error', (e: Error) => reject(e));
    });
    req.on('error', (e) => reject(e));
    if (opts.body) req.write(opts.body);
    req.end();
  });
}

function managerUrl(ctx: RemoteContext, params: Record<string, string>): string {
  const url = new URL('/api/files/manager', ctx.serverUrl);
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

/** The multipart upload the browser's own toolbar uses — same endpoint, same
 *  field name, so a document that lands here is a document the explorer would
 *  have produced. */
export async function uploadFile(
  ctx: RemoteContext,
  destDir: string,
  basename: string,
  bytes: Buffer,
): Promise<void> {
  if (bytes.length > MAX_DOCUMENT_BYTES) {
    throw new Error('the document is larger than ' + Math.round(MAX_DOCUMENT_BYTES / (1024 * 1024)) + ' MB');
  }
  const boundary = '----filexopenwith' + Date.now().toString(36) + Math.random().toString(36).slice(2);
  const ext = basename.slice(basename.lastIndexOf('.') + 1).toLowerCase();
  const mime = OFFICE_MIME_TYPES[ext] ?? 'application/octet-stream';
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
  const res = await request(ctx, {
    method: 'POST',
    url: managerUrl(ctx, { action: 'upload' }),
    contentType: 'multipart/form-data; boundary=' + boundary,
    body: Buffer.concat([head, bytes, tail]),
  });
  if (res.status < 200 || res.status >= 300) throw new Error(explain(res.status, res.body));
}

/** The bytes of one remote file. */
export async function downloadFile(ctx: RemoteContext, wirePath: string): Promise<Buffer> {
  const res = await request(ctx, {
    method: 'GET',
    url: managerUrl(ctx, { action: 'download', path: wirePath }),
  });
  if (res.status < 200 || res.status >= 300) throw new Error(explain(res.status, res.body));
  return res.body;
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
