<script setup lang="ts">
/**
 * SurfaceProgress — a plugin surface's `progress` node. `value` 0..100, or
 * null for "busy, no idea how long" (drawn as a moving bar).
 */
import { computed } from 'vue';
import type { LocaleCode } from '../../../types/ExplorerConfig';
import type { PluginText } from '../../../types/Plugins';
import { useLocale } from '../../../composables/useLocale';
import { appTextOr } from '../../../lib/pluginLabel';

const props = defineProps<{
  value: number | null | undefined;
  label?: PluginText | string;
  locale: LocaleCode;
}>();

const { t } = useLocale(() => props.locale);

const pct = computed<number | null>(() => {
  const v = props.value;
  if (v === null || v === undefined || !Number.isFinite(Number(v))) return null;
  return Math.min(100, Math.max(0, Math.round(Number(v))));
});

/* ⚠ See `appTextOr`: the app's line for this reader, then filex's own. */
const text = computed(() => appTextOr(props.label, props.locale, () => t('plugin.view.busy')));
</script>

<template>
  <div class="fe-sprogress" data-testid="surface-progress">
    <div class="fe-sprogress__head">
      <span class="fe-sprogress__label">{{ text }}</span>
      <span v-if="pct !== null" class="fe-sprogress__pct">{{ pct }}%</span>
    </div>
    <div
      class="fe-sprogress__track"
      :class="{ 'is-indeterminate': pct === null }"
      role="progressbar"
      :aria-label="text"
      aria-valuemin="0"
      aria-valuemax="100"
      :aria-valuenow="pct === null ? undefined : pct"
    >
      <div class="fe-sprogress__bar" :style="pct === null ? undefined : { width: `${pct}%` }"></div>
    </div>
  </div>
</template>
