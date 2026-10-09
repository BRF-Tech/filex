<script setup lang="ts">
/**
 * The band above every admin page when a paid app's license does not hold
 * (0.52.0, internal/appstore): red for an app that is HELD (installed, running
 * nothing - revoked, expired, no key, its grace ended…), amber for one
 * running on its grace because the store could not be asked. Each names the
 * app and links to its page, where the license section says why and takes a
 * new key.
 *
 * Read once per panel load. Draws nothing when there is nothing to say, and
 * nothing where licenses cannot be read (a tenant administrator: the route
 * is the platform operator's; the app runtime off).
 */
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { RouterLink } from 'vue-router';
import { ShieldAlert } from 'lucide-vue-next';

import { AppStoreApi, type AppLicense } from '@/api/appStore';

const { t } = useI18n();
const all = ref<AppLicense[]>([]);

onMounted(async () => {
  try {
    all.value = await AppStoreApi.licenses();
  } catch {
    all.value = [];
  }
});

const held = computed(() => all.value.filter((l) => l.held));
const grace = computed(() => all.value.filter((l) => !l.held && l.status === 'grace'));

/* A storage plugin from a store (#215) is named by its own name and found on
 * the Storage plugins tab, where its License… section is; an app on its page. */
const nameOf = (l: AppLicense) => l.name || l.app;
const whereOf = (l: AppLicense) =>
  l.kind === 'storage' ? { name: 'plugins', query: { tab: 'storage' } } : { name: 'plugins.app', params: { name: nameOf(l) } };
</script>

<template>
  <div v-if="held.length" class="mb-3 flex gap-2 rounded-md border border-rose-300 bg-rose-50 px-3 py-2 text-sm text-rose-900 dark:border-rose-700/60 dark:bg-rose-950/40 dark:text-rose-200" role="alert" data-testid="app-license-held-band">
    <ShieldAlert class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
    <div class="space-y-1">
      <p class="font-medium">{{ t('appStore.band.heldTitle') }}</p>
      <p v-for="l in held" :key="l.app">
        <RouterLink :to="whereOf(l)" class="font-mono underline">{{ nameOf(l) }}</RouterLink>
        - {{ t(`appStore.license.status.${l.status}`) }}
      </p>
    </div>
  </div>
  <div v-if="grace.length" class="mb-3 flex gap-2 rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-700/60 dark:bg-amber-950/40 dark:text-amber-200" data-testid="app-license-grace-band">
    <ShieldAlert class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
    <div class="space-y-1">
      <p class="font-medium">{{ t('appStore.band.graceTitle') }}</p>
      <p v-for="l in grace" :key="l.app">
        <RouterLink :to="whereOf(l)" class="font-mono underline">{{ nameOf(l) }}</RouterLink>
      </p>
    </div>
  </div>
</template>
