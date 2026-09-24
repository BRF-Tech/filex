import type { ExternalService } from '@/api/types';

/**
 * ONLYOFFICE is switched on and has an address, but filex holds no JWT secret
 * for it.
 *
 * ⚠⚠ The owner's decision (2026-09-22): this state gets a PERSISTENT red
 * warning on the admin Panel and on External services — not a toast, not a
 * dismissible banner — until a secret is set. The save callback is a public
 * route, and the secret is the only thing that tells a genuine save from a
 * forged one (the v0.43.0 security fix). One definition, read by the one
 * component that draws the warning (components/OnlyOfficeSecretAlert.vue).
 */
export function onlyofficeWithoutSecret(items: readonly ExternalService[] | null | undefined): boolean {
  const oo = (items ?? []).find((s) => s.id === 'onlyoffice');
  return !!oo && oo.enabled && !!(oo.url ?? '').trim() && !oo.jwt_secret_set;
}
