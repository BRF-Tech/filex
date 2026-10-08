// The vault (encryption level 3) against its test vectors, byte for byte
// (docs/E2E-VAULT-FORMAT.md → "Test vectors", items 1 to 9).
//
// The vectors come from an INDEPENDENT implementation - node:crypto, i.e.
// OpenSSL (backend/internal/e2edecrypt/testdata/gen_vault_vectors.mjs) - not
// from this code: the browser must read what it wrote and write what it
// wrote, so a mistake this code shares with the Go one still fails. The Go
// implementation (`filex decrypt`, `filex vault mount`) is held to the same
// files.
//
// The vector random source is the DRBG of the format page: the AES-256-CTR
// keystream with key SHA-256(seed), counter block 0, the whole block counting
// up (WebCrypto `AES-CTR`, `length: 128`). The writer draws its random bytes
// synchronously, so the keystream of a generation is made once, up front, as
// long as the vectors say the generation draws.

import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

import { describe, expect, it, vi } from 'vitest';

import {
  E2E_KNOWN_FEATURES,
  encryptionLevel,
  markerIsVault,
  parseMarker,
  parseMarkerDetailed,
  unlockWithPassword,
  unlockWithRecoveryKey,
  vaultIdOf,
  type E2eMarker,
} from '../../../packages/core/src/lib/e2ecrypto';
import {
  VAULT_ENTRIES_WARN,
  VAULT_ENTRIES_WRITE_MAX,
  VAULT_ENTRY_FILE,
  VAULT_ENTRY_FOLDER,
  VAULT_INDEX_READ_MAX,
  compareNames,
  fromHex,
  indexFileSize,
  indexPath,
  packPath,
  readUvarint,
  streamBodySize,
  toHex,
  uvarint,
} from '../../../packages/core/src/lib/e2evault/layout';
import { deriveVaultKeyBits, isVaultFmk } from '../../../packages/core/src/lib/e2evault/keys';
import {
  decodeIndexPlaintext,
  emptyIndexState,
  encodeIndexBody,
  type VaultIndexState,
  type VaultNode,
} from '../../../packages/core/src/lib/e2evault/vindex';
import {
  VaultContentError,
  loadLatestGeneration,
  openIndexFile,
  readFileRange,
  readWholeFile,
  type FetchRange,
  type VaultKeys,
} from '../../../packages/core/src/lib/e2evault/reader';
import {
  VaultWriteError,
  VaultWriter,
  vaultLimitStatus,
  type VaultOp,
} from '../../../packages/core/src/lib/e2evault/writer';
import { checkPackHeader } from '../../../packages/core/src/lib/e2evault/pack';
import { repackCandidates } from '../../../packages/core/src/lib/e2evault/gc';

import * as legacy047 from '../fixtures/e2ecrypto-legacy-v0.47.0';

const TESTDATA = path.resolve(__dirname, '../../../backend/internal/e2edecrypt/testdata');
const FIXTURE = path.join(TESTDATA, 'vault', 'v3-vault');

interface TreeRow {
  path: string;
  kind: 'folder' | 'file';
  mtime: number;
  size?: number;
  sha256?: string;
  text?: string;
  content_id?: string;
  content_key?: string;
  chunk_log2?: number;
  extents?: Array<{ pack: string; offset: number; length: number }>;
}
interface GenVector {
  generation: number;
  ops: Array<Record<string, unknown>>;
  drbg_seed: string;
  drbg_bytes_used: number;
  index_path: string;
  index_size: number;
  index_sha256: string;
  packs_written: Array<{ id: string; path: string; used: number; sha256: string }>;
  seal_id?: string;
  index_key?: string;
  body_len?: number;
  body_hex?: string;
  pack_table?: string[];
  graveyard?: Array<{ pack: string; died: number }>;
  tree?: TreeRow[];
  /** The repack branch: the set S this generation copies, in table order. */
  repack?: string[];
}
const V = JSON.parse(fs.readFileSync(path.join(TESTDATA, 'vault-vectors.json'), 'utf8')) as {
  secrets: { password: string; recovery_key: string; iter: number; fmk: string; vault_id: string; vault_id_b64url: string };
  marker: E2eMarker;
  pack_log2: number;
  latest_generation: number;
  generations: GenVector[];
  canonical_4mib: { marker: E2eMarker; pack_log2: number; generations: GenVector[] };
  repack?: { pack_log2: number; from_generation: number; generations: GenVector[] };
  layers: {
    drbg: { seed: string; first_64_bytes: string };
    uvarint: Array<{ value: number; hex: string }>;
    padme_index_size: Array<{ body_len: number; min_len: number; file_size: number }>;
    stream_size: Array<{ size: number; chunk_log2: number; ciphertext: number }>;
    name_order: { input: string[]; sorted: string[] };
  };
  body_cases: Array<{ name: string; valid: boolean; why: string; pack_log2: number; generation: number; plaintext_hex: string; writable?: boolean }>;
  negative: Array<{ name: string; do: string; expect: string }>;
};

