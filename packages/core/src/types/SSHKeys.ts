/**
 * The SSH key surface, as the frontend sees it.
 *
 * Mirrors `model.SSHPublicKey` plus the connection facts the endpoint returns
 * with it — the host, the port and the LOGIN NAME, which is the piece people
 * most often get wrong when they type it themselves.
 */

export interface SSHPublicKey {
  id: number;
  user_id: number;
  name: string;
  /** SHA256 fingerprint, base64, without the `SHA256:` prefix. */
  fingerprint: string;
  /** The normalised wire form, `<type> <base64>`. */
  public_key: string;
  created_at: string;
  last_used_at?: string | null;
  /** Set = the key is switched off but still listed. */
  disabled_at?: string | null;
}

/** Where an SSH client should connect, and as whom. */
export interface SSHConnection {
  /** The FILEX_SFTP kill switch. False = the operator turned the port off. */
  enabled: boolean;
  host: string;
  /**
   * ⚠ Not 22 and not the web port. SFTP is raw TCP on a port of its own, and
   * a guide that printed 443 would send every client at a proxy that speaks
   * only HTTP.
   */
  port: number;
  /** The login name: the account's username, or its e-mail when it has none. */
  login: string;
  /** The FTPS endpoint, reported by the same call. */
  ftps?: FTPSFacts;
}

/** What an FTP client needs to be told, computed on the server. */
export interface FTPSFacts {
  enabled: boolean;
  host: string;
  port: number;
  /**
   * ⚠ The passive data-port range. A firewall that blocks it makes every
   * transfer HANG with no error on either side — the classic FTP failure, and
   * impossible to diagnose from the client end. It belongs in the guide.
   */
  pasv_min: number;
  pasv_max: number;
  /** True when no certificate was configured and the server generated one. */
  self_signed: boolean;
}
