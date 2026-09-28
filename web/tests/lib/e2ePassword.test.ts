// Changing an encrypted folder's password.
//
// Two kinds of folder, two costs (docs/E2E-ENCRYPTION.md → "Changing the
// password"):
//   - `fmk: 'wrapped'` (every folder since v0.31): the folder key is random and
//     the password only wraps it, so a new password re-wraps ONE 32-byte key
//     and touches no file. Recovery key, escrow and encrypted names keep
//     working because they reach the same folder key.
//   - v1 and `fmk: 'kek'` (folders from before v0.31): the folder key IS the
//     password-derived key, so a new password needs a new folder key and every
//     file's DEK re-wrapped under it (the content stays exactly as it is). That
//     is a "re-key": resumable, because the old folder key rides in the marker
//     sealed under the new one until the last file is done.
//
// Old releases are measured with their FROZEN modules, not re-creations.

import { describe, expect, it } from 'vitest';

import {
  changePassword,
  createEncryptedFolder,
  decryptFile,
  decryptFileAny,
  encryptFile,
  escrowKeyId,
  finishRekey,
  importEscrowPrivateKey,
  parseMarker,
  passwordChangeNeedsRekey,
  rekeyPending,
  rewrapFileKey,
  startRekey,
  unlockNameKey,
  unlockPrevious,
  unlockWithEscrowKey,
  unlockWithPassword,
  unlockWithPasswordDetailed,
  unlockWithRecoveryKey,
  upgradeMarkerV1,
  E2eDecryptError,
  type E2eMarker,
} from '../../../packages/core/src/lib/e2ecrypto';
import { decryptStoredName, encryptName } from '../../../packages/core/src/lib/e2enames';

import * as legacy030 from '../fixtures/e2ecrypto-legacy-v0.30.1';
import * as legacy047 from '../fixtures/e2ecrypto-legacy-v0.47.0';
import escrowTestKey from '../fixtures/escrow-testkey.json';

const OLD = 'correct horse battery';
const NEW = 'a brand new passphrase';
const SLOW = 60_000;
const enc = new TextEncoder();
const dec = new TextDecoder();

function wire(m: E2eMarker): E2eMarker {
  const parsed = parseMarker(JSON.stringify(m));
  if (!parsed) throw new Error('marker did not survive parseMarker');
  return parsed;
}

const bytes = (s: string) => enc.encode(s).buffer as ArrayBuffer;

