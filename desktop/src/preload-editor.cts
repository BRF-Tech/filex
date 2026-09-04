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
import { contextBridge } from 'electron';

contextBridge.exposeInMainWorld('filexDesktop', true);
