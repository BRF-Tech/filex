// What a browser needs before it offers to install filex, held to the files
// that carry it (task #190).
//
// Chrome fires `beforeinstallprompt` - and draws its own install offer - only
// for a page served over HTTPS whose manifest has a name, a start address, an
// app display mode and a 192 px and a 512 px icon (web.dev "What does it take
// to be installable?", https://web.dev/articles/install-criteria); its
// automatic offer still wants a worker that answers `fetch`
// (https://developer.chrome.com/blog/update-install-criteria). An iPhone
// takes its Home Screen icon from `apple-touch-icon` and has no SVG for it.
// A manifest that quietly loses one of these is an app nobody is offered, and
// nothing on screen says why - so each is pinned here, against the real
// files: the manifest and the worker's options (web/pwa.config.ts, what
// vite.config.ts hands vite-plugin-pwa), the icons in web/public, the page
// (web/index.html) and the worker's own handlers (web/public/notify-sw.js).
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { describe, expect, it, vi } from 'vitest';

import { PWA_INCLUDE_ASSETS, PWA_MANIFEST, PWA_WORKBOX } from '../../pwa.config';

const WEB = path.resolve(__dirname, '../..');
const PUBLIC = path.join(WEB, 'public');
const INDEX = readFileSync(path.join(WEB, 'index.html'), 'utf8');

const PNG_MAGIC = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);

/** A PNG's real size, from its IHDR chunk. */
function pngSize(file: string): { width: number; height: number } {
  const b = readFileSync(file);
  expect(b.subarray(0, 8).equals(PNG_MAGIC), `${file} is a PNG`).toBe(true);
  return { width: b.readUInt32BE(16), height: b.readUInt32BE(20) };
}

const icons = PWA_MANIFEST.icons ?? [];
const purposes = (i: (typeof icons)[number]) => String(i.purpose ?? 'any').split(/\s+/);

describe('the manifest names an app a browser will install', () => {
  it('a name, a start address inside its scope, and an app display mode', () => {
    expect(PWA_MANIFEST.name?.trim()).toBeTruthy();
    expect(PWA_MANIFEST.short_name?.trim()).toBeTruthy();
    expect(PWA_MANIFEST.start_url).toBe('/admin/');
    // `standalone`: the installed app opens in a window of its own, with no
    // address bar - what the owner asked for (#190, 2026-10-06). Any of
    // fullscreen / standalone / minimal-ui would satisfy Chrome's criteria.
    expect(PWA_MANIFEST.display).toBe('standalone');
    // A related native app would make Chrome offer THAT instead.
    expect(PWA_MANIFEST.prefer_related_applications ?? false).toBe(false);
    // The scope keeps the start page AND the non-admin's front door in the
    // installed window (GitHub #14).
    expect(PWA_MANIFEST.start_url?.startsWith(PWA_MANIFEST.scope ?? '')).toBe(true);
    expect('/drive/explore'.startsWith(PWA_MANIFEST.scope ?? '')).toBe(true);
  });

  it('keeps its identity: every install made so far is this id', () => {
    expect(PWA_MANIFEST.id).toBe('/admin/');
  });

  it('a 192 px and a 512 px PNG, really those sizes, and a maskable one', () => {
    const pngs = icons.filter((i) => i.type === 'image/png');
    for (const i of pngs) {
      const [w, h] = String(i.sizes).split('x').map(Number);
      expect(pngSize(path.join(PUBLIC, i.src)), i.src).toEqual({ width: w, height: h });
    }
    const any = pngs.filter((i) => purposes(i).includes('any')).map((i) => i.sizes);
    expect(any).toContain('192x192');
    expect(any).toContain('512x512');
    expect(pngs.some((i) => purposes(i).includes('maskable'))).toBe(true);
  });

  it('every icon it names exists and is precached with the app', () => {
    for (const i of icons) {
      expect(existsSync(path.join(PUBLIC, i.src)), i.src).toBe(true);
      expect(PWA_INCLUDE_ASSETS, i.src).toContain(i.src);
    }
  });

  it('the page and the manifest paint the same title bar', () => {
    const meta = /<meta name="theme-color" content="([^"]+)"/.exec(INDEX)?.[1];
    expect(meta).toBe(PWA_MANIFEST.theme_color);
  });
});

describe('an iPhone gets the mark on its Home Screen', () => {
  it('index.html links an apple-touch-icon: a PNG of at least 180 px, under /admin/', () => {
    const href = /<link rel="apple-touch-icon" href="([^"]+)"/.exec(INDEX)?.[1];
    expect(href, 'no <link rel="apple-touch-icon"> in web/index.html').toBeTruthy();
    // Absolute under /admin/ - the server rewrites exactly that prefix under
    // a base path (backend/internal/api/spa_shell.go), and the same document
    // is served at /drive/ and /s/ where a relative path would 404.
    expect(href!.startsWith('/admin/')).toBe(true);
    const { width, height } = pngSize(path.join(PUBLIC, href!.slice('/admin/'.length)));
    expect(width).toBe(height);
    expect(width).toBeGreaterThanOrEqual(180);
  });
});

