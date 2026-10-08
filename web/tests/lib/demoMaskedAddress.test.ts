// A public demo's server writes "hidden on the demo" in place of an address it
// will not show (backend handlers/demo_redact.go demoMaskedIP): the audit log's
// address column and target, the Sign-in security page. The panel never prints
// that wire value as it is - it says it in the reader's language.
import { describe, expect, it } from 'vitest';

import tr from '@/locales/tr.json';
import { DEMO_MASKED_ADDRESS, shownAddress } from '@/lib/format';

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

  // An audit target that is a masked address is named in words by the SERVER
  // since 0.54 (handlers/audit_label.go auditTargetLabel →
  // `server.audit.hidden_address`; audit_label_test.go holds it).
});
