<script setup lang="ts">
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import Modal from './Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  count: number;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'confirm'): void;
}>();

const { t } = useLocale(() => props.locale);
</script>

<template>
  <Modal :open="open" :title="t('modal.delete.title')" size="sm" @close="emit('close')">
    <p>{{ t('modal.delete.message', { count }) }}</p>
    <template #actions>
      <button type="button" class="fe-btn" @click="emit('close')">
        {{ t('modal.delete.cancel') }}
      </button>
      <button type="button" class="fe-btn fe-btn--danger" @click="emit('confirm')">
        {{ t('modal.delete.confirm') }}
      </button>
    </template>
  </Modal>
</template>
