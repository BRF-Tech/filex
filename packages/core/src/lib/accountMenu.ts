/**
 * The account menu's rows — the host's own, the explorer's settings rows, and
 * the exit — put in order by ONE rule, for every host that draws the avatar.
 *
 * ⚠⚠ Why this is a module. The web's explorer page folds the explorer's "⋯"
 * into its avatar menu (owner, 2026-09-13: *"`...` bölgesini admin
 * dropdown'ının içine alacağız"* — one menu in that corner, not two), and since
 * 2026-09-27 the desktop app's explorer does the same from `config.account`.
 * The order (the host's doors, then the explorer's settings, then Sign out),
 * the `fe:` prefix and the one hairline above each group are the same decision
 * in both places; written twice it would drift the first time either changed.
 *
 * Pure: no Vue, no DOM.
 */

/** One row of the account menu. */
export interface AccountAction {
  key: string;
  label: string;
  /** Draw a hairline above this row. */
  separated?: boolean;
  /**
   * An `actionIcons` key. Absent → the row's own `key` is tried, which is why
   * the explorer's rows (`refresh`, `theme`, `tour`, `shortcut-settings`, …)
   * need no mapping at all: their key IS the glyph's name. A key nothing is
   * drawn for renders an empty box of the same width, so a row with no glyph
   * still lines its label up with the rows that have one.
   */
  icon?: string;
}

/** Who the avatar stands for — the fields `personName` reads, plus a picture. */
export interface AccountPerson {
  name?: string | null;
  display_name?: string | null;
  username?: string | null;
  email?: string | null;
  /** Profile picture — a small data:image/… URI or a URL. */
  avatar_url?: string | null;
  /** `admin` for an administrator — what decides whether a host offers its
   *  console door. Never a security boundary: the server checks again. */
  role?: string | null;
}

/** A row the explorer's toolbar announces for its "⋯" (Toolbar `fe:header-menu`). */
export interface ExplorerMenuRow {
  key: string;
  label: string;
  divider?: boolean;
  disabled?: boolean;
  icon?: string;
}

/**
 * The "⋯" rows whose control the explorer ALSO keeps on screen at every width:
 * the view switcher (list / grid / gallery) and the ⓘ at the end of the
 * breadcrumb row, and the panel toggle at the start of the top bar. An avatar
 * menu that repeated them would be two controls for one job — the owner's rule
 * (*"aynı işlevi yapan iki buton olmaması lazım"*), which the web page applied
 * first (Explore.vue ROWS_WITH_ANOTHER_DOOR) and the explorer applies to its own
 * avatar (FileExplorer `config.account`).
 *
 * ⚠ `inspector` only while the ⓘ is drawn — `config.showInfoPanel !== false`.
 */
export const EXPLORER_DRAWN_ROWS: readonly string[] = ['view-list', 'view-grid', 'view-gallery', 'inspector', 'nav'];

/**
 * The prefix on an explorer row's key inside the account menu, so an explorer
 * row named `settings` one day cannot silently take over the host's own row.
 */
export const EXPLORER_ROW_PREFIX = 'fe:';

/**
 * The menu, in order: `own` (this account's doors — settings, admin), the
 * explorer's settings rows minus the ones the host has another door for
 * (`omit`), then `tail` (Sign out).
 *
 * ⚠ `omit` is a DROP list, not a keep list: a settings row added to the
 * explorer tomorrow has to arrive in the menu by itself, or the menu silently
 * falls behind the product it belongs to.
 *
 * ⚠ The explorer's rows sit BETWEEN the account's doors and the exit: they are
 * neither "who am I" nor "goodbye", and burying them under Sign out would put
 * the one destructive row in the middle of the list.
 */
export function accountMenuRows(
  own: AccountAction[],
  explorer: ExplorerMenuRow[],
  opts: { omit?: Iterable<string>; tail?: AccountAction[] } = {},
): AccountAction[] {
  const omit = new Set(opts.omit ?? []);
  const rows: AccountAction[] = [...own];
  explorer
    .filter((r) => !r.divider && !omit.has(r.key))
    .forEach((r, i) =>
      rows.push({
        key: `${EXPLORER_ROW_PREFIX}${r.key}`,
        label: r.label,
        icon: r.icon || r.key,
        separated: i === 0,
      }),
    );
  for (const r of opts.tail ?? []) rows.push({ ...r, separated: r.separated ?? true });
  return rows;
}

/**
 * The explorer row a menu key names, or `null` when the key is the host's.
 */
export function explorerRowKey(key: string): string | null {
  return key.startsWith(EXPLORER_ROW_PREFIX) ? key.slice(EXPLORER_ROW_PREFIX.length) : null;
}
