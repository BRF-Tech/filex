// A sub-path deployment (FILEX_BASE_PATH, https://example.com/filex/) as the
// browser side sees it.
//
// The server tells the document its base with `<meta name="filex-base">` in
// the index.html it serves (backend/internal/api/spa_shell.go); at the root
// there is no tag. Everything the app builds for the browser — the router's
// history, the API base, the service worker's address, the notification
// icons, a share link, the sign-out door — reads it through ONE helper (the
// core's lib/appBase), and with no tag every answer is the one the app gave
// before the feature existed.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  APP_BASE_META,
  appBase,
  readAppBase,
  underApiBase,
  withAppBase,
} from '@brftech/filex-core/src/lib/appBase';
import { pickMountBase, ADMIN_BASE, USER_BASE, SHARE_BASE, REQUEST_BASE } from '@/router';
import { signInPage } from '@/lib/signOut';
import { swLocation, registerAppServiceWorker } from '@/lib/serviceWorker';
import { brandBadgeUrl, brandIconUrl } from '@/lib/brand';
import { shareHref, notificationHref } from '@/lib/notificationTarget';
import { fallbackShareUrl } from '@/lib/shareLink';

function setBase(content: string | null): void {
  document.head.querySelectorAll(`meta[name="${APP_BASE_META}"]`).forEach((m) => m.remove());
  if (content === null) return;
  const m = document.createElement('meta');
  m.setAttribute('name', APP_BASE_META);
  m.setAttribute('content', content);
  document.head.appendChild(m);
}

async function freshConfig() {
  vi.resetModules();
  return await import('@/api/runtimeConfig');
}

afterEach(() => setBase(null));

describe('appBase — the one reader of the base the server published', () => {
  it('is empty at the root, where the server adds no tag', () => {
    setBase(null);
    expect(appBase()).toBe('');
    expect(withAppBase('/files/edit')).toBe('/files/edit');
  });

  it('is the tag under a base', () => {
    setBase('/filex');
    expect(appBase()).toBe('/filex');
    expect(withAppBase('/api/files/archive/list')).toBe('/filex/api/files/archive/list');
    setBase('/apps/filex');
    expect(appBase()).toBe('/apps/filex');
  });

  it('refuses a tag that is not a base the server could have written', () => {
    for (const bad of ['filex', '/filex/', '//evil.example', 'https://evil.example', '/a/../b', '/fi lex', '/filex?x', '']) {
      setBase(bad);
      expect(appBase(), bad).toBe('');
    }
    expect(readAppBase(null)).toBe(appBase());
  });
});

describe('underApiBase — a server-root-relative address under an API base', () => {
  it('keeps the base path (new URL(path, base) would drop it)', () => {
    expect(underApiBase('/filex', '/z/abc')).toBe('/filex/z/abc');
    expect(underApiBase('https://example.com/filex', '/z/abc')).toBe('https://example.com/filex/z/abc');
    expect(underApiBase('https://host.example/files-proxy/', '/api/files/thumb/7?sig=x')).toBe(
      'https://host.example/files-proxy/api/files/thumb/7?sig=x',
    );
  });

  it('leaves the root deployment and absolute addresses as they were', () => {
    expect(underApiBase('', '/z/abc')).toBe('/z/abc');
    expect(underApiBase(undefined, '/z/abc')).toBe('/z/abc');
    expect(underApiBase('/filex', 'https://cdn.example/z/abc')).toBe('https://cdn.example/z/abc');
  });
});

