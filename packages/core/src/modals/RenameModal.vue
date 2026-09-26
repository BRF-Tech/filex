<script setup lang="ts">
import { ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { isInternalName } from '../lib/internalPaths';
import Modal from './Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  currentName: string;
  /** Why the last attempt did not happen — the host's answer from the server
   *  (a taken name, a refusal, an outage). Shown under the field until the
   *  name is edited. */
  error?: string | null;
  /** The rename is on its way. Renaming a folder on an object store copies
   *  every object in it, so this can last; a second Save meanwhile met the
   *  half-copied folder and was refused as "already here". */
  busy?: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'submit', name: string): void;
}>();

const { t } = useLocale(() => props.locale);
const name = ref('');
// What the dialog is saying right now — ONE line, whoever said it: the host's
// error, or the dialog's own refusal of a name that is not one (a slash, "."
// or "..", a name filex keeps for itself). Either goes away once the person
// edits the name.
const shownError = ref<string | null>(null);

watch(
  () => [props.open, props.currentName] as const,
  ([isOpen, cur]) => {
    if (isOpen) {
      name.value = cur;
      shownError.value = props.error ?? null;
    }
  },
);

watch(
  () => props.error,
  (err) => {
    shownError.value = err ?? null;
  },
  { immediate: true },
);

function onInput() {
  shownError.value = null;
}

function submit() {
  if (props.busy) return;
  const clean = name.value.trim();
  if (!clean) return;
  if (/[\\/]/.test(clean) || clean === '.' || clean === '..') {
    // Refused here with a word, not by doing nothing: a Save button that
    // silently ignores the click reads as a broken dialog.
    shownError.value = t('modal.newfolder.invalid');
    return;
  }
  // A document renamed to `.versions` would vanish from every view the
  // moment it was renamed; the server refuses it, this says why.
  if (isInternalName(clean)) {
    shownError.value = t('names.reserved', { name: clean });
    return;
  }
  emit('submit', clean);
}
</script>

<template>
  <Modal :open="open" :title="t('modal.rename.title')" size="sm" @close="emit('close')">
    <form @submit.prevent="submit">
      <input
        v-model="name"
        type="text"
        class="fe-input"
        autocomplete="off"
        @input="onInput"
        @keydown.enter.prevent="submit"
      />
      <p v-if="shownError" class="fe-form__error" role="alert" data-testid="rename-error">{{ shownError }}</p>
    </form>
    <template #actions>
      <button type="button" class="fe-btn" @click="emit('close')">
        {{ t('modal.rename.cancel') }}
      </button>
      <button type="button" class="fe-btn fe-btn--primary" :disabled="busy" @click="submit">
        {{ busy ? t('modal.rename.saving') : t('modal.rename.save') }}
      </button>
    </template>
  </Modal>
</template>
