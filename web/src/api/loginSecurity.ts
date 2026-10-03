// Sign-in security — the administrator's side of the sign-in attempt limit.
//
// Contract (backend/internal/api/handlers/login_security.go, read from the
// code, not from a description of it):
//
//   GET   /api/admin/login-security
//   PATCH /api/admin/login-security            partial body, keys of `settings`
//   GET   /api/admin/login-security/locks?scope=&locked=1&limit=
//   POST  /api/admin/login-security/unlock     {scope, subject} | {all: true}
//   GET   /api/admin/login-security/attempts?action=&from=&to=&limit=&offset=
//
// ⚠ Instance-wide: one row of settings for every tenant, so the whole surface
// answers 403 `supertenant_only` to a tenant's administrator. The page shows
// that answer's sentence instead of a form that could not be saved.
import { api } from './client';

/** Where the list of trusted proxies in force came from: the page's setting,
 *  FILEX_TRUSTED_PROXIES, or neither (`auto`, the built-in default). */
export type TrustedProxiesSource = 'setting' | 'env' | 'auto';

export interface LoginSecuritySettings {
  enabled: boolean;
  account_max_fails: number;
  ip_max_fails: number;
  window_seconds: number;
  lock_base_seconds: number;
  lock_max_seconds: number;
  /** Addresses and CIDR networks exempt from the per-address limit. */
  ip_allowlist: string[];
  /**
   * The `login.trusted_proxies` setting as saved; empty = not set here. Its
   * entries are addresses, CIDR networks and the words (`auto`, `loopback`,
   * `private`, `link-local`, `none`).
   */
  trusted_proxies: string[];
}

/**
 * The words a trusted-proxy list takes in whole: `auto` (worked out from where
 * filex runs) and three classes of address, Go's net/netip definitions.
 */
export interface TrustedClasses {
  auto: boolean;
  loopback: boolean;
  private: boolean;
  link_local: boolean;
}

/** The words, in the order the server spells them. */
export const TRUSTED_CLASS_WORDS: Record<keyof TrustedClasses, string> = {
  auto: 'auto',
  loopback: 'loopback',
  private: 'private',
  link_local: 'link-local',
};

/**
 * The `trusted_proxies` list that means exactly these classes and addresses:
 * the words first, then the addresses; `none` when both are empty (an empty
 * list would mean "not set", i.e. the environment's list or the default).
 */
export function trustedProxyList(classes: TrustedClasses, addresses: string[]): string[] {
  const words = (Object.keys(TRUSTED_CLASS_WORDS) as (keyof TrustedClasses)[])
    .filter((k) => classes[k])
    .map((k) => TRUSTED_CLASS_WORDS[k]);
  const list = [...words, ...addresses];
  return list.length ? list : ['none'];
}

export type LoginSecurityNumberField =
  | 'account_max_fails'
  | 'ip_max_fails'
  | 'window_seconds'
  | 'lock_base_seconds'
  | 'lock_max_seconds';

export interface LoginSecurityLimit {
  min: number;
  max: number;
  default: number;
}

/** What `auto` resolved to, and why (GET trusted_proxies_auto). */
export type AutoEnvironment = 'plain' | 'container' | 'host-network' | 'kubernetes' | 'podman-rootless' | 'unknown';

export interface AutoInterface {
  name: string;
  /** The kernel's link kind (veth, macvlan, ipvlan, tun, bridge...), or "physical". */
  kind: string;
  addresses: string[];
  trusted: boolean;
  /** container-network, lan, tunnel, host, link-local-only, down, rootless, other. */
  reason: string;
}

export interface TrustedProxiesAuto {
  /** The list in force uses `auto`. */
  in_use: boolean;
  environment: AutoEnvironment | string;
  /** docker, podman, kubernetes, containerd, container; "" outside a container. */
  runtime: string;
  /** Container networks trusted besides loopback (CIDR). */
  networks: string[];
  /** Never trusted, even inside those networks: the gateways... */
  excluded_gateways: string[];
  /** ...and filex's own addresses. */
  excluded_self: string[];
  interfaces: AutoInterface[];
  /** "" or a code: unreadable, ambiguous (automatic fell back to loopback only). */
  warning: string;
  warning_detail?: string;
  resolved_at: string;
}

/** A peer that sent X-Forwarded-For / X-Real-IP without being trusted. */
export interface UntrustedForwarder {
  address: string;
  first_seen: string;
  last_seen: string;
  count: number;
  /** Not loopback, private or link-local. */
  public: boolean;
  /** An address relayed connections arrive from: a gateway, or filex's own. */
  relay: boolean;
}

