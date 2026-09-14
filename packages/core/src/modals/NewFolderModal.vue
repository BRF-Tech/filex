<script setup lang="ts">
import { ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { actionIconSvg } from '../lib/actionIcons'; /* ikon:emoji */
import Modal from './Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  /* wiring:e2 — hosts hide the encrypted option (e.g. already inside an
   * encrypted folder / feature-gated embeds). Default: shown. */
  encryptedOption?: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'submit', name: string): void;
  /* wiring:e2 — user picked "Create encrypted folder…" — the parent swaps
   * this modal for EncryptedFolderModal. */
  (e: 'encrypted'): void;
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
    err.value = t('modal.newfolder.invalid');
    return;
  }
  emit('submit', clean);
}
</script>

<template>
  <Modal :open="open" :title="t('modal.newfolder.title')" size="sm" @close="emit('close')">
    <form @submit.prevent="submit">
      <!-- A visible name, as in the encrypted-folder dialog this one opens. -->
      <label class="fe-field">
        <span class="fe-field__label">{{ t('modal.newfolder.placeholder') }}</span>
        <input
          v-model="name"
          type="text"
          class="fe-input"
          autocomplete="off"
          @keydown.enter.prevent="submit"
        />
      </label>
      <p v-if="err" class="fe-form__error">{{ err }}</p>
      <!-- wiring:e2 — encrypted-folder entry point lives inside the normal
           new-folder flow so every trigger (toolbar / context menu / palette)
           reaches it without extra wiring. -->
      <button
        v-if="encryptedOption !== false"
        type="button"
        class="fe-e2e-optlink"
        @click="emit('encrypted')"
      >
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
        <span class="fe-e2e-optlink__icon" aria-hidden="true" v-html="actionIconSvg('lock')"></span>
        {{ t('e2e.create.option') }}
      </button>
      <!-- /wiring:e2 -->
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
