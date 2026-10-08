// Vaults one implementation wrote, frozen on disk for the other - the vault
// level's twin of e2eFixtureFolders (the browser's folders, read by Go) and
// e2eGoFixtureFolders (Go's folders, read here):
//
//   backend/internal/e2edecrypt/testdata/vault-go/go-vault
//       written by the Go writer (`filex vault mount`, `filex vault prune`:
//       vault_crossfixture_test.go TestVaultGoFixture_Write); opened here
//   backend/internal/e2edecrypt/testdata/vault-web/web-vault
//       written by THIS code's writer (below, under FILEX_WRITE_E2E_FIXTURES=1);
//       opened by Go in TestVaultWebFixture_OpenWithGo
//
// The vectors hold both writers to one reference byte for byte; these hold
// each reader to what the OTHER writer really wrote - its random ids, its key
// file, its padding, a repack - with the same operations on both sides
// (crossOps here, vaultCrossOps in Go): four generations, names in byte order
// (`Zebra` < `alfa` < `cay.txt` < `Çay.txt`), an empty file, a file over three
// packs, a folder moved with what is in it, and a repack of the half-empty
// packs.
//
// Regenerating is deliberate, never incidental:
//
//   cd web && FILEX_WRITE_E2E_FIXTURES=1 npx vitest run tests/lib/e2eVaultCrossFixtures.test.ts
//
// The reading tests run every time.

import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

import { describe, expect, it } from 'vitest';

import {
  E2E_MARKER_NAME,
  createVault,
  markerIsVault,
  parseMarker,
  unlockWithPassword,
  unlockWithRecoveryKey,
  vaultIdOf,
  type E2eMarker,
} from '../../../packages/core/src/lib/e2ecrypto';
import { VAULT_ENTRY_FOLDER, VAULT_INDEX_MIN_SIZE, indexPath, packPath, toHex } from '../../../packages/core/src/lib/e2evault/layout';
import { checkPackHeader } from '../../../packages/core/src/lib/e2evault/pack';
import {
  loadLatestGeneration,
  openIndexFile,
  readFileRange,
  readWholeFile,
  type FetchRange,
  type VaultKeys,
} from '../../../packages/core/src/lib/e2evault/reader';
import { repackCandidates } from '../../../packages/core/src/lib/e2evault/gc';
import type { VaultIndexState } from '../../../packages/core/src/lib/e2evault/vindex';
import { VaultWriter, cryptoRandom, type VaultOp } from '../../../packages/core/src/lib/e2evault/writer';

const TESTDATA = path.resolve(__dirname, '../../../backend/internal/e2edecrypt/testdata');
const GO_ROOT = path.join(TESTDATA, 'vault-go');
const WEB_ROOT = path.join(TESTDATA, 'vault-web');
const PACK_LOG2 = 16;
const T0 = 1791277200000;
const SLOW = 180_000;

interface Entry {
  dir?: boolean;
  mtime: number;
  size?: number;
  sha256?: string;
}
type Tree = Record<string, Entry>;
interface CrossCase {
  password: string;
  recovery_key: string;
  pack_log2: number;
  latest: number;
  generations: Record<string, Tree>;
  repacked: number;
}

const sha = (b: Uint8Array) => createHash('sha256').update(b).digest('hex');
const utf8 = (s: string) => new TextEncoder().encode(s);

function pattern(n: number, seed: number): Uint8Array {
  const out = new Uint8Array(n);
  for (let i = 0; i < n; i++) out[i] = (i * 31 + seed) & 0xff;
  return out;
}

/** The operations of generations 2 and 3 - the same as vaultCrossOps in Go. */
function crossOps(gen: 2 | 3, who: string): VaultOp[] {
  if (gen === 2) {
    return [
      { op: 'mkdir', path: 'Belgeler', mtime: T0 },
      { op: 'mkdir', path: 'Belgeler/Arşiv', mtime: T0 + 1000 },
      { op: 'mkdir', path: 'Zebra', mtime: T0 + 2000 },
      { op: 'mkdir', path: 'alfa', mtime: T0 + 3000 },
      { op: 'write', path: 'not.txt', mtime: T0 + 4000, content: utf8(`${who} yazdı, öbürü okur.\n`) },
      { op: 'write', path: 'Belgeler/boş.txt', mtime: T0 + 5000, content: new Uint8Array(0) },
      { op: 'write', path: 'Belgeler/Arşiv/rapor.bin', mtime: T0 + 6000, content: pattern(150_000, 5) },
      { op: 'write', path: 'cay.txt', mtime: T0 + 7000, content: utf8('c\n') },
      { op: 'write', path: 'Çay.txt', mtime: T0 + 8000, content: utf8('Ç\n') },
      { op: 'write', path: 'Zebra/ü.md', mtime: T0 + 10000, content: pattern(700, 9) },
    ];
  }
  return [
    { op: 'delete', path: 'Belgeler/Arşiv/rapor.bin' },
    { op: 'move', from: 'Belgeler/boş.txt', to: 'boş.txt' },
    { op: 'write', path: 'not.txt', mtime: T0 + 60000, content: utf8(`${who} yazdı; bu ikinci sürüm.\n`) },
    { op: 'mkdir', path: 'Yeni', mtime: T0 + 61000 },
    { op: 'move', from: 'Zebra', to: 'Yeni/Zebra' },
  ];
}

