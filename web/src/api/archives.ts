import { api } from './client';

export interface ArchiveProvider {
  name: string;
  available: boolean;
  version?: string;
  create_formats: string[];
  extract_formats: string[];
  encrypted: boolean;
  error?: string;
}

export interface ArchiveSettings {
  enabled: boolean;
  default_format: string;
  allowed_formats: string[];
  max_entries: number;
  max_expanded_bytes: number;
  timeout_seconds: number;
  providers: ArchiveProvider[];
}

export type ArchiveSettingsPatch = Partial<Omit<ArchiveSettings, 'providers'>>;

export const ArchivesApi = {
  async get(): Promise<ArchiveSettings> {
    return (await api.get<ArchiveSettings>('/admin/archives')).data;
  },
  async update(patch: ArchiveSettingsPatch): Promise<ArchiveSettings> {
    return (await api.patch<ArchiveSettings>('/admin/archives', patch)).data;
  },
  async test(): Promise<{ ok: boolean; entries: number }> {
    return (await api.post<{ ok: boolean; entries: number }>('/admin/archives/test')).data;
  },
};
