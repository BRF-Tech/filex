import type { StoreRefusal, StoreSourceRef } from '@/api/appStore';

type T = (key: string, values?: Record<string, unknown>) => string;

/**
 * The codes a store install answers, each with the sentence written for it
 * in the reader's language (locales `appStore.err.*`). One table: the store
 * page and the install dialog read it alike.
 */
export const STORE_CODES = [
  'store_invalid',
  'store_not_allowed',
  'store_key_not_configured',
  'store_unreachable',
  'store_bad_answer',
  'store_signature_invalid',
  'store_source_changed',
  'intent_wrong_instance',
  'intent_unknown',
  'intent_gone',
  'intent_expired',
  'intent_used',
  'intent_invalid',
  'intent_pin_mismatch',
  'intent_version_rollback',
  'intent_session_unknown',
  'license_key_invalid',
] as const;

/**
 * Where an app came from, as a person reads it: "the store <origin>
 * (<repo>)", or - no store - "GitHub directly, without a store (<repo>)".
 * The upgrade review and the store_source_changed refusal say it alike.
 */
export function storeSource(src: Partial<StoreSourceRef> | null | undefined, t: T): string {
  const repo = src?.repo || src?.source_url || '-';
  if (src?.store) return t('appStore.wizard.sourceStore', { store: src.store, repo });
  // No store: its GitHub repository, or an upload / an address.
  return t(src?.repo ? 'appStore.wizard.sourceDirect' : 'appStore.wizard.sourceOther', { repo });
}

function sourceRef(v: string | StoreSourceRef | undefined): StoreSourceRef | null {
  return v && typeof v === 'object' ? v : null;
}

function versionText(v: string | StoreSourceRef | undefined): string {
  return typeof v === 'string' ? v : '';
}

/**
 * The sentence for a store refusal, or '' when the code is not one of
 * STORE_CODES (the caller falls back to its own). A pin mismatch names the
 * pins (a moved tag - `commit` - says so); a version refused names both
 * versions; a source change names both sources and what to do (an app
 * installed without a store is said apart); a link for another filex names
 * both.
 */
export function storeSentence(r: StoreRefusal, t: T): string {
  if (!(STORE_CODES as readonly string[]).includes(r.error)) return '';
  if (r.error === 'intent_pin_mismatch') {
    const fields = (r.detail?.mismatches ?? []).map((m) => m.field);
    const key = fields.includes('commit') ? 'appStore.err.intent_pin_mismatch_commit' : 'appStore.err.intent_pin_mismatch';
    return t(key, { fields: fields.join(', ') || '-' });
  }
  if (r.error === 'intent_version_rollback') {
    return t('appStore.err.intent_version_rollback', { installed: versionText(r.detail?.installed), link: versionText(r.detail?.link) });
  }
  if (r.error === 'store_source_changed') {
    const was = sourceRef(r.detail?.installed);
    const link = sourceRef(r.detail?.link);
    // Installed straight from the same repository, no store: a store's PAID
    // link does not take it under its license - said apart, with the way out.
    const direct = !!was && !was.store && !!was.repo && was.repo.toLowerCase() === (link?.repo ?? '').toLowerCase();
    if (direct) return t('appStore.err.store_source_changed_direct', { version: was.version || '-', repo: was.repo, store: link?.store || '-' });
    return t('appStore.err.store_source_changed', { version: was?.version || '-', installed: storeSource(was, t), link: storeSource(link, t) });
  }
  if (r.error === 'intent_wrong_instance') {
    // FILEX_PUBLIC_URL is set and unusable: every link is refused until the
    // operator fixes it - the sentence names the setting.
    if (r.detail?.public_url_invalid) return t('appStore.err.intent_wrong_instance_public_url');
    return t('appStore.err.intent_wrong_instance', { link: r.detail?.filex_origin || '-', here: r.detail?.this_filex || '-' });
  }
  return t(`appStore.err.${r.error}`);
}
