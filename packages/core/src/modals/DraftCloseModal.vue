<script setup lang="ts">
/**
 * DraftCloseModal — what closing a draft's editor asks (issue #71).
 *
 * A draft is a new document that has not been saved where it is meant to go.
 * Its content is already safe — the editor autosaves into the draft — so
 * closing loses nothing; the question is only what should BECOME of it:
 *
 *   Save to disk     it goes to its folder now (with the same "name (2).ext?"
 *                    question the editor's Save button asks);
 *   Keep in Drafts   the default: it stays in Drafts, to finish later;
 *   Discard          it goes to the trash, which deletes it after the
 *                    retention period (restorable until then).
 *
 * ⚠ Escape and a click outside cancel — back to the editor — never one of the
 * three: a key that throws a document away, or puts it somewhere, is a key
 * nobody pressed on purpose. Enter takes the default, "Keep in Drafts", which
 * is the one answer that changes nothing.
 */
import { nextTick, ref, watch } from 'vue';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import Modal from './Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  /** The viewer's theme: these dialogs sit beside its card, not inside it. */
  theme?: ThemeMode;
  /** The draft's name. */
  name: string;
  /** Where it is meant to go, as a person reads it (`docs / Reports`). */
  folder: string;
  /** A verb is on its way — the buttons wait for the answer. */
  busy?: boolean;
  /** What went wrong with the last answer, said. */
  error?: string | null;
}>();

const emit = defineEmits<{
  (e: 'cancel'): void;
  (e: 'save'): void;
  (e: 'keep'): void;
  (e: 'discard'): void;
}>();

const { t } = useLocale(() => props.locale);

const keepBtn = ref<HTMLButtonElement | null>(null);
// Keep has the focus the moment the question opens, not after Modal's 30 ms
// timer (Modal keeps a focus that is already inside). `immediate` covers a
// host that mounts the question already open; PreviewModal keeps it mounted
// and flips `open`.
//
// ⚠ previewDraft's "Keep is the default" failures on CI (v0.48.0 release run,
// then again with this watch immediate) were not this watch: the viewer's own
// Modal timer, BEHIND the question, pulled the focus back into the viewer.
// Modal now takes the focus only while it is the dialog in front.
watch(
  () => props.open,
  (open) => {
    if (open) void nextTick(() => keepBtn.value?.focus());
  },
  { immediate: true },
);
</script>

<template>
  <Modal :open="open" :locale="locale" :theme="theme" :title="t('draft.close.title')" size="sm" :busy="busy" @close="emit('cancel')">
    <div class="fe-draftq" data-testid="draft-close-dialog">
      <p class="fe-draftq__body">{{ t('draft.close.body', { name, folder }) }}</p>
      <p class="fe-draftq__hint">{{ t('draft.close.hint') }}</p>
      <p v-if="error" class="fe-form__error" role="alert">{{ error }}</p>
    </div>
    <template #actions>
      <div class="fe-draftq__actions">
        <button
          type="button"
          class="fe-btn fe-btn--danger-quiet"
          :disabled="busy"
          data-testid="draft-close-discard"
          @click="emit('discard')"
        >
          {{ t('draft.close.discard') }}
        </button>
        <span class="fe-draftq__gap" aria-hidden="true"></span>
        <button
          type="button"
          class="fe-btn"
          :disabled="busy"
          data-testid="draft-close-save"
          @click="emit('save')"
        >
          {{ t('draft.close.save') }}
        </button>
        <button
          ref="keepBtn"
          type="button"
          class="fe-btn fe-btn--primary"
          :disabled="busy"
          data-fe-autofocus
          data-testid="draft-close-keep"
          @click="emit('keep')"
        >
          {{ t('draft.close.keep') }}
        </button>
      </div>
    </template>
  </Modal>
</template>
