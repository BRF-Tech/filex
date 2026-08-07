# filex desktop (Electron shell) — Dilim 2

Wraps the **existing** filex web app in a desktop window. The Vue app is not
rewritten or re-skinned — Electron embeds the built bundle and adds three things
it can't do from a browser tab:

1. **Runs from a real origin, offline.** A custom `app://` protocol serves the
   embedded bundle so `createWebHistory('/admin/')` routing keeps working
   (file:// would break it — see `src/main.ts`).
2. **Native login → durable token.** A small login window collects the server
   address + credentials, mints a **self-service API token** (`POST /api/tokens`)
   and stores it **encrypted** via the OS keychain (`safeStorage`). No cookies
   (Electron is a different origin); no plaintext token on disk.
3. **Injects the runtime seam.** The stored `{serverUrl, token}` is fed to the
   Dilim-1 seam (`window.__FILEX_RUNTIME__`) from the preload before the bundle
   boots, so every request hits the remote server authenticated.

## Layout

| Path | Role |
|------|------|
| `src/main.ts` | Electron main: `app://` protocol, windows, IPC, lifecycle |
| `src/preload-app.ts` | Injects `window.__FILEX_RUNTIME__` + `filexDesktop.logout()` |
| `src/preload-login.ts` | Exposes `filexDesktop.login(server,email,pw)` |
| `src/auth.ts` | login → mint self-service token |
| `src/config-store.ts` | `safeStorage`-encrypted session at `<userData>/session.bin` |
| `login/` | Native login page (static HTML/JS, not Vue) |
| `scripts/sync-web.mjs` | Copies `web/dist` → `app/` for embedding |
| `electron-builder.yml` | Linux / Windows / macOS packaging |

## Build & run

```bash
# 1. Build the web bundle first (needs packages/core built).
pnpm run build:packages && pnpm --filter @brftech/filex-admin build
# 2. Build + run the shell.
pnpm --filter @brftech/filex-desktop dev
# 3. Package installers (unsigned):
pnpm --filter @brftech/filex-desktop dist        # current OS
pnpm --filter @brftech/filex-desktop dist:win    # etc.
```

## Security posture (do not loosen — trap #2)

`contextIsolation: true`, `nodeIntegration: false`, `sandbox: true`. The main
window only ever loads `app://`; external links open in the OS browser; the
preloads expose the narrowest possible surface (login OR logout+runtime).

## Signing

The first release is **unsigned** by design (trap #4). Windows SmartScreen /
macOS Gatekeeper will warn. Code-signing certificates are a separate, paid
decision — not a defect.

## Verification status ⚠️

Written and type-checked; the `app://` + preload-injection + `safeStorage`
plumbing is exercised by `scripts/plumbing-smoke.mjs`. **NOT yet verified on
this workspace:** the full embedded-web run and the 3-platform packaging +
end-to-end login/list/session-persist bar — `packages/core` (monaco/
model-viewer) OOM-kills on this memory-constrained shared host, so the web
bundle can't be built/embedded here, and there is no local backend to log in
against. Those must be run on a capable host / CI (see the epic task #34).
