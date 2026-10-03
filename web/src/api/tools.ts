import { api } from './client';

/** What a thumbnail repair draws: `fix` the missing, failed, skipped and
 *  stale thumbnails; `rebuild` every file in scope. */
export type ThumbRepairMode = 'fix' | 'rebuild';

/** Why a storage was left out of a run: its catalogue cannot hold its files
 *  yet (backend server.Gap*). */
export type ThumbRefusalCode = 'sync_running' | 'sync_aborted' | 'never_synced';

/**
 * One repair run, as `POST` and `GET /admin/tools/thumbnails/repair` report
 * it. `running: false` is the end. `GET` answers `{ running: false }` alone
 * when this tenant has not asked for one.
 */
export interface ThumbRepairStatus {
  /** The ops row: it is in the tray, and `cancel` stops it. */
  op_id?: number;
  running: boolean;
  queued?: boolean;
  cancelled?: boolean;
  /** ok | partial | failed | cancelled | running | pending */
  status?: string;
  mode?: ThumbRepairMode;
  /** The qualified path asked for; '' was every storage. */
  path?: string;
  storage_id?: number;
  total?: number;
  processed?: number;
  ok?: number;
  failed?: number;
  skipped?: number;
  refused?: { storage_id: number; storage?: string; code: ThumbRefusalCode | string }[];
  error?: string;
  started_at?: string;
  finished_at?: string;
}

/**
 * One handler asked about a file (0.50): `handler` is `builtin` or
 * `app:<name>`, `version` the app's; `ok`, or the reason in the row's own
 * fields (code, limit, tool, detail).
 */
export interface ThumbAttempt {
  handler: string;
  app?: string;
  version?: string;
  ok: boolean;
  code?: string;
  limit?: number;
  tool?: string;
  detail?: string;
  /** oo_retry: the tries so far. */
  tries?: number;
}

/** How many ready thumbnails one handler drew (`GET …/thumbnails/generators`).
 *  `generator` is `builtin`, `app:<name>@<version>`, or '' (drawn before 0.50,
 *  or the placeholder card). */
export interface ThumbGenerator {
  generator: string;
  app?: string;
  version?: string;
  count: number;
}

/** A file without a thumbnail, and why (`code`). */
export interface ThumbProblem {
  node_id: number;
  storage_id: number;
  storage: string;
  /** Qualified: what a repair of this one file is asked with. */
  path: string;
  name: string;
  size: number;
  /** `ready` only for a file drawn after an earlier handler failed (`fell_back`). */
  state: 'failed' | 'skipped' | 'ready';
  /**
   * svg_too_large (limit: bytes) · svg_timeout (limit: ms) · no_engine ·
   * no_tool (tool) · failed (detail) · no_handler · app_failed (app) ·
   * app_timeout (app, limit: ms) · app_too_large (app, limit: bytes) ·
   * fell_back (generator) · the OnlyOffice document server's (0.50):
   * oo_corrupt (detail) · oo_password · oo_too_large (limit: bytes, 0 = the
   * document server's own) · oo_retry (tries, detail)
   */
  code:
    | 'svg_too_large'
    | 'svg_timeout'
    | 'no_engine'
    | 'no_tool'
    | 'archive_encrypted'
    | 'archive_too_large'
    | 'failed'
    | 'no_handler'
    | 'app_failed'
    | 'app_timeout'
    | 'app_too_large'
    | 'fell_back'
    | 'oo_corrupt'
    | 'oo_password'
    | 'oo_too_large'
    | 'oo_retry'
    | string;
  limit?: number;
  /** no_tool: the kind whose program is missing (video, audio, pdf, heif,
   *  heic_codec: ImageMagick without a HEVC decoder; office: OnlyOffice is not
   *  configured). */
  tool?: string;
  detail?: string;
  /** oo_retry: the tries so far (six in all). */
  tries?: number;
  /** The marker the explorer shows on the file: corrupt, encrypted, too_large. */
  note?: 'corrupt' | 'encrypted' | 'too_large';
  attempted_at?: string;
  /** The app a reason is about (app_failed, app_timeout, app_too_large). */
  app?: string;
  /** fell_back: who drew it (`builtin` or `app:<name>`). */
  generator?: string;
  /** The handlers asked, in order, and what each answered (absent before 0.50). */
  attempts?: ThumbAttempt[];
}

/** The SVG limits in force, their bounds, and whether this caller may change
 *  them (they are the instance's; a tenant administrator reads them). */
export interface ThumbSettings {
  /** Pictures on folder cards and the list on a resting pointer. */
  folder_previews: boolean;
  svg_max_mb: number;
  svg_timeout_seconds: number;
  svg_max_mb_min: number;
  svg_max_mb_max: number;
  svg_timeout_seconds_min: number;
  svg_timeout_seconds_max: number;
  /** Office documents drawn by OnlyOffice (0.50): the largest one sent, and
   *  how many at once per filex process. */
  office_max_mb: number;
  office_slots: number;
  office_max_mb_min: number;
  office_max_mb_max: number;
  office_slots_min: number;
  office_slots_max: number;
  editable: boolean;
}

export const toolsApi = {
  /**
   * Start a repair. `path` is `storage://folder`, `storage://file`,
   * `storage://` or '' for every storage. The answer is the result when the
   * run ended within a few seconds, its progress otherwise. 409 `BUSY` while
   * this tenant already has one going (with that run as `job`).
   */
  async thumbRepair(req: { path: string; mode: ThumbRepairMode }): Promise<ThumbRepairStatus> {
    const res = await api.post<ThumbRepairStatus>('/admin/tools/thumbnails/repair', req);
    return res.data;
  },

  /** The latest repair this tenant started: still running, or how it ended. */
  async thumbRepairStatus(): Promise<ThumbRepairStatus> {
    const res = await api.get<ThumbRepairStatus>('/admin/tools/thumbnails/repair');
    return res.data;
  },

  /** Stop a run: the queue's own cancel, by its ops row. */
  async cancel(opId: number): Promise<void> {
    await api.post(`/files/ops/${opId}/cancel`);
  },

  /** Files whose thumbnail failed or was skipped, and those drawn only after
   *  an earlier handler failed, most recent first. */
  async thumbProblems(): Promise<{ items: ThumbProblem[]; truncated: boolean }> {
    const res = await api.get<{ items: ThumbProblem[]; truncated: boolean }>('/admin/tools/thumbnails/problems');
    return res.data;
  },

  /** The ready thumbnails in this administrator's reach, by who drew them. */
  async thumbGenerators(): Promise<ThumbGenerator[]> {
    const res = await api.get<{ generators?: ThumbGenerator[] }>('/admin/tools/thumbnails/generators');
    return res.data.generators ?? [];
  },

  async thumbSettings(): Promise<ThumbSettings> {
    const res = await api.get<ThumbSettings>('/admin/tools/thumbnails/settings');
    return res.data;
  },

  async saveThumbSettings(v: {
    folder_previews?: boolean;
    svg_max_mb?: number;
    svg_timeout_seconds?: number;
    office_max_mb?: number;
    office_slots?: number;
  }): Promise<ThumbSettings> {
    const res = await api.patch<ThumbSettings>('/admin/tools/thumbnails/settings', v);
    return res.data;
  },
};
