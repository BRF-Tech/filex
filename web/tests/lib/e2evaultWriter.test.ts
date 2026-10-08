// Writing a vault beyond the vectors (docs/E2E-VAULT-FORMAT.md → "Writing",
// "Garbage collection"): what each operation does to the tree, what a writer
// refuses before anything is written, a pack sent again under a new id, a
// commit sent again with the same bytes, a change stopped half way, contents
// that come as a Blob or a stream, the graveyard, the collection plan and
// repacking (bytes copied, nothing re-encrypted).

import { describe, expect, it, vi } from 'vitest';

import { createVault } from '../../../packages/core/src/lib/e2ecrypto';
import {
  VAULT_ENTRY_FILE,
  VAULT_ENTRY_FOLDER,
  VAULT_PACK_HEADER_LEN,
  indexPath,
  packDataSize,
  packPath,
  parseVaultPath,
  toHex,
} from '../../../packages/core/src/lib/e2evault/layout';
import { canonicalOrder, emptyIndexState, type VaultIndexState } from '../../../packages/core/src/lib/e2evault/vindex';
import { openIndexFile, readWholeFile, type FetchRange, type VaultKeys } from '../../../packages/core/src/lib/e2evault/reader';
import {
  VaultPackRetry,
  VaultWriteError,
  VaultWriter,
  cryptoRandom,
  type VaultSink,
} from '../../../packages/core/src/lib/e2evault/writer';
import { isExpired, planCollection, repackCandidates } from '../../../packages/core/src/lib/e2evault/gc';
import { checkPackHeader } from '../../../packages/core/src/lib/e2evault/pack';

const PACK_LOG2 = 16; // small packs: a few KB spans several

/** A storage in memory: what the server keeps. */
function store() {
  const files = new Map<string, Uint8Array>();
  const sink: VaultSink = {
    putPack: async (id, bytes) => void files.set(packPath(id), bytes),
    putIndex: async (gen, bytes) => void files.set(indexPath(gen), bytes),
  };
  const fetchRange: FetchRange = async (pack, offset, length) => files.get(packPath(pack))!.slice(offset, offset + length);
  return { files, sink, fetchRange };
}

async function newVault(): Promise<VaultKeys> {
  const made = await createVault('correct horse battery', { packLog2: 22 });
  return { fmk: made.fmk, vaultId: made.vaultId, packLog2: PACK_LOG2 };
}

function bytes(n: number, seed = 1): Uint8Array {
  const out = new Uint8Array(n);
  for (let i = 0; i < n; i++) out[i] = (i * 7 + seed) & 0xff;
  return out;
}

function writer(keys: VaultKeys, base: VaultIndexState, sink: VaultSink, extra: Partial<ConstructorParameters<typeof VaultWriter>[1]> = {}) {
  return new VaultWriter(base, { fmk: keys.fmk, vaultId: keys.vaultId, packLog2: PACK_LOG2, random: cryptoRandom, sink, ...extra });
}

describe('createVault', () => {
  it('a level-3 key file and generation 1, the empty tree in 64 KiB', async () => {
    const made = await createVault('correct horse battery', { packLog2: 24 });
    expect(made.marker.v).toBe(3);
    expect(made.marker.req).toEqual(['vault']);
    expect(made.marker.fmk).toBe('wrapped');
    expect(made.marker.vault).toEqual({ v: 1, id: expect.stringMatching(/^[A-Za-z0-9_-]{22}$/), pack: 24 });
    expect(made.marker.names).toBeUndefined();
    expect(made.index.length).toBe(65536);
    const st = await openIndexFile({ fmk: made.fmk, vaultId: made.vaultId, packLog2: 24 }, 1, made.index);
    expect(st.generation).toBe(1);
    expect(st.tree.size).toBe(0);
    expect(st.bodyLen).toBe(6);
  });

  it('only 4 MiB or 16 MiB packs are made', async () => {
    await expect(createVault('correct horse battery', { packLog2: 16 })).rejects.toThrow();
    await expect(createVault('correct horse battery', { packLog2: 20 })).rejects.toThrow();
  });
});

