/**
 * tableEnv — the two things every table needs from its HOST and no view
 * should have to pass by hand: the interface language and the light/dark
 * mode.
 *
 * ⚠ Why an injection and not two props on every table. The admin app draws
 * some thirty tables; each one needs the language (the column menu, "Resize
 * {col}", the pager, the Actions label) and the light/dark mode (the column
 * menu and the Actions menu teleport to <body>, outside the chrome that paints
 * with `.dark`, so they have to be told). Threading two props through thirty
 * call sites is thirty places for one of them to be forgotten — which is how
 * one table ends up with an English column menu inside a Turkish page. The
 * host provides it ONCE (web/src/App.vue); a prop on a table still wins, and
 * with neither the table falls back to English and the browser's own mode.
 */
import { inject, provide, type InjectionKey, type Ref } from 'vue';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';

export interface TableEnv {
  locale: Ref<LocaleCode>;
  theme?: Ref<ThemeMode | undefined>;
}

export const TABLE_ENV: InjectionKey<TableEnv> = Symbol('fe-table-env');

/** Called once by a host, in its root component's setup. */
export function provideTableEnv(env: TableEnv): void {
  provide(TABLE_ENV, env);
}

/** The host's answer, or null. Must be called during `setup()`. */
export function useTableEnv(): TableEnv | null {
  return inject(TABLE_ENV, null);
}
