// The mail and public-page templates (backend handlers/templates_admin.go,
// internal/templates). Each administrator edits their own scope: the
// install's, or their tenant's - the server reads which from the session.
import { api } from './client';

export type TemplateKind = 'mail' | 'page' | 'layout' | 'shell' | 'slot';
/** Whose text is in force: the caller's tenant's, the install's, or filex's own. */
export type TemplateSource = 'tenant' | 'instance' | 'default';

export interface TemplateRow {
  key: string;
  kind: TemplateKind;
  layout?: string;
  subject?: boolean;
  text?: boolean;
  /** What the page's own script needs to find (element ids, `name=…`). */
  hooks?: string[];
  vars: string[];
  /** Every variable the template has, the globals included. */
  all_vars: string[];
  source: TemplateSource;
  revision: number;
  updated_at?: string;
  last_error?: string;
  last_error_at?: string;
}

export interface TemplateList {
  scope: { tenant: boolean };
  templates: TemplateRow[];
  max_bytes: number;
  languages: string[];
}

export interface TemplateTexts {
  subject: string;
  body: string;
  text: string;
}

export interface TemplateDetail {
  template: TemplateRow;
  current: TemplateTexts;
  default: TemplateTexts;
}

export interface TemplatePreview {
  subject?: string;
  html: string;
  text?: string;
  /** What the sanitizer took out ("<script>", "onclick"). */
  removed?: string[];
  /** Hooks the draft lost. */
  missing?: string[];
}

const path = (key: string) => `/admin/templates/${encodeURIComponent(key)}`;

export const TemplatesApi = {
  async list(): Promise<TemplateList> {
    const { data } = await api.get<TemplateList>('/admin/templates');
    return data;
  },
  async get(key: string): Promise<TemplateDetail> {
    const { data } = await api.get<TemplateDetail>(path(key));
    return data;
  },
  async save(key: string, texts: TemplateTexts, revision: number, lang: string): Promise<{ template: TemplateRow; preview: TemplatePreview }> {
    const { data } = await api.put(path(key), { ...texts, revision, lang });
    return data;
  },
  async reset(key: string): Promise<{ template: TemplateRow; removed: boolean }> {
    const { data } = await api.delete(path(key));
    return data;
  },
  async preview(key: string, texts: TemplateTexts, lang: string): Promise<TemplatePreview> {
    const { data } = await api.post<{ preview: TemplatePreview }>(`${path(key)}/preview`, { ...texts, lang });
    return data.preview;
  },
  async testMail(key: string, texts: TemplateTexts, lang: string): Promise<{ sent: boolean; to: string }> {
    const { data } = await api.post(`${path(key)}/test`, { ...texts, lang });
    return data;
  },
};

/**
 * The line of template `name` the engine refused, read off its words -
 * "Liquid error (line 3): undefined variable ... in mail.share" - or null.
 * An error the engine places in ANOTHER template (the layout around this
 * one) names that one, and is no line of this one.
 */
export function errorLineOf(message: string, name: string): number | null {
  const m = /\(line (\d+)\)/.exec(message);
  if (!m) return null;
  const where = / in ([a-z][a-z0-9_.]*)/.exec(message)?.[1];
  if (where && where !== name) return null;
  const n = Number(m[1]);
  return n > 0 ? n : null;
}
