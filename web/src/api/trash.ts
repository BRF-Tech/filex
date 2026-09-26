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
  /** Who put it in the trash; every key absent when nobody is named (the
   *  scanner found it gone, or it was trashed before filex kept this). */
  deleted_by_id?: number;
  deleted_by_name?: string;
  deleted_by_self?: boolean;
}

export interface TrashList {
  entries: TrashEntry[];
  total: number;
  limit: number;
  offset: number;
}

/**
 * One "empty the trash" run, as `POST` and `GET /admin/trash/empty` report it.
 * `running: false` is the end — `purged` can finish below `total`, because a
 * folder takes the rows inside it along. `GET` answers `{ running: false }`
 * alone when this tenant has not started a run since the server did.
 */
export interface TrashEmptyStatus {
  ok?: boolean;
  /** The ops row behind the run: it is in the tray, and cancelEmpty stops it. */
  op_id?: number;
  running: boolean;
  /** Waiting its turn behind another purge; `running` is true meanwhile. */
  queued?: boolean;
  /** Somebody stopped it; what it had not reached is still in the trash. */
  cancelled?: boolean;
  storage_id?: number;
  older_than_days?: number;
  /** Rows in the run's scope when it started, and the bytes their files hold. */
  total?: number;
  total_bytes?: number;
  scanned?: number;
  purged?: number;
  failed?: number;
  bytes?: number;
  error?: string;
  started_at?: string;
  finished_at?: string;
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

  /**
   * Start emptying the trash (admin). `storage_id` / `older_than_days`
   * optional. The answer is the final count when the purge finished within a
   * few seconds (`running: false`), otherwise its progress so far (`running:
   * true`) while it goes on in the background — follow it with emptyStatus().
   * 409 `BUSY` while another purge holds the trash.
   */
  async empty(opts: { storage_id?: number; older_than_days?: number } = {}): Promise<TrashEmptyStatus> {
    const res = await api.post<TrashEmptyStatus>('/admin/trash/empty', opts);
    return res.data;
  },

  /** The latest empty this tenant started: still running, or how it ended. */
  async emptyStatus(): Promise<TrashEmptyStatus> {
    const res = await api.get<TrashEmptyStatus>('/admin/trash/empty');
    return res.data;
  },

  /** Stop a running empty: the queue's own cancel, by its ops row. What it
   *  purged is gone; the rest stays in the trash. */
  async cancelEmpty(opId: number): Promise<void> {
    await api.post(`/files/ops/${opId}/cancel`);
  },
};
