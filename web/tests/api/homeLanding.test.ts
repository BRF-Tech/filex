// Where each front door lands.
//
// ⚠⚠ The mount base is read ONCE, from `window.location`, at module load
// (router/index.ts) — which is precisely why this needs its own test rather
// than a reading of the source: the decision cannot be re-run per navigation
// and cannot be observed from inside a component. Each case below therefore
// puts the window on a prefix and imports the router fresh.
//
// What must stay true:
//   /admin/  → home           ⚠ CHANGED 2026-09-12 (owner's decision). It used
//   /drive/  → home           to be the dashboard, so the product opened on two
//                             different screens depending on which URL somebody
//                             had saved. Home is the landing page for everybody
//                             now; an operator who wants the dashboard on launch
//                             says so in their profile settings, and the case
//                             below proves that choice is still honoured.
// and the `home` route must NOT be public: its three blocks are all per-user,
// so an anonymous visitor has to meet the login form, not three empty boxes.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { RouteRecordNormalized, Router } from 'vue-router';

async function routerOn(path: string): Promise<Router> {
  window.history.replaceState({}, '', path);
  vi.resetModules();
  const mod = await import('@/router');
  return mod.default;
}

/** The record that owns `/` — the one carrying a redirect, not the layout. */
function rootRedirect(router: Router): RouteRecordNormalized {
  const rec = router.getRoutes().find((r) => r.path === '/' && r.redirect);
  if (!rec) throw new Error('no root redirect record');
  return rec;
}

type RedirectFn = (to: unknown) => { name?: string };
const target = (rec: RouteRecordNormalized): string | undefined =>
  (rec.redirect as RedirectFn)({ path: '/', query: {}, hash: '', params: {} }).name;

// ⚠ 20s for every case. `routerOn` is a COLD `import('@/router')` after
// `vi.resetModules()`: the first case pays for transforming the router's whole
// eager graph — which reaches `@brftech/filex-core` (stores/auth →
// lib/timezone, the settings modal's palette grid) and, since #167, the
// capabilities store and `@/i18n` — and every case evaluates it again. The
// first was measured at 4.5s against the default 5s; the later ones run in
// ~450ms alone, but took over 5s in the test chain's full run (2026-10-06,
// next to the Go shards), so the 20s the first case had is now the file's.
const COLD = { timeout: 20_000 };

describe('front doors', COLD, () => {
  beforeEach(() => {
    window.history.replaceState({}, '', '/admin/');
  });

  it('the operator lands on Home from /admin/ too, not on the dashboard', async () => {
    const router = await routerOn('/admin/');
    expect(target(rootRedirect(router))).toBe('home');
  });

  it('the end user lands on Home from /drive/', async () => {
    const router = await routerOn('/drive/');
    expect(target(rootRedirect(router))).toBe('home');
  });

  it('an unknown URL falls back to Home, not to a folder', async () => {
    const router = await routerOn('/drive/nope');
    const catchAll = router.getRoutes().find((r) => r.path.includes('pathMatch'));
    expect(catchAll).toBeTruthy();
    expect(target(catchAll as RouteRecordNormalized)).toBe('home');
  });

  it('home is a real route, authenticated, and outside the admin-only block', async () => {
    const router = await routerOn('/drive/');
    const home = router.getRoutes().find((r) => r.name === 'home');
    expect(home, 'a /home route exists').toBeTruthy();
    expect(home?.path).toBe('/home');
    // Not public: every block on it is per-user.
    expect(home?.meta.public).toBeFalsy();
    // Not admin-gated: it is the NON-admin's landing page.
    expect(home?.meta.requiresAdmin).toBeFalsy();
  });

  it('a saved start page overrides the door default, but only for the door', async () => {
    const { startRouteName } = await import('@/lib/startPage');

    // ⚠ No choice = Home, whoever is asking and whichever door they came
    // through. The door used to decide, which is the thing that changed.
    localStorage.removeItem('filex.startpage');
    expect(startRouteName({ isAdmin: true, userBase: false })).toBe('home');
    expect(startRouteName({ isAdmin: true, userBase: true })).toBe('home');
    expect(startRouteName({ isAdmin: false, userBase: true })).toBe('home');
    expect(startRouteName({ isAdmin: false, userBase: false })).toBe('home');

    // A choice wins over the door — an admin who asked for Files gets Files.
    localStorage.setItem('filex.startpage', 'files');
    expect(startRouteName({ isAdmin: true, userBase: false })).toBe('explore');

    // ⚠ And never hands a non-admin the panel. The router's admin guard would
    // bounce them, so honouring it would mean a redirect on every launch with
    // no visible cause.
    localStorage.setItem('filex.startpage', 'admin');
    expect(startRouteName({ isAdmin: false, userBase: true })).toBe('home');
    expect(startRouteName({ isAdmin: false, userBase: false })).toBe('home');
    localStorage.removeItem('filex.startpage');
  });

  it('the explorer deep link an admin bookmarked is untouched', async () => {
    const router = await routerOn('/admin/');
    const explore = router.resolve({ name: 'explore', query: { storage: 'qldemo' } });
    expect(explore.fullPath).toBe('/explore?storage=qldemo');
  });
});
