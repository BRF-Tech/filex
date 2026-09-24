/**
 * appLock — an app plugin's hold on a file, as words and as a refusal.
 *
 * A signing app freezes the document while its signers work on it
 * (`files:lock`, docs/APP-PLUGINS-API.md → "File locks"). Two things reach
 * the browser from that:
 *
 *   • the LISTING says so — `locked: true` + `lock: {plugin, reason, until}`
 *     on the row, with `perm` already capped at `viewer` for everyone, so
 *     the menu hides the write verbs without knowing anything about locks;
 *   • the SERVER says so — rename / move / delete of the file, or of any
 *     folder above it, answer `423 {"error":"locked", plugin, path, reason,
 *     until}`.
 *
 * ⚠⚠ The 423 is the whole reason this file exists. Without it the refusal
 * falls into the generic status map as "Error (423)" — or, worse, gets read
 * as a permission problem and told to the person as "you are not allowed to
 * do this". They ARE allowed; a named app is holding the file until a named
 * date, and that sentence is the only one they can act on. So: one parser,
 * one phrasing, read by every surface that can hit the refusal.
 *
 * ⚠ ONE reader for both facts. The badge on the row, the line in the details
 * panel and the toast after a refusal all take their words from `lockWords`
 * — a second phrasing is how the badge says "locked by sign" while the toast
 * says "permission denied" about the same file.
 */
import type { FileNode } from '../types/FileNode';
import type { AppLock } from '../types/Plugins';
import { labelOf } from './pluginLabel';

export type { AppLock };

/** The lock a listing row carries, or `null`. */
export function lockOf(node: Pick<FileNode, 'locked' | 'lock'> | null | undefined): AppLock | null {
  if (!node || node.locked !== true) return null;
  const l = node.lock;
  if (!l || typeof l !== 'object' || typeof l.plugin !== 'string' || !l.plugin) return null;
  return l;
}

/** Is any row of this selection frozen by an app? */
export function anyLocked(nodes: readonly Pick<FileNode, 'locked' | 'lock'>[]): boolean {
  return nodes.some((n) => lockOf(n) !== null);
}

/** The body of a `423 {"error": "locked", …}`. */
export interface LockedRefusal extends AppLock {
  /** The path the server refused — the file, or the folder above it. */
  path?: string;
}

/**
 * A rejected request that was a LOCK, not a permission problem — `null` for
 * anything else, so a caller can keep its existing error path untouched.
 *
 * ⚠ The status alone is the test that matters (`useFileApi` puts the raw body
 * in `.detail` and never parses it). A body that is missing or unparseable
 * still produces a refusal, with an empty plugin name: "an app is holding
 * this file" beats "Error (423)" even when the server said no more than that.
 */
export function lockedRefusal(err: unknown): LockedRefusal | null {
  const e = err as { status?: number; detail?: string } | null;
  if (!e || e.status !== 423) return null;
  const out: LockedRefusal = { plugin: '' };
  try {
    const body = JSON.parse(String(e.detail ?? '')) as Record<string, unknown>;
    if (typeof body.plugin === 'string') out.plugin = body.plugin;
    if (body.plugin_label && typeof body.plugin_label === 'object') {
      out.plugin_label = body.plugin_label as AppLock['plugin_label'];
    }
    if (typeof body.path === 'string') out.path = body.path;
    if (typeof body.reason === 'string' && body.reason) out.reason = body.reason;
    if (body.reason_text && typeof body.reason_text === 'object') out.reason_text = body.reason_text as AppLock['reason_text'];
    if (typeof body.until === 'string' && body.until) out.until = body.until;
  } catch {
    /* an unreadable body is still a lock — see the note above */
  }
  return out;
}

/** What `lockWords` needs from the caller's locale. */
export interface LockWordsHost {
  t: (key: string, vars?: Record<string, string | number>) => string;
  /** The explorer's own date renderer, so a lock reads on the user's clock. */
  formatDate?: (ms: number | undefined | null, opts?: { time?: boolean }) => string;
  /** The reader's language: which of the reason's languages to show. */
  locale?: string;
}

/**
 * A lock's reason in the reader's language: the app's own words for it
 * (`reason_text`, a manifest message the server kept in every language the
 * app wrote), else the plain `reason`.
 *
 * ⚠ The reason used to be ONE string in the language of whoever took the
 * lock, so a German administrator read the Turkish requester's "imzalar
 * toplanıyor" (v0.43.0 wave 2).
 */
export function lockReasonText(lock: AppLock | null | undefined, locale: string | undefined): string {
  if (!lock) return '';
  return labelOf(lock.reason_text, locale || 'en') || lock.reason || '';
}

/**
 * One sentence for a lock: who holds it, why, until when — with every part
 * that is missing simply left out rather than printed as "undefined".
 */
export function lockWords(lock: AppLock | LockedRefusal | null, host: LockWordsHost): string {
  if (!lock) return '';
  /* The app's own label first, its manifest name only where the server could
     not resolve one. ⚠ The name is an ADDRESS (`sign`); every other screen
     shows the label ("e-Signature"), and a banner that says "sign locked this
     file" hands the person an identifier they have never seen (v0.43.0). */
  const who = labelOf(lock.plugin_label, host.locale || 'en') || lock.plugin || host.t('applock.some_app');
  const reason = lockReasonText(lock, host.locale);
  const base = reason
    ? host.t('applock.held_reason', { app: who, reason })
    : host.t('applock.held', { app: who });
  const until = lockUntilText(lock.until, host);
  return until ? `${base} ${until}` : base;
}

/** " — until 3 Oct 2026", or `''` when the lock has no end. */
export function lockUntilText(until: string | null | undefined, host: LockWordsHost): string {
  if (!until) return '';
  const ms = Date.parse(until);
  if (!Number.isFinite(ms)) return '';
  const when = host.formatDate ? host.formatDate(ms, { time: true }) : new Date(ms).toISOString();
  return host.t('applock.until', { date: when });
}
