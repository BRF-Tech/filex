/**
 * usePluginHomeApps — the app plugins that have a screen of their own.
 *
 * ⚠⚠ ONE question, asked the way the explorer asks it: `GET
 * /api/files/plugins/actions` → the `views[]` rows placed `home`
 * (docs/APP-PLUGINS-API.md → Placements). The explorer's navigation panel
 * already lists exactly these rows as its "Apps" section (`FileExplorer.vue`
 * → `pluginHomeApps`), and the admin panel's sidebar now lists the same ones;
 * a second rule for "which apps have a screen" is how the two sides start
 * disagreeing about what is installed.
 *
 * ⚠ GENERIC, never a page per plugin. The signing app's request table is not
 * special: it is the `home` view of a plugin, and so is whatever the next
 * plugin ships. Nothing here names a plugin, and nothing should.
 *
 * The answer is the core composable's shared, five-minute cache, so the
 * sidebar and the page it opens ask once between them.
 */
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import {
  actionIconSvg,
  pluginLabelOf,
  useFileApi,
  usePluginActions,
  type ExplorerConfig,
} from '@brftech/filex-core';

import { explorerAuth } from '@/lib/explorerConfig';
import { useCapabilitiesStore } from '@/stores/capabilities';

/** One row of the panel's "Apps" section. */
export interface PluginHomeApp {
  /** `<plugin>/<view>` — the row's key, and what a test addresses it by. */
  key: string;
  plugin: string;
  view: string;
  /** The view's label in the reader's language, falling back to its id. */
  label: string;
  /**
   * Inline SVG for the row. ⚠ Static markup from `lib/actionIcons`, chosen by
   * the manifest's icon NAME — never markup a plugin supplied, so `v-html`
   * is injecting the icon library's own strings. An unknown name falls back
   * to the puzzle piece, the mark every extension surface wears.
   */
  svg: string;
}

export function usePluginHomeApps() {
  const caps = useCapabilitiesStore();
  const { locale } = useI18n();

  // The DEFAULT plugin endpoints — the same ones the explorer and the `page`
  // view derive, from the same auth block (lib/explorerConfig).
  const api = useFileApi({ apiBase: '', auth: explorerAuth() } as ExplorerConfig);

  // Nothing is requested while the feature is off: with `app_plugins.enabled`
  // false the panel makes zero plugin calls, exactly like the explorer.
  const store = usePluginActions(api, () => caps.data.app_plugins?.enabled === true);

  const apps = computed<PluginHomeApp[]>(() =>
    store.views.value
      .filter((v) => v.placement === 'home')
      .map((v) => ({
        key: `${v.plugin}/${v.id}`,
        plugin: v.plugin,
        view: v.id,
        label: pluginLabelOf(v.label, locale.value) || v.id,
        svg: actionIconSvg(v.icon && actionIconSvg(v.icon) !== '' ? v.icon : 'plugin'),
      })),
  );

  /** The row behind a `<plugin>/<view>` pair, if that view is still there. */
  function find(plugin: string, view: string): PluginHomeApp | undefined {
    return apps.value.find((a) => a.plugin === plugin && a.view === view);
  }

  return { apps, find, loaded: store.loaded, refresh: store.refresh };
}
