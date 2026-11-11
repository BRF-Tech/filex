/**
 * Uploaders (0.60) - a person's upload destinations: a key for one program
 * (a screenshot tool, a phone shortcut, a script), the folder it puts files
 * in and what the link it answers with opens. The wire shapes of
 * `/api/auth/uploaders` (backend handlers/uploaders.go).
 *
 * ⚠ The key and the ready-made client settings come back ONCE, in the answer
 * that made them (`UploaderMade`). Nothing stores them; a lost key is
 * replaced, never shown again.
 */

/** `public`: a public link anybody with it may open. `private`: the file in
 *  filex, behind sign-in and the reader's own permission. */
export type UploaderVisibility = 'public' | 'private';

export interface Uploader {
  id: number;
  name: string;
  /** The storage's name now (`''` when it is gone). */
  storage: string;
  storage_id: number;
  /** The folder inside the storage, `''` for its root. */
  folder: string;
  visibility: UploaderVisibility;
  /** A public link's life in days; 0 = the longest the server allows. */
  link_days: number;
  /** Answer with the link's short address, when an app shortens links (0.60). */
  short_link?: boolean;
  created_at: string;
  key_set_at: string;
  last_used_at?: string | null;
  /** False when the storage is gone, or no longer in the uploader's tenant:
   *  its uploads are refused until it is pointed at another folder. */
  available: boolean;
}

/** What a client needs, built by the server from the key it just made. */
export interface UploaderSetup {
  /** `POST` here, multipart `file`, `Authorization: Bearer <key>`. */
  endpoint: string;
  /** A ShareX custom uploader (`.sxcu`), as the file's JSON. */
  sharex: Record<string, unknown>;
  /** The name to save it under. */
  sharex_file: string;
  /** One line that uploads `<file>`. */
  curl: string;
}

/** `GET /api/auth/uploaders`. */
export interface UploadersList {
  uploaders: Uploader[];
  endpoint: string;
  /** The longest a public link may live for this person; 0 = no ceiling. */
  link_max_days: number;
  /** May this person make public links (share.links)? */
  may_link_publicly: boolean;
  /** May this person make uploaders at all (access.api)? */
  may_create: boolean;
  default_folder: string;
  /** The storages the person may point an uploader at. */
  storages: string[];
  /** An app shortens links and the person holds its permission (0.60). */
  short_links_available?: boolean;
}

/** The body of a create (every field) or a change (any of them). */
export interface UploaderRequest {
  name?: string;
  storage?: string;
  folder?: string;
  visibility?: UploaderVisibility;
  link_days?: number;
  /** Answer with the link's short address (0.60); sent only where offered. */
  short_link?: boolean;
}

/** `POST /api/auth/uploaders` and `POST /api/auth/uploaders/{id}/key`. */
export interface UploaderMade {
  uploader: Uploader;
  key: string;
  setup: UploaderSetup;
  /** The link life asked for was longer than the server allows, and was cut. */
  link_days_clamped?: boolean;
}

/** `PATCH /api/auth/uploaders/{id}`. */
export interface UploaderSaved {
  uploader: Uploader;
  link_days_clamped?: boolean;
}