describe('router — which door served this document', () => {
  it('reads the door below the base', () => {
    expect(pickMountBase('/filex/drive/explore', '/filex')).toBe(USER_BASE);
    expect(pickMountBase('/filex/drive', '/filex')).toBe(USER_BASE);
    expect(pickMountBase('/filex/admin/storages/3', '/filex')).toBe(ADMIN_BASE);
    expect(pickMountBase('/filex/s/tok', '/filex')).toBe(SHARE_BASE);
    expect(pickMountBase('/filex/d/tok', '/filex')).toBe(REQUEST_BASE);
    expect(pickMountBase('/filex/files/edit', '/filex')).toBe(ADMIN_BASE);
  });

  it('is unchanged at the root', () => {
    expect(pickMountBase('/drive/explore', '')).toBe(USER_BASE);
    expect(pickMountBase('/s/tok', '')).toBe(SHARE_BASE);
    expect(pickMountBase('/admin/', '')).toBe(ADMIN_BASE);
  });

  it('a base named like a door is still only the base', () => {
    // FILEX_BASE_PATH=/drive is legal; the door below it is what counts.
    expect(pickMountBase('/drive/admin/users', '/drive')).toBe(ADMIN_BASE);
    expect(pickMountBase('/drive/drive/', '/drive')).toBe(USER_BASE);
  });
});

describe('API base — the same-origin default follows the base', () => {
  beforeEach(() => {
    delete (window as unknown as { __FILEX_RUNTIME__?: unknown }).__FILEX_RUNTIME__;
  });

  it('is /api at the root, exactly as before', async () => {
    setBase(null);
    const cfg = await freshConfig();
    expect(cfg.getApiBaseUrl()).toBe('/api');
    expect(cfg.getServerRoot()).toBe('');
  });

  it('is <base>/api under a base, read at request time', async () => {
    setBase('/filex');
    const cfg = await freshConfig();
    expect(cfg.getApiBaseUrl()).toBe('/filex/api');
    expect(cfg.getServerRoot()).toBe('/filex');
    cfg.setApiBaseUrl('');
    expect(cfg.getApiBaseUrl()).toBe('/filex/api');
  });

  it('an injected base (the desktop app) wins and keeps its path', async () => {
    setBase(null);
    (window as unknown as { __FILEX_RUNTIME__?: unknown }).__FILEX_RUNTIME__ = {
      apiBaseUrl: 'https://example.com/filex/api',
    };
    const cfg = await freshConfig();
    cfg.initRuntimeConfig();
    expect(cfg.getServerRoot()).toBe('https://example.com/filex');
    delete (window as unknown as { __FILEX_RUNTIME__?: unknown }).__FILEX_RUNTIME__;
  });
});

describe('addresses the browser follows', () => {
  it('sign-out: the door is read below the base, and the key the server knows stays base-less', () => {
    const r = (base: string) => ({ options: { history: { base } } });
    expect(signInPage(r('/filex/drive'), '/filex')).toBe('/drive/login');
    expect(signInPage(r('/filex/admin'), '/filex')).toBe('/admin/login');
    expect(signInPage(r('/drive'), '')).toBe('/drive/login');
  });

  it('the service worker: same URL and scope at the root, under the base otherwise', () => {
    setBase(null);
    expect(swLocation()).toEqual({ url: '/admin/sw.js', scope: '/admin/' });
    setBase('/filex');
    expect(swLocation()).toEqual({ url: '/filex/admin/sw.js', scope: '/filex/admin/' });
  });

  it('notification icons', () => {
    setBase(null);
    expect(brandIconUrl()).toBe('/admin/icons/icon-192.png');
    expect(brandBadgeUrl()).toBe('/admin/icons/badge-96.png');
    setBase('/filex');
    expect(brandIconUrl()).toBe('/filex/admin/icons/icon-192.png');
    expect(brandBadgeUrl()).toBe('/filex/admin/icons/badge-96.png');
  });

  it('share links built on this side', () => {
    expect(shareHref('t0k')).toBe('/s/t0k');
    expect(shareHref('t0k', '/filex')).toBe('/filex/s/t0k');
    expect(notificationHref({ kind: 'share', token: 't0k' } as never, '/filex/drive/', '/filex')).toBe('/filex/s/t0k');
    setBase('/filex');
    expect(fallbackShareUrl('t0k')).toBe(`${window.location.origin}/filex/s/t0k`);
    setBase(null);
    expect(fallbackShareUrl('t0k')).toBe(`${window.location.origin}/s/t0k`);
  });
});