const hex = (s: string) => fromHex(s)!;
const sha = (b: Uint8Array) => createHash('sha256').update(b).digest('hex');

/** The vault as a storage holds it: relative path → bytes. */
function readFixture(): Map<string, Uint8Array> {
  const out = new Map<string, Uint8Array>();
  const walk = (dir: string, rel: string) => {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      const r = rel ? `${rel}/${e.name}` : e.name;
      if (e.isDirectory()) walk(path.join(dir, e.name), r);
      else out.set(r, new Uint8Array(fs.readFileSync(path.join(dir, e.name))));
    }
  };
  walk(FIXTURE, '');
  return out;
}

/** The vector DRBG's first `n` bytes for `seed`: AES-256-CTR, key SHA-256(seed). */
async function drbgBytes(seed: string, n: number): Promise<Uint8Array> {
  const keyRaw = new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(seed)));
  const key = await crypto.subtle.importKey('raw', keyRaw, { name: 'AES-CTR' }, false, ['encrypt']);
  return new Uint8Array(
    await crypto.subtle.encrypt({ name: 'AES-CTR', counter: new Uint8Array(16), length: 128 }, key, new Uint8Array(n)),
  );
}

/** A synchronous random source over a precomputed DRBG stream. */
function drbgSource(bytes: Uint8Array) {
  let pos = 0;
  const take = (n: number) => {
    if (pos + n > bytes.length) throw new Error(`DRBG overdrawn: ${pos} + ${n} > ${bytes.length}`);
    const out = bytes.slice(pos, pos + n);
    pos += n;
    return out;
  };
  return { take, used: () => pos };
}

function pattern(n: number, seed: number): Uint8Array {
  const out = new Uint8Array(n);
  for (let i = 0; i < n; i++) out[i] = (i * 31 + seed) & 0xff;
  return out;
}

function contentOf(c: Record<string, unknown>): Uint8Array {
  if (c.empty) return new Uint8Array(0);
  if (typeof c.text === 'string') return new TextEncoder().encode(c.text);
  const p = c.pattern as { len: number; seed: number };
  return pattern(p.len, p.seed);
}

function opsOf(g: GenVector): VaultOp[] {
  return g.ops.map((o): VaultOp => {
    if (o.op === 'mkdir') return { op: 'mkdir', path: String(o.path), mtime: Number(o.mtime) };
    if (o.op === 'write') return { op: 'write', path: String(o.path), mtime: Number(o.mtime), content: contentOf(o.content as Record<string, unknown>) };
    if (o.op === 'delete') return { op: 'delete', path: String(o.path) };
    return { op: 'move', from: String(o.from), to: String(o.to) };
  });
}

async function unlocked(marker: E2eMarker = V.marker): Promise<VaultKeys> {
  const fmk = await unlockWithPassword(marker, V.secrets.password);
  expect(fmk).not.toBeNull();
  return { fmk: fmk!, vaultId: vaultIdOf(marker)!, packLog2: marker.vault!.pack };
}

function fetchRangeFrom(files: Map<string, Uint8Array>): FetchRange {
  return async (pack, offset, length) => {
    const b = files.get(packPath(pack));
    if (!b) throw new Error(`no pack ${pack}`);
    return b.slice(offset, offset + length);
  };
}

