import { api } from './client';

export interface TrashEntry {
  id: number;
  storage_id: number;
  storage_name?: string;
  path: string;
  name: string;
  size: number;
  mime?: string;
  deleted_at: string;
  /** Days remaining before automatic purge. */
  ttl_days?: number;
}

export interface TrashList {
  entries: TrashEntry[];
  total: number;
  limit: number;
  offset: number;
}

export const trashApi = {
  /** List soft-deleted nodes across storages. */
  async list(params: { storage_id?: number; limit?: number; offset?: number } = {}) {
    const res = await api.get<TrashList>('/files/manager/trash', { params });
    return res.data;
  },

  /** Restore a node (clears `deleted_at`). */
  async restore(nodeId: number) {
    const res = await api.post('/files/manager/restore', { node_id: nodeId });
    return res.data;
  },

  /** Permanently delete a single node (admin or owner). */
  async purge(nodeId: number) {
    const res = await api.delete(`/admin/trash/${nodeId}`);
    return res.data;
  },

  /** Empty trash for a storage (admin). `older_than_days` optional. */
  async empty(opts: { storage_id?: number; older_than_days?: number } = {}) {
    const res = await api.post('/admin/trash/empty', opts);
    return res.data;
  },
};
