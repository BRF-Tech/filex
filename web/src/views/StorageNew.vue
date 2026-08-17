<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { Save, ArrowLeft, Activity } from 'lucide-vue-next';

import { useStoragesStore } from '@/stores/storages';
import { useToastStore } from '@/stores/toast';
import { useStorageDriversStore } from '@/stores/storageDrivers';
import { extractError } from '@/api/client';
import { StoragesApi } from '@/api/storages';
import type { StorageDriver } from '@/api/types';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Badge from '@/components/ui/Badge.vue';
import StorageDriverFields from '@/components/StorageDriverFields.vue';

const { t, te } = useI18n();
const router = useRouter();
const storages = useStoragesStore();
const toast = useToastStore();
const drivers = useStorageDriversStore();

const driver = ref<StorageDriver>('local');
const name = ref('');
const readOnly = ref(false);
const config = ref<Record<string, unknown>>({});
const saving = ref(false);

const testing = ref(false);
const testResult = ref<{ ok: boolean; error?: string } | null>(null);

// The picker lists whatever the backend registers — no hardcoded driver
// list. The literal that used to live here omitted `ftp`, which the
// backend has supported all along, so the driver was invisible.
const driverOptions = computed(() =>
  drivers.items.map((d) => ({
    value: d.driver,
    label: d.i18n_key && te(d.i18n_key) ? t(d.i18n_key) : d.label,
  })),
);

onMounted(async () => {
  await drivers.fetch();
  if (!drivers.descriptor(driver.value) && drivers.items.length > 0) {
    driver.value = drivers.items[0].driver;
  }
  config.value = drivers.defaults(driver.value);
});

function onDriverChange(d: StorageDriver) {
  driver.value = d;
  // Defaults come from the driver's descriptor. Switching drivers
  // replaces the config wholesale — stale keys are never carried over.
  config.value = drivers.defaults(d);
  testResult.value = null;
}

async function test() {
  testing.value = true;
  testResult.value = null;
  try {
    testResult.value = await StoragesApi.testConnection({
      name: name.value || 'test',
      driver: driver.value,
      config: config.value,
      read_only: readOnly.value,
    });
  } catch (e: unknown) {
    testResult.value = { ok: false, error: extractError(e, t('errors.generic')) };
  } finally {
    testing.value = false;
  }
}

async function submit() {
  saving.value = true;
  try {
    const created = await storages.create({
      name: name.value.trim(),
      driver: driver.value,
      config: config.value,
      read_only: readOnly.value,
    });
    toast.success(t('storages.createdOk'));
    router.push({ name: 'storages.edit', params: { id: created.id } });
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <form class="space-y-5 max-w-2xl" @submit.prevent="submit">
    <div class="flex items-center justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold">{{ t('storages.newTitle') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('storages.subtitle') }}</p>
      </div>
      <Button type="button" variant="ghost" size="sm" @click="router.push({ name: 'storages' })">
        <ArrowLeft class="h-4 w-4" />
        {{ t('common.back') }}
      </Button>
    </div>

    <div class="card card-body space-y-3">
      <Input
        v-model="name"
        :label="t('storages.fields.name')"
        :placeholder="t('storages.fields.namePlaceholder')"
        required
      />
      <Select
        :model-value="driver"
        :options="driverOptions"
        :label="t('storages.driverLabel')"
        @update:model-value="(v) => onDriverChange(v as StorageDriver)"
      />
      <Toggle v-model="readOnly" :label="t('storages.fields.readOnly')" />
    </div>

    <div class="card card-body">
      <StorageDriverFields v-model="config" :driver="driver" />
    </div>

    <div v-if="testResult" class="card card-body">
      <div v-if="testResult.ok" class="flex items-center gap-2 text-sm text-emerald-600">
        <Badge tone="emerald" dot>{{ t('storages.actions.ok') }}</Badge>
      </div>
      <div v-else class="space-y-2">
        <Badge tone="rose" dot>{{ t('storages.actions.fail') }}</Badge>
        <p class="text-xs text-rose-600 dark:text-rose-400 font-mono break-all">
          {{ testResult.error }}
        </p>
      </div>
    </div>

    <div class="flex items-center justify-between gap-2">
      <Button type="button" variant="outline" :loading="testing" @click="test">
        <Activity class="h-4 w-4" />
        {{ testing ? t('storages.actions.testing') : t('storages.actions.test') }}
      </Button>
      <div class="flex items-center gap-2">
        <Button type="button" variant="ghost" @click="router.push({ name: 'storages' })">
          {{ t('common.cancel') }}
        </Button>
        <Button type="submit" :loading="saving">
          <Save class="h-4 w-4" />
          {{ t('common.create') }}
        </Button>
      </div>
    </div>
  </form>
</template>
