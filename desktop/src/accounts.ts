import { app, safeStorage } from 'electron';
import fs from 'node:fs';
import path from 'node:path';
import { EMPTY_STATE, type DesktopState } from './account-state.js';

// The account store on disk. The shape and the rules — which account is
// active, what signing in again means — live in src/account-state.ts, which
// has no electron import and is covered by node:test; this file is the
// keychain around it. Import either from here.
//
// Everything is encrypted with the OS keychain (safeStorage). If the keychain
// is unavailable we REFUSE to write rather than falling back to plaintext: a
// durable, full-scope API token sitting readable on disk is a worse outcome
// than an app that says it cannot store the session.

export * from './account-state.js';

function file(): string {
  return path.join(app.getPath('userData'), 'desktop-state.bin');
}

export function loadState(): DesktopState {
  try {
    const raw = fs.readFileSync(file());
    if (!safeStorage.isEncryptionAvailable()) return structuredClone(EMPTY_STATE);
    return { ...structuredClone(EMPTY_STATE), ...(JSON.parse(safeStorage.decryptString(raw)) as DesktopState) };
  } catch {
    return structuredClone(EMPTY_STATE);
  }
}

export function saveState(state: DesktopState): void {
  if (!safeStorage.isEncryptionAvailable()) {
    throw new Error('OS keychain unavailable — refusing to store account tokens in plaintext');
  }
  fs.mkdirSync(path.dirname(file()), { recursive: true });
  fs.writeFileSync(file(), safeStorage.encryptString(JSON.stringify(state)), { mode: 0o600 });
}
