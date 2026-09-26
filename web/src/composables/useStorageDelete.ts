/**
 * Deleting a storage, said truthfully — the Storages list's and a storage's
 * own page's one way to do it.
 *
 * ⚠ Deleting a large storage outlasts a proxy (every row of it goes), and the
 * server finishes the delete whoever stops waiting. #66 said so — "it leaves
 * this list when it is done" — and then nothing read the list again: the row
 * stayed until a reload, with "Sync now" still offered on it. The store now
 * follows the deletion (`untilDeleted`, one read of the list per tick with
 * everything else it follows), the row says "Deleting…", and the end is said.
 */
import { ref } from 'vue';
import { useI18n } from 'vue-i18n';

import { extractError } from '@/api/client';
import { useStoragesStore } from '@/stores/storages';
import { useToastStore } from '@/stores/toast';

export function useStorageDelete() {
  const storages = useStoragesStore();
  const toast = useToastStore();
  const { t } = useI18n();
  /** The delete request is on its way. */
  const busy = ref(false);

  /** Deletes `target`: true once it is gone or the server is at it (the caller
   *  moves on), false when it was refused — said either way. */
  async function remove(target: { id: number; name: string }): Promise<boolean> {
    busy.value = true;
    try {
      if ((await storages.remove(target.id)) === 'deleted') {
        toast.success(t('storages.deletedOk'));
        return true;
      }
      toast.info(t('storages.deleteStillRunning', { name: target.name }));
      void storages.untilDeleted(target.id).then((gone) => {
        if (gone) toast.success(t('storages.deletedOk'));
      });
      return true;
    } catch (e: unknown) {
      toast.error(extractError(e, t('errors.generic')));
      return false;
    } finally {
      busy.value = false;
    }
  }

  return { remove, busy };
}