describe('operations', () => {
  it('mkdir, write, move a folder with what is under it, delete a folder with what is under it', async () => {
    const keys = await newVault();
    const s = store();
    const w = writer(keys, emptyIndexState(1), s.sink);
    await w.apply({ op: 'mkdir', path: 'A', mtime: 1 });
    await w.apply({ op: 'mkdir', path: 'A/B', mtime: 2 });
    await w.apply({ op: 'write', path: 'A/B/c.txt', mtime: 3, content: new TextEncoder().encode('hello') });
    await w.apply({ op: 'write', path: 'top.txt', mtime: 4, content: new Uint8Array(0) });
    await w.apply({ op: 'move', from: 'A', to: 'Z' });
    expect([...w.current.keys()].sort()).toEqual(['Z', 'Z/B', 'Z/B/c.txt', 'top.txt']);
    expect(w.current.get('Z/B/c.txt')!.parent).toBe('Z/B');
    expect(w.current.get('Z')!.mtime).toBe(1); // a move keeps mtime
    const c = await w.commit();
    expect(c.generation).toBe(2);
    expect(canonicalOrder(c.state.tree).map((e) => e.path)).toEqual(['Z', 'Z/B', 'Z/B/c.txt', 'top.txt']);
    const st = await openIndexFile(keys, 2, s.files.get(indexPath(2))!);
    expect(new TextDecoder().decode(await readWholeFile(keys, st.tree.get('Z/B/c.txt')!, s.fetchRange))).toBe('hello');

    const w2 = writer(keys, c.state, s.sink);
    await w2.apply({ op: 'delete', path: 'Z' });
    expect([...w2.current.keys()]).toEqual(['top.txt']);
  });

  it('refuses before writing: a taken name, a missing parent, a file as parent, a folder into itself, a bad name', async () => {
    const keys = await newVault();
    const s = store();
    const w = writer(keys, emptyIndexState(1), s.sink);
    await w.apply({ op: 'mkdir', path: 'A', mtime: 1 });
    await w.apply({ op: 'write', path: 'f', mtime: 1, content: new Uint8Array(0) });
    await expect(w.apply({ op: 'mkdir', path: 'A', mtime: 1 })).rejects.toMatchObject({ code: 'name_taken' });
    await expect(w.apply({ op: 'mkdir', path: 'X/Y', mtime: 1 })).rejects.toMatchObject({ code: 'parent_missing' });
    await expect(w.apply({ op: 'mkdir', path: 'f/Y', mtime: 1 })).rejects.toMatchObject({ code: 'parent_not_folder' });
    await expect(w.apply({ op: 'move', from: 'A', to: 'A/inner' })).rejects.toMatchObject({ code: 'into_itself' });
    await expect(w.apply({ op: 'write', path: 'A', mtime: 1, content: new Uint8Array(1) })).rejects.toMatchObject({ code: 'is_folder' });
    await expect(w.apply({ op: 'mkdir', path: '..', mtime: 1 })).rejects.toMatchObject({ code: 'bad_name' });
    await expect(w.apply({ op: 'mkdir', path: 'a\\b', mtime: 1 })).rejects.toMatchObject({ code: 'bad_name' });
    await expect(w.apply({ op: 'delete', path: 'nope' })).rejects.toMatchObject({ code: 'not_found' });
    expect(s.files.size).toBe(0);
  });

  it('names are stored NFC', async () => {
    const keys = await newVault();
    const s = store();
    const w = writer(keys, emptyIndexState(1), s.sink);
    // s + U+0327 COMBINING CEDILLA: how macOS spells the name (NFD).
    await w.apply({ op: 'mkdir', path: 'Ars' + String.fromCharCode(0x327) + 'iv', mtime: 1 });
    expect([...w.current.keys()]).toEqual(['Arşiv']);
  });

  it('new contents always get a new content id, even for the same bytes', async () => {
    const keys = await newVault();
    const s = store();
    const w1 = writer(keys, emptyIndexState(1), s.sink);
    await w1.apply({ op: 'write', path: 'a', mtime: 1, content: bytes(100) });
    const c1 = await w1.commit();
    const w2 = writer(keys, c1.state, s.sink);
    await w2.apply({ op: 'write', path: 'a', mtime: 2, content: bytes(100) });
    const c2 = await w2.commit();
    expect(c2.state.tree.get('a')!.content!.id).not.toBe(c1.state.tree.get('a')!.content!.id);
  });

  it('contents as a Blob and as a stream, across several packs', async () => {
    const keys = await newVault();
    const s = store();
    const w = writer(keys, emptyIndexState(1), s.sink);
    const big = bytes(3 * packDataSize(PACK_LOG2) + 123, 5);
    await w.apply({ op: 'write', path: 'blob.bin', mtime: 1, content: new Blob([big]) });
    await w.apply({
      op: 'write',
      path: 'stream.bin',
      mtime: 1,
      content: { size: big.length, stream: () => new Blob([big]).stream() as ReadableStream<Uint8Array> },
    });
    const c = await w.commit();
    for (const name of ['blob.bin', 'stream.bin']) {
      const got = await readWholeFile(keys, c.state.tree.get(name)!, s.fetchRange);
      expect(toHex(got) === toHex(big), name).toBe(true);
    }
    // Every pack is exactly 2^pack bytes, with a good header.
    for (const id of c.packsWritten) {
      const b = s.files.get(packPath(id))!;
      expect(b.length).toBe(2 ** PACK_LOG2);
      checkPackHeader(b, id, PACK_LOG2);
    }
  });

  it('a stream that says one size and yields another is refused', async () => {
    const keys = await newVault();
    const w = writer(keys, emptyIndexState(1), store().sink);
    await expect(
      w.apply({ op: 'write', path: 'x', mtime: 1, content: { size: 10, stream: () => new Blob([bytes(9)]).stream() as ReadableStream<Uint8Array> } }),
    ).rejects.toMatchObject({ code: 'size_mismatch' });
  });

  it('padding is random, never zeros', async () => {
    const keys = await newVault();
    const s = store();
    const w = writer(keys, emptyIndexState(1), s.sink);
    await w.apply({ op: 'write', path: 'a', mtime: 1, content: bytes(10) });
    const c = await w.commit();
    const pack = s.files.get(packPath(c.packsWritten[0]))!;
    const tail = pack.subarray(VAULT_PACK_HEADER_LEN + 26);
    expect(tail.filter((b) => b === 0).length).toBeLessThan(tail.length / 64);
  });
});

