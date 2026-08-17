import { defineStore } from 'pinia';
import { computed, ref } from 'vue';

import { StorageDriversApi } from '@/api/storageDrivers';
import type { StorageDriver, StorageDriverDescriptor, StorageField } from '@/api/types';

/**
 * The one source every admin surface uses to know what a storage driver
 * needs: the "new storage" page, the storage editor and the replication
 * target dialog all render their form from these descriptors.
 *
 * Fetched once per session; `fetch()` is safe to call from every view.
 */
export const useStorageDriversStore = defineStore('storageDrivers', () => {
  const items = ref<StorageDriverDescriptor[]>([]);
  const loading = ref(false);
  const loaded = ref(false);
  const error = ref<string | null>(null);

  const names = computed(() => items.value.map((d) => d.driver));

  async function fetch(force = false): Promise<void> {
    if (loading.value) return;
    if (loaded.value && !force) return;
    loading.value = true;
    error.value = null;
    try {
      items.value = await StorageDriversApi.list();
      loaded.value = true;
    } catch (e: unknown) {
      // Best effort: a surface that cannot reach the catalogue shows an
      // empty driver list and says so rather than falling back to a
      // hardcoded one — a stale hardcoded list is what broke the form.
      error.value = e instanceof Error ? e.message : String(e);
    } finally {
      loading.value = false;
    }
  }

  function descriptor(driver: StorageDriver | undefined): StorageDriverDescriptor | undefined {
    if (!driver) return undefined;
    return items.value.find((d) => d.driver === driver);
  }

  function fields(driver: StorageDriver | undefined): StorageField[] {
    return descriptor(driver)?.fields ?? [];
  }

  /**
   * Fresh config for a driver: every declared field seeded with its
   * default (or an empty value of the right type). Switching drivers
   * replaces the config wholesale — stale keys from the previous driver
   * are never carried over.
   */
  function defaults(driver: StorageDriver | undefined): Record<string, unknown> {
    const out: Record<string, unknown> = {};
    for (const f of fields(driver)) {
      if (f.default !== undefined && f.default !== null) {
        out[f.key] = f.default;
        continue;
      }
      switch (f.type) {
        case 'bool':
          out[f.key] = false;
          break;
        case 'int':
          out[f.key] = null;
          break;
        default:
          out[f.key] = '';
      }
    }
    return out;
  }

  return { items, loading, loaded, error, names, fetch, descriptor, fields, defaults };
});
