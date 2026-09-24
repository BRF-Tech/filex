<script setup lang="ts">
/**
 * The default (global) webhook — `FILEX_WEBHOOK_URL` + optional bearer token,
 * the one destination that receives every event.
 *
 * ⚠⚠ ONE place to set where events go. This card used to live on the
 * notifications page while the Webhooks page managed the signed targets, so
 * an administrator had two screens, two "webhook" settings and no way to tell
 * which one was in force (release-candidate sweep, 2026-09-21: "iki webhook
 * ayarı var"). It moved here, beside the targets, and the notifications page
 * points to it.
 */
import { onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Webhook } from 'lucide-vue-next';

import { useNotificationsStore } from '@/stores/notifications';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Badge from '@/components/ui/Badge.vue';

const { t } = useI18n();
const notif = useNotificationsStore();
const toast = useToastStore();

const editing = ref(false);
const url = ref('');
const token = ref('');

onMounted(() => void notif.fetchWebhook());

function edit() {
  url.value = notif.webhook?.url ?? '';
  token.value = '';
  editing.value = true;
}

async function save() {
  try {
    await notif.updateWebhook(url.value.trim(), token.value);
    toast.success(t('notifications.webhookSaved'));
    editing.value = false;
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.saveFailed')));
  }
}
</script>

<template>
  <div
    class="rounded-xl border border-zinc-200 bg-white p-4 shadow-sm dark:border-zinc-800 dark:bg-zinc-900"
    data-testid="global-webhook-card"
  >
    <div class="flex items-center justify-between">
      <div class="flex items-center gap-2">
        <Webhook class="h-5 w-5 text-zinc-500" />
        <h2 class="text-sm font-semibold">{{ t('webhooks.global.title') }}</h2>
      </div>
      <Button v-if="!editing" size="xs" variant="outline" @click="edit">{{ t('common.edit') }}</Button>
    </div>
    <p class="mt-1 text-xs text-zinc-500">{{ t('webhooks.global.hint') }}</p>

    <div v-if="!editing" class="mt-3 grid gap-2 text-sm sm:grid-cols-2">
      <div>
        <span class="text-xs uppercase tracking-wide text-zinc-500">{{ t('notifications.webhookUrl') }}</span>
        <div class="mt-0.5 break-all font-mono text-xs">{{ notif.webhook?.url || t('notifications.notConfigured') }}</div>
      </div>
      <div>
        <span class="text-xs uppercase tracking-wide text-zinc-500">{{ t('notifications.webhookToken') }}</span>
        <div class="mt-0.5 font-mono text-xs">
          <Badge v-if="notif.webhook?.token_set" tone="emerald">{{ t('notifications.tokenSet') }}</Badge>
          <Badge v-else tone="zinc">{{ t('notifications.tokenUnset') }}</Badge>
        </div>
      </div>
    </div>

    <form v-else class="mt-3 space-y-3" @submit.prevent="save">
      <Input v-model="url" :label="t('notifications.webhookUrl')" placeholder="https://example.com/webhook" />
      <Input v-model="token" :label="t('notifications.webhookToken')" type="password" :placeholder="t('notifications.tokenPlaceholder')" />
      <p class="text-xs text-zinc-500">{{ t('notifications.tokenHint') }}</p>
      <div class="flex justify-end gap-2">
        <Button type="button" size="sm" variant="ghost" @click="editing = false">{{ t('common.cancel') }}</Button>
        <Button type="submit" size="sm" variant="primary">{{ t('common.save') }}</Button>
      </div>
    </form>
  </div>
</template>