describe('a pack or a commit whose outcome is unknown', () => {
  it('a pack is sent again under a NEW id, and the index names the new one', async () => {
    const keys = await newVault();
    const s = store();
    let first = true;
    const tried: string[] = [];
    const sink: VaultSink = {
      putPack: async (id, b) => {
        tried.push(id);
        if (first) {
          first = false;
          s.files.set(packPath(id), b); // it did land: an orphan now
          throw new VaultPackRetry();
        }
        s.files.set(packPath(id), b);
      },
      putIndex: s.sink.putIndex,
    };
    const w = writer(keys, emptyIndexState(1), sink);
    await w.apply({ op: 'write', path: 'a', mtime: 1, content: bytes(1000) });
    const c = await w.commit();
    expect(tried.length).toBe(2);
    expect(tried[0]).not.toBe(tried[1]);
    expect(c.state.packs).toEqual([tried[1]]);
    expect(c.state.tree.get('a')!.content!.extents.every((x) => x.pack === tried[1])).toBe(true);
    // Only the header changed.
    const a = s.files.get(packPath(tried[0]))!;
    const b = s.files.get(packPath(tried[1]))!;
    expect(toHex(a.subarray(VAULT_PACK_HEADER_LEN))).toBe(toHex(b.subarray(VAULT_PACK_HEADER_LEN)));
    const st = await openIndexFile(keys, 2, s.files.get(indexPath(2))!);
    expect(toHex(await readWholeFile(keys, st.tree.get('a')!, s.fetchRange))).toBe(toHex(bytes(1000)));
  });

  it('a commit is sent again with the SAME bytes (nothing is sealed twice)', async () => {
    const keys = await newVault();
    const s = store();
    const sent: Uint8Array[] = [];
    const sink: VaultSink = {
      putPack: s.sink.putPack,
      putIndex: async (gen, b) => {
        sent.push(b);
        if (sent.length === 1) throw new VaultPackRetry();
        s.files.set(indexPath(gen), b);
      },
    };
    const w = writer(keys, emptyIndexState(1), sink);
    await w.apply({ op: 'mkdir', path: 'A', mtime: 1 });
    await w.commit();
    expect(sent.length).toBe(2);
    expect(toHex(sent[0])).toBe(toHex(sent[1]));
  });

  it('a change stopped half way commits nothing', async () => {
    const keys = await newVault();
    const s = store();
    const ctl = new AbortController();
    const putIndex = vi.fn(s.sink.putIndex);
    const w = writer(keys, emptyIndexState(1), { putPack: s.sink.putPack, putIndex }, { signal: ctl.signal });
    await w.apply({ op: 'write', path: 'a', mtime: 1, content: bytes(10) });
    ctl.abort();
    await expect(w.commit()).rejects.toMatchObject({ name: 'AbortError' });
    expect(putIndex).not.toHaveBeenCalled();
  });
});

