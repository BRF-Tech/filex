<script setup lang="ts">
import { ref } from 'vue';
import { Copy, Check } from 'lucide-vue-next';
import { useI18n } from 'vue-i18n';

interface Props {
  value: string;
  label?: string;
  size?: 'xs' | 'sm' | 'md';
}

const props = withDefaults(defineProps<Props>(), { size: 'sm' });
const { t } = useI18n();
const copied = ref(false);

async function copy() {
  try {
    await navigator.clipboard.writeText(props.value);
  } catch {
    // Fallback: textarea + execCommand. Best-effort only.
    const ta = document.createElement('textarea');
    ta.value = props.value;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    try {
      document.execCommand('copy');
    } catch {
      // ignore
    }
    document.body.removeChild(ta);
  }
  copied.value = true;
  setTimeout(() => (copied.value = false), 1500);
}

const sizing = {
  xs: 'text-xs px-1.5 py-0.5 gap-1',
  sm: 'text-xs px-2 py-1 gap-1.5',
  md: 'text-sm px-2.5 py-1.5 gap-2',
}[props.size];

const iconClass = props.size === 'md' ? 'h-4 w-4' : 'h-3.5 w-3.5';
</script>

<template>
  <button
    type="button"
    class="inline-flex items-center rounded-md border border-zinc-300 bg-white text-zinc-700 hover:bg-zinc-50 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-200 dark:hover:bg-zinc-800 transition-colors"
    :class="sizing"
    :aria-label="label ?? t('common.copy')"
    @click.stop="copy"
  >
    <Check v-if="copied" :class="['text-emerald-500', iconClass]" />
    <Copy v-else :class="iconClass" />
    <span v-if="$slots.default || label">
      <slot>{{ copied ? t('common.copied') : (label ?? t('common.copy')) }}</slot>
    </span>
  </button>
</template>
