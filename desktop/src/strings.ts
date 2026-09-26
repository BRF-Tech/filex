// The main process's own two-language strings — the tray menu, native dialogs,
// notifications: what the window's catalogue cannot draw. Each table lives
// beside the code that says it (main.ts TRAY_STRINGS, SYNC_STRINGS, …); the
// one way of reading them is here.
//
// ⚠ No `electron` import, so node:test can drive it.

/** key → [English, Turkish] */
export type Bilingual = Readonly<Record<string, readonly [en: string, tr: string]>>;

/**
 * One string of `table` in `locale`, every `{name}` filled from `vars`.
 *
 * ⚠ One reader for every table: main.ts had it four times over — trayText,
 * syncText, openText and a downloadText — identical but for the table. A key
 * the table lacks is said as the key rather than thrown in the middle of
 * building a menu.
 */
export function bilingual(
  table: Bilingual,
  key: string,
  locale: 'en' | 'tr',
  vars: Readonly<Record<string, string>> = {},
): string {
  const pair = table[key];
  const raw = pair ? (locale === 'tr' ? pair[1] : pair[0]) : key;
  return Object.entries(vars).reduce<string>((acc, [k, v]) => acc.replaceAll(`{${k}}`, v), raw);
}
