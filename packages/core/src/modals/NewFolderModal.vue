<script setup lang="ts">
import { ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import Modal from './Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'submit', name: string): void;
}>();

const { t } = useLocale(() => props.locale);
const name = ref('');
const err = ref<string | null>(null);

watch(() => props.open, (v) => {
  if (v) {
    name.value = '';
    err.value = null;
  }
});

function submit() {
  const clean = name.value.trim();
  if (!clean) {
    err.value = t('modal.newfolder.placeholder');
    return;
  }
  if (/[\\/]/.test(clean) || clean === '.' || clean === '..') {
    err.value = 'Geçersiz karakter';
    return;
  }
  emit('submit', clean);
}
</script>

<template>
  <Modal :open="open" :title="t('modal.newfolder.title')" size="sm" @close="emit('close')">
    <form @submit.prevent="submit">
      <input
        v-model="name"
        type="text"
        class="fe-input"
        :placeholder="t('modal.newfolder.placeholder')"
        autocomplete="off"
        @keydown.enter.prevent="submit"
      />
      <p v-if="err" class="fe-form__error">{{ err }}</p>
    </form>
    <template #actions>
      <button type="button" class="fe-btn" @click="emit('close')">
        {{ t('modal.newfolder.cancel') }}
      </button>
      <button type="button" class="fe-btn fe-btn--primary" @click="submit">
        {{ t('modal.newfolder.create') }}
      </button>
    </template>
  </Modal>
</template>
