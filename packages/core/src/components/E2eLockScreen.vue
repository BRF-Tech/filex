<script setup lang="ts">
/**
 * E2eLockScreen — an encrypted folder's lock screen (wiring:e2).
 *
 * One component for every pane that can stand inside an encrypted folder:
 * the main pane and the split pane draw the same screen, and the same unlock
 * opens the folder for both (the key ring is the explorer's, shared). The
 * password is checked against the folder's key file in the browser; it never
 * reaches the server. Collects the password; the unlocking is the parent's.
 */
import { ref } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';

const props = withDefaults(
  defineProps<{
    locale: LocaleCode;
    busy?: boolean;
    error?: string;
    /** The recovery-key / escrow door. Always offered where the dialog
     *  behind it can answer; whether the folder has one is said there. */
    showRecovery?: boolean;
    /** Smaller, for a narrow pane. */
    compact?: boolean;
  }>(),
  { busy: false, error: '', showRecovery: true, compact: false },
);
const emit = defineEmits<{
  (e: 'unlock', password: string): void;
  (e: 'recovery'): void;
}>();
const { t } = useLocale(() => props.locale);
const pw = ref('');

function submit() {
  if (!pw.value || props.busy) return;
  emit('unlock', pw.value);
}
defineExpose({ clear: () => (pw.value = '') });
</script>

<template>
  <div class="fe-state fe-e2e-lock" :class="{ 'fe-e2e-lock--compact': compact }" data-testid="e2e-lock">
    <svg
      class="fe-state__art"
      viewBox="0 0 120 100"
      :width="compact ? 80 : 110"
      :height="compact ? 67 : 92"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <rect x="38" y="44" width="44" height="34" rx="6" />
      <path d="M46 44v-8a14 14 0 0 1 28 0v8" />
      <circle cx="60" cy="59" r="3" fill="currentColor" stroke="none" />
      <path d="M60 62v7" />
    </svg>
    <p class="fe-state__title">{{ t('e2e.locked.title') }}</p>
    <p class="fe-state__hint">{{ t('e2e.locked.hint') }}</p>
    <form class="fe-e2e-lock__form" @submit.prevent="submit">
      <input
        v-model="pw"
        type="password"
        class="fe-input fe-e2e-lock__input"
        :placeholder="t('e2e.locked.pw_placeholder')"
        :aria-label="t('e2e.locked.pw_placeholder') /* the title and hint above
          say what this screen is; the field still needs its own name */"
        autocomplete="current-password"
        :disabled="busy"
      />
      <button type="submit" class="fe-btn fe-btn--primary" :disabled="busy || !pw">
        {{ busy ? t('e2e.locked.busy') : t('e2e.locked.unlock') }}
      </button>
    </form>
    <p v-if="error" class="fe-form__error" role="alert">{{ error }}</p>
    <!-- wiring:e2 recovery — the second door. Always offered: whether this
         folder actually has one is answered inside the dialog, which can say
         "this folder predates recovery keys" instead of leaving the user
         guessing why there is no link. -->
    <button v-if="showRecovery" type="button" class="fe-e2e-optlink" @click="emit('recovery')">
      {{ t('e2e.locked.use_recovery') }}
    </button>
  </div>
</template>
