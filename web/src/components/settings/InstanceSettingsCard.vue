<script setup lang="ts">
/**
 * One card per page for the settings that lived in the environment alone
 * until 0.57 (task #258, backend internal/instancesettings): the default
 * language, the sync interval, the apps' limits, the notifications switch and
 * the rest, each on the page it belongs to (`group`). ONE component and ONE
 * endpoint for every page that draws them, so two pages cannot say different
 * things about the same row.
 *
 * Every row says what it is, the bounds it accepts, the variable that gave it
 * its first value, and - when the server reads it at start - that a change
 * waits for the next one (and that one is waiting). A switch saves on the
 * click; numbers and languages save with the card's button, the server
 * checking each against its bounds and refusing under the field it is about
 * (one refusal, nothing written).
 */
import { computed, onMounted, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Save, SlidersHorizontal } from 'lucide-vue-next';
import { availableLocales } from '@brftech/filex-core';

import {
  InstanceSettingsApi,
  instanceSettingSlug,
  type InstanceSettingGroup,
  type InstanceSettingItem,
} from '@/api/instanceSettings';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';

import Badge from '@/components/ui/Badge.vue';
import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Toggle from '@/components/ui/Toggle.vue';

const props = defineProps<{ group: InstanceSettingGroup }>();

const { t } = useI18n();
const toast = useToastStore();

const items = ref<InstanceSettingItem[]>([]);
const loadError = ref('');
const loaded = ref(false);
const saving = ref(false);
/** What the person typed, per key (numbers and languages). */
const draft = reactive<Record<string, number | string>>({});
/** The server's refusal, under the field it is about. */
const fieldError = reactive<Record<string, string>>({});

const label = (it: InstanceSettingItem, part: 'label' | 'hint') =>
  t(`instanceSettings.items.${instanceSettingSlug(it.key)}.${part}`);

function apply(next: InstanceSettingItem[]) {
  items.value = next;
  for (const it of next) {
    if (it.kind !== 'bool') draft[it.key] = it.value as number | string;
    delete fieldError[it.key];
  }
}

async function load() {
  loadError.value = '';
  try {
    apply(await InstanceSettingsApi.list(props.group));
  } catch (e: unknown) {
    loadError.value = extractError(e, t('errors.loadFailed'));
  } finally {
    loaded.value = true;
  }
}
onMounted(load);

/* The offered languages - the built-in two and a language pack's - and the
   setting's own "nothing chosen". */
const languageOptions = (it: InstanceSettingItem) => [
  { value: '', label: t(`instanceSettings.items.${instanceSettingSlug(it.key)}.none`) },
  ...availableLocales().map((o) => ({ value: o.code, label: o.label || o.code })),
];

/** A field typed in: its value kept, its refusal (if any) cleared. */
function edit(key: string, v: number | string) {
  draft[key] = v;
  delete fieldError[key];
}

const changed = computed(() =>
  items.value.filter((it) => it.kind !== 'bool' && draft[it.key] !== undefined && draft[it.key] !== it.value),
);

/** The refused field and the server's sentence, or null for another failure. */
function refusal(e: unknown): { field: string; message: string } | null {
  const data = (e as { response?: { data?: { field?: unknown; message?: unknown } } })?.response?.data;
  return data && typeof data.field === 'string' && typeof data.message === 'string'
    ? { field: data.field, message: data.message }
    : null;
}

async function send(patch: Record<string, number | boolean | string>) {
  saving.value = true;
  try {
    apply(await InstanceSettingsApi.save(props.group, patch));
    toast.success(t('instanceSettings.savedOk'));
  } catch (e: unknown) {
    const r = refusal(e);
    if (r) fieldError[r.field] = r.message;
    else toast.error(extractError(e, t('errors.generic')));
  } finally {
    saving.value = false;
  }
}

function saveChanged() {
  const patch: Record<string, number | string> = {};
  for (const it of changed.value) {
    const v = draft[it.key];
    patch[it.key] = it.kind === 'int' ? Number(v) : String(v);
  }
  if (Object.keys(patch).length) void send(patch);
}

const unitLabel = (it: InstanceSettingItem) => (it.unit ? t(`instanceSettings.unit.${it.unit}`) : '');
const anyRestart = computed(() => items.value.some((it) => it.restart));
</script>

<template>
  <section :id="`instance-settings-${group}`" class="card card-body space-y-4" :data-testid="`instance-settings-${group}`">
    <div>
      <h2 class="flex items-center gap-2 text-base font-semibold">
        <SlidersHorizontal class="h-4 w-4" /> {{ t(`instanceSettings.groups.${group}.title`) }}
      </h2>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t(`instanceSettings.groups.${group}.desc`) }}</p>
      <p v-if="anyRestart" class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('instanceSettings.restartNote') }}</p>
    </div>

    <p v-if="loadError" class="text-sm text-rose-600 dark:text-rose-400">{{ loadError }}</p>

    <form v-else-if="loaded" class="space-y-4" novalidate @submit.prevent="saveChanged">
      <div
        v-for="it in items"
        :key="it.key"
        class="space-y-1 border-t border-zinc-200 pt-3 first:border-t-0 first:pt-0 dark:border-zinc-800"
        :data-testid="`instance-setting-${it.key}`"
      >
        <Toggle
          v-if="it.kind === 'bool'"
          :model-value="it.value as boolean"
          :disabled="saving"
          :label="label(it, 'label')"
          :description="label(it, 'hint')"
          :name="`instance-${it.key}`"
          @update:model-value="(v: boolean) => send({ [it.key]: v })"
        />
        <Input
          v-else-if="it.kind === 'int'"
          :model-value="draft[it.key] as number"
          type="number"
          :min="it.min"
          :max="it.max"
          :step="1"
          :label="`${label(it, 'label')}${unitLabel(it) ? ` (${unitLabel(it)})` : ''}`"
          :hint="`${label(it, 'hint')} ${t('instanceSettings.bounds', { min: it.min ?? '', max: it.max ?? '', default: String(it.default) })}`"
          :error="fieldError[it.key] || null"
          class="max-w-md"
          @update:model-value="(v) => edit(it.key, v as number)"
        />
        <template v-else>
          <Select
            :model-value="draft[it.key] as string"
            :options="languageOptions(it)"
            :label="label(it, 'label')"
            class="max-w-md"
            @update:model-value="(v) => edit(it.key, v as string)"
          />
          <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ label(it, 'hint') }}</p>
          <p v-if="fieldError[it.key]" class="text-xs text-rose-600 dark:text-rose-400">{{ fieldError[it.key] }}</p>
        </template>
        <p class="flex flex-wrap items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400">
          <span v-if="it.env">{{ t('instanceSettings.fromEnv', { env: it.env }) }}</span>
          <Badge v-if="it.restart_pending" tone="amber" :data-testid="`instance-setting-pending-${it.key}`">
            {{ t('instanceSettings.restartPending') }}
          </Badge>
        </p>
      </div>
      <div v-if="items.some((it) => it.kind !== 'bool')" class="flex justify-end pt-1">
        <Button type="submit" :loading="saving" :disabled="!changed.length" :data-testid="`instance-settings-${group}-save`">
          <Save class="h-4 w-4" />
          {{ t('common.save') }}
        </Button>
      </div>
    </form>
  </section>
</template>
