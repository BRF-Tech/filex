<script setup lang="ts">
/**
 * DraftConflictModal — "a file with this name is already there" (issue #71).
 *
 * Saving a draft never replaces anything. When its folder already holds a
 * file of its name, the server answers with the free name beside it —
 * `name (2).ext`, the numbering the New document dialog suggests — and saves
 * nothing until the person agrees to exactly that name. This is that
 * question, asked the same way from the editor's Save button, from its close
 * question and from the Drafts view.
 */
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import Modal from './Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  /** The viewer's theme: these dialogs sit beside its card, not inside it. */
  theme?: ThemeMode;
  /** The draft's own name — what is already there. */
  name: string;
  /** The free name the server offered. */
  suggested: string;
  /** The folder, as a person reads it (`docs / Reports`). */
  folder: string;
  busy?: boolean;
  error?: string | null;
}>();

const emit = defineEmits<{
  (e: 'cancel'): void;
  (e: 'confirm'): void;
}>();

const { t } = useLocale(() => props.locale);
</script>

<template>
  <Modal :open="open" :locale="locale" :theme="theme" :title="t('draft.taken.title')" size="sm" :busy="busy" @close="emit('cancel')">
    <div class="fe-draftq" data-testid="draft-taken-dialog">
      <p class="fe-draftq__body">{{ t('draft.taken.body', { name, folder, suggested }) }}</p>
      <p v-if="error" class="fe-form__error" role="alert">{{ error }}</p>
    </div>
    <template #actions>
      <button type="button" class="fe-btn" :disabled="busy" data-testid="draft-taken-cancel" @click="emit('cancel')">
        {{ t('modal.rename.cancel') }}
      </button>
      <button
        type="button"
        class="fe-btn fe-btn--primary"
        :disabled="busy"
        :aria-busy="busy ? 'true' : undefined"
        data-testid="draft-taken-confirm"
        @click="emit('confirm')"
      >
        {{ t('draft.taken.confirm', { name: suggested }) }}
      </button>
    </template>
  </Modal>
</template>
