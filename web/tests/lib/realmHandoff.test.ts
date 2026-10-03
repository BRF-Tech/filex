// The browser half of a sign-in handed to a tenant's own address (#128): the
// ticket travels in the URL fragment of the tenant's sign-in page — the same
// front door and query — and nowhere else.
import { describe, it, expect } from 'vitest';

import { handoffTarget, readHandoffFragment } from '@/lib/realmHandoff';

describe('handoffTarget', () => {
  it('keeps the front door and the query, puts the ticket in the fragment', () => {
    const got = handoffTarget(
      { origin: 'https://files.acme.test', code: 'abc_123' },
      { pathname: '/drive/login', search: '?redirect=%2Fdrive%2F&desktop_state=s' },
      '',
    );
    expect(got).toBe('https://files.acme.test/drive/login?redirect=%2Fdrive%2F&desktop_state=s#handoff=abc_123');
  });

  it('does not repeat the base path the tenant origin already carries', () => {
    const got = handoffTarget(
      { origin: 'https://files.acme.test/filex', code: 'x' },
      { pathname: '/filex/admin/login', search: '' },
      '/filex',
    );
    expect(got).toBe('https://files.acme.test/filex/admin/login#handoff=x');
  });

  it('never navigates to a scheme it did not expect, nor without a ticket', () => {
    const loc = { pathname: '/admin/login', search: '' };
    expect(handoffTarget({ origin: 'javascript:alert(1)', code: 'x' }, loc, '')).toBeNull();
    expect(handoffTarget({ origin: 'not a url', code: 'x' }, loc, '')).toBeNull();
    expect(handoffTarget({ origin: 'https://files.acme.test', code: '' }, loc, '')).toBeNull();
  });

  it('keeps the ticket out of the query and the path', () => {
    const got = handoffTarget({ origin: 'https://t.test', code: 'secret' }, { pathname: '/admin/login', search: '?a=1' }, '')!;
    const u = new URL(got);
    expect(u.search).not.toContain('secret');
    expect(u.pathname).not.toContain('secret');
    expect(u.hash).toBe('#handoff=secret');
  });
});

describe('readHandoffFragment', () => {
  it('reads the ticket, and nothing else', () => {
    expect(readHandoffFragment('#handoff=abc')).toBe('abc');
    expect(readHandoffFragment('#x=1&handoff=abc%2Bd')).toBe('abc+d');
    expect(readHandoffFragment('')).toBeNull();
    expect(readHandoffFragment('#other=1')).toBeNull();
    expect(readHandoffFragment('#nothandoff=1')).toBeNull();
  });
});
