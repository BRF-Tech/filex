// Preload for the shell chrome (connect / settings / sync folders).
//
// The surface stays deliberately small and NEVER exposes an account token —
// the renderer only ever sees email + server + ids. Anything that needs the
// credential happens in the main process.
import { contextBridge, ipcRenderer } from 'electron';

contextBridge.exposeInMainWorld('filexShell', {
  getState: () => ipcRenderer.invoke('state:get'),
  beginAuth: (serverUrl: string) => ipcRenderer.invoke('auth:begin', serverUrl),
  completeManual: (code: string) => ipcRenderer.invoke('auth:completeManual', code),
  signOut: (id: string) => ipcRenderer.invoke('auth:signOut', id),
  switchAccount: (id: string) => ipcRenderer.invoke('auth:switch', id),
  setSettings: (patch: unknown) => ipcRenderer.invoke('settings:set', patch),
  addSyncFolder: (remotePath: string) => ipcRenderer.invoke('sync:add', remotePath),
  removeSyncFolder: (id: string) => ipcRenderer.invoke('sync:remove', id),
  toggleSyncFolder: (id: string) => ipcRenderer.invoke('sync:toggle', id),
  __testDeepLink: (url: string) => ipcRenderer.invoke('test:deepLink', url),
});
