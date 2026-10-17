<script setup lang="ts">
/**
 * #117 — a storage's "Access" section: the whole storage given to people and
 * groups, on the storage's own page.
 *
 * An assignment is a grant on the storage's root (api/storageAccess): the row
 * a folder grant is, so it reaches every file and folder in the storage on
 * every surface - the web interface, the desktop app, WebDAV, SFTP, FTP, NFS,
 * the S3 API, an AI agent - with no rule of its own anywhere. The explorer's
 * storage row ("Access…") opens the same assignments in its access dialog.
 *
 * The rules the page says out loud, because they are the server's:
 *   - the higher level counts where a folder grant and the storage's both
 *     apply; nothing here narrows access (no deny);
 *   - "Revoke all access" takes back the storage-wide assignments only, one
 *     row each; folder grants are other rows and stay (listed under Folder
 *     access);
 *   - with per-item access control off, nobody can be given anything (the
 *     server answers 409). The section says so and offers to turn it on - in
 *     a dialog that first collects who keeps access, because the moment it is
 *     on, nobody but an administrator sees the storage without a grant (the
 *     owner's call, 2026-10-15: set the assignments up first, then turn it
 *     on).
 *
 * THE table (core DataTable, docs/CONTRIBUTING.md → "One table"), remembered
 * under `admin.storage.access`; the level is changed in its cell, the row's
 * one verb is "Revoke".
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { RouterLink } from 'vue-router';
import { RefreshCcw, ShieldAlert, UsersRound, X } from 'lucide-vue-next';
import { ChoiceButtons, DataTable, type ChoiceOption, type ContextAction, type DataColumn } from '@brftech/filex-core';

import {
  StorageAccessApi,
  accessKey,
  assigneeName,
  type AccessCandidate,
  type AccessLevel,
  type StorageAssignment,
} from '@/api/storageAccess';
import { extractError } from '@/api/client';
import type { StorageRef } from '@/api/types';
import { useStoragesStore } from '@/stores/storages';
import { useToastStore } from '@/stores/toast';
import AccessPicker from '@/components/AccessPicker.vue';
import Badge from '@/components/ui/Badge.vue';
import Button from '@/components/ui/Button.vue';
import Modal from '@/components/ui/Modal.vue';

const props = defineProps<{
  storageId: number;
  storageName: string;
  /** Per-item access control, as SAVED - not the form's unsaved switch. */
  rbacEnabled: boolean;
}>();
const emit = defineEmits<{ (e: 'rbac-enabled', storage: StorageRef): void }>();

const { t } = useI18n();
const toast = useToastStore();
const storages = useStoragesStore();

type Row = StorageAssignment & { row_key: string };
const rows = ref<Row[]>([]);
const loading = ref(true);
const busy = ref(false);

