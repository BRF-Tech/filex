import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router';
import { useAuthStore } from '@/stores/auth';
import { stashDesktopHandoff } from '@/lib/desktopHandoff';
import { applyDocumentTitle } from '@/lib/documentTitle';

import AdminLayout from '@/components/AdminLayout.vue';

// The SPA is served from TWO prefixes (backend/internal/api/routes.go →
// wireStatic). Same bundle, same routes; only the address bar differs.
//
//   /admin/  the operator's front door — unchanged, every old bookmark works
//   /drive/  the end-user's front door
//
// Reported as GitHub #14: a non-admin who signed in landed on /admin/explore
// with the whole file manager open and no admin chrome, and was still told
// three times before reaching a file that this was an administrator's tool —
// by the URL, by the tab title and by the login form. The product was right;
// the signposting was not.
export const ADMIN_BASE = '/admin/';
export const USER_BASE = '/drive/';

// Which prefix served THIS document. Read once, at module load, because that
// is exactly what vue-router's history base has to be: the base is baked into
// every push, so a router booted on /admin/ can never produce a /drive/ URL.
// The existing /files/edit carve-out proves it — a browser sent to the bare
// /files/edit has its address rewritten to /admin/files/edit the moment the
// router hydrates (measured 2026-09-04, before this change).
const mountBase =
  typeof window !== 'undefined' &&
  (window.location.pathname === '/drive' || window.location.pathname.startsWith(USER_BASE))
    ? USER_BASE
    : ADMIN_BASE;

/** True when this document was served from the end-user prefix. */
export function onUserBase(): boolean {
  return mountBase === USER_BASE;
}

