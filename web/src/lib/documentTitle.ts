// Per-route document title.
//
// web/index.html hardcoded `<title>filex Admin</title>` and nothing ever
// changed it, so the browser tab said "Admin" on the login form every user of
// the instance sees and on the explorer a non-admin is redirected to. That was
// one of the three things GitHub #14 counted before a user reached a file.
//
// The rule: admin-panel routes say Admin, everything else is just the
// instance's name. The name is the operator's own when they set one on the
// Branding page (wiring:e1), so a deployment called "Acme Files" does not
// advertise filex in the tab either.

import type { RouteLocationNormalized } from 'vue-router';

import { BrandingApi } from '@/api/branding';
import { t } from '@/i18n';

const DEFAULT_NAME = 'filex';

let instanceName = DEFAULT_NAME;
let brandingRequested = false;
let lastRoute: RouteLocationNormalized | null = null;

/**
 * Fetch the instance name once per page load. Best-effort and non-blocking:
 * the title is set immediately from the default and re-applied if a branded
 * name arrives, because a title that waits on a network round-trip is a title
 * the user watches change.
 */
async function loadInstanceName(): Promise<void> {
  if (brandingRequested) return;
  brandingRequested = true;
  try {
    const branding = await BrandingApi.get();
    const name = branding?.name?.trim();
    if (name && name !== instanceName) {
      instanceName = name;
      if (lastRoute) applyDocumentTitle(lastRoute);
    }
  } catch {
    /* /api/branding is public but optional — the default name is fine. */
  }
}

export function applyDocumentTitle(to: RouteLocationNormalized): void {
  lastRoute = to;
  // `requiresAdmin` is the panel's own marker (router/index.ts), so this stays
  // correct when a route is added: a new admin page is admin-flavoured because
  // it is admin-gated, not because someone remembered to add it to a list.
  document.title = to.meta.requiresAdmin
    ? t('title.admin', { name: instanceName })
    : instanceName;
  void loadInstanceName();
}
