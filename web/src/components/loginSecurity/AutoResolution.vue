<script setup lang="ts">
// What `auto` - the automatic trusted-proxy set - resolved to, and why: the
// networks it trusts besides this machine, the addresses it never trusts
// (gateways, filex's own), each network interface with what became of it, and
// the warning when filex could not tell and fell back to this machine alone.
//
// ⚠ Every wire value (the environment, the runtime, an interface's reason) is
// said in words; one this page does not know reads as the "unknown" or "other"
// sentence, never as the raw code (backend/internal/clientip/auto.go).
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { AlertTriangle } from 'lucide-vue-next';

import type { TrustedProxiesAuto } from '@/api/loginSecurity';
import { shownAddress } from '@/lib/format';
import Badge from '@/components/ui/Badge.vue';

const props = defineProps<{ auto: TrustedProxiesAuto }>();
const { t } = useI18n();
/** An address or a network as the page says it - the demo's mask in the reader's language. */
const addr = (v: string) => shownAddress(v, t('demo.hiddenAddress'));

const ENVIRONMENTS = ['plain', 'container', 'host-network', 'kubernetes', 'podman-rootless', 'unknown'];
const RUNTIMES = ['docker', 'podman', 'kubernetes', 'containerd', 'container'];
const REASONS = ['container-network', 'lan', 'tunnel', 'host', 'link-local-only', 'down', 'rootless', 'other'];
const WARNINGS = ['unreadable', 'ambiguous'];

const envText = computed(() => {
  const e = ENVIRONMENTS.includes(props.auto.environment) ? props.auto.environment : 'unknown';
  return t(`loginSecurity.proxies.auto.env.${e}`);
});
const runtimeText = computed(() => {
  const r = props.auto.runtime;
  if (!r) return '';
  return t(`loginSecurity.proxies.auto.runtime.${RUNTIMES.includes(r) ? r : 'container'}`);
});
const reasonText = (r: string) => t(`loginSecurity.proxies.auto.reason.${REASONS.includes(r) ? r : 'other'}`);
const warningText = computed(() => {
  const w = props.auto.warning;
  if (!w) return '';
  return t(`loginSecurity.proxies.auto.warning.${WARNINGS.includes(w) ? w : 'unreadable'}`);
});
const excluded = computed(() => [...(props.auto.excluded_gateways ?? []), ...(props.auto.excluded_self ?? [])]);
</script>

<template>
  <div
    class="space-y-2 rounded-lg border border-zinc-200 bg-zinc-50 px-3 py-2 text-sm dark:border-zinc-700 dark:bg-zinc-800/40"
    data-testid="login-proxies-auto"
  >
    <div class="flex flex-wrap items-center gap-2">
      <span class="font-medium">{{ t('loginSecurity.proxies.auto.title') }}</span>
      <Badge v-if="runtimeText" size="xs" tone="zinc" data-testid="login-proxies-auto-runtime">
        {{ t('loginSecurity.proxies.auto.runtimeLabel', { runtime: runtimeText }) }}
      </Badge>
    </div>
    <p v-if="!auto.in_use" class="text-zinc-600 dark:text-zinc-300" data-testid="login-proxies-auto-unused">
      {{ t('loginSecurity.proxies.auto.notInUse') }}
    </p>
    <p class="text-zinc-600 dark:text-zinc-300" data-testid="login-proxies-auto-why">{{ envText }}</p>
    <p
      v-if="warningText"
      class="flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 px-2 py-1.5 text-amber-800 dark:border-amber-700/60 dark:bg-amber-900/20 dark:text-amber-200"
      data-testid="login-proxies-auto-warning"
    >
      <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0" />
      <span>
        {{ warningText }}
        <span v-if="auto.warning_detail" class="mt-0.5 block break-all font-mono text-xs opacity-80">{{
          auto.warning_detail
        }}</span>
      </span>
    </p>
    <dl class="grid gap-x-3 gap-y-1 sm:grid-cols-[max-content_1fr]">
      <dt class="text-zinc-500 dark:text-zinc-400">{{ t('loginSecurity.proxies.auto.believes') }}</dt>
      <dd class="flex flex-wrap gap-1" data-testid="login-proxies-auto-believes">
        <Badge size="xs" tone="emerald">{{ t('loginSecurity.proxies.auto.thisMachine') }}</Badge>
        <Badge v-for="(n, k) in auto.networks ?? []" :key="k" size="xs" tone="emerald" class="font-mono">{{ addr(n) }}</Badge>
      </dd>
      <template v-if="excluded.length">
        <dt class="text-zinc-500 dark:text-zinc-400">{{ t('loginSecurity.proxies.auto.excluded') }}</dt>
        <dd data-testid="login-proxies-auto-excluded">
          <div class="flex flex-wrap gap-1">
            <Badge v-for="(a, k) in excluded" :key="k" size="xs" tone="rose" class="font-mono">{{ addr(a) }}</Badge>
          </div>
          <p class="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">{{ t('loginSecurity.proxies.auto.excludedHint') }}</p>
        </dd>
      </template>
      <template v-if="(auto.interfaces ?? []).length">
        <dt class="text-zinc-500 dark:text-zinc-400">{{ t('loginSecurity.proxies.auto.interfaces') }}</dt>
        <dd>
          <ul class="space-y-0.5" data-testid="login-proxies-auto-interfaces">
            <li v-for="i in auto.interfaces" :key="i.name" class="flex flex-wrap items-baseline gap-x-2">
              <span class="font-mono">{{ i.name }}</span>
              <span v-if="i.kind" class="text-xs text-zinc-500 dark:text-zinc-400">{{ i.kind }}</span>
              <span class="break-all font-mono text-xs">{{ i.addresses.map(addr).join(', ') }}</span>
              <span :class="i.trusted ? 'text-emerald-700 dark:text-emerald-300' : 'text-zinc-500 dark:text-zinc-400'">{{
                reasonText(i.reason)
              }}</span>
            </li>
          </ul>
        </dd>
      </template>
    </dl>
  </div>
</template>