function sourceOf(files: Map<string, Uint8Array>) {
  return {
    fetchIndex: async (g: number) => files.get(indexPath(g)) ?? null,
    listGenerations: async () =>
      [...files.keys()]
        .map((k) => /^v\/idx\/([0-9a-f]{16})\.fxi$/.exec(k))
        .filter((m): m is RegExpExecArray => !!m)
        .map((m) => parseInt(m[1], 16)),
    hasPacks: async () => [...files.keys()].some((k) => k.startsWith('v/p/')),
    sleep: async () => undefined,
  };
}

function treeRows(st: VaultIndexState) {
  return encodeIndexBody(st.tree, st.grave).entries.map((e: VaultNode) => ({
    path: e.path,
    kind: e.kind === VAULT_ENTRY_FOLDER ? 'folder' : 'file',
    mtime: e.mtime,
    ...(e.kind === VAULT_ENTRY_FILE ? { size: e.size } : {}),
  }));
}

describe('vault vectors 1 - unlock the key file', () => {
  it('the fixture key file is the vectors key file, and it is a vault', () => {
    const text = new TextDecoder().decode(readFixture().get('.filex-e2e.json'));
    const parsed = parseMarkerDetailed(text);
    expect(parsed).not.toBeNull();
    expect(parsed!.unsupported).toEqual([]);
    expect(parsed!.marker).toEqual(V.marker);
    expect(markerIsVault(parsed!.marker)).toBe(true);
    expect(encryptionLevel(parsed!.marker)).toBe('vault');
    expect(E2E_KNOWN_FEATURES).toContain('vault');
  });

  it('the password opens the FMK of the vectors, as an HKDF key', async () => {
    const fmk = await unlockWithPassword(V.marker, V.secrets.password);
    expect(isVaultFmk(fmk)).toBe(true);
    expect(fmk!.extractable).toBe(false);
    // The FMK is not extractable: it is proved by what it derives.
    const g1 = V.generations[0];
    const bits = await deriveVaultKeyBits(fmk!, hex(V.secrets.vault_id), 'index', hex(g1.seal_id!));
    expect(toHex(bits)).toBe(g1.index_key);
  });

  it('the recovery key opens the same FMK', async () => {
    const fmk = await unlockWithRecoveryKey(V.marker, V.secrets.recovery_key);
    expect(isVaultFmk(fmk)).toBe(true);
    const g1 = V.generations[0];
    expect(toHex(await deriveVaultKeyBits(fmk!, hex(V.secrets.vault_id), 'index', hex(g1.seal_id!)))).toBe(g1.index_key);
  });

  it('the vault id is the 16 bytes of vault.id', () => {
    expect(toHex(vaultIdOf(V.marker)!)).toBe(V.secrets.vault_id);
  });

  it('a filex 0.47 does not open a vault key file at all', () => {
    expect(legacy047.parseMarker(JSON.stringify(V.marker))).toBeNull();
  });
});

describe('vault vectors 2 - keys', () => {
  it('every index key and every content key', async () => {
    const keys = await unlocked();
    for (const g of V.generations) {
      expect(toHex(await deriveVaultKeyBits(keys.fmk, keys.vaultId, 'index', hex(g.seal_id!))), `index key ${g.generation}`).toBe(g.index_key);
      for (const row of g.tree ?? []) {
        if (!row.content_id) continue;
        expect(toHex(await deriveVaultKeyBits(keys.fmk, keys.vaultId, 'content', hex(row.content_id))), row.path).toBe(row.content_key);
      }
    }
  });
});

