/* wiring:e1 — public branding endpoint (pre-login: no auth required) */
import { api } from './client';

export interface BrandingConfig {
  name: string;
  logo_url: string;
  accent: string;
  footer_text: string;
  hide_powered_by: boolean;
}

export const BrandingApi = {
  /** GET /api/branding — effective branding for this host (public). */
  async get(): Promise<BrandingConfig> {
    const { data } = await api.get<BrandingConfig>('/branding');
    return data;
  },
};
