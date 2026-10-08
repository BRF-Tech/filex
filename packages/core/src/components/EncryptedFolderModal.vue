<script setup lang="ts">
/**
 * EncryptedFolderModal — create an E2E-encrypted folder (wiring:e2), or
 * encrypt one that already exists (`existing`: its name; wiring:e2 convert).
 *
 * One dialog for both, because they ask the same things — a password, the
 * level, the acknowledgement — and the second only adds what encrypting in
 * place cannot undo: the plaintext copies filex already holds, and whether to
 * remove them (docs/E2E-ENCRYPTION.md → "Encrypting a folder you already
 * have").
 *
 * Collects folder name + password ×2 and an explicit "I understand there
 * is NO recovery" acknowledgement. The parent (FileExplorer) performs the
 * actual newfolder + marker-upload dance; this modal never touches the
 * network and never stores the password anywhere.
 *
 * Crypto scheme + threat model: docs/E2E-ENCRYPTION.md.
 */
import { computed, ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { E2E_DEFAULT_LEVEL, E2E_MIN_PASSWORD_LEN, type ChoosableLevel } from '../lib/e2ecrypto';
import { VAULT_DEFAULT_PACK_LOG2, VAULT_LARGE_PACK_LOG2 } from '../lib/e2evault/consts';
import Modal from '../modals/Modal.vue';
import E2eLevelPicker from './E2eLevelPicker.vue';
import ChoiceButtons, { type ChoiceOption } from './ChoiceButtons.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  /** True while the parent is creating the folder + uploading the marker. */
  busy?: boolean;
  /** Short id of this installation's E2E escrow key, when one is configured.
   *  Shown BEFORE the folder is created: escrow means the operator can open
   *  it without the password, and that is not a detail to discover later. */
  escrowKid?: string | null;
  /** Encrypt this existing folder in place (its name as people read it). */
  existing?: string | null;
  /** wiring:e2 vault — the server has the vault (`capabilities.e2e_vault`).
   *  Offered for a NEW folder only: an existing one is never converted. */
  vaultAvailable?: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (
    e: 'submit',
    payload: {
      name: string;
      password: string;
      level: ChoosableLevel;
      /** `existing` only: remove the versions / trash entries filex holds. */
      cleanup?: { versions: boolean; trash: boolean };
      /** wiring:e2 vault — level 'vault' only: the pack size, log2 (22 or 24). */
      packLog2?: number;
    },
  ): void;
}>();

const { t } = useLocale(() => props.locale);
const name = ref('');
const password = ref('');
const password2 = ref('');
const ack = ref(false);
/* wiring:e2 — the folder's encryption level. Level 1 (contents only) is the
 * default: names stay readable to WebDAV, the CLI and desktop sync, and every
 * filex since 0.31 opens the folder. Level 2 also encrypts the names; each
 * option says what it costs before anyone chooses (E2eLevelPicker). */
const level = ref<ChoosableLevel>(E2E_DEFAULT_LEVEL);
const err = ref<string | null>(null);
/* wiring:e2 convert — on by default: they are plaintext copies of what is
 * being encrypted. Off is a choice, and it is said what it leaves behind. */
const dropVersions = ref(true);
const dropTrash = ref(true);
/* wiring:e2 vault — the pack size, chosen when the vault is made and never
 * again: 4 MiB (the default) or 16 MiB. Every pack is that large on the
 * server, filled up with random bytes, so the choice is what the smallest
 * change costs against how many files the server sees. */
const packLog2 = ref<number>(VAULT_DEFAULT_PACK_LOG2);
const offerVault = computed(() => props.vaultAvailable === true && !props.existing);
const packOptions = computed<ChoiceOption[]>(() => [
  { value: String(VAULT_DEFAULT_PACK_LOG2), label: t('e2e.vault.pack_4'), help: t('e2e.vault.pack_4_help') },
  { value: String(VAULT_LARGE_PACK_LOG2), label: t('e2e.vault.pack_16'), help: t('e2e.vault.pack_16_help') },
]);

watch(
  () => props.open,
  (v) => {
    if (v) {
      name.value = '';
      password.value = '';
      password2.value = '';
      ack.value = false;
      level.value = E2E_DEFAULT_LEVEL;
      packLog2.value = VAULT_DEFAULT_PACK_LOG2;
      dropVersions.value = true;
      dropTrash.value = true;
      err.value = null;
    }
  },
);

