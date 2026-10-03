// What the sign-in page says after an SSO sign-in that did not end in a
// session.
//
// The server sends the browser back to the sign-in page (backend
// internal/api/handlers/auth.go, OIDCCallback / OIDCStart) with `?error=oidc`,
// or with `?maintenance=1` when the account may not open a session now - and,
// when the person can do something about it, `&reason=<code>`
// (backend/internal/auth/sso_refusal.go). The page says what happened and what
// to do, in the reader's language (`login.ssoRefused.*`).
//
// ⚠ Only the codes listed here are read. Anything else in `reason` (an address
// somebody edited, a code a newer server knows) gets the generic sentence, and
// the code itself is never put on the screen. An e-mail address with an account
// in another tenant comes back with NO code on purpose - the page must answer
// it exactly as it answers any failure (docs/SSO.md).
//
// The same codes come two other ways, read here too:
//   - a password provider (LDAP, PAM, Windows) whose operator switched
//     show_refusal_reason on answers a refused sign-in 403 `{reason}`
//     (lib/loginRefusal);
//   - the header proxy's refusal rides on the 401 of /api/auth/me
//     (stores/auth `signInReason`).
//
// One place, read by every sign-in page: the admin and drive front doors, the
// realm's page and the desktop app's browser sign-in all render
// views/Login.vue, which asks here.

export const SSO_REFUSAL_REASONS = [
  'auto_create_off',
  'group_not_allowed',
  'no_email',
  'idp_denied',
  'idp_error',
  'expired',
  'account_disabled',
  'tenant_suspended',
  'maintenance',
  'busy',
  'forbidden_account',
  'email_unverified',
  'account_pending',
  'identity_mismatch',
] as const;

export type SsoRefusalReason = (typeof SSO_REFUSAL_REASONS)[number];

/** The generic sentence: any SSO failure the server gave no code for. */
export const SSO_FAILED_KEY = 'login.errOidc';

/** A route query value: a string, or the first of a repeated parameter. */
function first(v: unknown): string | undefined {
  if (Array.isArray(v)) return typeof v[0] === 'string' ? v[0] : undefined;
  return typeof v === 'string' ? v : undefined;
}

function isReason(v: string | undefined): v is SsoRefusalReason {
  return v !== undefined && (SSO_REFUSAL_REASONS as readonly string[]).includes(v);
}

/** A reason code the page knows, from a query value or an answer's body; else null. */
export function ssoRefusalReason(v: unknown): SsoRefusalReason | null {
  const r = first(v);
  return isReason(r) ? r : null;
}

/** The i18n key of a known reason's sentence, or null for anything else. */
export function ssoRefusalKeyOf(v: unknown): string | null {
  const r = ssoRefusalReason(v);
  return r ? `login.ssoRefused.${r}` : null;
}

/**
 * The i18n key of the sentence for a sign-in page address, or null when the
 * address reports no failed SSO sign-in. `query` is the route's query.
 */
export function ssoRefusalKey(query: Record<string, unknown>): string | null {
  const failed = first(query.error) === 'oidc';
  const held = query.maintenance !== undefined;
  if (!failed && !held) return null;
  return ssoRefusalKeyOf(query.reason) ?? (held ? 'login.ssoRefused.maintenance' : SSO_FAILED_KEY);
}
