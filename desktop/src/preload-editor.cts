// Preload for the EDITOR window — one line, and deliberately one line.
//
// That window shows the SERVER's own `/files/edit` page, which is remote
// content. It gets no `filexApp` bridge: handing the account token and the sync
// engine to whatever an origin serves is the opposite of what the main window's
// narrow preload is careful about. The credential that page needs arrives the
// way every other request in this app gets one — the header injector in
// main.ts.
//
// The single flag is the SPA's own, documented opt-out: `useInstallPrompt`
// reads `window.filexDesktop` and returns null for "which installer to offer"
// because "inside the Electron shell there is nothing to install". Without it
// the desktop app's editor window advertises the desktop app to itself —
// measured 2026-09-04, "Get the filex desktop app" was the first thing on the
// page above the document.
//
// ⚠ It has to be a preload rather than an executeJavaScript after load: the
// prompt decides once, when the Vue app mounts, and by `did-finish-load` that
// has already happened.
import { contextBridge, ipcRenderer } from 'electron';

contextBridge.exposeInMainWorld('filexDesktop', true);

// yeni-pencere:v1 — the document window is frameless (no native caption on
// Windows/Linux), so our injected buttons (main.ts docChromeScript) need a way
// to drive THIS window. This bridge is controls-only — minimize / maximize /
// close, no data and no token — so unlike a `filexApp` bridge it is safe to
// expose to the remote /files/edit page. Each call acts on the window that sent
// it (main.ts win:* handlers via BrowserWindow.fromWebContents).
contextBridge.exposeInMainWorld('filexWin', {
  minimize: () => ipcRenderer.invoke('win:minimize'),
  toggleMaximize: () => ipcRenderer.invoke('win:toggleMaximize'),
  close: () => ipcRenderer.invoke('win:close'),
});

// #184 — the document this window edits changed OUTSIDE filex (an agent, an
// editor, a sync client rewrote it on the computer). The app watches the file
// and tells the page; the page's editor (packages/core PreviewModal) decides:
// nothing unsaved → it loads the new version, else it asks which version
// stays. The page answers here, and says whether its editor holds an edit.
//
// ⚠ Data only, and only about THIS window's document: no path on this
// computer, no token, nothing to read. Each message acts on the session of the
// window that sent it (main.ts, by webContents), and the app ignores it from a
// window that is not an "Open with filex" one. What a page could do with it -
// answer a question about its own document - it can already do by saving.
// Values are copied out of the page's objects, never passed through.
type OutsideMessage = { seq: number; path?: string | null; pending?: boolean };
let outsideListener: ((m: OutsideMessage) => void) | null = null;
ipcRenderer.on('outside:change', (_e, m: OutsideMessage) => {
  if (outsideListener && m && typeof m.seq === 'number') {
    outsideListener({ seq: m.seq, path: typeof m.path === 'string' ? m.path : null, pending: m.pending === true });
  }
});
contextBridge.exposeInMainWorld('filexOutside', {
  /** The page can take outside changes (protocol version). */
  hello: (version: number) => ipcRenderer.send('outside:hello', Number(version) || 0),
  /** Whether the page's editor holds an edit of its own. */
  report: (state: { edited?: boolean }) => ipcRenderer.send('outside:state', { edited: state?.edited === true }),
  /** What became of a change: 'reloaded', or the person's answer. */
  answer: (a: { seq?: number; choice?: string; path?: string }) =>
    ipcRenderer.send('outside:answer', {
      seq: Number(a?.seq) || 0,
      choice: String(a?.choice ?? ''),
      path: typeof a?.path === 'string' ? a.path : '',
    }),
  /** One listener: the page's. A second call replaces the first. */
  on: (cb: (m: OutsideMessage) => void) => {
    outsideListener = typeof cb === 'function' ? cb : null;
  },
});
