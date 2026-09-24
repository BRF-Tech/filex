/**
 * pluginMenu — the file menu's plugin rows, as pure data.
 *
 * `FileExplorer.selectionActionList` splices these between the tags row and
 * the desktop-sync rows. Kept out of the component so the gating can be
 * tested without mounting the explorer: a row is offered only when the
 * feature is on, the selection is a plain listing selection (not the trash,
 * not inside an encrypted folder, not a storage mount row) and the action's
 * `applies` rule accepts it.
 *
 * ⚠ v2: the rule reads the row's `app_state` too, so one app contributes
 * DIFFERENT rows to the same menu depending on the file — "Sign / Fill"
 * where a signature is pending, "Request signatures" where none is. They
 * are ordinary rows beside the built-in verbs; nothing about them is a
 * separate menu or a separate surface.
 */
import type { PluginActionRow, PluginGatedRule } from '../types/Plugins';
import { appliesToNodes, type AppliesNodeLike } from './pluginApplies';
import { labelOf } from './pluginLabel';

/** The `ContextAction` fields a plugin row fills (the menu's own type owns the rest). */
export interface PluginMenuRow {
  key: string;
  label: string;
  icon?: string;
  danger?: boolean;
  divider?: boolean;
  /** Greyed: offered once something the server lacks is there (`title` says what). */
  disabled?: boolean;
  title?: string;
}

export interface PluginMenuContext {
  locale: string;
  /** The trash view is open — plugins never run on trashed rows. */
  trash?: boolean;
  /** Inside an E2E-encrypted folder — the server would only see ciphertext. */
  e2e?: boolean;
  /**
   * The selection sits on a read-only storage. An action whose result is
   * WRITTEN there (`output_mode` `sibling` / `version`) is then not offered —
   * see `pluginActionWrites`.
   */
  readOnly?: boolean;
  /**
   * The caller's level on one selected row (`none` | `viewer` | `editor` |
   * `owner`), as the explorer's own write gate reads it (`rowPerm`). An
   * action the level cannot run is not offered — see `pluginActionNeed`.
   * Omitted, or any other answer (`''`, undefined: no ACL on that storage),
   * leaves the row ungated; the server enforces either way.
   */
  permOf?: (node: PluginMenuNode) => string | undefined;
  /** Which manifest icon names have a glyph; others fall back to `plugin`. */
  hasIcon?: (name: string) => boolean;
  /**
   * The sentence for a greyed row (an action's `gated` rule): what the server
   * lacks, in the reader's language. Absent, gated rows are not drawn.
   */
  needWords?: (need: PluginGatedRule['needs']) => string;
}

/** What the menu reads of a selected row. */
export type PluginMenuNode = AppliesNodeLike & {
  e2e?: boolean;
  mime_type?: string | null;
  /** The app holding the file read-only, if one does (listing `lock`). */
  lock?: { plugin?: string } | null;
};

/**
 * The Convert app's manifest name (BRF-Tech/filex-convert). Its actions
 * replace the legacy iframe converter — see serviceGate `legacyConvertGate`.
 */
export const CONVERT_APP = 'convert';

/** Whether the Convert app has any action on this instance for this caller. */
export function convertAppOffered(actions: readonly Pick<PluginActionRow, 'plugin'>[]): boolean {
  return actions.some((a) => a.plugin === CONVERT_APP);
}

/** The menu key for an action row: the server's, or `plugin:<plugin>/<id>`. */
export function pluginActionKey(a: Pick<PluginActionRow, 'plugin' | 'id' | 'key'>): string {
  return a.key || `plugin:${a.plugin}/${a.id}`;
}

/** Whether a menu key names a plugin action. */
export function isPluginActionKey(key: string): boolean {
  return key.startsWith('plugin:');
}