describe('the graveyard and the collection', () => {
  it('packs the tree no longer uses go to the graveyard, minus the ones this writer deleted', async () => {
    const keys = await newVault();
    const s = store();
    const w1 = writer(keys, emptyIndexState(1), s.sink);
    await w1.apply({ op: 'write', path: 'a', mtime: 1, content: bytes(10) });
    await w1.apply({ op: 'write', path: 'b', mtime: 1, content: bytes(packDataSize(PACK_LOG2), 3) });
    const c1 = await w1.commit();
    expect(c1.state.packs.length).toBe(2);
    const w2 = writer(keys, c1.state, s.sink);
    await w2.apply({ op: 'delete', path: 'a' });
    await w2.apply({ op: 'delete', path: 'b' });
    const c2 = await w2.commit();
    expect(c2.state.packs).toEqual([]);
    expect(c2.state.grave.map((g) => g.died)).toEqual([3, 3]);
    const deleted = new Set([c2.state.grave[0].pack]);
    const w3 = writer(keys, c2.state, s.sink, { deleted });
    await w3.apply({ op: 'mkdir', path: 'x', mtime: 1 });
    const c3 = await w3.commit();
    expect(c3.state.grave.map((g) => g.pack)).toEqual([c2.state.grave[1].pack]);
  });

  it('retention: the three newest generations are kept, and any replaced less than 15 minutes ago', () => {
    const now = 10_000_000;
    const at = new Map<number, number>([
      [5, now - 60 * 60_000],
      [6, now - 20 * 60_000],
      [7, now - 10 * 60_000],
      [8, now - 60_000],
      [9, now - 1000],
    ]);
    expect(isExpired(9, 9, at, now)).toBe(false);
    expect(isExpired(7, 9, at, now)).toBe(false);
    expect(isExpired(6, 9, at, now)).toBe(false); // 7 came 10 minutes ago
    expect(isExpired(5, 9, at, now)).toBe(true); // 6 came 20 minutes ago
    expect(isExpired(2, 9, at, now)).toBe(true); // 3 is gone: long ago
  });

  it('plans index files, graveyard packs and orphans; never what this writer uploaded', () => {
    const now = 10_000_000;
    const P = (n: number) => n.toString(16).padStart(32, '0');
    const latest: VaultIndexState = {
      generation: 9,
      tree: new Map([['a', { path: 'a', parent: '', name: 'a', kind: VAULT_ENTRY_FILE, mtime: 0, size: 5, content: { id: P(99), log2: 20, extents: [{ pack: P(1), offset: 32, length: 21 }] } }]]),
      packs: [P(1)],
      grave: [
        { pack: P(2), died: 6 }, // last user 5: expired
        { pack: P(3), died: 8 }, // last user 7: kept
      ],
      hasExt: false,
      bodyLen: 100,
    };
    const old = now - 60 * 60_000;
    const plan = planCollection({
      latest,
      indexes: [5, 6, 7, 8, 9].map((g) => ({ generation: g, mtime: g >= 8 ? now - 1000 : old })),
      packs: [P(1), P(2), P(3), P(4), P(5)].map((id) => ({ id })),
      uploaded: new Set([P(5)]),
      now,
    });
    expect(plan.indexes).toEqual([5, 6]);
    expect(plan.packs).toEqual([P(2), P(4)]);
    // Nothing while the latest carries extension bytes.
    expect(planCollection({ latest: { ...latest, hasExt: true }, indexes: [], packs: [{ id: P(4) }], uploaded: new Set(), now })).toEqual({ indexes: [], packs: [] });
  });

  it('at most 1 000 deletions in one pass', () => {
    const latest: VaultIndexState = { ...emptyIndexState(4), bodyLen: 6 };
    const packs = Array.from({ length: 1500 }, (_, i) => ({ id: i.toString(16).padStart(32, '0') }));
    const plan = planCollection({ latest, indexes: [], packs, uploaded: new Set(), now: 0 });
    expect(plan.packs.length + plan.indexes.length).toBe(1000);
  });
});

