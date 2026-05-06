<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Bell, RefreshCcw, Send, Webhook } from 'lucide-vue-next';

import { useNotificationsStore } from '@/stores/notifications';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import { formatDate } from '@/lib/format';
import type { Severity } from '@/api/types';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Badge from '@/components/ui/Badge.vue';

const { t, locale } = useI18n();
const notif = useNotificationsStore();
const toast = useToastStore();

const refreshing = ref(false);
const showWebhookForm = ref(false);
const webhookUrl = ref('');
const webhookToken = ref('');

async function load() {
  refreshing.value = true;
  try {
    await Promise.all([notif.fetchAdminList(), notif.fetchUnread(), notif.fetchWebhook()]);
  } finally {
    refreshing.value = false;
  }
}

onMounted(load);

function severityTone(s: Severity): 'sky' | 'amber' | 'rose' {
  if (s === 'critical' || s === 'error') return 'rose';
  if (s === 'warning') return 'amber';
  return 'sky';
}

async function sendTest() {
  try {
    const r = await notif.sendTest();
    toast.success(t('notifications.testSent', { id: r.id }));
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, 'Test send failed'));
  }
}

function openWebhookForm() {
  webhookUrl.value = notif.webhook?.url ?? '';
  webhookToken.value = '';
  showWebhookForm.value = true;
}

async function saveWebhook() {
  try {
    await notif.updateWebhook(webhookUrl.value.trim(), webhookToken.value);
    toast.success(t('notifications.webhookSaved'));
    showWebhookForm.value = false;
  } catch (e: unknown) {
    toast.error(extractError(e, 'Save failed'));
  }
}

function setUnread(v: boolean) {
  notif.setUnreadFilter(v);
  notif.fetchAdminList();
}

const tableRows = computed(() => notif.items);

function gotoPage(p: number) {
  notif.setPage(p);
  notif.fetchAdminList();
}

function pageCount(): number {
  if (notif.limit === 0) return 1;
  return Math.max(1, Math.ceil(notif.total / notif.limit));
}

function currentPage(): number {
  if (notif.limit === 0) return 1;
  return Math.floor(notif.offset / notif.limit) + 1;
}
</script>

<template>
  <section class="space-y-4">
    <header class="flex items-center justify-between">
      <div class="flex items-center gap-2">
        <Bell class="h-6 w-6 text-brand-600 dark:text-brand-400" />
        <h1 class="text-xl font-semibold">{{ t('notifications.adminTitle') }}</h1>
      </div>
      <div class="flex items-center gap-2">
        <Button variant="outline" size="sm" @click="load" :loading="refreshing">
          <RefreshCcw class="h-4 w-4" />
          {{ t('common.refresh') }}
        </Button>
        <Button variant="outline" size="sm" @click="sendTest">
          <Send class="h-4 w-4" />
          {{ t('notifications.sendTest') }}
        </Button>
      </div>
    </header>

    <!-- Webhook config card -->
    <div class="rounded-xl border border-zinc-200 bg-white p-4 shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-2">
          <Webhook class="h-5 w-5 text-zinc-500" />
          <h2 class="text-sm font-semibold">{{ t('notifications.webhookConfig') }}</h2>
        </div>
        <Button v-if="!showWebhookForm" size="xs" variant="outline" @click="openWebhookForm">{{ t('common.edit') }}</Button>
      </div>

      <div v-if="!showWebhookForm" class="mt-3 grid gap-2 text-sm sm:grid-cols-2">
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

      <form v-else class="mt-3 space-y-3" @submit.prevent="saveWebhook">
        <Input v-model="webhookUrl" :label="t('notifications.webhookUrl')" placeholder="https://example.com/webhook" />
        <Input v-model="webhookToken" :label="t('notifications.webhookToken')" type="password" :placeholder="t('notifications.tokenPlaceholder')" />
        <p class="text-xs text-zinc-500">{{ t('notifications.tokenHint') }}</p>
        <div class="flex justify-end gap-2">
          <Button type="button" size="sm" variant="ghost" @click="showWebhookForm = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" size="sm" variant="primary">{{ t('common.save') }}</Button>
        </div>
      </form>
    </div>

    <!-- Filter -->
    <div class="flex items-center gap-3">
      <Toggle :model-value="notif.onlyUnread" :label="t('notifications.unreadOnly')" @update:model-value="setUnread" />
      <span class="text-xs text-zinc-500">{{ t('notifications.unreadCount', { n: notif.unreadCount }) }}</span>
    </div>

    <!-- Table -->
    <div class="overflow-x-auto rounded-xl border border-zinc-200 dark:border-zinc-800">
      <table class="w-full text-sm">
        <thead class="bg-zinc-50 text-xs uppercase text-zinc-500 dark:bg-zinc-900 dark:text-zinc-400">
          <tr>
            <th class="px-3 py-2 text-left">{{ t('notifications.fields.event') }}</th>
            <th class="px-3 py-2 text-left">{{ t('notifications.fields.severity') }}</th>
            <th class="px-3 py-2 text-left">{{ t('notifications.fields.title') }}</th>
            <th class="px-3 py-2 text-left">{{ t('notifications.fields.body') }}</th>
            <th class="px-3 py-2 text-left">{{ t('notifications.fields.scope') }}</th>
            <th class="px-3 py-2 text-left">{{ t('notifications.fields.webhook') }}</th>
            <th class="px-3 py-2 text-left">{{ t('notifications.fields.createdAt') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-zinc-200 dark:divide-zinc-800">
          <tr v-for="n in tableRows" :key="n.id" class="bg-white dark:bg-zinc-950" :class="{ 'opacity-70': n.read_at }">
            <td class="px-3 py-2 font-mono text-xs">{{ n.event }}</td>
            <td class="px-3 py-2"><Badge :tone="severityTone(n.severity)">{{ n.severity }}</Badge></td>
            <td class="px-3 py-2">{{ n.title }}</td>
            <td class="px-3 py-2 max-w-md truncate text-xs text-zinc-600 dark:text-zinc-400">{{ n.body }}</td>
            <td class="px-3 py-2 text-xs">
              <span v-if="n.user_id">user #{{ n.user_id }}</span>
              <span v-else class="text-zinc-500">broadcast</span>
            </td>
            <td class="px-3 py-2 text-xs">
              <Badge :tone="n.webhook_status === 'sent' ? 'emerald' : n.webhook_status === 'failed' ? 'rose' : 'zinc'">{{ n.webhook_status }}</Badge>
              <span v-if="n.webhook_error" class="ml-1 text-rose-500">— {{ n.webhook_error }}</span>
            </td>
            <td class="px-3 py-2 whitespace-nowrap text-xs">{{ formatDate(n.created_at, locale) }}</td>
          </tr>
          <tr v-if="!tableRows.length && !notif.loading">
            <td colspan="7" class="px-3 py-8 text-center text-zinc-500 dark:text-zinc-400">
              {{ t('notifications.empty') }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="pageCount() > 1" class="flex items-center justify-between text-xs">
      <span>{{ t('common.pageOf', { current: currentPage(), total: pageCount() }) }}</span>
      <div class="flex gap-2">
        <Button size="xs" variant="outline" :disabled="currentPage() <= 1" @click="gotoPage(currentPage() - 1)">{{ t('common.prev') }}</Button>
        <Button size="xs" variant="outline" :disabled="currentPage() >= pageCount()" @click="gotoPage(currentPage() + 1)">{{ t('common.next') }}</Button>
      </div>
    </div>
  </section>
</template>