/**
 * Whether running the action writes next to or over its file: its manifest
 * output is `sibling` (a new file beside it) or `version` (a new version of
 * it). The server's own test is the same one (handlers/app_plugins.go
 * `authorise` → `jobOutputMode`), and it refuses those on a read-only storage
 * with `409 read_only`.
 *
 * ⚠⚠ Why the menu asks at all (QA, 2026-09-21): on a read-only drive
 * "Dönüştür…", "İmzala…" and "İmza iste…" were all offered, and the first
 * one answered the click with a 409 and a toast that was gone in 2.5 s — for
 * the person it did nothing. An action that can only be refused is not
 * offered, the same rule the built-in verbs follow (Rename / Delete / Move
 * are not in the menu of a read-only row either).
 *
 * ⚠ `none` answers false, and that is the limit of what the menu can know:
 * an action with no file output may still write through the app's own
 * permissions ("İmza iste…" starts a flow whose LAST step writes the signed
 * copy). The app has to refuse that itself, in words, and the host's refusal
 * path (useFileApi `refusalMessage`) says a `read_only` answer in words
 * whichever surface it comes back through.
 */
export function pluginActionWrites(a: Pick<PluginActionRow, 'output_mode'> & { applies?: Pick<PluginActionRow['applies'], 'writable'> }): boolean {
  return a.output_mode === 'sibling' || a.output_mode === 'version' || a.applies?.writable === true;
}

/**
 * Not offered on a read-only storage: an action that writes there — unless
 * its result may go into a folder the person chooses (`output_elsewhere`),
 * whose screen then asks where.
 */
function refusedReadOnly(a: PluginActionRow): boolean {
  return pluginActionWrites(a) && a.output_elsewhere !== true;
}

const LEVEL_RANK: Record<string, number> = { none: 0, viewer: 1, editor: 2, owner: 3 };

/**
 * The level a caller needs on every input to run the action — the server's
 * `pluginACLNeed` (handlers/app_plugins.go), mirrored: `viewer` to read,
 * `editor` once the job writes its result back, and whatever higher floor the
 * manifest's `min_role` declares.
 *
 * ⚠⚠ Why the menu asks (v0.43.0 wave 2, 2026-09-22): the menu never read
 * `min_role` at all. A person granted `viewer` on an RBAC storage was offered
 * "Dönüştür…", "İmzala…" and "İmza iste…" on every file there, and each one
 * answered the click with `403 insufficient permission` — the same "offered,
 * then refused" the read-only rule above already removed. The built-in verbs
 * follow the level (Rename / Delete are not in a viewer's menu); the app's
 * rows now do too.
 */
export function pluginActionNeed(a: Pick<PluginActionRow, 'output_mode' | 'min_role'>): 'viewer' | 'editor' | 'owner' {
  if (a.min_role === 'owner') return 'owner';
  if (a.min_role === 'editor' || pluginActionWrites(a)) return 'editor';
  return 'viewer';
}

/**
 * Whether the caller's level on `node` lets them run `a`.
 *
 * ⚠ A row the SAME app has locked is not judged by its listed level: the
 * listing caps a locked file at `viewer` for everyone (app_plugins_badges.go),
 * which is the lock speaking, not the person's grant — and the app holding it
 * is exactly the one that must still reach it ("Sign / Fill" on a document
 * frozen while signatures are collected). The server decides that row.
 */
function levelAllows(a: PluginActionRow, node: PluginMenuNode, permOf: (n: PluginMenuNode) => string | undefined): boolean {
  if (node.lock?.plugin && node.lock.plugin === a.plugin) return true;
  const level = permOf(node);
  if (level === undefined || !(level in LEVEL_RANK)) return true;
  return LEVEL_RANK[level] >= LEVEL_RANK[pluginActionNeed(a)];
}

/** The action rows whose rule accepts this selection. */
export function pluginActionsFor(
  actions: readonly PluginActionRow[],
  selection: readonly AppliesNodeLike[],
): PluginActionRow[] {
  if (selection.length === 0) return [];
  // ⚠ `a.plugin` is not decoration: `applies.state` names the plugin's OWN
  // keys while the rows carry them namespaced, so the matcher has to know
  // whose keys to look for (lib/pluginApplies → stateKeysOf).
  return actions.filter((a) => appliesToNodes(a.applies, selection as AppliesNodeLike[], a.plugin));
}

