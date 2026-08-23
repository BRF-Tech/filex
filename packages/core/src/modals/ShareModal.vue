<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { ShareInfo } from '../types/FileNode';
import { useLocale } from '../composables/useLocale';
import { shareCliCommand } from '../lib/shareCli';
import { clampExpiryDate, expiryInputMax, ttlCeilingHint, validUntilLine } from '../lib/shareTtl';
import Modal from './Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  share?: (ShareInfo & { url: string; filename?: string }) | null;
  /** Server ceiling on a new link's life in days (0/undefined = none). */
  shareMaxTtlDays?: number;
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

// The picker is capped at the server's ceiling and the value is clamped once
// more on submit — the server clamps too, but asking for a date it will
// refuse is how the user ends up with a link that says one thing and does
// another.
const expiryMax = computed(() => expiryInputMax(props.shareMaxTtlDays));
const ttlHint = computed(() => ttlCeilingHint(props.shareMaxTtlDays, props.locale === 'tr' ? 'tr' : 'en'));
const validUntil = computed(() =>
  props.share ? validUntilLine(props.share.expires_at ?? null, props.locale === 'tr' ? 'tr' : 'en') : '',
);

function submit() {
  const chosen = expiresAt.value ? new Date(expiresAt.value) : null;
  emit('submit', {
    password: usePin.value,
    expires_at: clampExpiryDate(chosen, props.shareMaxTtlDays).iso,
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
          <input v-model="expiresAt" type="datetime-local" class="fe-input" :max="expiryMax" />
          <small v-if="ttlHint" class="fe-form__hint">{{ ttlHint }}</small>
        </label>
        <label class="fe-form__row fe-form__row--stack">
          <span>{{ t('modal.share.max_downloads') }}</span>
          <input v-model="maxDownloads" type="number" min="1" class="fe-input" />
        </label>
      </form>
    </template>
    <template v-else>
      <div class="fe-share-result">
        <div class="fe-share-result__row fe-share-result__row--note">
          <small>{{ validUntil }}<template v-if="share.expiry_clamped"> {{ t('modal.share.limit_applied') }}</template></small>
        </div>
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
        {{ share ? t('modal.share.close') : t('modal.share.cancel') }}
      </button>
      <button v-if="!share" type="button" class="fe-btn fe-btn--primary" @click="submit">
        {{ t('modal.share.create') }}
      </button>
    </template>
  </Modal>
</template>
