// The phone's install offer reaches the screen (task #190).
//
// ⚠⚠ Before #190 a phone was shown nothing. The offer wore the corner chip
// on every page but the sign-in one, and on a phone's file list that corner is
// the upload button's (`.fe-fab`, bottom-right, 56 px) with the rows' controls
// reaching both edges - so `lib/keepClear` stepped the chip aside, correctly,
// every time. Chrome's own install bar had already been suppressed for it
// (`preventDefault`), so the offer simply did not exist on a phone.
//
// On the file list (`home`, `explore`) the phone's offer now wears the
// sign-in page's band, and the list makes room for it (Explore.vue reserves
// `--filex-install-banner-h`); everywhere else it keeps the chip, and a PC's
// desktop-app offer keeps the chip on the file list too.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

vi.mock('@/lib/serviceWorker', async () => {
  const { ref } = await import('vue');
  return {
    registerAppServiceWorker: vi.fn(() => ({
      needRefresh: ref(false),
      updateServiceWorker: vi.fn(async () => {}),
    })),
    swLocation: () => ({ url: '/admin/sw.js', scope: '/admin/' }),
  };
});

// #219: the installation's name, as GET /api/branding answers it, and the
// manifest the browser installs from, as the server writes it.
const branding = vi.hoisted(() => ({ name: '', manifest: null as unknown, manifestAsked: [] as string[] }));
vi.mock('@/api/branding', () => ({
  BrandingApi: { boot: vi.fn(async () => ({ name: branding.name })) },
}));

import InstallPrompt from '@/components/InstallPrompt.vue';
import { __resetInstallPrompt, captureInstallPrompt } from '@/composables/useInstallPrompt';
import { __resetBrand, loadBrandName } from '@/lib/brand';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const UA = {
  android:
    'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Mobile Safari/537.36',
  iPhone:
    'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1',
  windows:
    'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36',
};

const Blank = { template: '<div />' };

async function mountAt(at: string, locale = 'en') {
  const router = createRouter({
    history: createMemoryHistory('/admin/'),
    routes: [
      { path: '/', name: 'home', component: Blank },
      { path: '/explore', name: 'explore', component: Blank },
      { path: '/login', name: 'login', component: Blank },
      { path: '/my-shares', name: 'my-shares', component: Blank },
    ],
  });
  await router.push(at);
  await router.isReady();
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(InstallPrompt, { global: { plugins: [router, i18n] }, attachTo: document.body });
  await flushPromises();
  return w;
}

function offerInstall() {
  const e = new Event('beforeinstallprompt', { cancelable: true });
  Object.assign(e, {
    platforms: ['web'],
    prompt: vi.fn(async () => {}),
    userChoice: Promise.resolve({ outcome: 'accepted', platform: 'web' }),
  });
  window.dispatchEvent(e);
}

beforeEach(() => {
  __resetInstallPrompt();
  vi.stubGlobal('matchMedia', (q: string) => ({
    matches: false,
    media: q,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  }));
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      branding.manifestAsked.push(String(url));
      return branding.manifest
        ? { ok: true, json: async () => branding.manifest }
        : { ok: false, json: async () => null };
    }),
  );
});

afterEach(() => {
  __resetInstallPrompt();
  __resetBrand();
  branding.name = '';
  branding.manifest = null;
  branding.manifestAsked = [];
  vi.unstubAllGlobals();
});

const banner = (w: Awaited<ReturnType<typeof mountAt>>) => w.find('[data-testid="pwa-install-banner"]');

