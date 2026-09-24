import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router';
import { useAuthStore } from '@/stores/auth';
import { stashDesktopHandoff } from '@/lib/desktopHandoff';
import { applyDocumentTitle } from '@/lib/documentTitle';
import { startRouteName } from '@/lib/startPage';

import AdminLayout from '@/components/AdminLayout.vue';

// The SPA is served from several prefixes (backend/internal/api/routes.go →
// wireStatic). Same bundle; only the address bar differs — and under the
// public ones, the routes too.
//
//   /admin/  the operator's front door — unchanged, every old bookmark works
//   /drive/  the end-user's front door
//   /s/      a SHARE, followed by somebody with no account (v3 §1)
//   /d/      a FILE REQUEST, likewise
//   /p/      an app plugin's page — RETIRED by v3 (an app's public page is a
//            share now) and kept only so links already sent still open
//
// ⚠⚠ The last three are the PUBLIC prefixes and they share one route table
// and one component (`views/public/PublicLink.vue` → the package's
// `PublicLinkPage`). Before v3 `/s/` and `/d/` were HTML that Go wrote by
// hand and `/p/` was a Vue screen, which is exactly why the PIN box of a
// signature request looked nothing like the PIN box of a download.
//
// Reported as GitHub #14: a non-admin who signed in landed on /admin/explore
// with the whole file manager open and no admin chrome, and was still told
// three times before reaching a file that this was an administrator's tool —
// by the URL, by the tab title and by the login form. The product was right;
// the signposting was not.
export const ADMIN_BASE = '/admin/';
export const USER_BASE = '/drive/';
export const SHARE_BASE = '/s/';
export const REQUEST_BASE = '/d/';
/**
 * ⚠⚠ RETIRED by v3 §1. An app's public page is a share now, and the SERVER
 * answers `/p/<token>` with a 301 to `/s/<token>` (routes.go →
 * `RetiredPagePrefix`). The constant stays because callers import it; the SPA
 * has no route table for it, because a browser never gets here.
 */
export const PAGE_BASE = '/p/';

/** Which public link a prefix means, or '' for the application's own bases. */
export type PublicKind = 'share' | 'request';
const PUBLIC_BASES: Array<[string, PublicKind]> = [
  [SHARE_BASE, 'share'],
  [REQUEST_BASE, 'request'],
];

// Which prefix served THIS document. Read once, at module load, because that
// is exactly what vue-router's history base has to be: the base is baked into
// every push, so a router booted on /admin/ can never produce a /drive/ URL.
// The existing /files/edit carve-out proves it — a browser sent to the bare
// /files/edit has its address rewritten to /admin/files/edit the moment the
// router hydrates (measured 2026-09-04, before this change).
const mountBase = pickMountBase(typeof window !== 'undefined' ? window.location.pathname : '');

/** Which prefix a document path was served from. Exported for the tests. */
export function pickMountBase(pathname: string): string {
  if (pathname === '/drive' || pathname.startsWith(USER_BASE)) return USER_BASE;
  for (const [base] of PUBLIC_BASES) {
    if (pathname.startsWith(base)) return base;
  }
  return ADMIN_BASE;
}

/** Which kind of public link this document is, or '' when it is the app. */
export function publicKindOf(base: string): PublicKind | '' {
  return PUBLIC_BASES.find(([b]) => b === base)?.[1] ?? '';
}

/** True when this document was served from the end-user prefix. */
export function onUserBase(): boolean {
  return mountBase === USER_BASE;
}

/**
 * The prefix this document was served from (`/admin/`, `/drive/`, `/p/`).
 *
 * ⚠ Read by anything that builds an address the BROWSER will follow rather
 * than a route the router will push — an app plugin's page opens in a new
 * tab, so vue-router cannot prepend the base for it, and `/apps/…` without
 * the base is a server 404 (routes.go → wireStatic serves only these three).
 */
export function currentMountBase(): string {
  return mountBase;
}

