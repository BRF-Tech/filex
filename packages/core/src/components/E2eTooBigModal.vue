<script setup lang="ts">
/**
 * E2eTooBigModal — a decrypted download this browser cannot hold.
 *
 * Firefox and Safari have no File System Access API, so a file decrypted in
 * the tab is gathered in memory and handed to the normal download — up to
 * E2E_BLOB_SAVE_LIMIT (lib/e2esave.ts). Past that, the download is refused
 * BEFORE anything is decrypted (or, for a zip whose size is not known up
 * front, the moment it outgrows the limit), and this dialog says so and gives
 * the way out: the encrypted bytes, as the server has them, and the one
 * command that decrypts them on the person's own computer (`filex decrypt`,
 * docs/CLI.md). Chrome, Edge and the desktop app save as a stream and never
 * get here.
 */
import { computed, ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import type { FxeTooBig } from '../composables/useE2eFiles';
import Modal from '../modals/Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  info: FxeTooBig | null;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'download'): void;
}>();

const { t, formatSize } = useLocale(() => props.locale);
const copied = ref(false);

watch(
  () => props.open,
  (v) => {
    if (v) copied.value = false;
  },
);

const lead = computed(() => {
  const i = props.info;
  if (!i) return '';
  const limit = formatSize(i.limit);
  return i.size !== null
    ? t('e2e.big.lead', { name: i.name, size: formatSize(i.size), limit })
    : t('e2e.big.lead_grew', { name: i.name, limit });
});

async function copy() {
  const cmd = props.info?.command;
  if (!cmd) return;
  try {
    await navigator.clipboard.writeText(cmd);
    copied.value = true;
  } catch {
    /* no clipboard here: the command stays on screen to select */
  }
}
</script>

<template>
  <Modal :open="open" :title="t('e2e.big.title')" size="sm" @close="emit('close')">
    <div v-if="info" class="fe-e2e-form" data-testid="e2e-too-big">
      <p class="fe-e2e-rk__lead">{{ lead }}</p>
      <p>{{ t('e2e.big.instead') }}</p>
      <ol class="fe-e2e-big__steps">
        <li>{{ info.encrypted.kind === 'file' ? t('e2e.big.step_file') : t('e2e.big.step_folder') }}</li>
        <li>
          {{ t('e2e.big.step_run') }}
          <span class="fe-e2e-big__cmd">
            <code data-testid="e2e-too-big-command">{{ info.command }}</code>
            <button type="button" class="fe-e2e-optlink" data-testid="e2e-too-big-copy" @click="copy">
              {{ copied ? t('e2e.big.copied') : t('e2e.big.copy') }}
            </button>
          </span>
        </li>
      </ol>
      <p class="fe-e2e-names__hint">{{ t('e2e.big.other_ways') }}</p>
    </div>
    <template #actions>
      <button type="button" class="fe-btn" @click="emit('close')">
        {{ t('e2e.big.close') }}
      </button>
      <button type="button" class="fe-btn fe-btn--primary" data-testid="e2e-too-big-download" @click="emit('download')">
        {{ info?.encrypted.kind === 'folder' ? t('e2e.big.download_folder') : t('e2e.big.download_file') }}
      </button>
    </template>
  </Modal>
</template>

