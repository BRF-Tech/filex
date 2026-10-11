/**
 * appPlaintext - an app's interface editing a file of an end-to-end
 * encrypted folder (filex 0.56, task #189; docs/E2E-ENCRYPTION.md → Editing
 * in an app).
 *
 * The CryptPad model: the server holds the file only as ciphertext and has
 * no key. The explorer holds the folder key while the folder is unlocked
 * (the key ring, lib/e2ecrypto createKeyRing), so:
 *
 *   read   AppFrame asks the server's app door for the file with `e2e=1`
 *          and gets the CIPHERTEXT; `open` decrypts it here and the
 *          plaintext goes to the app's sandboxed frame over its own
 *          MessagePort (transferred: this page keeps no copy, and no
 *          `blob:` address is ever made of it);
 *   save   what the app hands over is encrypted here with the same folder
 *          key (`seal`) and only then sent to the save door with `e2e=1`,
 *          which refuses a body that is not an encrypted file.
 *
 * The door holds no key itself: it asks the host for the folder key of a
 * path at the moment of each call (`keyFor`), so a folder locked while the
 * app is open answers `null` - nothing more is decrypted or encrypted.
 *
 * Which app may be handed a file is lib/appViewer's rule (`e2ePlaintext`):
 * an app granted `files:e2e-plaintext` (E2E_PLAINTEXT_GRANT), a file of an
 * unlocked encrypted folder - its names encrypted too or not: the app is
 * chosen by the name decrypted here (`plaintextOpenOf`) - never a `.fxe`,
 * and the administrator's switch on (capabilities
 * `apps_e2e_plaintext`).
 *
 * The door also answers the app's own small store for such a session
 * (`ledger`, lib/appStateE2e): what the app keeps with `state.set` while it
 * holds the file is sealed with a key derived from the same folder key.
 *
 * A VAULT's file (level 3, filex 0.56) takes the vault's own way, the door's
 * `vault` half (AppVaultDoor): the server holds no file there, only equal
 * packs and an encrypted index, so nothing goes through the server's app
 * doors at all. The vault engine (composables/e2eVaultEngine) reads the file
 * from its packs; a save is the vault's next generation, written under the
 * vault's write lock like any change in it - refused, and said, when
 * somebody else writes the vault, when this session's lock ended before the
 * commit, or when the file changed since the app read it. The server never
 * learns the file's name, its path or that an app opened it.
 */
import { LIMITS } from '@brftech/filex-app-ui/protocol';

import { E2E_MAX_FILE_BYTES, encryptFile } from './e2ecrypto';
import { decryptFolderFileStream } from './e2estream';
import { appStateLedger, type AppStateLedger } from './appStateE2e';

/** The folder key of a file's encrypted folder, and the previous one while
 *  the folder is mid re-key. */
export interface PlaintextKeys {
  fmk: CryptoKey;
  previous: CryptoKey | null;
}

/** The host's half: decrypt and encrypt in this tab, with the folder's key. */
export interface AppPlaintextDoor {
  /**
   * filex 0.56: a vault's file (level 3) - read from the vault's packs and
   * saved as its next generation, all in this tab (AppVaultDoor). Absent: no
   * app is handed a vault's file here.
   */
  vault?: AppVaultDoor | null;
  /**
   * The plaintext of the file at `path`, from the ciphertext the server's
   * app door answered. null: the folder is locked now. Throws an
   * E2eDecryptError for a wrong key or damaged data, and TooLargeToHand past
   * `limit`.
   */
  open(path: string, ciphertext: ReadableStream<Uint8Array>, limit?: number): Promise<Uint8Array<ArrayBuffer> | null>;
  /**
   * The bytes, encrypted for the folder of the file at `path` (a file of an
   * encrypted folder as filex writes it: `filexe2e`, one-shot). null: the
   * folder is locked now. Throws TooLargeToHand past `limit`.
   */
  seal(path: string, plain: Uint8Array<ArrayBuffer>, limit?: number): Promise<ArrayBuffer | null>;
  /**
   * filex 0.56: the ledger `app` keeps its `state.set` values in while it
   * holds a file of the folder of `path` in the clear (lib/appStateE2e) -
   * keys derived from that folder's key, one ledger per app. null: the
   * folder is locked now (the app starts with an empty one). Throws
   * LedgerNeedsAccount when the folder is open but this page cannot tell
   * which account it is signed in as - the ledger is sealed for one.
   */
  ledger(path: string, app: string): Promise<AppStateLedger | null>;
  /**
   * The folder key of the file at `path` itself (and the previous one while
   * the folder is re-keyed), null while it is locked - for what this page
   * seals with it besides the file: an editing session's key and the changes
   * kept in this browser (lib/coeditSession, filex 0.56). Non-extractable
   * CryptoKeys; the frame never sees them.
   */
  folderKeys?(path: string): PlaintextKeys | null;
}