describe('vault vectors 3 - index files', () => {
  it('each generation decrypts to body_hex then zeros, and decodes to tree, pack table and graveyard', async () => {
    const keys = await unlocked();
    const files = readFixture();
    for (const g of V.generations) {
      const file = files.get(g.index_path)!;
      expect(file.length).toBe(g.index_size);
      expect(sha(file)).toBe(g.index_sha256);
      // The plaintext, by hand, under the vectors' index key.
      const k = await crypto.subtle.importKey('raw', hex(g.index_key!), { name: 'AES-GCM' }, false, ['decrypt']);
      const plain = new Uint8Array(
        await crypto.subtle.decrypt({ name: 'AES-GCM', iv: new Uint8Array(12), additionalData: file.slice(0, 40) }, k, file.slice(40)),
      );
      expect(toHex(plain.subarray(0, g.body_len!))).toBe(g.body_hex);
      expect(plain.subarray(g.body_len!).every((b) => b === 0)).toBe(true);

      const st = await openIndexFile(keys, g.generation, file);
      expect(st.generation).toBe(g.generation);
      expect(st.bodyLen).toBe(g.body_len);
      expect(st.packs).toEqual(g.pack_table);
      expect(st.grave).toEqual((g.graveyard ?? []).map((x) => ({ pack: x.pack, died: x.died })));
      expect(treeRows(st)).toEqual(
        (g.tree ?? []).map((r) => ({ path: r.path, kind: r.kind, mtime: r.mtime, ...(r.kind === 'file' ? { size: r.size } : {}) })),
      );
      for (const r of g.tree ?? []) {
        const n = st.tree.get(r.path)!;
        if (!r.content_id) {
          expect(n.content).toBeNull();
          continue;
        }
        expect(n.content!.id).toBe(r.content_id);
        expect(n.content!.log2).toBe(r.chunk_log2);
        expect(n.content!.extents).toEqual(r.extents);
      }
      // One encoding: the decoded tree encodes back to the same body.
      expect(toHex(encodeIndexBody(st.tree, st.grave).body)).toBe(g.body_hex);
    }
  });

  it('the latest generation is the highest index file', async () => {
    const keys = await unlocked();
    const files = readFixture();
    const loaded = await loadLatestGeneration(keys, V.latest_generation, sourceOf(files));
    expect(loaded.damagedLatest).toBe(false);
    expect(loaded.state.generation).toBe(3);
  });

  it('every pack of the fixture has a good header', () => {
    const files = readFixture();
    for (const g of V.generations) {
      for (const p of g.packs_written) {
        const b = files.get(p.path)!;
        expect(sha(b)).toBe(p.sha256);
        expect(() => checkPackHeader(b, p.id, V.pack_log2)).not.toThrow();
      }
    }
  });
});

describe('vault vectors 4 - contents', () => {
  it('every file of generations 2 and 3 reads back whole', async () => {
    const keys = await unlocked();
    const files = readFixture();
    const fr = fetchRangeFrom(files);
    for (const g of V.generations.slice(1)) {
      const st = await openIndexFile(keys, g.generation, files.get(g.index_path)!);
      for (const r of g.tree ?? []) {
        if (r.kind !== 'file') continue;
        const bytes = await readWholeFile(keys, st.tree.get(r.path)!, fr);
        expect(sha(bytes), r.path).toBe(r.sha256);
        if (r.text !== undefined) expect(new TextDecoder().decode(bytes)).toBe(r.text);
      }
    }
  });

  it('by ranges that cross a chunk and a pack boundary', async () => {
    const keys = await unlocked();
    const files = readFixture();
    const fr = fetchRangeFrom(files);
    const g2 = V.generations[1];
    const st = await openIndexFile(keys, 2, files.get(g2.index_path)!);
    const big = st.tree.get('Belgeler/Arşiv/büyük.bin')!;
    const whole = pattern(1048577, 7);
    for (const [a, b] of [
      [65400, 65600],
      [1048570, 1048577],
      [0, 1],
      [1048575, 1048577],
      [1048576, 1048577],
      [0, 1048577],
    ]) {
      const got = await readFileRange(keys, big, a, b, fr);
      expect(toHex(got), `${a}-${b}`).toBe(toHex(whole.subarray(a, b)));
    }
    // A span past the end is cut at the end.
    expect((await readFileRange(keys, big, 1048570, 2_000_000, fr)).length).toBe(7);
  });
});