const routes: RouteRecordRaw[] = [
  {
    // Two front doors, two answers: /admin/ opens the panel, /drive/ opens
    // the files. Everything below this is shared between them.
    path: '/',
    redirect: () => (onUserBase() ? { name: 'explore' } : { name: 'dashboard' }),
  },
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/Login.vue'),
    meta: { public: true, layout: 'blank' },
  },
  {
    // Demo "Filex'i göster" lands here. No admin chrome — just the
    // FileExplorer Web Component. `public: true` lets unauthenticated
    // visitors see the page; the explorer itself returns 401 from
    // /api endpoints, so the demo flow auto-logs-in first.
    path: '/explore',
    name: 'explore',
    component: () => import('@/views/Explore.vue'),
    meta: { public: true, layout: 'blank' },
  },
  {
    // Standalone editor — the SFC's "Aç" / double-click opens this in
    // a new tab. Reads `?path=<adapter>://<rel>&type=<ext>&mode=edit`
    // from the URL and mounts the right viewer fullscreen with
    // save-on-change. No admin chrome.
    path: '/files/edit',
    name: 'files.edit',
    component: () => import('@/views/Editor.vue'),
    meta: { layout: 'blank' },
  },
  {
    path: '/',
    component: AdminLayout,
    // The whole admin panel is admin-only. Non-admin (user/viewer) accounts
    // are redirected to the chrome-less /explore by the guard below. Enforcement
    // is backend-side (every /api/admin/* route checks the role); this is the
    // cosmetic navigation gate so non-admins never see the panel shell.
    meta: { requiresAdmin: true },
    children: [
      {
        path: 'dashboard',
        name: 'dashboard',
        component: () => import('@/views/Dashboard.vue'),
        meta: { breadcrumb: 'nav.dashboard' },
      },
      {
        // The shared connections surface — the same component the desktop
        // app mounts as <filex-connections>. Storages.vue below stays the
        // operational console (sync runs, drift, RBAC); this is the
        // connect-and-instructions half of the same subject.
        path: 'connections',
        name: 'connections',
        component: () => import('@/views/Connections.vue'),
        meta: { breadcrumb: 'nav.connections' },
      },
      {
        path: 'storages',
        name: 'storages',
        component: () => import('@/views/Storages.vue'),
        meta: { breadcrumb: 'nav.storages' },
      },
      {
        path: 'storages/new',
        name: 'storages.new',
        component: () => import('@/views/StorageNew.vue'),
        meta: { breadcrumb: 'storages.newTitle', parent: 'storages' },
      },
      {
        path: 'storages/:id',
        name: 'storages.edit',
        component: () => import('@/views/StorageEdit.vue'),
        meta: { breadcrumb: 'storages.editTitle', parent: 'storages' },
      },
      {
        path: 'users',
        name: 'users',
        component: () => import('@/views/Users.vue'),
        meta: { breadcrumb: 'nav.users' },
      },
      {
        path: 'users/:id',
        name: 'users.edit',
        component: () => import('@/views/UserEdit.vue'),
        meta: { breadcrumb: 'users.editTitle', parent: 'users' },
      },
      {
        path: 'profile',
        name: 'profile',
        component: () => import('@/views/Profile.vue'),
        meta: { breadcrumb: 'nav.profile' },
      },
      {
        path: 'settings',
        name: 'settings',
        component: () => import('@/views/Settings.vue'),
        meta: { breadcrumb: 'nav.settings' },
      },
      {
        // wiring:e1 — settings-driven branding (public pages + login).
        path: 'branding',
        name: 'branding',
        component: () => import('@/views/Branding.vue'),
        meta: { breadcrumb: 'nav.branding' },
      },
      {
        path: 'external',
        name: 'external',
        component: () => import('@/views/External.vue'),
        meta: { breadcrumb: 'nav.external' },
      },
      {
        path: 'auth-providers',
        name: 'auth-providers',
        component: () => import('@/views/AuthProviders.vue'),
        meta: { breadcrumb: 'nav.authProviders' },
      },
      {
        path: 'api-mcp',
        name: 'api-mcp',
        component: () => import('@/views/ApiMcp.vue'),
        meta: { breadcrumb: 'nav.apiMcp' },
      },
      {
        path: 'updates',
        name: 'updates',
        component: () => import('@/views/Updates.vue'),
        meta: { breadcrumb: 'nav.updates' },
      },
      {
        path: 'grants',
        name: 'grants',
        component: () => import('@/views/AdminGrants.vue'),
        meta: { breadcrumb: 'nav.grants' },
      },
      {
        path: 'audit',
        name: 'audit',
        component: () => import('@/views/Audit.vue'),
        meta: { breadcrumb: 'nav.audit' },
      },
      {
        path: 'sync',
        name: 'sync',
        component: () => import('@/views/Sync.vue'),
        meta: { breadcrumb: 'nav.sync' },
      },
      {
        path: 'shares',
        name: 'shares',
        component: () => import('@/views/Shares.vue'),
        meta: { breadcrumb: 'nav.shares' },
      },
      {
        path: 'trash',
        name: 'trash',
        component: () => import('@/views/Trash.vue'),
        meta: { breadcrumb: 'nav.trash' },
      },
      {
        // koru:k3 — data-protection settings (trash retention, version
        // policy, antivirus status).
        path: 'protection',
        name: 'protection',
        component: () => import('@/views/Protection.vue'),
        meta: { breadcrumb: 'nav.protection' },
      },
      {
        path: 'search',
        name: 'search',
        component: () => import('@/views/SearchTest.vue'),
        meta: { breadcrumb: 'nav.search' },
      },
      {
        // bul:s3 — duplicate-files report (read-only).
        path: 'duplicates',
        name: 'duplicates',
        component: () => import('@/views/Duplicates.vue'),
        meta: { breadcrumb: 'nav.duplicates' },
      },
      {
        path: 'tagged',
        name: 'tagged',
        component: () => import('@/views/TaggedFiles.vue'),
        meta: { breadcrumb: 'nav.tagged' },
      },
      {
        path: 'replica',
        name: 'replica',
        component: () => import('@/views/Replica.vue'),
        meta: { breadcrumb: 'nav.replica' },
      },
      {
        path: 'queue',
        name: 'queue',
        component: () => import('@/views/Queue.vue'),
        meta: { breadcrumb: 'nav.queue' },
      },
      {
        path: 'notifications',
        name: 'notifications',
        component: () => import('@/views/Notifications.vue'),
        meta: { breadcrumb: 'nav.notifications' },
      },
      {
        // Storage plugins - drivers that live outside the binary. Sits
        // next to the other instance-wide settings; storages ON a plugin
        // are created from Connections like any other driver's.
        path: 'plugins',
        name: 'plugins',
        component: () => import('@/views/Plugins.vue'),
        meta: { breadcrumb: 'nav.plugins' },
      },
      {
        // bag:b3 — webhook v2 target CRUD (multi-destination, signed).
        path: 'webhooks',
        name: 'webhooks',
        component: () => import('@/views/Webhooks.vue'),
        meta: { breadcrumb: 'nav.webhooks' },
      },
      {
        path: 'about',
        name: 'about',
        component: () => import('@/views/About.vue'),
        meta: { breadcrumb: 'nav.about' },
      },
      {
        // Lookup page → routes to per-node version history. See
        // AdminFiles.vue for the rationale (SFC context menu can't be
        // extended from the embedder).
        path: 'files',
        name: 'admin-files',
        component: () => import('@/views/AdminFiles.vue'),
        meta: { breadcrumb: 'nav.adminFiles' },
      },
      {
        path: 'files/:nodeId/versions',
        name: 'files.versions',
        component: () => import('@/views/FileVersions.vue'),
        meta: { breadcrumb: 'versions.title', parent: 'admin-files' },
      },
    ],
  },
  {
    // Catch-all so unknown URLs don't 404 inside the SPA.
    path: '/:pathMatch(.*)*',
    redirect: () => (onUserBase() ? { name: 'explore' } : { name: 'dashboard' }),
  },
];

