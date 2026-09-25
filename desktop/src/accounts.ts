import { app, safeStorage } from 'electron';
import fs from 'node:fs';
import path from 'node:path';
import { EMPTY_STATE, type DesktopState } from './account-state.js';
import { keychainState, type KeychainState } from './keychain.js';

// The account store on disk. The shape and the rules — which account is
// active, what signing in again means — live in src/account-state.ts, which
// has no electron import and is covered by node:test; this file is the
// keychain around it. Import either from here.
//
// Everything is encrypted with the OS keychain (safeStorage). If the keychain
// is unavailable we REFUSE to write rather than falling back to plaintext: a
// durable, full-scope API token sitting readable on disk is a worse outcome
// than an app that says it cannot store the session. ⚠ On Linux "unavailable"
// includes Chromium's `basic_text` fallback — see src/keychain.ts.

export * from './account-state.js';
export type { KeychainState } from './keychain.js';

function file(): string {
  return path.join(app.getPath('userData'), 'desktop-state.bin');
}

/** Whether safeStorage may be trusted with the store right now. Valid only
 *  after `ready` (before it, Linux reports its backend as 'unknown'). */
export function keychain(): KeychainState {
  return keychainState({
    platform: process.platform,
    available: safeStorage.isEncryptionAvailable(),
    backend: process.platform === 'linux' ? safeStorage.getSelectedStorageBackend() : null,
  });
}

/** The error a refused write throws. The sign-in window and Settings explain
 *  the situation in the user's language from `publicState().keychain`; this
 *  text is what the log and an inline error show. */
export function keychainRefusal(k: KeychainState): Error {
  return new Error(
    k === 'plaintext'
      ? 'no OS keychain answered (only the "basic_text" fallback, which anyone can decrypt) — refusing to store account tokens'
      : 'OS keychain unavailable — refusing to store account tokens in plaintext',
  );
}

export function loadState(): DesktopState {
  try {
    // ⚠ Not even READ without a keychain. A file written by a keychain that is
    // merely not answering yet (a login-time race with the secret service)
    // cannot be decrypted by `basic_text`, and the empty state that follows
    // must never be written back over it — saveState refuses for the same
    // reason, so the file waits, untouched, for the next start.
    if (keychain() !== 'ok') return structuredClone(EMPTY_STATE);
    const raw = fs.readFileSync(file());
    return { ...structuredClone(EMPTY_STATE), ...(JSON.parse(safeStorage.decryptString(raw)) as DesktopState) };
  } catch {
    return structuredClone(EMPTY_STATE);
  }
}

export function saveState(state: DesktopState): void {
  const k = keychain();
  if (k !== 'ok') throw keychainRefusal(k);
  fs.mkdirSync(path.dirname(file()), { recursive: true });
  fs.writeFileSync(file(), safeStorage.encryptString(JSON.stringify(state)), { mode: 0o600 });
}
