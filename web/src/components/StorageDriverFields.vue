<script setup lang="ts">
/**
 * Storage driver config form — rendered from the driver's own descriptor
 * (GET /admin/storage-drivers), never from a per-driver template.
 *
 * The v-if chain this replaced drifted away from the backend: it asked
 * for `base_path` where the driver reads `root`, never offered the s3
 * `prefix` at all, and knew nothing about ftp. Three of the four drivers
 * it offered could not be created — every submit came back 400
 * ROOT_PATH_FORBIDDEN. A driver now declares its keys once and every
 * surface (this form, the storage editor, the replication dialog) renders
 * the same thing.
 */
import { computed, onMounted, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';

import { useStorageDriversStore } from '@/stores/storageDrivers';
import type { StorageDriver, StorageField } from '@/api/types';
import Input from './ui/Input.vue';
import Textarea from './ui/Textarea.vue';
import Select from './ui/Select.vue';
import Toggle from './ui/Toggle.vue';

interface Props {
  /** The driver whose descriptor supplies the fields — unless `fields` is given. */
  driver?: StorageDriver;
  modelValue: Record<string, unknown>;
  /**
   * An explicit field list, for a form that is not a storage driver's: an
   * app plugin's settings (its manifest declares `Field[]` in the same shape,
   * backend/pkg/pluginkit/wire). With this set the driver store is not asked.
   */
  fields?: StorageField[];
}

const props = defineProps<Props>();
const emit = defineEmits<{
  (e: 'update:modelValue', v: Record<string, unknown>): void;
}>();

const { t, te } = useI18n();
const drivers = useStorageDriversStore();

onMounted(() => {
  if (!props.fields) drivers.fetch();
});

const showAdvanced = ref(false);

const fields = computed(() => props.fields ?? drivers.fields(props.driver));
const basicFields = computed(() => fields.value.filter((f) => !f.advanced));
const advancedFields = computed(() => fields.value.filter((f) => f.advanced));

/** Translation first, driver's English fallback second. A driver added
 *  after this release ships labels the catalogue has never heard of; they
 *  render in English instead of as a raw i18n key. */
function label(f: StorageField): string {
  return f.i18n_key && te(f.i18n_key) ? t(f.i18n_key) : f.label;
}

function help(f: StorageField): string | undefined {
  if (f.help_i18n_key && te(f.help_i18n_key)) return t(f.help_i18n_key);
  return f.help || undefined;
}

function optionLabel(o: { label: string; i18n_key?: string }): string {
  return o.i18n_key && te(o.i18n_key) ? t(o.i18n_key) : o.label;
}

function set(key: string, value: unknown) {
  emit('update:modelValue', { ...props.modelValue, [key]: value });
}

function value(f: StorageField): unknown {
  const v = props.modelValue?.[f.key];
  if (v !== undefined && v !== null) return v;
  // Legacy rows carry the old spelling of a key; show it in the field it
  // now belongs to instead of an empty box the operator would refill.
  for (const alias of f.aliases ?? []) {
    const a = props.modelValue?.[alias];
    if (a !== undefined && a !== null) return a;
  }
  return f.default ?? undefined;
}

function str(f: StorageField): string {
  const v = value(f);
  return v === undefined || v === null ? '' : String(v);
}

function num(f: StorageField): number | null {
  const v = value(f);
  if (v === '' || v === undefined || v === null) return null;
  const n = Number(v);
  return Number.isNaN(n) ? null : n;
}

function bool(f: StorageField): boolean {
  return value(f) === true;
}

/* A saved credential is never sent back: the server answers it as "***"
 * (storage.MaskSecrets). The field then stays empty and says it is kept - type
 * to change it. Left empty, or emptied again, it goes back as "***", which the
 * server reads as "keep the saved one". ⚠ Shown as a value, the mask would be
 * saved as the password the first time somebody typed one character into it. */
const SECRET_MASK = '***';
const kept = reactive(new Set<string>());

// Every key the server answered masked - a field its descriptor marks secret,
// or one it knows by name - whether or not the descriptors have loaded yet.
watch(
  () => props.modelValue,
  (mv) => {
    for (const [k, v] of Object.entries(mv ?? {})) {
      if (v === SECRET_MASK) kept.add(k);
    }
  },
  { immediate: true },
);

function isKept(f: StorageField): boolean {
  return value(f) === SECRET_MASK;
}

function text(f: StorageField): string {
  return isKept(f) ? '' : str(f);
}

function placeholderOf(f: StorageField): string | undefined {
  return isKept(f) ? t('storages.secretKept') : f.placeholder;
}

function setText(f: StorageField, v: unknown) {
  const s = v === undefined || v === null ? '' : String(v);
  if (s === '' && kept.has(f.key)) {
    set(f.key, SECRET_MASK);
    return;
  }
  set(f.key, s);
}
</script>

<template>
  <div class="space-y-3">
    <p v-if="!props.fields && drivers.loading && !fields.length" class="text-sm text-zinc-500">
      {{ t('common.loading') }}
    </p>
    <p v-else-if="!props.fields && !fields.length" class="text-sm text-amber-600 dark:text-amber-400">
      {{ t('storages.driverFieldsUnavailable', { driver }) }}
    </p>

    <template v-for="f in basicFields" :key="f.key">
      <Toggle
        v-if="f.type === 'bool'"
        :model-value="bool(f)"
        :label="label(f)"
        :description="help(f)"
        @update:model-value="(v) => set(f.key, v)"
      />
      <Select
        v-else-if="f.type === 'select'"
        :model-value="str(f)"
        :options="(f.options ?? []).map((o) => ({ value: o.value, label: optionLabel(o) }))"
        :label="label(f)"
        :hint="help(f)"
        :required="f.required"
        @update:model-value="(v) => set(f.key, v)"
      />
      <Textarea
        v-else-if="f.multiline"
        :model-value="text(f)"
        :label="label(f)"
        :hint="help(f)"
        :placeholder="placeholderOf(f)"
        :required="f.required && !isKept(f)"
        :monospace="f.monospace"
        :rows="4"
        @update:model-value="(v) => setText(f, v)"
      />
      <Input
        v-else-if="f.type === 'int'"
        :model-value="num(f)"
        :label="label(f)"
        :hint="help(f)"
        :placeholder="f.placeholder"
        :required="f.required"
        type="number"
        :min="f.min"
        :max="f.max"
        @update:model-value="(v) => set(f.key, v === '' || v === null ? null : Number(v))"
      />
      <Input
        v-else
        :model-value="text(f)"
        :label="label(f)"
        :hint="help(f)"
        :placeholder="placeholderOf(f)"
        :required="f.required && !isKept(f)"
        :monospace="f.monospace"
        :type="f.type === 'password' ? 'password' : 'text'"
        :autocomplete="f.secret ? 'new-password' : 'off'"
        :data-secret-kept="isKept(f) ? 'true' : undefined"
        @update:model-value="(v) => setText(f, v)"
      />
    </template>

    <div v-if="advancedFields.length" class="pt-1">
      <button
        type="button"
        class="text-xs font-medium text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100"
        @click="showAdvanced = !showAdvanced"
      >
        {{ showAdvanced ? '▾' : '▸' }} {{ t('storages.advancedFields') }}
      </button>
      <div v-if="showAdvanced" class="mt-3 space-y-3">
        <template v-for="f in advancedFields" :key="f.key">
          <Toggle
            v-if="f.type === 'bool'"
            :model-value="bool(f)"
            :label="label(f)"
            :description="help(f)"
            @update:model-value="(v) => set(f.key, v)"
          />
          <Input
            v-else-if="f.type === 'int'"
            :model-value="num(f)"
            :label="label(f)"
            :hint="help(f)"
            :placeholder="f.placeholder"
            type="number"
            :min="f.min"
            :max="f.max"
            @update:model-value="(v) => set(f.key, v === '' || v === null ? null : Number(v))"
          />
          <Input
            v-else
            :model-value="text(f)"
            :label="label(f)"
            :hint="help(f)"
            :placeholder="placeholderOf(f)"
            :monospace="f.monospace"
            :type="f.type === 'password' ? 'password' : 'text'"
            :autocomplete="f.secret ? 'new-password' : 'off'"
            :data-secret-kept="isKept(f) ? 'true' : undefined"
            @update:model-value="(v) => setText(f, v)"
          />
        </template>
      </div>
    </div>
  </div>
</template>
