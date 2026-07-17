<script setup lang="ts">
/**
 * EncryptedFolderModal — create an E2E-encrypted folder (wiring:e2).
 *
 * Collects folder name + password ×2 and an explicit "I understand there
 * is NO recovery" acknowledgement. The parent (FileExplorer) performs the
 * actual newfolder + marker-upload dance; this modal never touches the
 * network and never stores the password anywhere.
 *
 * Crypto scheme + threat model: docs/E2E-ENCRYPTION.md.
 */
import { ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { E2E_MIN_PASSWORD_LEN } from '../lib/e2ecrypto';
import Modal from '../modals/Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  /** True while the parent is creating the folder + uploading the marker. */
  busy?: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'submit', payload: { name: string; password: string }): void;
}>();

const { t } = useLocale(() => props.locale);
const name = ref('');
const password = ref('');
const password2 = ref('');
const ack = ref(false);
const err = ref<string | null>(null);

watch(
  () => props.open,
  (v) => {
    if (v) {
      name.value = '';
      password.value = '';
      password2.value = '';
      ack.value = false;
      err.value = null;
    }
  },
);

function submit() {
  if (props.busy) return;
  const clean = name.value.trim();
  if (!clean) {
    err.value = t('modal.newfolder.placeholder');
    return;
  }
  if (/[\\/]/.test(clean) || clean === '.' || clean === '..' || clean.startsWith('.filex')) {
    err.value = t('e2e.create.bad_name');
    return;
  }
  if (password.value.length < E2E_MIN_PASSWORD_LEN) {
    err.value = t('e2e.create.pw_short');
    return;
  }
  if (password.value !== password2.value) {
    err.value = t('e2e.create.pw_mismatch');
    return;
  }
  if (!ack.value) {
    err.value = t('e2e.create.ack_required');
    return;
  }
  err.value = null;
  emit('submit', { name: clean, password: password.value });
}
</script>

<template>
  <Modal :open="open" :title="t('e2e.create.title')" size="sm" @close="emit('close')">
    <form class="fe-e2e-form" @submit.prevent="submit">
      <input
        v-model="name"
        type="text"
        class="fe-input"
        :placeholder="t('modal.newfolder.placeholder')"
        autocomplete="off"
        :disabled="busy"
      />
      <input
        v-model="password"
        type="password"
        class="fe-input"
        :placeholder="t('e2e.create.pw_placeholder')"
        autocomplete="new-password"
        :disabled="busy"
      />
      <input
        v-model="password2"
        type="password"
        class="fe-input"
        :placeholder="t('e2e.create.pw2_placeholder')"
        autocomplete="new-password"
        :disabled="busy"
        @keydown.enter.prevent="submit"
      />
      <div class="fe-e2e-warn" role="alert">
        <strong>{{ t('e2e.create.warn_title') }}</strong>
        <p>{{ t('e2e.create.warn_body') }}</p>
      </div>
      <label class="fe-e2e-ack">
        <input v-model="ack" type="checkbox" :disabled="busy" />
        <span>{{ t('e2e.create.ack') }}</span>
      </label>
      <p v-if="err" class="fe-form__error">{{ err }}</p>
    </form>
    <template #actions>
      <button type="button" class="fe-btn" :disabled="busy" @click="emit('close')">
        {{ t('modal.newfolder.cancel') }}
      </button>
      <button type="button" class="fe-btn fe-btn--primary" :disabled="busy" @click="submit">
        {{ busy ? t('e2e.create.busy') : t('e2e.create.create') }}
      </button>
    </template>
  </Modal>
</template>
