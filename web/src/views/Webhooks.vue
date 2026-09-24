<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { Plus, RefreshCcw, Send, Webhook as WebhookIcon, Pencil, Trash2 } from 'lucide-vue-next';

import { WebhooksApi } from '@/api/webhooks';
import type { WebhookTarget } from '@/api/types';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { formatDate, formatRelative } from '@/lib/format';
import { WEBHOOK_EVENTS, eventOffReason, webhookEventKey } from '@/lib/webhookEvents';
import { useCapabilitiesStore } from '@/stores/capabilities';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Badge from '@/components/ui/Badge.vue';
import Checkbox from '@/components/ui/Checkbox.vue';
import Modal from '@/components/ui/Modal.vue';
import GlobalWebhookCard from '@/components/GlobalWebhookCard.vue';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';

const { t, locale } = useI18n();
const caps = useCapabilitiesStore();

/**
 * The line under an event's box: its wire name, and — when the service it
 * depends on is off here — why it will not fire yet (lib/webhookEvents
 * eventOffReason). ⚠ The box stays tickable: an operator may subscribe ahead
 * of switching the service on. The settings dialog offered these to people
 * as if they could happen (QA #39).
 */
function eventNote(ev: string): string {
  const off = eventOffReason(ev, caps.data);
  return off ? `${ev} — ${t(off)}` : ev;
}
const toast = useToastStore();

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
    toast.error(extractError(e, t('errors.loadFailed')));
  } finally {
    loading.value = false;
  }
}

onMounted(load);

/* The explorer's table (DataTable), remembered under `admin.webhooks`. Every
 * target arrives in one answer, so the table sorts them itself. The status
 * column sorts by WHEN the last delivery happened — the one ordering of it
 * that answers a question ("which one fired last"). */
function lastDeliveryMs(w: WebhookTarget): number | null {
  const at = w.last_delivery_at || w.last_status?.at;
  return at ? Date.parse(at) : null;
}
const columns = computed<DataColumn<WebhookTarget>[]>(() => [
  { id: 'name', label: t('common.name'), sortable: true, width: 180 },
  { id: 'url', label: 'URL', sortable: true, width: 260 },
  {
    id: 'events',
    label: t('webhooks.fields.events'),
    sortable: true,
    width: 220,
    sortValue: (w) => (w.events.length ? w.events.join(',') : null),
  },
  {
    id: 'secret',
    label: t('webhooks.fields.secret'),
    sortable: true,
    width: 120,
    sortValue: (w) => (w.secret_set ? 1 : 0),
  },
  {
    id: 'last_status',
    label: t('webhooks.fields.lastStatus'),
    sortable: true,
    sortDir: 'desc',
    width: 200,
    sortValue: lastDeliveryMs,
  },
  {
    id: 'enabled',
    label: t('webhooks.fields.enabled'),
    sortable: true,
    width: 100,
    sortValue: (w) => (w.enabled ? 1 : 0),
  },
]);

function openCreate() {
  formTried.value = false;
  formFailure.value = '';
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
  formTried.value = false;
  formFailure.value = '';
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

/*
 * ⚠ Name and URL are required and SAID to be (the star), checked before the
 * request, and every refusal is shown in the dialog. Before: neither box was
 * marked, an empty save answered with a toast drawn BEHIND the dialog's
 * backdrop, and a URL that was not http(s) came back as the server's English
 * ("url must start with http:// or https://") — release-candidate sweep,
 * 2026-09-21. The URL rule is the server's (validWebhookURL).
 */
const formTried = ref(false);
const formFailure = ref('');
watch([formName, formUrl], () => {
  formFailure.value = '';
});
const nameError = computed(() =>
  formTried.value && !formName.value.trim() ? t('webhooks.errName') : '',
);
const urlError = computed(() => {
  const u = formUrl.value.trim();
  if (!u) return formTried.value ? t('webhooks.errUrl') : '';
  return /^https?:\/\/[^\s/]+/i.test(u) ? '' : t('webhooks.errUrlScheme');
});

async function save() {
  formTried.value = true;
  formFailure.value = '';
  const name = formName.value.trim();
  const url = formUrl.value.trim();
  if (nameError.value || urlError.value) return;
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
    formFailure.value = extractError(e, t('errors.generic'));
  } finally {
    saving.value = false;
  }
}

async function toggleEnabled(target: WebhookTarget, enabled: boolean) {
  try {
    await WebhooksApi.update(target.id, { enabled });
    target.enabled = enabled;
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.updateFailed')));
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
    toast.error(extractError(e, t('errors.deleteFailed')));
  }
}

// Persisted last-delivery helpers (last_http_status / last_error /
// last_delivery_at — migration 00019). 2xx = green; anything else —
// including 0 (no HTTP response at all) — is red with the error message
// in the tooltip.
function deliveryOk(w: WebhookTarget): boolean {
  return w.last_http_status != null && w.last_http_status >= 200 && w.last_http_status < 300;
}

function deliveryBadgeText(w: WebhookTarget): string {
  const code = w.last_http_status ?? 0;
  const rel = formatRelative(w.last_delivery_at ?? null, locale.value);
  if (deliveryOk(w)) return `${code} · ${rel}`;
  const label = code > 0 ? `HTTP ${code}` : t('webhooks.statusFailed');
  return `${label} · ${rel}`;
}

