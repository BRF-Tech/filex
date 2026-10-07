/**
 * What the admin panel's search finds by itself, and where each row goes
 * (task #168, docs/ADMIN-PANEL.md → Search).
 *
 * Three kinds of row are made here, from what the panel already knows:
 *
 *   pages     the mega menu's own entries, AS THIS READER SEES THEM
 *             (composables/useAdminNav): a page the menu leaves out for a
 *             delegated administrator or a tenant is not here either. Each
 *             answers to its label in the interface's language, its English
 *             label and its synonyms (`adminSearch.alias.*`: "LDAP" finds
 *             Identity providers), and shows the menu's short line under it.
 *   tabs      a page's tabs that have an address of their own (`?tab=`):
 *             Plugins → Apps, Identity providers → LDAP, Tools → Thumbnail
 *             repair. Offered only when their page is.
 *   settings  single settings by the name their page gives them ("Trash
 *             retention", "Require two-factor authentication"), each sending
 *             to its page. Offered only when their page is.
 *
 * The rest - people, groups, API keys, apps, storages, shares - is the
 * server's answer (GET /api/admin/panel-search), and files are the file
 * index's (GET /api/files/search); `remoteItem` and `fileItem` turn those
 * into rows. Matching and ranking are core's (`lib/panelSearch`).
 */
import { splitList, type MegaMenuEntry, type PanelSearchItem } from '@brftech/filex-core';

import { TOOLS } from '@/components/tools/registry';
import { ADMIN_APP_ROUTE, adminNavPages } from '@/lib/adminNav';

/** Where a row goes. */
export type AdminSearchTarget =
  | { route: { name: string; params?: Record<string, string>; query?: Record<string, string> } }
  | { file: { storage: string; path: string; dir: boolean } };

/** A page's tab with an address of its own. */
export interface AdminSearchTab {
  /** The page's route. */
  route: string;
  /** The `?tab=` value. */
  tab: string;
  /** The tab's label key, the one the page draws. */
  label: string;
}

/**
 * The tabs. ⚠ The page owns the list (views/Plugins.vue and
 * views/AuthProviders.vue `TABS`, components/tools/registry.ts `TOOLS`);
 * web/tests/lib/adminSearch.test.ts reads those sources and fails when this
 * list and theirs part ways.
 */
export const ADMIN_SEARCH_TABS: readonly AdminSearchTab[] = [
  { route: 'plugins', tab: 'storage', label: 'appPlugins.tabs.storage' },
  { route: 'plugins', tab: 'apps', label: 'appPlugins.tabs.apps' },
  { route: 'plugins', tab: 'defaults', label: 'appPlugins.tabs.defaults' },
  { route: 'auth-providers', tab: 'ldap', label: 'authProviders.providers.ldap' },
  { route: 'auth-providers', tab: 'local', label: 'authProviders.providers.local' },
  { route: 'auth-providers', tab: 'oidc', label: 'authProviders.providers.oidc' },
  { route: 'auth-providers', tab: 'proxy-header', label: 'authProviders.providers.proxy-header' },
  { route: 'auth-providers', tab: 'windows', label: 'authProviders.providers.windows' },
  { route: 'auth-providers', tab: 'pam', label: 'authProviders.providers.pam' },
  ...TOOLS.map((tool) => ({ route: 'tools', tab: tool.id, label: `tools.tabs.${tool.id}` })),
];

/** One setting, by the name its page gives it. */
export interface AdminSearchSetting {
  /** Stable id (`setting:<id>`). */
  id: string;
  /** The page it is on. */
  route: string;
  /** The label key the page draws over it. */
  label: string;
  /** More words it answers to (`adminSearch.alias.*`). */
  alias?: string;
  /** The page's tab it is on. */
  query?: Record<string, string>;
}

/**
 * The settings. ⚠ Labels are the pages' own keys, so the search says what
 * the page says; web/tests/lib/adminSearch.test.ts holds every key to both
 * catalogues and every route to the router.
 */
