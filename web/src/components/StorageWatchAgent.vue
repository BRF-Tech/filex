<script setup lang="ts">
/**
 * #352 - a storage's filex-watch card, on the storage's own page.
 *
 * filex's own watch (Live watching) only sees what the filex machine writes.
 * When the storage's disk is somewhere else - a NAS share mounted over the
 * network, the folder an SFTP storage points at - filex-watch runs THERE and
 * reports the folders that change; filex looks at those folders alone
 * (docs/FILEX-WATCH.md). This card makes the key the agent reports with,
 * lists the keys with what each one's agent last said, and shows the install
 * commands.
 *
 * The rules it says out loud, because they are the server's:
 *   - a key is shown ONCE, in the answer that made it; afterwards the page
 *     lists it without it, and a lost key is revoked and made again;
 *   - the install commands are the server's (`setup`), shown as they come;
 *     the key is in them only right after it is made;
 *   - a key opens one door - this storage's change reports - and nothing
 *     else, so it is not one of the API keys on the API / MCP page's terms.
 *
 * THE table (core DataTable, docs/CONTRIBUTING.md -> "One table"), remembered
 * under `admin.storage.watch`; the row's one verb is "Revoke".
 *
 * Shown only for a driver whose descriptor says an agent can serve it
 * (`watch_agent`: real folders on a disk; not S3 or another object store, not
 * a driver that streams its own changes) - the driver's own answer, never a
 * list of names here. The server refuses a key for any other
 * (`watch_unsupported_driver`).
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { KeyRound, Radar, RefreshCcw } from 'lucide-vue-next';
import { ChoiceButtons, DataTable, type ChoiceOption, type ContextAction, type DataColumn } from '@brftech/filex-core';

import {
  StorageWatchApi,
  WATCH_PLATFORMS,
  type WatchKey,
  type WatchKeyMade,
  type WatchPlatform,
  type WatchStatus,
} from '@/api/storageWatch';
import { extractError } from '@/api/client';
import type { StorageDriver } from '@/api/types';
import { formatRelative } from '@/lib/format';
import { useStorageDriversStore } from '@/stores/storageDrivers';
import { useToastStore } from '@/stores/toast';
import Button from '@/components/ui/Button.vue';
import CopyButton from '@/components/ui/CopyButton.vue';
import Input from '@/components/ui/Input.vue';

const props = defineProps<{ storageId: number; driver: StorageDriver | undefined }>();

const { t, locale } = useI18n();
const toast = useToastStore();
const drivers = useStorageDriversStore();
/** The driver's own answer (descriptor `watch_agent`): no card, and nothing
 *  asked of the server, for a storage an agent cannot serve. */
const supported = computed(() => drivers.descriptor(props.driver)?.watch_agent === true);

const DOCS = 'https://docs.filex.sh/FILEX-WATCH';

const status = ref<WatchStatus | null>(null);
const loading = ref(true);
const busy = ref(false);
const name = ref('');
/** The key just made: shown until the person says they copied it. */
const made = ref<WatchKeyMade | null>(null);
const platform = ref<WatchPlatform>('linux');

