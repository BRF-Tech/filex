// The one answer to "does this server run multi-tenant mode?" (lib/tenancy,
// task #167): the capabilities field `multi_tenant`, and only for a server
// too old to send it, the presence of the Realm field.
import { describe, expect, it } from 'vitest';

import { tenancyOn } from '@/lib/tenancy';

describe('tenancyOn', () => {
  it('is the server’s multi_tenant when it says it, whatever else the answer carries', () => {
    expect(tenancyOn({ multi_tenant: true })).toBe(true);
    expect(tenancyOn({ multi_tenant: false })).toBe(false);
    expect(tenancyOn({ multi_tenant: false, realm: { enabled: true, locked_realm: 'acme' } })).toBe(false);
  });

  it('falls back to the Realm field on a server from before the field', () => {
    expect(tenancyOn({ realm: { enabled: true, locked_realm: null } })).toBe(true);
    expect(tenancyOn({})).toBe(false);
  });

  it('is off with no answer at all', () => {
    expect(tenancyOn(null)).toBe(false);
    expect(tenancyOn(undefined)).toBe(false);
  });
});
