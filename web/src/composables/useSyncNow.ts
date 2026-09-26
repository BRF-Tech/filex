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
 *
 * The following itself is the storages store's ONE follower (core
 * lib/storageWatch — the explorer's "Catalog everything" uses the same):
 * one read of the list per tick however many storages are followed, and a
 * scan pressed on one page is still followed, and its end still said, on the
 * next page the administrator opens.
 */
import { onBeforeUnmount, ref } from 'vue';
import { useI18n } from 'vue-i18n';

import { extractError } from '@/api/client';
import { useStoragesStore } from '@/stores/storages';
import { useToastStore } from '@/stores/toast';

export function useSyncNow(opts: { onEnd?: (id: number) => void | Promise<void> } = {}) {
  const storages = useStoragesStore();
  const toast = useToastStore();
  const { t } = useI18n();

  /** Storages whose "Sync now" request is on its way (before the follower
   *  holds them). */
  const asking = ref<Set<number>>(new Set());
  let alive = true;

  function setAsking(id: number, on: boolean) {
    const next = new Set(asking.value);
    if (on) next.add(id);
    else next.delete(id);
    asking.value = next;
  }

  /** Starts (or joins) the scan and returns once the server has answered;
   *  the end is said when it comes. */
  async function press(id: number, name: string): Promise<void> {
    if (isBusy(id) || storages.deleting(id)) return;
    setAsking(id, true);
    try {
      const res = await storages.syncNow(id);
      if (res?.status === 'running') toast.info(t('storages.syncAlreadyRunning', { name }));
      else toast.success(t('storages.syncStarted'));
    } catch (e: unknown) {
      toast.error(extractError(e, t('errors.generic')));
      return;
    } finally {
      setAsking(id, false);
    }
    void storages.followScan(id).then(async (end) => {
      if (!end) return;
      switch (end.status) {
        case 'ok':
          toast.success(t('storages.syncDone', { name }));
          break;
        case 'failed':
          toast.error(t('storages.syncFailed', { name, error: end.error || t('errors.generic') }));
          break;
        case 'aborted':
          toast.info(t('storages.syncStopped', { name }));
          break;
      }
      if (alive) await opts.onEnd?.(id);
    });
  }

  function isBusy(id: number): boolean {
    return asking.value.has(id) || storages.scanning(id);
  }

  onBeforeUnmount(() => {
    alive = false;
  });

  return { press, isBusy };
}
