/**
 * One step of a sign-in provider's test (auth.ProbeCheck), in words. The
 * sentence names what was reached (a host, a DN, an address) and, when a step
 * failed, why - the reason classified by the server (refused, timeout, dns,
 * tls, credentials ...) and said here in the reader's language. The server's
 * own error text is kept for the "technical detail" line, never as the
 * sentence. ONE wording for every page that tests a provider (Admin →
 * Identity providers, a tenant's own providers).
 */
import type { AuthProviderCheck } from '@/api/types';

type T = (key: string, values?: Record<string, unknown>, plural?: number) => string;
type TE = (key: string, locale?: string) => boolean;

/** A field's name in words (`authProviders.fields.<key>`), else the key. */
export function providerFieldLabel(key: string, t: T, te: TE): string {
  const k = `authProviders.fields.${key}`;
  return te(k) || te(k, 'en') ? t(k) : key;
}

export function providerCheckText(c: AuthProviderCheck, t: T, te: TE): string {
  const params: Record<string, string> = { ...(c.params ?? {}) };
  // A failed step the server already worded - the fix and its command, in the
  // reader's language (the operating-system providers) - is the sentence.
  if (c.status === 'fail' && params.hint) return params.hint;
  if (params.fields) {
    params.fields = params.fields
      .split(',')
      .map((k) => providerFieldLabel(k, t, te))
      .join(', ');
  }
  if (params.reason) {
    const k = `authProviders.reasons.${params.reason}`;
    const said = t(k, params);
    params.reason = said === k ? params.reason : said;
  }
  // ⚠ The one env name a check line cannot draw as <code>: it is one text
  // span built from a computed key, so the name is filled as a value instead -
  // out of the translatable sentence either way.
  params.env = 'FILEX_SECRET_KEY';
  const key = `authProviders.checks.${c.id}.${c.status}`;
  // A step that counts ("1000+" past the test's cap) picks the plural by it.
  const n = params.n !== undefined ? parseInt(params.n, 10) : NaN;
  const text = Number.isNaN(n) ? t(key, params) : t(key, params, n);
  return text === key ? `${c.id}: ${c.status}` : text;
}
