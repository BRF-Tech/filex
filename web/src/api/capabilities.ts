import { api } from './client';
import type { Capabilities } from './types';

export const CapabilitiesApi = {
  async fetch(): Promise<Capabilities> {
    const { data } = await api.get<Capabilities>('/capabilities');
    return data;
  },
};
