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
 * #162 - and here a trusted store is CONNECTED: the one-time code the
 * store's "My instances" page makes is pasted in, filex makes a key for that
 * store and from then on asks it for a fresh install link when a request
 * from the store screen is approved (docs/APP-PLUGINS.md → The store
 * screen). The key never leaves the server; the row shows its fingerprint.
 *
 * Draws nothing where the panel may not read them (an API-key session has
 * no panel; a tenant administrator gets 403).
 */
import { onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Link2, Link2Off, Store, Trash2 } from 'lucide-vue-next';

import { AppStoreApi, groupedFingerprint, storeRefusal, type StoreConnection, type TrustedStore } from '@/api/appStore';
import { extractError } from '@/api/client';
import { formatDate } from '@/lib/format';
import { storeSentence } from '@/lib/storeRefusal';
import { useToastStore } from '@/stores/toast';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Input from '@/components/ui/Input.vue';

const emit = defineEmits<{
  /** The trusted stores changed (a store was removed): the store screen's settings read them again. */
  (e: 'changed'): void;
}>();

const { t, locale } = useI18n();
const toast = useToastStore();

const stores = ref<TrustedStore[] | null>(null);
const busy = ref('');
/** Each store's connection, by origin. */
const connections = ref<Record<string, StoreConnection>>({});
/** The store whose code is being typed, and the code. */
const connecting = ref('');
const code = ref('');
const connectError = ref('');

async function load() {
  try {
    stores.value = await AppStoreApi.stores();
  } catch {
    stores.value = null;
    return;
  }
  const next: Record<string, StoreConnection> = {};
  await Promise.all(
    (stores.value ?? []).map(async (s) => {
      try {
        next[s.origin] = await AppStoreApi.connection(s.origin);
      } catch {
        /* the row says "not connected" */
      }
    }),
  );
  connections.value = next;
}
onMounted(load);

async function remove(s: TrustedStore) {
  if (!window.confirm(t('appStore.stores.confirmRemove', { store: s.origin }))) return;
  busy.value = s.origin;
  try {
    await AppStoreApi.untrust(s.origin);
    await load();
    emit('changed');
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busy.value = '';
  }
}

function startConnect(s: TrustedStore) {
  connecting.value = s.origin;
  code.value = '';
  connectError.value = '';
}

async function connect() {
  const origin = connecting.value;
  if (!origin || !code.value.trim()) return;
  busy.value = origin;
  connectError.value = '';
  try {
    connections.value = { ...connections.value, [origin]: await AppStoreApi.connect(origin, code.value.trim()) };
    connecting.value = '';
    code.value = '';
    toast.success(t('appStore.connection.done', { store: origin }));
    emit('changed');
  } catch (e: unknown) {
    const r = storeRefusal(e);
    connectError.value = (r && storeSentence(r, t)) || extractError(e, t('errors.generic'));
  } finally {
    busy.value = '';
  }
}

async function disconnect(s: TrustedStore) {
  if (!window.confirm(t('appStore.connection.confirmDisconnect', { store: s.origin }))) return;
  busy.value = s.origin;
  try {
    await AppStoreApi.disconnect(s.origin);
    connections.value = { ...connections.value, [s.origin]: { store: s.origin, connected: false } };
    emit('changed');
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
            <Badge
              :tone="connections[s.origin]?.connected ? 'emerald' : 'zinc'"
              size="xs"
              dot
              data-testid="app-store-connection"
            >
              {{ connections[s.origin]?.connected ? t('appStore.connection.connected') : t('appStore.connection.notConnected') }}
            </Badge>
          </div>
          <div class="flex flex-wrap items-center gap-1">
            <Button
              v-if="connections[s.origin]?.connected"
              size="sm"
              variant="ghost"
              :loading="busy === s.origin"
              data-testid="app-store-disconnect"
              @click="disconnect(s)"
            >
              <Link2Off class="h-4 w-4" />
              {{ t('appStore.connection.disconnect') }}
            </Button>
            <Button
              size="sm"
              variant="outline"
              :disabled="busy === s.origin"
              data-testid="app-store-connect-open"
              @click="startConnect(s)"
            >
              <Link2 class="h-4 w-4" />
              {{ connections[s.origin]?.connected ? t('appStore.connection.reconnect') : t('appStore.connection.connect') }}
            </Button>
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
        </div>
        <ul class="mt-2 space-y-1 text-xs text-zinc-600 dark:text-zinc-400">
          <li v-for="k in s.keys" :key="k.id" class="break-all font-mono">
            {{ t(`appStore.trust.use.${k.use}`) }} · {{ k.id }} · {{ groupedFingerprint(k.fingerprint) }}
          </li>
        </ul>
        <p
          v-if="connections[s.origin]?.connected"
          class="mt-2 break-all text-xs text-zinc-600 dark:text-zinc-400"
          data-testid="app-store-connection-detail"
        >
          {{
            t('appStore.connection.detail', {
              key: groupedFingerprint(connections[s.origin]?.key_fingerprint || ''),
              who: connections[s.origin]?.connected_by_name || '-',
              when: formatDate(connections[s.origin]?.connected_at, locale),
            })
          }}
        </p>
        <!-- The store's one-time code, pasted here. What it does is said
             before the field, not after a press. -->
        <form
          v-if="connecting === s.origin"
          class="mt-3 space-y-2 rounded-lg bg-zinc-50 p-3 dark:bg-zinc-900"
          data-testid="app-store-connect-form"
          @submit.prevent="connect"
        >
          <p class="text-xs text-zinc-700 dark:text-zinc-300">{{ t('appStore.connection.intro') }}</p>
          <Input
            v-model="code"
            name="app-store-connect-code"
            :label="t('appStore.connection.code')"
            :hint="t('appStore.connection.codeHint')"
            autocomplete="off"
            :error="connectError || null"
            data-testid="app-store-connect-code"
          />
          <div class="flex flex-wrap justify-end gap-2">
            <Button type="button" size="sm" variant="ghost" @click="connecting = ''">{{ t('common.cancel') }}</Button>
            <Button type="submit" size="sm" variant="primary" :disabled="!code.trim()" :loading="busy === s.origin" data-testid="app-store-connect">
              {{ t('appStore.connection.connect') }}
            </Button>
          </div>
        </form>
      </li>
    </ul>
  </section>
</template>
