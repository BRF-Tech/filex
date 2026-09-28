<script setup lang="ts">
/**
 * E2eFileUnlockModal — open, download or un-encrypt a single encrypted file
 * (`.fxe`, docs/E2E-ENCRYPTION.md → "Single encrypted files").
 *
 * One dialog, three intents, and the dialog says which:
 *   'open'      the password (or the recovery key), then the viewers;
 *   'download'  the same, then the save;
 *   'remove'    a WARNING first — the plaintext goes back to the server —
 *               and the password, unless the file is already open in this
 *               tab (then only the confirmation is asked).
 *
 * The password is checked in the parent (useE2eFiles), in this browser; this
 * component collects it and validates its shape, and never stores it.
 *
 * Three doors, as on an encrypted folder: the password, the recovery key, and
 * — when this file was sealed to THIS installation's escrow key — the escrow
 * key, whose use tells the file's owner. The dialog says so before the key is
 * pasted, and says why when the installation has escrow but this file does
 * not open with it.
 */
import { computed, ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { parseRecoveryKey } from '../lib/e2ecrypto';
import type { EscrowAvailability } from '../lib/e2ecrypto';
import type { FxeIntent } from '../composables/useE2eFiles';
import Modal from '../modals/Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  intent: FxeIntent;
  /** The name to call the file by (the real one when known). */
  fileName: string;
  /** The file is already open in this tab: no password is asked. */
  known?: boolean;
  hasRecovery?: boolean;
  busy?: boolean;
  error?: string | null;
  progress?: number | null;
  /** Unlocked; the browser wants one more click before its save dialog. */
  needsSave?: boolean;
  /** Whether the installation's escrow key opens this file (escrowAvailability). */
  escrowState?: EscrowAvailability;
  /** The escrow key this file was sealed to. */
  escrowKid?: string | null;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'submit', payload: { password?: string; recoveryKey?: string; escrowKey?: string }): void;
}>();

const { t } = useLocale(() => props.locale);
const mode = ref<'password' | 'recovery' | 'escrow'>('password');
const password = ref('');
const recovery = ref('');
const escrow = ref('');
const err = ref<string | null>(null);

watch(
  () => props.open,
  (v) => {
    if (!v) return;
    mode.value = 'password';
    password.value = '';
    recovery.value = '';
    escrow.value = '';
    err.value = null;
  },
);

/** The escrow door is offered only when the key can open THIS file. */
const escrowOffered = computed(() => props.escrowState === 'available');
/** The installation has an escrow key that does not open this file: said in
 *  words, or a missing door reads as a bug. */
const escrowNote = computed(() =>
  props.escrowState === 'predates'
    ? t('e2e.fxe.escrow_predates')
    : props.escrowState === 'other-key'
      ? t('e2e.fxe.escrow_other_key')
      : null,
);

const title = computed(() =>
  props.intent === 'remove'
    ? t('e2e.fxe.unlock_title_remove')
    : props.intent === 'download'
      ? t('e2e.fxe.unlock_title_download')
      : t('e2e.fxe.unlock_title_open'),
);
const primary = computed(() =>
  props.needsSave
    ? t('e2e.fxe.save_button')
    : props.intent === 'remove'
      ? t('e2e.fxe.unlock_remove')
      : props.intent === 'download'
        ? t('e2e.fxe.unlock_download')
        : t('e2e.fxe.unlock_open'),
);
const shownError = computed(() => err.value ?? props.error ?? null);

function submit() {
  if (props.busy) return;
  err.value = null;
  if (props.known || props.needsSave) {
    emit('submit', {});
    return;
  }
  if (mode.value === 'escrow') {
    if (!escrow.value.trim()) {
      err.value = t('e2e.recover.escrow_required');
      return;
    }
    emit('submit', { escrowKey: escrow.value });
    return;
  }
  if (mode.value === 'recovery') {
    if (!parseRecoveryKey(recovery.value)) {
      err.value = t('e2e.password.bad_recovery');
      return;
    }
    emit('submit', { recoveryKey: recovery.value });
    return;
  }
  if (!password.value) {
    err.value = t('e2e.password.current_required');
    return;
  }
  emit('submit', { password: password.value });
}
</script>

