<script setup lang="ts">
/**
 * The notification digest's defaults (backend notify/digest.go,
 * GET/PATCH /api/admin/notifications/digest): how long the kinds that are not
 * urgent are held, and which kinds are urgent for everybody who did not choose
 * otherwise. For the tenant the administrator runs - a single-tenant install's
 * administrator, for the instance.
 *
 * ⚠ The same catalogue and the same words as the person's own switches (the
 * settings dialog, packages/core UserSettingsDialog): a kind is named here as
 * the person who receives it reads it, and the administrator alerts are one
 * switch there and one switch here.
 */
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Layers, RotateCcw, Save } from 'lucide-vue-next';
import { DIGEST_EVENT, WEBHOOK_EVENTS, userEventKey } from '@brftech/filex-core';

import { NotificationsApi } from '@/api/notifications';
import type { DigestPolicy } from '@/api/types';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Spinner from '@/components/ui/Spinner.vue';
import Toggle from '@/components/ui/Toggle.vue';

const { t } = useI18n();
const toast = useToastStore();

const policy = ref<DigestPolicy | null>(null);
const windowMinutes = ref<string | number | null>(1);
const urgent = ref<Set<string>>(new Set());
const loading = ref(true);
const saving = ref(false);
const error = ref('');

/** One switch per kind a person meets; the digest itself is never held. A
 *  kind the server knows and this page does not show (the admin page's test)
 *  keeps what it was - it rides along in `urgent`. */
const shown = computed(() => {
  const p = policy.value;
  if (!p) return [];
  return WEBHOOK_EVENTS.filter((e) => e !== DIGEST_EVENT && p.events.includes(e));
});
const adminEvents = computed(() => policy.value?.admin_events ?? []);
const adminUrgent = computed(
  () => adminEvents.value.length > 0 && adminEvents.value.every((e) => urgent.value.has(e)),
);

/** Every kind told at once - the default, and what an install that nobody
 *  touched has: said in words, so "nothing is held" is not something to work
 *  out from a column of switches. */
const allUrgent = computed(() => {
  const p = policy.value;
  return !!p && p.events.every((e) => urgent.value.has(e));
});

const windowValid = computed(() => {
  const p = policy.value;
  const n = Number(windowMinutes.value);
  return !!p && Number.isInteger(n) && n >= p.window_min && n <= p.window_max;
});

const scopeLabel = computed(() => {
  const p = policy.value;
  if (p?.scope === 'tenant' && p.tenant) {
    return t('notifications.digest.scopeTenant', { tenant: p.tenant.name || `#${p.tenant.id}` });
  }
  return t('notifications.digest.scopeInstance');
});

function apply(p: DigestPolicy) {
  policy.value = p;
  windowMinutes.value = p.window_minutes;
  urgent.value = new Set(p.urgent_events);
}

async function load() {
  loading.value = true;
  error.value = '';
  try {
    apply(await NotificationsApi.getDigestPolicy());
  } catch (e: unknown) {
    error.value = extractError(e, t('errors.loadFailed'));
  } finally {
    loading.value = false;
  }
}

function setUrgent(events: string[], on: boolean) {
  const next = new Set(urgent.value);
  for (const ev of events) {
    if (on) next.add(ev);
    else next.delete(ev);
  }
  urgent.value = next;
}

async function save() {
  if (!policy.value) return;
  if (!windowValid.value) {
    error.value = t('notifications.digest.invalidWindow');
    return;
  }
  saving.value = true;
  error.value = '';
  try {
    apply(
      await NotificationsApi.updateDigestPolicy({
        window_minutes: Number(windowMinutes.value),
        urgent_events: [...urgent.value],
      }),
    );
    toast.success(t('notifications.digest.saved'));
  } catch (e: unknown) {
    error.value = extractError(e, t('errors.generic'));
  } finally {
    saving.value = false;
  }
}

/** The built-in window and urgent list. The list is stored as "none chosen",
 *  so a later release's list reaches this tenant too. */
