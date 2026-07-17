<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Plus, RefreshCcw, Send, Webhook as WebhookIcon, Pencil, Trash2 } from 'lucide-vue-next';

import { WebhooksApi } from '@/api/webhooks';
import type { WebhookTarget } from '@/api/types';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { formatDate } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Badge from '@/components/ui/Badge.vue';
import Checkbox from '@/components/ui/Checkbox.vue';
import Modal from '@/components/ui/Modal.vue';

const { t, locale } = useI18n();
const toast = useToastStore();

// The canonical file/share events targets can filter on. An empty
// selection subscribes the target to every event (backend contract).
const KNOWN_EVENTS = [
  'file.uploaded',
  'file.deleted',
  'file.moved',
  'file.trashed',
  'share.created',
  'drop.received',
] as const;

const items = ref<WebhookTarget[]>([]);
const loading = ref(false);
const testingId = ref<number | null>(null);

const showForm = ref(false);
const editingId = ref<number | null>(null);
const formName = ref('');
const formUrl = ref('');
const formSecret = ref('');
const formSecretSet = ref(false);
const formClearSecret = ref(false);
const formEnabled = ref(true);
const formEvents = ref<Set<string>>(new Set());
const saving = ref(false);

async function load() {
  loading.value = true;
  try {
    items.value = await WebhooksApi.list();
  } catch (e: unknown) {
    toast.error(extractError(e, 'Failed to load webhooks'));
  } finally {
    loading.value = false;
  }
}

onMounted(load);

function openCreate() {
  editingId.value = null;
  formName.value = '';
  formUrl.value = '';
  formSecret.value = '';
  formSecretSet.value = false;
  formClearSecret.value = false;
  formEnabled.value = true;
  formEvents.value = new Set();
  showForm.value = true;
}

function openEdit(target: WebhookTarget) {
  editingId.value = target.id;
  formName.value = target.name;
  formUrl.value = target.url;
  formSecret.value = '';
  formSecretSet.value = target.secret_set;
  formClearSecret.value = false;
  formEnabled.value = target.enabled;
  formEvents.value = new Set(target.events);
  showForm.value = true;
}

function toggleEvent(ev: string, on: boolean) {
  const next = new Set(formEvents.value);
  if (on) next.add(ev);
  else next.delete(ev);
  formEvents.value = next;
}

async function save() {
  const name = formName.value.trim();
  const url = formUrl.value.trim();
  if (!name || !url) {
    toast.error(t('webhooks.errNameUrl'));
    return;
  }
  saving.value = true;
  try {
    const events = Array.from(formEvents.value);
    if (editingId.value == null) {
      await WebhooksApi.create({
        name,
        url,
        secret: formSecret.value || undefined,
        events,
        enabled: formEnabled.value,
      });
    } else {
      const payload: Record<string, unknown> = { name, url, events, enabled: formEnabled.value };
      // Secret is write-only: absent keeps the stored one, '' clears it.
      if (formClearSecret.value) payload.secret = '';
      else if (formSecret.value) payload.secret = formSecret.value;
      await WebhooksApi.update(editingId.value, payload);
    }
    toast.success(t('webhooks.saved'));
    showForm.value = false;
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, 'Save failed'));
  } finally {
    saving.value = false;
  }
}

async function toggleEnabled(target: WebhookTarget, enabled: boolean) {
  try {
    await WebhooksApi.update(target.id, { enabled });
    target.enabled = enabled;
  } catch (e: unknown) {
    toast.error(extractError(e, 'Update failed'));
    await load();
  }
}

async function removeTarget(target: WebhookTarget) {
  if (!window.confirm(t('webhooks.deleteConfirm', { name: target.name }))) return;
  try {
    await WebhooksApi.remove(target.id);
    toast.success(t('webhooks.deleted'));
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, 'Delete failed'));
  }
}

async function testTarget(target: WebhookTarget) {
  testingId.value = target.id;
  try {
    const r = await WebhooksApi.test(target.id);
    if (r.ok) {
      toast.success(t('webhooks.testOk', { name: target.name }));
    } else {
      toast.error(t('webhooks.testFail', { error: r.result?.error ?? '?' }));
    }
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, 'Test failed'));
  } finally {
    testingId.value = null;
  }
}
</script>

