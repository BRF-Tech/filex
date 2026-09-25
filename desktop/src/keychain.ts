// Whether the account store may be written on this machine — and, when it
// may not, what the person has to do about it.
//
// The account store holds each account's API token — durable and full-scope —
// and the promise (src/accounts.ts, README) is that it is written ONLY when
// the OS keychain encrypts it; otherwise the app refuses to store the session.
//
// Two ways that promise used to go wrong on Linux:
//
// ⚠⚠ 1. The refusal came LAST. Measured on the v0.43.0 .deb in a GNOME
//    session with no keyring running: the app let the person start a sign-in,
//    sent them through the browser, spent the one-time code on the exchange —
//    and only then refused to store the token, with an English-only error. The
//    account, token included, stayed in the running app's state (not on disk)
//    as if signed in. Now the state is known before anything starts: the
//    sign-in window explains it and the IPC refuses (src/main.ts, ui/index.html).
//
// ⚠ 2. `basic_text`. When no secret service answers, Chromium can fall back to
//    "encrypting" with a password printed in its own source. Electron 31
//    reports that backend as unavailable (measured: `--password-store=basic`
//    → basic_text, isEncryptionAvailable() false), so nothing was written that
//    way — but only because of that one answer: `setUsePlainTextEncryption`,
//    or an Electron that answers differently, would turn it into a token
//    anyone who can read the file can decrypt. So the backend is checked by
//    name as well; it is the only way to tell (Electron's own advice).
//
// A strict snap sees exactly case 1 until `password-manager-service` is
// connected (it does not connect by itself), so its advice names the command.
//
// ⚠ No `electron` import, so node:test can drive it; src/accounts.ts feeds it
// what safeStorage reports.

/**
 * ok           — the keychain encrypts; the store may be read and written.
 * unavailable  — no encryption at all (or none known yet).
 * plaintext    — Linux `basic_text`: "encrypted" with a published key.
 */
export type KeychainState = 'ok' | 'unavailable' | 'plaintext';

export interface KeychainProbe {
  platform: string;
  available: boolean;
  /** `safeStorage.getSelectedStorageBackend()` — Linux only. */
  backend?: string | null;
}

export function keychainState(p: KeychainProbe): KeychainState {
  // Named first: whatever `available` says, this backend is not a keychain.
  if (p.platform === 'linux' && p.backend === 'basic_text' && p.available) return 'plaintext';
  if (!p.available) return 'unavailable';
  if (p.platform !== 'linux') return 'ok';
  // A deny-list, not an allow-list, on purpose: `basic_text` is THE unsafe
  // backend, and a secure one a future Electron adds (a portal, kwallet7) must
  // not lock every user out of signing in.
  // Asked before `ready` (or a backend that could not be determined): no
  // decision can be made, and "store it anyway" is the wrong default.
  if (!p.backend || p.backend === 'unknown') return 'unavailable';
  return 'ok';
}

/**
 * What the sign-in window tells the person, when a sign-in cannot be kept.
 *
 *   snap-connect   a snap: its keyring plug is not connected — one command.
 *   linux-keyring  another Linux package: no keyring answered — start or
 *                  install one (GNOME Keyring, KWallet).
 *   os-storage     Windows/macOS: the OS's own storage is not available.
 */
export type KeychainAdvice = 'snap-connect' | 'linux-keyring' | 'os-storage';

export function keychainAdvice(k: KeychainState, platform: string, channel: string | null): KeychainAdvice | null {
  if (k === 'ok') return null;
  if (platform === 'linux') return channel === 'snap' ? 'snap-connect' : 'linux-keyring';
  return 'os-storage';
}

/**
 * The command that connects a snap's keyring plug — the one line the sign-in
 * window shows a snap user. Built from the name snapd runs this copy under
 * (`SNAP_INSTANCE_NAME`, which a parallel install suffixes; else
 * `SNAP_NAME`), never typed out: the snap was renamed once already
 * (filex → filex-app), and a command naming the wrong snap fails with
 * "snap not found".
 */
export function snapConnectCommand(env: Record<string, string | undefined>, plug = 'password-manager-service'): string | null {
  const name = env.SNAP_INSTANCE_NAME || env.SNAP_NAME;
  return name ? `snap connect ${name}:${plug}` : null;
}
