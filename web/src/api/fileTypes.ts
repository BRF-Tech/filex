import { api } from './client';
import type { PluginText } from '@brftech/filex-core';

// Default apps (filex 0.50): which handler opens a kind of file, and which
// draws its thumbnail (backend internal/assoc, handlers/file_types_admin.go,
// docs/APP-PLUGINS.md → Default apps).
//
// A kind is a file name's extension, lower-case, no dot. Each kind has two
// capabilities, `open` and `thumbnail`, and each an ordered list of handlers:
// `builtin` (filex's own viewer, or its own thumbnail drawer), an app's
// interface that opens files (`app:<app>/<view>`), an app that draws
// thumbnails (`app:<app>`). The administrator's rule reorders the list and
// switches handlers off; a kind with no rule keeps the default order.

/** filex's own handler, for either capability. */
export const BUILTIN_HANDLER = 'builtin';

/** The OnlyOffice document server, a thumbnail handler of filex's own (0.50):
 *  first for the office kinds while OnlyOffice is configured. */
export const ONLYOFFICE_HANDLER = 'onlyoffice';

export type FileTypeCapability = 'open' | 'thumbnail';

/** One handler (assoc.Handler). `label` is the app's (or the view's) name in
 *  its languages; empty for the built-in one, which the screen names itself. */
export interface FileTypeHandler {
  id: string;
  app?: string;
  view?: string;
  version?: string;
  label?: PluginText;
}

/** The administrator's decision for one kind and one capability (assoc.Rule). */
export interface FileTypeRule {
  order: string[];
  off: string[];
}

/** One capability of one kind, as the server says it is now (assoc.KindCap). */
export interface FileTypeCap {
  /** The handlers that are on, in the order they are asked. */
  on: FileTypeHandler[];
  /** The handlers switched off. */
  off: FileTypeHandler[];
  /** An administrator's rule decided this order (false: the default). */
  custom: boolean;
  rule?: FileTypeRule | null;
}

/** One row of Admin → Plugins → Default apps (assoc.Kind). */
export interface FileTypeKind {
  ext: string;
  mime?: string;
  open: FileTypeCap;
  thumbnail: FileTypeCap;
}

/** `GET /api/admin/file-types` and the answer of every change. */
export interface FileTypesAnswer {
  kinds: FileTypeKind[];
  /** App plugins run here (false: nothing but filex handles any kind). */
  enabled: boolean;
  /** This caller may change it: a signed-in administrator, not on a demo. */
  editable: boolean;
}

/** A change for one kind: a capability left out stays as it is; `null` puts
 *  it back to the default. */
export interface FileTypeChange {
  open?: FileTypeRule | null;
  thumbnail?: FileTypeRule | null;
}

function answerOf(data: Partial<FileTypesAnswer> | undefined): FileTypesAnswer {
  const cap = (c: Partial<FileTypeCap> | undefined): FileTypeCap => ({
    on: c?.on ?? [],
    off: c?.off ?? [],
    custom: !!c?.custom,
    rule: c?.rule ?? null,
  });
  return {
    kinds: (data?.kinds ?? []).map((k) => ({ ...k, open: cap(k.open), thumbnail: cap(k.thumbnail) })),
    enabled: !!data?.enabled,
    editable: !!data?.editable,
  };
}

const BASE = '/admin/file-types';

export const FileTypesApi = {
  async list(): Promise<FileTypesAnswer> {
    const { data } = await api.get<Partial<FileTypesAnswer>>(BASE);
    return answerOf(data);
  },

  /** Change one kind. Answers the whole list again. */
  async put(ext: string, change: FileTypeChange): Promise<FileTypesAnswer> {
    const { data } = await api.put<Partial<FileTypesAnswer>>(`${BASE}/${encodeURIComponent(ext)}`, change);
    return answerOf(data);
  },

  /** Both capabilities of one kind back to the default order. */
  async reset(ext: string): Promise<FileTypesAnswer> {
    const { data } = await api.delete<Partial<FileTypesAnswer>>(`${BASE}/${encodeURIComponent(ext)}`);
    return answerOf(data);
  },
};
