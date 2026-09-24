<script setup lang="ts">
/**
 * PluginConfirmModal — the question an action's manifest `confirm` text asks
 * before the job is queued. The same small dialog every surface uses
 * (`modals/Modal`), so a plugin's confirmation looks like the trash's.
 */
import type { LocaleCode, ThemeMode } from '../../types/ExplorerConfig';
import { useLocale } from '../../composables/useLocale';
import Modal from '../../modals/Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  theme?: ThemeMode;
  /** The action's label — the dialog's title. */
  title: string;
  /** The manifest's `confirm` text in the viewer's language. */
  message: string;
  danger?: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'confirm'): void;
}>();

const { t } = useLocale(() => props.locale);
</script>

<template>
  <Modal
    :open="open"
    :title="title || t('plugin.confirm.title')"
    size="sm"
    :theme="theme"
    @close="emit('close')"
  >
    <p data-testid="plugin-confirm-message">{{ message }}</p>
    <template #actions>
      <button type="button" class="fe-btn" @click="emit('close')">
        {{ t('plugin.confirm.cancel') }}
      </button>
      <button
        type="button"
        class="fe-btn"
        :class="danger ? 'fe-btn--danger' : 'fe-btn--primary'"
        data-testid="plugin-confirm-run"
        @click="emit('confirm')"
      >
        {{ t('plugin.confirm.run') }}
      </button>
    </template>
  </Modal>
</template>
