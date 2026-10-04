<script setup lang="ts">
/**
 * Directory sync on a directory provider's card (docs/LDAP.md → Directory
 * sync): Sync now, whether a run is going, and what the last one did. A run
 * goes on in the background on the server; the panel looks again every two
 * seconds until it ends.
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { RefreshCw } from 'lucide-vue-next';

import { AuthProvidersApi, type DirectorySyncStatus } from '@/api/auth-providers';
import { extractError } from '@/api/client';
import { formatRelative } from '@/lib/format';
import { useToastStore } from '@/stores/toast';
import Button from '@/components/ui/Button.vue';

const props = defineProps<{ name: string }>();
const emit = defineEmits<{ (e: 'synced'): void }>();

const { t, locale } = useI18n();
const toast = useToastStore();
const status = ref<DirectorySyncStatus | null>(null);
const starting = ref(false);
let timer: ReturnType<typeof setTimeout> | null = null;

async function load() {
  try {
    const wasRunning = status.value?.running === true;
    status.value = await AuthProvidersApi.syncStatus(props.name);
    if (status.value.running) {
      timer = setTimeout(load, 2000);
    } else if (wasRunning) {
      emit('synced');
    }
  } catch {
    // An older server has no sync: the panel stays empty.
  }
}
onMounted(load);
onBeforeUnmount(() => {
  if (timer) clearTimeout(timer);
});

async function syncNow() {
  starting.value = true;
  try {
    await AuthProvidersApi.syncStart(props.name);
    if (timer) clearTimeout(timer);
    await load();
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    starting.value = false;
  }
}

/** "6h", "30m", "1h30m" — the interval as it is configured. */
const every = computed(() => {
  const s = status.value?.interval_seconds ?? 0;
  if (!s) return '';
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  return [h ? `${h}h` : '', m ? `${m}m` : ''].join('') || `${s}s`;
});
const last = computed(() => status.value?.last ?? null);
</script>

<template>
  <div v-if="status?.available" class="rounded-lg border border-[var(--fe-border)] p-3 space-y-2 text-sm" data-testid="directory-sync">
    <div class="flex items-center justify-between gap-2 flex-wrap">
      <div>
        <div class="font-medium">{{ t('authProviders.sync.title') }}</div>
        <div class="text-xs text-zinc-500">
          {{ every ? t('authProviders.sync.every', { every }) : t('authProviders.sync.manualOnly') }}
        </div>
      </div>
      <Button
        variant="outline"
        size="sm"
        :loading="starting || status.running"
        :disabled="status.running"
        data-testid="directory-sync-now"
        @click="syncNow"
      >
        <RefreshCw class="h-4 w-4" />
        {{ status.running ? t('authProviders.sync.running') : t('authProviders.sync.now') }}
      </Button>
    </div>
    <p v-if="!last" class="text-xs text-zinc-500">{{ t('authProviders.sync.never') }}</p>
    <div v-else class="space-y-1" data-testid="directory-sync-last">
      <p class="text-xs text-zinc-500">
        {{ t(last.trigger === 'schedule' ? 'authProviders.sync.lastScheduled' : 'authProviders.sync.lastManual', { when: formatRelative(last.finished_at || last.started_at, locale) }) }}
      </p>
      <p v-if="last.error" class="error-text" role="alert">{{ last.error }}</p>
      <ul class="flex flex-wrap gap-x-4 gap-y-1 text-xs">
        <li>{{ t('authProviders.sync.found', { n: last.found }, last.found) }}</li>
        <li>{{ t('authProviders.sync.created', { n: last.created }, last.created) }}</li>
        <li>{{ t('authProviders.sync.updated', { n: last.updated }, last.updated) }}</li>
        <li v-if="last.skipped">{{ t('authProviders.sync.skipped', { n: last.skipped }, last.skipped) }}</li>
        <li v-if="last.missing">{{ t('authProviders.sync.missing', { n: last.missing }, last.missing) }}</li>
        <li v-if="last.disabled">{{ t('authProviders.sync.disabled', { n: last.disabled }, last.disabled) }}</li>
        <li v-if="last.enabled">{{ t('authProviders.sync.enabled', { n: last.enabled }, last.enabled) }}</li>
        <li v-if="last.emails_changed">{{ t('authProviders.sync.emailsChanged', { n: last.emails_changed }, last.emails_changed) }}</li>
      </ul>
      <ul v-if="last.groups_found" class="flex flex-wrap gap-x-4 gap-y-1 text-xs" data-testid="directory-sync-groups">
        <li>{{ t('authProviders.sync.groupsFound', { n: last.groups_found }, last.groups_found) }}</li>
        <li>{{ t('authProviders.sync.groupsCreated', { n: last.groups_created }, last.groups_created) }}</li>
        <li v-if="last.groups_renamed">{{ t('authProviders.sync.groupsRenamed', { n: last.groups_renamed }, last.groups_renamed) }}</li>
        <li v-if="last.groups_restored">{{ t('authProviders.sync.groupsRestored', { n: last.groups_restored }, last.groups_restored) }}</li>
        <li v-if="last.groups_removed" class="text-amber-700 dark:text-amber-400">{{ t('authProviders.sync.groupsRemoved', { n: last.groups_removed }, last.groups_removed) }}</li>
        <li v-if="last.groups_linked_by_hand">{{ t('authProviders.sync.groupsByHand', { n: last.groups_linked_by_hand }, last.groups_linked_by_hand) }}</li>
      </ul>
      <details v-if="last.problems?.length" class="text-xs">
        <summary class="cursor-pointer text-zinc-500">{{ t('authProviders.sync.problems', { n: last.problems.length }, last.problems.length) }}</summary>
        <ul class="mt-1 space-y-0.5 tbl-mono">
          <li v-for="(p, i) in last.problems" :key="i"><bdi>{{ p }}</bdi></li>
        </ul>
      </details>
    </div>
  </div>
</template>
