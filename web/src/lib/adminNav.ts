/**
 * The admin panel's navigation, in ONE place: which pages exist, how they are
 * grouped, and who sees which (GitHub #82, 0.51.0).
 *
 * The owner's grouping (2026-10-03): the dashboard as a plain link, then
 * three top entries, each a panel whose sections are columns -
 *
 *   Files & storage     Files · Apps (when an app has a screen) · Storage
 *   People & security   People & access · Security
 *   System              Plugins & integrations · Customization · Maintenance & records
 *
 * The drawing is core's `MegaMenu` (the same component on a wide screen and,
 * as a headed list, in a phone's drawer); this module only says WHAT it
 * draws. `components/AdminNav.vue` binds it to the router and the stores, and
 * `components/Breadcrumbs.vue` reads `adminNavSectionOf` for the trail's
 * section step, so the menu and the trail cannot disagree about where a page
 * lives.
 *
 * ⚠ Every admin page that is not a sub-page (`meta.parent`) or a redirect is
 * here exactly once - web/tests/lib/adminNav.test.ts reads the router to hold
 * that, so a page added to the router without a place in the menu fails a
 * test instead of being unreachable from the panel.
 *
 * ⚠ Addresses are not this module's: it names ROUTES, and the menu links to
 * whatever address the router gives them. Grouping pages never moved one.
 */
import type { Component } from 'vue';
import type { RouteMeta } from 'vue-router';
import {
  Archive,
  ArrowUpCircle,
  BarChart3,
  Bell,
  Blocks,
  Brush,
  Building2,
  Cable,
  Copy,
  Database,
  Fingerprint,
  FolderOpen,
  GitBranch,
  History,
  Info,
  KeyRound,
  LayoutDashboard,
  ListChecks,
  Lock,
  Palette,
  PlugZap,
  RefreshCcw,
  ScrollText,
  Search,
  Settings,
  Share2,
  Shield,
  ShieldAlert,
  ShieldCheck,
  Tag,
  Trash2,
  UserCog,
  Users,
  UsersRound,
  Webhook,
  Wrench,
} from 'lucide-vue-next';
import type { MegaMenuEntry, MegaMenuItem, MegaMenuSection } from '@brftech/filex-core';

import TrashFull from '@/components/icons/TrashFull.vue';

/** One page of the menu. */
export interface AdminNavPage {
  /** The route's name: also the item's id and test hook (`nav-<route>`). */
  route: string;
  /** The label's i18n key - the page's own `nav.*` key, as the trail uses. */
  label: string;
  /** The short line's i18n key. */
  hint: string;
  icon: Component;
  /**
   * Shown only on a multi-tenant install, and only to one of its two kinds
   * of administrator: `tenants` to the platform operator, `tenantSelf` to a
   * tenant's own administrator (docs/TENANT-ADMIN.md).
   */
  only?: 'tenants' | 'tenantSelf';
}

export interface AdminNavSection {
  id: string;
  /** The heading's i18n key. */
  label: string;
  pages: AdminNavPage[];
  /** The installed apps' screens go here (one row per `home` view). */
  apps?: true;
}

export interface AdminNavEntry {
  id: string;
  label: string;
  sections: AdminNavSection[];
}

/** The dashboard: a plain link before the three entries, one click away. */
export const ADMIN_NAV_HOME: AdminNavPage = {
  route: 'dashboard',
  label: 'nav.dashboard',
  hint: 'nav.hint.dashboard',
  icon: LayoutDashboard,
};