export const ADMIN_SEARCH_SETTINGS: readonly AdminSearchSetting[] = [
  { id: 'site-name', route: 'settings', label: 'settings.siteName' },
  { id: 'folder-view', route: 'settings', label: 'settings.folderView.title' },
  { id: 'smtp', route: 'settings', label: 'settings.smtp.title', alias: 'adminSearch.alias.smtp' },
  { id: 'sign-in-limit', route: 'login-security', label: 'loginSecurity.limit.title' },
  { id: 'allowed-addresses', route: 'login-security', label: 'loginSecurity.allowlist.title', alias: 'adminSearch.alias.allowlist' },
  { id: 'trusted-proxies', route: 'login-security', label: 'loginSecurity.proxies.title' },
  { id: 'sign-in-locks', route: 'login-security', label: 'loginSecurity.locks.title' },
  { id: 'trash-retention', route: 'protection', label: 'protection.trash.title' },
  { id: 'version-policy', route: 'protection', label: 'protection.versions.title' },
  { id: 'share-links', route: 'protection', label: 'protection.share.title' },
  { id: 'drafts', route: 'protection', label: 'protection.drafts.title' },
  { id: 'antivirus', route: 'protection', label: 'protection.av.title' },
  { id: 'require-2fa', route: 'roles', label: 'permissions.rules.require2fa', alias: 'adminSearch.alias.twoFactor' },
  { id: 'link-lifetime', route: 'roles', label: 'permissions.rules.maxDays' },
  { id: 'custom-css', route: 'appearance', label: 'appearance.css.title' },
  { id: 'who-may-encrypt', route: 'encryption', label: 'encryption.policy.title' },
  { id: 'encryption-requests', route: 'encryption', label: 'encryption.requests.title' },
  { id: 'default-webhook', route: 'webhooks', label: 'webhooks.global.title' },
  { id: 'replica-targets', route: 'replica', label: 'replica.targets.title' },
  { id: 'replica-rules', route: 'replica', label: 'replica.rules.title' },
  { id: 'usage-source', route: 'usage', label: 'usage.config.title' },
  { id: 'trusted-stores', route: 'plugins', label: 'appStore.stores.title', alias: 'adminSearch.alias.stores', query: { tab: 'apps' } },
  { id: 'storage-quota', route: 'users', label: 'users.quota.title' },
];

/** What building the panel's own rows needs from the app. */
export interface AdminSearchContext {
  /** The menu as this reader sees it (composables/useAdminNav). */
  entries: readonly MegaMenuEntry[];
  /** The route an entry's address was made from. */
  routeFor: (href: string) => { name: string; params?: Record<string, string> } | undefined;
  /** A key in the interface's language. */
  t: (key: string) => string;
  /** The same key in English; '' when the English catalogue has none. */
  en: (key: string) => string;
}

/**
 * A comma-separated synonym list, as words to match. A translator types it,
 * in a language pack too, so it splits on every comma a keyboard writes
 * (`،` in Arabic, `、` in Japanese): core's splitList, the one list splitter.
 */
export function aliasWords(text: string): string[] {
  return splitList(text);
}

/** A key's text in the interface's language and in English, without repeats or `skip`. */
function bothLanguages(ctx: AdminSearchContext, key: string | undefined, skip: string, split: boolean): string[] {
  if (!key) return [];
  const english = ctx.en(key);
  // No English text: the key is not in the catalogue (vue-i18n would answer the key itself).
  if (!english) return [];
  const out = new Set<string>();
  for (const text of [ctx.t(key), english]) {
    for (const w of split ? aliasWords(text) : [text]) {
      if (w && w !== skip && w !== key) out.add(w);
    }
  }
  return [...out];
}

/** The page key in `adminSearch.alias.*` for a menu label key (`nav.users` → `users`). */
function aliasKeyFor(labelKey: string): string {
  return `adminSearch.alias.${labelKey.replace(/^nav\./, '')}`;
}

/**
 * The rows the panel makes by itself: pages, their tabs and single settings,
 * all limited to what the menu offers this reader.
 */
