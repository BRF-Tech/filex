/**
 * appViewer - which handler opens a file, for every surface that opens files:
 * the explorer's preview, "Open with", and the standalone editor tab
 * (/files/edit). ONE rule, so a file opens in the same place wherever it is
 * opened (0.50: docs/APP-PLUGINS.md → Default apps):
 *
 *  - the handlers of a file are ONLYOFFICE (`onlyoffice`, filex 0.51) for a
 *    kind it opens as a choice (OFFICE_OPEN_KINDS: `.csv`) while it is
 *    configured, then the app interfaces that open it (`viewer` views with a
 *    `ui`, in the server's order: by app name, then the app's own order) and
 *    filex's own viewer, `builtin`, last - backend assoc.OpenDefault;
 *  - the administrator's rule for the file's kind (`open_rules[ext]`, from
 *    the server, backend internal/assoc) puts the handlers it names first, in
 *    its order, and leaves out the ones it switched off; the rest follow in
 *    the default order - exactly assoc.Apply;
 *  - the one that opens it: what "Open with" chose, else the person's own
 *    "always open with" choice (lib/openWith), else the first that is on -
 *    each only when it is on. With every handler off, filex's own viewer
 *    opens it: a file must open somewhere;
 *  - an end-to-end encrypted file (lib/encryptedRow, or a file of a listing
 *    the host knows is encrypted: `inEncrypted`) has ONE handler, filex's own
 *    viewer, which decrypts it in the tab - no app's interface and no
 *    ONLYOFFICE, whatever the rule or the person's choice says (#189). The
 *    same answer from a folder, Recent, Starred, a tag view, a search or
 *    Shared with me;
 *  - except (filex 0.56) where the host can hand the file to an app in the
 *    clear (`e2ePlaintext`: a file of an encrypted folder - not a vault, not
 *    a `.fxe` - that is unlocked in this tab, and the administrator's switch
 *    on): then the app interfaces granted `files:e2e-plaintext`
 *    (lib/appPlaintext) open it too, under the same rule, and filex's own
 *    viewer stays last. Never ONLYOFFICE, never an app without the grant.
 *    Which ones is THE BROWSER'S KIND RULE, `plaintextOpenOf`: decided from
 *    the PLAINTEXT name alone, in this tab - in a folder whose names are
 *    encrypted (level 2) the server cannot read the kind at all, and neither
 *    the name nor its extension is ever sent to it.
 *  - and (filex 0.56) a VAULT's file, its own way (vaultPlaintextViewersFor,
 *    handsOverVault): with the vault open in this tab, the same apps by the
 *    same rule - the kind read HERE, from the name this tab read in the
 *    vault's encrypted index, which the server never sees.
 */
import type { OpenRule, PluginViewRow } from '../types/Plugins';
import { rowIsEncrypted, type EncryptedRowLike } from './encryptedRow';
import { appliesItemOf, appliesMatches, stateKeysOf, type AppliesNodeLike } from './pluginApplies';
import { e2eMimeForExt } from './e2emime';
import { extensionOf } from './e2enames';
import { isOfficeExt } from './serverRules';

/**
 * The grant an app holds to be handed a file of an encrypted folder in the
 * clear (filex 0.56; derived from its manifest's `encrypted_folders`, backend
 * wasmplugin/e2eplain.go). The door itself is lib/appPlaintext.
 */
export const E2E_PLAINTEXT_GRANT = 'files:e2e-plaintext';

/** Whether an app (its view's `ui.grants`) was granted encrypted folders. */
export function takesPlaintext(grants: readonly string[] | null | undefined): boolean {
  return !!grants && grants.includes(E2E_PLAINTEXT_GRANT);
}

/** filex's own viewer, as a handler id (and "Open with"'s choice of it). */
export const BUILTIN_VIEWER = 'builtin';

/** ONLYOFFICE's editor, as a handler id (filex 0.51, backend assoc.OnlyOffice). */
export const ONLYOFFICE_VIEWER = 'onlyoffice';

/**
 * The kinds ONLYOFFICE opens as ONE choice among others: filex's own viewer
 * opens them too (a read-only table for a CSV), so "Open with" offers both.
 * ⚠ The twin of backend assoc `onlyOfficeOpenKinds`; a test holds the two
 * together. The office kinds (docx, xlsx...) are not here: ONLYOFFICE is the
 * only thing that opens them, there is nothing to choose.
 */
