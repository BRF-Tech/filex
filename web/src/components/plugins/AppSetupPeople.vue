<script setup lang="ts">
/**
 * AppSetupPeople — the setup wizard's "Who may use it" step: each of the
 * app's own permissions (manifest `user_permissions`, `app.<app>.<id>`),
 * decided in the SAME three places Admin → Roles and a person's page decide
 * them (docs/PERMISSIONS.md → App permissions): a built-in role (User,
 * Viewer), a custom role, a person's exception.
 *
 * Groups: a group holds no permission of its own, it gives its members a
 * ROLE (docs/GROUPS.md → The role). Deciding for a group therefore decides
 * for that role, and so for everybody else who holds it.
 *
 * ⚠⚠ The owner's rule (2026-11-11): the step names WHO ELSE holds a role it
 * is about to change - how many people, which groups - and, when a role is
 * shared, recommends deciding per person instead. A decision that silently
 * reaches a hundred people is the mistake this step exists to prevent.
 *
 * Every decision is written at once, through the same endpoints the role and
 * person editors use (RolesApi). ⚠ A person's whole exception map is replaced
 * on PUT, so it is read first and written back whole.
 */
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { ChoiceButtons, DataTable, personName, pluginLabelOf, type ChoiceOption, type ContextAction, type DataColumn } from '@brftech/filex-core';

import type { AppSetupPermission } from '@/api/appPlugins';
import { extractError } from '@/api/client';
import { GroupsApi, type Group } from '@/api/groups';
import { RolesApi, type BuiltinRole, type BuiltinRoleAnswer, type PermEffect, type PermissionRule, type GroupRole } from '@/api/roles';
import type { User } from '@/api/types';
import { UsersApi } from '@/api/users';

import Input from '@/components/ui/Input.vue';
import Spinner from '@/components/ui/Spinner.vue';

const props = defineProps<{
  permissions: AppSetupPermission[];
}>();

const { t, locale } = useI18n();

type Decision = 'default' | 'allow' | 'deny';

const loading = ref(true);
const error = ref('');
const saving = ref('');
const builtin = ref<Record<BuiltinRole, BuiltinRoleAnswer | null>>({ user: null, viewer: null });
const rules = ref<PermissionRule[]>([]);
const assignments = ref<Record<string, number>>({});
const groupAssignments = ref<Record<string, GroupRole>>({});
const builtinMembers = ref<Partial<Record<'admin' | BuiltinRole, number>>>({});
const groups = ref<Group[]>([]);
const overrides = ref<Record<string, Record<string, PermEffect>>>({});
const users = ref<Record<number, User>>({});

async function load() {
  loading.value = true;
  error.value = '';
  try {
    const [u, v, list, gs, ov, us] = await Promise.all([
      RolesApi.getDefaults('user'),
      RolesApi.getDefaults('viewer'),
      RolesApi.listRules(),
      GroupsApi.list(),
      RolesApi.allOverrides(),
      UsersApi.list({ page_size: 500 }),
    ]);
    builtin.value = { user: u, viewer: v };
    rules.value = list.rules.filter((r) => r.enabled);
    assignments.value = list.assignments;
    groupAssignments.value = list.groupAssignments ?? {};
    builtinMembers.value = list.builtinMembers ?? {};
    groups.value = gs;
    overrides.value = ov;
    users.value = Object.fromEntries(us.items.map((x) => [x.id, x]));
  } catch (e: unknown) {
    error.value = extractError(e, t('errors.loadFailed'));
  } finally {
    loading.value = false;
  }
}
onMounted(load);

function decisionOf(apps: Record<string, PermEffect> | undefined, key: string): Decision {
  const d = apps?.[key];
  return d === 'allow' || d === 'deny' ? d : 'default';
}
function withDecision(apps: Record<string, PermEffect> | undefined, key: string, d: Decision): Record<string, PermEffect> {
  const next = { ...(apps ?? {}) };
  if (d === 'default') delete next[key];
  else next[key] = d;
  return next;
}

function defaultWord(p: AppSetupPermission): string {
  const known = ['viewer', 'user', 'admin'];
  return known.includes(p.default) ? t(`appPlugins.setup.people.default.${p.default}`) : p.default;
}
function choices(p: AppSetupPermission): ChoiceOption[] {
  return [
    { value: 'default', label: t('appPlugins.setup.people.useDefault'), help: defaultWord(p) },
    { value: 'allow', label: t('appPlugins.setup.people.allow') },
    { value: 'deny', label: t('appPlugins.setup.people.deny') },
  ];
}
const personChoices = computed<ChoiceOption[]>(() => [
  { value: 'allow', label: t('appPlugins.setup.people.allow') },
  { value: 'deny', label: t('appPlugins.setup.people.deny') },
]);