describe('vault vectors 5 - writing, byte for byte', () => {
  async function replay(marker: E2eMarker, gens: GenVector[], packLog2: number) {
    const keys = await unlocked(marker);
    const written = new Map<string, Uint8Array>();
    let state: VaultIndexState = emptyIndexState(0);
    for (const g of gens) {
      const drbg = drbgSource(await drbgBytes(g.drbg_seed, g.drbg_bytes_used));
      const w = new VaultWriter(state, {
        fmk: keys.fmk,
        vaultId: keys.vaultId,
        packLog2,
        random: drbg.take,
        sink: {
          putPack: async (id, bytes) => void written.set(packPath(id), bytes),
          putIndex: async (gen, bytes) => void written.set(indexPath(gen), bytes),
        },
      });
      for (const op of opsOf(g)) await w.apply(op);
      const c = await w.commit();
      expect(c.generation).toBe(g.generation);
      expect(drbg.used(), `DRBG bytes of generation ${g.generation}`).toBe(g.drbg_bytes_used);
      expect(c.packsWritten).toEqual(g.packs_written.map((p) => p.id));
      for (const p of g.packs_written) expect(sha(written.get(p.path)!), p.path).toBe(p.sha256);
      expect(written.get(g.index_path)!.length).toBe(g.index_size);
      expect(sha(written.get(g.index_path)!), g.index_path).toBe(g.index_sha256);
      state = c.state;
    }
    return written;
  }

  it('generations 1 to 3 at 2^16 write the fixture', async () => {
    const written = await replay(V.marker, V.generations, V.pack_log2);
    const fixture = readFixture();
    for (const [p, b] of written) expect(sha(b), p).toBe(sha(fixture.get(p)!));
  });

  it('the same operations at 2^22 write canonical_4mib', async () => {
    await replay(V.canonical_4mib.marker, V.canonical_4mib.generations, V.canonical_4mib.pack_log2);
  }, 120_000);
});

// docs/E2E-VAULT-FORMAT.md → "Repacking", and the vectors' repack branch: from
// the fixture's generation 3, generation 4 writes three files, 5 deletes two,
// and 6 is the repack the planner calls for after 5 - the live extents of the
// whole table copied, bytes as they are and each piece an extent of its own,
// into one new pack. The Go twin is TestVaultVectors_10_Repack.
describe('vault vectors 10 - repack', () => {
  it('generations 4 to 6 write byte for byte, and 6 repacks the set the planner picks', async () => {
    const R = V.repack;
    expect(R, 'vault-vectors.json has no repack branch: run gen_vault_vectors.mjs').toBeDefined();
    expect(R!.pack_log2).toBe(V.pack_log2);
    expect(R!.from_generation).toBe(V.latest_generation);
    const last = R!.generations[R!.generations.length - 1];
    expect(last.repack?.length ?? 0, 'the branch ends in a repack').toBeGreaterThan(1);

    const keys = await unlocked();
    const files = readFixture();
    const written = new Map(files);
    let state: VaultIndexState = await openIndexFile(keys, V.latest_generation, files.get(indexPath(V.latest_generation))!);
    for (const g of R!.generations) {
      const drbg = drbgSource(await drbgBytes(g.drbg_seed, g.drbg_bytes_used));
      const w = new VaultWriter(state, {
        fmk: keys.fmk,
        vaultId: keys.vaultId,
        packLog2: R!.pack_log2,
        random: drbg.take,
        sink: {
          putPack: async (id, bytes) => void written.set(packPath(id), bytes),
          putIndex: async (gen, bytes) => void written.set(indexPath(gen), bytes),
        },
      });
      for (const op of opsOf(g)) await w.apply(op);
      if (g.repack) {
        const S = repackCandidates(state, R!.pack_log2);
        expect(S ? [...S] : null, `the packs to repack after generation ${g.generation - 1}`).toEqual(g.repack);
        await w.repack(S!, fetchRangeFrom(written));
      }
      const c = await w.commit();
      expect(c.generation).toBe(g.generation);
      expect(c.state.packs, `pack table of generation ${g.generation}`).toEqual(g.pack_table);
      expect(c.state.grave).toEqual((g.graveyard ?? []).map((x) => ({ pack: x.pack, died: x.died })));
      for (const r of g.tree ?? []) {
        if (r.content_id) expect(c.state.tree.get(r.path)!.content!.extents, r.path).toEqual(r.extents);
      }
      expect(drbg.used(), `DRBG bytes of generation ${g.generation}`).toBe(g.drbg_bytes_used);
      expect(c.packsWritten).toEqual(g.packs_written.map((p) => p.id));
      for (const p of g.packs_written) expect(sha(written.get(p.path)!), p.path).toBe(p.sha256);
      expect(sha(written.get(g.index_path)!), g.index_path).toBe(g.index_sha256);
      state = c.state;
    }
    expect(repackCandidates(state, R!.pack_log2), 'a repacked vault calls for no other repack').toBeNull();
    for (const p of last.repack!) expect(state.grave).toContainEqual({ pack: p, died: last.generation });

    // Every file of the repacked generation reads back from its new pack.
    const fr = fetchRangeFrom(written);
    for (const r of last.tree ?? []) {
      if (r.kind !== 'file') continue;
      expect(sha(await readWholeFile(keys, state.tree.get(r.path)!, fr)), r.path).toBe(r.sha256);
    }
  });
});