export const OFFICE_OPEN_KINDS: readonly string[] = ['csv'];

/** ONLYOFFICE is an open handler of this kind (an extension, no dot). */
export function officeOpensKind(ext: string | null | undefined): boolean {
  return OFFICE_OPEN_KINDS.includes(String(ext ?? '').toLowerCase());
}

/** What only the host knows about a file's handlers. */
export interface OpenHandlerOptions {
  /**
   * ONLYOFFICE is configured and answering here (the capabilities probe, the
   * same answer the office documents' Open reads). Absent or false: it opens
   * nothing, and a choice of it falls through to the next handler.
   */
  onlyOffice?: boolean;
  /**
   * The file sits where the host already knows everything is end-to-end
   * encrypted (an encrypted folder's listing, a vault the tab has open) -
   * for a row that may not say so itself. Only filex's own viewer opens it
   * (onlyBuiltinOpens).
   */
  inEncrypted?: boolean;
  /**
   * filex 0.56: the host can hand this encrypted file to an app in the clear
   * (lib/appPlaintext) - a file of an encrypted folder unlocked in this tab,
   * not a `.fxe`, and the server's switch (capabilities
   * `apps_e2e_plaintext`) on - or (0.56) a vault's file, the vault open in
   * this tab. The app interfaces granted `files:e2e-plaintext` then open it
   * beside filex's own viewer, chosen by its decrypted name
   * (`plaintextOpenOf`) - in a folder whose names are encrypted too, the
   * row's name decrypted in this tab; in a vault, the name read from its
   * index.
   */
  e2ePlaintext?: boolean;
}

type NodeLike =
  | (AppliesNodeLike &
      EncryptedRowLike & {
        type?: string;
        /** A folder with encrypted names (lib/useE2eNames): what the server
         *  stores, and whether `basename` is the decrypted name. */
        e2e_stored?: unknown;
        e2e_name_state?: string;
      })
  | null
  | undefined;

/**
 * Only filex's own viewer opens this file: it is end-to-end encrypted, by its
 * own row (lib/encryptedRow) or by where the host found it (`inEncrypted`).
 * THE rule for every surface that offers an app or ONLYOFFICE a file.
 */
export function onlyBuiltinOpens(node: NodeLike, opts?: OpenHandlerOptions | null): boolean {
  return !!node && node.type === 'file' && (!!opts?.inEncrypted || rowIsEncrypted(node));
}

/** One handler that opens a file: an app's interface, or filex's own (view null). */
export interface OpenHandler {
  id: string;
  view: PluginViewRow | null;
}

/** The handler id of an app's interface. */
export function openHandlerId(v: Pick<PluginViewRow, 'plugin' | 'id'>): string {
  return `app:${v.plugin}/${v.id}`;
}

/**
 * A choice as a handler id. Accepts the spelling before 0.50 too
 * (`plugin/view`, what "Open in new tab" put in `app=`).
 */
export function normalizeOpenChoice(choice: string | null | undefined): string | null {
  if (!choice) return null;
  if (choice === BUILTIN_VIEWER || choice === ONLYOFFICE_VIEWER || choice.startsWith('app:')) return choice;
  return `app:${choice}`;
}

/** The handler is ONLYOFFICE's editor (not an app's interface, not filex's viewer). */
export function isOfficeHandler(h: Pick<OpenHandler, 'id'> | null | undefined): boolean {
  return h?.id === ONLYOFFICE_VIEWER;
}

/** A file's kind: its extension, lower-case, no dot ('' for none). */
export function openKindOf(node: NodeLike): string {
  if (!node) return '';
  return String(appliesItemOf(node).ext ?? '').toLowerCase();
}

/** The `viewer` views that are an app's own interface. */
export function viewerViews(views: readonly PluginViewRow[]): PluginViewRow[] {
  return views.filter((v) => v.placement === 'viewer' && !!v.ui);
}

/** The app interfaces that open this file, in the server's order. */
export function appViewersFor(views: readonly PluginViewRow[], node: NodeLike): PluginViewRow[] {
  if (!node || node.type !== 'file') return [];
  return viewerViews(views).filter((v) => appliesMatches(v.applies ?? {}, [appliesItemOf(node, v.plugin)]));
}