describe('changing the password of a folder with a wrapped key (v0.31+)', () => {
  it('re-wraps the folder key: the new password opens every file, the old one nothing', async () => {
    const made = await createEncryptedFolder(OLD, { escrowPublicKey: escrowTestKey.public_spki_b64 });
    const m1 = wire(made.marker);
    const file = await encryptFile(made.fmk, bytes('dosya içeriği'));

    expect(passwordChangeNeedsRekey(m1)).toBe(false);
    const m2 = wire(await changePassword(m1, { password: OLD }, NEW));

    expect(await unlockWithPassword(m2, OLD)).toBeNull();
    const fmk = (await unlockWithPassword(m2, NEW))!;
    expect(dec.decode(await decryptFile(fmk, file))).toBe('dosya içeriği');

    // Nothing else moved: a new salt and verify, the same recovery and escrow slots.
    expect(m2.salt).not.toBe(m1.salt);
    expect(m2.verify).not.toBe(m1.verify);
    expect(m2.fmk_pw).not.toBe(m1.fmk_pw);
    expect(m2.rk).toEqual(m1.rk);
    expect(m2.esc).toEqual(m1.esc);
    expect(m2.v).toBe(m1.v);

    // …and those slots still open the same files.
    const byRk = (await unlockWithRecoveryKey(m2, made.recoveryKey))!;
    expect(dec.decode(await decryptFile(byRk, file))).toBe('dosya içeriği');
    const priv = await importEscrowPrivateKey(escrowTestKey.private_pkcs8_b64);
    const byEsc = (await unlockWithEscrowKey(m2, priv))!;
    expect(dec.decode(await decryptFile(byEsc, file))).toBe('dosya içeriği');
  }, SLOW);

  it('accepts the recovery key instead of the old password (a reset)', async () => {
    const made = await createEncryptedFolder(OLD);
    const file = await encryptFile(made.fmk, bytes('x'));
    const m2 = wire(await changePassword(wire(made.marker), { recoveryKey: made.recoveryKey }, NEW));
    expect(await unlockWithPassword(m2, OLD)).toBeNull();
    expect(dec.decode(await decryptFile((await unlockWithPassword(m2, NEW))!, file))).toBe('x');
  }, SLOW);

  it('refuses a wrong password or a wrong recovery key, and a too-short new password', async () => {
    const made = await createEncryptedFolder(OLD);
    const m = wire(made.marker);
    await expect(changePassword(m, { password: 'not it at all' }, NEW)).rejects.toBeInstanceOf(E2eDecryptError);
    await expect(
      changePassword(m, { recoveryKey: '0000-0000-0000-0000-0000-0000-0000-0000' }, NEW),
    ).rejects.toBeInstanceOf(E2eDecryptError);
    await expect(changePassword(m, { password: OLD }, 'short')).rejects.toThrow(/at least/);
  }, SLOW);

  it('keeps encrypted names readable — the name key hangs off the folder key, not the password', async () => {
    const made = await createEncryptedFolder(OLD, { encryptNames: true });
    const stored = (await encryptName(made.names!, 'Sözleşme.pdf', made.names!.rootId)).stored;
    const m2 = wire(await changePassword(wire(made.marker), { password: OLD }, NEW));
    expect(m2.names).toEqual(made.marker.names);
    const nk = await unlockNameKey(m2, (await unlockWithPassword(m2, NEW))!);
    expect((await decryptStoredName(nk!, stored, nk!.rootId)).name).toBe('Sözleşme.pdf');
  }, SLOW);

  it('still opens in the v0.47.0 module afterwards — a v2 folder stays a v2 folder', async () => {
    const old = await legacy047.createEncryptedFolder(OLD);
    const file = await legacy047.encryptFile(old.fmk, bytes('eski'));
    const m2 = await changePassword(wire(old.marker as E2eMarker), { password: OLD }, NEW);
    const back = legacy047.parseMarker(JSON.stringify(m2))!;
    expect(back).not.toBeNull();
    const fmk = (await legacy047.unlockWithPassword(back, NEW))!;
    expect(dec.decode(await legacy047.decryptFile(fmk, file))).toBe('eski');
    expect(await legacy047.unlockWithPassword(back, OLD)).toBeNull();
  }, SLOW);
});

describe('a damaged key file is not a wrong password', () => {
  it('tells the two apart', async () => {
    const made = await createEncryptedFolder(OLD);
    const m = wire(made.marker);
    expect(await unlockWithPasswordDetailed(m, 'nope nope nope')).toEqual({ error: 'wrong' });
    const raw = atob(m.fmk_pw!);
    const broken = { ...m, fmk_pw: btoa(raw.slice(0, -1) + String.fromCharCode(raw.charCodeAt(raw.length - 1) ^ 1)) };
    expect(await unlockWithPasswordDetailed(broken, OLD)).toEqual({ error: 'damaged' });
    expect('fmk' in (await unlockWithPasswordDetailed(m, OLD))).toBe(true);
  }, SLOW);
});

