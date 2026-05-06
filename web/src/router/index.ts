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
    path: '/',
    component: AdminLayout,
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
        path: 'about',
        name: 'about',
        component: () => import('@/views/About.vue'),
        meta: { breadcrumb: 'nav.about' },
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

  return true;
});

export default router;