export const ADMIN_NAV: AdminNavEntry[] = [
  {
    id: 'files',
    label: 'nav.top.files',
    sections: [
      {
        id: 'files',
        label: 'nav.section.files',
        pages: [
          { route: 'explore', label: 'nav.files', hint: 'nav.hint.files', icon: FolderOpen },
          { route: 'admin-files', label: 'nav.adminFiles', hint: 'nav.hint.adminFiles', icon: History },
          { route: 'shares', label: 'nav.shares', hint: 'nav.hint.shares', icon: Share2 },
          { route: 'trash', label: 'nav.trash', hint: 'nav.hint.trash', icon: Trash2 },
          { route: 'tagged', label: 'nav.tagged', hint: 'nav.hint.tagged', icon: Tag },
          { route: 'duplicates', label: 'nav.duplicates', hint: 'nav.hint.duplicates', icon: Copy },
          { route: 'search', label: 'nav.search', hint: 'nav.hint.search', icon: Search },
        ],
      },
      // An app is a PLACE you go, like a storage - not a setting. The
      // explorer's own navigation panel puts its Apps rows right after the
      // places, and the admin menu keeps them in the same company.
      { id: 'apps', label: 'nav.apps', pages: [], apps: true },
      {
        id: 'storage',
        label: 'nav.section.storage',
        pages: [
          { route: 'storages', label: 'nav.storages', hint: 'nav.hint.storages', icon: Database },
          { route: 'connections', label: 'nav.connections', hint: 'nav.hint.connections', icon: Cable },
          { route: 'sync', label: 'nav.sync', hint: 'nav.hint.sync', icon: RefreshCcw },
          { route: 'replica', label: 'nav.replica', hint: 'nav.hint.replica', icon: GitBranch },
          { route: 'usage', label: 'nav.usage', hint: 'nav.hint.usage', icon: BarChart3 },
        ],
      },
    ],
  },
  {
    id: 'people',
    label: 'nav.top.people',
    sections: [
      {
        id: 'people',
        label: 'nav.section.people',
        pages: [
          { route: 'users', label: 'nav.users', hint: 'nav.hint.users', icon: Users },
          { route: 'groups', label: 'nav.groups', hint: 'nav.hint.groups', icon: UsersRound },
          { route: 'roles', label: 'nav.roles', hint: 'nav.hint.roles', icon: UserCog },
          { route: 'grants', label: 'nav.grants', hint: 'nav.hint.grants', icon: ShieldCheck },
          { route: 'tenants', label: 'nav.tenants', hint: 'nav.hint.tenants', icon: Building2, only: 'tenants' },
          { route: 'tenant-self', label: 'nav.myTenant', hint: 'nav.hint.myTenant', icon: Building2, only: 'tenantSelf' },
        ],
      },
      {
        id: 'security',
        label: 'nav.section.security',
        pages: [
          { route: 'auth-providers', label: 'nav.authProviders', hint: 'nav.hint.authProviders', icon: Fingerprint },
          { route: 'login-security', label: 'nav.loginSecurity', hint: 'nav.hint.loginSecurity', icon: ShieldAlert },
          { route: 'encryption', label: 'nav.encryption', hint: 'nav.hint.encryption', icon: Lock },
          { route: 'protection', label: 'nav.protection', hint: 'nav.hint.protection', icon: Shield },
          { route: 'api-mcp', label: 'nav.apiMcp', hint: 'nav.hint.apiMcp', icon: KeyRound },
        ],
      },
    ],
  },
  {
    id: 'system',
    label: 'nav.top.system',
    sections: [
      {
        id: 'integrations',
        label: 'nav.section.integrations',
        pages: [
          { route: 'plugins', label: 'nav.plugins', hint: 'nav.hint.plugins', icon: Blocks },
          { route: 'external', label: 'nav.external', hint: 'nav.hint.external', icon: PlugZap },
          { route: 'webhooks', label: 'nav.webhooks', hint: 'nav.hint.webhooks', icon: Webhook },
          { route: 'notifications', label: 'nav.notifications', hint: 'nav.hint.notifications', icon: Bell },
        ],
      },
      {
        id: 'customize',
        label: 'nav.section.customize',
        pages: [
          { route: 'settings', label: 'nav.settings', hint: 'nav.hint.settings', icon: Settings },
          { route: 'branding', label: 'nav.branding', hint: 'nav.hint.branding', icon: Palette },
          { route: 'appearance', label: 'nav.appearance', hint: 'nav.hint.appearance', icon: Brush },
          { route: 'archives', label: 'nav.archives', hint: 'nav.hint.archives', icon: Archive },
        ],
      },
      {
        id: 'maintenance',
        label: 'nav.section.maintenance',
        pages: [
          { route: 'queue', label: 'nav.queue', hint: 'nav.hint.queue', icon: ListChecks },
          { route: 'tools', label: 'nav.tools', hint: 'nav.hint.tools', icon: Wrench },
          { route: 'audit', label: 'nav.audit', hint: 'nav.hint.audit', icon: ScrollText },
          { route: 'updates', label: 'nav.updates', hint: 'nav.hint.updates', icon: ArrowUpCircle },
          { route: 'about', label: 'nav.about', hint: 'nav.hint.about', icon: Info },
        ],
      },
    ],
  },
];

/** The route every app row opens (`views/AppHome.vue`). */
export const ADMIN_APP_ROUTE = 'admin-app';

/** Every page the menu offers, the dashboard first. */
export function adminNavPages(): AdminNavPage[] {
  return [ADMIN_NAV_HOME, ...ADMIN_NAV.flatMap((e) => e.sections.flatMap((s) => s.pages))];
}

/**
 * Where a page lives in the menu - its entry and its section - or `null`
 * for one the menu does not carry (the dashboard, a public page). An app's
 * screen lives in the Apps section; a sub-page is looked up by its parent.
 */
