/**
 * e2evault/keys — the vault's key schedule (docs/E2E-VAULT-FORMAT.md → "Keys
 * and the nonce rule").
 *
 *   FMK (32 bytes, from a key slot)
 *    |- HKDF-SHA-256(IKM = FMK, salt = vault id,
 *    |               info = "filex-vault-index-v1"   ‖ seal id)     -> index key
 *    '- HKDF-SHA-256(IKM = FMK, salt = vault id,
 *                    info = "filex-vault-content-v1" ‖ content id)  -> content key
 *
 * A vault's FMK is imported ONCE as a non-extractable HKDF key and its bytes
 * are zeroed (lib/e2ecrypto does that on every unlock of a vault key file).
 * It never encrypts anything itself. Every AES-GCM key derived here encrypts
 * exactly one plaintext: an index file, or one version of one file.
 *
 * WebCrypto only.
 */
import {
  VAULT_ID_LEN,
  VAULT_INFO_CONTENT,
  VAULT_INFO_INDEX,
  bufferView,
  concatBytes,
  utf8,
} from './layout';

/**
 * Import the raw 32 FMK bytes as the vault's HKDF key. Non-extractable; the
 * caller zeroes `raw` afterwards.
 */
export async function importVaultFmk(raw: Uint8Array): Promise<CryptoKey> {
  if (raw.length !== 32) throw new Error('vault: the folder master key must be 32 bytes');
  return crypto.subtle.importKey('raw', bufferView(raw), 'HKDF', false, ['deriveKey', 'deriveBits']);
}

/** True for a key `importVaultFmk` made (what the key ring holds for a vault). */
export function isVaultFmk(key: CryptoKey | null | undefined): key is CryptoKey {
  return !!key && key.algorithm?.name === 'HKDF';
}

function hkdfParams(vaultId: Uint8Array, label: string, id: Uint8Array): HkdfParams {
  if (vaultId.length !== VAULT_ID_LEN) throw new Error('vault: a vault id is 16 bytes');
  if (id.length !== 16) throw new Error('vault: a seal or content id is 16 bytes');
  return {
    name: 'HKDF',
    hash: 'SHA-256',
    salt: bufferView(vaultId),
    info: concatBytes([utf8(label), id]),
  };
}

async function deriveAes(
  fmk: CryptoKey,
  vaultId: Uint8Array,
  label: string,
  id: Uint8Array,
  usages: KeyUsage[],
): Promise<CryptoKey> {
  return crypto.subtle.deriveKey(hkdfParams(vaultId, label, id), fmk, { name: 'AES-GCM', length: 256 }, false, usages);
}

/** The AES-256-GCM key of the index file sealed under `sealId`. */
export function indexKey(fmk: CryptoKey, vaultId: Uint8Array, sealId: Uint8Array): Promise<CryptoKey> {
  return deriveAes(fmk, vaultId, VAULT_INFO_INDEX, sealId, ['encrypt', 'decrypt']);
}

/** The AES-256-GCM key of the file version written under `contentId`. */
export function contentKey(fmk: CryptoKey, vaultId: Uint8Array, contentId: Uint8Array): Promise<CryptoKey> {
  return deriveAes(fmk, vaultId, VAULT_INFO_CONTENT, contentId, ['encrypt', 'decrypt']);
}

/**
 * The same 32 bytes as `indexKey` / `contentKey`, raw — for the test vectors
 * only (the vectors list `index_key` and `content_key`). Nothing in the
 * product reads a derived key's bytes.
 */
export async function deriveVaultKeyBits(
  fmk: CryptoKey,
  vaultId: Uint8Array,
  kind: 'index' | 'content',
  id: Uint8Array,
): Promise<Uint8Array<ArrayBuffer>> {
  const label = kind === 'index' ? VAULT_INFO_INDEX : VAULT_INFO_CONTENT;
  return new Uint8Array(await crypto.subtle.deriveBits(hkdfParams(vaultId, label, id), fmk, 256));
}