describe('re-keying a folder from before v0.31 (the key IS the password)', () => {
  it('says so, and changePassword refuses to pretend otherwise', async () => {
    const v1 = wire((await legacy030.createMarker(OLD)).marker as E2eMarker);
    expect(passwordChangeNeedsRekey(v1)).toBe(true);
    await expect(changePassword(v1, { password: OLD }, NEW)).rejects.toThrow(/re-wrap/);
    const up = await upgradeMarkerV1(v1, OLD);
    expect(passwordChangeNeedsRekey(wire(up.marker))).toBe(true);
  }, SLOW);

  it('re-wraps every file key, resumably, and the old password opens nothing when it is done', async () => {
    // A folder and two files written by the v0.30.1 module itself.
    const made = await legacy030.createMarker(OLD);
    const v1 = wire(made.marker as E2eMarker);
    const a = await legacy030.encryptFile(made.kek, bytes('birinci'));
    const b = await legacy030.encryptFile(made.kek, bytes('ikinci'));

    const start = await startRekey(v1, { password: OLD }, NEW);
    const m = wire(start.marker);
    expect(m.v).toBe(3);
    expect(m.req).toEqual(['rekey']);
    expect(m.fmk).toBe('wrapped');
    expect(rekeyPending(m)).toBe(true);
    expect(start.recoveryKeyIsNew).toBe(true);
    // An older filex will not open a folder that is half re-keyed.
    expect(legacy047.parseMarker(JSON.stringify(m))).toBeNull();

    // Interrupted after ONE file: a later session unlocks with the new
    // password and still reads both — the new file key for the done one, the
    // previous key (sealed in the marker) for the other.
    const a2 = (await rewrapFileKey(a, start.previous, start.fmk))!;
    expect(a2.byteLength).toBe(a.byteLength);
    expect(new Uint8Array(a2).slice(97)).toEqual(new Uint8Array(a).slice(97)); // content untouched
    const fmk = (await unlockWithPassword(m, NEW))!;
    const previous = (await unlockPrevious(m, fmk))!;
    expect(dec.decode(await decryptFileAny(fmk, previous, a2))).toBe('birinci');
    expect(dec.decode(await decryptFileAny(fmk, previous, b))).toBe('ikinci');
    // The recovery key the re-key minted opens it too.
    expect(await unlockWithRecoveryKey(m, start.recoveryKey)).not.toBeNull();

    // Resuming is running the same step again: a done file says so (null).
    expect(await rewrapFileKey(a2, previous, fmk)).toBeNull();
    const b2 = (await rewrapFileKey(b, previous, fmk))!;

    const done = wire(finishRekey(m));
    expect(done.v).toBe(2); // nothing else required: back to a plain v2 folder
    expect(done.req).toBeUndefined();
    expect(done.rekey).toBeUndefined();
    const fmk2 = (await unlockWithPassword(done, NEW))!;
    expect(dec.decode(await decryptFile(fmk2, a2))).toBe('birinci');
    expect(dec.decode(await decryptFile(fmk2, b2))).toBe('ikinci');
    expect(await unlockWithPassword(done, OLD)).toBeNull();
    expect(await unlockPrevious(done, fmk2)).toBeNull();
    // v0.47.0 opens the finished folder again.
    const back = legacy047.parseMarker(JSON.stringify(done))!;
    expect(dec.decode(await legacy047.decryptFile((await legacy047.unlockWithPassword(back, NEW))!, b2))).toBe('ikinci');
  }, SLOW);

  it('keeps the recovery key when the reset was made with it, and keeps names readable', async () => {
    const made = await legacy030.createMarker(OLD);
    const up = await upgradeMarkerV1(wire(made.marker as E2eMarker), OLD);
    const withNames = await (async () => {
      // A v1 folder upgraded in place, then switched to encrypted names.
      const { enableNames } = await import('../../../packages/core/src/lib/e2ecrypto');
      return enableNames(wire(up.marker), up.fmk);
    })();
    const stored = (await encryptName(withNames.names, 'Ek-1.docx', withNames.names.rootId)).stored;
    const file = await legacy030.encryptFile(made.kek, bytes('eski belge'));

    const start = await startRekey(wire(withNames.marker), { recoveryKey: up.recoveryKey }, NEW);
    expect(start.recoveryKeyIsNew).toBe(false);
    expect(start.recoveryKey).toBe(up.recoveryKey);
    const m = wire(start.marker);
    expect(m.req).toEqual(['names', 'rekey']);
    const fmk = (await unlockWithRecoveryKey(m, up.recoveryKey))!;
    const nk = await unlockNameKey(m, fmk);
    expect((await decryptStoredName(nk!, stored, nk!.rootId)).name).toBe('Ek-1.docx');
    const f2 = (await rewrapFileKey(file, start.previous, start.fmk))!;
    const done = wire(finishRekey(m));
    expect(done.v).toBe(3);
    expect(done.req).toEqual(['names']);
    expect(dec.decode(await decryptFile((await unlockWithPassword(done, NEW))!, f2))).toBe('eski belge');
  }, SLOW);

  it('carries an escrow slot over to the new key when it is this installation\'s', async () => {
    const made = await legacy030.createMarker(OLD);
    const up = await upgradeMarkerV1(wire(made.marker as E2eMarker), OLD, {
      escrowPublicKey: escrowTestKey.public_spki_b64,
    });
    const file = await legacy030.encryptFile(made.kek, bytes('emanet'));
    const start = await startRekey(wire(up.marker), { password: OLD }, NEW, {
      escrowPublicKey: escrowTestKey.public_spki_b64,
    });
    const m = wire(start.marker);
    expect(m.esc?.kid).toBe(await escrowKeyId(escrowTestKey.public_spki_b64));
    const priv = await importEscrowPrivateKey(escrowTestKey.private_pkcs8_b64);
    const byEsc = (await unlockWithEscrowKey(m, priv))!;
    const f2 = (await rewrapFileKey(file, start.previous, start.fmk))!;
    expect(dec.decode(await decryptFile(byEsc, f2))).toBe('emanet');
  }, SLOW);

  it('refuses rather than drop an escrow slot it cannot re-seal', async () => {
    const made = await legacy030.createMarker(OLD);
    const up = await upgradeMarkerV1(wire(made.marker as E2eMarker), OLD, {
      escrowPublicKey: escrowTestKey.public_spki_b64,
    });
    // No installation key on offer (escrow switched off, or another install).
    await expect(startRekey(wire(up.marker), { password: OLD }, NEW)).rejects.toThrow(/escrow/);
  }, SLOW);

  it('a wrapped folder can be re-keyed on purpose too (the old password may be known)', async () => {
    const made = await createEncryptedFolder(OLD);
    const file = await encryptFile(made.fmk, bytes('döndür'));
    const start = await startRekey(wire(made.marker), { password: OLD }, NEW);
    const f2 = (await rewrapFileKey(file, start.previous, start.fmk))!;
    const done = wire(finishRekey(wire(start.marker)));
    expect(done.v).toBe(2);
    const fmk = (await unlockWithPassword(done, NEW))!;
    expect(dec.decode(await decryptFile(fmk, f2))).toBe('döndür');
    // The OLD folder key no longer opens the re-wrapped file.
    await expect(decryptFile(made.fmk, f2)).rejects.toBeInstanceOf(E2eDecryptError);
    // The old recovery key is gone with it; the new one works.
    expect(await unlockWithRecoveryKey(done, made.recoveryKey)).toBeNull();
    expect(await unlockWithRecoveryKey(done, start.recoveryKey)).not.toBeNull();
  }, SLOW);

  it('refuses a file that opens under neither key, and never writes half a header', async () => {
    const a = await createEncryptedFolder(OLD);
    const b = await createEncryptedFolder(OLD);
    const alien = await encryptFile(b.fmk, bytes('başka klasör'));
    const start = await startRekey(wire(a.marker), { password: OLD }, NEW);
    await expect(rewrapFileKey(alien, start.previous, start.fmk)).rejects.toBeInstanceOf(E2eDecryptError);
  }, SLOW);
});
