<script setup lang="ts">
/**
 * Roles (administrators only). Every person has ONE role: a built-in one —
 * Administrator (everything, locked), User or Viewer, whose permissions are
 * edited here — or a custom one: its own list of permissions and limits,
 * optionally different in some folders (backend: permission rules). People are given a role on their own page; a custom
 * role can also be the starting role of new SSO accounts in some groups.
 *
 * Above the table, the roles that allow adding files but not encrypting, as
 * a save on a version without files.encrypt (0.50 or older) leaves them
 * (backend perm/gaps.go): nothing gives the permission back by itself, so
 * each has one click to give it back and one to say it was on purpose.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { ShieldCheck, Plus, Lock, TriangleAlert } from 'lucide-vue-next';
import { RouterLink } from 'vue-router';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';

import { RolesApi, type BuiltinRole, type PermCatalogue, type PermGap, type PermissionRule } from '@/api/roles';
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
/** Roles that may have lost a permission (PermGap), for those who may edit them. */
const gaps = ref<PermGap[]>([]);
/** How many permissions each editable built-in role has (best effort). */
const builtinPerms = ref<Partial<Record<BuiltinRole, number>>>({});
const loading = ref(true);

const editorOpen = ref(false);
const editing = ref<PermissionRule | null>(null);
const builtinOpen = ref(false);
const builtinRole = ref<BuiltinRole>('user');

