<script setup lang="ts">
/**
 * E2eFileEncryptModal — encrypt ONE file on its own, as a `.fxe`
 * (docs/E2E-ENCRYPTION.md → "Single encrypted files").
 *
 * Collects the password twice, whether to hide the file name too, and an
 * explicit acknowledgement; says BEFORE anything happens what the server has
 * already seen of the file and what stays behind (the trash, version history,
 * a thumbnail, backups), and — when the installation has escrow — that its
 * operator holds a key. All crypto and every request is the parent's
 * (useE2eFiles); this dialog never stores the password.
 *
 * An administrator is also offered "delete the original for good": its old
 * versions and its trash entry go as soon as the encrypted copy is saved. When
 * the file is not theirs, the dialog names its owner and asks for a second,
 * separate confirmation — deleting someone else's file for good is not a side
 * effect to tick past.
 */
import { computed, ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { E2E_MIN_PASSWORD_LEN } from '../lib/e2ecrypto';
import Modal from '../modals/Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  /** The plain file's name. */
  fileName: string;
  busy?: boolean;
  /** Set by the parent after a failed attempt. */
  error?: string | null;
  /** 0–100 while encrypting and uploading. */
  progress?: number | null;
  /** The installation's escrow key id, when escrow is on. */
  escrowKid?: string | null;
  /** The viewer administers this installation: "delete for good" is offered. */
  canPurge?: boolean;
  /** The file is not the viewer's (someone else's, or nobody's on record). */
  notOwner?: boolean;
  /** Its owner's name, when it has one on record. */
  ownerName?: string;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'submit', payload: { password: string; hideName: boolean; purge: boolean }): void;
}>();

const { t } = useLocale(() => props.locale);
const pw1 = ref('');
const pw2 = ref('');
const hideName = ref(false);
const ack = ref(false);
const purge = ref(false);
const purgeOthersAck = ref(false);
const err = ref<string | null>(null);

watch(
  () => props.open,
  (v) => {
    if (!v) return;
    pw1.value = '';
    pw2.value = '';
    hideName.value = false;
    ack.value = false;
    purge.value = false;
    purgeOthersAck.value = false;
    err.value = null;
  },
);

const storedExample = computed(() => (hideName.value ? 'encrypted-xxxxxxxx.fxe' : `${props.fileName}.fxe`));
const shownError = computed(() => err.value ?? props.error ?? null);

function submit() {
  if (props.busy) return;
  if (pw1.value.length < E2E_MIN_PASSWORD_LEN) {
    err.value = t('e2e.create.pw_short');
    return;
  }
  if (pw1.value !== pw2.value) {
    err.value = t('e2e.create.pw_mismatch');
    return;
  }
  if (!ack.value) {
    err.value = t('e2e.create.ack_required');
    return;
  }
  const purging = !!props.canPurge && purge.value;
  if (purging && props.notOwner && !purgeOthersAck.value) {
    err.value = t('e2e.fxe.purge_others_required');
    return;
  }
  err.value = null;
  emit('submit', { password: pw1.value, hideName: hideName.value, purge: purging });
}
</script>