function deliveryTooltip(w: WebhookTarget): string {
  if (deliveryOk(w)) return t('webhooks.lastDeliveryOk', { at: formatDate(w.last_delivery_at ?? null, locale.value) });
  return w.last_error || t('webhooks.statusFailed');
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
    toast.error(extractError(e, t('errors.actionFailed')));
  } finally {
    testingId.value = null;
  }
}

/** The row's verbs, behind its one pinned `Actions` control. `Test` fires a
 *  live delivery, so it is disabled while one is already in flight for this
 *  row rather than being clickable twice. */
function rowActions(row: WebhookTarget): ContextAction[] {
  return [
    { key: 'test', label: t('webhooks.test'), icon: 'upload', disabled: testingId.value === row.id },
    { key: 'edit', label: t('common.edit'), icon: 'rename' },
    { key: 'delete', label: t('common.delete'), icon: 'delete', danger: true },
  ];
}

function onRowAction(key: string, row: WebhookTarget) {
  if (key === 'test') testTarget(row);
  else if (key === 'edit') openEdit(row);
  else if (key === 'delete') removeTarget(row);
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

    <!-- The default webhook sits beside the targets so every place an event
         goes is set on ONE page (it used to be on the notifications page). -->
    <GlobalWebhookCard />

    <DataTable
      table-id="admin.webhooks"
      :columns="columns"
      :rows="items"
      :loading="loading"
      :empty="t('webhooks.empty')"
      row-key="id"
      :row-actions="(row: WebhookTarget) => rowActions(row)"
      :row-actions-test-id="(row: WebhookTarget) => `webhook-actions-${row.id}`"
      @row-action="(key: string, row: WebhookTarget) => onRowAction(key, row)"
    >
      <template #cell-url="{ row }">
        <span class="tbl-mono tbl-clamp" :title="row.url">{{ row.url }}</span>
      </template>

      <!-- ⚠ Wrapped: a DataTable cell is a flex row, and the badges would
           otherwise run off its right edge instead of wrapping. -->
      <template #cell-events="{ row }">
        <div v-if="row.events.length">
          <Badge v-for="ev in row.events" :key="ev" tone="zinc" size="xs" class="me-1">{{ ev }}</Badge>
        </div>
        <span v-else class="tbl-sub">{{ t('webhooks.allEvents') }}</span>
      </template>

      <template #cell-secret="{ row }">
        <Badge v-if="row.secret_set" tone="emerald">{{ t('webhooks.secretSet') }}</Badge>
        <Badge v-else tone="zinc">{{ t('webhooks.secretUnset') }}</Badge>
      </template>

      <template #cell-last_status="{ row }">
        <template v-if="row.last_http_status != null">
          <Badge :tone="deliveryOk(row) ? 'emerald' : 'rose'" :title="deliveryTooltip(row)">
            {{ deliveryBadgeText(row) }}
          </Badge>
        </template>
        <div v-else-if="row.last_status">
          <Badge :tone="row.last_status.status === 'sent' ? 'emerald' : 'rose'">
            {{ row.last_status.status === 'sent' ? t('webhooks.statusSent') : t('webhooks.statusFailed') }}
          </Badge>
          <span v-if="row.last_status.error" class="ms-1 text-rose-500">— {{ row.last_status.error }}</span>
          <span class="tbl-sub">{{ formatDate(row.last_status.at, locale) }}</span>
        </div>
        <span v-else>—</span>
      </template>

      <template #cell-enabled="{ row }">
        <Toggle :model-value="row.enabled" @update:model-value="(v: boolean) => toggleEnabled(row, v)" />
      </template>

    </DataTable>

    <Modal v-model="showForm" :title="editingId == null ? t('webhooks.add') : t('webhooks.edit')" size="lg">
      <!-- ⚠ novalidate: the boxes are marked `required` for the star and for
           assistive tech, but the checking is ours (said in the panel's
           language, inside the dialog). Without it the browser intercepts
           Enter / a submit button with its own bubble, in the BROWSER's
           language, and our check never runs (seen in the RC re-test,
           2026-09-21: an empty New webhook save showed no message of ours). -->
      <form class="space-y-4" novalidate @submit.prevent="save">
        <Input
          v-model="formName"
          :label="t('common.name')"
          :placeholder="t('webhooks.namePlaceholder')"
          required
          :error="nameError || null"
          name="webhook-name"
        />
        <Input
          v-model="formUrl"
          label="URL"
          placeholder="https://example.com/hooks/filex"
          required
          :error="urlError || null"
          name="webhook-url"
        />
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
              v-for="ev in WEBHOOK_EVENTS"
              :key="ev"
              :model-value="formEvents.has(ev)"
              :label="t(webhookEventKey(ev))"
              :description="eventNote(ev)"
              :data-testid="`webhook-event-${ev}`"
              @update:model-value="(v: boolean) => toggleEvent(ev, v)"
            />
          </div>
        </div>
        <Toggle v-model="formEnabled" :label="t('webhooks.fields.enabled')" />
        <p v-if="formFailure" class="error-text" role="alert" data-testid="webhook-form-error">{{ formFailure }}</p>
        <div class="flex justify-end gap-2">
          <Button type="button" size="sm" variant="ghost" @click="showForm = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" size="sm" variant="primary" :loading="saving">{{ t('common.save') }}</Button>
        </div>
      </form>
    </Modal>
  </section>
</template>
