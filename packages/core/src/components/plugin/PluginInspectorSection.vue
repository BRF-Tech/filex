<script setup lang="ts">
/**
 * PluginInspectorSection — one `inspector` view of an app plugin, as a
 * collapsible section of the details panel.
 *
 * Closed, it is a heading and nothing else: no request is made until the
 * person opens it (a panel that loaded every plugin's view for every click
 * would make N calls per selection). Opened, it asks
 * `GET …/views/{plugin}/{view}?path=` once for the selected item and draws
 * the surface inline; the footer buttons post events through the same
 * conversation the modal holds (`usePluginSurface`), and a `{op}` answer
 * goes up for the host to register in the tray, exactly as M1's modal does.
 * A new selection resets the section to closed-and-unloaded.
 */
import { computed, ref, watch } from 'vue';
import type { LocaleCode, ThemeMode } from '../../types/ExplorerConfig';
import type { FileApi } from '../../composables/useFileApi';
import type { PluginViewRow, SurfaceOpenRequest } from '../../types/Plugins';
import { useLocale } from '../../composables/useLocale';
import { usePluginSurface } from '../../composables/usePluginSurface';
import { labelOf } from '../../lib/pluginLabel';
import SurfaceConversation from './SurfaceConversation.vue';
import SurfaceFooterButtons from './SurfaceFooterButtons.vue';

const props = defineProps<{
  api: FileApi;
  view: PluginViewRow;
  /** Adapter-qualified path of the selected item. */
  path: string;
  locale: LocaleCode;
  theme?: ThemeMode;
  storages?: string[];
}>();

const emit = defineEmits<{
  (e: 'op', op: Record<string, unknown>): void;
  (e: 'toast', message: string): void;
  /** v3 §3.0 — the answer says "go to this file"; the explorer navigates. */
  (e: 'open', req: SurfaceOpenRequest): void;
}>();

const { t } = useLocale(() => props.locale);

const expanded = ref(false);
const loading = ref(false);
const loadError = ref('');

const conv = usePluginSurface(
  {
    api: props.api,
    plugin: props.view.plugin,
    view: props.view.id,
    path: () => props.path,
    locale: () => props.locale,
    errorText: () => t('plugin.view.error'),
  },
  {
    onOp: (op) => emit('op', op),
    onOpen: (req) => emit('open', req),
    // The conversation ended (a job was queued, or the plugin said `done`):
    // what the section shows next is the view's fresh state.
    onDone: () => void load(),
    onToast: (m) => emit('toast', m),
  },
);

const label = computed(() => labelOf(props.view.label, props.locale) || props.view.id);
const sectionId = computed(() => `fe-plugin-insp-${props.view.plugin}-${props.view.id}`);

async function load() {
  loading.value = true;
  loadError.value = '';
  try {
    const res = await props.api.pluginView(props.view.plugin, props.view.id, props.path);
    conv.setSurface(res?.surface ?? null);
    if (!res?.surface) loadError.value = t('plugin.view.error');
  } catch (e) {
    conv.setSurface(null);
    // ⚠ An app's own error text never met the catalogue: isolate its
    //   machine runs for the reader's direction (lib/direction).
    loadError.value = t.foreign?.(String((e as Error)?.message ?? e)) || t('plugin.view.error');
  } finally {
    loading.value = false;
  }
}

function toggle() {
  expanded.value = !expanded.value;
  if (expanded.value && !conv.current.value && !loading.value) void load();
}

watch(
  () => props.path,
  () => {
    expanded.value = false;
    loadError.value = '';
    conv.setSurface(null);
  },
);
</script>

<template>
  <section class="fe-inspector__section fe-pinsp" :data-testid="`inspector-plugin-${view.plugin}-${view.id}`">
    <h3 class="fe-inspector__heading fe-pinsp__heading">
      <button
        type="button"
        class="fe-pinsp__toggle"
        :aria-expanded="expanded"
        :aria-controls="sectionId"
        :data-testid="`inspector-plugin-toggle-${view.plugin}-${view.id}`"
        @click="toggle"
      >
        <svg
          class="fe-ficon fe-pinsp__chev"
          :class="{ 'is-open': expanded }"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
          focusable="false"
        >
          <path d="M9 5l7 7-7 7" />
        </svg>
        <span>{{ label }}</span>
      </button>
    </h3>
    <div v-if="expanded" :id="sectionId" class="fe-pinsp__body">
      <p v-if="loading" class="fe-inspector__empty">{{ t('plugin.view.loading') }}</p>
      <p v-else-if="loadError" class="fe-surface__error" role="alert">{{ loadError }}</p>
      <template v-else-if="conv.current.value">
        <SurfaceConversation
          :conv="conv"
          :locale="locale"
          :theme="theme"
          :api="api"
          :plugin="view.plugin"
          :storages="storages"
          :start-at="path"
        />
        <div v-if="conv.footer.value.length" class="fe-pinsp__actions">
          <SurfaceFooterButtons :conv="conv" :locale="locale" testid-prefix="inspector-plugin" small />
        </div>
      </template>
    </div>
  </section>
</template>
