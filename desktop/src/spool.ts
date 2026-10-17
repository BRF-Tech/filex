// What "Open with filex" holds on this computer's disk instead of in memory.
//
// A save brought home from the server is as large as the document - a PSD of
// several GB - and it used to be held in memory once per transfer. Now it is
// streamed into a SPOOL, a file of the app's own (openSpool: positional
// writes, fsync, then renamed from `.part` to its name, so a spool is never
// half-written), and every place it goes from there - over the document,
// beside it, a conflict copy, a recovered edit - is written from that file
// through one fixed buffer (writeContent), its sha256 checked on the way: a
// spool that is not what was received writes nothing.
//
// ⚠ PURE (node:fs only), so every rule here is measured without Electron
// (desktop/test/openwith-stream.test.ts).

import crypto from 'node:crypto';
import fs from 'node:fs';
import nodePath from 'node:path';

/** The one buffer every copy and every hash here reads through. */
export const COPY_BUFFER_BYTES = 1024 * 1024;

/** Content on this computer's disk: a spool file, its size and its sha256. */
export interface Spooled {
  readonly file: string;
  readonly size: number;
  readonly sha256: string;
}

/** A save's bytes: in memory (a small one, a test), or spooled. */
export type Content = Buffer | Uint8Array | Spooled;

export function isSpooled(c: Content): c is Spooled {
  return !(c instanceof Uint8Array);
}

export function contentSize(c: Content): number {
  return isSpooled(c) ? c.size : c.byteLength;
}

export function contentSha256(c: Content): string {
  return isSpooled(c) ? c.sha256 : crypto.createHash('sha256').update(c).digest('hex');
}

/** `data`, written whole at `at` (a positional write may take part of it). */
export async function writeAllAt(fh: fs.promises.FileHandle, data: Uint8Array, at: number): Promise<void> {
  let off = 0;
  while (off < data.byteLength) {
    const { bytesWritten } = await fh.write(data, off, data.byteLength - off, at + off);
    if (bytesWritten <= 0) throw new Error('the disk took no more bytes');
    off += bytesWritten;
  }
}

/** Fills `buf` from `at`; how many bytes there were (fewer at the end of the file). */
export async function readFullAt(fh: fs.promises.FileHandle, buf: Buffer, at: number): Promise<number> {
  let got = 0;
  while (got < buf.length) {
    const { bytesRead } = await fh.read(buf, got, buf.length - got, at + got);
    if (!bytesRead) break;
    got += bytesRead;
  }
  return got;
}

/**
 * Feeds `[from, to)` of an open file (to its end when `to` is left out) into
 * `h`, through one fixed buffer. How many bytes there were.
 */
export async function hashInto(
  fh: fs.promises.FileHandle,
  h: crypto.Hash,
  from = 0,
  to = Number.MAX_SAFE_INTEGER,
): Promise<number> {
  const buf = Buffer.allocUnsafe(COPY_BUFFER_BYTES);
  let at = from;
  while (at < to) {
    const want = Math.min(buf.length, to - at);
    const { bytesRead } = await fh.read(buf, 0, want, at);
    if (!bytesRead) break;
    h.update(buf.subarray(0, bytesRead));
    at += bytesRead;
  }
  return at - from;
}

// ── spools ───────────────────────────────────────────────────────────

/** `<tag>-<16 hex>`, and `.part` while it is written: the only names this
 *  module creates in a spool folder, and the only ones it removes. */
const SPOOL_NAME = /^([a-z0-9]{1,32})-[0-9a-f]{16}(\.part)?$/;

/** A spool being written: the RangeSink transfer.ts downloads into. */
export interface SpoolWriter {
  /** The `.part` file. */
  readonly file: string;
  /** Start over: what was written goes (a whole new version is coming). */
  restart(): Promise<void>;
  /** `data` at `at`. */
  write(at: number, data: Uint8Array): Promise<void>;
  /** Done: `size` bytes, on the disk (fsync), under the spool's own name. */
  finish(size: number, sha256: string): Promise<Spooled>;
  /** Not done: the `.part` file goes. */
  discard(): Promise<void>;
}

/**
 * A new spool in `dir`, named after `tag` (the session it is for), written
 * at positions: a part asked for again lands where it belongs.
 */
export async function openSpool(dir: string, tag: string): Promise<SpoolWriter> {
  if (!/^[a-z0-9]{1,32}$/.test(tag)) throw new Error('a spool tag is lower-case letters and digits: ' + tag);
  await fs.promises.mkdir(dir, { recursive: true });
  const name = tag + '-' + crypto.randomBytes(8).toString('hex');
  const final = nodePath.join(dir, name);
  const part = final + '.part';
  let fh: fs.promises.FileHandle | null = await fs.promises.open(part, 'wx');
  const close = async () => {
    const h = fh;
    fh = null;
    await h?.close().catch(() => undefined);
  };
  const open = () => {
    if (!fh) throw new Error('the spool is closed');
    return fh;
  };
  return {
    file: part,
    restart: async () => {
      await open().truncate(0);
    },
    write: async (at, data) => {
      await writeAllAt(open(), data, at);
    },
    finish: async (size, sha256) => {
      const h = open();
      try {
        // A longer attempt before a restart may have left bytes past the end.
        await h.truncate(size);
        await h.sync();
        const st = await h.stat();
        if (st.size !== size) throw new Error('the spool holds ' + st.size + ' bytes, not ' + size);
      } catch (err) {
        await close();
        await fs.promises.rm(part, { force: true }).catch(() => undefined);
        throw err;
      }
      await close();
      await fs.promises.rename(part, final);
      return { file: final, size, sha256 };
    },
    discard: async () => {
      await close();
      await fs.promises.rm(part, { force: true }).catch(() => undefined);
    },
  };
}

