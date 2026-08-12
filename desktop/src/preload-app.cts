// Preload for the MAIN window — the app's own page (account rail + explorer +
// app settings).
//
// The page is ours, but it is still a renderer, so the bridge stays narrow and
// nothing here hands over a token by default. `token()` fetches one credential
// for one account at call time, which is what `<filex-explorer>`'s
// `auth: { kind: 'bearer', token: fn }` form is for — the value never has to sit
// in the page between requests.
import { contextBridge, ipcRenderer } from 'electron';

contextBridge.exposeInMainWorld('filexApp', {
  isDesktop: true,

  getState: () => ipcRenderer.invoke('state:get'),
  token: (accountId: string) => ipcRenderer.invoke('account:token', accountId),

  // accounts
  addAccount: () => ipcRenderer.invoke('auth:add'),
  signOut: (id: string) => ipcRenderer.invoke('auth:signOut', id),
  switchAccount: (id: string) => ipcRenderer.invoke('auth:switch', id),
  /** Opens the SERVER's admin panel in the system browser, not in here. */
  openAdmin: (id: string) => ipcRenderer.invoke('account:openAdmin', id),

  // files
  storages: (accountId: string) => ipcRenderer.invoke('remote:storages', accountId),
  /** The server's own logo + name (Branding settings), for the account rail. */
  branding: (accountId: string) => ipcRenderer.invoke('remote:branding', accountId),

  // sync
  browse: (accountId: string, remotePath: string) =>
    ipcRenderer.invoke('remote:browse', accountId, remotePath),
  addSync: (remotePath: string) => ipcRenderer.invoke('sync:add', remotePath),
  removeSync: (id: string) => ipcRenderer.invoke('sync:remove', id),
  syncTrash: () => ipcRenderer.invoke('sync:trash'),
  openLocal: (p: string) => ipcRenderer.invoke('shell:openPath', p),

  // app settings
  setSettings: (patch: unknown) => ipcRenderer.invoke('settings:set', patch),

  /** Backs the navigator.share polyfill the page installs. See main.ts. */
  share: (data: unknown) => ipcRenderer.invoke('app:share', data),

  // Transfers happen in a background process and accounts can change from the
  // tray, so the page is told when to repaint rather than only being right at
  // the moment it opened.
  onChanged: (fn: () => void) => ipcRenderer.on('sync:changed', () => fn()),
  onOpenSettings: (fn: () => void) => ipcRenderer.on('app:open-settings', () => fn()),
});
