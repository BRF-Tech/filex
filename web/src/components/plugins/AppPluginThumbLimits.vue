<script setup lang="ts">
/**
 * An app's Thumbnails section (filex 0.50): the kinds it draws thumbnails of,
 * and the four limits the administrator sets for it - the largest file it is
 * sent, the time it has per file, its memory, how many files it draws at
 * once (docs/thumbnails.md → Thumbnails drawn by apps).
 *
 * An empty box (or 0) is the default, which the box's placeholder says; a
 * value outside its range is refused by the server with the field named, and
 * said under that box. Which kinds it is asked for first is decided in
 * Plugins → Default apps, and the section says so.
 */
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { Save } from 'lucide-vue-next';

import { AppPluginsApi, type AppThumbLimits, type AppThumbLimitsAnswer } from '@/api/appPlugins';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';

const props = defineProps<{ pluginId: number }>();

const { t } = useI18n();
const toast = useToastStore();

type Field = keyof AppThumbLimits;
const FIELDS: Field[] = ['max_input_mb', 'timeout_s', 'memory_mb', 'concurrency'];

const ans = ref<AppThumbLimitsAnswer | null>(null);
const values = ref<Record<Field, string | number | null>>({ max_input_mb: '', timeout_s: '', memory_mb: '', concurrency: '' });
const errors = ref<Partial<Record<Field, string>>>({});
const saving = ref(false);
const loadError = ref('');

function seed(a: AppThumbLimitsAnswer) {
  ans.value = a;
  for (const f of FIELDS) values.value[f] = a.stored[f] ? String(a.stored[f]) : '';
  errors.value = {};
}

async function load() {
  loadError.value = '';
  try {
    seed(await AppPluginsApi.thumbLimits(props.pluginId));
  } catch (e: unknown) {
    loadError.value = extractError(e, t('errors.loadFailed'));
  }
}

watch(() => props.pluginId, () => void load(), { immediate: true });

const kinds = computed(() => {
  const a = ans.value;
  if (!a) return '';
  return [...a.ext.map((e) => `.${e}`), ...a.mime].join(', ');
});

function hint(f: Field): string {
  const a = ans.value;
  if (!a) return '';
  return t('appPlugins.detail.thumbs.hint', { def: a.defaults[f], min: a.min[f], max: a.max[f] });
}

/** A box's number: empty is 0, the default. NaN for what is not a whole number. */
function numberOf(v: string | number | null): number {
  const s = String(v ?? '').trim();
  if (!s) return 0;
  return /^\d+$/.test(s) ? Number(s) : NaN;
}

async function save() {
  const a = ans.value;
  if (!a) return;
  errors.value = {};
  const next = {} as AppThumbLimits;
  for (const f of FIELDS) {
    const n = numberOf(values.value[f]);
    if (Number.isNaN(n) || (n !== 0 && (n < a.min[f] || n > a.max[f]))) {
      errors.value[f] = t('appPlugins.detail.thumbs.outOfRange', { min: a.min[f], max: a.max[f] });
    }
    next[f] = Number.isNaN(n) ? 0 : n;
  }
  if (Object.keys(errors.value).length) return;
  saving.value = true;
  try {
    seed(await AppPluginsApi.putThumbLimits(props.pluginId, next));
    toast.success(t('appPlugins.detail.thumbs.saved'));
  } catch (e: unknown) {
    const data = (e as { response?: { data?: { error?: string; field?: string } } })?.response?.data;
    if (data?.error === 'out_of_range' && data.field && FIELDS.includes(data.field as Field)) {
      const f = data.field as Field;
      errors.value[f] = t('appPlugins.detail.thumbs.outOfRange', { min: a.min[f], max: a.max[f] });
    } else {
      toast.error(extractError(e, t('errors.actionFailed')));
    }
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <section class="space-y-3" data-testid="app-thumb-limits">
    <h3 class="text-sm font-semibold">{{ t('appPlugins.detail.thumbs.title') }}</h3>
    <p v-if="loadError" class="text-sm text-rose-700 dark:text-rose-300" role="alert">{{ loadError }}</p>
    <template v-else-if="ans">
      <p class="text-sm text-zinc-600 dark:text-zinc-400" data-testid="app-thumb-kinds">
        {{ t('appPlugins.detail.thumbs.desc', { kinds }) }}
      </p>
      <form class="grid gap-3 sm:grid-cols-2" @submit.prevent="save">
        <div v-for="f in FIELDS" :key="f" :data-testid="`app-thumb-${f}`">
          <Input
            v-model="values[f]"
            type="number"
            inputmode="numeric"
            :min="0"
            :max="ans.max[f]"
            :name="`app-thumb-${f}`"
            :label="t(`appPlugins.detail.thumbs.field.${f}`)"
            :placeholder="String(ans.defaults[f])"
            :hint="hint(f)"
            :error="errors[f] ?? null"
          />
        </div>
        <div class="flex items-end justify-between gap-3 sm:col-span-2">
          <p class="text-xs text-zinc-500">{{ t('appPlugins.detail.thumbs.order') }}</p>
          <Button type="submit" size="sm" variant="primary" :loading="saving" data-testid="app-thumb-save">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </form>
    </template>
  </section>
</template>
