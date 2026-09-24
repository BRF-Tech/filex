<script setup lang="ts">
/**
 * PublicPinGate — the code a protected link asks for.
 *
 * ⚠⚠ ONE gate for all three public links (v3 §1). There used to be two: the
 * Go-rendered form on `/s/` and `/d/`, and this one on an app's page. They
 * disagreed about everything a person notices — the wording, the keyboard
 * the phone opened, what a wrong code said, whether the box was cleared and
 * refocused, and whether being locked out said so or just kept refusing.
 *
 * ⚠ The PIN is never stored, never logged and never put in a URL. What the
 * server returns is an HttpOnly cookie this code cannot read; the box is
 * cleared on a wrong answer so the next attempt starts from empty.
 */
import { nextTick, onMounted, ref, watch } from 'vue';
import type { LocaleCode } from '../../types/ExplorerConfig';
import { useLocale } from '../../composables/useLocale';

const props = defineProps<{
  locale: LocaleCode | string;
  busy?: boolean;
  /** `wrong` | `locked` | '' — from the composable, not from a status code here. */
  failure?: '' | 'wrong' | 'locked';
  /** The server's own words for a lock, when it sends any. */
  lockMessage?: string;
}>();

const emit = defineEmits<{
  (e: 'submit', pin: string): void;
}>();

const { t } = useLocale(() => props.locale);

const pin = ref('');
const input = ref<HTMLInputElement | null>(null);

const message = () => {
  if (props.failure === 'wrong') return t('plugin.page.pin_wrong');
  if (props.failure === 'locked') return t('plugin.page.pin_locked');
  return '';
};

// A wrong code empties the box and puts the caret back in it: a form that
// keeps the rejected digits makes the person select-all before retyping.
watch(
  () => props.failure,
  async (f) => {
    if (f === 'wrong') {
      pin.value = '';
      await nextTick();
      input.value?.focus();
    }
  },
);

onMounted(() => input.value?.focus());

function submit(): void {
  if (props.busy || props.failure === 'locked') return;
  // ⚠ An empty box is not an error to announce, it is a caret to move. The
  // button stays live — a greyed-out primary is the whole page's one act
  // looking broken before the person has done anything wrong.
  if (!pin.value.trim()) {
    input.value?.focus();
    return;
  }
  emit('submit', pin.value);
}
</script>

<template>
  <form class="fe-ppage__pin" data-testid="public-page-pin" @submit.prevent="submit">
    <div class="fe-ppage__state">
      <h2 class="fe-ppage__state-title">{{ t('plugin.page.pin_title') }}</h2>
      <p class="fe-surface__text fe-surface__text--muted">{{ t('plugin.page.pin_hint') }}</p>
    </div>
    <!-- ⚠ The label is for a screen reader only. The heading right above it
         already says the box is for a code, and a visible "PIN" caption over
         a PIN box is furniture — the old gate had none. -->
    <label class="fe-ppage__srlabel" for="fe-ppage-pin">{{ t('plugin.pin.label') }}</label>
    <input
      id="fe-ppage-pin"
      ref="input"
      v-model="pin"
      class="fe-cfield__input fe-ppage__pinput"
      :class="{ 'is-invalid': !!message() }"
      type="text"
      inputmode="numeric"
      autocomplete="one-time-code"
      spellcheck="false"
      maxlength="12"
      :disabled="busy || failure === 'locked'"
      :aria-invalid="message() ? 'true' : undefined"
      data-testid="public-page-pin-input"
    />
    <p v-if="message()" class="fe-ppage__error" role="alert" data-testid="public-page-pin-error">
      {{ message() }}<template v-if="lockMessage"> ({{ lockMessage }})</template>
    </p>
    <button
      type="submit"
      class="fe-btn fe-btn--primary"
      :disabled="busy || failure === 'locked'"
      data-testid="public-page-pin-submit"
    >
      {{ t('plugin.page.pin_submit') }}
    </button>
  </form>
</template>
