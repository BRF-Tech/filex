<script setup lang="ts">
/**
 * RecoveryKeyModal — shows a folder recovery key, exactly once (wiring:e2).
 *
 * This is the only moment the key exists anywhere outside the user's own
 * records. filex does not store it, cannot recompute it, and will never be
 * able to show it again — so the dialog is deliberately hard to dismiss by
 * accident: no backdrop close, no cancel button, and an acknowledgement the
 * user has to tick.
 *
 * ⚠ The key must not leave this component. No logging, no analytics, no
 * emit carrying its value. Copy and download are both local.
 *
 * Crypto scheme: docs/E2E-ENCRYPTION.md and lib/e2ecrypto.ts.
 */
import { computed, ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import Modal from '../modals/Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  /** The key itself. Shown, copied, downloaded — never emitted or stored. */
  recoveryKey: string;
  /** Folder name, for the downloaded file and the dialog copy. */
  folderName?: string;
  /** Set when this installation holds an escrow key for the folder too. */
  escrowKid?: string | null;
  /** 'created' = a new folder; 'upgraded' = an existing folder gained recovery. */
  variant?: 'created' | 'upgraded';
}>();

const emit = defineEmits<{ (e: 'close'): void }>();

const { t } = useLocale(() => props.locale);
const ack = ref(false);
const copied = ref(false);

watch(
  () => props.open,
  (v) => {
    if (v) {
      ack.value = false;
      copied.value = false;
    }
  },
);

const title = computed(() =>
  props.variant === 'upgraded' ? t('e2e.recovery.title_upgraded') : t('e2e.recovery.title'),
);

async function copy() {
  try {
    await navigator.clipboard.writeText(props.recoveryKey);
    copied.value = true;
    window.setTimeout(() => (copied.value = false), 2000);
  } catch {
    // Clipboard permission denied (or a non-secure origin). The key is on
    // screen and selectable, so this is a convenience, not the only route.
    copied.value = false;
  }
}

function download() {
  const name = props.folderName || 'folder';
  const body =
    `filex recovery key\n` +
    `folder: ${name}\n` +
    `\n${props.recoveryKey}\n\n` +
    `This key opens the encrypted folder without its password.\n` +
    `Anyone holding it can read the folder. Store it like a password.\n` +
    `filex does not keep a copy and cannot show it again.\n`;
  const url = URL.createObjectURL(new Blob([body], { type: 'text/plain' }));
  const a = document.createElement('a');
  a.href = url;
  a.download = `filex-recovery-${name}.txt`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 30_000);
}

function done() {
  if (!ack.value) return;
  emit('close');
}
</script>

<template>
  <!-- No backdrop close, and ESC only lands once the box is ticked: the key
       cannot be shown again, so an accidental dismissal is data loss. -->
  <Modal :open="open" :title="title" size="sm" :close-on-backdrop="false" @close="done">
    <div class="fe-e2e-rk">
      <p class="fe-e2e-rk__lead">
        {{ variant === 'upgraded' ? t('e2e.recovery.lead_upgraded') : t('e2e.recovery.lead') }}
      </p>

      <output class="fe-e2e-rk__key" aria-label="recovery key">{{ recoveryKey }}</output>

      <div class="fe-e2e-rk__actions">
        <button type="button" class="fe-btn" @click="copy">
          {{ copied ? t('e2e.recovery.copied') : t('e2e.recovery.copy') }}
        </button>
        <button type="button" class="fe-btn" @click="download">
          {{ t('e2e.recovery.download') }}
        </button>
      </div>

      <div class="fe-e2e-warn" role="alert">
        <strong>{{ t('e2e.recovery.warn_title') }}</strong>
        <p>{{ t('e2e.recovery.warn_body') }}</p>
      </div>

      <div v-if="escrowKid" class="fe-e2e-rk__escrow" role="note">
        <strong>{{ t('e2e.recovery.escrow_title') }}</strong>
        <p>{{ t('e2e.recovery.escrow_body') }}</p>
        <p class="fe-e2e-rk__kid">{{ t('e2e.recovery.escrow_kid') }}: <code>{{ escrowKid }}</code></p>
      </div>

      <label class="fe-e2e-ack">
        <input v-model="ack" type="checkbox" />
        <span>{{ t('e2e.recovery.ack') }}</span>
      </label>
    </div>
    <template #actions>
      <button type="button" class="fe-btn fe-btn--primary" :disabled="!ack" @click="done">
        {{ t('e2e.recovery.done') }}
      </button>
    </template>
  </Modal>
</template>
