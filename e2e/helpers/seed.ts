import type { APIRequestContext } from '@playwright/test';
import { apiLogin } from './auth';

/**
 * Seed a local-driver storage so the file tests have somewhere to play.
 * Returns the storage row from the API.
 */
export async function seedLocalStorage(
  request: APIRequestContext,
  name = 'e2e-local',
  mountPath = '/tmp/filex-e2e-storage',
) {
  await apiLogin(request);
  const res = await request.post('/api/admin/storages', {
    data: {
      name,
      driver: 'local',
      mount_path: mountPath,
      config_json: JSON.stringify({ root: mountPath }),
      sync_mode: 'fsnotify',
      sync_interval_s: 0,
      enabled: true,
      read_only: false,
    },
  });
  if (!res.ok()) throw new Error(`seedLocalStorage failed: ${res.status()} ${await res.text()}`);
  return res.json();
}

/**
 * Best-effort cleanup — removes any storage with the given name. The
 * tests share a single DB so cleanup between runs avoids drift.
 */
export async function dropStorageByName(request: APIRequestContext, name: string) {
  await apiLogin(request);
  const list = await request.get('/api/admin/storages');
  if (!list.ok()) return;
  const items: Array<{ id: number; name: string }> = await list.json();
  for (const item of items) {
    if (item.name === name) {
      await request.delete(`/api/admin/storages/${item.id}`);
    }
  }
}
