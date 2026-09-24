<script setup lang="ts">
/**
 * SurfaceSteps — a plugin surface's `steps` node: where the person is in a
 * wizard. Purely a picture; the plugin moves the active step by answering
 * with a new surface.
 */
import type { LocaleCode } from '../../../types/ExplorerConfig';
import type { StepsNodeProps } from '../../../types/Plugins';
import { useLocale } from '../../../composables/useLocale';
import { labelOf } from '../../../lib/pluginLabel';

const props = defineProps<{
  items: StepsNodeProps['items'];
  locale: LocaleCode;
}>();

const { t } = useLocale(() => props.locale);

function stateOf(s: unknown): 'done' | 'active' | 'todo' {
  return s === 'done' || s === 'active' ? s : 'todo';
}
</script>

<template>
  <ol class="fe-steps" :aria-label="t('plugin.steps.label')" data-testid="surface-steps">
    <li
      v-for="(it, i) in items ?? []"
      :key="it.id || i"
      class="fe-steps__item"
      :class="`is-${stateOf(it.state)}`"
      :aria-current="stateOf(it.state) === 'active' ? 'step' : undefined"
      :data-step="it.id"
    >
      <span class="fe-steps__marker" aria-hidden="true">
        <svg
          v-if="stateOf(it.state) === 'done'"
          class="fe-ficon"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2.2"
          stroke-linecap="round"
          stroke-linejoin="round"
          focusable="false"
        >
          <path d="M4.8 12.6l4.7 4.7L19.2 6.9" />
        </svg>
        <template v-else>{{ i + 1 }}</template>
      </span>
      <span class="fe-steps__label">{{ labelOf(it.label, locale) }}</span>
    </li>
  </ol>
</template>
