import { defineStore } from 'pinia';
import { ref } from 'vue';

export type ToastLevel = 'success' | 'info' | 'warn' | 'error';

export interface Toast {
  id: number;
  level: ToastLevel;
  title?: string;
  message: string;
  durationMs: number;
}

let nextId = 1;
const DEFAULT_MS: Record<ToastLevel, number> = {
  success: 3500,
  info: 4000,
  warn: 6000,
  error: 8000,
};

export const useToastStore = defineStore('toast', () => {
  const toasts = ref<Toast[]>([]);

  function push(level: ToastLevel, message: string, title?: string, durationMs?: number): number {
    const id = nextId++;
    const t: Toast = {
      id,
      level,
      message,
      title,
      durationMs: durationMs ?? DEFAULT_MS[level],
    };
    toasts.value = [...toasts.value, t];
    if (t.durationMs > 0) {
      window.setTimeout(() => dismiss(id), t.durationMs);
    }
    return id;
  }

  function dismiss(id: number): void {
    toasts.value = toasts.value.filter((t) => t.id !== id);
  }

  function clear(): void {
    toasts.value = [];
  }

  return {
    toasts,
    push,
    dismiss,
    clear,
    success: (m: string, title?: string) => push('success', m, title),
    info: (m: string, title?: string) => push('info', m, title),
    warn: (m: string, title?: string) => push('warn', m, title),
    error: (m: string, title?: string) => push('error', m, title),
  };
});
