/**
 * NFS exports, as the frontend sees them.
 *
 * ⚠⚠ The `path` a freshly minted export returns IS the credential. NFSv3 cannot
 * authenticate a request without Kerberos, so filex binds the identity to the
 * export path instead: 32 bytes of entropy, shown once, stored hashed. Whoever
 * knows it can mount as that account — which is why it belongs in a config file
 * treated like a password, and why the UI has to say so rather than letting
 * somebody discover it from a mount table.
 */

export interface NFSExport {
  id: number;
  user_id: number;
  api_token_id?: number | null;
  label: string;
  /** Confinement, in the same shape the S3 keys use. */
  storage_name?: string;
  prefix?: string;
  /** Refuses every write through this mount, whatever the account may do. */
  read_only: boolean;
  /** Comma-separated CIDR allow-list. Empty = any address the listener takes. */
  allow_cidrs?: string;
  created_at: string;
  last_used_at?: string | null;
  expires_at?: string | null;
  disabled_at?: string | null;
}

/** Where an NFS client should point, reported by the server. */
export interface NFSConnection {
  /** The FILEX_NFS kill switch. */
  enabled: boolean;
  host: string;
  /**
   * ⚠ 2049 by convention, but filex serves mount AND nfs on this one port and
   * runs no portmapper — so a client needs `port=` and `mountport=` both set to
   * it. A guide that omitted either produces a mount that hangs.
   */
  port: number;
}

/** What minting returns. The path appears exactly once. */
export interface NFSExportCreated extends NFSConnection {
  export: NFSExport;
  path: string;
}
