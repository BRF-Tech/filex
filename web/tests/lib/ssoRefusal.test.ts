// The reason codes a failed SSO sign-in brings back to the sign-in page
// (src/lib/ssoRefusal.ts) are the server's (backend/internal/auth/
// sso_refusal.go), every one of them has a sentence in both languages, and a
// code the page does not know is the generic sentence, never shown as is.
import { describe, it, expect } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { SSO_FAILED_KEY, SSO_REFUSAL_REASONS, ssoRefusalKey, ssoRefusalKeyOf, ssoRefusalReason } from '@/lib/ssoRefusal';

const here = path.dirname(fileURLToPath(import.meta.url));
const AUTH = path.resolve(here, '../../../backend/internal/auth');

/** Every reason code the server can send: the SSOReason* constants, with the two the first-login rule names. */
function serverCodes(): string[] {
  const src = fs.readFileSync(path.join(AUTH, 'sso_refusal.go'), 'utf8');
  const first = fs.readFileSync(path.join(AUTH, 'firstlogin.go'), 'utf8');
  const named: Record<string, string> = {};
  for (const m of first.matchAll(/^\s*(Reason\w+)\s*=\s*"([a-z_]+)"/gm)) named[m[1]] = m[2];
  const out: string[] = [];
  for (const m of src.matchAll(/^\s*SSOReason\w+\s*=\s*(?:"([a-z_]+)"|(Reason\w+))\s*$/gm)) {
    out.push(m[1] ?? named[m[2]]);
  }
  return out;
}

const get = (cat: Record<string, unknown>, key: string): unknown =>
  key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], cat);

describe('SSO refusal reasons', () => {
  it('are exactly the codes the server sends', () => {
    const codes = serverCodes();
    expect(codes.length, 'the scan of sso_refusal.go found nothing').toBeGreaterThan(5);
    expect(codes.every(Boolean), 'a code the scan could not resolve').toBe(true);
    expect([...SSO_REFUSAL_REASONS].sort()).toEqual([...codes].sort());
  });

  it('each has a sentence in English and Turkish', () => {
    for (const r of SSO_REFUSAL_REASONS) {
      for (const [lang, cat] of [['en', en], ['tr', tr]] as const) {
        const v = get(cat as Record<string, unknown>, `login.ssoRefused.${r}`);
        expect(typeof v === 'string' && v.trim() !== '', `${lang}: login.ssoRefused.${r}`).toBe(true);
      }
    }
  });

  it('reads the sign-in page address', () => {
    expect(ssoRefusalKey({})).toBeNull();
    expect(ssoRefusalKey({ signed_out: '1' })).toBeNull();
    expect(ssoRefusalKey({ error: 'oidc' })).toBe(SSO_FAILED_KEY);
    expect(ssoRefusalKey({ error: 'oidc', reason: 'auto_create_off' })).toBe('login.ssoRefused.auto_create_off');
    expect(ssoRefusalKey({ error: 'oidc', reason: ['group_not_allowed', 'x'] })).toBe('login.ssoRefused.group_not_allowed');
    // Held out of the session: the gate's sentence; with no code, maintenance.
    expect(ssoRefusalKey({ maintenance: '1', reason: 'tenant_suspended' })).toBe('login.ssoRefused.tenant_suspended');
    expect(ssoRefusalKey({ maintenance: '1' })).toBe('login.ssoRefused.maintenance');
    // 0.50: which account an SSO sign-in opens.
    expect(ssoRefusalKey({ error: 'oidc', reason: 'email_unverified' })).toBe('login.ssoRefused.email_unverified');
    expect(ssoRefusalKey({ error: 'oidc', reason: 'account_pending' })).toBe('login.ssoRefused.account_pending');
    expect(ssoRefusalKey({ error: 'oidc', reason: 'identity_mismatch' })).toBe('login.ssoRefused.identity_mismatch');
  });

  it('reads a code from an answer body (the password form, /api/auth/me)', () => {
    expect(ssoRefusalReason('forbidden_account')).toBe('forbidden_account');
    expect(ssoRefusalReason('other_tenant')).toBeNull();
    expect(ssoRefusalReason(undefined)).toBeNull();
    expect(ssoRefusalReason(42)).toBeNull();
    expect(ssoRefusalKeyOf('group_not_allowed')).toBe('login.ssoRefused.group_not_allowed');
    expect(ssoRefusalKeyOf('__proto__')).toBeNull();
  });

  it('a code it does not know is the generic sentence', () => {
    for (const reason of ['', 'other_tenant', '__proto__', 'login.errInvalid', '<b>x</b>', null]) {
      expect(ssoRefusalKey({ error: 'oidc', reason }), String(reason)).toBe(SSO_FAILED_KEY);
    }
    // A reason with no failure marker is not a failure.
    expect(ssoRefusalKey({ reason: 'auto_create_off' })).toBeNull();
  });
});
