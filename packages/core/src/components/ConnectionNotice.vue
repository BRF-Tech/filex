<script setup lang="ts">
/**
 * ConnectionNotice — the one line a page shows while the server cannot be
 * reached, in place of a toast per failed request (lib/connection).
 *
 * ⚠⚠ A STRIP, not a toast, and that is the point rather than a preference. A
 * toast is an event ("this just happened") with a timer on it; being offline
 * is a STATE that lasts. Said as a toast it has to be re-armed to stay true,
 * which is how one dropped connection became a corner full of copies — and
 * when the connection came back the person was still left with a stack of
 * stale ones to dismiss. A strip is drawn while the state holds and is gone
 * the moment it does not, with nothing to dismiss.
 *
 * It appears at the TOP of the window, not in the toast corner: it must not
 * cover, or be covered by, the individual message a failed upload or rename
 * still raises down there.
 *
 * ⚠ No string of its own. It says `err.network` — the very sentence the toast
 * said — so a language pack that already translates the product translates
 * this too, and the release's packs stay at 100%.
 *
 * ⚠ The FOLDING is the fix and it lives in lib/connection, which every
 * request layer feeds whether or not this component is on screen. Drawing the
 * strip is a separate, opt-in decision: a host page that embeds an explorer
 * may not want a page-wide bar over its own chrome, and it keeps the quiet
 * corner either way.
 *
 * ⚠⚠ Its rules are in styles/base.css (`.fe-connlost`), NOT in a `<style>`
 * block here. A single-file component's styles reach only a bundle that
 * imports it, and the web-component distribution does not import this one —
 * so the two shipped stylesheets drifted the moment they lived here
 * (web/tests/deploy/packageLook.test.ts caught it, 2026-09-24).
 */
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { connectionDown } from '../lib/connection';

const props = defineProps<{
  /** The reader's language. Defaults to English, as every core surface does. */
  locale?: LocaleCode | string;
}>();

const { t } = useLocale(() => props.locale ?? 'en');
</script>

<template>
  <!-- `role="status"` + `aria-live="polite"`: announced once when it appears,
       and never re-announced, because the element is never replaced. -->
  <div
    v-if="connectionDown"
    class="fe-connlost"
    role="status"
    aria-live="polite"
    data-testid="connection-notice"
  >
    <span class="fe-connlost__dot" aria-hidden="true"></span>
    <span class="fe-connlost__text">{{ t('err.network') }}</span>
  </div>
</template>
