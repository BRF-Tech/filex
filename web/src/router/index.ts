import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router';
import { useAuthStore } from '@/stores/auth';

import AdminLayout from '@/components/AdminLayout.vue';

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    redirect: { name: 'dashboard' },
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
    redirect: { name: 'dashboard' },
  },
];

const router = createRouter({
  // Vite serves the SPA from /admin/, so the router base mirrors the build base.
  history: createWebHistory('/admin/'),
  routes,
  scrollBehavior(_to, _from, saved) {
    return saved ?? { top: 0 };
  },
});

router.beforeEach(async (to) => {
  const auth = useAuthStore();

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
    return { name: 'explore' };
  }

  return true;
});

export default router;
