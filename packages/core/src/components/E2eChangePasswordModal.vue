<script setup lang="ts">
/**
 * E2eChangePasswordModal — a new password for an encrypted folder.
 *
 * docs/E2E-ENCRYPTION.md → "Changing the password". Two modes:
 *
 *   'change'  asked from the unlocked strip. Proof is the CURRENT password,
 *             or the recovery key instead ("I do not remember it").
 *   'reset'   right after an unlock with the recovery key. A recovery key
 *             that was used is a password that was lost or leaked, so a new
 *             one is set before the folder is used. There is no proof field
 *             (the key is in hand), and closing the dialog LOCKS the folder:
 *             the parent answers `close` in this mode by locking.
 *
 * What a change costs is decided by the folder and SAID before anything
 * happens:
 *   - a folder with its own key (every folder since v0.31) re-wraps one key;
 *     no file is touched, the recovery key and escrow keep working;
 *   - a folder from before v0.31 (`needsRekey`) has its key derived from the
 *     password, so every file's key is re-wrapped (the contents stay as they
 *     are), a new recovery key is issued unless the change is made with the
 *     current one, and it can be resumed if interrupted. That needs a tick.
 *   - a folder with its own key may be re-keyed on purpose (`rotate`), when
 *     the old password may be known to someone: the same re-wrap.
 *
 * Collects input and validates its shape; all crypto and every request is
 * the parent's (FileExplorer), which owns the marker and the key ring.
 */
import { computed, ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { E2E_MIN_PASSWORD_LEN, parseRecoveryKey } from '../lib/e2ecrypto';
import Modal from '../modals/Modal.vue';

export interface E2eChangePasswordPayload {
  /** Proof: the current password or the recovery key. Empty in 'reset' mode. */
  proof: { password: string } | { recoveryKey: string } | null;
  newPassword: string;
  /** Re-key: re-wrap every file key under a new folder key. */
  rotate: boolean;
}

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  mode: 'change' | 'reset';
  /** The folder's key IS its password's (v1 / fmk: kek): a re-key is required. */
  needsRekey: boolean;
  /** The folder has a recovery key slot (the "use the recovery key" link). */
  hasRecovery: boolean;
  /** The folder has an escrow slot; a re-key carries it over, and says so. */
  hasEscrow: boolean;
  busy?: boolean;
  /** Set by the parent after a failed attempt. */
  error?: string | null;
  /** Re-wrap progress, while a re-key runs. */
  progress?: { done: number; failed: number } | null;
  /**
   * wiring:e2 fxe — what gets the new password: an encrypted folder (the
   * default) or a single encrypted file (`.fxe`). A file's header is all
   * that changes, it has no folder key to replace, and the words say so.
   */
  subject?: 'folder' | 'file';
  /**
   * wiring:e2 vault — the folder is a vault: its folder key derives every key
   * in it, so it is never re-keyed (a new key is a new vault). Only the
   * password slot changes, and "re-key on purpose" is not offered.
   */
  vault?: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'submit', payload: E2eChangePasswordPayload): void;
}>();

const { t } = useLocale(() => props.locale);
const useRecovery = ref(false);
const current = ref('');
const recovery = ref('');
const pw1 = ref('');
const pw2 = ref('');
const rotate = ref(false);
const ack = ref(false);
const err = ref<string | null>(null);

watch(
  () => props.open,
  (v) => {
    if (!v) return;
    useRecovery.value = false;
    current.value = '';
    recovery.value = '';
    pw1.value = '';
    pw2.value = '';
    rotate.value = false;
    ack.value = false;
    err.value = null;
  },
);

const isFile = computed(() => props.subject === 'file');
/** A re-key is what this change will be. */
const rekey = computed(() => !isFile.value && !props.vault && (props.needsRekey || rotate.value));
const title = computed(() =>
  props.mode === 'reset'
    ? t('e2e.password.reset_title')
    : isFile.value
      ? t('e2e.fxe.password_title')
      : t('e2e.password.title'),
);
const shownError = computed(() => err.value ?? props.error ?? null);

function submit() {
  if (props.busy) return;
  let proof: E2eChangePasswordPayload['proof'] = null;
  if (props.mode === 'change') {
    if (useRecovery.value) {
      if (!parseRecoveryKey(recovery.value)) {
        err.value = t('e2e.password.bad_recovery');
        return;
      }
      proof = { recoveryKey: recovery.value };
    } else {
      if (!current.value) {
        err.value = t('e2e.password.current_required');
        return;
      }
      proof = { password: current.value };
    }
  }
  if (pw1.value.length < E2E_MIN_PASSWORD_LEN) {
    err.value = t('e2e.create.pw_short');
    return;
  }
  if (pw1.value !== pw2.value) {
    err.value = t('e2e.create.pw_mismatch');
    return;
  }
  if (proof && 'password' in proof && proof.password === pw1.value) {
    err.value = t('e2e.password.same');
    return;
  }
  if (rekey.value && !ack.value) {
    err.value = t('e2e.create.ack_required');
    return;
  }
  err.value = null;
  emit('submit', { proof, newPassword: pw1.value, rotate: rekey.value });
}
</script>