/**
 * True when this document is a PUBLIC link of any kind (`/s/`, `/d/`, `/p/`).
 *
 * ⚠ Everything that reads this is asking one question: "is there a session
 * to hydrate and somewhere to redirect to?" The answer for all three is no —
 * a stranger with a link has a token and nothing else, so a 401 here would
 * only buy a sign-in form they cannot use. It kept its old name because
 * every caller means exactly what it says.
 */
export function onPublicPageBase(): boolean {
  return publicKindOf(mountBase) !== '';
}

/** Which public link this document is (`share` | `request` | `page` | ''). */
export function currentPublicKind(): PublicKind | '' {
  return publicKindOf(mountBase);
}

// The public route table. ⚠ Nothing from the application's: no login, no
// home, no admin layout. A visitor here has a token and nothing else, so
// every path is either a link or "not available" — never a redirect into a
// sign-in form for an account they do not have.
//
// ⚠ ONE table for all three prefixes. `kind` is the only difference and it
// comes from the prefix that served the document, so a fourth public link
// would be a row in PUBLIC_BASES rather than a second table.
function publicRoutes(kind: PublicKind): RouteRecordRaw[] {
  return [
    {
      path: '/:token([A-Za-z0-9_-]+)',
      name: 'public-link',
      component: () => import('@/views/public/PublicLink.vue'),
      props: (route) => ({ kind, token: route.params.token }),
      meta: { public: true, layout: 'blank' },
    },
    {
      // A bare prefix, or anything that is not a token: the same view with
      // no token, which draws the "not available" state.
      path: '/:pathMatch(.*)*',
      name: 'public-link-missing',
      component: () => import('@/views/public/PublicLink.vue'),
      props: () => ({ kind, token: '' }),
      meta: { public: true, layout: 'blank' },
    },
  ];
}