async function load() {
  loading.value = true;
  try {
    const [cat, rs, sts, gs, gp] = await Promise.all([
      RolesApi.catalogue(),
      RolesApi.listRules(),
      StoragesApi.list().catch(() => [] as StorageRef[]),
      GroupsApi.list().catch(() => [] as Group[]),
      // An administrator's alone: anyone else is refused and sees none.
      RolesApi.gaps().catch(() => [] as PermGap[]),
    ]);
    gaps.value = gp;
    groups.value = gs;
    catalogue.value = cat;
    roles.value = rs.rules;
    assignments.value = rs.assignments;
    groupAssignments.value = rs.groupAssignments ?? {};
    builtinMembers.value = rs.builtinMembers ?? {};
    storages.value = sts;
    void loadBuiltinPerms();
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}
onMounted(load);

async function loadBuiltinPerms() {
  const out: Partial<Record<BuiltinRole, number>> = {};
  await Promise.all(
    (['user', 'viewer'] as const).map(async (role) => {
      try {
        out[role] = (await RolesApi.getDefaults(role)).permissions.length;
      } catch {
        // An older server: the row says its permissions without a count.
      }
    }),
  );
  builtinPerms.value = out;
}
/** Every permission a role can hold (the catalogue less the one that is
 *  Administrator's alone). */
const totalPerms = computed(() => (catalogue.value ? catalogue.value.permissions.length - 1 : 0));

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
// A built-in role edited: its count may have moved.
watch(builtinOpen, (open) => {
  if (!open) void loadBuiltinPerms();
});
function openNew() {
  editing.value = null;
  editorOpen.value = true;
}
function openEdit(r: PermissionRule) {
  editing.value = r;
  editorOpen.value = true;
}
async function loadGaps() {
  gaps.value = await RolesApi.gaps().catch(() => [] as PermGap[]);
}
function onSaved(r: PermissionRule) {
  const i = roles.value.findIndex((x) => x.id === r.id);
  if (i >= 0) roles.value.splice(i, 1, r);
  else roles.value.push(r);
  void loadGaps();
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

/** The role a gap is in, by the name the table gives it. */
function gapRole(g: PermGap): string {
  if (g.role) return t(`users.roles.${g.role}`);
  const r = roles.value.find((x) => x.id === g.rule_id);
  return r ? roleName(r, locale.value) : (g.rule_name ?? '');
}
function gapText(g: PermGap): string {
  const vars = { role: gapRole(g), from: permLabel(g.from), key: permLabel(g.key) };
  return g.role ? t('permissions.gaps.builtin', vars) : t('permissions.gaps.folders', vars);
}
/** The gap a click is working on; its buttons wait for it. */
const gapBusy = ref('');
async function gapAction(g: PermGap, restore: boolean) {
  gapBusy.value = g.id;
  try {
    gaps.value = restore ? await RolesApi.restoreGap(g.id) : await RolesApi.dismissGap(g.id);
    toast.success(
      restore
        ? t('permissions.gaps.restored', { permission: permLabel(g.key), role: gapRole(g) })
        : t('permissions.gaps.dismissed', { role: gapRole(g) }),
    );
    // A custom role given the Allow in its folder part reads anew.
    if (restore && g.rule_id) roles.value = (await RolesApi.listRules()).rules;
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
    await loadGaps();
  } finally {
    gapBusy.value = '';
  }
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
/** Everyone who holds a custom role: their own, and through groups. */
function totalMembers(r: PermissionRule): number {
  return memberCount(r) + viaGroupCount(r);
}
/** How they hold it, under the count: "all through 1 group", "12 through
 *  2 groups", or — a group gives it but nobody is in one — "given by 1
 *  group". "" when everyone holds it as their own. */
function membersHow(r: PermissionRule): string {
  const n = memberCount(r);
  const v = viaGroupCount(r);
  const g = groupCount(r);
  if (v && !n) return t('permissions.rules.allThroughGroups', { count: g }, g);
  if (v) return t('permissions.rules.someThroughGroups', { n: v, count: g }, g);
  if (g) return t('permissions.rules.givenByGroups', { count: g }, g);
  return '';
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
function limitLines(r: PermissionRule): string[] {
  return permissionLimitLines(r.settings, t, locale.value);
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
const builtinRows = computed<RoleRow[]>(() =>
  builtins.value.map((b) => ({ key: `builtin-${b.role}`, builtin: b.role, name: t(`users.roles.${b.role}`), members: b.members })),
);
const customRows = computed<RoleRow[]>(() =>
  roles.value.map((r) => ({ key: `rule-${r.id}`, rule: r, name: roleName(r, locale.value), members: totalMembers(r) })),
);
const rows = computed<RoleRow[]>(() => [...builtinRows.value, ...customRows.value]);
/* Built-in roles, then a heading and the custom ones. ⚠ A grouped table draws
 * its rows as given (no header sort) — which is why no column sorts here:
 * six roles in a fixed, meaningful order beat an arrow that re-orders one
 * group. */
const tableGroups = computed(() => [
  { id: 'builtin', label: t('permissions.rules.builtinHeading'), items: builtinRows.value },
  ...(customRows.value.length ? [{ id: 'custom', label: t('permissions.rules.customHeading'), items: customRows.value }] : []),
]);
/** A built-in role's permission count, once read. */
function builtinCount(row: RoleRow): number | undefined {
  return row.builtin && row.builtin !== 'admin' ? builtinPerms.value[row.builtin] : undefined;
}
function openRow(row: RoleRow) {
  if (row.builtin && row.builtin !== 'admin') openBuiltin(row.builtin);
  else if (row.rule) openEdit(row.rule);
}
const columns = computed<DataColumn<RoleRow>[]>(() => [
  { id: 'name', label: t('permissions.rules.name'), width: 220 },
  { id: 'members', label: t('permissions.rules.membersColumn'), width: 190 },
  { id: 'summary', label: t('permissions.rules.effects'), width: 360 },
  { id: 'enabled', label: t('permissions.rules.statusColumn'), width: 130 },
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
      <div
        v-if="gaps.length"
        role="status"
        class="rounded-lg border border-amber-300 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-700/60 dark:bg-amber-950/40 dark:text-amber-200"
        data-testid="role-gaps"
      >
        <p class="flex items-center gap-1.5 font-medium">
          <TriangleAlert class="h-4 w-4 shrink-0" /> {{ t('permissions.gaps.title', { count: gaps.length }, gaps.length) }}
        </p>
        <p class="mt-1">{{ t('permissions.gaps.why') }}</p>
        <ul class="mt-2 space-y-2">
          <li v-for="g in gaps" :key="g.id" class="flex flex-wrap items-center gap-2" :data-testid="`role-gap-${g.id}`">
            <span class="min-w-0 flex-1">{{ gapText(g) }}</span>
            <Button
              size="sm"
              :disabled="gapBusy !== ''"
              :data-testid="`role-gap-restore-${g.id}`"
              @click="gapAction(g, true)"
            >
              {{ t('permissions.gaps.restore', { permission: permLabel(g.key) }) }}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              :disabled="gapBusy !== ''"
              :data-testid="`role-gap-dismiss-${g.id}`"
              @click="gapAction(g, false)"
            >
              {{ t('permissions.gaps.dismiss') }}
            </Button>
          </li>
        </ul>
      </div>

      <DataTable
        table-id="admin.roles"
        :columns="columns"
        :rows="rows"
        :groups="tableGroups"
        :loading="loading"
        :empty="t('permissions.rules.empty')"
        row-key="key"
        data-testid="roles-list"
        :row-actions="(row: RoleRow) => rowActions(row)"
        :row-actions-test-id="(row: RoleRow) => `role-actions-${row.key}`"
        :row-class="(row: RoleRow) => (row.builtin === 'admin' ? '' : 'cursor-pointer')"
        @row-action="(key: string, row: RoleRow) => onRowAction(key, row)"
        @row-click="(row: RoleRow) => openRow(row)"
      >
        <!-- ⚠ Each cell is ONE root: a DataTable cell is a flex row, and a
             Badge beside another root is squeezed under its own label. -->
        <template #cell-name="{ row }">
          <div :data-testid="`role-name-${row.key}`">
            <span class="font-medium">{{ row.name }}</span>
            <!-- Built-in or custom: the group heading says it. -->
            <Badge v-if="row.rule && !row.rule.enabled" tone="zinc" class="ms-2">{{ t('permissions.rules.disabled') }}</Badge>
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
            <span :class="row.members === 0 ? 'text-zinc-400 dark:text-zinc-500' : ''">
              {{ t('permissions.rules.members', { count: row.members }, row.members) }}
            </span>
            <RouterLink
              v-if="row.rule && membersHow(row.rule)"
              :to="{ name: 'groups' }"
              class="tbl-sub hover:underline"
              :data-testid="`role-members-how-${row.key}`"
              @click.stop
            >{{ membersHow(row.rule) }}</RouterLink>
            <span v-if="row.rule && ssoGroupsText(row.rule)" class="tbl-sub">{{ ssoGroupsText(row.rule) }}</span>
          </div>
        </template>

        <template #cell-summary="{ row }">
          <div v-if="row.builtin === 'admin'" :data-testid="`role-summary-${row.key}`">{{ t('permissions.rules.allPermissions') }}</div>
          <div v-else-if="row.builtin" :data-testid="`role-summary-${row.key}`">
            <template v-if="builtinCount(row) !== undefined">{{ t('permissions.rules.summaryCount', { count: builtinCount(row), total: totalPerms }) }}</template>
            <template v-else>{{ t('permissions.rules.builtinSummary') }}</template>
          </div>
          <div v-else-if="row.rule" :data-testid="`role-summary-${row.key}`">
            <span class="inline-flex flex-wrap items-center gap-1.5">
              {{ t('permissions.rules.summaryCount', { count: (row.rule.permissions ?? []).length, total: totalPerms }) }}
              <Badge
                v-if="limitLines(row.rule).length"
                tone="amber"
                size="xs"
                :title="limitLines(row.rule).join('\n')"
                :data-testid="`role-limits-${row.key}`"
              >{{ t('permissions.rules.limitsBadge', { count: limitLines(row.rule).length }, limitLines(row.rule).length) }}</Badge>
            </span>
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
          </div>
        </template>

        <template #cell-enabled="{ row }">
          <div :data-testid="`role-enabled-${row.key}`">
            <!-- No label beside it: the column is the label, and "Enabled"
                 next to a switch that is off reads as a state. -->
            <Toggle
              v-if="row.rule"
              :model-value="row.rule.enabled"
              @click.stop
              @update:model-value="(v: boolean) => row.rule && setEnabled(row.rule, v)"
            />
            <span v-else class="inline-flex items-center gap-1 text-xs text-zinc-500" :title="row.builtin === 'admin' ? t('permissions.rules.locked') : undefined">
              <Lock v-if="row.builtin === 'admin'" class="h-3.5 w-3.5" aria-hidden="true" />
              {{ t('permissions.rules.alwaysOn') }}
            </span>
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
      <BuiltinRoleEditor
        v-model="builtinOpen"
        :role="builtinRole"
        :catalogue="catalogue"
        :readonly="builtinReadOnly"
        @saved="loadGaps"
      />

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
