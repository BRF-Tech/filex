<script setup lang="ts">
// Peers that sent X-Forwarded-For / X-Real-IP without being trusted. Behind an
// untrusted proxy every visitor is counted as the proxy, so the page names
// each such peer and offers to trust it; the question it asks before saving
// (the page's, see LoginSecurity.vue) says what trusting a stranger costs.
//
// ⚠ On a public demo the server sends the addresses masked: a value that is
// not an address gets no "Trust" button (there is nothing to add).
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { AlertTriangle } from 'lucide-vue-next';

import type { UntrustedForwarder } from '@/api/loginSecurity';
import { formatDate, shownAddress } from '@/lib/format';
import Badge from '@/components/ui/Badge.vue';
import Button from '@/components/ui/Button.vue';

const props = defineProps<{
  forwarders: UntrustedForwarder[];
  /** How many the server remembers (the list it sent is capped). */
  total: number;
  busy?: boolean;
}>();
const emit = defineEmits<{ (e: 'trust', f: UntrustedForwarder): void }>();

const { t, locale } = useI18n();
/** The address as the page says it - the demo's mask in the reader's language. */
const addr = (v: string) => shownAddress(v, t('demo.hiddenAddress'));
const FIRST = 3;
const open = ref(false);
const shown = computed(() => (open.value ? props.forwarders : props.forwarders.slice(0, FIRST)));
const notSent = computed(() => Math.max(0, props.total - props.forwarders.length));

/** An IPv4 or IPv6 address, as the server spells one - anything else is masked text. */
function isAddress(s: string): boolean {
  return /^[0-9.]+$/.test(s) ? s.split('.').length === 4 : /^[0-9a-f:.]+$/i.test(s) && s.includes(':');
}
</script>

<template>
  <div
    class="space-y-2 rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-700/60 dark:bg-amber-900/20 dark:text-amber-100"
    role="alert"
    data-testid="login-forwarders"
  >
    <p class="flex items-center gap-2 font-medium">
      <AlertTriangle class="h-4 w-4 shrink-0" />
      {{ t('loginSecurity.proxies.forwarders.title') }}
    </p>
    <p>{{ t('loginSecurity.proxies.forwarders.desc') }}</p>
    <ul class="space-y-1">
      <li
        v-for="(f, k) in shown"
        :key="`${k}-${f.first_seen}`"
        class="flex flex-wrap items-center gap-2"
        :data-testid="`login-forwarder-${f.address}`"
      >
        <span class="font-mono font-medium">{{ addr(f.address) }}</span>
        <Badge v-if="f.public" size="xs" tone="rose">{{ t('loginSecurity.proxies.forwarders.public') }}</Badge>
        <Badge v-if="f.relay" size="xs" tone="amber">{{ t('loginSecurity.proxies.forwarders.relay') }}</Badge>
        <span class="text-xs opacity-80">{{
          t('loginSecurity.proxies.forwarders.seen', { count: f.count, when: formatDate(f.last_seen, locale) }, f.count)
        }}</span>
        <Button
          v-if="isAddress(f.address)"
          type="button"
          size="sm"
          variant="outline"
          class="ms-auto"
          :disabled="busy"
          :data-testid="`login-forwarder-trust-${f.address}`"
          @click="emit('trust', f)"
        >
          {{ t('loginSecurity.proxies.forwarders.trust', { address: f.address }) }}
        </Button>
      </li>
    </ul>
    <div class="flex flex-wrap items-center gap-3 text-xs">
      <button
        v-if="forwarders.length > FIRST"
        type="button"
        class="underline"
        data-testid="login-forwarders-toggle"
        @click="open = !open"
      >
        {{ open ? t('loginSecurity.proxies.forwarders.showFewer') : t('loginSecurity.proxies.forwarders.showAll', { n: forwarders.length }) }}
      </button>
      <span v-if="notSent > 0" data-testid="login-forwarders-more">{{ t('loginSecurity.proxies.forwarders.more', { n: notSent }) }}</span>
    </div>
  </div>
</template>
