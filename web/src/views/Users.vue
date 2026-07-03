<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { Plus, Trash2, Pencil, KeyRound, RefreshCcw } from 'lucide-vue-next';

import { useUsersStore } from '@/stores/users';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import type { User, UserRole } from '@/api/types';
import { formatRelative } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Modal from '@/components/ui/Modal.vue';
import Table, { type Column } from '@/components/ui/Table.vue';
import CopyButton from '@/components/ui/CopyButton.vue';

const { t, locale } = useI18n();
const router = useRouter();
const users = useUsersStore();
const toast = useToastStore();

const q = ref('');
const role = ref<UserRole | ''>('');
const page = ref(1);
const pageSize = 25;

const showCreate = ref(false);
const showDelete = ref<User | null>(null);
const showReset = ref<User | null>(null);
const resetPassword = ref<string | null>(null);

const newEmail = ref('');
const newName = ref('');
const newRole = ref<UserRole>('viewer');
const newPassword = ref('');
const newOidc = ref('');
const creating = ref(false);
const deleting = ref(false);
const resetting = ref(false);

async function load() {
  await users.fetch({
    q: q.value || undefined,
    role: role.value || undefined,
    page: page.value,
    page_size: pageSize,
  });
}

watch([q, role], () => {
  page.value = 1;
  load();
});

const roleOptions = [
  { value: '', label: t('common.all') },
  { value: 'admin', label: t('users.roles.admin') },
  { value: 'user', label: t('users.roles.user') },
  { value: 'viewer', label: t('users.roles.viewer') },
];

const createRoleOptions = roleOptions.filter((o) => o.value !== '');

const columns = computed<Column<User>[]>(() => [
  { key: 'email', label: t('common.email'), sortable: true },
  { key: 'display_name', label: t('users.fields.displayName') },
  { key: 'role', label: t('common.role'), cell: 'slot' },
  { key: 'last_login_at', label: 'Last login', cell: 'slot' },
  { key: 'actions', label: t('common.actions'), cell: 'slot', align: 'right', width: '180px' },
]);

const roleTone = (r: UserRole) => {
  if (r === 'admin') return 'rose';
  if (r === 'user') return 'amber';
  return 'zinc';
};

async function submitCreate() {
  creating.value = true;
  try {
    await users.create({
      email: newEmail.value.trim(),
      display_name: newName.value.trim(),
      role: newRole.value,
      password: newPassword.value || undefined,
      oidc_subject: newOidc.value.trim() || null,
    });
    toast.success(t('users.createdOk'));
    showCreate.value = false;
    newEmail.value = '';
    newName.value = '';
    newPassword.value = '';
    newOidc.value = '';
    newRole.value = 'viewer';
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
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

async function confirmReset() {
  if (!showReset.value) return;
  resetting.value = true;
  try {
    resetPassword.value = await users.resetPassword(showReset.value.id);
    toast.success(t('users.resetPasswordOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    resetting.value = false;
  }
}

function closeReset() {
  showReset.value = null;
  resetPassword.value = null;
}

onMounted(load);
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
        <Button @click="showCreate = true">
          <Plus class="h-4 w-4" />
          {{ t('users.addNew') }}
        </Button>
      </div>
    </div>

    <Table
      :columns="columns"
      :rows="users.page.items"
      :loading="users.loading"
      :empty="t('common.none')"
      :page="page"
      :page-size="pageSize"
      :total="users.page.total"
      row-key="id"
      @page="(p) => ((page = p), load())"
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
        <Badge :tone="roleTone((row as User).role)" size="xs">
          {{ t(`users.roles.${(row as User).role}`) }}
        </Badge>
      </template>

      <template #cell-last_login_at="{ row }">
        <span class="text-xs text-zinc-500">{{
          (row as User).last_login_at
            ? formatRelative((row as User).last_login_at, locale)
            : '—'
        }}</span>
      </template>

      <template #cell-actions="{ row }">
        <div class="flex items-center justify-end gap-1">
          <Button
            size="xs"
            variant="ghost"
            @click="router.push({ name: 'users.edit', params: { id: (row as User).id } })"
            :title="t('common.edit')"
          >
            <Pencil class="h-3.5 w-3.5" />
          </Button>
          <Button
            size="xs"
            variant="ghost"
            @click="showReset = row as User"
            :title="t('users.resetPasswordOk')"
          >
            <KeyRound class="h-3.5 w-3.5" />
          </Button>
          <Button
            size="xs"
            variant="ghost"
            @click="showDelete = row as User"
            :title="t('common.delete')"
          >
            <Trash2 class="h-3.5 w-3.5 text-rose-500" />
          </Button>
        </div>
      </template>
    </Table>

    <!-- Create modal -->
    <Modal v-model="showCreate" :title="t('users.newTitle')" size="md">
      <form class="space-y-3" @submit.prevent="submitCreate">
        <Input v-model="newEmail" type="email" :label="t('common.email')" required />
        <Input v-model="newName" :label="t('users.fields.displayName')" required />
        <Select v-model="newRole" :options="createRoleOptions" :label="t('common.role')" />
        <Input
          v-model="newPassword"
          type="password"
          :label="t('common.password')"
          autocomplete="new-password"
          :hint="t('common.optional')"
        />
        <Input
          v-model="newOidc"
          :label="t('users.fields.oidcSubject')"
          monospace
          :hint="t('common.optional')"
        />
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

    <!-- Reset password modal -->
    <Modal
      :model-value="showReset !== null"
      :title="t('users.resetPasswordTitle')"
      size="sm"
      :prevent-close="resetting"
      @close="closeReset"
    >
      <div v-if="!resetPassword" class="space-y-3 text-sm">
        <p>{{ t('users.deleteConfirm', { email: showReset?.email }) }}</p>
      </div>
      <div v-else class="space-y-2">
        <p class="text-sm text-zinc-600 dark:text-zinc-400">
          {{ t('users.resetPasswordSubtitle') }}
        </p>
        <div class="flex items-center gap-2">
          <code
            class="flex-1 select-all rounded-md border border-zinc-200 dark:border-zinc-700 bg-zinc-50 dark:bg-zinc-800 p-2 text-sm font-mono break-all"
          >
            {{ resetPassword }}
          </code>
          <CopyButton :value="resetPassword" />
        </div>
      </div>
      <template #footer>
        <Button v-if="!resetPassword" variant="ghost" @click="closeReset">
          {{ t('common.cancel') }}
        </Button>
        <Button v-if="!resetPassword" :loading="resetting" @click="confirmReset">
          {{ t('common.confirm') }}
        </Button>
        <Button v-else @click="closeReset">{{ t('common.close') }}</Button>
      </template>
    </Modal>
  </div>
</template>
