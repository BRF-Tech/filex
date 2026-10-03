// A public demo's server writes "hidden on the demo" in place of an address it
// will not show (backend handlers/demo_redact.go demoMaskedIP): the audit log's
// address column and target, the Sign-in security page. The panel never prints
// that wire value as it is - it says it in the reader's language.
import { describe, expect, it } from 'vitest';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { DEMO_MASKED_ADDRESS, shownAddress } from '@/lib/format';
import { auditTargetLabel } from '@/lib/auditLabel';

function lookup(catalogue: Record<string, unknown>) {
  const get = (key: string): unknown =>
    key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], catalogue);
  const te = (key: string) => typeof get(key) === 'string';
  const t = (key: string, values: Record<string, unknown> = {}) =>
    String(get(key) ?? key).replace(/\{(\w+)\}/g, (_, k) => String(values[k] ?? ''));
  return { t, te };
}

describe('the demo mask, said in words', () => {
  it('is the server value', () => {
    expect(DEMO_MASKED_ADDRESS).toBe('hidden on the demo');
  });

  it('shownAddress says the mask in the reader language and leaves an address an address', () => {
    expect(shownAddress(DEMO_MASKED_ADDRESS, tr.demo.hiddenAddress)).toBe('demoda gizli');
    expect(shownAddress('203.0.113.7:51234', tr.demo.hiddenAddress)).toBe('203.0.113.7');
    expect(shownAddress('', tr.demo.hiddenAddress)).toBe('');
    expect(shownAddress(undefined, tr.demo.hiddenAddress)).toBe('');
  });

  it('an audit target that is a masked address is named in words', () => {
    const { t, te } = lookup(tr as Record<string, unknown>);
    const byId = auditTargetLabel('login', DEMO_MASKED_ADDRESS, t, te);
    expect(byId).toContain('demoda gizli');
    expect(byId).not.toContain(DEMO_MASKED_ADDRESS);
    const byName = auditTargetLabel('login', DEMO_MASKED_ADDRESS, t, te, DEMO_MASKED_ADDRESS);
    expect(byName).toContain('demoda gizli');
    expect(byName).not.toContain(DEMO_MASKED_ADDRESS);
    const en_ = lookup(en as Record<string, unknown>);
    expect(auditTargetLabel('login', 'ada@example.com', en_.t, en_.te)).toContain('ada@example.com');
  });
});
