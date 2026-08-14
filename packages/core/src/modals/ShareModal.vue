<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { ShareInfo } from '../types/FileNode';
import { useLocale } from '../composables/useLocale';
import { shareCliCommand } from '../lib/shareCli';
import Modal from './Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  share?: (ShareInfo & { url: string; filename?: string }) | null;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (
    e: 'submit',
    payload: { password: boolean; expires_at: string | null; max_downloads: number | null },
  ): void;
  (e: 'toast', msg: string): void;
}>();

const { t } = useLocale(() => props.locale);

const usePin = ref(false);
const expiresAt = ref<string>('');
const maxDownloads = ref<string>('');

watch(() => props.open, (v) => {
  if (v) {
    usePin.value = false;
    expiresAt.value = '';
    maxDownloads.value = '';
  }
});

function submit() {
  emit('submit', {
    password: usePin.value,
    expires_at: expiresAt.value ? new Date(expiresAt.value).toISOString() : null,
    max_downloads: maxDownloads.value ? Number(maxDownloads.value) : null,
  });
}

async function copy(value: string, toast: string) {
  try {
    await navigator.clipboard.writeText(value);
    emit('toast', toast);
  } catch {
    /* no-op */
  }
}

// One-line curl that downloads the shared file straight onto a server.
// Built by the shared helper so this dialog and the "Share / Permissions"
// panel cannot drift into two different commands.
const cliCommand = computed(() =>
  shareCliCommand(props.share ? { url: props.share.url, pin: props.share.password_pin, filename: props.share.filename } : null),
);
</script>

<template>
  <Modal :open="open" :title="t('modal.share.title')" size="md" @close="emit('close')">
    <template v-if="!share">
      <form class="fe-form" @submit.prevent="submit">
        <label class="fe-form__row">
          <input v-model="usePin" type="checkbox" />
          <span>{{ t('modal.share.pin') }}</span>
        </label>
        <label class="fe-form__row fe-form__row--stack">
          <span>{{ t('modal.share.expires') }}</span>
          <input v-model="expiresAt" type="datetime-local" class="fe-input" />
        </label>
        <label class="fe-form__row fe-form__row--stack">
          <span>{{ t('modal.share.max_downloads') }}</span>
          <input v-model="maxDownloads" type="number" min="1" class="fe-input" />
        </label>
      </form>
    </template>
    <template v-else>
      <div class="fe-share-result">
        <div class="fe-share-result__row">
          <label>Link</label>
          <div class="fe-share-result__copy">
            <input :value="share.url" readonly class="fe-input" />
            <button type="button" class="fe-btn" @click="copy(share.url, t('modal.share.url_copied'))">
              {{ t('modal.share.copy') }}
            </button>
          </div>
        </div>
        <div v-if="share.password_pin" class="fe-share-result__row">
          <label>PIN</label>
          <div class="fe-share-result__copy">
            <input :value="share.password_pin" readonly class="fe-input fe-input--mono" />
            <button type="button" class="fe-btn" @click="copy(share.password_pin, t('modal.share.pin_copied'))">
              {{ t('modal.share.copy') }}
            </button>
          </div>
        </div>
        <div class="fe-share-result__row">
          <label>CLI</label>
          <div class="fe-share-result__copy">
            <input :value="cliCommand" readonly class="fe-input fe-input--mono" />
            <button type="button" class="fe-btn" @click="copy(cliCommand, t('modal.share.cli_copied'))">
              {{ t('modal.share.copy') }}
            </button>
          </div>
        </div>
      </div>
    </template>
    <template #actions>
      <button type="button" class="fe-btn" @click="emit('close')">
        {{ share ? 'Kapat' : t('modal.share.cancel') }}
      </button>
      <button v-if="!share" type="button" class="fe-btn fe-btn--primary" @click="submit">
        {{ t('modal.share.create') }}
      </button>
    </template>
  </Modal>
</template>
