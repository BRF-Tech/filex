<script setup lang="ts">
/**
 * The groups one account is in, on its edit page — and how: added by hand, or
 * through an SSO link (kept in step with the identity provider at each
 * sign-in). Adding and removing go through the group's own routes, so the
 * same lines hold as on the group's page: a delegated administrator never
 * changes their own groups, nor gives a role they could not give directly.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { UsersRound, Plus } from 'lucide-vue-next';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';

import { GroupsApi, type Group, type UserGroup } from '@/api/groups';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Select from '@/components/ui/Select.vue';
import Spinner from '@/components/ui/Spinner.vue';

const props = defineProps<{ userId: number; userName: string }>();
const emit = defineEmits<{ (e: 'changed'): void }>();

const { t } = useI18n();
const toast = useToastStore();

const mine = ref<UserGroup[]>([]);
const all = ref<Group[]>([]);
const loading = ref(true);
const pick = ref<string>('');
const busy = ref(false);

async function load() {
  loading.value = true;
  try {
    const [m, a] = await Promise.all([GroupsApi.forUser(props.userId), GroupsApi.list()]);
    mine.value = m;
    all.value = a;
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}
onMounted(load);
watch(() => props.userId, load);

const options = computed(() => {
  const inIds = new Set(mine.value.map((g) => g.id));
  return [
    { value: '', label: t('groups.userCard.pick') },
    ...all.value.filter((g) => !inIds.has(g.id)).map((g) => ({ value: String(g.id), label: g.name })),
  ];
});

async function add() {
  if (!pick.value) return;
  busy.value = true;
  try {
    await GroupsApi.addMembers(Number(pick.value), [props.userId]);
    pick.value = '';
    await load();
    emit('changed');
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busy.value = false;
  }
}

/* THE table (DataTable, docs/CONTRIBUTING.md → "One table"), remembered
 * under `admin.users.groups`; a row's verb is behind its one Actions control. */
const columns = computed<DataColumn<UserGroup>[]>(() => [
  { id: 'name', label: t('common.name'), sortable: true, width: 240, sortValue: (g) => g.name.toLocaleLowerCase() },
  { id: 'source', label: t('groups.sourceColumn'), sortable: true, width: 160, sortValue: (g) => g.source },
]);
function rowActions(_g: UserGroup): ContextAction[] {
  return [{ key: 'remove', label: t('groups.remove'), icon: 'delete', danger: true, disabled: busy.value }];
}
function onRowAction(key: string, g: UserGroup) {
  if (key === 'remove') remove(g);
}

async function remove(g: UserGroup) {
  const msg =
    g.source === 'sso'
      ? t('groups.removeSsoConfirm', { name: props.userName })
      : t('groups.userCard.removeConfirm', { name: props.userName, group: g.name });
  if (!confirm(msg)) return;
  busy.value = true;
  try {
    await GroupsApi.removeMember(g.id, props.userId);
    await load();
    emit('changed');
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="card card-body space-y-3" data-testid="user-groups-card">
    <h2 class="flex items-center gap-2 text-base font-semibold"><UsersRound class="h-4 w-4" /> {{ t('groups.userCard.title') }}</h2>
    <div v-if="loading" class="text-center text-zinc-500"><Spinner /></div>
    <template v-else>
      <DataTable
        table-id="admin.users.groups"
        :columns="columns"
        :rows="mine"
        :empty="t('groups.userCard.none')"
        row-key="id"
        :row-actions="(g: UserGroup) => rowActions(g)"
        :row-actions-test-id="(g: UserGroup) => `user-group-actions-${g.id}`"
        @row-action="(key: string, g: UserGroup) => onRowAction(key, g)"
      >
        <!-- ⚠ Each cell is ONE root: a DataTable cell is a flex row. -->
        <template #cell-name="{ row }">
          <RouterLink :to="{ name: 'groups.edit', params: { id: (row as UserGroup).id } }" class="font-medium hover:underline">{{ (row as UserGroup).name }}</RouterLink>
        </template>
        <template #cell-source="{ row }">
          <div>
            <Badge :tone="(row as UserGroup).source === 'sso' ? 'sky' : 'zinc'">{{
              (row as UserGroup).source === 'sso' ? t('groups.source.sso') : t('groups.source.manual')
            }}</Badge>
          </div>
        </template>
      </DataTable>
      <div v-if="options.length > 1" class="flex items-end gap-2 flex-wrap">
        <Select v-model="pick" :options="options" :label="t('groups.userCard.add')" size="sm" class="w-60 max-w-full" />
        <Button type="button" size="sm" variant="outline" :disabled="!pick" :loading="busy" data-testid="user-group-add" @click="add">
          <Plus class="h-4 w-4" /> {{ t('groups.userCard.addButton') }}
        </Button>
      </div>
    </template>
  </div>
</template>
