// Encrypted folders the GO writer made (`filex encrypt`), opened by the
// BROWSER code - the mirror of e2eFixtureFolders.test.ts, where the browser
// writes and `filex decrypt` reads.
//
// backend/internal/e2edecrypt/testdata/folders-go/ was written by
// backend/internal/e2edecrypt/gofixture_test.go (FILEX_WRITE_E2E_FIXTURES=1):
//
//   go-v2         level 1 from a folder on disk, plus a STREAM (0x02) file
//   go-v3-names   level 2: names sealed per folder, a long name + sidecar
//   go-v3-conv    a key file mid-conversion: one file encrypted, one not yet
//
// secrets.json holds each case's password, recovery key and plaintext tree.
// If this fails, either the Go writer drifted from the format or the browser
// stopped reading what an earlier filex wrote - both are the same bug.

import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

import { describe, expect, it } from 'vitest';

import {
  E2E_MARKER_NAME,
  conversionPending,
  decryptFileAny,
  hasMagic,
  markerHasNames,
  parseMarker,
  unlockNameKey,
  unlockWithPassword,
  unlockWithRecoveryKey,
} from '../../../packages/core/src/lib/e2ecrypto';
import { decryptStoredName, effectiveDirId, type E2eNameKey } from '../../../packages/core/src/lib/e2enames';

const ROOT = path.resolve(__dirname, '../../../backend/internal/e2edecrypt/testdata/folders-go');
const SLOW = 120_000;

type Tree = Record<string, string>;

interface GoCase {
  password: string;
  recovery_key: string;
  marker_v: number;
  names: boolean;
  conv: boolean;
  tree: Tree;
}

const sha = (b: Uint8Array) => createHash('sha256').update(b).digest('hex');
const ab = (b: Uint8Array) => new Uint8Array(b).buffer as ArrayBuffer;

/** The plaintext tree of a Go-written folder, read with the browser's code. */
async function readBack(dir: string, fmk: CryptoKey, names: E2eNameKey | null): Promise<{ tree: Tree; plain: string[] }> {
  const tree: Tree = {};
  const plain: string[] = [];
  const walk = async (abs: string, rel: string, isRoot: boolean, dirId: Uint8Array | null) => {
    for (const ent of fs.readdirSync(abs, { withFileTypes: true })) {
      if (isRoot && ent.name === E2E_MARKER_NAME) continue;
      let name = ent.name;
      if (names && dirId) {
        const got = await decryptStoredName(names, ent.name, dirId, async (side) => {
          const p = path.join(abs, side);
          return fs.existsSync(p) ? fs.readFileSync(p, 'utf8') : null;
        });
        if (got.state === 'sidecar') continue;
        expect(got.state, `${rel}${ent.name}: the name Go sealed opens here`).toBe('enc');
        name = got.name!;
      }
      const r = rel + name;
      if (ent.isDirectory()) {
        tree[r + '/'] = 'dir';
        await walk(path.join(abs, ent.name), r + '/', false, names && dirId ? await effectiveDirId(names, dirId, ent.name) : null);
        continue;
      }
      const data = new Uint8Array(fs.readFileSync(path.join(abs, ent.name)));
      if (!hasMagic(data)) plain.push(r);
      tree[r] = sha(hasMagic(data) ? new Uint8Array(await decryptFileAny(fmk, null, ab(data))) : data);
    }
  };
  await walk(dir, '', true, names?.rootId ?? null);
  return { tree, plain };
}

describe('encrypted folders the Go writer made (filex encrypt), read by the browser', () => {
  it(
    'every case opens by password and by recovery key, names and contents',
    async () => {
      const secretsPath = path.join(ROOT, 'secrets.json');
      expect(
        fs.existsSync(secretsPath),
        'testdata/folders-go is not generated: FILEX_WRITE_E2E_FIXTURES=1 go test -run TestGoFixtures_Write ./internal/e2edecrypt/',
      ).toBe(true);
      const secrets = JSON.parse(fs.readFileSync(secretsPath, 'utf8')) as { cases: Record<string, GoCase> };
      expect(Object.keys(secrets.cases).sort()).toEqual(['go-v2', 'go-v3-conv', 'go-v3-names']);

      for (const [name, c] of Object.entries(secrets.cases)) {
        const dir = path.join(ROOT, name);
        const marker = parseMarker(fs.readFileSync(path.join(dir, E2E_MARKER_NAME), 'utf8'));
        expect(marker, `${name}: the browser parses the key file Go wrote`).not.toBeNull();
        expect(marker!.v, name).toBe(c.marker_v);
        expect(markerHasNames(marker), name).toBe(c.names);
        expect(conversionPending(marker), name).toBe(c.conv);

        for (const fmk of [
          await unlockWithPassword(marker!, c.password),
          await unlockWithRecoveryKey(marker!, c.recovery_key),
        ]) {
          expect(fmk, name).not.toBeNull();
          const nk = c.names ? await unlockNameKey(marker!, fmk!) : null;
          if (c.names) expect(nk, `${name}: the name key slot opens`).not.toBeNull();
          const got = await readBack(dir, fmk!, nk);
          expect(got.tree, name).toEqual(c.tree);
          // Only the conversion's file not reached yet is plaintext.
          expect(got.plain, name).toEqual(c.conv ? ['not-yet.txt'] : []);
        }
        expect(await unlockWithPassword(marker!, c.password + 'x'), name).toBeNull();
      }
    },
    SLOW,
  );

  it('go-v2 holds a STREAM file the Go writer made, beside one-shot ones', () => {
    const versions: number[] = [];
    const walk = (abs: string) => {
      for (const ent of fs.readdirSync(abs, { withFileTypes: true })) {
        const p = path.join(abs, ent.name);
        if (ent.isDirectory()) walk(p);
        else if (ent.name !== E2E_MARKER_NAME) versions.push(fs.readFileSync(p)[8]);
      }
    };
    walk(path.join(ROOT, 'go-v2'));
    expect(versions.filter((v) => v === 2)).toHaveLength(1);
    expect(versions.filter((v) => v === 1).length).toBeGreaterThanOrEqual(5);
  });

  it('go-v3-names stores no plaintext name', () => {
    const words = ['Bütçe', 'Sözleşme', 'Kira', 'fatura', 'boş', 'gizli', 'şşş'];
    const walk = (abs: string) => {
      for (const ent of fs.readdirSync(abs, { withFileTypes: true })) {
        if (ent.name === E2E_MARKER_NAME) continue;
        for (const w of words) expect(ent.name.includes(w), ent.name).toBe(false);
        if (ent.isDirectory()) walk(path.join(abs, ent.name));
      }
    };
    walk(path.join(ROOT, 'go-v3-names'));
  });
});
