/**
 * dropLimits — what a file request (`/d/<token>`) will take, decided BEFORE a
 * byte is sent.
 *
 * ⚠⚠ Why this exists (QA, 2026-09-21): the page sent every file it was given
 * and let the server refuse it. A PDF-only link handed a .txt uploaded it
 * anyway, got `415 ext_not_allowed` back, and printed the only fallback it had
 * — the app-plugin runtime's "The app returned an error". A person who is not
 * a filex user, sent a link by somebody who asked them for a document, should
 * learn "that type is not accepted" before a transfer starts, not after.
 *
 * The rules are the SERVER's, restated — `handlers/drop.go` stays the
 * authority and refuses whatever gets past this:
 *   · the extension, lower-cased, without its dot, must be in the list
 *     (`extAllowed`; an empty list allows every type);
 *   · a file may be at most `max_file_size_mb` MiB (`MaxFileSizeMB << 20` —
 *     binary megabytes, which is why this is not `formatSize`'s decimal MB);
 *   · one submission carries at most `max_files` files, and never more than
 *     the link has left (`uploads_left`).
 */
import type { PublicDropLimits } from '../types/Public';

/** Why one file is not sent. `too_many` carries how many the submission could take. */
export type DropRefusal =
  | { code: 'ext'; name: string }
  | { code: 'too_large'; name: string; mb: number }
  | { code: 'too_many'; count: number };

export interface DropCheck<F> {
  accepted: F[];
  refused: Array<{ file: F; reason: DropRefusal }>;
}

/** The server's extension test (`extAllowed`). */
export function dropExtAllowed(name: string, allow: readonly string[] | undefined): boolean {
  const list = (allow ?? []).map((e) => e.trim().replace(/^\./, '').toLowerCase()).filter(Boolean);
  if (!list.length) return true;
  const dot = name.lastIndexOf('.');
  const ext = dot > 0 || (dot === 0 && name.length > 1) ? name.slice(dot + 1).toLowerCase() : '';
  return list.includes(ext);
}

/**
 * Split what a person picked into what will be sent and what will not, with
 * the reason for each refusal. The order is kept: the first files that fit are
 * the ones that go, exactly as the no-JavaScript page picks them.
 */
export function checkDropFiles<F extends { name: string; size: number }>(
  files: readonly F[],
  limits: PublicDropLimits | undefined,
  uploadsLeft?: number | null,
): DropCheck<F> {
  const out: DropCheck<F> = { accepted: [], refused: [] };
  const maxMb = limits?.max_file_size_mb ?? 0;
  let room = limits?.max_files && limits.max_files > 0 ? limits.max_files : Infinity;
  if (typeof uploadsLeft === 'number' && Number.isFinite(uploadsLeft)) room = Math.min(room, Math.max(0, uploadsLeft));
  for (const f of files) {
    if (!dropExtAllowed(f.name, limits?.allowed_ext)) {
      out.refused.push({ file: f, reason: { code: 'ext', name: f.name } });
    } else if (maxMb > 0 && f.size > maxMb * 1024 * 1024) {
      out.refused.push({ file: f, reason: { code: 'too_large', name: f.name, mb: maxMb } });
    } else if (out.accepted.length >= room) {
      out.refused.push({ file: f, reason: { code: 'too_many', count: room } });
    } else {
      out.accepted.push(f);
    }
  }
  return out;
}
