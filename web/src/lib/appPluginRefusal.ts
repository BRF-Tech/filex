/**
 * The sentence for a refused app install, upgrade or update, in the reader's
 * language.
 *
 * ⚠ ONE place, for two screens: the install wizard (a refusal it was just
 * answered) and the Apps list (the refusal an update check stored when it
 * could not read an app's source, or undid an automatic update —
 * `update.refusal`, the same wire shape, read by `refusalOf`). Written twice,
 * the list would say a failed update in the server's English while the
 * wizard says the same refusal in Turkish.
 *
 * ⚠ `message` is the server's English, for the log: it is only ever a
 * parameter of `manifest_invalid` (the manifest's own complaint), never the
 * sentence itself.
 */
import type { AppPluginInstallRefusal } from '@/api/appPlugins';

type Translate = (key: string, values?: Record<string, unknown>) => string;

/** Why a fetch failed (wasmplugin.FetchReason*), each with its sentence. */
export const FETCH_REASONS = [
  'bad_repo',
  'manifest_not_found',
  'module_not_found',
  'unreachable',
  'http_status',
  'bad_url',
  'missing_url',
  'too_large',
  'changed',
];

/** The refusal codes with a sentence of their own (wasmplugin.ErrCode*). */
export const REFUSAL_CODES = [
  'permissions_incomplete',
  'permissions_changed',
  'sha256_mismatch',
  'sha256_required',
  'manifest_invalid',
  'signature_required',
  'signature_invalid',
  'name_taken',
  'describe_mismatch',
  'too_large',
  'demo_refused',
  'not_found',
  'incompatible',
  'up_to_date',
];

/** The sentence for err, or null when its code has none (the caller falls back). */
export function refusalSentence(err: AppPluginInstallRefusal, t: Translate): string | null {
  if (err.code === 'fetch_failed') {
    const reason = FETCH_REASONS.includes(err.reason) ? err.reason : 'unreachable';
    return t(`appPlugins.wizard.errors.fetch.${reason}`, {
      where: err.where || '—',
      refs: err.refs.join(', ') || '—',
      status: err.status || '—',
    });
  }
  if (REFUSAL_CODES.includes(err.code)) {
    return t(`appPlugins.wizard.errors.${err.code}`, {
      missing: err.missing.join(', ') || '—',
      message: err.message || '—',
      requires: err.requires || '—',
      filex: err.filex || '—',
    });
  }
  return null;
}
