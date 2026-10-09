<script setup lang="ts">
/**
 * StorageStoreReview - the store review of a STORAGE plugin's install link
 * (#215, docs/PLUGINS.md → Installing from a store). The same page and the
 * same steps as an app's (views/StoreInstall.vue): the link was read and
 * checked by the server, nothing is installed until Install is pressed, and
 * closing the dialog tells the store the link was cancelled.
 *
 * Everything this dialog SAYS is the server's (handlers/app_store_storage.go
 * → `storage_review`): the sentences about what installing means (a program
 * with filex's own rights, outside any sandbox), what the store's plugin
 * validator measured and on which platform, where the build's signature
 * stands against FILEX_PLUGIN_TRUSTED_KEYS, a paid plugin's hold - and
 * whether the install may go ahead at all (`can_install`). The page draws
 * them, the build's facts and the release notes, and asks for a license key
 * when the plugin is paid.
 *
 * ⚠ While Install is on its way the dialog cannot be closed (no ×, Escape
 * and the backdrop do nothing): the server finishes the install either way,
 * and a close then would tell the store `cancelled` about a plugin that lands
 * a moment later (the same rule as AppPluginInstallWizard).
 */
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { AlertTriangle, Info, ShieldAlert, Store } from 'lucide-vue-next';

import { AppStoreApi, storeRefusal, type StoreReview, type StorageNotice } from '@/api/appStore';
import type { Plugin } from '@/api/plugins';
import { extractError } from '@/api/client';
import { formatBytes } from '@/lib/format';
import { storeSentence } from '@/lib/storeRefusal';

import Badge from '@/components/ui/Badge.vue';
import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Modal from '@/components/ui/Modal.vue';
import ReleaseNotes from '@/components/plugins/ReleaseNotes.vue';

const props = defineProps<{ modelValue: boolean; review: StoreReview | null }>();
const emit = defineEmits<{
  (e: 'update:modelValue', v: boolean): void;
  (e: 'installing', v: boolean): void;
  (e: 'installed', p: Plugin): void;
}>();

const { t, locale } = useI18n();

const rv = computed(() => props.review?.storage_review ?? null);
const licenseKey = ref('');
const installing = ref(false);
const failure = ref('');

watch(
  () => props.review?.handle,
  () => {
    licenseKey.value = '';
    failure.value = '';
  },
);

function tone(n: StorageNotice): string {
  if (n.level === 'error') return 'border-rose-200 bg-rose-50 text-rose-900 dark:border-rose-900/50 dark:bg-rose-950/30 dark:text-rose-200';
  if (n.level === 'warning') return 'border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200';
  return 'border-zinc-200 bg-zinc-50 text-zinc-700 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-300';
}

function close(v: boolean) {
  if (!v && installing.value) return;
  emit('update:modelValue', v);
}

async function install() {
  const r = props.review;
  if (!r || !rv.value?.can_install) return;
  installing.value = true;
  emit('installing', true);
  failure.value = '';
  try {
    const got = await AppStoreApi.installStorage(r.handle, licenseKey.value.trim());
    emit('installed', got.plugin);
    installing.value = false;
    emit('installing', false);
    emit('update:modelValue', false);
  } catch (e: unknown) {
    const refusal = storeRefusal(e);
    failure.value = (refusal && storeSentence(refusal)) || extractError(e, t('errors.generic'));
  } finally {
    if (installing.value) {
      installing.value = false;
      emit('installing', false);
    }
  }
}
</script>

