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

import InstallPrompt from '@/components/InstallPrompt.vue';
import { __resetInstallPrompt, captureInstallPrompt } from '@/composables/useInstallPrompt';
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

async function mountAt(at: string) {
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
  const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en, tr } });
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
});

afterEach(() => {
  __resetInstallPrompt();
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
    expect(phone.text()).toContain(en.install.title);
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