/** A spool that is no longer needed. Never throws. */
export async function disposeSpool(s: Spooled | null | undefined): Promise<void> {
  if (!s) return;
  await fs.promises.rm(s.file, { force: true }).catch(() => undefined);
}

/**
 * Removes spools in `dir` - only names this module makes, nothing else: every
 * one, or only `tag`'s, but never those whose tag `keep` keeps, and the
 * `.part` files still being written only when `parts` is not false. How many
 * went.
 */
export async function clearSpools(
  dir: string,
  o: { tag?: string; keep?: (tag: string) => boolean; parts?: boolean } = {},
): Promise<number> {
  let names: string[];
  try {
    names = await fs.promises.readdir(dir);
  } catch {
    return 0;
  }
  let gone = 0;
  for (const n of names) {
    const m = SPOOL_NAME.exec(n);
    if (!m) continue;
    if (o.tag !== undefined && m[1] !== o.tag) continue;
    if (o.keep?.(m[1]!)) continue;
    if (m[2] && o.parts === false) continue;
    try {
      await fs.promises.rm(nodePath.join(dir, n), { force: true });
      gone++;
    } catch {
      /* held open by something: the next start takes it */
    }
  }
  return gone;
}

// ── writing content somewhere ────────────────────────────────────────

/**
 * Writes `c` to `p` and fsyncs it. `exclusive`: `p` must not exist (it is
 * never replaced), and on any failure the file this call created goes again -
 * a half-written copy under a name the person reads as theirs is not left
 * behind.
 *
 * A spool is copied through COPY_BUFFER_BYTES, hashed as it goes: bytes that
 * are not the spool's recorded sha256 (a disk error, a spool something
 * truncated) fail the write. ⚠ Copied, not `fs.copyFile`: that keeps the
 * spool's modification time on Windows (CopyFileW) and macOS, and a document
 * written back must carry the time it was written - a backup tool comparing
 * times would pass it over otherwise.
 */
export async function writeContent(p: string, c: Content, o: { exclusive?: boolean } = {}): Promise<void> {
  let dst: fs.promises.FileHandle | null = null;
  let src: fs.promises.FileHandle | null = null;
  try {
    if (isSpooled(c)) src = await fs.promises.open(c.file, 'r');
    dst = await fs.promises.open(p, o.exclusive ? 'wx' : 'w');
  } catch (err) {
    await src?.close().catch(() => undefined);
    throw err;
  }
  try {
    if (!src) {
      await writeAllAt(dst, c as Uint8Array, 0);
    } else {
      const sp = c as Spooled;
      const h = crypto.createHash('sha256');
      const buf = Buffer.allocUnsafe(Math.max(1, Math.min(COPY_BUFFER_BYTES, sp.size)));
      let at = 0;
      for (;;) {
        const { bytesRead } = await src.read(buf, 0, buf.length, at);
        if (!bytesRead) break;
        const chunk = buf.subarray(0, bytesRead);
        h.update(chunk);
        await writeAllAt(dst, chunk, at);
        at += bytesRead;
      }
      if (at !== sp.size || h.digest('hex') !== sp.sha256) {
        throw new Error('the spooled copy is not what was received (' + at + ' of ' + sp.size + ' bytes, or not its sha256)');
      }
    }
    await dst.sync();
  } catch (err) {
    await dst.close().catch(() => undefined);
    dst = null;
    if (o.exclusive) await fs.promises.rm(p, { force: true }).catch(() => undefined);
    throw err;
  } finally {
    await dst?.close().catch(() => undefined);
    await src?.close().catch(() => undefined);
  }
}

/** The first `n` bytes of `c` (fewer when it is shorter). */
export async function contentHead(c: Content, n: number): Promise<Uint8Array> {
  if (!isSpooled(c)) return c.subarray(0, Math.min(n, c.byteLength));
  const fh = await fs.promises.open(c.file, 'r');
  try {
    const buf = Buffer.allocUnsafe(Math.max(0, Math.min(n, c.size)));
    const got = await readFullAt(fh, buf, 0);
    return buf.subarray(0, got);
  } finally {
    await fh.close().catch(() => undefined);
  }
}

/** Whether `needle` occurs anywhere in `c`, read through one fixed buffer. */
export async function contentIncludes(c: Content, needle: Buffer): Promise<boolean> {
  if (!isSpooled(c)) return Buffer.from(c.buffer, c.byteOffset, c.byteLength).includes(needle);
  if (!needle.length) return true;
  const fh = await fs.promises.open(c.file, 'r');
  try {
    // The tail of the previous read goes in front of the next one, so a
    // needle split across two reads is still found.
    const keep = needle.length - 1;
    const buf = Buffer.allocUnsafe(keep + COPY_BUFFER_BYTES);
    let carried = 0;
    let at = 0;
    for (;;) {
      const { bytesRead } = await fh.read(buf, carried, COPY_BUFFER_BYTES, at);
      if (!bytesRead) return false;
      at += bytesRead;
      const filled = carried + bytesRead;
      if (buf.subarray(0, filled).includes(needle)) return true;
      carried = Math.min(keep, filled);
      buf.copy(buf, 0, filled - carried, filled);
    }
  } finally {
    await fh.close().catch(() => undefined);
  }
}
