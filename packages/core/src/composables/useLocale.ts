/**
 * useLocale — tr/en string table with a tiny `t()` helper.
 *
 * No i18n library — the catalogue is small enough to ship inline (see
 * src/locales).
 */

import { computed, type Ref } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { messages } from '../locales';

export function useLocale(localeRef: Ref<LocaleCode> | (() => LocaleCode)) {
  const lookup = computed(() => {
    const code = typeof localeRef === 'function' ? localeRef() : localeRef.value;
    return messages[code] ?? messages.tr;
  });

  function t(key: string, vars: Record<string, string | number> = {}): string {
    const raw = lookup.value[key] ?? key;
    return Object.entries(vars).reduce(
      (acc, [k, v]) => acc.replaceAll(`{${k}}`, String(v)),
      raw,
    );
  }

  function formatSize(bytes: number | undefined | null): string {
    if (bytes == null || bytes < 0) return '—';
    // A real zero (empty file / empty folder) is information, not absence.
    if (bytes === 0) return `0 ${t('unit.bytes')}`;
    const units: Array<[number, string]> = [
      [1024 ** 4, 'unit.tb'],
      [1024 ** 3, 'unit.gb'],
      [1024 ** 2, 'unit.mb'],
      [1024, 'unit.kb'],
      [1, 'unit.bytes'],
    ];
    for (const [div, key] of units) {
      if (bytes >= div) {
        const val = bytes / div;
        const rounded = val >= 100 ? Math.round(val) : val.toFixed(val >= 10 ? 1 : 2);
        return `${rounded} ${t(key)}`;
      }
    }
    return `${bytes} ${t('unit.bytes')}`;
  }

  /**
   * Render a FileNode's basename with locale-aware overrides:
   *   - `.trash` directory → "Çöp Kutusu" / "Trash"
   *   - Trash entries are stored as `<Ymd-His>-<rand>__<original>` so
   *     listings inside .trash/ would otherwise show timestamps. Strip
   *     that prefix so the user sees the original basename.
   */
  function nodeDisplayName(node: { basename: string }): string {
    if (node.basename === '.trash') return t('node.trash');
    const m = node.basename.match(/^\d{8}-\d{6}-[A-Za-z0-9]+__(.+)$/);
    if (m) return m[1];
    return node.basename;
  }

  return { t, formatSize, nodeDisplayName };
}
