/**
 * App permissions on the role and exception editors (backend perm/app.go):
 * what an installed app lets the administrator hand out per role and per
 * person — `app.<app>.<id>`, allow or deny, and when nobody has decided, the
 * app's own default.
 *
 * The SERVER decides every answer; this file only reads, on an editor, what
 * the "Default" choice of a ROLE comes to, so the choice reads "Default
 * (allowed)" rather than a bare "Default". A person's editor does not use it:
 * the server answers that one itself (`effective.apps[].inherited`).
 *
 * ⚠ No rule is worked out here. Which built-in role a CUSTOM role's people
 * are on (perm.HolderRole) is asked of the server (`RolesApi.previewHolder`),
 * and what an app's default comes to on a built-in role (the last layer of
 * perm.Result.AppAllowed) is the catalogue's `default_for`. 0.49.0 kept a copy
 * of both in this file; a change on one side would have made the editor say
 * "allowed" where the server answers 403.
 */
import type { AppPermDef, BuiltinRole, PermEffect } from '@/api/roles';

/**
 * What "Default" comes to for a built-in role's people, given the decisions
 * the built-in role itself has made (`decisions`, what the Roles page edits
 * for User and Viewer): the role's decision, else what the catalogue says the
 * app's default gives that role (`default_for`). `undefined` when the
 * catalogue does not say: the editor then shows a plain "Default", never a
 * guess.
 */
export function builtinAppDefault(
  def: AppPermDef,
  role: BuiltinRole,
  decisions: Record<string, PermEffect> | undefined,
): boolean | undefined {
  const d = decisions?.[def.key];
  if (d === 'allow') return true;
  if (d === 'deny') return false;
  return def.default_for?.[role];
}

/**
 * `builtinAppDefault` for every app permission of the catalogue, keyed by
 * permission — the `appDefaults` a PermissionGrid takes. A permission the
 * catalogue says nothing about is left out ("Default", not a guess).
 */
export function builtinAppDefaults(
  apps: AppPermDef[] | undefined,
  role: BuiltinRole,
  decisions?: Record<string, PermEffect>,
): Record<string, boolean> {
  const out: Record<string, boolean> = {};
  for (const a of apps ?? []) {
    const v = builtinAppDefault(a, role, decisions);
    if (v !== undefined) out[a.key] = v;
  }
  return out;
}

/** How long the custom role editor waits after the last tick before it asks
 *  the server again which built-in role the role's people would be on. */
export const HOLDER_PREVIEW_DELAY_MS = 250;

/** Splits a person's exceptions into the catalogue's and the apps' (app.*). */
export function isAppKey(key: string): boolean {
  return key.startsWith('app.');
}

export function splitOverrides(all: Record<string, PermEffect>): {
  perms: Record<string, PermEffect>;
  apps: Record<string, PermEffect>;
} {
  const perms: Record<string, PermEffect> = {};
  const apps: Record<string, PermEffect> = {};
  for (const [k, v] of Object.entries(all ?? {})) {
    if (isAppKey(k)) apps[k] = v;
    else perms[k] = v;
  }
  return { perms, apps };
}
