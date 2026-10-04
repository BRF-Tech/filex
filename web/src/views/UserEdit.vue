<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { ArrowLeft, Save, KeyRound, Trash2, Database, RefreshCcw, ShieldCheck, Unlink } from 'lucide-vue-next';

import { UsersApi } from '@/api/users';
import { quotaApi, type QuotaSnapshot } from '@/api/quota';
import { useUsersStore } from '@/stores/users';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import type { User, UserRole } from '@/api/types';
import { formatBytes, formatPercent } from '@/lib/format';
import { personName } from '@brftech/filex-core';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import SourceBadge from '@/components/SourceBadge.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Modal from '@/components/ui/Modal.vue';
import ResetPasswordModal from '@/components/ResetPasswordModal.vue';
import UserRolesCard from '@/components/UserRolesCard.vue';
import UserGroupsCard from '@/components/UserGroupsCard.vue';
import { RolesApi, type GroupRole, type PermissionRule } from '@/api/roles';
import { roleName } from '@/lib/roleName';
import Spinner from '@/components/ui/Spinner.vue';

const { t, locale } = useI18n();
const route = useRoute();
const router = useRouter();
const users = useUsersStore();
const toast = useToastStore();

const id = computed(() => Number(route.params.id));
const user = ref<User | null>(null);
const loading = ref(true);
const saving = ref(false);

const email = ref('');
const displayName = ref('');
// The Role field: a built-in role ("admin" | "user" | "viewer") or a custom
// role as "custom:<id>". One per person.
const role = ref<string>('viewer');
const customRoles = ref<PermissionRule[]>([]);
const heldRoleId = ref<number | null>(null);
const heldRole = computed(() => customRoles.value.find((r) => r.id === heldRoleId.value) ?? null);
/** With no custom role of their own: the one a group gives them. */
const groupRole = ref<GroupRole | null>(null);
const groupRoleRule = computed(() =>
  groupRole.value ? (customRoles.value.find((r) => r.id === groupRole.value?.role_id) ?? null) : null,
);
/** The custom role in force — their own, else their group's — for the card. */
const roleInForce = computed(() => {
  // The role itself (its names in other languages come with it), and the
  // group it comes from when it is not their own.
  if (heldRole.value) return { ...heldRole.value, group: null as string | null };
  if (groupRoleRule.value && groupRole.value) return { ...groupRoleRule.value, group: groupRole.value.group_name };
  return null;
});
const CUSTOM = 'custom:';
/** The choice as saved, to tell whether the Role field changed. */
const savedRole = ref('');
function currentChoice(): string {
  const u = user.value;
  if (!u) return '';
  return heldRoleId.value != null && u.role !== 'admin' ? `${CUSTOM}${heldRoleId.value}` : u.role;
}

const showReset = ref(false);

const showDelete = ref(false);
const deleting = ref(false);

