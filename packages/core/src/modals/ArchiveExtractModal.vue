<script setup lang="ts">
import { ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import Modal from './Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  archiveName: string;
  suggestedFolder: string;
  busy?: boolean;
  error?: string;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'submit', value: { folder: string }): void;
}>();

const { t } = useLocale(() => props.locale);
const folder = ref('');
const localError = ref('');

watch(() => props.open, (open) => {
  if (!open) return;
  folder.value = props.suggestedFolder;
  localError.value = '';
});

function submit() {
  if (!folder.value.trim() || /[\\/]/.test(folder.value)) {
    localError.value = t('archive.invalid_folder');
    return;
  }
  emit('submit', { folder: folder.value.trim() });
}
</script>

<template>
  <Modal :open="open" :title="t('archive.extract_title')" size="sm" @close="emit('close')">
    <form class="fe-form" @submit.prevent="submit">
      <p class="fe-field__hint">{{ archiveName }}</p>
      <label class="fe-field">
        <span class="fe-field__label">{{ t('archive.destination_folder') }}</span>
        <input v-model="folder" class="fe-input" autocomplete="off" />
      </label>
      <p v-if="localError || error" class="fe-form__error">{{ localError || error }}</p>
    </form>
    <template #actions>
      <button type="button" class="fe-btn" :disabled="busy" @click="emit('close')">{{ t('archive.cancel') }}</button>
      <button type="button" class="fe-btn fe-btn--primary" :disabled="busy" @click="submit">
        {{ busy ? t('archive.extracting') : t('archive.extract_to_folder') }}
      </button>
    </template>
  </Modal>
</template>
