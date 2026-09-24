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

import { brandName, loadBrandName, onBrandName } from '@/lib/brand';
import { t } from '@/i18n';

let lastRoute: RouteLocationNormalized | null = null;

// ⚠ ONE source for the instance's name (`lib/brand`), shared with the
// notification toast and the login wordmark. This file used to fetch and
// cache it privately, which is how the tab could read "Acme Files" while a
// notification from the same page still said "filex".
onBrandName(() => {
  if (lastRoute) applyDocumentTitle(lastRoute);
});

export function applyDocumentTitle(to: RouteLocationNormalized): void {
  lastRoute = to;
  // The standalone editor/viewer route (a tab opened on ONE file) names the
  // DOCUMENT, not the instance — the same rule the desktop's document windows
  // follow. Without this the tab read the Branding name ("BRF Teknoloji") over
  // whatever file was open. Falls back to the instance name when there is no
  // path (a bare /files/edit).
  if (to.name === 'files.edit') {
    const raw = typeof to.query.path === 'string' ? to.query.path : '';
    const base = raw.split('/').filter(Boolean).pop() ?? '';
    document.title = base || brandName();
    loadBrandName();
    return;
  }
  // `requiresAdmin` is the panel's own marker (router/index.ts), so this stays
  // correct when a route is added: a new admin page is admin-flavoured because
  // it is admin-gated, not because someone remembered to add it to a list.
  document.title = to.meta.requiresAdmin
    ? t('title.admin', { name: brandName() })
    : brandName();
  loadBrandName();
}
