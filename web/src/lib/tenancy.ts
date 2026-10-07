/**
 * Multi-tenant mode, as the admin panel reads it: ONE question, one answer
 * (task #167, backend internal/tenancy).
 *
 * ⚠⚠ The answer is the server's capabilities field `multi_tenant` - the mode
 * IN FORCE on the server the page talks to. Nothing about tenants or realms is
 * drawn unless it is true: the Tenants and My tenant pages, the sign-in
 * page's Realm field, the tenant bindings of Identity providers, the tenants'
 * encryption ceilings, the sentences that speak of a tenant's administrator.
 * A screen asks `useTenancy()` (composables/useTenancy) or, outside a
 * component, this function; it never asks the question its own way (an
 * `if` per screen is how one screen ends up showing what the others hide).
 *
 * A server from before 0.53 did not send `multi_tenant`; it sent `realm` on a
 * multi-tenant install and nothing on a single-tenant one, so its absence
 * falls back to that.
 */
import type { Capabilities } from '@/api/types';

export function tenancyOn(caps: Pick<Capabilities, 'multi_tenant' | 'realm'> | null | undefined): boolean {
  if (!caps) return false;
  if (typeof caps.multi_tenant === 'boolean') return caps.multi_tenant;
  return caps.realm?.enabled === true;
}
