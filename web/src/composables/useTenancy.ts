/**
 * useTenancy - whether this server runs multi-tenant mode, and who the reader
 * is in it (task #167). The one place a screen of the admin panel asks; the
 * rule and its reasons are in lib/tenancy.
 *
 *   enabled     the mode is in force: anything about tenants may be drawn
 *   realm       the sign-in form's Realm field ({ locked_realm }), or null
 *   operator    the platform operator of a multi-tenant install (Tenants)
 *   tenantAdmin a tenant's own administrator (My tenant)
 *
 * `operator` and `tenantAdmin` are the server's answer too: `caller_admin` is
 * true for an administrator who may configure the instance, which on a
 * multi-tenant install is the platform's own and never a tenant's.
 */
import { computed, type ComputedRef } from 'vue';

import { useAuthStore } from '@/stores/auth';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { tenancyOn } from '@/lib/tenancy';

export interface Tenancy {
  enabled: ComputedRef<boolean>;
  realm: ComputedRef<{ enabled: boolean; locked_realm: string | null } | null>;
  operator: ComputedRef<boolean>;
  tenantAdmin: ComputedRef<boolean>;
}

export function useTenancy(): Tenancy {
  const caps = useCapabilitiesStore();
  const auth = useAuthStore();
  const enabled = computed(() => tenancyOn(caps.data));
  const realm = computed(() => {
    const r = caps.data.realm;
    return enabled.value && r && r.enabled === true ? r : null;
  });
  const operator = computed(() => enabled.value && caps.data.caller_admin === true);
  const tenantAdmin = computed(() => enabled.value && caps.data.caller_admin !== true && auth.isAdmin);
  return { enabled, realm, operator, tenantAdmin };
}