<template>
  <section class="space-y-4">
    <header class="flex items-center justify-between">
      <div class="flex items-center gap-2">
        <WebhookIcon class="h-6 w-6 text-brand-600 dark:text-brand-400" />
        <h1 class="text-xl font-semibold">{{ t('webhooks.title') }}</h1>
      </div>
      <div class="flex items-center gap-2">
        <Button variant="outline" size="sm" @click="load" :loading="loading">
          <RefreshCcw class="h-4 w-4" />
          {{ t('common.refresh') }}
        </Button>
        <Button variant="primary" size="sm" @click="openCreate">
          <Plus class="h-4 w-4" />
          {{ t('webhooks.add') }}
        </Button>
      </div>
    </header>

    <p class="text-sm text-zinc-600 dark:text-zinc-400">{{ t('webhooks.subtitle') }}</p>

    <div class="overflow-x-auto rounded-xl border border-zinc-200 dark:border-zinc-800">
      <table class="w-full text-sm">
        <thead class="bg-zinc-50 text-xs uppercase text-zinc-500 dark:bg-zinc-900 dark:text-zinc-400">
          <tr>
            <th class="px-3 py-2 text-left">{{ t('common.name') }}</th>
            <th class="px-3 py-2 text-left">URL</th>
            <th class="px-3 py-2 text-left">{{ t('webhooks.fields.events') }}</th>
            <th class="px-3 py-2 text-left">{{ t('webhooks.fields.secret') }}</th>
            <th class="px-3 py-2 text-left">{{ t('webhooks.fields.lastStatus') }}</th>
            <th class="px-3 py-2 text-left">{{ t('webhooks.fields.enabled') }}</th>
            <th class="px-3 py-2 text-right">{{ t('webhooks.fields.actions') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-zinc-200 dark:divide-zinc-800">
          <tr v-for="w in items" :key="w.id" class="bg-white dark:bg-zinc-950">
            <td class="px-3 py-2 font-medium">{{ w.name }}</td>
            <td class="px-3 py-2 max-w-xs truncate font-mono text-xs">{{ w.url }}</td>
            <td class="px-3 py-2 text-xs">
              <template v-if="w.events.length">
                <span
                  v-for="ev in w.events"
                  :key="ev"
                  class="mr-1 inline-block rounded bg-zinc-100 px-1.5 py-0.5 font-mono text-[11px] dark:bg-zinc-800"
                  >{{ ev }}</span
                >
              </template>
              <span v-else class="text-zinc-500">{{ t('webhooks.allEvents') }}</span>
            </td>
            <td class="px-3 py-2">
              <Badge v-if="w.secret_set" tone="emerald">{{ t('webhooks.secretSet') }}</Badge>
              <Badge v-else tone="zinc">{{ t('webhooks.secretUnset') }}</Badge>
            </td>
            <td class="px-3 py-2 text-xs">
              <template v-if="w.last_status">
                <Badge :tone="w.last_status.status === 'sent' ? 'emerald' : 'rose'">
                  {{ w.last_status.status === 'sent' ? t('webhooks.statusSent') : t('webhooks.statusFailed') }}
                </Badge>
                <span v-if="w.last_status.error" class="ml-1 text-rose-500">— {{ w.last_status.error }}</span>
                <div class="mt-0.5 text-[11px] text-zinc-500">{{ formatDate(w.last_status.at, locale) }}</div>
              </template>
              <span v-else class="text-zinc-500">—</span>
            </td>
            <td class="px-3 py-2">
              <Toggle :model-value="w.enabled" @update:model-value="(v: boolean) => toggleEnabled(w, v)" />
            </td>
            <td class="px-3 py-2">
              <div class="flex justify-end gap-1">
                <Button size="xs" variant="outline" :loading="testingId === w.id" @click="testTarget(w)">
                  <Send class="h-3.5 w-3.5" />
                  {{ t('webhooks.test') }}
                </Button>
                <Button size="xs" variant="ghost" @click="openEdit(w)" :aria-label="t('common.edit')">
                  <Pencil class="h-3.5 w-3.5" />
                </Button>
                <Button size="xs" variant="ghost" @click="removeTarget(w)" :aria-label="t('common.delete')">
                  <Trash2 class="h-3.5 w-3.5 text-rose-500" />
                </Button>
              </div>
            </td>
          </tr>
          <tr v-if="!items.length && !loading">
            <td colspan="7" class="px-3 py-8 text-center text-zinc-500 dark:text-zinc-400">
              {{ t('webhooks.empty') }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <Modal v-model="showForm" :title="editingId == null ? t('webhooks.add') : t('webhooks.edit')" size="lg">
      <form class="space-y-4" @submit.prevent="save">
        <Input v-model="formName" :label="t('common.name')" :placeholder="t('webhooks.namePlaceholder')" />
        <Input v-model="formUrl" label="URL" placeholder="https://example.com/hooks/filex" />
        <div>
          <Input
            v-model="formSecret"
            :label="t('webhooks.fields.secret')"
            type="password"
            :placeholder="formSecretSet ? t('webhooks.secretKeepPlaceholder') : t('webhooks.secretPlaceholder')"
            :disabled="formClearSecret"
          />
          <p class="mt-1 text-xs text-zinc-500">{{ t('webhooks.secretHint') }}</p>
          <Checkbox
            v-if="editingId != null && formSecretSet"
            class="mt-2"
            :model-value="formClearSecret"
            :label="t('webhooks.clearSecret')"
            @update:model-value="(v: boolean) => (formClearSecret = v)"
          />
        </div>
        <div>
          <span class="text-sm font-medium text-zinc-800 dark:text-zinc-100">{{ t('webhooks.fields.events') }}</span>
          <p class="mb-2 text-xs text-zinc-500">{{ t('webhooks.eventsHint') }}</p>
          <div class="grid grid-cols-2 gap-2 sm:grid-cols-3">
            <Checkbox
              v-for="ev in KNOWN_EVENTS"
              :key="ev"
              :model-value="formEvents.has(ev)"
              :label="ev"
              @update:model-value="(v: boolean) => toggleEvent(ev, v)"
            />
          </div>
        </div>
        <Toggle v-model="formEnabled" :label="t('webhooks.fields.enabled')" />
        <div class="flex justify-end gap-2">
          <Button type="button" size="sm" variant="ghost" @click="showForm = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" size="sm" variant="primary" :loading="saving">{{ t('common.save') }}</Button>
        </div>
      </form>
    </Modal>
  </section>
</template>
