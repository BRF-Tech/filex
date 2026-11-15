<script setup lang="ts">
/**
 * #351 on the storage page: what the server says about watching this storage.
 *
 *  - `sync_suggestion`: a local storage that polls (an install from before
 *    live watching became the default for local storages - existing storages
 *    are never switched for anybody). One click switches it; the server
 *    decides which storages are offered (model.SyncModeSuggestion).
 *  - `filesystem.network`: the storage's folder is on a network filesystem or
 *    a FUSE mount. A change made on another machine never reaches a watch:
 *    with live watching every opened folder is read from the disk, and
 *    `fsnotify` (Watch the whole storage) scans at the interval instead
 *    (sync.loopFSNotify). The sentence follows the storage's saved mode;
 *    filex-watch on the machine the disk is attached to sends changes as they
 *    happen.
 */
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Eye, Network } from 'lucide-vue-next';

import type { StorageRef } from '@/api/types';
import { extractError } from '@/api/client';
import { useStoragesStore } from '@/stores/storages';
import { useToastStore } from '@/stores/toast';
import Button from './ui/Button.vue';

/** Where filex-watch is explained (docs/FILEX-WATCH.md on the docs site). */
const FILEX_WATCH_DOCS = 'https://docs.filex.sh/FILEX-WATCH';

const props = defineProps<{ storage: StorageRef }>();
const emit = defineEmits<{ (e: 'switched', updated: StorageRef): void }>();

const { t } = useI18n();
const storages = useStoragesStore();
const toast = useToastStore();
const busy = ref(false);

const offered = computed(() => props.storage.sync_suggestion === 'lazy');
const network = computed(() => props.storage.filesystem?.network === true);
const networkTitle = computed(() =>
  props.storage.filesystem?.type
    ? t('storages.networkFs.titleTyped', { type: props.storage.filesystem.type })
    : t('storages.networkFs.title'),
);
/** What the storage's own mode does there - the saved one, which is what
 *  runs. */
const networkText = computed(() => {
  switch (props.storage.sync_mode) {
    case 'lazy':
      return t('storages.networkFs.body.lazy');
    case 'fsnotify':
      return t('storages.networkFs.body.fsnotify');
    default:
      return t('storages.networkFs.body.scan');
  }
});

async function switchToLive() {
  busy.value = true;
  try {
    // The mode alone: the rest of the row - and whatever the form above has
    // not saved yet - is left as it is.
    const updated = await storages.update(props.storage.id, { sync_mode: 'lazy' });
    toast.success(t('storages.liveOffer.switched', { name: props.storage.name }));
    emit('switched', updated);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <section
    v-if="offered || network"
    class="card card-body space-y-3"
    data-testid="storage-live-watch"
  >
    <div v-if="offered" class="flex items-start gap-3" data-testid="storage-live-offer">
      <Eye class="h-5 w-5 mt-0.5 shrink-0 text-brand-600 dark:text-brand-400" aria-hidden="true" />
      <div class="min-w-0 flex-1 space-y-2">
        <h2 class="text-sm font-semibold">{{ t('storages.liveOffer.title') }}</h2>
        <p class="text-sm text-zinc-600 dark:text-zinc-300">{{ t('storages.liveOffer.body') }}</p>
        <Button
          type="button"
          size="sm"
          :loading="busy"
          data-testid="storage-live-offer-switch"
          @click="switchToLive"
        >
          {{ t('storages.liveOffer.switch') }}
        </Button>
      </div>
    </div>
    <div v-if="network" class="flex items-start gap-3" data-testid="storage-network-fs">
      <Network class="h-5 w-5 mt-0.5 shrink-0 text-amber-600 dark:text-amber-500" aria-hidden="true" />
      <div class="min-w-0 flex-1 space-y-1">
        <h2 class="text-sm font-semibold">{{ networkTitle }}</h2>
        <p class="text-sm text-zinc-600 dark:text-zinc-300">{{ networkText }}</p>
        <a
          :href="FILEX_WATCH_DOCS"
          target="_blank"
          rel="noopener noreferrer"
          class="text-sm underline text-brand-600 dark:text-brand-400"
          data-testid="storage-network-fs-docs"
        >
          {{ t('storages.networkFs.docs') }}
        </a>
      </div>
    </div>
  </section>
</template>