/** What the administrator's rule makes of a file's handlers. */
export interface OpenHandlers {
  /** The handlers that are on, in the order they are offered. */
  on: OpenHandler[];
  /** The ones switched off for the file's kind. */
  off: OpenHandler[];
  /** An administrator's rule decided the order. */
  custom: boolean;
}

/* ── the browser's kind rule (filex 0.56, task #189) ─────────────────── */

/**
 * What the browser knows of a file the server holds only as ciphertext, to
 * choose the app that opens it: its PLAINTEXT name - decrypted in this tab
 * where the folder's names are encrypted - and its row's app state keys
 * (`<plugin>:<key>`, the listing's `app_state`), if any. Nothing of it is
 * ever sent anywhere.
 */
export interface PlaintextFile {
  name: string;
  app_state?: string[];
}

/** What else the choice reads (each optional). */
export interface PlaintextOpenOptions {
  /** The administrator's open rules, by kind (Default apps; capabilities). */
  rules?: Readonly<Record<string, OpenRule>> | null;
  /** What "Open with" chose, a handler id. */
  choice?: string | null;
  /** The person's "always open with" choice for the kind (lib/openWith) -
   *  or a function of the kind, as the kind is known only here. */
  personal?: string | null | ((kind: string) => string | null);
}

/** The handlers of a file chosen by its plaintext name, and the one that opens it. */
export interface PlaintextOpen extends OpenHandlers {
  /** The kind: the plaintext name's extension, lower-case, no dot ('' for none). */
  kind: string;
  /** The handler that opens it (`choice`, else `personal`, else the first on). */
  handler: OpenHandler | null;
}

/** The MIME type the rule reads for a kind: the one the decrypted copy is
 *  given (lib/e2emime) where the kind has one, none otherwise. */
function plaintextMimeOf(kind: string): string {
  const m = e2eMimeForExt(kind);
  return m === 'application/octet-stream' ? '' : m;
}

/** The app interfaces granted files:e2e-plaintext whose `applies` take the file. */
function plaintextApps(views: readonly PluginViewRow[], file: PlaintextFile, kind: string): PluginViewRow[] {
  const mime = plaintextMimeOf(kind);
  return viewerViews(views).filter(
    (v) =>
      takesPlaintext(v.ui?.grants) &&
      appliesMatches(v.applies ?? {}, [{ kind: 'file', ext: kind, mime, state: stateKeysOf({ app_state: file.app_state }, v.plugin) }]),
  );
}

/**
 * filex 0.56 (task #189) - THE BROWSER'S KIND RULE: which handlers open a
 * file the server holds only as ciphertext, and which one opens it, decided
 * from its PLAINTEXT name alone, in this tab.
 *
 * The server holds a file of an encrypted folder only as ciphertext; in a
 * folder whose names are encrypted (level 2) - and in a vault (level 3) - it
 * cannot even read the name, so it cannot tell the kind: the kind is decided
 * here, and neither the name nor its extension is ever sent. The rule is the
 * one every other file opens by (header): the app interfaces granted
 * `files:e2e-plaintext` whose `applies` take the name's extension (and the
 * MIME type it implies, lib/e2emime), in the server's order, filex's own
 * viewer last; the administrator's rule for the kind (`rules[kind]`) orders
 * them and switches them off; the one that opens it is `choice`, else
 * `personal`, else the first that is on. No app takes it: filex's own viewer
 * alone. Never ONLYOFFICE, never an app without the grant.
 *
 * The CALLER has already decided the file may be handed to an app in the
 * clear at all (an unlocked folder, the administrator's switch, the door) -
 * openHandlersFor's `e2ePlaintext`, or a vault's own surface. A name given
 * as a string is the file's whole name.
 */
export function plaintextOpenOf(
  views: readonly PluginViewRow[],
  file: PlaintextFile | string,
  opts?: PlaintextOpenOptions | null,
): PlaintextOpen {
  const f: PlaintextFile = typeof file === 'string' ? { name: file } : file;
  const kind = extensionOf(f.name ?? '');
  const apps = plaintextApps(views, f, kind);
  const handlers: OpenHandlers =
    apps.length === 0
      ? { on: [{ id: BUILTIN_VIEWER, view: null }], off: [], custom: false }
      : ruled([...apps.map((v) => ({ id: openHandlerId(v), view: v })), { id: BUILTIN_VIEWER, view: null }], opts?.rules?.[kind]);
  const personal = typeof opts?.personal === 'function' ? opts.personal(kind) : opts?.personal;
  return { ...handlers, kind, handler: chosen(handlers.on, opts?.choice, personal) };
}