describe('the service worker registration flow', () => {
  function fakeWorkbox() {
    const handlers: Record<string, Array<(e: { isUpdate?: boolean; isExternal?: boolean }) => void>> = {};
    const wb = {
      created: null as null | { url: string; scope: string },
      skipped: 0,
      addEventListener(type: string, fn: (e: { isUpdate?: boolean; isExternal?: boolean }) => void) {
        (handlers[type] ??= []).push(fn);
      },
      fire(type: string, e: { isUpdate?: boolean; isExternal?: boolean } = {}) {
        for (const fn of handlers[type] ?? []) fn(e);
      },
      register: vi.fn().mockResolvedValue(undefined),
      messageSkipWaiting() {
        wb.skipped++;
      },
    };
    return wb;
  }

  it('registers at the base, prompts on a waiting version, reloads when it takes over', async () => {
    setBase('/filex');
    const wb = fakeWorkbox();
    const reload = vi.fn();
    const registered = vi.fn();
    const sw = registerAppServiceWorker({
      supported: () => true,
      reload,
      onRegisteredSW: registered,
      createWorkbox: async (url, scope) => {
        wb.created = { url, scope };
        return wb;
      },
    });
    await vi.waitFor(() => expect(registered).toHaveBeenCalled());
    expect(wb.created).toEqual({ url: '/filex/admin/sw.js', scope: '/filex/admin/' });
    expect(registered.mock.calls[0][0]).toBe('/filex/admin/sw.js');
    expect(wb.register).toHaveBeenCalledWith({ immediate: true });

    expect(sw.needRefresh.value).toBe(false);
    wb.fire('waiting');
    expect(sw.needRefresh.value).toBe(true);

    await sw.updateServiceWorker(true);
    expect(wb.skipped).toBe(1);
    wb.fire('controlling', { isUpdate: true });
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it('does nothing where there are no service workers', async () => {
    const create = vi.fn();
    const sw = registerAppServiceWorker({ supported: () => false, createWorkbox: create });
    await sw.updateServiceWorker();
    expect(create).not.toHaveBeenCalled();
    expect(sw.needRefresh.value).toBe(false);
  });
});

describe('the modules that fetch on their own at boot', () => {
  // Measured by the sub-path e2e run: account preferences, the language list
  // and the public branding asked the host's root, because an unset base meant
  // '' — the admin app configures them with none.
  it('account preferences, the language list and the public branding ask under the base', async () => {
    setBase('/filex');
    const seen: string[] = [];
    const fetchImpl = (async (url: string) => {
      seen.push(String(url));
      return new Response('{}', { status: 404 });
    }) as unknown as typeof fetch;

    const prefs = await import('@brftech/filex-core/src/lib/prefs');
    prefs.resetPrefs();
    prefs.configurePrefs({ surface: 'web', fetchImpl });
    prefs.rememberSession(true);
    await prefs.hydratePrefs();

    const locales = await import('@brftech/filex-core/src/lib/uiLocales');
    await locales.loadLocales({ fetchImpl });

    const { usePublicBranding } = await import('@brftech/filex-core/src/composables/usePublicBranding');
    await usePublicBranding({ fetchImpl }).load();

    expect(seen.length).toBeGreaterThanOrEqual(3);
    for (const u of seen) expect(u, u).toMatch(/^\/filex\/api\//);
    prefs.resetPrefs();
  });

  it('and at the root exactly where they always did', async () => {
    setBase(null);
    const seen: string[] = [];
    const fetchImpl = (async (url: string) => {
      seen.push(String(url));
      return new Response('{}', { status: 404 });
    }) as unknown as typeof fetch;
    const locales = await import('@brftech/filex-core/src/lib/uiLocales');
    await locales.loadLocales({ fetchImpl });
    expect(seen).toEqual(['/api/public/branding']);
  });
});
