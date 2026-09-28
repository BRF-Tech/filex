// A single encrypted file, `.fxe` (docs/E2E-ENCRYPTION.md → "Single encrypted
// files").
//
// First: the file an INDEPENDENT implementation wrote (Python
// `cryptography`, testdata/gen_stream_vectors.py) opens here — header,
// slots, name and body — by password and by recovery key, and a field this
// build does not know survives a rewrite. Then: what this build writes opens
// again, a password change touches the header only, the escrow slot is the
// installation's key (the Go CLI's keypair, as in e2ecrypto.test.ts), and
// every kind of damage is refused.

import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

import { describe, expect, it } from 'vitest';

import {
  E2eDecryptError,
  E2E_DEFAULT_ITERATIONS,
  escrowKeyId,
  importEscrowPrivateKey,
} from '../../../packages/core/src/lib/e2ecrypto';
import {
  FXE_EXTENSION,
  FXE_FIXED_LEN,
  FxeFormatError,
  changeFxePassword,
  createFxe,
  decryptFxeBody,
  encodeFxePrefix,
  fxeFileSize,
  fxeStoredName,
  hiddenFxeName,
  isFxeName,
  isHiddenFxeName,
  parseFxePrefix,
  readFxe,
  replaceFxeHeader,
  unlockFxe,
  type FxeHeader,
} from '../../../packages/core/src/lib/e2efile';
import { bytesStream, collectBytes } from '../../../packages/core/src/lib/e2estream';

import escrowTestKey from '../fixtures/escrow-testkey.json';

const VECTORS = JSON.parse(
  fs.readFileSync(path.resolve(__dirname, '../../../backend/internal/e2edecrypt/testdata/stream-vectors.json'), 'utf8'),
) as { fxe: { password: string; recovery_key: string; name: string; len: number; seed: number; file_hex: string } };

const sha = (b: Uint8Array) => createHash('sha256').update(b).digest('hex');
const PW = 'a file password';
/** Asked for, and refused: a writer never uses fewer than 600 000 (checked below). */
const FAST = { iterations: 1000 } as const;

function pattern(n: number, seed: number): Uint8Array {
  const out = new Uint8Array(n);
  for (let i = 0; i < n; i++) out[i] = (i * 31 + seed) & 0xff;
  return out;
}

async function make(plain: Uint8Array, name = 'Rapor 2027.pdf', opts: Parameters<typeof createFxe>[4] = {}) {
  const created = await createFxe(name, plain.length, bytesStream(plain, 777), PW, { chunkLog2: 10, ...FAST, ...opts });
  const file = await collectBytes(created.stream);
  return { created, file };
}

async function openWith(file: Uint8Array, cred: Parameters<typeof unlockFxe>[1]) {
  const { parsed, body } = await readFxe(bytesStream(file, 501));
  const u = await unlockFxe(parsed.header, cred);
  if ('error' in u) {
    await body.cancel();
    return { error: u.error };
  }
  const plain = await collectBytes(decryptFxeBody(u.key, body));
  return { name: u.key.name, plain, header: parsed.header };
}

describe('.fxe — the file an independent implementation wrote', () => {
  const v = VECTORS.fxe;
  const file = new Uint8Array(Buffer.from(v.file_hex, 'hex'));

  it('opens by password: name and content', async () => {
    const got = await openWith(file, { password: v.password });
    expect('error' in got).toBe(false);
    if ('error' in got) return;
    expect(got.name).toBe(v.name);
    expect(sha(got.plain)).toBe(sha(pattern(v.len, v.seed)));
    expect(fxeFileSize(got.header, parseFxePrefix(file).prefixLength)).toBe(file.length);
  });

  it('opens by recovery key; a wrong password and a wrong key open nothing', async () => {
    const got = await openWith(file, { recoveryKey: v.recovery_key });
    expect('error' in got ? got.error : got.name).toBe(v.name);
    expect(await openWith(file, { password: v.password + 'x' })).toEqual({ error: 'wrong' });
    expect(await openWith(file, { recoveryKey: 'AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA-AAAA' })).toEqual({ error: 'wrong' });
  });

  it('a password change keeps a field this build does not know, and the body byte for byte', async () => {
    const parsed = parseFxePrefix(file);
    expect(parsed.header.x_future).toEqual({ kept: true });
    const next = await changeFxePassword(parsed.header, { password: v.password }, 'the new vector password');
    expect(next.x_future).toEqual({ kept: true });
    expect(next.dek).toBe(parsed.header.dek);
    expect(next.rk).toEqual(parsed.header.rk);
    expect(next.iter).toBe(E2E_DEFAULT_ITERATIONS);
    const body = bytesStream(file.slice(parsed.prefixLength));
    const out = replaceFxeHeader(next, body);
    const rewritten = await collectBytes(out.stream);
    expect(rewritten.length).toBe(out.size);
    const newPrefix = parseFxePrefix(rewritten).prefixLength;
    expect(Buffer.compare(Buffer.from(rewritten.slice(newPrefix)), Buffer.from(file.slice(parsed.prefixLength)))).toBe(0);
    expect(await openWith(rewritten, { password: v.password })).toEqual({ error: 'wrong' });
    const again = await openWith(rewritten, { password: 'the new vector password' });
    expect('error' in again ? again.error : sha(again.plain)).toBe(sha(pattern(v.len, v.seed)));
    const byRk = await openWith(rewritten, { recoveryKey: v.recovery_key });
    expect('error' in byRk ? byRk.error : byRk.name).toBe(v.name);
  }, 30_000);
});

