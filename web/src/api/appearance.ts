/**
 * tema:v1 — appearance: the instance's themes, and the operator stylesheet.
 *
 * Three different reads with three different audiences, which is why they are
 * three endpoints and not one convenient blob:
 *
 *   GET /api/appearance        PUBLIC.  The palette list + the instance
 *                              default. The login page and every anonymous
 *                              share visitor need this before a session
 *                              exists, so it must contain nothing private.
 *   GET /api/me/custom-css     AUTHENTICATED.  The operator stylesheet. It is
 *                              behind auth precisely so the login page cannot
 *                              wear it — see lib/customCss.ts.
 *   /api/admin/themes          SUPERTENANT.  The editor's CRUD.
 */
import { api } from './client';

/** One palette as the server sends it — the same shape as a built-in. */
export interface WireTheme {
  id: string;
  name: string;
  light: Record<string, string>;
  dark: Record<string, string>;
}

export interface AppearancePayload {
  themes: WireTheme[];
  default_theme_id: string;
}

/** The portable document: what Export downloads and Import reads. */
export interface ThemeDocument {
  filex_theme: number;
  key: string;
  name: string;
  light: Record<string, string>;
  dark: Record<string, string>;
}

export interface CustomCssPayload {
  css: string;
  enabled: boolean;
}

export const AppearanceApi = {
  /** GET /api/appearance — the palette list (public). */
  async get(): Promise<AppearancePayload> {
    const { data } = await api.get<AppearancePayload>('/appearance');
    return data;
  },

  /**
   * The boot read, shared — the same memoisation BrandingApi uses and for the
   * same reason: two callers want this payload as the page loads (the palette
   * registry and the admin screen), the response is cacheable, and two
   * parallel cold requests both miss the cache and make the server answer
   * twice for one page load.
   */
  boot(): Promise<AppearancePayload> {
    bootPromise ??= AppearanceApi.get();
    return bootPromise;
  },

  /** Drop the memoised boot payload after an edit, so the next read is fresh. */
  invalidateBoot(): void {
    bootPromise = null;
  },

  /** GET /api/me/custom-css — the operator stylesheet (signed in only). */
  async customCss(): Promise<CustomCssPayload> {
    const { data } = await api.get<CustomCssPayload>('/me/custom-css');
    return data;
  },

  /** GET /api/admin/themes — the full documents, for the editor. */
  async list(): Promise<ThemeDocument[]> {
    const { data } = await api.get<{ themes: ThemeDocument[] }>('/admin/themes');
    return data.themes ?? [];
  },

  /**
   * PUT /api/admin/themes/{key} — create, replace, or import.
   *
   * ⚠ One call for all three. An import IS a write of a document that came
   * from a file rather than from the form, and giving it an endpoint of its
   * own would have meant a second validator to keep in step with the first.
   */
  async put(key: string, doc: ThemeDocument): Promise<ThemeDocument> {
    const { data } = await api.put<ThemeDocument>(`/admin/themes/${encodeURIComponent(key)}`, doc);
    return data;
  },

  async remove(key: string): Promise<void> {
    await api.delete(`/admin/themes/${encodeURIComponent(key)}`);
  },
};

let bootPromise: Promise<AppearancePayload> | null = null;