function personLabel(id: number | string): string {
  const u = users.value[Number(id)];
  return u ? personName(u) : `#${id}`;
}

/** The people (by own role or through a group) and groups that hold a custom role. */
function holdersOf(roleId: number) {
  const own = Object.entries(assignments.value).filter(([, rid]) => rid === roleId).map(([uid]) => Number(uid));
  const viaGroup = Object.entries(groupAssignments.value).filter(([, ga]) => ga.role_id === roleId).map(([uid]) => Number(uid));
  const gs = groups.value.filter((g) => g.role_id === roleId);
  return { people: [...new Set([...own, ...viaGroup])], groups: gs };
}

/** "3 people: Alice, Bob, Carol · groups: Sales (12)" - concrete, not "some". */
function holdersLine(roleId: number, exceptGroup?: number): string {
  const h = holdersOf(roleId);
  const parts: string[] = [];
  const names = h.people.slice(0, 3).map(personLabel);
  if (h.people.length) {
    const more = h.people.length - names.length;
    parts.push(
      more > 0
        ? t('appPlugins.setup.people.holdersPeopleMore', { n: h.people.length, names: names.join(', '), more }, h.people.length)
        : t('appPlugins.setup.people.holdersPeople', { n: h.people.length, names: names.join(', ') }, h.people.length),
    );
  }
  const otherGroups = h.groups.filter((g) => g.id !== exceptGroup);
  if (otherGroups.length) {
    parts.push(
      t('appPlugins.setup.people.holdersGroups', {
        groups: otherGroups.map((g) => (g.member_count != null ? `${g.name} (${g.member_count})` : g.name)).join(', '),
      }),
    );
  }
  return parts.length ? parts.join(' · ') : t('appPlugins.setup.people.holdersNone');
}

/** A role somebody else holds too: deciding on it reaches them as well. */
function shared(roleId: number, exceptGroup?: number): boolean {
  const h = holdersOf(roleId);
  return h.people.length > 0 || h.groups.some((g) => g.id !== exceptGroup);
}

function ruleName(r: PermissionRule): string {
  return r.names?.[locale.value] || r.name;
}

// ── roles ──
interface RoleRow {
  id: string;
  name: string;
  holders: string;
  builtin?: BuiltinRole;
  rule?: PermissionRule;
}
const roleRows = computed<RoleRow[]>(() => [
  {
    id: 'builtin:user',
    name: t('appPlugins.setup.people.roleUser'),
    holders: t('appPlugins.setup.people.holdersBuiltin', { n: builtinMembers.value.user ?? 0 }, builtinMembers.value.user ?? 0),
    builtin: 'user',
  },
  {
    id: 'builtin:viewer',
    name: t('appPlugins.setup.people.roleViewer'),
    holders: t('appPlugins.setup.people.holdersBuiltin', { n: builtinMembers.value.viewer ?? 0 }, builtinMembers.value.viewer ?? 0),
    builtin: 'viewer',
  },
  ...rules.value.map((r) => ({ id: `rule:${r.id}`, name: ruleName(r), holders: holdersLine(r.id), rule: r })),
]);

function roleDecision(row: RoleRow, key: string): Decision {
  if (row.builtin) return decisionOf(builtin.value[row.builtin]?.apps, key);
  return decisionOf(row.rule?.settings?.apps, key);
}

async function setBuiltin(role: BuiltinRole, key: string, d: Decision) {
  const cur = builtin.value[role];
  if (!cur) return;
  builtin.value[role] = await RolesApi.putDefaults(cur.permissions, role, withDecision(cur.apps, key, d));
}

async function setRule(rule: PermissionRule, key: string, d: Decision) {
  const { id, created_by: _c, created_at: _a, updated_at: _u, ...input } = rule;
  const next = await RolesApi.updateRule(id, { ...input, settings: { ...input.settings, apps: withDecision(input.settings?.apps, key, d) } });
  rules.value = rules.value.map((r) => (r.id === id ? next : r));
}

async function decideRole(row: RoleRow, key: string, v: string | string[]) {
  const d = String(v) as Decision;
  saving.value = `${row.id}:${key}`;
  error.value = '';
  try {
    if (row.builtin) await setBuiltin(row.builtin, key, d);
    else if (row.rule) await setRule(row.rule, key, d);
  } catch (e: unknown) {
    error.value = extractError(e, t('errors.updateFailed'));
  } finally {
    saving.value = '';
  }
}

