import type { StoreRefusal, StoreSourceRef } from '@/api/appStore';

type T = (key: string, values?: Record<string, unknown>) => string;

/**
 * Where an app came from, as a person reads it: "the store <origin>
 * (<repo>)", or - no store - "GitHub directly, without a store (<repo>)".
 * The upgrade review says it beside what the link brings.
 */
export function storeSource(src: Partial<StoreSourceRef> | null | undefined, t: T): string {
  const repo = src?.repo || src?.source_url || '-';
  if (src?.store) return t('appStore.wizard.sourceStore', { store: src.store, repo });
  // No store: its GitHub repository, or an upload / an address.
  return t(src?.repo ? 'appStore.wizard.sourceDirect' : 'appStore.wizard.sourceOther', { repo });
}

/**
 * The sentence of a store refusal: the server's own (`message`), written in
 * the reader's language for every code a store route answers - an app's
 * link, a language pack's, a storage plugin's, the trust, the connection, a
 * license key (backend handlers/app_store_words.go, `server.store.*`) - or ''
 * when it sent none (the caller falls back to its own).
 *
 * ⚠⚠ No table of codes here. Until 0.55 this file kept one (STORE_CODES and
 * a sentence per code in the panel's locales) and built the sentence from the
 * code and the detail: a second copy of every sentence, one the command line
 * and an agent never had.
 */
export function storeSentence(r: StoreRefusal): string {
  return typeof r.message === 'string' ? r.message.trim() : '';
}
