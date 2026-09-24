<script setup lang="ts">
/**
 * SurfaceConversation — the body of ANY screen a plugin is talking through.
 *
 * Three frames draw a plugin surface: the modal a `modal` view opens, the
 * whole page a `page` view opens, and the card an outside participant gets
 * at `/p/<token>`. The FRAME is what differs (a dialog, a document, a card);
 * the body is the same five things every time — the nodes, the toast the
 * answer carried, "working…", the failure, and the footer buttons.
 *
 * ⚠⚠ Written once for that reason. Three copies of this block existed by the
 * time the `page` placement arrived, and the drift had already started: the
 * modal showed no toast at all, so a plugin that answered `{toast}` from a
 * dialog was simply not heard. Anything added here — a node type's wiring, a
 * busy affordance, an aria role — now reaches all three frames or none.
 */
import type { LocaleCode, ThemeMode } from '../../types/ExplorerConfig';
import type { PluginSurfaceStore } from '../../composables/usePluginSurface';
import type { FileRefResolver } from '../../types/Plugins';
import { useLocale } from '../../composables/useLocale';
import SurfaceRenderer, { type SurfaceApi } from './SurfaceRenderer.vue';

const props = defineProps<{
  /** The conversation this body belongs to (`usePluginSurface`). */
  conv: PluginSurfaceStore;
  locale: LocaleCode;
  theme?: ThemeMode;
  /** Absent in a bare render (the public page has no explorer API). */
  api?: SurfaceApi;
  /** The plugin the surface belongs to — the people-picker's lookup asks for it. */
  plugin?: string;
  storages?: string[];
  startAt?: string;
  /** A public page's exposed copies (`pub:N`) → a URL the browser may load. */
  fileUrl?: FileRefResolver;
  /** The last toast the frame collected, if it shows one inline. */
  toast?: string;
  /**
   * v3 §3.2 — `page` lets a `pdf-fields` node take the viewport instead of
   * sitting in the document flow. The FRAME knows which it is; the nodes do
   * not, and must not guess from the window size (an embed in a narrow
   * column is not a page).
   */
  layout?: 'inline' | 'page';
  /** Where the document may grow to in `page` layout — the frame's own box. */
  pageHeight?: string;
}>();

const { t } = useLocale(() => props.locale);
</script>

<template>
  <SurfaceRenderer
    v-if="conv.current.value"
    :nodes="conv.current.value.nodes ?? []"
    :locale="locale"
    :theme="theme"
    :errors="conv.errors.value"
    :values="conv.values.value"
    :api="api"
    :plugin="plugin"
    :storages="storages"
    :start-at="startAt"
    :file-url="fileUrl"
    :layout="layout"
    :page-height="pageHeight"
    :invalid-keys="conv.invalidKeys.value"
    :disabled="conv.busy.value"
    @update:values="conv.updateValues"
    @change="conv.scheduleChange"
    @action="conv.rowAction"
  />
  <p v-if="toast" class="fe-surface__text fe-surface__text--info" role="status">{{ toast }}</p>
  <p v-if="conv.busy.value" class="fe-surface__text fe-surface__text--muted">{{ t('plugin.view.busy') }}</p>
  <p v-if="conv.failure.value" class="fe-surface__error" role="alert">{{ conv.failure.value }}</p>
</template>
