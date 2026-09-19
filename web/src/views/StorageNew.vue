<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { Save, ArrowLeft, Activity, FolderTree, Layers } from 'lucide-vue-next';

import { useStoragesStore } from '@/stores/storages';
import { useToastStore } from '@/stores/toast';
import { useStorageDriversStore } from '@/stores/storageDrivers';
import { extractError } from '@/api/client';
import { StoragesApi } from '@/api/storages';
import type { DiscoveredFolder, StorageDriver } from '@/api/types';
import { secondsFromMinutes } from '@/lib/syncInterval';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Checkbox from '@/components/ui/Checkbox.vue';
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
/** Poll cadence in minutes; '' = the server default. Seconds on the wire. */
const syncIntervalMin = ref<number | ''>('');
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
  discovered.value = null;
  discoverError.value = '';
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
      sync_interval_s: secondsFromMinutes(syncIntervalMin.value),
    });
    toast.success(t('storages.createdOk'));
    router.push({ name: 'storages.edit', params: { id: created.id } });
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    saving.value = false;
  }
}

/* ---------------------------------------------------------------------------
 * Mount several folders at once (issue #31).
 *
 * The root of a bucket or share is never mounted as one storage — filex would
 * take ownership of everything at the root of a shared namespace
 * (ROOT_PATH_FORBIDDEN). The operator who wanted "the whole bucket in the
 * sidebar" therefore filled this form once per top-level folder. This lists
 * the folders under the root typed above (the probe may look where a mount may
 * not) and creates one storage per ticked folder through the ordinary create,
 * with the same credentials and the folder as that storage's root.
 *
 * Only for drivers that declare a root field: a driver without one mounts no
 * shared namespace and has nothing to look under.
 * ------------------------------------------------------------------------- */
interface Candidate extends DiscoveredFolder {
  selected: boolean;
  storageName: string;
}

const rootKey = computed(() => drivers.fields(driver.value).find((f) => f.root)?.key ?? '');
const discovering = ref(false);
const discoverError = ref('');
const discovered = ref<Candidate[] | null>(null);
const bulkSaving = ref(false);

const selectedCount = computed(() => (discovered.value ?? []).filter((c) => c.selected).length);

async function listFolders() {
  discovering.value = true;
  discoverError.value = '';
  discovered.value = null;
  try {
    const res = await StoragesApi.discover({ driver: driver.value, config: config.value });
    if (!res.ok) {
      discoverError.value = res.error || t('errors.generic');
      return;
    }
    discovered.value = (res.folders ?? []).map((f) => ({
      ...f,
      selected: true,
      storageName: f.name,
    }));
  } catch (e: unknown) {
    discoverError.value = extractError(e, t('errors.generic'));
  } finally {
    discovering.value = false;
  }
}

function selectAll(on: boolean) {
  for (const c of discovered.value ?? []) c.selected = on;
}

async function createDiscovered() {
  const picked = (discovered.value ?? []).filter((c) => c.selected && c.storageName.trim());
  if (!picked.length || !rootKey.value) return;
  bulkSaving.value = true;
  const failed: string[] = [];
  try {
    // One at a time, on purpose: each create restarts the sync worker for the
    // new row, and a burst of parallel creates against one bucket is a burst of
    // parallel probes against it. A failure names the folder and the rest go on.
    for (const c of picked) {
      try {
        await storages.create({
          name: c.storageName.trim(),
          driver: driver.value,
          config: { ...config.value, [rootKey.value]: c.root },
          read_only: readOnly.value,
          sync_interval_s: secondsFromMinutes(syncIntervalMin.value),
        });
      } catch {
        failed.push(c.storageName.trim());
      }
    }
    const ok = picked.length - failed.length;
    if (!failed.length) {
      toast.success(t('storages.discover.createdOk', { count: ok }, ok));
      router.push({ name: 'storages' });
    } else {
      toast.error(
        t('storages.discover.createdPartial', {
          ok,
          total: picked.length,
          failed: failed.length,
          names: failed.join(', '),
        }),
      );
      // Leave the failed ones ticked and the created ones unticked, so a
      // second press retries exactly what did not land.
      for (const c of discovered.value ?? []) c.selected = failed.includes(c.storageName.trim());
    }
  } finally {
    bulkSaving.value = false;
  }
}
</script>

