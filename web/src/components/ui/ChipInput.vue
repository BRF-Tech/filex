<script setup lang="ts">
/**
 * ChipInput — a short list of tokens (extensions, MIME types, tags) typed one
 * at a time. Enter, comma or space commits the token; Backspace on an empty
 * box removes the last one; each chip has its own ×.
 */
import { computed, ref, useId } from 'vue';
import { X } from 'lucide-vue-next';
import { isListSeparatorKey } from '@brftech/filex-core';

interface Props {
  modelValue: string[];
  label?: string;
  hint?: string;
  placeholder?: string;
  disabled?: boolean;
  /** Applied to every token before it is kept (lower-casing, trimming a dot…). */
  normalize?: (raw: string) => string;
  name?: string;
}

const props = defineProps<Props>();
const emit = defineEmits<{ (e: 'update:modelValue', v: string[]): void }>();

const fallback = useId();
const id = computed(() => props.name ?? fallback);
const draft = ref('');

function clean(raw: string): string {
  const s = raw.trim();
  return props.normalize ? props.normalize(s) : s;
}

function commit() {
  const token = clean(draft.value);
  draft.value = '';
  if (!token || props.modelValue.includes(token)) return;
  emit('update:modelValue', [...props.modelValue, token]);
}

function remove(idx: number) {
  emit('update:modelValue', props.modelValue.filter((_, i) => i !== idx));
}

function onKey(e: KeyboardEvent) {
  // ⚠ "،" / "，" end a chip too (core lib/listInput — an Arabic or CJK keyboard).
  if (e.key === 'Enter' || isListSeparatorKey(e.key) || e.key === ' ') {
    e.preventDefault();
    commit();
  } else if (e.key === 'Backspace' && draft.value === '' && props.modelValue.length) {
    remove(props.modelValue.length - 1);
  }
}
</script>

<template>
  <div class="space-y-1">
    <label v-if="label" :for="id" class="label-base">{{ label }}</label>
    <div
      class="flex min-h-[38px] flex-wrap items-center gap-1 rounded-lg border border-zinc-300 bg-white px-2 py-1 text-sm focus-within:ring-2 focus-within:ring-brand-500 dark:border-zinc-700 dark:bg-zinc-900"
      :class="disabled && 'opacity-60'"
    >
      <span
        v-for="(chip, i) in modelValue"
        :key="chip"
        class="inline-flex items-center gap-1 rounded bg-zinc-100 px-1.5 py-0.5 font-mono text-xs dark:bg-zinc-800"
        data-testid="chip"
      >
        {{ chip }}
        <button
          v-if="!disabled"
          type="button"
          class="rounded hover:text-rose-600"
          :aria-label="`${chip} ×`"
          @click="remove(i)"
        >
          <X class="h-3 w-3" />
        </button>
      </span>
      <input
        :id="id"
        v-model="draft"
        type="text"
        class="min-w-[6rem] flex-1 border-0 bg-transparent p-1 text-sm outline-none"
        :placeholder="placeholder"
        :disabled="disabled"
        :name="name"
        @keydown="onKey"
        @blur="commit"
      />
    </div>
    <p v-if="hint" class="text-xs text-zinc-500">{{ hint }}</p>
  </div>
</template>
