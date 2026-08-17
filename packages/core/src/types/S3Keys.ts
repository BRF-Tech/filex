/**
 * The S3 access-key surface, as the frontend sees it.
 *
 * Mirrors `model.S3AccessKey` and the connection facts the key endpoints
 * return alongside it. The facts travel WITH the keys on purpose: a key
 * without an endpoint is not a usable credential, and a UI that assembled
 * the URL itself would be a second place to get it wrong (the application
 * URL and the S3 endpoint are different hosts whenever one is configured).
 */

export interface S3AccessKey {
  id: number;
  access_key_id: string;
  user_id: number;
  api_token_id?: number | null;
  label: string;
  /** Optional confinement, in the shape S3 clients already understand. */
  bucket?: string;
  prefix?: string;
  created_at: string;
  last_used_at?: string | null;
  expires_at?: string | null;
  /** Set = the key is switched off but still in the audit trail. */
  disabled_at?: string | null;
}

/** Where to point a client, and how to address it. */
export interface S3Connection {
  /** Absolute endpoint URL — the S3 host's root, or `<app>/s3`. */
  endpoint: string;
  /** The FILEX_S3 kill switch. False = the operator turned the endpoint off. */
  enabled: boolean;
  /**
   * True when there is no dedicated host, so clients must be told to force
   * path-style addressing. A current SDK defaults to virtual-hosted and then
   * fails at DNS with an error that names neither filex nor the cause.
   */
  path_style: boolean;
}

/** What a freshly minted key returns. The secret appears exactly once. */
export interface S3KeyCreated extends S3Connection {
  key: S3AccessKey;
  secret: string;
}

/** What the caller asked for when minting. */
export interface S3KeyRequest {
  label: string;
  /** Mint FROM an API token, inheriting its scopes, confinement and expiry. */
  api_token_id?: number;
  bucket?: string;
  prefix?: string;
  expires_at?: string;
}