/**
 * The host can hand this file to an app in the clear (`e2ePlaintext`) and its
 * name is known here: never a vault's file (handsOverVault's) or a `.fxe`,
 * whatever the host says, and never a row whose name this tab could not decrypt (a locked
 * folder's placeholder, an unreadable name) - the kind would be a guess.
 */
function handsOverPlaintext(node: NonNullable<NodeLike>, opts?: OpenHandlerOptions | null): boolean {
  if (!opts?.e2ePlaintext || node.type !== 'file') return false;
  if (node.encrypted === 'vault' || node.encrypted === 'file') return false;
  const state = node.e2e_name_state;
  return state === undefined || state === 'enc' || state === 'plain';
}

/** A row as the browser's kind rule reads it: its plaintext name. */
function plaintextFileOf(node: NonNullable<NodeLike>): PlaintextFile {
  return { name: String(node.basename ?? ''), ...(Array.isArray(node.app_state) ? { app_state: node.app_state } : {}) };
}

/**
 * The server cannot read this row's name: it sits in a folder whose names
 * are encrypted (level 2) and the row carries the stored, encrypted one apart
 * (`e2e_stored`, lib/useE2eNames). Its kind is the browser's alone, and
 * nothing that names the kind is sent from it - "Always use this app for
 * .<ext> files" (lib/openWith, the account's choices) is not offered there.
 */
export function nameHiddenFromServer(node: NodeLike): boolean {
  return !!node && typeof node.e2e_stored === 'string';
}

/**
 * The app interfaces that may open an encrypted file in the clear (filex
 * 0.56): granted `files:e2e-plaintext`, and only where the host can hand it
 * over (`e2ePlaintext`) - never a vault's file or a `.fxe`, whatever the host
 * says - chosen by its plaintext name (`plaintextOpenOf`).
 */
export function plaintextViewersFor(views: readonly PluginViewRow[], node: NodeLike, opts?: OpenHandlerOptions | null): PluginViewRow[] {
  if (!node || !handsOverPlaintext(node, opts)) return [];
  const f = plaintextFileOf(node);
  return plaintextApps(views, f, extensionOf(f.name));
}

/**
 * filex 0.56 (task #189) - the app interfaces that may open a VAULT's file
 * in the clear: granted `files:e2e-plaintext`, and only where the host can
 * hand it over (`e2ePlaintext`: the vault open in this tab, the server's
 * switch on). The kind rule (each app's `applies`) is applied here, in the
 * browser, to the file's extension - and the type it implies - as this tab
 * read the name from the vault's encrypted index: the server never learns a
 * vault's names, so it cannot pick the app. Nothing of the file is sent to
 * decide it.
 */
export function vaultPlaintextViewersFor(views: readonly PluginViewRow[], node: NodeLike, opts?: OpenHandlerOptions | null): PluginViewRow[] {
  if (!node || !handsOverVault(node, opts)) return [];
  const f = plaintextFileOf(node);
  return plaintextApps(views, f, extensionOf(f.name));
}

/**
 * The host can hand this VAULT's file to an app in the clear (`e2ePlaintext`:
 * the vault open in this tab, the server's switch on). Its name is the one
 * this tab read in the vault's index - a vault's rows never carry a stored,
 * encrypted name - so the browser's kind rule reads it as it is.
 */
function handsOverVault(node: NonNullable<NodeLike>, opts?: OpenHandlerOptions | null): boolean {
  return !!opts?.e2ePlaintext && node.type === 'file' && node.encrypted === 'vault';
}