describe('vault vectors 6 - body cases', () => {
  for (const c of V.body_cases) {
    it(`${c.name}: ${c.why}`, () => {
      const run = () => decodeIndexPlaintext(hex(c.plaintext_hex), { generation: c.generation, packLog2: c.pack_log2 });
      if (!c.valid) {
        expect(run).toThrow();
        return;
      }
      const st = run();
      if (c.writable === false) {
        expect(st.hasExt).toBe(true);
        // A writer that meets extension bytes does not write the vault.
        expect(() => new VaultWriter(st, {} as never)).toThrow(VaultWriteError);
      } else {
        expect(st.hasExt).toBe(false);
      }
    });
  }
});

describe('vault vectors 7 - layers', () => {
  it('uvarint', () => {
    for (const u of V.layers.uvarint) {
      expect(toHex(uvarint(u.value)), String(u.value)).toBe(u.hex);
      expect(readUvarint(hex(u.hex))).toEqual({ value: u.value, length: u.hex.length / 2 });
    }
    // Refused: non-minimal, and past 2^53 - 1.
    expect(() => readUvarint(hex('8000'))).toThrow();
    expect(() => readUvarint(hex('ffffffffffffff1f'))).toThrow();
    expect(() => readUvarint(hex('ffffffffffffffff7f'))).toThrow();
  });

  it('padme_index_size', () => {
    for (const p of V.layers.padme_index_size) expect(indexFileSize(p.body_len), String(p.body_len)).toBe(p.file_size);
  });

  it('stream_size', () => {
    for (const s of V.layers.stream_size) expect(streamBodySize(s.size, s.chunk_log2)).toBe(s.ciphertext);
  });

  it('name_order: bytes, not a locale', () => {
    expect([...V.layers.name_order.input].sort(compareNames)).toEqual(V.layers.name_order.sorted);
  });

  it('drbg', async () => {
    expect(toHex(await drbgBytes(V.layers.drbg.seed, 64))).toBe(V.layers.drbg.first_64_bytes);
  });
});

