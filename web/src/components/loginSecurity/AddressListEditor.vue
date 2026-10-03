<script setup lang="ts">
// A list of addresses and networks, edited as chips: type one (or several,
// separated by commas, spaces or new lines), press Enter or "Add", remove one
// with its ×. Used twice on the sign-in security page — the allowed addresses
// and the trusted proxies are the same kind of list — so the two cannot drift
// apart in how they take input.
//
// ⚠ It only edits the DRAFT; whether an entry is a valid address is the
// server's answer (it parses CIDR and, for proxies, the keywords), shown in
// `error`. Nothing is guessed here.
//
// ⚠ On a public demo the server sends "hidden on the demo" in place of each
// address (handlers/demo_redact.go): such a chip says it in the reader's
// language, is keyed by its place (several read alike) and has no ×, since it
// names nothing that could be removed.
//
// ⚠ The label is written above, and the hint or the error UNDER, the row that
// holds the box and the Add button — not by the box itself. When the box drew
// them, the button was aligned to the bottom of box + line, so a hint or an
// error pushed it below the box (0.50, measured in e2e/shots/loginsecurity.mjs;
// structure pinned in web/tests/components/addressListEditor.test.ts).
import { computed, ref, useId } from 'vue';
import { useI18n } from 'vue-i18n';
import { Plus, X } from 'lucide-vue-next';
import { splitList } from '@brftech/filex-core';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import { DEMO_MASKED_ADDRESS } from '@/lib/format';

const props = defineProps<{
  modelValue: string[];
  label: string;
  placeholder?: string;
  hint?: string;
  error?: string | null;
  /** The chip's remove button's accessible name, for one entry. */
  removeLabel: (entry: string) => string;
  /** What the list says while it holds nothing. */
  empty?: string;
  testId?: string;
}>();
const emit = defineEmits<{ (e: 'update:modelValue', v: string[]): void }>();

const { t } = useI18n();
const typed = ref('');
const inputId = useId();

const entries = computed(() => props.modelValue);
/** The line under the row that describes the box: the error, else the hint. */
const lineId = computed(() => (props.error ? `${inputId}-err` : props.hint ? `${inputId}-hint` : undefined));

function add() {
  const next = [...props.modelValue];
  for (const e of splitList(typed.value)) if (!next.includes(e)) next.push(e);
  typed.value = '';
  emit('update:modelValue', next);
}

function remove(entry: string) {
  emit(
    'update:modelValue',
    props.modelValue.filter((e) => e !== entry),
  );
}
</script>

<template>
  <div class="space-y-2" :data-testid="testId">
    <div class="space-y-1">
      <label :for="inputId" class="label-base">{{ label }}</label>
      <div class="flex items-stretch gap-2">
        <Input
          :model-value="typed"
          :name="inputId"
          :placeholder="placeholder"
          :describedby="lineId"
          :invalid="!!error"
          monospace
          class="flex-1 min-w-0"
          @update:model-value="(v) => (typed = String(v ?? ''))"
          @enter="add"
        />
        <Button
          type="button"
          variant="outline"
          class="shrink-0"
          :disabled="!typed.trim()"
          data-testid="address-add"
          @click="add"
        >
          <Plus class="h-4 w-4" />
          {{ t('common.add') }}
        </Button>
      </div>
      <p v-if="error" :id="`${inputId}-err`" class="error-text" data-testid="field-error">{{ error }}</p>
      <p v-else-if="hint" :id="`${inputId}-hint`" class="help-text">{{ hint }}</p>
    </div>
    <ul v-if="entries.length" class="flex flex-wrap gap-1.5" data-testid="address-chips">
      <li
        v-for="(entry, i) in entries"
        :key="`${i}:${entry}`"
        class="inline-flex items-center gap-1 rounded-md bg-zinc-100 dark:bg-zinc-800 ps-2 pe-1 py-0.5 text-xs font-mono"
      >
        <span v-if="entry === DEMO_MASKED_ADDRESS" class="pe-1 font-sans italic text-zinc-500">{{
          t('demo.hiddenAddress')
        }}</span>
        <span v-else dir="ltr">{{ entry }}</span>
        <button
          v-if="entry !== DEMO_MASKED_ADDRESS"
          type="button"
          class="rounded p-0.5 text-zinc-500 hover:text-rose-600 hover:bg-zinc-200 dark:hover:bg-zinc-700"
          :aria-label="removeLabel(entry)"
          @click="remove(entry)"
        >
          <X class="h-3 w-3" />
        </button>
      </li>
    </ul>
    <p v-else-if="empty" class="text-sm text-zinc-500 dark:text-zinc-400">{{ empty }}</p>
  </div>
</template>
