<script setup lang="ts">
/**
 * AppStoresPanel - the stores this filex takes install links and license
 * answers from (0.52.0, docs/APP-PLUGINS.md → Trusted stores): each with where
 * its trust comes from (an administrator, on first use, or the operator's
 * FILEX_APP_STORE_URLS) and the key fingerprints it was trusted with.
 *
 * A store is TRUSTED from its own install link, never from here: the link's
 * page shows the fingerprints and asks (views/StoreInstall.vue). Here an
 * administrator only stops trusting one; a configured store is the
 * operator's and has no button.
 *
 * Draws nothing where the panel may not read them (an API-key session has
 * no panel; a tenant administrator gets 403).
 */
import { onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Store, Trash2 } from 'lucide-vue-next';

import { AppStoreApi, groupedFingerprint, type TrustedStore } from '@/api/appStore';
import { extractError } from '@/api/client';
import { formatDate } from '@/lib/format';
import { useToastStore } from '@/stores/toast';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';

const { t, locale } = useI18n();
const toast = useToastStore();

const stores = ref<TrustedStore[] | null>(null);
const busy = ref('');

async function load() {
  try {
    stores.value = await AppStoreApi.stores();
  } catch {
    stores.value = null;
  }
}
onMounted(load);

async function remove(s: TrustedStore) {
  if (!window.confirm(t('appStore.stores.confirmRemove', { store: s.origin }))) return;
  busy.value = s.origin;
  try {
    await AppStoreApi.untrust(s.origin);
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busy.value = '';
  }
}
</script>

<template>
  <section v-if="stores" class="card card-body space-y-3" data-testid="app-stores">
    <h3 class="flex items-center gap-2 text-sm font-semibold">
      <Store class="h-4 w-4" />
      {{ t('appStore.stores.title') }}
    </h3>
    <p class="text-xs text-zinc-600 dark:text-zinc-400">{{ t('appStore.stores.intro') }}</p>
    <p v-if="!stores.length" class="text-sm text-zinc-500" data-testid="app-stores-empty">{{ t('appStore.stores.empty') }}</p>
    <ul v-else class="space-y-2">
      <li
        v-for="s in stores"
        :key="s.origin"
        class="rounded-lg border border-zinc-200 p-3 text-sm dark:border-zinc-800"
        data-testid="app-store-row"
      >
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="flex min-w-0 flex-wrap items-center gap-2">
            <span class="break-all font-mono">{{ s.origin }}</span>
            <Badge :tone="s.source === 'config' ? 'zinc' : 'brand'" size="xs">
              {{ s.source === 'config' ? t('appStore.stores.configured') : t('appStore.stores.trustedBy', { who: s.approved_by_name || '-', when: formatDate(s.approved_at, locale) }) }}
            </Badge>
          </div>
          <Button
            v-if="s.source === 'admin'"
            size="sm"
            variant="ghost"
            :loading="busy === s.origin"
            data-testid="app-store-remove"
            @click="remove(s)"
          >
            <Trash2 class="h-4 w-4" />
            {{ t('appStore.stores.remove') }}
          </Button>
        </div>
        <ul class="mt-2 space-y-1 text-xs text-zinc-600 dark:text-zinc-400">
          <li v-for="k in s.keys" :key="k.id" class="break-all font-mono">
            {{ t(`appStore.trust.use.${k.use}`) }} · {{ k.id }} · {{ groupedFingerprint(k.fingerprint) }}
          </li>
        </ul>
      </li>
    </ul>
  </section>
</template>
