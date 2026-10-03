<script setup lang="ts">
/**
 * Roles (administrators only). Every person has ONE role: a built-in one —
 * Administrator (everything, locked), User or Viewer, whose permissions are
 * edited here — or a custom one: its own list of permissions and limits,
 * optionally different in some folders (backend: permission rules). People are given a role on their own page; a custom
 * role can also be the starting role of new SSO accounts in some groups.
 */
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { ShieldCheck, Plus, Lock } from 'lucide-vue-next';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';

import { RolesApi, type BuiltinRole, type PermCatalogue, type PermissionRule } from '@/api/roles';
import { GroupsApi, type Group } from '@/api/groups';
import { StoragesApi } from '@/api/storages';
import type { StorageRef } from '@/api/types';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { permissionLimitLines } from '@/lib/permissionLimits';
import { roleDescription, roleName } from '@/lib/roleName';
import RoleEditor from '@/components/RoleEditor.vue';
import BuiltinRoleEditor from '@/components/BuiltinRoleEditor.vue';
import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Spinner from '@/components/ui/Spinner.vue';
import Modal from '@/components/ui/Modal.vue';
import Select from '@/components/ui/Select.vue';

const { t, te, locale } = useI18n();
const toast = useToastStore();

const catalogue = ref<PermCatalogue | null>(null);
const roles = ref<PermissionRule[]>([]);
/** user id → the custom role they hold. */
const assignments = ref<Record<string, number>>({});
/** user id → the role a group gives them (people with none of their own). */
const groupAssignments = ref<Record<string, { role_id: number }>>({});
const storages = ref<StorageRef[]>([]);
/** Groups, for how many hold each role (a group's role reaches its members). */
const groups = ref<Group[]>([]);
/** People on each built-in role itself (counted by the server). */
const builtinMembers = ref<Partial<Record<'admin' | BuiltinRole, number>>>({});
const loading = ref(true);

const editorOpen = ref(false);
const editing = ref<PermissionRule | null>(null);
const builtinOpen = ref(false);
const builtinRole = ref<BuiltinRole>('user');