<template>
  <Modal :open="open" :title="title" size="sm" :busy="busy" @close="emit('close')">
    <form class="fe-e2e-form" data-testid="fxe-unlock-form" :data-intent="intent" @submit.prevent="submit">
      <p class="fe-e2e-rk__lead">{{ t('e2e.fxe.unlock_lead', { name: fileName }) }}</p>
      <div v-if="intent === 'remove'" class="fe-e2e-warn" role="alert" data-testid="fxe-remove-warn">
        <strong>{{ t('e2e.fxe.remove_warn_title') }}</strong>
        <p>{{ t('e2e.fxe.remove_warn_body') }}</p>
      </div>
      <p v-if="needsSave" class="fe-e2e-pw__note" role="status" data-testid="fxe-unlock-save-again">
        {{ t('e2e.fxe.save_again') }}
      </p>
      <p v-else-if="known" class="fe-e2e-pw__note">{{ t('e2e.fxe.known_note') }}</p>
      <template v-else>
        <label v-if="mode === 'password'" class="fe-field">
          <span class="fe-field__label">{{ t('e2e.fxe.pw_label') }}</span>
          <input
            v-model="password"
            type="password"
            class="fe-input"
            autocomplete="current-password"
            data-testid="fxe-unlock-pw"
            :disabled="busy"
            @keydown.enter.prevent="submit"
          />
        </label>
        <label v-else-if="mode === 'recovery'" class="fe-field">
          <span class="fe-field__label">{{ t('e2e.password.recovery') }}</span>
          <input
            v-model="recovery"
            type="text"
            class="fe-input fe-e2e-recover__key"
            autocomplete="off"
            spellcheck="false"
            :placeholder="t('e2e.recover.recovery_placeholder')"
            data-testid="fxe-unlock-recovery"
            :disabled="busy"
            @keydown.enter.prevent="submit"
          />
        </label>
        <template v-else>
          <div class="fe-e2e-warn" role="alert" data-testid="fxe-unlock-escrow-warn">
            <strong>{{ t('e2e.fxe.escrow_warn_title') }}</strong>
            <p>{{ t('e2e.fxe.escrow_warn_body') }}</p>
            <p v-if="escrowKid">
              {{ t('e2e.recover.escrow_kid') }}: <code>{{ escrowKid }}</code>
            </p>
          </div>
          <textarea
            v-model="escrow"
            class="fe-input fe-e2e-recover__escrow"
            rows="5"
            :placeholder="t('e2e.recover.escrow_placeholder')"
            autocomplete="off"
            spellcheck="false"
            data-testid="fxe-unlock-escrow"
            :disabled="busy"
          ></textarea>
        </template>
        <button
          v-if="mode !== 'password' || hasRecovery !== false"
          type="button"
          class="fe-e2e-optlink"
          data-testid="fxe-unlock-toggle"
          :disabled="busy"
          @click="mode = mode === 'password' ? 'recovery' : 'password'"
        >
          {{ mode === 'password' ? t('e2e.password.use_recovery') : t('e2e.fxe.use_password') }}
        </button>
        <button
          v-if="escrowOffered && mode !== 'escrow'"
          type="button"
          class="fe-e2e-optlink"
          data-testid="fxe-unlock-escrow-toggle"
          :disabled="busy"
          @click="mode = 'escrow'"
        >
          {{ t('e2e.fxe.use_escrow') }}
        </button>
        <!-- Said where someone looks for another way in: an operator who knows
             the installation has escrow, and finds no escrow door here. -->
        <p v-if="escrowNote && mode === 'recovery'" class="fe-e2e-recover__note" data-testid="fxe-unlock-escrow-note">
          {{ escrowNote }}
        </p>
      </template>
      <p v-if="busy && progress !== null && progress !== undefined" class="fe-e2e-upgrade__progress" role="status">
        {{ t('e2e.fxe.progress', { percent: progress }) }}
      </p>
      <p v-if="shownError" class="fe-form__error" role="alert" data-testid="fxe-unlock-error">{{ shownError }}</p>
    </form>
    <template #actions>
      <button type="button" class="fe-btn" :disabled="busy" @click="emit('close')">
        {{ t('modal.newfolder.cancel') }}
      </button>
      <button
        type="button"
        class="fe-btn fe-btn--primary"
        :class="{ 'fe-btn--danger': intent === 'remove' && !needsSave }"
        :disabled="busy"
        data-testid="fxe-unlock-submit"
        @click="submit"
      >
        {{ busy ? t('e2e.fxe.unlock_busy') : primary }}
      </button>
    </template>
  </Modal>
</template>