async function load() {
  loading.value = true;
  try {
    const got = await StorageAccessApi.list(props.storageName);
    rows.value = got.assignments.map((a) => ({ ...a, row_key: accessKey(a.kind, a.id) }));
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}
onMounted(load);
watch(() => props.storageName, load);

/** Who is already there, so the picker does not offer them again. */
const present = computed(() =>
  rows.value.map((r) => accessKey(r.kind, r.kind === 'group' ? r.group_id : r.user_id)),
);

const levelChoices = computed<ChoiceOption[]>(() =>
  (['viewer', 'editor', 'owner'] as const).map((v) => ({
    value: v,
    label: t(`groups.level.${v}`),
    help: t(`storages.access.levelHelp.${v}`),
  })),
);

/** Owner makes every member of a group a manager of who else gets in: ask. */
function ownerForGroupOk(kind: 'user' | 'group', name: string, level: AccessLevel): boolean {
  return kind !== 'group' || level !== 'owner' || confirm(t('storages.access.groupOwnerConfirm', { name }));
}

/** The names a partial failure leaves behind, for one toast. */
function failed(names: string[]) {
  if (names.length) toast.error(t('storages.access.someFailed', { names: names.join(', ') }));
}

async function give(who: AccessCandidate, level: AccessLevel) {
  if (!ownerForGroupOk(who.kind, who.name, level)) return;
  busy.value = true;
  try {
    await StorageAccessApi.assign(props.storageName, who, level);
    toast.success(t('storages.access.given'));
    await load();
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busy.value = false;
  }
}

async function changeLevel(row: Row, level: AccessLevel) {
  if (level === row.level) return;
  if (!ownerForGroupOk(row.kind, assigneeName(row), level)) return;
  busy.value = true;
  try {
    await StorageAccessApi.setLevel(row, level);
    row.level = level;
    toast.success(t('storages.access.changed'));
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
    await load();
  } finally {
    busy.value = false;
  }
}

async function revoke(row: Row) {
  if (!confirm(t('storages.access.revokeConfirm', { who: assigneeName(row) }))) return;
  busy.value = true;
  try {
    await StorageAccessApi.revoke(row);
    rows.value = rows.value.filter((r) => r.row_key !== row.row_key);
    toast.success(t('grants.revokedOk'));
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busy.value = false;
  }
}

async function revokeAll() {
  if (!rows.value.length) return;
  if (!confirm(t('storages.access.revokeAllConfirm', { name: props.storageName }))) return;
  busy.value = true;
  try {
    const left = await StorageAccessApi.revokeAll(rows.value);
    if (!left.length) toast.success(t('storages.access.revokedAll'));
    failed(left.map(assigneeName));
    await load();
  } finally {
    busy.value = false;
  }
}

const columns = computed<DataColumn<Row>[]>(() => [
  {
    id: 'who',
    label: t('grants.user'),
    sortable: true,
    width: 260,
    sortValue: (r) => assigneeName(r).toLocaleLowerCase(),
  },
  {
    id: 'level',
    label: t('grants.level'),
    sortable: true,
    width: 300,
    sortValue: (r) => ({ owner: 0, editor: 1, viewer: 2 })[r.level] ?? 9,
  },
]);

function rowActions(): ContextAction[] {
  return [{ key: 'revoke', label: t('grants.revoke'), icon: 'delete', danger: true }];
}
function onRowAction(key: string, row: Row) {
  if (key === 'revoke') void revoke(row);
}

/* ── per-item access off: the dialog that turns it on ──────────────────── */

const showEnable = ref(false);
/** Who keeps access the moment it is on: given right after the switch. */
const keepers = ref<Array<{ who: AccessCandidate; level: AccessLevel }>>([]);
const keeperKeys = computed(() => [...present.value, ...keepers.value.map((k) => accessKey(k.who.kind, k.who.id))]);

function openEnable() {
  keepers.value = [];
  showEnable.value = true;
}

function keep(who: AccessCandidate, level: AccessLevel) {
  if (!ownerForGroupOk(who.kind, who.name, level)) return;
  keepers.value = [...keepers.value, { who, level }];
}

function unkeep(i: number) {
  keepers.value = keepers.value.filter((_, j) => j !== i);
}

/**
 * Switch it on, then give each chosen person or group the storage. The two
 * cannot be one step: a grant is refused (409) until the switch is on. So the
 * people are chosen FIRST, here, and given in the same press - the moment in
 * between is a request long, not the time it takes an administrator to find
 * the section again.
 */
async function enable() {
  busy.value = true;
  try {
    const updated = await storages.update(props.storageId, { rbac_enabled: true });
    emit('rbac-enabled', updated);
    const notGiven: string[] = [];
    for (const k of keepers.value) {
      try {
        await StorageAccessApi.assign(props.storageName, k.who, k.level);
      } catch {
        notGiven.push(k.who.name);
      }
    }
    showEnable.value = false;
    toast.success(t('storages.access.enabledOk'));
    failed(notGiven);
    await load();
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <section class="card" data-testid="storage-access">
    <header class="card-header flex items-center justify-between gap-2">
      <h2 class="flex items-center gap-2 text-sm font-semibold">
        <UsersRound class="h-4 w-4" /> {{ t('storages.access.title') }}
      </h2>
      <Button variant="ghost" size="xs" :loading="loading" :aria-label="t('common.refresh')" @click="load">
        <RefreshCcw class="h-3.5 w-3.5" />
      </Button>
    </header>
    <div class="card-body space-y-3">
      <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('storages.access.hint') }}</p>

      <div
        v-if="!rbacEnabled"
        class="flex flex-wrap items-center justify-between gap-3 rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-300"
        data-testid="storage-access-rbac-off"
      >
        <p class="flex min-w-0 flex-1 basis-64 items-start gap-2">
          <ShieldAlert class="mt-0.5 h-4 w-4 shrink-0" />
          <span>{{ t('storages.access.rbacOff') }}</span>
        </p>
        <Button size="sm" data-testid="storage-access-enable" @click="openEnable">
          {{ t('storages.access.enable') }}
        </Button>
      </div>
      <AccessPicker v-else testid="storage-access-add" :exclude="present" :disabled="busy" @pick="give" />

      <DataTable
        table-id="admin.storage.access"
        :columns="columns"
        :rows="rows"
        row-key="row_key"
        :loading="loading"
        :empty="t('storages.access.empty')"
        :row-attrs="(r: Row) => ({ 'data-testid': `storage-access-row-${r.row_key}` })"
        :row-actions="() => rowActions()"
        :row-actions-test-id="(r: Row) => `storage-access-actions-${r.row_key}`"
        @row-action="(key: string, r: Row) => onRowAction(key, r)"
      >
        <template #toolbar>
          <Button
            variant="outline"
            size="sm"
            :disabled="!rows.length || busy"
            data-testid="storage-access-revoke-all"
            @click="revokeAll"
          >
            {{ t('storages.access.revokeAll') }}
          </Button>
        </template>
        <!-- ⚠ Each cell is ONE root: a DataTable cell is a flex row.
             ⚠ The `{{ ' ' }}` is the space before the Group pill. Vue's
             compiler drops a whitespace-only text node between two elements,
             so the name and the pill rendered welded ("DesignGroup", 0.57
             shots); Folder access has text before its pill and kept it. -->
        <template #cell-who="{ row }">
          <div class="min-w-0">
            <span class="font-medium">{{ assigneeName(row as Row) }}</span>{{ ' ' }}<Badge v-if="(row as Row).kind === 'group'" size="xs" tone="brand">{{ t('grants.group') }}</Badge>
            <span v-else-if="(row as Row).user_email" class="tbl-sub"><bdi>{{ (row as Row).user_email }}</bdi></span>
          </div>
        </template>
        <template #cell-level="{ row }">
          <div>
            <ChoiceButtons
              :options="levelChoices"
              :model-value="(row as Row).level"
              segmented
              :disabled="busy"
              :aria-label="assigneeName(row as Row)"
              :testid-prefix="`storage-access-level-${(row as Row).row_key}`"
              @update:model-value="(v: string | string[]) => changeLevel(row as Row, v as AccessLevel)"
            />
          </div>
        </template>
      </DataTable>

      <p class="text-xs text-zinc-500 dark:text-zinc-400">
        {{ t('storages.access.folderGrants') }}
        <RouterLink to="/grants" class="underline" data-testid="storage-access-folder-grants">{{ t('grants.title') }}</RouterLink>
      </p>
    </div>

    <Modal v-model="showEnable" :title="t('storages.access.enable')" size="lg">
      <div class="space-y-3" data-testid="storage-access-enable-dialog">
        <p class="flex items-start gap-2 text-sm text-amber-700 dark:text-amber-400" data-testid="storage-access-enable-warning">
          <ShieldAlert class="mt-0.5 h-4 w-4 shrink-0" />
          <span>{{ t('storages.access.enableWarning') }}</span>
        </p>
        <AccessPicker testid="storage-access-keep" :exclude="keeperKeys" :disabled="busy" @pick="keep" />
        <div>
          <h3 class="mb-1 text-xs font-semibold text-zinc-600 dark:text-zinc-300">{{ t('storages.access.enableKeepers') }}</h3>
          <ul v-if="keepers.length" class="flex flex-wrap gap-2" data-testid="storage-access-keepers">
            <li
              v-for="(k, i) in keepers"
              :key="accessKey(k.who.kind, k.who.id)"
              class="flex items-center gap-1.5 rounded-full border border-zinc-200 px-2.5 py-1 text-xs dark:border-zinc-700"
            >
              <span>{{ k.who.name }}</span>
              <span class="text-zinc-500">· {{ t(`groups.level.${k.level}`) }}</span>
              <button
                type="button"
                class="rounded p-0.5 hover:bg-[var(--fe-bg-hover)]"
                :aria-label="t('common.remove')"
                :title="t('common.remove')"
                @click="unkeep(i)"
              >
                <X class="h-3 w-3" />
              </button>
            </li>
          </ul>
          <p v-else class="text-xs text-zinc-500" data-testid="storage-access-keepers-none">{{ t('storages.access.enableNobody') }}</p>
        </div>
        <p v-if="rows.length" class="text-xs text-zinc-500" data-testid="storage-access-earlier">
          {{ t('storages.access.enableEarlier', { names: rows.map(assigneeName).join(', ') }) }}
        </p>
      </div>
      <template #footer>
        <Button variant="ghost" :disabled="busy" @click="showEnable = false">{{ t('common.cancel') }}</Button>
        <Button :loading="busy" data-testid="storage-access-enable-confirm" @click="enable">
          {{ t('storages.access.enableConfirm') }}
        </Button>
      </template>
    </Modal>
  </section>
</template>
