<script setup lang="ts">
// The one "reset this account's password" dialog. The user list and the user
// detail page each used to carry their own copy, and the copies drifted: the
// list's asked "Delete user …?" above a Confirm button (issue #25), the detail
// page printed the after-text before anything had happened. One dialog, one
// question, one place to read the server's answer.
import { ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';

import { useUsersStore } from '@/stores/users';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import type { User } from '@/api/types';

import Button from '@/components/ui/Button.vue';
import Modal from '@/components/ui/Modal.vue';
import CopyButton from '@/components/ui/CopyButton.vue';

const props = defineProps<{ user: Pick<User, 'id' | 'email'> | null }>();
const emit = defineEmits<{ close: [] }>();

const { t } = useI18n();
const users = useUsersStore();
const toast = useToastStore();

const resetting = ref(false);
const password = ref<string | null>(null);

watch(
  () => props.user?.id,
  () => {
    password.value = null;
  },
);

async function confirm() {
  if (!props.user) return;
  resetting.value = true;
  try {
    password.value = await users.resetPassword(props.user.id);
    toast.success(t('users.resetPasswordOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    resetting.value = false;
  }
}

function close() {
  password.value = null;
  emit('close');
}
</script>

<template>
  <Modal
    :model-value="user !== null"
    :title="t('users.resetPasswordTitle')"
    size="sm"
    :prevent-close="resetting"
    @close="close"
  >
    <p v-if="!password" class="text-sm" data-testid="user-reset-question">
      {{ t('users.resetPasswordConfirm', { email: user?.email }) }}
    </p>
    <div v-else class="space-y-2">
      <p class="text-sm text-zinc-600 dark:text-zinc-400">
        {{ t('users.resetPasswordSubtitle') }}
      </p>
      <div class="flex items-center gap-2">
        <code
          class="flex-1 select-all rounded-md border border-zinc-200 dark:border-zinc-700 bg-zinc-50 dark:bg-zinc-800 p-2 text-sm font-mono break-all"
          data-testid="user-reset-password"
        >
          {{ password }}
        </code>
        <CopyButton :value="password" />
      </div>
    </div>
    <template #footer>
      <template v-if="!password">
        <Button variant="ghost" @click="close">{{ t('common.cancel') }}</Button>
        <Button :loading="resetting" @click="confirm">{{ t('common.confirm') }}</Button>
      </template>
      <Button v-else @click="close">{{ t('common.close') }}</Button>
    </template>
  </Modal>
</template>
