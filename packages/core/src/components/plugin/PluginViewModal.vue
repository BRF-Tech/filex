<script setup lang="ts">
/**
 * PluginViewModal — the dialog an app plugin's `modal` view opens in (and
 * the frame a `home` view is drawn in, full-size).
 *
 * The explorer hands it the first surface (the answer to `run`, or the
 * `open` event of a view); from then on the conversation is
 * `usePluginSurface`'s — the same one the inspector section holds — and
 * this file is only the frame: title, size, the footer buttons, Close.
 * When the server answers `{op}` the row goes up (`op`) and the dialog
 * closes.
 *
 * ⚠ No iframe, no `v-html`, no script: SurfaceRenderer draws nodes with our
 * own components and shows an unknown type as unknown.
 *
 * ⚠ The body and the buttons are `SurfaceConversation` / `SurfaceFooterButtons`
 * — shared with the `page` frame and the public card, so a fix to any of the
 * five things a surface body draws reaches all three.
 */
import { computed, watch } from 'vue';
import type { LocaleCode, ThemeMode } from '../../types/ExplorerConfig';
import type { FileApi } from '../../composables/useFileApi';
import type { PluginSurface, SurfaceOpenRequest } from '../../types/Plugins';
import { useLocale } from '../../composables/useLocale';
import { usePluginSurface } from '../../composables/usePluginSurface';
import { labelOf } from '../../lib/pluginLabel';
import Modal from '../../modals/Modal.vue';
import SurfaceConversation from './SurfaceConversation.vue';
import SurfaceSections from './SurfaceSections.vue';
import SurfaceFooterButtons from './SurfaceFooterButtons.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  theme?: ThemeMode;
  api: FileApi;
  plugin: string;
  view: string;
  /** The first screen. */
  surface: PluginSurface | null;
  /** Adapter-qualified path of the row the view was opened on (echoed on every event). */
  path?: string;
  /**
   * Every row of the selection the view was opened on (echoed on every
   * event as `paths`). ⚠ Without it a screen opened on three files talks
   * about the first one from its second event on (#64).
   */
  paths?: string[];
  /** Storage names a file-chooser may span; where its picker opens. */
  storages?: string[];
  startAt?: string;
  /** Forces the dialog size (a `home` view is drawn `xl`); else the surface's own. */
  size?: 'sm' | 'md' | 'lg' | 'xl';
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  /** The server enqueued a job for this view; the raw ops row. */
  (e: 'op', op: Record<string, unknown>): void;
  (e: 'toast', message: string): void;
  /**
   * v3 §3.0 — the answer says "go to this file". Reported UP rather than
   * acted on here: the explorer is what can show a file and start a screen
   * on it (`openAppTarget`), and a dialog navigating the window under itself
   * would throw away the folder the person was standing in.
   */
  (e: 'open', req: SurfaceOpenRequest): void;
}>();

const { t } = useLocale(() => props.locale);

const conv = usePluginSurface(
  {
    api: props.api,
    plugin: props.plugin,
    view: props.view,
    path: () => props.path,
    paths: () => props.paths,
    locale: () => props.locale,
    errorText: () => t('plugin.view.error'),
  },
  {
    onOp: (op) => emit('op', op),
    onDone: () => emit('close'),
    onToast: (m) => emit('toast', m),
    onOpen: (req) => emit('open', req),
  },
);

watch(
  () => props.surface,
  (s) => conv.setSurface(s),
  { immediate: true },
);

const current = conv.current;
const title = computed(() => labelOf(current.value?.title, props.locale) || props.view);
/**
 * A home page's menu, when a host with no page of its own draws one in this
 * dialog (the degraded frame — an embed without `pluginPageBase`). There is
 * no address to keep here, so a choice simply asks for that section.
 */
const sections = computed(() => current.value?.sections ?? []);
async function chooseSection(id: string): Promise<void> {
  if (!id || id === current.value?.section) return;
  try {
    const res = await props.api.pluginView(props.plugin, props.view, props.path, id);
    if (res?.surface) conv.setSurface(res.surface);
  } catch (e) {
    // ⚠ An app's own error text never met the catalogue — isolate it.
    emit('toast', t.foreign?.(String((e as Error)?.message ?? '')) || t('plugin.view.error'));
  }
}

const size = computed<'sm' | 'md' | 'lg' | 'xl'>(() => {
  if (props.size) return props.size;
  const s = current.value?.size;
  return s === 'sm' || s === 'lg' || s === 'xl' ? s : 'md';
});
</script>

<template>
  <Modal :open="open" :title="title" :size="size" :theme="theme" :locale="locale" @close="emit('close')">
    <div class="fe-plugin-view" data-testid="plugin-view">
      <SurfaceSections
        v-if="sections.length"
        :sections="sections"
        :active="current?.section"
        :locale="locale"
        :label="title"
        :disabled="conv.busy.value"
        @select="chooseSection"
      />
      <SurfaceConversation
        :conv="conv"
        :locale="locale"
        :theme="theme"
        :api="api"
        :plugin="plugin"
        :storages="storages"
        :start-at="startAt"
      />
    </div>
    <!-- ⚠ The frame's own Close only when the screen brought no buttons of
         its own. A plugin's footer already carries its way out (Cancel,
         Back), the header has its ×, and Escape and an outside click close
         the dialog — a "Close" beside the plugin's "Cancel" was two buttons
         doing one thing (the converter's first step read "Kapat · Vazgeç ·
         İleri", release-candidate sweep 2026-09-21). A screen with no
         buttons at all — a result, a notice — still gets one, so no screen
         is a dead end for a pointer. -->
    <template #actions>
      <button
        v-if="!conv.footer.value.length"
        type="button"
        class="fe-btn"
        :disabled="conv.busy.value"
        data-testid="plugin-view-close"
        @click="emit('close')"
      >
        {{ t('plugin.view.close') }}
      </button>
      <SurfaceFooterButtons :conv="conv" :locale="locale" testid-prefix="plugin-view" />
    </template>
  </Modal>
</template>
