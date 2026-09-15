/* wiring:e1 — public branding endpoint (pre-login: no auth required) */
import { api } from './client';

export interface BrandingConfig {
  name: string;
  logo_url: string;
  accent: string;
  footer_text: string;
  hide_powered_by: boolean;
  /* issue #28 — the SSO button's own label (settings key `branding.sso_label`).
     Empty means the product's translated default. */
  sso_label?: string;
  /* gorunum:v1 — the operator's own stylesheet (settings key `ui.custom_css`).
     It rides this payload instead of getting an endpoint of its own because
     this is the appearance fetch the SPA already makes on every page load,
     before a session exists. `web/src/lib/customCss.ts` injects it. */
  custom_css: string;
}

export const BrandingApi = {
  /** GET /api/branding — effective branding for this host (public). */
  async get(): Promise<BrandingConfig> {
    const { data } = await api.get<BrandingConfig>('/branding');
    return data;
  },

  /**
   * The boot read, shared. Two callers need this payload as the page loads —
   * the document title and the custom-stylesheet injector — and they must not
   * each fire their own request: the response is `Cache-Control: max-age=60`,
   * so two parallel cold requests both miss the cache and the server answers
   * twice for one page load. Memoised per page load, never invalidated (a
   * reload is a new page load; the Settings page applies its own save
   * directly).
   */
  boot(): Promise<BrandingConfig> {
    bootPromise ??= BrandingApi.get();
    return bootPromise;
  },
};

let bootPromise: Promise<BrandingConfig> | null = null;
