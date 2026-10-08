// Regenerate the vault (encryption level 3) test vectors with an INDEPENDENT
// implementation.
//
// docs/E2E-VAULT-FORMAT.md is the normative text; this file is its reference
// implementation. It uses Node's `node:crypto` (OpenSSL), which is neither the
// WebCrypto code the browser runs (packages/core) nor the Go code `filex
// decrypt` and `filex vault mount` run (backend/internal/e2edecrypt). Both of
// those are tested against what this file writes, byte for byte, so a mistake
// the two of them share still fails.
//
//     node backend/internal/e2edecrypt/testdata/gen_vault_vectors.mjs
//
// Node 18 or later, no packages. The output is deterministic: every random
// byte comes from the vector DRBG (docs/E2E-VAULT-FORMAT.md -> "Test
// vectors"), so running it twice writes the same files. It writes:
//
//   testdata/vault/v3-vault/        a vault exactly as a storage holds it
//                                   (pack size 2^16), after three generations
//   testdata/vault-vectors.json     secrets, keys, every generation's layout,
//                                   the 4 MiB variant's hashes, the repack
//                                   branch (generations 4 to 6, hashes and
//                                   layouts), layer vectors and the
//                                   index-body cases (good and bad)
//
// Do not edit the outputs by hand. Change the format, then this file, then
// run it.

import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const FIXTURE = path.join(HERE, 'vault', 'v3-vault');
const OUT_JSON = path.join(HERE, 'vault-vectors.json');

// ── Format constants (docs/E2E-VAULT-FORMAT.md) ─────────────────────────
const MAGIC = Buffer.from('filexvlt', 'ascii');
const FORMAT_VERSION = 1;
const KIND_PACK = 0x50; // 'P'
const KIND_INDEX = 0x49; // 'I'
const PACK_HEADER = 32;
const INDEX_HEADER = 40;
const TAG = 16;
const INDEX_MIN_SIZE = 65536;
const BODY_VERSION = 1;
const INFO_INDEX = Buffer.from('filex-vault-index-v1', 'ascii');
const INFO_CONTENT = Buffer.from('filex-vault-content-v1', 'ascii');
const CHUNK_LOG2 = 20;
const E_FOLDER = 1;
const E_FILE = 2;
const C_NONE = 0;
const C_STREAM = 1;
const MAX_UVARINT = 2 ** 53 - 1;

// ── Vector inputs ─────────────────────────────────────────────────────────
const PASSWORD = 'vault vector password, not a real one';
const ITER = 1000; // readers accept 1 ... 100 000 000; writers use 600 000
const SMALL_PACK_LOG2 = 16; // the fixture: 64 KiB packs keep the repository small
const BIG_PACK_LOG2 = 22; // the product default, hashes only

const T = 1791277200000; // 2026-10-06T09:00:00Z, in ms
const NOTE_2 = 'Kasadaki ilk not: çay demlendi.\n';
const NOTE_3 = 'Kasadaki ikinci not: şeker yok.\n';
const BIG = { len: 1048577, seed: 7 }; // 1 MiB + 1 byte

const nfc = (s) => s.normalize('NFC');

const GENERATIONS = [
  { gen: 1, ops: [] },
  {
    gen: 2,
    ops: [
      { op: 'mkdir', path: nfc('Belgeler'), mtime: T + 1000 },
      { op: 'mkdir', path: nfc('Belgeler/Arşiv'), mtime: T + 2000 },
      { op: 'write', path: nfc('not.txt'), mtime: T + 3000, content: { text: NOTE_2 } },
      { op: 'write', path: nfc('Belgeler/boş.txt'), mtime: T + 4000, content: { empty: true } },
      { op: 'write', path: nfc('Belgeler/Arşiv/büyük.bin'), mtime: T + 5000, content: { pattern: BIG } },
    ],
  },
  {
    gen: 3,
    ops: [
      { op: 'delete', path: nfc('Belgeler/Arşiv/büyük.bin') },
      { op: 'move', from: nfc('Belgeler/boş.txt'), to: nfc('boş.txt') },
      { op: 'write', path: nfc('not.txt'), mtime: T + 6000, content: { text: NOTE_3 } },
    ],
  },
];

// A branch of that history that ends in a REPACK (docs/E2E-VAULT-FORMAT.md →
// "Garbage collection", "Repacking"), from generation 3 on, as hashes and
// layouts only (no files in the fixture). Generation 4 writes three files of
// 40 000 bytes over two packs; generation 5 deletes the first and the third,
// which leaves every pack of the table less than half live; generation 6 is
// the repack the writer runs after that commit: the live extents of all three
// packs - generation 3's `not.txt` among them - copied into one new pack.
const REPACK_GENERATIONS = [
  {
    gen: 4,
    ops: [
      { op: 'write', path: nfc('a.bin'), mtime: T + 7000, content: { pattern: { len: 40000, seed: 11 } } },
      { op: 'write', path: nfc('b.bin'), mtime: T + 8000, content: { pattern: { len: 40000, seed: 12 } } },
      { op: 'write', path: nfc('c.bin'), mtime: T + 9000, content: { pattern: { len: 40000, seed: 13 } } },
    ],
  },
  {
    gen: 5,
    ops: [
      { op: 'delete', path: nfc('a.bin') },
      { op: 'delete', path: nfc('c.bin') },
    ],
  },
  { gen: 6, ops: [], repack: true },
];
const REPACK_SEED = 'filex vault vectors v1 repack generation';