describe('.fxe — what this build writes', () => {
  it('round trip, many chunks, the size it promised, and the name in NFC', async () => {
    const plain = pattern(5000, 3);
    // "İ" typed on a Mac arrives decomposed; it is stored composed.
    const nfd = 'İzmir notları.txt'.normalize('NFD');
    const { created, file } = await make(plain, nfd);
    expect(file.length).toBe(created.size);
    expect(Buffer.from(file.slice(0, 8)).toString()).toBe('filexfxe');
    expect(file[8]).toBe(1);
    const got = await openWith(file, { password: PW });
    expect('error' in got).toBe(false);
    if ('error' in got) return;
    expect(got.name).toBe('İzmir notları.txt'.normalize('NFC'));
    expect(sha(got.plain)).toBe(sha(plain));
    // the recovery key shown at creation opens it
    const byRk = await openWith(file, { recoveryKey: created.recoveryKey });
    expect('error' in byRk ? byRk.error : sha(byRk.plain)).toBe(sha(plain));
    // the header says nothing readable: no name, no content
    const headerText = new TextDecoder().decode(file.slice(FXE_FIXED_LEN, parseFxePrefix(file).prefixLength));
    expect(headerText).not.toContain('zmir');
    // Writers never go below 600 000, whatever a caller asks for.
    expect(created.header.iter).toBe(600_000);
  });

  it('an empty file, and one exactly a chunk long', async () => {
    for (const n of [0, 1024, 2048]) {
      const plain = pattern(n, 4);
      const { file } = await make(plain, 'x.bin');
      const got = await openWith(file, { password: PW });
      expect('error' in got ? got.error : sha(got.plain), `len ${n}`).toBe(sha(plain));
    }
  });

  it('writers use the production chunk size and at least 600 000 iterations by default', async () => {
    const created = await createFxe('a.txt', 3, bytesStream(new Uint8Array([1, 2, 3])), PW);
    expect(created.header.chunk).toBe(20);
    expect(created.header.iter).toBeGreaterThanOrEqual(600_000);
    await created.stream.cancel();
  }, 30_000);

  it('the escrow slot is sealed to the installation key and opens with its private half', async () => {
    const plain = pattern(1500, 5);
    const { created, file } = await make(plain, 'e.txt', { escrowPublicKey: escrowTestKey.public_spki_b64 });
    expect(created.header.esc?.kid).toBe(await escrowKeyId(escrowTestKey.public_spki_b64));
    expect(created.header.esc?.alg).toBe('RSA-OAEP-256');
    const priv = await importEscrowPrivateKey(escrowTestKey.private_pkcs8_b64);
    const got = await openWith(file, { escrowKey: priv });
    expect('error' in got ? got.error : sha(got.plain)).toBe(sha(plain));
    // no escrow slot → the escrow key opens nothing
    const { file: noEsc } = await make(plain, 'n.txt');
    expect(await openWith(noEsc, { escrowKey: priv })).toEqual({ error: 'wrong' });
  });

  it('refuses a name a disk could not hold, and a short password', async () => {
    await expect(createFxe('a/b.txt', 1, bytesStream(new Uint8Array(1)), PW, FAST)).rejects.toThrow(/invalid name/);
    await expect(createFxe('ok.txt', 1, bytesStream(new Uint8Array(1)), 'short', FAST)).rejects.toThrow(/at least 8/);
  });

  it('a source that disagrees with the size it announced never becomes a file', async () => {
    const created = await createFxe('a.txt', 10, bytesStream(pattern(9, 1)), PW, { ...FAST, chunkLog2: 10 });
    await expect(collectBytes(created.stream)).rejects.toThrow(/shorter/);
  });
});

describe('.fxe — changing the password', () => {
  it('old fails, new works, the recovery key still works, the body is untouched', async () => {
    const plain = pattern(3333, 6);
    const { created, file } = await make(plain);
    const parsed = parseFxePrefix(file);
    await expect(changeFxePassword(parsed.header, { password: 'not it at all' }, 'a newer password')).rejects.toBeInstanceOf(
      E2eDecryptError,
    );
    const next = await changeFxePassword(parsed.header, { password: PW }, 'a newer password');
    const out = await collectBytes(replaceFxeHeader(next, bytesStream(file.slice(parsed.prefixLength))).stream);
    expect(await openWith(out, { password: PW })).toEqual({ error: 'wrong' });
    const got = await openWith(out, { password: 'a newer password' });
    expect('error' in got ? got.error : sha(got.plain)).toBe(sha(plain));
    const rk = await openWith(out, { recoveryKey: created.recoveryKey });
    expect('error' in rk ? rk.error : sha(rk.plain)).toBe(sha(plain));
    // …and the recovery key is proof enough to set one
    const viaRk = await changeFxePassword(next, { recoveryKey: created.recoveryKey }, 'set with the recovery key');
    const out2 = await collectBytes(replaceFxeHeader(viaRk, bytesStream(file.slice(parsed.prefixLength))).stream);
    const got2 = await openWith(out2, { password: 'set with the recovery key' });
    expect('error' in got2 ? got2.error : got2.name).toBe('Rapor 2027.pdf');
  }, 30_000);
});

