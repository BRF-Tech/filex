// Encrypted names: the cipher, the spelling on the server, and the marker.
//
// Three independent witnesses, on purpose:
//   1. RFC 5297 appendix A.1 — the published AES-SIV vector.
//   2. name-vectors.json — produced by Python `cryptography`'s AESSIV
//      (backend/internal/e2edecrypt/testdata/gen_name_vectors.py), an
//      implementation that shares no code and no author with ours. The Go
//      decryptor reads the same file, so JS and Go are each pinned to a third
//      party rather than only to each other.
//   3. The frozen v0.47.0 module — the last release before names. A folder it
//      made must still open here, and a folder with encrypted names must NOT
//      open there.

import { beforeAll, describe, expect, it } from 'vitest';

import { importSivKey, sivDecrypt, sivEncrypt } from '../../../packages/core/src/lib/aessiv';
import {
  b64urlDecode,
  b64urlEncode,
  classifyStoredName,
  decryptStoredName,
  deriveDirId,
  dirIdOf,
  effectiveDirId,
  encryptName,
  importNameKey,
  namePlainProblem,
  sidecarNameFor,
  type E2eNameKey,
} from '../../../packages/core/src/lib/e2enames';
import {
  canRaiseToNames,
  createEncryptedFolder,
  decryptFile,
  enableNames,
  encryptFile,
  encryptionLevel,
  finishNames,
  importEscrowPrivateKey,
  markerHasNames,
  parseMarker,
  parseMarkerDetailed,
  unlockNameKey,
  unlockWithEscrowKey,
  unlockWithPassword,
  unlockWithRecoveryKey,
  upgradeMarkerV1,
  type E2eMarker,
} from '../../../packages/core/src/lib/e2ecrypto';

import * as legacy047 from '../fixtures/e2ecrypto-legacy-v0.47.0';
import * as legacy030 from '../fixtures/e2ecrypto-legacy-v0.30.1';
import vectors from '../../../backend/internal/e2edecrypt/testdata/name-vectors.json';
import escrowTestKey from '../fixtures/escrow-testkey.json';

const PW = 'correct horse battery';
const SLOW = 30_000;
const enc = new TextEncoder();
const dec = new TextDecoder();

