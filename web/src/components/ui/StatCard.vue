<script setup lang="ts">
import type { Component } from 'vue';
import { computed } from 'vue';

interface Props {
  label: string;
  value: string | number;
  delta?: string;
  deltaTone?: 'up' | 'down' | 'neutral';
  icon?: Component;
  iconTone?: 'brand' | 'emerald' | 'rose' | 'amber' | 'sky' | 'zinc';
  hint?: string;
  loading?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  iconTone: 'brand',
  deltaTone: 'neutral',
});

const iconBg = computed(() => {
  return {
    brand: 'bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400',
    emerald: 'bg-emerald-50 text-emerald-600 dark:bg-emerald-500/10 dark:text-emerald-400',
    rose: 'bg-rose-50 text-rose-600 dark:bg-rose-500/10 dark:text-rose-400',
    amber: 'bg-amber-50 text-amber-600 dark:bg-amber-500/10 dark:text-amber-400',
    sky: 'bg-sky-50 text-sky-600 dark:bg-sky-500/10 dark:text-sky-400',
    zinc: 'bg-zinc-100 text-zinc-700 dark:bg-zinc-800 dark:text-zinc-300',
  }[props.iconTone];
});

const deltaColor = computed(
  () =>
    ({
      up: 'text-emerald-600 dark:text-emerald-400',
      down: 'text-rose-600 dark:text-rose-400',
      neutral: 'text-zinc-500 dark:text-zinc-400',
    })[props.deltaTone],
);
</script>

<template>
  <div class="card card-body flex items-start gap-3 stat-card">
    <div
      v-if="icon"
      class="stat-card__icon flex h-10 w-10 shrink-0 items-center justify-center rounded-lg"
      :class="iconBg"
    >
      <component :is="icon" class="h-5 w-5" />
    </div>
    <div class="min-w-0 flex-1">
      <p class="text-xs font-medium uppercase tracking-wide text-zinc-500 dark:text-zinc-400">
        {{ label }}
      </p>
      <p
        class="stat-card__value mt-1 text-2xl font-semibold tabular-nums text-zinc-900 dark:text-zinc-100"
        :title="loading ? undefined : String(value)"
      >
        <template v-if="loading">…</template>
        <template v-else>{{ value }}</template>
      </p>
      <p v-if="delta || hint" class="mt-1 text-xs flex items-center gap-1.5">
        <span v-if="delta" :class="deltaColor">{{ delta }}</span>
        <span v-if="hint" class="text-zinc-500 dark:text-zinc-400">{{ hint }}</span>
      </p>
    </div>
  </div>
</template>

<style scoped>
/* ⚠ Six of these share a row on a laptop, and the value had what was left of
   ~110 px after a 40 px icon: "3,03 Ko" in French ran out of its card
   (translator report, 2026-09-22). The card measures ITSELF: when it is
   narrow the icon steps aside and the number gets the whole width, and a
   value that still does not fit is cut with its full text in the tooltip —
   never drawn over the next card. */
.stat-card {
  container-type: inline-size;
}
@container (max-width: 140px) {
  .stat-card__icon {
    display: none;
  }
}
.stat-card__value {
  /* The number shrinks with its card before anything is cut: 1.5rem where
     there is room, never below 1.125rem. */
  font-size: clamp(1.125rem, 15cqi, 1.5rem);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