// ── Primitives ────────────────────────────────────────────────────────────

/** The vector DRBG: AES-256-CTR keystream, key = SHA-256(seed), counter 0. */
class Drbg {
  constructor(seed) {
    this.seed = seed;
    const key = crypto.createHash('sha256').update(Buffer.from(seed, 'utf8')).digest();
    this.ctr = crypto.createCipheriv('aes-256-ctr', key, Buffer.alloc(16));
    this.used = 0;
  }
  take(n) {
    this.used += n;
    if (n === 0) return Buffer.alloc(0);
    const out = this.ctr.update(Buffer.alloc(n));
    if (out.length !== n) throw new Error('drbg: short keystream');
    return out;
  }
}

function sha256hex(b) {
  return crypto.createHash('sha256').update(b).digest('hex');
}

function uvarint(n) {
  if (!Number.isSafeInteger(n) || n < 0 || n > MAX_UVARINT) throw new Error(`uvarint out of range: ${n}`);
  const out = [];
  let v = BigInt(n);
  for (;;) {
    const b = Number(v & 0x7fn);
    v >>= 7n;
    if (v === 0n) {
      out.push(b);
      break;
    }
    out.push(b | 0x80);
  }
  return Buffer.from(out);
}

function bitLen(n) {
  let b = 0;
  let v = BigInt(n);
  while (v > 0n) {
    b++;
    v >>= 1n;
  }
  return b;
}

/** Padmé (Nikitin et al., PETS 2019): leaks O(log log L) bits of a length. */
function padme(L) {
  const E = bitLen(L) - 1;
  const S = bitLen(E);
  const step = 2 ** (E - S);
  return Math.ceil(L / step) * step;
}

function indexFileSize(bodyLen) {
  return Math.max(INDEX_MIN_SIZE, padme(INDEX_HEADER + bodyLen + TAG));
}

function gcmSeal(key, iv, plain, aad) {
  const c = crypto.createCipheriv('aes-256-gcm', key, iv);
  if (aad) c.setAAD(aad);
  return Buffer.concat([c.update(plain), c.final(), c.getAuthTag()]);
}

function gcmOpen(key, iv, sealed, aad) {
  const d = crypto.createDecipheriv('aes-256-gcm', key, iv);
  if (aad) d.setAAD(aad);
  d.setAuthTag(sealed.subarray(sealed.length - TAG));
  return Buffer.concat([d.update(sealed.subarray(0, sealed.length - TAG)), d.final()]);
}

function streamNonce(i, last) {
  const n = Buffer.alloc(12); // prefix: 7 zero bytes
  n.writeUInt32BE(i, 7);
  n[11] = last ? 1 : 0;
  return n;
}

function streamSize(size, log2) {
  return size + TAG * Math.max(1, Math.ceil(size / 2 ** log2));
}

function streamEncrypt(key, plain, log2) {
  const size = 2 ** log2;
  const chunks = [];
  for (let off = 0; off < plain.length; off += size) chunks.push(plain.subarray(off, off + size));
  if (chunks.length === 0) throw new Error('a vault never stores an empty STREAM');
  const out = chunks.map((c, i) => gcmSeal(key, streamNonce(i, i === chunks.length - 1), c, null));
  return Buffer.concat(out);
}

function streamDecrypt(key, body, log2) {
  const csize = 2 ** log2 + TAG;
  const out = [];
  let i = 0;
  for (let off = 0; off < body.length; off += csize, i++) {
    const c = body.subarray(off, Math.min(off + csize, body.length));
    out.push(gcmOpen(key, streamNonce(i, off + csize >= body.length), c, null));
  }
  return Buffer.concat(out);
}

function hkdf(fmk, vaultId, label, id) {
  return Buffer.from(crypto.hkdfSync('sha256', fmk, vaultId, Buffer.concat([label, id]), 32));
}

function pattern(len, seed) {
  const b = Buffer.alloc(len);
  for (let i = 0; i < len; i++) b[i] = (i * 31 + seed) & 0xff;
  return b;
}

function b64(b) {
  return b.toString('base64');
}