<template>
  <Modal
    :model-value="modelValue && !!rv"
    :title="rv ? t('appStore.storage.title', { name: rv.name, version: rv.version }) : ''"
    size="lg"
    :prevent-close="installing"
    :close-on-backdrop="!installing"
    @update:model-value="close"
  >
    <div v-if="rv && review" class="space-y-4" data-testid="storage-store-review">
      <div class="flex flex-wrap items-center gap-2 text-xs">
        <Badge tone="brand" size="xs">
          <Store class="h-3 w-3" aria-hidden="true" />
          {{ t('appStore.storage.fromStore', { store: review.store }) }}
        </Badge>
        <Badge tone="zinc" size="xs">{{ t('appStore.storage.kind') }}</Badge>
        <Badge v-if="rv.paid" tone="amber" size="xs">{{ t('appStore.storage.paid') }}</Badge>
      </div>

      <ul class="space-y-2" data-testid="storage-store-notices">
        <li
          v-for="(n, i) in rv.notices"
          :key="i"
          class="flex gap-2 rounded-lg border p-2 text-sm"
          :class="tone(n)"
          :role="n.level === 'error' ? 'alert' : undefined"
          :data-testid="`storage-store-notice-${n.level}`"
        >
          <ShieldAlert v-if="n.level === 'error'" class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
          <AlertTriangle v-else-if="n.level === 'warning'" class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
          <Info v-else class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
          <span>{{ n.text }}</span>
        </li>
      </ul>

      <dl class="grid grid-cols-1 gap-x-4 gap-y-1 text-sm sm:grid-cols-[auto_minmax(0,1fr)]">
        <dt class="text-zinc-500">{{ t('appStore.storage.fields.version') }}</dt>
        <dd data-testid="storage-store-version">
          <template v-if="review.upgrade_of">{{ t('appStore.storage.upgrade', { from: review.upgrade_of.version, to: rv.version }) }}</template>
          <template v-else>{{ rv.version }}</template>
        </dd>
        <dt class="text-zinc-500">{{ t('appStore.storage.fields.platform') }}</dt>
        <dd class="font-mono text-xs">{{ rv.platform }}</dd>
        <dt class="text-zinc-500">{{ t('appStore.storage.fields.sha256') }}</dt>
        <dd class="break-all font-mono text-xs" data-testid="storage-store-sha256">{{ rv.sha256 }}</dd>
        <template v-if="rv.size">
          <dt class="text-zinc-500">{{ t('appStore.storage.fields.size') }}</dt>
          <dd>{{ formatBytes(rv.size, locale) }}</dd>
        </template>
        <dt class="text-zinc-500">{{ t('appStore.storage.fields.source') }}</dt>
        <dd class="break-all font-mono text-xs">{{ rv.source }}</dd>
        <dt class="text-zinc-500">{{ t('appStore.storage.fields.platforms') }}</dt>
        <dd class="font-mono text-xs">{{ rv.platforms.join(', ') }}</dd>
      </dl>

      <div v-if="rv.capabilities.length" class="space-y-1">
        <h3 class="text-sm font-semibold">{{ t('appStore.storage.capabilities') }}</h3>
        <ul class="flex flex-wrap gap-1" data-testid="storage-store-capabilities">
          <li v-for="c in rv.capabilities" :key="c.id">
            <Badge tone="zinc" size="xs">{{ c.label }}</Badge>
          </li>
        </ul>
      </div>

      <div v-if="rv.notes" class="space-y-1">
        <h3 class="text-sm font-semibold">{{ t('appStore.storage.notes') }}</h3>
        <ReleaseNotes :notes="rv.notes" testid="storage-store-notes" />
      </div>

      <div v-if="rv.paid" class="space-y-1">
        <Input
          v-model="licenseKey"
          name="storage-store-license"
          :label="t('appStore.license.key')"
          :placeholder="review.intent.license_key_prefix ? t('appStore.storage.licenseFromLink', { prefix: review.intent.license_key_prefix }) : t('appStore.license.enterKey')"
          autocomplete="off"
          data-testid="storage-store-license"
        />
      </div>

      <p v-if="failure" class="rounded-lg bg-rose-50 p-3 text-sm text-rose-900 dark:bg-rose-950/40 dark:text-rose-200" role="alert" data-testid="storage-store-error">
        {{ failure }}
      </p>
    </div>

    <template #footer>
      <Button variant="ghost" :disabled="installing" data-testid="storage-store-cancel" @click="close(false)">{{ t('common.cancel') }}</Button>
      <Button
        variant="primary"
        :disabled="!rv?.can_install"
        :loading="installing"
        data-testid="storage-store-install"
        @click="install"
      >
        {{ review?.upgrade_of ? t('appStore.storage.upgradeAction') : t('appStore.storage.install') }}
      </Button>
    </template>
  </Modal>
</template>
