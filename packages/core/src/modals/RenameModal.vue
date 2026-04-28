<script setup lang="ts">
import { ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import Modal from './Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  currentName: string;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'submit', name: string): void;
}>();

const { t } = useLocale(() => props.locale);
const name = ref('');

watch(
  () => [props.open, props.currentName] as const,
  ([isOpen, cur]) => {
    if (isOpen) {
      name.value = cur;
    }
  },
);

function submit() {
  const clean = name.value.trim();
  if (!clean) return;
  if (/[\\/]/.test(clean) || clean === '.' || clean === '..') return;
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
        @keydown.enter.prevent="submit"
      />
    </form>
    <template #actions>
      <button type="button" class="fe-btn" @click="emit('close')">
        {{ t('modal.rename.cancel') }}
      </button>
      <button type="button" class="fe-btn fe-btn--primary" @click="submit">
        {{ t('modal.rename.save') }}
      </button>
    </template>
  </Modal>
</template>