function hex(s: string): Uint8Array {
  const out = new Uint8Array(s.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(s.slice(i * 2, i * 2 + 2), 16);
  return out;
}
function toHex(b: Uint8Array): string {
  return Array.from(b)
    .map((x) => x.toString(16).padStart(2, '0'))
    .join('');
}
type Vector = {
  name: string;
  parent_id: string;
  dir: boolean;
  encoded: string;
  stored: string;
  dir_id?: string;
  sidecar?: string;
};
const VECTORS = vectors.vectors as Vector[];
const ROOT_ID = b64urlDecode(vectors.root_id)!;
function idOf(b64: string): Uint8Array {
  const id = b64urlDecode(b64);
  if (!id || id.length !== 16) throw new Error(`not a folder id: ${b64}`);
  return id;
}
function wire(m: E2eMarker): E2eMarker {
  const parsed = parseMarker(JSON.stringify(m));
  if (!parsed) throw new Error('marker did not survive parseMarker');
  return parsed;
}

// ─────────────────────────────────────────────────────────────────────
// 1. The cipher
// ─────────────────────────────────────────────────────────────────────

describe('AES-SIV', () => {
  it('reproduces RFC 5297 appendix A.1 (AES-128-SIV with associated data)', async () => {
    const v = vectors.rfc5297_a1;
    const k = await importSivKey(hex(v.key_hex));
    const out = await sivEncrypt(k, hex(v.plaintext_hex), [hex(v.ad_hex)]);
    expect(toHex(out)).toBe(v.output_hex);
    expect(toHex(await sivDecrypt(k, out, [hex(v.ad_hex)]))).toBe(v.plaintext_hex);
  });

  it('refuses a ciphertext with one bit flipped, anywhere', async () => {
    const k = await importSivKey(hex(vectors.key_hex));
    const sealed = await sivEncrypt(k, enc.encode('Rapor.docx'));
    for (const at of [0, 15, 16, sealed.length - 1]) {
      const bad = sealed.slice();
      bad[at] ^= 1;
      await expect(sivDecrypt(k, bad)).rejects.toThrow(/authentication/);
    }
  });

  it('is deterministic within a folder — the property the server-side uniqueness check rests on', async () => {
    const k = await importSivKey(hex(vectors.key_hex));
    const a = await sivEncrypt(k, enc.encode('same'));
    const b = await sivEncrypt(k, enc.encode('same'));
    expect(toHex(a)).toBe(toHex(b));
  });
});

// ─────────────────────────────────────────────────────────────────────
// 2. Names on the server — pinned to an independent implementation
// ─────────────────────────────────────────────────────────────────────

describe('encrypted names, against Python cryptography', () => {
  let key: E2eNameKey;
  beforeAll(async () => {
    key = await importNameKey(hex(vectors.key_hex), vectors.long, ROOT_ID);
  });

  it('derives every folder id exactly as the reference does', async () => {
    for (const v of VECTORS.filter((x) => x.dir)) {
      const id = await deriveDirId(key, idOf(v.parent_id), v.name);
      expect(b64urlEncode(id), v.name).toBe(v.dir_id);
    }
  });

  it('spells every vector exactly as the reference does', async () => {
    for (const v of VECTORS) {
      const opts = v.dir ? { dirId: idOf(v.dir_id!) } : {};
      const got = await encryptName(key, v.name, idOf(v.parent_id), opts);
      expect(got.encoded, v.name).toBe(v.encoded);
      expect(got.stored, v.name).toBe(v.stored);
      expect(got.sidecar?.name, v.name).toBe(v.sidecar);
    }
  });

  it('reads every vector back in its own folder, long ones through their sidecar', async () => {
    for (const v of VECTORS) {
      const got = await decryptStoredName(key, v.stored, idOf(v.parent_id), async (n) =>
        n === v.sidecar ? v.encoded : null,
      );
      expect(got, v.name).toEqual({ name: v.name, state: 'enc' });
    }
  });

  it('gives the same name in two folders two different stored names', () => {
    // Sözleşmeler/2024/fatura.pdf and Sözleşmeler/2025/fatura.pdf.
    const both = VECTORS.filter((v) => v.name === 'fatura.pdf');
    expect(both).toHaveLength(2);
    expect(both[0].parent_id).not.toBe(both[1].parent_id);
    expect(both[0].stored).not.toBe(both[1].stored);
    const years = VECTORS.filter((v) => v.name === '2024' || v.name === '2025');
    expect(new Set(years.map((v) => v.dir_id)).size).toBe(2);
    // …and each is found under the folder whose id its parent's name carries.
    for (const f of both) {
      const parent = years.find((y) => y.dir_id === f.parent_id)!;
      expect(b64urlEncode(dirIdOf(parent.stored)!)).toBe(f.parent_id);
    }
  });

  it('does not read a name in a folder it was not sealed for', async () => {
    const [a, b] = VECTORS.filter((v) => v.name === 'fatura.pdf');
    // A file name moved over WebDAV without re-sealing: it may as well be a
    // plaintext name that looks like base64url, so it reads as one.
    expect(await decryptStoredName(key, a.stored, idOf(b.parent_id))).toEqual({
      name: a.stored,
      state: 'plain',
    });
    // A folder name (`S.D`) is ours by its spelling: unreadable, not plain.
    const y24 = VECTORS.find((v) => v.name === '2024')!;
    expect(await decryptStoredName(key, y24.stored, ROOT_ID)).toEqual({ name: null, state: 'unreadable' });
  });

  it('spells a folder S.D, and a long one H.fxl.D, the id intact', () => {
    for (const v of VECTORS.filter((x) => x.dir)) {
      const c = classifyStoredName(v.stored);
      expect(c.kind, v.name).toBe(v.sidecar ? 'longdir' : 'dir');
      expect(b64urlEncode(c.dirId!), v.name).toBe(v.dir_id);
      expect(v.stored.endsWith(`.${v.dir_id}`), v.name).toBe(true);
    }
    for (const v of VECTORS.filter((x) => !x.dir)) {
      expect(classifyStoredName(v.stored).kind, v.name).toBe(v.sidecar ? 'long' : 'file');
      expect(dirIdOf(v.stored), v.name).toBeNull();
    }
  });

  it('shortens exactly above the threshold, never at it — a folder counts its id', () => {
    const at = VECTORS.find((v) => v.name === 'a'.repeat(149))!;
    const over = VECTORS.find((v) => v.name === 'a'.repeat(150))!;
    expect(at.encoded.length).toBe(220);
    expect(at.stored).toBe(at.encoded);
    expect(over.encoded.length).toBe(222);
    expect(over.stored.endsWith('.fxl')).toBe(true);
    const dirAt = VECTORS.find((v) => v.name === 'K'.repeat(131))!;
    const dirOver = VECTORS.find((v) => v.name === 'K'.repeat(132))!;
    expect(dirAt.stored.length).toBeLessThanOrEqual(220);
    expect(dirAt.sidecar).toBeUndefined();
    expect(dirOver.encoded.length + 23).toBeGreaterThan(220);
    expect(dirOver.stored).toMatch(/^[A-Za-z0-9_-]{43}\.fxl\.[A-Za-z0-9_-]{22}$/);
  });

  it('never puts padding, a slash or a stray dot in a stored name', () => {
    for (const v of VECTORS) {
      expect(v.stored, v.name).toMatch(/^[A-Za-z0-9_-]+(\.fxl)?(\.[A-Za-z0-9_-]{22})?$/);
    }
  });

  it('normalises to NFC, so a macOS (NFD) name and a Windows (NFC) name are one name', async () => {
    const nfd = 'İzmir notları — 2026.txt'.normalize('NFD');
    expect(nfd).not.toBe('İzmir notları — 2026.txt');
    const v = VECTORS.find((x) => x.name === 'İzmir notları — 2026.txt')!;
    expect((await encryptName(key, nfd, ROOT_ID)).stored).toBe(v.stored);
  });

  it('gives a new folder a random id, and keeps an id it is handed', async () => {
    const a = await encryptName(key, 'Yeni klasör', ROOT_ID, { isDir: true });
    const b = await encryptName(key, 'Yeni klasör', ROOT_ID, { isDir: true });
    expect(a.encoded).toBe(b.encoded);
    expect(b64urlEncode(a.dirId!)).not.toBe(b64urlEncode(b.dirId!));
    const kept = await encryptName(key, 'Başka ad', ROOT_ID, { dirId: a.dirId });
    expect(b64urlEncode(dirIdOf(kept.stored)!)).toBe(b64urlEncode(a.dirId!));
  });

  it('knows the id of a folder before its name is encrypted — the same one it will carry', async () => {
    const soz = VECTORS.find((v) => v.name === 'Sözleşmeler')!;
    // Plaintext-named: derived. Encrypted: read from the name. Same id.
    expect(b64urlEncode(await effectiveDirId(key, ROOT_ID, 'Sözleşmeler'))).toBe(soz.dir_id);
    expect(b64urlEncode(await effectiveDirId(key, ROOT_ID, soz.stored))).toBe(soz.dir_id);
  });

  it('reads a name that was never encrypted as plaintext, not as damage', async () => {
    // Valid base64url, long enough to be a candidate, and not ours.
    const plain = 'ReadMe_2024-final-version';
    expect(classifyStoredName(plain).kind).toBe('file');
    expect(await decryptStoredName(key, plain, ROOT_ID)).toEqual({ name: plain, state: 'plain' });
    expect(await decryptStoredName(key, 'notes.txt', ROOT_ID)).toEqual({ name: 'notes.txt', state: 'plain' });
    expect(await decryptStoredName(key, 'Sözleşmeler', ROOT_ID)).toEqual({ name: 'Sözleşmeler', state: 'plain' });
  });

  it('marks a long name whose sidecar is missing or swapped as unreadable', async () => {
    const long = VECTORS.find((v) => v.name === 'ş'.repeat(120))!;
    const other = VECTORS.find((v) => v.name === 'a'.repeat(150))!;
    expect(await decryptStoredName(key, long.stored, ROOT_ID, async () => null)).toEqual({
      name: null,
      state: 'unreadable',
    });
    // The other long name's sidecar is valid ciphertext — for a different item.
    expect(await decryptStoredName(key, long.stored, ROOT_ID, async () => other.encoded)).toEqual({
      name: null,
      state: 'unreadable',
    });
  });

  it('hides sidecars and pairs them with their item, file or folder', () => {
    for (const v of VECTORS.filter((x) => x.sidecar)) {
      expect(classifyStoredName(v.sidecar!).kind, v.name).toBe('sidecar');
      expect(sidecarNameFor(v.stored), v.name).toBe(v.sidecar);
    }
  });

  it('does not open under another folder\'s key', async () => {
    const other = await importNameKey(new Uint8Array(64).fill(7), 220, ROOT_ID);
    const v = VECTORS[1];
    expect(await decryptStoredName(other, v.stored, ROOT_ID)).toEqual({ name: v.stored, state: 'plain' });
  });

  it('refuses a name a disk could not hold', () => {
    expect(namePlainProblem('')).toBe('empty');
    expect(namePlainProblem('..')).toBe('dot');
    expect(namePlainProblem('a/b')).toBe('slash');
    expect(namePlainProblem('a\\b')).toBe('slash');
    expect(namePlainProblem('a' + String.fromCharCode(1))).toBe('control');
    expect(namePlainProblem('ş'.repeat(128))).toBe('too_long');
    expect(namePlainProblem('ş'.repeat(127))).toBeNull();
  });

  it('decodes base64url strictly', () => {
    const b = new Uint8Array([0xfb, 0xff, 0x01]);
    expect(b64urlEncode(b)).toBe('-_8B');
    expect(b64urlDecode('-_8B')).toEqual(b);
    expect(b64urlDecode('-_8B=')).toBeNull();
    expect(b64urlDecode('+/8B')).toBeNull();
    // Non-canonical: the unused low bits of the last character are set.
    expect(b64urlDecode('AB')).toBeNull();
    expect(b64urlDecode('AA')).toEqual(new Uint8Array([0]));
  });
});

// ─────────────────────────────────────────────────────────────────────
// 3. The marker: v3, required features, and every way in
// ─────────────────────────────────────────────────────────────────────

describe('a new folder with encrypted names', () => {
  let marker: E2eMarker;
  let recoveryKey: string;
  let stored: string;

  beforeAll(async () => {
    const made = await createEncryptedFolder(PW, { encryptNames: true });
    marker = wire(made.marker);
    recoveryKey = made.recoveryKey;
    stored = (await encryptName(made.names!, 'Bütçe 2027.xlsx', made.names!.rootId)).stored;
  }, SLOW);

  it('is a v3 marker that requires the names feature', () => {
    expect(marker.v).toBe(3);
    expect(marker.req).toEqual(['names']);
    expect(markerHasNames(marker)).toBe(true);
    expect(marker.names?.pending).toBeUndefined();
    expect(encryptionLevel(marker)).toBe('names');
  });

  it('carries the root folder id — random, 16 bytes, one per folder', async () => {
    expect(b64urlDecode(marker.names!.root_id)?.length).toBe(16);
    const other = await createEncryptedFolder(PW, { encryptNames: true });
    expect(other.marker.names!.root_id).not.toBe(marker.names!.root_id);
  }, SLOW);

  it('reaches the same name key by password and by recovery key', async () => {
    const byPw = await unlockNameKey(marker, (await unlockWithPassword(marker, PW))!);
    const byRk = await unlockNameKey(marker, (await unlockWithRecoveryKey(marker, recoveryKey))!);
    expect((await decryptStoredName(byPw!, stored, byPw!.rootId)).name).toBe('Bütçe 2027.xlsx');
    expect((await decryptStoredName(byRk!, stored, byRk!.rootId)).name).toBe('Bütçe 2027.xlsx');
  }, SLOW);

  it('reaches it through escrow too, when the folder was sealed to it', async () => {
    const made = await createEncryptedFolder(PW, {
      encryptNames: true,
      escrowPublicKey: escrowTestKey.public_spki_b64,
    });
    const m = wire(made.marker);
    const name = (await encryptName(made.names!, 'escrowed.txt', made.names!.rootId)).stored;
    const priv = await importEscrowPrivateKey(escrowTestKey.private_pkcs8_b64);
    const fmk = await unlockWithEscrowKey(m, priv);
    const nk = await unlockNameKey(m, fmk!);
    expect((await decryptStoredName(nk!, name, nk!.rootId)).name).toBe('escrowed.txt');
  }, SLOW);

  it('is refused by the v0.47.0 module — an old filex will not open it', () => {
    expect(legacy047.parseMarker(JSON.stringify(marker))).toBeNull();
  });

  it('is refused by the v0.30.1 module too', () => {
    expect(legacy030.parseMarker(JSON.stringify(marker))).toBeNull();
  });

  it('still encrypts content the way every release reads it', async () => {
    const fmk = (await unlockWithPassword(marker, PW))!;
    const ct = await encryptFile(fmk, enc.encode('içerik').buffer as ArrayBuffer);
    expect(dec.decode(await decryptFile(fmk, ct))).toBe('içerik');
  }, SLOW);
});

describe('required features', () => {
  const base = {
    v: 3,
    salt: 'AAAAAAAAAAAAAAAAAAAAAA==',
    iter: 600000,
    verify: 'AAAA',
    fmk: 'wrapped',
    fmk_pw: 'AAAA',
  };
  const names = { alg: 'AES-SIV-512', enc: 'b64url', long: 220, key: 'AAAA', root_id: 'AAAAAAAAAAAAAAAAAAAAAA' };

  it('names a feature this build does not know, and refuses to open', () => {
    const text = JSON.stringify({ ...base, req: ['names', 'vault'], names });
    expect(parseMarkerDetailed(text)?.unsupported).toEqual(['vault']);
    expect(parseMarker(text)).toBeNull();
  });

  it('rejects a v3 marker without a req list, or with a broken names slot', () => {
    expect(parseMarker(JSON.stringify({ ...base, names }))).toBeNull();
    expect(parseMarker(JSON.stringify({ ...base, req: ['names'] }))).toBeNull();
    expect(parseMarker(JSON.stringify({ ...base, req: ['names'], names: { ...names, alg: 'AES-GCM' } }))).toBeNull();
    expect(parseMarker(JSON.stringify({ ...base, req: ['names'], names: { ...names, long: 9000 } }))).toBeNull();
    // Without its root id no name in the folder could be read.
    const { root_id: _gone, ...noRoot } = names;
    expect(parseMarker(JSON.stringify({ ...base, req: ['names'], names: noRoot }))).toBeNull();
    expect(parseMarker(JSON.stringify({ ...base, req: ['names'], names: { ...names, root_id: 'AAAA' } }))).toBeNull();
  });

  it('rejects v3 fields on a v2 marker — no filex writes that', () => {
    expect(parseMarker(JSON.stringify({ ...base, v: 2, req: ['names'], names }))).toBeNull();
  });

  it('accepts a well-formed v3 marker', () => {
    expect(parseMarker(JSON.stringify({ ...base, req: ['names'], names }))?.v).toBe(3);
  });
});

describe('raising a folder from level 1 (contents) to level 2 (contents + names)', () => {
  it('works on a folder v0.47.0 created, and keeps its files and keys working', async () => {
    // Made by the frozen v0.47.0 module, file and all.
    const old = await legacy047.createEncryptedFolder(PW);
    const oldMarker = wire(old.marker as E2eMarker);
    const file = await legacy047.encryptFile(old.fmk, enc.encode('eski dosya').buffer as ArrayBuffer);

    // It opens in this build exactly as it did.
    const fmk = (await unlockWithPassword(oldMarker, PW))!;
    expect(dec.decode(await decryptFile(fmk, file))).toBe('eski dosya');
    expect(encryptionLevel(oldMarker)).toBe('content');
    expect(canRaiseToNames(oldMarker)).toBe(true);

    // The switch writes the marker first, with pending set.
    const started = await enableNames(oldMarker, fmk);
    const m1 = wire(started.marker);
    expect(m1.v).toBe(3);
    expect(m1.names?.pending).toBe(true);
    expect(encryptionLevel(m1)).toBe('pending');
    expect(canRaiseToNames(m1)).toBe(false);

    // Password, recovery key and the old file all keep working after it.
    const fmk2 = (await unlockWithPassword(m1, PW))!;
    expect(dec.decode(await decryptFile(fmk2, file))).toBe('eski dosya');
    const byRk = (await unlockWithRecoveryKey(m1, old.recoveryKey))!;
    expect(dec.decode(await decryptFile(byRk, file))).toBe('eski dosya');
    const nk = await unlockNameKey(m1, fmk2);
    const stored = (await encryptName(started.names, 'x.txt', started.names.rootId)).stored;
    expect((await decryptStoredName(nk!, stored, nk!.rootId)).name).toBe('x.txt');

    // Finishing drops pending and nothing else.
    const done = wire(finishNames(m1));
    expect(done.names?.pending).toBeUndefined();
    expect(done.names?.key).toBe(m1.names?.key);
    expect(encryptionLevel(done)).toBe('names');
    expect(done.names?.root_id).toBe(m1.names?.root_id);

    // And from here on the old module refuses it.
    expect(legacy047.parseMarker(JSON.stringify(done))).toBeNull();
  }, SLOW);

  it('works on a v0.30.1 folder once it has had its recovery upgrade (fmk: kek)', async () => {
    const made = await legacy030.createMarker(PW);
    const v1 = wire(made.marker as E2eMarker);
    const file = await legacy030.encryptFile(made.kek, enc.encode('v1').buffer as ArrayBuffer);
    expect(encryptionLevel(v1)).toBe('content');
    expect(canRaiseToNames(v1)).toBe(false); // its first step is the recovery upgrade
    const up = await upgradeMarkerV1(v1, PW);
    const v2 = wire(up.marker);
    const started = await enableNames(v2, up.fmk);
    const m = wire(started.marker);
    const fmk = (await unlockWithPassword(m, PW))!;
    expect(dec.decode(await decryptFile(fmk, file))).toBe('v1');
    const nk = await unlockNameKey(m, fmk);
    const stored = (await encryptName(started.names, 'ad.txt', started.names.rootId)).stored;
    expect((await decryptStoredName(nk!, stored, nk!.rootId)).name).toBe('ad.txt');
  }, SLOW);

  it('a new folder is level 1 unless level 2 is chosen', async () => {
    const plain = await createEncryptedFolder(PW);
    expect(plain.marker.v).toBe(2);
    expect(encryptionLevel(plain.marker)).toBe('content');
    expect(canRaiseToNames(plain.marker)).toBe(true);
  }, SLOW);

  it('refuses to run on a v1 or an already-switched marker', async () => {
    const made = await createEncryptedFolder(PW, { encryptNames: true });
    await expect(enableNames(made.marker, made.fmk)).rejects.toThrow(/v2/);
    const v1 = (await legacy030.createMarker(PW)).marker as E2eMarker;
    await expect(enableNames(v1, made.fmk)).rejects.toThrow(/v2/);
  }, SLOW);

  it('does not hand out a name key under the wrong folder key', async () => {
    const a = await createEncryptedFolder(PW, { encryptNames: true });
    const b = await createEncryptedFolder(PW, { encryptNames: true });
    expect(await unlockNameKey(a.marker, b.fmk)).toBeNull();
  }, SLOW);
});

describe('what a key holder cannot make the explorer do', () => {
  it('a decrypted name that is a path, not a name, is unreadable — never a download filename', async () => {
    const key = await importNameKey(new Uint8Array(64).fill(3), 220, ROOT_ID);
    const { sivEncrypt: seal } = await import('../../../packages/core/src/lib/aessiv');
    for (const evil of ['../escape.txt', 'a/b', 'x\u0001y', '..']) {
      const stored = b64urlEncode(await seal(key.siv, enc.encode(evil), [ROOT_ID]));
      expect((await decryptStoredName(key, stored, ROOT_ID)).state, evil).toBe('unreadable');
      // …and the same as a folder name.
      const asDir = `${stored}.${b64urlEncode(new Uint8Array(16).fill(9))}`;
      expect((await decryptStoredName(key, asDir, ROOT_ID)).state, evil).toBe('unreadable');
    }
  });

  it('a marker cannot ask for a key derivation that would hang the tab', () => {
    const base = { v: 2, salt: 'AAAAAAAAAAAAAAAAAAAAAA==', verify: 'AAAA', fmk: 'wrapped', fmk_pw: 'AAAA' };
    expect(parseMarker(JSON.stringify({ ...base, iter: 1e12 }))).toBeNull();
    expect(parseMarker(JSON.stringify({ ...base, iter: 600000.5 }))).toBeNull();
    expect(parseMarker(JSON.stringify({ ...base, iter: 600000 }))).not.toBeNull();
  });
});
