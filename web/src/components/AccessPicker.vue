<script setup lang="ts">
/**
 * #117 — "who, at what level": the person-or-group search and the level
 * strip of a storage's Access section, and of the dialog that turns per-item
 * access on (where it collects the people who keep access before anything
 * changes). It only PICKS: what a pick does - give access now, or hold it
 * until per-item access is on - is the caller's.
 *
 * The level is three buttons side by side, each saying what it means
 * (core ChoiceButtons `segmented`, never a native select). A viewer account
 * is offered at viewer: the account role is the ceiling and the server
 * refuses more (api/storageAccess levelFor).
 */
import { computed, onBeforeUnmount, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { ChoiceButtons, type ChoiceOption } from '@brftech/filex-core';

import { StorageAccessApi, accessKey, levelFor, type AccessCandidate, type AccessLevel } from '@/api/storageAccess';
import Badge from '@/components/ui/Badge.vue';
import Input from '@/components/ui/Input.vue';

const props = withDefaults(
  defineProps<{
    /** `data-testid` of the block; the search box, the strip and each
     *  suggestion are named under it. */
    testid: string;
    /** People and groups already there (`accessKey`), left out of the list. */
    exclude?: string[];
    disabled?: boolean;
  }>(),
  { exclude: () => [], disabled: false },
);
const emit = defineEmits<{ (e: 'pick', who: AccessCandidate, level: AccessLevel): void }>();

const { t } = useI18n();

const query = ref('');
const found = ref<AccessCandidate[]>([]);
const asked = ref('');
const level = ref<AccessLevel>('viewer');

const levelChoices = computed<ChoiceOption[]>(() =>
  (['viewer', 'editor', 'owner'] as const).map((v) => ({
    value: v,
    label: t(`groups.level.${v}`),
    help: t(`storages.access.levelHelp.${v}`),
  })),
);

const shown = computed(() => found.value.filter((c) => !props.exclude.includes(accessKey(c.kind, c.id))));

/* One lookup in flight per pause in the typing; an answer to an older
   question than the box now holds is dropped. */
let pause: ReturnType<typeof setTimeout> | undefined;
onBeforeUnmount(() => pause && clearTimeout(pause));

function lookUp(text: string | number | null) {
  const q = String(text ?? '').trim();
  if (pause) clearTimeout(pause);
  if (!q) {
    found.value = [];
    asked.value = '';
    return;
  }
  pause = setTimeout(() => void ask(q), 180);
}

async function ask(q: string) {
  let answer: AccessCandidate[] = [];
  try {
    answer = await StorageAccessApi.search(q);
  } catch {
    // Nothing to offer; typing again asks again.
  }
  if (query.value.trim() !== q) return;
  found.value = answer;
  asked.value = q;
}

function choose(c: AccessCandidate) {
  emit('pick', c, levelFor(c, level.value));
  query.value = '';
  found.value = [];
  asked.value = '';
}
</script>

<template>
  <div class="flex flex-wrap items-start gap-2" :data-testid="testid">
    <div class="relative min-w-0 flex-1 basis-56">
      <Input
        v-model="query"
        type="search"
        size="sm"
        :placeholder="t('storages.access.search')"
        :disabled="disabled"
        :data-testid="`${testid}-search`"
        @update:model-value="lookUp"
      />
      <ul v-if="shown.length" class="row-box absolute z-10 mt-1 w-full rounded-lg shadow-lg">
        <li v-for="c in shown" :key="accessKey(c.kind, c.id)">
          <button
            type="button"
            class="flex w-full items-center gap-2 px-3 py-2 text-start text-sm hover:bg-[var(--fe-bg-hover)]"
            :data-testid="`${testid}-option-${accessKey(c.kind, c.id)}`"
            @click="choose(c)"
          >
            <span class="truncate">{{ c.name }}</span>
            <Badge v-if="c.kind === 'group'" size="xs" tone="brand">{{ t('grants.group') }}</Badge>
            <span v-if="c.detail" class="truncate text-xs text-zinc-500"><bdi>{{ c.detail }}</bdi></span>
          </button>
        </li>
      </ul>
      <p v-else-if="asked && asked === query.trim()" class="mt-1 text-xs text-zinc-500" :data-testid="`${testid}-none`">
        {{ t('storages.access.noMatch') }}
      </p>
    </div>
    <ChoiceButtons
      :options="levelChoices"
      :model-value="level"
      segmented
      :disabled="disabled"
      :aria-label="t('grants.level')"
      :testid="`${testid}-level`"
      :testid-prefix="`${testid}-level`"
      @update:model-value="(v: string | string[]) => (level = v as AccessLevel)"
    />
  </div>
</template>
