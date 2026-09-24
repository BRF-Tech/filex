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
  /* ⚠⚠ NO `custom_css` HERE, and the absence is the point. The operator
     stylesheet used to ride this payload — which is public, fetched before a
     session exists — so it reached anonymous visitors and the sign-in form
     itself. It is served from `GET /api/me/custom-css` behind auth now
     (`api/appearance.ts`, injected by `lib/customCss.ts`), and the server
     DELETED the field rather than blanking it. Declaring it here again would
     be a type promising a string the wire never sends: `undefined` typed as
     `string`, and every reader of it silently styling nothing. */
}

export const BrandingApi = {
  /** GET /api/branding — effective branding for this host (public). */
  async get(): Promise<BrandingConfig> {
    const { data } = await api.get<BrandingConfig>('/branding');
    return data;
  },

  /**
   * The boot read, shared. The response is `Cache-Control: max-age=60`, so two
   * parallel cold requests both miss the cache and the server answers twice
   * for one page load; memoising per page load is what stops that. Never
   * invalidated — a reload is a new page load, and the admin screens apply
   * their own saves directly.
   *
   * ⚠ The custom-stylesheet injector used to be the second caller here. It is
   * not any more: the sheet is authenticated and comes from
   * `AppearanceApi.customCss()` (`lib/customCss.ts`), never from this public
   * payload.
   */
  boot(): Promise<BrandingConfig> {
    bootPromise ??= BrandingApi.get();
    return bootPromise;
  },
};

let bootPromise: Promise<BrandingConfig> | null = null;