<template>
  <form
    class="space-y-5 max-w-2xl"
    @submit.prevent="submit"
  >
    <div class="flex items-center justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold">
          {{ t('storages.newTitle') }}
        </h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">
          {{ t('storages.subtitle') }}
        </p>
      </div>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        @click="router.push({ name: 'storages' })"
      >
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
      <Toggle
        v-model="readOnly"
        :label="t('storages.fields.readOnly')"
      />
      <Input
        v-model="syncIntervalMin"
        type="number"
        :min="1"
        :step="1"
        :label="t('storages.fields.syncInterval')"
        :hint="t('storages.fields.syncIntervalHint')"
        placeholder="15"
        data-testid="storage-sync-interval"
      />
    </div>

    <div class="card card-body">
      <StorageDriverFields
        v-model="config"
        :driver="driver"
      />
    </div>

    <div
      v-if="testResult"
      class="card card-body"
    >
      <div
        v-if="testResult.ok"
        class="flex items-center gap-2 text-sm text-emerald-600"
      >
        <Badge
          tone="emerald"
          dot
        >
          {{ t('storages.actions.ok') }}
        </Badge>
      </div>
      <div
        v-else
        class="space-y-2"
      >
        <Badge
          tone="rose"
          dot
        >
          {{ t('storages.actions.fail') }}
        </Badge>
        <p class="text-xs text-rose-600 dark:text-rose-400 font-mono break-all">
          {{ testResult.error }}
        </p>
      </div>
    </div>

    <div class="flex items-center justify-between gap-2">
      <Button
        type="button"
        variant="outline"
        :loading="testing"
        @click="test"
      >
        <Activity class="h-4 w-4" />
        {{ testing ? t('storages.actions.testing') : t('storages.actions.test') }}
      </Button>
      <div class="flex items-center gap-2">
        <Button
          type="button"
          variant="ghost"
          @click="router.push({ name: 'storages' })"
        >
          {{ t('common.cancel') }}
        </Button>
        <Button
          type="submit"
          :loading="saving"
        >
          <Save class="h-4 w-4" />
          {{ t('common.create') }}
        </Button>
      </div>
    </div>

    <!-- Several storages from the folders under this root. Below the single
         create on purpose: it is the exception, and it reuses everything typed
         above (driver, credentials, read-only, cadence). -->
    <div
      v-if="rootKey"
      class="card card-body space-y-3"
      data-testid="discover-card"
    >
      <div class="flex items-start gap-2">
        <FolderTree
          class="h-5 w-5 mt-0.5 shrink-0 text-zinc-500"
          aria-hidden="true"
        />
        <div>
          <h2 class="text-base font-semibold">
            {{ t('storages.discover.title') }}
          </h2>
          <p class="text-sm text-zinc-500 dark:text-zinc-400">
            {{ t('storages.discover.hint') }}
          </p>
        </div>
      </div>

      <div>
        <Button
          type="button"
          variant="outline"
          :loading="discovering"
          data-testid="discover-list"
          @click="listFolders"
        >
          <Layers class="h-4 w-4" />
          {{ discovering ? t('storages.discover.listing') : t('storages.discover.list') }}
        </Button>
      </div>

      <p
        v-if="discoverError"
        class="text-xs text-rose-600 dark:text-rose-400 font-mono break-all"
      >
        {{ discoverError }}
      </p>

      <template v-if="discovered">
        <p
          v-if="!discovered.length"
          class="text-sm text-zinc-500 dark:text-zinc-400"
        >
          {{ t('storages.discover.none') }}
        </p>
        <template v-else>
          <p class="text-sm text-zinc-600 dark:text-zinc-300">
            {{ t('storages.discover.found', { count: discovered.length }, discovered.length) }}
          </p>
          <div class="flex items-center gap-3 text-xs">
            <button
              type="button"
              class="underline text-zinc-600 dark:text-zinc-300"
              @click="selectAll(true)"
            >
              {{ t('storages.discover.selectAll') }}
            </button>
            <button
              type="button"
              class="underline text-zinc-600 dark:text-zinc-300"
              @click="selectAll(false)"
            >
              {{ t('storages.discover.selectNone') }}
            </button>
          </div>
          <ul
            class="divide-y divide-zinc-200 dark:divide-zinc-800"
            data-testid="discover-folders"
          >
            <li
              v-for="c in discovered"
              :key="c.root"
              class="py-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 items-start"
              :data-folder="c.name"
            >
              <Checkbox
                v-model="c.selected"
                :label="c.name"
              />
              <div class="space-y-1">
                <Input
                  v-model="c.storageName"
                  :label="t('storages.discover.nameLabel')"
                  :disabled="!c.selected"
                />
                <p class="font-mono text-xs text-zinc-500 dark:text-zinc-400 break-all">
                  {{ rootKey }} = {{ c.root }}
                </p>
              </div>
            </li>
          </ul>
          <div class="flex justify-end">
            <Button
              type="button"
              :loading="bulkSaving"
              :disabled="!selectedCount"
              data-testid="discover-create"
              @click="createDiscovered"
            >
              <Save class="h-4 w-4" />
              {{ bulkSaving ? t('storages.discover.creating') : t('storages.discover.create', { count: selectedCount }, selectedCount) }}
            </Button>
          </div>
        </template>
      </template>
    </div>
  </form>
</template>
