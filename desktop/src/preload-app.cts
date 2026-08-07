// Preload for the MAIN window (the embedded filex explorer).
//
// Two jobs, both minimal — the attack surface of a preload holding a live token
// must stay tiny:
//   1. Inject the runtime seam BEFORE the web bundle boots, so the app talks to
//      the account's server with its token. window.__FILEX_RUNTIME__ is exactly
//      what web/src/api/runtimeConfig.ts reads on init.
//   2. Expose the desktop-only entry points the web app cannot provide:
//      Settings (accounts) and Sync folders.
import { contextBridge, ipcRenderer } from 'electron';

// Synchronous so the global exists before any app script runs.
const runtime = ipcRenderer.sendSync('session:runtime') as {
  apiBaseUrl?: string;
  bearerToken?: string;
  useCredentials?: boolean;
};

contextBridge.exposeInMainWorld('__FILEX_RUNTIME__', {
  apiBaseUrl: runtime.apiBaseUrl,
  bearerToken: runtime.bearerToken,
  // MUST be false. The renderer lives on `app://`, so every call to the server
  // is cross-origin, and filex answers `Access-Control-Allow-Origin: *` by
  // default. A credentialed request may not be answered with a wildcard origin
  // (Fetch spec), so the browser would reject every response and the app would
  // look completely dead. We authenticate with the bearer token above and have
  // no cookie jar, so dropping credentials costs nothing.
  useCredentials: false,
});

contextBridge.exposeInMainWorld('filexDesktop', {
  isDesktop: true,
  openSettings: () => ipcRenderer.invoke('shell:open', '/settings'),
  openSyncFolders: () => ipcRenderer.invoke('shell:open', '/sync'),
});
