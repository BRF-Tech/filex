<script setup lang="ts">
/**
 * OpenWithDialog - "Choose an app…" from the file menu (0.50, docs/
 * APP-PLUGINS.md → Default apps), the way a desktop asks it: every handler the
 * administrator left on for this kind, the one that opens it now marked, and
 * "Always use this app for .<ext> files". Ticked, the choice is kept on the
 * person's account (lib/openWith) and every later opening of the kind uses it.
 *
 * The list is the explorer's (lib/appViewer `openHandlersFor`): this dialog
 * never offers a handler the administrator switched off.
 */
import { computed, ref, watch } from 'vue';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import { isOfficeHandler, type OpenHandler } from '../lib/appViewer';
import { useLocale } from '../composables/useLocale';
import { labelOf as pluginLabelOf } from '../lib/pluginLabel';
import Modal from './Modal.vue';
import ChoiceButtons, { type ChoiceOption } from '../components/ChoiceButtons.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  theme?: ThemeMode;
  /** The file's name, as the person reads it. */
  name: string;
  /** Its kind: the extension, lower-case, no dot ('' for none). */
  kind: string;
  /** The handlers that are on for the kind, in the administrator's order. */
  handlers: OpenHandler[];
  /** The handler that opens it now. */
  current: string | null;
  /** The person already chose one for the kind (it is kept on the account). */
  remembered: string | null;
}>();

const emit = defineEmits<{
  (e: 'cancel'): void;
  (e: 'open', id: string, always: boolean): void;
}>();

const { t } = useLocale(() => props.locale);

const picked = ref<string | null>(null);
const always = ref(false);

watch(
  () => props.open,
  (on) => {
    if (!on) return;
    picked.value = props.current ?? props.handlers[0]?.id ?? null;
    always.value = !!props.remembered && props.remembered === picked.value;
  },
  { immediate: true },
);

/** A handler as the person reads it: the app's view by its label, filex's own
 *  and ONLYOFFICE (0.51, a .csv) by name. */
function handlerLabel(h: OpenHandler): string {
  if (isOfficeHandler(h)) return t('openWith.onlyoffice');
  if (!h.view) return t('openWith.builtin');
  return pluginLabelOf(h.view.label, props.locale) || h.view.plugin;
}

const options = computed<ChoiceOption[]>(() =>
  props.handlers.map((h) => ({
    value: h.id,
    label: handlerLabel(h),
    help: h.id === props.current ? t('openWith.now') : undefined,
  })),
);

function submit(): void {
  if (!picked.value) return;
  emit('open', picked.value, always.value && !!props.kind);
}
</script>

<template>
  <Modal :open="open" :locale="locale" :theme="theme" :title="t('openWith.title')" size="sm" @close="emit('cancel')">
    <div class="fe-openwith" data-testid="open-with-dialog">
      <p class="fe-openwith__lead">{{ t('openWith.lead', { name }) }}</p>
      <ChoiceButtons
        :model-value="picked"
        @update:model-value="(v: string | string[]) => (picked = typeof v === 'string' ? v : null)"
        :options="options"
        :aria-label="t('openWith.title')"
        testid-prefix="open-with-choice"
      />
      <label v-if="kind" class="fe-openwith__always">
        <input v-model="always" type="checkbox" data-testid="open-with-always" />
        <span>{{ t('openWith.always', { ext: '.' + kind }) }}</span>
      </label>
      <p v-if="kind" class="fe-openwith__hint">{{ t('openWith.hint') }}</p>
    </div>
    <template #actions>
      <button type="button" class="fe-btn" data-testid="open-with-cancel" @click="emit('cancel')">
        {{ t('modal.rename.cancel') }}
      </button>
      <button type="button" class="fe-btn fe-btn--primary" :disabled="!picked" data-testid="open-with-open" @click="submit">
        {{ t('openWith.open') }}
      </button>
    </template>
  </Modal>
</template>

<style>
.fe-openwith {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}
.fe-openwith__lead {
  margin: 0;
  color: var(--fe-text);
  overflow-wrap: anywhere;
}
.fe-openwith__always {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  color: var(--fe-text);
  cursor: pointer;
}
.fe-openwith__hint {
  margin: 0;
  font-size: 0.8125rem;
  color: var(--fe-text-muted);
}
</style>