describe('repacking', () => {
  it('copies the live extents of half-empty packs into new packs, bytes as they are', async () => {
    const keys = await newVault();
    const s = store();
    const area = packDataSize(PACK_LOG2);
    // Four packs, each mostly one big file plus a small one.
    let state = emptyIndexState(1);
    for (let i = 0; i < 4; i++) {
      const w = writer(keys, state, s.sink);
      await w.apply({ op: 'write', path: `big${i}`, mtime: 1, content: bytes(area - 200, i) });
      await w.apply({ op: 'write', path: `small${i}`, mtime: 1, content: bytes(100, 50 + i) });
      state = (await w.commit()).state;
    }
    expect(repackCandidates(state, PACK_LOG2)).toBeNull();
    // Delete the big files: four packs with ~120 live bytes each.
    const wd = writer(keys, state, s.sink);
    for (let i = 0; i < 4; i++) await wd.apply({ op: 'delete', path: `big${i}` });
    state = (await wd.commit()).state;
    const S = repackCandidates(state, PACK_LOG2);
    expect(S).not.toBeNull();
    const before = new Map([...state.tree].map(([p, n]) => [p, n.content!.id]));
    const wr = writer(keys, state, s.sink);
    const enc = vi.spyOn(crypto.subtle, 'encrypt');
    await wr.repack(S!, s.fetchRange);
    const c = await wr.commit();
    // Nothing re-encrypted but the new index.
    expect(enc).toHaveBeenCalledTimes(1);
    enc.mockRestore();
    expect(c.state.packs.length).toBe(1);
    expect([...S!].every((id) => c.state.grave.some((g) => g.pack === id))).toBe(true);
    for (const [p, n] of c.state.tree) {
      expect(n.content!.id).toBe(before.get(p)); // same content id: copied, not re-sealed
      expect(n.content!.extents.length).toBe(1); // one extent before, one after
      expect(toHex(await readWholeFile(keys, n, s.fetchRange))).toBe(toHex(bytes(100, 50 + Number(p.slice(5)))));
    }
  });

  // docs/E2E-VAULT-FORMAT.md → "Details the implementations settled": a
  // repack merges nothing, so two writers lay it out alike. The Go writer is
  // held to the same (vault_gc_test.go TestVaultRepack_KeepsExtentsApart).
  it('a file whose extents land side by side in one new pack keeps them as two', async () => {
    const keys = await newVault();
    const s = store();
    const area = packDataSize(PACK_LOG2);
    // The filler leaves 488 bytes of the first pack: the file's body (3 016
    // bytes) is 488 there and 2 528 at the start of the second.
    const data = bytes(3000, 9);
    const w1 = writer(keys, emptyIndexState(0), s.sink);
    await w1.apply({ op: 'write', path: 'filler', mtime: 1, content: bytes(area - 488 - 16, 3) });
    await w1.apply({ op: 'write', path: 'x', mtime: 1, content: data });
    let state = (await w1.commit()).state;
    expect(state.tree.get('x')!.content!.extents.length).toBe(2);
    const wd = writer(keys, state, s.sink);
    await wd.apply({ op: 'delete', path: 'filler' });
    state = (await wd.commit()).state;
    const S = repackCandidates(state, PACK_LOG2);
    expect(S?.size).toBe(2);
    const wr = writer(keys, state, s.sink);
    await wr.repack(S!, s.fetchRange);
    const c = await wr.commit();
    expect(c.state.packs.length).toBe(1);
    const ext = c.state.tree.get('x')!.content!.extents;
    expect(ext.map((x) => [x.offset, x.length])).toEqual([
      [VAULT_PACK_HEADER_LEN, 488],
      [VAULT_PACK_HEADER_LEN + 488, 2528],
    ]);
    expect(ext[0].pack).toBe(ext[1].pack);
    expect(toHex(await readWholeFile(keys, c.state.tree.get('x')!, s.fetchRange))).toBe(toHex(data));
  });
});

describe('paths', () => {
  it('what is a vault path on the storage', () => {
    expect(parseVaultPath('.filex-e2e.json')).toEqual({ kind: 'keyfile' });
    expect(parseVaultPath('v/idx/0000000000000003.fxi')).toEqual({ kind: 'index', gen: 3 });
    expect(parseVaultPath('v/p/b0/b067d7bcd62c9f817216a5ef1b1b653b.fxp')).toEqual({ kind: 'pack', id: 'b067d7bcd62c9f817216a5ef1b1b653b' });
    expect(parseVaultPath('v/p/aa/b067d7bcd62c9f817216a5ef1b1b653b.fxp')).toEqual({ kind: 'other' });
    expect(parseVaultPath('v/idx/.tmp-123')).toEqual({ kind: 'temp' });
    expect(parseVaultPath('v/idx/3.fxi')).toEqual({ kind: 'other' });
    expect(packPath('b067d7bcd62c9f817216a5ef1b1b653b')).toBe('v/p/b0/b067d7bcd62c9f817216a5ef1b1b653b.fxp');
    expect(indexPath(3)).toBe('v/idx/0000000000000003.fxi');
  });

  it('a kind is a folder or a file', () => {
    expect(VAULT_ENTRY_FOLDER).toBe(1);
    expect(VAULT_ENTRY_FILE).toBe(2);
  });
});
