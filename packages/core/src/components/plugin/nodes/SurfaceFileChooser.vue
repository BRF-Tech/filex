<script setup lang="ts">
/**
 * SurfaceFileChooser — a plugin surface's `file-chooser` node. "Choose…"
 * opens the explorer's OWN destination picker (`modals/DestinationPickerModal`,
 * the same dialog Move to… / Copy to… use) in `choose` mode; `kind: "file"`
 * asks it to list files and answer with the one ticked. The answer is an
 * adapter-qualified path, which is what goes into `data.values[id]`.
 */
import { computed, ref } from 'vue';
import type { LocaleCode, ThemeMode } from '../../../types/ExplorerConfig';
import type { FileApi } from '../../../composables/useFileApi';
import { useLocale } from '../../../composables/useLocale';
import { labelOfWire, parentOfWire } from '../../../lib/destinationTree';
import DestinationPickerModal from '../../../modals/DestinationPickerModal.vue';

const props = defineProps<{
  id: string;
  kind?: string;
  modelValue: string;
  locale: LocaleCode;
  theme?: ThemeMode;
  /** Absent: the button is drawn disabled — there is nothing to browse with. */
  api?: Pick<FileApi, 'index'>;
  /** Storage names the picker may span. */
  storages?: string[];
  /** Where the picker opens — the folder the view was opened in. */
  startAt?: string;
  disabled?: boolean;
  invalid?: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', v: string): void;
}>();

const { t } = useLocale(() => props.locale);

const open = ref(false);
const pick = computed<'file' | 'dir'>(() => (props.kind === 'dir' ? 'dir' : 'file'));
const shown = computed(() => (props.modelValue ? labelOfWire(props.modelValue, '') || props.modelValue : ''));
/** Where the picker opens: the chosen entry's folder, else where the view was opened. */
const openAt = computed(() => {
  if (!props.modelValue) return props.startAt;
  if (pick.value === 'dir') return props.modelValue;
  return parentOfWire(props.modelValue, false) ?? props.modelValue;
});

function onPick(path: string) {
  open.value = false;
  emit('update:modelValue', path);
}
</script>

<template>
  <div class="fe-sfile" :class="{ 'is-invalid': invalid }" data-testid="surface-file-chooser">
    <span
      class="fe-sfile__value"
      :class="{ 'fe-sfile__value--empty': !modelValue }"
      :title="modelValue || undefined"
      data-testid="surface-file-chooser-value"
    >{{ shown || t('plugin.file.none') }}</span>
    <button
      type="button"
      class="fe-btn fe-btn--sm"
      :disabled="disabled || !api"
      data-testid="surface-file-chooser-open"
      @click="open = true"
    >
      {{ t('plugin.file.choose') }}
    </button>
    <button
      v-if="modelValue"
      type="button"
      class="fe-btn fe-btn--sm"
      :disabled="disabled"
      data-testid="surface-file-chooser-clear"
      @click="emit('update:modelValue', '')"
    >
      {{ t('plugin.file.clear') }}
    </button>
    <DestinationPickerModal
      v-if="api"
      :open="open"
      :api="api"
      :locale="locale"
      mode="choose"
      :pick="pick"
      :storages="storages"
      :start-at="openAt"
      @close="open = false"
      @pick="onPick"
    />
  </div>
</template>
