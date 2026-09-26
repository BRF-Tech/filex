/**
 * "Sync now", said truthfully and followed to its end. One behaviour for the
 * three buttons that start a scan: the Dashboard's, the Storages list's, and a
 * storage's own page.
 *
 * ⚠ Each of them said "Sync started" whatever the server answered, "a scan is
 * already running" included, and then nothing: the badge was not read again,
 * so the end of the scan, and how it ended, went unsaid. The server answers
 * only once the scan holds the storage (`running` in the list is true from
 * then on), so reading the list until `running` drops is an honest way to
 * know the scan is over; `last_sync_state` then says how it ended.
 */
import { onBeforeUnmount, ref } from 'vue';
import { useI18n } from 'vue-i18n';

import { extractError } from '@/api/client';
import { useStoragesStore } from '@/stores/storages';
import { useToastStore } from '@/stores/toast';

/** How often a followed scan's storage is read again. */
export const SYNC_FOLLOW_MS = 3000;

export function useSyncNow(opts: { onEnd?: (id: number) => void | Promise<void> } = {}) {
  const storages = useStoragesStore();
  const toast = useToastStore();
  const { t } = useI18n();

  /** Storages whose scan this page started or joined and is following. */
  const busy = ref<Set<number>>(new Set());
  const timers = new Map<number, ReturnType<typeof setTimeout>>();
  let alive = true;

  function settle(id: number) {
    const next = new Set(busy.value);
    next.delete(id);
    busy.value = next;
    clearTimeout(timers.get(id));
    timers.delete(id);
  }

  function follow(id: number, name: string) {
    const tick = async () => {
      if (!alive) return;
      try {
        await storages.fetch();
      } catch {
        // A failed read is not the end of the scan: look again.
      }
      if (!alive) return;
      const row = storages.find(id);
      if (row?.running) {
        timers.set(id, setTimeout(() => void tick(), SYNC_FOLLOW_MS));
        return;
      }
      settle(id);
      switch (row?.last_sync_state) {
        case 'ok':
          toast.success(t('storages.syncDone', { name }));
          break;
        case 'failed':
        case 'error':
          toast.error(t('storages.syncFailed', { name, error: row?.last_sync_error || t('errors.generic') }));
          break;
        case 'aborted':
          toast.info(t('storages.syncStopped', { name }));
          break;
      }
      await opts.onEnd?.(id);
    };
    timers.set(id, setTimeout(() => void tick(), SYNC_FOLLOW_MS));
  }

  async function press(id: number, name: string): Promise<void> {
    if (busy.value.has(id)) return;
    busy.value = new Set(busy.value).add(id);
    try {
      const res = await storages.syncNow(id);
      if (res?.status === 'running') toast.info(t('storages.syncAlreadyRunning', { name }));
      else toast.success(t('storages.syncStarted'));
      follow(id, name);
    } catch (e: unknown) {
      settle(id);
      toast.error(extractError(e, t('errors.generic')));
    }
  }

  onBeforeUnmount(() => {
    alive = false;
    timers.forEach((h) => clearTimeout(h));
    timers.clear();
  });

  return { press, isBusy: (id: number) => busy.value.has(id) };
}