// ── groups ──
function groupRule(g: Group): PermissionRule | null {
  return g.role_id ? rules.value.find((r) => r.id === g.role_id) ?? null : null;
}
const groupColumns = computed<DataColumn<Group>[]>(() => [
  { id: 'name', label: t('appPlugins.setup.people.colGroup'), width: 180, lead: true },
  { id: 'role', label: t('appPlugins.setup.people.colGivesRole'), width: 200 },
  { id: 'decision', label: t('appPlugins.setup.people.colDecision'), width: 300 },
]);

// ── people ──
interface PersonRow {
  id: number;
  decision: Decision;
}
function peopleWith(key: string): PersonRow[] {
  return Object.entries(overrides.value)
    .filter(([, o]) => o[key] === 'allow' || o[key] === 'deny')
    .map(([uid, o]) => ({ id: Number(uid), decision: o[key] as Decision }));
}
async function setPerson(userId: number, key: string, d: Decision) {
  saving.value = `person:${userId}:${key}`;
  error.value = '';
  try {
    const cur = await RolesApi.forUser(userId);
    const next = withDecision(cur.overrides, key, d);
    const res = await RolesApi.setOverrides(userId, next);
    overrides.value = { ...overrides.value, [String(userId)]: res.overrides ?? next };
  } catch (e: unknown) {
    error.value = extractError(e, t('errors.updateFailed'));
  } finally {
    saving.value = '';
  }
}

const query = ref<Record<string, string>>({});
const found = ref<Record<string, User[]>>({});
let searchTimer: ReturnType<typeof setTimeout> | undefined;
function search(key: string, q: string) {
  query.value = { ...query.value, [key]: q };
  if (searchTimer) clearTimeout(searchTimer);
  if (!q.trim()) {
    found.value = { ...found.value, [key]: [] };
    return;
  }
  searchTimer = setTimeout(async () => {
    try {
      const res = await UsersApi.list({ q: q.trim(), page_size: 8 });
      for (const u of res.items) users.value[u.id] = u;
      found.value = { ...found.value, [key]: res.items };
    } catch {
      found.value = { ...found.value, [key]: [] };
    }
  }, 250);
}
async function addPerson(key: string, u: User) {
  await setPerson(u.id, key, 'allow');
  query.value = { ...query.value, [key]: '' };
  found.value = { ...found.value, [key]: [] };
}

const roleColumns = computed<DataColumn<RoleRow>[]>(() => [
  { id: 'name', label: t('appPlugins.setup.people.colRole'), width: 160, lead: true },
  { id: 'holders', label: t('appPlugins.setup.people.colHolders'), width: 260 },
  { id: 'decision', label: t('appPlugins.setup.people.colDecision'), width: 300 },
]);
const personColumns = computed<DataColumn<PersonRow>[]>(() => [
  { id: 'person', label: t('appPlugins.setup.people.colPerson'), width: 240, lead: true, format: (r) => personLabel(r.id) },
  { id: 'decision', label: t('appPlugins.setup.people.colDecision'), width: 220 },
]);
function personActions(): ContextAction[] {
  return [{ key: 'clear', label: t('appPlugins.setup.people.clearPerson'), icon: 'close' }];
}
</script>

