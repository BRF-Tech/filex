<script setup lang="ts">
/**
 * A custom role's name and description in the other interface languages
 * (backend migration 00071): the folded part of the role editor.
 *
 * One row per language the panel offers — the built-ins and every language
 * pack's (`availableLocales`, THE list every picker reads) — plus any
 * language the role was already translated into that is no longer offered
 * (a pack removed since), so that entry stays visible and can be cleared. A
 * blank box means "show the name above": the role's own name is required and
 * is what every language without an entry reads. People see the role in the
 * language the interface is drawn in for them (lib/roleName; the server's
 * refusal sentences use the account's language the same way).
 */
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { availableLocales, localeLabel } from '@brftech/filex-core';

import Input from '@/components/ui/Input.vue';

type Texts = Record<string, string>;

const props = defineProps<{
  names?: Texts | null;
  descriptions?: Texts | null;
  /** The role's own name and description: the placeholder of a blank box. */
  name?: string;
  description?: string;
}>();
const emit = defineEmits<{
  (e: 'update:names', v: Texts): void;
  (e: 'update:descriptions', v: Texts): void;
}>();

const { t } = useI18n();

const languages = computed(() => {
  const out = availableLocales().map((o) => ({ code: o.code, label: o.label }));
  const seen = new Set(out.map((o) => o.code));
  for (const code of [...Object.keys(props.names ?? {}), ...Object.keys(props.descriptions ?? {})]) {
    if (seen.has(code)) continue;
    seen.add(code);
    out.push({ code, label: localeLabel(code) });
  }
  return out;
});

/** How many languages have something written, for the folded summary. */
const filled = computed(
  () =>
    languages.value.filter((l) => (props.names?.[l.code] ?? '').trim() || (props.descriptions?.[l.code] ?? '').trim())
      .length,
);

function put(which: 'names' | 'descriptions', code: string, v: string | number | null) {
  const next: Texts = { ...((which === 'names' ? props.names : props.descriptions) ?? {}) };
  const text = String(v ?? '');
  if (text.trim()) next[code] = text;
  else delete next[code];
  if (which === 'names') emit('update:names', next);
  else emit('update:descriptions', next);
}
</script>

<template>
  <details class="rounded-md border border-zinc-200 dark:border-zinc-800" data-testid="role-translations">
    <summary class="cursor-pointer select-none px-3 py-2 text-sm font-medium">
      {{ t('permissions.rules.translations') }}
      <span v-if="filled" class="ms-1 text-xs font-normal text-zinc-500" data-testid="role-translations-count">
        ({{ t('permissions.rules.translationsCount', { count: filled }, filled) }})
      </span>
    </summary>
    <div class="space-y-3 px-3 pb-3">
      <p class="text-xs text-zinc-500">{{ t('permissions.rules.translationsHint') }}</p>
      <div
        v-for="l in languages"
        :key="l.code"
        class="grid gap-2 sm:grid-cols-2"
        :data-testid="`role-translation-${l.code}`"
      >
        <Input
          :model-value="names?.[l.code] ?? ''"
          :label="t('permissions.rules.nameIn', { language: l.label })"
          :placeholder="name"
          :data-testid="`role-translation-name-${l.code}`"
          @update:model-value="(v) => put('names', l.code, v)"
        />
        <Input
          :model-value="descriptions?.[l.code] ?? ''"
          :label="t('permissions.rules.descriptionIn', { language: l.label })"
          :placeholder="description"
          :data-testid="`role-translation-description-${l.code}`"
          @update:model-value="(v) => put('descriptions', l.code, v)"
        />
      </div>
    </div>
  </details>
</template>
