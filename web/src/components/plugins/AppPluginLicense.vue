<script setup lang="ts">
/**
 * AppPluginLicense - a paid app's license, on its page (Admin → Apps → the
 * app): the status the store last signed, who holds it, the seats, the
 * dates, when it was last asked and when next; the key (its prefix only - the
 * key itself never leaves the server); a new key; "Verify now".
 *
 * Draws nothing for a free app, nor where the panel may not read licenses
 * (a tenant administrator: the route is the platform operator's).
 *
 * A license that does not hold keeps the app HELD - installed, its data and
 * settings kept, running nothing - until it holds again (internal/appstore,
 * docs/APP-PLUGINS.md → Paid apps).
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { BadgeCheck, RefreshCw, Save } from 'lucide-vue-next';

import { AppStoreApi, licenseRuns, storeRefusal, type AppLicense } from '@/api/appStore';
import { extractError } from '@/api/client';
import { storeSentence } from '@/lib/storeRefusal';
import { useToastStore } from '@/stores/toast';
import { formatDate } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Input from '@/components/ui/Input.vue';

const props = defineProps<{ pluginId: number }>();
const emit = defineEmits<{ (e: 'changed'): void }>();

const { t, locale } = useI18n();
const toast = useToastStore();

const lic = ref<AppLicense | null>(null);
const newKey = ref('');
const busy = ref(false);

async function load() {
  try {
    lic.value = await AppStoreApi.license(props.pluginId);
  } catch {
    lic.value = null;
  }
}
onMounted(load);
watch(() => props.pluginId, () => void load());

const shown = computed(() => !!lic.value?.required);
const runs = computed(() => (lic.value ? licenseRuns(lic.value.status) : true));
const tone = computed<'emerald' | 'amber' | 'rose'>(() => {
  const s = lic.value?.status;
  if (s === 'valid') return 'emerald';
  if (s === 'grace') return 'amber';
  return 'rose';
});

function when(v: string | undefined): string {
  return v ? formatDate(v, locale.value) : '-';
}

function statusText(s: string): string {
  return t(`appStore.license.status.${s}`);
}

async function act(fn: () => Promise<AppLicense>, ok: string) {
  busy.value = true;
  try {
    const before = lic.value?.held;
    lic.value = await fn();
    toast.success(ok);
    if (before !== lic.value.held) emit('changed');
  } catch (e: unknown) {
    const r = storeRefusal(e);
    toast.error((r && storeSentence(r, t)) || extractError(e, t('errors.generic')));
  } finally {
    busy.value = false;
  }
}

function verify() {
  void act(() => AppStoreApi.verifyLicense(props.pluginId), t('appStore.license.verified'));
}

function saveKey() {
  const k = newKey.value.trim();
  if (!k) return;
  void act(async () => {
    const v = await AppStoreApi.setLicenseKey(props.pluginId, k);
    newKey.value = '';
    return v;
  }, t('appStore.license.keySaved'));
}
</script>

<template>
  <section v-if="shown && lic" class="card card-body space-y-3" data-testid="app-plugin-license">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h2 class="flex items-center gap-2 text-sm font-semibold">
        <BadgeCheck class="h-4 w-4" />
        {{ t('appStore.license.title') }}
        <Badge :tone="tone" dot data-testid="app-plugin-license-status">{{ statusText(lic.status) }}</Badge>
      </h2>
      <Button size="sm" variant="outline" :loading="busy" data-testid="app-plugin-license-verify" @click="verify">
        <RefreshCw class="h-4 w-4" />
        {{ t('appStore.license.verifyNow') }}
      </Button>
    </div>

    <p
      v-if="!runs"
      class="rounded-lg border border-rose-200 bg-rose-50 p-2 text-sm text-rose-900 dark:border-rose-900/50 dark:bg-rose-950/30 dark:text-rose-200"
      role="alert"
      data-testid="app-plugin-license-held"
    >
      {{ t('appStore.license.heldNote', { status: statusText(lic.status) }) }}
    </p>
    <p
      v-else-if="lic.status === 'grace'"
      class="rounded-lg border border-amber-200 bg-amber-50 p-2 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200"
      data-testid="app-plugin-license-grace"
    >
      {{ t('appStore.license.graceNote', { until: when(lic.grace_until) }) }}
    </p>
    <p v-if="lic.store && !lic.store_trusted" class="text-sm text-rose-700 dark:text-rose-300" data-testid="app-plugin-license-untrusted">
      {{ t('appStore.license.storeUntrusted', { store: lic.store }) }}
    </p>

    <dl class="grid grid-cols-1 gap-x-4 gap-y-1 text-sm sm:grid-cols-4" data-testid="app-plugin-license-facts">
      <dt class="text-zinc-500">{{ t('appStore.license.licensee') }}</dt>
      <dd class="sm:col-span-3">{{ lic.licensee || '-' }}</dd>
      <dt class="text-zinc-500">{{ t('appStore.license.seats') }}</dt>
      <dd class="sm:col-span-3">
        <template v-if="lic.seats != null">{{ t('appStore.license.seatsOf', { used: lic.seats_used ?? 0, total: lic.seats }) }}</template>
        <template v-else>-</template>
      </dd>
      <dt class="text-zinc-500">{{ t('appStore.license.validUntil') }}</dt>
      <dd class="sm:col-span-3">{{ when(lic.valid_until) }}</dd>
      <dt class="text-zinc-500">{{ t('appStore.license.updatesUntil') }}</dt>
      <dd class="sm:col-span-3">{{ when(lic.updates_until) }}</dd>
      <dt class="text-zinc-500">{{ t('appStore.license.checkedAt') }}</dt>
      <dd class="sm:col-span-3" data-testid="app-plugin-license-checked">{{ when(lic.checked_at) }}</dd>
      <dt class="text-zinc-500">{{ t('appStore.license.nextCheck') }}</dt>
      <dd class="sm:col-span-3">{{ when(lic.next_check_by) }}</dd>
      <dt class="text-zinc-500">{{ t('appStore.license.store') }}</dt>
      <dd class="break-all font-mono sm:col-span-3">{{ lic.store || '-' }}</dd>
      <dt class="text-zinc-500">{{ t('appStore.license.key') }}</dt>
      <dd class="font-mono sm:col-span-3" data-testid="app-plugin-license-prefix">{{ lic.key_prefix || '-' }}</dd>
      <template v-if="lic.last_error">
        <dt class="text-zinc-500">{{ t('appStore.license.lastError') }}</dt>
        <dd class="break-words text-rose-700 sm:col-span-3 dark:text-rose-300">
          {{ when(lic.last_attempt_at) }} · {{ lic.last_error }}
        </dd>
      </template>
    </dl>

    <form class="flex flex-wrap items-end gap-2" @submit.prevent="saveKey">
      <div class="min-w-0 flex-1">
        <Input
          v-model="newKey"
          :label="lic.key_prefix ? t('appStore.license.changeKey') : t('appStore.license.enterKey')"
          monospace
          autocomplete="off"
          name="app-plugin-license-key"
        />
      </div>
      <Button type="submit" size="sm" variant="primary" :disabled="!newKey.trim()" :loading="busy" data-testid="app-plugin-license-save">
        <Save class="h-4 w-4" />
        {{ t('common.save') }}
      </Button>
    </form>
  </section>
</template>
