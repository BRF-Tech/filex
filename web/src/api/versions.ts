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

/** WHICH file a history is of (backend versions.go List → `node`). */
export interface VersionedFile {
  id: number;
  name?: string;
  path?: string;
  storage_id?: number;
  storage_name?: string;
}

export interface VersionListResponse {
  versions: NodeVersion[] | null;
  node_id: number;
  node?: VersionedFile;
}

export const versionsApi = {
  /** List version history (newest-first) for a node. */
  async list(nodeId: number): Promise<NodeVersion[]> {
    return (await this.history(nodeId)).versions;
  },

  /** The history and the file it is of. */
  async history(nodeId: number): Promise<{ versions: NodeVersion[]; file: VersionedFile | null }> {
    const res = await api.get<VersionListResponse>('/files/versions', {
      params: { node_id: nodeId },
    });
    return { versions: res.data.versions ?? [], file: res.data.node ?? null };
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