/**
 * The sealed store cannot be opened: it is bound to the account the session
 * is signed in as (lib/appStateE2e), and this page could not learn which (an
 * embed without the account's `/api/auth/me`, a session gone). Not a locked
 * folder: the folder is open, the app's own store for it is not.
 */
export class LedgerNeedsAccount extends Error {
  constructor() {
    super('the sealed app store needs the account this page is signed in as');
    this.name = 'LedgerNeedsAccount';
  }
}

/** The sealed ledger of `app` under a folder key (a vault's too): throws
 *  LedgerNeedsAccount where no account is known. */
async function sealedLedgerOf(fmk: CryptoKey, app: string, previous?: CryptoKey | null): Promise<AppStateLedger> {
  const ledger = await appStateLedger(fmk, app, previous);
  if (!ledger) throw new LedgerNeedsAccount();
  return ledger;
}

/** A file larger than an app may be handed in the clear, or hand back. */
export class TooLargeToHand extends Error {
  constructor(public limit: number) {
    super(`at most ${limit >> 20} MiB in an encrypted folder`);
    this.name = 'TooLargeToHand';
  }
}

/** What an app may be handed: the protocol's limit, never past what one-shot
 *  encryption takes. */
export const APP_PLAINTEXT_LIMIT = Math.min(LIMITS.maxEncryptedBytes, E2E_MAX_FILE_BYTES);

/**
 * The door, over the host's keys. `keyFor` answers the folder key for a
 * path while the folder is unlocked in this tab, null otherwise; `vault`,
 * the vault's half (createAppVaultDoor), when the host opens vaults.
 */
export function createAppPlaintextDoor(keyFor: (path: string) => PlaintextKeys | null, vault?: AppVaultDoor | null): AppPlaintextDoor {
  return {
    vault: vault ?? null,
    async open(path, ciphertext, limit = APP_PLAINTEXT_LIMIT) {
      const keys = keyFor(path);
      if (!keys) {
        await ciphertext.cancel().catch(() => undefined);
        return null;
      }
      // A file written in the clear over WebDAV into the folder is handed
      // over as it is: the server already holds those bytes (passPlain).
      const reader = decryptFolderFileStream(keys.fmk, keys.previous, ciphertext, { passPlain: true }).getReader();
      const parts: Uint8Array[] = [];
      let total = 0;
      try {
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          total += value.byteLength;
          if (total > limit) throw new TooLargeToHand(limit);
          parts.push(value);
        }
      } catch (e) {
        await reader.cancel(e).catch(() => undefined);
        for (const p of parts) p.fill(0);
        throw e;
      }
      const out = new Uint8Array(total);
      let at = 0;
      for (const p of parts) {
        out.set(p, at);
        at += p.byteLength;
        // The pieces are copies the page no longer needs: not left behind.
        p.fill(0);
      }
      return out;
    },
    async seal(path, plain, limit = APP_PLAINTEXT_LIMIT) {
      if (plain.byteLength > limit) throw new TooLargeToHand(limit);
      const keys = keyFor(path);
      if (!keys) return null;
      return encryptFile(keys.fmk, plain.buffer.byteLength === plain.byteLength ? plain.buffer : plain.slice().buffer);
    },
    async ledger(path, app) {
      const keys = keyFor(path);
      return keys ? sealedLedgerOf(keys.fmk, app, keys.previous) : null;
    },
    folderKeys(path) {
      return keyFor(path);
    },
  };
}

/* ── a vault's file (level 3, filex 0.56) ─────────────────────────────── */

/**
 * Why the vault did not hand an app its file, or did not take its save. The
 * vault said it to the person already (its toast, its strip), except
 * `locked`: the lock says that itself, and closes the app.
 */
export type VaultAppRefusal =
  /** The vault is not open in this tab (locked by hand, or idle). */
  | 'locked'
  /** Somebody else holds the vault's write lock (the server's VAULT_LOCKED). */
  | 'held'
  /** This session's write lock ended before the save was committed
   *  (VAULT_LOCK_LOST, or the connection): nothing was written. */
  | 'lock_lost'
  /** The file changed in the vault since the app read it. */
  | 'changed'
  /** The file is not in the vault any more (deleted, moved, renamed). */
  | 'gone'
  /** The vault is read-only for this session (damaged, rolled back, or
   *  written by a newer filex). */
  | 'read_only'
  /** More than an app may be handed. */
  | 'too_large'
  /** Anything else: a pack that does not decrypt, a server failure. */
  | 'failed';