/** The handlers of a file, ordered and switched off by the rule for its kind. */
export function openHandlersFor(
  views: readonly PluginViewRow[],
  node: NodeLike,
  rules?: Readonly<Record<string, OpenRule>> | null,
  opts?: OpenHandlerOptions | null,
): OpenHandlers {
  if (!node || node.type !== 'file') return { on: [], off: [], custom: false };
  if (onlyBuiltinOpens(node, opts)) {
    // A vault's file goes its own way (handsOverVault); every other
    // encrypted file the encrypted folders' (handsOverPlaintext). Either
    // way the browser's kind rule picks the app, by the plaintext name.
    const handed = node.encrypted === 'vault' ? handsOverVault(node, opts) : handsOverPlaintext(node, opts);
    if (!handed) return { on: [{ id: BUILTIN_VIEWER, view: null }], off: [], custom: false };
    const { on, off, custom } = plaintextOpenOf(views, plaintextFileOf(node), { rules });
    return { on, off, custom };
  }
  const avail: OpenHandler[] = [
    ...(opts?.onlyOffice && officeOpensKind(openKindOf(node)) ? [{ id: ONLYOFFICE_VIEWER, view: null }] : []),
    ...appViewersFor(views, node).map((v) => ({ id: openHandlerId(v), view: v })),
    { id: BUILTIN_VIEWER, view: null },
  ];
  return ruled(avail, rules?.[openKindOf(node)]);
}

/** The administrator's rule for a kind over the handlers available for it
 *  (backend assoc.Apply): its order first, its switched-off ones out, the
 *  rest in the default order. */
function ruled(avail: OpenHandler[], rule: OpenRule | null | undefined): OpenHandlers {
  if (!rule) return { on: avail, off: [], custom: false };
  const isOff = new Set(rule.off ?? []);
  const byId = new Map(avail.map((h) => [h.id, h]));
  const placed = new Set<string>();
  const on: OpenHandler[] = [];
  for (const id of rule.order ?? []) {
    const h = byId.get(id);
    if (!h || isOff.has(id) || placed.has(id)) continue;
    placed.add(id);
    on.push(h);
  }
  const off: OpenHandler[] = [];
  for (const h of avail) {
    if (isOff.has(h.id)) off.push(h);
    else if (!placed.has(h.id)) on.push(h);
  }
  return { on, off, custom: true };
}

/** The handler that opens the file (see the header for the rule). */
export function pickOpenHandler(
  views: readonly PluginViewRow[],
  node: NodeLike,
  choice?: string | null,
  rules?: Readonly<Record<string, OpenRule>> | null,
  personal?: string | null,
  opts?: OpenHandlerOptions | null,
): OpenHandler | null {
  if (!node || node.type !== 'file') return null;
  return chosen(withoutDeadOfficeViewer(openHandlersFor(views, node, rules, opts).on, node, opts), choice, personal);
}

/**
 * filex 0.57: an office document (docx, xlsx, pptx and the rest the server's
 * `edit_kinds.office` names) opens in filex's own viewer only through a
 * Document Server. With none configured that viewer can only say one is
 * missing, so where an app's interface opens the kind (the Office editor
 * app), filex's own viewer is not chosen - not by the tab's choice, not by a
 * person's or an administrator's earlier pick, not as the first. The same on
 * every surface that asks this rule: the explorer, the editor tab, the
 * desktop app's document windows. With a Document Server, or for a kind
 * filex's own viewer opens by itself (a CSV's table), the handlers stay as
 * they are; it still is in "Open with" either way.
 */
function withoutDeadOfficeViewer(on: OpenHandler[], node: NodeLike, opts?: OpenHandlerOptions | null): OpenHandler[] {
  const kind = openKindOf(node);
  if (opts?.onlyOffice || !isOfficeExt(kind) || officeOpensKind(kind) || !on.some((h) => h.view)) return on;
  return on.filter((h) => h.id !== BUILTIN_VIEWER);
}

/** The handler among those on: the choice, else the person's, else the first. */
function chosen(on: OpenHandler[], choice?: string | null, personal?: string | null): OpenHandler | null {
  const find = (id: string | null) => (id ? (on.find((h) => h.id === id) ?? null) : null);
  return find(normalizeOpenChoice(choice)) ?? find(normalizeOpenChoice(personal)) ?? on[0] ?? null;
}

/** The app interface that opens the file; null is filex's own viewer, or
 *  ONLYOFFICE (pickOpenHandler tells the two apart). */
export function pickAppViewer(
  views: readonly PluginViewRow[],
  node: NodeLike,
  choice?: string | null,
  rules?: Readonly<Record<string, OpenRule>> | null,
  personal?: string | null,
  opts?: OpenHandlerOptions | null,
): PluginViewRow | null {
  return pickOpenHandler(views, node, choice, rules, personal, opts)?.view ?? null;
}
