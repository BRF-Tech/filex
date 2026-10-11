// The install offer's mark is the manifest's own icon (#219): lib/brand
// manifestIcon picks it the way a browser reads the manifest, so the band and
// the home screen get the same picture whatever the server drew it from.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises } from '@vue/test-utils';

import { __resetBrand, loadBrandAppIcon, manifestIcon } from '@/lib/brand';
import { brandedNotification } from '@/lib/browserNotify';

const MANIFEST = 'https://files.example.com/admin/manifest.webmanifest';

describe('manifestIcon', () => {
  it('a branded raster logo (data: URI or address): its first any icon, resolved against the manifest', () => {
    const m = {
      icons: [
        { src: 'brand-icons/abc-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
        { src: 'brand-icons/abc-maskable-192.png', sizes: '192x192', type: 'image/png', purpose: 'maskable' },
      ],
    };
    expect(manifestIcon(m, MANIFEST)).toBe('https://files.example.com/admin/brand-icons/abc-192.png');
  });

  it('nothing branded: filex’s own first any icon', () => {
    const m = {
      icons: [
        { src: 'icons/icon.svg', purpose: 'any' },
        { src: 'icons/icon.svg', purpose: 'maskable' },
      ],
    };
    expect(manifestIcon(m, MANIFEST)).toBe('https://files.example.com/admin/icons/icon.svg');
  });

  it('skips a maskable-only icon; a missing purpose is any; "any maskable" counts', () => {
    expect(
      manifestIcon({ icons: [{ src: 'm.png', purpose: 'maskable' }, { src: 'a.png' }] }, MANIFEST),
    ).toBe('https://files.example.com/admin/a.png');
    expect(manifestIcon({ icons: [{ src: 'both.png', purpose: 'any maskable' }] }, MANIFEST)).toBe(
      'https://files.example.com/admin/both.png',
    );
  });

  it('under a base path the icon stays under it', () => {
    expect(
      manifestIcon({ icons: [{ src: 'brand-icons/abc.svg', purpose: 'any' }] }, 'https://h.example/filex/admin/manifest.webmanifest'),
    ).toBe('https://h.example/filex/admin/brand-icons/abc.svg');
  });

  it('a manifest with no usable icon names none', () => {
    expect(manifestIcon(null, MANIFEST)).toBeNull();
    expect(manifestIcon({}, MANIFEST)).toBeNull();
    expect(manifestIcon({ icons: 'x' }, MANIFEST)).toBeNull();
    expect(manifestIcon({ icons: [{ purpose: 'any' }, { src: '', purpose: 'any' }, { src: 'm.png', purpose: 'maskable' }] }, MANIFEST)).toBeNull();
  });
});

// #219 - a notification shows the installed app's icon too: the manifest's
// first PNG `any` icon (Chromium decodes no SVG in a notification), the same
// one the server puts in a Web Push (spa_brand.go pushIcon).
describe('the notification icon is the manifest’s PNG', () => {
  const rasterBrand = {
    icons: [
      { src: 'brand-icons/abc-192.png', type: 'image/png', purpose: 'any' },
      { src: 'brand-icons/abc-maskable-192.png', type: 'image/png', purpose: 'maskable' },
    ],
  };
  const svgBrand = {
    icons: [
      { src: 'brand-icons/abc.svg', sizes: 'any', type: 'image/svg+xml', purpose: 'any' },
      { src: 'icons/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
    ],
  };
  const unbranded = {
    icons: [
      { src: 'icons/icon.svg', type: 'image/svg+xml', purpose: 'any' },
      { src: 'icons/icon.svg', type: 'image/svg+xml', purpose: 'maskable' },
      { src: 'icons/icon-192.png', type: 'image/png', purpose: 'any' },
    ],
  };

  it('picks the first PNG any icon', () => {
    expect(manifestIcon(rasterBrand, MANIFEST, { png: true })).toBe('https://files.example.com/admin/brand-icons/abc-192.png');
    expect(manifestIcon(svgBrand, MANIFEST, { png: true })).toBe('https://files.example.com/admin/icons/icon-192.png');
    expect(manifestIcon(unbranded, MANIFEST, { png: true })).toBe('https://files.example.com/admin/icons/icon-192.png');
  });

  afterEach(() => {
    __resetBrand();
    vi.unstubAllGlobals();
  });

  async function toastIcon(manifest: unknown): Promise<unknown> {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => (manifest ? { ok: true, json: async () => manifest } : { ok: false, json: async () => null })),
    );
    loadBrandAppIcon();
    await flushPromises();
    return brandedNotification({ title: 'New file: report.pdf' }).options.icon;
  }

  it('a branded instance: the logo the server drew', async () => {
    expect(await toastIcon(rasterBrand)).toBe(new URL('/admin/brand-icons/abc-192.png', window.location.href).href);
  });

  it('nothing branded, or an SVG logo: filex’s PNG, as the manifest names it', async () => {
    expect(await toastIcon(unbranded)).toBe(new URL('/admin/icons/icon-192.png', window.location.href).href);
    __resetBrand();
    expect(await toastIcon(svgBrand)).toBe(new URL('/admin/icons/icon-192.png', window.location.href).href);
  });

  it('before the manifest has answered, or when it cannot be read: filex’s PNG', async () => {
    expect(brandedNotification({ title: 'x' }).options.icon).toBe('/admin/icons/icon-192.png');
    expect(await toastIcon(null)).toBe('/admin/icons/icon-192.png');
  });
});
