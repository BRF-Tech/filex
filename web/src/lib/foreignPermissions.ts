import type { PermCatalogue, PermKey } from '@/api/roles';
import { isAppKey } from '@/lib/appPermissions';

/**
 * Permissions a LATER filex stored, which this one's catalogue does not know
 * (backend perm/foreign.go).
 *
 * A role or a person's exceptions saved by a newer version and then read here
 * after the server went back to this one may hold a key the editors have no
 * row for. They cannot show it, so they must not drop it: it goes back with
 * every save as it came - through the folder part's filter and a preset
 * applied to the role too - and the server keeps it whatever is sent.
 * ⚠ 0.50 dropped them, and that is how files.encrypt was lost on the way back
 * from 0.51.0 (PR #86).
 */
export function isForeignPerm(key: string, catalogue: Pick<PermCatalogue, 'permissions'> | null | undefined): boolean {
  if (!catalogue || isAppKey(key)) return false;
  return !catalogue.permissions.some((d) => d.key === key);
}

/** The keys of a list the catalogue does not know, in their order. */
export function foreignPerms(keys: PermKey[], catalogue: Pick<PermCatalogue, 'permissions'> | null | undefined): PermKey[] {
  return keys.filter((k) => isForeignPerm(k, catalogue));
}

/**
 * The permissions a role editor showed the administrator: its catalogue.
 * Sent with every save (`shown`), so that a list the save leaves without a
 * permission it showed reads as a decision rather than as one an older
 * version left behind (backend perm.NoteGapsSaved).
 */
export function shownPerms(catalogue: Pick<PermCatalogue, 'permissions'>): PermKey[] {
  return catalogue.permissions.map((d) => d.key);
}
