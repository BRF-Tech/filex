<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { Plus, Trash2, Pencil, KeyRound, RefreshCcw } from 'lucide-vue-next';

import { useUsersStore } from '@/stores/users';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import type { User, UserRole } from '@/api/types';
import { emailProblem, refusalField } from '@brftech/filex-core';
import { formatRelative } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import { RolesApi, type PermissionRule } from '@/api/roles';
import { roleName } from '@/lib/roleName';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Modal from '@/components/ui/Modal.vue';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';
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
watch(newEmail, () => {
  createRefusal.value = null;
  createFailure.value = '';
});
const newEmailError = computed(() => {
  if (createRefusal.value?.field === 'email') return createRefusal.value.message;
  // Said while typing once something is typed, and for an empty box only
  // after a submit — an error on a box nobody has reached yet is noise.
  if (!createTried.value && !newEmail.value.trim()) return '';
  const p = emailProblem(newEmail.value);
  return p ? t(`account.errors.${p.key}`, p.params ?? {}) : '';
});

function openCreate() {
  newEmail.value = '';
  newName.value = '';
  newPassword.value = '';
  newRole.value = 'viewer';
  createTried.value = false;
  createRefusal.value = null;
  createFailure.value = '';
  showCreate.value = true;
}

async function load() {
  await Promise.all([
    users.fetch({
      q: q.value || undefined,
      role: undefined,
      page: page.value,
      page_size: pageSize,
    }),
    loadCustom(),
  ]);
}

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
async function loadCustom() {
  const [all, roles] = await Promise.all([
    RolesApi.allOverrides().catch(() => ({})),
    RolesApi.listRules().catch(() => ({ rules: [], assignments: {} as Record<string, number> })),
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
  roleIdOf.value = ids;
  customRules.value = roles.rules;
}

// Search and the role filter narrow the rows already here (visibleRows):
// the server answers every account and reads neither, so asking it again
// on each keystroke only fetched the same list.
watch([q, role], () => {
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
  const needle = q.value.trim().toLocaleLowerCase();
  if (!f && !needle) return users.page.items;
  return users.page.items.filter((u) => {
    if (needle && ![u.email, u.display_name, u.username].some((x) => (x ?? '').toLocaleLowerCase().includes(needle))) {
      return false;
    }
    if (!f) return true;
    const held = u.role !== 'admin' ? roleIdOf.value.get(u.id) : undefined;
    if (f.startsWith('custom:')) return held === Number(f.slice(7));
    return u.role === f && held === undefined;
  });
});

/* The explorer's table (DataTable): every column resizes, hides, moves and
 * sorts, and the arrangement is remembered on the account under
 * `admin.users`. ⚠ The list is paged by the SERVER, which has no sort
 * parameter — so while it spans more than one page the table closes its
 * headers and says why, rather than re-ordering 25 rows of 300 and calling
 * that sorted. (It used to draw an arrow on Email and move nothing at all.) */
const ROLE_RANK: Record<UserRole, number> = { admin: 0, user: 1, viewer: 2 };
const columns = computed<DataColumn<User>[]>(() => [
  { id: 'email', label: t('common.email'), sortable: true, width: 240 },
  { id: 'display_name', label: t('users.fields.displayName'), sortable: true, width: 180 },
  {
    id: 'role',
    label: t('common.role'),
    sortable: true,
    width: 110,
    sortValue: (u) => ROLE_RANK[u.role] ?? 9,
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

const roleTone = (r: UserRole) => {
  if (r === 'admin') return 'rose';
  if (r === 'user') return 'amber';
  return 'zinc';
};

async function submitCreate() {
  createTried.value = true;
  createFailure.value = '';
  if (newEmailError.value || creating.value) return;
  creating.value = true;
  const picked = newRole.value;
  const customId = picked.startsWith('custom:') ? Number(picked.slice(7)) : null;
  try {
    // A custom role: the account starts as a Viewer — the least it can be —
    // and the role call then sets the level the role needs. If that call
    // fails, the person is a Viewer, never more.
    const created = await users.create({
      email: newEmail.value.trim(),
      display_name: newName.value.trim(),
      role: customId ? 'viewer' : (picked as UserRole),
      password: newPassword.value || undefined,
    });
    if (customId) {
      try {
        await RolesApi.setUserRole(created.id, customId);
      } catch (e: unknown) {
        toast.error(t('users.createdRoleNotSet', { error: extractError(e, t('errors.generic')) }));
      }
      await Promise.all([load(), loadCustom()]);
    }
    toast.success(t('users.createdOk'));
    showCreate.value = false;
    newEmail.value = '';
    newName.value = '';
    newPassword.value = '';
    newRole.value = 'viewer';
  } catch (e: unknown) {
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
      :rows="visibleRows"
      :loading="users.loading"
      :empty="t('common.none')"
      :page="page"
      :page-size="pageSize"
      :total="role || q.trim() ? visibleRows.length : users.page.total"
      row-key="id"
      :row-actions="(row: User) => rowActions(row)"
      :row-actions-test-id="(row: User) => `user-actions-${row.id}`"
      @row-action="(key: string, row: User) => onRowAction(key, row)"
      @page="(p: number) => ((page = p), load())"
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
      </template>

      <template #cell-role="{ row }">
        <span class="inline-flex flex-wrap items-center gap-1">
        <Badge v-if="(row as User).role !== 'admin' && roleOf.has((row as User).id)" tone="brand" size="xs">
          {{ roleOf.get((row as User).id) }}
        </Badge>
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
        </span>
      </template>

      <template #cell-last_login_at="{ row }">
        <span class="text-xs text-zinc-500">{{
          (row as User).last_login_at
            ? formatRelative((row as User).last_login_at, locale)
            : '—'
        }}</span>
      </template>

    </DataTable>

    <!-- Create modal -->
    <Modal v-model="showCreate" :title="t('users.newTitle')" size="md">
      <!-- ⚠ novalidate: the boxes are marked `required` for the star and for
           assistive tech, but the checking is ours (said in the panel's
           language, inside the dialog). Without it the browser intercepts
           Enter / a submit button with its own bubble, in the BROWSER's
           language, and our check never runs (seen in the RC re-test,
           2026-09-21: an empty New webhook save showed no message of ours). -->
      <form class="space-y-3" novalidate @submit.prevent="submitCreate">
        <Input
          v-model="newEmail"
          type="email"
          :label="t('common.email')"
          required
          :error="newEmailError || null"
          name="new-user-email"
        />
        <!-- ⚠ Not marked required: the server creates an account without a
             display name (the address stands in for it), so a star here was a
             rule the form did not keep. -->
        <Input v-model="newName" :label="t('users.fields.displayName')" name="new-user-name" />
        <Select v-model="newRole" :options="createRoleOptions" :label="t('common.role')" />
        <Input
          v-model="newPassword"
          type="password"
          :label="t('common.password')"
          autocomplete="new-password"
          :hint="t('users.passwordOptionalHint')"
        />
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
        <Button variant="ghost" @click="showCreate = false">{{ t('common.cancel') }}</Button>
        <Button :loading="creating" @click="submitCreate">{{ t('common.create') }}</Button>
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