const routes: RouteRecordRaw[] = [
  {
    // ⚠ ONE answer, for both front doors and for everybody: Home.
    //
    // Landing inside a folder answers a question nobody asked on arrival
    // ("what is in this particular directory?"); Home answers the three that
    // are: which drives are mine, what was I just working on, what did I mark
    // to come back to. A storage card is one click from the files — the same
    // click the storage list in the explorer's root would have cost.
    //
    // ⚠ The ADMIN gets it too, and that is a change (owner's decision,
    // 2026-09-12). `/admin/` used to open the dashboard, so the product opened
    // on two different screens depending on which URL somebody had saved. An
    // operator who wants the dashboard on launch chooses it in their profile
    // settings — `lib/startPage`, honoured by the guard below, which is where
    // the question can be answered with a hydrated session. The dashboard
    // itself is unchanged and one click away, from the admin button in the
    // explorer's header cluster.
    path: '/',
    redirect: () => ({ name: 'home' }),
  },
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/Login.vue'),
    meta: { public: true, layout: 'blank' },
  },
  {
    // The landing page, for both front doors and every role.
    //
    // ⚠⚠ It renders `Explore.vue` — the SAME component `/explore` renders, not
    // a page of its own. Home is a VIEW of the explorer now (the navigation
    // panel's first row, `packages/core` HomeView.vue), so the two routes
    // differ only by where the explorer opens: this one passes the `.home`
    // sentinel as `initialPath`, `/explore` passes none and restores wherever
    // the person was. `web/src/views/Home.vue` — a separate page with a header
    // of its own, a logo, a refresh, an "All files" button, an apps grid,
    // sign-out and a language switch, and no panel at all — is gone with it.
    //
    // ⚠ NOT `public: true`. /explore below is public because the demo flow
    // sends unauthenticated visitors there and the API answers them 401;
    // Home's three blocks are all per-USER (their storages, their recents,
    // their stars), so an anonymous visitor here has nothing to be shown and
    // the guard sends them to the login form instead of to three empty boxes.
    // No `requiresAdmin`: this route is outside the AdminLayout block, so an
    // ordinary account reaches it.
    path: '/home',
    name: 'home',
    component: () => import('@/views/Explore.vue'),
    meta: { layout: 'blank' },
  },
  {
    // The demo's "Filex'i göster" (Show Filex) button lands here. No admin
    // chrome — just the FileExplorer Web Component. `public: true` lets
    // unauthenticated visitors see the page; the explorer itself returns 401
    // from /api endpoints, so the demo flow auto-logs-in first.
    path: '/explore',
    name: 'explore',
    component: () => import('@/views/Explore.vue'),
    meta: { public: true, layout: 'blank' },
  },
  {
    // An app plugin's `page` view — a wizard with the document beside it,
    // opened in a new tab by the file menu (docs/APP-PLUGINS-API.md →
    // Placements). Reads `?path=<adapter>://<rel>` and draws the SAME
    // surface a `modal` view would, without the dialog.
    //
    // ⚠ It lives under the SPA's mount base, not at the site root: only
    // `/admin/*`, `/drive/*` and `/p/*` fall back to index.html
    // (routes.go → wireStatic), so a bare `/apps/…` is a server 404. The
    // explorer builds the address from `pluginPageBase`, which Explore.vue
    // sets to whichever prefix served the document.
    //
    // ⚠ NOT `public: true`: every call this page makes is an authenticated
    // `/api/files/plugins/…` one, so an anonymous visitor would get a blank
    // screen full of 401s instead of the login form. The outside
    // participant's screen is `/p/<token>`, which is a different thing.
    path: '/apps/:plugin/:view',
    name: 'app-page',
    component: () => import('@/views/AppPage.vue'),
    props: true,
    meta: { layout: 'blank' },
  },
  {
    /**
     * "Paylaştıklarım" / "My shares" — the links THIS person handed out.
     *
     * ⚠⚠ OUTSIDE the AdminLayout block below, and that is the whole point.
     * The panel is admin-only (`requiresAdmin` on its parent) and a non-admin
     * who lands on one of its routes is sent to the end-user front door, so a
     * page for everybody could not live in there. Same route table, both
     * bases: /drive/my-shares and /admin/my-shares.
     *
     * ⚠ NOT `public: true`. Every call it makes is an authenticated
     * `/api/shares` one, so an anonymous visitor here would watch a table
     * 401 instead of being offered the sign-in form.
     *
     * ⚠ The admin's own `shares` route (everybody's links) stays exactly what
     * it is. Two audiences, two screens — see views/MyShares.vue.
     */
    path: '/my-shares',
    name: 'my-shares',
    component: () => import('@/views/MyShares.vue'),
    meta: { layout: 'blank', breadcrumb: 'myShares.title' },
  },
  {
    /**
     * An app plugin's `home` view as a page of ITS OWN, in the same tab —
     * the explorer's "Apps" rows open here (`config.appHomePage` →
     * `open-app-home`). `?section=` is the section of the page on screen
     * (`surface.sections`), so Back walks the sections and a notification
     * can land on one.
     *
     * ⚠⚠ The owner, 2026-09-21: "İmzalar popup açıyor … kendi sayfasını
     * açsın ve her biri ayrı bir menü içinde farklı tablolar göstersin". It
     * is laid out the way My shares is, beside which it sits, and like it
     * it lives OUTSIDE the AdminLayout block: the panel is admin-only, and
     * every person an app offers a home view to must be able to reach it.
     *
     * ⚠ `/app/`, not `/apps/`. `/apps/:plugin/:view` is the new-tab `page`
     * view above and `apps/:plugin/home/:view` the admin panel's own copy
     * inside AdminLayout; two routes on one path do not both work (vue-router
     * scores them alike and the first registered wins).
     *
     * ⚠ NOT `public: true`: every call it makes is an authenticated one.
     */
    path: '/app/:plugin/:view',
    name: 'app-home',
    component: () => import('@/views/AppScreen.vue'),
    meta: { layout: 'blank', breadcrumb: 'nav.apps' },
  },
  {
    // Standalone editor — the SFC's "Open" / double-click opens this in
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
        path: 'usage',
        name: 'usage',
        component: () => import('@/views/Usage.vue'),
        meta: { breadcrumb: 'nav.usage' },
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
        /**
         * gorunum:v2 — the profile PAGE is gone; every field it had is in the
         * user-settings dialog. The ADDRESS stays because the server prints it:
         * the startup banner and `<data>/.first-run.txt` both tell a fresh
         * operator to change their password at /admin/profile, and copies of
         * that file are already on disk in the field. A dialog behind an avatar
         * menu has no address to print, so this one forwards to it.
         */
        path: 'profile',
        name: 'profile',
        redirect: { name: 'dashboard', query: { settings: '1' } },
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
        // tema:v1 — the instance's own themes plus the raw-CSS escape hatch.
        //
        // ⚠⚠ A route of its own, and not a section of Settings, because this
        // is the screen that has to keep working when an operator has pasted a
        // ruinous stylesheet: it suspends that stylesheet while it is open
        // (views/Appearance.vue), and a URL somebody can type is the last
        // resort when the chrome around it has been styled away.
        path: 'appearance',
        name: 'appearance',
        component: () => import('@/views/Appearance.vue'),
        meta: { breadcrumb: 'nav.appearance' },
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
        path: 'archives',
        name: 'archives',
        component: () => import('@/views/Archives.vue'),
        meta: { breadcrumb: 'nav.archives' },
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
        // One installed app, as a PAGE with sections — it was a dialog
        // holding settings, three tables and a live log (views/
        // AppPluginPage.vue says why it moved). Addressed by the app's name.
        //
        // ⚠ Checked against the top-level paths (router lesson): this is
        // `/plugins/apps/:name`, and no top-level route starts with
        // `/plugins`; `/apps/:plugin/:view` (the new-tab page view) and
        // `apps/:plugin/home/:view` below are different paths.
        path: 'plugins/apps/:name',
        name: 'plugins.app',
        component: () => import('@/views/AppPluginPage.vue'),
        meta: { breadcrumb: 'appPlugins.page.breadcrumb', parent: 'plugins' },
      },
      {
        // An app plugin's `home` view, drawn INSIDE the panel — the sidebar's
        // "Apps" section opens one of these. Generic: every installed plugin
        // that ships a `home` view gets a row, nothing here knows a plugin's
        // name (views/AppHome.vue, composables/usePluginHomeApps.ts).
        //
        // ⚠⚠ NOT `apps/:plugin/:view`. That address is already taken, by the
        // chrome-less `page` view this SPA opens in a NEW TAB (the top-level
        // route above, and `PLUGIN_PAGE_SEGMENT` in the package). Two routes
        // with the same path do not both work: vue-router scores them alike
        // and the first one registered wins, so the panel's page would simply
        // never render. The extra `home` segment is the placement's own name,
        // which is exactly what distinguishes the two screens.
        path: 'apps/:plugin/home/:view',
        name: 'admin-app',
        component: () => import('@/views/AppHome.vue'),
        meta: { breadcrumb: 'nav.apps' },
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
    // Catch-all so unknown URLs don't 404 inside the SPA. A URL that means
    // nothing lands on the landing page, same as `/` above — and the same one
    // for both doors, for the same reason.
    path: '/:pathMatch(.*)*',
    redirect: () => ({ name: 'home' }),
  },
];

