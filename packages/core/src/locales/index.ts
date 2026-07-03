/**
 * Locale message catalogue. The string-table is intentionally tiny — no
 * external i18n library, just a `t(key, vars?)` helper exposed via
 * `useLocale`.
 */
import { tr } from './tr';
import { en } from './en';
import type { LocaleCode } from '../types/ExplorerConfig';

export const messages: Record<LocaleCode, Record<string, string>> = { tr, en };

export { tr, en };