<template>
  <!-- In 'reset' mode a click outside does nothing and × / Escape LOCK the
       folder (the parent's answer to `close`): a used recovery key is a lost
       or leaked password, and the folder is not used until it has a new one. -->
  <Modal
    :open="open"
    :title="title"
    size="sm"
    :busy="busy"
    :close-on-backdrop="mode !== 'reset'"
    @close="emit('close')"
  >
    <form class="fe-e2e-form" data-testid="e2e-password-form" @submit.prevent="submit">
      <p v-if="mode === 'reset'" class="fe-e2e-pw__lead">{{ t('e2e.password.reset_lead') }}</p>

      <template v-if="mode === 'change'">
        <label v-if="!useRecovery" class="fe-field">
          <span class="fe-field__label">{{ t('e2e.password.current') }}</span>
          <input
            v-model="current"
            type="password"
            class="fe-input"
            autocomplete="current-password"
            data-testid="e2e-password-current"
            :disabled="busy"
          />
        </label>
        <label v-else class="fe-field">
          <span class="fe-field__label">{{ t('e2e.password.recovery') }}</span>
          <input
            v-model="recovery"
            type="text"
            class="fe-input fe-e2e-recover__key"
            autocomplete="off"
            spellcheck="false"
            :placeholder="t('e2e.recover.recovery_placeholder')"
            data-testid="e2e-password-recovery"
            :disabled="busy"
          />
        </label>
        <button
          v-if="hasRecovery"
          type="button"
          class="fe-e2e-optlink"
          :disabled="busy"
          @click="useRecovery = !useRecovery"
        >
          {{ useRecovery ? t('e2e.password.use_current') : t('e2e.password.use_recovery') }}
        </button>
      </template>

      <label class="fe-field">
        <span class="fe-field__label">{{ t('e2e.password.new') }}</span>
        <input
          v-model="pw1"
          type="password"
          class="fe-input"
          autocomplete="new-password"
          :placeholder="t('e2e.create.pw_placeholder')"
          data-testid="e2e-password-new"
          :disabled="busy"
        />
      </label>
      <label class="fe-field">
        <span class="fe-field__label">{{ t('e2e.create.pw2_label') }}</span>
        <input
          v-model="pw2"
          type="password"
          class="fe-input"
          autocomplete="new-password"
          data-testid="e2e-password-new2"
          :disabled="busy"
          @keydown.enter.prevent="submit"
        />
      </label>

      <!-- What it costs, before anything happens. -->
      <p v-if="!rekey" class="fe-e2e-pw__note" data-testid="e2e-password-cost">
        {{ isFile ? t('e2e.fxe.password_cost') : t('e2e.password.cheap_note') }}
      </p>
      <div v-else class="fe-e2e-warn" role="note" data-testid="e2e-password-cost">
        <strong>{{ needsRekey ? t('e2e.password.rekey_title') : t('e2e.password.rotate_title') }}</strong>
        <p>{{ needsRekey ? t('e2e.password.rekey_body') : t('e2e.password.rotate_body') }}</p>
        <p>{{ mode === 'reset' || useRecovery ? t('e2e.password.rekey_rk_kept') : t('e2e.password.rekey_rk_new') }}</p>
        <p v-if="hasEscrow">{{ t('e2e.password.rekey_escrow') }}</p>
      </div>
      <label v-if="!needsRekey && !isFile && !vault" class="fe-e2e-ack">
        <input v-model="rotate" type="checkbox" data-testid="e2e-password-rotate" :disabled="busy" />
        <span>{{ t('e2e.password.rotate_label') }}</span>
      </label>
      <label v-if="rekey" class="fe-e2e-ack">
        <input v-model="ack" type="checkbox" data-testid="e2e-password-ack" :disabled="busy" />
        <span>{{ t('e2e.password.rekey_ack') }}</span>
      </label>
      <p v-if="!rekey" class="fe-e2e-pw__note">{{ isFile ? t('e2e.fxe.password_old_copies') : t('e2e.password.old_copies') }}</p>

      <p v-if="progress" class="fe-e2e-upgrade__progress" role="status" data-testid="e2e-password-progress">
        {{ t('e2e.password.progress', { n: progress.done, failed: progress.failed }) }}
      </p>
      <p v-if="shownError" class="fe-form__error" role="alert" data-testid="e2e-password-error">{{ shownError }}</p>
    </form>
    <template #actions>
      <button type="button" class="fe-btn" :disabled="busy" @click="emit('close')">
        {{ mode === 'reset' ? t('e2e.password.lock_instead') : t('modal.newfolder.cancel') }}
      </button>
      <button
        type="button"
        class="fe-btn fe-btn--primary"
        :disabled="busy"
        data-testid="e2e-password-submit"
        @click="submit"
      >
        {{ busy ? t('e2e.password.busy') : t('e2e.password.submit') }}
      </button>
    </template>
  </Modal>
</template>
