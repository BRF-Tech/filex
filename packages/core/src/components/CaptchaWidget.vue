<script setup lang="ts">
/**
 * CaptchaWidget - the bot check a protected form shows: the provider's widget
 * (Turnstile or reCAPTCHA) or, in an outage, the local proof of work with its
 * progress bar.
 *
 * ⚠⚠ The ONE drawing of it (composables/useCaptcha has the behaviour). The
 * sign-in page, the demo's one-click sign-in, a link's PIN box and a file
 * request's drop box all mount this component; there is no second copy on a
 * surface of its own.
 *
 * ⚠ Accessible without the widget: the fallback's sentence is a live region
 * (`role="status"`) and its progress a `progressbar`, so a screen reader hears
 * "the verification service is unreachable, your browser is doing a short
 * computation" instead of a silent wait.
 *
 * `data-captcha-solve-ms` carries how long the local solution took - the
 * end-to-end tests read it (the shared spec's < 3 s on a desktop).
 */
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';
import type { CaptchaStore } from '../composables/useCaptcha';
import { useLocale } from '../composables/useLocale';

const props = defineProps<{
  captcha: CaptchaStore;
  locale: string;
}>();

const { t } = useLocale(() => props.locale);
const host = ref<HTMLElement | null>(null);

onMounted(() => {
  if (host.value) void props.captcha.attach(host.value);
});
onBeforeUnmount(() => props.captcha.detach());

// The form was switched on after the page opened (the config came late):
// the element is there, the widget is not yet.
watch(
  () => props.captcha.active.value,
  (on) => {
    if (on && host.value) void props.captcha.attach(host.value);
  },
);
</script>

<template>
  <div
    class="fe-captcha"
    data-testid="captcha"
    :data-captcha-form="captcha.form.value"
    :data-captcha-provider="captcha.provider.value"
    :data-captcha-state="captcha.state.value"
    :data-captcha-solve-ms="captcha.solveMs.value ?? undefined"
    role="group"
    :aria-label="t('captcha.label')"
  >
    <div
      v-show="!captcha.widgetHidden.value"
      ref="host"
      class="fe-captcha__widget"
      data-testid="captcha-widget"
    ></div>
    <div class="fe-captcha__status" :data-kind="captcha.messageKind.value || undefined" role="status" aria-live="polite">
      <span v-if="captcha.message.value" data-testid="captcha-message">{{ captcha.message.value }}</span>
      <!-- The product's one progress track (fe-sprogress, the share panel's
           and the trash bar's), not a native <progress>: each engine and a
           forced dark mode paint that one their own colour. -->
      <div
        v-if="captcha.state.value === 'solving'"
        class="fe-sprogress__track"
        role="progressbar"
        aria-valuemin="0"
        aria-valuemax="100"
        :aria-valuenow="captcha.progress.value"
        :aria-label="t('captcha.progress')"
        data-testid="captcha-progress"
      >
        <div class="fe-sprogress__bar" :style="{ width: `${captcha.progress.value}%` }"></div>
      </div>
    </div>
  </div>
</template>