describe("a phone's install offer on the file list", () => {
  it.each(['/explore', '/'])('Android, %s: the band with the Install button, not a chip', async (at) => {
    vi.stubGlobal('navigator', { userAgent: UA.android, platform: 'Linux armv8l' });
    captureInstallPrompt();
    offerInstall();
    const w = await mountAt(at);
    expect(banner(w).exists()).toBe(true);
    expect(banner(w).attributes('data-place')).toBe('band');
    expect(w.find('[data-testid="pwa-install-button"]').exists()).toBe(true);
    // A band is not a disclosure: nothing to open first.
    expect(w.find('[data-testid="pwa-install-toggle"]').exists()).toBe(false);
  });

  it('Android before the browser offers: the band says where its menu has it', async () => {
    vi.stubGlobal('navigator', { userAgent: UA.android, platform: 'Linux armv8l' });
    const w = await mountAt('/explore');
    expect(banner(w).attributes('data-place')).toBe('band');
    expect(w.find('[data-testid="pwa-menu-instructions"]').text()).toContain('Install app');
    expect(w.find('[data-testid="pwa-install-button"]').exists()).toBe(false);
    offerInstall();
    await flushPromises();
    expect(w.find('[data-testid="pwa-install-button"]').exists()).toBe(true);
    expect(w.find('[data-testid="pwa-menu-instructions"]').exists()).toBe(false);
  });

  it('iPhone: the band with the Add to Home Screen help', async () => {
    vi.stubGlobal('navigator', { userAgent: UA.iPhone, platform: 'iPhone' });
    const w = await mountAt('/explore');
    expect(banner(w).attributes('data-place')).toBe('band');
    expect(w.find('[data-testid="pwa-ios-instructions"]').text()).toContain('Add to Home Screen');
    expect(w.find('[data-testid="pwa-install-button"]').exists()).toBe(false);
  });

  it('elsewhere the same offer keeps the corner chip', async () => {
    vi.stubGlobal('navigator', { userAgent: UA.android, platform: 'Linux armv8l' });
    captureInstallPrompt();
    offerInstall();
    const w = await mountAt('/my-shares');
    expect(banner(w).exists()).toBe(true);
    expect(banner(w).attributes('data-place')).not.toBe('band');
  });

  it("a PC's desktop-app offer keeps its chip on the file list", async () => {
    vi.stubGlobal('navigator', { userAgent: UA.windows, platform: 'Win32' });
    const w = await mountAt('/explore');
    expect(banner(w).exists()).toBe(true);
    expect(banner(w).attributes('data-place')).not.toBe('band');
    expect(w.find('[data-testid="pwa-install-toggle"]').exists()).toBe(true);
  });

  it('the installed app: no band, on the file list or on the sign-in page', async () => {
    vi.stubGlobal('matchMedia', (q: string) => ({
      matches: q === '(display-mode: standalone)',
      media: q,
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => false,
    }));
    vi.stubGlobal('navigator', { userAgent: UA.android, platform: 'Linux armv8l' });
    captureInstallPrompt();
    offerInstall();
    for (const at of ['/explore', '/login']) {
      const w = await mountAt(at);
      expect(banner(w).exists(), at).toBe(false);
      w.unmount();
    }
  });

  it('the file list makes room for the band, so it covers neither the upload button nor a row', () => {
    const src = readFileSync(path.resolve(__dirname, '../../src/views/Explore.vue'), 'utf8');
    expect(src).toMatch(/padding-block-end:\s*var\(--filex-install-banner-h,\s*0px\)/);
    expect(src).toMatch(/class="h-screen[^"]*\bfx-explore-shell\b/);
  });
});

describe("a phone's sign-in page", () => {
  it('wears the band a PC’s sign-in page wears, offering the web app, never the desktop app', async () => {
    // ⚠ The owner's ruling (2026-10-06): "ekran altı bandımız var ya, aynısı
    // olsun" - one band, the same component and the same shell.
    vi.stubGlobal('navigator', { userAgent: UA.android, platform: 'Linux armv8l' });
    captureInstallPrompt();
    offerInstall();
    const phone = await mountAt('/login');
    expect(banner(phone).attributes('data-place')).toBe('band');
    expect(banner(phone).classes()).toContain('ip-dock--band');
    expect(phone.find('.ip-card--install').exists()).toBe(true);
    expect(phone.find('[data-testid="pwa-install-button"]').exists()).toBe(true);
    expect(phone.find('[data-testid="desktop-download-button"]').exists()).toBe(false);
    expect(phone.text()).toContain('Install filex as a web app');
    phone.unmount();

    __resetInstallPrompt();
    vi.stubGlobal('navigator', { userAgent: UA.windows, platform: 'Win32' });
    const pc = await mountAt('/login');
    expect(banner(pc).attributes('data-place')).toBe('band');
    expect(banner(pc).classes()).toContain('ip-dock--band');
    expect(pc.find('.ip-card--install').exists()).toBe(true);
    expect(pc.find('[data-testid="desktop-download-button"]').exists()).toBe(true);
    expect(pc.find('[data-testid="pwa-install-button"]').exists()).toBe(false);
  });
});

/* #219 - the band names the app the home screen will show.
 *
 * ⚠ 0.56 made the installed app wear the installation's brand (spa_brand.go:
 * the manifest's name, the icons, the first paint), but the band offering the
 * install still read the catalogue's constant "Install filex as a web app" on
 * an instance called "BRF Teknoloji". The name comes from lib/brand (GET
 * /api/branding, the same BrandingSource the manifest is written from). */
