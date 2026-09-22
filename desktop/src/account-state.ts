// The account store's shape and rules — everything in src/accounts.ts that is
// not the keychain.
//
// ⚠ No `electron` import, so node:test can drive it (same boundary as
// src/notifications.ts). src/accounts.ts re-exports all of it; import from
// there in the app.
//
// Multi-account store. This is a PC app: one person routinely has a work
// server and a personal one, or two tenants on the same host, and expects to
// add both and switch — not to sign out to look at the other.

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
  /** The storage "Open with filex" puts its scratch copies on, remembered after
   *  the first document opens successfully. Without it every open re-discovers
   *  the storage list and could settle on a different one than last time,
   *  scattering working copies across the account. */
  openWithStorage?: string;
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
  /** Show a native OS notification when something new lands in the bell.
   *  Default ON — the desktop window has no bell of its own, so off would mean
   *  the always-running client is the one that never tells you anything. */
  notifications: boolean;
  /**
   * The ground the window last painted, as the RENDERER resolved it — the
   * palette's own `--fe-bg`, in whichever variant was active.
   *
   * ⚠ This is not a preference and nothing reads it as one. It exists because
   * `BrowserWindow.backgroundColor` has to be decided BEFORE the page that
   * knows the answer has loaded, and Electron paints its default white in the
   * meantime. The theme mode and the palette both live in the renderer's
   * localStorage (packages/core/src/lib/themes.ts), which the main process
   * cannot read at construction — so the window remembers what it painted last
   * time and opens on that. Absent (a first-ever launch) falls back to the OS.
   */
  themeBg?: string;
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

/** What a first launch starts from — and what every stored state is merged
 *  over, so a field added later reads as its default instead of undefined. */
export const EMPTY_STATE: DesktopState = {
  accounts: [],
  activeId: null,
  syncFolders: [],
  runInBackground: true,
  launchAtLogin: false,
  locale: 'system',
  notifications: true,
};

export function activeAccount(state: DesktopState): Account | null {
  return state.accounts.find((a) => a.id === state.activeId) ?? null;
}

const normUrl = (s: string) => s.replace(/\/+$/, '').toLowerCase();

/** The account for this server + person, if this computer has it. */
export function findAccount(state: DesktopState, serverUrl: string, email: string): Account | null {
  return (
    state.accounts.find(
      (a) => normUrl(a.serverUrl) === normUrl(serverUrl) && a.email.toLowerCase() === email.toLowerCase(),
    ) ?? null
  );
}

/** Adds or REPLACES: signing in again to the same server as the same user
 *  refreshes that account's token instead of stacking duplicates. */
export function upsertAccount(state: DesktopState, acc: Omit<Account, 'id' | 'addedAt'>): Account {
  const existing = findAccount(state, acc.serverUrl, acc.email);
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

/**
 * A completed sign-in. `existed` says the account was already here — its id,
 * its sync pairs and its filex folder are kept, and only the token changes.
 *
 * ⚠ That is exactly the case where a running watcher is WRONG: it was started
 * with the old token in its environment and keeps using it. The caller stops
 * it so reconcile() starts one with the new token.
 */
export function signIn(
  state: DesktopState,
  acc: Omit<Account, 'id' | 'addedAt'>,
): { account: Account; existed: boolean } {
  const existed = findAccount(state, acc.serverUrl, acc.email) !== null;
  return { account: upsertAccount(state, acc), existed };
}

export function removeAccount(state: DesktopState, id: string): void {
  state.accounts = state.accounts.filter((a) => a.id !== id);
  // Folder pairings belong to the account that authorized them; orphaning them
  // would leave the sync screen listing work nobody can perform.
  state.syncFolders = state.syncFolders.filter((f) => f.accountId !== id);
  if (state.activeId === id) state.activeId = state.accounts[0]?.id ?? null;
}
