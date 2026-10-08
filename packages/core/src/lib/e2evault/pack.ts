/**
 * e2evault/pack — packs: the header, the canonical way bytes go into them,
 * and mapping a span of a file's body onto its extents
 * (docs/E2E-VAULT-FORMAT.md → "Packs", "File contents").
 *
 * A pack is exactly 2^`vault.pack` bytes: a 32-byte plaintext header, then
 * extents of encrypted file contents back to back, then random padding (never
 * zeros: zeros would show the server how full every pack is). A pack is
 * uploaded whole, once, and never changed.
 */
import {
  VAULT_FORMAT,
  VAULT_KIND_PACK,
  VAULT_MAGIC,
  VAULT_PACK_HEADER_LEN,
  VaultFormatError,
  fromHex,
  packDataSize,
  toHex,
} from './layout';
import type { VaultExtent } from './vindex';

/** The 32-byte header of pack `id` (hex) of size 2^`packLog2`. */
export function buildPackHeader(id: string, packLog2: number): Uint8Array<ArrayBuffer> {
  const raw = fromHex(id);
  if (!raw || raw.length !== 16) throw new VaultFormatError('bad_id');
  const h = new Uint8Array(VAULT_PACK_HEADER_LEN);
  h.set(VAULT_MAGIC, 0);
  h[8] = VAULT_FORMAT;
  h[9] = VAULT_KIND_PACK;
  h[10] = packLog2;
  // [11..16) zero
  h.set(raw, 16);
  return h;
}

/**
 * Check a whole pack's header (magic, version 1, kind P, the log2, the five
 * zeros, the id). A reader that fetches byte ranges never sees it; one that
 * holds the whole pack checks it. Throws VaultFormatError: damage.
 */
export function checkPackHeader(b: Uint8Array, id: string, packLog2: number): void {
  if (b.length < VAULT_PACK_HEADER_LEN) throw new VaultFormatError('pack_short');
  for (let i = 0; i < 8; i++) if (b[i] !== VAULT_MAGIC[i]) throw new VaultFormatError('pack_magic');
  if (b[8] !== VAULT_FORMAT) throw new VaultFormatError('pack_version');
  if (b[9] !== VAULT_KIND_PACK) throw new VaultFormatError('pack_kind');
  if (b[10] !== packLog2) throw new VaultFormatError('pack_log2');
  for (let i = 11; i < 16; i++) if (b[i] !== 0) throw new VaultFormatError('pack_zeros');
  if (toHex(b.subarray(16, 32)) !== id) throw new VaultFormatError('pack_id');
  if (b.length !== 2 ** packLog2) throw new VaultFormatError('pack_size');
}

/** A pack being filled in memory. `data` is the data area (no header). */
export interface OpenPack {
  /** Its id (hex). May change once, when an upload of unknown outcome is
   *  sent again under a new id. */
  id: string;
  data: Uint8Array<ArrayBuffer>;
  used: number;
}

export function openPack(id: string, packLog2: number): OpenPack {
  return { id, data: new Uint8Array(packDataSize(packLog2)), used: 0 };
}

/** The whole pack: header and data area. */
export function packBytes(p: OpenPack, packLog2: number): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(2 ** packLog2);
  out.set(buildPackHeader(p.id, packLog2), 0);
  out.set(p.data, VAULT_PACK_HEADER_LEN);
  return out;
}

/** One run of bytes inside one pack: what one HTTP range request fetches. */
export interface PackRun {
  pack: string;
  /** Offset within the pack (header included). */
  offset: number;
  length: number;
}

/**
 * Map bytes `[start, end)` of a body onto its extents: the runs, in order,
 * whose bytes joined are that span. Adjacent runs in one pack are merged.
 */
export function mapBodySpan(extents: VaultExtent[], start: number, end: number): PackRun[] {
  const runs: PackRun[] = [];
  let pos = 0;
  for (const x of extents) {
    const xs = pos;
    const xe = pos + x.length;
    pos = xe;
    if (xe <= start) continue;
    if (xs >= end) break;
    const a = Math.max(start, xs);
    const b = Math.min(end, xe);
    const run = { pack: x.pack, offset: x.offset + (a - xs), length: b - a };
    const last = runs[runs.length - 1];
    if (last && last.pack === run.pack && last.offset + last.length === run.offset) last.length += run.length;
    else runs.push(run);
  }
  return runs;
}

/** Merge extents that follow each other in one pack (writers make them maximal). */
export function mergeExtents(extents: VaultExtent[]): VaultExtent[] {
  const out: VaultExtent[] = [];
  for (const x of extents) {
    const last = out[out.length - 1];
    if (last && last.pack === x.pack && last.offset + last.length === x.offset) last.length += x.length;
    else out.push({ ...x });
  }
  return out;
}

/**
 * The STREAM chunks a plaintext span `[a, b)` needs, and the body span that
 * holds their ciphertext (docs/E2E-VAULT-FORMAT.md → "Reading bytes [a, b)").
 */
export function chunkSpan(
  size: number,
  log2: number,
  a: number,
  b: number,
): { first: number; last: number; start: number; end: number; count: number } {
  const chunk = 2 ** log2;
  const full = chunk + 16;
  const count = Math.max(1, Math.ceil(size / chunk));
  const bodyLen = size + 16 * count;
  const first = Math.floor(a / chunk);
  const last = Math.floor((b - 1) / chunk);
  return { first, last, start: first * full, end: Math.min((last + 1) * full, bodyLen), count };
}