/** The vault engine's answer to a read (composables/e2eVaultEngine
 *  `readForApp`): the file, and the signature of the version read. */
export type VaultAppRead = { ok: true; bytes: Uint8Array; sig: string } | { ok: false; why: VaultAppRefusal };

/** The vault engine's answer to a save (`saveForApp`): the version written. */
export type VaultAppSaved = { ok: true; size: number; sig: string } | { ok: false; why: VaultAppRefusal };

/** What the vault door asks of the vault (the explorer's useE2eVault). */
export interface VaultAppEngine {
  readForApp(path: string, limit: number): Promise<VaultAppRead>;
  saveForApp(path: string, bytes: Uint8Array, expect: string | undefined): Promise<VaultAppSaved>;
  /** The vault's folder key (an HKDF key) while the vault is open in this
   *  tab, null otherwise: the app's sealed store for the vault comes from it. */
  keyForApp(path: string): CryptoKey | null;
}

/** The vault refused (VaultAppRefusal says why). */
export class VaultAppRefused extends Error {
  constructor(public readonly why: VaultAppRefusal) {
    super(`vault: ${why}`);
    this.name = 'VaultAppRefused';
  }
}

/**
 * The vault's half of the door. `path` is the file's wire path in the vault
 * (the vault folder, `/`, its path in the tree), which exists only in this
 * tab: it is handed to the vault engine, never to the server.
 */
export interface AppVaultDoor {
  /**
   * The plaintext of the vault's file at `path`, and the signature of the
   * version read (`sig`) - a save is written only over it. null: the vault
   * is not open here. Throws TooLargeToHand past `limit` (nothing fetched),
   * VaultAppRefused otherwise.
   */
  read(path: string, limit?: number): Promise<{ bytes: Uint8Array<ArrayBuffer>; sig: string } | null>;
  /**
   * `plain` over the vault's file at `path` as the vault's next generation,
   * only over the version `expect` names (a read's `sig`, or "<size>:<ms>"
   * as the file's row said it). Answers the bytes written and the new
   * version's `sig`. Throws TooLargeToHand past `limit`, VaultAppRefused
   * when the vault did not write it.
   */
  save(path: string, plain: Uint8Array<ArrayBuffer>, expect?: string, limit?: number): Promise<{ size: number; sig: string }>;
  /**
   * The ledger `app` keeps its `state.set` values in while it holds a file of
   * the vault (lib/appStateE2e): sealed with keys derived from the vault's own
   * key, one ledger per account and app - as AppPlaintextDoor.ledger is for a
   * folder. null: the vault is not open here. Throws LedgerNeedsAccount when
   * no account is known.
   */
  ledger(path: string, app: string): Promise<AppStateLedger | null>;
}

/** Bytes whose buffer is exactly them: what a transfer hands over whole. */
function ownBytes(b: Uint8Array): Uint8Array<ArrayBuffer> {
  if (b.buffer instanceof ArrayBuffer && b.byteOffset === 0 && b.byteLength === b.buffer.byteLength) return b as Uint8Array<ArrayBuffer>;
  const out = new Uint8Array(b.byteLength);
  out.set(b);
  b.fill(0);
  return out;
}

/** The vault door, over the vault engine (useE2eVault `readForApp` / `saveForApp`). */
export function createAppVaultDoor(engine: VaultAppEngine): AppVaultDoor {
  return {
    async read(path, limit = APP_PLAINTEXT_LIMIT) {
      const r = await engine.readForApp(path, limit);
      if (r.ok) return { bytes: ownBytes(r.bytes), sig: r.sig };
      if (r.why === 'locked') return null;
      if (r.why === 'too_large') throw new TooLargeToHand(limit);
      throw new VaultAppRefused(r.why);
    },
    async save(path, plain, expect, limit = APP_PLAINTEXT_LIMIT) {
      if (plain.byteLength > limit) throw new TooLargeToHand(limit);
      const r = await engine.saveForApp(path, plain, expect);
      if (!r.ok) throw new VaultAppRefused(r.why);
      return { size: r.size, sig: r.sig };
    },
    async ledger(path, app) {
      // A vault has no re-key (docs/E2E-VAULT-FORMAT.md): one key, no previous.
      const fmk = engine.keyForApp(path);
      return fmk ? sealedLedgerOf(fmk, app) : null;
    },
  };
}