/** A vault folder on disk: its packs and index files by path. */
function diskOf(dir: string) {
  const read = (rel: string) => {
    const p = path.join(dir, ...rel.split('/'));
    return fs.existsSync(p) ? new Uint8Array(fs.readFileSync(p)) : null;
  };
  const fetchRange: FetchRange = async (pack, offset, length) => {
    const b = read(packPath(pack));
    if (!b) throw new Error(`no pack ${pack}`);
    return b.slice(offset, offset + length);
  };
  const generations = () =>
    fs.existsSync(path.join(dir, 'v', 'idx'))
      ? fs
          .readdirSync(path.join(dir, 'v', 'idx'))
          .map((n) => /^([0-9a-f]{16})\.fxi$/.exec(n))
          .filter((m): m is RegExpExecArray => !!m)
          .map((m) => parseInt(m[1], 16))
      : [];
  const packs = () => {
    const out: string[] = [];
    const root = path.join(dir, 'v', 'p');
    if (!fs.existsSync(root)) return out;
    for (const xx of fs.readdirSync(root)) {
      for (const f of fs.readdirSync(path.join(root, xx))) {
        const m = /^([0-9a-f]{32})\.fxp$/.exec(f);
        if (m && m[1].startsWith(xx)) out.push(m[1]);
      }
    }
    return out.sort();
  };
  return { read, fetchRange, generations, packs };
}

/** A generation's tree in secrets.json's shape, read back through the reader. */
async function treeOf(keys: VaultKeys, st: VaultIndexState, fetchRange: FetchRange): Promise<Tree> {
  const out: Tree = {};
  for (const [p, n] of st.tree) {
    if (n.kind === VAULT_ENTRY_FOLDER) {
      out[p] = { dir: true, mtime: n.mtime };
      continue;
    }
    const data = await readWholeFile(keys, n, fetchRange);
    out[p] = { mtime: n.mtime, ...(n.size ? { size: n.size } : {}), sha256: sha(data) };
  }
  return out;
}

/** Entries compared as values: a missing size is 0, a missing dir false. */
function norm(t: Tree): Record<string, Required<Entry>> {
  const out: Record<string, Required<Entry>> = {};
  for (const [p, e] of Object.entries(t)) out[p] = { dir: !!e.dir, mtime: e.mtime, size: e.size ?? 0, sha256: e.sha256 ?? '' };
  return out;
}

/** Open one cross fixture with the browser's code: every kept generation, by
 *  password and by recovery key, a range over a pack boundary, every pack. */
async function checkCross(root: string, name: string, c: CrossCase): Promise<void> {
  const dir = path.join(root, name);
  const disk = diskOf(dir);
  const marker = parseMarker(fs.readFileSync(path.join(dir, E2E_MARKER_NAME), 'utf8'));
  expect(marker, `${name}: the key file parses here`).not.toBeNull();
  expect(markerIsVault(marker), name).toBe(true);
  expect(marker!.vault!.pack, name).toBe(c.pack_log2);

  for (const how of ['password', 'recovery'] as const) {
    const fmk = how === 'password' ? await unlockWithPassword(marker!, c.password) : await unlockWithRecoveryKey(marker!, c.recovery_key);
    expect(fmk, `${name}: the ${how} opens it`).not.toBeNull();
    const keys: VaultKeys = { fmk: fmk!, vaultId: vaultIdOf(marker)!, packLog2: c.pack_log2 };

    const latest = Math.max(...disk.generations());
    expect(latest, name).toBe(c.latest);
    const loaded = await loadLatestGeneration(keys, latest, {
      fetchIndex: async (g) => disk.read(indexPath(g)),
      listGenerations: async () => disk.generations(),
      sleep: async () => undefined,
    });
    expect(loaded.damagedLatest, name).toBe(false);
    expect(loaded.state.generation, name).toBe(c.latest);
    expect(loaded.state.hasExt, `${name}: writable here`).toBe(false);

    const g1 = disk.read(indexPath(1))!;
    expect(g1.length).toBe(VAULT_INDEX_MIN_SIZE);
    expect((await openIndexFile(keys, 1, g1)).tree.size, `${name}: generation 1 is the empty tree`).toBe(0);

    for (const [g, want] of Object.entries(c.generations)) {
      const st = await openIndexFile(keys, Number(g), disk.read(indexPath(Number(g)))!);
      expect(norm(await treeOf(keys, st, disk.fetchRange)), `${name} generation ${g}`).toEqual(norm(want));
      if (g === '2') {
        // Bytes 65 400 to 65 600 of the file over three packs: across the end
        // of the first.
        const n = st.tree.get('Belgeler/Arşiv/rapor.bin')!;
        const got = await readFileRange(keys, n, 65_400, 65_600, disk.fetchRange);
        expect(toHex(got)).toBe(toHex(pattern(150_000, 5).subarray(65_400, 65_600)));
      }
    }

    const packs = disk.packs();
    expect(packs.length, name).toBeGreaterThan(0);
    for (const id of packs) {
      const b = disk.read(packPath(id))!;
      expect(b.length, `${name}: pack ${id}`).toBe(2 ** c.pack_log2);
      expect(() => checkPackHeader(b, id, c.pack_log2), `${name}: pack ${id}`).not.toThrow();
    }
  }
}

