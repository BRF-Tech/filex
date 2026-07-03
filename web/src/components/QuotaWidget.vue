<script setup lang="ts">
/**
 * QuotaWidget — small inline storage-usage widget that lives in TopNav.
 *
 *   [▰▰▰▱▱  4.2 / 10 GB]
 *
 * Click toggles a dropdown with raw numbers + the formatted percentage.
 * The dropdown is intentionally lightweight for v1 — a future iteration
 * can break it down by storage / by category (the backend snapshot is
 * already per-user so storage breakdown will need a new endpoint).
 *
 * Polls every 60s while mounted. Hidden entirely when the snapshot is
 * unloaded (cold-load → request still in-flight) so the bar doesn't
 * flash a wrong "0 / unlimited" while the auth round-trip resolves.
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Menu, MenuButton, MenuItems } from '@headlessui/vue';
import { Database, RefreshCcw } from 'lucide-vue-next';

import { useQuotaStore } from '@/stores/quota';
import { formatBytes } from '@/lib/format';

const POLL_MS = 60_000;

const { t, locale } = useI18n();
const quota = useQuotaStore();

let pollHandle: number | null = null;

onMounted(() => {
  void quota.fetch();
  pollHandle = window.setInterval(() => {
    void quota.fetch();
  }, POLL_MS);
});
onBeforeUnmount(() => {
  if (pollHandle != null) window.clearInterval(pollHandle);
});

// Color by saturation: green up to 75%, amber 75..90, rose 90+. Matches
// the existing UI palette (NotificationBell uses similar cutoffs).
const tone = computed(() => {
  if (quota.unlimited) return 'sky';
  if (quota.percent >= 90) return 'rose';
  if (quota.percent >= 75) return 'amber';
  return 'emerald';
});

const fillBgClass = computed(() => {
  switch (tone.value) {
    case 'rose':
      return 'bg-rose-500';
    case 'amber':
      return 'bg-amber-500';
    case 'sky':
      return 'bg-sky-500';
    default:
      return 'bg-emerald-500';
  }
});

const textTone = computed(() => {
  switch (tone.value) {
    case 'rose':
      return 'text-rose-700 dark:text-rose-300';
    case 'amber':
      return 'text-amber-700 dark:text-amber-300';
    case 'sky':
      return 'text-sky-700 dark:text-sky-300';
    default:
      return 'text-emerald-700 dark:text-emerald-300';
  }
});

const usedLabel = computed(() => formatBytes(quota.used, locale.value));
const limitLabel = computed(() => formatBytes(quota.limit, locale.value));
const percentLabel = computed(() => {
  if (quota.unlimited) return null;
  if (!Number.isFinite(quota.percent)) return null;
  return `${quota.percent.toFixed(quota.percent < 10 ? 1 : 0)}%`;
});

const tooltip = computed(() => {
  if (!quota.ready) return t('common.loading');
  if (quota.unlimited) return t('quota.unlimitedTooltip', { used: usedLabel.value });
  return t('quota.tooltip', {
    used: usedLabel.value,
    limit: limitLabel.value,
    percent: percentLabel.value ?? '—',
  });
});

async function refresh() {
  await quota.fetch();
}
</script>

<template>
  <Menu v-if="quota.ready" as="div" class="relative">
    <MenuButton
      class="hidden md:inline-flex items-center gap-2 rounded-md border border-zinc-200 dark:border-zinc-700 bg-zinc-50/60 dark:bg-zinc-800/60 px-2 py-1 text-xs font-medium text-zinc-700 dark:text-zinc-300 hover:bg-zinc-100 dark:hover:bg-zinc-800 transition-colors"
      :title="tooltip"
      :aria-label="tooltip"
    >
      <!-- Bar — fixed-width track + dynamic-width fill. Tailwind's
        w-[N%] arbitrary value would be purged at build time, so we
        feed `style.width` directly. -->
      <span
        v-if="!quota.unlimited"
        class="relative inline-block h-2 w-12 overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-700"
        aria-hidden="true"
      >
        <span
          class="absolute inset-y-0 left-0 transition-all duration-300"
          :class="fillBgClass"
          :style="{ width: `${quota.percent}%` }"
        />
      </span>
      <span v-else class="inline-flex h-2 w-2 rounded-full" :class="fillBgClass" aria-hidden="true" />

      <!-- Numbers — short form. `4.2 / 10 GB` if both have the same unit, else
        full pair. formatBytes is already locale-aware. -->
      <span :class="['tabular-nums', textTone]">
        <template v-if="quota.unlimited">
          {{ usedLabel }}
        </template>
        <template v-else>
          {{ usedLabel }} / {{ limitLabel }}
        </template>
      </span>
    </MenuButton>

    <transition
      enter-active-class="transition ease-out duration-100"
      enter-from-class="transform opacity-0 scale-95"
      enter-to-class="transform opacity-100 scale-100"
      leave-active-class="transition ease-in duration-75"
      leave-from-class="transform opacity-100 scale-100"
      leave-to-class="transform opacity-0 scale-95"
    >
      <MenuItems
        class="absolute right-0 z-50 mt-2 w-72 origin-top-right rounded-md border border-zinc-200 bg-white shadow-lg focus:outline-none dark:border-zinc-800 dark:bg-zinc-950"
      >
        <div class="border-b border-zinc-200 px-3 py-2 dark:border-zinc-800">
          <div class="flex items-center gap-2">
            <Database class="h-4 w-4 text-zinc-500 dark:text-zinc-400" />
            <span class="text-sm font-medium text-zinc-900 dark:text-zinc-100">
              {{ t('quota.title') }}
            </span>
            <button
              type="button"
              class="ml-auto rounded p-1 text-zinc-500 hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-100"
              :title="t('common.refresh')"
              :aria-label="t('common.refresh')"
              @click.stop="refresh"
            >
              <RefreshCcw class="h-3.5 w-3.5" :class="quota.loading ? 'animate-spin' : ''" />
            </button>
          </div>
        </div>

        <div class="px-3 py-3">
          <div v-if="quota.unlimited" class="flex items-center gap-2 text-sm">
            <span class="inline-flex h-2 w-2 rounded-full bg-sky-500" aria-hidden="true" />
            <span class="text-zinc-700 dark:text-zinc-200">
              {{ t('quota.unlimited') }}
            </span>
          </div>

          <template v-else>
            <!-- Big bar -->
            <div
              class="relative h-2 w-full overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-700"
              aria-hidden="true"
            >
              <span
                class="absolute inset-y-0 left-0 transition-all duration-300"
                :class="fillBgClass"
                :style="{ width: `${quota.percent}%` }"
              />
            </div>

            <dl class="mt-3 grid grid-cols-2 gap-y-2 text-xs">
              <dt class="text-zinc-500 dark:text-zinc-400">{{ t('quota.used') }}</dt>
              <dd class="text-right tabular-nums font-medium text-zinc-900 dark:text-zinc-100">
                {{ usedLabel }}
              </dd>
              <dt class="text-zinc-500 dark:text-zinc-400">{{ t('quota.limit') }}</dt>
              <dd class="text-right tabular-nums font-medium text-zinc-900 dark:text-zinc-100">
                {{ limitLabel }}
              </dd>
              <dt class="text-zinc-500 dark:text-zinc-400">{{ t('quota.percent') }}</dt>
              <dd :class="['text-right tabular-nums font-medium', textTone]">
                {{ percentLabel ?? '—' }}
              </dd>
            </dl>
          </template>
        </div>
      </MenuItems>
    </transition>
  </Menu>
</template>