export interface LoginSecurityState {
  settings: LoginSecuritySettings;
  limits: Record<LoginSecurityNumberField, LoginSecurityLimit>;
  /** The list in force with `auto` spelled out: the class words first, then the networks and addresses. */
  trusted_proxies_effective: string[];
  trusted_proxies_source: TrustedProxiesSource;
  /** Which words the list in force takes (auto and the classes) - one switch each on the page. */
  trusted_defaults: TrustedClasses;
  /** The addresses and networks of the list in force, without the words. */
  trusted_addresses: string[];
  /** What `auto` resolves to right now, and why. */
  trusted_proxies_auto?: TrustedProxiesAuto;
  /** Peers that sent forwarded addresses without being trusted, the busiest first. */
  untrusted_forwarders?: UntrustedForwarder[];
  /** How many such peers are remembered (the list above is capped). */
  untrusted_forwarders_total?: number;
  /** The address filex sees THIS request coming from. */
  your_ip: string;
  your_ip_allowlisted: boolean;
}

export type LoginSecurityPatch = Partial<LoginSecuritySettings>;

export type LoginScope = 'account' | 'ip';

export interface LoginLock {
  /**
   * The row's own name: stable while the server runs, unique per counter, and
   * telling nothing about the subject. Use it as the row key - on a public
   * demo two locked addresses are both "hidden on the demo" (DEMO_MASKED_ADDRESS).
   */
  id: string;
  scope: LoginScope;
  /** The account identifier, or the address; DEMO_MASKED_ADDRESS on a demo. */
  subject: string;
  fails: number;
  limit: number;
  lock_level: number;
  locked: boolean;
  locked_until?: string;
  /** Seconds until the lock ends, when it is in force. */
  retry_after: number;
  window_start: string;
  last_fail_at: string;
  last_ip?: string;
  last_protocol?: string;
}

/** What the trail records: the limiter's four events, and a change to its settings. */
export type LoginAttemptAction =
  | 'login.failed'
  | 'login.locked'
  | 'login.unlocked'
  | 'login.allowlist_pass'
  | 'login_security.update';

/** The door an administrator's row came through: a session, an admin API key, an MCP tool. */
export type LoginAdminDoor = 'panel' | 'api' | 'mcp';

export interface LoginAttempt {
  id: number;
  action: string;
  identifier?: string;
  ip?: string;
  protocol?: string;
  reason?: string;
  scope?: string;
  /** An administrator's row (an unlock, a settings change): the door they used. */
  via?: string;
  /** A settings change: the fields it changed (the keys of `settings`). */
  changed_fields?: string[];
  at: string;
  metadata?: Record<string, unknown>;
}

export interface LoginAttemptPage {
  items: LoginAttempt[];
  total: number;
}

export const LoginSecurityApi = {
  async get(): Promise<LoginSecurityState> {
    const { data } = await api.get<LoginSecurityState>('/admin/login-security');
    return data;
  },

  async update(patch: LoginSecurityPatch): Promise<LoginSecurityState> {
    const { data } = await api.patch<LoginSecurityState>('/admin/login-security', patch);
    return data;
  },

  async locks(params: { scope?: LoginScope; locked?: boolean; limit?: number } = {}): Promise<LoginLock[]> {
    const query: Record<string, string | number> = {};
    if (params.scope) query.scope = params.scope;
    if (params.locked) query.locked = 1;
    if (params.limit) query.limit = params.limit;
    const { data } = await api.get<{ items: LoginLock[] | null }>('/admin/login-security/locks', {
      params: query,
    });
    return data.items ?? [];
  },

  /** Lifts one lock (and clears its counter and escalation). Resolves to how many were lifted. */
  async unlock(scope: LoginScope, subject: string): Promise<number> {
    const { data } = await api.post<{ unlocked: number }>('/admin/login-security/unlock', {
      scope,
      subject,
    });
    return data.unlocked;
  },

  /** Lifts every lock in force. */
  async unlockAll(): Promise<number> {
    const { data } = await api.post<{ unlocked: number }>('/admin/login-security/unlock', { all: true });
    return data.unlocked;
  },

  async attempts(
    params: { action?: string; limit?: number; offset?: number } = {},
  ): Promise<LoginAttemptPage> {
    const query: Record<string, string | number> = {};
    if (params.action) query.action = params.action;
    if (params.limit) query.limit = params.limit;
    if (params.offset) query.offset = params.offset;
    const { data } = await api.get<{ items: LoginAttempt[] | null; total: number }>(
      '/admin/login-security/attempts',
      { params: query },
    );
    return { items: data.items ?? [], total: data.total ?? 0 };
  },
};