const router = createRouter({
  // Whichever prefix served this document. Vite's build `base` stays '/admin/'
  // — asset URLs are absolute, so the same index.html works from any mount.
  history: createWebHistory(mountBase),
  routes: onPublicPageBase() ? publicRoutes(currentPublicKind() as PublicKind) : routes,
  scrollBehavior(_to, _from, saved) {
    return saved ?? { top: 0 };
  },
});

router.beforeEach(async (to) => {
  // A public link has no session to hydrate and nowhere to redirect to:
  // asking /api/auth/me here would only cost a 401 (and, once a route has
  // matched, the axios interceptor's push to /login — a form the visitor
  // cannot use). See App.vue, which skips its own boot fetches the same way.
  if (onPublicPageBase()) return true;

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
      // …and they land wherever they chose to land. This is the OTHER front
      // door: a browser with a live session that hits /login never passes
      // through `/`, so naming `dashboard` here would have made the start-page
      // preference apply to some launches and not others.
      return { name: startRouteName({ isAdmin: auth.isAdmin, userBase: onUserBase() }) };
    }
    return true;
  }

  if (!auth.isAuthenticated) {
    // ⚠ `redirectedFrom` FIRST. A visitor who opened the bare front door has
    // already been through the `/` record's redirect by the time this runs, so
    // `to.fullPath` is the door's DEFAULT destination (`/dashboard`), not the
    // door. Remembering that turns "I opened filex" into "I explicitly asked
    // for the dashboard": the post-login push then goes straight there, the
    // start-page preference below never sees a front-door navigation, and on
    // the /drive/ base a non-admin was being sent back to an admin-only route
    // to be bounced off it. Measured 2026-09-12 — `?redirect=/dashboard`.
    const from = to.redirectedFrom?.fullPath ?? to.fullPath;
    return { name: 'login', query: { redirect: from } };
  }

  // ── Start page ──────────────────────────────────────────────────────────
  //
  // The reader for `lib/startPage`. It lives HERE, not on the `/` record's
  // own `redirect`, for one reason: a record redirect is evaluated while the
  // route is being resolved, which is BEFORE `auth.fetchMe()` above has run on
  // a cold load — so `isAdmin` is still false there and an administrator who
  // chose "Admin panel" would be sent to Home on every launch. By this line
  // the session is hydrated and the question can be answered truthfully.
  //
  // ⚠ Above the admin guard below, so a non-admin who chose Files is taken to
  // Files rather than being bounced off `dashboard` first.
  //
  // ⚠ No flash: navigation guards run before any component mounts, so the
  // door's default destination is never painted on the way past.
  //
  // ⚠ `to.redirectedFrom` — only a navigation that actually came through the
  // front door is re-aimed. Typing /admin/dashboard still opens the dashboard;
  // a preference that hijacked explicit URLs would be a trap, not a default.
  // The query string is carried over so deep links through `/` (`?storage=…`)
  // survive the re-aim.
  if (to.redirectedFrom?.path === '/') {
    // ⚠ The door comes first, and only then the room. A non-admin who opened
    // the OPERATOR's prefix belongs on the end-user one — GitHub #14 is about
    // exactly that URL telling an ordinary user they are in an admin tool. The
    // admin guard below does this for admin-only routes; a start page of Files
    // or Home is not admin-only, so without this line a non-admin who saved
    // one would be left sitting on /admin/explore. Measured 2026-09-12.
    if (!auth.isAdmin && !onUserBase()) {
      window.location.replace(USER_BASE);
      return false;
    }
    const want = startRouteName({ isAdmin: auth.isAdmin, userBase: onUserBase() });
    if (want !== to.name) return { name: want, query: to.query, hash: to.hash };
  }

  // Admin-panel routes are admin-only. Non-admin accounts (user/viewer) get
  // their own front door instead — they never see the panel chrome.
  //
  // ⚠ The door, not a room inside it: `USER_BASE` resolves through the `/`
  // redirect above to Home, so signing in and being handed on both end on the
  // same screen. Naming `explore` here instead would mean the ONLY way to see
  // Home was to type /drive/ by hand — the page would exist and nobody would
  // ever arrive on it.
  if (to.meta.requiresAdmin && !auth.isAdmin) {
    if (!onUserBase()) {
      // ⚠ A real navigation, not a router redirect. vue-router prefixes every
      // push with the base it booted on, so `return { name: 'home' }` from
      // an /admin/ document lands the user on /admin/home — the exact kind of
      // URL GitHub #14 is about. Reloading is safe for the flows that cross
      // this line: a desktop pairing is stashed in sessionStorage a few lines
      // up and sessionStorage survives a same-tab navigation (measured), and
      // the explorer's remembered folder lives in localStorage.
      window.location.replace(USER_BASE);
      return false;
    }
    return { name: 'home' };
  }

  return true;
});

// The tab used to read "filex Admin" on every route — see lib/documentTitle.
router.afterEach((to) => {
  applyDocumentTitle(to);
});

export default router;