const router = createRouter({
  // Whichever prefix served this document. Vite's build `base` stays '/admin/'
  // — asset URLs are absolute, so the same index.html works from either mount.
  history: createWebHistory(mountBase),
  routes,
  scrollBehavior(_to, _from, saved) {
    return saved ?? { top: 0 };
  },
});

router.beforeEach(async (to) => {
  const auth = useAuthStore();

  // ⚠ Desktop pairing params must be stashed HERE, not only in the login
  // view. A browser that already has a session never mounts Login.vue — the
  // dashboard redirect below fires first and destroys the query string, so
  // the desktop app sat on its waiting screen forever and the browser showed
  // a file manager with no code and no error. Measured 2026-08-18: pairing
  // only ever worked from a browser with no session. Stashing before the
  // first await also beats App.vue's post-fetchMe hasPendingHandoff() check.
  if (to.name === 'login') {
    stashDesktopHandoff(to.query.desktop_state, to.query.desktop_challenge);
  }

  // Hydrate session on cold-load before guarding.
  if (!auth.ready) {
    await auth.fetchMe();
  }

  if (to.meta.public) {
    // Already signed-in users shouldn't see /login.
    if (to.name === 'login' && auth.isAuthenticated) {
      return { name: 'dashboard' };
    }
    return true;
  }

  if (!auth.isAuthenticated) {
    return { name: 'login', query: { redirect: to.fullPath } };
  }

  // Admin-panel routes are admin-only. Non-admin accounts (user/viewer) get
  // the explorer instead — they never see the panel chrome.
  if (to.meta.requiresAdmin && !auth.isAdmin) {
    if (!onUserBase()) {
      // ⚠ A real navigation, not a router redirect. vue-router prefixes every
      // push with the base it booted on, so `return { name: 'explore' }` from
      // an /admin/ document lands the user on /admin/explore — the exact URL
      // GitHub #14 is about. Reloading is safe for the flows that cross this
      // line: a desktop pairing is stashed in sessionStorage a few lines up
      // and sessionStorage survives a same-tab navigation (measured), and the
      // explorer's remembered folder lives in localStorage.
      window.location.replace(`${USER_BASE}explore`);
      return false;
    }
    return { name: 'explore' };
  }

  return true;
});

// The tab used to read "filex Admin" on every route — see lib/documentTitle.
router.afterEach((to) => {
  applyDocumentTitle(to);
});

export default router;
