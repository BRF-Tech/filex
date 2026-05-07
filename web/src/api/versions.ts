import { api } from './client';

// Mirrors backend `model.NodeVersion` (see internal/model/node.go).
export interface NodeVersion {
  id: number;
  node_id: number;
  version_n: number;
  storage_key?: string;
  size: number;
  etag?: string;
  created_at: string;
}

export interface VersionListResponse {
  versions: NodeVersion[] | null;
  node_id: number;
}

export const versionsApi = {
  /** List version history (newest-first) for a node. */
  async list(nodeId: number): Promise<NodeVersion[]> {
    const res = await api.get<VersionListResponse>('/files/versions', {
      params: { node_id: nodeId },
    });
    return res.data.versions ?? [];
  },

  /** Restore a recorded version back over the live path. */
  async restore(nodeId: number, versionId: number, snapshotCurrent = true): Promise<void> {
    await api.post('/files/versions/restore', {
      node_id: nodeId,
      version_id: versionId,
      snapshot_current: snapshotCurrent,
    });
  },

  /** Admin-only hard delete of a single version row + its storage object. */
  async hardDelete(versionId: number): Promise<void> {
    await api.delete(`/admin/versions/${versionId}`);
  },
};