export function adminNavSectionOf(route: string): { entry: AdminNavEntry; section: AdminNavSection } | null {
  for (const entry of ADMIN_NAV) {
    for (const section of entry.sections) {
      if (route === ADMIN_APP_ROUTE ? section.apps === true : section.pages.some((p) => p.route === route)) {
        return { entry, section };
      }
    }
  }
  return null;
}

/**
 * May this reader open a page with this route meta? The router guard's own
 * question (router/index.ts), asked of the menu so the two cannot disagree:
 * an administrator opens every page; a delegated administrator (an admin.*
 * permission without the role, backend internal/perm) the admin pages that
 * name a permission they hold; anybody the pages outside the admin area.
 */
export function pageOpenTo(meta: RouteMeta, who: { isAdmin: boolean; can: (perm: string) => boolean }): boolean {
  if (who.isAdmin) return true;
  if (!meta.requiresAdmin) return true;
  return typeof meta.adminPerm === 'string' && who.can(meta.adminPerm);
}

/** An installed app's screen, as `composables/usePluginHomeApps` lists them. */
export interface AdminNavApp {
  key: string;
  plugin: string;
  view: string;
  label: string;
  /** A core icon NAME (lib/actionIcons). */
  icon: string;
}

export interface AdminNavContext {
  t: (key: string) => string;
  /**
   * May the reader open this route? (`pageOpenTo` over the route's meta.)
   * Given the whole location, params included: a route with required params
   * (an app's screen) cannot be resolved by its name alone.
   */
  open: (to: { name: string; params?: Record<string, string> }) => boolean;
  /** The address of a route, under whichever prefix the app was opened on. */
  href: (to: { name: string; params?: Record<string, string> }) => string;
  /** The route being looked at. */
  current: { name: string; params: Record<string, unknown>; parent?: string };
  /** A multi-tenant install, read by its platform operator. */
  tenants: boolean;
  /** A multi-tenant install, read by one of its tenants' administrators. */
  tenantSelf: boolean;
  apps: AdminNavApp[];
  /** The trash holds something: its row wears the full bin. */
  trashFull?: boolean;
}

function isCurrent(ctx: AdminNavContext, route: string, params?: Record<string, string>): boolean {
  if (ctx.current.name === route) {
    // ⚠ Several app rows share ONE route and differ only in their params;
    // comparing the name alone would light up every app whenever one is open.
    if (!params) return true;
    return Object.entries(params).every(([k, v]) => String(ctx.current.params[k] ?? '') === v);
  }
  return !params && ctx.current.parent === route;
}

function pageItem(ctx: AdminNavContext, p: AdminNavPage): MegaMenuItem {
  return {
    id: p.route,
    label: ctx.t(p.label),
    hint: ctx.t(p.hint),
    href: ctx.href({ name: p.route }),
    active: isCurrent(ctx, p.route),
    icon: p.route === 'trash' && ctx.trashFull ? TrashFull : p.icon,
  };
}

function pageShown(ctx: AdminNavContext, p: AdminNavPage): boolean {
  if (p.only === 'tenants' && !ctx.tenants) return false;
  if (p.only === 'tenantSelf' && !ctx.tenantSelf) return false;
  return ctx.open({ name: p.route });
}

/**
 * The menu as this reader sees it, for core's `MegaMenu`. Pages the reader
 * may not open are left out here; an emptied section or entry is then left
 * out by the menu itself (core `pruneMegaMenu`).
 */
export function buildAdminNav(ctx: AdminNavContext): MegaMenuEntry[] {
  const out: MegaMenuEntry[] = [];
  if (pageShown(ctx, ADMIN_NAV_HOME)) {
    out.push({
      id: ADMIN_NAV_HOME.route,
      label: ctx.t(ADMIN_NAV_HOME.label),
      href: ctx.href({ name: ADMIN_NAV_HOME.route }),
      active: isCurrent(ctx, ADMIN_NAV_HOME.route),
      icon: ADMIN_NAV_HOME.icon,
    });
  }
  for (const entry of ADMIN_NAV) {
    const sections: MegaMenuSection[] = entry.sections.map((s) => ({
      id: s.id,
      label: ctx.t(s.label),
      items: s.apps
        ? ctx.apps
            .map((a) => ({ a, params: { plugin: a.plugin, view: a.view } }))
            .filter(({ params }) => ctx.open({ name: ADMIN_APP_ROUTE, params }))
            .map(({ a, params }) => ({
              id: `app-${a.key}`,
              label: a.label,
              href: ctx.href({ name: ADMIN_APP_ROUTE, params }),
              active: isCurrent(ctx, ADMIN_APP_ROUTE, params),
              iconName: a.icon,
            }))
        : s.pages.filter((p) => pageShown(ctx, p)).map((p) => pageItem(ctx, p)),
    }));
    out.push({ id: entry.id, label: ctx.t(entry.label), sections });
  }
  return out;
}
