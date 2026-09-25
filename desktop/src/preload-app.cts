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
  // yeni-pencere:v1 — the page draws its own title bar (frameless window). On
  // macOS it leaves room for the native traffic lights instead of drawing
  // buttons; the page needs to know which.
  isMac: process.platform === 'darwin',
  // Our own window controls for the frameless MAIN window (Win/Linux). Act on
  // the window that sent them (main.ts win:* handlers).
  winMinimize: () => ipcRenderer.invoke('win:minimize'),
  winToggleMaximize: () => ipcRenderer.invoke('win:toggleMaximize'),
  winClose: () => ipcRenderer.invoke('win:close'),

  getState: () => ipcRenderer.invoke('state:get'),
  token: (accountId: string) => ipcRenderer.invoke('account:token', accountId),

  // accounts
  addAccount: () => ipcRenderer.invoke('auth:add'),
  signOut: (id: string) => ipcRenderer.invoke('auth:signOut', id),
  /** The browser sign-in again, for the same server — after the server stopped
   *  accepting this account's token. Keeps the account's folders. */
  reconnect: (id: string) => ipcRenderer.invoke('auth:reconnect', id),
  switchAccount: (id: string) => ipcRenderer.invoke('auth:switch', id),
  /** Opens the SERVER's admin panel in the system browser, not in here. */
  openAdmin: (id: string) => ipcRenderer.invoke('account:openAdmin', id),

  // files
  /** Host-owned open: open a remote file in its OWN editor/viewer window, one
   *  per document. The explorer emits `file-opened` and the page calls this. */
  openDoc: (accountId: string, remote: string) =>
    ipcRenderer.invoke('doc:open', accountId, remote),
  storages: (accountId: string) => ipcRenderer.invoke('remote:storages', accountId),
  /** The server's own logo + name (Branding settings), for the account rail. */
  branding: (accountId: string) => ipcRenderer.invoke('remote:branding', accountId),
  /** #47 — ⌘K across accounts: search / download on ANOTHER account's server,
   *  with the credential the main process keeps for it (never handed here). */
  searchAccount: (accountId: string, query: string, opts?: { limit?: number; scope?: string }) =>
    ipcRenderer.invoke('remote:search', accountId, query, opts),
  downloadRemote: (accountId: string, remote: string) =>
    ipcRenderer.invoke('remote:download', accountId, remote),

  // sync
  browse: (accountId: string, remotePath: string) =>
    ipcRenderer.invoke('remote:browse', accountId, remotePath),
  addSync: (remotePath: string) => ipcRenderer.invoke('sync:add', remotePath),
  removeSync: (id: string) => ipcRenderer.invoke('sync:remove', id),
  syncTrash: () => ipcRenderer.invoke('sync:trash'),
  /** Items the engine holds for a decision: send them, or move them to this
   *  computer's sync trash (the main process asks first). */
  syncHoldUpload: (pairId: string) => ipcRenderer.invoke('sync:holdUpload', pairId),
  syncHoldDiscard: (pairId: string) => ipcRenderer.invoke('sync:holdDiscard', pairId),
  openLocal: (p: string) => ipcRenderer.invoke('shell:openPath', p),

  // app settings
  setSettings: (patch: unknown) => ipcRenderer.invoke('settings:set', patch),

  // updates — the app keeps itself current; these are the manual affordances.
  checkUpdate: () => ipcRenderer.invoke('update:check'),
  installUpdate: () => ipcRenderer.invoke('update:install'),
  // Only meaningful on a macOS build that cannot swap itself (ad-hoc seal):
  // opens the feed's dmg in the browser instead of pretending to self-update.
  downloadUpdate: () => ipcRenderer.invoke('update:download'),
  // The Store build only: its login item is switched in Windows Settings.
  openStartupSettings: () => ipcRenderer.invoke('login:osSettings'),
  // The Store build only: where the filex.sh copy is removed.
  openAppsSettings: () => ipcRenderer.invoke('app:osAppsSettings'),

  /** Backs the navigator.share polyfill the page installs. See main.ts. */
  share: (data: unknown) => ipcRenderer.invoke('app:share', data),

  // Transfers happen in a background process and accounts can change from the
  // tray, so the page is told when to repaint rather than only being right at
  // the moment it opened.
  onChanged: (fn: () => void) => ipcRenderer.on('sync:changed', () => fn()),

  // selective sync — "keep on this computer". The explorer component calls
  // these through config.desktopSync; the account id pins which server's
  // pairs are meant, so a rail switch mid-flight cannot cross wires.
  syncKept: (accountId: string) => ipcRenderer.invoke('sync:kept', accountId),
  syncKeep: (accountId: string, remote: string, isFile?: boolean) => ipcRenderer.invoke('sync:keep', accountId, remote, isFile === true),
  syncUnkeep: (accountId: string, remote: string) => ipcRenderer.invoke('sync:unkeep', accountId, remote),
  syncReveal: (accountId: string, remote: string) => ipcRenderer.invoke('sync:reveal', accountId, remote),
  syncStatus: (accountId: string) => ipcRenderer.invoke('sync:status', accountId),
  setSyncRoot: (accountId: string) => ipcRenderer.invoke('sync:setRoot', accountId),
  onOpenSettings: (fn: () => void) => ipcRenderer.on('app:open-settings', () => fn()),

  // A native notification was clicked. The payload is the ALREADY RESOLVED
  // destination (see src/notifications.ts) — the page navigates to it, it does
  // not work out where "it" is. One resolver, three surfaces.
  onNotificationOpen: (fn: (p: unknown) => void) =>
    ipcRenderer.on('notify:open', (_e, p) => fn(p)),

  // drag-out — handing real files to the OS. Two calls on purpose: the bytes
  // must be on disk before the drag can start (see src/dragout.ts), so the
  // explorer prepares first and only switches to the native drag once this
  // side says ready.
  dragPrepare: (accountId: string, items: unknown) => ipcRenderer.invoke('drag:prepare', accountId, items),
  dragStart: (accountId: string, items: unknown) => ipcRenderer.invoke('drag:start', accountId, items),
  /** The drag ended inside the window — stop waiting for a drop out there. */
  dragCancel: () => ipcRenderer.invoke('drag:cancel'),
  // "Open with filex" — which document types this app can be a handler for,
  // whether the installer registered them, and the one action that can move the
  // OS's own default. Read-only plus a single button, deliberately: nothing
  // here can take a file type over on its own.
  openWith: () => ipcRenderer.invoke('openwith:state'),
  openWithSetDefault: () => ipcRenderer.invoke('openwith:setDefault'),

  // "Mount as a drive" (WebDAV) — attach this account's server (or one storage)
  // as an OS drive, and detach it. The token never crosses this bridge: the main
  // process reads it from the account and hands it to the OS mounter on stdin
  // (src/drive.ts).
  driveState: () => ipcRenderer.invoke('drive:state'),
  driveMount: (accountId: string, storage?: string) => ipcRenderer.invoke('drive:mount', accountId, storage),
  driveUnmount: (accountId: string, storage?: string) => ipcRenderer.invoke('drive:unmount', accountId, storage),

  /** The log file's path — for a bug report, and for Settings to reveal. */
  logPath: () => ipcRenderer.invoke('app:logPath'),
  onDragProgress: (fn: (p: unknown) => void) =>
    ipcRenderer.on('drag:progress', (_e, p) => fn(p)),
});
