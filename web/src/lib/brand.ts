// What this installation is CALLED, and what it looks like — once.
//
// ⚠⚠ Why a module rather than a `t('…')` or a constant. An operator can
// rename the instance on the Branding page (`GET /api/branding` → `name`), and
// "filex" is only the fallback. Every surface that says the product's name to
// a person therefore has to ask the same question of the same answer: the tab
// title did (lib/documentTitle), the login wordmark did (views/Login), and
// the notification toast did NOT — it put the server's event sentence in the
// title and left the browser to print the bare origin underneath, which is
// what "the app name does not show up properly" looked like on a real
// machine.
//
// So the name is fetched once per page load, cached, and handed out
// synchronously with the default until the answer lands. Nothing waits on the
// network for a label.
//
// ⚠ It is the same answer the server wears in the installed app (0.56, #219):
// `/api/branding` and the per-request manifest / index.html rewrite
// (backend/internal/api/spa_brand.go) both read handlers.BrandingSource. The
// install band said "Install filex as a web app" on an instance whose home
// screen icon was going to read "BRF Teknoloji" - a constant in the catalogue
// where this module's answer belonged.

import { shallowRef } from 'vue';
import { withAppBase } from '@brftech/filex-core';
import { BrandingApi } from '@/api/branding';

/** The product's own name, used until (and unless) an operator renames it. */
export const DEFAULT_BRAND_NAME = 'filex';

// A ref, so a template or a computed that calls brandName() paints itself
// again when the answer lands (the install band, the settings dialog's host);
// the listeners below are for code outside a render (the tab title).
const name = shallowRef(DEFAULT_BRAND_NAME);
let requested = false;
const listeners = new Set<(n: string) => void>();

/** The instance's name right now — never empty, never a promise. */
export function brandName(): string {
  return name.value;
}

/**
 * Ask the server once, in the background. Safe to call from anywhere and as
 * often as you like; the first caller does the request and the rest are free.
 *
 * ⚠ `/api/branding` is public but optional (an older server has no such
 * route), so a failure is not an error — it is the default name, which is
 * already on screen.
 */
export function loadBrandName(): void {
  if (requested) return;
  requested = true;
  void BrandingApi.boot()
    .then((b) => {
      const next = b?.name?.trim();
      if (!next || next === name.value) return;
      name.value = next;
      for (const fn of listeners) fn(next);
    })
    .catch(() => {
      /* the default name is a perfectly good answer */
    });
}

/** Tests only: forget the answers, so the next load asks again. */
export function __resetBrand(): void {
  name.value = DEFAULT_BRAND_NAME;
  requested = false;
  appIcon.value = null;
  notifyIcon.value = null;
  iconRequested = false;
}

/**
 * Be told when the fetched name arrives (or changes), so a label that was
 * already painted with the default can be repainted rather than watched.
 * Returns the unsubscribe.
 */
export function onBrandName(fn: (n: string) => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/* ── the installed app's icon (#219) ─────────────────────────────────────── */

/** filex's own mark: what a manifest with nothing branded leads with. */
export const filexMarkUrl = (): string => withAppBase('/admin/icons/icon.svg');

const appIcon = shallowRef<string | null>(null);
const notifyIcon = shallowRef<string | null>(null);
let iconRequested = false;

/**
 * The icon the installed app wears on the home screen, as the manifest the
 * browser installs from names it; null until the manifest has answered.
 *
 * ⚠⚠ READ FROM THE MANIFEST, never rebuilt here. The server writes the
 * manifest's icons per request (backend/internal/api/spa_brand.go): a logo
 * given as a data: URI or as an address becomes
 * `brand-icons/<content hash>-….png`, an SVG logo leads as itself, an install
 * with nothing branded leads with filex's mark. A second rule in this file
 * would be an offer showing one picture and a home screen getting another -
 * which is what the band did in 0.56, filex's mark beside "Install BRF
 * Teknoloji".
 */
export function brandAppIcon(): string | null {
  return appIcon.value;
}

/**
 * Ask for the manifest once: the band's mark and the notifications' icon both
 * come from it. filex's own if it cannot be read.
 */
export function loadBrandAppIcon(): void {
  if (iconRequested) return;
  iconRequested = true;
  const href = manifestHref();
  void fetch(href)
    .then((r) => (r.ok ? r.json() : null))
    .then((m: unknown) => {
      appIcon.value = manifestIcon(m, href) ?? filexMarkUrl();
      notifyIcon.value = manifestIcon(m, href, { png: true });
    })
    .catch(() => {
      appIcon.value = filexMarkUrl();
    });
}

/** The manifest the page links (under the base path), else the build's address. */
function manifestHref(): string {
  const link = document.querySelector<HTMLLinkElement>('link[rel="manifest"]');
  if (link?.href) return link.href;
  return new URL(withAppBase('/admin/manifest.webmanifest'), window.location.href).href;
}

/**
 * The manifest's first `any` icon (a missing `purpose` is `any`) - with `png`,
 * its first PNG one - resolved against the manifest's own address the way the
 * browser resolves it: the icons are written relative to it. null when the
 * manifest names none.
 */
export function manifestIcon(manifest: unknown, manifestUrl: string, opts: { png?: boolean } = {}): string | null {
  const icons = (manifest as { icons?: unknown } | null)?.icons;
  if (!Array.isArray(icons)) return null;
  for (const icon of icons) {
    if (!icon || typeof icon !== 'object') continue;
    const { src, purpose, type } = icon as { src?: unknown; purpose?: unknown; type?: unknown };
    if (typeof src !== 'string' || !src) continue;
    const purposes = typeof purpose === 'string' && purpose.trim() ? purpose.trim().split(/\s+/) : ['any'];
    if (!purposes.includes('any')) continue;
    if (opts.png && !(type === 'image/png' || (!type && /\.png$/i.test(src)))) continue;
    try {
      return new URL(src, manifestUrl).href;
    } catch {
      continue;
    }
  }
  return null;
}

/**
 * The mark, as URLs the BROWSER will fetch.
 *
 * ⚠⚠ The ICON is the installed app's (#219): the manifest's first PNG `any`
 * icon once loadBrandAppIcon has read it - the logo the server drew for a
 * branded instance, filex's own PNG otherwise or until then. The same picture
 * a Web Push shows (the server reads it off the same manifest, spa_brand.go
 * pushIcon) and the home screen gets. The badge stays filex's: a status-bar
 * mask is alpha only, and no logo is drawn as one.
 *
 * ⚠⚠ PNG, not the SVG. Chromium decodes a notification's `icon` and `badge`
 * through its image decoders, and SVG is not among them — so the SVG that
 * works everywhere else is, in a notification, no icon at all (Firefox draws
 * it, which is why this went unnoticed). `scripts/make-icon-pngs.mjs`
 * rasterises both from `icons/icon.svg` so there is still one mark.
 *
 * ⚠ Absolute, under `/admin/`: the same bundle is served from `/drive/` and
 * `/p/` too, and a relative icon path resolves against whichever one served
 * the page — `/drive/icons/icon-192.png` is a 404 and a toast with no logo.
 * The assets live in the admin build's public folder, so that is the one
 * address that is always right — under the base path the app is served at
 * (FILEX_BASE_PATH, the core's `withAppBase`), which is '' at the root.
 */
export const brandIconUrl = (): string => notifyIcon.value ?? withAppBase('/admin/icons/icon-192.png');

/** The Android status-bar mask — monochrome, alpha only. */
export const brandBadgeUrl = (): string => withAppBase('/admin/icons/badge-96.png');