async function load() {
  loading.value = true;
  try {
    status.value = await StorageWatchApi.status(props.storageId);
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}
onMounted(() => void drivers.fetch());
watch(
  [() => props.storageId, supported],
  ([id, on], [prevId]) => {
    // Another storage's page: the key just made is not this one's.
    if (prevId !== undefined && prevId !== id) made.value = null;
    if (on) void load();
  },
  { immediate: true },
);

const keys = computed(() => status.value?.keys ?? []);
const full = computed(() => {
  const max = status.value?.max_keys ?? 0;
  return max > 0 && keys.value.length >= max;
});
/** The commands: with the new key in them right after it is made. */
const setup = computed(() => made.value?.setup ?? status.value?.setup);
const command = computed(() => setup.value?.[platform.value] ?? '');

const platformChoices = computed<ChoiceOption[]>(() =>
  WATCH_PLATFORMS.map((p) => ({ value: p, label: t(`storageWatch.platform.${p}`) })),
);

async function createKey() {
  if (full.value) return;
  busy.value = true;
  try {
    made.value = await StorageWatchApi.createKey(props.storageId, name.value.trim());
    name.value = '';
    toast.success(t('storageWatch.made'));
    await load();
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busy.value = false;
  }
}

async function revoke(row: WatchKey) {
  if (!confirm(t('storageWatch.revokeConfirm', { name: row.name }))) return;
  busy.value = true;
  try {
    await StorageWatchApi.revokeKey(props.storageId, row.id);
    if (made.value?.row.id === row.id) made.value = null;
    toast.success(t('storageWatch.revoked'));
    await load();
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busy.value = false;
  }
}

/** When the key last reported: the agent's own word since filex started,
 *  else the key's last use. */
function seenAt(row: WatchKey): string | undefined {
  return row.agent?.seen_at ?? row.last_used_at;
}

function seenLabel(row: WatchKey): string {
  const at = seenAt(row);
  return at ? formatRelative(at, locale.value) : t('storageWatch.never');
}

/** "0.1.0 · nas01 · linux/amd64": what the agent said about itself. */
function agentLine(row: WatchKey): string {
  const a = row.agent?.agent;
  if (!a) return '';
  return [a.version, a.host, a.os && a.arch ? `${a.os}/${a.arch}` : a.os].filter(Boolean).join(' · ');
}

const columns = computed<DataColumn<WatchKey>[]>(() => [
  {
    id: 'name',
    label: t('storageWatch.cols.name'),
    sortable: true,
    width: 200,
    sortValue: (r) => r.name.toLocaleLowerCase(),
  },
  {
    id: 'agent',
    label: t('storageWatch.cols.agent'),
    sortable: true,
    width: 300,
    sortValue: (r) => r.agent?.agent.host ?? '',
  },
  {
    id: 'seen',
    label: t('storageWatch.cols.seen'),
    sortable: true,
    width: 180,
    sortValue: (r) => seenAt(r) ?? '',
  },
]);

function rowActions(): ContextAction[] {
  return [{ key: 'revoke', label: t('storageWatch.revoke'), icon: 'delete', danger: true }];
}
function onRowAction(key: string, row: WatchKey) {
  if (key === 'revoke') void revoke(row);
}
</script>

<template>
  <section v-if="supported" class="card" data-testid="storage-watch">
    <header class="card-header flex items-center justify-between gap-2">
      <h2 class="flex items-center gap-2 text-sm font-semibold">
        <Radar class="h-4 w-4" /> {{ t('storageWatch.title') }}
      </h2>
      <Button variant="ghost" size="xs" :loading="loading" :aria-label="t('common.refresh')" @click="load">
        <RefreshCcw class="h-3.5 w-3.5" />
      </Button>
    </header>
    <div class="card-body space-y-3">
      <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('storageWatch.hint') }}</p>
      <p class="text-xs text-zinc-500 dark:text-zinc-400">
        <span data-testid="storage-watch-number">{{ t('storageWatch.storageNumber', { id: storageId }) }}</span>
        ·
        <a :href="DOCS" target="_blank" rel="noopener" class="underline" data-testid="storage-watch-docs">{{ t('storageWatch.docs') }}</a>
      </p>

      <div
        v-if="made"
        class="space-y-2 rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200"
        data-testid="storage-watch-made"
      >
        <p class="font-medium">{{ t('storageWatch.shownOnce') }}</p>
        <div class="flex items-center gap-2">
          <code class="min-w-0 flex-1 break-all font-mono text-xs" data-testid="storage-watch-key">{{ made.key }}</code>
          <CopyButton :value="made.key" size="xs" />
        </div>
        <Button variant="ghost" size="xs" data-testid="storage-watch-made-done" @click="made = null">
          {{ t('storageWatch.copiedIt') }}
        </Button>
      </div>

      <form class="flex flex-wrap items-end gap-2" data-testid="storage-watch-form" @submit.prevent="createKey">
        <div class="min-w-0 flex-1 basis-56">
          <Input
            v-model="name"
            :label="t('storageWatch.keyName')"
            :placeholder="t('storageWatch.keyNamePlaceholder')"
            data-testid="storage-watch-name"
          />
        </div>
        <Button type="submit" size="sm" :loading="busy" :disabled="full" data-testid="storage-watch-create">
          <KeyRound class="h-4 w-4" /> {{ t('storageWatch.create') }}
        </Button>
      </form>
      <p v-if="full" class="text-xs text-amber-700 dark:text-amber-400" data-testid="storage-watch-full">
        {{ t('storageWatch.full', { max: status?.max_keys ?? 0 }) }}
      </p>

      <DataTable
        table-id="admin.storage.watch"
        :columns="columns"
        :rows="keys"
        row-key="id"
        :loading="loading"
        :empty="t('storageWatch.empty')"
        :row-attrs="(r: WatchKey) => ({ 'data-testid': `storage-watch-row-${r.id}` })"
        :row-actions="() => rowActions()"
        :row-actions-test-id="(r: WatchKey) => `storage-watch-actions-${r.id}`"
        @row-action="(key: string, r: WatchKey) => onRowAction(key, r)"
      >
        <template #cell-name="{ row }">
          <div class="min-w-0">
            <span class="font-medium">{{ (row as WatchKey).name }}</span>
            <span v-if="(row as WatchKey).created_by" class="tbl-sub">{{ t('storageWatch.madeBy', { who: (row as WatchKey).created_by }) }}</span>
          </div>
        </template>
        <template #cell-agent="{ row }">
          <div class="min-w-0">
            <span v-if="(row as WatchKey).agent" data-testid="storage-watch-agent">{{ agentLine(row as WatchKey) }}</span>
            <span v-else class="text-zinc-500">{{ t('storageWatch.noAgent') }}</span>
            <span v-if="(row as WatchKey).agent?.agent.root" class="tbl-sub"><bdi>{{ (row as WatchKey).agent?.agent.root }}</bdi></span>
          </div>
        </template>
        <template #cell-seen="{ row }">
          <span>{{ seenLabel(row as WatchKey) }}</span>
        </template>
      </DataTable>

      <div v-if="setup" class="space-y-2" data-testid="storage-watch-setup">
        <h3 class="text-xs font-semibold text-zinc-600 dark:text-zinc-300">{{ t('storageWatch.install') }}</h3>
        <ChoiceButtons
          :options="platformChoices"
          :model-value="platform"
          segmented
          :aria-label="t('storageWatch.install')"
          testid-prefix="storage-watch-platform"
          @update:model-value="(v: string | string[]) => (platform = v as WatchPlatform)"
        />
        <div class="relative">
          <pre
            class="overflow-x-auto whitespace-pre rounded-md bg-zinc-100 p-3 pe-20 font-mono text-xs dark:bg-zinc-800"
            data-testid="storage-watch-command"
          >{{ command }}</pre>
          <div class="absolute end-2 top-2">
            <CopyButton :value="command" size="xs" />
          </div>
        </div>
        <p class="text-xs text-zinc-500 dark:text-zinc-400">
          {{ made ? t('storageWatch.replaceFolder') : t('storageWatch.replaceBoth') }}
        </p>
      </div>
    </div>
  </section>
</template>