describe('.fxe — damage and newer files are refused', () => {
  it('not a .fxe, a newer version, an absurd header length', async () => {
    const { file } = await make(pattern(10, 7));
    await expect(readFxe(bytesStream(new TextEncoder().encode('filexe2e and the rest of it')))).rejects.toBeInstanceOf(FxeFormatError);
    const v2 = file.slice();
    v2[8] = 2;
    await expect(readFxe(bytesStream(v2))).rejects.toThrow(/version 2/);
    const huge = file.slice();
    new DataView(huge.buffer).setUint32(9, 10_000_000, false);
    await expect(readFxe(bytesStream(huge))).rejects.toThrow(/out of range/);
    await expect(readFxe(bytesStream(file.slice(0, 40)))).rejects.toThrow(/truncated/);
  });

  it('an unknown required feature is named, not opened', () => {
    const h = {
      salt: 'AAAAAAAAAAAAAAAAAAAAAA==',
      iter: 1000,
      verify: 'x',
      fmk: 'wrapped',
      fmk_pw: 'x',
      dek: 'x',
      name: 'x',
      chunk: 20,
      nonce: 'AAAAAAAAAA==',
      size: 0,
      req: ['vault'],
    } as FxeHeader;
    expect(parseFxePrefix(encodeFxePrefix(h)).unsupported).toEqual(['vault']);
    expect(() => parseFxePrefix(encodeFxePrefix({ ...h, chunk: 30 }))).toThrow(/chunk/);
    expect(() => parseFxePrefix(encodeFxePrefix({ ...h, nonce: 'AAAA' }))).toThrow(/nonce/);
    expect(() => parseFxePrefix(encodeFxePrefix({ ...h, fmk: 'kek' as 'wrapped' }))).toThrow(/fmk/);
  });

  it('a truncated, extended or bit-flipped body; a header that lies about the size', async () => {
    const plain = pattern(4000, 8);
    const { file } = await make(plain);
    const cut = file.slice(0, file.length - 1040);
    expect((await openWith(cut, { password: PW }).catch((e) => e)) instanceof E2eDecryptError).toBe(true);
    const longer = new Uint8Array(file.length + 16);
    longer.set(file, 0);
    expect((await openWith(longer, { password: PW }).catch((e) => e)) instanceof E2eDecryptError).toBe(true);
    const flipped = file.slice();
    flipped[flipped.length - 100] ^= 1;
    expect((await openWith(flipped, { password: PW }).catch((e) => e)) instanceof E2eDecryptError).toBe(true);
    const parsed = parseFxePrefix(file);
    for (const size of [plain.length - 1, plain.length + 1]) {
      const liar = replaceFxeHeader({ ...parsed.header, size }, bytesStream(file.slice(parsed.prefixLength)));
      const bytes = await collectBytes(liar.stream);
      expect((await openWith(bytes, { password: PW }).catch((e) => e)) instanceof E2eDecryptError, `size ${size}`).toBe(true);
    }
  });

  it('a DEK or a name sealed under another file’s key is damage, not a wrong password', async () => {
    const a = parseFxePrefix((await make(pattern(10, 1))).file).header;
    const b = parseFxePrefix((await make(pattern(10, 2))).file).header;
    expect(await unlockFxe({ ...a, dek: b.dek }, { password: PW })).toEqual({ error: 'damaged' });
    expect(await unlockFxe({ ...a, name: b.name }, { password: PW })).toEqual({ error: 'damaged' });
  });
});

describe('.fxe — stored names', () => {
  it('<name>.fxe by default, encrypted-<8 hex>.fxe when the name is hidden', () => {
    expect(fxeStoredName('Rapor.pdf', false)).toBe('Rapor.pdf.fxe');
    const hidden = fxeStoredName('Rapor.pdf', true);
    expect(hidden).toMatch(/^encrypted-[0-9a-f]{8}\.fxe$/);
    expect(isHiddenFxeName(hidden)).toBe(true);
    expect(isHiddenFxeName('Rapor.pdf.fxe')).toBe(false);
    expect(hiddenFxeName()).not.toBe(hiddenFxeName());
    expect(isFxeName('a.FXE')).toBe(true);
    expect(isFxeName(FXE_EXTENSION)).toBe(false);
    expect(isFxeName('a.fxe.txt')).toBe(false);
  });
});