function b64url(b) {
  return b.toString('base64').replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function crockford(raw) {
  const alphabet = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
  let bits = '';
  for (const x of raw) bits += x.toString(2).padStart(8, '0');
  let s = '';
  for (let i = 0; i < bits.length; i += 5) s += alphabet[parseInt(bits.slice(i, i + 5).padEnd(5, '0'), 2)];
  return s.match(/.{1,4}/g).join('-');
}

function hex16(n) {
  return n.toString(16).padStart(16, '0');
}

function contentBytes(c) {
  if (c.empty) return Buffer.alloc(0);
  if (c.text !== undefined) return Buffer.from(c.text, 'utf8');
  return pattern(c.pattern.len, c.pattern.seed);
}

// ── The key file ─────────────────────────────────────────────────────────
function makeMarker() {
  const d = new Drbg('filex vault vectors v1 marker');
  const salt = d.take(16);
  const fmk = d.take(32);
  const rkRaw = d.take(20);
  const rkSalt = d.take(16);
  const vaultId = d.take(16);
  const verifyIv = d.take(12);
  const fmkPwIv = d.take(12);
  const rkIv = d.take(12);
  const kek = crypto.pbkdf2Sync(Buffer.from(PASSWORD, 'utf8'), salt, ITER, 32, 'sha256');
  const rkek = Buffer.from(crypto.hkdfSync('sha256', rkRaw, rkSalt, Buffer.from('filex-e2e-recovery-v1', 'ascii'), 32));
  const markerFor = (packLog2) => ({
    v: 3,
    req: ['vault'],
    salt: b64(salt),
    iter: ITER,
    verify: b64(Buffer.concat([verifyIv, gcmSeal(kek, verifyIv, Buffer.from('filex-e2e-verify-v1', 'ascii'), null)])),
    fmk: 'wrapped',
    fmk_pw: b64(Buffer.concat([fmkPwIv, gcmSeal(kek, fmkPwIv, fmk, null)])),
    rk: { salt: b64(rkSalt), blob: b64(Buffer.concat([rkIv, gcmSeal(rkek, rkIv, fmk, null)])) },
    vault: { v: 1, id: b64url(vaultId), pack: packLog2 },
  });
  return { fmk, vaultId, recoveryKey: crockford(rkRaw), markerFor };
}

// ── The index body ───────────────────────────────────────────────────────

/** Entries in canonical order: pre-order, siblings by the bytes of their name. */
function ordered(tree) {
  const kids = new Map();
  for (const e of tree.values()) {
    if (!kids.has(e.parent)) kids.set(e.parent, []);
    kids.get(e.parent).push(e);
  }
  for (const list of kids.values()) list.sort((a, b) => Buffer.compare(Buffer.from(a.name, 'utf8'), Buffer.from(b.name, 'utf8')));
  const out = [];
  const walk = (p) => {
    for (const e of kids.get(p) || []) {
      out.push(e);
      if (e.kind === E_FOLDER) walk(e.path);
    }
  };
  walk('');
  return out;
}

function packTableOf(tree) {
  const ids = new Map();
  for (const e of tree.values()) for (const x of e.content?.extents || []) ids.set(x.pack.toString('hex'), x.pack);
  return [...ids.values()].sort(Buffer.compare);
}

/**
 * Encode a body from explicit parts. `entries[i].parent` is the 1-based
 * position of its parent (0 = the vault root), `content.extents[j].pack` an
 * index into `packs`. Nothing is checked here: the bad cases are made with it.
 */
function encodeRaw({ version = BODY_VERSION, flags = 0, packs, entries, grave, ext = Buffer.alloc(0) }) {
  const parts = [Buffer.from([version]), uvarint(flags), uvarint(packs.length), ...packs, uvarint(entries.length)];
  for (const e of entries) {
    const name = Buffer.from(e.name, 'utf8');
    parts.push(Buffer.from([e.kind]), uvarint(e.parent), uvarint(name.length), name, uvarint(e.mtime));
    if (e.kind === E_FILE) {
      parts.push(uvarint(e.size));
      if (e.content) {
        parts.push(Buffer.from([C_STREAM]), e.content.id, Buffer.from([e.content.log2]), uvarint(e.content.extents.length));
        for (const x of e.content.extents) parts.push(uvarint(x.pack), uvarint(x.offset), uvarint(x.length));
      } else {
        parts.push(Buffer.from([C_NONE]));
      }
    }
    const ex = e.ext || Buffer.alloc(0);
    parts.push(uvarint(ex.length), ex);
  }
  parts.push(uvarint(grave.length));
  for (const g of grave) parts.push(g.id, uvarint(g.died));
  parts.push(uvarint(ext.length), ext);
  return Buffer.concat(parts);
}

function encodeBody(tree, grave) {
  const entries = ordered(tree);
  const packs = packTableOf(tree);
  const packPos = new Map(packs.map((p, i) => [p.toString('hex'), i]));
  const pos = new Map(entries.map((e, i) => [e.path, i + 1]));
  const raw = entries.map((e) => ({
    kind: e.kind,
    parent: e.parent === '' ? 0 : pos.get(e.parent),
    name: e.name,
    mtime: e.mtime,
    size: e.size,
    content: e.content
      ? {
          id: e.content.id,
          log2: e.content.log2,
          extents: e.content.extents.map((x) => ({ pack: packPos.get(x.pack.toString('hex')), offset: x.offset, length: x.length })),
        }
      : null,
  }));
  const g = [...grave].sort((a, b) => Buffer.compare(a.id, b.id));
  return { body: encodeRaw({ packs, entries: raw, grave: g }), entries, packs, grave: g };
}

// ── Writing one generation ───────────────────────────────────────────────

function splitPath(p) {
  const i = p.lastIndexOf('/');
  return i < 0 ? { parent: '', name: p } : { parent: p.slice(0, i), name: p.slice(i + 1) };
}

function cloneTree(tree) {
  const t = new Map();
  for (const [k, e] of tree) t.set(k, { ...e });
  return t;
}

/** The live bytes of each pack of `tree`'s table, by hex id. */
function liveBytes(tree) {
  const live = new Map(packTableOf(tree).map((p) => [p.toString('hex'), 0]));
  for (const e of tree.values()) for (const x of e.content?.extents || []) live.set(x.pack.toString('hex'), live.get(x.pack.toString('hex')) + x.length);
  return live;
}

/**
 * The packs to repack after a commit, or null (docs/E2E-VAULT-FORMAT.md →
 * "Repacking"): S = the packs of the table with fewer live bytes than half a
 * data area; repack when S has at least 2 packs, their live bytes fit in
 * fewer packs than S has, and more than half of all the data areas in the
 * table is dead. In table order.
 */
function repackSet(tree, packLog2) {
  const area = 2 ** packLog2 - PACK_HEADER;
  const table = packTableOf(tree);
  const live = liveBytes(tree);
  const S = table.filter((p) => live.get(p.toString('hex')) * 2 < area);
  const liveS = S.reduce((n, p) => n + live.get(p.toString('hex')), 0);
  const liveAll = table.reduce((n, p) => n + live.get(p.toString('hex')), 0);
  const total = area * table.length;
  if (S.length < 2 || Math.ceil(liveS / area) >= S.length || (total - liveAll) * 2 <= total) return null;
  return S;
}

function writeGeneration({ gen, ops, prev, fmk, vaultId, packLog2, seed, repack = null }) {
  const d = new Drbg(seed);
  const cap = 2 ** packLog2 - PACK_HEADER;
  const tree = cloneTree(prev.tree);
  const packs = [];
  let open = null;

  const appendBody = (body) => {
    const extents = [];
    let pos = 0;
    while (pos < body.length) {
      if (!open) {
        open = { id: d.take(16), data: Buffer.alloc(cap), used: 0 };
        packs.push(open);
      }
      const n = Math.min(body.length - pos, cap - open.used);
      body.copy(open.data, open.used, pos, pos + n);
      extents.push({ pack: open.id, offset: PACK_HEADER + open.used, length: n });
      open.used += n;
      pos += n;
      if (open.used === cap) {
        open.filled = open.used;
        open = null;
      }
    }
    return extents;
  };

  for (const op of ops) {
    if (op.op === 'mkdir') {
      const { parent, name } = splitPath(op.path);
      tree.set(op.path, { path: op.path, kind: E_FOLDER, parent, name, mtime: op.mtime });
    } else if (op.op === 'write') {
      const { parent, name } = splitPath(op.path);
      const data = contentBytes(op.content);
      let content = null;
      if (data.length > 0) {
        const id = d.take(16);
        const key = hkdf(fmk, vaultId, INFO_CONTENT, id);
        const body = streamEncrypt(key, data, CHUNK_LOG2);
        if (body.length !== streamSize(data.length, CHUNK_LOG2)) throw new Error('stream size');
        content = { id, log2: CHUNK_LOG2, extents: appendBody(body), key };
      }
      tree.set(op.path, { path: op.path, kind: E_FILE, parent, name, mtime: op.mtime, size: data.length, content, plain: data });
    } else if (op.op === 'delete') {
      if (!tree.delete(op.path)) throw new Error(`delete: no ${op.path}`);
    } else if (op.op === 'move') {
      const e = tree.get(op.from);
      if (!e || e.kind !== E_FILE) throw new Error(`move: no file ${op.from}`);
      tree.delete(op.from);
      const { parent, name } = splitPath(op.to);
      tree.set(op.to, { ...e, path: op.to, parent, name });
    } else {
      throw new Error(`unknown op ${op.op}`);
    }
  }
  // Repacking: the live extents in `repack` - entry by entry in index order,
  // each entry's extents in order, the bytes as they are, nothing encrypted
  // again - appended by the canonical layout. Every piece stays an extent of
  // its own, also when two land side by side in one new pack (no merging).
  if (repack) {
    const set = new Set(repack.map((p) => p.toString('hex')));
    const bytesOf = new Map(prev.packFiles.map((p) => [p.id.toString('hex'), p.bytes]));
    for (const e of ordered(tree)) {
      if (!e.content || !e.content.extents.some((x) => set.has(x.pack.toString('hex')))) continue;
      const extents = [];
      for (const x of e.content.extents) {
        if (!set.has(x.pack.toString('hex'))) {
          extents.push({ ...x });
          continue;
        }
        extents.push(...appendBody(bytesOf.get(x.pack.toString('hex')).subarray(x.offset, x.offset + x.length)));
      }
      tree.set(e.path, { ...e, content: { ...e.content, extents } });
    }
  }
  if (open) {
    open.filled = open.used;
    if (open.used < cap) open.data.set(d.take(cap - open.used), open.used);
    open = null;
  }

  const nowTable = new Set(packTableOf(tree).map((p) => p.toString('hex')));
  const grave = [...prev.grave];
  for (const id of prev.packs) if (!nowTable.has(id.toString('hex'))) grave.push({ id, died: gen });

  const { body, entries, packs: table, grave: g } = encodeBody(tree, grave);
  const size = indexFileSize(body.length);
  const plain = Buffer.concat([body, Buffer.alloc(size - INDEX_HEADER - TAG - body.length)]);
  const sealId = d.take(16);
  const header = Buffer.alloc(INDEX_HEADER);
  MAGIC.copy(header, 0);
  header[8] = FORMAT_VERSION;
  header[9] = KIND_INDEX;
  header.writeBigUInt64BE(BigInt(gen), 16);
  sealId.copy(header, 24);
  const indexKey = hkdf(fmk, vaultId, INFO_INDEX, sealId);
  const index = Buffer.concat([header, gcmSeal(indexKey, Buffer.alloc(12), plain, header)]);
  if (index.length !== size) throw new Error('index size');

  const packFiles = packs.map((p) => {
    const h = Buffer.alloc(PACK_HEADER);
    MAGIC.copy(h, 0);
    h[8] = FORMAT_VERSION;
    h[9] = KIND_PACK;
    h[10] = packLog2;
    p.id.copy(h, 16);
    return { id: p.id, used: p.filled, bytes: Buffer.concat([h, p.data]) };
  });

  // Self-check: every file decrypts back from what was just written.
  const byId = new Map([...prev.packFiles, ...packFiles].map((p) => [p.id.toString('hex'), p.bytes]));
  for (const e of tree.values()) {
    if (e.kind !== E_FILE || !e.content) continue;
    const cbody = Buffer.concat(e.content.extents.map((x) => byId.get(x.pack.toString('hex')).subarray(x.offset, x.offset + x.length)));
    const back = streamDecrypt(hkdf(fmk, vaultId, INFO_CONTENT, e.content.id), cbody, e.content.log2);
    if (!back.equals(e.plain)) throw new Error(`self-check: ${e.path}`);
  }
  const reopened = gcmOpen(indexKey, Buffer.alloc(12), index.subarray(INDEX_HEADER), index.subarray(0, INDEX_HEADER));
  if (!reopened.subarray(0, body.length).equals(body)) throw new Error('self-check: index');

  return {
    gen,
    tree,
    packs: table,
    grave: g,
    entries,
    body,
    sealId,
    indexKey,
    index,
    packFiles,
    allPackFiles: [...prev.packFiles, ...packFiles],
    drbgUsed: d.used,
  };
}

function runVault({ fmk, vaultId, packLog2 }) {
  let prev = { tree: new Map(), packs: [], grave: [], packFiles: [] };
  const out = [];
  for (const g of GENERATIONS) {
    const r = writeGeneration({ gen: g.gen, ops: g.ops, prev, fmk, vaultId, packLog2, seed: `filex vault vectors v1 generation ${g.gen}` });
    out.push(r);
    prev = { tree: r.tree, packs: r.packs, grave: r.grave, packFiles: r.allPackFiles };
  }
  return out;
}

/** The repack branch, from generation `from` of a run (see REPACK_GENERATIONS). */
function runRepack({ fmk, vaultId, packLog2, from }) {
  let prev = { tree: from.tree, packs: from.packs, grave: from.grave, packFiles: from.allPackFiles };
  const out = [];
  for (const g of REPACK_GENERATIONS) {
    let repack = null;
    if (g.repack) {
      repack = repackSet(prev.tree, packLog2);
      if (!repack) throw new Error(`repack generation ${g.gen}: nothing to repack`);
    }
    const r = writeGeneration({ gen: g.gen, ops: g.ops, prev, fmk, vaultId, packLog2, seed: `${REPACK_SEED} ${g.gen}`, repack });
    // Only the last generation repacks: the ones before it must not call for it.
    if (!g.repack && g.gen !== REPACK_GENERATIONS[REPACK_GENERATIONS.length - 2].gen && repackSet(r.tree, packLog2)) {
      throw new Error(`repack generation ${g.gen}: calls for a repack too early`);
    }
    r.repack = repack;
    out.push(r);
    prev = { tree: r.tree, packs: r.packs, grave: r.grave, packFiles: r.allPackFiles };
  }
  if (repackSet(prev.tree, packLog2)) throw new Error('repack: the repacked generation calls for another');
  return out;
}

const packPath = (id) => `v/p/${id.toString('hex').slice(0, 2)}/${id.toString('hex')}.fxp`;
const indexPath = (gen) => `v/idx/${hex16(gen)}.fxi`;

// ── Body cases: one good-with-ext, many bad ──────────────────────────────

function bodyCases() {
  const P = (n) => Buffer.alloc(16, n);
  const cid = Buffer.alloc(16, 0xcc);
  const file = (parent, name, size, extents) => ({
    kind: E_FILE,
    parent,
    name,
    mtime: T,
    size,
    content: size ? { id: cid, log2: CHUNK_LOG2, extents } : null,
  });
  const folder = (parent, name) => ({ kind: E_FOLDER, parent, name, mtime: T });
  const ok5 = [{ pack: 0, offset: 32, length: 21 }];
  const base = { packs: [P(1)], grave: [] };
  const cases = [];
  const add = (name, valid, why, body, extra = {}) =>
    cases.push({ name, valid, why, pack_log2: SMALL_PACK_LOG2, generation: 5, plaintext_hex: body.toString('hex'), ...extra });

  add('minimal', true, 'one folder and one 5-byte file', encodeRaw({ ...base, entries: [folder(0, 'a'), file(1, 'b.txt', 5, ok5)] }));
  add(
    'ext_is_skipped',
    true,
    'a reader skips extension bytes; a writer that meets them stays read-only',
    encodeRaw({ ...base, entries: [folder(0, 'a'), { ...file(1, 'b.txt', 5, ok5), ext: Buffer.from([1, 2, 3]) }] }),
    { writable: false },
  );
  add('zero_tail', true, 'zero bytes after the body are padding', Buffer.concat([encodeRaw({ ...base, entries: [file(0, 'b.txt', 5, ok5)] }), Buffer.alloc(9)]));
  add('nonzero_tail', false, 'the padding after the body must be zero', Buffer.concat([encodeRaw({ ...base, entries: [file(0, 'b.txt', 5, ok5)] }), Buffer.from([0, 7])]));
  add('body_version_2', false, 'a body version this reader does not know', encodeRaw({ ...base, version: 2, entries: [file(0, 'b.txt', 5, ok5)] }));
  add('flags_unknown', false, 'an unknown flag bit is a required feature', encodeRaw({ ...base, flags: 1, entries: [file(0, 'b.txt', 5, ok5)] }));
  add('sibling_order', false, 'siblings out of byte order', encodeRaw({ packs: [P(1)], grave: [], entries: [file(0, 'b.txt', 5, ok5), { ...file(0, 'a.txt', 5, [{ pack: 0, offset: 53, length: 21 }]) }] }));
  add('sibling_order_bytes_not_locale', true, '"Zebra" before "alfa": bytes, not a locale', encodeRaw({ packs: [], grave: [], entries: [folder(0, 'Zebra'), folder(0, 'alfa')] }));
  add('parent_forward', false, 'a parent must come before its child', encodeRaw({ ...base, entries: [file(2, 'b.txt', 5, ok5), folder(0, 'a')] }));
  add('parent_is_file', false, 'a parent must be a folder', encodeRaw({ packs: [P(1)], grave: [], entries: [file(0, 'a.txt', 5, ok5), file(1, 'b.txt', 0, null)] }));
  add('not_preorder', false, 'a folder subtree must follow its folder directly', encodeRaw({ packs: [], grave: [], entries: [folder(0, 'a'), folder(0, 'b'), folder(1, 'c')] }));
  add('duplicate_name', false, 'two siblings with the same name', encodeRaw({ packs: [], grave: [], entries: [folder(0, 'a'), folder(0, 'a')] }));
  add('name_slash', false, 'a name with "/"', encodeRaw({ packs: [], grave: [], entries: [folder(0, 'a/b')] }));
  add('name_dotdot', false, 'the name ".."', encodeRaw({ packs: [], grave: [], entries: [folder(0, '..')] }));
  add('name_empty', false, 'an empty name', encodeRaw({ packs: [], grave: [], entries: [folder(0, '')] }));
  add('extent_sum', false, 'extents add up to 20 bytes, a 5-byte file needs 21', encodeRaw({ ...base, entries: [file(0, 'b.txt', 5, [{ pack: 0, offset: 32, length: 20 }])] }));
  add('extent_in_header', false, 'an extent may not start inside the pack header', encodeRaw({ ...base, entries: [file(0, 'b.txt', 5, [{ pack: 0, offset: 31, length: 21 }])] }));
  add('extent_past_pack', false, 'an extent may not run past the end of a 2^16-byte pack', encodeRaw({ ...base, entries: [file(0, 'b.txt', 5, [{ pack: 0, offset: 65530, length: 21 }])] }));
  add('extent_pack_index', false, 'an extent names pack 1 of a one-pack table', encodeRaw({ ...base, entries: [file(0, 'b.txt', 5, [{ pack: 1, offset: 32, length: 21 }])] }));
  add('pack_unreferenced', false, 'the pack table lists only packs an extent uses', encodeRaw({ packs: [P(1), P(2)], grave: [], entries: [file(0, 'b.txt', 5, ok5)] }));
  add('pack_table_order', false, 'the pack table is in ascending byte order', encodeRaw({ packs: [P(2), P(1)], grave: [], entries: [file(0, 'a.txt', 5, [{ pack: 0, offset: 32, length: 21 }]), file(0, 'b.txt', 5, [{ pack: 1, offset: 32, length: 21 }])] }));
  add('empty_with_content', false, 'a 0-byte file has no content', encodeRaw({ ...base, entries: [{ ...file(0, 'b.txt', 0, null), content: { id: cid, log2: CHUNK_LOG2, extents: [{ pack: 0, offset: 32, length: 16 }] } }] }));
  add('sized_without_content', false, 'a file with bytes has content', encodeRaw({ packs: [], grave: [], entries: [{ ...file(0, 'b.txt', 0, null), size: 5 }] }));
  add('grave_in_table', false, 'a graveyard pack may not also be live', encodeRaw({ packs: [P(1)], grave: [{ id: P(1), died: 3 }], entries: [file(0, 'b.txt', 5, ok5)] }));
  add('grave_future', false, 'a pack cannot die after this generation (5)', encodeRaw({ packs: [], grave: [{ id: P(9), died: 6 }], entries: [] }));
  add('grave_ok', true, 'a graveyard entry, sorted, disjoint, died 2 ... 5', encodeRaw({ packs: [], grave: [{ id: P(8), died: 2 }, { id: P(9), died: 5 }], entries: [] }));
  add('varint_nonminimal', false, 'entry count 0 written as 0x80 0x00', Buffer.concat([Buffer.from([1, 0, 0, 0x80, 0x00]), uvarint(0), uvarint(0)]));
  add('truncated', false, 'the body ends inside an entry', encodeRaw({ ...base, entries: [file(0, 'b.txt', 5, ok5)] }).subarray(0, 30));
  add('chunk_log2_small', false, 'a chunk size below 2^10', encodeRaw({ ...base, entries: [{ ...file(0, 'b.txt', 5, ok5), content: { id: cid, log2: 9, extents: ok5 } }] }));
  return cases;
}

// ── Run ──────────────────────────────────────────────────────────────────

const mk = makeMarker();
const small = runVault({ fmk: mk.fmk, vaultId: mk.vaultId, packLog2: SMALL_PACK_LOG2 });
const big = runVault({ fmk: mk.fmk, vaultId: mk.vaultId, packLog2: BIG_PACK_LOG2 });
const repacked = runRepack({ fmk: mk.fmk, vaultId: mk.vaultId, packLog2: SMALL_PACK_LOG2, from: small[small.length - 1] });

// The fixture folder, rewritten from scratch (only this one directory).
if (!FIXTURE.replace(/\\/g, '/').endsWith('/testdata/vault/v3-vault')) throw new Error(`refusing to clean ${FIXTURE}`);
fs.rmSync(FIXTURE, { recursive: true, force: true });
const put = (rel, bytes) => {
  const p = path.join(FIXTURE, ...rel.split('/'));
  fs.mkdirSync(path.dirname(p), { recursive: true });
  fs.writeFileSync(p, bytes);
};
put('.filex-e2e.json', Buffer.from(JSON.stringify(mk.markerFor(SMALL_PACK_LOG2), null, 2) + '\n', 'utf8'));
for (const r of small) {
  put(indexPath(r.gen), r.index);
  for (const p of r.packFiles) put(packPath(p.id), p.bytes);
}

const treeJson = (r) =>
  r.entries.map((e) => {
    const row = { path: e.path, kind: e.kind === E_FOLDER ? 'folder' : 'file', mtime: e.mtime };
    if (e.kind === E_FILE) {
      row.size = e.size;
      row.sha256 = sha256hex(e.plain);
      if (e.plain.length <= 256) row.text = e.plain.toString('utf8');
      if (e.content) {
        row.content_id = e.content.id.toString('hex');
        row.content_key = e.content.key.toString('hex');
        row.chunk_log2 = e.content.log2;
        row.extents = e.content.extents.map((x) => ({ pack: x.pack.toString('hex'), offset: x.offset, length: x.length }));
      }
    }
    return row;
  });

const genJson = (r, full, list = GENERATIONS, seed = 'filex vault vectors v1 generation') => {
  const o = {
    generation: r.gen,
    ops: list.find((g) => g.gen === r.gen).ops,
    drbg_seed: `${seed} ${r.gen}`,
    drbg_bytes_used: r.drbgUsed,
    index_path: indexPath(r.gen),
    index_size: r.index.length,
    index_sha256: sha256hex(r.index),
    packs_written: r.packFiles.map((p) => ({ id: p.id.toString('hex'), path: packPath(p.id), used: p.used, sha256: sha256hex(p.bytes) })),
  };
  if (full) {
    o.seal_id = r.sealId.toString('hex');
    o.index_key = r.indexKey.toString('hex');
    o.body_len = r.body.length;
    o.body_hex = r.body.toString('hex');
    o.pack_table = r.packs.map((p) => p.toString('hex'));
    o.graveyard = r.grave.map((g) => ({ pack: g.id.toString('hex'), died: g.died }));
    o.tree = treeJson(r);
  }
  return o;
};

const drbgProbe = new Drbg('filex vault vectors v1 generation 2').take(64).toString('hex');
const names = ['alfa', 'Zebra', 'zebra', 'Äpfel', 'Apfel', 'İzmir', 'ılık', 'Ilık', 'a b', 'a', 'a.txt', 'a-b', '日本', 'çay', 'Çay', 'cay'].map(nfc);
const sortedNames = [...names].sort((a, b) => Buffer.compare(Buffer.from(a, 'utf8'), Buffer.from(b, 'utf8')));

const out = {
  comment:
    'Vault (encryption level 3) vectors, generated by gen_vault_vectors.mjs with node:crypto. Do not edit by hand. Format: docs/E2E-VAULT-FORMAT.md. Passwords and keys here open nothing but these files.',
  format: 'docs/E2E-VAULT-FORMAT.md',
  fixture_dir: 'vault/v3-vault',
  secrets: {
    password: PASSWORD,
    recovery_key: mk.recoveryKey,
    iter: ITER,
    fmk: mk.fmk.toString('hex'),
    vault_id: mk.vaultId.toString('hex'),
    vault_id_b64url: b64url(mk.vaultId),
  },
  marker: mk.markerFor(SMALL_PACK_LOG2),
  pack_log2: SMALL_PACK_LOG2,
  latest_generation: small[small.length - 1].gen,
  generations: small.map((r) => genJson(r, true)),
  canonical_4mib: {
    comment: 'The same operations, the same DRBG seeds, packs of 2^22 bytes. Only hashes: an implementation writes them and compares.',
    marker: mk.markerFor(BIG_PACK_LOG2),
    pack_log2: BIG_PACK_LOG2,
    generations: big.map((r) => genJson(r, false)),
  },
  repack: {
    comment:
      'A branch of the fixture from its generation 3 on, packs of 2^16: generation 4 writes three files, 5 deletes two of them, and 6 is the repack a writer runs after that commit. Hashes and layouts only; an implementation replays generations 1 to 3, then these, and compares. `repack` is the set S (Repacking), in table order.',
    pack_log2: SMALL_PACK_LOG2,
    from_generation: small[small.length - 1].gen,
    generations: repacked.map((r) => ({
      ...genJson(r, true, REPACK_GENERATIONS, REPACK_SEED),
      ...(r.repack ? { repack: r.repack.map((p) => p.toString('hex')) } : {}),
    })),
  },
  layers: {
    drbg: { seed: 'filex vault vectors v1 generation 2', first_64_bytes: drbgProbe },
    uvarint: [0, 1, 127, 128, 255, 300, 16383, 16384, 2097151, 2097152, 4294967295, MAX_UVARINT].map((v) => ({ value: v, hex: uvarint(v).toString('hex') })),
    padme_index_size: [6, 1000, 65480, 65481, 70000, 131072, 1048577, 33554433].map((bodyLen) => ({
      body_len: bodyLen,
      min_len: INDEX_HEADER + bodyLen + TAG,
      file_size: indexFileSize(bodyLen),
    })),
    stream_size: [
      [1, 20],
      [1048576, 20],
      [1048577, 20],
      [3000, 10],
    ].map(([size, log2]) => ({ size, chunk_log2: log2, ciphertext: streamSize(size, log2) })),
    name_order: { input: names, sorted: sortedNames },
  },
  body_cases: bodyCases(),
  negative: [
    { name: 'wrong_password', do: 'unlock with "vault vector password, not the real one"', expect: 'wrong password; nothing opens' },
    { name: 'index_renamed', do: 'copy v/idx/0000000000000003.fxi to v/idx/0000000000000004.fxi', expect: 'generation 4 is damaged (its header says 3); readers show generation 3 read-only and say the latest is damaged; writers refuse to write' },
    { name: 'index_bitflip', do: 'flip bit 0 of byte 1000 of v/idx/0000000000000003.fxi', expect: 'generation 3 is damaged; readers show generation 2 read-only and say so; writers refuse to write' },
    { name: 'index_wrong_size', do: 'append one zero byte to v/idx/0000000000000003.fxi', expect: 'generation 3 is damaged (the size is not its Padme size)' },
    { name: 'pack_bitflip', do: 'in generation 2, flip bit 0 of the byte at offset 1000 of the third extent of Belgeler/Arşiv/büyük.bin', expect: 'that file fails as damaged; not.txt of generation 2 still reads' },
    { name: 'marker_req_extra', do: 'set the marker req to ["vault", "names"]', expect: 'the key file is refused as malformed' },
    { name: 'marker_vault_v2', do: 'set the marker vault.v to 2', expect: 'refused: the vault needs a newer filex' },
    { name: 'marker_kek', do: 'set the marker fmk to "kek" and drop fmk_pw', expect: 'the key file is refused as malformed (a vault always has a wrapped FMK)' },
  ],
};

fs.writeFileSync(OUT_JSON, JSON.stringify(out, null, 2) + '\n', 'utf8');
console.log(
  `vault vectors: ${small.length} generations, ${small.reduce((n, r) => n + r.packFiles.length, 0)} packs of 2^${SMALL_PACK_LOG2}, latest index ${small[small.length - 1].index.length} bytes, repack branch ${repacked.map((r) => r.gen).join('/')} (${repacked[repacked.length - 1].repack.length} packs into ${repacked[repacked.length - 1].packFiles.length}) -> ${path.relative(process.cwd(), OUT_JSON)}`,
);