// The account's switch and its SSO bind (docs/SSO.md). An account an SSO
// sign-in opened switched off waits for approval: switching it on IS the
// approval. Both answer {ok}, so the account is read again.
const pending = computed(() => user.value?.enabled === false && user.value?.disabled_reason === 'pending_approval');
const enabling = ref(false);
async function enableAccount() {
  enabling.value = true;
  try {
    await UsersApi.update(id.value, { enabled: true });
    user.value = await UsersApi.get(id.value);
    toast.success(t('users.account.enabledOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    enabling.value = false;
  }
}
const showUnlink = ref(false);
const unlinking = ref(false);
async function unlinkSSO() {
  unlinking.value = true;
  try {
    await UsersApi.update(id.value, { sso_unlink: true });
    user.value = await UsersApi.get(id.value);
    toast.success(t('users.sso.unlinkedOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    unlinking.value = false;
    showUnlink.value = false;
  }
}

async function load() {
  loading.value = true;
  try {
    const [u, list, held] = await Promise.all([
      UsersApi.get(id.value),
      // Best effort: without the roles list the field offers the built-in
      // roles only.
      RolesApi.listRules().catch(() => ({ rules: [] as PermissionRule[], assignments: {} })),
      RolesApi.userRoleDetail(id.value).catch(() => ({ role_id: null, group_role: null })),
    ]);
    user.value = u;
    email.value = u.email;
    displayName.value = u.display_name;
    customRoles.value = list.rules;
    heldRoleId.value = held.role_id;
    groupRole.value = held.group_role;
    role.value = currentChoice();
    savedRole.value = role.value;
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
    router.replace({ name: 'users' });
  } finally {
    loading.value = false;
  }
}

async function save() {
  saving.value = true;
  try {
    const picked = role.value;
    if (displayName.value.trim() !== user.value?.display_name) {
      await users.update(id.value, { display_name: displayName.value.trim() });
    }
    // The role is one server call (it also guards the last administrator),
    // and only made when the choice changed — no audit entry for a no-op.
    let stillFromGroup: GroupRole | null = null;
    if (picked !== savedRole.value) {
      // ⚠ A built-in role does not replace the role a group gives them (only
      // a custom role of their own does) — say so before, not after.
      const g = groupRole.value;
      if (g && !picked.startsWith(CUSTOM) && picked !== 'admin') {
        const ok = confirm(
          t('users.groupRoleConfirm', { name: personName(user.value!), role: groupRoleRule.value ? roleName(groupRoleRule.value, locale.value) : `#${g.role_id}`, group: g.group_name }),
        );
        if (!ok) return;
      }
      const res = await RolesApi.setUserRole(
        id.value,
        picked.startsWith(CUSTOM) ? Number(picked.slice(CUSTOM.length)) : (picked as UserRole),
      );
      heldRoleId.value = res.role_id;
      stillFromGroup = res.group_role ?? null;
    }
    await refreshRole();
    savedRole.value = currentChoice();
    if (stillFromGroup) {
      const rule = customRoles.value.find((r) => r.id === stillFromGroup?.role_id);
      const name = rule ? roleName(rule, locale.value) : `#${stillFromGroup.role_id}`;
      toast.warn(t('users.groupRoleStill', { role: name, group: stillFromGroup.group_name }));
    } else {
      toast.success(t('users.updatedOk'));
    }
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    saving.value = false;
  }
}

/** Re-reads the account and the role in force — after its role or its
 *  groups changed, either of which can move the other. */
async function refreshRole() {
  const [u, held] = await Promise.all([
    UsersApi.get(id.value),
    RolesApi.userRoleDetail(id.value).catch(() => ({ role_id: heldRoleId.value, group_role: null })),
  ]);
  user.value = u;
  heldRoleId.value = held.role_id;
  groupRole.value = held.group_role;
  role.value = currentChoice();
  savedRole.value = role.value;
}

async function confirmDelete() {
  deleting.value = true;
  try {
    await users.remove(id.value);
    toast.success(t('users.deletedOk'));
    router.replace({ name: 'users' });
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    deleting.value = false;
    showDelete.value = false;
  }
}

// ⚠ computed, not a plain array: a label built once at setup keeps the
// language the page was opened in when the language changes.
const roleOptions = computed(() => [
  { value: 'admin', label: t('users.roles.admin') },
  { value: 'user', label: t('users.roles.user') },
  { value: 'viewer', label: t('users.roles.viewer') },
  ...customRoles.value.map((r) => ({ value: `${CUSTOM}${r.id}`, label: roleName(r, locale.value) })),
]);

// ── koru:k3 — storage quota card ─────────────────────────────────
// GB inputs use the same 1000-base as formatBytes so "10 GB" here
// matches the "10 GB" the widget renders.
const GB = 1_000_000_000;

const quotaSnap = ref<QuotaSnapshot | null>(null);
const quotaGb = ref<number>(0);
const quotaErr = ref<string | null>(null);
const quotaSaving = ref(false);
const quotaRecomputing = ref(false);
// False when the backend has no admin quota read endpoint (older builds):
// the card still lets the admin set a new limit; usage shows after save.
const quotaReadable = ref(true);

async function loadQuota() {
  try {
    applySnap(await quotaApi.adminGet(id.value));
    quotaReadable.value = true;
  } catch {
    quotaReadable.value = false;
  }
}

function applySnap(snap: QuotaSnapshot) {
  quotaSnap.value = snap;
  quotaGb.value = snap.quota_bytes > 0 ? Math.round((snap.quota_bytes / GB) * 100) / 100 : 0;
}

const quotaBarClass = computed(() => {
  const p = quotaSnap.value?.percent_used ?? 0;
  if (p >= 90) return 'bg-rose-500';
  if (p >= 75) return 'bg-amber-500';
  return 'bg-emerald-500';
});

async function saveQuota() {
  quotaErr.value = null;
  const gb = quotaGb.value;
  if (typeof gb !== 'number' || !Number.isFinite(gb) || gb < 0) {
    quotaErr.value = t('users.quota.errMin');
    return;
  }
  quotaSaving.value = true;
  try {
    applySnap(await quotaApi.adminSet(id.value, Math.round(gb * GB)));
    quotaReadable.value = true;
    toast.success(t('users.quota.savedOk'));
  } catch (e: unknown) {
    quotaErr.value = extractError(e, t('errors.generic'));
  } finally {
    quotaSaving.value = false;
  }
}

async function recomputeQuota() {
  quotaRecomputing.value = true;
  try {
    const used = await quotaApi.adminRecompute(id.value);
    if (quotaSnap.value) {
      const limit = quotaSnap.value.quota_bytes;
      quotaSnap.value = {
        ...quotaSnap.value,
        used_bytes: used,
        percent_used: limit > 0 ? (used / limit) * 100 : 0,
      };
    }
    toast.success(`${t('users.quota.recomputeOk')} - ${formatBytes(used, locale.value)}`);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    quotaRecomputing.value = false;
  }
}

onMounted(() => {
  load();
  loadQuota();
});
</script>

<template>
  <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>
  <div v-else-if="user" class="space-y-5 max-w-2xl">
    <div class="flex items-center justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold flex items-center gap-2">
          {{ personName(user) }}
          <!-- ⚠ The role in words (it printed "user" under the name). -->
          <SourceBadge :source="user.auth_source" data-testid="user-edit-source" />
          <Badge size="xs" data-testid="user-edit-role">{{ roleInForce && user.role !== 'admin' ? roleName(roleInForce, locale) : t(`users.roles.${user.role}`) }}</Badge>
        </h1>
        <p class="text-sm text-zinc-500">{{ user.email }}</p>
      </div>
      <Button variant="ghost" size="sm" @click="router.push({ name: 'users' })">
        <ArrowLeft class="h-4 w-4" />
        {{ t('common.back') }}
      </Button>
    </div>

    <form class="card card-body space-y-3" @submit.prevent="save">
      <!-- The server does not change an account's e-mail (it is the identity an
           SSO sign-in is matched on), so the field is shown, not offered. -->
      <Input v-model="email" type="email" :label="t('common.email')" readonly disabled />
      <Input v-model="displayName" :label="t('users.fields.displayName')" required />
      <Select v-model="role" :options="roleOptions" :label="t('common.role')" :disabled="user.admin_by_group" />
      <p v-if="user.admin_by_group" class="text-xs text-zinc-600 dark:text-zinc-300" data-testid="user-admin-by-group">
        {{ t('users.adminByGroup') }}
      </p>
      <p v-if="user.role !== 'admin' && !heldRole && roleInForce?.group" class="text-xs text-zinc-600 dark:text-zinc-300" data-testid="user-group-role">
        {{ t('groups.userCard.roleFromGroup', { role: roleName(roleInForce, locale), group: roleInForce.group }) }}
      </p>

      <div class="flex justify-between items-center pt-2 gap-2">
        <Button type="button" variant="outline" @click="showReset = true">
          <KeyRound class="h-4 w-4" />
          {{ t('users.resetPassword') }}
        </Button>
        <div class="flex items-center gap-2">
          <Button type="button" variant="danger" @click="showDelete = true">
            <Trash2 class="h-4 w-4" />
            {{ t('common.delete') }}
          </Button>
          <Button type="submit" :loading="saving">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </div>
    </form>

    <div
      v-if="user.enabled === false"
      class="card card-body space-y-3"
      :class="pending ? 'border-amber-300 dark:border-amber-700' : ''"
      data-testid="user-account-off"
    >
      <p class="text-sm">{{ pending ? t('users.account.pendingAbout') : user.disabled_reason === 'directory' ? t('users.account.directoryAbout') : t('users.account.disabledAbout') }}</p>
      <div class="flex justify-end">
        <Button type="button" :loading="enabling" data-testid="user-account-enable" @click="enableAccount">
          <ShieldCheck class="h-4 w-4" />
          {{ pending ? t('users.account.approve') : t('users.account.enable') }}
        </Button>
      </div>
    </div>

    <div v-if="user.sso_linked" class="card card-body space-y-3" data-testid="user-sso">
      <h2 class="text-base font-semibold">{{ t('users.sso.title') }}</h2>
      <p class="text-sm text-zinc-600 dark:text-zinc-300">{{ t('users.sso.linked') }}</p>
      <div class="flex justify-end">
        <Button type="button" variant="outline" data-testid="user-sso-unlink" @click="showUnlink = true">
          <Unlink class="h-4 w-4" />
          {{ t('users.sso.unlink') }}
        </Button>
      </div>
    </div>

    <!-- Per-user permissions (backend internal/perm). Keyed on the SAVED
         role, not the form's: the card describes what the server holds. -->
    <UserRolesCard :user-id="user.id" :role="user.role" :custom-role="user.role !== 'admin' ? roleInForce : null" />

    <!-- Groups (backend internal/group): folder access and a role for
         everyone in them. Changing them can move the role in force. -->
    <UserGroupsCard :user-id="user.id" :user-name="personName(user)" @changed="refreshRole" />

    <!-- koru:k3 — storage quota -->
    <div class="card card-body space-y-3">
      <div class="flex items-center justify-between gap-2">
        <h2 class="flex items-center gap-2 text-base font-semibold">
          <Database class="h-4 w-4" /> {{ t('users.quota.title') }}
        </h2>
        <Badge v-if="quotaSnap?.unlimited" tone="sky">{{ t('quota.unlimited') }}</Badge>
      </div>

      <template v-if="quotaSnap">
        <div
          v-if="!quotaSnap.unlimited"
          class="relative h-2 w-full overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-700"
          aria-hidden="true"
        >
          <span
            class="absolute inset-y-0 start-0 transition-all duration-300"
            :class="quotaBarClass"
            :style="{ width: `${Math.min(100, quotaSnap.percent_used)}%` }"
          />
        </div>
        <p class="text-sm text-zinc-600 dark:text-zinc-300 tabular-nums">
          <template v-if="quotaSnap.unlimited">
            {{ t('quota.usedIs', { used: formatBytes(quotaSnap.used_bytes, locale) }) }}
          </template>
          <template v-else>
            {{
              t('quota.usedOf', {
                used: formatBytes(quotaSnap.used_bytes, locale),
                quota: formatBytes(quotaSnap.quota_bytes, locale),
                percent: formatPercent(quotaSnap.percent_used, locale),
              })
            }}
          </template>
        </p>
      </template>
      <p v-else-if="!quotaReadable" class="text-xs text-zinc-500 dark:text-zinc-400">
        {{ t('users.quota.noRead') }}
      </p>

      <div class="flex items-end gap-2 flex-wrap">
        <Input
          :model-value="quotaGb"
          type="number"
          :min="0"
          :step="0.5"
          :label="t('users.quota.limitLabel')"
          :hint="t('users.quota.limitHint')"
          :error="quotaErr"
          class="w-48"
          @update:model-value="(v) => ((quotaGb = v as number), (quotaErr = null))"
        />
        <Button type="button" :loading="quotaSaving" @click="saveQuota">
          <Save class="h-4 w-4" />
          {{ t('common.save') }}
        </Button>
        <Button type="button" variant="outline" :loading="quotaRecomputing" @click="recomputeQuota">
          <RefreshCcw class="h-4 w-4" />
          {{ t('users.quota.recompute') }}
        </Button>
      </div>
    </div>

    <ResetPasswordModal :user="showReset ? user : null" @close="showReset = false" />

    <Modal v-model="showUnlink" :title="t('users.sso.unlink')" size="sm">
      <p class="text-sm">{{ t('users.sso.unlinkConfirm', { email: user.email }) }}</p>
      <template #footer>
        <Button variant="ghost" @click="showUnlink = false">{{ t('common.cancel') }}</Button>
        <Button :loading="unlinking" data-testid="user-sso-unlink-confirm" @click="unlinkSSO">
          {{ t('users.sso.unlink') }}
        </Button>
      </template>
    </Modal>

    <Modal v-model="showDelete" :title="t('common.delete')" size="sm">
      <p class="text-sm">{{ t('users.deleteConfirm', { email: user.email }) }}</p>
      <template #footer>
        <Button variant="ghost" @click="showDelete = false">{{ t('common.cancel') }}</Button>
        <Button variant="danger" :loading="deleting" @click="confirmDelete">
          {{ t('common.yesDelete') }}
        </Button>
      </template>
    </Modal>
  </div>
</template>
