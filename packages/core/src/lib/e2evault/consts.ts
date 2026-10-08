/**
 * e2evault/consts — the few numbers of the vault format that code outside the
 * vault needs while no vault is open: the key file's rules (lib/e2ecrypto
 * reads every key file), the pack sizes the new-folder dialog offers and the
 * entry limit the strip says.
 *
 * ⚠ A module of its own so the explorer's main chunk carries only these. The
 * rest of lib/e2evault and the vault's composable are loaded when a vault is
 * opened or made (composables/useE2eVault.ts → e2eVaultEngine.ts): everything
 * that imports this file is in the main chunk, and a module there takes every
 * export the lazy code uses with it. layout.ts re-exports all of them, and the
 * Go side keeps the same numbers (backend/internal/e2e/vault.go).
 *
 * No imports: nothing here may pull more into the main chunk.
 */

/** The `req` entry of a vault's key file. */
export const VAULT_FEATURE = 'vault';
/** `vault.v` in the key file, header byte 8, and the body's version. */
export const VAULT_FORMAT = 1;
/** Pack sizes a reader accepts, as log2. */
export const VAULT_MIN_PACK_LOG2 = 16;
export const VAULT_MAX_PACK_LOG2 = 24;
/** Pack sizes a writer creates: 4 MiB (the default) or 16 MiB. */
export const VAULT_DEFAULT_PACK_LOG2 = 22;
export const VAULT_LARGE_PACK_LOG2 = 24;
export const VAULT_WRITER_PACK_LOG2: readonly number[] = [VAULT_DEFAULT_PACK_LOG2, VAULT_LARGE_PACK_LOG2];
export const VAULT_ID_LEN = 16;
/** A writer refuses a change beyond this many entries. */
export const VAULT_ENTRIES_WRITE_MAX = 250_000;

/** A pack size a reader accepts. */
export function validReaderPackLog2(n: unknown): n is number {
  return typeof n === 'number' && Number.isInteger(n) && n >= VAULT_MIN_PACK_LOG2 && n <= VAULT_MAX_PACK_LOG2;
}