export function adminSearchItems(ctx: AdminSearchContext): PanelSearchItem[] {
  const pages = new Map(adminNavPages().map((p) => [p.route, p]));
  const shown = new Set<string>();
  const out: PanelSearchItem[] = [];

  const pageRow = (id: string, label: string, href: string, hint?: string, icon?: PanelSearchItem['icon']) => {
    const def = pages.get(id);
    const to = ctx.routeFor(href) ?? { name: id };
    if (def) shown.add(def.route);
    out.push({
      id: `page:${id}`,
      kind: 'page',
      label,
      detail: hint,
      icon,
      terms: def ? bothLanguages(ctx, def.label, label, false) : [],
      aliases: def ? bothLanguages(ctx, aliasKeyFor(def.label), label, true) : [],
      target: { route: to } satisfies AdminSearchTarget,
    });
  };

  for (const entry of ctx.entries) {
    if (!entry.sections) {
      if (entry.href) pageRow(entry.id, entry.label, entry.href, undefined, entry.icon);
      continue;
    }
    for (const section of entry.sections) {
      for (const item of section.items) {
        const to = ctx.routeFor(item.href);
        if (to?.name === ADMIN_APP_ROUTE) {
          // An installed app's own screen, from the menu's Apps section.
          out.push({
            id: `page:${item.id}`,
            kind: 'app',
            label: item.label,
            detail: section.label,
            iconName: item.iconName,
            target: { route: to } satisfies AdminSearchTarget,
          });
          continue;
        }
        pageRow(item.id, item.label, item.href, item.hint, item.icon);
      }
    }
  }

  for (const tab of ADMIN_SEARCH_TABS) {
    if (!shown.has(tab.route)) continue;
    const page = pages.get(tab.route);
    const label = ctx.t(tab.label);
    out.push({
      id: `tab:${tab.route}:${tab.tab}`,
      kind: 'page',
      label,
      detail: page ? ctx.t(page.label) : undefined,
      terms: bothLanguages(ctx, tab.label, label, false),
      target: { route: { name: tab.route, query: { tab: tab.tab } } } satisfies AdminSearchTarget,
    });
  }

  for (const s of ADMIN_SEARCH_SETTINGS) {
    if (!shown.has(s.route)) continue;
    const page = pages.get(s.route);
    const label = ctx.t(s.label);
    out.push({
      id: `setting:${s.id}`,
      kind: 'setting',
      label,
      detail: page ? ctx.t(page.label) : undefined,
      terms: bothLanguages(ctx, s.label, label, false),
      aliases: bothLanguages(ctx, s.alias, label, true),
      target: { route: { name: s.route, ...(s.query ? { query: s.query } : {}) } } satisfies AdminSearchTarget,
    });
  }
  return out;
}

/** One row of the server's answer (handlers/panel_search.go `panelHit`). */
export interface PanelHit {
  kind: 'user' | 'group' | 'key' | 'app' | 'storage' | 'share';
  id: string;
  label: string;
  detail?: string;
  /** An app's (or an app action's) label in every language it gave. */
  labels?: Record<string, string>;
  /** The app an action belongs to, or the app itself (its page's address). */
  app?: string;
  /** Other words it answers to (a username, an app's name). */
  terms?: string[];
}

const HIT_ICONS: Record<PanelHit['kind'], string> = {
  user: 'account',
  group: 'account',
  key: 'lock',
  app: 'plugin',
  storage: 'connect',
  share: 'link',
};

/** Where one of the server's rows goes: the page that holds it. */
function hitTarget(hit: PanelHit): AdminSearchTarget {
  switch (hit.kind) {
    case 'user':
      return { route: { name: 'users.edit', params: { id: hit.id } } };
    case 'group':
      return { route: { name: 'groups.edit', params: { id: hit.id } } };
    case 'storage':
      return { route: { name: 'storages.edit', params: { id: hit.id } } };
    case 'app':
      return { route: { name: 'plugins.app', params: { name: hit.app || hit.id } } };
    case 'key':
      return { route: { name: 'api-mcp' } };
    case 'share':
      return { route: { name: 'shares' } };
  }
}

/**
 * One of the server's rows as a search row. An app's label is the one in the
 * interface's language, else English, else what the server said; every other
 * language's label is a word it answers to.
 */
export function remoteItem(hit: PanelHit, locale: string): PanelSearchItem {
  const labels = hit.labels ?? {};
  const label = labels[locale] || labels.en || hit.label;
  const terms = new Set<string>([...(hit.terms ?? []), ...Object.values(labels)]);
  terms.delete(label);
  return {
    id: `${hit.kind}:${hit.id}`,
    kind: hit.kind,
    label,
    detail: hit.detail,
    terms: [...terms],
    iconName: HIT_ICONS[hit.kind],
    target: hitTarget(hit),
  };
}

/** One hit of the file index (`/api/files/search`) as a search row. */
export function fileItem(hit: { id?: number | string; storage?: string; storage_id?: number; path?: string; name?: string; type?: string }): PanelSearchItem {
  const path = String(hit.path ?? '');
  const storage = String(hit.storage ?? '');
  const rel = path.replace(/^\/+/, '');
  const slash = rel.lastIndexOf('/');
  const parent = slash > 0 ? rel.slice(0, slash) : '';
  const name = String(hit.name ?? (slash >= 0 ? rel.slice(slash + 1) : rel));
  const dir = hit.type === 'dir';
  return {
    id: `file:${hit.storage_id ?? storage}:${path}`,
    kind: 'file',
    label: name,
    detail: [storage, parent].filter(Boolean).join('/') || '/',
    isDir: dir,
    target: { file: { storage, path: rel, dir } } satisfies AdminSearchTarget,
  };
}
