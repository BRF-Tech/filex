// The app's service worker: where it lives, and registering it in the
// "prompt" update flow (a new deploy asks to reload rather than being served
// stale from the cache — useInstallPrompt).
//
// ⚠⚠ Why this is not `useRegisterSW` from `virtual:pwa-register/vue` any more.
// That module has the worker's address BAKED IN at build time
// (`new Workbox("/admin/sw.js", { scope: "/admin/" })`), and the build no
// longer knows its address: filex can be served under a sub-path
// (FILEX_BASE_PATH, https://example.com/filex/) and the same bundle runs at the
// root and under any base (web/vite.config.ts). Under a base that registration
// asked the host's root for /admin/sw.js — somebody else's server — so the
// app had no worker, no offline shell and no update prompt. The address is
// taken at RUNTIME here, from the base the server published to the document.
//
// The flow below is vite-plugin-pwa's own prompt flow (registerType
// 'prompt', src/client/build/register.ts), kept event for event so the root
// deployment behaves exactly as it did: same URL, same scope, same events.
import { ref, type Ref } from 'vue';
import { withAppBase } from '@brftech/filex-core';

/**
 * The worker's script and the scope it controls — `/admin/sw.js` and
 * `/admin/` at the root, the same under the base path otherwise.
 *
 * ⚠ The scope stays the panel's (`/admin/`), never the whole base: the worker
 * owns the offline shell for the app and must not claim `/drive/` or the
 * public link pages (vite.config.ts, the `scope` note).
 */
export function swLocation(): { url: string; scope: string } {
  return { url: withAppBase('/admin/sw.js'), scope: withAppBase('/admin/') };
}

/** The subset of workbox-window's events this flow reads. */
interface WbEvent {
  isUpdate?: boolean;
  isExternal?: boolean;
}

interface WorkboxLike {
  addEventListener(type: string, fn: (e: WbEvent) => void): void;
  register(opts?: { immediate?: boolean }): Promise<ServiceWorkerRegistration | undefined>;
  messageSkipWaiting(): void;
}

export interface AppServiceWorker {
  /** True once a new version is installed and waiting for a reload. */
  needRefresh: Ref<boolean>;
  /** Activate the waiting version; the page reloads when it takes over. */
  updateServiceWorker: (reloadPage?: boolean) => Promise<void>;
}

export interface RegisterOptions {
  onRegisteredSW?: (url: string, registration?: ServiceWorkerRegistration) => void;
  onRegisterError?: (err: unknown) => void;
  /** Test seam: builds the Workbox instance. Default: workbox-window. */
  createWorkbox?: (url: string, scope: string) => Promise<WorkboxLike>;
  /** Test seam: whether this browser has service workers at all. */
  supported?: () => boolean;
  /** Test seam: what a takeover by the new version does. */
  reload?: () => void;
}

/** Register the app's worker in the prompt flow. */
export function registerAppServiceWorker(opts: RegisterOptions = {}): AppServiceWorker {
  const needRefresh = ref(false);
  const supported = opts.supported ?? (() => typeof navigator !== 'undefined' && 'serviceWorker' in navigator);
  const reload = opts.reload ?? (() => window.location.reload());
  const create =
    opts.createWorkbox ??
    (async (url: string, scope: string) => {
      const { Workbox } = await import('workbox-window');
      return new Workbox(url, { scope, type: 'classic' }) as unknown as WorkboxLike;
    });

  let wb: WorkboxLike | undefined;

  async function register(): Promise<void> {
    if (!supported()) return;
    const { url, scope } = swLocation();
    try {
      wb = await create(url, scope);
    } catch (e) {
      opts.onRegisterError?.(e);
      return;
    }
    const promptForReload = () => {
      wb?.addEventListener('controlling', (event) => {
        if (event.isUpdate) reload();
      });
      needRefresh.value = true;
    };
    // (The plugin's "offline ready" branches are left out: nothing here
    // listens for that.)
    wb.addEventListener('installed', (event) => {
      // A worker installed by ANOTHER tab of this app is waiting too.
      if (typeof event.isUpdate === 'undefined') {
        if (typeof event.isExternal !== 'undefined') {
          if (event.isExternal) promptForReload();
        } else if (event.isExternal) {
          reload();
        }
      }
    });
    wb.addEventListener('waiting', promptForReload);
    wb.addEventListener('externalwaiting', promptForReload);
    try {
      const reg = await wb.register({ immediate: true });
      opts.onRegisteredSW?.(url, reg);
    } catch (e) {
      opts.onRegisterError?.(e);
    }
  }

  const registering = register();

  return {
    needRefresh,
    async updateServiceWorker() {
      await registering;
      wb?.messageSkipWaiting();
    },
  };
}
