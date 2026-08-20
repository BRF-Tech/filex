import { app, safeStorage } from 'electron';
import fs from 'node:fs';
import path from 'node:path';

// Multi-account store. This is a PC app: one person routinely has a work
// server and a personal one, or two tenants on the same host, and expects to
// add both and switch — not to sign out to look at the other.
//
// Everything is encrypted with the OS keychain (safeStorage). If the keychain
// is unavailable we REFUSE to write rather than falling back to plaintext: a
// durable, full-scope API token sitting readable on disk is a worse outcome
// than an app that says it cannot store the session.

export interface Account {
  id: string;
  serverUrl: string;
  email: string;
  token: string;
  addedAt: string;
  /** Root folder for "keep on this computer" mirrors. Chosen once, at the
   *  first keep; every kept folder lands under it as
   *  `<syncRoot>/<storage>/<path…>`. Absent until then. */
  syncRoot?: string;
}

export interface DesktopState {
  accounts: Account[];
  activeId: string | null;
  /** Folder pairings shown in "Sync folders". The engine that acts on them is
   *  a separate piece of work; this is the record it will read. */
  syncFolders: SyncFolder[];
  /** Keep running in the tray when the window is closed. */
  runInBackground: boolean;
  launchAtLogin: boolean;
  /** Interface language. 'system' follows the OS — what the app did when there
   *  was nothing to choose, so an existing install keeps the language it
   *  already had. The window, the tray menu and the file explorer inside it all
   *  read this one value: a Turkish shell around an English file list is one
   *  app pretending to be two. */
  locale: DesktopLocale;
}

/** 'system' resolves against the OS at read time, so moving a laptop between
 *  language settings keeps working without a stored value going stale. */
export type DesktopLocale = 'system' | 'en' | 'tr';

export interface SyncFolder {
  id: string;
  accountId: string;
  remotePath: string;
  localPath: string;
  enabled: boolean;
  lastSyncAt: string | null;
  status: 'idle' | 'syncing' | 'error' | 'never';
}

const EMPTY: DesktopState = {
  accounts: [],
  activeId: null,
  syncFolders: [],
  runInBackground: true,
  launchAtLogin: false,
  locale: 'system',
};

function file(): string {
  return path.join(app.getPath('userData'), 'desktop-state.bin');
}

export function loadState(): DesktopState {
  try {
    const raw = fs.readFileSync(file());
    if (!safeStorage.isEncryptionAvailable()) return { ...EMPTY };
    return { ...EMPTY, ...(JSON.parse(safeStorage.decryptString(raw)) as DesktopState) };
  } catch {
    return { ...EMPTY };
  }
}

export function saveState(state: DesktopState): void {
  if (!safeStorage.isEncryptionAvailable()) {
    throw new Error('OS keychain unavailable — refusing to store account tokens in plaintext');
  }
  fs.mkdirSync(path.dirname(file()), { recursive: true });
  fs.writeFileSync(file(), safeStorage.encryptString(JSON.stringify(state)), { mode: 0o600 });
}

export function activeAccount(state: DesktopState): Account | null {
  return state.accounts.find((a) => a.id === state.activeId) ?? null;
}

/** Adds or REPLACES: signing in again to the same server as the same user
 *  refreshes that account's token instead of stacking duplicates. */
export function upsertAccount(state: DesktopState, acc: Omit<Account, 'id' | 'addedAt'>): Account {
  const norm = (s: string) => s.replace(/\/+$/, '').toLowerCase();
  const existing = state.accounts.find(
    (a) => norm(a.serverUrl) === norm(acc.serverUrl) && a.email.toLowerCase() === acc.email.toLowerCase(),
  );
  if (existing) {
    existing.token = acc.token;
    state.activeId = existing.id;
    return existing;
  }
  const created: Account = {
    ...acc,
    id: `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
    addedAt: new Date().toISOString(),
  };
  state.accounts.push(created);
  state.activeId = created.id;
  return created;
}

export function removeAccount(state: DesktopState, id: string): void {
  state.accounts = state.accounts.filter((a) => a.id !== id);
  // Folder pairings belong to the account that authorized them; orphaning them
  // would leave the sync screen listing work nobody can perform.
  state.syncFolders = state.syncFolders.filter((f) => f.accountId !== id);
  if (state.activeId === id) state.activeId = state.accounts[0]?.id ?? null;
}
