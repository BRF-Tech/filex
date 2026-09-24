/**
 * symlink — a link the server will NOT follow, said in words.
 *
 * Issue #34: a symlink to a directory inside a `local` storage root listed as
 * a 0-byte file that would not open. The backend half of the answer landed
 * first: a link whose target is INSIDE the root is now followed, so it comes
 * back as the directory it is and opens normally — nothing here is involved in
 * that case, and nothing here should be. What is left is the other half, which
 * is the half the reporter actually complained about: a link the server
 * refuses to follow still arrives looking like an ordinary file, and clicking
 * it does nothing anybody can explain.
 *
 * ⚠⚠ THE WIRE, AS MEASURED — read this before changing anything below.
 * The driver decides a link's fate in `local.describeLink` and records it as
 * `storage.MetaLinkState` (`followed` / `outside_root` / `broken` /
 * `unresolved`). What reaches the browser is LESS than that, and it is less in
 * two different ways:
 *
 *   1. `followed` never arrives. `handlers/manager.go` attaches the keys only
 *      when `o.Kind == storage.KindSymlink`, and a followed link has already
 *      become `KindDirectory`/`KindFile` by then. That is correct — a followed
 *      link IS its target and needs no explaining — so `symlink: true` on the
 *      wire always means "this one will not open".
 *   2. `link_state` is the EXCEPTION, not the rule. Two projectors build
 *      listing rows:
 *        • `projectFileNodes` (manager.go:1412) — the DB cache, i.e. the
 *          normal, steady-state listing. It emits `symlink: true` and NOTHING
 *          ELSE, because `model.Node` has no column for the state: the reason
 *          is a property of the link right now, and the catalogue stores what
 *          was seen at scan time.
 *        • `projectDriverObjects` (manager.go:826) — the cold-cache / pre-sync
 *          fallback, which reads the driver directly and therefore DOES carry
 *          `link_state`.
 *      So `symlink: true` with no `link_state` is the COMMON case, not an edge
 *      one, and `'unknown'` below is a first-class state with wording of its
 *      own. A design that only handled the three named states would leave the
 *      reporter's own screen — a warm cache — exactly as broken as before.
 *
 * `link_target` (the resolved path a followed directory link carries for the
 * walk's cycle guard) is never serialised at all. It is a server-side identity
 * and no surface should grow a dependency on it.
 *
 * ⚠ The wire `type` stays inside the closed `'file' | 'dir'` union: widening
 * it would be a breaking change for every embedder of `@brftech/filex`, so an
 * unfollowable link is reported as a file and FLAGGED. Everything here reads
 * the flag; nothing here looks at `type`.
 *
 * ⚠ ONE reader, for the same reason `lib/appLock` is one reader: the badge on
 * the row, the line in the details panel and the toast after a refused open
 * all take their words from here. A second phrasing is how a badge saying
 * "Outside" ends up beside a toast saying "Error".
 */
import type { FileNode } from '../types/FileNode';

/**
 * Why a link will not open.
 *
 * The first three are `storage.LinkOutsideRoot` / `LinkBroken` /
 * `LinkUnresolved`. `unknown` is ours: the row says it is a link and the
 * server did not say why — the warm-cache listing (see the note above), or a
 * server newer than this client that named a state we have never heard of.
 * Both deserve the honest general sentence rather than silence.
 */
export type LinkState = 'outside_root' | 'broken' | 'unresolved' | 'unknown';

const NAMED: readonly string[] = ['outside_root', 'broken', 'unresolved'];

/**
 * The state of a row the server will not follow, or `null` for every ordinary
 * row — including a link it DID follow, which arrives as its target and is not
 * a link as far as anyone using it is concerned.
 */
export function linkStateOf(node: Pick<FileNode, 'symlink' | 'link_state'> | null | undefined): LinkState | null {
  if (!node || node.symlink !== true) return null;
  const s = node.link_state;
  return typeof s === 'string' && NAMED.includes(s) ? (s as LinkState) : 'unknown';
}

/** What `linkWords` needs from the caller's locale. */
export interface LinkWordsHost {
  t: (key: string, vars?: Record<string, string | number>) => string;
}

/** The short word on the row, and the sentence behind it. */
export interface LinkWords {
  state: LinkState;
  /** The badge's visible text — two words at most; the row is narrow. */
  badge: string;
  /**
   * What it is, why it will not open, and who can change that — in a sentence,
   * because "Outside" on its own is a second riddle rather than an answer.
   */
  why: string;
}

/**
 * The words for a state.
 *
 * ⚠ BROKEN IS NOT OUTSIDE-OF-ROOT and must never read as if it were. Telling
 * somebody an administrator can allow a link whose target has been deleted
 * sends them to a settings screen that will not help, and leaves the real
 * repair — fix or remove the link on the server — unsaid.
 *
 * ⚠ Literal `t('…')` keys on purpose: `web/tests/i18n/coreKeysUsed.test.ts`
 * scans the source for them and skips anything computed, so a table built with
 * `t('symlink.badge.' + state)` would take these eight strings outside the only
 * check that notices a key nobody ever added to the catalogues.
 */
export function linkWords(state: LinkState | null, host: LinkWordsHost): LinkWords | null {
  switch (state) {
    case 'outside_root':
      return { state, badge: host.t('symlink.badge.outside_root'), why: host.t('symlink.why.outside_root') };
    case 'broken':
      return { state, badge: host.t('symlink.badge.broken'), why: host.t('symlink.why.broken') };
    case 'unresolved':
      return { state, badge: host.t('symlink.badge.unresolved'), why: host.t('symlink.why.unresolved') };
    case 'unknown':
      return { state, badge: host.t('symlink.badge.unknown'), why: host.t('symlink.why.unknown') };
    default:
      return null;
  }
}

/** The one call a view makes: the words for this row, or `null`. */
export function linkWordsFor(
  node: Pick<FileNode, 'symlink' | 'link_state'> | null | undefined,
  host: LinkWordsHost,
): LinkWords | null {
  return linkWords(linkStateOf(node), host);
}

/**
 * Is this row one no surface may try to open?
 *
 * ⚠⚠ THE DECISION, WRITTEN DOWN. An unfollowable link is refused by the
 * CLIENT, before any request, and the refusal is spoken (a toast carrying
 * `why`). The two alternatives were both live bugs:
 *
 *   • doing nothing is issue #34 itself — the row that "just does not open";
 *   • letting the request go is barely better, because the driver answers with
 *     a containment error that surfaces as a generic failure and reads like a
 *     server fault rather than a deliberate, configurable boundary.
 *
 * Refusing here costs nothing the person could have had: the server would
 * refuse it too, and only this side knows the sentence that says why and names
 * the setting that changes it.
 */
export function isUnopenableLink(node: Pick<FileNode, 'symlink' | 'link_state'> | null | undefined): boolean {
  return linkStateOf(node) !== null;
}
