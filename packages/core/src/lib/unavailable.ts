/**
 * unavailable - an entry the STORAGE COULD NOT ANSWER FOR, said in words.
 *
 * Issue #104. The storage sync drops a row whose object is gone only when the
 * storage itself says "not found". When it answers anything else - a storage
 * plugin that cannot look at a folder, a permission it lacks, a backend error
 * - the row is kept and MARKED: the listing carries `unavailable: true` and
 * the storage's own answer in `unavailable_reason`. The server refuses every
 * operation on it, and on anything inside it, with 409 `ENTRY_UNAVAILABLE`
 * until the storage answers for it again.
 *
 * ⚠ ONE reader, for the reason `lib/symlink` is one: the "!" on the row, the
 * line in the details panel, the toast after a refused open and the words for
 * the server's refusal all take their sentence from here, so a badge and a
 * toast can never disagree about what happened.
 *
 * ⚠ The storage's answer is shown as it came (usually English, sometimes a
 * backend's error text), AFTER our own sentence: ours says what it means and
 * what will happen; the reason is the detail an administrator can act on.
 */
import type { FileNode } from '../types/FileNode';

/** What `unavailableWords` needs from the caller's locale. */
export interface UnavailableWordsHost {
  t: (key: string, vars?: Record<string, string | number>) => string;
}

/** The words for an unavailable entry. */
export interface UnavailableWords {
  /** The badge's visible text: one character, the row is narrow. */
  badge: string;
  /** What it means and what will happen, in a sentence. */
  why: string;
  /** The storage's own answer, '' when it sent none. */
  reason: string;
  /** `why` and `reason` together: a tooltip, an aria-label, a toast. */
  full: string;
}

/** Is this an entry the storage could not answer for? */
export function isUnavailable(node: Pick<FileNode, 'unavailable'> | null | undefined): boolean {
  return !!node && node.unavailable === true;
}

/**
 * The words for an unavailable entry, or `null` for every ordinary row.
 *
 * ⚠ Literal `t('…')` keys on purpose: `web/tests/i18n/coreKeysUsed.test.ts`
 * scans the source for them and skips anything computed.
 */
export function unavailableWordsFor(
  node: Pick<FileNode, 'unavailable' | 'unavailable_reason' | 'type'> | null | undefined,
  host: UnavailableWordsHost,
): UnavailableWords | null {
  if (!isUnavailable(node)) return null;
  const why = node!.type === 'dir' ? host.t('unavailable.why.dir') : host.t('unavailable.why.file');
  const reason = typeof node!.unavailable_reason === 'string' ? node!.unavailable_reason.trim() : '';
  const full = reason ? host.t('unavailable.withReason', { why, reason }) : why;
  return { badge: '!', why, reason, full };
}
