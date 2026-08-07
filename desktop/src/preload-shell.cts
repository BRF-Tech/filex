// Preload for the pre-login window (connect + waiting screens).
//
// This surface exists to get an account signed in and nothing else — accounts,
// sync folders and app settings live in the app window itself. The bridge is
// therefore tiny, and it NEVER exposes a token: the renderer only ever sees
// email + server + ids.
import { contextBridge, ipcRenderer } from 'electron';

contextBridge.exposeInMainWorld('filexShell', {
  getState: () => ipcRenderer.invoke('state:get'),
  beginAuth: (serverUrl: string) => ipcRenderer.invoke('auth:begin', serverUrl),
  completeManual: (code: string) => ipcRenderer.invoke('auth:completeManual', code),
  __testDeepLink: (url: string) => ipcRenderer.invoke('test:deepLink', url),
});