function submit() {
  if (props.busy) return;
  const clean = props.existing ? props.existing : name.value.trim();
  if (!clean) {
    err.value = t('modal.newfolder.placeholder');
    return;
  }
  if (!props.existing && (/[\\/]/.test(clean) || clean === '.' || clean === '..' || clean.startsWith('.filex'))) {
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
  // An existing folder is never made a vault, whatever the picker held.
  const chosen: ChoosableLevel = props.existing && level.value === 'vault' ? E2E_DEFAULT_LEVEL : level.value;
  emit('submit', {
    name: clean,
    password: password.value,
    level: chosen,
    ...(props.existing ? { cleanup: { versions: dropVersions.value, trash: dropTrash.value } } : {}),
    ...(chosen === 'vault' ? { packLog2: packLog2.value } : {}),
  });
}
</script>

<template>
  <Modal
    :open="open"
    :title="existing ? t('e2e.convert.title', { name: existing }) : t('e2e.create.title')"
    size="sm"
    @close="emit('close')"
  >
    <!-- ⚠ Every field carries a visible <label>. They had placeholders only
         (v0.41.0 screenshot pass): the name of a field vanished the moment
         anything was typed into it, and on the two password fields — where
         what is typed is dots — nothing on screen said which box was which.
         A placeholder is a hint, not a name; the length rule stays one. -->
    <form class="fe-e2e-form" @submit.prevent="submit">
      <label v-if="!existing" class="fe-field">
        <span class="fe-field__label">{{ t('modal.newfolder.placeholder') }}</span>
        <input v-model="name" type="text" class="fe-input" autocomplete="off" :disabled="busy" />
      </label>
      <p v-else class="fe-e2e-names__hint" data-testid="e2e-convert-lead">{{ t('e2e.convert.lead') }}</p>
      <label class="fe-field">
        <span class="fe-field__label">{{ t('e2e.create.pw_label') }}</span>
        <input
          v-model="password"
          type="password"
          class="fe-input"
          :placeholder="t('e2e.create.pw_placeholder')"
          autocomplete="new-password"
          :disabled="busy"
        />
      </label>
      <label class="fe-field">
        <span class="fe-field__label">{{ t('e2e.create.pw2_label') }}</span>
        <input
          v-model="password2"
          type="password"
          class="fe-input"
          autocomplete="new-password"
          :disabled="busy"
          @keydown.enter.prevent="submit"
        />
      </label>
      <!-- wiring:e2 — the level: what the server will and will not see. -->
      <div class="fe-e2e-names" data-testid="e2e-create-names">
        <E2eLevelPicker v-model="level" :locale="locale" :disabled="busy" :vault="offerVault" />
        <p v-if="level !== 'vault'" class="fe-e2e-names__hint">{{ t('e2e.create.names_root_hint') }}</p>
      </div>
      <!-- wiring:e2 vault — the pack size, only for a vault. -->
      <div v-if="offerVault && level === 'vault'" class="fe-field" data-testid="e2e-vault-pack">
        <span class="fe-field__label" id="fe-e2e-vault-pack-label">{{ t('e2e.vault.pack_label') }}</span>
        <ChoiceButtons
          segmented
          :options="packOptions"
          :model-value="String(packLog2)"
          :disabled="busy"
          aria-labelledby="fe-e2e-vault-pack-label"
          testid-prefix="e2e-vault-pack"
          @update:model-value="(v) => (packLog2 = Number(v))"
        />
        <p class="fe-e2e-names__hint">{{ t('e2e.vault.pack_hint') }}</p>
      </div>
      <!-- wiring:e2 convert — what encrypting now cannot reach, said before. -->
      <div v-if="existing" class="fe-e2e-convert-past" data-testid="e2e-convert-past">
        <strong>{{ t('e2e.convert.past_title') }}</strong>
        <p>{{ t('e2e.convert.past_body') }}</p>
        <label class="fe-e2e-ack">
          <input v-model="dropVersions" type="checkbox" :disabled="busy" data-testid="e2e-convert-versions" />
          <span>{{ t('e2e.convert.drop_versions') }}</span>
        </label>
        <label class="fe-e2e-ack">
          <input v-model="dropTrash" type="checkbox" :disabled="busy" data-testid="e2e-convert-trash" />
          <span>{{ t('e2e.convert.drop_trash') }}</span>
        </label>
        <p class="fe-e2e-names__hint">{{ t('e2e.convert.past_outside') }}</p>
      </div>
      <div class="fe-e2e-warn" role="alert">
        <strong>{{ t('e2e.create.warn_title') }}</strong>
        <p>{{ t('e2e.create.warn_body') }}</p>
      </div>
      <!-- wiring:e2 recovery — disclosure, not a setting. The user cannot
           turn escrow off for their folder; they can only know about it. -->
      <div v-if="escrowKid" class="fe-e2e-rk__escrow" role="note">
        <strong>{{ t('e2e.create.escrow_title') }}</strong>
        <p>{{ t('e2e.create.escrow_body') }}</p>
      </div>
      <label class="fe-e2e-ack">
        <input v-model="ack" type="checkbox" :disabled="busy" data-testid="e2e-create-ack" />
        <span>{{ t('e2e.create.ack') }}</span>
      </label>
      <p v-if="err" class="fe-form__error">{{ err }}</p>
    </form>
    <template #actions>
      <button type="button" class="fe-btn" :disabled="busy" @click="emit('close')">
        {{ t('modal.newfolder.cancel') }}
      </button>
      <button type="button" class="fe-btn fe-btn--primary" :disabled="busy" @click="submit">
        {{ busy ? t('e2e.create.busy') : existing ? t('e2e.convert.submit') : t('e2e.create.create') }}
      </button>
    </template>
  </Modal>
</template>