function loadSecrets(root: string, regen: string): { cases: Record<string, CrossCase> } {
  const p = path.join(root, 'secrets.json');
  expect(fs.existsSync(p), `${root} is not generated: ${regen}`).toBe(true);
  return JSON.parse(fs.readFileSync(p, 'utf8')) as { cases: Record<string, CrossCase> };
}

describe.skipIf(!process.env.FILEX_WRITE_E2E_FIXTURES)('write the browser vault for Go (FILEX_WRITE_E2E_FIXTURES=1)', () => {
  it(
    'web-vault: four generations by the browser writer',
    async () => {
      const password = 'fixture-web-vault-password';
      fs.rmSync(WEB_ROOT, { recursive: true, force: true });
      const dir = path.join(WEB_ROOT, 'web-vault');
      fs.mkdirSync(dir, { recursive: true });
      const write = (rel: string, b: Uint8Array) => {
        const p = path.join(dir, ...rel.split('/'));
        fs.mkdirSync(path.dirname(p), { recursive: true });
        fs.writeFileSync(p, b);
      };
      // Writers make 4 or 16 MiB packs; the fixture's are 64 KiB (readers
      // accept 2^16), as in the vectors, to keep the repository small.
      const made = await createVault(password, { packLog2: 22 });
      const marker: E2eMarker = { ...made.marker, vault: { ...made.marker.vault!, pack: PACK_LOG2 } };
      write(E2E_MARKER_NAME, utf8(JSON.stringify(marker)));
      write(indexPath(1), made.index);
      const keys: VaultKeys = { fmk: made.fmk, vaultId: made.vaultId, packLog2: PACK_LOG2 };
      const disk = diskOf(dir);
      const commit = async (base: VaultIndexState, run: (w: VaultWriter) => Promise<void>) => {
        const w = new VaultWriter(base, {
          fmk: keys.fmk,
          vaultId: keys.vaultId,
          packLog2: PACK_LOG2,
          random: cryptoRandom,
          sink: {
            putPack: async (id, b) => write(packPath(id), b),
            putIndex: async (g, b) => write(indexPath(g), b),
          },
        });
        await run(w);
        const c = await w.commit();
        return openIndexFile(keys, c.generation, disk.read(indexPath(c.generation))!);
      };

      const out: CrossCase = { password, recovery_key: made.recoveryKey, pack_log2: PACK_LOG2, latest: 0, generations: {}, repacked: 0 };
      let st = await openIndexFile(keys, 1, made.index);
      expect(st.generation).toBe(1);
      for (const g of [2, 3] as const) {
        st = await commit(st, async (w) => {
          for (const op of crossOps(g, 'Tarayıcı')) await w.apply(op);
        });
        out.generations[String(g)] = await treeOf(keys, st, disk.fetchRange);
      }
      const S = repackCandidates(st, PACK_LOG2);
      expect(S, 'generation 3 leaves half-empty packs to repack').not.toBeNull();
      st = await commit(st, (w) => w.repack(S!, disk.fetchRange));
      out.generations['4'] = await treeOf(keys, st, disk.fetchRange);
      out.latest = st.generation;
      out.repacked = S!.size;

      fs.writeFileSync(
        path.join(WEB_ROOT, 'secrets.json'),
        JSON.stringify(
          {
            comment:
              'Test fixtures only. Written by web/tests/lib/e2eVaultCrossFixtures.test.ts (FILEX_WRITE_E2E_FIXTURES=1). ' +
              'The password and recovery key here open nothing but this vault.',
            cases: { 'web-vault': out },
          },
          null,
          2,
        ) + '\n',
      );
    },
    SLOW,
  );
});

describe('vaults written by the other implementation', () => {
  it(
    'the browser opens the vault the Go writer made (testdata/vault-go)',
    async () => {
      const s = loadSecrets(GO_ROOT, 'FILEX_WRITE_E2E_FIXTURES=1 go test -run TestVaultGoFixture_Write ./internal/e2edecrypt/');
      expect(Object.keys(s.cases)).toEqual(['go-vault']);
      await checkCross(GO_ROOT, 'go-vault', s.cases['go-vault']);
    },
    SLOW,
  );

  it(
    'the browser still opens the vault it made (testdata/vault-web; Go opens it in TestVaultWebFixture_OpenWithGo)',
    async () => {
      const s = loadSecrets(WEB_ROOT, 'cd web && FILEX_WRITE_E2E_FIXTURES=1 npx vitest run tests/lib/e2eVaultCrossFixtures.test.ts');
      expect(Object.keys(s.cases)).toEqual(['web-vault']);
      await checkCross(WEB_ROOT, 'web-vault', s.cases['web-vault']);
    },
    SLOW,
  );
});
