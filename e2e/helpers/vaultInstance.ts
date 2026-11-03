/**
 * A second filex with the vault on (`FILEX_E2E_VAULT=1`), its own data
 * directory and the same admin - for the specs that need level 3 (212 the
 * vault, 236 an app editing a vault's file). The vault is off by default and
 * the shared hermetic instance keeps the default (spec 172 counts two levels
 * there), so these boot their own from E2E_FILEX_BIN, like spec 173's escrow
 * instance. The booting itself is helpers/extraInstance's.
 */
import { bootExtraInstance } from './extraInstance';

/** A filex with the vault on, its own data directory, the same admin. */
export async function bootVaultInstance(bin: string): Promise<{ base: string; stop: () => Promise<void> }> {
  const { base, stop } = await bootExtraInstance(bin, {
    label: 'vault',
    env: {
      FILEX_SECRET_KEY: 'e2e-vault-key-not-a-real-secret',
      FILEX_E2E_VAULT: '1',
    },
  });
  return { base, stop };
}
