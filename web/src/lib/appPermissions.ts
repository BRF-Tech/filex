/**
 * App permissions on the role and exception editors (backend perm/app.go):
 * what an installed app lets the administrator hand out per role and per
 * person — `app.<app>.<id>`, allow or deny, and when nobody has decided, the
 * app's own default.
 *
 * The SERVER decides every answer; this file only says, on an editor, what
 * the "Default" choice of a ROLE would come to, so the choice reads
 * "Default (allowed)" rather than a bare "Default". A person's editor does not
 * use it: the server answers that one itself (`effective.apps[].inherited`).
 */
import type { AppPermDef, AppPermDefault, BuiltinRole, PermDef, PermEffect, PermKey, PermissionRuleInput } from '@/api/roles';

/**
 * The app's default for an account on a built-in role — the last layer of
 * perm.Result.AppAllowed: `viewer` is everybody's, `user` is the User role's
 * (accounts that can change files), `admin` nobody's until granted.
 */
export function appDefaultHolds(def: AppPermDefault, role: BuiltinRole): boolean {
  if (def === 'viewer') return true;
  if (def === 'user') return role === 'user';
  return false;
}

/**
 * What "Default" comes to for a built-in role's people, given the decisions
 * the built-in role itself has made (`decisions`, what the Roles page edits
 * for User and Viewer): the role's decision, else the app's default.
 */
export function builtinAppDefault(
  def: AppPermDef,
  role: BuiltinRole,
  decisions: Record<string, PermEffect> | undefined,
): boolean {
  const d = decisions?.[def.key];
  if (d === 'allow') return true;
  if (d === 'deny') return false;
  return appDefaultHolds(def.default, role);
}

/**
 * The built-in role the people holding a custom role get — perm.HolderRole:
 * User when the role can add, change, delete or share anything (everywhere,
 * or allowed in some folders), otherwise Viewer. A custom role's "Default"
 * for an app permission is that built-in role's answer.
 */
export function holderRole(
  rule: Pick<PermissionRuleInput, 'permissions' | 'effects' | 'conditions'>,
  catalogue: PermDef[],
): BuiltinRole {
  const capped = new Set<PermKey>(catalogue.filter((d) => d.viewer_capped).map((d) => d.key));
  if ((rule.permissions ?? []).some((k) => capped.has(k))) return 'user';
  const hasPlace = (rule.conditions?.storage_ids?.length ?? 0) > 0 || (rule.conditions?.paths?.length ?? 0) > 0;
  if (hasPlace) {
    for (const [k, eff] of Object.entries(rule.effects ?? {})) {
      if (eff === 'allow' && capped.has(k)) return 'user';
    }
  }
  return 'viewer';
}

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
