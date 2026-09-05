// Regenerate the escrow-adoption fixtures in this directory.
//
//   node --experimental-strip-types backend/internal/e2e/testdata/adoption/gen.mjs
//
// It imports packages/core/src/lib/e2ecrypto.ts — the module the browser
// actually runs — and writes two real encrypted folders:
//
//   before/    a folder created while escrow was OFF (no `esc` slot)
//   after/     a folder created after escrow was ADOPTED (`esc` sealed to
//              the key in escrow_pub.b64)
//   upgraded/  a folder created while escrow was OFF whose OWNER later
//              accepted the offer — its file was encrypted BEFORE the slot
//              existed and is not rewritten, which is the claim under test
//   declined/  the same, but the owner said no. `esc_declined` is set and
//              there is no slot, today or after any number of unlocks
//
// installation_crypto_test.go then does the arithmetic in Go with real
// RSA-OAEP-256 and real AES-GCM. The point of generating the inputs here
// rather than in Go is that the ciphertext under test is produced by the
// shipping client, not by the test's own idea of the format.
//
// The keypair is a throwaway minted for this fixture; nothing else uses it.

import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { fileURLToPath } from 'node:url';
import { webcrypto } from 'node:crypto';

if (!globalThis.crypto) globalThis.crypto = webcrypto;

const here = dirname(fileURLToPath(import.meta.url));
const cryptoModule = join(here, '../../../../../packages/core/src/lib/e2ecrypto.ts');
const { createEncryptedFolder, encryptFile, bytesToB64, addEscrowSlot, declineEscrowSlot } =
  await import(
  pathToFileURL(cryptoModule).href,
);

const PASSWORD = 'correct horse battery staple';
const PLAINTEXT = 'the quick brown fox jumps over the lazy dog\n';

// A 2048-bit escrow keypair. Small on purpose: the test decrypts with it on
// every run, and 2048 is the minimum filex accepts (EscrowMinKeyBits).
const pair = await webcrypto.subtle.generateKey(
  { name: 'RSA-OAEP', modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: 'SHA-256' },
  true,
  ['encrypt', 'decrypt'],
);
const spki = bytesToB64(new Uint8Array(await webcrypto.subtle.exportKey('spki', pair.publicKey)));
const pkcs8 = bytesToB64(new Uint8Array(await webcrypto.subtle.exportKey('pkcs8', pair.privateKey)));

// ⚠ `iterations` is NOT honoured downward: createEncryptedFolder clamps with
// Math.max(E2E_MIN_ITERATIONS, …), so every fixture below carries the real
// 600 000 rounds. The Go test pays ~0.2 s of PBKDF2 for it, deliberately —
// a fixture with weakened parameters would not be the format under test.
async function folder(name, escrowPublicKey, after) {
  const dir = join(here, name);
  mkdirSync(dir, { recursive: true });
  const { marker, fmk } = await createEncryptedFolder(PASSWORD, { escrowPublicKey });
  // The file is encrypted with the folder key AS IT IS NOW. For `upgraded/`
  // that is before the escrow slot exists, which is the whole point: the
  // slot is added afterwards and no file is rewritten.
  const ct = await encryptFile(fmk, new TextEncoder().encode(PLAINTEXT).buffer);
  const final = after ? await after(marker) : marker;
  writeFileSync(join(dir, 'marker.json'), JSON.stringify(final, null, 2) + '\n');
  writeFileSync(join(dir, 'secret.bin'), Buffer.from(ct));
  return final;
}

const before = await folder('before', null);
const after = await folder('after', spki);
// The owner accepted the offer at unlock: same folder, same ciphertext, a
// slot bolted on with the password.
const upgraded = await folder('upgraded', null, (m) => addEscrowSlot(m, PASSWORD, spki));
// The owner declined. Recorded in the marker so the question is not asked
// again, and reversible by the same person who declined it.
const declined = await folder('declined', null, (m) =>
  declineEscrowSlot(m, '2026-09-05T12:00:00Z'),
);

writeFileSync(join(here, 'escrow_pub.b64'), spki + '\n');
writeFileSync(join(here, 'escrow_priv.b64'), pkcs8 + '\n');
writeFileSync(
  join(here, 'meta.json'),
  JSON.stringify(
    {
      password: PASSWORD,
      plaintext: PLAINTEXT,
      generated_by: 'packages/core/src/lib/e2ecrypto.ts via gen.mjs',
      before_has_escrow_slot: !!before.esc,
      after_has_escrow_slot: !!after.esc,
      upgraded_has_escrow_slot: !!upgraded.esc,
      declined_has_escrow_slot: !!declined.esc,
      declined_at: declined.esc_declined ?? null,
    },
    null,
    2,
  ) + '\n',
);

console.log('before.esc    =', before.esc ?? null);
console.log('after.esc.kid =', after.esc?.kid, 'alg =', after.esc?.alg);
console.log('upgraded.esc.kid =', upgraded.esc?.kid);
console.log('declined.esc  =', declined.esc ?? null, 'declined_at =', declined.esc_declined);
console.log('wrote fixtures to', here);
