/**
 * e2enamepass — give every entry of an encrypted-names folder the name it
 * should have (docs/E2E-ENCRYPTION.md → "Encryption levels").
 *
 * One pass does three jobs, because they are one job seen from the server:
 *
 *   - raising a folder from level 1 (contents) to level 2 (contents + names):
 *     every entry still has its plaintext name;
 *   - finishing that after an interruption: some entries do, some do not;
 *   - repairing what arrived from outside the browser: a name written over
 *     WebDAV (plaintext), or an entry MOVED over WebDAV into another folder
 *     of the same encrypted folder — its name is sealed for the folder it
 *     came from (lib/e2enames → folder ids), so here it does not open. The
 *     pass knows every folder id, tries them, and re-seals the name it finds
 *     for the folder the entry is in now.
 *
 * Renames only: no content is read or written. Idempotent: an entry whose
 * name opens where it is is left alone, and a plaintext-named folder's id is
 * derived (the same every time), so running the pass again after a stop
 * continues it. Folders go top-down to learn ids and bottom-up to rename, so
 * a folder's contents are sealed under its id before the folder itself is
 * renamed, and a queued folder rename never races its own contents.
 *
 * Pure over an `io` object, so it runs against a fake tree in the tests and
 * against the file API in the explorer.
 */

import { E2E_MARKER_NAME } from './e2ecrypto';
import {
  classifyStoredName,
  decryptEncoded,
  decryptStoredName,
  effectiveDirId,
  encryptName,
  namePlainProblem,
  type E2eNameKey,
} from './e2enames';

export interface NamePassRow {
  path: string;
  basename: string;
  type: 'file' | 'dir';
}

export interface NamePassIo {
  /** The raw listing of a folder (stored names, not decorated). */
  list(dirWire: string): Promise<NamePassRow[]>;
  /** A sibling sidecar's content, or null when there is none. */
  readSidecar(dirWire: string, sidecarName: string): Promise<string | null>;
  writeSidecar(dirWire: string, sidecarName: string, content: string): Promise<void>;
  /** Rename `row` (in `dirWire`) to the stored name `to`. */
  rename(dirWire: string, row: NamePassRow, to: string): Promise<void>;
  /** Asked between entries: stop as soon as this says so. */
  stopped(): boolean;
}

export interface NamePassProgress {
  /** Entries renamed so far. */
  renamed: number;
  /** Entries that could not be renamed (no permission, a name a disk cannot
   *  hold, a name that cannot be read at all). */
  failed: number;
  /** Entries found needing a name. */
  seen: number;
  /** Of `renamed`, entries that had been moved without being re-sealed. */
  repaired?: number;
}

/** "name (2).ext" — a free plaintext name when the one we need is taken. */
export function numberedName(name: string, n: number): string {
  const dot = name.lastIndexOf('.');
  if (dot <= 0) return `${name} (${n})`;
  return `${name.slice(0, dot)} (${n})${name.slice(dot)}`;
}

function sameId(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

/**
 * The plaintext of a name sealed for another folder of this encrypted folder
 * (an entry moved over WebDAV), or null. Tries every folder id the pass
 * knows; a name only ever opens under the one it was sealed for.
 */
async function recoverMoved(
  nk: E2eNameKey,
  encoded: string | null,
  here: Uint8Array,
  ids: Uint8Array[],
): Promise<string | null> {
  if (!encoded) return null;
  for (const id of ids) {
    if (sameId(id, here)) continue;
    const plain = await decryptEncoded(nk, encoded, id);
    if (plain !== null) return namePlainProblem(plain) ? null : plain;
  }
  return null;
}

/** The encoded ciphertext a stored name carries, through its sidecar for a long one. */
async function encodedOf(stored: string, dirWire: string, io: NamePassIo): Promise<string | null> {
  const c = classifyStoredName(stored);
  if (c.kind === 'file' || c.kind === 'dir') return c.encoded ?? null;
  if (c.kind !== 'long' && c.kind !== 'longdir') return null;
  const content = (await io.readSidecar(dirWire, `${c.hash}.fxl.name`))?.trim() ?? '';
  return content || null;
}

export async function runNamePass(
  nk: E2eNameKey,
  rootWire: string,
  io: NamePassIo,
  prog: NamePassProgress,
): Promise<void> {
  // 1. Every folder once, top-down, with the id its contents are sealed under.
  const dirs: Array<{ wire: string; id: Uint8Array; rows: NamePassRow[] }> = [];
  const queue: Array<{ wire: string; id: Uint8Array }> = [{ wire: rootWire, id: nk.rootId }];
  while (queue.length) {
    if (io.stopped()) return;
    const d = queue.shift()!;
    let rows: NamePassRow[];
    try {
      rows = await io.list(d.wire);
    } catch {
      prog.failed++;
      continue;
    }
    dirs.push({ ...d, rows });
    for (const r of rows) {
      if (r.type !== 'dir') continue;
      queue.push({ wire: r.path, id: await effectiveDirId(nk, d.id, r.basename) });
    }
  }
  const ids = dirs.map((d) => d.id);

  // 2. Deepest folders first (a breadth-first list, reversed).
  for (const d of dirs.reverse()) {
    const taken = new Set(d.rows.map((r) => r.basename));
    for (const row of d.rows) {
      if (io.stopped()) return;
      const stored = row.basename;
      if (stored === E2E_MARKER_NAME) continue;
      const got = await decryptStoredName(nk, stored, d.id, (n) => io.readSidecar(d.wire, n));
      if (got.state === 'enc' || got.state === 'sidecar') continue;
      prog.seen++;
      const moved = await recoverMoved(nk, await encodedOf(stored, d.wire, io), d.id, ids);
      const plain = moved ?? (got.state === 'plain' ? stored : null);
      if (plain === null || namePlainProblem(plain)) {
        prog.failed++;
        continue;
      }
      // A folder keeps the id its contents are already sealed under.
      const opts = row.type === 'dir' ? { dirId: await effectiveDirId(nk, d.id, stored) } : {};
      let enc = await encryptName(nk, plain, d.id, opts);
      // Taken: something was written under this very name after the change
      // began. Keep both, the way an upload conflict would be kept.
      for (let i = 2; taken.has(enc.stored) && i < 100; i++) {
        enc = await encryptName(nk, numberedName(plain, i), d.id, opts);
      }
      if (taken.has(enc.stored)) {
        prog.failed++;
        continue;
      }
      try {
        if (enc.sidecar) await io.writeSidecar(d.wire, enc.sidecar.name, enc.sidecar.content);
        await io.rename(d.wire, row, enc.stored);
        taken.delete(stored);
        taken.add(enc.stored);
        prog.renamed++;
        if (moved !== null) prog.repaired = (prog.repaired ?? 0) + 1;
      } catch {
        prog.failed++;
      }
    }
  }
}
