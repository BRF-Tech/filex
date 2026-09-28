<script setup lang="ts">
/**
 * E2eSettingsModal — an unlocked encrypted folder's encryption settings.
 *
 * The encryption LEVEL is a property of the folder (docs/E2E-ENCRYPTION.md →
 * "Encryption levels"): chosen when it is encrypted, and changed here, by a
 * deliberate act — never by an offer that pops up after an unlock. The same
 * place holds the folder's other keys: its password and, where the
 * installation has one, the operator's escrow slot.
 *
 * Levels are a list the dialog walks, not three hard-coded buttons: level 3
 * (the vault) is designed and not built, so it is not in the list and not
 * shown — a choice that does not work is not offered.
 *
 * Collects the choice; every action is the parent's (FileExplorer).
 */
import { computed, ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { EncryptionLevel } from '../lib/e2ecrypto';
import { useLocale } from '../composables/useLocale';
import Modal from '../modals/Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  level: EncryptionLevel;
  /** A v2 folder that may move from contents to contents + names. */
  canRaise: boolean;
  /** Names found in view that were never encrypted (a WebDAV write). */
  plainNamed: number;
  /** Escrow: 'n/a' (nothing to do), 'offer' / 'declined' (the way back). */
  escrowState: 'n/a' | 'offer' | 'declined';
  busy?: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'raise-level'): void;
  (e: 'fix-names'): void;
  (e: 'change-password'): void;
  (e: 'escrow'): void;
}>();

const { t } = useLocale(() => props.locale);
const confirming = ref(false);
const ack = ref(false);

watch(
  () => props.open,
  (v) => {
    if (!v) return;
    confirming.value = false;
    ack.value = false;
  },
);

const levelLabel = computed(() =>
  props.level === 'content' ? t('e2e.level.content') : t('e2e.level.names'),
);
const levelHint = computed(() =>
  props.level === 'content' ? t('e2e.level.content_hint') : t('e2e.level.names_hint'),
);

function raise() {
  if (!ack.value || props.busy) return;
  emit('raise-level');
}
</script>

<template>
  <Modal :open="open" :title="t('e2e.settings.title')" size="sm" :busy="busy" @close="emit('close')">
    <div class="fe-e2e-settings" data-testid="e2e-settings">
      <section class="fe-e2e-settings__section">
        <h3 class="fe-e2e-settings__head">{{ t('e2e.settings.level') }}</h3>
        <p class="fe-e2e-settings__value" data-testid="e2e-settings-level">
          {{ levelLabel }}<span v-if="level === 'pending'"> — {{ t('e2e.level.pending') }}</span>
        </p>
        <p class="fe-e2e-settings__hint">{{ levelHint }}</p>

        <template v-if="canRaise">
          <button
            v-if="!confirming"
            type="button"
            class="fe-btn"
            data-testid="e2e-settings-raise"
            :disabled="busy"
            @click="confirming = true"
          >
            {{ t('e2e.level.change') }}
          </button>
          <div v-else class="fe-e2e-warn" role="note" data-testid="e2e-settings-raise-confirm">
            <strong>{{ t('e2e.level.raise_title') }}</strong>
            <p>{{ t('e2e.level.raise_body') }}</p>
            <p>{{ t('e2e.names.offer_cost') }}</p>
            <label class="fe-e2e-ack">
              <input v-model="ack" type="checkbox" data-testid="e2e-settings-raise-ack" :disabled="busy" />
              <span>{{ t('e2e.level.raise_ack') }}</span>
            </label>
            <div class="fe-e2e-settings__row">
              <button type="button" class="fe-btn" :disabled="busy" @click="confirming = false">
                {{ t('modal.newfolder.cancel') }}
              </button>
              <button
                type="button"
                class="fe-btn fe-btn--primary"
                data-testid="e2e-settings-raise-go"
                :disabled="!ack || busy"
                @click="raise"
              >
                {{ t('e2e.level.raise_go') }}
              </button>
            </div>
          </div>
        </template>
        <button
          v-if="level !== 'content' && plainNamed > 0"
          type="button"
          class="fe-btn"
          data-testid="e2e-settings-fix-names"
          :disabled="busy"
          @click="emit('fix-names')"
        >
          {{ t('e2e.names.strip_fix', { n: plainNamed }) }}
        </button>
      </section>

      <section class="fe-e2e-settings__section">
        <h3 class="fe-e2e-settings__head">{{ t('e2e.settings.password') }}</h3>
        <button type="button" class="fe-btn" data-testid="e2e-password-open" :disabled="busy" @click="emit('change-password')">
          {{ t('e2e.password.strip_action') }}
        </button>
      </section>

      <section v-if="escrowState !== 'n/a'" class="fe-e2e-settings__section">
        <h3 class="fe-e2e-settings__head">{{ t('e2e.settings.escrow') }}</h3>
        <button type="button" class="fe-btn" :disabled="busy" @click="emit('escrow')">
          {{ t('e2e.escrowoffer.strip_action') }}
        </button>
      </section>
    </div>
    <template #actions>
      <button type="button" class="fe-btn" :disabled="busy" @click="emit('close')">
        {{ t('e2e.settings.close') }}
      </button>
    </template>
  </Modal>
</template>
