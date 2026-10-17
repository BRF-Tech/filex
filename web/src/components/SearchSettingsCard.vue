<script setup lang="ts">
/**
 * The search settings (task #261): content extraction, its size cap, the
 * memory budget and the automatic repair - settings-table rows since 0.57,
 * db -> env -> default. FILEX_SEARCH_* only gave them their first value.
 *
 * A component of its own, on the Search page for now: the settings hub
 * (task #258) places it, and it carries everything it needs - its own load,
 * its own saves, every refusal under the field it is about.
 *
 * ⚠ No range rule here (the 0.54 audit's B8, as on the Protection page): a
 * number goes to the server as typed and the server refuses one out of its
 * bounds with a sentence in the reader's language, which lands under the
 * field. The bounds reach the inputs from the GET, as hints.
 *
 * What applies when, said on the page because it differs: the switch and the
 * size cap apply to the next file written, the memory share at once, the
 * repair at the next start.
 */
import { computed, onMounted, ref, type Ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Save, SlidersHorizontal } from 'lucide-vue-next';

import { SearchApi, type SearchSettings, type SearchSettingsPatch } from '@/api/search';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { formatBytes } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Toggle from '@/components/ui/Toggle.vue';

const { t, locale } = useI18n();
const toast = useToastStore();

const settings = ref<SearchSettings | null>(null);
const loadError = ref<string | null>(null);

const contentMax = ref<number>(5);
const memoryPercent = ref<number>(25);
const errContentMax = ref<string | null>(null);
const errMemory = ref<string | null>(null);

const savingContent = ref(false);
const savingContentMax = ref(false);
const savingMemory = ref(false);
const savingAutoRebuild = ref(false);

function apply(next: SearchSettings) {
  settings.value = next;
  contentMax.value = next.content_max_mb;
  memoryPercent.value = next.memory_percent;
}

async function load() {
  loadError.value = null;
  try {
    apply(await SearchApi.settings());
  } catch (e: unknown) {
    loadError.value = extractError(e, t('errors.generic'));
  }
}

onMounted(load);

/**
 * One change saved: the patch sent, the answer read back into the card, the
 * toast said - or the server's sentence put under the field (a switch has no
 * field: its refusal is a toast).
 */
async function save(patch: SearchSettingsPatch, busy: Ref<boolean>, err: Ref<string | null> | null, ok: string) {
  if (err) err.value = null;
  busy.value = true;
  try {
    apply(await SearchApi.updateSettings(patch));
    toast.success(ok);
  } catch (e: unknown) {
    const said = extractError(e, t('errors.generic'));
    if (err) err.value = said;
    else toast.error(said);
  } finally {
    busy.value = false;
  }
}

const saveContent = (v: boolean) => save({ content: v }, savingContent, null, t('search.settings.savedOk'));
const saveContentMax = () =>
  save({ content_max_mb: contentMax.value }, savingContentMax, errContentMax, t('search.settings.savedOk'));
const saveMemory = () =>
  save({ memory_percent: memoryPercent.value }, savingMemory, errMemory, t('search.settings.savedOk'));
const saveAutoRebuild = (v: boolean) =>
  save({ auto_rebuild: v }, savingAutoRebuild, null, t('search.settings.savedNextStart'));

/** What the share came to in the running server, in words. */
const budgetLine = computed(() => {
  const s = settings.value;
  if (!s || !s.index_enabled) return '';
  if (!s.memory_given_bytes || !s.memory_from_kind) return t('search.settings.memory.unknown');
  return t('search.settings.memory.inForce', {
    budget: formatBytes(s.memory_budget_bytes, locale.value),
    given: formatBytes(s.memory_given_bytes, locale.value),
    from: t(`search.settings.memory.from.${s.memory_from_kind}`),
  });
});
</script>

<template>
  <section class="card card-body space-y-4" data-testid="search-settings">
    <div>
      <h2 class="flex items-center gap-2 text-base font-semibold">
        <SlidersHorizontal class="h-4 w-4" /> {{ t('search.settings.title') }}
      </h2>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('search.settings.desc') }}</p>
    </div>

    <p v-if="loadError" class="text-sm text-rose-600 dark:text-rose-400">{{ loadError }}</p>

    <template v-else-if="settings">
      <p
        v-if="!settings.index_enabled"
        class="text-sm text-amber-700 dark:text-amber-300"
        data-testid="search-settings-index-off"
      >
        {{ t('search.settings.indexOff') }}
      </p>

      <Toggle
        :model-value="settings.content"
        :disabled="savingContent"
        :label="t('search.settings.content.label')"
        :description="t('search.settings.content.desc')"
        name="search-content"
        data-testid="search-settings-content"
        @update:model-value="saveContent"
      />

      <form
        class="space-y-3 border-t border-zinc-200 pt-3 dark:border-zinc-800"
        novalidate
        @submit.prevent="saveContentMax"
      >
        <h3 class="text-sm font-semibold">{{ t('search.settings.contentMax.title') }}</h3>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('search.settings.contentMax.desc') }}</p>
        <Input
          :model-value="contentMax"
          type="number"
          :min="settings.content_max_mb_min"
          :max="settings.content_max_mb_max"
          :step="1"
          :label="t('search.settings.contentMax.label')"
          :hint="t('search.settings.contentMax.hint', { min: settings.content_max_mb_min, max: settings.content_max_mb_max })"
          :error="errContentMax"
          class="max-w-xs"
          data-testid="search-settings-content-max"
          @update:model-value="(v) => ((contentMax = v as number), (errContentMax = null))"
        />
        <div class="flex justify-end pt-1">
          <Button type="submit" :loading="savingContentMax" data-testid="search-settings-content-max-save">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </form>

      <form
        class="space-y-3 border-t border-zinc-200 pt-3 dark:border-zinc-800"
        novalidate
        @submit.prevent="saveMemory"
      >
        <h3 class="text-sm font-semibold">{{ t('search.settings.memory.title') }}</h3>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('search.settings.memory.desc') }}</p>
        <Input
          :model-value="memoryPercent"
          type="number"
          :min="settings.memory_percent_min"
          :max="settings.memory_percent_max"
          :step="1"
          :label="t('search.settings.memory.label')"
          :hint="t('search.settings.memory.hint', { min: settings.memory_percent_min, max: settings.memory_percent_max })"
          :error="errMemory"
          class="max-w-xs"
          data-testid="search-settings-memory"
          @update:model-value="(v) => ((memoryPercent = v as number), (errMemory = null))"
        />
        <p v-if="budgetLine" class="text-xs text-zinc-500 dark:text-zinc-400" data-testid="search-settings-budget">
          {{ budgetLine }}
        </p>
        <div class="flex justify-end pt-1">
          <Button type="submit" :loading="savingMemory" data-testid="search-settings-memory-save">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </form>

      <div class="border-t border-zinc-200 pt-3 dark:border-zinc-800">
        <Toggle
          :model-value="settings.auto_rebuild"
          :disabled="savingAutoRebuild"
          :label="t('search.settings.autoRebuild.label')"
          :description="t('search.settings.autoRebuild.desc')"
          name="search-auto-rebuild"
          data-testid="search-settings-auto-rebuild"
          @update:model-value="saveAutoRebuild"
        />
      </div>
    </template>
  </section>
</template>
