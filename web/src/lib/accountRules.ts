/**
 * accountRules — what an account's e-mail address and username may be,
 * checked WHILE the person types.
 *
 * ⚠⚠ The server is the authority (backend/internal/identity.Check for the
 * username, handlers/account_rules.go for both) and refuses the same things
 * in the same words; this is its mirror, so the form can say what is wrong
 * before Save instead of after. A rule added there is added here, or the
 * form lets through what the save then refuses. The reverse — a rule only
 * here — would refuse a name the server accepts, which is worse.
 *
 * Why it exists (release-candidate sweep, 2026-09-21): the profile saved
 * "bu-bir-eposta-degil" as an address and answered "Profil kaydedildi", and a
 * username with "ş" came back, after Save, as the server's raw English
 * (`invalid username: 'ş' is not allowed …`). The Appearance page's theme
 * identifier already said its problem under the box while it was typed; this
 * is that behaviour for the two account fields, used by every form that
 * asks for them (the profile, an administrator adding a user).
 *
 * Returns i18n keys + params, never sentences: the words live in the
 * locale files, in both languages.
 */

/** Username bounds — identity.MinLen / identity.MaxLen. */
export const USERNAME_MIN = 3;
export const USERNAME_MAX = 32;

/**
 * identity.reserved: names an operator or a protocol means something else by.
 * Exported for the test that reads the Go map and fails when the two differ.
 */
export const RESERVED_USERNAMES: ReadonlySet<string> = new Set([
  'admin', 'administrator', 'root', 'filex', 'api', 'www', 'ftp', 'sftp', 'webdav',
  'dav', 's3', 'nfs', 'smb', 'system', 'support', 'help', 'null', 'undefined',
  'anonymous', 'guest', 'nobody',
]);

/** A problem to say: the i18n key under `account.errors.` and its params. */
export interface AccountProblem {
  key: string;
  params?: Record<string, string | number>;
}

/** identity.Normalize: trimmed, lower-case — what the server will store. */
export function normalizeUsername(raw: string): string {
  return raw.trim().toLowerCase();
}

function allowedChar(c: string): boolean {
  return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c === '.' || c === '-' || c === '_';
}

/**
 * Why this username would be refused, null when it would not. The same
 * order as identity.Check, so the first thing said is the thing the server
 * would say.
 */
export function usernameProblem(raw: string, current?: string): AccountProblem | null {
  const name = normalizeUsername(raw);
  // Keeping the name the account already holds is not claiming it: the first
  // administrator holds the reserved "admin" (server identity.ClaimBootstrap),
  // and its settings form must not call its own name refused.
  if (current && name === normalizeUsername(current)) return null;
  if (name === '') return { key: 'usernameEmpty' };
  if (name.includes('@')) return { key: 'usernameAt' };
  // ⚠ Measured in BYTES, as the server measures it (Go's len): "9ş" is three
  // bytes, so the server says "starts with a digit", not "too short", and
  // counting characters here would say the other sentence.
  const bytes = new TextEncoder().encode(name).length;
  if (bytes < USERNAME_MIN) return { key: 'usernameShort', params: { min: USERNAME_MIN } };
  if (bytes > USERNAME_MAX) return { key: 'usernameLong', params: { max: USERNAME_MAX } };
  if (name[0] >= '0' && name[0] <= '9') return { key: 'usernameDigit' };
  for (const c of name) {
    if (!allowedChar(c)) {
      return c === ' ' ? { key: 'usernameSpace' } : { key: 'usernameChar', params: { char: c } };
    }
  }
  if (RESERVED_USERNAMES.has(name)) return { key: 'usernameReserved', params: { name } };
  return null;
}

/**
 * Why this e-mail address would be refused, null when it would not.
 * handlers.validEmailAddress: one `@` with something on both sides, no
 * spaces, no "Name <addr>" form. A dotless domain is fine — `admin@local`
 * is the first administrator's address.
 */
export function emailProblem(raw: string): AccountProblem | null {
  const email = raw.trim();
  if (email === '') return { key: 'emailRequired' };
  if (/[\s<>]/.test(email)) return { key: 'emailInvalid' };
  const at = email.lastIndexOf('@');
  if (at <= 0 || at === email.length - 1 || email.indexOf('@') !== at) return { key: 'emailInvalid' };
  return null;
}

/**
 * The field a server refusal is about (`{error, message, field}` from
 * handlers/account_rules.go), so a form can put the server's sentence — it
 * is already in the reader's language — under the right box.
 */
export function refusalField(err: unknown): { field: string; message: string } | null {
  const data = (err as { response?: { data?: { field?: unknown; message?: unknown } } })?.response?.data;
  if (!data || typeof data.field !== 'string' || typeof data.message !== 'string') return null;
  return { field: data.field, message: data.message };
}