async function load() {
  loading.value = true;
  try {
    const [cat, rs, sts, gs] = await Promise.all([
      RolesApi.catalogue(),
      RolesApi.listRules(),
      StoragesApi.list().catch(() => [] as StorageRef[]),
      GroupsApi.list().catch(() => [] as Group[]),
    ]);
    groups.value = gs;
    catalogue.value = cat;
    roles.value = rs.rules;
    assignments.value = rs.assignments;
    groupAssignments.value = rs.groupAssignments ?? {};
    builtinMembers.value = rs.builtinMembers ?? {};
    storages.value = sts;
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}
onMounted(load);

const builtins = computed(() =>
  (['admin', 'user', 'viewer'] as const).map((role) => ({
    role,
    // People on the built-in role itself, not on a custom role.
    members: builtinMembers.value[role] ?? 0,
  })),
);

// ⚠ The built-in roles are ONE row for the whole platform, and the server
// refuses a tenant admin's save (requireSupertenant in PutDefaults). A tenant's
// admin sees them read-only rather than a Save that is always refused. Until
// capabilities have loaded the answer is unknown, and the server decides.
const caps = useCapabilitiesStore();
const builtinReadOnly = computed(() => caps.loaded && caps.data.caller_admin === false);

function openBuiltin(role: BuiltinRole) {
  builtinRole.value = role;
  builtinOpen.value = true;
}
function openNew() {
  editing.value = null;
  editorOpen.value = true;
}
function openEdit(r: PermissionRule) {
  editing.value = r;
  editorOpen.value = true;
}
function onSaved(r: PermissionRule) {
  const i = roles.value.findIndex((x) => x.id === r.id);
  if (i >= 0) roles.value.splice(i, 1, r);
  else roles.value.push(r);
  // Recount who is on which role.
  RolesApi.listRules()
    .then((rs) => {
      assignments.value = rs.assignments;
      groupAssignments.value = rs.groupAssignments ?? {};
      builtinMembers.value = rs.builtinMembers ?? {};
    })
    .catch(() => {});
}

async function setEnabled(r: PermissionRule, enabled: boolean) {
  const n = memberCount(r) + viaGroupCount(r);
  // A switched-off role gives its people nothing until it is on again.
  if (!enabled && n > 0 && !confirm(t('permissions.rules.disableConfirm', { name: roleName(r, locale.value), count: n }, n))) return;
  try {
    const { id, created_by: _c, created_at: _a, updated_at: _u, ...body } = r;
    onSaved(await RolesApi.updateRule(id, { ...body, enabled }));
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

// Deleting: a role nobody holds just goes; one people hold asks what they
// become (the server refuses otherwise).
const deleting = ref<PermissionRule | null>(null);
const moveTo = ref('user');
const moveOptions = computed(() => [
  { value: 'user', label: t('users.roles.user') },
  { value: 'viewer', label: t('users.roles.viewer') },
  ...roles.value
    .filter((x) => x.id !== deleting.value?.id)
    .map((x) => ({ value: String(x.id), label: roleName(x, locale.value) })),
]);
async function remove(r: PermissionRule) {
  // People OR groups holding it: the server asks what they become either way.
  if (memberCount(r) > 0 || groupCount(r) > 0) {
    deleting.value = r;
    moveTo.value = 'user';
    return;
  }
  if (!confirm(t('permissions.rules.deleteConfirm', { name: roleName(r, locale.value) }))) return;
  await doDelete(r);
}
async function doDelete(r: PermissionRule, to?: string) {
  try {
    await RolesApi.deleteRule(r.id, to);
    roles.value = roles.value.filter((x) => x.id !== r.id);
    deleting.value = null;
    toast.success(t('permissions.rules.deleted'));
    await load();
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

function permLabel(k: string): string {
  const key = `permissions.items.${k}.label`;
  return te(key) ? t(key) : k;
}
function memberCount(r: PermissionRule): number {
  return Object.values(assignments.value).filter((id) => id === r.id).length;
}
/** People who hold the role through a group (none of their own). */
function viaGroupCount(r: PermissionRule): number {
  return Object.values(groupAssignments.value).filter((g) => g.role_id === r.id).length;
}
/** Groups that give the role to their members. */
function groupCount(r: PermissionRule): number {
  return groups.value.filter((g) => g.role_id === r.id).length;
}
/** "2 members · 3 through groups · 1 group" — or "3 members through groups"
 *  on its own when nobody holds the role directly. */
function membersText(r: PermissionRule): string {
  const n = memberCount(r);
  const v = viaGroupCount(r);
  const parts = [v && !n ? t('permissions.rules.viaGroups', { count: v }, v) : t('permissions.rules.members', { count: n }, n)];
  if (v && n) parts.push(t('permissions.rules.viaGroups', { count: v }, v));
  const g = groupCount(r);
  if (g) parts.push(t('permissions.rules.groups', { count: g }, g));
  return parts.join(' · ');
}
function ssoGroupsText(r: PermissionRule): string {
  const groups = r.targets.filter((x) => x.kind === 'sso_group').map((x) => x.value);
  return groups.length ? t('permissions.rules.plusGroups', { list: groups.join(', ') }) : '';
}
function placeText(r: PermissionRule): string {
  const parts: string[] = [];
  const ids = r.conditions?.storage_ids ?? [];
  if (ids.length) parts.push(ids.map((id) => storages.value.find((s) => s.id === id)?.name ?? `#${id}`).join(', '));
  const paths = r.conditions?.paths ?? [];
  if (paths.length) parts.push(paths.join(', '));
  return parts.join(' · ');
}
function effectsText(r: PermissionRule, eff: 'allow' | 'deny'): string {
  return Object.entries(r.effects)
    .filter(([, v]) => v === eff)
    .map(([k]) => permLabel(k))
    .join(', ');
}
function limitsText(r: PermissionRule): string {
  return permissionLimitLines(r.settings, t, locale.value).join('; ');
}

/* The explorer's table (DataTable), remembered under `admin.roles`: the
 * built-in roles first, then the custom ones, every column resizable,
 * sortable and hideable like every other table (docs/CONTRIBUTING.md →
 * "UI rules" → "One table"). A row's verbs are behind its one Actions
 * control; the Administrator row has none — it is locked. */
interface RoleRow {
  key: string;
  builtin?: 'admin' | BuiltinRole;
  rule?: PermissionRule;
  name: string;
  members: number;
}
const rows = computed<RoleRow[]>(() => [
  ...builtins.value.map((b) => ({ key: `builtin-${b.role}`, builtin: b.role, name: t(`users.roles.${b.role}`), members: b.members })),
  ...roles.value.map((r) => ({ key: `rule-${r.id}`, rule: r, name: roleName(r, locale.value), members: memberCount(r) + viaGroupCount(r) })),
]);
const columns = computed<DataColumn<RoleRow>[]>(() => [
  { id: 'name', label: t('permissions.rules.name'), sortable: true, width: 240 },
  { id: 'members', label: t('permissions.rules.membersColumn'), sortable: true, width: 150, sortValue: (row) => row.members },
  {
    id: 'summary',
    label: t('permissions.rules.effects'),
    sortable: true,
    width: 380,
    sortValue: (row) => (row.rule ? (row.rule.permissions ?? []).length : row.builtin === 'admin' ? 99 : null),
  },
  {
    id: 'enabled',
    label: t('permissions.rules.enabled'),
    sortable: true,
    width: 110,
    sortValue: (row) => (row.rule ? (row.rule.enabled ? 1 : 0) : 2),
  },
]);
function rowActions(row: RoleRow): ContextAction[] {
  if (row.builtin === 'admin') return [];
  if (row.builtin && builtinReadOnly.value) {
    return [{ key: 'edit', label: t('permissions.rules.viewBuiltin', { role: row.name }), icon: 'lock' }];
  }
  if (row.builtin) return [{ key: 'edit', label: t('permissions.rules.editBuiltin', { role: row.name }), icon: 'rename' }];
  return [
    { key: 'edit', label: t('permissions.rules.edit'), icon: 'rename' },
    { key: 'delete', label: t('common.delete'), icon: 'delete', danger: true },
  ];
}
function onRowAction(key: string, row: RoleRow) {
  if (row.builtin && row.builtin !== 'admin' && key === 'edit') openBuiltin(row.builtin);
  else if (row.rule && key === 'edit') openEdit(row.rule);
  else if (row.rule && key === 'delete') remove(row.rule);
}
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-3 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold flex items-center gap-2">
          <ShieldCheck class="h-5 w-5" /> {{ t('permissions.title') }}
        </h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400 max-w-2xl">{{ t('permissions.subtitle') }}</p>
      </div>
      <Button data-testid="rule-new" @click="openNew"><Plus class="h-4 w-4" /> {{ t('permissions.rules.add') }}</Button>
    </div>

    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <template v-else-if="catalogue">
      <DataTable
        table-id="admin.roles"
        :columns="columns"
        :rows="rows"
        :loading="loading"
        :empty="t('permissions.rules.empty')"
        row-key="key"
        data-testid="roles-list"
        :row-actions="(row: RoleRow) => rowActions(row)"
        :row-actions-test-id="(row: RoleRow) => `role-actions-${row.key}`"
        @row-action="(key: string, row: RoleRow) => onRowAction(key, row)"
      >
        <!-- ⚠ Each cell is ONE root: a DataTable cell is a flex row, and a
             Badge beside another root is squeezed under its own label. -->
        <template #cell-name="{ row }">
          <div :data-testid="`role-name-${row.key}`">
            <span class="font-medium">{{ row.name }}</span>
            <Badge v-if="row.builtin" tone="zinc" class="ms-2">{{ t('permissions.rules.builtin') }}</Badge>
            <Badge v-else tone="brand" class="ms-2">{{ t('permissions.rules.custom') }}</Badge>
            <Badge v-if="row.rule && !row.rule.enabled" tone="zinc" class="ms-1">{{ t('permissions.rules.disabled') }}</Badge>
            <span
              v-if="row.rule && roleDescription(row.rule, locale)"
              class="tbl-sub tbl-clamp"
              :title="roleDescription(row.rule, locale)"
              :data-testid="`role-description-${row.key}`"
            >{{ roleDescription(row.rule, locale) }}</span>
          </div>
        </template>

        <template #cell-members="{ row }">
          <div :data-testid="`role-members-${row.key}`">
            <template v-if="row.rule">{{ membersText(row.rule) }}</template>
            <template v-else>{{ t('permissions.rules.members', { count: row.members }, row.members) }}</template>
            <span v-if="row.rule && ssoGroupsText(row.rule)" class="tbl-sub">{{ ssoGroupsText(row.rule) }}</span>
          </div>
        </template>

        <template #cell-summary="{ row }">
          <div v-if="row.builtin === 'admin'" class="tbl-sub">{{ t('permissions.rules.locked') }}</div>
          <div v-else-if="row.builtin" class="tbl-sub">{{ t('permissions.rules.builtinSummary') }}</div>
          <div v-else-if="row.rule">
            <span>{{ t('permissions.rules.summaryCount', { count: (row.rule.permissions ?? []).length, total: catalogue ? catalogue.permissions.length - 1 : 0 }) }}</span>
            <span
              v-if="placeText(row.rule) && (effectsText(row.rule, 'deny') || effectsText(row.rule, 'allow'))"
              class="tbl-sub"
            >
              {{ t('permissions.rules.summaryFolders', { places: placeText(row.rule) }) }}
              <span v-if="effectsText(row.rule, 'allow')" class="text-emerald-700 dark:text-emerald-400">
                {{ t('permissions.rules.summaryAllow', { list: effectsText(row.rule, 'allow') }) }}
              </span>
              <span v-if="effectsText(row.rule, 'deny')" class="text-rose-700 dark:text-rose-400">
                {{ t('permissions.rules.summaryDeny', { list: effectsText(row.rule, 'deny') }) }}
              </span>
            </span>
            <span v-if="limitsText(row.rule)" class="tbl-sub">{{ t('permissions.rules.summaryLimits', { list: limitsText(row.rule) }) }}</span>
          </div>
        </template>

        <template #cell-enabled="{ row }">
          <div :data-testid="`role-enabled-${row.key}`">
            <!-- No label beside it: the column is the label, and "Enabled"
                 next to a switch that is off reads as a state. -->
            <Toggle
              v-if="row.rule"
              :model-value="row.rule.enabled"
              @update:model-value="(v: boolean) => row.rule && setEnabled(row.rule, v)"
            />
            <Lock v-else-if="row.builtin === 'admin'" class="h-4 w-4 text-zinc-400" :aria-label="t('permissions.rules.locked')" />
            <span v-else class="tbl-sub">-</span>
          </div>
        </template>
      </DataTable>
      <p v-if="roles.length === 0" class="text-sm text-zinc-500">{{ t('permissions.rules.empty') }}</p>

      <RoleEditor
        v-model="editorOpen"
        :rule="editing"
        :catalogue="catalogue"
        :storages="storages"
        @saved="onSaved"
      />
      <BuiltinRoleEditor v-model="builtinOpen" :role="builtinRole" :catalogue="catalogue" :readonly="builtinReadOnly" />

      <Modal
        :model-value="deleting !== null"
        :title="t('permissions.rules.deleteTitle', { name: roleName(deleting, locale) })"
        @update:model-value="(v: boolean) => { if (!v) deleting = null; }"
      >
        <div v-if="deleting" class="space-y-3" data-testid="role-delete-move">
          <p v-if="memberCount(deleting)" class="text-sm">
            {{ t('permissions.rules.deleteMove', { count: memberCount(deleting) }, memberCount(deleting)) }}
          </p>
          <p v-if="groupCount(deleting)" class="text-sm">
            {{ t('permissions.rules.deleteMoveGroups', { count: groupCount(deleting) }) }}
          </p>
          <Select v-model="moveTo" :options="moveOptions" :label="t('permissions.rules.moveTo')" class="w-60 max-w-full" />
        </div>
        <template #footer>
          <Button variant="ghost" @click="deleting = null">{{ t('common.cancel') }}</Button>
          <Button variant="danger" data-testid="role-delete-confirm" @click="deleting && doDelete(deleting, moveTo)">
            {{ t('common.delete') }}
          </Button>
        </template>
      </Modal>
    </template>
  </div>
</template>