/**
 * Menu rows for a selection, or `[]`.
 *
 * ONE GROUP PER APP, each behind its own divider. The first divider keeps its
 * old key, `sep-plugins`, so the block still reads as its own group under
 * Tags; every further app opens with `sep-plugin:<plugin>`.
 *
 * ⚠⚠ Why per app and not one block. The owner, 2026-09-21, looking at a PDF's
 * right-click menu with the converter and the signing app both installed:
 * "dönüştür ve imzalama pluginlerinin menüleri — yani her plugin'in menüleri —
 * arasına çizgi çekelim". Eight rows from two apps in one undivided run read
 * as one app's vocabulary, and "Convert to DOCX" sitting directly above
 * "Request signatures" invited the question of which app would do what. The
 * divider is the menu's own mechanism (`ContextAction.divider`), so the right
 * click, the selection bar's "⋯" and the ⋮ all draw the same lines, and
 * `ContextMenu` still collapses a divider that ends up leading, trailing or
 * doubled.
 *
 * ⚠ Grouped by `plugin`, in the order each app's FIRST row arrived — not
 * alphabetically and not by re-sorting the rows. The server lists an app's
 * actions in manifest order and the app author chose that order; a stable
 * grouping keeps it inside each app and only pulls apart rows that were
 * interleaved (which the server does not do today, but a grouping that
 * depended on it would break silently the day it did).
 */
export function pluginMenuRows(
  actions: readonly PluginActionRow[],
  selection: readonly PluginMenuNode[],
  ctx: PluginMenuContext,
): PluginMenuRow[] {
  if (ctx.trash || ctx.e2e) return [];
  if (selection.some((n) => n.e2e === true || n.mime_type === 'inode/storage')) return [];
  const permOf = ctx.permOf;
  const allowed = (a: PluginActionRow) =>
    !(ctx.readOnly && refusedReadOnly(a)) && (!permOf || selection.every((n) => levelAllows(a, n, permOf)));
  const offered = pluginActionsFor(actions, selection).filter(allowed);
  // ⚠ Greyed WITH the reason — the owner's rule for anything that depends on
  // how the server is set up ("not configured → disabled with the reason for
  // administrators, hidden for everybody else"). The server sends `gated`
  // to administrators only; here a row the selection would take once the
  // missing piece is installed is drawn disabled, saying what is missing
  // ("LibreOffice is not installed on this server").
  const greyed = new Map<PluginActionRow, string>();
  if (ctx.needWords) {
    for (const a of actions) {
      if (offered.includes(a) || !a.gated?.length || !allowed(a)) continue;
      const gate = a.gated.find((g) =>
        appliesToNodes({ ...a.applies, ext: g.ext ?? [], mime: [] }, selection as AppliesNodeLike[], a.plugin),
      );
      if (gate) greyed.set(a, ctx.needWords(gate.needs));
    }
  }
  const rows = actions.filter((a) => offered.includes(a) || greyed.has(a));
  if (rows.length === 0) return [];
  const hasIcon = ctx.hasIcon ?? (() => false);
  const byApp = new Map<string, PluginActionRow[]>();
  for (const a of rows) {
    const list = byApp.get(a.plugin);
    if (list) list.push(a);
    else byApp.set(a.plugin, [a]);
  }
  const out: PluginMenuRow[] = [];
  let first = true;
  for (const [plugin, list] of byApp) {
    out.push({ divider: true, key: first ? 'sep-plugins' : `sep-plugin:${plugin}`, label: '' });
    first = false;
    for (const a of list) {
      const why = greyed.get(a);
      out.push({
        key: pluginActionKey(a),
        label: labelOf(a.label, ctx.locale),
        icon: a.icon && hasIcon(a.icon) ? a.icon : 'plugin',
        danger: a.danger === true,
        ...(why ? { disabled: true, title: why } : {}),
      });
    }
  }
  return out;
}