describe('vault vectors 8 - negative cases', () => {
  const MARKER_TEXT = () => new TextDecoder().decode(readFixture().get('.filex-e2e.json'));

  it('wrong_password', async () => {
    expect(await unlockWithPassword(V.marker, 'vault vector password, not the real one')).toBeNull();
  });

  it('index_renamed: generation 4 is damaged, generation 3 is shown read-only', async () => {
    const keys = await unlocked();
    const files = readFixture();
    files.set(indexPath(4), files.get(indexPath(3))!);
    await expect(openIndexFile(keys, 4, files.get(indexPath(4))!)).rejects.toThrow();
    const loaded = await loadLatestGeneration(keys, 4, sourceOf(files));
    expect(loaded.damagedLatest).toBe(true);
    expect(loaded.state.generation).toBe(3);
    expect(loaded.latest).toBe(4);
  });

  it('index_bitflip: generation 3 is damaged, generation 2 is shown', async () => {
    const keys = await unlocked();
    const files = readFixture();
    const b = files.get(indexPath(3))!.slice();
    b[1000] ^= 1;
    files.set(indexPath(3), b);
    const loaded = await loadLatestGeneration(keys, 3, sourceOf(files));
    expect(loaded.damagedLatest).toBe(true);
    expect(loaded.state.generation).toBe(2);
  });

  it('index_wrong_size: one more byte is not a Padmé size', async () => {
    const keys = await unlocked();
    const files = readFixture();
    const b = files.get(indexPath(3))!;
    const longer = new Uint8Array(b.length + 1);
    longer.set(b);
    await expect(openIndexFile(keys, 3, longer)).rejects.toMatchObject({ code: 'index_size' });
  });

  it('pack_bitflip: büyük.bin fails as damaged, not.txt of generation 2 still reads', async () => {
    const keys = await unlocked();
    const files = readFixture();
    const g2 = V.generations[1];
    const st = await openIndexFile(keys, 2, files.get(g2.index_path)!);
    const big = st.tree.get('Belgeler/Arşiv/büyük.bin')!;
    const third = big.content!.extents[2];
    const p = files.get(packPath(third.pack))!.slice();
    p[third.offset + 1000] ^= 1;
    files.set(packPath(third.pack), p);
    await expect(readWholeFile(keys, big, fetchRangeFrom(files))).rejects.toBeInstanceOf(VaultContentError);
    const note = await readWholeFile(keys, st.tree.get('not.txt')!, fetchRangeFrom(files));
    expect(new TextDecoder().decode(note)).toBe('Kasadaki ilk not: çay demlendi.\n');
  });

  it('marker_req_extra: refused as malformed', () => {
    const m = JSON.parse(MARKER_TEXT());
    m.req = ['vault', 'names'];
    expect(parseMarkerDetailed(JSON.stringify(m))).toBeNull();
  });

  it('marker_vault_v2: refused, a newer filex is needed', () => {
    const m = JSON.parse(MARKER_TEXT());
    m.vault.v = 2;
    const parsed = parseMarkerDetailed(JSON.stringify(m));
    expect(parsed).not.toBeNull();
    expect(parsed!.unsupported.length).toBe(1);
    expect(parsed!.unsupported[0]).toContain('vault');
    expect(parseMarker(JSON.stringify(m))).toBeNull();
  });

  it('marker_kek: refused as malformed (a vault always has a wrapped FMK)', () => {
    const m = JSON.parse(MARKER_TEXT());
    m.fmk = 'kek';
    delete m.fmk_pw;
    expect(parseMarkerDetailed(JSON.stringify(m))).toBeNull();
  });

  it('every negative case of the vectors is covered here', () => {
    const covered = ['wrong_password', 'index_renamed', 'index_bitflip', 'index_wrong_size', 'pack_bitflip', 'marker_req_extra', 'marker_vault_v2', 'marker_kek'];
    expect(V.negative.map((n) => n.name).sort()).toEqual([...covered].sort());
  });
});

describe('vault vectors 9 - limits', () => {
  it('a writer warns from the 200 000th entry and refuses the 250 001st', async () => {
    expect(vaultLimitStatus(VAULT_ENTRIES_WARN - 1, 65536)).toBe('ok');
    expect(vaultLimitStatus(VAULT_ENTRIES_WARN, 65536)).toBe('warn');
    expect(vaultLimitStatus(VAULT_ENTRIES_WRITE_MAX, 65536)).toBe('full');

    const tree = new Map<string, VaultNode>();
    for (let i = 0; i < VAULT_ENTRIES_WRITE_MAX; i++) {
      const name = `d${i}`;
      tree.set(name, { path: name, parent: '', name, kind: VAULT_ENTRY_FOLDER, mtime: 0, size: 0, content: null });
    }
    const keys = await unlocked();
    const w = new VaultWriter(
      { generation: 7, tree, packs: [], grave: [], hasExt: false, bodyLen: 0 },
      { fmk: keys.fmk, vaultId: keys.vaultId, packLog2: 16, random: () => new Uint8Array(16), sink: { putPack: async () => undefined, putIndex: async () => undefined } },
    );
    await expect(w.apply({ op: 'mkdir', path: 'one-more', mtime: 0 })).rejects.toMatchObject({ code: 'too_many_entries' });
  });

  it('a reader refuses an index file over 64 MiB before decrypting it', async () => {
    const keys = await unlocked();
    const huge = new Uint8Array(VAULT_INDEX_READ_MAX + 1);
    const decrypt = vi.spyOn(crypto.subtle, 'decrypt');
    try {
      await expect(openIndexFile(keys, 3, huge)).rejects.toMatchObject({ code: 'index_too_large' });
      expect(decrypt).not.toHaveBeenCalled();
    } finally {
      decrypt.mockRestore();
    }
  });
});
