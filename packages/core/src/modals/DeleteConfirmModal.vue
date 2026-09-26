<script setup lang="ts">
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import Modal from './Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  count: number;
  /** The server's answer when it did not take the delete. It used to go to
   *  the host's console only, and the dialog stayed open with nothing in it. */
  error?: string | null;
  /** The delete is on its way; the button waits for the answer. */
  busy?: boolean;
  /** The items are in the trash already: this deletes them for good, and
   *  the words say so. They said "will be moved to trash" about items that
   *  were there. */
  permanent?: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'confirm'): void;
}>();

const { t } = useLocale(() => props.locale);

function confirm() {
  if (props.busy) return;
  emit('confirm');
}
</script>

<template>
  <Modal
    :open="open"
    :title="permanent ? t('modal.delete.permanent_title') : t('modal.delete.title')"
    size="sm"
    @close="emit('close')"
  >
    <p>{{ permanent ? t('modal.delete.permanent_message', { count }) : t('modal.delete.message', { count }) }}</p>
    <p v-if="error" class="fe-form__error" role="alert">{{ error }}</p>
    <template #actions>
      <button type="button" class="fe-btn" @click="emit('close')">
        {{ t('modal.delete.cancel') }}
      </button>
      <button type="button" class="fe-btn fe-btn--danger" :disabled="busy" @click="confirm">
        {{
          busy
            ? t(permanent ? 'modal.delete.permanent_working' : 'modal.delete.working')
            : t(permanent ? 'modal.delete.permanent_confirm' : 'modal.delete.confirm')
        }}
      </button>
    </template>
  </Modal>
</template>
