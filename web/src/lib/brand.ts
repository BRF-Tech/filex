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

import { BrandingApi } from '@/api/branding';

/** The product's own name, used until (and unless) an operator renames it. */
export const DEFAULT_BRAND_NAME = 'filex';

let name = DEFAULT_BRAND_NAME;
let requested = false;
const listeners = new Set<(n: string) => void>();

/** The instance's name right now — never empty, never a promise. */
export function brandName(): string {
  return name;
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
      if (!next || next === name) return;
      name = next;
      for (const fn of listeners) fn(name);
    })
    .catch(() => {
      /* the default name is a perfectly good answer */
    });
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

/**
 * The mark, as URLs the BROWSER will fetch.
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
 * address that is always right.
 */
export const BRAND_ICON_URL = '/admin/icons/icon-192.png';

/** The Android status-bar mask — monochrome, alpha only. */
export const BRAND_BADGE_URL = '/admin/icons/badge-96.png';
