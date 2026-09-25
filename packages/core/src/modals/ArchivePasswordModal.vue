<script setup lang="ts">
import { ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import Modal from './Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  archiveName: string;
  busy?: boolean;
  error?: string;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'submit', password: string): void;
}>();

const { t } = useLocale(() => props.locale);
const password = ref('');

watch(() => props.open, (open) => {
  if (open) password.value = '';
});

function submit() {
  if (!password.value || props.busy) return;
  emit('submit', password.value);
}
</script>

<template>
  <Modal :open="open" :title="t('archive.password_title')" size="sm" @close="emit('close')">
    <form class="fe-form" @submit.prevent="submit">
      <p class="fe-field__hint">{{ archiveName }}</p>
      <label class="fe-field">
        <span class="fe-field__label">{{ t('archive.password_required') }}</span>
        <input
          v-model="password"
          class="fe-input"
          type="password"
          autocomplete="off"
          data-1p-ignore
          data-bwignore="true"
          data-lpignore="true"
          autofocus
          :disabled="busy"
        />
      </label>
      <p v-if="error" class="fe-form__error" role="alert">{{ error }}</p>
    </form>
    <template #actions>
      <button type="button" class="fe-btn" :disabled="busy" @click="emit('close')">
        {{ t('archive.cancel') }}
      </button>
      <button type="button" class="fe-btn fe-btn--primary" :disabled="busy || !password" @click="submit">
        {{ busy ? t('archive.opening') : t('archive.continue') }}
      </button>
    </template>
  </Modal>
</template>