describe('the generated worker answers fetch', () => {
  it('precaches the app shell and answers navigations from it - never the API', () => {
    // workbox's precache route IS the worker's `fetch` listener; with nothing
    // to precache, or no navigation fallback, there would be none to speak of.
    expect(PWA_WORKBOX.globPatterns?.some((g) => /\bhtml\b/.test(g))).toBe(true);
    expect(PWA_WORKBOX.navigateFallback).toBe('index.html');
    const deny = PWA_WORKBOX.navigateFallbackDenylist ?? [];
    expect(deny.some((r) => r.test('/api/files/list'))).toBe(true);
    expect(deny.some((r) => r.test('/admin/explore'))).toBe(false);
  });

  it('pulls in the notification handlers, which exist', () => {
    expect(PWA_WORKBOX.importScripts).toContain('notify-sw.js');
    expect(existsSync(path.join(PUBLIC, 'notify-sw.js'))).toBe(true);
  });
});

/* ── the worker's own handlers, run as a worker would run them ─────────── */

interface FakeWindow {
  url: string;
  focus: ReturnType<typeof vi.fn>;
  navigate: ReturnType<typeof vi.fn>;
}

function bootWorker(windows: FakeWindow[] = []) {
  const listeners: Record<string, (e: unknown) => void> = {};
  const shown: Array<{ title: string; options: Record<string, unknown> }> = [];
  const opened: string[] = [];
  const self = {
    location: { href: 'https://files.example/admin/sw.js', origin: 'https://files.example' },
    addEventListener: (type: string, fn: (e: unknown) => void) => {
      listeners[type] = fn;
    },
    registration: {
      showNotification: async (title: string, options: Record<string, unknown>) => {
        shown.push({ title, options });
      },
    },
    clients: {
      matchAll: async () => windows,
      openWindow: async (url: string) => {
        opened.push(url);
      },
    },
  };
  vm.runInNewContext(readFileSync(path.join(PUBLIC, 'notify-sw.js'), 'utf8'), { self, URL, Promise });
  /** Dispatch an event and wait for what it handed `waitUntil`. */
  async function fire(type: string, init: Record<string, unknown>) {
    const pending: Promise<unknown>[] = [];
    listeners[type]({ ...init, waitUntil: (p: Promise<unknown>) => pending.push(p) });
    await Promise.all(pending);
  }
  return { listeners, shown, opened, fire };
}

describe('notify-sw.js: what the installed app does with a push and a tap', () => {
  it('a push raises the app’s own toast, with a logo the browser can decode', async () => {
    const w = bootWorker();
    await w.fire('push', {
      data: { json: () => ({ title: 'Acme Files', body: 'report.pdf', tag: 'n-7', url: '/drive/explore#s/Docs' }) },
    });
    expect(w.shown).toHaveLength(1);
    expect(w.shown[0].title).toBe('Acme Files');
    expect(w.shown[0].options).toMatchObject({
      body: 'report.pdf',
      icon: 'https://files.example/admin/icons/icon-192.png',
      badge: 'https://files.example/admin/icons/badge-96.png',
      tag: 'n-7',
      renotify: true,
      data: { url: '/drive/explore#s/Docs' },
    });
  });

  it('a push with no title still says whose it is', async () => {
    const w = bootWorker();
    await w.fire('push', { data: { json: () => ({ body: 'x' }) } });
    expect(w.shown[0].title).toBe('filex');
  });

  it('a tap reuses the open window and sends it where the toast points', async () => {
    const tab: FakeWindow = {
      url: 'https://files.example/drive/explore',
      focus: vi.fn(async () => undefined),
      navigate: vi.fn(async () => undefined),
    };
    const w = bootWorker([tab]);
    const close = vi.fn();
    await w.fire('notificationclick', { notification: { close, data: { url: '/drive/explore#s/Docs' } } });
    expect(close).toHaveBeenCalled();
    expect(tab.focus).toHaveBeenCalled();
    expect(tab.navigate).toHaveBeenCalledWith('https://files.example/drive/explore#s/Docs');
    expect(w.opened).toEqual([]);
  });

  it('with no window open, a tap opens one', async () => {
    const w = bootWorker([]);
    await w.fire('notificationclick', { notification: { close: vi.fn(), data: { url: '/drive/explore' } } });
    expect(w.opened).toEqual(['https://files.example/drive/explore']);
  });
});