<template>
  <div class="space-y-6" data-testid="app-setup-people">
    <div v-if="loading" class="text-center text-zinc-500"><Spinner /></div>
    <template v-else>
      <p v-if="error" class="text-sm text-rose-700 dark:text-rose-300" role="alert" data-testid="app-setup-people-error">{{ error }}</p>
      <section v-for="p in permissions" :key="p.key" class="space-y-4" :data-testid="`app-setup-perm-${p.id}`">
        <header class="space-y-1">
          <h3 class="text-sm font-semibold">{{ pluginLabelOf(p.label, locale) || p.id }}</h3>
          <p v-if="p.description" class="text-xs text-zinc-600 dark:text-zinc-400">{{ pluginLabelOf(p.description, locale) }}</p>
          <p class="text-xs text-zinc-600 dark:text-zinc-400">{{ t('appPlugins.setup.people.appDefault', { who: defaultWord(p) }) }}</p>
        </header>

        <!-- Roles: a decision here reaches everybody who holds the role. -->
        <div class="space-y-2">
          <h4 class="text-xs font-semibold uppercase tracking-wide text-zinc-500">{{ t('appPlugins.setup.people.roles') }}</h4>
          <p class="text-xs text-amber-800 dark:text-amber-300" :data-testid="`app-setup-roles-note-${p.id}`">{{ t('appPlugins.setup.people.rolesNote') }}</p>
          <DataTable table-id="admin.app-setup.roles" :columns="roleColumns" :rows="roleRows" row-key="id">
            <template #cell-holders="{ row }">
              <span class="text-xs" :data-testid="`app-setup-holders-${row.id}`">{{ row.holders }}</span>
            </template>
            <template #cell-decision="{ row }">
              <ChoiceButtons
                :model-value="roleDecision(row, p.key)"
                :options="choices(p)"
                segmented
                :disabled="saving !== ''"
                :aria-label="row.name"
                :testid-prefix="`app-setup-role-${row.id}-${p.id}`"
                @update:model-value="(v: string | string[]) => decideRole(row, p.key, v)"
              />
            </template>
          </DataTable>
        </div>

        <!-- Groups: through the role each one gives. -->
        <div v-if="groups.length" class="space-y-2">
          <h4 class="text-xs font-semibold uppercase tracking-wide text-zinc-500">{{ t('appPlugins.setup.people.groups') }}</h4>
          <DataTable table-id="admin.app-setup.groups" :columns="groupColumns" :rows="groups" row-key="id">
            <template #cell-role="{ row }">
              <span class="text-xs">
                <template v-if="row.gives_admin">{{ t('appPlugins.setup.people.groupAdmin') }}</template>
                <template v-else-if="groupRule(row)">{{ ruleName(groupRule(row)!) }}</template>
                <template v-else>{{ t('appPlugins.setup.people.groupNoRole') }}</template>
              </span>
            </template>
            <template #cell-decision="{ row }">
              <div v-if="groupRule(row)" class="space-y-1">
                <ChoiceButtons
                  :model-value="decisionOf(groupRule(row)!.settings?.apps, p.key)"
                  :options="choices(p)"
                  segmented
                  :disabled="saving !== ''"
                  :aria-label="row.name"
                  :testid-prefix="`app-setup-group-${row.id}-${p.id}`"
                  @update:model-value="(v: string | string[]) => decideRole({ id: `rule:${groupRule(row)!.id}`, name: row.name, holders: '', rule: groupRule(row)! }, p.key, v)"
                />
                <p
                  v-if="shared(groupRule(row)!.id, row.id)"
                  class="text-xs text-amber-800 dark:text-amber-300"
                  :data-testid="`app-setup-group-shared-${row.id}`"
                >
                  {{ t('appPlugins.setup.people.groupShared', { role: ruleName(groupRule(row)!), who: holdersLine(groupRule(row)!.id, row.id) }) }}
                </p>
              </div>
              <span v-else class="text-xs text-zinc-500">{{ row.gives_admin ? t('appPlugins.setup.people.groupAdminHolds') : t('appPlugins.setup.people.groupNoRoleHint') }}</span>
            </template>
          </DataTable>
        </div>

        <!-- People: one person's exception, nobody else's. -->
        <div class="space-y-2">
          <h4 class="text-xs font-semibold uppercase tracking-wide text-zinc-500">{{ t('appPlugins.setup.people.people') }}</h4>
          <p class="text-xs text-zinc-600 dark:text-zinc-400">{{ t('appPlugins.setup.people.peopleNote') }}</p>
          <DataTable
            v-if="peopleWith(p.key).length"
            table-id="admin.app-setup.people"
            :columns="personColumns"
            :rows="peopleWith(p.key)"
            row-key="id"
            :row-actions="() => personActions()"
            :row-actions-test-id="(r: PersonRow) => `app-setup-person-actions-${r.id}`"
            @row-action="(_k: string, r: PersonRow) => setPerson(r.id, p.key, 'default')"
          >
            <template #cell-decision="{ row }">
              <ChoiceButtons
                :model-value="row.decision"
                :options="personChoices"
                segmented
                :disabled="saving !== ''"
                :aria-label="personLabel(row.id)"
                :testid-prefix="`app-setup-person-${row.id}-${p.id}`"
                @update:model-value="(v: string | string[]) => setPerson(row.id, p.key, String(v) as Decision)"
              />
            </template>
          </DataTable>
          <Input
            :model-value="query[p.key] ?? ''"
            :name="`app-setup-person-search-${p.id}`"
            :placeholder="t('appPlugins.setup.people.searchPerson')"
            autocomplete="off"
            @update:model-value="(v: string | number | null) => search(p.key, String(v ?? ''))"
          />
          <ul v-if="(found[p.key] ?? []).length" class="space-y-1" :data-testid="`app-setup-person-found-${p.id}`">
            <li v-for="u in found[p.key]" :key="u.id">
              <button
                type="button"
                class="w-full rounded-md px-2 py-1 text-start text-sm hover:bg-zinc-100 dark:hover:bg-zinc-800"
                :data-testid="`app-setup-person-pick-${u.id}`"
                @click="addPerson(p.key, u)"
              >
                {{ personName(u) }} <span class="text-xs text-zinc-500">{{ u.email }}</span>
              </button>
            </li>
          </ul>
        </div>
      </section>
    </template>
  </div>
</template>
