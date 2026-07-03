<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { ArrowLeft, Save, KeyRound, Trash2 } from 'lucide-vue-next';

import { UsersApi } from '@/api/users';
import { useUsersStore } from '@/stores/users';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import type { User, UserRole } from '@/api/types';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Modal from '@/components/ui/Modal.vue';
import CopyButton from '@/components/ui/CopyButton.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t } = useI18n();
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
const role = ref<UserRole>('viewer');
const oidcSubject = ref('');

const showReset = ref(false);
const resetting = ref(false);
const resetPassword = ref<string | null>(null);

const showDelete = ref(false);
const deleting = ref(false);

async function load() {
  loading.value = true;
  try {
    const u = await UsersApi.get(id.value);
    user.value = u;
    email.value = u.email;
    displayName.value = u.display_name;
    role.value = u.role;
    oidcSubject.value = u.oidc_subject ?? '';
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
    const updated = await users.update(id.value, {
      email: email.value.trim(),
      display_name: displayName.value.trim(),
      role: role.value,
      oidc_subject: oidcSubject.value.trim() || null,
    });
    user.value = updated;
    toast.success(t('users.updatedOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    saving.value = false;
  }
}

async function doReset() {
  resetting.value = true;
  try {
    resetPassword.value = await users.resetPassword(id.value);
    toast.success(t('users.resetPasswordOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    resetting.value = false;
  }
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

const roleOptions = [
  { value: 'admin', label: t('users.roles.admin') },
  { value: 'user', label: t('users.roles.user') },
  { value: 'viewer', label: t('users.roles.viewer') },
];

onMounted(load);
</script>

<template>
  <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>
  <div v-else-if="user" class="space-y-5 max-w-2xl">
    <div class="flex items-center justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold flex items-center gap-2">
          {{ user.display_name || user.email }}
          <Badge size="xs">{{ user.role }}</Badge>
        </h1>
        <p class="text-sm text-zinc-500">{{ user.email }}</p>
      </div>
      <Button variant="ghost" size="sm" @click="router.push({ name: 'users' })">
        <ArrowLeft class="h-4 w-4" />
        {{ t('common.back') }}
      </Button>
    </div>

    <form class="card card-body space-y-3" @submit.prevent="save">
      <Input v-model="email" type="email" :label="t('common.email')" required />
      <Input v-model="displayName" :label="t('users.fields.displayName')" required />
      <Select v-model="role" :options="roleOptions" :label="t('common.role')" />
      <Input
        v-model="oidcSubject"
        :label="t('users.fields.oidcSubject')"
        monospace
        :hint="t('common.optional')"
      />

      <div class="flex justify-between items-center pt-2 gap-2">
        <Button type="button" variant="outline" @click="showReset = true">
          <KeyRound class="h-4 w-4" />
          {{ t('users.resetPasswordOk') }}
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

    <!-- Reset password -->
    <Modal
      v-model="showReset"
      :title="t('users.resetPasswordTitle')"
      size="sm"
      :prevent-close="resetting"
      @close="(resetPassword = null)"
    >
      <div v-if="!resetPassword" class="text-sm">
        <p>{{ t('users.resetPasswordSubtitle') }}</p>
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
        <Button v-if="!resetPassword" variant="ghost" @click="showReset = false">
          {{ t('common.cancel') }}
        </Button>
        <Button v-if="!resetPassword" :loading="resetting" @click="doReset">
          {{ t('common.confirm') }}
        </Button>
        <Button v-else @click="(showReset = false), (resetPassword = null)">
          {{ t('common.close') }}
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
