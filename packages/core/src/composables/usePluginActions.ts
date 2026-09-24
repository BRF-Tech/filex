/**
 * usePluginActions — the file-menu rows app plugins add, and how to run one.
 *
 * `GET /api/files/plugins/actions` is fetched once per endpoint URL and
 * shared by every explorer instance on the page (the admin explorer, a split
 * pane, an embedded web component all ask the same server), and re-asked
 * after five minutes — the contract's client cache. Nothing is requested
 * until `enabled()` says so: with `capabilities.app_plugins.enabled` false the
 * explorer makes zero plugin calls.
 *
 * The composable holds no menu logic of its own: which rows a selection gets
 * is `lib/pluginMenu`, the rule is `lib/pluginApplies`, both pure.
 */
import { computed, ref, watch, type Ref } from 'vue';
import type { FileApi } from './useFileApi';
import type { FileNode } from '../types/FileNode';
import type { PluginActionRow, PluginActionsResponse, PluginRunResult, PluginViewRow } from '../types/Plugins';
import { pluginActionsFor, pluginActionKey } from '../lib/pluginMenu';

export const PLUGIN_ACTIONS_TTL_MS = 5 * 60 * 1000;

interface CacheEntry {
  at: number;
  data: Ref<PluginActionsResponse>;
  inflight: Promise<PluginActionsResponse> | null;
}

/** One entry per actions URL, shared across every instance on the page. */
const cache = new Map<string, CacheEntry>();

/** Forget every cached answer — tests, and a host that just installed a plugin. */
export function invalidatePluginActions(): void {
  cache.clear();
}

function entryFor(url: string): CacheEntry {
  let e = cache.get(url);
  if (!e) {
    e = { at: 0, data: ref<PluginActionsResponse>({ actions: [], views: [] }), inflight: null };
    cache.set(url, e);
  }
  return e;
}

export function usePluginActions(api: FileApi, enabled: () => boolean) {
  const url = api.endpoints.pluginActions ?? '';
  const entry = entryFor(url || '(none)');
  const loaded = ref(false);
  const error = ref<string | null>(null);

  const isEnabled = computed(() => enabled() && url !== '');

  const actions = computed<PluginActionRow[]>(() => (isEnabled.value ? entry.data.value.actions : []));
  const views = computed<PluginViewRow[]>(() => (isEnabled.value ? entry.data.value.views : []));

  /** Fetch (or reuse the shared answer) — a no-op while the feature is off. */
  async function refresh(force = false): Promise<PluginActionsResponse> {
    if (!isEnabled.value) return { actions: [], views: [] };
    const fresh = entry.at > 0 && Date.now() - entry.at < PLUGIN_ACTIONS_TTL_MS;
    if (fresh && !force) {
      loaded.value = true;
      return entry.data.value;
    }
    if (entry.inflight) return entry.inflight;
    entry.inflight = api
      .pluginActions()
      .then((res) => {
        entry.data.value = { actions: res.actions ?? [], views: res.views ?? [] };
        entry.at = Date.now();
        error.value = null;
        return entry.data.value;
      })
      .catch((e: unknown) => {
        error.value = String((e as Error)?.message ?? e);
        return entry.data.value;
      })
      .finally(() => {
        entry.inflight = null;
        loaded.value = true;
      });
    return entry.inflight;
  }

  // The first time the feature turns on (capabilities answered), ask once.
  watch(
    isEnabled,
    (on) => {
      if (on) void refresh();
    },
    { immediate: true },
  );

  /** Action rows whose rule accepts this selection. */
  function actionsFor(selection: FileNode[]): PluginActionRow[] {
    return pluginActionsFor(actions.value, selection);
  }

  /** The action row behind a menu key, if any. */
  function byKey(key: string): PluginActionRow | undefined {
    return actions.value.find((a) => pluginActionKey(a) === key);
  }

  /**
   * Run an action on wire paths. `{op}` when the server queued a job, or
   * `{surface}` when the action opens a view first.
   */
  function run(
    action: PluginActionRow,
    targets: FileNode[],
    params?: Record<string, unknown>,
  ): Promise<PluginRunResult> {
    return api.pluginActionRun(action.plugin, action.id, {
      paths: targets.map((n) => n.path),
      params,
    });
  }

  return { enabled: isEnabled, actions, views, loaded, error, refresh, actionsFor, byKey, run };
}

export type PluginActionsStore = ReturnType<typeof usePluginActions>;
