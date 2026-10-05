<script setup lang="ts">
/**
 * Groups (admin.users). A group is a named set of people in one tenant: it can
 * be given folder access in the explorer's sharing panel, like a person, and a
 * custom role — the role of every member who has none of their own. People
 * are added here by hand, or by linking the group to an SSO or LDAP group
 * their sign-in carries; LDAP directory sync brings the directory's groups in
 * as groups of their own. Each group is edited on its own page.
 */
import { computed, onMounted, ref } from 'vue';
import { RouterLink, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { UsersRound, Plus } from 'lucide-vue-next';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';

import { GroupsApi, type Group } from '@/api/groups';
import { RolesApi, type PermissionRule } from '@/api/roles';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import SourceBadge from '@/components/SourceBadge.vue';
import Select from '@/components/ui/Select.vue';
import Input from '@/components/ui/Input.vue';
import Modal from '@/components/ui/Modal.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t } = useI18n();
const toast = useToastStore();
const router = useRouter();

const groups = ref<Group[]>([]);
const roles = ref<PermissionRule[]>([]);
const loading = ref(true);
const q = ref('');
/** "" every group, or where its members come from: local, sso, ldap,
 *  synced (brought in by directory sync) or removed (its directory group is
 *  gone). */
const kind = ref('');

async function load() {
  loading.value = true;
  try {
    const [gs, rs] = await Promise.all([
      GroupsApi.list(),
      // Best effort: without it a group's role shows as its number.
      RolesApi.listRules().catch(() => ({ rules: [] as PermissionRule[] })),
    ]);
    groups.value = gs;
    roles.value = rs.rules;
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}
onMounted(load);

const shown = computed(() => {
  const term = q.value.trim().toLocaleLowerCase();
  const list = groups.value.filter(
    (g) =>
      ofKind(g, kind.value) &&
      (!term ||
        g.name.toLocaleLowerCase().includes(term) ||
        g.description.toLocaleLowerCase().includes(term) ||
        g.links.some((l) => l.value.toLocaleLowerCase().includes(term))),
  );
  return [...list].sort((a, b) => a.name.localeCompare(b.name));
});

/* The explorer's table (DataTable), remembered under `admin.groups`: every
 * column resizable, sortable and hideable like every other admin table
 * (docs/CONTRIBUTING.md → "One table"). A row's verb is behind its one
 * Actions control. */
const columns = computed<DataColumn<Group>[]>(() => [
  { id: 'name', label: t('groups.fields.name'), sortable: true, width: 260, sortValue: (g) => g.name.toLocaleLowerCase() },
  { id: 'source', label: t('groups.kindColumn'), sortable: true, width: 130, sortValue: (g) => kindsOf(g).join(',') },
  { id: 'role', label: t('groups.fields.role'), sortable: true, width: 200, sortValue: (g) => roleName(g) || null },
  { id: 'members', label: t('groups.membersTitle'), sortable: true, width: 130, sortValue: (g) => g.member_count ?? 0 },
  { id: 'folders', label: t('groups.foldersTitle'), sortable: true, width: 140, sortValue: (g) => g.grant_count ?? 0 },
  { id: 'origin', label: t('groups.originColumn'), sortable: true, width: 260, sortValue: (g) => originLine(g) || null },
]);
function rowActions(_g: Group): ContextAction[] {
  return [{ key: 'edit', label: t('common.edit'), icon: 'rename' }];
}
function onRowAction(key: string, g: Group) {
  if (key === 'edit') router.push({ name: 'groups.edit', params: { id: g.id } });
}

function roleName(g: Group): string {
  if (g.gives_admin) return t('users.roles.admin');
  if (g.role_id == null) return '';
  return roles.value.find((r) => r.id === g.role_id)?.name ?? `#${g.role_id}`;
}
function linksOf(g: Group, kind: string): string {
  return g.links
    .filter((l) => l.kind === kind)
    .map((l) => l.value)
    .join(', ');
}
/** Where the group's members come from: "local" (by hand only) or the kinds
 *  of directory it is linked to. */
function kindsOf(g: Group): string[] {
  const kinds = ['sso', 'ldap'].filter((k) => g.links.some((l) => l.kind === k));
  return kinds.length ? kinds : ['local'];
}
function ofKind(g: Group, k: string): boolean {
  switch (k) {
    case 'synced':
      return !!g.directory_id && g.directory_state !== 'removed';
    case 'removed':
      return g.directory_state === 'removed';
    case '':
      return true;
    default:
      return kindsOf(g).includes(k);
  }
}
const kindOptions = computed(() => [
  { value: '', label: t('groups.kind.all') },
  { value: 'local', label: t('groups.kind.local') },
  { value: 'sso', label: t('groups.kind.sso') },
  { value: 'ldap', label: t('groups.kind.ldap') },
  { value: 'synced', label: t('groups.kind.synced') },
  { value: 'removed', label: t('groups.kind.removed') },
]);
const hasDirectory = computed(() => groups.value.some((g) => g.links.length > 0 || g.directory_id));
/** Which directory a synced group follows (and its name there when renamed
 *  here), else the SSO / LDAP groups it is linked to, else "added by hand". */
function originLine(g: Group): string {
  if (g.directory_id) {
    const dir = g.directory_id.slice(0, g.directory_id.indexOf(':'));
    const from = dir === 'ldap' ? t('groups.card.fromMain') : t('groups.card.fromDirectory', { name: dir });
    return g.directory_name && g.directory_name !== g.name ? `${from} · cn=${g.directory_name}` : from;
  }
  const links = [
    linksOf(g, 'sso') ? t('groups.ssoSummary', { list: linksOf(g, 'sso') }) : '',
    linksOf(g, 'ldap') ? t('groups.ldapSummary', { list: linksOf(g, 'ldap') }) : '',
  ].filter(Boolean);
  return links.length ? links.join(' · ') : t('groups.card.byHand');
}

// ── new group: a name, then its page ──
const creating = ref(false);
const newName = ref('');
const saving = ref(false);
function openNew() {
  newName.value = '';
  creating.value = true;
}
async function create() {
  if (!newName.value.trim()) return;
  saving.value = true;
  try {
    const d = await GroupsApi.create({ name: newName.value.trim(), description: '', role_id: null, links: [] });
    creating.value = false;
    router.push({ name: 'groups.edit', params: { id: d.group.id } });
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-3 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold flex items-center gap-2">
          <UsersRound class="h-5 w-5" /> {{ t('groups.title') }}
        </h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400 max-w-2xl">{{ t('groups.subtitle') }}</p>
      </div>
      <Button data-testid="group-new" @click="openNew"><Plus class="h-4 w-4" /> {{ t('groups.add') }}</Button>
    </div>

    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <DataTable
      v-else
      table-id="admin.groups"
      :columns="columns"
      :rows="shown"
      :loading="loading"
      :empty="groups.length === 0 ? t('groups.empty') : t('groups.noMatch')"
      row-key="id"
      data-testid="groups-list"
      :row-actions="(g: Group) => rowActions(g)"
      :row-actions-test-id="(g: Group) => `group-actions-${g.id}`"
      @row-action="(key: string, g: Group) => onRowAction(key, g)"
    >
      <template #toolbar>
        <Input v-model="q" type="search" :placeholder="t('groups.search')" size="sm" class="w-64 max-w-full" />
        <Select v-if="hasDirectory" v-model="kind" :options="kindOptions" size="sm" data-testid="groups-kind" />
      </template>
      <!-- ⚠ Each cell is ONE root: a DataTable cell is a flex row. -->
      <template #cell-name="{ row }">
        <div :data-testid="`group-${(row as Group).id}`">
          <RouterLink :to="{ name: 'groups.edit', params: { id: (row as Group).id } }" class="font-medium hover:underline">{{ (row as Group).name }}</RouterLink>
          <Badge
            v-if="(row as Group).directory_state === 'removed'"
            tone="amber"
            size="xs"
            :data-testid="`group-removed-${(row as Group).id}`"
            :title="t('groups.directory.removedHint')"
          >{{ t('groups.directory.removed') }}</Badge>
          <span v-if="(row as Group).description" class="tbl-sub tbl-clamp" :title="(row as Group).description">{{ (row as Group).description }}</span>
        </div>
      </template>
      <template #cell-source="{ row }">
        <div class="flex gap-1 flex-wrap">
          <SourceBadge v-for="k in kindsOf(row as Group)" :key="k" :source="k" of="group" />
        </div>
      </template>
      <template #cell-role="{ row }">
        <div>
          <Badge v-if="roleName(row as Group)" tone="brand">{{ roleName(row as Group) }}</Badge>
          <span v-else class="tbl-sub">{{ t('groups.fields.noRole') }}</span>
          <span v-if="roleName(row as Group) && (row as Group).priority" class="tbl-sub">{{ t('groups.prioritySummary', { n: (row as Group).priority }) }}</span>
        </div>
      </template>
      <template #cell-members="{ row }">
        <span>{{ t('groups.members', { count: (row as Group).member_count ?? 0 }, (row as Group).member_count ?? 0) }}</span>
      </template>
      <template #cell-folders="{ row }">
        <span>{{ t('groups.folders', { count: (row as Group).grant_count ?? 0 }, (row as Group).grant_count ?? 0) }}</span>
      </template>
      <template #cell-origin="{ row }">
        <span
          :title="originLine(row as Group)"
          :data-testid="`group-origin-${(row as Group).id}`"
        >{{ originLine(row as Group) }}</span>
      </template>
    </DataTable>

    <Modal v-model="creating" :title="t('groups.add')" size="sm">
      <form class="space-y-3" @submit.prevent="create">
        <Input v-model="newName" :label="t('groups.fields.name')" required data-testid="group-new-name" />
      </form>
      <template #footer>
        <Button variant="ghost" @click="creating = false">{{ t('common.cancel') }}</Button>
        <Button :loading="saving" :disabled="!newName.trim()" data-testid="group-new-create" @click="create">{{ t('groups.create') }}</Button>
      </template>
    </Modal>
  </div>
</template>
