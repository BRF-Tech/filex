/**
 * The settings that lived in the environment alone until 0.57 (task #258,
 * backend internal/instancesettings): `GET/PATCH /api/admin/instance-settings`.
 * Settings-table rows, db → environment / config.yaml → default; the bounds,
 * the unit, the variable that gave the first value and whether a change waits
 * for a restart travel with every row.
 */
import { api } from '@/api/client';

export type InstanceSettingGroup = 'general' | 'protection' | 'apps' | 'notifications' | 'webhooks';

export interface InstanceSettingItem {
  key: string;
  group: InstanceSettingGroup;
  kind: 'int' | 'bool' | 'language';
  value: number | boolean | string;
  default: number | boolean | string;
  min?: number;
  max?: number;
  /** minutes | hours | days | MB | GB | people */
  unit?: string;
  /** The variable that gave the setting its first value. */
  env?: string;
  /** A change applies at the next start. */
  restart: boolean;
  /** A change is waiting for that start. */
  restart_pending: boolean;
}

export const InstanceSettingsApi = {
  async list(group: InstanceSettingGroup): Promise<InstanceSettingItem[]> {
    const { data } = await api.get<{ items: InstanceSettingItem[] }>('/admin/instance-settings', { params: { group } });
    return data.items;
  },
  /** Every value checked first; one refused, nothing written (`field` + `message`). */
  async save(group: InstanceSettingGroup, patch: Record<string, number | boolean | string>): Promise<InstanceSettingItem[]> {
    const { data } = await api.patch<{ items: InstanceSettingItem[] }>('/admin/instance-settings', patch, { params: { group } });
    return data.items;
  },
};

/** The i18n key part for a setting (`sync.default_interval_minutes` → `sync_default_interval_minutes`). */
export function instanceSettingSlug(key: string): string {
  return key.replace(/\./g, '_');
}
