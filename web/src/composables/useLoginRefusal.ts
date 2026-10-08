import { computed, onBeforeUnmount, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { foreignText } from '@brftech/filex-core';

import { clock, type LoginRefusal } from '@/lib/loginRefusal';
import { ssoRefusalKeyOf } from '@/lib/ssoRefusal';

/**
 * The sentence for a refused sign-in, and the countdown while a lock holds.
 *
 * ⚠ The countdown is the CLIENT's clock counting down from `retry_after`, not
 * the server's worded-once `message` (which would say "1 minute" for a
 * minute). It ends by itself: at zero the sentence goes and the button comes
 * back, without another request.
 *
 * ⚠⚠ The WORDS are the server's (0.54 audit A8): the tries left are its
 * `message`, and a lock is its `countdown` - the same sentence with `{wait}`
 * left open, which this fills with the clock. This file used to keep its own
 * copy of both sentences.
 *
 * Nothing here decides anything — the server refuses or lets in; this only
 * shows why and for how long.
 */
export function useLoginRefusal() {
  const { t, locale } = useI18n();
  /** The server's sentence, isolated for the reader's direction. */
  const said = (text: string): string => foreignText(String(locale.value), text);
  const refusal = ref<LoginRefusal | null>(null);
  const until = ref(0);
  const now = ref(Date.now());
  let timer: ReturnType<typeof setInterval> | undefined;

  function stop() {
    if (timer) clearInterval(timer);
    timer = undefined;
  }

  const secondsLeft = computed(() => (until.value ? Math.max(0, Math.ceil((until.value - now.value) / 1000)) : 0));
  const locked = computed(() => !!refusal.value?.locked && secondsLeft.value > 0);
  const time = computed(() => clock(secondsLeft.value));

  /** Take in what the failed request said; null clears it. */
  function apply(r: LoginRefusal | null) {
    stop();
    refusal.value = r;
    until.value = 0;
    if (r?.locked && r.retryAfter && r.retryAfter > 0) {
      now.value = Date.now();
      until.value = now.value + r.retryAfter * 1000;
      timer = setInterval(() => {
        now.value = Date.now();
        if (now.value >= until.value) {
          stop();
          refusal.value = null;
        }
      }, 1000);
    }
  }

  /** What to say, or null when the server said nothing more than "no". */
  const text = computed<string | null>(() => {
    const r = refusal.value;
    if (!r) return null;
    if (r.locked) {
      if (secondsLeft.value <= 0) return null;
      if (r.countdown) return said(r.countdown.replaceAll('{wait}', time.value));
      return r.message ? said(r.message) : null;
    }
    const told = ssoRefusalKeyOf(r.reason);
    if (told) return t(told);
    if (typeof r.remaining === 'number' && r.message) return said(r.message);
    return null;
  });

  onBeforeUnmount(stop);
  return { apply, text, locked, time };
}
