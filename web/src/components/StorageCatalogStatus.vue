<script setup lang="ts">
/**
 * A lazily cataloged storage's catalog, for the operator (the storage page):
 * how far it has come, what the background pass is doing, how much of the
 * watch budget is in use, and whether any folder's deletions were held back.
 * The server's `catalogue` block (sync.CatalogueCoverage, the lazy engine's
 * state) — see docs/LAZY-CATALOGUE.md → "Admin and observability".
 */
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { coveragePercent } from '@brftech/filex-core';

import type { CatalogCoverage } from '@/api/types';
import { formatNumber } from '@/lib/format';

const props = defineProps<{ catalogue: CatalogCoverage }>();

const { t, locale } = useI18n();

const done = computed(() => props.catalogue.catalogued_folders ?? 0);
const pending = computed(() => props.catalogue.pending_folders ?? 0);
const pct = computed(() => (props.catalogue.complete ? 100 : coveragePercent(props.catalogue)));
const behavior = computed(() =>
  props.catalogue.fill === 'on_open' ? t('storages.lazyFill.on_open') : t('storages.lazyFill.background'),
);
const filler = computed(() => {
  const s = props.catalogue.filler || 'off';
  return t('storages.catalog.fillerState.' + s);
});
const summary = computed(() => {
  if (props.catalogue.complete) return t('storages.catalog.complete');
  if (props.catalogue.fill === 'on_open') return t('storages.catalog.onOpenHint');
  return t('storages.catalog.filling', { pct: pct.value });
});
</script>

<template>
  <div class="space-y-3" data-testid="storage-catalog-status">
    <p class="text-sm text-zinc-600 dark:text-zinc-300" data-testid="storage-catalog-summary">
      {{ summary }}
    </p>
    <div
      v-if="catalogue.fill !== 'on_open'"
      class="h-1.5 w-full rounded-full bg-zinc-100 dark:bg-zinc-800 overflow-hidden"
      role="progressbar"
      :aria-valuenow="pct"
      aria-valuemin="0"
      aria-valuemax="100"
      :aria-label="t('storages.catalog.title')"
    >
      <div class="h-full bg-brand-500" :style="{ width: pct + '%' }"></div>
    </div>
    <div class="grid grid-cols-2 sm:grid-cols-3 gap-3 text-sm">
      <div>
        <p class="text-xs text-zinc-500">{{ t('storages.fields.lazyFill') }}</p>
        <p class="font-semibold">{{ behavior }}</p>
      </div>
      <div>
        <p class="text-xs text-zinc-500">{{ t('storages.catalog.filler') }}</p>
        <p class="font-semibold" data-testid="storage-catalog-filler">{{ filler }}</p>
      </div>
      <div>
        <p class="text-xs text-zinc-500">{{ t('storages.catalog.cataloged') }}</p>
        <p class="font-semibold" data-testid="storage-catalog-done">{{ formatNumber(done, locale) }}</p>
      </div>
      <div>
        <p class="text-xs text-zinc-500">{{ t('storages.catalog.pending') }}</p>
        <p class="font-semibold" data-testid="storage-catalog-pending">{{ formatNumber(pending, locale) }}</p>
      </div>
      <div>
        <p class="text-xs text-zinc-500">{{ t('storages.catalog.watched') }}</p>
        <p class="font-semibold" data-testid="storage-catalog-watched">
          {{
            t('storages.catalog.watchedOf', {
              n: formatNumber(catalogue.watched_folders ?? 0, locale),
              max: formatNumber(catalogue.max_watches ?? 0, locale),
            })
          }}
        </p>
      </div>
      <div>
        <p class="text-xs text-zinc-500">{{ t('storages.catalog.heldBack') }}</p>
        <p
          class="font-semibold"
          :class="(catalogue.held_back_folders ?? 0) > 0 ? 'text-amber-600 dark:text-amber-400' : ''"
          :title="(catalogue.held_back_folders ?? 0) > 0 ? t('storages.catalog.heldBackHint') : undefined"
        >
          {{ formatNumber(catalogue.held_back_folders ?? 0, locale) }}
        </p>
      </div>
    </div>
  </div>
</template>
