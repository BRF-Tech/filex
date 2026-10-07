<script setup lang="ts">
/**
 * OutsideChangeModal — the document changed outside this editor while the
 * editor has changes of its own (issue #184). Which version stays?
 *
 *   Keep the outside version  this editor's changes are dropped and the new
 *                             version is loaded;
 *   Write mine                this editor's version replaces it when saved;
 *   Keep both                 the outside version stays, this editor's is
 *                             saved beside it as a conflict copy.
 *
 * ⚠ Escape and a click outside put the question AWAY, they answer nothing:
 * the viewer keeps a line under its bar to bring it back (PreviewModal), and
 * until an answer comes nothing is written over either version. Enter takes
 * "Keep both", the one answer that loses nothing.
 */
import { nextTick, ref, watch } from 'vue';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import type { OutsideChoice } from '../lib/outsideChange';
import { useLocale } from '../composables/useLocale';
import Modal from './Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  /** The viewer's theme: this dialog sits beside its card, not inside it. */
  theme?: ThemeMode;
  /** The document's name. */
  name: string;
}>();

const emit = defineEmits<{
  (e: 'dismiss'): void;
  (e: 'choose', choice: Exclude<OutsideChoice, 'reloaded'>): void;
}>();

const { t } = useLocale(() => props.locale);

const bothBtn = ref<HTMLButtonElement | null>(null);
watch(
  () => props.open,
  (open) => {
    if (open) void nextTick(() => bothBtn.value?.focus());
  },
  { immediate: true },
);
</script>

<template>
  <Modal :open="open" :locale="locale" :theme="theme" :title="t('outside.title')" size="sm" @close="emit('dismiss')">
    <div class="fe-draftq" data-testid="outside-change-dialog">
      <p class="fe-draftq__body">{{ t('outside.body', { name }) }}</p>
      <ul class="fe-outsideq__list">
        <li><strong>{{ t('outside.theirs') }}</strong> - {{ t('outside.theirs_hint') }}</li>
        <li><strong>{{ t('outside.mine') }}</strong> - {{ t('outside.mine_hint') }}</li>
        <li><strong>{{ t('outside.both') }}</strong> - {{ t('outside.both_hint') }}</li>
      </ul>
      <p class="fe-draftq__hint">{{ t('outside.hint') }}</p>
    </div>
    <template #actions>
      <div class="fe-draftq__actions">
        <button
          type="button"
          class="fe-btn fe-btn--danger-quiet"
          data-testid="outside-keep-theirs"
          @click="emit('choose', 'theirs')"
        >
          {{ t('outside.theirs') }}
        </button>
        <span class="fe-draftq__gap" aria-hidden="true"></span>
        <button
          type="button"
          class="fe-btn"
          data-testid="outside-write-mine"
          @click="emit('choose', 'mine')"
        >
          {{ t('outside.mine') }}
        </button>
        <button
          ref="bothBtn"
          type="button"
          class="fe-btn fe-btn--primary"
          data-fe-autofocus
          data-testid="outside-keep-both"
          @click="emit('choose', 'both')"
        >
          {{ t('outside.both') }}
        </button>
      </div>
    </template>
  </Modal>
</template>
