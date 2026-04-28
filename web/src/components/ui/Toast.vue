<script setup lang="ts">
import { computed } from 'vue';
import { CheckCircle2, Info, AlertTriangle, XCircle, X } from 'lucide-vue-next';
import type { Toast } from '@/stores/toast';

interface Props {
  toast: Toast;
}

const props = defineProps<Props>();
const emit = defineEmits<{ (e: 'dismiss', id: number): void }>();

const styleMap = {
  success: {
    icon: CheckCircle2,
    bar: 'bg-emerald-500',
    iconColor: 'text-emerald-500',
  },
  info: {
    icon: Info,
    bar: 'bg-sky-500',
    iconColor: 'text-sky-500',
  },
  warn: {
    icon: AlertTriangle,
    bar: 'bg-amber-500',
    iconColor: 'text-amber-500',
  },
  error: {
    icon: XCircle,
    bar: 'bg-rose-500',
    iconColor: 'text-rose-500',
  },
} as const;

const cfg = computed(() => styleMap[props.toast.level]);
</script>

<template>
  <div
    role="alert"
    class="card flex w-80 max-w-full overflow-hidden shadow-lg animate-slide-up pointer-events-auto"
  >
    <span class="w-1 shrink-0" :class="cfg.bar" aria-hidden="true" />
    <div class="flex flex-1 items-start gap-3 p-3">
      <component :is="cfg.icon" :class="['h-5 w-5 mt-0.5 shrink-0', cfg.iconColor]" />
      <div class="min-w-0 flex-1 text-sm">
        <p v-if="toast.title" class="font-medium text-zinc-900 dark:text-zinc-100">
          {{ toast.title }}
        </p>
        <p class="text-zinc-700 dark:text-zinc-300 break-words">{{ toast.message }}</p>
      </div>
      <button
        type="button"
        class="rounded p-1 text-zinc-500 hover:bg-zinc-100 dark:hover:bg-zinc-800"
        aria-label="Dismiss"
        @click="emit('dismiss', toast.id)"
      >
        <X class="h-4 w-4" />
      </button>
    </div>
  </div>
</template>
