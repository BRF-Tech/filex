<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { RouterLink, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { Plus, Trash2, Pencil, KeyRound, RefreshCcw, Eye, EyeOff, Wand2 } from 'lucide-vue-next';

import { useUsersStore } from '@/stores/users';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import type { User, UserRole } from '@/api/types';
import { emailProblem, normalizeUsername, refusalField, usernameProblem } from '@brftech/filex-core';
import { formatRelative } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import SourceBadge from '@/components/SourceBadge.vue';
import { RolesApi, type PermissionRule } from '@/api/roles';
import { GroupsApi, type Group, type Membership } from '@/api/groups';
import { useAuthStore } from '@/stores/auth';
import { roleDescription, roleName } from '@/lib/roleName';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Modal from '@/components/ui/Modal.vue';
import { ChoiceButtons, DataTable, setTableSort, tableSort, type ContextAction, type DataColumn } from '@brftech/filex-core';
import CopyButton from '@/components/ui/CopyButton.vue';
import ResetPasswordModal from '@/components/ResetPasswordModal.vue';

const { t, locale } = useI18n();
const router = useRouter();
const users = useUsersStore();
// ⚠ On a public demo the account on screen is the SHARED one every visitor
// signs in with, so "reset password" and "delete" are the two buttons that
// take the demo away from the next reader. The server refuses them either way
// (api/demo_guard.go); not drawing them is how a visitor finds that out
// without first breaking the demo for somebody else.
const caps = useCapabilitiesStore();
const toast = useToastStore();

const q = ref('');
// The role filter: '' (all), a built-in role, or "custom:<id>". Applied
// here: people on a custom role are listed under it, not under the level
// underneath it.
const role = ref<string>('');
/** "" is every group; otherwise a group's id. */
const groupFilter = ref<string>('');
const page = ref(1);
const pageSize = 25;

const showCreate = ref(false);
const showDelete = ref<User | null>(null);
const showReset = ref<User | null>(null);

const newEmail = ref('');
const newName = ref('');
// A built-in role, or a custom one as "custom:<id>".
const newRole = ref<string>('viewer');
const newPassword = ref('');
const newUsername = ref('');
/** How the new account signs in: a password typed or generated here, or an
 *  invitation that e-mails a first one. (People of an LDAP directory or an
 *  SSO provider are not added here: they arrive by sign-in and sync.) */
// How the new account signs in: its own password, an emailed invitation, or
// none here - an SSO account made ahead of its first sign-in, or one that
// only uses API keys (issue #25).
const newAccess = ref<'password' | 'invite' | 'none'>('password');
const showPassword = ref(false);
/** Hand-made groups to put the new account in. */
const newGroups = ref<number[]>([]);
const handGroups = ref<Group[]>([]);
/** Suggestions follow the address until a box is typed into by hand. */
const nameTouched = ref(false);
const usernameTouched = ref(false);
/** An address an LDAP directory owns: said, with a way to go on anyway. */
const directoryRefusal = ref<{ directory: string; label: string; message: string } | null>(null);
/** An invitation that could not be e-mailed: its first password, once. */
const invited = ref<{ email: string; password: string } | null>(null);
const auth = useAuthStore();
const creating = ref(false);
const deleting = ref(false);

/*
 * ⚠ "Add user" is checked HERE before anything is sent, and what the server
 * still refuses is said INSIDE the dialog. An empty submit used to reach the
 * server and come back as "email required" — English, in a toast drawn
 * BEHIND the dialog's backdrop, so nobody read it (release-candidate sweep,
 * 2026-09-21). The address is judged by lib/accountRules.ts, the same rules
 * the profile form and the server use.
 */
const createTried = ref(false);
const createRefusal = ref<{ field: string; message: string } | null>(null);
const createFailure = ref('');
watch(newEmail, (email) => {
  createRefusal.value = null;
  createFailure.value = '';
  directoryRefusal.value = null;
  if (!nameTouched.value) newName.value = suggestName(email);
  if (!usernameTouched.value) newUsername.value = suggestUsername(email);
});

/** "jane.doe@corp.com" → "Jane Doe". */
function suggestName(email: string): string {
  const local = email.trim().split('@')[0] ?? '';
  return local
    .split(/[._\-+]+/)
    .filter(Boolean)
    .map((w) => w.charAt(0).toLocaleUpperCase() + w.slice(1))
    .join(' ');
}
/** "Jane.Doe+x@corp.com" → "jane.doe": what identity allows, from the
 *  address's own name. */
function suggestUsername(email: string): string {
  const local = normalizeUsername(email.split('@')[0] ?? '').split('+')[0];
  let name = local.replace(/[^a-z0-9._-]+/g, '.').replace(/^[._-]+|[._-]+$/g, '');
  if (/^[0-9]/.test(name)) name = `u${name}`;
  return name;
}
const newUsernameError = computed(() => {
  if (createRefusal.value?.field === 'username') return createRefusal.value.message;
  if (!newUsername.value.trim()) return '';
  const p = usernameProblem(newUsername.value);
  return p ? t(`account.errors.${p.key}`, p.params ?? {}) : '';
});
watch(newUsername, () => {
  if (createRefusal.value?.field === 'username') createRefusal.value = null;
});

/** A strong first password: 16 characters, none that read alike. */
function generatePassword() {
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789-_!';
  const bytes = new Uint32Array(16);
  crypto.getRandomValues(bytes);
  newPassword.value = Array.from(bytes, (b) => alphabet[b % alphabet.length]).join('');
  showPassword.value = true;
}

/** One line under the role picker saying what it gives. */
const newRoleHint = computed(() => {
  const picked = newRole.value;
  if (picked.startsWith('custom:')) {
    const rule = customRules.value.find((r) => r.id === Number(picked.slice(7)));
    return (rule && roleDescription(rule, locale.value)) || t('users.add.roleHint.custom');
  }
  return t(`users.add.roleHint.${picked}`);
});
const newEmailError = computed(() => {
  if (createRefusal.value?.field === 'email') return createRefusal.value.message;
  // Said while typing once something is typed, and for an empty box only
  // after a submit — an error on a box nobody has reached yet is noise.
  if (!createTried.value && !newEmail.value.trim()) return '';
  const p = emailProblem(newEmail.value);
  return p ? t(`account.errors.${p.key}`, p.params ?? {}) : '';
});

function resetCreate() {
  newEmail.value = '';
  newName.value = '';
  newPassword.value = '';
  newUsername.value = '';
  nameTouched.value = false;
  usernameTouched.value = false;
  showPassword.value = false;
  createTried.value = false;
  createRefusal.value = null;
  createFailure.value = '';
  directoryRefusal.value = null;
}
function openCreate() {
  resetCreate();
  newRole.value = 'viewer';
  newAccess.value = 'password';
  newGroups.value = [];
  invited.value = null;
  showCreate.value = true;
  // Groups made here (a synced group fills itself from its directory).
  GroupsApi.list()
    .then((gs) => (handGroups.value = gs.filter((g) => !g.directory_id).sort((a, b) => a.name.localeCompare(b.name))))
    .catch(() => (handGroups.value = []));
}
/* Both pickers are core's ChoiceButtons (a radio group with arrow keys; a
 * set of toggles), not buttons drawn here: the same control, keyboard and
 * all, as every other choice the person can read without opening. */
const accessOptions = computed(() =>
  (['password', 'invite', 'none'] as const).map((v) => ({ value: v, label: t(`users.add.access.${v}`) })),
);
const groupChoices = computed(() => handGroups.value.map((g) => ({ value: String(g.id), label: g.name })));

async function load() {
  await Promise.all([
    users.fetch({
      q: q.value || undefined,
      role: undefined,
      page: page.value,
      page_size: pageSize,
    }),
    loadCustom(),
    loadGroups(),
  ]);
}

// user id → their groups (the Groups column), and every group anybody is in
// (the filter). Best effort, like the role markers: the list is whole
// without it.
const groupsOf = ref<Map<number, Membership[]>>(new Map());
async function loadGroups() {
  try {
    const all = await GroupsApi.memberships();
    groupsOf.value = new Map(Object.entries(all).map(([uid, gs]) => [Number(uid), gs]));
  } catch {
    groupsOf.value = new Map();
  }
}
const groupOptions = computed(() => {
  const names = new Map<number, string>();
  for (const gs of groupsOf.value.values()) for (const g of gs) names.set(g.id, g.name);
  return [
    { value: '', label: t('users.allGroups') },
    ...[...names].sort((a, b) => a[1].localeCompare(b[1])).map(([id, name]) => ({ value: String(id), label: name })),
  ];
});
const MEMBER_TONE: Record<string, 'zinc' | 'sky' | 'violet'> = { manual: 'zinc', sso: 'sky', ldap: 'violet' };
/** Groups a row names before "+N": a directory person is in dozens, and a
 *  row of chips per group made the list a wall. The server puts the groups
 *  that give a role or folder access first — the ones that matter here. */
const SHOWN_GROUPS = 2;

// Which accounts have their own permission overrides (backend internal/perm),
// for the "Custom permissions" marker. Best effort: an older server has no
// such endpoint, and the list is whole without it.
const customPerms = ref<Set<number>>(new Set());
// user id → the custom role it holds (one per person); its name, in the
// panel's language (lib/roleName), is shown in place of the built-in role.
const roleIdOf = ref<Map<number, number>>(new Map());
const customRules = ref<PermissionRule[]>([]);
// ⚠ computed: the names follow the panel's language when it changes.
const customRoleList = computed(() => customRules.value.map((r) => ({ id: r.id, name: roleName(r, locale.value) })));
const roleOf = computed(() => {
  const names = new Map(customRoleList.value.map((r) => [r.id, r.name]));
  const m = new Map<number, string>();
  for (const [uid, rid] of roleIdOf.value) {
    const name = names.get(rid);
    if (name) m.set(uid, name);
  }
  return m;
});
/** user id → the group their role comes from, when it is not their own. */
const viaOf = ref<Map<number, string>>(new Map());
async function loadCustom() {
  const [all, roles] = await Promise.all([
    RolesApi.allOverrides().catch(() => ({})),
    RolesApi.listRules().catch(() => ({
      rules: [],
      assignments: {} as Record<string, number>,
      groupAssignments: {} as Record<string, { role_id: number; group_name: string }>,
    })),
  ]);
  customPerms.value = new Set(
    Object.entries(all)
      .filter(([, m]) => Object.keys(m as object).length > 0)
      .map(([id]) => Number(id)),
  );
  const known = new Set(roles.rules.map((r) => r.id));
  const ids = new Map<number, number>();
  for (const [uid, rid] of Object.entries(roles.assignments)) {
    if (known.has(rid)) ids.set(Number(uid), rid);
  }
  // With none of their own, the role a group gives them is the one in force.
  const via = new Map<number, string>();
  for (const [uid, g] of Object.entries(roles.groupAssignments ?? {})) {
    if (known.has(g.role_id) && !ids.has(Number(uid))) {
      ids.set(Number(uid), g.role_id);
      via.set(Number(uid), g.group_name);
    }
  }
  roleIdOf.value = ids;
  viaOf.value = via;
  customRules.value = roles.rules;
}

// Search and the role filter narrow the rows already here (visibleRows):
// the server answers every account and reads neither, so asking it again
// on each keystroke only fetched the same list.
watch([q, role, groupFilter], () => {
  page.value = 1;
});

// ⚠ computed, not a plain array: a label built once at setup keeps the
// language the page was opened in when the language changes.
const builtinRoleOptions = computed(() => [
  { value: 'admin', label: t('users.roles.admin') },
  { value: 'user', label: t('users.roles.user') },
  { value: 'viewer', label: t('users.roles.viewer') },
]);
const roleOptions = computed(() => [
  { value: '', label: t('common.all') },
  ...builtinRoleOptions.value,
  ...customRoleList.value.map((r) => ({ value: `custom:${r.id}`, label: r.name })),
]);

const createRoleOptions = computed(() => [
  ...builtinRoleOptions.value,
  ...customRoleList.value.map((r) => ({ value: `custom:${r.id}`, label: r.name })),
]);

const visibleRows = computed(() => {
  const f = role.value;
  const gf = groupFilter.value === '' ? null : Number(groupFilter.value);
  const needle = q.value.trim().toLocaleLowerCase();
  if (!f && !needle && gf === null) return users.page.items;
  return users.page.items.filter((u) => {
    if (needle && ![u.email, u.display_name, u.username].some((x) => (x ?? '').toLocaleLowerCase().includes(needle))) {
      return false;
    }
    if (gf !== null && !(groupsOf.value.get(u.id) ?? []).some((g) => g.id === gf)) return false;
    if (!f) return true;
    const held = u.role !== 'admin' ? roleIdOf.value.get(u.id) : undefined;
    if (f.startsWith('custom:')) return held === Number(f.slice(7));
    return u.role === f && held === undefined;
  });
});

/* The explorer's table (DataTable): every column resizes, hides, moves and
 * sorts, and the arrangement is remembered on the account under
 * `admin.users`. ⚠ The server answers EVERY account in one list (it reads no
 * page or sort parameter), so this page sorts and pages that list itself
 * (sortedRows, pagedRows) and hands the table one page of it. The table is
 * told the sort (`sort`), so it neither re-sorts 25 rows of 300 nor closes
 * its headers: the order is the whole list's. Paging used to ask the server
 * for "page 2" and get all of them again — Next did nothing. */
const ROLE_RANK: Record<UserRole, number> = { admin: 0, user: 1, viewer: 2 };

type Sort = { key: string; dir: 'asc' | 'desc' };
const TABLE_ID = 'admin.users';
const sort = ref<Sort | null>(tableSort(TABLE_ID));
function onSort(next: Sort) {
  sort.value = next;
  setTableSort(TABLE_ID, next);
  page.value = 1;
}
/** Empty last either way; numbers as numbers; words in the panel's language. */
function compare(a: unknown, b: unknown): number {
  if (typeof a === 'number' && typeof b === 'number') return a - b;
  return String(a).localeCompare(String(b), locale.value, { numeric: true, sensitivity: 'base' });
}
const sortedRows = computed(() => {
  const s = sort.value;
  const col = s ? columns.value.find((c) => c.id === s.key) : undefined;
  if (!s || !col) return visibleRows.value;
  const value = (u: User): unknown => (col.sortValue ? col.sortValue(u) : (u as unknown as Record<string, unknown>)[col.id]);
  const dir = s.dir === 'asc' ? 1 : -1;
  return visibleRows.value
    .map((u, i) => ({ u, i, v: value(u) }))
    .sort((x, y) => {
      const xe = x.v === null || x.v === undefined || x.v === '';
      const ye = y.v === null || y.v === undefined || y.v === '';
      if (xe !== ye) return xe ? 1 : -1;
      return (xe ? 0 : dir * compare(x.v, y.v)) || x.i - y.i;
    })
    .map((x) => x.u);
});
const pagedRows = computed(() => sortedRows.value.slice((page.value - 1) * pageSize, page.value * pageSize));
const columns = computed<DataColumn<User>[]>(() => [
  { id: 'email', label: t('common.email'), sortable: true, width: 240 },
  { id: 'display_name', label: t('users.fields.displayName'), sortable: true, width: 180 },
  {
    id: 'auth_source',
    label: t('users.fields.source'),
    sortable: true,
    width: 90,
    sortValue: (u) => u.auth_source || 'local',
  },
  {
    id: 'role',
    label: t('common.role'),
    sortable: true,
    // Wide enough for a role and "through {group}" on one line.
    width: 180,
    sortValue: (u) => ROLE_RANK[u.role] ?? 9,
  },
  {
    id: 'groups',
    label: t('users.fields.groups'),
    sortable: true,
    width: 200,
    sortValue: (u) => (groupsOf.value.get(u.id) ?? []).map((g) => g.name).join(', ') || null,
  },
  {
    id: 'last_login_at',
    label: t('users.fields.lastLogin'),
    sortable: true,
    sortDir: 'desc',
    width: 150,
    sortValue: (u) => (u.last_login_at ? Date.parse(u.last_login_at) : null),
  },
]);
// A filter, a deletion or a reload can leave the page past the end.
// ⚠ Below `columns`: the watch reads sortedRows at once, and with a sort
// remembered from an earlier visit that reads `columns` — above it, the page
// failed to open for anyone who had sorted the list.
watch(
  () => sortedRows.value.length,
  (n) => {
    const last = Math.max(1, Math.ceil(n / pageSize));
    if (page.value > last) page.value = last;
  },
);

const roleTone = (r: UserRole) => {
  if (r === 'admin') return 'rose';
  if (r === 'user') return 'amber';
  return 'zinc';
};

async function submitCreate(another = false, anyway = false) {
  createTried.value = true;
  createFailure.value = '';
  if (newEmailError.value || newUsernameError.value || creating.value) return;
  if (newAccess.value === 'password' && !newPassword.value) {
    createFailure.value = t('users.add.passwordNeeded');
    return;
  }
  creating.value = true;
  const picked = newRole.value;
  const customId = picked.startsWith('custom:') ? Number(picked.slice(7)) : null;
  const email = newEmail.value.trim();
  try {
    // A custom role: the account starts as a Viewer — the least it can be —
    // and the role call then sets the level the role needs. If that call
    // fails, the person is a Viewer, never more.
    const created = await users.create({
      email,
      display_name: newName.value.trim(),
      role: customId ? 'viewer' : (picked as UserRole),
      username: newUsername.value.trim() || undefined,
      password: newAccess.value === 'password' ? newPassword.value : undefined,
      send_invite: newAccess.value === 'invite' || undefined,
      allow_directory_email: anyway || undefined,
    });
    if (customId) {
      try {
        await RolesApi.setUserRole(created.id, customId);
      } catch (e: unknown) {
        toast.error(t('users.createdRoleNotSet', { error: extractError(e, t('errors.generic')) }));
      }
    }
    // Its groups: each on its own, so one refused (a delegated
    // administrator's line) does not undo the account or the others.
    for (const gid of newGroups.value) {
      try {
        await GroupsApi.addMembers(gid, [created.id]);
      } catch (e: unknown) {
        const g = handGroups.value.find((x) => x.id === gid);
        toast.error(t('users.add.groupNotSet', { group: g?.name ?? `#${gid}`, error: extractError(e, t('errors.generic')) }));
      }
    }
    if (customId || newGroups.value.length) await Promise.all([load(), loadCustom()]);
    if (created.invite?.emailed) toast.success(t('users.add.invitedOk', { email }));
    else toast.success(t('users.createdOk'));
    if (created.invite && !created.invite.emailed && created.invite.temp_password) {
      // No mail could go out: the first password, once, for passing on.
      invited.value = { email, password: created.invite.temp_password };
      resetCreate();
      return;
    }
    if (another) resetCreate();
    else showCreate.value = false;
  } catch (e: unknown) {
    const body = (e as { response?: { data?: { error?: string; directory?: string; label?: string; message?: string } } })?.response?.data;
    if (body?.error === 'directory_email') {
      directoryRefusal.value = { directory: body.directory ?? '', label: body.label ?? '', message: body.message ?? '' };
      return;
    }
    const refusal = refusalField(e);
    if (refusal) createRefusal.value = refusal;
    else createFailure.value = extractError(e, t('errors.generic'));
  } finally {
    creating.value = false;
  }
}

async function confirmDelete() {
  if (!showDelete.value) return;
  deleting.value = true;
  try {
    await users.remove(showDelete.value.id);
    toast.success(t('users.deletedOk'));
    showDelete.value = null;
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    deleting.value = false;
  }
}

onMounted(load);

/** The row's verbs. They were three unlabelled icon buttons — a pencil, a key
 *  and a bin — which is the row the owner pointed at ("karma karışık"): the
 *  key meant "reset password" and nothing on screen said so.
 *
 * ⚠ On the read-only demo the two writing verbs stay VISIBLE and greyed, with
 * the reason in their title, rather than vanishing: grey with a reason reads
 * as a rule, absent reads as a feature that does not exist. Delete and reset
 * keep their own confirmation dialogs (`showDelete`, `showReset`). */
function rowActions(_row: User): ContextAction[] {
  const ro = caps.demoReadOnly;
  const why = ro ? t('userSettings.demoReadOnly') : undefined;
  return [
    { key: 'edit', label: t('common.edit'), icon: 'rename' },
    { key: 'reset', label: t('users.resetPassword'), icon: 'lock', disabled: ro, title: why },
    {
      key: 'delete',
      label: t('common.delete'),
      icon: 'delete',
      danger: true,
      disabled: ro,
      title: why,
    },
  ];
}

function onRowAction(key: string, row: User) {
  if (key === 'edit') router.push({ name: 'users.edit', params: { id: row.id } });
  else if (key === 'reset') showReset.value = row;
  else if (key === 'delete') showDelete.value = row;
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-end justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold">{{ t('users.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('users.subtitle') }}</p>
      </div>
      <div class="flex items-center gap-2">
        <Button variant="outline" size="sm" @click="load" :loading="users.loading">
          <RefreshCcw class="h-4 w-4" />
          {{ t('common.refresh') }}
        </Button>
        <Button v-if="!caps.demoReadOnly" @click="openCreate">
          <Plus class="h-4 w-4" />
          {{ t('users.addNew') }}
        </Button>
      </div>
    </div>

    <DataTable
      table-id="admin.users"
      :columns="columns"
      :rows="pagedRows"
      :loading="users.loading"
      :empty="t('common.none')"
      :page="page"
      :page-size="pageSize"
      :total="sortedRows.length"
      :sort="sort"
      row-key="id"
      :row-actions="(row: User) => rowActions(row)"
      :row-actions-test-id="(row: User) => `user-actions-${row.id}`"
      @row-action="(key: string, row: User) => onRowAction(key, row)"
      @page="(p: number) => (page = p)"
      @sort="onSort"
    >
      <template #toolbar>
        <Input
          v-model="q"
          :placeholder="t('common.search')"
          size="sm"
          class="w-60"
          autocomplete="off"
        />
        <Select v-model="role" :options="roleOptions" size="sm" />
        <Select
          v-if="groupOptions.length > 1"
          v-model="groupFilter"
          :options="groupOptions"
          size="sm"
          data-testid="users-group-filter"
        />
      </template>

      <template #cell-role="{ row }">
        <span class="inline-flex flex-wrap items-center gap-1">
        <template v-if="(row as User).role !== 'admin' && roleOf.has((row as User).id)">
          <Badge
            tone="brand"
            size="xs"
            :title="viaOf.has((row as User).id) ? t('users.viaGroup', { group: viaOf.get((row as User).id) }) : undefined"
          >
            {{ roleOf.get((row as User).id) }}
          </Badge>
          <span v-if="viaOf.has((row as User).id)" class="text-xs text-zinc-500" data-testid="user-role-via">
            {{ t('users.viaGroup', { group: viaOf.get((row as User).id) }) }}
          </span>
        </template>
        <Badge v-else :tone="roleTone((row as User).role)" size="xs">
          {{ t(`users.roles.${(row as User).role}`) }}
        </Badge>
        <Badge
          v-if="customPerms.has((row as User).id)"
          tone="amber"
          size="xs"
          :data-testid="`user-custom-perms-${(row as User).id}`"
        >
          {{ t('permissions.customBadge') }}
        </Badge>
        <Badge
          v-if="(row as User).enabled === false && (row as User).disabled_reason === 'pending_approval'"
          tone="amber"
          size="xs"
          :title="t('users.status.pendingTitle')"
          :data-testid="`user-pending-${(row as User).id}`"
        >
          {{ t('users.status.pending') }}
        </Badge>
        <Badge
          v-else-if="(row as User).enabled === false && (row as User).disabled_reason === 'directory'"
          tone="zinc"
          size="xs"
          :title="t('users.status.directoryTitle')"
          :data-testid="`user-disabled-${(row as User).id}`"
        >
          {{ t('users.status.directory') }}
        </Badge>
        <Badge
          v-else-if="(row as User).enabled === false"
          tone="zinc"
          size="xs"
          :data-testid="`user-disabled-${(row as User).id}`"
        >
          {{ t('users.status.disabled') }}
        </Badge>
        </span>
      </template>

      <template #cell-auth_source="{ row }">
        <SourceBadge :source="(row as User).auth_source" :directory="(row as User).auth_directory" />
      </template>

      <template #cell-groups="{ row }">
        <span v-if="groupsOf.get((row as User).id)?.length" class="inline-flex flex-wrap items-center gap-1" :data-testid="`user-groups-${(row as User).id}`">
          <RouterLink
            v-for="g in (groupsOf.get((row as User).id) ?? []).slice(0, SHOWN_GROUPS)"
            :key="g.id"
            :to="{ name: 'groups.edit', params: { id: g.id } }"
            :title="t(`sources.memberHint.${MEMBER_TONE[g.source] ? g.source : 'manual'}`)"
          >
            <Badge :tone="MEMBER_TONE[g.source] ?? 'zinc'" size="xs" class="hover:underline">{{ g.name }}</Badge>
          </RouterLink>
          <RouterLink
            v-if="(groupsOf.get((row as User).id)?.length ?? 0) > SHOWN_GROUPS"
            :to="{ name: 'users.edit', params: { id: (row as User).id } }"
            :title="(groupsOf.get((row as User).id) ?? []).slice(SHOWN_GROUPS).map((g) => g.name).join(', ')"
            :data-testid="`user-groups-more-${(row as User).id}`"
          >
            <Badge tone="zinc" size="xs" class="hover:underline">+{{ (groupsOf.get((row as User).id)?.length ?? 0) - SHOWN_GROUPS }}</Badge>
          </RouterLink>
        </span>
        <span v-else class="text-xs text-zinc-500">-</span>
      </template>

      <template #cell-last_login_at="{ row }">
        <span class="text-xs text-zinc-500">{{
          (row as User).last_login_at
            ? formatRelative((row as User).last_login_at, locale)
            : '-'
        }}</span>
      </template>

    </DataTable>

    <!-- Create modal -->
    <Modal v-model="showCreate" :title="t('users.newTitle')" size="md">
      <!-- An invitation that could not be e-mailed: its first password, once. -->
      <div v-if="invited" class="space-y-3" data-testid="user-invited">
        <p class="text-sm">{{ t('users.add.notEmailed', { email: invited.email }) }}</p>
        <div class="flex items-center gap-2">
          <code class="flex-1 rounded-md border border-[var(--fe-border)] px-3 py-2 text-sm font-mono select-all" data-testid="user-invited-password">{{ invited.password }}</code>
          <CopyButton :value="invited.password" />
        </div>
        <p class="text-xs text-zinc-500">{{ t('users.add.shownOnce') }}</p>
      </div>
      <!-- ⚠ novalidate: the boxes are marked `required` for the star and for
           assistive tech, but the checking is ours (said in the panel's
           language, inside the dialog). Without it the browser intercepts
           Enter / a submit button with its own bubble, in the BROWSER's
           language, and our check never runs (seen in the RC re-test,
           2026-09-21: an empty New webhook save showed no message of ours). -->
      <!-- Hidden, not removed, while the invited password shows: swapping the
           form out (v-else) broke Vue's unmount of it in the tests' DOM. -->
      <form v-show="!invited" class="space-y-3" novalidate data-testid="user-create-form" @submit.prevent="submitCreate()">
        <p class="text-sm text-zinc-600 dark:text-zinc-400">{{ t('users.add.intro') }}</p>
        <Input
          v-model="newEmail"
          type="email"
          :label="t('common.email')"
          required
          :error="newEmailError || null"
          name="new-user-email"
        />
        <!-- An address an LDAP directory owns: its people arrive by sign-in
             and sync. Said before anything is made; going on is a choice. -->
        <div
          v-if="directoryRefusal"
          class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200 space-y-2"
          role="alert"
          data-testid="user-create-directory"
        >
          <p>{{ directoryRefusal.message }}</p>
          <div class="flex flex-wrap gap-2">
            <RouterLink
              v-if="auth.isAdmin"
              :to="{ name: 'auth-providers.edit', params: { name: directoryRefusal.directory }, query: { section: 'sync' } }"
              class="text-sm underline"
              data-testid="user-create-directory-sync"
            >{{ t('users.add.openDirectory', { directory: directoryRefusal.label }) }}</RouterLink>
            <button type="button" class="text-sm underline" data-testid="user-create-anyway" @click="submitCreate(false, true)">
              {{ t('users.add.createAnyway') }}
            </button>
          </div>
        </div>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <!-- ⚠ Not marked required: the server creates an account without a
               display name (the address stands in for it). -->
          <Input v-model="newName" :label="t('users.fields.displayName')" name="new-user-name" @update:model-value="nameTouched = true" />
          <Input
            v-model="newUsername"
            :label="t('users.add.username')"
            :hint="newUsernameError ? undefined : t('users.add.usernameHint')"
            :error="newUsernameError || null"
            name="new-user-username"
            autocomplete="off"
            @update:model-value="usernameTouched = true"
          />
        </div>
        <div>
          <Select v-model="newRole" :options="createRoleOptions" :label="t('common.role')" />
          <p class="mt-1 text-xs text-zinc-500" data-testid="user-create-role-hint">{{ newRoleHint }}</p>
        </div>

        <fieldset class="space-y-2">
          <legend id="user-create-access-label" class="text-sm font-medium">{{ t('users.add.signIn') }}</legend>
          <ChoiceButtons
            :model-value="newAccess"
            :options="accessOptions"
            aria-labelledby="user-create-access-label"
            testid-prefix="user-create-access"
            @update:model-value="(v: string | string[]) => (newAccess = v as typeof newAccess)"
          />
          <div v-show="newAccess === 'password'" class="flex items-end gap-2">
            <Input
              v-model="newPassword"
              :type="showPassword ? 'text' : 'password'"
              :label="t('common.password')"
              autocomplete="new-password"
              name="new-user-password"
              class="flex-1"
            />
            <Button type="button" variant="ghost" size="sm" :aria-label="showPassword ? t('users.add.hide') : t('users.add.show')" @click="showPassword = !showPassword">
              <EyeOff v-if="showPassword" class="h-4 w-4" /><Eye v-else class="h-4 w-4" />
            </Button>
            <Button type="button" variant="outline" size="sm" data-testid="user-create-generate" @click="generatePassword">
              <Wand2 class="h-4 w-4" /> {{ t('users.add.generate') }}
            </Button>
            <CopyButton v-if="newPassword" :value="newPassword" />
          </div>
          <p v-show="newAccess === 'invite'" class="text-xs text-zinc-500" data-testid="user-create-invite-hint">{{ t('users.add.inviteHint') }}</p>
          <p v-show="newAccess === 'none'" class="text-xs text-zinc-500" data-testid="user-create-none-hint">{{ t('users.add.noneHint') }}</p>
        </fieldset>

        <div v-if="handGroups.length" class="space-y-1">
          <p id="user-create-groups-label" class="text-sm font-medium">{{ t('users.add.groups') }}</p>
          <div class="max-h-28 overflow-y-auto" data-testid="user-create-groups">
            <ChoiceButtons
              multi
              :model-value="newGroups.map(String)"
              :options="groupChoices"
              aria-labelledby="user-create-groups-label"
              testid-prefix="user-create-group"
              @update:model-value="(v: string | string[]) => (newGroups = (v as string[]).map(Number))"
            />
          </div>
        </div>

        <p v-if="createFailure" class="error-text" role="alert" data-testid="user-create-error">{{ createFailure }}</p>
        <!-- ⚠ Enter in a box submits: the form's visible buttons sit in the
             dialog footer, OUTSIDE this <form>, and a form with more than one
             field and no submit button of its own ignores Enter (implicit
             submission needs one). Visually hidden, not display:none — some
             engines skip a display:none default button. RC re-test,
             2026-09-21: Enter in the Add user e-mail box did nothing. -->
        <button type="submit" class="sr-only" tabindex="-1" aria-hidden="true" data-testid="user-create-submit">{{ t('common.create') }}</button>
      </form>
      <template #footer>
        <!-- One button list, each shown or not: swapping two fragments of the
             slot tripped Vue's unmount of the dialog's footer. -->
        <Button v-if="invited" variant="ghost" data-testid="user-invited-another" @click="invited = null">{{ t('users.add.addAnother') }}</Button>
        <Button v-if="invited" @click="(invited = null), (showCreate = false)">{{ t('common.close') }}</Button>
        <Button v-if="!invited" variant="ghost" @click="showCreate = false">{{ t('common.cancel') }}</Button>
        <Button v-if="!invited" variant="outline" :loading="creating" data-testid="user-create-another" @click="submitCreate(true)">{{ t('users.add.createAnother') }}</Button>
        <Button v-if="!invited" :loading="creating" data-testid="user-create" @click="submitCreate()">{{ t('common.create') }}</Button>
      </template>
    </Modal>

    <!-- Delete modal -->
    <Modal
      :model-value="showDelete !== null"
      :title="t('common.delete')"
      size="sm"
      @update:model-value="(v) => (v ? null : (showDelete = null))"
    >
      <p class="text-sm">{{ t('users.deleteConfirm', { email: showDelete?.email }) }}</p>
      <template #footer>
        <Button variant="ghost" @click="showDelete = null">{{ t('common.cancel') }}</Button>
        <Button variant="danger" :loading="deleting" @click="confirmDelete">
          {{ t('common.yesDelete') }}
        </Button>
      </template>
    </Modal>

    <ResetPasswordModal :user="showReset" @close="showReset = null" />
  </div>
</template>