describe('the band names the installation', () => {
  const title = (w: Awaited<ReturnType<typeof mountAt>>) => w.find('.ip-title').text();

  it('a branded installation: its own name, repainted when the answer lands', async () => {
    vi.stubGlobal('navigator', { userAgent: UA.android, platform: 'Linux armv8l' });
    captureInstallPrompt();
    offerInstall();
    branding.name = 'BRF Teknoloji';
    const w = await mountAt('/login');
    // Painted before the answer: the product's own name, never a blank.
    expect(title(w)).toBe('Install filex as a web app');
    loadBrandName();
    await flushPromises();
    expect(title(w)).toBe('Install BRF Teknoloji as a web app');
    w.unmount();
  });

  it('an installation nobody named: filex', async () => {
    vi.stubGlobal('navigator', { userAgent: UA.android, platform: 'Linux armv8l' });
    captureInstallPrompt();
    offerInstall();
    loadBrandName();
    await flushPromises();
    const w = await mountAt('/explore');
    expect(title(w)).toBe('Install filex as a web app');
    w.unmount();
  });

  it('speaks Turkish with Turkish characters, and no suffix glued to the name', async () => {
    vi.stubGlobal('navigator', { userAgent: UA.iPhone, platform: 'iPhone' });
    captureInstallPrompt();
    branding.name = 'BRF Teknoloji';
    loadBrandName();
    await flushPromises();
    const w = await mountAt('/login', 'tr');
    expect(title(w)).toBe('BRF Teknoloji web uygulamasını kurun');
    w.unmount();
  });
});

/* #219 - and the mark beside the offer is the icon the home screen will get.
 *
 * ⚠ The band showed filex's mark beside "Install BRF Teknoloji". It now shows
 * the manifest's own first `any` icon - the one the server draws from the logo
 * (spa_brand.go), whether the logo was given as a data: URI or as an address -
 * so the offer and the home screen cannot disagree. The manifests below are
 * shaped the way the server writes them in each case. */
describe('the band shows the icon the home screen will get', () => {
  const at = (p: string) => new URL(p, window.location.href).href;
  const rasterBrand = (hash: string) => ({
    name: 'BRF Teknoloji',
    icons: [
      { src: `brand-icons/${hash}-192.png`, sizes: '192x192', type: 'image/png', purpose: 'any' },
      { src: `brand-icons/${hash}-512.png`, sizes: '512x512', type: 'image/png', purpose: 'any' },
      { src: `brand-icons/${hash}-maskable-192.png`, sizes: '192x192', type: 'image/png', purpose: 'maskable' },
      { src: `brand-icons/${hash}-maskable-512.png`, sizes: '512x512', type: 'image/png', purpose: 'maskable' },
    ],
  });
  const unbranded = {
    name: 'filex - File Manager',
    icons: [
      { src: 'icons/icon.svg', sizes: 'any', type: 'image/svg+xml', purpose: 'any' },
      { src: 'icons/icon.svg', sizes: 'any', type: 'image/svg+xml', purpose: 'maskable' },
      { src: 'icons/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
    ],
  };
  const mark = (w: Awaited<ReturnType<typeof mountAt>>) => w.find('[data-testid="pwa-install-mark"]');

  async function phoneBand(manifest: unknown) {
    vi.stubGlobal('navigator', { userAgent: UA.android, platform: 'Linux armv8l' });
    captureInstallPrompt();
    offerInstall();
    branding.manifest = manifest;
    return mountAt('/login');
  }

  it('a logo given as a data: URI: the manifest’s icon drawn from it', async () => {
    const w = await phoneBand(rasterBrand('d4a7a0e1c2b3f405'));
    expect(branding.manifestAsked).toEqual([at('/admin/manifest.webmanifest')]);
    expect(mark(w).attributes('src')).toBe(at('/admin/brand-icons/d4a7a0e1c2b3f405-192.png'));
    w.unmount();
  });

  it('a logo given as an address: the same, the icon the server drew from what it fetched', async () => {
    const w = await phoneBand(rasterBrand('0e11a9b8c7d6e5f4'));
    expect(mark(w).attributes('src')).toBe(at('/admin/brand-icons/0e11a9b8c7d6e5f4-192.png'));
    w.unmount();
  });

  it('nothing branded: filex’s mark, because that is what the manifest leads with', async () => {
    const w = await phoneBand(unbranded);
    expect(mark(w).attributes('src')).toBe(at('/admin/icons/icon.svg'));
    w.unmount();
  });

  it('a manifest that cannot be read: filex’s mark, never an empty box for good', async () => {
    const w = await phoneBand(null);
    expect(mark(w).attributes('src')).toBe('/admin/icons/icon.svg');
    w.unmount();
  });

  it('a PC’s desktop-app offer keeps filex’s mark: that download is filex', async () => {
    vi.stubGlobal('navigator', { userAgent: UA.windows, platform: 'Win32' });
    branding.manifest = rasterBrand('d4a7a0e1c2b3f405');
    const w = await mountAt('/login');
    expect(w.find('[data-testid="desktop-download-button"]').exists()).toBe(true);
    expect(mark(w).attributes('src')).toBe('/admin/icons/icon.svg');
    w.unmount();
  });
});