async function restore() {
  const p = policy.value;
  if (!p) return;
  saving.value = true;
  error.value = '';
  try {
    apply(await NotificationsApi.updateDigestPolicy({ window_minutes: p.defaults.window_minutes, urgent_events: null }));
    toast.success(t('notifications.digest.saved'));
  } catch (e: unknown) {
    error.value = extractError(e, t('errors.generic'));
  } finally {
    saving.value = false;
  }
}

onMounted(load);
</script>

<template>
  <section
    class="space-y-4 rounded-xl border border-zinc-200 bg-white p-4 text-sm shadow-sm dark:border-zinc-800 dark:bg-zinc-900"
    data-testid="notif-digest"
  >
    <header class="flex flex-wrap items-center justify-between gap-2">
      <div class="flex items-center gap-2">
        <Layers class="h-5 w-5 text-zinc-500" />
        <h2 class="text-base font-semibold">{{ t('notifications.digest.title') }}</h2>
      </div>
      <span class="text-xs text-zinc-500" data-testid="notif-digest-scope">{{ scopeLabel }}</span>
    </header>
    <p class="text-zinc-600 dark:text-zinc-400">{{ t('notifications.digest.intro') }}</p>

    <div v-if="loading" class="flex justify-center py-4"><Spinner /></div>
    <template v-else-if="policy">
      <p
        v-if="allUrgent"
        class="rounded-lg bg-zinc-50 px-3 py-2 text-zinc-700 dark:bg-zinc-800/60 dark:text-zinc-300"
        data-testid="notif-digest-all-urgent"
      >
        {{ t('notifications.digest.allUrgent') }}
      </p>
      <div class="flex flex-wrap items-end gap-2">
        <div class="w-32">
          <Input
            v-model="windowMinutes"
            type="number"
            name="digest-window"
            :min="policy.window_min"
            :max="policy.window_max"
            :step="1"
            :label="t('notifications.digest.window')"
            :error="windowValid ? null : t('notifications.digest.invalidWindow')"
          />
        </div>
        <span class="pb-2 text-zinc-500">{{ t('notifications.digest.windowUnit') }}</span>
      </div>
      <p class="text-xs text-zinc-500">{{ t('notifications.digest.windowHint') }}</p>

      <div class="space-y-2">
        <h3 class="font-medium">{{ t('notifications.digest.urgentTitle') }}</h3>
        <p class="text-xs text-zinc-500">{{ t('notifications.digest.urgentHint') }}</p>
        <div class="grid gap-3 sm:grid-cols-2">
          <Toggle
            v-for="ev in shown"
            :key="ev"
            :model-value="urgent.has(ev)"
            :label="t(userEventKey(ev))"
            :name="`digest-urgent-${ev}`"
            :disabled="saving"
            :data-testid="`notif-digest-urgent-${ev}`"
            @update:model-value="(v: boolean) => setUrgent([ev], v)"
          />
          <Toggle
            v-if="adminEvents.length"
            :model-value="adminUrgent"
            :label="t('notifications.digest.adminAlerts')"
            :description="t('notifications.digest.adminAlertsHint')"
            name="digest-urgent-admin"
            :disabled="saving"
            data-testid="notif-digest-urgent-admin"
            @update:model-value="(v: boolean) => setUrgent(adminEvents, v)"
          />
        </div>
      </div>

      <p v-if="error" class="text-rose-600 dark:text-rose-400" data-testid="notif-digest-error">{{ error }}</p>
      <div class="flex flex-wrap justify-end gap-2">
        <Button variant="outline" size="sm" :disabled="saving" data-testid="notif-digest-restore" @click="restore">
          <RotateCcw class="h-4 w-4" />
          {{ t('notifications.digest.restore') }}
        </Button>
        <Button size="sm" :loading="saving" :disabled="!windowValid" data-testid="notif-digest-save" @click="save">
          <Save class="h-4 w-4" />
          {{ t('common.save') }}
        </Button>
      </div>
    </template>
    <p v-else-if="error" class="text-rose-600 dark:text-rose-400">{{ error }}</p>
  </section>
</template>
