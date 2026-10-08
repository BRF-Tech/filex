<script setup lang="ts">
/**
 * #191 - the instance's Web Push key (backend internal/notify push.go,
 * GET /api/admin/notifications/push, POST …/push/rotate): whether push works on
 * this server, why not when it does not, how many devices receive it, and the
 * rotation.
 *
 * ⚠ The instance's operator's alone (the platform's administrators on a
 * multi-tenant install - the handlers' requireSupertenant), so it is drawn
 * only for `caller_admin`. A person turns push on for a device in their own
 * notification settings, not here.
 *
 * ⚠ A rotation forgets every device: a subscription is bound to the key it
 * was made with. Each device subscribes again the next time filex opens on it
 * where its person turned push on (core lib/webPush reconcile).
 */
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { KeyRound, Smartphone } from 'lucide-vue-next';

import { NotificationsApi, type PushAdminInfo } from '@/api/notifications';
import { extractError } from '@/api/client';
import { formatDate } from '@/lib/format';
import { useToastStore } from '@/stores/toast';
import Button from '@/components/ui/Button.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t, locale } = useI18n();
const toast = useToastStore();

const info = ref<PushAdminInfo | null>(null);
const loading = ref(true);
const rotating = ref(false);
const error = ref('');

/** Why push is off, said for the operator, who can do something about it. */
const offReason = computed(() => {
  const p = info.value?.push;
  if (!p || p.available) return '';
  switch (p.reason) {
    case 'disabled':
      return t('notifications.push.offDisabled');
    case 'no_secret_key':
      return t('notifications.push.offNoSecretKey');
    case 'key_unreadable':
      return t('notifications.push.offKeyUnreadable');
    default:
      return t('notifications.push.offError');
  }
});

/** A key that does not open can only be replaced; one that is off by a switch cannot be. */
const canRotate = computed(() => {
  const p = info.value?.push;
  return !!p && (p.available || p.reason === 'key_unreadable');
});

async function load() {
  loading.value = true;
  error.value = '';
  try {
    info.value = await NotificationsApi.getPushAdmin();
  } catch (e: unknown) {
    error.value = extractError(e, t('errors.loadFailed'));
  } finally {
    loading.value = false;
  }
}

async function rotate() {
  if (rotating.value || !window.confirm(t('notifications.push.rotateConfirm'))) return;
  rotating.value = true;
  error.value = '';
  try {
    const res = await NotificationsApi.rotatePush();
    toast.success(t('notifications.push.rotated', { count: res.devices_forgotten }));
    await load();
  } catch (e: unknown) {
    error.value = extractError(e, t('errors.generic'));
  } finally {
    rotating.value = false;
  }
}

onMounted(load);
</script>

<template>
  <section
    class="space-y-3 rounded-xl border border-zinc-200 bg-white p-4 text-sm shadow-sm dark:border-zinc-800 dark:bg-zinc-900"
    data-testid="notif-push"
  >
    <header class="flex items-center gap-2">
      <Smartphone class="h-5 w-5 text-zinc-500" />
      <h2 class="text-base font-semibold">{{ t('notifications.push.title') }}</h2>
    </header>
    <p class="text-zinc-600 dark:text-zinc-400">{{ t('notifications.push.intro') }}</p>

    <div v-if="loading" class="flex justify-center py-4"><Spinner /></div>
    <template v-else-if="info">
      <p v-if="info.push.available" class="text-zinc-700 dark:text-zinc-300" data-testid="notif-push-on">
        {{ t('notifications.push.on', { count: info.devices }) }}
      </p>
      <p v-else class="text-amber-700 dark:text-amber-400" data-testid="notif-push-off">{{ offReason }}</p>
      <p v-if="info.push.key_created_at" class="text-xs text-zinc-500" data-testid="notif-push-key-date">
        {{ t('notifications.push.keyCreated', { date: formatDate(info.push.key_created_at, String(locale)) }) }}
      </p>
      <div class="flex flex-wrap justify-end gap-2">
        <Button
          variant="outline"
          size="sm"
          :loading="rotating"
          :disabled="!canRotate"
          data-testid="notif-push-rotate"
          @click="rotate"
        >
          <KeyRound class="h-4 w-4" />
          {{ t('notifications.push.rotate') }}
        </Button>
      </div>
    </template>
    <p v-if="error" class="text-rose-600 dark:text-rose-400" data-testid="notif-push-error">{{ error }}</p>
  </section>
</template>
