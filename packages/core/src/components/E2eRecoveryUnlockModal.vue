<script setup lang="ts">
/**
 * E2eRecoveryUnlockModal — open an encrypted folder without its password.
 *
 * Two doors, and the dialog is explicit about which one you are using
 * because they are not equivalent:
 *
 *   - the USER RECOVERY KEY, shown once when the folder was created. Opens
 *     the folder and nobody is told, because it is your key.
 *   - the ESCROW KEY, held by the operator of this installation. Opening a
 *     folder with it NOTIFIES the folder's owner. The dialog says so before
 *     the key is typed, not after.
 *
 * The component collects input and validates its shape. All crypto and all
 * network calls happen in the parent (FileExplorer), which owns the marker
 * and the key ring — same division as the password lock screen.
 */
import { computed, ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { parseRecoveryKey } from '../lib/e2ecrypto';
import Modal from '../modals/Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  /** The folder has a user recovery key slot (v2 markers created since 0.31). */
  hasRecovery: boolean;
  /** The folder has an escrow slot AND this installation has escrow enabled. */
  hasEscrow: boolean;
  /** Short id of the escrow key the folder was sealed to. */
  escrowKid?: string | null;
  busy?: boolean;
  /** Set by the parent after a failed attempt. */
  error?: string | null;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'submit', payload: { mode: 'recovery' | 'escrow'; value: string }): void;
}>();

const { t } = useLocale(() => props.locale);
const mode = ref<'recovery' | 'escrow'>('recovery');
const recoveryValue = ref('');
const escrowValue = ref('');
const localErr = ref<string | null>(null);

watch(
  () => props.open,
  (v) => {
    if (v) {
      mode.value = props.hasRecovery ? 'recovery' : 'escrow';
      recoveryValue.value = '';
      escrowValue.value = '';
      localErr.value = null;
    }
  },
);

/** A folder with neither slot is a pre-0.31 folder: the password is the
 *  only way in, and saying so is more useful than an empty dialog. */
const nothingAvailable = computed(() => !props.hasRecovery && !props.hasEscrow);

const shownError = computed(() => localErr.value || props.error || null);

function submit() {
  if (props.busy || nothingAvailable.value) return;
  localErr.value = null;
  if (mode.value === 'recovery') {
    // Check the shape locally so a typo reads as a typo rather than as a
    // wrong key — the two failures call for different next steps.
    if (!parseRecoveryKey(recoveryValue.value)) {
      localErr.value = t('e2e.recover.bad_format');
      return;
    }
    emit('submit', { mode: 'recovery', value: recoveryValue.value });
    return;
  }
  if (!escrowValue.value.trim()) {
    localErr.value = t('e2e.recover.escrow_required');
    return;
  }
  emit('submit', { mode: 'escrow', value: escrowValue.value });
}
</script>

<template>
  <Modal :open="open" :title="t('e2e.recover.title')" size="sm" @close="emit('close')">
    <div class="fe-e2e-recover">
      <p v-if="nothingAvailable" class="fe-e2e-recover__none">
        {{ t('e2e.recover.none') }}
      </p>

      <template v-else>
        <div v-if="hasRecovery && hasEscrow" class="fe-e2e-recover__tabs" role="tablist">
          <button
            type="button"
            role="tab"
            class="fe-e2e-recover__tab"
            :class="{ 'fe-e2e-recover__tab--on': mode === 'recovery' }"
            :aria-selected="mode === 'recovery'"
            @click="mode = 'recovery'"
          >
            {{ t('e2e.recover.tab_recovery') }}
          </button>
          <button
            type="button"
            role="tab"
            class="fe-e2e-recover__tab"
            :class="{ 'fe-e2e-recover__tab--on': mode === 'escrow' }"
            :aria-selected="mode === 'escrow'"
            @click="mode = 'escrow'"
          >
            {{ t('e2e.recover.tab_escrow') }}
          </button>
        </div>

        <form v-if="mode === 'recovery'" class="fe-e2e-form" @submit.prevent="submit">
          <p class="fe-e2e-recover__hint">{{ t('e2e.recover.recovery_hint') }}</p>
          <input
            v-model="recoveryValue"
            type="text"
            class="fe-input fe-e2e-recover__key"
            :placeholder="t('e2e.recover.recovery_placeholder')"
            autocomplete="off"
            spellcheck="false"
            :disabled="busy"
          />
        </form>

        <form v-else class="fe-e2e-form" @submit.prevent="submit">
          <div class="fe-e2e-warn" role="alert">
            <strong>{{ t('e2e.recover.escrow_warn_title') }}</strong>
            <p>{{ t('e2e.recover.escrow_warn_body') }}</p>
          </div>
          <p v-if="escrowKid" class="fe-e2e-recover__hint">
            {{ t('e2e.recover.escrow_kid') }}: <code>{{ escrowKid }}</code>
          </p>
          <textarea
            v-model="escrowValue"
            class="fe-input fe-e2e-recover__escrow"
            rows="5"
            :placeholder="t('e2e.recover.escrow_placeholder')"
            autocomplete="off"
            spellcheck="false"
            :disabled="busy"
          ></textarea>
        </form>
      </template>

      <p v-if="shownError" class="fe-form__error">{{ shownError }}</p>
    </div>
    <template #actions>
      <button type="button" class="fe-btn" :disabled="busy" @click="emit('close')">
        {{ t('modal.newfolder.cancel') }}
      </button>
      <button
        type="button"
        class="fe-btn fe-btn--primary"
        :disabled="busy || nothingAvailable"
        @click="submit"
      >
        {{ busy ? t('e2e.recover.busy') : t('e2e.recover.unlock') }}
      </button>
    </template>
  </Modal>
</template>
