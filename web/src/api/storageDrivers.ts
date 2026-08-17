import { api } from './client';
import type { StorageDriverDescriptor } from './types';

/**
 * Storage driver config contracts, declared by the drivers themselves
 * (backend/internal/storage/descriptor.go) and served from
 * GET /api/admin/storage-drivers.
 *
 * Every admin surface that builds a driver config renders from this.
 * Hardcoding a field list in a form is what made three of the four
 * offered drivers uncreatable: the form collected keys the backend never
 * read, and every submit came back 400 ROOT_PATH_FORBIDDEN.
 */
export const StorageDriversApi = {
  async list(): Promise<StorageDriverDescriptor[]> {
    const { data } = await api.get<StorageDriverDescriptor[]>('/admin/storage-drivers');
    return data ?? [];
  },
};