<template>
  <Modal :open="open" :title="t('e2e.fxe.encrypt_title')" size="sm" :busy="busy" @close="emit('close')">
    <form class="fe-e2e-form" data-testid="fxe-encrypt-form" @submit.prevent="submit">
      <p class="fe-e2e-rk__lead">{{ t('e2e.fxe.encrypt_lead', { name: fileName }) }}</p>
      <label class="fe-field">
        <span class="fe-field__label">{{ t('e2e.fxe.pw_label') }}</span>
        <input
          v-model="pw1"
          type="password"
          class="fe-input"
          :placeholder="t('e2e.create.pw_placeholder')"
          autocomplete="new-password"
          data-testid="fxe-encrypt-pw"
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
          data-testid="fxe-encrypt-pw2"
          :disabled="busy"
          @keydown.enter.prevent="submit"
        />
      </label>
      <div class="fe-e2e-names">
        <label class="fe-e2e-ack">
          <input v-model="hideName" type="checkbox" :disabled="busy" data-testid="fxe-encrypt-hide" />
          <span>{{ t('e2e.fxe.hide_label') }}</span>
        </label>
        <p class="fe-e2e-names__hint" data-testid="fxe-encrypt-hide-hint">
          {{ hideName ? t('e2e.fxe.hide_on_hint', { stored: storedExample }) : t('e2e.fxe.hide_off_hint', { stored: storedExample }) }}
        </p>
      </div>
      <!-- What the server already saw. Said before, not after: the original
           was on the server in the clear, and encrypting a copy does not
           reach the trash, the version history, a thumbnail or a backup. -->
      <div class="fe-e2e-seen" role="note" data-testid="fxe-encrypt-seen">
        <strong>{{ t('e2e.fxe.seen_title') }}</strong>
        <p>{{ t('e2e.fxe.seen_lead') }}</p>
        <ul>
          <li>{{ canPurge ? t('e2e.fxe.seen_trash_admin') : t('e2e.fxe.seen_trash') }}</li>
          <li>{{ canPurge ? t('e2e.fxe.seen_versions_admin') : t('e2e.fxe.seen_versions') }}</li>
          <li>{{ t('e2e.fxe.seen_thumb') }}</li>
          <li>{{ t('e2e.fxe.seen_backups') }}</li>
        </ul>
      </div>
      <!-- An administrator's "delete for good". Unticked by default: the
           trash is how a mistake is undone, and giving that up is a choice. -->
      <div v-if="canPurge" class="fe-e2e-names" data-testid="fxe-encrypt-purge-box">
        <label class="fe-e2e-ack">
          <input v-model="purge" type="checkbox" :disabled="busy" data-testid="fxe-encrypt-purge" />
          <span>{{ t('e2e.fxe.purge_label') }}</span>
        </label>
        <p class="fe-e2e-names__hint">{{ t('e2e.fxe.purge_hint') }}</p>
        <label v-if="purge && notOwner" class="fe-e2e-ack fe-e2e-warn" data-testid="fxe-encrypt-purge-others">
          <input v-model="purgeOthersAck" type="checkbox" :disabled="busy" data-testid="fxe-encrypt-purge-others-ack" />
          <span>{{ ownerName ? t('e2e.fxe.purge_others', { owner: ownerName }) : t('e2e.fxe.purge_others_unknown') }}</span>
        </label>
      </div>
      <div class="fe-e2e-warn" role="alert">
        <strong>{{ t('e2e.create.warn_title') }}</strong>
        <p>{{ t('e2e.fxe.warn_body') }}</p>
      </div>
      <div v-if="escrowKid" class="fe-e2e-rk__escrow" role="note" data-testid="fxe-encrypt-escrow">
        <strong>{{ t('e2e.create.escrow_title') }}</strong>
        <p>{{ t('e2e.fxe.escrow_body') }}</p>
      </div>
      <label class="fe-e2e-ack">
        <input v-model="ack" type="checkbox" :disabled="busy" data-testid="fxe-encrypt-ack" />
        <span>{{ t('e2e.create.ack') }}</span>
      </label>
      <p v-if="busy && progress !== null && progress !== undefined" class="fe-e2e-upgrade__progress" role="status">
        {{ t('e2e.fxe.encrypt_busy', { percent: progress }) }}
      </p>
      <p v-if="shownError" class="fe-form__error" role="alert" data-testid="fxe-encrypt-error">{{ shownError }}</p>
    </form>
    <template #actions>
      <button type="button" class="fe-btn" :disabled="busy" @click="emit('close')">
        {{ t('modal.newfolder.cancel') }}
      </button>
      <button type="button" class="fe-btn fe-btn--primary" :disabled="busy" data-testid="fxe-encrypt-submit" @click="submit">
        {{ busy ? t('e2e.fxe.unlock_busy') : t('e2e.fxe.encrypt_submit') }}
      </button>
    </template>
  </Modal>
</template>
