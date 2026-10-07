<script setup lang="ts">
/**
 * The admin panel's search (task #168, docs/ADMIN-PANEL.md → Search) - core's
 * PanelSearch, bound to THIS app.
 *
 * ⚠⚠ Not a second search. The box, the panel, the keyboard, the screen-reader
 * wiring and the matching are `@brftech/filex-core`'s (`PanelSearch`,
 * `lib/panelSearch`); what the panel finds by itself is `lib/adminSearch`,
 * built from the menu's own entries (`composables/useAdminNav`), so a page
 * the menu does not offer this reader is not found either. What stays here is
 * only what neither can know: the API, the router and vue-i18n.
 *
 * Mounted once, in the top bar (components/TopNav.vue): a box on a wide
 * screen, a button and a layer over the window below 1024px. Ctrl+K (the
 * explorer's palette key, as this person has it bound) opens it.
 */
import { computed } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import {
  PANEL_SEARCH_PREFIXED_CAP,
  PanelSearch,
  type PanelRecentStore,
  type PanelSearchItem,
  type PanelSearchKind,
  type PanelSearchQuery,
} from '@brftech/filex-core';

import { PanelSearchApi } from '@/api/panelSearch';
import { useAdminNav } from '@/composables/useAdminNav';
import { adminSearchItems, fileItem, remoteItem, type AdminSearchTarget } from '@/lib/adminSearch';
import { openNotificationTarget } from '@/lib/notificationNav';
import en from '@/locales/en.json';

defineProps<{ compact?: boolean }>();

const { t, locale } = useI18n();
const router = useRouter();
const { entries, routeFor } = useAdminNav();

/** A key's English text, '' when the English catalogue has none. */
function english(key: string): string {
  let node: unknown = en;
  for (const part of key.split('.')) {
    if (!node || typeof node !== 'object') return '';
    node = (node as Record<string, unknown>)[part];
  }
  return typeof node === 'string' ? node : '';
}

const items = computed<PanelSearchItem[]>(() =>
  adminSearchItems({ entries: entries.value, routeFor, t: (key) => t(key), en: english }),
);

/** The kinds the server answers; a prefix for any other kind asks it nothing. */
const SERVER_KINDS: readonly PanelSearchKind[] = ['user', 'group', 'key', 'app', 'storage', 'share'];

async function remote(q: PanelSearchQuery): Promise<PanelSearchItem[]> {
  if (q.kind && !SERVER_KINDS.includes(q.kind)) return [];
  // A prefix shows a full page of its kind (core PANEL_SEARCH_PREFIXED_CAP).
  const hits = q.kind
    ? await PanelSearchApi.search(q.text, [q.kind], PANEL_SEARCH_PREFIXED_CAP)
    : await PanelSearchApi.search(q.text);
  return hits.map((h) => remoteItem(h, String(locale.value)));
}

async function files(text: string, limit: number): Promise<PanelSearchItem[]> {
  return (await PanelSearchApi.files(text, limit)).map(fileItem);
}

/* The person's recent searches live on the server (migration 00090), so they
   follow them to another browser or device. */
const recent: PanelRecentStore = {
  list: async () => (await PanelSearchApi.recent()).map((r) => ({ id: r.id, query: r.query })),
  add: (query) => PanelSearchApi.addRecent(query),
  remove: (id) => PanelSearchApi.removeRecent(id),
  clear: () => PanelSearchApi.clearRecent(),
};

function onChoose(item: PanelSearchItem): void {
  const target = item.target as AdminSearchTarget | undefined;
  if (!target) return;
  if ('file' in target) {
    // A file opens in the file manager, in its folder with its row selected:
    // the same door a notification about a file uses.
    void openNotificationTarget(router, {
      kind: target.file.dir ? 'dir' : 'file',
      storage: target.file.storage,
      path: target.file.path,
    });
    return;
  }
  void router.push(target.route);
}
</script>

<template>
  <PanelSearch
    :locale="String(locale)"
    :compact="compact"
    :items="items"
    :remote="remote"
    :files="files"
    :recent="recent"
    @choose="onChoose"
  />
</template>
