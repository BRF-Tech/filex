/**
 * A custom role's name and description as the viewer reads them.
 *
 * A role carries its own `name` / `description` (required, the fallback) and,
 * optionally, the same in other interface languages (`names` /
 * `descriptions`, language code → text; backend migration 00071). Everyone
 * sees the role in the language the panel is drawn in. ⚠ THE one place that
 * choice is made on this side — every screen that shows a custom role (the
 * Roles table, the Users list and its filter, a person's page, the delete and
 * move dialog, the permission grid's "role “X”") calls these, never
 * `rule.name` directly. The server's twin is `model.LocalizedText`
 * (PermissionRule.NameFor), which names the role in refusal sentences.
 */

/** What a role needs for its name to be picked; the full PermissionRule fits. */
export interface NamedRole {
  name: string;
  names?: Record<string, string> | null;
  description?: string;
  descriptions?: Record<string, string> | null;
}

function tagOf(locale: string | null | undefined): string {
  return String(locale ?? '')
    .trim()
    .toLowerCase()
    .replace(/_/g, '-');
}

/**
 * `base` as a reader of `locale` sees it: the translation for the locale
 * itself, then for its primary language (`pt-br` reads `pt`), then `base`.
 * A blank translation is none.
 */
export function localizedText(
  base: string,
  texts: Record<string, string> | null | undefined,
  locale: string | null | undefined,
): string {
  if (!texts) return base;
  const tag = tagOf(locale);
  if (!tag) return base;
  const exact = texts[tag]?.trim();
  if (exact) return exact;
  const primary = tag.split('-')[0];
  if (primary !== tag) {
    const p = texts[primary]?.trim();
    if (p) return p;
  }
  return base;
}

/** The role's name in `locale`, else its own name. */
export function roleName(rule: NamedRole | null | undefined, locale: string | null | undefined): string {
  if (!rule) return '';
  return localizedText(rule.name, rule.names, locale);
}

/** The role's description in `locale`, else its own description. */
export function roleDescription(rule: NamedRole | null | undefined, locale: string | null | undefined): string {
  if (!rule) return '';
  return localizedText(rule.description ?? '', rule.descriptions, locale);
}
