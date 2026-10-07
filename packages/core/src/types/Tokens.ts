/**
 * The self-service API-token surface (`/api/tokens`).
 *
 * ⚠ Why this exists at all: three of the six protocols filex serves take an
 * API token as their password — FTPS, WebDAV and `filex mount`. Until
 * 2026-08-17 the only place to mint one was `/api/admin/ai-tokens`, which is
 * admin-only, so a normal user opened the FTPS guide, read "use an API token
 * as the password", and had nowhere to get one. The backend route has always
 * been open to every account; only the UI was missing.
 */

/** One token row. The secret is NEVER in here — only the hash is stored. */
export interface ApiToken {
  id: number;
  label: string;
  scopes: string;
  /**
   * The level of each permission the key holds, as the server answers it -
   * `{comments: 'read' | 'rw'}`. A key whose list does not name a permission
   * holds its default, and the server is where that rule lives; absent only
   * from an older server.
   */
  permissions?: Record<string, string>;
  /** Per-token display identities for the audit trail; may be absent. */
  usernames?: string | null;
  created_at?: string;
  last_used_at?: string | null;
  expires_at?: string | null;
}

/** What comes back from a mint. `token` is shown exactly once. */
export interface ApiTokenCreated {
  token: string;
  row: ApiToken;
}

export interface ApiTokenRequest {
  label: string;
  /**
   * Comma-separated verbs. The server CAPS this against the caller's role and
   * their own grants, so asking for more than you have is refused rather than
   * quietly granted — never send `admin`, it is rejected outright here.
   * `comments:rw` lets the key add and delete comments too; without it the
   * key reads them (`write` does not include it).
   */
  scopes?: string;
  expires_in_days?: number;
}
