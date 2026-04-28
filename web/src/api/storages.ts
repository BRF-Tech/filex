import { api } from './client';
import type {
  DriftReport,
  StorageCreateRequest,
  StorageRef,
  StorageUpdateRequest,
  SyncRun,
} from './types';

export const StoragesApi = {
  async list(): Promise<StorageRef[]> {
    const { data } = await api.get<StorageRef[]>('/admin/storages');
    return data;
  },

  async get(id: number): Promise<StorageRef> {
    const { data } = await api.get<StorageRef>(`/admin/storages/${id}`);
    return data;
  },

  async create(payload: StorageCreateRequest): Promise<StorageRef> {
    const { data } = await api.post<StorageRef>('/admin/storages', payload);
    return data;
  },

  async update(id: number, payload: StorageUpdateRequest): Promise<StorageRef> {
    const { data } = await api.patch<StorageRef>(`/admin/storages/${id}`, payload);
    return data;
  },

  async remove(id: number): Promise<void> {
    await api.delete(`/admin/storages/${id}`);
  },

  async syncNow(id: number): Promise<{ run_id: number }> {
    const { data } = await api.post<{ run_id: number }>(`/admin/storages/${id}/sync`);
    return data;
  },

  async syncHistory(id: number, limit = 50): Promise<SyncRun[]> {
    const { data } = await api.get<SyncRun[]>(`/admin/storages/${id}/sync-runs`, {
      params: { limit },
    });
    return data;
  },

  async drift(id: number): Promise<DriftReport> {
    const { data } = await api.get<DriftReport>(`/admin/storages/${id}/drift`);
    return data;
  },

  async testConnection(payload: StorageCreateRequest): Promise<{ ok: boolean; error?: string }> {
    const { data } = await api.post<{ ok: boolean; error?: string }>(
      '/admin/storages/test',
      payload,
    );
    return data;
  },
};
