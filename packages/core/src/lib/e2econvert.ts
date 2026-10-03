/**
 * e2econvert — encrypt a folder that already exists, in place
 * (docs/E2E-ENCRYPTION.md → "Encrypting a folder you already have").
 *
 * The key file is written first (e2ecrypto `startConversion`: v3, `conv`
 * required and pending), which makes the folder an encrypted folder to the
 * server from that moment. Then this walks it: every file whose bytes do not
 * start with the `filexe2e` magic is read, encrypted under the folder key and
 * written back over itself as a CONVERSION WRITE — one the server keeps no
 * version of, because what it replaces is the plaintext being removed.
 *
 * A file over the one-shot limit (200 MB) is converted too, as a STREAM
 * (header 0x02) file read, encrypted and written as streams (`writeLarge`);
 * it is left as it is only where the server cannot take it from a browser.
 *
 * Idempotent and resumable by the magic: a file already converted is left
 * alone, so running this again after a stop, a reload or a failure continues
 * it. A large file stopped half-way is not half-converted: its write is one
 * upload that replaces the file only when it commits, so until then the
 * server still has the whole plaintext and the next run starts it over. A write is conditional on the file being the one that was listed
 * (`expect`), so a file changed meanwhile is not overwritten with an older
 * version of itself — it counts as failed, and the next run picks it up.
 *
 * Names are not touched here: at level 2 the name pass (lib/e2enamepass)
 * runs after it. Pure over an `io` object, like the name pass.
 */

import { E2E_MARKER_NAME, hasMagic } from './e2ecrypto';
import { classifyStoredName } from './e2enames';

export interface ConvertRow {
  path: string;
  basename: string;
  type: 'file' | 'dir';
  /** Bytes, as listed. */
  size?: number;
  /** Milliseconds, as listed — with the size, the write's precondition. */
  last_modified?: number;
}

export interface ConvertIo {
  /** The raw listing of a folder. */
  list(dirWire: string): Promise<ConvertRow[]>;
  /** The first bytes of a file (at least 9 when it has them). */
  head(wire: string): Promise<Uint8Array>;
  /** The whole file. */
  read(wire: string): Promise<ArrayBuffer>;
  /**
   * Write `data` over the file `row` in `dirWire` as a conversion write, on
   * condition that it is still the file listed (`expect`: "<size>:<ms>").
   * Throws when the server refuses (a changed file: 412).
   */
  write(dirWire: string, row: ConvertRow, data: Blob, expect: string | null): Promise<void>;
  /** Encrypt one file's bytes under the folder key. */
  encrypt(data: ArrayBuffer): Promise<ArrayBuffer>;
  /**
   * A file over `maxBytes` (the one-shot limit): read, encrypted as a STREAM
   * (header 0x02) file and written over itself as a conversion write on the
   * same condition as `write` - all as streams, so memory stays at a few
   * chunks whatever the size. Resolves false when this server cannot take it
   * from this browser (no staged upload, and too big to gather for a single
   * POST); throws when it failed (changed meanwhile: 412, no permission, …).
   * Absent: such files are left as they are and counted in `tooBig`.
   */
  writeLarge?(dir: string, row: ConvertRow, expect: string | null): Promise<boolean>;
  /** Asked between files. */
  stopped(): boolean;
}

export interface ConvertProgress {
  /** Files found (everything but the key file and name sidecars). */
  total: number;
  /** Converted by this run. */
  done: number;
  /** Already encrypted — converted earlier, or uploaded encrypted. */
  skipped: number;
  /**
   * Over the one-shot limit and NOT converted: this server cannot take a file
   * sent in pieces, and it is too big to gather for one POST (or the caller
   * has no streamed write at all). Left as they are.
   */
  tooBig: number;
  /** Of `done`, files over the one-shot limit, written as STREAM (0x02). */
  large?: number;
  /** Could not be read or written (changed meanwhile, no permission, …). */
  failed: number;
}

export interface ConvertOptions {
  /** The one-shot limit: files larger than this go through `io.writeLarge`
   *  (STREAM, 0x02), or are counted in `tooBig` when there is none. */
  maxBytes: number;
}

/** "<size>:<ms>" — the listing's own signature of the file, or null. */
export function expectOf(row: ConvertRow): string | null {
  if (typeof row.size !== 'number' || typeof row.last_modified !== 'number') return null;
  return `${row.size}:${row.last_modified}`;
}

export async function runConversion(
  rootWire: string,
  io: ConvertIo,
  prog: ConvertProgress,
  opts: ConvertOptions,
): Promise<void> {
  // 1. The files, all of them first: the progress has a total.
  const files: Array<{ dir: string; row: ConvertRow }> = [];
  const queue = [rootWire];
  const isRoot = (dir: string) => dir.replace(/\/+$/, '') === rootWire.replace(/\/+$/, '');
  while (queue.length) {
    if (io.stopped()) return;
    const dir = queue.shift()!;
    let rows: ConvertRow[];
    try {
      rows = await io.list(dir);
    } catch {
      prog.failed++;
      continue;
    }
    for (const r of rows) {
      if (r.type === 'dir') {
        queue.push(r.path);
        continue;
      }
      if (isRoot(dir) && r.basename === E2E_MARKER_NAME) continue;
      if (classifyStoredName(r.basename).kind === 'sidecar') continue;
      files.push({ dir, row: r });
    }
  }
  prog.total = files.length;

  // 2. One file at a time: never more than one in memory (and a large one
  //    never whole).
  for (const { dir, row } of files) {
    if (io.stopped()) return;
    try {
      if (hasMagic(await io.head(row.path))) {
        prog.skipped++;
        continue;
      }
      if (typeof row.size === 'number' && row.size > opts.maxBytes) {
        // wiring:e2 convert - over the one-shot limit: STREAM (0x02), the
        // format an upload of the same file gets (lib/e2estream).
        if (io.writeLarge && (await io.writeLarge(dir, row, expectOf(row)))) {
          prog.done++;
          prog.large = (prog.large ?? 0) + 1;
        } else {
          prog.tooBig++;
        }
        continue;
      }
      const plain = await io.read(row.path);
      if (plain.byteLength > opts.maxBytes) {
        prog.tooBig++;
        continue;
      }
      const ct = await io.encrypt(plain);
      await io.write(dir, row, new Blob([ct], { type: 'application/octet-stream' }), expectOf(row));
      prog.done++;
    } catch {
      prog.failed++;
    }
  }
}
